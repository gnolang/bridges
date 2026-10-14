package hlgnot

import (
	"chain"
	"chain/banker"
	"chain/runtime"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"gno.land/p/nt/testutils/v0"
	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlmailbox"
	"gno.land/r/gov/dao"
	govinit "gno.land/r/gov/dao/init/v0"
)

// The tests drive the real GovDAO (one T1 member) and a 2-of-3 verifier over hlmailbox's signer set; the vectors
// are messages from Solana testnet to this realm's address, signed by the test signers (vectors_test.gno). Test
// time moves 5 s a height. The tests run in order and share state. Who decides follows the chain's own order
// (M1k): the compiled governor makes every decision directly through Govern from TestSetup to TestSettings, then
// TestYield hands over to GovDAO and the tests after run as proposals — pass picks the path by the yield.

const (
	solanaTestnet = uint32(1399811150)
	testVerifier  = namespace + "hltestverifier"
	dayHeights    = 86400 / 5
	solWallet     = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
)

var (
	member  = testutils.TestAddress("hl-member")
	alice   = testutils.TestAddress("hlg-alice")
	bob     = testutils.TestAddress("hlg-bob")
	carol   = testutils.TestAddress("hlg-carol")
	dave    = testutils.TestAddress("hlg-dave")
	erin    = testutils.TestAddress("hlg-erin")
	relayer = testutils.TestAddress("hlg-relayer")
	fees    = chain.PackageAddress(feesPath)

	firstSend string // the id of alice's first send: this route's own, so never refunded (TestSettings, AQ2)
)

type verifier2of3 struct{}

func (verifier2of3) Verify(origin uint32, metadata, message []byte) error {
	ks, rule := hlmailbox.Signers()
	keys := make([][]byte, len(ks))
	for i := range ks {
		k := ks[i]
		keys[i] = k[:]
	}
	return hl.VerifyMultisig(keys, rule.Threshold, metadata, message)
}

func (verifier2of3) Satisfiable(signers []hlmailbox.Key) bool {
	_, rule := hlmailbox.Signers()
	return len(signers) >= rule.Threshold
}

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func idOf(msgHex string) string {
	id := hl.ID(unhex(msgHex))
	return hex.EncodeToString(id[:])
}

func aborts(t *testing.T, want string, f func()) {
	t.Helper()
	r := revive(f)
	if r == nil {
		t.Fatalf("expected an abort containing %q, got none", want)
	}
	msg := ""
	switch e := r.(type) {
	case error:
		msg = e.Error()
	case string:
		msg = e
	}
	if !strings.Contains(msg, want) {
		t.Fatalf("abort %q does not contain %q", msg, want)
	}
}

// pass makes a builder's decision by whoever governs now (M1k): the governor's own Govern before the yield, and
// GovDAO's proposal — created by the member, voted yes, executed — after it. The realm is set in this frame, right
// before the call.
func pass(cur realm, req Request) {
	if hlmailbox.Yielded() == 0 {
		testing.SetRealm(testing.NewUserRealm(launchGovernor))
		Govern(cross(cur), req)
		return
	}
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		panic("the proposal did not execute")
	}
}

// passMailbox is pass for the mailbox's own requests (the launch, the verifier, the seats).
func passMailbox(cur realm, req hlmailbox.Request) {
	if hlmailbox.Yielded() == 0 {
		testing.SetRealm(testing.NewUserRealm(launchGovernor))
		hlmailbox.Govern(cross(cur), req)
		return
	}
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		panic("the proposal did not execute")
	}
}

// refusedByDAO proves GovDAO's proxy cannot make a builder's decision before the yield (M1k): the proposal is
// created and voted, its execution aborts on the governor's rule — a panic, so on chain the proposal survives for
// a retry after the yield — and nothing is recorded.
func refusedByDAO(cur realm, t *testing.T, what string, req Request) {
	t.Helper()
	n := st.Ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlgnot: until the governor yields, "+string(launchGovernor)+" alone decides, by Govern from its own key: not gno.land/r/gov/dao (a GovDAO proposal is refused until the yield)", func() { dao.ExecuteProposal(cross(cur), pid) })
	if st.Ledger.Last() != n {
		t.Fatalf("%s: a refused proposal recorded a decision", what)
	}
}

func asMember(cur realm)  { testing.SetRealm(testing.NewUserRealm(member)) }
func asRelayer(cur realm) { testing.SetRealm(testing.NewUserRealm(relayer)) }
func skipDays(n int64)    { testing.SkipHeights(n*dayHeights + 1) }

func balance(a address) int64 { return banker.NewReadonlyBanker().GetCoin(a, "ugnot") }

// envelope sets up a direct user call from `from` sending exactly amount + fee; the test then calls TransferRemote
// itself (the origin-call assertion counts frames, so no helper may stand between).
func envelope(from address, amount int64) {
	testing.SetRealm(testing.NewCodeRealm(pkgPath)) // the envelope lands on this realm (an earlier code-realm override moved it)
	testing.SetRealm(testing.NewUserRealm(from))
	testing.SetOriginSend(chain.Coins{{"ugnot", amount + st.Fee}})
	testing.IssueCoins(Address(), chain.Coins{{"ugnot", amount + st.Fee}})
}

// fundLock puts the lock's worth of ugnot in the realm's account: the harness's coin table does not survive from
// one test function to the next, while the realm's state does, so each test that pays out starts by restoring it.
func fundLock() { testing.IssueCoins(Address(), chain.Coins{{"ugnot", Locked()}}) }

// inherit hands this realm a predecessor's page carrying one send, as a successor takes it (hlroute.Import): the
// lock and sentTotal rise by its amount (page 0 carries them in a real migration) and its coins are issued; the
// only kind of send a refund may name (AQ2). The first call is page 0, which empties the meters (R34).
func inherit(id string, from address, amount int64) {
	st.Import(now(), 0, hlmailbox.LaunchedAt(), &hlroute.Snapshot{Page: st.Imported(), Lock: amount, SentTotal: amount, Sends: []hlroute.SendItem{{ID: id, From: from, Amount: amount, At: now()}}})
	testing.IssueCoins(Address(), chain.Coins{{"ugnot", amount}})
}

func TestLaunchValues(cur realm, t *testing.T) {
	route, _ := hl.ParseKey32(testRoute)
	if hlmailbox.Live() || Locked() != 0 || Fee() != 5*gnot || Minimum() != 10*gnot || FeeFloor() != 2*gnot || CapFloor() != 10_000*gnot ||
		Maximum() != 25_000*gnot || Domain() != solanaTestnet || Router() != hex.EncodeToString(route[:]) || Cap() != 10_000*gnot || Migrated() != "" {
		t.Fatalf("launch values: fee %d min %d floor %d cap %d max %d router %s", Fee(), Minimum(), FeeFloor(), CapFloor(), Maximum(), Router())
	}
	p := Render("")
	for _, want := range []string{"**not launched**", "fee 5 GNOT (floor 2 GNOT); minimum 10 GNOT; per-message maximum 25000 GNOT", "day cap: 10000 GNOT (floor 10000 GNOT", "router on domain 1399811150: `" + Router() + "`", "No decision yet", "- clock factor 1 (1 on mainnet; a devnet build is compiled with 8): the 7-day delay is 604800 s and the 14-day deadline 1209600 s of block time; the old router's window after a swap is the 7-day delay"} {
		if !strings.Contains(p, want) {
			t.Fatalf("page lacks %q:\n%s", want, p)
		}
	}
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "not launched", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 10*gnot) })
	asRelayer(cur)
	aborts(t, "not launched", func() { Process(cross(cur), inAlice100Meta, inAlice100Msg) })
	asRelayer(cur)
	aborts(t, "not launched", func() { Claim(cross(cur), 1) })
	asMember(cur)
	aborts(t, "not launched", func() { NewSetCapFloorRequest(cross(cur), 12_000*gnot) })
	asMember(cur)
	aborts(t, "is not pending", func() { NewCancelRequest(cross(cur), 1) })
	asRelayer(cur)
	aborts(t, "not launched", func() { Execute(cross(cur), 1) })
}

// TestSetup: GovDAO launches the realms and activates the test verifier.
func TestSetup(cur realm, t *testing.T) {
	asMember(cur)
	govinit.InitWithUsers(cross(cur), member)
	testing.SetRealm(testing.NewCodeRealm(testVerifier))
	hlmailbox.RegisterVerifier(cross(cur), verifier2of3{})
	asMember(cur)
	passMailbox(cur, hlmailbox.NewLaunchRequest(cross(cur)))
	asMember(cur)
	passMailbox(cur, hlmailbox.NewSetVerifierRequest(cross(cur), solanaTestnet, testVerifier))
	skipDays(7)
	asRelayer(cur)
	hlmailbox.Execute(cross(cur), 2)
	if !hlmailbox.Live() || hlmailbox.ActiveVerifier(solanaTestnet) != testVerifier {
		t.Fatalf("setup")
	}
	if !strings.Contains(Render(""), "**live** since") || !strings.Contains(Render(""), "- governor: "+string(launchGovernor)+" (until the yield) — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused\n") || !strings.Contains(Render("json"), "\"live\":true,\"governor\":\""+string(launchGovernor)+"\",\"yielded\":0,") {
		t.Fatalf("page: %s\n%s", Render(""), Render("json"))
	}
}

// TestTransferRemote: a direct user call with exactly amount + fee locks the amount and pays the fee.
func TestTransferRemote(cur realm, t *testing.T) {
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "no route to domain 1", func() { TransferRemote(cross(cur), 1, solWallet, 10*gnot) })
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "below the minimum", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 9*gnot) })
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "above the per-message maximum", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 25_001*gnot) })
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "must be positive", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 0) })
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "all zeros", func() { TransferRemote(cross(cur), solanaTestnet, "11111111111111111111111111111111", 10*gnot) })
	mb, _ := hl.AddressToBytes32(hlmailbox.Address())
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "a realm of the bridge", func() { TransferRemote(cross(cur), solanaTestnet, hex.EncodeToString(mb[:]), 10*gnot) })
	// every route realm the mailbox registers is a realm of the bridge too (R39): a successor's address — hlbubble's
	// here, registered as a migrate's apply registers one — is refused as a recipient, at send and in a credit or
	// a sweep payment by vote, as the six compiled ones are
	succ := namespace + "hlbubble7"
	testing.SetRealm(testing.NewCodeRealm(hlbubblePath))
	hlmailbox.AddRoute(cross(cur), succ)
	sb, _ := hl.AddressToBytes32(chain.PackageAddress(succ))
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "a realm of the bridge", func() { TransferRemote(cross(cur), solanaTestnet, hex.EncodeToString(sb[:]), 10*gnot) })
	if !isRealm(chain.PackageAddress(succ)) || isRealm(alice) {
		t.Fatalf("a registered route's address is not a realm of the bridge")
	}
	testing.SetRealm(testing.NewCodeRealm(namespace + "hltestsender"))
	aborts(t, "a direct user call only", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 10*gnot) })
	testing.SetRealm(testing.NewUserRealm(alice))
	testing.SetOriginSend(chain.Coins{{"ugnot", 100 * gnot}})
	aborts(t, "send exactly 105000000ugnot", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 100*gnot) })
	testing.SetRealm(testing.NewUserRealm(alice))
	testing.SetOriginSend(chain.Coins{{"ugnot", 105 * gnot}, {"foo", 1}})
	aborts(t, "send exactly 105000000ugnot", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 100*gnot) })
	if Locked() != 0 || hlmailbox.Count() != 0 {
		t.Fatalf("a refused send moved something")
	}

	envelope(alice, 100*gnot)
	firstSend = TransferRemote(cross(cur), solanaTestnet, solWallet, 100*gnot)
	if Locked() != 100*gnot || Balance() != 100*gnot || balance(fees) != 5*gnot || hlmailbox.Count() != 1 {
		t.Fatalf("locked %d balance %d fees %d count %d", Locked(), Balance(), balance(fees), hlmailbox.Count())
	}
	rawHex, _ := hlmailbox.Message(0)
	m, _ := hl.ParseMessage(unhex(rawHex))
	self, _ := hl.AddressToBytes32(Address())
	wallet, _ := hl.ParseKey32(solWallet)
	route, _ := hl.ParseKey32(testRoute)
	if m.Sender != self || m.Recipient != route || m.Destination != solanaTestnet || string(m.Body) != string(hl.TokenMessage(wallet, uint64(100*gnot), nil)) || idOf(rawHex) != firstSend {
		t.Fatalf("message %+v", m)
	}
	if sd := st.SendOf(firstSend); sd == nil || sd.From != alice || sd.Amount != 100*gnot {
		t.Fatalf("send record %+v", sd)
	}
	// two more sends build the lock for the installments later
	envelope(bob, 20_000*gnot)
	TransferRemote(cross(cur), solanaTestnet, solWallet, 20_000*gnot)
	envelope(bob, 20_000*gnot)
	TransferRemote(cross(cur), solanaTestnet, solWallet, 20_000*gnot)
	if Locked() != 40_100*gnot || balance(fees) != 15*gnot || Cap() != 10_000*gnot {
		t.Fatalf("locked %d fees %d cap %d", Locked(), balance(fees), Cap())
	}
	if !strings.Contains(Render("json"), "\"lock\":40100000000,\"sentTotal\":40100000000") {
		t.Fatalf("json: %s", Render("json"))
	}
}

// TestProcessAndClaim: a burn comes back as a claim; the meters start empty at launch; a claim pays in turns with
// the fee first; a burn below the fee or to a realm address is swept; a claim above the maximum pays in installments.
func TestProcessAndClaim(cur realm, t *testing.T) {
	aid := idOf(inAlice100Msg)
	fundLock()
	asRelayer(cur)
	Process(cross(cur), inAlice100Meta, inAlice100Msg)
	c := st.ClaimOf(aid)
	if c == nil || c.Amount != 100*gnot || c.State != hlroute.Open || c.By != hlroute.BySigners || c.To != alice || st.Owed != 100*gnot {
		t.Fatalf("claim %+v owed %d", c, st.Owed)
	}
	asRelayer(cur)
	aborts(t, "already delivered", func() { Process(cross(cur), inAlice100Meta, inAlice100Msg) })
	asRelayer(cur)
	aborts(t, "not from the router", func() { Process(cross(cur), strangerRouteMeta, strangerRouteMsg) })
	// a message the router addressed to another realm (AQ1): a successor takes its predecessor's messages once the
	// last page of the migration is in; this route succeeded nobody, so it is refused here, before the mailbox is
	// asked (the mark would stand in this harness) and whoever it is for is none of this route's business
	asRelayer(cur)
	aborts(t, "addressed to another realm: a successor takes its predecessor's messages once the last page of the migration is in (0 pages so far)", func() {
		Process(cross(cur), inOtherRealm60Meta, inOtherRealm60Msg)
	})
	if hlmailbox.Delivered(idOf(inOtherRealm60Msg)) != 0 {
		t.Fatalf("a refused message was marked delivered")
	}
	asRelayer(cur)
	Process(cross(cur), inBob3Meta, inBob3Msg)
	asRelayer(cur)
	Process(cross(cur), inFees50Meta, inFees50Msg)
	if st.Sweep != 53*gnot || st.SweptOf(idOf(inBob3Msg)) != 3*gnot || st.SweptOf(idOf(inFees50Msg)) != 50*gnot || st.Owed != 153*gnot {
		t.Fatalf("sweep %d owed %d", st.Sweep, st.Owed)
	}
	if !strings.Contains(Render("claim/"+idOf(inBob3Msg)), "\"swept\":3000000") || !strings.Contains(Render("claim/"+aid), "\"state\":\"open\"") || Render("claim/zz") != "null" {
		t.Fatalf("claim pages")
	}
	if !strings.HasPrefix(Render("claims/0"), "[{\"id\":\""+aid+"\"") || Render("claims/1") != "[]" || Render("claims/x") != "[]" ||
		!strings.Contains(Render("sweeps/0"), "\"id\":\""+idOf(inBob3Msg)+"\",\"swept\":3000000") || Render("sweeps/2") != "[]" {
		t.Fatalf("list pages: %s %s", Render("claims/0"), Render("sweeps/0"))
	}
	// the meters are empty at launch (the realm was deployed earlier): nothing pays at once
	asRelayer(cur)
	if n := Claim(cross(cur), 20); n != 0 {
		t.Fatalf("paid from an empty meter")
	}
	a0, f0 := balance(alice), balance(fees)
	skipDays(1)
	asRelayer(cur)
	if n := Claim(cross(cur), 20); n != 1 {
		t.Fatalf("paid %d claims", n)
	}
	if balance(alice)-a0 != 95*gnot || balance(fees)-f0 != 5*gnot || Locked() != 40_000*gnot || st.PaidTotal != 100*gnot || st.Owed != 53*gnot || st.ClaimOf(aid).State != hlroute.Done {
		t.Fatalf("alice +%d fees +%d locked %d paid %d owed %d", balance(alice)-a0, balance(fees)-f0, Locked(), st.PaidTotal, st.Owed)
	}
	// a claim above the per-message maximum is credited and paid in installments: a quarter of the cap a day
	asRelayer(cur)
	Process(cross(cur), inAlice30000Meta, inAlice30000Msg)
	big := idOf(inAlice30000Msg)
	skipDays(1)
	a0 = balance(alice)
	asRelayer(cur)
	Claim(cross(cur), 20)
	if balance(alice)-a0 != 2_495*gnot || st.ClaimOf(big).Paid != 2_500*gnot || st.ClaimOf(big).Remaining() != 27_500*gnot {
		t.Fatalf("installment: alice +%d paid %d", balance(alice)-a0, st.ClaimOf(big).Paid)
	}
	if !strings.Contains(Render(""), string(alice)+" #1: 27500 GNOT of 30000 GNOT open (signers)") {
		t.Fatalf("page: %s", Render(""))
	}
}

// TestVoidAndFlags: a guardian's payouts flag pauses Claim and names the ids GovDAO voids at once; the rest wait;
// andUnpause reopens payouts in the same closure and restarts the meters; a watch key's flag unlocks nothing.
func TestVoidAndFlags(cur realm, t *testing.T) {
	cid, did := idOf(inCarol1000Msg), idOf(inDave1000Msg)
	fundLock()
	asRelayer(cur)
	Process(cross(cur), inCarol1000Meta, inCarol1000Msg)
	asRelayer(cur)
	Process(cross(cur), inDave1000Meta, inDave1000Msg)
	g1, w5 := hlmailbox.Seat(1), hlmailbox.Seat(5)
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, cid)
	asRelayer(cur)
	aborts(t, "payouts are paused", func() { Claim(cross(cur), 20) })
	if !strings.Contains(Render(""), "**paused**: payouts") {
		t.Fatalf("page: %s", Render(""))
	}
	asMember(cur)
	aborts(t, "no claim", func() { NewVoidRequest(cross(cur), strings.Repeat("cd", 32), false, "", "") })
	asMember(cur)
	aborts(t, "sends, credits and payouts", func() { NewVoidRequest(cross(cur), cid, false, "claims", "") })
	asMember(cur)
	aborts(t, "must be 1 to 6", func() { NewVoidRequest(cross(cur), cid, false, "", "7") })
	// the unpause option vacates two guardian seats at most (§5.3's cap, as rotate seats has it); both watch keys
	// beside them are fine
	asMember(cur)
	aborts(t, "two guardian seats at most", func() { NewVoidRequest(cross(cur), cid, false, "", "1,2,3") })
	asMember(cur)
	aborts(t, "two guardian seats at most", func() { NewVoidRequest(cross(cur), cid, false, "", "5,6,4,3,2") })
	asMember(cur)
	if req := NewVoidRequest(cross(cur), cid, false, "", "5,6,1,2"); !strings.Contains(req.Description(), "removing seats 5:"+string(hlmailbox.Seat(5))+",6:"+string(hlmailbox.Seat(6))+",1:"+string(hlmailbox.Seat(1))+",2:"+string(hlmailbox.Seat(2))) {
		t.Fatalf("description: %s", req.Description())
	}
	asMember(cur)
	pass(cur, NewVoidRequest(cross(cur), cid+","+did, false, "payouts", ""))
	d := st.Ledger.Get(st.Ledger.Last())
	if d.Kind != hlroute.KindVoid || !d.Queued() || !strings.Contains(d.Note, "1 voided at once") || !strings.Contains(d.Note, "unpaused payouts") || !strings.Contains(d.Note, "1 queued") {
		t.Fatalf("void decision %+v", d)
	}
	if st.ClaimOf(cid).State != hlroute.Withheld || st.ClaimOf(did).State != hlroute.Open || st.Voided != 1_000*gnot || hlmailbox.Paused(pkgPath, hlmailbox.FnPayouts) {
		t.Fatalf("after the void: carol %s dave %s voided %d", st.ClaimOf(cid).State, st.ClaimOf(did).State, st.Voided)
	}
	// payouts reopened and the meters restarted empty: nothing pays now, a day's worth tomorrow
	asRelayer(cur)
	if n := Claim(cross(cur), 20); n != 0 {
		t.Fatalf("the meters did not restart")
	}
	d0, a0 := balance(dave), balance(alice)
	skipDays(1)
	asRelayer(cur)
	Claim(cross(cur), 20)
	if balance(dave)-d0 != 995*gnot || balance(alice)-a0 != 2_500*gnot {
		t.Fatalf("a day after the restart: dave +%d alice +%d", balance(dave)-d0, balance(alice)-a0)
	}
	// the queued void of dave's id applies after 7 days — dave's claim is paid in full by then, which a void as
	// withheld cannot take: the id is skipped and the note says so (R22), never a panic that leaves the proposal
	// pending
	skipDays(6)
	asRelayer(cur)
	Execute(cross(cur), d.ID)
	if d.State() != "applied" || d.Note != "applied after the delay; 0 voided, 1 skipped (not voidable as it stands)" || st.ClaimOf(did).State != hlroute.Done {
		t.Fatalf("the queued void: %+v", d)
	}
	// a watch key's payouts flag pauses, names nothing and unlocks nothing: a void under it waits
	testing.SetRealm(testing.NewUserRealm(w5))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, "")
	asMember(cur)
	pass(cur, NewVoidRequest(cross(cur), did, true, "", ""))
	d2 := st.Ledger.Get(st.Ledger.Last())
	if !d2.Queued() || st.ClaimOf(did).State != hlroute.Done {
		t.Fatalf("a watch key's flag unlocked a void: %+v", d2)
	}
	testing.SetRealm(testing.NewUserRealm(w5))
	hlmailbox.ClearFlag(cross(cur), pkgPath, hlmailbox.FnPayouts)
	// GovDAO cancels it at once
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), d2.ID))
	if d2.State() != "cancelled" {
		t.Fatalf("not cancelled")
	}
	// carol's withheld claim: restored at once, then voided as forged under a flag, restored after 7 days
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), cid))
	if st.ClaimOf(cid).State != hlroute.Open || st.Voided != 0 {
		t.Fatalf("restore of a withheld claim")
	}
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, cid)
	testing.SetRealm(testing.NewUserRealm(w5))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnCredits, "")
	// §1's leak ending in one closure: the void under the flag, payouts and credits reopened, the watch key retired
	asMember(cur)
	pass(cur, NewVoidRequest(cross(cur), cid, true, "payouts,credits", "5"))
	// carol's 1,000 is dropped from owed; what stays owed is alice's 25,000 remainder and the 53 swept
	if st.ClaimOf(cid).State != hlroute.Forged || st.Lost != 0 || st.Owed != 53*gnot+25_000*gnot {
		t.Fatalf("forged: %s lost %d owed %d", st.ClaimOf(cid).State, st.Lost, st.Owed)
	}
	if hlmailbox.Paused(pkgPath, hlmailbox.FnPayouts) || hlmailbox.Paused(pkgPath, hlmailbox.FnCredits) || hlmailbox.Seat(5) != "" || hlmailbox.SeatOf(w5) != 0 {
		t.Fatalf("the unpause option: payouts %v credits %v seat 5 %s", hlmailbox.Paused(pkgPath, hlmailbox.FnPayouts), hlmailbox.Paused(pkgPath, hlmailbox.FnCredits), hlmailbox.Seat(5))
	}
	if !strings.Contains(st.Ledger.Get(st.Ledger.Last()).Note, "unpaused payouts,credits, seats 5 vacated") {
		t.Fatalf("note: %s", st.Ledger.Get(st.Ledger.Last()).Note)
	}
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), cid))
	d3 := st.Ledger.Get(st.Ledger.Last())
	if !d3.Queued() || st.ClaimOf(cid).State != hlroute.Forged {
		t.Fatalf("a forged restore should wait")
	}
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), d3.ID)
	if st.ClaimOf(cid).State != hlroute.Open {
		t.Fatalf("not restored")
	}
	c0 := balance(carol)
	skipDays(1)
	asRelayer(cur)
	Claim(cross(cur), 20)
	if balance(carol)-c0 != 995*gnot {
		t.Fatalf("carol +%d", balance(carol)-c0)
	}
	sent, paid, owed, voided, lost := Counters()
	if sent-paid != Locked() || owed != st.Owed || voided != 0 || lost != 0 {
		t.Fatalf("counters %d %d %d %d %d", sent, paid, owed, voided, lost)
	}
}

// TestSettings: every setting's clock — a lowering at once, a raise or a swap after 7 days — and the sweep, the
// credit by hand and the refund by id.
func TestSettings(cur realm, t *testing.T) {
	fundLock()
	// a refund waits until kind 6 has enrolled this route on Solana for 14 days (AR1, R37: a week for the delivery
	// and a week for Solana's own clock), as r/hlgov notes the dispatch with the mailbox — r/hlgov's own call, stood
	// in for here; the note at this function's start, so every refund the tests after make (each starts the clock
	// over) is past the 14 days
	asMember(cur)
	aborts(t, "no kind 6 has enrolled this route on Solana yet (none dispatched from r/hlgov): a refund waits 14 days after the enrolment", func() { NewRefundRequest(cross(cur), firstSend) })
	testing.SetRealm(testing.NewCodeRealm(hlgovPath))
	hlmailbox.NoteEnrolled(cross(cur), pkgPath, runtime.ChainHeight())
	asMember(cur)
	aborts(t, "kind 6 enrolling this route was dispatched at height "+itoa(runtime.ChainHeight())+", 0 s ago: a refund waits 14 days after it", func() { NewRefundRequest(cross(cur), firstSend) })
	asMember(cur)
	aborts(t, "never goes below the launch floor", func() { NewSetCapFloorRequest(cross(cur), 9_999*gnot) })
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 12_000*gnot))
	raise := st.Ledger.Last()
	if CapFloor() != 10_000*gnot || !st.Ledger.Get(raise).Queued() {
		t.Fatalf("a raise applied at once")
	}
	asRelayer(cur)
	aborts(t, "is due at", func() { Execute(cross(cur), raise) })
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), raise)
	if CapFloor() != 12_000*gnot || Cap() != 12_000*gnot {
		t.Fatalf("cap floor %d", CapFloor())
	}
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 11_000*gnot))
	if CapFloor() != 11_000*gnot || st.Ledger.Get(st.Ledger.Last()).Queued() {
		t.Fatalf("a lowering waited")
	}
	asMember(cur)
	aborts(t, "never goes below the minimum", func() { NewSetMaximumRequest(cross(cur), 9*gnot) })
	asMember(cur)
	pass(cur, NewSetMaximumRequest(cross(cur), 20_000*gnot))
	if Maximum() != 20_000*gnot {
		t.Fatalf("maximum")
	}
	// the maximum's description carries the order of a raise (R15): the Solana limit first
	asMember(cur)
	if req := NewSetMaximumRequest(cross(cur), 21_000*gnot); !strings.Contains(req.Description(), "set maximum to 21000 GNOT (now 20000 GNOT); raise the Solana limit (kind 12) first; a send above the Solana maximum strands") {
		t.Fatalf("description: %s", req.Description())
	}
	asMember(cur)
	aborts(t, "never goes below the fee floor", func() { NewSetFeeRequest(cross(cur), 1*gnot, 10*gnot) })
	// the cap floor is 11,000 now: a fee above its quarter (2,750) would stall every first installment
	asMember(cur)
	aborts(t, "never goes above a quarter of the cap floor (2750 GNOT)", func() { NewSetFeeRequest(cross(cur), 2_751*gnot, 10*gnot) })
	asMember(cur)
	pass(cur, NewSetFeeRequest(cross(cur), 6*gnot, 10*gnot))
	if Fee() != 5*gnot {
		t.Fatalf("a fee raise applied at once")
	}
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if Fee() != 6*gnot {
		t.Fatalf("fee %d", Fee())
	}
	asMember(cur)
	pass(cur, NewSetFeeFloorRequest(cross(cur), 3*gnot))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if FeeFloor() != 3*gnot {
		t.Fatalf("fee floor %d", FeeFloor())
	}
	// the router: the new route's messages are refused until the swap applies; the old route's are taken for 7
	// days after it, not later
	asRelayer(cur)
	aborts(t, "not from the router", func() { Process(cross(cur), newRouteAlice7Meta, newRouteAlice7Msg) })
	asMember(cur)
	pass(cur, NewSetRouterRequest(cross(cur), testRoute2))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	r2, _ := hl.ParseKey32(testRoute2)
	if Router() != hex.EncodeToString(r2[:]) {
		t.Fatalf("router %s", Router())
	}
	asRelayer(cur)
	Process(cross(cur), newRouteAlice7Meta, newRouteAlice7Msg)
	asRelayer(cur)
	Process(cross(cur), oldRouteAlice8Meta, oldRouteAlice8Msg)
	skipDays(7)
	asRelayer(cur)
	aborts(t, "not from the router", func() { Process(cross(cur), oldRouteAlice9Meta, oldRouteAlice9Msg) })
	// the sweep queue pays only by decision, entry by entry (R19): the fees realm's 50 to erin, the entry dropped
	fid := idOf(inFees50Msg)
	asMember(cur)
	aborts(t, "no sweep entry", func() { NewSweepRequest(cross(cur), strings.Repeat("cd", 32), erin) })
	asMember(cur)
	aborts(t, "is a claim, not a sweep entry", func() { NewSweepRequest(cross(cur), idOf(inAlice100Msg), erin) })
	asMember(cur)
	aborts(t, "outside the bridge's realms", func() { NewSweepRequest(cross(cur), fid, fees) })
	asMember(cur)
	req := NewSweepRequest(cross(cur), fid, erin)
	if !strings.Contains(req.Description(), "pay 50 GNOT of the sweep queue to "+string(erin)+": "+fid) {
		t.Fatalf("description: %s", req.Description())
	}
	pass(cur, req)
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if balance(erin) != 50*gnot || st.Sweep != 3*gnot || st.SweepOf(fid) != nil || Render("claim/"+fid) != "null" || !strings.Contains(st.Ledger.Get(st.Ledger.Last()).Note, "1 entries paid (50 GNOT)") {
		t.Fatalf("sweep: erin %d sweep %d entry %v", balance(erin), st.Sweep, st.SweepOf(fid))
	}
	asMember(cur)
	aborts(t, "no sweep entry", func() { NewSweepRequest(cross(cur), fid, erin) })
	// a credit by hand marks the id done in the mailbox and queues a claim; a done id is refused
	fresh := strings.Repeat("ab", 32)
	asMember(cur)
	aborts(t, "credited already and not voided", func() { NewCreditRequest(cross(cur), idOf(inAlice100Msg), bob, 10*gnot) })
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), fresh, bob, 100*gnot))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if hlmailbox.Delivered(fresh) == 0 || st.ClaimOf(fresh) == nil || st.ClaimOf(fresh).By != hlroute.ByVote {
		t.Fatalf("credit by vote")
	}
	asMember(cur)
	aborts(t, "credited already and not voided", func() { NewCreditRequest(cross(cur), fresh, bob, 10*gnot) })
	b0 := balance(bob)
	skipDays(1)
	asRelayer(cur)
	Claim(cross(cur), 20)
	if balance(bob)-b0 != 94*gnot { // the fee is 6 now
		t.Fatalf("bob +%d", balance(bob)-b0)
	}
	// a refund is a successor's duty, over the sends it imported (AQ2): this route's own send — alice's first — is
	// never refunded (a stranded send waits for a kind 12 on Solana, where it stays deliverable); a send handed to
	// this realm as a predecessor's page would is, after 7 days, once — the mark at send/<id>, the send in
	// refunds/0, the count and the sum in the json
	asMember(cur)
	aborts(t, "is this route's own send: a stranded send waits for a kind 12 on Solana, never a refund", func() { NewRefundRequest(cross(cur), firstSend) })
	inherited := strings.Repeat("ef", 32)
	inherit(inherited, alice, 100*gnot)
	// page 0 of a migration empties the meters as of now (R34): the route pays nothing until they refill (alice's
	// 25,000 remainder waits), and the json shows the bridge meter empty
	asRelayer(cur)
	if n := Claim(cross(cur), 20); n != 0 || !strings.Contains(Render("json"), "\"meter\":0,") {
		t.Fatalf("the meters after page 0: paid %d claims; json %s", n, Render("json"))
	}
	if !strings.HasSuffix(Render("send/"+inherited), "\"imported\":true,\"refunded\":0,\"writtenOff\":0}") || !strings.Contains(Render("send/"+firstSend), "\"from\":\""+string(alice)+"\",\"amount\":100000000,") || !strings.HasSuffix(Render("send/"+firstSend), "\"imported\":false,\"refunded\":0,\"writtenOff\":0}") || Render("send/zz") != "null" || Render("refunds/0") != "[]" {
		t.Fatalf("send pages: %s %s", Render("send/"+inherited), Render("send/"+firstSend))
	}
	a0, l0 := balance(alice), Locked()
	asMember(cur)
	req = NewRefundRequest(cross(cur), inherited)
	if !strings.Contains(req.Description(), "refund 100 GNOT of sends imported from the predecessor (14 days after kind 6's dispatch enrolled this realm) to their senders: "+inherited) {
		t.Fatalf("description: %s", req.Description())
	}
	pass(cur, req)
	refund := st.Ledger.Last()
	skipDays(7)
	// a refund's apply is GovDAO's own at-once act (AT2): Execute refuses the kind, the apply builder takes the
	// matured decision
	asRelayer(cur)
	aborts(t, "decision "+itoa(refund)+" is a refund: a refund is applied by GovDAO's own at-once decision (apply refund) after hlwatch's Solana check, never by Execute", func() { Execute(cross(cur), refund) })
	if balance(alice) != a0 || Locked() != l0 || st.SendOf(inherited).Refunded != 0 || !st.Ledger.Get(refund).Pending() {
		t.Fatalf("Execute applied a refund: alice +%d locked %d", balance(alice)-a0, Locked())
	}
	asMember(cur)
	pass(cur, NewApplyRefundRequest(cross(cur), refund))
	if balance(alice)-a0 != 100*gnot || Locked() != l0-100*gnot || st.SendOf(inherited).Refunded == 0 || st.SendOf(firstSend).Refunded != 0 || st.Ledger.Get(refund).State() != "applied" {
		t.Fatalf("refund: alice +%d locked %d", balance(alice)-a0, Locked())
	}
	if !strings.Contains(Render("json"), "\"refunded\":1,\"refundedTotal\":100000000,\"writtenOff\":0,\"writtenOffTotal\":0,") || Render("refunds/0") != "["+Render("send/"+inherited)+"]" || Render("refunds/1") != "[]" || !strings.Contains(Render("send/"+inherited), "\"refunded\":"+itoa(st.SendOf(inherited).Refunded)+",\"writtenOff\":0}") || !strings.Contains(Render(""), ", refunded 1 (100 GNOT); decisions: ") {
		t.Fatalf("the mark: %s\n%s", Render("send/"+inherited), Render("json"))
	}
	asMember(cur)
	aborts(t, "is not a send to refund", func() { NewRefundRequest(cross(cur), inherited) })
	p := Render("decisions")
	for _, want := range []string{"### set cap floor", "### set maximum", "### set fee and minimum", "### set fee floor", "### set router", "### sweep", "### credit", "### refund", "### apply refund", "### void", "### restore", "### cancel"} {
		if !strings.Contains(p, want) {
			t.Fatalf("decisions page lacks %q", want)
		}
	}
}

// decisionsOf reads a realm's "decisions" count out of its json: the id of its newest decision.
func decisionsOf(json string) int64 {
	s := json[strings.Index(json, "\"decisions\":")+len("\"decisions\":"):]
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.ParseInt(s[:end], 10, 64)
	return n
}

// TestYield (M1k): before the yield every builder's request GovDAO's proxy executes is refused — the one closure
// every builder shares refuses the proxy first, so the eleven requests this state lets a builder make stand for
// the fifteen (a restore, a refund and an apply refund need a voided claim, an enrolment note and a due refund
// this test does not make; TestSettings and TestSweepVoidAndGuards run those builders under both governors) — and
// nothing is recorded; then the governor yields on the mailbox, Govern is closed to it and a GovDAO proposal
// decides, on the same clocks.
func TestYield(cur realm, t *testing.T) {
	fundLock()
	// the governor queues a raise, so a cancel can be requested; nothing of it survives this test
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 11_500*gnot))
	raise := st.Ledger.Last()
	if !st.Ledger.Get(raise).Queued() || CapFloor() != 11_000*gnot {
		t.Fatalf("the governor's raise applied at once")
	}
	announced := namespace + "hlgnotm1k"
	testing.SetRealm(testing.NewCodeRealm(announced))
	hlmailbox.AnnounceSuccessor(cross(cur), hlmailbox.SymbolGNOT)
	bid, refunded, fresh := idOf(inBob3Msg), strings.Repeat("ef", 32), strings.Repeat("9a", 32)
	for _, r := range []struct {
		what string
		mk   func() Request
	}{
		{"set cap floor", func() Request { return NewSetCapFloorRequest(cross(cur), 12_000*gnot) }},
		{"set maximum", func() Request { return NewSetMaximumRequest(cross(cur), 19_000*gnot) }},
		{"set fee and minimum", func() Request { return NewSetFeeRequest(cross(cur), 5*gnot, 10*gnot) }},
		{"set fee floor", func() Request { return NewSetFeeFloorRequest(cross(cur), 2*gnot) }},
		{"set router", func() Request { return NewSetRouterRequest(cross(cur), testRoute) }},
		{"void", func() Request { return NewVoidRequest(cross(cur), "since:1", true, "", "") }},
		{"credit", func() Request { return NewCreditRequest(cross(cur), fresh, bob, 10*gnot) }},
		{"sweep", func() Request { return NewSweepRequest(cross(cur), bid, erin) }},
		{"write off", func() Request { return NewWriteOffRequest(cross(cur), refunded) }},
		{"migrate", func() Request { return NewMigrateRequest(cross(cur), announced) }},
		{"cancel", func() Request { return NewCancelRequest(cross(cur), raise) }},
	} {
		asMember(cur)
		refusedByDAO(cur, t, r.what, r.mk())
	}
	if CapFloor() != 11_000*gnot || Maximum() != 20_000*gnot || Fee() != 6*gnot || FeeFloor() != 3*gnot || st.SweepOf(bid) == nil || st.SendOf(refunded).WrittenOff != 0 || Migrated() != "" || !st.Ledger.Get(raise).Pending() || hlmailbox.Delivered(fresh) != 0 {
		t.Fatalf("a refused proposal changed something")
	}
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), raise))
	if st.Ledger.Get(raise).State() != "cancelled" {
		t.Fatalf("the governor's cancel")
	}
	// the yield, on the mailbox: this realm reads it
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	hlmailbox.Yield(cross(cur))
	h := hlmailbox.Yielded()
	if h == 0 || !strings.Contains(Render(""), "- governor: gno.land/r/gov/dao since height "+itoa(h)+" ("+string(launchGovernor)+" yielded)\n") || !strings.Contains(Render("json"), "\"governor\":\""+string(launchGovernor)+"\",\"yielded\":"+itoa(h)+",") {
		t.Fatalf("after the yield: %d\n%s", h, Render(""))
	}
	n := st.Ledger.Last()
	asMember(cur)
	req := NewSetCapFloorRequest(cross(cur), 11_500*gnot)
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	aborts(t, "hlgnot: the governor yielded at height "+itoa(h)+", gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never Govern", func() { Govern(cross(cur), req) })
	if st.Ledger.Last() != n {
		t.Fatalf("Govern recorded after the yield")
	}
	// GovDAO's proposal decides now (pass takes that path from here on): queued as the governor's was, then cancelled
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 11_500*gnot))
	raise = st.Ledger.Last()
	if d := st.Ledger.Get(raise); !d.Queued() || d.Due != d.Recorded+decisions.Delay || CapFloor() != 11_000*gnot {
		t.Fatalf("GovDAO's raise after the yield: %+v", d)
	}
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), raise))
	if st.Ledger.Get(raise).State() != "cancelled" || CapFloor() != 11_000*gnot {
		t.Fatalf("GovDAO's cancel after the yield")
	}
}

// TestSweepVoidAndGuards: R11 — a sweep entry is voided as forged under a guardian's flag naming it (as withheld
// never), restored after 7 days, and credited by id to its owner, which replaces it with a claim; R16 — a refund
// (a sweep payment the same) due under a standing payouts flag waits for the clear; R17 — a void's seat removal
// names the key the seat holds when the proposal is made, and a seat whose occupant changed by execution is
// skipped, the note saying so.
func TestSweepVoidAndGuards(cur realm, t *testing.T) {
	fundLock()
	bid := idOf(inBob3Msg)
	g1 := hlmailbox.Seat(1)
	if st.SweptOf(bid) != 3*gnot || st.Sweep != 3*gnot {
		t.Fatalf("sweep %d", st.Sweep)
	}
	owed0 := st.Owed
	asMember(cur)
	aborts(t, "is a sweep entry: voided as forged only", func() { NewVoidRequest(cross(cur), bid, false, "", "") })
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, bid)
	asMember(cur)
	pass(cur, NewVoidRequest(cross(cur), bid, true, "payouts", ""))
	d := st.Ledger.Get(st.Ledger.Last())
	if d.Queued() || !strings.Contains(d.Note, "1 voided at once") || st.Sweep != 0 || st.Owed != owed0-3*gnot || !st.SweepOf(bid).Voided || hlmailbox.Paused(pkgPath, hlmailbox.FnPayouts) {
		t.Fatalf("sweep void: %+v sweep %d owed %d", d, st.Sweep, st.Owed)
	}
	if j := Render("claim/" + bid); !strings.Contains(j, "\"swept\":3000000,") || !strings.Contains(j, "\"voided\":true") || !strings.Contains(Render(""), "`"+bid+"` (signers) at height ") || !strings.Contains(Render(""), ", voided as forged") {
		t.Fatalf("pages: %s\n%s", j, Render(""))
	}
	sent, paid, owed, voided, lost := Counters()
	if sent-paid != Locked() || owed != st.Owed || voided != 0 || lost != 0 {
		t.Fatalf("counters %d %d %d %d %d", sent, paid, owed, voided, lost)
	}
	asMember(cur)
	aborts(t, "is voided already", func() { NewVoidRequest(cross(cur), bid, true, "", "") })
	// restored after 7 days
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), bid))
	d = st.Ledger.Get(st.Ledger.Last())
	if !d.Queued() || !st.SweepOf(bid).Voided {
		t.Fatalf("a sweep restore should wait: %+v", d)
	}
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), d.ID)
	if st.SweepOf(bid).Voided || st.Sweep != 3*gnot || st.Owed != owed0 {
		t.Fatalf("restored: sweep %d owed %d", st.Sweep, st.Owed)
	}
	// credited by id to bob: the entry replaced by a claim of 3 in bob's queue
	asMember(cur)
	req := NewCreditRequest(cross(cur), bid, bob, 3*gnot)
	if !strings.Contains(req.Description(), "credit "+bid+" to "+string(bob)+" for 3 GNOT (replacing the sweep entry of 3 GNOT)") {
		t.Fatalf("description: %s", req.Description())
	}
	pass(cur, req)
	skipDays(8) // a day past the week, so the claim's height is not one TestRangeVoid's week-wide range names (each test function counts heights from the same start)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if c := st.ClaimOf(bid); st.SweepOf(bid) != nil || c == nil || c.To != bob || c.By != hlroute.ByVote || c.Amount != 3*gnot || st.Sweep != 0 || st.Owed != owed0 || !strings.Contains(Render("claim/"+bid), "\"state\":\"open\"") {
		t.Fatalf("credit replacing the entry: sweep %d owed %d", st.Sweep, st.Owed)
	}
	// R16, AT2: a refund — of a send a predecessor's page brought (AQ2), 20,000 of bob's handed to this realm as a
	// page would — is queued 7 days and applied by GovDAO's own at-once decision, never by anyone's Execute: the
	// apply builder refuses before the delay, Execute refuses the kind before and after it with the coins untouched,
	// and under a payouts flag the apply waits like a claim (the closure refuses; on chain the proposal survives
	// for a retry until its deadline, the harness keeps the DAO's accepted mark, so a fresh one here)
	sendID, sender := strings.Repeat("e2", 32), bob
	inherit(sendID, sender, 20_000*gnot)
	asMember(cur)
	pass(cur, NewRefundRequest(cross(cur), sendID))
	refund := st.Ledger.Last()
	if !strings.Contains(st.Ledger.Get(refund).Note, "queued 7 days: applied by GovDAO's own apply refund decision after, never by Execute") {
		t.Fatalf("the refund's note: %q", st.Ledger.Get(refund).Note)
	}
	asMember(cur)
	aborts(t, "refund decision "+itoa(refund)+" is queued until "+itoa(st.Ledger.Get(refund).Due)+" (", func() { NewApplyRefundRequest(cross(cur), refund) })
	asRelayer(cur)
	aborts(t, "never by Execute", func() { Execute(cross(cur), refund) })
	skipDays(7)
	s0, l0 := balance(sender), Locked()
	asRelayer(cur)
	aborts(t, "decision "+itoa(refund)+" is a refund: a refund is applied by GovDAO's own at-once decision (apply refund) after hlwatch's Solana check, never by Execute", func() { Execute(cross(cur), refund) })
	if !st.Ledger.Get(refund).Pending() || st.SendOf(sendID).Refunded != 0 || balance(sender) != s0 || Locked() != l0 {
		t.Fatalf("Execute applied a refund: sender +%d locked %d", balance(sender)-s0, Locked())
	}
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, "")
	asMember(cur)
	areq := NewApplyRefundRequest(cross(cur), refund)
	if !strings.Contains(areq.Description(), "apply refund decision "+itoa(refund)+" at once (refund 20000 GNOT of sends imported from the predecessor (14 days after kind 6's dispatch enrolled this realm) to their senders: "+sendID+"), after hlwatch's check that the token's router on Solana is this realm") {
		t.Fatalf("description: %s", areq.Description())
	}
	testing.SetRealm(testing.NewUserRealm(member))
	apid := dao.MustCreateProposal(cross(cur), areq.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(apid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "payouts are paused", func() { dao.ExecuteProposal(cross(cur), apid) })
	if !st.Ledger.Get(refund).Pending() || st.SendOf(sendID).Refunded != 0 || balance(sender) != s0 || Locked() != l0 {
		t.Fatalf("a refund paid under the flag")
	}
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.ClearFlag(cross(cur), pkgPath, hlmailbox.FnPayouts)
	asMember(cur)
	pass(cur, NewApplyRefundRequest(cross(cur), refund))
	ap := st.Ledger.Get(st.Ledger.Last())
	if balance(sender)-s0 != 20_000*gnot || Locked() != l0-20_000*gnot || st.SendOf(sendID).Refunded == 0 || st.Ledger.Get(refund).State() != "applied" || ap.Kind != hlroute.KindApplyRefund || ap.Queued() || ap.Note != "at once: refund decision "+itoa(refund)+" applied; 1 refunded" || st.Ledger.Get(refund).Note != "applied by GovDAO's apply refund decision "+itoa(ap.ID)+"; 1 refunded" {
		t.Fatalf("refund after the clear: sender +%d locked %d, notes %q / %q", balance(sender)-s0, Locked(), ap.Note, st.Ledger.Get(refund).Note)
	}
	asMember(cur)
	aborts(t, "refund decision "+itoa(refund)+" is applied, not pending", func() { NewApplyRefundRequest(cross(cur), refund) })
	asMember(cur)
	aborts(t, "decision "+itoa(ap.ID)+" is of kind apply refund, not refund: an apply refund names a queued refund decision", func() { NewApplyRefundRequest(cross(cur), ap.ID) })
	// R17: a void vacating seat 2 names the key seat 2 holds now; a replacement that lands before the void
	// executes stays, and the note says the seat was skipped
	g2 := hlmailbox.Seat(2)
	newKey := testutils.TestAddress("hlg-new-guardian")
	asMember(cur)
	armed := NewVoidRequest(cross(cur), bid, false, "", "2")
	if !strings.Contains(armed.Description(), "removing seats 2:"+string(g2)) {
		t.Fatalf("description: %s", armed.Description())
	}
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), armed.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	asMember(cur)
	passMailbox(cur, hlmailbox.NewRotateSeatsRequest(cross(cur), "2:"+string(newKey)))
	skipDays(7)
	asRelayer(cur)
	hlmailbox.Execute(cross(cur), decisionsOf(hlmailbox.Render("json")))
	if hlmailbox.Seat(2) != newKey {
		t.Fatalf("seat 2 %s", hlmailbox.Seat(2))
	}
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		t.Fatalf("the void did not execute")
	}
	d = st.Ledger.Get(st.Ledger.Last())
	if !d.Queued() || !strings.Contains(d.Note, "seats 2 skipped (the occupant changed since the proposal was made)") || hlmailbox.Seat(2) != newKey || hlmailbox.SeatOf(newKey) != 2 {
		t.Fatalf("a void vacated a replaced seat: %+v seat 2 %s", d, hlmailbox.Seat(2))
	}
	// the queued void of bob's claim is cancelled: the tests after expect it open
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), d.ID))
	if d.State() != "cancelled" || st.ClaimOf(bid).State != hlroute.Open {
		t.Fatalf("cancel")
	}
}

// TestRangeVoid: a guardian's payouts flag and a void may name a range of credit heights instead of a list; the
// at-once rule applies to the claims the flag's range covers, the rest of the void's range waits 7 days.
func TestRangeVoid(cur realm, t *testing.T) {
	fundLock()
	// two hand credits, 7 days apart, so their heights differ
	idA, idB := strings.Repeat("a1", 32), strings.Repeat("b2", 32)
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), idA, carol, 100*gnot))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	hA := st.ClaimOf(idA).Height
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), idB, dave, 100*gnot))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	hB := st.ClaimOf(idB).Height
	if hA <= 0 || hB <= hA || !strings.Contains(Render("claim/"+idA), "\"height\":"+itoa(hA)+",") || !strings.Contains(Render(""), "at height "+itoa(hA)+" `"+idA+"`") {
		t.Fatalf("heights %d %d:\n%s", hA, hB, Render(""))
	}
	// an empty or malformed range is refused when the proposal is made
	asMember(cur)
	aborts(t, "no claim to void in claims credited at heights 1-2", func() { NewVoidRequest(cross(cur), "heights:1-2", true, "", "") })
	asMember(cur)
	aborts(t, "lower height to the higher", func() { NewVoidRequest(cross(cur), "heights:9-8", true, "", "") })
	// a since-range above every credit finds nothing (the harness gives each test function its own height
	// counter, so the verifier's credits of the tests before sit at heights of their own: only a height past them
	// all is surely empty)
	asMember(cur)
	aborts(t, "no claim to void in the verifier's credits since height 9000000000000000", func() { NewVoidRequest(cross(cur), "since:9000000000000000", true, "", "") })
	// a guardian's flag naming heights hA-hA covers carol's claim alone: the range void takes it at once, queues
	// dave's in the same decision and reopens payouts in the same closure
	g1 := hlmailbox.Seat(1)
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, "heights:"+itoa(hA)+"-"+itoa(hA))
	asMember(cur)
	req := NewVoidRequest(cross(cur), "heights:"+itoa(hA)+"-"+itoa(hB), false, "payouts", "")
	if !strings.Contains(req.Description(), "void as withheld: claims credited at heights "+itoa(hA)+"-"+itoa(hB)+", and unpause payouts") {
		t.Fatalf("description: %s", req.Description())
	}
	pass(cur, req)
	d := st.Ledger.Get(st.Ledger.Last())
	if !d.Queued() || !strings.Contains(d.Note, "1 voided at once (named by a guardian's payouts flag) of claims credited at heights "+itoa(hA)+"-"+itoa(hB)+", unpaused payouts, 1 queued 7 days") {
		t.Fatalf("range void %+v", d)
	}
	if st.ClaimOf(idA).State != hlroute.Withheld || st.ClaimOf(idB).State != hlroute.Open || hlmailbox.Paused(pkgPath, hlmailbox.FnPayouts) {
		t.Fatalf("after the void: carol %s dave %s", st.ClaimOf(idA).State, st.ClaimOf(idB).State)
	}
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), d.ID)
	if st.ClaimOf(idB).State != hlroute.Withheld || st.Voided != 200*gnot {
		t.Fatalf("after the delay: dave %s voided %d", st.ClaimOf(idB).State, st.Voided)
	}
	// restored (a list), for the migration after
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), idA+","+idB))
	if st.ClaimOf(idA).State != hlroute.Open || st.ClaimOf(idB).State != hlroute.Open || st.Voided != 0 {
		t.Fatalf("restore")
	}
}

// TestWriteOff (AR1): a refunded send delivered on Solana after its refund (a stranger's Reemit) minted a second
// time; the write-off, 7 days on, books the amount as lost and marks the send, so the identity closes; only a
// refunded send is written off, once.
func TestWriteOff(cur realm, t *testing.T) {
	refunded := strings.Repeat("ef", 32) // alice's inherited 100, refunded in TestSettings
	asMember(cur)
	aborts(t, firstSend+" is not a refunded send: only a send delivered on Solana after its refund is written off", func() { NewWriteOffRequest(cross(cur), firstSend) })
	asMember(cur)
	aborts(t, "is not a refunded send", func() { NewWriteOffRequest(cross(cur), strings.Repeat("cd", 32)) })
	asMember(cur)
	req := NewWriteOffRequest(cross(cur), refunded)
	if !strings.Contains(req.Description(), "write off 100 GNOT of refunded sends delivered on Solana after their refund (hlwatch's DELIVERED line names the id): "+refunded) {
		t.Fatalf("description: %s", req.Description())
	}
	pass(cur, req)
	w := st.Ledger.Last()
	if !st.Ledger.Get(w).Queued() || st.Lost != 0 {
		t.Fatalf("a write-off applied at once: %+v", st.Ledger.Get(w))
	}
	lock := Locked()
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), w)
	sent, paid, _, _, lost := Counters()
	if lost != 100*gnot || st.SendOf(refunded).WrittenOff == 0 || Locked() != lock || sent-paid != Locked() || !strings.Contains(st.Ledger.Get(w).Note, "1 written off (100 GNOT)") {
		t.Fatalf("written off: lost %d locked %d note %q", lost, Locked(), st.Ledger.Get(w).Note)
	}
	if !strings.Contains(Render("send/"+refunded), ",\"writtenOff\":"+itoa(st.SendOf(refunded).WrittenOff)+"}") || !strings.Contains(Render("json"), "\"writtenOff\":1,\"writtenOffTotal\":100000000,") || !strings.Contains(Render(""), ", refunded 2 (20100 GNOT), written off 1 (100 GNOT); decisions: ") || !strings.Contains(Render("decisions"), "### write off") {
		t.Fatalf("the mark: %s\n%s", Render("send/"+refunded), Render(""))
	}
	asMember(cur)
	aborts(t, refunded+" is written off already", func() { NewWriteOffRequest(cross(cur), refunded) })
	// a mistaken write-off is undone by a restore naming the send (R38): 7 days, like a forged claim's restore,
	// since it re-opens the identity — the mark cleared, the amount back out of lost, the page and the json bare of
	// it; a send not written off is nothing to restore
	asMember(cur)
	aborts(t, firstSend+" is a send that is not written off: nothing to restore", func() { NewRestoreRequest(cross(cur), firstSend) })
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), refunded))
	rs := st.Ledger.Last()
	if !st.Ledger.Get(rs).Queued() || st.Lost != 100*gnot {
		t.Fatalf("a write-off's restore applied at once: %+v", st.Ledger.Get(rs))
	}
	asRelayer(cur)
	aborts(t, "is due at", func() { Execute(cross(cur), rs) })
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), rs)
	sent, paid, _, _, lost = Counters()
	if lost != 0 || st.SendOf(refunded).WrittenOff != 0 || st.SendOf(refunded).Refunded == 0 || Locked() != lock || sent-paid != Locked() || !strings.Contains(st.Ledger.Get(rs).Note, "1 restored (1 write-off undone: the send is lost no more)") {
		t.Fatalf("the write-off undone: lost %d note %q", lost, st.Ledger.Get(rs).Note)
	}
	if !strings.HasSuffix(Render("send/"+refunded), ",\"writtenOff\":0}") || !strings.Contains(Render("json"), "\"writtenOff\":0,\"writtenOffTotal\":0,") || strings.Contains(Render(""), ", written off ") || !strings.Contains(Render(""), ", refunded 2 (20100 GNOT); decisions: ") {
		t.Fatalf("the mark after the restore: %s\n%s", Render("send/"+refunded), Render(""))
	}
}

// TestMigrate: after 7 days the successor pulls the lock and the records; this realm refuses everything after.
func TestMigrate(cur realm, t *testing.T) {
	succ := namespace + "hlgnot2"
	fundLock()
	asMember(cur)
	aborts(t, "another realm under gno.land/r/", func() { NewMigrateRequest(cross(cur), pkgPath) })
	// a sub-identity's path (host#subpath) is no deployed realm, so it is never a successor (X1's sibling)
	asMember(cur)
	aborts(t, "another realm under gno.land/r/", func() { NewMigrateRequest(cross(cur), succ+"#1") })
	// a route the mailbox registers already — the BUBBLE route here — is refused when the proposal is made (R31),
	// not a week on by the mailbox's AddRoute, whatever it announced
	asMember(cur)
	aborts(t, "the successor "+hlbubblePath+" is a route already", func() { NewMigrateRequest(cross(cur), hlbubblePath) })
	// a successor announces itself to the mailbox from its own code (R25): a path with no realm behind it — a typo,
	// an undeployed path whose namespace owner could deploy anything and pull the lock — is never a successor
	asMember(cur)
	aborts(t, "has not announced itself", func() { NewMigrateRequest(cross(cur), succ) })
	// a successor announces which route it succeeds (AQ5): one announced for the BUBBLE route is refused here, so a
	// slip never sends the ugnot to a realm whose code moves BUBBLE
	other := namespace + "hlbubble2"
	testing.SetRealm(testing.NewCodeRealm(other))
	hlmailbox.AnnounceSuccessor(cross(cur), "BUBBLE")
	asMember(cur)
	aborts(t, "announced itself as the BUBBLE route's successor, not this GNOT route's", func() { NewMigrateRequest(cross(cur), other) })
	testing.SetRealm(testing.NewCodeRealm(succ))
	aborts(t, "the route a successor announces for is GNOT or BUBBLE, not DOGE", func() { hlmailbox.AnnounceSuccessor(cross(cur), "DOGE") })
	testing.SetRealm(testing.NewCodeRealm(succ))
	hlmailbox.AnnounceSuccessor(cross(cur), "GNOT")
	if !hlmailbox.SuccessorAnnounced(succ) || hlmailbox.SuccessorFor(succ) != "GNOT" || hlmailbox.SuccessorFor(other) != "BUBBLE" || hlmailbox.SuccessorFor(namespace+"nobody") != "" {
		t.Fatalf("not announced: %q", hlmailbox.SuccessorFor(succ))
	}
	asMember(cur)
	if req := NewMigrateRequest(cross(cur), succ); !strings.Contains(req.Description(), "migrate to "+succ+" (announced as the GNOT route's successor)") {
		t.Fatalf("description: %s", req.Description())
	}
	asMember(cur)
	pass(cur, NewMigrateRequest(cross(cur), succ))
	if Migrated() != "" {
		t.Fatalf("migrated at once")
	}
	skipDays(7)
	// a payouts flag standing when the migrate applies follows the lock to the successor (R27): the successor's
	// Claim asks its own path's flags and finds it, and page 0 — the lock — waits under it like a sweep payment
	g1 := hlmailbox.Seat(1)
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, "")
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if Migrated() != succ || !hlmailbox.IsRoute(succ) || hlmailbox.Predecessor(succ) != pkgPath || !hlmailbox.Paused(succ, hlmailbox.FnPayouts) {
		t.Fatalf("migrated %s route %v paused %v", Migrated(), hlmailbox.IsRoute(succ), hlmailbox.Paused(succ, hlmailbox.FnPayouts))
	}
	// the copy is the seat's own: cleared here, it stands on the successor until cleared there
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.ClearFlag(cross(cur), pkgPath, hlmailbox.FnPayouts)
	if hlmailbox.Paused(pkgPath, hlmailbox.FnPayouts) || !hlmailbox.Paused(succ, hlmailbox.FnPayouts) {
		t.Fatalf("the successor's copy cleared with the predecessor's")
	}
	envelope(alice, 10*gnot)
	aborts(t, "migrated to", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 10*gnot) })
	asRelayer(cur)
	aborts(t, "migrated to", func() { Claim(cross(cur), 20) })
	asRelayer(cur)
	aborts(t, "migrated to", func() { Process(cross(cur), inBob60Meta, inBob60Msg) })
	// the successor pulls nothing (AR2): before page 0 nothing has moved, so a second migrate — to another successor
	// announced for this route, never the same one — re-points the migration after 7 days, the mailbox dropping the
	// first successor's registration and the flags copied to it and copying the flags standing here now to the new
	// path; every other builder refuses as before
	first := succ
	asMember(cur)
	aborts(t, "migrated to "+first+" already (nothing pulled yet): a re-point names another successor", func() { NewMigrateRequest(cross(cur), first) })
	asMember(cur)
	aborts(t, "migrated to "+first, func() { NewSetCapFloorRequest(cross(cur), 12_000*gnot) })
	succ = namespace + "hlgnot3"
	testing.SetRealm(testing.NewCodeRealm(succ))
	hlmailbox.AnnounceSuccessor(cross(cur), "GNOT")
	asMember(cur)
	pass(cur, NewMigrateRequest(cross(cur), succ))
	repoint := st.Ledger.Last()
	if !st.Ledger.Get(repoint).Queued() || Migrated() != first {
		t.Fatalf("a re-point applied at once: %+v", st.Ledger.Get(repoint))
	}
	w6 := hlmailbox.Seat(6)
	testing.SetRealm(testing.NewUserRealm(w6))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnCredits, "")
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), repoint)
	if Migrated() != succ || hlmailbox.IsRoute(first) || hlmailbox.Predecessor(first) != "" || hlmailbox.Successor(pkgPath) != succ || hlmailbox.Predecessor(succ) != pkgPath || !hlmailbox.IsRoute(succ) {
		t.Fatalf("re-pointed: migrated %s, first a route %v, successor %s", Migrated(), hlmailbox.IsRoute(first), hlmailbox.Successor(pkgPath))
	}
	if hlmailbox.Paused(first, hlmailbox.FnPayouts) || !hlmailbox.Paused(succ, hlmailbox.FnCredits) || hlmailbox.Paused(succ, hlmailbox.FnPayouts) {
		t.Fatalf("the flags after the re-point: first payouts %v, new credits %v payouts %v", hlmailbox.Paused(first, hlmailbox.FnPayouts), hlmailbox.Paused(succ, hlmailbox.FnCredits), hlmailbox.Paused(succ, hlmailbox.FnPayouts))
	}
	testing.SetRealm(testing.NewUserRealm(w6))
	hlmailbox.ClearFlag(cross(cur), pkgPath, hlmailbox.FnCredits)
	testing.SetRealm(testing.NewUserRealm(w6))
	hlmailbox.ClearFlag(cross(cur), succ, hlmailbox.FnCredits)
	if !strings.Contains(Render(""), "**migrated** to `"+succ+"`") {
		t.Fatalf("page: %s", Render(""))
	}
	// the dropped successor is remembered (R35): a route no more, in nobody's chain, that neither sends nor takes —
	// its Dispatch refused by the mailbox (Solana's token may still name it until the next kind 6: a send through
	// it would mint against a lock no realm of the line holds), the line's last realm taking what is addressed to
	// it once its import ends (the mailbox's TestRepointSuccessor); and nothing was ever in it, since a successor
	// takes no send before its last page (TestMustSend, hlgnot2's tests) and kind 6 never names one before page 0
	if hlmailbox.Dropped(first) != pkgPath || hlmailbox.Dropped(succ) != "" || hlmailbox.Pulled(succ) {
		t.Fatalf("dropped %q %q, pulled %v", hlmailbox.Dropped(first), hlmailbox.Dropped(succ), hlmailbox.Pulled(succ))
	}
	// the dropped successor's address stays a realm of the bridge (AT6): refused as a credit's recipient here, as a
	// registered route's is (a burn naming it is swept by the line's last realm: hlgnot2's and hlbubble's tests)
	asMember(cur)
	aborts(t, "the recipient must be an address outside the bridge's realms", func() { NewCreditRequest(cross(cur), strings.Repeat("f1", 32), chain.PackageAddress(first), 1*gnot) })
	testing.SetRealm(testing.NewCodeRealm(first))
	aborts(t, first+" is a route no more (its predecessor "+pkgPath+" re-pointed the migration to another successor): it sends nothing", func() {
		hlmailbox.Dispatch(cross(cur), solanaTestnet, [32]byte{1}, []byte("a send through a dropped successor"))
	})
	// a burn addressed to this realm after the migrate (AQ1): refused here before the mailbox marks it, and the
	// mailbox hands it to the successor alone (a stranger is refused, and the first successor, a route no more) —
	// the successor's own route code credits it once its last page is in; the mark is the shared one, so it is
	// done once
	if hlmailbox.Delivered(idOf(inBob60Msg)) != 0 {
		t.Fatalf("the predecessor's refusal marked the message delivered")
	}
	asRelayer(cur)
	aborts(t, "only the message's recipient, or a successor of it", func() { hlmailbox.Process(cross(cur), unhex(inBob60Meta), unhex(inBob60Msg)) })
	testing.SetRealm(testing.NewCodeRealm(first))
	aborts(t, first+" is a route no more (its predecessor "+pkgPath+" re-pointed the migration to another successor): the line's last realm takes this message", func() { hlmailbox.Process(cross(cur), unhex(inBob60Meta), unhex(inBob60Msg)) })
	testing.SetRealm(testing.NewCodeRealm(succ))
	wallet, _ := hl.AddressToBytes32(bob)
	oldRoute, _ := hl.ParseKey32(testRoute) // the launch router signed it; the swap to testRoute2 is the route's concern, not the mailbox's
	if origin, sender, body := hlmailbox.Process(cross(cur), unhex(inBob60Meta), unhex(inBob60Msg)); origin != solanaTestnet || sender != oldRoute || string(body) != string(hl.TokenMessage(wallet, uint64(60*gnot), nil)) {
		t.Fatalf("the successor took %d %x %x", origin, sender, body)
	}
	if hlmailbox.Delivered(idOf(inBob60Msg)) == 0 {
		t.Fatalf("not marked delivered")
	}
	asRelayer(cur)
	aborts(t, "only the successor", func() { Pull(cross(cur), 0) })
	testing.SetRealm(testing.NewCodeRealm(first))
	aborts(t, "only the successor", func() { Pull(cross(cur), 0) })
	lock, owed := Locked(), st.Owed
	// page 0 moves the lock, so under a standing payouts flag it waits for the unpause like a sweep payment (R27)
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, "")
	testing.SetRealm(testing.NewCodeRealm(succ))
	aborts(t, "payouts are paused", func() { Pull(cross(cur), 0) })
	if Locked() != lock || balance(chain.PackageAddress(succ)) != 0 {
		t.Fatalf("a pull under the flag moved the lock: %d, successor %d", Locked(), balance(chain.PackageAddress(succ)))
	}
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.ClearFlag(cross(cur), pkgPath, hlmailbox.FnPayouts)
	testing.SetRealm(testing.NewCodeRealm(succ))
	snap := Pull(cross(cur), 0)
	if snap.Page != 0 || snap.More || snap.Lock != lock || snap.Owed != owed || len(snap.Claims) == 0 || len(snap.Sends) == 0 || balance(chain.PackageAddress(succ)) != lock || Locked() != 0 || st.Owed != 0 {
		t.Fatalf("pull: lock %d → %d, claims %d", lock, balance(chain.PackageAddress(succ)), len(snap.Claims))
	}
	// page 0 pulled is noted with the mailbox by this realm's Pull (R35): what r/hlgov's kind 6 asks before it
	// enrols the successor, and what refuses a re-point there too
	if !hlmailbox.Pulled(succ) || hlmailbox.Pulled(pkgPath) {
		t.Fatalf("page 0 not noted pulled: %v", hlmailbox.Pulled(succ))
	}
	// page 0 pulled, the migration is under way: no second migrate re-points it (AR2)
	asMember(cur)
	aborts(t, "the successor pulled 1 pages already (the lock moved with page 0); a migration under way is never re-pointed", func() { NewMigrateRequest(cross(cur), first) })
	// a refunded send goes with its page, its mark kept (R33): the two refunded here (TestSettings and
	// TestSweepVoidAndGuards) are in the page with their times, and this realm counts them no more
	refunded := 0
	for _, sd := range snap.Sends {
		if sd.Refunded != 0 {
			refunded++
		}
	}
	if refunded != 2 || st.Sends() != 0 || !strings.Contains(Render("json"), "\"sends\":0,\"refunded\":0,\"refundedTotal\":0,") || Render("refunds/0") != "[]" {
		t.Fatalf("refunded sends in the page: %d of %d; json %s", refunded, len(snap.Sends), Render("json"))
	}
	// a page pulled already hands over nothing and pays nothing; the pages go in order
	testing.SetRealm(testing.NewCodeRealm(succ))
	if again := Pull(cross(cur), 0); again.Lock != 0 || len(again.Claims) != 0 || len(again.Sends) != 0 || balance(chain.PackageAddress(succ)) != lock {
		t.Fatalf("a second pull of page 0 handed something over: %+v", again)
	}
	testing.SetRealm(testing.NewCodeRealm(succ))
	aborts(t, "page 1 is the next to pull, not 2", func() { Pull(cross(cur), 2) })
	testing.SetRealm(testing.NewCodeRealm(succ))
	if empty := Pull(cross(cur), 1); empty.More || len(empty.Claims) != 0 || len(empty.Sweeps) != 0 || len(empty.Sends) != 0 {
		t.Fatalf("page 1 is not empty: %+v", empty)
	}
	if !strings.Contains(Render(""), "**migrated** to `"+succ+"`") {
		t.Fatalf("page: %s", Render(""))
	}
}

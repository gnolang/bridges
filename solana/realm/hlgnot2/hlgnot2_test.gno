package hlgnot2

import (
	"chain"
	"chain/banker"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"gno.land/p/nt/testutils/v0"
	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlgnot"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlgov"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlmailbox"
	"gno.land/r/gov/dao"
	govinit "gno.land/r/gov/dao/init/v0"
)

// The whole migration hlgnot → hlgnot2, end to end in one chain of test functions (AS5, AS6, C384: "No successor
// realm exists yet"): the real GovDAO (one T1 member), the real r/hlgnot, r/hlgov and r/hlmailbox, this realm
// deployed beside them. The messages from Solana are crafted under a verifier that takes anything, since the
// signed vectors name hlgnot's address alone and the migration needs burns addressed to three realms; the
// signatures are hlverifier's and the mailbox's subject. Test time moves 5 s a height; the tests run in order
// and share state, and the harness's coin table does not survive a test function, so the migration runs inside
// one.

const (
	solanaTestnet = uint32(1399811150)
	testVerifier  = namespace + "hltestverifier"
	dayHeights    = 86400 / 5
	solWallet     = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"

	hlgnotPath = predecessorPath
	firstX     = namespace + "hlgnotx" // the first successor hlgnot's migrate names, which pulls nothing and is re-pointed away: a dropped route (R35)
	hlgnotY    = namespace + "hlgnoty" // a would-be re-point after page 0, refused
	hlgnot3    = namespace + "hlgnot3" // this realm's own successor, in its turn

	smallSends = 496 // sends of 10 GNOT that fill the records past one page of 500: two pages to adopt
)

var (
	member    = testutils.TestAddress("hl-member")
	alice     = testutils.TestAddress("hl2-alice")
	bob       = testutils.TestAddress("hl2-bob")
	carol     = testutils.TestAddress("hl2-carol")
	dave      = testutils.TestAddress("hl2-dave")
	erin      = testutils.TestAddress("hl2-erin")
	frank     = testutils.TestAddress("hl2-frank")
	relayer   = testutils.TestAddress("hl2-relayer")
	stranger1 = testutils.TestAddress("hl2-stranger-1") // the two keys that drive Adopt: nobody's in particular
	stranger2 = testutils.TestAddress("hl2-stranger-2")
	fees      = chain.PackageAddress(feesPath)
)

// anyVerifier takes every message.
type anyVerifier struct{}

func (anyVerifier) Verify(origin uint32, metadata, message []byte) error { return nil }
func (anyVerifier) Satisfiable(signers []hlmailbox.Key) bool             { return true }

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

// proposal is GovDAO's whole path for a request's proposal: created by the member, voted yes, executed.
func proposal(cur realm, p dao.ProposalRequest) {
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), p)
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		panic("the proposal did not execute")
	}
}

// pass, passGnot, passGov and passMailbox make a builder's decision on this realm, on hlgnot, on hlgov and on the
// mailbox by whoever governs now (M1k): the governor's own Govern before the yield — the migration's first half,
// up to the refund's apply — and GovDAO's proposal after it. The realm is set in this frame, right before the
// call, as the DAO's member is.
func pass(cur realm, req Request) {
	if hlmailbox.Yielded() == 0 {
		testing.SetRealm(testing.NewUserRealm(launchGovernor))
		Govern(cross(cur), req)
		return
	}
	proposal(cur, req.Proposal())
}

func passGnot(cur realm, req hlgnot.Request) {
	if hlmailbox.Yielded() == 0 {
		testing.SetRealm(testing.NewUserRealm(launchGovernor))
		hlgnot.Govern(cross(cur), req)
		return
	}
	proposal(cur, req.Proposal())
}

func passGov(cur realm, req hlgov.Request) {
	if hlmailbox.Yielded() == 0 {
		testing.SetRealm(testing.NewUserRealm(launchGovernor))
		hlgov.Govern(cross(cur), req)
		return
	}
	proposal(cur, req.Proposal())
}

func passMailbox(cur realm, req hlmailbox.Request) {
	if hlmailbox.Yielded() == 0 {
		testing.SetRealm(testing.NewUserRealm(launchGovernor))
		hlmailbox.Govern(cross(cur), req)
		return
	}
	proposal(cur, req.Proposal())
}

// refusedByDAO proves GovDAO's proxy cannot make a builder's decision of this realm before the yield (M1k): the
// proposal is created and voted, its execution aborts on the governor's rule, and nothing is recorded.
func refusedByDAO(cur realm, t *testing.T, what string, req Request) {
	t.Helper()
	n := st.Ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlgnot2: until the governor yields, "+string(launchGovernor)+" alone decides, by Govern from its own key: not gno.land/r/gov/dao (a GovDAO proposal is refused until the yield)", func() { dao.ExecuteProposal(cross(cur), pid) })
	if st.Ledger.Last() != n {
		t.Fatalf("%s: a refused proposal recorded a decision", what)
	}
}

func asMember(cur realm)  { testing.SetRealm(testing.NewUserRealm(member)) }
func asRelayer(cur realm) { testing.SetRealm(testing.NewUserRealm(relayer)) }
func skipDays(n int64)    { testing.SkipHeights(n*dayHeights + 1) }

func balance(a address) int64 { return banker.NewReadonlyBanker().GetCoin(a, "ugnot") }

func bytes32(a address) [32]byte {
	b, err := hl.AddressToBytes32(a)
	if err != nil {
		panic(err)
	}
	return b
}

// envelope sets up the coins of a direct user call on the route realm at path: exactly amount + fee sent, issued to
// the route only when the send is expected to go through, so a refused send leaves no coins outside the lock. The
// test then sets the user realm ITSELF, in its own frame, right before it calls TransferRemote: a user override
// set inside a helper binds to the helper's frame and a crossing call into another package made after it does not
// see it (the callee's cur.Previous() is then this package, not the user, and IsUserCall fails), while an
// in-package call does — which is why hlgnot's own tests may set it in a helper and these may not.
func envelope(path string, amount, fee int64, issue bool) {
	testing.SetRealm(testing.NewCodeRealm(path)) // the envelope lands on this realm
	testing.SetOriginSpend(nil)                  // the harness carries what earlier envelopes spent (the fees) into every later context: a fresh envelope starts at 0
	testing.SetOriginSend(chain.Coins{{"ugnot", amount + fee}})
	if issue {
		testing.IssueCoins(chain.PackageAddress(path), chain.Coins{{"ugnot", amount + fee}})
	}
}

// burn is a token message from the router to the route realm at `to`, crediting rcpt with amount, as hex, and its
// id; nonce makes each one distinct.
func burn(nonce uint32, to address, rcpt address, amount int64) (msg, id string) {
	m := hl.Message{Version: hl.Version, Nonce: nonce, Origin: solanaTestnet, Sender: st.Router, Destination: hlmailbox.LocalDomain(), Recipient: bytes32(to), Body: hl.TokenMessage(bytes32(rcpt), uint64(amount), nil)}
	raw := m.Bytes()
	idb := hl.ID(raw)
	return hex.EncodeToString(raw), hex.EncodeToString(idb[:])
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

// sums is the pair's five counters summed, and the pair's lock and balance summed: what the watch, hlwatch and the
// signers read over a route's line (§5.3), invariant at every step of the migration.
type sums struct{ sent, paid, owed, voided, lost, lock, balance int64 }

func pair() sums {
	s1, p1, o1, v1, l1 := hlgnot.Counters()
	s2, p2, o2, v2, l2 := Counters()
	return sums{s1 + s2, p1 + p2, o1 + o2, v1 + v2, l1 + l2, hlgnot.Locked() + Locked(), hlgnot.Balance() + Balance()}
}

func samePair(t *testing.T, want sums, step string) {
	t.Helper()
	if got := pair(); got != want {
		t.Fatalf("the pair's sums moved at %s: %+v, want %+v", step, got, want)
	}
}

// identity checks §5.9's identity on this realm: lock = sentTotal − paidTotal = supply + owed + voided − lost.
func identity(t *testing.T, supply int64, step string) {
	t.Helper()
	sent, paid, owed, voided, lost := Counters()
	if Locked() != sent-paid || Locked() != supply+owed+voided-lost {
		t.Fatalf("the identity at %s: lock %d, sent %d paid %d, supply %d owed %d voided %d lost %d", step, Locked(), sent, paid, supply, owed, voided, lost)
	}
}

func TestLaunchValues(cur realm, t *testing.T) {
	route, _ := hl.ParseKey32(launchRouter)
	if hlmailbox.Live() || Locked() != 0 || Fee() != 5*gnot || Minimum() != 10*gnot || Maximum() != 25_000*gnot || Domain() != solanaTestnet || Router() != hex.EncodeToString(route[:]) || Predecessor() != hlgnotPath || Imported() != 0 || Importing() || Migrated() != "" {
		t.Fatalf("launch values: predecessor %s imported %d", Predecessor(), Imported())
	}
	// announced from init (R25, AQ5): the GNOT route's possible successor, a route the mailbox registers not yet
	if !hlmailbox.SuccessorAnnounced(pkgPath) || hlmailbox.SuccessorFor(pkgPath) != hlmailbox.SymbolGNOT || hlmailbox.RouteSymbol(pkgPath) != hlmailbox.SymbolGNOT || hlmailbox.IsRoute(pkgPath) || hlmailbox.Predecessor(pkgPath) != "" {
		t.Fatalf("not announced: %q, route %v", hlmailbox.SuccessorFor(pkgPath), hlmailbox.IsRoute(pkgPath))
	}
	p := Render("")
	for _, want := range []string{"# GNOT across chains (successor)", "**not launched**", "- succeeds `" + hlgnotPath + "` (compiled; announced to the mailbox): no migrate decision names this realm yet", "fee 5 GNOT (floor 2 GNOT); minimum 10 GNOT; per-message maximum 25000 GNOT", "No decision yet",
		"- governor: " + string(launchGovernor) + " (until the yield) — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused\n"} {
		if !strings.Contains(p, want) {
			t.Fatalf("page lacks %q:\n%s", want, p)
		}
	}
	if !strings.Contains(Render("json"), "\"live\":false,\"governor\":\""+string(launchGovernor)+"\",\"yielded\":0,\"predecessor\":\""+hlgnotPath+"\",") || !strings.Contains(Render("json"), "\"importing\":false,\"imported\":0,") {
		t.Fatalf("json: %s", Render("json"))
	}
	testing.SetRealm(testing.NewUserRealm(stranger1))
	aborts(t, "not launched", func() { Adopt(cross(cur), 0) })
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "not launched", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 10*gnot) })
}

// TestSetup: GovDAO launches the realms and activates the test verifier.
func TestSetup(cur realm, t *testing.T) {
	asMember(cur)
	govinit.InitWithUsers(cross(cur), member)
	testing.SetRealm(testing.NewCodeRealm(testVerifier))
	hlmailbox.RegisterVerifier(cross(cur), anyVerifier{})
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
	// no migrate names this realm: nothing to adopt, and no page before the migrate either
	testing.SetRealm(testing.NewUserRealm(stranger1))
	aborts(t, "no migrate decision of "+hlgnotPath+" names this realm yet: nothing to adopt", func() { Adopt(cross(cur), 0) })
}

// TestMigration drives the migration end to end: hlgnot takes sends and burns (502 records, two pages); its migrate
// names a first successor that pulls nothing (hlgnotx), which GovDAO re-points to this realm (AR2) — the dropped
// route neither sends nor takes (R35) and kind 6 never named it; before page 0 this realm takes no send (R35,
// F28), credits nothing (R32) and is no router (R35); a stranger's Adopt(0) waits under the predecessor's payouts
// flag and then moves the lock, the counters and the first page in one call, the pair's sums unmoved; a re-point
// is refused from then on; another stranger's Adopt(1) brings the last page; this realm then takes a message
// addressed to the dropped route, to the predecessor and to itself, sends nothing before kind 6 enrols it (AT7)
// and sends after; a refund of an imported send is refused inside kind 6's 14 days and taken after (AR1, R37),
// applied by GovDAO's own at-once decision (AT2); a write-off and its restore (R38) close the identity both ways;
// a burn to a registered route's address, or a dropped one's, is swept and credited by id (R39, AT6); and this
// realm migrates on to a successor of its own, whose Pull notes page 0 with the mailbox.
func TestMigration(cur realm, t *testing.T) {
	// --- hlgnot's state: three sends of alice, bob and carol, 496 small ones of dave, three burns, a day's payouts
	envelope(hlgnotPath, 100*gnot, hlgnot.Fee(), true)
	testing.SetRealm(testing.NewUserRealm(alice))
	aliceSend := hlgnot.TransferRemote(cross(cur), solanaTestnet, solWallet, 100*gnot)
	envelope(hlgnotPath, 20_000*gnot, hlgnot.Fee(), true)
	testing.SetRealm(testing.NewUserRealm(bob))
	bobSend := hlgnot.TransferRemote(cross(cur), solanaTestnet, solWallet, 20_000*gnot)
	envelope(hlgnotPath, 20_000*gnot, hlgnot.Fee(), true)
	testing.SetRealm(testing.NewUserRealm(carol))
	carolSend := hlgnot.TransferRemote(cross(cur), solanaTestnet, solWallet, 20_000*gnot)
	for i := 0; i < smallSends; i++ {
		envelope(hlgnotPath, 10*gnot, hlgnot.Fee(), true)
		testing.SetRealm(testing.NewUserRealm(dave))
		hlgnot.TransferRemote(cross(cur), solanaTestnet, solWallet, 10*gnot)
	}
	lock0 := int64(100+20_000+20_000+10*smallSends) * gnot
	if hlgnot.Locked() != lock0 || hlgnot.Balance() != lock0 {
		t.Fatalf("hlgnot's lock %d, balance %d, want %d", hlgnot.Locked(), hlgnot.Balance(), lock0)
	}
	aliceBurn, aliceBurnID := burn(1, hlgnot.Address(), alice, 100*gnot)
	bobBurn, bobBurnID := burn(2, hlgnot.Address(), bob, 3*gnot) // below the fee: swept
	erinBurn, erinBurnID := burn(3, hlgnot.Address(), erin, 30_000*gnot)
	asRelayer(cur)
	hlgnot.Process(cross(cur), "", aliceBurn)
	asRelayer(cur)
	hlgnot.Process(cross(cur), "", bobBurn)
	asRelayer(cur)
	hlgnot.Process(cross(cur), "", erinBurn)
	skipDays(1)
	asRelayer(cur)
	if n := hlgnot.Claim(cross(cur), 20); n != 2 || balance(alice) != 95*gnot || balance(erin) != 2_495*gnot {
		t.Fatalf("a day's payouts on hlgnot: %d claims, alice %d erin %d", n, balance(alice), balance(erin))
	}
	supply := lock0 - (100+3+30_000)*gnot // every send minted, every burn burned: what the identity derives
	want := pair()
	if want.sent != lock0 || want.paid != 2_600*gnot || want.owed != 27_503*gnot || want.voided != 0 || want.lost != 0 || want.lock != lock0-2_600*gnot || want.balance != want.lock || Locked() != 0 {
		t.Fatalf("the pair before the migration: %+v", want)
	}

	// --- the first migrate names hlgnotx, which pulls nothing: kind 6 never names it (R35)
	testing.SetRealm(testing.NewCodeRealm(firstX))
	hlmailbox.AnnounceSuccessor(cross(cur), hlmailbox.SymbolGNOT)
	asMember(cur)
	passGnot(cur, hlgnot.NewMigrateRequest(cross(cur), firstX))
	skipDays(7)
	asRelayer(cur)
	hlgnot.Execute(cross(cur), decisionsOf(hlgnot.Render("json")))
	if hlgnot.Migrated() != firstX || !hlmailbox.IsRoute(firstX) || hlmailbox.Predecessor(firstX) != hlgnotPath || hlmailbox.Pulled(firstX) {
		t.Fatalf("the first migrate: %s", hlgnot.Migrated())
	}
	samePair(t, want, "the first migrate")
	kx := bytes32(chain.PackageAddress(firstX))
	asMember(cur)
	aborts(t, firstX+" succeeds "+hlgnotPath+" and has pulled nothing yet (page 0 is not in): a kind 6 enrolling it waits for its first page", func() {
		hlgov.NewEnrollRouterRequest(cross(cur), 0, hlmailbox.LocalDomain(), hex.EncodeToString(kx[:]))
	})

	// --- the re-point to this realm (AR2), under a guardian's payouts flag: hlgnotx is dropped, the flag follows
	asMember(cur)
	passGnot(cur, hlgnot.NewMigrateRequest(cross(cur), pkgPath))
	skipDays(7)
	g1 := hlmailbox.Seat(1)
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.SetFlag(cross(cur), hlgnotPath, hlmailbox.FnPayouts, "")
	asRelayer(cur)
	hlgnot.Execute(cross(cur), decisionsOf(hlgnot.Render("json")))
	if hlgnot.Migrated() != pkgPath || !hlmailbox.IsRoute(pkgPath) || hlmailbox.Predecessor(pkgPath) != hlgnotPath || hlmailbox.IsRoute(firstX) || hlmailbox.Dropped(firstX) != hlgnotPath || !hlmailbox.Paused(pkgPath, hlmailbox.FnPayouts) || hlmailbox.Pulled(pkgPath) {
		t.Fatalf("the re-point: migrated %s, dropped %q, paused %v", hlgnot.Migrated(), hlmailbox.Dropped(firstX), hlmailbox.Paused(pkgPath, hlmailbox.FnPayouts))
	}
	samePair(t, want, "the re-point")
	if !strings.Contains(Render(""), "- succeeds `"+hlgnotPath+"`: migrate applied, page 0 not adopted yet (anyone may call Adopt(0)); nothing is sent or credited before the last page") {
		t.Fatalf("page: %s", Render(""))
	}

	// --- the dropped route: a route no more that neither sends nor takes
	testing.SetRealm(testing.NewCodeRealm(firstX))
	aborts(t, firstX+" is a route no more (its predecessor "+hlgnotPath+" re-pointed the migration to another successor): it sends nothing", func() {
		hlmailbox.Dispatch(cross(cur), solanaTestnet, st.Router, hl.TokenMessage(bytes32(alice), 10, nil))
	})
	toX, toXID := burn(4, chain.PackageAddress(firstX), frank, 60*gnot)
	testing.SetRealm(testing.NewCodeRealm(firstX))
	aborts(t, firstX+" is a route no more (its predecessor "+hlgnotPath+" re-pointed the migration to another successor): the line's last realm takes this message", func() {
		hlmailbox.Process(cross(cur), nil, mustHex(toX, "message"))
	})
	testing.SetRealm(testing.NewCodeRealm(firstX))
	aborts(t, "only a route realm", func() { hlmailbox.MarkDone(cross(cur), toXID) })
	if hlmailbox.Delivered(toXID) != 0 {
		t.Fatalf("a refused message was marked delivered")
	}

	// --- this realm before page 0: no send (R35, F28), no credit (R32), no router (R35); Adopt waits under the flag
	envelope(pkgPath, 10*gnot, Fee(), false)
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "this route succeeds "+hlgnotPath+": nothing is sent before the last page of the migration is in (0 pages so far)", func() {
		TransferRemote(cross(cur), solanaTestnet, solWallet, 10*gnot)
	})
	toSelf, toSelfID := burn(5, Address(), frank, 80*gnot)
	toPred, toPredID := burn(6, hlgnot.Address(), frank, 70*gnot)
	asRelayer(cur)
	aborts(t, "this route succeeds "+hlgnotPath+": nothing is credited before the last page of the migration is in (0 pages so far)", func() { Process(cross(cur), "", toSelf) })
	asRelayer(cur)
	aborts(t, "addressed to another realm: a successor takes its predecessor's messages once the last page of the migration is in (0 pages so far)", func() { Process(cross(cur), "", toX) })
	asRelayer(cur)
	aborts(t, "addressed to another realm: a successor takes its predecessor's messages once the last page of the migration is in (0 pages so far)", func() { Process(cross(cur), "", toPred) })
	asRelayer(cur)
	aborts(t, "migrated to "+pkgPath+": the successor takes this message", func() { hlgnot.Process(cross(cur), "", toPred) })
	k2 := bytes32(Address())
	asMember(cur)
	aborts(t, pkgPath+" succeeds "+hlgnotPath+" and has pulled nothing yet (page 0 is not in): a kind 6 enrolling it waits for its first page", func() {
		hlgov.NewEnrollRouterRequest(cross(cur), 0, hlmailbox.LocalDomain(), hex.EncodeToString(k2[:]))
	})
	if hlmailbox.Delivered(toSelfID) != 0 || hlmailbox.Delivered(toPredID) != 0 || Locked() != 0 || Balance() != 0 {
		t.Fatalf("something moved before page 0: locked %d balance %d", Locked(), Balance())
	}
	testing.SetRealm(testing.NewUserRealm(stranger1))
	aborts(t, "hlgnot: payouts are paused by a guardian or a watch key", func() { Adopt(cross(cur), 0) })
	testing.SetRealm(testing.NewUserRealm(stranger1))
	aborts(t, "page 0 is the next to adopt, not 1", func() { Adopt(cross(cur), 1) })
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.ClearFlag(cross(cur), hlgnotPath, hlmailbox.FnPayouts)
	if !hlmailbox.Paused(pkgPath, hlmailbox.FnPayouts) {
		t.Fatalf("the copied flag cleared with the predecessor's")
	}
	samePair(t, want, "before page 0")

	// --- page 0, by a stranger: the lock, the counters and 500 records move in one call; noted pulled; no re-point
	testing.SetRealm(testing.NewUserRealm(stranger1))
	Adopt(cross(cur), 0)
	if Imported() != 1 || !Importing() || Locked() != want.lock || Balance() != want.lock || hlgnot.Locked() != 0 || hlgnot.Balance() != 0 || !hlmailbox.Pulled(pkgPath) {
		t.Fatalf("page 0: imported %d importing %v, lock %d balance %d, hlgnot %d/%d, pulled %v", Imported(), Importing(), Locked(), Balance(), hlgnot.Locked(), hlgnot.Balance(), hlmailbox.Pulled(pkgPath))
	}
	samePair(t, want, "page 0")
	if c := st.ClaimOf(erinBurnID); c == nil || c.State != hlroute.Open || c.Remaining() != 27_500*gnot || st.ClaimOf(aliceBurnID).State != hlroute.Done || st.SweptOf(bobBurnID) != 3*gnot || st.Sends() != 497 || st.SendOf(bobSend) == nil || !st.SendOf(bobSend).Imported {
		t.Fatalf("page 0's records: claims %v, sends %d", c, st.Sends())
	}
	if !strings.Contains(hlgnot.Render("json"), "\"sends\":2,") || !strings.Contains(Render("json"), "\"importing\":true,\"imported\":1,") || !strings.Contains(Render(""), "- succeeds `"+hlgnotPath+"`: 1 pages adopted, more to come (anyone may call Adopt(1)); nothing is sent or credited before the last page") {
		t.Fatalf("after page 0: %s\n%s", hlgnot.Render("json"), Render(""))
	}
	testing.SetRealm(testing.NewCodeRealm(hlgnotY))
	hlmailbox.AnnounceSuccessor(cross(cur), hlmailbox.SymbolGNOT)
	asMember(cur)
	aborts(t, "migrated to "+pkgPath+": the successor pulled 1 pages already (the lock moved with page 0); a migration under way is never re-pointed", func() { hlgnot.NewMigrateRequest(cross(cur), hlgnotY) })

	// --- between pages: still no send, no credit, no page out of order; payouts under the copied flag, then empty meters
	envelope(pkgPath, 10*gnot, Fee(), false)
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "nothing is sent before the last page of the migration is in (1 pages so far)", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 10*gnot) })
	asRelayer(cur)
	aborts(t, "nothing is credited before the last page of the migration is in (1 pages so far)", func() { Process(cross(cur), "", toSelf) })
	asRelayer(cur)
	aborts(t, "(1 pages so far)", func() { Process(cross(cur), "", toX) })
	testing.SetRealm(testing.NewUserRealm(stranger2))
	aborts(t, "page 1 is the next to adopt, not 0", func() { Adopt(cross(cur), 0) })
	testing.SetRealm(testing.NewUserRealm(stranger2))
	aborts(t, "page 1 is the next to adopt, not 2", func() { Adopt(cross(cur), 2) })
	asRelayer(cur)
	aborts(t, "hlgnot2: payouts are paused", func() { Claim(cross(cur), 20) })
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.ClearFlag(cross(cur), pkgPath, hlmailbox.FnPayouts)
	asRelayer(cur)
	if n := Claim(cross(cur), 20); n != 0 || !strings.Contains(Render("json"), "\"meter\":0,") {
		t.Fatalf("the meters after page 0 (R34): paid %d claims; %s", n, Render("json"))
	}
	samePair(t, want, "between pages")

	// --- the last page, by another stranger: the import ends; the predecessor holds nothing
	testing.SetRealm(testing.NewUserRealm(stranger2))
	Adopt(cross(cur), 1)
	if Imported() != 2 || Importing() || st.Sends() != 499 || st.SendOf(aliceSend) == nil || st.SendOf(carolSend) == nil || !strings.Contains(hlgnot.Render("json"), "\"sends\":0,\"refunded\":0,") || !strings.Contains(Render("json"), "\"importing\":false,\"imported\":2,") {
		t.Fatalf("the last page: imported %d importing %v sends %d", Imported(), Importing(), st.Sends())
	}
	samePair(t, want, "the last page")
	identity(t, supply, "the last page")
	testing.SetRealm(testing.NewUserRealm(stranger2))
	aborts(t, "the migration's last page is in already: nothing more to adopt", func() { Adopt(cross(cur), 2) })
	if !strings.Contains(Render(""), "- succeeds `"+hlgnotPath+"`: the migration is complete (2 pages adopted); a route like any other") {
		t.Fatalf("page: %s", Render(""))
	}

	// --- a route like any other: it takes what is addressed to the dropped route, to the predecessor and to itself
	// (AQ1, R35), done once in the shared mailbox, and it sends
	asRelayer(cur)
	Process(cross(cur), "", toX)
	asRelayer(cur)
	Process(cross(cur), "", toPred)
	asRelayer(cur)
	Process(cross(cur), "", toSelf)
	if st.ClaimOf(toXID) == nil || st.ClaimOf(toPredID) == nil || st.ClaimOf(toSelfID) == nil || hlmailbox.Delivered(toXID) == 0 || st.Owed != want.owed+210*gnot {
		t.Fatalf("the three burns: owed %d", st.Owed)
	}
	asRelayer(cur)
	aborts(t, "already delivered", func() { Process(cross(cur), "", toX) })
	supply -= 210 * gnot
	// --- no send before r/hlgov has dispatched the kind 6 enrolling this realm (AT7): the token program refuses a
	// sender that is not its enrolled router, so a send before it would park on Solana until the kind 6 lands —
	// weeks under a hold, never if GovDAO cancels it — unrefundable as the route's own; then kind 6 enrols this
	// realm (the note the refund waits 14 days on) and it sends
	envelope(pkgPath, 10*gnot, Fee(), false)
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "this route succeeds "+hlgnotPath+": nothing is sent before r/hlgov has dispatched the kind 6 enrolling this realm as the token's router on Solana (no enrolment note yet): a send before it would park there until a kind 6 lands, unrefundable as the route's own", func() {
		TransferRemote(cross(cur), solanaTestnet, solWallet, 10*gnot)
	})
	if Locked() != want.lock || st.Sends() != 499 {
		t.Fatalf("a send before the kind 6 moved something: lock %d sends %d", Locked(), st.Sends())
	}
	asMember(cur)
	passGov(cur, hlgov.NewEnrollRouterRequest(cross(cur), 0, hlmailbox.LocalDomain(), hex.EncodeToString(k2[:])))
	h6, at6 := hlmailbox.Enrolled(pkgPath)
	if h6 == 0 || at6 == 0 {
		t.Fatalf("not enrolled: %d %d", h6, at6)
	}
	envelope(pkgPath, 10*gnot, Fee(), true)
	testing.SetRealm(testing.NewUserRealm(alice))
	ownSend := TransferRemote(cross(cur), solanaTestnet, solWallet, 10*gnot)
	supply += 10 * gnot
	if Locked() != want.lock+10*gnot || st.SendOf(ownSend).Imported || Balance() != Locked() {
		t.Fatalf("the successor's own send: lock %d balance %d", Locked(), Balance())
	}
	identity(t, supply, "the successor's own send")
	skipDays(1)
	asRelayer(cur)
	if n := Claim(cross(cur), 20); n != 4 || balance(frank) != 195*gnot || balance(erin) != 2_495*gnot+2_500*gnot {
		t.Fatalf("a day's payouts on the successor: %d claims, frank %d erin %d", n, balance(frank), balance(erin))
	}
	identity(t, supply, "a day's payouts")
	if hlgnot.Balance() != 0 || Balance() != Locked() {
		t.Fatalf("balances: hlgnot %d, this %d against a lock of %d", hlgnot.Balance(), Balance(), Locked())
	}

	// --- a refund of an imported send waits 14 days on kind 6's note (AR1, R37): 13 days refused, 14 taken
	asMember(cur)
	aborts(t, "kind 6 enrolling this route was dispatched at height "+strconv.FormatInt(h6, 10)+", ", func() { NewRefundRequest(cross(cur), bobSend) })
	skipDays(12) // 13 days and a few seconds after the note
	asMember(cur)
	aborts(t, "a refund waits 14 days after it (until "+strconv.FormatInt(at6+hlroute.EnrolmentWait, 10)+")", func() { NewRefundRequest(cross(cur), bobSend) })
	skipDays(1)
	// the gate open, the ids are judged: this realm's own send is never refunded (AQ2), an imported one is
	asMember(cur)
	aborts(t, "is this route's own send: a stranded send waits for a kind 12 on Solana, never a refund", func() { NewRefundRequest(cross(cur), ownSend) })
	asMember(cur)
	req := NewRefundRequest(cross(cur), bobSend)
	if !strings.Contains(req.Description(), "refund 20000 GNOT of sends imported from the predecessor (14 days after kind 6's dispatch enrolled this realm) to their senders: "+bobSend) {
		t.Fatalf("description: %s", req.Description())
	}
	pass(cur, req)
	refund := st.Ledger.Last()
	if !st.Ledger.Get(refund).Queued() || st.SendOf(bobSend).Refunded != 0 {
		t.Fatalf("a refund applied at once")
	}
	// the apply is GovDAO's own at-once act after the 7 days (AT2): the builder refuses before them, Execute
	// refuses the kind — bob, whose id the description names, cannot apply his own refund — and the apply names
	// the refund decision and its ids
	asMember(cur)
	aborts(t, "refund decision "+strconv.FormatInt(refund, 10)+" is queued until", func() { NewApplyRefundRequest(cross(cur), refund) })
	skipDays(7)
	b0, l0 := balance(bob), Locked()
	testing.SetRealm(testing.NewUserRealm(bob))
	aborts(t, "a refund is applied by GovDAO's own at-once decision (apply refund) after hlwatch's Solana check, never by Execute", func() { Execute(cross(cur), refund) })
	if balance(bob) != b0 || Locked() != l0 || st.SendOf(bobSend).Refunded != 0 || !st.Ledger.Get(refund).Pending() {
		t.Fatalf("Execute applied a refund: bob +%d lock %d", balance(bob)-b0, Locked())
	}
	asMember(cur)
	areq := NewApplyRefundRequest(cross(cur), refund)
	if !strings.Contains(areq.Description(), "apply refund decision "+strconv.FormatInt(refund, 10)+" at once (refund 20000 GNOT of sends imported from the predecessor (14 days after kind 6's dispatch enrolled this realm) to their senders: "+bobSend+"), after hlwatch's check that the token's router on Solana is this realm") {
		t.Fatalf("description: %s", areq.Description())
	}
	pass(cur, areq)
	if balance(bob)-b0 != 20_000*gnot || Locked() != l0-20_000*gnot || st.SendOf(bobSend).Refunded == 0 || st.Ledger.Get(refund).State() != "applied" || Render("refunds/0") != "["+Render("send/"+bobSend)+"]" {
		t.Fatalf("the refund: bob +%d lock %d", balance(bob)-b0, Locked())
	}
	supply -= 20_000 * gnot // never minted after all
	identity(t, supply, "the refund")

	// --- the governor yields (M1k): every decision so far — the two migrates, the kind 6, the refund and its apply
	// — was its own, by Govern, and a GovDAO proposal was refused; from here GovDAO's proposals decide and the
	// governor's Govern is refused, the rest of the drill running as proposals
	asMember(cur)
	refusedByDAO(cur, t, "write off", NewWriteOffRequest(cross(cur), bobSend))
	if st.SendOf(bobSend).WrittenOff != 0 {
		t.Fatalf("GovDAO wrote off before the yield")
	}
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	hlmailbox.Yield(cross(cur))
	h := hlmailbox.Yielded()
	if h == 0 || !strings.Contains(Render(""), "- governor: gno.land/r/gov/dao since height "+strconv.FormatInt(h, 10)+" ("+string(launchGovernor)+" yielded)\n") || !strings.Contains(hlgnot.Render(""), "- governor: gno.land/r/gov/dao since height "+strconv.FormatInt(h, 10)) {
		t.Fatalf("after the yield: %d\n%s", h, Render(""))
	}
	asMember(cur)
	wreq := NewWriteOffRequest(cross(cur), bobSend)
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	aborts(t, "hlgnot2: the governor yielded at height "+strconv.FormatInt(h, 10)+", gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never Govern", func() { Govern(cross(cur), wreq) })
	if st.SendOf(bobSend).WrittenOff != 0 {
		t.Fatalf("the governor wrote off after the yield")
	}

	// --- a write-off (the send delivered after its refund, say) and its restore (R38) move lost both ways
	asMember(cur)
	pass(cur, NewWriteOffRequest(cross(cur), bobSend))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if _, _, _, _, lost := Counters(); lost != 20_000*gnot || st.SendOf(bobSend).WrittenOff == 0 {
		t.Fatalf("the write-off: lost %d", lost)
	}
	identity(t, supply+20_000*gnot, "the write-off")
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), bobSend))
	restore := st.Ledger.Last()
	asRelayer(cur)
	aborts(t, "is due at", func() { Execute(cross(cur), restore) })
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), restore)
	if _, _, _, _, lost := Counters(); lost != 0 || st.SendOf(bobSend).WrittenOff != 0 || st.SendOf(bobSend).Refunded == 0 || !strings.Contains(st.Ledger.Get(restore).Note, "1 restored (1 write-off undone: the send is lost no more)") {
		t.Fatalf("the restore: lost %d note %q", lost, st.Ledger.Get(restore).Note)
	}
	identity(t, supply, "the restore")

	// --- a burn to a registered route's address is swept and credited to its owner by id (R39)
	other := namespace + "hlbubble7"
	testing.SetRealm(testing.NewCodeRealm(hlbubblePath))
	hlmailbox.AddRoute(cross(cur), other)
	ob := bytes32(chain.PackageAddress(other))
	envelope(pkgPath, 10*gnot, Fee(), false)
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "the recipient is a realm of the bridge", func() { TransferRemote(cross(cur), solanaTestnet, hex.EncodeToString(ob[:]), 10*gnot) })
	toRoute, toRouteID := burn(7, Address(), chain.PackageAddress(other), 150*gnot)
	owed0 := st.Owed
	asRelayer(cur)
	Process(cross(cur), "", toRoute)
	if st.SweptOf(toRouteID) != 150*gnot || st.ClaimOf(toRouteID) != nil || st.Owed != owed0+150*gnot {
		t.Fatalf("a burn to a route's address was not swept: %d", st.SweptOf(toRouteID))
	}
	supply -= 150 * gnot
	identity(t, supply, "the swept burn")
	asMember(cur)
	aborts(t, "outside the bridge's realms", func() { NewCreditRequest(cross(cur), toRouteID, chain.PackageAddress(other), 150*gnot) })
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), toRouteID, frank, 150*gnot))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if c := st.ClaimOf(toRouteID); st.SweepOf(toRouteID) != nil || c == nil || c.To != frank || c.By != hlroute.ByVote || st.Owed != owed0+150*gnot {
		t.Fatalf("the credit by id: %+v", c)
	}
	identity(t, supply, "the credit by id")

	// --- a DROPPED route's address stays a realm of the bridge (AT6): hlgnotx's page printed it as the lock's holder
	// to be for a week, and the mailbox's tombstone keeps it — a burn to it is swept and credited to its owner by
	// id, a send to it refused, as for a registered route's
	xa := chain.PackageAddress(firstX)
	if hlmailbox.Dropped(firstX) != hlgnotPath || hlmailbox.IsRoute(firstX) {
		t.Fatalf("hlgnotx is not a dropped route: %q", hlmailbox.Dropped(firstX))
	}
	envelope(pkgPath, 10*gnot, Fee(), false)
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "the recipient is a realm of the bridge", func() { TransferRemote(cross(cur), solanaTestnet, hex.EncodeToString(kx[:]), 10*gnot) })
	toDropped, toDroppedID := burn(8, Address(), xa, 40*gnot)
	owed0 = st.Owed
	asRelayer(cur)
	Process(cross(cur), "", toDropped)
	if st.SweptOf(toDroppedID) != 40*gnot || st.ClaimOf(toDroppedID) != nil || st.Owed != owed0+40*gnot {
		t.Fatalf("a burn to a dropped route's address was not swept: swept %d, claim %v", st.SweptOf(toDroppedID), st.ClaimOf(toDroppedID))
	}
	supply -= 40 * gnot
	identity(t, supply, "the swept burn to a dropped route's address")
	asMember(cur)
	aborts(t, "outside the bridge's realms", func() { NewCreditRequest(cross(cur), toDroppedID, xa, 40*gnot) })
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), toDroppedID, frank, 40*gnot))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if c := st.ClaimOf(toDroppedID); st.SweepOf(toDroppedID) != nil || c == nil || c.To != frank || c.By != hlroute.ByVote || st.Owed != owed0+40*gnot {
		t.Fatalf("the credit by id of the dropped route's burn: %+v", c)
	}
	identity(t, supply, "the credit by id of the dropped route's burn")

	// --- this realm migrates on in its turn: a successor announced for GNOT, 7 days, its Pull noted pulled
	testing.SetRealm(testing.NewCodeRealm(hlgnot3))
	hlmailbox.AnnounceSuccessor(cross(cur), hlmailbox.SymbolGNOT)
	asMember(cur)
	pass(cur, NewMigrateRequest(cross(cur), hlgnot3))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if Migrated() != hlgnot3 || hlmailbox.Predecessor(hlgnot3) != pkgPath || hlmailbox.Successor(pkgPath) != hlgnot3 {
		t.Fatalf("migrated on: %s", Migrated())
	}
	envelope(pkgPath, 10*gnot, Fee(), false)
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "migrated to "+hlgnot3, func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 10*gnot) })
	asRelayer(cur)
	aborts(t, "migrated to "+hlgnot3+": the successor takes this message", func() { Process(cross(cur), "", toSelf) })
	lock := Locked()
	testing.SetRealm(testing.NewUserRealm(stranger1))
	aborts(t, "only the successor", func() { Pull(cross(cur), 0) })
	testing.SetRealm(testing.NewCodeRealm(hlgnot3))
	snap := Pull(cross(cur), 0)
	if snap.Lock != lock || Locked() != 0 || balance(chain.PackageAddress(hlgnot3)) != lock || !hlmailbox.Pulled(hlgnot3) || !strings.Contains(Render(""), "**migrated** to `"+hlgnot3+"`") {
		t.Fatalf("the next hop's page 0: lock %d → %d, pulled %v", lock, balance(chain.PackageAddress(hlgnot3)), hlmailbox.Pulled(hlgnot3))
	}
}

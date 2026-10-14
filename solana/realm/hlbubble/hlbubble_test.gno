package hlbubble

import (
	"chain"
	"chain/runtime"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"gno.land/p/nt/testutils/v0"
	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlmailbox"
	"gno.land/r/gov/dao"
	govinit "gno.land/r/gov/dao/init/v0"
)

// The shared state machine is tested in p/hlroute and the GovDAO path in r/hlgnot; these tests cover what is
// BUBBLE's own: the allowance, the token moving on sends, claims and fees, and the swept burn below the fee. Who
// decides follows the chain's own order (M1k): the compiled governor makes every decision directly through Govern
// from TestSetup to TestBuilders, then TestYield hands over to GovDAO and the tests after run as proposals — pass
// picks the path by the yield.

const (
	solanaTestnet = uint32(1399811150)
	testVerifier  = namespace + "hltestverifier"
	dayHeights    = 86400 / 5
	solWallet     = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
)

var (
	// premineTo holds bubble's premine from that realm's init: the tests' faucet.
	premineTo = address("g1ecsuj0q572jr0dhu29q9njtnmw03hyu7tyyvv6")
	member    = testutils.TestAddress("hl-member")
	alice     = testutils.TestAddress("hlb-alice")
	bob       = testutils.TestAddress("hlb-bob")
	carol     = testutils.TestAddress("hlb-carol")
	relayer   = testutils.TestAddress("hlb-relayer")
	fees      = address("")
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

// anyVerifier takes every message: the last test crafts messages the vectors do not carry (a burn whose recipient
// is a successor route's address), so the signatures are not its subject.
type anyVerifier struct{}

func (anyVerifier) Verify(origin uint32, metadata, message []byte) error { return nil }
func (anyVerifier) Satisfiable(signers []hlmailbox.Key) bool             { return true }

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
// GovDAO's proposal — created by the member, voted yes, executed — after it.
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

// passMailbox is pass for the mailbox's own requests (the launch, the verifier).
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
// created and voted, its execution aborts on the governor's rule, and nothing is recorded.
func refusedByDAO(cur realm, t *testing.T, what string, req Request) {
	t.Helper()
	n := st.Ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlbubble: until the governor yields, "+string(launchGovernor)+" alone decides, by Govern from its own key: not gno.land/r/gov/dao (a GovDAO proposal is refused until the yield)", func() { dao.ExecuteProposal(cross(cur), pid) })
	if st.Ledger.Last() != n {
		t.Fatalf("%s: a refused proposal recorded a decision", what)
	}
}

func asMember(cur realm)  { testing.SetRealm(testing.NewUserRealm(member)) }
func asRelayer(cur realm) { testing.SetRealm(testing.NewUserRealm(relayer)) }
func skipDays(n int64)    { testing.SkipHeights(n*dayHeights + 1) }

func fund(cur realm, who address, n int64) {
	testing.SetRealm(testing.NewUserRealm(premineTo))
	bubble.Transfer(cross(cur), who, n)
}

func TestLaunchValues(cur realm, t *testing.T) {
	route, _ := hl.ParseKey32(testRoute)
	if hlmailbox.Live() || Locked() != 0 || Fee() != 100*one || Minimum() != 200*one || FeeFloor() != 40*one || CapFloor() != 10_000*one ||
		Maximum() != 25_000*one || Domain() != solanaTestnet || Router() != hex.EncodeToString(route[:]) {
		t.Fatalf("launch values")
	}
	if !strings.Contains(Render(""), "fee 100 BUBBLE (floor 40 BUBBLE); minimum 200 BUBBLE; per-message maximum 25000 BUBBLE") {
		t.Fatalf("page: %s", Render(""))
	}
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "not launched", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 200*one) })
}

func TestSetup(cur realm, t *testing.T) {
	fees = hlmailboxFees()
	asMember(cur)
	govinit.InitWithUsers(cross(cur), member)
	testing.SetRealm(testing.NewCodeRealm(testVerifier))
	hlmailbox.RegisterVerifier(cross(cur), verifier2of3{})
	asMember(cur)
	passMailbox(cur, hlmailbox.NewLaunchRequest(cross(cur)))
	// the meter is shown as the launch reset leaves it — empty — before any Claim applied the reset (K22)
	if !strings.Contains(Render("json"), "\"meter\":0,") || !strings.Contains(Render(""), "the bridge meter holds 0 BUBBLE now") {
		t.Fatalf("the meter after Launch: %s", Render("json"))
	}
	asMember(cur)
	passMailbox(cur, hlmailbox.NewSetVerifierRequest(cross(cur), solanaTestnet, testVerifier))
	skipDays(7)
	asRelayer(cur)
	hlmailbox.Execute(cross(cur), 2)
	if !hlmailbox.Live() || hlmailbox.ActiveVerifier(solanaTestnet) != testVerifier {
		t.Fatalf("setup")
	}
}

func hlmailboxFees() address { return realms[2] }

// TestTransferRemote: the allowance covers amount + fee; the lock takes the amount, the fee realm the fee.
func TestTransferRemote(cur realm, t *testing.T) {
	fund(cur, alice, 1_000*one)
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "below the minimum", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 199*one) })
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "allowance exceeded", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 300*one) })
	testing.SetRealm(testing.NewUserRealm(alice))
	bubble.Approve(cross(cur), Address(), 400*one)
	n0 := hlmailbox.Count()
	testing.SetRealm(testing.NewUserRealm(alice))
	id := TransferRemote(cross(cur), solanaTestnet, solWallet, 300*one)
	if Locked() != 300*one || Balance() != 300*one || bubble.BalanceOf(alice) != 600*one || bubble.BalanceOf(fees) != 100*one || hlmailbox.Count() != n0+1 {
		t.Fatalf("locked %d balance %d alice %d fees %d", Locked(), Balance(), bubble.BalanceOf(alice), bubble.BalanceOf(fees))
	}
	rawHex, _ := hlmailbox.Message(n0)
	m, _ := hl.ParseMessage(unhex(rawHex))
	self, _ := hl.AddressToBytes32(Address())
	wallet, _ := hl.ParseKey32(solWallet)
	route, _ := hl.ParseKey32(testRoute)
	if m.Sender != self || m.Recipient != route || string(m.Body) != string(hl.TokenMessage(wallet, uint64(300*one), nil)) || idOf(rawHex) != id {
		t.Fatalf("message %+v", m)
	}
	// the rest of the allowance (0) does not cover another
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "allowance exceeded", func() { TransferRemote(cross(cur), solanaTestnet, solWallet, 200*one) })
}

// TestProcessAndClaim: a burn comes back as a claim paid under the meters in BUBBLE; a burn below the 100 BUBBLE
// fee is swept.
func TestProcessAndClaim(cur realm, t *testing.T) {
	// the lock is 300 BUBBLE: under the floor, the cap is 10,000, a quarter 2,500
	asRelayer(cur)
	Process(cross(cur), backAlice300Meta, backAlice300Msg)
	asRelayer(cur)
	aborts(t, "not from the router", func() { Process(cross(cur), strangerRouteMeta, strangerRouteMsg) })
	asRelayer(cur)
	Process(cross(cur), backCarol50Meta, backCarol50Msg)
	if st.ClaimOf(idOf(backAlice300Msg)) == nil || st.SweptOf(idOf(backCarol50Msg)) != 50*one || st.Owed != 350*one {
		t.Fatalf("credited: owed %d", st.Owed)
	}
	asRelayer(cur)
	if Claim(cross(cur), 20) != 0 {
		t.Fatalf("paid from an empty meter")
	}
	a0, f0 := bubble.BalanceOf(alice), bubble.BalanceOf(fees)
	skipDays(1)
	asRelayer(cur)
	Claim(cross(cur), 20)
	if bubble.BalanceOf(alice)-a0 != 200*one || bubble.BalanceOf(fees)-f0 != 100*one || Locked() != 0 || Balance() != 0 {
		t.Fatalf("alice +%d fees +%d locked %d balance %d", bubble.BalanceOf(alice)-a0, bubble.BalanceOf(fees)-f0, Locked(), Balance())
	}
	// a burn above the lock (500 against a lock of 0) cannot be honest: swept, owed, paying nobody
	asRelayer(cur)
	Process(cross(cur), backAlice500Meta, backAlice500Msg)
	if st.SweptOf(idOf(backAlice500Msg)) != 500*one || st.ClaimOf(idOf(backAlice500Msg)) != nil || st.Owed != 550*one {
		t.Fatalf("a burn above the lock: swept %d owed %d", st.SweptOf(idOf(backAlice500Msg)), st.Owed)
	}
	// a sweep payment never exceeds the lock (R11): carol's 50 against a lock of 0
	asMember(cur)
	aborts(t, "the entries sum to 50 BUBBLE, above the lock's 0 BUBBLE", func() { NewSweepRequest(cross(cur), idOf(backCarol50Msg), carol) })
	// a recipient whose top twelve bytes are not zero is no gno address (§13): the credit is swept — owed, paying
	// nobody until GovDAO credits it by id to its owner — never landed on the address its last twenty bytes spell.
	// A send first keeps the lock above the 250, so it is the recipient and not the lock that sweeps it
	fund(cur, bob, 1_000*one)
	testing.SetRealm(testing.NewUserRealm(bob))
	bubble.Approve(cross(cur), Address(), 1_000*one)
	testing.SetRealm(testing.NewUserRealm(bob))
	TransferRemote(cross(cur), solanaTestnet, solWallet, 900*one)
	asRelayer(cur)
	Process(cross(cur), dirtyBob250Meta, dirtyBob250Msg)
	if st.SweptOf(idOf(dirtyBob250Msg)) != 250*one || st.ClaimOf(idOf(dirtyBob250Msg)) != nil {
		t.Fatalf("a recipient with dirt in the padding was credited: swept %d", st.SweptOf(idOf(dirtyBob250Msg)))
	}
	b0 := bubble.BalanceOf(bob)
	skipDays(1)
	asRelayer(cur)
	Claim(cross(cur), 20)
	if bubble.BalanceOf(bob)-b0 != 0 || Locked() != 900*one {
		t.Fatalf("bob +%d locked %d", bubble.BalanceOf(bob)-b0, Locked())
	}
	sent, paid, owed, voided, lost := Counters()
	if sent != 1_200*one || paid != 300*one || owed != 800*one || voided != 0 || lost != 0 || sent-paid != Locked() {
		t.Fatalf("counters %d %d %d %d %d", sent, paid, owed, voided, lost)
	}
	if !strings.Contains(Render("json"), "\"symbol\":\"BUBBLE\"") || !strings.Contains(Render(""), "`"+Router()+"`") {
		t.Fatalf("pages")
	}
	_ = carol
	_ = hlroute.Open
}

// TestBuilders: one at-once and one queued decision, the BUBBLE amounts, and the sweep paid in BUBBLE.
func TestBuilders(cur realm, t *testing.T) {
	asMember(cur)
	aborts(t, "never goes below the launch floor", func() { NewSetCapFloorRequest(cross(cur), 9_999*one) })
	// a migrate never names a route the mailbox registers already (R31): the GNOT route here, whatever it announced
	asMember(cur)
	aborts(t, "the successor "+hlgnotPath+" is a route already", func() { NewMigrateRequest(cross(cur), hlgnotPath) })
	// a refund waits for kind 6's enrolment of this route (AR1), and a write-off takes only a refunded send
	asMember(cur)
	aborts(t, "no kind 6 has enrolled this route on Solana yet", func() { NewRefundRequest(cross(cur), strings.Repeat("ab", 32)) })
	asMember(cur)
	aborts(t, "is not a refunded send: only a send delivered on Solana after its refund is written off", func() { NewWriteOffRequest(cross(cur), strings.Repeat("ab", 32)) })
	asMember(cur)
	pass(cur, NewSetFeeRequest(cross(cur), 80*one, 200*one))
	if Fee() != 80*one || st.Ledger.Get(1).Queued() {
		t.Fatalf("a fee lowering waited")
	}
	asMember(cur)
	pass(cur, NewSweepRequest(cross(cur), idOf(backCarol50Msg), carol))
	if !st.Ledger.Get(2).Queued() {
		t.Fatalf("a sweep applied at once")
	}
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), 2)
	if bubble.BalanceOf(carol) != 50*one || st.Sweep != 750*one || Locked() != 850*one || st.SweepOf(idOf(backCarol50Msg)) != nil {
		t.Fatalf("sweep: carol %d sweep %d locked %d", bubble.BalanceOf(carol), st.Sweep, Locked())
	}
	if !strings.Contains(Render("decisions"), "### sweep") {
		t.Fatalf("decisions page")
	}
	// the fee and the cap floor's quarter bound each other: the fee builder refuses a fee above the quarter, the
	// cap-floor builder a floor whose quarter is below the fee, and a queued fee raise finds the floor lowered in
	// between is refused when it would apply
	asMember(cur)
	aborts(t, "never goes above a quarter of the cap floor (2500 BUBBLE)", func() { NewSetFeeRequest(cross(cur), 2_501*one, 200*one) })
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 20_000*one))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	asMember(cur)
	pass(cur, NewSetFeeRequest(cross(cur), 5_000*one, 200*one)) // a quarter of 20,000: queued
	raise := st.Ledger.Last()
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 12_000*one)) // a lowering, at once: the quarter is 3,000 now
	if CapFloor() != 12_000*one || Fee() != 80*one {
		t.Fatalf("cap floor %d fee %d", CapFloor(), Fee())
	}
	skipDays(7)
	asRelayer(cur)
	aborts(t, "above a quarter of the cap floor", func() { Execute(cross(cur), raise) })
	if Fee() != 80*one || !st.Ledger.Get(raise).Pending() {
		t.Fatalf("a fee above the quarter applied: %d", Fee())
	}
	asMember(cur)
	pass(cur, NewSetFeeRequest(cross(cur), 3_000*one, 200*one))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	asMember(cur)
	aborts(t, "quarter never goes below the fee of 3000 BUBBLE", func() { NewSetCapFloorRequest(cross(cur), 11_000*one) })
	if Fee() != 3_000*one || CapFloor() != 12_000*one {
		t.Fatalf("fee %d cap floor %d", Fee(), CapFloor())
	}
	// R16: a queued sweep payment (the dirty-address burn's 250, to bob), due, waits under a standing payouts flag
	// and goes through after the clear
	asMember(cur)
	pass(cur, NewSweepRequest(cross(cur), idOf(dirtyBob250Msg), bob))
	sweep := st.Ledger.Last()
	skipDays(7)
	g1 := hlmailbox.Seat(1)
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, "")
	asRelayer(cur)
	aborts(t, "payouts are paused", func() { Execute(cross(cur), sweep) })
	if !st.Ledger.Get(sweep).Pending() || st.Sweep != 750*one {
		t.Fatalf("a sweep paid under the flag")
	}
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.ClearFlag(cross(cur), pkgPath, hlmailbox.FnPayouts)
	b0 := bubble.BalanceOf(bob)
	asRelayer(cur)
	Execute(cross(cur), sweep)
	if bubble.BalanceOf(bob)-b0 != 250*one || st.Sweep != 500*one || Locked() != 600*one || st.SweepOf(idOf(dirtyBob250Msg)) != nil {
		t.Fatalf("sweep after the clear: bob +%d sweep %d locked %d", bubble.BalanceOf(bob)-b0, st.Sweep, Locked())
	}
}

// TestYield (M1k): before the yield every builder's request GovDAO's proxy executes is refused — the one closure
// every builder shares refuses the proxy first, so the eleven requests this state lets a builder make stand for
// the fifteen (a restore, a refund and an apply refund need a voided claim and an enrolment note this test does
// not make; TestApplyRefund runs the refund pair under GovDAO, hlgnot2's drill under the governor) — and nothing
// is recorded; then the governor yields on the mailbox, Govern is closed to it and a GovDAO proposal decides, on
// the same clocks.
func TestYield(cur realm, t *testing.T) {
	// the governor queues a raise, so a cancel can be requested; nothing of it survives this test
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 13_000*one))
	raise := st.Ledger.Last()
	if !st.Ledger.Get(raise).Queued() || CapFloor() != 12_000*one {
		t.Fatalf("the governor's raise applied at once")
	}
	announced := namespace + "hlbubblem1k"
	testing.SetRealm(testing.NewCodeRealm(announced))
	hlmailbox.AnnounceSuccessor(cross(cur), hlmailbox.SymbolBUBBLE)
	fresh, entry := strings.Repeat("9b", 32), idOf(backAlice500Msg)
	for _, r := range []struct {
		what string
		mk   func() Request
	}{
		{"set cap floor", func() Request { return NewSetCapFloorRequest(cross(cur), 14_000*one) }},
		{"set maximum", func() Request { return NewSetMaximumRequest(cross(cur), 24_000*one) }},
		{"set fee and minimum", func() Request { return NewSetFeeRequest(cross(cur), 2_000*one, 200*one) }},
		{"set fee floor", func() Request { return NewSetFeeFloorRequest(cross(cur), 30*one) }},
		{"set router", func() Request { return NewSetRouterRequest(cross(cur), strings.Repeat("ab", 32)) }},
		{"void", func() Request { return NewVoidRequest(cross(cur), "since:1", true, "", "") }},
		{"credit", func() Request { return NewCreditRequest(cross(cur), fresh, bob, 10*one) }},
		{"sweep", func() Request { return NewSweepRequest(cross(cur), entry, carol) }},
		{"write off", func() Request { return NewWriteOffRequest(cross(cur), strings.Repeat("ab", 32)) }},
		{"migrate", func() Request { return NewMigrateRequest(cross(cur), announced) }},
		{"cancel", func() Request { return NewCancelRequest(cross(cur), raise) }},
	} {
		if r.what == "write off" {
			continue // a write-off names a refunded send, which this state has none of: refused when made, by the builder
		}
		asMember(cur)
		refusedByDAO(cur, t, r.what, r.mk())
	}
	if CapFloor() != 12_000*one || Maximum() != 25_000*one || Fee() != 3_000*one || FeeFloor() != 40*one || st.SweepOf(entry) == nil || Migrated() != "" || !st.Ledger.Get(raise).Pending() || hlmailbox.Delivered(fresh) != 0 {
		t.Fatalf("a refused proposal changed something")
	}
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), raise))
	if st.Ledger.Get(raise).State() != "cancelled" {
		t.Fatalf("the governor's cancel")
	}
	if !strings.Contains(Render(""), "- governor: "+string(launchGovernor)+" (until the yield) — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused\n") || !strings.Contains(Render("json"), "\"live\":true,\"governor\":\""+string(launchGovernor)+"\",\"yielded\":0,") {
		t.Fatalf("page: %s\n%s", Render(""), Render("json"))
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
	req := NewSetCapFloorRequest(cross(cur), 13_000*one)
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	aborts(t, "hlbubble: the governor yielded at height "+itoa(h)+", gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never Govern", func() { Govern(cross(cur), req) })
	if st.Ledger.Last() != n {
		t.Fatalf("Govern recorded after the yield")
	}
	// GovDAO's proposal decides now (pass takes that path from here on): queued as the governor's was, then cancelled
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 13_000*one))
	raise = st.Ledger.Last()
	if d := st.Ledger.Get(raise); !d.Queued() || d.Due != d.Recorded+decisions.Delay || CapFloor() != 12_000*one {
		t.Fatalf("GovDAO's raise after the yield: %+v", d)
	}
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), raise))
	if st.Ledger.Get(raise).State() != "cancelled" || CapFloor() != 12_000*one {
		t.Fatalf("GovDAO's cancel after the yield")
	}
}

// TestRouteAddressSwept (R39): every route realm the mailbox registers is a realm of the bridge — a successor's
// address, which its page prints as the holder of the lock and a holder pastes as a deposit address, is refused
// as a send's recipient, and a burn to it is swept and credited to its owner by id, never credited as a claim and
// paid into a realm's account outside every counter. hlgnot7 stands for a successor (registered as a migrate's
// apply registers one); the messages are crafted under a verifier that takes anything.
func TestRouteAddressSwept(cur realm, t *testing.T) {
	anyPath := namespace + "hlanyverifier"
	testing.SetRealm(testing.NewCodeRealm(anyPath))
	hlmailbox.RegisterVerifier(cross(cur), anyVerifier{})
	asMember(cur)
	passMailbox(cur, hlmailbox.NewSetVerifierRequest(cross(cur), solanaTestnet, anyPath))
	skipDays(7)
	asRelayer(cur)
	hlmailbox.Execute(cross(cur), decisionsOf(hlmailbox.Render("json")))
	if hlmailbox.ActiveVerifier(solanaTestnet) != anyPath {
		t.Fatalf("verifier %s", hlmailbox.ActiveVerifier(solanaTestnet))
	}
	succ := namespace + "hlgnot7"
	testing.SetRealm(testing.NewCodeRealm(hlgnotPath))
	hlmailbox.AddRoute(cross(cur), succ)
	sb, _ := hl.AddressToBytes32(chain.PackageAddress(succ))
	fund(cur, alice, 1_000*one)
	testing.SetRealm(testing.NewUserRealm(alice))
	bubble.Approve(cross(cur), Address(), 1_000*one)
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "a realm of the bridge", func() { TransferRemote(cross(cur), solanaTestnet, hex.EncodeToString(sb[:]), 300*one) })
	// a burn of 150 to the successor's address: swept, owed, paying nobody
	self, _ := hl.AddressToBytes32(Address())
	m := hl.Message{Version: hl.Version, Nonce: 90, Origin: solanaTestnet, Sender: st.Router, Destination: hlmailbox.LocalDomain(), Recipient: self, Body: hl.TokenMessage(sb, uint64(150*one), nil)}
	raw := m.Bytes()
	idb := hl.ID(raw)
	id := hex.EncodeToString(idb[:])
	owed0, lock0 := st.Owed, Locked()
	asRelayer(cur)
	Process(cross(cur), "", hex.EncodeToString(raw))
	if st.SweptOf(id) != 150*one || st.ClaimOf(id) != nil || st.Owed != owed0+150*one || Locked() != lock0 {
		t.Fatalf("a burn to a successor's address was not swept: swept %d, claim %v", st.SweptOf(id), st.ClaimOf(id))
	}
	// then credited to its owner by id: the entry gone, a claim of 150 to bob in its place
	asMember(cur)
	aborts(t, "outside the bridge's realms", func() { NewCreditRequest(cross(cur), id, chain.PackageAddress(succ), 150*one) })
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), id, bob, 150*one))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if c := st.ClaimOf(id); st.SweepOf(id) != nil || c == nil || c.To != bob || c.By != hlroute.ByVote || c.Amount != 150*one || st.Owed != owed0+150*one {
		t.Fatalf("the credit by id: %+v owed %d", c, st.Owed)
	}
	sent, paid, owed, voided, lost := Counters()
	if sent-paid != Locked() || owed != st.Owed || voided != 0 || lost != 0 {
		t.Fatalf("counters %d %d %d %d %d", sent, paid, owed, voided, lost)
	}
	// a DROPPED route's address stays a realm of the bridge (AT6): hlgnot's migration is re-pointed to hlgnot8, so
	// hlgnot7 is a route no more — a holder who pasted its address while its page printed it as the lock's holder
	// to be is swept and credited by id all the same, and a send to it is refused
	next := namespace + "hlgnot8"
	testing.SetRealm(testing.NewCodeRealm(hlgnotPath))
	hlmailbox.AddRoute(cross(cur), next)
	if hlmailbox.Dropped(succ) != hlgnotPath || hlmailbox.IsRoute(succ) || !hlmailbox.IsRoute(next) {
		t.Fatalf("hlgnot7 is not a dropped route: %q", hlmailbox.Dropped(succ))
	}
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "a realm of the bridge", func() { TransferRemote(cross(cur), solanaTestnet, hex.EncodeToString(sb[:]), 300*one) })
	m = hl.Message{Version: hl.Version, Nonce: 91, Origin: solanaTestnet, Sender: st.Router, Destination: hlmailbox.LocalDomain(), Recipient: self, Body: hl.TokenMessage(sb, uint64(120*one), nil)}
	raw = m.Bytes()
	idb = hl.ID(raw)
	id2 := hex.EncodeToString(idb[:])
	owed0 = st.Owed
	asRelayer(cur)
	Process(cross(cur), "", hex.EncodeToString(raw))
	if st.SweptOf(id2) != 120*one || st.ClaimOf(id2) != nil || st.Owed != owed0+120*one {
		t.Fatalf("a burn to a dropped route's address was not swept: swept %d, claim %v", st.SweptOf(id2), st.ClaimOf(id2))
	}
	asMember(cur)
	aborts(t, "outside the bridge's realms", func() { NewCreditRequest(cross(cur), id2, chain.PackageAddress(succ), 120*one) })
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), id2, carol, 120*one))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if c := st.ClaimOf(id2); st.SweepOf(id2) != nil || c == nil || c.To != carol || c.By != hlroute.ByVote || c.Amount != 120*one || st.Owed != owed0+120*one {
		t.Fatalf("the credit by id of the dropped route's burn: %+v owed %d", c, st.Owed)
	}
	sent, paid, owed, voided, lost = Counters()
	if sent-paid != Locked() || owed != st.Owed || voided != 0 || lost != 0 {
		t.Fatalf("counters %d %d %d %d %d", sent, paid, owed, voided, lost)
	}
}

// TestApplyRefund (AT2): a refund's apply is GovDAO's own at-once act, never Execute's — over a send a
// predecessor's page brought (carol's 300, handed to this realm as page 0 would, its BUBBLE with it), the
// enrolment note 14 days old (r/hlgov's own call, stood in for). Last, and past the 35 days TestBuilders ran to,
// since the bubble realm's checkpoints refuse a clock that goes backwards on an account (this realm's, stamped by
// the sweep payment there) and every test function's clock starts over.
func TestApplyRefund(cur realm, t *testing.T) {
	testing.SetRealm(testing.NewCodeRealm(hlgovPath))
	hlmailbox.NoteEnrolled(cross(cur), pkgPath, runtime.ChainHeight())
	skipDays(36)
	sendID := strings.Repeat("e3", 32)
	st.Import(now(), 0, hlmailbox.LaunchedAt(), &hlroute.Snapshot{Page: 0, Lock: 300 * one, SentTotal: 300 * one, Sends: []hlroute.SendItem{{ID: sendID, From: carol, Amount: 300 * one, At: now()}}})
	fund(cur, Address(), 300*one)
	asMember(cur)
	aborts(t, "decision 2 is of kind sweep, not refund: an apply refund names a queued refund decision", func() { NewApplyRefundRequest(cross(cur), 2) })
	asMember(cur)
	pass(cur, NewRefundRequest(cross(cur), sendID))
	refund := st.Ledger.Last()
	asMember(cur)
	aborts(t, "refund decision "+itoa(refund)+" is queued until", func() { NewApplyRefundRequest(cross(cur), refund) })
	skipDays(7)
	c0, l0 := bubble.BalanceOf(carol), Locked()
	asRelayer(cur)
	aborts(t, "a refund is applied by GovDAO's own at-once decision (apply refund) after hlwatch's Solana check, never by Execute", func() { Execute(cross(cur), refund) })
	if bubble.BalanceOf(carol) != c0 || Locked() != l0 || st.SendOf(sendID).Refunded != 0 || !st.Ledger.Get(refund).Pending() {
		t.Fatalf("Execute applied a refund: carol +%d locked %d", bubble.BalanceOf(carol)-c0, Locked())
	}
	asMember(cur)
	pass(cur, NewApplyRefundRequest(cross(cur), refund))
	if bubble.BalanceOf(carol)-c0 != 300*one || Locked() != l0-300*one || st.SendOf(sendID).Refunded == 0 || st.Ledger.Get(refund).State() != "applied" || !strings.Contains(Render("decisions"), "### apply refund") {
		t.Fatalf("the apply: carol +%d locked %d", bubble.BalanceOf(carol)-c0, Locked())
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

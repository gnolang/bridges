package hlbubble

import (
	"chain"
	"encoding/hex"
	"strings"
	"testing"

	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
	"gno.land/r/gov/dao"
)

// The round-3 realms audit's E3-RL3 and E3-CF7(b), and E3-RL6's titles, run last: the verifier that takes anything is
// active since TestRouteAddressSwept, so the burns are crafted here, sent by the router the route holds.

var burnNonce = uint32(400)

// burnTo processes a burn of amount to k (32 bytes) from the route's router, with a nonce of its own, and returns
// its id.
func burnTo(cur realm, k [32]byte, amount int64) string {
	self, _ := hl.AddressToBytes32(Address())
	m := hl.Message{Version: hl.Version, Nonce: burnNonce, Origin: ethTest, Sender: st.Router, Destination: hlmailbox.LocalDomain(), Recipient: self, Body: hl.TokenMessage(k, uint64(amount), nil)}
	burnNonce++
	raw := m.Bytes()
	asRelayer(cur)
	Process(cross(cur), "", hex.EncodeToString(raw))
	idb := hl.ID(raw)
	return hex.EncodeToString(idb[:])
}

// lockUp sends amount of alice's BUBBLE to Ethereum, so the lock holds what the burns after it carry.
func lockUp(cur realm, amount int64) {
	fund(cur, alice, amount+Fee())
	testing.SetRealm(testing.NewUserRealm(alice))
	bubble.Approve(cross(cur), Address(), amount+Fee())
	testing.SetRealm(testing.NewUserRealm(alice))
	TransferRemote(cross(cur), ethTest, ethWallet, amount)
}

// TestAnnouncedSuccessorSwept (E3-RL3): a realm that announced itself to the mailbox as a route's successor, which no
// migrate has registered yet — its page naming it the lock's holder to be — is a realm of the bridge from its
// announcement: a burn naming it is swept (a claim's amount, as the control burn to carol shows), its twenty bytes
// are refused as a send's recipient and its address as a vote's payee; the swept burn is credited by id to its owner.
// Before, the burn was a claim paid into the realm's account, outside every counter of the line.
func TestAnnouncedSuccessorSwept(cur realm, t *testing.T) {
	skipDays(170) // past every account checkpoint the earlier test functions stamped (the bubble realm's clock rule)
	lockUp(cur, Minimum())
	amount := Fee() + one
	next := namespace + "hlbubblenext"
	a := chain.PackageAddress(next)
	k, _ := hl.AddressToBytes32(a)
	kc, _ := hl.AddressToBytes32(carol)
	if unpayable(a) || amount > Locked() {
		t.Fatalf("payable before its announcement: %v; the burns carry %d, the lock %d", unpayable(a), amount, Locked())
	}
	testing.SetRealm(testing.NewCodeRealm(next))
	hlmailbox.AnnounceSuccessor(cross(cur), hlmailbox.SymbolBUBBLE)
	if id := burnTo(cur, kc, amount); st.ClaimOf(id) == nil || st.SweptOf(id) != 0 {
		t.Fatalf("the control: a burn to carol was not a claim")
	}
	owed0, sweep0, lock0 := st.Owed, st.Sweep, Locked()
	id := burnTo(cur, k, amount)
	if st.SweptOf(id) != amount || st.ClaimOf(id) != nil || st.Owed != owed0+amount || st.Sweep != sweep0+amount || Locked() != lock0 || hlmailbox.IsRoute(next) {
		t.Fatalf("a burn to an announced successor was not swept: swept %d, claim %v", st.SweptOf(id), st.ClaimOf(id))
	}
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "hlbubble: the recipient is a realm of the bridge", func() { TransferRemote(cross(cur), ethTest, hex.EncodeToString(k[:]), Minimum()) })
	asMember(cur)
	aborts(t, "hlbubble: the recipient must be an address outside the bridge's realms", func() { NewCreditRequest(cross(cur), id, a, amount) })
	asMember(cur)
	aborts(t, "hlbubble: the recipient must be an address outside the bridge's realms", func() { NewSweepRequest(cross(cur), id, a) })
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), id, bob, amount))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if c := st.ClaimOf(id); c == nil || c.To != bob || st.SweepOf(id) != nil {
		t.Fatalf("the burn credited to its owner: %+v", c)
	}
	checkWaiting(t, "TestAnnouncedSuccessorSwept")
}

// registerRoute registers path as a route of the mailbox, as the migrate of the route at by does (AddRoute); a second
// call from the same route re-points its migration, the path before dropped (a route no more, a realm of the bridge
// still). The realm is set in this frame, right before the call, as the other helpers set theirs.
func registerRoute(cur realm, by, path string) {
	testing.SetRealm(testing.NewCodeRealm(by))
	hlmailbox.AddRoute(cross(cur), path)
}

// TestVotePayeeAtApply (E3-CF7(b), F9-5): a credit or a sweep vote asks unpayable of its payee when it is made, again
// when its proposal executes, and once more when its 7 days have passed (hlgnot's test says more). Here the payees
// become realms of the bridge as routes hlgnot's migrate registers (a re-point drops the one before, a realm still):
// at Execute the credit and the sweep stay pending, nothing moved and the payee unpaid; at the proposal's execution the
// vote is refused; a payable sweep applies; GovDAO cancels the stale votes and credits the id afresh.
func TestVotePayeeAtApply(cur realm, t *testing.T) {
	skipDays(200) // past TestAnnouncedSuccessorSwept's checkpoints (the bubble realm's clock rule)
	amount := Fee() + one
	lockUp(cur, 3*amount)
	creditID, sweepID, okID := burnTo(cur, [32]byte{}, amount), burnTo(cur, [32]byte{}, amount), burnTo(cur, [32]byte{}, amount)
	for _, id := range []string{creditID, sweepID, okID} {
		if st.SweptOf(id) != amount {
			t.Fatalf("the zero burn %s holds %d", id, st.SweptOf(id))
		}
	}
	pc, ps, pd := namespace+"hlpayeec", namespace+"hlpayees", namespace+"hlpayeed"
	ac, as, ad := chain.PackageAddress(pc), chain.PackageAddress(ps), chain.PackageAddress(pd)
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), creditID, ac, amount))
	credit := st.Ledger.Last()
	asMember(cur)
	pass(cur, NewSweepRequest(cross(cur), sweepID, as))
	sweep := st.Ledger.Last()
	asMember(cur)
	pass(cur, NewSweepRequest(cross(cur), okID, bob))
	ok := st.Ledger.Last()
	registerRoute(cur, hlgnotPath, pc)
	registerRoute(cur, hlgnotPath, ps)
	if !unpayable(ac) || !unpayable(as) || unpayable(bob) {
		t.Fatalf("the payees: %v %v", unpayable(ac), unpayable(as))
	}
	skipDays(7)
	lock0, owed0, sweep0 := Locked(), st.Owed, st.Sweep
	asRelayer(cur)
	aborts(t, "hlbubble: decision "+itoa(credit)+" pays "+string(ac)+", which is a realm of the bridge now", func() { Execute(cross(cur), credit) })
	asRelayer(cur)
	aborts(t, "hlbubble: decision "+itoa(sweep)+" pays "+string(as)+", which is a realm of the bridge now", func() { Execute(cross(cur), sweep) })
	if !st.Ledger.Get(credit).Pending() || !st.Ledger.Get(sweep).Pending() || st.ClaimOf(creditID) != nil || st.SweptOf(creditID) != amount || st.SweptOf(sweepID) != amount ||
		bubble.BalanceOf(as) != 0 || bubble.BalanceOf(ac) != 0 || Locked() != lock0 || st.Owed != owed0 || st.Sweep != sweep0 {
		t.Fatalf("a vote paid a realm of the bridge: credit %s, claim %v, paid %d %d, lock %d (was %d)", st.Ledger.Get(credit).State(), st.ClaimOf(creditID), bubble.BalanceOf(ac), bubble.BalanceOf(as), Locked(), lock0)
	}
	b0 := bubble.BalanceOf(bob)
	asRelayer(cur)
	Execute(cross(cur), ok)
	if bubble.BalanceOf(bob)-b0 != amount || st.SweepOf(okID) != nil || Locked() != lock0-amount {
		t.Fatalf("the payable sweep: +%d, entry %v", bubble.BalanceOf(bob)-b0, st.SweepOf(okID))
	}
	asMember(cur)
	req := NewCreditRequest(cross(cur), creditID, ad, amount)
	registerRoute(cur, hlgnotPath, pd)
	n0 := st.Ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlbubble: the vote pays "+string(ad)+", which is a realm of the bridge now", func() { dao.ExecuteProposal(cross(cur), pid) })
	if st.Ledger.Last() != n0 {
		t.Fatalf("a refused vote recorded a decision")
	}
	for _, id := range []int64{credit, sweep} {
		asMember(cur)
		pass(cur, NewCancelRequest(cross(cur), id))
		if st.Ledger.Get(id).State() != "cancelled" {
			t.Fatalf("decision %d: %s", id, st.Ledger.Get(id).State())
		}
	}
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), creditID, carol, amount))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if c := st.ClaimOf(creditID); c == nil || c.To != carol || st.SweepOf(creditID) != nil {
		t.Fatalf("the id credited afresh: %+v", c)
	}
	checkWaiting(t, "TestVotePayeeAtApply")
}

// TestTitles (E3-RL6): every proposal this realm's builders make is titled "eth/hlbubble: " and its kind.
func TestTitles(cur realm, t *testing.T) {
	asMember(cur)
	p := NewSetCapFloorRequest(cross(cur), CapFloor()+1_000*one).Proposal()
	asMember(cur)
	c := NewCreditRequest(cross(cur), strings.Repeat("7e", 32), carol, one).Proposal()
	if p.Title() != "eth/hlbubble: set cap floor" || c.Title() != "eth/hlbubble: credit" || !strings.HasPrefix(c.Description(), "credit "+strings.Repeat("7e", 32)+" to "+string(carol)) {
		t.Fatalf("titles %q %q", p.Title(), c.Title())
	}
	checkWaiting(t, "TestTitles")
}

// TestVerifierSwept (E4-RL5): a verifier activated after launch — the one that takes anything, active since
// TestRouteAddressSwept, and hltestverifier before it, replaced since — is a realm of the bridge like the launch
// hlverifier: a burn naming it is swept (the control burn to carol is a claim), its twenty bytes are refused as a send's
// recipient and its address as a vote's payee. Before, each burn was a claim paid into a realm that moves no coins.
func TestVerifierSwept(cur realm, t *testing.T) {
	skipDays(250) // past TestVotePayeeAtApply's checkpoints (the bubble realm's clock rule)
	lockUp(cur, Minimum())
	amount := Fee() + one
	kc, _ := hl.AddressToBytes32(carol)
	if id := burnTo(cur, kc, amount); st.ClaimOf(id) == nil || st.SweptOf(id) != 0 {
		t.Fatalf("the control: a burn to carol was not a claim")
	}
	anyPath := namespace + "hlanyverifier"
	if hlmailbox.ActiveVerifier(ethTest) != anyPath {
		t.Fatalf("the verifier that takes anything is not the active one: %s", hlmailbox.ActiveVerifier(ethTest))
	}
	for _, path := range []string{anyPath, testVerifier} {
		a := chain.PackageAddress(path)
		k, _ := hl.AddressToBytes32(a)
		sweep0 := st.Sweep
		id := burnTo(cur, k, amount)
		if st.SweptOf(id) != amount || st.ClaimOf(id) != nil || st.Sweep != sweep0+amount {
			t.Fatalf("a burn to the verifier %s was not swept: swept %d, claim %v", path, st.SweptOf(id), st.ClaimOf(id))
		}
		testing.SetRealm(testing.NewUserRealm(alice))
		aborts(t, "hlbubble: the recipient is a realm of the bridge", func() { TransferRemote(cross(cur), ethTest, hex.EncodeToString(k[:]), Minimum()) })
		asMember(cur)
		aborts(t, "hlbubble: the recipient must be an address outside the bridge's realms", func() { NewCreditRequest(cross(cur), id, a, amount) })
	}
	checkWaiting(t, "TestVerifierSwept")
}

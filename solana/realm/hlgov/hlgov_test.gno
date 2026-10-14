package hlgov

import (
	"chain"
	"chain/runtime"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"gno.land/p/nt/testutils/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlgnot"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlmailbox"
	"gno.land/r/gov/dao"
	govinit "gno.land/r/gov/dao/init/v0"
)

const dayHeights = 86400 / 5

var (
	member   = testutils.TestAddress("hl-member")
	stranger = testutils.TestAddress("hlv-other")
	keyA     = strings.Repeat("aa", 32)
	keyB     = strings.Repeat("bb", 32)
	keyC     = strings.Repeat("cc", 32)
	keyD     = strings.Repeat("dd", 32)
	zeros    = strings.Repeat("00", 32)
)

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

// pass makes a builder's decision by whoever governs now (M1k): the governor's own Govern before the yield — this
// suite's TestKinds and TestGuardians — and GovDAO's proposal, voted and executed, after it (TestYield hands over).
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

// passGnot is pass for the GNOT route's requests (the maximum kind 12 is checked against).
func passGnot(cur realm, req hlgnot.Request) {
	if hlmailbox.Yielded() == 0 {
		testing.SetRealm(testing.NewUserRealm(launchGovernor))
		hlgnot.Govern(cross(cur), req)
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

// passMailbox is pass for the mailbox's own requests (the launch).
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
// created and voted, its execution aborts on the governor's rule, and nothing is recorded or dispatched.
func refusedByDAO(cur realm, t *testing.T, what string, req Request) {
	t.Helper()
	n, last := ledger.Last(), Last()
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlgov: until the governor yields, "+string(launchGovernor)+" alone decides, by Govern from its own key: not gno.land/r/gov/dao (a GovDAO proposal is refused until the yield)", func() { dao.ExecuteProposal(cross(cur), pid) })
	if ledger.Last() != n || Last() != last {
		t.Fatalf("%s: a refused proposal recorded or dispatched something", what)
	}
}

func asMember(cur realm) { testing.SetRealm(testing.NewUserRealm(member)) }

// lastMessage is the newest message the mailbox dispatched, parsed.
func lastMessage(t *testing.T) hl.Message {
	t.Helper()
	raw, _ := hlmailbox.Message(hlmailbox.Count() - 1)
	b, _ := hex.DecodeString(raw)
	m, err := hl.ParseMessage(b)
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	return m
}

// bodyHex is kind ‖ seq ‖ params in hex.
func bodyHex(kind byte, n uint64, params string) string {
	seq := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		seq[i] = byte(n)
		n >>= 8
	}
	return hex.EncodeToString([]byte{kind}) + hex.EncodeToString(seq) + params
}

func TestLaunchValues(cur realm, t *testing.T) {
	if hlmailbox.Live() || Start() != 0 || Last() != 0 || Next() != 1 || Recipient() != launchRecipient || Destination() != 1399811150 || SolSeat(1) != launchSolSeat1 || SolSeat(6) != launchSolSeat6 || SolSeat(7) != "" {
		t.Fatalf("launch values: last %d recipient %s", Last(), Recipient())
	}
	p := Render("")
	for _, want := range []string{"**not launched**", "recipient `" + launchRecipient + "`", "launch client: `" + launchClient + "`", "kind 4 is reserved", "sequence: starts at 0 (compiled", "last number 0; the next takes number 1; 0 sent from here", "seat 3 (guardian): `" + launchSolSeat3 + "`", "seat 5 (watch key)", "- clock factor 1 (1 on mainnet; a devnet build is compiled with 8)"} {
		if !strings.Contains(p, want) {
			t.Fatalf("page lacks %q:\n%s", want, p)
		}
	}
	if !strings.Contains(Render("json"), "\"start\":0,\"last\":0,\"next\":1,") || !strings.Contains(Render("json"), "\"live\":false,\"governor\":\""+string(launchGovernor)+"\",\"yielded\":0,") {
		t.Fatalf("json: %s", Render("json"))
	}
	if !strings.Contains(p, "- governor: "+string(launchGovernor)+" (until the yield) — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused\n") {
		t.Fatalf("page lacks the governor:\n%s", p)
	}
	asMember(cur)
	aborts(t, "kind 4 is reserved", func() { NewSetClientLayoutRequest(cross(cur), launchClient, 1) })
	asMember(cur)
	aborts(t, "not launched", func() { NewUnpauseRequest(cross(cur), 0) })
	asMember(cur)
	aborts(t, "not launched", func() { NewSetMintLimitsRequest(cross(cur), 1, 1, 1) })
	asMember(cur)
	aborts(t, "not launched", func() { NewCancelRequest(cross(cur), "1") })
	asMember(cur)
	aborts(t, "not launched", func() { NewSyncSequenceRequest(cross(cur), 5) })
	asMember(cur)
	aborts(t, "not launched", func() { NewSetDecisionRecipientRequest(cross(cur), keyA) })
	asMember(cur)
	aborts(t, "is not pending", func() { NewCancelQueuedRequest(cross(cur), 1) })
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "no decision 1", func() { Redispatch(cross(cur), 1) })
}

// TestKinds: every kind's builder encodes §13's body and dispatches it with the next number at execution.
func TestKinds(cur realm, t *testing.T) {
	asMember(cur)
	govinit.InitWithUsers(cross(cur), member)
	asMember(cur)
	passMailbox(cur, hlmailbox.NewLaunchRequest(cross(cur)))
	self, _ := hl.AddressToBytes32(Address())

	// kind 2
	asMember(cur)
	aborts(t, "0 (the pausable ISM) or 1", func() { NewUnpauseRequest(cross(cur), 2) })
	n0 := hlmailbox.Count()
	asMember(cur)
	pass(cur, NewUnpauseRequest(cross(cur), 1))
	m := lastMessage(t)
	if hlmailbox.Count() != n0+1 || m.Sender != self || m.Destination != 1399811150 || hex.EncodeToString(m.Recipient[:]) != launchRecipient || hex.EncodeToString(m.Body) != bodyHex(2, 1, "01") {
		t.Fatalf("unpause: %+v body %x", m, m.Body)
	}
	if Last() != 1 || Body(1) != bodyHex(2, 1, "01") || Render("decision/1") == "null" || !strings.Contains(Render("decision/1"), "\"count\":1") {
		t.Fatalf("recorded: last %d body %s", Last(), Body(1))
	}
	d := ledger.Get(1)
	if d.Kind != "unpause" || d.State() != "applied" || !strings.Contains(d.Note, "dispatched as decision 1") {
		t.Fatalf("decision %+v", d)
	}
	// kind 3
	asMember(cur)
	aborts(t, "must be the pausable ISM", func() { NewSetTokenISMRequest(cross(cur), 0, keyA) })
	asMember(cur)
	aborts(t, "0 (GNOT) or 1 (BUBBLE)", func() { NewSetTokenISMRequest(cross(cur), 2, zeros) })
	asMember(cur)
	pass(cur, NewSetTokenISMRequest(cross(cur), 1, zeros))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(3, 2, "01"+zeros) {
		t.Fatalf("set token ISM: %x", lastMessage(t).Body)
	}
	asMember(cur)
	pass(cur, NewSetTokenISMRequest(cross(cur), 0, launchPausableISM))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(3, 3, "00"+launchPausableISM) {
		t.Fatalf("set token ISM: %x", lastMessage(t).Body)
	}
	// kind 4: reserved — refused whatever the parameters, and the counter does not move
	asMember(cur)
	aborts(t, "kind 4 is reserved", func() { NewSetClientLayoutRequest(cross(cur), launchClient, 2) })
	asMember(cur)
	aborts(t, "kind 4 is reserved", func() { NewSetClientLayoutRequest(cross(cur), keyA, 0) })
	if Last() != 3 || ledger.Last() != 3 {
		t.Fatalf("kind 4 moved the counter: %d, decisions %d", Last(), ledger.Last())
	}
	// kind 6, 7, 8, 9. For gno.land's own domain (1198419796 here) the router must be a route realm hlmailbox
	// registers, as 32 bytes; another domain takes any key
	routeGNOT, _ := hl.AddressToBytes32(chain.PackageAddress(namespace + "hlgnot"))
	rk := hex.EncodeToString(routeGNOT[:])
	asMember(cur)
	aborts(t, "must be set", func() { NewEnrollRouterRequest(cross(cur), 0, 0, keyA) })
	asMember(cur)
	aborts(t, "a route realm registered with hlmailbox", func() { NewEnrollRouterRequest(cross(cur), 0, 1198419796, keyA) })
	asMember(cur)
	aborts(t, "a route realm registered with hlmailbox", func() { NewEnrollRouterRequest(cross(cur), 0, 1198419796, hex.EncodeToString(self[:])) })
	asMember(cur)
	if req := NewEnrollRouterRequest(cross(cur), 1, 7, keyA); !strings.Contains(req.Description(), "domain 7: "+keyA) {
		t.Fatalf("a foreign domain's router: %s", req.Description())
	}
	asMember(cur)
	if h, at := hlmailbox.Enrolled(namespace + "hlgnot"); h != 0 || at != 0 {
		t.Fatalf("enrolled before kind 6: %d %d", h, at)
	}
	// the router must be the token's own route (R36): GNOT's kind 6 naming the BUBBLE route, or BUBBLE's naming
	// the GNOT route, is refused when made — a slip there would strand one token's burns behind per-id votes for
	// two weeks and write the enrolment note on the other token's route, re-opening AR1's double mint — and no
	// note lands
	routeBUBBLE, _ := hl.AddressToBytes32(chain.PackageAddress(namespace + "hlbubble"))
	asMember(cur)
	aborts(t, namespace+"hlbubble is the BUBBLE route's, not GNOT's: a kind 6 names the token's own route", func() { NewEnrollRouterRequest(cross(cur), 0, 1198419796, hex.EncodeToString(routeBUBBLE[:])) })
	asMember(cur)
	aborts(t, namespace+"hlgnot is the GNOT route's, not BUBBLE's: a kind 6 names the token's own route", func() { NewEnrollRouterRequest(cross(cur), 1, 1198419796, rk) })
	if h, _ := hlmailbox.Enrolled(namespace + "hlbubble"); h != 0 || Last() != 3 {
		t.Fatalf("a refused kind 6 left something: enrolled %d, last %d", h, Last())
	}
	pass(cur, NewEnrollRouterRequest(cross(cur), 0, 1198419796, rk))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(6, 4, "00"+"476e6f54"+rk) {
		t.Fatalf("enroll router: %x", lastMessage(t).Body)
	}
	// the closure notes the enrolment with the mailbox — the route and the height — for a route of this chain
	// (AR1: a route's refund waits 7 days after it); the decision's note says so
	if h, at := hlmailbox.Enrolled(namespace + "hlgnot"); h != runtime.ChainHeight() || at == 0 || !strings.Contains(ledger.Get(ledger.Last()).Note, "dispatched as decision 4; "+namespace+"hlgnot noted enrolled on Solana at height "+strconv.FormatInt(h, 10)) {
		t.Fatalf("enrolled: %d %d, note %q", h, at, ledger.Get(ledger.Last()).Note)
	}
	if h, _ := hlmailbox.Enrolled(namespace + "hlbubble"); h != 0 {
		t.Fatalf("the other route noted: %d", h)
	}
	asMember(cur)
	aborts(t, "at most 50000000 lamports", func() { NewSetProtocolFeeRequest(cross(cur), 50_000_001, keyB) })
	asMember(cur)
	aborts(t, "beneficiary of zeros", func() { NewSetProtocolFeeRequest(cross(cur), 1, zeros) })
	asMember(cur)
	pass(cur, NewSetProtocolFeeRequest(cross(cur), 256, keyB))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(7, 5, "0000000000000100"+keyB) {
		t.Fatalf("set protocol fee: %x", lastMessage(t).Body)
	}
	asMember(cur)
	aborts(t, "must be set and distinct", func() { NewUpgradeRequest(cross(cur), keyA, keyA, keyC) })
	asMember(cur)
	aborts(t, "never the admin program's own id", func() { NewUpgradeRequest(cross(cur), launchRecipient, keyB, keyC) })
	asMember(cur)
	pass(cur, NewUpgradeRequest(cross(cur), keyA, keyB, keyC))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(8, 6, keyA+keyB+keyC) {
		t.Fatalf("upgrade: %x", lastMessage(t).Body)
	}
	asMember(cur)
	aborts(t, "not the current admin program", func() { NewTransferToSuccessorRequest(cross(cur), launchRecipient) })
	asMember(cur)
	pass(cur, NewTransferToSuccessorRequest(cross(cur), keyD))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(9, 7, keyD) {
		t.Fatalf("transfer: %x", lastMessage(t).Body)
	}
	if Last() != 7 || !strings.Contains(Render("json"), "\"last\":7,\"next\":8") {
		t.Fatalf("counter %d", Last())
	}
}

// TestGuardians: kind 5 takes at most four entries, two guardian seats; a seat's current key as last set here is
// warned about in the description, not refused (the admin program spends such a decision itself and the mirror
// can drift); the mirror follows what was sent.
func TestGuardians(cur realm, t *testing.T) {
	asMember(cur)
	aborts(t, "seat:pubkey", func() { NewSetGuardiansRequest(cross(cur), "1") })
	asMember(cur)
	aborts(t, "must be 1 to 6", func() { NewSetGuardiansRequest(cross(cur), "7:"+keyA) })
	asMember(cur)
	aborts(t, "named twice", func() { NewSetGuardiansRequest(cross(cur), "1:"+keyA+",1:"+keyB) })
	asMember(cur)
	aborts(t, "pubkey named twice", func() { NewSetGuardiansRequest(cross(cur), "1:"+keyA+",2:"+keyA) })
	asMember(cur)
	aborts(t, "pubkey of zeros", func() { NewSetGuardiansRequest(cross(cur), "1:"+zeros) })
	asMember(cur)
	aborts(t, "1 to 4 seats", func() { NewSetGuardiansRequest(cross(cur), "1:"+keyA+",2:"+keyB+",5:"+keyC+",6:"+keyD+",3:"+strings.Repeat("ee", 32)) })
	asMember(cur)
	aborts(t, "two guardian seats at most", func() { NewSetGuardiansRequest(cross(cur), "1:"+keyA+",2:"+keyB+",3:"+keyC) })
	asMember(cur)
	if req := NewSetGuardiansRequest(cross(cur), "1:"+launchSolSeat2); !strings.Contains(req.Description(), "(warning: "+launchSolSeat2+" holds seat 2 as last set here") {
		t.Fatalf("a seat's current key was not warned about: %s", req.Description())
	}
	asMember(cur)
	req := NewSetGuardiansRequest(cross(cur), "1:"+keyA+",5:"+keyB)
	if strings.Contains(req.Description(), "warning") {
		t.Fatalf("a warning for fresh keys: %s", req.Description())
	}
	pass(cur, req)
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(5, 8, "02"+"01"+keyA+"05"+keyB) {
		t.Fatalf("set guardians: %x", lastMessage(t).Body)
	}
	if SolSeat(1) != keyA || SolSeat(5) != keyB || SolSeat(2) != launchSolSeat2 {
		t.Fatalf("mirror %s %s", SolSeat(1), SolSeat(5))
	}
	asMember(cur)
	if req := NewSetGuardiansRequest(cross(cur), "2:"+keyA+",6:"+keyB); !strings.Contains(req.Description(), "(warning: "+keyA+" holds seat 1 as last set here; "+keyB+" holds seat 5 as last set here — the admin program spends") {
		t.Fatalf("the mirror's warning: %s", req.Description())
	}
}

// TestYield (M1k): before the yield every builder's request GovDAO's proxy executes is refused — the one closure
// every builder shares refuses the proxy first — and nothing is recorded or dispatched (the numbers the tests
// after count on stand); then the governor yields on the mailbox, Govern is closed to it and a GovDAO proposal
// decides: a recipient swap queued as the governor's was, and cancelled, nothing dispatched.
func TestYield(cur realm, t *testing.T) {
	routeGNOT, _ := hl.AddressToBytes32(chain.PackageAddress(namespace + "hlgnot"))
	rk := hex.EncodeToString(routeGNOT[:])
	// the governor queues a recipient swap, so a cancel of a queued change can be requested; nothing of it survives
	asMember(cur)
	pass(cur, NewSetDecisionRecipientRequest(cross(cur), keyC))
	queued := ledger.Last()
	if !ledger.Get(queued).Queued() || Recipient() != launchRecipient {
		t.Fatalf("the governor's swap applied at once")
	}
	last := Last()
	for _, r := range []struct {
		what string
		mk   func() Request
	}{
		{"unpause", func() Request { return NewUnpauseRequest(cross(cur), 1) }},
		{"set token ISM", func() Request { return NewSetTokenISMRequest(cross(cur), 1, zeros) }},
		{"set guardians", func() Request { return NewSetGuardiansRequest(cross(cur), "2:"+keyC) }},
		{"enroll router", func() Request { return NewEnrollRouterRequest(cross(cur), 0, 1198419796, rk) }},
		{"set protocol fee", func() Request { return NewSetProtocolFeeRequest(cross(cur), 256, keyB) }},
		{"upgrade", func() Request { return NewUpgradeRequest(cross(cur), keyA, keyB, keyC) }},
		{"transfer to successor", func() Request { return NewTransferToSuccessorRequest(cross(cur), keyD) }},
		{"set mint limits", func() Request { return NewSetMintLimitsRequest(cross(cur), 1, 25_000_000_000, 100_000_000_000) }},
		{"sync sequence", func() Request { return NewSyncSequenceRequest(cross(cur), last+2) }},
		{"set decision recipient", func() Request { return NewSetDecisionRecipientRequest(cross(cur), keyD) }},
		{"cancel", func() Request { return NewCancelRequest(cross(cur), "1") }},
		{"cancel queued", func() Request { return NewCancelQueuedRequest(cross(cur), queued) }},
	} {
		asMember(cur)
		refusedByDAO(cur, t, r.what, r.mk())
	}
	if Last() != last || Recipient() != launchRecipient || !ledger.Get(queued).Pending() {
		t.Fatalf("a refused proposal changed something: last %d", Last())
	}
	asMember(cur)
	pass(cur, NewCancelQueuedRequest(cross(cur), queued))
	if ledger.Get(queued).State() != "cancelled" || Last() != last {
		t.Fatalf("the governor's cancel of a queued change")
	}
	// the yield, on the mailbox: this realm reads it
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	hlmailbox.Yield(cross(cur))
	h := hlmailbox.Yielded()
	if h == 0 || !strings.Contains(Render(""), "- governor: gno.land/r/gov/dao since height "+itoa(h)+" ("+string(launchGovernor)+" yielded)\n") || !strings.Contains(Render("json"), "\"governor\":\""+string(launchGovernor)+"\",\"yielded\":"+itoa(h)+",") {
		t.Fatalf("after the yield: %d\n%s", h, Render(""))
	}
	n := ledger.Last()
	asMember(cur)
	req := NewSetDecisionRecipientRequest(cross(cur), keyC)
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	aborts(t, "hlgov: the governor yielded at height "+itoa(h)+", gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never Govern", func() { Govern(cross(cur), req) })
	if ledger.Last() != n || Last() != last {
		t.Fatalf("Govern recorded after the yield")
	}
	// GovDAO's proposal decides now (pass takes that path from here on)
	asMember(cur)
	pass(cur, NewSetDecisionRecipientRequest(cross(cur), keyC))
	queued = ledger.Last()
	if d := ledger.Get(queued); !d.Queued() || Recipient() != launchRecipient {
		t.Fatalf("GovDAO's swap after the yield: %+v", d)
	}
	asMember(cur)
	pass(cur, NewCancelQueuedRequest(cross(cur), queued))
	if ledger.Get(queued).State() != "cancelled" || Last() != last {
		t.Fatalf("GovDAO's cancel after the yield")
	}
}

// cancelBody checks the newest message is §13's cancel body for decision n — kind 1 ‖ seq ‖ an 8-byte salt ‖ params
// — and that the realm kept it; it returns the salt in hex (never all zeros).
func cancelBody(t *testing.T, n uint64, params string) string {
	t.Helper()
	b := hex.EncodeToString(lastMessage(t).Body)
	if len(b) != 18+16+len(params) || b[:18] != bodyHex(1, n, "") || b[34:] != params {
		t.Fatalf("cancel #%d: body %s, want %s ‖ salt ‖ %s", n, b, bodyHex(1, n, ""), params)
	}
	salt := b[18:34]
	if salt == strings.Repeat("00", 8) || Body(n) != b {
		t.Fatalf("cancel #%d: salt %s, kept body %s", n, salt, Body(n))
	}
	return salt
}

// TestCancelSequenceRedispatch: a cancel names up to 16 numbers already dispatched and its body starts with a salt
// drawn at execution, so two cancels of one list differ; set sequence moves the counter at once, and only ever up;
// anyone re-dispatches a decision until it is recorded.
func TestCancelSequenceRedispatch(cur realm, t *testing.T) {
	asMember(cur)
	aborts(t, "not a decision number", func() { NewCancelRequest(cross(cur), "0") })
	asMember(cur)
	aborts(t, "named twice", func() { NewCancelRequest(cross(cur), "1,1") })
	asMember(cur)
	aborts(t, "was never dispatched", func() { NewCancelRequest(cross(cur), "1,99") })
	many := make([]string, 17)
	for i := range many {
		many[i] = "1"
	}
	asMember(cur)
	aborts(t, "named twice", func() { NewCancelRequest(cross(cur), strings.Join(many, ",")) })
	asMember(cur)
	req := NewCancelRequest(cross(cur), "2, 3")
	if req.Description() != "cancel decisions 2, 3" {
		t.Fatalf("description %q", req.Description())
	}
	pass(cur, req)
	list := "00" + "02" + "00" + "0000000000000002" + "00" + "0000000000000003"
	salt9 := cancelBody(t, 9, list)
	if d := ledger.Get(ledger.Last()); d.Kind != "cancel" || d.Note != "dispatched as decision 9, salt "+salt9 {
		t.Fatalf("decision %+v", d)
	}
	// form 1: a range, inclusive
	asMember(cur)
	aborts(t, "from the lower number", func() { NewCancelRequest(cross(cur), "5-4") })
	asMember(cur)
	aborts(t, "was never dispatched", func() { NewCancelRequest(cross(cur), "4-99") })
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), "4-7"))
	cancelBody(t, 10, "01"+"00"+"0000000000000004"+"0000000000000007")
	// the same list again: a new number and a new salt, so the bodies differ
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), "2, 3"))
	if salt11 := cancelBody(t, 11, list); salt11 == salt9 {
		t.Fatalf("two cancels of one list share the salt %s", salt9)
	}
	// sync sequence: the next number jumps, at once — and only ever up: a number at or below the next one is refused
	asMember(cur)
	aborts(t, "is 12 already", func() { NewSyncSequenceRequest(cross(cur), 12) })
	asMember(cur)
	aborts(t, "at least 1", func() { NewSyncSequenceRequest(cross(cur), 0) })
	asMember(cur)
	aborts(t, "only ever raises the counter: it stands at 11", func() { NewSyncSequenceRequest(cross(cur), 11) })
	asMember(cur)
	aborts(t, "only ever raises", func() { NewSyncSequenceRequest(cross(cur), 1) })
	// and by at most 2^32 (the realm's own bound, R18): a mistyped number can never wrap the counter
	asMember(cur)
	aborts(t, "reaches at most 2^32 above the next number (the realm's own bound): the next number is 12 and 4294967309 is beyond 4294967308", func() { NewSyncSequenceRequest(cross(cur), 12+(1<<32)+1) })
	asMember(cur)
	if req := NewSyncSequenceRequest(cross(cur), 12+(1<<32)); !strings.Contains(req.Description(), "takes number 4294967308") {
		t.Fatalf("the reach itself is allowed: %s", req.Description())
	}
	asMember(cur)
	pass(cur, NewSyncSequenceRequest(cross(cur), 14))
	if Next() != 14 || ledger.Get(ledger.Last()).Kind != "sync sequence" {
		t.Fatalf("next %d", Next())
	}
	asMember(cur)
	pass(cur, NewUnpauseRequest(cross(cur), 0))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(2, 14, "00") || Last() != 14 {
		t.Fatalf("after sync sequence: %x", lastMessage(t).Body)
	}
	if !strings.Contains(Render(""), "starts at 0 (compiled") || !strings.Contains(Render(""), "last number 14; the next takes number 15; 12 sent from here") {
		t.Fatalf("page: %s", Render(""))
	}
	// raise-only at execution too: a sync to 16 made while the next number was 15 is executed after two decisions
	// took 15 and 16 — it would lower the counter, so it is refused there (a panic: the counter stands; on chain the
	// proposal survives for a retry until its deadline; the harness keeps the DAO's accepted mark, so no retry here)
	asMember(cur)
	stale := NewSyncSequenceRequest(cross(cur), 16)
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), stale.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	asMember(cur)
	pass(cur, NewUnpauseRequest(cross(cur), 1))
	asMember(cur)
	pass(cur, NewUnpauseRequest(cross(cur), 0))
	if Last() != 16 {
		t.Fatalf("last %d", Last())
	}
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "only ever raises the counter: it stands at 16", func() { dao.ExecuteProposal(cross(cur), pid) })
	if Last() != 16 || Next() != 17 {
		t.Fatalf("a refused sync moved the counter: next %d", Next())
	}
	// a redispatch: the same body (the salt included) in a new message, counted
	n0 := hlmailbox.Count()
	testing.SetRealm(testing.NewUserRealm(stranger))
	Redispatch(cross(cur), 9)
	if hlmailbox.Count() != n0+1 || hex.EncodeToString(lastMessage(t).Body) != Body(9) || cancelBody(t, 9, list) != salt9 || !strings.Contains(Render("decision/9"), "\"count\":2") {
		t.Fatalf("redispatch")
	}
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "no decision 12", func() { Redispatch(cross(cur), 12) })
	if !strings.Contains(Render(""), "- #14 kind 2: unpause the pausable ISM (sent 1×") || !strings.Contains(Render(""), "- #9 kind 1: cancel decisions 2, 3 (sent 2×") || !strings.Contains(Render(""), "- #16 kind 2: unpause the pausable ISM (sent 1×") {
		t.Fatalf("page: %s", Render(""))
	}
}

// TestRecipient: the decision recipient changes after 7 days, and a queued change can be cancelled at once.
func TestRecipient(cur realm, t *testing.T) {
	asMember(cur)
	aborts(t, "differ from the current one", func() { NewSetDecisionRecipientRequest(cross(cur), launchRecipient) })
	asMember(cur)
	pass(cur, NewSetDecisionRecipientRequest(cross(cur), keyC))
	id := ledger.Last()
	if !ledger.Get(id).Queued() || Recipient() != launchRecipient {
		t.Fatalf("a recipient swap applied at once")
	}
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "is due at", func() { Execute(cross(cur), id) })
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), id)
	if Recipient() != keyC {
		t.Fatalf("recipient %s", Recipient())
	}
	asMember(cur)
	pass(cur, NewUnpauseRequest(cross(cur), 0))
	m := lastMessage(t)
	if hex.EncodeToString(m.Recipient[:]) != keyC {
		t.Fatalf("the next decision went to %x", m.Recipient)
	}
	// a queued swap cancelled
	asMember(cur)
	pass(cur, NewSetDecisionRecipientRequest(cross(cur), keyD))
	q := ledger.Last()
	asMember(cur)
	pass(cur, NewCancelQueuedRequest(cross(cur), q))
	if ledger.Get(q).State() != "cancelled" || Recipient() != keyC {
		t.Fatalf("cancel queued")
	}
	if !strings.Contains(Render("decisions"), "### set decision recipient") {
		t.Fatalf("decisions page")
	}
}

// TestMintLimits: kind 12's body is §13's 17 parameter bytes — token u8 ‖ maximum u64 ‖ day cap u64, base units —
// dispatched with the number current at execution; the builder refuses a token that is none of ours, a maximum of
// 0 and a day cap below the maximum, and records nothing until the proposal executes: a pre-armed proposal (voted,
// unexecuted) moves no counter, takes the number current when it executes, and past its 14 days is denied for good.
func TestMintLimits(cur realm, t *testing.T) {
	last, n0 := Last(), ledger.Last()
	asMember(cur)
	aborts(t, "0 (GNOT) or 1 (BUBBLE)", func() { NewSetMintLimitsRequest(cross(cur), 2, 1, 1) })
	asMember(cur)
	aborts(t, "the per-message maximum is at least 1", func() { NewSetMintLimitsRequest(cross(cur), 1, 0, 1) })
	asMember(cur)
	aborts(t, "the day cap is at least the per-message maximum", func() { NewSetMintLimitsRequest(cross(cur), 0, 2, 1) })
	if Last() != last || ledger.Last() != n0 {
		t.Fatalf("a refused builder moved something: last %d, decisions %d", Last(), ledger.Last())
	}
	// BUBBLE to 50,000 a message and 200,000 a day, in base units (6 decimals): the body byte for byte
	asMember(cur)
	req := NewSetMintLimitsRequest(cross(cur), 1, 50_000_000_000, 200_000_000_000)
	if req.Description() != "set BUBBLE's mint limits: 50000000000 a message, 200000000000 a day (base units)" {
		t.Fatalf("description %q", req.Description())
	}
	pass(cur, req)
	n := last + 1
	params := "01" + "0000000ba43b7400" + "0000002e90edd000"
	m := lastMessage(t)
	if hex.EncodeToString(m.Body) != bodyHex(12, n, params) || len(m.Body) != 9+17 || hex.EncodeToString(m.Recipient[:]) != Recipient() {
		t.Fatalf("set mint limits: %x to %x", m.Body, m.Recipient)
	}
	if Last() != n || Body(n) != bodyHex(12, n, params) || !strings.Contains(Render("decision/"+u64s(n)), "\"kind\":12,") {
		t.Fatalf("recorded: last %d body %s", Last(), Body(n))
	}
	d := ledger.Get(ledger.Last())
	if ledger.Last() != n0+1 || d.Kind != "set mint limits" || d.State() != "applied" || d.Note != "dispatched as decision "+u64s(n) {
		t.Fatalf("decision %+v", d)
	}
	if !strings.Contains(Render(""), "- #"+u64s(n)+" kind 12: set BUBBLE's mint limits: 50000000000 a message, 200000000000 a day (base units) (sent 1×") || !strings.Contains(Render("decisions"), "### set mint limits") {
		t.Fatalf("page: %s", Render(""))
	}
	// the maximum is never below the token's route's per-message maximum on gno.land (R15: a send between the two
	// would strand): GNOT's route maximum is the launch value, 25,000
	asMember(cur)
	aborts(t, "a per-message maximum of 24999999999 is below the GNOT route's maximum of 25000000000 (base units)", func() { NewSetMintLimitsRequest(cross(cur), 0, 24_999_999_999, 100_000_000_000) })
	// GNOT at the route's maximum, the day cap equal to the maximum (one such message a day): allowed
	asMember(cur)
	pass(cur, NewSetMintLimitsRequest(cross(cur), 0, 25_000_000_000, 25_000_000_000))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(12, n+1, "00"+"00000005d21dba00"+"00000005d21dba00") {
		t.Fatalf("set mint limits: %x", lastMessage(t).Body)
	}
	// pre-armed: made and voted, not executed — nothing recorded and no number taken; executed after another
	// decision, it takes the number current then (the launch limits, 25,000 and 100,000)
	asMember(cur)
	armed := NewSetMintLimitsRequest(cross(cur), 1, 25_000_000_000, 100_000_000_000)
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), armed.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	if Last() != n+1 || ledger.Last() != n0+2 {
		t.Fatalf("a pre-armed proposal moved something: last %d, decisions %d", Last(), ledger.Last())
	}
	asMember(cur)
	pass(cur, NewUnpauseRequest(cross(cur), 0))
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		t.Fatalf("the pre-armed proposal did not execute")
	}
	if Last() != n+3 || hex.EncodeToString(lastMessage(t).Body) != bodyHex(12, n+3, "01"+"00000005d21dba00"+"000000174876e800") || ledger.Last() != n0+4 {
		t.Fatalf("the pre-armed decision: last %d body %x", Last(), lastMessage(t).Body)
	}
	// the deadline: pre-armed and left 15 days, it is denied for good and dispatches nothing
	asMember(cur)
	stale := NewSetMintLimitsRequest(cross(cur), 0, 25_000_000_000, 30_000_000_000)
	testing.SetRealm(testing.NewUserRealm(member))
	pid = dao.MustCreateProposal(cross(cur), stale.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SkipHeights(15 * dayHeights)
	testing.SetRealm(testing.NewUserRealm(member))
	if dao.ExecuteOrRejectProposal(cross(cur), pid) {
		t.Fatalf("executed past the deadline")
	}
	if Last() != n+3 || ledger.Last() != n0+4 {
		t.Fatalf("a denied proposal left something: last %d, decisions %d", Last(), ledger.Last())
	}
	// the route's maximum is checked in the closure too (R15): a pre-armed kind 12 at GNOT's maximum of 25,000 is
	// refused once the route's maximum was raised to 30,000 in between (the proposal stays for GovDAO's cancel)
	asMember(cur)
	armed = NewSetMintLimitsRequest(cross(cur), 0, 25_000_000_000, 100_000_000_000)
	testing.SetRealm(testing.NewUserRealm(member))
	pid = dao.MustCreateProposal(cross(cur), armed.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	asMember(cur)
	passGnot(cur, hlgnot.NewSetMaximumRequest(cross(cur), 30_000_000_000))
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	hlgnot.Execute(cross(cur), decisionsOf(hlgnot.Render("json")))
	if hlgnot.Maximum() != 30_000_000_000 {
		t.Fatalf("the route's maximum %d", hlgnot.Maximum())
	}
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "a per-message maximum of 25000000000 is below the GNOT route's maximum of 30000000000", func() { dao.ExecuteProposal(cross(cur), pid) })
	if Last() != n+3 {
		t.Fatalf("a refused kind 12 dispatched: last %d", Last())
	}
}

// TestDecisionBodyBound (M1f part 2, the programs lane's M2f; M1g part 2, the assumptions audit's F18): a body
// above MaxDecisionBody — 512 bytes, the kind and the number included — is refused where this realm dispatches,
// when a proposal is made and in its closure: Hyperlane's mainnet mailbox dies on its own 32 KB heap past about
// 2,250 parameter bytes, and a legacy Solana delivery carries at most 538 bytes of body (529 of parameters: 14
// account keys and 127 bytes of fixed instruction data leave that of the 1,232-byte packet), so a body no one can
// deliver would stick this realm's sequence with the number untaken. Every builder's body is far below (a cancel
// at most 163 bytes, kind 5 at most 4 entries), so the bound is exercised at the two choke points directly, before
// any SetRealm (the calls cross with this frame's cur).
func TestDecisionBodyBound(cur realm, t *testing.T) {
	big := make([]byte, 504)  // a 513-byte body
	fits := make([]byte, 503) // 512 exactly
	last, n0 := Last(), hlmailbox.Count()
	aborts(t, "513 bytes, above the 512 the mailbox delivers", func() { proposeKind(cross(cur), KindProtocolFee, "test", "test body", big, func() {}) })
	aborts(t, "513 bytes, above the 512 the mailbox delivers", func() { dispatch(cross(cur), KindProtocolFee, big, "test body", now()) })
	if Last() != last || hlmailbox.Count() != n0 || ledger.Last() != decisionsOf(Render("json")) {
		t.Fatalf("a refused body moved something: last %d count %d", Last(), hlmailbox.Count())
	}
	if req := proposeKind(cross(cur), KindProtocolFee, "test", "test body", fits, func() {}); req.Description() != "test body" {
		t.Fatalf("a body at the bound refused: %s", req.Description())
	}
}

// TestSuccessorIsNotTheRecipient (M1f part 2, the programs lane's S-15): kind 9 refuses the admin program itself —
// the decision recipient this realm knows — as the successor, when the proposal is made and again when it executes:
// a recipient swap that lands in between turns a pre-armed transfer into one the admin program would only spend
// (it re-points every authority to itself and dies on its own SuccessorInit), so the realm refuses it early, as it
// refuses kind 8's self-upgrade.
func TestSuccessorIsNotTheRecipient(cur realm, t *testing.T) {
	keyE := strings.Repeat("ee", 32)
	if Recipient() != keyC {
		t.Fatalf("recipient %s", Recipient())
	}
	asMember(cur)
	aborts(t, "never the admin program itself", func() { NewTransferToSuccessorRequest(cross(cur), keyC) })
	asMember(cur)
	aborts(t, "must be set", func() { NewTransferToSuccessorRequest(cross(cur), zeros) })
	// pre-armed: a transfer to keyE, voted; then a recipient swap to keyE lands, 7 days on
	asMember(cur)
	armed := NewTransferToSuccessorRequest(cross(cur), keyE)
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), armed.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	asMember(cur)
	pass(cur, NewSetDecisionRecipientRequest(cross(cur), keyE))
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), ledger.Last())
	if Recipient() != keyE {
		t.Fatalf("recipient %s", Recipient())
	}
	last := Last()
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "never the admin program itself", func() { dao.ExecuteProposal(cross(cur), pid) })
	if Last() != last {
		t.Fatalf("a refused kind 9 dispatched: last %d", Last())
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

// TestFormerRecipientRefused (R29): every decision recipient a swap replaced is kept as a former one, and kind 9
// and the recipient swap refuse any of them — a retired admin program answers Retired to every decision and
// NotInherited to a kind 9's successor_init, so a vote naming one is wasted at best and pends for good at worst —
// when the proposal is made, in its closure, and when a queued swap would apply. The recipient went
// launchRecipient → keyC → keyE in the tests before.
func TestFormerRecipientRefused(cur realm, t *testing.T) {
	keyE, keyF, keyG, keyH, keyI := strings.Repeat("ee", 32), strings.Repeat("f0", 32), strings.Repeat("f1", 32), strings.Repeat("f2", 32), strings.Repeat("f3", 32)
	if Recipient() != keyE {
		t.Fatalf("recipient %s", Recipient())
	}
	for _, k := range []string{launchRecipient, keyC} {
		asMember(cur)
		aborts(t, "never a former admin program ("+k+"): it is retired", func() { NewTransferToSuccessorRequest(cross(cur), k) })
		asMember(cur)
		aborts(t, "never a former decision recipient ("+k+"): it is retired", func() { NewSetDecisionRecipientRequest(cross(cur), k) })
	}
	if !strings.Contains(Render("json"), "\"former\":[\""+launchRecipient+"\",\""+keyC+"\"]") || !strings.Contains(Render(""), "- former recipients (retired; never a successor, never the recipient again): `"+launchRecipient+"`, `"+keyC+"`") {
		t.Fatalf("former: %s\n%s", Render("json"), Render(""))
	}
	// pre-armed: a swap and a kind 9 to keyF, voted while keyF is nobody; then keyF serves (a swap to it lands) and
	// is replaced by keyG — two swaps queued together and applied back to back a week on, inside the pre-armed
	// proposals' 14 days — so keyF is former when they execute: both closures refuse them
	asMember(cur)
	armedSwap := NewSetDecisionRecipientRequest(cross(cur), keyF)
	testing.SetRealm(testing.NewUserRealm(member))
	pidSwap := dao.MustCreateProposal(cross(cur), armedSwap.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pidSwap), "YES")
	asMember(cur)
	armedNine := NewTransferToSuccessorRequest(cross(cur), keyF)
	testing.SetRealm(testing.NewUserRealm(member))
	pidNine := dao.MustCreateProposal(cross(cur), armedNine.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pidNine), "YES")
	asMember(cur)
	pass(cur, NewSetDecisionRecipientRequest(cross(cur), keyF))
	toF := ledger.Last()
	asMember(cur)
	pass(cur, NewSetDecisionRecipientRequest(cross(cur), keyG))
	toG := ledger.Last()
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), toF)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), toG)
	if Recipient() != keyG || !strings.Contains(Render("json"), "\"former\":[\""+launchRecipient+"\",\""+keyC+"\",\""+keyE+"\",\""+keyF+"\"]") {
		t.Fatalf("recipient %s: %s", Recipient(), Render("json"))
	}
	last, n0 := Last(), ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "never a former decision recipient ("+keyF+")", func() { dao.ExecuteProposal(cross(cur), pidSwap) })
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "never a former admin program ("+keyF+")", func() { dao.ExecuteProposal(cross(cur), pidNine) })
	if Last() != last || ledger.Last() != n0 {
		t.Fatalf("a refused proposal dispatched or recorded: last %d decisions %d", Last(), ledger.Last())
	}
	// a queued swap decided while its target was nobody applies only while that holds: two swaps to keyH queued
	// together — the first lands; the second finds keyH the recipient already and is refused; once keyI replaces keyH
	// it finds keyH former and is refused for good (GovDAO cancels it)
	asMember(cur)
	pass(cur, NewSetDecisionRecipientRequest(cross(cur), keyH))
	first := ledger.Last()
	asMember(cur)
	pass(cur, NewSetDecisionRecipientRequest(cross(cur), keyH))
	second := ledger.Last()
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), first)
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, keyH+" is the decision recipient already: cancel decision "+itoa(second), func() { Execute(cross(cur), second) })
	asMember(cur)
	pass(cur, NewSetDecisionRecipientRequest(cross(cur), keyI))
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), ledger.Last())
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "never a former decision recipient ("+keyH+")", func() { Execute(cross(cur), second) })
	if Recipient() != keyI || !ledger.Get(second).Pending() || !strings.Contains(Render("json"), "\""+keyH+"\"]") {
		t.Fatalf("recipient %s, decision %d %s", Recipient(), second, ledger.Get(second).State())
	}
}

// TestCancelClearsEnrolment (R37, AS2): a kind 6 this realm cancels (kind 1) leaves no enrolment note on the route
// it enrolled — the realm's refund clock then restarts with a re-proposal, where before a cancelled, lost or held
// kind 6 left the note standing and the route's refund gate opened on the realm's own clock while the predecessor
// was still the enrolled router (AR1's window); only the latest note is cleared (an older kind 6 cancelled leaves
// a newer one's note), by a list or a range cancel alike, and the cancel's note says so.
func TestCancelClearsEnrolment(cur realm, t *testing.T) {
	route := namespace + "hlgnot"
	rk, _ := hl.AddressToBytes32(chain.PackageAddress(route))
	key := hex.EncodeToString(rk[:])
	someID := strings.Repeat("ab", 32)
	asMember(cur)
	pass(cur, NewEnrollRouterRequest(cross(cur), 0, 1198419796, key))
	first := Last()
	h1, _ := hlmailbox.Enrolled(route)
	if h1 != runtime.ChainHeight() {
		t.Fatalf("not noted: %d", h1)
	}
	// the route's refund waits on the note: 14 days after the dispatch (hlgnot's own gate, the route live here)
	asMember(cur)
	aborts(t, "kind 6 enrolling this route was dispatched at height "+strconv.FormatInt(h1, 10)+", 0 s ago: a refund waits 14 days after it", func() { hlgnot.NewRefundRequest(cross(cur), someID) })
	// a second kind 6 for the same route: the latest note counts; cancelling the first leaves it
	testing.SkipHeights(10)
	asMember(cur)
	pass(cur, NewEnrollRouterRequest(cross(cur), 0, 1198419796, key))
	second := Last()
	h2, _ := hlmailbox.Enrolled(route)
	if h2 != runtime.ChainHeight() || h2 == h1 {
		t.Fatalf("the latest note: %d (first %d)", h2, h1)
	}
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), u64s(first)))
	if h, _ := hlmailbox.Enrolled(route); h != h2 || strings.Contains(ledger.Get(ledger.Last()).Note, "enrolment note") {
		t.Fatalf("cancelling an older kind 6 touched the latest note: %d, note %q", h, ledger.Get(ledger.Last()).Note)
	}
	// cancelling the latest clears it: no note, the refund refused as if no kind 6 had been dispatched
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), u64s(second)))
	d := ledger.Get(ledger.Last())
	if h, at := hlmailbox.Enrolled(route); h != 0 || at != 0 || !strings.Contains(d.Note, "; the enrolment note of "+route+" cleared (kind 6 #"+u64s(second)+" cancelled): its refund clock restarts with a re-proposal") {
		t.Fatalf("a cancelled kind 6 left its note: %d %d, note %q", h, at, d.Note)
	}
	asMember(cur)
	aborts(t, "no kind 6 has enrolled this route on Solana yet (none dispatched from r/hlgov): a refund waits 14 days after the enrolment", func() { hlgnot.NewRefundRequest(cross(cur), someID) })
	// a range cancel clears too; a cancel of a decision that is no kind 6 clears nothing
	asMember(cur)
	pass(cur, NewEnrollRouterRequest(cross(cur), 0, 1198419796, key))
	third := Last()
	asMember(cur)
	pass(cur, NewUnpauseRequest(cross(cur), 0))
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), u64s(third)+"-"+u64s(third+1)))
	if h, _ := hlmailbox.Enrolled(route); h != 0 || !strings.Contains(ledger.Get(ledger.Last()).Note, "the enrolment note of "+route+" cleared (kind 6 #"+u64s(third)+" cancelled)") {
		t.Fatalf("a range cancel left the note: %d, note %q", h, ledger.Get(ledger.Last()).Note)
	}
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), u64s(third+1)))
	if strings.Contains(ledger.Get(ledger.Last()).Note, "enrolment note") {
		t.Fatalf("a cancel of no kind 6 said it cleared a note: %q", ledger.Get(ledger.Last()).Note)
	}
}

// TestEnrollSuccessor (R35, R36): kind 6 for gno.land's own domain never names a successor that has pulled
// nothing — page 0 not in — since a migrate its successor pulled nothing from may still be re-pointed (AR2),
// which would drop the route Solana then names the router for the deployment's life: a realm in nobody's chain,
// flaggable by nobody, every burn's recipient; once its predecessor's Pull has noted page 0 with the mailbox
// (Pulled) the successor is enrolled, and the note lands on it. Nor a migrated route (R36: its successor is the
// token's router) — a kind 6 pre-armed for hlgnot before its migrate applied is refused in the closure, the note
// untouched — nor the other token's successor. hlgnot2 is registered here as hlgnot's successor by the mailbox's
// own call, as a migrate's apply does, announced for GNOT first as every successor is.
func TestEnrollSuccessor(cur realm, t *testing.T) {
	pred, succ := namespace+"hlgnot", namespace+"hlgnot2"
	rk, _ := hl.AddressToBytes32(chain.PackageAddress(pred))
	k, _ := hl.AddressToBytes32(chain.PackageAddress(succ))
	key := hex.EncodeToString(k[:])
	// pre-armed while hlgnot is unmigrated: made and voted, executed below once hlgnot has a successor
	asMember(cur)
	armed := NewEnrollRouterRequest(cross(cur), 0, 1198419796, hex.EncodeToString(rk[:]))
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), armed.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	h0, at0 := hlmailbox.Enrolled(pred)
	testing.SetRealm(testing.NewCodeRealm(succ))
	hlmailbox.AnnounceSuccessor(cross(cur), hlmailbox.SymbolGNOT)
	testing.SetRealm(testing.NewCodeRealm(pred))
	hlmailbox.AddRoute(cross(cur), succ)
	if hlmailbox.Pulled(succ) || hlmailbox.Predecessor(succ) != pred || hlmailbox.RouteSymbol(succ) != "GNOT" {
		t.Fatalf("hlgnot2 should be hlgnot's un-pulled GNOT successor")
	}
	last, n0 := Last(), ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, pred+" migrated to "+succ+": never a migrated route, since its successor is the token's router", func() { dao.ExecuteProposal(cross(cur), pid) })
	asMember(cur)
	aborts(t, pred+" migrated to "+succ+": never a migrated route", func() { NewEnrollRouterRequest(cross(cur), 0, 1198419796, hex.EncodeToString(rk[:])) })
	if h, at := hlmailbox.Enrolled(pred); h != h0 || at != at0 || Last() != last || ledger.Last() != n0 {
		t.Fatalf("a refused kind 6 for a migrated route left something: enrolled %d %d (was %d %d), last %d, decisions %d", h, at, h0, at0, Last(), ledger.Last())
	}
	asMember(cur)
	aborts(t, succ+" succeeds "+pred+" and has pulled nothing yet (page 0 is not in): a kind 6 enrolling it waits for its first page, since a migrate that pulled nothing may be re-pointed and Solana must never name a dropped route", func() {
		NewEnrollRouterRequest(cross(cur), 0, 1198419796, key)
	})
	if h, at := hlmailbox.Enrolled(succ); h != 0 || at != 0 || Last() != last || ledger.Last() != n0 {
		t.Fatalf("a refused kind 6 left something: enrolled %d %d, last %d, decisions %d", h, at, Last(), ledger.Last())
	}
	// page 0 pulled (the predecessor's Pull notes it): enrolled for GNOT, the note on the successor — and never
	// for BUBBLE (R36), nor is the BUBBLE route's own successor ever GNOT's router
	testing.SetRealm(testing.NewCodeRealm(pred))
	hlmailbox.NotePulled(cross(cur), succ)
	asMember(cur)
	aborts(t, succ+" is the GNOT route's, not BUBBLE's: a kind 6 names the token's own route", func() { NewEnrollRouterRequest(cross(cur), 1, 1198419796, key) })
	asMember(cur)
	pass(cur, NewEnrollRouterRequest(cross(cur), 0, 1198419796, key))
	if h, at := hlmailbox.Enrolled(succ); Last() != last+1 || h != runtime.ChainHeight() || at == 0 || hex.EncodeToString(lastMessage(t).Body) != bodyHex(6, last+1, "00"+"476e6f54"+key) {
		t.Fatalf("the enrolment of a pulled successor: enrolled %d %d, last %d", h, at, Last())
	}
	other := namespace + "hlbubble2"
	ok, _ := hl.AddressToBytes32(chain.PackageAddress(other))
	testing.SetRealm(testing.NewCodeRealm(other))
	hlmailbox.AnnounceSuccessor(cross(cur), hlmailbox.SymbolBUBBLE)
	testing.SetRealm(testing.NewCodeRealm(namespace + "hlbubble"))
	hlmailbox.AddRoute(cross(cur), other)
	testing.SetRealm(testing.NewCodeRealm(namespace + "hlbubble"))
	hlmailbox.NotePulled(cross(cur), other)
	asMember(cur)
	aborts(t, other+" is the BUBBLE route's, not GNOT's: a kind 6 names the token's own route", func() { NewEnrollRouterRequest(cross(cur), 0, 1198419796, hex.EncodeToString(ok[:])) })
	if h, _ := hlmailbox.Enrolled(other); h != 0 || Last() != last+1 {
		t.Fatalf("a refused kind 6 for the other token's successor left something: enrolled %d, last %d", h, Last())
	}
	asMember(cur)
	pass(cur, NewEnrollRouterRequest(cross(cur), 1, 1198419796, hex.EncodeToString(ok[:])))
	if h, _ := hlmailbox.Enrolled(other); h != runtime.ChainHeight() || Last() != last+2 {
		t.Fatalf("the BUBBLE successor's enrolment: enrolled %d, last %d", h, Last())
	}
}

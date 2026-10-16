package hlgov

import (
	"chain"
	"chain/runtime"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/p/nt/testutils/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlbubble"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlgnot"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
	"gno.land/r/gov/dao"
	govinit "gno.land/r/gov/dao/init/v0"
)

const dayHeights = 86400 / 5

// The test keys are Ethereum addresses spelled in digits alone, so their EIP-55 form is their own spelling and the
// realm's pages and readers name them as written here; the mixed-case EIP-55 spellings are TestEthereumKinds'.
var (
	member   = testutils.TestAddress("hl-member")
	stranger = testutils.TestAddress("hlv-other")
	keyA     = "0x" + strings.Repeat("10", 20)
	keyB     = "0x" + strings.Repeat("20", 20)
	keyC     = "0x" + strings.Repeat("30", 20)
	keyD     = "0x" + strings.Repeat("40", 20)
	zeros    = "0x" + strings.Repeat("00", 20)
	hashC    = strings.Repeat("cc", 32) // a code hash (kinds 8 and 9): 32 bytes, no address
	hashD    = strings.Repeat("dd", 32)
)

// The Ethereum deployment's contracts this realm reads from hlmailbox (ethdeploy.gno): its compiled placeholders,
// spelled out here so the tests' golden bytes never come from the realm (TestLaunchValues checks they agree).
const (
	launchRecipient = "0x0101010101010101010101010101010101010101" // HlAdmin
	launchMailbox   = "0x0404040404040404040404040404040404040404"
	launchToken0    = "0x0BA554Aa0D0566e582dE0238a089Ec102c2e05a4"
	launchToken1    = "0x051D52Fd082d902f8E0CF300a60304D484cf2d07"
	launchClient    = "0x0303030303030303030303030303030303030303"
	launchGate      = "0x0202020202020202020202020202020202020202"
)

// pad is an Ethereum address (0x and 40 hex digits) as a decision body carries it, in hex: twelve zero bytes, then
// the twenty — written here apart from the realm's parser.
func pad(a string) string { return strings.Repeat("00", 12) + strings.ToLower(a[2:]) }

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

// swapRequest is the recipient swap's builder (NewSetDecisionRecipientRequest): from the recipient it replaces to the
// contract it names (Q-F6b). Every test makes the swap through it, so one line holds the builder's signature.
func swapRequest(cur realm, from, to string) Request {
	return NewSetDecisionRecipientRequest(cross(cur), from, to)
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
	if hlmailbox.Live() || Start() != 0 || Last() != 0 || Next() != 1 || Recipient() != launchRecipient || Destination() != 31337 || EthSeat(1) != launchEthSeat1 || EthSeat(6) != launchEthSeat6 || EthSeat(7) != "" {
		t.Fatalf("launch values: last %d recipient %s", Last(), Recipient())
	}
	p := Render("")
	for _, want := range []string{"**not launched**", "destination: domain 31337 (Ethereum), recipient `" + launchRecipient + "` (HlAdmin)", "the light client `" + launchClient + "`", "kinds 4, 10 and 11 are reserved and have no builder here", "sequence: starts at 0 (compiled", "last number 0; the next takes number 1; 0 sent from here", "seat 3 (guardian): `" + launchEthSeat3 + "`", "seat 5 (watch key)", "- clock factor 1 (1 on mainnet; a devnet build is compiled with 8)"} {
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
	// the five proxies kind 8 may upgrade and HlAdmin are hlmailbox's compiled contracts, on the page and in the json
	// in EIP-55 case; the two tokens are the routes' own launch routers (the stage step swaps both for the same
	// deployed proxies), the gate kind 3's ISM
	for i, want := range []string{launchMailbox, launchToken0, launchToken1, launchClient, launchGate, launchRecipient} {
		if _, k := hlmailbox.EthContractAt(i); hlmailbox.EthAddress(k) != want {
			t.Fatalf("hlmailbox's contract %d is %s, not %s", i, hlmailbox.EthAddress(k), want)
		}
	}
	if Proxy(0) != launchMailbox || Proxy(1) != launchToken0 || Proxy(2) != launchToken1 || Proxy(3) != launchClient || Proxy(4) != launchGate || Proxy(5) != "" || Proxy(-1) != "" ||
		launchToken0 != hlgnot.RouterAddress() || launchToken1 != hlbubble.RouterAddress() {
		t.Fatalf("proxies %s %s %s %s %s; the routes' routers %s %s", Proxy(0), Proxy(1), Proxy(2), Proxy(3), Proxy(4), hlgnot.RouterAddress(), hlbubble.RouterAddress())
	}
	if !strings.Contains(p, "the mailbox `"+launchMailbox+"`; token 0 (GNOT) `"+launchToken0+"`; token 1 (BUBBLE) `"+launchToken1+"`; the light client `"+launchClient+"`; the gate `"+launchGate+"`") ||
		!strings.Contains(Render("json"), "\"proxies\":{\"mailbox\":\""+launchMailbox+"\",\"token0\":\""+launchToken0+"\",\"token1\":\""+launchToken1+"\",\"client\":\""+launchClient+"\",\"gate\":\""+launchGate+"\"}") ||
		!strings.Contains(Render("json"), "\"destination\":31337,\"recipient\":\""+launchRecipient+"\",") || strings.Contains(p, "Solana") {
		t.Fatalf("page: %s\njson: %s", p, Render("json"))
	}
	asMember(cur)
	aborts(t, "not launched", func() { NewUnpauseRequest(cross(cur), 0) })
	asMember(cur)
	aborts(t, "not launched", func() { NewSetMintLimitsRequest(cross(cur), 1, 1, 1) })
	asMember(cur)
	aborts(t, "not launched", func() { NewCancelRequest(cross(cur), "1") })
	asMember(cur)
	aborts(t, "not launched", func() { NewSyncSequenceRequest(cross(cur), 5) })
	asMember(cur)
	aborts(t, "not launched", func() { swapRequest(cur, launchRecipient, keyA) })
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
	aborts(t, "0 (the gate) or 1 (HlAdmin)", func() { NewUnpauseRequest(cross(cur), 2) })
	n0 := hlmailbox.Count()
	asMember(cur)
	pass(cur, NewUnpauseRequest(cross(cur), 1))
	m := lastMessage(t)
	if hlmailbox.Count() != n0+1 || m.Sender != self || m.Destination != 31337 || hex.EncodeToString(m.Recipient[:]) != pad(launchRecipient) || hex.EncodeToString(m.Body) != bodyHex(2, 1, "01") {
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
	aborts(t, "the ISM must be the gate "+launchGate+" or the zero address, not "+keyA, func() { NewSetTokenISMRequest(cross(cur), 0, keyA) })
	asMember(cur)
	aborts(t, "0 (GNOT) or 1 (BUBBLE)", func() { NewSetTokenISMRequest(cross(cur), 2, zeros) })
	asMember(cur)
	pass(cur, NewSetTokenISMRequest(cross(cur), 1, zeros))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(3, 2, "01"+strings.Repeat("00", 32)) {
		t.Fatalf("set token ISM: %x", lastMessage(t).Body)
	}
	asMember(cur)
	pass(cur, NewSetTokenISMRequest(cross(cur), 0, launchGate))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(3, 3, "00"+pad(launchGate)) {
		t.Fatalf("set token ISM: %x", lastMessage(t).Body)
	}
	// kinds 4, 10 and 11 are reserved and have no builder here (eth §13): nothing can author one, and the counter
	// stands where kind 3 left it
	if Last() != 3 || ledger.Last() != 3 {
		t.Fatalf("the counter: %d, decisions %d", Last(), ledger.Last())
	}
	// kind 6, 7, 8, 9. For gno.land's own domain (1198419796 here) the router must be a route realm hlmailbox
	// registers, as 32 bytes; another domain takes any address
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
	if h, at := hlmailbox.Enrolled(namespace + "hlgnot"); h != runtime.ChainHeight() || at == 0 || !strings.Contains(ledger.Get(ledger.Last()).Note, "dispatched as decision 4; "+namespace+"hlgnot noted enrolled on Ethereum at height "+strconv.FormatInt(h, 10)) {
		t.Fatalf("enrolled: %d %d, note %q", h, at, ledger.Get(ledger.Last()).Note)
	}
	if h, _ := hlmailbox.Enrolled(namespace + "hlbubble"); h != 0 {
		t.Fatalf("the other route noted: %d", h)
	}
	asMember(cur)
	aborts(t, "the fee is at most 1000000000000000 wei (0.001 ETH)", func() { NewSetProtocolFeeRequest(cross(cur), 1_000_000_000_000_001, keyB) })
	asMember(cur)
	aborts(t, "the beneficiary is the zero address", func() { NewSetProtocolFeeRequest(cross(cur), 1, zeros) })
	asMember(cur)
	pass(cur, NewSetProtocolFeeRequest(cross(cur), 256, keyB))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(7, 5, "0000000000000100"+pad(keyB)) {
		t.Fatalf("set protocol fee: %x", lastMessage(t).Body)
	}
	asMember(cur)
	aborts(t, "the implementation must be set and differ from the proxy", func() { NewUpgradeRequest(cross(cur), launchMailbox, launchMailbox, hashC, "") })
	asMember(cur)
	aborts(t, launchRecipient+" is none of the five proxies kind 8 upgrades", func() { NewUpgradeRequest(cross(cur), launchRecipient, keyB, hashC, "") })
	asMember(cur)
	pass(cur, NewUpgradeRequest(cross(cur), launchMailbox, keyB, hashC, ""))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(8, 6, pad(launchMailbox)+pad(keyB)+hashC) {
		t.Fatalf("upgrade: %x", lastMessage(t).Body)
	}
	asMember(cur)
	aborts(t, "not the current HlAdmin as its own successor", func() { NewTransferToSuccessorRequest(cross(cur), launchRecipient, hashD) })
	asMember(cur)
	pass(cur, NewTransferToSuccessorRequest(cross(cur), keyD, hashD))
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(9, 7, pad(keyD)+hashD) {
		t.Fatalf("transfer: %x", lastMessage(t).Body)
	}
	if Last() != 7 || !strings.Contains(Render("json"), "\"last\":7,\"next\":8") {
		t.Fatalf("counter %d", Last())
	}
}

// TestGuardians: kind 5 takes at most four entries, two guardian seats, each key an Ethereum address; a seat's
// current key as last set here is warned about in the description, not refused (HlAdmin spends such a decision
// itself and the mirror can drift); the mirror follows what was sent.
func TestGuardians(cur realm, t *testing.T) {
	asMember(cur)
	aborts(t, "seat:address", func() { NewSetGuardiansRequest(cross(cur), "1") })
	asMember(cur)
	aborts(t, "must be 1 to 6", func() { NewSetGuardiansRequest(cross(cur), "7:"+keyA) })
	asMember(cur)
	aborts(t, "named twice", func() { NewSetGuardiansRequest(cross(cur), "1:"+keyA+",1:"+keyB) })
	asMember(cur)
	aborts(t, "an address named twice: "+keyA, func() { NewSetGuardiansRequest(cross(cur), "1:"+keyA+",2:000000000000000000000000"+keyA[2:]) }) // one address in its two spellings
	asMember(cur)
	aborts(t, "the zero address for seat 1", func() { NewSetGuardiansRequest(cross(cur), "1:"+zeros) })
	asMember(cur)
	aborts(t, "1 to 4 seats", func() {
		NewSetGuardiansRequest(cross(cur), "1:"+keyA+",2:"+keyB+",5:"+keyC+",6:"+keyD+",3:0x"+strings.Repeat("ee", 20))
	})
	asMember(cur)
	aborts(t, "two guardian seats at most", func() { NewSetGuardiansRequest(cross(cur), "1:"+keyA+",2:"+keyB+",3:"+keyC) })
	asMember(cur)
	if req := NewSetGuardiansRequest(cross(cur), "1:"+launchEthSeat2); !strings.Contains(req.Description(), "(warning: "+launchEthSeat2+" holds seat 2 as last set here") {
		t.Fatalf("a seat's current key was not warned about: %s", req.Description())
	}
	asMember(cur)
	req := NewSetGuardiansRequest(cross(cur), "1:"+keyA+",5:"+keyB)
	if strings.Contains(req.Description(), "warning") {
		t.Fatalf("a warning for fresh keys: %s", req.Description())
	}
	pass(cur, req)
	if hex.EncodeToString(lastMessage(t).Body) != bodyHex(5, 8, "02"+"01"+pad(keyA)+"05"+pad(keyB)) {
		t.Fatalf("set guardians: %x", lastMessage(t).Body)
	}
	if EthSeat(1) != keyA || EthSeat(5) != keyB || EthSeat(2) != launchEthSeat2 {
		t.Fatalf("mirror %s %s", EthSeat(1), EthSeat(5))
	}
	asMember(cur)
	if req := NewSetGuardiansRequest(cross(cur), "2:"+keyA+",6:"+keyB); !strings.Contains(req.Description(), "(warning: "+keyA+" holds seat 1 as last set here; "+keyB+" holds seat 5 as last set here — HlAdmin spends") {
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
	pass(cur, swapRequest(cur, launchRecipient, keyC))
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
		{"upgrade", func() Request { return NewUpgradeRequest(cross(cur), launchGate, keyB, hashC, "") }},
		{"transfer to successor", func() Request { return NewTransferToSuccessorRequest(cross(cur), keyD, hashD) }},
		{"set mint limits", func() Request { return NewSetMintLimitsRequest(cross(cur), 1, 25_000_000_000, 100_000_000_000) }},
		{"sync sequence", func() Request { return NewSyncSequenceRequest(cross(cur), last+2) }},
		{"set decision recipient", func() Request { return swapRequest(cur, launchRecipient, keyD) }},
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
	req := swapRequest(cur, launchRecipient, keyC)
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	aborts(t, "hlgov: the governor yielded at height "+itoa(h)+", gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never Govern", func() { Govern(cross(cur), req) })
	if ledger.Last() != n || Last() != last {
		t.Fatalf("Govern recorded after the yield")
	}
	// GovDAO's proposal decides now (pass takes that path from here on)
	asMember(cur)
	pass(cur, swapRequest(cur, launchRecipient, keyC))
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
	if !strings.Contains(Render(""), "- #14 kind 2: unpause the gate (sent 1×") || !strings.Contains(Render(""), "- #9 kind 1: cancel decisions 2, 3 (sent 2×") || !strings.Contains(Render(""), "- #16 kind 2: unpause the gate (sent 1×") {
		t.Fatalf("page: %s", Render(""))
	}
}

// TestRecipient: the decision recipient changes after 7 days, and a queued change can be cancelled at once.
func TestRecipient(cur realm, t *testing.T) {
	asMember(cur)
	aborts(t, "differ from the current one", func() { swapRequest(cur, launchRecipient, launchRecipient) })
	asMember(cur)
	pass(cur, swapRequest(cur, launchRecipient, keyC))
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
	if hex.EncodeToString(m.Recipient[:]) != pad(keyC) {
		t.Fatalf("the next decision went to %x", m.Recipient)
	}
	// a queued swap cancelled
	asMember(cur)
	pass(cur, swapRequest(cur, keyC, keyD))
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
	if hex.EncodeToString(m.Body) != bodyHex(12, n, params) || len(m.Body) != 9+17 || hex.EncodeToString(m.Recipient[:]) != pad(Recipient()) {
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

// TestDecisionBodyBound (the Solana deployment's M1f part 2 and M1g part 2, the assumptions audit's F18; eth §5.3
// keeps the bound as HlAdmin's "a body under 512 bytes"): a body above MaxDecisionBody — 512 bytes, the kind and
// the number included — is refused where this realm dispatches, when a proposal is made and in its closure, so a
// body HlAdmin would not record never sticks this realm's sequence with the number untaken. Every builder's body is
// far below (a cancel at most 163 bytes, kind 5 at most 142, kind 8 105), so the bound is exercised at the two choke
// points directly, before any SetRealm (the calls cross with this frame's cur).
func TestDecisionBodyBound(cur realm, t *testing.T) {
	big := make([]byte, 504)  // a 513-byte body
	fits := make([]byte, 503) // 512 exactly
	last, n0 := Last(), hlmailbox.Count()
	aborts(t, "513 bytes, above the 512 HlAdmin's rules allow", func() { proposeKind(cross(cur), KindProtocolFee, "test", "test body", big, func() {}) })
	aborts(t, "513 bytes, above the 512 HlAdmin's rules allow", func() { dispatch(cross(cur), KindProtocolFee, big, "test body", now()) })
	if Last() != last || hlmailbox.Count() != n0 || ledger.Last() != decisionsOf(Render("json")) {
		t.Fatalf("a refused body moved something: last %d count %d", Last(), hlmailbox.Count())
	}
	if req := proposeKind(cross(cur), KindProtocolFee, "test", "test body", fits, func() {}); req.Description() != "test body" {
		t.Fatalf("a body at the bound refused: %s", req.Description())
	}
}

// TestSuccessorIsNotTheRecipient (the Solana deployment's M1f part 2, its programs lane's S-15): kind 9 refuses
// HlAdmin itself — the decision recipient this realm knows — as the successor, when the proposal is made and again
// when it executes: a recipient swap that lands in between turns a pre-armed transfer into one HlAdmin would only
// spend, so the realm refuses it early.
func TestSuccessorIsNotTheRecipient(cur realm, t *testing.T) {
	keyE := "0x" + strings.Repeat("50", 20)
	if Recipient() != keyC {
		t.Fatalf("recipient %s", Recipient())
	}
	asMember(cur)
	aborts(t, "never HlAdmin itself", func() { NewTransferToSuccessorRequest(cross(cur), keyC, hashC) })
	asMember(cur)
	aborts(t, "must be set", func() { NewTransferToSuccessorRequest(cross(cur), zeros, hashC) })
	// pre-armed: a transfer to keyE, voted; then a recipient swap to keyE lands, 7 days on
	asMember(cur)
	armed := NewTransferToSuccessorRequest(cross(cur), keyE, hashC)
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), armed.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	asMember(cur)
	pass(cur, swapRequest(cur, keyC, keyE))
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), ledger.Last())
	if Recipient() != keyE {
		t.Fatalf("recipient %s", Recipient())
	}
	last := Last()
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "never HlAdmin itself", func() { dao.ExecuteProposal(cross(cur), pid) })
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

// TestSwapReplacesItsRecipient (Q-F6b, the round-2 realms fold's review): the recipient swap names the recipient it
// replaces, and holds only while that is the recipient — refused when the proposal is made and in its closure while
// another contract is the recipient, nothing recorded, and at Execute, where it stays pending for GovDAO's cancel. The
// recipient it names is read by the deployment's one parser, so any spelling of the recipient is the recipient; the
// description and the ledger's note name it in EIP-55 case. No decision is dispatched; the recipient stands at keyE,
// former launchRecipient and keyC, before and after.
func TestSwapReplacesItsRecipient(cur realm, t *testing.T) {
	keyE, keyK := "0x"+strings.Repeat("50", 20), "0x"+strings.Repeat("71", 20)
	if Recipient() != keyE || strings.Join(Former(), ",") != launchRecipient+","+keyC {
		t.Fatalf("recipient %s, former %v", Recipient(), Former())
	}
	last0, n0 := Last(), ledger.Last()
	// made: a swap from anything but the recipient is refused — a former recipient (keyC, the launch HlAdmin), an
	// address no recipient ever was (keyA, a guardian's key), the zero address, its own target — and nothing is recorded
	for _, from := range []string{keyC, launchRecipient, keyA, zeros, keyD} {
		asMember(cur)
		aborts(t, "hlgov: the swap replaces "+from+", but the decision recipient is "+keyE+": a swap names the recipient it replaces, as it stands", func() { swapRequest(cur, from, keyD) })
	}
	if ledger.Last() != n0 {
		t.Fatalf("a refused swap recorded something: %d", ledger.Last())
	}
	// the recipient in any spelling is the recipient; the description names both in EIP-55 case
	for _, from := range []string{keyE, strings.Repeat("00", 12) + keyE[2:], "0x" + strings.Repeat("00", 12) + keyE[2:], " " + keyE + " "} {
		asMember(cur)
		if d := swapRequest(cur, from, keyC).Description(); d != "set decision recipient to "+keyC+", replacing "+keyE+" (applies only while "+keyE+" is the recipient)" {
			t.Fatalf("a swap from %q: %s", from, d)
		}
	}
	// two swaps decided against keyE queued together, to keyC (q1) and to keyK (q2), and six days on a third, to keyD,
	// pre-armed (voted, not executed)
	asMember(cur)
	pass(cur, swapRequest(cur, keyE, keyC))
	q1 := ledger.Last()
	if d := ledger.Get(q1); !d.Queued() || d.Note != "queued 7 days; applies only while "+keyE+" is the recipient" {
		t.Fatalf("the queued swap: %+v", d)
	}
	asMember(cur)
	pass(cur, swapRequest(cur, keyE, keyK))
	q2 := ledger.Last()
	testing.SkipHeights(6 * dayHeights)
	asMember(cur)
	armed := swapRequest(cur, keyE, keyD)
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), armed.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	// q1 lands: keyC is the recipient. q2, decided against keyE, is refused at Execute — before this fold it applied
	// against keyC — and stays pending; the pre-armed swap's closure refuses it, nothing recorded
	testing.SkipHeights(dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), q1)
	if Recipient() != keyC {
		t.Fatalf("recipient %s", Recipient())
	}
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "hlgov: decision "+itoa(q2)+" replaces "+keyE+", and the decision recipient is "+keyC+" now: a swap applies only against the recipient it was decided against — cancel decision "+itoa(q2)+" and propose the swap against the recipient as it stands", func() { Execute(cross(cur), q2) })
	if !ledger.Get(q2).Pending() || Recipient() != keyC {
		t.Fatalf("the refused swap is %s, the recipient %s", ledger.Get(q2).State(), Recipient())
	}
	n1 := ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlgov: the swap replaces "+keyE+", but the decision recipient is "+keyC+": a swap names the recipient it replaces, as it stands", func() { dao.ExecuteProposal(cross(cur), pid) })
	if ledger.Last() != n1 || Recipient() != keyC {
		t.Fatalf("the refused pre-armed swap recorded something: decisions %d, recipient %s", ledger.Last(), Recipient())
	}
	// keyE restored by a swap made against keyC; q2, decided against keyE, would hold again: GovDAO cancels it
	asMember(cur)
	pass(cur, swapRequest(cur, keyC, keyE))
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), ledger.Last())
	asMember(cur)
	pass(cur, NewCancelQueuedRequest(cross(cur), q2))
	if Recipient() != keyE || strings.Join(Former(), ",") != launchRecipient+","+keyC || ledger.Get(q2).State() != "cancelled" || Last() != last0 {
		t.Fatalf("after: recipient %s, former %v, decision %d %s, last %d", Recipient(), Former(), q2, ledger.Get(q2).State(), Last())
	}
}

// TestFormerRecipients (R29, E2-RL2, Q-F6b): every decision recipient a swap replaced is kept as a former one, and
// kind 9 refuses any of them — a retired HlAdmin has inherited already, so a vote naming one is wasted — when the
// proposal is made and in its closure; the recipient swap takes a former recipient again (the round-2 realms audit's
// E2-RL2: a swap that lands while the kind 9 it follows still waits on Ethereum — one flag on HlAdmin defers it —
// leaves the old HlAdmin unreachable, frozen under that flag, until a second swap restores it), when it is made, in
// its closure and when it applies: the restored recipient leaves the former ones and the replaced one joins them, each
// once. A swap holds only while the recipient it replaces stands (Q-F6b): one pre-armed beside the swap that made its
// target the recipient is refused once that target served and was replaced — it would point decisions back at a
// retired HlAdmin — and one queued beside another against the same recipient is refused at Execute while the other's
// target is the recipient, pending, and applies once the recipient it replaces is restored. The recipient went
// launchRecipient → keyC → keyE in the tests before.
func TestFormerRecipients(cur realm, t *testing.T) {
	keyE, keyF, keyG, keyH := "0x"+strings.Repeat("50", 20), "0x"+strings.Repeat("60", 20), "0x"+strings.Repeat("61", 20), "0x"+strings.Repeat("62", 20)
	formerIs := func(want ...string) {
		t.Helper()
		if !strings.Contains(Render("json"), "\"former\":[\""+strings.Join(want, "\",\"")+"\"]") || strings.Join(Former(), ",") != strings.Join(want, ",") {
			t.Fatalf("former %v, want %v: %s", Former(), want, Render("json"))
		}
	}
	swap := func(to string) int64 { // a swap from the recipient now, passed now and applied by a stranger 7 days on
		t.Helper()
		asMember(cur)
		pass(cur, swapRequest(cur, Recipient(), to))
		id := ledger.Last()
		testing.SkipHeights(7*dayHeights + 1)
		testing.SetRealm(testing.NewUserRealm(stranger))
		Execute(cross(cur), id)
		if Recipient() != to {
			t.Fatalf("recipient %s, want %s", Recipient(), to)
		}
		return id
	}
	if Recipient() != keyE {
		t.Fatalf("recipient %s", Recipient())
	}
	formerIs(launchRecipient, keyC)
	// the swap takes every former recipient (TestSwapReplacesItsRecipient checks its description); kind 9 refuses each
	for _, k := range []string{launchRecipient, keyC} {
		asMember(cur)
		if r := revive(func() { swapRequest(cur, keyE, k) }); r != nil {
			t.Fatalf("a swap to the former recipient %s refused: %v", k, r)
		}
		asMember(cur)
		aborts(t, "hlgov: never a former HlAdmin ("+k+"): a decision recipient a swap replaced is never a successor", func() { NewTransferToSuccessorRequest(cross(cur), k, hashC) })
	}
	if !strings.Contains(Render(""), "- former recipients (never a successor by kind 9; a swap may name one again): `"+launchRecipient+"`, `"+keyC+"`") {
		t.Fatalf("page: %s", Render(""))
	}
	// the audit's case: a kind 9 to keyF and the swap to keyF made together; the swap lands first, and on Ethereum a
	// flag on keyE still defers the kind 9. A second swap restores keyE: the next decision — the unpause that lets the
	// kind 9 execute — reaches it; keyF is former now, refused by kind 9 like any; once the kind 9 has executed, the
	// swap to keyF is made again and taken
	asMember(cur)
	pass(cur, NewTransferToSuccessorRequest(cross(cur), keyF, hashC))
	swap(keyF)
	formerIs(launchRecipient, keyC, keyE)
	swap(keyE)
	formerIs(launchRecipient, keyC, keyF)
	asMember(cur)
	pass(cur, NewUnpauseRequest(cross(cur), 1))
	if m := lastMessage(t); hex.EncodeToString(m.Recipient[:]) != pad(keyE) || hex.EncodeToString(m.Body) != bodyHex(2, Last(), "01") {
		t.Fatalf("the unpause went to %x: %x", m.Recipient, m.Body)
	}
	asMember(cur)
	aborts(t, "never a former HlAdmin ("+keyF+")", func() { NewTransferToSuccessorRequest(cross(cur), keyF, hashC) })
	asMember(cur)
	aborts(t, "never HlAdmin itself ("+keyE+")", func() { NewTransferToSuccessorRequest(cross(cur), keyE, hashC) })
	swap(keyF)
	formerIs(launchRecipient, keyC, keyE)
	// Q-F6b's case: a swap from keyF to keyG passed, and six days on a second one and a kind 9 to keyG pre-armed
	// (voted while keyG is nobody); the first lands — keyG serves — and keyG is replaced by keyH, a swap made against
	// keyG, so keyG is former, a retired HlAdmin as far as this realm can tell, when the pre-armed two execute inside
	// their 14 days: the kind 9's closure refuses keyG as former, and the swap's refuses it as decided against keyF —
	// before this fold it was taken, pointing decisions back at keyG — nothing dispatched or recorded
	asMember(cur)
	pass(cur, swapRequest(cur, keyF, keyG))
	toG := ledger.Last()
	testing.SkipHeights(6 * dayHeights)
	asMember(cur)
	armedSwap := swapRequest(cur, keyF, keyG)
	testing.SetRealm(testing.NewUserRealm(member))
	pidSwap := dao.MustCreateProposal(cross(cur), armedSwap.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pidSwap), "YES")
	asMember(cur)
	armedNine := NewTransferToSuccessorRequest(cross(cur), keyG, hashC)
	testing.SetRealm(testing.NewUserRealm(member))
	pidNine := dao.MustCreateProposal(cross(cur), armedNine.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pidNine), "YES")
	testing.SkipHeights(dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), toG)
	if Recipient() != keyG {
		t.Fatalf("recipient %s", Recipient())
	}
	swap(keyH)
	formerIs(launchRecipient, keyC, keyE, keyF, keyG)
	last, n0 := Last(), ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "never a former HlAdmin ("+keyG+")", func() { dao.ExecuteProposal(cross(cur), pidNine) })
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlgov: the swap replaces "+keyF+", but the decision recipient is "+keyH+": a swap names the recipient it replaces, as it stands", func() { dao.ExecuteProposal(cross(cur), pidSwap) })
	if Last() != last || ledger.Last() != n0 || Recipient() != keyH {
		t.Fatalf("a refused pre-armed decision dispatched or recorded: last %d decisions %d recipient %s", Last(), ledger.Last(), Recipient())
	}
	// E2-RL2's undo with Q-F6b's rule: two swaps from keyH back to keyG queued together; the first lands — keyG leaves
	// the former ones, keyH joins them — and the second, decided against keyH, is refused at Execute while keyG is the
	// recipient, pending; once a swap restores keyH it applies, keyG the recipient again and keyH former
	asMember(cur)
	pass(cur, swapRequest(cur, keyH, keyG))
	back := ledger.Last()
	asMember(cur)
	pass(cur, swapRequest(cur, keyH, keyG))
	second := ledger.Last()
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), back)
	formerIs(launchRecipient, keyC, keyE, keyF, keyH)
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "hlgov: decision "+itoa(second)+" replaces "+keyH+", and the decision recipient is "+keyG+" now", func() { Execute(cross(cur), second) })
	if !ledger.Get(second).Pending() {
		t.Fatalf("the refused swap is %s", ledger.Get(second).State())
	}
	swap(keyH)
	formerIs(launchRecipient, keyC, keyE, keyF, keyG)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), second)
	if Recipient() != keyG || ledger.Get(second).Pending() {
		t.Fatalf("recipient %s, decision %d %s", Recipient(), second, ledger.Get(second).State())
	}
	formerIs(launchRecipient, keyC, keyE, keyF, keyH)
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
// which would drop the route Ethereum then enrolls as the router for the deployment's life: a realm in nobody's chain,
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
	aborts(t, succ+" succeeds "+pred+" and has pulled nothing yet (page 0 is not in): a kind 6 enrolling it waits for its first page, since a migrate that pulled nothing may be re-pointed and Ethereum must never enroll a dropped route", func() {
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

// TestEthereumKinds (eth §13): every Ethereum kind's body byte for byte against golden hex written out here — never
// computed by this realm's encoder — its addresses in every accepted spelling (EIP-55 mixed case, all upper, all
// lower, 64 hex digits), and each address parameter's refusals: a top twelve bytes not zero, a mixed case that is not
// the checksum, the zero address where the plan refuses it; kind 7's maximum; kind 8's five proxies and nothing
// else; kind 9's refusals. The addresses are EIP-55's own examples.
func TestEthereumKinds(cur realm, t *testing.T) {
	const (
		eip1 = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed" // EIP-55's mixed-case examples, in their checksum case
		eip2 = "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
		eip3 = "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB"
		eip4 = "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb"
		caps = "0x52908400098527886E0F7030069857D2E4169EE7" // EIP-55's all-caps example
		lows = "0x27b1fdb04752bbc536007a920d24acb045561c26" // EIP-55's all-lower example
		bad1 = "0x5aaeb6053F3E94C9b9A09f33669435E7Ef1BeAed" // eip1 with its first "A" in lower case: mixed, not the checksum
		// the same addresses as a decision body carries them: twelve zero bytes, then the twenty, in lower case
		eip1Body  = "0000000000000000000000005aaeb6053f3e94c9b9a09f33669435e7ef1beaed"
		eip2Body  = "000000000000000000000000fb6916095ca1df60bb79ce92ce3ea74c37c5d359"
		eip3Body  = "000000000000000000000000dbf03b407c01e7cd3cbea99509d93f8dddc8c6fb"
		eip4Body  = "000000000000000000000000d1220a0cf47c7b9be7a2e6ba89f429762e7b9adb"
		capsBody  = "00000000000000000000000052908400098527886e0f7030069857d2e4169ee7"
		lowsBody  = "00000000000000000000000027b1fdb04752bbc536007a920d24acb045561c26"
		gateBody  = "0000000000000000000000000202020202020202020202020202020202020202"
		tok1Body  = "000000000000000000000000051d52fd082d902f8e0cf300a60304d484cf2d07"
		clntBody  = "0000000000000000000000000303030303030303030303030303030303030303"
		maxWeiHex = "00038d7ea4c68000" // 10^15 wei, 0.001 ETH, as a big-endian u64
	)
	dirty := "01" + strings.Repeat("00", 11) + eip1Body[24:] // 64 hex digits whose top twelve bytes are not zero
	sentTo := func(kind byte, params string) {
		t.Helper()
		m := lastMessage(t)
		if hex.EncodeToString(m.Body) != bodyHex(kind, Last(), params) || m.Destination != 31337 || hex.EncodeToString(m.Recipient[:]) != pad(Recipient()) || Body(Last()) != hex.EncodeToString(m.Body) {
			t.Fatalf("kind %d: body %x to %d %x, want %s", kind, m.Body, m.Destination, m.Recipient, bodyHex(kind, Last(), params))
		}
	}
	// every address parameter refuses a non-zero top twelve bytes and a mixed case that is not the checksum
	for _, r := range []struct {
		what string
		mk   func(a string) Request
	}{
		{"kind 3's ISM", func(a string) Request { return NewSetTokenISMRequest(cross(cur), 0, a) }},
		{"kind 5's key", func(a string) Request { return NewSetGuardiansRequest(cross(cur), "5:"+a) }},
		{"kind 6's router", func(a string) Request { return NewEnrollRouterRequest(cross(cur), 0, 10, a) }},
		{"kind 7's beneficiary", func(a string) Request { return NewSetProtocolFeeRequest(cross(cur), 1, a) }},
		{"kind 8's proxy", func(a string) Request { return NewUpgradeRequest(cross(cur), a, eip4, hashC, "") }},
		{"kind 8's implementation", func(a string) Request { return NewUpgradeRequest(cross(cur), launchGate, a, hashC, "") }},
		{"kind 9's successor", func(a string) Request { return NewTransferToSuccessorRequest(cross(cur), a, hashC) }},
		{"the decision recipient", func(a string) Request { return swapRequest(cur, Recipient(), a) }},
		{"the recipient a swap replaces", func(a string) Request { return swapRequest(cur, a, eip4) }},
	} {
		asMember(cur)
		aborts(t, "is 32 bytes whose top twelve are not zero", func() { r.mk(dirty) })
		asMember(cur)
		aborts(t, "hlgov: not an Ethereum address: \""+bad1+"\" mixes upper and lower case but is not its EIP-55 checksum", func() { r.mk(bad1) })
		asMember(cur)
		aborts(t, "not an Ethereum address", func() { r.mk(eip1Body[24:]) }) // 40 hex digits without 0x
	}

	// kind 3: token u8 ‖ ism 32 — the gate, here in its 64-digit spelling
	asMember(cur)
	aborts(t, "the ISM must be the gate "+launchGate+" or the zero address, not "+eip1, func() { NewSetTokenISMRequest(cross(cur), 1, eip1) })
	asMember(cur)
	req := NewSetTokenISMRequest(cross(cur), 1, gateBody)
	if req.Description() != "set BUBBLE's ISM to "+launchGate {
		t.Fatalf("kind 3: %s", req.Description())
	}
	pass(cur, req)
	sentTo(3, "01"+gateBody)

	// kind 5: count u8 ‖ (seat u8 ‖ key 32)… — a mixed-case and an all-caps address
	asMember(cur)
	req = NewSetGuardiansRequest(cross(cur), "6:"+eip2+", 3:"+caps)
	if !strings.HasPrefix(req.Description(), "set guardians and watch keys: seat 6 to "+eip2+", seat 3 to 0x") || !strings.EqualFold(req.Description()[len(req.Description())-42:], caps) {
		t.Fatalf("kind 5: %s", req.Description())
	}
	pass(cur, req)
	sentTo(5, "02"+"06"+eip2Body+"03"+capsBody)
	if EthSeat(6) != eip2 || !strings.EqualFold(EthSeat(3), caps) {
		t.Fatalf("the mirror: %s %s", EthSeat(6), EthSeat(3))
	}

	// kind 6: token u8 ‖ domain u32 ‖ router 32 — another chain's router, a 20-byte address left-padded. The zero
	// router is refused for every domain — another chain's, gno.land's own, Ethereum's — and either token, in each
	// spelling (eth §13: HlAdmin executes it as unenrolling, which is no decision of the table), and nothing is
	// recorded or dispatched
	n6, last6 := ledger.Last(), Last()
	for _, z := range []string{zeros, strings.Repeat("00", 32), "0x" + strings.Repeat("00", 32)} {
		for _, domain := range []uint32{10, hlmailbox.LocalDomain(), hlmailbox.LaunchOrigin()} {
			for token := uint8(0); token < 2; token++ {
				asMember(cur)
				aborts(t, "hlgov: the router is the zero address, which HlAdmin executes as unenrolling the domain: unenrolling is no decision of this table (eth §13)", func() { NewEnrollRouterRequest(cross(cur), token, domain, z) })
			}
		}
	}
	if ledger.Last() != n6 || Last() != last6 {
		t.Fatalf("a refused zero router recorded or dispatched something: decisions %d, last %d", ledger.Last(), Last())
	}
	asMember(cur)
	req = NewEnrollRouterRequest(cross(cur), 0, 10, eip3)
	if req.Description() != "enroll GNOT's router for domain 10: "+eip3 {
		t.Fatalf("kind 6: %s", req.Description())
	}
	pass(cur, req)
	sentTo(6, "00"+"0000000a"+eip3Body)

	// kind 7: fee u64 in wei ‖ beneficiary 32 — at the 0.001 ETH maximum (ER11), not a wei above it
	asMember(cur)
	aborts(t, "the fee is at most 1000000000000000 wei (0.001 ETH), ProtocolFee's immutable maximum", func() { NewSetProtocolFeeRequest(cross(cur), 1_000_000_000_000_001, eip1) })
	asMember(cur)
	aborts(t, "the fee is at most", func() { NewSetProtocolFeeRequest(cross(cur), 10_000_000_000_000_000, eip1) }) // the plan's e1 maximum, 0.01 ETH
	asMember(cur)
	aborts(t, "the fee is at most", func() { NewSetProtocolFeeRequest(cross(cur), 18_446_744_073_709_551_615, eip1) })
	asMember(cur)
	aborts(t, "the beneficiary is the zero address", func() { NewSetProtocolFeeRequest(cross(cur), 0, "0x"+strings.Repeat("00", 32)) })
	asMember(cur)
	req = NewSetProtocolFeeRequest(cross(cur), 1_000_000_000_000_000, eip1)
	if req.Description() != "set the protocol fee to 1000000000000000 wei for "+eip1 {
		t.Fatalf("kind 7: %s", req.Description())
	}
	pass(cur, req)
	sentTo(7, maxWeiHex+eip1Body)
	asMember(cur)
	pass(cur, NewSetProtocolFeeRequest(cross(cur), 0, lows)) // a fee of 0 is allowed: the launch fee
	sentTo(7, "0000000000000000"+lowsBody)

	// kind 8: proxy 32 ‖ implementation 32 ‖ codehash 32 ‖ call — each of the five proxies, by name, and nothing else
	for i, proxy := range []string{launchMailbox, launchToken0, launchToken1, launchClient, launchGate} {
		asMember(cur)
		name := []string{"the mailbox", "token 0 (GNOT)", "token 1 (BUBBLE)", "the light client", "the gate"}[i]
		if d := NewUpgradeRequest(cross(cur), proxy, eip4, hashC, "").Description(); d != "upgrade "+name+" "+proxy+" to implementation "+eip4+" with code hash 0x"+hashC+", no call (upgrade)" {
			t.Fatalf("kind 8 on %s: %s", proxy, d)
		}
	}
	// HlAdmin (the launch one and the one now), any other address, and an address one byte off token 0's
	for _, notProxy := range []string{launchRecipient, Recipient(), eip1, keyA, "0x00" + strings.ToLower(launchToken0[4:])} {
		asMember(cur)
		aborts(t, "is none of the five proxies kind 8 upgrades", func() { NewUpgradeRequest(cross(cur), notProxy, eip4, hashC, "") })
	}
	asMember(cur)
	aborts(t, "the implementation must be set and differ from the proxy", func() { NewUpgradeRequest(cross(cur), launchGate, zeros, hashC, "") })
	asMember(cur)
	aborts(t, "the implementation must be set and differ from the proxy", func() { NewUpgradeRequest(cross(cur), launchGate, launchGate, hashC, "") })
	asMember(cur)
	aborts(t, "the code hash is all zeros", func() { NewUpgradeRequest(cross(cur), launchGate, eip4, strings.Repeat("00", 32), "") })
	// a code hash HlAdmin could never execute on is refused, by kind 8 and kind 9 alike, in each spelling (eth §13 as
	// plan e4 has it): the empty code's, keccak256 of nothing — an account holding no code, which HlAdmin never
	// compares, since a target with no code pends (NoCodeYet) — and zero; nothing is recorded or dispatched. One bit
	// off the empty code's hash is an ordinary hash
	const emptyCode = "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470" // keccak256 of nothing (EIP-1052's hash of an account with no code)
	nk, lastK := ledger.Last(), Last()
	for _, c := range []struct{ hash, why string }{
		{emptyCode, "hlgov: the code hash is the empty code's hash (keccak256 of nothing), which no contract's code hashes to: HlAdmin could never execute the decision; it pends while the target holds no code (NoCodeYet) and is spent once it holds some (eth section 13)"},
		{"0x" + emptyCode, "the code hash is the empty code's hash (keccak256 of nothing)"},
		{strings.ToUpper(emptyCode), "the code hash is the empty code's hash (keccak256 of nothing)"},
		{" 0x" + emptyCode + " ", "the code hash is the empty code's hash (keccak256 of nothing)"},
		{strings.Repeat("00", 32), "hlgov: the code hash is all zeros, which no contract's code hashes to: HlAdmin could never execute the decision; it pends while the target holds no code (NoCodeYet) and is spent once it holds some (eth section 13)"},
		{"0x" + strings.Repeat("00", 32), "the code hash is all zeros, which no contract's code hashes to"},
	} {
		asMember(cur)
		aborts(t, c.why, func() { NewUpgradeRequest(cross(cur), launchGate, eip4, c.hash, "") })
		asMember(cur)
		aborts(t, c.why, func() { NewTransferToSuccessorRequest(cross(cur), lows, c.hash) })
	}
	if ledger.Last() != nk || Last() != lastK {
		t.Fatalf("a refused code hash recorded or dispatched something: decisions %d, last %d", ledger.Last(), Last())
	}
	asMember(cur)
	if d := NewUpgradeRequest(cross(cur), launchGate, eip4, emptyCode[:63]+"1", "").Description(); !strings.Contains(d, " with code hash 0x"+emptyCode[:63]+"1, no call") {
		t.Fatalf("a hash one bit off the empty code's: %s", d)
	}
	asMember(cur)
	aborts(t, "the code hash is 32 bytes in hex", func() { NewUpgradeRequest(cross(cur), launchGate, eip4, strings.Repeat("ab", 31), "") })
	asMember(cur)
	aborts(t, "the code hash is 32 bytes in hex", func() { NewUpgradeRequest(cross(cur), launchGate, eip4, strings.Repeat("zz", 32), "") })
	asMember(cur)
	pass(cur, NewUpgradeRequest(cross(cur), strings.ToLower(launchToken1), eip4, "0x"+strings.Repeat("ab", 32), ""))
	sentTo(8, tok1Body+eip4Body+strings.Repeat("ab", 32)) // no call: the body ends at the code hash (upgrade)
	// with a call (ER7): the rest of the body, upgradeAndCall's calldata — here initialize()'s selector
	asMember(cur)
	req = NewUpgradeRequest(cross(cur), launchGate, eip4, hashC, "0x8129fc1c")
	if req.Description() != "upgrade the gate "+launchGate+" to implementation "+eip4+" with code hash 0x"+hashC+", then call 0x8129fc1c (4 bytes, upgradeAndCall)" {
		t.Fatalf("kind 8 with a call: %s", req.Description())
	}
	pass(cur, req)
	sentTo(8, gateBody+eip4Body+hashC+"8129fc1c")
	// the call's bound: 407 bytes fit (a 512-byte body, dispatched whole), 408 do not; an odd or a non-hex call is
	// refused; "0x" alone is no call
	full := strings.Repeat("c3", 407)
	asMember(cur)
	aborts(t, "the call is 408 bytes; at most 407 fit in the 512-byte decision body", func() { NewUpgradeRequest(cross(cur), launchClient, eip4, hashC, full+"c3") })
	asMember(cur)
	aborts(t, "the call is calldata in hex", func() { NewUpgradeRequest(cross(cur), launchClient, eip4, hashC, "0x8129fc1") })
	asMember(cur)
	aborts(t, "the call is calldata in hex", func() { NewUpgradeRequest(cross(cur), launchClient, eip4, hashC, "0xzz") })
	asMember(cur)
	if d := NewUpgradeRequest(cross(cur), launchClient, eip4, hashC, "0x").Description(); !strings.HasSuffix(d, " with code hash 0x"+hashC+", no call (upgrade)") {
		t.Fatalf("kind 8 with 0x for a call: %s", d)
	}
	asMember(cur)
	req = NewUpgradeRequest(cross(cur), launchClient, eip4, hashC, full)
	if !strings.HasSuffix(req.Description(), ", then call 0x"+full+" (407 bytes, upgradeAndCall)") {
		t.Fatalf("kind 8 with a 407-byte call: %s", req.Description())
	}
	pass(cur, req)
	sentTo(8, clntBody+eip4Body+hashC+full)
	if len(lastMessage(t).Body) != MaxDecisionBody {
		t.Fatalf("a body at the bound: %d bytes", len(lastMessage(t).Body))
	}

	// kind 9: successor 32 ‖ codehash 32 — never HlAdmin itself (the recipient now) nor a former one, never zero
	asMember(cur)
	aborts(t, "never HlAdmin itself ("+Recipient()+")", func() { NewTransferToSuccessorRequest(cross(cur), Recipient(), hashC) })
	asMember(cur)
	aborts(t, "never a former HlAdmin ("+launchRecipient+")", func() { NewTransferToSuccessorRequest(cross(cur), launchRecipient, hashC) })
	asMember(cur)
	aborts(t, "the successor must be set", func() { NewTransferToSuccessorRequest(cross(cur), zeros, hashC) })
	asMember(cur)
	aborts(t, "the code hash is all zeros", func() { NewTransferToSuccessorRequest(cross(cur), lows, "0x"+strings.Repeat("00", 32)) })
	asMember(cur)
	req = NewTransferToSuccessorRequest(cross(cur), lows, "0x"+strings.Repeat("cd", 32))
	if !strings.HasPrefix(req.Description(), "transfer every authority to 0x") || !strings.EqualFold(req.Description()[len("transfer every authority to "):len("transfer every authority to ")+42], lows) || !strings.HasSuffix(req.Description(), " (code hash 0x"+strings.Repeat("cd", 32)+")") {
		t.Fatalf("kind 9: %s", req.Description())
	}
	pass(cur, req)
	sentTo(9, lowsBody+strings.Repeat("cd", 32))

	// set decision recipient: Ethereum addresses, written in EIP-55 case whatever case or form they were typed in — the
	// recipient it replaces here in its 64-digit form
	asMember(cur)
	req = swapRequest(cur, strings.Repeat("00", 12)+Recipient()[2:], strings.ToLower(eip3))
	if req.Description() != "set decision recipient to "+eip3+", replacing "+Recipient()+" (applies only while "+Recipient()+" is the recipient)" {
		t.Fatalf("set decision recipient: %s", req.Description())
	}
	if p := Render(""); strings.Contains(p, "Solana") || !strings.Contains(p, "- #"+u64s(Last())+" kind 9: transfer every authority to 0x") {
		t.Fatalf("page: %s", p)
	}
}

// TestCancelDescription (the round-2 realms audit's E2-RL4): a cancel's description is its numbers as parsed —
// "from-to" for a range, the list joined by ", " — never the text as typed, which parseNumber trims around each
// number: "2", eighty newlines and "-  N" is a range cancel of 2 to N, and the description GovDAO's list, the ledger
// and this realm's pages show says so on one line (it said "cancel decisions 2" on its first, the range eighty lines
// below); a list typed with stray spaces, tabs and newlines reads as its numbers. The body is the range's, and the
// executed cancel's text is the description.
func TestCancelDescription(cur realm, t *testing.T) {
	top := Last()
	if top < 3 {
		t.Fatalf("only %d decisions dispatched", top)
	}
	h := strconv.FormatUint(top, 16)
	topHex := strings.Repeat("0", 16-len(h)) + h
	asMember(cur)
	req := NewCancelRequest(cross(cur), "2"+strings.Repeat("\n", 80)+"-  "+u64s(top))
	want := "cancel decisions 2-" + u64s(top)
	if req.Description() != want {
		t.Fatalf("the range's description %q, want %q", req.Description(), want)
	}
	pass(cur, req)
	n := Last()
	m := lastMessage(t)
	// kind 1 ‖ number ‖ salt 8 ‖ form 1 ‖ channel 0 ‖ from 2 ‖ to top
	if len(m.Body) != 9+8+2+16 || m.Body[0] != KindCancel || hex.EncodeToString(m.Body[17:]) != "0100"+"0000000000000002"+topHex {
		t.Fatalf("the range cancel's body %x", m.Body)
	}
	if !strings.Contains(Render(""), "- #"+u64s(n)+" kind 1: "+want+" (sent 1×") || !strings.Contains(Render("decision/"+u64s(n)), ",\"text\":\""+want+"\"}") ||
		ledger.Get(ledger.Last()).Text != want || strings.Contains(Render(""), "cancel decisions 2\n") {
		t.Fatalf("the range's text on the pages: %s\n%s", Render(""), Render("decision/"+u64s(n)))
	}
	// a list typed loosely: its numbers, in the order written, and the body's
	asMember(cur)
	req = NewCancelRequest(cross(cur), " 3 ,\t5,\n\n7 ,")
	if req.Description() != "cancel decisions 3, 5, 7" {
		t.Fatalf("the list's description %q", req.Description())
	}
	pass(cur, req)
	if b := lastMessage(t).Body; hex.EncodeToString(b[17:]) != "0003"+"00"+"0000000000000003"+"00"+"0000000000000005"+"00"+"0000000000000007" {
		t.Fatalf("the list cancel's body %x", b)
	}
	// a single number, as the e2e makes one
	asMember(cur)
	if d := NewCancelRequest(cross(cur), " 2 ").Description(); d != "cancel decisions 2" {
		t.Fatalf("one number: %q", d)
	}
}

// TestDeploymentTargets (the round-2 realms audit's E2-RL7): no builder names a target that can never serve — kind 9
// refuses each of the deployment's sixteen contracts (hlmailbox's list, none of which answers predecessor() with
// HlAdmin) as the successor, kind 8 each as the implementation (an upgrade names code deployed for it), the recipient
// swap each but HlAdmin (the launch HlAdmin, which a swap back may restore, E2-RL2); kind 6 refuses Ethereum's own
// domain and gno.land's other one — 1198419788 on this test chain (1198419796 on gnoland-1: TestRefusedDomain) — for
// either token, while another chain's domain takes a router as before. Nothing refused is recorded or dispatched.
func TestDeploymentTargets(cur realm, t *testing.T) {
	const other = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed" // EIP-55's first example, no contract of this deployment
	n0, last0 := ledger.Last(), Last()
	if !isFormer(ethContract(hlmailbox.EthHlAdmin)) || Recipient() == launchRecipient {
		t.Fatalf("the launch HlAdmin should be a former recipient here: recipient %s", Recipient())
	}
	for i := 0; i < hlmailbox.EthContracts; i++ {
		name, k := hlmailbox.EthContractAt(i)
		a := hlmailbox.EthAddress(k)
		// kind 9: never a successor (the launch HlAdmin, a former recipient, is refused as one)
		asMember(cur)
		if i == hlmailbox.EthHlAdmin {
			aborts(t, "hlgov: never a former HlAdmin ("+a+")", func() { NewTransferToSuccessorRequest(cross(cur), a, hashC) })
		} else {
			aborts(t, "hlgov: the successor "+a+" is "+name+" of this deployment, which can never succeed HlAdmin (its predecessor() is not HlAdmin): HlAdmin would never execute the decision", func() { NewTransferToSuccessorRequest(cross(cur), a, hashC) })
		}
		// kind 8: never an implementation (the proxy itself keeps its own refusal; its own launch implementation is a
		// rollback, TestRollback)
		proxy := launchGate
		if i == hlmailbox.EthGate || i == hlmailbox.EthGateImpl {
			proxy = launchMailbox
		}
		asMember(cur)
		aborts(t, "hlgov: the implementation "+a+" is "+name+" of this deployment: an upgrade names code deployed for it", func() { NewUpgradeRequest(cross(cur), proxy, strings.ToLower(a), hashC, "") })
		// the recipient swap: HlAdmin only
		asMember(cur)
		if i == hlmailbox.EthHlAdmin {
			if d := swapRequest(cur, Recipient(), a).Description(); d != "set decision recipient to "+a+", replacing "+Recipient()+" (applies only while "+Recipient()+" is the recipient)" {
				t.Fatalf("a swap back to HlAdmin: %s", d)
			}
		} else {
			aborts(t, "hlgov: "+a+" is "+name+" of this deployment, which records no decision: the recipient is HlAdmin or a successor of it", func() { swapRequest(cur, Recipient(), a) })
		}
	}
	// kind 8 and 9 still take an address of no contract here
	asMember(cur)
	if d := NewUpgradeRequest(cross(cur), launchGate, other, hashC, "").Description(); !strings.Contains(d, " to implementation "+other+" ") {
		t.Fatalf("kind 8: %s", d)
	}
	asMember(cur)
	if d := NewTransferToSuccessorRequest(cross(cur), other, hashC).Description(); !strings.HasPrefix(d, "transfer every authority to "+other) {
		t.Fatalf("kind 9: %s", d)
	}
	// kind 6: Ethereum's own domain and gno.land's other domain, for either token; another chain's domain as before
	for token := uint8(0); token < 2; token++ {
		asMember(cur)
		aborts(t, "hlgov: domain 31337 is Ethereum's own: a token enrolled for it would dispatch every burn there, which no relayer delivers and no signer counts — final and credited nowhere", func() { NewEnrollRouterRequest(cross(cur), token, hlmailbox.LaunchOrigin(), other) })
		asMember(cur)
		aborts(t, "hlgov: domain 1198419788 is gno.land's other domain (this chain's is 1198419796): no client on Ethereum proves it, so a burn to it would be final and credited nowhere", func() { NewEnrollRouterRequest(cross(cur), token, hlmailbox.DomainMainnet, other) })
		asMember(cur)
		if d := NewEnrollRouterRequest(cross(cur), token, 10, other).Description(); !strings.HasSuffix(d, "'s router for domain 10: "+other) {
			t.Fatalf("kind 6 for domain 10: %s", d)
		}
	}
	if ledger.Last() != n0 || Last() != last0 {
		t.Fatalf("a builder recorded or dispatched something: decisions %d, last %d", ledger.Last(), Last())
	}
}

// TestRollback (Q-F6c, the round-2 realms fold's review): kind 8 takes each proxy's own launch implementation — a
// rollback to the audited launch code, which stays deployed — in any spelling, its description saying so, and
// dispatches it; each proxy refuses the other four launch implementations and every other contract of the deployment,
// as E2-RL7 has it (the proxy as its own implementation keeps its own refusal, TestKinds). The launch implementations
// are ethdeploy.gno's placeholders, spelled out here, never read from the realm.
func TestRollback(cur realm, t *testing.T) {
	launches := []struct{ name, proxy, impl string }{
		{"the mailbox", launchMailbox, "0x0808080808080808080808080808080808080808"},
		{"token 0 (GNOT)", launchToken0, "0x0909090909090909090909090909090909090909"},
		{"token 1 (BUBBLE)", launchToken1, "0x7070707070707070707070707070707070707070"},
		{"the light client", launchClient, "0x8080808080808080808080808080808080808080"},
		{"the gate", launchGate, "0x9090909090909090909090909090909090909090"},
	}
	n0, last0 := ledger.Last(), Last()
	for _, x := range launches {
		// its own launch implementation, the proxy in lower case and the implementation in its 64-digit form: taken
		asMember(cur)
		d := ""
		if r := revive(func() {
			d = NewUpgradeRequest(cross(cur), strings.ToLower(x.proxy), strings.Repeat("00", 12)+x.impl[2:], hashC, "").Description()
		}); r != nil {
			t.Fatalf("the rollback of %s to its launch implementation %s refused: %v", x.name, x.impl, r)
		}
		if d != "upgrade "+x.name+" "+x.proxy+" to implementation "+x.impl+" (its launch implementation: a rollback) with code hash 0x"+hashC+", no call (upgrade)" {
			t.Fatalf("the rollback of %s: %s", x.name, d)
		}
		// every other contract of the deployment, the other proxies' launch implementations among them: refused
		for i := 0; i < hlmailbox.EthContracts; i++ {
			name, k := hlmailbox.EthContractAt(i)
			a := hlmailbox.EthAddress(k)
			if a == x.impl || a == x.proxy {
				continue
			}
			asMember(cur)
			aborts(t, "hlgov: the implementation "+a+" is "+name+" of this deployment: an upgrade names code deployed for it, or the proxy's own launch implementation (a rollback), never another of the deployment's sixteen contracts", func() { NewUpgradeRequest(cross(cur), x.proxy, a, hashC, "") })
		}
	}
	if ledger.Last() != n0 || Last() != last0 {
		t.Fatalf("a builder recorded or dispatched something: decisions %d, last %d", ledger.Last(), Last())
	}
	// a rollback passed: the gate back to its launch implementation, the body byte for byte
	gateImpl := launches[4].impl
	asMember(cur)
	pass(cur, NewUpgradeRequest(cross(cur), launchGate, gateImpl, hashD, ""))
	if m := lastMessage(t); Last() != last0+1 || hex.EncodeToString(m.Body) != bodyHex(8, last0+1, pad(launchGate)+pad(gateImpl)+hashD) || hex.EncodeToString(m.Recipient[:]) != pad(Recipient()) {
		t.Fatalf("the rollback dispatched: last %d, body %x to %x", Last(), m.Body, m.Recipient)
	}
}

// TestDeploymentKeys (E2-RL7's class, the F6 review's follow-ups): kind 5 never makes a contract of the deployment a
// seat's key — a guardian's or a watch key's: hlmailbox's sixteen flag and hold nothing, so the seat would be held by
// nobody — nor kind 7 its beneficiary — none of the sixteen takes ETH, so the hook's collectProtocolFees would revert
// until another kind 7: each of the sixteen is refused in any spelling, on every seat, as the only entry of a kind 5
// or the second, while an address of no contract is taken by both. Nothing refused is recorded or dispatched.
func TestDeploymentKeys(cur realm, t *testing.T) {
	const other = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed" // EIP-55's first example, no contract of this deployment
	n0, last0 := ledger.Last(), Last()
	for i := 0; i < hlmailbox.EthContracts; i++ {
		name, k := hlmailbox.EthContractAt(i)
		a := hlmailbox.EthAddress(k)
		seat, first := 1+i%seatCount, 1+(i+1)%seatCount // every seat in turn, and another before it
		why := "hlgov: seat " + strconv.Itoa(seat) + "'s key " + a + " is " + name + " of this deployment, a contract of the bridge that flags and holds nothing: the seat would be held by nobody"
		asMember(cur)
		aborts(t, why, func() {
			NewSetGuardiansRequest(cross(cur), strconv.Itoa(seat)+":"+strings.Repeat("00", 12)+strings.ToLower(a[2:]))
		})
		asMember(cur)
		aborts(t, why, func() {
			NewSetGuardiansRequest(cross(cur), strconv.Itoa(first)+":"+other+", "+strconv.Itoa(seat)+":"+a)
		})
		asMember(cur)
		aborts(t, "hlgov: the beneficiary "+a+" is "+name+" of this deployment, which takes no ETH: collectProtocolFees would revert, the fees held in the hook until another kind 7", func() { NewSetProtocolFeeRequest(cross(cur), 1, strings.ToLower(a)) })
	}
	// an address of no contract: a seat's key and the beneficiary as before
	asMember(cur)
	if d := NewSetGuardiansRequest(cross(cur), "5:"+other).Description(); !strings.HasPrefix(d, "set guardians and watch keys: seat 5 to "+other) {
		t.Fatalf("kind 5: %s", d)
	}
	asMember(cur)
	if d := NewSetProtocolFeeRequest(cross(cur), 1, other).Description(); d != "set the protocol fee to 1 wei for "+other {
		t.Fatalf("kind 7: %s", d)
	}
	if ledger.Last() != n0 || Last() != last0 {
		t.Fatalf("a builder recorded or dispatched something: decisions %d, last %d", ledger.Last(), Last())
	}
}

// TestHlAdminLineKeys (F9's reviews: the first's finding 5, the second's 2–4): kind 5's keys, kind 7's beneficiary and
// kind 8's implementation refuse HlAdmin's line — every address decisions go to or went to: the recipient (a successor
// of HlAdmin once a swap names it) and every former recipient — when the proposal is made (as any entry of a kind 5)
// and in its closure, since a swap moves the line: a kind 5, a kind 7 and a kind 8 naming keyI, pre-armed while keyI
// is nobody, are refused once a swap makes keyI the recipient, nothing dispatched. Conversely a swap refuses a key that
// holds an Ethereum seat as last set here, when made and at Execute — there after a kind 5 seated it once the swap was
// decided, the swap left pending. The recipient is keyG here; the former ones keyC, keyE, keyF, keyH and the launch
// HlAdmin (refused as one of the sixteen, TestDeploymentKeys).
func TestHlAdminLineKeys(cur realm, t *testing.T) {
	keyE, keyF, keyG, keyH, keyI, keyJ := "0x"+strings.Repeat("50", 20), "0x"+strings.Repeat("60", 20), "0x"+strings.Repeat("61", 20), "0x"+strings.Repeat("62", 20), "0x"+strings.Repeat("63", 20), "0x"+strings.Repeat("64", 20)
	const other = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed" // EIP-55's first example: no contract, not of the line
	if Recipient() != keyG {
		t.Fatalf("recipient %s", Recipient())
	}
	n0, last0 := ledger.Last(), Last()
	for _, c := range []struct{ key, why string }{
		{keyG, "the decision recipient (HlAdmin's line)"},
		{keyC, "a former decision recipient (HlAdmin's line)"},
		{keyE, "a former decision recipient (HlAdmin's line)"},
		{keyF, "a former decision recipient (HlAdmin's line)"},
		{keyH, "a former decision recipient (HlAdmin's line)"},
	} {
		asMember(cur)
		aborts(t, "hlgov: seat 5's key "+c.key+" is "+c.why+": an address decisions go to, or went to, never holds a seat", func() { NewSetGuardiansRequest(cross(cur), "5:"+c.key) })
		asMember(cur)
		aborts(t, "hlgov: seat 6's key "+c.key+" is "+c.why+": an address decisions go to", func() { NewSetGuardiansRequest(cross(cur), "1:"+other+", 6:"+c.key) })
		asMember(cur)
		aborts(t, "hlgov: the beneficiary "+c.key+" is "+c.why+": the fees never go to an address decisions go to, or went to", func() { NewSetProtocolFeeRequest(cross(cur), 1, c.key) })
		asMember(cur)
		aborts(t, "hlgov: the implementation "+c.key+" is "+c.why+": an address decisions go to, or went to, is never code behind a proxy", func() { NewUpgradeRequest(cross(cur), launchGate, c.key, hashC, "") })
	}
	if ledger.Last() != n0 || Last() != last0 {
		t.Fatalf("a builder recorded or dispatched something: decisions %d, last %d", ledger.Last(), Last())
	}
	// pre-armed while keyI is nobody, beside the swap that makes keyI the recipient: the closures refuse
	asMember(cur)
	pass(cur, swapRequest(cur, keyG, keyI))
	toI := ledger.Last()
	asMember(cur)
	five := NewSetGuardiansRequest(cross(cur), "6:"+keyI)
	testing.SetRealm(testing.NewUserRealm(member))
	pid5 := dao.MustCreateProposal(cross(cur), five.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid5), "YES")
	asMember(cur)
	seven := NewSetProtocolFeeRequest(cross(cur), 1, keyI)
	testing.SetRealm(testing.NewUserRealm(member))
	pid7 := dao.MustCreateProposal(cross(cur), seven.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid7), "YES")
	asMember(cur)
	eight := NewUpgradeRequest(cross(cur), launchGate, keyI, hashC, "")
	testing.SetRealm(testing.NewUserRealm(member))
	pid8 := dao.MustCreateProposal(cross(cur), eight.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid8), "YES")
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), toI)
	if Recipient() != keyI {
		t.Fatalf("recipient %s", Recipient())
	}
	last, n1 := Last(), ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlgov: seat 6's key "+keyI+" is the decision recipient (HlAdmin's line)", func() { dao.ExecuteProposal(cross(cur), pid5) })
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlgov: the beneficiary "+keyI+" is the decision recipient (HlAdmin's line)", func() { dao.ExecuteProposal(cross(cur), pid7) })
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlgov: the implementation "+keyI+" is the decision recipient (HlAdmin's line)", func() { dao.ExecuteProposal(cross(cur), pid8) })
	if Last() != last || ledger.Last() != n1 {
		t.Fatalf("a refused pre-armed decision dispatched or recorded: last %d decisions %d", Last(), ledger.Last())
	}
	// the converse: a swap never names a key that holds a seat as last set here (keyA holds seat 1, TestGuardians) …
	if EthSeat(1) != keyA {
		t.Fatalf("seat 1 %s", EthSeat(1))
	}
	asMember(cur)
	aborts(t, "hlgov: "+keyA+" holds seat 1 as last set here, a guardian's or watch key's: the recipient is HlAdmin or a successor of it", func() { swapRequest(cur, keyI, keyA) })
	// … nor applies once a kind 5 seated its target after it was decided: pending for GovDAO's cancel
	asMember(cur)
	pass(cur, swapRequest(cur, keyI, keyJ))
	toJ := ledger.Last()
	asMember(cur)
	pass(cur, NewSetGuardiansRequest(cross(cur), "4:"+keyJ))
	if EthSeat(4) != keyJ {
		t.Fatalf("seat 4 %s", EthSeat(4))
	}
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "hlgov: decision "+itoa(toJ)+" names "+keyJ+", which holds seat 4 as last set here, a guardian's or watch key's: cancel decision "+itoa(toJ), func() { Execute(cross(cur), toJ) })
	if !ledger.Get(toJ).Pending() || Recipient() != keyI {
		t.Fatalf("the swap to a seated key: %s, recipient %s", ledger.Get(toJ).State(), Recipient())
	}
}

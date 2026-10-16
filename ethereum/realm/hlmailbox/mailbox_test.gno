package hlmailbox

import (
	"chain"
	"chain/runtime"
	"crypto/bech32"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/p/nt/testutils/v0"
	"gno.land/r/gov/dao"
	govinit "gno.land/r/gov/dao/init/v0"
)

// The tests drive the real GovDAO (r/gov/dao with impl/v0, one T1 member) so every builder's closure runs the way
// a proposal's execution runs it — after the yield; before it (M1k) the compiled governor makes every decision
// directly through Govern, the way the first decisions on a chain are made: the launch, the verifier, the signers
// (TestLaunch to TestRotateSigners), then TestYield hands over to GovDAO and the tests after run as proposals.
// Test time moves 5 s a height (testing.SkipHeights).

const (
	ethTest      = uint32(31337) // testEthereumDomain: the launch origin on every chain but gnoland-1
	testVerifier = namespace + "hltestverifier"
	dayHeights   = 86400 / 5
)

var (
	member    = testutils.TestAddress("hl-member")
	recipient = testutils.TestAddress("hlm-recipient")
	stranger  = testutils.TestAddress("hlm-other")
)

// verifier2of3 is a verifier over this realm's own signer set: the ed25519 threshold check of the hyperlane
// package, which the real hlverifier realm also runs.
type verifier2of3 struct{}

func (verifier2of3) Verify(origin uint32, metadata, message []byte) error {
	ks, rule := Signers()
	keys := make([][]byte, len(ks))
	for i := range ks {
		k := ks[i]
		keys[i] = k[:]
	}
	return hl.VerifyMultisig(keys, rule.Threshold, metadata, message)
}

// satisfiableNo makes the test verifier answer no to every Satisfiable: a verifier whose judgment differs from the
// rule's shape (K19's case).
var satisfiableNo bool

// Satisfiable is hlverifier's rule: the threshold many, or one of each group.
func (verifier2of3) Satisfiable(signers []Key) bool {
	if satisfiableNo {
		return false
	}
	_, rule := Signers()
	if rule.TwoGroups() {
		return anyOf(rule.GroupA, signers) && anyOf(rule.GroupB, signers)
	}
	return len(signers) >= rule.Threshold
}

func anyOf(group, signers []Key) bool {
	for _, g := range group {
		if indexOf(signers, g) >= 0 {
			return true
		}
	}
	return false
}

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
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

// pass creates a proposal from the member, votes yes and executes it: GovDAO's whole path (after the yield).
func pass(cur realm, req Request) dao.ProposalID {
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		panic("the proposal did not execute")
	}
	return pid
}

// govern makes a builder's decision as the governor, directly (M1k): the whole path before the yield. The realm is
// set in this frame, right before the call, as pass sets the member's.
func govern(cur realm, req Request) {
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	Govern(cross(cur), req)
}

// refusedByDAO proves GovDAO's proxy cannot make a builder's decision before the yield: the proposal is created
// and voted, and its execution aborts on the governor's rule — a panic, so on chain the proposal survives for a
// retry after the yield (the harness keeps the DAO's accepted mark, so no retry here) — with nothing recorded.
func refusedByDAO(cur realm, t *testing.T, what string, req Request) {
	t.Helper()
	n := ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlmailbox: until the governor yields, "+string(launchGovernor)+" alone decides, by Govern from its own key: not "+decisions.DAOPath+" (a GovDAO proposal is refused until the yield)", func() { dao.ExecuteProposal(cross(cur), pid) })
	if ledger.Last() != n {
		t.Fatalf("%s: a refused proposal recorded a decision", what)
	}
}

func asMember(cur realm)   { testing.SetRealm(testing.NewUserRealm(member)) }
func asGovernor(cur realm) { testing.SetRealm(testing.NewUserRealm(launchGovernor)) }

func skipDays(n int64) { testing.SkipHeights(n*dayHeights + 1) }

func TestDomainAndHook(cur realm, t *testing.T) {
	if LocalDomain() != DomainTest || LaunchOrigin() != testEthereumDomain {
		t.Fatalf("domains %d %d", LocalDomain(), LaunchOrigin())
	}
	if domainOf("gnoland-1") != DomainMainnet || DomainMainnet != 1198419788 || ethereumOf("gnoland-1") != EthereumMainnet {
		t.Fatalf("mainnet domains")
	}
	want, _ := hl.AddressToBytes32(chain.PackageAddress(pkgPath))
	if Hook() != hex.EncodeToString(want[:]) || !strings.HasPrefix(Hook(), "000000000000000000000000") {
		t.Fatalf("hook %s", Hook())
	}
}

// TestLaunchValues: init wrote the compiled values, and nothing is live.
func TestLaunchValues(cur realm, t *testing.T) {
	if Live() || launchedAt != 0 {
		t.Fatalf("live before Launch")
	}
	if ActiveVerifier(ethTest) != launchVerifier || VerifierRegistered(launchVerifier) {
		t.Fatalf("verifier %s", ActiveVerifier(ethTest))
	}
	ks, rule := Signers()
	if len(ks) != 3 || rule.Threshold != 2 || rule.TwoGroups() || SignerKeys() != launchSigners || rule.String() != "2 of the set" {
		t.Fatalf("signers %d %+v", len(ks), rule)
	}
	for i := 1; i <= 6; i++ {
		if Seat(i) == "" || SeatOf(Seat(i)) != i {
			t.Fatalf("seat %d", i)
		}
	}
	if Seat(0) != "" || Seat(7) != "" || SeatOf(stranger) != 0 || IsGuardianSeat(5) || !IsGuardianSeat(4) {
		t.Fatalf("seat bounds")
	}
	if !IsRoute(launchRouteGNOT) || !IsRoute(launchRouteBUBBLE) || len(Routes()) != 2 || IsRoute(pkgPath) {
		t.Fatalf("routes %v", Routes())
	}
	// the governor (M1k): compiled, not yielded, shown on the page and in the json
	if Governor() != launchGovernor || launchGovernor == "" || Yielded() != 0 {
		t.Fatalf("governor %s yielded %d", Governor(), Yielded())
	}
	p := Render("")
	for _, want := range []string{"**not launched**", "`" + launchVerifier + "`", "**not registered**", "rule: 2 of the set", "seat 1 (guardian): " + string(Seat(1)), "seat 5 (watch key)", "`" + launchRouteGNOT + "`: payouts epoch 0, no flag", "- clock factor 1 (1 on mainnet; a devnet build is compiled with 8): the 7-day delay is 604800 s and the 14-day deadline 1209600 s of block time",
		"- governor: " + string(launchGovernor) + " (until the yield) — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused\n"} {
		if !strings.Contains(p, want) {
			t.Fatalf("page lacks %q:\n%s", want, p)
		}
	}
	if !strings.Contains(Render("json"), "\"live\":false,") || !strings.Contains(Render("json"), "\"31337\":{\"pkgpath\":\""+launchVerifier+"\",\"registered\":false}") || !strings.Contains(Render("json"), "\"clocks\":{\"factor\":1,\"delay\":604800,\"deadline\":1209600}") || !strings.Contains(Render("json"), "\"launchedAt\":0,\"governor\":\""+string(launchGovernor)+"\",\"yielded\":0,") {
		t.Fatalf("json: %s", Render("json"))
	}
	if !strings.Contains(Render("decisions"), "No decision yet") {
		t.Fatalf("decisions page")
	}
}

// TestBeforeLaunch: every builder but Launch and cancel refuses, and Process takes nothing.
func TestBeforeLaunch(cur realm, t *testing.T) {
	asMember(cur)
	aborts(t, "not launched", func() { NewSetVerifierRequest(cross(cur), ethTest, testVerifier) })
	asMember(cur)
	aborts(t, "not launched", func() { NewRotateSignersRequest(cross(cur), launchSigners, 1, "", "") })
	asMember(cur)
	aborts(t, "not launched", func() { NewRotateSeatsRequest(cross(cur), "2:") })
	asMember(cur)
	aborts(t, "not launched", func() { NewUnpauseRequest(cross(cur), launchRouteGNOT, "payouts", 0) })
	asMember(cur)
	aborts(t, "not pending", func() { NewCancelRequest(cross(cur), 1) })
	testing.SetRealm(testing.NewUserRealm(recipient))
	aborts(t, "is not registered", func() { Process(cross(cur), unhex(inHelloMeta), unhex(inHelloMsg)) })
}

// TestRegisterVerifier: a realm under /r/ registers once; users and /p/ paths cannot.
func TestRegisterVerifier(cur realm, t *testing.T) {
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "only a realm under gno.land/r/", func() { RegisterVerifier(cross(cur), verifier2of3{}) })
	testing.SetRealm(testing.NewCodeRealm(testVerifier))
	RegisterVerifier(cross(cur), verifier2of3{})
	if !VerifierRegistered(testVerifier) {
		t.Fatalf("not registered")
	}
	testing.SetRealm(testing.NewCodeRealm(testVerifier))
	aborts(t, "registered already", func() { RegisterVerifier(cross(cur), verifier2of3{}) })
}

// TestRegisterVerifierSub (X1): a sub-realm identity — gno v1.2.0's `cur.Sub`, a token a realm mints for free in
// its own namespace, whose pkgpath is `host#subpath` and passes the `gno.land/r/` prefix — is refused as a
// registrant, so every entry costs a package deployment. The token is minted before anything else: the harness's
// SetRealm rewrites this frame's cur, after which Sub refuses it.
func TestRegisterVerifierSub(cur realm, t *testing.T) {
	sub := cur.Sub("spam")
	if sub.Subpath() != "spam" || sub.PkgPath() != pkgPath+"#spam" || !strings.HasPrefix(sub.PkgPath(), "gno.land/r/") {
		t.Fatalf("sub-identity %s %q", sub.PkgPath(), sub.Subpath())
	}
	n := verifiers.Size()
	aborts(t, "not a sub-identity", func() { RegisterVerifier(cross(sub), verifier2of3{}) })
	if VerifierRegistered(pkgPath+"#spam") || verifiers.Size() != n {
		t.Fatalf("a sub-identity registered: %d verifiers", verifiers.Size())
	}
}

// TestLaunch: the launch is the governor's decision like any other (M1k) — a request it left 14 days lapses (the
// deadline error, nothing live, nothing recorded) and is made afresh; GovDAO's proposal of it is refused until
// the yield; the governor's Govern makes the realm live, once.
func TestLaunch(cur realm, t *testing.T) {
	asMember(cur)
	govinit.InitWithUsers(cross(cur), member)
	if !strings.Contains(Render(""), "executes within 14 days of its proposal") || !strings.Contains(Render(""), "executed with r/gov/dao's `ExecuteOrRejectProposal`") {
		t.Fatalf("page: %s", Render(""))
	}
	asGovernor(cur)
	stale := NewLaunchRequest(cross(cur))
	skipDays(15)
	aborts(t, "hlmailbox: the proposal is past its 14-day deadline", func() { govern(cur, stale) })
	if Live() || launchedAt != 0 || ledger.Last() != 0 {
		t.Fatalf("a lapsed launch left something: live %v, decisions %d", Live(), ledger.Last())
	}
	// GovDAO's launch, voted, is refused until the governor yields: nothing live, nothing recorded
	asMember(cur)
	refusedByDAO(cur, t, "launch", NewLaunchRequest(cross(cur)))
	if Live() || launchedAt != 0 {
		t.Fatalf("GovDAO launched before the yield")
	}
	// a stranger's Govern, a code realm's, and nothing of a request: refused
	asMember(cur)
	req := NewLaunchRequest(cross(cur))
	prop := req.Proposal()
	if !strings.Contains(req.Description(), "launch: the compiled values go live") || !strings.Contains(req.Description(), launchSigners) || prop.Description() != req.Description() || prop.Title() != "eth/hlmailbox: launch" {
		t.Fatalf("description: %s", req.Description())
	}
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "hlmailbox: until the governor yields, "+string(launchGovernor)+" alone decides, by Govern from its own key: not "+string(stranger), func() { Govern(cross(cur), req) })
	testing.SetRealm(testing.NewCodeRealm(launchRouteGNOT))
	aborts(t, "alone decides, by Govern from its own key: not "+launchRouteGNOT, func() { Govern(cross(cur), req) })
	asGovernor(cur)
	aborts(t, "not a builder's request", func() { Govern(cross(cur), Request{}) })
	if Live() || ledger.Last() != 0 {
		t.Fatalf("a refused Govern left something")
	}
	// the governor's own call, in time: live
	govern(cur, req)
	if !Live() || launchedAt == 0 {
		t.Fatalf("not live")
	}
	d := ledger.Get(1)
	if d == nil || d.Kind != "launch" || d.State() != "applied" || ledger.Last() != 1 {
		t.Fatalf("decision 1: %+v", d)
	}
	asMember(cur)
	aborts(t, "launched already", func() { NewLaunchRequest(cross(cur)) })
	if !strings.Contains(Render(""), "**live** since "+strconv.FormatInt(launchedAt, 10)) || !strings.Contains(Render("decisions"), "### launch") {
		t.Fatalf("page: %s", Render(""))
	}
	// the same request a second time: the closure's own refusal ("launched already"), nothing recorded
	aborts(t, "hlmailbox: launched already", func() { govern(cur, req) })
	if ledger.Last() != 1 {
		t.Fatalf("a second launch recorded")
	}
}

// TestGovernor (M1k): before the yield every builder's request is refused when GovDAO's proxy executes it — the
// one closure every builder shares refuses the proxy before anything — and nothing is recorded; the builders'
// own checks still hold for the governor (the realm's refusals, not the governor's wish, decide what a request
// may say).
func TestGovernor(cur realm, t *testing.T) {
	g2 := Seat(2)
	testing.SetRealm(testing.NewUserRealm(g2))
	SetFlag(cross(cur), launchRouteGNOT, FnSends, "")
	asMember(cur)
	refusedByDAO(cur, t, "set verifier", NewSetVerifierRequest(cross(cur), ethTest, testVerifier))
	asMember(cur)
	refusedByDAO(cur, t, "rotate signers", NewRotateSignersRequest(cross(cur), launchSigners, 3, "", "")) // a rule change: no verifier is asked when it is made
	asMember(cur)
	refusedByDAO(cur, t, "rotate seats", NewRotateSeatsRequest(cross(cur), "3:"))
	asMember(cur)
	refusedByDAO(cur, t, "unpause", NewUnpauseRequest(cross(cur), launchRouteGNOT, "sends", 0))
	if ActiveVerifier(ethTest) != launchVerifier || SignerKeys() != launchSigners || Seat(3) == "" || !Paused(launchRouteGNOT, FnSends) {
		t.Fatalf("a refused proposal changed something")
	}
	testing.SetRealm(testing.NewUserRealm(g2))
	ClearFlag(cross(cur), launchRouteGNOT, FnSends)
	// the governor is not above a builder's checks: nonsense is refused when the request is made, as for anyone
	asGovernor(cur)
	aborts(t, "no verifier registered at", func() { NewSetVerifierRequest(cross(cur), ethTest, namespace+"nobody") })
	asGovernor(cur)
	aborts(t, "nothing changes", func() { NewRotateSignersRequest(cross(cur), launchSigners, 2, "", "") })
}

// TestDispatch: a realm sends two messages; each is kept with the root its checkpoint names, and the roots are a
// tree's built apart from the mailbox's. Reemit re-emits a kept message and refuses an unknown nonce.
func TestDispatch(cur realm, t *testing.T) {
	senderPath := namespace + "hltestsender"
	sender, _ := hl.AddressToBytes32(chain.PackageAddress(senderPath))
	n0 := Count()
	var shadow hl.Tree
	for i := uint32(0); i < n0; i++ {
		raw, _ := Message(i)
		shadow.Insert(hl.ID(unhex(raw)))
	}
	to := [32]byte{1, 2, 3}
	for i, body := range []string{"one", "two"} {
		testing.SetRealm(testing.NewCodeRealm(senderPath))
		id := Dispatch(cross(cur), ethTest, to, []byte(body))
		nonce := n0 + uint32(i)
		if Count() != nonce+1 {
			t.Fatalf("count %d after nonce %d", Count(), nonce)
		}
		rawHex, rootHex := Message(nonce)
		raw := unhex(rawHex)
		m, err := hl.ParseMessage(raw)
		if err != nil || m.Version != 3 || m.Nonce != nonce || m.Origin != DomainTest || m.Sender != sender ||
			m.Destination != ethTest || m.Recipient != to || string(m.Body) != body {
			t.Fatalf("message %d: %v %+v", nonce, err, m)
		}
		if hl.ID(raw) != id {
			t.Fatalf("returned id is not the message's")
		}
		shadow.Insert(id)
		r := shadow.Root()
		if rootHex != hex.EncodeToString(r[:]) || Root() != rootHex {
			t.Fatalf("root %s, a tree built apart says %x", rootHex, r)
		}
		if j := Render("msg/" + u32(nonce)); !strings.Contains(j, "\"id\":\""+hex.EncodeToString(id[:])+"\"") || !strings.Contains(j, rawHex) {
			t.Fatalf("render: %s", j)
		}
	}
	if r, _ := Message(Count()); r != "" {
		t.Fatalf("a message past the count")
	}
	if Render("msg/"+u32(Count())) != "null" || !strings.HasPrefix(Render("msgs/0"), "[{\"nonce\":0,") {
		t.Fatalf("render pages: %s", Render("msgs/0"))
	}
	testing.SetRealm(testing.NewUserRealm(stranger))
	Reemit(cross(cur), n0)
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "no message with nonce", func() { Reemit(cross(cur), Count()) })
}

// TestSetVerifier: a swap of verifier waits 7 days, then anyone applies it — the governor's decision (M1k) queues
// as a proposal's does, and a cancel is a decision like any other: GovDAO's is refused until the yield.
func TestSetVerifier(cur realm, t *testing.T) {
	asMember(cur)
	aborts(t, "no verifier registered at", func() { NewSetVerifierRequest(cross(cur), ethTest, namespace+"nobody") })
	asMember(cur)
	aborts(t, "own domain", func() { NewSetVerifierRequest(cross(cur), DomainTest, testVerifier) })
	asGovernor(cur)
	govern(cur, NewSetVerifierRequest(cross(cur), ethTest, testVerifier))
	d := ledger.Get(2)
	if d == nil || d.Kind != "set verifier" || !d.Queued() || d.Due != d.Recorded+decisions.Delay || d.Note != "queued 7 days" {
		t.Fatalf("decision 2: %+v", d)
	}
	// the description says what a member reads the verifier's source for (E5-RL4): a panic in its Verify is the
	// verifier realm's own, crosses a realm boundary and aborts the transaction as written — this realm cannot quote it
	if want := "set verifier for origin " + u32(ethTest) + " to " + testVerifier + " (read before the vote: its Verify refuses by a returned error, never a panic, which would reach the block's results as written)"; d.Text != want {
		t.Fatalf("decision 2's description:\n%s\nnot\n%s", d.Text, want)
	}
	if ActiveVerifier(ethTest) != launchVerifier {
		t.Fatalf("applied early")
	}
	asMember(cur)
	refusedByDAO(cur, t, "cancel", NewCancelRequest(cross(cur), 2))
	if !d.Pending() {
		t.Fatalf("GovDAO cancelled before the yield")
	}
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "is due at", func() { Execute(cross(cur), 2) })
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), 2)
	if ActiveVerifier(ethTest) != testVerifier || d.State() != "applied" {
		t.Fatalf("not applied: %s %s", ActiveVerifier(ethTest), d.State())
	}
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "applied, not pending", func() { Execute(cross(cur), 2) })
	asMember(cur)
	aborts(t, "is the active verifier", func() { NewSetVerifierRequest(cross(cur), ethTest, testVerifier) })
}

// TestVerifierRealms (E4-RL5): the launch verifier, and the verifier a decision activated (decision 2, from its
// Execute on), are realms of the bridge for the routes (IsVerifier); an address that never was an active verifier
// is not.
func TestVerifierRealms(cur realm, t *testing.T) {
	if !IsVerifier(chain.PackageAddress(launchVerifier)) || !IsVerifier(chain.PackageAddress(testVerifier)) {
		t.Fatalf("the launch verifier (%v) and the one decision 2 activated (%v) are verifiers", IsVerifier(chain.PackageAddress(launchVerifier)), IsVerifier(chain.PackageAddress(testVerifier)))
	}
	if IsVerifier(stranger) || IsVerifier(chain.PackageAddress(namespace+"nobody")) || IsVerifier("") {
		t.Fatalf("an address that never was an active verifier is one")
	}
}

// TestProcess: the recipient takes a message signed by 2 of the 3 signers; everything else is refused.
func TestProcess(cur realm, t *testing.T) {
	meta, msg := unhex(inHelloMeta), unhex(inHelloMsg)
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "only the message's recipient", func() { Process(cross(cur), meta, msg) })
	testing.SetRealm(testing.NewUserRealm(recipient))
	aborts(t, "is for domain 1198419788, not this one (1198419796)", func() {
		Process(cross(cur), unhex(inMainnetMeta), unhex(inMainnetMsg))
	})
	testing.SetRealm(testing.NewUserRealm(recipient))
	origin, sender, body := Process(cross(cur), meta, msg)
	var five [32]byte // the sender, an Ethereum address left-padded: 0x5e5e…5e
	for i := 12; i < 32; i++ {
		five[i] = 0x5e
	}
	if origin != ethTest || sender != five || string(body) != "hello, gno" {
		t.Fatalf("process returned %d %x %q", origin, sender, body)
	}
	id := hl.ID(msg)
	if Delivered(hex.EncodeToString(id[:])) <= 0 || Delivered("0x"+hex.EncodeToString(id[:])) <= 0 || Render("delivered/"+hex.EncodeToString(id[:])) == "0" {
		t.Fatalf("not marked delivered")
	}
	testing.SetRealm(testing.NewUserRealm(recipient))
	aborts(t, "already delivered", func() { Process(cross(cur), meta, msg) })
	// one signature is short of the rule (the mark is set before the verifier is asked; on chain the abort
	// reverts it with the transaction)
	testing.SetRealm(testing.NewUserRealm(recipient))
	aborts(t, "fewer signatures than the threshold", func() { Process(cross(cur), unhex(inOneSigMeta), unhex(inOneSigMsg)) })
	// MarkDone: only a route
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "only a route realm", func() { MarkDone(cross(cur), "ab") })
	testing.SetRealm(testing.NewCodeRealm(launchRouteGNOT))
	aborts(t, "not a 32-byte message id", func() { MarkDone(cross(cur), "ab") })
	fresh := strings.Repeat("ab", 32)
	testing.SetRealm(testing.NewCodeRealm(launchRouteGNOT))
	MarkDone(cross(cur), fresh)
	if Delivered(fresh) <= 0 {
		t.Fatalf("MarkDone")
	}
	testing.SetRealm(testing.NewCodeRealm(launchRouteGNOT))
	aborts(t, "already delivered", func() { MarkDone(cross(cur), fresh) })
}

// TestRotateSigners: a removal the rule survives is at once; additions, rule changes and a removal the rule does
// not survive wait 7 days.
func TestRotateSigners(cur realm, t *testing.T) {
	ks := strings.Split(launchSigners, ",")
	asMember(cur)
	aborts(t, "nothing changes", func() { NewRotateSignersRequest(cross(cur), launchSigners, 2, "", "") })
	asMember(cur)
	aborts(t, "between 1 and the number", func() { NewRotateSignersRequest(cross(cur), launchSigners, 4, "", "") })
	asMember(cur)
	aborts(t, "name both groups or neither", func() { NewRotateSignersRequest(cross(cur), launchSigners, 0, ks[0], "") })
	asMember(cur)
	aborts(t, "takes no threshold", func() { NewRotateSignersRequest(cross(cur), launchSigners, 2, ks[0], ks[1]) })
	asMember(cur)
	aborts(t, "in both groups", func() { NewRotateSignersRequest(cross(cur), launchSigners, 0, ks[0], ks[0]+","+ks[1]) })
	asMember(cur)
	aborts(t, "every group member must be a signer", func() { NewRotateSignersRequest(cross(cur), ks[0]+","+ks[1], 0, ks[0], ks[2]) })
	asMember(cur)
	aborts(t, "named twice", func() { NewRotateSignersRequest(cross(cur), ks[0]+","+ks[0], 1, "", "") })
	// a pure removal the active verifier says the rule cannot survive is refused when it is made (K19), not left
	// to wait 7 days for nothing
	satisfiableNo = true
	asMember(cur)
	aborts(t, "the signers that remain cannot meet the rule", func() { NewRotateSignersRequest(cross(cur), ks[0]+","+ks[1], 2, "", "") })
	satisfiableNo = false

	// a removal: 2 of 3 to 2 of 2, at once
	asMember(cur)
	govern(cur, NewRotateSignersRequest(cross(cur), ks[0]+","+ks[1], 2, "", ""))
	if d := ledger.Get(3); d == nil || d.Kind != "rotate signers" || d.State() != "applied" || !strings.Contains(d.Note, "a removal the rule survives") {
		t.Fatalf("decision 3: %+v", d)
	}
	if SignerKeys() != ks[0]+","+ks[1] {
		t.Fatalf("signers %s", SignerKeys())
	}
	// a removal the rule does not survive: 2 of 2 to 1 key, queued
	asMember(cur)
	govern(cur, NewRotateSignersRequest(cross(cur), ks[0], 1, "", ""))
	if d := ledger.Get(4); d == nil || !d.Queued() {
		t.Fatalf("decision 4 should wait: %+v", d)
	}
	// adding the third back: queued, then applied after 7 days (decision 4 is still pending too)
	asMember(cur)
	govern(cur, NewRotateSignersRequest(cross(cur), launchSigners, 2, "", ""))
	if d := ledger.Get(5); d == nil || !d.Queued() || SignerKeys() != ks[0]+","+ks[1] || !strings.Contains(d.Note, "applies only while the signer set and rule stand") {
		t.Fatalf("decision 5: %+v", d)
	}
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), 5)
	if SignerKeys() != launchSigners {
		t.Fatalf("signers %s", SignerKeys())
	}
	// decision 4 (keys[0] alone, 1 of 1) is due too, but it was decided against the two-key set: the set changed
	// since, so it is refused and stays pending — a stale rotation never lands on a set it was not decided against
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "changed since decision 4 was decided", func() { Execute(cross(cur), 4) })
	if SignerKeys() != launchSigners || !ledger.Get(4).Pending() {
		t.Fatalf("a stale rotation applied: %s", SignerKeys())
	}
	// the two-group rule: queued, then live
	asMember(cur)
	govern(cur, NewRotateSignersRequest(cross(cur), launchSigners, 0, ks[0], ks[1]+","+ks[2]))
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), 6)
	_, rule := Signers()
	if !rule.TwoGroups() || rule.Threshold != 0 || len(rule.GroupA) != 1 || len(rule.GroupB) != 2 || !strings.Contains(rule.String(), "one of group A") {
		t.Fatalf("rule %+v", rule)
	}
	if !strings.Contains(Render("json"), "\"groupA\":[\""+ks[0]+"\"]") {
		t.Fatalf("json: %s", Render("json"))
	}
	// under the two-group rule a pure removal is at once when the live rule pruned of the removed keys is the rule
	// proposed: removing the partner (k0, group A's only member) empties a group, so it is never at once — the
	// proposal must name another rule, and waits
	asMember(cur)
	govern(cur, NewRotateSignersRequest(cross(cur), ks[1]+","+ks[2], 0, ks[1], ks[2]))
	partner := ledger.Last()
	if !ledger.Get(partner).Queued() || SignerKeys() != launchSigners {
		t.Fatalf("removing the partner applied at once: %+v", ledger.Get(partner))
	}
	asMember(cur)
	govern(cur, NewCancelRequest(cross(cur), partner))
	// k2 leaves group B: A={k0}, B={k1} is the live rule pruned of k2, and the verifier still finds one of each
	asMember(cur)
	govern(cur, NewRotateSignersRequest(cross(cur), ks[0]+","+ks[1], 0, ks[0], ks[1]))
	d := ledger.Get(ledger.Last())
	if d.State() != "applied" || !strings.Contains(d.Note, "a removal the rule survives") || SignerKeys() != ks[0]+","+ks[1] {
		t.Fatalf("a two-group removal waited: %+v signers %s", d, SignerKeys())
	}
	_, rule = Signers()
	if !rule.TwoGroups() || len(rule.GroupA) != 1 || len(rule.GroupB) != 1 || rule.GroupB[0] != parseKey(ks[1]) {
		t.Fatalf("rule after the removal %+v", rule)
	}
	// back to 2 of 3 for the tests after this one: an addition and a rule change, queued, then applied
	asMember(cur)
	govern(cur, NewRotateSignersRequest(cross(cur), launchSigners, 2, "", ""))
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), ledger.Last())
	if SignerKeys() != launchSigners {
		t.Fatalf("signers %s", SignerKeys())
	}
}

// TestYield (M1k): the governor's last decisions — a replacement of seats queued as a proposal's would be, its
// cancel and an unpause at once — then the yield: refused for a stranger and a code realm, taken once for the
// governor (the height recorded, the page and the json saying GovDAO governs since), refused twice; after it
// Govern is closed to the governor itself and GovDAO's proposals decide, with the same clocks.
func TestYield(cur realm, t *testing.T) {
	w6, newWatch := Seat(6), testutils.TestAddress("hl-watch-6b")
	asGovernor(cur)
	govern(cur, NewRotateSeatsRequest(cross(cur), "6:"+string(newWatch)))
	queued := ledger.Last()
	if d := ledger.Get(queued); !d.Queued() || d.Due != d.Recorded+decisions.Delay || Seat(6) != w6 || !strings.Contains(d.Note, "applies only while the seats stand as they do now") {
		t.Fatalf("a replacement by the governor did not wait: %+v", d)
	}
	asGovernor(cur)
	govern(cur, NewCancelRequest(cross(cur), queued))
	if ledger.Get(queued).State() != "cancelled" || ledger.Get(ledger.Last()).Kind != "cancel" || ledger.Get(ledger.Last()).Note != "at once: cancelled" {
		t.Fatalf("the governor's cancel: %s", ledger.Get(queued).State())
	}
	testing.SetRealm(testing.NewUserRealm(w6))
	SetFlag(cross(cur), launchRouteBUBBLE, FnSends, "")
	e0, _ := PayoutsEpoch(launchRouteBUBBLE)
	asGovernor(cur)
	govern(cur, NewUnpauseRequest(cross(cur), launchRouteBUBBLE, "sends", 0))
	if d := ledger.Get(ledger.Last()); d.Kind != "unpause" || d.State() != "applied" || !strings.Contains(d.Note, "1 flags cleared") || Paused(launchRouteBUBBLE, FnSends) {
		t.Fatalf("the governor's unpause: %+v", d)
	}
	if e, _ := PayoutsEpoch(launchRouteBUBBLE); e != e0 {
		t.Fatalf("a sends unpause moved the epoch")
	}
	// the yield: the governor alone, once
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "hlmailbox: only the governor "+string(launchGovernor)+" yields, not "+string(stranger), func() { Yield(cross(cur)) })
	testing.SetRealm(testing.NewCodeRealm(launchRouteGNOT))
	aborts(t, "only the governor "+string(launchGovernor)+" yields, not "+launchRouteGNOT, func() { Yield(cross(cur)) })
	if Yielded() != 0 {
		t.Fatalf("a stranger yielded")
	}
	testing.SkipHeights(1)
	asGovernor(cur)
	Yield(cross(cur))
	h := runtime.ChainHeight()
	if Yielded() != h || h == 0 || Governor() != launchGovernor {
		t.Fatalf("yielded at %d, height %d", Yielded(), h)
	}
	asGovernor(cur)
	aborts(t, "hlmailbox: the governor yielded at height "+strconv.FormatInt(h, 10)+" already", func() { Yield(cross(cur)) })
	line := "- governor: gno.land/r/gov/dao since height " + strconv.FormatInt(h, 10) + " (" + string(launchGovernor) + " yielded)\n"
	if !strings.Contains(Render(""), line) || strings.Contains(Render(""), "(until the yield)") || !strings.Contains(Render("json"), "\"governor\":\""+string(launchGovernor)+"\",\"yielded\":"+strconv.FormatInt(h, 10)+",") {
		t.Fatalf("page after the yield:\n%s\n%s", Render(""), Render("json"))
	}
	// Govern is closed, to the governor too; a GovDAO proposal decides now, on the same clocks
	n := ledger.Last()
	asGovernor(cur)
	req := NewRotateSeatsRequest(cross(cur), "6:"+string(newWatch))
	aborts(t, "hlmailbox: the governor yielded at height "+strconv.FormatInt(h, 10)+", gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never Govern", func() { govern(cur, req) })
	if ledger.Last() != n {
		t.Fatalf("Govern recorded after the yield")
	}
	asMember(cur)
	pass(cur, NewRotateSeatsRequest(cross(cur), "6:"+string(newWatch)))
	queued = ledger.Last()
	if d := ledger.Get(queued); !d.Queued() || d.Due != d.Recorded+decisions.Delay || Seat(6) != w6 {
		t.Fatalf("GovDAO's replacement after the yield: %+v", d)
	}
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), queued))
	if ledger.Get(queued).State() != "cancelled" || Seat(6) != w6 {
		t.Fatalf("GovDAO's cancel after the yield")
	}
}

// TestFlags: seats set and clear their own flags; a guardian's payouts flag names ids; a watch key's never does.
func TestFlags(cur realm, t *testing.T) {
	g2, w5 := Seat(2), Seat(5)
	id1, id2 := strings.Repeat("11", 32), strings.Repeat("22", 32)
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "not a guardian or a watch key", func() { SetFlag(cross(cur), launchRouteGNOT, FnSends, "") })
	testing.SetRealm(testing.NewUserRealm(g2))
	aborts(t, "no route", func() { SetFlag(cross(cur), pkgPath, FnSends, "") })
	testing.SetRealm(testing.NewUserRealm(g2))
	aborts(t, "sends, credits or payouts", func() { SetFlag(cross(cur), launchRouteGNOT, "claims", "") })
	testing.SetRealm(testing.NewUserRealm(g2))
	aborts(t, "only a guardian's payouts flag names ids", func() { SetFlag(cross(cur), launchRouteGNOT, FnSends, id1) })
	testing.SetRealm(testing.NewUserRealm(w5))
	aborts(t, "only a guardian's payouts flag names ids", func() { SetFlag(cross(cur), launchRouteGNOT, FnPayouts, id1) })
	testing.SetRealm(testing.NewUserRealm(g2))
	aborts(t, "not a 32-byte message id", func() { SetFlag(cross(cur), launchRouteGNOT, FnPayouts, "abcd") })

	testing.SetRealm(testing.NewUserRealm(g2))
	SetFlag(cross(cur), launchRouteGNOT, FnPayouts, id1+",0x"+strings.ToUpper(id2)+","+id1)
	testing.SetRealm(testing.NewUserRealm(w5))
	SetFlag(cross(cur), launchRouteGNOT, FnCredits, "")
	testing.SetRealm(testing.NewUserRealm(w5))
	SetFlag(cross(cur), launchRouteBUBBLE, FnPayouts, "")
	if !Paused(launchRouteGNOT, FnPayouts) || !Paused(launchRouteGNOT, FnCredits) || Paused(launchRouteGNOT, FnSends) || !Paused(launchRouteBUBBLE, FnPayouts) {
		t.Fatalf("paused: %v %v %v", Paused(launchRouteGNOT, FnPayouts), Paused(launchRouteGNOT, FnCredits), Paused(launchRouteGNOT, FnSends))
	}
	if !Flagged(launchRouteGNOT, id1, 0, true) || !Flagged(launchRouteGNOT, "0x"+id2, 0, false) || Flagged(launchRouteGNOT, strings.Repeat("33", 32), 0, true) || Flagged(launchRouteBUBBLE, id1, 0, true) {
		t.Fatalf("flagged")
	}
	fs := Flags(launchRouteGNOT)
	if len(fs) != 2 || !strings.Contains(fs[1], "payouts by seat 2 ("+string(g2)+")") || !strings.Contains(fs[1], "naming "+id1+","+id2) || !strings.HasPrefix(fs[0], "credits by seat 5") {
		t.Fatalf("flags %v", fs)
	}
	if !strings.Contains(Render(""), "**paused** payouts by seat 2") || !strings.Contains(Render("json"), "\"payouts\":true,\"flags\":[\"credits by seat 5") {
		t.Fatalf("page: %s", Render("json"))
	}
	// own clears only; a clear by the key resets no meter
	testing.SetRealm(testing.NewUserRealm(w5))
	aborts(t, "no such flag of yours", func() { ClearFlag(cross(cur), launchRouteGNOT, FnPayouts) })
	testing.SetRealm(testing.NewUserRealm(w5))
	ClearFlag(cross(cur), launchRouteBUBBLE, FnPayouts)
	if Paused(launchRouteBUBBLE, FnPayouts) {
		t.Fatalf("not cleared")
	}
	if e, _ := PayoutsEpoch(launchRouteBUBBLE); e != 0 {
		t.Fatalf("an own clear reset the meter")
	}
	// a route may clear every flag of one function on itself (the void's andUnpause), and that resets the meter
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "only a route realm", func() { ClearFlags(cross(cur), FnPayouts) })
	testing.SetRealm(testing.NewCodeRealm(launchRouteBUBBLE))
	if ClearFlags(cross(cur), FnPayouts) {
		t.Fatalf("cleared a flag that was not there")
	}
	testing.SetRealm(testing.NewCodeRealm(launchRouteGNOT))
	if !ClearFlags(cross(cur), FnPayouts) || Paused(launchRouteGNOT, FnPayouts) || !Paused(launchRouteGNOT, FnCredits) {
		t.Fatalf("ClearFlags")
	}
	if e, at := PayoutsEpoch(launchRouteGNOT); e != 1 || at == 0 {
		t.Fatalf("epoch %d %d", e, at)
	}
	// a route registers its successor
	testing.SetRealm(testing.NewCodeRealm(launchRouteGNOT))
	aborts(t, "a route is a realm under gno.land/r/", func() { AddRoute(cross(cur), "gno.land/p/x") })
	// a sub-identity's path (host#subpath) is no deployed realm, so it is never a route (X1's sibling)
	testing.SetRealm(testing.NewCodeRealm(launchRouteGNOT))
	aborts(t, "a route is a realm under gno.land/r/", func() { AddRoute(cross(cur), namespace+"hlgnot2#1") })
	testing.SetRealm(testing.NewCodeRealm(launchRouteGNOT))
	AddRoute(cross(cur), namespace+"hlgnot2")
	if !IsRoute(namespace+"hlgnot2") || len(Routes()) != 3 {
		t.Fatalf("routes %v", Routes())
	}
	// a guardian's payouts flag may name a range of credits instead of a list — the claims credited at heights
	// 10-20, or the verifier's credits since a height — and the at-once test covers a claim by its credit height;
	// a watch key's flag names neither
	id3 := strings.Repeat("33", 32)
	testing.SetRealm(testing.NewUserRealm(g2))
	aborts(t, "lower height to the higher", func() { SetFlag(cross(cur), launchRouteBUBBLE, FnPayouts, "heights:20-10") })
	testing.SetRealm(testing.NewUserRealm(g2))
	aborts(t, "not a block height", func() { SetFlag(cross(cur), launchRouteBUBBLE, FnPayouts, "since:x") })
	testing.SetRealm(testing.NewUserRealm(g2))
	aborts(t, "heights:<from>-<to>", func() { SetFlag(cross(cur), launchRouteBUBBLE, FnPayouts, "heights:7") })
	testing.SetRealm(testing.NewUserRealm(w5))
	aborts(t, "only a guardian's payouts flag names ids, or a range", func() { SetFlag(cross(cur), launchRouteBUBBLE, FnPayouts, "since:1") })
	testing.SetRealm(testing.NewUserRealm(g2))
	SetFlag(cross(cur), launchRouteBUBBLE, FnPayouts, "heights:10-20")
	if !Flagged(launchRouteBUBBLE, id3, 10, false) || !Flagged(launchRouteBUBBLE, id3, 20, true) || Flagged(launchRouteBUBBLE, id3, 21, true) || Flagged(launchRouteBUBBLE, id3, 9, true) || Flagged(launchRouteGNOT, id3, 15, true) {
		t.Fatalf("a height range's cover")
	}
	if fs := Flags(launchRouteBUBBLE); len(fs) != 1 || !strings.Contains(fs[0], "payouts by seat 2 ("+string(g2)+")") || !strings.HasSuffix(fs[0], " naming claims credited at heights 10-20") {
		t.Fatalf("flags %v", fs)
	}
	// re-asserted as a since-range: the verifier's credits from height 100 on, a hand credit never
	testing.SetRealm(testing.NewUserRealm(g2))
	SetFlag(cross(cur), launchRouteBUBBLE, FnPayouts, "since:100")
	if !Flagged(launchRouteBUBBLE, id3, 100, true) || !Flagged(launchRouteBUBBLE, id3, 1_000_000, true) || Flagged(launchRouteBUBBLE, id3, 100, false) || Flagged(launchRouteBUBBLE, id3, 99, true) || Flagged(launchRouteBUBBLE, id3, 15, true) {
		t.Fatalf("a since range's cover")
	}
	if !strings.Contains(Render("json"), "naming the verifier's credits since height 100") {
		t.Fatalf("json: %s", Render("json"))
	}
	testing.SetRealm(testing.NewUserRealm(g2))
	ClearFlag(cross(cur), launchRouteBUBBLE, FnPayouts)
	if Paused(launchRouteBUBBLE, FnPayouts) || Flagged(launchRouteBUBBLE, id3, 100, true) {
		t.Fatalf("not cleared")
	}
}

// TestUnpause: clears the named functions' flags at once; refused when nothing stands; spent when nothing is left
// to clear at execution; may vacate a seat.
func TestUnpause(cur realm, t *testing.T) {
	g2, w5 := Seat(2), Seat(5)
	// credits by seat 5 stands on hlgnot from the test before
	asMember(cur)
	aborts(t, "nothing to clear", func() { NewUnpauseRequest(cross(cur), launchRouteGNOT, "sends,payouts", 0) })
	asMember(cur)
	aborts(t, "named twice", func() { NewUnpauseRequest(cross(cur), launchRouteGNOT, "credits,credits", 0) })
	asMember(cur)
	aborts(t, "no route", func() { NewUnpauseRequest(cross(cur), pkgPath, "credits", 0) })
	asMember(cur)
	aborts(t, "must be 1 to 6", func() { NewUnpauseRequest(cross(cur), launchRouteGNOT, "credits", 7) })
	// an unpause of credits that finds its flag gone at execution is spent
	asMember(cur)
	req := NewUnpauseRequest(cross(cur), launchRouteGNOT, "credits", 0)
	testing.SetRealm(testing.NewUserRealm(w5))
	ClearFlag(cross(cur), launchRouteGNOT, FnCredits)
	pass(cur, req)
	d := ledger.Get(ledger.Last())
	if d.Kind != "unpause" || d.State() != "spent" || d.Note != "spent: nothing to clear" {
		t.Fatalf("decision %d: %+v", d.ID, d)
	}
	// payouts flags by a guardian and a watch key: the unpause clears both and restarts the meters
	e0, _ := PayoutsEpoch(launchRouteGNOT)
	testing.SetRealm(testing.NewUserRealm(g2))
	SetFlag(cross(cur), launchRouteGNOT, FnPayouts, "")
	testing.SetRealm(testing.NewUserRealm(w5))
	SetFlag(cross(cur), launchRouteGNOT, FnPayouts, "")
	testing.SetRealm(testing.NewUserRealm(w5))
	SetFlag(cross(cur), launchRouteGNOT, FnSends, "")
	asMember(cur)
	pass(cur, NewUnpauseRequest(cross(cur), launchRouteGNOT, "payouts,credits", 0))
	d = ledger.Get(ledger.Last())
	if d.State() != "applied" || !strings.Contains(d.Note, "2 flags cleared, meters restart") || Paused(launchRouteGNOT, FnPayouts) || !Paused(launchRouteGNOT, FnSends) {
		t.Fatalf("decision %d: %+v", d.ID, d)
	}
	if e, _ := PayoutsEpoch(launchRouteGNOT); e != e0+1 {
		t.Fatalf("epoch %d", e)
	}
	// an unpause of sends resets no meter, and vacates seat 5 in the same stroke
	asMember(cur)
	pass(cur, NewUnpauseRequest(cross(cur), launchRouteGNOT, "sends", 5))
	d = ledger.Get(ledger.Last())
	if d.State() != "applied" || !strings.Contains(d.Note, "1 flags cleared, seat 5 vacated") || Seat(5) != "" || SeatOf(w5) != 0 || Paused(launchRouteGNOT, FnSends) {
		t.Fatalf("decision %d: %+v seat5 %s", d.ID, d, Seat(5))
	}
	if e, _ := PayoutsEpoch(launchRouteGNOT); e != e0+1 {
		t.Fatalf("a sends unpause moved the epoch")
	}
}

// TestRotateSeats: removals at once (flags go with the key, a payouts flag restarting the meters); replacements wait.
func TestRotateSeats(cur realm, t *testing.T) {
	g2, g3 := Seat(2), Seat(3)
	newKey := testutils.TestAddress("hl-new-guardian")
	newWatch := testutils.TestAddress("hl-new-watch")
	asMember(cur)
	aborts(t, "no changes", func() { NewRotateSeatsRequest(cross(cur), " ") })
	asMember(cur)
	aborts(t, "seat:address", func() { NewRotateSeatsRequest(cross(cur), "2") })
	asMember(cur)
	aborts(t, "must be 1 to 6", func() { NewRotateSeatsRequest(cross(cur), "7:"+string(newKey)) })
	asMember(cur)
	aborts(t, "named twice", func() { NewRotateSeatsRequest(cross(cur), "2:,2:"+string(newKey)) })
	asMember(cur)
	aborts(t, "not an address", func() { NewRotateSeatsRequest(cross(cur), "2:nobody") })
	asMember(cur)
	aborts(t, "is empty already", func() { NewRotateSeatsRequest(cross(cur), "5:") })
	asMember(cur)
	aborts(t, "holds "+string(g2)+" already", func() { NewRotateSeatsRequest(cross(cur), "2:"+string(g2)) })
	asMember(cur)
	aborts(t, "holds seat 3 already", func() { NewRotateSeatsRequest(cross(cur), "2:"+string(g3)) })
	asMember(cur)
	aborts(t, "named for seats 2 and 5", func() { NewRotateSeatsRequest(cross(cur), "2:"+string(newKey)+",5:"+string(newKey)) })
	asMember(cur)
	aborts(t, "two guardians at most", func() { NewRotateSeatsRequest(cross(cur), "1:,2:,3:") })

	// seat 2 flags payouts, then is removed: at once, its flag gone, the meters restarted
	testing.SetRealm(testing.NewUserRealm(g2))
	SetFlag(cross(cur), launchRouteBUBBLE, FnPayouts, "")
	e0, _ := PayoutsEpoch(launchRouteBUBBLE)
	asMember(cur)
	pass(cur, NewRotateSeatsRequest(cross(cur), "2:-,3:"))
	d := ledger.Get(ledger.Last())
	if d.Kind != "rotate seats" || d.State() != "applied" || !strings.Contains(d.Note, "removals") || Seat(2) != "" || Seat(3) != "" {
		t.Fatalf("decision %d: %+v", d.ID, d)
	}
	if Paused(launchRouteBUBBLE, FnPayouts) {
		t.Fatalf("the removed key's flag stands")
	}
	if e, _ := PayoutsEpoch(launchRouteBUBBLE); e != e0+1 {
		t.Fatalf("epoch %d", e)
	}
	// a replacement and a removal together wait 7 days
	asMember(cur)
	pass(cur, NewRotateSeatsRequest(cross(cur), "2:"+string(newKey)+",5:"+string(newWatch)+",4:"))
	d = ledger.Get(ledger.Last())
	if !d.Queued() || Seat(2) != "" {
		t.Fatalf("decision %d: %+v", d.ID, d)
	}
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), d.ID)
	if Seat(2) != newKey || Seat(5) != newWatch || Seat(4) != "" || SeatOf(newKey) != 2 {
		t.Fatalf("seats %s %s %s", Seat(2), Seat(5), Seat(4))
	}
	// the new guardian can flag; the old one cannot
	testing.SetRealm(testing.NewUserRealm(g2))
	aborts(t, "not a guardian or a watch key", func() { SetFlag(cross(cur), launchRouteGNOT, FnSends, "") })
	testing.SetRealm(testing.NewUserRealm(newKey))
	SetFlag(cross(cur), launchRouteGNOT, FnSends, "")
	testing.SetRealm(testing.NewUserRealm(newKey))
	ClearFlag(cross(cur), launchRouteGNOT, FnSends)
	// a queued replacement is decided against the seats as they stand: a removal landing in between (seat 6
	// vacated at once) makes it stale, so it is refused on its 7th day and proposed again
	newKey2 := testutils.TestAddress("hl-new-guardian-2")
	asMember(cur)
	pass(cur, NewRotateSeatsRequest(cross(cur), "3:"+string(newKey2)))
	stale := ledger.Last()
	if !ledger.Get(stale).Queued() || !strings.Contains(ledger.Get(stale).Note, "applies only while the seats stand") {
		t.Fatalf("decision %d: %+v", stale, ledger.Get(stale))
	}
	asMember(cur)
	pass(cur, NewRotateSeatsRequest(cross(cur), "6:"))
	if Seat(6) != "" {
		t.Fatalf("seat 6 stands")
	}
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "the seats changed since decision "+strconv.FormatInt(stale, 10)+" was decided", func() { Execute(cross(cur), stale) })
	if Seat(3) != "" || !ledger.Get(stale).Pending() {
		t.Fatalf("a stale seat change applied: seat 3 %s", Seat(3))
	}
	asMember(cur)
	pass(cur, NewRotateSeatsRequest(cross(cur), "3:"+string(newKey2)))
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), ledger.Last())
	if Seat(3) != newKey2 || SeatOf(newKey2) != 3 {
		t.Fatalf("seat 3 %s", Seat(3))
	}
}

// TestCancelAndDeadline: GovDAO cancels a queued change at once; a proposal executed past 14 days is denied.
func TestCancelAndDeadline(cur realm, t *testing.T) {
	ks := strings.Split(launchSigners, ",")
	asMember(cur)
	pass(cur, NewRotateSignersRequest(cross(cur), launchSigners, 3, "", ""))
	queued := ledger.Last()
	if !ledger.Get(queued).Queued() {
		t.Fatalf("not queued")
	}
	asMember(cur)
	aborts(t, "is not pending", func() { NewCancelRequest(cross(cur), 1) })
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), queued))
	if ledger.Get(queued).State() != "cancelled" || ledger.Get(ledger.Last()).Kind != "cancel" {
		t.Fatalf("cancel: %s", ledger.Get(queued).State())
	}
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "cancelled, not pending", func() { Execute(cross(cur), queued) })
	_, rule := Signers()
	if rule.Threshold != 2 {
		t.Fatalf("a cancelled change applied")
	}
	// the deadline: made, then executed 15 days later — denied for good, no decision recorded
	asMember(cur)
	req := NewRotateSignersRequest(cross(cur), ks[0]+","+ks[1], 1, "", "")
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	last := ledger.Last()
	skipDays(15)
	testing.SetRealm(testing.NewUserRealm(member))
	if dao.ExecuteOrRejectProposal(cross(cur), pid) {
		t.Fatalf("executed past the deadline")
	}
	if ledger.Last() != last || SignerKeys() != launchSigners {
		t.Fatalf("a denied proposal left a decision")
	}
	// the DAO's page escapes markdown ("14\-day"), so the words around the hyphen are what is checked
	if page := dao.Render(cross(cur), strconv.FormatInt(int64(pid), 10)); !strings.Contains(page, "HAS BEEN DENIED") || !strings.Contains(page, "execution failed: the proposal is past its 14") || !strings.Contains(page, "day deadline") {
		t.Fatalf("the denial does not name the deadline:\n%s", page)
	}
	p := Render("decisions")
	for _, want := range []string{"### rotate signers", "### cancel", "### unpause", "### rotate seats", "### set verifier", "cancelled:"} {
		if !strings.Contains(p, want) {
			t.Fatalf("decisions page lacks %q:\n%s", want, p)
		}
	}
	if !strings.HasPrefix(Render("decisions.json"), "[{\"id\":1,\"kind\":\"launch\"") {
		t.Fatalf("decisions.json: %s", Render("decisions.json"))
	}
}

// TestUnpauseSeatGuard (R17): an unpause's seat removal names the key the seat holds when the proposal is made; a
// replacement that lands before it executes stays — the flags are cleared, the seat is skipped and the note says so.
func TestUnpauseSeatGuard(cur realm, t *testing.T) {
	g2, w5 := Seat(2), Seat(5)
	if g2 == "" || w5 == "" {
		t.Fatalf("seats 2 and 5 should be held: %s %s", g2, w5)
	}
	testing.SetRealm(testing.NewUserRealm(w5))
	SetFlag(cross(cur), launchRouteGNOT, FnSends, "")
	asMember(cur)
	req := NewUnpauseRequest(cross(cur), launchRouteGNOT, "sends", 2)
	if !strings.Contains(req.Description(), "unpause sends on "+launchRouteGNOT+", removing seat 2 ("+string(g2)+")") {
		t.Fatalf("description: %s", req.Description())
	}
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	another := testutils.TestAddress("hl-another-guardian")
	asMember(cur)
	pass(cur, NewRotateSeatsRequest(cross(cur), "2:"+string(another)))
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), ledger.Last())
	if Seat(2) != another {
		t.Fatalf("seat 2 %s", Seat(2))
	}
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		t.Fatalf("the unpause did not execute")
	}
	d := ledger.Get(ledger.Last())
	if d.Kind != "unpause" || d.State() != "applied" || d.Note != "at once: 1 flags cleared, seat 2 skipped (its occupant changed since the proposal was made)" || Seat(2) != another || SeatOf(another) != 2 || Paused(launchRouteGNOT, FnSends) {
		t.Fatalf("decision %d: %+v seat 2 %s", d.ID, d, Seat(2))
	}
	// the same with nothing left to clear: spent, the seat still skipped
	testing.SetRealm(testing.NewUserRealm(w5))
	SetFlag(cross(cur), launchRouteGNOT, FnSends, "")
	asMember(cur)
	req = NewUnpauseRequest(cross(cur), launchRouteGNOT, "sends", 2)
	testing.SetRealm(testing.NewUserRealm(w5))
	ClearFlag(cross(cur), launchRouteGNOT, FnSends)
	asMember(cur)
	pass(cur, NewRotateSeatsRequest(cross(cur), "2:"))
	pass(cur, req)
	d = ledger.Get(ledger.Last())
	if d.State() != "spent" || d.Note != "spent: nothing to clear, seat 2 skipped (its occupant changed since the proposal was made)" || Seat(2) != "" {
		t.Fatalf("decision %d: %+v", d.ID, d)
	}
}

// TestRotateSeatsRemovalGuard (R21): a rotate-seats removal names the key the seat holds when the proposal is made;
// a replacement that lands before it executes stays — the seat is skipped and the note says so (spent when every
// removal is skipped; the other removals apply); the queued path is unchanged (a stale one is refused whole).
func TestRotateSeatsRemovalGuard(cur realm, t *testing.T) {
	g3, w5 := Seat(3), Seat(5)
	if g3 == "" || w5 == "" {
		t.Fatalf("seats 3 and 5 should be held: %s %s", g3, w5)
	}
	asMember(cur)
	req := NewRotateSeatsRequest(cross(cur), "3:")
	if !strings.Contains(req.Description(), "rotate seats: remove seat 3 ("+string(g3)+")") {
		t.Fatalf("description: %s", req.Description())
	}
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	another := testutils.TestAddress("hl-guardian-three")
	asMember(cur)
	pass(cur, NewRotateSeatsRequest(cross(cur), "3:"+string(another)))
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), ledger.Last())
	if Seat(3) != another {
		t.Fatalf("seat 3 %s", Seat(3))
	}
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		t.Fatalf("the removal did not execute")
	}
	d := ledger.Get(ledger.Last())
	if d.Kind != "rotate seats" || d.State() != "spent" || d.Note != "spent: nothing to remove, seats 3 skipped (the occupant changed since the proposal was made)" || Seat(3) != another || SeatOf(another) != 3 {
		t.Fatalf("decision %d: %+v seat 3 %s", d.ID, d, Seat(3))
	}
	// two removals, one seat replaced in between: the other goes, the note names both
	asMember(cur)
	req = NewRotateSeatsRequest(cross(cur), "3:,5:")
	testing.SetRealm(testing.NewUserRealm(member))
	pid = dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	anotherWatch := testutils.TestAddress("hl-watch-five")
	asMember(cur)
	pass(cur, NewRotateSeatsRequest(cross(cur), "5:"+string(anotherWatch)))
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), ledger.Last())
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		t.Fatalf("the removals did not execute")
	}
	d = ledger.Get(ledger.Last())
	if d.State() != "applied" || d.Note != "at once: removals: seats 3 vacated, seats 5 skipped (the occupant changed since the proposal was made)" || Seat(3) != "" || Seat(5) != anotherWatch || SeatOf(another) != 0 {
		t.Fatalf("decision %d: %+v seats %s %s", d.ID, d, Seat(3), Seat(5))
	}
}

// writingVerifier answers yes to every Satisfiable and writes into the slice it is handed — what a verifier of a
// /p/-declared type with no object to anchor on could do under this realm's own authority (the no-anchor case);
// declared here, in the mailbox's own package, its write lands exactly as such a verifier's would.
type writingVerifier struct{}

var (
	writingPath = namespace + "hlwritingverifier"
	evilKey     = parseKey(strings.Repeat("ee", 32))
)

func (writingVerifier) Verify(origin uint32, metadata, message []byte) error { return nil }

func (writingVerifier) Satisfiable(signers []Key) bool {
	if len(signers) > 0 {
		signers[0] = evilKey
	}
	return true
}

// TestSatisfiableCopy (X2): satisfiable hands every active verifier a copy of the key slice, so the verifier's
// write changes nothing in the mailbox — neither the slice the builder checks when a removal is made nor the
// keys its closure stores: the signers after a removal are the keys the proposal named.
func TestSatisfiableCopy(cur realm, t *testing.T) {
	ks := strings.Split(launchSigners, ",")
	if SignerKeys() != launchSigners {
		t.Fatalf("signers before: %s", SignerKeys())
	}
	testing.SetRealm(testing.NewCodeRealm(writingPath))
	RegisterVerifier(cross(cur), writingVerifier{})
	asMember(cur)
	pass(cur, NewSetVerifierRequest(cross(cur), ethTest, writingPath))
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), ledger.Last())
	if ActiveVerifier(ethTest) != writingPath {
		t.Fatalf("active %s", ActiveVerifier(ethTest))
	}
	// the direct call: the slice handed in comes back as it went
	in := parseKeys(launchSigners)
	if !satisfiable(in) || !sameKeys(in, parseKeys(launchSigners)) {
		t.Fatalf("the verifier wrote into the mailbox's slice: %s", keysHex(in))
	}
	// the governance path: a pure removal, at once, stores the keys the proposal named
	asMember(cur)
	pass(cur, NewRotateSignersRequest(cross(cur), ks[0]+","+ks[1], 2, "", ""))
	d := ledger.Get(ledger.Last())
	if d.Kind != "rotate signers" || d.State() != "applied" || !strings.Contains(d.Text, "rotate signers to "+ks[0]+","+ks[1]+" under") {
		t.Fatalf("decision %d: %+v", d.ID, d)
	}
	if SignerKeys() != ks[0]+","+ks[1] || indexOf(signerKeys, evilKey) >= 0 {
		t.Fatalf("signers after: %s", SignerKeys())
	}
}

func quoteAll(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// TestRegisteredListing (X1): json and the page name the first 20 registered paths and the count; verifiers/<from>
// pages them all, 20 at a time, in path order. 25 more realms register (on chain each is a package deployment).
func TestRegisteredListing(cur realm, t *testing.T) {
	for i := 10; i < 35; i++ {
		testing.SetRealm(testing.NewCodeRealm(namespace + "hlv" + strconv.Itoa(i)))
		RegisterVerifier(cross(cur), verifier2of3{})
	}
	n := verifiers.Size()
	var all []string
	verifiers.Iterate("", "", func(k string, _ any) bool {
		all = append(all, k)
		return false
	})
	if n != len(all) || n < 27 || n > 40 {
		t.Fatalf("registered %d", n)
	}
	j := Render("json")
	want := "\"registered\":" + quoteAll(all[:20]) + ",\"registeredCount\":" + strconv.Itoa(n) + ",\"seats\":["
	if !strings.Contains(j, want) {
		t.Fatalf("json lacks %s:\n%s", want, j)
	}
	if Render("verifiers/0") != quoteAll(all[:20]) || Render("verifiers/20") != quoteAll(all[20:]) || Render("verifiers/"+strconv.Itoa(n)) != "[]" || Render("verifiers/x") != "[]" {
		t.Fatalf("pages: %s / %s", Render("verifiers/20"), Render("verifiers/"+strconv.Itoa(n)))
	}
	p := Render("")
	if !strings.Contains(p, "- registered ("+strconv.Itoa(n)+"): `"+all[0]+"`, `") || !strings.Contains(p, "`"+all[19]+"`, … (the first 20; the rest at [verifiers/20](") || strings.Contains(p, "`"+all[20]+"`") {
		t.Fatalf("page: %s", p)
	}
}

// TestAnnounceSuccessor (R25): a realm under /r/ announces itself as a route's possible successor, once, with its
// own cur — a user and a sub-identity cannot — and the json and the page list the announced paths, bounded, with
// the count; successors/<from> pages them. The sub-identity is minted first (the harness's SetRealm rewrites this
// frame's cur, after which Sub refuses it).
func TestAnnounceSuccessor(cur realm, t *testing.T) {
	sub := cur.Sub("succ")
	succ := namespace + "hlgnot2"
	if SuccessorAnnounced(succ) || SuccessorFor(succ) != "" || !strings.Contains(Render("json"), "\"successors\":[],\"successorCount\":0,\"successorRoutes\":{}}") || !strings.Contains(Render(""), "- successors announced (0): none") {
		t.Fatalf("before: %s", Render("json"))
	}
	aborts(t, "not a sub-identity", func() { AnnounceSuccessor(cross(sub), SymbolGNOT) })
	if SuccessorAnnounced(pkgPath+"#succ") || successors.Size() != 0 {
		t.Fatalf("a sub-identity announced itself")
	}
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "only a realm under gno.land/r/", func() { AnnounceSuccessor(cross(cur), SymbolGNOT) })
	// the announcement names the route it succeeds (AQ5): GNOT or BUBBLE, nothing else
	testing.SetRealm(testing.NewCodeRealm(succ))
	aborts(t, "the route a successor announces for is GNOT or BUBBLE, not \"gnot\"", func() { AnnounceSuccessor(cross(cur), "gnot") })
	if IsAnnouncedSuccessor(chain.PackageAddress(succ)) {
		t.Fatalf("announced by address before it announced")
	}
	testing.SetRealm(testing.NewCodeRealm(succ))
	AnnounceSuccessor(cross(cur), SymbolGNOT)
	if !SuccessorAnnounced(succ) || SuccessorAnnounced(succ+"#1") || successors.Size() != 1 || SuccessorFor(succ) != "GNOT" || SuccessorFor(succ+"#1") != "" {
		t.Fatalf("not announced: %q", SuccessorFor(succ))
	}
	// by its address too (E3-RL3): what the routes ask before they pay anyone — never by a pkgpath written as an
	// address, another realm's address, a launch route's, or the empty one
	if !IsAnnouncedSuccessor(chain.PackageAddress(succ)) || IsAnnouncedSuccessor(address(succ)) || IsAnnouncedSuccessor(chain.PackageAddress(namespace+"nobody")) ||
		IsAnnouncedSuccessor(chain.PackageAddress(launchRouteGNOT)) || IsAnnouncedSuccessor("") {
		t.Fatalf("announced by address: %v", IsAnnouncedSuccessor(chain.PackageAddress(succ)))
	}
	testing.SetRealm(testing.NewCodeRealm(succ))
	aborts(t, "announced itself already", func() { AnnounceSuccessor(cross(cur), SymbolBUBBLE) })
	if SuccessorFor(succ) != "GNOT" {
		t.Fatalf("a second announcement changed the route: %q", SuccessorFor(succ))
	}
	// the token a route is for (R36): the launch paths' compiled symbols, an announced successor's announcement,
	// nothing for the rest — what r/hlgov's kind 6 compares with the token it names
	if RouteSymbol(launchRouteGNOT) != SymbolGNOT || RouteSymbol(launchRouteBUBBLE) != SymbolBUBBLE || RouteSymbol(succ) != SymbolGNOT || RouteSymbol(namespace+"nobody") != "" || RouteSymbol(pkgPath) != "" {
		t.Fatalf("route symbols: %q %q %q", RouteSymbol(launchRouteGNOT), RouteSymbol(launchRouteBUBBLE), RouteSymbol(succ))
	}
	if !strings.Contains(Render("json"), "\"successors\":[\""+succ+"\"],\"successorCount\":1,\"successorRoutes\":{\""+succ+"\":\"GNOT\"}}") || Render("successors/0") != "[\""+succ+"\"]" || Render("successors/1") != "[]" || Render("successors/x") != "[]" {
		t.Fatalf("json: %s", Render("json"))
	}
	if !strings.Contains(Render(""), "- successors announced (1): `"+succ+"` (GNOT) (a route's migrate names only one of them, announced for its own route)") {
		t.Fatalf("page: %s", Render(""))
	}
}

// TestRouteSuccession (AQ1): a route's migrate registers its successor and the mailbox lets that successor — and
// its successor in turn, along the chain of migrations — take a message addressed to the predecessor, since the
// burns name the predecessor as recipient until kind 6 enrolls the successor; a stranger, the other route and a
// route of another chain are refused, and the mark is the shared `done`, so the predecessor itself then finds the
// message delivered. hlgnot2 became hlgnot's successor in TestFlags; here hlbubble migrates to hlbubble2, which
// migrates to hlbubble3. The active verifier accepts anything (TestSatisfiableCopy's), so the messages are bare.
func TestRouteSuccession(cur realm, t *testing.T) {
	pred, succ, succ2 := launchRouteBUBBLE, namespace+"hlbubble2", namespace+"hlbubble3"
	g1, w5 := Seat(1), Seat(5)
	if g1 == "" || w5 == "" || !IsGuardianSeat(1) || IsGuardianSeat(5) {
		t.Fatalf("seats 1 and 5 should be held: %s %s", g1, w5)
	}
	// the migrating route's standing flags follow the lock to its successor (R27): a guardian's payouts flag naming
	// an id and a watch key's credits flag stand on hlbubble when its migrate registers hlbubble2, and stand there
	// too — the seat, the time, the ids — so the successor's Claim and Process find them; each copy is the seat's
	// own, cleared where it stands
	idF := strings.Repeat("f1", 32)
	testing.SetRealm(testing.NewUserRealm(g1))
	SetFlag(cross(cur), pred, FnPayouts, idF)
	testing.SetRealm(testing.NewUserRealm(w5))
	SetFlag(cross(cur), pred, FnCredits, "")
	testing.SetRealm(testing.NewCodeRealm(pred))
	AddRoute(cross(cur), succ)
	if !Paused(succ, FnPayouts) || !Paused(succ, FnCredits) || Paused(succ, FnSends) || !Flagged(succ, idF, 0, true) || Flagged(succ, strings.Repeat("f2", 32), 0, true) {
		t.Fatalf("the flags did not follow: payouts %v credits %v", Paused(succ, FnPayouts), Paused(succ, FnCredits))
	}
	if fs, ps := Flags(succ), Flags(pred); len(fs) != 2 || !strings.HasPrefix(fs[0], "credits by seat 5 ("+string(w5)+")") || !strings.Contains(fs[1], "payouts by seat 1 ("+string(g1)+")") || !strings.HasSuffix(fs[1], " naming "+idF) || fs[0] != ps[0] || fs[1] != ps[1] {
		t.Fatalf("flags %v / %v", fs, ps)
	}
	testing.SetRealm(testing.NewUserRealm(g1))
	ClearFlag(cross(cur), pred, FnPayouts)
	if !Paused(succ, FnPayouts) || Paused(pred, FnPayouts) {
		t.Fatalf("a clear on the predecessor moved the successor's copy")
	}
	testing.SetRealm(testing.NewUserRealm(g1))
	ClearFlag(cross(cur), succ, FnPayouts)
	testing.SetRealm(testing.NewUserRealm(w5))
	ClearFlag(cross(cur), pred, FnCredits)
	if Paused(succ, FnPayouts) || !Paused(succ, FnCredits) || Paused(pred, FnCredits) {
		t.Fatalf("the copies are not the seats' own")
	}
	testing.SetRealm(testing.NewUserRealm(w5))
	ClearFlag(cross(cur), succ, FnCredits)
	if Successor(pred) != succ || Predecessor(succ) != pred || Predecessor(pred) != "" || Successor(succ) != "" || !IsRoute(succ) || Predecessor(namespace+"hlgnot2") != launchRouteGNOT {
		t.Fatalf("links: %s → %s", Successor(pred), Predecessor(succ))
	}
	// a route is never registered twice (a chain never cycles), and a route whose successor migrated on holds
	// nothing to re-point, so it migrates once (AR2: the re-point while nothing was pulled is TestRepointSuccessor's)
	testing.SetRealm(testing.NewCodeRealm(succ))
	aborts(t, "\""+launchRouteGNOT+"\" is a route already", func() { AddRoute(cross(cur), launchRouteGNOT) })
	testing.SetRealm(testing.NewCodeRealm(succ))
	AddRoute(cross(cur), succ2)
	if !IsRoute(succ) || !IsRoute(succ2) || Successor(succ) != succ2 || Predecessor(succ2) != succ || Successor("nobody") != "" {
		t.Fatalf("routes %v", Routes())
	}
	testing.SetRealm(testing.NewCodeRealm(pred))
	aborts(t, pred+" migrated to "+succ+", which migrated on to "+succ2+" already: the migration is under way and cannot be re-pointed", func() { AddRoute(cross(cur), namespace+"hlbubble9") })
	if Successor(pred) != succ || !IsRoute(succ) || IsRoute(namespace+"hlbubble9") {
		t.Fatalf("a refused re-point changed something: %s", Successor(pred))
	}
	j := Render("json")
	if !strings.Contains(j, q(pred)+":{\"epoch\":") || !strings.Contains(j, "\"flags\":[],\"predecessor\":\"\",\"successor\":"+q(succ)+",\"enrolled\":0,\"enrolledAt\":0,\"pulled\":false,\"dropped\":[]}") || !strings.Contains(j, "\"flags\":[],\"predecessor\":"+q(succ)+",\"successor\":\"\",\"enrolled\":0,\"enrolledAt\":0,\"pulled\":false,\"dropped\":[]}") || !strings.Contains(j, q(launchRouteGNOT)+":{\"epoch\":") {
		t.Fatalf("json: %s", j)
	}
	if p := Render(""); !strings.Contains(p, "- `"+succ+"`: payouts epoch 0, successor of `"+pred+"`, migrated to `"+succ2+"`, no flag") || !strings.Contains(p, "- `"+succ2+"`: payouts epoch 0, successor of `"+succ+"`, no flag") {
		t.Fatalf("page: %s", p)
	}
	to := mustBytes32(chain.PackageAddress(pred))
	burn := func(nonce uint32, rcpt [32]byte, body string) []byte {
		m := hl.Message{Version: hl.Version, Nonce: nonce, Origin: ethTest, Sender: [32]byte{9}, Destination: localDomain, Recipient: rcpt, Body: []byte(body)}
		return m.Bytes()
	}
	m1 := burn(70, to, "burn one")
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "only the message's recipient, or a successor of it, can take it", func() { Process(cross(cur), nil, m1) })
	testing.SetRealm(testing.NewCodeRealm(launchRouteGNOT))
	aborts(t, "only the message's recipient, or a successor of it", func() { Process(cross(cur), nil, m1) })
	testing.SetRealm(testing.NewCodeRealm(namespace + "hlgnot2")) // hlgnot's successor, not hlbubble's
	aborts(t, "only the message's recipient, or a successor of it", func() { Process(cross(cur), nil, m1) })
	id1 := hl.ID(m1)
	if Delivered(hex.EncodeToString(id1[:])) != 0 {
		t.Fatalf("a refused message was marked delivered")
	}
	testing.SetRealm(testing.NewCodeRealm(succ))
	if origin, sender, body := Process(cross(cur), nil, m1); origin != ethTest || sender != ([32]byte{9}) || string(body) != "burn one" {
		t.Fatalf("the successor took %d %x %q", origin, sender, body)
	}
	if Delivered(hex.EncodeToString(id1[:])) <= 0 {
		t.Fatalf("not marked delivered")
	}
	// once, in the shared mailbox: the predecessor itself finds it delivered
	testing.SetRealm(testing.NewCodeRealm(pred))
	aborts(t, "already delivered", func() { Process(cross(cur), nil, m1) })
	// two hops: the successor's successor takes a message addressed to the first predecessor
	m2 := burn(71, to, "burn two")
	testing.SetRealm(testing.NewCodeRealm(succ2))
	if _, _, body := Process(cross(cur), nil, m2); string(body) != "burn two" {
		t.Fatalf("two hops: %q", body)
	}
	// a message addressed to the successor is nobody else's: a predecessor never takes its successor's
	m3 := burn(72, mustBytes32(chain.PackageAddress(succ)), "burn three")
	testing.SetRealm(testing.NewCodeRealm(pred))
	aborts(t, "only the message's recipient, or a successor of it", func() { Process(cross(cur), nil, m3) })
	testing.SetRealm(testing.NewCodeRealm(succ))
	if _, _, body := Process(cross(cur), nil, m3); string(body) != "burn three" {
		t.Fatalf("the successor's own: %q", body)
	}
}

// TestRepointSuccessor (AR2): a route whose migrate re-points to another successor while the first pulled nothing
// (the route's own state refuses once page 0 moved) registers the new path in the first's place — the first's
// record and the flags copied to it dropped, so it is a route no more and takes nothing addressed to the
// predecessor; the flags standing on the route now copied to the new path — and a route whose successor migrated
// on is never re-pointed, since that successor holds the lock. hlgnot2 is hlgnot's successor (TestFlags); hlbubble's
// migrated on to hlbubble3 (TestRouteSuccession).
func TestRepointSuccessor(cur realm, t *testing.T) {
	pred, old, next := launchRouteGNOT, namespace+"hlgnot2", namespace+"hlgnot9"
	g1, w5 := Seat(1), Seat(5)
	if Successor(pred) != old || Predecessor(old) != pred || !IsRoute(old) {
		t.Fatalf("hlgnot2 should be hlgnot's successor: %s %s", Successor(pred), Predecessor(old))
	}
	testing.SetRealm(testing.NewUserRealm(g1))
	SetFlag(cross(cur), old, FnPayouts, "")
	testing.SetRealm(testing.NewUserRealm(w5))
	SetFlag(cross(cur), pred, FnCredits, "")
	testing.SetRealm(testing.NewCodeRealm(pred))
	AddRoute(cross(cur), next)
	if IsRoute(old) || Predecessor(old) != "" || Successor(old) != "" || Successor(pred) != next || Predecessor(next) != pred || !IsRoute(next) {
		t.Fatalf("re-pointed: old route %v, %s → %s, %s", IsRoute(old), pred, Successor(pred), Predecessor(next))
	}
	if Paused(old, FnPayouts) || len(Flags(old)) != 0 || !Paused(next, FnCredits) || Paused(next, FnPayouts) || !Paused(pred, FnCredits) {
		t.Fatalf("the flags: old %v next credits %v payouts %v", Flags(old), Paused(next, FnCredits), Paused(next, FnPayouts))
	}
	// the dropped successor is a route no more: its calls as a route are refused, and a message addressed to the
	// predecessor is the new successor's to take, not its
	testing.SetRealm(testing.NewCodeRealm(old))
	aborts(t, "only a route realm", func() { ClearFlags(cross(cur), FnCredits) })
	testing.SetRealm(testing.NewCodeRealm(old))
	aborts(t, "only a route realm", func() { AddRoute(cross(cur), namespace+"hlgnot3") })
	to := mustBytes32(chain.PackageAddress(pred))
	m := hl.Message{Version: hl.Version, Nonce: 80, Origin: ethTest, Sender: [32]byte{9}, Destination: localDomain, Recipient: to, Body: []byte("burn after the re-point")}
	raw := m.Bytes()
	testing.SetRealm(testing.NewCodeRealm(old))
	aborts(t, old+" is a route no more (its predecessor "+pred+" re-pointed the migration to another successor): the line's last realm takes this message", func() { Process(cross(cur), nil, raw) })
	testing.SetRealm(testing.NewCodeRealm(next))
	if _, _, body := Process(cross(cur), nil, raw); string(body) != "burn after the re-point" {
		t.Fatalf("the new successor took %q", body)
	}
	// a route whose successor migrated on holds nothing to re-point: refused
	testing.SetRealm(testing.NewCodeRealm(launchRouteBUBBLE))
	aborts(t, launchRouteBUBBLE+" migrated to "+namespace+"hlbubble2, which migrated on to "+namespace+"hlbubble3 already: the migration is under way and cannot be re-pointed", func() {
		AddRoute(cross(cur), namespace+"hlbubble4")
	})
	if Successor(launchRouteBUBBLE) != namespace+"hlbubble2" || !IsRoute(namespace+"hlbubble3") || IsRoute(namespace+"hlbubble4") {
		t.Fatalf("a refused re-point changed something: %s", Successor(launchRouteBUBBLE))
	}
	j := Render("json")
	if strings.Contains(j, q(old)+":{") || !strings.Contains(j, q(pred)+":{\"epoch\":") || !strings.Contains(j, "\"predecessor\":\"\",\"successor\":"+q(next)+",\"enrolled\":0,\"enrolledAt\":0,\"pulled\":false,\"dropped\":["+q(old)+"]}") || !strings.Contains(j, q(next)+":{\"epoch\":0") || !strings.Contains(j, "\"successors\":["+q(old)+"]") {
		t.Fatalf("json: %s", j)
	}
	if p := Render(""); strings.Contains(p, "`"+old+"`:") || !strings.Contains(p, ", migrated to `"+next+"`, dropped `"+old+"`\n") || !strings.Contains(p, "- `"+next+"`: payouts epoch 0, successor of `"+pred+"`\n") || !strings.Contains(p, "- dropped (routes no more, re-pointed away): `"+old+"` (by `"+pred+"`)\n") {
		t.Fatalf("page: %s", p)
	}
	// the dropped successor is remembered on its predecessor's record (R35): a tombstone the mailbox answers
	// (Dropped), the json and the page show, and three gates read — its Dispatch is refused (Ethereum's token may
	// still enroll it as the router until the next kind 6; a send through it would mint against a lock no realm of the
	// line holds), its Process is refused, a message addressed to itself included (as a launch route it would take
	// it against a lock of 0), and the line's live successor takes a message addressed to it, as it takes one
	// addressed to the predecessor
	if Dropped(old) != pred || Dropped(next) != "" || Dropped(pred) != "" || Dropped(namespace+"nobody") != "" {
		t.Fatalf("dropped: %q %q", Dropped(old), Dropped(next))
	}
	// the tombstones as a list beside Routes (AT6): what the routes' isRealm walks, so a burn to a dropped
	// successor's address is swept and a send to it refused, as a registered route's is
	if dr := DroppedRoutes(); len(dr) != 1 || dr[0] != old {
		t.Fatalf("dropped routes %v", dr)
	}
	if !strings.Contains(j, q(pred)+":{\"epoch\":") || !strings.Contains(j, ",\"enrolled\":0,\"enrolledAt\":0,\"pulled\":false,\"dropped\":["+q(old)+"]}") || !strings.Contains(j, "\"droppedRoutes\":{"+q(old)+":"+q(pred)+"}") {
		t.Fatalf("json: %s", j)
	}
	testing.SetRealm(testing.NewCodeRealm(old))
	aborts(t, old+" is a route no more (its predecessor "+pred+" re-pointed the migration to another successor): it sends nothing", func() { Dispatch(cross(cur), ethTest, [32]byte{1}, []byte("a send through a dropped successor")) })
	toOld := mustBytes32(chain.PackageAddress(old))
	mo := hl.Message{Version: hl.Version, Nonce: 81, Origin: ethTest, Sender: [32]byte{9}, Destination: localDomain, Recipient: toOld, Body: []byte("burn to the dropped successor")}
	rawOld := mo.Bytes()
	idOld := hl.ID(rawOld)
	testing.SetRealm(testing.NewCodeRealm(old))
	aborts(t, old+" is a route no more (its predecessor "+pred+" re-pointed the migration to another successor): the line's last realm takes this message", func() { Process(cross(cur), nil, rawOld) })
	testing.SetRealm(testing.NewCodeRealm(pred))
	aborts(t, "only the message's recipient, or a successor of it", func() { Process(cross(cur), nil, rawOld) })
	testing.SetRealm(testing.NewCodeRealm(launchRouteBUBBLE))
	aborts(t, "only the message's recipient, or a successor of it", func() { Process(cross(cur), nil, rawOld) })
	if Delivered(hex.EncodeToString(idOld[:])) != 0 {
		t.Fatalf("a refused message was marked delivered")
	}
	testing.SetRealm(testing.NewCodeRealm(next))
	if _, _, body := Process(cross(cur), nil, rawOld); string(body) != "burn to the dropped successor" {
		t.Fatalf("the new successor took %q", body)
	}
	if Delivered(hex.EncodeToString(idOld[:])) <= 0 {
		t.Fatalf("not marked delivered")
	}
	// a dropped path registered afresh — the migration re-pointed back to it while the newer successor pulled
	// nothing — is a route again and its tombstone goes, the newer successor's taking its place; and back
	testing.SetRealm(testing.NewCodeRealm(pred))
	AddRoute(cross(cur), old)
	if Dropped(old) != "" || Dropped(next) != pred || !IsRoute(old) || IsRoute(next) || Successor(pred) != old || Predecessor(old) != pred {
		t.Fatalf("re-pointed back: dropped %q %q, routes %v", Dropped(old), Dropped(next), Routes())
	}
	if dr := DroppedRoutes(); len(dr) != 1 || dr[0] != next {
		t.Fatalf("dropped routes %v", dr)
	}
	if j := Render("json"); !strings.Contains(j, "\"dropped\":["+q(next)+"]}") || !strings.Contains(j, "\"droppedRoutes\":{"+q(next)+":"+q(pred)+"}") {
		t.Fatalf("json: %s", j)
	}
	testing.SetRealm(testing.NewCodeRealm(pred))
	AddRoute(cross(cur), next)
	if Dropped(old) != pred || Dropped(next) != "" || IsRoute(old) || !IsRoute(next) || Successor(pred) != next {
		t.Fatalf("re-pointed again: dropped %q %q", Dropped(old), Dropped(next))
	}
	if dr := DroppedRoutes(); len(dr) != 1 || dr[0] != old {
		t.Fatalf("dropped routes %v", dr)
	}
	// page 0 pulled: the predecessor notes it from its Pull (NotePulled: the predecessor alone, for its own
	// successor alone), the mailbox answers Pulled — what r/hlgov's kind 6 asks before it enrols a successor — and
	// the migration can no longer be re-pointed here either (the route's own state refuses it first)
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "only a route realm", func() { NotePulled(cross(cur), next) })
	testing.SetRealm(testing.NewCodeRealm(launchRouteBUBBLE))
	aborts(t, "\""+next+"\" is not "+launchRouteBUBBLE+"'s successor", func() { NotePulled(cross(cur), next) })
	testing.SetRealm(testing.NewCodeRealm(pred))
	aborts(t, "\""+old+"\" is not "+pred+"'s successor", func() { NotePulled(cross(cur), old) })
	if Pulled(next) || Pulled(pred) || Pulled(old) {
		t.Fatalf("pulled before any note")
	}
	testing.SetRealm(testing.NewCodeRealm(pred))
	NotePulled(cross(cur), next)
	testing.SetRealm(testing.NewCodeRealm(pred))
	NotePulled(cross(cur), next) // a second Pull of page 0 notes it again: idempotent
	if !Pulled(next) || Pulled(pred) || !strings.Contains(Render("json"), q(next)+":{\"epoch\":0,\"epochAt\":0,\"sends\":false,\"credits\":true,\"payouts\":false,\"flags\":[\"credits by seat 5") || !strings.Contains(Render("json"), ",\"enrolled\":0,\"enrolledAt\":0,\"pulled\":true,\"dropped\":[]}") || !strings.Contains(Render(""), "- `"+next+"`: payouts epoch 0, successor of `"+pred+"`, page 0 pulled\n") {
		t.Fatalf("pulled: %v\n%s\n%s", Pulled(next), Render("json"), Render(""))
	}
	testing.SetRealm(testing.NewCodeRealm(pred))
	aborts(t, pred+" migrated to "+next+", which pulled page 0 already: the migration is under way and cannot be re-pointed", func() { AddRoute(cross(cur), old) })
	if Successor(pred) != next || !IsRoute(next) || IsRoute(old) {
		t.Fatalf("a refused re-point changed something: %s", Successor(pred))
	}
	testing.SetRealm(testing.NewUserRealm(w5))
	ClearFlag(cross(cur), pred, FnCredits)
	testing.SetRealm(testing.NewUserRealm(w5))
	ClearFlag(cross(cur), next, FnCredits)
}

// TestNoteEnrolled (AR1, R37): r/hlgov's kind-6 closure notes with the mailbox the route it enrolled on Ethereum
// and the height; nobody else notes, only a route is noted, the latest note counts, and the json and the page
// show it — what a route's refund builder reads (a refund waits 14 days after the note); r/hlgov's cancel of a
// kind 6 clears the note (ClearEnrolled, r/hlgov's alone), so the route's clock restarts with a re-proposal.
func TestNoteEnrolled(cur realm, t *testing.T) {
	route := launchRouteBUBBLE
	if h, at := Enrolled(route); h != 0 || at != 0 {
		t.Fatalf("enrolled before any note: %d %d", h, at)
	}
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "only "+hlgovPath+" notes an enrolment, not ", func() { NoteEnrolled(cross(cur), route, 10) })
	testing.SetRealm(testing.NewCodeRealm(route))
	aborts(t, "only "+hlgovPath+" notes an enrolment", func() { NoteEnrolled(cross(cur), route, 10) })
	testing.SetRealm(testing.NewCodeRealm(hlgovPath))
	aborts(t, "no route \""+namespace+"nobody\"", func() { NoteEnrolled(cross(cur), namespace+"nobody", 10) })
	testing.SetRealm(testing.NewCodeRealm(hlgovPath))
	aborts(t, "not a block height", func() { NoteEnrolled(cross(cur), route, 0) })
	if h, _ := Enrolled(route); h != 0 {
		t.Fatalf("a refused note was kept: %d", h)
	}
	testing.SetRealm(testing.NewCodeRealm(hlgovPath))
	NoteEnrolled(cross(cur), route, 4321)
	h, at := Enrolled(route)
	if h != 4321 || at != time.Now().Unix() {
		t.Fatalf("noted: %d %d", h, at)
	}
	if !strings.Contains(Render("json"), q(route)+":{\"epoch\":") || !strings.Contains(Render("json"), ",\"enrolled\":4321,\"enrolledAt\":"+strconv.FormatInt(at, 10)+",\"pulled\":false,\"dropped\":[]}") || !strings.Contains(Render(""), "- `"+route+"`: payouts epoch ") || !strings.Contains(Render(""), ", enrolled on Ethereum by kind 6 at height 4321") {
		t.Fatalf("json %s\npage %s", Render("json"), Render(""))
	}
	// a later kind 6 for the same route replaces the note: a refund's wait restarts
	testing.SetRealm(testing.NewCodeRealm(hlgovPath))
	NoteEnrolled(cross(cur), route, 5000)
	if h, _ := Enrolled(route); h != 5000 {
		t.Fatalf("the latest note: %d", h)
	}
	if h, at := Enrolled(launchRouteGNOT); h != 0 || at != 0 {
		t.Fatalf("another route noted: %d %d", h, at)
	}
	// cleared by r/hlgov alone (a cancelled kind 6): the note gone, the json and the page bare of it; a route or a
	// stranger cannot, nor is a non-route cleared
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "only "+hlgovPath+" notes an enrolment, not ", func() { ClearEnrolled(cross(cur), route) })
	testing.SetRealm(testing.NewCodeRealm(route))
	aborts(t, "only "+hlgovPath+" notes an enrolment", func() { ClearEnrolled(cross(cur), route) })
	testing.SetRealm(testing.NewCodeRealm(hlgovPath))
	aborts(t, "no route \""+namespace+"nobody\"", func() { ClearEnrolled(cross(cur), namespace+"nobody") })
	if h, _ := Enrolled(route); h != 5000 {
		t.Fatalf("a refused clear cleared: %d", h)
	}
	testing.SetRealm(testing.NewCodeRealm(hlgovPath))
	ClearEnrolled(cross(cur), route)
	if h, at := Enrolled(route); h != 0 || at != 0 || !strings.Contains(Render("json"), q(route)+":{\"epoch\":") || !strings.Contains(Render("json"), ",\"enrolled\":0,\"enrolledAt\":0,\"pulled\":false,\"dropped\":[]}") || strings.Contains(Render(""), "enrolled on Ethereum by kind 6") {
		t.Fatalf("not cleared: %d %d\n%s", h, at, Render("json"))
	}
	testing.SetRealm(testing.NewCodeRealm(hlgovPath))
	ClearEnrolled(cross(cur), route) // nothing noted: nothing to clear, no refusal
	testing.SetRealm(testing.NewCodeRealm(hlgovPath))
	NoteEnrolled(cross(cur), route, 6000)
	if h, _ := Enrolled(route); h != 6000 {
		t.Fatalf("a note after the clear: %d", h)
	}
}

// spellings are the four spellings of a gno address IsValid takes: as gno writes it (a), all in upper case, and with
// bech32m's checksum (BIP-350) in lower case and in upper (eth F9, its review's finding 1).
func spellings(a address) []address {
	b, err := hl.AddressToBytes32(a)
	if err != nil {
		panic(err)
	}
	data, err := bech32.ConvertBits(b[12:], 8, 5, true)
	if err != nil {
		panic(err)
	}
	m, err := bech32.EncodeM("g", data)
	if err != nil {
		panic(err)
	}
	return []address{a, address(strings.ToUpper(string(a))), address(m), address(strings.ToUpper(m))}
}

// TestSeatsCanonical (eth F9, the sibling of the routes' credit and sweep votes; its review's finding 1): a seat's
// address is taken in its canonical spelling (CanonicalAddress) — IsValid takes three more of the same twenty bytes,
// upper case and bech32m's checksum in either case, which no caller's address ever equals, so a seat set so would
// never flag: a holder's own address in each of them holds its seat already, on its own seat or another, two
// spellings of one new key in one proposal are one key named twice, and a new key typed with bech32m's checksum in
// upper case is named, seated and flags in the spelling gno writes.
func TestSeatsCanonical(cur realm, t *testing.T) {
	h := 0
	for n := 1; n <= seatCount && h == 0; n++ {
		if Seat(n) != "" {
			h = n
		}
	}
	if h == 0 {
		t.Fatalf("no seat is held")
	}
	held := Seat(h)
	other := h%seatCount + 1
	for _, a := range spellings(held)[1:] {
		asMember(cur)
		aborts(t, "hlmailbox: seat "+strconv.Itoa(h)+" holds "+string(held)+" already", func() { NewRotateSeatsRequest(cross(cur), strconv.Itoa(h)+":"+string(a)) })
		asMember(cur)
		aborts(t, "hlmailbox: "+string(held)+" holds seat "+strconv.Itoa(h)+" already", func() { NewRotateSeatsRequest(cross(cur), strconv.Itoa(other)+":"+string(a)) })
	}
	// two spellings of one new key in one proposal are one key, named twice
	twice := testutils.TestAddress("hl-canonical-twice")
	asMember(cur)
	aborts(t, "hlmailbox: "+string(twice)+" named for seats 5 and 6", func() {
		NewRotateSeatsRequest(cross(cur), "5:"+string(spellings(twice)[1])+",6:"+string(spellings(twice)[2]))
	})
	key := testutils.TestAddress("hl-canonical-watch")
	typed := spellings(key)[3]
	asMember(cur)
	req := NewRotateSeatsRequest(cross(cur), "6:"+string(typed))
	if !strings.Contains(req.Description(), "set seat 6 to "+string(key)) {
		t.Fatalf("description: %s", req.Description())
	}
	pass(cur, req)
	id := ledger.Last()
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), id)
	if Seat(6) != key || SeatOf(key) != 6 {
		t.Fatalf("seat 6 %s", Seat(6))
	}
	testing.SetRealm(testing.NewUserRealm(key))
	SetFlag(cross(cur), launchRouteGNOT, FnSends, "")
	if !Paused(launchRouteGNOT, FnSends) {
		t.Fatalf("the seated key's flag does not stand")
	}
	testing.SetRealm(testing.NewUserRealm(key))
	ClearFlag(cross(cur), launchRouteGNOT, FnSends)
}

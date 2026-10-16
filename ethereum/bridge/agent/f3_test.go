package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// Fold F3's tests of the machines (eth/bridge/M3b-NOTES.md, "Fold F3"): the executor's wait for a target's code
// (NoCodeYet); hlwatch's list of the decisions waiting for code and its alarm; the burns that follow a token's line of
// migrations and the amount-0 burns nobody signs or relays; every fresh scan from the config's eth.deployBlock; a
// waiting send's header bought by the fee that send paid.

// noCodeYet is HlAdmin's refusal of a matured kind 8 or 9 whose target has no code yet (fold F2), as the estimate
// answers it.
func noCodeYet(target common.Address) error {
	data := append(crypto.Keccak256([]byte("NoCodeYet(address)"))[:4], common.LeftPadBytes(target.Bytes(), 32)...)
	return ethx.DecodeRevert(data, "execution reverted")
}

// kind8Params is a kind 8's parameters: the proxy, the implementation, its code hash, no call.
func kind8Params(proxy, impl common.Address, codehash common.Hash) []byte {
	p := ethx.AddressTo32(proxy)
	i := ethx.AddressTo32(impl)
	return append(append(p[:], i[:]...), codehash[:]...)
}

// A matured kind 8 whose implementation has no code yet: HlAdmin refuses the estimate NoCodeYet and nothing is sent.
// The executor waits, as it waits for NotMature — never a failure, never parked, never the parked alarm — and looks
// again every few minutes; once the code is there the decision executes.
func TestExecutorWaitsForCode(t *testing.T) {
	w := newWorld(t, 100, "primary")
	a := w.e.admins[ethAdmin]
	impl := common.HexToAddress("0x000000000000000000000000000000000000d00d")
	a.lastSeq = 1
	a.decisions[1] = &ethx.Decision{Number: 1, Kind: 8, DeliveredTime: w.e.now - 8*86_400, Params: kind8Params(ethGate, impl, common.Hash{7})}
	w.e.fail["execute"] = noCodeYet(impl)
	for i := 0; i < 5; i++ {
		w.tick()
		w.advance(2 * time.Hour)
	}
	if _, parked := w.r.Alarm.Standing()["parked:execute:1"]; parked {
		t.Fatalf("a decision waiting for code was parked and alarmed: %v", w.r.Alarm.Standing())
	}
	if at := w.r.state.Attempts["execute:1"]; at == nil || at.Failures != 0 || !at.Parked.IsZero() {
		t.Fatalf("a wait counted as a failure: %+v", at)
	}
	if note := w.r.last["execute:1"]; !strings.Contains(note, "waits for code") || !strings.Contains(note, impl.Hex()) {
		t.Fatalf("the status does not say it waits for its implementation's code: %q", note)
	}
	if a.decisions[1] == nil {
		t.Fatal("the decision was spent")
	}
	// asked again once its retry is past (still no code), then not again inside the few minutes; past them, with the
	// code deployed, it executes
	w.tick()
	sent := len(w.e.sentOf("execute"))
	w.tick()
	if len(w.e.sentOf("execute")) != sent {
		t.Fatal("asked again inside the retry")
	}
	delete(w.e.fail, "execute")
	w.advance(time.Hour)
	w.tick()
	if a.decisions[1] != nil {
		t.Fatalf("not executed once its code is there: %q", w.r.last["execute:1"])
	}
}

// hlwatch lists every pending kind 8 or 9 whose target has no code at the read block, with its maturity; one that has
// waited past its maturity longer than the age is alarmed (decision:code-wait:<n>) and the page is not green; once
// the code is there the listing and the alarm are gone.
func TestWatchListsDecisionsWaitingForCode(t *testing.T) {
	r := newMonRig(t)
	r.m.ReadOnly = true
	impl := common.HexToAddress("0x000000000000000000000000000000000000d00d")
	b := body(8, 1, kind8Params(m3bGate, impl, common.Hash{7})...)
	r.decide(1, b, 50, b, true) // r/hlgov's own decision, delivered…
	r.mu.Lock()
	d := r.pending[1]
	d.DeliveredTime = uint64(r.now.Unix()) - 20*86_400 // …twenty days ago: matured thirteen days ago
	r.pending[1] = d
	r.mu.Unlock()
	w := &Watch{m: r.m, Supermajority: 66.66}
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	text := v.Text("build x")
	if !strings.Contains(text, "decisions waiting for code") || !strings.Contains(text, "#1 kind 8: no code at "+impl.Hex()) || !strings.Contains(text, "STALE") {
		t.Fatalf("the page does not list the decision waiting for code:\n%s", text)
	}
	if !strings.Contains(text, "NO CODE YET: the decision pends") {
		t.Fatalf("the check before it executes still says the decision would be spent:\n%s", text)
	}
	if ok, why := v.Green(); ok || !strings.Contains(strings.Join(why, ";"), "decision:code-wait:1") {
		t.Fatalf("a decision waiting for code past the age is green: %v", why)
	}
	// the code deployed: neither listed nor alarmed
	for _, p := range []*m3bProvider{r.pa, r.pb} {
		p.codeX = func(a common.Address) hexutil.Bytes {
			if a == impl {
				return hexutil.Bytes{0x60, 0x80, 0x60, 0x40}
			}
			return nil
		}
	}
	r.step(10 * time.Second)
	if v, err = w.View(context.Background()); err != nil {
		t.Fatal(err)
	}
	if text := v.Text("build x"); strings.Contains(text, "#1 kind 8: no code at") {
		t.Fatalf("still listed:\n%s", text)
	}
	if _, standing := v.Alarms["decision:code-wait:1"]; standing {
		t.Fatal("the alarm stands once the code is there")
	}
}

// A kind 6 named the successor hlgnot2 as GNOT's router: every later burn is addressed to it. The inbound relayer
// counts such a burn as the token's own (the line hlgnot → hlgnot2 read from gno.land) and delivers it through the
// line's live realm; a burn to a realm of no line stays ignored.
func TestInboundFollowsTheLine(t *testing.T) {
	r := newInboundRig(t)
	r.gno.set(m3bNS+"hlgnot", "json", `{"live":true,"lock":0,"migratedTo":"`+m3bNS+`hlgnot2"}`)
	r.gno.set(m3bNS+"hlgnot2", "json", `{"live":true,"lock":1000,"importing":false,"imported":3}`)
	gnot2 := gnoside.RealmKey(m3bNS + "hlgnot2")
	id := r.chain.burn(m3bGNOT, gnot2, r.alice, 200, 80)
	r.chain.burn(m3bGNOT, gnoside.RealmKey("gno.land/r/someone/else"), r.alice, 300, 85) // a realm of no line
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	if len(r.job.st.Pending) != 1 || r.job.st.Pending[0].ID != hex.EncodeToString(id[:]) {
		t.Fatalf("the burn to the successor was not queued (ignored %d): %+v", r.job.st.Ignored, r.job.st.Pending)
	}
	if r.job.st.Ignored != 1 {
		t.Fatalf("the burn to a realm of no line: ignored %d, want 1", r.job.st.Ignored)
	}
	raw, _ := hex.DecodeString(r.job.st.Pending[0].Message)
	r.sign(0, 0, raw, false)
	r.sign(1, 0, raw, false)
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	if calls := r.gno.callsOf("Process"); len(calls) != 1 || !strings.HasPrefix(calls[0], m3bNS+"hlgnot2.Process(") {
		t.Fatalf("not delivered through the line's live realm: %v", r.gno.calls)
	}
	// a line that cannot be read holds the scan (its cursor stays): no burn of a successor is passed over
	cursor := r.job.st.Cursor
	r.chain.mine(120, 120)
	r.gno.set(m3bNS+"hlgnot", "json", `{"live":true,"lock":0,"migratedTo":"`+m3bNS+`hlgnot3"}`) // hlgnot3 renders nothing
	if err := r.tick(); err == nil || !strings.Contains(err.Error(), "lines of migrations") {
		t.Fatalf("an unread line: %v", err)
	}
	if r.job.st.Cursor != cursor {
		t.Fatalf("the scan moved past blocks %d–%d with a line unread", cursor+1, r.job.st.Cursor)
	}
}

// The signer counts and signs a burn addressed to the line's successor as the token's own (the identity judged over
// the line's summed counters); before the line was followed it was ignored, and the uncredited burn was missing from
// the identity.
func TestSignerSignsABurnToTheLinesSuccessor(t *testing.T) {
	r := newSignerRig(t)
	r.gno.set(m3bNS+"hlgnot", "json", `{"live":true,"lock":0,"migratedTo":"`+m3bNS+`hlgnot2"}`)
	r.gno.set(m3bNS+"hlgnot2", "json", `{"live":true,"lock":1000,"imported":3}`)
	id := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot2"), r.alice, 200, 80)
	r.round()
	sg, err := r.s.readSigned(0)
	if err != nil {
		t.Fatalf("the burn to the successor was not signed (ignored: %v): %v", r.s.state.Index.Ignored, err)
	}
	if sg.ID != hex.EncodeToString(id[:]) {
		t.Fatalf("signed %s", sg.ID)
	}
	ident := r.status()["identity"].(map[string]any)["GNOT"].(map[string]any)
	if ident["verdict"] != "holds" || ident["uncredited"].(int64) != 200 {
		t.Fatalf("the identity over the line: %v", ident)
	}
}

// A burn of amount 0 moves nothing (§5.5): no signer indexes or signs it, and the inbound relayer never queues it.
func TestBurnsOfAmountZeroAreSkipped(t *testing.T) {
	r := newSignerRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.burn(m3bGNOT, gnot, r.alice, 0, 80)
	r.chain.burn(m3bGNOT, gnot, r.alice, 200, 81)
	r.round()
	if _, err := r.s.readSigned(0); err == nil {
		t.Fatal("a burn of amount 0 was signed")
	}
	if _, err := r.s.readSigned(1); err != nil {
		t.Fatalf("the burn beside it was not signed: %v", err)
	}
	if !strings.Contains(strings.Join(r.s.state.Index.Ignored, ";"), "an amount of zero") {
		t.Fatalf("the amount-0 burn is not named among the ignored: %v", r.s.state.Index.Ignored)
	}
	in := newInboundRig(t)
	in.chain.burn(m3bGNOT, gnot, in.alice, 0, 80)
	if err := in.tick(); err != nil {
		t.Fatal(err)
	}
	if len(in.job.st.Pending) != 0 {
		t.Fatalf("a burn of amount 0 was queued: %+v", in.job.st.Pending)
	}
}

// With eth.deployBlock in the config every fresh scan starts there — a signer's burn index, a monitor's (and so
// hlwatch's), the inbound relayer's — with no -from-block and no search for the mailbox's first block (E-M3b's Q1).
func TestFreshScansStartAtTheConfigsDeployBlock(t *testing.T) {
	cfg := m3bConfig()
	if err := json.Unmarshal([]byte(`{"eth":{"deployBlock":42}}`), cfg); err != nil {
		t.Fatal(err)
	}
	chain := newM3bChain(100)
	pair, pa, _ := m3bPair(chain)
	g := newM3bGno(60, time.Now())
	k, err := hl.NewValidatorKey()
	if err != nil {
		t.Fatal(err)
	}
	s := &Signer{Cfg: cfg, Key: k, Store: t.TempDir(), Alarm: &Alarmer{Source: "test"}, pair: pair, gno: g}
	if err := s.load(); err != nil {
		t.Fatal(err)
	}
	if err := s.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.state.From != 42 {
		t.Fatalf("the signer's index starts at %d, not the config's deployBlock 42", s.state.From)
	}
	m := &Monitor{Cfg: cfg, Alarm: &Alarmer{Source: "test"}, ReadOnly: true, pair: pair, own: g, sources: []gnoside.Reader{g}}
	if err := m.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.state.From != 42 {
		t.Fatalf("the monitor's scans start at %d, not 42", m.state.From)
	}
	if n := pa.count("eth_getCode"); n != 0 {
		t.Fatalf("%d eth_getCode reads: the mailbox's first block was searched for", n)
	}
	in := newInboundRig(t)
	in.job.FromBlock = 0
	if err := json.Unmarshal([]byte(`{"eth":{"deployBlock":42}}`), in.job.cfg); err != nil {
		t.Fatal(err)
	}
	if err := in.tick(); err != nil {
		t.Fatal(err)
	}
	if in.job.st.From != 42 {
		t.Fatalf("the inbound relayer's scan starts at %d, not 42", in.job.st.From)
	}
}

// dispatchSend puts a route's send into block h as r/hlgnot's TransferRemote leaves it: the mailbox's Dispatch and
// the route's SentTransferRemote with the fee the send paid, in one transaction.
func (f *fakeGno) dispatchSend(h int64, route string, fee int64, m hl.Message) {
	f.dispatch(h, m)
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.results[h][len(f.results[h])-1]
	id := hl.ID(f.raw(f.count - 1))
	r.Events = append(r.Events, &gnolight.Event{Type: "SentTransferRemote", PkgPath: route, Attributes: []gnolight.Attribute{
		{Key: "destination", Value: "31337"}, {Key: "recipient", Value: hex.EncodeToString(m.Recipient[:])}, {Key: "amount", Value: "500"},
		{Key: "fee", Value: strconv.FormatInt(fee, 10)}, {Key: "from", Value: "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5"}, {Key: "id", Value: hex.EncodeToString(id[:])}}})
	var anys [][]byte
	for _, e := range r.Events {
		anys = append(anys, gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(e)))
	}
	r.Leaf = gnolight.EncodeResult(nil, []byte("ok"), anys)
}

// A waiting send's header is bought by the fee that send paid — its SentTransferRemote event's — never its route's fee
// now (E-M3's Q1): a send that paid 50 GNOT covers an update after a decision lowered the fee to 1 GNOT, and a send
// that paid 1 GNOT does not after the fee rose to 50.
func TestUpdaterCountsEachSendsOwnFee(t *testing.T) {
	// an update costs 4.2M gas at (1 + 1) gwei, 0.0084 ETH; at 0.0002 ETH a GNOT, 50 GNOT is 0.01 ETH, 1 GNOT 0.0002
	w := newUpdWorld(t, 100)
	w.u.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.0002}
	w.g.dispatchSend(105, testGNOT, 50_000_000, sendMsg(testGNOT, ethGNOT, 500))
	w.g.renders[testGNOT+":json"] = routeJSON(1_000_000, "")
	w.g.head = 110
	w.advance(time.Hour)
	w.tick(t)
	if len(w.e.sentOf("update")) != 1 || w.e.trusted != 106 {
		t.Fatalf("the send's own 50 GNOT covers an update, yet none was bought (counted at the route's 1 GNOT now): %v", w.u.status["due"])
	}
	w2 := newUpdWorld(t, 100)
	w2.u.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.0002}
	w2.g.dispatchSend(105, testGNOT, 1_000_000, sendMsg(testGNOT, ethGNOT, 500))
	w2.g.renders[testGNOT+":json"] = routeJSON(50_000_000, "")
	w2.g.head = 110
	w2.advance(time.Hour)
	w2.tick(t)
	if len(w2.e.sentOf("update")) != 0 {
		t.Fatal("a send that paid 1 GNOT bought an update at the route's 50 GNOT now")
	}
}

// A staging call whose every signature the client counted already is refused (fold F4's NoNewSignature): the
// updater sends nothing for it and goes on with the calls that carry the rest. Here the earlier stage cannot be read
// back, so the update is sent whole: its first call repeats the staged slots and is skipped; the others finish it.
func TestUpdaterSkipsACallWithNothingNew(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.u.Cfg.Eth.MaxSigsPerCall = 2
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 1))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.e.failNth["update"] = 2 // the second staging call is lost: the stage holds the first call's two slots
	if err := w.u.tick(context.Background()); err == nil {
		t.Fatal("the lost call was not reported")
	}
	w.e.failNth = map[string]int{}
	w.e.mu.Lock()
	w.e.txs = map[common.Hash]*ethx.TxInput{} // the staging call's calldata cannot be read back
	w.e.mu.Unlock()
	w.advance(time.Minute)
	w.tick(t)
	if w.e.trusted != 106 {
		t.Fatalf("the update was not finished past the call with nothing new: trusted %d (plan %q)", w.e.trusted, w.statusPlan())
	}
	if _, ok := w.u.Alarm.Standing()["updater:refused"]; ok {
		t.Fatal("a call with nothing new was taken as a refusal")
	}
}

// The updater and the relayer fail closed on prices (round 2's fact check, E2-FC7): without a positive GNOT/ETH and
// BUBBLE/ETH rate they refuse to start, where an empty table once turned "a header is bought only when the waiting
// fees cover it" off — every waiting send buying its header. A relayer that names no contracts delivers nothing to
// Ethereum, buys no header, and starts.
func TestMachinesRefuseToStartWithoutPrices(t *testing.T) {
	for name, prices := range map[string]map[string]float64{
		"none":      nil,
		"GNOT only": {"GNOT": 0.0000282},
		"a zero":    {"GNOT": 0.0000282, "BUBBLE": 0},
	} {
		cfg := testConfig()
		cfg.Eth.Prices = prices
		u := &Updater{Cfg: cfg}
		if err := u.init(); err == nil || !strings.Contains(err.Error(), "eth.prices has no positive rate for") {
			t.Errorf("%s: the updater started: %v", name, err)
		}
		r := &Relayer{Cfg: cfg}
		if err := r.init(); err == nil || !strings.Contains(err.Error(), "eth.prices has no positive rate for") {
			t.Errorf("%s: the relayer started: %v", name, err)
		}
	}
	cfg := testConfig()
	cfg.Eth.Prices = map[string]float64{"GNOT": 0.0000282}
	if err := (&Updater{Cfg: cfg}).init(); err == nil || !strings.Contains(err.Error(), "for BUBBLE (") {
		t.Fatalf("the missing rate is not named: %v", err)
	}
	cfg.Eth.Mailbox, cfg.Eth.Client = "", ""
	if err := (&Relayer{Cfg: cfg}).init(); err != nil {
		t.Fatalf("a relayer with no contracts (no header to buy) refused: %v", err)
	}
	cfg = testConfig()
	if err := (&Updater{Cfg: cfg}).init(); err != nil {
		t.Fatalf("priced: %v", err)
	}
	if err := (&Relayer{Cfg: cfg}).init(); err != nil {
		t.Fatalf("priced: %v", err)
	}
}

// A kind 9's reading before the vote (round 2's fact check): the successor's state as HlAdmin's execute reads it
// beside its code. "MUTABLE: would be spent" when its predecessor() is not the HlAdmin that holds the decision, it has
// inherited, names a successor or runs at another clock factor, its code is no the decision's or an EIP-7702
// designator; "NO CODE YET: pends" with no code; "NOT SHOWN IMMUTABLE" when HlAdmin would execute it but nothing here
// vouches for its code — no build given matches it, or (fold F7, E2-RT1/RT3) a build that is a hash alone: its
// constructor and immutables unread ("immutable" needs forge's artifact and the creation transaction: f7_test.go).
// The page prints the same beside a pending kind 9.
func TestSuccessorCheckReadsItsState(t *testing.T) {
	r := newMonRig(t)
	ctx := context.Background()
	succ := common.HexToAddress("0x000000000000000000000000000000000000a9a9")
	code := hexutil.Bytes{0x60, 0x80, 0x60, 0x40, 0x09}
	var mu sync.Mutex
	cur := code
	for _, p := range []*m3bProvider{r.pa, r.pb} {
		p.codeX = func(a common.Address) hexutil.Bytes {
			mu.Lock()
			defer mu.Unlock()
			if a == succ {
				return cur
			}
			return nil
		}
	}
	pred, inherited, next, factor := m3bAdmin, false, common.Address{}, uint64(1)
	r.chain.view(succ, "predecessor", func(uint64, []any) []any { return []any{pred} })
	r.chain.view(succ, "inherited", func(uint64, []any) []any { return []any{inherited} })
	r.chain.view(succ, "successor", func(uint64, []any) []any { return []any{next} })
	r.chain.view(succ, "clockFactor", func(uint64, []any) []any { return []any{factor} })
	hash := crypto.Keccak256Hash(code)
	w := &Watch{m: r.m, Builds: map[string]Build{"hladmin-v2": {Hash: hash}}}
	at := ethx.AtNumber(100)
	check := func(want common.Hash) string { return w.SuccessorCheck(ctx, at, m3bAdmin, 1, succ, want) }
	verdictOf := func(s string) string { return s[strings.LastIndex(s, " — ")+len(" — "):] }

	if got := check(hash); !strings.HasPrefix(verdictOf(got), VerdictUnshown) || !strings.Contains(got, "is a hash alone") || !strings.Contains(got, "predecessor() "+m3bAdmin.Hex()+", inherited() false, successor() 0x0000000000000000000000000000000000000000, clockFactor() 1") {
		t.Fatalf("a successor that holds, held to a hash alone: %s", got)
	}
	for _, c := range []struct {
		name string
		set  func()
		why  string
	}{
		{"another predecessor", func() { pred = common.HexToAddress("0xbad") }, "its predecessor() is " + common.HexToAddress("0xbad").Hex() + ", not this HlAdmin " + m3bAdmin.Hex()},
		{"inherited", func() { inherited = true }, "it has inherited already"},
		{"a successor of its own", func() { next = common.HexToAddress("0x1") }, "it names a successor of its own"},
		{"another clock factor", func() { factor = 7 }, "its clock factor is 7, not this HlAdmin's 1"},
	} {
		c.set()
		if got := verdictOf(check(hash)); !strings.HasPrefix(got, VerdictSpent) || !strings.Contains(got, c.why) {
			t.Errorf("%s: %s", c.name, got)
		}
		pred, inherited, next, factor = m3bAdmin, false, common.Address{}, 1
	}
	if got := verdictOf(check(common.Hash{1})); !strings.HasPrefix(got, VerdictSpent) || !strings.Contains(got, "not the decision's") {
		t.Fatalf("another code hash: %s", got)
	}
	if got := verdictOf(w.SuccessorCheck(ctx, at, succ, 1, succ, hash)); !strings.HasPrefix(got, VerdictSpent) || !strings.Contains(got, "this HlAdmin itself") {
		t.Fatalf("HlAdmin as its own successor: %s", got)
	}
	w.Builds = map[string]Build{"other": {Hash: common.Hash{2}}}
	if got := verdictOf(check(hash)); !strings.HasPrefix(got, VerdictUnshown) || !strings.Contains(got, "matches no reproducible build") {
		t.Fatalf("no build matches: %s", got)
	}
	w.Builds = map[string]Build{"hladmin-v2": {Hash: hash}}
	if got := verdictOf(check(common.Hash{})); !strings.HasPrefix(got, VerdictUnshown) || !strings.Contains(got, "no code hash") {
		t.Fatalf("no code hash given: %s", got)
	}
	mu.Lock()
	cur = append(hexutil.Bytes{0xef, 0x01, 0x00}, make([]byte, 20)...)
	mu.Unlock()
	if got := verdictOf(check(crypto.Keccak256Hash(cur))); !strings.HasPrefix(got, VerdictSpent) || !strings.Contains(got, "EIP-7702") {
		t.Fatalf("an EIP-7702 account: %s", got)
	}
	mu.Lock()
	cur = hexutil.Bytes{}
	mu.Unlock()
	if got := verdictOf(check(hash)); got != VerdictNoCode {
		t.Fatalf("no code yet: %s", got)
	}
	mu.Lock()
	cur = code
	mu.Unlock()
	// the page beside a pending kind 9: the same reading, the HlAdmin that holds the decision as the predecessor
	r.mu.Lock()
	r.lastSeq = 1
	r.pending[1] = ethx.Decision{Number: 1, Kind: 9, DeliveredBlock: 90, DeliveredTime: uint64(r.now.Unix()), Params: append(common.LeftPadBytes(succ.Bytes(), 32), hash.Bytes()...)}
	r.mu.Unlock()
	v, err := w.View(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.Join(v.Checks, "\n"); !strings.Contains(text, "#1 kind 9: successor "+succ.Hex()) || !strings.Contains(text, " — "+VerdictUnshown) {
		t.Fatalf("the page's check of a pending kind 9: %s", text)
	}
	factor = 3
	if v, err = w.View(ctx); err != nil {
		t.Fatal(err)
	}
	if text := strings.Join(v.Checks, "\n"); !strings.Contains(text, " — "+VerdictSpent+" (its clock factor is 3, not this HlAdmin's 1)") {
		t.Fatalf("the page's check of a pending kind 9 at another clock factor: %s", text)
	}
}

// The guardians' and watch keys' floats are held to 0.05 ETH each (round 2's fact check, E2-FC3): a flag costs
// 101–112K gas and the sender holds 1.2× the estimate at twice the base fee, so 0.01 ETH sends no gate flag above
// ≈37 gwei. The monitor reads the gate's six keys every few minutes and alarms the one under 0.05 ETH, naming the
// base fee it can still flag under; topped up, the alarm clears. The watch key's planned rate is the measured flag's.
func TestFlagKeysKeepTheirFloat(t *testing.T) {
	r := newMonRig(t)
	keys := [6]common.Address{common.HexToAddress("0x01"), common.HexToAddress("0x02"), common.HexToAddress("0x03"), common.HexToAddress("0x04"), monWatchEth, common.HexToAddress("0x06")}
	r.chain.mu.Lock()
	r.chain.balances = map[common.Address]*big.Int{}
	for _, k := range keys {
		r.chain.balances[k] = big.NewInt(5e16)
	}
	r.chain.balances[keys[1]] = big.NewInt(1e16) // seat 2: 0.01 ETH
	r.chain.mu.Unlock()
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	alarms := r.m.Alarm.Standing()
	text, ok := alarms["float:key2"]
	if !ok || !strings.Contains(text, "seat 2's key (a guardian) "+keys[1].Hex()+" holds 0.010000 ETH: it sends a gate flag (112448 gas) while the base fee is under ≈37 gwei — under the 0.05 ETH a flag key keeps (≈185 gwei)") {
		t.Fatalf("a guardian holding 0.01 ETH: %q (standing %v)", text, alarms)
	}
	for _, k := range []string{"float:key1", "float:key3", "float:key4", "float:key5", "float:key6"} {
		if _, ok := alarms[k]; ok {
			t.Fatalf("%s alarmed at 0.05 ETH", k)
		}
	}
	r.chain.mu.Lock()
	r.chain.balances[keys[1]] = big.NewInt(6e16)
	r.chain.mu.Unlock()
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.m.Alarm.Standing()["float:key2"]; !ok {
		t.Fatal("the keys were read again before a few minutes passed")
	}
	r.step(floatEvery)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.m.Alarm.Standing()["float:key2"]; ok {
		t.Fatal("the alarm stands after the top-up")
	}
	if got := FlagCeilingGwei(1e16); got < 37 || got > 37.1 {
		t.Fatalf("0.01 ETH flags under %.2f gwei", got)
	}
	if plannedPerDay["watch-eth"] != 2*112_448*0.2e9 {
		t.Fatalf("the watch key's planned rate: %v", plannedPerDay["watch-eth"])
	}
}

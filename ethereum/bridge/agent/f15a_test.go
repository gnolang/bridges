package agent

// Audit round 4's go-spend fold (lane F15a): each test fails on 1a6770b5's code and passes with the fold (the notes,
// fold-r4/f15a.md, keep both outputs).

import (
	"context"
	"math/big"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// stringErrorAny is the amino Any of an abci.StringError carrying s ({1: url, 2: {1: s}}).
func stringErrorAny(s string) []byte {
	field := func(num byte, b []byte) []byte {
		out := []byte{num<<3 | 2}
		n := len(b)
		for n >= 0x80 {
			out = append(out, byte(n)|0x80)
			n >>= 7
		}
		out = append(out, byte(n))
		return append(out, b...)
	}
	return gnolight.Any("/abci.StringError", field(1, []byte(s)))
}

// poisonLossy puts a stranger's failed TransferRemote into block h — its realm's panic echoes the recipient string the
// caller chose ("\xff\xfe"), which the chain hashes raw while block_results' JSON serves it as U+FFFD — and makes header
// h+1 carry the chain's own root (the true leaf), not the one the JSON rebuilds (the go-spend auditor's E4-GS1 model).
func poisonLossy(w *testWorld, h int64) {
	msg := "hlgnot: the recipient is not an Ethereum address: %s (want 0x and 40 hex digits)"
	trueLeaf := gnolight.EncodeResult(stringErrorAny(strings.Replace(msg, "%s", "\xff\xfe", 1)), nil, nil)
	jsonLeaf := gnolight.EncodeResult(stringErrorAny(strings.Replace(msg, "%s", "��", 1)), nil, nil)
	w.g.mu.Lock()
	defer w.g.mu.Unlock()
	w.g.results[h] = append(w.g.results[h], &gnolight.TxResult{Leaf: jsonLeaf})
	var leaves [][]byte
	for _, r := range w.g.results[h] {
		leaves = append(leaves, r.Leaf)
	}
	leaves[len(leaves)-1] = trueLeaf
	root, _ := gnolight.SimpleHash(leaves)
	delete(w.g.headers, h+1)
	b := w.g.header(h + 1)
	hdr := *b.Header
	hdr.LastResultsHash = root[:]
	w.g.headers[h+1] = &gnolight.Block{Header: &hdr, Hash: hdr.Hash(), NumTxs: hdr.NumTxs}
}

// sendsDelivered counts the deliveries of the nonces from..to.
func sendsDelivered(t *testing.T, w *testWorld, from, to uint32) int {
	n := 0
	for k := range w.e.deliveredLeaves(t) {
		if k >= from && k <= to {
			n++
		}
	}
	return n
}

// E4-GS4, E4-CF2 (a): a stranger re-emits a six-hour send right after each header the updater buys for it (anyone may
// Reemit; one MsgCall), so the send's newest event always lies past that header. The booking names the send under the
// header it was bought for (the trailer of the update), so the relayer delivers it from that header's leaf at no fee
// check whatever newer event it has, and the updater buys no other: one header, one draw on the budget, delivered.
func TestReemitAfterTheHeaderBuysNoOther(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1} // 50 GNOT = 0.0005 ETH < a delivery at 2 gwei
	u := updaterOver(t, w)
	u.state.Budget.Balance = ethx.GweiOf(50_000_000).String() // 0.05 ETH of surplus earlier sends' fees left
	start := u.state.Budget.balance()
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 40_000_000)) // S, nonce 0
	w.g.head = 110
	w.step(6*time.Hour + time.Minute)
	stranger := 0
	for i := 0; i < 3*60; i++ { // half an hour of 10-second steps
		before := len(w.e.sentOf("update"))
		_ = u.tick(context.Background())
		if len(w.e.sentOf("update")) > before { // the stranger sees the update and re-emits S before the relayer judges it
			w.g.mu.Lock()
			_, _ = w.g.reemitLocked([]uint32{0})
			w.g.mu.Unlock()
			stranger++
		}
		w.tick()
		w.step(10 * time.Second)
	}
	ups := len(w.e.sentOf("update"))
	got := w.e.deliveredLeaves(t)
	spent := new(big.Int).Sub(start, u.state.Budget.balance())
	if ups != 1 || len(got) != 1 || got[0] == 0 || got[0] > 1000 || stranger != 1 {
		t.Fatalf("half an hour, a stranger's Reemit after each header: %d headers bought, delivered %v (%d re-emits by the stranger); relayer %s; ledger %s", ups, got, stranger, w.r.last["deliver:0"], u.state.Budget.text())
	}
	if len(u.state.Budget.Entries) != 1 || spent.Sign() <= 0 || spent.Cmp(ethx.GweiOf(10_000_000)) > 0 {
		t.Fatalf("the budget: %d bookings, %s ETH spent (%s)", len(u.state.Budget.Entries), eth(spent), u.state.Budget.text())
	}
}

// E4-GS2 (a): the gate is flagged; GovDAO's unpause and a stranger's five fee-short sends land in one block. The
// updater buys the decision's header with the routes paused out of its waiting: it books none of them — and the
// relayer, once the gate opens, delivers none of them at no fee check from that header: they are held to their own
// fee and wait as unproven sends do.
func TestPausedDecisionHeaderBooksNoSend(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1} // a send's 50 GNOT fee = 0.0005 ETH < a delivery at 2 gwei
	u := updaterOver(t, w)
	w.e.flags[4] = ethx.Flag{BlockNumber: 9, Time: 1} // a flag on the gate: route deliveries paused
	w.g.dispatch(105, decisionMsg(ethAdmin, 2, 1))    // GovDAO's unpause, executed on gno.land by the stranger's own call
	for i := 0; i < 5; i++ {
		w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, uint64(40_000_000+i))) // the stranger's sends, same block
	}
	w.g.head = 110
	for i := 0; i < 4 && w.e.trusted < 106; i++ {
		w.step(3 * time.Minute)
		_ = u.tick(context.Background())
		w.tick()
	}
	if w.e.trusted != 106 {
		t.Fatalf("the decision's header was not bought: %s", statusLine(u, "plan"))
	}
	w.e.flags[4] = ethx.Flag{} // the unpause executed: the gate opens
	for i := 0; i < 3; i++ {
		w.step(30 * time.Second)
		w.tick()
		_ = u.tick(context.Background())
	}
	if n := sendsDelivered(t, w, 1, 5); n != 0 {
		t.Fatalf("%d of the stranger's 5 fee-short sends delivered at no fee check from the decision's header (%s; ledger %s)", n, w.r.last["deliver:1"], u.state.Budget.text())
	}
	if !strings.Contains(w.r.last["deliver:1"], "held") {
		t.Fatalf("a send in the decision's block: %s", w.r.last["deliver:1"])
	}
}

// E4-GS2 (b): a stranger front-runs the updater's update of a decision's block, which then proves the decision's header
// for it alone (a header proof, its group below the trusted height); the stranger's fee-short sends in that block, at
// their dispatch, are no part of it — and none is delivered at no fee check under that proof.
func TestDecisionHeaderProofBooksNoSend(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
	u := updaterOver(t, w)
	w.g.dispatch(105, decisionMsg(ethAdmin, 7, 1))
	for i := 0; i < 5; i++ {
		w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, uint64(40_000_000+i)))
	}
	w.g.head = 110
	w.e.hook = map[string]func(ethx.Call) error{"update": func(ethx.Call) error {
		w.e.trust(109) // the stranger's update lands first, above 106
		return &ethx.Revert{Name: "HeightNotAboveTrusted", Mined: true}
	}}
	for i := 0; i < 6 && len(w.e.sentOf("proveHeader")) == 0; i++ {
		w.step(3 * time.Minute)
		_ = u.tick(context.Background())
	}
	w.e.hook = nil
	if len(w.e.sentOf("proveHeader")) == 0 {
		t.Fatalf("no header proof: %s", statusLine(u, "plan"))
	}
	w.tick()
	w.step(30 * time.Second)
	w.tick()
	if w.e.admins[ethAdmin].lastSeq != 1 {
		t.Fatalf("the decision was not delivered under its header proof: %s", w.r.last["deliver:0"])
	}
	if n := sendsDelivered(t, w, 1, 5); n != 0 {
		t.Fatalf("%d of the stranger's 5 fee-short sends delivered at no fee check under the decision's header proof (%s; ledger %s)", n, w.r.last["deliver:1"], u.state.Budget.text())
	}
}

// E4-GS2's note: a decision's header bought by an update books no route send either — a stranger's fee-short sends
// beside any GovDAO decision ride it only by their own fee — and the ledger books nothing for it.
func TestDecisionHeaderCarriesSendsOnlyByTheirFee(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
	u := updaterOver(t, w)
	w.g.dispatch(105, decisionMsg(ethAdmin, 7, 1))
	for i := 0; i < 5; i++ {
		w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, uint64(40_000_000+i)))
	}
	w.g.head = 110
	for i := 0; i < 4 && w.e.trusted < 106; i++ {
		w.step(3 * time.Minute)
		_ = u.tick(context.Background())
	}
	if w.e.trusted != 106 {
		t.Fatalf("the decision's header was not bought: %s", statusLine(u, "plan"))
	}
	w.tick()
	w.step(30 * time.Second)
	w.tick()
	if n := sendsDelivered(t, w, 1, 5); n != 0 || len(u.state.Budget.Entries) != 0 {
		t.Fatalf("under the decision's header: %d fee-short sends delivered at no fee check, %d bookings (%s)", n, len(u.state.Budget.Entries), u.state.Budget.text())
	}
	// at a base fee their fees cover, the relayer delivers them from that header by their own fees
	w.e.baseFee = ethx.GweiOf(0.1)
	w.step(time.Minute)
	w.tick()
	if n := sendsDelivered(t, w, 1, 5); n != 5 {
		t.Fatalf("their own fees cover their deliveries now: %d delivered (%s)", n, w.r.last["deliver:1"])
	}
}

// E4-CF2 (b): S, proven under a stranger's header from its own small leaf and short of nothing there, is re-emitted
// by a stranger into the block whose header the updater then buys for A, beside 6.5 KB of junk events. That header
// books A, not S: S is never delivered from the stranger's leaf at no fee check, only from its own by its own fee.
func TestUnbookedSendNeverRidesAnotherSendsHeader(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.000015, "BUBBLE": 1} // 50 GNOT = 0.00075 ETH: a small leaf at ≈2 gwei, not a 7 KB one
	w.g.dispatch(101, sendMsg(testGNOT, ethGNOT, 7))                       // S, nonce 0
	w.e.trust(102)                                                         // a stranger's update proves block 101
	w.g.mu.Lock()
	w.g.results[104] = append(w.g.results[104], junkReemit(w.g.mailbox, w.g.raw(0), 6500)) // a stranger re-emits S
	w.g.mu.Unlock()
	w.g.dispatch(104, sendMsg(testGNOT, ethGNOT, 9)) // A, nonce 1
	w.g.head = 110
	u := updaterOver(t, w)
	u.state.Budget.Balance = ethx.GweiOf(100_000_000).String()
	w.step(6*time.Hour + time.Minute) // A's six hours: its header is drawn within the budget
	for i := 0; i < 4 && w.e.trusted < 105; i++ {
		if err := u.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		w.step(2 * time.Minute)
	}
	if w.e.trusted != 105 || u.state.Budget.booked(0) || !u.state.Budget.booked(1) {
		t.Fatalf("the updater's header for A: trusted %d, S booked %v, A booked %v", w.e.trusted, u.state.Budget.booked(0), u.state.Budget.booked(1))
	}
	w.tick()
	got := w.e.deliveredLeaves(t)
	if n, ok := got[0]; !ok || n > 2000 {
		t.Fatalf("S: delivered from a %d-byte leaf (%v): %s", n, ok, w.r.last["deliver:0"])
	}
}

// E4-CF2 (c), the booking forgotten: a send the updater booked is delivered from its header however long after — the
// relayer's first look past the three days of logs it reads first (a long pause, the quarantine, a relayer down), and
// a restart whose provider shows no log at all: the booking read once is kept in the relayer's state file.
func TestBookingOutlivesTheLookBackAndARestart(t *testing.T) {
	for _, restart := range []bool{false, true} {
		w := newWorld(t, 100, "primary")
		w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1} // 50 GNOT = 0.0005 ETH: short of a delivery at ≈2 gwei
		w.r.Cfg.Eth.DeployBlock = 1000
		w.r.State = filepath.Join(t.TempDir(), "relayer.json")
		w.g.dispatch(101, sendMsg(testGNOT, ethGNOT, 7)) // S, nonce 0
		w.g.head = 110
		u := updaterOver(t, w)
		u.state.Budget.Balance = ethx.GweiOf(100_000_000).String()
		w.step(6*time.Hour + time.Minute) // S's six hours: its header is drawn within the budget, booked
		for i := 0; i < 4 && w.e.trusted < 102; i++ {
			if err := u.tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			w.step(2 * time.Minute)
		}
		if w.e.trusted != 102 || !u.state.Budget.booked(0) {
			t.Fatalf("the updater did not buy and book 102: trusted %d, booked %v", w.e.trusted, u.state.Budget.booked(0))
		}
		if restart {
			// the gate is flagged: the relayer reads the booking and waits out the pause; it then restarts, and its provider
			// answers no log at all
			w.e.flags[4] = ethx.Flag{BlockNumber: 9, Time: 1}
			w.tick()
			if len(w.e.sentOf("process")) != 0 {
				t.Fatal("delivered under a flag")
			}
			r2 := &Relayer{Cfg: w.r.Cfg, Role: "primary", Alarm: &Alarmer{Source: "test"}, Sender: w.e, Eth: w.e, gno: w.g, light: w.g, blocks: w.g, runner: w.g, State: w.r.State}
			if err := r2.init(); err != nil {
				t.Fatal(err)
			}
			r2.now = func() time.Time { return w.now }
			w.r = r2
			w.e.flags[4] = ethx.Flag{}
			w.e.lagLogs = 1 << 20
		}
		// 22,000 blocks pass (three days and more) before the relayer delivers
		w.e.mu.Lock()
		w.e.block += 22_000
		w.e.mu.Unlock()
		w.tick()
		if got := w.e.deliveredLeaves(t); len(got) != 1 {
			t.Fatalf("restart %v: S, booked by the updater, not delivered after 22,000 blocks: %v (%s)", restart, got, w.r.last["deliver:0"])
		}
	}
}

// The trailer: what the updater appends to a header's calls names the sends, reads back from the calldata, and leaves
// the call's arguments as they were (the light client, and the updater's own reading of a stage, decode the same).
func TestBookingTrailerRoundTrip(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.head = 110
	sh, err := signedHeader(context.Background(), w.g, "dev", 105, nil)
	if err != nil {
		t.Fatal(err)
	}
	calls, err := ethx.UpdateCalls(sh, 0)
	if err != nil {
		t.Fatal(err)
	}
	plain := calls[0].Data
	if ns, ok := trailerNonces(plain); ok || ns != nil {
		t.Fatalf("a call without a trailer read as naming %v", ns)
	}
	want := []uint32{0, 7, 4_000_000_000}
	data := append(append([]byte{}, plain...), bookingTrailer(want)...)
	got, ok := trailerNonces(data)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("the trailer read back %v %v, want %v", got, ok, want)
	}
	m1, h1, r1, s1, err1 := ethx.HeaderCallOf(plain)
	m2, h2, r2, s2, err2 := ethx.HeaderCallOf(data)
	if err1 != nil || err2 != nil || m1 != m2 || h1 != h2 || r1 != r2 || !reflect.DeepEqual(s1, s2) {
		t.Fatalf("the call's arguments read otherwise with a trailer: %v %v", err1, err2)
	}
	if bookingTrailer(nil) != nil {
		t.Fatal("an empty booking carries a trailer")
	}
	// the mark alone, or a count past the calldata, is no trailer
	if _, ok := trailerNonces(append(append([]byte{}, plain...), bookingMark[:]...)); ok {
		t.Fatal("a mark without a count read as a trailer")
	}
	bad := append([]byte{}, data...)
	bad[len(bad)-34], bad[len(bad)-33] = 0x13, 0x88
	if _, ok := trailerNonces(bad); ok {
		t.Fatal("a count of 5,000 nonces read from a trailer of 3")
	}
}

// E4-TL8: the executor's cursor starts at HlAdmin's stored start, never at 1: a launch whose numbers start at 1,000,000
// has its mature unpause executed at the first tick, and so does a successor that inherited a high number.
func TestExecutorCursorStartsAtHlAdminsStart(t *testing.T) {
	w := newWorld(t, 100, "primary")
	a := w.e.admins[ethAdmin]
	a.startSeq, a.lastSeq = 1_000_000, 1_000_001
	a.decisions[1_000_001] = &ethx.Decision{Number: 1_000_001, Kind: 2, DeliveredTime: w.e.now - 2*86_400} // an unpause, mature
	w.tick()
	if a.decisions[1_000_001] != nil {
		t.Fatalf("a mature decision at number 1,000,001 not executed at the first tick: the cursor %d", w.r.state.AdminLow[ethAdmin.Hex()])
	}
	// a successor inherits 2,000,000: its own numbers are read from 2,000,001
	succ := common.HexToAddress("0x00000000000000000000000000000000000000e2")
	a.successor = succ
	w.e.admins[succ] = &fakeAdmin{startSeq: 2_000_000, lastSeq: 2_000_001, clockFactor: 1, decisions: map[uint64]*ethx.Decision{
		2_000_001: {Number: 2_000_001, Kind: 2, DeliveredTime: w.e.now - 2*86_400}}}
	w.advance(time.Minute)
	w.tick()
	if w.e.admins[succ].decisions[2_000_001] != nil {
		t.Fatalf("the successor's mature decision not executed at its first tick: the cursor %d (%v)", w.r.state.AdminLow[succ.Hex()], w.r.last["executes:"+succ.Hex()])
	}
}

// E4-CT3's Go half: on a chain that meters state gas (EIP-8037), a starved kind 8 is sent again with a gas limit past
// EIP-7825's cap — a reservoir for its call's state growth — never the 3,000,000 that cannot carry a state-heavy
// initializer there; on a chain without state gas the resend keeps 3,000,000.
func TestStarvedExecuteCarriesAReservoirUnderStateGas(t *testing.T) {
	params := append(kind8Params(ethGate, common.HexToAddress("0x000000000000000000000000000000000000d00d"), common.Hash{7}), 0x81, 0x29, 0xfc, 0x1c)
	w := newWorld(t, 100, "primary")
	w.e.stateGas = true
	a := w.e.admins[ethAdmin]
	a.lastSeq = 1
	a.decisions[1] = &ethx.Decision{Number: 1, Kind: 8, DeliveredTime: w.e.now - 8*86_400, Params: params}
	// a state-heavy initializer: it starves under the least that executes, and at or under EIP-7825's cap (no reservoir)
	// its state growth runs its 2,000,000 out
	w.e.hook = map[string]func(ethx.Call) error{"execute": func(c ethx.Call) error {
		switch {
		case c.MinGas < leastKind8:
			return callStarved()
		case c.MinGas <= ethx.GasCap:
			return &ethx.Revert{Name: "", Reason: "out of gas: the initializer's fresh slots found no reservoir", Mined: true}
		}
		return nil
	}}
	for i := 0; i < 4 && a.decisions[1] != nil; i++ {
		w.tick()
		w.advance(2 * time.Hour)
	}
	ex := w.e.sentOf("execute")
	if a.decisions[1] != nil || len(ex) != 2 || ex[1].MinGas != ethx.GasCap+starvedReservoir {
		var gas []uint64
		for _, c := range ex {
			gas = append(gas, c.MinGas)
		}
		t.Fatalf("a state-heavy kind 8 on a state-gas chain: executed %v, sent with %v (%s)", a.decisions[1] == nil, gas, w.r.last["execute:1"])
	}
}

// E4-RT1: the relayers' key is the backstop payer of a decision's header, so float:relayer alarms while it holds less
// than one at the emergency cap (≈0.174 ETH at gnoland-1's set) — in the band where its deliveries' runway reads weeks.
func TestRelayerFloorAlarmsTheBackstopsReserve(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.e.balances = map[common.Address]*big.Int{ethKey: big.NewInt(1e17)} // 0.1 ETH: ≈32 days of deliveries
	w.tick()
	al := w.r.Alarm.Standing()
	if _, ok := al["float:relayer"]; !ok || !strings.Contains(al["float:relayer"], "0.174 ETH") {
		t.Fatalf("0.1 ETH on the relayers' key: %v", al)
	}
	if _, ok := al["runway:relayer-eth"]; ok {
		t.Fatalf("the test's own premise: the runway alarms already (%v)", al)
	}
	w.e.balances[ethKey] = big.NewInt(4e17) // the plan's 0.4: deliveries and the header
	w.advance(floatEvery)
	w.tick()
	if _, ok := w.r.Alarm.Standing()["float:relayer"]; ok {
		t.Fatalf("0.4 ETH on the relayers' key alarmed: %v", w.r.Alarm.Standing())
	}
}

// E4-FC5: the updater's floor is a decision's header at the cap (≈0.174 ETH at gnoland-1's set), its route headers
// on top: a key funded at 0.2 ETH does not stand float:updater from its first route header.
func TestUpdaterFloorIsTheHeaderNotAFlatFloat(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.e.balances = map[common.Address]*big.Int{ethKey: new(big.Int).Sub(big.NewInt(2e17), ethx.GweiOf(4_000_000))} // 0.2 ETH less a header
	if text := w.u.floatFloor(context.Background()); w.u.Alarm.Standing()["float:updater"] != "" {
		t.Fatalf("float:updater stands after the first route header: %s", text)
	}
	w.e.balances[ethKey] = big.NewInt(17e16) // 0.17 ETH: under a header at 50 gwei
	if text := w.u.floatFloor(context.Background()); w.u.Alarm.Standing()["float:updater"] == "" || !strings.Contains(text, "under the 0.174 ETH") {
		t.Fatalf("0.17 ETH: %s (%v)", text, w.u.Alarm.Standing())
	}
}

// E4-SF5's Ethereum twin: the re-emit back-off counts the message's re-emits that landed in blocks nothing can be
// proven from — the index's marks, judged against the own node's next header, never cleared by a block whose leaves
// merely parse — so re-emits into blocks a string the node's JSON cannot carry spoils keep backing off while a stranger
// updates the client every ten minutes: no more than the back-off's own count in five hours, reemit:poisoned standing.
func TestLossyReemitsBackOff(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	poisonLossy(w, 105)
	w.g.head = 110
	for i := 0; i < 600; i++ { // five hours of 30-second steps
		runs := len(w.g.runs)
		w.tick()
		if len(w.g.runs) > runs { // the stranger's failed call lands beside each re-emit
			poisonLossy(w, w.g.head-1)
		}
		if i%20 == 19 && w.g.head-1 > w.e.trusted {
			w.e.trust(w.g.head - 1) // a stranger's update every ten minutes
		}
		w.advance(30 * time.Second)
	}
	n := len(w.g.runs)
	t.Logf("five hours of lossy re-emit blocks, a stranger's update every ten minutes: %d re-emits; reemit:poisoned %v", n, w.r.Alarm.Standing()["reemit:poisoned"] != "")
	if n < 5 || n > 11 {
		t.Fatalf("%d re-emits in five hours of lossy blocks (a minute, doubling to an hour: about nine)", n)
	}
	if _, ok := w.r.Alarm.Standing()["reemit:poisoned"]; !ok {
		t.Fatalf("no reemit:poisoned: %v", w.r.Alarm.Standing())
	}
}

// E4-GS1's Go half: GovDAO's cancel, dispatched beside a stranger's failed call whose panic echoes a string the node's
// JSON cannot carry (and the same beside every re-emit), is never parked: it is re-emitted under the back-off — hourly
// at the end, alarmed reemit:poisoned and results:mismatch — for as long as the stranger pays, and delivered once a
// re-emit lands in a block that proves.
func TestPoisonedDecisionKeepsBeingRetried(t *testing.T) {
	w := newWorld(t, 100, "primary")
	u := updaterOver(t, w)
	w.g.dispatch(105, decisionMsg(ethAdmin, 1, 1)) // GovDAO's cancel
	poisonLossy(w, 105)
	w.g.head = 110
	var lastHours []int
	for i := 0; i < 12*60; i++ { // twelve hours of one-minute steps
		runs := len(w.g.runs)
		_ = u.tick(context.Background())
		w.tick()
		if len(w.g.runs) > runs {
			poisonLossy(w, w.g.head-1)
		}
		if i >= 9*60 && i%60 == 0 {
			lastHours = append(lastHours, len(w.g.runs))
		}
		w.step(time.Minute)
	}
	lastHours = append(lastHours, len(w.g.runs))
	t.Logf("twelve hours of poisoned blocks: %d re-emits (at hours 9-12: %v); alarms %v", len(w.g.runs), lastHours, w.r.Alarm.StandingKeys())
	for k := 1; k < len(lastHours); k++ {
		if lastHours[k] == lastHours[k-1] {
			t.Fatalf("no re-emit in hour %d of the last three: %v (relayer %s)", 9+k, lastHours, w.r.last["waiting"])
		}
	}
	al := w.r.Alarm.Standing()
	for _, k := range []string{"reemit:poisoned", "results:mismatch"} {
		if _, ok := al[k]; !ok {
			t.Fatalf("no %s: %v", k, w.r.Alarm.StandingKeys())
		}
	}
	for k := range al {
		if strings.HasPrefix(k, "parked:") {
			t.Fatalf("parked: %s: %s", k, al[k])
		}
	}
	if len(w.e.sentOf("process")) != 0 || w.r.state.Out.Done["0"] != "" {
		t.Fatalf("the test's own premise: delivered or ended under the poison (%v)", w.r.state.Out.Done)
	}
	// the stranger stops: the next re-emit lands in a block that proves, and the cancel is delivered
	for i := 0; i < 90 && w.e.admins[ethAdmin].lastSeq == 0; i++ {
		_ = u.tick(context.Background())
		w.tick()
		w.step(time.Minute)
	}
	if w.e.admins[ethAdmin].lastSeq != 1 {
		t.Fatalf("the cancel not delivered once the stranger stopped: %s; %s", w.r.last["waiting"], w.r.last["deliver:0"])
	}
}

// E4-GS4, with a provider whose log index lags: until the chain's word on the header it bought is read, the updater
// judges the sends by its own pending booking — the relayer, holding the send meanwhile, re-emits it at its six hours,
// and that re-emit must not buy a second header for it — then the relayer reads the trailer and delivers it, booked.
func TestLaggingLogsBuyNoSecondHeader(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
	u := updaterOver(t, w)
	u.state.Budget.Balance = ethx.GweiOf(50_000_000).String()
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 40_000_000)) // S, nonce 0
	w.g.head = 110
	w.step(6*time.Hour + time.Minute)
	w.e.lagLogs = 1 << 20 // every log read answers empty
	_ = u.tick(context.Background())
	if w.e.trusted != 106 {
		t.Fatalf("the six-hour header not bought: %s", statusLine(u, "plan"))
	}
	for i := 0; i < 20; i++ { // ten minutes of 30-second steps
		w.tick()
		_ = u.tick(context.Background())
		w.step(30 * time.Second)
	}
	if n := len(w.e.sentOf("update")); n != 1 {
		t.Fatalf("%d headers bought for S while the logs lag (%d re-emits; updater %s)", n, len(w.g.runs), statusLine(u, "plan"))
	}
	w.e.lagLogs = 0
	for i := 0; i < 4 && len(w.e.sentOf("process")) == 0; i++ {
		w.step(time.Minute)
		_ = u.tick(context.Background())
		w.tick()
	}
	if got := w.e.deliveredLeaves(t); len(got) != 1 || len(u.state.Budget.Entries) != 1 || len(w.e.sentOf("update")) != 1 {
		t.Fatalf("once the logs show: delivered %v, %d bookings, %d headers (%s)", got, len(u.state.Budget.Entries), len(w.e.sentOf("update")), w.r.last["deliver:0"])
	}
}

// E4-CF2 (c), what the look-back costs: a machine that ran through never searches for a header's recording past the
// last three days — a log its provider's index lags on is asked for again in a minute — while one whose last pass lies
// further back (down, a new or restored state file) looks back to that pass less three days, to eth.deployBlock, or
// thirty days at most when the config names none.
func TestTheLookPastThreeDaysOnlyAfterAGap(t *testing.T) {
	w := newWorld(t, 100, "primary")
	e := w.r.env
	w.e.mu.Lock()
	w.e.block = 1_000_000
	w.e.mu.Unlock()
	for _, c := range []struct {
		name         string
		seen, deploy uint64
		wide         bool
		lo           uint64
	}{
		{"ran through", 999_990, 0, false, 0},
		{"down four days", 1_000_000 - 28_800, 0, true, 1_000_000 - 28_800 - headerLogBack},
		{"a new state file, no deploy block", 0, 0, true, 1_000_000 - wideLogBack},
		{"a new state file, the deploy block", 0, 900_000, true, 900_000},
	} {
		e.st.Seen, e.cfg.Eth.DeployBlock = c.seen, c.deploy
		e.reset()
		e.unread, e.wide = nil, nil
		w.e.ranges = nil
		if b := e.bookingOf(context.Background(), w.now, [32]byte{1}, 1, 0); b != nil {
			t.Fatalf("%s: a booking of a header never recorded: %+v", c.name, b)
		}
		r := w.e.ranges
		if len(r) < 1 || r[0] != [2]uint64{1_000_000 - headerLogBack, 1_000_000} || (len(r) == 2) != c.wide || (c.wide && r[1] != [2]uint64{c.lo, 1_000_000 - headerLogBack - 1}) {
			t.Fatalf("%s: the log reads %v", c.name, r)
		}
		w.now = w.now.Add(30 * time.Second) // within a minute: no read at all
		w.e.ranges = nil
		if e.bookingOf(context.Background(), w.now, [32]byte{1}, 1, 0); len(w.e.ranges) != 0 {
			t.Fatalf("%s: asked again within a minute: %v", c.name, w.e.ranges)
		}
	}
}

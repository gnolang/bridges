package agent

// Audit round 3's go-spend fold (lane F13a): each test fails on eb7cebb4's code and passes with the fold.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// junkReemit is a stranger's transaction re-emitting a message beside junk bytes of its own realm's events: one
// results leaf carrying the message's Dispatch and the junk.
func junkReemit(mailbox string, raw []byte, junk int) *gnolight.TxResult {
	r := dispatchTx(mailbox, raw)
	j := &gnolight.Event{Type: "Junk", PkgPath: "gno.land/r/attacker/junk", Attributes: []gnolight.Attribute{{Key: "x", Value: strings.Repeat("z", junk)}}}
	anys := [][]byte{gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(r.Events[0])), gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(j))}
	r.Events = append(r.Events, j)
	r.Leaf = gnolight.EncodeResult(nil, []byte("ok"), anys)
	return r
}

// delivered is the size of the leaf each delivered nonce was proven from, by nonce.
func (f *fakeEth) deliveredLeaves(t *testing.T) map[uint32]int {
	t.Helper()
	out := map[uint32]int{}
	for _, p := range f.sentOf("process") {
		vals, err := ethx.Mailbox.ABI.Methods["process"].Inputs.Unpack(p.Data[4:])
		if err != nil {
			t.Fatal(err)
		}
		_, _, _, _, leaf, err := ethx.MetadataOf(vals[0].([]byte))
		if err != nil {
			t.Fatal(err)
		}
		m, _ := hl.ParseMessage(vals[1].([]byte))
		out[m.Nonce] = len(leaf)
	}
	return out
}

// updaterOver is an updater on a relayer world's chains, its clock the world's.
func updaterOver(t *testing.T, w *testWorld) *Updater {
	t.Helper()
	u := &Updater{Cfg: w.r.Cfg, Sender: w.e, Eth: w.e, Alarm: &Alarmer{Source: "test"}, gno: w.g, blocks: w.g}
	if err := u.init(); err != nil {
		t.Fatal(err)
	}
	u.now = func() time.Time { return w.now }
	return u
}

// step moves a relayer world's clocks, gno's and Ethereum's.
func (w *testWorld) step(d time.Duration) {
	w.now = w.now.Add(d)
	w.e.now += uint64(d / time.Second)
}

// E3-GS2 (plan CF8(c)), with E3-GS7: a send the updater never served — a stranger's Reemit at 104 keeps its newest
// event out of the group the updater serves at 102 — is proven under the updater's header 103 from the stranger's leaf
// of ≈7.5 KB of junk events, yet is not delivered at no fee check: its own fee falls short of that delivery priced by
// its leaf's bytes, so it is held, and it waits as an unproven send does — at its six hours its own group's header is
// bought within the budget, booked, and it is delivered from its own leaf under that.
func TestRelayerDeliversOnlyWhatTheUpdaterBooked(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1} // 50 GNOT = 0.0005 ETH, short of a delivery at 2 gwei
	w.r.Cfg.Eth.Updater = ethKey.Hex()
	w.g.dispatch(101, sendMsg(testGNOT, ethGNOT, 7)) // S, nonce 0: the stranger's send
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 9)) // A, nonce 1: an honest send
	w.g.mu.Lock()
	w.g.results[102] = append(w.g.results[102], junkReemit(w.g.mailbox, w.g.raw(0), 7400))
	w.g.results[104] = append(w.g.results[104], junkReemit(w.g.mailbox, w.g.raw(0), 10))
	w.g.mu.Unlock()
	w.g.head = 110
	u := updaterOver(t, w)
	u.state.Budget.Balance = ethx.GweiOf(100_000_000).String() // 0.1 ETH of surplus: six-hour sends draw on it
	w.step(6*time.Hour + time.Minute)
	for i := 0; i < 4 && w.e.trusted < 103; i++ {
		if err := u.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		w.step(2 * time.Minute)
	}
	if w.e.trusted != 103 || u.state.Budget.booked(0) || !u.state.Budget.booked(1) {
		t.Fatalf("the updater's header for A: trusted %d, S booked %v, A booked %v", w.e.trusted, u.state.Budget.booked(0), u.state.Budget.booked(1))
	}
	w.tick()
	got := w.e.deliveredLeaves(t)
	if n, ok := got[0]; ok {
		t.Fatalf("S (never served, never booked, its fee short of its delivery) was delivered from a %d-byte leaf under the updater's header for A", n)
	}
	if _, ok := got[1]; !ok {
		t.Fatalf("A not delivered: %v", w.r.last["deliver:1"])
	}
	if !strings.Contains(w.r.last["deliver:0"], "held") {
		t.Fatalf("S's line: %s", w.r.last["deliver:0"])
	}
	// held, S waits for its own group's header: its six hours draw on the budget, and the relayer delivers it from its
	// own leaf at 104, booked
	for i := 0; i < 8 && got[0] == 0; i++ {
		w.step(2 * time.Minute)
		if err := u.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		w.tick()
		got = w.e.deliveredLeaves(t)
	}
	if got[0] == 0 || got[0] > 1000 || w.e.trusted != 105 || !u.state.Budget.booked(0) {
		t.Fatalf("S not delivered from its own group's header within the budget: leaf %d, trusted %d, booked %v (updater %v; relayer %s)", got[0], w.e.trusted, u.state.Budget.booked(0), u.status["due"], w.r.last["deliver:0"])
	}
}

// E3-RT2's second half: a stranger's transaction landing before the relayer's batch in its block, re-emitting one of
// the batch's nonces beside 9 KB of its own events, does not make that nonce's leaf its own: the proof leaf is the
// smallest carrying the id (the lowest index among equals), so the batch's leaf stands and both sends are delivered
// from it, nothing re-emitted again.
func TestProofLeafIsTheSmallest(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 1))
	w.g.dispatch(104, sendMsg(testBUBBLE, ethBUBBLE, 2))
	w.g.head = 110
	w.tick()
	if len(w.g.runs) != 1 {
		t.Fatalf("no batch: %v", w.g.runs)
	}
	b := w.g.head - 1
	w.g.mu.Lock()
	w.g.results[b] = append([]*gnolight.TxResult{junkReemit(w.g.mailbox, w.g.raw(0), 9000)}, w.g.results[b]...)
	w.g.mu.Unlock()
	w.e.trust(b + 1)
	w.advance(time.Minute)
	w.tick()
	got := w.e.deliveredLeaves(t)
	if len(got) != 2 || got[0] != got[1] || got[0] > 2000 {
		t.Fatalf("delivered from leaves %v (the batch's two-Dispatch leaf wanted; runs %v; %s)", got, w.g.runs, w.r.last["deliver:0"])
	}
	if len(w.g.runs) != 1 {
		t.Fatalf("re-emitted again: %v", w.g.runs)
	}
}

// E3-GS3: an update a stranger's landed before — ours mined behind it in one block, reverting HeightNotAboveTrusted,
// the client then trusting a header above ours — is no failure of the updater: three in a row count nothing and park
// nothing, and GovDAO's decision's header goes at once. A park the route headers earn by failures of their own never
// holds a decision's header either: those count apart.
func TestStrangersUpdateIsNoUpdaterFailure(t *testing.T) {
	w := newUpdWorld(t, 100)
	h := int64(105)
	for i := 0; i < 3; i++ {
		w.g.dispatch(h, sendMsg(testGNOT, ethGNOT, uint64(500+i)))
		w.g.head = h + 5
		stranger := h + 2
		w.e.hook = map[string]func(ethx.Call) error{"update": func(ethx.Call) error {
			w.e.trust(stranger) // the stranger's update lands first, in the same block
			return &ethx.Revert{Name: "HeightNotAboveTrusted", Mined: true}
		}}
		before := len(w.e.sentOf("update"))
		for k := 0; k < 10 && len(w.e.sentOf("update")) == before; k++ {
			w.advance(2 * time.Minute)
			_ = w.u.tick(context.Background())
		}
		if len(w.e.sentOf("update")) == before {
			t.Fatalf("round %d: no update sent (plan %q)", i, w.statusPlan())
		}
		h += 10
	}
	w.e.hook = nil
	if a := w.u.state.Attempt; a != nil && (a.Failures > 0 || a.parked()) {
		t.Fatalf("a stranger's landing counted as the updater's failure: %+v", a)
	}
	if _, ok := w.u.Alarm.Standing()["updater:parked"]; ok {
		t.Fatal("parked")
	}
	w.g.dispatch(h, decisionMsg(ethAdmin, 1, 1))
	w.g.head = h + 5
	sent := len(w.e.sentOf("update"))
	w.advance(2*time.Minute + time.Second)
	w.tick(t)
	if len(w.e.sentOf("update")) != sent+1 || w.e.trusted != h+1 {
		t.Fatalf("the decision's header waits: plan %q", w.statusPlan())
	}
	// the route headers' own failures park them, never a decision's header
	w.e.admins[ethAdmin].lastSeq = 1
	h += 10
	w.g.dispatch(h, sendMsg(testGNOT, ethGNOT, 600))
	w.g.head = h + 5
	w.e.fail["update"] = &ethx.Revert{Name: "WrongSet", Mined: true}
	for k := 0; k < 8; k++ {
		w.advance(10 * time.Minute)
		_ = w.u.tick(context.Background())
	}
	if a := w.u.state.Attempt; a == nil || !a.parked() {
		t.Fatalf("the route headers' own failures did not park them: %+v", a)
	}
	delete(w.e.fail, "update")
	w.g.dispatch(h+10, decisionMsg(ethAdmin, 1, 2))
	w.g.head = h + 15
	sent = len(w.e.sentOf("update"))
	for k := 0; k < 3 && w.e.trusted < h+11; k++ {
		w.advance(2*time.Minute + time.Second)
		_ = w.u.tick(context.Background())
	}
	if w.e.trusted < h+11 {
		t.Fatalf("the decision's header held by the route headers' park: plan %q", w.statusPlan())
	}
}

// E3-RT4: a stranger who lands a header above the updater's every time — past the send's block, then past the
// relayer's re-emit — strands nothing: the re-emitted send, below the trusted height again, is proven by a header proof
// of its block's next header (proveHeader, which no later update can pre-empt) and delivered; nothing is re-emitted
// twice. A decision below the trusted height is proven at once, never re-emitted.
func TestStrangerRacingEveryUpdateStrandsNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		msg  hl.Message
	}{{"a send", sendMsg(testGNOT, ethGNOT, 500)}, {"a decision", decisionMsg(ethAdmin, 7, 1)}} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, 100, "primary")
			u := updaterOver(t, w)
			// every update is overtaken: the chain's newest header lands first
			w.e.hook = map[string]func(ethx.Call) error{"update": func(ethx.Call) error {
				w.e.trust(w.g.head)
				return nil // ours then reverts HeightNotAboveTrusted
			}}
			w.g.dispatch(105, c.msg)
			w.g.head = 110
			for i := 0; i < 12 && len(w.e.sentOf("process")) == 0; i++ {
				w.step(3 * time.Minute)
				_ = u.tick(context.Background())
				w.tick()
				w.g.head += 2 // the chain moves on
			}
			if len(w.e.sentOf("process")) != 1 {
				t.Fatalf("stranded: %d deliveries after a stranger's every update (%d updates, %d header proofs, %d re-emits: %v; updater %q; relayer %s)", len(w.e.sentOf("process")), len(w.e.sentOf("update")), len(w.e.sentOf("proveHeader")), len(w.g.runs), w.g.runs, statusLine(u, "plan"), w.r.last["waiting"])
			}
			if len(w.e.sentOf("proveHeader")) != 1 || len(w.g.runs) > 1 {
				t.Fatalf("%d header proofs, %d re-emits: %v", len(w.e.sentOf("proveHeader")), len(w.g.runs), w.g.runs)
			}
			if c.msg.Recipient == ethx.AddressTo32(ethAdmin) && len(w.g.runs) != 0 {
				t.Fatalf("a decision re-emitted: %v", w.g.runs)
			}
		})
	}
}

func statusLine(u *Updater, k string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	s, _ := u.status[k].(string)
	return s
}

// E3-GS5: an update still pending when the sender stops waiting — mined a moment later and delivered from — is booked
// once the chain shows it, so a 0.01 ETH budget never pays two six-hour headers; and its booking survives a restart
// before the state's save (made and saved before the update was sent).
func TestPendingUpdateIsBooked(t *testing.T) {
	for _, restart := range []bool{false, true} {
		w := newWorld(t, 100, "primary")
		w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
		w.r.Cfg.Eth.Updater = ethKey.Hex()
		w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 9))
		w.g.head = 110
		u := updaterOver(t, w)
		u.State = filepath.Join(t.TempDir(), "updater.json")
		u.state.Budget.Balance = ethx.GweiOf(10_000_000).String() // 0.01 ETH: one six-hour header's worth here
		if err := u.save(); err != nil {
			t.Fatal(err)
		}
		w.step(6*time.Hour + time.Minute)
		w.e.pendNth["update"] = 1 // the update outlives Send's wait, mined a moment later
		_ = u.tick(context.Background())
		w.e.pendNth = map[string]int{}
		_ = w.e.Settle(context.Background())
		if restart { // a stop before the step's save: the next run reads the file as it stood
			u = &Updater{Cfg: w.r.Cfg, Sender: w.e, Eth: w.e, State: u.State, Alarm: &Alarmer{Source: "test"}, gno: w.g, blocks: w.g}
			if err := u.init(); err != nil {
				t.Fatal(err)
			}
			u.now = func() time.Time { return w.now }
		}
		w.tick() // the relayer delivers the send from the updater's header
		if w.e.trusted != 103 || len(w.e.sentOf("process")) != 1 {
			t.Fatalf("restart %v: trusted %d, %d deliveries", restart, w.e.trusted, len(w.e.sentOf("process")))
		}
		// a second six-hour send: the surplus the first spent is not there to draw on
		w.g.dispatch(120, sendMsg(testGNOT, ethGNOT, 11))
		w.g.head = 125
		w.step(6*time.Hour + time.Minute)
		for i := 0; i < 3 && w.e.trusted < 121; i++ {
			w.step(3 * time.Minute)
			_ = u.tick(context.Background())
		}
		if w.e.trusted == 121 || len(u.state.Budget.Entries) != 1 || !u.state.Budget.booked(0) {
			t.Fatalf("restart %v: the first header unbooked or the second bought on the same surplus: trusted %d, %s", restart, w.e.trusted, u.state.Budget.text())
		}
	}
	// an update the simulation refused sent nothing: its booking stands only while a step asks for its header again
	w := newUpdWorld(t, 100)
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.e.fail["update"] = revert("WrongSet", "")
	w.advance(10 * time.Minute)
	_ = w.u.tick(context.Background())
	if len(w.u.state.Budget.Pending) != 1 || !w.u.state.Budget.booked(0) {
		t.Fatalf("no pending booking made before the send: %+v", w.u.state.Budget)
	}
	delete(w.e.fail, "update")
	w.e.delivered[w.g.idOf(0)] = true // delivered by anyone: nothing asks for the header now
	w.advance(16 * time.Minute)
	w.tick(t)
	if len(w.u.state.Budget.Pending) != 0 || w.u.state.Budget.booked(0) || len(w.e.sentOf("update")) != 1 {
		t.Fatalf("a refused update's booking stands: %+v", w.u.state.Budget)
	}
}

// E3-GS6: the updater buys a header only for the decision copy HlAdmin takes next, its predecessor recorded at the
// finalized block, and only once a number: copies anyone mints of a number HlAdmin cannot take yet buy nothing, a
// second copy of a number it served buys nothing, and a number whose predecessor is recorded at latest alone waits
// for the finalized block.
func TestDecisionCopiesBuyOneHeader(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.e.admins[ethAdmin].lastSeq = 0 // decision 1 is stuck somewhere; r/hlgov dispatched 2 already
	h := int64(105)
	for i := 0; i < 5; i++ {
		w.g.dispatch(h, decisionMsg(ethAdmin, 7, 2)) // a stranger's Redispatch(2)
		w.g.head = h + 3
		w.advance(2*time.Minute + time.Second)
		w.tick(t)
		h += 5
	}
	if n := len(w.e.sentOf("update")); n != 0 {
		t.Fatalf("%d headers bought for copies of a decision HlAdmin cannot take", n)
	}
	// two copies of the next number in two blocks: one header
	w2 := newUpdWorld(t, 100)
	w2.u.BatchWait = time.Second
	w2.g.dispatch(105, decisionMsg(ethAdmin, 7, 1))
	w2.g.dispatch(108, decisionMsg(ethAdmin, 7, 1))
	w2.g.head = 112
	for i := 0; i < 6; i++ {
		w2.advance(2*time.Minute + time.Second)
		w2.tick(t)
	}
	if n := len(w2.e.sentOf("update")); n != 1 {
		t.Fatalf("%d headers bought for two copies of one number", n)
	}
	// its predecessor recorded at latest alone: the next number waits for the finalized block
	w3 := newUpdWorld(t, 100)
	zero := uint64(0)
	w3.e.admins[ethAdmin].lastSeq, w3.e.admins[ethAdmin].finalLastSeq = 1, &zero
	w3.g.dispatch(105, decisionMsg(ethAdmin, 7, 2))
	w3.g.head = 110
	w3.advance(10 * time.Minute)
	w3.tick(t)
	if n := len(w3.e.sentOf("update")); n != 0 {
		t.Fatal("a header bought for a number whose predecessor is not final")
	}
	one := uint64(1)
	w3.e.admins[ethAdmin].finalLastSeq = &one
	w3.advance(time.Minute)
	w3.tick(t)
	if n := len(w3.e.sentOf("update")); n != 1 {
		t.Fatalf("%d headers once the predecessor is final", n)
	}
}

// E3-GS7: a send proven under a stranger's header whose own fee does not cover its delivery stays under the six-hour
// rule and delivery:age: the updater alarms it at seven hours, the relayer re-emits it (its newest event's header is
// accepted already: it needs a fresh one) and the updater buys that block's header within the budget; and an empty log
// answer from a lagging provider is no "not the updater's header" for good.
func TestHeldSendStaysUnderTheSixHourRule(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
	w.r.Cfg.Eth.Updater = ethKey.Hex()
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.e.trust(103) // a stranger's update: the send is proven, under a header that pays nothing for it
	u := updaterOver(t, w)
	w.step(time.Hour)
	_ = u.tick(context.Background())
	w.tick()
	if len(w.e.sentOf("process")) != 0 {
		t.Fatal("delivered from a stranger's header at a loss")
	}
	w.step(6*time.Hour + time.Minute) // seven hours past the dispatch block's time
	if err := u.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := u.Alarm.Standing()["delivery:age"]; !ok {
		t.Fatalf("seven hours and no delivery:age for a held send: %v", u.Alarm.Standing())
	}
	u.state.Budget.Balance = ethx.GweiOf(10_000_000).String() // a surplus to draw on
	for i := 0; i < 8 && len(w.e.sentOf("process")) == 0; i++ {
		w.step(2 * time.Minute)
		w.tick()
		_ = u.tick(context.Background())
	}
	w.tick()
	if len(w.e.sentOf("process")) != 1 || len(w.g.runs) != 1 || !u.state.Budget.booked(0) {
		t.Fatalf("the held send: %d deliveries, %d re-emits, booked %v (relayer %s; updater %v)", len(w.e.sentOf("process")), len(w.g.runs), u.state.Budget.booked(0), w.r.last["waiting"], u.status["due"])
	}
	// the updater's own header, read first through a provider whose log index lags: held a moment, then delivered
	w2 := newWorld(t, 100, "primary")
	w2.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
	w2.r.Cfg.Eth.Updater = ethKey.Hex()
	w2.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 500))
	w2.g.head = 110
	u2 := updaterOver(t, w2)
	u2.state.Budget.Balance = ethx.GweiOf(10_000_000).String()
	w2.step(6*time.Hour + time.Minute)
	_ = u2.tick(context.Background())
	if w2.e.trusted != 103 {
		t.Fatalf("the six-hour header not bought: trusted %d (%v)", w2.e.trusted, u2.status["due"])
	}
	w2.e.lagLogs = 1
	w2.g.runFail = errors.New("fake: the gno key is out of gas") // no fresh event: the send waits on header 103 alone
	w2.tick()
	if len(w2.e.sentOf("process")) != 0 {
		t.Fatal("the lagging answer was not met (the test's own premise)")
	}
	w2.step(2 * time.Minute)
	w2.tick()
	if len(w2.e.sentOf("process")) != 1 {
		t.Fatalf("a lagging provider's empty answer turned the updater's header into a stranger's: %s", w2.r.last["deliver:0"])
	}
}

// E3-RT7: the relayers' backstop times a decision from its dispatch, which no stranger's Reemit moves: re-emitted every
// four minutes, the cancel still has its header bought five minutes after it was dispatched while the updater cannot pay.
func TestBackstopTimesADecisionFromItsDispatch(t *testing.T) {
	w := newWorld(t, 100, "primary")
	upd := common.HexToAddress("0x000000000000000000000000000000000000eeee")
	w.r.Cfg.Eth.Updater = upd.Hex()
	w.e.balances = map[common.Address]*big.Int{upd: big.NewInt(1e15)} // the updater's float short
	w.g.mu.Lock()
	hNow := int64(w.now.Sub(w.g.genesis) / (3 * time.Second))
	w.g.mu.Unlock()
	w.g.dispatch(hNow, decisionMsg(ethAdmin, 1, 1)) // a cancel, dispatched now
	w.g.head = hNow + 2
	for m := 4; m <= 12 && w.e.admins[ethAdmin].lastSeq == 0; m += 4 {
		w.advance(4 * time.Minute)
		w.g.mu.Lock()
		h := int64(w.now.Sub(w.g.genesis) / (3 * time.Second))
		w.g.mu.Unlock()
		w.g.reemitAt(h, 0) // a stranger re-emits it: its newest event is a moment old
		w.g.head = h + 2
		w.tick()
		w.advance(time.Minute)
		w.tick()
	}
	if w.e.admins[ethAdmin].lastSeq != 1 {
		t.Fatalf("the cancel's header never bought while a stranger re-emits it: %d updates (%v)", len(w.e.sentOf("update")), w.r.last)
	}
}

// E3-AS1: gnoland-1 refuses a MsgRun from the relayer's key (run_submitters): the batch re-emit is one transaction of
// Reemit MsgCalls, which it takes.
func TestBatchReemitIsMsgCalls(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 1))
	w.g.dispatch(104, sendMsg(testBUBBLE, ethBUBBLE, 2))
	w.g.head = 110
	w.tick()
	if strings.Contains(w.r.last["reemit:batch"], "failed") || len(w.g.runs) != 1 || !strings.Contains(w.g.runs[0], "Reemit(cross, 0)") || !strings.Contains(w.g.runs[0], "Reemit(cross, 1)") {
		t.Fatalf("the batch: %v (%s)", w.g.runs, w.r.last["reemit:batch"])
	}
	at := w.g.head - 1
	w.e.trust(at + 1)
	w.advance(time.Minute)
	w.tick()
	if n := len(w.e.sentOf("process")); n != 2 {
		t.Fatalf("%d deliveries under the batch's header", n)
	}
}

// E3-GS11, E3-FC7: eth.prices carry the date they were set, and prices:stale stands while they are undated or older
// than a week; the updater's float floor prices a decision's header at the current set's size — a set of 20 makes
// 0.2 ETH short of one at the 50-gwei cap — never at six validators whatever the set. E3-CF8(b): the updater's
// planned rate is the model's header, so the plan's 0.2 ETH reads weeks, not 5.6 days, from launch.
func TestPricesDatedAndTheFloatsPriced(t *testing.T) {
	for _, c := range []struct {
		at    string
		stale bool
	}{{"", true}, {"2026-01-01", true}, {"fresh", false}} {
		w := newUpdWorld(t, 100)
		at := c.at
		if at == "fresh" {
			at = w.now.Add(-24 * time.Hour).UTC().Format("2006-01-02")
		}
		if err := json.Unmarshal([]byte(`{"eth":{"pricesAt":"`+at+`"}}`), w.u.Cfg); err != nil {
			t.Fatal(err)
		}
		w.u.now = func() time.Time { return w.now }
		w.tick(t)
		if _, ok := w.u.Alarm.Standing()["prices:stale"]; ok != c.stale {
			t.Fatalf("eth.pricesAt %q: prices:stale %v, want %v (%v)", at, ok, c.stale, w.u.Alarm.Standing())
		}
	}
	w := newUpdWorld(t, 100)
	powers := make([]int64, 20)
	for i := range powers {
		powers[i] = 60
	}
	w.g.mu.Lock()
	w.g.sets = map[int64]*fakeSet{1: newFakeSet(1, powers...)}
	w.g.headers = map[int64]*gnolight.Block{}
	w.g.mu.Unlock()
	w.e.trust(100)
	w.e.balances = map[common.Address]*big.Int{ethKey: big.NewInt(2e17)} // the plan's 0.2 ETH
	w.tick(t)
	text := w.u.floatFloor(context.Background())
	if _, ok := w.u.Alarm.Standing()["float:updater"]; !ok {
		t.Fatalf("0.2 ETH against a 20-validator set's header at the 50-gwei cap: %s", text)
	}
	var f floatWatch
	a := &Alarmer{Source: "test"}
	f.watch(context.Background(), w.now, "updater-eth", func(context.Context) (*big.Int, error) { return big.NewInt(2e17), nil }, nil, a)
	if _, ok := a.Standing()["runway:updater-eth"]; ok {
		t.Fatalf("the plan's 0.2 ETH reads under seven days at the planned rate: %v", a.Standing())
	}
}

// ---------------------------------------------------------------- the shared results fix, the relayer half --

// unbuildable is a transaction result no leaf can be rebuilt for: an error type gnolight has no schema for (a chain
// upgrade's, or a stranger's string the node's JSON cannot carry, as far as the relayer can tell).
func unbuildable() *gnolight.TxResult {
	return &gnolight.TxResult{Err: &gnolight.SchemaError{Kind: "error", TypeURL: "/vm.SomeNewError"}}
}

// E3-RT2/GS1, the relayer half: a message whose re-emits keep landing in blocks nothing can be proven from — a
// stranger's transaction beside each — re-emits again only after a back-off of its own (a minute, doubling, at most an
// hour) under reemit:poisoned, so whoever poisons every block cannot drain the relayer's GNOT; the type with no schema
// is named (results:schema:<type>).
func TestPoisonedReemitsBackOff(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.mu.Lock()
	w.g.results[105] = append(w.g.results[105], unbuildable()) // its dispatch's block poisoned too: no usable event
	w.g.mu.Unlock()
	w.g.head = 110
	for i := 0; i < 60; i++ { // half an hour of 30-second steps
		runs := len(w.g.runs)
		w.tick()
		if len(w.g.runs) > runs { // every re-emit lands beside a stranger's poison
			w.g.mu.Lock()
			b := w.g.head - 1
			w.g.results[b] = append(w.g.results[b], unbuildable())
			w.g.mu.Unlock()
		}
		w.advance(31 * time.Second)
	}
	if n := len(w.g.runs); n < 3 || n > 7 {
		t.Fatalf("%d re-emits in half an hour of poisoned blocks (a minute, doubling: about six)", n)
	}
	al := w.r.Alarm.Standing()
	if _, ok := al["reemit:poisoned"]; !ok {
		t.Fatalf("no reemit:poisoned: %v", al)
	}
	if _, ok := al["results:schema:/vm.SomeNewError"]; !ok {
		t.Fatalf("the type with no schema is not named: %v", al)
	}
}

// E3-RT2/GS1, the relayer half: a block whose rebuilt leaves do not hash to the next header's LastResultsHash (a string
// the node's JSON cannot carry) proves nothing — marked unbuildable, alarmed results:mismatch — and its message is
// re-emitted into a later block, never parked; under that block's header it is delivered.
func TestResultsMismatchAsksAReemit(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.e.trust(106) // header 106 hashes block 105's results as the chain has them
	w.g.mu.Lock()
	w.g.results[105][0] = junkReemit(w.g.mailbox, w.g.raw(0), 10) // the node's JSON gives other leaves than the chain hashed
	w.g.mu.Unlock()
	w.tick()
	if len(w.e.sentOf("process")) != 0 {
		t.Fatal("delivered from leaves the header does not carry")
	}
	if _, ok := w.r.Alarm.Standing()["results:mismatch"]; !ok {
		t.Fatalf("no results:mismatch: %v (%s)", w.r.Alarm.Standing(), w.r.last["deliver:0"])
	}
	for i := 0; i < 3 && len(w.g.runs) == 0; i++ {
		w.advance(31 * time.Second)
		w.tick()
	}
	if len(w.g.runs) != 1 {
		t.Fatalf("not re-emitted (%s; %s)", w.r.last["deliver:0"], w.r.last["waiting"])
	}
	if a := w.r.state.Attempts["deliver:0"]; a != nil && a.Failures > 0 {
		t.Fatalf("counted toward a park: %+v", a)
	}
	w.e.trust(w.g.head)
	w.advance(time.Minute)
	w.tick()
	if len(w.e.sentOf("process")) != 1 {
		t.Fatalf("not delivered under the re-emit's header: %s", w.r.last["deliver:0"])
	}
}

// The booking's log line keeps its kind alone in the parentheses — "booked (bought by its sends' fees): …" — which the
// e2e reads (local/e2e.sh): the pending booking (E3-GS5) carries the due rule's words apart.
func TestBookingLogLine(t *testing.T) {
	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	w := newUpdWorld(t, 100)
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.tick(t)
	if w.e.trusted != 106 || !strings.Contains(buf.String(), "header 106 booked (bought by its sends' fees): 1 send(s)") {
		t.Fatalf("trusted %d; the log: %s", w.e.trusted, buf.String())
	}
}

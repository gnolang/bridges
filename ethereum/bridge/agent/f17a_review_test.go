package agent

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// Lane F17a: the independent review of its first commits (R2, R3, R5, R6, R7, R8), each a test that fails on those
// commits (39529f0f) and passes after.

// R2: the updater's header proof of S's newest event below the trusted height (an old group, E3-RT4) waits in the pool;
// a stranger re-emits S five times above the trusted height meanwhile, so 108 is neither S's dispatch nor among its
// newest — and the proof's header unrecorded at the finalized block and at latest. The relayer's compaction dropped
// 108 from its index for good: the proof mined, the updater saw S booked and bought nothing, the relayer never read 108
// again. The walk reads a message's newest event below the trusted height every step, and never compacts what it reads.
func TestF17aProofInFlightKeepsItsEvent(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
	u := updaterOver(t, w)
	u.state.Budget.Balance = ethx.GweiOf(50_000_000).String()
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 40_000_000)) // S
	w.g.reemitAt(108, 0)
	w.g.head = 122
	w.e.trust(120)                // a stranger's update past S's events
	w.chainStep(13 * time.Minute) // … final
	w.step(6 * time.Hour)
	for i := 0; i < 3; i++ { // the scans reach the head
		w.tick()
		w.step(3 * time.Second)
	}
	w.e.pendNth["proveHeader"] = len(w.e.sentOf("proveHeader")) + 1 // the proof waits in the pool
	if err := u.tick(context.Background()); err == nil || len(w.e.sentOf("proveHeader")) != 1 {
		t.Fatalf("the updater's header proof of 109: %v, %d sent (plan: %s)", err, len(w.e.sentOf("proveHeader")), statusLine(u, "plan"))
	}
	for h := int64(123); h < 128; h++ {
		w.g.reemitAt(h, 0) // a stranger's re-emits above the trusted height
	}
	w.e.flags[4] = ethx.Flag{BlockNumber: 9, Time: 1} // deliveries wait, so the relayer's walk keeps S
	for i := 0; i < 2*60; i++ {                       // two hours of the relayer's steps
		w.tick()
		w.chainStep(time.Minute)
	}
	evs := w.r.state.Out.Index.Of(0, 105)
	_ = w.e.Settle(context.Background()) // the proof mines
	w.e.flags[4] = ethx.Flag{}
	for i := 0; i < 5; i++ {
		_ = u.tick(context.Background())
		w.tick()
		w.chainStep(time.Minute)
	}
	got := w.e.deliveredLeaves(t)
	t.Logf("the relayer's index of S while the proof waited: %v; then delivered %v, %d headers bought", evs, got, headersBought(w))
	if len(got) != 1 || headersBought(w) != 1 {
		t.Fatalf("S after its header proof waited in the pool: delivered %v, %d headers bought (%s)", got, headersBought(w), w.r.last["deliver:0"])
	}
}

// R3: one read by a provider's backend a block behind — no log and the recording transaction's receipt not found —
// deleted the updater's own provisional view of its header; S, ledger-booked, was then held on its own fee and the
// six-hour draw bought its header again. A miss above the finalized block is no verdict.
func TestF17aLaggingMissKeepsTheProvisionalBooking(t *testing.T) {
	w, u := sixHourSend(t)
	if err := u.tick(context.Background()); err != nil || !u.state.Budget.settled(0) {
		t.Fatalf("S's six-hour header: %v, %s", err, u.state.Budget.text())
	}
	var tx common.Hash
	for _, l := range w.e.logs {
		if l.Topics[0] == ethx.LightClient.Event("HeaderRecorded") {
			tx = l.TxHash
		}
	}
	w.g.reemitAt(115, 0) // a stranger's re-emit after the header, above what the scans read: S's newest event, above the trusted height
	w.g.head = 118
	w.e.mu.Lock()
	w.e.lagLogs = 3                               // the backend a block behind: no log …
	w.e.hideReceipts = map[common.Hash]int{tx: 3} // … and no receipt
	w.e.mu.Unlock()
	for i := 0; i < 4; i++ {
		w.step(2 * time.Minute)
		_ = u.tick(context.Background())
	}
	t.Logf("four steps after a lagging miss: %d header(s) bought; S's events %v; plan: %s", headersBought(w), u.state.Out.Index.Of(0, 105), statusLine(u, "plan"))
	if headersBought(w) != 1 {
		t.Fatalf("a lagging miss deleted the updater's view of its own header: %d headers bought for S", headersBought(w))
	}
}

// R5: the cadence counted a Claim by the route's fee alone — GovDAO may lower the fee to its floor, and the floor to 0:
// at a fee of 0 every sliver counted and the refill was nothing, one Claim a tick, E5-GS1 whole again. The unit is the
// fee and at least a five-hundredth of the cap.
func TestF17aClaimUnitHoldsAtAZeroFee(t *testing.T) {
	clock := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	r, h := claimModelRig(t, &clock, false)
	h.m.fee = 0
	h.m.credit("g1fresh", 1_000*gnotUnit, clock.Unix(), true)
	done := runClaims(r, h, &clock, 24*time.Hour, "g1fresh")
	t.Logf("a fee of 0: %d Claim calls, the claim paid in full after %s", h.calls, done.Round(time.Minute))
	if done == 0 || h.calls > 150 {
		t.Fatalf("a fee of 0: %d Claim calls (paid after %s)", h.calls, done)
	}
}

// R5's second half: a batch whose claims it finished — each paid its rest, less than the fee — counted as slivers and
// backed the route off: claims of 30 GNOT each (above the fee of 20, below twice it) to recipients whose first
// installment came already are finished in one pass, and the next batch follows at once.
func TestF17aFinishedClaimsCountAsPaying(t *testing.T) {
	clock := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	r, h := claimModelRig(t, &clock, false)
	for i := 0; i < 30; i++ {
		to := "g1rest" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		h.m.credit(to, 30*gnotUnit, clock.Unix(), false)
		h.m.mu.Lock()
		rc := h.m.recips[to]
		rc.open[0].paid = 20 * gnotUnit // its first installment, the fee, paid already: 10 GNOT left
		h.m.mu.Unlock()
	}
	runClaims(r, h, &clock, time.Minute, "")
	if open := h.m.openClaims(); open != 0 {
		t.Fatalf("30 claims of 10 GNOT left each: %d still open after a minute, %d Claim calls (%s)", open, h.calls, h.notes["claim:GNOT"])
	}
}

// R6: a provider could name, as a header's recording, a transaction of eth.updater's key that carried the header and
// reverted (a stranger recorded it first) — the relayer then booked its trailer's sends. The recording's receipt must
// show it: successful, its log the header's, in the log's block.
func TestF17aRevertedCarrierBooksNothing(t *testing.T) {
	w, u := sixHourSend(t)
	if err := u.tick(context.Background()); err != nil || w.e.trusted != 106 {
		t.Fatalf("S's six-hour header: %v, trusted %d", err, w.e.trusted)
	}
	w.e.mu.Lock()
	for h, rc := range w.e.receipts {
		c := *rc
		c.Status, c.Logs = types.ReceiptStatusFailed, nil // as a reverted carrier's receipt reads
		w.e.receipts[h] = &c
	}
	w.e.mu.Unlock()
	w.tick()
	if got := w.e.deliveredLeaves(t); len(got) != 0 {
		t.Fatalf("a reverted transaction named as the recording booked S: delivered %v", got)
	}
}

// R7: the scan gate held every route header while the scan was behind — one block the node does not serve stopped them
// all. A send whose blocks the scan read is planned; only one above it waits.
func TestF17aScanGateHoldsOnlyWhatItHasNotRead(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 7)) // S: its fee covers its header (the test config's prices)
	w.g.head = 110
	u := updaterOver(t, w)
	stuck := &stuckAbove{fakeGno: w.g, from: 108}
	u.blocks, u.env.blocks = stuck, stuck
	w.g.dispatch(300, sendMsg(testGNOT, ethGNOT, 9)) // S2 above a block the node will not serve (108 has a send's transaction)
	w.g.mu.Lock()
	w.g.results[108] = w.g.results[105]
	w.g.head = 305
	w.g.mu.Unlock()
	for i := 0; i < 3; i++ {
		_ = u.tick(context.Background())
		w.step(3 * time.Minute)
	}
	t.Logf("the scan at %d of %d: trusted %d; %s", u.state.Out.Index.Scanned, w.g.head, w.e.trusted, statusLine(u, "plan"))
	if w.e.trusted < 106 {
		t.Fatalf("a send the scan has read got no header while a block above it does not read: trusted %d", w.e.trusted)
	}
}

// stuckAbove is a gno node whose block results from a height on do not read.
type stuckAbove struct {
	*fakeGno
	from int64
}

func (s *stuckAbove) BlockResults(ctx context.Context, h int64) ([]*gnolight.TxResult, error) {
	if h >= s.from {
		return nil, errFake("the node does not serve this block's results")
	}
	return s.fakeGno.BlockResults(ctx, h)
}

// R8: eth:logs was cleared by any read that answered — a shallow one after a deep one failed — and raised again by the
// next deep one: a provider that keeps no three days of logs posted an alarm and a clear by turns. It clears on a read
// reaching as far back as the one that failed.
func TestF17aLogAlarmClearsOnlyAsDeep(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.e.mu.Lock()
	w.e.block = 100_000
	w.e.mu.Unlock()
	refused := 0
	se := shallowEth{fakeEth: w.e, depth: 10_000, refused: &refused}
	w.r.Eth, w.r.env.eth = se, se
	e := w.r.env
	if _, _, err := e.lookBooking(context.Background(), [32]byte{7}, 1, 50_000, 100_000); err == nil {
		t.Fatal("a read past the provider's depth answered")
	}
	if _, _, err := e.lookBooking(context.Background(), [32]byte{7}, 1, 99_000, 100_000); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.r.Alarm.Standing()["eth:logs"]; !ok {
		t.Fatalf("a shallow read cleared the deep one's alarm: %v", w.r.Alarm.StandingKeys())
	}
}

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

type updWorld struct {
	g   *fakeGno
	e   *fakeEth
	u   *Updater
	now time.Time
}

func newUpdWorld(t *testing.T, trusted int64) *updWorld {
	t.Helper()
	w := newWorld(t, trusted, "primary") // the same chains and renders
	uw := &updWorld{g: w.g, e: w.e, now: w.now}
	uw.u = &Updater{Cfg: testConfig(), Sender: w.e, Eth: w.e, Alarm: &Alarmer{Source: "test"}, gno: w.g, blocks: w.g}
	if err := uw.u.init(); err != nil {
		t.Fatal(err)
	}
	uw.u.now = func() time.Time { return uw.now }
	return uw
}

func (w *updWorld) tick(t *testing.T) {
	t.Helper()
	if err := w.u.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

func (w *updWorld) advance(d time.Duration) {
	w.now = w.now.Add(d)
	w.e.now += uint64(d / time.Second)
}

// A dispatch waiting in one block above the trusted height buys the header after it — never inside the 2-minute
// spacing — under the normal cap; a decision's under the emergency cap; a stranger's dispatch and a decision copy
// HlAdmin recorded already buy nothing.
func TestUpdaterBuysTheSharedBlock(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.g.dispatch(105, sendMsg("gno.land/r/stranger", ethGNOT, 1)) // not ours
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.tick(t)
	if n := len(w.e.sentOf("update")); n != 0 {
		t.Fatalf("an update bought for a stranger's dispatch: %d", n)
	}
	w.g.dispatch(112, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 115
	w.tick(t)
	ups := w.e.sentOf("update")
	if len(ups) != 1 || w.e.trusted != 113 || ups[0].MaxBaseFee != nil {
		t.Fatalf("%d updates, trusted %d", len(ups), w.e.trusted)
	}
	// a decision: the emergency cap; inside the spacing only an announcing header goes
	w.g.dispatch(116, decisionMsg(ethAdmin, 7, 1))
	w.g.head = 120
	w.advance(30 * time.Second)
	w.tick(t)
	if len(w.e.sentOf("update")) != 1 || !strings.Contains(w.statusPlan(), "spacing") {
		t.Fatalf("an update inside the spacing (%s)", w.statusPlan())
	}
	w.advance(2 * time.Minute)
	w.tick(t)
	ups = w.e.sentOf("update")
	if len(ups) != 2 || w.e.trusted != 117 || ups[1].MaxBaseFee == nil {
		t.Fatalf("%d updates, trusted %d (plan %q, due %v)", len(ups), w.e.trusted, w.statusPlan(), w.u.status["due"])
	}
	// a second copy of decision 1, recorded already, buys nothing
	w.e.admins[ethAdmin].lastSeq = 1
	w.g.dispatch(125, decisionMsg(ethAdmin, 7, 1))
	w.g.head = 130
	w.advance(5 * time.Minute)
	w.tick(t)
	if len(w.e.sentOf("update")) != 2 {
		t.Fatal("an update bought for a recorded decision's copy")
	}
}

func (w *updWorld) statusPlan() string {
	w.u.mu.Lock()
	defer w.u.mu.Unlock()
	s, _ := w.u.status["plan"].(string)
	return s
}

// Under the budget's rule (§5.5, budget.go; E2-RT2, E2-RT4) a send whose own fee does not cover its header and its
// delivery waits — at its six hours too while the budget holds nothing, and seven hours alarm delivery:age — and buys
// its header within the budget once the ledger's surplus covers what its fee lacks, the booking entered in the ledger;
// with nothing waiting, nothing is bought: no idle heartbeat.
func TestUpdaterFeeRuleAndHeartbeat(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.u.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.0000001}
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.advance(time.Hour)
	w.tick(t)
	if len(w.e.sentOf("update")) != 0 {
		t.Fatal("an update its fees do not cover")
	}
	w.advance(5*time.Hour + time.Minute) // six hours old; the budget holds nothing
	w.tick(t)
	if len(w.e.sentOf("update")) != 0 || !strings.Contains(fmt.Sprint(w.u.status["due"]), "the budget (0.000000 ETH) does not cover what its fees lack") {
		t.Fatalf("a six-hour send bought its header with nothing in the budget (due %v)", w.u.status["due"])
	}
	w.advance(time.Hour)
	w.tick(t)
	if _, ok := w.u.Alarm.Standing()["delivery:age"]; !ok {
		t.Fatalf("seven hours and no delivery:age alarm: %v", w.u.Alarm.Standing())
	}
	// the budget holds a surplus (0.01 ETH): the seven-hour send's header is bought, drawn on it, and booked
	w.u.state.Budget.Balance = ethx.GweiOf(10_000_000).String()
	w.tick(t)
	if len(w.e.sentOf("update")) != 1 || w.e.trusted != 106 {
		t.Fatalf("the send's header was not bought within the budget: trusted %d (due %v)", w.e.trusted, w.u.status["due"])
	}
	b := w.u.state.Budget
	if len(b.Entries) != 1 || b.Entries[0].Header != 106 || fmt.Sprint(b.Entries[0].Nonces) != "[0]" || b.Entries[0].HeaderCost == "" || !strings.Contains(b.Entries[0].Why, "drawn on the budget") || !b.booked(0) {
		t.Fatalf("the booking: %+v", b)
	}
	// fees 5e-6 ETH credited; the header (4.1M gas at 2 gwei, its receipt) and the delivery (311,300 at 2 gwei) debited
	if want := "1182400000000000"; b.Balance != want {
		t.Fatalf("the balance after the booking %s, want %s", b.Balance, want)
	}
	w.tick(t) // the next look: the send is proven now, no longer waiting
	if _, ok := w.u.Alarm.Standing()["delivery:age"]; ok {
		t.Fatal("delivery:age stands for a send whose header is bought")
	}
	// nothing waits: nothing is bought, however long
	w.e.delivered[w.g.idOf(0)] = true
	w.advance(7 * time.Hour)
	w.g.head = 300
	w.tick(t)
	if len(w.e.sentOf("update")) != 1 {
		t.Fatalf("an update with nothing waiting (plan %q)", w.statusPlan())
	}
}

// A validator change: the announcing header goes at once — inside the spacing, under the emergency cap — with the
// set it announces; then the first header of the new set when something asks; an announced change not followed for
// 30 minutes alarms.
func TestUpdaterAnnouncing(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.g.sets[150] = newFakeSet(2, 60, 60, 60, 60, 60, 60, 60)
	w.g.head = 160
	w.tick(t) // within seconds of the last update: the spacing would hold anything else
	ups := w.e.sentOf("update")
	if len(ups) != 1 || w.e.trusted != 149 || ups[0].MaxBaseFee == nil || w.e.nextSetHash == ([32]byte{}) {
		t.Fatalf("%d updates, trusted %d", len(ups), w.e.trusted)
	}
	// the announcing header started the quarantine: a send waits for its end and buys no header
	w.g.dispatch(162, sendMsg(testGNOT, ethGNOT, 1))
	w.g.head = 164
	w.advance(3 * time.Minute)
	w.tick(t)
	if len(w.e.sentOf("update")) != 1 || !strings.Contains(fmt.Sprint(w.u.status["waiting"]), "quarantine") {
		t.Fatalf("an update bought for a send under the quarantine (%v)", w.u.status["waiting"])
	}
	// a decision does: the client follows the new set at its header
	w.g.dispatch(165, decisionMsg(ethAdmin, 7, 1))
	w.g.head = 170
	w.advance(3 * time.Minute)
	w.tick(t)
	if w.e.trusted != 166 || w.e.epoch != 2 {
		t.Fatalf("trusted %d epoch %d (plan %q, due %v, waiting %v)", w.e.trusted, w.e.epoch, w.statusPlan(), w.u.status["due"], w.u.status["waiting"])
	}
	// a change the client cannot take for 30 minutes alarms
	w.g.sets[200] = newFakeSet(3, 60, 60, 60, 60, 60, 60)
	w.g.head = 210
	w.e.fail["update"] = revert("Paused", "")
	_ = w.u.tick(context.Background())
	w.advance(31 * time.Minute)
	_ = w.u.tick(context.Background())
	if _, ok := w.u.Alarm.Standing()["client:announce-lag"]; !ok {
		t.Fatal("no announce-lag alarm")
	}
}

// Split waiting dispatches give the relayer's batch BatchWait, then the oldest is served alone.
func TestUpdaterWaitsForTheBatch(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 1))
	w.g.dispatch(104, sendMsg(testGNOT, ethGNOT, 2))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.tick(t)
	if len(w.e.sentOf("update")) != 0 || !strings.Contains(w.statusPlan(), "batch") {
		t.Fatalf("an update while the batch is awaited (%s)", w.statusPlan())
	}
	w.advance(DefaultBatchWait)
	w.tick(t)
	if len(w.e.sentOf("update")) != 1 || w.e.trusted != 103 {
		t.Fatalf("the oldest not served after the wait: trusted %d", w.e.trusted)
	}
	// a batch whose fees cover no update is not awaited (the relayer re-emits nothing); the wait starts when it
	// becomes due, at the oldest send's six hours less the lead (the relayer's re-emit), and the oldest block is then
	// served within the budget
	w2 := newUpdWorld(t, 100)
	w2.u.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.0000001}
	w2.u.state.Budget.Balance = ethx.GweiOf(10_000_000).String()
	w2.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 1))
	w2.g.dispatch(104, sendMsg(testGNOT, ethGNOT, 2))
	w2.g.head = 110
	w2.advance(time.Hour)
	w2.tick(t)
	if !w2.u.state.SplitSince.IsZero() || len(w2.e.sentOf("update")) != 0 {
		t.Fatalf("a batch not due awaited (%s)", w2.statusPlan())
	}
	w2.advance(5*time.Hour - updateLead + time.Minute)
	w2.tick(t)
	if w2.u.state.SplitSince.IsZero() || len(w2.e.sentOf("update")) != 0 {
		t.Fatalf("the batch due at the six hours' lead not awaited (%s)", w2.statusPlan())
	}
	w2.advance(DefaultBatchWait + updateLead)
	w2.tick(t)
	if len(w2.e.sentOf("update")) != 1 || w2.e.trusted != 103 {
		t.Fatalf("the oldest not served after the wait: trusted %d (%s)", w2.e.trusted, w2.statusPlan())
	}
}

// An update whose signatures do not fit one call is staged; a stop after the first call resumes from the chain —
// the slots its StageProgress transaction carried left out (the fake refuses a slot staged twice) — and a call over
// the gas cap halves the bound.
func TestUpdaterStagedAndResumed(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.u.Cfg.Eth.MaxSigsPerCall = 2
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 1))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.e.failNth["update"] = 2 // the second staging call is lost
	if err := w.u.tick(context.Background()); err == nil {
		t.Fatal("the lost call was not reported")
	}
	if w.e.trusted != 100 || len(w.e.stages) != 1 {
		t.Fatalf("trusted %d, %d stages", w.e.trusted, len(w.e.stages))
	}
	w.e.failNth = map[string]int{}
	w.advance(time.Minute)
	w.tick(t)
	if w.e.trusted != 106 {
		t.Fatalf("the stage was not finished: trusted %d", w.e.trusted)
	}
	// a stage the client's epoch moved past is orphaned: its slots are sent again under the new key, never skipped
	w.e.delivered[w.g.idOf(0)] = true
	w.g.dispatch(115, sendMsg(testGNOT, ethGNOT, 2))
	w.g.head = 120
	w.advance(10 * time.Minute)
	w.e.failNth["update"] = len(w.e.sentOf("update")) + 2 // this update's second call is lost
	if err := w.u.tick(context.Background()); err == nil {
		t.Fatal("the lost call was not reported")
	}
	w.e.failNth = map[string]int{}
	w.e.epoch++
	w.advance(time.Minute)
	w.tick(t)
	if w.e.trusted != 116 {
		t.Fatalf("an orphaned stage's slots were skipped: trusted %d (plan %q)", w.e.trusted, w.statusPlan())
	}
	// the gas cap: a call of more slots than fit halves the bound
	w2 := newUpdWorld(t, 100)
	w2.e.capSlots = 3
	w2.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 1))
	w2.g.head = 110
	w2.advance(10 * time.Minute)
	_ = w2.u.tick(context.Background())
	if w2.u.state.SigsBound != 2 {
		t.Fatalf("bound %d", w2.u.state.SigsBound)
	}
	w2.advance(time.Minute)
	w2.tick(t)
	if w2.e.trusted != 106 {
		t.Fatalf("not staged under the halved bound: trusted %d", w2.e.trusted)
	}
}

// A header is priced by the model, never by the last update's receipt — whose gas its sender chose (E2-RT4): an update
// that burned 16M gas (a stranger's, wrapped in a contract that burns gas) leaves the next send's header priced at the
// model's ≈1.45M, which its fee covers; and the 2-minute spacing counts this updater's own updates only.
func TestUpdaterPricesTheModelNotAReceipt(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.e.gasUsed = 16_000_000
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 1))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.tick(t) // header 106 bought (the whole ETH price covers anything), its receipt 16M gas
	if w.e.trusted != 106 {
		t.Fatalf("trusted %d", w.e.trusted)
	}
	// a send whose fee covers ≈1.76M gas at 2 gwei (0.0035 ETH) and not 16M (0.032 ETH): 0.01 ETH, 50 GNOT at 2e-4
	w.u.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.0002}
	w.e.delivered[w.g.idOf(0)] = true
	w.g.dispatch(115, sendMsg(testGNOT, ethGNOT, 2))
	w.g.head = 120
	w.advance(10 * time.Minute)
	w.tick(t)
	if w.e.trusted != 116 {
		t.Fatalf("the model's price was not used: trusted %d (due %v)", w.e.trusted, w.u.status["due"])
	}
	// a stranger's update right after ours holds nothing: the spacing is ours (the fake's updates are all one key's,
	// so the stranger's is the trusted header moved by hand)
	w.e.delivered[w.g.idOf(1)] = true
	w.g.dispatch(125, sendMsg(testGNOT, ethGNOT, 3))
	w.g.head = 130
	w.advance(3 * time.Minute)
	w.e.trust(120)
	w.tick(t)
	if w.e.trusted != 126 {
		t.Fatalf("a stranger's update held ours: trusted %d (plan %q)", w.e.trusted, w.statusPlan())
	}
}

// While the gate refuses route deliveries (a flag, or the quarantine), a waiting send buys no header; a decision does.
func TestUpdaterBuysNothingForPausedRoutes(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.e.flags[4] = ethx.Flag{BlockNumber: 3, Time: 1}
	w.tick(t)
	if len(w.e.sentOf("update")) != 0 {
		t.Fatal("an update bought for a send the gate refuses")
	}
	w.g.dispatch(105, decisionMsg(ethAdmin, 7, 1))
	w.g.head = 110
	w.tick(t)
	if len(w.e.sentOf("update")) != 1 || w.e.trusted != 106 {
		t.Fatalf("a waiting decision bought nothing under the flag: trusted %d", w.e.trusted)
	}
}

// A send gno.land refunded is never delivered, so the updater ends it and buys it no header; the next send does.
func TestUpdaterBuysNothingForRefundedSends(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.g.renders[testGNOT+":json"] = routeJSON(50_000_000, `,"imported":2`) // refunds are a successor's (G42)
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.renders[testGNOT+":send/"+hexID(w.g.idOf(0))] = `{"id":"x","from":"g1a","amount":500,"refunded":true}`
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.tick(t)
	if len(w.e.sentOf("update")) != 0 || w.u.state.Out.Done["0"] != "refunded" {
		t.Fatalf("a refunded send: %d updates, ended %v", len(w.e.sentOf("update")), w.u.state.Out.Done)
	}
	w.g.dispatch(112, sendMsg(testGNOT, ethGNOT, 600))
	w.g.renders[testGNOT+":send/"+hexID(w.g.idOf(1))] = `{"id":"y","from":"g1a","amount":600}`
	w.g.head = 115
	w.tick(t)
	if len(w.e.sentOf("update")) != 1 || w.e.trusted != 113 {
		t.Fatalf("the next send bought nothing: trusted %d (plan %q)", w.e.trusted, w.statusPlan())
	}
}

// Split waiting that no batch can gather (each block holds a batch already) are served block by block without waiting
// for a batch, the oldest block whose own messages are due first: a decision's block goes under the emergency cap
// while an older block of sends whose fees do not cover an update waits.
func TestUpdaterServesEachBlock(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.u.Cfg.Eth.ReemitBatch = 2
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500), sendMsg(testGNOT, ethGNOT, 501))
	w.g.dispatch(107, sendMsg(testGNOT, ethGNOT, 502), sendMsg(testGNOT, ethGNOT, 503))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.tick(t)
	if ups := w.e.sentOf("update"); len(ups) != 1 || w.e.trusted != 106 {
		t.Fatalf("the oldest block not served at once: %d updates, trusted %d (plan %q)", len(ups), w.e.trusted, w.statusPlan())
	}
	w.advance(3 * time.Minute)
	w.tick(t)
	if w.e.trusted != 108 {
		t.Fatalf("the next block: trusted %d (plan %q)", w.e.trusted, w.statusPlan())
	}
	// fees that cover nothing: an older block of sends waits; a newer block holding a decision is bought alone
	w.u.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.0000001}
	w.g.dispatch(112, sendMsg(testGNOT, ethGNOT, 504), sendMsg(testGNOT, ethGNOT, 505))
	w.g.dispatch(114, decisionMsg(ethAdmin, 7, 1), decisionMsg(ethAdmin, 7, 2))
	w.g.head = 120
	w.advance(3 * time.Minute)
	w.tick(t)
	ups := w.e.sentOf("update")
	if w.e.trusted != 115 || ups[len(ups)-1].MaxBaseFee == nil {
		t.Fatalf("the decisions' block: trusted %d (due %v)", w.e.trusted, w.u.status["due"])
	}
}

// A message whose results leaf is over the bound proves nothing from it, in the updater's reading too (the scan's
// leaf sizes, which both machines read alike): its block buys no header, and the block of its re-emit, whose leaf is
// its own, does.
func TestUpdaterSkipsAnOversizedLeaf(t *testing.T) {
	w := newUpdWorld(t, 100)
	other := sendMsg(testGNOT, ethGNOT, 7)
	other.Destination = 1399811149 // another domain's dispatch in the same transaction: a leaf of two Dispatches
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 1), other)
	w.g.head = 110
	two, err := gnolight.ResultLeaves(w.g.results[105])
	if err != nil {
		t.Fatal(err)
	}
	one, err := gnolight.ResultLeaves([]*gnolight.TxResult{dispatchTx(w.g.mailbox, w.g.raw(0))})
	if err != nil {
		t.Fatal(err)
	}
	w.u.Cfg.Eth.MaxLeafBytes = (len(one[0]) + len(two[0])) / 2
	w.advance(10 * time.Minute)
	w.tick(t)
	if n, _ := w.u.state.Out.Index.LeafOf(0, 105); n != len(two[0]) || len(w.e.sentOf("update")) != 0 {
		t.Fatalf("a header bought for a block whose leaf is over the bound (leaf %d; plan %q)", n, w.statusPlan())
	}
	// the relayer's re-emit (by hand here): its block's header is bought
	res, err := w.g.Reemit(context.Background(), w.g.mailbox, []uint32{0}, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.advance(time.Minute)
	w.tick(t)
	if w.e.trusted != res.Height+1 {
		t.Fatalf("the re-emit's header not bought: trusted %d, re-emitted at %d (plan %q)", w.e.trusted, res.Height, w.statusPlan())
	}
}

// The updater ends a decision copy whose number HlAdmin's counter covers at the finalized block, so its walk moves
// past it; a stranger's dispatch and anyone's Reemit of an ended nonce leave nothing in the event index (G8).
func TestUpdaterEndsPastCopiesAndKeepsNoStrangers(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.e.admins[ethAdmin].lastSeq = 1 // another copy of number 1 was recorded
	w.g.dispatch(105, decisionMsg(ethAdmin, 7, 1))
	w.g.dispatch(106, sendMsg("gno.land/r/someone/else", ethGNOT, 9))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.tick(t)
	w.tick(t)
	if w.u.state.Out.Next != 2 {
		t.Fatalf("the walk stands at %d (done %v)", w.u.state.Out.Next, w.u.state.Out.Done)
	}
	if len(w.e.sentOf("update")) != 0 {
		t.Fatal("a header bought for a recorded number's copy")
	}
	if _, ok := w.u.state.Out.Index.Heights[1]; ok {
		t.Fatalf("a stranger's dispatch kept in the index: %v", w.u.state.Out.Index.Heights)
	}
	// anyone re-emits the ended nonce 1: nothing is kept for it
	if _, err := w.g.Reemit(context.Background(), w.g.mailbox, []uint32{1}, 0); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	if len(w.u.state.Out.Index.Heights) != 0 {
		t.Fatalf("index %v", w.u.state.Out.Index.Heights)
	}
}

// The announcing header goes before, and apart from, the backlog's reads: a backlog that cannot be read holds no
// validator change.
func TestUpdaterAnnouncesDespiteTheBacklog(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.g.sets[150] = newFakeSet(2, 60, 60, 60, 60, 60, 60, 60)
	w.g.head = 160
	w.g.renders[w.g.mailbox+":json"] = "{" // hlmailbox's render broken
	_ = w.u.tick(context.Background())
	if ups := w.e.sentOf("update"); len(ups) != 1 || w.e.trusted != 149 {
		t.Fatalf("%d updates, trusted %d (plan %q)", len(ups), w.e.trusted, w.statusPlan())
	}
}

// A failed update is not sent again every step: a refusal the simulation answered (nothing spent: another
// submitter may have landed first) waits a minute and counts for nothing; one that spent gas — mined and reverted —
// backs off a minute, then two, …, counted toward the park; a success clears them.
func TestUpdaterBacksOffAfterAFailure(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.e.fail["update"] = revert("HeightNotAboveTrusted", "")
	_ = w.u.tick(context.Background())
	sent := len(w.e.sentOf("update"))
	if sent != 1 || w.u.state.Attempt == nil || w.u.state.Attempt.Failures != 0 || w.u.state.Attempt.Tries != 1 {
		t.Fatalf("%d sent, attempt %+v", sent, w.u.state.Attempt)
	}
	w.advance(5 * time.Second)
	_ = w.u.tick(context.Background())
	if len(w.e.sentOf("update")) != sent || !strings.Contains(w.statusPlan(), "failed") {
		t.Fatalf("re-sent inside the backoff (plan %q)", w.statusPlan())
	}
	// mined and reverted: counted, and the backoff doubles
	w.e.fail["update"] = &ethx.Revert{Name: "WrongSet", Mined: true}
	w.advance(time.Minute)
	_ = w.u.tick(context.Background())
	w.advance(time.Minute)
	_ = w.u.tick(context.Background())
	if a := w.u.state.Attempt; a == nil || a.Failures != 2 || len(w.e.sentOf("update")) != sent+2 {
		t.Fatalf("%d sent, attempt %+v", len(w.e.sentOf("update")), a)
	}
	w.advance(time.Minute) // inside the two-minute backoff
	_ = w.u.tick(context.Background())
	if len(w.e.sentOf("update")) != sent+2 {
		t.Fatal("re-sent inside the doubled backoff")
	}
	delete(w.e.fail, "update")
	w.advance(2 * time.Minute)
	w.tick(t)
	if w.e.trusted != 106 || w.u.state.Attempt != nil {
		t.Fatalf("trusted %d, attempt %+v", w.e.trusted, w.u.state.Attempt)
	}
}

// Split waiting in a fee spike: a block holding a decision is served first, under the emergency cap, while an older
// block of sends waits for the base fee to fall (its header would park under maxBaseFee and hold the decision's).
func TestUpdaterDecisionFirstInASpike(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.u.Cfg.Eth.ReemitBatch = 2 // the sends' block holds a batch: no batch gathers the decision
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500), sendMsg(testGNOT, ethGNOT, 501))
	w.g.dispatch(107, decisionMsg(ethAdmin, 7, 1))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.e.baseFee = ethx.GweiOf(40)
	w.tick(t)
	ups := w.e.sentOf("update")
	if w.e.trusted != 108 || len(ups) != 1 || ups[0].MaxBaseFee == nil {
		t.Fatalf("the decision's header not bought first: trusted %d, %d updates (plan %q, due %v)", w.e.trusted, len(ups), w.statusPlan(), w.u.status["due"])
	}
}

// A staging call left pending is resolved before the stage is read back, so its slots are never sent again.
func TestUpdaterSettlesAPendingStagingCall(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.u.Cfg.Eth.MaxSigsPerCall = 2
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 1))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.e.pendNth["update"] = 2 // the second staging call stays pending
	if err := w.u.tick(context.Background()); err == nil {
		t.Fatal("the pending call was not reported")
	}
	w.e.pendNth = map[string]int{}
	w.advance(time.Minute)
	w.tick(t) // the fake client refuses a slot staged twice (the real one skips it, paying its calldata)
	if w.e.trusted != 106 {
		t.Fatalf("the stage was not finished: trusted %d (plan %q)", w.e.trusted, w.statusPlan())
	}
}

// The client is read back at the update's own block, never at a latest a lagging node answers: an update that
// landed is no failure.
func TestUpdaterReadsTheUpdateAtItsBlock(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 1))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.e.lagLatest = true
	w.tick(t)
	if w.e.trusted != 106 || w.u.state.Attempt != nil {
		t.Fatalf("trusted %d, attempt %+v", w.e.trusted, w.u.state.Attempt)
	}
	if _, ok := w.u.Alarm.Standing()["updater:unfinished"]; ok {
		t.Fatal("an update that landed reported unfinished")
	}
}

package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// Round 6's fold lane F18c: a decision number's copies (audit round 6, go-spend, E6-GS1). Anyone may re-send any
// number r/hlgov ever dispatched (hlgov.Redispatch, a MsgCall's fee apiece), each a new hlmailbox message.

// f18cCountEth counts what a relayer step asks Ethereum: every eth_call (each view of a batch), the largest batch, and
// the block reads.
type f18cCountEth struct {
	*fakeEth
	mu                      *sync.Mutex
	calls, maxBatch, blocks *int
}

func (c f18cCountEth) CallBatch(ctx context.Context, reqs []ethx.CallReq, at ethx.At) ([][]byte, []error, error) {
	c.mu.Lock()
	*c.calls += len(reqs)
	*c.maxBatch = max(*c.maxBatch, len(reqs))
	c.mu.Unlock()
	return c.fakeEth.CallBatch(ctx, reqs, at)
}

func (c f18cCountEth) CallAt(ctx context.Context, to common.Address, data []byte, at ethx.At) ([]byte, error) {
	c.mu.Lock()
	*c.calls++
	c.mu.Unlock()
	return c.fakeEth.CallAt(ctx, to, data, at)
}

func (c f18cCountEth) BlockAt(ctx context.Context, at ethx.At) (*ethx.Block, error) {
	c.mu.Lock()
	*c.blocks++
	c.mu.Unlock()
	return c.fakeEth.BlockAt(ctx, at)
}

// f18cStep is what one relayer step asked Ethereum.
type f18cStep struct{ calls, maxBatch, blocks int }

// f18cCounted runs one relayer tick and answers what it asked Ethereum.
func f18cCounted(w *testWorld) f18cStep {
	var mu sync.Mutex
	var s f18cStep
	ce := f18cCountEth{fakeEth: w.e, mu: &mu, calls: &s.calls, maxBatch: &s.maxBatch, blocks: &s.blocks}
	w.r.Eth, w.r.env.eth = ce, ce
	w.tick()
	w.r.Eth, w.r.env.eth = w.e, w.e
	return s
}

// f18cCopiesOf is the copies the relayer keeps of a decision number.
func f18cCopiesOf(w *testWorld, seq uint64) []decisionCopy {
	return w.r.state.DecisionCopies[strconv.FormatUint(seq, 10)]
}

// f18cSettle ticks until the relayer's walk has ended every message (at most n ticks, 3 s apart).
func f18cSettle(w *testWorld, n int) {
	for i := 0; i < n && w.r.state.Out.Next < w.g.count; i++ {
		w.tick()
		w.advance(3 * time.Second)
	}
}

// f18cEnded are the nonces from..to-1 whose Done is not how (a message ends in its step; the walk passes it at the next).
func f18cEnded(w *testWorld, from, to uint32, how string) []string {
	var bad []string
	for n := from; n < to; n++ {
		if d := w.r.state.Out.Done[strconv.FormatUint(uint64(n), 10)]; d != how {
			bad = append(bad, strconv.FormatUint(uint64(n), 10)+":"+d)
		}
	}
	return bad
}

// The auditor's scenario (E6-GS1): decision 1 is delivered and ended; a stranger then re-dispatches it K times; once
// the pile ended, each fresh copy read the delivered mark of every copy ever kept — DeliveredMany over the whole pile,
// on the delivery's critical path (419 eth_calls a tick at a pile of 200, 2,219 at 2,000 on e3d0053f) — and the relayer's
// state kept every copy for good. Now a number keeps its first copy, the one gone through our mailbox and its newest
// few; a copy of a number gone through ends at once, with no read: one fresh copy costs a step what it costs with no pile.
func TestF18cRedispatchedCopiesCostAConstantStep(t *testing.T) {
	steps := map[int]f18cStep{}
	for _, K := range []int{0, 200, 2000} {
		w := newWorld(t, 100, "primary")
		w.g.dispatch(105, decisionMsg(ethAdmin, 1, 1)) // decision 1 (nonce 0), delivered and ended
		w.g.head = 110
		w.e.trust(106)
		f18cSettle(w, 4)
		h := int64(111)
		for k := 0; k < K; k++ { // the stranger's pile, 400 copies a block
			w.g.dispatch(h, decisionMsg(ethAdmin, 1, 1))
			if k%400 == 399 {
				h++
			}
		}
		w.g.head = h + 2
		w.e.trust(h + 1)
		f18cSettle(w, 60)
		if w.r.state.Out.Next != w.g.count {
			t.Fatalf("K=%d: the pile did not end: next %d of %d", K, w.r.state.Out.Next, w.g.count)
		}
		// one fresh copy, its header accepted: one step
		fh := w.g.head
		fresh := w.g.count
		w.g.dispatch(fh, decisionMsg(ethAdmin, 1, 1))
		w.g.head = fh + 2
		w.e.trust(fh + 1)
		s := f18cCounted(w)
		steps[K] = s
		kept := f18cCopiesOf(w, 1)
		t.Logf("K=%d: the fresh copy's step asked %d eth_calls (largest batch %d) and %d blocks; number 1 keeps %d copies %v; the fresh copy %q",
			K, s.calls, s.maxBatch, s.blocks, len(kept), kept, w.r.state.Out.Done[strconv.FormatUint(uint64(fresh), 10)])
		if bad := f18cEnded(w, fresh, fresh+1, "recorded"); len(bad) > 0 {
			t.Errorf("K=%d: the fresh copy of a recorded number did not end recorded in its step: %v", K, bad)
		}
		if len(kept) > 2+copiesNewest {
			t.Errorf("K=%d: number 1 keeps %d copies (want at most its first, the one delivered and its newest %d)", K, len(kept), copiesNewest)
		}
		if w.r.state.Taken["1"] {
			t.Errorf("K=%d: a recorded number reported taken", K)
		}
	}
	for _, K := range []int{200, 2000} {
		if steps[K].calls > steps[0].calls {
			t.Errorf("a pile of %d copies makes one fresh copy's step ask %d eth_calls, %d with no pile", K, steps[K].calls, steps[0].calls)
		}
	}
}

// A number another decision took (no copy of ours delivered at the finalized block, on two passes) is reported taken
// once; a stranger's later copies of it each read the finalized block, HlAdmin's state there and the mark of every
// copy kept — K copies in one step read ≈K²/2 marks. A copy of a number reported taken now ends at once, with no read.
func TestF18cATakenNumbersCopiesEndAtOnce(t *testing.T) {
	steps := map[int]f18cStep{}
	for _, K := range []int{1, 300} {
		w := newWorld(t, 100, "primary")
		w.g.dispatch(120, decisionMsg(ethAdmin, 7, 3)) // our copy of 3; a foreign decision took 3
		w.g.head = 125
		w.e.admins[ethAdmin].lastSeq = 3
		w.tick()
		w.tick()
		if !w.r.state.Taken["3"] {
			t.Fatalf("number 3 not reported taken: %v", w.r.state.Out.Done)
		}
		from := w.g.count
		for k := 0; k < K; k++ {
			w.g.dispatch(130, decisionMsg(ethAdmin, 7, 3))
		}
		w.g.head = 135
		s := f18cCounted(w)
		steps[K] = s
		t.Logf("K=%d copies of the taken number 3: one step asked %d eth_calls (largest batch %d) and %d blocks; %d copies kept",
			K, s.calls, s.maxBatch, s.blocks, len(f18cCopiesOf(w, 3)))
		if bad := f18cEnded(w, from, w.g.count, "taken"); len(bad) > 0 {
			t.Errorf("K=%d: copies of a taken number not ended taken in their step: %d of them (%v …)", K, len(bad), bad[:min(len(bad), 4)])
		}
		if n := len(f18cCopiesOf(w, 3)); n > 2+copiesNewest {
			t.Errorf("K=%d: number 3 keeps %d copies", K, n)
		}
	}
	// the walk reads each copy's mark once (one eth_call apiece); nothing else grows with the pile
	if extra := steps[300].calls - steps[1].calls; extra > 299 {
		t.Errorf("300 copies of a taken number: %d eth_calls more than one copy's step (%d): the walk's 299 marks and nothing else", extra, steps[1].calls)
	}
	if steps[300].blocks > steps[1].blocks {
		t.Errorf("300 copies of a taken number: %d block reads, one copy %d", steps[300].blocks, steps[1].blocks)
	}
}

// While a number is recorded at latest and not yet at the finalized block (≈13 minutes of Ethereum's finality), its
// copies wait for the verdict; each read the finalized block and HlAdmin's 22 views there every step — a stranger's
// pile in that window cost ≈23 reads a copy a step. A number's verdict is now read once a step, whatever its copies.
func TestF18cAPileBeforeFinalityReadsOneVerdictAStep(t *testing.T) {
	steps := map[int]f18cStep{}
	for _, K := range []int{1, 500} {
		w := newWorld(t, 100, "primary")
		w.g.dispatch(105, decisionMsg(ethAdmin, 1, 1)) // nonce 0: delivered at latest, not final yet
		w.g.head = 110
		w.e.trust(106)
		w.e.finalDelivered = map[[32]byte]bool{}
		fin := uint64(0)
		w.e.admins[ethAdmin].finalLastSeq = &fin
		w.tick()
		if !w.e.delivered[w.g.idOf(0)] {
			t.Fatal("decision 1 not delivered")
		}
		for k := 0; k < K; k++ {
			w.g.dispatch(112, decisionMsg(ethAdmin, 1, 1))
		}
		w.g.head = 115
		w.advance(3 * time.Second)
		w.tick() // the copies walked once, past at latest
		w.advance(3 * time.Second)
		s := f18cCounted(w)
		steps[K] = s
		t.Logf("K=%d copies while number 1 is not final: one step asked %d eth_calls (largest batch %d) and %d blocks", K, s.calls, s.maxBatch, s.blocks)
		if w.r.state.Out.Next != 0 || w.r.state.Taken["1"] {
			t.Fatalf("K=%d: a number not final judged: next %d, taken %v", K, w.r.state.Out.Next, w.r.state.Taken)
		}
		// finality: the delivery is final, and every copy ends recorded in one step
		w.e.finalDelivered, w.e.admins[ethAdmin].finalLastSeq = nil, nil
		w.advance(3 * time.Second)
		w.tick()
		if bad := append(f18cEnded(w, 0, 1, "delivered"), f18cEnded(w, 1, w.g.count, "recorded")...); len(bad) > 0 || w.r.state.Taken["1"] {
			t.Errorf("K=%d: at finality the copies did not end recorded in one step: %d not (%v …), taken %v", K, len(bad), bad[:min(len(bad), 4)], w.r.state.Taken)
		}
		if kept := f18cCopiesOf(w, 1); len(kept) > 2+copiesNewest || !kept[0].Delivered {
			t.Errorf("K=%d: number 1 keeps %v", K, kept)
		}
	}
	// the walk reads each waiting copy's mark at latest every step (one eth_call apiece); the verdict once
	if extra := steps[500].calls - steps[1].calls; extra > 499 {
		t.Errorf("500 copies before finality: %d eth_calls more than one copy's step (%d) — the walk's 499 marks and nothing else", extra, steps[1].calls)
	}
	if steps[500].blocks > steps[1].blocks {
		t.Errorf("500 copies before finality: %d block reads a step, one copy %d", steps[500].blocks, steps[1].blocks)
	}
}

// f18cOlderState restarts the relayer onto a state file written before the bound: copiesV gone and the given lists
// as the old code kept them — every copy, none marked.
func f18cOlderState(t *testing.T, w *testWorld, lists map[string][]uint32) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relayer.json")
	w.r.State = path
	if err := w.r.save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "copiesV")
	dc := map[string]any{}
	for sk, ns := range lists {
		var cs []map[string]any
		for _, n := range ns {
			id := w.g.idOf(n)
			cs = append(cs, map[string]any{"nonce": n, "id": hex.EncodeToString(id[:])})
		}
		dc[sk] = cs
	}
	m["decisionCopies"] = dc
	if b, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	r2 := &Relayer{Cfg: w.r.Cfg, State: path, Role: w.r.Role, Alarm: &Alarmer{Source: "test"}, Sender: w.e, Eth: w.e, gno: w.r.gno, light: w.g, blocks: w.g, runner: w.g}
	if err := r2.init(); err != nil {
		t.Fatal(err)
	}
	r2.now = func() time.Time { return w.now }
	w.r = r2
}

// f18cNonces are the nonces from..to-1.
func f18cNonces(from, to uint32) []uint32 {
	var out []uint32
	for n := from; n < to; n++ {
		out = append(out, n)
	}
	return out
}

// A state file written before the bound keeps every copy of a number, none marked, and the copy that went through may
// lie anywhere in it (a stranger's update can prove a copy of the pile before the first): its list is judged whole at
// the finalized block — copyReadsPerStep marks a step, oldest first — before the number can read "taken", so it reads
// "recorded" and keeps the delivered copy marked.
func TestF18cAnOlderStatesPileIsJudgedBeforeItIsCut(t *testing.T) {
	w := newWorld(t, 100, "primary")
	const K = 150
	for k := 0; k < K; k++ {
		w.g.dispatch(105+int64(k/50), decisionMsg(ethAdmin, 1, 1)) // nonces 0–149 in blocks 105, 106, 107
	}
	w.g.head = 112
	// only block 107's header is accepted: the relayer delivers the first copy it can prove, nonce 100
	w.e.trust(110)
	w.e.accept(108)
	f18cSettle(w, 20)
	if w.r.state.Out.Next != K || !w.e.delivered[w.g.idOf(100)] || w.e.delivered[w.g.idOf(0)] {
		t.Fatalf("the setup: next %d of %d; nonce 100 delivered %v, nonce 0 %v", w.r.state.Out.Next, K, w.e.delivered[w.g.idOf(100)], w.e.delivered[w.g.idOf(0)])
	}
	f18cOlderState(t, w, map[string][]uint32{"1": f18cNonces(0, K)})
	if q, kept := len(w.r.state.CopiesToJudge["1"]), len(f18cCopiesOf(w, 1)); q != K || kept != 0 {
		t.Fatalf("the older file loaded: %d copies to judge, %d kept", q, kept)
	}
	// a fresh copy of 1, proven
	w.g.dispatch(113, decisionMsg(ethAdmin, 1, 1))
	w.g.head = 116
	w.e.trust(114)
	var most int
	for i := 0; i < 8 && w.r.state.Out.Next < w.g.count; i++ {
		s := f18cCounted(w)
		most = max(most, s.maxBatch)
		w.advance(3 * time.Second)
	}
	kept := f18cCopiesOf(w, 1)
	t.Logf("the older state's 150 copies: next %d of %d, taken %v, kept %v, %d left to judge, largest batch %d", w.r.state.Out.Next, w.g.count, w.r.state.Taken, kept, len(w.r.state.CopiesToJudge["1"]), most)
	if w.r.state.Out.Next != w.g.count || w.r.state.Taken["1"] {
		t.Fatalf("the fresh copy of a recorded number: next %d of %d, taken %v (%s)", w.r.state.Out.Next, w.g.count, w.r.state.Taken, w.r.last["deliver:150"])
	}
	if len(kept) != 1 || kept[0].Nonce != 100 || !kept[0].Delivered || len(w.r.state.CopiesToJudge) != 0 {
		t.Fatalf("number 1 keeps %v (want nonce 100 marked delivered), %d numbers left to judge", kept, len(w.r.state.CopiesToJudge))
	}
	if most > copyReadsPerStep+K/50+8 {
		t.Errorf("a step read a batch of %d", most)
	}
}

// The review of the lane (finding 1): an older file's short list is no less unmarked — number 1 went to the retired
// HlAdmin as n0 and through to its successor as n1, both ended, their Done pruned — and four fresh copies of it made
// the bound drop n1 unread, so the number read "taken" on the next pass (decision:taken:1, a recorded decision to be
// re-made). The list is judged whole first.
func TestF18cAShortOlderListIsJudgedWhole(t *testing.T) {
	w := newWorld(t, 100, "primary")
	succ := common.HexToAddress("0x00000000000000000000000000000000000000e2")
	w.e.admins[ethAdmin].successor = succ
	w.e.admins[succ] = &fakeAdmin{decisions: map[uint64]*ethx.Decision{}, clockFactor: 1}
	w.g.dispatch(106, decisionMsg(ethAdmin, 7, 1), decisionMsg(succ, 7, 1)) // n0 to the retired one, n1 to the successor
	w.g.head = 112
	w.e.trust(107)
	f18cSettle(w, 6)
	if w.r.state.Out.Next != 2 || w.e.admins[succ].lastSeq != 1 {
		t.Fatalf("the setup: next %d, the successor's counter %d", w.r.state.Out.Next, w.e.admins[succ].lastSeq)
	}
	f18cOlderState(t, w, map[string][]uint32{"1": {0, 1}})
	for k := 0; k < 4; k++ { // the stranger's four Redispatch(1), in one transaction
		w.g.dispatch(113, decisionMsg(succ, 7, 1))
	}
	w.g.head = 116
	for i := 0; i < 5 && w.r.state.Out.Next < w.g.count; i++ {
		w.tick()
		w.advance(3 * time.Second)
		if w.r.state.Taken["1"] {
			t.Fatalf("a recorded number read taken after four fresh copies (step %d): %v", i, w.r.Alarm.StandingKeys())
		}
	}
	if w.r.state.Out.Next != w.g.count {
		t.Fatalf("the fresh copies did not end: next %d of %d (%s)", w.r.state.Out.Next, w.g.count, w.r.last["deliver:2"])
	}
	if kept := f18cCopiesOf(w, 1); len(kept) != 1 || kept[0].Nonce != 1 || !kept[0].Delivered {
		t.Fatalf("number 1 keeps %v (want n1 marked)", kept)
	}
}

// The review of the lane (finding 2): an older file's long list being judged while fresh copies of its number are
// walked — each step judged the oldest 64 of its middle, the walk put the cut copies back, and no copy ended for good
// (Next stuck, Done and the notes growing with the traffic). The older copies are judged apart (CopiesToJudge), which
// no copy the walk reads joins, oldest first.
func TestF18cAnOlderPileConvergesWhileFreshCopiesAreWalked(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(105, decisionMsg(ethAdmin, 1, 1)) // nonce 0: delivered
	w.g.head = 110
	w.e.trust(106)
	f18cSettle(w, 4)
	for k := 0; k < 149; k++ {
		w.g.dispatch(111, decisionMsg(ethAdmin, 1, 1))
	}
	w.g.head = 113
	w.e.trust(112)
	f18cSettle(w, 10)
	if w.r.state.Out.Next != 150 {
		t.Fatalf("the setup: next %d", w.r.state.Out.Next)
	}
	f18cOlderState(t, w, map[string][]uint32{"1": f18cNonces(0, 150)})
	for k := 0; k < 70; k++ { // fresh copies, walked every step until they end
		w.g.dispatch(114, decisionMsg(ethAdmin, 1, 1))
	}
	w.g.head = 117
	steps := 0
	for ; steps < 8 && w.r.state.Out.Next < w.g.count; steps++ {
		w.tick()
		w.advance(3 * time.Second)
	}
	t.Logf("150 older copies and 70 fresh ones: next %d of %d after %d steps; %d numbers left to judge; taken %v", w.r.state.Out.Next, w.g.count, steps, len(w.r.state.CopiesToJudge), w.r.state.Taken)
	if w.r.state.Out.Next != w.g.count || len(w.r.state.CopiesToJudge) != 0 || w.r.state.Taken["1"] {
		t.Fatalf("the older pile did not converge: next %d of %d, %v left to judge, taken %v", w.r.state.Out.Next, w.g.count, len(w.r.state.CopiesToJudge["1"]), w.r.state.Taken)
	}
}

// f18cLagEth is a provider behind a balancer whose node serving the latest block lags: while stale is set, the
// delivered marks read at latest are stale's; every read pinned to a block hash is a fresh node's.
type f18cLagEth struct {
	*fakeEth
	stale map[[32]byte]bool
}

func (c *f18cLagEth) CallBatch(ctx context.Context, reqs []ethx.CallReq, at ethx.At) ([][]byte, []error, error) {
	if c.stale != nil && at.Tag == "latest" {
		c.fakeEth.mu.Lock()
		real := c.fakeEth.delivered
		c.fakeEth.delivered = c.stale
		c.fakeEth.mu.Unlock()
		defer func() {
			c.fakeEth.mu.Lock()
			c.fakeEth.delivered = real
			c.fakeEth.mu.Unlock()
		}()
	}
	return c.fakeEth.CallBatch(ctx, reqs, at)
}

// The review of the lane (finding 3): the copy that went through is protected only while the walk sees it delivered at
// latest; a node two epochs behind serving the walk's latest marks in two steps, while the finalized reads go to fresh
// nodes, made a number read taken. The first copies seen delivered at latest are kept, and every verdict reads their
// marks at the finalized block.
func TestF18cTheCopySeenDeliveredIsReadAtTheFinalizedBlock(t *testing.T) {
	w := newWorld(t, 100, "primary")
	lag := &f18cLagEth{fakeEth: w.e}
	w.r.Eth, w.r.env.eth = lag, lag
	w.g.dispatch(105, decisionMsg(ethAdmin, 7, 1))                              // nonce 0: never proven
	w.g.dispatch(108, decisionMsg(ethAdmin, 7, 1), decisionMsg(ethAdmin, 7, 1)) // nonces 1, 2: proven
	w.g.head = 112
	w.e.trust(110)
	w.e.accept(109) // header 109 proves block 108; nothing proves block 105
	w.e.finalDelivered = map[[32]byte]bool{}
	fin := uint64(0)
	w.e.admins[ethAdmin].finalLastSeq = &fin
	w.tick() // nonce 1 delivered at latest
	if !w.e.delivered[w.g.idOf(1)] {
		t.Fatalf("nonce 1 not delivered: %s", w.r.last["deliver:1"])
	}
	for k := 0; k < 5; k++ { // the stranger's copies: nonce 1 is neither the first nor among the newest four
		w.g.dispatch(113, decisionMsg(ethAdmin, 7, 1))
	}
	w.g.head = 116
	w.advance(3 * time.Second)
	w.tick()
	// finality comes, and the walk's latest marks come from a node that has not seen nonce 1's delivery
	w.e.finalDelivered, w.e.admins[ethAdmin].finalLastSeq = nil, nil
	lag.stale = map[[32]byte]bool{}
	for i := 0; i < 3; i++ {
		w.advance(3 * time.Second)
		w.tick()
	}
	t.Logf("after three lagging steps: next %d of %d, taken %v, kept %v", w.r.state.Out.Next, w.g.count, w.r.state.Taken, f18cCopiesOf(w, 1))
	if w.r.state.Taken["1"] {
		t.Fatalf("a recorded number read taken through a lagging latest view: %v", w.r.Alarm.StandingKeys())
	}
	if bad := f18cEnded(w, w.r.state.Out.Next, w.g.count, "recorded"); w.r.state.Out.Next < 2 || len(bad) > 0 {
		t.Fatalf("the copies did not end recorded: next %d, %v", w.r.state.Out.Next, bad)
	}
}

// f18cRetiredAlarms are the standing decision:retired alarms.
func f18cRetiredAlarms(w *testWorld) []string {
	var out []string
	for _, k := range w.r.Alarm.StandingKeys() {
		if strings.HasPrefix(k, "decision:retired:") {
			out = append(out, k)
		}
	}
	return out
}

// HlAdmin handed every authority to a successor (kind 9) and r/hlgov still dispatches to it: every copy to it ends
// retired, alarmed by its nonce — so a stranger's Redispatch of any number raised one standing alarm apiece (K copies,
// K alarms on the webhook), old numbers the line recorded long ago included, and a re-dispatch to the successor cleared
// only those still in Done. The alarm is now the number's: raised while the number is not recorded on the line and no
// copy of it goes to the live HlAdmin, once whatever its copies; a copy of a number the live HlAdmin's counter covers
// ends retired in silence; a copy of the number to the live HlAdmin clears it.
func TestF18cRetiredCopiesAlarmOnceANumber(t *testing.T) {
	w := newWorld(t, 100, "primary")
	succ := common.HexToAddress("0x00000000000000000000000000000000000000e2")
	w.e.admins[ethAdmin].successor = succ
	w.e.admins[succ] = &fakeAdmin{decisions: map[uint64]*ethx.Decision{}, clockFactor: 1, lastSeq: 1} // inherited 1
	const K = 40
	for k := 0; k < K; k++ {
		w.g.dispatch(102, decisionMsg(ethAdmin, 7, 1)) // the stranger's copies of 1, recorded on the line
		w.g.dispatch(103, decisionMsg(ethAdmin, 7, 2)) // and of 2, recorded nowhere
	}
	w.g.head = 110
	w.e.trust(105)
	w.tick()
	w.tick()
	got := f18cRetiredAlarms(w)
	t.Logf("%d copies of 1 and %d of 2 to the retired HlAdmin: next %d of %d; standing %v", K, K, w.r.state.Out.Next, w.g.count, got)
	if w.r.state.Out.Next != w.g.count {
		t.Fatalf("the copies to the retired HlAdmin did not end: next %d of %d", w.r.state.Out.Next, w.g.count)
	}
	if len(got) != 1 || got[0] != "decision:retired:2" {
		t.Fatalf("the retired copies' alarms: %v (want decision:retired:2 alone)", got)
	}
	if text := w.r.Alarm.Standing()["decision:retired:2"]; !strings.Contains(text, succ.Hex()) {
		t.Fatalf("decision:retired:2 does not name the live HlAdmin: %s", text)
	}
	// r/hlgov's recipient switched, then a re-dispatch: the copy to the successor goes, and the alarm clears
	w.g.dispatch(111, decisionMsg(succ, 7, 2))
	w.g.head = 115
	w.e.trust(112)
	f18cSettle(w, 6)
	if w.e.admins[succ].lastSeq != 2 {
		t.Fatalf("the re-dispatch to the successor not delivered: last %d", w.e.admins[succ].lastSeq)
	}
	if got := f18cRetiredAlarms(w); len(got) != 0 {
		t.Fatalf("after the copy to the live HlAdmin: %v", got)
	}
}

// The retired alarm of a number that goes on to be recorded clears once its copy to the live HlAdmin is walked —
// in the step it first appears, beside the retired copy (r/hlgov switched between two dispatches of the number).
func TestF18cARetiredCopyBesideALiveOneRaisesNothing(t *testing.T) {
	w := newWorld(t, 100, "primary")
	succ := common.HexToAddress("0x00000000000000000000000000000000000000e2")
	w.e.admins[ethAdmin].successor = succ
	w.e.admins[succ] = &fakeAdmin{decisions: map[uint64]*ethx.Decision{}, clockFactor: 1}
	w.g.dispatch(106, decisionMsg(ethAdmin, 7, 1), decisionMsg(succ, 7, 1))
	w.g.head = 112
	w.e.trust(107)
	w.tick()
	if got := f18cRetiredAlarms(w); len(got) != 0 {
		t.Fatalf("a retired copy beside a copy of its number to the live HlAdmin alarmed: %v", got)
	}
	if w.r.state.Out.Done["0"] != "retired" || w.e.admins[succ].lastSeq != 1 {
		t.Fatalf("%v %d", w.r.state.Out.Done, w.e.admins[succ].lastSeq)
	}
}

// An open retired alarm (a number with no copy to the live HlAdmin) stood only in the Alarmer's memory: a restart
// dropped it, and the copy that raised it had ended. It is kept in the state (RetiredOpen) and raised again each pass.
func TestF18cARetiredAlarmSurvivesARestart(t *testing.T) {
	w := newWorld(t, 100, "primary")
	succ := common.HexToAddress("0x00000000000000000000000000000000000000e2")
	w.e.admins[ethAdmin].successor = succ
	w.e.admins[succ] = &fakeAdmin{decisions: map[uint64]*ethx.Decision{}, clockFactor: 1, lastSeq: 1}
	w.g.dispatch(103, decisionMsg(ethAdmin, 7, 2)) // number 2 to the retired HlAdmin, recorded nowhere
	w.g.head = 110
	w.e.trust(104)
	w.tick()
	w.tick()
	if got := f18cRetiredAlarms(w); len(got) != 1 {
		t.Fatalf("before the restart: %v", got)
	}
	w.r.State = filepath.Join(t.TempDir(), "relayer.json")
	if err := w.r.save(); err != nil {
		t.Fatal(err)
	}
	r2 := &Relayer{Cfg: w.r.Cfg, State: w.r.State, Role: "primary", Alarm: &Alarmer{Source: "test"}, Sender: w.e, Eth: w.e, gno: w.g, light: w.g, blocks: w.g, runner: w.g}
	if err := r2.init(); err != nil {
		t.Fatal(err)
	}
	r2.now = func() time.Time { return w.now }
	w.r = r2
	w.advance(3 * time.Second)
	w.tick()
	if got := f18cRetiredAlarms(w); len(got) != 1 || got[0] != "decision:retired:2" {
		t.Fatalf("after the restart: %v", got)
	}
	// the successor's counter comes to cover it (a copy of 2 went through elsewhere): the alarm closes
	w.e.admins[succ].lastSeq = 2
	w.advance(3 * time.Second)
	w.tick()
	if got := f18cRetiredAlarms(w); len(got) != 0 || len(w.r.state.RetiredOpen) != 0 {
		t.Fatalf("covered by the live counter: %v %v", got, w.r.state.RetiredOpen)
	}
}

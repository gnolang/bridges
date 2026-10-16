package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// Lane F17a (audit round 5's go-spend, E5-GS4): anyone's Reemit of a waiting message adds an event a block, and every
// walk of both machines read every event's next header from the gno node and its client entry from Ethereum in one
// batch — past a provider's batch cap the walk failed every step, gno.land → Ethereum halted for good, unalarmed.

// countGno counts the header reads the machines make of the own gno node.
type countGno struct {
	*fakeGno
	mu     *sync.Mutex
	blocks *int
}

func (c countGno) BlockAt(ctx context.Context, h int64) (*gnolight.Block, error) {
	c.mu.Lock()
	*c.blocks++
	c.mu.Unlock()
	return c.fakeGno.BlockAt(ctx, h)
}

// cappedEth is a provider that answers a JSON-RPC batch of more than limit calls as geth does past its
// --rpc.batch-request-limit (every element missing a response), and counts the calls and the largest batch.
type cappedEth struct {
	*fakeEth
	limit    int
	mu       *sync.Mutex
	calls    *int
	maxBatch *int
	fail     *bool // every read fails (a provider down)
}

func (c cappedEth) CallBatch(ctx context.Context, reqs []ethx.CallReq, at ethx.At) ([][]byte, []error, error) {
	c.mu.Lock()
	*c.calls += len(reqs)
	*c.maxBatch = max(*c.maxBatch, len(reqs))
	failing := c.fail != nil && *c.fail
	c.mu.Unlock()
	if failing {
		return nil, nil, errors.New("connection refused")
	}
	if len(reqs) > c.limit {
		errs := make([]error, len(reqs))
		for i := range errs {
			errs[i] = errors.New("response batch did not contain a response to this call")
		}
		return make([][]byte, len(reqs)), errs, nil
	}
	return c.fakeEth.CallBatch(ctx, reqs, at)
}

func (c cappedEth) CallAt(ctx context.Context, to common.Address, data []byte, at ethx.At) ([]byte, error) {
	c.mu.Lock()
	failing := c.fail != nil && *c.fail
	c.mu.Unlock()
	if failing {
		return nil, errors.New("connection refused")
	}
	return c.fakeEth.CallAt(ctx, to, data, at)
}

// reemitWorld is a relayer and an updater over the fakes, a fee-short send S (nonce 0) waiting since 105 and re-emitted by
// a stranger once a block up to 104+n, GovDAO's cancel (nonce 1) dispatched above them with its header accepted; the
// machines read Ethereum through a provider of geth's default batch cap, counted.
type reemitWorld struct {
	w                       *testWorld
	u                       *Updater
	mu                      sync.Mutex
	blocks, calls, maxBatch int
	fail                    bool
}

func newReemitWorld(t *testing.T, n int) *reemitWorld {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1} // S's fee is short: it waits (six hours)
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 40_000_000))             // S, nonce 0
	for h := int64(106); h < 106+int64(n); h++ {
		w.g.reemitAt(h, 0)
	}
	top := int64(106 + n)
	w.g.dispatch(top, decisionMsg(ethAdmin, 1, 1)) // GovDAO's cancel, nonce 1
	w.g.mu.Lock()
	w.g.head = top + 2
	w.g.mu.Unlock()
	w.e.trust(top + 1) // its header accepted (anyone's update)
	rw := &reemitWorld{w: w, u: updaterOver(t, w)}
	cg := countGno{fakeGno: w.g, mu: &rw.mu, blocks: &rw.blocks}
	ce := cappedEth{fakeEth: w.e, limit: 1000, mu: &rw.mu, calls: &rw.calls, maxBatch: &rw.maxBatch, fail: &rw.fail}
	w.r.light, w.r.env.light, w.r.Eth, w.r.env.eth = cg, cg, ce, ce
	rw.u.gno, rw.u.env.light, rw.u.Eth, rw.u.env.eth = cg, cg, ce, ce
	return rw
}

// reads is one machine step's header reads, eth_calls and largest batch.
func (rw *reemitWorld) reads(step func()) (blocks, calls, maxBatch int) {
	rw.mu.Lock()
	rw.blocks, rw.calls, rw.maxBatch = 0, 0, 0
	rw.mu.Unlock()
	step()
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.blocks, rw.calls, rw.maxBatch
}

// E5-GS4: a stranger re-emits a waiting send once a block, 3,000 times (≈8 GNOT an hour of MsgCalls). Through a
// provider that keeps geth's batch cap of 1,000, every walk failed and GovDAO's cancel, proven beside it, was never
// delivered; and below the cap each step of each machine read every event's header and entry (≈530 eth_calls a
// second). The walk reads a waiting message's dispatch, its newest few events and those under headers seen recorded,
// in batches of at most 100; the others are judged once at the finalized block and dropped from the index, so a step
// reads a bounded few whatever a stranger adds.
func TestF17aReemitsCostTheWalkABoundedRead(t *testing.T) {
	rw := newReemitWorld(t, 3000)
	w := rw.w
	w.chainStep(13 * time.Minute) // the stranger's update is final (the compaction judges at the finalized block)
	for i := 0; i < 3; i++ {      // the scans reach the head (2,000 blocks a step)
		w.tick()
		_ = rw.u.tick(context.Background())
		w.advance(3 * time.Second)
	}
	if w.e.admins[ethAdmin].lastSeq != 1 {
		t.Fatalf("GovDAO's cancel, proven beside a send re-emitted 3,000 times, was not delivered: HlAdmin's counter %d (%s)", w.e.admins[ethAdmin].lastSeq, w.r.last["deliver:1"])
	}
	rb, rc, rmax := rw.reads(w.tick)
	ub, uc, umax := rw.reads(func() { _ = rw.u.tick(context.Background()) })
	t.Logf("with S's %d events: a relayer step %d header reads, %d eth_calls (largest batch %d); an updater step %d, %d (%d)", len(w.r.state.Out.Index.Heights[0]), rb, rc, rmax, ub, uc, umax)
	for _, c := range []struct {
		who                    string
		blocks, calls, largest int
	}{{"relayer", rb, rc, rmax}, {"updater", ub, uc, umax}} {
		if c.blocks > compactPerStep+2*readNewest+8 || c.largest > 100 || c.calls > 3*compactPerStep+120 { // 100: ethx.DefaultBatchSize
			t.Fatalf("one %s step read %d headers and %d eth_calls (a batch of %d) for one send a stranger re-emitted 3,000 times", c.who, c.blocks, c.calls, c.largest)
		}
	}
	// the compaction drops the events no header can prove, a few a step, each judged twice an hour apart: the index
	// keeps the dispatch and the newest
	for round := 0; round < 2; round++ {
		for i := 0; i < 60 && len(w.r.state.Out.Index.Heights[0]) > 2*readNewest; i++ {
			w.tick()
			_ = rw.u.tick(context.Background())
			w.advance(3 * time.Second)
		}
		w.advance(compactAgain)
	}
	if n, m := len(w.r.state.Out.Index.Heights[0]), len(rw.u.state.Out.Index.Heights[0]); n > 2*readNewest || m > 2*readNewest {
		t.Fatalf("after 60 steps the indexes hold %d and %d of S's events", n, m)
	}
	t.Logf("compacted: the relayer's index holds %v", w.r.state.Out.Index.Heights[0])
}

// The bound must not lose a booked event (E4-GS4's class): S, re-emitted once at 108, buys its six-hour header after its
// newest event (109) and is booked under it; the gate is flagged before the relayer delivers, and a stranger re-emits S in
// ten later blocks meanwhile — 108 is then neither S's dispatch nor among its newest few. The header the relayer saw the
// client trust is read every step, so once the flag clears S is delivered from 108, booked, and no second header is
// bought.
func TestF17aBookedEventBuriedUnderReemitsStaysBooked(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
	u := updaterOver(t, w)
	u.state.Budget.Balance = ethx.GweiOf(50_000_000).String()
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 40_000_000)) // S
	w.g.reemitAt(108, 0)
	w.g.head = 112
	w.step(6*time.Hour + time.Minute)
	if err := u.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.e.trusted != 109 || !u.state.Budget.settled(0) {
		t.Fatalf("S's six-hour header: trusted %d, %s", w.e.trusted, u.state.Budget.text())
	}
	w.e.flags[4] = ethx.Flag{BlockNumber: 9, Time: 1} // a guardian's flag: route deliveries wait
	w.tick()
	for h := int64(112); h < 122; h++ {
		w.g.reemitAt(h, 0)
	}
	for i := 0; i < 3; i++ {
		_ = u.tick(context.Background())
		w.tick()
		w.step(time.Minute)
	}
	if got := w.e.deliveredLeaves(t); len(got) != 0 {
		t.Fatalf("delivered under a flag: %v", got)
	}
	w.e.flags[4] = ethx.Flag{}
	for i := 0; i < 4; i++ {
		_ = u.tick(context.Background())
		w.tick()
		w.step(time.Minute)
	}
	got := w.e.deliveredLeaves(t)
	ups := len(w.e.sentOf("update")) + len(w.e.sentOf("proveHeader"))
	t.Logf("S under ten re-emits after its booked event: delivered %v, %d header(s) bought; its events %v", got, ups, w.r.state.Out.Index.Of(0, 105))
	if len(got) != 1 || ups != 1 {
		t.Fatalf("S buried under ten re-emits: delivered %v, %d headers bought (%s)", got, ups, w.r.last["deliver:0"])
	}
}

// A walk that fails holds every delivery, decision and header to Ethereum: it was a log line on both machines. Failing
// for two minutes, it alarms outbox:walk on each, and the alarm clears once a walk goes.
func TestF17aFailedWalkAlarms(t *testing.T) {
	rw := newReemitWorld(t, 10)
	w := rw.w
	rw.fail = true
	for i := 0; i < 50; i++ {
		w.tick()
		_ = rw.u.tick(context.Background())
		w.step(3 * time.Second)
	}
	_, r := w.r.Alarm.Standing()["outbox:walk"]
	_, u := rw.u.Alarm.Standing()["outbox:walk"]
	if !r || !u {
		t.Fatalf("a walk failing for %s: outbox:walk on the relayer %v, the updater %v (relayer alarms %v)", 150*time.Second, r, u, w.r.Alarm.StandingKeys())
	}
	if text := w.r.Alarm.Standing()["outbox:walk"]; !strings.Contains(text, "no delivery, decision or header goes to Ethereum") {
		t.Fatalf("the alarm's text: %s", text)
	}
	rw.fail = false
	w.tick()
	_ = rw.u.tick(context.Background())
	if _, still := w.r.Alarm.Standing()["outbox:walk"]; still {
		t.Fatal("outbox:walk stands once the walk goes")
	}
}

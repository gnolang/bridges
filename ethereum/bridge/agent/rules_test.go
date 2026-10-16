package agent

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

func msgAt(n uint32, kind msgKind, events ...int64) *outMsg {
	return &outMsg{n: n, kind: kind, events: events, msg: &gnoside.Msg{Height: firstOr0(events)}}
}

func firstOr0(hs []int64) int64 {
	if len(hs) == 0 {
		return 0
	}
	return hs[0]
}

// The waiting rule: one update serves messages whose newest events share a block above the trusted height; a split
// over blocks, or a message no update can serve (below the trusted height), asks for the batch re-emit, oldest first,
// at most the batch size — of the ones a batch can gather: a block holding a batch already waits for its own header,
// and ones that already share one block are not re-emitted.
func TestPlanWaits(t *testing.T) {
	cases := []struct {
		name   string
		ws     []*outMsg
		trust  int64
		shared int64
		split  bool
		below  string
		reemit string
		oldest int64
	}{
		{"none", nil, 100, 0, false, "[]", "[]", 0},
		{"one block", []*outMsg{msgAt(1, kindRoute, 105), msgAt(2, kindRoute, 105)}, 100, 105, false, "[]", "[]", 105},
		{"a batch block shared after older events", []*outMsg{msgAt(1, kindRoute, 101, 110), msgAt(2, kindRoute, 104, 110)}, 100, 110, false, "[]", "[]", 110},
		{"two blocks", []*outMsg{msgAt(1, kindRoute, 105), msgAt(2, kindRoute, 107)}, 100, 0, true, "[]", "[1 2]", 105},
		{"one below the trusted height", []*outMsg{msgAt(1, kindRoute, 90), msgAt(2, kindRoute, 105)}, 100, 0, true, "[1]", "[1 2]", 105},
		{"the header after the event is the trusted one: below", []*outMsg{msgAt(1, kindDecision, 99)}, 100, 0, true, "[1]", "[1]", 0},
		{"the trusted height itself: an update above serves it", []*outMsg{msgAt(1, kindDecision, 100)}, 100, 100, false, "[]", "[]", 100},
		{"no usable event", []*outMsg{msgAt(1, kindRoute)}, 100, 0, true, "[1]", "[1]", 0},
		{"the batch size", []*outMsg{msgAt(1, kindRoute, 101), msgAt(2, kindRoute, 102), msgAt(3, kindRoute, 103)}, 100, 0, true, "[]", "[1 2]", 101},
		{"a block holding a batch is left to its header; one other block gathers nothing", []*outMsg{msgAt(1, kindRoute, 105), msgAt(2, kindRoute, 105), msgAt(3, kindRoute, 107)}, 100, 0, true, "[]", "[]", 105},
		{"the others gathered past a full block", []*outMsg{msgAt(1, kindRoute, 105), msgAt(2, kindRoute, 105), msgAt(3, kindRoute, 107), msgAt(4, kindRoute, 108)}, 100, 0, true, "[]", "[3 4]", 105},
		{"one below beside a full block", []*outMsg{msgAt(1, kindRoute, 90), msgAt(2, kindRoute, 105), msgAt(3, kindRoute, 105)}, 100, 0, true, "[1]", "[1]", 105},
		{"two full blocks", []*outMsg{msgAt(1, kindRoute, 103), msgAt(2, kindRoute, 103), msgAt(3, kindRoute, 105), msgAt(4, kindRoute, 105)}, 100, 0, true, "[]", "[]", 103},
		// E3-RT4: below the trusted height, a decision, and a send a re-emit gathered already, are proven by the header
		// after their newest event when the current set signs it (Old), never gathered again; a send at its dispatch is
		// re-emitted first, and so is one whose newest event's header is accepted already (a held send needs a fresh event)
		{"a decision below: a header proof", []*outMsg{old(msgAt(1, kindDecision, 95))}, 100, 0, true, "[]", "[]", 0},
		{"a re-emitted send below: a header proof", []*outMsg{old(msgAt(1, kindRoute, 90, 95)), msgAt(2, kindRoute, 105)}, 100, 0, true, "[]", "[]", 105},
		{"a send at its dispatch below: re-emitted", []*outMsg{old(msgAt(1, kindRoute, 95)), msgAt(2, kindRoute, 105)}, 100, 0, true, "[1]", "[1 2]", 105},
		{"a held send whose newest header is accepted: re-emitted", []*outMsg{accepted(old(msgAt(1, kindRoute, 90, 95))), msgAt(2, kindRoute, 105)}, 100, 0, true, "[1]", "[1 2]", 105},
		{"a decision below under another set: re-emitted", []*outMsg{msgAt(1, kindDecision, 95)}, 100, 0, true, "[1]", "[1]", 0},
	}
	for _, c := range cases {
		p := planWaits(c.ws, c.trust, 2)
		if p.Shared != c.shared || p.Split != c.split || fmt.Sprint(nz(p.Below)) != c.below || fmt.Sprint(nz(p.Reemit)) != c.reemit || p.Oldest != c.oldest {
			t.Errorf("%s: %+v", c.name, p)
		}
		n := len(p.Below)
		for _, g := range p.Old {
			n += len(g.Msgs)
			if g.Block+1 > c.trust {
				t.Errorf("%s: an old group above the trusted height: %+v", c.name, g)
			}
		}
		for i, g := range p.Groups {
			n += len(g.Msgs)
			if i > 0 && g.Block <= p.Groups[i-1].Block {
				t.Errorf("%s: groups out of order: %+v", c.name, p.Groups)
			}
		}
		if n != len(c.ws) {
			t.Errorf("%s: %d of %d messages planned", c.name, n, len(c.ws))
		}
	}
}

// old marks a message's newest event's header as signed by the client's current set (a header proof takes it).
func old(t *outMsg) *outMsg { t.newestCurrentSet = true; return t }

// accepted marks a message's newest event's header as accepted already.
func accepted(t *outMsg) *outMsg { t.newestProven = true; return t }

func nz(x []uint32) []uint32 {
	if x == nil {
		return []uint32{}
	}
	return x
}

// When a header is due (§5.5, budget.go): a waiting decision always (outside the budget); route sends when their own
// fees cover the whole cost — the header's and every delivery's, never the header's alone (E2-RT2); failing that, at
// the oldest send's six hours: the updater when its budget covers what the fees lack (a deficit draws nothing), the
// relayer's re-emit at once and its lead early, the relayer's header proof never (no budget behind it).
func TestHeaderDue(t *testing.T) {
	gwei := func(g float64) *big.Int { return ethx.GweiOf(g) }
	base := dueFacts{Routes: 2, FeesWei: gwei(1_000_000), HeaderWei: gwei(1_500_000), DeliveriesWei: gwei(500_000), CostWei: gwei(2_000_000), Price: gwei(1), Oldest: time.Hour, Heartbeat: 6 * time.Hour}
	if due, _, why := headerDue(base); due || !strings.Contains(why, "do not cover the whole cost") || !strings.Contains(why, "the oldest send's 6h0m0s") {
		t.Fatalf("fees under the cost: %v %s", due, why)
	}
	f := base
	f.FeesWei = gwei(1_600_000) // the header's cost, not its deliveries'
	if due, _, _ := headerDue(f); due {
		t.Fatal("fees that cover the header alone bought it: the deliveries are part of the cost")
	}
	f.FeesWei = gwei(2_000_000)
	if due, draw, why := headerDue(f); !due || draw || !strings.Contains(why, "own fees") {
		t.Fatalf("fees that cover the whole cost: %v %v %s", due, draw, why)
	}
	f = base
	f.Decisions = 1
	if due, draw, why := headerDue(f); !due || draw || !strings.Contains(why, "decision") {
		t.Fatal("a waiting decision waited for fees, or drew on the budget")
	}
	// six hours: the budget covers what the fees lack, or it does not (a deficit carried draws nothing)
	f = base
	f.Oldest, f.Budget = 6*time.Hour, gwei(1_000_000)
	if due, draw, why := headerDue(f); !due || !draw || !strings.Contains(why, "the budget") {
		t.Fatalf("a six-hour send with the budget behind it: %v %v %s", due, draw, why)
	}
	f.Budget = gwei(999_999)
	if due, _, why := headerDue(f); due || !strings.Contains(why, "does not cover what its fees lack") {
		t.Fatalf("a six-hour send the budget cannot cover: %v %s", due, why)
	}
	f.Budget = new(big.Int).Neg(gwei(1))
	f.FeesWei = gwei(2_000_000)
	if due, draw, _ := headerDue(f); !due || draw {
		t.Fatal("a deficit held back sends whose own fees cover their whole cost")
	}
	f.FeesWei = gwei(1_999_999)
	if due, _, _ := headerDue(f); due {
		t.Fatal("a deficit was drawn on")
	}
	// the relayer: its batch re-emitted at the six hours (its lead early); its header proof by the fees alone
	f = base
	f.Oldest, f.Lead, f.AgeReemits = 6*time.Hour-updateLead, updateLead, true
	if due, draw, why := headerDue(f); !due || draw || !strings.Contains(why, "re-emitted") {
		t.Fatalf("the relayer's six-hour re-emit: %v %s", due, why)
	}
	f.AgeReemits = false
	if due, _, why := headerDue(f); due || !strings.Contains(why, "a header proof is bought by its fees alone") {
		t.Fatalf("a header proof at the six hours: %v %s", due, why)
	}
	f = base
	f.Unpriced = []string{"BUBBLE"}
	if _, _, why := headerDue(f); !strings.Contains(why, "no price for BUBBLE") {
		t.Fatal(why)
	}
	if due, _, _ := headerDue(dueFacts{FeesWei: new(big.Int), CostWei: new(big.Int)}); due {
		t.Fatal("nothing waits")
	}
}

// The model (E-M2b's measurements, M2-NOTES.md §6): an update priced at or above every measured one, staged past the
// gas cap's margin; a delivery by its leaf's Dispatch events.
func TestGasModel(t *testing.T) {
	for _, m := range []struct {
		n, k     int
		measured uint64
	}{{4, 3, 966_125}, {6, 5, 1_445_334}, {20, 14, 3_646_855}, {28, 19, 4_868_663}, {40, 27, 6_822_321}, {60, 41, 10_236_808}} {
		g := headerGas(m.n, m.k)
		if g < m.measured || g > m.measured*102/100 {
			t.Errorf("%d validators, %d signatures: the model's %d gas, measured %d", m.n, m.k, g, m.measured)
		}
	}
	// 100 validators sign with 67: one transaction measured 16,595,220 gas, past the cap with its 20% margin, so the
	// update is staged and pays the set's share twice
	if g := headerGas(100, 67); g < 16_595_220+headerGasBase {
		t.Errorf("100 validators: %d gas, not staged", g)
	}
	set := newFakeSet(3, 60, 60, 60, 60, 60, 60).set
	if headerGasOfSet(set) != headerGas(6, 5) {
		t.Errorf("gnoland-1's six at 60: %d, want the five signatures' %d", headerGasOfSet(set), headerGas(6, 5))
	}
	if deliveryGas(1, 0) != 311_300 || deliveryGas(4, 0) != 311_300+3*101_000 || deliveryGas(0, 0) != deliveryGas(1, 0) || deliveryGas(4, 4*451) != deliveryGas(4, 0) {
		t.Errorf("deliveries: %d %d", deliveryGas(1, 0), deliveryGas(4, 0))
	}
	// E3-GS2: a leaf's bytes past its Dispatch events are paid — E-M2b's 8,924-byte leaf of one Dispatch measured
	// 284,069 gas to verify against 138,909 for 451 bytes, and every byte past them is calldata (16 when not zero)
	if g, measured := deliveryGas(1, 8924), uint64(311_246+(284_069-138_909)+16*(8924-451)); g < measured || g > measured*102/100 {
		t.Errorf("a delivery from 8,924 bytes: the model's %d gas, measured %d", g, measured)
	}
}

// A fee in a route's base units at the config's price: 50 GNOT at 2.82e-5 ETH a GNOT is 1.41e-3 ETH.
func TestFeeWei(t *testing.T) {
	cfg := &Config{}
	cfg.Eth.Prices = map[string]float64{"GNOT": 0.0000282}
	w, ok := cfg.FeeWei("GNOT", 50_000_000)
	if !ok || w.String() != "1410000000000000" {
		t.Fatalf("%s %v", w, ok)
	}
	if _, ok := cfg.FeeWei("BUBBLE", 1); ok {
		t.Fatal("an unpriced token priced")
	}
}

// When the executor attempts HlAdmin's execute (§6.5): at maturity by the chain's clock; kind 5 under a hold at the
// later of its 7th day and day 21 of the earliest hold overlapping its pending life; a cancelled decision at any
// time; never while a hold, or for the 7-day group a lone flag, defers it.
func TestExecuteWhen(t *testing.T) {
	const factor = 8
	now := int64(1_800_000_000)
	week, d := 7*day/factor, day/factor
	st := func() *ethx.AdminState { return &ethx.AdminState{ClockFactor: factor} }
	flagged := st()
	flagged.Flags[0] = ethx.Flag{BlockNumber: 5, Time: uint64(now - 10)}
	cancelled := st()
	cancelled.Cancelled = [][2]uint64{{5, 5}}
	hold := func(start int64, holds ...ethx.Hold) *ethx.AdminState {
		s := st()
		s.Hold = ethx.Flag{BlockNumber: 9, Time: uint64(start)}
		s.Holds = holds
		return s
	}
	dec := func(kind uint8, delivered int64) *ethx.Decision {
		return &ethx.Decision{Number: 5, Kind: kind, DeliveredTime: uint64(delivered)}
	}
	cases := []struct {
		name string
		d    *ethx.Decision
		st   *ethx.AdminState
		act  bool
		why  string
	}{
		{"a mature kind 7", dec(7, now-week), st(), true, "mature"},
		{"an immature kind 7", dec(7, now-week+10), st(), false, "matures at"},
		{"a kind 7 under a hold", dec(7, now-week), hold(now - 2*d), false, "until it ends"},
		{"a kind 7 under a lone flag", dec(7, now-week), flagged, false, "a flag"},
		{"a kind 5 under a young hold: day 21 ahead", dec(5, now-week), hold(now - 2*d), false, "the floor"},
		{"a kind 5 under a hold past its 21st day", dec(5, now-week), hold(now - 22*d), true, "mature"},
		{"a kind 5, an earlier overlapping hold's day 21 passed", dec(5, now-week), hold(now-2*d, ethx.Hold{Start: uint64(now - 30*d), End: uint64(now - 5*d)}), true, "mature"},
		{"a kind 5, a hold that ended before its delivery does not count", dec(5, now-week), hold(now-2*d, ethx.Hold{Start: uint64(now - 30*d), End: uint64(now - 8*d)}), false, "the floor"},
		{"a kind 5 under a lone flag", dec(5, now-week), flagged, true, "mature"},
		{"a cancelled decision", dec(7, now-10), cancelled, true, "cancelled"},
		{"a cancel, at once", dec(1, now), st(), true, "mature"},
		{"an unpause after its day", dec(2, now-d), hold(now - d), true, "mature"},
		{"an unpause before its day", dec(2, now-d+1), st(), false, "matures at"},
		{"kind 4, reserved, mature", dec(4, now-week), st(), true, "mature"},
	}
	for _, c := range cases {
		act, why := executeWhen(c.d, c.st, now)
		if act != c.act || !strings.Contains(why, c.why) {
			t.Errorf("%s: %v (%s), want %v (%q)", c.name, act, why, c.act, c.why)
		}
	}
	if judgeExecuteErr(revert("NotMature", "")) != "wait" || judgeExecuteErr(revert("Deferred", "")) != "wait" || judgeExecuteErr(&ethx.FeeTooHigh{BaseFee: big.NewInt(1), Max: big.NewInt(0)}) != "fee" || judgeExecuteErr(errors.New("x")) != "fail" {
		t.Fatal("judgeExecuteErr")
	}
}

// A decision goes only when its number is the next HlAdmin expects.
func TestDecisionGate(t *testing.T) {
	for _, c := range []struct {
		seq, last uint64
		want      string
	}{{1, 0, "deliver"}, {2, 0, "wait"}, {1, 1, "past"}, {5, 4, "deliver"}, {3, 7, "past"}} {
		if got := decisionGate(c.seq, c.last); got != c.want {
			t.Errorf("%d/%d: %s", c.seq, c.last, got)
		}
	}
}

// The day cap: a parked send goes when the bucket — drained at the cap per day — has room for it; a younger send
// of its token only beside the park's reserved amount.
func TestCapGate(t *testing.T) {
	l := ethx.MintLimit{MaxPerMessage: 100, DayCap: 200, Used: 180, UsedAt: 1_000}
	park := &capPark{n: 5, amount: 60}
	if ok, _ := capGate(5, 60, true, park, l, 1_000, 86_400, 0); ok {
		t.Fatal("a park went into a full bucket")
	}
	if ok, _ := capGate(5, 60, true, park, l, 1_000+86_400*40/200, 86_400, 0); !ok {
		t.Fatal("a park held although 40 drained")
	}
	if ok, why := capGate(7, 10, false, park, l, 1_000+86_400*40/200, 86_400, 0); ok || !strings.Contains(why, "behind gno #5") {
		t.Fatalf("a younger send took the park's room: %s", why)
	}
	if ok, _ := capGate(3, 10, false, park, l, 1_000, 86_400, 0); !ok {
		t.Fatal("an older send held behind a younger park")
	}
	if !overLiveLimits(150, l) || overLiveLimits(100, l) {
		t.Fatal("overLiveLimits")
	}
}

// The plan toward a target: the target itself in the expected set; the announcing header with its next set when a
// change lies between; the first header of the announced set once the client waits for it.
func TestPlanUpdate(t *testing.T) {
	g := newFakeGno(testMailbox, 200)
	a, b := g.sets[1], newFakeSet(2, 60, 60, 60, 60, 60, 60, 60)
	g.sets[150] = b
	ctx := context.Background()
	e := newFakeEth(g, 50)
	cs, err := ethx.ReadClient(ctx, e, ethClient, ethx.Latest)
	if err != nil {
		t.Fatal(err)
	}
	p, err := planUpdate(ctx, g, cs, 120)
	if err != nil || p.Target != 120 || p.Announcing {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = planUpdate(ctx, g, cs, 200)
	if err != nil || p.Target != 149 || !p.Announcing || len(p.NextSet) != 7 {
		t.Fatalf("%+v %v", p, err)
	}
	e.trust(149) // the client took the announcing header: it waits for the new set
	cs, _ = ethx.ReadClient(ctx, e, ethClient, ethx.Latest)
	if !cs.Announced() {
		t.Fatal("the announcing header did not announce")
	}
	p, err = planUpdate(ctx, g, cs, 180)
	if err != nil || p.Target != 180 || p.Announcing {
		t.Fatalf("%+v %v", p, err)
	}
	_ = a
	// a change right after a trusted header that announced nothing cannot be followed (no reset on Ethereum)
	e.trust(149)
	e.nextSetHash = [32]byte{}
	cs, _ = ethx.ReadClient(ctx, e, ethClient, ethx.Latest)
	if _, err := planUpdate(ctx, g, cs, 180); err == nil || !strings.Contains(err.Error(), "cannot follow") {
		t.Fatalf("%v", err)
	}
	var e0, o [32]byte
	o[0] = 1
	for change := int64(2); change <= 40; change++ {
		calls := 0
		x, err := firstDiffering(1, 40, e0, func(h int64) ([32]byte, error) {
			calls++
			if h >= change {
				return o, nil
			}
			return e0, nil
		})
		if err != nil || x != change || calls > 8 {
			t.Fatalf("change at %d: %d %v (%d reads)", change, x, err, calls)
		}
	}
	_ = gnolight.Validator{}
}

// A float's runway: the planned rate until a faster spend is observed; under 7 days it alarms.
func TestFloats(t *testing.T) {
	var w floatWatch
	a := &Alarmer{Source: "test"}
	now := time.Unix(1_900_000_000, 0)
	bal := big.NewInt(1e18) // 1 ETH at the relayers' planned 0.003 ETH a day: years
	read := func(context.Context) (*big.Int, error) { return new(big.Int).Set(bal), nil }
	line := w.watch(context.Background(), now, "relayer-eth", read, nil, a)
	if _, ok := a.Standing()["runway:relayer-eth"]; ok || !strings.Contains(line, "relayer-eth holds 1.000000 ETH") {
		t.Fatal(line)
	}
	// a day of spending 0.2 ETH: five days left at the observed rate
	for i := 1; i <= 24; i++ {
		bal.Sub(bal, big.NewInt(200_000_000_000_000_000/24))
		w.watch(context.Background(), now.Add(time.Duration(i)*time.Hour), "relayer-eth", read, nil, a)
	}
	if _, ok := a.Standing()["runway:relayer-eth"]; !ok {
		t.Fatal("no runway alarm at five days")
	}
	// the override lowers nothing below the observed rate
	if days, rate := runway("relayer-eth", w.f.Samples, map[string]float64{"relayer-eth": 1}); rate < 1e17 || days > 7 {
		t.Fatalf("%v %v", days, rate)
	}
}

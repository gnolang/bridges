package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// The integration lane I2 of the round-4 fold: the relayer's Claim batch against the gno client's gas ceiling (F15d's
// measure: Claim(20) ≈140M gas on a v1.5.1 node at 400 claims, the client simulating a call under the relayer's 45M and
// once more under four times it, 180M; p/hlroute's first Claim after Launch walks every queue, 429M at 400 queues).

// i2ClaimHost is the relayer as the inbound job sees it, with the gno client's gas rule on Claim: a Claim(n) pays
// min(n, open) claims using cost(paid) gas; the client simulates under the gas asked, once more under four times it
// when that does not reach the block's (gno.Client.Call), and a Claim past both runs out of gas.
type i2ClaimHost struct {
	*m3bHost
	cost  func(paid int) int64
	open  int
	paid  int
	calls []string
	oog   int
}

func (h *i2ClaimHost) CallGas() int64 { return 45_000_000 }
func (h *i2ClaimHost) TryCall(ctx context.Context, key, pkg, fn string, args ...string) (*gno.Result, error) {
	return h.TryCallGas(ctx, key, pkg, fn, 45_000_000, args...)
}
func (h *i2ClaimHost) TryCallGas(ctx context.Context, key, pkg, fn string, gas int64, args ...string) (*gno.Result, error) {
	if fn != "Claim" {
		return h.m3bHost.TryCallGas(ctx, key, pkg, fn, gas, args...)
	}
	n, _ := strconv.Atoi(args[0])
	paid := min(n, h.open)
	used := h.cost(paid)
	h.calls = append(h.calls, fmt.Sprintf("Claim(%d)@%dM", n, gas/1_000_000))
	if used > gas && (4*gas > gno.MaxBlockGas || used > 4*gas) {
		h.oog++
		return nil, &gno.CallError{Pkg: pkg, Fn: fn, Err: errors.New("exit status 1"), Output: fmt.Sprintf("--= Error =--\nData: out of gas error\nMsg Traces:\n    0  gas used: %d (gas wanted %d)", min(used, 4*gas), gas)}
	}
	h.open -= paid
	h.paid += paid
	h.g.payRoute(pkg, paid) // each claim paid whole, a fee's worth at least: the route's json moves as the realm's does
	return &gno.Result{Hash: fmt.Sprintf("TX%d", len(h.calls)), GasUsed: used, Events: []gno.Event{{Type: "Claimed", Attrs: map[string]string{"count": strconv.Itoa(paid)}}}}, nil
}

// payRoute moves a route's json as a Claim paying n claims a fee each does (E5-GS1: the relayer reads it right after a
// Claim to see what the Claim paid): paidTotal up by n fees, openClaims down by n.
func (g *m3bGnoNode) payRoute(pkg string, n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var j map[string]any
	if err := json.Unmarshal([]byte(g.renders[pkg+":json"]), &j); err != nil {
		return
	}
	num := func(k string) int64 { f, _ := j[k].(float64); return int64(f) }
	j["paidTotal"] = num("paidTotal") + int64(n)*num("fee")
	j["openClaims"] = max(num("openClaims")-int64(n), 0)
	b, _ := json.Marshal(j)
	g.renders[pkg+":json"] = string(b)
}

func i2ClaimRig(t *testing.T, cost func(int) int64) (*inboundRig, *i2ClaimHost) {
	r := newInboundRig(t)
	r.gno.set(m3bNS+"hlgnot", "json", `{"live":true,"lock":100000000,"owed":2000000000,"openClaims":400,"meter":5000000000,"fee":5000000}`)
	return r, &i2ClaimHost{m3bHost: r.host, cost: cost, open: 400}
}

func (r *inboundRig) i2Ticks(h *i2ClaimHost, n int) {
	r.t.Helper()
	for i := 0; i < n; i++ {
		_ = r.job.tick(context.Background(), h) // an out-of-gas Claim is the tick's error; the job goes on
	}
}

// A Claim(20) past the client's 180M — 60M and 7M a claim, 200M — ran out of gas every minute for good, paying nothing
// and alarming nothing: the batch halves on an out-of-gas (Claim(10), 130M, fits), tries one more (Claim(11), 137M,
// past the 135M room), and stays at the most that fits — never again at a size known over.
func TestI2ClaimBatchFitsTheClientsGas(t *testing.T) {
	r, h := i2ClaimRig(t, func(p int) int64 { return 60_000_000 + 7_000_000*int64(p) })
	r.i2Ticks(h, 6)
	t.Logf("Claim(20) at 200M: calls %v; paid %d; out of gas %d", h.calls, h.paid, h.oog)
	if h.paid == 0 || h.oog != 1 || strings.Join(h.calls, " ") != "Claim(20)@45M Claim(10)@45M Claim(11)@45M Claim(10)@45M Claim(10)@45M Claim(10)@45M" {
		t.Fatalf("the Claim batch did not settle at the most the client's gas fits: calls %v, paid %d", h.calls, h.paid)
	}
}

// The review of I2's first sizing (finding 2): the batch grew only by its projection (n × room / used, rounded down),
// which over-counts a call's fixed cost — a Claim(1) using more than half the room (100M and 7M a claim: 107M) never grew
// again, one claim a transaction for good. It grows by one at least while a full batch fits, to the most that fits
// (Claim(5), 135M), stopping below the size that went over.
func TestI2ClaimBatchGrowsPastAHighFixedCost(t *testing.T) {
	r, h := i2ClaimRig(t, func(p int) int64 { return 100_000_000 + 7_000_000*int64(p) })
	r.job.st.Claims = map[string]*inClaim{"GNOT": {Batch: 1}} // where a Claim of one after the first walk left it
	r.i2Ticks(h, 8)
	t.Logf("a fixed cost of 100M: calls %v; out of gas %d", h.calls, h.oog)
	if strings.Join(h.calls, " ") != "Claim(1)@45M Claim(2)@45M Claim(3)@45M Claim(4)@45M Claim(5)@45M Claim(6)@45M Claim(5)@45M Claim(5)@45M" || h.oog != 0 {
		t.Fatalf("the batch did not grow past a Claim of one: %v", h.calls)
	}
}

// A Claim that fits the 180M but not three quarters of it — 40M and 5.5M a claim, Claim(20) 150M — is the headroom F15d
// measured shrinking as the state grows: the next batch is scaled to the room (Claim(18), then 17: 133.5M), before any
// call runs out; a lighter Claim grows the batch back to twenty.
func TestI2ClaimBatchShrinksBeforeItRunsOut(t *testing.T) {
	r, h := i2ClaimRig(t, func(p int) int64 { return 40_000_000 + 5_500_000*int64(p) })
	r.i2Ticks(h, 4)
	t.Logf("Claim(20) at 150M: calls %v; out of gas %d", h.calls, h.oog)
	if strings.Join(h.calls, " ") != "Claim(20)@45M Claim(18)@45M Claim(17)@45M Claim(17)@45M" || h.oog != 0 {
		t.Fatalf("the batch was not sized to three quarters of the client's ceiling: %v", h.calls)
	}
	h.cost = func(p int) int64 { return 20_000_000 + 4_000_000*int64(p) } // the state shrank
	r.i2Ticks(h, 2)
	if last := h.calls[len(h.calls)-1]; last != "Claim(20)@45M" {
		t.Fatalf("the batch did not grow back: %v", h.calls)
	}
}

// p/hlroute's first Claim after Launch walks every queue (ResetMeters: 429M at 400 queues, F15d) whatever the batch:
// the batch halves to one, then the gas is raised four times (simulated under 180M, then 720M), alarmed; once a Claim
// goes the alarm clears, and once a Claim fits the relayer's own room again the gas goes back to it and the batch
// grows.
func TestI2ClaimOfOneRaisesTheGas(t *testing.T) {
	walked := false
	r, h := i2ClaimRig(t, nil)
	h.cost = func(p int) int64 {
		if !walked {
			return 429_000_000
		}
		return 60_000_000 + 7_000_000*int64(p)
	}
	for i := 0; i < 6 && h.paid == 0; i++ {
		r.i2Ticks(h, 1)
		if i == 4 {
			if _, ok := r.job.alarm.Standing()["claim:gas:GNOT"]; !ok {
				t.Fatalf("a Claim of one out of gas was not alarmed: calls %v", h.calls)
			}
		}
	}
	walked = h.paid > 0
	r.i2Ticks(h, 3)
	t.Logf("the walk at 429M: calls %v; paid %d; alarms %v", h.calls, h.paid, r.job.alarm.Standing())
	want := "Claim(20)@45M Claim(10)@45M Claim(5)@45M Claim(2)@45M Claim(1)@45M Claim(1)@180M Claim(2)@180M Claim(3)@45M Claim(5)@45M"
	if strings.Join(h.calls, " ") != want {
		t.Fatalf("the walk's Claim: calls %v, want %s", h.calls, want)
	}
	if _, ok := r.job.alarm.Standing()["claim:gas:GNOT"]; ok {
		t.Fatalf("the alarm stands after the Claim went: %v", r.job.alarm.Standing())
	}
}

// The review of I2's first sizing (finding 6): a Claim of one past the block's gas — raised to a quarter of it, the
// client's retry then the block's — is stuck: it was simulated under 750M and 3B every minute for good, for nothing; it
// is tried hourly, alarmed as such.
func TestI2ClaimPastTheBlockIsTriedHourly(t *testing.T) {
	clock := time.Now()
	r, h := i2ClaimRig(t, func(int) int64 { return 3_100_000_000 })
	r.job.now = func() time.Time { return clock }
	r.i2Ticks(h, 10)
	text := r.job.alarm.Standing()["claim:gas:GNOT"]
	t.Logf("past the block: calls %v; alarm %q", h.calls, text)
	if strings.Join(h.calls, " ") != "Claim(20)@45M Claim(10)@45M Claim(5)@45M Claim(2)@45M Claim(1)@45M Claim(1)@180M Claim(1)@720M Claim(1)@750M" || !strings.Contains(text, "payouts stand still") {
		t.Fatalf("a Claim past the block's gas, stuck: calls %v, alarm %q", h.calls, text)
	}
	clock = clock.Add(61 * time.Minute)
	r.i2Ticks(h, 1)
	if last := h.calls[len(h.calls)-1]; len(h.calls) != 9 || last != "Claim(1)@750M" {
		t.Fatalf("the stuck Claim was not tried again after an hour: %v", h.calls)
	}
}

// The review's finding 5: claim:gas:<symbol> ended only with this relayer's own Claim — once the route had no open claim
// (or the other relayer paid them), nothing cleared it. It ends with the payouts moving, or nothing left to pay.
func TestI2ClaimGasAlarmEndsWhenTheRouteIsIdle(t *testing.T) {
	r, h := i2ClaimRig(t, func(int) int64 { return 429_000_000 }) // every Claim walks every queue
	r.i2Ticks(h, 5)                                               // halved to one, out of gas: the gas is raised, alarmed
	if _, ok := r.job.alarm.Standing()["claim:gas:GNOT"]; !ok {
		t.Fatalf("a Claim of one out of gas was not alarmed: calls %v", h.calls)
	}
	r.gno.set(m3bNS+"hlgnot", "json", `{"live":true,"lock":100000000,"owed":0,"openClaims":0,"meter":5000000000,"fee":5000000}`)
	r.i2Ticks(h, 1)
	if _, ok := r.job.alarm.Standing()["claim:gas:GNOT"]; ok {
		t.Fatalf("claim:gas stands over a route with no open claim: %v", r.job.alarm.Standing())
	}
}

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// Lane F17a (audit round 5's go-spend, E5-GS1): the relayer's Claim loop against p/hlroute's payouts in installments.

// hlrouteModel is p/hlroute's payouts as the relayer meets them (realm/p/hlroute/v0/hlroute.gno: Claim, Meter.refill,
// queue): a bridge meter of the cap a day, each recipient's of a quarter of it, a recipient's empty at its first
// credit; Claim(n) takes recipients in turn, at most 4n visits, paying each one's oldest open claim — the fee first,
// whole, then as much as both meters hold — and skipping a dry one. later is the rule for an installment after the
// first: p/hlroute today pays whatever refilled (avail > 0); the realm half of the fix (lane F17c) pays only once
// min(remaining, fee) has.
type hlrouteModel struct {
	mu        sync.Mutex
	cap, fee  int64
	lock      int64
	bridge    modelMeter
	recips    map[string]*modelRecip
	cursor    string
	paidTotal int64
	claims    int64
	later     func(avail, remaining, fee int64) bool
}

type modelMeter struct {
	level, at, frac int64
}

// refill is hlroute.Meter.refill: rate a day, at most ceil, the elapsed time capped at a day.
func (m *modelMeter) refill(now, rate, ceil int64) {
	if now <= m.at {
		return
	}
	el := now - m.at
	if el > 86_400 {
		el, m.frac = 86_400, 0
	}
	num := (rate%86_400)*el + m.frac
	m.level += (rate/86_400)*el + num/86_400
	m.frac = num % 86_400
	if m.level > ceil {
		m.level, m.frac = ceil, 0
	}
	m.at = now
}

type modelRecip struct {
	m    modelMeter
	open []*modelClaim
}

type modelClaim struct{ amount, paid int64 }

func newHlrouteModel(cap, fee, lock int64, now int64) *hlrouteModel {
	return &hlrouteModel{cap: cap, fee: fee, lock: lock, bridge: modelMeter{level: cap, at: now}, recips: map[string]*modelRecip{},
		later: func(avail, _, _ int64) bool { return avail > 0 }}
}

// credit is a claim to a recipient: a fresh recipient's meter starts empty now (hlroute's queue), an old one's is full.
func (m *hlrouteModel) credit(to string, amount, now int64, fresh bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.recips[to]
	if r == nil {
		r = &modelRecip{m: modelMeter{at: now}}
		if !fresh {
			r.m.level = m.cap / 4
		}
		m.recips[to] = r
	}
	r.open = append(r.open, &modelClaim{amount: amount})
	m.claims++
}

func (m *hlrouteModel) openClaims() int64 {
	n := int64(0)
	for _, r := range m.recips {
		n += int64(len(r.open))
	}
	return n
}

// next is the recipient after the cursor among those with an open claim, wrapping ("" for none).
func (m *hlrouteModel) next() string {
	var ks []string
	for k, r := range m.recips {
		if len(r.open) > 0 {
			ks = append(ks, k)
		}
	}
	if len(ks) == 0 {
		return ""
	}
	sort.Strings(ks)
	for _, k := range ks {
		if k > m.cursor {
			return k
		}
	}
	return ks[0]
}

// claim is hlroute's State.Claim at now: the claims it paid something on.
func (m *hlrouteModel) claim(n int, now int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	paid := 0
	for visits := 0; paid < n && visits < 4*n; visits++ {
		to := m.next()
		if to == "" {
			break
		}
		m.cursor = to
		r := m.recips[to]
		c := r.open[0]
		m.bridge.refill(now, m.cap, m.cap)
		r.m.refill(now, m.cap/4, m.cap/4)
		avail := min(m.bridge.level, r.m.level, m.lock)
		fee := int64(0)
		if c.paid == 0 {
			fee = min(m.fee, c.amount)
			if avail < fee || avail <= 0 {
				continue
			}
		} else if !m.later(avail, c.amount-c.paid, m.fee) {
			continue
		}
		pay := min(c.amount-c.paid-fee, avail-fee)
		total := fee + pay
		c.paid += total
		m.bridge.level -= total
		r.m.level -= total
		m.paidTotal += total
		m.lock -= total
		if c.paid == c.amount {
			r.open = r.open[1:]
		}
		paid++
	}
	return paid
}

// json is the route's Render("json") as the job reads it.
func (m *hlrouteModel) json(now int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bridge.refill(now, m.cap, m.cap)
	b, _ := json.Marshal(map[string]any{"live": true, "lock": m.lock, "cap": m.cap, "fee": m.fee, "meter": m.bridge.level,
		"openClaims": m.openClaims(), "claims": m.claims, "paidTotal": m.paidTotal})
	return string(b)
}

// paidInFull says whether every claim of a recipient is paid in full.
func (m *hlrouteModel) paidInFull(to string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.recips[to]
	return r != nil && len(r.open) == 0
}

// claimModelHost is a relayer over the model: Claim is the route's, each one 28.5M gas (the GNOT route's measured
// installment, audit round 5's claim/run-gnot.out), the route's json the model's after it.
type claimModelHost struct {
	*m3bHost
	m     *hlrouteModel
	pkg   string
	clock *time.Time
	calls int
	gas   int64
}

func (h *claimModelHost) render() { h.g.set(h.pkg, "json", h.m.json(h.clock.Unix())) }

func (h *claimModelHost) TryCallGas(ctx context.Context, key, pkg, fn string, gas int64, args ...string) (*gno.Result, error) {
	if fn != "Claim" || pkg != h.pkg {
		return h.m3bHost.TryCallGas(ctx, key, pkg, fn, gas, args...)
	}
	n, _ := strconv.Atoi(args[0])
	paid := h.m.claim(n, h.clock.Unix())
	h.calls++
	h.gas += gno.GasFor(28_488_476)
	h.render()
	return &gno.Result{Hash: fmt.Sprintf("TX%d", h.calls), GasUsed: 28_488_476, Events: []gno.Event{{Type: "Claimed", Attrs: map[string]string{"count": strconv.Itoa(paid)}}}}, nil
}

const gnotUnit = 1_000_000

// claimModelRig is the inbound job over the model at the GNOT route's launch values: a fee of 20 GNOT, the cap at its
// 10,000 GNOT floor (a recipient's meter 2,500 GNOT a day: a fee's worth in ≈11.5 minutes).
func claimModelRig(t *testing.T, clock *time.Time, backup bool) (*inboundRig, *claimModelHost) {
	r := newInboundRig(t)
	m := newHlrouteModel(10_000*gnotUnit, 20*gnotUnit, 1_000_000*gnotUnit, clock.Unix())
	h := &claimModelHost{m3bHost: &m3bHost{g: r.gno, notes: map[string]string{}, act: true, backup: backup}, m: m, pkg: m3bNS + "hlgnot", clock: clock}
	r.job.now = func() time.Time { return *clock }
	h.render()
	return r, h
}

// runClaims ticks the job every tick (the relayer's 3 s and a gnoland-1 block) for d, the route's json read fresh each
// tick, and answers when the recipient's claims were all paid (0 when not).
func runClaims(r *inboundRig, h *claimModelHost, clock *time.Time, d time.Duration, to string) time.Duration {
	tick := 3*time.Second + 3430*time.Millisecond
	start := *clock
	var done time.Duration
	for elapsed := time.Duration(0); elapsed < d; elapsed += tick {
		h.render()
		_ = r.job.tick(context.Background(), h)
		if done == 0 && h.m.paidInFull(to) {
			done = clock.Sub(start)
		}
		*clock = clock.Add(tick)
	}
	return done
}

// E5-GS1: one honest transfer of 1,000 GNOT to a fresh recipient is paid in installments — its meter starts empty and
// refills 2,500 GNOT a day, so the fee comes ≈11.5 minutes after the credit and the rest over ≈9.4 hours. Each Claim
// that paid a sliver reset the backoff, so the relayer called Claim every tick for the whole claim: ≈5,300 calls,
// ≈185 GNOT of the gno key's gas against the 20 GNOT fee. A Claim counts as paying only by a fee's worth a claim, and
// the next waits for the meters to refill one: the claim is paid as fast as the meters allow, in ≈50 calls.
func TestF17aInstallmentsAreClaimedAtTheRefill(t *testing.T) {
	for _, c := range []struct {
		name  string
		later func(avail, remaining, fee int64) bool
	}{
		{"p/hlroute today: a later installment pays whatever refilled", nil},
		{"the realm half (F17c): a later installment only once min(remaining, fee) refilled", func(avail, remaining, fee int64) bool { return avail >= min(remaining, fee) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			clock := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
			r, h := claimModelRig(t, &clock, false)
			if c.later != nil {
				h.m.later = c.later
			}
			h.m.credit("g1fresh", 1_000*gnotUnit, clock.Unix(), true)
			done := runClaims(r, h, &clock, 24*time.Hour, "g1fresh")
			gnot := float64(h.gas) * gno.MinGasPrice / gnotUnit
			t.Logf("a 1,000 GNOT claim to a fresh recipient: %d Claim calls (%.2f GNOT of the gno key's gas), paid in full after %s; the job's note: %s", h.calls, gnot, done.Round(time.Minute), h.notes["claim:GNOT"])
			// the meters allow it in 1,000 ÷ 2,500 of a day ≈ 9.6 hours from the credit; a fee's worth a call is ≈50 calls
			if done == 0 || done > 11*time.Hour {
				t.Fatalf("the claim was not paid as the meters allow: done after %s", done)
			}
			if h.calls > 120 {
				t.Fatalf("%d Claim calls (%.1f GNOT of gas) for one claim paid in installments: the relayer calls Claim every tick while slivers refill", h.calls, gnot)
			}
		})
	}
}

// The cadence costs no latency where the meters hold enough: twenty-five recipients whose queues are old (their meters
// full) each get a claim of 100 GNOT, and every one is paid within a few ticks — a full batch is followed at once.
func TestF17aPayableClaimsArePaidAtOnce(t *testing.T) {
	clock := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	r, h := claimModelRig(t, &clock, false)
	for i := 0; i < 25; i++ {
		h.m.credit(fmt.Sprintf("g1old%02d", i), 100*gnotUnit, clock.Unix(), false)
	}
	runClaims(r, h, &clock, time.Minute, "")
	if open := h.m.openClaims(); open != 0 || h.calls > 4 {
		t.Fatalf("25 payable claims: %d still open after a minute, %d Claim calls (%s)", open, h.calls, h.notes["claim:GNOT"])
	}
	// a new claim to an old recipient, once the route is idle, is paid at the next tick
	runClaims(r, h, &clock, 30*time.Minute, "")
	calls := h.calls
	h.m.credit("g1old03", 100*gnotUnit, clock.Unix(), false)
	runClaims(r, h, &clock, 30*time.Second, "")
	if h.m.openClaims() != 0 || h.calls != calls+1 {
		t.Fatalf("a new payable claim on an idle route: %d open, %d more calls", h.m.openClaims(), h.calls-calls)
	}
}

// The backup acts on what has been possible for a minute and nobody took: with the primary pacing installments at the
// refill, a minute after each of its Claims would look like one — the backup counts the primary's payout as its pass
// and waits the refill after it. It makes no call while the primary runs, and takes over once the primary stops.
func TestF17aBackupWaitsThePrimarysRefill(t *testing.T) {
	clock := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	rp, hp := claimModelRig(t, &clock, false)
	rb := newInboundRig(t)
	rb.job.now = func() time.Time { return clock }
	hb := &claimModelHost{m3bHost: &m3bHost{g: rp.gno, notes: map[string]string{}, backup: true}, m: hp.m, pkg: hp.pkg, clock: &clock}
	ready := map[string]time.Time{} // the backup's act clocks (Relayer.act): possible since, reset by Forget
	act := func(key string) bool {
		if _, ok := ready[key]; !ok {
			ready[key] = clock
		}
		return clock.Sub(ready[key]) >= BackupOffset
	}
	bh := &actHost{claimModelHost: hb, act: act, forget: func(key string) { delete(ready, key) }}
	hp.m.credit("g1fresh", 1_000*gnotUnit, clock.Unix(), true)
	tick := 3*time.Second + 3430*time.Millisecond
	step := func(primary bool) {
		hp.render()
		if primary {
			_ = rp.job.tick(context.Background(), hp)
		}
		_ = rb.job.tick(context.Background(), bh)
		clock = clock.Add(tick)
	}
	for elapsed := time.Duration(0); elapsed < 3*time.Hour; elapsed += tick {
		step(true)
	}
	// (at most one: in the dry minutes before the first installment, which no payout shows it)
	if hb.calls > 1 {
		t.Fatalf("the backup called Claim %d times beside the primary's %d (its note: %s)", hb.calls, hp.calls, hb.notes["claim:GNOT"])
	}
	primary, backup := hp.calls, hb.calls
	for elapsed := time.Duration(0); elapsed < 30*time.Minute; elapsed += tick {
		step(false)
	}
	if hb.calls < 2 || hp.calls != primary {
		t.Fatalf("the primary stopped: the backup made %d calls in half an hour (%s)", hb.calls, hb.notes["claim:GNOT"])
	}
	t.Logf("three hours: the primary %d calls, the backup %d; then the backup took over: %d calls in all after half an hour", primary, backup, hb.calls)
}

// actHost is a host whose primary/backup rule is the test's (the relayer's act and forget, by key).
type actHost struct {
	*claimModelHost
	act    func(key string) bool
	forget func(key string)
}

func (h *actHost) Act(key string) bool { return h.act(key) }
func (h *actHost) Forget(key string)   { h.forget(key) }

package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func testMonitor(f *fakeChain) *Monitor {
	cfg := &Config{}
	cfg.Gno.Mailbox = testMailbox
	cfg.Gno.WatchKey = "watch"
	cfg.Gno.WatchAddress = "g1watch"
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute, Token: "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"}}
	now := time.Unix(1_800_000_000, 0)
	m := &Monitor{Cfg: cfg, Alarm: &Alarmer{Source: "test"}, own: f, watch: f, now: func() time.Time { return now }}
	if err := m.init(); err != nil {
		panic(err)
	}
	m.now = func() time.Time { return now }
	return m
}

func mailboxWith(flags ...string) *mailboxJSON {
	mb := &mailboxJSON{Seats: []string{"g1a", "g1b", "g1c", "g1d", "g1watch", "g1f"}}
	mb.Routes = map[string]routeFlags{testRoute: {Flags: flags}}
	return mb
}

// An invariant break flags credits and payouts on the route through the watch key — never sends — and does
// not set a flag that stands already; while the own node is the odd one out it alarms instead.
func TestMonitorBreakFlags(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	ctx := context.Background()
	mb := mailboxWith()
	m.seat = seatOf(mb.Seats, m.Cfg.Gno.WatchAddress)
	if m.seat != 5 {
		t.Fatalf("seat %d", m.seat)
	}
	broken := Identity{Symbol: "BUBBLE", Lock: 300, Supply: 300, Owed: 200}
	m.judge(ctx, m.Cfg.Routes[0], mb, broken)
	calls := f.callsOf(testMailbox + ".SetFlag")
	want := []string{testMailbox + ".SetFlag(" + testRoute + ",credits,)", testMailbox + ".SetFlag(" + testRoute + ",payouts,)"}
	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("flags set: %v, want %v", calls, want)
	}
	if n := len(f.callsOf(testMailbox + ".SetFlag(" + testRoute + ",sends")); n != 0 {
		t.Fatal("sends flagged on a break")
	}
	// Standing flags are not set again; a cleared one is re-asserted.
	mb = mailboxWith("credits by seat 5 (g1watch) at 1", "payouts by seat 5 (g1watch) at 1")
	m.judge(ctx, m.Cfg.Routes[0], mb, broken)
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 2 {
		t.Fatalf("standing flags set again: %d", n)
	}
	mb = mailboxWith("credits by seat 5 (g1watch) at 1")
	m.judge(ctx, m.Cfg.Routes[0], mb, broken)
	if n := len(f.callsOf(testMailbox + ".SetFlag(" + testRoute + ",payouts")); n != 2 {
		t.Fatalf("a cleared payouts flag was not re-asserted: %d", n)
	}
	// Own node odd one out: alarm only.
	m.state.OddNode = true
	f.calls = nil
	m.judge(ctx, m.Cfg.Routes[0], mailboxWith(), broken)
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 0 {
		t.Fatalf("flagged while the own node is the odd one out: %d", n)
	}
	if _, ok := m.Alarm.Standing()["identity:BUBBLE"]; !ok {
		t.Fatal("no alarm while the own node is the odd one out")
	}
	// A short right side alarms after three polls and never flags.
	m.state.OddNode = false
	short := Identity{Symbol: "BUBBLE", Lock: 300, Supply: 300, Lost: 100}
	for i := 0; i < 3; i++ {
		m.judge(ctx, m.Cfg.Routes[0], mailboxWith(), short)
	}
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 0 {
		t.Fatalf("a short right side set a flag: %d", n)
	}
	if a, ok := m.Alarm.Standing()["identity:BUBBLE"]; !ok || !strings.Contains(a, "alarm only") {
		t.Fatalf("no alarm for a short right side: %v", m.Alarm.Standing())
	}
	// Held: the alarm clears.
	m.judge(ctx, m.Cfg.Routes[0], mailboxWith(), Identity{Symbol: "BUBBLE", Lock: 300, Supply: 300})
	if _, ok := m.Alarm.Standing()["identity:BUBBLE"]; ok {
		t.Fatal("the alarm did not clear once the identity held")
	}
}

// Without a watch seat a break alarms and sets nothing.
func TestMonitorNoSeat(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	m.Cfg.Gno.WatchAddress = "g1nobody"
	mb := mailboxWith()
	m.seat = seatOf(mb.Seats, m.Cfg.Gno.WatchAddress)
	m.judge(context.Background(), m.Cfg.Routes[0], mb, Identity{Symbol: "BUBBLE", Lock: 300, Supply: 301})
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 0 {
		t.Fatalf("flag set without a seat: %d", n)
	}
	if _, ok := m.Alarm.Standing()["flag:"+testRoute+":credits"]; !ok {
		t.Fatal("no alarm about the missing seat")
	}
}

// A credited claim whose recipient or amount differs from its Solana dispatch record is a break (verifier
// credits only); a sweep is compared by amount.
func TestMonitorDivertedCredit(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	ctx := context.Background()
	mb := mailboxWith()
	m.seat = 5
	m.state.Index["aa"] = &dispatchRec{Symbol: "BUBBLE", Slot: 10, To: "g1alice", Amount: 200, Credited: 90}
	m.state.Index["bb"] = &dispatchRec{Symbol: "BUBBLE", Slot: 11, To: "g1bob", Amount: 300, Credited: 91}
	m.state.Index["cc"] = &dispatchRec{Symbol: "BUBBLE", Slot: 12, To: "g1carol", Amount: 5, Credited: 92}
	m.state.Index["dd"] = &dispatchRec{Symbol: "BUBBLE", Slot: 13, To: "g1dan", Amount: 7, Credited: 93}
	m.state.Index["ee"] = &dispatchRec{Symbol: "BUBBLE", Slot: 14, Bad: true, Amount: 9, Credited: 94} // a sweep entry paid by GovDAO's decision: gone (M1f)
	f.renders[testRoute+":claim/aa"] = `{"id":"aa","to":"g1alice","amount":200,"paid":0,"state":"open","by":"signers","at":1,"seq":1}`
	f.renders[testRoute+":claim/bb"] = `{"id":"bb","to":"g1mallory","amount":300,"paid":0,"state":"open","by":"signers","at":1,"seq":1}`
	f.renders[testRoute+":claim/cc"] = `{"id":"cc","swept":5}`
	f.renders[testRoute+":claim/dd"] = `{"id":"dd","to":"g1other","amount":7,"paid":0,"state":"open","by":"vote","at":1,"seq":1}`
	f.renders[testRoute+":claim/ee"] = `null`
	if err := m.divertedCredits(ctx, mb, 100); err != nil {
		t.Fatal(err)
	}
	calls := f.callsOf(testMailbox + ".SetFlag")
	if len(calls) != 2 {
		t.Fatalf("flags: %v", calls)
	}
	if _, ok := m.Alarm.Standing()["diverted:bb:flag"]; !ok {
		t.Fatalf("no flag alarm for the diverted credit: %v", m.Alarm.Standing())
	}
	if _, ok := m.Alarm.Standing()["diverted:ee:flag"]; ok {
		t.Fatal("a paid sweep entry, gone from the realm, was flagged")
	}
	for _, id := range []string{"aa", "cc", "dd", "ee"} {
		if !m.state.Index[id].Checked {
			t.Fatalf("%s not checked", id)
		}
	}
	// Checked once: no second look.
	f.calls = nil
	_ = m.divertedCredits(ctx, mb, 101)
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 0 {
		t.Fatalf("checked again: %d", n)
	}
}

// Own node odd one out flags credits on every route (never payouts); the public endpoint odd alarms only.
func TestMonitorSources(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	ctx := context.Background()
	m.Cfg.Gno.Sources = []string{"http://public", "http://third"}
	f.renders[testMailbox+":json"] = `{"domain":1198419796,"hook":"","address":"","count":0,"root":"","delivered":0,"live":true,"launchedAt":1,"launchOrigin":1399811150,"decisions":0,"signers":{"keys":[],"threshold":0,"groupA":[],"groupB":[]},"verifiers":{},"registered":[],"seats":["g1a","g1b","g1c","g1d","g1watch","g1f"],"routes":{}}`
	// The sources are real clients in Run; here the verdict is applied directly.
	m.applySources(ctx, 100, OwnNodeOdd, "a", "b", "b")
	calls := f.callsOf(testMailbox + ".SetFlag")
	if len(calls) != 1 || calls[0] != testMailbox+".SetFlag("+testRoute+",credits,)" {
		t.Fatalf("flags on own node odd: %v", calls)
	}
	if !m.state.OddNode {
		t.Fatal("OddNode not recorded")
	}
	f.calls = nil
	m.applySources(ctx, 101, PublicOdd, "a", "b", "a")
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 0 {
		t.Fatalf("flagged on the public endpoint being odd: %d", n)
	}
	if _, ok := m.Alarm.Standing()["sources"]; !ok {
		t.Fatal("no alarm on the disagreement")
	}
	m.applySources(ctx, 102, SourcesAgree, "a", "a", "a")
	if m.state.OddNode {
		t.Fatal("OddNode stands after agreement")
	}
}

// The runway: the trailing rate, never below the planned one, and 7 days.
func TestRunway(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	var samples []sample
	// 2 SOL, spending 0.01 SOL an hour over 6 hours (top-ups ignored).
	bal := int64(2 * lamportsPerSOL)
	for h := 0; h <= 6; h++ {
		samples = addSample(samples, sample{At: t0.Add(time.Duration(h) * time.Hour), Balance: bal})
		bal -= 10_000_000
		if h == 3 {
			bal += 500_000_000 // a top-up
		}
	}
	// Five decreases of 0.01 SOL over six hours (the top-up's hour counts for nothing): 0.2 SOL a day.
	obs := observedPerDay(samples)
	if obs < 0.2*lamportsPerSOL-1 || obs > 0.2*lamportsPerSOL+1 {
		t.Fatalf("observed %.0f lamports/day, want 0.2 SOL", obs)
	}
	days, rate := runway("relayer-sol", samples, nil)
	if rate != obs {
		t.Fatalf("rate %.0f, want the observed %.0f (above the planned %.0f)", rate, obs, plannedPerDay["relayer-sol"])
	}
	if days < 12 || days > 13 { // 2.43 SOL left at 0.2 a day
		t.Fatalf("days %.1f", days)
	}
	// Too short an observation (half an hour): the planned rate.
	brief := []sample{{At: t0, Balance: 2 * lamportsPerSOL}, {At: t0.Add(30 * time.Minute), Balance: 2*lamportsPerSOL - 10_000_000}}
	_, rate = runway("ata-payer-BUBBLE", brief, nil)
	if rate != plannedPerDay["ata-payer"] {
		t.Fatalf("rate %.0f, want the planned %.0f", rate, plannedPerDay["ata-payer"])
	}
	// The window drops old samples.
	long := addSample(samples, sample{At: t0.Add(30 * time.Hour), Balance: bal})
	if long[0].At.Before(t0.Add(6 * time.Hour)) {
		t.Fatalf("old samples kept: %v", long[0].At)
	}
	// An override wins over the plan.
	_, rate = runway("relayer-gno", nil, map[string]float64{"relayer-gno": 1234})
	if rate != 1234 {
		t.Fatalf("override: %.0f", rate)
	}
	// 0.3 SOL for the updater at the planned rate — four transactions a header, a header every ten minutes,
	// 0.00288 SOL a day — is ≈3.4 months (104 days), §5.5's own figure.
	days, rate = runway("updater-sol", []sample{{At: t0, Balance: 300_000_000}}, nil)
	if rate != 2_880_000 || days < 104 || days > 105 {
		t.Fatalf("updater: %.1f days at %.0f lamports/day, want ≈104 at 2,880,000", days, rate)
	}
	text := runwayText("relayer-gno", 500_000_000, 90, 5_500_000)
	if !strings.Contains(text, "ugnot") {
		t.Fatal(text)
	}
}

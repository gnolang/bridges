package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// Round 6's fold lane F18c: a decision that cannot fit a block (audit round 6, realms.md, "for the lead": the executor
// raises Execute's gas up to the block and then answers its out-of-gas — a queued range void of 1,000 credits needs
// ≈3.58B, past gnoland-1's 3B, and can never land).

// f18cBlockGno is a chain whose calls use the gas set for them, simulated as gnokey does: the client's ceiling, four
// times it within the block's gas (gno.Client.Call), and one needing more than the ceiling reached runs out of gas —
// at the block's gas, its output gnokey's failure lines (GAS WANTED, GAS USED: the gas the simulation used when the
// meter stopped) and the chain's out-of-gas error.
type f18cBlockGno struct {
	*fakeGno
	need  map[string]int64 // "pkg.fn" → the gas it uses
	asked map[string][]int64
}

func (g *f18cBlockGno) Call(ctx context.Context, pkg, fn string, gas int64, args ...string) (*gno.Result, error) {
	g.asked[pkg+"."+fn] = append(g.asked[pkg+"."+fn], gas)
	reach := gas
	if 4*gas <= gno.MaxBlockGas {
		reach = 4 * gas
	}
	if need := g.need[pkg+"."+fn]; need > reach {
		out := fmt.Sprintf("GAS WANTED: %d\nGAS USED:   %d\nHEIGHT:     0\nEVENTS:     []\nINFO:       \nTX HASH:    \n--= Error =--\nData: out of gas error\nMsg Traces:\n    0  /tm2/pkg/crypto/keys/client/maketx.go:378 - deliver transaction failed: log:out of gas in location: CPUCycles\n--= /Error =--\n", reach, reach)
		return nil, &gno.CallError{Pkg: pkg, Fn: fn, Err: errors.New("exit status 1"), Output: out}
	}
	return g.fakeGno.Call(ctx, pkg, fn, gas, args...)
}

// A queued decision whose Execute runs out of gas at the block's gas was simulated up to the block every minute for
// good — the out-of-gas transient, never parked, said by the log alone — so no member learnt to cancel it and split
// it. It is now alarmed decision:too-big:<realm>#<id> with the gas its simulation used, tried again hourly (the alarm
// standing between two tries), and its bookkeeping and alarm go once a member cancels it.
func TestF18cAnExecutePastTheBlockAlarmsAndWaitsAnHour(t *testing.T) {
	w := newWorld(t, 100, "primary")
	now := w.g.timeAt(w.g.head).Unix()
	decisions := func(state string) string {
		return fmt.Sprintf(`[{"id":7,"kind":"void","text":"since:4000","state":%q,"made":%d,"recorded":%d,"due":%d}]`, state, now-100, now-100, now-10)
	}
	w.g.renders[testGNOT+":decisions.json"] = decisions("pending")
	g := &f18cBlockGno{fakeGno: w.g, need: map[string]int64{testGNOT + ".Execute": 3_584_791_596}, asked: map[string][]int64{}} // realms.md: a queued range void of 1,000 credits
	w.r.gno = g
	key, alarm := "gnoexec:"+testGNOT+"#7", "decision:too-big:"+testGNOT+"#7"
	w.tick()
	text, raised := w.r.Alarm.Standing()[alarm]
	first := len(g.asked[testGNOT+".Execute"])
	t.Logf("the first try: ceilings %v; alarm %v: %s; %s", g.asked[testGNOT+".Execute"], raised, text, w.r.last[key])
	if !raised || !strings.Contains(text, "3000000000") || !strings.Contains(text, "used 3000000000") || !strings.Contains(text, "cancels it") {
		t.Fatalf("a decision too big for any block: alarm %v %q (standing %v)", raised, text, w.r.Alarm.StandingKeys())
	}
	if first == 0 || len(g.callsOf(testGNOT+".Execute")) != 0 {
		t.Fatalf("the simulations %v; broadcast %v", g.asked, g.callsOf(testGNOT+".Execute"))
	}
	// a minute on and fifty-nine more: no simulation to the block again, the alarm standing
	for i := 0; i < 59; i++ {
		w.advance(time.Minute)
		w.tick()
	}
	if n := len(g.asked[testGNOT+".Execute"]); n != first {
		t.Fatalf("simulated to the block again within the hour: %d ceilings asked, %d at the first try", n, first)
	}
	if _, ok := w.r.Alarm.Standing()[alarm]; !ok {
		t.Fatal("the alarm did not stand between two tries")
	}
	if a := w.r.state.Attempts[key]; a == nil || a.parked() {
		t.Fatalf("the attempt: %+v (never parked: the parked alarm is not its news)", a)
	}
	// the hour passed: tried again
	w.advance(2 * time.Minute)
	w.tick()
	if n := len(g.asked[testGNOT+".Execute"]); n != 2*first {
		t.Fatalf("not tried again after the hour: %d ceilings asked (%d a try)", n, first)
	}
	// a member cancels it: its bookkeeping and the alarm go
	w.g.renders[testGNOT+":decisions.json"] = decisions("cancelled")
	w.advance(time.Minute)
	w.tick()
	if _, ok := w.r.Alarm.Standing()[alarm]; ok {
		t.Fatalf("the alarm of a cancelled decision stands: %v", w.r.Alarm.StandingKeys())
	}
	if _, ok := w.r.state.Attempts[key]; ok {
		t.Fatalf("the attempt of a cancelled decision stays: %+v", w.r.state.Attempts[key])
	}
}

// A restart keeps the verdict: the state file's attempt raises the alarm again at the first tick, with no simulation
// to the block before the hour is out.
func TestF18cATooBigExecuteStaysAlarmedAcrossARestart(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.State = filepath.Join(t.TempDir(), "relayer.json")
	now := w.g.timeAt(w.g.head).Unix()
	w.g.renders[testGNOT+":decisions.json"] = fmt.Sprintf(`[{"id":7,"kind":"void","state":"pending","made":%d,"recorded":%d,"due":%d}]`, now-100, now-100, now-10)
	g := &f18cBlockGno{fakeGno: w.g, need: map[string]int64{testGNOT + ".Execute": 3_584_791_596}, asked: map[string][]int64{}}
	w.r.gno = g
	w.tick()
	first := len(g.asked[testGNOT+".Execute"])
	r2 := &Relayer{Cfg: w.r.Cfg, State: w.r.State, Role: "primary", Alarm: &Alarmer{Source: "test"}, Sender: w.e, Eth: w.e, gno: g, light: w.g, blocks: w.g, runner: w.g}
	if err := r2.init(); err != nil {
		t.Fatal(err)
	}
	r2.now = func() time.Time { return w.now }
	w.r = r2
	w.advance(5 * time.Minute)
	w.tick()
	text, ok := w.r.Alarm.Standing()["decision:too-big:"+testGNOT+"#7"]
	if !ok || !strings.Contains(text, "used 3000000000") {
		t.Fatalf("after a restart: alarm %v %q (%v)", ok, text, w.r.Alarm.StandingKeys())
	}
	if n := len(g.asked[testGNOT+".Execute"]); n != first {
		t.Fatalf("simulated to the block again after the restart: %d ceilings (%d a try)", n, first)
	}
}

// A decision whose Execute fits once the realm's state shrinks lands at its next hourly try, and the alarm clears.
func TestF18cATooBigExecuteThatShrinksLands(t *testing.T) {
	w := newWorld(t, 100, "primary")
	now := w.g.timeAt(w.g.head).Unix()
	w.g.renders[testGNOT+":decisions.json"] = fmt.Sprintf(`[{"id":7,"kind":"void","state":"pending","made":%d,"recorded":%d,"due":%d}]`, now-100, now-100, now-10)
	g := &f18cBlockGno{fakeGno: w.g, need: map[string]int64{testGNOT + ".Execute": 3_100_000_000}, asked: map[string][]int64{}}
	w.r.gno = g
	alarm := "decision:too-big:" + testGNOT + "#7"
	w.tick()
	if _, ok := w.r.Alarm.Standing()[alarm]; !ok {
		t.Fatalf("not alarmed: %v", w.r.Alarm.StandingKeys())
	}
	g.need[testGNOT+".Execute"] = 2_900_000_000
	w.advance(61 * time.Minute)
	w.tick()
	if calls := w.g.callsOf(testGNOT + ".Execute"); len(calls) != 1 {
		t.Fatalf("not applied once it fits: %v (%s)", calls, w.r.last["gnoexec:"+testGNOT+"#7"])
	}
	if _, ok := w.r.Alarm.Standing()[alarm]; ok {
		t.Fatalf("the alarm stands after it landed: %v", w.r.Alarm.StandingKeys())
	}
}

// GovDAO's ExecuteOrRejectProposal of a builder-made proposal with the votes runs its closure (a void at once of 1,000
// credits ≈4.44B): one past the block is alarmed proposal:too-big:<id> and tried again hourly, the alarm going once the
// proposal is no longer open.
func TestF18cAProposalPastTheBlockAlarms(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Gno.DAO = gnoside.DAOPath
	i3DAOList(w, "")
	g := &f18cBlockGno{fakeGno: w.g, need: map[string]int64{gnoside.DAOPath + ".ExecuteOrRejectProposal": 4_444_216_379}, asked: map[string][]int64{}}
	w.r.gno = g
	w.tick() // first seen open
	w.advance(14*24*time.Hour + 2*time.Minute)
	w.tick() // past its deadline
	text, raised := w.r.Alarm.Standing()["proposal:too-big:5"]
	first := len(g.asked[gnoside.DAOPath+".ExecuteOrRejectProposal"])
	t.Logf("ceilings %v; alarm %v: %s", g.asked[gnoside.DAOPath+".ExecuteOrRejectProposal"], raised, text)
	if !raised || first == 0 || !strings.Contains(text, "#5") {
		t.Fatalf("a proposal too big for any block: alarm %v %q (%v)", raised, text, w.r.Alarm.StandingKeys())
	}
	w.advance(time.Minute)
	w.tick()
	if n := len(g.asked[gnoside.DAOPath+".ExecuteOrRejectProposal"]); n != first {
		t.Fatalf("simulated to the block again a minute later: %d ceilings (%d a try)", n, first)
	}
	// the proposal closed (a member's own hand): the alarm goes
	w.g.renders[gnoside.DAOPath+":"] = "# GovDAO\n## Members\n[> Go to Memberstore <](/r/gov/dao/memberstore/v0)\n## Proposals\n### [Prop #5 - eth/hlgnot: refund](/r/gov/dao:5)\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\nStatus: ACCEPTED\n\nTiers eligible to vote: T1, T2, T3\n\n---\n\n"
	w.advance(time.Minute)
	w.tick()
	if _, ok := w.r.Alarm.Standing()["proposal:too-big:5"]; ok {
		t.Fatalf("the alarm of a closed proposal stands: %v (%s)", w.r.Alarm.StandingKeys(), w.r.last["proposals"])
	}
}

// f18cBroadcastGno is a chain where a call's simulation fits but its broadcast runs out of gas — the state moved
// between the two — with gnokey's failure lines for the transaction it sent (GAS WANTED the client sized, a TX HASH).
type f18cBroadcastGno struct {
	*fakeGno
	asked map[string][]int64
}

func (g *f18cBroadcastGno) Call(ctx context.Context, pkg, fn string, gas int64, args ...string) (*gno.Result, error) {
	g.asked[pkg+"."+fn] = append(g.asked[pkg+"."+fn], gas)
	if fn != "Execute" {
		return g.fakeGno.Call(ctx, pkg, fn, gas, args...)
	}
	out := "GAS WANTED: 1200000000\nGAS USED:   1200000000\nHEIGHT:     130\nEVENTS:     []\nINFO:       \nTX HASH:    aGFzaA==\n--= Error =--\nData: out of gas error\nMsg Traces:\n    0  deliver transaction failed: log:out of gas in location: CPUCycles\n--= /Error =--\n"
	return nil, &gno.CallError{Pkg: pkg, Fn: fn, Err: errors.New("exit status 1"), Output: out}
}

// The review of the lane (finding 5): an out-of-gas of the broadcast — sized below the block's gas from a simulation
// that fit — is no decision too big for any block: it is the transient retry it was, and alarms nothing.
func TestF18cABroadcastOutOfGasIsNotTooBig(t *testing.T) {
	w := newWorld(t, 100, "primary")
	now := w.g.timeAt(w.g.head).Unix()
	w.g.renders[testGNOT+":decisions.json"] = fmt.Sprintf(`[{"id":7,"kind":"void","state":"pending","made":%d,"recorded":%d,"due":%d}]`, now-100, now-100, now-10)
	g := &f18cBroadcastGno{fakeGno: w.g, asked: map[string][]int64{}}
	w.r.gno = g
	w.tick()
	first := len(g.asked[testGNOT+".Execute"])
	if _, ok := w.r.Alarm.Standing()["decision:too-big:"+testGNOT+"#7"]; ok {
		t.Fatalf("a broadcast's out-of-gas alarmed too big: %v", w.r.Alarm.StandingKeys())
	}
	if a := w.r.state.Attempts["gnoexec:"+testGNOT+"#7"]; a == nil || a.TooBig != 0 {
		t.Fatalf("the attempt: %+v", a)
	}
	w.advance(61 * time.Second)
	w.tick()
	if n := len(g.asked[testGNOT+".Execute"]); n <= first {
		t.Fatalf("not tried again a minute later (a transient failure): %d calls", n)
	}
}

// gnokey's output names the gas a failed simulation used on its GAS USED line, and the chain's out-of-gas log as "gas
// used (N)": the most either says, 0 when neither.
func TestF18cGasUsedOf(t *testing.T) {
	for _, c := range []struct {
		out  string
		want int64
	}{
		{"GAS WANTED: 3000000000\nGAS USED:   2999999871\n", 2999999871},
		{"Data: gas used (3000000400) exceeds max block gas (3000000000) during operation: simulation", 3000000400},
		{"--= Error =--\nData: out of gas in location: CPUCycles\n", 0},
	} {
		if got := gasUsedOf(&gno.CallError{Err: errors.New("exit status 1"), Output: c.out}); got != c.want {
			t.Errorf("%q: %d, want %d", c.out, got, c.want)
		}
	}
	if gasUsedOf(errors.New("GAS USED: 5")) != 0 {
		t.Error("an error that is no gno call's read")
	}
}

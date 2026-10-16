package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// E3-GS9: one decision pending for good — here a kind 7 the test keeps short of its seven days, as a kind 8 waiting
// for code or a decision under a lasting hold stays — no longer hides the decisions numbered 256 and more after it.
// The executor read the 256 numbers from the lowest pending one, so an unpause recorded as number 300 was never read
// nor executed; the cursor now passes every number read at the finalized block, keeps the pending one to read again
// every tick, and the unpause is executed — and the kept one still is, once it matures.
func TestExecutorSeesPastAStuckDecision(t *testing.T) {
	w := newWorld(t, 100, "primary")
	a := w.e.admins[ethAdmin]
	now := w.e.now
	a.lastSeq = 301
	a.decisions[1] = &ethx.Decision{Number: 1, Kind: 7, DeliveredTime: now - 3600}         // pends for days
	a.decisions[300] = &ethx.Decision{Number: 300, Kind: 2, DeliveredTime: now - 2*86_400} // an unpause, mature
	w.tick()
	ex := w.e.sentOf("execute")
	if len(ex) != 1 || a.decisions[300] != nil {
		t.Fatalf("the unpause 299 numbers after a decision pending for good: %d executes, decision 300 %v", len(ex), a.decisions[300])
	}
	// the first tick read numbers 1–256 at the finalized block (the cursor 257, number 1 kept), then 1 and 257–301 at
	// the latest
	if p := w.r.state.AdminPending[ethAdmin.Hex()]; fmt.Sprint(p) != "[1]" || w.r.state.AdminLow[ethAdmin.Hex()] != 257 {
		t.Fatalf("kept pending %v, the cursor %d (want [1] and 257)", p, w.r.state.AdminLow[ethAdmin.Hex()])
	}
	// the kept decision is read every tick: it executes once mature, and is dropped once the finalized block shows it gone
	w.e.now += 8 * 86_400
	w.tick()
	if a.decisions[1] != nil || len(w.e.sentOf("execute")) != 2 {
		t.Fatalf("the kept decision did not execute once mature: %v", a.decisions[1])
	}
	w.tick()
	if p := w.r.state.AdminPending[ethAdmin.Hex()]; len(p) != 0 {
		t.Fatalf("still kept: %v", p)
	}
	// a decision recorded later is read from the cursor on
	a.lastSeq = 302
	a.decisions[302] = &ethx.Decision{Number: 302, Kind: 2, DeliveredTime: w.e.now - 2*86_400}
	w.tick()
	if a.decisions[302] != nil {
		t.Fatal("a decision recorded after the cursor passed the last number was not executed")
	}
}

// E3-RL4's Go half, the executor: a realm's ledger is read by page from the lowest number that may still pend, so a
// realm whose whole decisions.json is out of query gas (past ≈360 decisions) still has its due decisions applied.
// Before, the executor read the whole page and applied nothing.
func TestExecutorReadsTheLedgerByPage(t *testing.T) {
	w := newWorld(t, 100, "primary")
	now := w.g.timeAt(w.g.head).Unix()
	// 45 decisions of r/hlgnot: 1–43 applied, 44 due, 45 queued; the whole page renders no more (no render here)
	delete(w.g.renders, testGNOT+":decisions.json")
	dec := func(id int64) string {
		state, due := "applied", now-1000
		switch id {
		case 44:
			state, due = "pending", now-10
		case 45:
			state, due = "pending", now+3600
		}
		return fmt.Sprintf(`{"id":%d,"kind":"set cap floor","text":"x","state":%q,"made":%d,"recorded":%d,"due":%d}`, id, state, now-2000, now-2000, due)
	}
	page := func(from int64) string {
		var parts []string
		for id := from; id <= 45 && id < from+20; id++ {
			parts = append(parts, dec(id))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	// and past the last, "[]", as the realms render it: a page of fewer than 20 is not the ledger's end (F10's format)
	for _, from := range []int64{1, 21, 41, 44, 46} {
		w.g.renders[fmt.Sprintf("%s:decisions.json/%d", testGNOT, from)] = page(from)
	}
	w.tick()
	if calls := w.g.callsOf(testGNOT + ".Execute"); len(calls) != 1 || calls[0] != testGNOT+".Execute(44)" {
		t.Fatalf("the due decision of a ledger read by page: %v", calls)
	}
	if low := w.r.state.GnoLow[testGNOT]; low != 44 {
		t.Fatalf("the ledger's cursor is %d (want 44: the first that may still pend)", low)
	}
	if !strings.Contains(w.r.last["gnoexec:"+testGNOT+"#44"], "applied") {
		t.Fatalf("decision 44: %s", w.r.last["gnoexec:"+testGNOT+"#44"])
	}
	// the next tick reads from the cursor alone (its page decisions.json/44): without the earlier pages — a read from
	// number 1 would find none and fall back to the whole page, which renders no more — it still finds 44 (this fake
	// renders it pending still) and calls Execute again
	for _, from := range []int64{1, 21, 41} {
		delete(w.g.renders, fmt.Sprintf("%s:decisions.json/%d", testGNOT, from))
	}
	w.tick()
	if calls := w.g.callsOf(testGNOT + ".Execute"); len(calls) != 2 || w.r.state.GnoLow[testGNOT] != 44 {
		t.Fatalf("the second tick from the cursor: %v, the cursor %d", calls, w.r.state.GnoLow[testGNOT])
	}
}

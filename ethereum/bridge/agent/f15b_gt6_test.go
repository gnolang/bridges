package agent

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// E4-GT6 (go-trust-poc/zz_audit4_diverted_test.go): a diverted credit's finding lived in the monitor's memory while its
// "judged" mark was in the state, so `hlwatch status -state` named it on its first run only, and a restarted monitor
// never alarmed or flagged it again. The finding is in the state, judged again every poll until its cure.
func TestF15bDivertedCreditStandsAcrossRuns(t *testing.T) {
	r := newMonRig(t)
	r.m.ReadOnly = true
	r.m.State = filepath.Join(t.TempDir(), "hlwatch.json")
	w := &Watch{m: r.m}
	id := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 200, 80) // alice burns 200 to herself
	idh := hex.EncodeToString(id[:])
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 200, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+idh, "59")
	// gno.land credited bob instead: a diverted credit, still open
	claim := `{"id":"` + idh + `","to":"g1us8428u2a5satrlxzagqqa5m6vmuze025anjlj","amount":200,"state":"open","by":"verifier","height":59}`
	r.gno.set(m3bNS+"hlgnot", "claim/"+idh, claim)
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := v.Green(); ok {
		t.Fatal("run 1 green over a diverted credit")
	}
	for run := 2; run <= 3; run++ { // a new hlwatch process over the same -state each time
		r.m.Alarm = &Alarmer{Source: "hlwatch-run"}
		if err := r.m.init(); err != nil {
			t.Fatal(err)
		}
		r.step(time.Minute)
		v, err := w.View(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ok, why := v.Green()
		t.Logf("run %d: green=%v %v", run, ok, why)
		if ok || !strings.Contains(strings.Join(why, ";"), "diverted:"+idh+":flag") {
			t.Fatalf("run %d over the same -state is green, or names no diverted credit: %v", run, why)
		}
	}
	// the cure: GovDAO voids the claim — the finding ends
	r.gno.set(m3bNS+"hlgnot", "claim/"+idh, strings.Replace(claim, `"open"`, `"forged"`, 1))
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 0, 0, 0))
	r.step(time.Minute)
	v, err = w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, why := v.Green()
	t.Logf("after the void: %v", why)
	if strings.Contains(strings.Join(why, ";"), "diverted:") {
		t.Fatalf("the diverted credit's finding outlived its cure: %v", why)
	}
}

// The monitor's side: restarted after its flags were cleared (GovDAO's unpause), it flags the still-open diverted claim
// again and alarms it, as the identity rule does a break it finds unflagged.
func TestF15bDivertedCreditReflaggedAfterRestart(t *testing.T) {
	r := newMonRig(t)
	r.m.State = filepath.Join(t.TempDir(), "monitor.json")
	id := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 200, 80)
	idh := hex.EncodeToString(id[:])
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 200, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+idh, "59")
	r.gno.set(m3bNS+"hlgnot", "claim/"+idh, `{"id":"`+idh+`","to":"g1us8428u2a5satrlxzagqqa5m6vmuze025anjlj","amount":200,"state":"open","by":"verifier","height":59}`)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	n1, e1 := len(r.gnoFlags()), r.snd.count()
	r.clearFlag(m3bGate, 4) // the unpause cleared the gate's flag; the fake's mailbox shows no gno flag standing
	r.m.Alarm = &Alarmer{Source: "monitor-restarted"}
	if err := r.m.init(); err != nil {
		t.Fatal(err)
	}
	r.chain.mine(130, 130)
	for i := 0; i < 2; i++ {
		r.step(10 * time.Second)
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("after the restart: gno flags %d (was %d), eth flag txs %d (was %d); alarms %v", len(r.gnoFlags()), n1, r.snd.count(), e1, r.m.Alarm.StandingKeys())
	if len(r.gnoFlags()) <= n1 || r.snd.count() <= e1 || !r.alarmed("diverted:"+idh+":flag") {
		t.Fatal("the restarted monitor neither flagged nor alarmed the open diverted claim again")
	}
}

// The foreign-decision rule the same way: a foreign decision still pending on HlAdmin is alarmed and flagged again
// after a restart and an unpause; once GovDAO cancels it (no longer pending), its finding ends.
func TestF15bForeignDecisionStandsAfterRestart(t *testing.T) {
	r := newMonRig(t)
	r.m.State = filepath.Join(t.TempDir(), "monitor.json")
	r.chain.mine(110, 110)
	r.decide(1, body(7, 1, append(make([]byte, 39), 1)...), 101, body(7, 1, make([]byte, 40)...), true)
	_ = r.poll()
	r.step(2 * time.Minute)
	_ = r.poll()
	n1 := r.snd.count()
	if n1 != 2 {
		t.Fatalf("the foreign decision was not flagged: %v", r.snd.calls)
	}
	r.clearFlag(m3bGate, 4)
	r.clearFlag(m3bAdmin, 4)
	r.m.Alarm = &Alarmer{Source: "monitor-restarted"}
	if err := r.m.init(); err != nil {
		t.Fatal(err)
	}
	r.chain.mine(130, 130)
	for i := 0; i < 3; i++ {
		r.step(2 * time.Minute)
		_ = r.poll()
	}
	t.Logf("after the restart and the unpause: eth flag txs %d (was %d) %v; alarm %q", r.snd.count(), n1, r.snd.calls, r.m.Alarm.Standing()["decision:foreign"])
	if r.snd.count() != n1+2 || !r.alarmed("decision:foreign") {
		t.Fatal("the pending foreign decision was not flagged and alarmed again after a restart and an unpause")
	}
	// GovDAO cancels it: no longer pending on HlAdmin, the finding ends
	r.mu.Lock()
	delete(r.pending, 1)
	r.mu.Unlock()
	r.step(2 * time.Minute)
	_ = r.poll()
	if r.alarmed("decision:foreign") {
		t.Fatalf("a cancelled foreign decision still alarmed: %v", r.m.Alarm.Standing()["decision:foreign"])
	}
}

// A void is not the cure of a diverted credit (the review's finding 2): a withheld claim is restorable at once, a forged
// one after 7 days, and a restore brings the diverted claim back. The voided finding is kept quietly — no alarm, no flag
// — and flagged again the moment the claim stands open again naming the wrong recipient; it ends once GovDAO credits
// the id afresh by vote.
func TestF15bVoidedDivertedCreditFlaggedAgainOnRestore(t *testing.T) {
	r := newMonRig(t)
	id := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 200, 80)
	idh := hex.EncodeToString(id[:])
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 200, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+idh, "59")
	bob := `{"id":"` + idh + `","to":"g1us8428u2a5satrlxzagqqa5m6vmuze025anjlj","amount":200,"state":"%s","by":"%s","height":59}`
	set := func(state, by string) { r.gno.set(m3bNS+"hlgnot", "claim/"+idh, fmt.Sprintf(bob, state, by)) }
	set("open", "verifier")
	poll := func() {
		t.Helper()
		r.step(10 * time.Second)
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
	}
	poll()
	if !r.alarmed("diverted:" + idh + ":flag") {
		t.Fatal("the diverted credit is not flagged")
	}
	set("withheld", "verifier") // voided as withheld (and unpaused)
	poll()
	if r.alarmed("diverted:" + idh + ":flag") {
		t.Fatal("a voided diverted claim still alarmed")
	}
	set("open", "verifier") // restored, naming bob again
	poll()
	if !r.alarmed("diverted:" + idh + ":flag") {
		t.Fatal("a restored diverted claim was not flagged again")
	}
	set("open", "vote") // GovDAO credits the id afresh by vote: the cure
	poll()
	if r.alarmed("diverted:"+idh+":flag") || len(r.m.state.Diverted) != 0 {
		t.Fatalf("the cure did not end the finding: %v", r.m.state.Diverted)
	}
}

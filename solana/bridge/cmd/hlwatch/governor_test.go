package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// M3l (M1k's Go half): the status says who governs — the compiled governor until the yield, GovDAO from the
// yield's height, GovDAO from the first block when none was compiled — prints "awaits the yield" beside a
// pre-armed proposal while the governor has not yielded (its closure refuses GovDAO until then: executing it
// panics and leaves it pending), names who applies a queued refund (the governor's apply by Govern before the
// yield, GovDAO's apply-refund proposal after), and the watch alarms the pre-armed proposal with the same words
// and the yield itself, once.
func TestWatchPrintsTheGovernor(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	mk := func(governor string, yielded int64) *view {
		return &view{At: now, Head: gno.Head{Height: 100, Time: now},
			Mailbox:   agent.MailboxView{Live: true, LaunchedAt: 1, Governor: governor, Yielded: yielded},
			Proposals: []agent.Proposal{{ID: 1, Title: "hlfees: set treasurer", Status: "ACTIVE", Yes: 100, Realm: "gno.land/r/test/hlfees", Text: "set treasurer to g1y (now g1x)", Armed: true}},
			Pending:   []agent.PendingDecision{{Realm: "gno.land/r/test/hlgnot2", ID: 4, Kind: "refund", Text: "refund 1 GNOT", Due: now.Unix() - 1}, {Realm: "gno.land/r/test/hlfees", ID: 2, Kind: "set treasurer", Text: "set treasurer to g1y", Due: now.Unix() + 600}}}
	}
	before := mk("g1gov", 0).text()
	for _, want := range []string{
		"- live since 1970-01-01T00:00:01Z\n- governor: g1gov (until the yield) — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused\n",
		"- #1 hlfees: set treasurer (yes 100.00%, ACTIVE): set treasurer to g1y (now g1x) — PRE-ARMED: has the votes; awaits the yield (until the governor yields, the builder's closure refuses gno.land/r/gov/dao: executing it now panics and leaves it pending, within its 14 days)",
		"## pending decisions on gno.land (apply with Execute(id) after the delay; the relayers do — a refund by the governor's apply-refund decision by Govern alone, AT2)",
		"- r/hlgnot2 #4 refund: refund 1 GNOT — MATURE, awaiting Execute; awaiting the governor's apply (an apply-refund decision by Govern; Execute refuses a refund and the relayer never calls it)",
		"- r/hlfees #2 set treasurer: set treasurer to g1y — matures in 10m0s",
	} {
		if !strings.Contains(before, want) {
			t.Fatalf("before the yield, the status lacks %q:\n%s", want, before)
		}
	}
	if strings.Contains(before, "GovDAO's apply") || strings.Contains(before, "anyone may execute it") {
		t.Fatalf("before the yield, the status names GovDAO as the applier or the executor:\n%s", before)
	}
	after := mk("g1gov", 77).text()
	for _, want := range []string{
		"- governor: gno.land/r/gov/dao since height 77 (g1gov yielded)\n",
		"— PRE-ARMED: has the votes, anyone may execute it",
		"a refund by GovDAO's apply-refund proposal alone, AT2)",
		"MATURE, awaiting Execute; awaiting GovDAO's apply (an apply-refund proposal; Execute refuses a refund and the relayer never calls it)",
	} {
		if !strings.Contains(after, want) {
			t.Fatalf("after the yield, the status lacks %q:\n%s", want, after)
		}
	}
	if strings.Contains(after, "awaits the yield") || strings.Contains(after, "the governor's apply") {
		t.Fatalf("after the yield, the status still waits on it:\n%s", after)
	}
	none := mk("", 0).text()
	if !strings.Contains(none, "- governor: gno.land/r/gov/dao from the first block (no governor was compiled)\n") || !strings.Contains(none, "anyone may execute it") || strings.Contains(none, "awaits the yield") {
		t.Fatalf("no governor compiled:\n%s", none)
	}
	// the watch: a pre-armed proposal is alarmed with "awaits the yield" before the yield and without it after;
	// the yield itself is one alarm; a mature refund's alarm names the applier
	w := &watcher{alarm: &agent.Alarmer{Source: "test"}}
	empty := mk("g1gov", 0)
	empty.Proposals, empty.Pending = nil, nil
	w.diff(empty, mk("g1gov", 0))
	st := w.alarm.Standing()
	if a := st["prearmed:1"]; !strings.HasSuffix(a, "has the votes and no execution: set treasurer to g1y (now g1x) — awaits the yield (its closure refuses gno.land/r/gov/dao until the governor yields)") {
		t.Fatalf("the pre-armed alarm before the yield: %q", a)
	}
	if a := st["mature:gno.land/r/test/hlgnot2#4"]; !strings.HasSuffix(a, "is mature: refund 1 GNOT — awaiting the governor's apply (an apply-refund decision by Govern; read the gate on the status page first, AT2)") {
		t.Fatalf("the mature refund's alarm before the yield: %q", a)
	}
	if _, ok := st["governor:yielded"]; ok {
		t.Fatal("the yield alarmed before it happened")
	}
	w.diff(mk("g1gov", 0), mk("g1gov", 77))
	if a := w.alarm.Standing()["governor:yielded"]; a != "the governor g1gov yielded at height 77: gno.land/r/gov/dao decides from here, and a pre-armed proposal may execute" {
		t.Fatalf("the yield's alarm: %q", a)
	}
	w = &watcher{alarm: &agent.Alarmer{Source: "test"}}
	empty.Mailbox.Yielded = 77
	w.diff(empty, mk("g1gov", 77))
	st = w.alarm.Standing()
	if a := st["prearmed:1"]; !strings.HasSuffix(a, "has the votes and no execution: set treasurer to g1y (now g1x)") {
		t.Fatalf("the pre-armed alarm after the yield: %q", a)
	}
	if a := st["mature:gno.land/r/test/hlgnot2#4"]; !strings.HasSuffix(a, "awaiting GovDAO's apply (an apply-refund proposal; read the gate on the status page first, AT2)") {
		t.Fatalf("the mature refund's alarm after the yield: %q", a)
	}
	if _, ok := st["governor:yielded"]; ok {
		t.Fatal("a view already yielded alarmed the yield")
	}
}

// M3l: the queued refund's finding (F27, AS2) names who applies it — the governor before the yield, GovDAO
// after — with the words that fit each (the governor makes the apply by Govern; GovDAO proposes and votes it);
// the gate it is read against is the same.
func TestWatchQueuedRefundNamesTheApplier(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Routes = []agent.Route{{Symbol: "GNOT", Gno: "gno.land/r/test/hlgnot2", Predecessor: "gno.land/r/test/hlgnot"}}
	cfg.Gno.Gov = "gno.land/r/test/hlgov"
	succ, pred := "gno.land/r/test/hlgnot2", "gno.land/r/test/hlgnot"
	enrolledAt := time.Unix(1_800_000_000, 0)
	body := sol.DecisionBody(sol.KindEnrollRouter, 3, sol.EncodeParams(&sol.Params{Kind: sol.KindEnrollRouter, Token: 0, Domain: hl.DomainGnoTest, Router: agent.RealmKey(succ)}))
	node := fakeNode{head: gno.Head{Height: 200, Time: enrolledAt}, renders: map[string]string{
		cfg.Gno.Gov + ":decision/3": fmt.Sprintf(`{"number":3,"kind":6,"body":"%s","id":"x","count":1,"at":%d}`, hex.EncodeToString(body), enrolledAt.Unix()),
		cfg.Gno.Gov + ":decision/2": "null",
		cfg.Gno.Gov + ":decision/1": "null",
	}}
	w := &watcher{cfg: cfg, gno: node, alarm: &agent.Alarmer{Source: "test"}}
	text := "refund 300000 GNOT of sends imported from the predecessor (after kind 6 enrolled this realm) to their senders: " + strings.Repeat("aa", 32)
	queued := agent.PendingDecision{Realm: succ, ID: 4, Kind: "refund", Text: text, Due: enrolledAt.Add(21 * 24 * time.Hour).Unix()}
	// the predecessor still the router on Solana (the kind 6 dispatched, not recorded): alarmed either way
	mk := func(governor string, yielded int64) *view {
		return &view{Head: gno.Head{Height: 200, Time: enrolledAt.Add(20 * 24 * time.Hour)},
			Mailbox: agent.MailboxView{Governor: governor, Yielded: yielded, Enrolled: map[string]agent.Enrollment{succ: {Height: 120, At: enrolledAt.Unix()}}},
			Gov:     agent.GovView{Address: "g1gov", Last: 3}, Pending: []agent.PendingDecision{queued},
			Sol: &agent.SolanaView{Processed: map[string]bool{}, Domain: hl.DomainGnoTest, Routers: map[string]agent.RouterView{"GNOT": {Enrolled: true, Router: agent.RealmKey(pred), Realm: pred}}, LastSeq: 2}}
	}
	v := mk("g1gov", 0)
	w.programChecks(context.Background(), v)
	if len(v.Findings) != 1 || v.Findings[0].key != "refund:queued:"+succ+"#4" || !strings.Contains(v.Findings[0].text, "r/hlgnot2 decision #4, a queued refund (GNOT), matures at 2027-02-05T08:00:00Z, awaiting the governor's apply: NOT ENROLLED ON SOLANA") || !strings.HasSuffix(v.Findings[0].text, " — do not make (by Govern) the apply while this stands (cancel the decision if the kind 6 was cancelled)") {
		t.Fatalf("the queued refund's finding before the yield: %+v", v.Findings)
	}
	v = mk("g1gov", 77)
	w.programChecks(context.Background(), v)
	if len(v.Findings) != 1 || !strings.Contains(v.Findings[0].text, "awaiting GovDAO's apply: NOT ENROLLED ON SOLANA") || !strings.HasSuffix(v.Findings[0].text, " — do not propose or vote the apply while this stands (cancel the decision if the kind 6 was cancelled)") {
		t.Fatalf("the queued refund's finding after the yield: %+v", v.Findings)
	}
}

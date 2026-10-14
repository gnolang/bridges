package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// The tests of audit round 6's fixes on hlwatch's side (M3j): G48 the line's proposals checked; AS4 the maxima
// compared; M1i's shapes (a kind 6 proposal against the mailbox, a restore of a write-off).

// M1i (R35, R36): a kind 6 proposal is read against the mailbox — NOT PULLED while the successor it names has
// pulled nothing, ANOTHER TOKEN'S ROUTE, A MIGRATED ROUTE, A DROPPED ROUTE, NOT A REGISTERED ROUTE — each a
// finding the closure would refuse; the token's own pulled live route is clean.
func TestWatchReadsAKind6Proposal(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Gno.Gov = "gno.land/r/test/hlgov"
	cfg.Routes = []agent.Route{{Symbol: "GNOT", Gno: "gno.land/r/test/hlgnot"}, {Symbol: "BUBBLE", Gno: "gno.land/r/test/hlbubble"}}
	succ, dropped := "gno.land/r/test/hlgnot2", "gno.land/r/test/hlgnotx"
	w := &watcher{cfg: cfg, alarm: &agent.Alarmer{Source: "test"}}
	text := func(token int, path string) string {
		k := agent.RealmKey(path)
		return fmt.Sprintf("enroll router for token %d, domain %d: %x", token, hl.DomainGnoTest, k[:])
	}
	mb := agent.MailboxView{RoutePaths: []string{"gno.land/r/test/hlbubble", "gno.land/r/test/hlgnot", succ},
		Predecessors: map[string]string{succ: "gno.land/r/test/hlgnot"}, Pulled: map[string]bool{}, DroppedRoutes: map[string]string{dropped: "gno.land/r/test/hlgnot"},
		SuccessorRoutes: map[string]string{succ: "GNOT"}, Successions: []string{"gno.land/r/test/hlgnot → " + succ}}
	v := &view{Mailbox: mb, Proposals: []agent.Proposal{
		{ID: 1, Realm: cfg.Gno.Gov, Text: text(0, succ)},
		{ID: 2, Realm: cfg.Gno.Gov, Text: text(1, succ)},
		{ID: 3, Realm: cfg.Gno.Gov, Text: text(0, "gno.land/r/test/hlgnot")},
		{ID: 4, Realm: cfg.Gno.Gov, Text: text(0, dropped)},
		{ID: 5, Realm: cfg.Gno.Gov, Text: text(0, "gno.land/r/test/stranger")},
		{ID: 6, Realm: cfg.Gno.Gov, Text: text(1, "gno.land/r/test/hlbubble")},
	}}
	w.programChecks(context.Background(), v)
	for i, want := range []string{
		"NOT PULLED: page 0 is not in (" + succ + " succeeds gno.land/r/test/hlgnot and has pulled nothing): the closure refuses it",
		"THE GNOT ROUTE'S, NOT BUBBLE'S: the closure refuses it",
		"A MIGRATED ROUTE (gno.land/r/test/hlgnot → " + succ + "): the closure refuses it",
		"A DROPPED ROUTE (re-pointed away by gno.land/r/test/hlgnot): the closure refuses it",
		"NOT A REGISTERED ROUTE: the closure refuses it",
		"the BUBBLE route, live and registered: the closure takes it",
	} {
		if p := v.Proposals[i].Text; !strings.Contains(p, want) {
			t.Fatalf("proposal #%d: %s, want %q", i+1, p, want)
		}
	}
	if len(v.Findings) != 5 || v.Findings[0].key != "enrol:proposal:1" || v.Findings[4].key != "enrol:proposal:5" {
		t.Fatalf("the findings: %+v", v.Findings)
	}
	// page 0 pulled: the successor is enrolled
	mb.Pulled[succ] = true
	v = &view{Mailbox: mb, Proposals: []agent.Proposal{{ID: 7, Realm: cfg.Gno.Gov, Text: text(0, succ)}}}
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "the GNOT route, a successor that pulled page 0: the closure takes it") || len(v.Findings) != 0 {
		t.Fatalf("a pulled successor: %s %+v", p, v.Findings)
	}
}

// G49, AS3, K40: hlwatch prints the checkpointed index's cut and the claims passed over as "before the checkpoint,
// not judged", and alarms — never flags — a claim credited after the cut that an unseeded index lacks, under its
// own key, cleared when it goes.
func TestWatchPrintsTheCheckpointedIndex(t *testing.T) {
	w := &watcher{alarm: &agent.Alarmer{Source: "test"}}
	id := strings.Repeat("ab", 32)
	line := "claim " + id + " on GNOT (credited at height 150 to g1bob for 300) is unindexed and the index starts at a checkpoint whose accounts could not be listed: its burn may predate the checkpoint's slot"
	v := &view{Sol: &agent.SolanaView{IndexFromHeight: 100, BeforeBase: 3, UnindexedBeforeBase: []string{line}}}
	s := v.text()
	if !strings.Contains(s, "the index starts at a checkpoint (the cut's height 100; NOT SEEDED (the mailbox's accounts could not be listed; retried hourly): a claim credited after it that the index lacks is alarmed, never forged); 3 waiting claims credited at or below it: before the checkpoint, not judged") || !strings.Contains(s, "- UNINDEXED BEFORE THE BASE: claim "+id) {
		t.Fatalf("the status: %s", s)
	}
	quiet := &view{Sol: &agent.SolanaView{IndexFromHeight: 100, IndexSeeded: true}}
	if s := quiet.text(); !strings.Contains(s, "seeded whole from the mailbox's dispatched-message accounts: every claim credited after it is judged") || strings.Contains(s, "UNINDEXED BEFORE THE BASE") {
		t.Fatalf("the seeded status: %s", s)
	}
	w.diff(quiet, v)
	if a, ok := w.alarm.Standing()["credit:unindexed-before-base:"+id]; !ok || !strings.HasPrefix(a, "UNINDEXED BEFORE THE BASE: claim "+id) {
		t.Fatalf("no alarm for the claim after the cut: %v", w.alarm.Standing())
	}
	for key := range w.alarm.Standing() {
		if strings.HasPrefix(key, "forged:") {
			t.Fatalf("a claim after the cut was flagged as forged: %s", key)
		}
	}
	w.diff(v, quiet)
	if _, ok := w.alarm.Standing()["credit:unindexed-before-base:"+id]; ok {
		t.Fatal("the alarm stands after the claim was indexed or gone")
	}
}

// M1i (R38): a restore proposal naming a written-off send's id is read as the write-off's undo; one naming a send
// that is not written off as a refusal; a claim's id says nothing of sends.
func TestWatchReadsARestoreOfAWriteOff(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Routes = []agent.Route{{Symbol: "GNOT", Gno: "gno.land/r/test/hlgnot2"}}
	ids := []string{strings.Repeat("aa", 32), strings.Repeat("bb", 32), strings.Repeat("cc", 32)}
	node := fakeNode{head: gno.Head{Height: 200}, renders: map[string]string{
		cfg.Routes[0].Gno + ":send/" + ids[0]: fmt.Sprintf(`{"id":%q,"from":"g1x","amount":25000,"at":1,"imported":true,"refunded":100,"writtenOff":200}`, ids[0]),
		cfg.Routes[0].Gno + ":send/" + ids[1]: fmt.Sprintf(`{"id":%q,"from":"g1y","amount":10,"at":1,"imported":true,"refunded":100,"writtenOff":0}`, ids[1]),
		cfg.Routes[0].Gno + ":send/" + ids[2]: "null",
	}}
	w := &watcher{cfg: cfg, gno: node, alarm: &agent.Alarmer{Source: "test"}}
	v := &view{Head: gno.Head{Height: 200}, Proposals: []agent.Proposal{{ID: 8, Realm: cfg.Routes[0].Gno, Text: "restore: " + strings.Join(ids, ",")}}}
	w.programChecks(context.Background(), v)
	p := v.Proposals[0].Text
	if !strings.Contains(p, ids[0]+": a written-off send (25000 from g1x): its restore undoes the write-off — lost falls by 25000, queued 7 days (R38)") || !strings.Contains(p, ids[1]+": a send that is not written off: the closure refuses it") || strings.Contains(p, ids[2]+":") {
		t.Fatalf("the restore's reading: %s", p)
	}
}

// AS4: hlwatch prints and alarms limits:mismatch:<token> while a route's live per-message maximum exceeds the
// pausable ISM's, and reads a route-maximum proposal against the ISM's live maximum — RAISE ABOVE THE SOLANA
// MAXIMUM when it would exceed it (a send between would strand), quiet when equal or below.
func TestWatchComparesTheMaxima(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Routes = []agent.Route{{Symbol: "BUBBLE", Gno: "gno.land/r/test/hlbubble", Token: "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"}}
	w := &watcher{cfg: cfg, alarm: &agent.Alarmer{Source: "test"}}
	limits := func(ism uint64, route int64, mismatch string) *view {
		v := &view{Routes: []agent.RouteView{{Symbol: "BUBBLE"}}, Sol: &agent.SolanaView{}}
		v.Sol.Limits[1] = agent.LimitView{Symbol: "BUBBLE", MaxPerMessage: ism, DayCap: 100_000_000_000, RouteMaximum: route, Mismatch: mismatch}
		return v
	}
	quiet := limits(25_000_000_000, 25_000_000_000, "")
	above := limits(25_000_000_000, 50_000_000_000, "BUBBLE's per-message maximum on gno.land, 50000000000, is above the pausable ISM's 25000000000")
	w.diff(quiet, above)
	if a, ok := w.alarm.Standing()["limits:mismatch:BUBBLE"]; !ok || !strings.Contains(a, "above the pausable ISM's") {
		t.Fatalf("no alarm on the mismatch: %v", w.alarm.Standing())
	}
	if s := above.text(); !strings.Contains(s, "  - LIMITS MISMATCH: BUBBLE's per-message maximum on gno.land, 50000000000, is above the pausable ISM's 25000000000") || !strings.Contains(s, "the route's maximum 50000000000") {
		t.Fatalf("the status does not print the mismatch: %s", s)
	}
	w.diff(above, quiet)
	if _, ok := w.alarm.Standing()["limits:mismatch:BUBBLE"]; ok {
		t.Fatal("the alarm stands with the maxima equal")
	}
	// a route-maximum proposal against the ISM's live maximum
	v := limits(25_000_000_000, 25_000_000_000, "")
	v.Proposals = []agent.Proposal{
		{ID: 11, Realm: cfg.Routes[0].Gno, Text: "set maximum to 50000 BUBBLE (now 25000 BUBBLE); raise the Solana limit (kind 12) first; a send above the Solana maximum strands"},
		{ID: 12, Realm: cfg.Routes[0].Gno, Text: "set maximum to 25000 BUBBLE (now 20000 BUBBLE); raise the Solana limit (kind 12) first; a send above the Solana maximum strands"},
		{ID: 13, Realm: cfg.Routes[0].Gno, Text: "set maximum to 20000.5 BUBBLE (now 25000 BUBBLE); raise the Solana limit (kind 12) first; a send above the Solana maximum strands"},
	}
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "RAISE ABOVE THE SOLANA MAXIMUM: 50000000000 base units against the pausable ISM's 25000000000") || !strings.Contains(p, "sends between would strand") {
		t.Fatalf("a raise above the ISM's maximum: %s", p)
	}
	if len(v.Findings) != 1 || v.Findings[0].key != "maximum:proposal:11" {
		t.Fatalf("the findings: %+v", v.Findings)
	}
	if p := v.Proposals[1].Text; !strings.Contains(p, "25000000000 base units, at the pausable ISM's 25000000000: no send strands") {
		t.Fatalf("a raise to the ISM's maximum: %s", p)
	}
	if p := v.Proposals[2].Text; !strings.Contains(p, "20000500000 base units, under the pausable ISM's 25000000000: no send strands") {
		t.Fatalf("a lowering: %s", p)
	}
}

// G48, K38: a proposal on a realm of a token's line the config does not name — the successor, in the standard
// layout after a migration — is checked as the configured realm's would be: a refund against the enrolment
// (alarmed), a second migrate against the mailbox.
func TestWatchChecksTheLinesProposals(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Routes = []agent.Route{{Symbol: "GNOT", Gno: "gno.land/r/test/hlgnot"}}
	at := time.Unix(1_800_000_000, 0)
	w := &watcher{cfg: cfg, gno: fakeNode{head: gno.Head{Height: 200, Time: at}}, alarm: &agent.Alarmer{Source: "test"}}
	refund := "refund 300000 GNOT of sends imported from the predecessor (after kind 6 enrolled this realm) to their senders: " + strings.Repeat("aa", 32)
	v := &view{Head: gno.Head{Height: 200, Time: at}, Lines: map[string][]string{"GNOT": {"gno.land/r/test/hlgnot", "gno.land/r/test/hlgnot2"}},
		Proposals: []agent.Proposal{{ID: 9, Realm: "gno.land/r/test/hlgnot2", Text: refund}, {ID: 10, Realm: "gno.land/r/test/hlgnot2", Text: "migrate to gno.land/r/test/hlgnot3"}}}
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "NO NOTE: no kind 6 enrolling gno.land/r/test/hlgnot2") {
		t.Fatalf("the successor's refund proposal was not checked: %s", p)
	}
	if len(v.Findings) != 1 || v.Findings[0].key != "refund:proposal:9" || !strings.Contains(v.Findings[0].text, "a refund on gno.land/r/test/hlgnot2 (GNOT)") {
		t.Fatalf("the findings: %+v", v.Findings)
	}
	if p := v.Proposals[1].Text; !strings.Contains(p, "SUCCESSOR gno.land/r/test/hlgnot3 NOT ANNOUNCED") {
		t.Fatalf("the successor's migrate proposal was not checked: %s", p)
	}
}

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/gno"
)

// The tests of audit round 4's fixes on hlwatch's side (G33 the view's order, K28 a successor mid-import, AQ2 a
// refunded send, AQ6 the line of succession, G29 what could not be read, C341 the quiet cap ratio).

// fakeNode is a gno node for the view: a head, and renders by "pkg:path".
type fakeNode struct {
	head    gno.Head
	renders map[string]string
}

func (f fakeNode) RenderAt(_ context.Context, pkg, path string, _ int64) (string, error) {
	s, ok := f.renders[pkg+":"+path]
	if !ok {
		return "", fmt.Errorf("fake: no render %s:%s", pkg, path)
	}
	return s, nil
}
func (f fakeNode) Status(context.Context) (gno.Head, error) { return f.head, nil }
func (f fakeNode) Block(_ context.Context, h int64) (gno.Head, error) {
	return gno.Head{Height: h, Hash: "h", Time: f.head.Time}, nil
}

// G33: the view takes the Solana snapshot first and the node's head after it, and refuses a catching-up or stale
// head as the monitor does — no BROKEN line from a node an hour behind; the watch alarms watch:no-verdict while
// it stands.
func TestWatchViewRefusesACatchingUpNode(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Gno.Mailbox = "gno.land/r/test/hlmailbox"
	cfg.Routes = []agent.Route{{Symbol: "BUBBLE", Gno: "gno.land/r/test/hlbubble"}}
	w := &watcher{cfg: cfg, gno: fakeNode{head: gno.Head{Height: 100, Time: time.Now(), CatchingUp: true}}, alarm: &agent.Alarmer{Source: "test"}}
	if _, err := w.view(context.Background()); err == nil || !strings.Contains(err.Error(), "no verdict") || !strings.Contains(err.Error(), "catching up") {
		t.Fatalf("a catching-up node was read: %v", err)
	}
	w.gno = fakeNode{head: gno.Head{Height: 100, Time: time.Now().Add(-time.Hour)}}
	if _, err := w.view(context.Background()); err == nil || !strings.Contains(err.Error(), "no verdict") {
		t.Fatalf("a stale head was read: %v", err)
	}
	// a fresh head after the snapshot reads on to the mailbox (the fake renders none: that is the next error)
	w.gno = fakeNode{head: gno.Head{Height: 100, Time: time.Now().Add(time.Second)}}
	if _, err := w.view(context.Background()); err == nil || strings.Contains(err.Error(), "no verdict") {
		t.Fatalf("a fresh head was refused: %v", err)
	}
}

// K28, AQ2, AQ6, G29, C341: the watch alarms a successor mid-import (route:importing), a refunded send still
// sitting in hlmailbox, and every section that could not be read, each cleared when it ends; the route line
// prints IMPORTING with its pages and the migration, the admin program line its line of succession, the limits
// line the quiet cap ratio; a switch is judged against the line read from the chain.
func TestWatchAlarmsMigrationsRefundsAndNotRead(t *testing.T) {
	w := &watcher{alarm: &agent.Alarmer{Source: "test"}}
	base := func() *view {
		return &view{Routes: []agent.RouteView{{Symbol: "BUBBLE"}}, Sol: &agent.SolanaView{Identities: map[string]agent.Identity{"BUBBLE": {Symbol: "BUBBLE", Lock: 100, Supply: 100}}}}
	}
	quiet := base()
	importing := base()
	importing.Routes[0].Importing, importing.Routes[0].Imported = true, 2
	importing.Sol.Migrations = map[string]agent.MigrationView{"BUBBLE": {Predecessor: "gno.land/r/x/hlbubble", Successor: "gno.land/r/x/hlbubble2", Importing: true, Imported: 2, Text: "migrating gno.land/r/x/hlbubble → gno.land/r/x/hlbubble2, IMPORTING (2 pages so far); the identity is judged over the pair's summed counters"}}
	importing.Sol.Refunded = []string{"gno #7 (BUBBLE, 25000) was refunded on gno.land — must never deliver: the lock no longer holds it"}
	importing.Sol.Err = []string{"the client's record: transaction 5xyz: BUILD GAP: a version above 1"}
	importing.Sol.AdminProgram, importing.Sol.Retired = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb", []string{strings.Repeat("aa", 32)}
	w.diff(quiet, importing)
	for _, key := range []string{"route:importing:BUBBLE", "refunded:7", "not-read:the client's record"} {
		if _, ok := w.alarm.Standing()[key]; !ok {
			t.Fatalf("no alarm %s: %v", key, w.alarm.Standing())
		}
	}
	s := importing.text()
	for _, want := range []string{"IMPORTING (2 pages)", "- MIGRATION: migrating gno.land/r/x/hlbubble → gno.land/r/x/hlbubble2", "- REFUNDED: gno #7 (BUBBLE, 25000) was refunded on gno.land — must never deliver", "the live admin program is TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb, a successor by kind 9", "- not read: the client's record: transaction 5xyz: BUILD GAP"} {
		if !strings.Contains(s, want) {
			t.Fatalf("the status lacks %q: %s", want, s)
		}
	}
	w.diff(importing, quiet)
	for _, key := range []string{"route:importing:BUBBLE", "refunded:7", "not-read:the client's record"} {
		if _, ok := w.alarm.Standing()[key]; ok {
			t.Fatalf("the alarm %s stands after its cure", key)
		}
	}
	// the whole Solana side not read: its own alarm
	gone := base()
	gone.Sol, gone.SolErr = nil, "the client from provider 1: timeout"
	w.diff(quiet, gone)
	if _, ok := w.alarm.Standing()["not-read:solana"]; !ok {
		t.Fatalf("no alarm for the unread Solana side: %v", w.alarm.Standing())
	}
	w.diff(gone, quiet)
	if _, ok := w.alarm.Standing()["not-read:solana"]; ok {
		t.Fatal("the unread alarm stands after a read")
	}
	// the cap-ratio note (C341) prints beside the limits, and no CAP RATIO line
	note := base()
	note.Sol.Limits[1] = agent.LimitView{Symbol: "BUBBLE", DayCap: 100_000, Lock: 20_000, Expected: 1_000_000, Note: "BUBBLE's day cap (100000) is not judged against its lock (20000) yet: the lock is below the expected first-week lock of 1000000 it was set against (C341)"}
	if s := note.text(); !strings.Contains(s, "  - cap ratio quiet: BUBBLE's day cap (100000) is not judged") || strings.Contains(s, "CAP RATIO:") {
		t.Fatalf("the quiet note: %s", s)
	}
	// AQ6: a switch to the live program of a followed line is clean; r/hlgov still dispatching to a retired
	// program says RETIRED naming the live one; a switch to a stranger is judged against the line
	gov := "gno.land/r/test/hlgov"
	admin, succ := strings.Repeat("aa", 32), strings.Repeat("bb", 32)
	v := &view{Proposals: []agent.Proposal{{ID: 7, Realm: gov, Text: "set decision recipient to " + succ + " (now " + admin + ")"}},
		Gov: agent.GovView{Address: "g1gov", Recipient: admin}, Sol: &agent.SolanaView{LiveHex: succ, Retired: []string{admin}}}
	notes := switchNotes(v, gov)
	if len(notes) != 1 || notes[0].key != "switch:retired" || !strings.Contains(notes[0].text, "the live program is "+succ) {
		t.Fatalf("a followed line: %+v", notes)
	}
	v.Gov.Recipient = succ
	if notes := switchNotes(v, gov); len(notes) != 0 {
		t.Fatalf("a switch to the live program: %+v", notes)
	}
	v.Proposals[0].Text = "set decision recipient to " + strings.Repeat("cc", 32)
	if notes := switchNotes(v, gov); len(notes) != 1 || !strings.Contains(notes[0].text, "the admin program's line ends at "+succ+", not this one") {
		t.Fatalf("a switch to a stranger: %+v", notes)
	}
}

// AQ5 (the realms' M1g shapes): a route's migrate proposal is read against the mailbox — the successor must have
// announced itself for this route (hlmailbox.SuccessorFor), one unannounced or announced for the other route is
// named, one registered already is a migrate that applied; the status prints the successions, the announced
// successors and r/hlgov's former recipients.
func TestWatchReadsAMigrateProposal(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Gno.Gov = "gno.land/r/test/hlgov"
	cfg.Routes = []agent.Route{{Symbol: "GNOT", Gno: "gno.land/r/test/hlgnot"}, {Symbol: "BUBBLE", Gno: "gno.land/r/test/hlbubble"}}
	w := &watcher{cfg: cfg}
	mb := agent.MailboxView{SuccessorRoutes: map[string]string{"gno.land/r/test/hlgnot2": "GNOT", "gno.land/r/test/hlbubble2": "BUBBLE"}, Successions: []string{"gno.land/r/test/hlbubble → gno.land/r/test/hlbubble2"}}
	text := func(to string) string { return "migrate to " + to + " (announced as the GNOT route's successor)" }
	v := &view{Mailbox: mb, Proposals: []agent.Proposal{
		{ID: 1, Realm: "gno.land/r/test/hlgnot", Text: text("gno.land/r/test/hlgnot2")},
		{ID: 2, Realm: "gno.land/r/test/hlgnot", Text: text("gno.land/r/test/hlbubble2")},
		{ID: 3, Realm: "gno.land/r/test/hlgnot", Text: text("gno.land/r/test/nowhere")},
		{ID: 4, Realm: "gno.land/r/test/hlbubble", Text: "migrate to gno.land/r/test/hlbubble2 (announced as the BUBBLE route's successor)"},
		{ID: 5, Realm: "gno.land/r/test/hlgnot", Text: "set cap floor 5"},
	}, Gov: agent.GovView{Address: "g1gov", Recipient: strings.Repeat("bb", 32), Former: []string{strings.Repeat("aa", 32)}}}
	w.programChecks(context.Background(), v)
	for i, want := range []string{
		"announced itself as the GNOT route's successor",
		"IS ANOTHER ROUTE'S SUCCESSOR ALREADY (gno.land/r/test/hlbubble → gno.land/r/test/hlbubble2)",
		"NOT ANNOUNCED",
		"registered already (gno.land/r/test/hlbubble → gno.land/r/test/hlbubble2): the migrate applied",
	} {
		if !strings.Contains(v.Proposals[i].Text, want) {
			t.Fatalf("proposal %d: %q lacks %q", i+1, v.Proposals[i].Text, want)
		}
	}
	if strings.Contains(v.Proposals[4].Text, "—") {
		t.Fatalf("another kind's proposal was checked: %s", v.Proposals[4].Text)
	}
	s := v.text()
	for _, want := range []string{
		"- migrated routes (the successor takes the predecessor's messages once its last page is in): gno.land/r/test/hlbubble → gno.land/r/test/hlbubble2",
		"- successors announced, each for a route (a migrate names only one announced for its own): gno.land/r/test/hlbubble2 (BUBBLE), gno.land/r/test/hlgnot2 (GNOT)",
		"- former recipients (retired; never a successor, never the recipient again): " + strings.Repeat("aa", 32),
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("the status lacks %q: %s", want, s)
		}
	}
}

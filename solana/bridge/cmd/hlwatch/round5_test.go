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

// The tests of audit round 5's fixes on hlwatch's side (AR1 a refund proposal and a delivered refund, S-20 a
// successor with decisions pending, S-21 a successor of another layout, K37 the cap-ratio key), with round 6's
// F27/AS2 on the refund's gate.

// AR1, F27, AS2: a successor's refund — a proposal, or a queued decision — is read first against the token
// program's enrolled router on Solana (NOT ENROLLED ON SOLANA while it is not the refunding realm, naming the
// router found and the kind 6's state; alarmed whatever the note says), then against hlmailbox's kind-6 note —
// missing, the realm refuses (alarmed); younger than the realm's 14-day wait (R37), the realm refuses until
// then (alarmed); old enough, the realm takes it — and DELIVERED is printed beside any id Solana has processed
// (alarmed: a double mint); a refunded send Solana processed after all is printed REFUNDED AND DELIVERED and
// alarmed until written off.
func TestWatchReadsARefundProposal(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Routes = []agent.Route{{Symbol: "GNOT", Gno: "gno.land/r/test/hlgnot2", Predecessor: "gno.land/r/test/hlgnot"}}
	cfg.Gno.Gov = "gno.land/r/test/hlgov"
	succ, pred := "gno.land/r/test/hlgnot2", "gno.land/r/test/hlgnot"
	enrolledAt := time.Unix(1_800_000_000, 0)
	// r/hlgov's decisions: #3 the kind 6 enrolling the successor as GNOT's router (#2 a fee, #1 none)
	body := sol.DecisionBody(sol.KindEnrollRouter, 3, sol.EncodeParams(&sol.Params{Kind: sol.KindEnrollRouter, Token: 0, Domain: hl.DomainGnoTest, Router: agent.RealmKey(succ)}))
	node := fakeNode{head: gno.Head{Height: 200, Time: enrolledAt}, renders: map[string]string{ // the block at any height reads the head's time
		cfg.Gno.Gov + ":decision/3": fmt.Sprintf(`{"number":3,"kind":6,"body":"%s","id":"x","count":1,"at":%d}`, hex.EncodeToString(body), enrolledAt.Unix()),
		cfg.Gno.Gov + ":decision/2": fmt.Sprintf(`{"number":2,"kind":7,"body":"%s","id":"y","count":1,"at":1}`, hex.EncodeToString(sol.DecisionBody(sol.KindSetFee, 2, make([]byte, 40)))),
		cfg.Gno.Gov + ":decision/1": "null",
	}}
	w := &watcher{cfg: cfg, gno: node, alarm: &agent.Alarmer{Source: "test"}}
	text := "refund 300000 GNOT of sends imported from the predecessor (after kind 6 enrolled this realm) to their senders: " + strings.Repeat("aa", 32) + "," + strings.Repeat("bb", 32)
	router := func(path string) map[string]agent.RouterView {
		return map[string]agent.RouterView{"GNOT": {Enrolled: true, Router: agent.RealmKey(path), Realm: path}}
	}
	// mk is a view at a chain time with the mailbox given; sol nil leaves the Solana side unread, else the router
	// named is the token's (the kind 6 executed: the admin program's last is 3) and processed the ids Solana took
	mk := func(now time.Time, mb agent.MailboxView, routers map[string]agent.RouterView, processed map[string]bool) *view {
		v := &view{Head: gno.Head{Height: 200, Time: now}, Mailbox: mb, Gov: agent.GovView{Address: "g1gov", Last: 3}, Proposals: []agent.Proposal{{ID: 9, Realm: succ, Text: text}}}
		if routers != nil {
			v.Sol = &agent.SolanaView{Processed: processed, Domain: hl.DomainGnoTest, Routers: routers, LastSeq: 3}
		}
		return v
	}
	// the Solana side not read, no enrolment note: NOT JUDGED ON SOLANA and NO NOTE, a finding; the ids not judged
	v := mk(enrolledAt.Add(20*24*time.Hour), agent.MailboxView{}, nil, nil)
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "NOT JUDGED ON SOLANA") || !strings.Contains(p, "NO NOTE: no kind 6 enrolling gno.land/r/test/hlgnot2 as the GNOT router was dispatched") || !strings.Contains(p, "delivery not judged") {
		t.Fatalf("the Solana side unread, no note: %s", p)
	}
	if len(v.Findings) != 1 || v.Findings[0].key != "refund:proposal:9" {
		t.Fatalf("the findings: %+v", v.Findings)
	}
	// F27: the note is old enough, but the predecessor is still the router on Solana — the kind 6 dispatched as
	// #3 and not recorded there yet (the admin program's last is 2): NOT ENROLLED ON SOLANA, a finding, whatever
	// the note says
	mb := agent.MailboxView{Enrolled: map[string]agent.Enrollment{succ: {Height: 120}}}
	v = mk(enrolledAt.Add(20*24*time.Hour), mb, router(pred), map[string]bool{})
	v.Sol.LastSeq = 2
	w.programChecks(context.Background(), v)
	predKey := agent.RealmKey(pred)
	if p := v.Proposals[0].Text; !strings.Contains(p, "NOT ENROLLED ON SOLANA: the GNOT token program's router for gno.land's domain 1198419796 is "+hex.EncodeToString(predKey[:])+" (gno.land/r/test/hlgnot), not this realm, and the kind 6 enrolling it is dispatched as r/hlgov's #3, not yet recorded on the admin program (its last number is 2)") || !strings.Contains(p, "past the realm's 14-day wait (R37): the realm takes the refund") {
		t.Fatalf("the predecessor still the router: %s", p)
	}
	if len(v.Findings) != 1 || v.Findings[0].key != "refund:proposal:9" || !strings.Contains(v.Findings[0].text, "NOT ENROLLED ON SOLANA") {
		t.Fatalf("the findings with the predecessor as the router: %+v", v.Findings)
	}
	// the kind 6 pending on the admin program: named with its maturity
	v = mk(enrolledAt.Add(20*24*time.Hour), mb, router(pred), map[string]bool{})
	v.Sol.Pending = []agent.PendingSolDecision{{Number: 3, Kind: 6, MaturesAt: enrolledAt.Add(8 * 24 * time.Hour).Unix()}}
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "the kind 6 enrolling it is pending on the admin program as #3, matures at 2027-01-23T08:00:00Z") {
		t.Fatalf("the kind 6 pending: %s", p)
	}
	// enrolled on Solana, the note two days old by the block's time (the note names the height alone): under the
	// realm's 14-day wait, a finding
	v = mk(enrolledAt.Add(2*24*time.Hour), mb, router(succ), map[string]bool{})
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "ENROLLED ON SOLANA: the GNOT token program's router for gno.land's domain 1198419796 is this realm (the kind 6 executed; r/hlgov's #3)") || !strings.Contains(p, "THE NOTE IS 48h0m0s OLD, UNDER THE REALM'S 14-DAY WAIT (height 120 at 2027-01-15T08:00:00Z; R37): the realm refuses the refund until 2027-01-29T08:00:00Z") || !strings.Contains(p, strings.Repeat("aa", 32)+" undelivered") {
		t.Fatalf("under the wait: %s", p)
	}
	if len(v.Findings) != 1 {
		t.Fatalf("the findings under the wait: %+v", v.Findings)
	}
	// the realms' clocks at factor 8: the wait is 42 hours, so two days is past it; the time rendered in the note
	// is read without the node
	mb8 := agent.MailboxView{Clocks: agent.Clocks{Factor: 8, Delay: 7 * 24 * 3600 / 8, Deadline: 14 * 24 * 3600 / 8}, Enrolled: map[string]agent.Enrollment{succ: {Height: 120, At: enrolledAt.Unix()}}}
	v = mk(enrolledAt.Add(2*24*time.Hour), mb8, router(succ), map[string]bool{})
	w.gno = nil
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "the note is 48h0m0s old (height 120 at 2027-01-15T08:00:00Z), past the realm's 14-day wait (R37): the realm takes the refund") || len(v.Findings) != 0 {
		t.Fatalf("past the wait at factor 8: %s %+v", p, v.Findings)
	}
	w.gno = node
	// old enough and enrolled, but one id delivered already: DELIVERED beside it, a finding
	v = mk(enrolledAt.Add(20*24*time.Hour), mb, router(succ), map[string]bool{strings.Repeat("bb", 32): true})
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "the realm takes the refund") || !strings.Contains(p, strings.Repeat("bb", 32)+" DELIVERED (Solana processed it already") || !strings.Contains(p, strings.Repeat("aa", 32)+" undelivered") {
		t.Fatalf("a delivered id: %s", p)
	}
	if len(v.Findings) != 1 || !strings.Contains(v.Findings[0].text, "GovDAO #9, a refund on gno.land/r/test/hlgnot2 (GNOT)") {
		t.Fatalf("the findings with a delivered id: %+v", v.Findings)
	}
	// F27: a queued refund decision on the successor is read against the same gate every poll until it applies —
	// alarmed under its own key while the predecessor is the router, its text carrying the reason; quiet once
	// the successor is enrolled
	queued := agent.PendingDecision{Realm: succ, ID: 4, Kind: "refund", Text: text, Due: enrolledAt.Add(21 * 24 * time.Hour).Unix()}
	v = mk(enrolledAt.Add(20*24*time.Hour), mb, router(pred), map[string]bool{})
	v.Proposals = nil
	v.Pending = []agent.PendingDecision{queued, {Realm: succ, ID: 5, Kind: "credit", Text: "credit x", Due: 1}}
	w.programChecks(context.Background(), v)
	if d := v.Pending[0].Text; !strings.Contains(d, "NOT ENROLLED ON SOLANA") || !strings.Contains(d, strings.Repeat("aa", 32)+" undelivered") {
		t.Fatalf("the queued refund's text: %s", d)
	}
	if v.Pending[1].Text != "credit x" {
		t.Fatalf("another kind's decision was checked as a refund: %s", v.Pending[1].Text)
	}
	if len(v.Findings) != 1 || v.Findings[0].key != "refund:queued:"+succ+"#4" || !strings.Contains(v.Findings[0].text, "r/hlgnot2 decision #4, a queued refund (GNOT), matures at 2027-02-05T08:00:00Z, awaiting GovDAO's apply: NOT ENROLLED ON SOLANA") || !strings.Contains(v.Findings[0].text, "do not propose or vote the apply while this stands") {
		t.Fatalf("the queued refund's finding: %+v", v.Findings)
	}
	v = mk(enrolledAt.Add(20*24*time.Hour), mb, router(succ), map[string]bool{})
	v.Proposals = nil
	v.Pending = []agent.PendingDecision{queued}
	w.programChecks(context.Background(), v)
	if len(v.Findings) != 0 || !strings.Contains(v.Pending[0].Text, "ENROLLED ON SOLANA") {
		t.Fatalf("the queued refund once enrolled: %s %+v", v.Pending[0].Text, v.Findings)
	}
	// the watch: a finding alarms while it stands and clears when it closes; a delivered refund is printed and
	// alarmed under refund:delivered:<n> until written off
	v = mk(enrolledAt.Add(20*24*time.Hour), mb, router(succ), map[string]bool{strings.Repeat("bb", 32): true})
	w.programChecks(context.Background(), v)
	quiet := &view{Sol: &agent.SolanaView{}}
	v.Sol.RefundDelivered = []string{"gno #7 (GNOT, 25000) was refunded on gno.land and Solana processed it after the refund: minted twice"}
	w.diff(quiet, v)
	for _, key := range []string{"refund:proposal:9", "refund:delivered:7"} {
		if _, ok := w.alarm.Standing()[key]; !ok {
			t.Fatalf("no alarm %s: %v", key, w.alarm.Standing())
		}
	}
	if s := v.text(); !strings.Contains(s, "- REFUNDED AND DELIVERED: gno #7 (GNOT, 25000) was refunded on gno.land and Solana processed it after the refund: minted twice") {
		t.Fatalf("the status lacks the delivered refund: %s", s)
	}
	w.diff(v, quiet)
	for _, key := range []string{"refund:proposal:9", "refund:delivered:7"} {
		if _, ok := w.alarm.Standing()[key]; ok {
			t.Fatalf("the alarm %s stands after its cure", key)
		}
	}
}

// M1h (AR2): the watch alarms a token's line re-pointed — its last realm changed between two views — once, and
// prints a route's written-off sends.
func TestWatchAlarmsARepointedLine(t *testing.T) {
	w := &watcher{alarm: &agent.Alarmer{Source: "test"}}
	mk := func(succ string) *view {
		return &view{Sol: &agent.SolanaView{Migrations: map[string]agent.MigrationView{"GNOT": {Predecessor: "gno.land/r/x/hlgnot", Successor: succ}}}}
	}
	w.diff(mk("gno.land/r/x/hlgnot2"), mk("gno.land/r/x/hlgnot3"))
	if a, ok := w.alarm.Standing()["route:successor-changed:GNOT"]; !ok || !strings.Contains(a, "was gno.land/r/x/hlgnot2, now gno.land/r/x/hlgnot3") {
		t.Fatalf("no alarm for the re-pointed line: %v", w.alarm.Standing())
	}
	w.diff(mk("gno.land/r/x/hlgnot3"), mk("gno.land/r/x/hlgnot3"))
	if _, ok := w.alarm.Standing()["route:successor-changed:GNOT"]; ok {
		t.Fatal("the change alarm stands past its view")
	}
	v := &view{Routes: []agent.RouteView{{Symbol: "GNOT", Refunded: 2, RefundedTotal: 300, WrittenOff: 1, WrittenOffTotal: 100}}}
	if s := v.text(); !strings.Contains(s, "; 2 sends refunded (300, out of the lock); 1 refunded sends delivered after all and written off (100, lost)") {
		t.Fatalf("the route line: %s", s)
	}
}

// G37: the watch flags header:mismatch when the client's trusted header differs from this node's block at its
// height, whether or not the record reaches it (a checkpointed record never does); a node without the block
// says nothing, and the flag is raised once per differing header.
func TestWatchFlagsATrustedHeaderThisNodeDoesNotHold(t *testing.T) {
	w := &watcher{alarm: &agent.Alarmer{Source: "test"}}
	view := func(trusted, chain string, match bool) *view {
		return &view{Sol: &agent.SolanaView{Client: agent.ClientView{TrustedHeight: 100, TrustedHash: trusted, ChainHash: chain, Match: match}}}
	}
	agreeing := view(strings.Repeat("ab", 32), strings.Repeat("ab", 32), true)
	forked := view(strings.Repeat("ab", 32), strings.Repeat("cd", 32), false)
	w.diff(agreeing, forked)
	if a, ok := w.alarm.Standing()["header:mismatch"]; !ok || !strings.Contains(a, "HEADER MISMATCH: the client's trusted header 100 hashes to abababababababab…, this node's block to cdcdcdcdcdcdcdcd…") {
		t.Fatalf("no flag for a trusted header this node does not hold: %v", w.alarm.Standing())
	}
	// the node has no block at the height: no verdict, no flag
	w.alarm = &agent.Alarmer{Source: "test"}
	w.diff(agreeing, view(strings.Repeat("ab", 32), "", false))
	if _, ok := w.alarm.Standing()["header:mismatch"]; ok {
		t.Fatal("a node without the block flagged a mismatch")
	}
	// a standing mismatch is not raised again on the same header
	w.alarm = &agent.Alarmer{Source: "test"}
	w.diff(forked, forked)
	if _, ok := w.alarm.Standing()["header:mismatch"]; ok {
		t.Fatal("a standing mismatch was raised again")
	}
}

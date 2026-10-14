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

// The tests of the final check's fixes on hlwatch's side (M3k): AT2, GovDAO's apply of a queued refund.

// AT2: a refund is applied by GovDAO's apply-refund proposal (M1j's NewApplyRefundRequest: the title "<realm>:
// apply refund", the description "apply refund decision N at once (<the refund's text>), after hlwatch's check
// …"), never by anyone's Execute (the realm refuses the kind), so the apply proposal — told by its title first,
// never read as the refund proposal its description quotes — is read against the refund's own gate: the token's
// enrolled router on Solana, the kind 6's state, the note, the delivered ids — and alarmed with "do not propose
// or vote the apply while this stands" while the predecessor is still the router, as the queued refund's own
// finding is; quiet once the successor is enrolled and the note is old enough; NO QUEUED REFUND when the realm
// holds no pending decision under the id, NOT A REFUND when the id is another kind's; the decision's maturity
// printed; and the status page lists the queued refund as awaiting GovDAO's apply.
func TestWatchReadsAnApplyRefundProposal(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Routes = []agent.Route{{Symbol: "GNOT", Gno: "gno.land/r/test/hlgnot2", Predecessor: "gno.land/r/test/hlgnot"}}
	cfg.Gno.Gov = "gno.land/r/test/hlgov"
	succ, pred := "gno.land/r/test/hlgnot2", "gno.land/r/test/hlgnot"
	enrolledAt := time.Unix(1_800_000_000, 0)
	// r/hlgov's #3 is the kind 6 enrolling the successor as GNOT's router
	body := sol.DecisionBody(sol.KindEnrollRouter, 3, sol.EncodeParams(&sol.Params{Kind: sol.KindEnrollRouter, Token: 0, Domain: hl.DomainGnoTest, Router: agent.RealmKey(succ)}))
	node := fakeNode{head: gno.Head{Height: 200, Time: enrolledAt}, renders: map[string]string{
		cfg.Gno.Gov + ":decision/3": fmt.Sprintf(`{"number":3,"kind":6,"body":"%s","id":"x","count":1,"at":%d}`, hex.EncodeToString(body), enrolledAt.Unix()),
		cfg.Gno.Gov + ":decision/2": "null",
		cfg.Gno.Gov + ":decision/1": "null",
	}}
	w := &watcher{cfg: cfg, gno: node, alarm: &agent.Alarmer{Source: "test"}}
	text := "refund 300000 GNOT of sends imported from the predecessor (after kind 6 enrolled this realm) to their senders: " + strings.Repeat("aa", 32) + "," + strings.Repeat("bb", 32)
	router := func(path string) map[string]agent.RouterView {
		return map[string]agent.RouterView{"GNOT": {Enrolled: true, Router: agent.RealmKey(path), Realm: path}}
	}
	mb := agent.MailboxView{Enrolled: map[string]agent.Enrollment{succ: {Height: 120, At: enrolledAt.Unix()}}}
	due := enrolledAt.Add(20 * 24 * time.Hour)
	queued := agent.PendingDecision{Realm: succ, ID: 4, Kind: "refund", Text: text, Due: due.Unix()}
	// mk is a view at a chain time: the token's router as named and the admin program's last number (the kind 6
	// executed once it is 3), the queued refund #4 and a credit #5 pending on the successor, the proposals given
	mk := func(now time.Time, routers map[string]agent.RouterView, lastSeq uint64, proposals ...agent.Proposal) *view {
		return &view{Head: gno.Head{Height: 200, Time: now}, Mailbox: mb, Gov: agent.GovView{Address: "g1gov", Last: 3}, Proposals: proposals,
			Pending:   []agent.PendingDecision{queued, {Realm: succ, ID: 5, Kind: "credit", Text: "credit x", Due: 1}},
			Sol:       &agent.SolanaView{Processed: map[string]bool{}, Domain: hl.DomainGnoTest, Routers: routers, LastSeq: lastSeq}}
	}
	// the builder's shapes (M1j): the title "<realm>: apply refund", the description quoting the refund's text
	apply := func(id int64, quoted string) agent.Proposal {
		return agent.Proposal{ID: 11, Realm: succ, Title: "hlgnot2: apply refund", Text: fmt.Sprintf("apply refund decision %d at once (%s), after hlwatch's check that the token's router on Solana is this realm", id, quoted)}
	}
	// the predecessor still the router (the kind 6 dispatched as #3, not recorded on the admin program): the apply
	// is alarmed under its own key — never as the refund proposal its description quotes — and the queued
	// refund's finding says the same
	now := enrolledAt.Add(21 * 24 * time.Hour)
	v := mk(now, router(pred), 2, apply(4, text))
	w.programChecks(context.Background(), v)
	predKey := agent.RealmKey(pred)
	if p := v.Proposals[0].Text; !strings.Contains(p, "applies r/hlgnot2's queued refund #4, mature: NOT ENROLLED ON SOLANA: the GNOT token program's router for gno.land's domain 1198419796 is "+hex.EncodeToString(predKey[:])+" (gno.land/r/test/hlgnot), not this realm, and the kind 6 enrolling it is dispatched as r/hlgov's #3, not yet recorded on the admin program (its last number is 2)") || !strings.Contains(p, "past the realm's 14-day wait (R37): the realm takes the refund") || !strings.Contains(p, strings.Repeat("aa", 32)+" undelivered") {
		t.Fatalf("the apply with the predecessor as the router: %s", p)
	}
	if len(v.Findings) != 2 || v.Findings[0].key != "refund:apply:11" || v.Findings[1].key != "refund:queued:"+succ+"#4" {
		t.Fatalf("the findings (an apply read as a refund proposal would add refund:proposal:11): %+v", v.Findings)
	}
	if p := v.Proposals[0].Text; strings.Count(p, "applies r/hlgnot2's queued refund #4") != 1 || strings.Contains(p, " — NOT ENROLLED ON SOLANA") {
		t.Fatalf("the apply's text was checked as the refund proposal it quotes too: %s", p)
	}
	if f := v.Findings[0].text; !strings.HasPrefix(f, "GovDAO #11, the apply of r/hlgnot2's refund decision #4 (GNOT): applies r/hlgnot2's queued refund #4, mature: NOT ENROLLED ON SOLANA") || !strings.HasSuffix(f, " — do not propose or vote the apply while this stands") {
		t.Fatalf("the apply's finding: %s", f)
	}
	if f := v.Findings[1].text; !strings.Contains(f, "a queued refund (GNOT), matures at 2027-02-04T08:00:00Z, awaiting GovDAO's apply: NOT ENROLLED ON SOLANA") || !strings.Contains(f, "do not propose or vote the apply while this stands") {
		t.Fatalf("the queued refund's finding: %s", f)
	}
	if d := v.Pending[1].Text; d != "credit x" {
		t.Fatalf("another kind's decision was checked as a refund: %s", d)
	}
	// the successor enrolled (the kind 6 executed: the admin program's last is 3), the note 21 days old: the apply
	// is clean and nothing is alarmed
	v = mk(now, router(succ), 3, apply(4, text))
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "applies r/hlgnot2's queued refund #4, mature: ENROLLED ON SOLANA: the GNOT token program's router for gno.land's domain 1198419796 is this realm (the kind 6 executed; r/hlgov's #3); the note is 504h0m0s old (height 120 at 2027-01-15T08:00:00Z), past the realm's 14-day wait (R37): the realm takes the refund") || len(v.Findings) != 0 {
		t.Fatalf("the apply with the successor enrolled: %s %+v", p, v.Findings)
	}
	// not mature yet (the note 15 days old, the decision's 7 days not over): said, not alarmed
	v = mk(enrolledAt.Add(15*24*time.Hour), router(succ), 3, apply(4, text))
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "applies r/hlgnot2's queued refund #4, not mature until 2027-02-04T08:00:00Z (in 120h0m0s; the closure executes it only then, and a passed vote retries): ENROLLED ON SOLANA") || len(v.Findings) != 0 {
		t.Fatalf("the apply before the maturity: %s %+v", p, v.Findings)
	}
	// a delivered id: alarmed through the same check (a double mint)
	v = mk(now, router(succ), 3, apply(4, text))
	v.Sol.Processed[strings.Repeat("bb", 32)] = true
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, strings.Repeat("bb", 32)+" DELIVERED (Solana processed it already") || len(v.Findings) != 2 || v.Findings[0].key != "refund:apply:11" {
		t.Fatalf("the apply of a delivered id: %s %+v", p, v.Findings)
	}
	// the title alone tells an apply (a description of another shape is still the builder's kind): read as the
	// apply of the decision its description names, never as a refund proposal
	v = mk(now, router(pred), 2, agent.Proposal{ID: 11, Realm: succ, Title: "hlgnot2: apply refund", Text: "apply refund decision 4 at once"})
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "applies r/hlgnot2's queued refund #4, mature: NOT ENROLLED ON SOLANA") || len(v.Findings) != 2 || v.Findings[0].key != "refund:apply:11" {
		t.Fatalf("the apply by its title: %s %+v", p, v.Findings)
	}
	// an apply naming no queued refund, and one naming the credit: each what the closure would refuse
	p12, p13 := apply(9, "refund 1 GNOT of sends imported from the predecessor to their senders: "+strings.Repeat("cc", 32)), apply(5, "credit x")
	p12.ID, p13.ID = 12, 13
	v = mk(now, router(succ), 3, p12, p13)
	w.programChecks(context.Background(), v)
	if p := v.Proposals[0].Text; !strings.Contains(p, "NO QUEUED REFUND #9 ON r/hlgnot2 (applied, cancelled or never made): the closure would refuse the apply") {
		t.Fatalf("the apply of no decision: %s", p)
	}
	if p := v.Proposals[1].Text; !strings.Contains(p, "DECISION #5 ON r/hlgnot2 IS A credit, NOT A REFUND: the closure would refuse the apply") {
		t.Fatalf("the apply of a credit: %s", p)
	}
	if len(v.Findings) != 2 || v.Findings[0].key != "refund:apply:12" || v.Findings[1].key != "refund:apply:13" {
		t.Fatalf("the findings of the two refusals: %+v", v.Findings)
	}
	// the status page: the queued refund awaits GovDAO's apply, the credit awaits Execute
	v.At = now
	page := v.text()
	if !strings.Contains(page, "pending decisions on gno.land (apply with Execute(id) after the delay; the relayers do — a refund by GovDAO's apply-refund proposal alone, AT2)") || !strings.Contains(page, "- r/hlgnot2 #4 refund: "+text) || !strings.Contains(page, "MATURE, awaiting Execute; awaiting GovDAO's apply (an apply-refund proposal; Execute refuses a refund and the relayer never calls it)") {
		t.Fatalf("the status page's pending list: %s", page)
	}
	if strings.Contains(page, "#5 credit: credit x — MATURE, awaiting Execute;") {
		t.Fatalf("the credit awaits GovDAO's apply: %s", page)
	}
}

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// The tests of M3l: the Go side of the governor (M1k, Jae 2026-10-14: one compiled key makes every realm decision
// through Govern until its one-way Yield on hlmailbox hands them to GovDAO).

// mailboxRender is the mailbox's json the test relayer reads, with the governor and the yield's height (M1k:
// `"governor":"…","yielded":H` right after "launchedAt"; "" and 0 is GovDAO from the first block).
func mailboxRender(governor string, yielded int64) string {
	return fmt.Sprintf(`{"domain":%d,"hook":"","address":"","count":0,"root":"","delivered":0,"live":true,"launchedAt":1,"governor":%q,"yielded":%d,"launchOrigin":%d,"decisions":0,"signers":{"keys":[],"threshold":0,"groupA":[],"groupB":[]},"verifiers":{},"registered":[],"seats":[],"routes":{}}`, hl.DomainGnoTest, governor, yielded, hl.DomainSolanaTestnet)
}

// M1k's json: every bridge realm renders "governor" and "yielded" right after "live" (hlmailbox after
// "launchedAt"), and the views read them — the mailbox's, which every realm reads before it accepts a decision,
// a route's and r/hlgov's; a realm from before M1k renders neither and reads as GovDAO from the first block. The
// rule is the realms' decisions.DAOGoverns, the line their decisions.GovernorLine.
func TestViewsReadTheGovernor(t *testing.T) {
	f := newFake()
	ctx := context.Background()
	gov := "gno.land/r/test/hlgov"
	f.renders[testMailbox+":json"] = mailboxRender("g1gov", 0)
	f.renders[testRoute+":json"] = `{"address":"g1route","live":true,"governor":"g1gov","yielded":0,"balance":0,"paused":"","symbol":"BUBBLE","domain":1399811150,"router":"","oldRouter":"","oldUntil":0,"lock":1000,"sentTotal":1000,"paidTotal":0,"owed":0,"voided":0,"lost":0,"sweep":0,"cap":100,"meter":0,"capFloor":100,"fee":10,"feeFloor":4,"minimum":20,"maximum":2500,"openClaims":0,"claims":1,"sends":1,"migratedTo":"","decisions":0}`
	f.renders[gov+":json"] = `{"address":"g1hlgov","live":true,"governor":"g1gov","yielded":0,"destination":1399811150,"recipient":"aa","former":[],"start":0,"last":2,"next":3,"decisions":2}`
	mb, err := ReadMailbox(ctx, f, testMailbox, 100)
	if err != nil || mb.Governor != "g1gov" || mb.Yielded != 0 || mb.DAOGoverns() {
		t.Fatalf("the mailbox before the yield: %+v %v", mb, err)
	}
	if l := mb.GovernorLine(); l != "governor: g1gov (until the yield) — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused" {
		t.Fatalf("the governor line before the yield: %s", l)
	}
	rv, err := ReadRoute(ctx, f, Route{Symbol: "BUBBLE", Gno: testRoute}, 100)
	if err != nil || rv.Governor != "g1gov" || rv.Yielded != 0 {
		t.Fatalf("the route's governor: %+v %v", rv, err)
	}
	gv, err := ReadGov(ctx, f, gov, 100)
	if err != nil || gv.Governor != "g1gov" || gv.Yielded != 0 || gv.Last != 2 {
		t.Fatalf("r/hlgov's governor: %+v %v", gv, err)
	}
	// the yield: GovDAO from height 77
	f.renders[testMailbox+":json"] = mailboxRender("g1gov", 77)
	f.renders[gov+":json"] = strings.Replace(f.renders[gov+":json"], `"yielded":0`, `"yielded":77`, 1)
	if mb, err = ReadMailbox(ctx, f, testMailbox, 100); err != nil || mb.Governor != "g1gov" || mb.Yielded != 77 || !mb.DAOGoverns() {
		t.Fatalf("the mailbox after the yield: %+v %v", mb, err)
	}
	if l := mb.GovernorLine(); l != "governor: gno.land/r/gov/dao since height 77 (g1gov yielded)" {
		t.Fatalf("the governor line after the yield: %s", l)
	}
	if gv, err = ReadGov(ctx, f, gov, 100); err != nil || gv.Yielded != 77 {
		t.Fatalf("r/hlgov after the yield: %+v %v", gv, err)
	}
	// no governor compiled, and a realm from before M1k (neither field): GovDAO from the first block
	f.renders[testMailbox+":json"] = mailboxRender("", 0)
	if mb, err = ReadMailbox(ctx, f, testMailbox, 100); err != nil || mb.Governor != "" || !mb.DAOGoverns() {
		t.Fatalf("no governor compiled: %+v %v", mb, err)
	}
	if l := mb.GovernorLine(); l != "governor: gno.land/r/gov/dao from the first block (no governor was compiled)" {
		t.Fatalf("the governor line with none compiled: %s", l)
	}
	f.renders[testMailbox+":json"] = strings.Replace(mailboxRender("x", 1), `"governor":"x","yielded":1,`, "", 1)
	if mb, err = ReadMailbox(ctx, f, testMailbox, 100); err != nil || mb.Governor != "" || mb.Yielded != 0 || !mb.DAOGoverns() {
		t.Fatalf("a realm from before M1k: %+v %v", mb, err)
	}
	if !DAOGoverns("", 0) || DAOGoverns("g1gov", 0) || !DAOGoverns("g1gov", 1) {
		t.Fatal("DAOGoverns: none compiled governs; a governor until a non-zero height")
	}
}

// M1k on the relayers: until the governor yields nothing of the relayer touches r/gov/dao — the finisher
// (ExecuteOrRejectProposal of a builder-made proposal with the votes and past its deadline, F2) reads `yielded`
// from the mailbox json once a tick and, while it is 0 with a governor compiled, neither reads the DAO nor
// calls it, saying so in its status; from the yield the proposals are first seen and their days count from
// there; with no governor compiled it runs from the first block, as before. Red first with the finisher from
// 83fcb9dd: the proposal was finished before the yield.
func TestFinishProposalsAwaitTheYield(t *testing.T) {
	f := newFake()
	f.renders[testMailbox+":json"] = mailboxRender("g1gov", 0)
	r := testRelayer(f, "primary")
	ctx := context.Background()
	r.Cfg.Gno.DAO = "gno.land/r/gov/dao"
	r.Cfg.Gno.Fees = "gno.land/r/test/hlfees"
	daoRenders(f, r.Cfg.Gno.DAO, 1, "ACTIVE", 100, r.Cfg.Gno.Fees)
	// a read of the DAO's pages would fail here: the fake's RenderAt counts nothing, so the pages are removed to
	// prove nothing reads them before the yield (a missing render is an error the finisher would return)
	list, page := f.renders[r.Cfg.Gno.DAO+":"], f.renders[r.Cfg.Gno.DAO+":1"]
	delete(f.renders, r.Cfg.Gno.DAO+":")
	delete(f.renders, r.Cfg.Gno.DAO+":1")
	if err := r.finishProposals(ctx); err != nil {
		t.Fatalf("before the yield the finisher read the DAO: %v", err)
	}
	advance(r, ProposalDeadline+2*time.Hour)
	if err := r.finishProposals(ctx); err != nil {
		t.Fatalf("before the yield, past the deadline, the finisher read the DAO: %v", err)
	}
	if n := len(f.callsOf(r.Cfg.Gno.DAO)); n != 0 {
		t.Fatalf("finished before the yield: %d calls", n)
	}
	if len(r.state.ProposalsSeen) != 0 {
		t.Fatalf("a proposal was seen before the yield: %v", r.state.ProposalsSeen)
	}
	if n := r.last["proposals"]; !strings.Contains(n, "awaits the yield: until the governor g1gov yields on hlmailbox a builder-made proposal is refused by its closure, and nothing of this relayer reads or calls gno.land/r/gov/dao") {
		t.Fatalf("the status before the yield: %q", n)
	}
	// the yield at height 77: the proposal is first seen now, and finished once its days have passed from here
	f.renders[r.Cfg.Gno.DAO+":"], f.renders[r.Cfg.Gno.DAO+":1"] = list, page
	f.renders[testMailbox+":json"] = mailboxRender("g1gov", 77)
	if err := r.finishProposals(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(f.callsOf(r.Cfg.Gno.DAO)); n != 0 {
		t.Fatalf("finished on first sight after the yield: %d", n)
	}
	if _, ok := r.state.ProposalsSeen["1"]; !ok {
		t.Fatal("the proposal was not seen at the yield")
	}
	if n := r.last["proposals"]; !strings.Contains(n, "since the yield at height 77 (g1gov yielded): a builder-made proposal with the votes and past its deadline is finished with ExecuteOrRejectProposal") {
		t.Fatalf("the status after the yield: %q", n)
	}
	advance(r, ProposalDeadline-time.Hour)
	_ = r.finishProposals(ctx)
	if n := len(f.callsOf(r.Cfg.Gno.DAO)); n != 0 {
		t.Fatalf("finished within its days from the yield: %d", n)
	}
	advance(r, 2*time.Hour)
	_ = r.finishProposals(ctx)
	if calls := f.callsOf(r.Cfg.Gno.DAO); len(calls) != 1 || calls[0] != r.Cfg.Gno.DAO+".ExecuteOrRejectProposal(1)" {
		t.Fatalf("not finished past its days from the yield: %v", calls)
	}
	// no governor compiled: GovDAO from the first block, the finisher runs as before M1k
	f.renders[testMailbox+":json"] = mailboxRender("", 0)
	daoRenders(f, r.Cfg.Gno.DAO, 2, "ACTIVE", 100, r.Cfg.Gno.Fees)
	_ = r.finishProposals(ctx)
	if _, ok := r.state.ProposalsSeen["2"]; !ok {
		t.Fatal("with no governor compiled the proposal was not seen")
	}
	if n := r.last["proposals"]; !strings.Contains(n, "gno.land/r/gov/dao governs from the first block (no governor was compiled)") {
		t.Fatalf("the status with no governor: %q", n)
	}
	// the mailbox unreadable: the finisher does nothing this tick and says why (the yield unknown)
	delete(f.renders, testMailbox+":json")
	if err := r.finishProposals(ctx); err == nil || !strings.Contains(err.Error(), "no render") {
		t.Fatalf("the mailbox unreadable: %v", err)
	}
}

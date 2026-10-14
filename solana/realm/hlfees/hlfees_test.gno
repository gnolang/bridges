package hlfees

import (
	"chain"
	"chain/banker"
	"strings"
	"testing"

	"gno.land/p/nt/testutils/v0"
	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlmailbox"
	"gno.land/r/gov/dao"
	govinit "gno.land/r/gov/dao/init/v0"
)

const dayHeights = 86400 / 5

var (
	member    = testutils.TestAddress("hl-member")
	stranger  = testutils.TestAddress("hlf-other")
	newTreas  = testutils.TestAddress("hl-treasurer")
	premineTo = address("g1ecsuj0q572jr0dhu29q9njtnmw03hyu7tyyvv6") // bubble's premine: the tests' BUBBLE faucet
)

func aborts(t *testing.T, want string, f func()) {
	t.Helper()
	r := revive(f)
	if r == nil {
		t.Fatalf("expected an abort containing %q, got none", want)
	}
	msg := ""
	switch e := r.(type) {
	case error:
		msg = e.Error()
	case string:
		msg = e
	}
	if !strings.Contains(msg, want) {
		t.Fatalf("abort %q does not contain %q", msg, want)
	}
}

// pass makes a builder's decision by whoever governs now (M1k): the governor's own Govern before the yield, and
// GovDAO's proposal — created by the member, voted yes, executed — after it.
func pass(cur realm, req Request) {
	if hlmailbox.Yielded() == 0 {
		testing.SetRealm(testing.NewUserRealm(launchGovernor))
		Govern(cross(cur), req)
		return
	}
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		panic("the proposal did not execute")
	}
}

// refusedByDAO proves GovDAO's proxy cannot make a builder's decision before the yield (M1k): the proposal is
// created and voted, its execution aborts on the governor's rule, and nothing is recorded.
func refusedByDAO(cur realm, t *testing.T, what string, req Request) {
	t.Helper()
	n := ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlfees: until the governor yields, "+string(launchGovernor)+" alone decides, by Govern from its own key: not gno.land/r/gov/dao (a GovDAO proposal is refused until the yield)", func() { dao.ExecuteProposal(cross(cur), pid) })
	if ledger.Last() != n {
		t.Fatalf("%s: a refused proposal recorded a decision", what)
	}
}

func asMember(cur realm) { testing.SetRealm(testing.NewUserRealm(member)) }

func ugnotOf(a address) int64 { return banker.NewReadonlyBanker().GetCoin(a, "ugnot") }

func TestWithdraw(cur realm, t *testing.T) {
	if Treasurer() != launchTreasurer {
		t.Fatalf("treasurer %s", Treasurer())
	}
	asMember(cur)
	aborts(t, "not launched", func() { NewSetTreasurerRequest(cross(cur), newTreas) })
	// fees arrive as coins and as BUBBLE
	testing.IssueCoins(Address(), chain.Coins{{"ugnot", 7_000_000}})
	testing.SetRealm(testing.NewUserRealm(premineTo))
	bubble.Transfer(cross(cur), Address(), 300_000_000)
	if u, b := Balances(); u != 7_000_000 || b != 300_000_000 {
		t.Fatalf("balances %d %d", u, b)
	}
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "only the treasurer", func() { Withdraw(cross(cur)) })
	testing.SetRealm(testing.NewUserRealm(launchTreasurer))
	Withdraw(cross(cur))
	if u, b := Balances(); u != 0 || b != 0 || ugnotOf(launchTreasurer) != 7_000_000 || bubble.BalanceOf(launchTreasurer) != 300_000_000 {
		t.Fatalf("after the withdrawal: held %d %d, treasurer %d %d", u, b, ugnotOf(launchTreasurer), bubble.BalanceOf(launchTreasurer))
	}
	if !strings.Contains(Render(""), "treasurer: "+string(launchTreasurer)) || !strings.Contains(Render("json"), "\"ugnot\":0,\"bubble\":0") {
		t.Fatalf("page")
	}
}

// TestSetTreasurer: the swap waits 7 days under the governor (M1k) as under GovDAO; GovDAO's proposals are refused
// until the yield, the governor's Govern after it; the page says who governs.
func TestSetTreasurer(cur realm, t *testing.T) {
	asMember(cur)
	govinit.InitWithUsers(cross(cur), member)
	if !strings.Contains(Render(""), "- governor: "+string(launchGovernor)+" (until the yield) — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused\n") || !strings.Contains(Render("json"), "\"governor\":\""+string(launchGovernor)+"\",\"yielded\":0,") {
		t.Fatalf("page: %s\n%s", Render(""), Render("json"))
	}
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	hlmailbox.Govern(cross(cur), hlmailbox.NewLaunchRequest(cross(cur)))
	asMember(cur)
	aborts(t, "is the treasurer already", func() { NewSetTreasurerRequest(cross(cur), launchTreasurer) })
	asMember(cur)
	aborts(t, "not an address", func() { NewSetTreasurerRequest(cross(cur), "nobody") })
	asMember(cur)
	refusedByDAO(cur, t, "set treasurer", NewSetTreasurerRequest(cross(cur), newTreas))
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "hlfees: until the governor yields, "+string(launchGovernor)+" alone decides, by Govern from its own key: not "+string(stranger), func() { Govern(cross(cur), NewSetTreasurerRequest(cross(cur), newTreas)) })
	if ledger.Last() != 0 || Treasurer() != launchTreasurer {
		t.Fatalf("a refusal changed something")
	}
	asMember(cur)
	pass(cur, NewSetTreasurerRequest(cross(cur), newTreas))
	d := ledger.Get(1)
	if d == nil || !d.Queued() || d.Due != d.Recorded+decisions.Delay || Treasurer() != launchTreasurer {
		t.Fatalf("a swap applied at once: %+v", d)
	}
	asMember(cur)
	refusedByDAO(cur, t, "cancel", NewCancelRequest(cross(cur), 1))
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "is due at", func() { Execute(cross(cur), 1) })
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), 1)
	if Treasurer() != newTreas || d.State() != "applied" {
		t.Fatalf("treasurer %s", Treasurer())
	}
	// the yield, on the mailbox: Govern is closed here too, and GovDAO's proposals decide from here on
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	hlmailbox.Yield(cross(cur))
	h := hlmailbox.Yielded()
	if h == 0 || !strings.Contains(Render(""), "- governor: gno.land/r/gov/dao since height "+itoa(h)+" ("+string(launchGovernor)+" yielded)\n") || !strings.Contains(Render("json"), "\"governor\":\""+string(launchGovernor)+"\",\"yielded\":"+itoa(h)+",") {
		t.Fatalf("after the yield: %s", Render(""))
	}
	asMember(cur)
	req := NewSetTreasurerRequest(cross(cur), stranger)
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	aborts(t, "hlfees: the governor yielded at height "+itoa(h)+", gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never Govern", func() { Govern(cross(cur), req) })
	if ledger.Last() != 1 {
		t.Fatalf("Govern recorded after the yield")
	}
	testing.IssueCoins(Address(), chain.Coins{{"ugnot", 1_000}})
	testing.SetRealm(testing.NewUserRealm(launchTreasurer))
	aborts(t, "only the treasurer", func() { Withdraw(cross(cur)) })
	testing.SetRealm(testing.NewUserRealm(newTreas))
	Withdraw(cross(cur))
	if ugnotOf(newTreas) != 1_000 {
		t.Fatalf("the new treasurer got %d", ugnotOf(newTreas))
	}
	// cancel a queued swap at once
	asMember(cur)
	pass(cur, NewSetTreasurerRequest(cross(cur), stranger))
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), 2))
	if ledger.Get(2).State() != "cancelled" || ledger.Get(3).Kind != "cancel" {
		t.Fatalf("cancel")
	}
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "cancelled, not pending", func() { Execute(cross(cur), 2) })
	if !strings.Contains(Render("decisions"), "### set treasurer") || !strings.HasPrefix(Render("decisions.json"), "[{\"id\":1") {
		t.Fatalf("decisions page")
	}
}

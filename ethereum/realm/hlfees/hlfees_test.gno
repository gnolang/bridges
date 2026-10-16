package hlfees

import (
	"chain"
	"chain/banker"
	"crypto/bech32"
	"strconv"
	"strings"
	"testing"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/p/nt/testutils/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
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

// spellings are the four spellings of a gno address IsValid takes: as gno writes it (a), all in upper case, and with
// bech32m's checksum (BIP-350) in lower case and in upper (eth F9, its review's finding 1).
func spellings(a address) []address {
	b, err := hl.AddressToBytes32(a)
	if err != nil {
		panic(err)
	}
	data, err := bech32.ConvertBits(b[12:], 8, 5, true)
	if err != nil {
		panic(err)
	}
	m, err := bech32.EncodeM("g", data)
	if err != nil {
		panic(err)
	}
	return []address{a, address(strings.ToUpper(string(a))), address(m), address(strings.ToUpper(m))}
}

// TestTreasurerCanonical (eth F9, the sibling of the routes' credit and sweep votes; its review's finding 1): the
// treasurer is named in its canonical spelling (hlmailbox.CanonicalAddress) — IsValid takes three more of the same
// twenty bytes, upper case and bech32m's checksum in either case, which no caller's address ever equals, so a
// treasurer named so could never withdraw: the treasurer's own address in each of them is the treasurer already, and
// another typed with bech32m's checksum in upper case is named, applied and withdraws in the spelling gno writes.
func TestTreasurerCanonical(cur realm, t *testing.T) {
	if Treasurer() != newTreas {
		t.Fatalf("treasurer %s", Treasurer())
	}
	for _, a := range spellings(newTreas)[1:] {
		asMember(cur)
		aborts(t, "hlfees: "+string(newTreas)+" is the treasurer already", func() { NewSetTreasurerRequest(cross(cur), a) })
	}
	next := testutils.TestAddress("hl-treasurer-2")
	asMember(cur)
	req := NewSetTreasurerRequest(cross(cur), spellings(next)[3])
	if req.Description() != "set treasurer to "+string(next)+" (now "+string(newTreas)+")" {
		t.Fatalf("description: %s", req.Description())
	}
	pass(cur, req)
	id := ledger.Last()
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), id)
	if Treasurer() != next {
		t.Fatalf("treasurer %s", Treasurer())
	}
	testing.IssueCoins(Address(), chain.Coins{{"ugnot", 2_000}})
	testing.SetRealm(testing.NewUserRealm(next))
	Withdraw(cross(cur))
	if ugnotOf(next) != 2_000 {
		t.Fatalf("the treasurer named in another spelling got %d", ugnotOf(next))
	}
}

// TestTreasurerFault (the round-2 fold's F9-6, E3-RL2): init refuses, on every chain, a launch treasurer that is no gno
// address in its canonical spelling — empty, all in upper case, with bech32m's checksum in either case, no address at
// all — since Withdraw compares the caller's canonical address with the treasurer as written; the compiled one, and
// any canonical address, pass. guardLaunch, init's guard, asks it.
func TestTreasurerFault(cur realm, t *testing.T) {
	const tail = ", no gno address in its canonical form (bech32, lower case): no call would ever be the treasurer's, and the fees would wait for a decision naming another"
	for _, a := range []address{launchTreasurer, testutils.TestAddress("f10-treasurer"), "g1zdaz6sypk5tdsjc2fzsqrrn4ttn8qzmnl4waqp"} {
		if why := treasurerFault(a); why != "" {
			t.Fatalf("%s: %q", a, why)
		}
	}
	for _, a := range append(spellings(launchTreasurer)[1:], "", "g1notanaddress", "jae") {
		if why := treasurerFault(a); why != "hlfees: launchTreasurer is "+string(a)+tail {
			t.Fatalf("%q: %q", a, why)
		}
	}
}

// TestTitles (E3-RL6): every proposal this realm's builders make is titled "eth/hlfees: " and its kind.
func TestTitles(cur realm, t *testing.T) {
	p := NewSetTreasurerRequest(cross(cur), testutils.TestAddress("f10-treasurer")).Proposal()
	if p.Title() != "eth/hlfees: set treasurer" {
		t.Fatalf("title %q", p.Title())
	}
}

// TestInitGuarded (E3-RL7): init called its guard; guardedOn is what the call returned, the gno.land domain it checked
// the launch values at.
func TestInitGuarded(cur realm, t *testing.T) {
	if guardedOn == 0 || guardedOn != hlmailbox.LocalDomain() {
		t.Fatalf("init never ran its guard (guardLaunch): guardedOn is %d", guardedOn)
	}
}

// TestDecisionsJSONPages (E3-RL4): "decisions.json/<from>" is this realm's ledger in pages: the page from 1 holds the
// first objects "decisions.json" holds; past the last, and for no number, a page is "[]".
func TestDecisionsJSONPages(cur realm, t *testing.T) {
	whole, page := Render("decisions.json"), Render("decisions.json/1")
	if ledger.Last() == 0 || page == "[]" || !strings.HasPrefix(whole, strings.TrimSuffix(page, "]")) || Render("decisions.json/0") != page {
		t.Fatalf("the first page is not the ledger's first objects:\n%s\n%s", page, whole)
	}
	if Render("decisions.json/"+strconv.FormatInt(ledger.Last()+1, 10)) != "[]" || Render("decisions.json/x") != "[]" {
		t.Fatalf("a page past the last")
	}
}

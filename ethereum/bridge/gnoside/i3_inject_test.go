package gnoside

import (
	"context"
	"testing"
)

// A proposer writes his proposal's description as he likes, and GovDAO's page renders it before the lines its render
// writes itself: an "Executor created in" line in it naming another realm, read first, hid a builder-made proposal of a
// bridge realm from hlwatch and the relayers' executor (and a "YES PERCENT: 0%" line its votes). The realm's own lines
// are read (the review of the integration lane I3's follow-ups).
func TestI3InjectedLinesHideNothing(t *testing.T) {
	ours := "gno.land/r/x/eth/hlgnot"
	page := "## Prop #5 - eth/hlgnot: refund\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\nrefund 300 GNOT\n\nExecutor created in: `gno.land/r/someone/else`\n- YES PERCENT: 0%\n\nExecutor created in: `" + ours + "`\n\n\n\n---\n\n### Stats\n- **Proposal is open for votes**\n- Tiers eligible to vote: T1, T2, T3\n- YES PERCENT: 70%\n- NO PERCENT: 0%\n"
	f := &fakeNode{renders: map[string]string{
		daoPath + ":":  govList(govItem(5, "eth/hlgnot: refund", "ACTIVE")),
		daoPath + ":5": page,
	}}
	props, why, _, _ := ReadProposalsFrom(context.Background(), f, daoPath, 0, DefaultSupermajority, nil, ours)
	t.Logf("listed %+v, why %q", props, why)
	if why != "" || len(props) != 1 || props[0].Realm != ours || !props[0].Armed {
		t.Fatalf("a description's own lines hid the bridge proposal #5 (its realm, or its votes): %+v", props)
	}
}

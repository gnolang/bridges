package gnoside

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Fold F17b (the Go trust audit's E5-GT2): GovDAO's list read as "no proposals" when its items did not parse — a
// status word in another case, an item heading in another form, a title with an escaped bracket, a list past the page
// limit — while every bridge gate (a PRE-ARMED proposal, the refund gate, the route maximum, the switch) rests on it.
// The reading now says what of the list it did not read (hlwatch is never green over it; since the integration lane I3
// the relayers' executor reads it so too, ReadProposalsFrom, and says it: agent's TestI3ExecutorSaysAListNotReadWhole).

const daoPath = "gno.land/r/gov/dao"

// govItem is one item of GovDAO impl v0's list as gno v1.5.1 renders it (renderProposalListItem, then "---").
func govItem(id int, title, status string) string {
	return fmt.Sprintf("### [Prop #%d - %s](/r/gov/dao:%d)\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\nStatus: %s\n\nTiers eligible to vote: T1, T2, T3\n\n---\n\n", id, title, id, status)
}

func govList(items ...string) string {
	return "# GovDAO\n## Members\n[> Go to Memberstore <](/r/gov/dao/memberstore/v0)\n## Proposals\n" + strings.Join(items, "")
}

func govNode(list string) *fakeNode {
	return &fakeNode{renders: map[string]string{
		daoPath + ":":  list,
		daoPath + ":5": "## Prop #5 - eth/hlgnot: refund\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\nrefund 300 GNOT\n\nExecutor created in: `gno.land/r/x/eth/hlgnot`\n\n---\n\n### Stats\n- YES PERCENT: 70%\n",
	}}
}

func TestF17bGovDAOListNotReadWholeIsSaid(t *testing.T) {
	ctx := context.Background()
	ours := "gno.land/r/x/eth/hlgnot"
	cases := []struct {
		name, list string
		listed     bool   // proposal #5 read and listed
		why        string // "" when the list was read whole
	}{
		{"as rendered today", govList(govItem(5, "eth/hlgnot: refund", "ACTIVE"), govItem(6, "other", "ACCEPTED")), true, ""},
		{"a title with an escaped bracket", govList(govItem(5, `eth/hlgnot: refund \[route\]`, "ACTIVE")), true, ""},
		{"no proposals", govList("\nNo proposals yet.\n\n"), false, ""},
		{"a status word in another case", govList(govItem(5, "eth/hlgnot: refund", "Active")), false, `status "Active"`},
		{"an item heading in another form", govList(strings.Replace(govItem(5, "eth/hlgnot: refund", "ACTIVE"), "### [Prop #5 - ", "### [Proposal 5: ", 1)), false, "shows no item this reader parses"},
		{"one item of two does not parse", govList(strings.Replace(govItem(4, "x", "ACTIVE"), "Status: ", "State: ", 1), govItem(5, "eth/hlgnot: refund", "ACTIVE")), true, "1 of the 2 items of its page 1 do not parse"},
	}
	for _, c := range cases {
		props, why, _, _ := ReadProposalsFrom(ctx, govNode(c.list), daoPath, 0, DefaultSupermajority, nil, ours)
		listed := len(props) == 1 && props[0].ID == 5 && props[0].Armed
		t.Logf("%s: listed %v, why %q", c.name, listed, why)
		if listed != c.listed || (c.why == "") != (why == "") || !strings.Contains(why, c.why) {
			t.Errorf("%s: listed %v (want %v), why %q (want %q)", c.name, listed, c.listed, why, c.why)
		}
		// (the relayers' executor reads the list as hlwatch does since the integration lane I3, saying a list not read whole:
		// agent's TestI3ExecutorSaysAListNotReadWhole)
	}
	// fifty-one pages of five, each pointing at the next: read whole since the integration lane I3 (a reading reads a
	// thousand pages at most, and from a floor only the pages of the proposals made since: TestI3ProposalFloor)
	f := &fakeNode{renders: map[string]string{}}
	for page := 1; page <= 51; page++ {
		var items []string
		for i := 0; i < 5; i++ {
			items = append(items, govItem(1000-5*page-i, "old", "REJECTED"))
		}
		path := ""
		if page > 1 {
			path = fmt.Sprintf("?page=%d", page)
		}
		link := ""
		if page < 51 {
			link = fmt.Sprintf("[%d](?page=%d)\n", page+1, page+1)
		}
		f.renders[daoPath+":"+path] = govList(items...) + link
	}
	if _, why, _, floor := ReadProposalsFrom(ctx, f, daoPath, 0, DefaultSupermajority, nil, ours); why != "" || floor == nil || floor.Next != 996 {
		t.Errorf("a list of fifty-one pages: %q, floor %+v", why, floor)
	}
}

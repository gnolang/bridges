package gnoside

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// i3Gov is GovDAO's list as impl v0 renders it — newest first, five a page, each page linking the next — over proposals
// numbered 0..n-1 (status by number), and each proposal's own page with its status line and the realm its executor was
// created in; it counts the list's pages and the proposals' pages read.
type i3Gov struct {
	n      int
	status func(id int) string
	realm  func(id int) string
	page   func(id int) string // a proposal's own page in place of the rendered one ("" for the rendered one)
	pages  int
	props  int
}

func (g *i3Gov) RenderAt(_ context.Context, pkg, path string, _ int64) (string, error) {
	if pkg != daoPath {
		return "", fmt.Errorf("no render %s:%s", pkg, path)
	}
	if id, err := strconv.Atoi(path); err == nil {
		g.props++
		if g.page != nil {
			if p := g.page(id); p != "" {
				return p, nil
			}
		}
		line := map[string]string{"ACTIVE": "- **Proposal is open for votes**", "ACCEPTED": "- **PROPOSAL HAS BEEN ACCEPTED**", "REJECTED": "- **PROPOSAL HAS BEEN DENIED**"}[g.status(id)]
		return fmt.Sprintf("## Prop #%d - p\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\nx\n\nExecutor created in: `%s`\n\n\n\n---\n\n### Stats\n%s\n- Tiers eligible to vote: T1, T2, T3\n- YES PERCENT: 70%%\n- NO PERCENT: 0%%\n", id, g.realm(id), line), nil
	}
	page := 1
	if p, ok := strings.CutPrefix(path, "?page="); ok {
		page, _ = strconv.Atoi(p)
	}
	g.pages++
	if g.n == 0 {
		return govList("\nNo proposals yet.\n\n"), nil
	}
	var items []string
	for id := g.n - 1 - 5*(page-1); id >= 0 && id > g.n-1-5*page; id-- {
		items = append(items, govItem(id, "p", g.status(id)))
	}
	if 5*page < g.n {
		items = append(items, fmt.Sprintf("[%d](?page=%d)\n", page+1, page+1))
	}
	return govList(items...), nil
}
func (g *i3Gov) Status(context.Context) (gno.Head, error)       { return gno.Head{}, nil }
func (g *i3Gov) Block(context.Context, int64) (gno.Head, error) { return gno.Head{}, nil }

// The list read whole once, then from the floor it keeps: the proposals made since (the list down to Next) and each one
// open at the last whole reading by its own page — however old it is: a proposal nobody finishes (impl v0 has no expiry;
// gnoland-1's #6) holds no reading to the pages above it (the review of I3's follow-up 3). A renumbered list and an open
// item the floor does not hold are read whole; another DAO's floor is ignored; an open proposal's page whose status does
// not parse is a list not read whole; one another implementation made is kept, unlisted; a whole reading past a thousand
// pages is said.
func TestI3ProposalFloor(t *testing.T) {
	ctx := context.Background()
	ours := "gno.land/r/x/eth/hlgnot"
	open := map[int]bool{297: true}
	g := &i3Gov{n: 300, status: func(id int) string {
		switch {
		case open[id]:
			return "ACTIVE"
		case id%2 == 0:
			return "ACCEPTED"
		}
		return "REJECTED"
	}, realm: func(int) string { return ours }}
	read := func(floor *ProposalFloor) ([]Proposal, string, *ProposalFloor, int, int) {
		g.pages, g.props = 0, 0
		props, why, _, next := ReadProposalsFrom(ctx, g, daoPath, 0, DefaultSupermajority, floor, ours)
		return props, why, next, g.pages, g.props
	}
	listed := func(props []Proposal) []int64 {
		var ids []int64
		for _, p := range props {
			ids = append(ids, p.ID)
		}
		return ids
	}
	is := func(f *ProposalFloor, next int64, open ...int64) bool {
		return f != nil && f.DAO == daoPath && f.Next == next && reflect.DeepEqual(f.Open, open)
	}
	// the first reading: no floor, 300 proposals (60 pages) read whole
	props, why, f1, pages, _ := read(nil)
	t.Logf("no floor: listed %v, why %q, %d pages, floor %+v", listed(props), why, pages, f1)
	if why != "" || pages != 60 || len(props) != 1 || props[0].ID != 297 || !props[0].Armed || !is(f1, 300, 297) {
		t.Fatalf("the first reading of 300 proposals: listed %v, why %q, %d pages, floor %+v", listed(props), why, pages, f1)
	}
	// five newer proposals: the list down to #299, #297 read on the second page
	g.n = 305
	props, why, f2, pages, _ := read(f1)
	if why != "" || pages != 2 || len(props) != 1 || !is(f2, 305, 297) {
		t.Fatalf("from the floor: listed %v, why %q, %d pages, floor %+v", listed(props), why, pages, f2)
	}
	// #297 finished: its own page says so (the list's first page is below Next already), then one page a reading
	delete(open, 297)
	_, why, f3, pages, props3 := read(f2)
	_, _, f4, pages4, props4 := read(f3)
	if why != "" || pages != 1 || props3 != 1 || !is(f3, 305) || pages4 != 1 || props4 != 0 || !is(f4, 305) {
		t.Fatalf("nothing open: why %q, %d then %d pages (%d proposal pages), floors %+v %+v", why, pages, pages4, props4, f3, f4)
	}
	// a proposal nobody finishes under 300 newer ones (gnoland-1's #6): one list page and its own page a reading
	g.n, open = 310, map[int]bool{10: true}
	props, why, f5, pages, propPages := read(&ProposalFloor{DAO: daoPath, Next: 305, Open: []int64{10}})
	t.Logf("#10 open under 300 newer: listed %v, why %q, %d list pages + %d proposal pages, floor %+v", listed(props), why, pages, propPages, f5)
	if why != "" || pages != 2 || propPages != 1 || len(props) != 1 || props[0].ID != 10 || !is(f5, 310, 10) {
		t.Fatalf("an old open proposal held the reading: listed %v, why %q, %d pages, %d proposal pages, floor %+v", listed(props), why, pages, propPages, f5)
	}
	// it is finished at last: its own page says so, and it leaves the floor
	delete(open, 10)
	if _, why, f6, _, _ := read(f5); why != "" || !is(f6, 310) {
		t.Fatalf("#10 finished: why %q, floor %+v", why, f6)
	}
	// an open proposal's page whose status does not parse: the list not read whole, no new floor
	open[10] = true
	g.page = func(id int) string {
		if id == 10 {
			return "## Prop #10 - p\n\n### Stats\n- **Voting**\n- YES PERCENT: 70%\n"
		}
		return ""
	}
	if _, why, f7, _, _ := read(f5); !strings.Contains(why, "proposal #10's page shows no status this reader parses") || f7 != nil {
		t.Fatalf("an unparsed status: why %q, floor %+v", why, f7)
	}
	// one this implementation holds no status for (another made it): kept, read again, not listed
	g.page = func(id int) string {
		if id == 10 {
			return "# Proposal #10 not available\n\nThis proposal was not created by the current govDAO implementation, so it has no voting status here."
		}
		return ""
	}
	if props, why, f8, _, _ := read(f5); why != "" || len(props) != 0 || !is(f8, 310, 10) {
		t.Fatalf("not available here: listed %v, why %q, floor %+v", listed(props), why, f8)
	}
	g.page = nil
	// the list renumbered (its newest #19, under the floor's 310): read whole (its first page, then the four again)
	g.n, open = 20, map[int]bool{3: true}
	props, why, f9, pages, _ := read(&ProposalFloor{DAO: daoPath, Next: 310})
	if why != "" || pages != 1+4 || len(props) != 1 || props[0].ID != 3 || !is(f9, 20, 3) {
		t.Fatalf("a renumbered list: listed %v, why %q, %d pages, floor %+v", listed(props), why, pages, f9)
	}
	// an open item below the floor that the floor does not hold open (it was final): another list — read whole
	open[18] = true
	if props, why, f10, pages, _ := read(f9); why != "" || pages != 1+4 || len(props) != 2 || !is(f10, 20, 3, 18) {
		t.Fatalf("open below the floor, not held: listed %v, why %q, %d pages, floor %+v", listed(props), why, pages, f10)
	}
	// another DAO's floor: read whole
	if _, _, _, pages, _ := read(&ProposalFloor{DAO: "gno.land/r/other/dao", Next: 20}); pages != 4 {
		t.Fatalf("another DAO's floor was read from: %d pages", pages)
	}
	// no proposals where the floor's reading saw some (a list never loses one): another list, read whole
	g.n, open = 0, map[int]bool{}
	if props, why, f11, pages, _ := read(&ProposalFloor{DAO: daoPath, Next: 20, Open: []int64{3}}); why != "" || len(props) != 0 || pages != 2 || !is(f11, 0) {
		t.Fatalf("an emptied list: listed %v, why %q, %d pages, floor %+v", listed(props), why, pages, f11)
	}
	// a whole reading past a thousand pages is said
	g.n, open = 5*proposalPagesMax+5, map[int]bool{}
	if _, why, next, pages, _ := read(nil); !strings.Contains(why, "runs past 1000 pages") || next != nil || pages != proposalPagesMax {
		t.Fatalf("a list past a thousand pages read whole: why %q, %d pages, floor %+v", why, pages, next)
	}
}

// A proposal's own page is read from the lines GovDAO's render writes itself, after the description: a description
// that holds an "Executor created in" line naming another realm, a YES PERCENT of 0, or a stats block saying it was
// accepted hides neither the bridge proposal's realm, its votes nor its being open (the review of I3's follow-ups: the
// reader took each first match).
func TestI3ProposalPageReadsTheRealmsOwnLines(t *testing.T) {
	ours := "gno.land/r/x/eth/hlgnot"
	desc := "refund\n\nExecutor created in: `gno.land/r/someone/else`\n\n### Stats\n- **PROPOSAL HAS BEEN ACCEPTED**\n- YES PERCENT: 0%\n"
	pg := fmt.Sprintf("## Prop #5 - eth/hlgnot: refund\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\n%s\n\nExecutor created in: `%s`\n\n\n\n---\n\n### Stats\n- **Proposal is open for votes**\n- Tiers eligible to vote: T1, T2, T3\n- YES PERCENT: 70%%\n- NO PERCENT: 0%%\n\n[Detailed voting list](/r/gov/dao:5/votes)\n", desc, ours)
	r := pageOf(pg)
	if r.status != "ACTIVE" || r.realm != ours || r.yes != 70 || r.title != "eth/hlgnot: refund" || ExecutorRealm(pg) != ours {
		t.Fatalf("the page read %+v (ExecutorRealm %q)", r, ExecutorRealm(pg))
	}
	g := &i3Gov{n: 6, status: func(id int) string {
		if id == 5 {
			return "ACTIVE"
		}
		return "ACCEPTED"
	}, realm: func(int) string { return ours }, page: func(id int) string {
		if id == 5 {
			return pg
		}
		return ""
	}}
	props, why, _, floor := ReadProposalsFrom(context.Background(), g, daoPath, 0, DefaultSupermajority, nil, ours)
	if why != "" || len(props) != 1 || props[0].Realm != ours || !props[0].Armed || floor == nil {
		t.Fatalf("the bridge proposal behind a description's lines: %+v, why %q", props, why)
	}
	// and read by its own page from a floor (the list's first page, #11–#7, not reaching it)
	g.n, g.pages = 12, 0
	props, why, _, _ = ReadProposalsFrom(context.Background(), g, daoPath, 0, DefaultSupermajority, &ProposalFloor{DAO: daoPath, Next: 12, Open: []int64{5}}, ours)
	if why != "" || len(props) != 1 || !props[0].Armed || g.pages != 1 {
		t.Fatalf("by its own page: %+v, why %q", props, why)
	}
}

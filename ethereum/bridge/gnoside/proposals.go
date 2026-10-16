package gnoside

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// GovDAO's open proposals made with a bridge realm's builder (the Solana agent's views.go, carried): the relayer
// finishes one that has the votes and is past its deadline (ExecuteOrRejectProposal), and hlwatch lists them.

// DefaultSupermajority is the YES percent GovDAO's law needs (its impl v0's law.Supermajority).
const DefaultSupermajority = 66.66

// Proposal is one GovDAO proposal made with a bridge realm's builder, open and not executed.
type Proposal struct {
	ID     int64
	Title  string
	Status string
	Yes    float64
	Realm  string
	Text   string
	Armed  bool // has the votes GovDAO's law needs: anyone may execute it
}

var (
	// a title as GovDAO renders it escapes markdown's brackets (md.EscapeText: "\]"), so an escaped character is part of
	// it; an item's Status line is looked for before the next heading or "---" only (an item without one never takes the
	// next item's)
	propItemRE = regexp.MustCompile(`(?m)^### \[Prop #(\d+) - ((?:\\.|[^\]\\])*)\]\([^)]*\)\s*\n(?:(?:[^#\-\n].*)?\n)*?Status: (\w+)`)
	yesRE      = regexp.MustCompile(`(?m)^- YES PERCENT: ([0-9.]+)%`)
	executorRE = regexp.MustCompile("(?m)^Executor created in: `([^`]+)`")
	nextPageRE = regexp.MustCompile(`\?page=(\d+)`)
	noPropsRE  = regexp.MustCompile(`(?i)no proposals yet`)
	// a proposal's own page that this implementation holds no status for (impl v0: one another implementation made)
	elsewhereRE = regexp.MustCompile(`^# Proposal #\d+ not available`)
)

// propStatuses are the status words GovDAO's list writes (impl v0's getPropStatus): another is a render this reader
// does not know, never read as "not open".
var propStatuses = map[string]bool{"ACTIVE": true, "ACCEPTED": true, "REJECTED": true}

// proposalPagesMax bounds the list's pages a reading reads (five proposals a page on impl v0: the 5,000 newest): a
// reading from a floor reads only the pages of the proposals made since it.
const proposalPagesMax = 1000

// ProposalFloor is what a reading of GovDAO's list keeps for the next (the integration lane I3, the Go trust audit's
// E5-GT2: past 250 proposals the list was never read whole, and hlwatch never green). Proposals are numbered upward and
// a status only moves from ACTIVE to final (ACCEPTED, or REJECTED: impl v0 never unsets Accepted or Denied), so a
// reading that read the list whole knows every proposal numbered below Next for good but the ones it found open (Open):
// the next reading reads the list from its newest proposal down to Next — the proposals made since — and each open one
// by its own page, whose status line says whether it still is. A proposal nobody finishes costs one page a reading, never
// the pages of the list above it (impl v0 has no expiry: one that never reaches a supermajority either way stays ACTIVE
// for good, as gnoland-1's #6 has while #7–#13 were accepted — a floor at the lowest open proposal stayed there, and 250
// proposals on the reading ran past its pages for good: the review of I3's follow-up 3). A list whose newest proposal is
// older than Next−1, that says it has none where the floor's reading saw some, or that shows open a proposal below Next
// the floor does not hold open, is another list (r/gov/dao pointed at another implementation): read whole. DAO is the
// list's realm.
type ProposalFloor struct {
	DAO  string  `json:"dao"`
	Next int64   `json:"next"`
	Open []int64 `json:"open,omitempty"`
}

// ReadProposalsFrom reads r/gov/dao's list and every open proposal's page at a height — a proposal counts as the
// bridge's when its executor was created in one of the realms named, and as armed when it has the votes — from a floor
// when one is given (the list down to the floor's Next, its open proposals each by its own page) and whole otherwise (a
// state's first reading, another DAO's floor, a floor that does not fit the list). It names the open proposals whose own
// page did not read, with why (hlwatch is never green over a proposal it could not read: E4-GT5, E4-CF3), and answers a
// list it did not read whole as a list not read (the Go trust audit's E5-GT2): a page that shows no item it parses and
// does not say "No proposals yet", an item whose heading or status it does not parse, a status word GovDAO's render did
// not write before, an open proposal's page whose status it does not parse, a list past its pages — GovDAO's render is
// not fixed for the bridge's life (r/gov/dao is a proxy GovDAO can point at another implementation), and none of these
// is "no proposals". The proposals it did read are answered too, and the floor of a reading with nothing partial (nil
// otherwise: the next reading reads from the old floor again). The relayers' executor and hlwatch both read it so: the
// relayers finish what they read and say the rest (F17b's Q3).
func ReadProposalsFrom(ctx context.Context, g Reader, dao string, H int64, supermajority float64, floor *ProposalFloor, realms ...string) ([]Proposal, string, []string, *ProposalFloor) {
	if floor != nil && floor.DAO != dao {
		floor = nil
	}
	out, why, unread, partial, next := readProposals(ctx, g, dao, H, supermajority, floor, realms...)
	if why == "" && len(partial) > 0 {
		why = "r/gov/dao's list was not read whole: " + strings.Join(partial, "; ")
	}
	return out, why, unread, next
}

// readProposals is the list's reading, from a floor when one is given: the proposals, why nothing could be read, the
// open proposals whose page did not read, what of the list it did not read (partial), and the floor of a reading with
// nothing partial (nil otherwise). A floor that does not fit the list is dropped for a reading of it whole.
func readProposals(ctx context.Context, g Reader, dao string, H int64, supermajority float64, floor *ProposalFloor, realms ...string) ([]Proposal, string, []string, []string, *ProposalFloor) {
	ours := map[string]bool{}
	for _, p := range realms {
		if p != "" {
			ours[p] = true
		}
	}
	wasOpen := map[int64]bool{}
	if floor != nil {
		for _, id := range floor.Open {
			wasOpen[id] = true
		}
	}
	var out []Proposal
	var unread, partial []string
	seen := map[int64]bool{}
	open := map[int64]bool{} // the proposals this reading found open (or could not tell of: their page did not read)
	newest := int64(-1)
	// list reads an open proposal's own page: listed when it is the bridge's; its status line decides (page) or the
	// list's ACTIVE stands (an item of the list)
	list := func(id int64, title string, page bool) {
		pg, err := g.RenderAt(ctx, dao, strconv.FormatInt(id, 10), H)
		if err != nil {
			unread = append(unread, fmt.Sprintf("proposal #%d: %v", id, err))
			open[id] = true
			return
		}
		st := pageOf(pg)
		switch {
		case page && st.status == "elsewhere":
			open[id] = true // no status in this implementation: kept, read again (an implementation swapped back holds it)
			return
		case page && (st.status == "ACCEPTED" || st.status == "REJECTED"):
			return // final since the floor's reading
		case page && st.status != "ACTIVE":
			partial = append(partial, fmt.Sprintf("proposal #%d's page shows no status this reader parses (open for votes, ACCEPTED, DENIED)", id))
			open[id] = true
			return
		}
		open[id] = true
		if !ours[st.realm] {
			return
		}
		if title == "" {
			title = st.title
		}
		out = append(out, Proposal{ID: id, Title: title, Status: "ACTIVE", Realm: st.realm, Text: proposalDescription(pg), Yes: st.yes, Armed: st.yes >= supermajority})
	}
	more := false
	reached := false // an item below the floor's Next was read: every proposal made since was
	for page := 1; page <= proposalPagesMax; page++ {
		path := ""
		if page > 1 {
			path = "?page=" + strconv.Itoa(page)
		}
		lp, err := g.RenderAt(ctx, dao, path, H)
		if err != nil {
			return nil, "r/gov/dao: " + err.Error(), unread, partial, nil
		}
		if strings.Contains(lp, "DAO not initialized") {
			return nil, "r/gov/dao: not initialized", unread, partial, nil
		}
		items := propItemRE.FindAllStringSubmatch(lp, -1)
		if floor != nil && floor.Next > 0 && page == 1 && len(items) == 0 && noPropsRE.MatchString(lp) {
			// no proposals where the floor's reading saw some (a list never loses one): another list — read whole
			return readProposals(ctx, g, dao, H, supermajority, nil, realms...)
		}
		switch seps := listSeparators(lp); {
		case len(items) == 0 && !noPropsRE.MatchString(lp):
			partial = append(partial, fmt.Sprintf("its page %d shows no item this reader parses and does not say \"No proposals yet\"", page))
		case seps > len(items):
			partial = append(partial, fmt.Sprintf("%d of the %d items of its page %d do not parse", seps-len(items), seps, page))
		}
		for i, it := range items {
			id, _ := strconv.ParseInt(it[1], 10, 64)
			if floor != nil && page == 1 && i == 0 && id+1 < floor.Next {
				// the newest proposal is older than the newest the floor's reading saw: another list — read whole
				return readProposals(ctx, g, dao, H, supermajority, nil, realms...)
			}
			newest = max(newest, id)
			if seen[id] {
				continue
			}
			seen[id] = true
			below := floor != nil && id < floor.Next
			if below {
				reached = true
			}
			if !propStatuses[it[3]] {
				partial = append(partial, fmt.Sprintf("proposal #%s's status %q is none GovDAO's list wrote before (ACTIVE, ACCEPTED, REJECTED)", it[1], it[3]))
				continue
			}
			if it[3] != "ACTIVE" {
				continue // final (below the floor: closed since, if it was open)
			}
			if below && !wasOpen[id] {
				// open below the floor, where every proposal the floor does not hold open was final: another list — read whole
				return readProposals(ctx, g, dao, H, supermajority, nil, realms...)
			}
			list(id, strings.TrimSpace(it[2]), false)
		}
		more = false
		for _, m := range nextPageRE.FindAllStringSubmatch(lp, -1) {
			if n, _ := strconv.Atoi(m[1]); n > page {
				more = true
			}
		}
		if reached || !more || len(items) == 0 {
			more = false
			break
		}
	}
	if more {
		partial = append(partial, fmt.Sprintf("it runs past %d pages: the older proposals are not read", proposalPagesMax))
	}
	// the proposals open at the floor's reading that the pages above did not reach: each by its own page
	if floor != nil {
		ids := append([]int64(nil), floor.Open...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			if !seen[id] && id < floor.Next && !open[id] {
				seen[id] = true
				list(id, "", true)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(partial) > 0 {
		return out, "", unread, partial, nil
	}
	next := &ProposalFloor{DAO: dao, Next: newest + 1}
	if floor != nil {
		next.Next = max(next.Next, floor.Next)
	}
	for id := range open {
		next.Open = append(next.Open, id)
	}
	sort.Slice(next.Open, func(i, j int) bool { return next.Open[i] < next.Open[j] })
	return out, "", unread, nil, next
}

// pageRead is what a proposal's own page says, read from the lines GovDAO's render writes itself — its stats after the
// description and the executor's metadata, the executor's realm after both — never from the description, which a
// proposer writes as he likes: a "Executor created in", "YES PERCENT" or status line in it read first would hide a
// bridge proposal's realm, its votes or its being open (the review of I3's follow-ups: the first match was taken).
type pageRead struct {
	status string // ACTIVE ("Proposal is open for votes"), ACCEPTED, REJECTED ("DENIED"), elsewhere (no status here), or ""
	realm  string // the realm its executor was created in ("" when the page names none)
	yes    float64
	title  string
}

// pageOf reads a proposal's own page (impl v0's renderProposalPage: title, author, description, the executor's metadata,
// "Executor created in", then "### Stats" with the status line first and YES PERCENT).
func pageOf(pg string) pageRead {
	var r pageRead
	if elsewhereRE.MatchString(strings.TrimSpace(pg)) {
		r.status = "elsewhere"
		return r
	}
	if l, _, ok := strings.Cut(pg, "\n"); ok && strings.HasPrefix(l, "## Prop #") {
		if _, t, ok := strings.Cut(l, " - "); ok {
			r.title = strings.TrimSpace(t)
		}
	}
	head, stats := pg, ""
	if i := strings.LastIndex(pg, "\n### Stats\n"); i >= 0 {
		head, stats = pg[:i], pg[i+len("\n### Stats\n"):]
		first, _, _ := strings.Cut(stats, "\n")
		switch strings.TrimSpace(first) {
		case "- **Proposal is open for votes**":
			r.status = "ACTIVE"
		case "- **PROPOSAL HAS BEEN ACCEPTED**":
			r.status = "ACCEPTED"
		case "- **PROPOSAL HAS BEEN DENIED**":
			r.status = "REJECTED"
		}
	}
	if m := executorRE.FindAllStringSubmatch(head, -1); len(m) > 0 {
		r.realm = m[len(m)-1][1]
	}
	if m := yesRE.FindStringSubmatch(stats); m != nil {
		r.yes, _ = strconv.ParseFloat(m[1], 64)
	}
	return r
}

// listSeparators counts the "---" lines of a list page: GovDAO's render closes every item with one.
func listSeparators(list string) int {
	n := 0
	for _, l := range strings.Split(list, "\n") {
		if strings.TrimSpace(l) == "---" {
			n++
		}
	}
	return n
}

// ExecutorRealm is the realm a GovDAO proposal's page says its executor was created in ("" when it names none): the line
// GovDAO's render writes after the description, never one the description holds.
func ExecutorRealm(page string) string { return pageOf(page).realm }

// ProposalText is a GovDAO proposal page's description, its lines joined.
func ProposalText(page string) string { return proposalDescription(page) }

func proposalDescription(page string) string {
	lines := strings.Split(page, "\n")
	var out []string
	in := false
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "Author:"):
			in = true
			continue
		case strings.HasPrefix(l, "This proposal contains"), strings.HasPrefix(l, "Executor created in"), strings.HasPrefix(l, "---"):
			if in {
				return strings.TrimSpace(strings.Join(out, " "))
			}
		}
		if in && strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return strings.TrimSpace(strings.Join(out, " "))
}

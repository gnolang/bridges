package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The exported views of the gno.land side, for hlwatch (cmd/hlwatch): the same renders the machines read,
// pinned at one height, in plain values.

// MailboxView is hlmailbox at a height.
type MailboxView struct {
	Live               bool
	LaunchedAt         int64
	Governor           string // the compiled governor, "" when GovDAO governs from the first block (M1k)
	Yielded            int64  // the height the governor yielded at, 0 until the yield
	LaunchOrigin       uint32
	ActiveVerifier     string
	VerifierRegistered bool
	Rule               string
	SignerKeys         []string
	Seats              []string
	Flags              []FlagView
	Count              uint32
	Delivered          int
	Decisions          int64
	Clocks             Clocks
	// SuccessorRoutes is the route each announced successor announced for (AQ5), and Successions the registered
	// routes' places in a migration ("pred → succ" per migrated route); Enrolled the kind-6 enrolment record of
	// each route that has one (AR1: a refund proposal is read against it).
	SuccessorRoutes map[string]string
	Successions     []string
	Enrolled        map[string]Enrollment
	// RoutePaths are every registered route's pkgpath, sorted; Predecessors each successor's predecessor; Pulled
	// the successors that pulled page 0; DroppedRoutes the re-pointed-away successors, each with the predecessor
	// that dropped it (M1i: a kind 6 proposal and a burn's recipient are read against them).
	RoutePaths    []string
	Predecessors  map[string]string
	Pulled        map[string]bool
	DroppedRoutes map[string]string
}

// Enrollment is a route's kind-6 enrolment record (AR1): the height, and the time when the realm renders it.
type Enrollment struct {
	Height int64
	At     int64
}

// FlagView is one standing flag.
type FlagView struct {
	Route string
	Fn    string
	Seat  int
	Addr  string
	At    int64
	IDs   []string
}

// DAOPath is gno.land/r/gov/dao's pkgpath: the realms' decisions.DAOPath, the only governor after the yield.
const DAOPath = "gno.land/r/gov/dao"

// DAOGoverns is whether GovDAO's proxy decides now (the realms' decisions.DAOGoverns, M1k): no governor was
// compiled, or the governor yielded at that height (0 until the yield). Until then the governor's own call
// through Govern is a decision and a GovDAO proposal is refused by its closure.
func DAOGoverns(governor string, yielded int64) bool { return governor == "" || yielded != 0 }

// DAOGoverns is whether GovDAO decides on this mailbox's realms now (M1k).
func (m *mailboxJSON) DAOGoverns() bool { return DAOGoverns(m.Governor, m.Yielded) }

// DAOGoverns is whether GovDAO decides now (M1k): the governor yielded, or none was compiled.
func (m MailboxView) DAOGoverns() bool { return DAOGoverns(m.Governor, m.Yielded) }

// GovernorLine is the line every bridge realm's page shows of who governs it (the realms' decisions.GovernorLine):
// the governor until the yield, GovDAO from the yield's height, or GovDAO from the first block.
func (m MailboxView) GovernorLine() string {
	switch {
	case m.Governor == "":
		return "governor: " + DAOPath + " from the first block (no governor was compiled)"
	case m.Yielded == 0:
		return "governor: " + m.Governor + " (until the yield) — it alone decides, by Govern from its own key, and " + DAOPath + "'s proposals are refused"
	}
	return "governor: " + DAOPath + " since height " + strconv.FormatInt(m.Yielded, 10) + " (" + m.Governor + " yielded)"
}

// ReadMailbox reads hlmailbox's JSON at a height (0 for the latest).
func ReadMailbox(ctx context.Context, g gnoReader, mailbox string, height int64) (MailboxView, error) {
	m, err := mailboxAt(ctx, g, mailbox, height)
	if err != nil {
		return MailboxView{}, err
	}
	v := MailboxView{Live: m.Live, LaunchedAt: m.LaunchedAt, Governor: m.Governor, Yielded: m.Yielded, LaunchOrigin: m.LaunchOrigin, SignerKeys: m.Signers.Keys, Seats: m.Seats,
		Count: m.Count, Delivered: m.Delivered, Decisions: m.Decisions, Clocks: m.Clocks}
	if r, err := m.rule(); err == nil {
		v.Rule = r.String()
	} else {
		v.Rule = "unusable: " + err.Error()
	}
	for origin, ver := range m.Verifiers {
		_ = origin
		v.ActiveVerifier, v.VerifierRegistered = ver.PkgPath, ver.Registered
	}
	var routes []string
	for p := range m.Routes {
		routes = append(routes, p)
	}
	sort.Strings(routes)
	v.Predecessors, v.Pulled = map[string]string{}, map[string]bool{}
	for _, p := range routes {
		for _, f := range m.Routes[p].StandingFlags() {
			v.Flags = append(v.Flags, FlagView{Route: p, Fn: f.Fn, Seat: f.Seat, Addr: f.Addr, At: f.At, IDs: f.IDs})
		}
		if s := m.Routes[p].Successor; s != "" {
			v.Successions = append(v.Successions, p+" → "+s)
		}
		if height, at := m.Routes[p].Enrollment(); height > 0 || at > 0 {
			if v.Enrolled == nil {
				v.Enrolled = map[string]Enrollment{}
			}
			v.Enrolled[p] = Enrollment{Height: height, At: at}
		}
		if pred := m.Routes[p].Predecessor; pred != "" {
			v.Predecessors[p] = pred
		}
		if m.Routes[p].Pulled {
			v.Pulled[p] = true
		}
	}
	v.RoutePaths = routes
	v.SuccessorRoutes = m.SuccessorRoutes
	v.DroppedRoutes = m.DroppedRoutes
	return v, nil
}

// SendView is a route's send record by message id, as hlwatch reads one (M1i: a restore proposal may name a
// written-off send, R38).
type SendView struct {
	From       string
	Amount     int64
	Imported   bool
	Refunded   bool
	WrittenOff bool
}

// SendOf reads a send by id at a height; nil if the route holds no such send.
func SendOf(ctx context.Context, g gnoReader, route, id string, height int64) (*SendView, error) {
	sd, err := sendAt(ctx, g, route, id, height)
	if err != nil || sd == nil {
		return nil, err
	}
	return &SendView{From: sd.From, Amount: sd.Amount, Imported: sd.Imported, Refunded: bool(sd.Refunded), WrittenOff: bool(sd.WrittenOff)}, nil
}

// RouteView is a route's figures at a height.
type RouteView struct {
	Symbol                            string
	Gno                               string
	Live                              bool
	Governor                          string // the compiled governor and the yield's height, as the realm renders them (M1k)
	Yielded                           int64
	Paused                            string
	Lock, SentTotal, PaidTotal        int64
	Owed, Voided, Lost, Sweep         int64
	Cap, Meter, Fee, Minimum, Maximum int64
	OpenClaims, Claims, Sends         int64
	MigratedTo                        string
	Importing                         bool  // a predecessor's pages are arriving (K28)
	Imported                          int   // pages taken so far
	Refunded, RefundedTotal           int64 // the sends GovDAO refunded here, and their sum (AQ2)
	WrittenOff, WrittenOffTotal       int64 // the refunded sends delivered after all and booked as lost, and their sum (AR1)
	Clocks                            Clocks
}

// ImpliedSupply is what Solana's supply should be by the identity: lock − owed − voided + lost.
func (r RouteView) ImpliedSupply() int64 { return r.Lock - r.Owed - r.Voided + r.Lost }

// ReadRoute reads a route's JSON at a height.
func ReadRoute(ctx context.Context, g gnoReader, r Route, height int64) (RouteView, error) {
	j, err := routeAt(ctx, g, r.Gno, height)
	if err != nil {
		return RouteView{}, err
	}
	return RouteView{Symbol: r.Symbol, Gno: r.Gno, Live: j.Live, Governor: j.Governor, Yielded: j.Yielded, Paused: j.Paused, Lock: j.Lock, SentTotal: j.SentTotal, PaidTotal: j.PaidTotal,
		Owed: j.Owed, Voided: j.Voided, Lost: j.Lost, Sweep: j.Sweep, Cap: j.Cap, Meter: j.Meter, Fee: j.Fee, Minimum: j.Minimum, Maximum: j.Maximum,
		OpenClaims: j.OpenClaims, Claims: j.Claims, Sends: j.Sends, MigratedTo: j.MigratedTo, Importing: j.Importing, Imported: j.Imported, Refunded: j.Refunded, RefundedTotal: j.RefundedTotal,
		WrittenOff: j.WrittenOff, WrittenOffTotal: j.WrittenOffTotal, Clocks: j.Clocks}, nil
}

// LineRealms is each token's line of migrations at a height, by symbol, first realm to last — the configured
// realm alone for a token with no open migration, or whose line could not be read (logged) — and the realms of
// the lines the config does not name, in order, once each (G48: the proposals and queued decisions a reader
// lists come from every realm of a line, the configured one being the predecessor after a migration).
func LineRealms(ctx context.Context, g gnoReader, cfg *Config, H int64) (lines map[string][]string, extra []string) {
	lines = map[string][]string{}
	configured := map[string]bool{cfg.Gno.Mailbox: true, cfg.Gno.Fees: true, cfg.Gno.Gov: true}
	for _, r := range cfg.Routes {
		configured[r.Gno] = true
		lines[r.Symbol] = []string{r.Gno}
	}
	mb, err := mailboxAt(ctx, g, cfg.Gno.Mailbox, H)
	if err != nil {
		log.Printf("the lines of migrations: the mailbox: %v (the configured realms alone are read)", err)
		return lines, nil
	}
	routes := map[string]*routeJSON{}
	for _, r := range cfg.Routes {
		if rj, err := routeAt(ctx, g, r.Gno, H); err == nil {
			routes[r.Symbol] = rj
		} else {
			log.Printf("the lines of migrations: %s: %v (the configured realm alone is read)", r.Symbol, err)
		}
	}
	migs, errs := migrationsOf(ctx, g, cfg, routes, mb, H)
	for sym, err := range errs {
		log.Printf("the lines of migrations: %s: %v (the configured realm alone is read)", sym, err)
	}
	seen := map[string]bool{}
	for _, r := range cfg.Routes {
		if mig := migs[r.Symbol]; mig != nil {
			lines[r.Symbol] = mig.Paths()
		}
		for _, p := range lines[r.Symbol] {
			if !configured[p] && !seen[p] {
				seen[p] = true
				extra = append(extra, p)
			}
		}
	}
	return lines, extra
}

// PendingDecision is a queued decision of a realm and when it matures.
type PendingDecision struct {
	Realm    string
	ID       int64
	Kind     string
	Text     string
	Recorded int64
	Due      int64
}

// ReadPending lists every pending decision of every realm with a ledger, by realm then id; extra names the
// realms beyond the configured ones (the lines' successors, G48). Each realm's ledger is read whole
// (Render("decisions.json")): the Solana deployment's realms have no pages; the Ethereum deployment's are read by
// ReadPendingByPage.
func ReadPending(ctx context.Context, g gnoReader, cfg *Config, height int64, extra ...string) ([]PendingDecision, error) {
	var out []PendingDecision
	for _, realm := range ledgerRealms(cfg, extra) {
		ds, err := decisionsAt(ctx, g, realm, height)
		if err != nil {
			return nil, err
		}
		out = appendPending(out, realm, ds)
	}
	return out, nil
}

// ReadPendingByPage is ReadPending for realms that render their ledger by page — the Ethereum deployment's (eth
// PLAN.md §5.3, §13: "decisions.json/<from>", the realms' DecisionsJSONPage) — so no read grows with a ledger: the
// whole decisions.json of a realm past ≈360 decisions is out of vm/qrender's gas (E3-RL4), and read whole it blanked
// the site's every pending decision (E4-TL7). Each realm is read page by page from decision 1 until an empty page, as
// the Ethereum machines read it (eth/bridge/gnoside.DecisionsFrom); a realm whose first page renders "unknown path"
// has no pages and is read whole. The realms that read are listed; each that does not is one error of the joined
// error returned beside them (errors.Join).
func ReadPendingByPage(ctx context.Context, g gnoReader, cfg *Config, height int64, extra ...string) ([]PendingDecision, error) {
	var out []PendingDecision
	var errs []error
	for _, realm := range ledgerRealms(cfg, extra) {
		ds, err := decisionsByPage(ctx, g, realm, height)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", realm, err))
			continue
		}
		out = appendPending(out, realm, ds)
	}
	return out, errors.Join(errs...)
}

// ledgerRealms are the realms with a ledger, each once: the mailbox, the routes, the fees realm, r/hlgov, then extra
// (the lines' successors, G48).
func ledgerRealms(cfg *Config, extra []string) []string {
	realms := []string{cfg.Gno.Mailbox}
	for _, r := range cfg.Routes {
		realms = append(realms, r.Gno)
	}
	if cfg.Gno.Fees != "" {
		realms = append(realms, cfg.Gno.Fees)
	}
	if cfg.Gno.Gov != "" {
		realms = append(realms, cfg.Gno.Gov)
	}
	seen := map[string]bool{}
	for _, p := range realms {
		seen[p] = true
	}
	for _, p := range extra {
		if p != "" && !seen[p] {
			seen[p] = true
			realms = append(realms, p)
		}
	}
	return realms
}

func appendPending(out []PendingDecision, realm string, ds []decisionJSON) []PendingDecision {
	for _, d := range ds {
		if d.State == "pending" {
			out = append(out, PendingDecision{Realm: realm, ID: d.ID, Kind: d.Kind, Text: d.Text, Recorded: d.Recorded, Due: d.Due})
		}
	}
	return out
}

// ledgerPages bounds the pages one ledger read walks: a realm whose pages never end is no ledger.
const ledgerPages = 100_000

// decisionsByPage reads a realm's ledger at a height page by page: Render("decisions.json/<from>") from decision 1,
// each page's decisions numbered from, from+1, … (anything else is no page of the ledger), on from the last one read
// plus one until an empty page ("[]": a page shorter than twenty is no end — the realm cuts a page at 16 KB of text). A
// realm whose first page is "unknown path" renders no pages (a realm from before them, every Solana one): its whole
// decisions.json is read instead. A render that fails is the answer, no fallback.
func decisionsByPage(ctx context.Context, g gnoReader, realm string, height int64) ([]decisionJSON, error) {
	var all []decisionJSON
	from := int64(1)
	for page := 0; ; page++ {
		if page == ledgerPages {
			return nil, fmt.Errorf("its decisions run past %d pages", ledgerPages)
		}
		path := "decisions.json/" + strconv.FormatInt(from, 10)
		s, err := g.RenderAt(ctx, realm, path, height)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(strings.TrimSpace(s), "unknown path") {
			if page > 0 {
				return nil, fmt.Errorf("it renders %s as an unknown path after %d pages of its decisions", path, page)
			}
			return decisionsAt(ctx, g, realm, height)
		}
		var ds []decisionJSON
		if err := jsonUnmarshal(s, &ds); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for i, d := range ds {
			if d.ID != from+int64(i) {
				return nil, fmt.Errorf("%s holds decision #%d at place %d (want #%d): no page of its ledger", path, d.ID, i, from+int64(i))
			}
		}
		if len(ds) == 0 {
			return all, nil
		}
		all = append(all, ds...)
		from += int64(len(ds))
	}
}

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
	Armed  bool // has the votes GovDAO's law needs: anyone may execute it (a pre-armed one, §5.8)
}

var (
	propItemRE = regexp.MustCompile(`(?m)^### \[Prop #(\d+) - ([^\]]*)\]\([^)]*\)\s*\n(?:.*\n)*?Status: (\w+)`)
	yesRE      = regexp.MustCompile(`YES PERCENT: ([0-9.]+)%`)
	executorRE = regexp.MustCompile("Executor created in: `([^`]+)`")
	nextPageRE = regexp.MustCompile(`\?page=(\d+)`)
)

// ReadProposals reads r/gov/dao's list page by page and every open proposal's page at a height; a proposal
// counts as the bridge's when its executor was created in one of the realms — the configured ones and extra,
// the lines' realms the config does not name (G48: a successor's refund, its second migrate and its lapsed
// proposal are its own) — (the title is GovDAO's own field and free), and as pre-armed when it has the votes
// GovDAO's law needs and was not executed. The DAO's page format is gno's examples' r/gov/dao (impl v0); the
// parse is loose, and another implementation needs a look. The second answer says why nothing could be read
// ("" when the list was read).
func ReadProposals(ctx context.Context, g gnoReader, cfg *Config, H int64, supermajority float64, extra ...string) ([]Proposal, string) {
	realms := map[string]bool{cfg.Gno.Mailbox: true, cfg.Gno.Fees: true, cfg.Gno.Gov: true}
	for _, r := range cfg.Routes {
		realms[r.Gno] = true
	}
	for _, p := range extra {
		if p != "" {
			realms[p] = true
		}
	}
	var out []Proposal
	seen := map[int64]bool{}
	for page := 1; page <= 50; page++ {
		path := ""
		if page > 1 {
			path = "?page=" + strconv.Itoa(page)
		}
		list, err := g.RenderAt(ctx, cfg.Gno.DAO, path, H)
		if err != nil {
			return nil, "r/gov/dao: " + err.Error()
		}
		if strings.Contains(list, "DAO not initialized") {
			return nil, "r/gov/dao: not initialized"
		}
		items := propItemRE.FindAllStringSubmatch(list, -1)
		for _, it := range items {
			id, _ := strconv.ParseInt(it[1], 10, 64)
			if seen[id] {
				continue
			}
			seen[id] = true
			status := it[3]
			if status != "ACTIVE" {
				continue // ACCEPTED is executed; REJECTED is denied (a failed execution included)
			}
			pg, err := g.RenderAt(ctx, cfg.Gno.DAO, it[1], H)
			if err != nil {
				continue
			}
			ex := executorRE.FindStringSubmatch(pg)
			if ex == nil || !realms[ex[1]] {
				continue
			}
			p := Proposal{ID: id, Title: strings.TrimSpace(it[2]), Status: status, Realm: ex[1], Text: proposalDescription(pg)}
			if m := yesRE.FindStringSubmatch(pg); m != nil {
				p.Yes, _ = strconv.ParseFloat(m[1], 64)
			}
			p.Armed = p.Yes >= supermajority
			out = append(out, p)
		}
		// Another page only when the picker names one past this.
		more := false
		for _, m := range nextPageRE.FindAllStringSubmatch(list, -1) {
			if n, _ := strconv.Atoi(m[1]); n > page {
				more = true
			}
		}
		if !more || len(items) == 0 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, ""
}

// proposalDescription is the builder's text on a proposal page: after the author line, before the metadata.
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

// GovView is r/hlgov's counter, its decision recipient and the former recipients a swap replaced (R29: retired
// admin programs, which r/hlgov refuses as a kind 9's successor and as the recipient for good).
type GovView struct {
	Address   string
	Governor  string // the compiled governor and the yield's height, as r/hlgov renders them (M1k)
	Yielded   int64
	Recipient string
	Former    []string
	Last      uint64
	Next      uint64
}

// GovDecision is one decision r/hlgov dispatched, as hlwatch lists the recent ones: its number and kind, what its
// parameters say (sol.DescribeParams: a cancel with its salt, kind 4 as reserved), how many times it was
// dispatched and the last message id.
type GovDecision struct {
	Number uint64
	Kind   byte
	Text   string
	Count  int
	ID     string
	At     int64
}

// ReadGovDecisions reads r/hlgov's last n dispatched decisions (decision/<k>, numbers last−n+1..last) at a
// height, highest first.
func ReadGovDecisions(ctx context.Context, g gnoReader, gov string, last uint64, n int, height int64) ([]GovDecision, error) {
	var out []GovDecision
	for k := last; k >= 1 && len(out) < n; k-- {
		s, err := g.RenderAt(ctx, gov, "decision/"+strconv.FormatUint(k, 10), height)
		if err != nil {
			return out, err
		}
		if strings.TrimSpace(s) == "null" {
			continue
		}
		var j struct {
			Number uint64 `json:"number"`
			Kind   byte   `json:"kind"`
			Body   string `json:"body"`
			ID     string `json:"id"`
			Count  int    `json:"count"`
			At     int64  `json:"at"`
		}
		if err := jsonUnmarshal(s, &j); err != nil {
			return out, err
		}
		body, err := hex.DecodeString(j.Body)
		if err != nil || len(body) < 9 {
			return out, fmt.Errorf("r/hlgov's decision %d: a body of %d bytes", k, len(body))
		}
		out = append(out, GovDecision{Number: j.Number, Kind: j.Kind, Text: sol.DescribeParams(j.Kind, body[9:]), Count: j.Count, ID: j.ID, At: j.At})
	}
	return out, nil
}

// ReadGov reads r/hlgov's JSON at a height.
func ReadGov(ctx context.Context, g gnoReader, gov string, height int64) (GovView, error) {
	var j struct {
		Address   string   `json:"address"`
		Governor  string   `json:"governor"`
		Yielded   int64    `json:"yielded"`
		Recipient string   `json:"recipient"`
		Former    []string `json:"former"`
		Last      uint64   `json:"last"`
		Next      uint64   `json:"next"`
	}
	s, err := g.RenderAt(ctx, gov, "json", height)
	if err != nil {
		return GovView{}, err
	}
	if err := jsonUnmarshal(s, &j); err != nil {
		return GovView{}, err
	}
	return GovView{Address: j.Address, Governor: j.Governor, Yielded: j.Yielded, Recipient: j.Recipient, Former: j.Former, Last: j.Last, Next: j.Next}, nil
}

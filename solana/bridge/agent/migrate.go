package agent

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A route's migration (PLAN.md §5.3) as the machines read it (AQ1, AQ3, K28): once a migrate decision applied,
// the predecessor's json names its successor (`migratedTo`), page 0 moves every counter to the successor in one
// transaction and the successor's `importing` stands from its first page to its last. The token is one token
// throughout — one Solana supply against two realms — so the monitors and hlwatch judge it over the pair's
// summed counters while a migration is open (never a migrated or importing route alone: the predecessor shows
// lock 0 against the live supply from page 0, the successor the whole lock, each BROKEN on its own), and the
// relayer brings a burn addressed to the predecessor to the successor once its import has ended. The config may
// name a route's successor or predecessor; otherwise they are found on the chain — the successor by
// `migratedTo`, the predecessor of a configured successor among the mailbox's registered routes.
//
// A migration is a line, not a pair (AR6, G41): a successor may itself migrate on, and hlmailbox walks sixteen
// of them (`maxChain`), so the tools follow `migratedTo` from the configured path to the chain's end — summing
// every realm of the line, delivering, claiming and flagging on its last — and back to the line's first by the
// mailbox's `predecessor` entries. Before, a second migration without a config edit on four machines summed the
// first pair to 0 against the live supply (BROKEN, both tokens' deliveries flagged) and dropped the burns
// addressed to the last successor as nobody's.

// maxChain is how far a line of migrations is followed, hlmailbox's own bound (flags.gno: a chain never cycles).
const maxChain = 16

// migration is one token's open migration: the line from its first realm to its last, every realm read at one
// height. Predecessor and Pred are the line's first realm, Successor and Succ its last — the live one, which
// credits and pays the token now; Hops the realms between them, in order (none for a single migration).
type migration struct {
	Symbol      string
	Predecessor string // pkgpath of the line's first realm
	Successor   string // pkgpath of the line's last realm, the live one
	Pred, Succ  *routeJSON
	Hops        []migrationHop
}

// migrationHop is a realm between the line's first and its last: one that imported its predecessor's pages and
// migrated on.
type migrationHop struct {
	Path string
	JSON *routeJSON
}

// Importing says whether the live successor is between its first page and its last (K28: shown and alarmed).
func (m *migration) Importing() bool { return m != nil && m.Succ != nil && m.Succ.Importing }

// Ended says whether the live successor's import has ended (AQ1: a burn addressed to any realm of the line goes
// to the successor from then on).
func (m *migration) Ended() bool { return m != nil && m.Succ != nil && m.Succ.ImportEnded() }

// Live is the realm that credits and pays the token now: the line's last successor (every realm before it
// refuses every function once migrated).
func (m *migration) Live() string { return m.Successor }

// Paths are the line's realms in order, first to last.
func (m *migration) Paths() []string {
	out := []string{m.Predecessor}
	for _, h := range m.Hops {
		out = append(out, h.Path)
	}
	return append(out, m.Successor)
}

// realms are the line's jsons in the Paths' order (a nil for one not read).
func (m *migration) realms() []*routeJSON {
	out := []*routeJSON{m.Pred}
	for _, h := range m.Hops {
		out = append(out, h.JSON)
	}
	return append(out, m.Succ)
}

// Has says whether a pkgpath is a realm of the line.
func (m *migration) Has(path string) bool {
	if m == nil {
		return false
	}
	for _, p := range m.Paths() {
		if p == path {
			return true
		}
	}
	return false
}

// Others are the line's realms other than one, the last first (the records of a migrated route end up on the
// live successor, so a read by id tries it before the realms between).
func (m *migration) Others(path string) []string {
	if m == nil {
		return nil
	}
	paths := m.Paths()
	var out []string
	for i := len(paths) - 1; i >= 0; i-- {
		if paths[i] != path {
			out = append(out, paths[i])
		}
	}
	return out
}

// Sum is the line's counters added (AQ3: the sum is invariant at every height of the migration — page 0 moves
// them in one transaction, the later pages move records whose amounts are inside owed and the lock already;
// AR6: a realm that migrated on holds 0 of each once its successor's page 0 landed).
func (m *migration) Sum() (lock, owed, voided, lost int64) {
	for _, j := range m.realms() {
		if j != nil {
			lock += j.Lock
			owed += j.Owed
			voided += j.Voided
			lost += j.Lost
		}
	}
	return lock, owed, voided, lost
}

// Line writes the line as "first → … → last".
func (m *migration) Line() string { return strings.Join(m.Paths(), " → ") }

// Text names the migration for a status line.
func (m *migration) Text() string {
	state := "the import has not begun"
	switch {
	case m.Importing():
		state = "IMPORTING (" + strconv.Itoa(m.Succ.Imported) + " pages so far)"
	case m.Ended():
		state = "the import ended (" + strconv.Itoa(m.Succ.Imported) + " pages)"
	}
	over := "the pair's"
	if len(m.Hops) > 0 {
		over = "the line's " + strconv.Itoa(len(m.Hops)+2) + " realms'"
	}
	return fmt.Sprintf("migrating %s, %s; the identity is judged over %s summed counters", m.Line(), state, over)
}

// WaitText says why a burn addressed to a realm of the line waits (AQ1, AR3: never parked, the status names why).
func (m *migration) WaitText() string {
	if m.Importing() {
		return fmt.Sprintf("%s migrated to %s, whose import is still arriving (%d pages so far): delivered to the successor once its last page is in (AQ1)", m.Predecessor, m.Successor, m.Succ.Imported)
	}
	return fmt.Sprintf("%s migrated to %s, whose import has not begun (no page pulled yet): delivered to the successor once its last page is in (AQ1, AR3)", m.Predecessor, m.Successor)
}

// routeReader reads a route realm's json by pkgpath — at the poll's height for the monitors and hlwatch, from
// the relayer's per-step cache at the head.
type routeReader func(ctx context.Context, path string) (*routeJSON, error)

// routeReaderAt reads routes through a gno node at one height.
func routeReaderAt(g gnoReader, H int64) routeReader {
	return func(ctx context.Context, path string) (*routeJSON, error) { return routeAt(ctx, g, path, H) }
}

// successorNamed is the successor a route's json or the mailbox's entry names for it ("" for none).
func successorNamed(path string, rj *routeJSON, mb *mailboxJSON) string {
	if rj != nil && rj.MigratedTo != "" {
		return rj.MigratedTo
	}
	if mb != nil {
		return mb.Routes[path].Successor
	}
	return ""
}

// followLine follows a line of migrations forward from a realm whose json is read already: every successor the
// jsons (or the mailbox's entries) name, at most maxChain hops, a loop refused. It answers the hops after the
// start and the last realm; nil hops and "" when the start migrated nowhere.
func followLine(ctx context.Context, read routeReader, mb *mailboxJSON, start string, sj *routeJSON) (hops []migrationHop, last string, lj *routeJSON, err error) {
	seen := map[string]bool{start: true}
	path, rj := start, sj
	for hop := 0; ; hop++ {
		next := successorNamed(path, rj, mb)
		if next == "" {
			if path == start {
				return nil, "", nil, nil
			}
			return hops, path, rj, nil
		}
		if hop >= maxChain {
			return nil, "", nil, fmt.Errorf("the line of migrations from %s runs past %d hops at %s → %s", start, maxChain, path, next)
		}
		if seen[next] {
			return nil, "", nil, fmt.Errorf("the line of migrations from %s loops at %s → %s", start, path, next)
		}
		seen[next] = true
		nj, err := read(ctx, next)
		if err != nil {
			return nil, "", nil, fmt.Errorf("%s's successor %s: %w", path, next, err)
		}
		if path != start {
			hops = append(hops, migrationHop{Path: path, JSON: rj})
		}
		path, rj = next, nj
	}
}

// predecessorsOf follows the mailbox's predecessor entries back from a realm (AddRoute records them at each
// migrate's apply): the realms before it, oldest first, each read; at most maxChain, a loop refused. first is
// the configured predecessor of a successor (the config's or the walk's), followed from too.
func predecessorsOf(ctx context.Context, read routeReader, mb *mailboxJSON, path, first string) (paths []string, jsons []*routeJSON, err error) {
	seen := map[string]bool{path: true}
	pred := first
	if mb != nil && mb.Routes[path].Predecessor != "" {
		pred = mb.Routes[path].Predecessor
	}
	for hop := 0; pred != ""; hop++ {
		if hop >= maxChain {
			return nil, nil, fmt.Errorf("the line of migrations before %s runs past %d hops at %s", path, maxChain, pred)
		}
		if seen[pred] {
			return nil, nil, fmt.Errorf("the line of migrations before %s loops at %s", path, pred)
		}
		seen[pred] = true
		pj, err := read(ctx, pred)
		if err != nil {
			return nil, nil, fmt.Errorf("the predecessor %s: %w", pred, err)
		}
		paths = append([]string{pred}, paths...)
		jsons = append([]*routeJSON{pj}, jsons...)
		next := ""
		if mb != nil {
			next = mb.Routes[pred].Predecessor
		}
		pred = next
	}
	return paths, jsons, nil
}

// lineOf assembles a migration from a line's realms (first to last, two at least).
func lineOf(symbol string, paths []string, jsons []*routeJSON) *migration {
	m := &migration{Symbol: symbol, Predecessor: paths[0], Pred: jsons[0], Successor: paths[len(paths)-1], Succ: jsons[len(jsons)-1]}
	for i := 1; i < len(paths)-1; i++ {
		m.Hops = append(m.Hops, migrationHop{Path: paths[i], JSON: jsons[i]})
	}
	return m
}

// migrationOf reads a configured route's migration: its `migratedTo` (or the mailbox's entry for it) names its
// successor, followed to the line's end (AR6); a route that is importing or imported is a successor, whose
// predecessor is the mailbox's entry for it, the config's (`predecessor`), or the registered route whose
// `migratedTo` names it, followed back to the line's first; nil for a route with one live realm. rj is the
// route's json, read already.
func migrationOf(ctx context.Context, read routeReader, r Route, rj *routeJSON, mb *mailboxJSON) (*migration, error) {
	if rj == nil {
		return nil, nil
	}
	hops, last, lj, err := followLine(ctx, read, mb, r.Gno, rj)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.Symbol, err)
	}
	// the realms before the configured one, if it is a successor: the mailbox's entry, the config, else the
	// registered route whose migratedTo names it
	first := ""
	if rj.IsSuccessor() || (mb != nil && mb.Routes[r.Gno].Predecessor != "") {
		first = r.Predecessor
		if (mb == nil || mb.Routes[r.Gno].Predecessor == "") && first == "" && mb != nil {
			var paths []string
			for p := range mb.Routes {
				if p != r.Gno {
					paths = append(paths, p)
				}
			}
			sort.Strings(paths)
			for _, p := range paths {
				pj, err := read(ctx, p)
				if err != nil {
					return nil, fmt.Errorf("%s: the registered route %s: %w", r.Symbol, p, err)
				}
				if pj.MigratedTo == r.Gno {
					first = p
					break
				}
			}
		}
	}
	before, beforeJSON, err := predecessorsOf(ctx, read, mb, r.Gno, first)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.Symbol, err)
	}
	if last == "" && len(before) == 0 {
		// a live route, or a successor whose predecessor is not registered here (judged alone: its counters
		// are 0 once page 0 moved); the config may name a successor the chain does not yet — the migrate has
		// not applied (or the operator is early) — nothing has moved, the route is judged alone
		return nil, nil
	}
	paths := append(append([]string{}, before...), r.Gno)
	jsons := append(append([]*routeJSON{}, beforeJSON...), rj)
	for _, h := range hops {
		paths = append(paths, h.Path)
		jsons = append(jsons, h.JSON)
	}
	if last != "" {
		paths = append(paths, last)
		jsons = append(jsons, lj)
	}
	return lineOf(r.Symbol, paths, jsons), nil
}

// migrationsOf reads every configured route's migration at a height, by symbol (only the open ones), and the
// tokens whose line could not be read, by symbol (G44: one partner's render error is that token's "no verdict",
// never the whole poll's — every route is tried).
func migrationsOf(ctx context.Context, g gnoReader, cfg *Config, routes map[string]*routeJSON, mb *mailboxJSON, H int64) (map[string]*migration, map[string]error) {
	out := map[string]*migration{}
	errs := map[string]error{}
	read := routeReaderAt(g, H)
	for _, r := range cfg.Routes {
		m, err := migrationOf(ctx, read, r, routes[r.Symbol], mb)
		if err != nil {
			errs[r.Symbol] = err
			continue
		}
		if m != nil {
			out[r.Symbol] = m
		}
	}
	return out, errs
}

// identityOf is a token's identity over its route, or over the line's summed counters while a migration is
// open (AQ3, AR6).
func identityOf(r Route, rj *routeJSON, mig *migration, H int64, supply int64, slot uint64, inflightOut, inflightIn int64) Identity {
	id := Identity{Symbol: r.Symbol, Height: H, Supply: supply, Slot: slot, InflightOut: inflightOut, InflightIn: inflightIn}
	if mig != nil {
		id.Lock, id.Owed, id.Voided, id.Lost = mig.Sum()
		return id
	}
	if rj != nil {
		id.Lock, id.Owed, id.Voided, id.Lost = rj.Lock, rj.Owed, rj.Voided, rj.Lost
	}
	return id
}

// claimOnPair is a claim by id on the route, or on another realm of its line when the route holds none (a claim
// migrated with its page, AQ3's reading of a diverted credit after the move): the live successor first.
func claimOnPair(ctx context.Context, g gnoReader, route string, mig *migration, id string, H int64) (*claimJSON, string, error) {
	c, err := claimAt(ctx, g, route, id, H)
	if err != nil || c != nil || mig == nil {
		return c, route, err
	}
	for _, other := range mig.Others(route) {
		c, err = claimAt(ctx, g, other, id, H)
		if err != nil || c != nil {
			return c, other, err
		}
	}
	return nil, route, nil
}

// sendOnPair is a send record by id on the route, or on another realm of its line when the route holds none (a
// send never refunded is exported with its page; a refunded one stays, AQ2): the live successor first.
func sendOnPair(ctx context.Context, g gnoReader, route string, mig *migration, id string, H int64) (*sendJSON, error) {
	sd, err := sendAt(ctx, g, route, id, H)
	if err != nil || sd != nil || mig == nil {
		return sd, err
	}
	for _, other := range mig.Others(route) {
		sd, err = sendAt(ctx, g, other, id, H)
		if err != nil || sd != nil {
			return sd, err
		}
	}
	return nil, nil
}

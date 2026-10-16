package gnoside

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A route's migration (the Solana plan §5.3, carried unchanged): once a migrate decision applied, the predecessor's
// json names its successor (`migratedTo`), page 0 moves every counter to the successor in one transaction and the
// successor's `importing` stands from its first page to its last. A migration is a line, not a pair (AR6, G41): a
// successor may migrate on, and hlmailbox walks sixteen of them, so the machines follow `migratedTo` from the
// configured realm to the line's end — delivering, claiming and flagging on its last — and back to the line's first
// by the mailbox's `predecessor` entries. The Solana agent's migrate.go, exported.

// MaxChain is how far a line of migrations is followed, hlmailbox's own bound.
const MaxChain = 16

// Migration is one token's open migration: the line from its first realm to its last, every realm read at one
// height.
type Migration struct {
	Symbol      string
	Predecessor string // the line's first realm
	Successor   string // the line's last realm, the live one
	Pred, Succ  *Route
	Hops        []MigrationHop
}

// MigrationHop is a realm between the line's first and its last.
type MigrationHop struct {
	Path string
	JSON *Route
}

// Importing says whether the live successor is between its first page and its last.
func (m *Migration) Importing() bool { return m != nil && m.Succ != nil && m.Succ.Importing }

// Ended says whether the live successor's import has ended.
func (m *Migration) Ended() bool { return m != nil && m.Succ != nil && m.Succ.ImportEnded() }

// Live is the realm that credits and pays the token now.
func (m *Migration) Live() string { return m.Successor }

// Paths are the line's realms in order, first to last.
func (m *Migration) Paths() []string {
	out := []string{m.Predecessor}
	for _, h := range m.Hops {
		out = append(out, h.Path)
	}
	return append(out, m.Successor)
}

// Realms are the line's jsons in the Paths' order.
func (m *Migration) Realms() []*Route {
	out := []*Route{m.Pred}
	for _, h := range m.Hops {
		out = append(out, h.JSON)
	}
	return append(out, m.Succ)
}

// Has says whether a pkgpath is a realm of the line.
func (m *Migration) Has(path string) bool {
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

// Others are the line's realms other than one, the last first.
func (m *Migration) Others(path string) []string {
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

// Sum is the line's counters added (AQ3: invariant at every height of the migration).
func (m *Migration) Sum() (lock, owed, voided, lost int64) {
	for _, j := range m.Realms() {
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
func (m *Migration) Line() string { return strings.Join(m.Paths(), " → ") }

// Text names the migration for a status line.
func (m *Migration) Text() string {
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

// WaitText says why a burn addressed to a realm of the line waits.
func (m *Migration) WaitText() string {
	if m.Importing() {
		return fmt.Sprintf("%s migrated to %s, whose import is still arriving (%d pages so far): delivered to the successor once its last page is in (AQ1)", m.Predecessor, m.Successor, m.Succ.Imported)
	}
	return fmt.Sprintf("%s migrated to %s, whose import has not begun (no page pulled yet): delivered to the successor once its last page is in (AQ1, AR3)", m.Predecessor, m.Successor)
}

// RouteReader reads a route realm's json by pkgpath.
type RouteReader func(ctx context.Context, path string) (*Route, error)

// RouteReaderAt reads routes through a gno node at one height.
func RouteReaderAt(g Reader, H int64) RouteReader {
	return func(ctx context.Context, path string) (*Route, error) { return RouteAt(ctx, g, path, H) }
}

func successorNamed(path string, rj *Route, mb *Mailbox) string {
	if rj != nil && rj.MigratedTo != "" {
		return rj.MigratedTo
	}
	if mb != nil {
		return mb.Routes[path].Successor
	}
	return ""
}

func followLine(ctx context.Context, read RouteReader, mb *Mailbox, start string, sj *Route) (hops []MigrationHop, last string, lj *Route, err error) {
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
		if hop >= MaxChain {
			return nil, "", nil, fmt.Errorf("the line of migrations from %s runs past %d hops at %s → %s", start, MaxChain, path, next)
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
			hops = append(hops, MigrationHop{Path: path, JSON: rj})
		}
		path, rj = next, nj
	}
}

func predecessorsOf(ctx context.Context, read RouteReader, mb *Mailbox, path, first string) (paths []string, jsons []*Route, err error) {
	seen := map[string]bool{path: true}
	pred := first
	if mb != nil && mb.Routes[path].Predecessor != "" {
		pred = mb.Routes[path].Predecessor
	}
	for hop := 0; pred != ""; hop++ {
		if hop >= MaxChain {
			return nil, nil, fmt.Errorf("the line of migrations before %s runs past %d hops at %s", path, MaxChain, pred)
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
		jsons = append([]*Route{pj}, jsons...)
		next := ""
		if mb != nil {
			next = mb.Routes[pred].Predecessor
		}
		pred = next
	}
	return paths, jsons, nil
}

func lineOf(symbol string, paths []string, jsons []*Route) *Migration {
	m := &Migration{Symbol: symbol, Predecessor: paths[0], Pred: jsons[0], Successor: paths[len(paths)-1], Succ: jsons[len(jsons)-1]}
	for i := 1; i < len(paths)-1; i++ {
		m.Hops = append(m.Hops, MigrationHop{Path: paths[i], JSON: jsons[i]})
	}
	return m
}

// MigrationOf reads a configured route's migration: its `migratedTo` (or the mailbox's entry) followed to the line's
// end; a route that is importing or imported is a successor, whose predecessor is the mailbox's entry for it, the
// configured one, or the registered route whose `migratedTo` names it, followed back to the line's first; nil for a
// route with one live realm. rj is the route's json, read already.
func MigrationOf(ctx context.Context, read RouteReader, symbol, gnoPath, configuredPredecessor string, rj *Route, mb *Mailbox) (*Migration, error) {
	if rj == nil {
		return nil, nil
	}
	hops, last, lj, err := followLine(ctx, read, mb, gnoPath, rj)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", symbol, err)
	}
	first := ""
	if rj.IsSuccessor() || (mb != nil && mb.Routes[gnoPath].Predecessor != "") {
		first = configuredPredecessor
		if (mb == nil || mb.Routes[gnoPath].Predecessor == "") && first == "" && mb != nil {
			var paths []string
			for p := range mb.Routes {
				if p != gnoPath {
					paths = append(paths, p)
				}
			}
			sort.Strings(paths)
			for _, p := range paths {
				pj, err := read(ctx, p)
				if err != nil {
					return nil, fmt.Errorf("%s: the registered route %s: %w", symbol, p, err)
				}
				if pj.MigratedTo == gnoPath {
					first = p
					break
				}
			}
		}
	}
	before, beforeJSON, err := predecessorsOf(ctx, read, mb, gnoPath, first)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", symbol, err)
	}
	if last == "" && len(before) == 0 {
		return nil, nil
	}
	paths := append(append([]string{}, before...), gnoPath)
	jsons := append(append([]*Route{}, beforeJSON...), rj)
	for _, h := range hops {
		paths = append(paths, h.Path)
		jsons = append(jsons, h.JSON)
	}
	if last != "" {
		paths = append(paths, last)
		jsons = append(jsons, lj)
	}
	return lineOf(symbol, paths, jsons), nil
}

// SendOnLine is a send record by id on the route, or on another realm of its line when the route holds none (the
// live successor first).
func SendOnLine(ctx context.Context, g Reader, route string, mig *Migration, id string, H int64) (*Send, error) {
	sd, err := SendAt(ctx, g, route, id, H)
	if err != nil || sd != nil || mig == nil {
		return sd, err
	}
	for _, other := range mig.Others(route) {
		sd, err = SendAt(ctx, g, other, id, H)
		if err != nil || sd != nil {
			return sd, err
		}
	}
	return nil, nil
}

// ClaimOnLine is a claim by id on the route, or on another realm of its line when the route holds none.
func ClaimOnLine(ctx context.Context, g Reader, route string, mig *Migration, id string, H int64) (*Claim, string, error) {
	c, err := ClaimAt(ctx, g, route, id, H)
	if err != nil || c != nil || mig == nil {
		return c, route, err
	}
	for _, other := range mig.Others(route) {
		c, err = ClaimAt(ctx, g, other, id, H)
		if err != nil || c != nil {
			return c, other, err
		}
	}
	return nil, route, nil
}

// Refundable says whether a route's sends may carry a refund mark (AQ2, G42): a refund is a successor's duty over
// the sends it imported, so only a route of an open line, a successor or a migrated route can hold one.
func Refundable(rj *Route, mig *Migration) bool {
	return mig != nil || (rj != nil && (rj.IsSuccessor() || rj.MigratedTo != ""))
}

package gnoside

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// ledgerNode is a realm's decision ledger as a node renders it: n decisions numbered 1…n, each pending when its
// number is in pending; paged renders decisions.json/<from> (DecisionsPage at a time, from decision number <from> on),
// otherwise it is a realm built before the pages, which answers "unknown path" for them; whole is whether the whole
// decisions.json still renders (past ≈360 decisions it is out of query gas, E3-RL4). Every render is counted.
type ledgerNode struct {
	n       int64
	pending map[int64]bool
	paged   bool
	whole   bool
	renders []string
	// long are decisions whose text is long: a page ends after one of them, as the realm ends a page before its text
	// would pass 16 KB (so a page may hold fewer than DecisionsPage and not be the ledger's end)
	long map[int64]bool
}

func (l *ledgerNode) decision(id int64) string {
	state, due := "applied", int64(1_700_000_000)
	if l.pending[id] {
		state, due = "pending", 1_700_604_800
	}
	return fmt.Sprintf(`{"id":%d,"kind":"set fee","text":"fee %d","state":%q,"made":1700000000,"recorded":1700000000,"due":%d,"applied":0,"cancelled":0,"note":""}`, id, id, state, due)
}

func (l *ledgerNode) RenderAt(_ context.Context, realm, path string, _ int64) (string, error) {
	l.renders = append(l.renders, path)
	switch {
	case path == "decisions.json":
		if !l.whole {
			return "", errors.New("gno query vm/qrender: OutOfGasError{CPUCycles} (the whole ledger)")
		}
		var parts []string
		for id := int64(1); id <= l.n; id++ {
			parts = append(parts, l.decision(id))
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	case strings.HasPrefix(path, "decisions.json/") && l.paged:
		from, err := strconv.ParseInt(path[len("decisions.json/"):], 10, 64)
		if err != nil {
			return "[]", nil
		}
		var parts []string
		for id := max(from, 1); id <= l.n && len(parts) < DecisionsPage; id++ {
			parts = append(parts, l.decision(id))
			if l.long[id] {
				break
			}
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	}
	return "unknown path; see the page", nil
}

func (l *ledgerNode) Status(context.Context) (gno.Head, error) { return gno.Head{}, nil }
func (l *ledgerNode) Block(_ context.Context, h int64) (gno.Head, error) {
	return gno.Head{Height: h}, nil
}

// E3-RL4's Go half: the decisions are read page by page (decisions.json/<from>, 20 at a time from decision number
// <from> on), so a ledger whose whole page is out of query gas still reads; a realm built before the pages ("unknown
// path") is read whole, as before; a page that is not the ledger's (numbers out of place) is refused.
func TestDecisionsReadByPage(t *testing.T) {
	ctx := context.Background()
	ids := func(ds []Decision) string {
		var parts []string
		for _, d := range ds {
			parts = append(parts, strconv.FormatInt(d.ID, 10))
		}
		return strings.Join(parts, ",")
	}

	// a realm with the pages and 47 decisions, whose whole page no longer renders: three pages, every decision
	l := &ledgerNode{n: 47, paged: true, pending: map[int64]bool{30: true}}
	ds, err := DecisionsAt(ctx, l, "r", 0)
	if err != nil || len(ds) != 47 || ds[0].ID != 1 || ds[46].ID != 47 || !ds[29].Queued() {
		t.Fatalf("47 decisions by page: %d (%v)", len(ds), err)
	}
	if strings.Join(l.renders, " ") != "decisions.json/1 decisions.json/21 decisions.json/41 decisions.json/48" {
		t.Fatalf("the pages read: %v", l.renders)
	}
	// from a number on: only the pages from it
	l.renders = nil
	if ds, err = DecisionsFrom(ctx, l, "r", 30, 0); err != nil || ids(ds) != ids(nilsFrom(30, 47)) || len(l.renders) != 2 {
		t.Fatalf("from 30: %s (%v), renders %v", ids(ds), err, l.renders)
	}
	// a page the realm cut short at 16 KB of text is not the end: every decision after it is read too
	cut := &ledgerNode{n: 47, paged: true, long: map[int64]bool{3: true, 30: true}}
	if ds, err = DecisionsAt(ctx, cut, "r", 0); err != nil || ids(ds) != ids(nilsFrom(1, 47)) {
		t.Fatalf("pages cut short: %s (%v), renders %v", ids(ds), err, cut.renders)
	}
	// a ledger of exactly one page reads a second, empty one; an empty ledger one empty page
	for _, n := range []int64{20, 0} {
		l := &ledgerNode{n: n, paged: true}
		if ds, err := DecisionsAt(ctx, l, "r", 0); err != nil || int64(len(ds)) != n {
			t.Fatalf("%d decisions: %d (%v)", n, len(ds), err)
		}
	}

	// a realm built before the pages: "unknown path" for them, the whole page read instead (from 30: its tail)
	old := &ledgerNode{n: 47, whole: true, pending: map[int64]bool{30: true}}
	if ds, err = DecisionsAt(ctx, old, "r", 0); err != nil || len(ds) != 47 {
		t.Fatalf("an older realm: %d (%v)", len(ds), err)
	}
	if strings.Join(old.renders, " ") != "decisions.json/1 decisions.json" {
		t.Fatalf("an older realm's reads: %v", old.renders)
	}
	if ds, err = DecisionsFrom(ctx, old, "r", 30, 0); err != nil || ids(ds) != ids(nilsFrom(30, 47)) {
		t.Fatalf("an older realm from 30: %s (%v)", ids(ds), err)
	}

	// a page whose numbers are not the ledger's: refused, never taken as decisions
	bad := &badPages{}
	if _, err = DecisionsAt(ctx, bad, "r", 0); err == nil || !strings.Contains(err.Error(), "holds decision #5 at place 0 (want #1)") {
		t.Fatalf("a page out of place: %v", err)
	}
}

func nilsFrom(from, to int64) []Decision {
	var out []Decision
	for id := from; id <= to; id++ {
		out = append(out, Decision{ID: id})
	}
	return out
}

// badPages renders a page that starts at the wrong number.
type badPages struct{ ledgerNode }

func (b *badPages) RenderAt(context.Context, string, string, int64) (string, error) {
	return `[{"id":5,"kind":"x","state":"applied"}]`, nil
}

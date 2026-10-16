package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The credits no dispatch index holds, found by id (the realms audit's E4-RL1, round 4, its Solana side). The
// forged-credit rule (AP1) judged every waiting claim the verifier credited whose id neither provider's index holds,
// and found them by walking each route's claims/<from> pages — but p/hlroute's ClaimsPage reaches <from> by walking
// every waiting claim before it (≈1.5M gas a claim on gnoland-1's gno v1.5.1), so with ≈1,600 claims waiting on one
// route claims/1600 is past the node's 3B query gas: the walk's read failed every poll, and the poll returned before
// the Solana checks, the stalls and the runways for as long as the backlog stood — a mass exit, or a stranger's 1,700
// fee-sized burns (the routes are immutable: the Go must change). The rule needs the claims no index holds, not all
// of them: every credit is a delivered id in hlmailbox's done tree (Process, and a credit by vote, mark it), whose
// count the mailbox renders. While that count is more than the index's credited burns and the credits already found,
// the tree is read by offset — vm/qeval in the mailbox's own package block (gno v1.5.1 resolves its unexported names
// there, as the record read of E3-RL1 does: doneExpr), IterateByOffset reaching a page in O(log n) — and each id the
// index does not hold is judged by its claim, read by id (claim/<id>: O(log n) on the route's claims tree). No read
// grows with the backlog past its page.

// doneEntry is one of hlmailbox's delivered message ids and the height it was delivered at.
type doneEntry struct {
	ID     string
	Height int64
}

// donePage is how many delivered ids one vm/qeval reads.
const donePage = 100

// doneExpr is the vm/qeval expression for hlmailbox's delivered ids from offset from (`done`: the message id in hex →
// the block height it was delivered at, mailbox.gno; `u32` its own formatter — a height is far below 2³²): up to
// count of them in id order, each "<id>:<height>,".
func doneExpr(from, count int) string {
	return fmt.Sprintf(`func() string { s := ""; done.IterateByOffset(%d, %d, func(k string, v any) bool { s += k + ":" + u32(uint32(v.(int64))) + ","; return false }); return s }()`, from, count)
}

var doneRE = regexp.MustCompile(`^\("([0-9a-f:,]*)" string\)$`)

// queryReader is a node's ABCI query (vm/qeval): the gno client's own.
type queryReader interface {
	Query(ctx context.Context, path string, data []byte, height int64) ([]byte, error)
}

// errNoQuery is a node reader without an ABCI query (the tests' plain fakes).
var errNoQuery = errors.New("this node reader answers no vm/qeval")

// donePageAt reads up to donePage delivered ids from offset from.
func donePageAt(ctx context.Context, q queryReader, mailbox string, from int) ([]doneEntry, error) {
	out, err := q.Query(ctx, "vm/qeval", []byte(mailbox+"."+doneExpr(from, donePage)), 0)
	if err != nil {
		return nil, fmt.Errorf("hlmailbox's delivered ids from %d: %w", from, err)
	}
	f := doneRE.FindStringSubmatch(strings.TrimSpace(string(out)))
	if f == nil {
		return nil, fmt.Errorf("hlmailbox's delivered ids from %d: %.200q", from, out)
	}
	var page []doneEntry
	for _, item := range strings.Split(f[1], ",") {
		if item == "" {
			continue
		}
		id, h, ok := strings.Cut(item, ":")
		height, err := strconv.ParseInt(h, 10, 64)
		if !ok || len(id) != 64 || err != nil || height <= 0 {
			return nil, fmt.Errorf("hlmailbox's delivered ids from %d: an entry %q", from, item)
		}
		page = append(page, doneEntry{ID: id, Height: height})
	}
	if len(page) > donePage {
		return nil, fmt.Errorf("hlmailbox's delivered ids from %d: %d answered, at most %d asked", from, len(page), donePage)
	}
	return page, nil
}

// markSeen is when a burn's delivered mark was last read unset: hlmailbox's delivered count at that reading's first
// read of the mailbox. A mark is set only with that count's rise — Process and a credit by vote each set one, in the
// transaction that raises the count, and none is ever unset — so while a reading's first read gives the same count, a
// mark read unset at it is unset still, and is not read again (E5-SF5's class: the burns nobody credits — the zero
// burns the signers indexed, the dust below a route's fee the relayers leave, a stranger's at a Solana fee each — were
// read again on every signer's round and every monitor's poll, for good).
type markSeen struct {
	delivered int
	read      bool
}

// stale says whether the mark is to be read at a reading whose first read of hlmailbox gave the delivered count d.
func (m markSeen) stale(d int) bool { return !m.read || m.delivered != d }

// markRefresh is how often every uncredited mark is read again whatever the count (beside every reading whose book of
// credits still holds a window the index could not place): the count and the marks are separate reads, and a node that
// answers them from two backends a block apart (a load-balanced own node) can give a mark read unset at a count that
// already holds its credit — kept, that one stale read would leave the credited burn in flight for good, BROKEN once its
// window's margin passed (the review of lane s6, F1); an unplaced credit is the sign of it, and the refresh bounds the
// rest.
const markRefresh = 10 * time.Minute

// deliveredMarks reads the delivered marks of ids at a reading whose first read of hlmailbox gave the delivered count d:
// the height each delivered one was delivered at (the others absent). One render each — or, for more ids than a page and
// than the done tree has pages (a restart's first reading of every burn it signed, a monitor's first poll over a pile of
// dust), the done tree whole by vm/qeval, a hundred ids a read, an id it does not hold being undelivered; a node that
// answers no vm/qeval is read id by id.
func deliveredMarks(ctx context.Context, g gnoReader, mailbox string, ids []string, d int, height int64) (map[string]int64, error) {
	out := map[string]int64{}
	if len(ids) > donePage && len(ids) > (d+donePage-1)/donePage {
		if entries, err := doneIDs(ctx, g, mailbox, d); err == nil {
			want := make(map[string]bool, len(ids))
			for _, id := range ids {
				want[id] = true
			}
			for _, e := range entries {
				if want[e.ID] {
					out[e.ID] = e.Height
				}
			}
			return out, nil
		}
	}
	for _, id := range ids {
		at, err := deliveredAt(ctx, g, mailbox, id, height)
		if err != nil {
			return nil, err
		}
		if at != 0 {
			out[id] = at
		}
	}
	return out, nil
}

// doneIDs is every id hlmailbox marked delivered, page by page until a short one (at most total+donePage of them: the
// count the mailbox rendered, and the credits landing while the pages are read).
func doneIDs(ctx context.Context, g gnoReader, mailbox string, total int) ([]doneEntry, error) {
	q, ok := g.(queryReader)
	if !ok {
		return nil, errNoQuery
	}
	var out []doneEntry
	for from := 0; ; from += donePage {
		page, err := donePageAt(ctx, q, mailbox, from)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < donePage {
			return out, nil
		}
		if from > total+donePage {
			return nil, fmt.Errorf("hlmailbox's delivered ids: past %d pages for a count of %d", from/donePage, total)
		}
	}
}

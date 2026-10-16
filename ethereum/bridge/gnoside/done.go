package gnoside

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// hlmailbox's delivered message ids read whole (the Solana machines' done.go, carried over by the integration lane I3
// for the dust the relayers leave, E5-GS5): every Process and every credit by vote marks an id delivered in the
// mailbox's `done` tree (message id in hex → the block height it was delivered at, mailbox.gno), whose size it renders
// as "delivered". A pile of uncredited burns — the dust below a route's fee nobody brings — has its marks read as that
// tree by offset, a hundred ids a vm/qeval in the mailbox's own package block (IterateByOffset reaching a page in
// O(log n); gno v1.5.1 resolves the package's unexported names there), instead of one delivered/<id> render each.

// Done is one of hlmailbox's delivered message ids and the height it was delivered at.
type Done struct {
	ID     string
	Height int64
}

// DonePage is how many delivered ids one vm/qeval reads.
const DonePage = 100

// doneExpr is the vm/qeval expression for hlmailbox's delivered ids from offset from (`u32` is the mailbox's own
// formatter — a height is far below 2³²): up to count of them in id order, each "<id>:<height>,".
func doneExpr(from, count int) string {
	return fmt.Sprintf(`func() string { s := ""; done.IterateByOffset(%d, %d, func(k string, v any) bool { s += k + ":" + u32(uint32(v.(int64))) + ","; return false }); return s }()`, from, count)
}

var doneRE = regexp.MustCompile(`^\("([0-9a-f:,]*)" string\)$`)

// Querier is a node's ABCI query (vm/qeval): the gno client's own.
type Querier interface {
	Query(ctx context.Context, path string, data []byte, height int64) ([]byte, error)
}

// ErrNoQuery is a node reader without an ABCI query (the tests' plain fakes): its marks are read id by id.
var ErrNoQuery = errors.New("this node reader answers no vm/qeval")

// DoneQuery is the vm/qeval data for hlmailbox's delivered ids from offset from (a test's fake node answers it).
func DoneQuery(mailbox string, from int) []byte {
	return []byte(mailbox + "." + doneExpr(from, DonePage))
}

// DonePageAt reads up to DonePage delivered ids from offset from, at a height (the cut's: a gno node answers its latest
// state at any height today, and the cut's other reads pass it too).
func DonePageAt(ctx context.Context, q Querier, mailbox string, from int, at int64) ([]Done, error) {
	out, err := q.Query(ctx, "vm/qeval", DoneQuery(mailbox, from), at)
	if err != nil {
		return nil, fmt.Errorf("hlmailbox's delivered ids from %d: %w", from, err)
	}
	f := doneRE.FindStringSubmatch(strings.TrimSpace(string(out)))
	if f == nil {
		return nil, fmt.Errorf("hlmailbox's delivered ids from %d: %.200q", from, out)
	}
	var page []Done
	for _, item := range strings.Split(f[1], ",") {
		if item == "" {
			continue
		}
		id, h, ok := strings.Cut(item, ":")
		height, err := strconv.ParseInt(h, 10, 64)
		if !ok || len(id) != 64 || err != nil || height <= 0 {
			return nil, fmt.Errorf("hlmailbox's delivered ids from %d: an entry %q", from, item)
		}
		page = append(page, Done{ID: id, Height: height})
	}
	if len(page) > DonePage {
		return nil, fmt.Errorf("hlmailbox's delivered ids from %d: %d answered, at most %d asked", from, len(page), DonePage)
	}
	return page, nil
}

// DoneIDs is every id hlmailbox marked delivered, page by page until a short one: at most two pages past total, the
// count the mailbox rendered (the credits landing while the pages are read), else an error.
func DoneIDs(ctx context.Context, g Reader, mailbox string, total int, at int64) ([]Done, error) {
	q, ok := g.(Querier)
	if !ok {
		return nil, ErrNoQuery
	}
	var out []Done
	for from := 0; ; from += DonePage {
		page, err := DonePageAt(ctx, q, mailbox, from, at)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < DonePage {
			return out, nil
		}
		if from > total+DonePage {
			return nil, fmt.Errorf("hlmailbox's delivered ids: past %d pages for a count of %d", from/DonePage, total)
		}
	}
}

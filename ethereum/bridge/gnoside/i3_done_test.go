package gnoside

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// i3Done answers hlmailbox's done tree by offset as gno's vm/qeval does, counting the reads (or answering the given text).
type i3Done struct {
	ids    []Done
	reads  int
	answer string
	err    error
}

func (d *i3Done) Query(_ context.Context, path string, data []byte, _ int64) ([]byte, error) {
	d.reads++
	if d.err != nil {
		return nil, d.err
	}
	if d.answer != "" {
		return []byte(d.answer), nil
	}
	var from int
	if path != "vm/qeval" || !strings.HasPrefix(string(data), "gno.land/r/x/hlmailbox.func()") {
		return nil, fmt.Errorf("asked %s %s", path, data)
	}
	if _, err := fmt.Sscanf(string(data[strings.Index(string(data), "IterateByOffset(")+len("IterateByOffset("):]), "%d", &from); err != nil {
		return nil, err
	}
	var b strings.Builder
	for i := from; i < len(d.ids) && i < from+DonePage; i++ {
		fmt.Fprintf(&b, "%s:%d,", d.ids[i].ID, d.ids[i].Height)
	}
	return []byte(`("` + b.String() + `" string)`), nil
}
func (d *i3Done) RenderAt(context.Context, string, string, int64) (string, error) { return "", nil }
func (d *i3Done) Status(context.Context) (gno.Head, error)                        { return gno.Head{}, nil }
func (d *i3Done) Block(context.Context, int64) (gno.Head, error)                  { return gno.Head{}, nil }

// DoneIDs reads hlmailbox's delivered ids a hundred a vm/qeval until a short page; a reader without a query, an
// answer it cannot read and an entry that is not an id and a height are errors (the caller then reads id by id).
func TestI3DoneIDs(t *testing.T) {
	ctx := context.Background()
	d := &i3Done{}
	for i := 0; i < 250; i++ {
		d.ids = append(d.ids, Done{ID: fmt.Sprintf("%064x", i+1), Height: int64(10 + i)})
	}
	got, err := DoneIDs(ctx, d, "gno.land/r/x/hlmailbox", 250, 0)
	if err != nil || len(got) != 250 || d.reads != 3 || got[249] != d.ids[249] {
		t.Fatalf("250 ids: %d read in %d queries (%v)", len(got), d.reads, err)
	}
	d.reads, d.ids = 0, d.ids[:200]
	if got, err := DoneIDs(ctx, d, "gno.land/r/x/hlmailbox", 200, 0); err != nil || len(got) != 200 || d.reads != 3 {
		t.Fatalf("200 ids (two full pages, then an empty one): %d in %d queries (%v)", len(got), d.reads, err)
	}
	type plain struct{ Reader }
	if _, err := DoneIDs(ctx, plain{d}, "gno.land/r/x/hlmailbox", 1, 0); !errors.Is(err, ErrNoQuery) {
		t.Fatalf("a reader without a query: %v", err)
	}
	for _, bad := range []string{`("ab:12," string)`, `("` + strings.Repeat("a", 64) + `:0," string)`, `(12 int)`, `("` + strings.Repeat("a", 64) + `:x," string)`} {
		if _, err := DoneIDs(ctx, &i3Done{answer: bad}, "gno.land/r/x/hlmailbox", 1, 0); err == nil {
			t.Fatalf("an answer %q read as ids", bad)
		}
	}
	if _, err := DoneIDs(ctx, &i3Done{err: errors.New("query failed")}, "gno.land/r/x/hlmailbox", 1, 0); err == nil {
		t.Fatal("a failed query read as no ids")
	}
	// a page of more than a hundred is refused
	var many strings.Builder
	for i := 0; i < DonePage+1; i++ {
		fmt.Fprintf(&many, "%064x:1,", i+1)
	}
	if _, err := DoneIDs(ctx, &i3Done{answer: `("` + many.String() + `" string)`}, "gno.land/r/x/hlmailbox", 1, 0); err == nil {
		t.Fatal("a page of 101 ids read")
	}
	// a tree that keeps answering full pages past its count is refused (two pages past it at most)
	full := &i3Done{}
	for i := 0; i < 1000; i++ {
		full.ids = append(full.ids, Done{ID: fmt.Sprintf("%064x", i+1), Height: 1})
	}
	if _, err := DoneIDs(ctx, full, "gno.land/r/x/hlmailbox", 100, 0); err == nil || full.reads > 4 {
		t.Fatalf("a tree past its count: %v after %d reads", err, full.reads)
	}
}

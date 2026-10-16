package gnoside

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// WaitingAt (the integration lane I2, E4-RL1): a page of a route's waiting index is held to its contract — at most 100
// entries, each at or past <from>, in increasing place — and a route built before the index (an unknown path) says so.
func TestI2WaitingAt(t *testing.T) {
	route := "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlgnot"
	entry := func(pos int, state string) string {
		return fmt.Sprintf(`{"pos":%d,"id":"%064x","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":5,"paid":0,"state":"%s","by":"verifier","at":1,"height":59,"seq":1}`, pos, pos, state)
	}
	page := func(ps ...int) string {
		var es []string
		for _, p := range ps {
			es = append(es, entry(p, "open"))
		}
		return "[" + strings.Join(es, ",") + "]"
	}
	var hundredOne []int
	for i := 1; i <= 101; i++ {
		hundredOne = append(hundredOne, i)
	}
	g := &fakeNode{renders: map[string]string{
		route + ":waiting/1": page(1, 3, 9) + "",
		route + ":waiting/5": page(5, 4),
		route + ":waiting/7": page(6, 8),
		route + ":waiting/2": page(hundredOne...),
		route + ":waiting/9": `[{"pos":9,"id":"` + strings.Repeat("ab", 32) + `","state":"gone"}]`,
		route + ":waiting/4": "unknown path; see the route's page",
		route + ":waiting/6": "not json",
	}}
	ctx := context.Background()
	es, unknown, err := WaitingAt(ctx, g, route, 0, 0) // 0 reads as 1
	if err != nil || unknown || len(es) != 3 || es[0].Pos != 1 || es[2].Pos != 9 || es[1].State != "open" || es[1].To == "" {
		t.Fatalf("a page of places 1, 3, 9: %+v %v %v", es, unknown, err)
	}
	if es, _, err := WaitingAt(ctx, g, route, 9, 0); err != nil || len(es) != 1 || es[0].State != "gone" || es[0].ID != strings.Repeat("ab", 32) {
		t.Fatalf("a gone entry: %+v %v", es, err)
	}
	for from, want := range map[int64]string{5: "after place 5", 7: "after place 6", 2: "101 entries", 6: "not json"} {
		if _, _, err := WaitingAt(ctx, g, route, from, 0); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("waiting/%d: %v, want an error naming %q", from, err, want)
		}
	}
	if es, unknown, err := WaitingAt(ctx, g, route, 4, 0); err != nil || !unknown || es != nil {
		t.Fatalf("an unknown path: %+v %v %v", es, unknown, err)
	}
	if _, _, err := WaitingAt(ctx, g, route, 3, 0); err == nil {
		t.Fatal("a render that failed was no error")
	}
}

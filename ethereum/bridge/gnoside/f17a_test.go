package gnoside

import (
	"fmt"
	"testing"
)

// Lane F17a (E5-GS4): the compaction drops one event of a message from the index — its height, its leaf's size and
// count — and nothing of the others.
func TestF17aIndexDropsOneEvent(t *testing.T) {
	var x EventIndex
	for _, h := range []int64{105, 106, 107} {
		x.Note(0, h)
		x.Note(1, h)
		x.init()
		for _, n := range []uint32{0, 1} {
			if x.Leaves[n] == nil {
				x.Leaves[n], x.Counts[n] = map[int64]int{}, map[int64]int{}
			}
			x.Leaves[n][h], x.Counts[n][h] = 451, 1
		}
	}
	x.Drop(0, 106)
	if fmt.Sprint(x.Heights[0]) != "[105 107]" || fmt.Sprint(x.Heights[1]) != "[105 106 107]" {
		t.Fatalf("the heights after dropping 0@106: %v", x.Heights)
	}
	if _, ok := x.LeafOf(0, 106); ok {
		t.Fatal("the dropped event's leaf stays")
	}
	if _, ok := x.CountOf(1, 106); !ok {
		t.Fatal("another message's event at the height went with it")
	}
	x.Drop(0, 105)
	x.Drop(0, 107)
	if _, ok := x.Heights[0]; ok || x.Leaves[0] != nil {
		t.Fatalf("a message with no event left keeps its entries: %v %v", x.Heights, x.Leaves)
	}
}

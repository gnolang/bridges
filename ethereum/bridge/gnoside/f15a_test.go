package gnoside

// Audit round 4's go-spend fold (lane F15a): E4-GS3.

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

const f15aMailbox = "gno.land/r/test/eth/hlmailbox"

// reemitLeaf is one transaction re-emitting ids, beside junk bytes of another realm's events (a failed one when err).
func reemitLeaf(ids [][32]byte, junk int, failed bool) []byte {
	var anys [][]byte
	for k, id := range ids {
		d := gnolight.Dispatch{ID: id, Nonce: uint32(k), Destination: 31337}
		anys = append(anys, gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(d.ToEvent(f15aMailbox))))
	}
	if junk > 0 {
		pad := &gnolight.Event{Type: "Junk", PkgPath: "gno.land/r/x/y", Attributes: []gnolight.Attribute{{Key: "x", Value: strings.Repeat("z", junk)}}}
		anys = append(anys, gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(pad)))
	}
	var errAny []byte
	if failed {
		errAny = gnolight.Any("/std.InternalError", nil)
	}
	return gnolight.EncodeResult(errAny, []byte("\n\n"), anys)
}

// E4-GS3: the scan's leaf choice is one pass over the block's leaves: a stranger's block of 1,000 small Reemit
// transactions, 5,000 Dispatch events, each leaf smaller than the last (each a new best for every id), is sized in well
// under a second — the per-dispatch pass took 2 m 16 s of both machines' ticks — and the choice is ProofLeaf's for
// every id (the smallest leaf carrying it, the lowest index among equals, a failed transaction's leaf never).
func TestScanLeafChoiceIsOnePass(t *testing.T) {
	const n = 1000
	var leaves [][]byte
	var ds []gnolight.Dispatch
	for i := 0; i < n; i++ {
		var ids [][32]byte
		for k := 0; k < 5; k++ {
			var d gnolight.Dispatch
			d.ID[0], d.ID[1], d.ID[2] = byte(i), byte(i>>8), byte(k)
			ds = append(ds, d)
			ids = append(ids, d.ID)
		}
		leaves = append(leaves, reemitLeaf(ids, n-i, false))
	}
	start := time.Now()
	sizes, counts := leafSizes(leaves, f15aMailbox, ds)
	took := time.Since(start)
	if took > 5*time.Second || len(sizes) != len(ds) {
		t.Fatalf("%d transactions × 5 re-emits (%d Dispatch events): leafSizes took %s (%d sized)", n, len(ds), took.Round(time.Millisecond), len(sizes))
	}
	for _, d := range ds[:50] {
		i, c := ProofLeaf(leaves, f15aMailbox, d.ID)
		if sizes[d.ID] != len(leaves[i]) || counts[d.ID] != c {
			t.Fatalf("id %x: sized %d/%d, ProofLeaf's leaf %d of %d bytes and %d", d.ID[:3], sizes[d.ID], counts[d.ID], i, len(leaves[i]), c)
		}
	}
	// random blocks: ties, failed leaves, ids carried by several leaves of every size
	rng := rand.New(rand.NewSource(15))
	for round := 0; round < 200; round++ {
		pool := make([][32]byte, 1+rng.Intn(6))
		for k := range pool {
			pool[k][0], pool[k][1] = byte(round), byte(k)
		}
		var leaves [][]byte
		for j := 0; j < 1+rng.Intn(8); j++ {
			var ids [][32]byte
			for k := 0; k < 1+rng.Intn(4); k++ {
				ids = append(ids, pool[rng.Intn(len(pool))])
			}
			leaves = append(leaves, reemitLeaf(ids, rng.Intn(3)*50, rng.Intn(5) == 0))
		}
		best := ProofLeaves(leaves, f15aMailbox)
		for _, id := range pool {
			i, c := ProofLeaf(leaves, f15aMailbox, id)
			b, ok := best[id]
			if (i >= 0) != ok || (ok && (b.Index != i || b.Dispatches != c)) {
				t.Fatalf("round %d, id %x: one pass chose %+v %v, ProofLeaf %d (%d)", round, id[:2], b, ok, i, c)
			}
		}
	}
}

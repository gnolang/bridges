package agent

// Fold F18a (round 6): the Go trust path's E6-GT3 — the dust the relayers leave folded out of the per-round state.
// Fails on the parent (e3d0053f); the audit's measurement is audit-r6/go-trust-pocs/zz_audit6_pile_test.go.

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// f18aPile is how many dust burns the state-size test piles (F18A_PILE overrides it: the audit's 50,000 take minutes,
// most of them signing a signature file each).
func f18aPile() int {
	if n, err := strconv.Atoi(os.Getenv("F18A_PILE")); err == nil && n > 0 {
		return n
	}
	return 1000
}

// A pile of dust burns (1 unit each, below the route's fee: the relayers leave them), signed, then dust for an hour: the
// signer's state written every round no longer holds them — each token's sum in flight is what the identity reads —
// and a quiet round stays a quiet round (the parent: ≈700 bytes a burn rewritten every round, 35 MB and 610 ms a round
// at 50,000).
func TestF18aDustFoldedOutOfTheState(t *testing.T) {
	n := f18aPile()
	r := newSignerRig(t)
	r.chain.setSupply(m3bGNOT, 1, 10_000_000)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(10_000_000, 0, 0, 0))
	r.round()
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.mine(400, 400)
	for i := 0; i < n; i++ {
		r.chain.burn(m3bGNOT, gnot, r.alice, 1, uint64(101+i*290/n))
	}
	r.step(time.Second)
	start := time.Now()
	r.round() // scans, reads every new mark, signs every burn
	first := time.Since(start)
	r.step(61 * time.Minute) // dust for an hour
	r.round()
	var quiet []time.Duration
	for i := 0; i < 3; i++ {
		r.step(time.Second)
		s := time.Now()
		r.round()
		quiet = append(quiet, time.Since(s))
	}
	fi, err := os.Stat(filepath.Join(r.s.Store, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	st := r.status()
	id := st["identity"].(map[string]any)["GNOT"].(map[string]any)
	t.Logf("%d dust burns: state.json %d bytes; first round %s, quiet rounds %v; signing %v, waiting %v, uncredited %v", n, fi.Size(), first.Round(time.Millisecond), quiet, st["signing"], st["waiting"], id["uncredited"])
	if st["signing"] != true || id["uncredited"].(int64) != int64(n) || st["waiting"].(int) != n {
		t.Fatalf("the folded dust is not counted in flight, or the signer stopped: %v", st)
	}
	if fi.Size() > 64<<10 {
		t.Fatalf("the state written every round holds the pile: %d bytes for %d dust burns", fi.Size(), n)
	}
}

package agent

// Fold F18a: the folded dust's mechanism (dust.go, E6-GT3) — a folded burn someone delivers, a fee lowered past one, a
// crash between the file and the state, the file rewritten whole.

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// f18aFoldedRig is a signer whose index folded dust: 20 burns of 1 unit, signed, dust for an hour.
func f18aFoldedRig(t *testing.T) (*signerRig, []string) {
	r := newSignerRig(t)
	r.round()
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.mine(130, 130)
	var ids []string
	for i := 0; i < 20; i++ {
		id := r.chain.burn(m3bGNOT, gnot, r.alice, 1, 120)
		ids = append(ids, hex.EncodeToString(id[:]))
	}
	r.step(time.Second)
	r.round()
	r.step(foldAfter + time.Minute)
	r.round()
	if len(r.s.state.Index.folded()) != 20 || len(r.s.state.Index.Burns) != 0 {
		t.Fatalf("setup: %d folded, %d burns", len(r.s.state.Index.folded()), len(r.s.state.Index.Burns))
	}
	return r, ids
}

// A folded burn delivered by a stranger (its Process paid: the route's sweep queue owes its amount, its mark set) reads,
// until its mark is read, as its amount in owed and in the folded sum — BROKEN by it, with a delivery unplaced. Its
// folded marks are read before the token is judged: it is unfolded, credited, and the round holds; the signer signs on.
// A restart over the file keeps it unfolded.
func TestF18aFoldedDustDeliveredIsUnfolded(t *testing.T) {
	r, ids := f18aFoldedRig(t)
	n := &f18aNode{m3bGnoNode: r.gno, lastRoute: m3bNS + "hlbubble"}
	r.s.gno = n
	h := n.deliver(ids[3])
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 1, 0, 0)) // swept: owed by its amount
	r.step(time.Second)
	r.round()
	st := r.status()
	t.Logf("delivered at %d: signing %v, halted %q, folded %d, burns %d", h, st["signing"], st["halted"], len(r.s.state.Index.folded()), len(r.s.state.Index.Burns))
	if st["signing"] != true || st["halted"] != "" || len(r.s.state.Index.folded()) != 19 {
		t.Fatalf("a delivered folded burn halted the signer, or stayed folded: %v", st)
	}
	// unfolded, credited and signed: pruned as any credited burn — counted among the deliveries placed
	if r.s.state.PrunedCredited != 1 {
		t.Fatalf("the unfolded burn was not pruned as credited: %d", r.s.state.PrunedCredited)
	}
	// a restart over the same store reads the file and the state alike
	s2 := &Signer{Cfg: r.s.Cfg, Key: r.s.Key, Store: r.s.Store, Every: time.Second, Alarm: &Alarmer{Source: "test2"}, pair: r.s.pair, gno: n, now: r.clock}
	if err := s2.load(); err != nil {
		t.Fatal(err)
	}
	if len(s2.state.Index.folded()) != 19 || s2.state.Index.has(ids[3]) {
		t.Fatalf("after a restart: %d folded, the delivered one held %v", len(s2.state.Index.folded()), s2.state.Index.has(ids[3]))
	}
	r.s = s2
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] != true {
		t.Fatalf("after the restart: %v", st)
	}
}

// The monitor's half: a folded burn delivered is no break (no flag), its id is the index's to the forged-credit rule,
// and its folded dust is in the status' dust count.
func TestF18aMonitorFoldedDustDelivered(t *testing.T) {
	r, n := f18aMonRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	var ids []string
	for i := 0; i < 10; i++ {
		id := r.chain.burn(m3bGNOT, gnot, r.alice, 1, 95)
		ids = append(ids, hex.EncodeToString(id[:]))
	}
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.step(foldAfter + time.Minute)
	f18aKeepPace(r.chain, r.now)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if len(r.m.state.Index.folded()) != 10 {
		t.Fatalf("setup: %d folded", len(r.m.state.Index.folded()))
	}
	if d := r.m.Status()["dust"].(map[string]any)["GNOT"]; d != float64(10) {
		t.Fatalf("the folded dust is not in the status: %v", r.m.Status()["dust"])
	}
	n.deliver(ids[0])
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 1, 0, 0))
	r.gno.set(m3bNS+"hlgnot", "claims/0", `[{"id":"`+ids[0]+`","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":1,"state":"open","height":70}]`)
	r.step(10 * time.Second)
	f18aKeepPace(r.chain, r.now)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("flags %v; folded %d; identity %v", r.gnoFlags(), len(r.m.state.Index.folded()), r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)["verdict"])
	if len(r.gnoFlags()) > 0 || r.snd.count() > 0 || len(r.m.state.Index.folded()) != 9 || r.alarmed("credit:unindexed:"+ids[0]+":flag") {
		t.Fatalf("a delivered folded burn was flagged, or stayed folded: %v %v", r.gnoFlags(), r.m.Alarm.StandingKeys())
	}
}

// A fee lowered past a folded burn's amount makes it a delivery like any: unfolded, waited for (the stalls see it).
func TestF18aFoldedDustUnfoldedByALowerFee(t *testing.T) {
	r, ids := f18aFoldedRig(t)
	r.gno.set(m3bNS+"hlgnot", "json", `{"live":true,"lock":1000,"owed":0,"voided":0,"lost":0,"fee":1}`)
	r.step(time.Second)
	r.round()
	if len(r.s.state.Index.folded()) != 0 || r.s.state.Index.Burns[ids[0]] == nil {
		t.Fatalf("a fee of 1 left dust of 1 folded: %d folded", len(r.s.state.Index.folded()))
	}
}

// The file and the state agree by the count the state keeps: lines written for a round whose state was never saved (a
// crash between the two) are dropped when the file is read; and the file, rewritten whole once its unfolds outnumber
// its folds, holds exactly the live folds.
func TestF18aDustFileAgreesWithTheState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dust.jsonl")
	var x burnIndex
	if err := x.attachDust(path); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 600; i++ {
		id := strconv.Itoa(1000 + i)
		x.Burns[id] = &burnRec{Symbol: "GNOT", Nonce: uint32(i), Block: 90, Amount: 1, Seen: 1}
		x.fold(id)
	}
	if err := saveWithDust(&x, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	saved := x.DustLines // the state as saved: 600 folds
	// a round that unfolds 500 and writes its lines, then crashes before its state is saved
	for i := 0; i < 500; i++ {
		x.unfold(strconv.Itoa(1000+i), 70)
	}
	if err := x.flushDust(); err != nil {
		t.Fatal(err)
	}
	// the restart reads a copy of the file as the crash left it
	crashed := filepath.Join(dir, "crashed.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(crashed, b, 0o600); err != nil {
		t.Fatal(err)
	}
	var y burnIndex
	y.DustLines = saved
	if err := y.attachDust(crashed); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(crashed); fi.Size() >= int64(len(b)) {
		t.Fatalf("the crashed round's lines were left in the file: %d bytes of %d", fi.Size(), len(b))
	}
	if len(y.folded()) != 600 {
		t.Fatalf("the crashed round's lines were taken: %d folded, want the saved 600", len(y.folded()))
	}
	// the round saved this time: the file is rewritten whole (500 unfolds past the 100 live folds)
	if err := saveWithDust(&x, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if lines := strings.Count(string(b), "\n"); lines != 100 || x.DustLines != 100 {
		t.Fatalf("the file holds %d lines (state %d), want the 100 live folds", lines, x.DustLines)
	}
	var z burnIndex
	z.DustLines = x.DustLines
	if err := z.attachDust(path); err != nil {
		t.Fatal(err)
	}
	if len(z.folded()) != 100 || z.has("1000") || !z.has("1599") {
		t.Fatalf("the rewritten file: %d folded", len(z.folded()))
	}
}

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// encRig is a monitor over an own node whose blocks carry a send's transaction each, and, where asked, a stranger's
// transaction whose event attribute is not UTF-8: the chain hashes its bytes, the node's JSON serves U+FFFD.
type encRig struct {
	*monRig
	f    *fakeGno
	node *encNode
}

func newEncRig(t *testing.T, lossyAt ...int64) *encRig {
	r := newMonRig(t)
	f := newFakeGno(m3bMailPath, 1000)
	ev := &gnolight.Event{Type: "Hello", PkgPath: "gno.land/r/stranger/x", Attributes: []gnolight.Attribute{{Key: "k", Value: "\xff\xfe"}}}
	trueLeaf := gnolight.EncodeResult(nil, []byte("ok"), [][]byte{gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(ev))})
	lossy := map[int64]bool{}
	for _, h := range lossyAt {
		lossy[h] = true
	}
	f.mu.Lock()
	for h := int64(1); h <= 100; h++ {
		f.results[h] = append(f.results[h], dispatchTx(m3bMailPath, hl.Message{Version: 3, Nonce: uint32(h), Origin: m3bGnoDom, Sender: gnoside.RealmKey(m3bNS + "hlgnot"), Destination: m3bEthDom, Recipient: ethx.AddressTo32(m3bGNOT), Body: hl.TokenMessage([32]byte{1}, 5, nil)}.Bytes()))
		if lossy[h] {
			f.results[h] = append(f.results[h], &gnolight.TxResult{Leaf: trueLeaf, Events: []*gnolight.Event{ev}}) // what the chain hashed
		}
	}
	f.mu.Unlock()
	// the stranger's result as the node's JSON serves it, read back as the monitor reads it
	evJSON, _ := json.Marshal(map[string]any{"@type": gnolight.EventTypeURL, "type": ev.Type, "attrs": []map[string]string{{"key": "k", "value": "\xff\xfe"}}, "pkg_path": ev.PkgPath})
	served, err := gnolight.BlockResultsFromJSON([]byte(`{"results":{"deliver_tx":[{"ResponseBase":{"Error":null,"Data":"b2s=","Events":[` + string(evJSON) + `]}}]}}`))
	if err != nil || len(served) != 1 || served[0].Err != nil {
		t.Fatalf("the served result: %v %v", served, err)
	}
	node := &encNode{m3bGnoNode: r.gno, f: f, version: "v1.5.1"}
	node.results = func(rs []*gnolight.TxResult) []*gnolight.TxResult {
		out := append([]*gnolight.TxResult{}, rs...)
		for i, x := range out {
			if len(x.Events) == 1 && x.Events[0].Type == "Hello" {
				out[i] = served[0]
			}
		}
		return out
	}
	r.m.own = node
	return &encRig{monRig: r, f: f, node: node}
}

func (r *encRig) probe(t *testing.T) {
	t.Helper()
	r.step(61 * time.Second) // past the minute between rebuilds; the head moves one block
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
}

// E4-AS1 = E4-CF1(b) = E4-GT4: a stranger's non-UTF-8 event string in the block below the head is no gno upgrade — no
// gno:encoding; results:lossy says the block is unprovable from the JSON, and the probe judges the block below it.
func TestF15bLossyStringIsNoUpgrade(t *testing.T) {
	r := newEncRig(t, 60) // the head reaches 61 at the first probe: block 60 is the one below it
	r.probe(t)
	t.Logf("standing: gno:encoding %q; results:lossy %q", r.m.Alarm.Standing()["gno:encoding"], r.m.Alarm.Standing()["results:lossy"])
	if r.alarmed("gno:encoding") {
		t.Fatalf("a stranger's string raised gno:encoding: %s", r.m.Alarm.Standing()["gno:encoding"])
	}
	if !strings.Contains(r.m.Alarm.Standing()["results:lossy"], "block 60") {
		t.Fatalf("the lossy block is not said: %v", r.m.Alarm.Standing())
	}
	// a standing gno:encoding (a header that hashed otherwise) is cleared by a probe that judged a clean block below
	r.node.mu.Lock()
	r.node.header = func(b *gnolight.Block) *gnolight.Block { c := *b; c.Hash[0] ^= 0xff; return &c }
	r.node.mu.Unlock()
	r.probe(t)
	if !r.alarmed("gno:encoding") {
		t.Fatal("a header that hashes otherwise is not alarmed")
	}
	r.node.mu.Lock()
	r.node.header = nil
	r.node.mu.Unlock()
	r.f.mu.Lock()
	r.f.results[62] = append(r.f.results[62], r.f.results[60][1]) // the stranger again, below the next head
	r.f.mu.Unlock()
	r.probe(t)
	if r.alarmed("gno:encoding") {
		t.Fatalf("not cleared by a probe that judged the clean block below the lossy one: %v", r.m.Alarm.Standing())
	}
}

// A real change of the results' encoding in a clean block below a lossy one is still found.
func TestF15bResultsChangeBelowALossyBlockIsFound(t *testing.T) {
	r := newEncRig(t, 60)
	r.node.mu.Lock()
	inner := r.node.results
	r.node.results = func(rs []*gnolight.TxResult) []*gnolight.TxResult {
		out := inner(rs)
		for i, x := range out {
			if x.Err == nil && !strings.Contains(string(x.Leaf), "�") {
				c := *x
				c.Leaf = append(append([]byte{}, x.Leaf...), 0x00) // the clean leaves rebuild otherwise
				out[i] = &c
			}
		}
		return out
	}
	r.node.mu.Unlock()
	r.probe(t)
	text := r.m.Alarm.Standing()["gno:encoding"]
	t.Logf("gno:encoding %q", text)
	if !strings.Contains(text, "block 59") {
		t.Fatalf("a changed results encoding below a lossy block is not found: %v", r.m.Alarm.Standing())
	}
}

// Every block the probe reads carries the stranger's string: the results are not judged — no gno:encoding raised, a
// standing one not cleared — and hlwatch says the probe did not run (never green over it).
func TestF15bLossyEverywhereIsNotJudged(t *testing.T) {
	var all []int64
	for h := int64(1); h <= 100; h++ {
		all = append(all, h)
	}
	r := newEncRig(t, all...)
	r.m.ReadOnly = true
	w := &Watch{m: r.m}
	r.step(61 * time.Second)
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ok, why := v.Green()
	t.Logf("green %v %v", ok, why)
	if r.alarmed("gno:encoding") || ok || !strings.Contains(strings.Join(why, ";"), "the encodings probe") {
		t.Fatalf("every block lossy: gno:encoding %v, green %v %v", r.alarmed("gno:encoding"), ok, why)
	}
}

// E4-CF1(a) (probes-conformance/zz_audit4_cf_encoding_test.go): a failed transaction whose error type is outside the
// client's closed set — a gno upgrade added it; no stranger can make one — is a finding, and never clears a standing
// gno:encoding.
func TestF15bUnknownErrorTypeIsAFinding(t *testing.T) {
	r := newEncRig(t)
	r.probe(t)
	if r.alarmed("gno:encoding") {
		t.Fatalf("honest: %v", r.m.Alarm.Standing())
	}
	r.node.mu.Lock()
	r.node.results = func(rs []*gnolight.TxResult) []*gnolight.TxResult {
		return []*gnolight.TxResult{{Err: &gnolight.SchemaError{Kind: "error", TypeURL: "/vm.SomeNewError"}}}
	}
	r.node.mu.Unlock()
	r.probe(t)
	text := r.m.Alarm.Standing()["gno:encoding"]
	t.Logf("gno:encoding %q", text)
	if !strings.Contains(text, "/vm.SomeNewError") {
		t.Fatalf("a type outside the closed set is not a finding: %v", r.m.Alarm.Standing())
	}
}

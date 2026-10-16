package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// Leaves that do not hash to the header's LastResultsHash — a string the node's JSON cannot carry (bytes that are
// not UTF-8 read as U+FFFD: anyone's event attribute or panic message can hold them), a changed type, a node
// serving other results — make the block unbuildable: the delivery waits, the Reemit rule asks for a fresh block,
// and the mismatch is alarmed. Before, a failed transaction's StringError of such bytes made the block unbuildable
// (every failed transaction did); now that failed transactions rebuild, its leaf comes out wrong and only the root
// shows it — and a bare error there parked the message for good instead of re-emitting it.
func TestAResultsMismatchAsksAReemit(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	_, d, id := dispatchOf(6, testMailbox)
	leaves := [][]byte{
		gnolight.EncodeResult(nil, nil, [][]byte{gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(d.ToEvent(testMailbox)))}),
		gnolight.EncodeResult(gnolight.Any("/abci.StringError", []byte("\x0a\x03\xef\xbf\xbd")), nil, nil),
	}
	root, _ := gnolight.SimpleHash(leaves)
	if err := r.resultsMatch(70, id, leaves, 71, root); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.leafs[leafKey(70, id)]; ok || len(r.Alarm.Standing()) != 0 {
		t.Fatal("a matching root marked the block")
	}
	var chains [32]byte
	chains[0] = 0xc4 // the chain hashed the panic's own byte, 0xff, where the JSON said U+FFFD
	mismatch := r.resultsMatch(70, id, leaves, 71, chains)
	if mismatch == nil || !errors.Is(mismatch, errNotYet) {
		t.Fatalf("%v", mismatch)
	}
	if _, ok := r.Alarm.Standing()["results:mismatch"]; !ok {
		t.Fatalf("no alarm: %v", r.Alarm.StandingKeys())
	}
	n, buildable, err := (&solClientView{r}).LeafBytes(ctx, 70, id)
	if err != nil || buildable || n != 0 {
		t.Fatalf("LeafBytes %d %v %v", n, buildable, err)
	}
	if need, _ := needsReemit(reemitInput{DispatchHeight: 70, CurrentSetFrom: 10, LeafUnbuildable: !buildable}); !need {
		t.Fatal("no Reemit asked")
	}
	// the delivery's own failure is a wait, never a step toward parking
	if err := r.deliverFailed(todo{n: 6}, "deliver:6", "6", mismatch); err != nil || r.state.Attempts["deliver:6"] != nil {
		t.Fatalf("%v %+v", err, r.state.Attempts["deliver:6"])
	}
	// the mark stands: the next tick's leafAt reads no block (whose JSON would rebuild the same wrong leaves)
	r.light = &resultsLight{fakeLight: &fakeLight{fakeChain: f}, results: map[int64][]*gnolight.TxResult{
		70: blockResults(t, txJSON("null", dispatchJSON(d, testMailbox))),
	}}
	li, got, err := r.leafAt(ctx, 70, id)
	if err != nil || li.buildable || got != nil {
		t.Fatalf("%+v %d leaves %v", li, len(got), err)
	}
}

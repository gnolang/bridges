package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// resultsLight serves block results from JSON, as a node's block_results.
type resultsLight struct {
	*fakeLight
	results map[int64][]*gnolight.TxResult
}

func (l *resultsLight) BlockResults(_ context.Context, h int64) ([]*gnolight.TxResult, error) {
	return l.results[h], nil
}

// txJSON is one DeliverTx result as block_results serves it.
func txJSON(errJSON string, events ...string) string {
	evs := "null"
	if len(events) > 0 {
		evs = "[" + strings.Join(events, ",") + "]"
	}
	return `{"ResponseBase":{"Error":` + errJSON + `,"Data":null,"Events":` + evs + `,"Log":"","Info":""},"GasWanted":"100000","GasUsed":"62488"}`
}

func dispatchJSON(d gnolight.Dispatch, path string) string {
	e := d.ToEvent(path)
	var attrs []string
	for _, a := range e.Attributes {
		attrs = append(attrs, fmt.Sprintf(`{"key":%q,"value":%q}`, a.Key, a.Value))
	}
	return fmt.Sprintf(`{"@type":"/tm.Event","type":"Dispatch","attrs":[%s],"pkg_path":%q}`, strings.Join(attrs, ","), path)
}

func blockResults(t *testing.T, txs ...string) []*gnolight.TxResult {
	t.Helper()
	rs, err := gnolight.BlockResultsFromJSON([]byte(`{"height":"7","results":{"deliver_tx":[` + strings.Join(txs, ",") + `]}}`))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// A message dispatched beside failed transactions is proven from its own block (E3-RT2, E3-GS1): a failed
// transaction's leaf is rebuilt with its error, so the block stays buildable, the leaf is the dispatch's and the
// Reemit rule asks nothing. Before, any failed transaction in the block — a MsgSend above the sender's balance, a
// thousandth of a GNOT — made it unbuildable, and the relayer re-emitted into the next block, where the same costs
// the attacker as little.
func TestLeafAtProvesBesideFailedTransactions(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	_, d, id := dispatchOf(4, testMailbox)
	rl := &resultsLight{fakeLight: &fakeLight{fakeChain: f}, results: map[int64][]*gnolight.TxResult{
		50: blockResults(t,
			txJSON(`{"@type":"/std.VestingLockedCoinsError"}`),
			txJSON("null", dispatchJSON(d, testMailbox)),
			txJSON(`{"@type":"/abci.StringError","value":"send at least 661438ugnot for the bid, got 612373ugnot"}`),
			txJSON(`{"@type":"/vm.InvalidPkgPathError"}`),
			txJSON(`{"@type":"/std.OutOfGasError"}`)),
	}}
	r.light = rl
	li, leaves, err := r.leafAt(ctx, 50, id)
	if err != nil || !li.buildable || li.index != 1 || len(leaves) != 5 {
		t.Fatalf("%+v %d leaves %v", li, len(leaves), err)
	}
	if n, ok, err := (&solClientView{r}).LeafBytes(ctx, 50, id); err != nil || !ok || n != li.bytes {
		t.Fatalf("LeafBytes %d %v %v", n, ok, err)
	}
	if need, why := needsReemit(reemitInput{DispatchHeight: 50, CurrentSetFrom: 10, LeafBytes: li.bytes, LeafUnbuildable: !li.buildable}); need {
		t.Fatalf("a Reemit asked: %s", why)
	}
	if len(r.Alarm.Standing()) != 0 {
		t.Fatalf("alarms %v", r.Alarm.StandingKeys())
	}
}

// A result of a type gnolight has no schema for — a chain upgrade's new error or event type — leaves the block
// unbuildable, as before, and is alarmed once by name rather than only re-emitting every message beside one.
func TestLeafAtAlarmsATypeWithNoSchema(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	_, d, id := dispatchOf(5, testMailbox)
	rl := &resultsLight{fakeLight: &fakeLight{fakeChain: f}, results: map[int64][]*gnolight.TxResult{
		60: blockResults(t, txJSON("null", dispatchJSON(d, testMailbox)), txJSON(`{"@type":"/vm.SomeNewError"}`)),
		61: blockResults(t, txJSON("null", dispatchJSON(d, testMailbox)), txJSON("null", `{"@type":"/vm.NewEvent","x":"y"}`)),
	}}
	r.light = rl
	for _, c := range []struct {
		height int64
		key    string
	}{{60, "results:schema:/vm.SomeNewError"}, {61, "results:schema:/vm.NewEvent"}} {
		li, _, err := r.leafAt(ctx, c.height, id)
		if err != nil || li.buildable {
			t.Fatalf("height %d: %+v %v", c.height, li, err)
		}
		text, ok := r.Alarm.Standing()[c.key]
		if !ok || !strings.Contains(text, fmt.Sprintf("block %d", c.height)) {
			t.Fatalf("height %d: no alarm %s: %v", c.height, c.key, r.Alarm.Standing())
		}
		if need, _ := needsReemit(reemitInput{DispatchHeight: c.height, CurrentSetFrom: 10, LeafUnbuildable: !li.buildable}); !need {
			t.Fatalf("height %d: no Reemit asked", c.height)
		}
	}
}

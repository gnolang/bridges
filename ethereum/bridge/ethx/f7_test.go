package ethx

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Fold F7's tests of ethx (eth/bridge/M3b-NOTES.md, "Fold F7"): the launch line in the burn rule, read from the launch
// HlAdmin; fold F5's new selectors named.

// E2-RT6 by the launch line (M2-NOTES.md Fold F5, F5.1 item 12): a token's own burn — its Dispatch to its route realm
// and its SentTransferRemote in one transaction, the very shape of a real burn — numbered below HlAdmin.launchNonce()
// was dispatched before the launch (the deployer's mint through a stand-in ISM and burn back, possibly inside
// finishSetup's own block) and is no burn: ignored, said why. At the line and above, the same shape counts.
func TestMatchBurnsBelowTheLaunchLine(t *testing.T) {
	alice := [32]byte{12: 0xaa, 31: 0x01}
	dispatches := []types.Log{
		dispatchLog(t, tokGNOT, realmGNOT, 0, alice, 500, 1, 3),     // the deployer's burn back, before the launch
		dispatchLog(t, tokBUBBLE, realmBUBBLE, 1, alice, 70, 2, 3),  // another, in finishSetup's own block
		dispatchLog(t, tokGNOT, realmGNOT, 2, alice, 600, 3, 3),     // the first burn of the launch: at the line
		dispatchLog(t, tokBUBBLE, realmBUBBLE, 3, alice, 800, 4, 3), // and after it
	}
	sents := []types.Log{sentLog(tokGNOT, alice, 500, 1, 2), sentLog(tokBUBBLE, alice, 70, 2, 2), sentLog(tokGNOT, alice, 600, 3, 2), sentLog(tokBUBBLE, alice, 800, 4, 2)}
	burns, ignored, err := MatchBurns(burnMailbox, ethDomain, gnoDomain, 2, burnRoutes(), dispatches, sents)
	if err != nil {
		t.Fatal(err)
	}
	if len(burns) != 2 || burns[0].Nonce != 2 || burns[1].Nonce != 3 {
		t.Fatalf("burns at the line 2: %+v", burns)
	}
	if len(ignored) != 2 || !strings.Contains(ignored[0].Why, "message #0 is below the launch line 2 (HlAdmin.launchNonce())") || !strings.Contains(ignored[1].Why, "#1 is below the launch line 2") {
		t.Fatalf("ignored: %+v", ignored)
	}
	// line 0 (nothing dispatched before the launch): every one counts
	if burns, _, _ := MatchBurns(burnMailbox, ethDomain, gnoDomain, 0, burnRoutes(), dispatches, sents); len(burns) != 4 {
		t.Fatalf("line 0: %d burns", len(burns))
	}
}

// lineReader answers HlAdmin's predecessor() and launchNonce() by address.
type lineReader struct {
	pred  map[common.Address]common.Address
	nonce map[common.Address]uint32
	code  map[common.Address]bool
}

func (r *lineReader) CallAt(_ context.Context, to common.Address, data []byte, _ At) ([]byte, error) {
	if !r.code[to] {
		return nil, nil
	}
	m, err := Admin.ABI.MethodById(data[:4])
	if err != nil {
		return nil, err
	}
	switch m.Name {
	case "predecessor":
		return m.Outputs.Pack(r.pred[to])
	case "launchNonce":
		return m.Outputs.Pack(r.nonce[to])
	}
	return nil, fmt.Errorf("no view %s", m.Name)
}

func (r *lineReader) CallBatch(ctx context.Context, reqs []CallReq, at At) ([][]byte, []error, error) {
	out, errs := make([][]byte, len(reqs)), make([]error, len(reqs))
	for i, q := range reqs {
		out[i], errs[i] = r.CallAt(ctx, q.To, q.Data, at)
	}
	return out, errs, nil
}

// The line is the launch HlAdmin's: from a successor (whose own launchNonce() is 0) predecessor() is followed back to
// the HlAdmin that has none; a loop of predecessors, or no HlAdmin at the address, is an error, never line 0.
func TestReadLaunchLine(t *testing.T) {
	launch, succ, succ2 := common.HexToAddress("0xa1"), common.HexToAddress("0xa2"), common.HexToAddress("0xa3")
	r := &lineReader{pred: map[common.Address]common.Address{succ: launch, succ2: succ}, nonce: map[common.Address]uint32{launch: 7}, code: map[common.Address]bool{launch: true, succ: true, succ2: true}}
	for _, from := range []common.Address{launch, succ, succ2} {
		l, err := ReadLaunchLine(context.Background(), r, from, Latest)
		if err != nil || l.Nonce != 7 || l.Launch != launch {
			t.Fatalf("from %s: %+v %v", from.Hex(), l, err)
		}
	}
	r.pred[launch] = succ2 // a loop
	if _, err := ReadLaunchLine(context.Background(), r, succ2, Latest); err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("a loop of predecessors: %v", err)
	}
	if _, err := ReadLaunchLine(context.Background(), r, common.HexToAddress("0xb0"), Latest); err == nil || !strings.Contains(err.Error(), "an empty answer") {
		t.Fatalf("no HlAdmin there: %v", err)
	}
}

// Fold F5's interface (M2-NOTES.md F5.1 items 1 and 3): ForeignExec and ImplementationMoved are named from their
// selectors, and the two new views have theirs.
func TestF5SelectorsAreNamed(t *testing.T) {
	for name, sel := range map[string]string{"ForeignExec": "33200c06", "ImplementationMoved": "2e23fc45"} {
		data, _ := hex.DecodeString(sel + strings.Repeat("00", 32))
		if r := DecodeRevert(data, "execution reverted"); r.Name != name {
			t.Errorf("selector %s is named %q, not %s", sel, r.Name, name)
		}
	}
	for name, sel := range map[string]string{"launchNonce": "e9a037df", "mailboxPkgPathHash": "40c354cb"} {
		if got := hex.EncodeToString(Admin.ABI.Methods[name].ID); got != sel {
			t.Errorf("HlAdmin.%s() is %s, F5 says %s", name, got, sel)
		}
	}
}

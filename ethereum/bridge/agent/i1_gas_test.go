package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// The integration lane I1's tests of the machines (audit round 3's fold, the cross-lane follow-ups): the executor
// and a starved kind 8.

// callStarved is HlAdmin's CallStarved as a mined execution reverts it (audit round 3, E3-CT4): the transaction left a
// kind 8's call less than its fixed 2,000,000 gas.
func callStarved() error {
	r := ethx.DecodeRevert(crypto.Keccak256([]byte("CallStarved()"))[:4], "execution reverted")
	r.Mined = true
	return r
}

// leastKind8 is the least gas limit that executes the contracts' kind 8 probe (fold F11's measurement): the call's
// floor, CALL_GAS·64/63 + 10,000, left before the call, and the execution's own reads around it.
const leastKind8 = 2_140_278

// E3-CT4's Go half (fold F11's "Go must follow" 2): HlAdmin forwards a kind 8's call exactly 2,000,000 gas and reverts
// an execution left less (CallStarved), the decision pending. A matured kind 8 whose execution starves — sent at an
// estimate short of what the call needs, as a provider answering the gas used would give — is sent again at the next
// tick with starvedGas, never counted as a failure nor parked, and executes; one that starves at starvedGas too counts
// toward the park (more gas is not what it lacks), parked and alarmed after three.
func TestExecutorResendsAStarvedExecuteWithMoreGas(t *testing.T) {
	if got := judgeExecuteErr(callStarved()); got != "gas" {
		t.Fatalf("CallStarved is judged %q", got)
	}
	impl := common.HexToAddress("0x000000000000000000000000000000000000d00d")
	params := append(kind8Params(ethGate, impl, common.Hash{7}), 0x81, 0x29, 0xfc, 0x1c) // and its call: initialize()
	matured := func(w *testWorld) *fakeAdmin {
		a := w.e.admins[ethAdmin]
		a.lastSeq = 1
		a.decisions[1] = &ethx.Decision{Number: 1, Kind: 8, DeliveredTime: w.e.now - 8*86_400, Params: params}
		return a
	}
	t.Run("sent again with more gas", func(t *testing.T) {
		w := newWorld(t, 100, "primary")
		a := matured(w)
		w.e.hook = map[string]func(ethx.Call) error{"execute": func(c ethx.Call) error {
			if c.MinGas < leastKind8 {
				return callStarved()
			}
			return nil
		}}
		w.tick()
		ex := w.e.sentOf("execute")
		at := w.r.state.Attempts["execute:1"]
		if len(ex) != 1 || a.decisions[1] == nil || at == nil || at.Failures != 0 || !at.Parked.IsZero() || at.MinGas != starvedGas {
			t.Fatalf("the starved execute: %d sent, the decision %v, the attempt %+v", len(ex), a.decisions[1] != nil, at)
		}
		if note := w.r.last["execute:1"]; !strings.Contains(note, "starved") || !strings.Contains(note, fmt.Sprint(starvedGas)) {
			t.Fatalf("the status does not say it starved and is sent again with more gas: %q", note)
		}
		w.tick()
		ex = w.e.sentOf("execute")
		if len(ex) != 2 || ex[1].MinGas != starvedGas || a.decisions[1] != nil {
			t.Fatalf("not sent again with more gas at the next tick: %d sent (the last with MinGas %d), %q", len(ex), ex[len(ex)-1].MinGas, w.r.last["execute:1"])
		}
		if at := w.r.state.Attempts["execute:1"]; at != nil {
			t.Fatalf("the attempt stands after the execution: %+v", at)
		}
		if _, parked := w.r.Alarm.Standing()["parked:execute:1"]; parked {
			t.Fatal("a starved execute was parked")
		}
	})
	t.Run("starved at more gas too", func(t *testing.T) {
		w := newWorld(t, 100, "primary")
		a := matured(w)
		w.e.hook = map[string]func(ethx.Call) error{"execute": func(ethx.Call) error { return callStarved() }}
		for i := 0; i < 6; i++ {
			w.tick()
			w.advance(2 * time.Hour)
		}
		at := w.r.state.Attempts["execute:1"]
		if _, parked := w.r.Alarm.Standing()["parked:execute:1"]; !parked || at == nil || at.Parked.IsZero() || at.MinGas != starvedGas {
			t.Fatalf("an execute starved at %d gas too is not parked: %+v, %v", starvedGas, at, w.r.Alarm.StandingKeys())
		}
		ex := w.e.sentOf("execute")
		if len(ex) < 4 || ex[0].MinGas != 0 {
			t.Fatalf("%d executes sent, the first with MinGas %d", len(ex), ex[0].MinGas)
		}
		for i, c := range ex[1:] {
			if c.MinGas != starvedGas {
				t.Fatalf("send %d after the first starve carried MinGas %d", i+2, c.MinGas)
			}
		}
		if a.decisions[1] == nil {
			t.Fatal("the decision was spent")
		}
	})
}

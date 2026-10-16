package agent

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// The integration lane I1's tests of the machines (audit round 3's fold, the cross-lane follow-ups): the proxies'
// EIP-1967 slots after a kind 8.

// executed logs HlAdmin's DecisionExecuted of a number at a block.
func (r *monRig) executed(n uint64, kind uint8, block uint64) {
	data, err := ethx.Admin.ABI.Events["DecisionExecuted"].Inputs.NonIndexed().Pack(kind)
	if err != nil {
		r.t.Fatal(err)
	}
	var nt common.Hash
	binary.BigEndian.PutUint64(nt[24:], n)
	r.chain.log(types.Log{Address: m3bAdmin, Topics: []common.Hash{ethx.Admin.Event("DecisionExecuted"), nt}, Data: data, BlockNumber: block, TxHash: crypto.Keccak256Hash([]byte(fmt.Sprint("executed", n)))})
}

// E3-RT8 and E3-AS7 (fold F11's "Go must follow" 3, after the execution): HlAdmin's checks after a kind 8 read the
// proxy's own answers, which code whose call rewrote the EIP-1967 slots answers as the deployment's. So once a kind 8
// has executed, the monitor — and hlwatch, which is the monitor read-only — reads the proxy's two slots themselves at
// F: holding the decision's implementation and the ProxyAdmin nothing is alarmed; an admin slot rewritten (at the
// upgrade or by any later call of the new code), or an implementation slot off the decision's, alarms
// proxy:slots:<proxy> and the page is not green; the slots put back clear it, and a second kind 8 to the proxy moves
// what its implementation slot must hold.
func TestMonitorHoldsTheProxySlotsToTheKindEight(t *testing.T) {
	r := newMonRig(t)
	proxyAdmin := common.HexToAddress("0x000000000000000000000000000000000000fA11")
	r.chain.view(m3bAdmin, "proxyAdmin", func(uint64, []any) []any { return []any{proxyAdmin} })
	key := "proxy:slots:" + m3bGate.Hex()
	poll := func(height uint64) {
		t.Helper()
		r.chain.mine(height, height)
		r.step(10 * time.Second)
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
	}
	impl := common.HexToAddress("0x000000000000000000000000000000000000D00d")
	b := body(8, 1, append(kind8Params(m3bGate, impl, common.Hash{7}), 0x81, 0x29, 0xfc, 0x1c)...)
	r.chain.mine(110, 110)
	r.decide(1, b, 101, b, false) // r/hlgov's own, recorded at 101
	r.executed(1, 8, 102)         // and executed at 102
	r.chain.setSlot(m3bGate, ethx.SlotAdmin, 1, proxyAdmin)
	r.chain.setSlot(m3bGate, ethx.SlotImplementation, 102, impl)
	poll(110)
	if r.alarmed(key) || len(r.m.state.Decisions.Upgrades) != 1 || r.m.state.Decisions.Upgrades[0].Number != 1 {
		t.Fatalf("slots that hold decision 1's: %v, upgrades %+v", r.m.Alarm.Standing(), r.m.state.Decisions.Upgrades)
	}
	// the new code rewrote the admin slot (a later call of it, at block 112): alarmed, read from the slot itself
	thief := common.HexToAddress("0x0000000000000000000000000000000000000bAd")
	r.chain.setSlot(m3bGate, ethx.SlotAdmin, 112, thief)
	poll(120)
	if text := r.m.Alarm.Standing()[key]; !strings.Contains(text, "its admin slot holds "+thief.Hex()+", not the ProxyAdmin "+proxyAdmin.Hex()) || !strings.Contains(text, "decision #1 (kind 8)") {
		t.Fatalf("a rewritten admin slot: %v", r.m.Alarm.Standing())
	}
	if r.snd.count() != 0 {
		t.Fatalf("flagged (the rule alarms): %v", r.snd.calls)
	}
	// put back: cleared; then the implementation slot moved off the decision's
	r.chain.setSlot(m3bGate, ethx.SlotAdmin, 113, proxyAdmin)
	poll(130)
	if r.alarmed(key) {
		t.Fatalf("the slots put back still alarm: %v", r.m.Alarm.Standing()[key])
	}
	other := common.HexToAddress("0x000000000000000000000000000000000000bEEF")
	r.chain.setSlot(m3bGate, ethx.SlotImplementation, 125, other)
	poll(140)
	if text := r.m.Alarm.Standing()[key]; !strings.Contains(text, "its implementation slot holds "+other.Hex()+", not "+impl.Hex()+", which decision #1 (kind 8) installed") {
		t.Fatalf("an implementation slot off the decision's: %v", r.m.Alarm.Standing())
	}
	// hlwatch, the same reading read-only: not green over it
	r.m.ReadOnly = true
	w := &Watch{m: r.m, Supermajority: 66.66}
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok, why := v.Green(); ok || !strings.Contains(strings.Join(why, "; "), "alarm "+key) {
		t.Fatalf("hlwatch is green, or silent, over a proxy's slots off its kind 8: %v", why)
	}
	// a second kind 8 to the gate installed that implementation: the slot is held to it now
	b2 := body(8, 2, kind8Params(m3bGate, other, common.Hash{8})...)
	r.chain.mine(150, 150)
	r.decide(2, b2, 141, b2, false)
	r.executed(2, 8, 142)
	poll(150)
	if r.alarmed(key) || len(r.m.state.Decisions.Upgrades) != 2 {
		t.Fatalf("the slots of the second kind 8: %v, upgrades %+v", r.m.Alarm.Standing()[key], r.m.state.Decisions.Upgrades)
	}
}

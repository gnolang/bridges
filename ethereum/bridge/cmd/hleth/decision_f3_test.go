package main

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// A pending kind 8 whose implementation has no code yet is shown waiting for it (fold F2: it pends, never spent),
// with who decides when it runs.
func TestStatusShowsADecisionWaitingForCode(t *testing.T) {
	impl := common.HexToAddress("0x000000000000000000000000000000000000d00d")
	proxy := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	params, _ := hex.DecodeString(padHex(proxy.Hex()) + padHex(impl.Hex()) + strings.Repeat("07", 32))
	now := int64(1_800_000_000)
	f := &adminFacts{Admin: &ethx.AdminState{LastSeq: 1, Pending: 1, ClockFactor: 1}, Gate: &ethx.GateState{}, Block: &ethx.Block{Number: 9, Time: uint64(now)},
		Pending: []*ethx.Decision{{Number: 1, Kind: 8, DeliveredTime: uint64(now - 8*86_400), Params: params}}, NoCode: map[uint64]common.Address{1: impl}}
	var b strings.Builder
	printStatus(&b, &Deployment{HlAdmin: "0x0000000000000000000000000000000000000001"}, f)
	if !strings.Contains(b.String(), "NO CODE at "+impl.Hex()+" yet: it pends (HlAdmin refuses its execute NoCodeYet)") {
		t.Fatalf("the status:\n%s", b.String())
	}
}

package agent

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// E4-RT1 on the monitor (lane F15a's float:relayer, the coordinator's ask): the monitor raises the relayers' floor as it
// raises the updater's — the backstop's decision header at the emergency cap — and clears it once the key holds it.
func TestF15bMonitorAlarmsTheRelayersFloor(t *testing.T) {
	r := newMonRig(t)
	relayer := common.HexToAddress("0x000000000000000000000000000000000000cccc")
	r.m.Cfg.Eth.Relayer = relayer.Hex()
	r.chain.balances = map[common.Address]*big.Int{relayer: big.NewInt(5e16)} // 0.05 ETH, short of ≈0.174
	r.m.runways(context.Background())
	if !r.alarmed("float:relayer") {
		t.Fatalf("the monitor did not alarm the relayers' 0.05 ETH: %v", r.m.Alarm.StandingKeys())
	}
	r.chain.balances[relayer] = big.NewInt(2e17)
	r.m.runways(context.Background())
	if r.alarmed("float:relayer") {
		t.Fatal("float:relayer outlived the top-up")
	}
}

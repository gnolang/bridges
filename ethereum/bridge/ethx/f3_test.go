package ethx

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// Fold F3's tests of ethx (eth/bridge/M3b-NOTES.md, "Fold F3"): every custom error HlAdmin and HlAdminExec can
// answer is named by its selector alone; the backup relayer's replacement of a transaction the silent primary left
// pending at the shared nonce, both fees 12.5% over its own. (The sender's creations: f3_create_test.go.)

// errorDecl reads a Solidity source's error declarations: name and parameter types (the canonical signature).
var errorDecl = regexp.MustCompile(`(?m)^\s*error\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)\s*;`)

// Every error declared in HlAdmin, its storage and execution halves and the Guardians library of this tree's
// contracts is named from its selector, as a provider's revert data gives it (E-M3b's Q5: HlAdmin's interface ABI
// declares none, and F2's NoCodeYet is the executor's wait).
func TestEveryAdminErrorIsNamedBySelector(t *testing.T) {
	n := 0
	for _, f := range []string{"HlAdmin.sol", "admin/HlAdminStorage.sol", "admin/HlAdminExec.sol", "lib/Guardians.sol", "GnoLightClient.sol"} {
		b, err := os.ReadFile(filepath.Join("..", "contracts", "src", f))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range errorDecl.FindAllStringSubmatch(string(b), -1) {
			var types []string
			for _, p := range strings.Split(m[2], ",") {
				if p = strings.TrimSpace(p); p != "" {
					types = append(types, strings.Fields(p)[0])
				}
			}
			sig := m[1] + "(" + strings.Join(types, ",") + ")"
			data := append(crypto.Keccak256([]byte(sig))[:4], make([]byte, 32*len(types))...)
			if r := DecodeRevert(data, "execution reverted"); r.Name != m[1] {
				t.Errorf("%s (declared in %s, selector %x) is named %q", sig, f, data[:4], r.Name)
			}
			n++
		}
	}
	if n < 26 {
		t.Fatalf("%d error declarations read: the sources moved", n)
	}
	// fold F4's three by their selectors: a call that adds no signature to its stage, a trust root more than an hour
	// from the initialization, a launch HlAdmin built without the ProxyAdmin
	for sig, sel := range map[string]string{"NoNewSignature": "e10e1571", "TrustedTime": "f3b3b4ed", "ProxyAdminNotHeld": "77bec738"} {
		data, _ := hex.DecodeString(sel)
		if r := DecodeRevert(data, "execution reverted"); r.Name != sig {
			t.Errorf("selector %s is named %q, not %s", sel, r.Name, sig)
		}
	}
	// the executor's wait: a matured kind 8 or 9 whose target has no code yet (fold F2)
	data := append(crypto.Keccak256([]byte("NoCodeYet(address)"))[:4], common.LeftPadBytes([]byte{0xd0, 0x0d}, 32)...)
	if !RevertIs(DecodeRevert(data, "execution reverted"), "NoCodeYet") {
		t.Fatal("NoCodeYet is not named")
	}
}

// pendingBy signs a transaction of the sender's key at a nonce, as another machine sharing the key sent it.
func pendingBy(t *testing.T, s *Sender, nonce uint64, tip, feeCap *big.Int) *types.Transaction {
	t.Helper()
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: s.ChainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: feeCap, Gas: 21000, To: &target, Value: big.NewInt(0)})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(s.ChainID), s.Key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// The backup takes over (§5.7): the primary went silent with a transaction pending at the shared nonce, re-priced far
// above what a fresh call pays. The backup's call reads it from the pool and replaces it on its first broadcast, its
// tip and its fee cap each 12.5% over the pending one's own (E-M3's Q2) — raising its own fees 12.5% at a time never
// reaches it. A pending transaction the call's bounds cannot outbid keeps its nonce: nothing is broadcast.
func TestSenderReplacesThePrimarysPendingTransaction(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{MaxBaseFee: GweiOf(50)})
	ctx := context.Background()
	c.pool[0] = pendingBy(t, s, 0, GweiOf(1), GweiOf(10))
	c.autoMine = true
	rc, err := s.Send(ctx, Call{To: target, Data: []byte{9}, Label: "the backup's call"})
	if err != nil {
		t.Fatalf("the backup could not replace the primary's pending transaction: %v", err)
	}
	if len(c.sent) != 1 {
		t.Fatalf("%d versions broadcast: the pool refused the ones before the one it took", len(c.sent))
	}
	tx := c.sent[0]
	if tx.Nonce() != 0 || tx.GasTipCap().Cmp(bumped(GweiOf(1))) < 0 || tx.GasFeeCap().Cmp(bumped(GweiOf(10))) < 0 {
		t.Fatalf("nonce %d, tip %s, fee cap %s: not both fees 12.5%% over the pending one's (1 gwei, 10 gwei)", tx.Nonce(), tx.GasTipCap(), tx.GasFeeCap())
	}
	if rc.TxHash != tx.Hash() {
		t.Fatal("the receipt is not the replacement's")
	}
	// a pending transaction whose 12.5% rise passes this call's tip ceiling (2 gwei): it keeps its nonce
	s2, c2, _ := testSender(t, SendConfig{MaxBaseFee: GweiOf(50)})
	c2.pool[0] = pendingBy(t, s2, 0, GweiOf(1.9), GweiOf(10))
	if _, err := s2.Send(ctx, Call{To: target, Data: []byte{9}, Label: "a route call"}); !errors.Is(err, ErrStillPending) || !strings.Contains(err.Error(), "past this call's bounds") {
		t.Fatalf("a pending transaction past the call's bounds: %v", err)
	}
	if len(c2.sent) != 0 {
		t.Fatal("a version was broadcast that cannot replace the pending one")
	}
}

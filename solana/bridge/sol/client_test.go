package sol

import (
	"testing"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// A staged transaction's StageVerify names the ed25519 precompile's position in the transaction as sent: past the
// compute-budget instruction when one leads (the e2e's first failure: index 0 named, where the budget sat, and the
// program answered NotThePrecompile), and 0 without one. Every transaction of a staging carries its budget first.
func TestStagedTxsNameThePrecompile(t *testing.T) {
	f := loadFixture(t, "487883")
	c := LightClient{Program: pk(0x11)}
	keys := make([][32]byte, len(f.set.Validators))
	for i, v := range f.set.Validators {
		keys[i] = v.PubKey
	}
	for _, budget := range []uint32{0, 400_000} {
		upd, err := c.UpdateTxs(pk(4), "gnoland-1", f.block.Header, f.commit, keys, nil, 2, budget)
		if err != nil {
			t.Fatal(err)
		}
		proof := &gnolight.SimpleProof{Total: 3, Index: 1, Aunts: [][32]byte{rep(1), rep(2)}}
		prf, err := c.ProofTxs(pk(4), "gnoland-1", f.block.Header, [32]byte{}, f.commit, keys, []byte("message"), proof, make([]byte, 1000), 2, budget)
		if err != nil {
			t.Fatal(err)
		}
		ring, err := c.ProofTxs(pk(4), "gnoland-1", nil, rep(9), nil, nil, []byte("message"), proof, []byte("leaf"), 2, budget)
		if err != nil {
			t.Fatal(err)
		}
		verifies := 0
		for _, txs := range [][][]solana.Instruction{upd, prf, ring} {
			for _, tx := range txs {
				if leads := tx[0].ProgramID().Equals(ComputeBudget); leads != (budget > 0) {
					t.Fatalf("budget %d: the budget instruction leads: %v", budget, leads)
				}
				data, _ := tx[len(tx)-1].Data()
				if len(data) < 3 || data[0] != 4 {
					continue
				}
				idx := int(data[1]) | int(data[2])<<8
				if idx >= len(tx) || !tx[idx].ProgramID().Equals(Ed25519Program) {
					t.Fatalf("budget %d: StageVerify names index %d, not the precompile's (%d instructions)", budget, idx, len(tx))
				}
				verifies++
			}
		}
		if verifies == 0 {
			t.Fatal("no verify transaction built")
		}
	}
}

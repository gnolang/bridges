package sol

import (
	"testing"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// txSize is a transaction's size on the wire: the instructions compiled against one signer (the submitter, who
// pays), with its signature slot.
func txSize(t *testing.T, payer solana.PublicKey, ixs []solana.Instruction) int {
	t.Helper()
	tx, err := solana.NewTransaction(ixs, solana.Hash{1}, solana.TransactionPayer(payer))
	if err != nil {
		t.Fatal(err)
	}
	b, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return len(b)
}

// commitOf is a synthetic commit of n present precommits with 64-byte signatures, and the set's keys.
func commitOf(n int) (*gnolight.Commit, [][32]byte, []gnolight.Validator) {
	cm := &gnolight.Commit{BlockID: gnolight.BlockID{Hash: make([]byte, 32), Parts: gnolight.PartSetHeader{Total: 1, Hash: make([]byte, 32)}}}
	keys := make([][32]byte, n)
	var set []gnolight.Validator
	for i := 0; i < n; i++ {
		cm.Precommits = append(cm.Precommits, &gnolight.Precommit{Round: 0, Time: gnolight.Time{Secs: 1_800_000_000, Nanos: 123}})
		cm.Signatures = append(cm.Signatures, make([]byte, 64))
		keys[i][0], keys[i][1] = byte(i), byte(i>>8)
		set = append(set, gnolight.Validator{PubKey: keys[i], Power: 10})
	}
	return cm, keys, set
}

// F6: every transaction the builders stage — an update's init with its precommit chunks, the next-set chunks,
// the verifies and the finish; a proof's by header and by the ring with its leaf chunks — fits Solana's
// 1,232-byte packet for a 60-validator set, and for 255 (the client's count byte); the program-test harness
// enforces no packet size, so this test does.
func TestStagedTxsFitThePacket(t *testing.T) {
	c := LightClient{Program: solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")}
	sub := solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	hdr := &gnolight.Header{Version: "v1.0.0-rc.0", ChainID: "gnoland-1", Height: 741_862, NumTxs: 1, TotalTxs: 123_456, AppVersion: "v1.2.0", ValidatorsHash: make([]byte, 32), NextValidatorsHash: make([]byte, 32),
		AppHash: make([]byte, 32), ConsensusHash: make([]byte, 32), LastResultsHash: make([]byte, 32), DataHash: make([]byte, 32), LastCommitHash: make([]byte, 32), ProposerAddress: "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5",
		LastBlockID: gnolight.BlockID{Hash: make([]byte, 32), Parts: gnolight.PartSetHeader{Total: 1, Hash: make([]byte, 32)}}, Time: gnolight.Time{Secs: 1_800_000_000}}
	for _, n := range []int{6, 17, 18, 60, 255} {
		cm, keys, set := commitOf(n)
		upd, err := c.UpdateTxs(sub, "gnoland-1", hdr, cm, keys, set, 3, 400_000)
		if err != nil {
			t.Fatal(err)
		}
		proof := &gnolight.SimpleProof{Total: 8, Index: 3, LeafHash: [32]byte{1}, Aunts: [][32]byte{{2}, {3}, {4}}}
		prf, err := c.ProofTxs(sub, "gnoland-1", hdr, [32]byte{}, cm, keys, make([]byte, 200), proof, make([]byte, MaxLeaf), 3, 400_000)
		if err != nil {
			t.Fatal(err)
		}
		ring, err := c.ProofTxs(sub, "gnoland-1", nil, [32]byte{9}, nil, nil, make([]byte, 200), proof, make([]byte, 1000), 3, 400_000)
		if err != nil {
			t.Fatal(err)
		}
		for name, txs := range map[string][][]solana.Instruction{"update": upd, "proof": prf, "ring": ring} {
			for i, tx := range txs {
				if size := txSize(t, sub, tx); size > PacketLimit {
					t.Errorf("%d validators: %s transaction %d of %d is %d bytes, over the packet limit of %d", n, name, i, len(txs), size, PacketLimit)
				}
			}
		}
		// the precommit slots and the next set arrive whole, each slot once
		filled := map[int]bool{}
		next := 0
		for _, tx := range upd {
			for _, ix := range tx {
				d, _ := ix.Data()
				if !ix.ProgramID().Equals(c.Program) || len(d) == 0 {
					continue
				}
				switch d[0] {
				case 2:
					off := int(d[1]) | int(d[2])<<8
					count := int(d[3]) | int(d[4])<<8 | int(d[5])<<16 | int(d[6])<<24
					for j := 0; j < count; j++ {
						if filled[off+j] {
							t.Fatalf("%d validators: slot %d staged twice", n, off+j)
						}
						filled[off+j] = true
					}
				case 9:
					next += int(d[3]) | int(d[4])<<8 | int(d[5])<<16 | int(d[6])<<24
				}
			}
		}
		if len(filled) != n || next != n {
			t.Fatalf("%d validators: %d precommit slots and %d next-set entries staged", n, len(filled), next)
		}
	}
	// the sizes named in the finding: 25 next-set entries overflowed, 23 fit; 18 precommits beside the init
	// overflowed, 16 fit with room
	if size := txSize(t, sub, withBudget(400_000, c.StageNextSetIx(sub, sub, 0, commitSet(25)))); size <= PacketLimit {
		t.Fatalf("25 next-set entries fit in %d bytes: the chunk could be larger", size)
	}
	if size := txSize(t, sub, withBudget(400_000, c.StageNextSetIx(sub, sub, 0, commitSet(NextSetChunk)))); size > PacketLimit {
		t.Fatalf("%d next-set entries are %d bytes", NextSetChunk, size)
	}
}

func commitSet(n int) []gnolight.Validator {
	_, _, set := commitOf(n)
	return set
}

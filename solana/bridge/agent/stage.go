package agent

import (
	"context"
	"fmt"
	"log"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/sol"
)

// A staging (an update or a proof) is several transactions into one PDA keyed by (submitter, hash). driveStage
// sends them in order and resumes what a stage already on chain shows done: the init once the account stands,
// a next-set chunk once the set is filled past it, a verify transaction once its slots are verified, a leaf chunk
// once the leaf is filled past it — but only a stage that is this plan's, field by field (stageMatches): one
// left by an earlier plan — another header, the ring instead of a header, another next set, proof or leaf — is
// closed and rebuilt (G1: resumed as it stood, it strands the message behind a deterministic refusal). A refusal
// that says someone else moved the client first closes the stage; a stage that needs room grows the state
// toward bodyCap (the region a plan's set change needs, S-02; 0 for the body's own want); any other refusal of
// the program closes the stage and comes back as the error.
func driveStage(ctx context.Context, c *sol.Client, client sol.LightClient, stage solana.PublicKey, txs [][]solana.Instruction, bodyCap uint32) error {
	exists, data, err := accountExists(ctx, c.RPC, stage, rpc.CommitmentConfirmed)
	if err != nil {
		return err
	}
	payer := c.Payer.PublicKey()
	var st *sol.Stage
	if exists {
		if st, err = sol.ParseStage(data); err != nil {
			return fmt.Errorf("the stage on chain: %w", err)
		}
		plan, ok := planOf(txs)
		if !ok {
			return fmt.Errorf("the staging's first transaction carries no StageInit")
		}
		if same, why := stageMatches(st, plan); !same {
			log.Printf("staging: the stage on chain is not this plan's (%s): closing it and rebuilding", why)
			if _, err := c.Send(ctx, []solana.Instruction{client.StageCloseIx(stage, payer)}); err != nil {
				return fmt.Errorf("closing a stale stage: %w", err)
			}
			st = nil
		}
	}
	filled := uint32(0)
	for i := 0; i < len(txs); i++ {
		tx := txs[i]
		if st != nil {
			if i == 0 {
				continue // the init (and the precommits) landed
			}
			if allVerified(st, tx) {
				continue
			}
			if off, n, ok := nextSetChunk(tx); ok && int(off)+n <= len(st.NextSet) {
				continue // this chunk of the next set is on chain
			}
			if off, n, ok := precommitChunk(tx); ok && allFilled(st, int(off), n) {
				continue // this chunk of the precommit slots is on chain (F6: the program refuses a slot filled twice)
			}
			if n, ok := leafChunk(tx); ok {
				filled += n
				if filled <= st.LeafFilled {
					continue
				}
			}
		}
		// sent as built: the list carries its compute budget first and a verify instruction names the precompile's
		// position in it (sol.UpdateTxs/ProofTxs); prepending anything here would move the precompile
		if _, err := c.Send(ctx, tx); err != nil {
			switch {
			case solErrIs(err, "HeightNotAboveTrusted"):
				log.Printf("staging: the client moved past this header first; closing the stage")
				_, _ = c.Send(ctx, []solana.Instruction{client.StageCloseIx(stage, payer)})
				return nil
			case solErrIs(err, "NeedsGrow"):
				log.Printf("staging: the client's state needs room: growing it (toward a body region of %d bytes)", bodyCap)
				if _, gerr := c.Send(ctx, []solana.Instruction{client.GrowIx(payer, bodyCap)}); gerr != nil {
					return gerr
				}
				i-- // the same transaction again
				continue
			case solDeterministic(err):
				if i > 0 || st != nil {
					_, _ = c.Send(ctx, []solana.Instruction{client.StageCloseIx(stage, payer)})
				}
				return fmt.Errorf("the client refused the staging (%s): %w", orNone(solErrName(err)), err)
			}
			return err
		}
	}
	return nil
}

// planOf is the StageInit the staging's first transaction carries.
func planOf(txs [][]solana.Instruction) (*sol.StageInitArgs, bool) {
	if len(txs) == 0 {
		return nil, false
	}
	data, ok := clientIxData(txs[0], 1)
	if !ok {
		return nil, false
	}
	a, ok, err := sol.DecodeStageInit(data)
	if err != nil || !ok {
		return nil, false
	}
	return a, true
}

// stageMatches compares a stage on chain with a plan's StageInit, field by field: the kind, the key (a proof's
// message hash), by the ring or by a header and which hash, the precommit slots, the next set an update
// announces, the proof and the leaf's length. Anything else on chain is another plan's stage.
func stageMatches(st *sol.Stage, plan *sol.StageInitArgs) (bool, string) {
	if st.Kind != plan.Kind {
		return false, fmt.Sprintf("kind %d on chain, the plan's is %d", st.Kind, plan.Kind)
	}
	if plan.Kind == sol.StageKindProof && st.Key != plan.MessageHash {
		return false, "another message's proof"
	}
	if plan.Header != nil {
		if st.ByRing {
			return false, "by the ring on chain, by a header in the plan"
		}
		if h := plan.Header.Hash(); st.HeaderHash != h {
			return false, fmt.Sprintf("header %x on chain, the plan's is %x (height %d)", st.HeaderHash[:6], h[:6], plan.Header.Height)
		}
		if st.NSlots != plan.NSlots {
			return false, fmt.Sprintf("%d precommit slots on chain, %d in the plan", st.NSlots, plan.NSlots)
		}
		if st.Round != plan.Round {
			return false, fmt.Sprintf("the commit's round %d on chain, %d in the plan (S-08)", st.Round, plan.Round)
		}
	} else {
		if !st.ByRing {
			return false, "by a header on chain, by the ring in the plan"
		}
		if st.HeaderHash != plan.RingHash {
			return false, fmt.Sprintf("ring hash %x on chain, the plan's is %x", st.HeaderHash[:6], plan.RingHash[:6])
		}
	}
	if st.NextSetLen != plan.NextSetLen || st.NextSetHash != plan.NextSetHash {
		return false, fmt.Sprintf("a next set of %d on chain (hash %x), %d in the plan (%x)", st.NextSetLen, st.NextSetHash[:6], plan.NextSetLen, plan.NextSetHash[:6])
	}
	if (st.Proof == nil) != (plan.Proof == nil) {
		return false, "one carries a proof, the other not"
	}
	if plan.Proof != nil {
		a, b := st.Proof, plan.Proof
		same := a.Total == b.Total && a.Index == b.Index && a.LeafHash == b.LeafHash && len(a.Aunts) == len(b.Aunts)
		for i := 0; same && i < len(a.Aunts); i++ {
			same = a.Aunts[i] == b.Aunts[i]
		}
		if !same {
			return false, "another proof"
		}
	}
	if st.LeafLen != plan.LeafLen {
		return false, fmt.Sprintf("a leaf of %d bytes on chain, %d in the plan", st.LeafLen, plan.LeafLen)
	}
	return true, ""
}

// clientIxData is the data of the client's instruction with this discriminator in a staged transaction, past the
// compute-budget and precompile instructions before it.
func clientIxData(tx []solana.Instruction, disc byte) ([]byte, bool) {
	for _, ix := range tx {
		if p := ix.ProgramID(); p.Equals(sol.ComputeBudget) || p.Equals(sol.Ed25519Program) {
			continue
		}
		data, err := ix.Data()
		if err != nil || len(data) == 0 || data[0] != disc {
			return nil, false
		}
		return data, true
	}
	return nil, false
}

// allVerified says whether a [budget + precompile + StageVerify] transaction's slots are verified already.
func allVerified(st *sol.Stage, tx []solana.Instruction) bool {
	data, ok := clientIxData(tx, 4)
	if !ok || len(data) < 7 {
		return false
	}
	n := int(data[3]) | int(data[4])<<8 | int(data[5])<<16 | int(data[6])<<24
	for j := 0; j < n; j++ {
		o := 7 + 2*j
		if o+1 >= len(data) {
			return false
		}
		slot := int(data[o]) | int(data[o+1])<<8
		if !st.IsVerified(slot) {
			return false
		}
	}
	return true
}

// leafChunk is the size of a [budget + StageLeaf] transaction's chunk.
func leafChunk(tx []solana.Instruction) (uint32, bool) {
	data, ok := clientIxData(tx, 3)
	if !ok || len(data) < 5 {
		return 0, false
	}
	return uint32(data[1]) | uint32(data[2])<<8 | uint32(data[3])<<16 | uint32(data[4])<<24, true
}

// precommitChunk is the offset and count of a [budget + StagePrecommits] transaction of its own (F6: the slots
// past the init's chunk travel in further transactions through the instruction's offset).
func precommitChunk(tx []solana.Instruction) (offset uint16, count int, ok bool) {
	data, ok := clientIxData(tx, 2)
	if !ok || len(data) < 7 {
		return 0, 0, false
	}
	offset = uint16(data[1]) | uint16(data[2])<<8
	count = int(data[3]) | int(data[4])<<8 | int(data[5])<<16 | int(data[6])<<24
	return offset, count, true
}

// allFilled says whether the stage holds every precommit slot of [off, off+n).
func allFilled(st *sol.Stage, off, n int) bool {
	for i := off; i < off+n; i++ {
		if !st.IsFilled(i) {
			return false
		}
	}
	return true
}

// nextSetChunk is the offset and count of a [budget + StageNextSet] transaction's validators.
func nextSetChunk(tx []solana.Instruction) (offset uint16, count int, ok bool) {
	data, ok := clientIxData(tx, 9)
	if !ok || len(data) < 7 {
		return 0, 0, false
	}
	offset = uint16(data[1]) | uint16(data[2])<<8
	count = int(data[3]) | int(data[4])<<8 | int(data[5])<<16 | int(data[6])<<24
	return offset, count, true
}

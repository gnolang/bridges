package agent

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The record rebuilt from instruction data folds exactly what the client records (one counter, one running hash
// over hash ‖ height ‖ slot), from the init through updates and fresh proofs, with the ring's proofs recording
// nothing; a finish whose stage was never seen is a gap.
func TestClientRecordFold(t *testing.T) {
	client := sol.LightClient{Program: solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")}
	mailbox := solana.MustPublicKeyFromBase58("9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr")
	submitter := solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	hdr := func(h int64) *gnolight.Header {
		return &gnolight.Header{ChainID: "dev", Height: h, ValidatorsHash: make([]byte, 32), NextValidatorsHash: make([]byte, 32)}
	}
	ixOf := func(i solana.Instruction) txInstruction {
		d, _ := i.Data()
		var accts []solana.PublicKey
		for _, a := range i.Accounts() {
			accts = append(accts, a.PublicKey)
		}
		return txInstruction{Program: i.ProgramID(), Accounts: accts, Data: d}
	}
	var r ClientRecord
	// the init records the trusted header at its slot
	var trusted [32]byte
	trusted[0] = 9
	r.Fold(client, mailbox, []txInstruction{ixOf(client.InitIx(submitter, submitter, "dev", 1, 100, trusted, nil, submitter))}, nil, 10, "s1")
	// an update: StageInit at slot 11, the finish at 12 records
	h101 := hdr(101)
	stage := client.UpdateStage(submitter, h101.Hash())
	r.Fold(client, mailbox, []txInstruction{ixOf(client.StageInitIx(submitter, stage, &sol.StageInitArgs{Kind: sol.StageKindUpdate, Header: h101, NSlots: 1}))}, nil, 11, "s2")
	r.Fold(client, mailbox, []txInstruction{ixOf(client.UpdateFinishIx(stage, submitter))}, nil, 12, "s3")
	// a proof by header: staged at 13, consumed by the mailbox's process at 14
	h99 := hdr(99)
	pstage := client.ProofStage(submitter, [32]byte{1})
	r.Fold(client, mailbox, []txInstruction{ixOf(client.StageInitIx(submitter, pstage, &sol.StageInitArgs{Kind: sol.StageKindProof, Header: h99, NSlots: 1, MessageHash: [32]byte{1}, Proof: &gnolight.SimpleProof{Total: 1}, LeafLen: 5}))}, nil, 13, "s4")
	process := txInstruction{Program: mailbox, Data: []byte{1, 0, 0, 0, 0}}
	r.Fold(client, mailbox, []txInstruction{process}, []solana.PublicKey{submitter, pstage, client.State()}, 14, "s5")
	// a proof by the ring: records nothing
	rstage := client.ProofStage(submitter, [32]byte{2})
	r.Fold(client, mailbox, []txInstruction{ixOf(client.StageInitIx(submitter, rstage, &sol.StageInitArgs{Kind: sol.StageKindProof, RingHash: h101.Hash(), MessageHash: [32]byte{2}, Proof: &gnolight.SimpleProof{Total: 1}, LeafLen: 5}))}, nil, 15, "s6")
	r.Fold(client, mailbox, []txInstruction{process}, []solana.PublicKey{submitter, rstage, client.State()}, 16, "s7")
	// a finish of a stage never seen: a gap
	r.Fold(client, mailbox, []txInstruction{ixOf(client.UpdateFinishIx(client.UpdateStage(submitter, [32]byte{3}), submitter))}, nil, 18, "s9")

	if r.Count != 3 || r.Gaps != 1 {
		t.Fatalf("count %d gaps %d", r.Count, r.Gaps)
	}
	// the running hash as the program computes it
	want := make([]byte, 32)
	h101h, h99h := h101.Hash(), h99.Hash()
	for _, e := range []struct {
		hash   [32]byte
		height int64
		slot   uint64
	}{{trusted, 100, 10}, {h101h, 101, 12}, {h99h, 99, 14}} {
		var hb, sb [8]byte
		binary.LittleEndian.PutUint64(hb[:], uint64(e.height))
		binary.LittleEndian.PutUint64(sb[:], e.slot)
		x := sha256.Sum256(append(append(append(append([]byte{}, want...), e.hash[:]...), hb[:]...), sb[:]...))
		want = x[:]
	}
	if r.Hash != hex.EncodeToString(want) {
		t.Fatalf("running hash %s, want %s", r.Hash, hex.EncodeToString(want))
	}
	var running [32]byte
	copy(running[:], want)
	if ok, why := r.Consistent(3, running, 18); !ok {
		t.Fatal(why)
	}
	r.ScanSlot = 18
	if ok, why := r.Consistent(4, running, 18); ok || why[:3] != "an " {
		t.Fatalf("a gap must show: %v %s", ok, why)
	}
	r.ScanSlot = 10
	if ok, why := r.Consistent(4, running, 18); ok || why[:7] != "behind:" {
		t.Fatalf("a scan behind the snapshot is not a gap yet: %v %s", ok, why)
	}
	// the chain agrees on every header but 99, where the client's differs: the cut is slot 14
	chain := fakeHashes{heads: 200, hashes: map[int64]string{100: hex.EncodeToString(trusted[:]), 101: hex.EncodeToString(h101h[:]), 99: "aa"}}
	mm, err := r.Check(context.Background(), chain)
	if err != nil || mm == nil || mm.Height != 99 || mm.Slot != 14 || mm.Chain != "aa" {
		t.Fatalf("%+v %v", mm, err)
	}
	if r.Checked != 3 {
		t.Fatalf("checked %d", r.Checked)
	}
	_ = fmt.Sprint(mm.String())
}

type fakeHashes struct {
	heads  int64
	hashes map[int64]string
}

func (f fakeHashes) BlockHash(_ context.Context, height int64) (string, int64, error) {
	if height > f.heads {
		return "", f.heads, nil
	}
	h, ok := f.hashes[height]
	if !ok {
		return "", f.heads, errors.New("no hash in the fake")
	}
	return h, f.heads, nil
}

// S-10: a header-based proof is a record entry only when its header is new to the client — an accepted update's
// header, or one proven before, records nothing again; a set switch clears the ring, so the same header counts
// once more after it.
func TestClientRecordRingRule(t *testing.T) {
	client := sol.LightClient{Program: solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")}
	mailbox := solana.MustPublicKeyFromBase58("9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr")
	submitter := solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	hdr := func(h int64, vals byte) *gnolight.Header {
		v := make([]byte, 32)
		v[0] = vals
		return &gnolight.Header{ChainID: "dev", Height: h, ValidatorsHash: v, NextValidatorsHash: v}
	}
	ixOf := func(i solana.Instruction) txInstruction {
		d, _ := i.Data()
		var accts []solana.PublicKey
		for _, a := range i.Accounts() {
			accts = append(accts, a.PublicKey)
		}
		return txInstruction{Program: i.ProgramID(), Accounts: accts, Data: d}
	}
	var r ClientRecord
	slot := uint64(1)
	update := func(h *gnolight.Header) {
		stage := client.UpdateStage(submitter, h.Hash())
		r.Fold(client, mailbox, []txInstruction{ixOf(client.StageInitIx(submitter, stage, &sol.StageInitArgs{Kind: sol.StageKindUpdate, Header: h, NSlots: 1}))}, nil, slot, "u")
		r.Fold(client, mailbox, []txInstruction{ixOf(client.UpdateFinishIx(stage, submitter))}, nil, slot+1, "u")
		slot += 2
	}
	prove := func(h *gnolight.Header, key byte) {
		stage := client.ProofStage(submitter, [32]byte{key})
		r.Fold(client, mailbox, []txInstruction{ixOf(client.StageInitIx(submitter, stage, &sol.StageInitArgs{Kind: sol.StageKindProof, Header: h, NSlots: 1, MessageHash: [32]byte{key}, Proof: &gnolight.SimpleProof{Total: 1}, LeafLen: 5}))}, nil, slot, "p")
		r.Fold(client, mailbox, []txInstruction{{Program: mailbox, Data: []byte{1, 0, 0, 0, 0}}}, []solana.PublicKey{submitter, stage, client.State()}, slot+1, "p")
		slot += 2
	}
	r.Fold(client, mailbox, []txInstruction{ixOf(client.InitIx(submitter, submitter, "dev", 1, 100, [32]byte{9}, nil, submitter))}, nil, slot, "i")
	h101, h99, h98 := hdr(101, 0), hdr(99, 0), hdr(98, 0)
	update(h101) // recorded, in the ring
	prove(h101, 1)
	if r.Count != 2 {
		t.Fatalf("a proof of an accepted header was recorded: %d entries", r.Count)
	}
	prove(h99, 2) // fresh: recorded once
	prove(h99, 3) // the ring holds it now
	if r.Count != 3 || len(r.Ring) != 2 {
		t.Fatalf("a repeated proof: %d entries, ring %d", r.Count, len(r.Ring))
	}
	// a set switch (an update whose ValidatorsHash differs) clears the ring: the same header counts once more
	update(hdr(200, 1))
	prove(h99, 4)
	if r.Count != 5 || len(r.Ring) != 2 {
		t.Fatalf("after the switch: %d entries, ring %d", r.Count, len(r.Ring))
	}
	prove(h98, 5)
	if r.Count != 6 {
		t.Fatalf("a fresh header after the switch: %d entries", r.Count)
	}
	// the ring evicts past the client's capacity
	for i := 0; i < sol.Ring+5; i++ {
		update(hdr(1000+int64(i), 1))
	}
	if len(r.Ring) != sol.Ring {
		t.Fatalf("ring %d, want %d", len(r.Ring), sol.Ring)
	}
}

// A header above the own node's head is no mismatch (G15): it waits unchecked for the node to reach it — a
// node seconds behind the updater's is no fork, and a client past the chain is step 2's to judge with the
// allowance, a second source and two minutes' persistence — and is checked once the head is there.
func TestClientRecordAboveTheHead(t *testing.T) {
	var r ClientRecord
	r.init()
	zero := hex.EncodeToString(make([]byte, 32))
	r.fold(RecordEntry{Hash: zero, Height: 500, Slot: 3, Kind: "update", Sig: "x"})
	mm, err := r.Check(context.Background(), fakeHashes{heads: 400})
	if err != nil || mm != nil || r.Checked != 0 || r.Unchecked() != 1 {
		t.Fatalf("an entry above the head was judged: %+v %v (checked %d, unchecked %d)", mm, err, r.Checked, r.Unchecked())
	}
	mm, err = r.Check(context.Background(), fakeHashes{heads: 500, hashes: map[int64]string{500: zero}})
	if err != nil || mm != nil || r.Checked != 1 || r.Unchecked() != 0 {
		t.Fatalf("the entry was not checked once the head reached it: %+v %v (checked %d)", mm, err, r.Checked)
	}
	r.fold(RecordEntry{Hash: zero, Height: 501, Slot: 4, Kind: "update", Sig: "y"})
	mm, _ = r.Check(context.Background(), fakeHashes{heads: 600, hashes: map[int64]string{501: "bb"}})
	if mm == nil || mm.Height != 501 || mm.Chain != "bb" {
		t.Fatalf("a differing header at a height the node has is not the cut: %+v", mm)
	}
}

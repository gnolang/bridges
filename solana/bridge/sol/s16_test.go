package sol

import (
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// S-16 (M2f): the client Body ends with its ring epoch (8 bytes after running_hash) and every Stage with a
// by-ring proof's copy of its entry (48 bytes after leaf_filled: the epoch, the height, the results hash); the
// Go side reads both, counts the body's 8 in its size, and refuses the old forms short of them.
func TestClientStateAndStageCarryTheRingEpoch(t *testing.T) {
	body := &ClientBody{Admin: pk(1), ChainID: "dev", OriginDomain: 7, Trusted: Trusted{Height: 100, Hash: rep(2)}, Set: []gnolight.Validator{{PubKey: rep(3), Power: 10}},
		SetHash: rep(4), NextSetHash: rep(5), History: []SetEntry{{FromHeight: 1, SetHash: rep(4), Set: []gnolight.Validator{{PubKey: rep(3), Power: 10}}}}, Records: 9, RunningHash: rep(6), RingEpoch: 3}
	enc := body.encode()
	if body.Size() != len(enc) || enc[len(enc)-8] != 3 {
		t.Fatalf("the body's size %d does not count the ring epoch at its end: %x", body.Size(), enc[len(enc)-8:])
	}
	var h enc2
	h.u8(ClientStateVersion)
	h.u8(8)
	h.u16(0)
	h.u16(0)
	h.u16(uint16(BodyCapFor(len(enc)) / 8))
	h.i64(0)
	h.i64(0)
	h.u64(100)
	h.arr32(rep(2))
	data := append(h.Bytes(), enc...)
	data = append(data, make([]byte, BodyCapFor(len(enc))-len(enc))...)
	_, got, _, err := ParseClientState(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.RingEpoch != 3 || got.Records != 9 || got.RunningHash != rep(6) {
		t.Fatalf("parsed: epoch %d, records %d", got.RingEpoch, got.Records)
	}
	// the form before S-16, 8 bytes short, does not parse
	short := append(h.Bytes(), enc[:len(enc)-8]...)
	if _, _, _, err := ParseClientState(short); err == nil {
		t.Fatal("a body without the ring epoch parsed")
	}
	// a stage: every field in ParseStage's order, the copy last, the leaf's bytes after
	var s enc2
	s.u8(1)
	s.u8(StageKindProof)
	s.u8(254)
	s.key(pk(9))
	s.u64(500)
	s.i64(1_800_000_000)
	s.arr32(rep(7))
	s.boolean(true)
	s.arr32(rep(8))
	s.header(&gnolight.Header{ChainID: "dev", Height: 102, ValidatorsHash: make([]byte, 32), NextValidatorsHash: make([]byte, 32)})
	s.blockID(gnolight.BlockID{})
	s.i64(0)
	s.arr32(rep(4))
	s.u16(0)
	s.u32(0)
	s.vec(nil)
	s.vec(nil)
	s.validators(nil)
	s.u16(0)
	s.arr32([32]byte{})
	s.u8(1)
	s.proof(&gnolight.SimpleProof{Total: 1, Index: 0, LeafHash: rep(11)})
	s.u32(5)
	s.u32(5)
	s.u64(3)
	s.i64(102)
	s.arr32(rep(12))
	stageBytes := append(s.Bytes(), 'l', 'e', 'a', 'f', '!')
	st, err := ParseStage(stageBytes)
	if err != nil {
		t.Fatal(err)
	}
	if st.RingEpoch != 3 || st.RingHeight != 102 || st.RingResults != rep(12) || st.LeafFilled != 5 || !st.ByRing {
		t.Fatalf("the stage's copy: epoch %d, height %d, results %x", st.RingEpoch, st.RingHeight, st.RingResults[:4])
	}
	if _, err := ParseStage(stageBytes[:len(stageBytes)-5-48]); err == nil {
		t.Fatal("a stage without the 48-byte copy parsed")
	}
}

// enc2 is the package's encoder with the stage's composite writers, for the test's fixtures.
type enc2 = enc

package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// fakeLight is a synthetic chain: one validator set per height, with headers whose ValidatorsHash is the set's
// and whose NextValidatorsHash is the next height's set's.
type fakeLight struct {
	*fakeChain
	sets  map[int64]*gnolight.ValidatorSet
	head  int64
	calls int
}

func setOf(seed byte, n int) *gnolight.ValidatorSet {
	s := &gnolight.ValidatorSet{}
	for i := 0; i < n; i++ {
		var pk [32]byte
		pk[0], pk[1] = seed, byte(i)
		s.Validators = append(s.Validators, gnolight.Validator{PubKey: pk, Power: 10})
	}
	s.Sort()
	return s
}

func (f *fakeLight) setAt(h int64) *gnolight.ValidatorSet {
	var best int64
	for from := range f.sets {
		if from <= h && from > best {
			best = from
		}
	}
	return f.sets[best]
}

func (f *fakeLight) ValidatorsAt(_ context.Context, h int64) (*gnolight.ValidatorSet, error) {
	f.calls++
	if h > f.head+1 || h < 1 {
		return nil, errors.New("no such height")
	}
	return f.setAt(h), nil
}

func (f *fakeLight) BlockAt(_ context.Context, h int64) (*gnolight.Block, error) {
	if h > f.head || h < 1 {
		return nil, errors.New("no such height")
	}
	cur, _ := f.setAt(h).Hash()
	next, _ := f.setAt(h + 1).Hash()
	hdr := &gnolight.Header{ChainID: "dev", Height: h, ValidatorsHash: cur[:], NextValidatorsHash: next[:]}
	return &gnolight.Block{Header: hdr, Hash: hdr.Hash()}, nil
}

func (f *fakeLight) CommitAt(context.Context, int64) (*gnolight.Commit, []*gnolight.Vote, error) {
	return nil, nil, errors.New("not in the fake")
}

func (f *fakeLight) BlockResults(context.Context, int64) ([]*gnolight.TxResult, error) {
	return nil, nil
}

func clientAt(trusted int64, set *gnolight.ValidatorSet, next *gnolight.ValidatorSet) *clientState {
	sh, _ := set.Hash()
	cs := &clientState{Head: &sol.ClientHead{TrustedHeight: uint64(trusted)}, Body: &sol.ClientBody{SetHash: sh, Set: set.Validators}}
	if next != nil {
		nh, _ := next.Hash()
		cs.Body.NextSet, cs.Body.NextSetHash = next.Validators, nh
	}
	return cs
}

// The plan: the head when its set is the client's; the announcing header (with the set it announces) when the
// set changed after the trusted height; the first header of the announced set once the client waits for it.
func TestPlanUpdate(t *testing.T) {
	a, b, c := setOf(1, 4), setOf(2, 5), setOf(3, 6)
	f := &fakeLight{fakeChain: newFake(), sets: map[int64]*gnolight.ValidatorSet{1: a, 100: b, 150: c}, head: 200}
	ctx := context.Background()

	// quiet: the head
	p, err := planUpdate(ctx, f, clientAt(50, a, nil), 90)
	if err != nil || p.Target != 90 || p.Announcing {
		t.Fatalf("%+v %v", p, err)
	}
	// the head is itself the announcing header (99 announces the set of 100)
	p, err = planUpdate(ctx, f, clientAt(50, a, nil), 99)
	if err != nil || p.Target != 99 || !p.Announcing || len(p.NextSet) != 5 {
		t.Fatalf("%+v %v", p, err)
	}
	// a rotation behind the head: 99 must be presented, with the set of 100
	f.calls = 0
	p, err = planUpdate(ctx, f, clientAt(50, a, nil), 200)
	if err != nil || p.Target != 99 || !p.Announcing || len(p.NextSet) != 5 {
		t.Fatalf("%+v %v", p, err)
	}
	if f.calls > 20 {
		t.Fatalf("%d validator reads for a binary search over 150 heights", f.calls)
	}
	// the client waits for the announced set: any header of it, the head if the head is still in it
	p, err = planUpdate(ctx, f, clientAt(99, a, b), 120)
	if err != nil || p.Target != 120 || p.Announcing {
		t.Fatalf("%+v %v", p, err)
	}
	// two rotations behind: first the one the client waits for ends at 149, which announces the set of 150
	p, err = planUpdate(ctx, f, clientAt(99, a, b), 200)
	if err != nil || p.Target != 149 || !p.Announcing || len(p.NextSet) != 6 {
		t.Fatalf("%+v %v", p, err)
	}
	// the chain at the trusted height: nothing (the caller checks the head first); a head below is an error path
	// a trusted header that itself announced a change the client did not take: the cure is a reset
	_, err = planUpdate(ctx, f, clientAt(99, a, nil), 200)
	if err == nil {
		t.Fatal("a change right after the trusted header must be refused")
	}
}

func TestFirstDiffering(t *testing.T) {
	var e, o [32]byte
	o[0] = 1
	for change := int64(2); change <= 40; change++ {
		calls := 0
		hashAt := func(h int64) ([32]byte, error) {
			calls++
			if h >= change {
				return o, nil
			}
			return e, nil
		}
		x, err := firstDiffering(1, 40, e, hashAt)
		if err != nil || x != change {
			t.Fatalf("change at %d: got %d %v", change, x, err)
		}
		if calls > 8 {
			t.Fatalf("change at %d: %d reads", change, calls)
		}
	}
	// a set that changed and changed back: the linear fallback finds the first change
	hashAt := func(h int64) ([32]byte, error) {
		if h == 5 || h == 6 {
			return o, nil
		}
		return e, nil
	}
	if x, _ := firstDiffering(1, 40, e, hashAt); x != 5 {
		t.Fatalf("non-monotone: %d", x)
	}
}

// A stage already on chain is resumed: verified slots are skipped.
func TestAllVerified(t *testing.T) {
	c := sol.LightClient{}
	st := &sol.Stage{Verified: []byte{0b00000111}}
	verify := func(slots ...uint16) []solana.Instruction { // the shape as built: the budget first, the precompile at 1
		return []solana.Instruction{sol.ComputeLimitIx(400_000), sol.Ed25519Ix(nil), c.StageVerifyIx(solana.PublicKey{}, 1, slots)}
	}
	if !allVerified(st, verify(0, 1, 2)) {
		t.Fatal("verified slots not skipped")
	}
	if allVerified(st, verify(3)) || allVerified(st, verify(2, 3)) {
		t.Fatal("an unverified slot skipped")
	}
	if allVerified(st, verify(0, 1, 2)[1:]) != true { // and without a budget instruction
		t.Fatal("the shape without a budget instruction is not read")
	}
	if n, ok := leafChunk([]solana.Instruction{sol.ComputeLimitIx(1), c.StageLeafIx(solana.PublicKey{}, solana.PublicKey{}, make([]byte, 900))}); !ok || n != 900 {
		t.Fatalf("a leaf chunk past the budget instruction: %d %v", n, ok)
	}
	if allVerified(st, []solana.Instruction{sol.ComputeLimitIx(1), c.StageLeafIx(solana.PublicKey{}, solana.PublicKey{}, nil)}) {
		t.Fatal("a leaf transaction taken for a verify")
	}
	_ = fmt.Sprint(st)
}

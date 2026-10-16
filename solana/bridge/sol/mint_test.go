package sol

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// TestMintSupply: the supply read from a mint's bytes for both token programs (Token-2022's with extensions after
// the base), refusing another owner, a short account and an uninitialized mint.
func TestMintSupply(t *testing.T) {
	mint := func(supply uint64, size int, init byte) []byte {
		d := make([]byte, size)
		binary.LittleEndian.PutUint32(d[0:4], 1) // a mint authority
		binary.LittleEndian.PutUint64(d[36:44], supply)
		d[44] = 6
		d[45] = init
		return d
	}
	if n, err := MintSupply(Token2022, mint(1_234_567_890, 82+83+40, 1)); err != nil || n != 1_234_567_890 {
		t.Errorf("token-2022 with extensions: %d %v", n, err)
	}
	if n, err := MintSupply(TokenProgram, mint(42, 82, 1)); err != nil || n != 42 {
		t.Errorf("classic: %d %v", n, err)
	}
	if _, err := MintSupply(solana.SystemProgramID, mint(1, 82, 1)); err == nil || !strings.Contains(err.Error(), "not a token program") {
		t.Errorf("another owner: %v", err)
	}
	if _, err := MintSupply(Token2022, mint(1, 81, 1)[:81]); err == nil {
		t.Error("a short account was read")
	}
	if _, err := MintSupply(Token2022, mint(1, 82, 0)); err == nil {
		t.Error("an uninitialized mint was read")
	}
}

// tenMax is a provider that blocks a batch above ten, as solana-rpc.publicnode.com does.
type tenMax struct{ calls int }

func (f *tenMax) GetMultipleAccountsWithOpts(ctx context.Context, keys []solana.PublicKey, opts *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error) {
	f.calls++
	if len(keys) > 10 {
		return nil, errors.New("Request blocked")
	}
	out := &rpc.GetMultipleAccountsResult{RPCContext: rpc.RPCContext{Context: rpc.Context{Slot: uint64(1000 - f.calls)}}}
	for _, k := range keys {
		out.Value = append(out.Value, &rpc.Account{Owner: k})
	}
	return out, nil
}

// TestGetMultipleChunked: 23 accounts read in three calls of at most ten, in order, at the lowest slot answered.
func TestGetMultipleChunked(t *testing.T) {
	var keys []solana.PublicKey
	for i := 0; i < 23; i++ {
		keys = append(keys, solana.NewWallet().PublicKey())
	}
	f := &tenMax{}
	if _, err := f.GetMultipleAccountsWithOpts(context.Background(), keys, nil); err == nil {
		t.Fatal("the fake does not block a batch above ten")
	}
	f.calls = 0
	res, err := GetMultipleChunked(context.Background(), f, keys, nil)
	if err != nil || f.calls != 3 || len(res.Value) != 23 || res.Context.Slot != 997 {
		t.Fatalf("chunked: %v calls %d values %d slot %d", err, f.calls, len(res.Value), res.Context.Slot)
	}
	for i, k := range keys {
		if !res.Value[i].Owner.Equals(k) {
			t.Fatalf("value %d out of order", i)
		}
	}
	// each account with the slot of the chunk that read it (the fake answers its calls at 999, 998, 997)
	f.calls = 0
	_, slots, err := GetMultipleChunkedAt(context.Background(), f, keys, nil)
	if err != nil || len(slots) != 23 {
		t.Fatalf("chunked with slots: %v, %d slots", err, len(slots))
	}
	for i, s := range slots {
		if want := uint64(999 - i/10); s != want {
			t.Fatalf("account %d read at %d, its chunk answered at %d", i, s, want)
		}
	}
}

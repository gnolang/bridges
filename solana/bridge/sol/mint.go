package sol

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// TokenProgram is the classic SPL Token program; Hyperlane's synthetic mints are Token-2022's, whose mint keeps the
// same base layout.
var TokenProgram = solana.TokenProgramID

// MintSupply reads a token's supply from its mint account: the base mint layout both token programs share
// (mint authority COption 36 bytes, the supply u64 little-endian at 36, decimals at 44, is_initialized at 45,
// freeze authority COption; 82 bytes, Token-2022's extensions after them). The bridge reads the supply this way
// rather than by getTokenSupply, which a public provider may refuse as an "indexed" call (the mainnet launch,
// 2026-10-14: solana-rpc.publicnode.com did) while it serves every account read.
func MintSupply(owner solana.PublicKey, data []byte) (uint64, error) {
	if !owner.Equals(Token2022) && !owner.Equals(TokenProgram) {
		return 0, fmt.Errorf("the mint is owned by %s, not a token program", owner)
	}
	if len(data) < 82 {
		return 0, fmt.Errorf("the mint is %d bytes, under the 82 of a mint", len(data))
	}
	if data[45] != 1 {
		return 0, errors.New("the mint is not initialized")
	}
	return binary.LittleEndian.Uint64(data[36:44]), nil
}

// MintReader is the account read MintSupplyAt needs (an *rpc.Client, or a fake).
type MintReader interface {
	GetAccountInfoWithOpts(ctx context.Context, account solana.PublicKey, opts *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error)
}

// MintSupplyAt reads a mint's supply at the commitment given, with the slot the provider answered at.
func MintSupplyAt(ctx context.Context, c MintReader, mint solana.PublicKey, commitment rpc.CommitmentType) (uint64, uint64, error) {
	res, err := c.GetAccountInfoWithOpts(ctx, mint, &rpc.GetAccountInfoOpts{Encoding: solana.EncodingBase64, Commitment: commitment})
	if err != nil {
		return 0, 0, err
	}
	if res == nil || res.Value == nil {
		return 0, 0, errors.New("no mint account")
	}
	n, err := MintSupply(res.Value.Owner, res.Value.Data.GetBinary())
	if err != nil {
		return 0, 0, err
	}
	return n, res.Context.Slot, nil
}

// MultiMax is the most accounts one getMultipleAccounts asks for: a public provider blocks more than ten
// ("Request blocked", solana-rpc.publicnode.com, the mainnet launch 2026-10-14), so every batched read is chunked
// by ten (§5.7 said a hundred, the RPC's own ceiling).
const MultiMax = 10

// MultiReader is the batched account read (an *rpc.Client, a provider pair, or a fake).
type MultiReader interface {
	GetMultipleAccountsWithOpts(ctx context.Context, accounts []solana.PublicKey, opts *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error)
}

// GetMultipleChunked reads the accounts MultiMax at a time and answers as one getMultipleAccounts would: the values
// in the keys' order, the context slot the lowest any chunk answered at.
func GetMultipleChunked(ctx context.Context, c MultiReader, keys []solana.PublicKey, opts *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error) {
	out, _, err := GetMultipleChunkedAt(ctx, c, keys, opts)
	return out, err
}

// GetMultipleChunkedAt is GetMultipleChunked with the slot each account was read at: one getMultipleAccounts answers
// every account it holds from one bank, at one slot, but two chunks are two calls, and the finalized root moves
// between them (E4-SF1: a reading that holds accounts read at two slots against one slot counts what moved between
// them twice, or not at all). slots[i] is the context slot of the chunk that read keys[i].
func GetMultipleChunkedAt(ctx context.Context, c MultiReader, keys []solana.PublicKey, opts *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, []uint64, error) {
	out := &rpc.GetMultipleAccountsResult{Value: make([]*rpc.Account, 0, len(keys))}
	slots := make([]uint64, 0, len(keys))
	for start := 0; start < len(keys); start += MultiMax {
		end := min(start+MultiMax, len(keys))
		res, err := c.GetMultipleAccountsWithOpts(ctx, keys[start:end], opts)
		if err != nil {
			return nil, nil, err
		}
		if res == nil || len(res.Value) != end-start {
			return nil, nil, fmt.Errorf("getMultipleAccounts answered %d of %d accounts", lenValues(res), end-start)
		}
		out.Value = append(out.Value, res.Value...)
		for range res.Value {
			slots = append(slots, res.Context.Slot)
		}
		if out.Context.Slot == 0 || res.Context.Slot < out.Context.Slot {
			out.Context.Slot = res.Context.Slot
		}
	}
	return out, slots, nil
}

func lenValues(r *rpc.GetMultipleAccountsResult) int {
	if r == nil {
		return 0
	}
	return len(r.Value)
}

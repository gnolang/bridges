package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/sol"
)

// solReader is the read side of Solana as the machines use it: one provider (*rpc.Client), or two that must
// agree (pair). A reading a flag rests on — the watch's and hlwatch's — goes through a pair (§5.6, §5.9: "two
// providers of its own", "from two agreeing providers"); the relayer, which decides nothing a lying provider could
// cash, reads one.
type solReader interface {
	GetAccountInfoWithOpts(ctx context.Context, account solana.PublicKey, opts *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error)
	GetMultipleAccountsWithOpts(ctx context.Context, accounts []solana.PublicKey, opts *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error)
	GetTransaction(ctx context.Context, sig solana.Signature, opts *rpc.GetTransactionOpts) (*rpc.GetTransactionResult, error)
	GetSignaturesForAddressWithOpts(ctx context.Context, account solana.PublicKey, opts *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error)
}

// errProviders is a reading the two providers did not give alike: no verdict rests on it, and no flag.
var errProviders = errors.New("the providers disagree")

// ErrProviders is errProviders for the commands: hlwatch marks a section UNCONFIRMED on it (G26).
var ErrProviders = errProviders

// SolReader is the read side of Solana for the commands (hlwatch reads a kind 8's and 9's programs and the
// holder's seat through the pair, G26).
type SolReader = solReader

// pair reads through two providers and answers only what both say, byte for byte: an account's owner and data,
// a transaction's bytes, slot, failure and inner instructions. A listing of signatures comes from the first
// provider alone (it is a cursor; every transaction it names is then read from both).
type pair struct{ a, b *rpc.Client }

func twoProviders(c [2]*rpc.Client) pair { return pair{c[0], c[1]} }

// TwoProviders is the pair for the commands (G26).
func TwoProviders(c [2]*rpc.Client) SolReader { return twoProviders(c) }

func sameAccount(x, y *rpc.Account) bool {
	if x == nil || y == nil {
		return x == nil && y == nil
	}
	return x.Owner.Equals(y.Owner) && bytes.Equal(x.Data.GetBinary(), y.Data.GetBinary())
}

func (p pair) GetAccountInfoWithOpts(ctx context.Context, k solana.PublicKey, opts *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error) {
	ra, ea := p.a.GetAccountInfoWithOpts(ctx, k, opts)
	rb, eb := p.b.GetAccountInfoWithOpts(ctx, k, opts)
	na, nb := errors.Is(ea, rpc.ErrNotFound), errors.Is(eb, rpc.ErrNotFound)
	switch {
	case na && nb:
		return nil, rpc.ErrNotFound
	case ea != nil && !na:
		return nil, fmt.Errorf("provider 1: %w", ea)
	case eb != nil && !nb:
		return nil, fmt.Errorf("provider 2: %w", eb)
	case na != nb:
		return nil, fmt.Errorf("%w on account %s: one provider has it, the other not", errProviders, k)
	}
	if !sameAccount(ra.Value, rb.Value) {
		return nil, fmt.Errorf("%w on account %s", errProviders, k)
	}
	if rb.Context.Slot < ra.Context.Slot {
		ra.Context.Slot = rb.Context.Slot
	}
	return ra, nil
}

func (p pair) GetMultipleAccountsWithOpts(ctx context.Context, keys []solana.PublicKey, opts *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error) {
	ra, err := sol.GetMultipleChunked(ctx, p.a, keys, opts) // ten at a time: a public provider blocks more
	if err != nil {
		return nil, fmt.Errorf("provider 1: %w", err)
	}
	rb, err := sol.GetMultipleChunked(ctx, p.b, keys, opts)
	if err != nil {
		return nil, fmt.Errorf("provider 2: %w", err)
	}
	if len(ra.Value) != len(rb.Value) {
		return nil, fmt.Errorf("%w on %d accounts (%d and %d answered)", errProviders, len(keys), len(ra.Value), len(rb.Value))
	}
	for i := range ra.Value {
		if !sameAccount(ra.Value[i], rb.Value[i]) {
			return nil, fmt.Errorf("%w on account %s", errProviders, keys[i])
		}
	}
	if rb.Context.Slot < ra.Context.Slot {
		ra.Context.Slot = rb.Context.Slot
	}
	return ra, nil
}

// GetProgramAccountsWithOpts lists a program's accounts as both providers give them: the same keys with the same
// owner and data, in key order (G37: the checkpoint's stage seed reads through the pair).
func (p pair) GetProgramAccountsWithOpts(ctx context.Context, program solana.PublicKey, opts *rpc.GetProgramAccountsOpts) (rpc.GetProgramAccountsResult, error) {
	ra, err := p.a.GetProgramAccountsWithOpts(ctx, program, opts)
	if err != nil {
		return nil, fmt.Errorf("provider 1: %w", err)
	}
	rb, err := p.b.GetProgramAccountsWithOpts(ctx, program, opts)
	if err != nil {
		return nil, fmt.Errorf("provider 2: %w", err)
	}
	byKey := func(r rpc.GetProgramAccountsResult) rpc.GetProgramAccountsResult {
		out := append(rpc.GetProgramAccountsResult{}, r...)
		sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Pubkey[:], out[j].Pubkey[:]) < 0 })
		return out
	}
	ra, rb = byKey(ra), byKey(rb)
	if len(ra) != len(rb) {
		return nil, fmt.Errorf("%w on the accounts of %s (%d and %d listed)", errProviders, program, len(ra), len(rb))
	}
	for i := range ra {
		if !ra[i].Pubkey.Equals(rb[i].Pubkey) || !sameAccount(ra[i].Account, rb[i].Account) {
			return nil, fmt.Errorf("%w on the accounts of %s (at %s)", errProviders, program, ra[i].Pubkey)
		}
	}
	return ra, nil
}

// txFingerprint is what two providers must agree on about a transaction: its slot, whether it failed, its
// bytes, the addresses it loaded and the inner instructions its meta records.
func txFingerprint(r *rpc.GetTransactionResult) ([]byte, error) {
	if r == nil {
		return []byte("none"), nil
	}
	var txb []byte
	var err error
	if r.Transaction != nil {
		txb, err = r.Transaction.MarshalJSON()
		if err != nil {
			return nil, err
		}
	}
	var failed any
	var inner any
	var loaded any
	if r.Meta != nil {
		failed, inner, loaded = r.Meta.Err, r.Meta.InnerInstructions, r.Meta.LoadedAddresses
	}
	return json.Marshal(map[string]any{"slot": r.Slot, "failed": failed, "tx": json.RawMessage(txb), "inner": inner, "loaded": loaded})
}

func (p pair) GetTransaction(ctx context.Context, sig solana.Signature, opts *rpc.GetTransactionOpts) (*rpc.GetTransactionResult, error) {
	ra, err := p.a.GetTransaction(ctx, sig, opts)
	if err != nil {
		return nil, fmt.Errorf("provider 1: %w", err)
	}
	rb, err := p.b.GetTransaction(ctx, sig, opts)
	if err != nil {
		return nil, fmt.Errorf("provider 2: %w", err)
	}
	fa, err := txFingerprint(ra)
	if err != nil {
		return nil, err
	}
	fb, err := txFingerprint(rb)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(fa, fb) {
		return nil, fmt.Errorf("%w on transaction %s", errProviders, sig)
	}
	return ra, nil
}

func (p pair) GetSignaturesForAddressWithOpts(ctx context.Context, k solana.PublicKey, opts *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error) {
	return p.a.GetSignaturesForAddressWithOpts(ctx, k, opts)
}

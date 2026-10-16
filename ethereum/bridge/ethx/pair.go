package ethx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// The "two providers must agree" readings (eth/bridge/PLAN.md §5.6, §5.9): a signer and a monitor read Ethereum
// through two providers of their own and act only on what both answer alike — the finalized block's number and
// hash, every log of a range (each one's block hash checked against the canonical hash at its number), an eth_call
// pinned by block hash, a contract's code hash, a storage slot. A disagreement is ErrDisagree: no verdict rests on
// it and no flag (the relayer and the updater, which decide nothing a lying provider could cash, read one).

// ErrDisagree is a reading the two providers did not give alike.
var ErrDisagree = errors.New("the providers disagree")

// ErrFinalizedApart is the disagreement of two finalized answers by their distance alone (FinalizedWithin; errors.Is
// matches ErrDisagree too): a stuck or lying provider, or two honest ones read seconds apart just as the finalized
// block jumps several epochs at once (finality resuming after a delay).
var ErrFinalizedApart = errors.New("finalized answers too far apart")

// Pair is two providers that must agree.
type Pair struct{ A, B *Client }

// NewPair pairs two clients; the same provider twice is refused unless allowSame (a local run's one anvil).
func NewPair(a, b *Client, allowSame bool) (Pair, error) {
	if a == nil || b == nil {
		return Pair{}, errors.New("a pair needs two providers")
	}
	if a.Provider.URL == b.Provider.URL && !allowSame {
		return Pair{}, fmt.Errorf("the pair names one provider twice (%s): two providers that must agree cannot be one", a.Provider)
	}
	return Pair{A: a, B: b}, nil
}

func (p Pair) both() [2]*Client { return [2]*Client{p.A, p.B} }

// Finalized is the finalized block both providers agree on: the lower of their two finalized numbers, with the
// same hash from both at it (a provider a block ahead is no disagreement; two hashes at one finalized number are) —
// and the same time and parent: a block's JSON hash is not recomputed from its header here, so its timestamp is bound to
// nothing but the other provider's word, and the machines judge by it (the Go trust path's fold F15b: a credit the
// burn index cannot place waits until this block's time passes the credit's, E4-GT1 — one provider's time would hold
// a forgery unjudged, or call an honest credit forged).
func (p Pair) Finalized(ctx context.Context) (*Block, error) { return p.FinalizedWithin(ctx, 0) }

// FinalizedApart is how far apart two providers' finalized answers may lie for the machines that judge by them (the
// signers, the monitors, hlwatch): two epochs. Ethereum finalizes an epoch (32 blocks) at a time and two honest
// providers' finalized tags differ by an epoch at most for the seconds one updates later; one answering a block two
// epochs or more below the other's stands still (a stuck backend, or a liar answering an old but real block), and the
// pair's lower answer would blind every rule read at it while nothing disagrees (the plan red team's E5-RT1).
const FinalizedApart = 64

// FinalizedWithin is Finalized held to two answers at most apart blocks apart (0: any): farther apart is a disagreement,
// no reading.
func (p Pair) FinalizedWithin(ctx context.Context, apart uint64) (*Block, error) {
	fa, err := p.A.BlockAt(ctx, Finalized)
	if err != nil {
		return nil, err
	}
	fb, err := p.B.BlockAt(ctx, Finalized)
	if err != nil {
		return nil, err
	}
	low, high, highC := fa, fb, p.B
	if fb.Number < fa.Number {
		low, high, highC = fb, fa, p.A
	}
	if apart > 0 && high.Number-low.Number > apart {
		return nil, fmt.Errorf("%w on the finalized block (%w): %s answers %d and %s %d, %d blocks apart (more than %d): one provider's finalized block stands still or runs ahead, and the lower answer would blind every rule read at it (E5-RT1)",
			ErrDisagree, ErrFinalizedApart, p.A.Provider.Name, fa.Number, p.B.Provider.Name, fb.Number, high.Number-low.Number, apart)
	}
	if low.Number == high.Number {
		if !sameBlock(low, high) {
			return nil, fmt.Errorf("%w on the finalized block %d: %s at %d against %s at %d", ErrDisagree, low.Number, fa.Hash.Hex(), fa.Time, fb.Hash.Hex(), fb.Time)
		}
		return low, nil
	}
	at, err := highC.BlockAt(ctx, AtNumber(low.Number))
	if err != nil {
		return nil, err
	}
	if !sameBlock(low, at) {
		return nil, fmt.Errorf("%w on the finalized block %d: %s at %d against %s at %d", ErrDisagree, low.Number, low.Hash.Hex(), low.Time, at.Hash.Hex(), at.Time)
	}
	return low, nil
}

// sameBlock says whether two providers gave one block alike: its hash, its time and its parent.
func sameBlock(a, b *Block) bool {
	return a.Hash == b.Hash && a.Time == b.Time && a.ParentHash == b.ParentHash
}

// CanonicalHash is the hash both providers give the block at a number (at or below an agreed finalized block, the
// canonical one for good).
func (p Pair) CanonicalHash(ctx context.Context, n uint64) (common.Hash, error) {
	a, err := p.A.BlockAt(ctx, AtNumber(n))
	if err != nil {
		return common.Hash{}, err
	}
	b, err := p.B.BlockAt(ctx, AtNumber(n))
	if err != nil {
		return common.Hash{}, err
	}
	if a.Hash != b.Hash {
		return common.Hash{}, fmt.Errorf("%w on block %d: %s against %s", ErrDisagree, n, a.Hash.Hex(), b.Hash.Hex())
	}
	return a.Hash, nil
}

// CallAt is an eth_call pinned by block hash, the same bytes from both (or the same revert).
func (p Pair) CallAt(ctx context.Context, to common.Address, data []byte, at At) ([]byte, error) {
	if at.Tag != "" {
		return nil, fmt.Errorf("a pair reads pinned by block hash or number, not %q", at.Tag)
	}
	ra, ea := p.A.CallAt(ctx, to, data, at)
	rb, eb := p.B.CallAt(ctx, to, data, at)
	va, aRev := AsRevert(ea)
	vb, bRev := AsRevert(eb)
	switch {
	case aRev && bRev:
		if !bytes.Equal(va.Data, vb.Data) {
			return nil, fmt.Errorf("%w on a reverting call to %s at %s", ErrDisagree, to.Hex(), at)
		}
		return nil, ea
	case ea != nil && !aRev:
		return nil, ea
	case eb != nil && !bRev:
		return nil, eb
	case aRev != bRev:
		return nil, fmt.Errorf("%w on a call to %s at %s: one reverts, the other answers", ErrDisagree, to.Hex(), at)
	}
	if !bytes.Equal(ra, rb) {
		return nil, fmt.Errorf("%w on a call to %s at %s", ErrDisagree, to.Hex(), at)
	}
	return ra, nil
}

// BatchSize is the most calls one batch through the pair carries: the smaller of its two providers' (E5-GS4).
func (p Pair) BatchSize() int { return min(p.A.BatchSize(), p.B.BatchSize()) }

// CallBatch reads several views at one pinned state through both providers; every answer must agree.
func (p Pair) CallBatch(ctx context.Context, reqs []CallReq, at At) ([][]byte, []error, error) {
	if at.Tag != "" {
		return nil, nil, fmt.Errorf("a pair reads pinned by block hash or number, not %q", at.Tag)
	}
	ra, ea, err := p.A.CallBatch(ctx, reqs, at)
	if err != nil {
		return nil, nil, err
	}
	rb, eb, err := p.B.CallBatch(ctx, reqs, at)
	if err != nil {
		return nil, nil, err
	}
	errs := make([]error, len(reqs))
	for i := range reqs {
		va, aRev := AsRevert(ea[i])
		vb, bRev := AsRevert(eb[i])
		switch {
		case ea[i] != nil && !aRev:
			errs[i] = ea[i]
		case eb[i] != nil && !bRev:
			errs[i] = eb[i]
		case aRev != bRev, aRev && !bytes.Equal(va.Data, vb.Data):
			errs[i] = fmt.Errorf("%w on a call to %s at %s", ErrDisagree, reqs[i].To.Hex(), at)
		case aRev:
			errs[i] = ea[i]
		case !bytes.Equal(ra[i], rb[i]):
			errs[i] = fmt.Errorf("%w on a call to %s at %s", ErrDisagree, reqs[i].To.Hex(), at)
		}
	}
	return ra, errs, nil
}

// CodeHashAt is the keccak256 of an address's runtime code at a pinned state, both agreeing (the hash of empty
// code for an account without any).
func (p Pair) CodeHashAt(ctx context.Context, addr common.Address, at At) (common.Hash, error) {
	ca, err := p.A.CodeAt(ctx, addr, at)
	if err != nil {
		return common.Hash{}, err
	}
	cb, err := p.B.CodeAt(ctx, addr, at)
	if err != nil {
		return common.Hash{}, err
	}
	if !bytes.Equal(ca, cb) {
		return common.Hash{}, fmt.Errorf("%w on the code of %s at %s", ErrDisagree, addr.Hex(), at)
	}
	return crypto.Keccak256Hash(ca), nil
}

// StorageAt is a storage slot at a pinned state, both agreeing.
func (p Pair) StorageAt(ctx context.Context, addr common.Address, slot common.Hash, at At) (common.Hash, error) {
	a, err := p.A.StorageAt(ctx, addr, slot, at)
	if err != nil {
		return common.Hash{}, err
	}
	b, err := p.B.StorageAt(ctx, addr, slot, at)
	if err != nil {
		return common.Hash{}, err
	}
	if a != b {
		return common.Hash{}, fmt.Errorf("%w on slot %s of %s at %s", ErrDisagree, slot.Hex(), addr.Hex(), at)
	}
	return a, nil
}

// EIP-1967's slots: keccak256("eip1967.proxy.implementation") − 1 and keccak256("eip1967.proxy.admin") − 1.
var (
	SlotImplementation = common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
	SlotAdmin          = common.HexToHash("0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103")
)

// ImplementationAt is a proxy's implementation (EIP-1967) at a pinned state, both agreeing.
func (p Pair) ImplementationAt(ctx context.Context, proxy common.Address, at At) (common.Address, error) {
	w, err := p.StorageAt(ctx, proxy, SlotImplementation, at)
	if err != nil {
		return common.Address{}, err
	}
	return common.BytesToAddress(w[12:]), nil
}

// ProxyAdminAt is a proxy's admin (EIP-1967) at a pinned state, both agreeing.
func (p Pair) ProxyAdminAt(ctx context.Context, proxy common.Address, at At) (common.Address, error) {
	w, err := p.StorageAt(ctx, proxy, SlotAdmin, at)
	if err != nil {
		return common.Address{}, err
	}
	return common.BytesToAddress(w[12:]), nil
}

// Logs reads a range's logs from both providers, each in its own chunks (Provider.LogRange), and answers them only
// when both gave the same logs byte for byte — address, topics, data, block number and hash, transaction hash and
// index, log index — and each log's block hash is the canonical hash both give for its number. to must be at or
// below an agreed finalized block (the caller's snapshot), so canonical is final. One provider's empty answer where
// the other has logs is a disagreement, never an empty range (a provider has answered a busy window with no logs).
func (p Pair) Logs(ctx context.Context, q LogQuery, from, to uint64) ([]types.Log, error) {
	if q.BlockHash != nil {
		return nil, errors.New("a pair's log range is by numbers, checked against the canonical hashes")
	}
	if to < from {
		return nil, nil
	}
	la, err := p.A.LogsRange(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	lb, err := p.B.LogsRange(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	sortLogs(la)
	sortLogs(lb)
	if len(la) != len(lb) {
		return nil, fmt.Errorf("%w on the logs of blocks %d–%d: %d against %d", ErrDisagree, from, to, len(la), len(lb))
	}
	hashes := map[uint64]common.Hash{}
	for i := range la {
		if !sameLog(&la[i], &lb[i]) {
			return nil, fmt.Errorf("%w on a log of block %d (index %d)", ErrDisagree, la[i].BlockNumber, la[i].Index)
		}
		n := la[i].BlockNumber
		if n < from || n > to {
			return nil, fmt.Errorf("a log of block %d outside the range %d–%d", n, from, to)
		}
		h, ok := hashes[n]
		if !ok {
			if h, err = p.CanonicalHash(ctx, n); err != nil {
				return nil, err
			}
			hashes[n] = h
		}
		if la[i].BlockHash != h {
			return nil, fmt.Errorf("a log of block %d carries block hash %s, the canonical hash is %s", n, la[i].BlockHash.Hex(), h.Hex())
		}
	}
	return la, nil
}

func sortLogs(ls []types.Log) {
	sort.Slice(ls, func(i, j int) bool {
		if ls[i].BlockNumber != ls[j].BlockNumber {
			return ls[i].BlockNumber < ls[j].BlockNumber
		}
		return ls[i].Index < ls[j].Index
	})
}

func sameLog(a, b *types.Log) bool {
	if a.Address != b.Address || !bytes.Equal(a.Data, b.Data) || a.BlockNumber != b.BlockNumber || a.BlockHash != b.BlockHash ||
		a.TxHash != b.TxHash || a.TxIndex != b.TxIndex || a.Index != b.Index || a.Removed != b.Removed || len(a.Topics) != len(b.Topics) {
		return false
	}
	for i := range a.Topics {
		if a.Topics[i] != b.Topics[i] {
			return false
		}
	}
	return true
}

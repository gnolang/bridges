// Package ethx is the bridge's Ethereum client (eth/bridge/PLAN.md §5.6–§5.8), over go-ethereum's RPC client: the
// providers by name and URL, every URL redacted before it is logged or served; reads pinned by block hash
// (EIP-1898) and the "two providers must agree" helpers the signers and monitors use (pair.go); a transaction
// sender for one secp256k1 key file (sender.go); the ABIs of our three contracts and of the Hyperlane and
// OpenZeppelin contracts the bridge touches (abi.go); the calldata builders from gnolight (calldata.go); typed
// readers of the contracts' state (contracts.go); revert reasons decoded (revert.go).
package ethx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

// RPCTimeout bounds every read and send (G12: one stalled endpoint must not hold a machine's loop).
const RPCTimeout = 30 * time.Second

// Provider is one RPC provider by a name and its URL. The URL carries the provider's key: it is never logged or
// served whole — String, every error and every status say the name and the redacted URL. LogRange is how many
// blocks one eth_getLogs of it may span (providers differ: notes/research-ethereum.md §5 measured publicnode at
// 20,000 results, drpc at about 100 blocks, blastapi at 10, nodies and 1rpc at 50, blockrazor at 25); 0 is
// DefaultLogRange, and a chunk the provider refuses as too wide is halved until it answers. BatchSize is the most
// calls one JSON-RPC batch to it carries (0: DefaultBatchSize), halved for a batch it answers as too large.
type Provider struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	LogRange  uint64 `json:"logRange,omitempty"`
	BatchSize int    `json:"batchSize,omitempty"`
}

// DefaultLogRange is the blocks one eth_getLogs spans when the provider's config names none.
const DefaultLogRange = 1_000

// DefaultBatchSize is the most calls one JSON-RPC batch carries when the provider's config names none (E5-GS4): geth
// answers a batch past its --rpc.batch-request-limit (1,000 by default) with one "batch too large" error and every
// other element missing, and hosted providers cap batches too — a read of every waiting message's events in one batch
// failed whole past the cap, every step.
const DefaultBatchSize = 100

func (p Provider) String() string {
	if p.Name == "" {
		return Redact(p.URL)
	}
	return p.Name + " (" + Redact(p.URL) + ")"
}

// ProviderError is a provider's failure, named and redacted; the cause stays reachable (errors.As finds an RPC
// error's data, errors.Is a not-found).
type ProviderError struct {
	Provider string
	Err      error
}

func (e *ProviderError) Error() string { return e.Provider + ": " + Redact(e.Err.Error()) }
func (e *ProviderError) Unwrap() error { return e.Err }

// ErrNotFound is a block, transaction or receipt the provider does not have.
var ErrNotFound = errors.New("not found")

// Client reads one provider and sends raw transactions through it.
type Client struct {
	Provider Provider
	rpc      rpcCaller
	// batchCap is the most calls a batch of this provider was answered whole at, once one was refused as too large (0:
	// none refused yet)
	batchCap atomic.Int64
}

// rpcCaller is go-ethereum's *rpc.Client as this package uses it (a fake in tests).
type rpcCaller interface {
	CallContext(ctx context.Context, result any, method string, args ...any) error
	BatchCallContext(ctx context.Context, b []rpc.BatchElem) error
	Close()
}

// Dial connects to one provider (http(s) or ws(s)); every request ends within RPCTimeout.
func Dial(ctx context.Context, p Provider) (*Client, error) {
	if p.URL == "" {
		return nil, fmt.Errorf("provider %q has no URL", p.Name)
	}
	c, err := rpc.DialOptions(ctx, p.URL, rpc.WithHTTPClient(&http.Client{Timeout: RPCTimeout}))
	if err != nil {
		return nil, &ProviderError{Provider: p.String(), Err: err}
	}
	return &Client{Provider: p, rpc: c}, nil
}

// NewClient wraps an RPC caller (tests, and a caller that dialled already).
func NewClient(p Provider, c rpcCaller) *Client { return &Client{Provider: p, rpc: c} }

func (c *Client) Close() {
	if c.rpc != nil {
		c.rpc.Close()
	}
}

func (c *Client) String() string { return c.Provider.String() }

// call is one request, its error named and redacted, a revert's data kept (revert.go).
func (c *Client) call(ctx context.Context, result any, method string, args ...any) error {
	ctx, cancel := context.WithTimeout(ctx, RPCTimeout)
	defer cancel()
	if err := c.rpc.CallContext(ctx, result, method, args...); err != nil {
		return c.wrap(err)
	}
	return nil
}

func (c *Client) wrap(err error) error {
	if err == nil {
		return nil
	}
	if r := revertFromRPC(err); r != nil {
		return r
	}
	return &ProviderError{Provider: c.Provider.String(), Err: err}
}

// ------------------------------------------------------------------ blocks --

// At selects the state a read sees: a tag ("latest", "finalized", "safe", "pending"), a block number, or a block
// hash read with requireCanonical (EIP-1898: a provider answers for that block or refuses — never for another).
type At struct {
	Tag    string
	Number uint64
	Hash   common.Hash
	byNum  bool
}

var (
	Latest    = At{Tag: "latest"}
	Finalized = At{Tag: "finalized"}
	Safe      = At{Tag: "safe"}
	Pending   = At{Tag: "pending"}
)

// AtHash pins a read to a block by its hash (EIP-1898, requireCanonical).
func AtHash(h common.Hash) At { return At{Hash: h} }

// AtNumber pins a read to a block number.
func AtNumber(n uint64) At { return At{Number: n, byNum: true} }

func (a At) arg() any {
	switch {
	case a.Tag != "":
		return a.Tag
	case a.byNum:
		return hexutil.Uint64(a.Number).String()
	}
	return rpc.BlockNumberOrHashWithHash(a.Hash, true)
}

func (a At) String() string {
	switch {
	case a.Tag != "":
		return a.Tag
	case a.byNum:
		return fmt.Sprintf("block %d", a.Number)
	}
	return "block " + a.Hash.Hex()
}

// Block is a block's head as the machines use it: its number, hash, parent, time and base fee, taken from the
// provider's own fields (never recomputed from a header this build may not know the fork of).
type Block struct {
	Number     uint64
	Hash       common.Hash
	ParentHash common.Hash
	Time       uint64
	BaseFee    *big.Int // nil before London
	GasLimit   uint64   // 0 when the provider did not say
}

type rpcBlock struct {
	Number     *hexutil.Big   `json:"number"`
	Hash       *common.Hash   `json:"hash"`
	ParentHash common.Hash    `json:"parentHash"`
	Timestamp  hexutil.Uint64 `json:"timestamp"`
	BaseFee    *hexutil.Big   `json:"baseFeePerGas"`
	GasLimit   hexutil.Uint64 `json:"gasLimit"`
}

func (b *rpcBlock) block() (*Block, error) {
	if b.Number == nil || b.Hash == nil {
		return nil, errors.New("a block without a number or hash (a pending block?)")
	}
	out := &Block{Number: (*big.Int)(b.Number).Uint64(), Hash: *b.Hash, ParentHash: b.ParentHash, Time: uint64(b.Timestamp), GasLimit: uint64(b.GasLimit)}
	if b.BaseFee != nil {
		out.BaseFee = (*big.Int)(b.BaseFee)
	}
	return out, nil
}

// BlockAt is the block a tag or number names; ErrNotFound when the provider has none (a finalized block on a chain
// that never finalized, a number past the head).
func (c *Client) BlockAt(ctx context.Context, at At) (*Block, error) {
	var raw json.RawMessage
	var err error
	if !at.byNum && at.Tag == "" {
		err = c.call(ctx, &raw, "eth_getBlockByHash", at.Hash, false)
	} else {
		err = c.call(ctx, &raw, "eth_getBlockByNumber", at.arg(), false)
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, &ProviderError{Provider: c.Provider.String(), Err: fmt.Errorf("%s: %w", at, ErrNotFound)}
	}
	var b rpcBlock
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, c.wrap(err)
	}
	blk, err := b.block()
	if err != nil {
		return nil, c.wrap(fmt.Errorf("%s: %w", at, err))
	}
	if !at.byNum && at.Tag == "" && blk.Hash != at.Hash {
		return nil, c.wrap(fmt.Errorf("asked for block %s, served %s", at.Hash.Hex(), blk.Hash.Hex()))
	}
	if at.byNum && blk.Number != at.Number {
		return nil, c.wrap(fmt.Errorf("asked for block %d, served %d", at.Number, blk.Number))
	}
	return blk, nil
}

// ChainID is the provider's chain id (eth_chainId).
func (c *Client) ChainID(ctx context.Context) (*big.Int, error) {
	var id hexutil.Big
	if err := c.call(ctx, &id, "eth_chainId"); err != nil {
		return nil, err
	}
	return (*big.Int)(&id), nil
}

// ------------------------------------------------------------------- reads --

func callArg(from *common.Address, to common.Address, data []byte, value *big.Int) map[string]any {
	arg := map[string]any{"to": to, "data": hexutil.Bytes(data)}
	if from != nil {
		arg["from"] = *from
	}
	if value != nil && value.Sign() > 0 {
		arg["value"] = (*hexutil.Big)(value)
	}
	return arg
}

// CallAt is eth_call of a view at a state; a revert comes back as a *Revert.
func (c *Client) CallAt(ctx context.Context, to common.Address, data []byte, at At) ([]byte, error) {
	var out hexutil.Bytes
	if err := c.call(ctx, &out, "eth_call", callArg(nil, to, data, nil), at.arg()); err != nil {
		return nil, err
	}
	return out, nil
}

// CallFrom is eth_call as a sender (a function that reads msg.sender), with a value.
func (c *Client) CallFrom(ctx context.Context, from, to common.Address, data []byte, value *big.Int, at At) ([]byte, error) {
	var out hexutil.Bytes
	if err := c.call(ctx, &out, "eth_call", callArg(&from, to, data, value), at.arg()); err != nil {
		return nil, err
	}
	return out, nil
}

// CallReq is one view call of a batch.
type CallReq struct {
	To   common.Address
	Data []byte
}

// BatchSize is the most calls one JSON-RPC batch to this provider carries: Provider.BatchSize, else DefaultBatchSize —
// or less, once the provider refused a batch as too large (the size it then answered).
func (c *Client) BatchSize() int {
	n := DefaultBatchSize
	if c.Provider.BatchSize > 0 {
		n = c.Provider.BatchSize
	}
	if l := int(c.batchCap.Load()); l > 0 && l < n {
		n = l
	}
	return n
}

// CallBatch reads several views at one state in JSON-RPC batches of at most the provider's BatchSize; each answer or
// error is in the order asked. A batch the provider answers as too large — every element failed, one of them a missing
// response or the words (geth: "batch too large" on the first, ErrMissingBatchResponse on the rest) — is asked again
// in halves (E5-GS4). A batch the provider refuses whole is an error.
func (c *Client) CallBatch(ctx context.Context, reqs []CallReq, at At) ([][]byte, []error, error) {
	if len(reqs) == 0 {
		return nil, nil, nil
	}
	res, errs := make([][]byte, len(reqs)), make([]error, len(reqs))
	size := c.BatchSize()
	for lo := 0; lo < len(reqs); {
		hi := min(lo+size, len(reqs))
		r, e, err := c.callBatch(ctx, reqs[lo:hi], at)
		if err != nil {
			return nil, nil, err
		}
		if hi-lo > 1 && batchTooLarge(e) {
			size = max((hi-lo)/2, 1)
			c.batchCap.Store(int64(size)) // remembered: the next read starts there
			continue
		}
		copy(res[lo:hi], r)
		copy(errs[lo:hi], e)
		lo = hi
	}
	return res, errs, nil
}

// batchTooLarge says whether a batch's answers are a provider's refusal of its size: every element failed, and one
// of them for want of a response or with the words.
func batchTooLarge(errs []error) bool {
	why := false
	for _, err := range errs {
		if err == nil {
			return false
		}
		if errors.Is(err, rpc.ErrMissingBatchResponse) || lowerContains(err, "batch too large", "batch size", "batch limit", "too many requests in batch", "batch request limit") {
			why = true
		}
	}
	return why
}

// callBatch is one JSON-RPC batch.
func (c *Client) callBatch(ctx context.Context, reqs []CallReq, at At) ([][]byte, []error, error) {
	elems := make([]rpc.BatchElem, len(reqs))
	outs := make([]hexutil.Bytes, len(reqs))
	for i, r := range reqs {
		elems[i] = rpc.BatchElem{Method: "eth_call", Args: []any{callArg(nil, r.To, r.Data, nil), at.arg()}, Result: &outs[i]}
	}
	ctx, cancel := context.WithTimeout(ctx, RPCTimeout)
	defer cancel()
	if err := c.rpc.BatchCallContext(ctx, elems); err != nil {
		return nil, nil, c.wrap(err)
	}
	res := make([][]byte, len(reqs))
	errs := make([]error, len(reqs))
	for i := range elems {
		if elems[i].Error != nil {
			errs[i] = c.wrap(elems[i].Error)
			continue
		}
		res[i] = outs[i]
	}
	return res, errs, nil
}

// CodeAt is an address's runtime code at a state.
func (c *Client) CodeAt(ctx context.Context, addr common.Address, at At) ([]byte, error) {
	var out hexutil.Bytes
	if err := c.call(ctx, &out, "eth_getCode", addr, at.arg()); err != nil {
		return nil, err
	}
	return out, nil
}

// StorageAt is one storage slot at a state.
// CallOverride is eth_call from an address with storage words overridden for the call alone (the state override's
// stateDiff: every other word as the chain holds it) — the audit re-runs HlAdmin.finishSetup with setupDone false.
func (c *Client) CallOverride(ctx context.Context, from, to common.Address, data []byte, at At, words map[common.Address]map[common.Hash]common.Hash) ([]byte, error) {
	over := map[common.Address]map[string]map[common.Hash]common.Hash{}
	for a, ws := range words {
		over[a] = map[string]map[common.Hash]common.Hash{"stateDiff": ws}
	}
	var out hexutil.Bytes
	if err := c.call(ctx, &out, "eth_call", callArg(&from, to, data, nil), at.arg(), over); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) StorageAt(ctx context.Context, addr common.Address, slot common.Hash, at At) (common.Hash, error) {
	var out hexutil.Bytes
	if err := c.call(ctx, &out, "eth_getStorageAt", addr, slot, at.arg()); err != nil {
		return common.Hash{}, err
	}
	if len(out) > 32 {
		return common.Hash{}, c.wrap(fmt.Errorf("a storage word of %d bytes", len(out)))
	}
	return common.BytesToHash(out), nil
}

// BalanceAt is an address's wei at a state.
func (c *Client) BalanceAt(ctx context.Context, addr common.Address, at At) (*big.Int, error) {
	var out hexutil.Big
	if err := c.call(ctx, &out, "eth_getBalance", addr, at.arg()); err != nil {
		return nil, err
	}
	return (*big.Int)(&out), nil
}

// NonceAt is an address's transaction count at a state ("pending" counts the provider's mempool).
func (c *Client) NonceAt(ctx context.Context, addr common.Address, at At) (uint64, error) {
	var out hexutil.Uint64
	if err := c.call(ctx, &out, "eth_getTransactionCount", addr, at.arg()); err != nil {
		return 0, err
	}
	return uint64(out), nil
}

// LogQuery is an eth_getLogs filter over a block range (or one block by hash).
type LogQuery struct {
	Addresses []common.Address
	Topics    [][]common.Hash
	From, To  uint64
	BlockHash *common.Hash
}

func (q LogQuery) arg() map[string]any {
	arg := map[string]any{}
	if len(q.Addresses) > 0 {
		arg["address"] = q.Addresses
	}
	if len(q.Topics) > 0 {
		topics := make([]any, len(q.Topics))
		for i, t := range q.Topics {
			switch len(t) {
			case 0:
				topics[i] = nil
			case 1:
				topics[i] = t[0]
			default:
				topics[i] = t
			}
		}
		arg["topics"] = topics
	}
	if q.BlockHash != nil {
		arg["blockHash"] = *q.BlockHash
	} else {
		arg["fromBlock"] = hexutil.Uint64(q.From).String()
		arg["toBlock"] = hexutil.Uint64(q.To).String()
	}
	return arg
}

// Logs is eth_getLogs; a removed log (a reorganised one) is refused, never returned.
func (c *Client) Logs(ctx context.Context, q LogQuery) ([]types.Log, error) {
	var out []types.Log
	if err := c.call(ctx, &out, "eth_getLogs", q.arg()); err != nil {
		return nil, err
	}
	for _, l := range out {
		if l.Removed {
			return nil, c.wrap(fmt.Errorf("a removed log at block %d (a reorganisation under the range)", l.BlockNumber))
		}
	}
	return out, nil
}

// --------------------------------------------------------------- sending --

// EstimateGas is eth_estimateGas of a call from an address at the latest block — never "pending", some nodes' default
// (anvil's): a transaction pending at the key's nonce is about to be replaced by this one, and an estimate that ran
// after it would size the gas for a state that never comes (the backup taking the silent primary's nonce, §5.7); a
// revert comes back as a *Revert (nothing is sent).
func (c *Client) EstimateGas(ctx context.Context, from, to common.Address, data []byte, value *big.Int) (uint64, error) {
	var out hexutil.Uint64
	if err := c.call(ctx, &out, "eth_estimateGas", callArg(&from, to, data, value), "latest"); err != nil {
		return 0, err
	}
	return uint64(out), nil
}

// EstimateCreate is eth_estimateGas of a contract creation (a transaction with no "to": data is the creation code
// with its constructor's arguments); a constructor that would revert comes back as a *Revert (nothing is sent).
func (c *Client) EstimateCreate(ctx context.Context, from common.Address, data []byte, value *big.Int) (uint64, error) {
	var out hexutil.Uint64
	if err := c.call(ctx, &out, "eth_estimateGas", createArg(from, data, value), "latest"); err != nil {
		return 0, err
	}
	return uint64(out), nil
}

// CallCreate is eth_call of a contract creation at a state (a mined creation's revert is read so).
func (c *Client) CallCreate(ctx context.Context, from common.Address, data []byte, value *big.Int, at At) ([]byte, error) {
	var out hexutil.Bytes
	if err := c.call(ctx, &out, "eth_call", createArg(from, data, value), at.arg()); err != nil {
		return nil, err
	}
	return out, nil
}

func createArg(from common.Address, data []byte, value *big.Int) map[string]any {
	arg := map[string]any{"from": from, "data": hexutil.Bytes(data)}
	if value != nil && value.Sign() > 0 {
		arg["value"] = (*hexutil.Big)(value)
	}
	return arg
}

// PendingTx is a key's transaction waiting in a node's pool at a nonce, as the pool shows it: its hash and its two
// fees (a legacy transaction's gas price is both).
type PendingTx struct {
	Hash   common.Hash
	Nonce  uint64
	Tip    *big.Int
	FeeCap *big.Int
}

// pendingJSON is a transaction object as eth_getTransactionBySenderAndNonce and txpool_contentFrom give it.
type pendingJSON struct {
	Hash                 common.Hash     `json:"hash"`
	Nonce                hexutil.Uint64  `json:"nonce"`
	BlockNumber          *hexutil.Big    `json:"blockNumber"`
	GasPrice             *hexutil.Big    `json:"gasPrice"`
	MaxFeePerGas         *hexutil.Big    `json:"maxFeePerGas"`
	MaxPriorityFeePerGas *hexutil.Big    `json:"maxPriorityFeePerGas"`
	From                 *common.Address `json:"from"`
}

func (p *pendingJSON) tx() *PendingTx {
	out := &PendingTx{Hash: p.Hash, Nonce: uint64(p.Nonce)}
	switch {
	case p.MaxFeePerGas != nil && p.MaxPriorityFeePerGas != nil:
		out.Tip, out.FeeCap = new(big.Int).Set((*big.Int)(p.MaxPriorityFeePerGas)), new(big.Int).Set((*big.Int)(p.MaxFeePerGas))
	case p.GasPrice != nil:
		out.Tip, out.FeeCap = new(big.Int).Set((*big.Int)(p.GasPrice)), new(big.Int).Set((*big.Int)(p.GasPrice))
	default:
		return nil
	}
	return out
}

// PendingAt is a key's transaction waiting in the provider's pool at a nonce — another machine's that shares the key
// (§5.7: the backup relayer takes over a primary that went silent with a transaction pending) — read by
// eth_getTransactionBySenderAndNonce (reth, Erigon, anvil), else txpool_contentFrom (geth, anvil); nil when the pool
// holds none there, it is mined already, or the provider serves neither method (ErrNoPoolView).
func (c *Client) PendingAt(ctx context.Context, from common.Address, nonce uint64) (*PendingTx, error) {
	var raw json.RawMessage
	err := c.call(ctx, &raw, "eth_getTransactionBySenderAndNonce", from, hexutil.Uint64(nonce))
	if err == nil {
		if len(raw) == 0 || string(raw) == "null" {
			return nil, nil
		}
		var p pendingJSON
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, c.wrap(err)
		}
		if p.BlockNumber != nil || uint64(p.Nonce) != nonce || (p.From != nil && *p.From != from) {
			return nil, nil // mined already, or not the asked transaction
		}
		return p.tx(), nil
	}
	if !noSuchMethod(err) {
		return nil, err
	}
	var pool struct {
		Pending map[string]pendingJSON `json:"pending"`
	}
	if err := c.call(ctx, &pool, "txpool_contentFrom", from); err != nil {
		if noSuchMethod(err) {
			return nil, ErrNoPoolView
		}
		return nil, err
	}
	for k, p := range pool.Pending {
		// geth keys the pool by the nonce in decimal; a "0x" key is read too
		if n, err := strconv.ParseUint(k, 0, 64); err == nil && n == nonce {
			return p.tx(), nil
		}
	}
	return nil, nil
}

// ErrNoPoolView is a provider that shows no pending transaction by sender and nonce (neither
// eth_getTransactionBySenderAndNonce nor txpool_contentFrom): a replacement is then found by raising its fees until
// the pool takes it.
var ErrNoPoolView = errors.New("the provider shows no pending transaction by sender and nonce")

// noSuchMethod says whether an RPC error is a method the provider does not serve (JSON-RPC -32601, or the words).
func noSuchMethod(err error) bool {
	var re rpc.Error
	if errors.As(err, &re) && re.ErrorCode() == -32601 {
		return true
	}
	return lowerContains(err, "method not found", "does not exist", "not supported", "unsupported method", "is not available")
}

// SuggestTip is the provider's priority fee suggestion (eth_maxPriorityFeePerGas).
func (c *Client) SuggestTip(ctx context.Context) (*big.Int, error) {
	var out hexutil.Big
	if err := c.call(ctx, &out, "eth_maxPriorityFeePerGas"); err != nil {
		return nil, err
	}
	return (*big.Int)(&out), nil
}

// SendRaw broadcasts a signed transaction.
func (c *Client) SendRaw(ctx context.Context, raw []byte) (common.Hash, error) {
	var h common.Hash
	if err := c.call(ctx, &h, "eth_sendRawTransaction", hexutil.Bytes(raw)); err != nil {
		return common.Hash{}, err
	}
	return h, nil
}

// Receipt is a mined transaction's receipt; ErrNotFound while it is not mined.
func (c *Client) Receipt(ctx context.Context, h common.Hash) (*types.Receipt, error) {
	var raw json.RawMessage
	if err := c.call(ctx, &raw, "eth_getTransactionReceipt", h); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, &ProviderError{Provider: c.Provider.String(), Err: fmt.Errorf("receipt %s: %w", h.Hex(), ErrNotFound)}
	}
	var r types.Receipt
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, c.wrap(err)
	}
	return &r, nil
}

// TxInput is a transaction's sender-independent facts: its target and calldata (a staged update's resumption
// reads which slots an earlier call carried, updater.go).
type TxInput struct {
	To    *common.Address
	Input []byte
	Nonce uint64
	From  common.Address
}

// Transaction reads a transaction by hash.
func (c *Client) Transaction(ctx context.Context, h common.Hash) (*TxInput, error) {
	var raw json.RawMessage
	if err := c.call(ctx, &raw, "eth_getTransactionByHash", h); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, &ProviderError{Provider: c.Provider.String(), Err: fmt.Errorf("transaction %s: %w", h.Hex(), ErrNotFound)}
	}
	var t struct {
		To    *common.Address `json:"to"`
		Input hexutil.Bytes   `json:"input"`
		Nonce hexutil.Uint64  `json:"nonce"`
		From  common.Address  `json:"from"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, c.wrap(err)
	}
	return &TxInput{To: t.To, Input: t.Input, Nonce: uint64(t.Nonce), From: t.From}, nil
}

// SignedTransaction reads a transaction by hash and answers it as its signature makes it (the plan red team's E5-RT3):
// the provider's answer decoded as a signed transaction, which must hash to the hash asked, its sender recovered from
// its signature — never the provider's "from" field. A provider that lies can then name no other sender for a
// transaction, and no other calldata, than the one the hash commits to; it can still answer a transaction that is
// not in the chain, or not where its logs say (what a log names is the caller's to bind: the spend path holds the
// calldata to the header the log records).
func (c *Client) SignedTransaction(ctx context.Context, h common.Hash) (*TxInput, error) {
	var raw json.RawMessage
	if err := c.call(ctx, &raw, "eth_getTransactionByHash", h); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, &ProviderError{Provider: c.Provider.String(), Err: fmt.Errorf("transaction %s: %w", h.Hex(), ErrNotFound)}
	}
	return signedTx(h, raw, c.wrap)
}

// signedTx decodes a provider's answer for transaction h as a signed transaction (SignedTransaction).
func signedTx(h common.Hash, raw []byte, wrap func(error) error) (*TxInput, error) {
	var tx types.Transaction
	if err := tx.UnmarshalJSON(raw); err != nil {
		return nil, wrap(fmt.Errorf("transaction %s: not a signed transaction: %w", h.Hex(), err))
	}
	if got := tx.Hash(); got != h {
		return nil, wrap(fmt.Errorf("transaction %s: the provider's answer hashes to %s", h.Hex(), got.Hex()))
	}
	var signer types.Signer = types.HomesteadSigner{}
	if tx.Protected() {
		signer = types.LatestSignerForChainID(tx.ChainId())
	}
	from, err := types.Sender(signer, &tx)
	if err != nil {
		return nil, wrap(fmt.Errorf("transaction %s: its signer: %w", h.Hex(), err))
	}
	return &TxInput{To: tx.To(), Input: tx.Data(), Nonce: tx.Nonce(), From: from}, nil
}

// IsNotFound says whether an error is a provider's "none" (no such block, transaction or receipt).
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// ErrNoState is a read the provider cannot answer for that block: it keeps only the last ~100–128 blocks of state
// (publicnode, 1rpc, blockrazor at the `finalized` block, notes/research-ethereum.md §5). A reading that meets it is
// no verdict — never a zero, never a refusal of the call.
var ErrNoState = errors.New("the provider keeps no state for that block (no verdict)")

// noStateTexts are the providers' refusals of a block they keep no state or history for — publicnode's past its ~100
// blocks of state and its logs ~10,000 blocks back: HTTP 403, "Archive requests require a personal token. Get one at:
// https://www.allnodes.com/publicnode" (2026-10-14, the assumptions audit's E3-AS4). Halving a log range cannot cure
// it: the blocks are as old as they were, and a full-history provider is needed.
var noStateTexts = []string{"missing trie node", "header not found", "state not available", "state is not available", "historical state", "pruned", "unknown block", "state at block", "not available for block", "required historical state", "archive requests require"}

// IsNoState says whether a read failed for want of the block's state.
func IsNoState(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNoState) {
		return true
	}
	if _, ok := AsRevert(err); ok {
		return false
	}
	return lowerContains(err, noStateTexts...)
}

// limitTexts are a provider's refusals of an eth_getLogs too wide (its block range or its result count), matched in the
// error's text and in its data (an RPC error's ErrorData). Measured 2026-10-14 (the assumptions audit's E3-AS4): drpc's
// free plan refuses any range past ~100 blocks with HTTP 400, code 35, "ranges over 10000 blocks are not supported on
// free plan" (its text names 10,000; 1,001 blocks are refused); publicnode past 20,000 results with "query exceeds max
// results 20000, retry with the range …"; Tenderly with "invalid params" and the data "Query returned more than 20000
// results. Try with this block range […]".
var limitTexts = []string{"block range", "range is too large", "range too large", "more than", "too many", "limit exceeded", "exceed", "query timeout", "response size", "max results", "query returned", "ranges over", "not supported on free plan"}

// tooWide says whether an eth_getLogs refusal is the provider's range or result limit: its text, or the data an RPC
// error carries beside its message (Tenderly's message is "invalid params", the reason in its data).
func tooWide(err error) bool {
	if lowerContains(err, limitTexts...) {
		return true
	}
	var de rpc.DataError
	if errors.As(err, &de) {
		if d := de.ErrorData(); d != nil {
			return lowerContains(fmt.Errorf("%v", d), limitTexts...)
		}
	}
	return false
}

// LogsRange reads a range's logs in the provider's own chunks (LogRange), halving a chunk the provider refuses as
// too wide; a removed log is refused. An empty answer is one provider's word and is never taken alone where a
// verdict rests on it (a provider has answered a busy window with no logs and no error): the pair compares.
func (c *Client) LogsRange(ctx context.Context, q LogQuery, from, to uint64) ([]types.Log, error) {
	if q.BlockHash != nil {
		return c.Logs(ctx, q)
	}
	step := c.Provider.LogRange
	if step == 0 {
		step = DefaultLogRange
	}
	var out []types.Log
	for lo := from; lo <= to; {
		hi := lo + step - 1
		if hi > to || hi < lo {
			hi = to
		}
		cq := q
		cq.From, cq.To = lo, hi
		ls, err := c.Logs(ctx, cq)
		if err != nil {
			if tooWide(err) && !IsNoState(err) && hi > lo {
				step = max((hi-lo+1)/2, 1)
				continue
			}
			return nil, err
		}
		out = append(out, ls...)
		if hi == to {
			break
		}
		lo = hi + 1
	}
	return out, nil
}

// errText is an error's text, redacted, for notes and alarms.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return Redact(err.Error())
}

// lowerContains is a case-insensitive substring test over an error's text.
func lowerContains(err error, subs ...string) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

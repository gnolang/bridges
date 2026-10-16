package ethx

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

// fakeRPC is a provider in memory: a handler per method, every call logged.
type fakeRPC struct {
	mu       sync.Mutex
	handlers map[string]func(args []any) (any, error)
	calls    []string
	noBatch  bool
}

func newFakeRPC() *fakeRPC { return &fakeRPC{handlers: map[string]func([]any) (any, error){}} }

func (f *fakeRPC) on(method string, h func(args []any) (any, error)) { f.handlers[method] = h }

func (f *fakeRPC) CallContext(_ context.Context, result any, method string, args ...any) error {
	f.mu.Lock()
	f.calls = append(f.calls, method)
	h := f.handlers[method]
	f.mu.Unlock()
	if h == nil {
		return fmt.Errorf("fake: no handler for %s", method)
	}
	v, err := h(args)
	if err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, result)
}

func (f *fakeRPC) BatchCallContext(ctx context.Context, b []rpc.BatchElem) error {
	if f.noBatch {
		return fmt.Errorf("fake: batches refused")
	}
	for i := range b {
		b[i].Error = f.CallContext(ctx, b[i].Result, b[i].Method, b[i].Args...)
	}
	return nil
}

func (f *fakeRPC) Close() {}

func (f *fakeRPC) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

// dataErr is a JSON-RPC error with data, as geth answers a revert (code 3).
type dataErr struct {
	msg  string
	data string
}

func (e *dataErr) Error() string          { return e.msg }
func (e *dataErr) ErrorCode() int         { return 3 }
func (e *dataErr) ErrorData() interface{} { return e.data }

// revertWith is a revert carrying Error(string).
func revertWith(reason string) error {
	b, _ := revertData(reason)
	return &dataErr{msg: "execution reverted: " + reason, data: hexutil.Encode(b)}
}

func revertData(reason string) ([]byte, error) {
	ty, _ := newType("string")
	enc, err := argsOf(ty).Pack(reason)
	if err != nil {
		return nil, err
	}
	return append(append([]byte{}, errorSelector...), enc...), nil
}

// blockJSON is a block head as a provider serves it.
func blockJSON(n uint64, hash common.Hash, t uint64, baseFee *big.Int) map[string]any {
	m := map[string]any{"number": hexutil.EncodeUint64(n), "hash": hash, "parentHash": common.Hash{}, "timestamp": hexutil.EncodeUint64(t)}
	if baseFee != nil {
		m["baseFeePerGas"] = (*hexutil.Big)(baseFee)
	}
	return m
}

// hashOf is a block hash of a number in the fakes (a different family per seed).
func hashOf(seed byte, n uint64) common.Hash {
	var h common.Hash
	h[0] = seed
	h[31] = byte(n)
	h[30] = byte(n >> 8)
	return h
}

// chain is a key's view of a chain for the sender's tests: a base fee, a mempool by nonce, receipts, a block
// counter; mine() includes the pending transaction at the next nonce when its fee cap meets the base fee.
type chain struct {
	mu        sync.Mutex
	from      common.Address
	baseFee   *big.Int
	tip       *big.Int
	nonce     uint64 // mined
	pool      map[uint64]*types.Transaction
	receipts  map[common.Hash]*types.Receipt
	block     uint64
	estimate  func(data []byte) (uint64, error)
	callErr   func(data []byte) error
	revertOn  map[string]bool // calldata hex → mined with status 0
	sent      []*types.Transaction
	signer    types.Signer
	autoMine  bool
	foreignAt map[uint64]bool // nonces a stranger's transaction took
	gasLimit  uint64          // the head block's gas limit (0: not said)
}

func newChain(from common.Address, chainID *big.Int) *chain {
	return &chain{from: from, baseFee: GweiOf(1), tip: GweiOf(0.1), pool: map[uint64]*types.Transaction{}, receipts: map[common.Hash]*types.Receipt{},
		block: 100, revertOn: map[string]bool{}, signer: types.LatestSignerForChainID(chainID), foreignAt: map[uint64]bool{},
		estimate: func([]byte) (uint64, error) { return 100_000, nil }}
}

func (c *chain) mine() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.block++
	tx := c.pool[c.nonce]
	if tx == nil || tx.GasFeeCap().Cmp(c.baseFee) < 0 {
		return
	}
	delete(c.pool, c.nonce)
	status := types.ReceiptStatusSuccessful
	if c.revertOn[hexutil.Encode(tx.Data())] {
		status = types.ReceiptStatusFailed
	}
	c.receipts[tx.Hash()] = &types.Receipt{Type: types.DynamicFeeTxType, Status: status, CumulativeGasUsed: 50_000, GasUsed: 50_000, TxHash: tx.Hash(),
		BlockNumber: new(big.Int).SetUint64(c.block), Logs: []*types.Log{}, Bloom: types.Bloom{}, EffectiveGasPrice: new(big.Int).Set(c.baseFee)}
	c.nonce++
}

// takeNonce is a stranger's transaction mined at the next nonce.
func (c *chain) takeNonce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pool, c.nonce)
	c.foreignAt[c.nonce] = true
	c.nonce++
	c.block++
}

func (c *chain) rpc() *fakeRPC {
	f := newFakeRPC()
	f.on("eth_estimateGas", func(args []any) (any, error) {
		arg := args[0].(map[string]any)
		g, err := c.estimate(arg["data"].(hexutil.Bytes))
		if err != nil {
			return nil, err
		}
		return hexutil.Uint64(g), nil
	})
	f.on("eth_getBlockByNumber", func(args []any) (any, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		b := blockJSON(c.block, hashOf(1, c.block), 1_800_000_000+c.block*12, c.baseFee)
		if c.gasLimit > 0 {
			b["gasLimit"] = hexutil.Uint64(c.gasLimit)
		}
		return b, nil
	})
	f.on("eth_maxPriorityFeePerGas", func([]any) (any, error) { return (*hexutil.Big)(c.tip), nil })
	f.on("eth_getTransactionCount", func(args []any) (any, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if args[1] == "pending" {
			n := c.nonce
			for c.pool[n] != nil {
				n++
			}
			return hexutil.Uint64(n), nil
		}
		return hexutil.Uint64(c.nonce), nil
	})
	f.on("eth_sendRawTransaction", func(args []any) (any, error) {
		var tx types.Transaction
		if err := tx.UnmarshalBinary(args[0].(hexutil.Bytes)); err != nil {
			return nil, err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if from, err := types.Sender(c.signer, &tx); err != nil || from != c.from {
			return nil, fmt.Errorf("bad signature")
		}
		if tx.Nonce() < c.nonce {
			return nil, fmt.Errorf("nonce too low: next nonce %d, tx nonce %d", c.nonce, tx.Nonce())
		}
		if old := c.pool[tx.Nonce()]; old != nil {
			if old.Hash() == tx.Hash() {
				return nil, fmt.Errorf("already known")
			}
			if tx.GasTipCap().Cmp(mulDiv(old.GasTipCap(), 11, 10)) < 0 || tx.GasFeeCap().Cmp(mulDiv(old.GasFeeCap(), 11, 10)) < 0 {
				return nil, fmt.Errorf("replacement transaction underpriced")
			}
		}
		c.pool[tx.Nonce()] = &tx
		c.sent = append(c.sent, &tx)
		if c.autoMine {
			c.mu.Unlock()
			c.mine()
			c.mu.Lock()
		}
		return tx.Hash(), nil
	})
	f.on("eth_getTransactionReceipt", func(args []any) (any, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if r := c.receipts[args[0].(common.Hash)]; r != nil {
			return r, nil
		}
		return nil, nil
	})
	f.on("eth_call", func(args []any) (any, error) {
		arg := args[0].(map[string]any)
		if c.callErr != nil {
			if err := c.callErr(arg["data"].(hexutil.Bytes)); err != nil {
				return nil, err
			}
		}
		return hexutil.Bytes{}, nil
	})
	f.on("eth_getBalance", func([]any) (any, error) { return (*hexutil.Big)(big.NewInt(1e18)), nil })
	// the pool as a node shows it by sender and nonce (reth, Erigon, anvil): a pending transaction, or null
	f.on("eth_getTransactionBySenderAndNonce", func(args []any) (any, error) {
		n := uint64(args[1].(hexutil.Uint64))
		c.mu.Lock()
		defer c.mu.Unlock()
		tx := c.pool[n]
		if tx == nil {
			return nil, nil
		}
		return map[string]any{"hash": tx.Hash(), "nonce": hexutil.Uint64(tx.Nonce()), "maxFeePerGas": (*hexutil.Big)(tx.GasFeeCap()),
			"maxPriorityFeePerGas": (*hexutil.Big)(tx.GasTipCap()), "from": c.from, "blockNumber": nil}, nil
	})
	return f
}

func newType(s string) (abi.Type, error) { return abi.NewType(s, "", nil) }

func argsOf(ts ...abi.Type) abi.Arguments {
	out := make(abi.Arguments, len(ts))
	for i, t := range ts {
		out[i] = abi.Argument{Type: t}
	}
	return out
}

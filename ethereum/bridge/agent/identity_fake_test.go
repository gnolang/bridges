package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The fakes of both chains the E-M3b machines' tests run on (the Solana agent's tests' pattern): an Ethereum chain in
// memory answering JSON-RPC through ethx's own client — two providers over one chain, either of which can be made to
// lie, lag, forget state or serve a reorganised log — and a gno node answering the realms' renders.

var (
	m3bMailbox = common.HexToAddress("0x000000000000000000000000000000000000b0b0")
	m3bHook    = common.HexToAddress("0x000000000000000000000000000000000000b0b1")
	m3bClient  = common.HexToAddress("0x000000000000000000000000000000000000b0b2")
	m3bGate    = common.HexToAddress("0x000000000000000000000000000000000000b0b3")
	m3bAdmin   = common.HexToAddress("0x000000000000000000000000000000000000b0b4")
	m3bGNOT    = common.HexToAddress("0x000000000000000000000000000000000000a001")
	m3bBUBBLE  = common.HexToAddress("0x000000000000000000000000000000000000a002")
)

const (
	m3bNS       = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/"
	m3bEthDom   = uint32(31337)
	m3bGnoDom   = uint32(1198419796)
	m3bMailPath = m3bNS + "hlmailbox"
)

// m3bBlock is one block of the fake chain.
type m3bBlock struct {
	n    uint64
	hash common.Hash
	time uint64
}

// m3bChain is the Ethereum chain both fake providers read.
type m3bChain struct {
	mu        sync.Mutex
	blocks    []m3bBlock // by number (0 is genesis)
	finalized uint64
	logs      []types.Log
	supply    map[common.Address][]m3bAt // a token's supply from a block on
	views     map[common.Address]map[string]func(n uint64, in []any) []any
	code      map[common.Address]uint64   // the block a contract's code appears at
	balances  map[common.Address]*big.Int // an account's ETH (eth_getBalance answers no other)
	nonce     uint32                      // the mailbox's next nonce
	txs       map[common.Hash]m3bTx       // transactions by hash: their target and calldata
	receipts  map[common.Hash]m3bReceipt  // receipts by transaction hash (a creation's: its contract and block)
	// slots are the storage words eth_getStorageAt answers, each from a block on
	slots map[common.Address]map[common.Hash][]m3bWord
	// t0 is block 0's time: block n's is t0 + 12n (m3bTime0 unless a test retimes the chain)
	t0 uint64
}

// m3bTime0 is the fake chain's block 0 time by default.
const m3bTime0 = 1_700_000_000

// retime moves every block's time to a new base, block n at t0 + 12n (a test that needs the chain's finalized block
// to be past, or before, a time on gno.land: E4-GT1's credits are judged by the index's block time).
func (c *m3bChain) retime(t0 uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t0 = t0
	for i := range c.blocks {
		c.blocks[i].time = t0 + 12*c.blocks[i].n
	}
}

// m3bWord is a storage word from a block on.
type m3bWord struct {
	from uint64
	w    common.Hash
}

// setSlot writes a contract's storage slot from a block on (an address, as the EIP-1967 slots hold one).
func (c *m3bChain) setSlot(addr common.Address, slot common.Hash, from uint64, v common.Address) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.slots == nil {
		c.slots = map[common.Address]map[common.Hash][]m3bWord{}
	}
	if c.slots[addr] == nil {
		c.slots[addr] = map[common.Hash][]m3bWord{}
	}
	c.slots[addr][slot] = append(c.slots[addr][slot], m3bWord{from: from, w: common.BytesToHash(v.Bytes())})
}

// slotAt is a slot's word at a block (zero before its first write).
func (c *m3bChain) slotAt(addr common.Address, slot common.Hash, n uint64) common.Hash {
	var w common.Hash
	for _, x := range c.slots[addr][slot] {
		if x.from <= n {
			w = x.w
		}
	}
	return w
}

// m3bReceipt is a mined transaction's receipt as the fake serves it.
type m3bReceipt struct {
	status   uint64
	contract common.Address
	block    uint64
}

// creation records a top-level creation transaction: no target, its input, its sender, and its receipt.
func (c *m3bChain) creation(h common.Hash, from common.Address, input []byte, contract common.Address, block uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.txs == nil {
		c.txs = map[common.Hash]m3bTx{}
	}
	if c.receipts == nil {
		c.receipts = map[common.Hash]m3bReceipt{}
	}
	c.txs[h] = m3bTx{input: input, create: true, from: from}
	c.receipts[h] = m3bReceipt{status: 1, contract: contract, block: block}
}

// m3bTx is a transaction's target and calldata (a creation has no target).
type m3bTx struct {
	to     common.Address
	input  []byte
	create bool
	from   common.Address
}

// tx records a transaction's target and calldata.
func (c *m3bChain) tx(h common.Hash, to common.Address, input []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.txs == nil {
		c.txs = map[common.Hash]m3bTx{}
	}
	c.txs[h] = m3bTx{to: to, input: input}
}

// log records a log at a block (its block hash the canonical one).
func (c *m3bChain) log(l types.Log) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l.BlockHash = c.blocks[l.BlockNumber].hash
	c.logs = append(c.logs, l)
}

type m3bAt struct {
	from uint64
	v    *big.Int
}

func newM3bChain(height uint64) *m3bChain {
	c := &m3bChain{supply: map[common.Address][]m3bAt{}, views: map[common.Address]map[string]func(uint64, []any) []any{}, code: map[common.Address]uint64{m3bMailbox: 1}, t0: m3bTime0}
	for n := uint64(0); n <= height; n++ {
		c.blocks = append(c.blocks, m3bBlock{n: n, hash: m3bHash(0, n), time: c.t0 + 12*n})
	}
	c.finalized = height
	return c
}

func m3bHash(fork byte, n uint64) common.Hash {
	return crypto.Keccak256Hash([]byte{fork}, new(big.Int).SetUint64(n).Bytes())
}

// mine adds blocks up to height and finalizes up to final.
func (c *m3bChain) mine(height, final uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for n := uint64(len(c.blocks)); n <= height; n++ {
		c.blocks = append(c.blocks, m3bBlock{n: n, hash: m3bHash(0, n), time: c.t0 + 12*n})
	}
	c.finalized = final
}

func (c *m3bChain) setSupply(token common.Address, from uint64, v int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.supply[token] = append(c.supply[token], m3bAt{from: from, v: big.NewInt(v)})
}

func (c *m3bChain) supplyAt(token common.Address, n uint64) *big.Int {
	out := new(big.Int)
	for _, a := range c.supply[token] {
		if a.from <= n {
			out = a.v
		}
	}
	return out
}

// view sets what a contract's view answers (at any block).
func (c *m3bChain) view(addr common.Address, method string, f func(n uint64, in []any) []any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.views[addr] == nil {
		c.views[addr] = map[string]func(uint64, []any) []any{}
	}
	c.views[addr][method] = f
}

// burn records a token's own burn at a block: its SentTransferRemote and the mailbox's Dispatch in one transaction,
// and the supply falling by the amount from that block. It answers the message's id.
func (c *m3bChain) burn(token common.Address, realm [32]byte, to [32]byte, amount uint64, block uint64) [32]byte {
	c.mu.Lock()
	n := c.nonce
	c.nonce++
	c.mu.Unlock()
	tx := crypto.Keccak256Hash([]byte("tx"), new(big.Int).SetUint64(uint64(n)).Bytes(), token.Bytes())
	sent := types.Log{Address: token, Topics: []common.Hash{ethx.HypERC20.Event("SentTransferRemote"), ethx.Uint32Topic(m3bGnoDom), common.Hash(to)},
		Data: common.BigToHash(new(big.Int).SetUint64(amount)).Bytes(), BlockNumber: block, TxHash: tx, Index: uint(2 * n)}
	m := hl.Message{Version: 3, Nonce: n, Origin: m3bEthDom, Sender: ethx.AddressTo32(token), Destination: m3bGnoDom, Recipient: realm, Body: hl.TokenMessage(to, amount, nil)}
	c.dispatch(m, block, tx, uint(2*n+1))
	c.mu.Lock()
	sent.BlockHash = c.blocks[block].hash
	c.logs = append(c.logs, sent)
	cur := c.supplyAt(token, block)
	c.supply[token] = append(c.supply[token], m3bAt{from: block, v: new(big.Int).Sub(cur, new(big.Int).SetUint64(amount))})
	c.mu.Unlock()
	return hl.ID(m.Bytes())
}

// dispatch records the mailbox's Dispatch of a message (anyone's) at a block.
func (c *m3bChain) dispatch(m hl.Message, block uint64, tx common.Hash, index uint) {
	data, err := ethx.Mailbox.ABI.Events["Dispatch"].Inputs.NonIndexed().Pack(m.Bytes())
	if err != nil {
		panic(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sender := common.BytesToAddress(m.Sender[12:])
	c.logs = append(c.logs, types.Log{Address: m3bMailbox, Topics: []common.Hash{ethx.Mailbox.Event("Dispatch"), ethx.AddressTopic(sender), ethx.Uint32Topic(m.Destination), common.Hash(m.Recipient)},
		Data: data, BlockNumber: block, BlockHash: c.blocks[block].hash, TxHash: tx, Index: index})
}

// stranger records a stranger's dispatch toward gno.land naming a route realm as recipient (no token transfer).
func (c *m3bChain) stranger(realm [32]byte, to [32]byte, amount uint64, block uint64) {
	c.mu.Lock()
	n := c.nonce
	c.nonce++
	c.mu.Unlock()
	from := common.HexToAddress("0x00000000000000000000000000000000000000ff")
	m := hl.Message{Version: 3, Nonce: n, Origin: m3bEthDom, Sender: ethx.AddressTo32(from), Destination: m3bGnoDom, Recipient: realm, Body: hl.TokenMessage(to, amount, nil)}
	c.dispatch(m, block, crypto.Keccak256Hash([]byte("stranger"), new(big.Int).SetUint64(uint64(n)).Bytes()), uint(2*n+1))
}

// m3bProvider is one provider over the chain; its knobs make it lie, lag or forget.
type m3bProvider struct {
	c        *m3bChain
	mu       sync.Mutex
	lag      uint64                              // how many blocks its finalized trails the chain's
	noState  bool                                // eth_call at any block but the head fails as a pruned node's does
	dropLogs bool                                // answers every log query empty (a provider that does so, with no error)
	removed  bool                                // marks every log it answers as removed (a reorganisation under the range)
	mutate   func(l *types.Log)                  // changes a log it answers
	supplyX  func(n uint64, v *big.Int) *big.Int // changes a supply it answers
	calls    map[string]int
	maxRange uint64                               // refuses a log range wider than this ("block range too large"); 0 none
	codeX    func(a common.Address) hexutil.Bytes // the code an address answers with, when it says so
}

func (p *m3bProvider) count(method string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[method]
}

func (p *m3bProvider) CallContext(_ context.Context, result any, method string, args ...any) error {
	p.mu.Lock()
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[method]++
	p.mu.Unlock()
	v, err := p.answer(method, args)
	if err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, result)
}

func (p *m3bProvider) BatchCallContext(ctx context.Context, b []rpc.BatchElem) error {
	for i := range b {
		b[i].Error = p.CallContext(ctx, b[i].Result, b[i].Method, b[i].Args...)
	}
	return nil
}

func (p *m3bProvider) Close() {}

type m3bRPCBlock struct {
	Number     hexutil.Uint64 `json:"number"`
	Hash       common.Hash    `json:"hash"`
	ParentHash common.Hash    `json:"parentHash"`
	Timestamp  hexutil.Uint64 `json:"timestamp"`
	BaseFee    *hexutil.Big   `json:"baseFeePerGas"`
}

func (p *m3bProvider) blockJSON(n uint64) any {
	c := p.c
	if n >= uint64(len(c.blocks)) {
		return nil
	}
	b := c.blocks[n]
	var parent common.Hash
	if n > 0 {
		parent = c.blocks[n-1].hash
	}
	return m3bRPCBlock{Number: hexutil.Uint64(n), Hash: b.hash, ParentHash: parent, Timestamp: hexutil.Uint64(b.time), BaseFee: (*hexutil.Big)(big.NewInt(1e8))}
}

// blockOf is the block a read is pinned to.
func (p *m3bProvider) blockOf(at any) (uint64, error) {
	c := p.c
	switch a := at.(type) {
	case string:
		switch a {
		case "latest", "pending":
			return uint64(len(c.blocks) - 1), nil
		case "finalized", "safe":
			return c.finalized - min(p.lag, c.finalized), nil
		}
		n, err := hexutil.DecodeUint64(a)
		return n, err
	case rpc.BlockNumberOrHash:
		if h, ok := a.Hash(); ok {
			for _, b := range c.blocks {
				if b.hash == h {
					return b.n, nil
				}
			}
			return 0, errors.New("header not found")
		}
		if bn, ok := a.Number(); ok {
			return uint64(bn), nil
		}
	}
	return 0, fmt.Errorf("fake: a block argument %T", at)
}

func (p *m3bProvider) answer(method string, args []any) (any, error) {
	c := p.c
	c.mu.Lock()
	defer c.mu.Unlock()
	switch method {
	case "eth_chainId":
		return hexutil.Uint64(31337), nil
	case "eth_getBalance":
		if b, ok := c.balances[args[0].(common.Address)]; ok {
			return (*hexutil.Big)(b), nil
		}
		return nil, fmt.Errorf("fake: no balance for %s", args[0].(common.Address).Hex())
	case "eth_getTransactionByHash":
		t, ok := c.txs[args[0].(common.Hash)]
		if !ok {
			return nil, nil
		}
		if t.create {
			return map[string]any{"to": nil, "input": hexutil.Bytes(t.input), "nonce": hexutil.Uint64(0), "from": t.from}, nil
		}
		return map[string]any{"to": t.to, "input": hexutil.Bytes(t.input), "nonce": hexutil.Uint64(0), "from": common.Address{}}, nil
	case "eth_getTransactionReceipt":
		h := args[0].(common.Hash)
		r, ok := c.receipts[h]
		if !ok {
			return nil, nil
		}
		return map[string]any{"type": "0x2", "status": hexutil.Uint64(r.status), "cumulativeGasUsed": "0x1", "logsBloom": hexutil.Bytes(make([]byte, 256)), "logs": []any{},
			"transactionHash": h, "contractAddress": r.contract, "gasUsed": "0x1", "effectiveGasPrice": "0x1", "blockHash": c.blocks[r.block].hash,
			"blockNumber": hexutil.Uint64(r.block), "transactionIndex": "0x0"}, nil
	case "eth_getBlockByNumber":
		n, err := p.blockOf(args[0])
		if err != nil {
			return nil, err
		}
		return p.blockJSON(n), nil
	case "eth_getBlockByHash":
		h := args[0].(common.Hash)
		for _, b := range c.blocks {
			if b.hash == h {
				return p.blockJSON(b.n), nil
			}
		}
		return nil, nil
	case "eth_getCode":
		n, err := p.blockOf(args[1])
		if err != nil {
			return nil, err
		}
		if p.codeX != nil {
			if code := p.codeX(args[0].(common.Address)); code != nil {
				return code, nil
			}
		}
		if from, ok := c.code[args[0].(common.Address)]; ok && n >= from {
			return hexutil.Bytes{0x60, 0x80}, nil
		}
		return hexutil.Bytes{}, nil
	case "eth_getStorageAt":
		n, err := p.blockOf(args[2])
		if err != nil {
			return nil, err
		}
		return c.slotAt(args[0].(common.Address), args[1].(common.Hash), n), nil
	case "eth_call":
		arg := args[0].(map[string]any)
		n, err := p.blockOf(args[1])
		if err != nil {
			return nil, err
		}
		if p.noState && n+128 < uint64(len(c.blocks)-1) {
			return nil, errors.New("missing trie node 7a3c… (path ) state 0x… is not available")
		}
		return p.call(arg["to"].(common.Address), []byte(arg["data"].(hexutil.Bytes)), n)
	case "eth_getLogs":
		q := args[0].(map[string]any)
		from, _ := hexutil.DecodeUint64(q["fromBlock"].(string))
		to, _ := hexutil.DecodeUint64(q["toBlock"].(string))
		if p.maxRange > 0 && to-from+1 > p.maxRange {
			return nil, errors.New("block range is too large")
		}
		if p.dropLogs {
			return []types.Log{}, nil
		}
		var addrs []common.Address
		if a, ok := q["address"].([]common.Address); ok {
			addrs = a
		}
		var topics []any
		if t, ok := q["topics"].([]any); ok {
			topics = t
		}
		var out []types.Log
		for _, l := range c.logs {
			if l.BlockNumber < from || l.BlockNumber > to || !m3bMatch(l, addrs, topics) {
				continue
			}
			l2 := l
			l2.Topics = append([]common.Hash{}, l.Topics...)
			if p.mutate != nil {
				p.mutate(&l2)
			}
			l2.Removed = p.removed
			out = append(out, l2)
		}
		if out == nil {
			out = []types.Log{}
		}
		return out, nil
	}
	return nil, fmt.Errorf("fake: no method %s", method)
}

func m3bMatch(l types.Log, addrs []common.Address, topics []any) bool {
	if len(addrs) > 0 {
		ok := false
		for _, a := range addrs {
			ok = ok || a == l.Address
		}
		if !ok {
			return false
		}
	}
	for i, t := range topics {
		if t == nil {
			continue
		}
		if i >= len(l.Topics) {
			return false
		}
		switch tv := t.(type) {
		case common.Hash:
			if l.Topics[i] != tv {
				return false
			}
		case []common.Hash:
			ok := false
			for _, h := range tv {
				ok = ok || l.Topics[i] == h
			}
			if !ok {
				return false
			}
		}
	}
	return true
}

// call answers a view of the fake contracts at a block.
func (p *m3bProvider) call(to common.Address, data []byte, n uint64) (any, error) {
	c := p.c
	if len(data) < 4 {
		return nil, errors.New("fake: short calldata")
	}
	var contract *ethx.Contract
	switch to {
	case m3bGNOT, m3bBUBBLE:
		contract = ethx.HypERC20
	case m3bClient:
		contract = ethx.LightClient
	case m3bMailbox:
		contract = ethx.Mailbox
	case m3bGate:
		contract = ethx.Gate
	case m3bAdmin:
		contract = ethx.Admin
	default:
		if c.views[to] == nil {
			return nil, fmt.Errorf("fake: no contract at %s", to.Hex())
		}
		contract = ethx.Admin // another address given views answers as an HlAdmin (a kind 9's successor)
	}
	m, err := contract.ABI.MethodById(data[:4])
	if err != nil {
		return nil, err
	}
	in, err := m.Inputs.Unpack(data[4:])
	if err != nil {
		return nil, err
	}
	var vals []any
	if f := c.views[to][m.Name]; f != nil {
		vals = f(n, in)
	} else if m.Name == "totalSupply" {
		s := c.supplyAt(to, n)
		if p.supplyX != nil {
			s = p.supplyX(n, s)
		}
		vals = []any{s}
	} else if m.Name == "delivered" {
		vals = []any{false}
	} else if m.Name == "nonce" && to == m3bMailbox {
		// the mailbox's nonce at a block: the messages it dispatched at or below it (Mailbox.nonce counts every one)
		var nonce uint32
		for _, l := range c.logs {
			if l.Address == m3bMailbox && len(l.Topics) > 0 && l.Topics[0] == ethx.Mailbox.Event("Dispatch") && l.BlockNumber <= n {
				nonce++
			}
		}
		vals = []any{nonce}
	} else {
		vals = m3bZero(m.Outputs)
	}
	out, err := m.Outputs.Pack(vals...)
	if err != nil {
		return nil, fmt.Errorf("fake: packing %s: %w", m.Name, err)
	}
	return hexutil.Bytes(out), nil
}

// m3bZero is a method's zero answer.
func m3bZero(outs abi.Arguments) []any {
	var vals []any
	for _, o := range outs {
		vals = append(vals, reflectZero(o.Type))
	}
	return vals
}

func reflectZero(t abi.Type) any {
	if (t.T == abi.UintTy || t.T == abi.IntTy) && t.Size > 64 {
		return new(big.Int)
	}
	return reflect.New(t.GetType()).Elem().Interface()
}

// m3bClientViews sets the client's views: a trusted height and hash.
func (c *m3bChain) clientAt(trusted int64, hash [32]byte) {
	c.view(m3bClient, "trustedHeight", func(uint64, []any) []any { return []any{trusted} })
	c.view(m3bClient, "trustedHash", func(uint64, []any) []any { return []any{hash} })
}

// m3bPair is the fake chain read through two providers.
func m3bPair(c *m3bChain) (ethx.Pair, *m3bProvider, *m3bProvider) {
	a, b := &m3bProvider{c: c}, &m3bProvider{c: c}
	p, err := ethx.NewPair(ethx.NewClient(ethx.Provider{Name: "a", URL: "https://a.example/KEY"}, a), ethx.NewClient(ethx.Provider{Name: "b", URL: "https://b.example/KEY"}, b), false)
	if err != nil {
		panic(err)
	}
	return p, a, b
}

// ------------------------------------------------------------------------------------------------ gno.land --

// m3bGno is a gno node in memory: its head, its blocks' hashes and every render by "pkgpath:path".
type m3bGnoNode struct {
	mu        sync.Mutex
	height    int64
	time      time.Time
	catching  bool
	hashes    map[int64]string
	renders   map[string]string
	calls     []string
	callErr   error
	statusErr error
	// afterRender runs after a render is answered (outside the lock): a test lands a send between two reads, as a
	// real node does, which answers its latest state at any height (gno.land's realm objects are not versioned)
	afterRender func(pkg, path string)
	// blockErr makes Block fail at the heights it answers an error for (a node that does not serve a block)
	blockErr func(h int64) error
	// renderErr makes a render fail (a query out of gas, a node error) where it answers an error
	renderErr func(pkg, path string) error
	// waiting are routes' waiting indexes (E4-RL1, waiting/<from>), each entry an object with "pos" first, in place
	// order; a route with none set serves its claims/<from> pages' claims at places 1, 2, … (the index of a route whose
	// open claims those are), and olderRoutes render waiting/<from> as an unknown path (built before the index)
	waiting     map[string][]string
	olderRoutes map[string]bool
	// placed are the places the derived indexes gave, by route and claim id: a claim keeps its place, a new one takes
	// the next (the realm's index is append-only)
	placed     map[string]map[string]int64
	placedLast map[string]int64
}

// setWaiting sets a route's waiting index: its entries (objects with "pos" first) in place order.
func (g *m3bGnoNode) setWaiting(route string, entries []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.waiting == nil {
		g.waiting = map[string][]string{}
	}
	g.waiting[route] = entries
}

// olderRoute makes a route one built before the waiting index: waiting/<from> is an unknown path there.
func (g *m3bGnoNode) olderRoute(route string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.olderRoutes == nil {
		g.olderRoutes = map[string]bool{}
	}
	g.olderRoutes[route] = true
}

// waitingEntries is a route's waiting index, in place order: the one set (setWaiting), else the claims of its
// claims/<from> pages, each at the place it took when first listed (placed) — and the last place it gave, which never
// goes down (the route json's "waitingLast"). Called with g.mu held.
func (g *m3bGnoNode) waitingEntries(pkg string) ([]string, int64) {
	if g.placed == nil {
		g.placed, g.placedLast = map[string]map[string]int64{}, map[string]int64{}
	}
	if entries, ok := g.waiting[pkg]; ok {
		for _, e := range entries {
			var p struct {
				Pos int64 `json:"pos"`
			}
			if json.Unmarshal([]byte(e), &p) == nil && p.Pos > g.placedLast[pkg] {
				g.placedLast[pkg] = p.Pos
			}
		}
		return entries, g.placedLast[pkg]
	}
	if g.placed[pkg] == nil {
		g.placed[pkg] = map[string]int64{}
	}
	type placedClaim struct {
		pos int64
		obj string
	}
	var listed []placedClaim
	for at := 0; ; at += 100 {
		page, ok := g.renders[pkg+":claims/"+strconv.Itoa(at)]
		if !ok {
			break
		}
		var claims []json.RawMessage
		if json.Unmarshal([]byte(page), &claims) != nil {
			break
		}
		for _, c := range claims {
			var id struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(c, &id)
			pos, ok := g.placed[pkg][id.ID]
			if !ok {
				g.placedLast[pkg]++
				pos = g.placedLast[pkg]
				g.placed[pkg][id.ID] = pos
			}
			listed = append(listed, placedClaim{pos, strings.TrimPrefix(strings.TrimSpace(string(c)), "{")})
		}
		if len(claims) < 100 {
			break
		}
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].pos < listed[j].pos })
	var entries []string
	for _, c := range listed {
		entries = append(entries, fmt.Sprintf(`{"pos":%d,%s`, c.pos, c.obj))
	}
	return entries, g.placedLast[pkg]
}

// waitingPage is a route's waiting/<from> as the realms render it (waitingPage, F15d): up to 100 entries from the
// least place at or past <from> (0 reads as 1), "[]" for a <from> of anything but digits. Called with g.mu held.
func (g *m3bGnoNode) waitingPage(pkg, from string) string {
	if g.olderRoutes[pkg] {
		return "unknown path; see the route's page"
	}
	n, err := strconv.ParseUint(from, 10, 63)
	if err != nil {
		return "[]"
	}
	entries, _ := g.waitingEntries(pkg)
	var out []string
	for _, e := range entries {
		var p struct {
			Pos uint64 `json:"pos"`
		}
		if json.Unmarshal([]byte(e), &p) != nil || p.Pos < max(n, 1) {
			continue
		}
		if out = append(out, e); len(out) == 100 {
			break
		}
	}
	return "[" + strings.Join(out, ",") + "]"
}

// withWaitingLast renders a route's json as the realms do since F15d: with "waitingLast", the last place its waiting
// index gave — on a route json that names none, of a route that is not built before the index. Called with g.mu held.
func (g *m3bGnoNode) withWaitingLast(pkg, v string) string {
	if g.olderRoutes[pkg] || strings.Contains(v, `"waitingLast"`) || !strings.HasPrefix(strings.TrimSpace(v), "{") {
		return v
	}
	if !strings.Contains(v, `"lock"`) { // a route's json carries its lock; hlmailbox's, r/hlgov's, r/hlfees' do not
		return v
	}
	_, last := g.waitingEntries(pkg)
	return strings.TrimSuffix(strings.TrimSpace(v), "}") + `,"waitingLast":` + strconv.FormatInt(last, 10) + "}"
}

func newM3bGno(height int64, at time.Time) *m3bGnoNode {
	return &m3bGnoNode{height: height, time: at, hashes: map[int64]string{}, renders: map[string]string{}}
}

func (g *m3bGnoNode) set(pkg, path, v string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.renders[pkg+":"+path] = v
}

func (g *m3bGnoNode) advance(height int64, at time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.height, g.time = height, at
}

func (g *m3bGnoNode) RenderAt(ctx context.Context, pkg, path string, h int64) (string, error) {
	v, err := g.renderAt(ctx, pkg, path, h)
	g.mu.Lock()
	after := g.afterRender
	g.mu.Unlock()
	if after != nil {
		after(pkg, path)
	}
	return v, err
}

func (g *m3bGnoNode) renderAt(_ context.Context, pkg, path string, _ int64) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.renderErr != nil {
		if err := g.renderErr(pkg, path); err != nil {
			return "", err
		}
	}
	if v, ok := g.renders[pkg+":"+path]; ok {
		if path == "json" {
			v = g.withWaitingLast(pkg, g.withDelivered(pkg, v))
		}
		return v, nil
	}
	switch {
	case strings.HasPrefix(path, "delivered/"):
		return "0", nil
	case strings.HasPrefix(path, "claim/"), strings.HasPrefix(path, "send/"), strings.HasPrefix(path, "decision/"):
		return "null", nil
	case strings.HasPrefix(path, "claims/"):
		return "[]", nil
	case strings.HasPrefix(path, "waiting/"):
		return g.waitingPage(pkg, path[len("waiting/"):]), nil
	case path == "decisions.json":
		return "[]", nil
	case strings.HasPrefix(path, "decisions.json/"):
		return "unknown path; see the page", nil // a realm built before the pages (E3-RL4): its whole page is read
	}
	return "", fmt.Errorf("fake gno: no render %s:%s", pkg, path)
}

// withDelivered renders a mailbox's json as hlmailbox does: its "delivered" the number of message ids it delivered (the
// realm's done.Size(): every Process and every MarkDone adds one, nothing removes one) — here the delivered/<id> marks a
// test set to a height — so a credit a test lands moves the count as a real credit does. A json with no such mark is
// left as it is.
func (g *m3bGnoNode) withDelivered(pkg, v string) string {
	n := 0
	prefix := pkg + ":delivered/"
	for k, h := range g.renders {
		if strings.HasPrefix(k, prefix) && strings.TrimSpace(h) != "0" && strings.TrimSpace(h) != "" {
			n++
		}
	}
	if n == 0 {
		return v
	}
	var j map[string]any
	d := json.NewDecoder(strings.NewReader(v))
	d.UseNumber() // the numbers as written
	if err := d.Decode(&j); err != nil {
		return v
	}
	j["delivered"] = n
	b, err := json.Marshal(j)
	if err != nil {
		return v
	}
	return string(b)
}

func (g *m3bGnoNode) Status(context.Context) (gno.Head, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.statusErr != nil {
		return gno.Head{}, g.statusErr
	}
	return gno.Head{Height: g.height, Time: g.time, Hash: g.hashOf(g.height), CatchingUp: g.catching}, nil
}

func (g *m3bGnoNode) hashOf(h int64) string {
	if v, ok := g.hashes[h]; ok {
		return v
	}
	return fmt.Sprintf("%064x", h)
}

func (g *m3bGnoNode) Block(_ context.Context, h int64) (gno.Head, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if h > g.height {
		return gno.Head{}, fmt.Errorf("height %d must be less than or equal to the current blockchain height %d", h, g.height)
	}
	if g.blockErr != nil {
		if err := g.blockErr(h); err != nil {
			return gno.Head{}, err
		}
	}
	return gno.Head{Height: h, Time: g.time, Hash: g.hashOf(h)}, nil
}

func (g *m3bGnoNode) Call(_ context.Context, pkg, fn string, _ int64, args ...string) (*gno.Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, pkg+"."+fn+"("+strings.Join(args, ",")+")")
	if g.callErr != nil {
		return nil, g.callErr
	}
	return &gno.Result{Hash: "TX" + strconv.Itoa(len(g.calls)), Height: g.height}, nil
}

func (g *m3bGnoNode) callsOf(fn string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, c := range g.calls {
		if strings.Contains(c, "."+fn+"(") {
			out = append(out, c)
		}
	}
	return out
}

var _ gnoside.Caller = (*m3bGnoNode)(nil)

// m3bMailboxJSON is hlmailbox's json for the tests: gno.land's domain, the signers and the seats.
func m3bMailboxJSON(signers []string, threshold int, seats []string) string {
	b, _ := json.Marshal(map[string]any{"domain": m3bGnoDom, "live": true, "count": 0, "signers": map[string]any{"keys": signers, "threshold": threshold}, "seats": seats, "routes": map[string]any{}})
	return string(b)
}

// m3bRouteJSON is a route realm's json with its counters.
func m3bRouteJSON(lock, owed, voided, lost int64) string {
	b, _ := json.Marshal(map[string]any{"live": true, "lock": lock, "owed": owed, "voided": voided, "lost": lost, "fee": 5_000_000})
	return string(b)
}

// m3bConfig is a deployment's config over the fakes.
func m3bConfig() *Config {
	c := &Config{}
	c.Gno.RPC = "http://127.0.0.1:26657"
	c.Gno.ChainID = "dev"
	c.Gno.Mailbox = m3bMailPath
	c.Gno.Gov = m3bNS + "hlgov"
	c.Eth.Domain = m3bEthDom
	c.Eth.ChainID = 31337
	c.Eth.Mailbox = m3bMailbox.Hex()
	c.Eth.MerkleTreeHook = m3bHook.Hex()
	c.Eth.Client = m3bClient.Hex()
	c.Eth.Gate = m3bGate.Hex()
	c.Eth.Admin = m3bAdmin.Hex()
	c.Routes = []Route{{Symbol: "GNOT", Gno: m3bNS + "hlgnot", Token: m3bGNOT.Hex()}, {Symbol: "BUBBLE", Gno: m3bNS + "hlbubble", Token: m3bBUBBLE.Hex()}}
	return c
}

package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// fakeEth is Ethereum in memory for the machines' tests: a model of the light client, the gate, HlAdmin (and its
// successors), the mailbox and the two tokens, answering their views by ABI and applying the calls the machines
// send — an update or a header proof records the header the fake gno chain has under that hash, a process checks
// the proof under an accepted header, the gate's flags, quarantine and limits and HlAdmin's counter, an execute a
// decision's maturity — and the blocks, logs, receipts and transactions the machines read back.
type fakeEth struct {
	mu sync.Mutex
	g  *fakeGno

	mailbox, client, gate common.Address
	tokens                [2]common.Address
	admins                map[common.Address]*fakeAdmin

	// the client
	trusted       int64
	trustedHash   [32]byte
	trustedTime   uint64
	setHash       [32]byte
	nextSetHash   [32]byte
	epoch         uint64
	quarantineEnd uint64
	entries       map[[32]byte]ethx.Entry
	stages        map[[32]byte]map[int]bool // stage key → staged slots
	// the gate
	flags      [6]ethx.Flag
	limits     [2]ethx.MintLimit
	gateFactor uint64
	// the mailbox and the tokens
	delivered      map[[32]byte]bool
	finalDelivered map[[32]byte]bool // the marks at the finalized block (nil: as at latest)
	ism            map[common.Address]common.Address
	routers        map[common.Address][32]byte
	// the chain
	block    uint64
	now      uint64
	baseFee  *big.Int
	logs     []types.Log
	times    map[uint64]uint64 // a mined block's time
	receipts map[common.Hash]*types.Receipt
	txs      map[common.Hash]*ethx.TxInput
	// the key
	from    common.Address
	sent    []ethx.Call
	fail    map[string]error // a send of this method answers this error
	failNth map[string]int   // the nth send of this method (1-based) answers a transient error
	pendNth map[string]int   // the nth send of this method stays pending (ErrStillPending), mined at the next Send or Settle
	// lagLatest answers the client's trusted height at latest from before the last update (a node a block behind)
	lagLatest   bool
	prevTrusted int64
	pending     *ethx.Call
	capSlots    int // an update carrying more slots than this answers ErrGasCap (0: no cap)
	quiet       bool
	// hook runs before a send of the method is applied (a stranger's update landing first, E3-GS3); an error it answers
	// is the send's
	hook map[string]func(c ethx.Call) error
	// lagLogs answers the next this many log reads empty (a provider whose log index lags its latest, E3-GS7)
	lagLogs int
	// finalLag is how many blocks the finalized block lies behind the latest (64 by default; blocks are mined by sends
	// only, so a test of finality mines them as its clock runs: mineFor)
	finalLag uint64
	// numbered answers a block by its number (its time 12 s a block before now when not mined): the tests of the log
	// window's start set it (the others' block numbers jump without their clock)
	numbered bool
	// hideEntries answers the client's entry of a header empty at latest this many reads more (a node a block behind);
	// hideReceipts answers a transaction's receipt not found this many reads more
	hideEntries  map[[32]byte]int
	hideReceipts map[common.Hash]int
	// lieFrom is a transaction's sender as a lying provider's JSON names it: Transaction answers it, SignedTransaction
	// the key that signed it
	lieFrom map[common.Hash]common.Address
	// entryAt is the block each entry was recorded in, and trustHist the trusted height from each block on: the client's
	// entries and trusted height read at the finalized block are as of it (the setup's are final from block 0)
	entryAt   map[[32]byte]uint64
	trustHist []trustRec
	balances  map[common.Address]*big.Int // an account's ETH (1 ETH when not set)
	gasUsed   uint64                      // an update's receipt's gas (4,100,000 when not set)
	stateGas  bool                        // the chain meters state gas (EIP-8037: Sepolia since Glamsterdam)
	ranges    [][2]uint64                 // every LogsRange asked, from and to
}

// trustRec is the client's trusted height from a block on.
type trustRec struct {
	block  uint64
	height int64
}

// trustedAt is the client's trusted height as of block n.
func (f *fakeEth) trustedAt(n uint64) int64 {
	h := int64(0)
	for _, r := range f.trustHist {
		if r.block <= n {
			h = r.height
		}
	}
	return h
}

type fakeAdmin struct {
	lastSeq        uint64
	startSeq       uint64  // the number its sequence started after (its first storage word's bytes 16–23)
	finalLastSeq   *uint64 // the counter at the finalized block when it differs
	decisions      map[uint64]*ethx.Decision
	finalDecisions map[uint64]*ethx.Decision // the decisions at the finalized block when they differ
	flags          [6]ethx.Flag
	hold           ethx.Flag
	holds          []ethx.Hold
	cancelled      [][2]uint64
	successor      common.Address
	clockFactor    uint64
}

var (
	ethMailbox = common.HexToAddress("0x00000000000000000000000000000000000000b1")
	ethClient  = common.HexToAddress("0x00000000000000000000000000000000000000c1")
	ethGate    = common.HexToAddress("0x00000000000000000000000000000000000000d1")
	ethAdmin   = common.HexToAddress("0x00000000000000000000000000000000000000e1")
	ethGNOT    = common.HexToAddress("0x00000000000000000000000000000000000000f1")
	ethBUBBLE  = common.HexToAddress("0x00000000000000000000000000000000000000f2")
	ethKey     = common.HexToAddress("0x0000000000000000000000000000000000000a01")
)

func newFakeEth(g *fakeGno, trusted int64) *fakeEth {
	f := &fakeEth{g: g, mailbox: ethMailbox, client: ethClient, gate: ethGate, tokens: [2]common.Address{ethGNOT, ethBUBBLE},
		admins: map[common.Address]*fakeAdmin{ethAdmin: {decisions: map[uint64]*ethx.Decision{}, clockFactor: 1}}, epoch: 1,
		entries: map[[32]byte]ethx.Entry{}, stages: map[[32]byte]map[int]bool{}, gateFactor: 1,
		limits:    [2]ethx.MintLimit{{MaxPerMessage: 25_000_000_000, DayCap: 100_000_000_000}, {MaxPerMessage: 25_000_000_000, DayCap: 100_000_000_000}},
		delivered: map[[32]byte]bool{}, ism: map[common.Address]common.Address{ethGNOT: ethGate, ethBUBBLE: ethGate}, routers: map[common.Address][32]byte{},
		block: 1000, now: uint64(g.timeAt(g.head).Unix()), baseFee: ethx.GweiOf(1), times: map[uint64]uint64{}, receipts: map[common.Hash]*types.Receipt{}, txs: map[common.Hash]*ethx.TxInput{},
		from: ethKey, fail: map[string]error{}, failNth: map[string]int{}, pendNth: map[string]int{}, quiet: true}
	f.finalLag = 64
	f.trust(trusted)
	// the setup's trusted header is final from the start
	f.trustHist[0].block = 0
	for h := range f.entryAt {
		f.entryAt[h] = 0
	}
	return f
}

// trust sets the client's trusted header to the fake chain's header at h (an update recorded in the past).
func (f *fakeEth) trust(h int64) {
	f.g.mu.Lock()
	b := f.g.header(h)
	f.g.mu.Unlock()
	f.trusted, f.trustedHash, f.trustedTime = h, b.Hash, uint64(b.Header.Time.Secs)
	f.setHash = b.Header.ValidatorsHashArray()
	if b.Header.Announcing() {
		f.nextSetHash = b.Header.NextValidatorsHashArray()
	} else {
		f.nextSetHash = [32]byte{}
	}
	lrh, _ := b.Header.LastResultsHashArray()
	f.entries[b.Hash] = ethx.Entry{Epoch: f.epoch, Height: h, LastResultsHash: lrh}
	f.recordAt(b.Hash, h, true, f.block)
}

// recordAt notes the block an entry was recorded in, and the trusted height from it on when an update moved it.
func (f *fakeEth) recordAt(hash [32]byte, h int64, trusted bool, block uint64) {
	if f.entryAt == nil {
		f.entryAt = map[[32]byte]uint64{}
	}
	f.entryAt[hash] = block
	if trusted {
		f.trustHist = append(f.trustHist, trustRec{block: block, height: h})
	}
}

// accept records an entry for the fake chain's header at h without moving the trusted height (a header proof).
func (f *fakeEth) accept(h int64) {
	f.g.mu.Lock()
	b := f.g.header(h)
	f.g.mu.Unlock()
	lrh, _ := b.Header.LastResultsHashArray()
	f.entries[b.Hash] = ethx.Entry{Epoch: f.epoch, Height: h, LastResultsHash: lrh}
	f.recordAt(b.Hash, h, false, f.block)
}

func blockHash(n uint64) common.Hash {
	var h common.Hash
	h[0] = 0xbb
	binary.BigEndian.PutUint64(h[24:], n)
	return h
}

// mine advances a block, at the chain's time now (the tests move time), and answers it.
func (f *fakeEth) mine() uint64 {
	f.block++
	f.times[f.block] = f.now
	return f.block
}

// ------------------------------------------------------------------ ethChain --

func (f *fakeEth) BlockAt(_ context.Context, at ethx.At) (*ethx.Block, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case at.Tag == "latest" || at.Tag == "pending":
		return &ethx.Block{Number: f.block, Hash: blockHash(f.block), Time: f.now, BaseFee: new(big.Int).Set(f.baseFee)}, nil
	case at.Tag == "finalized":
		n := f.block - f.finalLag
		return &ethx.Block{Number: n, Hash: blockHash(n), Time: f.now - f.finalLag*12, BaseFee: new(big.Int).Set(f.baseFee)}, nil
	case at.Tag == "" && at.Hash == (common.Hash{}) && f.numbered:
		n := at.Number
		if n > f.block {
			return nil, fmt.Errorf("fake: no block %d yet", n)
		}
		t, ok := f.times[n]
		if !ok {
			t = f.now - (f.block-n)*12
		}
		return &ethx.Block{Number: n, Hash: blockHash(n), Time: t, BaseFee: new(big.Int).Set(f.baseFee)}, nil
	case at.Tag == "":
		for n := f.block; n > 0 && n+500 > f.block; n-- {
			if blockHash(n) == at.Hash {
				t, ok := f.times[n]
				if !ok {
					t = f.now - (f.block-n)*12
				}
				return &ethx.Block{Number: n, Hash: at.Hash, Time: t, BaseFee: new(big.Int).Set(f.baseFee)}, nil
			}
		}
	}
	return nil, fmt.Errorf("fake: no block %s", at)
}

func (f *fakeEth) finalized(at ethx.At) bool {
	return at.Tag == "finalized" || (at.Tag == "" && at.Hash == blockHash(f.block-f.finalLag))
}

func (f *fakeEth) CallAt(ctx context.Context, to common.Address, data []byte, at ethx.At) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view(to, data, at)
}

func (f *fakeEth) CallBatch(ctx context.Context, reqs []ethx.CallReq, at ethx.At) ([][]byte, []error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(reqs))
	errs := make([]error, len(reqs))
	for i, r := range reqs {
		out[i], errs[i] = f.view(r.To, r.Data, at)
	}
	return out, errs, nil
}

type flagOut = struct {
	BlockNumber uint64 `json:"blockNumber"`
	Time        uint64 `json:"time"`
}

func flagsOut(fs [6]ethx.Flag) [6]flagOut {
	var out [6]flagOut
	for i, x := range fs {
		out[i] = flagOut{x.BlockNumber, x.Time}
	}
	return out
}

// view answers one view by the contract at to.
func (f *fakeEth) view(to common.Address, data []byte, at ethx.At) ([]byte, error) {
	var c *ethx.Contract
	switch {
	case to == f.client:
		c = ethx.LightClient
	case to == f.gate:
		c = ethx.Gate
	case to == f.mailbox:
		c = ethx.Mailbox
	case to == f.tokens[0] || to == f.tokens[1]:
		c = ethx.HypERC20
	case f.admins[to] != nil:
		c = ethx.Admin
	default:
		return nil, fmt.Errorf("fake: no contract at %s", to.Hex())
	}
	m, err := c.ABI.MethodById(data[:4])
	if err != nil {
		return nil, err
	}
	args, err := m.Inputs.Unpack(data[4:])
	if err != nil {
		return nil, err
	}
	var out []any
	switch c {
	case ethx.LightClient:
		switch m.Name {
		case "trustedHeight":
			out = []any{f.trusted}
			if f.lagLatest && at.Tag == "latest" {
				out = []any{f.prevTrusted} // a provider behind a balancer answering latest from a node a block behind
			}
			if f.finalized(at) {
				out = []any{f.trustedAt(f.block - f.finalLag)}
			}
		case "trustedHash":
			out = []any{f.trustedHash}
		case "trustedTime":
			out = []any{f.trustedTime}
		case "setHash":
			out = []any{f.setHash}
		case "nextSetHash":
			out = []any{f.nextSetHash}
		case "epoch":
			out = []any{f.epoch}
		case "quarantineEnd":
			out = []any{f.quarantineEnd}
		case "chainId":
			out = []any{f.g.chainID}
		case "originDomain":
			out = []any{hl.DomainGnoTest}
		case "mailboxPkgPathHash", "runningHash":
			out = []any{[32]byte{}}
		case "clockFactor", "records":
			out = []any{uint64(1)}
		case "admin":
			out = []any{ethAdmin}
		case "historyLength":
			out = []any{big.NewInt(1)}
		case "entry":
			e := f.entries[args[0].([32]byte)]
			if h := args[0].([32]byte); at.Tag == "latest" && f.hideEntries[h] > 0 {
				f.hideEntries[h]--
				e = ethx.Entry{}
			}
			if n, ok := f.entryAt[args[0].([32]byte)]; ok && f.finalized(at) && n > f.block-f.finalLag {
				e = ethx.Entry{} // recorded after the finalized block
			}
			out = []any{struct {
				Epoch           uint64   `json:"epoch"`
				Height          int64    `json:"height"`
				LastResultsHash [32]byte `json:"lastResultsHash"`
			}{e.Epoch, e.Height, e.LastResultsHash}}
		case "stagePower":
			out = []any{int64(0)}
		}
	case ethx.Gate:
		switch m.Name {
		case "owner", "mailbox", "client":
			out = []any{common.Address{}}
		case "token":
			out = []any{f.tokens[args[0].(uint8)]}
		case "clockFactor":
			out = []any{f.gateFactor}
		case "keys":
			out = []any{[6]common.Address{}}
		case "flags":
			out = []any{flagsOut(f.flags)}
		case "hold", "holdSince", "dipSince":
			out = []any{flagOut{}}
		case "limits":
			l := f.limits[args[0].(uint8)]
			out = []any{struct {
				MaxPerMessage uint64 `json:"maxPerMessage"`
				DayCap        uint64 `json:"dayCap"`
				Used          uint64 `json:"used"`
				UsedAt        uint64 `json:"usedAt"`
			}{l.MaxPerMessage, l.DayCap, l.Used, l.UsedAt}}
		}
	case ethx.Mailbox:
		switch m.Name {
		case "delivered":
			id := args[0].([32]byte)
			marks := f.delivered
			if f.finalDelivered != nil && f.finalized(at) {
				marks = f.finalDelivered
			}
			out = []any{marks[id]}
		case "recipientIsm":
			out = []any{f.ism[args[0].(common.Address)]}
		}
	case ethx.HypERC20:
		switch m.Name {
		case "routers":
			out = []any{f.routers[to]}
		case "totalSupply":
			out = []any{big.NewInt(0)}
		}
	case ethx.Admin:
		a := f.admins[to]
		switch m.Name {
		case "lastSeq":
			n := a.lastSeq
			if a.finalLastSeq != nil && f.finalized(at) {
				n = *a.finalLastSeq
			}
			out = []any{n}
		case "pending":
			out = []any{uint32(len(a.decisions))}
		case "keys":
			out = []any{[6]common.Address{}}
		case "flags":
			out = []any{flagsOut(a.flags)}
		case "hold":
			out = []any{flagOut{a.hold.BlockNumber, a.hold.Time}}
		case "holds":
			hs := []struct {
				Start uint64 `json:"start"`
				End   uint64 `json:"end"`
			}{}
			for _, h := range a.holds {
				hs = append(hs, struct {
					Start uint64 `json:"start"`
					End   uint64 `json:"end"`
				}{h.Start, h.End})
			}
			out = []any{hs}
		case "cancelledRanges":
			cr := a.cancelled
			if cr == nil {
				cr = [][2]uint64{}
			}
			out = []any{cr}
		case "setupDone":
			out = []any{true}
		case "inherited":
			out = []any{false}
		case "successor":
			out = []any{a.successor}
		case "predecessor", "mailbox", "client", "gate", "proxyAdmin", "merkleTreeHook", "protocolFee":
			out = []any{common.Address{}}
		case "token":
			out = []any{f.tokens[args[0].(uint8)]}
		case "clockFactor":
			out = []any{a.clockFactor}
		case "origin":
			out = []any{hl.DomainGnoTest}
		case "hlgov":
			out = []any{[32]byte{}}
		case "decision":
			d := a.decisions[args[0].(uint64)]
			if a.finalDecisions != nil && f.finalized(at) {
				d = a.finalDecisions[args[0].(uint64)]
			}
			if d == nil {
				d = &ethx.Decision{Params: []byte{}}
			}
			out = []any{struct {
				Number         uint64         `json:"number"`
				Kind           uint8          `json:"kind"`
				DeliveredBlock uint64         `json:"deliveredBlock"`
				DeliveredTime  uint64         `json:"deliveredTime"`
				Deliverer      common.Address `json:"deliverer"`
				Params         []byte         `json:"params"`
			}{d.Number, d.Kind, d.DeliveredBlock, d.DeliveredTime, d.Deliverer, d.Params}}
		}
	}
	if out == nil {
		return nil, fmt.Errorf("fake: %s.%s has no answer", c.Name, m.Name)
	}
	return m.Outputs.Pack(out...)
}

func (f *fakeEth) Logs(ctx context.Context, q ethx.LogQuery) ([]types.Log, error) {
	return f.LogsRange(ctx, q, q.From, q.To)
}

func (f *fakeEth) LogsRange(_ context.Context, q ethx.LogQuery, from, to uint64) ([]types.Log, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ranges = append(f.ranges, [2]uint64{from, to})
	if f.lagLogs > 0 {
		f.lagLogs--
		return nil, nil
	}
	var out []types.Log
	for _, l := range f.logs {
		if l.BlockNumber < from || l.BlockNumber > to {
			continue
		}
		if len(q.Addresses) > 0 && q.Addresses[0] != l.Address {
			continue
		}
		ok := true
		for i, want := range q.Topics {
			if len(want) == 0 {
				continue
			}
			if i >= len(l.Topics) || l.Topics[i] != want[0] {
				ok = false
			}
		}
		if ok {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeEth) Receipt(_ context.Context, h common.Hash) (*types.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hideReceipts[h] > 0 {
		f.hideReceipts[h]--
		return nil, ethx.ErrNotFound
	}
	if r := f.receipts[h]; r != nil {
		return r, nil
	}
	return nil, ethx.ErrNotFound
}

func (f *fakeEth) Transaction(_ context.Context, h common.Hash) (*ethx.TxInput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t := f.txs[h]; t != nil {
		if from, ok := f.lieFrom[h]; ok {
			c := *t
			c.From = from
			return &c, nil
		}
		return t, nil
	}
	return nil, ethx.ErrNotFound
}

// SignedTransaction is the transaction as its signature makes it: the key that signed it, whatever a provider's JSON
// says (lieFrom).
func (f *fakeEth) SignedTransaction(_ context.Context, h common.Hash) (*ethx.TxInput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t := f.txs[h]; t != nil {
		return t, nil
	}
	return nil, ethx.ErrNotFound
}

// mineFor mines a block every 12 s of d, each at its time, the clock moving with them (a chain that advances while the
// machines wait: its finalized block passes what they read).
func (f *fakeEth) mineFor(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	secs := uint64(d / time.Second)
	for ; secs >= 12; secs -= 12 {
		f.now += 12
		f.mine()
	}
	f.now += secs
}

// chainStep moves a relayer world's clocks as step does, Ethereum mining a block every 12 s of it.
func (w *testWorld) chainStep(d time.Duration) {
	w.now = w.now.Add(d)
	w.e.mineFor(d)
}

// ------------------------------------------------------------------ ethSender --

// BalanceAt is an account's ETH (1 ETH when the test set none).
func (f *fakeEth) BalanceAt(_ context.Context, a common.Address, _ ethx.At) (*big.Int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.balances[a]; ok {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(1e18), nil
}

// StorageAt answers HlAdmin's first storage word (lastSeq in bytes 24–31, startSeq in 16–23); every other slot is
// zero.
func (f *fakeEth) StorageAt(_ context.Context, a common.Address, slot common.Hash, at ethx.At) (common.Hash, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var w common.Hash
	if ad := f.admins[a]; ad != nil && slot == hlAdminSlot {
		n := ad.lastSeq
		if ad.finalLastSeq != nil && f.finalized(at) {
			n = *ad.finalLastSeq
		}
		binary.BigEndian.PutUint64(w[24:], n)
		binary.BigEndian.PutUint64(w[16:24], ad.startSeq)
	}
	return w, nil
}

func (f *fakeEth) StateGas(context.Context) bool { return f.stateGas }

func (f *fakeEth) KeyQuiet(context.Context, time.Duration) (bool, error) { return f.quiet, nil }
func (f *fakeEth) Tip(context.Context) (*big.Int, error)                 { return ethx.GweiOf(1), nil }
func (f *fakeEth) Balance(ctx context.Context) (*big.Int, error) {
	return f.BalanceAt(ctx, f.from, ethx.Latest)
}
func (f *fakeEth) InFlight() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending != nil {
		return f.pending.Label
	}
	return ""
}
func (f *fakeEth) Address() common.Address { return f.from }

func revert(name, reason string) error { return &ethx.Revert{Name: name, Reason: reason} }

// methodOf names a call by the contract at its address.
func (f *fakeEth) methodOf(c ethx.Call) (*ethx.Contract, string, []any, error) {
	var ct *ethx.Contract
	switch {
	case c.To == f.client:
		ct = ethx.LightClient
	case c.To == f.mailbox:
		ct = ethx.Mailbox
	case f.admins[c.To] != nil:
		ct = ethx.Admin
	default:
		return nil, "", nil, fmt.Errorf("fake: a send to %s", c.To.Hex())
	}
	m, err := ct.ABI.MethodById(c.Data[:4])
	if err != nil {
		return nil, "", nil, err
	}
	args, err := m.Inputs.Unpack(c.Data[4:])
	return ct, m.Name, args, err
}

// Send applies a call to the model after the checks the contracts make; a refusal is a revert, nothing applied.
// settlePending mines the transaction left pending, as the sender resolves it before its next call.
func (f *fakeEth) settlePending() {
	if p := f.pending; p != nil {
		f.pending = nil
		if _, name, args, err := f.methodOf(*p); err == nil {
			_, _ = f.apply(*p, name, args)
		}
	}
}

func (f *fakeEth) Settle(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settlePending()
	return nil
}

func (f *fakeEth) Send(_ context.Context, c ethx.Call) (*types.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settlePending()
	f.sent = append(f.sent, c)
	_, name, args, err := f.methodOf(c)
	if err != nil {
		return nil, err
	}
	if h := f.hook[name]; h != nil {
		if err := h(c); err != nil {
			return nil, err
		}
	}
	if err := f.fail[name]; err != nil {
		return nil, err
	}
	count := 0
	for _, c2 := range f.sent {
		if _, nm, _, _ := f.methodOf(c2); nm == name {
			count++
		}
	}
	if n := f.failNth[name]; n > 0 && count == n {
		return nil, errors.New("fake: connection reset (a transient failure)")
	}
	if name == "update" && f.capSlots > 0 {
		if _, _, _, slots, err := ethx.HeaderCallOf(c.Data); err == nil && len(slots) > f.capSlots {
			return nil, fmt.Errorf("%w: %d slots", ethx.ErrGasCap, len(slots))
		}
	}
	max := ethx.GweiOf(5)
	if c.MaxBaseFee != nil {
		max = c.MaxBaseFee
	}
	if f.baseFee.Cmp(max) > 0 {
		return nil, &ethx.FeeTooHigh{BaseFee: f.baseFee, Max: max}
	}
	if n := f.pendNth[name]; n > 0 && count == n {
		f.pending = &c
		return nil, fmt.Errorf("%s: %w", c.Label, ethx.ErrStillPending)
	}
	return f.apply(c, name, args)
}

// apply is a transaction mined: its effect, its block, its receipt and logs.
func (f *fakeEth) apply(c ethx.Call, name string, args []any) (*types.Receipt, error) {
	var logs []*types.Log
	var err error
	switch name {
	case "update", "proveHeader":
		logs, err = f.header(name, c, args)
	case "process":
		err = f.process(c.To, args[0].([]byte), args[1].([]byte))
	case "execute":
		err = f.execute(c.To, args[0].(uint64))
	default:
		err = fmt.Errorf("fake: no effect for %s", name)
	}
	if err != nil {
		return nil, err
	}
	n := f.mine()
	tx := common.BytesToHash(append([]byte{0x7e}, big.NewInt(int64(len(f.sent))).Bytes()...))
	gas := f.gasUsed
	if gas == 0 {
		gas = 4_100_000
	}
	rc := &types.Receipt{Status: types.ReceiptStatusSuccessful, TxHash: tx, BlockNumber: new(big.Int).SetUint64(n), BlockHash: blockHash(n), GasUsed: gas, EffectiveGasPrice: new(big.Int).Add(f.baseFee, ethx.GweiOf(1))}
	f.receipts[tx] = rc
	to := c.To
	f.txs[tx] = &ethx.TxInput{To: &to, Input: c.Data, From: f.from}
	for _, l := range logs {
		l.BlockNumber, l.BlockHash, l.TxHash = n, blockHash(n), tx
		f.logs = append(f.logs, *l)
	}
	rc.Logs = logs
	return rc, nil
}

// header applies an update or a header proof: the header the fake chain has under the leaves' hash, signed by the
// expected set, the slots' power more than 2/3 (staged across calls by slot).
func (f *fakeEth) header(name string, c ethx.Call, args []any) ([]*types.Log, error) {
	_, hash, _, slots, err := ethx.HeaderCallOf(c.Data)
	if err != nil {
		return nil, err
	}
	f.g.mu.Lock()
	var blk *gnolight.Block
	for h := int64(1); h <= f.g.head; h++ {
		if b := f.g.header(h); b.Hash == hash {
			blk = b
			break
		}
	}
	var set *gnolight.ValidatorSet
	if blk != nil {
		set = f.g.setAt(blk.Header.Height).set
	}
	f.g.mu.Unlock()
	if blk == nil {
		return nil, revert("UnknownHeader", fmt.Sprintf("%x", hash[:4]))
	}
	h := blk.Header.Height
	expect := f.setHash
	if name == "update" {
		if h <= f.trusted {
			return nil, revert("HeightNotAboveTrusted", "heights only rise")
		}
		if f.nextSetHash != ([32]byte{}) {
			expect = f.nextSetHash
		}
	} else if h > f.trusted {
		return nil, revert("HeightAboveTrusted", "a header proof is at or below the trusted height")
	}
	if blk.Header.ValidatorsHashArray() != expect {
		return nil, revert("WrongSet", "")
	}
	// the tally lives under the stage key (E-M2b's stageKey): the submitter, the header, the commit's part set and
	// round, the epoch and the expected set — a stage begun under another epoch or set is orphaned
	cm, _, err := f.g.CommitAt(context.Background(), h)
	if err != nil {
		return nil, err
	}
	key, err := ethx.StageKey(f.from, &ethx.SignedHeader{Header: blk.Header, Commit: cm}, f.epoch, expect)
	if err != nil {
		return nil, err
	}
	staged := f.stages[key]
	if staged == nil {
		staged = map[int]bool{}
	}
	// a slot counted already is skipped, and a call that adds none reverts (fold F4's NoNewSignature)
	var tally int64
	added := 0
	for _, s := range slots {
		if !staged[s] {
			staged[s] = true
			added++
		}
	}
	if added == 0 {
		return nil, revert("NoNewSignature", "")
	}
	for s := range staged {
		tally += set.Validators[s].Power
	}
	if !gnolight.CommitRule(tally, set.TotalPower()) {
		f.stages[key] = staged
		data, _ := ethx.LightClient.ABI.Events["StageProgress"].Inputs.NonIndexed().Pack(hash, h, tally, set.TotalPower())
		return []*types.Log{{Address: f.client, Topics: []common.Hash{ethx.LightClient.Event("StageProgress"), key, ethx.AddressTopic(f.from)}, Data: data}}, nil
	}
	delete(f.stages, key)
	lrh, _ := blk.Header.LastResultsHashArray()
	proven := name == "proveHeader"
	if !proven {
		// as E-M2b's client: the first header of the announced set switches it (the epoch rises); an announcing
		// header records the next set and starts the quarantine
		f.prevTrusted = f.trusted
		f.trusted, f.trustedHash, f.trustedTime = h, hash, uint64(blk.Header.Time.Secs)
		if f.nextSetHash != ([32]byte{}) && blk.Header.ValidatorsHashArray() == f.nextSetHash {
			f.epoch++
			f.setHash, f.nextSetHash = f.nextSetHash, [32]byte{}
		}
		if blk.Header.Announcing() {
			f.nextSetHash = blk.Header.NextValidatorsHashArray()
			f.quarantineEnd = max(f.quarantineEnd, f.now+7*86_400)
		}
	}
	f.entries[hash] = ethx.Entry{Epoch: f.epoch, Height: h, LastResultsHash: lrh}
	f.recordAt(hash, h, !proven, f.block+1) // mined in the next block
	// the record counts the initialization's (record 1, the trusted header) as E-M2b's client does
	data, _ := ethx.LightClient.ABI.Events["HeaderRecorded"].Inputs.NonIndexed().Pack(f.epoch, uint64(len(f.entries))+1, [32]byte{}, proven)
	return []*types.Log{{Address: f.client, Topics: []common.Hash{ethx.LightClient.Event("HeaderRecorded"), common.Hash(hash), common.BigToHash(big.NewInt(h))}, Data: data}}, nil
}

// process is Mailbox.process: delivered once, the proof under an accepted header of the current epoch, the gate's
// checks for a token, HlAdmin's counter for a decision.
func (f *fakeEth) process(_ common.Address, meta, msg []byte) error {
	id := hl.ID(msg)
	if f.delivered[id] {
		return revert("Error", "Mailbox: already delivered")
	}
	hh, total, index, aunts, leaf, err := ethx.MetadataOf(meta)
	if err != nil {
		return revert("Error", "bad metadata")
	}
	e := f.entries[hh]
	if !e.Proves(f.epoch) {
		return revert("NotAccepted", "")
	}
	p := &gnolight.SimpleProof{Total: total, Index: index, LeafHash: gnolight.LeafHash(leaf), Aunts: aunts}
	if p.Verify(e.LastResultsHash, leaf) != nil {
		return revert("BadProof", "")
	}
	ds, err := gnolight.DispatchEvents(leaf, f.g.mailbox)
	found := false
	for _, d := range ds {
		found = found || d.ID == id
	}
	if err != nil || !found {
		return revert("NoDispatch", "")
	}
	m, _ := hl.ParseMessage(msg)
	rcpt, _ := ethx.AddressFrom32(m.Recipient)
	if a := f.admins[rcpt]; a != nil {
		if a.successor != (common.Address{}) {
			return revert("RetiredAdmin", "")
		}
		kind, seq, _ := gnolight.DecisionPrefix(m.Body)
		switch {
		case seq > a.lastSeq+1:
			return revert("OutOfOrder", "")
		case seq == a.lastSeq+1:
			a.lastSeq = seq
			a.decisions[seq] = &ethx.Decision{Number: seq, Kind: kind, DeliveredBlock: f.block + 1, DeliveredTime: f.now + 12, Deliverer: f.from, Params: m.Body[9:]}
			if kind == kindCancel {
				delete(a.decisions, seq)
			}
		}
		f.delivered[id] = true
		return nil
	}
	tok := -1
	for i, t := range f.tokens {
		if t == rcpt {
			tok = i
		}
	}
	if tok < 0 {
		return revert("NoRecipient", "")
	}
	for _, fl := range f.flags {
		if fl.Set() {
			return revert("Paused", "")
		}
	}
	if f.quarantineEnd > f.now {
		return revert("Quarantine", "")
	}
	_, amount, _, _ := hl.ParseTokenMessage(m.Body)
	l := &f.limits[tok]
	if amount > l.MaxPerMessage {
		return revert("OverMax", "")
	}
	level := l.Level(int64(f.now), 86_400/int64(f.gateFactor))
	if level+amount > l.DayCap {
		return revert("OverDayCap", "")
	}
	l.Used, l.UsedAt = level+amount, f.now
	f.delivered[id] = true
	return nil
}

// execute is HlAdmin.execute: a pending decision, mature by the chain's time.
func (f *fakeEth) execute(admin common.Address, n uint64) error {
	a := f.admins[admin]
	d := a.decisions[n]
	if d == nil {
		return revert("NoDecision", "")
	}
	st := &ethx.AdminState{Hold: a.hold, Holds: a.holds, Cancelled: a.cancelled, Flags: a.flags, ClockFactor: a.clockFactor}
	if ok, why := executeWhen(d, st, int64(f.now)); !ok {
		if bytes.Contains([]byte(why), []byte("deferred")) {
			return revert("Deferred", why)
		}
		return revert("NotMature", why)
	}
	delete(a.decisions, n)
	return nil
}

func (f *fakeEth) sentOf(method string) []ethx.Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ethx.Call
	for _, c := range f.sent {
		if _, name, _, err := f.methodOf(c); err == nil && name == method {
			out = append(out, c)
		}
	}
	return out
}

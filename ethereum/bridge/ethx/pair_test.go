package ethx

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// provider is a fake provider's chain: a finalized number, a hash family, per-call answers.
type provider struct {
	finalized uint64
	seed      byte                // the hash family of its blocks
	forkFrom  uint64              // from this number on its blocks are of family seed+1 (0: never)
	call      func([]byte) []byte // eth_call's answer
	logs      []types.Log
	code      []byte
	slot      common.Hash
	timeX     uint64 // added to every block's time (a provider lying about it)
}

func (p *provider) hash(n uint64) common.Hash {
	if p.forkFrom != 0 && n >= p.forkFrom {
		return hashOf(p.seed+1, n)
	}
	return hashOf(p.seed, n)
}

func (p *provider) client(name string) *Client {
	f := newFakeRPC()
	f.on("eth_getBlockByNumber", func(args []any) (any, error) {
		n := p.finalized
		if s, ok := args[0].(string); ok && s != "finalized" && s != "latest" {
			v, err := hexutil.DecodeUint64(s)
			if err != nil {
				return nil, err
			}
			if v > p.finalized+10 {
				return nil, nil
			}
			n = v
		}
		return blockJSON(n, p.hash(n), 1_800_000_000+n*12+p.timeX, big.NewInt(1)), nil
	})
	f.on("eth_call", func(args []any) (any, error) {
		return hexutil.Bytes(p.call(args[0].(map[string]any)["data"].(hexutil.Bytes))), nil
	})
	f.on("eth_getLogs", func(args []any) (any, error) { return p.logs, nil })
	f.on("eth_getCode", func([]any) (any, error) { return hexutil.Bytes(p.code), nil })
	f.on("eth_getStorageAt", func([]any) (any, error) { return p.slot, nil })
	return NewClient(Provider{Name: name, URL: "https://" + name + ".example/KEY"}, f)
}

func pairOf(t *testing.T, a, b *provider) Pair {
	t.Helper()
	p, err := NewPair(a.client("a"), b.client("b"), false)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The finalized block both agree on: the lower number when one is ahead with the same chain; a disagreement when
// their hashes at it differ.
func TestPairFinalized(t *testing.T) {
	ctx := context.Background()
	b, err := pairOf(t, &provider{finalized: 100, seed: 1}, &provider{finalized: 100, seed: 1}).Finalized(ctx)
	if err != nil || b.Number != 100 || b.Hash != hashOf(1, 100) {
		t.Fatalf("%+v %v", b, err)
	}
	b, err = pairOf(t, &provider{finalized: 103, seed: 1}, &provider{finalized: 100, seed: 1}).Finalized(ctx)
	if err != nil || b.Number != 100 {
		t.Fatalf("one ahead: %+v %v", b, err)
	}
	if _, err := pairOf(t, &provider{finalized: 100, seed: 1}, &provider{finalized: 100, seed: 2}).Finalized(ctx); !errors.Is(err, ErrDisagree) {
		t.Fatalf("two hashes at one number: %v", err)
	}
	if _, err := pairOf(t, &provider{finalized: 103, seed: 1, forkFrom: 99}, &provider{finalized: 100, seed: 1}).Finalized(ctx); !errors.Is(err, ErrDisagree) {
		t.Fatalf("the ahead one on another chain at the lower number: %v", err)
	}
	if _, err := NewPair(NewClient(Provider{URL: "https://x/"}, newFakeRPC()), NewClient(Provider{URL: "https://x/"}, newFakeRPC()), false); err == nil {
		t.Fatal("one provider twice made a pair")
	}
}

// An eth_call pinned by hash agrees or is refused; tags are refused (a pair reads one state).
func TestPairCall(t *testing.T) {
	ctx := context.Background()
	same := func([]byte) []byte { return []byte{1, 2} }
	other := func([]byte) []byte { return []byte{1, 3} }
	at := AtHash(hashOf(1, 100))
	if out, err := pairOf(t, &provider{call: same}, &provider{call: same}).CallAt(ctx, target, nil, at); err != nil || string(out) != "\x01\x02" {
		t.Fatalf("%x %v", out, err)
	}
	if _, err := pairOf(t, &provider{call: same}, &provider{call: other}).CallAt(ctx, target, nil, at); !errors.Is(err, ErrDisagree) {
		t.Fatalf("%v", err)
	}
	if _, err := pairOf(t, &provider{call: same}, &provider{call: same}).CallAt(ctx, target, nil, Latest); err == nil {
		t.Fatal("a pair read at a tag")
	}
	outs, errs, err := pairOf(t, &provider{call: same}, &provider{call: other}).CallBatch(ctx, []CallReq{{To: target}}, at)
	if err != nil || !errors.Is(errs[0], ErrDisagree) || outs == nil {
		t.Fatalf("batch: %v %v", errs, err)
	}
	if h, err := pairOf(t, &provider{code: []byte{0x60}}, &provider{code: []byte{0x60}}).CodeHashAt(ctx, target, at); err != nil || h == (common.Hash{}) {
		t.Fatalf("%v", err)
	}
	if _, err := pairOf(t, &provider{code: []byte{0x60}}, &provider{code: []byte{0x61}}).CodeHashAt(ctx, target, at); !errors.Is(err, ErrDisagree) {
		t.Fatalf("%v", err)
	}
	impl := common.HexToHash("0x000000000000000000000000000000000000000000000000000000000000beef")
	if a, err := pairOf(t, &provider{slot: impl}, &provider{slot: impl}).ImplementationAt(ctx, target, at); err != nil || a != common.HexToAddress("0xbeef") {
		t.Fatalf("%s %v", a.Hex(), err)
	}
}

// Logs are taken only when both providers give the same ones and each one's block hash is the canonical hash of its
// number from both.
func TestPairLogs(t *testing.T) {
	ctx := context.Background()
	log := func(n uint64, idx uint, h common.Hash, data byte) types.Log {
		return types.Log{Address: target, Topics: []common.Hash{Mailbox.Event("Dispatch")}, Data: []byte{data}, BlockNumber: n, BlockHash: h, TxHash: common.Hash{9}, Index: idx}
	}
	good := []types.Log{log(95, 1, hashOf(1, 95), 1), log(90, 0, hashOf(1, 90), 2)}
	a := &provider{finalized: 100, seed: 1, logs: good}
	b := &provider{finalized: 100, seed: 1, logs: []types.Log{good[1], good[0]}} // another order is no disagreement
	out, err := pairOf(t, a, b).Logs(ctx, LogQuery{Addresses: []common.Address{target}}, 80, 100)
	if err != nil || len(out) != 2 || out[0].BlockNumber != 90 {
		t.Fatalf("%v %v", out, err)
	}
	b.logs = []types.Log{good[0], log(90, 0, hashOf(1, 90), 3)}
	if _, err := pairOf(t, a, b).Logs(ctx, LogQuery{}, 80, 100); !errors.Is(err, ErrDisagree) {
		t.Fatalf("different data: %v", err)
	}
	b.logs = good[:1]
	if _, err := pairOf(t, a, b).Logs(ctx, LogQuery{}, 80, 100); !errors.Is(err, ErrDisagree) {
		t.Fatalf("a log one provider omits: %v", err)
	}
	stale := []types.Log{log(95, 1, hashOf(7, 95), 1)} // a block hash that is not the canonical one
	if _, err := pairOf(t, &provider{finalized: 100, seed: 1, logs: stale}, &provider{finalized: 100, seed: 1, logs: stale}).Logs(ctx, LogQuery{}, 80, 100); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("a non-canonical block hash: %v", err)
	}
}

// A provider's log chunks are its own (LogRange), and a chunk it refuses as too wide is halved until it answers.
func TestLogsRangeChunksAndHalves(t *testing.T) {
	f := newFakeRPC()
	var ranges [][2]uint64
	f.on("eth_getLogs", func(args []any) (any, error) {
		q := args[0].(map[string]any)
		lo, _ := hexutil.DecodeUint64(q["fromBlock"].(string))
		hi, _ := hexutil.DecodeUint64(q["toBlock"].(string))
		ranges = append(ranges, [2]uint64{lo, hi})
		if hi-lo+1 > 30 {
			return nil, errors.New("block range is too large: max 30 blocks")
		}
		return []types.Log{}, nil
	})
	c := NewClient(Provider{Name: "narrow", URL: "https://n.example/", LogRange: 100}, f)
	if _, err := c.LogsRange(context.Background(), LogQuery{}, 1, 100); err != nil {
		t.Fatal(err)
	}
	if ranges[0] != [2]uint64{1, 100} || ranges[1] != [2]uint64{1, 50} || ranges[2] != [2]uint64{1, 25} {
		t.Fatalf("%v", ranges)
	}
	last := ranges[len(ranges)-1]
	if last[1] != 100 {
		t.Fatalf("the range was not read to its end: %v", ranges)
	}
	for _, r := range ranges[2:] {
		if r[1]-r[0]+1 > 30 {
			t.Fatalf("a refused width asked again: %v", ranges)
		}
	}
}

// A read the provider has no state for is no verdict, never a revert or a zero.
func TestIsNoState(t *testing.T) {
	for _, msg := range []string{"missing trie node 0xabc (path ) state is not available", "header not found", "required historical state unavailable (reexec=128)"} {
		err := (&Client{Provider: Provider{Name: "p"}}).wrap(errors.New(msg))
		if !IsNoState(err) {
			t.Errorf("%q not read as no state", msg)
		}
	}
	if IsNoState((&Client{Provider: Provider{Name: "p"}}).wrap(revertWith("x"))) || IsNoState(errors.New("connection refused")) {
		t.Fatal("a revert or a dead provider read as no state")
	}
}

package ethx

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
)

// Lane F17a (audit round 5's go-spend, E5-GS4; the plan red team's E5-RT3).

// cappedReader is a reader that answers the light client's entry view, and a batch of more than limit calls as geth
// does past its --rpc.batch-request-limit: every element missing a response (go-ethereum's client sets
// ErrMissingBatchResponse on each).
type cappedReader struct {
	limit, batches, largest int
}

func (c *cappedReader) CallAt(_ context.Context, _ common.Address, data []byte, _ At) ([]byte, error) {
	m, err := LightClient.ABI.MethodById(data[:4])
	if err != nil || m.Name != "entry" {
		return nil, errors.New("no such view in the fake")
	}
	args, err := m.Inputs.Unpack(data[4:])
	if err != nil {
		return nil, err
	}
	h := args[0].([32]byte)
	return m.Outputs.Pack(struct {
		Epoch           uint64   `json:"epoch"`
		Height          int64    `json:"height"`
		LastResultsHash [32]byte `json:"lastResultsHash"`
	}{1, int64(h[31]) + 1, h})
}

func (c *cappedReader) CallBatch(ctx context.Context, reqs []CallReq, at At) ([][]byte, []error, error) {
	c.batches++
	c.largest = max(c.largest, len(reqs))
	res, errs := make([][]byte, len(reqs)), make([]error, len(reqs))
	for i, q := range reqs {
		if len(reqs) > c.limit {
			errs[i] = rpc.ErrMissingBatchResponse
			continue
		}
		res[i], errs[i] = c.CallAt(ctx, q.To, q.Data, at)
	}
	return res, errs, nil
}

// E5-GS4: every view of a read went in one JSON-RPC batch, and the walk read every event's entry in one: past a
// provider's cap (geth's default 1,000) the batch failed whole, every step, and gno.land → Ethereum stopped. A read is
// made in batches of at most DefaultBatchSize.
func TestF17aReadsSplitIntoBatches(t *testing.T) {
	r := &cappedReader{limit: 1000}
	hashes := make([][32]byte, 2500)
	for i := range hashes {
		hashes[i] = [32]byte{30: byte(i >> 8), 31: byte(i)}
	}
	es, err := ReadEntries(context.Background(), r, common.Address{1}, hashes, Latest)
	t.Logf("2,500 entries through a provider of geth's batch cap: %d batches, the largest %d calls (err %v)", r.batches, r.largest, err)
	if err != nil || len(es) != len(hashes) || es[2499].Height != int64(hashes[2499][31])+1 {
		t.Fatalf("2,500 entries through a provider of geth's batch cap of 1,000: %v", err)
	}
	if r.largest > DefaultBatchSize {
		t.Fatalf("a batch of %d calls", r.largest)
	}
}

// A provider whose cap lies below the batch the client sends — geth answers "batch too large" on the first element and
// a missing response on every other — is asked again in halves until it answers.
func TestF17aClientHalvesABatchTooLarge(t *testing.T) {
	f := newFakeRPC()
	f.on("eth_call", func([]any) (any, error) { return hexutil.Bytes{1}, nil })
	cap := 30
	sizes := []int{}
	c := NewClient(Provider{Name: "small", URL: "https://s.example/"}, &batchCapRPC{fakeRPC: f, limit: cap, sizes: &sizes})
	reqs := make([]CallReq, 250)
	out, errs, err := c.CallBatch(context.Background(), reqs, Latest)
	if err != nil {
		t.Fatal(err)
	}
	for i := range reqs {
		if errs[i] != nil || len(out[i]) != 1 {
			t.Fatalf("call %d of 250 through a provider capped at %d: %v (batches %v)", i, cap, errs[i], sizes)
		}
	}
	t.Logf("250 calls through a provider capped at %d: batches %v", cap, sizes)
}

// batchCapRPC answers a batch past limit as geth does: "batch too large" on the first element, a missing response on
// the rest.
type batchCapRPC struct {
	*fakeRPC
	limit int
	sizes *[]int
}

func (b *batchCapRPC) BatchCallContext(ctx context.Context, elems []rpc.BatchElem) error {
	*b.sizes = append(*b.sizes, len(elems))
	if len(elems) > b.limit {
		for i := range elems {
			elems[i].Error = rpc.ErrMissingBatchResponse
		}
		elems[0].Error = errors.New("batch too large")
		return nil
	}
	return b.fakeRPC.BatchCallContext(ctx, elems)
}

// signedJSON is a transaction as a provider serves it (eth_getTransactionByHash), its "from" field what the provider
// says.
func signedJSON(t *testing.T, key *ecdsa.PrivateKey, to common.Address, input []byte, from common.Address) (common.Hash, map[string]any) {
	t.Helper()
	signer := types.LatestSignerForChainID(big.NewInt(1))
	tx, err := types.SignNewTx(key, signer, &types.DynamicFeeTx{ChainID: big.NewInt(1), Nonce: 7, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2), Gas: 100_000, To: &to, Data: input})
	if err != nil {
		t.Fatal(err)
	}
	b, err := tx.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["from"] = from
	m["blockNumber"] = "0x10"
	m["blockHash"] = common.Hash{1}
	return tx.Hash(), m
}

// The plan red team's E5-RT3: the relayer's "booked, delivered at no fee check" rested on one provider's word about a
// transaction's sender — its JSON "from" field. The sender is recovered from the signature: a provider that names
// another sender is not believed, and one that changes the calldata answers a transaction that hashes to another hash.
func TestF17aSignedTransactionRecoversTheSigner(t *testing.T) {
	key, _ := crypto.GenerateKey()
	signer := crypto.PubkeyToAddress(key.PublicKey)
	updater := common.HexToAddress("0x0000000000000000000000000000000000000a01")
	to := common.HexToAddress("0x00000000000000000000000000000000000000c1")
	h, honest := signedJSON(t, key, to, []byte("an update and its trailer"), signer)
	_, lying := signedJSON(t, key, to, []byte("an update and its trailer"), updater)
	f := newFakeRPC()
	answer := honest
	f.on("eth_getTransactionByHash", func([]any) (any, error) { return answer, nil })
	c := NewClient(Provider{Name: "a", URL: "https://a.example/"}, f)
	tx, err := c.SignedTransaction(context.Background(), h)
	if err != nil || tx.From != signer || string(tx.Input) != "an update and its trailer" || tx.To == nil || *tx.To != to {
		t.Fatalf("an honest answer: %+v, %v", tx, err)
	}
	answer = lying // the provider says the updater sent it
	if tx, err := c.SignedTransaction(context.Background(), h); err != nil || tx.From != signer {
		t.Fatalf("a provider that names eth.updater as the sender: %+v, %v", tx, err)
	}
	forged := map[string]any{}
	for k, v := range honest {
		forged[k] = v
	}
	forged["input"] = hexutil.Bytes("another trailer")
	answer = forged // the calldata changed: no longer the transaction asked for
	if tx, err := c.SignedTransaction(context.Background(), h); err == nil || !strings.Contains(err.Error(), "hashes to") {
		t.Fatalf("a provider that changes the calldata: %+v, %v", tx, err)
	}
}

// The review of the lane (R4): after a restart, before any send, the sender's InFlight read its state unloaded — ""
// though its file holds a transaction in flight — and the updater ended a pending booking by "nothing of this key in
// flight": a header transaction stuck across a restart could mine after its booking was dropped. The state is read first.
func TestF17aInFlightReadsTheStateFirst(t *testing.T) {
	path := t.TempDir() + "/sender.json"
	st := `{"inflight":{"nonce":7,"hashes":["0x` + strings.Repeat("ab", 32) + `"],"tip":"0x1","feeCap":"0x2","gas":21000,"to":"0x00000000000000000000000000000000000000c1","data":"0x","label":"update 106 (1/1)","firstAt":"2026-10-15T00:00:00Z","sentAt":"2026-10-15T00:00:00Z"}}`
	if err := os.WriteFile(path, []byte(st), 0o600); err != nil {
		t.Fatal(err)
	}
	key, _ := crypto.GenerateKey()
	s, err := NewSender(key, big.NewInt(1), []*Client{NewClient(Provider{Name: "a", URL: "https://a.example/"}, newFakeRPC())}, SendConfig{State: path})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.InFlight(); !strings.Contains(got, "update 106") {
		t.Fatalf("a restarted sender whose state holds a transaction in flight says %q", got)
	}
}

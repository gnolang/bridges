package ethx

import (
	"context"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// Fold F3's tests of the sender's new calls (eth/bridge/M3b-NOTES.md, "Fold F3"): a contract creation (a transaction
// with no "to", which hleth setup sends), the record a caller writes before each version is broadcast, the
// resumption of a transaction a stopped run left in flight, and on a real node the backup's takeover of the primary's
// pending transaction.

// A creation is a transaction with no "to", estimated without one; BeforeBroadcast hears its nonce and hash before
// it is broadcast (a caller records the address CREATE gives it), and its error stops the call; a creation that names
// a "to" is refused.
func TestSenderCreates(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{})
	ctx := context.Background()
	c.autoMine = true
	var heard []uint64
	var hashes []common.Hash
	rc, err := s.Send(ctx, Call{Create: true, Data: []byte{0x60, 0x00, 0x60, 0x00, 0xf3}, Label: "a creation", BeforeBroadcast: func(n uint64, h common.Hash) error {
		heard, hashes = append(heard, n), append(hashes, h)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	tx := c.sent[0]
	if tx.To() != nil || len(heard) != 1 || heard[0] != 0 || hashes[0] != tx.Hash() || rc.TxHash != tx.Hash() {
		t.Fatalf("to %v, heard %v %v, sent %s", tx.To(), heard, hashes, tx.Hash().Hex())
	}
	if _, err := s.Send(ctx, Call{Create: true, To: target, Data: []byte{1}, Label: "a creation with a to"}); err == nil {
		t.Fatal("a creation naming a to was sent")
	}
	stop := errors.New("the record could not be written")
	if _, err := s.Send(ctx, Call{Create: true, Data: []byte{2}, Label: "unrecorded", BeforeBroadcast: func(uint64, common.Hash) error { return stop }}); !errors.Is(err, stop) {
		t.Fatalf("a refused record: %v", err)
	}
	if len(c.sent) != 1 {
		t.Fatalf("%d sent: a creation went without its record", len(c.sent))
	}
}

// Resume finishes the transaction a stopped run left in flight: a new sender over the same state file waits for it
// and answers its receipt; one the pool dropped is broadcast again at once.
func TestSenderResumes(t *testing.T) {
	state := filepath.Join(t.TempDir(), "sender.json")
	s, c, _ := testSender(t, SendConfig{State: state, Wait: time.Minute})
	ctx := context.Background()
	if _, err := s.Send(ctx, Call{Create: true, Data: []byte{0x60, 0x00}, Label: "a creation"}); !errors.Is(err, ErrStillPending) {
		t.Fatalf("unmined: %v", err)
	}
	first := c.sent[0]
	delete(c.pool, first.Nonce()) // the pool dropped it while the run was down
	s2, err := NewSender(s.Key, s.ChainID, s.Clients, SendConfig{State: state, Wait: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	s2.now, s2.sleep = s.now, s.sleep
	c.autoMine = true
	rc, err := s2.Resume(ctx)
	if err != nil || rc == nil || rc.TxHash != first.Hash() {
		t.Fatalf("resume: %+v %v", rc, err)
	}
	if rc, err := s2.Resume(ctx); rc != nil || err != nil {
		t.Fatalf("nothing in flight: %+v %v", rc, err)
	}
}

// On a real node: a creation through the sender lands at CREATE(key, nonce); then, mining off, a primary leaves a
// transaction pending at the shared nonce and goes silent; the backup sees the key quiet for its span, sends, and its
// transaction replaces the primary's at that nonce with both fees 12.5% over the pending one's — the node's pool takes
// it, and the primary's is never mined.
func TestSenderCreatesAndTakesOverOnAnvil(t *testing.T) {
	c := startAnvil(t)
	ctx := context.Background()
	key, err := crypto.HexToECDSA(anvilKey)
	if err != nil {
		t.Fatal(err)
	}
	id := big.NewInt(31337)
	code, err := os.ReadFile(filepath.Join("testdata", "probe", "probe.bin"))
	if err != nil {
		t.Fatal(err)
	}
	bin, err := hexutil.Decode(strings.TrimSpace(string(code)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := SendConfig{MaxBaseFee: GweiOf(50), Poll: 100 * time.Millisecond, Wait: 30 * time.Second}
	primaryCfg := cfg
	primaryCfg.MinTip, primaryCfg.Wait = GweiOf(1.7), 300*time.Millisecond
	primary, err := NewSender(key, id, []*Client{c}, primaryCfg)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := primary.Send(ctx, Call{Create: true, Data: bin, Label: "the probe"})
	if err != nil || rc.Status != 1 {
		t.Fatalf("the creation: %+v %v", rc, err)
	}
	probe := crypto.CreateAddress(primary.From, 0)
	if rc.ContractAddress != probe {
		t.Fatalf("created at %s, not CREATE(key, 0) %s", rc.ContractAddress.Hex(), probe.Hex())
	}
	if got, err := c.CodeAt(ctx, probe, Latest); err != nil || len(got) == 0 {
		t.Fatalf("no code at %s: %v", probe.Hex(), err)
	}
	ok := append([]byte{}, crypto.Keccak256([]byte("ok()"))[:4]...) // the probe's write
	var b bool
	if err := c.rpc.CallContext(ctx, &b, "evm_setAutomine", false); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.Send(ctx, Call{To: probe, Data: ok, Label: "the primary's call"}); !errors.Is(err, ErrStillPending) {
		t.Fatalf("the primary's call: %v", err)
	}
	theirs, err := c.PendingAt(ctx, primary.From, 1)
	if err != nil || theirs == nil {
		t.Fatalf("the pool shows no transaction at nonce 1: %+v %v", theirs, err)
	}
	backup, err := NewSender(key, id, []*Client{c}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if q, err := backup.KeyQuiet(ctx, 400*time.Millisecond); err != nil || q {
		t.Fatalf("the first look is a move: %v %v", q, err)
	}
	time.Sleep(500 * time.Millisecond)
	if q, err := backup.KeyQuiet(ctx, 400*time.Millisecond); err != nil || !q {
		t.Fatalf("the key is quiet (the primary silent): %v %v", q, err)
	}
	done := make(chan *types.Receipt, 1)
	errc := make(chan error, 1)
	go func() {
		rc, err := backup.Send(ctx, Call{To: probe, Data: ok, Label: "the backup's call"})
		errc <- err
		done <- rc
	}()
	var replaced *PendingTx
	for i := 0; i < 100; i++ {
		if p, err := c.PendingAt(ctx, primary.From, 1); err == nil && p != nil && p.Hash != theirs.Hash {
			replaced = p
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if replaced == nil {
		t.Fatal("the backup's transaction never replaced the primary's in the pool")
	}
	var mined any
	if err := c.rpc.CallContext(ctx, &mined, "evm_mine"); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	rc = <-done
	if rc.TxHash != replaced.Hash {
		t.Fatalf("mined %s, not the backup's %s", rc.TxHash.Hex(), replaced.Hash.Hex())
	}
	if replaced.Tip.Cmp(bumped(theirs.Tip)) < 0 || replaced.FeeCap.Cmp(bumped(theirs.FeeCap)) < 0 {
		t.Fatalf("the backup's tip %s and fee cap %s are not both 12.5%% over the primary's %s and %s", replaced.Tip, replaced.FeeCap, theirs.Tip, theirs.FeeCap)
	}
	if tx, err := c.Transaction(ctx, rc.TxHash); err != nil || tx.Nonce != 1 {
		t.Fatalf("mined at nonce %v: %v", tx, err)
	}
	if err := primary.Settle(ctx); err != nil {
		t.Fatalf("the primary's record of its replaced transaction: %v", err)
	}
	if primary.InFlight() != "" {
		t.Fatal("the primary still holds its replaced transaction")
	}
}

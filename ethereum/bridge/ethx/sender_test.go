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

// testSender is a sender over a fake chain, its clock fake too: each sleep advances it and runs tick.
func testSender(t *testing.T, cfg SendConfig) (*Sender, *chain, *time.Time) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	id := big.NewInt(31337)
	c := newChain(KeyAddress(key), id)
	s, err := NewSender(key, id, []*Client{NewClient(Provider{Name: "a", URL: "https://a.example/v2/SECRET"}, c.rpc()), NewClient(Provider{Name: "b", URL: "https://b.example/?key=SECRET"}, c.rpc())}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	s.sleep = func(ctx context.Context, d time.Duration) error {
		now = now.Add(d)
		return ctx.Err()
	}
	return s, c, &now
}

var target = common.HexToAddress("0x00000000000000000000000000000000000000aa")

// A call is estimated, given 20% more gas, a fee cap of twice the base fee plus the tip, broadcast to every
// provider and its receipt awaited.
func TestSenderSends(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{})
	c.autoMine = true
	rc, err := s.Send(context.Background(), Call{To: target, Data: []byte{1, 2, 3}, Label: "a call"})
	if err != nil {
		t.Fatal(err)
	}
	if rc.Status != types.ReceiptStatusSuccessful || len(c.sent) != 1 {
		t.Fatalf("receipt %+v, %d sent", rc, len(c.sent))
	}
	tx := c.sent[0]
	if tx.Gas() != 120_000 {
		t.Fatalf("gas %d, want the estimate plus 20%%", tx.Gas())
	}
	wantCap := new(big.Int).Add(new(big.Int).Mul(GweiOf(1), big.NewInt(2)), GweiOf(0.1))
	if tx.GasFeeCap().Cmp(wantCap) != 0 || tx.GasTipCap().Cmp(GweiOf(0.1)) != 0 || tx.Type() != types.DynamicFeeTxType {
		t.Fatalf("fees: cap %s tip %s type %d", tx.GasFeeCap(), tx.GasTipCap(), tx.Type())
	}
	if s.InFlight() != "" {
		t.Fatal("an in-flight record after the receipt")
	}
	// a tip the provider suggests above the bound is held under it, low enough that the replacements before the
	// stuck alarm all fit under MaxTip; one below the floor is raised to it
	c.tip = GweiOf(50)
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{4}, Label: "b"}); err != nil {
		t.Fatal(err)
	}
	got := c.sent[1].GasTipCap()
	if got.Cmp(s.firstTipCeiling()) != 0 || got.Cmp(GweiOf(1.4)) < 0 {
		t.Fatalf("tip %s, want the first-version ceiling %s", got, s.firstTipCeiling())
	}
	top := new(big.Int).Set(got)
	for i := 0; i < StuckAfter; i++ {
		top = bumped(top)
	}
	if top.Cmp(GweiOf(2)) > 0 {
		t.Fatalf("%d replacements of a %s tip reach %s, above the 2 gwei bound", StuckAfter, got, top)
	}
	c.tip = big.NewInt(0)
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{5}, Label: "c"}); err != nil {
		t.Fatal(err)
	}
	if got := c.sent[2].GasTipCap(); got.Cmp(GweiOf(0.01)) != 0 {
		t.Fatalf("tip %s, want the 0.01 gwei floor", got)
	}
}

// A call that would revert is refused before sending, its reason decoded; a base fee above the maximum sends
// nothing and says so (the caller parks).
func TestSenderRefusesBeforeSending(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{})
	c.estimate = func([]byte) (uint64, error) { return 0, revertWith("Mailbox: already delivered") }
	_, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "x"})
	r, ok := AsRevert(err)
	if !ok || r.Name != "Error" || r.Reason != "Mailbox: already delivered" || r.Mined || !RevertIs(err, "already delivered") || Transient(err) {
		t.Fatalf("%v (%+v)", err, r)
	}
	if len(c.sent) != 0 {
		t.Fatal("a reverting call was sent")
	}
	c.estimate = func([]byte) (uint64, error) { return 100_000, nil }
	c.baseFee = GweiOf(6)
	_, err = s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "x"})
	var ft *FeeTooHigh
	if !errors.As(err, &ft) || !Transient(err) || len(c.sent) != 0 {
		t.Fatalf("%v, %d sent", err, len(c.sent))
	}
	// a call past the transaction gas cap
	c.baseFee = GweiOf(1)
	c.estimate = func([]byte) (uint64, error) { return GasCap + 1, nil }
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "big"}); !errors.Is(err, ErrGasCap) || Transient(err) {
		t.Fatalf("%v", err)
	}
	// an estimate under the cap whose 20% margin is not (EIP-7825): refused too — the caller splits it, never sends
	// it with less margin
	c.estimate = func([]byte) (uint64, error) { return GasCap - 10, nil }
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "near"}); !errors.Is(err, ErrGasCap) {
		t.Fatalf("%v", err)
	}
	// the largest estimate whose margin fits goes, its gas limit at most the cap
	c.estimate = func([]byte) (uint64, error) { return GasCap * 5 / 6, nil }
	c.autoMine = true
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "fits"}); err != nil {
		t.Fatal(err)
	}
	if g := c.sent[0].Gas(); g > GasCap || g != GasCap*5/6+GasCap*5/6/5 {
		t.Fatalf("gas %d", g)
	}
}

// A transaction not mined in 3 minutes is replaced at its nonce with 12.5% more tip and fee cap; whichever version
// is mined is the receipt.
func TestSenderReplaces(t *testing.T) {
	s, c, now := testSender(t, SendConfig{Poll: 10 * time.Second, Wait: 10 * time.Minute})
	start := *now
	mineAt := start.Add(200 * time.Second)
	s.sleep = func(ctx context.Context, d time.Duration) error {
		*now = now.Add(d)
		if !now.Before(mineAt) {
			c.mine()
		}
		return nil
	}
	rc, err := s.Send(context.Background(), Call{To: target, Data: []byte{9}, Label: "slow"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.sent) != 2 {
		t.Fatalf("%d versions sent, want the first and one replacement", len(c.sent))
	}
	a, b := c.sent[0], c.sent[1]
	if a.Nonce() != b.Nonce() || b.GasTipCap().Cmp(mulDiv(a.GasTipCap(), 1125, 1000)) < 0 || b.GasFeeCap().Cmp(mulDiv(a.GasFeeCap(), 1125, 1000)) < 0 {
		t.Fatalf("replacement: nonce %d/%d tip %s→%s cap %s→%s", a.Nonce(), b.Nonce(), a.GasTipCap(), b.GasTipCap(), a.GasFeeCap(), b.GasFeeCap())
	}
	if rc.TxHash != b.Hash() {
		t.Fatal("the receipt is not the replacement's")
	}
}

// A nonce that survives three replacements is said (the machine's alarm); each version rises 12.5% over the one
// before in tip and fee cap alike, even as the base fee falls.
func TestSenderStuckAfterThreeReplacements(t *testing.T) {
	s, c, now := testSender(t, SendConfig{Poll: 30 * time.Second, Wait: 15 * time.Minute, MaxTip: GweiOf(10)})
	var said []int
	s.OnStuck = func(label string, nonce uint64, n int) { said = append(said, n) }
	s.sleep = func(ctx context.Context, d time.Duration) error {
		*now = now.Add(d)
		c.baseFee = mulDiv(c.baseFee, 9, 10) // a falling base fee lowers no replacement
		return nil
	}
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "stuck"}); !errors.Is(err, ErrStillPending) {
		t.Fatalf("%v", err)
	}
	if len(said) == 0 || said[0] != StuckAfter {
		t.Fatalf("said %v", said)
	}
	for i := 1; i < len(c.sent); i++ {
		a, b := c.sent[i-1], c.sent[i]
		if b.GasTipCap().Cmp(mulDiv(a.GasTipCap(), 1125, 1000)) < 0 || b.GasFeeCap().Cmp(mulDiv(a.GasFeeCap(), 1125, 1000)) < 0 {
			t.Fatalf("version %d: tip %s→%s cap %s→%s", i, a.GasTipCap(), b.GasTipCap(), a.GasFeeCap(), b.GasFeeCap())
		}
	}
}

// The bounds stop the bumps: no replacement past the tip bound; the wait answers ErrStillPending and keeps the
// record, which the next Send resumes first.
func TestSenderStillPendingResumes(t *testing.T) {
	dir := t.TempDir()
	cfg := SendConfig{Poll: 10 * time.Second, Wait: time.Minute, MaxTip: GweiOf(0.1), State: filepath.Join(dir, "sender.json")}
	s, c, _ := testSender(t, cfg)
	_, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "first"})
	if !errors.Is(err, ErrStillPending) || !Transient(err) {
		t.Fatalf("%v", err)
	}
	if s.InFlight() == "" {
		t.Fatal("no in-flight record")
	}
	st, err := os.Stat(cfg.State)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v %v", st, err)
	}
	// a fresh sender (a restart) reads the record: its next Send resolves the first (mined meanwhile), then sends
	s2, err := NewSender(s.Key, s.ChainID, s.Clients, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s2.now, s2.sleep = s.now, s.sleep
	c.mine()
	c.autoMine = true
	if _, err := s2.Send(context.Background(), Call{To: target, Data: []byte{2}, Label: "second"}); err != nil {
		t.Fatal(err)
	}
	if len(c.sent) != 2 || c.sent[1].Nonce() != c.sent[0].Nonce()+1 {
		t.Fatalf("sent %d, nonces %d %d", len(c.sent), c.sent[0].Nonce(), c.sent[len(c.sent)-1].Nonce())
	}
}

// A mined revert is read for its reason at the block before; a nonce another transaction took is ErrNonceTaken
// (the call was not made, tried again).
func TestSenderMinedRevertAndTakenNonce(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{})
	c.autoMine = true
	c.revertOn[hexutil.Encode([]byte{7})] = true
	c.callErr = func(data []byte) error {
		if len(data) == 1 && data[0] == 7 {
			return revertWith("HlAdmin: not mature")
		}
		return nil
	}
	rc, err := s.Send(context.Background(), Call{To: target, Data: []byte{7}, Label: "reverts when mined"})
	r, ok := AsRevert(err)
	if !ok || !r.Mined || r.Reason != "HlAdmin: not mature" || rc == nil || rc.Status != types.ReceiptStatusFailed {
		t.Fatalf("%v %+v", err, rc)
	}
	c.autoMine = false
	s.sleep = func(ctx context.Context, d time.Duration) error {
		if len(c.pool) > 0 {
			c.takeNonce()
		}
		return nil
	}
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{8}, Label: "taken"}); !errors.Is(err, ErrNonceTaken) {
		t.Fatalf("%v", err)
	}
	if s.InFlight() != "" {
		t.Fatal("the taken record stays")
	}
}

// A key's transaction already pending at the nonce (another machine's) is replaced when ours pays 12.5% more,
// bumped until the pool takes it.
func TestSenderReplacesAForeignPending(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{})
	foreign := types.NewTx(&types.DynamicFeeTx{ChainID: s.ChainID, Nonce: 0, GasTipCap: GweiOf(0.11), GasFeeCap: GweiOf(2.2), Gas: 21000, To: &target, Value: big.NewInt(0)})
	signed, err := types.SignTx(foreign, types.LatestSignerForChainID(s.ChainID), s.Key)
	if err != nil {
		t.Fatal(err)
	}
	c.pool[0] = signed
	c.autoMine = true
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{3}, Label: "over a stuck one"}); err != nil {
		t.Fatal(err)
	}
	last := c.sent[len(c.sent)-1]
	if last.Nonce() != 0 || last.GasTipCap().Cmp(mulDiv(GweiOf(0.11), 11, 10)) < 0 {
		t.Fatalf("nonce %d tip %s", last.Nonce(), last.GasTipCap())
	}
}

// The backup's rule: the key is quiet once its nonce has moved by nobody else's send for the whole span; the
// first look counts as a move; this sender's own sends never count.
func TestKeyQuiet(t *testing.T) {
	s, c, now := testSender(t, SendConfig{})
	ctx := context.Background()
	quiet := func() bool {
		q, err := s.KeyQuiet(ctx, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	if quiet() {
		t.Fatal("quiet at the first look")
	}
	*now = now.Add(61 * time.Second)
	if !quiet() {
		t.Fatal("not quiet after a minute of nothing")
	}
	c.takeNonce() // the primary sent
	if quiet() {
		t.Fatal("quiet right after another machine's send")
	}
	*now = now.Add(61 * time.Second)
	if !quiet() {
		t.Fatal("not quiet a minute after it")
	}
	c.autoMine = true
	if _, err := s.Send(ctx, Call{To: target, Data: []byte{1}, Label: "own"}); err != nil {
		t.Fatal(err)
	}
	if !quiet() {
		t.Fatal("the backup's own send counted as another machine's")
	}
}

// The sender's errors name the provider and never its URL's key.
func TestSenderErrorsAreRedacted(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{})
	c.estimate = func([]byte) (uint64, error) {
		return 0, errors.New(`Post "https://a.example/v2/SECRET": dial tcp: connection refused`)
	}
	_, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "x"})
	if err == nil || strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "a (https://a.example/…)") || !Transient(err) {
		t.Fatalf("%v", err)
	}
}

// A transaction whose bounds leave it no rise (a route call's fee cap at its ceiling while the base fee climbs) is
// broadcast again as it stands each window — a pool that dropped it takes it back — and still says it is stuck after
// StuckAfter windows.
func TestSenderRebroadcastsWhenItCannotRise(t *testing.T) {
	s, c, now := testSender(t, SendConfig{Poll: 30 * time.Second, Wait: 14 * time.Minute})
	var said []int
	s.OnStuck = func(label string, nonce uint64, n int) { said = append(said, n) }
	c.baseFee = GweiOf(4.9)
	windows := 0
	s.sleep = func(ctx context.Context, d time.Duration) error {
		before := now.Sub(time.Unix(1_800_000_000, 0)) / (3 * time.Minute)
		*now = now.Add(d)
		if after := now.Sub(time.Unix(1_800_000_000, 0)) / (3 * time.Minute); after != before {
			windows++
			c.mu.Lock()
			c.baseFee = GweiOf(20)  // above every version's fee cap: nothing is mined
			delete(c.pool, c.nonce) // the pools drop it between windows
			c.mu.Unlock()
		}
		return nil
	}
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "route"}); !errors.Is(err, ErrStillPending) {
		t.Fatalf("%v", err)
	}
	c.mu.Lock()
	inPool := c.pool[c.nonce] != nil
	c.mu.Unlock()
	if !inPool {
		t.Fatal("a dropped transaction was not broadcast again")
	}
	if len(said) == 0 || said[0] != StuckAfter {
		t.Fatalf("stuck said %v after %d windows", said, windows)
	}
	for _, tx := range c.sent {
		if tx.Nonce() != c.sent[0].Nonce() {
			t.Fatal("a version at another nonce")
		}
	}
}

// A call whose base-fee cap is higher (an emergency call, ER6) takes the nonce of a transaction the base fee has
// passed — both fees 12.5% over its own — and is mined; a call of the same cap waits behind it.
func TestSenderEmergencyDisplacesAStrandedOne(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{Poll: 10 * time.Second, Wait: 30 * time.Second})
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "route"}); !errors.Is(err, ErrStillPending) {
		t.Fatalf("%v", err)
	}
	first := c.sent[0]
	c.baseFee = GweiOf(15) // a spike past the route transaction's fee cap and past maxBaseFee
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{2}, Label: "another route"}); err == nil || !strings.Contains(err.Error(), "still in flight") {
		t.Fatalf("a call of the same cap: %v", err)
	}
	c.autoMine = true
	rc, err := s.Send(context.Background(), Call{To: target, Data: []byte{3}, Label: "decision", MaxBaseFee: GweiOf(200)})
	if err != nil {
		t.Fatal(err)
	}
	last := c.sent[len(c.sent)-1]
	if rc.TxHash != last.Hash() || last.Nonce() != first.Nonce() || last.Data()[0] != 3 {
		t.Fatalf("the emergency call: nonce %d (first %d), data %x", last.Nonce(), first.Nonce(), last.Data())
	}
	if last.GasTipCap().Cmp(mulDiv(first.GasTipCap(), 1125, 1000)) < 0 || last.GasFeeCap().Cmp(new(big.Int).Mul(GweiOf(15), big.NewInt(2))) < 0 {
		t.Fatalf("tip %s→%s, fee cap %s", first.GasTipCap(), last.GasTipCap(), last.GasFeeCap())
	}
}

// A node that finds no gas limit it allows for the call ("gas required exceeds allowance") is ErrGasCap: the caller
// splits the call, as for an estimate over the cap.
func TestSenderAllowanceIsTheGasCap(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{})
	c.estimate = func([]byte) (uint64, error) { return 0, errors.New("gas required exceeds allowance (16777216)") }
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "huge"}); !errors.Is(err, ErrGasCap) {
		t.Fatalf("%v", err)
	}
}

// A corrupt state file is refused on every call, never forgotten after the first refusal.
func TestSenderCorruptStateStaysRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sender.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, c, _ := testSender(t, SendConfig{State: path})
	c.autoMine = true
	for i := 0; i < 2; i++ {
		if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "x"}); err == nil || !strings.Contains(err.Error(), "corrupt") {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if len(c.sent) != 0 {
		t.Fatal("sent over a corrupt record")
	}
	if b, _ := os.ReadFile(path); string(b) != "{not json" {
		t.Fatal("the corrupt file was overwritten")
	}
}

// An emergency call takes the nonce of a route transaction already re-priced up to MaxTip (its tip bound is higher:
// geth's pool wants the tip to rise too).
func TestSenderEmergencyDisplacesARaisedOne(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{Poll: 30 * time.Second, Wait: 10 * time.Minute})
	c.tip = GweiOf(3) // congestion: the provider suggests more than MaxTip
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "route"}); !errors.Is(err, ErrStillPending) {
		t.Fatalf("%v", err)
	}
	stranded := c.sent[len(c.sent)-1]
	if stranded.GasTipCap().Cmp(mulDiv(GweiOf(2), 99, 100)) < 0 {
		t.Fatalf("the route transaction's tip %s did not rise to MaxTip", stranded.GasTipCap())
	}
	c.baseFee = GweiOf(15)
	c.autoMine = true
	rc, err := s.Send(context.Background(), Call{To: target, Data: []byte{3}, Label: "decision", MaxBaseFee: GweiOf(200)})
	if err != nil {
		t.Fatalf("the emergency call is held behind the stranded route transaction: %v", err)
	}
	last := c.sent[len(c.sent)-1]
	if rc.TxHash != last.Hash() || last.Nonce() != stranded.Nonce() || last.GasTipCap().Cmp(mulDiv(stranded.GasTipCap(), 11, 10)) < 0 {
		t.Fatalf("nonce %d/%d, tip %s→%s", stranded.Nonce(), last.Nonce(), stranded.GasTipCap(), last.GasTipCap())
	}
}

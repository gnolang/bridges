package agent

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The tests of audit round 2's fixes (bridge/AUDIT.md; the findings G13–G21, K13–K16, K21, AO1, AO3, AO5, and
// the assumptions audit's F6, F7, F9 where the Go side carries them).

// G13: a dispatch finalized above the slot of the verdict's snapshot is not signed until a held cut that
// counted it; the one at or below it is signed at once.
func TestSignerWaitsForTheVerdictSlot(t *testing.T) {
	k, _ := hl.NewValidatorKey()
	cfg := &Config{}
	cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	s := &Signer{Cfg: cfg, Key: k, Store: t.TempDir(), Every: time.Second, Alarm: &Alarmer{Source: "test"}}
	if err := s.load(); err != nil {
		t.Fatal(err)
	}
	verdictAt(s, 20)
	a := dispatchFor(t, 1, 10, 500, "3Ckv5ySpfK5KXdWwmYUjpR4FnRmfZ8E8tYXkSxcDQkC5iTX6B2aFCxNjxp7Q4nbQ1hRZcbjyW4mYnqN8f3NSn1zT")
	b := dispatchFor(t, 2, 30, 700, "4Ckv5ySpfK5KXdWwmYUjpR4FnRmfZ8E8tYXkSxcDQkC5iTX6B2aFCxNjxp7Q4nbQ1hRZcbjyW4mYnqN8f3NSn1zU")
	for _, p := range []int{0, 1} {
		s.record(a, "BUBBLE", p)
		s.record(b, "BUBBLE", p)
	}
	if err := s.signSol(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.unsigned) != 1 || s.unsigned[0].d.Slot != 30 {
		t.Fatalf("the dispatch above the verdict's slot was signed, or the one below it was not: %d unsigned", len(s.unsigned))
	}
	if _, err := s.readSigned("sol", 1); err != nil {
		t.Fatal("the dispatch at slot 10 is not in the store")
	}
	if _, err := s.readSigned("sol", 2); err == nil {
		t.Fatal("the dispatch at slot 30 was signed on a verdict whose snapshot was at slot 20")
	}
	// the next held cut, whose snapshot is at or past the dispatch's slot, covers it
	verdictAt(s, 30)
	if err := s.signSol(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.unsigned) != 0 {
		t.Fatal("not signed after a cut that counted it")
	}
	if st := s.Status(); st["verdictSlot"] != uint64(30) {
		t.Fatalf("the status does not carry the verdict's slot: %v", st["verdictSlot"])
	}
	if _, ok := s.Status()["providers"]; ok {
		t.Fatal("the providers' URLs are on the status (G16)")
	}
}

// K14: §5.6's ahead-stop clears only on a verdict at a height at or above T; a held identity at the head below
// T keeps it; a broken identity at any height halts; a nonce conflict is never lifted.
func TestSignerAheadStopClearsAtT(t *testing.T) {
	k, _ := hl.NewValidatorKey()
	s := &Signer{Cfg: &Config{}, Key: k, Store: t.TempDir(), Alarm: &Alarmer{Source: "test"}}
	_ = s.load()
	verdictNow(s)
	s.applyAhead(judgeAhead(5000, 1000, 1200, true, DefaultAheadBlocks))
	if s.signing() || s.Status()["aheadHalt"] != true {
		t.Fatal("not halted on the client running ahead")
	}
	s.conclude(nil, 4000, 5000) // the identity holds at the head, below T
	if s.signing() {
		t.Fatal("the ahead-stop cleared on a verdict below T")
	}
	s.conclude(nil, 5000, 5000) // at T
	if !s.signing() || s.Status()["aheadHalt"] != false {
		t.Fatal("the ahead-stop did not clear on a verdict at T")
	}
	s.applyAhead(judgeAhead(5000, 1000, 1200, true, DefaultAheadBlocks))
	s.conclude([]string{"BUBBLE broken"}, 6000, 5000)
	if s.signing() || !strings.Contains(s.Status()["halted"].(string), "broken") {
		t.Fatal("a broken identity did not halt")
	}
	s.conclude(nil, 4000, 5000) // holds again, but below T: the ahead-stop still stands
	if s.signing() {
		t.Fatal("the stop cleared below T after a break")
	}
	s.conclude(nil, 6000, 5000)
	if !s.signing() {
		t.Fatal("not resumed at or above T")
	}
	// a plain halt (no ahead-stop) clears on any held verdict
	s.halt("the supply identity is broken")
	s.conclude(nil, 10, 5000)
	if !s.signing() {
		t.Fatal("a plain halt did not clear on a held verdict")
	}
}

// G17 (the signer): the second provider showing an indexed dispatch with another slot disputes it; a disputed
// dispatch is a round with no verdict and is not signed; agreed facts end the dispute.
func TestSignerDisputedDispatch(t *testing.T) {
	k, _ := hl.NewValidatorKey()
	cfg := &Config{}
	cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	s := &Signer{Cfg: cfg, Key: k, Store: t.TempDir(), Every: time.Second, Alarm: &Alarmer{Source: "test"}}
	if err := s.load(); err != nil {
		t.Fatal(err)
	}
	verdictNow(s)
	a := dispatchFor(t, 1, 10, 500, "3Ckv5ySpfK5KXdWwmYUjpR4FnRmfZ8E8tYXkSxcDQkC5iTX6B2aFCxNjxp7Q4nbQ1hRZcbjyW4mYnqN8f3NSn1zT")
	lie := a
	lie.Slot = 1 << 62
	s.record(a, "BUBBLE", 0)
	s.record(lie, "BUBBLE", 1)
	s.publishCounts() // as the loop does after each scan (G24)
	rec := s.index[a.IDHex()]
	if rec.disputed == "" || !strings.Contains(rec.disputed, "slot 4611686018427387904 against 10") || rec.providers != 3 {
		t.Fatalf("not disputed: %+v", rec)
	}
	if id, why := s.disputed(); id != a.IDHex() || !strings.Contains(why, "disagree") {
		t.Fatalf("disputed(): %q %q", id, why)
	}
	if err := s.signSol(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.unsigned) != 1 {
		t.Fatal("a disputed dispatch was signed")
	}
	if st := s.Status(); st["disputed"] != 1 {
		t.Fatalf("the status does not count the dispute: %v", st["disputed"])
	}
	rec.disputed = "" // the pair agreed (indexScan's re-read)
	if err := s.signSol(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.unsigned) != 0 {
		t.Fatal("not signed once the dispute ended")
	}
	// the same sight twice disputes nothing
	s2 := &Signer{Cfg: cfg, Key: k, Store: t.TempDir(), Every: time.Second, Alarm: &Alarmer{Source: "test"}}
	_ = s2.load()
	s2.record(a, "BUBBLE", 0)
	s2.record(a, "BUBBLE", 1)
	if s2.index[a.IDHex()].disputed != "" {
		t.Fatal("an agreeing second sight disputed the dispatch")
	}
	if why := dispatchDiffers(a, lie); !strings.Contains(why, "slot") {
		t.Fatal(why)
	}
	if why := dispatchDiffers(a, a); why != "" {
		t.Fatal(why)
	}
}

// G17 (the monitor): the second provider's sight is compared with the record; a difference disputes it, and a
// disputed record is UNCONFIRMED to hlwatch's forged-id rule; agreeing sights only add the provider's bit.
func TestIndexDisputedDispatch(t *testing.T) {
	alarm := &Alarmer{Source: "test"}
	index := map[string]*dispatchRec{}
	now := time.Unix(1_800_000_000, 0)
	a := dispatchFor(t, 1, 10, 500, "3Ckv5ySpfK5KXdWwmYUjpR4FnRmfZ8E8tYXkSxcDQkC5iTX6B2aFCxNjxp7Q4nbQ1hRZcbjyW4mYnqN8f3NSn1zT")
	foldDispatch(index, "BUBBLE", 0, a, now, alarm)
	rec := index[a.IDHex()]
	if rec == nil || rec.Providers != 1 || rec.Sig != a.Sig || rec.Slot != 10 {
		t.Fatalf("record: %+v", rec)
	}
	foldDispatch(index, "BUBBLE", 1, a, now, alarm)
	if rec.Providers != 3 || rec.Disputed != "" {
		t.Fatalf("an agreeing sight: %+v", rec)
	}
	b := dispatchFor(t, 2, 11, 700, "4Ckv5ySpfK5KXdWwmYUjpR4FnRmfZ8E8tYXkSxcDQkC5iTX6B2aFCxNjxp7Q4nbQ1hRZcbjyW4mYnqN8f3NSn1zU")
	lie := b
	lie.Slot = 1 << 62
	foldDispatch(index, "BUBBLE", 1, lie, now, alarm) // provider 2 first, lying about the slot
	foldDispatch(index, "BUBBLE", 0, b, now, alarm)
	rb := index[b.IDHex()]
	if rb.Disputed == "" || !strings.Contains(rb.Disputed, "provider 1 shows slot 11 against 4611686018427387904") {
		t.Fatalf("not disputed: %+v", rb)
	}
	if _, ok := alarm.Standing()["solana:providers:"+b.IDHex()[:8]]; !ok {
		t.Fatalf("no solana:providers alarm: %v", alarm.StandingKeys())
	}
	if why := recordDiffers(rb, rb.Slot, rb.To, rb.Amount); why != "" {
		t.Fatal(why)
	}
	v := &SolanaView{Identities: map[string]Identity{"BUBBLE": {Symbol: "BUBBLE", Lock: 1, Supply: 1000}}}
	st := &WatchState{Index: index}
	if got := v.ForgedVerdict(st, b.IDHex(), "BUBBLE", 700); !strings.Contains(got, "UNCONFIRMED") {
		t.Fatalf("a disputed record is not UNCONFIRMED: %s", got)
	}
	if got := v.ForgedVerdict(st, a.IDHex(), "BUBBLE", 500); !strings.HasPrefix(got, "has a dispatch record:") {
		t.Fatalf("an agreed record: %s", got)
	}
}

// fakeSol is a Solana reader in memory: signatures (newest first) and transactions for one account.
type fakeSol struct {
	sigs []*rpc.TransactionSignature // newest first
	txs  map[string]*rpc.GetTransactionResult
}

func (f *fakeSol) GetAccountInfoWithOpts(context.Context, solana.PublicKey, *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error) {
	return nil, errors.New("not in the fake")
}
func (f *fakeSol) GetMultipleAccountsWithOpts(context.Context, []solana.PublicKey, *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error) {
	return nil, errors.New("not in the fake")
}
func (f *fakeSol) GetTransaction(_ context.Context, sig solana.Signature, _ *rpc.GetTransactionOpts) (*rpc.GetTransactionResult, error) {
	r, ok := f.txs[sig.String()]
	if !ok {
		return nil, errors.New("no such transaction")
	}
	return r, nil
}
func (f *fakeSol) GetSignaturesForAddressWithOpts(_ context.Context, _ solana.PublicKey, opts *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error) {
	var out []*rpc.TransactionSignature
	for _, s := range f.sigs {
		if !opts.Until.IsZero() && s.Signature.Equals(opts.Until) {
			break
		}
		out = append(out, s)
	}
	return out, nil
}

// txResult wraps real instructions (built for a payer who is their only signer) as a finalized transaction
// result at a slot.
func txResult(t *testing.T, slot uint64, build func(payer solana.PublicKey) []solana.Instruction) (*rpc.GetTransactionResult, solana.Signature) {
	t.Helper()
	payer := solana.NewWallet()
	tx, err := solana.NewTransaction(build(payer.PublicKey()), solana.Hash{1}, solana.TransactionPayer(payer.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Sign(func(k solana.PublicKey) *solana.PrivateKey {
		if k.Equals(payer.PublicKey()) {
			return &payer.PrivateKey
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var env rpc.TransactionResultEnvelope
	if err := json.Unmarshal([]byte(fmt.Sprintf(`[%q,"base64"]`, base64.StdEncoding.EncodeToString(raw))), &env); err != nil {
		t.Fatal(err)
	}
	return &rpc.GetTransactionResult{Slot: slot, Transaction: &env, Meta: &rpc.TransactionMeta{}}, tx.Signatures[0]
}

// G14: the record's scan folds nothing finalized above the snapshot's slot, leaving its cursor there, so the
// rebuilt record is consistent with the client's counter at the snapshot; the next scan folds the rest.
func TestRecordSyncStopsAtTheSnapshotSlot(t *testing.T) {
	client := sol.LightClient{Program: solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")}
	mailbox := solana.MustPublicKeyFromBase58("9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr")
	f := &fakeSol{txs: map[string]*rpc.GetTransactionResult{}}
	for _, x := range []struct {
		slot   uint64
		height uint64
	}{{10, 100}, {12, 101}, {14, 102}} {
		res, sig := txResult(t, x.slot, func(payer solana.PublicKey) []solana.Instruction {
			return []solana.Instruction{client.InitIx(payer, payer, "dev", 1, x.height, [32]byte{byte(x.height)}, nil, payer)}
		})
		f.txs[sig.String()] = res
		f.sigs = append([]*rpc.TransactionSignature{{Signature: sig, Slot: x.slot}}, f.sigs...) // newest first
	}
	var r ClientRecord
	ctx := context.Background()
	if err := r.Sync(ctx, f, client, mailbox, 12); err != nil {
		t.Fatal(err)
	}
	if r.Count != 2 || r.ScanSlot != 12 {
		t.Fatalf("folded %d entries to slot %d, want 2 to slot 12", r.Count, r.ScanSlot)
	}
	var running [32]byte
	b, _ := hex.DecodeString(r.Hash)
	copy(running[:], b)
	if ok, why := r.Consistent(2, running, 12); !ok {
		t.Fatalf("not consistent at the snapshot: %s", why)
	}
	// the next snapshot, past the third transaction, folds it from the cursor; the complete scan stands at the
	// snapshot's slot, not the last transaction's (G22)
	if err := r.Sync(ctx, f, client, mailbox, 20); err != nil {
		t.Fatal(err)
	}
	if r.Count != 3 || r.ScanSlot != 20 {
		t.Fatalf("folded %d entries to slot %d after the second scan, want 3 to slot 20", r.Count, r.ScanSlot)
	}
	// nothing new: the cursor holds
	if err := r.Sync(ctx, f, client, mailbox, 0); err != nil || r.Count != 3 {
		t.Fatalf("a third scan: %v, %d entries", err, r.Count)
	}
}

// G15: the record's mismatch flags the Solana objects only once it has stood the cut's two minutes; the
// header:gap sighting is dropped while the scan is behind.
func TestMonitorMismatchPersists(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	ctx := context.Background()
	mm := &Mismatch{Height: 99, Client: "aa", Chain: "bb", Slot: 14, Sig: "s"}
	m.recordMismatch(ctx, mm)
	if a := m.Alarm.Standing()["header:mismatch"]; !strings.Contains(a, "flagged once it has stood") {
		t.Fatalf("flagged at first sight: %q", a)
	}
	now := m.now().Add(cutFailAfter)
	m.now = func() time.Time { return now }
	m.recordMismatch(ctx, mm)
	if a := m.Alarm.Standing()["header:mismatch"]; strings.Contains(a, "flagged once") || !strings.Contains(a, "header mismatch (a fork") {
		t.Fatalf("not flagged after the persistence: %q", a)
	}
	if _, ok := m.Alarm.Standing()["flag:solana"]; !ok {
		t.Fatal("no flag attempt (this machine holds no Solana watch key: it alarms)")
	}
	m.recordMismatch(ctx, nil)
	if _, ok := m.Alarm.Standing()["header:mismatch"]; ok {
		t.Fatal("not cleared")
	}
	if _, ok := m.seen["header:mismatch"]; ok {
		t.Fatal("the sighting survived the clear")
	}
}

// heightFake renders differently at a pinned height and at the head.
type heightFake struct {
	*fakeChain
	atHead map[string]string
}

func (h heightFake) RenderAt(ctx context.Context, pkg, path string, height int64) (string, error) {
	if height == 0 {
		if s, ok := h.atHead[pkg+":"+path]; ok {
			return s, nil
		}
	}
	return h.fakeChain.RenderAt(ctx, pkg, path, height)
}

// G15: a number r/hlgov shows no body for at H is read again at the head before it is called foreign — one
// dispatched after H is a lag, checked at a later H; one absent at the head too is foreign.
func TestForeignFindingsRereadsAtTheHead(t *testing.T) {
	f := newFake()
	cfg := &Config{}
	cfg.Gno.Gov = "gno.land/r/test/hlgov"
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute}}
	body := hex.EncodeToString(sol.DecisionBody(7, 3, nil))
	f.renders[cfg.Gno.Gov+":decision/3"] = "null"
	hf := heightFake{fakeChain: f, atHead: map[string]string{cfg.Gno.Gov + ":decision/3": `{"number":3,"kind":7,"body":"` + body + `"}`}}
	findings, checked, err := foreignFindings(context.Background(), nil, hf, cfg, sol.Admin{}, nil, 100, 2, 3, 3, 0, foreignCtx{})
	if err != nil || len(findings) != 0 || checked != 2 {
		t.Fatalf("a decision dispatched after H was called foreign: %v %d %v", findings, checked, err)
	}
	delete(hf.atHead, cfg.Gno.Gov+":decision/3")
	findings, checked, err = foreignFindings(context.Background(), nil, hf, cfg, sol.Admin{}, nil, 100, 2, 3, 2, 0, foreignCtx{})
	if err != nil || len(findings) != 1 || checked != 3 || !strings.Contains(findings[0], "foreign decision") {
		t.Fatalf("a number r/hlgov never dispatched was not called foreign: %v %d %v", findings, checked, err)
	}
}

// G16: every http(s) URL in a text is cut to its scheme and host — the userinfo, the path, the query string and
// the fragment go — in alarms, statuses and logs alike.
func TestRedact(t *testing.T) {
	cases := map[string]string{
		"rpc call getTokenSupply() on https://mainnet.helius-rpc.com/?api-key=SECRET: context deadline exceeded": "rpc call getTokenSupply() on https://mainnet.helius-rpc.com/…: context deadline exceeded",
		`Post "https://user:pw@solana-mainnet.g.alchemy.com/v2/KEY#x": EOF`:                                      `Post "https://solana-mainnet.g.alchemy.com/…": EOF`,
		"own node http://127.0.0.1:26657 answered":                                                               "own node http://127.0.0.1:26657 answered",
		"two: http://a.example/?k=1, https://b.example/p.":                                                       "two: http://a.example/…, https://b.example/….",
		"no url here": "no url here",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	a := &Alarmer{Source: "test"}
	a.Alarm("x", "provider https://h.example/?api-key=K failed")
	if s := a.Standing()["x"]; strings.Contains(s, "api-key") {
		t.Fatalf("the alarm kept the key: %s", s)
	}
	var box statusBox
	box.publish(map[string]string{"err": "https://h.example/?api-key=K: timeout"})
	if s := string(box.bytes()); strings.Contains(s, "api-key") {
		t.Fatalf("the status kept the key: %s", s)
	}
	var sb strings.Builder
	w := RedactWriter{W: &sb}
	if _, err := w.Write([]byte("log https://h.example/v2/KEY\n")); err != nil || sb.String() != "log https://h.example/…\n" {
		t.Fatalf("the log writer: %q %v", sb.String(), err)
	}
}

// G19/AO4: the set-bound alarm text from the current and announced set sizes.
func TestSetBoundText(t *testing.T) {
	if setBoundText(6, 0) != "" || setBoundText(39, 39) != "" {
		t.Fatal("alarmed below the warning size")
	}
	if s := setBoundText(40, 0); !strings.Contains(s, "the current validator set has 40") || !strings.Contains(s, "about 50") {
		t.Fatal(s)
	}
	if s := setBoundText(6, 41); !strings.Contains(s, "an announced validator set has 41") {
		t.Fatal(s)
	}
	f := newFake()
	m := testMonitor(f)
	m.setBound(6, 0)
	if _, ok := m.Alarm.Standing()["client:set-bound"]; ok {
		t.Fatal("alarmed at 6")
	}
	m.setBound(6, 45)
	if _, ok := m.Alarm.Standing()["client:set-bound"]; !ok {
		t.Fatal("no alarm at an announced 45")
	}
	m.setBound(6, 0)
	if _, ok := m.Alarm.Standing()["client:set-bound"]; ok {
		t.Fatal("not cleared")
	}
}

// G18/F7: the executor's extend has its own bookkeeping — the whole excess in one ExtendProgram, the cost
// alarmed before the first send, the backup after its offset, three deterministic refusals parking it.
func TestExtendOnce(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	program := solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")
	buffer := solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	executor := solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	d := &sol.Decision{Number: 5, Kind: sol.KindUpgrade, Params: sol.EncodeParams(&sol.Params{Kind: sol.KindUpgrade, Program: program, Buffer: buffer})}
	r.solData = func(_ context.Context, k solana.PublicKey) ([]byte, error) {
		switch {
		case k.Equals(sol.ProgramData(program)):
			return make([]byte, sol.ProgramDataHeader+100_000), nil
		case k.Equals(buffer):
			return make([]byte, sol.BufferHeader+125_000), nil
		}
		return nil, errors.New("no account")
	}
	var sent []solana.Instruction
	var sendErr error
	r.solSend = func(_ context.Context, ixs []solana.Instruction) (solana.Signature, error) {
		sent = append(sent, ixs...)
		return solana.Signature{1}, sendErr
	}
	// the rent at the chain's rate (AP5, F15: mainnet's 5,080 a byte, not the compiled test validator's 6,960)
	r.solRent = func(_ context.Context, p solana.PublicKey, add uint32) (uint64, error) {
		if !p.Equals(program) {
			return 0, errors.New("another program")
		}
		return uint64(add) * 5_080, nil
	}
	key := "solexec:5"
	r.extendOnce(ctx, d, key, executor)
	if len(sent) != 1 {
		t.Fatalf("%d instructions sent, want one ExtendProgram", len(sent))
	}
	data, _ := sent[0].Data()
	if !sent[0].ProgramID().Equals(sol.LoaderID) || len(data) != 8 || data[0] != 6 {
		t.Fatalf("not the loader's ExtendProgram: %x", data)
	}
	add := uint32(data[4]) | uint32(data[5])<<8 | uint32(data[6])<<16 | uint32(data[7])<<24
	if add != 25_000 {
		t.Fatalf("extended by %d, want the whole excess of 25,000 in one instruction (F7)", add)
	}
	alarm, ok := r.Alarm.Standing()["execute:extend:5"]
	if !ok || !strings.Contains(alarm, "25000 bytes") || !strings.Contains(alarm, "≈0.1270 SOL of rent at the chain's rate") {
		t.Fatalf("the cost alarm: %q", alarm)
	}
	if r.state.Extends["solext:5"] != "25000" {
		t.Fatalf("the plan not kept: %v", r.state.Extends)
	}
	// a deterministic refusal three times parks the extend, like any call
	sendErr = errors.New("simulation failed: custom program error: 0x7")
	for i := 0; i < 3; i++ {
		advance(r, time.Hour)
		r.extendOnce(ctx, d, key, executor)
	}
	a := r.state.Attempts["solext:5"]
	if a == nil || a.Failures != 3 || a.Parked.IsZero() {
		t.Fatalf("not parked after three refusals: %+v", a)
	}
	if _, ok := r.Alarm.Standing()["parked:solext:5"]; !ok {
		t.Fatal("no parked alarm")
	}
	n := len(sent)
	r.extendOnce(ctx, d, key, executor) // inside the hour: nothing sent
	if len(sent) != n {
		t.Fatal("a parked extend was sent inside its hour")
	}
	// Execute's success forgets the extend and its alarm
	r.forgetExtend(key)
	if _, ok := r.Alarm.Standing()["execute:extend:5"]; ok || r.state.Attempts["solext:5"] != nil || r.state.Extends["solext:5"] != "" {
		t.Fatal("the extend's bookkeeping survived the decision")
	}
	// the backup extends only after its offset
	b := testRelayer(f, "backup")
	b.solData, b.solSend = r.solData, func(context.Context, []solana.Instruction) (solana.Signature, error) {
		sent = append(sent, nil)
		return solana.Signature{2}, nil
	}
	n = len(sent)
	b.extendOnce(ctx, d, key, executor)
	if len(sent) != n {
		t.Fatal("the backup extended at once")
	}
	advance(b, BackupOffset+time.Second)
	b.extendOnce(ctx, d, key, executor)
	if len(sent) != n+1 {
		t.Fatal("the backup did not extend after its offset")
	}
}

// AO5: a delivery parked on OverDayCap is retried when the day's bucket has room for its amount, what the pass
// already delivered counted, and not before; a parked attempt is told from its last error.
func TestBucketRoom(t *testing.T) {
	l := sol.MintLimit{MaxPerMessage: 25_000, DayCap: 100_000, Used: 100_000, UsedAt: 1_000}
	day := int64(86_400)
	if ok, why := bucketRoom(l, 1_000, day, 0, 10_000); ok || !strings.Contains(why, "holds 100000 of 100000") {
		t.Fatalf("room in a full bucket: %v %s", ok, why)
	}
	if ok, _ := bucketRoom(l, 1_000+day/10, day, 0, 10_000); !ok { // a tenth drained
		t.Fatal("no room after a tenth of the day")
	}
	if ok, _ := bucketRoom(l, 1_000+day/10, day, 5_000, 10_000); ok {
		t.Fatal("room although this pass used half of it")
	}
	if ok, _ := bucketRoom(l, 1_000+day, day, 0, 30_000); ok {
		t.Fatal("room for an amount above the per-message maximum")
	}
	if overDayCap(nil) || overDayCap(&attempt{LastErr: "pausable ISM: OverDayCap"}) {
		t.Fatal("an unparked attempt counted as parked on the cap")
	}
	if !overDayCap(&attempt{Parked: time.Unix(1, 0), LastErr: "pausable ISM: OverDayCap: send: …"}) {
		t.Fatal("a park on OverDayCap not told")
	}
	if overDayCap(&attempt{Parked: time.Unix(1, 0), LastErr: "pausable ISM: OverMax"}) {
		t.Fatal("a park on OverMax taken for the cap's")
	}
	rt := &Route{Symbol: "BUBBLE"}
	msg := &gnoMsg{Msg: hl.Message{Body: hl.TokenMessage([32]byte{1}, 777, nil)}}
	if tok, amount, ok := tokenAmount(todo{rt: rt, msg: msg}); !ok || tok != 1 || amount != 777 {
		t.Fatalf("tokenAmount: %d %d %v", tok, amount, ok)
	}
	if _, _, ok := tokenAmount(todo{msg: msg}); ok {
		t.Fatal("a decision has a token")
	}
}

// G21: a message found delivered on Solana closes its proof stage when the stage still stands (a process
// confirmed after Send's minute), and leaves nothing to do when it does not.
func TestClosesDeliveredStage(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	r.Cfg.Solana.Client = "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"
	r.Cfg.Solana.Pausable = "9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr"
	r.Cfg.Solana.Admin = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	r.Cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	msg := hl.Message{Version: 3, Nonce: 0, Origin: hl.DomainGnoTest, Sender: route, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, 5, nil)}
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"hook":"","address":"","count":1,"root":"","delivered":0,"live":true,"launchedAt":1,"launchOrigin":%d,"decisions":0,"signers":{"keys":[],"threshold":0,"groupA":[],"groupB":[]},"verifiers":{},"registered":[],"seats":[],"routes":{}}`, hl.DomainGnoTest, hl.DomainSolanaTestnet)
	f.renders[testMailbox+":msg/0"] = msgRender(msg, 50)
	r.solProcessed = func(context.Context, [32]byte) (bool, error) { return true, nil }
	payer := solana.NewWallet()
	r.sol = &sol.Client{Payer: payer.PrivateKey}
	client := r.Cfg.Programs().LightClient()
	stage := client.ProofStage(payer.PublicKey(), gnolight.MessageHash(msg.Bytes()))
	stands := true
	r.solData = func(_ context.Context, k solana.PublicKey) ([]byte, error) {
		if k.Equals(stage) && stands {
			return []byte{1, 2, 3}, nil
		}
		return nil, errors.New("no account")
	}
	var closed []solana.PublicKey
	r.solSend = func(_ context.Context, ixs []solana.Instruction) (solana.Signature, error) {
		for _, ix := range ixs {
			d, _ := ix.Data()
			if ix.ProgramID().Equals(client.Program) && len(d) == 1 && d[0] == 6 {
				closed = append(closed, ix.Accounts()[0].PublicKey)
			}
		}
		return solana.Signature{}, nil
	}
	if err := r.deliverUnderLightClient(ctx); err != nil {
		t.Fatal(err)
	}
	if r.state.Done["0"] != "delivered" || r.state.GnoNext != 1 {
		t.Fatalf("the delivered message was not recorded: %v next %d", r.state.Done, r.state.GnoNext)
	}
	if len(closed) != 1 || !closed[0].Equals(stage) {
		t.Fatalf("the standing stage was not closed: %v", closed)
	}
	// a fresh relayer whose stage is gone sends nothing
	r2 := testRelayer(f, "primary")
	r2.Cfg = r.Cfg
	r2.solProcessed, r2.sol, r2.solData = r.solProcessed, r.sol, r.solData
	stands = false
	n := 0
	r2.solSend = func(context.Context, []solana.Instruction) (solana.Signature, error) {
		n++
		return solana.Signature{}, nil
	}
	if err := r2.deliverUnderLightClient(ctx); err != nil || n != 0 {
		t.Fatalf("a close was sent for no stage: %v %d", err, n)
	}
}

// F9: the relayer's gno client classes a fee or gas refusal as transient — re-priced and re-measured next
// call, never parked — and a realm's "sequence" wording as deterministic (G21).
func TestGasRefusalsAreTransient(t *testing.T) {
	for _, out := range []string{
		"insufficient fees; got: {Gas-Wanted: 45000000, Gas-Fee 45000ugnot}, fee required: 50000 with 1ugnot/900gas as block gas price",
		"insufficient fee error",
		"out of gas error: gas wanted 45000000, used 46000000 (location: VM)",
		"invalid sequence error: got 5, expected 6",
		"signature verification failed; verify correct account, sequence, and chain-id",
	} {
		if gno.Deterministic(&gno.CallError{Pkg: "r", Fn: "f", Err: errors.New("exit status 1"), Output: out}) {
			t.Errorf("classed deterministic: %s", out)
		}
	}
	if !gno.Deterministic(&gno.CallError{Pkg: "r", Fn: "f", Err: errors.New("exit status 1"), Output: "panic: hlgov: sync sequence: the counter would not rise"}) {
		t.Error("a realm's refusal wording 'sequence' classed transient")
	}
}

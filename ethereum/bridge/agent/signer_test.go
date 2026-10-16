package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// signerRig is one signer over the fakes of both chains: GNOT locked 1,000 on gno.land and minted 1,000 on Ethereum,
// the client trusted at height 50, gno.land's head at 60 one second after the clock.
type signerRig struct {
	t      *testing.T
	chain  *m3bChain
	pa, pb *m3bProvider
	gno    *m3bGnoNode
	s      *Signer
	now    time.Time
	alice  [32]byte
	clk    sync.Mutex // the fake clock is read by the status's goroutine too
}

func (r *signerRig) clock() time.Time {
	r.clk.Lock()
	defer r.clk.Unlock()
	return r.now
}

func newSignerRig(t *testing.T) *signerRig {
	t.Helper()
	r := &signerRig{t: t, chain: newM3bChain(100), now: time.Unix(1_800_000_000, 0)}
	r.chain.setSupply(m3bGNOT, 1, 1000)
	r.chain.clientAt(50, [32]byte{1})
	pair, pa, pb := m3bPair(r.chain)
	r.pa, r.pb = pa, pb
	r.gno = newM3bGno(60, r.now.Add(time.Second))
	r.gno.set(m3bMailPath, "json", m3bMailboxJSON(nil, 2, nil))
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 0, 0, 0))
	r.gno.set(m3bNS+"hlbubble", "json", m3bRouteJSON(0, 0, 0, 0))
	a, err := hl.GnoAddressToBytes32("g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5")
	if err != nil {
		t.Fatal(err)
	}
	r.alice = a
	k, err := hl.NewValidatorKey()
	if err != nil {
		t.Fatal(err)
	}
	r.s = &Signer{Cfg: m3bConfig(), Key: k, Store: t.TempDir(), Every: time.Second, Alarm: &Alarmer{Source: "test"}, pair: pair, gno: r.gno, now: r.clock}
	if err := r.s.load(); err != nil {
		t.Fatal(err)
	}
	r.s.state.From = 1
	return r
}

// step moves the clock and gno.land's head on.
func (r *signerRig) step(d time.Duration) {
	r.clk.Lock()
	r.now = r.now.Add(d)
	now := r.now
	r.clk.Unlock()
	r.gno.advance(r.gno.height+1, now.Add(time.Second))
}

func (r *signerRig) round() {
	r.t.Helper()
	r.s.round(context.Background())
}

func (r *signerRig) status() map[string]any { return r.s.Status() }

func (r *signerRig) alarmed(key string) bool {
	_, ok := r.s.Alarm.Standing()[key]
	return ok
}

// A signer signs a token's own burn and nothing else: a stranger's dispatch naming the route realm as recipient (no
// token transfer beside it) is ignored, never counted in flight and never signed (ER5); the signature is over this
// bridge's checkpoint — our MerkleTreeHook, root 0, index the nonce — and the public endpoint serves it.
func TestSignerSignsTheTokensOwnBurnsOnly(t *testing.T) {
	r := newSignerRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.stranger(gnot, r.alice, 9_000_000, 70)      // nonce 0: a stranger's dispatch to our realm
	id := r.chain.burn(m3bGNOT, gnot, r.alice, 200, 80) // nonce 1: alice burns 200 GNOT
	// nonce 2: a Dispatch whose sender topic is the token but with no transfer of the token beside it (what a lying
	// provider or a hook could show): not the token's own burn, ignored with why
	forged := hl.Message{Version: 3, Nonce: 2, Origin: m3bEthDom, Sender: ethx.AddressTo32(m3bGNOT), Destination: m3bGnoDom, Recipient: gnot, Body: hl.TokenMessage(r.alice, 7_000_000, nil)}
	r.chain.nonce = 3
	r.chain.dispatch(forged, 85, common.Hash{0xf0}, 1)
	r.round()
	st := r.status()
	if st["signing"] != true {
		t.Fatalf("not signing: %v", st)
	}
	if st["ignored"].(int) != 1 || !strings.Contains(strings.Join(r.s.state.Index.Ignored, ";"), "no SentTransferRemote") {
		t.Fatalf("the dispatch without the token's transfer was not ignored with why: %v %v", st, r.s.state.Index.Ignored)
	}
	if _, err := r.s.readSigned(2); err == nil {
		t.Fatal("a dispatch without the token's transfer was signed")
	}
	ident := st["identity"].(map[string]any)["GNOT"].(map[string]any)
	if ident["verdict"] != "holds" || ident["uncredited"].(int64) != 200 || ident["supply"].(int64) != 800 {
		t.Fatalf("the identity: %v", ident)
	}
	if _, err := r.s.readSigned(0); err == nil {
		t.Fatal("the stranger's dispatch was signed")
	}
	sg, err := r.s.readSigned(1)
	if err != nil {
		t.Fatalf("the burn was not signed: %v", err)
	}
	if sg.ID != hex.EncodeToString(id[:]) || sg.Origin != m3bEthDom || sg.Block != 80 {
		t.Fatalf("the signature's facts: %+v", sg)
	}
	hook := ethx.AddressTo32(m3bHook)
	if sg.Hook != hex.EncodeToString(hook[:]) || sg.Root != strings.Repeat("0", 64) {
		t.Fatalf("the checkpoint is not this bridge's convention: %+v", sg)
	}
	d := hl.CheckpointDigest(m3bEthDom, hook, [32]byte{}, 1, id)
	sig, _ := hex.DecodeString(sg.Signature)
	if !ed25519.Verify(r.s.Key.EdPublic(), d[:], sig) {
		t.Fatal("the signature does not verify over the checkpoint digest")
	}
	// the public endpoint serves it, and nothing else
	srv := httptest.NewServer(r.s.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/v1/eth/1")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET /v1/eth/1: %v %v", resp, err)
	}
	var served SignedBurn
	_ = json.NewDecoder(resp.Body).Decode(&served)
	resp.Body.Close()
	if served.Signature != sg.Signature {
		t.Fatal("the endpoint serves another signature")
	}
	if resp, _ := srv.Client().Get(srv.URL + "/v1/status"); resp.StatusCode != 404 {
		t.Fatal("the public endpoint serves the status")
	}
	// credited on gno.land: the claim is owed, the identity still holds, the burn leaves the index
	r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(id[:]), "61")
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 200, 0, 0))
	r.step(time.Second)
	r.round()
	ident = r.status()["identity"].(map[string]any)["GNOT"].(map[string]any)
	if ident["verdict"] != "holds" || ident["uncredited"].(int64) != 0 || ident["owed"].(int64) != 200 {
		t.Fatalf("after the credit: %v", ident)
	}
	if len(r.s.state.Index.Burns) != 0 {
		t.Fatal("a credited, signed burn stays in the index")
	}
}

// Two providers that disagree are no verdict: one answering a log with another amount, one answering no logs at all
// (with no error) — nothing is signed and the alarm names the providers.
func TestSignerProvidersDisagree(t *testing.T) {
	for _, tc := range []struct {
		name string
		lie  func(p *m3bProvider)
	}{
		{"another amount", func(p *m3bProvider) {
			p.mutate = func(l *types.Log) {
				if l.Topics[0] == ethx.HypERC20.Event("SentTransferRemote") {
					l.Data = common.BigToHash(common.Big3).Bytes()
				}
			}
		}},
		{"no logs and no error", func(p *m3bProvider) { p.dropLogs = true }},
		{"another supply", func(p *m3bProvider) {
			p.supplyX = func(n uint64, v *big.Int) *big.Int { return new(big.Int).Add(v, common.Big1) }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newSignerRig(t)
			r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 200, 80)
			tc.lie(r.pb)
			r.round()
			st := r.status()
			if st["verified"] == true || st["signing"] == true {
				t.Fatalf("a verdict on two providers that disagree: %v", st)
			}
			if _, err := r.s.readSigned(0); err == nil {
				t.Fatal("signed on a disagreement")
			}
			if !strings.Contains(st["cut"].(string), "disagree") && !r.alarmed("eth:providers") {
				t.Fatalf("the disagreement is not named: %v", st)
			}
		})
	}
}

// A reorganised log is refused: a log a provider marks removed, and a log whose block hash is not the canonical hash
// both providers give for its number, are no reading of the chain — no verdict, nothing signed.
func TestSignerRefusesAReorgedLog(t *testing.T) {
	r := newSignerRig(t)
	r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 200, 80)
	r.pa.removed, r.pb.removed = true, true
	r.round()
	if st := r.status(); st["verified"] == true {
		t.Fatalf("a verdict on a removed log: %v", st)
	}
	if _, err := r.s.readSigned(0); err == nil {
		t.Fatal("a removed log was signed")
	}
	// a log carrying a block hash of another fork, both providers alike
	r2 := newSignerRig(t)
	r2.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r2.alice, 200, 80)
	fork := func(l *types.Log) { l.BlockHash = m3bHash(7, l.BlockNumber) }
	r2.pa.mutate, r2.pb.mutate = fork, fork
	r2.round()
	if st := r2.status(); st["verified"] == true || !strings.Contains(st["cut"].(string), "canonical") {
		t.Fatalf("a log of another fork was taken: %v", st)
	}
	if _, err := r2.s.readSigned(0); err == nil {
		t.Fatal("a log of another fork was signed")
	}
}

// The refusal rule (§5.6): a supply above the lock stops the signer with the reason on its status; a forged mint
// burned back toward gno.land keeps it stopped by its own amount (the burn in flight counts as supply, AN3); GovDAO's
// void as forged — the paid part booked as lost — closes the identity, and the stop clears itself.
func TestSignerRefusalRuleStopsAndClears(t *testing.T) {
	r := newSignerRig(t)
	r.round()
	if r.status()["signing"] != true {
		t.Fatalf("not signing at the start: %v", r.status())
	}
	// a forged mint of 500 at block 90: supply 1,500 against a lock of 1,000
	r.chain.mine(110, 110)
	r.chain.setSupply(m3bGNOT, 90, 1500)
	r.step(time.Second)
	r.round()
	st := r.status()
	if st["signing"] == true || !strings.Contains(st["halted"].(string), "supply identity is broken") {
		t.Fatalf("a supply above the lock did not stop the signer: %v", st)
	}
	if !r.alarmed("signer:halt") {
		t.Fatal("no halt alarm")
	}
	// the forger burns the 500 back toward gno.land: the supply is 1,000 again, the burn in flight keeps the stop
	r.chain.mine(130, 130)
	id := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 500, 120)
	r.step(time.Second)
	r.round()
	st = r.status()
	ident := st["identity"].(map[string]any)["GNOT"].(map[string]any)
	if st["signing"] == true || ident["uncredited"].(int64) != 500 || ident["stops"] != true {
		t.Fatalf("the forged mint burned back did not keep the stop: %v", st)
	}
	if _, err := r.s.readSigned(0); err == nil {
		t.Fatal("the forged burn was signed while stopped")
	}
	// the burn credited (by two other signers, say) and GovDAO voids it as forged before a unit is paid: the
	// remainder leaves owed — the identity holds again, and the stop clears itself
	r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(id[:]), "70")
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 0, 0, 0))
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] != true || st["halted"] != "" {
		t.Fatalf("the stop did not clear once the identity held: %v", st)
	}
	if r.alarmed("signer:halt") {
		t.Fatal("the halt alarm stands after the clear")
	}
}

// The one-round wait: a burn above the last verdict's finalized block is not signed by that verdict; the next verdict,
// whose snapshot counted it in flight, signs it.
func TestSignerWaitsOneRound(t *testing.T) {
	r := newSignerRig(t)
	r.round()
	r.chain.mine(110, 110)
	id := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 100, 104)
	// the index holds it, scanned past the verdict's block (a scan after the gno read reaches a later finalized block)
	if _, _, err := r.s.state.Index.catchUp(context.Background(), r.s.pair, r.s.Cfg.Eth.Mailbox, r.s.Cfg.Eth.Admin, m3bEthDom, m3bGnoDom, burnRoutes(r.s.Cfg, nil), 1, 110, r.now); err != nil {
		t.Fatal(err)
	}
	if r.s.verdictAt != 100 {
		t.Fatalf("the verdict's block is %d", r.s.verdictAt)
	}
	if err := r.s.signBurns(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.readSigned(0); err == nil {
		t.Fatal("a burn above the verdict's finalized block was signed")
	}
	r.step(time.Second)
	r.round()
	sg, err := r.s.readSigned(0)
	if err != nil || sg.ID != hex.EncodeToString(id[:]) {
		t.Fatalf("the next verdict did not sign it: %v %v", sg, err)
	}
}

// A provider without the finalized block's state is no verdict, never a zero: the alarm names the cure (a full-history
// provider); an hour without a verdict halts the signer; a verdict lifts the halt.
func TestSignerNoStateAndTheHourWithoutAVerdict(t *testing.T) {
	r := newSignerRig(t)
	r.round()
	if r.status()["signing"] != true {
		t.Fatal("not signing at the start")
	}
	r.chain.mine(600, 400) // the finalized block 200 behind the head: past a pruned node's ~128 blocks of state
	r.pb.noState = true
	r.step(time.Second)
	r.round()
	if !r.alarmed("eth:no-state") || !strings.Contains(r.s.Alarm.Standing()["eth:no-state"], "full-history") {
		t.Fatalf("no alarm naming the full-history cure: %v", r.s.Alarm.Standing())
	}
	if r.status()["halted"] != "" {
		t.Fatal("one round without a verdict halted the signer")
	}
	r.step(61 * time.Minute)
	r.round()
	if st := r.status(); st["signing"] == true || !strings.Contains(st["halted"].(string), "no verdict for an hour") {
		t.Fatalf("an hour without a verdict did not halt: %v", st)
	}
	r.pb.noState = false
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] != true {
		t.Fatalf("a verdict did not lift the halt: %v", st)
	}
	if r.alarmed("eth:no-state") {
		t.Fatal("the no-state alarm stands after a good read")
	}
}

// A finality stall — the finalized block standing still for an hour — alarms; the signers sign nothing newer.
func TestSignerFinalityStall(t *testing.T) {
	r := newSignerRig(t)
	r.round()
	r.step(30 * time.Minute)
	r.round()
	if r.alarmed("eth:finality-stall") {
		t.Fatal("alarmed inside the hour")
	}
	r.step(31 * time.Minute)
	r.round()
	if !r.alarmed("eth:finality-stall") {
		t.Fatal("no alarm for a finality stall of an hour")
	}
	r.chain.mine(120, 110)
	r.step(time.Second)
	r.round()
	if r.alarmed("eth:finality-stall") {
		t.Fatal("the stall alarm stands after finality resumed")
	}
}

// A signer signs once per message and refuses two messages for one nonce: the second stops it (it keeps serving and
// does not exit), the status says why, and the identity holding again does not lift it.
func TestSignerRefusesTwoMessagesForOneNonce(t *testing.T) {
	r := newSignerRig(t)
	r.round()
	s := r.s
	first := SignedBurn{Nonce: 3, ID: "aa"}
	if fresh, err := s.put(first); err != nil || !fresh {
		t.Fatalf("first put: %v %v", fresh, err)
	}
	if fresh, err := s.put(first); err != nil || fresh {
		t.Fatalf("the same message again: fresh %v err %v", fresh, err)
	}
	if !s.signing() {
		t.Fatal("halted after a harmless repeat")
	}
	if _, err := s.put(SignedBurn{Nonce: 3, ID: "bb"}); err == nil || !strings.Contains(err.Error(), "REFUSING") {
		t.Fatalf("a second message for nonce 3 was taken: %v", err)
	}
	st := s.Status()
	if s.signing() || st["nonceConflict"] != true || !strings.Contains(st["halted"].(string), "nonce conflict") {
		t.Fatalf("the status does not say why: %v", st)
	}
	r.step(time.Second)
	r.round()
	if s.signing() {
		t.Fatal("a nonce conflict was lifted by the identity holding")
	}
	if b, err := s.readSigned(3); err != nil || b.ID != "aa" {
		t.Fatalf("the first signature is gone: %v %v", b, err)
	}
}

// §5.6's stop when the client runs ahead (judgeAhead): T more than an hour of blocks past both the own node's head and a
// second source's stops the signer; T past it on the own node alone — the second source near T, or past it — or with
// no second source, is a reading the node cannot give yet. (The Solana judgeAhead read a second source past T as a
// stop: T − second underflowed; found here, M3b-NOTES.)
func TestSignerStopsWhenTheClientRunsAhead(t *testing.T) {
	const allow = DefaultAheadBlocks
	cases := []struct {
		name           string
		T, own, second uint64
		secondOK       bool
		want           aheadVerdict
	}{
		{"inside the allowance", 2000, 1500, 0, false, aheadInside},
		{"exactly the allowance", 2000 + allow, 2000, 2000, true, aheadInside},
		{"past it on the own node, the second near T", 5000, 1000, 4500, true, aheadOwnLags},
		{"past it on the own node, the second past T", 5000, 1000, 5200, true, aheadOwnLags},
		{"past it on both", 5000, 1000, 1200, true, aheadStop},
		{"past it on both by one block", 5000, 1000, 5000 - allow - 1, true, aheadStop},
		{"no second source", 5000, 1000, 0, false, aheadNoSecond},
	}
	for _, c := range cases {
		if v, why := judgeAhead(c.T, c.own, c.second, c.secondOK, allow); v != c.want {
			t.Errorf("%s: %d (%s), want %d", c.name, v, why, c.want)
		}
	}
	// the rig's client trusted at 5,000 against an own node at 60 and a second source at 100: the stop
	r := newSignerRig(t)
	r.round()
	r.chain.clientAt(5000, [32]byte{2})
	second := newM3bGno(100, r.now.Add(time.Hour))
	r.s.sources = []gnoside.Reader{second}
	r.step(time.Second)
	r.round()
	st := r.status()
	if st["signing"] == true || !strings.Contains(st["halted"].(string), "ahead of its chain") || st["aheadHalt"] != true {
		t.Fatalf("not stopped with the client ahead of its chain: %v", st)
	}
	// a verdict at a height below T does not lift it; one at or above T does
	r.chain.clientAt(5000, [32]byte{2})
	r.gno.advance(4990, r.now.Add(2*time.Second))
	r.step(time.Second)
	r.round()
	if r.status()["signing"] == true {
		t.Fatal("a verdict below T lifted the ahead stop")
	}
	r.gno.advance(5000, r.now.Add(2*time.Second))
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] != true {
		t.Fatalf("a verdict at T did not lift the ahead stop: %v", st)
	}
}

// §5.6's provider rule across the signer machines: two providers each, never the same twice, no pair shared by two
// signers, no provider in more than two.
func TestProviderRule(t *testing.T) {
	cfg := func(a, b string) *Config {
		c := &Config{}
		c.Eth.Providers = []ethx.Provider{{Name: "1", URL: a}, {Name: "2", URL: b}}
		return c
	}
	good := map[string]*Config{"s1": cfg("https://p1/K", "https://p2/K"), "s2": cfg("https://p3/K", "https://p4/K"), "s3": cfg("https://p1/K", "https://p3/K")}
	if err := ProviderRule(good); err != nil {
		t.Fatalf("four providers across three signers refused: %v", err)
	}
	for name, bad := range map[string]map[string]*Config{
		"a shared pair":       {"s1": cfg("https://p1/K", "https://p2/K"), "s2": cfg("https://p2/K", "https://p1/K")},
		"a provider in three": {"s1": cfg("https://p1/K", "https://p2/K"), "s2": cfg("https://p1/K", "https://p3/K"), "s3": cfg("https://p1/K", "https://p4/K")},
		"the same twice":      {"s1": cfg("https://p1/K", "https://p1/K")},
		"one provider":        {"s1": {}},
	} {
		if err := ProviderRule(bad); err == nil {
			t.Errorf("%s: taken", name)
		} else if strings.Contains(err.Error(), "/K") {
			t.Errorf("%s: the refusal prints a provider's key: %v", name, err)
		}
	}
}

// The status is read while the loop runs (the loopback listener's goroutine): every field comes from what the loop
// published under the lock, never from the index it writes (-race holds it).
func TestSignerStatusDuringRounds(t *testing.T) {
	r := newSignerRig(t)
	r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 200, 80)
	srv := httptest.NewServer(r.s.StatusHandler())
	defer srv.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			_ = r.s.Status()
			if resp, err := srv.Client().Get(srv.URL + "/v1/status"); err == nil {
				resp.Body.Close()
			}
		}
	}()
	for i := 0; i < 5; i++ {
		r.round()
		r.step(time.Second)
	}
	<-done
	if st := r.status(); st["cursor"].(uint64) != 100 || st["signed"].(int) != 1 {
		t.Fatalf("the published counts: %v", st)
	}
}

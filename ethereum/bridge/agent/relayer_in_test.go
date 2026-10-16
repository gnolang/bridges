package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// m3bHost is the relayer as the inbound job sees it: notes, the primary's act, calls to the fake gno node.
type m3bHost struct {
	g      *m3bGnoNode
	notes  map[string]string
	act    bool
	backup bool
}

func (h *m3bHost) Note(k, w string)          { h.notes[k] = w }
func (h *m3bHost) Act(string) bool           { return h.act }
func (h *m3bHost) Forget(string)             {}
func (h *m3bHost) GnoClient() gnoside.Caller { return h.g }
func (h *m3bHost) TryCall(ctx context.Context, key, pkg, fn string, args ...string) (*gno.Result, error) {
	return h.g.Call(ctx, pkg, fn, 0, args...)
}
func (h *m3bHost) TryCallGas(ctx context.Context, key, pkg, fn string, gas int64, args ...string) (*gno.Result, error) {
	return h.g.Call(ctx, pkg, fn, gas, args...)
}
func (h *m3bHost) CallGas() int64 { return 45_000_000 }
func (h *m3bHost) Backup() bool   { return h.backup }

// inboundRig is the inbound job over the fakes: three signers serving their stores over HTTP, hlmailbox's rule 2 of 3.
type inboundRig struct {
	t       *testing.T
	chain   *m3bChain
	gno     *m3bGnoNode
	host    *m3bHost
	job     *inboundJob
	keys    []*hl.ValidatorKey
	signers []*Signer
	alice   [32]byte
}

func newInboundRig(t *testing.T) *inboundRig {
	t.Helper()
	r := &inboundRig{t: t, chain: newM3bChain(100)}
	r.gno = newM3bGno(60, time.Now())
	r.host = &m3bHost{g: r.gno, notes: map[string]string{}, act: true}
	cfg := m3bConfig()
	cfg.AllowHTTP = true
	var keys []string
	for i := 0; i < 3; i++ {
		k, err := hl.NewValidatorKey()
		if err != nil {
			t.Fatal(err)
		}
		s := &Signer{Cfg: cfg, Key: k, Store: t.TempDir(), Alarm: &Alarmer{Source: "test"}}
		if err := s.load(); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(s.Handler())
		t.Cleanup(srv.Close)
		r.keys = append(r.keys, k)
		r.signers = append(r.signers, s)
		keys = append(keys, hex.EncodeToString(k.EdPublic()))
		cfg.Signers = append(cfg.Signers, SignerRef{URL: srv.URL, Key: hex.EncodeToString(k.EdPublic())})
	}
	r.gno.set(m3bMailPath, "json", m3bMailboxJSON(keys, 2, nil))
	r.gno.set(m3bNS+"hlgnot", "json", inboundRouteJSON(1000))
	r.gno.set(m3bNS+"hlbubble", "json", inboundRouteJSON(0))
	a, _ := hl.GnoAddressToBytes32("g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5")
	r.alice = a
	prov := &m3bProvider{c: r.chain}
	r.job = &inboundJob{cfg: cfg, eth: ethx.NewClient(ethx.Provider{Name: "a", URL: "https://a.example/KEY"}, prov), alarm: &Alarmer{Source: "test"}, FromBlock: 1}
	return r
}

// inboundRouteJSON is a route whose fee (100 base units) lies below the rig's burns of 200 and 300: a burn below its
// route's fee is left unprocessed (E5-GS5).
func inboundRouteJSON(lock int64) string {
	return fmt.Sprintf(`{"live":true,"lock":%d,"owed":0,"voided":0,"lost":0,"fee":100}`, lock)
}

// sign has signer i sign burn id at nonce n as it would (hook: our MerkleTreeHook), or over a wrong hook when bad.
func (r *inboundRig) sign(i int, n uint32, raw []byte, bad bool) {
	r.t.Helper()
	hook := ethx.AddressTo32(m3bHook)
	if bad {
		hook = [32]byte{1}
	}
	sig := hl.SignEd25519(r.keys[i].Ed, hl.Checkpoint{Hook: hook, Index: n}, raw)
	id := hl.ID(raw)
	if _, err := r.signers[i].put(SignedBurn{Origin: m3bEthDom, Nonce: n, ID: hex.EncodeToString(id[:]), Message: hex.EncodeToString(raw), Signature: hex.EncodeToString(sig[:]), Signer: hex.EncodeToString(r.keys[i].EdPublic())}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *inboundRig) tick() error { return r.job.tick(context.Background(), r.host) }

// A burn is delivered through its route's Process with exactly the signatures the rule needs — the lowest-indexed
// valid ones (signers 0 and 1 of three) — under this bridge's checkpoint; a signature over another checkpoint is
// skipped (signer 1's), and with one valid signature the burn waits; a stranger's dispatch is never queued.
func TestInboundDeliversWithExactlyTheRule(t *testing.T) {
	r := newInboundRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.stranger(gnot, r.alice, 9_000_000, 70)
	id := r.chain.burn(m3bGNOT, gnot, r.alice, 200, 80)
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	if len(r.job.st.Pending) != 1 || r.job.st.Pending[0].Nonce != 1 {
		t.Fatalf("the queue: %+v", r.job.st.Pending)
	}
	raw, _ := hex.DecodeString(r.job.st.Pending[0].Message)
	key := "process:" + hex.EncodeToString(id[:])
	if !strings.Contains(r.host.notes[key], "waiting for signatures") || len(r.gno.callsOf("Process")) != 0 {
		t.Fatalf("delivered with no signatures: %v %v", r.host.notes, r.gno.calls)
	}
	r.sign(0, 1, raw, false)
	r.sign(1, 1, raw, true) // over another hook: not its signature of this checkpoint
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	if len(r.gno.callsOf("Process")) != 0 {
		t.Fatalf("delivered with one valid signature: %v", r.gno.calls)
	}
	r.sign(2, 1, raw, false)
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	calls := r.gno.callsOf("Process")
	if len(calls) != 1 || !strings.HasPrefix(calls[0], m3bNS+"hlgnot.Process(") {
		t.Fatalf("not delivered through the route: %v", r.gno.calls)
	}
	args := strings.Split(strings.TrimSuffix(strings.SplitN(calls[0], "(", 2)[1], ")"), ",")
	meta, _ := hex.DecodeString(args[0])
	if len(meta) != 68+2*65 || meta[68] != 0 || meta[68+65] != 2 {
		t.Fatalf("the metadata does not carry exactly signers 0 and 2: %x", meta)
	}
	if args[1] != hex.EncodeToString(raw) {
		t.Fatal("the message delivered is not the burn's")
	}
	hook := ethx.AddressTo32(m3bHook)
	if hex.EncodeToString(meta[:32]) != hex.EncodeToString(hook[:]) || meta[67] != 1 {
		t.Fatalf("the checkpoint is not our hook and the nonce: %x", meta[:68])
	}
	// delivered: hlmailbox marks it, the job drops it
	r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(id[:]), "61")
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	if len(r.job.st.Pending) != 0 || len(r.job.st.Sigs) != 0 {
		t.Fatalf("a delivered burn stays queued: %+v", r.job.st)
	}
}

// A burn addressed to a route whose successor is still importing waits, named, never attempted.
func TestInboundWaitsForAMigration(t *testing.T) {
	r := newInboundRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.burn(m3bGNOT, gnot, r.alice, 200, 80)
	r.gno.set(m3bNS+"hlgnot", "json", `{"live":true,"lock":0,"migratedTo":"`+m3bNS+`hlgnot2"}`)
	r.gno.set(m3bNS+"hlgnot2", "json", `{"live":true,"lock":1000,"importing":true,"imported":1}`)
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	for k, v := range r.host.notes {
		if strings.HasPrefix(k, "process:") && !strings.Contains(v, "waits:") {
			t.Fatalf("not waiting: %s", v)
		}
	}
	if len(r.gno.callsOf("Process")) != 0 {
		t.Fatal("delivered to a route mid-import")
	}
}

// Claim(20) runs while a claim waits and the meter holds the fee, and backs off while a call pays nothing; payouts
// paused, no open claim or a dry meter is idle, named.
func TestInboundClaims(t *testing.T) {
	r := newInboundRig(t)
	r.gno.set(m3bNS+"hlgnot", "json", `{"live":true,"lock":1000,"owed":200,"openClaims":1,"meter":50000000,"fee":5000000}`)
	r.gno.set(m3bNS+"hlbubble", "json", `{"live":true,"lock":0,"openClaims":1,"meter":10,"fee":100000000}`)
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	if c := r.gno.callsOf("Claim"); len(c) != 1 || c[0] != m3bNS+"hlgnot.Claim(20)" {
		t.Fatalf("the claim calls: %v", r.gno.calls)
	}
	if !strings.Contains(r.host.notes["claim:BUBBLE"], "the meter holds less than the fee") {
		t.Fatalf("a dry meter is not named: %v", r.host.notes)
	}
	// it paid nothing (no Claimed event): the next call waits a minute
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	if c := r.gno.callsOf("Claim"); len(c) != 1 {
		t.Fatalf("called again inside the backoff: %v", c)
	}
	if cs := r.job.st.Claims["GNOT"]; cs.Backoff != time.Minute || cs.Calls != 1 {
		t.Fatalf("the backoff: %+v", cs)
	}
	r.gno.set(m3bNS+"hlgnot", "json", `{"live":true,"lock":1000,"owed":200,"openClaims":1,"meter":50000000,"fee":5000000,"paused":"payouts"}`)
	r.job.st.Claims["GNOT"].Next = time.Time{}
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.host.notes["claim:GNOT"], "payouts paused") || len(r.gno.callsOf("Claim")) != 1 {
		t.Fatalf("claimed under a payouts flag: %v %v", r.host.notes, r.gno.calls)
	}
}

// A gno call's failure is the relayer's to count (the job keeps the burn queued and tries again).
func TestInboundKeepsAFailedDelivery(t *testing.T) {
	r := newInboundRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.burn(m3bGNOT, gnot, r.alice, 200, 80)
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	raw, _ := hex.DecodeString(r.job.st.Pending[0].Message)
	r.sign(0, 0, raw, false)
	r.sign(1, 0, raw, false)
	r.gno.callErr = errors.New("panic: hlverifier: refused")
	_ = r.tick()
	if len(r.job.st.Pending) != 1 {
		t.Fatal("a failed delivery left the queue")
	}
	r.gno.callErr = nil
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	if len(r.gno.callsOf("Process")) != 2 {
		t.Fatalf("not tried again: %v", r.gno.calls)
	}
}

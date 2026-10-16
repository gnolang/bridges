package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// Lane s7's tests of its new mechanisms (they do not compile on the parent): the listing past a cursor (E6-SF1), the
// rollback step that drops dust (E6-SF5), the deploy step that names the burns of nothing signed (E6-SF3).

// ----------------------------------------------------------------------------------- the listing itself --

// s7Lister is a provider's listing of one address in memory, with RPC's paging (newest first, `before`, `until`, a
// thousand a page), a first available block below which a signature is −32020 "not found", and its answer of it.
type s7Lister struct {
	sigs  []*rpc.TransactionSignature // oldest first
	first uint64
	calls int
}

func (l *s7Lister) GetSignaturesForAddressWithOpts(_ context.Context, _ solana.PublicKey, o *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error) {
	l.calls++
	for _, given := range []solana.Signature{o.Until, o.Before} {
		if given.IsZero() {
			continue
		}
		for _, s := range l.sigs {
			if s.Signature == given && s.Slot < l.first {
				return nil, &jsonrpc.RPCError{Code: -32020, Message: "Transaction " + given.String() + " not found"}
			}
		}
	}
	var out []*rpc.TransactionSignature
	skipping := !o.Before.IsZero()
	for i := len(l.sigs) - 1; i >= 0 && l.sigs[i].Slot >= l.first; i-- {
		s := l.sigs[i]
		if skipping {
			skipping = s.Signature != o.Before
			continue
		}
		if !o.Until.IsZero() && s.Signature == o.Until {
			break
		}
		out = append(out, s)
		if o.Limit != nil && len(out) >= *o.Limit {
			break
		}
	}
	return out, nil
}

func (l *s7Lister) GetFirstAvailableBlock(context.Context) (uint64, error) { return l.first, nil }

func (l *s7Lister) GetAccountInfoWithOpts(context.Context, solana.PublicKey, *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error) {
	return nil, rpc.ErrNotFound
}
func (l *s7Lister) GetMultipleAccountsWithOpts(context.Context, []solana.PublicKey, *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error) {
	return &rpc.GetMultipleAccountsResult{}, nil
}
func (l *s7Lister) GetTransaction(context.Context, solana.Signature, *rpc.GetTransactionOpts) (*rpc.GetTransactionResult, error) {
	return nil, rpc.ErrNotFound
}

// Every case of a listing past a cursor, over 2,500 signatures three to a slot (slots 1–834), across pages: the cursor's
// signature known, unknown below the history with the slot read through inside it or below it, a slot alone, nothing
// past it — never a signature skipped, never one past the cursor's point missed.
func TestS7ListAfterNeverSkipsASignature(t *testing.T) {
	l := &s7Lister{}
	for i := 0; i < 2500; i++ {
		l.sigs = append(l.sigs, &rpc.TransactionSignature{Signature: solana.Signature{byte(i), byte(i >> 8), 7}, Slot: uint64(1 + i/3)})
	}
	ctx := context.Background()
	want := func(above uint64, after int) []string { // the signatures past a point, newest first
		var out []string
		for i := len(l.sigs) - 1; i > after && l.sigs[i].Slot > above; i-- {
			out = append(out, l.sigs[i].Signature.String())
		}
		return out
	}
	got := func(ld listed) []string {
		var out []string
		for _, s := range ld.sigs {
			out = append(out, s.Signature.String())
		}
		return out
	}
	same := func(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }
	cases := []struct {
		name  string
		first uint64
		cur   scanCursor
		want  []string
		gap   bool
	}{
		{"the cursor known (1,200 past it, two pages)", 0, scanCursor{Sig: l.sigs[1299].Signature.String(), Through: 433}, want(0, 1299), false},
		{"the cursor below the history, the slot read through inside it", 500, scanCursor{Sig: l.sigs[10].Signature.String(), Through: 600}, want(600, -1), false},
		{"the cursor below the history, the slot read through below it too", 500, scanCursor{Sig: l.sigs[10].Signature.String(), Through: 400}, nil, true},
		{"the cursor below the history, no slot known (a state from before)", 500, scanCursor{Sig: l.sigs[10].Signature.String()}, nil, true},
		{"a slot alone, inside the history (2,400 past it, three pages back)", 0, scanCursor{Through: 34}, want(34, -1), false},
		{"nothing past the slot read through, the history reaching the next slot", 835, scanCursor{Through: 834}, nil, false},
		{"nothing past the slot read through, the history above it", 836, scanCursor{Through: 834}, nil, true},
		{"no cursor at all: every signature", 0, scanCursor{}, want(0, -1), false},
	}
	for _, c := range cases {
		l.first = c.first
		ld, err := listAfter(ctx, l, solana.PublicKey{}, c.cur, 0)
		_, isGap := asHistoryGap(err)
		t.Logf("%s: %d signatures, back %v, gap %v (%v)", c.name, len(ld.sigs), ld.back, isGap, err)
		if c.gap {
			if !isGap {
				t.Fatalf("%s: not refused (%d signatures, err %v)", c.name, len(ld.sigs), err)
			}
			continue
		}
		if err != nil || !same(got(ld), c.want) {
			t.Fatalf("%s: %d signatures, want %d (err %v)", c.name, len(ld.sigs), len(c.want), err)
		}
	}
}

// ------------------------------------------------------------------------------------------- E6-SF5 --

// E6-SF5: a relayer from before 1aea555f (the live 3559a3da) has no fee rule, so started over a state the new relayer
// wrote it Processes every dust burn kept pending — up to a thousand, ≈0.83 GNOT of the relayers' gno key each (the
// audit's rollback_a6: "3 pending, 3 of them dust under the fee"). The rollback step drops them first: the honest burn
// stays, the dust goes with its clocks and cached signatures, and the old loader reads the file.
func TestS7DropDustForTheRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relayer-state.json")
	r := testRelayer(newFake(), "primary")
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	for i, amount := range []uint64{3, 3, 3, 50} {
		m := hl.Message{Version: 3, Nonce: uint32(i), Origin: hl.DomainSolanaTestnet, Sender: token, Destination: hl.DomainGnoTest, Recipient: route, Body: hl.TokenMessage([32]byte{1}, amount, nil)}
		d := solDispatch{Sig: "s", Slot: 10, Raw: hexBytes(m.Bytes())}
		if err := d.fill(); err != nil {
			t.Fatal(err)
		}
		r.state.Pending = append(r.state.Pending, d)
		r.state.Sigs[d.IDHex()] = map[string]string{"k": "sig"}
		r.state.Ready["process:"+d.IDHex()] = r.now()
		if amount < 10 {
			r.state.Left[d.IDHex()] = 10
		}
	}
	honest := r.state.Pending[3].IDHex()
	r.State = path
	if err := r.save(); err != nil {
		t.Fatal(err)
	}
	n, err := DropDust(path)
	if err != nil {
		t.Fatal(err)
	}
	old := testRelayer(newFake(), "primary")
	old.State = path
	if err := old.load(); err != nil {
		t.Fatalf("the state after the rollback step does not load: %v", err)
	}
	t.Logf("dropped %d; pending %d (%s…), left %d, sigs %d, ready %d", n, len(old.state.Pending), honest[:12], len(old.state.Left), len(old.state.Sigs), len(old.state.Ready))
	if n != 3 || len(old.state.Pending) != 1 || old.state.Pending[0].IDHex() != honest || len(old.state.Left) != 0 || len(old.state.Sigs) != 1 || len(old.state.Ready) != 1 {
		t.Fatalf("the rollback step left dust pending or dropped the honest burn")
	}
	if n, err := DropDust(path); err != nil || n != 0 {
		t.Fatalf("a second run: %d %v", n, err)
	}
}

// ------------------------------------------------------------------------------------------- E6-SF3 --

// E6-SF3: the signers before a63337f0 signed a burn of nothing, and a new signer never indexes one, so each such credit
// brought after the deploy holds every new signer at no verdict for a window. The deploy step reads every signer's
// public signatures, nonce by nonce, until a run no signer serves, and names the burns of nothing.
func TestS7ZeroBurnsReadTheSignersStores(t *testing.T) {
	token := [32]byte{9}
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	signed := func(n uint32, amount uint64) []byte {
		m := hl.Message{Version: 3, Nonce: n, Origin: hl.DomainSolanaTestnet, Sender: token, Destination: hl.DomainGnoTest, Recipient: route, Body: hl.TokenMessage([32]byte{1}, amount, nil)}
		raw := m.Bytes()
		id := hl.ID(raw)
		b, _ := json.Marshal(Signed{Origin: m.Origin, Nonce: n, ID: hex.EncodeToString(id[:]), Message: hex.EncodeToString(raw)})
		return b
	}
	serve := func(files map[uint32][]byte) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			n, err := strconv.ParseUint(strings.TrimPrefix(req.URL.Path, "/v1/sol/"), 10, 32)
			if b, ok := files[uint32(n)]; err == nil && ok {
				_, _ = w.Write(b)
				return
			}
			http.NotFound(w, req)
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	a := serve(map[uint32][]byte{0: signed(0, 10), 1: signed(1, 0), 2: signed(2, 0), 4: signed(4, 5)})
	b := serve(map[uint32][]byte{0: signed(0, 10), 1: signed(1, 0), 4: signed(4, 5)})
	cfg := &Config{AllowHTTP: true, Signers: []SignerRef{{URL: a, Key: "signer-a"}, {URL: b, Key: "signer-b"}}}
	zs, read, err := ZeroBurns(context.Background(), cfg, nil, 0, 2)
	for _, z := range zs {
		t.Logf("%s", z)
	}
	t.Logf("%d nonces read", read)
	if err != nil || len(zs) != 2 || zs[0].Nonce != 1 || len(zs[0].Signers) != 2 || zs[1].Nonce != 2 || len(zs[1].Signers) != 1 || read != 7 {
		t.Fatalf("the burns of nothing: %v (read %d, err %v)", zs, read, err)
	}
}

// The review of lane s7, R3's other half: a slot read through stands only while the provider that gave it is the one at
// its place — a liar's leftover (here planted, as a state from before the cap would hold it) is dropped when the config
// names another provider there, and that one, keeping the history, lists the burns below it.
func TestS7AnotherProviderInItsPlaceDropsItsSlot(t *testing.T) {
	through := map[string]uint64{"BUBBLE#0": 1_000_000_000_000, "GNOT#0": 7, "BUBBLE#1": 140}
	hosts := keepThroughOf(through, []string{"https://liar.example", "https://honest.example"}, []string{"https://other.example/?api-key=SECRET", "https://honest.example/v2/K"})
	t.Logf("hosts %v, through %v", hosts, through)
	if len(through) != 1 || through["BUBBLE#1"] != 140 || hosts[0] != "https://other.example" || strings.Contains(strings.Join(hosts, " "), "SECRET") {
		t.Fatalf("the slots of the provider replaced at place 0 stand, or a key is kept: %v %v", through, hosts)
	}
	if kept := map[string]uint64{"BUBBLE#0": 9}; len(keepThroughOf(kept, nil, []string{"https://a", "https://b"})) != 2 || kept["BUBBLE#0"] != 9 {
		t.Fatalf("a state with no record of its providers dropped a slot: %v", kept)
	}
	r := newS4Monitor(t, false)
	r.n.slots(10_000, 10_000)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	r.m.state.Through["BUBBLE#0"] = 1_000_000_000_000 // the liar's leftover
	r.m.state.ThroughHosts = []string{"https://liar.example", "https://honest.example"}
	r.m.state.ThroughHosts = keepThroughOf(r.m.state.Through, r.m.state.ThroughHosts, []string{"https://other.example", "https://honest.example"}) // Run's step at a start
	bob := r.n.burn(r.cfg, 0, 10_015, s4Bob, 300)
	r.n.supply(s4Token, 10_015, 700)
	for i := 0; i < 2; i++ {
		r.n.slots(10_020+uint64(10*i), 10_020+uint64(10*i))
		r.step(10 * time.Second)
		r.g.setHead(201+int64(i), s4ClockZero.Add(time.Duration(15+10*i)*time.Second))
		r.poll()
	}
	if rec := r.m.state.Index[s4IDHex(bob)]; rec == nil || rec.Providers != 3 {
		t.Fatalf("the provider in the liar's place does not list bob: %+v", rec)
	}
}

// DropDust over a path that names no file says so (a mistyped path reported 0 dropped: the review of lane s7).
func TestS7DropDustOverNoFile(t *testing.T) {
	if _, err := DropDust(filepath.Join(t.TempDir(), "no-such-state.json")); err == nil {
		t.Fatal("no error for a state file that is not there")
	}
}

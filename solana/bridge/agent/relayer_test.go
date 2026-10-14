package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// fakeChain is a gno node in memory: canned renders, a call log, failures on demand.
type fakeChain struct {
	mu      sync.Mutex
	renders map[string]string      // "pkg:path" → what Render says
	events  map[string][]gno.Event // "pkg.fn" → the events a call emits
	fail    map[string]error       // "pkg.fn" → the error a call returns
	head    gno.Head
	calls   []string
	height  int64
}

func newFake() *fakeChain {
	return &fakeChain{renders: map[string]string{}, events: map[string][]gno.Event{}, fail: map[string]error{}, height: 100,
		head: gno.Head{Height: 100, Time: time.Unix(1_800_000_000, 0)}}
}

func (f *fakeChain) RenderAt(_ context.Context, pkg, path string, _ int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.renders[pkg+":"+path]
	if !ok {
		return "", fmt.Errorf("fake: no render %s:%s", pkg, path)
	}
	return s, nil
}

func (f *fakeChain) Status(context.Context) (gno.Head, error) { return f.head, nil }

func (f *fakeChain) Block(_ context.Context, height int64) (gno.Head, error) {
	if height > f.head.Height {
		return gno.Head{}, errors.New("fake: no such height")
	}
	return gno.Head{Height: height, Hash: fmt.Sprintf("hash%d", height), Time: f.head.Time}, nil
}

func (f *fakeChain) Call(_ context.Context, pkg, fn string, _ int64, args ...string) (*gno.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, pkg+"."+fn+"("+strings.Join(args, ",")+")")
	if err := f.fail[pkg+"."+fn]; err != nil {
		return nil, err
	}
	f.height++
	return &gno.Result{Hash: fmt.Sprintf("tx%d", len(f.calls)), Height: f.height, Events: f.events[pkg+"."+fn]}, nil
}

func (f *fakeChain) callsOf(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func deterministic(msg string) error {
	return &gno.CallError{Pkg: "r", Fn: "f", Err: errors.New("exit status 1"), Output: "panic: " + msg}
}

func transient() error {
	return &gno.CallError{Pkg: "r", Fn: "f", Err: errors.New("exit status 1"), Output: "dial tcp 127.0.0.1:26657: connection refused"}
}

const (
	testMailbox = "gno.land/r/test/hlmailbox"
	testRoute   = "gno.land/r/test/hlbubble"
)

func testRelayer(f *fakeChain, role string) *Relayer {
	cfg := &Config{}
	cfg.Gno.Mailbox = testMailbox
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute}}
	cfg.Solana.Domain = hl.DomainSolanaTestnet
	now := time.Unix(1_800_000_000, 0)
	r := &Relayer{Cfg: cfg, Role: role, Alarm: &Alarmer{Source: "test"}, gno: f, now: func() time.Time { return now }}
	if _, ok := f.renders[testMailbox+":json"]; !ok {
		// the mailbox's json, which a step reads once for the routes' lines of migration (AR6)
		f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"hook":"","address":"","count":0,"root":"","delivered":0,"live":true,"launchedAt":1,"launchOrigin":%d,"decisions":0,"signers":{"keys":[],"threshold":0,"groupA":[],"groupB":[]},"verifiers":{},"registered":[],"seats":[],"routes":{}}`, hl.DomainGnoTest, hl.DomainSolanaTestnet)
	}
	if err := r.init(); err != nil {
		panic(err)
	}
	r.now = func() time.Time { return now }
	return r
}

func advance(r *Relayer, d time.Duration) {
	t := r.now().Add(d)
	r.now = func() time.Time { return t }
}

func routeRender(open, meter, paidTotal int64, paused string) string {
	return fmt.Sprintf(`{"address":"g1route","live":true,"balance":0,"paused":%q,"symbol":"BUBBLE","domain":1399811150,"router":"","oldRouter":"","oldUntil":0,"lock":1000,"sentTotal":1000,"paidTotal":%d,"owed":0,"voided":0,"lost":0,"sweep":0,"cap":100,"meter":%d,"capFloor":100,"fee":10,"feeFloor":4,"minimum":20,"maximum":2500,"openClaims":%d,"claims":1,"sends":1,"migratedTo":"","decisions":0}`, paused, paidTotal, meter, open)
}

// Claim runs while a claim waits and the meter holds something, and backs off while a call pays nothing.
func TestRelayerClaims(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()

	f.renders[testRoute+":json"] = routeRender(0, 50, 0, "")
	_ = r.claims(ctx)
	if n := len(f.callsOf(testRoute + ".Claim")); n != 0 {
		t.Fatalf("Claim called with no open claim: %d", n)
	}
	f.renders[testRoute+":json"] = routeRender(1, 50, 0, "payouts")
	_ = r.claims(ctx)
	if n := len(f.callsOf(testRoute + ".Claim")); n != 0 {
		t.Fatalf("Claim called while payouts are paused: %d", n)
	}
	f.renders[testRoute+":json"] = routeRender(1, 0, 0, "")
	_ = r.claims(ctx)
	if n := len(f.callsOf(testRoute + ".Claim")); n != 0 {
		t.Fatalf("Claim called on an empty meter: %d", n)
	}

	f.renders[testRoute+":json"] = routeRender(1, 50, 0, "")
	f.events[testRoute+".Claim"] = []gno.Event{{Type: "Claimed", Attrs: map[string]string{"count": "1"}}}
	_ = r.claims(ctx)
	calls := f.callsOf(testRoute + ".Claim")
	if len(calls) != 1 || calls[0] != testRoute+".Claim(20)" {
		t.Fatalf("Claim(20) not called once: %v", calls)
	}
	cs := r.state.Claims["BUBBLE"]
	if cs.Paid != 1 || cs.Backoff != 0 {
		t.Fatalf("after a paying call: %+v", cs)
	}
	// Still waiting, paid again at once.
	_ = r.claims(ctx)
	if n := len(f.callsOf(testRoute + ".Claim")); n != 2 {
		t.Fatalf("a second call while claims wait: %d", n)
	}
	// Nothing paid: back off a minute, then two.
	f.events[testRoute+".Claim"] = []gno.Event{{Type: "Claimed", Attrs: map[string]string{"count": "0"}}}
	_ = r.claims(ctx)
	if cs.Backoff != time.Minute {
		t.Fatalf("backoff after nothing paid: %s", cs.Backoff)
	}
	_ = r.claims(ctx)
	if n := len(f.callsOf(testRoute + ".Claim")); n != 3 {
		t.Fatalf("called again inside the backoff: %d", n)
	}
	advance(r, 61*time.Second)
	_ = r.claims(ctx)
	if n := len(f.callsOf(testRoute + ".Claim")); n != 4 || cs.Backoff != 2*time.Minute {
		t.Fatalf("after the backoff: %d calls, backoff %s", n, cs.Backoff)
	}
}

// The backup acts 60 s after an action became possible, and not at all if someone else took it.
func TestRelayerBackupOffset(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "backup")
	ctx := context.Background()
	f.renders[testRoute+":json"] = routeRender(1, 50, 0, "")
	f.events[testRoute+".Claim"] = []gno.Event{{Type: "Claimed", Attrs: map[string]string{"count": "1"}}}
	_ = r.claims(ctx)
	advance(r, 30*time.Second)
	_ = r.claims(ctx)
	if n := len(f.callsOf(testRoute + ".Claim")); n != 0 {
		t.Fatalf("the backup acted inside 60 s: %d", n)
	}
	// The primary paid (paidTotal moved): the clock restarts.
	f.renders[testRoute+":json"] = routeRender(1, 50, 10, "")
	advance(r, 40*time.Second)
	_ = r.claims(ctx)
	if n := len(f.callsOf(testRoute + ".Claim")); n != 0 {
		t.Fatalf("the backup acted although the primary just paid: %d", n)
	}
	advance(r, 61*time.Second)
	_ = r.claims(ctx)
	if n := len(f.callsOf(testRoute + ".Claim")); n != 1 {
		t.Fatalf("the backup did not act after 60 s of nobody paying: %d", n)
	}
}

func decisionsRender(ds ...decisionJSON) string {
	var parts []string
	for _, d := range ds {
		parts = append(parts, fmt.Sprintf(`{"id":%d,"kind":%q,"text":"t","state":%q,"made":%d,"recorded":%d,"due":%d,"applied":%d,"cancelled":0,"note":""}`,
			d.ID, d.Kind, d.State, d.Made, d.Recorded, d.Due, d.Applied))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Execute(id) is called for a queued decision once its delay has passed by block time, never before, and
// never for one applied at once; three deterministic failures park it, retried hourly; a transient failure
// is retried in a minute.
func TestRelayerExecutes(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	now := f.head.Time.Unix()
	queued := decisionJSON{ID: 2, Kind: "set cap floor", State: "pending", Made: now - 100, Recorded: now - 100, Due: now + 500}
	atOnce := decisionJSON{ID: 1, Kind: "cancel", State: "applied", Made: now - 100, Recorded: now - 100, Due: now - 100, Applied: now - 100}
	f.renders[testMailbox+":decisions.json"] = "[]"
	f.renders[testRoute+":decisions.json"] = decisionsRender(atOnce, queued)
	_ = r.executes(ctx)
	if n := len(f.callsOf(testRoute + ".Execute")); n != 0 {
		t.Fatalf("Execute called before the delay: %d", n)
	}
	f.head.Time = time.Unix(now+500, 0) // block time reaches the due time
	_ = r.executes(ctx)
	calls := f.callsOf(testRoute + ".Execute")
	if len(calls) != 1 || calls[0] != testRoute+".Execute(2)" {
		t.Fatalf("Execute(2) not called once: %v", calls)
	}
	// Still rendered pending (the chain lags): called again next tick is fine, the realm refuses a repeat.
	// A deterministic failure three times parks it.
	f.renders[testRoute+":decisions.json"] = decisionsRender(decisionJSON{ID: 3, Kind: "credit", State: "pending", Made: now, Recorded: now, Due: now + 400})
	f.fail[testRoute+".Execute"] = deterministic("hlroute: the recipient is gone")
	key := "execute:" + testRoute + "#3"
	for i := 1; i <= 3; i++ {
		_ = r.executes(ctx)
		a := r.state.Attempts[key]
		if a == nil || a.Failures != i {
			t.Fatalf("after failure %d: %+v", i, a)
		}
		if i < 3 {
			if !a.Parked.IsZero() {
				t.Fatalf("parked after %d failures", i)
			}
			wait := backoffMin << uint(i-1)
			if a.Next != r.now().Add(wait) {
				t.Fatalf("backoff after failure %d: %s, want %s", i, a.Next.Sub(r.now()), wait)
			}
			_ = r.executes(ctx) // inside the backoff: no call
			if n := len(f.callsOf(testRoute + ".Execute(3)")); n != i {
				t.Fatalf("called inside the backoff: %d", n)
			}
			advance(r, wait)
		}
	}
	a := r.state.Attempts[key]
	if a.Parked.IsZero() || a.Next != r.now().Add(parkedRetry) {
		t.Fatalf("not parked with an hourly retry: %+v", a)
	}
	if _, ok := r.Alarm.Standing()["parked:"+key]; !ok {
		t.Fatal("no alarm for the parked decision")
	}
	advance(r, parkedRetry)
	delete(f.fail, testRoute+".Execute")
	_ = r.executes(ctx)
	if n := len(f.callsOf(testRoute + ".Execute(3)")); n != 4 {
		t.Fatalf("the hourly retry did not happen: %d", n)
	}
	if r.state.Attempts[key] != nil {
		t.Fatal("the attempt record survived success")
	}
	// A transient failure counts for nothing and retries in a minute.
	f.renders[testRoute+":decisions.json"] = decisionsRender(decisionJSON{ID: 4, Kind: "credit", State: "pending", Made: now, Recorded: now, Due: now + 400})
	f.fail[testRoute+".Execute"] = transient()
	_ = r.executes(ctx)
	a = r.state.Attempts["execute:"+testRoute+"#4"]
	if a == nil || a.Failures != 0 || a.Next != r.now().Add(backoffMin) {
		t.Fatalf("after a transient failure: %+v", a)
	}
}

func msgRender(m hl.Message, height int64) string {
	raw := m.Bytes()
	id := hl.ID(raw)
	return fmt.Sprintf(`{"nonce":%d,"id":%q,"root":"%s","height":%d,"message":%q}`, m.Nonce, hex.EncodeToString(id[:]), strings.Repeat("00", 32), height, hex.EncodeToString(raw))
}

type fakeClient struct {
	setFrom int64
	leaves  map[int64]int
}

func (c fakeClient) CurrentSetFrom(context.Context) (int64, error) { return c.setFrom, nil }
func (c fakeClient) LeafBytes(_ context.Context, h int64, _ [32]byte) (int, bool, error) {
	return c.leaves[h], true, nil
}

// Reemit is called once for a message dispatched before the current validator set's first height, and once
// for a results leaf the ISM would refuse; never for a delivered one, never twice for the same reason.
func TestNeedsReemit(t *testing.T) {
	cases := []struct {
		name string
		in   reemitInput
		want bool
	}{
		{"nothing known", reemitInput{DispatchHeight: 50}, false},
		{"before the set", reemitInput{DispatchHeight: 50, CurrentSetFrom: 100}, true},
		{"after the set", reemitInput{DispatchHeight: 150, CurrentSetFrom: 100}, false},
		{"re-emitted since the set began", reemitInput{DispatchHeight: 50, CurrentSetFrom: 100, LastReemitHeight: 120}, false},
		{"re-emitted before another change", reemitInput{DispatchHeight: 50, CurrentSetFrom: 200, LastReemitHeight: 120}, true},
		{"delivered", reemitInput{DispatchHeight: 50, CurrentSetFrom: 100, Delivered: true}, false},
		{"a big leaf", reemitInput{DispatchHeight: 150, CurrentSetFrom: 100, LeafBytes: 9000}, true},
		{"a big leaf, re-emitted", reemitInput{DispatchHeight: 150, CurrentSetFrom: 100, LeafBytes: 9000, LastReemitHeight: 160}, false},
		{"a small leaf", reemitInput{DispatchHeight: 150, CurrentSetFrom: 100, LeafBytes: 8192}, false},
		{"an unbuildable block", reemitInput{DispatchHeight: 150, CurrentSetFrom: 100, LeafUnbuildable: true}, true},
		{"re-emitted into a buildable block", reemitInput{DispatchHeight: 150, CurrentSetFrom: 100, LeafUnbuildable: false, LastReemitHeight: 160}, false},
	}
	for _, c := range cases {
		if got, _ := needsReemit(c.in); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRelayerReemits(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	msg := hl.Message{Version: 3, Nonce: 0, Origin: hl.DomainGnoTest, Sender: route, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, 5, nil)}
	other := hl.Message{Version: 3, Nonce: 1, Origin: hl.DomainGnoTest, Destination: hl.DomainSolanaTestnet, Recipient: [32]byte{8}, Body: nil}
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"hook":"","address":"","count":2,"root":"","delivered":0,"live":true,"launchedAt":1,"launchOrigin":%d,"decisions":0,"signers":{"keys":[],"threshold":0,"groupA":[],"groupB":[]},"verifiers":{},"registered":[],"seats":[],"routes":{}}`, hl.DomainGnoTest, hl.DomainSolanaTestnet)
	f.renders[testMailbox+":msg/0"] = msgRender(msg, 50)
	f.renders[testMailbox+":msg/1"] = msgRender(other, 50)
	r.client = fakeClient{setFrom: 0}
	_ = r.reemits(ctx)
	if n := len(f.callsOf(testMailbox + ".Reemit")); n != 0 {
		t.Fatalf("Reemit with nothing known: %d", n)
	}
	r.client = fakeClient{setFrom: 100}
	_ = r.reemits(ctx)
	calls := f.callsOf(testMailbox + ".Reemit")
	if len(calls) != 1 || calls[0] != testMailbox+".Reemit(0)" {
		t.Fatalf("Reemit(0) once, and not for the message to another recipient: %v", calls)
	}
	_ = r.reemits(ctx)
	if n := len(f.callsOf(testMailbox + ".Reemit")); n != 1 {
		t.Fatalf("Reemit repeated for the same set: %d", n)
	}
	r.client = fakeClient{setFrom: 200}
	_ = r.reemits(ctx)
	if n := len(f.callsOf(testMailbox + ".Reemit")); n != 2 {
		t.Fatalf("no Reemit after another set change: %d", n)
	}
}

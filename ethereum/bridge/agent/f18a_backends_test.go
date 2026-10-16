package agent

// Fold F18a (round 6): the Go trust path's E6-GT2 — an endpoint answering from more than one backend is no own node
// (§11.10): a head or an hlmailbox count reading lower than one read before it, a mark set above a head read after it,
// or more deliveries placed at a cut's head than hlmailbox's count after it, refuses every verdict and alarms
// gno:backends at flag level, kept until the operator gives the machine a node of its own. Each test fails on the
// parent (e3d0053f); the audit's measurement is audit-r6/go-trust-pocs/zz_audit6_lb_test.go.

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

const f18aBackends = "gno:backends" // backendsKey (a literal here, so this file compiles on the parent)

// f18aRefused says whether a monitor's last poll refused its verdict over a second backend: no verdict, the alarm at
// flag level.
func f18aRefused(r *monRig) bool {
	return !r.m.last.Verdict && flagLevel(r.m.Alarm, f18aBackends) && strings.Contains(cutText(r.m.Status()), "more than one backend")
}

// A head reading lower than one read before it — a load balancer's second backend a block behind — refuses the
// verdict, and the finding is kept: the head back above, a restart over the same state, the machine still refuses (the
// parent: a verdict every poll). hlwatch over it is never green.
func TestF18aHeadLowerThanAnEarlierReadIsRefused(t *testing.T) {
	r := newMonRig(t)
	r.m.State = filepath.Join(t.TempDir(), "monitor.json")
	r.step(10 * time.Second) // head 61
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if !r.m.last.Verdict {
		t.Fatalf("setup: %v", cutText(r.m.Status()))
	}
	r.now = r.now.Add(10 * time.Second)
	r.gno.advance(60, r.now.Add(time.Second)) // the backend behind answers
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("head 61 then 60: verdict %v; cut %q; alarms %v", r.m.last.Verdict, cutText(r.m.Status()), r.m.Alarm.StandingKeys())
	if !f18aRefused(r) {
		t.Fatalf("a head read lower than the one before it was taken for one node's")
	}
	r.step(10 * time.Second) // the head 61 again, then 62
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if !f18aRefused(r) {
		t.Fatalf("the finding was not kept once the head read higher: %q", cutText(r.m.Status()))
	}
	m2 := &Monitor{Cfg: r.m.Cfg, State: r.m.State, Alarm: &Alarmer{Source: "test2"}, Every: 10 * time.Second, pair: r.m.pair, own: r.gno, watch: r.gno, sources: r.m.sources, ethWatch: r.snd, now: func() time.Time { return r.now }}
	if err := m2.init(); err != nil {
		t.Fatal(err)
	}
	r.m = m2
	r.m.ReadOnly = true
	r.step(10 * time.Second)
	_, green, why := r.view(t)
	if !f18aRefused(r) || green || !strings.Contains(why, "flag-level alarm "+f18aBackends) {
		t.Fatalf("after a restart the finding was forgotten, or hlwatch green: refused %v, green %v: %s", f18aRefused(r), green, why)
	}
}

// The signer: hlmailbox's delivered count reading lower than one read before it refuses the round's verdict; the
// signer signs nothing past its last verdict (the parent: the lower count read as a verdict, signing on).
func TestF18aSignerCountLowerThanAnEarlierReadIsRefused(t *testing.T) {
	r := newSignerRig(t)
	r.step(time.Second)
	r.gno.set(m3bMailPath, "delivered/"+strings.Repeat("d0", 32), strconv.FormatInt(r.gno.height, 10)) // a delivery to no route
	r.round()
	if st := r.status(); st["signing"] != true {
		t.Fatalf("setup: %v", st["cut"])
	}
	r.gno.set(m3bMailPath, "delivered/"+strings.Repeat("d0", 32), "0") // the backend behind: hlmailbox's delivered count 0
	for i := 0; i < 3; i++ {
		r.step(time.Second)
		r.round()
	}
	st := r.status()
	t.Logf("signer: signing=%v cut=%q alarms %v", st["signing"], st["cut"], r.s.Alarm.StandingKeys())
	if st["signing"] == true || !r.alarmed(f18aBackends) || !strings.Contains(fmt.Sprint(st["cut"]), "more than one backend") {
		t.Fatalf("a delivered count read lower than the one before it was taken for one node's: %v", st)
	}
}

// A delivered mark set above a head read after it — a mark from the backend ahead, the head from the one behind — is
// no one node's answer: refused (the parent: the honest credit's reading taken, as it came).
func TestF18aMarkAboveALaterHeadIsRefused(t *testing.T) {
	r := newMonRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	id := r.chain.burn(m3bGNOT, gnot, r.alice, 200, 80)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(id[:]), "75") // credited at 75, the head at 61
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 200, 0, 0))
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("a mark at 75 under heads of 61: verdict %v; cut %q", r.m.last.Verdict, cutText(r.m.Status()))
	if !f18aRefused(r) {
		t.Fatalf("a mark set above a head read after it was taken for one node's")
	}
}

// f18aClone copies a fake node's state: a backend that has not applied what the original applies next.
func f18aClone(g *m3bGnoNode) *m3bGnoNode {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := newM3bGno(g.height, g.time)
	for k, v := range g.renders {
		c.renders[k] = v
	}
	for k, v := range g.hashes {
		c.hashes[k] = v
	}
	return c
}

// f18aTwoBackends is an endpoint before two nodes, each read answered by the one a rule picks.
type f18aTwoBackends struct {
	ahead, behind *m3bGnoNode
	behindFor     func(what string) bool // "pkg:path", "status" or "block"
}

func (b *f18aTwoBackends) pick(what string) *m3bGnoNode {
	if b.behindFor(what) {
		return b.behind
	}
	return b.ahead
}

func (b *f18aTwoBackends) RenderAt(ctx context.Context, pkg, path string, h int64) (string, error) {
	return b.pick(pkg+":"+path).RenderAt(ctx, pkg, path, h)
}
func (b *f18aTwoBackends) Status(ctx context.Context) (gno.Head, error) {
	return b.pick("status").Status(ctx)
}
func (b *f18aTwoBackends) Block(ctx context.Context, h int64) (gno.Head, error) {
	return b.pick("block").Block(ctx, h)
}

// The audit's shape (b): bob's honest burn of 300 credited on the backend ahead only; hlmailbox's count read from the
// backend behind (0: the count bob's mark was read unset at, so it is not read again by the count), the route from the
// one ahead (owed 300): bob in flight and in owed, nothing unplaced — flagged at once by the parent. Every mark is read
// again under a BROKEN reading now, bob's from the backend ahead, and the deliveries placed at the cut's head (1) pass
// hlmailbox's count read after it (0): refused, never flagged.
func TestF18aStaleCountFromASecondBackendIsRefused(t *testing.T) {
	r := newMonRig(t)
	r.chain.retime(uint64(r.now.Unix()) - 12*100 - 13*60)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.chain.mine(162, 110)
	bob := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 300, 105)
	r.step(10 * time.Second)
	if err := r.poll(); err != nil { // bob's mark read unset at the count 0
		t.Fatal(err)
	}
	behind := f18aClone(r.gno)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(bob[:]), strconv.FormatInt(r.gno.height, 10))
	r.step(10 * time.Second)
	behind.advance(r.gno.height, r.gno.time)
	r.m.own = &f18aTwoBackends{ahead: r.gno, behind: behind, behindFor: func(what string) bool { return what == m3bMailPath+":json" }}
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("shape (b): verdict %v; gno flags %v, eth flag txs %v; cut %q", r.m.last.Verdict, r.gnoFlags(), r.snd.calls, cutText(r.m.Status()))
	if len(r.gnoFlags()) > 0 || r.snd.count() > 0 {
		t.Fatalf("an honest credit read through two backends was flagged: %v %v", r.gnoFlags(), r.snd.calls)
	}
	if !f18aRefused(r) {
		t.Fatalf("the stale count from a second backend was not refused: %q", cutText(r.m.Status()))
	}
}

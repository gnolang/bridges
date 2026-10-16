package agent

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// E4-GT1 (go-trust-poc/zz_audit4_lagging_final_test.go): an honest burn the other signers saw final, signed and the
// relayer credited, while one of this machine's providers trails the chain's finalized block by 15 blocks: the pair's
// finalized block (the lower answer) is below the burn, so the index cannot place the credit, and the supply at F and
// owed both count it. The reading is no verdict, never BROKEN — no flag on gno.land or Ethereum — and once the provider
// catches up the index places the burn and the identity holds.
func TestF15bLaggingProviderFlagsNothing(t *testing.T) {
	r := newMonRig(t)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.mine(130, 130)
	id := r.chain.burn(m3bGNOT, gnot, r.alice, 300, 120)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(id[:]), "61")
	r.pa.mu.Lock()
	r.pa.lag = 15
	r.pa.mu.Unlock()
	for i := 0; i < 3; i++ { // the provider trails for half a minute
		r.step(10 * time.Second)
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
	}
	gid := r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)
	t.Logf("trailing: identity %v %q; gno flags %v; eth flag txs %v", gid["verdict"], gid["text"], r.gnoFlags(), r.snd.calls)
	if len(r.gnoFlags()) > 0 || r.snd.count() > 0 {
		t.Fatalf("an honest credit above the trailing pair's finalized block flagged: gno %v, eth %v", r.gnoFlags(), r.snd.calls)
	}
	if gid["verdict"] != "no verdict" || r.alarmed("identity:GNOT:flag") {
		t.Fatalf("the reading is not pending: %v %v", gid, r.m.Alarm.StandingKeys())
	}
	// the provider catches up: the burn is placed (credited at 61, its block 120 above the reading's 115: the lag),
	// the pending reading ends, the identity holds
	r.pa.mu.Lock()
	r.pa.lag = 0
	r.pa.mu.Unlock()
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	gid = r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)
	t.Logf("caught up: identity %v", gid["verdict"])
	if gid["verdict"] != "holds" || len(r.gnoFlags()) > 0 || r.snd.count() > 0 {
		t.Fatalf("after the provider caught up: %v, flags %v %v", gid, r.gnoFlags(), r.snd.calls)
	}
}

// The signer's side: no halt (and no signer:halt alarm) over the same reading; the round has no verdict, so nothing is
// signed meanwhile, and once its pair catches up it signs again.
func TestF15bLaggingProviderHaltsNoSigner(t *testing.T) {
	r := newSignerRig(t)
	r.round()
	if st := r.status(); st["signing"] != true {
		t.Fatalf("round 1: %v", st)
	}
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.mine(130, 130)
	id := r.chain.burn(m3bGNOT, gnot, r.alice, 300, 120)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(id[:]), "61")
	r.pa.mu.Lock()
	r.pa.lag = 15
	r.pa.mu.Unlock()
	for i := 0; i < 3; i++ {
		r.step(time.Second)
		r.round()
	}
	st := r.status()
	t.Logf("trailing: signing=%v halted=%q cut=%q", st["signing"], st["halted"], st["cut"])
	if r.alarmed("signer:halt") || st["halted"] != "" {
		t.Fatalf("a halt over an honest credit above the trailing pair's finalized block: %v", st["halted"])
	}
	if st["signing"] == true {
		t.Fatal("the signer signs on a stale verdict while its reading waits")
	}
	r.pa.mu.Lock()
	r.pa.lag = 0
	r.pa.mu.Unlock()
	r.step(time.Second)
	r.round()
	st = r.status()
	t.Logf("caught up: signing=%v halted=%q", st["signing"], st["halted"])
	if st["signing"] != true {
		t.Fatalf("after the pair caught up: %v", st)
	}
}

// hlwatch over the same honest credit, its own pair trailing: no BROKEN line and no unindexed-credit alarm naming the
// honest claim; the page says why it waits and is not green.
func TestF15bLaggingProviderHlwatchWaits(t *testing.T) {
	r := newMonRig(t)
	r.m.ReadOnly = true
	w := &Watch{m: r.m}
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.mine(130, 130)
	id := r.chain.burn(m3bGNOT, gnot, r.alice, 300, 120)
	idh := hex.EncodeToString(id[:])
	claim := `{"id":"` + idh + `","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":300,"state":"open","by":"verifier","height":61}`
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+idh, "61")
	r.gno.set(m3bNS+"hlgnot", "claims/0", "["+claim+"]")
	r.gno.set(m3bNS+"hlgnot", "claim/"+idh, claim)
	r.pa.mu.Lock()
	r.pa.lag = 15
	r.pa.mu.Unlock()
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ok, why := v.Green()
	t.Logf("hlwatch: green=%v broken=%v identity=%q; why %v", ok, v.Broken, v.Identity["GNOT"], why)
	if len(v.Broken) > 0 || strings.Contains(strings.Join(why, ";"), "credit:unindexed") {
		t.Fatalf("hlwatch names the honest credit a forgery: %v %v", v.Broken, why)
	}
	if ok || !strings.HasPrefix(v.Identity["GNOT"], "no verdict") {
		t.Fatalf("hlwatch green, or no word of the wait: %v %q", why, v.Identity["GNOT"])
	}
}

// A forged credit — a delivery no burn backs, owed by its amount — is never cleared by the wait: the reading is kept
// pending and, once the index's finalized block's time has passed the reading's, flagged as a break (credits, payouts,
// the gate); the waiting claim is named a forged credit from then on.
func TestF15bForgedCreditFlaggedOnceTheIndexPassesIt(t *testing.T) {
	r := newMonRig(t)
	forged := strings.Repeat("ef", 32)
	claim := `{"id":"` + forged + `","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":300,"state":"open","by":"verifier","height":61}`
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+forged, "61")
	r.gno.set(m3bNS+"hlgnot", "claims/0", "["+claim+"]")
	r.gno.set(m3bNS+"hlgnot", "claim/"+forged, claim)
	for i := 0; i < 2; i++ {
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		r.step(10 * time.Second)
	}
	gid := r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)
	t.Logf("before the index passes it: identity %v; flags %v %v", gid["verdict"], r.gnoFlags(), r.snd.calls)
	if len(r.gnoFlags()) > 0 || r.snd.count() > 0 || gid["verdict"] != "no verdict" {
		t.Fatalf("judged before the index's finalized block passed the credit: %v %v %v", r.gnoFlags(), r.snd.calls, gid)
	}
	r.chain.retime(uint64(r.now.Unix())) // the finalized block 100 twenty minutes after the reading
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("once it passed: flags %v %v; alarms %v", r.gnoFlags(), r.snd.calls, r.m.Alarm.StandingKeys())
	if len(r.gnoFlags()) != 2 || r.snd.count() != 1 || !r.alarmed("identity:GNOT:flag") || !r.alarmed("credit:unindexed:"+forged+":flag") {
		t.Fatalf("the forged credit was not flagged once the index passed it: %v %v %v", r.gnoFlags(), r.snd.calls, r.m.Alarm.StandingKeys())
	}
}

// The signer over a forged credit: no verdict while it waits (nothing signed), then a halt once the index passes it.
func TestF15bSignerHaltsOnAForgedCreditOnceTheIndexPassesIt(t *testing.T) {
	r := newSignerRig(t)
	r.round()
	forged := strings.Repeat("ef", 32)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+forged, "61")
	for i := 0; i < 3; i++ {
		r.step(time.Second)
		r.round()
	}
	if st := r.status(); st["signing"] == true || st["halted"] != "" {
		t.Fatalf("waiting: %v", st)
	}
	r.chain.retime(uint64(r.now.Unix()))
	r.step(time.Second)
	r.round()
	st := r.status()
	t.Logf("once the index passed it: signing=%v halted=%q", st["signing"], st["halted"])
	if st["signing"] == true || !r.alarmed("signer:halt") || !strings.Contains(st["halted"].(string), "E4-GT1") {
		t.Fatalf("no halt once the index passed the forged credit: %v", st)
	}
}

// The fast path for a forged credit (the review's finding 3): a waiting claim no burn of the index carries, whose id
// neither provider — each current to its head — shows dispatched above the index either, is no lag: flagged at once,
// not a finality lag later. An honest burn above a trailing pair's finalized block is shown above it and keeps the
// wait.
func TestF15bForgedClaimFlaggedAtOnceWhenNoProviderShowsItsDispatch(t *testing.T) {
	r := newMonRig(t)
	r.chain.mine(130, 100)
	r.chain.retime(uint64(r.now.Unix()) - 30 - 130*12) // the head 30 s old, the finalized block 100 six minutes behind it
	forged := strings.Repeat("ef", 32)
	claim := `{"id":"` + forged + `","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":300,"state":"open","by":"verifier","height":61}`
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+forged, "59")
	r.gno.set(m3bNS+"hlgnot", "claims/0", "["+claim+"]")
	r.gno.set(m3bNS+"hlgnot", "claim/"+forged, claim)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("first poll: flags %v %v; %s", r.gnoFlags(), r.snd.calls, r.m.Alarm.Standing()["credit:unindexed:"+forged+":flag"])
	if !r.alarmed("credit:unindexed:"+forged+":flag") || len(r.gnoFlags()) != 2 || r.snd.count() != 1 {
		t.Fatalf("a forged credit no provider shows dispatched was not flagged at once: %v %v %v", r.gnoFlags(), r.snd.calls, r.m.Alarm.StandingKeys())
	}
	// the honest lag with current providers: the burn at 120 is above the trailing pair's 115, shown there — it waits
	h := newMonRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	h.chain.mine(130, 130)
	id := h.chain.burn(m3bGNOT, gnot, h.alice, 300, 120)
	h.chain.retime(uint64(h.now.Unix()) - 30 - 130*12)
	idh := hex.EncodeToString(id[:])
	honest := `{"id":"` + idh + `","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":300,"state":"open","by":"verifier","height":59}`
	h.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	h.gno.set(m3bMailPath, "delivered/"+idh, "59")
	h.gno.set(m3bNS+"hlgnot", "claims/0", "["+honest+"]")
	h.gno.set(m3bNS+"hlgnot", "claim/"+idh, honest)
	h.pa.mu.Lock()
	h.pa.lag = 15
	h.pa.mu.Unlock()
	if err := h.poll(); err != nil {
		t.Fatal(err)
	}
	if len(h.gnoFlags()) > 0 || h.snd.count() > 0 || h.alarmed("credit:unindexed:"+idh+":flag") {
		t.Fatalf("an honest credit shown above the trailing pair was flagged: %v %v", h.gnoFlags(), h.snd.calls)
	}
}

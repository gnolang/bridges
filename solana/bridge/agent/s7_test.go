package agent

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// Lane s7 (2026-10-16): round 6's live-fixes audit folded into the live Solana bridge's Go — E6-SF1 (a provider's short
// history), E6-SF2 (one provider honouring minContextSlot on a monitor), E6-SF4/SF6 (the relayer's and the monitors' dust),
// E6-GT1's Solana sibling.

// ------------------------------------------------------------------------------------------- E6-SF1 --

// s7Poll is a monitor's poll, its state saved, with its node's head at its own clock (the head's age judged against it);
// it answers the poll's error (the whole poll without a verdict).
func (r *s4Monitor) s7Poll(height int64) error {
	r.g.setHead(height, r.clock)
	err := r.m.poll(context.Background())
	if err := r.m.save(); err != nil {
		r.t.Fatal(err)
	}
	return err
}

// E6-SF1: publicnode keeps a day of ledger and answers −32020 "Transaction … not found" for an `until` below it. A route
// with no transaction for a day — BUBBLE has had none since its launch — left the cursor (the route's last transaction)
// below that history, and the scan failed every poll for good: the live monitor 2 without a verdict since 05:49 UTC
// 10-15. The cursor now carries the slot the route was read through, which every poll's listing (held to the supply's
// slot) moves; a provider that does not know the cursor is listed back from its newest signature to that slot, its first
// available block at or below it. A burn after the history passed the cursor is indexed by both providers.
func TestS7MonitorQuietRouteOutlivesAShortHistory(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.s7Poll(200)
	r.n.burn(r.cfg, 0, 101, s4Bob, 300) // the route's last transaction for a day
	r.n.supply(s4Token, 101, 700)
	h := int64(201)
	for slot := uint64(110); slot <= 11000; slot += 989 { // the route quiet, the polls going on (a day, in the rig's slots)
		r.n.slots(slot, slot)
		r.step(10 * time.Second)
		r.s7Poll(h)
		h++
	}
	if v := r.verdict(); v != "holds" {
		t.Fatalf("before the history passes the cursor: %s (%s)", v, r.cut())
	}
	t.Logf("cursors %v, read through %v", r.m.state.Cursors, r.m.state.Through) // s7:new
	// provider 1's ledger now begins at 9,000: the cursor's transaction (slot 101) is below it
	r.n.keepHistory(0, 9000)
	var out []string
	for i := 0; i < 3; i++ {
		r.n.slots(11001+uint64(i), 11001+uint64(i))
		r.step(10 * time.Second)
		err := r.s7Poll(h)
		h++
		out = append(out, fmt.Sprintf("%s [%.120s]", r.verdict(), r.cut()))
		if err != nil {
			t.Fatalf("poll %d after provider 1's history passed the cursor failed whole (no verdict): %.300v", i+1, err)
		}
	}
	t.Logf("polls after provider 1's history passed the cursor: %s; alarms %v", strings.Join(out, " | "), r.m.Alarm.StandingKeys())
	if v := r.verdict(); v != "holds" || r.alarmed("solana:history:1") || r.alarmed("monitor:no-verdict") {
		t.Fatalf("a quiet route outliving provider 1's history: %s (%s); alarms %v", v, r.cut(), r.m.Alarm.StandingKeys())
	}
	alice := r.n.burn(r.cfg, 1, 11050, s4Alice, 100) // a burn after the history passed the cursor
	r.n.supply(s4Token, 11050, 600)
	r.n.slots(11060, 11060)
	r.step(10 * time.Second)
	r.s7Poll(h)
	rec := r.m.state.Index[s4IDHex(alice)]
	t.Logf("the burn at 11050: %+v; verdict %s", rec, r.verdict())
	if rec == nil || rec.Providers != 3 || r.verdict() != "holds" {
		t.Fatalf("a burn after the history passed the cursor: %+v, %s (%s)", rec, r.verdict(), r.cut())
	}
}

// A provider whose history does not reach where the route was read — a state written before the slot was kept (the
// deploy over the live state), or a machine stopped longer than the provider's day — is refused for that route, named by
// solana:history:<i> once it has stood two minutes: the token has no verdict (never a reading on the other provider's
// word), the rest of the poll runs; a provider that keeps the history ends it.
func TestS7MonitorRefusesAProviderWithoutTheHistory(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.s7Poll(200)
	r.n.burn(r.cfg, 0, 101, s4Bob, 300)
	r.n.supply(s4Token, 101, 700)
	r.n.slots(102, 102)
	r.step(10 * time.Second)
	r.s7Poll(201)
	r.m.state.Through = map[string]uint64{} // the live state: cursors alone // s7:new
	r.n.keepHistory(0, 900)
	h := int64(202)
	for i := 0; i < 14; i++ { // over two minutes
		r.n.slots(1000+uint64(i), 1000+uint64(i))
		r.step(10 * time.Second)
		if err := r.s7Poll(h); err != nil {
			t.Fatalf("poll %d: the poll failed whole over the one provider without the history: %.300v", i+1, err)
		}
		h++
		if v := r.verdict(); v != "no verdict" {
			t.Fatalf("poll %d: provider 1 without the history read %s (%s)", i+1, v, r.cut())
		}
	}
	text := r.m.Alarm.Standing()["solana:history:1"]
	t.Logf("verdict %s; solana:history:1 %q; waiting %v; alarms %v", r.verdict(), text, r.m.status.Waiting, r.m.Alarm.StandingKeys())
	if !strings.Contains(text, "BUBBLE") || r.alarmed("solana:history:2") || r.alarmed("monitor:no-verdict") {
		t.Fatalf("the refused provider is not named, or the poll failed whole: %v", r.m.Alarm.StandingKeys())
	}
	if r.m.state.Cursors["BUBBLE#0"] == "" {
		t.Fatalf("the refused provider's cursor was dropped (a guess past the gap)")
	}
	r.n.keepHistory(0, 0) // the config names a provider with the history
	r.n.slots(1020, 1020)
	r.step(10 * time.Second)
	r.s7Poll(h)
	if v := r.verdict(); v != "holds" || r.alarmed("solana:history:1") {
		t.Fatalf("a provider with the history: %s, alarms %v", v, r.m.Alarm.StandingKeys())
	}
}

// The signer the same: its round keeps its verdict past the cursor's leaving the history (the live signers 1 and 2
// halted at the hour), the next burn is signed, and a restart lists from the slot its state keeps (the signing cursor,
// the last signed burn's transaction, is below the history).
func TestS7SignerQuietRouteOutlivesAShortHistory(t *testing.T) {
	r := newS4Signer(t, false)
	clock := s4ClockZero
	r.s.now = func() time.Time { return clock }
	r.n.slots(100, 100)
	r.round()
	r.n.burn(r.cfg, 0, 101, s4Bob, 300)
	r.n.supply(s4Token, 101, 700)
	r.n.slots(102, 102)
	r.round()
	r.round()
	if !r.signed(0) {
		t.Fatalf("bob's burn not signed: %v", r.s.Status())
	}
	h := int64(201)
	round := func(slot uint64) {
		r.n.slots(slot, slot)
		clock = clock.Add(4 * time.Second)
		r.g.setHead(h, clock)
		h++
		r.round()
	}
	for slot := uint64(110); slot <= 11000; slot += 989 { // a day, in the rig's slots
		round(slot)
	}
	r.n.keepHistory(0, 9000)
	for i := 0; i < 5; i++ {
		round(11001 + uint64(i))
	}
	clock = clock.Add(2 * time.Hour) // two hours of rounds: one with no verdict for an hour halts
	round(11010)
	st := r.s.Status()
	t.Logf("after provider 1's history passed the cursor: signing %v, halted %q, cut %.140q", st["signing"], st["halted"], r.cut())
	if st["signing"] != true || st["halted"] != "" {
		t.Fatalf("the signer over a quiet route outliving provider 1's history: signing %v, halted %q (%s)", st["signing"], st["halted"], r.cut())
	}
	r.n.burn(r.cfg, 1, 11050, s4Alice, 100)
	r.n.supply(s4Token, 11050, 600)
	round(11060)
	round(11061)
	if !r.signed(1) {
		t.Fatalf("the burn after the history passed the cursor was not signed: %s", r.cut())
	}
	// a restart: the store's signing cursor (alice's transaction) below the history by then, or the slot the state keeps
	for slot := uint64(12000); slot <= 13000; slot += 500 {
		round(slot)
	}
	r.n.keepHistory(0, 11500) // the history passes alice's burn too
	round(13100)
	if err := r.s.save(); err != nil {
		t.Fatal(err)
	}
	s2 := r.newSigner(r.s.Store)
	s2.now = func() time.Time { return clock }
	t.Logf("the state's restart slots: %v", s2.state.SolThrough) // s7:new
	r.s = s2
	round(13101)
	st = r.s.Status()
	if st["signing"] != true || strings.HasPrefix(r.cut(), "no verdict") {
		t.Fatalf("the restarted signer over a signing cursor below the history: signing %v (%s)", st["signing"], r.cut())
	}
}

// A signer whose state names no slot (the live one) and whose provider's history passed its signing cursor is refused at
// its restart, never a guess: no verdict (a signer never signs before its first), its alarm.
func TestS7SignerRefusesAProviderWithoutTheHistory(t *testing.T) {
	r := newS4Signer(t, false)
	clock := s4ClockZero
	r.s.now = func() time.Time { return clock }
	r.n.slots(100, 100)
	r.round()
	r.n.burn(r.cfg, 0, 101, s4Bob, 300)
	r.n.supply(s4Token, 101, 700)
	r.n.slots(102, 102)
	r.round()
	r.round()
	r.s.state.SolThrough = nil // the live state: the signing cursor alone // s7:new
	if err := r.s.save(); err != nil {
		t.Fatal(err)
	}
	r.s = r.newSigner(r.s.Store) // the deploy restarts it
	r.s.now = func() time.Time { return clock }
	r.n.keepHistory(0, 900)
	h := int64(201)
	for i := 0; i < 40; i++ {
		r.n.slots(1000+uint64(i), 1000+uint64(i))
		clock = clock.Add(4 * time.Second)
		r.g.setHead(h, clock)
		h++
		r.round()
	}
	clock = clock.Add(time.Hour)
	r.round()
	st := r.s.Status()
	t.Logf("signing %v, verified %v, cut %.160q; alarms %v", st["signing"], st["verified"], r.cut(), r.s.Alarm.StandingKeys())
	if st["signing"] == true || st["verified"] == true || !r.alarmed("solana:history:1") || !strings.Contains(r.cut(), "one provider's listing") {
		t.Fatalf("a signer without the history: signing %v, cut %q, alarms %v", st["signing"], r.cut(), r.s.Alarm.StandingKeys())
	}
}

// The relayer, which reads one provider, takes up a burn made after its provider's history passed its cursor (the live
// backup relayer was deaf to every new burn).
func TestS7RelayerQuietRouteOutlivesAShortHistory(t *testing.T) {
	sg := newS4Signer(t, false)
	sg.n.slots(100, 100)
	f := newFake()
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"delivered":0,"live":true,"signers":{"keys":[%q],"threshold":1,"groupA":[],"groupB":[]},"routes":{}}`, hl.DomainGnoTest, strings.Repeat("ab", 32))
	f.renders[testRoute+":json"] = routeRender(0, 50, 0, "")
	r := testRelayer(f, "primary")
	r.Cfg.Routes[0].Token = s4Token.String()
	r.Cfg.Solana.Mailbox = s4SolBox.String()
	r.sol = &sol.Client{RPC: rpc.New(sg.n.urls[0])}
	ctx := context.Background()
	tick := func(slot uint64) {
		sg.n.slots(slot, slot)
		advance(r, 61*time.Second) // the listings' bound is read once a minute
		if err := r.solToGno(ctx); err != nil {
			t.Logf("tick at %d: %v", slot, err)
		}
	}
	bob := sg.n.burn(sg.cfg, 0, 101, s4Bob, 300)
	f.renders[testMailbox+":delivered/"+s4IDHex(bob)] = "150" // delivered: the relayer forgets it
	// a day, in the rig's slots
	for slot := uint64(110); slot <= 11000; slot += 989 {
		tick(slot)
	}
	t.Logf("cursor %v, read through %v", r.state.SolCursor, r.state.SolThrough) // s7:new
	sg.n.keepHistory(0, 9000)
	alice := sg.n.burn(sg.cfg, 1, 11050, s4Alice, 100)
	f.renders[testMailbox+":delivered/"+s4IDHex(alice)] = "0"
	tick(11060)
	pending := 0
	for _, d := range r.state.Pending {
		if d.ID == alice {
			pending++
		}
	}
	t.Logf("pending %d, alarms %v", len(r.state.Pending), r.Alarm.StandingKeys())
	if pending != 1 {
		t.Fatalf("the burn after the history passed the cursor was not taken up: pending %d", len(r.state.Pending))
	}
}

// ------------------------------------------------------------------------------------------- E6-SF2 --

// s7ForgedMint is the round-6 audit's forged-mint case (its relaxed_a6 experiment): a forged mint of 300 burned straight
// back toward gno.land (nonce 0, the burn the forger wants signed), then 300 of the forger's own burned at slot 112; the
// providers at 114. It answers the forger's own burn's transaction.
func s7ForgedMint(n *solNet, cfg *Config) solana.Signature {
	n.supply(s4Token, 101, 1300)
	n.burn(cfg, 0, 102, s4Alice, 300)
	n.supply(s4Token, 102, 1000)
	n.burn(cfg, 1, 112, s4Alice, 300)
	n.supply(s4Token, 112, 700)
	n.slots(114, 114)
	sig, _ := solana.SignatureFromBase58(n.lastSig())
	return sig
}

// E6-SF2: S6's check refused a reading when either provider ignores minContextSlot — and both live monitors pair Alchemy,
// which does on the listing, so after the deploy both would give no verdict for good. A monitor's reading needs one
// provider that honours it now: provider 2 ignoring the bound and listing four slots behind the supplies leaves the
// forger's own burn at 112 one-sided — no verdict, never "holds" over the forged mint — and once it lists it, BROKEN,
// flagged. A signer still needs both (TestS7OneHonouringProviderThatLies…): nothing forged is signed.
func TestS7MonitorOneHonouringProviderNeverHoldsOverTheForgedMint(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.ignoreMinList = [2]bool{false, true}
	s7ForgedMint(r.n, r.cfg)
	r.n.lagListing(1, 4)
	r.poll()
	v1 := r.verdict()
	t.Logf("poll 1 (provider 2 lagging): %s (%.140s); alarms %v", v1, r.cut(), r.m.Alarm.StandingKeys())
	if v1 == "holds" {
		t.Fatalf("one ignoring, lagging provider hid the forger's own burn: holds")
	}
	r.n.lagListing(1, 0)
	r.step(10 * time.Second)
	r.poll()
	t.Logf("poll 2 (caught up): %s; alarms %v", r.verdict(), r.m.Alarm.StandingKeys())
	if r.verdict() != "BROKEN" || !r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("the forged mint not flagged once both providers listed the burn: %s (%s)", r.verdict(), r.cut())
	}
	s := newS4Signer(t, false)
	s.n.ignoreMinList = [2]bool{false, true}
	s7ForgedMint(s.n, s.cfg)
	s.n.lagListing(1, 4)
	s.round()
	s.round()
	t.Logf("signer: signing %v, cut %.120q, the forged burn signed %v", s.s.Status()["signing"], s.cut(), s.signed(0))
	if s.signed(0) {
		t.Fatalf("the forged burn was signed")
	}
	h := newS4Monitor(t, false) // one honouring, one ignoring, nothing in flight: judged at once
	h.n.ignoreMinList = [2]bool{false, true}
	h.n.slots(100, 100)
	h.poll()
	if h.verdict() != "holds" || !h.alarmed("solana:min-context-ignored:2") {
		t.Fatalf("one honouring provider, nothing in flight: %s, alarms %v", h.verdict(), h.m.Alarm.StandingKeys())
	}
}

// What one honouring provider does not cover, measured: if it is the liar — its listing leaves out the forger's own burn
// at 112 — while its ignoring partner lags four slots, no index holds that burn for the readings the partner lags, and the
// reading is short by it: the forged mint reads as holding. On a monitor that is one reading (the next has the burn on the
// partner's word alone: no verdict, as a lying provider can always make it); on a signer it is the forged burn signed —
// so a signer still needs both providers to honour the bound (no live signer pairs Alchemy).
func TestS7OneHonouringProviderThatLiesHidesABurnForAReading(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.ignoreMinList = [2]bool{false, true}
	own := s7ForgedMint(r.n, r.cfg)
	r.n.hide[0] = map[solana.Signature]bool{own: true}
	r.n.lagListing(1, 4)
	r.poll()
	v1 := r.verdict()
	r.n.lagListing(1, 0)
	r.step(10 * time.Second)
	r.poll()
	v2 := r.verdict()
	t.Logf("monitor: poll 1 %s, poll 2 %s (%.120s); flags %v", v1, v2, r.cut(), r.alarmed("identity:BUBBLE:flag"))
	if v2 != "no verdict" || r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("the monitor once the partner lists the hidden burn: %s", v2)
	}
	if v1 != "holds" {
		t.Logf("(the residual this test documents is gone: poll 1 reads %s)", v1)
	}
	signer := func(asIfOneSuffices bool) *s4Signer {
		s := newS4Signer(t, false)
		s.n.ignoreMinList = [2]bool{false, true}
		own := s7ForgedMint(s.n, s.cfg)
		s.n.hide[0] = map[solana.Signature]bool{own: true}
		s.n.lagListing(1, 4)
		if asIfOneSuffices { // the check as a monitor's: provider 2 taken with provider 1's honour
			s.s.minCtx.state = [2]int{minContextHonours, minContextHonours}
			s.s.minCtx.next = s4ClockZero.Add(24 * time.Hour)
		}
		s.round()
		s.round()
		return s
	}
	strict, loose := signer(false), signer(true)
	t.Logf("signer needing both: forged burn signed %v (%.100q); a signer taking one: signed %v", strict.signed(0), strict.cut(), loose.signed(0))
	if strict.signed(0) {
		t.Fatalf("the signer needing both providers signed the forged burn")
	}
	if !loose.signed(0) {
		t.Fatalf("the measurement behind keeping the signers strict no longer holds: a signer taking one honouring provider did not sign the forged burn")
	}
}

// ------------------------------------------------------------------------------------------- E6-SF4 --

// s7CountingGno counts a fake node's renders by path (the relayer reads every gno render through it).
type s7CountingGno struct {
	*fakeChain
	mu    sync.Mutex
	reads map[string]int
}

func (g *s7CountingGno) RenderAt(ctx context.Context, pkg, path string, h int64) (string, error) {
	g.mu.Lock()
	g.reads[path]++
	g.mu.Unlock()
	return g.fakeChain.RenderAt(ctx, pkg, path, h)
}

func (g *s7CountingGno) count(path string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := g.reads[path]
	g.reads[path] = 0
	return n
}

// s7DustRig is a relayer over a signer's store: a dust burn of 3 (below the fee of 10) and an honest one of 50.
func s7DustRig(t *testing.T) (*Relayer, *fakeChain, *s7CountingGno, [32]byte, [32]byte) {
	sg := newS4Signer(t, false)
	sg.n.slots(100, 100)
	sg.round()
	dust := sg.n.burn(sg.cfg, 0, 101, s4Alice, 3)
	honest := sg.n.burn(sg.cfg, 1, 102, s4Bob, 50)
	sg.n.supply(s4Token, 102, 1000-53)
	sg.n.slots(103, 103)
	sg.round()
	sg.round()
	if !sg.signed(0) || !sg.signed(1) {
		t.Fatalf("the signers did not sign both burns: %v", sg.s.Status())
	}
	signer := httptest.NewServer(sg.s.Handler())
	t.Cleanup(signer.Close)
	key := hex.EncodeToString(sg.s.Key.EdPublic())
	f := newFake()
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"delivered":0,"live":true,"signers":{"keys":[%q],"threshold":1,"groupA":[],"groupB":[]},"routes":{}}`, hl.DomainGnoTest, key)
	f.renders[testRoute+":json"] = routeRender(0, 50, 0, "") // fee 10
	for _, id := range [][32]byte{dust, honest} {
		f.renders[testMailbox+":delivered/"+s4IDHex(id)] = "0"
	}
	r := testRelayer(f, "primary")
	g := &s7CountingGno{fakeChain: f, reads: map[string]int{}}
	r.gno = g
	r.Cfg.Routes[0].Token = s4Token.String()
	r.Cfg.Solana.Mailbox = s4SolBox.String()
	r.Cfg.Signers = []SignerRef{{URL: signer.URL, Key: key}}
	r.Cfg.AllowHTTP = true
	r.sol = &sol.Client{RPC: rpc.New(sg.n.urls[0])}
	r.http = &http.Client{Timeout: 5 * time.Second}
	f.onCall = func(pkg, fn string, args []string) { // a Process delivers: its mark set, the count raised
		if fn == "Process" && len(args) == 2 {
			raw, _ := hex.DecodeString(args[1])
			f.renders[testMailbox+":delivered/"+s4IDHex(hl.ID(raw))] = "150"
			f.renders[testMailbox+":json"] = strings.Replace(f.renders[testMailbox+":json"], `"delivered":0`, `"delivered":1`, 1)
		}
	}
	return r, f, g, dust, honest
}

// E6-SF4 (I3's review, finding 4, on the Solana relayer): a burn left below the fee that someone else Processes at his
// own cost — the design's own way out — was never read again while the fee stood: pending and left for good. It is
// dropped once a credit moves hlmailbox's delivered count (a mark is set only with its rise).
func TestS7RelayerDropsDustSomeoneDelivered(t *testing.T) {
	r, f, _, dust, _ := s7DustRig(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := r.solToGno(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, left := r.state.Left[s4IDHex(dust)]; !left || len(r.state.Pending) != 1 {
		t.Fatalf("the dust is not left pending: pending %d, left %v", len(r.state.Pending), r.state.Left)
	}
	f.mu.Lock() // a stranger Processes the dust: its mark set, the count 2
	f.renders[testMailbox+":delivered/"+s4IDHex(dust)] = "160"
	f.renders[testMailbox+":json"] = strings.Replace(f.renders[testMailbox+":json"], `"delivered":1`, `"delivered":2`, 1)
	f.mu.Unlock()
	for i := 0; i < 2; i++ {
		if err := r.solToGno(ctx); err != nil {
			t.Fatal(err)
		}
	}
	_, left := r.state.Left[s4IDHex(dust)]
	t.Logf("delivered by someone else: pending %d, left %v; the relayer's Process calls %d", len(r.state.Pending), left, len(f.callsOf(testRoute+".Process")))
	if len(r.state.Pending) != 0 || left {
		t.Fatalf("dust someone else delivered stays pending (%d) and left (%v)", len(r.state.Pending), left)
	}
	if n := len(f.callsOf(testRoute + ".Process")); n != 1 {
		t.Fatalf("the relayer Processed %d burns (the honest one alone is its)", n)
	}
}

// E6-SF4: while a migration's successor imports, a burn below its fee waiting for the import was not judged as dust at
// all, so every tick read each one's mark (a stranger's pile against the node, and the bound on left dust not counting
// it). It is judged as dust as any other: left, alarmed once, its mark read once after it is left.
func TestS7RelayerJudgesDustDuringAnImport(t *testing.T) {
	r, f, g, dust, _ := s7DustRig(t)
	f.renders[testRoute+":json"] = strings.Replace(routeRender(0, 50, 0, ""), `"migratedTo":""`, `"migratedTo":"gno.land/r/test/hlbubble2"`, 1)
	f.renders["gno.land/r/test/hlbubble2:json"] = `{"live":true,"symbol":"BUBBLE","lock":1000,"importing":true,"imported":1,"fee":10,"migratedTo":""}`
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := r.solToGno(ctx); err != nil {
			t.Fatal(err)
		}
	}
	_, left := r.state.Left[s4IDHex(dust)]
	reads := g.count("delivered/" + s4IDHex(dust))
	note := r.last["process:"+s4IDHex(dust)]
	t.Logf("three ticks while hlbubble2 imports: the dust's mark read %d times, left %v; note %q", reads, left, note)
	if !left || reads > 1 {
		t.Fatalf("dust waiting for an import is not judged as dust: left %v, its mark read %d times in three ticks", left, reads)
	}
	if strings.Contains(note, "waits:") {
		t.Fatalf("the dust is noted as waiting for the import: %q", note)
	}
}

// ------------------------------------------------------------------------------------------- E6-SF6 --

// E6-SF6 (the audit's TestA6MonitorDustAgeAtTheFeeDrop, and I3's second half): a burn below the fee waits for nobody;
// once a decision lowers the fee past it, its wait counts from the last poll that found it dust — the age alarm stood at
// once over a burn indexed three hours before, before any relayer could deliver it — and stands two hours after.
func TestS7MonitorDustAgeAtTheFeeDrop(t *testing.T) {
	r := newS4Monitor(t, false)
	route := func(fee int64) string {
		return fmt.Sprintf(`{"address":"g1route","live":true,"symbol":"BUBBLE","lock":1000,"sentTotal":1000,"owed":0,"voided":0,"lost":0,"fee":%d,"migratedTo":""}`, fee)
	}
	r.g.set(s4Route+":json", route(10))
	r.n.slots(100, 100)
	r.poll()
	r.n.burn(r.cfg, 0, 101, s4Alice, 3) // dust under the fee of 10
	r.n.supply(s4Token, 101, 997)
	r.m.state.LastDelivery = r.clock.Unix()
	for i := 0; i < 18; i++ { // three hours
		r.n.slots(102+uint64(i), 102+uint64(i))
		r.step(10 * time.Minute)
		r.g.setHead(200+int64(i), r.clock)
		r.m.state.LastDelivery = r.clock.Unix() // other burns delivered meanwhile: no stall
		r.poll()
	}
	if r.alarmed("age") {
		t.Fatalf("dust raised the age alarm: %v", r.m.Alarm.StandingKeys())
	}
	r.g.set(s4Route+":json", route(2)) // a decision lowers the fee: the burn of 3 is a delivery now
	r.n.slots(130, 130)
	r.step(10 * time.Second)
	r.g.setHead(230, r.clock)
	r.poll()
	t.Logf("the poll right after the fee fell: age alarmed %v; standing %v", r.alarmed("age"), r.m.Alarm.StandingKeys())
	if r.alarmed("age") {
		t.Fatalf("the fee decrease raised the age alarm at once over a burn indexed 3 h ago, before any relayer could deliver it")
	}
	for i := 0; i < 13; i++ { // two hours and ten minutes, still undelivered
		r.n.slots(131+uint64(i), 131+uint64(i))
		r.step(10 * time.Minute)
		r.g.setHead(231+int64(i), r.clock)
		r.m.state.LastDelivery = r.clock.Unix()
		r.poll()
	}
	if !r.alarmed("age") {
		t.Fatalf("a delivery two hours waiting since the fee fell raised no age alarm: %v", r.m.Alarm.StandingKeys())
	}
}

// ------------------------------------------------------------------------------------------- E6-GT1 --

// s7Forger lands one forged credit of 100 (two signer keys' Process of a message with no burn) at each poll's head
// height, right after the poll reads the route — inside the cut's window — so every cut moves: owed up by 100, a claim to
// alice, hlmailbox's delivered count and done tree. It answers how many it landed.
type s7Forger struct {
	g       *s4Gno
	mu      sync.Mutex
	n       int
	height  int64
	armed   bool
	between bool // land it before the poll instead (the cut holds, the book's window stays open)
}

func (f *s7Forger) land() {
	f.n++
	id := fmt.Sprintf("%064x", 0xf0000+f.n)
	addr, _ := hl.Bytes32ToGnoAddress(s4Alice)
	f.g.renders[s4Route+":json"] = s4RouteJSON(1000, int64(100*f.n), 0, 0, "")
	f.g.renders[s4Mailbox+":json"] = s4MailboxJSON(0, f.n)
	f.g.renders[s4Mailbox+":delivered/"+id] = fmt.Sprint(f.height)
	f.g.renders[s4Route+":claim/"+id] = fmt.Sprintf(`{"id":%q,"to":%q,"amount":100,"paid":0,"state":"open","by":"verifier","height":%d}`, id, addr, f.height)
}

func (f *s7Forger) hook(pkg, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.armed && !f.between && pkg == s4Route && path == "json" {
		f.armed = false
		f.g.mu.Lock()
		f.land()
		f.g.mu.Unlock()
	}
}

// s7ForgerPolls runs a monitor's polls 10 s apart under a forger's credit a poll and says each poll's verdict.
func s7ForgerPolls(t *testing.T, r *s4Monitor, f *s7Forger, polls int) []string {
	var out []string
	for i := 1; i <= polls; i++ {
		r.n.slots(100+25*uint64(i), 100+25*uint64(i)) // Solana's clock: s4ClockZero + 10 s × i
		r.step(10 * time.Second)
		h := int64(200 + i)
		r.g.setHead(h, s4ClockZero.Add(time.Duration(10*i+5)*time.Second))
		f.mu.Lock()
		f.height, f.armed = h, true
		if f.between {
			f.g.mu.Lock()
			f.land()
			f.g.mu.Unlock()
		}
		f.mu.Unlock()
		r.poll()
		out = append(out, r.verdict())
		if r.alarmed("identity:BUBBLE:flag") {
			break
		}
	}
	return out
}

// E6-GT1's Solana sibling: a forger with two signer keys lands one forged Process a gno block. Measured (TestS7Cut…), the
// Solana monitors' cut window — hlmailbox's delivered count read before the routes and again after the marks — holds the
// two routes' listings, ≈2.5–3.2 s each on api.mainnet-beta (the live monitor 1's, signers 1 and 3's), so it spans a
// block; a forged credit landing inside every window made every poll "no verdict", nothing after the cut ran, nothing was
// flagged. A move of the delivered count alone makes a reading short, never over, and a credit that may be a burn above
// the snapshot's slot puts it over by its claim at most: a token over by more is broken whatever landed — flagged.
func TestS7ForgerKeepsEveryMonitorCutMoving(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	f := &s7Forger{g: r.g}
	r.g.hook(f.hook)
	out := s7ForgerPolls(t, r, f, 12)
	t.Logf("%d forged credits; polls %v; flagged %v; alarms %v", f.n, out, r.alarmed("identity:BUBBLE:flag"), r.m.Alarm.StandingKeys())
	if !r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("a forged credit inside every cut's window: %d polls, none flagged (%v)", len(out), out)
	}
}

// The same forger landing his credit a block before each poll instead: every cut holds, but the book of credits keeps a
// window open — the newest forged credit is within the margin of the snapshot — so every poll was "no verdict" too.
func TestS7ForgerKeepsTheMonitorsBookOpen(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	f := &s7Forger{g: r.g, between: true}
	out := s7ForgerPolls(t, r, f, 12)
	t.Logf("%d forged credits; polls %v; flagged %v; cut %.160q", f.n, out, r.alarmed("identity:BUBBLE:flag"), r.cut())
	if !r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("a forged credit a block before every poll: %d polls, none flagged (%v)", len(out), out)
	}
}

// An honest credit landing inside the window — a burn above the snapshot's slot, its credit read in owed while its supply
// is not burned at the snapshot — is a moving cut and no flag: the bound is its claim.
func TestS7HonestCreditInsideTheWindowIsNoFlag(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	bob := r.n.burn(r.cfg, 0, 112, s4Bob, 300) // above the snapshot's slot (110): the providers trail
	r.n.supply(s4Token, 112, 700)
	r.n.slots(110, 110)
	r.step(10 * time.Second)
	r.g.setHead(201, s4ClockZero.Add(6*time.Second))
	var once sync.Once
	r.g.hook(func(pkg, path string) {
		if pkg == s4Mailbox && path == "json" {
			once.Do(func() { // credited right after the poll's first read of hlmailbox: in owed, and the count moved
				r.g.mu.Lock()
				r.g.renders[s4Route+":json"] = s4RouteJSON(1000, 300, 0, 0, "")
				r.g.renders[s4Mailbox+":json"] = s4MailboxJSON(0, 1)
				r.g.renders[s4Mailbox+":delivered/"+s4IDHex(bob)] = "201"
				r.g.renders[s4Route+":claim/"+s4IDHex(bob)] = s4Claim(bob, s4Bob, 300, 201)
				r.g.mu.Unlock()
			})
		}
	})
	r.poll()
	t.Logf("verdict %s; cut %.140q; alarms %v", r.verdict(), r.cut(), r.m.Alarm.StandingKeys())
	if r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("an honest credit of a burn above the snapshot's slot, landing inside the window, was flagged")
	}
}

// A credited burn whose delivered mark a backend a block behind reads unset counts in flight and owed alike: BROKEN by its
// amount, its credit one the index cannot place — no verdict, healed by the next reading's read of every mark (the review
// of lane s6, F1; the audit's TestA6…StaleMark…). The judgement past the cut must not flag it: burns in flight are left
// out of its measure.
func TestS7StaleMarkPastTheCutIsNoFlag(t *testing.T) {
	stale := func(g *s4Gno, bob [32]byte) {
		var once sync.Once
		g.hook(func(_, path string) {
			if path == "delivered/"+s4IDHex(bob) {
				once.Do(func() { g.set(s4Mailbox+":delivered/"+s4IDHex(bob), "201") }) // this read came from behind
			}
		})
	}
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	bob := r.n.burn(r.cfg, 0, 101, s4Bob, 300)
	r.n.supply(s4Token, 101, 700)
	r.n.slots(102, 102)
	r.step(10 * time.Second)
	r.g.setHead(200, s4ClockZero.Add(10*time.Second))
	r.poll()
	r.g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, "")) // credited at 201: owed 300, the count 1 …
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
	r.g.set(s4Route+":claim/"+s4IDHex(bob), s4Claim(bob, s4Bob, 300, 201))
	stale(r.g, bob) // … its mark read unset once
	var out []string
	for i := 0; i < 4; i++ {
		r.n.slots(110+uint64(25*i), 110+uint64(25*i))
		r.step(10 * time.Second)
		r.g.setHead(201+int64(i), s4ClockZero.Add(time.Duration(20+10*i)*time.Second))
		r.poll()
		out = append(out, r.verdict())
	}
	t.Logf("monitor: %v; flagged %v", out, r.alarmed("identity:BUBBLE:flag"))
	if r.alarmed("identity:BUBBLE:flag") || strings.Contains(strings.Join(out, " "), "BROKEN") {
		t.Fatalf("a stale mark past the cut was read as a break: %v", out)
	}
	sg := newS4Signer(t, false)
	clock := s4ClockZero
	sg.s.now = func() time.Time { return clock }
	sg.n.slots(100, 100)
	sg.g.setHead(200, s4ClockZero.Add(5*time.Second))
	sg.round()
	sbob := sg.n.burn(sg.cfg, 0, 101, s4Bob, 300)
	sg.n.supply(s4Token, 101, 700)
	sg.n.slots(102, 102)
	sg.round()
	sg.round()
	sg.g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, ""))
	sg.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
	stale(sg.g, sbob)
	for i := 0; i < 4; i++ {
		sg.n.slots(110+uint64(10*i), 110+uint64(10*i))
		clock = clock.Add(4 * time.Second)
		sg.g.setHead(201+int64(i), s4ClockZero.Add(time.Duration(20+4*i)*time.Second))
		sg.round()
		if h := fmt.Sprint(sg.s.Status()["halted"]); h != "" {
			t.Fatalf("round %d: a stale mark past the cut halted the signer: %s", i+1, h)
		}
	}
	t.Logf("signer: never halted; cut %.100q", sg.cut())
}

// The signer: the same forger kept every round at no verdict (it signs nothing then, and halts at the hour); it halts at
// once on the break the moving count cannot explain.
func TestS7ForgerKeepsEverySignerCutMoving(t *testing.T) {
	r := newS4Signer(t, false)
	clock := s4ClockZero
	r.s.now = func() time.Time { return clock }
	r.n.slots(100, 100)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.round()
	f := &s7Forger{g: r.g}
	r.g.hook(f.hook)
	var out []string
	for i := 1; i <= 12; i++ {
		r.n.slots(100+25*uint64(i), 100+25*uint64(i))
		clock = clock.Add(10 * time.Second)
		h := int64(200 + i)
		r.g.setHead(h, s4ClockZero.Add(time.Duration(10*i+5)*time.Second))
		f.mu.Lock()
		f.height, f.armed = h, true
		f.mu.Unlock()
		r.round()
		out = append(out, fmt.Sprintf("%.40s", r.s.Status()["halted"]))
		if r.s.Status()["halted"] != "" {
			break
		}
	}
	t.Logf("%d forged credits; halted %v", f.n, out)
	if !strings.Contains(fmt.Sprint(r.s.Status()["halted"]), "broken") {
		t.Fatalf("a forged credit inside every round's window: the signer never halted over the break (%v)", out)
	}
}

// What runs while no cut holds (E6-GT1's Solana sibling, the fix's other halves): the rules past the cut — here the stall
// alarm over a burn waiting with no delivery for half an hour — and, past an hour, `cut` at flag level.
func TestS7MovingCutsStillRunTheOtherRules(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	r.n.burn(r.cfg, 0, 101, s4Bob, 300) // waiting for its credit
	r.n.supply(s4Token, 101, 700)
	n := 0
	r.g.hook(func(pkg, path string) { // an honest credit of somebody's burn elsewhere moves every cut (the count alone)
		if pkg == s4Route && path == "json" {
			r.g.mu.Lock()
			n++
			r.g.renders[s4Mailbox+":json"] = s4MailboxJSON(0, n)
			r.g.mu.Unlock()
		}
	})
	for i := 1; i <= 400; i++ { // an hour and six minutes, every cut moving
		r.n.slots(100+25*uint64(i), 100+25*uint64(i))
		r.step(10 * time.Second)
		r.g.setHead(int64(200+i), r.clock)
		r.poll()
	}
	t.Logf("cut %.120q; alarms %v", r.cut(), r.m.Alarm.StandingKeys())
	if !r.alarmed("stall") || !r.alarmed("cut:hour") {
		t.Fatalf("an hour of moving cuts: stall %v, cut at flag level %v (%v)", r.alarmed("stall"), r.alarmed("cut:hour"), r.m.Alarm.StandingKeys())
	}
}

// The measure behind E6-GT1's Solana sibling (S7_MEASURE=1; real time): the wall time of a monitor's poll and a signer's
// round between hlmailbox's first read and its read again, the providers answering with the latency measured on mainnet
// from the Mac (2026-10-16, lane s7's latency probes: publicnode ≈0.2–0.65 s a call; api.mainnet-beta ≈0.2–0.3 s an
// account read and 2.5–3.2 s a listing, `until` its route's newest signature included) and the own node ≈30 ms a render.
func TestS7CutWindowMeasured(t *testing.T) {
	if os.Getenv("S7_MEASURE") == "" {
		t.Skip("S7_MEASURE not set")
	}
	window := func(n *solNet, g *s4Gno, run func()) time.Duration {
		var mu sync.Mutex
		var first, last time.Time
		g.hook(func(pkg, path string) {
			time.Sleep(30 * time.Millisecond)
			if pkg == s4Mailbox && path == "json" {
				mu.Lock()
				if first.IsZero() {
					first = time.Now()
				}
				last = time.Now()
				mu.Unlock()
			}
		})
		run()
		g.hook(nil)
		return last.Sub(first)
	}
	for _, c := range []struct {
		name string
		list [2]time.Duration
	}{{"publicnode + publicnode", [2]time.Duration{250 * time.Millisecond, 250 * time.Millisecond}},
		{"api.mainnet-beta + publicnode", [2]time.Duration{2700 * time.Millisecond, 250 * time.Millisecond}}} {
		r := newS4Monitor(t, false)
		r.n.slots(100, 100)
		r.poll()
		r.n.mu.Lock()
		r.n.latency = func(i int, method string) time.Duration {
			if method == "getSignaturesForAddress" {
				return c.list[i]
			}
			return 250 * time.Millisecond
		}
		r.n.mu.Unlock()
		r.n.slots(110, 110)
		r.step(10 * time.Second)
		mon := window(r.n, r.g, r.poll)
		s := newS4Signer(t, false)
		s.n.slots(100, 100)
		s.round()
		s.n.mu.Lock()
		s.n.latency = r.n.latency
		s.n.mu.Unlock()
		s.n.slots(110, 110)
		sig := window(s.n, s.g, s.round)
		t.Logf("%s, one route: the monitor's window %s, the signer's %s (a gno block ≈3.4 s; the live routes are two)", c.name, mon.Round(10*time.Millisecond), sig.Round(10*time.Millisecond))
	}
}

// E6-SF2's other half: the forged-credit rule flags a credit whose burn is in NEITHER provider's index — so it still needs
// both providers to honour minContextSlot. With one ignoring the bound and lagging past the credits' margin, a real burn
// the honouring provider hides is in no index, and its honest credit read as forged: credits, payouts and the pausable ISM
// flagged on one liar's word. The rule waits while a provider ignores the bound (the identities still judge).
func TestS7ForgedCreditRuleNeedsBothProviders(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.ignoreMinList = [2]bool{false, true}
	r.n.slots(100, 100)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	bob := r.n.burn(r.cfg, 0, 101, s4Bob, 300)
	r.n.supply(s4Token, 101, 700)
	sig, _ := solana.SignatureFromBase58(r.n.lastSig())
	r.n.hide[0] = map[solana.Signature]bool{sig: true} // provider 1 honours the bound and lies: it hides bob's burn
	r.n.lagListing(1, 100)                             // provider 2 ignores it and lists 100 slots (40 s) behind
	s5Credit(r.g, bob, s4Bob, 300, 300, 201, 1)        // bob's honest credit at 201
	for i := 1; i <= 6; i++ {
		r.n.slots(150+uint64(i), 150+uint64(i)) // Solana's clock 20 s past the credit's block: placed by time
		r.step(10 * time.Second)
		r.g.setHead(201+int64(i), s4ClockZero.Add(time.Duration(5+i)*time.Second))
		r.poll()
	}
	t.Logf("verdict %s; alarms %v", r.verdict(), r.m.Alarm.StandingKeys())
	for k := range r.m.Alarm.Standing() {
		if strings.HasPrefix(k, "credit:unindexed:") && strings.HasSuffix(k, ":flag") {
			t.Fatalf("bob's honest credit read as forged on one lying provider's word: %v", r.m.Alarm.StandingKeys())
		}
	}
}

// ------------------------------------------------------------------- the review of lane s7 (R1–R3, R5) --

// s7PinSupply makes both providers answer the token's mint (the snapshot's call) from slot s while their listings answer
// at their finalized slot: a provider whose account backend trails its listing backend (the review's zzPinSupply).
func s7PinSupply(n *solNet, s uint64) {
	mint := sol.Synthetic{Program: s4Token}.Mint()
	n.pinAt(0, mint, s)
	n.pinAt(1, mint, s)
}

// The review's R1 (one node, no stale read): bob's honest burn at 112 is indexed while the snapshot reads at 110, its
// mark read unset at count 0; his credit lands right after the next poll's first read of hlmailbox — owed counts it, the
// count moved, his mark is not read again (the count read first is still 0). Above the slot he is neither in flight nor
// credited above it, and indexed he was in no bound: flagged, the signer halted. Every burn the index holds above its
// slot is in the bound now.
func TestS7IndexedBurnAboveTheSlotCreditedInTheWindow(t *testing.T) {
	credit := func(g *s4Gno, bob [32]byte) {
		var once sync.Once
		g.hook(func(pkg, path string) {
			if pkg == s4Mailbox && path == "json" {
				once.Do(func() {
					g.mu.Lock()
					g.renders[s4Route+":json"] = s4RouteJSON(1000, 300, 0, 0, "")
					g.renders[s4Mailbox+":json"] = s4MailboxJSON(0, 1)
					g.renders[s4Mailbox+":delivered/"+s4IDHex(bob)] = "202"
					g.renders[s4Route+":claim/"+s4IDHex(bob)] = s4Claim(bob, s4Bob, 300, 202)
					g.mu.Unlock()
				})
			}
		})
	}
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	bob := r.n.burn(r.cfg, 0, 112, s4Bob, 300)
	r.n.supply(s4Token, 112, 700)
	r.n.slots(115, 115)
	s7PinSupply(r.n, 110)
	r.step(10 * time.Second)
	r.g.setHead(201, s4ClockZero.Add(15*time.Second))
	r.poll()
	if r.m.state.Index[s4IDHex(bob)] == nil {
		t.Fatalf("bob's burn not indexed (cut %q)", r.cut())
	}
	r.step(10 * time.Second)
	r.g.setHead(202, s4ClockZero.Add(25*time.Second))
	credit(r.g, bob)
	r.poll()
	r.g.hook(nil)
	t.Logf("monitor: %s; cut %.120q; flagged %v", r.verdict(), r.cut(), r.alarmed("identity:BUBBLE:flag"))
	if r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("an honest credit of an indexed burn above the snapshot's slot, landing in the window, was flagged")
	}
	sg := newS4Signer(t, false)
	clock := s4ClockZero
	sg.s.now = func() time.Time { return clock }
	sg.n.slots(100, 100)
	sg.g.setHead(200, s4ClockZero.Add(5*time.Second))
	sg.round()
	sbob := sg.n.burn(sg.cfg, 0, 112, s4Bob, 300)
	sg.n.supply(s4Token, 112, 700)
	sg.n.slots(115, 115)
	s7PinSupply(sg.n, 110)
	clock = clock.Add(3 * time.Second)
	sg.g.setHead(201, s4ClockZero.Add(8*time.Second))
	sg.round()
	clock = clock.Add(3 * time.Second)
	sg.g.setHead(202, s4ClockZero.Add(11*time.Second))
	credit(sg.g, sbob)
	sg.round()
	sg.g.hook(nil)
	t.Logf("signer: halted %.120q", sg.s.Status()["halted"])
	if h := fmt.Sprint(sg.s.Status()["halted"]); strings.Contains(h, "broken") {
		t.Fatalf("the same honest credit halted the signer: %s", h)
	}
}

// R1's sibling: bob indexed above the slot, credited between polls, his mark read unset once by a backend behind — before
// lane s7 no verdict and heal, then flagged past the cut. No longer.
func TestS7StaleMarkAboveTheSlotIsNoFlag(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	bob := r.n.burn(r.cfg, 0, 112, s4Bob, 300)
	r.n.supply(s4Token, 112, 700)
	r.n.slots(115, 115)
	s7PinSupply(r.n, 110)
	r.step(10 * time.Second)
	r.g.setHead(201, s4ClockZero.Add(15*time.Second))
	r.poll()
	r.g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, ""))
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
	r.g.set(s4Route+":claim/"+s4IDHex(bob), s4Claim(bob, s4Bob, 300, 202))
	var once sync.Once
	r.g.hook(func(_, path string) {
		if path == "delivered/"+s4IDHex(bob) {
			once.Do(func() { r.g.set(s4Mailbox+":delivered/"+s4IDHex(bob), "202") }) // this one read came from behind
		}
	})
	r.step(10 * time.Second)
	r.g.setHead(202, s4ClockZero.Add(25*time.Second))
	r.poll()
	t.Logf("%s; cut %.120q; flagged %v", r.verdict(), r.cut(), r.alarmed("identity:BUBBLE:flag"))
	if r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("a stale mark of an indexed burn above the slot was flagged")
	}
}

// The review's R2: a quiet route on a provider that no longer knows its cursor is listed back every reading; one listing
// that left out a fresh burn (an address index a moment behind its bank, which minContextSlot does not show) moved the
// slot read through past it, and no later listing went back that far: the burn one-sided for good. The slot read through
// stays a margin below the bound now, so the next listing back lists it. (The rig's slots run past the margin.)
func TestS7ListBackHealsABurnOneListingLeftOut(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(10_000, 10_000)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	for i, slot := range []uint64{15_000, 20_000} { // quiet: provider 1 lists the route back to the slot read through
		r.n.slots(slot, slot)
		r.step(10 * time.Second)
		r.g.setHead(201+int64(i), s4ClockZero.Add(time.Duration(15+10*i)*time.Second))
		r.poll()
	}
	dust := r.n.burn(r.cfg, 1, 20_005, s4Bob, 3) // never credited (dust under a fee, say)
	r.n.supply(s4Token, 20_005, 997)
	sig, _ := solana.SignatureFromBase58(r.n.lastSig())
	r.n.mu.Lock()
	r.n.hide[0] = map[solana.Signature]bool{sig: true} // provider 1's one listing that leaves it out
	r.n.mu.Unlock()
	r.n.slots(20_010, 20_010)
	r.step(10 * time.Second)
	r.g.setHead(203, s4ClockZero.Add(35*time.Second))
	r.poll()
	r.n.mu.Lock()
	r.n.hide[0] = nil
	r.n.mu.Unlock()
	for i := 0; i < 4; i++ {
		r.n.slots(20_020+uint64(10*i), 20_020+uint64(10*i))
		r.step(10 * time.Second)
		r.g.setHead(204+int64(i), s4ClockZero.Add(time.Duration(45+10*i)*time.Second))
		r.poll()
	}
	rec := r.m.state.Index[s4IDHex(dust)]
	t.Logf("the burn one listing left out: %+v; through %v; %s", rec, r.m.state.Through, r.verdict()) // s7:new
	if rec == nil || rec.Providers != 3 {
		t.Fatalf("a burn one listing back left out is never listed by that provider again: %+v", rec)
	}
}

// R2 on a signer's restart: provider 1 left a fresh burn out of one round; its restart slot was lowered only below
// burns provider 1 itself listed, so after a restart it never listed that burn again (without one, its next `until` did).
func TestS7SignerRestartRelistsABurnOneProviderLeftOut(t *testing.T) {
	r := newS4Signer(t, false)
	clock := s4ClockZero
	r.s.now = func() time.Time { return clock }
	h := int64(200)
	round := func(slot uint64) {
		r.n.slots(slot, slot)
		clock = clock.Add(4 * time.Second)
		r.g.setHead(h, clock)
		h++
		r.round()
	}
	round(10_000)
	bob := r.n.burn(r.cfg, 0, 10_001, s4Bob, 300) // signed and credited: the signing cursor
	r.n.supply(s4Token, 10_001, 700)
	round(10_002)
	round(10_003)
	r.g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, ""))
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
	r.g.set(s4Mailbox+":delivered/"+s4IDHex(bob), "150")
	round(12_000)
	dust := r.n.burn(r.cfg, 1, 12_005, s4Alice, 3) // never credited
	r.n.supply(s4Token, 12_005, 697)
	sig, _ := solana.SignatureFromBase58(r.n.lastSig())
	r.n.mu.Lock()
	r.n.hide[0] = map[solana.Signature]bool{sig: true}
	r.n.mu.Unlock()
	round(14_000)
	r.n.mu.Lock()
	r.n.hide[0] = nil
	r.n.mu.Unlock()
	if err := r.s.save(); err != nil {
		t.Fatal(err)
	}
	s2 := r.newSigner(r.s.Store)
	s2.now = func() time.Time { return clock }
	t.Logf("restart: the state's restart slots %v", s2.state.SolThrough) // s7:new
	r.s = s2
	for i := 0; i < 3; i++ {
		round(14_010 + uint64(i))
	}
	rec := r.s.index[s4IDHex(dust)]
	prov := 0
	if rec != nil {
		prov = rec.providers
	}
	t.Logf("after the restart: the burn's providers %d; cut %.120q", prov, r.cut())
	if prov != 3 {
		t.Fatalf("the burn provider 1 left out once is never listed by it after the restart (providers %d): %s", prov, r.cut())
	}
}

// The review's R3: a lying provider's newest signature at slot 10^12 became the slot read through, kept in the state
// under its place in the config, so the provider named in its place listed back to it and never listed a burn below it.
// The slot a listing claims is held to the supplies' slot, which the other provider's answer bounds.
func TestS7ALiarsSlotIsHeldToTheSupplies(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(10_000, 10_000)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	lie := solana.Signature{0xee, 7}
	r.n.mu.Lock()
	r.n.invent[0] = []solSigAt{{sig: lie, slot: 1_000_000_000_000}} // provider 1's newest, of its own making
	r.n.mu.Unlock()
	r.n.slots(10_010, 10_010)
	r.step(10 * time.Second)
	r.g.setHead(201, s4ClockZero.Add(15*time.Second))
	r.poll()
	t.Logf("after the liar's listing: through %v", r.m.state.Through) // s7:new
	r.n.mu.Lock()
	r.n.invent[0] = nil // the config names another provider in its place, one with a short history
	r.n.mu.Unlock()
	r.n.keepHistory(0, 5_000)
	bob := r.n.burn(r.cfg, 0, 10_015, s4Bob, 300)
	r.n.supply(s4Token, 10_015, 700)
	for i := 0; i < 3; i++ {
		r.n.slots(10_020+uint64(10*i), 10_020+uint64(10*i))
		r.step(10 * time.Second)
		r.g.setHead(202+int64(i), s4ClockZero.Add(time.Duration(25+10*i)*time.Second))
		r.poll()
	}
	rec := r.m.state.Index[s4IDHex(bob)]
	t.Logf("bob: %+v; %s", rec, r.verdict())
	if rec == nil || rec.Providers != 3 {
		t.Fatalf("the provider named in the liar's place never lists bob: %+v (%s)", rec, r.cut())
	}
}

// The review's R5 (hardening): through every poll a forger's credits kept from holding the status said the last held
// cut's "holds"; an identity judged at an earlier poll is shown stale while no cut holds. And an hour of polls that fail
// whole says so at flag level, as an hour without a held cut does.
func TestS7StatusSaysNoCutHeld(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.g.setHead(200, s4ClockZero.Add(5*time.Second))
	r.poll()
	f := &s7Forger{g: r.g}
	r.g.hook(f.hook)
	s7ForgerPolls(t, r, f, 2) // within the bound: no flag yet, every cut moving
	r.m.box.publish(r.m.buildStatus())
	entry, _ := r.m.Status()["identity"].(map[string]any)["BUBBLE"].(map[string]any)
	t.Logf("BUBBLE on the status: verdict %v, stale %.80v", entry["verdict"], entry["stale"])
	if s, _ := entry["stale"].(string); !strings.Contains(s, "no cut held since") {
		t.Fatalf("the status shows the last held cut's verdict through moving cuts with no word of it: %v", entry)
	}
	m := newS4Monitor(t, false)
	m.m.afterPoll(nil)
	for i := 0; i < 7; i++ {
		m.m.afterPoll(fmt.Errorf("BUBBLE from provider 1: a listing that fails"))
		m.step(10 * time.Minute)
	}
	if !m.alarmed("monitor:no-verdict:hour") {
		t.Fatalf("an hour of failing polls is not said at flag level: %v", m.m.Alarm.StandingKeys())
	}
}

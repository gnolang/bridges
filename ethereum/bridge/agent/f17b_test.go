package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// Fold F17b (round 5): the Go trust path's findings — E5-RT1 (a finality stall or a frozen provider blinds the watch,
// green), E5-GT1 (a forged credit paid out at once waits the finality lag), E5-RT6 (a head from a backend a block behind
// the state flags an honest credit), E5-GT2, E5-GT4 and E5-CF3's items. Each test fails on the parent (04441d95).

// flagLevel says whether a standing alarm was raised at flag level.
func flagLevel(a *Alarmer, key string) bool {
	for _, k := range a.StandingFlags() {
		if k == key {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------------------------------- E5-RT1 --

// A finality stall: blocks keep coming, the finalized block stands. Past half an hour by its own time every
// Ethereum-side rule is blind — said, and the page is not green — and eth:finality-stall alarms; past the hour the
// alarm is at flag level. A one-shot `hlwatch status` with no state reads the same from the block's time alone. Once
// finality resumes the page is green again.
func TestF17bFinalityStallBlindsTheWatch(t *testing.T) {
	r := newMonRig(t)
	r.chain.mine(110, 100) // the stall: the finalized block 100, 13 minutes old, stands
	if _, ok, why := r.view(t); !ok {
		t.Fatalf("not green inside the window: %s", why)
	}
	r.step(20 * time.Minute) // block 100 is 33 minutes old
	_, ok, why := r.view(t)
	t.Logf("33 minutes: green=%v %s", ok, why)
	if ok || !strings.Contains(why, "not read: every Ethereum-side rule") || !r.alarmed("eth:finality-stall") || flagLevel(r.m.Alarm, "eth:finality-stall") {
		t.Fatalf("a finalized block 33 minutes old: green=%v, alarm %v, flag level %v: %s", ok, r.alarmed("eth:finality-stall"), flagLevel(r.m.Alarm, "eth:finality-stall"), why)
	}
	// a one-shot hlwatch status: a fresh process with no state
	m2 := &Monitor{Cfg: r.m.Cfg, Alarm: &Alarmer{Source: "hlwatch"}, ReadOnly: true, pair: r.m.pair, own: r.gno, sources: r.m.sources, now: r.m.now}
	if err := m2.init(); err != nil {
		t.Fatal(err)
	}
	m2.state.From = 1
	v2, err := (&Watch{m: m2}).View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok2, why2 := v2.Green(); ok2 || !strings.Contains(strings.Join(why2, ";"), "every Ethereum-side rule") {
		t.Fatalf("a one-shot status over the stall: green=%v %v", ok2, why2)
	}
	r.step(30 * time.Minute) // 63 minutes old
	v, ok, why := r.view(t)
	t.Logf("63 minutes: green=%v flagged %v", ok, v.Flagged)
	if ok || !flagLevel(r.m.Alarm, "eth:finality-stall") {
		t.Fatalf("past the hour the stall is not a flag-level alarm: %v %s", v.Flagged, why)
	}
	r.chain.mine(400, 340) // finality resumes: block 340 is a quarter of an hour old
	r.step(10 * time.Second)
	if _, ok, why := r.view(t); !ok || r.alarmed("eth:finality-stall") {
		t.Fatalf("finality resumed, the page is not green: %s", why)
	}
}

// One provider's finalized answer stands still (a real old block) while the chain finalizes on: once the two answers
// are more than two epochs apart they are a disagreement — no verdict, eth:providers — never a reading at the lower one
// with the page green.
func TestF17bFrozenProviderIsADisagreement(t *testing.T) {
	r := newMonRig(t)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	var err error
	for fin := uint64(110); fin <= 180; fin += 10 { // ten blocks finalized a two-minute step, provider B frozen at 100
		r.chain.mine(fin+64, fin)
		r.pb.mu.Lock()
		r.pb.lag = fin - 100
		r.pb.mu.Unlock()
		r.step(2 * time.Minute)
		err = r.poll()
	}
	t.Logf("the last poll: %v; alarms %v", err, r.m.Alarm.StandingKeys())
	if err == nil || !r.alarmed("eth:providers") {
		t.Fatalf("provider B 80 blocks behind: no disagreement (%v; %v)", err, r.m.Alarm.StandingKeys())
	}
	r.m.ReadOnly = true
	v, err := (&Watch{m: r.m}).View(context.Background()) // hlwatch status: no verdict, the snapshot refused
	if err == nil {
		ok, why := v.Green()
		t.Fatalf("hlwatch read a frozen provider's block (green %v): %v", ok, why)
	}
	t.Logf("hlwatch: %v", err)
	// the signer: no verdict, retried quietly as a finality jump read seconds apart would be, then eth:providers —
	// never a reading at the frozen block
	s := newSignerRig(t)
	s.round()
	s.chain.mine(250, 180)
	s.pb.mu.Lock()
	s.pb.lag = 80
	s.pb.mu.Unlock()
	s.step(time.Second)
	s.round()
	if st := s.status(); !strings.Contains(st["cut"].(string), "no verdict") || s.alarmed("eth:providers") {
		t.Fatalf("a signer over a provider 80 blocks behind, one round: %v; alarmed %v", st["cut"], s.alarmed("eth:providers"))
	}
	for i := 1; i < movingCutAlarm; i++ {
		s.step(time.Second)
		s.round()
	}
	st := s.status()
	t.Logf("signer: %v %q", st["cut"], s.s.Alarm.Standing()["eth:providers"])
	if !s.alarmed("eth:providers") || !strings.Contains(st["cut"].(string), "no verdict") {
		t.Fatalf("a signer over a provider 80 blocks behind for %d rounds: %v", movingCutAlarm, st["cut"])
	}
}

// The stall's clock is kept in the state: a monitor restarted over its state (or the next `hlwatch status -state`), and a
// restarted signer, do not start it again. The block's own time is made to look current here, so only the clock can
// say it stood.
func TestF17bFinalityClockSurvivesARestart(t *testing.T) {
	r := newMonRig(t)
	r.m.State = filepath.Join(t.TempDir(), "monitor.json")
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.step(65 * time.Minute)
	r.chain.retime(uint64(r.now.Unix()) - 12*100 - 60) // block 100 a minute old by its time, standing for 65 minutes
	m := &Monitor{Cfg: r.m.Cfg, State: r.m.State, Alarm: &Alarmer{Source: "restarted"}, Every: r.m.Every, pair: r.m.pair, own: r.gno, watch: r.gno, sources: r.m.sources, ethWatch: r.snd, now: r.m.now}
	if err := m.init(); err != nil {
		t.Fatal(err)
	}
	r.m = m
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("restarted: %q", r.m.Alarm.Standing()["eth:finality-stall"])
	if !flagLevel(r.m.Alarm, "eth:finality-stall") {
		t.Fatalf("a monitor restarted 65 minutes into a stall started its clock again: %v", r.m.Alarm.StandingKeys())
	}
	// the signer's clock too
	s := newSignerRig(t)
	s.round()
	s.step(65 * time.Minute)
	s2 := &Signer{Cfg: s.s.Cfg, Key: s.s.Key, Store: s.s.Store, Every: s.s.Every, Alarm: &Alarmer{Source: "restarted"}, pair: s.s.pair, gno: s.s.gno, now: s.clock}
	if err := s2.load(); err != nil {
		t.Fatal(err)
	}
	s.s = s2
	s.round()
	if !s.alarmed("eth:finality-stall") {
		t.Fatalf("a signer restarted 65 minutes into a stall started its clock again: %v", s.s.Alarm.StandingKeys())
	}
}

// No finalized block seen yet (a chain at its first blocks answers finalized 0) starts no stall's clock: a fresh block
// numbered 0 is neither blind nor a stall.
func TestF17bNoStallBeforeAFinalizedBlockIsSeen(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	m := &Monitor{Cfg: m3bConfig(), Alarm: &Alarmer{Source: "test"}, now: func() time.Time { return now }}
	if err := m.init(); err != nil {
		t.Fatal(err)
	}
	m.last = &monitorView{}
	m.noteFinality(&ethx.Block{Number: 0, Time: uint64(now.Unix()) - 24})
	if _, ok := m.Alarm.Standing()["eth:finality-stall"]; ok || len(m.last.Blind) > 0 {
		t.Fatalf("a fresh finalized block 0 read as a stall: %v %v", m.Alarm.StandingKeys(), m.last.Blind)
	}
}

// ---------------------------------------------------------------------------------------------------- E5-GT1 --

// gt1Chain is a realistic chain: the head 6 seconds old, the finalized block 95 blocks (19 minutes) behind it.
func gt1Chain(c *m3bChain, now time.Time) {
	c.mine(195, 100)
	c.retime(uint64(now.Unix()) - 6 - 12*195)
}

// A forged credit paid out in its own block (Process, then anyone's Claim) is in no waiting index: the identity reads
// BROKEN by its amount while its delivery is unplaced. Both providers, current, show no burn above the index that could
// carry it, so it is a break at once — flagged at the first poll, hlwatch naming it — not after the finality lag.
func TestF17bForgedCreditPaidAtOnceFlaggedAtOnce(t *testing.T) {
	r := newMonRig(t)
	gt1Chain(r.chain, r.now)
	forged := strings.Repeat("ef", 32)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(700, 0, 0, 0)) // the forged 300 credited and paid: the lock down by 300
	r.gno.set(m3bMailPath, "delivered/"+forged, "61")
	r.gno.set(m3bNS+"hlgnot", "claim/"+forged, `{"id":"`+forged+`","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":300,"paid":300,"state":"done","by":"verifier","height":61}`)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	text := r.m.Alarm.Standing()["identity:GNOT:flag"]
	t.Logf("poll 1: flags %v %v; %s", r.gnoFlags(), r.snd.calls, text)
	if len(r.gnoFlags()) != 2 || r.snd.count() != 1 || !strings.Contains(text, "more than any honest lag") {
		t.Fatalf("a forged credit paid at once was not flagged at the first poll: %v %v %v", r.gnoFlags(), r.snd.calls, r.m.Alarm.StandingKeys())
	}
	// hlwatch over the same chains
	h := newMonRig(t)
	gt1Chain(h.chain, h.now)
	h.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(700, 0, 0, 0))
	h.gno.set(m3bMailPath, "delivered/"+forged, "61")
	v, ok, why := h.view(t)
	if ok || len(v.Broken) != 1 || v.Broken[0] != "GNOT" {
		t.Fatalf("hlwatch over a forged credit paid at once: broken %v, green %v: %s", v.Broken, ok, why)
	}
}

// The bound holds an honest lag: a burn above the index both providers show, credited on gno.land — the reading BROKEN
// by its amount waits, and ends once the index holds it (no flag); a burn whose delivered mark does not read is counted
// as credited (the bound only widens).
func TestF17bHonestLagWithinTheBoundWaits(t *testing.T) {
	for _, unread := range []bool{false, true} {
		r := newMonRig(t)
		gnot := gnoside.RealmKey(m3bNS + "hlgnot")
		gt1Chain(r.chain, r.now)
		id := r.chain.burn(m3bGNOT, gnot, r.alice, 300, 150) // above the finalized block 100
		idh := hex.EncodeToString(id[:])
		r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
		r.gno.set(m3bMailPath, "delivered/"+idh, "59")
		if unread {
			n := 0
			r.gno.renderErr = func(pkg, path string) error {
				if pkg == m3bMailPath && path == "delivered/"+idh {
					if n++; n == 1 { // the cut's mark of the burn above the index
						return context.DeadlineExceeded
					}
				}
				return nil
			}
		}
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		if len(r.gnoFlags()) > 0 || r.m.state.Pending["GNOT"] == nil {
			t.Fatalf("unread %v: an honest lag within the bound was flagged, or did not wait: %v %v", unread, r.gnoFlags(), r.m.state.Pending)
		}
		r.chain.mine(220, 160) // the index reaches the burn
		r.step(10 * time.Second)
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		if len(r.gnoFlags()) > 0 || r.m.state.Pending["GNOT"] != nil {
			t.Fatalf("unread %v: the lag did not end the reading: %v %v", unread, r.gnoFlags(), r.m.state.Pending)
		}
	}
}

// The signer over a forged credit paid at once, its providers current: it halts at once (it signed nothing meanwhile
// either way), not a finality lag later.
func TestF17bSignerHaltsAtOnceOnAPaidForgedCredit(t *testing.T) {
	r := newSignerRig(t)
	gt1Chain(r.chain, r.now)
	r.round()
	forged := strings.Repeat("ef", 32)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(700, 0, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+forged, "61")
	r.step(3 * time.Second)
	r.round()
	st := r.status()
	t.Logf("signer: signing=%v halted=%q", st["signing"], st["halted"])
	if st["signing"] == true || !strings.Contains(st["halted"].(string), "more than any honest lag") {
		t.Fatalf("the signer did not halt at once over a forged credit paid at once: %v", st)
	}
}

// ---------------------------------------------------------------------------------------------------- E5-RT6 --

// rt6Credit sets the PoC's honest credit: a burn at block 120 credited at gno height 61 while one provider trails 15
// blocks (the pair's finalized block 115, the burn above the index), the head answered at 60 when behind (a
// load-balanced endpoint's backend a block behind the one that served the state).
func rt6Credit(c *m3bChain, g *m3bGnoNode, pa *m3bProvider, alice [32]byte, now time.Time, behind bool) {
	c.retime(uint64(now.Unix()) - 12*130)
	c.mine(130, 130)
	id := c.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), alice, 300, 120)
	g.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	g.set(m3bMailPath, "delivered/"+hex.EncodeToString(id[:]), "61")
	pa.mu.Lock()
	pa.lag = 15
	pa.mu.Unlock()
	if behind {
		g.advance(60, now.Add(time.Second))
	}
}

// A head read a block behind the state's (a load-balanced endpoint): the reading's height is raised to the highest
// credit its state shows, so the honest credit at 61 is counted as the lag it is — never flagged, never a halt.
func TestF17bHeadBehindTheStateCountsTheLag(t *testing.T) {
	r := newMonRig(t)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.step(10 * time.Second) // gno head 61
	rt6Credit(r.chain, r.gno, r.pa, r.alice, r.now, true)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	p := r.m.state.Pending["GNOT"]
	if p == nil || p.Height != 61 {
		t.Fatalf("the reading's height is not the state's credit: %+v", p)
	}
	r.pa.mu.Lock()
	r.pa.lag = 0
	r.pa.mu.Unlock()
	for i := 0; i < 6; i++ {
		r.step(5 * time.Minute)
		r.chain.mine(uint64(130+25*(i+1)), uint64(130+25*(i+1)))
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.gnoFlags()) > 0 || r.snd.count() > 0 {
		t.Fatalf("an honest credit flagged behind a head a block behind: %v %v", r.gnoFlags(), r.snd.calls)
	}
	// the signer
	s := newSignerRig(t)
	s.round()
	s.step(time.Second) // gno head 61
	rt6Credit(s.chain, s.gno, s.pa, s.alice, s.now, true)
	s.round()
	s.pa.mu.Lock()
	s.pa.lag = 0
	s.pa.mu.Unlock()
	for i := 0; i < 6; i++ {
		s.step(5 * time.Minute)
		s.chain.mine(uint64(130+25*(i+1)), uint64(130+25*(i+1)))
		s.round()
	}
	if st := s.status(); st["halted"] != "" {
		t.Fatalf("a signer halted on an honest credit behind a head a block behind: %v", st["halted"])
	}
}

// ---------------------------------------------------------------------------------------------------- E5-GT2 --

// No second gno source configured: the sources rule never runs, nor the ahead rule's, the stages' and the trusted
// header's second readings — said, and hlwatch is not green (a configured source down was already).
func TestF17bNoSecondSourceIsNotGreen(t *testing.T) {
	r := newMonRig(t)
	r.chain.mine(110, 110)
	r.m.sources = nil
	_, ok, why := r.view(t)
	t.Logf("no gno.sources: green=%v %s", ok, why)
	if ok || !strings.Contains(why, "no second gno source is configured") {
		t.Fatalf("hlwatch with no second gno source: green=%v %s", ok, why)
	}
}

// GovDAO's list whose status word the reader does not know (here "Active", as another implementation GovDAO voted in
// might render it) is a list not read — the refund proposal NOT ENROLLED ON ETHEREUM on it is not listed, and the page
// is not green — never "no proposals".
func TestF17bGovDAOListUnparsedIsNotGreen(t *testing.T) {
	r := newMonRig(t)
	route := m3bNS + "hlgnot"
	r.chain.mine(110, 110)
	r.daoProposals(map[int64][3]string{5: {route, "eth/hlgnot: refund", "refund 300 GNOT of sends imported from the predecessor"}})
	list, _ := r.gno.renderAt(context.Background(), gnoside.DAOPath, "", 0)
	r.gno.set(gnoside.DAOPath, "", strings.ReplaceAll(list, "Status: ACTIVE", "Status: Active"))
	v, ok, why := r.view(t)
	t.Logf("status word changed: green=%v DAOErr=%q", ok, v.DAOErr)
	if ok || !strings.Contains(v.DAOErr, `status "Active"`) || !strings.Contains(why, "GovDAO's proposals") {
		t.Fatalf("an unparsed GovDAO list read as no proposals: green=%v DAOErr=%q %s", ok, v.DAOErr, why)
	}
}

// ---------------------------------------------------------------------------------------------------- E5-GT4 --

// Item 1: a forged credit's flag-level alarm ends only when every route answered and none holds the claim waiting — not
// at a poll where its own route's read failed and another route answered "no such claim".
func TestF17bUnindexedAlarmNotCuredByAnotherRoute(t *testing.T) {
	r := newMonRig(t)
	gt1Chain(r.chain, r.now)
	forged := strings.Repeat("ef", 32)
	claim := `{"id":"` + forged + `","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":300,"state":"open","by":"verifier","height":61}`
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+forged, "61")
	r.gno.set(m3bNS+"hlgnot", "claims/0", "["+claim+"]")
	r.gno.set(m3bNS+"hlgnot", "claim/"+forged, claim)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	key := "credit:unindexed:" + forged + ":flag"
	if !r.alarmed(key) {
		t.Fatalf("the forged credit was not flagged: %v", r.m.Alarm.StandingKeys())
	}
	r.gno.renderErr = func(pkg, path string) error {
		if pkg == m3bNS+"hlgnot" && path == "claim/"+forged {
			return context.DeadlineExceeded
		}
		return nil
	}
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("hlgnot's claim read fails, hlbubble answers null: %s standing %v", key, r.alarmed(key))
	if !r.alarmed(key) {
		t.Fatal("another route's null cured the forged credit's alarm while its own route did not answer")
	}
	// GovDAO voids it, and the claim reads: cured
	r.gno.renderErr = nil
	voided := strings.Replace(claim, `"open"`, `"forged"`, 1)
	r.gno.set(m3bNS+"hlgnot", "claims/0", "["+voided+"]")
	r.gno.set(m3bNS+"hlgnot", "claim/"+forged, voided)
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if r.alarmed(key) {
		t.Fatalf("the voided claim's alarm stands once every route answered: %v", r.m.Alarm.StandingKeys())
	}
}

// Item 4: a decision waiting for code past the age keeps its alarm through a poll whose code read fails (the providers
// give the target's code differently); it ends once the code is there.
func TestF17bCodeWaitAlarmStandsOverAnUnreadCode(t *testing.T) {
	r := newMonRig(t)
	r.m.ReadOnly = true
	impl := common.HexToAddress("0x000000000000000000000000000000000000d00d")
	b := body(8, 1, kind8Params(m3bGate, impl, common.Hash{7})...)
	r.decide(1, b, 50, b, true)
	r.mu.Lock()
	d := r.pending[1]
	d.DeliveredTime = uint64(r.now.Unix()) - 20*86_400 // matured thirteen days ago
	r.pending[1] = d
	r.mu.Unlock()
	w := &Watch{m: r.m, Supermajority: 66.66}
	if _, err := w.View(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.alarmed("decision:code-wait:1") {
		t.Fatalf("not alarmed: %v", r.m.Alarm.StandingKeys())
	}
	r.pa.codeX = func(a common.Address) hexutil.Bytes { // one provider answers code, the other none: unconfirmed
		if a == impl {
			return hexutil.Bytes{0x60}
		}
		return nil
	}
	r.step(10 * time.Second)
	if _, err := w.View(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.alarmed("decision:code-wait:1") {
		t.Fatal("the code-wait alarm ended over a code that did not read")
	}
	r.pb.codeX = r.pa.codeX // the code is there, both agreeing
	r.step(10 * time.Second)
	if _, err := w.View(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.alarmed("decision:code-wait:1") {
		t.Fatal("the alarm stands once the code is there")
	}
}

// ---------------------------------------------------------------------------------------------------- E5-CF3 --

// Item 4: a checkpointed index's waiting claims, which it cannot judge, are one alarm — how many, why, the first named —
// never one a claim (up to a thousand a poll as a reading's pages reach them); cured claims leave it, and it ends with
// the last.
func TestF17bBeforeBaseClaimsAreOneAlarm(t *testing.T) {
	r := newMonRig(t)
	r.m.state.IndexBase = &indexBase{Nonce: 300, Missed: 300, FromHeight: 70} // the index began past 300 of the messages
	route := m3bNS + "hlgnot"
	var entries []string
	for i := 1; i <= 250; i++ {
		entries = append(entries, fmt.Sprintf(`{"pos":%d,%s`, i, strings.TrimPrefix(i2Claim(fmt.Sprintf("%064x", i), "open", "verifier", 1), "{")))
	}
	r.gno.setWaiting(route, entries)
	r.i2Delivered(250)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, k := range r.m.Alarm.StandingKeys() {
		if strings.HasPrefix(k, "credit:unindexed-before-base") {
			keys = append(keys, k)
		}
	}
	text := r.m.Alarm.Standing()["credit:unindexed-before-base"]
	t.Logf("%d alarm(s): %.300s", len(keys), text)
	if len(keys) != 1 || !strings.Contains(text, "250 waiting claim(s)") || !strings.Contains(text, "and 247 more") || len(r.gnoFlags()) > 0 {
		t.Fatalf("250 claims before the index's base: %d alarms (%v), flags %v", len(keys), keys[:min(len(keys), 3)], r.gnoFlags())
	}
	// GovDAO voids them all: the alarm ends
	for i := range entries {
		entries[i] = strings.Replace(entries[i], `"open"`, `"withheld"`, 1)
	}
	r.gno.setWaiting(route, entries)
	for i := 0; i < 3; i++ {
		r.step(10 * time.Second)
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
	}
	if r.alarmed("credit:unindexed-before-base") {
		t.Fatalf("the alarm outlived every claim's cure: %.200s", r.m.Alarm.Standing()["credit:unindexed-before-base"])
	}
}

// Item 5: the waiting index's reading — its place and its suspects — is in the state: a monitor restarted over its state
// (or the next `hlwatch status -state`) reads on from the place the last reading reached, not from place 1.
func TestF17bWaitingReadingSurvivesARestart(t *testing.T) {
	r := newMonRig(t)
	gt1Chain(r.chain, r.now) // both providers current: a claim no dispatch carries is judged once it is read
	r.m.State = filepath.Join(t.TempDir(), "monitor.json")
	route := m3bNS + "hlgnot"
	var entries, indexed []string
	for i := 1; i <= 2000; i++ {
		id := fmt.Sprintf("%064x", i)
		indexed = append(indexed, id)
		entries = append(entries, fmt.Sprintf(`{"pos":%d,%s`, i, strings.TrimPrefix(i2Claim(id, "open", "verifier", 1), "{")))
	}
	forged := strings.Repeat("ef", 32) // a forged claim past the first thousand places
	entries = append(entries, fmt.Sprintf(`{"pos":2001,%s`, strings.TrimPrefix(i2Claim(forged, "open", "verifier", 5_000_000), "{")))
	r.gno.setWaiting(route, entries)
	r.gno.set(route, "claim/"+forged, i2Claim(forged, "open", "verifier", 5_000_000))
	r.i2Indexed(indexed)
	r.i2Delivered(2001)
	reads := r.i2Reads(route, "waiting/")
	if err := r.poll(); err != nil { // ten pages: places 1–1000
		t.Fatal(err)
	}
	m := &Monitor{Cfg: r.m.Cfg, State: r.m.State, Alarm: &Alarmer{Source: "restarted"}, Every: r.m.Every, pair: r.m.pair, own: r.gno, watch: r.gno, sources: r.m.sources, ethWatch: r.snd, now: r.m.now}
	if err := m.init(); err != nil {
		t.Fatal(err)
	}
	r.m = m
	r.step(10 * time.Second)
	before := len(reads())
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	after := reads()[before:]
	t.Logf("restarted, the pages read: %v", after)
	if len(after) == 0 || after[0] != "waiting/1001" {
		t.Fatalf("a restarted monitor read its waiting index from %v, not from place 1001", after)
	}
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if !r.alarmed("credit:unindexed:" + forged + ":flag") {
		t.Fatalf("the forged claim at place 2001 was never named: %v", r.m.Alarm.StandingKeys())
	}
}

// ------------------------------------------------------------------------------- the review of this fold --

// A lying provider cannot move an honest burn above the index to the other token (review finding 1): it answers its
// finalized block one epoch behind (a real block, within two epochs: no disagreement) and files the token GNOT's Dispatch
// logs above it under BUBBLE's sender topic. The burn's token is its message's sender, which its id binds, so GNOT's
// bound still holds it: the reading waits for the index, no flag, no halt.
func TestF17bLiarCannotMoveABurnToAnotherToken(t *testing.T) {
	setup := func(c *m3bChain, pa *m3bProvider, now time.Time) {
		c.mine(195, 160)
		c.retime(uint64(now.Unix()) - 6 - 12*195)
		pa.mu.Lock()
		pa.lag = 32
		pa.mutate = func(l *types.Log) {
			if l.Address == m3bMailbox && l.BlockNumber > 128 && len(l.Topics) > 1 && l.Topics[1] == ethx.AddressTopic(m3bGNOT) {
				l.Topics[1] = ethx.AddressTopic(m3bBUBBLE)
			}
		}
		pa.mu.Unlock()
	}
	r := newMonRig(t)
	setup(r.chain, r.pa, r.now)
	id := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 300, 150) // honest, final for the signers
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(id[:]), "59")
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("monitor: flags %v %v; pending %v", r.gnoFlags(), r.snd.calls, r.m.state.Pending["GNOT"] != nil)
	if len(r.gnoFlags()) > 0 || r.snd.count() > 0 || r.m.state.Pending["GNOT"] == nil {
		t.Fatalf("an honest credit flagged on one lying provider's topics: %v %v", r.gnoFlags(), r.snd.calls)
	}
	s := newSignerRig(t)
	setup(s.chain, s.pa, s.now)
	id2 := s.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), s.alice, 300, 150)
	s.round()
	s.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	s.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(id2[:]), "61")
	s.step(3 * time.Second)
	s.round()
	if st := s.status(); st["halted"] != "" {
		t.Fatalf("a signer halted on one lying provider's topics: %v", st["halted"])
	}
}

// A standing alarm raised again at flag level is posted to the webhook at once (review finding 3): the finality stall's
// escalation past the hour is the news, not its next hourly repeat.
func TestF17bFlagEscalationIsPostedAtOnce(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var j map[string]any
		_ = json.Unmarshal(b, &j)
		if j["key"] == "eth:finality-stall" {
			mu.Lock()
			posts = append(posts, fmt.Sprint(j["level"]))
			mu.Unlock()
		}
	}))
	defer srv.Close()
	r := newMonRig(t)
	r.m.Alarm.URL = srv.URL
	r.chain.mine(110, 100) // the stall
	r.step(20 * time.Minute)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.step(30 * time.Minute) // past the hour
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := append([]string{}, posts...)
		mu.Unlock()
		if len(got) >= 2 || time.Now().After(deadline) {
			t.Logf("webhook posts for eth:finality-stall: %v", got)
			if len(got) != 2 || got[0] != "alarm" || got[1] != "flag" {
				t.Fatalf("the stall's escalation to flag level was not posted at once: %v", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A claim alarmed as before the index's base while the base did not read leaves that alarm once the base reads and the
// index misses nothing (review finding 7): it is judged then, by the forged-credit rule, and the one alarm ends.
func TestF17bBeforeBaseEntryEndsOnceTheBaseReads(t *testing.T) {
	r := newMonRig(t)
	gt1Chain(r.chain, r.now)
	r.m.state.From = 50 // the base is read at block 49
	r.pa.mu.Lock()
	r.pa.noState = true // block 49's state is not served: where the index begins cannot be read
	r.pa.mu.Unlock()
	route := m3bNS + "hlgnot"
	forged := strings.Repeat("ef", 32)
	r.gno.setWaiting(route, []string{`{"pos":1,` + strings.TrimPrefix(i2Claim(forged, "open", "verifier", 300), "{")})
	r.gno.set(route, "claim/"+forged, i2Claim(forged, "open", "verifier", 300))
	r.i2Delivered(1)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.m.Alarm.Standing()["credit:unindexed-before-base"], forged) {
		t.Fatalf("not alarmed while the base did not read: %v", r.m.Alarm.StandingKeys())
	}
	r.pa.mu.Lock()
	r.pa.noState = false
	r.pa.mu.Unlock()
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("the base read: %v", r.m.Alarm.StandingKeys())
	if r.alarmed("credit:unindexed-before-base") || !r.alarmed("credit:unindexed:"+forged+":flag") {
		t.Fatalf("the base read and misses nothing: the claim is judged, its before-base alarm gone: %v", r.m.Alarm.StandingKeys())
	}
}

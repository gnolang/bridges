package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
	"github.com/gnolang/bridges/ethereum/bridge/repro"
)

// Fold F12 of audit round 3: the Go trust path — the signers, the monitors, hlwatch, the provider pair.

// an append-only pair of storage layouts (forge's storageLayout JSON) for the kind 8s the tests read (E3-CF3)
const (
	appendOnlyOld = `{"storage":[{"label":"_owner","slot":"0","offset":0,"type":"t_address"}],"types":{"t_address":{"numberOfBytes":"20"}}}`
	appendOnlyNew = `{"storage":[{"label":"_owner","slot":"0","offset":0,"type":"t_address"},{"label":"_minter","slot":"1","offset":0,"type":"t_address"}],"types":{"t_address":{"numberOfBytes":"20"}}}`
)

// cutText is the signer's or the monitor's cut line.
func cutText(st map[string]any) string { return fmt.Sprint(st["cut"]) }

// ------------------------------------------------------------------------------------------ E3-GT2, E3-RT3 --

// E3-GT2 (the plan red team's E3-RT3): the read-twice guard held hlmailbox's dispatch count only. A credit landing after
// the signer read the routes and before it read the credited marks was in neither owed nor in flight — the reading
// short by its amount — so a forged mint of that amount, burned back, passed the round, the halt lifted and its burn
// was signed. hlmailbox's delivered count moves with every credit: read again once the marks are read, a move is a
// moving cut (no verdict, quiet), and the break shows the round after; credits landing round after round alarm at the
// fifth, as dispatches do.
func TestF12CreditBetweenReadsIsAMovingCut(t *testing.T) {
	r := newSignerRig(t) // GNOT locked 1,000 on gno.land, supply 1,000 on Ethereum
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	honest := r.chain.burn(m3bGNOT, gnot, r.alice, 300, 80) // nonce 0: alice burns 300; supply 700, 300 in flight
	r.round()
	if st := r.status(); st["signing"] != true {
		t.Fatalf("round 1: %v", st)
	}
	r.chain.mine(110, 110)
	r.chain.setSupply(m3bGNOT, 105, 1000)          // a forged mint of 300 (the client fooled) …
	r.chain.burn(m3bGNOT, gnot, r.alice, 300, 106) // … burned straight back (nonce 1): supply 700
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] == true {
		t.Fatalf("round 2 did not halt on the forged mint: %v", st)
	}
	// round 3: alice's honest credit lands right after the route was read
	var once sync.Once
	r.gno.mu.Lock()
	r.gno.afterRender = func(pkg, path string) {
		if pkg == m3bNS+"hlgnot" && path == "json" {
			once.Do(func() {
				r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0)) // owed 300: alice's claim
				r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(honest[:]), "62")
			})
		}
	}
	r.gno.mu.Unlock()
	r.step(time.Second)
	r.round()
	st := r.status()
	if !strings.Contains(cutText(st), "delivered 1 message(s) while the cut was read") {
		t.Fatalf("round 3 took a verdict over a credit landing between the routes' read and the marks': %v", st)
	}
	if _, err := r.s.readSigned(1); err == nil {
		t.Fatal("the burn of the forged mint (nonce 1) was signed")
	}
	if st["signing"] == true || r.alarmed("signer:no-verdict") {
		t.Fatalf("round 3: signing %v, alarms %v", st["signing"], r.s.Alarm.Standing())
	}
	// round 4: the reads hold still, the break shows
	r.gno.mu.Lock()
	r.gno.afterRender = nil
	r.gno.mu.Unlock()
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] == true || !strings.Contains(fmt.Sprint(st["halted"]), "broken") {
		t.Fatalf("round 4: %v", st)
	}
	if _, err := r.s.readSigned(1); err == nil {
		t.Fatal("the burn of the forged mint was signed")
	}
	// credits landing round after round alarm at the fifth
	n := 0
	r.gno.mu.Lock()
	r.gno.afterRender = func(pkg, path string) {
		if pkg == m3bNS+"hlgnot" && path == "json" {
			n++
			r.gno.set(m3bMailPath, fmt.Sprintf("delivered/%064x", n), "70")
		}
	}
	r.gno.mu.Unlock()
	for i := 1; i <= movingCutAlarm; i++ {
		r.step(time.Second)
		r.round()
		if got := r.alarmed("signer:no-verdict"); got != (i == movingCutAlarm) {
			t.Fatalf("moving round %d: alarmed %v (%s)", i, got, cutText(r.status()))
		}
	}
}

// The same race on the monitor: a credit landing between its routes' read and its marks' read hid a forged mint of
// its amount for the poll. The poll is a moving cut, no verdict; the next one flags the forged mint.
func TestF12MonitorCreditBetweenReads(t *testing.T) {
	r := newMonRig(t) // GNOT locked 1,000, supply 1,000
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	honest := r.chain.burn(m3bGNOT, gnot, r.alice, 300, 80) // alice's honest 300, in flight
	r.chain.mine(110, 110)
	r.chain.setSupply(m3bGNOT, 105, 1000)          // a forged mint of 300 …
	r.chain.burn(m3bGNOT, gnot, r.alice, 300, 106) // … burned back
	var once sync.Once
	r.gno.mu.Lock()
	r.gno.afterRender = func(pkg, path string) {
		if pkg == m3bNS+"hlgnot" && path == "json" {
			once.Do(func() {
				r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
				r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(honest[:]), "59")
			})
		}
	}
	r.gno.mu.Unlock()
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if c := cutText(r.m.Status()); !strings.Contains(c, "delivered 1 message(s) while the cut was read") {
		t.Fatalf("the poll took a verdict over a credit landing mid-read: %s (%v)", c, r.m.Status()["identity"])
	}
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if !r.alarmed("identity:GNOT:flag") || len(r.gnoFlags()) == 0 {
		t.Fatalf("the forged mint was not flagged on the next poll: %v", r.m.Alarm.Standing())
	}
}

// The sibling (E3-RT3: "every counter"): a refund — GovDAO's, on a successor's imported send — applied between the
// routes' read and the sends' refund marks took the send out of flight while the lock read before still held it: the
// reading short by its amount, a forged mint of it passing. A token whose sends can be refunded has its counters read
// again with hlmailbox: a refund (or a migration's page) landing between the reads is a moving cut.
func TestF12RefundBetweenReadsIsAMovingCut(t *testing.T) {
	r := newSignerRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	// a successor route (it imported its predecessor's pages: its sends can be refunded), 1,300 locked: a send of 300
	// waits for Ethereum
	successor := func(lock, refunded int64) string {
		b, _ := json.Marshal(map[string]any{"live": true, "lock": lock, "owed": 0, "voided": 0, "lost": 0, "fee": 5_000_000, "imported": 1, "refunded": refunded})
		return string(b)
	}
	r.gno.set(m3bNS+"hlgnot", "json", successor(1300, 0))
	id := r.pendingSend(300, 55)
	r.chain.view(m3bMailbox, "delivered", func(uint64, []any) []any { return []any{false} })
	r.chain.setSupply(m3bGNOT, 70, 1300)          // a forged mint of 300 …
	r.chain.burn(m3bGNOT, gnot, r.alice, 300, 80) // … burned back (nonce 0): supply 1,000
	// the refund of the waiting send lands right after the route was read
	var once sync.Once
	r.gno.mu.Lock()
	r.gno.afterRender = func(pkg, path string) {
		if pkg == m3bNS+"hlgnot" && path == "json" {
			once.Do(func() {
				r.gno.set(m3bNS+"hlgnot", "json", successor(1000, 1))
				r.gno.set(m3bNS+"hlgnot", "send/"+hex.EncodeToString(id[:]), `{"id":"`+hex.EncodeToString(id[:])+`","from":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":300,"at":1,"imported":true,"refunded":1800000100,"writtenOff":0}`)
			})
		}
	}
	r.gno.mu.Unlock()
	r.round()
	st := r.status()
	if !strings.Contains(cutText(st), "GNOT's counters moved while the cut was read") {
		t.Fatalf("the round took a verdict over a refund landing mid-read: %v", st)
	}
	if _, err := r.s.readSigned(0); err == nil {
		t.Fatal("the burn of the forged mint was signed")
	}
	r.gno.mu.Lock()
	r.gno.afterRender = nil
	r.gno.mu.Unlock()
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] == true || !strings.Contains(fmt.Sprint(st["halted"]), "broken") {
		t.Fatalf("the next round: %v", st)
	}
	if _, err := r.s.readSigned(0); err == nil {
		t.Fatal("the burn of the forged mint was signed")
	}
}

// ---------------------------------------------------------------------------------------------- E3-GT9 --

// floorRig is the step-back the audit ran (go-trust-poc/zz_audit3_stepback_mask_test.go): a send delivered on Ethereum
// at block 108 (its mark kept), a forged mint of 300 at 100 burned back at 101; a round at F=110 halts.
func floorRig(t *testing.T) *signerRig {
	r := newSignerRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1300, 0, 0, 0))
	id := r.pendingSend(300, 55) // locked on gno.land: the lock is 1,300
	r.chain.mine(110, 110)
	r.chain.view(m3bMailbox, "delivered", func(n uint64, in []any) []any { return []any{n >= 108 && in[0].([32]byte) == id} })
	r.chain.setSupply(m3bGNOT, 100, 1300)          // a FORGED mint of 300 at block 100
	r.chain.burn(m3bGNOT, gnot, r.alice, 300, 101) // burned straight back: supply 1,000 from 101
	r.chain.setSupply(m3bGNOT, 108, 1300)          // the honest send delivered at block 108: supply 1,300
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] == true {
		t.Fatalf("the forgery did not stop the signer at F=110: %v", st)
	}
	return r
}

// E3-GT9: `finalized` had no floor. One provider answering an older (real) finalized block — a lagging backend, or a
// liar — gave a round whose supply lacked a mint the kept delivered mark counts: the reading short, the halt lifted,
// the forged burn signed. A round below the highest finalized block already read at has no verdict (quiet, alarmed at
// the fifth in a row), and the floor is in the state: a restart keeps it.
func TestF12FinalizedFloorSigner(t *testing.T) {
	r := floorRig(t)
	r.pa.mu.Lock()
	r.pa.lag = 7 // provider A's finalized steps back to 103
	r.pa.mu.Unlock()
	r.step(time.Second)
	r.round()
	st := r.status()
	if !strings.Contains(cutText(st), "below 110, the highest a round already read at") {
		t.Fatalf("a finalized block below the floor was read: %v", st)
	}
	if _, err := r.s.readSigned(0); err == nil || st["signing"] == true {
		t.Fatalf("the forged burn #0 was signed (signing %v)", st["signing"])
	}
	// a restart keeps the floor
	s2 := &Signer{Cfg: r.s.Cfg, Key: r.s.Key, Store: r.s.Store, Every: time.Second, Alarm: &Alarmer{Source: "test2"}, pair: r.s.pair, gno: r.gno, now: r.clock}
	if err := s2.load(); err != nil {
		t.Fatal(err)
	}
	r.s = s2
	for i := 1; i <= movingCutAlarm; i++ {
		r.step(time.Second)
		r.round()
		st = r.status()
		if !strings.Contains(cutText(st), "below 110") {
			t.Fatalf("after the restart, round %d read below the floor: %v", i, st)
		}
		if got := r.alarmed("eth:finalized-floor"); got != (i == movingCutAlarm) {
			t.Fatalf("round %d below the floor: alarmed %v", i, got)
		}
	}
	if _, err := r.s.readSigned(0); err == nil {
		t.Fatal("the forged burn #0 was signed after the restart")
	}
	// the pair answers at the floor again: the break shows, the alarm clears
	r.pa.mu.Lock()
	r.pa.lag = 0
	r.pa.mu.Unlock()
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] == true || !strings.Contains(fmt.Sprint(st["halted"]), "broken") || r.alarmed("eth:finalized-floor") {
		t.Fatalf("back at 110: %v", st)
	}
}

// The other direction of the step-back (go-trust-poc's zz_audit3_stepback_test.go): a burn credited and pruned read
// against a supply that predates it stood twice — a false halt. Below the floor there is no reading at all.
func TestF12FinalizedFloorNoFalseHalt(t *testing.T) {
	r := newSignerRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.mine(110, 110)
	id := r.chain.burn(m3bGNOT, gnot, r.alice, 300, 105)
	r.step(time.Second)
	r.round() // signs #0
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(id[:]), "62")
	r.step(time.Second)
	r.round() // credited: pruned
	r.pa.mu.Lock()
	r.pa.lag = 10
	r.pa.mu.Unlock()
	r.step(time.Second)
	r.round()
	st := r.status()
	if st["halted"] != "" || r.alarmed("signer:halt") || !strings.Contains(cutText(st), "below 110") {
		t.Fatalf("a step-back below the floor halted the signer: %v", st)
	}
}

// The monitor's side (go-trust-poc's zz_audit3_stepback_gap_test.go): the record rebuilt to F=110 against the client's
// counter at F=100 was a "gap" that flagged the gate and HlAdmin after two minutes. Below the floor: no verdict, the
// floor alarm after two minutes, no flag — and the floor survives a restart.
func TestF12FinalizedFloorMonitor(t *testing.T) {
	r := newMonRig(t)
	r.m.State = filepath.Join(t.TempDir(), "monitor.json")
	r.chain.mine(110, 110)
	r.recordHeader(55, 105, r.nodeHash(55)) // an honest header recorded at block 105
	r.chain.view(m3bClient, "trustedHeight", func(n uint64, _ []any) []any {
		if n >= 105 {
			return []any{int64(55)}
		}
		return []any{int64(50)}
	})
	if err := r.poll(); err != nil {
		t.Fatalf("poll at F=110: %v", err)
	}
	r.pa.mu.Lock()
	r.pa.lag = 10 // provider A answers finalized = 100 from now on
	r.pa.mu.Unlock()
	for i := 0; i < 14; i++ { // 140 s of polls
		r.step(10 * time.Second)
		_ = r.poll()
	}
	if r.snd.count() != 0 || r.alarmed("header:gap") || r.alarmed("header:gap:pending") {
		t.Fatalf("a step-back flagged: eth flag txs %v, alarms %v", r.snd.calls, r.m.Alarm.StandingKeys())
	}
	if !r.alarmed("eth:finalized-floor") || !strings.Contains(cutText(r.m.Status()), "below 110") {
		t.Fatalf("the step-back is not alarmed: %v (%s)", r.m.Alarm.StandingKeys(), cutText(r.m.Status()))
	}
	// a restart over the same state keeps the floor
	m2 := &Monitor{Cfg: r.m.Cfg, State: r.m.State, Alarm: &Alarmer{Source: "test2"}, Every: 10 * time.Second, pair: r.m.pair, own: r.gno, watch: r.gno, sources: r.m.sources, ethWatch: r.snd, now: r.m.now}
	if err := m2.init(); err != nil {
		t.Fatal(err)
	}
	if m2.state.Floor != 110 {
		t.Fatalf("the floor after a restart: %d", m2.state.Floor)
	}
	r.m = m2
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cutText(r.m.Status()), "below 110") || r.snd.count() != 0 {
		t.Fatalf("after the restart: %s", cutText(r.m.Status()))
	}
}

// ---------------------------------------------------------------------------------------------- E3-GT3 --

// E3-GT3: `hlwatch status` printed GREEN over a diverted credit (alice burns 200 to herself, gno.land credits bob):
// the identity holds, the watch raised diverted:<id>:flag, which Green did not read. A flag-level alarm, and a finding's
// alarm, keep the page from green.
func TestF12GreenNotOverADivertedCredit(t *testing.T) {
	r := newMonRig(t)
	r.m.ReadOnly = true
	w := &Watch{m: r.m}
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	id := r.chain.burn(m3bGNOT, gnot, r.alice, 200, 80)
	idh := hex.EncodeToString(id[:])
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 200, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+idh, "59")
	r.gno.set(m3bNS+"hlgnot", "claim/"+idh, `{"id":"`+idh+`","to":"g1us8428u2a5satrlxzagqqa5m6vmuze025anjlj","amount":200,"state":"open","by":"verifier","height":59}`)
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ok, why := v.Green()
	if ok || !strings.Contains(strings.Join(why, ";"), "diverted:"+idh) {
		t.Fatalf("green over a diverted credit: %v %v (alarms %v)", ok, why, r.m.Alarm.StandingKeys())
	}
}

// …and over a claim that does not read after the identity's verdict: the diverted-credit rule did not run for it, so
// the page says so and is not green (fold F15b: the rule is blind for that claim and the poll goes on to the Ethereum
// rules — a failing read no longer ends the poll, E4-RL1's class).
func TestF12GreenNotOverAFailedPoll(t *testing.T) {
	r := newMonRig(t)
	r.m.ReadOnly = true
	w := &Watch{m: r.m}
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	id := r.chain.burn(m3bGNOT, gnot, r.alice, 200, 80)
	idh := hex.EncodeToString(id[:])
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 200, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+idh, "59")
	r.gno.set(m3bNS+"hlgnot", "claim/"+idh, `{"id": not json`)
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ok, why := v.Green()
	if ok || !strings.Contains(strings.Join(why, ";"), "not read: the credited burn "+idh[:12]+"'s claim") {
		t.Fatalf("green over a claim that did not read: %v %v (verdict %q)", ok, why, v.Verdict)
	}
}

// A read-only watch may poll once: an unindexed credit — a claim no burn in the index carries — is named at its first
// sighting, and the page is not green (here credited before the index's finalized block: judged at once, E4-GT1).
func TestF12GreenNotOverAnUnindexedCredit(t *testing.T) {
	r := newMonRig(t)
	r.m.ReadOnly = true
	r.chain.retime(uint64(r.now.Unix())) // the finalized block 100 twenty minutes after the credit
	w := &Watch{m: r.m}
	forged := strings.Repeat("ab", 32)
	claim := `{"id":"` + forged + `","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":200,"state":"open","by":"verifier","height":59}`
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 200, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+forged, "59") // a forged credit is a delivery like any (hlmailbox's done)
	r.gno.set(m3bNS+"hlgnot", "claims/0", "["+claim+"]")
	r.gno.set(m3bNS+"hlgnot", "claim/"+forged, claim)
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok, why := v.Green(); ok || !strings.Contains(strings.Join(why, ";"), "credit:unindexed:"+forged) {
		t.Fatalf("green over an unindexed credit: %v", why)
	}
}

// ---------------------------------------------------------------------------------------------- E3-GT10 --

// E3-GT10: `hlwatch status` without a state file reached no verdict once the deployment was 20,000 blocks old: one poll
// a run, 20,000 blocks a poll. A View polls until its scans reach the finalized block (bounded), and the page is green.
func TestF12StatusCatchesUp(t *testing.T) {
	r := newMonRig(t)
	r.m.ReadOnly = true
	r.chain.mine(50_000, 50_000)
	w := &Watch{m: r.m}
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok, why := v.Green(); !ok {
		t.Fatalf("a quiet bridge 50,000 blocks old is not green: %v", why)
	}
	if c := r.m.state.Index.Cursor; c != 50_000 || r.m.state.Record.Cursor != 50_000 || r.m.state.Stages.Cursor != 50_000 || r.m.state.Decisions.Cursor != 50_000 {
		t.Fatalf("the scans: index %d, record %d, stages %d, decisions %d", c, r.m.state.Record.Cursor, r.m.state.Stages.Cursor, r.m.state.Decisions.Cursor)
	}
}

// …and a View cut short (its catch-up deadline) names what is behind, never green.
func TestF12StatusNamesWhatIsBehind(t *testing.T) {
	r := newMonRig(t)
	r.m.ReadOnly = true
	r.chain.mine(50_000, 50_000)
	w := &Watch{m: r.m, CatchUp: time.Nanosecond}
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ok, why := v.Green()
	if ok || !strings.Contains(strings.Join(why, ";"), "catching up: the burn index is read to block 20000 of the finalized block 50000") {
		t.Fatalf("a reading behind: %v %v", ok, why)
	}
	if page := v.Text("build x"); !strings.Contains(page, "CATCHING UP (the rule waits for it): the burn index is read to block 20000") {
		t.Fatalf("the page does not say what is behind:\n%s", page)
	}
}

// ---------------------------------------------------------------------------------------------- E3-CF3 --

// E3-CF3: hlwatch called a kind 8 "immutable" without reading a storage layout. Without both layouts the reading says
// the layout is NOT CHECKED and is NOT SHOWN IMMUTABLE; a layout that does not read, or differs, is not either.
func TestF12KindEightNeedsTheLayouts(t *testing.T) {
	ctx := context.Background()
	r, w, codes := newTargetRig(t)
	_ = r
	art := synthArt(t, "HypERC20", tokenABI, 400, map[string][]int{"MailboxClient.mailbox": {10, 200}, "MailboxClient.localDomain": {60}, "HypERC20._decimals": {100},
		"TokenRouter.scaleNumerator": {140}, "TokenRouter.scaleDenominator": {250}, "Indexed.deployedBlock": {300}})
	impl := common.HexToAddress("0x000000000000000000000000000000000000d00d")
	codes[impl] = fillArt(art, map[string][32]byte{"MailboxClient.mailbox": w32(m3bMailbox), "MailboxClient.localDomain": u32(uint64(m3bEthDom)), "HypERC20._decimals": u32(6),
		"TokenRouter.scaleNumerator": u32(1), "TokenRouter.scaleDenominator": u32(1), "Indexed.deployedBlock": u32(77)})
	input, err := art.CreationCode(repro.Values{"decimals": uint8(6), "scalenumerator": big.NewInt(1), "scaledenominator": big.NewInt(1), "mailbox": m3bMailbox})
	if err != nil {
		t.Fatal(err)
	}
	tx := common.HexToHash("0xc7ea7e")
	r.chain.creation(tx, common.HexToAddress("0x00000000000000000000000000000000000de9e7"), input, impl, 77)
	w.Builds = map[string]Build{"hyperc20-v2": BuildOf(art)}
	w.CreationTxs[impl] = tx
	check := func() string {
		return w.ImplementationCheck(ctx, ethx.AtNumber(100), m3bAdmin, m3bGNOT, impl, crypto.Keccak256Hash(codes[impl]), nil, "")
	}
	if got := check(); !strings.HasSuffix(got, " — "+VerdictImmutable) {
		t.Fatalf("an honest upgrade with its layouts read is not immutable:\n%s", got)
	}
	for _, c := range []struct{ name, old, new, want string }{
		{"no layouts", "", "", "LAYOUT NOT CHECKED: no storage layouts given"},
		{"one layout", appendOnlyOld, "", "LAYOUT NOT CHECKED: no storage layouts given"},
		{"a layout that does not read", appendOnlyOld, "not json", "the new layout does not read"},
		{"an empty running layout", `{}`, appendOnlyNew, "the running layout holds no entry"},
		{"a reordered layout", appendOnlyOld, `{"storage":[{"label":"_minter","slot":"0","offset":0,"type":"t_address"},{"label":"_owner","slot":"1","offset":0,"type":"t_address"}]}`, "LAYOUT DIFFERS"},
	} {
		w.LayoutOld, w.LayoutNew = []byte(c.old), []byte(c.new)
		got := check()
		if !strings.Contains(got, c.want) || !strings.Contains(got, " — "+VerdictUnshown) {
			t.Errorf("%s: want %q and NOT SHOWN IMMUTABLE:\n%s", c.name, c.want, got)
		}
	}
}

// A storage gap (`uint256[40] private __gap`, in the gate and the client) is the plan's own way to add a variable to a
// contract others inherit: the new variables take the gap's first slots and a smaller gap the rest. LayoutDiff accepts
// that, and refuses a gap grown past its end or a variable placed anywhere else in the running layout.
func TestF12LayoutDiffAcceptsAConsumedGap(t *testing.T) {
	types := `"types":{"t_address":{"numberOfBytes":"20"},"t_uint256":{"numberOfBytes":"32"},"t_array(t_uint256)40_storage":{"numberOfBytes":"1280"},"t_array(t_uint256)38_storage":{"numberOfBytes":"1216"},"t_array(t_uint256)41_storage":{"numberOfBytes":"1312"}}`
	old := `{"storage":[{"label":"_owner","slot":"0","offset":0,"type":"t_address"},{"label":"__gap","slot":"1","offset":0,"type":"t_array(t_uint256)40_storage"},{"label":"_child","slot":"41","offset":0,"type":"t_uint256"}],` + types + `}`
	cases := []struct{ name, new, want string }{
		{"two variables in the gap, the gap two smaller", `{"storage":[{"label":"_owner","slot":"0","offset":0,"type":"t_address"},{"label":"_a","slot":"1","offset":0,"type":"t_uint256"},{"label":"_b","slot":"2","offset":0,"type":"t_address"},{"label":"__gap","slot":"3","offset":0,"type":"t_array(t_uint256)38_storage"},{"label":"_child","slot":"41","offset":0,"type":"t_uint256"}],` + types + `}`, LayoutAppendOnly + " (2 entries kept, 0 added after them, 3 placed in a storage gap's slots)"},
		{"the gap kept, a variable after", `{"storage":[{"label":"_owner","slot":"0","offset":0,"type":"t_address"},{"label":"__gap","slot":"1","offset":0,"type":"t_array(t_uint256)40_storage"},{"label":"_child","slot":"41","offset":0,"type":"t_uint256"},{"label":"_x","slot":"42","offset":0,"type":"t_uint256"}],` + types + `}`, LayoutAppendOnly + " (3 entries kept, 1 added after them)"},
		{"a variable in the gap, the gap not shrunk", `{"storage":[{"label":"_owner","slot":"0","offset":0,"type":"t_address"},{"label":"_a","slot":"1","offset":0,"type":"t_uint256"},{"label":"__gap","slot":"2","offset":0,"type":"t_array(t_uint256)40_storage"},{"label":"_child","slot":"42","offset":0,"type":"t_uint256"}],` + types + `}`, "LAYOUT DIFFERS"},
		{"the gap grown past its end", `{"storage":[{"label":"_owner","slot":"0","offset":0,"type":"t_address"},{"label":"__gap","slot":"1","offset":0,"type":"t_array(t_uint256)41_storage"},{"label":"_child","slot":"41","offset":0,"type":"t_uint256"}],` + types + `}`, "LAYOUT DIFFERS"},
		{"a variable before the gap", `{"storage":[{"label":"_owner","slot":"0","offset":0,"type":"t_address"},{"label":"_a","slot":"0","offset":20,"type":"t_uint256"},{"label":"__gap","slot":"1","offset":0,"type":"t_array(t_uint256)40_storage"},{"label":"_child","slot":"41","offset":0,"type":"t_uint256"}],` + types + `}`, "LAYOUT DIFFERS"},
	}
	for _, c := range cases {
		if got := LayoutDiff([]byte(old), []byte(c.new)); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: %s", c.name, got)
		}
	}
}

// One -layout-old/-layout-new pair is one implementation's: the page holds no kind 8 to it while several are pending
// (each is read alone with hlwatch check), and a pending kind 8 NOT SHOWN IMMUTABLE keeps the page from green.
func TestF12StatusLayoutsAreOneImplementations(t *testing.T) {
	r, w, codes := newTargetRig(t)
	r.m.ReadOnly = true
	a, b := common.HexToAddress("0x000000000000000000000000000000000000d00a"), common.HexToAddress("0x000000000000000000000000000000000000d00b")
	codes[a], codes[b] = []byte{0x60, 0x01}, []byte{0x60, 0x02}
	for i, x := range []struct {
		proxy, impl common.Address
	}{{m3bGNOT, a}, {m3bBUBBLE, b}} {
		w32p, w32i := ethx.AddressTo32(x.proxy), ethx.AddressTo32(x.impl)
		h := crypto.Keccak256Hash(codes[x.impl])
		params := append(append(append([]byte{}, w32p[:]...), w32i[:]...), h[:]...)
		bd := body(8, uint64(i+1), params...)
		r.decide(uint64(i+1), bd, 90, bd, true)
	}
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Checks) != 2 || !strings.Contains(v.Checks[0], "LAYOUT NOT CHECKED: one -layout-old/-layout-new pair was given and 2 kind 8s are pending") {
		t.Fatalf("the checks: %v", v.Checks)
	}
	if ok, why := v.Green(); ok || !strings.Contains(strings.Join(why, ";"), "is "+VerdictUnshown) {
		t.Fatalf("green over a pending kind 8 not shown immutable: %v", why)
	}
}

// ---------------------------------------------------------------------------------------------- E3-GT6 --

// E3-GT6: §5.6's ahead-stop lived in memory: a signer restarted at a head below T signed at once (go-trust-poc's
// zz_audit3_ahead_restart_test.go). The stop is in the state: a restart keeps it, a verdict at or above T lifts it.
func TestF12AheadStopSurvivesARestart(t *testing.T) {
	r := newSignerRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.clientAt(5000, [32]byte{1}) // the client trusts height 5,000; gno.land's head is 60
	second := newM3bGno(60, r.now.Add(time.Second))
	r.s.sources = []gnoside.Reader{second}
	r.round()
	if st := r.status(); st["aheadHalt"] != true {
		t.Fatalf("no ahead-stop: %v", st)
	}
	r.chain.burn(m3bGNOT, gnot, r.alice, 100, 90)
	r.gno.advance(4500, r.now.Add(2*time.Second))
	second.advance(4500, r.now.Add(2*time.Second))
	restart := func() {
		s2 := &Signer{Cfg: r.s.Cfg, Key: r.s.Key, Store: r.s.Store, Every: time.Second, Alarm: &Alarmer{Source: "test2"}, pair: r.s.pair, gno: r.gno, sources: []gnoside.Reader{second}, now: r.clock}
		if err := s2.load(); err != nil {
			t.Fatal(err)
		}
		r.s = s2
	}
	restart()
	r.step(time.Second)
	r.round()
	st := r.status()
	if st["signing"] == true || st["aheadHalt"] != true || !r.alarmed("signer:halt") {
		t.Fatalf("a restart below T lifted the ahead-stop: %v", st)
	}
	if _, err := r.s.readSigned(0); err == nil {
		t.Fatal("burn #0 was signed under the ahead-stop after a restart")
	}
	// gno.land reaches T: a verdict at or above it lifts the stop, and a restart after keeps it lifted
	r.gno.advance(5001, r.now.Add(3*time.Second))
	second.advance(5001, r.now.Add(3*time.Second))
	r.step(time.Second)
	r.round()
	if st := r.status(); st["aheadHalt"] == true || st["halted"] != "" {
		t.Fatalf("a verdict at T did not lift the stop: %v", st)
	}
	restart()
	if st := r.status(); st["aheadHalt"] == true || st["halted"] != "" {
		t.Fatalf("a lifted stop came back with a restart: %v", st)
	}
}

// ---------------------------------------------------------------------------------------------- E3-GT8 --

// E3-GT8: nothing required eth.client; without it the signer skipped §5.6's ahead-stop and the monitor every Ethereum
// rule, silently. The signer, the monitor and hlwatch refuse to start without the contracts each rule reads.
func TestF12TrustPathRefusesAnIncompleteConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	chain := newM3bChain(100)
	pair, _, _ := m3bPair(chain)
	g := newM3bGno(60, time.Now())
	k, err := hl.NewValidatorKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg := m3bConfig()
	cfg.Eth.Client = ""
	s := &Signer{Cfg: cfg, Key: k, Store: t.TempDir(), Alarm: &Alarmer{Source: "test"}, pair: pair, gno: g}
	if err := s.Run(ctx); err == nil || !strings.Contains(err.Error(), "eth.client") {
		t.Fatalf("a signer without eth.client: %v", err)
	}
	for _, role := range []string{"monitor", "hlwatch"} {
		m := &Monitor{Cfg: cfg, Alarm: &Alarmer{Source: "test"}, ReadOnly: role == "hlwatch", pair: pair, own: g, sources: []gnoside.Reader{g}}
		if err := m.Open(ctx); err == nil || !strings.Contains(err.Error(), "eth.client") {
			t.Fatalf("a %s without eth.client: %v", role, err)
		}
	}
	cfg = m3bConfig()
	cfg.Routes[1].Token = ""
	m := &Monitor{Cfg: cfg, Alarm: &Alarmer{Source: "test"}, ReadOnly: true, pair: pair, own: g, sources: []gnoside.Reader{g}}
	if err := m.Open(ctx); err == nil || !strings.Contains(err.Error(), "routes[BUBBLE].token") {
		t.Fatalf("hlwatch with a route without its token: %v", err)
	}
}

// ---------------------------------------------------------------------------------------------- E3-GT4 --

// E3-GT4: a monitor started with -from-block past an honest burn flagged its waiting claim as a forged credit — credits,
// payouts and the gate (go-trust-poc's zz_audit3_fromblock_test.go). An index that misses messages of the deployment
// (Mailbox.nonce() at the block before it, past the launch line) is a checkpoint, as the Solana monitors' (G49): such a
// claim is alarmed, never flagged. An index from the deploy block still flags an unindexed claim.
func TestF12FromBlockAlarmsAnHonestOpenClaim(t *testing.T) {
	setup := func(t *testing.T, from uint64) (*monRig, string) {
		r := newMonRig(t)
		gnot := gnoside.RealmKey(m3bNS + "hlgnot")
		id := r.chain.burn(m3bGNOT, gnot, r.alice, 200, 80) // alice's honest burn at block 80
		idh := hex.EncodeToString(id[:])
		r.gno.set(m3bNS+"hlgnot", "json", `{"live":true,"lock":1000,"owed":200,"voided":0,"lost":0,"fee":5000000,"openClaims":1}`)
		r.gno.set(m3bMailPath, "delivered/"+idh, "59")
		claim := `{"id":"` + idh + `","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":200,"state":"open","by":"verifier","height":59}`
		r.gno.set(m3bNS+"hlgnot", "claim/"+idh, claim)
		r.gno.set(m3bNS+"hlgnot", "claims/0", "["+claim+"]")
		r.m.state.From = from
		for i := 0; i < 3; i++ {
			if err := r.poll(); err != nil {
				t.Fatalf("poll %d: %v", i, err)
			}
			r.step(10 * time.Second)
		}
		return r, idh
	}
	t.Run("an index past the burn", func(t *testing.T) {
		r, idh := setup(t, 90) // -from-block 90: a fresh state that starts after the burn
		if fl := r.gnoFlags(); len(fl) > 0 || r.snd.count() != 0 {
			t.Fatalf("an honest open claim was flagged as a forged credit: gno %v, eth %v", fl, r.snd.calls)
		}
		if text := r.m.Alarm.Standing()["credit:unindexed-before-base"]; !strings.Contains(text, idh) { // one alarm for them all (E5-CF3)
			t.Fatalf("the unindexed claim is not alarmed: %v", r.m.Alarm.StandingKeys())
		}
		b := r.m.state.IndexBase
		if b == nil || b.Missed != 1 || b.FromHeight == 0 {
			t.Fatalf("the index's base: %+v", b)
		}
	})
	t.Run("an index from the deploy block", func(t *testing.T) {
		r, _ := setup(t, 1)
		r.chain.retime(uint64(r.now.Unix())) // the index's finalized block past the credit's time (E4-GT1)
		forged := strings.Repeat("cd", 32)   // a claim no burn carries
		r.gno.set(m3bMailPath, "delivered/"+forged, "61")
		r.gno.set(m3bNS+"hlgnot", "claims/0", `[{"id":"`+forged+`","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":200,"state":"open","by":"verifier","height":61}]`)
		r.gno.set(m3bNS+"hlgnot", "claim/"+forged, `{"id":"`+forged+`","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":200,"state":"open","by":"verifier","height":61}`)
		for i := 0; i < 2; i++ {
			if err := r.poll(); err != nil {
				t.Fatal(err)
			}
			r.step(10 * time.Second)
		}
		if !r.alarmed("credit:unindexed:"+forged+":flag") || len(r.gnoFlags()) == 0 {
			t.Fatalf("a forged credit on an index from the deploy block was not flagged: %v", r.m.Alarm.StandingKeys())
		}
	})
}

// ---------------------------------------------------------------------------------------------- E3-GT7 --

// E3-GT7: `hlwatch check` read a kind 8's proxy and call from the flags typed, so a check without them said "no call
// (ProxyAdmin.upgrade)" of a decision that carried one. It reads them from the decision: a number r/hlgov dispatched,
// or a GovDAO proposal r/hlgov's builder made.
func TestF12CheckReadsTheDecisionsOwnParameters(t *testing.T) {
	r, w, codes := newTargetRig(t)
	impl := common.HexToAddress("0x000000000000000000000000000000000000d00d")
	codes[impl] = []byte{0x60, 0x80, 0x60, 0x40}
	h := crypto.Keccak256Hash(codes[impl])
	call := []byte{0xde, 0xad, 0xbe, 0xef}
	wp, wi := ethx.AddressTo32(m3bMailbox), ethx.AddressTo32(impl)
	params := append(append(append(append([]byte{}, wp[:]...), wi[:]...), h[:]...), call...)
	bd := body(8, 4, params...)
	j, _ := json.Marshal(map[string]any{"number": 4, "kind": 8, "body": hex.EncodeToString(bd), "id": "", "count": 1, "at": 1})
	r.gno.set(m3bNS+"hlgov", "decision/4", string(j))
	spec, err := w.DecisionSpec(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Kind != 8 || spec.Proxy != m3bMailbox || spec.Target != impl || spec.CodeHash != h || !strings.EqualFold(hex.EncodeToString(spec.Call), "deadbeef") {
		t.Fatalf("decision 4 read as %+v", spec)
	}
	got, err := w.TargetCheck(context.Background(), ethx.AtNumber(100), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "r/hlgov's decision 4: implementation") || !strings.Contains(got, "the call is 0xdeadbeef") || strings.Contains(got, "no call") {
		t.Fatalf("the check of decision 4:\n%s", got)
	}
	// before the vote: GovDAO's proposal, made with r/hlgov's builder
	r.m.Cfg.Gno.DAO = "gno.land/r/gov/dao"
	page := fmt.Sprintf("## Prop #7 - hlgov: upgrade\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\nupgrade mailbox %s to implementation %s with code hash 0x%s, then call 0xdeadbeef (4 bytes, upgradeAndCall)\n\nExecutor created in: `%s`\n\n---\n\n### Stats\n- YES PERCENT: 0%%\n", m3bMailbox.Hex(), impl.Hex(), hex.EncodeToString(h[:]), m3bNS+"hlgov")
	r.gno.set("gno.land/r/gov/dao", "7", page)
	spec2, err := w.ProposalSpec(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if spec2.Kind != 8 || spec2.Proxy != m3bMailbox || spec2.Target != impl || spec2.CodeHash != h || hex.EncodeToString(spec2.Call) != "deadbeef" {
		t.Fatalf("proposal 7 read as %+v", spec2)
	}
	// a proposal another realm's executor made is not read
	r.gno.set("gno.land/r/gov/dao", "8", strings.Replace(page, m3bNS+"hlgov", "gno.land/r/someone/else", 1))
	if _, err := w.ProposalSpec(context.Background(), 8); err == nil || !strings.Contains(err.Error(), "not made with r/hlgov's builder") {
		t.Fatalf("a stranger's proposal: %v", err)
	}
	// kind 9's parameters
	ws := ethx.AddressTo32(impl)
	s9, err := SpecOfParams(9, append(ws[:], h[:]...), "x")
	if err != nil || s9.Kind != 9 || s9.Target != impl || s9.CodeHash != h {
		t.Fatalf("a kind 9: %+v %v", s9, err)
	}
	if _, err := SpecOfParams(8, params[:90], "x"); err == nil {
		t.Fatal("a short kind 8 was read")
	}
}

// ---------------------------------------------------------------------------------------------- E3-AS3 --

// encNode is the monitor's own node with headers, sets, commits and results (fakeGno's chain) and the node's /status.
type encNode struct {
	*m3bGnoNode
	f       *fakeGno
	mu      sync.Mutex
	version string
	header  func(b *gnolight.Block) *gnolight.Block // what the node serves for a block (nil: the chain's)
	results func(rs []*gnolight.TxResult) []*gnolight.TxResult
}

func (e *encNode) BlockAt(ctx context.Context, h int64) (*gnolight.Block, error) {
	b, err := e.f.BlockAt(ctx, h)
	e.mu.Lock()
	x := e.header
	e.mu.Unlock()
	if err != nil || x == nil {
		return b, err
	}
	return x(b), nil
}

func (e *encNode) ValidatorsAt(ctx context.Context, h int64) (*gnolight.ValidatorSet, error) {
	return e.f.ValidatorsAt(ctx, h)
}

func (e *encNode) CommitAt(ctx context.Context, h int64) (*gnolight.Commit, []*gnolight.Vote, error) {
	return e.f.CommitAt(ctx, h)
}

func (e *encNode) BlockResults(ctx context.Context, h int64) ([]*gnolight.TxResult, error) {
	rs, err := e.f.BlockResults(ctx, h)
	e.mu.Lock()
	x := e.results
	e.mu.Unlock()
	if err != nil || x == nil {
		return rs, err
	}
	return x(rs), nil
}

func (e *encNode) Request(_ context.Context, method string, _ map[string]any, result any) error {
	if method != "status" {
		return fmt.Errorf("fake: no %s", method)
	}
	e.mu.Lock()
	v := e.version
	e.mu.Unlock()
	return json.Unmarshal([]byte(`{"node_info":{"version":"`+v+`","version_set":[{"Name":"app","Version":"`+v+`"}]}}`), result)
}

// E3-AS3: the client compiles tm2 v1.2.0's encodings for its life and gnoland-1 upgrades often; nothing watched for an
// upgrade that changed one. The monitor rebuilds, as the client does, the headers it reads (fields → hash), the block
// below the head whole (its set's leaf, its precommits' sign bytes, its results leaves) and alarms gno:encoding on a
// difference; it records the node's version and alarms gno:version when it changes.
func TestF12MonitorWatchesTheEncodings(t *testing.T) {
	r := newMonRig(t)
	f := newFakeGno(m3bMailPath, 1000)
	f.mu.Lock()
	for h := int64(1); h <= 100; h++ { // every block carries a send's transaction: its results are rebuilt below the head
		f.results[h] = append(f.results[h], dispatchTx(m3bMailPath, hl.Message{Version: 3, Nonce: uint32(h), Origin: m3bGnoDom, Sender: gnoside.RealmKey(m3bNS + "hlgnot"), Destination: m3bEthDom, Recipient: ethx.AddressTo32(m3bGNOT), Body: hl.TokenMessage([32]byte{1}, 5, nil)}.Bytes()))
	}
	f.mu.Unlock()
	node := &encNode{m3bGnoNode: r.gno, f: f, version: "v1.5.1"}
	r.m.own = node
	poll := func(what string) {
		t.Helper()
		r.step(61 * time.Second) // past the minute between rebuilds
		if err := r.poll(); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	poll("honest")
	if r.alarmed("gno:encoding") || r.alarmed("gno:version") {
		t.Fatalf("an honest node: alarms %v", r.m.Alarm.Standing())
	}
	// a header whose fields no longer hash to the node's block hash (the header's encoding changed)
	node.mu.Lock()
	node.header = func(b *gnolight.Block) *gnolight.Block {
		c := *b
		c.Hash[0] ^= 0xff
		return &c
	}
	node.mu.Unlock()
	poll("the header's encoding")
	if text := r.m.Alarm.Standing()["gno:encoding"]; !strings.Contains(text, "fields hash to") {
		t.Fatalf("a header whose fields hash otherwise is not alarmed: %v", r.m.Alarm.Standing())
	}
	node.mu.Lock()
	node.header = nil
	node.mu.Unlock()
	poll("honest again")
	if r.alarmed("gno:encoding") {
		t.Fatalf("the alarm stands over an honest node: %v", r.m.Alarm.Standing())
	}
	// a results leaf the client rebuilds otherwise (the results' encoding changed)
	node.mu.Lock()
	node.results = func(rs []*gnolight.TxResult) []*gnolight.TxResult {
		out := make([]*gnolight.TxResult, len(rs))
		for i, x := range rs {
			c := *x
			c.Leaf = append(append([]byte{}, x.Leaf...), 0x00)
			out[i] = &c
		}
		return out
	}
	node.mu.Unlock()
	poll("the results' encoding")
	if text := r.m.Alarm.Standing()["gno:encoding"]; !strings.Contains(text, "LastResultsHash") {
		t.Fatalf("results that hash otherwise are not alarmed: %v", r.m.Alarm.Standing())
	}
	node.mu.Lock()
	node.results = nil
	node.mu.Unlock()
	// precommits signed over other sign bytes (the vote's encoding changed): here, another chain id in them
	f.mu.Lock()
	f.chainID = "other"
	f.headers = map[int64]*gnolight.Block{}
	f.mu.Unlock()
	poll("the vote's encoding")
	if text := r.m.Alarm.Standing()["gno:encoding"]; !strings.Contains(text, "do not verify over the sign bytes the client builds") {
		t.Fatalf("precommits that do not verify are not alarmed: %v", r.m.Alarm.Standing())
	}
	f.mu.Lock()
	f.chainID = "dev"
	f.headers = map[int64]*gnolight.Block{}
	f.mu.Unlock()
	// an upgrade: the node's version changes — alarmed once, for a day
	node.mu.Lock()
	node.version = "v1.6.0"
	node.mu.Unlock()
	poll("the upgrade")
	if text := r.m.Alarm.Standing()["gno:version"]; !strings.Contains(text, `from "v1.5.1 (app@v1.5.1)" to "v1.6.0 (app@v1.6.0)"`) || r.alarmed("gno:encoding") {
		t.Fatalf("the version change: %v", r.m.Alarm.Standing())
	}
}

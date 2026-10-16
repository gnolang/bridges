package agent

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The tests of audit round 5's fixes (bridge/AUDIT.md: the red team's AR1 and AR3–AR6 where the Go carries them,
// the Go audit's G36–G45, the programs audit's S-20 and S-21 on the Go side, the assumptions audit's F21–F24,
// the conformance audit's K34–K37).

const testSucc2 = "gno.land/r/test/hlbubble3"

// mailboxLineJSON renders a mailbox json whose routes carry a line's predecessor and successor entries (AddRoute
// records them at each migrate's apply).
func mailboxLineJSON(line ...string) *mailboxJSON {
	mb := &mailboxJSON{Domain: hl.DomainGnoTest, Routes: map[string]routeFlags{}}
	for i, p := range line {
		rf := routeFlags{}
		if i > 0 {
			rf.Predecessor = line[i-1]
		}
		if i+1 < len(line) {
			rf.Successor = line[i+1]
		}
		mb.Routes[p] = rf
	}
	return mb
}

// AR6, G41: a line of migrations is followed from the configured path to its end (sixteen hops, as hlmailbox
// walks), the identity summed over every realm of the line, a burn addressed to any realm of it delivered to the
// last, the claims paid there, the executes walking every realm; the same line is found from its last or its
// middle realm through the mailbox's entries; a loop and a line past the bound are refused.
func TestMigrationLineIsFollowedToItsEnd(t *testing.T) {
	f := newFake()
	ctx := context.Background()
	read := routeReaderAt(f, 100)
	r := Route{Symbol: "BUBBLE", Gno: testRoute, Token: "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"}
	mb := mailboxLineJSON(testRoute, testSucc, testSucc2)
	// P migrated to S (whose import ended), S migrated on to S2, importing: the lock sits on S2 from its page 0
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(0, 0, testSucc2, false, 3)
	f.renders[testSucc2+":json"] = routeJSONWith(1_000, 200, "", true, 1)
	pj, _ := routeAt(ctx, f, testRoute, 100)
	mig, err := migrationOf(ctx, read, r, pj, mb)
	if err != nil || mig == nil || mig.Predecessor != testRoute || mig.Successor != testSucc2 || len(mig.Hops) != 1 || mig.Hops[0].Path != testSucc {
		t.Fatalf("the line from its first realm: %+v %v", mig, err)
	}
	if got := strings.Join(mig.Paths(), ","); got != testRoute+","+testSucc+","+testSucc2 {
		t.Fatalf("the paths: %s", got)
	}
	if !mig.Importing() || mig.Ended() || mig.Live() != testSucc2 || !mig.Has(testSucc) || mig.Has("gno.land/r/test/other") {
		t.Fatalf("the line's state: importing %v ended %v live %s", mig.Importing(), mig.Ended(), mig.Live())
	}
	if !strings.Contains(mig.Text(), "migrating "+testRoute+" → "+testSucc+" → "+testSucc2+", IMPORTING (1 pages so far)") || !strings.Contains(mig.Text(), "the line's 3 realms' summed counters") {
		t.Fatalf("the text: %s", mig.Text())
	}
	if others := strings.Join(mig.Others(testRoute), ","); others != testSucc2+","+testSucc {
		t.Fatalf("the other realms, the live one first: %s", others)
	}
	// the identity over the line holds; the first pair alone (what one hop summed) read BROKEN — the premise
	pair := &migration{Symbol: "BUBBLE", Predecessor: testRoute, Successor: testSucc, Pred: pj, Succ: mig.Hops[0].JSON}
	if v, _ := identityOf(r, pj, pair, 100, 800, 7, 0, 0).Verdict(); v != Broken {
		t.Fatal("the first pair alone did not read BROKEN (the test's premise)")
	}
	if v, text := identityOf(r, pj, mig, 100, 800, 7, 0, 0).Verdict(); v != Holds {
		t.Fatalf("the line: %s %s", v, text)
	}
	// mid-line: S2's page 0 not yet pulled, the lock still on S — the sum is the same
	f.renders[testSucc+":json"] = routeJSONWith(1_000, 200, testSucc2, false, 3)
	f.renders[testSucc2+":json"] = routeJSONWith(0, 0, "", false, 0)
	mig, _ = migrationOf(ctx, read, r, pj, mb)
	if v, _ := identityOf(r, pj, mig, 100, 800, 7, 0, 0).Verdict(); v != Holds || mig.Ended() || mig.Importing() {
		t.Fatalf("the line before S2's page 0: %s", mig.Text())
	}
	f.renders[testSucc+":json"] = routeJSONWith(0, 0, testSucc2, false, 3)
	f.renders[testSucc2+":json"] = routeJSONWith(1_000, 200, "", false, 2)
	// the same line from its last realm, and from its middle one, through the mailbox's entries
	for _, start := range []string{testSucc2, testSucc} {
		sj, _ := routeAt(ctx, f, start, 100)
		m, err := migrationOf(ctx, read, Route{Symbol: "BUBBLE", Gno: start}, sj, mb)
		if err != nil || m == nil || strings.Join(m.Paths(), ",") != testRoute+","+testSucc+","+testSucc2 {
			t.Fatalf("the line from %s: %+v %v", start, m, err)
		}
	}
	// a record by id is read on the live successor first, then the realms between
	f.renders[testSucc2+":claim/cc"] = `{"id":"cc","to":"g1x","amount":5,"paid":0,"state":"open","by":"signers","at":1,"seq":1}`
	f.renders[testRoute+":claim/cc"] = "null"
	if c, on, err := claimOnPair(ctx, f, testRoute, mig, "cc", 100); err != nil || c == nil || on != testSucc2 {
		t.Fatalf("the claim on the line's last realm: %v %s %v", c, on, err)
	}
	f.renders[testRoute+":send/dd"] = "null"
	f.renders[testSucc2+":send/dd"] = "null"
	f.renders[testSucc+":send/dd"] = `{"id":"dd","from":"g1y","amount":7,"at":1,"refunded":0}`
	if sd, err := sendOnPair(ctx, f, testRoute, mig, "dd", 100); err != nil || sd == nil || sd.From != "g1y" {
		t.Fatalf("the send record on a realm between: %v %v", sd, err)
	}
	// the relayer: a burn addressed to any realm of the line goes to the last once its import ended; the claims
	// are paid there; the executes walk every realm
	rl := testRelayer(f, "primary")
	token := [32]byte{9}
	rl.Cfg.Routes[0].Token = hl.Base58(token[:])
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"live":true,"routes":{%q:{"successor":%q},%q:{"predecessor":%q,"successor":%q},%q:{"predecessor":%q}}}`,
		hl.DomainGnoTest, testRoute, testSucc, testSucc, testRoute, testSucc2, testSucc2, testSucc)
	burn := func(pkg string) solDispatch {
		rcpt, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(pkg))
		m := hl.Message{Version: 3, Nonce: 1, Origin: hl.DomainSolanaTestnet, Sender: token, Destination: hl.DomainGnoTest, Recipient: rcpt, Body: hl.TokenMessage([32]byte{1}, 500, nil)}
		d := solDispatch{Sig: "s", Slot: 10, Raw: hexBytes(m.Bytes())}
		if err := d.fill(); err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, pkg := range []string{testRoute, testSucc, testSucc2} {
		rl.resetStep()
		if rt, target, wait, err := rl.deliveryTarget(ctx, burn(pkg)); err != nil || rt == nil || target != testSucc2 || wait != "" {
			t.Fatalf("a burn addressed to %s after the last page: %v %q %q %v", pkg, rt, target, wait, err)
		}
	}
	rl.resetStep()
	if rt, _, _, _ := rl.deliveryTarget(ctx, burn("gno.land/r/test/stranger")); rt != nil {
		t.Fatal("a burn to a stranger's address was taken")
	}
	f.renders[testSucc2+":json"] = routeJSONWith(1_000, 200, "", true, 1)
	rl.resetStep()
	if _, target, wait, _ := rl.deliveryTarget(ctx, burn(testSucc)); target != testSucc2 || !strings.Contains(wait, "still arriving (1 pages so far)") {
		t.Fatalf("a burn addressed to the realm between, mid-import: %q %q", target, wait)
	}
	f.renders[testSucc2+":json"] = routeJSONWith(1_000, 200, "", false, 2)
	if realms := strings.Join(rl.realms(ctx), ","); !strings.Contains(realms, testRoute+","+testSucc+","+testSucc2) {
		t.Fatalf("the executes walk: %s", realms)
	}
	f.renders[testSucc2+":json"] = routeJSONWith(1_000, 200, "", false, 2)
	_ = rl.claims(ctx)
	if n := len(f.callsOf(testSucc2 + ".Claim")); n != 1 {
		t.Fatalf("Claim not called on the line's last realm: %d (%v)", n, f.calls)
	}
	if n := len(f.callsOf(testSucc+".Claim")) + len(f.callsOf(testRoute+".Claim")); n != 0 {
		t.Fatalf("Claim called on a migrated realm: %d", n)
	}
	// the monitor flags the line's last realm on a break, and the claims walk visits every realm
	m := testMonitor(f)
	m.migs = map[string]*migration{"BUBBLE": mig}
	if paths := m.liveRoutes(); len(paths) != 2 || paths[1] != testSucc2 {
		t.Fatalf("the live routes: %v", paths)
	}
	// a loop is refused, and so is a line past the bound
	f.renders[testSucc2+":json"] = routeJSONWith(1_000, 200, testRoute, false, 2)
	if _, err := migrationOf(ctx, read, r, pj, mb); err == nil || !strings.Contains(err.Error(), "loops") {
		t.Fatalf("a loop was followed: %v", err)
	}
	long := []string{testRoute}
	for i := 1; i <= maxChain+1; i++ {
		long = append(long, fmt.Sprintf("gno.land/r/test/hop%d", i))
	}
	for i, p := range long {
		next := ""
		if i+1 < len(long) {
			next = long[i+1]
		}
		f.renders[p+":json"] = routeJSONWith(0, 0, next, false, 1)
	}
	lj, _ := routeAt(ctx, f, testRoute, 100)
	if _, err := migrationOf(ctx, read, r, lj, nil); err == nil || !strings.Contains(err.Error(), "runs past 16 hops") {
		t.Fatalf("a line past the bound was followed: %v", err)
	}
	f.renders[long[maxChain]+":json"] = routeJSONWith(1_000, 0, "", false, 1)
	if m, err := migrationOf(ctx, read, r, lj, nil); err != nil || m == nil || len(m.Hops) != maxChain-1 || m.Successor != long[maxChain] {
		t.Fatalf("a line of exactly %d hops: %+v %v", maxChain, m, err)
	}
}

// AR3: a burn addressed to the successor itself (kind 6 enrolled it) waits on the successor's import having
// ended, as one addressed to the predecessor does — never delivered before the first page (the realm would sweep
// it: before page 0 the lock is not there), never while pages arrive.
func TestSuccessorAddressedBurnWaitsForTheFirstPage(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	succ, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testSucc))
	m := hl.Message{Version: 3, Nonce: 1, Origin: hl.DomainSolanaTestnet, Sender: token, Destination: hl.DomainGnoTest, Recipient: succ, Body: hl.TokenMessage([32]byte{1}, 500, nil)}
	d := solDispatch{Sig: "s", Slot: 10, Raw: hexBytes(m.Bytes())}
	if err := d.fill(); err != nil {
		t.Fatal(err)
	}
	f.renders[testRoute+":json"] = routeJSONWith(1000, 0, testSucc, false, 0)
	for _, c := range []struct {
		succ string
		wait string
	}{
		{routeJSONWith(0, 0, "", false, 0), "whose import has not begun"},        // before page 0: the lock is not there yet
		{routeJSONWith(1000, 0, "", true, 2), "still arriving (2 pages so far)"}, // pages arriving: every credit refused
		{routeJSONWith(1000, 0, "", false, 3), ""},                               // the last page is in: delivered
	} {
		r.resetStep()
		f.renders[testSucc+":json"] = c.succ
		rt, target, wait, err := r.deliveryTarget(ctx, d)
		if err != nil || rt == nil || target != testSucc || !strings.Contains(wait, c.wait) || (c.wait == "" && wait != "") {
			t.Fatalf("a burn addressed to the successor at %s: %v %q %q %v", c.succ, rt, target, wait, err)
		}
	}
}

const testGnot = "gno.land/r/test/hlgnot"

// G44: one migration partner's render error is that token's "no verdict" — its identity is not judged (the
// predecessor alone would read BROKEN), its cap ratio, diverted and unindexed rules skip it marking nothing —
// while the other token and the rest of the poll run; hlwatch names the error per token and keeps the other
// token's identity.
func TestPartnerReadErrorIsNoVerdictForTheToken(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	m.Cfg.Routes = append(m.Cfg.Routes, Route{Symbol: "GNOT", Gno: testGnot, Token: "9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr"})
	ctx := context.Background()
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0) // migrated; the successor's render fails
	f.renders[testGnot+":json"] = routeJSONWith(1_000, 0, "", false, 0)
	routes := map[string]*routeJSON{}
	for _, r := range m.Cfg.Routes {
		rj, err := routeAt(ctx, f, r.Gno, 100)
		if err != nil {
			t.Fatal(err)
		}
		routes[r.Symbol] = rj
	}
	mb := mailboxWith()
	m.seat = 5
	migs := m.readMigrations(ctx, routes, mb, 100)
	if _, ok := migs["BUBBLE"]; ok || m.migErrs["BUBBLE"] == nil || m.migErrs["GNOT"] != nil || !m.lineUnread("BUBBLE") || m.lineUnread("GNOT") {
		t.Fatalf("the unread line: %v %v", migs, m.migErrs)
	}
	if !strings.Contains(m.status.Migrations["BUBBLE"], "NOT READ: BUBBLE: "+testRoute+"'s successor "+testSucc) {
		t.Fatalf("the status does not name the unread line: %v", m.status.Migrations)
	}
	snap := &Snapshot{Slot: 7, Supplies: map[string]int64{"BUBBLE": 1_000, "GNOT": 1_000}} // the lock sits on the successor
	m.judgeIdentities(ctx, mb, routes, migs, 100, snap, map[string]int64{}, map[string]int64{})
	bubble := m.status.Identity["BUBBLE"].(map[string]any)
	if bubble["verdict"] != "no verdict" || !strings.Contains(bubble["text"].(string), "could not be read this poll (G44)") {
		t.Fatalf("the token with an unread line: %v", bubble)
	}
	if gnot := m.status.Identity["GNOT"].(map[string]any); gnot["verdict"] != Holds.String() {
		t.Fatalf("the other token was not judged: %v", gnot)
	}
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 0 {
		t.Fatalf("a token with an unread line was flagged (the predecessor alone reads BROKEN): %d", n)
	}
	// the cap ratio skips the token (its lock is the line's sum, unknown), judges the other
	ps := &sol.PausableState{Limits: [2]sol.MintLimit{{DayCap: 100_000}, {DayCap: 100_000}}}
	m.capRatios(ps, routes, nil)
	if _, ok := m.Alarm.Standing()["mint:cap-ratio:BUBBLE"]; ok {
		t.Fatal("the cap ratio alarmed on a token whose line was not read")
	}
	if _, ok := m.Alarm.Standing()["mint:cap-ratio:GNOT"]; !ok {
		t.Fatalf("the other token's cap ratio was not judged: %v", m.Alarm.Standing())
	}
	// a credited dispatch of the token is not marked checked: its claim may sit on the unread realm
	m.state.Index["bb"] = &dispatchRec{Symbol: "BUBBLE", Slot: 1, To: "g1bob", Amount: 300, Credited: 91}
	if err := m.divertedCredits(ctx, mb, 100); err != nil || m.state.Index["bb"].Checked {
		t.Fatalf("the diverted check marked a record under an unread line: %v %v", m.state.Index["bb"].Checked, err)
	}
	f.renders[testGnot+":claims/0"] = "[]" // the other token's walk runs; the unread line's does not (no render for it)
	if err := m.unindexedCredits(ctx, mb, 100); err != nil {
		t.Fatalf("the unindexed rule walked an unread line: %v", err)
	}
	// the partner readable again: the token is judged over the line, and its record checked
	f.renders[testSucc+":json"] = routeJSONWith(1_000, 0, "", false, 1)
	f.renders[testSucc+":claim/bb"] = `{"id":"bb","to":"g1bob","amount":300,"paid":0,"state":"open","by":"signers","at":1,"seq":1}`
	f.renders[testRoute+":claim/bb"] = "null"
	migs = m.readMigrations(ctx, routes, mb, 100)
	if migs["BUBBLE"] == nil || m.lineUnread("BUBBLE") {
		t.Fatalf("the line read again: %v %v", migs, m.migErrs)
	}
	m.judgeIdentities(ctx, mb, routes, migs, 100, snap, map[string]int64{}, map[string]int64{})
	if v := m.status.Identity["BUBBLE"].(map[string]any)["verdict"]; v != Holds.String() {
		t.Fatalf("the token over its line: %v", v)
	}
	if err := m.divertedCredits(ctx, mb, 100); err != nil || !m.state.Index["bb"].Checked {
		t.Fatalf("the record was not checked once the line was read: %v", err)
	}
	// hlwatch's reading: the error per token, the other token's migration intact
	delete(f.renders, testSucc+":json")
	ms, errs := migrationsOf(ctx, f, m.Cfg, routes, mb, 100)
	if len(ms) != 0 || len(errs) != 1 || errs["BUBBLE"] == nil {
		t.Fatalf("migrationsOf: %v %v", ms, errs)
	}
}

// AR5: the cap-ratio quiet expires by the clock — the cap is judged from the later of the lock reaching the
// expectation and the realms' 14-day deadline after hlmailbox's launch (at the chain's clock factor), the
// standing persisted like CapReached; before the launch, or with no mailbox read, there is no clock.
func TestCapRatioQuietExpiresAFortnightAfterLaunch(t *testing.T) {
	cap := uint64(100_000)
	launch := int64(1_800_000_000)
	fortnight := int64(14 * 24 * 3600)
	mb := &mailboxJSON{LaunchedAt: launch}
	if got := capQuietEnds(mb); got != launch+fortnight {
		t.Fatalf("the quiet's end at factor 1: %d, want %d", got, launch+fortnight)
	}
	if got := capQuietEnds(&mailboxJSON{LaunchedAt: launch, Clocks: Clocks{Factor: 8, Delay: 7 * 24 * 3600 / 8, Deadline: fortnight / 8}}); got != launch+fortnight/8 {
		t.Fatalf("the quiet's end at factor 8: %d, want %d", got, launch+fortnight/8)
	}
	if capQuietEnds(nil) != 0 || capQuietEnds(&mailboxJSON{}) != 0 {
		t.Fatal("a quiet's end with no launch time")
	}
	ends := capQuietEnds(mb)
	if v := capRatio("BUBBLE", cap, 20_000, 1_000_000, false, ends, ends-1); v.Reached || v.Alarm != "" || !strings.Contains(v.Note, "or 14 days after the launch ("+time.Unix(ends, 0).UTC().Format(time.RFC3339)+", AR5)") {
		t.Fatalf("a day before the fortnight: %+v", v)
	}
	if v := capRatio("BUBBLE", cap, 20_000, 1_000_000, false, ends, ends); !v.Reached || !strings.Contains(v.Alarm, "above a fifth of its lock on gno.land, 20000") {
		t.Fatalf("at the fortnight: %+v", v)
	}
	if v := capRatio("BUBBLE", cap, 20_000, 1_000_000, false, 0, ends+1); v.Reached {
		t.Fatalf("no launch time, yet the quiet ended: %+v", v)
	}
	// the monitor: quiet a day before, alarmed at the fortnight, the standing kept
	f := newFake()
	m := testMonitor(f)
	m.Cfg.Routes[0].ExpectedLock = 1_000_000
	ps := &sol.PausableState{Limits: [2]sol.MintLimit{{DayCap: cap}, {DayCap: cap}}}
	routes := map[string]*routeJSON{"BUBBLE": {Lock: 20_000}}
	now := time.Unix(ends-86_400, 0)
	m.now = func() time.Time { return now }
	m.capRatios(ps, routes, mb)
	if _, ok := m.Alarm.Standing()["mint:cap-ratio:BUBBLE"]; ok || m.state.CapReached["BUBBLE"] {
		t.Fatalf("alarmed a day before the fortnight: %v", m.Alarm.Standing())
	}
	now = time.Unix(ends, 0)
	m.capRatios(ps, routes, mb)
	if _, ok := m.Alarm.Standing()["mint:cap-ratio:BUBBLE"]; !ok || !m.state.CapReached["BUBBLE"] {
		t.Fatalf("not alarmed at the fortnight: %v %v", m.Alarm.Standing(), m.state.CapReached)
	}
}

// G43: the quiet also ends once the lock reaches five day caps — the alarm is silent there anyway — so a lie in
// `expectedLock` (a tenfold figure, or one in whole tokens) delays the first judgement by at most the climb to
// five caps and never the standing after it.
func TestCapRatioReachesAtFiveCaps(t *testing.T) {
	cap := uint64(100_000)
	lie := int64(10_000_000_000_000)
	if v := capRatio("GNOT", cap, 499_999, lie, false, 0, 0); v.Reached || !strings.Contains(v.Note, "or five day caps (500000, G43)") {
		t.Fatalf("below five caps: %+v", v)
	}
	if v := capRatio("GNOT", cap, 500_000, lie, false, 0, 0); !v.Reached || v.Alarm != "" || v.Note != "" {
		t.Fatalf("at five caps: %+v", v)
	}
	if v := capRatio("GNOT", cap, 100_000, lie, true, 0, 0); !v.Reached || !strings.Contains(v.Alarm, "above a fifth of its lock on gno.land, 100000") {
		t.Fatalf("a lock that shrank after the reach: %+v", v)
	}
	if v := capRatio("GNOT", 0, 0, lie, false, 0, 0); v.Reached {
		t.Fatalf("a zero cap reached five caps: %+v", v)
	}
}

// AR1: a refunded send Solana processed after its refund is a double mint — the monitors alarm it as a standing
// refund:delivered:<id> (daily) until the route's write-off books it as lost, which the send record then shows;
// the mailbox's enrolment record and the send's write-off mark are read in the shapes the realms may render.
func TestRefundedSendDeliveredAfterAll(t *testing.T) {
	// the enrolment record: a height, a time, an object, or none
	for _, c := range []struct {
		json string
		want enrollMark
	}{
		{`{"enrolled":120}`, enrollMark{Height: 120}},
		{`{"enrolled":1800000500}`, enrollMark{At: 1_800_000_500}},
		{`{"enrolled":{"height":120,"at":1800000500}}`, enrollMark{Height: 120, At: 1_800_000_500}},
		{`{"enrolled":null}`, enrollMark{}},
		{`{}`, enrollMark{}},
	} {
		var rf routeFlags
		if err := jsonUnmarshal(c.json, &rf); err != nil || rf.Enrolled != c.want {
			t.Fatalf("the enrolment record %s: %+v %v", c.json, rf.Enrolled, err)
		}
	}
	var mb mailboxJSON
	if err := jsonUnmarshal(`{"routes":{"gno.land/r/x/hlgnot2":{"enrolled":120},"gno.land/r/x/hlgnot":{}}}`, &mb); err != nil || !mb.Routes["gno.land/r/x/hlgnot2"].Enrolled.Set() || mb.Routes["gno.land/r/x/hlgnot"].Enrolled.Set() {
		t.Fatalf("the mailbox's enrolment records: %+v %v", mb.Routes, err)
	}
	f := newFake()
	cfg := &Config{}
	cfg.Gno.Mailbox = testMailbox
	cfg.Solana.Domain = hl.DomainSolanaTestnet
	cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	token := [32]byte{9}
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute, Token: hl.Base58(token[:])}}
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	m0 := hl.Message{Version: 3, Nonce: 0, Origin: hl.DomainGnoTest, Sender: route, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, 25_000, nil)}
	id0 := hl.ID(m0.Bytes())
	idHex := hex.EncodeToString(id0[:])
	f.renders[testMailbox+":msg/0"] = msgRender(m0, 50)
	f.renders[testRoute+":send/"+idHex] = `{"id":"` + idHex + `","from":"g1alice","amount":25000,"at":1,"imported":true,"refunded":1800000500}`
	mailbox := sol.Mailbox{Program: cfg.SolMailbox()}
	provider := &countingSol{owner: mailbox.Program, processed: map[solana.PublicKey]bool{}}
	m := testMonitor(f)
	m.Cfg = cfg
	m.both = provider
	routes := map[string]*routeJSON{"BUBBLE": {Lock: 1000, Imported: 1}} // a successor: its imported sends may be refunded
	mbj := &mailboxJSON{Domain: hl.DomainGnoTest, Count: 1}
	if err := m.sends(context.Background(), mbj, routes, 100, &supplySnapshot{}, nil); err != nil {
		t.Fatal(err)
	}
	if s := m.state.Sends["0"]; !s.Refunded || s.Processed || s.DoubleMinted() {
		t.Fatalf("the refunded send: %+v", s)
	}
	if _, ok := m.Alarm.Standing()["send:refunded:0"]; !ok {
		t.Fatalf("no refunded alarm: %v", m.Alarm.Standing())
	}
	// Solana processes it after the refund: the standing double-mint alarm, the refunded one ended
	provider.processed[mailbox.ProcessedMessage(id0)] = true
	if err := m.sends(context.Background(), mbj, routes, 101, &supplySnapshot{}, nil); err != nil {
		t.Fatal(err)
	}
	if s := m.state.Sends["0"]; !s.DoubleMinted() {
		t.Fatalf("not a double mint: %+v", s)
	}
	if a, ok := m.Alarm.Standing()["refund:delivered:"+idHex]; !ok || !strings.Contains(a, "minted twice") || !strings.Contains(a, "write-off builder") {
		t.Fatalf("no double-mint alarm: %v", m.Alarm.Standing())
	}
	if _, ok := m.Alarm.Standing()["send:refunded:0"]; ok {
		t.Fatal("the refunded alarm stands beside the double-mint one")
	}
	// the write-off books it as lost: read within the minute, the alarm ends
	f.renders[testRoute+":send/"+idHex] = `{"id":"` + idHex + `","from":"g1alice","amount":25000,"at":1,"imported":true,"refunded":1800000500,"writtenOff":1800001200}`
	now := m.now().Add(time.Minute)
	m.now = func() time.Time { return now }
	if err := m.sends(context.Background(), mbj, routes, 102, &supplySnapshot{}, nil); err != nil {
		t.Fatal(err)
	}
	if s := m.state.Sends["0"]; !s.WrittenOff || s.DoubleMinted() {
		t.Fatalf("the write-off was not read: %+v", s)
	}
	if _, ok := m.Alarm.Standing()["refund:delivered:"+idHex]; ok {
		t.Fatal("the double-mint alarm stands after the write-off")
	}
}

// S-20: a successor that has not inherited runs nothing of its own (the program's awaits_inheritance): the Go
// reads it the same way. (hlrecover's init refusal went with the recovery path.)
func TestAwaitsInheritance(t *testing.T) {
	pred := solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	if !(&sol.AdminState{Predecessor: pred}).AwaitsInheritance() || (&sol.AdminState{Predecessor: pred, Inherited: 1}).AwaitsInheritance() || (&sol.AdminState{}).AwaitsInheritance() {
		t.Fatal("AwaitsInheritance is not the program's rule")
	}
}

// tokenStateBytes is a synthetic token program's state as ParseToken reads it, with one remote router enrolled
// for a domain (none when router is nil).
func tokenStateBytes(domain uint32, router *[32]byte) []byte {
	b := []byte{1, 255}                // initialized; the bump
	b = append(b, make([]byte, 64)...) // the mailbox, the process authority
	b = append(b, 254, 6, 6)           // the dispatch authority's bump, decimals, remote decimals
	b = append(b, 0, 0, 0)             // no owner, no ISM, no paymaster
	b = append(b, 0, 0, 0, 0)          // no destination gas
	if router == nil {
		b = append(b, 0, 0, 0, 0)
	} else {
		b = append(b, 1, 0, 0, 0, byte(domain), byte(domain>>8), byte(domain>>16), byte(domain>>24))
		b = append(b, router[:]...)
	}
	b = append(b, make([]byte, 32)...) // the mint
	return append(b, 253, 252)         // the mint's and the ATA payer's bumps
}

// K34: after a migration's kind 6 enrols the successor as the token's router, a send through the successor
// dispatches with the successor as sender — ours, by the line read once a step, never "other"; a stranger's
// send stays other; the token program's enrolled router is read and alarmed while it is no realm of the line.
func TestSuccessorSendIsOurs(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", false, 3)
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"live":true,"routes":{%q:{"successor":%q},%q:{"predecessor":%q}}}`, hl.DomainGnoTest, testRoute, testSucc, testSucc, testRoute)
	from := func(pkg string) *gnoMsg {
		sender, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(pkg))
		return &gnoMsg{Msg: hl.Message{Version: 3, Origin: hl.DomainGnoTest, Sender: sender, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, 500, nil)}}
	}
	r.resetStep()
	r.readLines(ctx)
	if k, rt := r.classify(from(testSucc)); k != kindRoute || rt == nil || rt.Symbol != "BUBBLE" {
		t.Fatalf("a send through the successor: kind %d %v", k, rt)
	}
	if k, _ := r.classify(from(testRoute)); k != kindRoute {
		t.Fatal("a send through the configured realm")
	}
	if k, _ := r.classify(from("gno.land/r/test/stranger")); k != kindOther {
		t.Fatal("a stranger's send was ours")
	}
	// the line is read once a step: before the read, a send from anyone but the configured realm is held, never
	// "other" (G46: a failed read left the successor's send walked past for good)
	r.resetStep()
	if k, _ := r.classify(from(testSucc)); k != kindLineUnread {
		t.Fatalf("the successor's send before the step's read: kind %d, want it held", k)
	}
	if k, _ := r.classify(from(testRoute)); k != kindRoute {
		t.Fatal("the configured realm's send before the step's read")
	}
	// the enrolled router against the line: the successor's address is fine, a stranger's or none alarms
	succAddr, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testSucc))
	stranger, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress("gno.land/r/test/stranger"))
	var state []byte
	r.solData = func(context.Context, solana.PublicKey) ([]byte, error) { return state, nil }
	r.readLines(ctx)
	state = tokenStateBytes(hl.DomainGnoTest, &succAddr)
	r.checkRouters(ctx, hl.DomainGnoTest)
	if _, ok := r.Alarm.Standing()["router:unknown:BUBBLE"]; ok {
		t.Fatalf("the successor as the enrolled router alarmed: %v", r.Alarm.Standing())
	}
	state = tokenStateBytes(hl.DomainGnoTest, &stranger)
	r.checkRouters(ctx, hl.DomainGnoTest)
	if a, ok := r.Alarm.Standing()["router:unknown:BUBBLE"]; !ok || !strings.Contains(a, "is no realm of the configured route's line") {
		t.Fatalf("a stranger as the enrolled router did not alarm: %v", r.Alarm.Standing())
	}
	state = tokenStateBytes(hl.DomainGnoTest, nil)
	r.checkRouters(ctx, hl.DomainGnoTest)
	if a, ok := r.Alarm.Standing()["router:unknown:BUBBLE"]; !ok || !strings.Contains(a, "enrolls no router") {
		t.Fatalf("no enrolled router did not alarm: %v", r.Alarm.Standing())
	}
	state = tokenStateBytes(hl.DomainGnoTest, &succAddr)
	r.checkRouters(ctx, hl.DomainGnoTest)
	if _, ok := r.Alarm.Standing()["router:unknown:BUBBLE"]; ok {
		t.Fatal("the alarm stands after the router is of the line again")
	}
}

// G39: a decision copy addressed to a retired program of the line is ended "retired" at the gate whatever its
// number — no attempt, the same alarm and record as an attempt that answered Retired — so the walk moves past
// it and the Reemit rule leaves it; a copy to the live program passes the gate. G45: a park over the live day
// cap, under the maximum, is held from the hourly staging as one over the maximum is.
func TestRetiredCopyEndsAtTheGate(t *testing.T) {
	a := solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	b := solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	f := newFake()
	r := testRelayer(f, "primary")
	r.admins = &adminChain{Live: sol.Admin{Program: b}, Retired: []retiredAdmin{{Admin: sol.Admin{Program: a}, State: &sol.AdminState{LastSeq: 5}}}}
	gov := [32]byte{7}
	msg := func(n uint32, rcpt solana.PublicKey, seq uint64) todo {
		m := hl.Message{Version: 3, Nonce: n, Origin: hl.DomainGnoTest, Sender: gov, Destination: hl.DomainSolanaTestnet, Recipient: [32]byte(rcpt), Body: sol.DecisionBody(sol.KindSetGuardians, seq, make([]byte, 33))}
		return todo{n: n, msg: &gnoMsg{Raw: m.Bytes(), Msg: m, ID: hl.ID(m.Bytes())}, kind: kindDecision, seq: seq}
	}
	// L+2 to the retired program: ended at the gate, alarmed, its copy remembered; never "waits"
	t7 := msg(7, a, 7)
	r.rememberCopy(7, 7, t7.msg.ID)
	if !r.retireCopy(t7, "deliver:7", "7", a) || r.state.Done["7"] != "retired" {
		t.Fatalf("a copy to the retired program was not ended: %q", r.state.Done["7"])
	}
	if al, ok := r.Alarm.Standing()["decision:retired:7"]; !ok || !strings.Contains(al, "never attempted") || !strings.Contains(al, "set decision recipient to the live program "+b.String()) {
		t.Fatalf("the retired alarm: %v", r.Alarm.Standing())
	}
	if len(r.state.DecisionCopies["7"]) != 1 || r.state.DecisionCopies["7"][0].Nonce != 7 {
		t.Fatalf("the copy's record: %+v", r.state.DecisionCopies)
	}
	if !doneForReemit(r.state.Done["7"]) {
		t.Fatal("a retired copy is re-emitted")
	}
	// the re-dispatch to the live program passes the gate and ends the alarm
	t9 := msg(9, b, 7)
	r.rememberCopy(7, 9, t9.msg.ID)
	if r.retireCopy(t9, "deliver:9", "9", b) || r.state.Done["9"] != "" {
		t.Fatal("a copy to the live program was ended as retired")
	}
	if _, ok := r.Alarm.Standing()["decision:retired:7"]; ok {
		t.Fatal("the retired alarm stands after the re-dispatch")
	}
	// G45: the hourly staging gate reads both limits
	l := sol.MintLimit{MaxPerMessage: 25_000, DayCap: 10_000}
	if !overLiveLimits(15_000, l) || !overLiveLimits(30_000, l) || overLiveLimits(9_000, l) {
		t.Fatal("the live-limits gate does not read both limits")
	}
}

// strokeRPC is a JSON-RPC server for a one-stroke flag: the admin state from a table (a missing account is
// null), a blockhash, a sent transaction recorded and confirmed.
type strokeRPC struct {
	accounts map[string][]byte // key → the admin state's bytes (owner: the program the key is the state of)
	owners   map[string]string
	sent     [][]byte // the transactions sent, decoded from base64
	failed   bool     // every sent transaction's status carries an error (G50: Send returns the signature with the failure)
}

func (s *strokeRPC) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "getAccountInfo":
			var value any
			if data, ok := s.accounts[fmt.Sprint(req.Params[0])]; ok {
				value = map[string]any{"data": []string{base64.StdEncoding.EncodeToString(data), "base64"}, "executable": false, "lamports": 1000, "owner": s.owners[fmt.Sprint(req.Params[0])], "rentEpoch": 0, "space": len(data)}
			}
			result = map[string]any{"context": map[string]any{"slot": 7}, "value": value}
		case "getLatestBlockhash":
			result = map[string]any{"context": map[string]any{"slot": 7}, "value": map[string]any{"blockhash": solana.Hash{1}.String(), "lastValidBlockHeight": 100}}
		case "sendTransaction":
			raw, _ := base64.StdEncoding.DecodeString(fmt.Sprint(req.Params[0]))
			s.sent = append(s.sent, raw)
			result = solana.Signature{byte(len(s.sent))}.String()
		case "getSignatureStatuses":
			var txErr any
			if s.failed {
				txErr = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 6}}}
			}
			result = map[string]any{"context": map[string]any{"slot": 7}, "value": []any{map[string]any{"slot": 7, "confirmations": 1, "confirmationStatus": "confirmed", "err": txErr}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// programsOf are the program ids a sent transaction's instructions name.
func programsOf(t *testing.T, raw []byte) []solana.PublicKey {
	t.Helper()
	tx, err := solana.TransactionFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out []solana.PublicKey
	for _, ix := range tx.Message.Instructions {
		p, err := tx.Message.Program(ix.ProgramIDIndex)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// G40: the one-stroke flag lands on the pausable ISM and the configured admin program when the admin program's
// line cannot be read — a state naming itself as successor, here — and the line's error comes back beside the
// signature; with a readable line it lands on the live program as before. Reads fail closed; the pausable half
// never.
func TestStrokeLandsWithoutTheLine(t *testing.T) {
	cfg := &Config{}
	cfg.Solana.Client, cfg.Solana.Pausable, cfg.Solana.Admin = "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU", "9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr", "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	progs := cfg.Programs()
	admin := progs.AdminProgram()
	key := solana.NewWallet()
	seats := []solana.PublicKey{key.PublicKey()}
	// the configured program names itself as its successor: a line no build follows
	self := admin.Program
	rpcFake := &strokeRPC{accounts: map[string][]byte{admin.State().String(): adminStateBytes(seats, &self, solana.PublicKey{}, 0)}, owners: map[string]string{admin.State().String(): admin.Program.String()}}
	srv := rpcFake.serve(t)
	c := sol.NewClient(srv.URL, key.PrivateKey)
	sig, err := FlagSolana(context.Background(), c, progs, true)
	if !LineUnreadAfterStroke(sig, err) || !strings.Contains(err.Error(), "names itself as its successor") || !strings.Contains(err.Error(), "the stroke landed on the pausable ISM and the configured admin program") {
		t.Fatalf("the stroke without the line: %s %v", sig, err)
	}
	if len(rpcFake.sent) != 1 {
		t.Fatalf("%d transactions sent, want the stroke alone", len(rpcFake.sent))
	}
	if ps := programsOf(t, rpcFake.sent[0]); len(ps) != 2 || !ps[0].Equals(progs.Pausable) || !ps[1].Equals(admin.Program) {
		t.Fatalf("the stroke's programs: %v", ps)
	}
	// a readable line: the live successor is struck, the retired one too (its seat), no error
	succ := solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	rpcFake.accounts[admin.State().String()] = adminStateBytes(seats, &succ, solana.PublicKey{}, 0)
	rpcFake.accounts[sol.Admin{Program: succ}.State().String()] = adminStateBytes(seats, nil, admin.Program, 1)
	rpcFake.owners[sol.Admin{Program: succ}.State().String()] = succ.String()
	rpcFake.sent = nil
	sig, err = FlagSolana(context.Background(), c, progs, true)
	if err != nil || sig.IsZero() || LineUnreadAfterStroke(sig, err) {
		t.Fatalf("the stroke with the line: %s %v", sig, err)
	}
	if len(rpcFake.sent) != 2 {
		t.Fatalf("%d transactions sent, want the stroke and the retired program's", len(rpcFake.sent))
	}
	if ps := programsOf(t, rpcFake.sent[0]); len(ps) != 2 || !ps[0].Equals(progs.Pausable) || !ps[1].Equals(succ) {
		t.Fatalf("the stroke's programs with the line: %v", ps)
	}
	if ps := programsOf(t, rpcFake.sent[1]); len(ps) != 1 || !ps[0].Equals(admin.Program) {
		t.Fatalf("the retired program's stroke: %v", ps)
	}
	// G50: a stroke that failed on chain comes back with its signature and the failure — never read as the
	// line's failure beside a landed stroke (the holder would read "set" over a flag that is not there)
	rpcFake.failed = true
	rpcFake.sent = nil
	sig, err = FlagSolana(context.Background(), c, progs, true)
	if err == nil || sig.IsZero() || LineUnreadAfterStroke(sig, err) || !strings.Contains(err.Error(), "transaction "+sig.String()+" failed") {
		t.Fatalf("a failed stroke with a readable line: %s %v (line unread %v)", sig, err, LineUnreadAfterStroke(sig, err))
	}
	// a failed stroke under an unreadable line: the failure first, the line's error beside it, still not "landed"
	rpcFake.accounts[admin.State().String()] = adminStateBytes(seats, &self, solana.PublicKey{}, 0)
	sig, err = FlagSolana(context.Background(), c, progs, true)
	if err == nil || LineUnreadAfterStroke(sig, err) || !strings.Contains(err.Error(), "failed") || !strings.Contains(err.Error(), "names itself as its successor") {
		t.Fatalf("a failed stroke under an unreadable line: %s %v", sig, err)
	}
}

// benc is a borsh encoder for the tests' Solana fixtures.
type benc struct{ b []byte }

func (e *benc) u8(v byte)        { e.b = append(e.b, v) }
func (e *benc) u16(v uint16)     { e.b = append(e.b, byte(v), byte(v>>8)) }
func (e *benc) u32(v uint32)     { e.b = append(e.b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24)) }
func (e *benc) u64(v uint64)     { e.u32(uint32(v)); e.u32(uint32(v >> 32)) }
func (e *benc) i64(v int64)      { e.u64(uint64(v)) }
func (e *benc) i32(v int32)      { e.u32(uint32(v)) }
func (e *benc) arr32(v [32]byte) { e.b = append(e.b, v[:]...) }
func (e *benc) bytes(v []byte)   { e.u32(uint32(len(v))); e.b = append(e.b, v...) }
func (e *benc) str(s string)     { e.bytes([]byte(s)) }

// stageAccountBytes is a staged update's account as ParseStage reads it (the S-16 layout): the header's hash,
// height and validators' hash are what the record's seed keeps.
func stageAccountBytes(headerHash [32]byte, height int64, vals []byte) []byte {
	var e benc
	e.u8(1)
	e.u8(sol.StageKindUpdate)
	e.u8(254)
	e.arr32([32]byte{9}) // the submitter
	e.u64(500)
	e.i64(1_800_000_000)
	e.arr32(headerHash) // the key
	e.u8(0)             // not by ring
	e.arr32(headerHash)
	// the header
	e.str("v1")
	e.str("dev")
	e.i64(height)
	e.i64(1_800_000_000)
	e.i32(0)
	e.i64(0)
	e.i64(0)
	e.str("")
	e.bytes(nil) // the last block id: hash, parts
	e.u32(0)
	e.bytes(nil)
	for i := 0; i < 2; i++ { // last commit hash, data hash
		e.bytes(nil)
	}
	e.bytes(vals)             // validators hash
	e.bytes(make([]byte, 32)) // next validators hash
	for i := 0; i < 3; i++ {  // consensus, app, last results
		e.bytes(nil)
	}
	e.str("")
	e.bytes(nil) // the block id
	e.u32(0)
	e.bytes(nil)
	e.i64(0)             // the round
	e.arr32([32]byte{4}) // the set hash
	e.u16(0)
	e.u32(0)     // no precommits
	e.bytes(nil) // filled, verified
	e.bytes(nil)
	e.u32(0) // no next set
	e.u16(0)
	e.arr32([32]byte{})
	e.u8(0) // no proof
	e.u32(0)
	e.u32(0)
	e.u64(3) // the ring copy (S-16)
	e.i64(0)
	e.arr32([32]byte{})
	return e.b
}

// hashNode is a gno node whose block at any height has the hash given ("" for none).
type hashNode struct {
	fakeChain
	hash string
}

func (n *hashNode) Block(_ context.Context, h int64) (gno.Head, error) {
	if n.hash == "" {
		return gno.Head{}, errors.New("no such height")
	}
	return gno.Head{Height: h, Hash: n.hash}, nil
}

// checkpointSol serves a checkpoint's Solana reads: a program's accounts and an address's signatures.
type checkpointSol struct {
	fakeSol
	accounts rpc.GetProgramAccountsResult
	sigs     map[solana.PublicKey][]*rpc.TransactionSignature // newest first
}

func (c *checkpointSol) GetProgramAccountsWithOpts(context.Context, solana.PublicKey, *rpc.GetProgramAccountsOpts) (rpc.GetProgramAccountsResult, error) {
	return c.accounts, nil
}

func (c *checkpointSol) GetSignaturesForAddressWithOpts(_ context.Context, addr solana.PublicKey, opts *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error) {
	sigs := c.sigs[addr]
	if !opts.Before.IsZero() {
		for i, s := range sigs {
			if s.Signature.Equals(opts.Before) {
				sigs = sigs[i+1:]
				break
			}
		}
	}
	if opts.Limit != nil && len(sigs) > *opts.Limit {
		sigs = sigs[:*opts.Limit]
	}
	return sigs, nil
}

// programRing is the client's ring as the program keeps it: a fixed array written at ring_next, the oldest entry
// overwritten once full (state.rs:154-172).
type programRing struct {
	slots [][32]byte
	next  int
	cap   int
}

func (p *programRing) push(h [32]byte) {
	if len(p.slots) < p.cap {
		p.slots = append(p.slots, h)
		p.next = len(p.slots) % p.cap
		return
	}
	p.slots[p.next] = h
	p.next = (p.next + 1) % p.cap
}

func (p *programRing) has(h [32]byte) bool {
	for _, s := range p.slots {
		if s == h {
			return true
		}
	}
	return false
}

// F21: a checkpoint over a wrapped ring takes the ring in the program's eviction order, so the mirror and the
// program hold the same headers through 300 more pushes; in the account's slot order the mirror evicts the wrong
// entries for the next 256.
func TestCheckpointRingFollowsTheProgramsEviction(t *testing.T) {
	hash := func(i int) [32]byte {
		var h [32]byte
		h[0], h[1], h[2] = byte(i), byte(i>>8), 0xf
		return h
	}
	// the program's ring after 300 pushes: full and wrapped (ring_next 44)
	prog := &programRing{cap: sol.Ring}
	for i := 0; i < 300; i++ {
		prog.push(hash(i))
	}
	if prog.next != 44 {
		t.Fatalf("the program's ring_next: %d", prog.next)
	}
	// the account as read: the slots in order, the head's count and next
	cs := &clientState{Head: &sol.ClientHead{RingCount: uint16(sol.Ring), RingNext: uint16(prog.next)}, Body: &sol.ClientBody{}, Len: sol.HeadLen + 0 + sol.Ring*sol.RingEntryLen}
	for _, s := range prog.slots {
		cs.Ring = append(cs.Ring, sol.RingEntry{Hash: s})
	}
	if sol.RingCapacity(cs.Head, cs.Len) != sol.Ring {
		t.Fatalf("the test's capacity: %d", sol.RingCapacity(cs.Head, cs.Len))
	}
	ordered := ringInOrder(cs)
	if len(ordered) != sol.Ring || ordered[0] != hash(44) || ordered[sol.Ring-1] != hash(299) {
		t.Fatalf("the ring in order: first %x, last %x", ordered[0][:3], ordered[len(ordered)-1][:3])
	}
	var mirror, naive ClientRecord
	ring := [][32]byte{}
	for _, s := range prog.slots {
		ring = append(ring, s)
	}
	if err := mirror.StartFrom(300, [32]byte{}, 1000, ordered, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := naive.StartFrom(300, [32]byte{}, 1000, ring, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	diverged := false
	for i := 300; i < 600; i++ {
		h := hash(i)
		prog.push(h)
		mirror.ringPush(hex.EncodeToString(h[:]))
		naive.ringPush(hex.EncodeToString(h[:]))
		for _, s := range prog.slots {
			if !mirror.ringHas(hex.EncodeToString(s[:])) {
				t.Fatalf("after push %d the mirror lacks %x, which the program holds", i, s[:3])
			}
			if !naive.ringHas(hex.EncodeToString(s[:])) {
				diverged = true
			}
		}
		if len(mirror.Ring) != sol.Ring {
			t.Fatalf("the mirror holds %d entries", len(mirror.Ring))
		}
	}
	if !diverged {
		t.Fatal("the slot-order mirror never diverged from the program (the test's premise)")
	}
	// a ring not yet full is in slot order already: no rotation
	part := &clientState{Head: &sol.ClientHead{RingCount: 3, RingNext: 3}, Body: &sol.ClientBody{}, Len: sol.HeadLen + sol.Ring*sol.RingEntryLen, Ring: []sol.RingEntry{{Hash: hash(0)}, {Hash: hash(1)}, {Hash: hash(2)}}}
	if o := ringInOrder(part); len(o) != 3 || o[0] != hash(0) {
		t.Fatalf("a ring not yet full was rotated: %x", o[0][:3])
	}
	// F29: between a growth's resize and its relocation the account holds room for more than 256 entries (398 for
	// one 10,240-byte step) while the program's ring is still 256 (Ring::push caps at RING): the mirror rotates
	// at 256 whatever the account holds
	grown := &clientState{Head: cs.Head, Body: cs.Body, Ring: cs.Ring, Len: sol.HeadLen + 398*sol.RingEntryLen}
	if sol.RingCapacity(grown.Head, grown.Len) != 398 {
		t.Fatalf("the test's mid-growth capacity: %d", sol.RingCapacity(grown.Head, grown.Len))
	}
	if o := ringInOrder(grown); len(o) != sol.Ring || o[0] != hash(44) || o[sol.Ring-1] != hash(299) {
		t.Fatalf("a full ring mid-growth was not rotated at 256: first %x, last %x", o[0][:3], o[len(o)-1][:3])
	}
}

// G37, G36, G45: a checkpoint is refused when the client's trusted header differs from the own node's block at
// its height (the mismatch names the slot as the cut); taken, it seeds the open stages from the client's
// accounts and the index's cursors at the checkpoint's slot; a null transaction answer is a retention skip
// retried hourly, not every poll.
func TestCheckpointChecksTheHeaderAndSeedsTheIndex(t *testing.T) {
	ctx := context.Background()
	trusted := [32]byte{0xab, 0xcd}
	cs := &clientState{Head: &sol.ClientHead{TrustedHeight: 100, TrustedHash: trusted}, Body: &sol.ClientBody{Records: 7, RunningHash: [32]byte{1}, SetHash: [32]byte{2}}, Slot: 5000}
	// a forked trusted header: refused, the cut at the checkpoint's slot
	var rec ClientRecord
	mm, err := checkpointBase(ctx, &hashNode{hash: "deadbeef"}, cs, &rec)
	if err != nil || mm == nil || mm.Height != 100 || mm.Slot != 5000 || rec.Count != 0 {
		t.Fatalf("a forked trusted header took a base: %+v %v (count %d)", mm, err, rec.Count)
	}
	if text := checkpointMismatchText(mm); !strings.Contains(text, "HEADER MISMATCH AT THE CHECKPOINT") || !strings.Contains(text, "the cut is slot 5000") {
		t.Fatalf("the mismatch's text: %s", text)
	}
	// a node with no block at the height: an error, no base
	if _, err := checkpointBase(ctx, &hashNode{}, cs, &rec); err == nil || rec.Count != 0 {
		t.Fatal("a node without the block took a base")
	}
	// the header agrees: the base is taken
	mm, err = checkpointBase(ctx, &hashNode{hash: hex.EncodeToString(trusted[:])}, cs, &rec)
	if err != nil || mm != nil || rec.Count != 7 || rec.FromSlot != 5000 {
		t.Fatalf("an agreeing header: %+v %v (count %d)", mm, err, rec.Count)
	}
	// the open stages from the client's accounts, whatever their age
	cfg := &Config{}
	cfg.Solana.Client, cfg.Solana.Pausable, cfg.Solana.Admin = "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU", "9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr", "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	token := solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute, Token: token.String()}}
	client := cfg.Programs().LightClient()
	stagePDA := client.UpdateStage(solana.PublicKey{9}, [32]byte{0x55})
	vals := make([]byte, 32)
	vals[0] = 0x77
	reader := &checkpointSol{accounts: rpc.GetProgramAccountsResult{
		{Pubkey: client.State(), Account: &rpc.Account{Owner: client.Program, Data: rpc.DataBytesOrJSONFromBytes([]byte{1, 2, 3})}},
		{Pubkey: stagePDA, Account: &rpc.Account{Owner: client.Program, Data: rpc.DataBytesOrJSONFromBytes(append(stageAccountBytes([32]byte{0x55}, 120, vals), 'l', 'e', 'a', 'f'))}},
		{Pubkey: solana.PublicKey{3}, Account: &rpc.Account{Owner: client.Program, Data: rpc.DataBytesOrJSONFromBytes([]byte{0})}}, // not a stage
	}}
	n, err := seedStages(ctx, reader, client, &rec)
	if err != nil || n != 1 {
		t.Fatalf("the stages seeded: %d %v", n, err)
	}
	if ref, ok := rec.Stages[stagePDA.String()]; !ok || ref.Height != 120 || ref.Hash != hex.EncodeToString([]byte{0x55})+strings.Repeat("00", 31) || ref.Vals != hex.EncodeToString(vals) {
		t.Fatalf("the seeded stage: %+v", rec.Stages)
	}
	if n, err := seedStages(ctx, &fakeSol{}, client, &rec); err != nil || n != 0 {
		t.Fatalf("a reader without program accounts: %d %v", n, err)
	}
	// the index's cursors: the newest signature at or below the slot on each provider, an empty cursor only
	sigs := []*rpc.TransactionSignature{}
	for i := 0; i < 2500; i++ { // newest first: slots 7499 down to 5000
		sigs = append(sigs, &rpc.TransactionSignature{Signature: solana.Signature{byte(i), byte(i >> 8), 1}, Slot: uint64(7499 - i)})
	}
	reader.sigs = map[solana.PublicKey][]*rpc.TransactionSignature{token: sigs}
	cursors, through := map[string]string{"BUBBLE#1": "kept"}, map[string]uint64{}
	n, err = seedCursors(ctx, [2]solReader{reader, reader}, cfg, 5000, cursors, through)
	if err != nil || n != 1 || cursors["BUBBLE#0"] != sigs[2499].Signature.String() || cursors["BUBBLE#1"] != "kept" {
		t.Fatalf("the cursors: %d %v %v", n, err, cursors)
	}
	// each seeded cursor is read through the checkpoint's slot (lane s7, E6-SF1); a kept one keeps its own
	if through["BUBBLE#0"] != 5000 || through["BUBBLE#1"] != 0 {
		t.Fatalf("the slots read through: %v", through)
	}
	// every signature above the slot: no cursor (the whole history is after the checkpoint), read through the slot
	cursors, through = map[string]string{}, map[string]uint64{}
	if n, err := seedCursors(ctx, [2]solReader{reader, reader}, cfg, 4000, cursors, through); err != nil || n != 0 || len(cursors) != 0 || through["BUBBLE#0"] != 4000 {
		t.Fatalf("cursors with nothing at or below the slot: %d %v %v %v", n, err, cursors, through)
	}
	// a null transaction answer is tried again at once nullRetries times (lane s6: a backend behind the listing), then a
	// retention skip, retried after its hour
	now := time.Unix(1_800_000_000, 0)
	old := solana.Signature{5}.String()
	if first := skipFor(old, 10, rpc.ErrNotFound, now, 1); first.Retry != 0 || strings.Contains(first.Err, "past the provider's retention") {
		t.Fatalf("a first null answer was taken for the retention: %+v", first)
	}
	sk := skipFor(old, 10, rpc.ErrNotFound, now, nullRetries+1)
	if sk.Retry != now.Add(time.Hour).Unix() || !strings.Contains(sk.Err, "past the provider's retention") {
		t.Fatalf("the retention skip: %+v", sk)
	}
	if other := skipFor(old, 10, errors.New("timeout"), now, 1); other.Retry != 0 {
		t.Fatalf("a transient skip got a retry hour: %+v", other)
	}
	mailbox := sol.Mailbox{Program: cfg.SolMailbox()}
	kept := []scanSkip{sk}
	if _, keep := retrySkipped(ctx, &fakeSol{txs: map[string]*rpc.GetTransactionResult{}}, mailbox, token, 1, 2, kept, now.Add(30*time.Minute)); len(keep) != 1 || keep[0].Tries != nullRetries+1 {
		t.Fatalf("a retention skip was read inside its hour: %+v", keep)
	}
	if _, keep := retrySkipped(ctx, &fakeSol{txs: map[string]*rpc.GetTransactionResult{}}, mailbox, token, 1, 2, kept, now.Add(2*time.Hour)); len(keep) != 1 || keep[0].Tries != nullRetries+2 {
		t.Fatalf("a retention skip was not read after its hour: %+v", keep)
	}
	if text := skipText(append(kept, scanSkip{Sig: "9abc", Slot: 11, Err: "timeout", Tries: 3})); !strings.Contains(text, "1 past the provider's retention") || !strings.Contains(text, "9abc (slot 11, 3 tries: timeout)") {
		t.Fatalf("the skip text: %s", text)
	}
}

// G38 (plan v35): a 7-day decision gone inside its week that no later cancel of r/hlgov's names is a foreign
// finding — no validators' cancel exists, so no window or hold excuses it; a number at or below a retired
// program's last is judged on that
// program's accounts, so a fresh install after a kind 9 does not call the predecessor's pending decisions gone;
// the monitor alarms a finding first and flags it after the cut's patience, the numbers advancing with the flag.
func TestGoneDecisionIsForeign(t *testing.T) {
	f := newFake()
	cfg := &Config{}
	cfg.Gno.Gov = "gno.land/r/test/hlgov"
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute}}
	admin := sol.Admin{Program: solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")}
	retired := sol.Admin{Program: solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")}
	none := &fakeAccounts{accounts: map[solana.PublicKey]*rpc.Account{}}
	params := append([]byte{1}, make([]byte, 32)...)
	at := int64(1_800_000_000)
	f.renders[cfg.Gno.Gov+":decision/3"] = fmt.Sprintf(`{"number":3,"kind":%d,"body":"%s","id":"x","count":1,"at":%d}`, sol.KindSetGuardians, hex.EncodeToString(sol.DecisionBody(sol.KindSetGuardians, 3, params)), at)
	st := &sol.AdminState{ClockFactor: 1}
	run := func(fc foreignCtx) []string {
		t.Helper()
		findings, checked, err := foreignFindings(context.Background(), none, f, cfg, admin, st, 100, 2, 3, 3, at+3600, fc)
		if err != nil || checked != 3 {
			t.Fatalf("the walk: %d %v", checked, err)
		}
		return findings
	}
	if fs := run(foreignCtx{}); len(fs) != 1 || !strings.Contains(fs[0], "foreign decision: number 3's decision (kind 5) is gone though r/hlgov's matures in 7 days") {
		t.Fatalf("a gone decision: %v", fs)
	}
	// a number at or below a retired program's last is read on that program: its state holds the cancelled
	// range, so nothing is gone; the live program's state alone would call it foreign
	fc := foreignCtx{Retired: []retiredAdmin{{Admin: retired, State: &sol.AdminState{ClockFactor: 1, LastSeq: 3, Cancelled: []sol.CancelledRange{{From: 3, To: 3}}}}}}
	if fs := run(fc); len(fs) != 0 {
		t.Fatalf("a predecessor's number was judged on the live program: %v", fs)
	}
	// the monitor: the pending alarm first, the flag after the cut's patience
	m := testMonitor(f)
	m.seat = 5
	finding := "foreign decision: number 3's decision is gone"
	m.applyForeign(context.Background(), []string{finding}, 3, nil)
	if _, ok := m.Alarm.Standing()["decision:foreign:pending"]; !ok || m.state.DecisionsChecked != 0 {
		t.Fatalf("the first sighting: %v checked %d", m.Alarm.Standing(), m.state.DecisionsChecked)
	}
	later := m.now().Add(cutFailAfter + time.Second)
	m.now = func() time.Time { return later }
	m.applyForeign(context.Background(), []string{finding}, 3, nil)
	if _, ok := m.Alarm.Standing()["decision:foreign"]; !ok || m.state.DecisionsChecked != 3 {
		t.Fatalf("the flag after the patience: %v checked %d", m.Alarm.Standing(), m.state.DecisionsChecked)
	}
	if ctxOf := foreignCtxOf(&adminChain{Live: admin, Retired: fc.Retired}); len(ctxOf.Retired) != 1 {
		t.Fatalf("the context from the line: %+v", ctxOf)
	}
}

// F24: running past the hop bounds is alarmed with its chore — the admin program's line unfollowed for two
// minutes (decision:line-unfollowed), a route's line of migrations unfollowed for two minutes (route:line) —
// and both clear with a read.
func TestUnfollowedLinesAlarm(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	ctx := context.Background()
	m.noteAdminLine(errors.New("the admin program's line of succession from X runs past 4 hops"))
	if _, ok := m.Alarm.Standing()["decision:line-unfollowed"]; ok {
		t.Fatal("alarmed at first sight")
	}
	now := m.now().Add(cutFailAfter)
	m.now = func() time.Time { return now }
	m.noteAdminLine(errors.New("the admin program's line of succession from X runs past 4 hops"))
	if a, ok := m.Alarm.Standing()["decision:line-unfollowed"]; !ok || !strings.Contains(a, "edit solana.admin in bridge.json to the live program") {
		t.Fatalf("no alarm after two minutes: %v", m.Alarm.Standing())
	}
	m.noteAdminLine(nil)
	if _, ok := m.Alarm.Standing()["decision:line-unfollowed"]; ok {
		t.Fatal("not cleared by a read")
	}
	// a route's line that loops
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, testRoute, false, 1)
	routes := map[string]*routeJSON{}
	routes["BUBBLE"], _ = routeAt(ctx, f, testRoute, 100)
	m.readMigrations(ctx, routes, mailboxWith(), 100)
	if _, ok := m.Alarm.Standing()["route:line:BUBBLE"]; ok {
		t.Fatal("the route line alarmed at first sight")
	}
	now = now.Add(cutFailAfter)
	m.readMigrations(ctx, routes, mailboxWith(), 100)
	if a, ok := m.Alarm.Standing()["route:line:BUBBLE"]; !ok || !strings.Contains(a, "loops") || !strings.Contains(a, "edit routes[].gno in bridge.json") {
		t.Fatalf("no alarm for the looping line: %v", m.Alarm.Standing())
	}
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", false, 1)
	m.readMigrations(ctx, routes, mailboxWith(), 100)
	if _, ok := m.Alarm.Standing()["route:line:BUBBLE"]; ok {
		t.Fatal("the route line alarm stands after a read")
	}
}

// K35: the signers judge the identity over the token's line as the watch does — the predecessor's 0 and the
// successor's lock summed from page 0 — and a successor the config names before page 0 is found back to its
// predecessor through the mailbox; a partner that cannot be read is no verdict.
func TestSignerJudgesOverTheLine(t *testing.T) {
	f := newFake()
	ctx := context.Background()
	cfg := &Config{}
	cfg.Gno.Mailbox = testMailbox
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute, Token: "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"}}
	snap := &supplySnapshot{Slot: 7, Supplies: map[string]int64{"BUBBLE": 1_000}}
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"live":true,"routes":{%q:{"successor":%q},%q:{"predecessor":%q}}}`, hl.DomainGnoTest, testRoute, testSucc, testSucc, testRoute)
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(1_000, 0, "", true, 1)
	mb, err := mailboxAt(ctx, f, testMailbox, 100)
	if err != nil {
		t.Fatal(err)
	}
	ids, _, _, err := signerIdentities(ctx, f, cfg, 100, snap, mb)
	if err != nil || ids["BUBBLE"] == nil || ids["BUBBLE"].Lock != 1_000 {
		t.Fatalf("the signer's identity over the line: %+v %v", ids["BUBBLE"], err)
	}
	if v, _ := ids["BUBBLE"].Verdict(); v != Holds || ids["BUBBLE"].SignerStops() {
		t.Fatalf("the signer stops on a held line: %s", v)
	}
	// the configured successor before page 0: the lock still on the predecessor, found through the mailbox
	cfg.Routes[0].Gno = testSucc
	f.renders[testRoute+":json"] = routeJSONWith(1_000, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(0, 0, "", false, 0)
	ids, _, _, err = signerIdentities(ctx, f, cfg, 100, snap, mb)
	if err != nil || ids["BUBBLE"].Lock != 1_000 || ids["BUBBLE"].SignerStops() {
		t.Fatalf("a configured successor before page 0: %+v %v", ids["BUBBLE"], err)
	}
	// a partner that cannot be read: no verdict
	delete(f.renders, testRoute+":json")
	if _, _, _, err := signerIdentities(ctx, f, cfg, 100, snap, mb); err == nil {
		t.Fatal("an unread partner gave a verdict")
	}
}

// M1h's shapes (AR1, AR2): the mailbox's routes[path] carries `enrolled` (the height) and `enrolledAt` (the
// time) and the view takes both; a route's json carries `writtenOff` and `writtenOffTotal`; a token's line
// re-pointed before page 0 (the first successor leaving the mailbox's routes) is alarmed once by the monitor,
// which re-reads the line each poll.
func TestM1hShapesAreRead(t *testing.T) {
	f := newFake()
	ctx := context.Background()
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"live":true,"routes":{%q:{"enrolled":120,"enrolledAt":1800000500,"predecessor":%q},%q:{"enrolled":0,"enrolledAt":0,"successor":%q}}}`, hl.DomainGnoTest, testSucc, testRoute, testRoute, testSucc)
	mb, err := ReadMailbox(ctx, f, testMailbox, 100)
	if err != nil || mb.Enrolled[testSucc] != (Enrollment{Height: 120, At: 1_800_000_500}) || len(mb.Enrolled) != 1 {
		t.Fatalf("the enrolment read: %+v %v", mb.Enrolled, err)
	}
	f.renders[testRoute+":json"] = strings.Replace(routeJSONWith(1000, 0, "", false, 1), `"decisions":0`, `"refunded":2,"refundedTotal":300,"writtenOff":1,"writtenOffTotal":100,"decisions":0`, 1)
	rv, err := ReadRoute(ctx, f, Route{Symbol: "BUBBLE", Gno: testRoute}, 100)
	if err != nil || rv.WrittenOff != 1 || rv.WrittenOffTotal != 100 || rv.Refunded != 2 {
		t.Fatalf("the route view: %+v %v", rv, err)
	}
	// the line re-pointed: P → S at one poll, P → S2 at the next (S pulled nothing, the migrate re-pointed)
	m := testMonitor(f)
	f.renders[testRoute+":json"] = routeJSONWith(1000, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(0, 0, "", false, 0)
	routes := map[string]*routeJSON{}
	routes["BUBBLE"], _ = routeAt(ctx, f, testRoute, 100)
	m.readMigrations(ctx, routes, mailboxLineJSON(testRoute, testSucc), 100)
	if m.state.LineEnds["BUBBLE"] != testSucc {
		t.Fatalf("the line's end kept: %v", m.state.LineEnds)
	}
	if _, ok := m.Alarm.Standing()["route:successor-changed:BUBBLE"]; ok {
		t.Fatal("alarmed on the first read")
	}
	f.renders[testRoute+":json"] = routeJSONWith(1000, 0, testSucc2, false, 0)
	f.renders[testSucc2+":json"] = routeJSONWith(0, 0, "", false, 0)
	routes["BUBBLE"], _ = routeAt(ctx, f, testRoute, 101)
	m.readMigrations(ctx, routes, mailboxLineJSON(testRoute, testSucc2), 101)
	if a, ok := m.Alarm.Standing()["route:successor-changed:BUBBLE"]; !ok || !strings.Contains(a, "was "+testSucc+", now "+testSucc2) {
		t.Fatalf("the re-pointed line was not alarmed: %v", m.Alarm.Standing())
	}
	m.readMigrations(ctx, routes, mailboxLineJSON(testRoute, testSucc2), 102)
	if _, ok := m.Alarm.Standing()["route:successor-changed:BUBBLE"]; ok {
		t.Fatal("the change alarm stands past its poll")
	}
}

// countingChain counts a fake node's renders by their page ("msg", "send", "json", …).
type countingChain struct {
	*fakeChain
	renders map[string]int
}

func (c *countingChain) RenderAt(ctx context.Context, pkg, path string, h int64) (string, error) {
	page := path
	if i := strings.Index(path, "/"); i > 0 {
		page = path[:i]
	}
	c.renders[page]++
	return c.fakeChain.RenderAt(ctx, pkg, path, h)
}

// countingSol counts GetMultipleAccounts calls and answers the processed records it was told of.
type countingSol struct {
	notFoundSol
	multi     int
	owner     solana.PublicKey
	processed map[solana.PublicKey]bool
}

func (s *countingSol) GetMultipleAccountsWithOpts(_ context.Context, keys []solana.PublicKey, _ *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error) {
	s.multi++
	out := make([]*rpc.Account, len(keys))
	for i, k := range keys {
		if s.processed[k] {
			out[i] = &rpc.Account{Owner: s.owner}
		}
	}
	return &rpc.GetMultipleAccountsResult{Value: out}, nil
}

// F23, G42: the monitors' reads under a backlog are bounded — a message page is read once (its id kept), the
// processed records come by 100 through the pair, and a refund mark is read only where one can be set (a
// successor or migrated route, never a live one), once per send and again only when the token's refund count
// rose; the relayer reads no mark for a live route's send.
func TestSendReadsAreBoundedUnderABacklog(t *testing.T) {
	const N = 250
	f := newFake()
	chain := &countingChain{fakeChain: f, renders: map[string]int{}}
	cfg := &Config{}
	cfg.Gno.Mailbox = testMailbox
	cfg.Solana.Domain = hl.DomainSolanaTestnet
	cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	token := [32]byte{9}
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute, Token: hl.Base58(token[:])}}
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	mailbox := sol.Mailbox{Program: cfg.SolMailbox()}
	provider := &countingSol{owner: mailbox.Program, processed: map[solana.PublicKey]bool{}}
	var ids [][32]byte
	for n := uint32(0); n < N; n++ {
		m := hl.Message{Version: 3, Nonce: n, Origin: hl.DomainGnoTest, Sender: route, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, 5, nil)}
		f.renders[testMailbox+":msg/"+strconv.FormatUint(uint64(n), 10)] = msgRender(m, int64(50+n))
		id := hl.ID(m.Bytes())
		ids = append(ids, id)
		refunded := "0"
		if n == 7 {
			refunded = "1800000500"
		}
		f.renders[testRoute+":send/"+hex.EncodeToString(id[:])] = `{"id":"` + hex.EncodeToString(id[:]) + `","from":"g1a","amount":5,"at":1,"imported":true,"refunded":` + refunded + `}`
	}
	mb := &mailboxJSON{Domain: hl.DomainGnoTest, Count: N}
	sends := map[string]*sendRec{}
	var next uint32
	now := time.Unix(1_800_000_000, 0)
	live := map[string]*routeJSON{"BUBBLE": {Lock: 1000}}
	sr := sendReads{Routes: live, RefundCounts: map[string]int64{}}
	poll := func(what string, msg, send, multi int) {
		t.Helper()
		chain.renders = map[string]int{}
		provider.multi = 0
		if _, err := readSends(context.Background(), chain, provider, cfg, mb, 100, sends, &next, now, sr); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if chain.renders["msg"] != msg || chain.renders["send"] != send || provider.multi != multi {
			t.Fatalf("%s: %d message renders, %d mark renders, %d GetMultipleAccounts calls; want %d, %d, %d", what, chain.renders["msg"], chain.renders["send"], provider.multi, msg, send, multi)
		}
		now = now.Add(10 * time.Second)
	}
	poll("the first poll", N, 0, 25)  // every message once; a live route's marks never; the records by ten (sol.MultiMax)
	poll("the second poll", 0, 0, 25) // nothing re-read
	if len(sends) != N || sends["7"].Refunded {
		t.Fatalf("the sends: %d, #7 refunded %v", len(sends), sends["7"].Refunded)
	}
	// a processed record: found by the batch read, out of the next poll's batch
	provider.processed[mailbox.ProcessedMessage(ids[3])] = true
	poll("a delivery", 0, 0, 25)
	if !sends["3"].Processed {
		t.Fatal("the processed record was not read")
	}
	// the route a successor now: every mark read once, then not again while the count stands
	sr.Routes = map[string]*routeJSON{"BUBBLE": {Lock: 1000, Imported: 1}}
	poll("the successor's first poll", 0, N, 25)
	if !sends["7"].Refunded || sends["8"].Refunded {
		t.Fatalf("the marks: #7 %v, #8 %v", sends["7"].Refunded, sends["8"].Refunded)
	}
	poll("the successor's second poll", 0, 0, 25)
	// the count rises: every unrefunded mark read again (the processed send's too: a refund after a delivery
	// is a double mint, AR1), then not again
	sr.Routes["BUBBLE"].Refunded = 2
	f.renders[testRoute+":send/"+hex.EncodeToString(ids[3][:])] = `{"id":"x","from":"g1a","amount":5,"at":1,"imported":true,"refunded":1800000600}`
	poll("the count rose", 0, N-1, 25)
	if !sends["3"].Refunded || !sends["3"].Processed {
		t.Fatal("the processed send's mark was not read on the rise")
	}
	poll("after the rise", 0, 0, 25)
	if sr.RefundCounts["BUBBLE"] != 2 {
		t.Fatalf("the count kept: %v", sr.RefundCounts)
	}
	// the relayer reads no mark for a live route's send, and the successor's once it is about to stage
	r := testRelayer(f, "primary")
	r.Cfg = cfg
	r.gno = chain
	f.renders[testRoute+":json"] = routeJSONWith(1000, 0, "", false, 0)
	m7 := hl.Message{Version: 3, Nonce: 7, Origin: hl.DomainGnoTest, Sender: route, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, 5, nil)}
	chain.renders = map[string]int{}
	if refunded, _, err := r.sendRefunded(context.Background(), todo{n: 7, msg: &gnoMsg{Raw: m7.Bytes(), Msg: m7, ID: ids[7]}, rt: &cfg.Routes[0]}); err != nil || refunded || chain.renders["send"] != 0 {
		t.Fatalf("a live route's mark was read: %v %v %d", refunded, err, chain.renders["send"])
	}
	r.resetStep()
	f.renders[testRoute+":json"] = routeJSONWith(1000, 0, "", false, 1)
	if refunded, _, err := r.sendRefunded(context.Background(), todo{n: 7, msg: &gnoMsg{Raw: m7.Bytes(), Msg: m7, ID: ids[7]}, rt: &cfg.Routes[0]}); err != nil || !refunded || chain.renders["send"] != 1 {
		t.Fatalf("the successor's mark: %v %v %d", refunded, err, chain.renders["send"])
	}
}

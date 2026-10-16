package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// pt is a point as the tests write it: the time, GNOT's lock in whole tokens, an estimate or not.
type pt struct {
	at   int64
	gnot int64
	est  bool
}

func points(ps []histPoint, sym string) []pt {
	var out []pt
	for _, p := range ps {
		out = append(out, pt{p.At, p.Locks[sym] / one, p.Est})
	}
	return out
}

// mainnetActivity is the activity's index as mainnet's first day left it, in small numbers: a decision, four
// sends, two burns back paid out — one before this reader first looked (no payout time), one it saw paid.
func mainnetActivity() *activityState {
	act := newActivityState()
	act.count = 5
	act.dispatch[0] = &gnoDispatch{Nonce: 0, Kind: "decision", At: 1001}
	act.dispatch[1] = &gnoDispatch{Nonce: 1, Kind: "send", Token: "GNOT", Amount: 10 * one, At: 1100}
	act.dispatch[2] = &gnoDispatch{Nonce: 2, Kind: "send", Token: "GNOT", Amount: 30 * one, At: 1300}
	act.dispatch[3] = &gnoDispatch{Nonce: 3, Kind: "send", Token: "GNOT", Amount: 100 * one, At: 1400}
	act.dispatch[4] = &gnoDispatch{Nonce: 4, Kind: "send", Token: "GNOT", Amount: 15 * one, At: 1450}
	act.claims["a"] = &claimRec{Token: "GNOT", Amount: 10 * one, Paid: 10 * one, At: 1200, Final: true}
	act.claims["b"] = &claimRec{Token: "GNOT", Amount: 15 * one, Paid: 15 * one, At: 1500, PaidAt: 1550, Final: true}
	return act
}

// TestRebuildHistory walks the records back from the first reading's locks: the launch at nothing, each send
// at its record's time, a payout at the time this reader saw it, and one made before it looked at its credit,
// marked an estimate; the last point is the reading itself.
func TestRebuildHistory(t *testing.T) {
	locks := map[string]int64{"GNOT": 130 * one, "BUBBLE": 0}
	got := points(rebuildHistory(1000, 2000, locks, mainnetActivity()), "GNOT")
	want := []pt{{1000, 0, false}, {1100, 10, false}, {1200, 0, true}, {1300, 30, false}, {1400, 130, false}, {1450, 145, false}, {1550, 130, false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rebuilt\n got %v\nwant %v", got, want)
	}
	for _, p := range rebuildHistory(1000, 2000, locks, mainnetActivity()) {
		if p.Locks["BUBBLE"] != 0 {
			t.Fatalf("BUBBLE moved at %d: %v", p.At, p.Locks)
		}
	}
}

// What the records do not explain is the launch's level, and a reading past the records ends the line.
func TestRebuildHistoryUnexplained(t *testing.T) {
	act := newActivityState()
	act.count = 1
	act.dispatch[0] = &gnoDispatch{Nonce: 0, Kind: "send", Token: "GNOT", Amount: 25 * one, At: 1500}
	got := points(rebuildHistory(1000, 2000, map[string]int64{"GNOT": 1000 * one}, act), "GNOT")
	want := []pt{{1000, 975, false}, {1500, 1000, false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rebuilt\n got %v\nwant %v", got, want)
	}
	// no records at all: the launch's level and nothing else
	got = points(rebuildHistory(1000, 2000, map[string]int64{"GNOT": 7 * one}, newActivityState()), "GNOT")
	if !reflect.DeepEqual(got, []pt{{1000, 7, false}}) {
		t.Fatalf("no records: %v", got)
	}
	// records claiming more in than the lock holds: never a level below nothing
	act = newActivityState()
	act.count = 1
	act.dispatch[0] = &gnoDispatch{Nonce: 0, Kind: "send", Token: "GNOT", Amount: 50 * one, At: 1200}
	got = points(rebuildHistory(1000, 2000, map[string]int64{"GNOT": 0}, act), "GNOT")
	if got[0].gnot != 0 {
		t.Fatalf("a level below nothing: %v", got)
	}
}

// TestActivityComplete: the rebuild waits for an index that holds every dispatch and every claim the routes count.
func TestActivityComplete(t *testing.T) {
	act := mainnetActivity()
	if !activityComplete(act, map[string]int64{"GNOT": 2, "BUBBLE": 0}) {
		t.Fatal("the full index is not complete")
	}
	if activityComplete(act, map[string]int64{"GNOT": 3}) {
		t.Fatal("complete with a claim missing")
	}
	delete(act.dispatch, 2)
	if activityComplete(act, map[string]int64{"GNOT": 2}) {
		t.Fatal("complete with a dispatch missing")
	}
	act = mainnetActivity()
	act.errors = []string{"a read failed"}
	if activityComplete(act, nil) || activityComplete(nil, nil) {
		t.Fatal("complete with an error, or with no reading")
	}
}

// TestSampleHistory follows the mock's world: the first reading starts the record (the mock's canned index counts
// claims it does not hold, so no rebuild: the launch's level is an estimate), an unchanged reading adds nothing,
// the burn in flight paid out adds a point at that reading's time, a change first seen after a gap is an
// estimate, and the record survives a restart.
func TestSampleHistory(t *testing.T) {
	clock := fixedNow()
	s, _, _ := mockServer(true, siteDir, func() time.Time { return clock })
	dir := t.TempDir()
	s.hist.dir = dir
	ch := &s.cfg.Chains[0]
	other := s.others[ch.ID]
	ctx := context.Background()
	sample := func() {
		t.Helper()
		if err := s.sampleHistory(ctx, ch, other); err != nil {
			t.Fatalf("sample at %d: %v", clock.Unix(), err)
		}
	}
	h := func() *chainHistory { return s.hist.byID[ch.ID] }
	sample()
	if h() == nil || h().Rebuilt || len(h().Points) != 2 || !h().Points[0].Est || h().Points[1].Est {
		t.Fatalf("the first reading: %+v", h())
	}
	first := h().Points[1]
	if first.At != clock.Unix() || first.Locks["GNOT"] != 1_000_000*one || first.Locks["BUBBLE"] != 2_500_200*one {
		t.Fatalf("the first reading's point: %+v", first)
	}
	clock = clock.Add(time.Minute)
	sample()
	if len(h().Points) != 2 || h().Last != clock.Unix() {
		t.Fatalf("an unchanged reading added a point: %d points, last %d", len(h().Points), h().Last)
	}
	for i := 0; i < 5; i++ {
		if err := s.world.step("", "in"); err != nil {
			t.Fatal(err)
		}
	}
	clock = clock.Add(time.Minute)
	sample()
	if n := len(h().Points); n != 3 || h().Points[2].At != clock.Unix() || h().Points[2].Locks["BUBBLE"] != 2_500_000*one || h().Points[2].Est {
		t.Fatalf("the payout: %+v", h().Points)
	}
	// down ten minutes, then the world is back where it started: seen late, so an estimate
	if err := s.world.step("", "reset"); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(10 * time.Minute)
	sample()
	if p := h().Points[len(h().Points)-1]; !p.Est || p.Locks["BUBBLE"] != 2_500_200*one {
		t.Fatalf("a change seen after a gap: %+v", p)
	}
	// a restart: the same record from the file, and the next reading goes on from it
	b, err := os.ReadFile(filepath.Join(dir, "history-solana.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk chainHistory
	if err := json.Unmarshal(b, &onDisk); err != nil || !reflect.DeepEqual(&onDisk, h()) {
		t.Fatalf("the file holds %+v, the server %+v (%v)", onDisk, h(), err)
	}
	s2, _, _ := mockServer(true, siteDir, func() time.Time { return clock })
	s2.hist.dir = dir
	clock = clock.Add(time.Minute)
	if err := s2.sampleHistory(ctx, &s2.cfg.Chains[0], s2.others[ch.ID]); err != nil {
		t.Fatal(err)
	}
	if got := s2.hist.byID[ch.ID]; got == nil || len(got.Points) != len(onDisk.Points) || got.Sampled != onDisk.Sampled {
		t.Fatalf("after a restart: %+v", got)
	}
}

// TestHistoryAPI reads /api/solana/history as the page does: each token's points where its lock changed, an
// estimate marked, its lock now.
func TestHistoryAPI(t *testing.T) {
	clock := fixedNow()
	s, _, _ := mockServer(true, siteDir, func() time.Time { return clock })
	get := func() historyJSON {
		t.Helper()
		rr := httptest.NewRecorder()
		s.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/solana/history", nil))
		var h historyJSON
		if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &h) != nil {
			t.Fatalf("history: %d %s", rr.Code, rr.Body.String())
		}
		return h
	}
	h := get()
	if h.Chain != "solana" || h.From != fixedNow().Unix()-86400 || h.Now != fixedNow().Unix() || len(h.Tokens) != 2 {
		t.Fatalf("history: %+v", h)
	}
	g, b := h.Tokens[0], h.Tokens[1]
	if g.Symbol != "GNOT" || g.Lock != 1_000_000*one || len(g.Points) != 1 || len(g.Points[0]) != 3 || g.Points[0][2] != 1 {
		t.Fatalf("GNOT: %+v (one point, the launch's level, an estimate)", g)
	}
	if b.Symbol != "BUBBLE" || b.Lock != 2_500_200*one {
		t.Fatalf("BUBBLE: %+v", b)
	}
	for i := 0; i < 5; i++ {
		if err := s.world.step("", "in"); err != nil {
			t.Fatal(err)
		}
	}
	clock = clock.Add(time.Minute)
	h = get()
	b = h.Tokens[1]
	if b.Lock != 2_500_000*one || len(b.Points) != 2 || b.Points[1][0] != clock.Unix() || b.Points[1][1] != 2_500_000*one || len(b.Points[1]) != 2 {
		t.Fatalf("BUBBLE after the payout: %+v", b)
	}
	if len(h.Tokens[0].Points) != 1 {
		t.Fatalf("GNOT gained a point it never moved for: %+v", h.Tokens[0])
	}
	// an unknown chain is the API's 404
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/nowhere/history", nil))
	if rr.Code != 404 {
		t.Fatalf("an unknown chain: %d", rr.Code)
	}
}

// TestHistoryAPIEthereum reads the history beside Ethereum (the merge of the chart into E-M4's two deployments):
// /api/ethereum/history follows the Ethereum deployment's own locks — its burn in flight paid out drops BUBBLE's
// line a step — while Solana's history, read from the same server, keeps its own figures and moves with its own
// world alone.
func TestHistoryAPIEthereum(t *testing.T) {
	clock := fixedNow()
	s, _, _, _ := mockBoth(true, siteDir, func() time.Time { return clock })
	get := func(chain string) historyJSON {
		t.Helper()
		rr := httptest.NewRecorder()
		s.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/"+chain+"/history", nil))
		var h historyJSON
		if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &h) != nil {
			t.Fatalf("%s history: %d %s", chain, rr.Code, rr.Body.String())
		}
		return h
	}
	locks := func(h historyJSON) [2]int64 { return [2]int64{h.Tokens[0].Lock, h.Tokens[1].Lock} }
	e, sol := get("ethereum"), get("solana")
	if e.Chain != "ethereum" || len(e.Tokens) != 2 || e.Tokens[0].Symbol != "GNOT" || e.Tokens[1].Symbol != "BUBBLE" {
		t.Fatalf("ethereum history: %+v", e)
	}
	if locks(e) != [2]int64{500_000 * one, 1_202_500 * one} || locks(sol) != [2]int64{1_000_000 * one, 2_500_200 * one} {
		t.Fatalf("the locks: ethereum %v, solana %v", locks(e), locks(sol))
	}
	n0 := len(e.Tokens[1].Points)
	for i := 0; i < 6; i++ { // final, signer 1, signer 2, credited, half paid, paid
		if err := s.world.step("ethereum", "in"); err != nil {
			t.Fatal(err)
		}
	}
	clock = clock.Add(time.Minute)
	e, sol = get("ethereum"), get("solana")
	b := e.Tokens[1]
	if b.Lock != 1_200_000*one || len(b.Points) != n0+1 || b.Points[n0][0] != clock.Unix() || b.Points[n0][1] != 1_200_000*one {
		t.Fatalf("ethereum BUBBLE after the payout: %+v", b)
	}
	if locks(sol) != [2]int64{1_000_000 * one, 2_500_200 * one} || len(sol.Tokens[1].Points) != 1 {
		t.Fatalf("solana's history moved with Ethereum's world: %+v", sol)
	}
}

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// activity reads /api/solana/activity as the page does.
func activity(t *testing.T, s *server) activityJSON {
	t.Helper()
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/solana/activity", nil))
	var a activityJSON
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &a) != nil {
		t.Fatalf("activity: %d %s", rr.Code, rr.Body.String())
	}
	return a
}

func eventByKey(a activityJSON, key string) *eventJSON {
	for i := range a.Events {
		if a.Events[i].Key == key {
			return &a.Events[i]
		}
	}
	return nil
}

func doneKeys(w []walkStep) string {
	var out []string
	for _, s := range w {
		if s.Done {
			out = append(out, s.Key)
		}
	}
	return strings.Join(out, " ")
}

func lampStates(ls []signerLamp) string {
	var out []string
	for _, l := range ls {
		out = append(out, l.State)
	}
	return strings.Join(out, ",")
}

// TestActivity follows the mock's world through the activity: the history's events with their times, the send in
// flight through its header, its proof and its mint, the burn in flight through the signers' lamps, its credit
// and its payout, a signer going down — each event once, under a key that does not change between reads.
func TestActivity(t *testing.T) {
	s, _, _ := mockServer(true, siteDir, fixedNow)
	a := activity(t, s)
	if len(a.Errors) > 0 || a.Stale || len(a.Status) == 0 {
		t.Fatalf("errors %v stale %v status %d bytes", a.Errors, a.Stale, len(a.Status))
	}
	if a.Burns != 10 || a.Processed != 2 || a.Trusted != 1288 || a.Threshold != 2 {
		t.Errorf("counters: burns %d processed %d trusted %d threshold %d", a.Burns, a.Processed, a.Trusted, a.Threshold)
	}
	if lampStates(a.Signers) != "up,up,up" || a.Signers[0].Nonce != 9 {
		t.Errorf("lamps before any signer vouches for burn 9: %+v", a.Signers)
	}
	start := fixedNow().Unix()
	bt := func(h int64) int64 { return fixedNow().Add(time.Duration(h-mockHead) * mockBlock).Unix() }
	// the history, every event with its chain's time
	for _, c := range []struct {
		key  string
		time int64
	}{
		{"decision:" + mockDecID, bt(decisionHeight)},
		{"enact:" + mockDecID, bt(decisionHeight) + 90},
		{"send:" + mockPID, bt(pHeight)},
		{"header:604", bt(pHeight) + 40},
		{"mint:" + mockPID, bt(pHeight) + 100},
		{"header:1288", bt(1288) + 20},
		{"burn:" + mockQID, start - 1800},
		{"credit:" + mockQID, bt(qCreditHeight)},
		{"payout:" + mockQID + ":500000000", bt(qCreditHeight)},
		{"send:" + mockOutID, bt(outHeight)},
		{"burn:" + mockInID, start - 300},
	} {
		e := eventByKey(a, c.key)
		if e == nil {
			t.Errorf("no event %s in %v", c.key, keys(a))
			continue
		}
		if e.Time != c.time {
			t.Errorf("%s at %d, want %d", c.key, e.Time, c.time)
		}
	}
	if e := eventByKey(a, "mint:"+mockPID); e == nil || e.Proof == nil || e.Proof.Height != 601 || e.Proof.Total != 4 || e.Proof.Index != 2 || e.Proof.Aunts != 2 || e.Token != "GNOT" || e.Amount != 25*one || e.To != mockPub("alice").String() {
		t.Errorf("the history's mint and its proof: %+v", e)
	}
	if e := eventByKey(a, "payout:"+mockQID+":500000000"); e == nil || e.Exact || !e.Full {
		t.Errorf("a payout made before the first look is full and not exactly timed: %+v", e)
	}
	if e := eventByKey(a, "send:"+mockOutID); e == nil || e.Height != outHeight || e.From != mockSender || e.To != mockPub("alice").String() || e.Nonce == nil || *e.Nonce != 2 {
		t.Errorf("the send in flight: %+v", e)
	}
	if e := eventByKey(a, "decision:"+mockDecID); e == nil || !strings.HasPrefix(e.What, "#3 ") {
		t.Errorf("the decision: %+v", e)
	}
	for i := 1; i < len(a.Events); i++ {
		if a.Events[i].Time > a.Events[i-1].Time {
			t.Fatalf("the events are not newest first: %v", keys(a))
		}
	}
	for _, k := range []string{"header:1291", "mint:" + mockOutID, "credit:" + mockInID} {
		if eventByKey(a, k) != nil {
			t.Errorf("%s before it happened", k)
		}
	}
	// the replay's transfers: the newest that completed each way — the history's, while the two in flight are not
	if a.Last.Out == nil || a.Last.Out.ID != mockPID || doneKeys(a.Last.Out.Walk) != "lock final header proof mint" || a.Last.In == nil || a.Last.In.ID != mockQID || doneKeys(a.Last.In.Walk) != "burn vouch deliver credit pay" {
		t.Fatalf("the last transfers: %+v %+v", a.Last.Out, a.Last.In)
	}
	if w := a.Last.Out.Walk; w[2].Height != 604 || w[2].Time != bt(pHeight)+40 || w[3].Height != 601 || a.Last.Out.Proof == nil || a.Last.Out.Proof.Total != 4 || w[4].Time != bt(pHeight)+100 {
		t.Errorf("the history's send, step by step: %+v proof %+v", w, a.Last.Out.Proof)
	}
	// the send: its header, its proof, its mint
	_ = s.world.step("", "out")
	a = activity(t, s)
	if e := eventByKey(a, "header:1291"); e == nil || e.Time != start || a.Trusted != 1291 {
		t.Errorf("the header past the send: %+v, trusted %d", e, a.Trusted)
	}
	_ = s.world.step("", "out")
	a = activity(t, s)
	if a.Last.Out.ID != mockPID {
		t.Errorf("a send not yet minted is not the replay's: %s", a.Last.Out.ID)
	}
	_ = s.world.step("", "out")
	a = activity(t, s)
	if e := eventByKey(a, "mint:"+mockOutID); e == nil || e.Proof == nil || !e.Proof.Ring || e.Proof.Height != outHeight+1 || e.Proof.Total != 3 || e.Amount != 10*one || a.Processed != 3 {
		t.Errorf("the mint: %+v, processed %d", e, a.Processed)
	}
	if a.Last.Out.ID != mockOutID || doneKeys(a.Last.Out.Walk) != "lock final header proof mint" || a.Last.Out.Walk[2].Height != 1291 {
		t.Errorf("the send minted is the replay's: %s %+v", a.Last.Out.ID, a.Last.Out.Walk)
	}
	// the burn: the signers light one by one, then the credit, half paid, paid
	for i, want := range []string{"vouching,up,up", "vouching,vouching,up"} {
		_ = s.world.step("", "in")
		a = activity(t, s)
		if lampStates(a.Signers) != want || fmt.Sprint(eventByKey(a, "burn:"+mockInID).Signers) != fmt.Sprint(vouched(a.Signers)) {
			t.Errorf("in stage %d: lamps %s, the burn's signers %v", i+1, lampStates(a.Signers), eventByKey(a, "burn:"+mockInID).Signers)
		}
	}
	_ = s.world.step("", "in")
	a = activity(t, s)
	if e := eventByKey(a, "credit:"+mockInID); e == nil || e.Height != 1300 || e.Amount != 200*one || a.Last.In.ID != mockInID || doneKeys(a.Last.In.Walk) != "burn vouch deliver credit" {
		t.Errorf("the credit: %+v, the replay's burn %s %s", e, a.Last.In.ID, doneKeys(a.Last.In.Walk))
	}
	_ = s.world.step("", "in")
	a = activity(t, s)
	if e := eventByKey(a, "payout:"+mockInID+":100000000"); e == nil || !e.Exact || e.Full {
		t.Errorf("half paid, seen by this reader: %+v", e)
	}
	_ = s.world.step("", "in")
	a = activity(t, s)
	if e := eventByKey(a, "payout:"+mockInID+":200000000"); e == nil || !e.Full || doneKeys(a.Last.In.Walk) != "burn vouch deliver credit pay" {
		t.Errorf("paid in full: %+v", e)
	}
	_ = s.world.step("", "down")
	if a = activity(t, s); lampStates(a.Signers) != "vouching,vouching,down" {
		t.Errorf("signer 3 down: %s", lampStates(a.Signers))
	}
	if len(a.Errors) > 0 {
		t.Errorf("errors: %v", a.Errors)
	}
	// the browser suite's reset: the world and its readings start again
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, httptest.NewRequest("POST", "/api/mock/step?flow=reset", nil))
	if rr.Code != 200 {
		t.Fatalf("reset: %d %s", rr.Code, rr.Body.String())
	}
	if a = activity(t, s); eventByKey(a, "header:1291") != nil || eventByKey(a, "mint:"+mockOutID) != nil || lampStates(a.Signers) != "up,up,up" || a.Trusted != 1288 {
		t.Errorf("after the reset: trusted %d lamps %s events %v", a.Trusted, lampStates(a.Signers), keys(a))
	}
}

func keys(a activityJSON) []string {
	var out []string
	for _, e := range a.Events {
		out = append(out, e.Key)
	}
	return out
}

// TestActivityReadsOnlyWhatMoved pins the cost to the providers: the first read lists each account once and reads
// each transaction once, never more than ten accounts at a time (the mock refuses more, as a public provider
// does); a read where nothing moved is the mailbox's two counters and the client's 64-byte head on Solana, and the
// mailbox's render on gno.land.
func TestActivityReadsOnlyWhatMoved(t *testing.T) {
	s, g, ms := mockServer(false, siteDir, fixedNow)
	ch, other := &s.cfg.Chains[0], s.others["solana"]
	errs := s.activityNow(ctx, ch, other).errors
	if len(errs) > 0 {
		t.Fatalf("the first read: %v", errs)
	}
	if ms.calls["getTransaction"] != len(ms.txs) || ms.calls["getSignaturesForAddress"] != 3 {
		t.Errorf("the first read: %v for %d transactions", ms.calls, len(ms.txs))
	}
	ms.calls, g.renders = map[string]int{}, map[string]int{}
	s.now = func() time.Time { return fixedNow().Add(activityTTL + time.Second) }
	errs = s.activityNow(ctx, ch, other).errors
	if len(errs) > 0 || fmt.Sprint(ms.calls) != "map[getAccountInfo:1 getMultipleAccounts:1]" || fmt.Sprint(g.renders) != "map[json:1]" {
		t.Errorf("a read where nothing moved: solana %v, gno %v, errors %v", ms.calls, g.renders, errs)
	}
	// within the TTL nothing is read at all
	ms.calls, g.renders = map[string]int{}, map[string]int{}
	s.activityNow(ctx, ch, other)
	if len(ms.calls) != 0 || len(g.renders) != 0 {
		t.Errorf("a read within the TTL: %v %v", ms.calls, g.renders)
	}
}

// TestActivityAnswersAtOnce: once there is a reading, a stale one is served at once and refreshed behind — a
// provider that takes seconds to answer never holds the page — one refresh at a time however many ask.
func TestActivityAnswersAtOnce(t *testing.T) {
	s, _, ms := mockServer(false, siteDir, fixedNow)
	s.syncRefresh = false
	ch, other := &s.cfg.Chains[0], s.others["solana"]
	first := s.activityNow(ctx, ch, other) // the first reading is made in the request: there is nothing to serve yet
	if first == nil || len(first.errors) > 0 {
		t.Fatalf("the first reading: %+v", first)
	}
	var clock sync.Mutex
	now := fixedNow().Add(activityTTL + time.Second)
	s.now = func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	ms.delay = 300 * time.Millisecond
	ms.calls = map[string]int{}
	t0 := time.Now()
	for range 5 {
		if got := s.activityNow(ctx, ch, other); got != first {
			t.Fatal("a stale reading was not served as it stood")
		}
	}
	if d := time.Since(t0); d > 200*time.Millisecond {
		t.Errorf("five reads of a stale reading took %s: they waited for the provider", d)
	}
	a := s.activityOf(ch.ID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.Lock()
		st := a.st
		a.mu.Unlock()
		if st != first {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background refresh never replaced the reading")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ms.mu.Lock()
	batches := ms.calls["getMultipleAccounts"]
	ms.mu.Unlock()
	if batches != 1 {
		t.Errorf("five stale reads made %d refreshes, want 1", batches)
	}
}

// TestActivityKeepsTheLastStatus: while gno.land cannot be read (a public node's rate limit, say) the activity
// carries the last good status, marked stale, and says what failed and how old the figures are.
func TestActivityKeepsTheLastStatus(t *testing.T) {
	s, _, _ := mockServer(false, siteDir, fixedNow)
	if a := activity(t, s); a.Stale || len(a.Status) == 0 {
		t.Fatalf("the first read: stale %v", a.Stale)
	}
	s.gno = failingGno{}
	s.now = func() time.Time { return fixedNow().Add(statusTTL + time.Second) }
	a := activity(t, s)
	var st statusJSON
	if !a.Stale || json.Unmarshal(a.Status, &st) != nil || len(st.Tokens) != 2 || !strings.Contains(strings.Join(a.Errors, " "), "could not be read") || !strings.Contains(strings.Join(a.Errors, " "), "the figures are from") {
		t.Errorf("stale %v, %d tokens, errors %v", a.Stale, len(st.Tokens), a.Errors)
	}
	// /status itself still says 503: only the picture keeps its last figures
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/solana/status", nil))
	if rr.Code != 503 {
		t.Errorf("/status while failing: %d", rr.Code)
	}
}

// fakeProber answers each endpoint as told and counts the questions.
type fakeProber struct {
	mu    sync.Mutex
	code  map[string]int
	asked int
}

func (p *fakeProber) Probe(ctx context.Context, base, lane string, nonce uint64) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked++
	c := p.code[base]
	if c < 0 {
		return 0, errors.New("connection refused")
	}
	return c, nil
}

func TestLamps(t *testing.T) {
	s, _, _ := mockServer(false, siteDir, fixedNow)
	ch := &s.cfg.Chains[0]
	ch.Signers = []string{"https://a/1", "https://a/2", "https://a/3", "https://a/4"}
	p := &fakeProber{code: map[string]int{"https://a/1": 200, "https://a/2": 404, "https://a/3": -1, "https://a/4": 502}}
	s.prober = p
	if got := lampStates(s.lamps(ctx, ch, 7)); got != "vouching,up,down,down" {
		t.Errorf("lamps: %s", got)
	}
	if got := lampStates(s.lamps(ctx, ch, 7)); got != "vouching,up,down,down" || p.asked != 4 {
		t.Errorf("a second look within the TTL asked again: %d questions", p.asked)
	}
	// no burn yet: nothing to vouch for, so a signature served for nonce 0 says only that the signer is up
	if got := lampStates(s.lamps(ctx, ch, -1)); got != "up,up,down,down" {
		t.Errorf("lamps before any burn: %s", got)
	}
	if got := fmt.Sprint(vouched(s.lamps(ctx, ch, 7))); got != "[1]" {
		t.Errorf("vouched: %s", got)
	}
	ch.Signers = nil
	if got := s.lamps(ctx, ch, 7); len(got) != 0 {
		t.Errorf("no endpoints, no lamps: %+v", got)
	}
}

func TestProcessMessage(t *testing.T) {
	msg := mockOutMessage()
	ix := sol.ProcessIx(sol.Mailbox{Program: mockPub("mailbox")}, mockPub("relayer"), []byte{9, 8, 7}, msg.Bytes(), msg.ID(), mockPub("gnot"), mockPub("pausable"), nil, nil, nil)
	data, _ := ix.Data()
	raw, ok := processMessage(data)
	if !ok || hex.EncodeToString(raw) != hex.EncodeToString(msg.Bytes()) {
		t.Fatalf("the process instruction's message: %v %x", ok, raw)
	}
	for _, bad := range [][]byte{nil, {2, 0, 0, 0, 0}, {1, 9, 0, 0, 0, 1}, data[:len(data)-1]} {
		if _, ok := processMessage(bad); ok {
			t.Errorf("%x read as a process instruction", bad)
		}
	}
}

func TestThresholdOf(t *testing.T) {
	for rule, want := range map[string]int{"2 of 3 signers": 2, "": 0, "unusable: no signers": 0, "3 of 5 signers": 3} {
		if got := thresholdOf(rule); got != want {
			t.Errorf("%q: %d, want %d", rule, got, want)
		}
	}
}

// TestWalks: the steps' facts from what is known — the first header that reached the send's block (not merely
// the newest), the client's trusted height when the header itself is older than the index, a mint whose proof
// the index never saw, a burn known by its id alone.
func TestWalks(t *testing.T) {
	headers := []HeaderEvent{{Height: 130, Time: 900}, {Height: 112, Time: 800}, {Height: 105, Time: 700}}
	w := outWalk(100, 650, headers, 130, nil, nil)
	if doneKeys(w) != "lock final header" || w[2].Height != 105 || w[2].Time != 700 || !w[2].Exact {
		t.Errorf("the first header past block 101: %+v", w)
	}
	w = outWalk(100, 650, nil, 130, nil, nil)
	if doneKeys(w) != "lock final header" || w[2].Exact || w[2].Height != 130 {
		t.Errorf("the trusted height alone: %+v", w)
	}
	w = outWalk(100, 650, nil, 90, nil, nil)
	if doneKeys(w) != "lock final" {
		t.Errorf("a client behind the send: %+v", w)
	}
	w = outWalk(0, 0, nil, 0, nil, &DeliveryEvent{Time: 990, Sig: "x"})
	if doneKeys(w) != "lock final header proof mint" || w[3].Exact || w[2].Height != 0 || w[4].Time != 990 {
		t.Errorf("a mint older than the index: %+v", w)
	}
	w = outWalk(100, 650, headers, 130, &ProofEvent{Height: 112, Time: 850}, nil)
	if w[2].Height != 112 || doneKeys(w) != "lock final header proof" {
		t.Errorf("the proof's header decides which header counts: %+v", w)
	}
	w = inWalk(nil, "", nil, 2, nil)
	if doneKeys(w) != "burn" {
		t.Errorf("a burn by id: %+v", w)
	}
	w = inWalk(&Burn{Time: 5}, "", []int{1, 3}, 2, nil)
	if doneKeys(w) != "burn vouch" {
		t.Errorf("two vouched: %+v", w)
	}
	w = inWalk(&Burn{Time: 5}, "", nil, 2, &claimRec{Amount: 10, Paid: 10, At: 9, Height: 4})
	if doneKeys(w) != "burn vouch deliver credit pay" || w[4].Exact || w[3].Height != 4 {
		t.Errorf("credited and paid: %+v", w)
	}
}

// manyMsgs is a node whose mailbox dispatched 45 sends, for the tracker's search of the pages before the index's
// listing.
type manyMsgs struct {
	*mockGno
	mu    sync.Mutex
	pages []string
}

func (m *manyMsgs) RenderAt(ctx context.Context, pkgPath, path string, height int64) (string, error) {
	switch {
	case strings.HasSuffix(pkgPath, "hlmailbox") && path == "json":
		s, _ := m.mockGno.RenderAt(ctx, pkgPath, path, height)
		return strings.Replace(s, `"count":3`, `"count":45`, 1), nil
	case strings.HasPrefix(path, "msgs/"):
		from, _ := strconv.Atoi(strings.TrimPrefix(path, "msgs/"))
		m.mu.Lock()
		m.pages = append(m.pages, path)
		m.mu.Unlock()
		var page []msgJSON
		for n := from; n < 45 && len(page) < 20; n++ {
			msg := mockSendMessage(uint32(n), int64(n+1)*one)
			page = append(page, msgJSON{Nonce: uint32(n), ID: idOf(msg), Height: int64(100 + n), Message: hex.EncodeToString(msg.Bytes())})
		}
		b, _ := json.Marshal(page)
		return string(b), nil
	}
	return m.mockGno.RenderAt(ctx, pkgPath, path, height)
}

func TestFindDispatch(t *testing.T) {
	s, g, _ := mockServer(false, siteDir, fixedNow)
	many := &manyMsgs{mockGno: g}
	s.gno = many
	ch, other := &s.cfg.Chains[0], s.others["solana"]
	st, a := s.activityNow(ctx, ch, other), s.activityOf(ch.ID)
	if len(st.dispatch) != activityList || st.count != 45 {
		t.Fatalf("the index holds the newest %d of 45: %d, count %d", activityList, len(st.dispatch), st.count)
	}
	for _, n := range []int{30, 5, 2, 5} {
		id := idOf(mockSendMessage(uint32(n), int64(n+1)*one))
		d := s.findDispatch(ctx, ch, other, a, st, id)
		if d == nil || int(d.Nonce) != n || d.Height != int64(100+n) || d.Amount != int64(n+1)*one || d.Kind != "send" {
			t.Errorf("nonce %d: %+v", n, d)
		}
	}
	if s.findDispatch(ctx, ch, other, a, st, strings.Repeat("ab", 32)) != nil {
		t.Error("an id nobody dispatched was found")
	}
	// the listing (msgs/25), then each older page once: msgs/5 for nonce 5, msgs/0 for nonce 2
	if got := strings.Join(many.pages, " "); got != "msgs/25 msgs/5 msgs/0" {
		t.Errorf("pages read: %s", got)
	}
	_ = hl.Version
}

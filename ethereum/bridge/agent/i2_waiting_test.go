package agent

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// The integration lane I2 of the round-4 fold: the monitors and hlwatch read a route's waiting claims from its waiting
// index (waiting/<from>, the realms' fold F15d, E4-RL1), never past a bound: F15b's walk read claims/0, /100 and /200
// alone, so a forged claim past the first 300 waiting claims was never named by id.

// i2Claim is a claim object as claim/<id> renders it.
func i2Claim(id, state, by string, amount int64) string {
	return fmt.Sprintf(`{"id":"%s","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":%d,"paid":0,"state":"%s","by":"%s","at":0,"height":59,"seq":1}`, id, amount, state, by)
}

// i2Indexed puts burns into the monitor's index as credited at height 59 from block 50 and checked: their claims are
// placed deliveries, never suspects.
func (r *monRig) i2Indexed(ids []string) {
	r.m.state.Index.init()
	for i, id := range ids {
		r.m.state.Index.Burns[id] = &burnRec{Symbol: "GNOT", Nonce: uint32(100_000 + i), Block: 50, Tx: fmt.Sprintf("0x%064x", i+1), To: "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5", Amount: 5_000_000, Credited: 59, Checked: true, Seen: r.now.Unix()}
	}
}

// i2Pages are the claims and waiting pages among reads.
func i2Pages(reads []string) []string {
	var out []string
	for _, p := range reads {
		if strings.HasPrefix(p, "waiting/") || strings.HasPrefix(p, "claims/") {
			out = append(out, p)
		}
	}
	return out
}

// i2Delivered sets hlmailbox's delivered count.
func (r *monRig) i2Delivered(n int) {
	r.gno.set(m3bMailPath, "json", strings.Replace(m3bMailboxJSON(nil, 2, []string{"", "", "", "", monWatchGno, ""}), `"count":0`, fmt.Sprintf(`"count":0,"delivered":%d`, n), 1))
}

// i2Reads counts a route's renders of the given path prefixes, in order.
func (r *monRig) i2Reads(route string, prefixes ...string) func() []string {
	var mu sync.Mutex
	var reads []string
	r.gno.afterRender = func(pkg, path string) {
		for _, p := range prefixes {
			if pkg == route && strings.HasPrefix(path, p) {
				mu.Lock()
				reads = append(reads, path)
				mu.Unlock()
			}
		}
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, reads...)
	}
}

// 2,000 claims wait on GNOT's route, every one a burn of the index but one forged credit at the 1,950th place: the
// monitor names it by id — a forged credit, flagged — reading the waiting index ten pages a poll (F15b's walk read the
// first 300 and said the rest unread, for good); hlwatch's one View catches up over the polls and names it too.
func TestI2ForgedClaimPastTheFirst300IsNamed(t *testing.T) {
	setup := func(t *testing.T) (*monRig, string) {
		r := newMonRig(t)
		r.chain.mine(130, 100)
		r.chain.retime(uint64(r.now.Unix()) - 30 - 130*12) // both providers current: no dispatch of its id above the index
		forged := strings.Repeat("ef", 32)
		var indexed []string
		route := m3bNS + "hlgnot"
		for at := 0; at < 2000; at += 100 {
			var page []string
			for i := at; i < at+100; i++ {
				id := fmt.Sprintf("%064x", i+1)
				if i == 1949 {
					id = forged
				} else {
					indexed = append(indexed, id)
				}
				page = append(page, i2Claim(id, "open", "verifier", 5_000_000))
			}
			r.gno.set(route, fmt.Sprintf("claims/%d", at), "["+strings.Join(page, ",")+"]")
		}
		r.gno.set(route, "json", m3bRouteJSON(1000, 300, 0, 0))
		r.gno.set(route, "claim/"+forged, i2Claim(forged, "open", "verifier", 5_000_000))
		r.i2Indexed(indexed)
		r.i2Delivered(2000)
		return r, forged
	}
	r, forged := setup(t)
	reads := r.i2Reads(m3bNS+"hlgnot", "claims/", "waiting/")
	for i := 0; i < 3 && !r.alarmed("credit:unindexed:"+forged+":flag"); i++ {
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		r.step(10_000_000_000)
	}
	got := reads()
	t.Logf("the monitor: %d pages read %v; flags %v; alarms %v", len(got), got, r.gnoFlags(), r.m.Alarm.StandingKeys())
	if !r.alarmed("credit:unindexed:" + forged + ":flag") {
		t.Fatalf("the forged claim at the 1,950th place was never named: pages read %v; alarms %v", got, r.m.Alarm.StandingKeys())
	}
	for _, p := range got {
		if strings.HasPrefix(p, "claims/") {
			t.Fatalf("a claims/<from> walk on a route with a waiting index: %v", got)
		}
	}
	// the same over hlwatch: one View (its catch-up polls on while the waiting index's reading moves)
	h, forged := setup(t)
	v, ok, why := h.view(t)
	t.Logf("hlwatch: green %v; %s", ok, why)
	if ok || !strings.Contains(why, "credit:unindexed:"+forged) || v.Verdict != "" {
		t.Fatalf("hlwatch did not name the forged claim past the first 300: green %v; %s", ok, why)
	}
}

// The waiting index is read once, the suspects again by id: entries no longer open (withheld, gone, paid) are skipped,
// places with gaps are read on from the last place + 1, a credit by vote counts as found and is never judged, a forged
// credit is flagged; the next poll reads only the index's tail and the suspects by id; a claim restored at a new place
// is read there and judged; a suspect paid is dropped; a page whose places do not increase is no page — said, never
// read past.
func TestI2WaitingIndexReadOnceAndSuspectsByID(t *testing.T) {
	r := newMonRig(t)
	r.chain.mine(130, 100)
	r.chain.retime(uint64(r.now.Unix()) - 30 - 130*12)
	route := m3bNS + "hlgnot"
	withheld, gone, paid := strings.Repeat("a1", 32), strings.Repeat("b2", 32), strings.Repeat("c3", 32)
	vote, forged := strings.Repeat("d4", 32), strings.Repeat("ef", 32)
	var entries, indexed []string
	for i := 0; i < 250; i++ {
		pos := 2*i + 1 // the tidy leaves gaps
		id := fmt.Sprintf("%064x", i+1)
		obj := i2Claim(id, "open", "verifier", 5_000_000)
		switch pos {
		case 11:
			id, obj = withheld, i2Claim(withheld, "withheld", "verifier", 5_000_000)
		case 13:
			id, obj = gone, `{"id":"`+gone+`","state":"gone"}`
		case 15:
			id, obj = paid, i2Claim(paid, "done", "verifier", 5_000_000)
		case 401:
			id, obj = vote, i2Claim(vote, "open", "vote", 5_000_000)
		case 451:
			id, obj = forged, i2Claim(forged, "open", "verifier", 5_000_000)
		default:
			indexed = append(indexed, id)
		}
		entries = append(entries, fmt.Sprintf(`{"pos":%d,%s`, pos, strings.TrimPrefix(obj, "{")))
	}
	r.gno.setWaiting(route, entries)
	r.gno.set(route, "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(route, "claim/"+vote, i2Claim(vote, "open", "vote", 5_000_000))
	r.gno.set(route, "claim/"+forged, i2Claim(forged, "open", "verifier", 5_000_000))
	r.i2Indexed(indexed)
	r.i2Delivered(len(indexed) + 5) // the five no burn carries: withheld, gone, paid, the vote's, the forged one
	reads := r.i2Reads(route, "claims/", "waiting/", "claim/")
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	first := reads()
	t.Logf("poll 1: reads %v; alarms %v", first, r.m.Alarm.StandingKeys())
	if want := []string{"waiting/1", "waiting/200", "waiting/400"}; strings.Join(i2Pages(first), " ") != strings.Join(want, " ") {
		t.Fatalf("poll 1 read the pages %v, want %v", i2Pages(first), want)
	}
	if !r.alarmed("credit:unindexed:"+forged+":flag") || r.alarmed("credit:unindexed:"+vote+":flag") {
		t.Fatalf("the forged credit not flagged, or the vote's judged: %v", r.m.Alarm.StandingKeys())
	}
	for _, id := range []string{withheld, gone, paid} {
		if r.alarmed("credit:unindexed:"+id) || r.alarmed("credit:unindexed:"+id+":flag") {
			t.Fatalf("an entry no longer open was judged: %s %v", id, r.m.Alarm.StandingKeys())
		}
	}
	// poll 2: the tail, and the two suspects by id
	r.step(10_000_000_000)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	second := reads()[len(first):]
	t.Logf("poll 2: reads %v", second)
	byID := map[string]bool{}
	for _, p := range second {
		if strings.HasPrefix(p, "claim/") {
			byID[strings.TrimPrefix(p, "claim/")] = true
		}
	}
	if strings.Join(i2Pages(second), " ") != "waiting/500" || !byID[vote] || !byID[forged] {
		t.Fatalf("poll 2 read %v: not the index's tail and the two suspects by id", second)
	}
	// the withheld claim restored: open again at a new place; the vote's claim paid
	restored := i2Claim(withheld, "open", "verifier", 5_000_000)
	entries = append(entries[:5], entries[6:]...) // out of place 11
	entries = append(entries, `{"pos":501,`+strings.TrimPrefix(restored, "{"))
	r.gno.setWaiting(route, entries)
	r.gno.set(route, "claim/"+withheld, restored)
	r.gno.set(route, "claim/"+vote, i2Claim(vote, "done", "vote", 5_000_000))
	r.step(10_000_000_000)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("poll 3: reads %v; alarms %v", reads()[len(first)+len(second):], r.m.Alarm.StandingKeys())
	if !r.alarmed("credit:unindexed:" + withheld + ":flag") {
		t.Fatalf("the claim restored at place 501 was not read and judged: %v", r.m.Alarm.StandingKeys())
	}
	// poll 4: the tail from place 502, the two suspects left by id (the vote's paid claim dropped)
	r.step(10_000_000_000)
	before := len(reads())
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	fourth := reads()[before:]
	byID = map[string]bool{}
	for _, p := range fourth {
		if strings.HasPrefix(p, "claim/") {
			byID[strings.TrimPrefix(p, "claim/")] = true
		}
	}
	t.Logf("poll 4: reads %v", fourth)
	if strings.Join(i2Pages(fourth), " ") != "waiting/502" || !byID[forged] || !byID[withheld] || byID[vote] {
		t.Fatalf("poll 4 read %v: not the tail from 502 and the suspects left (the forged and the restored claims) by id", fourth)
	}
	// a page whose places do not increase: said, and read again from the same place
	r.gno.setWaiting(route, append(entries, `{"pos":503,`+strings.TrimPrefix(i2Claim(fmt.Sprintf("%064x", 9999), "open", "verifier", 1), "{"), `{"pos":502,`+strings.TrimPrefix(i2Claim(fmt.Sprintf("%064x", 9998), "open", "verifier", 1), "{")))
	before = len(reads())
	v, ok, why := r.view(t)
	t.Logf("a bad page: green %v; %s", ok, why)
	if ok || !strings.Contains(why, "the waiting claims of "+route) || !strings.Contains(why, "increasing places") || v.Verdict != "" {
		t.Fatalf("a page of decreasing places was read past: green %v; %s", ok, why)
	}
	r.gno.setWaiting(route, entries)
	r.step(10_000_000_000)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	pages := i2Pages(reads()[before:])
	for _, p := range pages {
		if p != "waiting/502" {
			t.Fatalf("after the bad page the index was not read again from its place: %v", pages)
		}
	}
	if len(pages) < 2 {
		t.Fatalf("the index was not read after the bad page: %v", pages)
	}
}

// A route built before the waiting index (waiting/<from> an unknown path) is walked as F15b's bound walks it — the first
// 300 claims, the rest said unread — and asked for the index once only.
func TestI2OlderRouteKeepsTheBoundedWalk(t *testing.T) {
	r := newMonRig(t)
	route := m3bNS + "hlgnot"
	r.claimsPages(route, 2000)
	r.gno.olderRoute(route)
	r.i2Delivered(1000)
	reads := r.i2Reads(route, "claims/", "waiting/")
	r.m.ReadOnly = true
	for i := 0; i < 2; i++ {
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		r.step(10_000_000_000)
	}
	got := reads()
	t.Logf("two polls of an older route: %v", got)
	if strings.Join(got, " ") != "waiting/1 claims/0 claims/100 claims/200 claims/0 claims/100 claims/200" {
		t.Fatalf("the older route's reads: %v", got)
	}
	if _, ok, why := r.view(t); ok || !strings.Contains(why, "built before the waiting index") {
		t.Fatalf("the bound is not said: green %v; %s", ok, why)
	}
}

// The review of I2's first reading (finding 1): a suspect whose read by id fails is not judged on what it was. A forged
// claim flagged, then voided by GovDAO (whose decision clears the flags): at the next poll its claim/<id> does not read
// — the poll must not flag the route again over the forged claim's old "open" state; the poll after reads it voided
// and drops it.
func TestI2SuspectNotJudgedOnAFailedRead(t *testing.T) {
	r := newMonRig(t)
	r.chain.mine(130, 100)
	r.chain.retime(uint64(r.now.Unix()) - 30 - 130*12)
	route := m3bNS + "hlgnot"
	forged := strings.Repeat("ef", 32)
	var entries, indexed []string
	for i := 1; i <= 10; i++ {
		id, obj := fmt.Sprintf("%064x", i), ""
		if i == 5 {
			id = forged
		} else {
			indexed = append(indexed, id)
		}
		obj = i2Claim(id, "open", "verifier", 5_000_000)
		entries = append(entries, fmt.Sprintf(`{"pos":%d,%s`, i, strings.TrimPrefix(obj, "{")))
	}
	r.gno.setWaiting(route, entries)
	// the identity holds (the claims are the waiting index's alone): the rule under test is the forged-credit rule's — an
	// owed nothing on Ethereum carries is a break of the identity's own, flagged at once by its bound (E5-GT1)
	r.gno.set(route, "json", m3bRouteJSON(1000, 0, 0, 0))
	r.gno.set(route, "claim/"+forged, i2Claim(forged, "open", "verifier", 5_000_000))
	r.i2Indexed(indexed)
	r.i2Delivered(10)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	flagged := len(r.gnoFlags())
	if flagged != 2 || !r.alarmed("credit:unindexed:"+forged+":flag") {
		t.Fatalf("the forged claim was not flagged at the first poll: %v %v", r.gnoFlags(), r.m.Alarm.StandingKeys())
	}
	// GovDAO voids it as forged (the unpause option clears the flags); its read by id then fails once
	voided := i2Claim(forged, "forged", "verifier", 5_000_000)
	r.gno.set(route, "claim/"+forged, voided)
	entries[4] = `{"pos":5,` + strings.TrimPrefix(voided, "{")
	r.gno.setWaiting(route, entries)
	r.gno.renderErr = func(pkg, path string) error {
		if pkg == route && path == "claim/"+forged {
			return errors.New("timeout")
		}
		return nil
	}
	r.step(10_000_000_000)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("the poll whose read failed: gno flags %d (was %d); alarms %v", len(r.gnoFlags()), flagged, r.m.Alarm.StandingKeys())
	if len(r.gnoFlags()) != flagged {
		t.Fatalf("the voided claim was flagged again on its old state while its read failed: %v", r.gnoFlags())
	}
	r.gno.renderErr = nil
	r.step(10_000_000_000)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if len(r.gnoFlags()) != flagged || r.alarmed("credit:unindexed:"+forged+":flag") {
		t.Fatalf("the voided claim was not dropped once it read: flags %v; alarms %v", r.gnoFlags(), r.m.Alarm.StandingKeys())
	}
}

// The review's finding 3: the suspects are read again by id a hundred a poll, the least recently read first — 250 open
// credits by vote no burn carries (a checkpointed index's open claims from before it read the same) cost a hundred
// reads a poll, never 250, and each is read again within three polls; none of them is judged (a vote's credit).
func TestI2SuspectsReadAgainInTurn(t *testing.T) {
	r := newMonRig(t)
	route := m3bNS + "hlgnot"
	var entries []string
	ids := map[string]bool{}
	for i := 1; i <= 250; i++ {
		id := fmt.Sprintf("%064x", i)
		ids[id] = true
		obj := i2Claim(id, "open", "vote", 5_000_000)
		entries = append(entries, fmt.Sprintf(`{"pos":%d,%s`, i, strings.TrimPrefix(obj, "{")))
		r.gno.set(route, "claim/"+id, obj)
	}
	r.gno.setWaiting(route, entries)
	r.gno.set(route, "json", m3bRouteJSON(1000, 0, 0, 0))
	r.i2Delivered(250)
	reads := r.i2Reads(route, "claim/", "waiting/")
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if got := reads(); strings.Join(got, " ") != "waiting/1 waiting/101 waiting/201" {
		t.Fatalf("poll 1 read %v", got)
	}
	seen := map[string]int{}
	for p := 2; p <= 4; p++ {
		before := len(reads())
		r.step(10_000_000_000)
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		byID := 0
		for _, path := range reads()[before:] {
			if strings.HasPrefix(path, "claim/") {
				byID++
				seen[strings.TrimPrefix(path, "claim/")]++
			}
		}
		t.Logf("poll %d: %d reads by id", p, byID)
		if byID > 100 {
			t.Fatalf("poll %d read %d suspects by id (a hundred a poll at most)", p, byID)
		}
	}
	for id := range ids {
		if seen[id] == 0 {
			t.Fatalf("suspect %s… was not read again in three polls (%d read)", id[:12], len(seen))
		}
	}
	if len(r.gnoFlags()) != 0 {
		t.Fatalf("a credit by vote was judged: %v", r.gnoFlags())
	}
}

// The review's finding 4: a page answered past the route's own last place (a node's odd answer) moved the reading past
// every real place; the route json's "waitingLast" (the last place the index gave) brings it back to place 1 at the next
// poll, so a forged claim entered below is read and judged.
func TestI2ReadingStartsOverPastTheRoutesLastPlace(t *testing.T) {
	r := newMonRig(t)
	r.chain.mine(130, 100)
	r.chain.retime(uint64(r.now.Unix()) - 30 - 130*12)
	route := m3bNS + "hlgnot"
	a, b, forged := fmt.Sprintf("%064x", 1), fmt.Sprintf("%064x", 2), strings.Repeat("ef", 32)
	entry := func(pos int, id string) string {
		return fmt.Sprintf(`{"pos":%d,%s`, pos, strings.TrimPrefix(i2Claim(id, "open", "verifier", 5_000_000), "{"))
	}
	r.i2Indexed([]string{a, b})
	r.i2Delivered(3)
	r.gno.set(route, "json", strings.TrimSuffix(m3bRouteJSON(1000, 300, 0, 0), "}")+`,"waitingLast":3}`)
	r.gno.set(route, "waiting/1", "["+entry(1, a)+","+entry(900, b)+"]") // the odd answer: a place past the route's last
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.gno.mu.Lock()
	delete(r.gno.renders, route+":waiting/1")
	r.gno.mu.Unlock()
	r.gno.setWaiting(route, []string{entry(1, a), entry(2, b), entry(3, forged)})
	r.gno.set(route, "claim/"+forged, i2Claim(forged, "open", "verifier", 5_000_000))
	reads := r.i2Reads(route, "waiting/")
	r.step(10_000_000_000)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	t.Logf("after the odd page: reads %v; alarms %v", reads(), r.m.Alarm.StandingKeys())
	if !r.alarmed("credit:unindexed:" + forged + ":flag") {
		t.Fatalf("the reading did not start over below the route's last place: reads %v, alarms %v", reads(), r.m.Alarm.StandingKeys())
	}
}

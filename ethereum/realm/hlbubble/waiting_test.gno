package hlbubble

import (
	"chain"
	"chain/runtime"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/p/nt/testutils/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
)

// The waiting index's page and the route probes (E4-RL1, E4-RL2) run last, on a route that never migrates: the
// verifier that takes anything is active (TestRouteAddressSwept), so the burns are crafted, as payees_test.gno's are.

// placeOfEntry is an entry's place: {"pos":P,…
func placeOfEntry(e string) int64 {
	s := e[len("{\"pos\":"):]
	n, _ := strconv.ParseInt(s[:strings.Index(s, ",")], 10, 64)
	return n
}

// idOfEntry is an entry's claim id.
func idOfEntry(e string) string {
	i := strings.Index(e, "\"id\":\"") + len("\"id\":\"")
	return e[i : i+64]
}

// waitingEntries are the waiting index's entries as waiting/<from> pages them, from place 1, read as a machine reads
// them: a page, then from its last place + 1, until a page shorter than a full one.
func waitingEntries() []string {
	var all []string
	from := "1"
	for {
		p := Render("waiting/" + from)
		if p == "[]" {
			return all
		}
		entries := strings.Split(p[len("[{"):len(p)-len("}]")], "},{")
		for i := range entries {
			entries[i] = "{" + entries[i] + "}"
		}
		all = append(all, entries...)
		if len(entries) < hlroute.PageLines {
			return all
		}
		from = itoa(placeOfEntry(entries[len(entries)-1]) + 1)
	}
}

// placeOf is a claim's place in the waiting index, 0 if it holds none.
func placeOf(id string) int64 {
	var at int64
	waitingByPos.Iterate("", "", func(k string, v any) bool {
		if v.(string) == id {
			at, _ = strconv.ParseInt(strings.TrimLeft(k, "0"), 10, 64)
			return true
		}
		return false
	})
	return at
}

// TestWaitingPage (E4-RL1): waiting/<from> pages the waiting index — each entry claim/<id>'s object byte for byte with
// its place first, in place order, the pages joined (a page, then from its last place + 1) the whole index, which holds
// every open claim once (checkWaiting); "0" reads as 1; anything but decimal digits, and a place past the last, is
// "[]". A claim made open again takes a new place above every place before: a withheld claim restored at once, and a
// voided one credited by vote, which moves it to its new recipient's queue. A claim Claim pays in full stays, "done",
// until the tidy each credit makes passes it (Claim, the payouts' path, does no work for the index).
func TestWaitingPage(cur realm, t *testing.T) {
	skipDays(300) // past the earlier test functions' account checkpoints (the bubble realm's clock rule)
	lockUp(cur, 4*Minimum())
	amount := Fee() + one
	kc, _ := hl.AddressToBytes32(carol)
	ka, _ := hl.AddressToBytes32(alice)
	c1, c2 := burnTo(cur, kc, amount), burnTo(cur, ka, amount)
	checkWaiting(t, "two burns")
	entries := waitingEntries()
	if len(entries) != waitingByPos.Size() || len(entries) == 0 {
		t.Fatalf("the pages hold %d entries, the index %d", len(entries), waitingByPos.Size())
	}
	last := int64(0)
	for _, e := range entries {
		pos, id := placeOfEntry(e), idOfEntry(e)
		if pos <= last {
			t.Fatalf("place %d after %d", pos, last)
		}
		last = pos
		want := "{\"pos\":" + itoa(pos) + ",\"id\":\"" + id + "\",\"state\":\"gone\"}"
		if j := st.ClaimJSON(id); j != "null" {
			want = "{\"pos\":" + itoa(pos) + "," + j[1:]
		}
		if e != want {
			t.Fatalf("an entry is not claim/<id>'s object with its place first:\n%s\n%s", e, want)
		}
	}
	if last != waitingLast {
		t.Fatalf("the last entry's place %d, the last place given %d", last, waitingLast)
	}
	if Render("waiting/0") != Render("waiting/1") {
		t.Fatalf("waiting/0 is not waiting/1")
	}
	for _, bad := range []string{"", "x", "-1", "+1", " 1", "1 ", "0x1", "1.0", "9223372036854775808", itoa(waitingLast + 1)} {
		if p := Render("waiting/" + bad); p != "[]" {
			t.Fatalf("waiting/%q: %s", bad, head(p))
		}
	}
	if p, at := Render("waiting/"+itoa(placeOf(c2))), placeOf(c2); !strings.HasPrefix(p, "[{\"pos\":"+itoa(at)+",\"id\":\""+c2+"\"") {
		t.Fatalf("a page from c2's place %d: %s", at, head(p))
	}
	if !strings.Contains(Render("json"), ",\"waitingLast\":"+itoa(waitingLast)+"}") {
		t.Fatalf("the json's last place: %s", Render("json"))
	}

	// c1 voided as withheld after 7 days — its entry stays, in that state, until it opens again — then restored at once:
	// a new place, above every place before
	p1 := placeOf(c1)
	asMember(cur)
	pass(cur, NewVoidRequest(cross(cur), c1, false, "", ""))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if st.ClaimOf(c1).State != hlroute.Withheld || placeOf(c1) != p1 || !strings.Contains(Render("waiting/"+itoa(p1)), "\"state\":\"withheld\"") {
		t.Fatalf("c1 voided: %s, place %d (was %d)", st.ClaimOf(c1).State, placeOf(c1), p1)
	}
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), c1))
	if st.ClaimOf(c1).State != hlroute.Open || placeOf(c1) != waitingLast || waitingLast <= p1 {
		t.Fatalf("c1 restored at once: %s, place %d, the last %d (was %d)", st.ClaimOf(c1).State, placeOf(c1), waitingLast, p1)
	}
	checkWaiting(t, "restored at once")

	// c2 voided, then credited by vote to bob: out of alice's queue place, into bob's, at a new place
	asMember(cur)
	pass(cur, NewVoidRequest(cross(cur), c2, false, "", ""))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	p2 := placeOf(c2)
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), c2, bob, amount))
	vote := st.Ledger.Last()
	if placeOf(c2) != p2 {
		t.Fatalf("a credit by vote moved c2 before its 7 days")
	}
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), vote)
	if c := st.ClaimOf(c2); c == nil || c.To != bob || c.State != hlroute.Open || placeOf(c2) != waitingLast || waitingLast <= p2 {
		t.Fatalf("c2 credited to bob by vote: %+v, place %d, the last %d", st.ClaimOf(c2), placeOf(c2), waitingLast)
	}
	checkWaiting(t, "credited by vote")

	// Claim pays c1 (carol's) and c2 (bob's) in full: their entries stay, shown "done", until the credits' tidy passes
	for i := 0; i < 5 && (st.ClaimOf(c1).State != hlroute.Done || st.ClaimOf(c2).State != hlroute.Done); i++ {
		skipDays(1)
		asRelayer(cur)
		Claim(cross(cur), 20)
	}
	for _, id := range []string{c1, c2} {
		if c := st.ClaimOf(id); c.State != hlroute.Done || placeOf(id) == 0 || !strings.Contains(Render("waiting/"+itoa(placeOf(id))), "\"id\":\""+id+"\",\"to\":") {
			t.Fatalf("%s paid: %s, place %d", id, c.State, placeOf(id))
		}
	}
	if !strings.Contains(Render("waiting/"+itoa(placeOf(c1))), "\"state\":\"done\"") {
		t.Fatalf("c1's entry does not show it done: %s", head(Render("waiting/"+itoa(placeOf(c1)))))
	}
	checkWaiting(t, "paid")
	// credits: each one's tidy looks at two entries on from where the last stopped, so as many credits as the index held
	// take every entry no longer open out — c1's and c2's with them
	n := waitingByPos.Size()
	for i := 0; i < n; i++ {
		burnTo(cur, kc, amount)
	}
	if placeOf(c1) != 0 || placeOf(c2) != 0 {
		t.Fatalf("after %d credits c1 is at %d, c2 at %d", n, placeOf(c1), placeOf(c2))
	}
	if stale := checkWaiting(t, "tidied"); stale != 0 {
		t.Fatalf("%d entries no longer open after %d credits' tidy", stale, n)
	}
}

// TestLaterInstallments (the round-5 go-spend audit's E5-GS1, the realm half; installments.gno, the same in the three
// routes, which hlgnot's TestLaterInstallments drives whole): a recipient mid-installments whose meters refilled less
// than the fee is passed over by Claim — where p/hlroute paid it whatever had refilled — and paid a fee's worth once
// they hold it.
func TestLaterInstallments(cur realm, t *testing.T) {
	skipDays(400) // past the earlier test functions' account checkpoints and meters (the bubble realm's clock rule)
	fee, rate := Fee(), Cap()/4
	lockUp(cur, 3*fee)
	r := testutils.TestAddress("hlb-installments")
	k, _ := hl.AddressToBytes32(r)
	id := burnTo(cur, k, 3*fee)
	if c := st.ClaimOf(id); c == nil || c.State != hlroute.Open {
		t.Fatalf("the claim: %v", c)
	}
	heights := func(n int64) int64 { return n*86400/rate/5 + 2 }
	testing.SkipHeights(heights(fee))
	asRelayer(cur)
	Claim(cross(cur), 20)
	first := st.ClaimOf(id).Paid
	if first < fee || bubble.BalanceOf(r) != first-fee {
		t.Fatalf("the first installment: %d, received %d", first, bubble.BalanceOf(r))
	}
	testing.SkipHeights(12) // a minute: the meter holds ≈7% of the fee
	asRelayer(cur)
	Claim(cross(cur), 20)
	if c := st.ClaimOf(id); c.Paid != first || bubble.BalanceOf(r) != first-fee {
		t.Fatalf("a minute on, a sliver of %d was paid (the fee is %d)", c.Paid-first, fee)
	}
	testing.SkipHeights(heights(fee))
	asRelayer(cur)
	Claim(cross(cur), 20)
	if c := st.ClaimOf(id); c.Paid-first < fee || bubble.BalanceOf(r) != c.Paid-fee {
		t.Fatalf("a fee's worth refilled: an installment of %d (the fee is %d), received %d", c.Paid-first, fee, bubble.BalanceOf(r))
	}
	tidy(waitingByPos.Size()) // the claims these payouts finished leave the index, as the next credits' tidy would take them: the probes count from a tidy index
	if stale := checkWaiting(t, "TestLaterInstallments"); stale != 0 {
		t.Fatalf("%d stale entries after a whole tidy", stale)
	}
}

// TestReopens (the round-5 realms audit's E5-RL2): a restore reads its ids once — restoreList, the draft's own parse,
// is hlroute.ParseIDs's list whatever the spelling — and names 128 at most (maxRestoreIDs); its closure enters the
// claims it restored at once and keeps only the others for Execute, which enters those it opens and moves none the
// closure entered; a cancel drops what the decision it cancels kept; the index keeps its contract throughout.
func TestReopens(cur realm, t *testing.T) {
	skipDays(7)
	st.Send(now(), probeID(0xe6, 0), alice, 1_000*Fee()) // a lock the claims stand under (nothing is paid here)
	epoch, _ := hlmailbox.PayoutsEpoch(pkgPath)
	credit := func(i int, sweep bool) string {
		id := probeID(0xe7, i)
		st.Credit(now(), runtime.ChainHeight(), epoch, id, probeTo(1_000_000+i), Fee()+one, hlroute.BySigners, sweep)
		enter(id) // as Process does; a sweep entry is no claim, and enter passes it by
		return id
	}
	void := func(ids string, forged bool) {
		asMember(cur)
		pass(cur, NewVoidRequest(cross(cur), ids, forged, "", ""))
		skipDays(7)
		asRelayer(cur)
		Execute(cross(cur), st.Ledger.Last())
	}
	w, f, sw := credit(0, false), credit(1, false), credit(2, true)
	void(w, false)
	void(f+","+sw, true)
	if st.ClaimOf(w).State != hlroute.Withheld || st.ClaimOf(f).State != hlroute.Forged || !st.SweepOf(sw).Voided {
		t.Fatalf("the voids: %s %s %v", st.ClaimOf(w).State, st.ClaimOf(f).State, st.SweepOf(sw).Voided)
	}

	// the list a restore keeps is its draft's, read once: the same ids, in order, as hlroute.ParseIDs reads the spelling
	spelled := " 0X" + strings.ToUpper(w) + ",, " + f + " ,0x" + sw + ","
	list, ok := restoreList(st.DraftRestore(spelled))
	want := hlroute.ParseIDs(spelled)
	if !ok || strings.Join(list, ",") != strings.Join(want, ",") {
		t.Fatalf("restoreList %v %v, ParseIDs %v", ok, list, want)
	}
	for _, d := range []hlroute.Draft{{Kind: hlroute.KindCancel, Text: "restore: " + w}, {Kind: hlroute.KindRestore, Text: "restore: " + w[:63]}, {Kind: hlroute.KindRestore, Text: "restore: " + w + ";" + f}, {Kind: hlroute.KindRestore, Text: "restore: "}} {
		if l, ok := restoreList(d); ok {
			t.Fatalf("restoreList read %v out of %q", l, d.Text)
		}
	}

	// w withheld is restored at once and enters in the closure; f forged and the sweep entry wait for Execute, and
	// only they are kept for it
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), spelled))
	dec := st.Ledger.Last()
	pw := placeOf(w)
	if st.ClaimOf(w).State != hlroute.Open || pw != waitingLast || st.ClaimOf(f).State != hlroute.Forged || strings.Join(reopened(dec), ",") != f+","+sw {
		t.Fatalf("restored at once: w %s at %d (the last %d), f %s, kept %v", st.ClaimOf(w).State, pw, waitingLast, st.ClaimOf(f).State, reopened(dec))
	}
	checkWaiting(t, "restored at once")
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), dec)
	if st.ClaimOf(f).State != hlroute.Open || placeOf(f) != waitingLast || placeOf(w) != pw || reopened(dec) != nil || st.SweepOf(sw).Voided {
		t.Fatalf("the restore applied: f %s at %d (the last %d), w at %d (was %d), kept %v", st.ClaimOf(f).State, placeOf(f), waitingLast, placeOf(w), pw, reopened(dec))
	}
	checkWaiting(t, "the restore applied")

	// a cancel drops what the decision it cancels kept
	c := credit(3, false)
	void(c, true)
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), c))
	dec2 := st.Ledger.Last()
	if strings.Join(reopened(dec2), ",") != c {
		t.Fatalf("the pending restore kept %v", reopened(dec2))
	}
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), dec2))
	if st.Ledger.Get(dec2).State() != "cancelled" || reopened(dec2) != nil {
		t.Fatalf("the cancelled restore: %s, kept %v", st.Ledger.Get(dec2).State(), reopened(dec2))
	}

	// 128 ids at most: 129 voided claims, the restore of all refused, of 128 restored at once and entered
	ids := make([]string, maxRestoreIDs+1)
	for i := range ids {
		ids[i] = credit(10+i, false)
	}
	void(strings.Join(ids, ","), false)
	asMember(cur)
	aborts(t, "hlbubble: a restore names 129 ids, 128 at most: restore them by more than one decision", func() { NewRestoreRequest(cross(cur), strings.Join(ids, ",")) })
	last := waitingLast
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), strings.Join(ids[:maxRestoreIDs], ",")))
	for i, id := range ids[:maxRestoreIDs] {
		if st.ClaimOf(id).State != hlroute.Open || placeOf(id) != last+int64(i)+1 {
			t.Fatalf("the restore of 128: %s at %d", st.ClaimOf(id).State, placeOf(id))
		}
	}
	tidy(waitingByPos.Size()) // the claims still voided leave the index, as the next credits' tidy would take them: the probes count from a tidy index
	if stale := checkWaiting(t, "TestReopens"); stale != 0 {
		t.Fatalf("%d stale entries after a whole tidy", stale)
	}
}

// The probes' route: probeClaims claims waiting, each to a recipient of its own, credited as Process credits them
// (st.Credit, then enter and tidy), each beside a send of its own (st.Send: the lock they are owed from, and send/<id>'s
// records) — past the ≈1,600 at which p/hlroute's claims/<from> walk is out of a query's gas on a node (E4-RL1).
const probeClaims = 2000

// probeID is the i-th probe record's id, kind a byte of its own (a send's, a claim's).
func probeID(kind byte, i int) string {
	var b [32]byte
	b[0], b[1], b[2], b[3], b[31] = byte(i>>24), byte(i>>16), byte(i>>8), byte(i), kind
	return hex.EncodeToString(b[:])
}

// probeTo is the i-th probe claim's recipient: an address of a user's length, canonical.
func probeTo(i int) address { return chain.PackageAddress("probe/recipient/" + strconv.Itoa(i)) }

var probeFirst int64 // the place of the first probe claim

func TestWaitingSetup(cur realm, t *testing.T) {
	epoch, _ := hlmailbox.PayoutsEpoch(pkgPath)
	n := waitingByPos.Size()
	probeFirst = waitingLast + 1
	for i := 0; i < probeClaims; i++ {
		st.Send(now(), probeID(0x5e, i), probeTo(i), 1_000*one)
		st.Credit(now(), runtime.ChainHeight(), epoch, probeID(0xc1, i), probeTo(i), 500*one, hlroute.BySigners, false)
		enter(probeID(0xc1, i))
		tidy(tidyStep)
	}
	if waitingByPos.Size() != n+probeClaims || waitingPos.Size() != n+probeClaims || placeOf(probeID(0xc1, probeClaims-1)) != waitingLast {
		t.Fatalf("the probe claims: %d entries (were %d)", waitingByPos.Size(), n)
	}
}

// TestRenderGasWaitingPage: the waiting index's deepest full page, at 2,000 waiting claims — a seek and a hundred
// entries, whatever lies before (what claims/<from> walked: ≈1.5M gas a claim before it on a node).
func TestRenderGasWaitingPage(cur realm, t *testing.T) {
	if p := Render("waiting/" + itoa(waitingLast-hlroute.PageLines+1)); len(p) < hlroute.PageLines*200 {
		t.Fatalf("not a full page: %d bytes", len(p))
	}
}

// TestRenderGasRouteClaim: claim/<id> of the newest of 2,000 claims.
func TestRenderGasRouteClaim(cur realm, t *testing.T) {
	if p := Render("claim/" + probeID(0xc1, probeClaims-1)); len(p) < 200 {
		t.Fatalf("claim/<id>: %s", p)
	}
}

// TestRenderGasRouteSend: send/<id> of the newest of 2,000 sends.
func TestRenderGasRouteSend(cur realm, t *testing.T) {
	if p := Render("send/" + probeID(0x5e, probeClaims-1)); len(p) < 150 {
		t.Fatalf("send/<id>: %s", p)
	}
}

// TestRenderGasRouteJSON: the json every machine reads the route by.
func TestRenderGasRouteJSON(cur realm, t *testing.T) {
	if p := Render("json"); len(p) < 800 {
		t.Fatalf("json: %s", p)
	}
}

// TestWalkClaimsDeep is no probe of the gate: claims/<from> — p/hlroute's page, which the machines no longer read —
// from the 1,900th waiting claim, its "--- GAS:" line kept for the record (≈0.76B here; out of gas on a node, E4-RL1).
func TestWalkClaimsDeep(cur realm, t *testing.T) {
	if p := Render("claims/1900"); len(p) < 200 {
		t.Fatalf("claims/1900: %d bytes", len(p))
	}
}

// gasCancel is the route's longest decision a builder writes: the cancel of a void naming 256 ids with the unpause
// option, whose text nests the void's (≈16.8 KB).
var gasCancel int64

func TestRouteDecisionsSetup(cur realm, t *testing.T) {
	ids := make([]string, hlroute.MaxIDs)
	for i := range ids {
		ids[i] = probeID(0xc1, i)
	}
	asMember(cur)
	pass(cur, NewVoidRequest(cross(cur), strings.Join(ids, ","), false, "sends,credits,payouts", ""))
	void := st.Ledger.Last()
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), void))
	gasCancel = st.Ledger.Last()
	if d := st.Ledger.Get(gasCancel); d == nil || d.Kind != hlroute.KindCancel || len(d.Text) < 16_600 {
		t.Fatalf("the longest decision: %+v", d)
	}
}

// TestRenderGasRouteDecisions: the route's decisions.json/<from> page of its longest decision, alone on its page.
func TestRenderGasRouteDecisions(cur realm, t *testing.T) {
	if p := Render("decisions.json/" + itoa(gasCancel)); len(p) < 16_600 {
		t.Fatalf("the longest decision's page: %d bytes", len(p))
	}
}

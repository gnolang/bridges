package hlgnot

import (
	"chain"
	"strconv"
	"strings"
	"testing"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
)

// The void gate's probes (the round-6 realms audit's E6-RL1): eth/scripts/gnotest.sh runs this suite a third time with
// -run 'TestSetup$|TestTxVoid' — TestSetup launches the route, TestTxVoidSetup credits the probes' claims as Process
// credits them (the credit tally included), stands three guardians' lists of 256 ids (seats 1, 2, 3) and seat 4's
// flag over a range, and queues a range void — and holds each probe's gas, one transaction's work at a void's bound,
// under a third of gnoland-1's block at its node figure (the void gate; the factors and the node figures they come
// from are gnotest.sh's). The transactions, each as the governor makes it before the yield, when the builder and the
// closure run in one (after it they are two):
//
//	TestTxVoidList            Govern of a void at once of maxVoidIDs ids, every one named by seat 3's list (Flagged
//	                          searches seats 1 and 2's first)
//	TestTxVoidRange           Govern of a range void at once of maxVoidCredits credits, one a height, named by seat 4's
//	                          range (Flagged searches the three lists first)
//	TestTxVoidRangeExecute    the Execute of a range void of maxVoidCredits credits, one a height, queued 7 days
//	TestTxVoidRestore         Govern of a restore at once of maxRestoreIDs claims voided as withheld (seat 3's, the
//	                          list void's): p/hlroute's parse, checks and restores and the waiting index's new places
//	TestTxVoidRestoreExecute  the Execute of a queued restore of maxRestoreIDs claims voided as forged (seat 1's)
//
// (a restore is a void's cure, bounded by the same gate: maxRestoreIDs, waiting.gno). In the whole suite the route has
// migrated already (TestMigrate): all skip.

var (
	txvoidReady   bool
	txvoidQueued  int64 // the queued range void TestTxVoidRangeExecute applies
	txvoidRestore int64 // the queued restore of forged claims TestTxVoidRestoreExecute applies
)

// txvoidIDs are the ids of the probe claims [from, to), comma separated.
func txvoidIDs(from, to int) string {
	ids := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		ids = append(ids, txgasID(0xc1, i))
	}
	return strings.Join(ids, ",")
}

// TestTxVoidSetup: 768 claims — the three lists' — at heights no range names (txvoidFar on), then 2×maxVoidCredits at
// heights 1 to 2×maxVoidCredits, one a height (the tally's costliest reading), each to a recipient of its own beside a
// send of its own, credited as Process credits them (st.Credit, the tally, enter, tidy); seats 1–3 name the first 768
// in three lists of 256, seat 4 the range heights:1-maxVoidCredits; the void of the next maxVoidCredits heights, which
// no flag names, queued.
func TestTxVoidSetup(cur realm, t *testing.T) {
	if Migrated() != "" {
		t.Skip("the route has migrated (the whole suite): the void gate's probes run alone")
	}
	const txvoidFar = int64(1_000_000_000_000)
	epoch, _ := hlmailbox.PayoutsEpoch(pkgPath)
	credit := func(i int, h int64) {
		to := chain.PackageAddress("probe/recipient/" + strconv.Itoa(i))
		st.Send(now(), txgasID(0x5e, i), to, 30*gnot)
		st.Credit(now(), h, epoch, txgasID(0xc1, i), to, 25*gnot, hlroute.BySigners, false)
		countCredit(h)
		enter(txgasID(0xc1, i))
		tidy(tidyStep)
	}
	n := 0
	for ; n < 3*256; n++ {
		credit(n, txvoidFar+int64(n-n%100))
	}
	for k := 0; k < 2*maxVoidCredits; k++ {
		credit(n, int64(k+1))
		n++
	}
	for seat := 1; seat <= 3; seat++ {
		testing.SetRealm(testing.NewUserRealm(hlmailbox.Seat(seat)))
		hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, txvoidIDs((seat-1)*256, seat*256))
	}
	testing.SetRealm(testing.NewUserRealm(hlmailbox.Seat(4)))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, "heights:1-"+itoa(maxVoidCredits))
	testing.SkipHeights(2*maxVoidCredits + 2) // a range closes at the block before its proposal's (voids.gno): past both
	asMember(cur)
	pass(cur, NewVoidRequest(cross(cur), "heights:"+itoa(maxVoidCredits+1)+"-"+itoa(2*maxVoidCredits), false, "", ""))
	txvoidQueued = st.Ledger.Last()
	a, q := creditsIn(hlroute.Range{From: 1, To: maxVoidCredits}), creditsIn(hlroute.Range{From: maxVoidCredits + 1, To: 2 * maxVoidCredits})
	if d := st.Ledger.Get(txvoidQueued); d == nil || !d.Pending() || a != maxVoidCredits || q != maxVoidCredits {
		t.Fatalf("the queued range void: %+v; the tally over the two ranges %d and %d", d, a, q)
	}
	txvoidReady = true
}

// TestTxVoidList: the governor's Govern of a void at once of maxVoidIDs ids of seat 3's list.
func TestTxVoidList(cur realm, t *testing.T) {
	if !txvoidReady {
		t.Skip("no probe state (the whole suite)")
	}
	ids := txvoidIDs(512, 512+maxVoidIDs)
	asMember(cur)
	pass(cur, NewVoidRequest(cross(cur), ids, false, "", ""))
	if d := st.Ledger.Get(st.Ledger.Last()); d.Pending() || !strings.Contains(d.Note, itoa(maxVoidIDs)+" voided at once") {
		t.Fatalf("the list void: %s", d.Note)
	}
}

// TestTxVoidRange: the governor's Govern of a range void at once of maxVoidCredits credits under seat 4's range (a test
// function's height starts over: past the range first, a few heights' work).
func TestTxVoidRange(cur realm, t *testing.T) {
	if !txvoidReady {
		t.Skip("no probe state (the whole suite)")
	}
	testing.SkipHeights(maxVoidCredits + 2)
	asMember(cur)
	pass(cur, NewVoidRequest(cross(cur), "heights:1-"+itoa(maxVoidCredits), false, "", ""))
	if d := st.Ledger.Get(st.Ledger.Last()); d.Pending() || !strings.Contains(d.Note, itoa(maxVoidCredits)+" voided at once") {
		t.Fatalf("the range void: %s", d.Note)
	}
}

// TestTxVoidRangeExecute: the Execute of the range void of maxVoidCredits credits TestTxVoidSetup queued, once due (a
// test function's clock starts over: the days are skipped here, a few heights' work).
func TestTxVoidRangeExecute(cur realm, t *testing.T) {
	if !txvoidReady {
		t.Skip("no probe state (the whole suite)")
	}
	for now() < st.Ledger.Get(txvoidQueued).Due {
		skipDays(1)
	}
	asRelayer(cur)
	Execute(cross(cur), txvoidQueued)
	if d := st.Ledger.Get(txvoidQueued); d.Pending() || !strings.Contains(d.Note, itoa(maxVoidCredits)+" voided") {
		t.Fatalf("the queued range void: %s", d.Note)
	}
}

// TestTxVoidRestoreSetup: the restores' state, after the voids' probes — the claims [512, 512+maxRestoreIDs) of seat
// 3's list voided as withheld (TestTxVoidList voided the first maxVoidIDs of them; any more a restore may name, here by
// voids at once of maxVoidIDs at most), and [0, maxRestoreIDs) of seat 1's voided as forged at once (its list names
// them), their restore proposed: queued 7 days, as a forged claim's restore is.
func TestTxVoidRestoreSetup(cur realm, t *testing.T) {
	if !txvoidReady {
		t.Skip("no probe state (the whole suite)")
	}
	void := func(from, to int, forged bool) {
		for i := from; i < to; i += maxVoidIDs {
			j := i + maxVoidIDs
			if j > to {
				j = to
			}
			asMember(cur)
			pass(cur, NewVoidRequest(cross(cur), txvoidIDs(i, j), forged, "", ""))
		}
	}
	void(512+maxVoidIDs, 512+maxRestoreIDs, false)
	void(0, maxRestoreIDs, true)
	for i := 0; i < maxRestoreIDs; i++ {
		if w, f := st.ClaimOf(txgasID(0xc1, 512+i)).State, st.ClaimOf(txgasID(0xc1, i)).State; w != hlroute.Withheld || f != hlroute.Forged {
			t.Fatalf("the restores' claims %d: %s and %s", i, w, f)
		}
	}
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), txvoidIDs(0, maxRestoreIDs)))
	txvoidRestore = st.Ledger.Last()
	if d := st.Ledger.Get(txvoidRestore); d == nil || !d.Pending() || !strings.Contains(d.Note, itoa(maxRestoreIDs)+" forged, or written off, queued 7 days") {
		t.Fatalf("the queued restore of the forged claims: %+v", d)
	}
}

// TestTxVoidRestore: the governor's Govern of a restore at once of maxRestoreIDs claims of seat 3's list voided as
// withheld — a restore's costliest transaction: p/hlroute's parse, checks and restores, and the waiting index's new
// places, with the builder's own parse and checks, in one.
func TestTxVoidRestore(cur realm, t *testing.T) {
	if !txvoidReady {
		t.Skip("no probe state (the whole suite)")
	}
	if txvoidRestore == 0 {
		t.Fatal("no restores' state: TestTxVoidRestoreSetup did not run")
	}
	asMember(cur)
	pass(cur, NewRestoreRequest(cross(cur), txvoidIDs(512, 512+maxRestoreIDs)))
	if d := st.Ledger.Get(st.Ledger.Last()); d.Pending() || !strings.Contains(d.Note, itoa(maxRestoreIDs)+" withheld restored at once") {
		t.Fatalf("the restore at once: %s", d.Note)
	}
}

// TestTxVoidRestoreExecute: the Execute of the restore of maxRestoreIDs forged claims TestTxVoidRestoreSetup queued,
// once due (a test function's clock starts over: the days are skipped here, a few heights' work).
func TestTxVoidRestoreExecute(cur realm, t *testing.T) {
	if !txvoidReady {
		t.Skip("no probe state (the whole suite)")
	}
	if txvoidRestore == 0 {
		t.Fatal("no restores' state: TestTxVoidRestoreSetup did not run")
	}
	for now() < st.Ledger.Get(txvoidRestore).Due {
		skipDays(1)
	}
	asRelayer(cur)
	Execute(cross(cur), txvoidRestore)
	if d := st.Ledger.Get(txvoidRestore); d.Pending() || !strings.Contains(d.Note, itoa(maxRestoreIDs)+" restored") {
		t.Fatalf("the queued restore: %s", d.Note)
	}
}

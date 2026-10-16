package hlgnot

import (
	"sort"
	"strings"
	"testing"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	"gno.land/p/nt/testutils/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
)

// The installments rule's two guards no test held (the round-6 realms audit's E6-RL4; installments.gno): feePaid set
// afresh at each turn, and the bridge meter's half of isSliver. Each test needs a ring of its own recipients and their
// meters at known levels, so eth/scripts/gnotest.sh runs them alone, on a route TestSetup has just launched (-run
// 'TestSetup$|TestAlone'); in the whole suite the route has migrated (TestMigrate) and they skip. A test
// function's clock starts over, so each begins past every meter the ones before left.

// instLevels are the bridge meter's level and to's, now (st.MeterLevels: the refill a Claim's visit makes first).
func instLevels(to address) (bridge, recipient int64) {
	epoch, at := hlmailbox.PayoutsEpoch(pkgPath)
	return st.MeterLevels(now(), to, epoch, at)
}

// instClaim is one Claim by the relayer, and every claim's installment it paid (by id).
func instClaim(cur realm, ids []string) (int, map[string]int64) {
	before := map[string]int64{}
	for _, id := range ids {
		before[id] = st.ClaimOf(id).Paid
	}
	asRelayer(cur)
	n := Claim(cross(cur), 20)
	paid := map[string]int64{}
	for _, id := range ids {
		if d := st.ClaimOf(id).Paid - before[id]; d != 0 {
			paid[id] = d
		}
	}
	return n, paid
}

// TestAloneFeeEachTurn: a turn that pays a first installment — the fee first — lets only that turn's payment
// through unchecked. In one Claim, rb's first installment is paid and ra's later one, which ra's own meter holds
// under the fee, is passed over in the turns after it; a feePaid carried from turn to turn (set once a call) pays ra
// that sliver.
func TestAloneFeeEachTurn(cur realm, t *testing.T) {
	if Migrated() != "" {
		t.Skip("the route has migrated (the whole suite): the installments tests run alone, on a fresh route")
	}
	skipDays(400)
	fee, rate := Fee(), Cap()/4
	st.Send(now(), strings.Repeat("f0", 32), alice, 1_000*fee) // a lock the claims are owed from (the cap stays the floor)
	fundLock()
	ra, rb := testutils.TestAddress("hlg-feeturn-a"), testutils.TestAddress("hlg-feeturn-b")
	idA, idB, idB2 := strings.Repeat("f1", 32), strings.Repeat("f2", 32), strings.Repeat("f3", 32)
	ids := []string{idA, idB}
	heights := func(n int64) int64 { return n*86400/rate/5 + 2 } // the blocks a recipient's meter takes to refill n
	// rb's queue first, its one claim exactly the fee (paid whole at its first installment); ra's, 300 fees
	instCredit(t, idB, rb, fee)
	instCredit(t, idA, ra, 300*fee)
	testing.SkipHeights(heights(fee))
	if n, _ := instClaim(cur, ids); n != 2 || st.ClaimOf(idB).State != hlroute.Done || st.ClaimOf(idA).Paid < fee {
		t.Fatalf("the first installments: %d paid, rb %s, ra paid %d", n, st.ClaimOf(idB).State, st.ClaimOf(idA).Paid)
	}
	// a day on, ra alone waits: its later installment empties its meter; rb's queue, with nothing open, fills
	skipDays(1)
	if n, paid := instClaim(cur, ids); n != 1 || paid[idA] < fee {
		t.Fatalf("ra's later installment: %d paid, %v", n, paid)
	}
	// rb's second claim, its meter full; ten seconds on ra's meter holds a sliver of the fee, the bridge meter plenty
	ids = append(ids, idB2)
	instCredit(t, idB2, rb, 3*fee)
	testing.SkipHeights(2)
	bridge, aLevel := instLevels(ra)
	_, bLevel := instLevels(rb)
	if aLevel <= 0 || aLevel >= fee || bridge < 4*fee || bLevel < 3*fee {
		t.Fatalf("the meters before the Claim: bridge %d, ra %d, rb %d (the fee %d)", bridge, aLevel, bLevel, fee)
	}
	firstA := st.ClaimOf(idA).Paid
	n, paid := instClaim(cur, ids)
	if c := st.ClaimOf(idB2); n != 1 || c.State != hlroute.Done || paid[idB2] != 3*fee {
		t.Fatalf("rb's first installment: %d paid, %v, %s", n, paid, c.State)
	}
	if d := st.ClaimOf(idA).Paid - firstA; d != 0 {
		t.Fatalf("ra was paid a later installment of %d under the fee %d (its meter held %d) in a Claim whose other turn paid a fee", d, fee, aLevel)
	}
	checkWaiting(t, "TestAloneFeeEachTurn")
}

// TestAloneBridgeMeter: a later installment the bridge meter bounds under the fee is passed over though the
// recipient's own meter holds far more. Five recipients of this test alone: four drain the bridge meter in one Claim
// (their full meters, a quarter of the cap each, against a bridge meter short of the cap), the fifth's own meter holding
// a sliver then; twenty blocks on, the bridge meter has refilled under the fee, below the last drainer's meter and the
// fifth's: neither is paid. An isSliver that reads the recipient's meter alone pays one of them the bridge meter's
// sliver.
func TestAloneBridgeMeter(cur realm, t *testing.T) {
	if Migrated() != "" {
		t.Skip("the route has migrated (the whole suite): the installments tests run alone, on a fresh route")
	}
	skipDays(400)
	fee, rate := Fee(), Cap()/4
	st.Send(now(), strings.Repeat("f4", 32), alice, 1_000*fee)
	fundLock()
	// the ring of the test's own recipients: what an earlier test left waiting is paid off first
	for i := 0; i < 20 && len(idsIn(st.ClaimsPage(0))) > 0; i++ {
		skipDays(1)
		asRelayer(cur)
		Claim(cross(cur), 20)
	}
	if left := idsIn(st.ClaimsPage(0)); len(left) > 0 {
		t.Fatalf("%d claims still wait", len(left))
	}
	heights := func(n int64) int64 { return n*86400/rate/5 + 2 }
	// five recipients, the last in the ring's order the fifth (r5): after a Claim that visits it alone, the next
	// Claim's turns start at the first drainer
	names := []string{}
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		names = append(names, string(testutils.TestAddress("hlg-bridgebinds-"+n)))
	}
	sort.Strings(names)
	rs := []address{}
	for _, n := range names {
		rs = append(rs, address(n))
	}
	drainers, r5 := rs[:4], rs[4]
	idOf := func(i int, k string) string { return strings.Repeat(k+itoa(int64(i)), 32) } // 64 hex digits
	var ids []string
	// the drainers' queues, each a claim of exactly the fee, paid whole; r5's claim, 300 fees, its first installment
	for i, d := range drainers {
		ids = append(ids, idOf(i, "a"))
		instCredit(t, idOf(i, "a"), d, fee)
	}
	id5 := strings.Repeat("e5", 32)
	ids = append(ids, id5)
	instCredit(t, id5, r5, 300*fee)
	testing.SkipHeights(heights(fee))
	if n, _ := instClaim(cur, ids); n != 5 {
		t.Fatalf("the first installments: %d paid", n)
	}
	// a day on, r5 alone waits: a later installment empties its meter, the bridge meter a quarter short of the cap
	skipDays(1)
	if n, paid := instClaim(cur, ids); n != 1 || paid[id5] < fee {
		t.Fatalf("r5's later installment: %d paid, %v", n, paid)
	}
	// the drainers' second claims, each above a quarter of the cap, their meters full
	for i, d := range drainers {
		ids = append(ids, idOf(i, "b"))
		instCredit(t, idOf(i, "b"), d, 150*fee)
	}
	// ten minutes on (r5's meter under the fee): one Claim, the drainers' first installments empty the bridge meter
	testing.SkipHeights(120)
	if _, l := instLevels(r5); l <= 0 || l >= fee {
		t.Fatalf("r5's meter before the drain: %d (the fee %d)", l, fee)
	}
	instClaim(cur, ids)
	if bridge, _ := instLevels(r5); bridge != 0 {
		t.Fatalf("the bridge meter after the drain: %d", bridge)
	}
	// twenty blocks on: the bridge meter under the fee; some recipient's own meter above it, a later installment due
	testing.SkipHeights(20)
	bridge, l5 := instLevels(r5)
	binds := 0
	for i, d := range drainers {
		if c := st.ClaimOf(idOf(i, "b")); c.Paid > 0 && c.Remaining() > bridge {
			if _, l := instLevels(d); l > bridge {
				binds++
			}
		}
	}
	if l5 > bridge && st.ClaimOf(id5).Remaining() > bridge {
		binds++
	}
	if bridge <= 0 || bridge >= fee || binds == 0 {
		t.Fatalf("the meters: the bridge %d (the fee %d), r5 %d; %d recipients whose own meter holds more than the bridge", bridge, fee, l5, binds)
	}
	n, paid := instClaim(cur, ids)
	if n != 0 || len(paid) != 0 {
		t.Fatalf("the bridge meter held %d, under the fee %d: %d claims were paid a sliver it bounds, %v", bridge, fee, n, paid)
	}
	checkWaiting(t, "TestAloneBridgeMeter")
}

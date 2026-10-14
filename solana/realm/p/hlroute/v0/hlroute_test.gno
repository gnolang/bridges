package hlroute

import (
	"strconv"
	"strings"
	"testing"

	"gno.land/p/nt/testutils/v0"
	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
)

const (
	gnot = int64(1_000_000)
	hour = int64(3600)
	t0   = int64(1_700_000_000)
)

var (
	alice = testutils.TestAddress("hlr-alice")
	bob   = testutils.TestAddress("hlr-bob")
	carol = testutils.TestAddress("hlr-carol")
	dave  = testutils.TestAddress("hlr-dave")
	erin  = testutils.TestAddress("hlr-erin")
)

func id(n int) string {
	const digits = "0123456789abcdef"
	return strings.Repeat("0", 62) + string(digits[n/16]) + string(digits[n%16])
}

func newState() *State {
	return New(Launch{Symbol: "GNOT", Fee: 5 * gnot, Minimum: 10 * gnot, FeeFloor: 2 * gnot, CapFloor: 10_000 * gnot,
		Maximum: 25_000 * gnot, Domain: 1399811150, Router: [32]byte{1}, Now: t0})
}

// recorder is a test Env: it books every payment and answers the flag, delivery, epoch, height and seat questions
// from maps and fields.
type recorder struct {
	paid    map[string]int64 // recipient → total
	fees    int64
	flagged map[string]bool // ids a guardian's payouts flag names …
	rng     *Range          // … or the range it names
	done    map[string]bool
	epoch   int64
	height  int64
	cleared []string
	removed []int
	changed map[int]bool // seats whose occupant changed since the proposal was made
	routes  []string
	announced map[string]string // successor paths the mailbox knows, each with the route it announced for (R25, AQ5)
	predecessor string          // the route this one's registration succeeds, "" for a launch route or a realm not yet registered (R31)
	isRoute     map[string]bool // the paths the mailbox registers as routes (R31): AddRoute adds to it
	enrolledH   int64           // the height and time kind 6 last enrolled this route on Solana as r/hlgov noted it (AR1): long ago unless a test says otherwise, so a refund's 7-day wait is met
	enrolledAt  int64
}

func newRecorder() *recorder {
	return &recorder{paid: map[string]int64{}, flagged: map[string]bool{}, done: map[string]bool{}, changed: map[int]bool{}, announced: map[string]string{}, isRoute: map[string]bool{}, enrolledH: 1, enrolledAt: 1}
}

// seatAddr is the key the test seats hold.
func seatAddr(n int) address { return testutils.TestAddress("hlr-seat-" + strconv.Itoa(n)) }

func (r *recorder) seatOf(seat int) address { return seatAddr(seat) }

func (r *recorder) env() Env {
	return Env{
		Flagged: func(id string, height int64, verifier bool) bool {
			return r.flagged[id] || (r.rng != nil && r.rng.Covers(height, verifier))
		},
		ClearFlags: func(fn string) bool { r.cleared = append(r.cleared, fn); return true },
		RemoveSeat: func(seat int, holder address) bool {
			r.removed = append(r.removed, seat)
			return holder == seatAddr(seat) && !r.changed[seat]
		},
		Delivered:  func(id string) bool { return r.done[id] },
		MarkDone:   func(id string) { r.done[id] = true },
		Epoch:      func() int64 { return r.epoch },
		Height:     func() int64 { return r.height },
		Pay:        func(to address, n int64) { r.paid[string(to)] += n },
		PayFee:     func(n int64) { r.fees += n },
		AddRoute:     func(p string) { r.routes = append(r.routes, p); r.isRoute[p] = true },
		AnnouncedFor: func(p string) string { return r.announced[p] },
		Predecessor:  func() string { return r.predecessor },
		IsRoute:      func(p string) bool { return r.isRoute[p] },
		Enrolled:     func() (int64, int64) { return r.enrolledH, r.enrolledAt },
	}
}

func (r *recorder) total() int64 {
	n := r.fees
	for _, v := range r.paid {
		n += v
	}
	return n
}

// lock puts amount into the lock by sends.
func lock(s *State, amount int64, k int) {
	s.Send(t0, id(20+k), erin, amount)
}

// inherit takes a page carrying sends alone into s, as a predecessor's page would (the lock and sentTotal raised
// by their sum, which page 0 carries in a real migration): the sends a refund may name (AQ2).
func inherit(s *State, sends ...SendItem) {
	snap := &Snapshot{Page: s.imported, Sends: sends}
	for _, sd := range sends {
		snap.Lock += sd.Amount
		snap.SentTotal += sd.Amount
	}
	s.Import(t0, 0, 0, snap)
}

// applyRefund is GovDAO's own at-once apply of a queued refund decision (AT2), as a realm's NewApplyRefundRequest
// makes it: drafted and decided in one transaction at now.
func applyRefund(s *State, now, id int64, env Env) *decisions.Decision {
	return s.Decide(now, now, s.DraftApplyRefund(id, now, env), env)
}

func identity(t *testing.T, s *State, supply int64) {
	t.Helper()
	if s.Lock != s.SentTotal-s.PaidTotal {
		t.Fatalf("lock %d != sent %d - paid %d", s.Lock, s.SentTotal, s.PaidTotal)
	}
	if s.Lock != supply+s.Owed+s.Voided-s.Lost {
		t.Fatalf("lock %d != supply %d + owed %d + voided %d - lost %d", s.Lock, supply, s.Owed, s.Voided, s.Lost)
	}
}

func TestRefillArithmetic(t *testing.T) {
	// one hour from empty at 10,000 GNOT a day: floor(1e10 * 3600 / 86400) exactly, with no overflow
	var m Meter
	m.At = t0
	m.refill(t0+hour, 10_000*gnot, 10_000*gnot, 0, 0)
	if m.Level != 416_666_666 {
		t.Fatalf("an hour's refill %d", m.Level)
	}
	// the rest of the day fills it exactly (the remainder is carried), a week does not overfill it
	m.refill(t0+Day, 10_000*gnot, 20_000*gnot, 0, 0)
	if m.Level != 10_000*gnot || m.Frac != 0 {
		t.Fatalf("a day's refill %d frac %d", m.Level, m.Frac)
	}
	m.refill(t0+Day, 10_000*gnot, 10_000*gnot, 0, 0)
	m.refill(t0+8*Day, 10_000*gnot, 10_000*gnot, 0, 0)
	if m.Level != 10_000*gnot {
		t.Fatalf("overfilled %d", m.Level)
	}
	// a big cap: a tenth of a billion GNOT a day, a day's refill, no overflow
	big := 100_000_000 * gnot
	m = Meter{At: t0}
	m.refill(t0+Day, big, big, 0, 0)
	if m.Level != big {
		t.Fatalf("big refill %d", m.Level)
	}
	// a new epoch empties it as of the epoch's time and refills from there
	m.refill(t0+Day+hour, big, big, 1, t0+Day)
	if m.Level != big/24 {
		t.Fatalf("after the epoch %d, want %d", m.Level, big/24)
	}
}

// TestCapAndFirstDay: §5.4 — the cap is max(floor, a tenth of the lock); the meters start empty, so forged claims
// draw at most one cap in the first 24 hours; a single recipient draws a quarter. The exact figures are read where
// the floor binds (a lock under 100,000), since above it the cap follows the lock down as claims pay.
func TestCapAndFirstDay(t *testing.T) {
	s := newState()
	r := newRecorder()
	if s.Cap() != 10_000*gnot {
		t.Fatalf("cap with an empty lock %d", s.Cap())
	}
	lock(s, 200_000*gnot, 0)
	if s.Cap() != 20_000*gnot || s.Lock != 200_000*gnot || s.SentTotal != 200_000*gnot {
		t.Fatalf("cap %d lock %d", s.Cap(), s.Lock)
	}
	s = newState()
	lock(s, 50_000*gnot, 0) // the floor binds: cap 10,000, a quarter 2,500
	if s.Cap() != 10_000*gnot {
		t.Fatalf("cap under the floor %d", s.Cap())
	}
	// four recipients' worth of forged claims, credited the moment the realm is deployed (each the lock's worth: a
	// claim above the lock is swept, never a claim)
	for i, a := range []address{alice, bob, carol, dave} {
		s.Credit(t0, 0, 0, id(i), a, 50_000*gnot, BySigners, false)
	}
	if s.Owed != 200_000*gnot || s.nOpen != 4 {
		t.Fatalf("owed %d open %d", s.Owed, s.nOpen)
	}
	identity(t, s, 50_000*gnot-200_000*gnot) // a forged credit breaks the identity by its amount: the supply is really 50,000
	// nothing at once: the meters are empty
	if n := s.Claim(t0, 20, 0, 0, r.env()); n != 0 || r.total() != 0 {
		t.Fatalf("paid from an empty meter: %d %d", n, r.total())
	}
	// after an hour: each recipient's quarter has 1/24 of 2,500; the bridge 1/24 of 10,000
	n := s.Claim(t0+hour, 20, 0, 0, r.env())
	q := 2_500 * gnot / 24
	if n != 4 || r.total() != 4*q {
		t.Fatalf("after an hour: %d claims, %d paid, want %d", n, r.total(), 4*q)
	}
	if r.fees != 4*5*gnot || r.paid[string(alice)] != q-5*gnot {
		t.Fatalf("the fee first: fees %d alice %d", r.fees, r.paid[string(alice)])
	}
	// at 24 hours the day's total is exactly one cap
	s.Claim(t0+Day, 20, 0, 0, r.env())
	if r.total() != 10_000*gnot {
		t.Fatalf("the first day paid %d, want one cap %d", r.total(), 10_000*gnot)
	}
	if s.PaidTotal != 10_000*gnot || s.Lock != 40_000*gnot || s.Owed != 190_000*gnot {
		t.Fatalf("counters paid %d lock %d owed %d", s.PaidTotal, s.Lock, s.Owed)
	}
	// a fifth recipient gets nothing more that day: the bridge meter is empty
	s.Credit(t0+Day, 0, 0, id(4), erin, 100*gnot, BySigners, false)
	if n := s.Claim(t0+Day, 20, 0, 0, r.env()); n != 0 {
		t.Fatalf("paid past the cap")
	}
	// above the floor the cap follows the lock down as claims pay: a day's draw is at most the cap it began with
	s = newState()
	r = newRecorder()
	lock(s, 400_000*gnot, 0) // cap 40,000
	for i, a := range []address{alice, bob, carol, dave} {
		s.Credit(t0, 0, 0, id(i), a, 100_000*gnot, BySigners, false)
	}
	s.Claim(t0+Day, 20, 0, 0, r.env())
	if r.total() > 40_000*gnot || r.total() < 38_000*gnot || s.Cap() != s.Lock/10 {
		t.Fatalf("a day above the floor paid %d (cap now %d)", r.total(), s.Cap())
	}
}

// TestWorstDay: a full meter plus a day's refill — two caps in the worst 24 hours, never more.
func TestWorstDay(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 50_000*gnot, 0) // the floor binds: cap 10,000, a quarter 2,500
	for i, a := range []address{alice, bob, carol, dave} {
		s.Credit(t0, 0, 0, id(i), a, 50_000*gnot, BySigners, false)
	}
	// a quiet two days: every meter full (the bridge's 10,000, each quarter 2,500)
	s.Claim(t0+2*Day, 20, 0, 0, r.env())
	if r.total() != 10_000*gnot {
		t.Fatalf("a full meter paid %d", r.total())
	}
	// a day later, another cap: two caps in 24 hours
	s.Claim(t0+3*Day, 20, 0, 0, r.env())
	if r.total() != 20_000*gnot {
		t.Fatalf("the worst day paid %d, want two caps", r.total())
	}
	// and the ceiling bounds any single recipient to a quarter per day
	if r.paid[string(alice)]+5*gnot != 5_000*gnot {
		t.Fatalf("alice %d", r.paid[string(alice)])
	}
	if n := s.Claim(t0+3*Day, 20, 0, 0, r.env()); n != 0 {
		t.Fatalf("paid past the cap")
	}
	// a second later only a second's refill is paid: 10,000 GNOT a day is 0.115740 GNOT a second for the bridge,
	// a quarter of it for one recipient
	s.Claim(t0+3*Day+1, 20, 0, 0, r.env())
	if r.total() != 20_000*gnot+4*28_935 {
		t.Fatalf("a second's refill paid %d", r.total()-20_000*gnot)
	}
	identity(t, s, s.Lock-s.Owed)
}

// TestQuarterAndTurns: one recipient leaves at a quarter a day; recipients take turns; a flood aimed at one
// recipient slows only that recipient; a dry recipient is skipped.
func TestQuarterAndTurns(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 50_000*gnot, 0) // cap 10,000, quarter 2,500
	s.Credit(t0, 0, 0, id(0), alice, 50_000*gnot, BySigners, false)
	s.Claim(t0+Day, 20, 0, 0, r.env())
	if r.paid[string(alice)]+r.fees != 2_500*gnot {
		t.Fatalf("a day alone paid %d", r.paid[string(alice)]+r.fees)
	}
	// three quiet days: still a quarter, the ceiling
	s.Claim(t0+4*Day, 20, 0, 0, r.env())
	if r.paid[string(alice)]+r.fees != 5_000*gnot {
		t.Fatalf("after three quiet days paid %d", r.paid[string(alice)]+r.fees)
	}
	// a flood of 10 claims at bob slows bob alone: alice still gets her quarter the next day, bob his
	for i := 0; i < 10; i++ {
		s.Credit(t0+4*Day, 0, 0, id(1+i), bob, 200*gnot, BySigners, false)
	}
	before := r.paid[string(alice)]
	n := s.Claim(t0+5*Day, 20, 0, 0, r.env())
	if r.paid[string(alice)]-before != 2_500*gnot {
		t.Fatalf("alice's turn %d", r.paid[string(alice)]-before)
	}
	// bob's quarter is 2,500 a day: ten 200 claims, the fee first on each, paid in turns with alice
	if r.paid[string(bob)] != 10*195*gnot || n != 11 {
		t.Fatalf("bob %d in %d turns", r.paid[string(bob)], n)
	}
	// n bounds the turns: with two waiting, n=1 pays one, the cursor moves on
	s.Credit(t0+5*Day, 0, 0, id(15), carol, 300*gnot, BySigners, false)
	s.Credit(t0+5*Day, 0, 0, id(16), dave, 300*gnot, BySigners, false)
	if n := s.Claim(t0+6*Day, 1, 0, 0, r.env()); n != 1 {
		t.Fatalf("n=1 paid %d", n)
	}
	if n := s.Claim(t0+6*Day, 3, 0, 0, r.env()); n != 2 {
		t.Fatalf("the rest: %d", n)
	}
	if r.paid[string(carol)] != 295*gnot || r.paid[string(dave)] != 295*gnot {
		t.Fatalf("carol %d dave %d", r.paid[string(carol)], r.paid[string(dave)])
	}
	// bob's claims were paid in full: done, out of the queue, the record kept
	if c := s.ClaimOf(id(1)); c == nil || c.State != Done || c.Remaining() != 0 {
		t.Fatalf("bob's first claim %+v", c)
	}
	if s.nOpen != 1 || !strings.Contains(s.Figures(t0+6*Day, 0, 0, 0, ""), string(alice)+" #1:") {
		t.Fatalf("open %d\n%s", s.nOpen, s.Figures(t0+6*Day, 0, 0, 0, ""))
	}
	identity(t, s, s.Lock-s.Owed)
}

// TestFeeAndSweep: the fee is the first installment, whole, inside the meter; a burn below the fee and a burn to a
// realm address go to the sweep queue, owed and paying nobody until GovDAO directs it.
func TestFeeAndSweep(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 100_000*gnot, 0)
	if !s.Credit(t0, 0, 0, id(0), alice, 4*gnot, BySigners, false) || !s.Credit(t0, 0, 0, id(1), bob, 50*gnot, BySigners, true) {
		t.Fatalf("not swept")
	}
	if s.Sweep != 54*gnot || s.Owed != 54*gnot || s.SweptOf(id(1)) != 50*gnot || s.nOpen != 0 {
		t.Fatalf("sweep %d owed %d", s.Sweep, s.Owed)
	}
	// a burn above the lock (100,001 against 100,000) cannot be honest: swept, owed, paying nobody; one of the lock
	// itself is a claim
	if !s.Credit(t0, 0, 0, id(9), dave, 100_001*gnot, BySigners, false) || s.SweptOf(id(9)) != 100_001*gnot || s.ClaimOf(id(9)) != nil {
		t.Fatalf("a burn above the lock was not swept")
	}
	if s.Credit(t0, 0, 0, id(10), dave, 100_000*gnot, BySigners, false) || s.ClaimOf(id(10)) == nil {
		t.Fatalf("a burn of the lock was swept")
	}
	if s.Sweep != 100_055*gnot || s.Owed != 200_055*gnot {
		t.Fatalf("sweep %d owed %d", s.Sweep, s.Owed)
	}
	identity(t, s, s.Lock-s.Owed)
	s = newState()
	r = newRecorder()
	lock(s, 100_000*gnot, 0)
	if !s.Credit(t0, 0, 0, id(0), alice, 4*gnot, BySigners, false) || !s.Credit(t0, 0, 0, id(1), bob, 50*gnot, BySigners, true) {
		t.Fatalf("not swept")
	}
	if s.Claim(t0+Day, 20, 0, 0, r.env()) != 0 {
		t.Fatalf("paid a swept claim")
	}
	// a claim of exactly the fee pays only the fee
	if s.Credit(t0, 0, 0, id(2), carol, 5*gnot, BySigners, false) {
		t.Fatalf("swept a claim of the fee")
	}
	// a meter holding less than the fee is dry for a first installment
	s.Claim(t0+1, 20, 0, 0, r.env()) // 1 second of refill: ≈0.1157 GNOT
	if r.total() != 0 {
		t.Fatalf("paid part of a fee")
	}
	s.Claim(t0+hour, 20, 0, 0, r.env())
	if r.fees != 5*gnot || r.paid[string(carol)] != 0 || s.ClaimOf(id(2)).State != Done {
		t.Fatalf("fee-only claim: fees %d carol %d", r.fees, r.paid[string(carol)])
	}
	// the sweep payment: a 7-day decision naming bob's entry, then paid to dave, the entry dropped
	rec := r
	d := s.Decide(t0, t0, s.DraftSweep(id(1), dave), rec.env())
	if !d.Queued() || d.Kind != KindSweep || d.Text != "pay 50 GNOT of the sweep queue to "+string(dave)+": "+id(1) {
		t.Fatalf("sweep decision %+v", d)
	}
	s.Execute(t0+decisions.Delay, d.ID, rec.env())
	if rec.paid[string(dave)] != 50*gnot || s.Sweep != 4*gnot || s.Owed != 4*gnot || s.PaidTotal != 55*gnot || s.SweepOf(id(1)) != nil || d.Note != "applied after the delay; 1 entries paid (50 GNOT)" {
		t.Fatalf("after the sweep: dave %d sweep %d owed %d paid %d note %q", rec.paid[string(dave)], s.Sweep, s.Owed, s.PaidTotal, d.Note)
	}
	identity(t, s, s.Lock-s.Owed)
}

// TestVoidRestoreIdentity: §5.9 — a forged credit breaks the identity by its amount until it is voided as forged,
// which drops its unpaid remainder from owed and books its paid part as lost; a withheld void keeps it booked and
// restores at once; a forged void restores after 7 days; the void is per id, at once for the ids a guardian's
// standing payouts flag names.
func TestVoidRestoreIdentity(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 100_000*gnot, 0)
	s.Credit(t0, 0, 0, id(0), alice, 1_000*gnot, BySigners, false) // honest: alice burned 1,000 on Solana
	s.Credit(t0, 0, 0, id(1), bob, 2_000*gnot, BySigners, false)   // forged
	s.Credit(t0, 0, 0, id(2), carol, 3_000*gnot, BySigners, false) // forged
	supply := int64(99_000 * gnot)
	// the forgeries: the supply did not move for them, so the identity is off by 5,000
	if s.Lock-s.Owed-s.Voided+s.Lost != supply-5_000*gnot {
		t.Fatalf("identity before the void")
	}
	s.Claim(t0+Day, 20, 0, 0, r.env()) // every claim paid in full (quarter 2,500 ≥ each... carol's 3,000 is not)
	if r.paid[string(bob)] != 1_995*gnot || r.paid[string(carol)] != 2_495*gnot || r.paid[string(alice)] != 995*gnot {
		t.Fatalf("paid alice %d bob %d carol %d", r.paid[string(alice)], r.paid[string(bob)], r.paid[string(carol)])
	}
	// a guardian's flag names bob's id: the void is at once for it and queued for carol's
	r.flagged[id(1)] = true
	d := s.Decide(t0+Day, t0+Day, s.DraftVoid(id(1)+","+id(2), true, "payouts,credits", "5,2", 0, r.seatOf), r.env())
	if !d.Queued() || !strings.Contains(d.Note, "1 voided at once") || !strings.Contains(d.Note, "unpaused payouts,credits") || !strings.Contains(d.Note, "seats 5,2 vacated") || !strings.Contains(d.Note, "1 queued") {
		t.Fatalf("void decision %+v", d)
	}
	if len(r.cleared) != 2 || r.cleared[0] != "payouts" || r.cleared[1] != "credits" || len(r.removed) != 2 || r.removed[0] != 5 || r.removed[1] != 2 {
		t.Fatalf("the unpause option: cleared %v removed %v", r.cleared, r.removed)
	}
	if !strings.Contains(d.Text, "and unpause payouts,credits, removing seats 5:"+string(seatAddr(5))+",2:"+string(seatAddr(2))) {
		t.Fatalf("text %s", d.Text)
	}
	if c := s.ClaimOf(id(1)); c.State != Forged || s.Lost != 2_000*gnot {
		t.Fatalf("bob's claim %+v lost %d", c, s.Lost)
	}
	if s.ClaimOf(id(2)).State != Open {
		t.Fatalf("carol's void applied early")
	}
	s.Execute(t0+Day+decisions.Delay, d.ID, r.env())
	// carol's unpaid 500 is dropped from owed; the 2,000 and 2,500 paid are lost
	if c := s.ClaimOf(id(2)); c.State != Forged || s.Lost != 4_500*gnot || s.Owed != 0 {
		t.Fatalf("carol's claim %+v lost %d owed %d", c, s.Lost, s.Owed)
	}
	// the identity closes: lock = supply + owed + voided − lost
	identity(t, s, supply)
	// a withheld void of an open claim keeps the remainder booked; its restore is at once
	s.Credit(t0+2*Day, 0, 0, id(3), dave, 1_000*gnot, BySigners, false)
	d = s.Decide(t0+2*Day, t0+2*Day, s.DraftVoid(id(3), false, "", "", 0, r.seatOf), r.env())
	if !d.Queued() || s.ClaimOf(id(3)).State != Open {
		t.Fatalf("an unflagged void should wait: %+v", d)
	}
	s.Execute(t0+2*Day+decisions.Delay, d.ID, r.env())
	if s.ClaimOf(id(3)).State != Withheld || s.Voided != 1_000*gnot || s.Owed != 0 {
		t.Fatalf("withheld: voided %d owed %d", s.Voided, s.Owed)
	}
	identity(t, s, supply-1_000*gnot) // dave's burn did move the supply down by 1,000 (an honest claim withheld)
	d = s.Decide(t0+3*Day, t0+3*Day, s.DraftRestore(id(3)), r.env())
	if d.Queued() || s.ClaimOf(id(3)).State != Open || s.Voided != 0 || s.Owed != 1_000*gnot {
		t.Fatalf("restore of a withheld claim: %+v", d)
	}
	// re-voided as forged, and that restore waits 7 days and undoes it fully
	r.flagged[id(3)] = true
	s.Decide(t0+3*Day, t0+3*Day, s.DraftVoid(id(3), false, "", "", 0, r.seatOf), r.env())
	s.Decide(t0+3*Day, t0+3*Day, s.DraftVoid(id(3), true, "", "", 0, r.seatOf), r.env())
	if s.ClaimOf(id(3)).State != Forged || s.Voided != 0 || s.Lost != 4_500*gnot {
		t.Fatalf("re-void as forged: voided %d lost %d", s.Voided, s.Lost)
	}
	d = s.Decide(t0+3*Day, t0+3*Day, s.DraftRestore(id(3)), r.env())
	if !d.Queued() || s.ClaimOf(id(3)).State != Forged {
		t.Fatalf("a forged restore should wait")
	}
	s.Execute(t0+3*Day+decisions.Delay, d.ID, r.env())
	if s.ClaimOf(id(3)).State != Open || s.Owed != 1_000*gnot || s.Lost != 4_500*gnot {
		t.Fatalf("after the restore: owed %d lost %d", s.Owed, s.Lost)
	}
	s.Claim(t0+4*Day+decisions.Delay, 20, 0, 0, r.env())
	if r.paid[string(dave)] != 995*gnot || s.ClaimOf(id(3)).State != Done {
		t.Fatalf("dave paid %d", r.paid[string(dave)])
	}
	identity(t, s, supply-1_000*gnot)
	// a claim paid in full can still be voided as forged: its paid part becomes lost
	s.Decide(t0+5*Day+decisions.Delay, t0+5*Day+decisions.Delay, s.DraftVoid(id(0), true, "", "", 0, r.seatOf), r.env())
	if s.ClaimOf(id(0)).State != Done {
		t.Fatalf("an unflagged void of a done claim applied at once")
	}
	r.flagged[id(0)] = true
	s.Decide(t0+5*Day+decisions.Delay, t0+5*Day+decisions.Delay, s.DraftVoid(id(0), true, "", "", 0, r.seatOf), r.env())
	if s.ClaimOf(id(0)).State != Forged || s.Lost != 5_500*gnot {
		t.Fatalf("void of a done claim: %s lost %d", s.ClaimOf(id(0)).State, s.Lost)
	}
}

// TestVoidByRange: a void may name a range of credits instead of a list — every claim credited at heights
// from-to, or the verifier's credits since a height — and a guardian's flag may too; the at-once rule applies to
// the claims the flag's range covers, the rest of the void's range waits 7 days, closed at the height the void
// executed; a claim the void cannot take as it stands is skipped.
func TestVoidByRange(t *testing.T) {
	if r, ok := ParseRange("heights:5-9"); !ok || r.From != 5 || r.To != 9 || r.Since || r.String() != "claims credited at heights 5-9" {
		t.Fatalf("heights: %+v %v", r, ok)
	}
	if r, ok := ParseRange(" since:12 "); !ok || r.From != 12 || !r.Since || r.To != 0 || r.String() != "the verifier's credits since height 12" {
		t.Fatalf("since: %+v %v", r, ok)
	}
	if _, ok := ParseRange(id(1) + "," + id(2)); ok {
		t.Fatalf("a list read as a range")
	}
	r5 := Range{From: 5, To: 9}
	if !r5.Covers(5, false) || !r5.Covers(9, true) || r5.Covers(10, true) || r5.Covers(4, true) {
		t.Fatalf("heights covers")
	}
	s12 := Range{From: 12, Since: true}
	if !s12.Covers(12, true) || !s12.Covers(1_000_000, true) || s12.Covers(12, false) || s12.Covers(11, true) || !s12.closed(20).Covers(20, true) || s12.closed(20).Covers(21, true) {
		t.Fatalf("since covers")
	}
	s := newState()
	r := newRecorder()
	lock(s, 100_000*gnot, 0)
	// six claims at heights 100 to 105, the verifier's but the one at 103, which GovDAO credited by hand
	for i, to := range []address{alice, bob, carol, dave, erin, alice} {
		if i == 3 {
			r.height = 103
			d := s.Decide(t0, t0, s.DraftCredit(id(i), to, 100*gnot, false), r.env())
			s.Execute(t0+decisions.Delay, d.ID, r.env())
			continue
		}
		s.Credit(t0, int64(100+i), 0, id(i), to, 100*gnot, BySigners, false)
	}
	if c := s.ClaimOf(id(3)); c.Height != 103 || c.By != ByVote || s.ClaimOf(id(5)).Height != 105 {
		t.Fatalf("heights %+v", c)
	}
	// a guardian's flag covers heights 101-103: a void of 100-105 as forged takes 101, 102 and 103 at once (the hand
	// credit too, by height) and queues 100, 104 and 105 in the same decision, the range closed at this height
	r.rng = &Range{From: 101, To: 103}
	r.height = 200
	d := s.Decide(t0, t0, s.DraftVoid("heights:100-105", true, "payouts", "", 200, r.seatOf), r.env())
	if !d.Queued() || d.Note != "3 voided at once (named by a guardian's payouts flag) of claims credited at heights 100-105, unpaused payouts, 3 queued 7 days, last voided at height 103" {
		t.Fatalf("range void %+v", d)
	}
	if !strings.Contains(d.Text, "void as forged: claims credited at heights 100-105, and unpause payouts") || len(r.cleared) != 1 {
		t.Fatalf("text %s cleared %v", d.Text, r.cleared)
	}
	for i, want := range []string{Open, Forged, Forged, Forged, Open, Open} {
		if got := s.ClaimOf(id(i)).State; got != want {
			t.Fatalf("claim %d is %s, want %s", i, got, want)
		}
	}
	if s.Owed != 300*gnot {
		t.Fatalf("owed %d", s.Owed)
	}
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	for i := 0; i < 6; i++ {
		if s.ClaimOf(id(i)).State != Forged {
			t.Fatalf("claim %d not voided after the delay", i)
		}
	}
	if s.Owed != 0 || s.Lost != 0 || d.Note != "applied after the delay; 3 voided, 3 skipped (not voidable as it stands), last voided at height 105" {
		t.Fatalf("owed %d lost %d note %q", s.Owed, s.Lost, d.Note)
	}
	identity(t, s, s.Lock)
	// a range void over claims it cannot take is refused when made; restored (a list), they can be
	all := id(0) + "," + id(1) + "," + id(2) + "," + id(3) + "," + id(4) + "," + id(5)
	d = s.Decide(t0+decisions.Delay, t0+decisions.Delay, s.DraftRestore(all), r.env())
	s.Execute(t0+2*decisions.Delay, d.ID, r.env())
	if s.nOpen != 6 || s.Owed != 600*gnot {
		t.Fatalf("restored: open %d owed %d", s.nOpen, s.Owed)
	}
	// "since:103" names the verifier's credits only — 104 and 105, not the hand credit at 103 — and the void closes
	// it at the height it executes: at height 104 the claim at 105 is beyond it; a since-flag from 103 covers 104
	r.rng = &Range{From: 103, Since: true}
	r.height = 104
	d = s.Decide(t0+2*decisions.Delay, t0+2*decisions.Delay, s.DraftVoid("since:103", false, "", "", 104, r.seatOf), r.env())
	if d.Queued() || d.Note != "at once: 1 voided at once (named by a guardian's payouts flag) of the verifier's credits since height 103 up to height 104, last voided at height 104" {
		t.Fatalf("since void %+v", d)
	}
	if s.ClaimOf(id(4)).State != Withheld || s.ClaimOf(id(3)).State != Open || s.ClaimOf(id(5)).State != Open {
		t.Fatalf("since void states %s %s %s", s.ClaimOf(id(4)).State, s.ClaimOf(id(3)).State, s.ClaimOf(id(5)).State)
	}
	// a withheld claim in a range voided as withheld again is skipped, not refused; without a flag the rest queue
	r.rng = nil
	r.height = 300
	d = s.Decide(t0+2*decisions.Delay, t0+2*decisions.Delay, s.DraftVoid("heights:104-105", false, "", "", 300, r.seatOf), r.env())
	if !d.Queued() || !strings.Contains(d.Note, "0 voided at once (named by a guardian's payouts flag) of claims credited at heights 104-105, 1 skipped (not voidable as it stands), 1 queued 7 days") {
		t.Fatalf("skip %+v", d)
	}
	s.Execute(t0+3*decisions.Delay, d.ID, r.env())
	if s.ClaimOf(id(5)).State != Withheld || s.ClaimOf(id(4)).State != Withheld || s.Voided != 200*gnot {
		t.Fatalf("after the delay: %s %s voided %d", s.ClaimOf(id(5)).State, s.ClaimOf(id(4)).State, s.Voided)
	}
	identity(t, s, s.Lock-s.Owed-s.Voided)
}

// TestCreditByVote: a hand credit marks the id done and queues a claim; one for a voided id replaces it.
func TestCreditByVote(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 100_000*gnot, 0)
	d := s.Decide(t0, t0, s.DraftCredit(id(0), alice, 500*gnot, false), r.env())
	if !d.Queued() || r.done[id(0)] {
		t.Fatalf("a credit waits: %+v", d)
	}
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	c := s.ClaimOf(id(0))
	if !r.done[id(0)] || c == nil || c.By != ByVote || c.Amount != 500*gnot || s.Owed != 500*gnot {
		t.Fatalf("credited: %+v owed %d", c, s.Owed)
	}
	// a diverted credit: voided as withheld, then credited again to the right recipient
	r.flagged[id(0)] = true
	s.Decide(t0+decisions.Delay, t0+decisions.Delay, s.DraftVoid(id(0), false, "", "", 0, r.seatOf), r.env())
	d = s.Decide(t0+decisions.Delay, t0+decisions.Delay, s.DraftCredit(id(0), bob, 500*gnot, true), r.env())
	s.Execute(t0+2*decisions.Delay, d.ID, r.env())
	c = s.ClaimOf(id(0))
	if c == nil || c.To != bob || c.State != Open || c.Paid != 0 || s.Owed != 500*gnot || s.Voided != 0 || s.nOpen != 1 {
		t.Fatalf("replaced: %+v owed %d voided %d", c, s.Owed, s.Voided)
	}
	s.Claim(t0+3*decisions.Delay, 20, 0, 0, r.env())
	if r.paid[string(bob)] != 495*gnot || r.paid[string(alice)] != 0 {
		t.Fatalf("paid bob %d alice %d", r.paid[string(bob)], r.paid[string(alice)])
	}
	identity(t, s, s.Lock-s.Owed)
}

// TestSettingsClocks: a lowering is at once, a raise waits 7 days; the router swap keeps the old router 7 days;
// cancel is at once.
func TestSettingsClocks(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 100_000*gnot, 0)
	// cap floor: raise waits, then applies; lowering at once, never below the launch floor (the draft refuses)
	d := s.Decide(t0, t0, s.DraftCapFloor(12_000*gnot), r.env())
	if !d.Queued() || s.CapFloor != 10_000*gnot {
		t.Fatalf("a raise applied at once")
	}
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if s.CapFloor != 12_000*gnot || s.Cap() != 12_000*gnot {
		t.Fatalf("cap floor %d", s.CapFloor)
	}
	d = s.Decide(t0, t0, s.DraftCapFloor(11_000*gnot), r.env())
	if d.Queued() || s.CapFloor != 11_000*gnot || !strings.Contains(d.Note, "a lowering") {
		t.Fatalf("a lowering waited: %+v", d)
	}
	// maximum
	d = s.Decide(t0, t0, s.DraftMaximum(20_000*gnot), r.env())
	if d.Queued() || s.Maximum != 20_000*gnot {
		t.Fatalf("maximum lowering")
	}
	d = s.Decide(t0, t0, s.DraftMaximum(30_000*gnot), r.env())
	if !d.Queued() || s.Maximum != 20_000*gnot {
		t.Fatalf("maximum raise")
	}
	// fee and minimum: both lower at once; either raise waits
	d = s.Decide(t0, t0, s.DraftFee(4*gnot, 8*gnot), r.env())
	if d.Queued() || s.Fee != 4*gnot || s.Minimum != 8*gnot {
		t.Fatalf("fee lowering")
	}
	d = s.Decide(t0, t0, s.DraftFee(3*gnot, 9*gnot), r.env())
	if !d.Queued() || s.Fee != 4*gnot {
		t.Fatalf("a raised minimum should wait")
	}
	// fee floor
	d = s.Decide(t0, t0, s.DraftFeeFloor(1*gnot), r.env())
	if d.Queued() || s.FeeFloor != 1*gnot {
		t.Fatalf("fee floor lowering")
	}
	d = s.Decide(t0, t0, s.DraftFeeFloor(3*gnot), r.env())
	if !d.Queued() || s.FeeFloor != 1*gnot {
		t.Fatalf("fee floor raise")
	}
	// router: a swap waits; the old router is accepted 7 more days after it applies
	nr := [32]byte{2}
	d = s.Decide(t0, t0, s.DraftRouter(nr, "router two"), r.env())
	if !d.Queued() || s.Router != ([32]byte{1}) {
		t.Fatalf("router swap")
	}
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if s.Router != nr || !s.RouterAccepts(t0+decisions.Delay, nr) || !s.RouterAccepts(t0+decisions.Delay+1, [32]byte{1}) {
		t.Fatalf("router after the swap")
	}
	if s.RouterAccepts(t0+2*decisions.Delay, [32]byte{1}) || s.RouterAccepts(t0, [32]byte{3}) {
		t.Fatalf("the old router outlived its 7 days")
	}
	// cancel: at once, the cancelled one never applies
	d = s.Decide(t0, t0, s.DraftMaximum(40_000*gnot), r.env())
	c := s.Decide(t0, t0, s.DraftCancel(d.ID), r.env())
	if c.Queued() || d.State() != "cancelled" || c.Kind != KindCancel {
		t.Fatalf("cancel: %s %+v", d.State(), c)
	}
	if !strings.Contains(s.Ledger.Page(), "### set maximum") || !strings.Contains(s.Figures(t0, 0, 0, 0, "sends"), "**paused**: sends") {
		t.Fatalf("pages")
	}
	if !strings.Contains(s.JSON(t0, 0, 0, 0), "\"fee\":4000000,\"feeFloor\":1000000,\"minimum\":8000000,\"maximum\":20000000") {
		t.Fatalf("json: %s", s.JSON(t0, 0, 0, 0))
	}
}

// TestMeterEpoch: the meters restart empty at the launch and at every payouts epoch, and not otherwise.
func TestMeterEpoch(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 100_000*gnot, 0)
	s.Credit(t0, 0, 0, id(0), alice, 50_000*gnot, BySigners, false)
	s.Claim(t0+2*Day, 20, 0, 0, r.env())
	if r.total() != 2_500*gnot {
		t.Fatalf("a quarter %d", r.total())
	}
	// an unpause that cleared a payouts flag at t0+2d: epoch 1; a claim an hour later draws an hour's refill only
	s.Claim(t0+2*Day+hour, 20, 1, t0+2*Day, r.env())
	if r.total() != 2_500*gnot+2_500*gnot/24 {
		t.Fatalf("after the epoch %d", r.total())
	}
	// ResetMeters (the launch): empty again
	s.ResetMeters(t0 + 3*Day)
	if b, q := s.MeterLevels(t0+3*Day, alice, 1, t0+2*Day); b != 0 || q != 0 {
		t.Fatalf("after the reset %d %d", b, q)
	}
	if b, q := s.MeterLevels(t0+4*Day, alice, 1, t0+2*Day); b != 10_000*gnot || q != 2_500*gnot {
		t.Fatalf("a day after the reset %d %d", b, q)
	}
	// a recipient whose queue is made ten days after the bump starts its meter at the current epoch, empty as of
	// its first credit: an hour later it holds an hour's quarter-refill, never the quarter the epoch's reset
	// would fill it with from ten days back
	s.Credit(t0+12*Day, 0, 1, id(1), bob, 50_000*gnot, BySigners, false)
	s.Claim(t0+12*Day+hour, 20, 1, t0+2*Day, r.env())
	if r.paid[string(bob)] != 2_500*gnot/24-5*gnot {
		t.Fatalf("a fresh recipient after the bump drew %d, want an hour's %d", r.paid[string(bob)]+5*gnot, 2_500*gnot/24)
	}
	// the same claim credited by vote through the decision path: the epoch comes from the realm (Env.Epoch)
	r.epoch = 1
	d := s.Decide(t0+12*Day, t0+12*Day, s.DraftCredit(id(2), carol, 50_000*gnot, false), r.env())
	s.Execute(t0+12*Day+decisions.Delay, d.ID, r.env())
	s.Claim(t0+12*Day+decisions.Delay+hour, 20, 1, t0+2*Day, r.env())
	if r.paid[string(carol)] != 2_500*gnot/24-5*gnot {
		t.Fatalf("a fresh recipient by vote drew %d", r.paid[string(carol)]+5*gnot)
	}
}

// TestPages: the page lists the first 100 waiting claims and the first 100 sweep entries and counts the rest;
// claims/<from> and sweeps/<from> page the whole lists by hundreds.
func TestPages(t *testing.T) {
	s := newState()
	lock(s, 1_000_000*gnot, 0)
	for i := 0; i < 105; i++ {
		s.Credit(t0, 0, 0, id(i), testutils.TestAddress("hlr-many-"+strconv.Itoa(i)), 100*gnot, BySigners, false)
	}
	for i := 0; i < 102; i++ {
		s.Credit(t0, 0, 0, id(120+i), alice, 1*gnot, BySigners, false) // below the fee: swept
	}
	p := s.Figures(t0, 0, 0, 0, "")
	if strings.Count(p, " #1: 100 GNOT of 100 GNOT open (signers)") != 100 || !strings.Contains(p, "and 5 more (105 waiting): the whole list, by hundreds, at claims/0, claims/100") {
		t.Fatalf("waiting claims on the page:\n%s", p)
	}
	if strings.Count(p, "- 1 GNOT `") != 100 || !strings.Contains(p, "and 2 more (102 entries): the whole queue, by hundreds, at sweeps/0, sweeps/100") {
		t.Fatalf("the sweep queue on the page:\n%s", p)
	}
	if n := strings.Count(s.ClaimsPage(0), "\"id\":"); n != 100 {
		t.Fatalf("claims/0 holds %d", n)
	}
	if n := strings.Count(s.ClaimsPage(100), "\"id\":"); n != 5 {
		t.Fatalf("claims/100 holds %d", n)
	}
	if s.ClaimsPage(105) != "[]" || s.ClaimsPage(-1) != "[]" || !strings.HasPrefix(s.ClaimsPage(0), "[{\"id\":\"") {
		t.Fatalf("claims pages: %s", s.ClaimsPage(105))
	}
	if n := strings.Count(s.SweepsPage(0), "\"swept\":1000000"); n != 100 {
		t.Fatalf("sweeps/0 holds %d", n)
	}
	if n := strings.Count(s.SweepsPage(100), "\"swept\":1000000"); n != 2 {
		t.Fatalf("sweeps/100 holds %d", n)
	}
	if s.SweepsPage(102) != "[]" || s.SweepsPage(-1) != "[]" {
		t.Fatalf("sweeps pages: %s", s.SweepsPage(102))
	}
	// a short list: no count of the rest
	s2 := newState()
	lock(s2, 1_000*gnot, 0)
	s2.Credit(t0, 0, 0, id(0), bob, 100*gnot, BySigners, false)
	if p := s2.Figures(t0, 0, 0, 0, ""); strings.Contains(p, "more (") || !strings.Contains(p, string(bob)+" #1: 100 GNOT") {
		t.Fatalf("a short list:\n%s", p)
	}
}

// TestSendsRefundMigrate: send records; a live route never refunds its own send (AQ2: a refunded send stays
// deliverable on Solana, so a stranded one waits for a kind 12); the migration's export and import; the
// successor's refund of a send it imported, by id, after 7 days, once — the mark at send/<id>, the send in
// refunds/<from>, the count and the sum in the json — and a refunded send left behind by the next migration.
func TestSendsRefundMigrate(t *testing.T) {
	s := newState()
	r := newRecorder()
	s.CheckSend(10 * gnot)
	s.CheckSend(25_000 * gnot)
	s.Send(t0, id(0), alice, 1_000*gnot)
	s.Send(t0, id(1), bob, 2_000*gnot)
	if s.Lock != 3_000*gnot || s.SendOf(id(0)).From != alice || s.Sends() != 2 || s.SendOf(id(1)).Imported {
		t.Fatalf("sends")
	}
	mustPanic(t, id(1)+" is this route's own send: a stranded send waits for a kind 12 on Solana, never a refund", func() { s.DraftRefund(id(1), t0, r.env()) })
	mustPanic(t, id(9)+" is not a send to refund", func() { s.DraftRefund(id(9), t0, r.env()) })
	if s.SendJSON(id(1)) != "{\"id\":\""+id(1)+"\",\"from\":\""+string(bob)+"\",\"amount\":2000000000,\"at\":"+itoa(t0)+",\"imported\":false,\"refunded\":0,\"writtenOff\":0}" || s.SendJSON(id(9)) != "null" || s.SendJSON("zz") != "null" || s.RefundsPage(0) != "[]" {
		t.Fatalf("send json: %s", s.SendJSON(id(1)))
	}
	identity(t, s, 3_000*gnot)
	// claims and a sweep, then migrate — to a successor that announced itself to the mailbox (R25): a path that did
	// not is refused when the proposal is made and, asked again, when it would apply
	s.Credit(t0, 0, 0, id(2), carol, 300*gnot, BySigners, false)
	s.Credit(t0, 0, 0, id(3), dave, 2*gnot, BySigners, false)
	mustPanic(t, "the successor gno.land/r/x/hlgnot2 has not announced itself to the mailbox", func() {
		s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env())
	})
	// a successor announces which route it succeeds (AQ5): the other token's is refused, when the proposal is made
	// and, asked again, when it would apply
	r.announced["gno.land/r/x/hlbubble2"] = "BUBBLE"
	mustPanic(t, "the successor gno.land/r/x/hlbubble2 announced itself as the BUBBLE route's successor, not this GNOT route's", func() {
		s.DraftMigrate("gno.land/r/x/hlbubble2", "gno.land/r/x/hlgnot", r.env())
	})
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	d := s.Decide(t0, t0, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	if !d.Queued() || s.Migrated() || d.Text != "migrate to gno.land/r/x/hlgnot2 (announced as the GNOT route's successor)" {
		t.Fatalf("migrate waits: %+v", d)
	}
	r.announced["gno.land/r/x/hlgnot2"] = ""
	mustPanic(t, "has not announced itself to the mailbox", func() { s.Execute(t0+decisions.Delay, d.ID, r.env()) })
	r.announced["gno.land/r/x/hlgnot2"] = "BUBBLE"
	mustPanic(t, "announced itself as the BUBBLE route's successor, not this GNOT route's", func() { s.Execute(t0+decisions.Delay, d.ID, r.env()) })
	if s.Migrated() || !d.Pending() {
		t.Fatalf("migrated to an unannounced path")
	}
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if s.MigratedTo != "gno.land/r/x/hlgnot2" || len(r.routes) != 1 || r.routes[0] != "gno.land/r/x/hlgnot2" {
		t.Fatalf("migrated %s routes %v", s.MigratedTo, r.routes)
	}
	snap := s.Export(0, PullPage)
	if snap.Lock != 3_000*gnot || snap.Owed != 302*gnot || snap.Sweep != 2*gnot || len(snap.Claims) != 1 || len(snap.Sweeps) != 1 || len(snap.Sends) != 2 || snap.More {
		t.Fatalf("snapshot %+v", snap)
	}
	if s.Lock != 0 || s.Owed != 0 || s.Sends() != 0 || s.ClaimOf(id(2)) != nil {
		t.Fatalf("the predecessor kept something")
	}
	// the successor: the imported sends are marked so, the only ones a refund may name
	s2 := newState()
	s2.Import(t0+decisions.Delay, 0, 0, snap)
	if s2.Lock != 3_000*gnot || s2.Owed != 302*gnot || s2.ClaimOf(id(2)).To != carol || s2.SweptOf(id(3)) != 2*gnot || s2.SendOf(id(0)).From != alice || !s2.SendOf(id(0)).Imported || !s2.SendOf(id(1)).Imported || s2.nOpen != 1 {
		t.Fatalf("imported: lock %d owed %d", s2.Lock, s2.Owed)
	}
	identity(t, s2, 3_000*gnot-302*gnot)
	r2 := newRecorder()
	s2.Claim(t0+2*decisions.Delay, 20, 0, 0, r2.env())
	if r2.paid[string(carol)] != 295*gnot || r2.fees != 5*gnot || s2.Lock != 2_700*gnot {
		t.Fatalf("the successor pays: carol %d fees %d lock %d", r2.paid[string(carol)], r2.fees, s2.Lock)
	}
	// the successor refunds bob's imported send by id, after 7 days, once: the amount back to bob out of the lock,
	// sentTotal and the lock falling together; its own send (erin's, below) it never refunds
	d = s2.Decide(t0+2*decisions.Delay, t0+2*decisions.Delay, s2.DraftRefund(id(1), t0+2*decisions.Delay, r2.env()), r2.env())
	if !d.Queued() || d.Text != "refund 2000 GNOT of sends imported from the predecessor (14 days after kind 6's dispatch enrolled this realm) to their senders: "+id(1) {
		t.Fatalf("a refund waits: %+v", d)
	}
	if !strings.Contains(s2.JSON(t0, 0, 0, 0), "\"sends\":2,\"refunded\":0,\"refundedTotal\":0,") {
		t.Fatalf("json before: %s", s2.JSON(t0, 0, 0, 0))
	}
	ap := applyRefund(s2, t0+3*decisions.Delay, d.ID, r2.env())
	if r2.paid[string(bob)] != 2_000*gnot || s2.Lock != 700*gnot || s2.SentTotal != 1_000*gnot || s2.SendOf(id(1)).Refunded != t0+3*decisions.Delay || d.Note != "applied by GovDAO's apply refund decision "+itoa(ap.ID)+"; 1 refunded" {
		t.Fatalf("refund: bob %d lock %d sent %d note %q", r2.paid[string(bob)], s2.Lock, s2.SentTotal, d.Note)
	}
	if !strings.Contains(s2.JSON(t0, 0, 0, 0), "\"sends\":2,\"refunded\":1,\"refundedTotal\":2000000000,") || !strings.HasSuffix(s2.SendJSON(id(1)), "\"imported\":true,\"refunded\":"+itoa(t0+3*decisions.Delay)+",\"writtenOff\":0}") || s2.RefundsPage(0) != "["+s2.SendJSON(id(1))+"]" || s2.RefundsPage(1) != "[]" || s2.RefundsPage(-1) != "[]" {
		t.Fatalf("the mark: %s\n%s", s2.SendJSON(id(1)), s2.RefundsPage(0))
	}
	if !strings.Contains(s2.Figures(t0, 0, 0, 0, ""), "sends recorded: 2, refunded 1 (2000 GNOT)") {
		t.Fatalf("page:\n%s", s2.Figures(t0, 0, 0, 0, ""))
	}
	mustPanic(t, id(1)+" is not a send to refund", func() { s2.DraftRefund(id(1), t0+3*decisions.Delay, r2.env()) })
	s2.Send(t0+3*decisions.Delay, id(4), erin, 500*gnot)
	mustPanic(t, id(4)+" is this route's own send", func() { s2.DraftRefund(id(0)+","+id(4), t0+3*decisions.Delay, r2.env()) })
	identity(t, s2, s2.Lock-s2.Owed)
	// migrated on, every send goes with its page — the successor's own among them, imported there and so refundable
	// by the next route — a refunded one with its mark (R33): the next route answers send/<id> with the refund's
	// time, lists it at refunds/0, counts it in its json and refuses to refund it again, and the predecessor, which
	// holds no record now, counts it no more — so a monitor always finds one realm that calls the send refunded
	r2.announced["gno.land/r/x/hlgnot3"] = "GNOT"
	d = s2.Decide(t0+3*decisions.Delay, t0+3*decisions.Delay, s2.DraftMigrate("gno.land/r/x/hlgnot3", "gno.land/r/x/hlgnot2", r2.env()), r2.env())
	s2.Execute(t0+4*decisions.Delay, d.ID, r2.env())
	p := s2.Export(0, PullPage)
	if len(p.Sends) != 3 || p.Sends[0].ID != id(0) || p.Sends[0].Refunded != 0 || p.Sends[1].ID != id(1) || p.Sends[1].Refunded != t0+3*decisions.Delay || p.Sends[2].ID != id(4) || p.Lock != 1_200*gnot || p.SentTotal != 1_500*gnot {
		t.Fatalf("the next page: %+v", p)
	}
	if s2.RefundsPage(0) != "[]" || s2.SendJSON(id(1)) != "null" || !strings.Contains(s2.JSON(t0, 0, 0, 0), "\"sends\":0,\"refunded\":0,\"refundedTotal\":0,") {
		t.Fatalf("the predecessor after the export: %s %s", s2.RefundsPage(0), s2.JSON(t0, 0, 0, 0))
	}
	s3 := newState()
	r3 := newRecorder()
	s3.Import(t0+4*decisions.Delay, 0, 0, p)
	if sd := s3.SendOf(id(1)); sd == nil || sd.Refunded != t0+3*decisions.Delay || !sd.Imported || !strings.HasSuffix(s3.SendJSON(id(1)), "\"imported\":true,\"refunded\":"+itoa(t0+3*decisions.Delay)+",\"writtenOff\":0}") || s3.RefundsPage(0) != "["+s3.SendJSON(id(1))+"]" || !strings.Contains(s3.JSON(t0, 0, 0, 0), "\"sends\":3,\"refunded\":1,\"refundedTotal\":2000000000,") {
		t.Fatalf("the mark after the second migration: %s\n%s", s3.SendJSON(id(1)), s3.JSON(t0, 0, 0, 0))
	}
	mustPanic(t, id(1)+" is not a send to refund", func() { s3.DraftRefund(id(1), t0+4*decisions.Delay, r3.env()) })
	if s3.Lock != 1_200*gnot || s3.SentTotal != 1_500*gnot || s3.SendOf(id(0)).Refunded != 0 || !s3.SendOf(id(4)).Imported {
		t.Fatalf("the next route: lock %d sent %d", s3.Lock, s3.SentTotal)
	}
	identity(t, s3, s3.Lock-s3.Owed)
}

// mustPanic runs fn and wants a panic whose message contains want.
func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected a panic containing %q, got none", want)
		}
		msg := ""
		switch e := r.(type) {
		case error:
			msg = e.Error()
		case string:
			msg = e
		}
		if !strings.Contains(msg, want) {
			t.Fatalf("panic %q does not contain %q", msg, want)
		}
	}()
	fn()
}

// idN is a message id for a batch bigger than id's 256.
func idN(n int) string {
	h := strconv.FormatInt(int64(n), 16)
	return strings.Repeat("0", 64-len(h)) + h
}

// TestSweepVoid (R11): a sweep entry is a record a void takes as forged — by id or by range, at once under a
// guardian's flag, after 7 days otherwise, never as withheld — and a restore gives back after 7 days; a credit by
// id replaces an entry with the claim GovDAO names; the identity holds at every step.
func TestSweepVoid(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 1_000*gnot, 0)
	supply := int64(1_000 * gnot)
	// alice burned 2 (below the fee: swept, an honest burn) at height 10; a forger delivered 5,000 — above the
	// lock — at height 11
	if !s.Credit(t0, 10, 0, id(0), alice, 2*gnot, BySigners, false) || !s.Credit(t0, 11, 0, id(1), bob, 5_000*gnot, BySigners, false) {
		t.Fatalf("not swept")
	}
	e := s.SweepOf(id(1))
	if e == nil || e.Amount != 5_000*gnot || e.Height != 11 || e.By != BySigners || e.Voided || s.Sweep != 5_002*gnot || s.Owed != 5_002*gnot {
		t.Fatalf("entry %+v sweep %d owed %d", e, s.Sweep, s.Owed)
	}
	identity(t, s, supply-5_002*gnot) // the real supply is 998: the forged 5,000 holds the identity open
	// a void as withheld never takes an entry; one as forged, by id, at once under a guardian's flag naming it:
	// the amount leaves the sweep queue and what is owed
	mustPanic(t, id(1)+" is a sweep entry: voided as forged only", func() { s.DraftVoid(id(1), false, "", "", 0, r.seatOf) })
	r.flagged[id(1)] = true
	d := s.Decide(t0, t0, s.DraftVoid(id(1), true, "", "", 0, r.seatOf), r.env())
	if d.Queued() || !strings.Contains(d.Note, "1 voided at once") || !e.Voided || s.Sweep != 2*gnot || s.Owed != 2*gnot || s.SweptOf(id(1)) != 5_000*gnot {
		t.Fatalf("void of a sweep entry: %+v sweep %d owed %d", d, s.Sweep, s.Owed)
	}
	identity(t, s, supply-2*gnot) // closed
	mustPanic(t, id(1)+" is voided already", func() { s.DraftVoid(id(1), true, "", "", 0, r.seatOf) })
	if j := s.ClaimJSON(id(1)); !strings.Contains(j, "\"swept\":5000000000,\"height\":11,\"by\":\"signers\",\"voided\":true") || !strings.Contains(s.Figures(t0, 0, 0, 0, ""), "- 5000 GNOT `"+id(1)+"` (signers) at height 11, voided as forged") {
		t.Fatalf("pages: %s\n%s", j, s.Figures(t0, 0, 0, 0, ""))
	}
	// a voided entry is not paid: restored first
	mustPanic(t, id(1)+" is voided; restore it first", func() { s.DraftSweep(id(1), carol) })
	// restored after 7 days, in full
	d = s.Decide(t0, t0, s.DraftRestore(id(1)), r.env())
	if !d.Queued() || !e.Voided {
		t.Fatalf("a sweep restore should wait: %+v", d)
	}
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if e.Voided || s.Sweep != 5_002*gnot || s.Owed != 5_002*gnot {
		t.Fatalf("restored: sweep %d owed %d", s.Sweep, s.Owed)
	}
	// the sweep payment is bounded by the lock: the restored 5,000 is above the lock's 1,000
	mustPanic(t, "the entries sum to 5000 GNOT, above the lock's 1000 GNOT", func() { s.DraftSweep(id(1), carol) })
	identity(t, s, supply-5_002*gnot)
	// by range: a void as withheld skips the entries (a claim at height 12 in the range queues); as forged,
	// unflagged, the entries queue with it and are voided after 7 days
	s.Credit(t0, 12, 0, id(2), carol, 100*gnot, BySigners, false)
	r.flagged = map[string]bool{}
	d = s.Decide(t0, t0, s.DraftVoid("heights:10-12", false, "", "", 50, r.seatOf), r.env())
	if !d.Queued() || !strings.Contains(d.Note, "0 voided at once (named by a guardian's payouts flag) of claims credited at heights 10-12, 2 skipped (not voidable as it stands), 1 queued 7 days") {
		t.Fatalf("a withheld range skips the entries: %+v", d)
	}
	s.Decide(t0, t0, s.DraftCancel(d.ID), r.env())
	d = s.Decide(t0, t0, s.DraftVoid("heights:10-12", true, "", "", 50, r.seatOf), r.env())
	if !d.Queued() || !strings.Contains(d.Note, "0 voided at once (named by a guardian's payouts flag) of claims credited at heights 10-12, 3 queued 7 days") {
		t.Fatalf("a forged range covers the entries: %+v", d)
	}
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if !s.SweepOf(id(0)).Voided || !e.Voided || s.ClaimOf(id(2)).State != Forged || s.Sweep != 0 || s.Owed != 0 || s.nOpen != 0 {
		t.Fatalf("after the range void: sweep %d owed %d", s.Sweep, s.Owed)
	}
	identity(t, s, supply)
	// a credit by id replaces an entry with the claim GovDAO names: alice's 2, voided by mistake, back as a claim
	// of 2 to her (the entry gone); bob's 5,000, restored, then credited to carol for 10 — the entry gone, the
	// 10 owed in its place, and no mark in the mailbox (the message was delivered)
	d = s.Decide(t0, t0, s.DraftCredit(id(0), alice, 2*gnot, true), r.env())
	if !strings.Contains(d.Text, "credit "+id(0)+" to "+string(alice)+" for 2 GNOT (replacing the sweep entry of 2 GNOT)") {
		t.Fatalf("text %s", d.Text)
	}
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if c := s.ClaimOf(id(0)); s.SweepOf(id(0)) != nil || c == nil || c.To != alice || c.By != ByVote || c.State != Open || s.Owed != 2*gnot || s.Sweep != 0 || s.nOpen != 1 {
		t.Fatalf("a credit replacing a voided entry: owed %d sweep %d", s.Owed, s.Sweep)
	}
	identity(t, s, supply-2*gnot)
	d = s.Decide(t0, t0, s.DraftRestore(id(1)), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if s.Sweep != 5_000*gnot || s.Owed != 5_002*gnot {
		t.Fatalf("restored again: sweep %d owed %d", s.Sweep, s.Owed)
	}
	d = s.Decide(t0, t0, s.DraftCredit(id(1), carol, 10*gnot, true), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if c := s.ClaimOf(id(1)); s.SweepOf(id(1)) != nil || c.Amount != 10*gnot || c.To != carol || s.Sweep != 0 || s.Owed != 12*gnot || s.nOpen != 2 || r.done[id(1)] {
		t.Fatalf("a credit replacing a live entry: owed %d sweep %d open %d", s.Owed, s.Sweep, s.nOpen)
	}
	identity(t, s, supply-12*gnot)
	// the claims pay like any: alice's 2 as a fee-only claim, carol's 10 with the fee first
	s.Claim(t0+Day+decisions.Delay, 20, 0, 0, r.env())
	if r.fees != 7*gnot || r.paid[string(carol)] != 5*gnot || r.paid[string(alice)] != 0 || s.nOpen != 0 {
		t.Fatalf("paid: fees %d carol %d alice %d", r.fees, r.paid[string(carol)], r.paid[string(alice)])
	}
	identity(t, s, supply-12*gnot)
}

// TestVoidedClaimsLeaveTheQueue (R12): voided claims leave their recipient's queue, so a flood of them, once
// voided, stands ahead of nothing — 300 voided claims and one open at bob, and Claim pays the open one in one
// call; a restore puts a claim back at its place; the page walks the open ones only.
func TestVoidedClaimsLeaveTheQueue(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 100_000*gnot, 0)
	for i := 0; i < 300; i++ {
		s.Credit(t0, 5, 0, idN(1000+i), bob, 10*gnot, BySigners, false)
	}
	s.Credit(t0, 6, 0, idN(2000), bob, 100*gnot, BySigners, false)
	r.rng = &Range{From: 5, To: 5}
	d := s.Decide(t0, t0, s.DraftVoid("heights:5-5", true, "", "", 7, r.seatOf), r.env())
	if d.Queued() || !strings.Contains(d.Note, "300 voided at once") || s.nOpen != 1 || s.Owed != 100*gnot {
		t.Fatalf("flood void: %+v open %d owed %d", d, s.nOpen, s.Owed)
	}
	if q := s.queues.Get(string(bob)).(*Queue); q.items.Size() != 1 || s.waitingCount() != 1 {
		t.Fatalf("the queue keeps tombstones: %d items, %d waiting", q.items.Size(), s.waitingCount())
	}
	if n := s.Claim(t0+Day, 1, 0, 0, r.env()); n != 1 || r.paid[string(bob)] != 95*gnot || s.waiting.Size() != 0 {
		t.Fatalf("one call paid %d claims, bob %d", n, r.paid[string(bob)])
	}
	identity(t, s, s.Lock-s.Owed)
	// a restore re-inserts a claim at its place: the second voided claim restored sits ahead of a newer credit
	d = s.Decide(t0+Day, t0+Day, s.DraftRestore(idN(1001)), r.env())
	s.Execute(t0+Day+decisions.Delay, d.ID, r.env())
	s.Credit(t0+Day, 8, 0, idN(2001), bob, 50*gnot, BySigners, false)
	var order []string
	s.eachWaiting(func(_ string, _ int, c *Claim) bool { order = append(order, c.ID); return false })
	if len(order) != 2 || order[0] != idN(1001) || order[1] != idN(2001) || s.ClaimOf(idN(1001)).Seq != 2 || s.waitingCount() != 2 {
		t.Fatalf("order %v seq %d", order, s.ClaimOf(idN(1001)).Seq)
	}
	if p := s.Figures(t0+Day, 0, 0, 0, ""); !strings.Contains(p, string(bob)+" #1: 10 GNOT of 10 GNOT open (signers) at height 5 `"+idN(1001)+"`") || !strings.Contains(p, string(bob)+" #2: 50 GNOT") {
		t.Fatalf("page:\n%s", p)
	}
}

// TestSeatRemovalGuard (R17): a void's seat removal records each seat's holder when the proposal is made; a seat
// whose occupant changed by execution is skipped, the note saying so; an empty seat is refused.
func TestSeatRemovalGuard(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 1_000*gnot, 0)
	s.Credit(t0, 0, 0, id(0), alice, 100*gnot, BySigners, false)
	mustPanic(t, "seat 3 is empty", func() {
		s.DraftVoid(id(0), false, "", "3", 0, func(seat int) address { return "" })
	})
	r.flagged[id(0)] = true
	r.changed[2] = true
	d := s.Decide(t0, t0, s.DraftVoid(id(0), false, "", "5,2", 0, r.seatOf), r.env())
	if p := d.Params.(VoidParams); len(p.RemoveAddrs) != 2 || p.RemoveAddrs[0] != seatAddr(5) || p.RemoveAddrs[1] != seatAddr(2) {
		t.Fatalf("params %+v", d.Params)
	}
	if !strings.Contains(d.Text, "removing seats 5:"+string(seatAddr(5))+",2:"+string(seatAddr(2))) || !strings.Contains(d.Note, "1 voided at once (named by a guardian's payouts flag), seats 5 vacated, seats 2 skipped (the occupant changed since the proposal was made)") || len(r.removed) != 2 {
		t.Fatalf("%s\n%s %v", d.Text, d.Note, r.removed)
	}
}

// TestPagedExport (R13): the migration hands over the counters with page 0 and the records by pages of a given
// size — every claim ever (open, voided and paid), the sweep entries, the sends (a refunded one with its mark,
// R33: TestSendsRefundMigrate); a page pulled again exports nothing; the successor's queues keep the predecessor's
// order whatever the pages' order, behind the claims it credited itself before the import.
func TestPagedExport(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 10_000*gnot, 0)
	for i := 0; i < 3; i++ {
		s.Credit(t0, int64(100+i), 0, id(i), alice, 100*gnot, BySigners, false)
	}
	s.Credit(t0, 103, 0, id(3), carol, 300*gnot, BySigners, false)
	s.Credit(t0, 104, 0, id(4), dave, 1*gnot, BySigners, false) // swept
	s.Credit(t0, 105, 0, id(5), erin, 3*gnot, BySigners, false) // swept, voided below
	s.Claim(t0+Day, 2, 0, 0, r.env())                         // alice's first and carol's, paid in full
	r.flagged[id(2)] = true
	s.Decide(t0+Day, t0+Day, s.DraftVoid(id(2), false, "", "", 0, r.seatOf), r.env())
	r.flagged[id(5)] = true
	s.Decide(t0+Day, t0+Day, s.DraftVoid(id(5), true, "", "", 0, r.seatOf), r.env())
	if s.ClaimOf(id(0)).State != Done || s.ClaimOf(id(3)).State != Done || s.ClaimOf(id(1)).State != Open || s.ClaimOf(id(2)).State != Withheld || !s.SweepOf(id(5)).Voided {
		t.Fatalf("setup: %s %s %s %s", s.ClaimOf(id(0)).State, s.ClaimOf(id(3)).State, s.ClaimOf(id(1)).State, s.ClaimOf(id(2)).State)
	}
	owed, voided := s.Owed, s.Voided
	if owed != 101*gnot || voided != 100*gnot || s.Lock != 9_600*gnot || s.Sweep != 1*gnot {
		t.Fatalf("owed %d voided %d lock %d sweep %d", owed, voided, s.Lock, s.Sweep)
	}
	identity(t, s, s.Lock-owed-voided)
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	d := s.Decide(t0+Day, t0+Day, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+Day+decisions.Delay, d.ID, r.env())
	// the successor credited a claim of its own to alice before the import
	s2 := newState()
	r2 := newRecorder()
	lock(s2, 1_000*gnot, 3)
	s2.Credit(t0, 200, 0, id(9), alice, 50*gnot, BySigners, false)
	// pages of three: page 0 the counters and three claims; page 1 the fourth claim and the two sweep entries;
	// page 2 the one send not refunded; a page pulled again hands over nothing; the pages go in order
	p0 := s.Export(0, 3)
	if p0.Page != 0 || !p0.More || p0.Lock != 9_600*gnot || p0.Owed != owed || p0.Voided != voided || p0.Sweep != 1*gnot || len(p0.Claims) != 3 || len(p0.Sweeps) != 0 || len(p0.Sends) != 0 || s.Lock != 0 || s.Owed != 0 || s.Exported() != 1 {
		t.Fatalf("page 0: %+v", p0)
	}
	if again := s.Export(0, 3); again.Lock != 0 || len(again.Claims) != 0 || again.More || s.Exported() != 1 {
		t.Fatalf("page 0 again: %+v", again)
	}
	mustPanic(t, "page 1 is the next to pull, not 2", func() { s.Export(2, 3) })
	s2.Import(t0+2*Day, 0, 0, p0)
	p1 := s.Export(1, 3)
	if p1.Page != 1 || !p1.More || p1.Lock != 0 || p1.Owed != 0 || len(p1.Claims) != 1 || len(p1.Sweeps) != 2 || len(p1.Sends) != 0 {
		t.Fatalf("page 1: %+v", p1)
	}
	s2.Import(t0+2*Day, 0, 0, p1)
	p2 := s.Export(2, 3)
	if p2.More || len(p2.Claims) != 0 || len(p2.Sweeps) != 0 || len(p2.Sends) != 1 || p2.Sends[0].ID != id(20) || s.Exported() != 3 {
		t.Fatalf("page 2: %+v", p2)
	}
	s2.Import(t0+2*Day, 0, 0, p2)
	if p3 := s.Export(3, 3); p3.More || len(p3.Claims) != 0 || len(p3.Sends) != 0 || s.Exported() != 3 {
		t.Fatalf("an empty page: %+v", p3)
	}
	if s.ClaimOf(id(1)) != nil || s.SweepOf(id(4)) != nil || s.Sends() != 0 || s.nOpen != 0 || s.nClaims != 0 || s.waitingCount() != 0 {
		t.Fatalf("the predecessor kept something")
	}
	// the successor: the counters whole, every record with its state, the voided entry voided, alice's queue in
	// order — its own claim first, then the predecessor's open one — and a withheld claim restored there takes its
	// place after them
	if s2.Lock != 10_600*gnot || s2.Owed != owed+50*gnot || s2.Voided != voided || s2.Sweep != 1*gnot || s2.nClaims != 5 || s2.nOpen != 2 || s2.ClaimOf(id(0)).State != Done || s2.ClaimOf(id(3)).State != Done || s2.ClaimOf(id(2)).State != Withheld || !s2.SweepOf(id(5)).Voided || s2.SweptOf(id(4)) != 1*gnot || s2.SendOf(id(20)) == nil || s2.SendOf(id(20)).From != erin {
		t.Fatalf("imported: lock %d owed %d claims %d open %d", s2.Lock, s2.Owed, s2.nClaims, s2.nOpen)
	}
	identity(t, s2, s2.Lock-s2.Owed-s2.Voided)
	var order []string
	s2.eachWaiting(func(_ string, _ int, c *Claim) bool { order = append(order, c.ID); return false })
	if len(order) != 2 || order[0] != id(9) || order[1] != id(1) || s2.ClaimOf(id(1)).Seq != 3 {
		t.Fatalf("order %v", order)
	}
	s2.Decide(t0+2*Day, t0+2*Day, s2.DraftRestore(id(2)), r2.env())
	order = nil
	s2.eachWaiting(func(_ string, _ int, c *Claim) bool { order = append(order, c.ID); return false })
	if len(order) != 3 || order[2] != id(2) || s2.ClaimOf(id(2)).Seq != 4 || s2.Voided != 0 {
		t.Fatalf("order after the restore %v seq %d", order, s2.ClaimOf(id(2)).Seq)
	}
	s2.Claim(t0+3*Day, 20, 0, 0, r2.env())
	if r2.paid[string(alice)] != 235*gnot || s2.nOpen != 0 {
		t.Fatalf("the successor pays: alice %d open %d", r2.paid[string(alice)], s2.nOpen)
	}
	identity(t, s2, s2.Lock-s2.Owed-s2.Voided)
}

// TestRangeClosedAtCreation (R14): a range is closed at the height the proposal is made — a since range, and a
// heights range reaching past it — so what it covers never grows before it executes.
func TestRangeClosedAtCreation(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 10_000*gnot, 0)
	s.Credit(t0, 100, 0, id(0), bob, 100*gnot, BySigners, false)
	d := s.DraftVoid("since:100", true, "", "", 150, r.seatOf)
	if p := d.Params.(VoidParams); !p.Ranged || !p.Range.Since || p.Range.From != 100 || p.Range.To != 150 || d.Text != "void as forged: the verifier's credits since height 100 up to height 150" {
		t.Fatalf("draft %+v %s", d.Params, d.Text)
	}
	if p := s.DraftVoid("heights:100-999", true, "", "", 150, r.seatOf).Params.(VoidParams); p.Range.To != 150 {
		t.Fatalf("a heights range past the creation height: %+v", p.Range)
	}
	// a credit after the proposal was made is outside it, whatever the height it executes at
	s.Credit(t0, 160, 0, id(1), bob, 100*gnot, BySigners, false)
	r.height = 170
	dec := s.Decide(t0, t0, d, r.env())
	if !dec.Queued() || !strings.Contains(dec.Note, "of the verifier's credits since height 100 up to height 150, 1 queued 7 days") {
		t.Fatalf("decide %+v", dec)
	}
	s.Execute(t0+decisions.Delay, dec.ID, r.env())
	if s.ClaimOf(id(0)).State != Forged || s.ClaimOf(id(1)).State != Open {
		t.Fatalf("the range grew: %s %s", s.ClaimOf(id(0)).State, s.ClaimOf(id(1)).State)
	}
	identity(t, s, s.Lock-s.Owed)
}

// TestRangeVoidThousand (R14, R23): a range covering more than MaxRangeClaims credits — every credit the walk
// visits, voidable or not, since the walk at execution covers them all (R23) — is refused when the proposal is made
// (GovDAO splits it by heights); one covering exactly that many is taken — here at once under a flag covering them
// all — and its note names the last height voided, where the next range starts. The GAS line the harness prints for
// this test, against TestRangeCapBaseline's (the same first credits and nothing else), is the cost of the scans,
// the thousand voids and the later credits.
func TestRangeVoidThousand(t *testing.T) {
	big := newState()
	r := newRecorder()
	lock(big, 1_000_000*gnot, 0)
	for i := 0; i < MaxRangeClaims; i++ {
		big.Credit(t0, 1_000, 0, idN(5000+i), alice, 10*gnot, BySigners, false)
	}
	big.Credit(t0, 1_001, 0, idN(9000), carol, 10*gnot, BySigners, false)
	big.Credit(t0, 1_002, 0, idN(9001), dave, 10*gnot, BySigners, false)
	mustPanic(t, "more than 1000 credits in claims credited at heights 1000-1001, voidable or not: split the range by heights", func() {
		big.DraftVoid("heights:1000-1001", true, "", "", 2_000, r.seatOf)
	})
	r.rng = &Range{From: 1_000, To: 1_000}
	r.height = 2_000
	dec := big.Decide(t0, t0, big.DraftVoid("heights:1000-1000", true, "", "", 2_000, r.seatOf), r.env())
	if dec.Queued() || dec.Note != "at once: 1000 voided at once (named by a guardian's payouts flag) of claims credited at heights 1000-1000, last voided at height 1000" || big.nOpen != 2 || big.Owed != 20*gnot || big.waitingCount() != 2 {
		t.Fatalf("the thousand: %+v open %d owed %d", dec, big.nOpen, big.Owed)
	}
	identity(t, big, big.Lock-big.Owed)
	// the natural cleanup of a flood — `since:` repeated — is refused once the forged thousand plus the open credits
	// after them exceed the cap, though the void could take only the 998 open ones: the walk would visit them all;
	// the advice is to split, and the first decision's note says where the next range starts
	for i := 0; i < 996; i++ {
		big.Credit(t0, 1_003, 0, idN(10000+i), erin, 10*gnot, BySigners, false)
	}
	mustPanic(t, "more than 1000 credits in the verifier's credits since height 1000 up to height 2000, voidable or not: split the range by heights", func() {
		big.DraftVoid("since:1000", true, "", "", 2_000, r.seatOf)
	})
	if d := big.DraftVoid("heights:1001-1003", true, "", "", 2_000, r.seatOf); d.Params.(VoidParams).Range.From != 1_001 {
		t.Fatalf("a range after the last voided height: %+v", d.Params)
	}
	// with 2,000 open credits after the forged thousand the same
	for i := 0; i < 1_002; i++ {
		big.Credit(t0, 1_004, 0, idN(12000+i), bob, 10*gnot, BySigners, false)
	}
	mustPanic(t, "more than 1000 credits in the verifier's credits since height 1000 up to height 2000, voidable or not", func() {
		big.DraftVoid("since:1000", true, "", "", 2_000, r.seatOf)
	})
	mustPanic(t, "more than 1000 credits in claims credited at heights 1001-1004, voidable or not", func() {
		big.DraftVoid("heights:1001-1004", true, "", "", 2_000, r.seatOf)
	})
	if big.nOpen != 2_000 {
		t.Fatalf("open %d", big.nOpen)
	}
}

// TestRangeCapBaseline credits what TestRangeVoidThousand voids and nothing else (its GAS line is the baseline).
func TestRangeCapBaseline(t *testing.T) {
	big := newState()
	lock(big, 1_000_000*gnot, 0)
	for i := 0; i < MaxRangeClaims; i++ {
		big.Credit(t0, 1_000, 0, idN(5000+i), alice, 10*gnot, BySigners, false)
	}
	big.Credit(t0, 1_001, 0, idN(9000), carol, 10*gnot, BySigners, false)
	big.Credit(t0, 1_002, 0, idN(9001), dave, 10*gnot, BySigners, false)
	if big.nOpen != MaxRangeClaims+2 {
		t.Fatalf("open %d", big.nOpen)
	}
}

// TestLaunchResetView (K22): until the first Claim after Launch applies the launch reset, the page and the JSON
// show the meter as the reset leaves it — a pure read, the state untouched — so no reader sees a full meter that
// Claim would then empty.
func TestLaunchResetView(t *testing.T) {
	s := newState() // the meter counts from t0, the deploy
	lock(s, 50_000*gnot, 0)
	launch := t0 + Day - hour
	// a day after the deploy the raw meter is full; a launch an hour ago leaves an hour's refill
	if !strings.Contains(s.Figures(t0+Day, 0, 0, 0, ""), "the bridge meter holds 10000 GNOT now") {
		t.Fatalf("before the launch:\n%s", s.Figures(t0+Day, 0, 0, 0, ""))
	}
	if !strings.Contains(s.Figures(t0+Day, 0, 0, launch, ""), "the bridge meter holds 416.666666 GNOT now") || !strings.Contains(s.JSON(t0+Day, 0, 0, launch), "\"meter\":416666666,") {
		t.Fatalf("the launch reset's view:\n%s", s.Figures(t0+Day, 0, 0, launch, ""))
	}
	if s.meter.Level != 0 || s.meter.At != t0 {
		t.Fatalf("a read moved the meter: %+v", s.meter)
	}
	// the reset applied (the first Claim does it): the same figures
	s.ResetMeters(launch)
	s.LaunchSeen = launch
	if !strings.Contains(s.JSON(t0+Day, 0, 0, launch), "\"meter\":416666666,") {
		t.Fatalf("after the reset: %s", s.JSON(t0+Day, 0, 0, launch))
	}
}

// TestSweepByID (R19): the sweep queue is settled by id — a payment names entries, pays their sum and drops them, so
// a paid entry can never be credited by id or voided a second time and the counters never go negative; the draft
// refuses an unknown id, a claim's, a voided entry and a sum above the lock; at execution an entry voided or gone
// since is skipped and a sum the lock no longer covers waits.
func TestSweepByID(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 1_000*gnot, 0)
	// alice's 60 landed addressed to r/hlfees (swept by the realm's flag) at height 10; bob's 2, below the fee, at
	// 11; a forger's 5,000, above the lock, at 12; dave's honest 100 is a claim
	if !s.Credit(t0, 10, 0, id(0), alice, 60*gnot, BySigners, true) || !s.Credit(t0, 11, 0, id(1), bob, 2*gnot, BySigners, false) || !s.Credit(t0, 12, 0, id(2), carol, 5_000*gnot, BySigners, false) {
		t.Fatalf("not swept")
	}
	s.Credit(t0, 13, 0, id(3), dave, 100*gnot, BySigners, false)
	mustPanic(t, "no sweep entry "+id(9), func() { s.DraftSweep(id(9), alice) })
	mustPanic(t, id(3)+" is a claim, not a sweep entry", func() { s.DraftSweep(id(3), dave) })
	mustPanic(t, "the entries sum to 5000 GNOT, above the lock's 1000 GNOT", func() { s.DraftSweep(id(2), carol) })
	r.flagged[id(2)] = true
	s.Decide(t0, t0, s.DraftVoid(id(2), true, "", "", 0, r.seatOf), r.env())
	mustPanic(t, id(2)+" is voided; restore it first", func() { s.DraftSweep(id(2), carol) })
	// two entries paid as one sum, dropped with their index
	d := s.Decide(t0, t0, s.DraftSweep(id(0)+","+id(1), alice), r.env())
	if !d.Queued() || d.Text != "pay 62 GNOT of the sweep queue to "+string(alice)+": "+id(0)+","+id(1) {
		t.Fatalf("sweep decision %+v", d)
	}
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if r.paid[string(alice)] != 62*gnot || s.Sweep != 0 || s.Owed != 100*gnot || s.PaidTotal != 62*gnot || s.Lock != 938*gnot || d.Note != "applied after the delay; 2 entries paid (62 GNOT)" {
		t.Fatalf("paid alice %d sweep %d owed %d paid %d lock %d note %q", r.paid[string(alice)], s.Sweep, s.Owed, s.PaidTotal, s.Lock, d.Note)
	}
	if s.SweepOf(id(0)) != nil || s.SweepOf(id(1)) != nil || s.ClaimJSON(id(0)) != "null" || strings.Contains(s.SweepsPage(0), id(1)) || strings.Contains(s.Figures(t0, 0, 0, 0, ""), id(0)) {
		t.Fatalf("a paid entry stands: %s %s", s.ClaimJSON(id(0)), s.SweepsPage(0))
	}
	identity(t, s, 1_000*gnot-162*gnot) // the real supply: alice's 60, bob's 2 and dave's 100 burned
	// the paid entries are done: a credit by id of either is refused (the message is done, no record stands), a void
	// finds no entry, a range over their heights nothing, a second payment nothing
	mustPanic(t, id(0)+" is done already", func() { s.DraftCredit(id(0), alice, 60*gnot, true) })
	mustPanic(t, "no claim or sweep entry "+id(1), func() { s.DraftVoid(id(1), true, "", "", 0, r.seatOf) })
	mustPanic(t, "no claim to void in claims credited at heights 10-11", func() { s.DraftVoid("heights:10-11", true, "", "", 50, r.seatOf) })
	mustPanic(t, "no sweep entry "+id(0), func() { s.DraftSweep(id(0), alice) })
	// an entry voided between the proposal and its execution is skipped, the rest paid, the note saying so
	s.Credit(t0, 14, 0, id(5), bob, 3*gnot, BySigners, false)
	s.Credit(t0, 15, 0, id(6), carol, 4*gnot, BySigners, false)
	d = s.Decide(t0, t0, s.DraftSweep(id(5)+","+id(6), bob), r.env())
	r.flagged[id(6)] = true
	s.Decide(t0, t0, s.DraftVoid(id(6), true, "", "", 0, r.seatOf), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if r.paid[string(bob)] != 3*gnot || s.Sweep != 0 || s.SweepOf(id(5)) != nil || !s.SweepOf(id(6)).Voided || d.Note != "applied after the delay; 1 entries paid (3 GNOT), 1 skipped (voided or gone since the proposal was made)" {
		t.Fatalf("a skipped entry: bob %d sweep %d note %q", r.paid[string(bob)], s.Sweep, d.Note)
	}
	// a sum the lock no longer covers waits: dave's 100 paid out of the lock leaves 835, under the 900 named
	s.Credit(t0, 16, 0, id(7), dave, 900*gnot, BySigners, true)
	d = s.Decide(t0, t0, s.DraftSweep(id(7), dave), r.env())
	s.Claim(t0+Day, 20, 0, 0, r.env())
	if s.Lock != 835*gnot {
		t.Fatalf("lock %d", s.Lock)
	}
	mustPanic(t, "the lock (835 GNOT) holds less than the entries' 900 GNOT now", func() { s.Execute(t0+decisions.Delay, d.ID, r.env()) })
	identity(t, s, s.Lock-s.Owed)
	// the invariant guards: the balance is the sum of the live entries, so a void or a credit of an entry above it
	// is refused (unreachable through the builders; a broken counter would be)
	e := s.SweepOf(id(7))
	s.Sweep = 1 * gnot
	mustPanic(t, id(7)+"'s 900 GNOT exceeds the sweep queue's balance of 1 GNOT", func() { s.voidSweep(e) })
	mustPanic(t, "exceeds the sweep queue's balance", func() { s.creditByVote(t0, creditParams{id(7), dave, 800 * gnot}, r.env()) })
	s.Sweep = 900 * gnot
}

// TestMigrationCreditBetweenPages (R20): a page carries each claim's recipient counter (the predecessor's final
// one), and the successor raises its own counter to it on first seeing the recipient, so a claim it credits between
// two pages — by hand, here — lands above every place a later page can claim, and bob's consecutive seqs on two
// pages import without a collision, in order, the successor's own credit behind them.
func TestMigrationCreditBetweenPages(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 10_000*gnot, 0)
	s.Credit(t0, 100, 0, id(0), bob, 100*gnot, BySigners, false) // bob's seq 1
	s.Credit(t0, 101, 0, id(1), bob, 200*gnot, BySigners, false) // bob's seq 2
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	d := s.Decide(t0, t0, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	s2 := newState()
	r2 := newRecorder()
	lock(s2, 1_000*gnot, 3)
	p0 := s.Export(0, 1) // the counters and bob's seq 1; seq 2 follows on page 1
	if len(p0.Claims) != 1 || p0.Claims[0].ID != id(0) || !p0.More {
		t.Fatalf("page 0: %+v", p0)
	}
	s2.Import(t0, 0, 0, p0)
	// between the pages GovDAO credits bob by hand on the successor (a fresh id)
	r2.height = 300
	hd := s2.Decide(t0, t0, s2.DraftCredit(id(5), bob, 50*gnot, false), r2.env())
	s2.Execute(t0+decisions.Delay, hd.ID, r2.env())
	p1 := s.Export(1, 1)
	if len(p1.Claims) != 1 || p1.Claims[0].ID != id(1) || !p1.More {
		t.Fatalf("page 1: %+v", p1)
	}
	s2.Import(t0, 0, 0, p1)
	var order []string
	s2.eachWaiting(func(_ string, _ int, c *Claim) bool { order = append(order, c.ID); return false })
	if len(order) != 3 || order[0] != id(0) || order[1] != id(1) || order[2] != id(5) || s2.ClaimOf(id(1)).Seq != 2 || s2.ClaimOf(id(5)).Seq != 3 {
		t.Fatalf("order %v seqs %d %d", order, s2.ClaimOf(id(1)).Seq, s2.ClaimOf(id(5)).Seq)
	}
	p2 := s.Export(2, 1) // the send
	if p2.More || len(p2.Sends) != 1 {
		t.Fatalf("page 2: %+v", p2)
	}
	s2.Import(t0, 0, 0, p2)
	if s2.Lock != 11_000*gnot || s2.Owed != 350*gnot || s2.nOpen != 3 {
		t.Fatalf("imported: lock %d owed %d open %d", s2.Lock, s2.Owed, s2.nOpen)
	}
	identity(t, s2, s2.Lock-s2.Owed)
	s2.Claim(t0+Day, 20, 0, 0, r2.env())
	if r2.paid[string(bob)] != 335*gnot || s2.nOpen != 0 {
		t.Fatalf("the successor pays: bob %d open %d", r2.paid[string(bob)], s2.nOpen)
	}
}

// TestImportingRefusesCredits (AP4): from page 0's import until the page with More false has arrived the
// successor refuses Credit (the message stays undelivered; the relayer retries), the page and the json saying so;
// the pages are imported in order, each once.
func TestImportingRefusesCredits(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 10_000*gnot, 0)
	s.Credit(t0, 100, 0, id(0), alice, 100*gnot, BySigners, false)
	s.Credit(t0, 101, 0, id(1), bob, 100*gnot, BySigners, false)
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	d := s.Decide(t0, t0, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	s2 := newState()
	lock(s2, 1_000*gnot, 3)
	if strings.Contains(s2.JSON(t0, 0, 0, 0), "\"importing\":true") {
		t.Fatalf("importing before any page")
	}
	p0 := s.Export(0, 1)
	s2.Import(t0, 0, 0, p0)
	if !strings.Contains(s2.JSON(t0, 0, 0, 0), "\"importing\":true") || !strings.Contains(s2.Figures(t0, 0, 0, 0, ""), "**importing**") {
		t.Fatalf("not importing: %s", s2.JSON(t0, 0, 0, 0))
	}
	mustPanic(t, "importing", func() { s2.Credit(t0, 300, 0, id(7), carol, 10*gnot, BySigners, false) })
	mustPanic(t, "page 1 is the next to import, not 0", func() { s2.Import(t0, 0, 0, p0) })
	p1 := s.Export(1, 1)
	s2.Import(t0, 0, 0, p1)
	mustPanic(t, "importing", func() { s2.Credit(t0, 300, 0, id(7), carol, 10*gnot, BySigners, false) })
	p2 := s.Export(2, 1)
	if p2.More {
		t.Fatalf("page 2 is not the last: %+v", p2)
	}
	s2.Import(t0, 0, 0, p2)
	if strings.Contains(s2.JSON(t0, 0, 0, 0), "\"importing\":true") || !strings.Contains(s2.JSON(t0, 0, 0, 0), "\"importing\":false") || strings.Contains(s2.Figures(t0, 0, 0, 0, ""), "**importing**") {
		t.Fatalf("still importing: %s", s2.JSON(t0, 0, 0, 0))
	}
	if s2.Credit(t0, 300, 0, id(7), carol, 10*gnot, BySigners, false) || s2.nOpen != 3 {
		t.Fatalf("a credit after the last page: open %d", s2.nOpen)
	}
	identity(t, s2, s2.Lock-s2.Owed)
}

// TestListSkipsChangedIDs (R22): a list — a void, a restore, a refund — executed after another decision took one of
// its ids skips that id and counts it in the note, the range form's rule, instead of panicking and blocking the
// whole proposal with its unpause option and seat removals; at once and after the delay alike.
func TestListSkipsChangedIDs(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 10_000*gnot, 0)
	for i := 0; i < 3; i++ {
		s.Credit(t0, 10, 0, id(i), alice, 100*gnot, BySigners, false)
	}
	s.Credit(t0, 11, 0, id(3), bob, 1*gnot, BySigners, false) // a sweep entry
	// two members' lists share id 1, both made while every id stands; a guardian's flag names them all, so each
	// executes at once — the second finds id 1 forged already and skips it
	d1 := s.DraftVoid(id(0)+","+id(1), true, "payouts", "", 20, r.seatOf)
	d2 := s.DraftVoid(id(1)+","+id(2)+","+id(3), true, "payouts", "", 20, r.seatOf)
	for i := 0; i < 4; i++ {
		r.flagged[id(i)] = true
	}
	s.Decide(t0, t0, d1, r.env())
	dec := s.Decide(t0, t0, d2, r.env())
	if dec.Queued() || dec.Note != "at once: 2 voided at once (named by a guardian's payouts flag), 1 skipped (not voidable as it stands), unpaused payouts" {
		t.Fatalf("the second list: %+v", dec)
	}
	if s.ClaimOf(id(1)).State != Forged || s.ClaimOf(id(2)).State != Forged || !s.SweepOf(id(3)).Voided || s.Owed != 0 || len(r.cleared) != 2 {
		t.Fatalf("after the lists: owed %d cleared %v", s.Owed, r.cleared)
	}
	identity(t, s, s.Lock)
	// the queued form: a list made while its ids stand, one of them voided as forged by another decision in between,
	// applies after the delay skipping it
	s.Credit(t0, 12, 0, id(4), carol, 100*gnot, BySigners, false)
	s.Credit(t0, 12, 0, id(5), dave, 100*gnot, BySigners, false)
	r.flagged = map[string]bool{}
	q := s.Decide(t0, t0, s.DraftVoid(id(4)+","+id(5), false, "", "", 20, r.seatOf), r.env())
	if !q.Queued() {
		t.Fatalf("an unflagged list should wait: %+v", q)
	}
	r.flagged[id(4)] = true
	s.Decide(t0, t0, s.DraftVoid(id(4), true, "", "", 20, r.seatOf), r.env())
	s.Execute(t0+decisions.Delay, q.ID, r.env())
	if s.ClaimOf(id(4)).State != Forged || s.ClaimOf(id(5)).State != Withheld || q.Note != "applied after the delay; 1 voided, 1 skipped (not voidable as it stands)" {
		t.Fatalf("after the delay: %s %s note %q", s.ClaimOf(id(4)).State, s.ClaimOf(id(5)).State, q.Note)
	}
	identity(t, s, s.Lock-s.Voided)
	// a restore list the same way: two queued restores of the forged id 4 — the second finds it restored and skips it;
	// a withheld id 5 restored at once by one list is skipped by another made while it stood withheld
	later := s.DraftRestore(id(5) + "," + id(4))
	rl := s.Decide(t0, t0, s.DraftRestore(id(4)), r.env())
	rl2 := s.Decide(t0, t0, s.DraftRestore(id(4)), r.env())
	if !rl.Queued() || !rl2.Queued() {
		t.Fatalf("forged restores should wait")
	}
	s.Execute(t0+decisions.Delay, rl.ID, r.env())
	s.Execute(t0+decisions.Delay, rl2.ID, r.env())
	if s.ClaimOf(id(4)).State != Open || s.Owed != 100*gnot || rl.Note != "applied after the delay; 1 restored" || rl2.Note != "applied after the delay; 0 restored, 1 skipped (not voided as it stands)" {
		t.Fatalf("restores: %s owed %d notes %q %q", s.ClaimOf(id(4)).State, s.Owed, rl.Note, rl2.Note)
	}
	s.Decide(t0, t0, s.DraftRestore(id(5)), r.env())
	dec = s.Decide(t0, t0, later, r.env())
	if dec.Queued() || dec.Note != "at once: 0 withheld restored at once, 2 skipped (not voided as it stands)" || s.ClaimOf(id(5)).State != Open || s.Voided != 0 {
		t.Fatalf("a restore list made earlier: %+v", dec)
	}
	identity(t, s, s.Lock-s.Owed)
	// a refund list — over sends imported from a predecessor (AQ2), handed to this route as a page would —: one of
	// its sends refunded by another decision in between is skipped, the other refunded
	inherit(s, SendItem{ID: id(21), From: erin, Amount: 100 * gnot, At: t0}, SendItem{ID: id(22), From: erin, Amount: 200 * gnot, At: t0})
	rf := s.Decide(t0, t0, s.DraftRefund(id(21)+","+id(22), t0, r.env()), r.env())
	rf2 := s.Decide(t0, t0, s.DraftRefund(id(21), t0, r.env()), r.env())
	applyRefund(s, t0+decisions.Delay, rf2.ID, r.env())
	ap := applyRefund(s, t0+decisions.Delay, rf.ID, r.env())
	if r.paid[string(erin)] != 300*gnot || s.SendOf(id(22)).Refunded == 0 || rf.Note != "applied by GovDAO's apply refund decision "+itoa(ap.ID)+"; 1 refunded, 1 skipped (refunded already, or gone)" {
		t.Fatalf("refunds: erin %d note %q", r.paid[string(erin)], rf.Note)
	}
	identity(t, s, s.Lock-s.Owed)
}

// TestFiguresCountsByCounter (R24): the page's count of waiting claims is the counter nOpen, not a walk of every
// waiting recipient, so a flood of fee-sized claims to fresh addresses cannot make the page a member reads exceed a
// query's gas; the counter and the walk agree (R12's invariant). The GAS line the harness prints for this test is
// the measure: 2,000 recipients' credits plus the page, the walk's cost gone from it.
func TestFiguresCountsByCounter(t *testing.T) {
	s := newState()
	lock(s, 10_000_000*gnot, 0)
	for i := 0; i < 2_000; i++ {
		s.Credit(t0, 7, 0, idN(20000+i), testutils.TestAddress("hlr-flood-"+strconv.Itoa(i)), 10*gnot, BySigners, false)
	}
	if s.nOpen != 2_000 || s.waitingCount() != 2_000 {
		t.Fatalf("open %d waiting %d", s.nOpen, s.waitingCount())
	}
	p := s.Figures(t0, 0, 0, 0, "")
	if !strings.Contains(p, "- … and 1900 more (2000 waiting): the whole list, by hundreds, at claims/0, claims/100") || strings.Count(p, " #1: 10 GNOT of 10 GNOT open (signers) at height 7 `") != 100 {
		t.Fatalf("the page:\n%s", p)
	}
	if !strings.Contains(s.JSON(t0, 0, 0, 0), "\"openClaims\":2000,") {
		t.Fatalf("json: %s", s.JSON(t0, 0, 0, 0))
	}
}

// TestClaimAboveTheLock (R26): a hand credit is bounded by the lock when it is made and when it would apply (R7's
// rule for the verifier's credits); a claim's turn never asks the lock for more than it holds — an honest claim
// left standing above the lock after a forgery drained part of it is paid what the lock holds, and a dry lock is
// a skip, never a reverting payment that stalls every recipient behind it.
func TestClaimAboveTheLock(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 2_000*gnot, 0) // under the 10,000 floor: the cap is 10,000, a recipient's quarter 2,500 — more than the lock
	mustPanic(t, "above the lock of 2000 GNOT", func() { s.DraftCredit(id(0), carol, 8_000*gnot, false) })
	d := s.Decide(t0, t0, s.DraftCredit(id(0), carol, 1_500*gnot, false), r.env())
	// alice's honest 1,500 and bob's forged 1,500, each within the lock; alice's turn comes first and takes 1,500 of
	// the 2,000, so bob's claim stands above what the lock holds: his turn pays the 500 left, never more
	s.Credit(t0, 0, 0, id(1), alice, 1_500*gnot, BySigners, false)
	s.Credit(t0, 0, 0, id(2), bob, 1_500*gnot, BySigners, false)
	n := s.Claim(t0+Day, 20, 0, 0, r.env())
	if n != 2 || r.paid[string(alice)] != 1_495*gnot || r.paid[string(bob)] != 495*gnot || s.Lock != 0 || s.ClaimOf(id(2)).Remaining() != 1_000*gnot || s.PaidTotal != 2_000*gnot {
		t.Fatalf("turns above the lock: paid alice %d bob %d, lock %d, bob's remainder %d", r.paid[string(alice)], r.paid[string(bob)], s.Lock, s.ClaimOf(id(2)).Remaining())
	}
	// the lock dry: a turn is a skip, nothing moves
	if n := s.Claim(t0+2*Day, 20, 0, 0, r.env()); n != 0 || s.Lock != 0 || s.PaidTotal != 2_000*gnot {
		t.Fatalf("a dry lock paid %d claims", n)
	}
	// the queued hand credit of 1,500 finds a lock of 0 and waits
	mustPanic(t, "above the lock of 0 GNOT now", func() { s.Execute(t0+decisions.Delay, d.ID, r.env()) })
	if s.ClaimOf(id(0)) != nil || !d.Pending() {
		t.Fatalf("a hand credit above the lock applied")
	}
	// a send refills the lock: bob's remainder pays, then the hand credit applies
	lock(s, 3_000*gnot, 1)
	s.Claim(t0+3*Day, 20, 0, 0, r.env())
	if r.paid[string(bob)] != 1_495*gnot || s.Lock != 2_000*gnot || s.ClaimOf(id(2)).State != Done {
		t.Fatalf("after the refill: bob %d lock %d", r.paid[string(bob)], s.Lock)
	}
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if c := s.ClaimOf(id(0)); c == nil || c.Amount != 1_500*gnot || s.Owed != 1_500*gnot {
		t.Fatalf("the hand credit: %+v owed %d", c, s.Owed)
	}
	identity(t, s, s.Lock-s.Owed)
}

// TestInheritedMessageWaitsForTheLastPage (AQ1): the mailbox lets a successor take a message addressed to its
// predecessor, and the route credits it only once the predecessor's last page is in — before page 0 the lock is
// not here, between pages Credit refuses anyway — so the message waits undelivered (the relayer brings it again);
// after the last page it is an ordinary credit of the successor's. A route that succeeded none refuses it for good.
func TestInheritedMessageWaitsForTheLastPage(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 10_000*gnot, 0)
	s.Credit(t0, 100, 0, id(0), alice, 100*gnot, BySigners, false)
	s.Credit(t0, 101, 0, id(1), bob, 100*gnot, BySigners, false)
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	d := s.Decide(t0, t0, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	s2 := newState()
	mustPanic(t, "addressed to another realm: a successor takes its predecessor's messages once the last page of the migration is in (0 pages so far)", func() { s2.MustHaveImported() })
	s2.Import(t0, 0, 0, s.Export(0, 1))
	mustPanic(t, "(1 pages so far)", func() { s2.MustHaveImported() })
	s2.Import(t0, 0, 0, s.Export(1, 1))
	mustPanic(t, "(2 pages so far)", func() { s2.MustHaveImported() })
	last := s.Export(2, 1)
	if last.More || s2.Imported() != 2 {
		t.Fatalf("page 2 is not the last: %+v (imported %d)", last, s2.Imported())
	}
	s2.Import(t0, 0, 0, last)
	s2.MustHaveImported()
	if s2.Credit(t0, 300, 0, id(7), carol, 10*gnot, BySigners, false) || s2.ClaimOf(id(7)) == nil || s2.nOpen != 3 || s2.Imported() != 3 {
		t.Fatalf("the inherited credit after the last page: open %d", s2.nOpen)
	}
	identity(t, s2, s2.Lock-s2.Owed)
	mustPanic(t, "(0 pages so far)", func() { newState().MustHaveImported() })
}

// TestRangeAcrossPages (R28): a range void and a migrate are refused while a predecessor's pages are still
// arriving — a page brings claims at their old heights, inside any range closed earlier, so a draft between two
// pages sees a partial record set — and the walk that voids a range, at once under a flag or after the delay,
// counts the credits the range covers now against MaxRangeClaims and refuses above it: a range drafted before page
// 0 over a height the pages then fill never asks a block for more than the cap (GovDAO cancels it and splits).
func TestRangeAcrossPages(t *testing.T) {
	// the predecessor: MaxRangeClaims + 1 claims at height 500, migrated in pages of 500
	s := newState()
	r := newRecorder()
	lock(s, 1_000_000*gnot, 0)
	for i := 0; i <= MaxRangeClaims; i++ {
		s.Credit(t0, 500, 0, idN(30000+i), alice, 10*gnot, BySigners, false)
	}
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	d := s.Decide(t0, t0, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	// the successor: two credits of its own at height 500 and, before any page, a range void over that height — two
	// credits when made, far under the cap — queued (no flag names them), and a second draft kept for Decide below
	s2 := newState()
	r2 := newRecorder()
	lock(s2, 1_000*gnot, 3)
	s2.Credit(t0, 500, 0, id(0), bob, 10*gnot, BySigners, false)
	s2.Credit(t0, 500, 0, id(1), carol, 10*gnot, BySigners, false)
	queued := s2.Decide(t0, t0, s2.DraftVoid("heights:500-500", true, "", "", 600, r2.seatOf), r2.env())
	if !queued.Queued() || !strings.Contains(queued.Note, "2 queued 7 days") {
		t.Fatalf("the range before the pages: %+v", queued)
	}
	later := s2.DraftVoid("heights:500-500", true, "", "", 600, r2.seatOf)
	// between the pages a range void and a migrate are refused; a list of ids, which names what stands, is not
	s2.Import(t0, 0, 0, s.Export(0, 500))
	mustPanic(t, "the predecessor's pages are still arriving (1 so far): a range void waits for the last page", func() {
		s2.DraftVoid("heights:500-500", true, "", "", 600, r2.seatOf)
	})
	r2.announced["gno.land/r/x/hlgnot3"] = "GNOT"
	mustPanic(t, "the predecessor's pages are still arriving (1 so far): a migrate waits for the last page", func() {
		s2.DraftMigrate("gno.land/r/x/hlgnot3", "gno.land/r/x/hlgnot2", r2.env())
	})
	if dl := s2.DraftVoid(id(0), false, "", "", 600, r2.seatOf); dl.Kind != KindVoid || dl.Params.(VoidParams).Ranged {
		t.Fatalf("a list between pages: %+v", dl)
	}
	s2.Import(t0, 0, 0, s.Export(1, 500))
	last := s.Export(2, 500)
	if last.More || len(last.Claims) != 1 || len(last.Sends) != 1 {
		t.Fatalf("page 2 is not the last: %+v", last)
	}
	s2.Import(t0, 0, 0, last)
	if s2.importing || s2.Imported() != 3 || s2.nOpen != MaxRangeClaims+3 {
		t.Fatalf("after the pages: importing %v, %d pages, %d open", s2.importing, s2.Imported(), s2.nOpen)
	}
	owed := s2.Owed
	// the queued range's walk would visit 1,003 credits now: refused when it would apply, nothing voided, the
	// decision pending for GovDAO's cancel
	mustPanic(t, "more than 1000 credits in claims credited at heights 500-500 now (a predecessor's pages brought more since the proposal was made): cancel it and split the range by heights", func() {
		s2.Execute(t0+decisions.Delay, queued.ID, r2.env())
	})
	if !queued.Pending() || s2.ClaimOf(id(0)).State != Open || s2.ClaimOf(idN(30000)).State != Open || s2.Owed != owed || s2.nOpen != MaxRangeClaims+3 {
		t.Fatalf("a refused walk voided something: owed %d open %d", s2.Owed, s2.nOpen)
	}
	// and at once: the draft made before the pages, decided now under a guardian's flag covering the height, the same
	r2.rng = &Range{From: 500, To: 500}
	mustPanic(t, "more than 1000 credits in claims credited at heights 500-500 now", func() { s2.Decide(t0, t0, later, r2.env()) })
	if s2.ClaimOf(id(0)).State != Open || s2.Owed != owed || s2.Ledger.Last() != queued.ID {
		t.Fatalf("a refused at-once walk voided or recorded something: owed %d decisions %d", s2.Owed, s2.Ledger.Last())
	}
	// a range the pages left under the cap is taken: the successor's own credits at a later height
	s2.Credit(t0, 700, 0, id(2), dave, 10*gnot, BySigners, false)
	r2.rng = &Range{From: 700, To: 700}
	dec := s2.Decide(t0, t0, s2.DraftVoid("heights:700-700", true, "", "", 800, r2.seatOf), r2.env())
	if dec.Queued() || s2.ClaimOf(id(2)).State != Forged {
		t.Fatalf("a range under the cap: %+v", dec)
	}
	identity(t, s2, s2.Lock-s2.Owed)
}

// TestRefundAboveTheLock (R30): a refund is bounded by the lock when the proposal is made and again when it would
// apply, as a sweep payment is — the sends a successor imported summed to what page 0 brought, but a forgery paid
// before its void leaves the lock short — so a refund the lock no longer covers waits, the decision pending for a
// refill or a cancel, instead of the realm's coin balance being the only guard (a payment that fails on chain
// until a stranger's coins make it succeed, the lock reading negative and the identity broken by that much).
func TestRefundAboveTheLock(t *testing.T) {
	s := newState()
	r := newRecorder()
	inherit(s, SendItem{ID: id(0), From: alice, Amount: 1_000 * gnot, At: t0}, SendItem{ID: id(1), From: bob, Amount: 2_000 * gnot, At: t0}) // the lock, 3,000, from the predecessor
	if s.Lock != 3_000*gnot || !s.SendOf(id(1)).Imported {
		t.Fatalf("inherited: lock %d", s.Lock)
	}
	// bob's refund is drafted while the lock covers it; a forged claim of 2,800 is then paid 2,500 (a quarter of the
	// floor's cap, a day on) before any void, leaving 500
	d := s.Decide(t0, t0, s.DraftRefund(id(1), t0, r.env()), r.env())
	s.Credit(t0, 0, 0, id(2), carol, 2_800*gnot, BySigners, false)
	s.Claim(t0+Day, 20, 0, 0, r.env())
	if s.Lock != 500*gnot || r.paid[string(carol)] != 2_495*gnot {
		t.Fatalf("after the forgery's turn: lock %d carol %d", s.Lock, r.paid[string(carol)])
	}
	mustPanic(t, "the sends sum to 3000 GNOT, above the lock's 500 GNOT", func() { s.DraftRefund(id(0)+","+id(1), t0, r.env()) })
	mustPanic(t, "the sends sum to 1000 GNOT, above the lock's 500 GNOT", func() { s.DraftRefund(id(0), t0, r.env()) })
	// the queued refund of 2,000 finds a lock of 500 and waits: GovDAO's apply (AT2) is refused, nothing paid,
	// nothing marked, nothing recorded, the counters whole
	last := s.Ledger.Last()
	mustPanic(t, "the lock (500 GNOT) holds less than the sends' 2000 GNOT now", func() { applyRefund(s, t0+decisions.Delay, d.ID, r.env()) })
	if !d.Pending() || s.SendOf(id(1)).Refunded != 0 || s.Lock != 500*gnot || s.SentTotal != 3_000*gnot || r.paid[string(bob)] != 0 || s.Ledger.Last() != last {
		t.Fatalf("a refund above the lock applied: lock %d sent %d bob %d, decisions %d", s.Lock, s.SentTotal, r.paid[string(bob)], s.Ledger.Last())
	}
	identity(t, s, s.Lock-s.Owed)
	// sends refill the lock: the refund applies, bob paid in full, the identity whole
	lock(s, 2_000*gnot, 0)
	ap := applyRefund(s, t0+decisions.Delay, d.ID, r.env())
	if r.paid[string(bob)] != 2_000*gnot || s.Lock != 500*gnot || s.SentTotal != 3_000*gnot || s.SendOf(id(1)).Refunded == 0 || d.Note != "applied by GovDAO's apply refund decision "+itoa(ap.ID)+"; 1 refunded" {
		t.Fatalf("after the refill: bob %d lock %d sent %d note %q", r.paid[string(bob)], s.Lock, s.SentTotal, d.Note)
	}
	identity(t, s, s.Lock-s.Owed)
}

// TestMigrateGuards (R31): a route registered as a successor migrates on only once its own import has ended —
// before page 0 nothing has moved, and its MigratedTo would make its Import refuse, the predecessor's Pull revert for
// ever and the lock sit on the first predecessor, which refuses everything, with no decision that reaches it — so
// a migrate of a successor that pulled nothing is refused when the proposal is made and, drafted before the
// registration (an announced realm not yet a route is a launch route to its state), when it would apply: pending
// for a cancel, never spent. And a migrate never names a route the mailbox registers already, whatever it
// announced: the mailbox would refuse it a week on, the vote wasted.
func TestMigrateGuards(t *testing.T) {
	s := newState() // the predecessor, migrated to hlgnot2
	r := newRecorder()
	lock(s, 10_000*gnot, 0)
	s.Credit(t0, 100, 0, id(0), alice, 100*gnot, BySigners, false)
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	d := s.Decide(t0, t0, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	// the successor, registered (its predecessor named by the mailbox) with nothing pulled: a migrate on is refused
	s2 := newState()
	r2 := newRecorder()
	r2.predecessor = "gno.land/r/x/hlgnot"
	r2.announced["gno.land/r/x/hlgnot3"] = "GNOT"
	mustPanic(t, "page 0 is not in yet: this route succeeds gno.land/r/x/hlgnot and has pulled nothing; a migrate waits for the last page of its own import", func() {
		s2.DraftMigrate("gno.land/r/x/hlgnot3", "gno.land/r/x/hlgnot2", r2.env())
	})
	// a route the mailbox registers already is refused when the proposal is made, whatever it announced
	r2.predecessor = ""
	r2.isRoute["gno.land/r/x/hlgnot3"] = true
	mustPanic(t, "the successor gno.land/r/x/hlgnot3 is a route already", func() {
		s2.DraftMigrate("gno.land/r/x/hlgnot3", "gno.land/r/x/hlgnot2", r2.env())
	})
	delete(r2.isRoute, "gno.land/r/x/hlgnot3")
	// drafted and decided before the registration, registered by the time it would apply with nothing pulled: the
	// migration is refused and the decision stays pending; the pages in, it applies
	dm := s2.Decide(t0, t0, s2.DraftMigrate("gno.land/r/x/hlgnot3", "gno.land/r/x/hlgnot2", r2.env()), r2.env())
	if !dm.Queued() {
		t.Fatalf("a migrate waits: %+v", dm)
	}
	r2.predecessor = "gno.land/r/x/hlgnot"
	mustPanic(t, "page 0 is not in yet", func() { s2.Execute(t0+decisions.Delay, dm.ID, r2.env()) })
	if !dm.Pending() || s2.Migrated() || len(r2.routes) != 0 {
		t.Fatalf("a migrate of an un-pulled successor applied: %+v migrated %q routes %v", dm, s2.MigratedTo, r2.routes)
	}
	s2.Import(t0, 0, 0, s.Export(0, PullPage))
	s2.Execute(t0+decisions.Delay, dm.ID, r2.env())
	if s2.MigratedTo != "gno.land/r/x/hlgnot3" || len(r2.routes) != 1 || r2.routes[0] != "gno.land/r/x/hlgnot3" {
		t.Fatalf("after the pages: migrated %q routes %v", s2.MigratedTo, r2.routes)
	}
}

// TestMigrateWhileImporting (R28, AR2): a successor whose pages are still arriving is refused a migrate on at all
// three clocks — when the proposal is made, when it executes (nothing recorded: the proposal survives for a retry)
// and when a decision taken before the pages would apply (pending, never spent) — and takes it once the last page
// is in.
func TestMigrateWhileImporting(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 10_000*gnot, 0)
	s.Credit(t0, 100, 0, id(0), alice, 100*gnot, BySigners, false)
	s.Credit(t0, 101, 0, id(1), bob, 100*gnot, BySigners, false)
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	d := s.Decide(t0, t0, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	// the successor drafts, and decides, a migrate on before its registration (a launch route to its state then)
	s2 := newState()
	r2 := newRecorder()
	r2.announced["gno.land/r/x/hlgnot3"] = "GNOT"
	draft := s2.DraftMigrate("gno.land/r/x/hlgnot3", "gno.land/r/x/hlgnot2", r2.env())
	queued := s2.Decide(t0, t0, draft, r2.env())
	if !queued.Queued() {
		t.Fatalf("a migrate waits: %+v", queued)
	}
	// registered, page 0 of three in: refused at every clock, nothing recorded, the decision pending
	r2.predecessor = "gno.land/r/x/hlgnot"
	s2.Import(t0, 0, 0, s.Export(0, 1))
	still := "the predecessor's pages are still arriving (1 so far): a migrate waits for the last page"
	mustPanic(t, still, func() { s2.DraftMigrate("gno.land/r/x/hlgnot3", "gno.land/r/x/hlgnot2", r2.env()) })
	mustPanic(t, still, func() { s2.Decide(t0, t0, draft, r2.env()) })
	mustPanic(t, still, func() { s2.Execute(t0+decisions.Delay, queued.ID, r2.env()) })
	if s2.Ledger.Last() != queued.ID || !queued.Pending() || s2.Migrated() || len(r2.routes) != 0 {
		t.Fatalf("a migrate between pages: decisions %d, %+v, migrated %q, routes %v", s2.Ledger.Last(), queued, s2.MigratedTo, r2.routes)
	}
	s2.Import(t0, 0, 0, s.Export(1, 1))
	mustPanic(t, "(2 so far)", func() { s2.Execute(t0+decisions.Delay, queued.ID, r2.env()) })
	last := s.Export(2, 1)
	if last.More {
		t.Fatalf("page 2 is not the last: %+v", last)
	}
	s2.Import(t0, 0, 0, last)
	// the last page in: the decision taken before the pages applies
	s2.Execute(t0+decisions.Delay, queued.ID, r2.env())
	if s2.MigratedTo != "gno.land/r/x/hlgnot3" || queued.State() != "applied" || len(r2.routes) != 1 || r2.routes[0] != "gno.land/r/x/hlgnot3" {
		t.Fatalf("after the last page: migrated %q, %+v, routes %v", s2.MigratedTo, queued, r2.routes)
	}
	identity(t, s2, s2.Lock-s2.Owed)
}

// TestRepointMigrate (AR2): before page 0 nothing has moved, so a predecessor whose successor pulled nothing takes
// a second migrate, after 7 days, that re-points the migration to another successor — the cure for a successor that
// never pulls, or whose driver refuses every caller, which would otherwise hold the lock on a predecessor that
// refuses everything, with no decision that reaches it. The same successor is refused (nothing to re-point) and
// every other builder stays refused; once page 0 is pulled the migration is under way and a second migrate is
// refused when it is made and, decided before the pull, when it would apply — pending for a cancel, the one
// decision left.
func TestRepointMigrate(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 10_000*gnot, 0)
	s.Credit(t0, 100, 0, id(0), alice, 100*gnot, BySigners, false)
	for _, p := range []string{"gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot3", "gno.land/r/x/hlgnot4"} {
		r.announced[p] = "GNOT"
	}
	d := s.Decide(t0, t0, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	if s.MigratedTo != "gno.land/r/x/hlgnot2" || s.Exported() != 0 {
		t.Fatalf("migrated %q exported %d", s.MigratedTo, s.Exported())
	}
	// hlgnot2 pulls nothing: a migrate to it again is refused, every other builder refuses as before, and a migrate
	// to hlgnot3 — and one to hlgnot4, both decided while nothing is pulled — waits 7 days
	mustPanic(t, "migrated to gno.land/r/x/hlgnot2 already (nothing pulled yet): a re-point names another successor", func() {
		s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env())
	})
	mustPanic(t, "migrated to gno.land/r/x/hlgnot2", func() { s.DraftCapFloor(12_000 * gnot) })
	mustPanic(t, "migrated to gno.land/r/x/hlgnot2", func() { s.DraftRestore(id(0)) })
	d3 := s.Decide(t0+decisions.Delay, t0+decisions.Delay, s.DraftMigrate("gno.land/r/x/hlgnot3", "gno.land/r/x/hlgnot", r.env()), r.env())
	d4 := s.Decide(t0+decisions.Delay, t0+decisions.Delay, s.DraftMigrate("gno.land/r/x/hlgnot4", "gno.land/r/x/hlgnot", r.env()), r.env())
	if !d3.Queued() || !d4.Queued() || s.MigratedTo != "gno.land/r/x/hlgnot2" || d3.Text != "migrate to gno.land/r/x/hlgnot3 (announced as the GNOT route's successor)" {
		t.Fatalf("a re-point waits: %+v %+v migrated %q", d3, d4, s.MigratedTo)
	}
	// the re-point applies: the mailbox is told the new successor, and nothing of the state moved meanwhile
	s.Execute(t0+2*decisions.Delay, d3.ID, r.env())
	if s.MigratedTo != "gno.land/r/x/hlgnot3" || len(r.routes) != 2 || r.routes[1] != "gno.land/r/x/hlgnot3" || s.Lock != 10_000*gnot || s.Owed != 100*gnot || s.Exported() != 0 {
		t.Fatalf("re-pointed: migrated %q routes %v lock %d owed %d exported %d", s.MigratedTo, r.routes, s.Lock, s.Owed, s.Exported())
	}
	identity(t, s, s.Lock-s.Owed)
	// hlgnot3 pulls page 0: the migration is under way, and the decision for hlgnot4 is refused when it would apply —
	// pending, nothing moved — as a fresh draft is; a cancel is the one decision left
	snap := s.Export(0, PullPage)
	if snap.Lock != 10_000*gnot || len(snap.Claims) != 1 || snap.More || s.Exported() != 1 {
		t.Fatalf("page 0 to the new successor: %+v", snap)
	}
	under := "migrated to gno.land/r/x/hlgnot3: the successor pulled 1 pages already (the lock moved with page 0); a migration under way is never re-pointed"
	mustPanic(t, under, func() { s.Execute(t0+2*decisions.Delay, d4.ID, r.env()) })
	mustPanic(t, under, func() { s.DraftMigrate("gno.land/r/x/hlgnot4", "gno.land/r/x/hlgnot", r.env()) })
	if !d4.Pending() || s.MigratedTo != "gno.land/r/x/hlgnot3" || len(r.routes) != 2 {
		t.Fatalf("a migration under way was re-pointed: %+v migrated %q routes %v", d4, s.MigratedTo, r.routes)
	}
	c := s.Decide(t0+2*decisions.Delay, t0+2*decisions.Delay, s.DraftCancel(d4.ID), r.env())
	if c.Queued() || d4.State() != "cancelled" {
		t.Fatalf("cancel: %+v %s", c, d4.State())
	}
	// the new successor imports what the first never did
	s3 := newState()
	s3.Import(t0+2*decisions.Delay, 0, 0, snap)
	if s3.Lock != 10_000*gnot || s3.ClaimOf(id(0)) == nil || s3.Imported() != 1 {
		t.Fatalf("imported: lock %d", s3.Lock)
	}
	identity(t, s3, s3.Lock-s3.Owed)
}

// TestMustTake (R32, AR3): a route registered as a successor credits nothing before its predecessor's last page is
// in — a message addressed to itself included, not only one addressed to the predecessor (AQ1): before page 0 the
// lock is not here, so an honest burn to the successor, enrolled by kind 6 the day the migrate applied, would be
// swept as one above the lock, owed and paying nobody, 256 ids and 7 days a sweep payment — so every message
// waits undelivered until then and the relayer brings it again. A launch route takes its own messages as before
// and refuses another realm's for good.
func TestMustTake(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 10_000*gnot, 0)
	s.Credit(t0, 100, 0, id(0), alice, 100*gnot, BySigners, false)
	s.Credit(t0, 101, 0, id(1), bob, 100*gnot, BySigners, false)
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	d := s.Decide(t0, t0, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	// a launch route: its own message taken, another realm's refused for good
	s.MustTake(true, "")
	mustPanic(t, "addressed to another realm: a successor takes its predecessor's messages once the last page of the migration is in (0 pages so far)", func() { s.MustTake(false, "") })
	// the successor before page 0: everything waits, its own message too
	s2 := newState()
	own := "this route succeeds gno.land/r/x/hlgnot: nothing is credited before the last page of the migration is in (0 pages so far)"
	mustPanic(t, own, func() { s2.MustTake(true, "gno.land/r/x/hlgnot") })
	mustPanic(t, "(0 pages so far)", func() { s2.MustTake(false, "gno.land/r/x/hlgnot") })
	// between pages the same
	s2.Import(t0, 0, 0, s.Export(0, 1))
	mustPanic(t, "this route succeeds gno.land/r/x/hlgnot: nothing is credited before the last page of the migration is in (1 pages so far)", func() { s2.MustTake(true, "gno.land/r/x/hlgnot") })
	mustPanic(t, "addressed to another realm: a successor takes its predecessor's messages once the last page of the migration is in (1 pages so far)", func() { s2.MustTake(false, "gno.land/r/x/hlgnot") })
	s2.Import(t0, 0, 0, s.Export(1, 1))
	last := s.Export(2, 1)
	if last.More {
		t.Fatalf("page 2 is not the last: %+v", last)
	}
	s2.Import(t0, 0, 0, last)
	// the last page in: both taken, and the credit is an ordinary one
	s2.MustTake(true, "gno.land/r/x/hlgnot")
	s2.MustTake(false, "gno.land/r/x/hlgnot")
	if s2.Credit(t0, 300, 0, id(7), carol, 10*gnot, BySigners, false) || s2.nOpen != 3 {
		t.Fatalf("a credit after the last page: open %d", s2.nOpen)
	}
	identity(t, s2, s2.Lock-s2.Owed)
}

// TestMustSend (R35, F28): a route registered as a successor takes no send before its predecessor's last page is
// in — the mirror of MustTake: a send through a successor that pulled nothing would lock coins in a realm a
// re-point then drops, which Solana never enrols (kind 6 waits for page 0), which cannot refund it (its own send)
// and cannot migrate (a route no more) — so nothing of anyone's is ever in a successor a re-point drops. A launch
// route sends as it always did; a successor from its last page on; Importing says where the import stands.
func TestMustSend(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 10_000*gnot, 0)
	s.Credit(t0, 100, 0, id(0), alice, 100*gnot, BySigners, false)
	s.Credit(t0, 101, 0, id(1), bob, 100*gnot, BySigners, false)
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	d := s.Decide(t0, t0, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	// a launch route: its sends taken (the migrated predecessor's CheckSend refuses on its own ground), with no
	// enrolment note ever (the setup step enrols a launch route, never a kind 6)
	s2 := newState()
	r2 := newRecorder()
	r2.enrolledH, r2.enrolledAt = 0, 0
	s2.MustSend("", r2.env())
	if s2.Importing() || s2.Imported() != 0 {
		t.Fatalf("a fresh route is importing")
	}
	// the successor before page 0: no send, and none between pages — the pages first, whatever the note says
	own := "this route succeeds gno.land/r/x/hlgnot: nothing is sent before the last page of the migration is in (0 pages so far)"
	mustPanic(t, own, func() { s2.MustSend("gno.land/r/x/hlgnot", r2.env()) })
	s2.Import(t0, 0, 0, s.Export(0, 1))
	if !s2.Importing() {
		t.Fatalf("not importing after page 0 of three")
	}
	mustPanic(t, "this route succeeds gno.land/r/x/hlgnot: nothing is sent before the last page of the migration is in (1 pages so far)", func() { s2.MustSend("gno.land/r/x/hlgnot", r2.env()) })
	s2.Import(t0, 0, 0, s.Export(1, 1))
	mustPanic(t, "(2 pages so far)", func() { s2.MustSend("gno.land/r/x/hlgnot", r2.env()) })
	last := s.Export(2, 1)
	if last.More {
		t.Fatalf("page 2 is not the last: %+v", last)
	}
	s2.Import(t0, 0, 0, last)
	if s2.Importing() {
		t.Fatalf("still importing after the last page")
	}
	// the last page in, but no kind 6 dispatched for this realm yet (AT7): no send — the token program refuses a
	// sender that is not its enrolled router, so the send would park on Solana until a kind 6 lands (weeks under a
	// hold, never if GovDAO cancels it), unrefundable as the route's own; a cleared note (r/hlgov's cancel) closes
	// the gate again
	noNote := "this route succeeds gno.land/r/x/hlgnot: nothing is sent before r/hlgov has dispatched the kind 6 enrolling this realm as the token's router on Solana (no enrolment note yet): a send before it would park there until a kind 6 lands, unrefundable as the route's own"
	mustPanic(t, noNote, func() { s2.MustSend("gno.land/r/x/hlgnot", r2.env()) })
	// the note in: a send is an ordinary one
	r2.enrolledH, r2.enrolledAt = 700, t0
	s2.MustSend("gno.land/r/x/hlgnot", r2.env())
	r2.enrolledH, r2.enrolledAt = 0, 0
	mustPanic(t, noNote, func() { s2.MustSend("gno.land/r/x/hlgnot", r2.env()) })
	r2.enrolledH, r2.enrolledAt = 900, t0+Day
	s2.MustSend("gno.land/r/x/hlgnot", r2.env())
	s2.CheckSend(10 * gnot)
	s2.Send(t0, id(9), erin, 10*gnot)
	if s2.Lock != 10_010*gnot || s2.SendOf(id(9)).Imported {
		t.Fatalf("the successor's own send: lock %d", s2.Lock)
	}
	identity(t, s2, s2.Lock-s2.Owed)
}

// TestSuccessorMetersStartEmpty (R34): a successor's meters start empty when page 0 lands, as a launch route's do
// at Launch and every route's after an unpause — page 0's Import empties the bridge meter and every quarter as of
// now and takes the mailbox's launch time as the one the meters were last emptied for, so the realm's first Claim
// applies no launch reset (which rewound every meter to the launch, months back, and filled them all at once: a
// third cap in the 24 hours the predecessor closed, drawn by four addresses before a watch's second poll) — and
// the first Claim after the last page pays nothing until the meter refills: one cap the first day, never more.
func TestSuccessorMetersStartEmpty(t *testing.T) {
	s := newState()
	r := newRecorder()
	lock(s, 50_000*gnot, 0) // the floor binds: cap 10,000, a quarter 2,500
	for i, a := range []address{alice, bob, carol, dave} {
		s.Credit(t0, 0, 0, id(i), a, 50_000*gnot, BySigners, false)
	}
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	d := s.Decide(t0, t0, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+decisions.Delay, d.ID, r.env())
	// the successor, deployed at t0 (its raw meter counts from then), takes the pages a month on; the mailbox's
	// launch was long before
	launch := t0 - 100*Day
	at := t0 + 30*Day
	s2 := newState()
	r2 := newRecorder()
	s2.Import(at, 0, launch, s.Export(0, 2))
	s2.Import(at, 0, launch, s.Export(1, 2))
	last := s.Export(2, 2)
	if last.More {
		t.Fatalf("page 2 is not the last: %+v", last)
	}
	s2.Import(at, 0, launch, last)
	if s2.LaunchSeen != launch {
		t.Fatalf("the launch the meters were emptied for: %d, want %d", s2.LaunchSeen, launch)
	}
	// the realm's syncLaunch finds nothing to reset; the meters are empty as of the import
	if la := launch; la != s2.LaunchSeen {
		s2.ResetMeters(la)
		s2.LaunchSeen = la
	}
	if b, q := s2.MeterLevels(at, alice, 0, 0); b != 0 || q != 0 {
		t.Fatalf("the meters after the pages: %d %d", b, q)
	}
	if n := s2.Claim(at, 20, 0, 0, r2.env()); n != 0 || r2.total() != 0 {
		t.Fatalf("the first Claim paid %d claims, %d", n, r2.total())
	}
	if !strings.Contains(s2.JSON(at, 0, 0, launch), "\"meter\":0,") || !strings.Contains(s2.Figures(at, 0, 0, launch, ""), "the bridge meter holds 0 GNOT now") {
		t.Fatalf("the view after the pages: %s", s2.JSON(at, 0, 0, launch))
	}
	// an hour on, an hour's refill: each quarter 1/24 of 2,500, the bridge 1/24 of 10,000; at 24 hours one cap
	n := s2.Claim(at+hour, 20, 0, 0, r2.env())
	q := 2_500 * gnot / 24
	if n != 4 || r2.total() != 4*q {
		t.Fatalf("after an hour: %d claims, %d paid, want %d", n, r2.total(), 4*q)
	}
	s2.Claim(at+Day, 20, 0, 0, r2.env())
	if r2.total() != 10_000*gnot {
		t.Fatalf("the first day paid %d, want one cap", r2.total())
	}
	identity(t, s2, s2.Lock-s2.Owed)
}

// TestRefundWaitsForEnrolment (AR1, R37): a refund is refused — when the proposal is made and when it would
// apply, pending — until kind 6 has enrolled this route on Solana for 14 days, as r/hlgov noted the dispatch with
// the mailbox (Env.Enrolled): a week for the delivery and a week for Solana's own clock, which executes a kind 6
// 7 days after it is recorded there — so a 7-day wait from the dispatch always ended before the enrolment could
// take effect (R37, F27) — since a refunded send stays deliverable on Solana until the token's router is this
// route and no longer the predecessor's, and a refund that lands first lets one stranger's Reemit and delivery
// mint the send a second time, breaking the identity for good; the latest note counts, so a later kind 6 restarts
// the wait. Necessary, not sufficient: a hold defers Solana further, which hlwatch and the relayer read there.
func TestRefundWaitsForEnrolment(t *testing.T) {
	s := newState()
	r := newRecorder()
	inherit(s, SendItem{ID: id(0), From: alice, Amount: 1_000 * gnot, At: t0}, SendItem{ID: id(1), From: bob, Amount: 2_000 * gnot, At: t0})
	r.enrolledH, r.enrolledAt = 0, 0
	mustPanic(t, "no kind 6 has enrolled this route on Solana yet (none dispatched from r/hlgov): a refund waits 14 days after the enrolment", func() {
		s.DraftRefund(id(1), t0+20*Day, r.env())
	})
	// the gate comes first: an id that is no refundable send is refused for the enrolment all the same
	mustPanic(t, "no kind 6 has enrolled this route", func() { s.DraftRefund(id(9), t0+20*Day, r.env()) })
	// 13 days after the dispatch refused, 14 taken
	r.enrolledH, r.enrolledAt = 500, t0
	mustPanic(t, "kind 6 enrolling this route was dispatched at height 500, 1123200 s ago: a refund waits 14 days after it (until "+itoa(t0+2*decisions.Delay)+"): a week for the delivery and a week for Solana's own clock", func() {
		s.DraftRefund(id(1), t0+13*Day, r.env())
	})
	mustPanic(t, "a refund waits 14 days after it", func() { s.DraftRefund(id(1), t0+2*decisions.Delay-1, r.env()) })
	d := s.Decide(t0+2*decisions.Delay, t0+2*decisions.Delay, s.DraftRefund(id(1), t0+2*decisions.Delay, r.env()), r.env())
	if !d.Queued() {
		t.Fatalf("a refund waits: %+v", d)
	}
	// a later kind 6 for this route, noted four days before the refund would apply, restarts the wait: GovDAO's
	// apply (AT2) is refused when it would apply, pending, nothing paid or marked; taken once its 14 days have passed
	r.enrolledH, r.enrolledAt = 900, t0+2*decisions.Delay+4*Day
	mustPanic(t, "kind 6 enrolling this route was dispatched at height 900", func() { applyRefund(s, t0+3*decisions.Delay, d.ID, r.env()) })
	mustPanic(t, "kind 6 enrolling this route was dispatched at height 900", func() { applyRefund(s, t0+4*decisions.Delay+4*Day-1, d.ID, r.env()) })
	if !d.Pending() || s.SendOf(id(1)).Refunded != 0 || r.paid[string(bob)] != 0 || s.Lock != 3_000*gnot {
		t.Fatalf("a refund applied inside the wait: %+v", d)
	}
	applyRefund(s, t0+4*decisions.Delay+4*Day, d.ID, r.env())
	if r.paid[string(bob)] != 2_000*gnot || s.SendOf(id(1)).Refunded != t0+4*decisions.Delay+4*Day || s.Lock != 1_000*gnot {
		t.Fatalf("after the wait: bob %d lock %d", r.paid[string(bob)], s.Lock)
	}
	identity(t, s, s.Lock-s.Owed)
}

// TestWriteOff (AR1): a send delivered on Solana after its refund minted a second time, and no builder but this one
// closes the identity — a write-off (7 days) books the amount as lost and marks the send (send/<id>, the json's
// writtenOff and writtenOffTotal, the page), once; only a refunded send is written off, the member having checked
// hlwatch's DELIVERED line for the id; one written off by another decision since the proposal was made is skipped
// and counted; the mark goes with the record to the next route.
func TestWriteOff(t *testing.T) {
	s := newState()
	r := newRecorder()
	inherit(s, SendItem{ID: id(0), From: alice, Amount: 1_000 * gnot, At: t0}, SendItem{ID: id(1), From: bob, Amount: 2_000 * gnot, At: t0})
	d := s.Decide(t0, t0, s.DraftRefund(id(1), t0, r.env()), r.env())
	applyRefund(s, t0+decisions.Delay, d.ID, r.env())
	identity(t, s, 1_000*gnot) // the refunded send never delivered: the supply is alice's 1,000
	mustPanic(t, id(0)+" is not a refunded send: only a send delivered on Solana after its refund is written off", func() { s.DraftWriteOff(id(0)) })
	mustPanic(t, id(9)+" is not a refunded send", func() { s.DraftWriteOff(id(9)) })
	// bob's refunded send is delivered on Solana after all (a stranger's Reemit): the supply is 3,000 again and the
	// identity is off by 2,000 until the write-off applies
	w := s.Decide(t0+decisions.Delay, t0+decisions.Delay, s.DraftWriteOff(id(1)), r.env())
	if !w.Queued() || w.Kind != KindWriteOff || w.Text != "write off 2000 GNOT of refunded sends delivered on Solana after their refund (hlwatch's DELIVERED line names the id): "+id(1) || s.Lost != 0 {
		t.Fatalf("a write-off waits: %+v", w)
	}
	w2 := s.Decide(t0+decisions.Delay, t0+decisions.Delay, s.DraftWriteOff(id(1)), r.env())
	s.Execute(t0+2*decisions.Delay, w.ID, r.env())
	if s.Lost != 2_000*gnot || s.SendOf(id(1)).WrittenOff != t0+2*decisions.Delay || s.SendOf(id(1)).Refunded == 0 || w.Note != "applied after the delay; 1 written off (2000 GNOT)" || s.Lock != 1_000*gnot || s.SentTotal != 1_000*gnot {
		t.Fatalf("written off: lost %d note %q", s.Lost, w.Note)
	}
	identity(t, s, 3_000*gnot)
	if !strings.HasSuffix(s.SendJSON(id(1)), ",\"refunded\":"+itoa(t0+decisions.Delay)+",\"writtenOff\":"+itoa(t0+2*decisions.Delay)+"}") || !strings.Contains(s.JSON(t0, 0, 0, 0), "\"refunded\":1,\"refundedTotal\":2000000000,\"writtenOff\":1,\"writtenOffTotal\":2000000000,") || !strings.Contains(s.Figures(t0, 0, 0, 0, ""), "sends recorded: 2, refunded 1 (2000 GNOT), written off 1 (2000 GNOT); decisions: ") {
		t.Fatalf("the mark: %s\n%s\n%s", s.SendJSON(id(1)), s.JSON(t0, 0, 0, 0), s.Figures(t0, 0, 0, 0, ""))
	}
	mustPanic(t, id(1)+" is written off already", func() { s.DraftWriteOff(id(1)) })
	s.Execute(t0+2*decisions.Delay, w2.ID, r.env())
	if s.Lost != 2_000*gnot || w2.Note != "applied after the delay; 0 written off (0 GNOT), 1 skipped (written off already, never refunded, or gone)" {
		t.Fatalf("a second write-off: lost %d note %q", s.Lost, w2.Note)
	}
	// a mistaken write-off has its inverse (R38): a restore naming the send's id — a 7-day decision, like a forged
	// claim's restore, since it re-opens the identity — clears the mark and takes the amount back out of lost, the
	// counters down with it, so the identity closes on the old supply again (the send was never delivered after
	// all); a send not written off is nothing to restore, and a second restore skips it
	mustPanic(t, id(0)+" is a send that is not written off: nothing to restore", func() { s.DraftRestore(id(0)) })
	rs := s.Decide(t0+2*decisions.Delay, t0+2*decisions.Delay, s.DraftRestore(id(1)), r.env())
	if !rs.Queued() || rs.Note != "0 withheld restored at once, 1 forged, or written off, queued 7 days" || s.Lost != 2_000*gnot || s.SendOf(id(1)).WrittenOff == 0 {
		t.Fatalf("a write-off's restore waits: %+v lost %d", rs, s.Lost)
	}
	mustPanic(t, "is due at", func() { s.Execute(t0+2*decisions.Delay+Day, rs.ID, r.env()) })
	rs2 := s.Decide(t0+2*decisions.Delay, t0+2*decisions.Delay, s.DraftRestore(id(1)), r.env())
	s.Execute(t0+3*decisions.Delay, rs.ID, r.env())
	if s.Lost != 0 || s.SendOf(id(1)).WrittenOff != 0 || s.SendOf(id(1)).Refunded != t0+decisions.Delay || rs.Note != "applied after the delay; 1 restored (1 write-off undone: the send is lost no more)" || s.Lock != 1_000*gnot || s.SentTotal != 1_000*gnot {
		t.Fatalf("the write-off undone: lost %d note %q", s.Lost, rs.Note)
	}
	identity(t, s, 1_000*gnot)
	if !strings.HasSuffix(s.SendJSON(id(1)), ",\"refunded\":"+itoa(t0+decisions.Delay)+",\"writtenOff\":0}") || !strings.Contains(s.JSON(t0, 0, 0, 0), "\"refunded\":1,\"refundedTotal\":2000000000,\"writtenOff\":0,\"writtenOffTotal\":0,") || strings.Contains(s.Figures(t0, 0, 0, 0, ""), "written off") {
		t.Fatalf("the mark after the restore: %s\n%s", s.SendJSON(id(1)), s.JSON(t0, 0, 0, 0))
	}
	s.Execute(t0+3*decisions.Delay, rs2.ID, r.env())
	if s.Lost != 0 || rs2.Note != "applied after the delay; 0 restored, 1 skipped (not voided as it stands)" {
		t.Fatalf("a second restore: lost %d note %q", s.Lost, rs2.Note)
	}
	// written off again (the delivery confirmed this time, say), then migrated on: the mark goes with the record
	w3 := s.Decide(t0+3*decisions.Delay, t0+3*decisions.Delay, s.DraftWriteOff(id(1)), r.env())
	s.Execute(t0+4*decisions.Delay, w3.ID, r.env())
	if s.Lost != 2_000*gnot || s.SendOf(id(1)).WrittenOff != t0+4*decisions.Delay {
		t.Fatalf("written off again: lost %d", s.Lost)
	}
	identity(t, s, 3_000*gnot)
	r.announced["gno.land/r/x/hlgnot2"] = "GNOT"
	m := s.Decide(t0+4*decisions.Delay, t0+4*decisions.Delay, s.DraftMigrate("gno.land/r/x/hlgnot2", "gno.land/r/x/hlgnot", r.env()), r.env())
	s.Execute(t0+5*decisions.Delay, m.ID, r.env())
	p := s.Export(0, PullPage)
	if len(p.Sends) != 2 || p.Sends[1].WrittenOff != t0+4*decisions.Delay || p.Lost != 2_000*gnot {
		t.Fatalf("the page: %+v", p)
	}
	s2 := newState()
	s2.Import(t0+5*decisions.Delay, 0, 0, p)
	if s2.SendOf(id(1)).WrittenOff != t0+4*decisions.Delay || s2.Lost != 2_000*gnot || !strings.Contains(s2.JSON(t0, 0, 0, 0), "\"writtenOff\":1,\"writtenOffTotal\":2000000000,") || !strings.Contains(s.JSON(t0, 0, 0, 0), "\"writtenOff\":0,\"writtenOffTotal\":0,") {
		t.Fatalf("the mark after the migration: %s\n%s", s2.SendJSON(id(1)), s2.JSON(t0, 0, 0, 0))
	}
	mustPanic(t, id(1)+" is written off already", func() { s2.DraftWriteOff(id(1)) })
	identity(t, s2, 3_000*gnot)
	// and the restore reaches the mark on the next route too
	r2 := newRecorder()
	rs3 := s2.Decide(t0+5*decisions.Delay, t0+5*decisions.Delay, s2.DraftRestore(id(1)), r2.env())
	s2.Execute(t0+6*decisions.Delay, rs3.ID, r2.env())
	if s2.Lost != 0 || s2.SendOf(id(1)).WrittenOff != 0 || !strings.Contains(s2.JSON(t0, 0, 0, 0), "\"writtenOff\":0,\"writtenOffTotal\":0,") {
		t.Fatalf("the restore on the next route: lost %d", s2.Lost)
	}
	identity(t, s2, 1_000*gnot)
}

// TestApplyRefund (AT2): a refund's apply is GovDAO's own at-once act, never anyone's Execute. The realm cannot see
// Solana — hlwatch reads the token's enrolled router there and the kind 6's state — and on the note alone a
// refunded sender could call Execute the day his refund matured while a kind 6 deferred by one flag left the
// predecessor the router, then deliver his send there: a double mint, bounded by nothing the realm holds. So
// Execute refuses the kind with the coins untouched, and the apply is a builder of its own — at once, like an
// unpause — naming a queued refund decision whose 7 days have passed: refused for a decision that is no refund, one
// not pending, one not yet due, and until the enrolment note is 14 days old (asked when the proposal is made and
// again when it executes, the proposal surviving a refusal); the description names the refund decision and its
// ids; the apply is the delay's own (the lock's bound, a send refunded since skipped), noted on both decisions.
func TestApplyRefund(t *testing.T) {
	s := newState()
	r := newRecorder()
	inherit(s, SendItem{ID: id(0), From: alice, Amount: 1_000 * gnot, At: t0}, SendItem{ID: id(1), From: bob, Amount: 2_000 * gnot, At: t0})
	d := s.Decide(t0, t0, s.DraftRefund(id(1), t0, r.env()), r.env())
	if !d.Queued() || d.Note != "queued 7 days: applied by GovDAO's own apply refund decision after, never by Execute" {
		t.Fatalf("a refund waits for GovDAO's apply: %+v", d)
	}
	// Execute never applies a refund: refused before and after the delay, the coins untouched, the decision pending
	never := "decision " + itoa(d.ID) + " is a refund: a refund is applied by GovDAO's own at-once decision (apply refund) after hlwatch's Solana check, never by Execute"
	mustPanic(t, never, func() { s.Execute(t0+Day, d.ID, r.env()) })
	mustPanic(t, never, func() { s.Execute(t0+decisions.Delay, d.ID, r.env()) })
	if !d.Pending() || r.paid[string(bob)] != 0 || s.Lock != 3_000*gnot || s.SentTotal != 3_000*gnot || s.SendOf(id(1)).Refunded != 0 {
		t.Fatalf("Execute applied a refund: bob %d lock %d", r.paid[string(bob)], s.Lock)
	}
	identity(t, s, 3_000*gnot)
	// the apply builder names a queued refund decision whose 7 days have passed, and nothing else
	mustPanic(t, "no decision 9", func() { s.DraftApplyRefund(9, t0+decisions.Delay, r.env()) })
	cf := s.Decide(t0, t0, s.DraftCapFloor(12_000*gnot), r.env())
	mustPanic(t, "decision "+itoa(cf.ID)+" is of kind set cap floor, not refund: an apply refund names a queued refund decision", func() {
		s.DraftApplyRefund(cf.ID, t0+decisions.Delay, r.env())
	})
	mustPanic(t, "refund decision "+itoa(d.ID)+" is queued until "+itoa(t0+decisions.Delay)+" (1 s more): its 7 days have not passed", func() {
		s.DraftApplyRefund(d.ID, t0+decisions.Delay-1, r.env())
	})
	// the enrolment note's 14 days are asked of the apply too: a later kind 6, noted a day after the refund was
	// recorded, restarts the wait, and the apply is refused until it ends
	r.enrolledH, r.enrolledAt = 900, t0+Day
	mustPanic(t, "kind 6 enrolling this route was dispatched at height 900", func() { s.DraftApplyRefund(d.ID, t0+decisions.Delay, r.env()) })
	mustPanic(t, "a refund waits 14 days after it (until "+itoa(t0+Day+EnrolmentWait)+")", func() { s.DraftApplyRefund(d.ID, t0+Day+EnrolmentWait-1, r.env()) })
	at := t0 + Day + EnrolmentWait
	a := s.DraftApplyRefund(d.ID, at, r.env())
	if a.Kind != KindApplyRefund || a.Text != "apply refund decision "+itoa(d.ID)+" at once (refund 2000 GNOT of sends imported from the predecessor (14 days after kind 6's dispatch enrolled this realm) to their senders: "+id(1)+"), after hlwatch's check that the token's router on Solana is this realm" {
		t.Fatalf("the apply's draft: %+v", a)
	}
	// the closure asks again: a note moved in between refuses it — a panic, so the proposal survives — and nothing
	// is paid, marked or recorded
	r.enrolledAt = at - 1
	mustPanic(t, "a refund waits 14 days after it", func() { s.Decide(at, at, a, r.env()) })
	if !d.Pending() || r.paid[string(bob)] != 0 || s.Ledger.Last() != cf.ID {
		t.Fatalf("a refused apply moved something: bob %d, decisions %d", r.paid[string(bob)], s.Ledger.Last())
	}
	r.enrolledAt = at - EnrolmentWait
	dec := s.Decide(at, at, a, r.env())
	if dec.Queued() || dec.Kind != KindApplyRefund || dec.Note != "at once: refund decision "+itoa(d.ID)+" applied; 1 refunded" || d.State() != "applied" || d.Note != "applied by GovDAO's apply refund decision "+itoa(dec.ID)+"; 1 refunded" {
		t.Fatalf("the apply: %+v\nthe refund: %+v", dec, d)
	}
	if r.paid[string(bob)] != 2_000*gnot || s.Lock != 1_000*gnot || s.SentTotal != 1_000*gnot || s.SendOf(id(1)).Refunded != at {
		t.Fatalf("after the apply: bob %d lock %d sent %d", r.paid[string(bob)], s.Lock, s.SentTotal)
	}
	identity(t, s, 1_000*gnot)
	if !strings.Contains(s.Ledger.Page(), "### apply refund\n\npending: none\n\nexecuted:\n- #"+itoa(dec.ID)+" applied: apply refund decision "+itoa(d.ID)+" at once (") || !strings.Contains(s.Ledger.JSON(), "\"kind\":\"apply refund\"") {
		t.Fatalf("the page:\n%s", s.Ledger.Page())
	}
	// applied, or cancelled: not pending, so no apply; an apply drafted while the refund stood is refused when it
	// executes if a cancel landed in between, nothing paid
	mustPanic(t, "refund decision "+itoa(d.ID)+" is applied, not pending", func() { s.DraftApplyRefund(d.ID, at, r.env()) })
	d2 := s.Decide(at, at, s.DraftRefund(id(0), at, r.env()), r.env())
	s.Decide(at, at, s.DraftCancel(d2.ID), r.env())
	mustPanic(t, "refund decision "+itoa(d2.ID)+" is cancelled, not pending", func() { s.DraftApplyRefund(d2.ID, at+decisions.Delay, r.env()) })
	d3 := s.Decide(at, at, s.DraftRefund(id(0), at, r.env()), r.env())
	a3 := s.DraftApplyRefund(d3.ID, at+decisions.Delay, r.env())
	s.Decide(at+decisions.Delay, at+decisions.Delay, s.DraftCancel(d3.ID), r.env())
	mustPanic(t, "refund decision "+itoa(d3.ID)+" is cancelled, not pending", func() { s.Decide(at+decisions.Delay, at+decisions.Delay, a3, r.env()) })
	if r.paid[string(alice)] != 0 || s.Lock != 1_000*gnot || s.SendOf(id(0)).Refunded != 0 {
		t.Fatalf("a cancelled refund was applied: alice %d", r.paid[string(alice)])
	}
	identity(t, s, 1_000*gnot)
}

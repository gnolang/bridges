package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The tests of audit round 3's fixes (bridge/AUDIT.md: the Go findings G22–G27, the red team's AP1–AP3, AP5,
// AP6, the assumptions audit's F14 and F15 where the Go side carries them).

// G22: a scan that folded every listed transaction at or below the snapshot's slot stands at the snapshot, so a
// client counter above the rebuilt record is an unfillable gap, not "behind" for good; a scan that errored
// midway keeps the last folded slot and stays behind.
func TestRecordCompleteScanStandsAtTheSnapshot(t *testing.T) {
	client := sol.LightClient{Program: solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")}
	mailbox := solana.MustPublicKeyFromBase58("9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr")
	f := &fakeSol{txs: map[string]*rpc.GetTransactionResult{}}
	for _, x := range []struct {
		slot   uint64
		height uint64
	}{{10, 100}, {12, 101}} {
		res, sig := txResult(t, x.slot, func(payer solana.PublicKey) []solana.Instruction {
			return []solana.Instruction{client.InitIx(payer, payer, "dev", 1, x.height, [32]byte{byte(x.height)}, nil, payer)}
		})
		f.txs[sig.String()] = res
		f.sigs = append([]*rpc.TransactionSignature{{Signature: sig, Slot: x.slot}}, f.sigs...)
	}
	var r ClientRecord
	ctx := context.Background()
	if err := r.Sync(ctx, f, client, mailbox, 50); err != nil {
		t.Fatal(err)
	}
	if r.Count != 2 || r.ScanSlot != 50 {
		t.Fatalf("folded %d entries, the scan at slot %d: a complete scan stands at the snapshot (50)", r.Count, r.ScanSlot)
	}
	var running [32]byte
	b, _ := hex.DecodeString(r.Hash)
	copy(running[:], b)
	if ok, why := r.Consistent(2, running, 50); !ok {
		t.Fatalf("not consistent at the snapshot: %s", why)
	}
	// the client counts one header the scan never saw: the gap rule fires now (it read "behind" for good before)
	if ok, why := r.Consistent(3, running, 50); ok || !strings.HasPrefix(why, "an unfillable gap") {
		t.Fatalf("a counter above a complete scan: %v %q", ok, why)
	}
	// a scan that errors midway stays behind: the second transaction cannot be read
	g := &fakeSol{txs: map[string]*rpc.GetTransactionResult{}, sigs: f.sigs}
	for sig, res := range f.txs {
		if res.Slot == 10 {
			g.txs[sig] = res
		}
	}
	var r2 ClientRecord
	if err := r2.Sync(ctx, g, client, mailbox, 50); err == nil {
		t.Fatal("a scan missing a transaction passed")
	}
	if r2.Count != 1 || r2.ScanSlot != 10 {
		t.Fatalf("an errored scan: %d entries at slot %d, want 1 at 10", r2.Count, r2.ScanSlot)
	}
	b, _ = hex.DecodeString(r2.Hash)
	copy(running[:], b)
	if ok, why := r2.Consistent(2, running, 50); ok || !strings.HasPrefix(why, "behind:") {
		t.Fatalf("an errored scan is not behind: %v %q", ok, why)
	}
	// nothing listed above the cursor: the scan still stands at the newer snapshot
	if err := r.Sync(ctx, f, client, mailbox, 60); err != nil || r.ScanSlot != 60 || r.Count != 2 {
		t.Fatalf("an empty scan: %v, slot %d, %d entries", err, r.ScanSlot, r.Count)
	}
}

// G24: the signer's status is read beside the loop that writes the index and the unsigned list (an operator's
// scraper on the loopback endpoint); under -race the status must touch nothing the loop writes — it serves the
// counts the loop publishes under the mutex, which match the index once the loop has published.
func TestSignerStatusUnderTheLoop(t *testing.T) {
	k, _ := hl.NewValidatorKey()
	cfg := &Config{}
	cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	s := &Signer{Cfg: cfg, Key: k, Store: t.TempDir(), Every: time.Second, Alarm: &Alarmer{Source: "test"}}
	if err := s.load(); err != nil {
		t.Fatal(err)
	}
	verdictNow(s)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			st := s.Status()
			if st["indexed"].(int) < st["unsigned"].(int) {
				panic("more unsigned than indexed")
			}
		}
	}()
	ctx := context.Background()
	for i := uint32(1); i <= 300; i++ {
		d := dispatchFor(t, i, 10, 5, "sig"+strconv.Itoa(int(i)))
		s.record(d, "BUBBLE", 0)
		if i%3 == 0 {
			lie := d
			lie.Slot = 11
			s.record(lie, "BUBBLE", 1) // disputed: stays unsigned
		} else {
			s.record(d, "BUBBLE", 1)
		}
		s.publishCounts()
		if i%10 == 0 {
			if err := s.signSol(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	<-done
	_ = s.signSol(ctx)
	st := s.Status()
	if st["indexed"] != len(s.index) || st["unsigned"] != len(s.unsigned) || st["disputed"] != 100 {
		t.Fatalf("the published counts: %v %v %v, the index %d, unsigned %d", st["indexed"], st["unsigned"], st["disputed"], len(s.index), len(s.unsigned))
	}
}

// G27: the Execute and extend bookkeeping of a decision no longer pending — executed by the other relayer, by
// hlrecover execute, or closed by a cancel — is forgotten with its alarms; a pending decision's stays.
func TestSolExecutesForgetsGoneDecisions(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	now := r.now()
	for _, dec := range []string{"5", "6"} {
		r.state.Ready["solexec:"+dec] = now
		r.state.Attempts["solexec:"+dec] = &attempt{Failures: 3, Parked: now, LastErr: "x"}
		r.Alarm.Alarm("parked:solexec:"+dec, "parked")
		r.state.Extends["solext:"+dec] = "25000"
		r.state.Attempts["solext:"+dec] = &attempt{Failures: 1}
		r.Alarm.Alarm("execute:extend:"+dec, "one ExtendProgram")
	}
	r.state.Refused["solexec:5"] = "CancelSpent"
	r.Alarm.Alarm("execute:refused:solexec:5", "refused for good")
	r.forgetGone([]*sol.Decision{{Number: 6}})
	for _, key := range []string{"solexec:5", "solext:5"} {
		if _, ok := r.state.Ready[key]; ok {
			t.Fatalf("%s's clock survived the decision", key)
		}
		if _, ok := r.state.Attempts[key]; ok {
			t.Fatalf("%s's attempts survived the decision", key)
		}
	}
	if _, ok := r.state.Extends["solext:5"]; ok {
		t.Fatal("the extend plan survived the decision")
	}
	if _, ok := r.state.Refused["solexec:5"]; ok {
		t.Fatal("the refusal survived the decision")
	}
	for _, key := range []string{"parked:solexec:5", "parked:solext:5", "execute:extend:5", "execute:refused:solexec:5"} {
		if _, ok := r.Alarm.Standing()[key]; ok {
			t.Fatalf("the alarm %s survived the decision", key)
		}
	}
	for _, key := range []string{"parked:solexec:6", "execute:extend:6"} {
		if _, ok := r.Alarm.Standing()[key]; !ok {
			t.Fatalf("the pending decision's alarm %s was dropped", key)
		}
	}
	if r.state.Extends["solext:6"] != "25000" || r.state.Attempts["solexec:6"] == nil {
		t.Fatal("the pending decision's bookkeeping was dropped")
	}
}

// G27: a parked action's hourly retry re-raises the parked alarm on each failure (the Alarmer posts it to the
// webhook once an hour while it stands), so a parked message is not heard once and forgotten.
func TestParkedAlarmRepeats(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	r.Alarm.Repeat = time.Nanosecond
	f.fail[testRoute+".Claim"] = deterministic("refused for good")
	ctx := context.Background()
	key := "claim:BUBBLE"
	for i := 0; i < 3; i++ {
		_, _ = r.tryCall(ctx, key, testRoute, "Claim", 1, "20")
		advance(r, time.Hour)
	}
	a := r.state.Attempts[key]
	if !a.parked() {
		t.Fatalf("not parked after three refusals: %+v", a)
	}
	r.Alarm.mu.Lock()
	first := r.Alarm.standing["parked:"+key]
	r.Alarm.mu.Unlock()
	if first.IsZero() {
		t.Fatal("no parked alarm at the parking")
	}
	time.Sleep(time.Millisecond)
	advance(r, time.Hour)
	if _, err := r.tryCall(ctx, key, testRoute, "Claim", 1, "20"); err == nil || errors.Is(err, errNotYet) {
		t.Fatalf("the hourly retry: %v", err)
	}
	r.Alarm.mu.Lock()
	again := r.Alarm.standing["parked:"+key]
	r.Alarm.mu.Unlock()
	if !again.After(first) {
		t.Fatal("the hourly retry's failure did not re-raise the parked alarm")
	}
	// a success clears it
	delete(f.fail, testRoute+".Claim")
	advance(r, time.Hour)
	if _, err := r.tryCall(ctx, key, testRoute, "Claim", 1, "20"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Alarm.Standing()["parked:"+key]; ok {
		t.Fatal("the parked alarm stands after a success")
	}
}

// G27: a persisted condition's clear drops its first sighting too, so the next failure starts a fresh spell
// instead of alarming at once; a disputed record is no message waiting for the stall and age alarms.
func TestMonitorClearsSightingsAndSkipsDisputed(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	if m.persisted("cut", cutFailAfter) {
		t.Fatal("persisted at first sight")
	}
	m.clearPersisted("cut")
	now := m.now().Add(cutFailAfter)
	m.now = func() time.Time { return now }
	if m.persisted("cut", cutFailAfter) {
		t.Fatal("a cleared condition counted its old sighting")
	}
	now = now.Add(cutFailAfter)
	if !m.persisted("cut", cutFailAfter) {
		t.Fatal("a standing condition did not persist")
	}
	m.state.LastDelivery = m.now().Unix()
	m.state.Index["aa"] = &dispatchRec{Symbol: "BUBBLE", Seen: m.now().Add(-3 * time.Hour).Unix(), Disputed: "provider 2 shows slot 1 against 2"}
	m.stalls()
	if _, ok := m.Alarm.Standing()["age"]; ok {
		t.Fatal("a disputed record raised the age alarm")
	}
	if m.status.Waiting["solana→gno BUBBLE"] != 0 {
		t.Fatal("a disputed record counted as waiting")
	}
	m.state.Index["bb"] = &dispatchRec{Symbol: "BUBBLE", Seen: m.now().Add(-3 * time.Hour).Unix()}
	m.stalls()
	if _, ok := m.Alarm.Standing()["age"]; !ok || m.status.Waiting["solana→gno BUBBLE"] != 1 {
		t.Fatalf("an undisputed old record did not raise the age alarm: %v %v", m.Alarm.StandingKeys(), m.status.Waiting)
	}
}

// AP1: a waiting claim the verifier credited whose id neither provider's dispatch index holds flags credits and
// payouts on its route once it has stood one more poll; a voted claim, an indexed one, and any claim while a
// provider's scan has transactions skipped are left alone; an id that turns up in the index clears the alarm.
func TestMonitorFlagsAnUnindexedCredit(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	ctx := context.Background()
	mb := mailboxWith()
	m.seat = 5
	claim := func(id, by string) string {
		return `{"id":"` + id + `","to":"g1eve","amount":500,"paid":0,"state":"open","by":"` + by + `","at":1,"height":90,"seq":1}`
	}
	f.renders[testRoute+":claims/0"] = "[" + claim("ee", "signers") + "," + claim("vv", "vote") + "," + claim("aa", "signers") + "]"
	m.state.Index["aa"] = &dispatchRec{Symbol: "BUBBLE", Slot: 10, To: "g1eve", Amount: 500}
	if err := m.unindexedCredits(ctx, mb, 100); err != nil {
		t.Fatal(err)
	}
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 0 {
		t.Fatalf("flagged at first sight: %d", n)
	}
	now := m.now().Add(m.Every)
	m.now = func() time.Time { return now }
	if err := m.unindexedCredits(ctx, mb, 101); err != nil {
		t.Fatal(err)
	}
	calls := f.callsOf(testMailbox + ".SetFlag")
	want := []string{testMailbox + ".SetFlag(" + testRoute + ",credits,)", testMailbox + ".SetFlag(" + testRoute + ",payouts,)"}
	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("flags set: %v, want %v (the unindexed claim ee alone)", calls, want)
	}
	if a, ok := m.Alarm.Standing()["credit:unindexed:ee:flag"]; !ok || !strings.Contains(a, "forged credit ee") {
		t.Fatalf("no flag alarm for the unindexed credit: %v", m.Alarm.Standing())
	}
	// the id turns up in the index (a provider's lag): the alarm and the sighting go
	m.state.Index["ee"] = &dispatchRec{Symbol: "BUBBLE", Slot: 11, To: "g1eve", Amount: 500}
	if err := m.unindexedCredits(ctx, mb, 102); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Alarm.Standing()["credit:unindexed:ee:flag"]; ok {
		t.Fatal("the alarm stands after the id was indexed")
	}
	if _, ok := m.seen["credit:unindexed:ee"]; ok {
		t.Fatal("the sighting stands after the id was indexed")
	}
	// a skipped transaction on a provider: no verdict on any claim
	delete(m.state.Index, "ee")
	m.state.Skipped["BUBBLE#1"] = []scanSkip{{Sig: "x", Err: "unreadable"}}
	f.calls = nil
	for i := 0; i < 3; i++ {
		now = now.Add(m.Every)
		_ = m.unindexedCredits(ctx, mb, 103)
	}
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 0 {
		t.Fatalf("flagged while a scan had transactions skipped: %d", n)
	}
}

// AP2: while a token's oldest OverDayCap park waits, a younger message of the token that fits the bucket alone
// is held when the park's amount beside it would not; an older message, the other token and the park itself are
// gated as before; once the bucket drains past both, the park goes first and the younger follows.
func TestDayCapReservation(t *testing.T) {
	rt := &Route{Symbol: "BUBBLE"}
	msg := func(amount uint64) *gnoMsg {
		return &gnoMsg{Msg: hl.Message{Body: hl.TokenMessage([32]byte{1}, amount, nil)}}
	}
	older, parked, younger := todo{n: 3, rt: rt, msg: msg(500)}, todo{n: 5, rt: rt, msg: msg(25_000)}, todo{n: 8, rt: rt, msg: msg(90)}
	gnot := todo{n: 9, rt: &Route{Symbol: "GNOT"}, msg: msg(90)}
	attempts := map[string]*attempt{"deliver:5": {Parked: time.Unix(1, 0), LastErr: "pausable ISM: OverDayCap: send: …"}}
	parks := capParks([]todo{older, parked, younger, gnot}, attempts)
	if parks[1] == nil || parks[1].n != 5 || parks[1].amount != 25_000 || parks[0] != nil {
		t.Fatalf("the parks: %+v %+v", parks[0], parks[1])
	}
	l := sol.MintLimit{MaxPerMessage: 25_000, DayCap: 100_000, Used: 100_000, UsedAt: 1_000}
	day := int64(86_400)
	now := int64(1_000 + day/10) // a tenth drained: room for 10,000
	if ok, why := capGate(younger.n, 90, false, parks[1], l, now, day, 0); ok || !strings.Contains(why, "held under the day cap behind gno #5's parked 25000") {
		t.Fatalf("a younger send took the park's room: %v %s", ok, why)
	}
	if ok, _ := capGate(older.n, 500, false, parks[1], l, now, day, 0); !ok {
		t.Fatal("an older send was held behind a younger park")
	}
	if ok, why := capGate(parked.n, 25_000, true, parks[1], l, now, day, 0); ok || !strings.Contains(why, "parked (OverDayCap), retried once the day's bucket has room: the day's bucket holds 90000 of 100000, 25000 asked") {
		t.Fatalf("the park went without room: %v %s", ok, why)
	}
	if ok, _ := capGate(gnot.n, 90, false, parks[0], l, now, day, 0); !ok {
		t.Fatal("the other token was held")
	}
	// a younger message parked itself waits behind the older park too
	if ok, why := capGate(younger.n, 90, true, parks[1], l, now, day, 0); ok || !strings.Contains(why, "behind gno #5's parked 25000 (oldest first, its amount reserved)") {
		t.Fatalf("a younger park took the older park's room: %v %s", ok, why)
	}
	// the bucket drained past both: the park goes, then the younger, what the pass delivered counted
	now = 1_000 + day/4 // room for 25,000
	if ok, _ := capGate(parked.n, 25_000, true, parks[1], l, now, day, 0); !ok {
		t.Fatal("the park did not go once the bucket had room")
	}
	if ok, _ := capGate(younger.n, 90, false, parks[1], l, now, day, 0); ok {
		t.Fatal("the younger send went in the same pass when the park's amount left no room for it")
	}
	now = 1_000 + day/4 + 1_000 // a little more drained: both fit
	// the park delivered: the walk counts it in the pass and drops the reservation (else its amount counted twice)
	if ok, _ := capGate(younger.n, 90, false, nil, l, now, day, 25_000); !ok {
		t.Fatal("the younger send was held after the park was delivered and counted")
	}
	if ok, _ := capGate(younger.n, 90, false, parks[1], l, now, day, 25_000); ok {
		t.Fatal("a reservation kept beside the delivered park's count let the younger send through (the walk must drop it)")
	}
	if ok, _ := capGate(younger.n, 90, false, nil, l, now, day, 0); !ok {
		t.Fatal("a message with no park of its token was gated")
	}
}

// AP3: a delivery the admin program answers Retired is dead there — done as "retired", never parked, alarmed
// once, left out of the Reemit rule; NotInherited and the other refusals keep §5.7's bookkeeping toward parking.
func TestRetiredIsNotParked(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	custom := func(code int) error {
		return errors.New("send: transaction failed: InstructionError(1, Custom(" + strconv.Itoa(code) + "))")
	}
	t4 := todo{n: 4, kind: kindDecision}
	r.state.Ready["deliver:4"] = r.now()
	if err := r.deliverFailed(t4, "deliver:4", "4", custom(3025)); err != nil {
		t.Fatalf("a Retired delivery came back as a failure: %v", err)
	}
	if r.state.Done["4"] != "retired" || r.state.Attempts["deliver:4"] != nil {
		t.Fatalf("not retired: done %q, attempts %+v", r.state.Done["4"], r.state.Attempts["deliver:4"])
	}
	if _, ok := r.state.Ready["deliver:4"]; ok {
		t.Fatal("the backup's clock survived")
	}
	if a, ok := r.Alarm.Standing()["decision:retired:4"]; !ok || !strings.Contains(a, "re-dispatch") {
		t.Fatalf("no alarm naming the re-dispatch: %v", r.Alarm.Standing())
	}
	if !doneForReemit("retired") || doneForReemit("taken") || doneForReemit("") {
		t.Fatal("the Reemit rule's skip set")
	}
	// NotInherited: a deterministic refusal that may pass once the kind 9 lands — the attempt bookkeeping
	t5 := todo{n: 5, kind: kindDecision}
	if err := r.deliverFailed(t5, "deliver:5", "5", custom(3027)); err == nil || !strings.Contains(err.Error(), "NotInherited") {
		t.Fatalf("NotInherited: %v", err)
	}
	if a := r.state.Attempts["deliver:5"]; a == nil || a.Failures != 1 || r.state.Done["5"] != "" {
		t.Fatalf("NotInherited did not take the attempt path: %+v %q", a, r.state.Done["5"])
	}
}

// AP5: hlrecover execute's growth is planned before it is paid — the size and the rent in a line — and refused
// above -max-rent without -yes; a buffer the program data already holds is no growth.
func TestExtendPlan(t *testing.T) {
	program := solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")
	terms := ExtendTerms{MaxRent: 500_000_000}
	add, line, err := extendPlan(program, 100_000, 125_000, 127_000_000, terms)
	if err != nil || add != 25_000 || !strings.Contains(line, "one ExtendProgram of 25000 bytes costs ≈0.1270 SOL of rent") {
		t.Fatalf("a growth under the bound: %d %q %v", add, line, err)
	}
	_, _, err = extendPlan(program, 272_000, 790_000, 2_600_000_000, terms)
	if err == nil || !strings.Contains(err.Error(), "518000 bytes costs ≈2.6000 SOL") || !strings.Contains(err.Error(), "above -max-rent (0.5000 SOL); run again with -yes") {
		t.Fatalf("a growth above the bound was not refused: %v", err)
	}
	terms.Confirmed = true
	if add, _, err = extendPlan(program, 272_000, 790_000, 2_600_000_000, terms); err != nil || add != 518_000 {
		t.Fatalf("-yes did not allow the growth: %d %v", add, err)
	}
	if _, _, err = extendPlan(program, 125_000, 125_000, 0, terms); err == nil || !strings.Contains(err.Error(), "holds the buffer's") {
		t.Fatalf("no growth needed: %v", err)
	}
}

// AP6: a token's day cap on the pausable ISM above a fifth of its lock on gno.land alarms mint:cap-ratio (the
// launch week's lock, an exit wave's), cleared once the lock covers it; a route without a token is left alone.
func TestCapRatio(t *testing.T) {
	if s := capRatioText("BUBBLE", 100_000, 500_000); s != "" {
		t.Fatalf("a cap at a fifth alarmed: %s", s)
	}
	if s := capRatioText("BUBBLE", 100_001, 500_000); !strings.Contains(s, "BUBBLE's day cap on the pausable ISM, 100001, is above a fifth of its lock on gno.land, 500000") {
		t.Fatalf("a cap over a fifth did not alarm: %q", s)
	}
	if s := capRatioText("GNOT", 1, 0); s == "" {
		t.Fatal("a cap over an empty lock did not alarm")
	}
	f := newFake()
	m := testMonitor(f)
	m.Cfg.Routes = append(m.Cfg.Routes, Route{Symbol: "GNOT", Gno: "gno.land/r/test/hlgnot"}) // no token: no rule
	ps := &sol.PausableState{Limits: [2]sol.MintLimit{{DayCap: 100_000}, {DayCap: 100_000}}}
	routes := map[string]*routeJSON{"BUBBLE": {Lock: 20_000}, "GNOT": {Lock: 1}}
	m.capRatios(ps, routes, nil)
	if a, ok := m.Alarm.Standing()["mint:cap-ratio:BUBBLE"]; !ok || !strings.Contains(a, "100000") {
		t.Fatalf("no cap-ratio alarm at a lock of 20,000: %v", m.Alarm.Standing())
	}
	if _, ok := m.Alarm.Standing()["mint:cap-ratio:GNOT"]; ok {
		t.Fatal("a route with no token alarmed")
	}
	routes["BUBBLE"].Lock = 500_000
	m.capRatios(ps, routes, nil)
	if _, ok := m.Alarm.Standing()["mint:cap-ratio:BUBBLE"]; ok {
		t.Fatal("not cleared once the lock covered the cap")
	}
}

// headErrFake renders at a pinned height and errors at the head (a closed keep-alive connection, a timeout).
type headErrFake struct {
	*fakeChain
	headErr map[string]error
}

func (h headErrFake) RenderAt(ctx context.Context, pkg, path string, height int64) (string, error) {
	if height == 0 {
		if err, ok := h.headErr[pkg+":"+path]; ok {
			return "", err
		}
	}
	return h.fakeChain.RenderAt(ctx, pkg, path, height)
}

// G23: a head re-read that errors is no reading — no finding, the number stays unchecked, the error comes back;
// the monitor's foreign flag stands only once a finding has stood the cut's two minutes, an alarm before, and an
// erroring poll raises nothing and advances nothing.
func TestForeignRereadErrorIsNoFinding(t *testing.T) {
	f := newFake()
	cfg := &Config{}
	cfg.Gno.Gov = "gno.land/r/test/hlgov"
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute}}
	f.renders[cfg.Gno.Gov+":decision/3"] = "null"
	hf := headErrFake{fakeChain: f, headErr: map[string]error{cfg.Gno.Gov + ":decision/3": errors.New(`Post "http://127.0.0.1:26657": EOF`)}}
	findings, checked, err := foreignFindings(context.Background(), nil, hf, cfg, sol.Admin{}, nil, 100, 2, 3, 2, 0, foreignCtx{})
	if err == nil || len(findings) != 0 || checked != 2 {
		t.Fatalf("an erroring head re-read made a finding, or moved the cursor: %v %d %v", findings, checked, err)
	}
	// the monitor on that poll: no alarm, no flag, nothing advanced
	m := testMonitor(f)
	ctx := context.Background()
	m.state.DecisionsChecked = 2
	m.applyForeign(ctx, findings, checked, err)
	if len(m.Alarm.StandingKeys()) != 0 || m.state.DecisionsChecked != 2 {
		t.Fatalf("an erroring poll raised %v or advanced to %d", m.Alarm.StandingKeys(), m.state.DecisionsChecked)
	}
	// a real finding: an alarm first, the flag once it has stood the cut's patience, the cursor past it then
	finding := []string{"foreign decision: the admin program recorded number 3, which r/hlgov never dispatched"}
	m.applyForeign(ctx, finding, 3, nil)
	if a := m.Alarm.Standing()["decision:foreign:pending"]; !strings.Contains(a, "flagged once it has stood") {
		t.Fatalf("no pending alarm at first sight: %v", m.Alarm.Standing())
	}
	if _, ok := m.Alarm.Standing()["decision:foreign"]; ok || m.state.DecisionsChecked != 2 {
		t.Fatalf("flagged at first sight, or the cursor advanced: %v %d", m.Alarm.StandingKeys(), m.state.DecisionsChecked)
	}
	// an erroring poll in between keeps the sighting
	m.applyForeign(ctx, nil, 2, errors.New("EOF"))
	if _, ok := m.seen["decision:foreign"]; !ok {
		t.Fatal("an erroring poll dropped the sighting")
	}
	now := m.now().Add(cutFailAfter)
	m.now = func() time.Time { return now }
	m.applyForeign(ctx, finding, 3, nil)
	if a, ok := m.Alarm.Standing()["decision:foreign"]; !ok || !strings.Contains(a, "number 3") {
		t.Fatalf("not flagged after the persistence: %v", m.Alarm.Standing())
	}
	if _, ok := m.Alarm.Standing()["flag:solana"]; !ok {
		t.Fatal("no flag attempt (this machine holds no Solana watch key: it alarms)")
	}
	if _, ok := m.Alarm.Standing()["decision:foreign:pending"]; ok || m.state.DecisionsChecked != 3 {
		t.Fatalf("the pending alarm stands after the flag, or the cursor did not advance: %v %d", m.Alarm.StandingKeys(), m.state.DecisionsChecked)
	}
	// a clean poll with nothing new clears a pending sighting, never the flag raised
	m.applyForeign(ctx, finding, 4, nil)
	m.applyForeign(ctx, nil, 4, nil)
	if _, ok := m.Alarm.Standing()["decision:foreign:pending"]; ok {
		t.Fatal("a clean poll left the pending alarm")
	}
	if _, ok := m.seen["decision:foreign"]; ok {
		t.Fatal("a clean poll left the sighting")
	}
	if _, ok := m.Alarm.Standing()["decision:foreign"]; !ok {
		t.Fatal("the flag raised was cleared by a clean poll")
	}
}

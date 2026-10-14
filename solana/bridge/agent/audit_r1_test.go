package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The tests of audit round 1's fixes (bridge/AUDIT.md; the findings G1–G12, AN2, AN3, K2, K5, K6, F2).

// verdictNow gives a signer a fresh held-cut verdict, as checkIdentity does, with a snapshot slot past any
// dispatch these tests index (G13: a dispatch above the verdict's slot waits for the next cut).
func verdictNow(s *Signer) {
	verdictAt(s, 1<<40)
}

// verdictAt gives a signer a fresh verdict whose snapshot was taken at a slot.
func verdictAt(s *Signer, slot uint64) {
	if s.now == nil {
		s.now = time.Now
	}
	s.mu.Lock()
	s.verified = true
	s.lastVerdict = s.now()
	s.verdictSlot = slot
	s.mu.Unlock()
}

// AN2/G7: a signer signs on no verdict older than one round, and halts after an hour without one; a fresh verdict
// clears the halt.
func TestSignerVerdictAge(t *testing.T) {
	k, _ := hl.NewValidatorKey()
	now := time.Unix(1_800_000_000, 0)
	s := &Signer{Cfg: &Config{}, Key: k, Store: t.TempDir(), Every: 3 * time.Second, Alarm: &Alarmer{Source: "test"}, now: func() time.Time { return now }}
	_ = s.load()
	verdictNow(s)
	if !s.signing() {
		t.Fatal("not signing on a fresh verdict")
	}
	now = now.Add(5 * time.Second) // the next round: the previous round's verdict still serves
	if !s.signing() {
		t.Fatal("not signing on the previous round's verdict")
	}
	now = now.Add(5 * time.Second) // two rounds without a verdict: nothing to sign on
	if s.signing() {
		t.Fatal("signing on a verdict two rounds old")
	}
	if st := s.Status(); st["signing"] != false || st["verdictFresh"] != false || st["verified"] != true {
		t.Fatalf("the status does not say the verdict is stale: %v", st)
	}
	s.unverified("the providers disagree")
	if s.Status()["halted"] != "" {
		t.Fatal("halted before the hour")
	}
	now = now.Add(time.Hour)
	s.unverified("the providers disagree")
	if h := s.Status()["halted"].(string); !strings.Contains(h, "no verdict for an hour") {
		t.Fatalf("not halted after an hour without a verdict: %q", h)
	}
	if _, ok := s.Alarm.Standing()["signer:halt"]; !ok {
		t.Fatal("no halt alarm")
	}
	verdictNow(s)
	s.resume()
	if !s.signing() {
		t.Fatal("a fresh verdict did not clear the halt")
	}
}

// AN3: the signers' rule counts the burns dispatched at or before the snapshot and not yet credited as supply —
// an honest burn in flight holds, a forged mint burned back breaks it.
func TestSignerStopsOnUncreditedBurns(t *testing.T) {
	honest := Identity{Symbol: "BUBBLE", Lock: 1000, Supply: 800, InflightIn: 200} // 200 burned, not yet credited
	if honest.SignerStops() {
		t.Fatal("an honest burn in flight stopped the signer")
	}
	forged := Identity{Symbol: "BUBBLE", Lock: 1000, Supply: 1000, InflightIn: 50} // 50 minted from nothing, then burned toward gno.land
	if !forged.SignerStops() {
		t.Fatal("a forged mint burned back did not stop the signer")
	}
	// AP1: the monitors' and hlwatch's verdict is the signers' exact rule now (the round-1 reading, in flight only
	// shortening the right side there, let a forged credit hide inside what was in flight)
	if v, _ := forged.Verdict(); v != Broken {
		t.Fatal("the monitor's verdict is not the signers' rule: a forged mint burned back must break it")
	}
}

func dispatchFor(t *testing.T, nonce uint32, slot uint64, amount uint64, sig string) solDispatch {
	t.Helper()
	to, err := hl.GnoAddressToBytes32("g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5")
	if err != nil {
		t.Fatal(err)
	}
	m := hl.Message{Version: 3, Nonce: nonce, Origin: hl.DomainSolanaTestnet, Destination: hl.DomainGnoTest, Body: hl.TokenMessage(to, amount, nil)}
	d := solDispatch{Sig: sig, Slot: slot, Raw: hexBytes(m.Bytes())}
	if err := d.fill(); err != nil {
		t.Fatal(err)
	}
	return d
}

// G7: the index keeps the unsigned dispatches in memory in provider 1's order and signs each once both providers
// showed the same transaction with the same bytes; the signing cursor is the last signed; a dispatch provider 2
// has not shown yet stops the batch.
func TestSignerIndexAndSign(t *testing.T) {
	k, _ := hl.NewValidatorKey()
	cfg := &Config{}
	cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	s := &Signer{Cfg: cfg, Key: k, Store: t.TempDir(), Every: time.Second, Alarm: &Alarmer{Source: "test"}}
	if err := s.load(); err != nil {
		t.Fatal(err)
	}
	verdictNow(s)
	a := dispatchFor(t, 1, 10, 500, "3Ckv5ySpfK5KXdWwmYUjpR4FnRmfZ8E8tYXkSxcDQkC5iTX6B2aFCxNjxp7Q4nbQ1hRZcbjyW4mYnqN8f3NSn1zT")
	b := dispatchFor(t, 2, 11, 700, "4Ckv5ySpfK5KXdWwmYUjpR4FnRmfZ8E8tYXkSxcDQkC5iTX6B2aFCxNjxp7Q4nbQ1hRZcbjyW4mYnqN8f3NSn1zU")
	s.record(a, "BUBBLE", 0)
	s.record(b, "BUBBLE", 0)
	if len(s.unsigned) != 2 || len(s.index) != 2 {
		t.Fatalf("unsigned %d indexed %d", len(s.unsigned), len(s.index))
	}
	if err := s.signSol(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.unsigned) != 2 {
		t.Fatal("signed before provider 2 showed the dispatch")
	}
	s.record(b, "BUBBLE", 1) // provider 2 shows the second first: the batch still waits at the first
	if err := s.signSol(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.unsigned) != 2 {
		t.Fatal("signed out of order")
	}
	s.record(a, "BUBBLE", 1)
	if err := s.signSol(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.unsigned) != 0 {
		t.Fatalf("%d unsigned after both providers showed both", len(s.unsigned))
	}
	if c := s.state.SolCursors["BUBBLE#0"]; c != b.Sig {
		t.Fatalf("the signing cursor is %q, want the last signed", c)
	}
	for _, n := range []uint32{1, 2} {
		if _, err := s.readSigned("sol", n); err != nil {
			t.Fatalf("nonce %d not in the store: %v", n, err)
		}
	}
	// a second sight of a signed dispatch indexes it (for the cut) and queues nothing
	s.record(a, "BUBBLE", 0)
	if len(s.unsigned) != 0 {
		t.Fatal("a signed dispatch was queued again")
	}
	// a fresh signer from the same store knows what it signed and queues nothing for it
	again := &Signer{Cfg: cfg, Key: k, Store: s.Store, Every: time.Second, Alarm: &Alarmer{Source: "test"}}
	if err := again.load(); err != nil {
		t.Fatal(err)
	}
	again.record(a, "BUBBLE", 0)
	if len(again.unsigned) != 0 || !again.signed[a.IDHex()] {
		t.Fatal("the store's signatures were not read back")
	}
}

// G12: a signature file that is not a signature is corruption, named as such, never a nonce conflict; a
// zero-length state file is corruption too.
func TestCorruptionIsNamed(t *testing.T) {
	k, _ := hl.NewValidatorKey()
	s := &Signer{Cfg: &Config{}, Key: k, Store: t.TempDir(), Alarm: &Alarmer{Source: "test"}}
	if err := s.load(); err != nil {
		t.Fatal(err)
	}
	verdictNow(s)
	p := filepath.Join(s.Store, "sol", "7.json")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.put("sol", Signed{Nonce: 7, ID: "aa"})
	if err == nil || !errors.Is(err, errCorrupt) {
		t.Fatalf("an empty signature file was not named as corruption: %v", err)
	}
	if !s.signing() {
		t.Fatal("corruption was taken for a nonce conflict")
	}
	var v struct{ A int }
	st := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(st, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := readJSON(st, &v); err == nil || !errors.Is(err, errCorrupt) {
		t.Fatalf("a zero-length state file was not named as corruption: %v", err)
	}
	if err := writeJSON(st, map[string]int{"A": 3}); err != nil {
		t.Fatal(err)
	}
	if err := readJSON(st, &v); err != nil || v.A != 3 {
		t.Fatalf("round trip: %v %+v", err, v)
	}
	if _, err := os.Stat(st + ".tmp"); err == nil {
		t.Fatal("the temporary file was left behind")
	}
}

// G12: the same provider twice is refused unless the config says a local run.
func TestTwoProvidersRefusesTheSameURL(t *testing.T) {
	c := &Config{}
	c.Solana.RPCs = []string{"http://a", "http://a"}
	if _, err := c.TwoProviders(); err == nil {
		t.Fatal("the same provider twice was taken")
	}
	c.AllowHTTP = true
	if _, err := c.TwoProviders(); err != nil {
		t.Fatal(err)
	}
	c.AllowHTTP = false
	c.Solana.RPCs = []string{"http://a", "http://b"}
	if _, err := c.TwoProviders(); err != nil {
		t.Fatal(err)
	}
}

// G3: the monitor's own node gives no verdict while catching up, while its head is not after the snapshot, or
// while its head is stale.
func TestJudgeOwnHead(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	snap := now.Add(-10 * time.Second)
	fresh := gnoHead(100, now.Add(-3*time.Second))
	if ok, why := judgeOwnHead(fresh, snap, now); !ok {
		t.Fatal(why)
	}
	catching := fresh
	catching.CatchingUp = true
	if ok, why := judgeOwnHead(catching, snap, now); ok || !strings.Contains(why, "catching up") {
		t.Fatalf("a catching-up node gave a verdict: %v %s", ok, why)
	}
	if ok, why := judgeOwnHead(gnoHead(100, snap.Add(-time.Second)), snap, now); ok || !strings.Contains(why, "not after") {
		t.Fatalf("a head before the snapshot gave a verdict: %v %s", ok, why)
	}
	if ok, why := judgeOwnHead(gnoHead(100, now.Add(-6*time.Minute)), now.Add(-7*time.Minute), now); ok || !strings.Contains(why, "old") {
		t.Fatalf("a stale head gave a verdict: %v %s", ok, why)
	}
}

// G3: a condition persists for its allowance before it flags.
func TestMonitorPersisted(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	if m.persisted("x", cutFailAfter) {
		t.Fatal("persisted at first sight")
	}
	now := m.now().Add(cutFailAfter)
	m.now = func() time.Time { return now }
	if !m.persisted("x", cutFailAfter) {
		t.Fatal("not persisted after the allowance")
	}
}

// K2: a burn to no gno address is indexed without a recipient and its credit must be a sweep of the amount.
func TestMonitorBadRecipientExpectsTheSweep(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	ctx := context.Background()
	mb := mailboxWith()
	m.seat = 5
	m.state.Index["ee"] = &dispatchRec{Symbol: "BUBBLE", Slot: 10, To: "", Bad: true, Amount: 9, Credited: 90}
	m.state.Index["ff"] = &dispatchRec{Symbol: "BUBBLE", Slot: 11, To: "", Bad: true, Amount: 9, Credited: 91}
	f.renders[testRoute+":claim/ee"] = `{"id":"ee","swept":9}`
	f.renders[testRoute+":claim/ff"] = `{"id":"ff","to":"g1somebody","amount":9,"paid":0,"state":"open","by":"signers","at":1,"seq":1}`
	if err := m.divertedCredits(ctx, mb, 100); err != nil {
		t.Fatal(err)
	}
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 2 {
		t.Fatalf("flags: %d (one break expected, two flags)", n)
	}
	if _, ok := m.Alarm.Standing()["diverted:ff:flag"]; !ok {
		t.Fatal("the credit to an address nobody holds was not a break")
	}
	if _, ok := m.Alarm.Standing()["diverted:ee:flag"]; ok {
		t.Fatal("the sweep was a break")
	}
	// the dispatch reader: a raw Solana pubkey as the recipient is no gno address
	var raw [32]byte
	raw[0] = 7
	msg := hl.Message{Version: 3, Nonce: 1, Origin: hl.DomainSolanaTestnet, Destination: hl.DomainGnoTest, Body: hl.TokenMessage(raw, 5, nil)}
	d := solDispatch{Raw: hexBytes(msg.Bytes())}
	_ = d.fill()
	to, amount, bad, ok := d.token()
	if !ok || !bad || to != "" || amount != 5 {
		t.Fatalf("token(): %q %d bad %v ok %v", to, amount, bad, ok)
	}
}

// G2: deliveries are paused by any flag on the pausable ISM or a standing quarantine.
func TestDeliveriesPausedNow(t *testing.T) {
	g := &sol.Guardians{}
	if paused, _ := deliveriesPausedNow(g, 0, 1000); paused {
		t.Fatal("paused with no flag and no quarantine")
	}
	if paused, why := deliveriesPausedNow(g, 2000, 1000); !paused || !strings.Contains(why, "quarantine") {
		t.Fatalf("a standing quarantine did not pause: %v %s", paused, why)
	}
	if paused, _ := deliveriesPausedNow(g, 999, 1000); paused {
		t.Fatal("an ended quarantine paused")
	}
	g.Flags[5] = &sol.Flag{Time: 1}
	if paused, why := deliveriesPausedNow(g, 0, 1000); !paused || !strings.Contains(why, "flag") {
		t.Fatalf("a watch flag did not pause: %v %s", paused, why)
	}
}

// G4: a decision whose number the admin program recorded is "recorded" when a copy of ours went through — by
// this relayer's own record, or by a processed-message record at finalized — and "taken" only when no copy's
// record is there on a second pass either; the alarm fires once.
func TestPastDecisionTwoPasses(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "backup")
	ctx := context.Background()
	processed := map[string]bool{}
	r.solProcessed = func(_ context.Context, id [32]byte) (bool, error) { return processed[hex.EncodeToString(id[:])], nil }
	var idA, idB [32]byte
	idA[0], idB[0] = 1, 2
	r.rememberCopy(5, 3, idA)
	r.rememberCopy(5, 3, idA) // once
	r.rememberCopy(5, 9, idB)
	if len(r.state.DecisionCopies["5"]) != 2 {
		t.Fatalf("copies: %v", r.state.DecisionCopies["5"])
	}
	r.pastDecision(ctx, 5, 7, "3")
	if r.state.Done["3"] != "" {
		t.Fatalf("decided on the first pass: %q", r.state.Done["3"])
	}
	if _, ok := r.Alarm.Standing()["decision:taken:5"]; ok {
		t.Fatal("reported taken on the first pass")
	}
	// the record of the other copy appears at finalized before the second pass: recorded, never taken
	processed[hex.EncodeToString(idB[:])] = true
	r.pastDecision(ctx, 5, 7, "3")
	if r.state.Done["3"] != "recorded" {
		t.Fatalf("a copy processed at finalized: %q", r.state.Done["3"])
	}
	// another number: absent on both passes → taken, once
	var idC [32]byte
	idC[0] = 3
	r.rememberCopy(6, 4, idC)
	r.pastDecision(ctx, 6, 7, "4")
	r.pastDecision(ctx, 6, 7, "4")
	if r.state.Done["4"] != "taken" || !r.state.Taken["6"] {
		t.Fatalf("not taken on the second pass: %q", r.state.Done["4"])
	}
	if _, ok := r.Alarm.Standing()["decision:taken:6"]; !ok {
		t.Fatal("no taken alarm")
	}
	// a copy this relayer delivered earlier: recorded at once
	var idD [32]byte
	idD[0] = 4
	r.rememberCopy(7, 5, idD)
	r.rememberCopy(7, 6, idD)
	r.state.Done["5"] = "delivered"
	r.pastDecision(ctx, 7, 7, "6")
	if r.state.Done["6"] != "recorded" {
		t.Fatalf("own delivery not recognised: %q", r.state.Done["6"])
	}
}

// G8/G12: Done is pruned to the nonces the walk consults; forget clears a parked alarm.
func TestPruneDoneAndForget(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	r.state.GnoNext = 5
	r.state.Done = map[string]string{"1": "delivered", "4": "other", "5": "other", "9": "delivered"}
	r.pruneDone()
	if len(r.state.Done) != 2 || r.state.Done["5"] == "" || r.state.Done["9"] == "" {
		t.Fatalf("pruned to %v", r.state.Done)
	}
	r.state.Attempts["x"] = &attempt{Parked: r.now()}
	r.Alarm.Alarm("parked:x", "parked")
	r.forget("x")
	if _, ok := r.Alarm.Standing()["parked:x"]; ok {
		t.Fatal("forget left the parked alarm standing")
	}
}

type failingClient struct {
	fakeClient
	errAt map[int64]error
}

func (c failingClient) LeafBytes(ctx context.Context, h int64, id [32]byte) (int, bool, error) {
	if err := c.errAt[h]; err != nil {
		return 0, true, err
	}
	return c.fakeClient.LeafBytes(ctx, h, id)
}

// G8/G11: a decision is re-emitted like a route message, and a message whose read fails is skipped, not the pass.
func TestReemitsDecisionsAndSkipsFailures(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	r.Cfg.Gno.Gov = "gno.land/r/test/hlgov"
	r.Cfg.Solana.Admin = "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	gov, _ := r.Cfg.GovSender()
	admin := hl.MustKey32(r.Cfg.Solana.Admin)
	r.solProcessed = func(context.Context, [32]byte) (bool, error) { return false, nil }
	routeMsg := hl.Message{Version: 3, Nonce: 0, Origin: hl.DomainGnoTest, Sender: route, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, 5, nil)}
	decision := hl.Message{Version: 3, Nonce: 1, Origin: hl.DomainGnoTest, Sender: gov, Destination: hl.DomainSolanaTestnet, Recipient: admin, Body: sol.DecisionBody(7, 1, nil)}
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"hook":"","address":"","count":2,"root":"","delivered":0,"live":true,"launchedAt":1,"launchOrigin":%d,"decisions":0,"signers":{"keys":[],"threshold":0,"groupA":[],"groupB":[]},"verifiers":{},"registered":[],"seats":[],"routes":{}}`, hl.DomainGnoTest, hl.DomainSolanaTestnet)
	f.renders[testMailbox+":msg/0"] = msgRender(routeMsg, 50)
	f.renders[testMailbox+":msg/1"] = msgRender(decision, 60)
	r.client = failingClient{fakeClient: fakeClient{setFrom: 100}, errAt: map[int64]error{50: errors.New("block_results: timeout")}}
	err := r.reemits(ctx)
	if err == nil || !strings.Contains(err.Error(), "gno #0") {
		t.Fatalf("the failed read was not reported: %v", err)
	}
	calls := f.callsOf(testMailbox + ".Reemit")
	if len(calls) != 1 || calls[0] != testMailbox+".Reemit(1)" {
		t.Fatalf("the decision was not re-emitted past the failed route message: %v", calls)
	}
	// the Reemit walk runs once a minute, or when an attempt is due
	r.nextReemits = r.now().Add(time.Minute)
	if r.reemitsDue() {
		t.Fatal("due inside the minute")
	}
	r.state.Attempts["reemit:0"] = &attempt{Next: r.now()}
	if !r.reemitsDue() {
		t.Fatal("not due with a retry due")
	}
	advance(r, 2*time.Minute)
	delete(r.state.Attempts, "reemit:0")
	if !r.reemitsDue() {
		t.Fatal("not due after the minute")
	}
}

func daoRenders(f *fakeChain, dao string, pid int, status string, yes float64, realm string) {
	f.renders[dao+":"] = fmt.Sprintf("# proposals\n\n### [Prop #%d - hlfees: set treasurer](/r/gov/dao:%d)\n\nAuthor: g1x\n\nStatus: %s\n\n", pid, pid, status)
	f.renders[dao+":"+fmt.Sprint(pid)] = fmt.Sprintf("# Prop #%d\n\nAuthor: g1x\n\nset treasurer g1y\n\nThis proposal contains the following metadata:\n\nExecutor created in: `%s`\n\nYES PERCENT: %.2f%%\n", pid, realm, yes)
}

// F2: a builder-made proposal with the votes and past its 14 days (counted from when the relayer first saw it)
// is finished with ExecuteOrRejectProposal; one within its days, or without the votes, is left alone.
func TestFinishProposals(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	r.Cfg.Gno.DAO = "gno.land/r/gov/dao"
	r.Cfg.Gno.Fees = "gno.land/r/test/hlfees"
	daoRenders(f, r.Cfg.Gno.DAO, 1, "ACTIVE", 100, r.Cfg.Gno.Fees)
	props, why := ReadProposals(ctx, f, r.Cfg, 100, DefaultSupermajority)
	if why != "" || len(props) != 1 || !props[0].Armed || props[0].Realm != r.Cfg.Gno.Fees {
		t.Fatalf("read: %v %s", props, why)
	}
	if err := r.finishProposals(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(f.callsOf(r.Cfg.Gno.DAO)); n != 0 {
		t.Fatalf("finished on first sight: %d", n)
	}
	advance(r, ProposalDeadline-time.Hour)
	_ = r.finishProposals(ctx)
	if n := len(f.callsOf(r.Cfg.Gno.DAO)); n != 0 {
		t.Fatalf("finished within its days: %d", n)
	}
	advance(r, 2*time.Hour)
	_ = r.finishProposals(ctx)
	calls := f.callsOf(r.Cfg.Gno.DAO)
	if len(calls) != 1 || calls[0] != r.Cfg.Gno.DAO+".ExecuteOrRejectProposal(1)" {
		t.Fatalf("not finished past its days: %v", calls)
	}
	// without the votes: nothing, ever
	daoRenders(f, r.Cfg.Gno.DAO, 2, "ACTIVE", 40, r.Cfg.Gno.Fees)
	_ = r.finishProposals(ctx)
	advance(r, ProposalDeadline+time.Hour)
	_ = r.finishProposals(ctx)
	if n := len(f.callsOf(r.Cfg.Gno.DAO)); n != 1 {
		t.Fatalf("a proposal without the votes was finished: %d", n)
	}
	// a proposal no longer open is forgotten
	daoRenders(f, r.Cfg.Gno.DAO, 2, "REJECTED", 40, r.Cfg.Gno.Fees)
	_ = r.finishProposals(ctx)
	if _, ok := r.state.ProposalsSeen["2"]; ok {
		t.Fatal("a closed proposal was kept")
	}
}

// G1: a stage on chain is this plan's only field by field.
func TestStageMatches(t *testing.T) {
	hdr := func(h int64) *gnolight.Header {
		return &gnolight.Header{ChainID: "dev", Height: h, ValidatorsHash: make([]byte, 32), NextValidatorsHash: make([]byte, 32)}
	}
	proof := &gnolight.SimpleProof{Total: 4, Index: 1, LeafHash: [32]byte{1}, Aunts: [][32]byte{{2}, {3}}}
	msgHash := [32]byte{9}
	byHeader := &sol.StageInitArgs{Kind: sol.StageKindProof, Header: hdr(100), NSlots: 1, MessageHash: msgHash, Proof: proof, LeafLen: 50}
	h100 := hdr(100).Hash()
	st := &sol.Stage{Kind: sol.StageKindProof, Key: msgHash, HeaderHash: h100, NSlots: 1, Proof: &gnolight.SimpleProof{Total: 4, Index: 1, LeafHash: [32]byte{1}, Aunts: [][32]byte{{2}, {3}}}, LeafLen: 50}
	if ok, why := stageMatches(st, byHeader); !ok {
		t.Fatal(why)
	}
	byRing := &sol.StageInitArgs{Kind: sol.StageKindProof, RingHash: h100, MessageHash: msgHash, Proof: proof, LeafLen: 50}
	if ok, why := stageMatches(st, byRing); ok || !strings.Contains(why, "ring") {
		t.Fatalf("a by-header stage matched a by-ring plan: %v %s", ok, why)
	}
	ringStage := *st
	ringStage.ByRing = true
	if ok, why := stageMatches(&ringStage, byRing); !ok {
		t.Fatal(why)
	}
	other := *st
	other.HeaderHash = hdr(101).Hash()
	if ok, _ := stageMatches(&other, byHeader); ok {
		t.Fatal("another header matched")
	}
	other = *st
	other.LeafLen = 51
	if ok, _ := stageMatches(&other, byHeader); ok {
		t.Fatal("another leaf length matched")
	}
	other = *st
	other.Proof = &gnolight.SimpleProof{Total: 4, Index: 2, LeafHash: [32]byte{1}, Aunts: [][32]byte{{2}, {3}}}
	if ok, _ := stageMatches(&other, byHeader); ok {
		t.Fatal("another proof matched")
	}
	other = *st
	other.Key = [32]byte{8}
	if ok, _ := stageMatches(&other, byHeader); ok {
		t.Fatal("another message's stage matched")
	}
	// an update plan against an update stage, and a next set (S-02: the stage carries the set's length and hash,
	// the set itself arriving in chunks)
	next := []gnolight.Validator{{PubKey: [32]byte{1}, Power: 10}}
	upd := &sol.StageInitArgs{Kind: sol.StageKindUpdate, Header: hdr(200), NSlots: 3}
	upd.Announce(next)
	us := &sol.Stage{Kind: sol.StageKindUpdate, Key: hdr(200).Hash(), HeaderHash: hdr(200).Hash(), NSlots: 3, NextSetLen: 1, NextSetHash: sol.SetHash(next)}
	if ok, why := stageMatches(us, upd); !ok {
		t.Fatal(why)
	}
	us.NextSetLen, us.NextSetHash = 0, [32]byte{}
	if ok, _ := stageMatches(us, upd); ok {
		t.Fatal("a stage announcing nothing matched an announcing plan")
	}
	us.NextSetLen, us.NextSetHash = 2, sol.SetHash(next)
	if ok, _ := stageMatches(us, upd); ok {
		t.Fatal("a stage announcing another length matched")
	}
	// a next set's chunk transaction is read back (the resume skips chunks the stage holds)
	client0 := sol.LightClient{}
	if off, n, ok := nextSetChunk([]solana.Instruction{sol.ComputeLimitIx(1), client0.StageNextSetIx(solana.PublicKey{}, solana.PublicKey{}, 25, next)}); !ok || off != 25 || n != 1 {
		t.Fatalf("nextSetChunk: %d %d %v", off, n, ok)
	}
	// the plan is read from the staging's first transaction
	client := sol.LightClient{Program: solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")}
	sub := solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	txs, err := client.ProofTxs(sub, "dev", nil, h100, nil, nil, []byte("m"), proof, make([]byte, 50), 3, 400_000)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := planOf(txs)
	if !ok || plan.Kind != sol.StageKindProof || plan.RingHash != h100 || plan.LeafLen != 50 {
		t.Fatalf("planOf: %v %+v", ok, plan)
	}
}

// G9: a transaction's instructions are folded in execution order, the inner ones under their top-level
// instruction — an instruction of the client sent by CPI reaches the record (no program CPIs the client since
// M2j; the order rule stands for any inner instruction, here the client's init under another program's call).
func TestInstructionsInOrderFoldsInner(t *testing.T) {
	client := sol.LightClient{Program: solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")}
	admin := solana.MustPublicKeyFromBase58("9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr")
	mailbox := solana.MustPublicKeyFromBase58("E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi")
	var trusted [32]byte
	trusted[0] = 7
	initIx := client.InitIx(admin, admin, "dev2", 2, 5, trusted, nil, admin)
	initData, _ := initIx.Data()
	keys := []solana.PublicKey{admin, client.Program, client.State()}
	top := []solana.CompiledInstruction{{ProgramIDIndex: 0, Accounts: []uint16{2}, Data: []byte{3, 0, 0, 0, 0, 0, 0, 0, 0}}}
	inner := []rpc.InnerInstruction{{Index: 0, Instructions: []rpc.CompiledInstruction{{ProgramIDIndex: 1, Accounts: []uint16{2}, Data: solana.Base58(initData)}}}}
	ixs := instructionsInOrder(top, inner, keys)
	if len(ixs) != 2 || !ixs[0].Program.Equals(admin) || !ixs[1].Program.Equals(client.Program) || ixs[1].Data[0] != 0 {
		t.Fatalf("order: %+v", ixs)
	}
	var r ClientRecord
	r.Fold(client, mailbox, ixs, keys, 40, "s1")
	if r.Count != 1 || r.Recent[0].Kind != "init" || r.Recent[0].Height != 5 {
		t.Fatalf("the init was not folded: %+v", r.Recent)
	}
}

// G12: a non-signature key file is refused by the Solana loader too.
func TestSolanaKeyFileMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.json")
	w := solana.NewWallet()
	b := []byte("[")
	for i, x := range w.PrivateKey {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, []byte(fmt.Sprint(x))...)
	}
	b = append(b, ']')
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sol.PrivateKeyFromFile(p); err == nil {
		t.Fatal("a 0644 keypair file was loaded")
	}
	_ = os.Chmod(p, 0o600)
	if k, err := sol.PrivateKeyFromFile(p); err != nil || !k.PublicKey().Equals(w.PublicKey()) {
		t.Fatalf("load: %v", err)
	}
}

func gnoHead(h int64, t time.Time) gno.Head { return gno.Head{Height: h, Time: t} }

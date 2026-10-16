package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The tests of audit round 6's Go fixes (M3j): G53 a refunded park reserves nothing; G46 an unread line holds the
// walk; G48 the lines' realms' proposals and queued decisions are read; and the final check's AT2 (M3k): a
// refund is GovDAO's apply, never the relayer's Execute.

// AT2 (the relayer's half): a matured refund decision is never applied by the relayer — the realm's Execute
// refuses the kind and GovDAO's apply-refund proposal applies it, at once, after the member read hlwatch's gate
// — whatever the token program's router on Solana says (the successor enrolled here, where M3j's gate would
// have applied it); the status says it awaits GovDAO's apply, the line is logged once over the ticks, no alarm
// stands and nothing is attempted (so nothing can park); the credit decision beside it is applied as before.
func TestRefundIsNeverExecutedByTheRelayer(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", false, 3)
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"live":true,"routes":{%q:{"successor":%q},%q:{"predecessor":%q}}}`, hl.DomainGnoTest, testRoute, testSucc, testSucc, testRoute)
	now := f.head.Time.Unix()
	f.renders[testMailbox+":decisions.json"] = "[]"
	f.renders[testRoute+":decisions.json"] = "[]"
	f.renders[testSucc+":decisions.json"] = decisionsRender(
		decisionJSON{ID: 4, Kind: "refund", State: "pending", Made: now - 1000, Recorded: now - 1000, Due: now - 1},
		decisionJSON{ID: 5, Kind: "credit", State: "pending", Made: now - 1000, Recorded: now - 1000, Due: now - 1})
	succ := RealmKey(testSucc)
	state := tokenStateBytes(hl.DomainGnoTest, &succ)
	r.solData = func(context.Context, solana.PublicKey) ([]byte, error) { return state, nil }
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	for tick := 0; tick < 3; tick++ {
		if err := r.executes(ctx); err != nil {
			t.Fatal(err)
		}
		advance(r, time.Minute)
	}
	if n := len(f.callsOf(testSucc + ".Execute(4)")); n != 0 {
		t.Fatalf("the relayer applied a refund: %d calls", n)
	}
	if n := len(f.callsOf(testSucc + ".Execute(5)")); n != 3 {
		t.Fatalf("the credit decision beside it was not applied each tick (the fake's render keeps it pending): %d calls", n)
	}
	key := "execute:" + testSucc + "#4"
	if n := r.last[key]; !strings.Contains(n, "awaiting GovDAO's apply (an apply-refund proposal, at once; Execute refuses a refund and the relayer never calls it") {
		t.Fatalf("the refund's status line: %q", n)
	}
	if n := strings.Count(logs.String(), testSucc+" decision #4 (refund) is mature and awaiting GovDAO's apply"); n != 1 {
		t.Fatalf("the line was logged %d times over three ticks, want once:\n%s", n, logs.String())
	}
	if _, ok := r.state.Ready[key]; ok || r.state.Attempts[key] != nil {
		t.Fatal("the refund has an action clock or an attempt record (a park could follow)")
	}
	for k := range r.Alarm.Standing() {
		if strings.Contains(k, "refund") || strings.Contains(k, "#4") {
			t.Fatalf("an alarm stands for the refund: %s", k)
		}
	}
}

// AS4: the monitor alarms limits:mismatch:<token> while a route's live per-message maximum exceeds the pausable
// ISM's (a send between the two is taken on gno.land and parked for good on Solana, OverMax until a kind 12),
// and clears it once they agree; the live realm is the line's last while a migration is open.
func TestLimitsMismatchAlarms(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	one := int64(1_000_000)
	ps := &sol.PausableState{}
	ps.Limits[1] = sol.MintLimit{MaxPerMessage: 25_000 * uint64(one), DayCap: 100_000 * uint64(one)}
	route := func(maximum int64) map[string]*routeJSON {
		return map[string]*routeJSON{"BUBBLE": {Lock: 1000, Maximum: maximum}}
	}
	mb := &mailboxJSON{}
	m.capRatios(ps, route(50_000*one), mb)
	if a, ok := m.Alarm.Standing()["limits:mismatch:BUBBLE"]; !ok || !strings.Contains(a, "BUBBLE's per-message maximum on gno.land, 50000000000, is above the pausable ISM's 25000000000") {
		t.Fatalf("no alarm for a route maximum above the ISM's: %v", m.Alarm.Standing())
	}
	m.capRatios(ps, route(25_000*one), mb)
	if _, ok := m.Alarm.Standing()["limits:mismatch:BUBBLE"]; ok {
		t.Fatal("the alarm stands with the maxima equal")
	}
	m.capRatios(ps, route(10_000*one), mb)
	if _, ok := m.Alarm.Standing()["limits:mismatch:BUBBLE"]; ok {
		t.Fatal("the alarm stands with gno.land's maximum below the ISM's (allowed: a lower one strands nothing)")
	}
	// a migration open: the successor's maximum is the live one
	m.migs["BUBBLE"] = &migration{Symbol: "BUBBLE", Predecessor: testRoute, Successor: testSucc, Pred: &routeJSON{Maximum: 10_000 * one}, Succ: &routeJSON{Maximum: 30_000 * one}}
	m.capRatios(ps, route(10_000*one), mb)
	if _, ok := m.Alarm.Standing()["limits:mismatch:BUBBLE"]; !ok {
		t.Fatalf("the successor's maximum above the ISM's did not alarm: %v", m.Alarm.Standing())
	}
	if text := limitsMismatch("GNOT", 30_000*one, sol.MintLimit{MaxPerMessage: 25_000 * uint64(one)}); !strings.Contains(text, "raise the pausable ISM's by kind 12 first") {
		t.Fatalf("the text: %s", text)
	}
}

// G47: a checkpoint refused on a trusted header the own node's block does not hold strokes the pausable ISM and
// the admin program at once (the fork of §6.4, its cut at the checkpoint's slot), not a webhook line alone; and
// on a checkpointed install the trusted header is compared with the own node's block every poll — flagged
// through the record's two-minute patience — since the checkpointed record never reaches it; an agreeing node
// flags nothing.
func TestCheckpointMismatchIsAStroke(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	m.Cfg.Solana.Client, m.Cfg.Solana.Pausable, m.Cfg.Solana.Admin = "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU", "9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr", "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	m.Cfg.Routes[0].Token = "" // no token program: the checkpoint seeds no scan cursor (the providers are fakes here)
	key := solana.NewWallet()
	rpcFake := &strokeRPC{accounts: map[string][]byte{}, owners: map[string]string{}}
	m.solWatch = sol.NewClient(rpcFake.serve(t).URL, key.PrivateKey)
	m.RecordFrom = "client"
	trusted := [32]byte{0xab, 0xcd}
	cs := &clientState{Head: &sol.ClientHead{TrustedHeight: 100, TrustedHash: trusted}, Body: &sol.ClientBody{Records: 7}, Slot: 5000}
	// the checkpoint refused on a forked trusted header: the stroke at once
	m.own = &hashNode{hash: "deadbeef"}
	if err := m.applyCheckpoint(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
	if m.state.SolFlagsSet != 1 || len(rpcFake.sent) != 1 {
		t.Fatalf("the refused checkpoint did not stroke the Solana objects: %d flags set, %d sent", m.state.SolFlagsSet, len(rpcFake.sent))
	}
	if a, ok := m.Alarm.Standing()["header:mismatch"]; !ok || !strings.Contains(a, "HEADER MISMATCH AT THE CHECKPOINT") {
		t.Fatalf("no header:mismatch flag for the refused checkpoint: %v", m.Alarm.Standing())
	}
	if m.state.Record.FromSlot != 0 || m.state.Record.Count != 0 {
		t.Fatal("a base was taken over a forked header")
	}
	// the per-poll compare on a checkpointed install: a fresh monitor whose base was taken, the node forking later
	m2 := testMonitor(f)
	m2.Cfg = m.Cfg
	m2.solWatch = m.solWatch
	m2.RecordFrom = "client"
	m2.own = &hashNode{hash: hex.EncodeToString(trusted[:])}
	if err := m2.applyCheckpoint(context.Background(), cs); err != nil || m2.state.Record.FromSlot != 5000 || m2.state.SolFlagsSet != 0 {
		t.Fatalf("the agreeing checkpoint: %v (from slot %d, flags %d)", err, m2.state.Record.FromSlot, m2.state.SolFlagsSet)
	}
	m2.checkpointedHeaderCheck(context.Background(), cs)
	if _, ok := m2.Alarm.Standing()["header:mismatch"]; ok || m2.state.SolFlagsSet != 0 {
		t.Fatalf("an agreeing node alarmed or flagged: %v %d", m2.Alarm.Standing(), m2.state.SolFlagsSet)
	}
	m2.own = &hashNode{hash: "cdcd"} // the node's block at the trusted height differs now (a fork, or a forged header)
	m2.checkpointedHeaderCheck(context.Background(), cs)
	if a, ok := m2.Alarm.Standing()["header:mismatch"]; !ok || !strings.Contains(a, "flagged once it has stood 2m0s") || m2.state.SolFlagsSet != 0 {
		t.Fatalf("the first poll's mismatch is not an alarm under the patience: %v %d", m2.Alarm.Standing(), m2.state.SolFlagsSet)
	}
	later := m2.now().Add(2 * time.Minute)
	m2.now = func() time.Time { return later }
	m2.checkpointedHeaderCheck(context.Background(), cs)
	if m2.state.SolFlagsSet != 1 {
		t.Fatalf("the mismatch standing two minutes did not stroke: %d flags set", m2.state.SolFlagsSet)
	}
	if a := m2.Alarm.Standing()["header:mismatch"]; !strings.Contains(a, "the client holds header 100 as abababab") && !strings.Contains(a, "the client holds header 100 as abcd") {
		t.Fatalf("the flag's text: %q", a)
	}
	// a record built from the deploy compares nothing here (its own Check reaches the header)
	m3 := testMonitor(f)
	m3.Cfg = m.Cfg
	m3.own = &hashNode{hash: "cdcd"}
	m3.checkpointedHeaderCheck(context.Background(), cs)
	if _, ok := m3.Alarm.Standing()["header:mismatch"]; ok {
		t.Fatal("a record built from the deploy compared the trusted header here")
	}
}

// M1i's shapes (R35, AS1): the mailbox's `pulled`, `dropped` and `droppedRoutes` are read; a dropped route is no
// hop of the line, but a burn addressed to its address is the line's last realm's to deliver once its import
// ended (waiting before); the relayer alarms router:unknown naming a dropped route while the token program
// enrols one.
func TestM1iShapesAreRead(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	dropped := "gno.land/r/test/hlbubblex"
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", false, 0) // page 0 not in yet
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"live":true,"routes":{%q:{"successor":%q,"dropped":[%q]},%q:{"predecessor":%q,"pulled":false}},"droppedRoutes":{%q:%q}}`, hl.DomainGnoTest, testRoute, testSucc, dropped, testSucc, testRoute, dropped, testRoute)
	mb, err := mailboxAt(ctx, f, testMailbox, 0)
	if err != nil || !reflect.DeepEqual(mb.DroppedRoutes, map[string]string{dropped: testRoute}) || len(mb.Routes[testRoute].Dropped) != 1 || mb.Routes[testSucc].Pulled {
		t.Fatalf("the mailbox's new fields: %+v %v", mb, err)
	}
	// a burn addressed to the dropped route: the line's last realm's, waiting on its import; delivered there after
	burn := func(to string) solDispatch {
		rcpt := RealmKey(to)
		m := hl.Message{Version: 3, Origin: hl.DomainSolanaTestnet, Sender: token, Destination: hl.DomainGnoTest, Recipient: rcpt, Body: hl.TokenMessage([32]byte{1}, 5, nil)}
		d := solDispatch{Raw: m.Bytes()}
		_ = d.fill()
		return d
	}
	r.resetStep()
	rt, target, wait, err := r.deliveryTarget(ctx, burn(dropped))
	if err != nil || rt == nil || target != testSucc || wait == "" {
		t.Fatalf("a burn to the dropped route before the import: %v %q %q %v", rt, target, wait, err)
	}
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", false, 2) // the import ended
	r.resetStep()
	if rt, target, wait, err := r.deliveryTarget(ctx, burn(dropped)); err != nil || rt == nil || target != testSucc || wait != "" {
		t.Fatalf("a burn to the dropped route after the import: %v %q %q %v", rt, target, wait, err)
	}
	if rt, _, _, _ := r.deliveryTarget(ctx, burn("gno.land/r/test/stranger")); rt != nil {
		t.Fatal("a burn to a stranger's address was somebody's")
	}
	// the dropped route is no hop of the line
	mig, err := r.migrationFor(ctx, r.Cfg.Routes[0])
	if err != nil || mig == nil || mig.Has(dropped) || len(mig.Paths()) != 2 {
		t.Fatalf("the line: %v %v", mig, err)
	}
	// the enrolled router a dropped route: alarmed, named (AS1)
	droppedKey := RealmKey(dropped)
	state := tokenStateBytes(hl.DomainGnoTest, &droppedKey)
	r.solData = func(context.Context, solana.PublicKey) ([]byte, error) { return state, nil }
	r.resetStep()
	r.readLines(ctx)
	r.checkRouters(ctx, hl.DomainGnoTest)
	if a, ok := r.Alarm.Standing()["router:unknown:BUBBLE"]; !ok || !strings.Contains(a, "a DROPPED route "+dropped+" (re-pointed away by "+testRoute+")") {
		t.Fatalf("the dropped route as the router: %v", r.Alarm.Standing())
	}
	// hlwatch's view of the same
	mv, err := ReadMailbox(ctx, f, testMailbox, 0)
	if err != nil || mv.DroppedRoutes[dropped] != testRoute || mv.Predecessors[testSucc] != testRoute || mv.Pulled[testSucc] || len(mv.RoutePaths) != 2 {
		t.Fatalf("the mailbox view: %+v %v", mv, err)
	}
}

// G49, AS3: on a checkpointed install the forged-credit rule trusts a waiting claim credited at or below the
// cut's height (never a candidate, counted), alarms — never flags — one credited after it while the index was
// never seeded from the mailbox's accounts, and flags one credited after it once the index is seeded whole; a
// record built from the deploy judges every claim as before.
func TestCheckpointedIndexTrustsEarlierClaims(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	m.own = &doneNode{fakeChain: f} // the credits are found by id in hlmailbox's done tree (E4-RL1)
	ctx := context.Background()
	mb := mailboxWith()
	m.seat = 5
	early, late := strings.Repeat("aa", 32), strings.Repeat("bb", 32)
	f.renders[testMailbox+":delivered/"+early] = "90"
	f.renders[testMailbox+":delivered/"+late] = "150"
	mb.Delivered = 2
	f.renders[testRoute+":claim/"+early] = fmt.Sprintf(`{"id":%q,"to":"g1alice","amount":200,"paid":0,"state":"open","by":"signers","at":1,"height":90,"seq":1}`, early)
	f.renders[testRoute+":claim/"+late] = fmt.Sprintf(`{"id":%q,"to":"g1bob","amount":300,"paid":0,"state":"open","by":"signers","at":1,"height":150,"seq":2}`, late)
	m.state.IndexFromHeight, m.state.IndexSeeded = 100, false
	poll := func() {
		if err := m.unindexedCredits(ctx, mb, 200); err != nil {
			t.Fatal(err)
		}
		advance := m.now().Add(m.Every + time.Second)
		m.now = func() time.Time { return advance }
	}
	for i := 0; i < 3; i++ {
		poll()
	}
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 0 {
		t.Fatalf("a claim credited before the checkpoint, or after it on an unseeded index, was flagged: %d", n)
	}
	if m.status.ClaimsBeforeBase != 1 {
		t.Fatalf("the claims passed over as before the checkpoint: %d", m.status.ClaimsBeforeBase)
	}
	if _, ok := m.Alarm.Standing()["credit:unindexed-before-base:"+early]; ok {
		t.Fatal("a claim credited before the checkpoint was alarmed")
	}
	if a, ok := m.Alarm.Standing()["credit:unindexed-before-base:"+late]; !ok || !strings.Contains(a, "its burn may predate the checkpoint's slot") || !strings.Contains(a, "alarmed, never flagged") {
		t.Fatalf("no alarm for the claim credited after the cut on an unseeded index: %v", m.Alarm.Standing())
	}
	for _, key := range []string{"credit:unindexed:" + early + ":flag", "credit:unindexed:" + late + ":flag"} {
		if _, ok := m.Alarm.Standing()[key]; ok {
			t.Fatalf("%s flagged", key)
		}
	}
	// the index seeded whole: the claim credited after the cut is judged — a forged credit on the second poll;
	// the one before the cut stays trusted
	m.state.IndexSeeded = true
	poll()
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 0 {
		t.Fatalf("flagged on the first poll after the seed: %d", n)
	}
	poll()
	if calls := f.callsOf(testMailbox + ".SetFlag"); len(calls) != 2 {
		t.Fatalf("the late claim was not flagged once the index was seeded: %v", calls)
	}
	if _, ok := m.Alarm.Standing()["credit:unindexed:"+late+":flag"]; !ok {
		t.Fatalf("no flag alarm for the forged late claim: %v", m.Alarm.Standing())
	}
	if _, ok := m.Alarm.Standing()["credit:unindexed-before-base:"+late]; ok {
		t.Fatal("the before-base alarm stands after the seed")
	}
	if _, ok := m.Alarm.Standing()["credit:unindexed:"+early+":flag"]; ok || m.status.ClaimsBeforeBase != 1 {
		t.Fatalf("the early claim was judged after the seed: %v (%d before the base)", m.Alarm.Standing(), m.status.ClaimsBeforeBase)
	}
	// no checkpoint: both judged as before
	for _, v := range []struct {
		height, from int64
		seeded       bool
		want         unindexedVerdict
	}{{90, 0, false, unindexedCandidate}, {90, 100, false, unindexedBeforeBase}, {100, 100, true, unindexedBeforeBase}, {150, 100, false, unindexedUnseeded}, {150, 100, true, unindexedCandidate}} {
		if got := judgeUnindexed(v.height, v.from, v.seeded); got != v.want {
			t.Fatalf("judgeUnindexed(%d, %d, %v) = %d, want %d", v.height, v.from, v.seeded, got, v.want)
		}
	}
}

// AS3: the checkpoint seeds the dispatch index from the mailbox's dispatched-message accounts — every burn of a
// configured token toward gno.land, both providers' bits set — and a reader that refuses the call seeds nothing
// (the height rule then stands).
func TestCheckpointSeedsTheIndexFromTheMailbox(t *testing.T) {
	cfg := &Config{}
	cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	cfg.Solana.Domain = hl.DomainSolanaTestnet
	token := solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute, Token: token.String()}}
	mailbox := sol.Mailbox{Program: cfg.SolMailbox()}
	account := func(nonce uint32, slot uint64, m hl.Message) *rpc.KeyedAccount {
		b := []byte{1}
		b = append(b, dispatchTag...)
		b = binary.LittleEndian.AppendUint32(b, nonce)
		b = binary.LittleEndian.AppendUint64(b, slot)
		b = append(b, make([]byte, 32)...) // the unique key
		b = append(b, m.Bytes()...)
		return &rpc.KeyedAccount{Pubkey: solana.PublicKey{byte(nonce)}, Account: &rpc.Account{Owner: mailbox.Program, Data: rpc.DataBytesOrJSONFromBytes(b)}}
	}
	burn := hl.Message{Version: 3, Nonce: 4, Origin: hl.DomainSolanaTestnet, Sender: [32]byte(token), Destination: hl.DomainGnoTest, Recipient: RealmKey("gno.land/r/x/alice"), Body: hl.TokenMessage([32]byte{1}, 700, nil)}
	elsewhere := hl.Message{Version: 3, Nonce: 5, Origin: hl.DomainSolanaTestnet, Sender: [32]byte(token), Destination: 77, Recipient: [32]byte{2}, Body: hl.TokenMessage([32]byte{1}, 9, nil)}
	stranger := hl.Message{Version: 3, Nonce: 6, Origin: hl.DomainSolanaTestnet, Sender: [32]byte{3}, Destination: hl.DomainGnoTest, Recipient: [32]byte{2}, Body: hl.TokenMessage([32]byte{1}, 9, nil)}
	reader := &checkpointSol{accounts: rpc.GetProgramAccountsResult{
		account(4, 4000, burn), account(5, 4001, elsewhere), account(6, 4002, stranger),
		{Pubkey: solana.PublicKey{9}, Account: &rpc.Account{Owner: mailbox.Program, Data: rpc.DataBytesOrJSONFromBytes([]byte{1, 2, 3})}}, // not a dispatch
	}}
	index := map[string]*dispatchRec{}
	n, err := seedDispatches(context.Background(), reader, cfg, mailbox, hl.DomainGnoTest, index, time.Unix(1_800_000_000, 0), &Alarmer{Source: "test"})
	if err != nil || n != 1 || len(index) != 1 {
		t.Fatalf("the seed: %d %v (%d records)", n, err, len(index))
	}
	id := hl.ID(burn.Bytes())
	rec := index[hex.EncodeToString(id[:])]
	if rec == nil || rec.Providers != 3 || rec.Slot != 4000 || rec.Amount != 700 || rec.Symbol != "BUBBLE" || rec.Nonce != 4 || rec.Credited != 0 {
		t.Fatalf("the seeded record: %+v", rec)
	}
	// seeded again: nothing doubled
	if n, err := seedDispatches(context.Background(), reader, cfg, mailbox, hl.DomainGnoTest, index, time.Unix(1_800_000_000, 0), &Alarmer{Source: "test"}); err != nil || n != 0 || len(index) != 1 {
		t.Fatalf("a second seed: %d %v (%d records)", n, err, len(index))
	}
	// a reader without program accounts: refused, nothing seeded
	if n, err := seedDispatches(context.Background(), &fakeSol{}, cfg, mailbox, hl.DomainGnoTest, map[string]*dispatchRec{}, time.Now(), nil); err == nil || n != 0 {
		t.Fatalf("a reader that lists no accounts seeded: %d %v", n, err)
	}
	if text := checkpointText(&clientState{Head: &sol.ClientHead{TrustedHeight: 100}, Body: &sol.ClientBody{Records: 7}, Slot: 5000}, 1, 2, 300, 0, errors.New("403")); !strings.Contains(text, "the mailbox's dispatched-message accounts could not be listed (403): the index starts at the slot — claims credited at or below height 300 are trusted") {
		t.Fatalf("the fallback's text: %s", text)
	}
}

// F27: r/hlgov's latest kind 6 enrolling a realm as the token's router is found among its numbers, and its state
// on the admin program is judged — absent, dispatched and not recorded, pending (deferred or not), cancelled,
// recorded and gone — with the gate's verdict built from the router and that state.
func TestKind6StateAndFindEnrolment(t *testing.T) {
	f := newFake()
	gov := "gno.land/r/test/hlgov"
	router := RealmKey(testSucc)
	other := RealmKey(testRoute)
	render := func(n uint64, kind byte, params *sol.Params) {
		body := sol.DecisionBody(kind, n, sol.EncodeParams(params))
		f.renders[gov+":decision/"+fmt.Sprint(n)] = fmt.Sprintf(`{"number":%d,"kind":%d,"body":"%s","id":"x","count":1,"at":%d}`, n, kind, hex.EncodeToString(body), 1_800_000_000)
	}
	render(1, sol.KindSetFee, &sol.Params{Kind: sol.KindSetFee, Lamports: 1})
	render(2, sol.KindEnrollRouter, &sol.Params{Kind: sol.KindEnrollRouter, Token: 1, Domain: hl.DomainGnoTest, Router: router})
	render(3, sol.KindEnrollRouter, &sol.Params{Kind: sol.KindEnrollRouter, Token: 1, Domain: hl.DomainGnoTest, Router: other})  // another realm's
	render(4, sol.KindEnrollRouter, &sol.Params{Kind: sol.KindEnrollRouter, Token: 0, Domain: hl.DomainGnoTest, Router: router}) // the other token's
	f.renders[gov+":decision/5"] = "null"
	ctx := context.Background()
	n, err := FindEnrolment(ctx, f, gov, 5, 100, 1, hl.DomainGnoTest, router)
	if err != nil || n != 2 {
		t.Fatalf("the kind 6 found: %d %v", n, err)
	}
	if n, err := FindEnrolment(ctx, f, gov, 5, 100, 1, hl.DomainGnoTest, [32]byte{7}); err != nil || n != 0 {
		t.Fatalf("a router never enrolled: %d %v", n, err)
	}
	cancelled := []sol.CancelledRange{{From: 2, To: 2}}
	pending := []PendingSolDecision{{Number: 2, Kind: 6, MaturesAt: 1_800_100_000, Deferred: "deferred by a hold on the admin program since x until it ends"}}
	for _, c := range []struct {
		n, last   uint64
		pending   []PendingSolDecision
		cancelled []sol.CancelledRange
		want      string
	}{
		{0, 9, nil, nil, "absent: r/hlgov dispatched no kind 6"},
		{2, 1, nil, nil, "dispatched as r/hlgov's #2, not yet recorded on the admin program (its last number is 1)"},
		{2, 2, pending, nil, "pending on the admin program as #2, matures at 2027-01-16T11:46:40Z (deferred by a hold"},
		{2, 2, nil, cancelled, "CANCELLED on the admin program (#2 is in a cancelled range)"},
		{2, 2, nil, nil, "recorded on the admin program as #2 and gone"},
	} {
		if got := Kind6StateOf(c.n, c.last, c.pending, c.cancelled); !strings.Contains(got.State, c.want) || got.Number != c.n {
			t.Fatalf("the kind 6's state for %d/%d: %+v, want %q", c.n, c.last, got, c.want)
		}
	}
	// the gate: the router read as the realm passes; another realm, none, or an unread router does not
	rv := RouterView{Enrolled: true, Router: router, Realm: testSucc}
	if text, ok := RefundGate("BUBBLE", testSucc, hl.DomainGnoTest, &rv, Kind6State{Number: 2, State: "x"}); !ok || !strings.Contains(text, "ENROLLED ON SOLANA") {
		t.Fatalf("the router as the realm: %s %v", text, ok)
	}
	rv = RouterView{Enrolled: true, Router: other, Realm: testRoute}
	if text, ok := RefundGate("BUBBLE", testSucc, hl.DomainGnoTest, &rv, Kind6State{Number: 2, State: "pending on the admin program as #2"}); ok || !strings.Contains(text, "NOT ENROLLED ON SOLANA: the BUBBLE token program's router for gno.land's domain "+fmt.Sprint(hl.DomainGnoTest)+" is "+hex.EncodeToString(other[:])+" ("+testRoute+"), not this realm, and the kind 6 enrolling it is pending on the admin program as #2") {
		t.Fatalf("the predecessor as the router: %s %v", text, ok)
	}
	if text, ok := RefundGate("BUBBLE", testSucc, hl.DomainGnoTest, &RouterView{}, Kind6State{State: "absent"}); ok || !strings.Contains(text, "is none (no router is enrolled for gno.land's domain), not this realm") {
		t.Fatalf("no router: %s %v", text, ok)
	}
	if text, ok := RefundGate("BUBBLE", testSucc, hl.DomainGnoTest, nil, Kind6State{}); ok || !strings.Contains(text, "NOT JUDGED ON SOLANA") {
		t.Fatalf("an unread router: %s %v", text, ok)
	}
}

// G48: the proposals and the queued decisions of every realm of a token's line are read — the configured realm
// is the predecessor after a migration (AQ1's standard layout), and the refund, the second migrate and the
// lapsed proposal all live on the successor — by hlwatch's readers (LineRealms naming the line's extra realms)
// and by the relayer's proposal finisher.
func TestLineProposalsAndDecisionsAreRead(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	r.Cfg.Gno.DAO = "gno.land/r/gov/dao"
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", false, 3)
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"live":true,"routes":{%q:{"successor":%q},%q:{"predecessor":%q}}}`, hl.DomainGnoTest, testRoute, testSucc, testSucc, testRoute)
	// the line's realms: the configured route and its successor; the successor is the extra one
	lines, extra := LineRealms(ctx, f, r.Cfg, 100)
	if len(lines["BUBBLE"]) != 2 || lines["BUBBLE"][0] != testRoute || lines["BUBBLE"][1] != testSucc || len(extra) != 1 || extra[0] != testSucc {
		t.Fatalf("the lines: %v, extra %v", lines, extra)
	}
	// a proposal whose executor was created in the successor: dropped by the configured realms alone, read with
	// the line's
	daoRenders(f, r.Cfg.Gno.DAO, 3, "ACTIVE", 100, testSucc)
	if props, why := ReadProposals(ctx, f, r.Cfg, 100, DefaultSupermajority); why != "" || len(props) != 0 {
		t.Fatalf("the successor's proposal read without the line: %v %s", props, why)
	}
	props, why := ReadProposals(ctx, f, r.Cfg, 100, DefaultSupermajority, extra...)
	if why != "" || len(props) != 1 || props[0].Realm != testSucc || !props[0].Armed {
		t.Fatalf("the successor's proposal read with the line: %v %s", props, why)
	}
	// the successor's queued decision, listed with the line
	now := f.head.Time.Unix()
	f.renders[testMailbox+":decisions.json"] = "[]"
	f.renders[testRoute+":decisions.json"] = "[]"
	f.renders[testSucc+":decisions.json"] = decisionsRender(decisionJSON{ID: 2, Kind: "refund", State: "pending", Made: now - 100, Recorded: now - 100, Due: now + 500})
	if ds, err := ReadPending(ctx, f, r.Cfg, 100); err != nil || len(ds) != 0 {
		t.Fatalf("the successor's decision read without the line: %v %v", ds, err)
	}
	ds, err := ReadPending(ctx, f, r.Cfg, 100, extra...)
	if err != nil || len(ds) != 1 || ds[0].Realm != testSucc || ds[0].ID != 2 || ds[0].Kind != "refund" {
		t.Fatalf("the successor's decision read with the line: %v %v", ds, err)
	}
	// the relayer's finisher sees a lapsed proposal on the successor: first sight, then past the deadline
	if err := r.finishProposals(ctx); err != nil {
		t.Fatal(err)
	}
	advance(r, 15*24*time.Hour)
	if err := r.finishProposals(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := f.callsOf(r.Cfg.Gno.DAO + ".ExecuteOrRejectProposal(3)"); len(calls) != 1 {
		t.Fatalf("the lapsed proposal on the successor was not finished: %v", f.callsOf(r.Cfg.Gno.DAO))
	}
}

// G46: a route's line that cannot be read for one tick holds the walk at a send whose sender is not the
// configured realm — a successor's send is never walked past as "other" by a transient (Done, GnoNext and the
// Reemit rule left it for good before) — and the walk takes it once the line reads; the enrolled-router check
// skips the route for the tick.
func TestUnreadLineHoldsTheWalk(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	succJSON := routeJSONWith(1000, 0, "", false, 3)
	sender, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testSucc))
	msg := hl.Message{Version: 3, Nonce: 0, Origin: hl.DomainGnoTest, Sender: sender, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, 500, nil)}
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":1,"live":true,"routes":{%q:{"successor":%q},%q:{"predecessor":%q}}}`, hl.DomainGnoTest, testRoute, testSucc, testSucc, testRoute)
	f.renders[testMailbox+":msg/0"] = msgRender(msg, 50)
	r.solProcessed = func(context.Context, [32]byte) (bool, error) { return true, nil }
	// the successor's render fails this tick (no render in the fake): the line is unread, the send held
	if err := r.deliverUnderLightClient(ctx); err != nil {
		t.Fatal(err)
	}
	if r.state.GnoNext != 0 || len(r.state.Done) != 0 {
		t.Fatalf("the walk moved past the successor's send while its line was unread: next %d, done %v", r.state.GnoNext, r.state.Done)
	}
	if a, ok := r.Alarm.Standing()["route:line-unread:BUBBLE"]; !ok || !strings.Contains(a, "could not be read") {
		t.Fatalf("no alarm for the unread line: %v", r.Alarm.Standing())
	}
	if n := r.last["deliver:0"]; !strings.Contains(n, "held") {
		t.Fatalf("the held send's note: %q", n)
	}
	// the router check skips the route for the tick (its line is what the router is judged against)
	state := tokenStateBytes(hl.DomainGnoTest, &sender)
	r.solData = func(context.Context, solana.PublicKey) ([]byte, error) { return state, nil }
	r.checkRouters(ctx, hl.DomainGnoTest)
	if _, ok := r.Alarm.Standing()["router:unknown:BUBBLE"]; ok {
		t.Fatalf("the router was judged against an unread line: %v", r.Alarm.Standing())
	}
	// the render returns: the send is the successor's, walked as ours
	f.renders[testSucc+":json"] = succJSON
	if err := r.deliverUnderLightClient(ctx); err != nil {
		t.Fatal(err)
	}
	if r.state.Done["0"] != "delivered" || r.state.GnoNext != 1 {
		t.Fatalf("the successor's send was not walked once the line read: done %v, next %d", r.state.Done, r.state.GnoNext)
	}
	if _, ok := r.Alarm.Standing()["route:line-unread:BUBBLE"]; ok {
		t.Fatal("the unread-line alarm stands after the line read")
	}
}

// G53: a refunded park reserves nothing — the mark is read for each token's oldest OverDayCap park before the
// reservation is computed, and a refunded park is ended (Done "refunded", its attempt forgotten, alarmed) and
// dropped, so no younger send of its token is held behind it; before, the mark was read only for the message
// about to be staged, which a park under a full bucket never is, and a refunded park held every younger send of
// its token until the bucket could have fitted it, a quarter of a day of flow.
func TestRefundedParkReservesNothing(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", false, 3)
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"live":true,"routes":{%q:{"successor":%q},%q:{"predecessor":%q}}}`, hl.DomainGnoTest, testRoute, testSucc, testSucc, testRoute)
	sender, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testSucc))
	mk := func(n uint32, amount uint64) todo {
		m := hl.Message{Version: 3, Nonce: n, Origin: hl.DomainGnoTest, Sender: sender, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, amount, nil)}
		raw := m.Bytes()
		return todo{n: n, msg: &gnoMsg{Raw: raw, Msg: m, ID: hl.ID(raw)}, kind: kindRoute, rt: &r.Cfg.Routes[0]}
	}
	parked, younger := mk(5, 25_000), mk(6, 100)
	amounts := map[uint32]uint64{5: 25_000, 6: 100}
	for _, td := range []todo{parked, younger} {
		id := hex.EncodeToString(td.msg.ID[:])
		f.renders[testRoute+":send/"+id] = "null" // the predecessor holds no send record: the successor's page took them
		refunded := 0
		if td.n == 5 {
			refunded = 1_800_000_000
		}
		f.renders[testSucc+":send/"+id] = fmt.Sprintf(`{"id":%q,"from":"g1x","amount":%d,"at":1,"imported":true,"refunded":%d}`, id, amounts[td.n], refunded)
	}
	r.state.Attempts["deliver:5"] = &attempt{Failures: 3, Parked: r.now(), Cause: "pausable ISM: OverDayCap"}
	r.Alarm.Alarm("parked:deliver:5", "parked")
	routes := []todo{parked, younger}
	parks := capParks(routes, r.state.Attempts)
	if parks[1] == nil || parks[1].n != 5 || parks[1].amount != 25_000 {
		t.Fatalf("the test's park: %+v", parks[1])
	}
	r.resetStep()
	parks = r.dropRefundedParks(ctx, parks)
	if parks[1] != nil {
		t.Fatalf("a refunded park still reserves: gno #%d's %d", parks[1].n, parks[1].amount)
	}
	if r.state.Done["5"] != "refunded" || r.state.Attempts["deliver:5"] != nil {
		t.Fatalf("the refunded park was not ended: done %q, attempt %+v", r.state.Done["5"], r.state.Attempts["deliver:5"])
	}
	if _, ok := r.Alarm.Standing()["send:refunded:5"]; !ok {
		t.Fatalf("no refunded alarm: %v", r.Alarm.Standing())
	}
	if _, ok := r.Alarm.Standing()["parked:deliver:5"]; ok {
		t.Fatal("the parked alarm stands after the refund ended the park")
	}
	// the younger send is not held behind it: the cap gate reserves nothing
	l := sol.MintLimit{MaxPerMessage: 25_000, DayCap: 10_000}
	if ok, why := capGate(6, 100, false, parks[1], l, r.now().Unix(), 86400, 0); !ok {
		t.Fatalf("the younger send is held behind a refunded park: %s", why)
	}
	// a park not refunded stays, its amount reserved against the younger send
	r.state.Done = map[string]string{}
	r.state.Attempts["deliver:6"] = &attempt{Failures: 3, Parked: r.now(), Cause: "pausable ISM: OverDayCap"}
	r.resetStep()
	parks = r.dropRefundedParks(ctx, capParks([]todo{younger, mk(7, 50)}, r.state.Attempts))
	if parks[1] == nil || parks[1].n != 6 || r.state.Done["6"] != "" {
		t.Fatalf("a park not refunded was dropped: %+v done %q", parks[1], r.state.Done["6"])
	}
	if ok, why := capGate(7, 50, false, parks[1], sol.MintLimit{MaxPerMessage: 25_000, DayCap: 120}, r.now().Unix(), 86400, 0); ok || why == "" {
		t.Fatal("the younger send is not held behind an unrefunded park whose amount the bucket cannot fit beside it")
	}
}

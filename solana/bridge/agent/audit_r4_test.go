package agent

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The tests of audit round 4's fixes (bridge/AUDIT.md: the red team's AQ1–AQ4 and AQ6 where the Go carries them,
// the fact check's C341, the conformance audit's K28, the Go audit's G28–G35, the assumptions audit's F19).

const testSucc = "gno.land/r/test/hlbubble2"

// routeJSONWith renders a route's json with the migration fields set.
func routeJSONWith(lock, owed int64, migratedTo string, importing bool, imported int) string {
	return fmt.Sprintf(`{"address":"g1route","live":true,"balance":0,"paused":"","symbol":"BUBBLE","domain":1399811150,"router":"","oldRouter":"","oldUntil":0,"lock":%d,"sentTotal":%d,"paidTotal":0,"owed":%d,"voided":0,"lost":0,"sweep":0,"cap":100,"meter":50,"capFloor":100,"fee":10,"feeFloor":4,"minimum":20,"maximum":2500,"openClaims":1,"claims":1,"sends":1,"migratedTo":%q,"importing":%v,"imported":%d,"decisions":0}`, lock, lock, owed, migratedTo, importing, imported)
}

// AQ1: a burn addressed to a migrated route is delivered to its successor once the successor's import has ended;
// until then it waits — never attempted, never parked — and the status names why; a burn addressed to the
// successor itself (kind 6 enrolled it) waits only while pages arrive; the relayer's claims and executes follow
// the successor the chain names.
func TestRelayerFollowsTheMigration(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	pred, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	succ, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testSucc))
	burn := func(rcpt [32]byte) solDispatch {
		m := hl.Message{Version: 3, Nonce: 1, Origin: hl.DomainSolanaTestnet, Sender: token, Destination: hl.DomainGnoTest, Recipient: rcpt, Body: hl.TokenMessage([32]byte{1}, 500, nil)}
		d := solDispatch{Sig: "s", Slot: 10, Raw: hexBytes(m.Bytes())}
		if err := d.fill(); err != nil {
			t.Fatal(err)
		}
		return d
	}
	// a live route: its own address, delivered to it
	f.renders[testRoute+":json"] = routeJSONWith(1000, 0, "", false, 0)
	rt, target, wait, err := r.deliveryTarget(ctx, burn(pred))
	if err != nil || rt == nil || target != testRoute || wait != "" {
		t.Fatalf("a live route: %v %q %q %v", rt, target, wait, err)
	}
	if rt, _, _, _ := r.deliveryTarget(ctx, burn(succ)); rt != nil {
		t.Fatal("a burn to a stranger's address was taken while the route is live")
	}
	// the migrate applied, no page pulled yet: the burn to the predecessor waits
	r.routeCache = nil
	f.renders[testRoute+":json"] = routeJSONWith(1000, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(0, 0, "", false, 0)
	rt, target, wait, err = r.deliveryTarget(ctx, burn(pred))
	if err != nil || rt == nil || target != testSucc || !strings.Contains(wait, "whose import has not begun") {
		t.Fatalf("before page 0: %v %q %q %v", rt, target, wait, err)
	}
	// pages arriving: still waits, naming the pages; a burn addressed to the successor waits too
	r.routeCache = nil
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", true, 2)
	if _, _, wait, _ = r.deliveryTarget(ctx, burn(pred)); !strings.Contains(wait, "still arriving (2 pages so far)") {
		t.Fatalf("mid-import: %q", wait)
	}
	if _, target, wait, _ = r.deliveryTarget(ctx, burn(succ)); target != testSucc || wait == "" {
		t.Fatalf("a burn to the successor mid-import: %q %q", target, wait)
	}
	// the last page is in: delivered to the successor, either address
	r.routeCache = nil
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", false, 3)
	for _, d := range []solDispatch{burn(pred), burn(succ)} {
		if rt, target, wait, err := r.deliveryTarget(ctx, d); err != nil || rt == nil || target != testSucc || wait != "" {
			t.Fatalf("after the last page: %v %q %q %v", rt, target, wait, err)
		}
	}
	// the claims loop pays on the successor once the import ended, idles while it arrives; the executes walk
	// the successor's ledger too
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", true, 2)
	_ = r.claims(ctx)
	if n := len(f.callsOf(testSucc+".Claim")) + len(f.callsOf(testRoute+".Claim")); n != 0 {
		t.Fatalf("Claim called while the import arrives: %d", n)
	}
	if last := r.last["claim:BUBBLE"]; !strings.Contains(last, "idle: "+testRoute+" migrated to "+testSucc) {
		t.Fatalf("the status does not name why: %q", last)
	}
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", false, 3)
	f.events[testSucc+".Claim"] = []gno.Event{{Type: "Claimed", Attrs: map[string]string{"count": "1"}}}
	_ = r.claims(ctx)
	if n := len(f.callsOf(testSucc + ".Claim")); n != 1 {
		t.Fatalf("Claim not called on the successor after the import: %d", n)
	}
	if n := len(f.callsOf(testRoute + ".Claim")); n != 0 {
		t.Fatalf("Claim called on the migrated route: %d", n)
	}
	realms := strings.Join(r.realms(ctx), ",")
	if !strings.Contains(realms, testSucc) || !strings.Contains(realms, testRoute) {
		t.Fatalf("the executes walk: %s", realms)
	}
}

// notFoundSol is a Solana reader with no accounts: every processed-message record is absent (not delivered).
type notFoundSol struct{ fakeSol }

func (*notFoundSol) GetAccountInfoWithOpts(context.Context, solana.PublicKey, *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error) {
	return nil, rpc.ErrNotFound
}

func (*notFoundSol) GetMultipleAccountsWithOpts(_ context.Context, keys []solana.PublicKey, _ *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error) {
	return &rpc.GetMultipleAccountsResult{Value: make([]*rpc.Account, len(keys))}, nil
}

// AQ2: a send GovDAO refunded on gno.land is dropped from in flight by the monitors (never a break), named by
// hlwatch while it still sits in hlmailbox, and refused by the relayer, which reads the mark before staging; a
// realm that renders no send record is an error, never a delivery.
func TestRefundedSendNeverDelivers(t *testing.T) {
	f := newFake()
	cfg := &Config{}
	cfg.Gno.Mailbox = testMailbox
	cfg.Solana.Domain = hl.DomainSolanaTestnet
	cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	token := [32]byte{9}
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute, Token: hl.Base58(token[:])}}
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	msg := func(nonce uint32, amount uint64) hl.Message {
		return hl.Message{Version: 3, Nonce: nonce, Origin: hl.DomainGnoTest, Sender: route, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, amount, nil)}
	}
	m0, m1 := msg(0, 25_000), msg(1, 300)
	id0, id1 := hl.ID(m0.Bytes()), hl.ID(m1.Bytes())
	mb := &mailboxJSON{Domain: hl.DomainGnoTest, Count: 2}
	f.renders[testMailbox+":msg/0"] = msgRender(m0, 50)
	f.renders[testMailbox+":msg/1"] = msgRender(m1, 51)
	f.renders[testRoute+":send/"+hex.EncodeToString(id0[:])] = `{"id":"` + hex.EncodeToString(id0[:]) + `","from":"g1alice","amount":25000,"at":1,"refunded":1800000500}`
	f.renders[testRoute+":send/"+hex.EncodeToString(id1[:])] = `{"id":"` + hex.EncodeToString(id1[:]) + `","from":"g1bob","amount":300,"at":2,"refunded":0}`
	sends := map[string]*sendRec{}
	var next uint32
	now := time.Unix(1_800_000_000, 0)
	// the marks are read for a successor's sends (a refund is a successor's duty over the sends it imported; a live
	// route's are never read, G42)
	succJSON, _ := routeAt(context.Background(), &fakeChain{renders: map[string]string{testRoute + ":json": routeJSONWith(1000, 0, "", false, 1)}}, testRoute, 100)
	if _, err := readSends(context.Background(), f, &notFoundSol{}, cfg, mb, 100, sends, &next, now, sendReads{Routes: map[string]*routeJSON{"BUBBLE": succJSON}, RefundCounts: map[string]int64{}}); err != nil {
		t.Fatal(err)
	}
	if !sends["0"].Refunded || sends["1"].Refunded || sends["0"].InFlight() || !sends["1"].InFlight() {
		t.Fatalf("the refund marks: %+v %+v", sends["0"], sends["1"])
	}
	// the monitor's in-flight sum and the stall count leave the refunded send out
	m := testMonitor(f)
	m.state.Sends = sends
	m.state.LastDelivery = now.Unix()
	m.stalls()
	if m.status.Waiting["gno→solana BUBBLE"] != 1 {
		t.Fatalf("the refunded send counted as waiting: %v", m.status.Waiting)
	}
	inflight := int64(0)
	for _, s := range sends {
		if s.InFlight() {
			inflight += s.Amount
		}
	}
	if inflight != 300 {
		t.Fatalf("in flight %d, want the unrefunded 300 alone", inflight)
	}
	// the relayer refuses the refunded send before staging, and a realm without the record is no delivery
	r := testRelayer(f, "primary")
	r.Cfg = cfg
	rt := &cfg.Routes[0]
	f.renders[testRoute+":json"] = routeJSONWith(1000, 0, "", false, 1) // a successor: its imported sends may be refunded
	g0 := &gnoMsg{Raw: m0.Bytes(), Msg: m0, ID: id0, Height: 50}
	g1 := &gnoMsg{Raw: m1.Bytes(), Msg: m1, ID: id1, Height: 51}
	if refunded, why, err := r.sendRefunded(context.Background(), todo{n: 0, msg: g0, rt: rt}); err != nil || !refunded || !strings.Contains(why, "must never deliver") {
		t.Fatalf("the refunded send: %v %q %v", refunded, why, err)
	}
	if refunded, _, err := r.sendRefunded(context.Background(), todo{n: 1, msg: g1, rt: rt}); err != nil || refunded {
		t.Fatalf("the unrefunded send: %v %v", refunded, err)
	}
	f.renders[testRoute+":send/"+hex.EncodeToString(id1[:])] = "unknown path; see the route's page"
	if _, _, err := r.sendRefunded(context.Background(), todo{n: 1, msg: g1, rt: rt}); err == nil {
		t.Fatal("a realm rendering no send record passed as unrefunded")
	}
	if !doneForReemit("refunded") {
		t.Fatal("a refunded message is re-emitted")
	}
	// the send record of a migrated route lives on its successor (a send never refunded moves with its page)
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(1000, 0, "", false, 1)
	f.renders[testRoute+":send/"+hex.EncodeToString(id1[:])] = "null"
	f.renders[testSucc+":send/"+hex.EncodeToString(id1[:])] = `{"id":"x","from":"g1bob","amount":300,"at":2,"refunded":true}`
	r.routeCache = nil
	if refunded, _, err := r.sendRefunded(context.Background(), todo{n: 1, msg: g1, rt: rt}); err != nil || !refunded {
		t.Fatalf("the successor's refund mark was not read: %v %v", refunded, err)
	}
}

// AQ3 and K28: while a migration is open the token is judged over the pair's summed counters — the predecessor
// alone (lock 0 from page 0) or the successor alone would read BROKEN — the successor mid-import is named,
// and a configured successor finds its predecessor among the mailbox's registered routes.
func TestMigratingTokenIsJudgedOverThePair(t *testing.T) {
	f := newFake()
	ctx := context.Background()
	r := Route{Symbol: "BUBBLE", Gno: testRoute, Token: "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"}
	mb := &mailboxJSON{Routes: map[string]routeFlags{testRoute: {}, testSucc: {}}}
	// page 0 moved the counters: the predecessor shows 0, the successor the whole lock, importing
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	f.renders[testSucc+":json"] = routeJSONWith(1_000, 200, "", true, 1)
	pj, _ := routeAt(ctx, f, testRoute, 100)
	mig, err := migrationOf(ctx, routeReaderAt(f, 100), r, pj, mb)
	if err != nil || mig == nil || mig.Successor != testSucc || !mig.Importing() || mig.Ended() {
		t.Fatalf("the migration from the predecessor's side: %+v %v", mig, err)
	}
	if !strings.Contains(mig.Text(), "IMPORTING (1 pages so far)") {
		t.Fatalf("the text: %s", mig.Text())
	}
	alone := identityOf(r, pj, nil, 100, 800, 7, 0, 0)
	if v, _ := alone.Verdict(); v != Broken {
		t.Fatal("the predecessor alone did not read BROKEN (the test's premise)")
	}
	pair := identityOf(r, pj, mig, 100, 800, 7, 0, 0)
	if v, text := pair.Verdict(); v != Holds || pair.Lock != 1_000 || pair.Owed != 200 {
		t.Fatalf("the pair: %s %s", v, text)
	}
	// the successor configured instead: its predecessor is the registered route whose migratedTo names it
	s := Route{Symbol: "BUBBLE", Gno: testSucc, Token: r.Token}
	sj, _ := routeAt(ctx, f, testSucc, 100)
	mig2, err := migrationOf(ctx, routeReaderAt(f, 100), s, sj, mb)
	if err != nil || mig2 == nil || mig2.Predecessor != testRoute || mig2.Successor != testSucc {
		t.Fatalf("the migration from the successor's side: %+v %v", mig2, err)
	}
	// the config may name it, without the walk
	s.Predecessor = testRoute
	if mig3, err := migrationOf(ctx, routeReaderAt(f, 100), s, sj, nil); err != nil || mig3 == nil || mig3.Predecessor != testRoute {
		t.Fatalf("the configured predecessor: %+v %v", mig3, err)
	}
	// the mailbox's own entries (M1g: AddRoute records them at the migrate's apply) name the pair without a walk
	// or a config — read first, the predecessor's migratedTo beside them
	mbm := &mailboxJSON{Routes: map[string]routeFlags{testRoute: {Successor: testSucc}, testSucc: {Predecessor: testRoute}}}
	s.Predecessor = ""
	if mig4, err := migrationOf(ctx, routeReaderAt(f, 100), s, sj, mbm); err != nil || mig4 == nil || mig4.Predecessor != testRoute {
		t.Fatalf("the mailbox's predecessor entry: %+v %v", mig4, err)
	}
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, "", false, 0) // a predecessor render from before migratedTo: the mailbox's entry stands in
	pj0, _ := routeAt(ctx, f, testRoute, 100)
	if mig5, err := migrationOf(ctx, routeReaderAt(f, 100), r, pj0, mbm); err != nil || mig5 == nil || mig5.Successor != testSucc {
		t.Fatalf("the mailbox's successor entry: %+v %v", mig5, err)
	}
	f.renders[testRoute+":json"] = routeJSONWith(0, 0, testSucc, false, 0)
	// the import ended: still one token over the pair (the predecessor's counters are final at 0)
	f.renders[testSucc+":json"] = routeJSONWith(1_000, 200, "", false, 3)
	sj, _ = routeAt(ctx, f, testSucc, 100)
	mig, _ = migrationOf(ctx, routeReaderAt(f, 100), r, pj, mb)
	if !mig.Ended() || !strings.Contains(mig.Text(), "the import ended (3 pages)") {
		t.Fatalf("after the last page: %s", mig.Text())
	}
	// a live route has no migration; a route the config calls a successor's predecessor without the chain
	// saying so is judged alone
	f.renders[testRoute+":json"] = routeJSONWith(1_000, 0, "", false, 0)
	lj, _ := routeAt(ctx, f, testRoute, 100)
	if mig, err := migrationOf(ctx, routeReaderAt(f, 100), Route{Symbol: "BUBBLE", Gno: testRoute, Successor: testSucc}, lj, mb); err != nil || mig != nil {
		t.Fatalf("a live route has a migration: %+v %v", mig, err)
	}
	// the monitor: the pair's verdict holds, the successor is the realm its flags go on, and a successor
	// mid-import alarms route:importing (K28)
	m := testMonitor(f)
	m.migs = map[string]*migration{"BUBBLE": {Symbol: "BUBBLE", Predecessor: testRoute, Successor: testSucc, Pred: pj, Succ: sj}}
	mbf := mailboxWith()
	mbf.Routes[testSucc] = routeFlags{}
	m.seat = 5
	m.judge(ctx, r, mbf, identityOf(r, pj, m.migs["BUBBLE"], 100, 800, 7, 0, 0))
	if n := len(f.callsOf(testMailbox + ".SetFlag")); n != 0 {
		t.Fatalf("a held pair was flagged: %d", n)
	}
	m.judge(ctx, r, mbf, identityOf(r, pj, m.migs["BUBBLE"], 100, 5_000, 7, 0, 0))
	calls := f.callsOf(testMailbox + ".SetFlag")
	if len(calls) != 2 || !strings.Contains(calls[0], testSucc+",credits") || !strings.Contains(calls[1], testSucc+",payouts") {
		t.Fatalf("a broken pair did not flag the live realm (the successor): %v", calls)
	}
	if entry := m.status.Identity["BUBBLE"].(map[string]any); !strings.Contains(entry["migration"].(string), "migrating "+testRoute+" → "+testSucc) {
		t.Fatalf("the status names no migration: %v", entry)
	}
	if paths := m.liveRoutes(); len(paths) != 2 || paths[1] != testSucc {
		t.Fatalf("the live routes: %v", paths)
	}
}

// AQ4 (and G35's cause field): a park's cause is its own field, kept through a transient retry; a park whose
// amount exceeds the live per-message maximum or day cap after a lowering kind 12 reserves nothing and is
// re-marked OverMax, so younger sends of the token go.
func TestLoweredCapReMarksThePark(t *testing.T) {
	now := time.Unix(1_000, 0)
	a := &attempt{}
	for i := 0; i < 3; i++ {
		a.fail(now, errors.New("send: InstructionError(1, Custom(2003))"), true, "pausable ISM: OverDayCap")
	}
	if !overDayCap(a) || a.Cause != "pausable ISM: OverDayCap" {
		t.Fatalf("not an OverDayCap park: %+v", a)
	}
	a.fail(now.Add(time.Hour), errors.New("dial tcp: connection refused"), false)
	if !overDayCap(a) {
		t.Fatal("a transient retry dropped the park's cause (the reservation would lapse)")
	}
	old := &attempt{Parked: now, LastErr: "pausable ISM: OverDayCap: send: …"}
	if !overDayCap(old) {
		t.Fatal("a state file from before the cause field is not read by its last error")
	}
	rt := &Route{Symbol: "BUBBLE"}
	msg := func(amount uint64) *gnoMsg {
		return &gnoMsg{Msg: hl.Message{Body: hl.TokenMessage([32]byte{1}, amount, nil)}}
	}
	parked, younger := todo{n: 5, rt: rt, msg: msg(25_000)}, todo{n: 8, rt: rt, msg: msg(90)}
	attempts := map[string]*attempt{"deliver:5": {Parked: now, Cause: "pausable ISM: OverDayCap"}}
	parks := capParks([]todo{parked, younger}, attempts)
	if parks[1] == nil || parks[1].n != 5 || parks[1].tok != 1 {
		t.Fatalf("the park: %+v", parks[1])
	}
	// the limits as they stand: the park fits, the younger send is held behind it (AP2)
	limits := &sol.PausableState{Limits: [2]sol.MintLimit{{MaxPerMessage: 25_000, DayCap: 100_000}, {MaxPerMessage: 25_000, DayCap: 100_000, Used: 100_000, UsedAt: 1_000}}}
	if over := overLimitParks(parks, limits); len(over) != 0 {
		t.Fatalf("a park within the limits was re-marked: %+v", over)
	}
	if ok, _ := capGate(younger.n, 90, false, parks[1], limits.Limits[1], 1_000+8_640, 86_400, 0); ok {
		t.Fatal("the younger send went past a park that fits the limits")
	}
	// a lowering kind 12 to 5,000 a message and 10,000 a day: the park can never go, reserves nothing
	limits.Limits[1].MaxPerMessage, limits.Limits[1].DayCap = 5_000, 10_000
	over := overLimitParks(parks, limits)
	if len(over) != 1 || over[0].n != 5 {
		t.Fatalf("the park above the lowered limits was not found: %+v", over)
	}
	parks[1] = nil
	if ok, _ := capGate(younger.n, 90, false, parks[1], limits.Limits[1], 1_000+86_400, 86_400, 0); !ok {
		t.Fatal("the younger send was held behind a park that can never go")
	}
	attempts["deliver:5"].Cause = overMaxByLimits
	if overDayCap(attempts["deliver:5"]) || !attempts["deliver:5"].causeIs("OverMax") {
		t.Fatalf("the re-marked park: %+v", attempts["deliver:5"])
	}
	if parks := capParks([]todo{parked, younger}, attempts); parks[1] != nil {
		t.Fatal("a re-marked park reserved on the next pass")
	}
}

// adminStateBytes is the admin program's state account in M2j's layout: the seats given (the rest empty), no
// flags, an optional successor, no cancelled range, no holds, nothing pending, a predecessor and whether it
// inherited.
func adminStateBytes(seats []solana.PublicKey, successor *solana.PublicKey, predecessor solana.PublicKey, inherited byte) []byte {
	b := []byte{1, 8, 1}
	b = append(b, 0, 0, 0, 0)         // origin
	b = append(b, make([]byte, 8)...) // last seq
	for i := 0; i < 6; i++ {
		if i < len(seats) {
			b = append(b, seats[i][:]...)
		} else {
			b = append(b, make([]byte, 32)...)
		}
	}
	b = append(b, make([]byte, 8)...) // six flags, holdSince, dipSince: none
	if successor == nil {
		b = append(b, 0)
	} else {
		b = append(append(b, 1), successor[:]...)
	}
	b = append(b, 254)        // the bump
	b = append(b, 0, 0, 0, 0) // no cancelled range
	b = append(b, 0, 0, 0, 0) // no holds
	b = append(b, 0, 0, 0, 0) // pending
	b = append(b, predecessor[:]...)
	return append(b, inherited)
}

// fakeAccounts is a Solana reader serving accounts from a table (a missing one is not found).
type fakeAccounts struct {
	fakeSol
	accounts map[solana.PublicKey]*rpc.Account
}

func (f *fakeAccounts) GetAccountInfoWithOpts(_ context.Context, k solana.PublicKey, _ *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error) {
	a, ok := f.accounts[k]
	if !ok {
		return nil, rpc.ErrNotFound
	}
	return &rpc.GetAccountInfoResult{RPCContext: rpc.RPCContext{Context: rpc.Context{Slot: 7}}, Value: a}, nil
}

func account(owner solana.PublicKey, data []byte) *rpc.Account {
	return &rpc.Account{Owner: owner, Data: rpc.DataBytesOrJSONFromBytes(data)}
}

// AQ6 and G31: the tools follow the admin program's successor on the chain from the configured id — the live
// program is what the relayer classifies and delivers to, what the one-stroke flag marks (the retired state
// too, while it still holds the key's seat), what hlrecover's facts name; a decision to a program the line does
// not reach is left undone and alarmed, never "other"; a loop or a line past four hops is refused.
func TestToolsFollowTheSuccessor(t *testing.T) {
	a := solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	b := solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	c := solana.MustPublicKeyFromBase58("noopb9bkMVfRPU8AsbpTUg8AQkHtKwMYZiFUjNRtMmV")
	watch := solana.MustPublicKeyFromBase58("ComputeBudget111111111111111111111111111111")
	f := &fakeAccounts{accounts: map[solana.PublicKey]*rpc.Account{
		sol.Admin{Program: a}.State(): account(a, adminStateBytes([]solana.PublicKey{{}, {}, {}, {}, watch}, &b, solana.PublicKey{}, 0)),
		sol.Admin{Program: b}.State(): account(b, adminStateBytes([]solana.PublicKey{{}, {}, {}, {}, watch}, nil, a, 1)),
	}}
	ctx := context.Background()
	chain, err := followSuccessors(ctx, f, sol.Admin{Program: a}, rpc.CommitmentFinalized)
	if err != nil || !chain.Live.Program.Equals(b) || len(chain.Retired) != 1 || !chain.Retired[0].Admin.Program.Equals(a) || chain.State.Inherited != 1 {
		t.Fatalf("the line: %+v %v", chain, err)
	}
	if !chain.Knows(a) || !chain.Knows(b) || chain.Knows(c) || !chain.IsRetired(a) || chain.IsLive(a) || !chain.IsLive(b) {
		t.Fatal("the line's membership")
	}
	if !strings.Contains(chain.Text(), "the admin program is "+b.String()) || !strings.Contains(chain.Text(), a.String()) {
		t.Fatalf("the text: %s", chain.Text())
	}
	cfg := &Config{}
	cfg.Solana.Client, cfg.Solana.Pausable, cfg.Solana.Admin = "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU", "9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr", a.String()
	progs := cfg.Programs()
	if !chain.Programs(progs).Admin.Equals(b) {
		t.Fatal("the live programs do not name the successor")
	}
	// the one-stroke flag: the pausable ISM and the live program first, the retired one holding the seat after
	first, retired := strokeIxs(progs, chain, watch, true, true)
	if len(first) != 2 || !first[0].ProgramID().Equals(progs.Pausable) || !first[1].ProgramID().Equals(b) {
		t.Fatalf("the stroke: %d instructions to %s, %s", len(first), first[0].ProgramID(), first[1].ProgramID())
	}
	if len(retired) != 1 || !retired[0].ProgramID().Equals(a) {
		t.Fatalf("the retired program's flag: %v", retired)
	}
	if first, retired := strokeIxs(progs, chain, c, true, true); len(first) != 2 || len(retired) != 0 {
		t.Fatal("a key with no seat on the retired program was sent there")
	}
	if first, _ := strokeIxs(progs, chain, watch, true, false); len(first) != 1 {
		t.Fatal("a pausable-only stroke touched the admin program")
	}
	// the relayer classifies by the line: r/hlgov's decision to the live or the retired program is a decision,
	// to a stranger undone (G31), a stranger's message to the live program nobody's
	fc := newFake()
	r := testRelayer(fc, "primary")
	r.Cfg.Gno.Gov = "gno.land/r/test/hlgov"
	r.Cfg.Solana.Admin = a.String()
	r.admins = chain
	gov, _ := r.Cfg.GovSender()
	msg := func(sender [32]byte, rcpt solana.PublicKey) *gnoMsg {
		return &gnoMsg{Msg: hl.Message{Version: 3, Origin: hl.DomainGnoTest, Sender: sender, Destination: hl.DomainSolanaTestnet, Recipient: [32]byte(rcpt), Body: []byte{7, 0, 0, 0, 0, 0, 0, 0, 1}}}
	}
	if k, _ := r.classify(msg(gov, b)); k != kindDecision {
		t.Fatal("a decision to the successor")
	}
	if k, _ := r.classify(msg(gov, a)); k != kindDecision {
		t.Fatal("a decision to the retired program (dead there, AP3)")
	}
	if k, _ := r.classify(msg(gov, c)); k != kindOtherAdmin {
		t.Fatal("a decision to a stranger's program was walked past as other (G31)")
	}
	if k, _ := r.classify(msg([32]byte{1}, b)); k != kindOther {
		t.Fatal("a stranger's message to the successor")
	}
	// before any read, the configured program alone is the line
	r.admins = nil
	if k, _ := r.classify(msg(gov, a)); k != kindDecision {
		t.Fatal("the configured program before a read")
	}
	if k, _ := r.classify(msg(gov, b)); k != kindOtherAdmin {
		t.Fatal("an unread successor was classed as a decision")
	}
	// hlrecover's facts name the live program; a loop and a long line are refused
	f.accounts[sol.Admin{Program: b}.State()] = account(b, adminStateBytes(nil, &a, a, 1))
	if _, err := followSuccessors(ctx, f, sol.Admin{Program: a}, rpc.CommitmentFinalized); err == nil || !strings.Contains(err.Error(), "loops") {
		t.Fatalf("a loop was followed: %v", err)
	}
	keys := []solana.PublicKey{a, b, c, solana.MustPublicKeyFromBase58("Vote111111111111111111111111111111111111111"), solana.MustPublicKeyFromBase58("Stake11111111111111111111111111111111111111"), solana.MustPublicKeyFromBase58("BPFLoaderUpgradeab1e11111111111111111111111")}
	for i := range keys {
		var succ *solana.PublicKey
		if i+1 < len(keys) {
			succ = &keys[i+1]
		}
		f.accounts[sol.Admin{Program: keys[i]}.State()] = account(keys[i], adminStateBytes(nil, succ, solana.PublicKey{}, 1))
	}
	if _, err := followSuccessors(ctx, f, sol.Admin{Program: a}, rpc.CommitmentFinalized); err == nil || !strings.Contains(err.Error(), "runs past 4 hops") {
		t.Fatalf("a line past the bound was followed: %v", err)
	}
	f.accounts[sol.Admin{Program: c}.State()] = account(c, adminStateBytes(nil, nil, b, 1))
	chain, err = followSuccessors(ctx, f, sol.Admin{Program: a}, rpc.CommitmentFinalized)
	if err != nil || !chain.Live.Program.Equals(c) || len(chain.Retired) != 2 {
		t.Fatalf("a line of two hops: %+v %v", chain, err)
	}
}

// C341: mint:cap-ratio is quiet while a token's lock is still below the expected first-week lock it was set
// against, judges the cap from the poll the lock reaches it and for good after (a lock that shrinks below five
// caps alarms), and with no expected lock configured judges from the first poll as before; the monitor keeps
// the token's standing and posts the alarm daily.
func TestCapRatioQuietUnderTheExpectedLock(t *testing.T) {
	cap := uint64(100_000)
	if v := capRatio("BUBBLE", cap, 20_000, 1_000_000, false, 0, 0); v.Alarm != "" || v.Reached || !strings.Contains(v.Note, "below the expected first-week lock of 1000000") {
		t.Fatalf("under the expected lock: %+v", v)
	}
	if v := capRatio("BUBBLE", cap, 1_000_000, 1_000_000, false, 0, 0); v.Alarm != "" || !v.Reached || v.Note != "" {
		t.Fatalf("at the expected lock: %+v", v)
	}
	if v := capRatio("BUBBLE", cap, 1_000_001, 1_000_000, false, 0, 0); v.Alarm != "" || !v.Reached {
		t.Fatalf("above the expected lock: %+v", v)
	}
	if v := capRatio("BUBBLE", cap, 400_000, 1_000_000, true, 0, 0); !strings.Contains(v.Alarm, "above a fifth of its lock on gno.land, 400000") || !v.Reached {
		t.Fatalf("a lock that shrank below five caps: %+v", v)
	}
	if v := capRatio("BUBBLE", cap, 400_000, 0, false, 0, 0); v.Alarm == "" || !v.Reached {
		t.Fatalf("no expected lock configured: %+v", v)
	}
	if v := capRatio("BUBBLE", cap, 600_000, 1_000_000, true, 0, 0); v.Alarm != "" {
		t.Fatalf("a cap within a fifth of a reached lock: %+v", v)
	}
	f := newFake()
	m := testMonitor(f)
	m.Cfg.Routes[0].ExpectedLock = 1_000_000
	m.Alarm.Repeat = time.Nanosecond
	ps := &sol.PausableState{Limits: [2]sol.MintLimit{{DayCap: cap}, {DayCap: cap}}}
	routes := map[string]*routeJSON{"BUBBLE": {Lock: 20_000}}
	m.capRatios(ps, routes, nil)
	if _, ok := m.Alarm.Standing()["mint:cap-ratio:BUBBLE"]; ok || m.state.CapReached["BUBBLE"] {
		t.Fatalf("alarmed under the expected lock: %v", m.Alarm.Standing())
	}
	routes["BUBBLE"].Lock = 1_000_000
	m.capRatios(ps, routes, nil)
	if _, ok := m.Alarm.Standing()["mint:cap-ratio:BUBBLE"]; ok || !m.state.CapReached["BUBBLE"] {
		t.Fatalf("at the expected lock: %v %v", m.Alarm.Standing(), m.state.CapReached)
	}
	routes["BUBBLE"].Lock = 300_000
	m.capRatios(ps, routes, nil)
	if _, ok := m.Alarm.Standing()["mint:cap-ratio:BUBBLE"]; !ok {
		t.Fatal("a lock that shrank below five caps did not alarm once reached")
	}
	// posted daily, not hourly (G35): a second raise within the day moves the posting time not at all
	m.Alarm.mu.Lock()
	first := m.Alarm.standing["mint:cap-ratio:BUBBLE"]
	m.Alarm.mu.Unlock()
	time.Sleep(time.Millisecond)
	m.capRatios(ps, routes, nil)
	m.Alarm.mu.Lock()
	again := m.Alarm.standing["mint:cap-ratio:BUBBLE"]
	m.Alarm.mu.Unlock()
	if !again.Equal(first) {
		t.Fatal("the cap-ratio alarm was posted again inside its day")
	}
	// the pair's lock while a migration is open (AQ3)
	m.state.CapReached["BUBBLE"] = false
	m.migs = map[string]*migration{"BUBBLE": {Symbol: "BUBBLE", Pred: &routeJSON{Lock: 0}, Succ: &routeJSON{Lock: 1_000_000}}}
	routes["BUBBLE"].Lock = 0
	m.Alarm.Clear("mint:cap-ratio:BUBBLE")
	m.capRatios(ps, routes, nil)
	if _, ok := m.Alarm.Standing()["mint:cap-ratio:BUBBLE"]; ok || !m.state.CapReached["BUBBLE"] {
		t.Fatalf("the pair's summed lock was not read: %v %v", m.Alarm.Standing(), m.state.CapReached)
	}
}

// versionFake records the transaction version the reads pin, serving one transaction.
type versionFake struct {
	fakeSol
	pinned *uint64
	refuse bool
}

func (f *versionFake) GetTransaction(ctx context.Context, sig solana.Signature, opts *rpc.GetTransactionOpts) (*rpc.GetTransactionResult, error) {
	f.pinned = opts.MaxSupportedTransactionVersion
	if f.refuse {
		return nil, errors.New(`(-32015) Transaction version (1) is not supported by the requesting client. Please try the request again with the following configuration parameter: "maxSupportedTransactionVersion": 1`)
	}
	return f.fakeSol.GetTransaction(ctx, sig, opts)
}

// G28: every transaction read pins the RPC to version 1, a v1-shaped transaction folds into the record, and a
// provider's "not supported by the requesting client" is named as a build gap, never a transient.
func TestReadsAVersionedTransaction(t *testing.T) {
	client := sol.LightClient{Program: solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")}
	mailbox := solana.MustPublicKeyFromBase58("9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr")
	payer := solana.NewWallet()
	tx, err := solana.NewTransaction([]solana.Instruction{client.InitIx(payer.PublicKey(), payer.PublicKey(), "dev", 1, 100, [32]byte{1}, nil, payer.PublicKey())}, solana.Hash{1}, solana.TransactionPayer(payer.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Message.SetVersion(solana.MessageVersionV1); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Sign(func(k solana.PublicKey) *solana.PrivateKey {
		if k.Equals(payer.PublicKey()) {
			return &payer.PrivateKey
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if raw[0] != 0x81 { // the v1 envelope: the prefixed message first, then the signatures (SIMD-0385)
		t.Fatalf("the test's transaction is not a v1 one: first byte %#x", raw[0])
	}
	var env rpc.TransactionResultEnvelope
	if err := json.Unmarshal([]byte(fmt.Sprintf(`[%q,"base64"]`, base64.StdEncoding.EncodeToString(raw))), &env); err != nil {
		t.Fatal(err)
	}
	sig := tx.Signatures[0]
	f := &versionFake{fakeSol: fakeSol{txs: map[string]*rpc.GetTransactionResult{sig.String(): {Slot: 10, Transaction: &env, Meta: &rpc.TransactionMeta{}}}, sigs: []*rpc.TransactionSignature{{Signature: sig, Slot: 10}}}}
	var r ClientRecord
	if err := r.Sync(context.Background(), f, client, mailbox, 50); err != nil {
		t.Fatalf("a v1 transaction was not read: %v", err)
	}
	if r.Count != 1 || f.pinned == nil || *f.pinned != 1 {
		t.Fatalf("folded %d entries under a pin of %v (want 1 under 1)", r.Count, f.pinned)
	}
	if _, err := solDispatchesIn(context.Background(), f, sol.Mailbox{Program: mailbox}, sig); err != nil && !strings.Contains(err.Error(), "not in the fake") {
		t.Fatalf("the dispatch scan's read of a v1 transaction: %v", err)
	}
	f.refuse = true
	_, _, err = transactionInstructions(context.Background(), f, sig)
	if err == nil || !strings.Contains(err.Error(), "BUILD GAP") || !strings.Contains(err.Error(), "a version above 1") {
		t.Fatalf("a refused version is not named as a build gap: %v", err)
	}
	skips := []scanSkip{{Sig: sig.String(), Slot: 10, Err: err.Error(), Tries: 3}}
	if !strings.Contains(skipText(skips), "BUILD GAP") {
		t.Fatal("the skip's alarm text does not name the build gap")
	}
}

// G29: an empty record started from the client's own counter and running hash at a slot passes over everything
// at or below it, folds what lands above, reads as consistent with the client, and keeps the ring the client
// holds; a record holding entries takes no checkpoint; a stalled scan alarms the monitor after two minutes.
func TestRecordStartsFromACheckpoint(t *testing.T) {
	client := sol.LightClient{Program: solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")}
	mailbox := solana.MustPublicKeyFromBase58("9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr")
	f := &fakeSol{txs: map[string]*rpc.GetTransactionResult{}}
	for _, x := range []struct {
		slot   uint64
		height uint64
	}{{10, 100}, {12, 101}, {14, 102}} {
		res, sig := txResult(t, x.slot, func(payer solana.PublicKey) []solana.Instruction {
			return []solana.Instruction{client.InitIx(payer, payer, "dev", 1, x.height, [32]byte{byte(x.height)}, nil, payer)}
		})
		f.txs[sig.String()] = res
		f.sigs = append([]*rpc.TransactionSignature{{Signature: sig, Slot: x.slot}}, f.sigs...)
	}
	// the chain's own record after the first two: what a fresh install reads from the client
	var full ClientRecord
	if err := full.Sync(context.Background(), f, client, mailbox, 12); err != nil || full.Count != 2 {
		t.Fatalf("the full record to slot 12: %d %v", full.Count, err)
	}
	var running [32]byte
	b, _ := hex.DecodeString(full.Hash)
	copy(running[:], b)
	var r ClientRecord
	ring := [][32]byte{{0xaa}, {0xbb}}
	if err := r.StartFrom(2, running, 12, ring, [32]byte{0xcc}); err != nil {
		t.Fatal(err)
	}
	setHash := [32]byte{0xcc}
	if r.Count != 2 || r.FromSlot != 12 || r.ScanSlot != 12 || len(r.Ring) != 2 || !r.ringHas(hex.EncodeToString(ring[1][:])) || r.LastVals != hex.EncodeToString(setHash[:]) {
		t.Fatalf("the checkpoint: %+v", r)
	}
	if err := r.StartFrom(3, running, 20, nil, [32]byte{}); err == nil {
		t.Fatal("a record with a base took a second checkpoint")
	}
	// the scan passes over slots 10 and 12 and folds the reset at 14
	if err := r.Sync(context.Background(), f, client, mailbox, 50); err != nil {
		t.Fatal(err)
	}
	if r.Count != 3 || r.ScanSlot != 50 {
		t.Fatalf("after the scan: %d entries, the scan at %d", r.Count, r.ScanSlot)
	}
	var full3 ClientRecord
	_ = full3.Sync(context.Background(), f, client, mailbox, 50)
	if r.Hash != full3.Hash {
		t.Fatal("the checkpointed record's running hash differs from the full record's")
	}
	b, _ = hex.DecodeString(r.Hash)
	copy(running[:], b)
	if ok, why := r.Consistent(3, running, 50); !ok {
		t.Fatalf("not consistent: %s", why)
	}
	// a stalled scan alarms the monitor once it has stood two minutes (record:scan), cleared by a clean one
	m := testMonitor(newFake())
	m.persist("record:scan", "the client's record scan fails", cutFailAfter)
	if _, ok := m.Alarm.Standing()["record:scan"]; ok {
		t.Fatal("alarmed at first sight")
	}
	now := m.now().Add(cutFailAfter)
	m.now = func() time.Time { return now }
	m.persist("record:scan", "the client's record scan fails", cutFailAfter)
	if _, ok := m.Alarm.Standing()["record:scan"]; !ok {
		t.Fatal("a stalled scan did not alarm after two minutes")
	}
	m.clearPersisted("record:scan")
	if _, ok := m.Alarm.Standing()["record:scan"]; ok {
		t.Fatal("not cleared")
	}
}

// G30: a poll that fails past the cut's own alarms marks the identity stale at once and alarms
// monitor:no-verdict once it has stood two minutes; a clean poll clears both.
func TestMonitorFailedPollAlarms(t *testing.T) {
	m := testMonitor(newFake())
	m.setIdentity("BUBBLE", Identity{Symbol: "BUBBLE"}, "holds", "x")
	m.afterPoll(errors.New("the mailbox: fake: no render"))
	if m.stale == "" {
		t.Fatal("not stale after a failed poll")
	}
	st := m.buildStatus()
	if st["stale"] == "" || st["identity"].(map[string]any)["BUBBLE"].(map[string]any)["stale"] == nil {
		t.Fatalf("the status does not mark the identity stale: %v", st)
	}
	if _, ok := m.Alarm.Standing()["monitor:no-verdict"]; ok {
		t.Fatal("alarmed at first sight")
	}
	now := m.now().Add(cutFailAfter)
	m.now = func() time.Time { return now }
	m.afterPoll(errors.New("the mailbox: fake: no render"))
	if a, ok := m.Alarm.Standing()["monitor:no-verdict"]; !ok || !strings.Contains(a, "no verdict: the poll fails") {
		t.Fatalf("no alarm after two minutes: %v", m.Alarm.Standing())
	}
	m.afterPoll(nil)
	if _, ok := m.Alarm.Standing()["monitor:no-verdict"]; ok || m.stale != "" {
		t.Fatal("a clean poll did not clear the alarm and the stale mark")
	}
	if st := m.buildStatus(); st["identity"].(map[string]any)["BUBBLE"].(map[string]any)["stale"] != nil {
		t.Fatal("the identity stays stale after a clean poll")
	}
}

// G32: a 7-day decision of r/hlgov's whose account the admin program no longer holds inside its week is a
// foreign finding (a cancel took its number), unless the state still holds its cancelled range or a later
// cancel of r/hlgov's names it; past its week, or a cancel body (closed at recording), it is nothing.
func TestForeignFindingsSeesAGoneSevenDayDecision(t *testing.T) {
	f := newFake()
	cfg := &Config{}
	cfg.Gno.Gov = "gno.land/r/test/hlgov"
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute}}
	admin := sol.Admin{Program: solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")}
	none := &fakeAccounts{accounts: map[solana.PublicKey]*rpc.Account{}} // no decision account stands
	seats := make([]byte, 0, 6*32)
	for i := 0; i < 6; i++ {
		seats = append(seats, make([]byte, 32)...)
	}
	params := append([]byte{1}, seats[:32]...) // a kind 5: one seat change
	render := func(n uint64, kind byte, params []byte, at int64) string {
		return fmt.Sprintf(`{"number":%d,"kind":%d,"body":"%s","id":"x","count":1,"at":%d}`, n, kind, hex.EncodeToString(sol.DecisionBody(kind, n, params)), at)
	}
	at := int64(1_800_000_000)
	f.renders[cfg.Gno.Gov+":decision/3"] = render(3, sol.KindSetGuardians, params, at)
	st := &sol.AdminState{ClockFactor: 1}
	findings, checked, err := foreignFindings(context.Background(), none, f, cfg, admin, st, 100, 2, 3, 3, at+3600, foreignCtx{})
	if err != nil || checked != 3 || len(findings) != 1 || !strings.Contains(findings[0], "number 3's decision (kind 5) is gone though r/hlgov's matures in 7 days") {
		t.Fatalf("a gone decision inside its week: %v %d %v", findings, checked, err)
	}
	// past its week: executed, nothing to say
	if findings, _, _ := foreignFindings(context.Background(), none, f, cfg, admin, st, 100, 2, 3, 3, at+8*86_400, foreignCtx{}); len(findings) != 0 {
		t.Fatalf("a decision past its week was called gone: %v", findings)
	}
	// the state still holds its cancelled range: GovDAO's own cancel, no finding
	cancelled := &sol.AdminState{ClockFactor: 1, Cancelled: []sol.CancelledRange{{From: 3, To: 3}}}
	if findings, _, _ := foreignFindings(context.Background(), none, f, cfg, admin, cancelled, 100, 2, 3, 3, at+3600, foreignCtx{}); len(findings) != 0 {
		t.Fatalf("a decision in a cancelled range was called gone: %v", findings)
	}
	// a later cancel of r/hlgov's names it: the range pruned, the cancel's body says why
	cancelParams := func(from, to uint64) []byte { // the salt, the range form, the channel byte (0), from, to (big-endian)
		b := make([]byte, 8)
		b = append(b, 1, 0)
		for _, n := range []uint64{from, to} {
			var v [8]byte
			for i := 0; i < 8; i++ {
				v[7-i] = byte(n >> (8 * i))
			}
			b = append(b, v[:]...)
		}
		return b
	}
	p, err := sol.ParseParams(sol.KindCancel, cancelParams(3, 3))
	if err != nil || len(p.Cancel) != 1 || p.Cancel[0].From != 3 {
		t.Fatalf("the test's cancel form does not parse as a range of 3: %+v %v", p, err)
	}
	f.renders[cfg.Gno.Gov+":decision/4"] = render(4, sol.KindCancel, cancelParams(3, 3), at+10)
	if findings, checked, err := foreignFindings(context.Background(), none, f, cfg, admin, st, 100, 2, 4, 4, at+3600, foreignCtx{}); err != nil || checked != 4 || len(findings) != 0 {
		t.Fatalf("a decision r/hlgov's own cancel closed was called gone: %v %d %v", findings, checked, err)
	}
	// a cancel at 4 of another number leaves 3 gone
	f.renders[cfg.Gno.Gov+":decision/4"] = render(4, sol.KindCancel, cancelParams(1, 1), at+10)
	if findings, _, _ := foreignFindings(context.Background(), none, f, cfg, admin, st, 100, 2, 4, 4, at+3600, foreignCtx{}); len(findings) != 1 {
		t.Fatalf("a cancel of another number hid the gone decision: %v", findings)
	}
}

// G33: a FORGED CREDIT line is printed on an id's second consecutive poll — the first sighting kept in the state
// — and an id that leaves the candidates is forgotten.
func TestForgedCreditWaitsASecondPoll(t *testing.T) {
	seen := map[string]int64{}
	if out := forgedNow(seen, []string{"aa", "bb"}, 1); len(out) != 0 || len(seen) != 2 {
		t.Fatalf("printed at first sight: %v %v", out, seen)
	}
	if out := forgedNow(seen, []string{"aa", "bb", "cc"}, 2); len(out) != 2 || out[0] != "aa" || out[1] != "bb" || seen["cc"] != 2 {
		t.Fatalf("the second poll: %v %v", out, seen)
	}
	if out := forgedNow(seen, []string{"cc"}, 3); len(out) != 1 || out[0] != "cc" || len(seen) != 1 {
		t.Fatalf("an indexed id was kept, or the standing one dropped: %v %v", out, seen)
	}
	if out := forgedNow(seen, nil, 4); len(out) != 0 || len(seen) != 0 {
		t.Fatalf("nothing left: %v %v", out, seen)
	}
}

// G34: the claims walk reads ten pages from where the last poll stopped and wraps at the end, so every waiting
// claim is visited within ⌈N/1,000⌉ polls; a short walk from 0 is complete.
func TestClaimsWalkResumesAndWraps(t *testing.T) {
	f := newFake()
	ctx := context.Background()
	const n = 1_250
	page := func(from int) string {
		var parts []string
		for i := from; i < from+claimsPage && i < n; i++ {
			parts = append(parts, fmt.Sprintf(`{"id":"%04x","to":"g1","amount":1,"paid":0,"state":"open","by":"signers","at":1,"height":1,"seq":%d}`, i, i))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	for from := 0; from <= n; from += claimsPage {
		f.renders[testRoute+":claims/"+strconv.Itoa(from)] = page(from)
	}
	visited := map[string]int{}
	from := 0
	for poll := 1; poll <= 2; poll++ {
		claims, next, complete, err := openClaims(ctx, f, testRoute, 100, from)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range claims {
			visited[c.ID] = poll
		}
		if poll == 1 && (len(claims) != 1_000 || next != 1_000 || complete) {
			t.Fatalf("the first poll: %d claims, next %d, complete %v", len(claims), next, complete)
		}
		if poll == 2 && (len(claims) != 950 || next != 700 || complete) {
			t.Fatalf("the second poll: %d claims, next %d, complete %v (the 250 past 1,000 in three pages, then seven pages from 0)", len(claims), next, complete)
		}
		from = next
	}
	if len(visited) != n {
		t.Fatalf("%d of %d claims visited within two polls", len(visited), n)
	}
	for i := 1_000; i < n; i++ {
		if visited[fmt.Sprintf("%04x", i)] != 2 {
			t.Fatalf("claim %d behind the first thousand was not visited on the second poll", i)
		}
	}
	// a short list from 0 is read whole
	for from := 0; from <= n; from += claimsPage {
		delete(f.renders, testRoute+":claims/"+strconv.Itoa(from))
	}
	f.renders[testRoute+":claims/0"] = page(1_200)
	claims, next, complete, err := openClaims(ctx, f, testRoute, 100, 0)
	if err != nil || len(claims) != 50 || next != 0 || !complete {
		t.Fatalf("a short list: %d %d %v %v", len(claims), next, complete, err)
	}
	// the monitor keeps the offset per realm
	m := testMonitor(f)
	m.state.Index["0000"] = &dispatchRec{Symbol: "BUBBLE"}
	f.renders[testRoute+":claims/0"] = page(0)
	f.renders[testRoute+":claims/100"] = "[]"
	if err := m.unindexedCredits(ctx, mailboxWith(), 100); err != nil {
		t.Fatal(err)
	}
	if m.state.ClaimsFrom[testRoute] != 0 {
		t.Fatalf("the offset after a complete walk: %d", m.state.ClaimsFrom[testRoute])
	}
}

// G35: the alarms that outlived their cure end with it — an unindexed credit's flag alarm once the claim is
// voided or gone, a diverted credit's likewise, a Retired copy's once the decision is re-dispatched — and the
// own node's lag is raised where it is cleared.
func TestAlarmsClearWithTheirCure(t *testing.T) {
	f := newFake()
	m := testMonitor(f)
	ctx := context.Background()
	mb := mailboxWith()
	m.seat = 5
	claim := func(id, state string) string {
		return `{"id":"` + id + `","to":"g1eve","amount":500,"paid":0,"state":"` + state + `","by":"signers","at":1,"height":90,"seq":1}`
	}
	f.renders[testRoute+":claims/0"] = "[" + claim("ee", "open") + "]"
	f.renders[testRoute+":claim/ee"] = claim("ee", "open")
	_ = m.unindexedCredits(ctx, mb, 100)
	now := m.now().Add(m.Every)
	m.now = func() time.Time { return now }
	_ = m.unindexedCredits(ctx, mb, 101)
	if _, ok := m.Alarm.Standing()["credit:unindexed:ee:flag"]; !ok {
		t.Fatalf("no flag alarm for the unindexed credit: %v", m.Alarm.Standing())
	}
	// GovDAO voids it: it leaves the waiting list; the alarm and the sighting go
	f.renders[testRoute+":claims/0"] = "[]"
	f.renders[testRoute+":claim/ee"] = claim("ee", "forged")
	_ = m.unindexedCredits(ctx, mb, 102)
	if _, ok := m.Alarm.Standing()["credit:unindexed:ee:flag"]; ok {
		t.Fatal("the flag alarm stands after the void")
	}
	if _, ok := m.seen["credit:unindexed:ee"]; ok {
		t.Fatal("the sighting stands after the void")
	}
	// a diverted credit: flagged, then cleared once the claim is voided
	m.state.Index["bb"] = &dispatchRec{Symbol: "BUBBLE", Slot: 11, To: "g1bob", Amount: 300, Credited: 91}
	f.renders[testRoute+":claim/bb"] = `{"id":"bb","to":"g1mallory","amount":300,"paid":0,"state":"open","by":"signers","at":1,"seq":1}`
	if err := m.divertedCredits(ctx, mb, 100); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Alarm.Standing()["diverted:bb:flag"]; !ok || !m.diverted["bb"] {
		t.Fatalf("no flag alarm for the diverted credit: %v", m.Alarm.Standing())
	}
	_ = m.divertedCredits(ctx, mb, 101)
	if _, ok := m.Alarm.Standing()["diverted:bb:flag"]; !ok {
		t.Fatal("cleared while the claim stands open")
	}
	f.renders[testRoute+":claim/bb"] = `{"id":"bb","to":"g1mallory","amount":300,"paid":0,"state":"forged","by":"signers","at":1,"seq":1}`
	_ = m.divertedCredits(ctx, mb, 102)
	if _, ok := m.Alarm.Standing()["diverted:bb:flag"]; ok || m.diverted["bb"] {
		t.Fatal("the diverted alarm stands after the void")
	}
	// a Retired copy's alarm ends with the re-dispatch
	r := testRelayer(f, "primary")
	r.rememberCopy(7, 4, [32]byte{4})
	r.state.Done["4"] = "retired"
	r.Alarm.Alarm("decision:retired:4", "retired")
	r.rememberCopy(7, 9, [32]byte{9})
	if _, ok := r.Alarm.Standing()["decision:retired:4"]; ok {
		t.Fatal("the retired alarm stands after the re-dispatch")
	}
	// the own node's lag is raised, and cleared by a good reading
	stale := gno.Head{Height: 100, Time: m.now().Add(-ownLagAfter - time.Minute)}
	if m.judgeHead(stale, m.now().Add(-time.Hour)) {
		t.Fatal("a stale head was a reading")
	}
	if _, ok := m.Alarm.Standing()["gno:own-lag"]; !ok {
		t.Fatalf("gno:own-lag not raised: %v", m.Alarm.Standing())
	}
	if !m.judgeHead(gno.Head{Height: 101, Time: m.now()}, m.now().Add(-time.Second)) {
		t.Fatal("a fresh head was no reading")
	}
	if _, ok := m.Alarm.Standing()["gno:own-lag"]; ok {
		t.Fatal("gno:own-lag not cleared")
	}
	// a per-key cadence never shortens the Alarmer's own
	al := &Alarmer{Source: "test", Repeat: time.Hour}
	al.AlarmEvery("k", "x", time.Minute)
	al.mu.Lock()
	first := al.standing["k"]
	al.mu.Unlock()
	al.AlarmEvery("k", "x", time.Minute)
	al.mu.Lock()
	again := al.standing["k"]
	al.mu.Unlock()
	if !again.Equal(first) {
		t.Fatal("a shorter per-key cadence re-posted inside the Alarmer's hour")
	}
}

// F19: a cancel's delivery passes only as many pending pairs as the packet carries — about six with distinct
// deliverers, more with one — never the sixteen the count allows; the program closes the rest by
// CloseCancelled.
func TestCancelPairsFitThePacket(t *testing.T) {
	admin := sol.Admin{Program: solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")}
	progs := sol.Programs{Mailbox: solana.MustPublicKeyFromBase58("E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"), Client: solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"), Pausable: solana.MustPublicKeyFromBase58("9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr"), Admin: admin.Program}
	submitter := solana.NewWallet().PublicKey()
	var distinct, one []sol.CancelPair
	for i := uint64(1); i <= sol.MaxCancel; i++ {
		distinct = append(distinct, sol.CancelPair{Number: i, Deliverer: solana.NewWallet().PublicKey()})
		one = append(one, sol.CancelPair{Number: i, Deliverer: submitter})
	}
	// the relayer's delivery: the mailbox's process with the ISM's and the client's accounts, a cancel body
	gov := [32]byte{7}
	body := sol.DecisionBody(sol.KindCancel, 9, append(make([]byte, 8), 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 16))
	msg := hl.Message{Version: 3, Nonce: 9, Origin: hl.DomainGnoTest, Sender: gov, Destination: hl.DomainSolanaTestnet, Recipient: [32]byte(admin.Program), Body: body}
	raw := msg.Bytes()
	client, mailbox := progs.LightClient(), progs.MailboxProgram()
	clientAccts := client.VerifyAccounts(submitter, raw)
	ismAccts := solana.AccountMetaSlice{solana.Meta(client.State()), solana.Meta(progs.Pausable)}
	metas := admin.HandleMetas(9)
	deliver := func(pairs []sol.CancelPair) bool {
		handle := admin.HandleAccounts(metas, sol.KindCancel, submitter, pairs)
		ix := sol.ProcessIx(mailbox, submitter, submitter.Bytes(), raw, hl.ID(raw), admin.Program, client.Program, ismAccts, clientAccts, handle)
		return sol.Fits(submitter, []solana.Instruction{sol.ComputeLimitIx(400_000), ix})
	}
	if deliver(distinct) {
		t.Fatal("sixteen pairs with distinct deliverers fit the packet (the test's premise)")
	}
	kept := fitPairs(distinct, deliver)
	if len(kept) == 0 || len(kept) > 9 || !deliver(kept) || deliver(distinct[:len(kept)+1]) {
		t.Fatalf("%d pairs kept with distinct deliverers for a delivery; fits %v", len(kept), deliver(kept))
	}
	keptOne := fitPairs(one, deliver)
	if len(keptOne) <= len(kept) || !deliver(keptOne) {
		t.Fatalf("%d pairs kept with one deliverer (distinct: %d)", len(keptOne), len(kept))
	}
	if n := len(fitPairs(distinct, nil)); n != sol.MaxCancel {
		t.Fatalf("no fit given: %d pairs, want the count's %d", n, sol.MaxCancel)
	}
	if n := len(fitPairs(distinct[:3], deliver)); n != 3 {
		t.Fatalf("three pairs were cut: %d", n)
	}
}

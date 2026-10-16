package agent

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

const (
	testMailbox = "gno.land/r/test/eth/hlmailbox"
	testGNOT    = "gno.land/r/test/eth/hlgnot"
	testBUBBLE  = "gno.land/r/test/eth/hlbubble"
	testGov     = "gno.land/r/test/eth/hlgov"
)

var alice = [32]byte{12: 0xaa, 31: 0x01}

func testConfig() *Config {
	cfg := &Config{}
	cfg.Gno.RPC, cfg.Gno.ChainID, cfg.Gno.Mailbox, cfg.Gno.Gov = "http://fake", "dev", testMailbox, testGov
	cfg.Eth.ChainID, cfg.Eth.Domain = 31337, 31337
	cfg.Eth.Mailbox, cfg.Eth.Client, cfg.Eth.Gate, cfg.Eth.Admin = ethMailbox.Hex(), ethClient.Hex(), ethGate.Hex(), ethAdmin.Hex()
	cfg.Eth.Updater = ethKey.Hex() // the fake's one key sends the updates (the tests of the backstop name another)
	cfg.Routes = []Route{{Symbol: "GNOT", Gno: testGNOT, Token: ethGNOT.Hex()}, {Symbol: "BUBBLE", Gno: testBUBBLE, Token: ethBUBBLE.Hex()}}
	// a whole ETH a token: every send's fee covers an update, so a waiting send buys its header at once (the tests of
	// the fee rule itself set their own)
	cfg.Eth.Prices = map[string]float64{"GNOT": 1, "BUBBLE": 1}
	return cfg
}

func routeJSON(fee int64, extra string) string {
	return fmt.Sprintf(`{"live":true,"fee":%d,"lock":1000,"migratedTo":""%s}`, fee, extra)
}

// testWorld is a relayer over a fake gno chain and a fake Ethereum.
type testWorld struct {
	g   *fakeGno
	e   *fakeEth
	r   *Relayer
	now time.Time
}

func newWorld(t *testing.T, trusted int64, role string) *testWorld {
	t.Helper()
	g := newFakeGno(testMailbox, trusted+1)
	g.renders[testGNOT+":json"] = routeJSON(50_000_000, "")
	g.renders[testBUBBLE+":json"] = routeJSON(1_000_000_000, "")
	for _, p := range []string{testMailbox, testGNOT, testBUBBLE, testGov} {
		g.renders[p+":decisions.json"] = "[]"
	}
	e := newFakeEth(g, trusted)
	e.routers[ethGNOT] = gnoside.RealmKey(testGNOT)
	e.routers[ethBUBBLE] = gnoside.RealmKey(testBUBBLE)
	w := &testWorld{g: g, e: e, now: time.Unix(int64(e.now), 0)}
	w.r = &Relayer{Cfg: testConfig(), Role: role, Alarm: &Alarmer{Source: "test"}, Sender: e, Eth: e, gno: g, light: g, blocks: g, runner: g}
	if err := w.r.init(); err != nil {
		t.Fatal(err)
	}
	w.r.now = func() time.Time { return w.now }
	return w
}

func (w *testWorld) advance(d time.Duration) { w.now = w.now.Add(d) }

func (w *testWorld) tick() { w.r.tick(context.Background()) }

func sendMsg(route string, token common.Address, amount uint64) hl.Message {
	return hl.Message{Sender: gnoside.RealmKey(route), Destination: 31337, Recipient: ethx.AddressTo32(token), Body: hl.TokenMessage(alice, amount, nil)}
}

func decisionMsg(admin common.Address, kind byte, seq uint64, params ...byte) hl.Message {
	body := []byte{kind}
	body = binary.BigEndian.AppendUint64(body, seq)
	gov := gnoside.RealmKey(testGov)
	return hl.Message{Sender: gov, Destination: 31337, Recipient: ethx.AddressTo32(admin), Body: append(body, params...)}
}

// A dispatch whose next header the client accepted is delivered by Mailbox.process with the proof under it — the
// fake mailbox checks the metadata against the accepted entry — and ends; a stranger's dispatch to our token, and a
// dispatch to another domain, are not ours and never sent.
func TestRelayerDeliversUnderAnAcceptedHeader(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	stranger := sendMsg("gno.land/r/someone/else", ethGNOT, 9_000)
	w.g.dispatch(106, stranger)
	other := sendMsg(testGNOT, ethGNOT, 7)
	other.Destination = 1399811149
	w.g.dispatch(107, other)
	w.g.head = 110
	w.e.trust(106) // header 106 proves block 105's results
	w.tick()
	if n := len(w.e.sentOf("process")); n != 1 {
		t.Fatalf("%d process calls, want the one of ours", n)
	}
	// delivered, and ended only when the finalized block shows the mark (the next step here)
	if !w.e.delivered[w.g.idOf(0)] || w.r.state.Out.Done["0"] != "" {
		t.Fatalf("not delivered, or ended before the finalized block shows it: %v", w.r.state.Out.Done)
	}
	if w.r.state.Out.Done["1"] != "other" || w.r.state.Out.Done["2"] != "other" {
		t.Fatalf("a stranger's or another domain's message was taken as ours: %v", w.r.state.Out.Done)
	}
	if c := w.e.sentOf("process")[0]; c.MaxBaseFee != nil {
		t.Fatal("a route delivery went under the emergency cap")
	}
	w.tick()
	if w.r.state.Out.Done["0"] != "delivered" {
		t.Fatalf("not ended once final: %v", w.r.state.Out.Done)
	}
	w.tick()
	if w.r.state.Out.Next != 3 || len(w.e.sentOf("process")) != 1 {
		t.Fatalf("the walk did not move past the ended messages: next %d", w.r.state.Out.Next)
	}
}

// Messages whose headers no update covers wait; when they are split over blocks and a header is due, they are
// re-emitted in one MsgRun — every one, oldest first — and delivered once the header after it is accepted; a send
// delivered already is never sent again ("already delivered" is success, and its mark ends it first).
func TestRelayerBatchReemit(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 1))
	w.g.dispatch(104, sendMsg(testBUBBLE, ethBUBBLE, 2))
	w.g.dispatch(90, sendMsg(testGNOT, ethGNOT, 3)) // below the trusted height, never accepted
	w.g.head = 110
	w.tick()
	if len(w.g.runs) != 1 || !strings.Contains(w.g.runs[0], "Reemit(cross, 0)") || !strings.Contains(w.g.runs[0], "Reemit(cross, 1)") || !strings.Contains(w.g.runs[0], "Reemit(cross, 2)") {
		t.Fatalf("runs %v", w.g.runs)
	}
	if len(w.e.sentOf("process")) != 0 {
		t.Fatal("a delivery without an accepted header")
	}
	at := w.g.head - 1 // the batch's block
	if hs := w.r.state.Out.Index.Of(0, 102); fmt.Sprint(hs) != fmt.Sprintf("[102 %d]", at) {
		t.Fatalf("the batch's height not noted: %v", hs)
	}
	// inside the spacing nothing is re-emitted again
	w.advance(10 * time.Second)
	w.tick()
	if len(w.g.runs) != 1 {
		t.Fatalf("re-emitted inside the spacing: %d", len(w.g.runs))
	}
	// the updater's update to the header after the batch: everything is delivered under it
	w.e.trust(at + 1)
	w.tick()
	if n := len(w.e.sentOf("process")); n != 3 {
		t.Fatalf("%d deliveries under the batch's header", n)
	}
	w.tick()
	for _, n := range []string{"0", "1", "2"} {
		if w.r.state.Out.Done[n] != "delivered" {
			t.Fatalf("%s not delivered: %v", n, w.r.state.Out.Done)
		}
	}
}

// More waiting than a batch: the batches gather the oldest first, a block holding a batch is never re-emitted again,
// and a remainder already in one block is left to its own header (no batch loop).
func TestRelayerBatchBeyondTheBound(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.ReemitBatch = 2
	for i := 0; i < 5; i++ {
		w.g.dispatch(int64(102+i), sendMsg(testGNOT, ethGNOT, uint64(i+1)))
	}
	w.g.head = 110
	has := func(run string, ns ...int) bool {
		for _, n := range ns {
			if !strings.Contains(run, fmt.Sprintf("Reemit(cross, %d)", n)) {
				return false
			}
		}
		return true
	}
	w.tick()
	if len(w.g.runs) != 1 || !has(w.g.runs[0], 0, 1) || has(w.g.runs[0], 2) {
		t.Fatalf("the first batch: %v", w.g.runs)
	}
	w.advance(31 * time.Second)
	w.tick()
	if len(w.g.runs) != 2 || !has(w.g.runs[1], 2, 3) || has(w.g.runs[1], 0) || has(w.g.runs[1], 4) {
		t.Fatalf("the second batch: %v", w.g.runs)
	}
	for i := 0; i < 3; i++ {
		w.advance(31 * time.Second)
		w.tick()
	}
	if len(w.g.runs) != 2 {
		t.Fatalf("a full block or a lone remainder re-emitted: %v", w.g.runs[2:])
	}
	if !strings.Contains(w.r.last["waiting"], "no batch gathers them") {
		t.Fatal(w.r.last["waiting"])
	}
}

// Under the fee rule a waiting send whose fee does not cover an update is not re-emitted (nothing is bought for it
// before the heartbeat); a decision always is.
func TestRelayerFeeRuleHoldsTheBatch(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.0000001, "BUBBLE": 0.0000001}
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 1))
	w.g.dispatch(104, sendMsg(testGNOT, ethGNOT, 2))
	w.g.head = 110
	w.advance(time.Hour) // an hour after the last update
	w.tick()
	if len(w.g.runs) != 0 {
		t.Fatalf("a batch bought for sends whose fees do not cover an update: %v", w.g.runs)
	}
	if !strings.Contains(w.r.last["waiting"], "do not cover the whole cost") {
		t.Fatal(w.r.last["waiting"])
	}
	w.g.dispatch(106, decisionMsg(ethAdmin, 7, 1))
	w.advance(time.Minute)
	w.tick()
	if len(w.g.runs) != 1 || !strings.Contains(w.g.runs[0], "Reemit(cross, 2)") {
		t.Fatalf("a waiting decision did not bring the batch: %v", w.g.runs)
	}
}

// Decisions go first, one copy per number, only the next HlAdmin expects, under the emergency cap; a second copy of
// a recorded number ends "recorded"; a number past HlAdmin's counter that no copy of ours took is reported taken on
// the second pass, read at the finalized block.
func TestRelayerDecisions(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(102, decisionMsg(ethAdmin, 7, 2)) // #0: number 2 before 1
	w.g.dispatch(103, decisionMsg(ethAdmin, 7, 1)) // #1
	w.g.dispatch(104, decisionMsg(ethAdmin, 7, 1)) // #2: a second copy of 1
	w.g.head = 110
	w.e.trust(105) // proves 104's block only
	w.tick()
	ps := w.e.sentOf("process")
	if len(ps) != 1 || ps[0].MaxBaseFee == nil {
		t.Fatalf("%d sends; the emergency cap %v", len(ps), ps)
	}
	if w.e.admins[ethAdmin].lastSeq != 1 || !w.e.delivered[w.g.idOf(2)] {
		t.Fatalf("decision 1 not recorded through copy #2: %d %v", w.e.admins[ethAdmin].lastSeq, w.r.state.Out.Done)
	}
	// the other copy of 1 is past: recorded (this relayer delivered a copy of it, final now)
	w.tick()
	if w.r.state.Out.Done["2"] != "delivered" || w.r.state.Out.Done["1"] != "recorded" {
		t.Fatalf("the second copy: %v", w.r.state.Out.Done)
	}
	// number 2 now goes, once a header proves its block
	w.e.trust(108)
	w.e.accept(103) // header 103 proves block 102's results, where number 2 is
	w.tick()
	w.tick()
	if w.e.admins[ethAdmin].lastSeq != 2 || w.r.state.Out.Done["0"] != "delivered" {
		t.Fatalf("decision 2: %d %v", w.e.admins[ethAdmin].lastSeq, w.r.state.Out.Done)
	}
	// a foreign decision took number 3: our copy is past at latest; at the finalized block the counter covers it too
	// and no copy of ours is delivered there — reported taken on the second pass, never on the first
	w.g.dispatch(120, decisionMsg(ethAdmin, 7, 3))
	w.g.head = 125
	w.e.admins[ethAdmin].lastSeq = 3
	w.tick()
	if w.r.state.Out.Done["3"] != "" {
		t.Fatalf("taken on the first pass: %v", w.r.state.Out.Done)
	}
	w.tick()
	if w.r.state.Out.Done["3"] != "taken" || !w.r.state.Taken["3"] {
		t.Fatalf("not reported taken: %v %v", w.r.state.Out.Done, w.r.state.Taken)
	}
	if _, ok := w.r.Alarm.Standing()["decision:taken:3"]; !ok {
		t.Fatal("no alarm")
	}
	// a number recorded at latest but not yet at the finalized block is no verdict
	w.g.dispatch(130, decisionMsg(ethAdmin, 7, 4))
	w.g.head = 135
	w.e.admins[ethAdmin].lastSeq = 4
	fin := uint64(3)
	w.e.admins[ethAdmin].finalLastSeq = &fin
	w.tick()
	w.tick()
	if w.r.state.Out.Done["4"] != "" {
		t.Fatalf("a number not yet final reported: %v", w.r.state.Out.Done)
	}
}

// While the gate is flagged or the quarantine stands, no route delivery is sent (decisions still go); OverDayCap
// parks a send, retried when the bucket has room; a send gno.land refunded never goes.
func TestRelayerPausedCapsAndRefunds(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.e.trust(103)
	w.e.flags[4] = ethx.Flag{BlockNumber: 9, Time: 1}
	w.tick()
	if len(w.e.sentOf("process")) != 0 || !strings.Contains(w.r.last["deliveries"], "paused") {
		t.Fatalf("delivered under a flag: %v", w.r.last["deliveries"])
	}
	w.e.flags[4] = ethx.Flag{}
	w.e.quarantineEnd = w.e.now + 3600
	w.tick()
	if len(w.e.sentOf("process")) != 0 {
		t.Fatal("delivered under the quarantine")
	}
	w.e.quarantineEnd = 0
	// the day cap: full; three refusals park it on OverDayCap
	w.e.limits[0] = ethx.MintLimit{MaxPerMessage: 1_000, DayCap: 1_000, Used: 1_000, UsedAt: w.e.now}
	for i := 0; i < 3; i++ {
		w.tick()
		w.advance(time.Hour)
	}
	a := w.r.state.Attempts["deliver:0"]
	if !a.parked() || !a.causeIs("OverDayCap") {
		t.Fatalf("%+v", a)
	}
	sent := len(w.e.sentOf("process"))
	w.tick()
	if len(w.e.sentOf("process")) != sent {
		t.Fatal("a park retried into a full bucket")
	}
	w.e.now += 86_400 // the bucket drained
	w.tick()
	w.tick()
	if w.r.state.Out.Done["0"] != "delivered" {
		t.Fatalf("the park did not go once the bucket had room: %+v", w.r.state.Attempts["deliver:0"])
	}
	// a refunded send: the route is a migrated one (refunds are a successor's), its record carries the mark
	w.g.dispatch(140, sendMsg(testGNOT, ethGNOT, 7))
	w.g.head = 145
	w.e.trust(141)
	w.g.renders[testGNOT+":json"] = routeJSON(50_000_000, `,"imported":2`)
	w.g.renders[testGNOT+":send/"+hexID(w.g.idOf(1))] = `{"id":"x","from":"g1a","amount":7,"refunded":1700000000}`
	w.tick()
	if w.r.state.Out.Done["1"] != "refunded" || w.e.delivered[w.g.idOf(1)] {
		t.Fatalf("a refunded send: %v", w.r.state.Out.Done)
	}
	// a refunded send no header proves yet buys nothing: ended before the waiting rule sees it
	w.g.dispatch(150, sendMsg(testGNOT, ethGNOT, 8))
	w.g.dispatch(152, sendMsg(testGNOT, ethGNOT, 9))
	w.g.head = 155
	w.g.renders[testGNOT+":send/"+hexID(w.g.idOf(2))] = `{"id":"x","from":"g1a","amount":8,"refunded":true}`
	w.g.renders[testGNOT+":send/"+hexID(w.g.idOf(3))] = `null`
	runs := len(w.g.runs)
	w.tick()
	if w.r.state.Out.Done["2"] != "refunded" {
		t.Fatalf("%v", w.r.state.Out.Done)
	}
	if len(w.g.runs) != runs {
		t.Fatalf("a batch bought with a refunded send in it: %v", w.g.runs[runs:])
	}
}

func hexID(id [32]byte) string { return fmt.Sprintf("%x", id[:]) }

// The backup acts only on what has been possible for 60 s, and sends on Ethereum only while the shared key is quiet.
func TestRelayerBackup(t *testing.T) {
	w := newWorld(t, 100, "backup")
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.e.trust(103)
	w.tick()
	w.advance(30 * time.Second)
	w.tick()
	if len(w.e.sentOf("process")) != 0 {
		t.Fatal("the backup acted inside 60 s")
	}
	w.advance(31 * time.Second)
	w.e.quiet = false // the primary's key sent a moment ago
	w.tick()
	if len(w.e.sentOf("process")) != 0 {
		t.Fatal("the backup sent while the shared key was busy")
	}
	w.e.quiet = true
	w.tick()
	if len(w.e.sentOf("process")) != 1 {
		t.Fatal("the backup did not take over")
	}
}

// A base fee above maxBaseFee parks route deliveries (a wait, no failure counted); a decision goes under the
// emergency cap.
func TestRelayerFeeHigh(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 500))
	w.g.dispatch(102, decisionMsg(ethAdmin, 7, 1))
	w.g.head = 110
	w.e.trust(103)
	w.e.baseFee = ethx.GweiOf(40)
	w.tick()
	w.tick()
	if w.r.state.Out.Done["1"] != "delivered" || w.r.state.Out.Done["0"] != "" {
		t.Fatalf("%v", w.r.state.Out.Done)
	}
	if a := w.r.state.Attempts["deliver:0"]; a != nil && a.Failures != 0 {
		t.Fatalf("a fee wait counted as a failure: %+v", a)
	}
	if _, ok := w.r.Alarm.Standing()["eth:fee-high"]; !ok {
		t.Fatal("no fee alarm")
	}
}

// HlAdmin's matured decisions are executed (emergency cap), an immature one waits, one deferred by a hold waits,
// a cancelled one is closed at any time; the contract's NotMature is a wait.
func TestRelayerAdminExecutes(t *testing.T) {
	w := newWorld(t, 100, "primary")
	a := w.e.admins[ethAdmin]
	now := w.e.now
	a.lastSeq = 4
	a.decisions[1] = &ethx.Decision{Number: 1, Kind: 7, DeliveredTime: now - 8*86_400}
	a.decisions[2] = &ethx.Decision{Number: 2, Kind: 7, DeliveredTime: now - 86_400}
	a.decisions[3] = &ethx.Decision{Number: 3, Kind: 2, DeliveredTime: now - 2*86_400}
	a.decisions[4] = &ethx.Decision{Number: 4, Kind: 12, DeliveredTime: now - 3600}
	a.cancelled = [][2]uint64{{4, 4}}
	w.tick()
	ex := w.e.sentOf("execute")
	if len(ex) != 3 || ex[0].MaxBaseFee == nil {
		t.Fatalf("%d executes", len(ex))
	}
	if a.decisions[1] != nil || a.decisions[3] != nil || a.decisions[4] != nil || a.decisions[2] == nil {
		t.Fatalf("%v", a.decisions)
	}
	// the cursor moves by the finalized block (E3-GS9): past every number read there, each still pending there kept
	// to be read again — number 1, gone at latest but pending at the finalized block, stays kept beside 2; once the
	// finalized block shows it gone only 2, pending, is kept
	a.finalDecisions = map[uint64]*ethx.Decision{1: {Number: 1, Kind: 7, DeliveredTime: now - 8*86_400}, 2: a.decisions[2]}
	w.tick()
	if p := w.r.state.AdminPending[ethAdmin.Hex()]; fmt.Sprint(p) != "[1 2]" || w.r.state.AdminLow[ethAdmin.Hex()] != 5 {
		t.Fatalf("the numbers kept pending at the finalized block: %v, the cursor %d", p, w.r.state.AdminLow[ethAdmin.Hex()])
	}
	a.finalDecisions = nil
	w.tick()
	if p := w.r.state.AdminPending[ethAdmin.Hex()]; fmt.Sprint(p) != "[2]" || w.r.state.AdminLow[ethAdmin.Hex()] != 5 {
		t.Fatalf("kept %v, the cursor %d", p, w.r.state.AdminLow[ethAdmin.Hex()])
	}
	// a hold defers the 7-day group until it ends
	a.decisions[5] = &ethx.Decision{Number: 5, Kind: 7, DeliveredTime: now - 8*86_400}
	a.lastSeq = 5
	a.hold = ethx.Flag{BlockNumber: 3, Time: now - 3600}
	w.tick()
	if a.decisions[5] == nil || !strings.Contains(w.r.last["execute:5"], "hold") {
		t.Fatalf("executed under a hold: %s", w.r.last["execute:5"])
	}
	a.hold = ethx.Flag{}
	// the contract says not mature (its clock ahead of this reading by seconds): a wait, no failure
	w.e.fail["execute"] = revert("NotMature", "")
	w.tick()
	if at := w.r.state.Attempts["execute:5"]; at == nil || at.Failures != 0 {
		t.Fatalf("%+v", at)
	}
}

// gno.land's queued decisions are applied once their delay passed by block time (Execute(id)); a refund never is.
func TestRelayerGnoExecutes(t *testing.T) {
	w := newWorld(t, 100, "primary")
	now := w.g.timeAt(w.g.head).Unix()
	w.g.renders[testGNOT+":decisions.json"] = fmt.Sprintf(`[{"id":2,"kind":"set cap floor","state":"pending","made":%d,"recorded":%d,"due":%d},{"id":3,"kind":"refund","state":"pending","made":%d,"recorded":%d,"due":%d}]`,
		now-100, now-100, now-10, now-100, now-100, now-10)
	w.tick()
	calls := w.g.callsOf(testGNOT + ".Execute")
	if len(calls) != 1 || calls[0] != testGNOT+".Execute(2)" {
		t.Fatalf("%v", calls)
	}
	if !strings.Contains(w.r.last["gnoexec:"+testGNOT+"#3"], "GovDAO's apply") {
		t.Fatal("a refund executed or not noted")
	}
}

// The config's contracts are held against the ones hlmailbox compiles: a mismatch delivers nothing and alarms.
func TestRelayerChecksTheCompiledContracts(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.e.trust(103)
	json := w.g.renders[testMailbox+":json"]
	w.g.renders[testMailbox+":json"] = strings.TrimSuffix(json, "}") + `,"ethContracts":{"mailbox":"` + ethMailbox.Hex() + `","gate":"0x00000000000000000000000000000000000000d9"}}`
	w.tick()
	if len(w.e.sentOf("process")) != 0 {
		t.Fatal("delivered under a config that names another gate")
	}
	if _, ok := w.r.Alarm.Standing()["config:contracts"]; !ok {
		t.Fatal("no alarm")
	}
	w.g.renders[testMailbox+":json"] = strings.TrimSuffix(json, "}") + `,"ethContracts":{"mailbox":"` + strings.ToLower(ethMailbox.Hex()) + `","gate":"` + ethGate.Hex() + `"}}`
	w.tick()
	if len(w.e.sentOf("process")) != 1 {
		t.Fatal("not delivered once the config matches")
	}
}

// The walk holds at r/hlgov's decision to an address HlAdmin's line does not name (G31); a retired HlAdmin's copy
// ends retired; HlAdmin's successor is followed.
func TestClassifyAndTheAdminLine(t *testing.T) {
	w := newWorld(t, 100, "primary")
	succ := common.HexToAddress("0x00000000000000000000000000000000000000e2")
	stranger := common.HexToAddress("0x00000000000000000000000000000000000000e9")
	w.g.dispatch(102, decisionMsg(stranger, 7, 1))
	w.g.head = 110
	w.e.trust(103)
	w.tick()
	if w.r.state.Out.Next != 0 || w.r.state.Out.Done["0"] != "" {
		t.Fatalf("walked past a decision to an unknown address: next %d %v", w.r.state.Out.Next, w.r.state.Out.Done)
	}
	if _, ok := w.r.Alarm.Standing()["decision:other-admin:0"]; !ok {
		t.Fatal("not alarmed")
	}
	// HlAdmin handed over to a successor: a copy to the old one ends retired; the successor takes decisions
	w.e.admins[ethAdmin].successor = succ
	w.e.admins[succ] = &fakeAdmin{decisions: map[uint64]*ethx.Decision{}, clockFactor: 1}
	w.g.dispatch(106, decisionMsg(ethAdmin, 7, 1), decisionMsg(succ, 7, 1))
	w.g.head = 112
	w.e.trust(107)
	w.tick()
	w.tick()
	if w.r.state.Out.Done["1"] != "retired" || w.r.state.Out.Done["2"] != "delivered" || w.e.admins[succ].lastSeq != 1 {
		t.Fatalf("%v %d", w.r.state.Out.Done, w.e.admins[succ].lastSeq)
	}
	_ = gnolight.Validator{}
}

// A delivery ends only once the finalized block shows its mark: delivered at latest alone, the message is neither
// sent again nor waiting; a delivery a reorg drops before it is final is made again.
func TestRelayerEndsAtFinality(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.e.trust(106)
	w.e.finalDelivered = map[[32]byte]bool{} // the finalized block shows no mark yet
	w.tick()
	w.tick()
	w.tick()
	if n := len(w.e.sentOf("process")); n != 1 || w.r.state.Out.Done["0"] != "" {
		t.Fatalf("%d deliveries, done %v", n, w.r.state.Out.Done)
	}
	if len(w.g.runs) != 0 {
		t.Fatalf("a batch bought for a settling message: %v", w.g.runs)
	}
	// a reorg drops the delivery before it is final: made again
	delete(w.e.delivered, w.g.idOf(0))
	w.tick()
	if n := len(w.e.sentOf("process")); n != 2 {
		t.Fatalf("%d deliveries after the reorg", n)
	}
	w.e.finalDelivered = nil // final now
	w.tick()
	if w.r.state.Out.Done["0"] != "delivered" {
		t.Fatalf("%v", w.r.state.Out.Done)
	}
}

// A park delivered meanwhile — by anyone, settling until final — reserves nothing against younger sends.
func TestCapParksSkipSettling(t *testing.T) {
	parked := &outMsg{n: 3, kind: kindRoute, msg: &gnoside.Msg{Msg: sendMsg(testGNOT, ethGNOT, 700)}, rt: &Route{Symbol: "GNOT"}}
	attempts := map[string]*attempt{"deliver:3": {Failures: parkAfter, Parked: time.Unix(1, 0), Cause: "OverDayCap"}}
	if ps := capParks([]*outMsg{parked}, attempts); ps[0] == nil && ps[1] == nil {
		t.Fatal("the park is not seen")
	}
	parked.settling = true
	if ps := capParks([]*outMsg{parked}, attempts); ps[0] != nil || ps[1] != nil {
		t.Fatalf("a settling park reserves: %+v", ps)
	}
}

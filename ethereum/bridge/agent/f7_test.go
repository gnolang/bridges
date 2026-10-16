package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
	"github.com/gnolang/bridges/ethereum/bridge/repro"
)

// Fold F7 — the plan red team's E2-RT1 … E2-RT10 on the machines' side (eth/bridge/M3b-NOTES.md, "Fold F7").

// E2-RT6: with no eth.deployBlock in the config, a fresh scan starts at finishSetup's block — the first block at which
// HlAdmin answers setupDone() true — never at the mailbox's first block, where a deployer's mint and burn-back before
// the setup was done would be read as a burn of this deployment.
func TestFreshScansFindFinishSetupsBlock(t *testing.T) {
	cfg := m3bConfig()
	chain := newM3bChain(100)
	chain.view(m3bAdmin, "setupDone", func(n uint64, _ []any) []any { return []any{n >= 42} })
	pair, _, _ := m3bPair(chain)
	g := newM3bGno(60, time.Now())
	k, err := hl.NewValidatorKey()
	if err != nil {
		t.Fatal(err)
	}
	s := &Signer{Cfg: cfg, Key: k, Store: t.TempDir(), Alarm: &Alarmer{Source: "test"}, pair: pair, gno: g}
	if err := s.load(); err != nil {
		t.Fatal(err)
	}
	if err := s.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.state.From != 42 {
		t.Fatalf("the signer's index starts at %d, not finishSetup's block 42 (the mailbox's code is there from block 1)", s.state.From)
	}
	m := &Monitor{Cfg: cfg, Alarm: &Alarmer{Source: "test"}, ReadOnly: true, pair: pair, own: g, sources: []gnoside.Reader{g}}
	if err := m.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.state.From != 42 {
		t.Fatalf("the monitor's scans start at %d, not 42", m.state.From)
	}
	// a setup not done at the head: no start is guessed
	chain.view(m3bAdmin, "setupDone", func(uint64, []any) []any { return []any{false} })
	s2 := &Signer{Cfg: cfg, Key: k, Store: t.TempDir(), Alarm: &Alarmer{Source: "test"}, pair: pair, gno: g}
	if err := s2.load(); err != nil {
		t.Fatal(err)
	}
	if err := s2.start(context.Background()); err == nil {
		t.Fatalf("a signer started over a setup not done, from block %d", s2.state.From)
	}
}

// E2-RT6: the header record of a fresh monitor starts at eth.deployBlock (finishSetup's block), after the client's
// initialization recorded the trusted header (step 4 of the setup): it is seeded from the client's counter and running
// hash at the block before, so the rebuilt record holds to the client's — no gap, no flag — and a header recorded from
// there on is folded and checked as before.
func TestMonitorRecordStartsAtTheDeployBlock(t *testing.T) {
	r := newMonRig(t) // the client's record: its trusted header at height 50, recorded at block 10
	r.chain.mu.Lock()
	r.chain.code[m3bClient] = 5
	r.chain.mu.Unlock()
	r.m.state.From = 20 // finishSetup's block
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	rec := r.m.state.Record
	if rec.Gap != "" || rec.Count != 1 || r.alarmed("header:gap:pending") {
		t.Fatalf("the record from the deploy block: count %d, gap %q (alarms %v)", rec.Count, rec.Gap, r.m.Alarm.Standing())
	}
	// an update after it: folded on the seed, checked against the node
	r.chain.mine(110, 110)
	r.recordHeader(55, 105, r.nodeHash(55))
	r.mu.Lock()
	r.trusted = 55
	r.mu.Unlock()
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	rec = r.m.state.Record
	if rec.Gap != "" || rec.Count != 2 || rec.Checked != 1 || rec.Mismatch != nil {
		t.Fatalf("the update after the deploy block: count %d, checked %d, gap %q, mismatch %v", rec.Count, rec.Checked, rec.Gap, rec.Mismatch)
	}
}

// reemitAt is a stranger's MsgRun at block h re-emitting the nonces given: one transaction, one results leaf carrying
// their Dispatch events (anyone may Reemit any nonce, any number of times, E2-RT2).
func (f *fakeGno) reemitAt(h int64, nonces ...uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var raws [][]byte
	for _, n := range nonces {
		raws = append(raws, f.raw(n))
	}
	f.results[h] = append(f.results[h], dispatchTx(f.mailbox, raws...))
	if f.head < h+1 {
		f.head = h + 1
	}
}

// E2-RT2: a send sitting in a leaf of more than a batch's Dispatch events — a stranger's MsgRun of five Reemits, under a
// header the stranger bought — is never delivered from it (each delivery would read all five, ≈94,000 gas apiece): the
// relayer re-emits the sends into its own batch, at most four, and delivers them from that leaf once its header is in.
func TestRelayerReemitsASendFromABigLeaf(t *testing.T) {
	w := newWorld(t, 100, "primary")
	for i := 0; i < 5; i++ {
		w.g.dispatch(int64(101+i), sendMsg(testGNOT, ethGNOT, uint64(10+i)))
	}
	w.g.reemitAt(107, 0, 1, 2, 3, 4)
	w.g.head = 110
	w.e.trust(108) // the stranger's update: header 108 proves block 107's leaf of five
	w.tick()
	if n := len(w.e.sentOf("process")); n != 0 {
		t.Fatalf("%d deliveries from a leaf of five Dispatch events", n)
	}
	if len(w.g.runs) != 1 || strings.Count(w.g.runs[0], "Reemit(cross,") != 4 {
		t.Fatalf("not re-emitted into the relayer's own batch of four: %v", w.g.runs)
	}
	at := w.g.head - 1 // the batch's block
	w.e.trust(at + 1)  // its header (the updater's, here by hand)
	w.advance(time.Minute)
	w.tick()
	ps := w.e.sentOf("process")
	if len(ps) != 4 {
		t.Fatalf("%d deliveries from the batch's leaf (runs %v, last %v)", len(ps), w.g.runs, w.r.last)
	}
	for _, p := range ps {
		meta, _ := ethx.Mailbox.ABI.Methods["process"].Inputs.Unpack(p.Data[4:])
		_, _, _, _, leaf, err := ethx.MetadataOf(meta[0].([]byte))
		if err != nil {
			t.Fatal(err)
		}
		if ds, _ := gnolight.DispatchEvents(leaf, testMailbox); len(ds) != 4 {
			t.Fatalf("a delivery proven from a leaf of %d Dispatch events", len(ds))
		}
	}
}

// E2-RT2: from a header the updater did not buy (a stranger's update), a send is delivered only while its own fee
// covers its delivery's model cost at the live price — nothing in the budget booked it; from the updater's header
// (its HeaderRecorded log's transaction sent from eth.updater) it is delivered as the budget booked it.
func TestRelayerHoldsAStrangersHeaderToTheSendsFee(t *testing.T) {
	w := newWorld(t, 100, "primary")
	// 50 GNOT at 1e-5 ETH is 0.0005 ETH; a delivery from a leaf of one costs 311,300 gas: 0.00062 ETH at 2 gwei
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
	w.r.Cfg.Eth.Updater = ethKey.Hex()
	w.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.e.trust(103) // a stranger's header: no HeaderRecorded log of the updater's
	w.tick()
	if n := len(w.e.sentOf("process")); n != 0 {
		t.Fatalf("a send delivered from a stranger's header at a loss: %d deliveries (%v)", n, w.r.last)
	}
	if !strings.Contains(w.r.last["deliver:0"], "no header the updater's key booked it under") || !strings.Contains(w.r.last["deliver:0"], "does not cover its delivery") {
		t.Fatalf("the reason: %s", w.r.last["deliver:0"])
	}
	// the base fee falls to 0.2 gwei: 311,300 gas at 1.2 gwei is 0.00037 ETH, under its fee
	w.e.baseFee = ethx.GweiOf(0.2)
	w.advance(time.Minute)
	w.tick()
	if n := len(w.e.sentOf("process")); n != 1 {
		t.Fatalf("the send not delivered once its fee covers its delivery: %d (%v)", n, w.r.last["deliver:0"])
	}
	// the updater's own header: the send is delivered whatever its fee (the budget booked it)
	w2 := newWorld(t, 100, "primary")
	w2.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
	w2.r.Cfg.Eth.Updater = ethKey.Hex()
	w2.g.dispatch(102, sendMsg(testGNOT, ethGNOT, 500))
	w2.g.head = 110
	u := &Updater{Cfg: w2.r.Cfg, Sender: w2.e, Eth: w2.e, Alarm: &Alarmer{Source: "test"}, gno: w2.g, blocks: w2.g}
	if err := u.init(); err != nil {
		t.Fatal(err)
	}
	u.now = func() time.Time { return w2.now }
	u.state.Budget.Balance = ethx.GweiOf(100_000_000).String() // 0.1 ETH: the six-hour send draws on it
	w2.advance(6*time.Hour + time.Minute)
	w2.e.now += uint64((6*time.Hour + time.Minute) / time.Second)
	if err := u.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w2.e.trusted != 103 {
		t.Fatalf("the updater did not buy the six-hour send's header: trusted %d (due %v)", w2.e.trusted, u.status["due"])
	}
	w2.tick()
	if n := len(w2.e.sentOf("process")); n != 1 {
		t.Fatalf("the send not delivered from the updater's header: %d (%v)", n, w2.r.last["deliver:0"])
	}
}

// E2-RT2: a header is due only while the sends' fees cover the whole cost, the deliveries' too: a send whose fee covers
// the header (1,448,600 gas, gnoland-1's five signatures) and not its delivery as well buys nothing.
func TestUpdaterPricesEveryDelivery(t *testing.T) {
	w := newUpdWorld(t, 100)
	// at 2 gwei the header is 0.0028972 ETH, the header and one delivery 0.0035198: 50 GNOT at 6.4e-5 is 0.0032
	w.u.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.000064}
	w.u.Cfg.Eth.UpdateGas = 1_448_600
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.advance(time.Hour)
	w.tick(t)
	if len(w.e.sentOf("update")) != 0 {
		t.Fatalf("a header bought by fees that cover it and not its delivery (due %v)", w.u.status["due"])
	}
	// a second send in the same block (a batch of two, one leaf each here): 0.0064 ETH covers 1,448,600 + 2 × 311,300
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 501))
	w.advance(time.Minute)
	w.tick(t)
	if len(w.e.sentOf("update")) != 1 || w.e.trusted != 106 {
		t.Fatalf("two sends' fees cover their header and deliveries: trusted %d (due %v)", w.e.trusted, w.u.status["due"])
	}
}

// E2-RT5: the batch is four by default — the cheapest, √(1.45M/94K) — and a send is proven from its cheapest usable
// leaf (the fewest Dispatch events), not its newest.
func TestBatchOfFourAndTheCheapestLeaf(t *testing.T) {
	w := newWorld(t, 100, "primary")
	for i := 0; i < 6; i++ {
		w.g.dispatch(int64(101+i), sendMsg(testGNOT, ethGNOT, uint64(10+i)))
	}
	w.g.head = 110
	w.e.trust(99 + 1) // nothing proven: every send waits, split over blocks
	w.tick()
	if len(w.g.runs) != 1 || strings.Count(w.g.runs[0], "Reemit(cross,") != 4 {
		t.Fatalf("a batch of %d (runs %v)", strings.Count(strings.Join(w.g.runs, ""), "Reemit(cross,"), w.g.runs)
	}
	// a send with two accepted events: its own leaf of one at 120, and a stranger's leaf of three at 122 (newer)
	w2 := newWorld(t, 100, "primary")
	w2.g.dispatch(120, sendMsg(testGNOT, ethGNOT, 7))
	w2.g.dispatch(121, sendMsg(testGNOT, ethGNOT, 8))
	w2.g.dispatch(121, sendMsg(testGNOT, ethGNOT, 9))
	w2.g.reemitAt(122, 0, 1, 2)
	w2.g.head = 125
	w2.e.accept(121)
	w2.e.trust(123)
	w2.tick()
	var proofHeader [32]byte
	for _, p := range w2.e.sentOf("process") {
		vals, _ := ethx.Mailbox.ABI.Methods["process"].Inputs.Unpack(p.Data[4:])
		hh, _, _, _, _, _ := ethx.MetadataOf(vals[0].([]byte))
		m, _ := hl.ParseMessage(vals[1].([]byte))
		if m.Nonce == 0 {
			proofHeader = hh
		}
	}
	w2.g.mu.Lock()
	want := w2.g.header(121).Hash
	w2.g.mu.Unlock()
	if proofHeader != want {
		t.Fatalf("send 0 proven under %x, not header 121's %x (its leaf of one)", proofHeader[:4], want[:4])
	}
}

// The budget's ledger is kept in the updater's state file: a restart keeps its balance, its bookings and its marks.
func TestBudgetSurvivesARestart(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.u.State = filepath.Join(t.TempDir(), "updater.json")
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.tick(t)
	if err := w.u.save(); err != nil {
		t.Fatal(err)
	}
	bal := w.u.state.Budget.Balance
	if len(w.u.state.Budget.Entries) != 1 || bal == "0" || !w.u.state.Budget.booked(0) {
		t.Fatalf("the booking: %+v", w.u.state.Budget)
	}
	u2 := &Updater{Cfg: w.u.Cfg, Sender: w.e, Eth: w.e, State: w.u.State, Alarm: &Alarmer{Source: "test"}, gno: w.g, blocks: w.g}
	if err := u2.init(); err != nil {
		t.Fatal(err)
	}
	if u2.state.Budget.Balance != bal || len(u2.state.Budget.Entries) != 1 || !u2.state.Budget.booked(0) {
		t.Fatalf("after a restart: %+v (was %s)", u2.state.Budget, bal)
	}
}

// E2-RT8, E4-FC5: the emergency cap is 50 gwei by default, and the updater keeps what a decision's header needs at it
// (≈0.174 ETH at gnoland-1's set): under it float:updater alarms, on the updater and on the monitor, naming the base fee
// it still sends one under.
func TestEmergencyCapAndTheUpdatersFloat(t *testing.T) {
	if got := (&Config{}).EmergencyMaxBaseFee(); got.Cmp(ethx.GweiOf(50)) != 0 {
		t.Fatalf("the emergency cap's default is %s wei, not 50 gwei", got)
	}
	w := newUpdWorld(t, 100)
	w.e.balances = map[common.Address]*big.Int{ethKey: big.NewInt(1e17)} // 0.1 ETH
	text := w.u.floatFloor(context.Background())
	if _, ok := w.u.Alarm.Standing()["float:updater"]; !ok || !strings.Contains(text, "0.100000 ETH") || !strings.Contains(text, "under ≈29 gwei") {
		t.Fatalf("0.1 ETH on the updater: %s (%v)", text, w.u.Alarm.Standing())
	}
	w.e.balances[ethKey] = big.NewInt(2e17)
	if w.u.floatFloor(context.Background()); w.u.Alarm.Standing()["float:updater"] != "" {
		t.Fatal("0.2 ETH alarmed")
	}
	// the floor is the header itself at the cap, priced at the set after the trusted height, never a flat 0.2 ETH the
	// route headers spend through from the first one (E4-FC5): a small set's header (one validator, 486,600 gas) fits
	// 0.15 ETH up to ≈128 gwei, past the cap, and needs 0.058 ETH there
	w.u.Cfg.Eth.UpdateGas = 486_600
	w.e.balances[ethKey] = big.NewInt(15e16)
	if text := w.u.floatFloor(context.Background()); w.u.Alarm.Standing()["float:updater"] != "" {
		t.Fatalf("0.15 ETH over a small set's header alarmed: %s", text)
	}
	w.e.balances[ethKey] = big.NewInt(5e16)
	if text := w.u.floatFloor(context.Background()); w.u.Alarm.Standing()["float:updater"] == "" || !strings.Contains(text, "under the 0.058 ETH a decision's header needs at the cap") {
		t.Fatalf("0.05 ETH over a small set's header: %s (%v)", text, w.u.Alarm.Standing())
	}
	r := newMonRig(t)
	r.m.Cfg.Eth.Updater = "0x000000000000000000000000000000000000dddd"
	r.chain.balances = map[common.Address]*big.Int{common.HexToAddress("0x000000000000000000000000000000000000dddd"): big.NewInt(5e16)}
	r.m.runways(context.Background())
	if !r.alarmed("float:updater") {
		t.Fatal("the monitor did not alarm the updater's 0.05 ETH")
	}
}

// E2-RT8: a waiting decision's header is bought by the relayers' key when the updater's cannot pay for it (its ETH
// short of the header at twice the base fee), five minutes on; with the updater able to pay, only after thirty
// minutes (the updater down) — and the decision is then delivered under it.
func TestRelayerBackstopBuysADecisionsHeader(t *testing.T) {
	for _, c := range []struct {
		name    string
		updater *big.Int
		wait    time.Duration
	}{{"the updater's float short", big.NewInt(1e15), 5 * time.Minute}, {"the updater down", big.NewInt(1e18), 30 * time.Minute}} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, 100, "primary")
			upd := common.HexToAddress("0x000000000000000000000000000000000000eeee")
			w.r.Cfg.Eth.Updater = upd.Hex()
			w.e.balances = map[common.Address]*big.Int{upd: c.updater}
			w.g.dispatch(102, decisionMsg(ethAdmin, 7, 1))
			w.g.head = 110
			w.advance(time.Minute)
			w.tick()
			if n := len(w.e.sentOf("update")); n != 0 {
				t.Fatalf("the backstop bought a header after a minute: %d", n)
			}
			w.advance(c.wait)
			w.tick()
			ups := w.e.sentOf("update")
			if len(ups) != 1 || w.e.trusted != 103 || ups[0].MaxBaseFee == nil {
				t.Fatalf("the backstop's header: %d updates, trusted %d (%v)", len(ups), w.e.trusted, w.r.last)
			}
			w.advance(time.Minute)
			w.tick()
			if w.e.admins[ethAdmin].lastSeq != 1 {
				t.Fatalf("the decision not delivered under the backstop's header: %v", w.r.last)
			}
		})
	}
	// the updater able to pay, a decision waiting ten minutes: the relayers' key buys nothing
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Updater = common.HexToAddress("0x000000000000000000000000000000000000eeee").Hex()
	w.g.dispatch(102, decisionMsg(ethAdmin, 7, 1))
	w.g.head = 110
	w.advance(10 * time.Minute)
	w.tick()
	if n := len(w.e.sentOf("update")); n != 0 {
		t.Fatalf("the backstop bought a header the updater could pay for: %d", n)
	}
}

// A send waits for its header by design (its fees, its batch, its six hours): the monitor's age alarm for a send
// toward Ethereum is seven hours, not two, and the stall counts a send only from its six hours.
func TestMonitorSendAgeIsSevenHours(t *testing.T) {
	r := newMonRig(t)
	r.m.state.Sends["0"] = &monSend{Symbol: "GNOT", Amount: 100, Height: 55, Seen: r.now.Unix(), ID: "00"}
	r.m.state.LastDelivery = r.now.Unix()
	r.now = r.now.Add(3 * time.Hour)
	r.m.stalls(nil)
	if r.alarmed("age") || r.alarmed("stall") {
		t.Fatalf("a send three hours old alarmed: %v", r.m.Alarm.Standing())
	}
	r.now = r.now.Add(3*time.Hour + time.Minute)
	r.m.stalls(nil)
	if r.alarmed("age") || !r.alarmed("stall") {
		t.Fatalf("six hours: the stall, not yet the age: %v", r.m.Alarm.Standing())
	}
	r.now = r.now.Add(time.Hour)
	r.m.stalls(nil)
	if !r.alarmed("age") {
		t.Fatal("seven hours and no age alarm")
	}
}

// pendingSend renders hlmailbox's message 0 as a GNOT send of amount toward Ethereum, dispatched at height h (locked on
// gno.land: the route's lock counts it), the mailbox's count 1.
func (r *signerRig) pendingSend(amount uint64, h int64) [32]byte {
	r.t.Helper()
	m := hl.Message{Version: 3, Nonce: 0, Origin: m3bGnoDom, Sender: gnoside.RealmKey(m3bNS + "hlgnot"), Destination: m3bEthDom, Recipient: ethx.AddressTo32(m3bGNOT), Body: hl.TokenMessage(ethx.AddressTo32(common.HexToAddress("0x00000000000000000000000000000000000a11ce")), amount, nil)}
	raw := m.Bytes()
	id := hl.ID(raw)
	r.gno.set(m3bMailPath, "msg/0", fmt.Sprintf(`{"nonce":0,"id":%q,"root":"%s","height":%d,"message":%q}`, hex.EncodeToString(id[:]), strings.Repeat("00", 32), h, hex.EncodeToString(raw)))
	r.gno.set(m3bMailPath, "json", `{"domain":1198419796,"live":true,"count":1,"signers":{"keys":[],"threshold":2},"seats":[],"routes":{}}`)
	return id
}

// E2-RT9: the signers count the sends locked on gno.land and not delivered on Ethereum in flight, as the monitors do,
// so a forged mint up to the undelivered amount stops them by its own amount — still on Ethereum, or burned back
// toward gno.land, which is then never signed — where before it passed inside the waiting sends' room. The honest
// delivery of the send holds.
func TestSignerCountsUndeliveredSends(t *testing.T) {
	setup := func(t *testing.T) (*signerRig, [32]byte) {
		r := newSignerRig(t) // GNOT supply 1,000 on Ethereum
		r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1300, 0, 0, 0))
		id := r.pendingSend(300, 55)                                                             // locked: 1,300 against a supply of 1,000
		r.chain.view(m3bMailbox, "delivered", func(uint64, []any) []any { return []any{false} }) // not delivered at F
		r.round()
		st := r.status()
		ident := st["identity"].(map[string]any)["GNOT"].(map[string]any)
		if st["signing"] != true || ident["verdict"] != "holds" || ident["undelivered"].(int64) != 300 {
			t.Fatalf("the send in flight: %v", st)
		}
		return r, id
	}
	t.Run("a forged mint of the undelivered amount", func(t *testing.T) {
		r, _ := setup(t)
		r.chain.mine(110, 110)
		r.chain.setSupply(m3bGNOT, 105, 1300) // 300 nobody locked for
		r.step(time.Second)
		r.round()
		st := r.status()
		if st["signing"] == true || !strings.Contains(st["halted"].(string), "undelivered sends") {
			t.Fatalf("a forged mint of the undelivered 300 did not stop the signer: %v", st)
		}
	})
	t.Run("burned back toward gno.land", func(t *testing.T) {
		r, _ := setup(t)
		r.chain.mine(110, 110)
		r.chain.setSupply(m3bGNOT, 104, 1300)
		r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 300, 106) // the forged 300 burned back: supply 1,000
		r.step(time.Second)
		r.round()
		st := r.status()
		if st["signing"] == true {
			t.Fatalf("the forged mint burned back did not stop the signer: %v", st)
		}
		if _, err := r.s.readSigned(0); err == nil {
			t.Fatal("the burn of the forged mint was signed")
		}
	})
	t.Run("the honest delivery", func(t *testing.T) {
		r, id := setup(t)
		r.chain.mine(110, 110)
		r.chain.setSupply(m3bGNOT, 105, 1300)
		r.chain.view(m3bMailbox, "delivered", func(n uint64, in []any) []any { return []any{n >= 105 && in[0].([32]byte) == id} })
		r.step(time.Second)
		r.round()
		st := r.status()
		ident := st["identity"].(map[string]any)["GNOT"].(map[string]any)
		if st["signing"] != true || ident["verdict"] != "holds" || ident["undelivered"].(int64) != 0 {
			t.Fatalf("the honest delivery: %v", st)
		}
	})
}

// ------------------------------------------------------------------------------------- before a kind 8 or 9 --

// synthArt is a build for the pre-vote tests: a runtime of size bytes whose immutables sit at the places given, a
// creation code, the ABI given.
func synthArt(t *testing.T, name, abiJSON string, size int, ims map[string][]int) *repro.Artifact {
	t.Helper()
	a, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		t.Fatal(err)
	}
	rt := make([]byte, size)
	for i := range rt {
		rt[i] = byte(i*7 + len(name))
	}
	art := &repro.Artifact{Name: name, ABI: a, Runtime: rt, Creation: append([]byte{0x60, 0x80, 0x60, 0x40, 0x52}, []byte(name)...)}
	id := 1
	names := make([]string, 0, len(ims))
	for n := range ims {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		im := repro.Immutable{ID: fmt.Sprint(id), Name: n}
		for _, at := range ims[n] {
			im.Refs = append(im.Refs, repro.Ref{Start: at, Length: 32})
		}
		art.Immutables = append(art.Immutables, im)
		id++
	}
	sort.Slice(art.Immutables, func(i, k int) bool { return art.Immutables[i].Refs[0].Start < art.Immutables[k].Refs[0].Start })
	return art
}

// fillArt is a build's runtime with each immutable's value in every place: a deployment's code.
func fillArt(a *repro.Artifact, vals map[string][32]byte) []byte {
	code := append([]byte{}, a.Runtime...)
	for _, im := range a.Immutables {
		v := vals[im.Name]
		for _, r := range im.Refs {
			copy(code[r.Start:r.Start+r.Length], v[:])
		}
	}
	return code
}

func w32(a common.Address) [32]byte { return common.BytesToHash(a.Bytes()) }
func u32(n uint64) [32]byte         { return common.BigToHash(new(big.Int).SetUint64(n)) }

// liveBindings gives an HlAdmin's binding views on the fake chain.
func liveBindings(c *m3bChain, at common.Address, b ethx.Bindings) {
	one := func(name string, v any) { c.view(at, name, func(uint64, []any) []any { return []any{v} }) }
	one("mailbox", b.Mailbox)
	one("client", b.Client)
	one("gate", b.Gate)
	c.view(at, "token", func(_ uint64, in []any) []any { return []any{b.Tokens[in[0].(uint8)]} })
	one("proxyAdmin", b.ProxyAdmin)
	one("merkleTreeHook", b.MerkleTreeHook)
	one("protocolFee", b.ProtocolFee)
	one("ed25519", b.Ed25519)
	one("ed25519CodeHash", b.Ed25519CodeHash)
	one("localDomain", b.LocalDomain)
	one("origin", b.Origin)
	one("hlgov", b.Hlgov)
	c.view(at, "route", func(_ uint64, in []any) []any { return []any{b.Routes[in[0].(uint8)]} })
	one("mailboxPkgPathHash", b.MailboxPkgPathHash)
}

var (
	f7Verifier = common.HexToAddress("0x000000000000000000000000000000000000e255")
	f7PkgPath  = common.HexToHash("0x1c3e16dfd0cdf286f9dc2050eb667de1372933a451bbac8996c769b69bc617e9")
	f7Bindings = ethx.Bindings{Mailbox: m3bMailbox, Client: m3bClient, Gate: m3bGate, Tokens: [2]common.Address{m3bGNOT, m3bBUBBLE},
		ProxyAdmin: common.HexToAddress("0x000000000000000000000000000000000000Pa00"), MerkleTreeHook: m3bHook, ProtocolFee: common.HexToAddress("0x000000000000000000000000000000000000fee0"),
		Ed25519: f7Verifier, Ed25519CodeHash: common.HexToHash("0xed"), LocalDomain: m3bEthDom, Origin: m3bGnoDom,
		Hlgov: gnoside.RealmKey(m3bNS + "hlgov"), Routes: [2][32]byte{gnoside.RealmKey(m3bNS + "hlgnot"), gnoside.RealmKey(m3bNS + "hlbubble")}, MailboxPkgPathHash: f7PkgPath}
)

// newTargetRig is a watch over the fakes with the live deployment's bindings on HlAdmin and the client's hlmailbox path
// and verifier; codes are what each address answers with.
func newTargetRig(t *testing.T) (*monRig, *Watch, map[common.Address][]byte) {
	r := newMonRig(t)
	liveBindings(r.chain, m3bAdmin, f7Bindings)
	r.chain.view(m3bClient, "mailboxPkgPathHash", func(uint64, []any) []any { return []any{[32]byte(f7PkgPath)} })
	r.chain.view(m3bClient, "ED25519", func(uint64, []any) []any { return []any{f7Verifier} })
	codes := map[common.Address][]byte{}
	var mu sync.Mutex
	for _, p := range []*m3bProvider{r.pa, r.pb} {
		p.codeX = func(a common.Address) hexutil.Bytes {
			mu.Lock()
			defer mu.Unlock()
			return codes[a]
		}
	}
	// a kind 8 is "immutable" only with its storage layouts read (E3-CF3): an append-only pair, for the kind 8s here
	return r, &Watch{m: r.m, CreationTxs: map[common.Address]common.Hash{}, LayoutOld: []byte(appendOnlyOld), LayoutNew: []byte(appendOnlyNew)}, codes
}

const tokenABI = `[{"type":"constructor","inputs":[{"name":"__decimals","type":"uint8"},{"name":"_scaleNumerator","type":"uint256"},{"name":"_scaleDenominator","type":"uint256"},{"name":"_mailbox","type":"address"}]},
	{"type":"function","name":"initializeV2","stateMutability":"nonpayable","inputs":[{"name":"minter","type":"address"}],"outputs":[]}]`

// E2-RT3, E2-RT1, E2-RT10 before a kind 8: a token implementation with immutables is held to its build masked — every
// immutable printed beside the live value it must have — its creation transaction to the build's creation code and
// each constructor argument, its build to the one the proxy runs, its call decoded against its ABI; "immutable" only
// when all of it holds. A hash of the build (its immutables zero) matches no honest upgrade; no creation transaction,
// a wrong immutable, another argument, a byte past the arguments, another proxy's build, a call no function of the
// build reads, a factory's creation: each NOT SHOWN IMMUTABLE, said why.
func TestImplementationHeldToItsBuild(t *testing.T) {
	ctx := context.Background()
	r, w, codes := newTargetRig(t)
	art := synthArt(t, "HypERC20", tokenABI, 400, map[string][]int{"MailboxClient.mailbox": {10, 200}, "MailboxClient.localDomain": {60}, "HypERC20._decimals": {100},
		"TokenRouter.scaleNumerator": {140}, "TokenRouter.scaleDenominator": {250}, "Indexed.deployedBlock": {300}})
	impl := common.HexToAddress("0x000000000000000000000000000000000000d00d")
	good := map[string][32]byte{"MailboxClient.mailbox": w32(m3bMailbox), "MailboxClient.localDomain": u32(uint64(m3bEthDom)), "HypERC20._decimals": u32(6),
		"TokenRouter.scaleNumerator": u32(1), "TokenRouter.scaleDenominator": u32(1), "Indexed.deployedBlock": u32(77)}
	codes[impl] = fillArt(art, good)
	input, err := art.CreationCode(repro.Values{"decimals": uint8(6), "scalenumerator": big.NewInt(1), "scaledenominator": big.NewInt(1), "mailbox": m3bMailbox})
	if err != nil {
		t.Fatal(err)
	}
	tx := common.HexToHash("0xc7ea7e")
	r.chain.creation(tx, common.HexToAddress("0x00000000000000000000000000000000000de9e7"), input, impl, 77)
	w.Builds = map[string]Build{"hyperc20-v2": BuildOf(art)}
	w.CreationTxs[impl] = tx
	minter := common.HexToAddress("0x000000000000000000000000000000000000a11c")
	call, _ := art.ABI.Pack("initializeV2", minter)
	at := ethx.AtNumber(100)
	want := crypto.Keccak256Hash(codes[impl])
	check := func(proxy common.Address, call []byte) string {
		return w.ImplementationCheck(ctx, at, m3bAdmin, proxy, impl, want, call, "")
	}
	got := check(m3bGNOT, call)
	for _, s := range []string{
		"equals the decision's", "is the reproducible build hyperc20-v2 outside its 6 masked immutable(s)",
		"MailboxClient.mailbox = " + m3bMailbox.Hex() + " ok (our mailbox, HlAdmin's mailbox())", "HypERC20._decimals = 6 ok", "Indexed.deployedBlock = 77 ok (the block of its creation transaction)",
		"its creation " + tx.Hex(), "its input is the build hyperc20-v2's creation code", "_mailbox = " + m3bMailbox.Hex() + " ok",
		"then call initializeV2(address minter = " + minter.Hex() + ")", " — " + VerdictImmutable,
	} {
		if !strings.Contains(got, s) {
			t.Fatalf("an honest upgrade lacks %q:\n%s", s, got)
		}
	}
	if !strings.HasSuffix(got, " — "+VerdictImmutable) {
		t.Fatalf("an honest upgrade is not immutable:\n%s", got)
	}
	unshown := func(what, got, why string) {
		t.Helper()
		if !strings.Contains(got, " — "+VerdictUnshown) || !strings.Contains(got, why) {
			t.Errorf("%s: want NOT SHOWN IMMUTABLE for %q:\n%s", what, why, got)
		}
	}
	// a build's bare hash (its immutables zero, as the old -build read forge's artifact) matches no honest upgrade
	w.Builds = map[string]Build{"hyperc20-v2": {Hash: crypto.Keccak256Hash(art.Runtime)}}
	unshown("a hash of the build", check(m3bGNOT, call), "matches no reproducible build given")
	w.Builds = map[string]Build{"hyperc20-v2": BuildOf(art)}
	// no creation transaction
	delete(w.CreationTxs, impl)
	unshown("no creation transaction", check(m3bGNOT, call), "no creation transaction given (-creation-tx)")
	w.CreationTxs[impl] = tx
	// another proxy's build: the mailbox runs Mailbox
	unshown("the mailbox's proxy", check(m3bMailbox, call), "runs Mailbox, and the implementation is a build of HypERC20")
	// a call no function of the build reads
	unshown("an unknown call", check(m3bGNOT, []byte{0xde, 0xad, 0xbe, 0xef}), "UNDECODED")
	if got := check(m3bGNOT, []byte{0xde, 0xad, 0xbe, 0xef}); !strings.Contains(got, "the selector 0xdeadbeef is no function of the build hyperc20-v2") {
		t.Errorf("the undecoded call's reason: %s", got)
	}
	// one byte past the constructor's arguments (the same runtime: the initcode differs)
	r.chain.creation(tx, common.HexToAddress("0x00000000000000000000000000000000000de9e7"), append(append([]byte{}, input...), 0), impl, 77)
	unshown("a byte past the arguments", check(m3bGNOT, call), "not a canonical encoding")
	// a factory's creation: a call, not a top-level creation
	r.chain.mu.Lock()
	r.chain.txs[tx] = m3bTx{to: common.HexToAddress("0x000000000000000000000000000000000000fac7"), input: input}
	r.chain.mu.Unlock()
	unshown("a factory's", check(m3bGNOT, call), "made through a factory")
	// a token over a stand-in mailbox, created with it: the immutable and the argument both named
	stand := common.HexToAddress("0x000000000000000000000000000000000000bEEF")
	bad := map[string][32]byte{}
	for k, v := range good {
		bad[k] = v
	}
	bad["MailboxClient.mailbox"] = w32(stand)
	codes[impl] = fillArt(art, bad)
	want = crypto.Keccak256Hash(codes[impl])
	in2, _ := art.CreationCode(repro.Values{"decimals": uint8(6), "scalenumerator": big.NewInt(1), "scaledenominator": big.NewInt(1), "mailbox": stand})
	r.chain.creation(tx, common.HexToAddress("0x00000000000000000000000000000000000de9e7"), in2, impl, 77)
	got = check(m3bGNOT, call)
	unshown("a stand-in mailbox", got, "MailboxClient.mailbox = "+stand.Hex()+", but it must be "+m3bMailbox.Hex())
	unshown("a stand-in mailbox's argument", got, "its argument _mailbox = "+stand.Hex()+", but it must be "+m3bMailbox.Hex())
}

const hlAdminABI = `[{"type":"constructor","inputs":[{"name":"c","type":"tuple","components":[{"name":"mailbox","type":"address"},{"name":"hlgov","type":"bytes32"},{"name":"seats","type":"address[4]"},{"name":"startSequence","type":"uint64"},{"name":"predecessor","type":"address"}]}]}]`

// E2-RT3, E2-RT1 before a kind 9: the successor held to its build masked and its creation — fold F5's three new
// immutables among its own (its HlAdminExec's code hash, a start of 0, the hlmailbox path's hash) — its state as
// HlAdmin's execute reads it, its sixteen bindings printed beside this HlAdmin's — a successor bound to another client
// is spent "bindings" (it would take every authority over a client that verifies anything) — and its HlAdminExec held
// to its build.
func TestSuccessorHeldToItsBuildAndBindings(t *testing.T) {
	ctx := context.Background()
	r, w, codes := newTargetRig(t)
	succ := common.HexToAddress("0x000000000000000000000000000000000000a9a9")
	exec := crypto.CreateAddress(succ, 1)
	hla := synthArt(t, "HlAdmin", hlAdminABI, 700, map[string][]int{"HlAdmin.mailbox": {20}, "HlAdmin.hlgov": {120, 400}, "HlAdmin.predecessor": {220}, "HlAdmin.exec": {320},
		"HlAdmin._execCodeHash": {450}, "HlAdmin._startSequence": {500}, "HlAdmin.mailboxPkgPathHash": {560}})
	exa := synthArt(t, "HlAdminExec", `[]`, 300, map[string][]int{"HlAdminExec.admin": {10}, "HlAdminExec.mailbox": {60}, "HlAdminExec.launch": {110}, "HlAdminExec.dayCap0": {160}})
	codes[exec] = fillArt(exa, map[string][32]byte{"HlAdminExec.admin": w32(succ), "HlAdminExec.mailbox": w32(m3bMailbox), "HlAdminExec.launch": u32(0), "HlAdminExec.dayCap0": u32(5)})
	succVals := map[string][32]byte{"HlAdmin.mailbox": w32(m3bMailbox), "HlAdmin.hlgov": f7Bindings.Hlgov, "HlAdmin.predecessor": w32(m3bAdmin), "HlAdmin.exec": w32(exec),
		"HlAdmin._execCodeHash": crypto.Keccak256Hash(codes[exec]), "HlAdmin._startSequence": u32(0), "HlAdmin.mailboxPkgPathHash": f7PkgPath}
	codes[succ] = fillArt(hla, succVals)
	input, err := hla.CreationCode(repro.Values{"c": repro.Values{"mailbox": m3bMailbox, "hlgov": f7Bindings.Hlgov, "seats": [4]common.Address{}, "startsequence": uint64(0), "predecessor": m3bAdmin}})
	if err != nil {
		t.Fatal(err)
	}
	tx := common.HexToHash("0x5ecc")
	r.chain.creation(tx, common.HexToAddress("0x0000000000000000000000000000000000000de9"), input, succ, 80)
	w.Builds = map[string]Build{"hladmin-v2": BuildOf(hla), "hladminexec-v2": BuildOf(exa)}
	w.CreationTxs[succ] = tx
	liveBindings(r.chain, succ, f7Bindings)
	r.chain.view(succ, "predecessor", func(uint64, []any) []any { return []any{m3bAdmin} })
	r.chain.view(succ, "inherited", func(uint64, []any) []any { return []any{false} })
	r.chain.view(succ, "successor", func(uint64, []any) []any { return []any{common.Address{}} })
	r.chain.view(succ, "clockFactor", func(uint64, []any) []any { return []any{uint64(1)} })
	at := ethx.AtNumber(100)
	want := crypto.Keccak256Hash(codes[succ])
	got := w.SuccessorCheck(ctx, at, m3bAdmin, 1, succ, want)
	for _, s := range []string{
		"HlAdmin.predecessor = " + m3bAdmin.Hex() + " ok", "HlAdmin.exec = " + exec.Hex() + " ok", "HlAdmin._startSequence = 0 ok",
		"HlAdmin._execCodeHash = " + crypto.Keccak256Hash(codes[exec]).Hex() + " ok", "HlAdmin.mailboxPkgPathHash = " + f7PkgPath.Hex() + " ok",
		"startSequence = 0 ok", "seats = ", "its bindings beside this HlAdmin's: mailbox() " + m3bMailbox.Hex() + " ok", "client() " + m3bClient.Hex() + " ok",
		"mailboxPkgPathHash() " + f7PkgPath.Hex() + " ok",
		"its HlAdminExec " + exec.Hex() + " is the build hladminexec-v2", "HlAdminExec.admin = " + succ.Hex() + " ok", "HlAdminExec.dayCap0 = 5 (a launch's limits: never read by a successor)",
	} {
		if !strings.Contains(got, s) {
			t.Fatalf("an honest successor lacks %q:\n%s", s, got)
		}
	}
	if !strings.HasSuffix(got, " — "+VerdictImmutable) {
		t.Fatalf("an honest successor is not immutable:\n%s", got)
	}
	// bound to another client: spent, said loudly
	other := f7Bindings
	other.Client = common.HexToAddress("0x000000000000000000000000000000000000ba1d")
	liveBindings(r.chain, succ, other)
	got = w.SuccessorCheck(ctx, at, m3bAdmin, 1, succ, want)
	if !strings.Contains(got, " — "+VerdictSpent) || !strings.Contains(got, "THE SUCCESSOR IS BOUND ELSEWHERE (client() "+other.Client.Hex()+", not this HlAdmin's "+m3bClient.Hex()) || !strings.Contains(got, `HlAdmin spends it "bindings"`) {
		t.Fatalf("a successor bound to another client:\n%s", got)
	}
	liveBindings(r.chain, succ, f7Bindings)
	// a start of its own in its code (an initcode's): the immutable printed beside the 0 it must be
	bad := map[string][32]byte{}
	for k, v := range succVals {
		bad[k] = v
	}
	bad["HlAdmin._startSequence"] = u32(41)
	codes[succ] = fillArt(hla, bad)
	if got := w.SuccessorCheck(ctx, at, m3bAdmin, 1, succ, crypto.Keccak256Hash(codes[succ])); !strings.Contains(got, " — "+VerdictUnshown) || !strings.Contains(got, "HlAdmin._startSequence = 41, but it must be 0") {
		t.Fatalf("a successor with a start of its own:\n%s", got)
	}
	codes[succ] = fillArt(hla, succVals)
	// its HlAdminExec held to no build
	w.Builds = map[string]Build{"hladmin-v2": BuildOf(hla)}
	if got := w.SuccessorCheck(ctx, at, m3bAdmin, 1, succ, want); !strings.Contains(got, " — "+VerdictUnshown) || !strings.Contains(got, "is held to no build") {
		t.Fatalf("a successor whose HlAdminExec no build holds:\n%s", got)
	}
	w.Builds = map[string]Build{"hladmin-v2": BuildOf(hla), "hladminexec-v2": BuildOf(exa)}
	// created with a number of its own (a successor takes its predecessor's by successorInit)
	in2, _ := hla.CreationCode(repro.Values{"c": repro.Values{"mailbox": m3bMailbox, "hlgov": f7Bindings.Hlgov, "seats": [4]common.Address{}, "startsequence": uint64(5), "predecessor": m3bAdmin}})
	r.chain.creation(tx, common.HexToAddress("0x0000000000000000000000000000000000000de9"), in2, succ, 80)
	if got := w.SuccessorCheck(ctx, at, m3bAdmin, 1, succ, want); !strings.Contains(got, " — "+VerdictUnshown) || !strings.Contains(got, "its argument startSequence = 5, but it must be 0") {
		t.Fatalf("a successor created with a number of its own:\n%s", got)
	}
}

// E2-RT10: a kind 8's call decoded against the implementation's ABI — the function and its arguments — or UNDECODED
// with why: no build's ABI, an unknown selector, arguments that do not decode or carry bytes beyond them.
func TestDecodeCall(t *testing.T) {
	a, err := abi.JSON(strings.NewReader(tokenABI))
	if err != nil {
		t.Fatal(err)
	}
	minter := common.HexToAddress("0x000000000000000000000000000000000000a11c")
	call, _ := a.Pack("initializeV2", minter)
	if got := DecodeCall(call, &a, "v2"); got != "then call initializeV2(address minter = "+minter.Hex()+") through ProxyAdmin.upgradeAndCall — read what it does before the vote" {
		t.Fatalf("decoded: %s", got)
	}
	if got := DecodeCall(nil, &a, "v2"); !strings.Contains(got, "no call") {
		t.Fatalf("no call: %s", got)
	}
	for name, c := range map[string]struct {
		call []byte
		abi  *abi.ABI
		why  string
	}{
		"no ABI":            {call, nil, "no reproducible build matches"},
		"an unknown one":    {[]byte{1, 2, 3, 4, 5}, &a, "is no function of the build v2"},
		"short arguments":   {call[:20], &a, "do not decode"},
		"a byte past them":  {append(append([]byte{}, call...), 0), &a, "not a canonical encoding"},
		"shorter than four": {[]byte{1}, &a, "shorter than a selector"},
	} {
		if got := DecodeCall(c.call, c.abi, "v2"); !strings.HasPrefix(got, "UNDECODED") || !strings.Contains(got, c.why) {
			t.Errorf("%s: %s", name, got)
		}
	}
}

// ----------------------------------------------------------------------------------- fold F5's two refusals --

// Fold F5's refusals of an execute (M2-NOTES.md F5.1 item 3): ForeignExec (the HlAdminExec an HlAdmin would delegate
// to is not the code its runtime pins) and ImplementationMoved (a kind 8's call re-pointed the proxy) — and
// AuthorityLost, read the same way — alarm at once, name what they mean, send nothing, and look again only hourly:
// no retry can make them pass, the decision pends until GovDAO cancels it.
func TestExecutorAlarmsOnFoldF5sRefusals(t *testing.T) {
	impl := common.HexToAddress("0x000000000000000000000000000000000000d00d")
	for name, rv := range map[string][]byte{
		"ForeignExec":         crypto.Keccak256([]byte("ForeignExec()"))[:4],
		"ImplementationMoved": append(crypto.Keccak256([]byte("ImplementationMoved(address)"))[:4], common.LeftPadBytes(ethGate.Bytes(), 32)...),
		"AuthorityLost":       append(crypto.Keccak256([]byte("AuthorityLost(address)"))[:4], common.LeftPadBytes(ethGate.Bytes(), 32)...),
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, 100, "primary")
			a := w.e.admins[ethAdmin]
			a.lastSeq = 1
			a.decisions[1] = &ethx.Decision{Number: 1, Kind: 8, DeliveredTime: w.e.now - 8*86_400, Params: kind8Params(ethGate, impl, common.Hash{7})}
			w.e.fail["execute"] = ethx.DecodeRevert(rv, "execution reverted")
			w.tick()
			text, ok := w.r.Alarm.Standing()["execute:execute:1"]
			if !ok || !strings.Contains(text, name) || !strings.Contains(text, "it pends until GovDAO cancels it") {
				t.Fatalf("no alarm naming %s: %v", name, w.r.Alarm.Standing())
			}
			asked := len(w.e.sentOf("execute")) // the fake records the estimate it refused
			w.advance(30 * time.Minute)
			w.tick()
			if at := w.r.state.Attempts["execute:1"]; at == nil || at.Tries != 1 || len(w.e.sentOf("execute")) != asked {
				t.Fatalf("asked again within the hour: %+v", at)
			}
			if _, parked := w.r.Alarm.Standing()["parked:execute:1"]; parked {
				t.Fatal("alarmed as a parked failure, not for what it means")
			}
			if a.decisions[1] == nil {
				t.Fatal("the decision was spent")
			}
		})
	}
}

// ------------------------------------------------------------------------------------------ the launch line --

// E2-RT6 by the launch line (M2-NOTES.md Fold F5, F5.1 item 12; PLAN.md §5.6): before the launch the deployer owned the
// mailbox and the tokens, minted 500 GNOT through a stand-in ISM and burned them back (nonce 0, block 30) — a token's
// own burn by every other test — and finishSetup recorded the mailbox's nonce, 1, as launchNonce(). The signer counts
// no message below the line: alice's burn (nonce 1) is signed beside an identity that holds, the deployer's is
// ignored, said why, and never signed; the inbound relayer never queues it. Counted (the line not read), it is 500
// uncredited burned against a lock that never held it: the identity fails and the signers stop — or, had the lock
// room for it, they would sign it and gno.land would release 500 GNOT nobody locked.
func TestNoBurnBelowTheLaunchLine(t *testing.T) {
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	deployer, _ := hl.GnoAddressToBytes32("g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5")
	prelaunch := func(c *m3bChain) {
		c.setSupply(m3bGNOT, 29, 1500)           // the stand-in ISM's mint
		c.burn(m3bGNOT, gnot, deployer, 500, 30) // and the burn back: nonce 0
		c.view(m3bAdmin, "launchNonce", func(n uint64, _ []any) []any {
			if n < 31 {
				return []any{uint32(0)} // finishSetup in block 31
			}
			return []any{uint32(1)}
		})
	}
	t.Run("signer", func(t *testing.T) {
		r := newSignerRig(t)
		prelaunch(r.chain)
		r.chain.burn(m3bGNOT, gnot, r.alice, 200, 80) // nonce 1: alice's
		r.round()
		st := r.status()
		ident := st["identity"].(map[string]any)["GNOT"].(map[string]any)
		if ident["verdict"] != "holds" || ident["uncredited"].(int64) != 200 || ident["supply"].(int64) != 800 {
			t.Fatalf("the identity counts the pre-launch burn: %v", ident)
		}
		if _, err := r.s.readSigned(0); err == nil {
			t.Fatal("the deployer's pre-launch burn was signed")
		}
		if _, err := r.s.readSigned(1); err != nil {
			t.Fatalf("alice's burn was not signed: %v", err)
		}
		if r.s.state.Index.Line != 1 || !strings.Contains(strings.Join(r.s.state.Index.Ignored, ";"), "message #0 is below the launch line 1") {
			t.Fatalf("the line %d, ignored %v", r.s.state.Index.Line, r.s.state.Index.Ignored)
		}
	})
	t.Run("an index scanned before the line drops the burn", func(t *testing.T) {
		r := newSignerRig(t)
		prelaunch(r.chain)
		// a scan that ended before finishSetup's block (a start guessed from a planted setupDone, say) indexed it
		if _, _, err := r.s.state.Index.catchUp(context.Background(), r.s.pair, r.s.Cfg.Eth.Mailbox, r.s.Cfg.Eth.Admin, m3bEthDom, m3bGnoDom, burnRoutes(r.s.Cfg, nil), 1, 30, r.now); err != nil {
			t.Fatal(err)
		}
		if len(r.s.state.Index.Burns) != 1 {
			t.Fatalf("the line 0 at block 30: %d burns indexed", len(r.s.state.Index.Burns))
		}
		if _, _, err := r.s.state.Index.catchUp(context.Background(), r.s.pair, r.s.Cfg.Eth.Mailbox, r.s.Cfg.Eth.Admin, m3bEthDom, m3bGnoDom, burnRoutes(r.s.Cfg, nil), 1, 100, r.now); err != nil {
			t.Fatal(err)
		}
		if len(r.s.state.Index.Burns) != 0 || !strings.Contains(strings.Join(r.s.state.Index.Ignored, ";"), "dropped from the index") {
			t.Fatalf("the burn below the line stays: %d, %v", len(r.s.state.Index.Burns), r.s.state.Index.Ignored)
		}
	})
	t.Run("a config without HlAdmin counts nothing", func(t *testing.T) {
		r := newSignerRig(t)
		r.s.Cfg.Eth.Admin = ""
		if _, _, err := r.s.state.Index.catchUp(context.Background(), r.s.pair, r.s.Cfg.Eth.Mailbox, "", m3bEthDom, m3bGnoDom, burnRoutes(r.s.Cfg, nil), 1, 100, r.now); err == nil || !strings.Contains(err.Error(), "the launch line") {
			t.Fatalf("scanned with no line: %v", err)
		}
	})
	t.Run("inbound relayer", func(t *testing.T) {
		r := newInboundRig(t)
		prelaunch(r.chain)
		r.chain.burn(m3bGNOT, gnot, r.alice, 200, 80)
		if err := r.tick(); err != nil {
			t.Fatal(err)
		}
		if len(r.job.st.Pending) != 1 || r.job.st.Pending[0].Nonce != 1 || r.job.st.Line != 1 {
			t.Fatalf("the queue %+v at the line %d", r.job.st.Pending, r.job.st.Line)
		}
	})
}

// E2-RT9's cut on a real node (the e2e found it): a gno node answers its latest state at any height — gno.land's realm
// objects live in an unversioned store — so a send landing between the routes' read and hlmailbox's would be counted in
// flight while missing from the lock read before it: a false halt of every signer. hlmailbox's count is read before
// the routes and again after; a send between them leaves the round without a verdict (no halt), and the next round,
// with nothing landing, holds with the send in flight and in the lock.
func TestSignerCutAroundALandingSend(t *testing.T) {
	r := newSignerRig(t) // GNOT supply 1,000 on Ethereum, the lock 1,000, nothing dispatched
	r.chain.view(m3bMailbox, "delivered", func(uint64, []any) []any { return []any{false} })
	landed := false
	r.gno.mu.Lock()
	r.gno.afterRender = func(pkg, path string) {
		if pkg == m3bNS+"hlgnot" && path == "json" && !landed {
			landed = true // the send lands just after the route was read: the lock 1,300 from now on
			r.pendingSend(300, 61)
			r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1300, 0, 0, 0))
		}
	}
	r.gno.mu.Unlock()
	r.round()
	if r.alarmed("signer:halt") || r.alarmed("signer:no-verdict") {
		t.Fatalf("a send landing mid-read halted the signer, or alarmed: %v", r.s.Alarm.Standing())
	}
	if st := r.status(); !strings.Contains(fmt.Sprint(st), "dispatched 1 message(s) while the routes were read") {
		t.Fatalf("the round took a verdict over a moving cut: %v", st)
	}
	r.step(time.Second)
	r.round()
	st := r.status()
	ident := st["identity"].(map[string]any)["GNOT"].(map[string]any)
	if r.alarmed("signer:halt") || st["signing"] != true || ident["verdict"] != "holds" || ident["undelivered"].(int64) != 300 || ident["lock"].(int64) != 1300 {
		t.Fatalf("the next round: %v", st)
	}
	// a cut that moves round after round (hlmailbox's count rising during every read) alarms at the fifth
	count := 1
	r.gno.mu.Lock()
	r.gno.afterRender = func(pkg, path string) {
		if pkg == m3bNS+"hlgnot" && path == "json" {
			count++
			r.gno.set(m3bMailPath, "json", fmt.Sprintf(`{"domain":1198419796,"live":true,"count":%d,"signers":{"keys":[],"threshold":2},"seats":[],"routes":{}}`, count))
		}
	}
	r.gno.mu.Unlock()
	for i := 1; i <= movingCutAlarm; i++ {
		r.step(time.Second)
		r.round()
		if got := r.alarmed("signer:no-verdict"); got != (i == movingCutAlarm) {
			t.Fatalf("moving round %d: alarmed %v", i, got)
		}
	}
}

// The same cut in the monitor, which reads hlmailbox before the routes: a send landing between them is in the lock and
// not yet counted in flight, so a forged mint of its amount would pass the poll unseen. The count read again after the
// routes leaves that poll without a verdict; the next one sees the forged mint (broken: flags).
func TestMonitorCutAroundALandingSend(t *testing.T) {
	r := newMonRig(t) // GNOT supply 1,000 on Ethereum, the lock 1,000, nothing dispatched
	r.chain.view(m3bMailbox, "delivered", func(uint64, []any) []any { return []any{false} })
	r.chain.setSupply(m3bGNOT, 1, 1300) // a forged mint of 300
	m := hl.Message{Version: 3, Nonce: 0, Origin: m3bGnoDom, Sender: gnoside.RealmKey(m3bNS + "hlgnot"), Destination: m3bEthDom, Recipient: ethx.AddressTo32(m3bGNOT), Body: hl.TokenMessage(ethx.AddressTo32(common.HexToAddress("0x00000000000000000000000000000000000a11ce")), 300, nil)}
	raw := m.Bytes()
	id := hl.ID(raw)
	landed := false
	r.gno.mu.Lock()
	r.gno.afterRender = func(pkg, path string) {
		if pkg == m3bMailPath && path == "json" && !landed {
			landed = true // a send of 300 lands just after hlmailbox was read: the lock 1,300 from now on
			r.gno.set(m3bMailPath, "msg/0", fmt.Sprintf(`{"nonce":0,"id":%q,"root":"%s","height":61,"message":%q}`, hex.EncodeToString(id[:]), strings.Repeat("00", 32), hex.EncodeToString(raw)))
			var mj map[string]any
			_ = json.Unmarshal([]byte(m3bMailboxJSON(nil, 2, []string{"", "", "", "", monWatchGno, ""})), &mj)
			mj["count"] = 1
			b, _ := json.Marshal(mj)
			r.gno.set(m3bMailPath, "json", string(b))
			r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1300, 0, 0, 0))
		}
	}
	r.gno.mu.Unlock()
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if v := fmt.Sprint(r.m.Status()); !strings.Contains(v, "dispatched 1 message(s) while the routes were read") {
		t.Fatalf("the poll took a verdict over a moving cut: %v", v)
	}
	if r.alarmed("identity:GNOT:flag") {
		t.Fatal("flagged on the moving cut")
	}
	r.step(time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if !r.alarmed("identity:GNOT:flag") {
		t.Fatalf("the forged mint of the landing send's amount passed: %v", r.m.Alarm.Standing())
	}
}

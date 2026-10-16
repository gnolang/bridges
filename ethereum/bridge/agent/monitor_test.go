package agent

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// m3bSender is the Ethereum watch key over the fake chain: a setFlag lands at the head's next block and stands on the
// object's flags from there.
type m3bSender struct {
	mu    sync.Mutex
	addr  common.Address
	rig   *monRig
	calls []string
}

func (s *m3bSender) Address() common.Address { return s.addr }

func (s *m3bSender) Send(_ context.Context, c ethx.Call) (*types.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rig
	r.chain.mu.Lock()
	block := uint64(len(r.chain.blocks)) // the next block
	r.chain.mu.Unlock()
	r.chain.mine(block, r.chain.finalized)
	name := "gate"
	if c.To == m3bAdmin {
		name = "admin"
	}
	s.calls = append(s.calls, fmt.Sprintf("%s@%d", name, block))
	r.setFlag(c.To, 4, block)
	return &types.Receipt{BlockNumber: new(big.Int).SetUint64(block), TxHash: crypto.Keccak256Hash([]byte(fmt.Sprint(block, name)))}, nil
}

func (s *m3bSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// monRig is a monitor over the fakes of both chains: GNOT locked 1,000 and minted 1,000; the client's record one
// header (its trusted header at height 50, recorded at block 10); the watch key in seat 5 of hlmailbox and in slot 5
// of the gate and HlAdmin.
type monRig struct {
	t      *testing.T
	chain  *m3bChain
	pa, pb *m3bProvider
	gno    *m3bGnoNode
	second *m3bGnoNode
	m      *Monitor
	snd    *m3bSender
	now    time.Time
	alice  [32]byte

	mu      sync.Mutex
	flags   map[common.Address]*[6]ethx.Flag
	running [32]byte
	records uint64
	recs    []monRec
	pending map[uint64]ethx.Decision
	lastSeq uint64
	trusted int64
}

const monWatchGno = "g1watchwatchwatchwatchwatchwatchwatchw"

var monWatchEth = common.HexToAddress("0x000000000000000000000000000000000000c0c5")

func newMonRig(t *testing.T) *monRig {
	t.Helper()
	r := &monRig{t: t, chain: newM3bChain(100), now: time.Unix(1_800_000_000, 0), flags: map[common.Address]*[6]ethx.Flag{m3bGate: {}, m3bAdmin: {}}, pending: map[uint64]ethx.Decision{}}
	// the chain keeps pace with the rig's clock: block 100, the head and the finalized block at the start, is 13 minutes
	// old (Ethereum finalizes 13–19 minutes behind its head) — a finalized block past half an hour old is a finality
	// stall the watch is blind over (E5-RT1); a test that steps the clock far mines along, or retimes the chain
	r.chain.retime(uint64(r.now.Unix()) - 12*100 - 13*60)
	r.chain.setSupply(m3bGNOT, 1, 1000)
	pair, pa, pb := m3bPair(r.chain)
	r.pa, r.pb = pa, pb
	r.gno = newM3bGno(60, r.now.Add(time.Second))
	r.second = newM3bGno(60, r.now.Add(time.Second))
	r.gno.set(m3bMailPath, "json", m3bMailboxJSON(nil, 2, []string{"", "", "", "", monWatchGno, ""}))
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 0, 0, 0))
	r.gno.set(m3bNS+"hlbubble", "json", m3bRouteJSON(0, 0, 0, 0))
	r.gno.set(m3bNS+"hlgov", "json", `{"last":0,"next":1}`)
	a, _ := hl.GnoAddressToBytes32("g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5")
	r.alice = a
	// the client: its trusted header at height 50, recorded at block 10 (the record's first entry)
	r.trusted = 50
	r.recordHeader(50, 10, r.nodeHash(50))
	r.chain.view(m3bClient, "trustedHeight", func(uint64, []any) []any { r.mu.Lock(); defer r.mu.Unlock(); return []any{r.trusted} })
	r.chain.view(m3bClient, "trustedTime", func(uint64, []any) []any { return []any{uint64(r.now.Unix())} })
	r.chain.view(m3bClient, "trustedHash", func(uint64, []any) []any { return []any{r.nodeHash(50)} })
	r.chain.view(m3bClient, "records", func(n uint64, _ []any) []any { c, _ := r.recordAt(n); return []any{c} })
	r.chain.view(m3bClient, "runningHash", func(n uint64, _ []any) []any { _, h := r.recordAt(n); return []any{h} })
	keys := [6]common.Address{common.HexToAddress("0x01"), common.HexToAddress("0x02"), common.HexToAddress("0x03"), common.HexToAddress("0x04"), monWatchEth, common.HexToAddress("0x06")}
	for _, obj := range []common.Address{m3bGate, m3bAdmin} {
		obj := obj
		r.chain.view(obj, "keys", func(uint64, []any) []any { return []any{keys} })
		r.chain.view(obj, "flags", func(n uint64, _ []any) []any { return []any{r.flagsAt(obj, n)} })
	}
	r.chain.view(m3bGate, "limits", func(uint64, []any) []any {
		return []any{struct {
			MaxPerMessage uint64 "json:\"maxPerMessage\""
			DayCap        uint64 "json:\"dayCap\""
			Used          uint64 "json:\"used\""
			UsedAt        uint64 "json:\"usedAt\""
		}{25_000_000_000, 100_000_000_000, 0, 0}}
	})
	r.chain.view(m3bAdmin, "lastSeq", func(uint64, []any) []any { r.mu.Lock(); defer r.mu.Unlock(); return []any{r.lastSeq} })
	r.chain.view(m3bAdmin, "clockFactor", func(uint64, []any) []any { return []any{uint64(1)} })
	r.chain.view(m3bAdmin, "decision", func(_ uint64, in []any) []any {
		r.mu.Lock()
		defer r.mu.Unlock()
		d := r.pending[in[0].(uint64)]
		return []any{struct {
			Number         uint64         "json:\"number\""
			Kind           uint8          "json:\"kind\""
			DeliveredBlock uint64         "json:\"deliveredBlock\""
			DeliveredTime  uint64         "json:\"deliveredTime\""
			Deliverer      common.Address "json:\"deliverer\""
			Params         []byte         "json:\"params\""
		}{d.Number, d.Kind, d.DeliveredBlock, d.DeliveredTime, d.Deliverer, d.Params}}
	})
	cfg := m3bConfig()
	cfg.Gno.WatchKey, cfg.Gno.WatchAddress = "watch", monWatchGno
	cfg.Gno.DAO = gnoside.DAOPath // as LoadConfig defaults it: its list read, empty (E4-GT5: a list that does not read is said)
	r.gno.set(gnoside.DAOPath, "", "# GovDAO\n\nno proposals yet\n")
	r.snd = &m3bSender{addr: monWatchEth, rig: r}
	r.m = &Monitor{Cfg: cfg, Alarm: &Alarmer{Source: "test"}, Every: 10 * time.Second, pair: pair, own: r.gno, watch: r.gno, sources: []gnoside.Reader{r.second}, ethWatch: r.snd, now: func() time.Time { return r.now }}
	if err := r.m.init(); err != nil {
		t.Fatal(err)
	}
	r.m.state.From = 1
	r.m.state.LastDelivery = r.now.Unix()
	return r
}

// nodeHash is the fake node's block hash at a height, as 32 bytes.
func (r *monRig) nodeHash(h int64) [32]byte {
	var out [32]byte
	b, _ := hex.DecodeString(fmt.Sprintf("%064x", h))
	copy(out[:], b)
	return out
}

// recordHeader records a header in the client at a block: its HeaderRecorded log, the counter and the running hash.
func (r *monRig) recordHeader(height int64, block uint64, hash [32]byte) {
	r.mu.Lock()
	r.records++
	r.running = recordRunning(r.running, hash, height, block)
	n, running := r.records, r.running
	r.recs = append(r.recs, monRec{block: block, n: n, h: running})
	r.mu.Unlock()
	data, err := ethx.LightClient.ABI.Events["HeaderRecorded"].Inputs.NonIndexed().Pack(uint64(0), n, running, false)
	if err != nil {
		r.t.Fatal(err)
	}
	var ht common.Hash
	binary.BigEndian.PutUint64(ht[24:], uint64(height))
	r.chain.log(types.Log{Address: m3bClient, Topics: []common.Hash{ethx.LightClient.Event("HeaderRecorded"), common.Hash(hash), ht}, Data: data, BlockNumber: block, TxHash: crypto.Keccak256Hash([]byte(fmt.Sprint("rec", n)))})
}

type monRec struct {
	block uint64
	n     uint64
	h     [32]byte
}

// recordAt is the client's counter and running hash as of a block.
func (r *monRig) recordAt(block uint64) (uint64, [32]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n uint64
	var h [32]byte
	for _, rc := range r.recs {
		if rc.block <= block {
			n, h = rc.n, rc.h
		}
	}
	return n, h
}

func (r *monRig) setFlag(obj common.Address, slot int, block uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flags[obj][slot] = ethx.Flag{BlockNumber: block, Time: uint64(r.now.Unix())}
}

func (r *monRig) clearFlag(obj common.Address, slot int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flags[obj][slot] = ethx.Flag{}
}

// flagsAt are an object's flags as a read at a block sees them (a flag set above it is not there yet).
func (r *monRig) flagsAt(obj common.Address, n uint64) [6]ethx.Flag {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [6]ethx.Flag
	for i, f := range r.flags[obj] {
		if f.BlockNumber != 0 && f.BlockNumber <= n {
			out[i] = f
		}
	}
	return out
}

// decide records decision n on HlAdmin at a block, delivered through Mailbox.process with the body given, and r/hlgov's
// dispatch of govBody under the same number ("" for none); pending keeps it in HlAdmin's pending list.
func (r *monRig) decide(n uint64, body []byte, block uint64, govBody []byte, pending bool) {
	r.mu.Lock()
	if n > r.lastSeq {
		r.lastSeq = n
	}
	if pending {
		r.pending[n] = ethx.Decision{Number: n, Kind: body[0], DeliveredBlock: block, DeliveredTime: uint64(r.now.Unix()), Params: body[9:]}
	}
	r.mu.Unlock()
	msg := hl.Message{Version: 3, Nonce: uint32(n), Origin: m3bGnoDom, Sender: gnoside.RealmKey(m3bNS + "hlgov"), Destination: m3bEthDom, Recipient: ethx.AddressTo32(m3bAdmin), Body: body}
	input, err := ethx.Mailbox.Pack("process", []byte{}, msg.Bytes())
	if err != nil {
		r.t.Fatal(err)
	}
	tx := crypto.Keccak256Hash([]byte(fmt.Sprint("decision", n, block)))
	r.chain.tx(tx, m3bMailbox, input)
	data, _ := ethx.Admin.ABI.Events["DecisionRecorded"].Inputs.NonIndexed().Pack(body[0], uint64(0))
	var nt common.Hash
	binary.BigEndian.PutUint64(nt[24:], n)
	r.chain.log(types.Log{Address: m3bAdmin, Topics: []common.Hash{ethx.Admin.Event("DecisionRecorded"), nt}, Data: data, BlockNumber: block, TxHash: tx})
	if govBody != nil {
		j, _ := json.Marshal(map[string]any{"number": n, "kind": govBody[0], "body": hex.EncodeToString(govBody), "id": "", "count": 1, "at": r.now.Unix()})
		r.gno.set(m3bNS+"hlgov", "decision/"+fmt.Sprint(n), string(j))
		r.gno.set(m3bNS+"hlgov", "json", fmt.Sprintf(`{"last":%d,"next":%d}`, n, n+1))
	}
}

// body is a decision body: kind ‖ number ‖ params.
func body(kind byte, n uint64, params ...byte) []byte {
	b := []byte{kind}
	b = binary.BigEndian.AppendUint64(b, n)
	return append(b, params...)
}

func (r *monRig) step(d time.Duration) {
	r.now = r.now.Add(d)
	r.gno.advance(r.gno.height+1, r.now.Add(time.Second))
	r.second.advance(r.second.height+1, r.now.Add(time.Second))
}

func (r *monRig) poll() error {
	r.t.Helper()
	return r.m.Step(context.Background())
}

func (r *monRig) alarmed(key string) bool {
	_, ok := r.m.Alarm.Standing()[key]
	return ok
}

func (r *monRig) gnoFlags() []string { return r.gno.callsOf("SetFlag") }

// The identity over the cut: a burn in flight and a send in flight are counted exactly; a supply above the lock (a
// forged mint) flags credits and payouts on gno.land and the gate on Ethereum — once, never on a timer.
func TestMonitorIdentityFlagsABreak(t *testing.T) {
	r := newMonRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.burn(m3bGNOT, gnot, r.alice, 200, 80) // supply 800, 200 in flight
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	id := r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)
	if id["ok"] != true || id["inflightIn"].(float64) != 200 || id["supply"].(float64) != 800 {
		t.Fatalf("the identity with a burn in flight: %v (%v)", id, r.m.Status()["cut"])
	}
	if len(r.gnoFlags()) != 0 || r.snd.count() != 0 {
		t.Fatal("flagged a held identity")
	}
	// a forged mint of 500: BROKEN — credits and payouts on hlgnot, and the gate
	r.chain.mine(110, 110)
	r.chain.setSupply(m3bGNOT, 105, 1300)
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if f := r.gnoFlags(); len(f) != 2 || !strings.Contains(f[0], "hlgnot,credits") || !strings.Contains(f[1], "hlgnot,payouts") {
		t.Fatalf("the gno flags: %v", f)
	}
	if r.snd.count() != 1 || !strings.HasPrefix(r.snd.calls[0], "gate@") {
		t.Fatalf("the gate was not flagged once: %v", r.snd.calls)
	}
	if !r.alarmed("identity:GNOT:flag") {
		t.Fatal("no flag alarm")
	}
	// the break stands: the gate's flag stands (pending finality at first, then read at a final block): no re-set
	r.gno.set(m3bMailPath, "json", m3bMailboxJSON(nil, 2, []string{"", "", "", "", monWatchGno, ""}))
	for i := 0; i < 5; i++ {
		r.step(10 * time.Second)
		if i == 2 {
			r.chain.mine(130, 125) // the flag's block becomes final
		}
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
	}
	if r.snd.count() != 1 {
		t.Fatalf("the gate's flag was re-asserted on a timer: %v", r.snd.calls)
	}
}

// §5.5's rule for the watch's flags on Ethereum: set again only when found cleared, or when an unpause recorded at a
// final block would clear it — never on a timer (pure, then through the monitor).
func TestMonitorFlagResetOnlyOnARecordedUnpause(t *testing.T) {
	set := ethx.Flag{BlockNumber: 120}
	for _, c := range []struct {
		name     string
		slot     int
		flag     ethx.Flag
		sent     uint64
		unpauses []uint64
		want     bool
	}{
		{"not set", 4, ethx.Flag{}, 0, nil, true},
		{"standing, nothing recorded", 4, set, 0, nil, false},
		{"standing, an unpause delivered before it", 4, set, 0, []uint64{110}, false},
		{"standing, an unpause delivered at its block", 4, set, 0, []uint64{120}, false},
		{"standing, an unpause delivered after it", 4, set, 0, []uint64{130}, true},
		{"our setFlag above the final block, an unpause below it", 4, ethx.Flag{}, 140, []uint64{130}, false},
		{"no slot", -1, ethx.Flag{}, 0, nil, false},
	} {
		if got, why := flagNeed(c.slot, c.flag, c.sent, c.unpauses); got != c.want {
			t.Errorf("%s: %v (%s)", c.name, got, why)
		}
	}
	r := newMonRig(t)
	r.chain.mine(150, 150)
	r.chain.setSupply(m3bGNOT, 140, 1300) // a standing break
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if r.snd.count() != 1 {
		t.Fatalf("the break did not flag the gate: %v", r.snd.calls)
	}
	flagBlock := uint64(151)
	r.chain.mine(160, 160) // the flag's block is final
	r.step(10 * time.Second)
	_ = r.poll()
	if r.snd.count() != 1 {
		t.Fatalf("re-asserted with nothing recorded: %v", r.snd.calls)
	}
	// HlAdmin records an unpause of the gate at block 158 (after the flag): it would clear the flag in 24 hours — the
	// watch sets it again, once that block is final
	r.decide(1, body(2, 1, 0), 158, body(2, 1, 0), true)
	r.step(10 * time.Second)
	_ = r.poll()
	if r.snd.count() != 2 || !strings.HasPrefix(r.snd.calls[1], "gate@") {
		t.Fatalf("not re-set after a recorded unpause: %v", r.snd.calls)
	}
	_ = flagBlock
	r.chain.mine(175, 175)
	r.step(10 * time.Second)
	_ = r.poll()
	if r.snd.count() != 2 {
		t.Fatalf("re-set again with the same unpause: %v", r.snd.calls)
	}
	// someone's unpause executed and cleared it: found cleared, set again
	r.clearFlag(m3bGate, 4)
	r.step(10 * time.Second)
	_ = r.poll()
	if r.snd.count() != 3 {
		t.Fatalf("a cleared flag was not set again: %v", r.snd.calls)
	}
}

// The fork rule: a header the client recorded that differs from this node's block and the second source's at its
// height is a mismatch — alarmed at once, both objects flagged once it has stood two minutes, the cut named; a header
// only this node lacks is the sources rule's (an alarm).
func TestMonitorHeaderMismatchFlagsBothObjects(t *testing.T) {
	r := newMonRig(t)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if mm := r.m.state.Record.Mismatch; mm != nil || r.m.state.Record.Checked != 1 {
		t.Fatalf("the trusted header: %+v checked %d", mm, r.m.state.Record.Checked)
	}
	// a forged header at height 58 recorded at block 105
	r.chain.mine(110, 110)
	r.recordHeader(58, 105, [32]byte{0xf0, 0x12})
	r.step(10 * time.Second)
	_ = r.poll()
	mm := r.m.state.Record.Mismatch
	if mm == nil || mm.Height != 58 || mm.Block != 105 {
		t.Fatalf("no mismatch: %+v", r.m.state.Record)
	}
	if !r.alarmed("header:mismatch") || r.snd.count() != 0 {
		t.Fatalf("flagged before it stood two minutes: %v", r.snd.calls)
	}
	r.step(2 * time.Minute)
	_ = r.poll()
	if r.snd.count() != 2 || !strings.HasPrefix(r.snd.calls[0], "gate@") || !strings.HasPrefix(r.snd.calls[1], "admin@") {
		t.Fatalf("both objects were not flagged: %v", r.snd.calls)
	}
	if !strings.Contains(r.m.Alarm.Standing()["header:mismatch"], "the cut is block 105") {
		t.Fatalf("the cut is not named: %v", r.m.Alarm.Standing()["header:mismatch"])
	}
	// a header the second source has and this node lacks: the own node is odd, an alarm and no mismatch
	r2 := newMonRig(t)
	r2.chain.mine(110, 110)
	odd := [32]byte{0xab}
	r2.second.hashes[59] = hex.EncodeToString(odd[:])
	r2.recordHeader(59, 105, odd)
	r2.step(10 * time.Second)
	_ = r2.poll()
	if r2.m.state.Record.Mismatch != nil || !r2.alarmed("header:own-odd") {
		t.Fatalf("own node odd read as a fork: %+v %v", r2.m.state.Record.Mismatch, r2.m.Alarm.Standing())
	}
}

// A gap: the client's counter past the record rebuilt (an event the scan never saw), flagged after two minutes.
func TestMonitorRecordGap(t *testing.T) {
	r := newMonRig(t)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.chain.mine(110, 110)
	r.mu.Lock()
	r.records++ // the client counts a header no event shows
	r.recs = append(r.recs, monRec{block: 105, n: r.records, h: [32]byte{9}})
	r.mu.Unlock()
	r.step(10 * time.Second)
	_ = r.poll()
	if !r.alarmed("header:gap:pending") || r.snd.count() != 0 {
		t.Fatalf("the gap: %v %v", r.m.Alarm.Standing(), r.snd.calls)
	}
	r.step(2 * time.Minute)
	_ = r.poll()
	if r.snd.count() != 2 {
		t.Fatalf("the gap did not flag both objects: %v", r.snd.calls)
	}
}

// A stage for a header gno.land does not have alarms (§6.3); one for a real header does not.
func TestMonitorStageForAnUnknownHeader(t *testing.T) {
	r := newMonRig(t)
	// relay: the staging call went through another contract (fold F4: the event carries the height, so it is judged
	// like any other; the watch no longer reads the call's leaves)
	stage := func(height int64, tamper bool, block uint64, key byte, relay bool) {
		h := &gnolight.Header{ChainID: "dev", Height: height}
		leaves := h.Leaves()
		var ls [][]byte
		for _, l := range leaves {
			ls = append(ls, l)
		}
		hash, _ := gnolight.SimpleHash(ls)
		if !tamper {
			r.gno.hashes[height] = hex.EncodeToString(hash[:])
			r.second.hashes[height] = hex.EncodeToString(hash[:])
		}
		input, err := ethx.LightClient.Pack("update", ls, ethx.Commit{PartsTotal: 1, Round: 0, Precommits: []ethx.Precommit{}}, []ethx.Validator{}, []ethx.Validator{})
		if err != nil {
			t.Fatal(err)
		}
		tx := crypto.Keccak256Hash([]byte{key})
		to := m3bClient
		if relay {
			to, input = common.HexToAddress("0x00000000000000000000000000000000000000e1"), []byte{0xde, 0xad}
		}
		r.chain.tx(tx, to, input)
		data, _ := ethx.LightClient.ABI.Events["StageProgress"].Inputs.NonIndexed().Pack(hash, height, int64(60), int64(360))
		r.chain.log(types.Log{Address: m3bClient, Topics: []common.Hash{ethx.LightClient.Event("StageProgress"), {key}, ethx.AddressTopic(common.HexToAddress("0xbad"))}, Data: data, BlockNumber: block, TxHash: tx})
	}
	r.chain.mine(110, 110)
	stage(55, false, 101, 1, false) // a real header of the chain
	stage(57, true, 102, 2, false)  // a header gno.land never had
	stage(58, false, 103, 3, true)  // a real header, staged through a relay contract
	stage(59, true, 104, 4, true)   // a header gno.land never had, staged through a relay contract
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	var unknown []string
	for k := range r.m.Alarm.Standing() {
		if strings.HasPrefix(k, "stage:unknown:") {
			unknown = append(unknown, k)
		}
		if strings.HasPrefix(k, "stage:unread:") {
			t.Fatalf("a stage made through a contract is unread: %v", r.m.Alarm.Standing())
		}
	}
	if len(unknown) != 2 {
		t.Fatalf("the unknown stage alarms: %v (stages %+v)", r.m.Alarm.Standing(), r.m.state.Stages.Seen)
	}
	if st := r.m.state.Stages.Seen[hexOf([32]byte{3})]; st.Verdict != "ok" || st.Height != 58 {
		t.Fatalf("the relayed stage of a real header: %+v", st)
	}
	if r.snd.count() != 0 {
		t.Fatal("a stage flagged (it alarms only)")
	}
}

// A decision HlAdmin recorded that r/hlgov never dispatched, or whose body is not r/hlgov's, is foreign: both objects
// flagged once it has stood two minutes; r/hlgov's own decisions are not.
func TestMonitorForeignDecisions(t *testing.T) {
	r := newMonRig(t)
	r.chain.mine(110, 110)
	r.decide(1, body(7, 1, make([]byte, 40)...), 101, body(7, 1, make([]byte, 40)...), true) // r/hlgov's own
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if r.alarmed("decision:foreign:pending") || r.snd.count() != 0 {
		t.Fatalf("r/hlgov's own decision read as foreign: %v", r.m.Alarm.Standing())
	}
	// number 2: a cancel r/hlgov never dispatched (a forged header's), executed at recording (not pending)
	r.chain.mine(120, 120)
	r.decide(2, body(1, 2, 0, 0, 0, 0, 0, 0, 0, 9, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1), 115, nil, false)
	r.step(10 * time.Second)
	_ = r.poll()
	if !r.alarmed("decision:foreign:pending") {
		t.Fatalf("the foreign decision is not alarmed: %v", r.m.Alarm.Standing())
	}
	r.step(2 * time.Minute)
	_ = r.poll()
	if r.snd.count() != 2 {
		t.Fatalf("a foreign decision did not flag both objects: %v", r.snd.calls)
	}
	// a body that is not r/hlgov's under r/hlgov's number (same kind): foreign too
	r3 := newMonRig(t)
	r3.chain.mine(110, 110)
	r3.decide(1, body(7, 1, append(make([]byte, 39), 1)...), 101, body(7, 1, make([]byte, 40)...), false)
	_ = r3.poll()
	r3.step(2 * time.Minute)
	_ = r3.poll()
	if r3.snd.count() != 2 || !strings.Contains(r3.m.Alarm.Standing()["decision:foreign"], "not r/hlgov's") {
		t.Fatalf("a body that is not r/hlgov's was not flagged: %v %v", r3.snd.calls, r3.m.Alarm.Standing())
	}
}

// Two providers that disagree are no verdict: nothing is judged, nothing flagged.
func TestMonitorProvidersDisagree(t *testing.T) {
	r := newMonRig(t)
	r.chain.mine(110, 110)
	r.chain.setSupply(m3bGNOT, 105, 1300) // a break, which one provider hides
	r.pb.supplyX = func(n uint64, v *big.Int) *big.Int { return big.NewInt(1000) }
	for i := 0; i < 15; i++ {
		_ = r.poll()
		r.step(10 * time.Second)
	}
	if r.snd.count() != 0 || len(r.gnoFlags()) != 0 {
		t.Fatal("flagged on two providers that disagree")
	}
	if !r.alarmed("eth:providers") {
		t.Fatalf("the disagreement is not alarmed: %v", r.m.Alarm.Standing())
	}
}

// A diverted credit — a claim naming another recipient or amount than the burn — breaks the identity's blind spot:
// credits and payouts flagged, the gate too; a forged credit (a waiting claim no burn backs) likewise once the burn
// index's finalized block's time has passed the credit's (E4-GT1: before, an honest burn above the index may be it).
func TestMonitorDivertedAndForgedCredits(t *testing.T) {
	r := newMonRig(t)
	id := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 200, 80)
	r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(id[:]), "60") // at the head (a mark above it is no one node's, E6-GT2)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 200, 0, 0))
	r.gno.set(m3bNS+"hlgnot", "claim/"+hex.EncodeToString(id[:]), `{"id":"`+hex.EncodeToString(id[:])+`","to":"g1thief000000000000000000000000000000000","amount":200,"state":"open"}`)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if !r.alarmed("diverted:"+hex.EncodeToString(id[:])+":flag") || len(r.gnoFlags()) != 2 || r.snd.count() != 1 {
		t.Fatalf("a diverted credit: %v %v %v", r.m.Alarm.Standing(), r.gnoFlags(), r.snd.calls)
	}
	r2 := newMonRig(t)
	forged := strings.Repeat("ab", 32)
	// the identity holds (the supply 700: say 300 burned toward another chain's bridge, or a mistaken void) — the
	// unindexed rule alone sees a claim no burn backs
	r2.chain.setSupply(m3bGNOT, 2, 700)
	r2.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r2.gno.set(m3bMailPath, "delivered/"+forged, "59") // a forged credit is a delivery like any (hlmailbox's done)
	r2.gno.set(m3bNS+"hlgnot", "claims/0", `[{"id":"`+forged+`","to":"g1thief000000000000000000000000000000000","amount":300,"state":"open","height":59}]`)
	r2.gno.set(m3bNS+"hlgnot", "claim/"+forged, `{"id":"`+forged+`","to":"g1thief000000000000000000000000000000000","amount":300,"state":"open","height":59}`)
	_ = r2.poll() // credited at the gno head's time, after the index's finalized block (1,700,001,200): it waits
	if len(r2.gnoFlags()) != 0 {
		t.Fatal("a forged credit flagged before the index's finalized block's time passed the credit's")
	}
	r2.chain.retime(uint64(r2.now.Unix())) // the finalized block 100 now 20 minutes after the credit
	r2.step(10 * time.Second)
	if err := r2.poll(); err != nil {
		t.Fatal(err)
	}
	if !r2.alarmed("credit:unindexed:"+forged+":flag") || len(r2.gnoFlags()) == 0 {
		t.Fatalf("a forged credit was not flagged once the index's block time passed it: %v %v", r2.m.Alarm.Standing(), r2.gnoFlags())
	}
}

// Deliveries paused on Ethereum (a flag on the gate) set sends on the gno routes; resumed, the watch clears its own.
func TestMonitorMirrorsAPauseAsSends(t *testing.T) {
	r := newMonRig(t)
	r.setFlag(m3bGate, 0, 90) // a guardian's flag on the gate
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if f := r.gno.callsOf("SetFlag"); len(f) != 2 || !strings.Contains(f[0], ",sends,") {
		t.Fatalf("the sends flags: %v", f)
	}
	r.clearFlag(m3bGate, 0)
	r.gno.set(m3bMailPath, "json", `{"domain":1198419796,"live":true,"seats":["","","","","`+monWatchGno+`",""],"signers":{"keys":[],"threshold":2},"routes":{"`+m3bNS+`hlgnot":{"flags":["sends by seat 5 (`+monWatchGno+`) at 1"]},"`+m3bNS+`hlbubble":{"flags":["sends by seat 5 (`+monWatchGno+`) at 1"]}}}`)
	r.step(10 * time.Second)
	_ = r.poll()
	if c := r.gno.callsOf("ClearFlag"); len(c) != 2 {
		t.Fatalf("the watch did not clear its sends flags: %v", r.gno.calls)
	}
}

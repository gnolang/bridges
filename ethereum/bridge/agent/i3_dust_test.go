package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The integration lane I3's follow-up 1 (F17a's review R1): the relayers leave a burn below its route's fee (E5-GS5), so
// such a burn stays uncredited for good — and every Ethereum signer's round and every monitor's poll read its delivered
// mark again, one render each, for good, while the monitors counted it waiting (`age` standing for good, `stall` after
// each quiet half hour). The Solana machines' handling carried over (lane S6: 1df7a1c7, 50f5c01e, 1aea555f): a mark read
// unset is read again only once hlmailbox's delivered count moved (a pile of them as the done tree, a hundred ids a
// vm/qeval), every one again while a cut is in doubt and every ten minutes; dust waits for nobody on the monitors; the
// relayer keeps it pending and judges it against the fee each tick, so a fee lowered past it makes it a delivery again.

// i3QNode is the fake gno node that also answers the done tree's vm/qeval (hlmailbox's delivered ids by offset, as
// gnoside.DoneQuery asks), and counts those reads and its delivered-mark renders.
type i3QNode struct {
	*m3bGnoNode
	cmu   sync.Mutex
	evals int
	marks int
}

func (q *i3QNode) RenderAt(ctx context.Context, pkg, path string, h int64) (string, error) {
	if strings.HasPrefix(path, "delivered/") {
		q.cmu.Lock()
		q.marks++
		q.cmu.Unlock()
	}
	return q.m3bGnoNode.RenderAt(ctx, pkg, path, h)
}

var i3DoneRE = regexp.MustCompile(`^(.*)\.func\(\) string \{ s := ""; done\.IterateByOffset\((\d+), (\d+),`)

func (q *i3QNode) Query(_ context.Context, path string, data []byte, _ int64) ([]byte, error) {
	q.cmu.Lock()
	q.evals++
	q.cmu.Unlock()
	m := i3DoneRE.FindSubmatch(data)
	if path != "vm/qeval" || m == nil || string(m[1]) != m3bMailPath {
		return nil, fmt.Errorf("fake: no answer to %s %.120s", path, data)
	}
	from, _ := strconv.Atoi(string(m[2]))
	count, _ := strconv.Atoi(string(m[3]))
	g := q.m3bGnoNode
	g.mu.Lock()
	defer g.mu.Unlock()
	prefix := m3bMailPath + ":delivered/"
	var ids []string
	for k, v := range g.renders {
		if v := strings.TrimSpace(v); strings.HasPrefix(k, prefix) && v != "0" && v != "" {
			ids = append(ids, k[len(prefix):])
		}
	}
	sort.Strings(ids)
	var b strings.Builder
	for i := from; i < len(ids) && i < from+count; i++ {
		b.WriteString(ids[i] + ":" + strings.TrimSpace(g.renders[prefix+ids[i]]) + ",")
	}
	return []byte(`("` + b.String() + `" string)`), nil
}

// take answers the marks rendered and the done tree's reads since the last take.
func (q *i3QNode) take() string {
	q.cmu.Lock()
	defer q.cmu.Unlock()
	s := fmt.Sprintf("%d renders + %d qevals", q.marks, q.evals)
	q.marks, q.evals = 0, 0
	return s
}

const i3None = "0 renders + 0 qevals"

// i3Num is a number in one of a status's maps (the status is published as JSON: maps of any, numbers float64).
func i3Num(st map[string]any, key, sub string) float64 {
	m, _ := st[key].(map[string]any)
	f, _ := m[sub].(float64)
	return f
}

// i3Route is a GNOT route json with its fee given (base units).
func i3Route(lock, owed, fee int64) string {
	b, _ := json.Marshal(map[string]any{"live": true, "lock": lock, "owed": owed, "voided": 0, "lost": 0, "fee": fee})
	return string(b)
}

// i3Mailbox is hlmailbox's json as the monitor rig renders it, its "delivered" count given (the fake counts the marks
// set instead when any is: withDelivered).
func i3Mailbox(delivered int) string {
	var j map[string]any
	_ = json.Unmarshal([]byte(m3bMailboxJSON(nil, 2, []string{"", "", "", "", monWatchGno, ""})), &j)
	j["delivered"] = delivered
	b, _ := json.Marshal(j)
	return string(b)
}

// A signer's round read the delivered mark of every burn its index holds uncredited: 300 burns of one base unit — dust
// the relayers leave (E5-GS5), a stranger's at an Ethereum fee each — were 300 renders every round, for good. A mark
// read unset is read again only once hlmailbox's delivered count moved (a mark is set only with its rise), and a pile of
// them is read as the done tree, a hundred ids a vm/qeval.
func TestI3SignerReadsUncreditedMarksOnce(t *testing.T) {
	r := newSignerRig(t)
	q := &i3QNode{m3bGnoNode: r.gno}
	r.s.gno = q
	r.round()
	r.chain.mine(110, 110) // the burns lie above the index's cursor, block 100 after the first round
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	const n = 300
	var ids [][32]byte
	for i := 0; i < n; i++ {
		ids = append(ids, r.chain.burn(m3bGNOT, gnot, r.alice, 1, 105))
	}
	var per []string
	for i := 0; i < 5; i++ {
		if i == 3 { // someone brings one at his own cost: the count moves, the marks are read once more
			r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(ids[7][:]), "61")
			r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 1, 0, 0))
		}
		r.step(time.Second)
		q.take()
		r.round()
		per = append(per, q.take())
	}
	st := r.status()
	id7 := hex.EncodeToString(ids[7][:])
	_, held := r.s.state.Index.Burns[id7]
	t.Logf("%d dust burns: delivered-mark reads a round %v; signing %v; #7 held %v, credited burns pruned %d", n, per, st["signing"], held, r.s.state.PrunedCredited)
	if st["signing"] != true {
		t.Fatalf("the signer does not sign over the dust: %v", st)
	}
	if per[1] != i3None || per[2] != i3None || per[4] != i3None {
		t.Fatalf("every round read the marks of the burns nobody credits again: %v", per)
	}
	if per[0] == i3None || per[3] == i3None || held || r.s.state.PrunedCredited != 1 {
		t.Fatalf("the marks were not read when they could have moved (the first round, the count moved): %v; #7 held %v, pruned %d", per, held, r.s.state.PrunedCredited)
	}
	if per[0] != "0 renders + 1 qevals" {
		t.Fatalf("300 marks were not read as the done tree (one vm/qeval): %v", per)
	}
}

// The monitor's poll the same — and hlwatch's reading, the monitor's own — and every mark is read again ten minutes on.
func TestI3MonitorReadsUncreditedMarksOnce(t *testing.T) {
	r := newMonRig(t)
	q := &i3QNode{m3bGnoNode: r.gno}
	r.m.own = q
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.chain.mine(110, 110) // the burns lie above the index's cursor, block 100 after the first poll
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	const n = 300
	for i := 0; i < n; i++ {
		r.chain.burn(m3bGNOT, gnot, r.alice, 1, 105)
	}
	var per []string
	for i := 0; i < 4; i++ {
		r.step(10 * time.Second)
		if i == 3 {
			r.step(10 * time.Minute) // the refresh: every uncredited mark read again whatever the count
		}
		q.take()
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		per = append(per, q.take())
	}
	v := r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)["verdict"]
	t.Logf("%d dust burns: delivered-mark reads a poll %v; verdict %v", n, per, v)
	if v != "holds" {
		t.Fatalf("the identity over the dust: %v (%v)", v, r.m.Status()["cut"])
	}
	if per[1] != i3None || per[2] != i3None {
		t.Fatalf("every poll read the marks of the burns nobody credits again: %v", per)
	}
	if per[0] != "0 renders + 1 qevals" || per[3] != "0 renders + 1 qevals" {
		t.Fatalf("the first poll and the ten minutes' refresh did not read the 300 marks as the done tree: %v", per)
	}
}

// The monitors counted every uncredited burn waiting: a burn below its route's fee, which the relayers leave to whoever
// pays for it, raised `age` for good and `stall` after every quiet half hour. It waits for nobody; a burn of the fee still
// does.
func TestI3MonitorDustWaitsForNobody(t *testing.T) {
	r := newMonRig(t)
	r.gno.set(m3bNS+"hlgnot", "json", i3Route(1000, 0, 100))
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.chain.mine(110, 110)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	dust := r.chain.burn(m3bGNOT, gnot, r.alice, 99, 105) // below the fee of 100
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	rec := r.m.state.Index.Burns[hex.EncodeToString(dust[:])]
	if rec == nil {
		t.Fatalf("the dust burn is not indexed: %v", r.m.Status()["cut"])
	}
	rec.Seen = r.now.Add(-3 * time.Hour).Unix() // uncredited for three hours, and no delivery for one
	r.m.state.LastDelivery = r.now.Add(-time.Hour).Unix()
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	st := r.m.Status()
	t.Logf("a burn of 99 under a fee of 100, three hours uncredited: waiting %v, dust %v; alarms %v", st["waiting"], st["dust"], r.m.Alarm.StandingKeys())
	if r.alarmed("age") || r.alarmed("stall") {
		t.Fatalf("a burn below the fee, which the relayers leave, raised the stall and age alarms: %v", r.m.Alarm.Standing())
	}
	if i3Num(st, "waiting", "ethereum→gno GNOT") != 0 {
		t.Fatalf("the dust counts as waiting: %v", st["waiting"])
	}
	if v := st["identity"].(map[string]any)["GNOT"].(map[string]any); v["verdict"] != "holds" || v["inflightIn"] != float64(99) {
		t.Fatalf("the dust is not in flight in a held identity (supply burned, the amount still in the lock): %v", v)
	}
	// a burn of the fee, three hours uncredited: the age alarm
	r.chain.mine(120, 120) // above the index's cursor, block 110 now
	fair := r.chain.burn(m3bGNOT, gnot, r.alice, 100, 115)
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.m.state.Index.Burns[hex.EncodeToString(fair[:])].Seen = r.now.Add(-3 * time.Hour).Unix()
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if !r.alarmed("age") || !r.alarmed("stall") {
		t.Fatalf("a burn of the fee three hours uncredited raised no age or stall alarm: %v", r.m.Alarm.StandingKeys())
	}
}

// One stale read — hlmailbox's count from a backend that holds bob's credit, his mark from one a block behind (a
// load-balanced endpoint, which §11.10's own node rules out) — reads his 300 in flight and owed alike: BROKEN, with the
// delivery the index cannot place. Read only once the count moves, the mark would stand unset while the count stands;
// a delivery unplaced under a BROKEN reading has every mark read again inside the cut (the review's finding 2), so the
// poll that read it stale holds: no pending break, no flag (as the Solana monitors' TestS6MonitorStaleMarkHeals).
func TestI3MonitorStaleMarkHeals(t *testing.T) {
	r := newMonRig(t)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.chain.mine(110, 110)
	bob := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 300, 105)
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	bobID := hex.EncodeToString(bob[:])
	stale := i3Stale(r.gno, bobID, 61)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0)) // credited at 61: owed 300, the count 1
	r.gno.set(m3bMailPath, "json", i3Mailbox(1))
	var out []string
	for i := 0; i < 3; i++ {
		r.step(10 * time.Second)
		_ = r.poll()
		v := r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)["verdict"]
		out = append(out, fmt.Sprintf("poll %d: %v (bob credited %d)", i+1, v, r.m.state.Index.Burns[bobID].Credited))
	}
	t.Logf("%s; stale reads %d; pending %d; gno flags %v; eth flag txs %d", strings.Join(out, ", "), stale(), len(r.m.state.Pending), r.gnoFlags(), r.snd.count())
	if stale() != 1 {
		t.Fatalf("the stale read did not happen as set: %d", stale())
	}
	if r.m.state.Index.Burns[bobID].Credited != 61 || !strings.HasPrefix(out[0], "poll 1: holds") {
		t.Fatalf("one stale delivered-mark read stood: %s", strings.Join(out, ", "))
	}
	if len(r.m.state.Pending) != 0 || len(r.gnoFlags()) > 0 || r.snd.count() > 0 {
		t.Fatalf("one stale delivered-mark read left a pending break (%d) or flagged (%v, %d)", len(r.m.state.Pending), r.gnoFlags(), r.snd.count())
	}
}

// The same on a signer: the round that read the mark stale reads it again inside its cut and signs on — never BROKEN,
// no pending break (as TestS6SignerStaleMarkHeals).
func TestI3SignerStaleMarkHeals(t *testing.T) {
	r := newSignerRig(t)
	r.round()
	r.chain.mine(110, 110)
	bob := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 300, 105)
	r.step(time.Second)
	r.round()
	bobID := hex.EncodeToString(bob[:])
	stale := i3Stale(r.gno, bobID, 61)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	var mb map[string]any
	_ = json.Unmarshal([]byte(m3bMailboxJSON(nil, 2, nil)), &mb)
	mb["delivered"] = 1
	b, _ := json.Marshal(mb)
	r.gno.set(m3bMailPath, "json", string(b))
	credited := func() string { // a credited burn the signer signed is pruned from its index at the round's end
		if rec := r.s.state.Index.Burns[bobID]; rec != nil {
			return fmt.Sprintf("bob uncredited in the index (%d)", rec.Credited)
		}
		return fmt.Sprintf("bob credited and pruned (%d pruned)", r.s.state.PrunedCredited)
	}
	var out []string
	for i := 0; i < 3; i++ {
		r.step(time.Second)
		r.round()
		out = append(out, fmt.Sprintf("round %d: signing %v, %s", i+1, r.status()["signing"], credited()))
	}
	t.Logf("%s; stale reads %d; pending %d", strings.Join(out, "; "), stale(), len(r.s.state.Pending))
	if stale() != 1 || r.s.state.Index.Burns[bobID] != nil || r.s.state.PrunedCredited != 1 || r.status()["signing"] != true {
		t.Fatalf("one stale delivered-mark read stood: %s (%v)", strings.Join(out, "; "), r.status()["cut"])
	}
	if !strings.Contains(out[0], "bob credited and pruned") || len(r.s.state.Pending) != 0 {
		t.Fatalf("the round that read the mark stale did not read it again in its cut: %s; pending %d", strings.Join(out, "; "), len(r.s.state.Pending))
	}
}

// i3Stale makes the next read of id's delivered mark answer unset, as a backend a block behind would, and every later
// read answer the height it was credited at; it answers how many reads were stale.
func i3Stale(g *m3bGnoNode, id string, at int64) func() int {
	var mu sync.Mutex
	n := 0
	g.mu.Lock()
	g.afterRender = func(_, path string) {
		if path != "delivered/"+id {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if n == 0 {
			n++
			g.set(m3bMailPath, "delivered/"+id, strconv.FormatInt(at, 10)) // every later read: current
		}
	}
	g.mu.Unlock()
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// The relayer left a burn below its route's fee out of its queue for good, at the fee it read then (F17a: E5-GS5): a
// decision lowering the fee past it made it a message the monitors count waiting — their age alarm for good — that no
// relayer would ever bring. It stays pending, judged against the fee each tick before any read of its own, alarmed once;
// once the fee is at or below its amount it is a burn like any other.
func TestI3RelayerJudgesDustAgainWhenTheFeeFalls(t *testing.T) {
	r := newInboundRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	dust := r.chain.burn(m3bGNOT, gnot, r.alice, 99, 80)    // below the route's fee of 100
	honest := r.chain.burn(m3bGNOT, gnot, r.alice, 100, 81) // the fee
	dustID := hex.EncodeToString(dust[:])
	var mu sync.Mutex
	dustReads := 0
	r.gno.mu.Lock()
	r.gno.afterRender = func(_, path string) {
		if path == "delivered/"+dustID {
			mu.Lock()
			dustReads++
			mu.Unlock()
		}
	}
	r.gno.mu.Unlock()
	// both burns signed by two signers, as the signers sign every burn of something (anyone may bring dust)
	for n, amount := range []uint64{99, 100} {
		m := hl.Message{Version: 3, Nonce: uint32(n), Origin: m3bEthDom, Sender: ethx.AddressTo32(m3bGNOT), Destination: m3bGnoDom, Recipient: gnot, Body: hl.TokenMessage(r.alice, amount, nil)}
		if id := hl.ID(m.Bytes()); id != [][32]byte{dust, honest}[n] {
			t.Fatalf("burn #%d is not the message signed", n)
		}
		for k := 0; k < 2; k++ {
			r.sign(k, uint32(n), m.Bytes(), false)
		}
	}
	for i := 0; i < 3; i++ {
		if err := r.tick(); err != nil {
			t.Fatal(err)
		}
	}
	// a Process call's message ends in its token message's amount (32 bytes): 99 is the dust's, 100 the honest one's
	processed := func(suffix string) int {
		n := 0
		for _, c := range r.gno.callsOf("Process") {
			if strings.HasSuffix(c, suffix) {
				n++
			}
		}
		return n
	}
	mu.Lock()
	reads := dustReads
	mu.Unlock()
	alarm := r.job.alarm.Standing()["inbound:dust:GNOT"]
	t.Logf("under the fee of 100: Process of the dust %d, of the fair burn %d; the dust's mark read %d times; alarm %q", processed("0000000063)"), processed("0000000064)"), reads, alarm)
	if processed("0000000063)") != 0 || processed("0000000064)") == 0 {
		t.Fatalf("under the fee: the dust Processed %d times, the fair burn %d", processed("0000000063)"), processed("0000000064)"))
	}
	if !strings.HasPrefix(alarm, "1 burn(s)") {
		t.Fatalf("the dust burn is not alarmed once: %q", alarm)
	}
	// GovDAO lowers the fee to 50: the dust left under 100 is a delivery now
	r.gno.set(m3bNS+"hlgnot", "json", fmt.Sprintf(`{"live":true,"lock":1000,"owed":0,"voided":0,"lost":0,"fee":%d}`, 50))
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	t.Logf("the fee lowered to 50: Process of the dust %d; alarm %q", processed("0000000063)"), r.job.alarm.Standing()["inbound:dust:GNOT"])
	if processed("0000000063)") == 0 {
		t.Fatal("the burn left under the fee of 100 was never Processed once the fee fell to 50: the monitors count it waiting (age) and no relayer brings it")
	}
	if !strings.HasPrefix(r.job.alarm.Standing()["inbound:dust:GNOT"], "1 burn(s)") {
		t.Fatalf("the dust burn was alarmed again: %q", r.job.alarm.Standing()["inbound:dust:GNOT"])
	}
	if reads > 1 {
		// once, the tick after it was left (it may have been delivered before: the review's finding 4), never each tick
		t.Fatalf("the dust left under the fee had its mark read %d times in three ticks (kept pending, a pile of it would cost a read each a tick)", reads)
	}
}

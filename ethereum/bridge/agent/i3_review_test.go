package agent

// The review of the integration lane I3's follow-ups (a read-only sub-agent over bb83eee2..48108756), its findings 1
// and 2: a stale delivered-mark read and what the machines do with the reading it makes. Its own helpers (rv*), so the
// file compiles on the parents too.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// rvStale makes the next read of id's delivered mark answer unset (a backend a block behind), every later read the
// height it was credited at; it answers how many reads were stale.
func rvStale(g *m3bGnoNode, id string, at int64) func() int {
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
			g.set(m3bMailPath, "delivered/"+id, strconv.FormatInt(at, 10))
		}
	}
	g.mu.Unlock()
	return func() int { mu.Lock(); defer mu.Unlock(); return n }
}

// rvMailbox is the monitor rig's hlmailbox json with its delivered count and its dispatched count.
func rvMailbox(delivered, count int) string {
	var j map[string]any
	_ = json.Unmarshal([]byte(m3bMailboxJSON(nil, 2, []string{"", "", "", "", monWatchGno, ""})), &j)
	j["delivered"] = delivered
	j["count"] = count
	b, _ := json.Marshal(j)
	return string(b)
}

// rvMonBob: a monitor rig whose index holds bob's burn of 300 GNOT at block 105, uncredited; fresh: the providers' heads
// 36 s old (what they show above the index is known, E5-GT1), else 13 minutes old as the rig keeps them.
func rvMonBob(t *testing.T, fresh bool) (*monRig, string) {
	r := newMonRig(t)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if fresh {
		r.chain.mine(162, 110)
	} else {
		r.chain.mine(110, 110)
	}
	bob := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 300, 105)
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	return r, hex.EncodeToString(bob[:])
}

func rvVerdict(r *monRig) any {
	return r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)["verdict"]
}

// (1) Providers current to their heads (the production norm): one stale mark read — the count from a backend that
// holds bob's credit, his mark from one a block behind — reads BROKEN by 300 past the most an honest lag can carry (0),
// so the monitor flags credits, payouts and the gate in that same poll. The doubt rule only re-reads the mark next poll.
func TestI3ReviewMonitorOneStaleMarkFlags(t *testing.T) {
	r, bobID := rvMonBob(t, true)
	stale := rvStale(r.gno, bobID, 61)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "json", rvMailbox(1, 0))
	var out []string
	for i := 0; i < 3; i++ {
		r.step(10 * time.Second)
		err := r.poll()
		out = append(out, fmt.Sprintf("poll %d: %v (err %v, bob credited %d)", i+1, rvVerdict(r), err, r.m.state.Index.Burns[bobID].Credited))
	}
	t.Logf("%s; stale reads %d; gno flags %v; eth flag txs %d; flag alarms %v", strings.Join(out, ", "), stale(), r.gnoFlags(), r.snd.count(), r.m.Alarm.StandingFlags())
	if stale() != 1 {
		t.Fatalf("setup: %d stale reads", stale())
	}
	if len(r.gnoFlags()) > 0 || r.snd.count() > 0 {
		t.Fatalf("one stale delivered-mark read flagged credits/payouts (%v) and the gate (%d txs)", r.gnoFlags(), r.snd.count())
	}
}

// (2) The rig's own scenario (TestI3MonitorStaleMarkHeals: providers 13 minutes behind, so the reading waits): the mark
// heals the next poll, but the pending break the stale reading made stays, and once the index's finalized block's time
// passes the reading's it is confirmed — flags on gno.land and Ethereum for a burn that was credited all along.
func TestI3ReviewMonitorStaleMarkPendingConfirmedLater(t *testing.T) {
	r, bobID := rvMonBob(t, false)
	stale := rvStale(r.gno, bobID, 61)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "json", rvMailbox(1, 0))
	var out []string
	for i := 0; i < 3; i++ {
		r.step(10 * time.Second)
		_ = r.poll()
		out = append(out, fmt.Sprintf("poll %d: %v (bob credited %d, pending %d)", i+1, rvVerdict(r), r.m.state.Index.Burns[bobID].Credited, len(r.m.state.Pending)))
	}
	if stale() != 1 || r.m.state.Index.Burns[bobID].Credited != 61 {
		t.Fatalf("setup: the mark did not heal: %s", strings.Join(out, ", "))
	}
	r.chain.retime(uint64(r.now.Unix())) // time passes: the index's finalized block's time is past the stale reading's
	r.step(10 * time.Second)
	err := r.poll()
	t.Logf("%s; then the index passes the reading: %v (err %v); gno flags %v; eth flag txs %d; flag alarms %v", strings.Join(out, ", "), rvVerdict(r), err, r.gnoFlags(), r.snd.count(), r.m.Alarm.StandingFlags())
	if len(r.gnoFlags()) > 0 || r.snd.count() > 0 {
		t.Fatalf("the healed stale reading's pending break was confirmed: gno flags %v, eth flag txs %d", r.gnoFlags(), r.snd.count())
	}
}

// (3) The count rule's own gap: the stale mark is read in a poll that stops before markDoubt (the sends' walk fails on
// one render), so the doubt flag is not set; the next poll does not read the mark again (the count did not move) and
// reads bob's credited 300 in flight — BROKEN past the lag, flagged at once. On bb83eee2 the next poll read the mark
// again and held.
func TestI3ReviewMonitorStaleMarkAfterAbortedPoll(t *testing.T) {
	r, bobID := rvMonBob(t, true)
	// hlmailbox's message 0: a dispatch to an address that is no token (no send of a route); its render fails once
	m := hl.Message{Version: 3, Nonce: 0, Origin: m3bGnoDom, Sender: gnoside.RealmKey(m3bNS + "hlgov"), Destination: m3bEthDom, Recipient: ethx.AddressTo32(common.HexToAddress("0x00000000000000000000000000000000000d0d0d")), Body: []byte{1, 2, 3}}
	raw := m.Bytes()
	id := hl.ID(raw)
	r.gno.set(m3bMailPath, "msg/0", fmt.Sprintf(`{"nonce":0,"id":%q,"root":"%s","height":%d,"message":%q}`, hex.EncodeToString(id[:]), strings.Repeat("00", 32), 61, hex.EncodeToString(raw)))
	failed := 0
	r.gno.mu.Lock()
	r.gno.renderErr = func(pkg, path string) error {
		if path == "msg/0" && failed == 0 {
			failed++
			return fmt.Errorf("a node hiccup")
		}
		return nil
	}
	r.gno.mu.Unlock()
	stale := rvStale(r.gno, bobID, 61)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	r.gno.set(m3bMailPath, "json", rvMailbox(1, 1))
	var out []string
	for i := 0; i < 3; i++ {
		r.step(10 * time.Second)
		err := r.poll()
		out = append(out, fmt.Sprintf("poll %d: %v (err %v, bob credited %d)", i+1, rvVerdict(r), err, r.m.state.Index.Burns[bobID].Credited))
	}
	t.Logf("%s; stale reads %d, failed renders %d; gno flags %v; eth flag txs %d", strings.Join(out, ", "), stale(), failed, r.gnoFlags(), r.snd.count())
	if stale() != 1 || failed != 1 {
		t.Fatalf("setup: %d stale reads, %d failed renders", stale(), failed)
	}
	if len(r.gnoFlags()) > 0 || r.snd.count() > 0 {
		t.Fatalf("a stale mark read in an aborted poll stood into the next: flags %v, eth txs %d", r.gnoFlags(), r.snd.count())
	}
}

// (4) The signer over the rig's stale read: the mark heals, but the pending break is confirmed once the index passes the
// reading: a flag-level signer:halt is raised (and cleared in the same round, the reading holding).
func TestI3ReviewSignerStaleMarkPendingHaltsLater(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	r := newSignerRig(t)
	r.round()
	r.chain.mine(110, 110)
	bob := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 300, 105)
	r.step(time.Second)
	r.round()
	bobID := hex.EncodeToString(bob[:])
	stale := rvStale(r.gno, bobID, 61)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	var mb map[string]any
	_ = json.Unmarshal([]byte(m3bMailboxJSON(nil, 2, nil)), &mb)
	mb["delivered"] = 1
	b, _ := json.Marshal(mb)
	r.gno.set(m3bMailPath, "json", string(b))
	for i := 0; i < 3; i++ {
		r.step(time.Second)
		r.round()
	}
	if stale() != 1 || r.s.state.Index.Burns[bobID] != nil {
		t.Fatalf("setup: the mark did not heal (stale %d)", stale())
	}
	pend := len(r.s.state.Pending)
	buf.Reset()
	r.chain.retime(uint64(r.now.Unix()))
	r.step(time.Second)
	r.round()
	flagged := strings.Contains(buf.String(), "flag signer:halt")
	t.Logf("after the heal: %d pending; once the index passes the reading: signer:halt flagged %v, signing %v, halted %q", pend, flagged, r.status()["signing"], r.status()["halted"])
	if flagged {
		t.Fatalf("the healed stale reading's pending break raised a flag-level signer:halt: %s", rvFirstLine(buf.String(), "signer:halt"))
	}
}

func rvFirstLine(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			if len(l) > 400 {
				return l[:400]
			}
			return l
		}
	}
	return ""
}

// (5) The signer's side of (3): the stale mark read in a round that stops at the sends' walk (before markDoubt) is not
// read again the next round, which reads bob's credited 300 in flight — past the lag its current providers bound (0):
// halted at once (a flag-level signer:halt). On bb83eee2 the next round read the mark again and signed on.
func TestI3ReviewSignerStaleMarkAfterAbortedRound(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	r := newSignerRig(t)
	r.round()
	r.chain.mine(162, 110)
	r.chain.retime(uint64(r.now.Unix()) - 30 - 162*12) // the providers' heads 30 s old: the lag above the index is known
	bob := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 300, 105)
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] != true {
		t.Fatalf("setup: not signing over bob's burn in flight: %v", st["cut"])
	}
	bobID := hex.EncodeToString(bob[:])
	m := hl.Message{Version: 3, Nonce: 0, Origin: m3bGnoDom, Sender: gnoside.RealmKey(m3bNS + "hlgov"), Destination: m3bEthDom, Recipient: ethx.AddressTo32(common.HexToAddress("0x00000000000000000000000000000000000d0d0d")), Body: []byte{1, 2, 3}}
	raw := m.Bytes()
	id := hl.ID(raw)
	r.gno.set(m3bMailPath, "msg/0", fmt.Sprintf(`{"nonce":0,"id":%q,"root":"%s","height":%d,"message":%q}`, hex.EncodeToString(id[:]), strings.Repeat("00", 32), 61, hex.EncodeToString(raw)))
	failed := 0
	r.gno.mu.Lock()
	r.gno.renderErr = func(pkg, path string) error {
		if path == "msg/0" && failed == 0 {
			failed++
			return fmt.Errorf("a node hiccup")
		}
		return nil
	}
	r.gno.mu.Unlock()
	stale := rvStale(r.gno, bobID, 61)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	var mbj map[string]any
	_ = json.Unmarshal([]byte(m3bMailboxJSON(nil, 2, nil)), &mbj)
	mbj["delivered"], mbj["count"] = 1, 1
	b, _ := json.Marshal(mbj)
	r.gno.set(m3bMailPath, "json", string(b))
	buf.Reset()
	var out []string
	for i := 0; i < 3; i++ {
		r.step(time.Second)
		r.round()
		out = append(out, fmt.Sprintf("round %d: signing %v halted %q", i+1, r.status()["signing"], r.status()["halted"]))
	}
	flagged := strings.Contains(buf.String(), "flag signer:halt")
	t.Logf("%s; stale reads %d, failed renders %d; signer:halt flagged %v", strings.Join(out, "; "), stale(), failed, flagged)
	if stale() != 1 || failed != 1 {
		t.Fatalf("setup: %d stale reads, %d failed renders", stale(), failed)
	}
	if flagged {
		t.Fatalf("a stale mark read in an aborted round stood into the next and halted the signer: %s", rvFirstLine(buf.String(), "signer:halt"))
	}
}

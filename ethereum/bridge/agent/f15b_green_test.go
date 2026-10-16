package agent

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// stageAt logs a StageProgress of a header at a height (one real signature of the set behind it, as every stage has).
func (r *monRig) stageAt(height int64, block uint64, key byte) {
	h := &gnolight.Header{ChainID: "dev", Height: height}
	var ls [][]byte
	for _, l := range h.Leaves() {
		ls = append(ls, l)
	}
	hash, _ := gnolight.SimpleHash(ls)
	input, _ := ethx.LightClient.Pack("update", ls, ethx.Commit{PartsTotal: 1}, []ethx.Validator{}, []ethx.Validator{})
	tx := crypto.Keccak256Hash([]byte("stage"), []byte{key})
	r.chain.tx(tx, m3bClient, input)
	data, _ := ethx.LightClient.ABI.Events["StageProgress"].Inputs.NonIndexed().Pack(hash, height, int64(60), int64(360))
	r.chain.log(types.Log{Address: m3bClient, Topics: []common.Hash{ethx.LightClient.Event("StageProgress"), {key}, ethx.AddressTopic(common.HexToAddress("0xbad"))}, Data: data, BlockNumber: block, TxHash: tx})
}

func (r *monRig) view(t *testing.T) (*WatchView, bool, string) {
	t.Helper()
	r.m.ReadOnly = true
	v, err := (&Watch{m: r.m}).View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ok, why := v.Green()
	return v, ok, strings.Join(why, "; ")
}

// E4-GT5 (1) (go-trust-poc/zz_audit4_stage_test.go): a stage of a header far above gno.land's head — the leaked key's
// early warning §6.3 names — alarms at once, and the page is not green; a stage whose block this node does not serve is
// said, never skipped in silence.
func TestF15bStageFarAboveTheHeadAlarms(t *testing.T) {
	r := newMonRig(t)
	r.chain.mine(110, 110)
	r.stageAt(60+10_000_000, 105, 9)
	v, ok, why := r.view(t)
	t.Logf("green %v: %s", ok, why)
	var stage []string
	for k := range v.Alarms {
		if strings.HasPrefix(k, "stage:unknown:") {
			stage = append(stage, k)
		}
	}
	if len(stage) == 0 || ok {
		t.Fatalf("a stage 10,000,000 blocks above the head: alarms %v, green %v", stage, ok)
	}
	r2 := newMonRig(t)
	r2.chain.mine(110, 110)
	r2.stageAt(55, 105, 8)
	r2.gno.blockErr = func(h int64) error {
		if h == 55 {
			return errors.New("block 55 is pruned here")
		}
		return nil
	}
	_, ok, why = r2.view(t)
	t.Logf("green %v: %s", ok, why)
	if ok || !strings.Contains(why, "the stage of header") {
		t.Fatalf("a stage whose block does not read is skipped in silence: %v %s", ok, why)
	}
}

// E4-GT5 (2), E4-CF3, E4-TL4 (zz_audit4_stage_test.go, zz_audit4_cf_green_test.go, zz_audit4_tools_agent_test.go): a
// gno.land ledger that does not read is NOT READ on the page — never "none" — and the page is not green; so is
// GovDAO's proposal list, and r/hlgov's counter.
func TestF15bUnreadReadsAreSaid(t *testing.T) {
	r := newMonRig(t)
	r.gno.set(m3bMailPath, "decisions.json/1", `[{"id":7,"kind":"signers","text":"replace the signer set","state":"pending","made":1,"recorded":1,"due":9999999999}]`)
	v, ok, why := r.view(t)
	text := v.Text("test")
	i := strings.Index(text, "## pending decisions on gno.land")
	page := text[i : i+200]
	t.Logf("hlmailbox's ledger out of place: green %v %s; %q", ok, why, page)
	if ok || strings.Contains(page, "- none") || !strings.Contains(page, "NOT READ: "+m3bMailPath) {
		t.Fatalf("an unread ledger: green %v, page %q", ok, page)
	}
	r2 := newMonRig(t) // r/hlgov's first page refused (a decision out of place)
	r2.gno.set(m3bNS+"hlgov", "decisions.json/1", `[{"id":7,"kind":"set decision recipient","text":"x","state":"pending","made":1,"recorded":1,"due":9999999999}]`)
	if _, ok, why := r2.view(t); ok || !strings.Contains(why, m3bNS+"hlgov") {
		t.Fatalf("r/hlgov's ledger unread: green %v %s", ok, why)
	}
	r3 := newMonRig(t) // GovDAO's list does not read
	r3.gno.set("gno.land/r/gov/dao", "", "")
	r3.gno.mu.Lock()
	delete(r3.gno.renders, "gno.land/r/gov/dao:")
	r3.gno.mu.Unlock()
	if _, ok, why := r3.view(t); ok || !strings.Contains(why, "GovDAO's proposals") {
		t.Fatalf("GovDAO's list unread: green %v %s", ok, why)
	}
}

// E4-GT5 (3): the sources rule says when it did not run (the public source does not answer); a public source a few
// blocks behind is compared at its own head, not skipped.
func TestF15bSourcesRuleSaysWhenItSkips(t *testing.T) {
	r := newMonRig(t)
	r.second.statusErr = errors.New("connection refused")
	r.second.blockErr = func(int64) error { return errors.New("connection refused") }
	_, ok, why := r.view(t)
	t.Logf("public source down: green %v %s", ok, why)
	if ok || !strings.Contains(why, "the sources rule") {
		t.Fatalf("the sources rule skipped in silence: %v %s", ok, why)
	}
	r2 := newMonRig(t)
	r2.second.advance(r2.gno.height-2, r2.now.Add(time.Second)) // two blocks behind
	if err := r2.poll(); err != nil {
		t.Fatal(err)
	}
	src, _ := r2.m.Status()["sources"].(string)
	t.Logf("public source two blocks behind: %q", src)
	if !strings.HasPrefix(src, "agree at height 58") {
		t.Fatalf("a public source two blocks behind was not compared at its head: %q", src)
	}
}

// E4-CF4 (probes-conformance/zz_audit4_cf_window_test.go): HlAdmin's pending decisions are read in full, not the last
// 256 numbers: a kind 8 waiting for code at number 10 of 300 is listed, checked, alarmed when stale; the page is not
// green.
func TestF15bEveryPendingDecisionListed(t *testing.T) {
	r := newMonRig(t)
	r.mu.Lock()
	r.lastSeq = 300
	impl := common.HexToAddress("0x00000000000000000000000000000000deadbeef") // no code there yet
	var params []byte
	params = append(params, common.LeftPadBytes(m3bMailbox.Bytes(), 32)...)
	params = append(params, common.LeftPadBytes(impl.Bytes(), 32)...)
	params = append(params, make([]byte, 32)...)
	r.pending[10] = ethx.Decision{Number: 10, Kind: 8, DeliveredBlock: 50, DeliveredTime: uint64(r.now.Unix()) - 400*86400, Params: params}
	r.mu.Unlock()
	r.gno.set(m3bNS+"hlgov", "json", `{"last":300,"next":301}`)
	r.m.state.DecisionsChecked = 300
	v, ok, why := r.view(t)
	listed := false
	for _, d := range v.Decisions {
		listed = listed || d.Number == 10
	}
	t.Logf("kind 8 #10 of 300: listed %v, checks %d, green %v %s", listed, len(v.Checks), ok, why)
	if !listed || len(v.Checks) == 0 || ok || !strings.Contains(why, "decision:code-wait:10") {
		t.Fatalf("a pending kind 8 older than 256 numbers: listed %v, checks %d, green %v %s", listed, len(v.Checks), ok, why)
	}
}

// E4-TL3 (zz_audit4_tools_agent_test.go): a client whose trusted header is not gno.land's block at its height — a root
// nobody updated since — is alarmed by the monitors, and hlwatch's MISMATCH keeps the page from green.
func TestF15bForeignTrustRootIsNotGreen(t *testing.T) {
	r := newMonRig(t)
	var forged [32]byte
	forged[0] = 0xf0
	r.chain.view(m3bClient, "trustedHash", func(uint64, []any) []any { return []any{forged} })
	r.m.state.From = 20 // eth.deployBlock: the root was recorded at block 10, the setup's step 4
	n, run := r.recordAt(19)
	r.m.state.Record = headerRecord{Cursor: 19, Count: n, Hash: hexOf(run)}
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if !r.alarmed("header:trusted") {
		t.Fatalf("the monitor does not alarm a trusted header that is not gno.land's: %v", r.m.Alarm.StandingKeys())
	}
	r.m.Alarm = &Alarmer{Source: "hlwatch"}
	_, ok, why := r.view(t)
	t.Logf("green %v %s", ok, why)
	if ok || !strings.Contains(why, "MISMATCH") {
		t.Fatalf("hlwatch green beside a trusted header it prints as a MISMATCH: %s", why)
	}
}

// E4-CF4 with TL8's point for the watch: a fresh cursor starts past HlAdmin's stored start (its first storage word,
// bytes 16–23) — no number at or below it ever held a decision — so a deployment numbered from 1,000,000 is read in two
// polls, not four thousand.
func TestF15bPendingCursorStartsAtTheStoredStart(t *testing.T) {
	r := newMonRig(t)
	var a common.Address
	binary.BigEndian.PutUint64(a[4:12], 1_000_000) // the word's bytes 16–23: startSeq
	r.chain.setSlot(m3bAdmin, hlAdminWordSlot, 1, a)
	r.mu.Lock()
	r.lastSeq = 1_000_300
	r.pending[1_000_100] = ethx.Decision{Number: 1_000_100, Kind: 2, DeliveredBlock: 50, DeliveredTime: uint64(r.now.Unix()), Params: []byte{0}}
	r.mu.Unlock()
	r.gno.set(m3bNS+"hlgov", "json", `{"last":1000300,"next":1000301}`)
	r.m.state.DecisionsChecked = 1_000_300
	v, _, _ := r.view(t)
	listed := false
	for _, d := range v.Decisions {
		listed = listed || d.Number == 1_000_100
	}
	cur := r.m.state.AdminLow[strings.ToLower(m3bAdmin.Hex())]
	t.Logf("listed %v, cursor %d", listed, cur)
	if !listed || cur != 1_000_301 {
		t.Fatalf("the cursor from the stored start: listed %v, cursor %d", listed, cur)
	}
}

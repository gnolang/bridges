package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// ethActivity reads /api/ethereum/activity as the page does.
func ethActivity(t *testing.T, s *server) activityJSON {
	t.Helper()
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/ethereum/activity", nil))
	var a activityJSON
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &a) != nil {
		t.Fatalf("activity: %d %s", rr.Code, rr.Body.String())
	}
	return a
}

// TestEthActivity follows the Ethereum deployment's world through the activity: the history's events with their
// blocks' times (and no stranger's dispatch: ER5), the send through its header and its delivery (the proof read
// from the delivering call), the burn through its finality, the signers' lamps, its credit and its payout.
func TestEthActivity(t *testing.T) {
	s, _, _, me := mockBoth(true, siteDir, fixedNow)
	a := ethActivity(t, s)
	if len(a.Errors) > 0 || a.Stale || len(a.Status) == 0 {
		t.Fatalf("errors %v stale %v", a.Errors, a.Stale)
	}
	if a.Burns != 5 || a.Processed != 0 || a.Trusted != uint64(ethHeartbeat) || a.Threshold != 2 {
		t.Errorf("counters: burns %d processed %d trusted %d threshold %d", a.Burns, a.Processed, a.Trusted, a.Threshold)
	}
	if lampStates(a.Signers) != "up,up,up" || a.Signers[0].Nonce != 4 {
		t.Errorf("lamps before the burn is final: %+v", a.Signers)
	}
	start := fixedNow().Unix()
	gt := func(h int64) int64 { return fixedNow().Add(time.Duration(h-mockHead) * mockBlock).Unix() }
	bt := func(t int64) int64 { return me.timeOf(me.blockAt(t)) }
	for _, c := range []struct {
		key  string
		time int64
	}{
		{"decision:" + mockEthDecID, gt(ethDecHeight)},
		{"header:321", bt(gt(ethDecHeight) + 40)},
		{"enact:" + mockEthDecID, bt(gt(ethDecHeight) + 90)},
		{"send:" + mockEthPID, gt(ethPHeight)},
		{"header:641", bt(gt(ethPHeight) + 40)},
		{"mint:" + mockEthPID, bt(gt(ethPHeight) + 100)},
		{"burn:" + mockEthQID, bt(start - 3000)},
		{"credit:" + mockEthQID, gt(ethQCredit)},
		{"header:1050", bt(gt(ethHeartbeat) + 20)},
		{"send:" + mockEthOutID, gt(ethOutHeight)},
		{"burn:" + mockEthInID, bt(start - 300)},
	} {
		e := eventByKey(a, c.key)
		if e == nil {
			t.Errorf("no event %s in %v", c.key, keys(a))
			continue
		}
		if e.Time != c.time {
			t.Errorf("%s at %d, want %d", c.key, e.Time, c.time)
		}
	}
	stranger := idOf(mockEthStrangerMessage())
	if eventByKey(a, "burn:"+stranger) != nil {
		t.Error("a stranger's dispatch is shown as a burn (ER5)")
	}
	if e := eventByKey(a, "mint:"+mockEthPID); e == nil || e.Proof == nil || e.Proof.Height != 641 || e.Proof.Total != 4 || e.Proof.Index != 2 || e.Proof.Aunts != 2 || !e.Proof.Ring || e.Token != "GNOT" || e.Amount != 100*one || e.To != mockEthAddr("bob").String() || e.Link != "https://sepolia.etherscan.io/tx/"+mockEthTx("P delivery") {
		t.Errorf("the history's mint and its proof: %+v", e)
	}
	if e := eventByKey(a, "burn:"+mockEthInID); e == nil || e.Token != "BUBBLE" || e.Amount != 2500*one || e.To != mockRecipient || e.Nonce == nil || *e.Nonce != 4 || e.Sig != mockEthBurnTx {
		t.Errorf("the burn in flight: %+v", e)
	}
	if e := eventByKey(a, "decision:"+mockEthDecID); e == nil || e.What != "#1 set protocol fee" {
		t.Errorf("the decision: %+v", e)
	}
	if a.Last.Out == nil || a.Last.Out.ID != mockEthPID || doneKeys(a.Last.Out.Walk) != "lock final header proof mint" || a.Last.In == nil || a.Last.In.ID != mockEthQID || doneKeys(a.Last.In.Walk) != "burn vouch deliver credit pay" {
		t.Fatalf("the last transfers: %+v %+v", a.Last.Out, a.Last.In)
	}
	// the send: its header, then its delivery with the proof under that header
	_ = s.world.step("ethereum", "out")
	a = ethActivity(t, s)
	if e := eventByKey(a, "header:1294"); e == nil || e.Time != bt(start) || a.Trusted != 1294 || e.Sig != mockEthTx("out header") {
		t.Errorf("the header past the send: %+v, trusted %d", e, a.Trusted)
	}
	_ = s.world.step("ethereum", "out")
	a = ethActivity(t, s)
	if e := eventByKey(a, "mint:"+mockEthOutID); e == nil || e.Proof == nil || e.Proof.Height != 1294 || e.Proof.Total != 3 || e.Proof.Index != 1 || e.Amount != 120*one {
		t.Errorf("the mint: %+v", e)
	}
	if a.Last.Out.ID != mockEthOutID || doneKeys(a.Last.Out.Walk) != "lock final header proof mint" || a.Last.Out.Walk[2].Height != 1294 {
		t.Errorf("the send minted is the replay's: %s %+v", a.Last.Out.ID, a.Last.Out.Walk)
	}
	// the burn: final, then the signers one by one, the credit, half paid, paid
	for i, want := range []string{"up,up,up", "vouching,up,up", "vouching,vouching,up"} {
		_ = s.world.step("ethereum", "in")
		if a = ethActivity(t, s); lampStates(a.Signers) != want {
			t.Errorf("in stage %d: lamps %s", i+1, lampStates(a.Signers))
		}
	}
	_ = s.world.step("ethereum", "in")
	a = ethActivity(t, s)
	if e := eventByKey(a, "credit:"+mockEthInID); e == nil || e.Amount != 2500*one || a.Last.In.ID != mockEthInID || doneKeys(a.Last.In.Walk) != "burn vouch deliver credit" {
		t.Errorf("the credit: %+v", e)
	}
	_ = s.world.step("ethereum", "in")
	a = ethActivity(t, s)
	if e := eventByKey(a, "payout:"+mockEthInID+":1250000000"); e == nil || !e.Exact || e.Full {
		t.Errorf("half paid: %+v", e)
	}
	_ = s.world.step("ethereum", "in")
	a = ethActivity(t, s)
	if e := eventByKey(a, "payout:"+mockEthInID+":2500000000"); e == nil || !e.Full || lampStates(a.Signers) != "vouching,vouching,vouching" {
		t.Errorf("paid in full: %+v %s", e, lampStates(a.Signers))
	}
	_ = s.world.step("ethereum", "down")
	if a = ethActivity(t, s); lampStates(a.Signers) != "vouching,vouching,down" || len(a.Errors) > 0 {
		t.Errorf("signer 3 down: %s %v", lampStates(a.Signers), a.Errors)
	}
	// the Solana side never moved
	if sa := activity(t, s); lampStates(sa.Signers) != "up,up,up" || eventByKey(sa, "header:1291") != nil {
		t.Errorf("the Solana world moved with Ethereum's: %s", lampStates(sa.Signers))
	}
}

// TestEthActivityReads pins what the index asks the provider: the first read the history in windows within the
// range, each delivering call and the newest update once, each block once; a read where nothing moved the two
// blocks, the trusted height and the tail; a read after new blocks were finalized the new range alone.
func TestEthActivityReads(t *testing.T) {
	s, _, _, me := mockBoth(false, siteDir, fixedNow)
	ch, e := s.cfg.Chain("ethereum"), ethOf(s)
	st := s.activityNow(ctx, ch, e)
	if len(st.errors) > 0 {
		t.Fatalf("the first read: %v", st.errors)
	}
	// forward: none; back: down to fromBlock (2,000 blocks before the head) in two windows of at most 1,000; the
	// tail; the three delivering calls and the newest update, each once
	if me.calls["eth_getLogs"] != 3 || me.calls["eth_getTransactionByHash"] != 4 {
		t.Errorf("the first read: %v", me.calls)
	}
	if p := st.other.eth; p == nil || !p.done || p.lo != mockEthHead-2000 || p.hi != me.finalized() {
		t.Errorf("the first read's reach: %+v", st.other.eth)
	}
	me.calls = map[string]int{}
	s.now = func() time.Time { return fixedNow().Add(activityTTL + time.Second) }
	st = s.activityNow(ctx, ch, e)
	// the finalized block, the newest, the trusted height, the tail and the one block in it that holds a log (a block
	// past the finalized one is read again: it may be taken back)
	if len(st.errors) > 0 || fmt.Sprint(me.calls) != "map[eth_call:1 eth_getBlockByNumber:3 eth_getLogs:1]" {
		t.Errorf("a read where nothing moved: %v %v", me.calls, st.errors)
	}
	// ten minutes later the finalized block moved eleven blocks: those are read, once, then the tail past them
	me.calls = map[string]int{}
	me.w.now = func() time.Time { return fixedNow().Add(10 * time.Minute) }
	s.now = func() time.Time { return fixedNow().Add(10*time.Minute + 2*activityTTL) }
	var ranges []string
	me.hook = func(method string, p []json.RawMessage) (json.RawMessage, *rpcError, bool) {
		if method == "eth_getLogs" {
			var f struct{ FromBlock, ToBlock string }
			_ = json.Unmarshal(p[0], &f)
			a, _ := parseQty(f.FromBlock)
			b, _ := parseQty(f.ToBlock)
			ranges = append(ranges, fmt.Sprintf("%d-%d", int64(a)-int64(mockEthHead), int64(b)-int64(mockEthHead)))
		}
		return nil, nil, false
	}
	st = s.activityNow(ctx, ch, e)
	if len(st.errors) > 0 || strings.Join(ranges, " ") != "-24--14 -13-50" {
		t.Errorf("the reads after five blocks: %v %v", ranges, st.errors)
	}
}

// TestEthActivityTail: a burn in a block past the finalized one is shown, and a reorganization that takes the block
// back takes the burn with it; once the provider holds it again it is back.
func TestEthActivityTail(t *testing.T) {
	s, _, _, me := mockBoth(true, siteDir, fixedNow)
	ch, e := s.cfg.Chain("ethereum"), ethOf(s)
	if st := s.activityNow(ctx, ch, e); st.other.Burns[mockEthInID].Slot == 0 {
		t.Fatal("the burn in the tail is not shown")
	}
	inBlock := me.blockAt(fixedNow().Unix() - 300)
	me.hook = func(method string, p []json.RawMessage) (json.RawMessage, *rpcError, bool) {
		if method == "eth_getLogs" { // the reorganized chain holds no burn in that block
			res, _ := me.handle(method, p)
			b, _ := json.Marshal(res)
			var logs []map[string]any
			_ = json.Unmarshal(b, &logs)
			var kept []map[string]any
			for _, l := range logs {
				if l["transactionHash"] != mockEthBurnTx {
					kept = append(kept, l)
				}
			}
			out, _ := json.Marshal(kept)
			if kept == nil {
				out = []byte("[]")
			}
			return out, nil, true
		}
		if method == "eth_getBlockByNumber" && str(p, 0) == qty(inBlock) {
			other := hl.Keccak([]byte("the other branch"))
			return json.RawMessage(fmt.Sprintf(`{"number":"%s","hash":"%s","timestamp":"0x1"}`, qty(inBlock), hex0x(other[:]))), nil, true
		}
		return nil, nil, false
	}
	if st := s.activityNow(ctx, ch, e); len(st.errors) > 0 || st.other.Burns[mockEthInID].Slot != 0 {
		t.Errorf("after the reorganization: the burn %+v, errors %v", st.other.Burns[mockEthInID], st.errors)
	}
	me.hook = nil
	if st := s.activityNow(ctx, ch, e); st.other.Burns[mockEthInID].Slot != inBlock {
		t.Errorf("back on the provider's chain: %+v", st.other.Burns[mockEthInID])
	}
}

// TestEthActivityInconsistent: a provider whose logs name a block hash it does not hold at that number (a stale
// node behind a balancer): nothing of the range is folded, the reading says why, and the range is read again.
func TestEthActivityInconsistent(t *testing.T) {
	s, _, _, me := mockBoth(true, siteDir, fixedNow)
	ch, e := s.cfg.Chain("ethereum"), ethOf(s)
	qBlock := me.blockAt(fixedNow().Unix() - 3000)
	me.hook = func(method string, p []json.RawMessage) (json.RawMessage, *rpcError, bool) {
		if method == "eth_getBlockByNumber" && str(p, 0) == qty(qBlock) {
			other := hl.Keccak([]byte("a stale node"))
			return json.RawMessage(fmt.Sprintf(`{"number":"%s","hash":"%s","timestamp":"0x1"}`, qty(qBlock), hex0x(other[:]))), nil, true
		}
		return nil, nil, false
	}
	st := s.activityNow(ctx, ch, e)
	if _, ok := st.other.Burns[mockEthQID]; ok || !strings.Contains(strings.Join(st.errors, " "), "no longer holds") {
		t.Errorf("an inconsistent range was folded: %v", st.errors)
	}
	me.hook = nil
	st = s.activityNow(ctx, ch, e)
	if _, ok := st.other.Burns[mockEthQID]; !ok || len(st.errors) > 0 {
		t.Errorf("read again: %v", st.errors)
	}
}

// TestEthActivityRangeRefused: a provider that refuses wide ranges has the window halved until it answers, and the
// history is read all the same.
func TestEthActivityRangeRefused(t *testing.T) {
	s, _, _, me := mockBoth(true, siteDir, fixedNow)
	ch, e := s.cfg.Chain("ethereum"), ethOf(s)
	me.hook = func(method string, p []json.RawMessage) (json.RawMessage, *rpcError, bool) {
		if method == "eth_getLogs" {
			var f struct{ FromBlock, ToBlock string }
			_ = json.Unmarshal(p[0], &f)
			a, _ := parseQty(f.FromBlock)
			b, _ := parseQty(f.ToBlock)
			if b-a+1 > 300 {
				return nil, &rpcError{Code: -32005, Message: "query exceeds max block range 300"}, true
			}
		}
		return nil, nil, false
	}
	var st *activityState
	for i := 0; i < 12; i++ {
		st = s.activityNow(ctx, ch, e)
	}
	if p := st.other.eth; p.rng != 250 || !p.done {
		t.Errorf("the window: %d, history done %v", p.rng, p.done)
	}
	for _, id := range []string{mockEthQID, mockEthInID} {
		if _, ok := st.other.Burns[id]; !ok {
			t.Errorf("burn %s not read through the narrower windows", id)
		}
	}
	if _, ok := st.other.Deliveries[mockEthPID]; !ok || len(st.errors) > 0 {
		t.Errorf("the history's delivery: %v", st.errors)
	}
}

// TestEthActivityNoTxIndex: a provider that no longer indexes a final delivering transaction (null for it) still
// lets the history be read: the delivery from its logs alone (the token's ReceivedTransferRemote: the amount and the
// recipient), no proof, no error, the window done; in the tail the same null is a lag, asked again.
func TestEthActivityNoTxIndex(t *testing.T) {
	s, _, _, me := mockBoth(true, siteDir, fixedNow)
	ch, e := s.cfg.Chain("ethereum"), ethOf(s)
	me.hook = func(method string, p []json.RawMessage) (json.RawMessage, *rpcError, bool) {
		if method == "eth_getTransactionByHash" && (str(p, 0) == mockEthTx("P delivery") || str(p, 0) == mockEthTx("out delivery")) {
			return json.RawMessage("null"), nil, true
		}
		return nil, nil, false
	}
	st := s.activityNow(ctx, ch, e)
	d, ok := st.other.Deliveries[mockEthPID]
	if !ok || len(st.errors) > 0 || d.Kind != "mint" || d.Amount != 100*one || d.To != mockEthAddr("bob").String() || d.Hash != "" || !st.other.eth.done {
		t.Errorf("a final delivery without its transaction: %+v, errors %v", d, st.errors)
	}
	if _, ok := st.other.Proofs[mockEthHash256(mockEthPMessage())]; ok {
		t.Error("a proof shown without the call that carried it")
	}
	// in the tail the provider may only lag: the delivery is not folded from a null, it is asked for again
	_ = s.world.step("ethereum", "out")
	_ = s.world.step("ethereum", "out")
	st = s.activityNow(ctx, ch, e)
	if _, ok := st.other.Deliveries[mockEthOutID]; ok || !strings.Contains(strings.Join(st.errors, " "), "holds no transaction") {
		t.Errorf("a tail delivery read from a null: errors %v", st.errors)
	}
	me.hook = nil
	st = s.activityNow(ctx, ch, e)
	if d, ok := st.other.Deliveries[mockEthOutID]; !ok || d.Hash == "" || len(st.errors) > 0 {
		t.Errorf("the tail delivery once the provider holds it: %+v %v", d, st.errors)
	}
}

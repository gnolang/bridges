package agent

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// Lane F17a (audit round 5's go-spend): E5-GS6, a send in a block the scan has not read booked at the least delivery;
// E5-GS5 with the plan red team's E5-RT5, the gno key's storage deposits and dust burns.

// E5-GS6: both machines down ≈2.7 hours, a send S dispatched meanwhile in a transaction that also emits 7.4 KB of its
// own events. The updater's first step scanned 2,000 blocks of the gap and walked S anyway, its leaf unmeasured, priced
// at the model's least delivery (311,300 gas): it bought and booked the header, and the relayer delivered S at no fee
// check from its 7.8 KB leaf (≈575K gas). Route headers wait until the scan has reached the head; then S is priced by its
// real leaf, which its fee does not cover.
func TestF17aUnscannedSendWaitsForTheScan(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.000075, "BUBBLE": 1} // 50 GNOT = 0.00375 ETH: a header and a lean delivery at 2 gwei, not a fat one
	const at = 4050
	w.g.dispatch(at, sendMsg(testGNOT, ethGNOT, 40_000_000)) // S
	w.g.mu.Lock()
	r := w.g.results[at][0]
	j := &gnolight.Event{Type: "Junk", PkgPath: "gno.land/r/attacker/junk", Attributes: []gnolight.Attribute{{Key: "x", Value: strings.Repeat("z", 7400)}}}
	anys := [][]byte{gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(r.Events[0])), gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(j))}
	r.Events = append(r.Events, j)
	r.Leaf = gnolight.EncodeResult(nil, []byte("ok"), anys)
	w.g.head = at + 5
	w.g.mu.Unlock()
	u := updaterOver(t, w)
	u.state.Out.Index.Scanned, w.r.state.Out.Index.Scanned = 100, 100 // both ran to 100 and were down since
	if err := u.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.e.trusted != 100 || len(u.state.Budget.Entries) != 0 {
		t.Fatalf("the updater's first step after the gap, its scan at %d of %d: trusted %d, ledger %s (plan: %s)", u.state.Out.Index.Scanned, w.g.head, w.e.trusted, u.state.Budget.text(), statusLine(u, "plan"))
	}
	for i := 0; i < 4; i++ {
		_ = u.tick(context.Background())
		w.tick()
		w.advance(3 * time.Second)
	}
	got := w.e.deliveredLeaves(t)
	t.Logf("once the scan reached the head: trusted %d, delivered %v, ledger %s; due: %s", w.e.trusted, got, u.state.Budget.text(), statusLine(u, "due"))
	if n, ok := got[0]; ok && n > 4000 {
		t.Fatalf("S booked at the least delivery and delivered at no fee check from a %d-byte leaf", n)
	}
}

// A scan that stays behind the head — a node whose blocks or results do not read — holds route headers: alarmed after
// ten minutes (updater:scan).
func TestF17aScanBehindAlarms(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 7))
	w.g.head = 110
	u := updaterOver(t, w)
	stuck := &stuckBlocks{fakeGno: w.g}
	u.blocks, u.env.blocks = stuck, stuck
	for i := 0; i < 130; i++ {
		_ = u.tick(context.Background())
		w.step(5 * time.Second)
	}
	if text := u.Alarm.Standing()["updater:scan"]; !strings.Contains(text, "wait for it") {
		t.Fatalf("a scan stuck behind the head for ten minutes: %v", u.Alarm.StandingKeys())
	}
}

// stuckBlocks is a gno node whose blocks' results do not read.
type stuckBlocks struct{ *fakeGno }

func (s *stuckBlocks) BlockResults(context.Context, int64) ([]*gnolight.TxResult, error) {
	return nil, errFake("the node does not serve block results")
}

type errFake string

func (e errFake) Error() string { return string(e) }

// E5-GS5: every credit locks ≈0.9–1.7 GNOT of storage deposit from the relayers' gno key, and a burn of one base unit
// was relayed like any other — the route sweeps it, no fee, and the key pays the deposit: ≈300–400 dust burns emptied
// the 500 GNOT float. A burn below its route's fee is left for anyone to Process at their own cost, alarmed; one at
// the fee is delivered.
func TestF17aDustBurnIsLeftForAnyone(t *testing.T) {
	r := newInboundRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	dust := r.chain.burn(m3bGNOT, gnot, r.alice, 99, 80)    // below the route's fee of 100
	honest := r.chain.burn(m3bGNOT, gnot, r.alice, 100, 81) // the fee
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	for _, p := range r.job.st.Pending {
		raw, _ := hex.DecodeString(p.Message)
		for k := 0; k < 2; k++ {
			r.sign(k, p.Nonce, raw, false)
		}
	}
	for i := 0; i < 2; i++ {
		if err := r.tick(); err != nil {
			t.Fatal(err)
		}
	}
	// a Process call's message ends in its token message's amount (32 bytes): 99 is the dust's, 100 the honest one's
	var dusty, fair int
	for _, c := range r.gno.callsOf("Process") {
		switch {
		case strings.HasSuffix(c, "0000000063)"):
			dusty++
		case strings.HasSuffix(c, "0000000064)"):
			fair++
		}
	}
	t.Logf("Process calls: %d of the burn below the fee, %d of the one at it; alarms %v; the queue %d", dusty, fair, r.job.alarm.StandingKeys(), len(r.job.st.Pending))
	if dusty != 0 {
		t.Fatalf("a burn below the route's fee was processed (the relayer's gno key pays its deposit): %v", r.gno.callsOf("Process"))
	}
	if fair == 0 {
		t.Fatalf("the burn at the fee was not delivered: %v", r.gno.callsOf("Process"))
	}
	// the burn below the fee stays pending, left under the fee and judged against it each tick before any read of its
	// own (the integration lane I3: a fee lowered past it makes it a delivery again — TestI3RelayerJudgesDustAgain…)
	queued := false
	for _, p := range r.job.st.Pending {
		queued = queued || p.ID == hex.EncodeToString(dust[:])
	}
	if fee, left := r.job.st.Left[hex.EncodeToString(dust[:])]; !queued || !left || fee != 100 {
		t.Fatalf("the burn below the fee is not kept pending as left under the fee of 100: queued %v, left %v", queued, r.job.st.Left)
	}
	_ = honest
	if text := r.job.alarm.Standing()["inbound:dust:GNOT"]; !strings.Contains(text, "below its fee") {
		t.Fatalf("the burn below the fee is not alarmed: %v", r.job.alarm.StandingKeys())
	}
}

// E5-RT5: the relayers' gno key was planned at 50 credits a day at 0.11 GNOT (the Solana plan's unmeasured guess): its
// 500 GNOT read as 90 days. A credit locks ≈1.3–1.7 GNOT of storage deposit, measured: 500 GNOT is under a week at the
// planned 50 a day, and the runway alarm says so from launch day.
func TestF17aGnoKeyRunwayPlansTheDeposit(t *testing.T) {
	now := time.Now()
	f := &Float{Name: "relayer-gno"}
	text, short := f.Observe(now, 500*1e6, nil)
	t.Logf("500 GNOT on the relayers' gno key at the planned rate: %s", text)
	if !short {
		t.Fatalf("500 GNOT at 50 credits a day of ≈1.3–1.7 GNOT of deposit each reads past seven days: %s", text)
	}
}

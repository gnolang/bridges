package agent

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// Lane F17a (E5-GS2, E5-RT3): the alarms and checks of the bookings' reads that have no behaviour of the parent to fail
// against — the start-up probe, a refused read, the calldata a recording must carry.

// A provider that serves no three days of the client's logs is alarmed eth:logs-depth by the probe the machines make at
// start and every six hours, and a refused read eth:logs when a booking is looked for; a read that answers clears it.
func TestF17aShallowProviderAlarmed(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.e.mu.Lock()
	w.e.block = 100_000
	w.e.mu.Unlock()
	refused := 0
	se := shallowEth{fakeEth: w.e, depth: 10_000, refused: &refused}
	w.r.Eth, w.r.env.eth = se, se
	w.r.env.probeLogs(context.Background(), w.now)
	if _, ok := w.r.Alarm.Standing()["eth:logs-depth"]; !ok {
		t.Fatalf("a provider that serves no three days of logs is not alarmed: %v", w.r.Alarm.StandingKeys())
	}
	if b := w.r.env.bookingOf(context.Background(), w.now, [32]byte{7}, 1, 0); b != nil || w.r.Alarm.Standing()["eth:logs"] == "" {
		t.Fatalf("a refused log read (a header of unknown time, looked for over three days): booking %v, alarms %v", b, w.r.Alarm.StandingKeys())
	}
	w.r.Eth, w.r.env.eth = w.e, w.e
	w.r.env.probeLogs(context.Background(), w.now.Add(7*time.Hour))
	w.r.env.unread = nil
	_ = w.r.env.bookingOf(context.Background(), w.now.Add(7*time.Hour), [32]byte{7}, 1, 0)
	if keys := w.r.Alarm.StandingKeys(); len(keys) > 0 && (contains(keys, "eth:logs") || contains(keys, "eth:logs-depth")) {
		t.Fatalf("the alarms stand once the reads answer: %v", keys)
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// E5-RT3's binding: a provider may name a genuine transaction of the updater's key — one that recorded another header,
// its trailer naming sends — as the recording of a stranger's header; the calldata must carry the header its log
// records, or it books nothing.
func TestF17aRecordingMustCarryItsHeader(t *testing.T) {
	w, u := sixHourSend(t)
	if err := u.tick(context.Background()); err != nil || w.e.trusted != 106 {
		t.Fatalf("the six-hour header: %v, trusted %d", err, w.e.trusted)
	}
	var ours common.Hash
	for _, l := range w.e.logs {
		if l.Topics[0] == ethx.LightClient.Event("HeaderRecorded") {
			ours = l.TxHash
		}
	}
	// a stranger's update records 120; the provider says its recording is the updater's transaction of 106
	w.g.head = 125
	w.e.trust(120)
	w.g.mu.Lock()
	blk := w.g.header(120)
	w.g.mu.Unlock()
	w.e.mu.Lock()
	n := w.e.mine()
	data, _ := ethx.LightClient.ABI.Events["HeaderRecorded"].Inputs.NonIndexed().Pack(w.e.epoch, uint64(3), [32]byte{}, false)
	w.e.logs = append(w.e.logs, ethLog(w.e.client, blk.Hash, 120, data, n, ours))
	w.e.mu.Unlock()
	if b := w.r.env.bookingOf(context.Background(), w.now, blk.Hash, w.e.epoch, 120); b != nil && b.Ours {
		t.Fatalf("the updater's transaction of header 106 booked header 120: %+v", b)
	}
}

// ethLog is the client's HeaderRecorded log of a header in block n, by transaction tx.
func ethLog(client common.Address, hash [32]byte, height int64, data []byte, n uint64, tx common.Hash) types.Log {
	return types.Log{Address: client, Topics: []common.Hash{ethx.LightClient.Event("HeaderRecorded"), common.Hash(hash), common.BigToHash(big.NewInt(height))}, Data: data, BlockNumber: n, BlockHash: blockHash(n), TxHash: tx}
}

// E5-GS2 with E5-GS3: the updater's own view of a header it recorded comes from its own receipt (no log read), kept
// provisional while the finalized block lies below it and for good once it passes — the receipt read again shows the
// recording stands.
func TestF17aOwnBookingTurnsFinal(t *testing.T) {
	w, u := sixHourSend(t)
	w.e.lagLogs = 1 << 30
	if err := u.tick(context.Background()); err != nil || !u.state.Budget.settled(0) {
		t.Fatalf("the six-hour header: %v, %s", err, u.state.Budget.text())
	}
	var b *headerBook
	for _, x := range u.state.Out.Books {
		b = x
	}
	if b == nil || !b.Ours || !b.Prov || b.Tx == "" {
		t.Fatalf("its own view right after: %+v", b)
	}
	for i := 0; i < 20; i++ {
		_ = u.tick(context.Background())
		w.chainStep(time.Minute)
	}
	if !b.Ours || b.Prov {
		t.Fatalf("its own view once the finalized block passed its receipt's: %+v", b)
	}
}

// E5-GS4 across an upgrade: a state file from before the list of headers seen recorded carries the bookings alone. S,
// booked under 109 (its event at 108), is buried under ten re-emits behind a gate flag, and a stranger's update moves the
// client past them, so neither the trusted height nor the newest events lead to 108: the bookings kept do, and S is
// delivered from its booked header, no second header (nor a header proof) bought.
func TestF17aBookingsLeadToTheirEventsAfterAnUpgrade(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
	u := updaterOver(t, w)
	u.state.Budget.Balance = ethx.GweiOf(50_000_000).String()
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 40_000_000)) // S
	w.g.reemitAt(108, 0)
	w.g.head = 112
	w.step(6*time.Hour + time.Minute)
	if err := u.tick(context.Background()); err != nil || w.e.trusted != 109 {
		t.Fatalf("S's six-hour header: %v, trusted %d", err, w.e.trusted)
	}
	w.e.flags[4] = ethx.Flag{BlockNumber: 9, Time: 1}
	w.tick() // the relayer reads S's booking under 109 (held by the flag)
	for h := int64(110); h < 120; h++ {
		w.g.reemitAt(h, 0)
	}
	w.g.head = 125
	w.e.trust(121) // a stranger's update past the re-emits
	u.state.Out.Headers, u.state.Out.HeadersEpoch = nil, 0
	w.r.state.Out.Headers, w.r.state.Out.HeadersEpoch = nil, 0
	w.e.flags[4] = ethx.Flag{}
	for i := 0; i < 4; i++ {
		_ = u.tick(context.Background())
		w.tick()
		w.step(time.Minute)
	}
	got := w.e.deliveredLeaves(t)
	t.Logf("after the upgrade: delivered %v, %d headers bought", got, headersBought(w))
	if len(got) != 1 || got[0] > 1000 || headersBought(w) != 1 {
		t.Fatalf("S after an upgrade: delivered %v, %d headers bought (%s)", got, headersBought(w), w.r.last["deliver:0"])
	}
}

package agent

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// Lane F17a (audit round 5's go-spend, E5-GS2, E5-GS3, the plan red team's E5-RT3): the bookings — who recorded a
// header, and which sends its trailer names — as the two machines read them from the chain.

// sixHourSend is a relayer world, its updater and a fee-short send S (nonce 0) at its six hours, the budget able to
// draw for its header.
func sixHourSend(t *testing.T) (*testWorld, *Updater) {
	t.Helper()
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1} // a send's 50 GNOT fee = 0.0005 ETH < a delivery at 2 gwei
	u := updaterOver(t, w)
	u.state.Budget.Balance = ethx.GweiOf(50_000_000).String() // 0.05 ETH of surplus
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 40_000_000)) // S, nonce 0
	w.g.head = 110
	w.step(6*time.Hour + time.Minute)
	return w, u
}

// headersBought is how many updates and header proofs the fake recorded sends of.
func headersBought(w *testWorld) int {
	return len(w.e.sentOf("update")) + len(w.e.sentOf("proveHeader"))
}

// E5-GS2: a provider that serves no log of the client's (publicnode refuses logs past ≈10,000 blocks; modelled by every
// log read answering none). The updater settled its own header only from eth_getLogs: the pending booking stood an hour
// on its own word, was dropped, and the send waited again and bought another header — one an hour, none booked, none
// alarmed. It settles its header from its own recording transaction's receipt and calldata, and never drops a header
// the chain recorded for want of a log.
func TestF17aOwnHeaderSettlesWithoutLogs(t *testing.T) {
	w, u := sixHourSend(t)
	w.e.lagLogs = 1 << 30       // no log read ever answers
	for i := 0; i < 6*60; i++ { // six hours of one-minute steps, Ethereum mining as they go
		_ = u.tick(context.Background())
		w.tick()
		w.chainStep(time.Minute)
		if w.g.head < w.e.trusted+5 {
			w.g.head = w.e.trusted + 5
		}
	}
	key := ""
	for k := range u.state.Out.Books {
		key = k
	}
	t.Logf("six hours, no log readable: %d headers bought; ledger %s; pending %d; the updater's book of its header %+v; relayer alarms %v", headersBought(w), u.state.Budget.text(), len(u.state.Budget.Pending), u.state.Out.Books[key], w.r.Alarm.StandingKeys())
	if headersBought(w) != 1 || len(u.state.Budget.Entries) != 1 || len(u.state.Budget.Pending) != 0 {
		t.Fatalf("%d headers bought for one fee-short send in six hours with no log readable (ledger %s, %d pending)", headersBought(w), u.state.Budget.text(), len(u.state.Budget.Pending))
	}
	if b := u.state.Out.Books[key]; b == nil || !b.Ours {
		t.Fatalf("the updater's own view of its header, from its receipt: %+v", b)
	}
	if _, ok := w.r.Alarm.Standing()["delivery:age"]; !ok {
		t.Fatalf("the relayer, which cannot read the booking, holds S unalarmed: %v", w.r.Alarm.StandingKeys())
	}
}

// E5-GS2's second half: a header the chain recorded for this key whose recording the updater cannot read — the update
// mined after the sender's wait gave up (no receipt kept), and no log answering — stood an hour and was dropped, and
// the send bought its header again. It stands, alarmed bookings:unread, and nothing is bought again.
func TestF17aRecordedBookingNeverDroppedForWantOfALog(t *testing.T) {
	w, u := sixHourSend(t)
	w.e.lagLogs = 1 << 30
	w.e.pendNth["update"] = 1 // the update waits in the pool past the sender's wait
	_ = u.tick(context.Background())
	if len(u.state.Budget.Pending) != 1 || len(w.e.sentOf("update")) != 1 {
		t.Fatalf("the booking before the send: %+v", u.state.Budget)
	}
	_ = w.e.Settle(context.Background()) // mined: the header recorded with the trailer, its receipt kept by nobody
	for i := 0; i < 3*60; i++ {
		_ = u.tick(context.Background())
		w.tick()
		w.chainStep(time.Minute)
		if w.g.head < w.e.trusted+5 {
			w.g.head = w.e.trusted + 5
		}
	}
	text := u.Alarm.Standing()["bookings:unread"]
	t.Logf("three hours: %d headers bought; pending %d; bookings:unread %q", headersBought(w), len(u.state.Budget.Pending), text)
	if headersBought(w) != 1 || len(u.state.Budget.Pending) != 1 {
		t.Fatalf("a recorded header's booking dropped for want of its log: %d headers bought, %d pending", headersBought(w), len(u.state.Budget.Pending))
	}
	if !strings.Contains(text, "header 106") {
		t.Fatalf("bookings:unread does not stand: %v", u.Alarm.StandingKeys())
	}
}

// shallowEth is a provider that serves no log further back than depth blocks from its head (publicnode: "Archive
// requests require a personal token" past ≈10,000), its refusals counted.
type shallowEth struct {
	*fakeEth
	depth   uint64
	refused *int
}

func (s shallowEth) LogsRange(ctx context.Context, q ethx.LogQuery, from, to uint64) ([]types.Log, error) {
	s.fakeEth.mu.Lock()
	head := s.fakeEth.block
	s.fakeEth.mu.Unlock()
	if head > s.depth && from < head-s.depth {
		*s.refused++
		return nil, errors.New("provider: 403 Forbidden: Archive requests require a personal token")
	}
	return s.fakeEth.LogsRange(ctx, q, from, to)
}

// E5-GS2: the relayer read a header's booking over the last three days of logs, which such a provider refuses whole —
// a booked fee-short send was held for good, silently. A header cannot be recorded before it was made: a six-hour send's
// header is looked for over the hours since its block, which the provider serves; a refusal is alarmed (eth:logs), and
// a provider that cannot serve three days is alarmed at start (eth:logs-depth).
func TestF17aRecentHeaderFoundThroughAShallowProvider(t *testing.T) {
	w, u := sixHourSend(t)
	w.e.mu.Lock()
	w.e.block, w.e.numbered = 100_000, true // a chain of a hundred thousand blocks, 12 s apart
	w.e.mu.Unlock()
	refused := 0
	se := shallowEth{fakeEth: w.e, depth: 10_000, refused: &refused}
	w.r.Eth, w.r.env.eth = se, se
	if err := u.tick(context.Background()); err != nil || !u.state.Budget.settled(0) {
		t.Fatalf("S's six-hour header: %v, %s", err, u.state.Budget.text())
	}
	w.tick()
	got := w.e.deliveredLeaves(t)
	t.Logf("through a provider of 10,000 blocks of logs: delivered %v (%s); %d log reads refused; relayer alarms %v", got, w.r.last["deliver:0"], refused, w.r.Alarm.StandingKeys())
	if len(got) != 1 {
		t.Fatalf("S, booked by the updater, not delivered through a provider of 10,000 blocks of logs: %s", w.r.last["deliver:0"])
	}
}

// reorgRecording moves the client's HeaderRecorded log of the updater's header to a stranger's transaction, as a
// reorganisation that took the stranger's racing update in place of the updater's does: the updater's transaction and
// its receipt are gone from the chain, the stranger's (its calldata without our trailer) records the header.
func reorgRecording(w *testWorld) {
	stranger := common.HexToAddress("0x000000000000000000000000000000000000bad1")
	w.e.mu.Lock()
	defer w.e.mu.Unlock()
	for i := range w.e.logs {
		l := &w.e.logs[i]
		if len(l.Topics) > 1 && l.Topics[0] == ethx.LightClient.Event("HeaderRecorded") {
			ours := l.TxHash
			tx := common.HexToHash("0x5eed")
			to := w.e.client
			in := w.e.txs[ours].Input
			w.e.txs[tx] = &ethx.TxInput{To: &to, Input: in[:len(in)-32*3], From: stranger}
			delete(w.e.receipts, ours)
			delete(w.e.txs, ours)
			l.TxHash = tx
		}
	}
}

// E5-GS3: each machine read a header's booking at latest once and kept it for good. A reorganisation that moves the
// recording of the updater's six-hour header to a stranger's racing update then left the updater with "ours" and the
// relayer, reading after, with "not ours": S held by the relayer, booked in the updater's eyes, no header bought for it,
// nothing alarmed. A booking above the finalized block is read again every minute until the finalized block passes it:
// both machines follow the chain's word, S is held by both and served again, and delivered.
func TestF17aReorgMovesBothMachinesBookings(t *testing.T) {
	w, u := sixHourSend(t)
	if err := u.tick(context.Background()); err != nil || w.e.trusted != 106 || !u.state.Budget.settled(0) {
		t.Fatalf("the six-hour header not bought and booked: %v, trusted %d, %s", err, w.e.trusted, u.state.Budget.text())
	}
	reorgRecording(w)
	book := func(st *outState) *headerBook {
		for _, b := range st.Books {
			if b.Height == 106 {
				return b
			}
		}
		return nil
	}
	for i := 0; i < 12*60; i++ { // twelve hours of one-minute steps, Ethereum mining as they go
		_ = u.tick(context.Background())
		w.tick()
		w.chainStep(time.Minute)
		if w.g.head < w.e.trusted+5 {
			w.g.head = w.e.trusted + 5
		}
		if i == 2 { // two minutes on: both machines read the chain's word (each reads a booking above the finalized block again every minute)
			ub, rb := book(&u.state.Out), book(&w.r.state.Out)
			t.Logf("two minutes after the reorganisation: header 106 as the updater keeps it %+v, as the relayer %+v", ub, rb)
			if ub == nil || rb == nil || ub.Ours || rb.Ours {
				t.Fatalf("the two machines' readings of header 106 two minutes after the reorganisation: the updater %+v, the relayer %+v", ub, rb)
			}
		}
	}
	got := w.e.deliveredLeaves(t)
	t.Logf("twelve hours after the reorganisation: %d headers; delivered %v; ledger %s", headersBought(w), got, u.state.Budget.text())
	if len(got) != 1 || got[0] > 1000 {
		t.Fatalf("S not delivered from its own leaf after the reorganisation: %v (%s)", got, w.r.last["deliver:0"])
	}
}

// E5-GS3's second door: the step after an update that mined past the sender's wait, a read at latest answered by a node
// a block behind (the header's entry empty) dropped the pending booking as "can no longer be recorded" — the client's
// trusted height, read from a fresher node, past it — though the chain had just recorded it with the trailer: the
// relayer then delivered at no fee check what the ledger never booked. The booking ends only on the finalized block's
// word; the next read settles it.
func TestF17aLaggingReadDropsNoRecordedBooking(t *testing.T) {
	w, u := sixHourSend(t)
	w.e.pendNth["update"] = 1
	_ = u.tick(context.Background())
	if len(u.state.Budget.Pending) != 1 {
		t.Fatalf("no pending booking: %+v", u.state.Budget)
	}
	w.step(16 * time.Minute)
	_ = w.e.Settle(context.Background()) // mined: recorded with the trailer
	hash := u.state.Budget.Pending[0].Hash
	var h [32]byte
	copy(h[:], common.FromHex(hash))
	w.e.mu.Lock()
	w.e.hideEntries = map[[32]byte]int{h: 1} // the next read of its entry at latest: a node a block behind
	w.e.mu.Unlock()
	_ = u.tick(context.Background())
	_ = u.tick(context.Background())
	w.tick()
	t.Logf("after the lagging read: pending %d, S booked in the ledger %v, delivered %v", len(u.state.Budget.Pending), u.state.Budget.settled(0), w.e.deliveredLeaves(t))
	if !u.state.Budget.settled(0) {
		t.Fatalf("one lagging read dropped the booking of a header this key recorded: the ledger %s", u.state.Budget.text())
	}
}

// The plan red team's E5-RT3: the relayer delivered at no fee check every send a trailer named when its one provider
// said the recording transaction was "from" eth.updater — the provider's JSON field, never the signer. A lying provider
// names any recorded header's transaction as the updater's with a trailer of its own: the sender is recovered from the
// signature, and a stranger's recording books nothing.
func TestF17aForgedSenderBooksNothing(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.00001, "BUBBLE": 1}
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 40_000_000)) // S, fee-short
	w.g.head = 110
	w.e.trust(106) // a stranger's update records 106
	stranger := common.HexToAddress("0x000000000000000000000000000000000000bad1")
	tx := common.HexToHash("0x0bad")
	w.g.mu.Lock()
	blk := w.g.header(106)
	w.g.mu.Unlock()
	data, _ := ethx.LightClient.ABI.Events["HeaderRecorded"].Inputs.NonIndexed().Pack(w.e.epoch, uint64(2), [32]byte{}, false)
	w.e.mu.Lock()
	n := w.e.mine()
	to := w.e.client
	w.e.txs[tx] = &ethx.TxInput{To: &to, Input: append([]byte{1, 2, 3, 4}, bookingTrailer([]uint32{0})...), From: stranger}
	w.e.lieFrom = map[common.Hash]common.Address{tx: ethKey} // the provider's JSON names the updater as its sender
	w.e.logs = append(w.e.logs, types.Log{Address: w.e.client, Topics: []common.Hash{ethx.LightClient.Event("HeaderRecorded"), common.Hash(blk.Hash), common.BigToHash(big.NewInt(106))}, Data: data, BlockNumber: n, BlockHash: blockHash(n), TxHash: tx})
	w.e.mu.Unlock()
	w.tick()
	if got := w.e.deliveredLeaves(t); len(got) != 0 {
		t.Fatalf("a stranger's recording the provider says is the updater's booked S at no fee check: delivered %v", got)
	}
	if !strings.Contains(w.r.last["deliver:0"], "held") {
		t.Fatalf("S's line: %s", w.r.last["deliver:0"])
	}
}

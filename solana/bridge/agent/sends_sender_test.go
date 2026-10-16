package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"

	"github.com/gagliardetto/solana-go"
)

// sendWalk is a mailbox of messages to one token program on Solana, and readSends over it.
type sendWalk struct {
	t                     *testing.T
	f                     *fakeChain
	cfg                   *Config
	route, succ, stranger [32]byte
	token                 [32]byte
	provider              *countingSol
	mb                    *mailboxJSON
	ids                   [][32]byte
	sends                 map[string]*sendRec
	next                  uint32
	sr                    sendReads
}

func newSendWalk(t *testing.T) *sendWalk {
	w := &sendWalk{t: t, f: newFake(), cfg: &Config{}, token: [32]byte{9}, stranger: [32]byte{0xee}, sends: map[string]*sendRec{}}
	w.cfg.Gno.Mailbox = testMailbox
	w.cfg.Solana.Domain = hl.DomainSolanaTestnet
	w.cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	w.cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: testRoute, Token: hl.Base58(w.token[:])}}
	w.route, _ = hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	w.succ, _ = hl.GnoAddressToBytes32(hl.GnoPackageAddress(testSucc))
	w.provider = &countingSol{owner: sol.Mailbox{Program: w.cfg.SolMailbox()}.Program, processed: map[solana.PublicKey]bool{}}
	w.mb = &mailboxJSON{Domain: hl.DomainGnoTest}
	w.sr = sendReads{Routes: map[string]*routeJSON{"BUBBLE": {Lock: 12}}, RefundCounts: map[string]int64{}}
	return w
}

// put dispatches a message to the token program from a sender, for an amount; it returns the nonce.
func (w *sendWalk) put(sender [32]byte, amount uint64) uint32 {
	n := w.mb.Count
	m := hl.Message{Version: 3, Nonce: n, Origin: hl.DomainGnoTest, Sender: sender, Destination: hl.DomainSolanaTestnet, Recipient: w.token, Body: hl.TokenMessage([32]byte{1}, amount, nil)}
	w.f.renders[testMailbox+":msg/"+strconv.FormatUint(uint64(n), 10)] = msgRender(m, int64(50+n))
	w.ids = append(w.ids, hl.ID(m.Bytes()))
	w.mb.Count++
	return n
}

func (w *sendWalk) read(what string) {
	w.t.Helper()
	if _, err := readSends(context.Background(), w.f, w.provider, w.cfg, w.mb, 100, w.sends, &w.next, time.Unix(1_800_000_000, 0), w.sr); err != nil {
		w.t.Fatalf("%s: %v", what, err)
	}
}

// inflight is what the monitors and hlwatch count in flight out: every send undelivered and not refunded.
func (w *sendWalk) inflight() int64 {
	var n int64
	for _, s := range w.sends {
		if s.InFlight() {
			n += s.Amount
		}
	}
	return n
}

// E3-CF1 (found by the Ethereum port's third audit round): hlmailbox's Dispatch is open, so anyone may send a
// message to a token program on Solana with any amount in its body. The program refuses it — its sender is no
// enrolled router — so it never delivers, and a monitor or hlwatch that counted it as a send held it in flight
// for good: the identity read BROKEN by its amount, and the monitors flagged credits and payouts on the route and
// the pausable ISM, re-asserted every poll, for one transaction's fee. Only a route realm of the token's line
// sends the token.
func TestStrangersMessageIsNoSend(t *testing.T) {
	w := newSendWalk(t)
	// the route's two sends, and between them a stranger's message to the token program for a billion
	w.put(w.route, 5)
	w.put(w.stranger, 1_000_000_000)
	w.put(w.route, 7)
	w.read("the walk")
	if len(w.sends) != 2 || w.sends["0"] == nil || w.sends["2"] == nil || w.next != 3 {
		t.Fatalf("the sends: %v (next %d); want #0 and #2, next 3", w.sends, w.next)
	}
	if got := w.inflight(); got != 12 {
		t.Fatalf("in flight %d, want 12", got)
	}
	// the identity holds (lock 12 = supply 0 + the 12 in flight), and a monitor flags nothing
	m := testMonitor(w.f)
	mb := mailboxWith()
	m.seat = seatOf(mb.Seats, m.Cfg.Gno.WatchAddress)
	id := identityOf(m.Cfg.Routes[0], w.sr.Routes["BUBBLE"], nil, 100, 0, 1, w.inflight(), 0)
	if v, text := id.Verdict(); v != Holds {
		t.Fatalf("the identity: %v: %s", v, text)
	}
	m.judge(context.Background(), m.Cfg.Routes[0], mb, id)
	if calls := w.f.callsOf(testMailbox + ".SetFlag"); len(calls) != 0 {
		t.Fatalf("flags set over a stranger's message: %v", calls)
	}
}

// The walk follows the token's line (E3-CF1, G46): a message whose sender cannot be judged — the line unread,
// the sender not the configured realm — holds the walk until the line reads; a successor's send is the token's
// while a migration is open; each send keeps its sender; a state file's stranger in flight is dropped once its
// message is read, and a processed send is never read again.
func TestSendWalkFollowsTheLine(t *testing.T) {
	w := newSendWalk(t)
	w.put(w.route, 5)
	w.put(w.stranger, 1_000_000_000)
	w.put(w.route, 7)
	w.read("the first walk")
	if w.sends["0"].Sender != hex.EncodeToString(w.route[:]) {
		t.Fatalf("the sender kept: %q", w.sends["0"].Sender)
	}

	// the line unread: a message the configured realm did not send may be a successor's — the walk holds there,
	// and reads on once the line reads
	w.sr.LineErrs = map[string]error{"BUBBLE": errors.New("the line: render failed")}
	w.put(w.stranger, 3)
	w.put(w.route, 4)
	w.read("the line unread")
	if w.next != 3 || len(w.sends) != 2 {
		t.Fatalf("the walk passed a message it could not judge: next %d, %d sends", w.next, len(w.sends))
	}
	w.sr.LineErrs = nil
	w.read("the line read")
	if w.next != 5 || len(w.sends) != 3 || w.sends["4"] == nil || w.sends["3"] != nil {
		t.Fatalf("after the line read: next %d, sends %v", w.next, w.sends)
	}

	// a migration open: the successor's send is the token's, a stranger's still is not
	w.sr.Migs = map[string]*migration{"BUBBLE": {Symbol: "BUBBLE", Predecessor: testRoute, Successor: testSucc, Pred: &routeJSON{}, Succ: &routeJSON{}}}
	n5 := w.put(w.succ, 6)
	w.put(w.stranger, 8)
	for _, n := range []uint32{0, 2, 4, n5} {
		w.f.renders[testRoute+":send/"+hex.EncodeToString(w.ids[n][:])] = "null"
		w.f.renders[testSucc+":send/"+hex.EncodeToString(w.ids[n][:])] = "null"
	}
	w.read("the migration")
	if w.next != 7 || w.sends["5"] == nil || w.sends["6"] != nil || w.sends["5"].Sender != hex.EncodeToString(w.succ[:]) {
		t.Fatalf("over the migration: next %d, sends %v", w.next, w.sends)
	}
	w.sr.Migs = nil

	// a state file from before E3-CF1: its stranger, in flight, is dropped once read; the route's send keeps its
	// place and gains its sender; a processed send is not read again
	w.sends["1"] = &sendRec{Symbol: "BUBBLE", Amount: 1_000_000_000, Height: 51, ID: hex.EncodeToString(w.ids[1][:])}
	w.sends["0"].Sender = ""
	w.sends["2"].Sender, w.sends["2"].Processed = "", true
	w.provider.processed[sol.Mailbox{Program: w.cfg.SolMailbox()}.ProcessedMessage(w.ids[2])] = true
	delete(w.f.renders, testMailbox+":msg/2") // a processed send's message is never read again
	w.read("an old state file")
	if w.sends["1"] != nil {
		t.Fatal("the old state's stranger stays in flight")
	}
	if w.sends["0"] == nil || w.sends["0"].Sender != hex.EncodeToString(w.route[:]) {
		t.Fatalf("the old state's send: %+v", w.sends["0"])
	}
	if w.sends["2"] == nil || w.sends["2"].Sender != "" {
		t.Fatalf("the processed send: %+v", w.sends["2"])
	}
}

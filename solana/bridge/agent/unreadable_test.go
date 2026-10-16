package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// headerFake answers the reads of a message's header (E3-RL1): vm/qeval of hlmailbox's record (the header bytes,
// the height, the length) and block_results.
type headerFake struct {
	records    map[uint32]record // nonce → the mailbox's record
	answers    map[uint32]string // nonce → a record answer that replaces the node's
	results    map[int64]string  // height → block_results' JSON
	evalErr    error
	resultsErr error
	evals      int
	requests   int
}

type record struct {
	msg    hl.Message
	height int64
}

func newHeaderFake() *headerFake {
	return &headerFake{records: map[uint32]record{}, answers: map[uint32]string{}, results: map[int64]string{}}
}

func (h *headerFake) Query(_ context.Context, path string, data []byte, _ int64) ([]byte, error) {
	h.evals++
	if path != "vm/qeval" {
		return nil, fmt.Errorf("fake: query %s", path)
	}
	if h.evalErr != nil {
		return nil, h.evalErr
	}
	q := string(data)
	for n, r := range h.records {
		// the realm's own names: its record tree, the key of the nonce, the record's type and fields, its hx
		if strings.HasPrefix(q, testMailbox+".") && strings.Contains(q, fmt.Sprintf("sent.Get(nonceKey(%d)).(*sentMsg)", n)) && strings.Contains(q, "hx([]byte(s.raw[:77])), s.height, len(s.raw)") {
			if a, ok := h.answers[n]; ok {
				return []byte(a), nil
			}
			raw := r.msg.Bytes()
			// as the node prints three values: ("<hex>" string), (<h> int64), (<n> int), one a line
			return []byte(fmt.Sprintf("(%q string)\n(%d int64)\n(%d int)", hex.EncodeToString(raw[:hl.HeaderLen]), r.height, len(raw))), nil
		}
	}
	return nil, errors.New(`fake: qeval: nil is not of type *gno.land/r/test/hlmailbox.sentMsg`)
}

func (h *headerFake) Request(_ context.Context, method string, params map[string]any, result any) error {
	h.requests++
	if method != "block_results" {
		return fmt.Errorf("fake: request %s", method)
	}
	if h.resultsErr != nil {
		return h.resultsErr
	}
	height, _ := strconv.ParseInt(params["height"].(string), 10, 64)
	s, ok := h.results[height]
	if !ok {
		return fmt.Errorf("fake: no results at %d", height)
	}
	return json.Unmarshal([]byte(s), result)
}

// headerChain is the fake node with the header reads; headerLight the updater's.
type headerChain struct {
	*fakeChain
	*headerFake
}

type headerLight struct {
	*fakeLight
	*headerFake
}

// resultTx is one transaction of a block's results: the mailbox's Dispatch events of its messages, under a
// pkgpath, failed or not.
type resultTx struct {
	failed bool
	pkg    string
	msgs   []hl.Message
}

// resultsJSON is block_results' result as the node serves it.
func resultsJSON(height int64, txs ...resultTx) string {
	type attr struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	type event struct {
		URL     string `json:"@type"`
		Type    string `json:"type"`
		Attrs   []attr `json:"attrs"`
		PkgPath string `json:"pkg_path"`
	}
	var deliver []map[string]any
	for _, tx := range txs {
		var evs []any
		for _, m := range tx.msgs {
			raw := m.Bytes()
			d := gnolight.Dispatch{ID: hl.ID(raw), Nonce: m.Nonce, Origin: m.Origin, Destination: m.Destination, Sender: m.Sender, Recipient: m.Recipient, Hash: gnolight.MessageHash(raw)}
			e := d.ToEvent(tx.pkg)
			ev := event{URL: gnolight.EventTypeURL, Type: e.Type, PkgPath: e.PkgPath}
			for _, a := range e.Attributes {
				ev.Attrs = append(ev.Attrs, attr{a.Key, a.Value})
			}
			evs = append(evs, ev)
		}
		var errv any
		if tx.failed {
			errv = map[string]string{"@type": "/vm.VMError"}
		}
		deliver = append(deliver, map[string]any{"ResponseBase": map[string]any{"Error": errv, "Data": "", "Events": evs, "Log": "", "Info": ""}, "GasWanted": "0", "GasUsed": "0"})
	}
	b, _ := json.Marshal(map[string]any{"height": strconv.FormatInt(height, 10), "results": map[string]any{"deliver_tx": deliver}})
	return string(b)
}

// bigBody is a stranger's body too big to render: its page fails, its header reads.
var bigBody = []byte(strings.Repeat("x", 50_000))

// unreadable records message m as one whose page fails (no render): the mailbox's record of it, and block h's
// results with its Dispatch event.
func (h *headerFake) unreadable(m hl.Message, height int64) {
	h.records[m.Nonce] = record{m, height}
	h.results[height] = resultsJSON(height, resultTx{pkg: testMailbox, msgs: []hl.Message{m}})
}

// relayerWalk is a relayer over a mailbox of messages: some with pages, some without (E3-RL1).
type relayerWalk struct {
	t     *testing.T
	f     *fakeChain
	hf    *headerFake
	r     *Relayer
	token [32]byte
	route [32]byte
	n     uint32
}

func newRelayerWalk(t *testing.T) *relayerWalk {
	f := newFake()
	w := &relayerWalk{t: t, f: f, hf: newHeaderFake(), token: [32]byte{9}}
	w.r = testRelayer(f, "primary")
	w.r.gno = &headerChain{fakeChain: f, headerFake: w.hf}
	w.r.Cfg.Routes[0].Token = hl.Base58(w.token[:])
	w.route, _ = hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	f.renders[testRoute+":json"] = routeRender(0, 50, 0, "")
	w.r.solProcessed = func(context.Context, [32]byte) (bool, error) { return true, nil } // every message of ours delivered: the walk is what is watched
	return w
}

// put adds a message: with its page when body is a token message, or without one (its header readable).
func (w *relayerWalk) put(sender, rcpt [32]byte, body []byte, page bool) hl.Message {
	m := hl.Message{Version: 3, Nonce: w.n, Origin: hl.DomainGnoTest, Sender: sender, Destination: hl.DomainSolanaTestnet, Recipient: rcpt, Body: body}
	height := int64(50 + w.n)
	if page {
		w.f.renders[testMailbox+":msg/"+strconv.FormatUint(uint64(w.n), 10)] = msgRender(m, height)
	} else {
		w.hf.unreadable(m, height)
	}
	w.n++
	w.f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":%d,"live":true,"routes":{}}`, hl.DomainGnoTest, w.n)
	return m
}

func (w *relayerWalk) walk(what string) error {
	w.t.Helper()
	w.r.resetStep()
	return w.r.deliverUnderLightClient(context.Background())
}

// E3-RL1: a stranger's message whose page the node cannot render (a 50 KB body: out of query gas) no longer stops
// the relayer's walk: its header — the mailbox's record of its height, that block's Dispatch event — names a
// stranger, so it is walked past as "other" with one alarm, and the route's send after it is delivered. Before,
// the walk returned the page's error at that nonce every tick, for good: no later send or decision reached Solana.
func TestUnreadableStrangerDoesNotStopTheRelayer(t *testing.T) {
	w := newRelayerWalk(t)
	stranger := [32]byte{0xee}
	w.put(w.route, w.token, hl.TokenMessage([32]byte{1}, 5, nil), true)
	big := w.put(stranger, w.token, bigBody, false)
	w.put(w.route, w.token, hl.TokenMessage([32]byte{1}, 7, nil), true)
	if err := w.walk("the walk"); err != nil {
		t.Fatalf("the walk stopped: %v", err)
	}
	if w.r.state.GnoNext != 3 || w.r.state.Done["1"] != "other" || w.r.state.Done["2"] != "delivered" {
		t.Fatalf("the walk: next %d, done %v; want next 3, #1 other, #2 delivered", w.r.state.GnoNext, w.r.state.Done)
	}
	id := hl.ID(big.Bytes())
	a, ok := w.r.Alarm.Standing()["msg:unreadable:1"]
	if !ok || !strings.Contains(a, "stranger") || !strings.Contains(a, hex.EncodeToString(stranger[:])) || !strings.Contains(a, "a 50000-byte body") || !strings.Contains(a, hex.EncodeToString(id[:])) {
		t.Fatalf("the stranger's alarm (nonce, sender, body length, id): %q", a)
	}
	if !strings.Contains(w.r.last["deliver:1"], "other") {
		t.Fatalf("the status note: %q", w.r.last["deliver:1"])
	}
	// the ruling is kept: a later tick reads nothing of #1 again, even while the walk cannot advance past it
	evals := w.hf.evals
	w.r.state.GnoNext = 0
	if err := w.walk("again"); err != nil || w.hf.evals != evals {
		t.Fatalf("the stranger's message was read again: err %v, %d header reads (had %d)", err, w.hf.evals, evals)
	}
}

// A message whose page cannot be read and whose header names a sender of ours — a route realm of the token's line,
// or r/hlgov to the admin program — holds the walk, alarmed loudly: it is never walked past, and never delivered
// from anything but its page (our realms' bodies are bounded, so this is a node to look at). The alarm clears
// once the page reads, and the message is delivered then.
func TestUnreadableOursHoldsTheRelayer(t *testing.T) {
	w := newRelayerWalk(t)
	w.r.Cfg.Gno.Gov = "gno.land/r/test/hlgov"
	w.r.Cfg.Solana.Admin = "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"
	gov, _ := w.r.Cfg.GovSender()
	admin := hl.MustKey32(w.r.Cfg.Solana.Admin)
	for _, c := range []struct {
		name         string
		sender, rcpt [32]byte
		body         []byte
	}{
		{"a route's send", w.route, w.token, hl.TokenMessage([32]byte{1}, 5, nil)},
		{"r/hlgov's decision", gov, admin, []byte{7, 0, 0, 0, 0, 0, 0, 0, 1}},
		{"r/hlgov's decision to a program the line does not reach", gov, [32]byte{7}, []byte{7, 0, 0, 0, 0, 0, 0, 0, 2}},
	} {
		w.r.state.GnoNext, w.r.state.Done = w.n, map[string]string{}
		m := w.put(c.sender, c.rcpt, c.body, false)
		nk := strconv.FormatUint(uint64(m.Nonce), 10)
		err := w.walk(c.name)
		if err == nil || w.r.state.GnoNext != m.Nonce || w.r.state.Done[nk] != "" {
			t.Fatalf("%s: the walk passed it: err %v, next %d, done %v", c.name, err, w.r.state.GnoNext, w.r.state.Done)
		}
		if a := w.r.Alarm.Standing()["msg:unreadable:"+nk]; !strings.Contains(a, "OURS") || !strings.Contains(a, "held") {
			t.Fatalf("%s: no loud alarm: %q", c.name, a)
		}
		// the page reads: the alarm clears, and the walk goes on as for any message
		w.f.renders[testMailbox+":msg/"+nk] = msgRender(m, 60)
		_ = w.walk(c.name + ", readable")
		if _, ok := w.r.Alarm.Standing()["msg:unreadable:"+nk]; ok {
			t.Fatalf("%s: the alarm stands after the page read", c.name)
		}
	}
}

// A header that cannot be read holds the walk exactly as before — a transient error never skips a message: the
// record (vm/qeval) unread, an answer that is no record, a record of another nonce or origin, a block whose
// Dispatch event of the nonce disagrees with the record, a reader without the header reads; once the record
// reads, the stranger's message is walked past.
func TestUnreadableHeaderFailureHolds(t *testing.T) {
	w := newRelayerWalk(t)
	stranger := [32]byte{0xee}
	m := w.put(stranger, w.token, bigBody, false)
	w.put(w.route, w.token, hl.TokenMessage([32]byte{1}, 7, nil), true)
	held := func(what string) {
		t.Helper()
		if err := w.walk(what); err == nil || w.r.state.GnoNext != 0 || len(w.r.state.Done) != 0 {
			t.Fatalf("%s: the walk passed a message it could not judge: err %v, next %d, done %v", what, err, w.r.state.GnoNext, w.r.state.Done)
		}
		if _, ok := w.r.Alarm.Standing()["msg:unreadable:0"]; ok {
			t.Fatalf("%s: alarmed as judged", what)
		}
		if !strings.Contains(w.r.last["deliver:0"], "held") {
			t.Fatalf("%s: the note: %q", what, w.r.last["deliver:0"])
		}
	}
	w.hf.evalErr = errors.New("gno rpc abci_query: connection refused")
	held("the record unread")
	w.hf.evalErr = nil
	w.hf.answers[0] = "(undefined)"
	held("an answer that is no record")
	other := m
	other.Nonce = 5
	raw := other.Bytes()
	w.hf.answers[0] = fmt.Sprintf("(%q string)\n(50 int64)\n(%d int)", hex.EncodeToString(raw[:hl.HeaderLen]), len(raw))
	held("a record of another nonce")
	delete(w.hf.answers, 0)
	liar := m
	liar.Sender = w.route
	good := w.hf.results[50]
	w.hf.results[50] = resultsJSON(50, resultTx{pkg: testMailbox, msgs: []hl.Message{liar}})
	held("an event that disagrees with the record")
	w.hf.results[50] = good
	if err := w.walk("the reads work"); err != nil || w.r.state.GnoNext != 2 || w.r.state.Done["0"] != "other" {
		t.Fatalf("once the header reads: err %v, next %d, done %v", err, w.r.state.GnoNext, w.r.state.Done)
	}
	// and a reader without the header reads (a plain node fake) holds too
	w2 := newRelayerWalk(t)
	w2.r.gno = w2.f
	w2.put(stranger, w2.token, bigBody, false)
	if err := w2.walk("no header reader"); err == nil || w2.r.state.GnoNext != 0 {
		t.Fatalf("a reader without the header reads passed it: err %v, next %d", err, w2.r.state.GnoNext)
	}
}

// The record is what is judged; the block's results only add the id and hash. Results that cannot be read — the
// node errs (pruned; a stranger who bloats that block past the client's 8 MB), or the block holds the nonce's
// Dispatch only in a failed transaction (its state reverted) or under another realm's pkgpath — leave the id
// unread and the stranger's message walked past all the same.
func TestUnreadableJudgedFromTheRecord(t *testing.T) {
	for _, c := range []struct {
		name string
		bend func(hf *headerFake, m hl.Message)
	}{
		{"the results unread", func(hf *headerFake, _ hl.Message) {
			hf.resultsErr = errors.New("gno rpc block_results: response too large")
		}},
		{"only a failed transaction's event and another realm's", func(hf *headerFake, m hl.Message) {
			hf.results[50] = resultsJSON(50, resultTx{failed: true, pkg: testMailbox, msgs: []hl.Message{m}}, resultTx{pkg: "gno.land/r/test/impostor", msgs: []hl.Message{m}})
		}},
	} {
		w := newRelayerWalk(t)
		m := w.put([32]byte{0xee}, w.token, bigBody, false)
		w.put(w.route, w.token, hl.TokenMessage([32]byte{1}, 7, nil), true)
		c.bend(w.hf, m)
		if err := w.walk(c.name); err != nil || w.r.state.GnoNext != 2 || w.r.state.Done["0"] != "other" {
			t.Fatalf("%s: err %v, next %d, done %v", c.name, err, w.r.state.GnoNext, w.r.state.Done)
		}
		if a := w.r.Alarm.Standing()["msg:unreadable:0"]; !strings.Contains(a, "its id unread") || !strings.Contains(a, "stranger") {
			t.Fatalf("%s: the alarm: %q", c.name, a)
		}
	}
}

// The send walk of the monitors and hlwatch (readSends): a stranger's message whose page cannot be read is no
// send — walked past, alarmed once — and the route's sends around it are counted (before, the walk returned the
// page's error and counted nothing after it, for good); a send of the token's line whose page cannot be read
// holds the walk, alarmed, never counted from a header; a header that cannot be read holds it as before.
func TestUnreadableStrangerDoesNotStopTheSendWalk(t *testing.T) {
	w := newSendWalk(t)
	hf := newHeaderFake()
	own := &headerChain{fakeChain: w.f, headerFake: hf}
	alarms := &Alarmer{Source: "monitor"}
	w.sr.Alarm = alarms
	read := func(what string) error {
		t.Helper()
		_, err := readSends(context.Background(), own, w.provider, w.cfg, w.mb, 100, w.sends, &w.next, time.Unix(1_800_000_000, 0), w.sr)
		return err
	}
	unreadable := func(sender [32]byte, body []byte) hl.Message {
		m := hl.Message{Version: 3, Nonce: w.mb.Count, Origin: hl.DomainGnoTest, Sender: sender, Destination: hl.DomainSolanaTestnet, Recipient: w.token, Body: body}
		hf.unreadable(m, int64(50+m.Nonce))
		w.ids = append(w.ids, hl.ID(m.Bytes()))
		w.mb.Count++
		return m
	}
	w.put(w.route, 5)
	unreadable(w.stranger, bigBody)
	w.put(w.route, 7)
	if err := read("the walk"); err != nil {
		t.Fatalf("the send walk stopped: %v", err)
	}
	if w.next != 3 || len(w.sends) != 2 || w.sends["0"] == nil || w.sends["2"] == nil || w.inflight() != 12 {
		t.Fatalf("the sends: %v (next %d, in flight %d); want #0 and #2, next 3, 12", w.sends, w.next, w.inflight())
	}
	if a := alarms.Standing()["msg:unreadable:1"]; !strings.Contains(a, hex.EncodeToString(w.stranger[:])) || !strings.Contains(a, "a 50000-byte body") || !strings.Contains(a, "no send") {
		t.Fatalf("the stranger's alarm: %q", a)
	}
	// a message to no token program at all, unreadable: walked past too
	m := hl.Message{Version: 3, Nonce: w.mb.Count, Origin: hl.DomainGnoTest, Sender: w.stranger, Destination: hl.DomainSolanaTestnet, Recipient: [32]byte{8}, Body: bigBody}
	hf.unreadable(m, 60)
	w.mb.Count++
	if err := read("to no token"); err != nil || w.next != 4 {
		t.Fatalf("a stranger's message to no token program: err %v, next %d", err, w.next)
	}

	// the route's own send, unreadable (a page that fails for any reason): held and alarmed, never counted from
	// its header
	ours := unreadable(w.route, hl.TokenMessage([32]byte{1}, 4, nil))
	if err := read("ours"); err == nil || w.next != ours.Nonce || w.sends["4"] != nil {
		t.Fatalf("a send of the line whose page cannot be read: err %v, next %d, sends %v", err, w.next, w.sends)
	}
	if a := alarms.Standing()["msg:unreadable:4"]; !strings.Contains(a, "holds") {
		t.Fatalf("no alarm for the held send: %q", a)
	}
	// its page reads (the read error passed): counted from the page, the alarm cleared
	w.f.renders[testMailbox+":msg/4"] = msgRender(ours, 54)
	if err := read("ours, readable"); err != nil || w.next != 5 || w.sends["4"] == nil {
		t.Fatalf("once its page read: err %v, next %d, sends %v", err, w.next, w.sends)
	}
	if _, ok := alarms.Standing()["msg:unreadable:4"]; ok {
		t.Fatal("the held send's alarm stands after its page read")
	}

	// the header unread: held as before
	unreadable(w.stranger, bigBody)
	hf.evalErr = errors.New("gno rpc abci_query: timeout")
	if err := read("the header unread"); err == nil || w.next != 5 {
		t.Fatalf("a message neither page nor header read was passed: err %v, next %d", err, w.next)
	}
	hf.evalErr = nil
	// the line unread: a stranger's unreadable message to the token may be a successor's (G46) — held, not alarmed
	w.sr.LineErrs = map[string]error{"BUBBLE": errors.New("the line: render failed")}
	if err := read("the line unread"); err != nil || w.next != 5 {
		t.Fatalf("a message whose sender cannot be judged was passed: err %v, next %d", err, w.next)
	}
	w.sr.LineErrs = nil
	if err := read("the line read"); err != nil || w.next != 6 {
		t.Fatalf("after the line read: err %v, next %d", err, w.next)
	}
}

// The updater reads the newest message's height for "a dispatch waits": when that page cannot be read (a
// stranger's big message as the newest), the height comes from the mailbox's record — before, every tick failed
// on it and no header went at all, the interval's and the announcing one's included.
func TestUnreadableNewestDoesNotStopTheUpdater(t *testing.T) {
	f := newFake()
	hf := newHeaderFake()
	cfg := &Config{}
	cfg.Gno.Mailbox = testMailbox
	u := &Updater{Cfg: cfg, gno: &headerLight{fakeLight: &fakeLight{fakeChain: f}, headerFake: hf}}
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":2}`, hl.DomainGnoTest)
	f.renders[testMailbox+":msg/0"] = msgRender(hl.Message{Version: 3, Nonce: 0, Origin: hl.DomainGnoTest}, 40)
	hf.unreadable(hl.Message{Version: 3, Nonce: 1, Origin: hl.DomainGnoTest, Sender: [32]byte{0xee}, Body: bigBody}, 60)
	ctx := context.Background()
	if waits, err := u.dispatchWaits(ctx, 55); err != nil || !waits {
		t.Fatalf("the newest message at 60, trusted 55: waits %v, err %v", waits, err)
	}
	// the height is kept: the next ticks read the mailbox's count, not the message again (each failed render
	// cost the node a query at its gas limit, every tick)
	if waits, err := u.dispatchWaits(ctx, 70); err != nil || waits || hf.evals != 1 {
		t.Fatalf("the newest message at 60, trusted 70: waits %v, err %v, %d record reads", waits, err, hf.evals)
	}
	// a newer message, its record unread: no answer, an error
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":3}`, hl.DomainGnoTest)
	hf.unreadable(hl.Message{Version: 3, Nonce: 2, Origin: hl.DomainGnoTest, Sender: [32]byte{0xee}, Body: bigBody}, 61)
	hf.evalErr = errors.New("gno rpc abci_query: timeout")
	if _, err := u.dispatchWaits(ctx, 55); err == nil {
		t.Fatal("neither page nor record read, and no error")
	}
	hf.evalErr = nil
	if waits, err := u.dispatchWaits(ctx, 61); err != nil || !waits {
		t.Fatalf("the newest message at 61, trusted 61: waits %v, err %v", waits, err)
	}
}

// dispatchIn takes the mailbox's Dispatch of the nonce from a transaction that succeeded, and nothing else.
func TestDispatchIn(t *testing.T) {
	m := func(n uint32, sender byte) hl.Message {
		return hl.Message{Version: 3, Nonce: n, Origin: hl.DomainGnoTest, Sender: [32]byte{sender}, Destination: hl.DomainSolanaTestnet, Recipient: [32]byte{9}, Body: []byte("b")}
	}
	raw := resultsJSON(7,
		resultTx{failed: true, pkg: testMailbox, msgs: []hl.Message{m(3, 1)}},  // reverted
		resultTx{pkg: "gno.land/r/test/impostor", msgs: []hl.Message{m(3, 2)}}, // another realm's event
		resultTx{pkg: testMailbox, msgs: []hl.Message{m(2, 3), m(3, 4)}},       // the mailbox's, a second dispatch beside it
	)
	d, err := dispatchIn(json.RawMessage(raw), testMailbox, 3)
	if err != nil || d.Sender != [32]byte{4} || d.Nonce != 3 || d.ID != hl.ID(m(3, 4).Bytes()) {
		t.Fatalf("the nonce's Dispatch: %+v, %v", d, err)
	}
	if _, err := dispatchIn(json.RawMessage(raw), testMailbox, 5); err == nil {
		t.Fatal("a nonce the block does not carry was found")
	}
	if _, err := dispatchIn(json.RawMessage(`{"height":"7","results":null}`), testMailbox, 3); err == nil {
		t.Fatal("an empty block's results gave a Dispatch")
	}
	// the record's answer as the node prints it (gno v1.2.0, v1.5.1, gno-fixes), and what is not one
	hf := newHeaderFake()
	rec := hl.Message{Version: 3, Nonce: 3, Origin: hl.DomainGnoTest, Sender: [32]byte{5}, Destination: hl.DomainSolanaTestnet, Recipient: [32]byte{9}, Body: bigBody}
	hf.records[3] = record{rec, 27}
	if m, h, n, err := msgRecord(context.Background(), hf, testMailbox, hl.DomainGnoTest, 3); err != nil || h != 27 || n != 50077 || m.Sender != rec.Sender || m.Recipient != rec.Recipient || len(m.Body) != 0 {
		t.Fatalf("the record: %+v %d %d %v", m.Sender, h, n, err)
	}
	if _, _, _, err := msgRecord(context.Background(), hf, testMailbox, hl.DomainGnoTest, 4); err == nil {
		t.Fatal("no record, and no error")
	}
	if _, _, _, err := msgRecord(context.Background(), hf, testMailbox, hl.DomainGnoMainnet, 3); err == nil {
		t.Fatal("a record from another origin was taken")
	}
}

package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// A message whose page cannot be read (E3-RL1). hlmailbox's Dispatch takes any body from anyone, and its page
// msg/<n> hex-encodes and then quotes the whole message, tens of thousands of gas a body byte, so a stranger's
// 50 KB body (one MsgCall, 27M gas and 5.3 GNOT of storage) puts msg/<n>, and every msgs/ page that holds it, past
// the node's 3,000,000,000-gas query limit: the node answers "out of gas in location: CPUCycles" (or "memory
// allocation", at 500 KB). The walks read every nonce in order and stopped there for good — no later send or
// decision reached Solana, the monitors and hlwatch stopped counting sends, and the updater, which reads the newest
// message, sent no header at all while it was a stranger's. Measured on gnodev at gno v1.2.0, at v1.5.1
// (gnoland-1's) and at the e2e's gno-fixes 46457f8e, alike.
//
// So when a page cannot be read, the message is classified from its header, read at a cost that does not grow
// with the body: hlmailbox's own record of the message — the record the page renders — through vm/qeval of its
// unexported state (recordExpr: the 77 header bytes, the height and the length, from one load of the record;
// 8.7M gas at 50 KB, 21.7M at 700 KB; the exported Message(nonce) hex-encodes the whole body: 173M gas at 50 KB,
// 2.3B at 700 KB). The block's results then give the message's id and hash, best effort: that block's Dispatch
// event of the mailbox's pkgpath for the nonce (§13: id, nonce, origin, destination, sender, recipient, hash),
// from a transaction that succeeded — a failed one's result may carry the events of the messages that ran before
// it failed, whose state it reverted; only the mailbox's own code emits under its pkgpath (chain.Emit names the
// package of the code that calls it), from a message it stores. An event that disagrees with the record is an
// error; results that cannot be read (pruned, past the client's 8 MB, or with no such event) leave the id unknown
// and the judgment to the record alone, so a stranger who also bloats that block's results stops nothing.
//
// A stranger's message — a sender that is no route realm of the token's line and not r/hlgov to the admin
// program, the relayer's classify — is walked past ("other"; no send), with one alarm per message. A message of
// ours is never walked past and never delivered from anything but its page: it holds the walk, alarmed loudly —
// our realms bound their bodies (a route's token message is 64 bytes, hl.TokenMessage with no metadata; r/hlgov
// refuses a decision body above MaxDecisionBody, 512 bytes, in its dispatch, and Redispatch sends a stored body
// again), and a 141-byte message's page costs 18M gas. A header that cannot be read either holds the walk as
// before: a transient error never skips a message.

// headerReader reads what a message's page is not needed for: an ABCI query (vm/qeval) and a JSON-RPC request
// (block_results) — the gno client's own. A reader without them reads no header, and the walk holds.
type headerReader interface {
	Query(ctx context.Context, path string, data []byte, height int64) ([]byte, error)
	Request(ctx context.Context, method string, params map[string]any, result any) error
}

// MsgHeader is a dispatched message without its body, for a message whose page could not be read (E3-RL1).
type MsgHeader struct {
	Msg    hl.Message // the 77-byte header — version, nonce, origin, sender, destination, recipient — no body (the record)
	Height int64      // the block it was dispatched in (the record)
	Bytes  int        // the message's length: the header and the body (the record)
	// ID and Hash are the message's id and sha256, from the block's Dispatch event; Event says they were read
	// (the event found, agreeing with the record).
	ID, Hash [32]byte
	Event    bool
	Page     error // why its page could not be read
}

// msg is the message as the walks classify it: its header, no body.
func (h *MsgHeader) msg() *gnoMsg {
	return &gnoMsg{Msg: h.Msg, ID: h.ID, Height: h.Height}
}

// Facts is the header in words, for an alarm.
func (h *MsgHeader) Facts() string {
	id := "its id unread"
	if h.Event {
		id = fmt.Sprintf("id %x", h.ID[:])
	}
	return fmt.Sprintf("its page cannot be read (%v); the mailbox's record names the sender %x and the recipient %x on domain %d, a message of %d bytes (a %d-byte body) dispatched in block %d, %s",
		h.Page, h.Msg.Sender[:], h.Msg.Recipient[:], h.Msg.Destination, h.Bytes, h.Bytes-hl.HeaderLen, h.Height, id)
}

// unreadableKey is the alarm of a message whose page cannot be read, by nonce.
func unreadableKey(n uint32) string { return "msg:unreadable:" + strconv.FormatUint(uint64(n), 10) }

// recordExpr is the vm/qeval expression for hlmailbox's record of message n (`sent`: nonceKey(n) → *sentMsg{raw,
// root, height}, mailbox.gno): the message's 77 header bytes in hex (the realm's hx), its height and its length,
// from one load of the record. The node evaluates it in the realm's own package block, where unexported names
// resolve (gno v1.2.0, v1.5.1, gno-fixes 46457f8e); a nonce with no record fails the type assertion, an error,
// never a value.
func recordExpr(n uint32) string {
	return fmt.Sprintf("func() (string, int64, int) { s := sent.Get(nonceKey(%d)).(*sentMsg); return hx([]byte(s.raw[:%d])), s.height, len(s.raw) }()", n, hl.HeaderLen)
}

var recordRE = regexp.MustCompile(`^\("([0-9a-f]+)" string\)\s+\(([0-9]+) int64\)\s+\(([0-9]+) int\)$`)

// errNoHeaderReader is a reader that cannot read a message's header (the tests' plain fakes).
var errNoHeaderReader = errors.New("this reader reads no message header")

// msgRecord is hlmailbox's record of message n: its header — the nonce asked for, from the mailbox's domain —
// its height and its length.
func msgRecord(ctx context.Context, hr headerReader, mailbox string, domain, n uint32) (hl.Message, int64, int, error) {
	bad := func(out []byte) error { return fmt.Errorf("the mailbox's record of message %d: %.300q", n, out) }
	out, err := hr.Query(ctx, "vm/qeval", []byte(mailbox+"."+recordExpr(n)), 0)
	if err != nil {
		return hl.Message{}, 0, 0, fmt.Errorf("the mailbox's record of message %d: %w", n, err)
	}
	f := recordRE.FindStringSubmatch(strings.TrimSpace(string(out)))
	if f == nil {
		return hl.Message{}, 0, 0, bad(out)
	}
	raw, err1 := hex.DecodeString(f[1])
	height, err2 := strconv.ParseInt(f[2], 10, 64)
	size, err3 := strconv.Atoi(f[3])
	if err1 != nil || err2 != nil || err3 != nil || len(raw) != hl.HeaderLen || height <= 0 || size < hl.HeaderLen {
		return hl.Message{}, 0, 0, bad(out)
	}
	m, err := hl.ParseMessage(raw)
	if err != nil || m.Version != hl.Version || m.Nonce != n || m.Origin != domain {
		return hl.Message{}, 0, 0, bad(out)
	}
	return m, height, size, nil
}

// qevalCheck is the self-check of the node property E3-RL1's reads rest on (E4-SF6): vm/qeval of hlmailbox's
// unexported state — the mailbox's record of message 0 (msgRecord) and, once anything was delivered, the first page of
// its delivered ids (doneExpr) — at a machine's start and hourly, gno:qeval alarmed while the node does not answer them
// (tried again each minute while it lasts). A node that refuses or limits vm/qeval — an operator moving the own node,
// a gno release that keeps qeval to exported names, a provider filtering abci paths — turned a stranger's message too
// big to render back into a held walk (the relayers', the monitors'), left the updater blind to its newest message
// and the forged-credit rule unable to find the credits no index holds, with a note or a log line at the moment it was
// needed: now it is an alarm before.
type qevalCheck struct{ next time.Time }

const (
	qevalCheckEvery = time.Hour
	qevalRetry      = time.Minute
)

// run checks when due: a mailbox that cannot be read is tried again in a minute, the alarm as it stands.
func (q *qevalCheck) run(ctx context.Context, g gnoReader, mailbox string, alarm *Alarmer, now time.Time) {
	if now.Before(q.next) {
		return
	}
	mb, err := mailboxAt(ctx, g, mailbox, 0)
	if err != nil {
		q.next = now.Add(qevalRetry)
		return
	}
	if err := qevalAnswers(ctx, g, mailbox, mb); err != nil {
		q.next = now.Add(qevalRetry)
		alarm.Alarm("gno:qeval", fmt.Sprintf("the own node does not answer vm/qeval of hlmailbox's state (%v): a message whose page cannot be read holds the relayers' and the monitors' walks again, the updater cannot read its newest message, the forged-credit rule cannot find the credits no index holds — check the node and its query paths (E4-SF6, E3-RL1)", err))
		return
	}
	q.next = now.Add(qevalCheckEvery)
	alarm.Clear("gno:qeval")
}

// qevalAnswers reads what the qeval reads need of a mailbox: the record of message 0 (once there is one) and the first
// page of its delivered ids (once there is one).
func qevalAnswers(ctx context.Context, g gnoReader, mailbox string, mb *mailboxJSON) error {
	q, ok := g.(queryReader)
	if !ok {
		return errNoQuery
	}
	if mb.Count > 0 {
		hr, ok := g.(headerReader)
		if !ok {
			return errNoHeaderReader
		}
		if _, _, _, err := msgRecord(ctx, hr, mailbox, mb.Domain, 0); err != nil {
			return err
		}
	}
	if mb.Delivered > 0 {
		page, err := donePageAt(ctx, q, mailbox, 0)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return fmt.Errorf("hlmailbox's delivered ids: none answered, %d delivered", mb.Delivered)
		}
	}
	return nil
}

// ReadMsgHeader reads message n's header without its page (E3-RL1): the mailbox's record of it, then — for its
// id and hash, best effort — that block's Dispatch event of the mailbox for the nonce, which must agree with the
// record. page is why the page could not be read.
func ReadMsgHeader(ctx context.Context, g GnoReader, mailbox string, domain, n uint32, page error) (*MsgHeader, error) {
	hr, ok := g.(headerReader)
	if !ok {
		return nil, errNoHeaderReader
	}
	m, height, size, err := msgRecord(ctx, hr, mailbox, domain, n)
	if err != nil {
		return nil, err
	}
	h := &MsgHeader{Msg: m, Height: height, Bytes: size, Page: page}
	var raw json.RawMessage
	if hr.Request(ctx, "block_results", map[string]any{"height": strconv.FormatInt(height, 10)}, &raw) != nil {
		return h, nil // the id unread; the record is what is judged
	}
	d, err := dispatchIn(raw, mailbox, n)
	if err != nil {
		return h, nil
	}
	if d.Origin != m.Origin || d.Sender != m.Sender || d.Destination != m.Destination || d.Recipient != m.Recipient {
		return nil, fmt.Errorf("block %d's Dispatch of message %d (sender %x, recipient %x on %d) disagrees with the mailbox's record (sender %x, recipient %x on %d)", height, n, d.Sender[:], d.Recipient[:], d.Destination, m.Sender[:], m.Recipient[:], m.Destination)
	}
	h.ID, h.Hash, h.Event = d.ID, d.Hash, true
	return h, nil
}

// dispatchIn is the mailbox realm's Dispatch event for a nonce among a block's results (block_results' JSON),
// from a transaction that succeeded: a failed transaction's result may carry the events of its messages that
// ran, whose state it reverted.
func dispatchIn(raw json.RawMessage, mailbox string, nonce uint32) (gnolight.Dispatch, error) {
	var j struct {
		Results *struct {
			DeliverTx []struct {
				ResponseBase struct {
					Error  json.RawMessage   `json:"Error"`
					Events []json.RawMessage `json:"Events"`
				} `json:"ResponseBase"`
			} `json:"deliver_tx"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return gnolight.Dispatch{}, err
	}
	if j.Results != nil {
		for _, tx := range j.Results.DeliverTx {
			if e := strings.TrimSpace(string(tx.ResponseBase.Error)); e != "" && e != "null" {
				continue
			}
			for _, raw := range tx.ResponseBase.Events {
				var e struct {
					URL   string `json:"@type"`
					Type  string `json:"type"`
					Attrs []struct {
						Key   string `json:"key"`
						Value string `json:"value"`
					} `json:"attrs"`
					PkgPath string `json:"pkg_path"`
				}
				if json.Unmarshal(raw, &e) != nil || e.URL != gnolight.EventTypeURL || e.Type != gnolight.DispatchType || e.PkgPath != mailbox {
					continue
				}
				ev := &gnolight.Event{Type: e.Type, PkgPath: e.PkgPath}
				for _, a := range e.Attrs {
					ev.Attributes = append(ev.Attributes, gnolight.Attribute{Key: a.Key, Value: a.Value})
				}
				d, err := gnolight.DispatchFromEvent(ev)
				if err != nil {
					return gnolight.Dispatch{}, fmt.Errorf("a Dispatch event of %s: %w", mailbox, err)
				}
				if d.Nonce == nonce {
					return d, nil
				}
			}
		}
	}
	return gnolight.Dispatch{}, fmt.Errorf("no Dispatch event of %s for message %d in a transaction that succeeded", mailbox, nonce)
}

// walkMessage is message n as a walk reads it: its page at H, or — when the page cannot be read — its header
// (E3-RL1), returned beside it; a header that cannot be read either leaves the page's error, and the walk holds.
func walkMessage(ctx context.Context, g gnoReader, mailbox string, domain, n uint32, H int64) (*gnoMsg, *MsgHeader, error) {
	s, err := g.RenderAt(ctx, mailbox, "msg/"+strconv.FormatUint(uint64(n), 10), H)
	if err == nil {
		msg, err := parseGnoMsg(s, domain, n)
		return msg, nil, err
	}
	h, herr := ReadMsgHeader(ctx, g, mailbox, domain, n, err)
	if herr != nil {
		return nil, nil, fmt.Errorf("%w (nor its header: %v)", err, herr)
	}
	return h.msg(), h, nil
}

// pastUnreadable is the relayer's ruling on message n, whose page could not be read (E3-RL1): true when its
// header names no message of ours — walked past as "other", alarmed once; false when the walk holds as before —
// the header could not be read either, or it names a sender of ours (alarmed loudly), or one the token's line,
// unread this tick, may yet name (G46).
func (r *Relayer) pastUnreadable(ctx context.Context, domain, n uint32, page error) bool {
	nk := strconv.FormatUint(uint64(n), 10)
	key := "deliver:" + nk
	h, err := ReadMsgHeader(ctx, r.gno, r.Cfg.Gno.Mailbox, domain, n, page)
	if err != nil {
		r.note(key, fmt.Sprintf("held: its page could not be read (%v), nor its header (%v)", page, err))
		return false
	}
	switch kind, rt := r.classify(h.msg()); kind {
	case kindOther:
		text := fmt.Sprintf("gno #%d is a stranger's message: %s — no route realm of a token's line, and not r/hlgov to the admin program; walked past as other, never delivered (E3-RL1: a body this big puts its page past the node's query gas)", n, h.Facts())
		r.Alarm.AlarmEvery(unreadableKey(n), text, 24*time.Hour)
		r.note(key, "other: "+text)
		return true
	case kindLineUnread:
		r.note(key, fmt.Sprintf("held: %s; %s's line of migrations could not be read this tick, so the sender may be a successor realm of it (G46): judged once the line reads", h.Facts(), rt.Symbol))
		return false
	default:
		ours := "r/hlgov"
		if rt != nil {
			ours = "a route realm of " + rt.Symbol + "'s line"
		}
		r.Alarm.Alarm(unreadableKey(n), fmt.Sprintf("gno #%d is OURS (%s) and %s: held — never walked past, never delivered from anything but its page. Our realms bound their bodies (a route's token message is 64 bytes, r/hlgov's decision at most 512) far under what renders: check the node (a read error that passes clears this) and this message (E3-RL1)", n, ours, h.Facts()))
		r.note(key, "held: a message of ours whose page cannot be read (alarmed)")
		return false
	}
}

// sendHeld is the send walk's ruling on a message whose page could not be read and whose header names a sender
// of the token's line (E3-RL1): never counted from a header and never walked past — the walk holds, alarmed.
func sendHeld(sr sendReads, n uint32, symbol string, h *MsgHeader) error {
	sr.Alarm.Alarm(unreadableKey(n), fmt.Sprintf("gno #%d is a send of %s's line and %s: the send walk holds there — a send is counted from its page alone. Our realms' token messages are 64 bytes, far under what renders: check the node (a read error that passes clears this) and this message (E3-RL1)", n, symbol, h.Facts()))
	return fmt.Errorf("gno #%d, a send of %s, cannot be read: %w", n, symbol, h.Page)
}

// sendPast alarms a message the send walk passed from its header alone (E3-RL1): no send of a token's line.
func sendPast(sr sendReads, n uint32, h *MsgHeader) {
	sr.Alarm.AlarmEvery(unreadableKey(n), fmt.Sprintf("gno #%d: %s — no send of a token's line: walked past, counted as no send (E3-RL1: a body this big puts its page past the node's query gas)", n, h.Facts()), 24*time.Hour)
}

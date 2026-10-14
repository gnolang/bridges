package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// What /api/<chain>/activity serves (bridge/SITE.md, M7): the bridge's recent events as the two chains record
// them, each with its time, for the page's picture of the machine — the sends locked on gno.land (hlmailbox's
// dispatches, the route's send record), the headers the far side's light client accepted and the proofs staged
// under them, the mints there, the burns toward gno.land, the credits and the payouts on gno.land (the routes'
// claims) — with the signers' lamps, the status beside them, and the last transfer each way with every step's
// time, which the page replays.
//
// One index per chain. It answers at once from its last reading and, when that is older than ten seconds,
// refreshes in the background — one refresh at a time, on a copy that replaces the reading whole — so a public
// provider's slow answer (fifteen seconds is seen) delays the picture, never the page. Each listing is read only
// when a counter moved, each transaction and record once.

// gnoDispatch is one message hlmailbox dispatched: a route's send or r/hlgov's decision.
type gnoDispatch struct {
	Nonce    uint32
	ID       string
	Height   int64  // the block it was dispatched in
	Hash     string // sha256 of the message bytes (hex): the key the far side stages its proof under
	Kind     string // "send", "decision", "other"
	Token    string
	Amount   int64
	To       string // the recipient on the far side, in its own form
	From     string // the sender on gno.land (the send record's)
	At       int64  // the send record's time; a decision's, its block's
	Decision string // a decision's kind and number
}

// claimRec is a burn's claim on gno.land as the index last read it.
type claimRec struct {
	Token  string
	To     string
	Amount int64
	Paid   int64
	State  string
	At     int64 // credited at (the block's time)
	Height int64 // credited in
	Swept  int64
	PaidAt int64 // when this reader saw the paid part grow; 0 when it was paid before the first look (the chain keeps no time)
	Final  bool  // paid in full, voided or swept: not read again
}

// activityState is one reading of a chain's index. A refresh works on a copy and replaces the reading whole, so a
// reading handed out is never written again.
type activityState struct {
	other    OtherActivity
	count    uint32 // hlmailbox's dispatch count at this reading
	received int    // the messages it took in
	dispatch map[uint32]*gnoDispatch
	claims   map[string]*claimRec
	times    map[int64]int64 // a block's time by height, read once
	errors   []string
}

func newActivityState() *activityState {
	return &activityState{dispatch: map[uint32]*gnoDispatch{}, claims: map[string]*claimRec{}, times: map[int64]int64{}}
}

// clone is a copy a refresh may write: the maps copied, the claims' records too (a refresh updates them); a
// dispatch is never changed once read.
func (st *activityState) clone() *activityState {
	c := newActivityState()
	if st == nil {
		return c
	}
	c.count, c.received = st.count, st.received
	for k, v := range st.dispatch {
		c.dispatch[k] = v
	}
	for k, v := range st.claims {
		x := *v
		c.claims[k] = &x
	}
	for k, v := range st.times {
		c.times[k] = v
	}
	c.other = st.other.clone()
	return c
}

func (a OtherActivity) clone() OtherActivity {
	c := a
	c.Errors, c.failed, c.failOrder = nil, nil, nil
	c.Headers = append([]HeaderEvent(nil), a.Headers...)
	if a.seen != nil {
		c.seen, c.stages, c.again = map[string]bool{}, map[string]int64{}, map[string]bool{}
		c.Proofs, c.Deliveries, c.Burns = map[string]ProofEvent{}, map[string]DeliveryEvent{}, map[string]Burn{}
		for k, v := range a.seen {
			c.seen[k] = v
		}
		for k, v := range a.stages {
			c.stages[k] = v
		}
		for k, v := range a.again {
			c.again[k] = v
		}
		for k, v := range a.Proofs {
			c.Proofs[k] = v
		}
		for k, v := range a.Deliveries {
			c.Deliveries[k] = v
		}
		for k, v := range a.Burns {
			c.Burns[k] = v
		}
	}
	return c
}

// chainActivity is one chain's index: its reading, and the tracker's cache of sends older than the listing.
type chainActivity struct {
	refreshMu sync.Mutex // one refresh at a time
	mu        sync.Mutex // st and at
	st        *activityState
	at        time.Time
	olderMu   sync.Mutex
	older     map[string]*gnoDispatch
	pagesRead map[int64]bool
}

func (s *server) activityOf(id string) *chainActivity {
	s.actsMu.Lock()
	defer s.actsMu.Unlock()
	a := s.acts[id]
	if a == nil {
		a = &chainActivity{older: map[string]*gnoDispatch{}, pagesRead: map[int64]bool{}}
		s.acts[id] = a
	}
	return a
}

// refreshBudget bounds a background refresh; readBudget each read inside one; firstWait how long a request waits
// for the very first reading before it answers without one.
const (
	refreshBudget = 90 * time.Second
	readBudget    = 15 * time.Second
	firstWait     = 4 * time.Second
)

// activityNow is a chain's reading, at once: a stale one starts a refresh in the background. Before the first
// reading exists a request waits for it up to firstWait, then answers with an empty one that says so (warm reads
// one at start). With syncRefresh — the tests and the mock — every refresh is made in the request.
func (s *server) activityNow(ctx context.Context, ch *ChainConfig, other Other) *activityState {
	a := s.activityOf(ch.ID)
	a.mu.Lock()
	st, at := a.st, a.at
	a.mu.Unlock()
	if st == nil || s.now().Sub(at) >= s.activityTTL {
		if s.syncRefresh {
			s.refresh(ctx, ch, other, a)
		} else {
			s.bg.run("activity/"+ch.ID, func() {
				c, cancel := context.WithTimeout(context.Background(), refreshBudget)
				defer cancel()
				s.refresh(c, ch, other, a)
			})
			if st == nil {
				waitFor(ctx, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.st != nil })
			}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil {
		empty := newActivityState()
		empty.errors = []string{"the first reading of the chains is under way; the picture fills in a moment"}
		return empty
	}
	return a.st
}

// waitFor polls ready until it holds, the request ends or firstWait passes.
func waitFor(ctx context.Context, ready func() bool) {
	deadline := time.Now().Add(firstWait)
	for !ready() && time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(50 * time.Millisecond)
	}
}

// refresh reads what moved into a copy of the reading and puts the copy in its place.
func (s *server) refresh(ctx context.Context, ch *ChainConfig, other Other, a *chainActivity) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	a.mu.Lock()
	cur, at := a.st, a.at
	a.mu.Unlock()
	if cur != nil && s.now().Sub(at) < s.activityTTL {
		return // another refresh just finished
	}
	st := cur.clone()
	mb, err := s.freshMailbox(ctx)
	if err != nil {
		st.errors = append(st.errors, "gno.land: "+agent.Redact(err.Error()))
	} else {
		if cur == nil || mb.Count != st.count {
			if err := s.readDispatches(ctx, ch, other, st, mb.Count); err != nil {
				st.errors = append(st.errors, "gno.land's dispatches: "+agent.Redact(err.Error()))
			}
		}
		st.received = mb.Delivered
	}
	pending := false
	for _, d := range st.dispatch {
		if _, ok := st.other.Deliveries[d.ID]; !ok && d.Kind == "send" {
			pending = true
		}
	}
	other.Activity(ctx, &st.other, pending)
	st.errors = append(st.errors, st.other.Errors...)
	s.readClaims(ctx, ch, st)
	a.mu.Lock()
	a.st, a.at = st, s.now()
	a.mu.Unlock()
}

// bgRunner runs one background job per key at a time.
type bgRunner struct {
	mu sync.Mutex
	on map[string]bool
}

func (b *bgRunner) run(key string, f func()) {
	b.mu.Lock()
	if b.on == nil {
		b.on = map[string]bool{}
	}
	if b.on[key] {
		b.mu.Unlock()
		return
	}
	b.on[key] = true
	b.mu.Unlock()
	go func() {
		defer func() {
			b.mu.Lock()
			delete(b.on, key)
			b.mu.Unlock()
		}()
		f()
	}()
}

func withBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, readBudget)
}

// freshMailbox is hlmailbox at the head: the status's reading when it is fresh, else read now.
func (s *server) freshMailbox(ctx context.Context) (agent.MailboxView, error) {
	s.mu.Lock()
	mb, at := s.mailbox, s.mailboxAt
	s.mu.Unlock()
	if mb != nil && s.now().Sub(at) < s.activityTTL {
		return *mb, nil
	}
	c, cancel := withBudget(ctx)
	defer cancel()
	v, err := agent.ReadMailbox(c, s.gno, s.cfg.Gno.Mailbox, 0)
	if err == nil {
		s.rememberMailbox(v)
	}
	return v, err
}

// msgJSON is one of hlmailbox's "msgs/<from>" entries.
type msgJSON struct {
	Nonce   uint32 `json:"nonce"`
	ID      string `json:"id"`
	Height  int64  `json:"height"`
	Message string `json:"message"`
}

// readDispatches reads hlmailbox's newest messages (one "msgs/" page) and each new send's record.
func (s *server) readDispatches(ctx context.Context, ch *ChainConfig, other Other, st *activityState, count uint32) error {
	from := uint32(0)
	if count > activityList {
		from = count - activityList
	}
	msgs, err := s.msgs(ctx, from)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		if _, ok := st.dispatch[m.Nonce]; ok {
			continue
		}
		d, err := s.dispatchOf(ctx, ch, other, st.times, m)
		if err != nil {
			return err
		}
		st.dispatch[m.Nonce] = d
	}
	if len(st.dispatch) > activityKeep {
		var ns []uint32
		for n := range st.dispatch {
			ns = append(ns, n)
		}
		sort.Slice(ns, func(i, j int) bool { return ns[i] > ns[j] })
		for _, n := range ns[activityKeep:] {
			delete(st.dispatch, n)
		}
	}
	st.count = count
	return nil
}

func (s *server) msgs(ctx context.Context, from uint32) ([]msgJSON, error) {
	c, cancel := withBudget(ctx)
	defer cancel()
	body, err := s.gno.RenderAt(c, s.cfg.Gno.Mailbox, "msgs/"+strconv.FormatUint(uint64(from), 10), 0)
	if err != nil {
		return nil, err
	}
	var msgs []msgJSON
	if err := json.Unmarshal([]byte(body), &msgs); err != nil {
		return nil, fmt.Errorf("msgs/%d: %w", from, err)
	}
	return msgs, nil
}

// dispatchOf reads one dispatched message: a route's send (its token, amount and recipient from the message, its
// sender and time from the route's record) or r/hlgov's decision (its kind and number, its block's time).
func (s *server) dispatchOf(ctx context.Context, ch *ChainConfig, other Other, times map[int64]int64, m msgJSON) (*gnoDispatch, error) {
	d := &gnoDispatch{Nonce: m.Nonce, ID: strings.ToLower(m.ID), Height: m.Height, Kind: "other"}
	raw, err := hex.DecodeString(m.Message)
	if err != nil {
		return d, nil
	}
	sum := sha256.Sum256(raw)
	d.Hash = hex.EncodeToString(sum[:])
	hm, err := hl.ParseMessage(raw)
	if err != nil {
		return d, nil
	}
	for _, t := range ch.Tokens {
		addr, err := hl.GnoAddressToBytes32(hl.GnoPackageAddress(t.Realm))
		if err != nil || addr != hm.Sender {
			continue
		}
		d.Kind, d.Token = "send", t.Symbol
		if rcpt, amt, _, err := hl.ParseTokenMessage(hm.Body); err == nil && amt <= 1<<62 {
			d.Amount, d.To = int64(amt), other.Address(rcpt)
		}
		sd, err := s.sendRecord(ctx, t.Realm, d.ID)
		if err != nil {
			return nil, err
		}
		if sd != nil {
			d.From, d.At = sd.From, sd.At
		}
	}
	if s.cfg.Gno.Gov != "" {
		if gov, err := hl.GnoAddressToBytes32(hl.GnoPackageAddress(s.cfg.Gno.Gov)); err == nil && gov == hm.Sender {
			d.Kind = "decision"
			if kind, seq, ok := gnolight.DecisionPrefix(hm.Body); ok {
				d.Decision = fmt.Sprintf("#%d %s", seq, sol.KindName(kind))
			}
		}
	}
	if d.At == 0 {
		d.At = s.blockTime(ctx, times, d.Height)
	}
	return d, nil
}

// sendRecord is a route's "send/<id>": who sent and when; nil when the route holds no such send.
func (s *server) sendRecord(ctx context.Context, realm, id string) (*struct {
	From string `json:"from"`
	At   int64  `json:"at"`
}, error) {
	c, cancel := withBudget(ctx)
	defer cancel()
	body, err := s.gno.RenderAt(c, realm, "send/"+id, 0)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(body) == "null" {
		return nil, nil
	}
	var sd struct {
		From string `json:"from"`
		At   int64  `json:"at"`
	}
	if err := json.Unmarshal([]byte(body), &sd); err != nil {
		return nil, fmt.Errorf("%s send/%s: %w", realm, id, err)
	}
	return &sd, nil
}

// blockTime is a block's time, read once (0 when it cannot be read: the event then carries no time).
func (s *server) blockTime(ctx context.Context, times map[int64]int64, height int64) int64 {
	if height <= 0 {
		return 0
	}
	if t, ok := times[height]; ok {
		return t
	}
	c, cancel := withBudget(ctx)
	defer cancel()
	h, err := s.gno.Block(c, height)
	if err != nil {
		return 0
	}
	if len(times) > 500 {
		for k := range times {
			delete(times, k)
		}
	}
	times[height] = h.Time.Unix()
	return times[height]
}

// claimJSON is a route's "claim/<id>".
type claimJSON struct {
	To     string `json:"to"`
	Amount int64  `json:"amount"`
	Paid   int64  `json:"paid"`
	State  string `json:"state"`
	At     int64  `json:"at"`
	Height int64  `json:"height"`
	Swept  int64  `json:"swept"`
}

func (s *server) claimRecord(ctx context.Context, realm, id string) (*claimJSON, error) {
	c, cancel := withBudget(ctx)
	defer cancel()
	body, err := s.gno.RenderAt(c, realm, "claim/"+id, 0)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(body) == "null" {
		return nil, nil
	}
	var cj claimJSON
	if err := json.Unmarshal([]byte(body), &cj); err != nil {
		return nil, fmt.Errorf("%s claim/%s: %w", realm, id, err)
	}
	return &cj, nil
}

// claimsRead bounds the claims one refresh reads: the newest burns' that are not final.
const claimsRead = 10

// readClaims reads the claims of the newest burns that are not final yet: credited or not, paid how far.
func (s *server) readClaims(ctx context.Context, ch *ChainConfig, st *activityState) {
	n := 0
	for _, b := range sortedBurns(st.other.Burns) {
		if n >= claimsRead {
			break
		}
		t := ch.Token(b.Token)
		if c := st.claims[b.ID]; t == nil || (c != nil && c.Final) {
			continue
		}
		n++
		cj, err := s.claimRecord(ctx, t.Realm, b.ID)
		if err != nil {
			st.errors = append(st.errors, b.Token+" claim: "+agent.Redact(err.Error()))
			continue
		}
		if cj == nil {
			continue // not credited yet
		}
		st.claims[b.ID] = foldClaim(st.claims[b.ID], b.Token, cj, s.now().Unix())
	}
	if len(st.claims) > 2*activityKeep {
		for id := range st.claims {
			if _, ok := st.other.Burns[id]; !ok {
				delete(st.claims, id)
			}
		}
	}
}

// foldClaim is a claim's new reading over its last (nil the first time); the paid part's growth is timed by this
// reader, since the chain records no payout time.
func foldClaim(last *claimRec, token string, cj *claimJSON, now int64) *claimRec {
	c := &claimRec{}
	if last != nil {
		*c = *last
		if cj.Paid > last.Paid {
			c.PaidAt = now
		}
	}
	c.Token, c.To, c.Amount, c.Paid, c.State, c.At, c.Height, c.Swept = token, cj.To, cj.Amount, cj.Paid, cj.State, cj.At, cj.Height, cj.Swept
	c.Final = (c.Paid >= c.Amount && c.Amount > 0) || c.Swept > 0 || c.State == "voided" || c.State == "withheld" || c.State == "forged"
	return c
}

func sortedBurns(m map[string]Burn) []Burn {
	out := make([]Burn, 0, len(m))
	for _, b := range m {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time > out[j].Time
		}
		return out[i].Nonce > out[j].Nonce
	})
	return out
}

// newestBurnNonce is the nonce the lamps ask about: the far side's newest dispatch toward gno.land, −1 when none.
func (st *activityState) newestBurnNonce() int64 {
	if st.other.Dispatched > 0 {
		return int64(st.other.Dispatched) - 1
	}
	return -1
}

// ------------------------------------------------------------------ the events --

// eventJSON is one thing that happened, as the page plays it.
type eventJSON struct {
	Kind    string      `json:"kind"` // send, header, mint, burn, credit, payout, decision, enact
	Key     string      `json:"key"`  // the same on every read: the page plays an event once
	Time    int64       `json:"time"`
	Exact   bool        `json:"exact"` // false: the nearest known time (a payout's is not recorded on chain)
	Token   string      `json:"token,omitempty"`
	Amount  int64       `json:"amount,omitempty"`
	ID      string      `json:"id,omitempty"`
	From    string      `json:"from,omitempty"`
	To      string      `json:"to,omitempty"`
	Height  int64       `json:"height,omitempty"`
	Slot    uint64      `json:"slot,omitempty"`
	Nonce   *int64      `json:"nonce,omitempty"`
	Sig     string      `json:"sig,omitempty"`
	Link    string      `json:"link,omitempty"`
	What    string      `json:"what,omitempty"`    // a decision's kind and number
	Proof   *ProofEvent `json:"proof,omitempty"`   // a mint's: what the relayer proved it under
	Signers []int       `json:"signers,omitempty"` // a burn's: the signers serving its signature, when asked
	Full    bool        `json:"full,omitempty"`    // a payout's: the claim is paid in full
}

func i64p(v int64) *int64 { return &v }

// events are a reading's events, newest first, at most activityKeep.
func (s *server) events(ch *ChainConfig, other Other, st *activityState, lamps []signerLamp) []eventJSON {
	var out []eventJSON
	for _, d := range st.dispatch {
		switch d.Kind {
		case "send":
			out = append(out, eventJSON{Kind: "send", Key: "send:" + d.ID, Time: d.At, Exact: true, Token: d.Token, Amount: d.Amount, ID: d.ID, From: d.From, To: d.To, Height: d.Height, Nonce: i64p(int64(d.Nonce)), Link: fmt.Sprintf(s.cfg.Gno.RealmLink, s.realmOf(ch, d.Token))})
		case "decision":
			out = append(out, eventJSON{Kind: "decision", Key: "decision:" + d.ID, Time: d.At, Exact: d.At > 0, ID: d.ID, Height: d.Height, Nonce: i64p(int64(d.Nonce)), What: d.Decision})
		}
	}
	for _, h := range st.other.Headers {
		out = append(out, eventJSON{Kind: "header", Key: "header:" + strconv.FormatInt(h.Height, 10), Time: h.Time, Exact: true, Height: h.Height, Slot: h.Slot, Sig: h.Sig, Link: other.TxLink(h.Sig)})
	}
	for _, dl := range st.other.Deliveries {
		e := eventJSON{Key: dl.Kind + ":" + dl.ID, Time: dl.Time, Exact: true, ID: dl.ID, Token: dl.Token, Amount: dl.Amount, To: dl.To, Slot: dl.Slot, Sig: dl.Sig, Link: dl.Link}
		switch dl.Kind {
		case "mint":
			e.Kind = "mint"
		case "decision":
			e.Kind, e.Key = "enact", "enact:"+dl.ID
		default:
			continue
		}
		if p, ok := st.other.Proofs[dl.Hash]; ok {
			p := p
			e.Proof = &p
		}
		out = append(out, e)
	}
	newest := st.newestBurnNonce()
	for _, b := range st.other.Burns {
		e := eventJSON{Kind: "burn", Key: "burn:" + b.ID, Time: b.Time, Exact: true, Token: b.Token, Amount: b.Amount, ID: b.ID, To: b.To, Slot: b.Slot, Nonce: i64p(int64(b.Nonce)), Sig: b.Sig, Link: other.TxLink(b.Sig)}
		if int64(b.Nonce) == newest {
			e.Signers = vouched(lamps)
		}
		out = append(out, e)
		c := st.claims[b.ID]
		if c == nil {
			continue
		}
		out = append(out, eventJSON{Kind: "credit", Key: "credit:" + b.ID, Time: c.At, Exact: true, Token: c.Token, Amount: c.Amount, ID: b.ID, To: c.To, Height: c.Height, Link: fmt.Sprintf(s.cfg.Gno.RealmLink, s.realmOf(ch, c.Token))})
		if c.Paid > 0 {
			at, exact := c.PaidAt, c.PaidAt > 0
			if !exact {
				at = c.At // paid before this reader first looked: no later than now, no earlier than the credit
			}
			out = append(out, eventJSON{Kind: "payout", Key: "payout:" + b.ID + ":" + strconv.FormatInt(c.Paid, 10), Time: at, Exact: exact, Token: c.Token, Amount: c.Paid, ID: b.ID, To: c.To, Full: c.Paid >= c.Amount})
		}
	}
	order := map[string]int{"payout": 7, "credit": 6, "burn": 5, "mint": 4, "enact": 4, "header": 3, "send": 2, "decision": 2}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time > out[j].Time
		}
		if order[out[i].Kind] != order[out[j].Kind] {
			return order[out[i].Kind] > order[out[j].Kind]
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > activityKeep {
		out = out[:activityKeep]
	}
	if out == nil {
		out = []eventJSON{}
	}
	return out
}

func (s *server) realmOf(ch *ChainConfig, symbol string) string {
	if t := ch.Token(symbol); t != nil {
		return t.Realm
	}
	return s.cfg.Gno.Mailbox
}

// ------------------------------------------------------------------ the walks --

// walkStep is one of a transfer's five steps as the page's walkthrough shows it.
//
//	out: lock (the send in its block) → final (the block committed) → header (a header at or past the block
//	     after it reached the far side's light client) → proof (the relayer staged the results proof) → mint
//	in:  burn → vouch (the signers' threshold serves its signature) → deliver (the relayer's Process on
//	     gno.land) → credit (the claim) → pay (in full, under the meters)
type walkStep struct {
	Key    string `json:"key"`
	Done   bool   `json:"done"`
	Time   int64  `json:"time"`  // 0 when not known
	Exact  bool   `json:"exact"` // the chain's record; false: the nearest known
	Height int64  `json:"height,omitempty"`
	Sig    string `json:"sig,omitempty"`
	Link   string `json:"link,omitempty"`
}

// outWalk is a send's five steps from what is known: its block (0 when it is older than the index), the headers
// the client accepted, its trusted height now, the proof staged and the delivery.
func outWalk(height, sentAt int64, headers []HeaderEvent, trusted uint64, proof *ProofEvent, dl *DeliveryEvent) []walkStep {
	lock := walkStep{Key: "lock", Done: true, Time: sentAt, Exact: sentAt > 0, Height: height}
	final := walkStep{Key: "final", Done: true, Time: sentAt, Height: height}
	header := walkStep{Key: "header"}
	need := height + 1 // the header whose LastResultsHash commits to the send's block
	if proof != nil && proof.Height > 0 {
		need = proof.Height
	}
	if height > 0 {
		for i := len(headers) - 1; i >= 0; i-- { // oldest first: the first header that reached the block
			if h := headers[i]; h.Height >= need && h.Time >= sentAt {
				header = walkStep{Key: "header", Done: true, Time: h.Time, Exact: true, Height: h.Height, Sig: h.Sig}
				break
			}
		}
		if !header.Done && int64(trusted) >= need {
			header = walkStep{Key: "header", Done: true, Height: int64(trusted)}
		}
	}
	pr := walkStep{Key: "proof"}
	if proof != nil {
		pr = walkStep{Key: "proof", Done: true, Time: proof.Time, Exact: true, Height: proof.Height, Sig: proof.Sig}
	}
	mint := walkStep{Key: "mint"}
	if dl != nil {
		mint = walkStep{Key: "mint", Done: true, Time: dl.Time, Exact: dl.Time > 0, Sig: dl.Sig, Link: dl.Link}
		if !pr.Done {
			pr = walkStep{Key: "proof", Done: true, Time: dl.Time}
		}
		if !header.Done {
			header = walkStep{Key: "header", Done: true} // a header reached it; which one is older than the index
			if height > 0 {
				header.Height = need
			}
		}
	}
	return []walkStep{lock, final, header, pr, mint}
}

// inWalk is a burn's five steps: the burn (nil when it is known by its id alone), the signers serving it, the
// threshold, the claim.
func inWalk(b *Burn, link string, signed []int, threshold int, c *claimRec) []walkStep {
	burn := walkStep{Key: "burn", Done: true}
	if b != nil {
		burn = walkStep{Key: "burn", Done: true, Time: b.Time, Exact: b.Time > 0, Sig: b.Sig, Link: link}
	}
	vouch := walkStep{Key: "vouch", Done: c != nil || (threshold > 0 && len(signed) >= threshold)}
	deliver, credit, pay := walkStep{Key: "deliver"}, walkStep{Key: "credit"}, walkStep{Key: "pay"}
	if c != nil {
		deliver = walkStep{Key: "deliver", Done: true, Time: c.At, Exact: c.At > 0, Height: c.Height}
		credit = walkStep{Key: "credit", Done: true, Time: c.At, Exact: c.At > 0, Height: c.Height}
		if c.Paid >= c.Amount && c.Amount > 0 {
			pay = walkStep{Key: "pay", Done: true, Time: c.PaidAt, Exact: c.PaidAt > 0}
		}
	}
	return []walkStep{burn, vouch, deliver, credit, pay}
}

// lastOut is the newest send that was minted, with its walk, for the replay — the newest send of all when none
// was; nil when there is none.
func (s *server) lastOut(ch *ChainConfig, st *activityState) *transferJSON {
	var d, done *gnoDispatch
	for _, x := range st.dispatch {
		if x.Kind != "send" {
			continue
		}
		if d == nil || x.Nonce > d.Nonce {
			d = x
		}
		if _, ok := st.other.Deliveries[x.ID]; ok && (done == nil || x.Nonce > done.Nonce) {
			done = x
		}
	}
	if done != nil {
		d = done
	}
	if d == nil {
		return nil
	}
	return s.outFacts(st, d.ID, d, nil)
}

// outFacts fills a send's transfer from a reading: its walk, its proof, the client's trusted height. known is the
// delivery as the tracker read it (its processed record), for a send older than the index's listing.
func (s *server) outFacts(st *activityState, id string, d *gnoDispatch, known *DeliveryEvent) *transferJSON {
	tr := &transferJSON{Ref: id, Kind: "message", Direction: "out", ID: id, State: "dispatched", Steps: []stepJSON{}, Nonce: -1, Trusted: int64(st.other.Trusted)}
	var height, at int64
	var proof *ProofEvent
	if d != nil {
		tr.Token, tr.From, tr.To, tr.Amount, tr.Height, tr.Nonce, tr.At = d.Token, d.From, d.To, d.Amount, d.Height, int64(d.Nonce), d.At
		height, at = d.Height, d.At
		if p, ok := st.other.Proofs[d.Hash]; ok {
			p := p
			proof = &p
		}
	}
	dl := known
	if x, ok := st.other.Deliveries[id]; ok {
		x := x
		dl = &x
	}
	if dl != nil {
		tr.State = "delivered"
		if tr.To == "" {
			tr.To = dl.To
		}
	}
	tr.Proof = proof
	tr.Walk = outWalk(height, at, st.other.Headers, st.other.Trusted, proof, dl)
	return tr
}

// findDispatch is a message id's dispatch: the reading's, else found in hlmailbox's pages before its listing — at
// most five pages back, each read once — nil when it is older still (its walk then has no block).
func (s *server) findDispatch(ctx context.Context, ch *ChainConfig, other Other, a *chainActivity, st *activityState, id string) *gnoDispatch {
	id = strings.ToLower(id)
	for _, d := range st.dispatch {
		if d.ID == id {
			return d
		}
	}
	a.olderMu.Lock()
	defer a.olderMu.Unlock()
	if d, ok := a.older[id]; ok {
		return d
	}
	times := map[int64]int64{}
	end := int64(st.count) - activityList
	for page := 0; page < 5 && end > 0; page++ {
		from := max(end-activityList, 0)
		if a.pagesRead[from] {
			end = from
			continue
		}
		msgs, err := s.msgs(ctx, uint32(from))
		if err != nil {
			return nil
		}
		a.pagesRead[from] = true
		for _, m := range msgs {
			if int64(m.Nonce) >= end {
				break
			}
			if strings.EqualFold(m.ID, id) {
				d, err := s.dispatchOf(ctx, ch, other, times, m)
				if err != nil {
					return nil
				}
				if len(a.older) > 200 {
					a.older = map[string]*gnoDispatch{}
				}
				a.older[id] = d
				return d
			}
		}
		end = from
	}
	return nil
}

// lastIn is the newest burn that was credited, with its walk, for the replay — the newest burn of all when none
// was; nil when there is none.
func (s *server) lastIn(ch *ChainConfig, other Other, st *activityState, lamps []signerLamp, threshold int) *transferJSON {
	burns := sortedBurns(st.other.Burns)
	if len(burns) == 0 {
		return nil
	}
	b := burns[0]
	for _, x := range burns {
		if st.claims[x.ID] != nil {
			b = x
			break
		}
	}
	signed := []int(nil)
	if int64(b.Nonce) == st.newestBurnNonce() {
		signed = vouched(lamps)
	}
	return s.inFacts(other, &b, st.claims[b.ID], signed, threshold, len(ch.Signers))
}

// inFacts fills a burn's transfer from what is known of it.
func (s *server) inFacts(other Other, b *Burn, c *claimRec, signed []int, threshold, signers int) *transferJSON {
	tr := &transferJSON{Ref: b.ID, Kind: "message", Direction: "in", Token: b.Token, ID: b.ID, To: b.To, Amount: b.Amount, State: "burned", Steps: []stepJSON{},
		Nonce: int64(b.Nonce), At: b.Time, Vouched: signed, Threshold: threshold, Signers: signers}
	if b.Sig != "" {
		tr.Ref = b.Sig
	}
	if c != nil {
		tr.To, tr.Amount, tr.Paid, tr.Height = c.To, c.Amount, c.Paid, c.Height
		tr.State = "credited"
		switch {
		case c.Swept > 0:
			tr.State = "swept"
		case c.State == "voided" || c.State == "withheld" || c.State == "forged":
			tr.State = "voided"
		case c.Paid >= c.Amount && c.Amount > 0:
			tr.State = "paid"
		case c.Paid > 0:
			tr.State = "paying"
		}
	}
	tr.Walk = inWalk(b, other.TxLink(b.Sig), signed, threshold, c)
	return tr
}

// ------------------------------------------------------------------ the endpoint --

// activityJSON is /api/<chain>/activity.
type activityJSON struct {
	Chain     string          `json:"chain"`
	At        int64           `json:"at"`
	Status    json.RawMessage `json:"status"` // as /status serves it; its last good reading while a read fails (Stale)
	Stale     bool            `json:"stale"`
	Signers   []signerLamp    `json:"signers"`
	Threshold int             `json:"threshold"` // how many signers must vouch
	Burns     uint64          `json:"burns"`     // the burns toward gno.land the far side's mailbox counts
	Processed uint64          `json:"processed"` // the messages it processed
	Trusted   uint64          `json:"trusted"`   // the light client's trusted height at the index's reading
	Events    []eventJSON     `json:"events"`
	Last      lastJSON        `json:"last"`
	Errors    []string        `json:"errors"`
}

type lastJSON struct {
	Out *transferJSON `json:"out"`
	In  *transferJSON `json:"in"`
}

func (s *server) handleActivity(w http.ResponseWriter, r *http.Request) {
	ch, other, ok := s.chainOf(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	out := activityJSON{Chain: ch.ID, At: s.now().Unix(), Errors: []string{}}
	if status, stale, failure := s.statusNow(ctx, ch, other); status != nil {
		out.Status, out.Stale = status, stale
		if failure != "" {
			out.Errors = append(out.Errors, failure)
		}
	} else {
		out.Errors = append(out.Errors, failure)
	}
	st := s.activityNow(ctx, ch, other)
	threshold := thresholdOf(s.lastRule())
	lamps := s.lampsNow(ctx, ch, st.newestBurnNonce())
	out.Signers, out.Threshold = lamps, threshold
	out.Burns, out.Processed, out.Trusted = st.other.Dispatched, st.other.Processed, st.other.Trusted
	out.Events = s.events(ch, other, st, lamps)
	out.Last = lastJSON{Out: s.lastOut(ch, st), In: s.lastIn(ch, other, st, lamps, threshold)}
	out.Errors = append(out.Errors, st.errors...)
	body, _ := json.Marshal(out)
	writeJSON(w, 200, body)
}

// lastRule is the signer rule hlmailbox last rendered ("2 of 3 signers"), "" before the first read.
func (s *server) lastRule() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mailbox == nil {
		return ""
	}
	return s.mailbox.Rule
}

// thresholdOf reads a rule's threshold: "2 of 3 signers" is 2; 0 when the rule says no number.
func thresholdOf(rule string) int {
	n, err := strconv.Atoi(strings.Fields(rule + " x")[0])
	if err != nil {
		return 0
	}
	return n
}

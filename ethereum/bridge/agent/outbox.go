package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The gno.land → Ethereum backlog, as the updater and the relayer both read it (eth/bridge/PLAN.md §5.7, §6.1,
// §6.2). Each walks hlmailbox's messages from the first one not ended, sorts ours — a route's token message from a
// realm of the token's line to its HypERC20, r/hlgov's decision to HlAdmin (or a retired HlAdmin of its line) — from
// the rest, reads Ethereum's delivered marks, and for each undelivered message of ours its usable events (the
// heights of its Dispatch events, the dispatch's and every Reemit's, in blocks whose leaves rebuild and with a leaf
// the relayer proves from) and the newest one whose next header the client accepted at its current epoch: a proof
// is always under an accepted header (§6.2). A message with no such header waits; the waiting rule (planWaits)
// says what one update can serve and when the relayer re-emits them all in one transaction instead.

// msgKind is what a hlmailbox message is to the gno → Ethereum machines.
type msgKind int

const (
	kindOther msgKind = iota
	kindRoute
	kindDecision
	// kindOtherAdmin is r/hlgov's decision to an address HlAdmin's line does not name (G31): held and alarmed,
	// never walked past — a successor the chain names later is delivered then.
	kindOtherAdmin
	// kindLineUnread is a route message from anyone but the configured realm while the token's line of migrations
	// could not be read this step (G46): held, never walked past as "other".
	kindLineUnread
)

// successorHops bounds HlAdmin's line followed from the configured address.
const successorHops = 4

// adminLine is HlAdmin's line of succession (§6.5, AQ6): the configured address and every successor its state
// names; the last is the live one, the earlier ones retired (they record nothing more).
type adminLine struct {
	Live    common.Address
	State   *ethx.AdminState // the live one's
	Retired []common.Address
}

// followAdmins reads HlAdmin from the configured address and follows `successor` as far as it goes.
func followAdmins(ctx context.Context, r ethx.Reader, start common.Address, at ethx.At) (*adminLine, error) {
	line := &adminLine{Live: start}
	for hop := 0; ; hop++ {
		st, err := ethx.ReadAdmin(ctx, r, line.Live, at)
		if err != nil {
			if hop == 0 {
				return nil, err
			}
			return nil, fmt.Errorf("the successor %s (hop %d from %s): %w", line.Live.Hex(), hop, start.Hex(), err)
		}
		line.State = st
		if !st.Retired() {
			return line, nil
		}
		if hop >= successorHops {
			return nil, fmt.Errorf("HlAdmin's line of succession from %s runs past %d hops at %s → %s", start.Hex(), successorHops, line.Live.Hex(), st.Successor.Hex())
		}
		next := st.Successor
		if next == line.Live {
			return nil, fmt.Errorf("HlAdmin %s names itself as its successor", next.Hex())
		}
		for _, r := range line.Retired {
			if r == next {
				return nil, fmt.Errorf("HlAdmin's line of succession from %s loops at %s", start.Hex(), next.Hex())
			}
		}
		line.Retired = append(line.Retired, line.Live)
		line.Live = next
	}
}

func (l *adminLine) IsLive(a common.Address) bool { return l != nil && l.Live == a }

func (l *adminLine) IsRetired(a common.Address) bool {
	if l == nil {
		return false
	}
	for _, r := range l.Retired {
		if r == a {
			return true
		}
	}
	return false
}

func (l *adminLine) Knows(a common.Address) bool { return l.IsLive(a) || l.IsRetired(a) }

// Text names a moved line ("" while the configured HlAdmin is the live one).
func (l *adminLine) Text() string {
	if l == nil || len(l.Retired) == 0 {
		return ""
	}
	var parts []string
	for _, r := range l.Retired {
		parts = append(parts, r.Hex())
	}
	return "HlAdmin's line moved: " + strings.Join(parts, " → ") + " → " + l.Live.Hex() + " (live)"
}

// outState is one machine's walk of the backlog, kept in its state file.
type outState struct {
	Next     uint32             `json:"next"` // the first nonce not ended
	Done     map[string]string  `json:"done"` // nonce → how it ended: delivered, recorded, taken, other, retired, refunded
	Index    gnoside.EventIndex `json:"index"`
	Unusable map[string]string  `json:"unusable"` // "nonce@height" → why that event proves nothing (a leaf too big, no event of it)
	// Books is, by header hash (hex), who recorded the header and which route sends the updater's key booked under it,
	// read once from the chain (bookingOf) and kept here — never only in memory, never only within the log look-back
	// (E4-CF2) — until no message that can still wait has an event below it
	Books map[string]*headerBook `json:"books,omitempty"`
	// Seen is the Ethereum block this machine's last pass judged the proven sends at: a pass after a gap longer than the
	// log look-back (the machine down, a state file new or restored) looks for a header's recording past it
	Seen uint64 `json:"seen,omitempty"`
	// Headers are the heights of gno headers this machine saw recorded on the client at its epoch HeadersEpoch — the
	// trusted heights it read, the bookings it read, the headers the compaction found recorded at the finalized block
	// (E5-GS4): a waiting message's events whose next header is one of them are read every step, beside its dispatch and
	// its newest few; every other event is judged once, at the finalized block (compact)
	Headers      []int64 `json:"headers,omitempty"`
	HeadersEpoch uint64  `json:"headersEpoch,omitempty"`
}

// headersKept bounds outState.Headers (the newest kept): far more than the headers a waiting message's events lie under.
const headersKept = 512

// noteHeader remembers a header height seen recorded at an epoch: a newer epoch forgets the old one's (their headers
// prove nothing now), an older one's is not kept (the finalized block can lag an epoch change).
func (s *outState) noteHeader(h int64, epoch uint64) {
	if h <= 0 || epoch < s.HeadersEpoch {
		return
	}
	if epoch > s.HeadersEpoch {
		s.Headers, s.HeadersEpoch = nil, epoch
	}
	i := sort.Search(len(s.Headers), func(i int) bool { return s.Headers[i] >= h })
	if i < len(s.Headers) && s.Headers[i] == h {
		return
	}
	s.Headers = append(s.Headers, 0)
	copy(s.Headers[i+1:], s.Headers[i:])
	s.Headers[i] = h
	if len(s.Headers) > headersKept {
		s.Headers = append([]int64(nil), s.Headers[len(s.Headers)-headersKept:]...)
	}
}

// pruneHeaders forgets the header heights at or below a height (a header proves the block before it: none of them
// proves a waiting message's event once the oldest waiting dispatch lies above them).
func (s *outState) pruneHeaders(atOrBelow int64) {
	i := sort.Search(len(s.Headers), func(i int) bool { return s.Headers[i] > atOrBelow })
	if i > 0 {
		s.Headers = append([]int64(nil), s.Headers[i:]...)
	}
}

// headerBook is a recorded header's booking as its recording transaction says it: the trailer of eth.updater's
// transaction (Ours), nothing for anyone else's.
type headerBook struct {
	Height int64    `json:"height"`
	Epoch  uint64   `json:"epoch"`
	Ours   bool     `json:"ours"`             // recorded by a transaction of eth.updater's key
	Nonces []uint32 `json:"nonces,omitempty"` // the route sends its trailer booked under it
	// Prov is a booking read from a log above the finalized block (E5-GS3): a reorganisation may still move the
	// recording to another transaction, so it is read again every unreadAgain — the log from From on, or first the
	// receipt of Tx, its recording transaction — until the finalized block passes Block, its log's, and is then kept
	// for good; until then each machine judges by the chain's latest word, and both by its final one after
	Prov  bool   `json:"prov,omitempty"`
	Block uint64 `json:"block,omitempty"`
	From  uint64 `json:"from,omitempty"`
	Tx    string `json:"tx,omitempty"`
}

// names says whether the booking names a send.
func (b *headerBook) names(n uint32) bool {
	if b == nil || !b.Ours {
		return false
	}
	for _, x := range b.Nonces {
		if x == n {
			return true
		}
	}
	return false
}

func (s *outState) init() {
	if s.Done == nil {
		s.Done = map[string]string{}
	}
	if s.Unusable == nil {
		s.Unusable = map[string]string{}
	}
	if s.Books == nil {
		s.Books = map[string]*headerBook{}
	}
}

func eventKey(n uint32, h int64) string {
	return strconv.FormatUint(uint64(n), 10) + "@" + strconv.FormatInt(h, 10)
}

// end records how a message ended and forgets its events.
func (s *outState) end(n uint32, how string) {
	s.init()
	s.Done[strconv.FormatUint(uint64(n), 10)] = how
	s.Index.Forget(n)
	for k := range s.Unusable {
		if strings.HasPrefix(k, strconv.FormatUint(uint64(n), 10)+"@") {
			delete(s.Unusable, k)
		}
	}
}

// prune keeps Done to the nonces at or past Next (G8: the file never grows by every nonce ever).
func (s *outState) prune() {
	for nk := range s.Done {
		n, err := strconv.ParseUint(nk, 10, 32)
		if err != nil || uint32(n) < s.Next {
			delete(s.Done, nk)
		}
	}
}

// outMsg is one undelivered message of ours.
type outMsg struct {
	n         uint32
	msg       *gnoside.Msg
	kind      msgKind
	rt        *Route
	seq       uint64 // a decision's number and kind
	dk        byte
	events    []int64 // usable event heights, ascending
	proofH    int64   // the usable event whose next header proves now, its leaf the cheapest by the model (then the newest); 0 for none
	proofHash [32]byte
	// proven are the usable events whose next header the client accepted at its epoch, with that header's hash (a
	// booked send is proven from the cheapest of them its bookings name: bookedFor)
	proven []provenEvent
	// counts and bytes are the Dispatch events and the size of the leaf each usable event would be proven from, as the
	// scan read them (a height the scan has not measured is absent)
	counts map[int64]int
	bytes  map[int64]int
	// newestHash is the header after the newest usable event (latest()), the one the updater buys for the group the
	// message waits in; newestProven says the client accepted it at its epoch, newestCurrentSet that it is signed by the
	// client's current set (proveHeader takes it while it lies at or below the trusted height: E3-RT4)
	newestHash       [32]byte
	newestProven     bool
	newestCurrentSet bool
	// spoiled counts a message's events past its dispatch in blocks nothing can be proven from (re-emits that landed
	// beside a stranger's transaction the leaves cannot be rebuilt for): its re-emits back off, and a header proof of
	// its newest usable event serves it instead of another re-emit (provesOld)
	spoiled  int
	past     bool // a decision copy whose number HlAdmin recorded already: it waits for no header
	settling bool // delivered at latest, not yet at the finalized block: neither sent again nor waiting
	// early is a decision copy HlAdmin cannot take yet (an earlier number is not recorded at the finalized block), dup a
	// copy whose number another copy is proven or delivered for: neither buys a header (E3-GS6)
	early bool
	dup   bool
	// booked is a route send a header the updater's key booked it under proves (bookedFor: delivered from that leaf at
	// no fee check); held one proven only under headers that book nothing for it (a stranger's, a decision's, or the
	// updater's bought for others) whose own fee does not cover its delivery: it waits as an unproven send does — for
	// its group's header, the six-hour rule within the budget and delivery:age (E3-GS2, E3-GS7, E4-GS2)
	booked bool
	held   bool
	// fee is a route send's own fee, as its SentTransferRemote event beside its Dispatch records it (feeKnown); a send
	// whose event the scan never read is judged at its route's fee now (E-M3's Q1)
	fee      int64
	feeKnown bool
	// feeSpent is a route send the updater's ledger booked already (its fee credited and its delivery debited once):
	// waiting again — its booked header no longer proves at the client's epoch — it adds neither to a header's due rule
	// (E4-GS4)
	feeSpent bool
	// reads are the events the walk read this step (E5-GS4): the compaction never drops one
	reads []int64
}

// provenEvent is a usable event whose next header the client accepted at its epoch.
type provenEvent struct {
	h    int64
	hash [32]byte
}

func (t *outMsg) nk() string { return strconv.FormatUint(uint64(t.n), 10) }

// latest is the newest usable event height, 0 for none.
func (t *outMsg) latest() int64 {
	if len(t.events) == 0 {
		return 0
	}
	return t.events[len(t.events)-1]
}

// outEnv is what the walk reads, with the step's caches.
type outEnv struct {
	cfg    *Config
	gno    gnoside.Reader
	light  gnoside.Light
	blocks gnoside.Blocks
	eth    ethChain
	alarm  *Alarmer

	// per step
	routes   map[string]*gnoside.Route
	mailbox  *gnoside.Mailbox
	lines    map[string]*gnoside.Migration
	lineErrs map[string]error
	admins   *adminLine
	gate     *ethx.GateState
	tip      *big.Int // the tip a call would pay, read once a step (the due rule's cost)
	setGas   map[int64]uint64
	// for good: a gno block's hash, its ValidatorsHash, its LastResultsHash and its time never change
	hashes map[int64][32]byte
	vals   map[int64][32]byte
	lrh    map[int64][32]byte
	times  map[int64]time.Time
	// st is the machine's own walk (its state file's): the bookings read from the chain are kept there (bookingOf)
	st *outState
	// own is the updater's own booking of a header it sent, while the chain's word on it is not read yet (its pending
	// booking: a provider whose log index lags must not make the updater serve again the sends it booked); nil for the
	// relayer, which delivers at no fee check only on the chain's word
	own func(hash [32]byte, epoch uint64) *headerBook
	// unread is when a header's recording transaction was last looked for and not found (a provider whose log index
	// lags its latest answers empty for a moment, which must not turn the updater's own header into a stranger's:
	// E3-GS7): asked again after unreadAgain; wide when the look-back past the last three days was last made
	unread map[[32]byte]time.Time
	wide   map[[32]byte]time.Time
	// seen is st.Seen as the step began (the gap the look past the last three days is made for)
	seen uint64
	// fin is the finalized block, read once a step when asked (finalized); finRead says it was asked this step
	fin     *ethx.Block
	finRead bool
	// final are the client's entries recorded at the finalized block, by header hash (E5-GS4): an entry recorded at an
	// epoch never changes and no reorganisation reaches below the finalized block, so each is read once, for good
	final map[[32]byte]ethx.Entry
	// provRead is when a provisional booking (headerBook.Prov) was last read again, by header hash
	provRead map[[32]byte]time.Time
	// pending are the headers the updater's key has sent and not yet settled (its pending bookings: their events are read
	// every step, E5-GS4); nil for the relayer
	pending func() []int64
	// walkFail is when the walk began failing (0: it went the last step); probeAt when the log probe is due next
	walkFail time.Time
	probeAt  time.Time
	// spacing is the chain's seconds a block (blockSpacing), read once a step
	spacing     float64
	spacingRead bool
	// unproven is when the compaction first judged an event unrecorded, by "nonce@height" (dropped when judged so again
	// compactAgain later)
	unproven map[string]time.Time
	// logsFailFrom is the first block of the earliest log range that failed to read (0: none): eth:logs clears only
	// on a read reaching as far back
	logsFailFrom uint64
	logsFailed   bool
	// gone is when a provisional booking's recording was first found gone after its block was final (deleted when
	// found so again a minute on: one lagging answer is no verdict)
	gone map[[32]byte]time.Time
}

// The walk's reads (E5-GS4): anyone may Reemit a waiting message once a block, each one more event, and every event was
// read every step — its next header from the gno node, its client entry from Ethereum in one batch — until a stranger's
// ≈8 GNOT an hour put a message past a provider's batch cap and every walk failed, decisions and all. The walk reads a
// waiting message's dispatch, its newest readNewest usable events and those whose next header this machine saw recorded
// (outState.Headers, the updater's pending headers); the compaction judges compactPerStep of the others a step at the
// finalized block, keeping the recorded and dropping the rest from the index.
const (
	readNewest     = 4
	compactPerStep = 64
	// recordSlack is the 12-second slots a header's recording is looked for before its time says (an hour: the two
	// chains' clocks)
	recordSlack = 300
	// reorgSpan is how far below a log's block a reorganisation may move it when no finalized block is known
	reorgSpan = 256
	// walkAlarmAfter is how long the walk fails before outbox:walk alarms (every gno.land → Ethereum delivery waits on it)
	walkAlarmAfter = 2 * time.Minute
	// probeEvery is how often the machines read their provider's last three days of the client's logs (probeLogs)
	probeEvery = 6 * time.Hour
)

// unreadAgain is how long a header whose recording transaction was not found stands before its log is asked for
// again; wideAgain how long before the look past the last three days is made again; wideLogBack how far that look
// goes when the config names no eth.deployBlock (thirty days of 12-second blocks).
const (
	unreadAgain = time.Minute
	wideAgain   = time.Hour
	wideLogBack = 216_000
)

func (e *outEnv) reset() {
	e.routes = map[string]*gnoside.Route{}
	e.mailbox = nil
	e.gate = nil
	e.tip = nil
	e.setGas = map[int64]uint64{}
	e.lines, e.lineErrs = nil, nil
	if e.hashes == nil || e.vals == nil || e.lrh == nil || len(e.hashes) > 4096 {
		e.hashes, e.vals, e.lrh = map[int64][32]byte{}, map[int64][32]byte{}, map[int64][32]byte{}
	}
	if e.times == nil || len(e.times) > 4096 {
		e.times = map[int64]time.Time{}
	}
	if e.unread == nil || len(e.unread) > 1024 {
		e.unread = map[[32]byte]time.Time{}
	}
	if e.wide == nil || len(e.wide) > 1024 {
		e.wide = map[[32]byte]time.Time{}
	}
	if e.final == nil || len(e.final) > 4096 {
		e.final = map[[32]byte]ethx.Entry{}
	}
	if e.provRead == nil || len(e.provRead) > 1024 {
		e.provRead = map[[32]byte]time.Time{}
	}
	e.fin, e.finRead = nil, false
	e.spacing, e.spacingRead = 0, false
	if e.st != nil {
		e.seen = e.st.Seen
	}
}

// finalized is the finalized block, read once a step; nil when the provider answers none.
func (e *outEnv) finalized(ctx context.Context) *ethx.Block {
	if !e.finRead {
		e.finRead = true
		if b, err := e.eth.BlockAt(ctx, ethx.Finalized); err == nil {
			e.fin = b
		}
	}
	return e.fin
}

// walked records the walk's result (E5-GS4): every delivery, decision and header of gno.land → Ethereum waits on it, so a
// walk failing walkAlarmAfter alarms outbox:walk — once a log line, while a stranger's re-emits held it for good — until
// one goes.
func (e *outEnv) walked(now time.Time, err error) {
	if err == nil {
		e.walkFail = time.Time{}
		e.alarm.Clear("outbox:walk")
		return
	}
	if e.walkFail.IsZero() {
		e.walkFail = now
	}
	if now.Sub(e.walkFail) >= walkAlarmAfter {
		e.alarm.Alarm("outbox:walk", fmt.Sprintf("the walk of gno.land's messages to Ethereum has failed for %s (%s): no delivery, decision or header goes to Ethereum until it reads", now.Sub(e.walkFail).Round(time.Second), ethx.Redact(err.Error())))
	}
}

// knownHeaders are the header heights whose events the walk reads every step (E5-GS4): those seen recorded at the
// client's epoch — the bookings kept among them (a state file from before the list carries its bookings alone) — and the
// updater's own pending ones.
func (e *outEnv) knownHeaders(st *outState, cs *ethx.ClientState) map[int64]bool {
	out := map[int64]bool{}
	if cs != nil && st.HeadersEpoch == cs.Epoch {
		for _, h := range st.Headers {
			out[h] = true
		}
	}
	for _, b := range st.Books {
		if b != nil && (cs == nil || b.Epoch == cs.Epoch) {
			out[b.Height] = true
		}
	}
	if e.pending != nil {
		for _, h := range e.pending() {
			out[h] = true
		}
	}
	return out
}

// eventsText is a message's usable events for a note: the first, the newest few and how many (a stranger's re-emits
// may number thousands).
func eventsText(hs []int64) string {
	if len(hs) <= readNewest+1 {
		return fmt.Sprint(hs)
	}
	return fmt.Sprintf("[%d … %s] (%d events)", hs[0], strings.Trim(fmt.Sprint(hs[len(hs)-readNewest:]), "[]"), len(hs))
}

// sawAt records the Ethereum block a pass judged the proven sends at (outState.Seen).
func (e *outEnv) sawAt(b *ethx.Block) {
	if e.st != nil && b != nil && b.Number > e.st.Seen {
		e.st.Seen = b.Number
	}
}

// wideFrom is where the look for a header's recording past the last three days starts, and whether it is made at all:
// only when this machine's last pass lies further back than them — the machine was down, or its state file is new or
// restored, so a header it never judged may have been recorded before them (E4-CF2) — back to that pass less three
// days, never before eth.deployBlock, and at most wideLogBack when the config names none. A machine that ran through
// never makes it: a log its provider's index lags on is asked for again in a minute, never searched for across the
// chain's history.
func (e *outEnv) wideFrom(latest uint64) (uint64, bool) {
	if e.seen != 0 && latest <= e.seen+headerLogBack {
		return 0, false
	}
	lo := e.cfg.Eth.DeployBlock
	if e.seen > headerLogBack && e.seen-headerLogBack > lo {
		lo = e.seen - headerLogBack
	}
	if e.cfg.Eth.DeployBlock == 0 && latest > wideLogBack && latest-wideLogBack > lo {
		lo = latest - wideLogBack
	}
	return lo, true
}

// blockTime is gno block h's time from the own node (cached for good); false when the node does not answer it.
func (e *outEnv) blockTime(ctx context.Context, h int64) (time.Time, bool) {
	if h <= 0 {
		return time.Time{}, false
	}
	if t, ok := e.times[h]; ok {
		return t, true
	}
	blk, err := e.light.BlockAt(ctx, h)
	if err != nil || blk.Header == nil {
		return time.Time{}, false
	}
	t := time.Unix(blk.Header.Time.Secs, int64(blk.Header.Time.Nanos)).UTC()
	if e.times == nil {
		e.times = map[int64]time.Time{}
	}
	e.times[h] = t
	return t, true
}

// headerGasAt is the model's gas for the header the client takes next, signed by the set at height h (the config's
// eth.updateGas when the operator set one): read once a step.
func (e *outEnv) headerGasAt(ctx context.Context, h int64) uint64 {
	if e.cfg.Eth.UpdateGas > 0 {
		return e.cfg.Eth.UpdateGas
	}
	if g, ok := e.setGas[h]; ok {
		return g
	}
	g := headerGas(6, 5)
	if set, err := e.light.ValidatorsAt(ctx, h); err == nil {
		g = headerGasOfSet(set)
	}
	if e.setGas == nil {
		e.setGas = map[int64]uint64{}
	}
	e.setGas[h] = g
	return g
}

// leaf is the Dispatch events and the bytes of the leaf a message's event at h would be proven from (one event and
// no bytes past it when the scan has not measured it: the relayer measures the leaf it builds before it delivers).
func (t *outMsg) leaf(h int64) (n, bytes int) {
	n = 1
	if c, ok := t.counts[h]; ok && c > 0 {
		n = c
	}
	return n, t.bytes[h]
}

// leafGas is the model's gas for a delivery from the leaf of the event at h (budget.go), the maximum for one the scan
// has not measured.
func (t *outMsg) leafGas(h int64) uint64 {
	if _, ok := t.counts[h]; !ok {
		return math.MaxUint64
	}
	return deliveryGas(t.leaf(h))
}

// bookingOf is a recorded header's booking (E4-GS2, E4-GS4, E4-CF2): the client's HeaderRecorded log of the header at
// the epoch asked (a header is recorded once an epoch: update refuses a height at or below the trusted one, proveHeader
// one recorded at the epoch already), the transaction behind it — its sender recovered from its signature, its calldata
// carrying this header (E5-RT3: never one provider's "from" field) — and, when eth.updater's key sent it, the route
// sends its trailer names (bookingTrailer). The booking is kept in the machine's state file, so a restart, or a delivery
// days later (a long pause, the quarantine), reads the same answer: for good once its log lies at or below the
// finalized block, provisional above it — read again every unreadAgain (reread) until the finalized block passes it, so
// a reorganisation that moves the recording to a stranger's transaction moves both machines' answer with it (E5-GS3:
// each once kept its first reading for good, and the two could split for good). height is the header's (0: unknown):
// no header is recorded before it was made, so its log is looked for
// from its time on (an hour of slots before it), and over the last three days at most — past them, after a gap in this
// machine's passes longer than them (wideFrom), at most hourly. nil while no log is found (a provider whose log index
// lags, a failed read — alarmed eth:logs, E5-GS2): no booking for now — the send is held to its own fee, never spent
// unbooked — asked again after unreadAgain, never a "not ours" kept for good.
func (e *outEnv) bookingOf(ctx context.Context, now time.Time, hash [32]byte, epoch uint64, height int64) *headerBook {
	key := hex.EncodeToString(hash[:])
	if e.st != nil {
		e.st.init()
		if b := e.st.Books[key]; b != nil && b.Epoch == epoch {
			if !b.Prov {
				return b
			}
			return e.reread(ctx, now, key, hash, b)
		}
	}
	if e.unread == nil {
		e.unread = map[[32]byte]time.Time{}
	}
	if e.wide == nil {
		e.wide = map[[32]byte]time.Time{}
	}
	if at, ok := e.unread[hash]; ok && now.Sub(at) < unreadAgain {
		return nil
	}
	miss := func() *headerBook {
		e.unread[hash] = now
		return nil
	}
	latest, err := e.eth.BlockAt(ctx, ethx.Latest)
	if err != nil {
		return miss()
	}
	from := uint64(0)
	if latest.Number > headerLogBack {
		from = latest.Number - headerLogBack
	}
	wide := true
	if start := e.recordableFrom(ctx, latest, height); start > from {
		from, wide = start, false // made since: nothing before its time can record it
	}
	b, at, err := e.lookBooking(ctx, hash, epoch, from, latest.Number)
	if err != nil {
		return miss()
	}
	if b == nil && !wide {
		// nothing since the header's time says: the plain three days, at most hourly (a block spacing or a clock past what
		// the window allows for)
		if t, ok := e.wide[hash]; !ok || now.Sub(t) >= wideAgain {
			e.wide[hash] = now
			plain := uint64(0)
			if latest.Number > headerLogBack {
				plain = latest.Number - headerLogBack
			}
			if plain < from {
				if b, at, err = e.lookBooking(ctx, hash, epoch, plain, from-1); err != nil {
					return miss()
				}
			}
		}
	}
	if b == nil && wide {
		if lo, ok := e.wideFrom(latest.Number); ok && from > lo {
			if t, ok := e.wide[hash]; !ok || now.Sub(t) >= wideAgain {
				e.wide[hash] = now
				if b, at, err = e.lookBooking(ctx, hash, epoch, lo, from-1); err != nil {
					return miss()
				}
			}
		}
	}
	if b == nil {
		return miss()
	}
	delete(e.unread, hash)
	e.keepBooking(ctx, now, key, hash, b, at, 0)
	return b
}

// keepBooking keeps a booking read from a log in block at in the state file — for good when the finalized block has
// reached it, else provisional (E5-GS3): read again from the first block after the finalized one (from, when the
// caller knows it) every unreadAgain until the finalized block passes it.
func (e *outEnv) keepBooking(ctx context.Context, now time.Time, key string, hash [32]byte, b *headerBook, at, from uint64) {
	if e.st == nil {
		return
	}
	b.Prov, b.Block, b.From = false, at, 0
	if fin := e.finalized(ctx); fin == nil || at > fin.Number {
		b.Prov = true
		switch {
		case from > 0:
			b.From = from
		case fin != nil:
			b.From = fin.Number + 1
		case at > reorgSpan:
			b.From = at - reorgSpan
		}
		if e.provRead == nil {
			e.provRead = map[[32]byte]time.Time{}
		}
		e.provRead[hash] = now
	}
	e.st.Books[key] = b
	e.st.noteHeader(b.Height, b.Epoch)
}

// reread is a provisional booking read again (E5-GS3), at most every unreadAgain: the log from the first block a
// reorganisation could have changed — found, it is the chain's word now (another transaction's when a reorganisation
// moved the recording), kept for good once the finalized block passes it. Not found, or a read failing, it stands as it
// was while its block lies above the finalized one — a provider's backend a block behind answers no log and no receipt,
// and one such answer once deleted the updater's own view of its header, which then bought it again (the review of the
// lane, R3). Once the finalized block has passed it, the receipt of its recording transaction decides — still there,
// successful, its log recording the header, the recording stands for good (a provider that serves no log: E5-GS2); gone,
// failed or recording nothing on two reads a minute apart, the booking goes (nil: none until a log says whose it is).
func (e *outEnv) reread(ctx context.Context, now time.Time, key string, hash [32]byte, b *headerBook) *headerBook {
	if e.provRead == nil {
		e.provRead = map[[32]byte]time.Time{}
	}
	if at, ok := e.provRead[hash]; ok && now.Sub(at) < unreadAgain {
		return b
	}
	e.provRead[hash] = now
	if latest, err := e.eth.BlockAt(ctx, ethx.Latest); err == nil {
		if nb, at, err := e.lookBooking(ctx, hash, b.Epoch, b.From, latest.Number); err == nil && nb != nil {
			delete(e.gone, hash)
			e.keepBooking(ctx, now, key, hash, nb, at, b.From)
			return e.st.Books[key]
		}
	}
	fin := e.finalized(ctx)
	if fin == nil || fin.Number < b.Block || b.Tx == "" {
		return b // no verdict on a miss above the finalized block
	}
	rc, err := e.eth.Receipt(ctx, common.HexToHash(b.Tx))
	gone := ethx.IsNotFound(err)
	if err == nil && rc.BlockNumber != nil {
		if _, ok := recordingOf(rc, Address(e.cfg.Eth.Client), hash); ok {
			delete(e.gone, hash)
			if rc.BlockNumber.Uint64() <= fin.Number {
				b.Prov, b.Block, b.From = false, rc.BlockNumber.Uint64(), 0
			}
			return b
		}
		gone = true // failed, or recording nothing
	}
	if !gone {
		return b
	}
	if e.gone == nil {
		e.gone = map[[32]byte]time.Time{}
	}
	if first, ok := e.gone[hash]; !ok || now.Sub(first) < unreadAgain {
		if !ok {
			e.gone[hash] = now
		}
		return b
	}
	delete(e.gone, hash)
	delete(e.st.Books, key) // its recording gone with a reorganisation: none until a log says whose it is
	return nil
}

// lookBooking reads a header's recording at an epoch over blocks from–to: its HeaderRecorded log, its transaction (the
// signer recovered, the calldata holding the header: E5-RT3) and its trailer when eth.updater's key sent it; the
// booking and its log's block, nil when no log is found. A read that fails is an error, alarmed (eth:logs).
func (e *outEnv) lookBooking(ctx context.Context, hash [32]byte, epoch uint64, from, to uint64) (*headerBook, uint64, error) {
	if from > to {
		return nil, 0, nil
	}
	q := ethx.LogQuery{Addresses: []common.Address{Address(e.cfg.Eth.Client)}, Topics: [][]common.Hash{{ethx.LightClient.Event("HeaderRecorded")}, {ethx.HashTopic(hash)}}}
	logs, err := e.eth.LogsRange(ctx, q, from, to)
	if err != nil {
		if !e.logsFailed || from < e.logsFailFrom {
			e.logsFailFrom = from
		}
		e.logsFailed = true
		e.alarm.AlarmEvery("eth:logs", fmt.Sprintf("the light client's logs of blocks %d–%d could not be read (%v): a header's booking is read from them (§5.5) — a send booked under it waits, and the updater's own header settles from its receipt; the relayer's and the updater's provider must serve the last three days of logs (E5-GS2)", from, to, err), 6*time.Hour)
		return nil, 0, err
	}
	if e.logsFailed && from <= e.logsFailFrom {
		// a read reaching as far back as the one that failed: a shallower one answering says nothing of it
		e.logsFailed = false
		e.alarm.Clear("eth:logs")
	}
	rec := -1
	var height int64
	for i, l := range logs {
		ev, err := ethx.DecodeHeaderRecorded(l)
		if err == nil && ev.Hash == common.Hash(hash) && ev.Epoch == epoch {
			rec, height = i, ev.Height
		}
	}
	if rec < 0 {
		return nil, 0, nil
	}
	b, err := e.bookOf(ctx, logs[rec].TxHash, hash, epoch, height)
	if err != nil {
		return nil, 0, err
	}
	if b.Ours {
		// the transaction must be the recording: a provider could name the updater's own transaction that carried the
		// header and reverted (overtaken), or a version it kept from the pool (the review of the lane, R6)
		rc, err := e.eth.Receipt(ctx, logs[rec].TxHash)
		if err != nil {
			return nil, 0, err
		}
		if _, ok := recordingOf(rc, Address(e.cfg.Eth.Client), hash); !ok || rc.BlockNumber == nil || rc.BlockNumber.Uint64() != logs[rec].BlockNumber {
			return nil, 0, fmt.Errorf("transaction %s of eth.updater's key is not the recording of header %x its log names (its receipt)", logs[rec].TxHash.Hex(), hash[:4])
		}
	}
	b.Tx = logs[rec].TxHash.Hex()
	return b, logs[rec].BlockNumber, nil
}

// bookOf is the booking a recording transaction makes (E5-RT3): its sender recovered from its signature, and — when
// that is eth.updater — its calldata must be a call of the client carrying this header, so a provider can name the
// updater's own transaction of another header as this one's recording, never make one; then the trailer's sends.
func (e *outEnv) bookOf(ctx context.Context, txHash common.Hash, hash [32]byte, epoch uint64, height int64) (*headerBook, error) {
	tx, err := e.eth.SignedTransaction(ctx, txHash)
	if err != nil {
		return nil, err
	}
	b := &headerBook{Height: height, Epoch: epoch}
	u := e.cfg.Eth.Updater
	if u == "" || tx.From != Address(u) {
		return b, nil
	}
	if tx.To == nil || *tx.To != Address(e.cfg.Eth.Client) {
		return nil, fmt.Errorf("transaction %s of eth.updater's key is not a call of the client, though a HeaderRecorded log of header %x names it", txHash.Hex(), hash[:4])
	}
	if _, h, _, _, err := ethx.HeaderCallOf(tx.Input); err != nil || h != hash {
		return nil, fmt.Errorf("transaction %s of eth.updater's key does not carry header %x, though its HeaderRecorded log names it", txHash.Hex(), hash[:4])
	}
	b.Ours = true
	b.Nonces, _ = trailerNonces(tx.Input)
	return b, nil
}

// recordableFrom is the first Ethereum block a gno header of a height can have been recorded in (E5-GS2): none before
// the header was made, so the blocks since its time at the chain's own spacing (measured over its last spacingSpan
// blocks: 12 s on mainnet, a second on the local run's anvil), and an hour's and a twentieth's more (the two chains'
// clocks, the spacing's drift) — a header bought hours after its block, as a six-hour send's is, is found within the
// last hours of logs, which a provider that keeps no three days of them (publicnode: ≈10,000 blocks) still serves. 0
// when its time or the spacing is not known.
func (e *outEnv) recordableFrom(ctx context.Context, latest *ethx.Block, height int64) uint64 {
	if height <= 0 || e.light == nil || latest == nil {
		return 0
	}
	t, ok := e.blockTime(ctx, height)
	if !ok {
		return 0
	}
	spacing := e.blockSpacing(ctx, latest)
	if spacing <= 0 {
		return 0
	}
	since := 0.0
	if s := t.Unix(); s > 0 && latest.Time > uint64(s) {
		since = float64(latest.Time - uint64(s))
	}
	blocks := uint64((since*1.05 + recordSlack*12) / spacing)
	if blocks >= latest.Number {
		return 0
	}
	return latest.Number - blocks
}

// spacingSpan is the blocks the chain's block spacing is measured over (blockSpacing).
const spacingSpan = 1_000

// blockSpacing is the seconds between the chain's blocks over its last spacingSpan, read once a step; 0 when unread.
func (e *outEnv) blockSpacing(ctx context.Context, latest *ethx.Block) float64 {
	if e.spacingRead {
		return e.spacing
	}
	e.spacingRead = true
	if latest.Number <= spacingSpan {
		return 0
	}
	old, err := e.eth.BlockAt(ctx, ethx.AtNumber(latest.Number-spacingSpan))
	if err != nil || old.Time >= latest.Time {
		return 0
	}
	e.spacing = float64(latest.Time-old.Time) / spacingSpan
	return e.spacing
}

// probeLogs reads the light client's logs of the last three days through the provider, at start and every probeEvery
// (E5-GS2): a provider that cannot serve them — publicnode refuses logs past ≈10,000 blocks — is alarmed eth:logs-depth
// at once, not found out when a booking older than its depth is looked for.
func (e *outEnv) probeLogs(ctx context.Context, now time.Time) {
	if now.Before(e.probeAt) || e.cfg.Eth.Client == "" {
		return
	}
	latest, err := e.eth.BlockAt(ctx, ethx.Latest)
	if err != nil {
		e.probeAt = now.Add(time.Minute) // the head not read: probed again in a minute
		return
	}
	e.probeAt = now.Add(probeEvery)
	from := uint64(0)
	if latest.Number > headerLogBack {
		from = latest.Number - headerLogBack
	}
	q := ethx.LogQuery{Addresses: []common.Address{Address(e.cfg.Eth.Client)}, Topics: [][]common.Hash{{ethx.LightClient.Event("HeaderRecorded")}}}
	if _, err := e.eth.LogsRange(ctx, q, from, latest.Number); err != nil {
		e.alarm.AlarmEvery("eth:logs-depth", fmt.Sprintf("the provider does not serve the light client's logs of the last three days (blocks %d–%d: %v): the bookings of headers are read from them (§5.5, E5-GS2) — a header older than its log depth books nothing for its sends (they wait for their own fee); give the relayer and the updater a provider that serves three days of logs", from, latest.Number, err), probeEvery)
		return
	}
	e.alarm.Clear("eth:logs-depth")
}

// headerLogBack is how far back a header's HeaderRecorded log is looked for first (three days of 12-second blocks).
const headerLogBack = 21_600

func (e *outEnv) route(ctx context.Context, pkg string) (*gnoside.Route, error) {
	if e.routes == nil {
		e.routes = map[string]*gnoside.Route{}
	}
	if rj, ok := e.routes[pkg]; ok {
		return rj, nil
	}
	rj, err := gnoside.RouteAt(ctx, e.gno, pkg, 0)
	if err != nil {
		return nil, err
	}
	e.routes[pkg] = rj
	return rj, nil
}

func (e *outEnv) mailboxJSON(ctx context.Context) (*gnoside.Mailbox, error) {
	if e.mailbox != nil {
		return e.mailbox, nil
	}
	mb, err := gnoside.MailboxAt(ctx, e.gno, e.cfg.Gno.Mailbox, 0)
	if err != nil {
		return nil, err
	}
	e.mailbox = mb
	return mb, nil
}

// migration is a route's open migration at the head (nil for none), read once a step.
func (e *outEnv) migration(ctx context.Context, rt Route) (*gnoside.Migration, error) {
	rj, err := e.route(ctx, rt.Gno)
	if err != nil {
		return nil, err
	}
	mb, err := e.mailboxJSON(ctx)
	if err != nil {
		return nil, err
	}
	return gnoside.MigrationOf(ctx, e.route, rt.Symbol, rt.Gno, rt.Predecessor, rj, mb)
}

// readLines reads every route's line of migrations once a step (K34); an unread line is alarmed while it stands and
// holds the walk at a route message from anyone but the configured realm (G46).
func (e *outEnv) readLines(ctx context.Context) {
	e.lines, e.lineErrs = map[string]*gnoside.Migration{}, map[string]error{}
	for _, rt := range e.cfg.Routes {
		key := "route:line-unread:" + rt.Symbol
		mig, err := e.migration(ctx, rt)
		if err != nil {
			e.lineErrs[rt.Symbol] = err
			e.alarm.Alarm(key, fmt.Sprintf("%s's line of migrations could not be read this step (%v): a send from anyone but the configured realm %s is held, never walked past, until the line reads (G46)", rt.Symbol, err, rt.Gno))
			continue
		}
		e.alarm.Clear(key)
		if mig != nil {
			e.lines[rt.Symbol] = mig
		}
	}
}

func (e *outEnv) lineUnread(rt *Route) bool { return e.lines == nil || e.lineErrs[rt.Symbol] != nil }

// lineSender says whether a message's sender is a route realm of the token's line (K34).
func (e *outEnv) lineSender(rt *Route, sender [32]byte) bool {
	return routeLineSender(rt, e.lines[rt.Symbol], sender)
}

// sendRefunded says whether a route message is a send gno.land refunded (AQ2): the route's send record carries the
// mark, read only where one can be set (G42). Both machines read it: a refunded send is never delivered, so it buys
// no header and no batch either.
func (e *outEnv) sendRefunded(ctx context.Context, t *outMsg) (bool, string, error) {
	if t.rt == nil {
		return false, "", nil
	}
	rj, err := e.route(ctx, t.rt.Gno)
	if err != nil {
		return false, "", err
	}
	mig, err := e.migration(ctx, *t.rt)
	if err != nil {
		return false, "", err
	}
	if !gnoside.Refundable(rj, mig) {
		return false, "", nil
	}
	id := t.msg.IDHex()
	sd, err := gnoside.SendOnLine(ctx, e.gno, t.rt.Gno, mig, id, 0)
	if err != nil {
		return false, "", err
	}
	if sd == nil || !bool(sd.Refunded) {
		return false, "", nil
	}
	return true, fmt.Sprintf("send %s (%s, %d to %s) was refunded on gno.land by GovDAO's decision: the amount left the lock, so a delivery would mint against nothing — it must never deliver (AQ2)", id, t.rt.Symbol, sd.Amount, sd.From), nil
}

// checkContracts holds the config's Ethereum addresses against the ones hlmailbox compiles (E-M1's ethContracts): a
// mismatch is the operator's config pointing at another deployment, alarmed, and nothing is delivered until it is
// mended (a realm that renders no list is not judged).
func (e *outEnv) checkContracts(mb *gnoside.Mailbox) error {
	if len(mb.EthContracts) == 0 {
		return nil
	}
	want := map[string]string{"mailbox": e.cfg.Eth.Mailbox, "client": e.cfg.Eth.Client, "gate": e.cfg.Eth.Gate, "hlAdmin": e.cfg.Eth.Admin,
		"proxyAdmin": e.cfg.Eth.ProxyAdmin, "merkleTreeHook": e.cfg.Eth.MerkleTreeHook, "protocolFee": e.cfg.Eth.ProtocolFee}
	if r := e.cfg.RouteBySymbol("GNOT"); r != nil {
		want["token0"] = r.Token
	}
	if r := e.cfg.RouteBySymbol("BUBBLE"); r != nil {
		want["token1"] = r.Token
	}
	var bad []string
	for name, cfg := range want {
		got, ok := mb.EthContracts[name]
		if cfg == "" || !ok {
			continue
		}
		if !strings.EqualFold(got, cfg) {
			bad = append(bad, fmt.Sprintf("%s: the config says %s, hlmailbox compiles %s", name, cfg, got))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		err := errors.New("the config's Ethereum contracts are not the deployment's hlmailbox compiles: " + strings.Join(bad, "; "))
		e.alarm.Alarm("config:contracts", err.Error())
		return err
	}
	e.alarm.Clear("config:contracts")
	return nil
}

// gateState is the gate's state at latest, read once a step.
func (e *outEnv) gateState(ctx context.Context) (*ethx.GateState, error) {
	if e.gate != nil {
		return e.gate, nil
	}
	g, err := ethx.ReadGate(ctx, e.eth, Address(e.cfg.Eth.Gate), ethx.Latest)
	if err != nil {
		return nil, err
	}
	e.gate = g
	return g, nil
}

// unpausedOnly drops the route messages from a waiting list while the gate refuses route deliveries — a flag on it,
// or the client's quarantine by the chain's time — and says why: no header and no batch is bought for a send that
// could not be delivered under it (the Solana relayer staged no route message while paused, §5.7); they wait for
// the pause to end, and the decisions, which skip the gate, still count.
func (e *outEnv) unpausedOnly(ctx context.Context, waiting []*outMsg, cs *ethx.ClientState, now int64) ([]*outMsg, string, error) {
	routes := false
	for _, t := range waiting {
		routes = routes || t.kind == kindRoute
	}
	if !routes {
		return waiting, "", nil
	}
	g, err := e.gateState(ctx)
	if err != nil {
		return nil, "", err
	}
	paused, why := deliveriesPaused(g, cs.QuarantineEnd, now)
	if !paused {
		return waiting, "", nil
	}
	var out []*outMsg
	for _, t := range waiting {
		if t.kind != kindRoute {
			out = append(out, t)
		}
	}
	return out, why, nil
}

// readAdmins reads HlAdmin's line once a step; on a failure the configured address stands in alone.
func (e *outEnv) readAdmins(ctx context.Context) error {
	line, err := followAdmins(ctx, e.eth, Address(e.cfg.Eth.Admin), ethx.Latest)
	if err != nil {
		e.admins = nil
		return err
	}
	e.admins = line
	return nil
}

func (e *outEnv) adminLine() *adminLine {
	if e.admins != nil {
		return e.admins
	}
	return &adminLine{Live: Address(e.cfg.Eth.Admin)}
}

// classify sorts a message: ours (a route's or a decision), held (G31, G46), or not ours.
func (e *outEnv) classify(m *gnoside.Msg) (msgKind, *Route) {
	if m.Msg.Destination != e.cfg.Eth.Domain {
		return kindOther, nil
	}
	rcpt, ok := ethx.AddressFrom32(m.Msg.Recipient)
	if !ok {
		return kindOther, nil // the mailbox's bytes32ToAddress refuses it: nobody can deliver it
	}
	line := e.adminLine()
	if gov, err := e.cfg.GovSender(); err == nil && gov == m.Msg.Sender {
		if line.Knows(rcpt) {
			return kindDecision, nil
		}
		if e.cfg.RouteByToken(rcpt) == nil {
			return kindOtherAdmin, nil
		}
	}
	if line.Knows(rcpt) {
		return kindOther, nil // to HlAdmin from a stranger: it refuses the sender
	}
	rt := e.cfg.RouteByToken(rcpt)
	if rt == nil {
		return kindOther, nil
	}
	if !e.lineSender(rt, m.Msg.Sender) {
		if e.lineUnread(rt) {
			return kindLineUnread, rt
		}
		return kindOther, nil // the token refuses a sender that is not its enrolled router
	}
	return kindRoute, rt
}

// headerHash is block h's hash from the own node (cached, with its ValidatorsHash); false when the node has no block h
// yet (past head).
func (e *outEnv) headerHash(ctx context.Context, h, head int64) ([32]byte, bool, error) {
	if x, ok := e.hashes[h]; ok {
		return x, true, nil
	}
	if h > head {
		return [32]byte{}, false, nil
	}
	blk, err := e.light.BlockAt(ctx, h)
	if err != nil {
		return [32]byte{}, false, err
	}
	if e.hashes == nil || e.vals == nil || e.lrh == nil {
		e.hashes, e.vals, e.lrh = map[int64][32]byte{}, map[int64][32]byte{}, map[int64][32]byte{}
	}
	e.hashes[h] = blk.Hash
	if blk.Header != nil {
		e.vals[h] = blk.Header.ValidatorsHashArray()
		e.lrh[h], _ = blk.Header.LastResultsHashArray()
	}
	return blk.Hash, true, nil
}

// walkOut walks the backlog from st.Next: ended messages and messages not ours advance the cursor; an undelivered
// message of ours is listed (decisions apart from routes), with its usable events and its proving header; a held
// one stops the cursor (never passed). Ethereum's delivered marks are read at latest, in one batch, and the ones
// marked there again at the finalized block: a message ends delivered only by that one (proofH −1), and one
// delivered at latest alone is settling. head is the gno head read after the mailbox, the one the scan reached, so
// every message walked lies in a block the scan read.
func (e *outEnv) walkOut(ctx context.Context, st *outState, cs *ethx.ClientState, mb *gnoside.Mailbox, head int64) (decisions, routes []*outMsg, err error) {
	st.init()
	advance := true
	var todo []*outMsg
	for n := st.Next; n < mb.Count; n++ {
		nk := strconv.FormatUint(uint64(n), 10)
		if st.Done[nk] != "" {
			if advance {
				st.Next = n + 1
			}
			continue
		}
		msg, err := gnoside.MessageAt(ctx, e.gno, e.cfg.Gno.Mailbox, mb.Domain, n)
		if err != nil {
			return nil, nil, err
		}
		kind, rt := e.classify(msg)
		switch kind {
		case kindOther:
			st.end(n, "other")
			if advance {
				st.Next = n + 1
			}
			continue
		case kindOtherAdmin:
			advance = false
			rcpt, _ := ethx.AddressFrom32(msg.Msg.Recipient)
			e.alarm.AlarmEvery("decision:other-admin:"+nk, fmt.Sprintf("gno #%d is r/hlgov's decision to %s, which HlAdmin's line (%s) does not name: held, never walked past — a successor the chain names later is delivered then; a stranger's address needs r/hlgov's recipient switch undone (G31)", n, rcpt.Hex(), e.adminLine().Live.Hex()), 24*time.Hour)
			continue
		case kindLineUnread:
			advance = false
			continue
		}
		e.alarm.Clear("decision:other-admin:" + nk)
		t := &outMsg{n: n, msg: msg, kind: kind, rt: rt}
		if kind == kindRoute {
			// the fee this send paid, from its own route realm's event (the message's sender): never another realm's word
			if sf, ok := st.Index.FeeOf(n); ok && gnoside.RealmKey(sf.Realm) == msg.Msg.Sender {
				t.fee, t.feeKnown = sf.Fee, true
			}
		}
		if kind == kindDecision {
			dk, seq, ok := gnolight.DecisionPrefix(msg.Msg.Body)
			if !ok {
				st.end(n, "other") // a body under nine bytes: HlAdmin's handle refuses it; the realm never writes one
				if advance {
					st.Next = n + 1
				}
				continue
			}
			t.dk, t.seq = dk, seq
		}
		todo = append(todo, t)
		advance = false
	}
	if len(todo) == 0 {
		return nil, nil, nil
	}
	ids := make([][32]byte, len(todo))
	for i, t := range todo {
		ids[i] = t.msg.ID
	}
	delivered, err := ethx.DeliveredMany(ctx, e.eth, Address(e.cfg.Eth.Mailbox), ids, ethx.Latest)
	if err != nil {
		return nil, nil, err
	}
	// a mark at latest ends a message only once it holds at the finalized block (a delivery a reorg drops is made
	// again); until then the message is settling: never sent again, waiting for no header. A provider without the
	// finalized block's state is no verdict: they stay settling.
	final := make([]bool, len(todo))
	var atLatest []int
	for i := range todo {
		if delivered[i] {
			atLatest = append(atLatest, i)
		}
	}
	if len(atLatest) > 0 {
		if fin, ferr := e.eth.BlockAt(ctx, ethx.Finalized); ferr == nil {
			fids := make([][32]byte, len(atLatest))
			for k, i := range atLatest {
				fids[k] = ids[i]
			}
			if marks, merr := ethx.DeliveredMany(ctx, e.eth, Address(e.cfg.Eth.Mailbox), fids, ethx.AtHash(fin.Hash)); merr == nil {
				for k, i := range atLatest {
					final[i] = marks[k]
				}
			}
		}
	}
	// each undelivered message's usable events, and the headers after the ones read this step (E5-GS4): its dispatch,
	// its newest readNewest and those whose next header this machine saw recorded — anyone's Reemit adds an event a
	// block, and every event was read every step; the others are judged once, at the finalized block (compact)
	if cs != nil {
		st.noteHeader(cs.TrustedHeight, cs.Epoch)
	}
	known := e.knownHeaders(st, cs)
	type cand struct {
		t     *outMsg
		h     int64
		hash  [32]byte
		entry *ethx.Entry // read for good at the finalized block (e.final); nil: read now
	}
	var cands []cand
	var hashes [][32]byte
	batch := e.cfg.ReemitBatch()
	for i, t := range todo {
		if delivered[i] {
			continue
		}
		var usable []int64
		for _, h := range st.Index.Of(t.n, t.msg.Height) {
			if _, bad := st.Unusable[eventKey(t.n, h)]; bad {
				continue
			}
			// the scan's reading of the block, which both machines make alike: a leaf over the bound, or none
			// carrying the message, proves nothing (the relayer re-emits it, and the updater serves the re-emit)
			size, read := st.Index.LeafOf(t.n, h)
			if read && (size < 0 || size > e.cfg.MaxLeaf()) {
				continue
			}
			// a route send is never proven from a leaf of more than a batch's Dispatch events — each costs every
			// delivery from the leaf ≈94,000 gas, and anyone's Reemits can pile them up (E2-RT2): it is re-emitted
			// into the relayer's own batch instead. A decision is delivered from any leaf (never waits for fees).
			n, counted := st.Index.CountOf(t.n, h)
			if counted && t.kind == kindRoute && n > batch {
				continue
			}
			if counted {
				if t.counts == nil {
					t.counts = map[int64]int{}
				}
				t.counts[h] = n
			}
			if read && size > 0 {
				if t.bytes == nil {
					t.bytes = map[int64]int{}
				}
				t.bytes[h] = size
			}
			usable = append(usable, h)
		}
		// its newest usable event at or below the trusted height: the one a header proof of our machines targets
		// (planWaits' old groups, the relayer's -proveOld, the backstop) — read while one may be in flight unseen
		below := int64(0)
		if cs != nil {
			for j := len(usable) - 1; j >= 0; j-- {
				if usable[j]+1 <= cs.TrustedHeight {
					below = usable[j]
					break
				}
			}
		}
		for j, h := range usable {
			if h != t.msg.Height && j < len(usable)-readNewest && !known[h+1] && h != below {
				t.events = append(t.events, h) // not read this step: compact judges it at the finalized block
				continue
			}
			t.reads = append(t.reads, h)
			hash, ok, err := e.headerHash(ctx, h+1, head)
			if err != nil {
				return nil, nil, err
			}
			// the rebuilt leaves must hash to the next header's LastResultsHash, or nothing in the block can be proven —
			// both machines judge it alike, so no header is bought for a block whose results the node's JSON does not
			// carry exactly (a string that is not UTF-8 reads as U+FFFD; the shared results fix's residual)
			if ok {
				if bad, why := st.Index.Mismatch(h, e.lrh[h+1]); bad {
					e.alarm.Alarm("results:mismatch", why+": a string the node's JSON cannot carry exactly (bytes that are not UTF-8 read as U+FFFD), a type whose fields changed, or a node serving other results; nothing in it is proven, its messages are re-emitted into a later block")
					continue
				}
			}
			t.events = append(t.events, h)
			// a header above the client's trusted height is recorded by no update yet (nor any header proof): not read
			if !ok || cs == nil || h+1 > cs.TrustedHeight {
				continue
			}
			if en, hit := e.final[hash]; hit {
				cands = append(cands, cand{t: t, h: h, hash: hash, entry: &en})
				continue
			}
			cands = append(cands, cand{t: t, h: h, hash: hash})
			hashes = append(hashes, hash)
		}
		t.spoiled = st.Index.Spoiled(t.n, t.msg.Height)
		// the header after the newest usable event: the one a header bought for the message's group is (E3-GS2), and
		// one a header proof may record while it lies at or below the trusted height and the current set signs it
		// (E3-RT4)
		if h := t.latest(); h > 0 {
			if x, ok := e.hashes[h+1]; ok {
				t.newestHash = x
				t.newestCurrentSet = cs != nil && e.vals[h+1] == cs.SetHash
			}
		}
	}
	if len(cands) > 0 && cs != nil {
		var entries []ethx.Entry
		if len(hashes) > 0 {
			var err error
			// in requests of at most the provider's batch size (ethx.readViews, E5-GS4)
			if entries, err = ethx.ReadEntries(ctx, e.eth, Address(e.cfg.Eth.Client), hashes, ethx.Latest); err != nil {
				return nil, nil, err
			}
		}
		// each message proven from its cheapest usable leaf whose next header the client accepted — by the model's
		// delivery gas, the leaf's Dispatch events and its bytes (E3-GS2); among equals the newest
		k := 0
		for _, c := range cands {
			en := c.entry
			if en == nil {
				en = &entries[k]
				k++
			}
			if !en.Proves(cs.Epoch) {
				continue
			}
			c.t.proven = append(c.t.proven, provenEvent{h: c.h, hash: c.hash})
			if c.h == c.t.latest() {
				c.t.newestProven = true
			}
			if p := c.t.proofH; p == 0 || c.t.leafGas(c.h) < c.t.leafGas(p) || (c.t.leafGas(c.h) == c.t.leafGas(p) && c.h > p) {
				c.t.proofH, c.t.proofHash = c.h, c.hash
			}
		}
	}
	for i, t := range todo {
		switch {
		case final[i]:
			t.proofH = -1 // delivered at the finalized block: ended by the caller
		case delivered[i]:
			t.settling = true
		}
		if t.kind == kindDecision {
			decisions = append(decisions, t)
		} else {
			routes = append(routes, t)
		}
	}
	return decisions, routes, nil
}

// compact judges, at the finalized block, the events of the listed messages the walk does not read every step (E5-GS4),
// at most compactPerStep a step, each message's newest first. An event the walk cannot use (an unusable mark, a leaf
// over the bound, a route send's leaf of more than a batch) is dropped from the index. One whose next header lies at or
// below the client's trusted height there is read there: recorded at the client's epoch, it is kept — its entry for
// good (e.final: it never changes, and no reorganisation reaches below the finalized block) and its header among the
// ones seen recorded, so the walk reads it every step; recorded neither there nor at latest (a header proof on its way
// to finality), it is judged again compactAgain later, and dropped when it still is not — no update records a header at
// or below the trusted height, and a header proof that waited an hour in the pool is rare. One above it waits for the
// finalized block to pass it (an update in flight may record it). Never dropped: a message's dispatch, its newest
// readNewest events, the events whose header this machine saw recorded or sent, and its newest event at or below the
// trusted height there — the one a header proof of our machines targets (planWaits' old groups, the relayer's
// -proveOld, the backstop), while one may be in flight unseen by this machine (the review of the lane, R2). A stranger's
// re-emits so cost each machine two reads apiece, an hour apart, instead of one every step for good.
func (e *outEnv) compact(ctx context.Context, now time.Time, st *outState, ms []*outMsg, head int64) error {
	type pick struct {
		t    *outMsg
		h    int64
		hash [32]byte
	}
	var picks []pick
	var hashes [][32]byte
	var fin *ethx.Block
	var trusted int64
	var epoch uint64
	loaded := false
	batch := e.cfg.ReemitBatch()
	known := e.knownHeaders(st, nil)
	for _, h := range st.Headers {
		known[h] = true // whatever their epoch: kept, the walk reads them
	}
	if e.unproven == nil || len(e.unproven) > 65_536 {
		e.unproven = map[string]time.Time{}
	}
	for _, t := range ms {
		if t.proofH < 0 || t.settling || len(picks) >= compactPerStep {
			continue
		}
		keep := map[int64]bool{t.msg.Height: true}
		for j := max(len(t.events)-readNewest, 0); j < len(t.events); j++ {
			keep[t.events[j]] = true
		}
		for _, h := range t.reads {
			keep[h] = true // the walk reads it every step
		}
		usable := map[int64]bool{}
		for _, h := range t.events {
			usable[h] = true
		}
		all := st.Index.Of(t.n, t.msg.Height)
		if len(all) <= len(keep) {
			continue
		}
		if !loaded {
			loaded = true
			if fin = e.finalized(ctx); fin == nil {
				return nil
			}
			var err error
			if trusted, epoch, err = ethx.ReadTrust(ctx, e.eth, Address(e.cfg.Eth.Client), ethx.AtHash(fin.Hash)); err != nil {
				return err
			}
		}
		// its newest usable event at or below the trusted height: a header proof's target
		for j := len(t.events) - 1; j >= 0; j-- {
			if t.events[j]+1 <= trusted {
				keep[t.events[j]] = true
				break
			}
		}
		for i := len(all) - 1; i >= 0 && len(picks) < compactPerStep; i-- {
			h := all[i]
			if keep[h] || known[h+1] {
				continue
			}
			if !usable[h] {
				// the walk's own exclusions (a leaf no header proves it from): nothing to read; a block whose root the
				// next header refutes left the walk's list too, and leaves the index once its mark stands
				_, bad := st.Unusable[eventKey(t.n, h)]
				size, read := st.Index.LeafOf(t.n, h)
				n, counted := st.Index.CountOf(t.n, h)
				if bad || (read && (size < 0 || size > e.cfg.MaxLeaf())) || (counted && t.kind == kindRoute && n > batch) {
					st.Index.Drop(t.n, h)
					delete(st.Unusable, eventKey(t.n, h))
				}
				continue
			}
			if h+1 > trusted {
				continue // an update may still record its next header: judged once the finalized block passes it
			}
			if since, ok := e.unproven[eventKey(t.n, h)]; ok && now.Sub(since) < compactAgain {
				continue // judged unrecorded once: again an hour on
			}
			hash, ok, err := e.headerHash(ctx, h+1, head)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if en, hit := e.final[hash]; hit && en.Epoch == epoch {
				continue // recorded, known: kept
			}
			picks = append(picks, pick{t, h, hash})
			hashes = append(hashes, hash)
		}
	}
	if len(picks) == 0 {
		return nil
	}
	entries, err := ethx.ReadEntries(ctx, e.eth, Address(e.cfg.Eth.Client), hashes, ethx.AtHash(fin.Hash))
	if err != nil {
		return err
	}
	var gone []pick
	var again [][32]byte
	for i, p := range picks {
		if entries[i].Proves(epoch) {
			e.final[p.hash] = entries[i]
			st.noteHeader(p.h+1, epoch)
			delete(e.unproven, eventKey(p.t.n, p.h))
			continue
		}
		gone, again = append(gone, p), append(again, p.hash)
	}
	if len(gone) == 0 {
		return nil
	}
	// recorded at latest at the epoch or after it, not yet at the finalized block (a header proof on its way): judged
	// once it is final; one recorded at an older epoch proves nothing now
	latest, err := ethx.ReadEntries(ctx, e.eth, Address(e.cfg.Eth.Client), again, ethx.Latest)
	if err != nil {
		return err
	}
	for i, p := range gone {
		key := eventKey(p.t.n, p.h)
		if latest[i].Height != 0 && latest[i].Epoch >= epoch {
			delete(e.unproven, key)
			continue
		}
		if _, judged := e.unproven[key]; !judged {
			e.unproven[key] = now // the first time: judged again compactAgain later
			continue
		}
		delete(e.unproven, key)
		st.Index.Drop(p.t.n, p.h)
		delete(st.Unusable, key)
	}
	return nil
}

// compactAgain is how long after an event was first judged unrecorded the compaction judges it again before it drops it.
const compactAgain = time.Hour

// scanStart is the dispatch height of the first message not ended (the oldest that can still wait), or the head
// when every message ended: where a fresh index starts, and the height below which no unbuildable mark matters.
func (e *outEnv) scanStart(ctx context.Context, st *outState, mb *gnoside.Mailbox, head int64) (int64, error) {
	if st.Next >= mb.Count {
		return head, nil
	}
	msg, err := gnoside.MessageAt(ctx, e.gno, e.cfg.Gno.Mailbox, mb.Domain, st.Next)
	if err != nil {
		return 0, err
	}
	return msg.Height, nil
}

// scanEvents extends the index from its last height to the head (at most maxBlocks a step), before the walk reads
// it: a fresh index starts at the oldest message that can still wait (scanStart), or at the head when none can. It
// answers the blocks read that carried an event (the updater reads r/sys/validators' among them).
func (e *outEnv) scanEvents(ctx context.Context, st *outState, head int64, oldest int64, maxBlocks int64) ([]gnoside.BlockEvents, error) {
	st.init()
	if st.Index.Scanned == 0 {
		switch {
		case oldest > 0:
			st.Index.Scanned = oldest - 1
		default:
			st.Index.Scanned = head
		}
	}
	// no message waits from before the oldest listed dispatch: older unbuildable marks are dropped, and the bookings of
	// headers at or below it (a header proves the block before it) with them
	if oldest > 0 {
		st.Index.PruneBelow(oldest)
		for k, b := range st.Books {
			if b == nil || b.Height <= oldest {
				delete(st.Books, k)
			}
		}
		st.pruneHeaders(oldest)
	}
	from := st.Index.Scanned + 1
	if from > head {
		return nil, nil
	}
	found, last, err := gnoside.ScanBlocks(ctx, e.blocks, e.cfg.Gno.Mailbox, from, head, maxBlocks)
	for _, be := range found {
		st.Index.Add(be, e.cfg.Eth.Domain)
		// a type gnolight has no schema for — a chain upgrade's new error or event type — would show only as re-emits
		// (each paid in GNOT) of every message beside one: named once, when a block of ours is read carrying it
		if be.Schema != "" {
			for _, d := range be.Dispatches {
				if d.Destination == e.cfg.Eth.Domain {
					e.alarm.Alarm("results:schema:"+be.Schema, fmt.Sprintf("block %d cannot be proven: %s — a type gnolight (errortypes.go, rpcjson.go) has no schema for, so every message dispatched beside one is re-emitted into a later block; add the type", be.Height, be.Why))
					break
				}
			}
		}
		// an ended message's events — a stranger's dispatch ended "other" before the scan reached it, anyone's
		// Reemit of a delivered nonce — are not kept (G8: the file never grows by them)
		for _, d := range be.Dispatches {
			if d.Destination == e.cfg.Eth.Domain && (d.Nonce < st.Next || st.Done[strconv.FormatUint(uint64(d.Nonce), 10)] != "") {
				st.Index.Forget(d.Nonce)
			}
		}
	}
	if last >= from {
		st.Index.Scanned = last
	}
	return found, err
}

// ---------------------------------------------------------------- the rule --

// waitPlan is what the waiting messages ask (§5.7, §6.1): one update serves them when their newest usable events
// share one block above the trusted height; when they do not — events in more than one block, or a message with no
// usable event above the trusted height, which no update can serve — the relayer re-emits them in one transaction
// (the batch, oldest first, at most the batch size: every Dispatch in a leaf costs each delivery proven from it
// ≈91k gas, E-M2b's measure), after which they share its block. A header for a group is bought when the fees of the
// group it serves cover it (§5.5); the batch is re-emitted when the fees of the batch cover the header it will need.
// A message whose newest usable event already lies below the trusted height after a re-emit gathered it — a header
// above it landed first, anyone's update: the race a stranger can run against every re-emit (E3-RT4) — and a decision
// whose newest event lies there are not re-emitted again: the header after that event, signed by the client's current
// set, is proven (proveHeader, which no later update pre-empts) under the same rule as a group's header (Old).
type waitPlan struct {
	Waiting int         // messages waiting for a header
	Shared  int64       // the one block their newest usable events share above the trusted height; 0 when none
	Split   bool        // they do not share one
	Below   []uint32    // the ones no header serves as they stand: re-emitted (no usable event above the trusted height, none to prove)
	Old     []waitGroup // the ones a header proof serves, by the block of their newest usable event below the trusted height
	Groups  []waitGroup // the others by the block of their newest usable event, oldest block first
	Reemit  []uint32    // the batch re-emit: oldest first, at most the batch size; empty when it would gather nothing
	Oldest  int64       // the oldest group's block (0: none)
}

// waitGroup is the waiting messages one header serves: their newest usable events lie in Block, proven by Block+1
// (an update; a header proof for an Old group).
type waitGroup struct {
	Block int64
	Msgs  []*outMsg
}

// provesOld says whether a waiting message is served by proving the header after its newest usable event (E3-RT4):
// that header is not accepted, the client's current set signs it (proveHeader takes it at or below the trusted
// height), and the message is a decision — which never waits for a re-emit to be raced again — or its newest usable
// event is no longer its own dispatch (a re-emit gathered it already), or a re-emit of it landed in a block nothing can
// be proven from (spoiled: another re-emit could land beside another stranger's). A send still at its dispatch is
// re-emitted first, so it shares a header with others.
func (t *outMsg) provesOld() bool {
	h := t.latest()
	return h > 0 && !t.newestProven && t.newestCurrentSet && (t.kind == kindDecision || h != t.msg.Height || t.spoiled > 0)
}

// planWaits applies the rule to the waiting messages (no accepted header among their events, or held), in nonce order.
// The batch gathers the ones below the trusted height that no header proof serves and the ones of blocks holding fewer
// than a batch, oldest first: a block holding a batch already is as gathered as a re-emit can make it and waits for its
// own header (re-emitting it again would gather nothing), and when the gatherable ones already share one block nothing
// is re-emitted.
func planWaits(waiting []*outMsg, trusted int64, batch int) waitPlan {
	p := waitPlan{Waiting: len(waiting)}
	add := func(gs []waitGroup, by map[int64]int, h int64, t *outMsg) []waitGroup {
		i, ok := by[h]
		if !ok {
			i = len(gs)
			by[h] = i
			gs = append(gs, waitGroup{Block: h})
		}
		gs[i].Msgs = append(gs[i].Msgs, t)
		return gs
	}
	byBlock, byOld := map[int64]int{}, map[int64]int{}
	below := func(t *outMsg) bool { h := t.latest(); return h == 0 || (h+1 <= trusted && !t.provesOld()) }
	for _, t := range waiting {
		h := t.latest()
		switch {
		case below(t):
			p.Below = append(p.Below, t.n)
		case h+1 <= trusted:
			p.Old = add(p.Old, byOld, h, t)
		default:
			p.Groups = add(p.Groups, byBlock, h, t)
		}
	}
	sort.Slice(p.Groups, func(i, j int) bool { return p.Groups[i].Block < p.Groups[j].Block })
	sort.Slice(p.Old, func(i, j int) bool { return p.Old[i].Block < p.Old[j].Block })
	if len(p.Groups) > 0 {
		p.Oldest = p.Groups[0].Block
	}
	if len(waiting) == 0 {
		return p
	}
	if len(p.Groups) == 1 && len(p.Below) == 0 && len(p.Old) == 0 {
		p.Shared = p.Groups[0].Block
		return p
	}
	p.Split = true
	full := map[int64]bool{}
	for _, g := range p.Groups {
		full[g.Block] = batch > 0 && len(g.Msgs) >= batch
	}
	var gather []uint32
	blocks, anyBelow := map[int64]bool{}, false
	for _, t := range waiting {
		h := t.latest()
		switch {
		case below(t):
			anyBelow = true
		case h+1 <= trusted, full[h]:
			continue // a header proof serves it, or its block holds a batch already
		default:
			blocks[h] = true
		}
		gather = append(gather, t.n)
	}
	if !anyBelow && len(blocks) <= 1 {
		return p
	}
	if batch > 0 && len(gather) > batch {
		gather = gather[:batch]
	}
	p.Reemit = gather
	return p
}

// msgsOf are the listed messages with these nonces.
func msgsOf(waiting []*outMsg, nonces []uint32) []*outMsg {
	want := map[uint32]bool{}
	for _, n := range nonces {
		want[n] = true
	}
	var out []*outMsg
	for _, t := range waiting {
		if want[t.n] {
			out = append(out, t)
		}
	}
	return out
}

// waitingOf are the listed messages that wait for a header (undelivered, no proving header or held under one that pays
// nothing for it, not a decision copy whose number HlAdmin recorded already, cannot take yet or takes from another copy,
// not settling), in nonce order.
func waitingOf(lists ...[]*outMsg) []*outMsg {
	var out []*outMsg
	for _, l := range lists {
		for _, t := range l {
			if (t.proofH == 0 || t.held) && !t.past && !t.settling && !t.early && !t.dup {
				out = append(out, t)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].n < out[j].n })
	return out
}

// oldestDispatch is the oldest dispatch height among listed messages (0 for none).
func oldestDispatch(lists ...[]*outMsg) int64 {
	var o int64
	for _, l := range lists {
		for _, t := range l {
			if t.msg != nil && t.msg.Height > 0 && (o == 0 || t.msg.Height < o) {
				o = t.msg.Height
			}
		}
	}
	return o
}

// markDecisions marks the decision copies that wait for no header (E3-GS6): past — a number their HlAdmin recorded
// already (its counter at latest), or a copy to a retired HlAdmin (the relayer ends both); early — a number HlAdmin
// cannot take yet, an earlier one not being recorded at the finalized block (its counter there, at latest when the
// provider has no finalized state); dup — a copy whose number another copy is proven, settling or recorded for. Anyone
// may mint copies of any number r/hlgov ever dispatched (Redispatch), each a new message: only the one copy HlAdmin
// takes next, and only while no other copy of its number is served, buys a header — the emergency cap's, outside the
// budget — or a re-emit. Read once per HlAdmin a step.
func (e *outEnv) markDecisions(ctx context.Context, ds []*outMsg) error {
	type counters struct{ latest, final uint64 }
	seen := map[common.Address]counters{}
	var fin *ethx.At
	finRead := false
	for _, t := range ds {
		rcpt, _ := ethx.AddressFrom32(t.msg.Msg.Recipient)
		if e.adminLine().IsRetired(rcpt) {
			t.past = true
			continue
		}
		c, ok := seen[rcpt]
		if !ok {
			st, err := ethx.ReadAdmin(ctx, e.eth, rcpt, ethx.Latest)
			if err != nil {
				return err
			}
			c = counters{latest: st.LastSeq, final: st.LastSeq}
			if !finRead {
				finRead = true
				if b, err := e.eth.BlockAt(ctx, ethx.Finalized); err == nil {
					at := ethx.AtHash(b.Hash)
					fin = &at
				}
			}
			if fin != nil {
				if fs, err := ethx.ReadAdmin(ctx, e.eth, rcpt, *fin); err == nil {
					c.final = fs.LastSeq
				}
			}
			seen[rcpt] = c
		}
		switch {
		case t.seq <= c.latest:
			t.past = true
		case t.seq != c.final+1:
			t.early = true
		}
	}
	key := func(t *outMsg) string {
		return string(t.msg.Msg.Recipient[:]) + strconv.FormatUint(t.seq, 10)
	}
	served := map[string]bool{}
	for _, t := range ds {
		if t.proofH != 0 || t.settling || t.past {
			served[key(t)] = true
		}
	}
	for _, t := range ds {
		if t.proofH == 0 && !t.settling && !t.past && served[key(t)] {
			t.dup = true
		}
	}
	return nil
}

// bookedFor says whether a proven route send is delivered at no fee check (§5.5; E3-GS2, plan CF8(c), E4-GS2, E4-GS4,
// E4-CF2): one of its usable events — any, not only the newest — lies in a block whose next header, accepted at the
// client's epoch, the updater's key recorded with a trailer naming the send (bookingOf): the (nonce, header) pair the
// budget booked, the send's fee credited and its delivery debited when it bought that header. It is then proven from
// the cheapest such leaf by the model, the newest among equals (proofH moves there). Any other header — a stranger's,
// the relayers' own backstop, a decision's or a validator change's, or the updater's bought for other sends, under
// which a stranger's Reemit beside kilobytes of its own events can put any send — books nothing for it; and a stranger's
// Reemit after the header was bought moves nothing the trailer named.
func (e *outEnv) bookedFor(ctx context.Context, now time.Time, cs *ethx.ClientState, t *outMsg) bool {
	if t.kind != kindRoute || cs == nil {
		return false
	}
	var best *provenEvent
	for i := range t.proven {
		p := &t.proven[i]
		b := e.bookingOf(ctx, now, p.hash, cs.Epoch, p.h+1)
		if b == nil && e.own != nil {
			b = e.own(p.hash, cs.Epoch)
		}
		if !b.names(t.n) {
			continue
		}
		if best == nil || t.leafGas(p.h) < t.leafGas(best.h) || (t.leafGas(p.h) == t.leafGas(best.h) && p.h > best.h) {
			best = p
		}
	}
	if best == nil {
		return false
	}
	t.proofH, t.proofHash, t.booked = best.h, best.hash, true
	return true
}

// markHeld judges every proven route send not settling (§5.5; E3-GS2, E3-GS7, E4-GS2): delivered at no fee check when
// a header the updater booked it under proves it (bookedFor), else only while its own fee covers its delivery from its
// cheapest proven leaf at the live price; one neither pays for is held — it waits as an unproven send does: for its
// group's header (a re-emit first when its newest event's header is accepted already), the six-hour rule within the
// budget, and delivery:age — never proven-and-forgotten. Both machines judge it alike, from the chain's bookings; the
// relayer judges again from the leaf it builds before it delivers.
func (e *outEnv) markHeld(ctx context.Context, sender ethSender, latest *ethx.Block, cs *ethx.ClientState, now time.Time, routes []*outMsg) error {
	for _, t := range routes {
		t.held, t.booked = false, false
		if t.kind != kindRoute || t.proofH <= 0 || t.settling {
			continue
		}
		if e.bookedFor(ctx, now, cs, t) {
			continue
		}
		// judged by its own fee whatever the ledger booked of it, as the relayer judges it: a send the ledger booked
		// whose booking a reorganisation moved to a stranger's recording (E5-GS3) is held, and served again
		tt := *t
		tt.feeSpent = false
		f, err := e.dueFacts(ctx, sender, latest, []*outMsg{&tt}, now, 0, func(*outMsg) (int, int) { return t.leaf(t.proofH) })
		if err != nil {
			return err
		}
		t.held = f.FeesWei.Cmp(f.DeliveriesWei) < 0
	}
	e.sawAt(latest)
	return nil
}

// finalPast are the decision copies marked past whose number their HlAdmin's counter covers at the finalized block
// too (no reorg brings them back), and the copies to a retired HlAdmin of the line: the updater ends them, so its
// walk moves past them (the relayer ends its own with its report). A provider without the finalized block's state
// is no verdict: those stay.
func (e *outEnv) finalPast(ctx context.Context, ds []*outMsg) []*outMsg {
	var out []*outMsg
	var at *ethx.At
	last := map[common.Address]uint64{}
	for _, t := range ds {
		if !t.past {
			continue
		}
		rcpt, _ := ethx.AddressFrom32(t.msg.Msg.Recipient)
		if e.adminLine().IsRetired(rcpt) {
			out = append(out, t)
			continue
		}
		if at == nil {
			fin, err := e.eth.BlockAt(ctx, ethx.Finalized)
			if err != nil {
				return out
			}
			a := ethx.AtHash(fin.Hash)
			at = &a
		}
		n, ok := last[rcpt]
		if !ok {
			st, err := ethx.ReadAdmin(ctx, e.eth, rcpt, *at)
			if err != nil {
				continue
			}
			n = st.LastSeq
			last[rcpt] = n
		}
		if t.seq <= n {
			out = append(out, t)
		}
	}
	return out
}

// decisionGate is the rule for a decision's number against HlAdmin's last: "deliver" when it is the next expected,
// "wait" when an earlier number must come first, "past" when the number is recorded already.
func decisionGate(seq, last uint64) string {
	switch {
	case seq <= last:
		return "past"
	case seq == last+1:
		return "deliver"
	}
	return "wait"
}

// tokenAmount is a route message's token index (the gate's order: 0 GNOT, 1 BUBBLE) and amount.
func tokenAmount(t *outMsg) (token int, amount uint64, ok bool) {
	if t.rt == nil {
		return 0, 0, false
	}
	token, ok = TokenIndex(t.rt.Symbol)
	if !ok {
		return 0, 0, false
	}
	_, amount, _, err := hl.ParseTokenMessage(t.msg.Msg.Body)
	return token, amount, err == nil
}

// logOnce logs a line when it differs from the last one under its key.
type logOnce map[string]string

func (l logOnce) say(key, text string) {
	if l[key] == text {
		return
	}
	l[key] = text
	log.Print(text)
}

var errNoHeader = errors.New("no accepted header proves it yet")

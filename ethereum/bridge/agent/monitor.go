package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

const (
	monitorEvery   = 10 * time.Second
	monStallAfter  = 30 * time.Minute // no delivery with a message waiting
	monAgeAfter    = 2 * time.Hour    // a burn waiting this long for its credit on gno.land
	monOwnLagAfter = 5 * time.Minute  // the own node's head older than this: no verdict
	monCutFail     = 2 * time.Minute  // a failing condition stands this long before it alarms (and a header rule flags)
	monShortPolls  = 3                // a short right side alarms once it has stood this many polls
	monFlagGas     = 45_000_000
)

// ethFlagSender is the Ethereum watch key (ethx.Sender; a fake in tests).
type ethFlagSender interface {
	Send(ctx context.Context, c ethx.Call) (*types.Receipt, error)
	Address() common.Address
}

// Monitor is one of the two watch machines of eth/bridge/PLAN.md §5.9 (the Solana plan §5.9 carried over; one binary,
// run on each): every poll it takes the Ethereum snapshot from two agreeing providers (the finalized block F, the
// supplies and the client's state at F), then reads gno.land through its own node at a height at or above the
// client's trusted height and after the snapshot — or at the node's head when the client is past it inside an hour
// of blocks — rebuilds the burn index and the sends, and judges the supply identity of every route by the signers'
// rule, the diverted and the unindexed credits, the sources against one another (the third-source rule), the client's
// header record against the chain (the fork rule), every stage against gno.land's headers, every decision HlAdmin
// recorded against r/hlgov's, the stalls, the ages, the runways and the lags. Every Ethereum reading a flag rests on
// comes from both providers alike, pinned to F. It sets the watch key's flags — credits and payouts on gno.land and
// the gate's flag on an invariant break; the gate's and HlAdmin's on a header mismatch, a gap or a foreign decision;
// sends alone on gno.land while deliveries are paused — and alarms for everything else, so a pause can never feed its
// own flag. On Ethereum a flag is a transaction per object, so it is never re-asserted on a timer: it is set again
// only when it is found cleared, or when HlAdmin has recorded an unpause (final at F) that would clear it (§5.5).
type Monitor struct {
	Cfg   *Config
	State string
	Every time.Duration
	Alarm *Alarmer
	// FromBlock is where the scans of a fresh state start when given (0: the config's eth.deployBlock, else
	// finishSetup's block found on the chain).
	FromBlock uint64
	// ReadOnly makes the watch alarm only, never flag (hlwatch's status and watch: a guardian flags by hand).
	ReadOnly bool

	pair     ethx.Pair
	own      gnoside.Reader
	watch    gnoside.Caller // the own node, signing with the watch key
	sources  []gnoside.Reader
	ethWatch ethFlagSender // the Ethereum watch key, if this machine holds one
	now      func() time.Time

	mu       sync.Mutex
	state    monitorState
	status   monitorStatus
	seat     int // the watch key's seat in hlmailbox, 0 if none
	seen     map[string]time.Time
	short    map[string]int
	box      statusBox
	migs     map[string]*gnoside.Migration
	migErrs  map[string]error
	stale    string
	last     *monitorView // the last poll's readings, for hlwatch
	keysNext time.Time    // when the flag keys' floats are read next
	// setChecks are the validator sets read for small-order keys (E2-ED3), by set hash: each read once
	setChecks map[[32]byte]*setCheck
	// encodingFindings are this poll's differences between gno.land's encodings and the client's (encodings.go);
	// encodingNext is when the block below the head is rebuilt whole again
	encodingFindings []string
	encodingNext     time.Time
	// gnoFlagged are the gno flags this poll set (route + fn): two rules flagging one route in one poll send one
	// transaction (the mailbox read at the cut does not show the first yet)
	gnoFlagged map[string]bool
	// adminCursor is the live HlAdmin's cursor after the last read of its pending decisions (a scan a View catches up)
	adminCursor uint64
	// above is this poll's reading of the dispatches above the burn index (readAbove: E4-GT1's fast path, E5-GT1's
	// bound, E5-RT6's marks), from the block aboveFrom; read once a poll, when a rule asks
	above     *aboveIndex
	aboveFrom uint64
	// marksAt is when every uncredited delivered mark was last read (markRefresh), marksDoubt whether the last cut was in
	// doubt (markDoubt): either reads them all at the next poll, which otherwise reads a mark again only once hlmailbox's
	// delivered count moved since it was read unset (the integration lane I3, the Solana machines' E5-SF5)
	marksAt    time.Time
	marksDoubt bool
	// ownURL is the own node's address (Open: gno.rpc, or hlwatch's -node): the one-node record is of it (E6-GT2)
	ownURL string
}

// monSend is one gno.land → Ethereum send of a route, as the identity counts it in flight.
type monSend struct {
	Symbol     string `json:"symbol"`
	Amount     int64  `json:"amount"`
	Height     int64  `json:"height"`
	Delivered  bool   `json:"delivered"` // Ethereum's mailbox marked it delivered at a final block
	Seen       int64  `json:"seen"`
	ID         string `json:"id"`
	Refunded   bool   `json:"refunded,omitempty"`
	RefundRead int64  `json:"refundRead,omitempty"`
	WrittenOff bool   `json:"writtenOff,omitempty"`
}

// inFlight says whether a send still counts in flight: undelivered and not refunded.
func (s *monSend) inFlight() bool { return !s.Delivered && !s.Refunded }

// doubleMinted says whether a send was refunded on gno.land and delivered on Ethereum after all, with no write-off.
func (s *monSend) doubleMinted() bool { return s.Refunded && s.Delivered && !s.WrittenOff }

type monitorState struct {
	From             uint64             `json:"from"`
	Index            burnIndex          `json:"index"`
	sendIndex                           // the sends in flight (sends.go): "sends", "sendNext", "refundCounts"
	Floats           map[string]*Float  `json:"floats"`
	LastDelivery     int64              `json:"lastDelivery"`
	OddNode          bool               `json:"oddNode"`
	Record           headerRecord       `json:"record"`
	Stages           stageWatch         `json:"stages"`
	Decisions        decisionLog        `json:"decisions"`
	DecisionsChecked uint64             `json:"decisionsChecked"`
	SendsSet         bool               `json:"sendsSet"`
	EthFlags         map[string]ethFlag `json:"ethFlags"` // object → the watch key's last setFlag sent
	CapReached       map[string]bool    `json:"capReached,omitempty"`
	LineEnds         map[string]string  `json:"lineEnds,omitempty"`
	// NodeVersion is the own node's version as last read (/status), VersionChanged when it last changed (E3-AS3).
	NodeVersion    string `json:"nodeVersion,omitempty"`
	VersionChanged int64  `json:"versionChanged,omitempty"`
	// IndexBase is where the burn index begins against the deployment's messages (the Go trust audit's E3-GT4, as the
	// Solana monitors' checkpoint, G49): read once, the first time the credit rules run.
	IndexBase *indexBase `json:"indexBase,omitempty"`
	// Floor is the highest finalized block a poll took its Ethereum readings at (the Go trust audit's E3-GT9): the
	// record, the stages, the decisions, the sends' delivered marks were read there and stay, so a later poll at a lower
	// block would hold the client's counter below the record rebuilt (a false gap: the gate and HlAdmin flagged) or a
	// supply short of a mint the marks count. A finalized block never un-finalizes: below the floor is no verdict.
	Floor uint64 `json:"floor,omitempty"`
	// Pending are the BROKEN readings whose cut's order is not shown, by symbol (cutorder.go, E4-GT1): each judged once
	// the index's finalized block's time passes the reading's; Samples hlmailbox's delivered count as the cuts read it.
	Pending map[string]*pendingBreak `json:"pendingBreaks,omitempty"`
	Samples []deliverySample         `json:"deliverySamples,omitempty"`
	// Diverted are the diverted credits found, by message id, and Foreign the foreign decisions flagged, by HlAdmin's
	// number — each with its finding (E4-GT6): kept here, beside the marks that say they were judged, and judged again
	// every poll until cured (the claim voided, paid under a vote or gone; the decision cancelled, spent or executed),
	// alarmed as flags meanwhile and flagged again when found cleared — a restart or a second hlwatch run forgets none.
	Diverted map[string]string `json:"diverted,omitempty"`
	Foreign  map[string]string `json:"foreign,omitempty"`
	// AdminLow is each HlAdmin's cursor (by address): every number below it was read at a finalized block; AdminPending
	// the numbers below it still pending there, read again every poll (E4-CF4, as the executor's)
	AdminLow     map[string]uint64   `json:"adminLow,omitempty"`
	AdminPending map[string][]uint64 `json:"adminPending,omitempty"`
	// FinalSeen is the highest finalized block the pair answered and FinalSince when this machine first saw it (unix):
	// kept here, so a restart or the next `hlwatch status -state` never restarts a finality stall's clock (the plan red
	// team's E5-RT1)
	FinalSeen  uint64 `json:"finalSeen,omitempty"`
	FinalSince int64  `json:"finalSince,omitempty"`
	// Waiting is the reading of each route realm's waiting index, by realm (waitingClaims, E4-RL1), and WaitingPoll
	// numbers the polls that read them (a suspect is judged only at a poll that read it): kept here, the place and the
	// suspects together, so a restart and the next `hlwatch status -state` read on from where the last reading stopped,
	// never from place 1 (the conformance audit's E5-CF3, item 5)
	Waiting     map[string]*waitingIndex `json:"waiting,omitempty"`
	WaitingPoll uint64                   `json:"waitingPoll,omitempty"`
	// BeforeBase are the waiting claims a checkpointed index cannot judge (E3-GT4), by id, each with its line: alarmed
	// together as one (credit:unindexed-before-base, E5-CF3 item 4), each until its cure — its burn indexed, the claim
	// no longer waiting, or dropped by its reading
	BeforeBase map[string]string `json:"beforeBase,omitempty"`
	// DAOFloor is where hlwatch's reading of GovDAO's list may stop: every proposal numbered below its Below was final
	// when the list was last read whole (gnoside.ProposalFloor; the integration lane I3: past 250 proposals the list was
	// never read whole and the page never green, E5-GT2)
	DAOFloor *gnoside.ProposalFloor `json:"daoFloor,omitempty"`
	// Gno is what the own node answered so far, held to one node's answers (backends.go, E6-GT2): a finding refuses every
	// verdict until the operator gives the machine a node of its own
	Gno gnoSeen `json:"gno"`
	// NoVerdictSince is when the polls last stopped reaching a verdict (unix; 0 while they reach one): `cut` is raised at
	// flag level once there has been none for an hour (E6-GT1), across a restart and between `hlwatch status` runs
	NoVerdictSince int64 `json:"noVerdictSince,omitempty"`
}

// indexBase is where a burn index begins (E3-GT4). An index scanned from the deployment's first block holds every burn
// of it (Missed 0) and the forged-credit rule judges every claim. One started past it — a -from-block, or a deployBlock
// set late — misses the messages Ethereum's mailbox dispatched before its first block from the launch line on
// (Mailbox.nonce() at the block before it, less HlAdmin.launchNonce()): a burn among them is honest and unindexed, and
// its claim may wait for days (installments, a payouts flag). Such an index is a checkpoint, as the Solana monitors'
// (G49, AS3): a claim credited at or below FromHeight — the own node's head at the first poll that judged the claims —
// predates this index, and an unindexed claim credited after it may still be a burn below the index's first block
// credited late; either is alarmed (credit:unindexed-before-base, one alarm for all), never flagged. Ethereum keeps no account per
// message to seed the index from, so this stands for the index's life; the identity still judges every supply.
type indexBase struct {
	Nonce      uint32 `json:"nonce"`      // Mailbox.nonce() at the block before the index's first
	Line       uint32 `json:"line"`       // the launch line it was held to
	Missed     uint32 `json:"missed"`     // the deployment's messages dispatched before the index's first block
	FromHeight int64  `json:"fromHeight"` // the own node's head at the first poll that judged the claims (checkpointed only)
}

// checkpointed says whether the index misses messages of the deployment.
func (b *indexBase) checkpointed() bool { return b != nil && b.Missed > 0 }

// ethFlag is a setFlag the watch key sent on an object: its block (the flag's, until a read at a final block shows it).
type ethFlag struct {
	Block uint64 `json:"block"`
	Tx    string `json:"tx"`
	At    int64  `json:"at"`
	Why   string `json:"why"`
}

type monitorStatus struct {
	Identity   map[string]any    `json:"identity"`
	Cut        string            `json:"cut"`
	Sources    string            `json:"sources"`
	Floats     map[string]string `json:"floats"`
	Flags      []string          `json:"flags"`
	Waiting    map[string]int    `json:"waiting"`
	Dust       map[string]int    `json:"dust,omitempty"` // the uncredited burns below their route's fee, by symbol (E5-GS5: the relayers leave them)
	Client     map[string]any    `json:"client"`
	Gate       map[string]any    `json:"gate"`
	Admin      map[string]any    `json:"admin"`
	Decisions  map[string]any    `json:"decisions"`
	Migrations map[string]string `json:"migrations"`
	Polls      int               `json:"polls"`
	LastPoll   string            `json:"lastPoll"`
	LastErr    string            `json:"lastErr"`
	// ClaimsBeforeBase counts the waiting claims, credited at or below a checkpointed index's height, that the
	// forged-credit rule alarmed and did not judge this poll (E3-GT4).
	ClaimsBeforeBase int `json:"claimsBeforeBase"`
}

func (m *Monitor) load() error {
	if err := readJSON(m.State, &m.state); err != nil {
		return err
	}
	s := &m.state
	if err := s.Index.attachDust(dustPath(m.State)); err != nil { // the folded dust (E6-GT3)
		return err
	}
	s.sendIndex.init()
	if s.Floats == nil {
		s.Floats = map[string]*Float{}
	}
	if s.EthFlags == nil {
		s.EthFlags = map[string]ethFlag{}
	}
	if s.CapReached == nil {
		s.CapReached = map[string]bool{}
	}
	if s.LineEnds == nil {
		s.LineEnds = map[string]string{}
	}
	if s.Pending == nil {
		s.Pending = map[string]*pendingBreak{}
	}
	if s.Diverted == nil {
		s.Diverted = map[string]string{}
	}
	if s.Foreign == nil {
		s.Foreign = map[string]string{}
	}
	if s.AdminLow == nil {
		s.AdminLow = map[string]uint64{}
	}
	if s.AdminPending == nil {
		s.AdminPending = map[string][]uint64{}
	}
	if s.Waiting == nil {
		s.Waiting = map[string]*waitingIndex{}
	}
	if s.BeforeBase == nil {
		s.BeforeBase = map[string]string{}
	}
	for _, w := range s.Waiting {
		if w.Suspect == nil {
			w.Suspect = map[string]*waitingSuspect{}
		}
	}
	s.Decisions.init()
	s.Stages.init()
	return nil
}

func (m *Monitor) save() error {
	if m.State == "" {
		return nil
	}
	return saveWithDust(&m.state.Index, func() error { return writeJSON(m.State, m.state) })
}

func (m *Monitor) init() error {
	if m.now == nil {
		m.now = time.Now
	}
	if m.Every == 0 {
		m.Every = monitorEvery
	}
	if m.Alarm == nil {
		m.Alarm = &Alarmer{Source: "monitor"}
	}
	m.seen = map[string]time.Time{}
	m.short = map[string]int{}
	m.migs = map[string]*gnoside.Migration{}
	m.migErrs = map[string]error{}
	m.status.Identity = map[string]any{}
	m.status.Floats = map[string]string{}
	m.status.Waiting = map[string]int{}
	m.status.Migrations = map[string]string{}
	return m.load()
}

// Open dials what the monitor reads and signs with, from the config: the two providers, the own node, the watch keys,
// the other gno sources. A test sets them itself.
func (m *Monitor) Open(ctx context.Context) error {
	role := "monitor"
	if m.ReadOnly {
		role = "hlwatch"
	}
	if err := m.Cfg.CheckTrustPath(role); err != nil { // every rule's contracts named (E3-GT8)
		return err
	}
	if err := m.init(); err != nil {
		return err
	}
	if m.pair.A == nil {
		p, err := pairOf(ctx, m.Cfg)
		if err != nil {
			return err
		}
		m.pair = p
	}
	if m.own == nil {
		m.own = m.Cfg.SourceClient(m.Cfg.Gno.RPC)
		m.ownURL = m.Cfg.Gno.RPC
	}
	m.state.Gno.begin(m.ownURL, m.Cfg.Gno.Mailbox)
	if m.watch == nil && m.Cfg.Gno.WatchKey != "" && !m.ReadOnly {
		m.watch = m.Cfg.WatchClient()
	}
	if m.sources == nil {
		for _, s := range m.Cfg.Gno.Sources {
			m.sources = append(m.sources, m.Cfg.SourceClient(s))
		}
	}
	if len(m.sources) == 0 {
		log.Printf("monitor: WARNING no second gno source (gno.sources): the sources and fork rules want rpc.gno.land at least")
	}
	if m.ethWatch == nil && m.Cfg.Eth.WatchKey != "" && !m.ReadOnly {
		k, err := ethx.LoadKey(m.Cfg.Eth.WatchKey)
		if err != nil {
			return fmt.Errorf("monitor: the Ethereum watch key: %w", err)
		}
		var clients []*ethx.Client
		for _, p := range m.Cfg.Eth.Providers {
			c, err := ethx.Dial(ctx, p)
			if err != nil {
				return err
			}
			clients = append(clients, c)
		}
		state := ""
		if m.State != "" {
			state = m.State + ".eth-watch.json"
		}
		snd, err := ethx.NewSender(k, m.Cfg.EthChainID(), clients, m.Cfg.SendConfig(state))
		if err != nil {
			return err
		}
		m.ethWatch = snd
		log.Printf("monitor: Ethereum watch key %s", snd.Address().Hex())
	} else if m.Cfg.Eth.WatchKey == "" && !m.ReadOnly {
		log.Printf("monitor: WARNING no Ethereum watch key (eth.watchKey): the gate and HlAdmin cannot be flagged from here")
	}
	if m.Cfg.Gno.WatchKey == "" && !m.ReadOnly {
		log.Printf("monitor: WARNING no gno watch key (gno.watchKey): a break alarms, gno.land's flags cannot be set")
	}
	if m.state.LastDelivery == 0 {
		m.state.LastDelivery = m.now().Unix()
	}
	if m.state.From == 0 {
		if from := m.Cfg.ScanFrom(m.FromBlock); from > 0 {
			m.state.From = from
		} else {
			head, err := m.pair.A.BlockAt(ctx, ethx.Latest)
			if err != nil {
				return fmt.Errorf("monitor: the head, to find the deployment's first block: %w", err)
			}
			from, err := deployBlock(ctx, m.pair.A, m.Cfg, head.Number)
			if err != nil {
				return fmt.Errorf("monitor: %w (or set eth.deployBlock in the config)", err)
			}
			m.state.From = from
		}
	}
	return nil
}

// Run polls until ctx ends.
func (m *Monitor) Run(ctx context.Context) error {
	if err := m.Open(ctx); err != nil {
		return err
	}
	log.Printf("monitor: own node %s, sources %v, providers %s and %s, every %s, scans from block %d", ethx.Redact(m.Cfg.Gno.RPC), m.Cfg.Gno.Sources, m.pair.A.Provider, m.pair.B.Provider, m.Every, m.state.From)
	for {
		m.Step(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.Every):
		}
	}
}

// Step is one poll and its bookkeeping: the state saved, the status published.
func (m *Monitor) Step(ctx context.Context) error {
	err := m.poll(ctx)
	m.afterPoll(err)
	switch {
	case m.last != nil && m.last.Verdict:
		m.state.NoVerdictSince = 0
	case m.state.NoVerdictSince == 0:
		m.state.NoVerdictSince = m.now().Unix()
	}
	if serr := m.save(); serr != nil {
		log.Printf("monitor: state: %v", serr)
	}
	m.box.publish(m.buildStatus())
	return err
}

// afterPoll sorts a poll's outcome: a poll that fails is no verdict — an alarm once it has stood two minutes, the
// identity marked stale meanwhile; a clean poll clears both.
func (m *Monitor) afterPoll(err error) {
	if err == nil {
		m.stale = ""
		m.clearPersisted("monitor:no-verdict")
		return
	}
	log.Printf("monitor: %v", err)
	m.mu.Lock()
	m.status.LastErr = ethx.Redact(err.Error())
	m.mu.Unlock()
	m.stale = m.now().UTC().Format(time.RFC3339) + ": " + ethx.Redact(err.Error())
	m.persist("monitor:no-verdict", "no verdict: the poll fails ("+ethx.Redact(err.Error())+"); the identity, the stalls and the runways are not judged until it passes", monCutFail)
}

// finalAgeBlind is how old the pair's finalized block may be, by its own time against this machine's clock, for a
// reading at it to vouch for the Ethereum side (the plan red team's E5-RT1). Ethereum finalizes 64–96 blocks (13–19
// minutes) behind its head, so a finalized block past half an hour is a finality stall, or providers answering an old
// block: every Ethereum-side rule reads at it — the supplies, the client's header record, HlAdmin's decisions, the
// stages, the proxies' slots — and sees nothing newer, while the light client accepts headers on inclusion (§5.7) and
// deliveries keep minting. Such a reading is blind (said; hlwatch is never green over it) and alarmed
// eth:finality-stall, at flag level once the block is past the hour or has stood still for one (finalityStall).
const finalAgeBlind = 30 * time.Minute

// noteFinality judges the pair's finalized block's age and how long it has stood still (E5-RT1, E4-GT1): past
// finalAgeBlind the reading is blind and eth:finality-stall alarms; past finalityStall (an hour) — by its time or by
// standing still, the clock kept in the state — the alarm is raised at flag level. A BROKEN reading kept pending is
// judged only once the index's finalized block's time passes it, so a stall holds it unjudged too (alarmed as
// identity:<token>:pending); no flag rests on one provider's word.
func (m *Monitor) noteFinality(F *ethx.Block) {
	now := m.now()
	s := &m.state
	if F.Number > s.FinalSeen {
		s.FinalSeen, s.FinalSince = F.Number, now.Unix()
	}
	age := now.Sub(time.Unix(int64(F.Time), 0))
	var stood time.Duration // how long the highest block seen has stood, read across restarts (none before one is seen)
	if s.FinalSince != 0 {
		stood = now.Sub(time.Unix(s.FinalSince, 0))
	}
	if age <= finalAgeBlind && stood <= finalityStall {
		m.Alarm.Clear("eth:finality-stall")
		return
	}
	text := fmt.Sprintf("the providers' finalized block %d is %s old by its own time (%s); the highest this machine saw, %d, since %s: Ethereum's finality stalled, or this machine's providers answer an old block — every Ethereum-side rule reads at it (the supplies, the client's header record, HlAdmin's decisions, the stages, the proxies' slots) and sees nothing newer, while the light client accepts headers on inclusion and deliveries mint (E5-RT1); the burn index and every pending reading wait on it (E4-GT1)",
		F.Number, age.Round(time.Second), time.Unix(int64(F.Time), 0).UTC().Format(time.RFC3339), s.FinalSeen, time.Unix(s.FinalSince, 0).UTC().Format(time.RFC3339))
	m.blind("every Ethereum-side rule: " + text)
	if age > finalityStall || stood > finalityStall {
		m.Alarm.Flag("eth:finality-stall", "PAST THE HOUR: "+text+" — a guardian's flag and hold are the brake meanwhile (§9)")
		return
	}
	m.Alarm.Alarm("eth:finality-stall", text)
}

// judgeOwnHead is the monitor's reading of its own node's head against the snapshot: no verdict while the node is
// catching up, while its head is not after the snapshot (the cut's order), or while its head is older than the lag
// allowance — a node that just came back serves an old head, which is no reading of the chain.
func judgeOwnHead(head gno.Head, snapAt, now time.Time) (ok bool, why string) {
	switch {
	case head.CatchingUp:
		return false, fmt.Sprintf("own node is catching up (head %d)", head.Height)
	case !head.Time.After(snapAt):
		return false, fmt.Sprintf("own node's head %d (%s) is not after the Ethereum snapshot (%s)", head.Height, head.Time.UTC().Format(time.RFC3339), snapAt.UTC().Format(time.RFC3339))
	case now.Sub(head.Time) > monOwnLagAfter:
		return false, fmt.Sprintf("own node's head %d is %s old (over %s): no reading of the chain", head.Height, now.Sub(head.Time).Round(time.Second), monOwnLagAfter)
	}
	return true, ""
}

// errNoVerdict is a poll that reached no verdict for a reason already alarmed (the order, the node's head).
var errNoVerdict = errors.New("no verdict")

// poll is one round, in §5.9's order.
func (m *Monitor) poll(ctx context.Context) error {
	m.mu.Lock()
	m.status.Polls++
	m.status.LastPoll = m.now().UTC().Format(time.RFC3339)
	m.status.LastErr = ""
	m.mu.Unlock()
	view := &monitorView{At: m.now()}
	m.last = view
	m.gnoFlagged = map[string]bool{}
	m.above = nil

	// 1. The Ethereum snapshot, both providers agreeing: F, the supplies, the client's state at F.
	snap, err := takeEthSnapshot(ctx, m.pair, m.Cfg, m.now())
	if err != nil {
		key := "eth:providers"
		if ethx.IsNoState(err) {
			key = "eth:no-state"
		}
		m.persist(key, "the Ethereum snapshot failed: "+ethReadText(err), monCutFail)
		return fmt.Errorf("the Ethereum snapshot: %w", err)
	}
	m.clearPersisted("eth:providers")
	m.clearPersisted("eth:no-state")
	view.Snap = snap
	m.noteFinality(&snap.Block)

	// 2. gno.land's head, waited for until it is after the snapshot; catching up, stale or not after it is no
	// verdict; the client's trusted height T against it.
	head, err := gnoHeadAfter(ctx, m.own, snap.At)
	if err != nil {
		m.persist("gno:own", "own node: "+err.Error(), monCutFail)
		return fmt.Errorf("own node: %w", err)
	}
	view.Head = head
	if !head.CatchingUp {
		m.state.Gno.head(head.Height, "the own node's head after the Ethereum snapshot", m.now())
	}
	if ok, why := judgeOwnHead(head, snap.At, m.now()); !ok {
		m.setCut("no verdict: " + why)
		m.persist("gno:own", why, monCutFail)
		if !head.CatchingUp && m.now().Sub(head.Time) > monOwnLagAfter {
			m.Alarm.Alarm("gno:own-lag", fmt.Sprintf("own node's head %d is %s old (over %s): %s", head.Height, m.now().Sub(head.Time).Round(time.Second), monOwnLagAfter, why))
		}
		return nil
	}
	m.clearPersisted("gno:own")
	m.Alarm.Clear("gno:own-lag")
	// the floor (E3-GT9): a finalized block below the highest one a poll already read at is a provider stepped back
	// (a lagging backend, or a liar) — no verdict, alarmed once it has stood two minutes; never a reading
	if why := belowFloor(snap.Block.Number, m.state.Floor); why != "" {
		m.setCut("no verdict: " + why)
		m.persist("eth:finalized-floor", why, monCutFail)
		return nil
	}
	m.clearPersisted("eth:finalized-floor")
	m.state.Floor = snap.Block.Number

	// 3–6, the gno side and its verdicts; then 7–9, the Ethereum side, whatever the gno side's cut gave (the Go trust
	// audit's E6-GT1): the record, the stages, the decisions and the proxies' slots read F and the own node's headers,
	// never hlmailbox's counts, so a cut kept moving — a delivery landing in every window — blinds none of them.
	g := &gnoReading{}
	err = m.gnoSide(ctx, snap, head, view, g)
	m.ethSide(ctx, snap, g, head.Height, view)
	return err
}

// gnoReading is what a poll's gno side read for the Ethereum side's rules: hlmailbox and the routes as the cut's window
// read them first (nil when they did not read).
type gnoReading struct {
	mb     *gnoside.Mailbox
	routes map[string]*gnoside.Route
}

// gnoSide is a poll's gno side once the snapshot, the own node's head and the floor stand: the ahead rule, the cut's
// order, the sources (3), the cut's window (4), then the burn index, the sends and the marks read after the window
// (5), and the verdicts (6).
func (m *Monitor) gnoSide(ctx context.Context, snap *ethSnapshot, head gno.Head, view *monitorView, g *gnoReading) error {
	if cs := snap.Client; cs != nil && head.Height < cs.TrustedHeight {
		T := uint64(cs.TrustedHeight)
		allowance := m.Cfg.AheadAllowance()
		if T-uint64(head.Height) > allowance {
			second, secondOK := gnoSecondHead(ctx, m.sources)
			v, why := judgeAhead(T, uint64(head.Height), second, secondOK, allowance)
			if v == aheadStop {
				if m.persisted("header:ahead", monCutFail) {
					m.flagEth(ctx, "header:mismatch", "header mismatch: the client is past the chain: "+why, true)
				} else {
					m.Alarm.Alarm("header:ahead", "the client is past the chain (flagged once it has stood "+monCutFail.String()+"): "+why)
				}
			} else {
				m.setCut("no verdict: " + why)
				m.persist("gno:own", why, monCutFail)
				return nil
			}
		} else {
			m.clearPersisted("header:ahead")
		}
	} else {
		m.clearPersisted("header:ahead")
	}
	cut := Cut{EthBlock: snap.Block.Number, EthTime: snap.At, GnoHeight: head.Height, GnoTime: head.Time}
	if ok, why := cut.Holds(); !ok {
		m.setCut("no verdict: " + why)
		m.cutAlarm("the order never holds: " + why)
		return nil
	}
	H := head.Height

	// 3. The sources against one another at H (the third-source rule); and every key of the sets the bridge trusts
	// or is about to — the client's and gno.land's, current and announced — that anyone can sign for (E2-ED3: an
	// alarm, never a refusal).
	m.judgeSources(ctx, H, head.Hash)
	m.checkKeys(ctx, H, snap.Client)
	m.checkEncodings(ctx, H) // and the client's encodings against gno.land's, with the node's version (E3-AS3)

	// 4. The cut's window (cutorder.go): hlmailbox, the routes and their lines of migrations, the own node's head,
	// hlmailbox again — taken again at once while it moves, and nothing else inside it.
	w, err := m.takeWindow(ctx, H, snap)
	if w.first != nil {
		g.mb, view.Mailbox = w.first, w.first
	}
	if err != nil {
		return err
	}
	g.routes, view.Routes = w.routes, w.routes // every route read
	mb, routes, migs := w.first, w.routes, m.migs

	// 5. After the window: the burn index (both providers, to their finalized block read after the gno reads), the sends,
	// every uncredited burn's delivered mark — each placed in the window's state by its height — and, under a BROKEN
	// reading with a delivery unplaced, what the providers show above the index.
	F2, err := m.pair.FinalizedWithin(ctx, ethx.FinalizedApart)
	if err != nil {
		if errors.Is(err, ethx.ErrDisagree) {
			// alarmed once it has stood two minutes, as the snapshot's: a finalized block jumping several epochs at
			// once (finality resuming) read by two honest providers seconds apart is one poll's disagreement
			m.persist("eth:providers", "the finalized block after the gno read: "+ethReadText(err), monCutFail)
		} else {
			m.ethErr("the finalized block after the gno read", err)
		}
		return err
	}
	if F2.Number < snap.Block.Number {
		F2 = &snap.Block
	}
	// the scan follows every token's line (a burn addressed to a successor a migration named is the token's own): it
	// waits while a line is unread, since a burn to a successor it did not know would be passed over for good
	if len(m.migErrs) > 0 {
		var why []string
		for _, sym := range sortedSymbols(m.migErrs) {
			why = append(why, sym+": "+m.migErrs[sym].Error())
		}
		m.setCut("no verdict: the burn scan waits for every token's line of migrations (" + strings.Join(why, "; ") + ")")
		return nil
	}
	_, done, err := m.state.Index.catchUp(ctx, m.pair, m.Cfg.Eth.Mailbox, m.Cfg.Eth.Admin, m.Cfg.Eth.Domain, mb.Domain, burnRoutes(m.Cfg, migs), m.state.From, F2.Number, m.now())
	if err != nil {
		m.ethErr("the burn index", err)
		m.persist("scan:burns", "the burn scan fails (the identity and the credit rules wait): "+ethReadText(err), monCutFail)
		return err
	}
	m.clearPersisted("scan:burns")
	if !done {
		m.setCut(fmt.Sprintf("no verdict: the burn index is at block %d, behind the finalized block %d (catching up, %d blocks a poll)", m.state.Index.Cursor, F2.Number, burnScanMax))
		m.behind(fmt.Sprintf("the burn index is read to block %d of the finalized block %d", m.state.Index.Cursor, F2.Number))
		return nil
	}
	if !w.sends {
		if err := m.sends(ctx, mb, routes, H, snap); err != nil {
			m.ethErr("the sends", err)
			return err
		}
	}
	// the delivered marks of the burns not credited yet, each read again only once hlmailbox's delivered count moved since
	// it was read unset — the burns nobody credits (the dust below a route's fee the relayers leave, E5-GS5) were read
	// every poll, for good — and all of them while the last cut was in doubt and every markRefresh; d is the count read
	// last before them (the window's second read), Hc the window's head they are placed by
	d, Hc := w.move.again.Delivered, w.height
	allMarks := m.marksDoubt || m.now().Sub(m.marksAt) >= markRefresh
	credited, markedTop, _, err := m.state.Index.markCredited(ctx, m.own, m.Cfg.Gno.Mailbox, H, d, allMarks)
	if err != nil {
		return err
	}
	if allMarks {
		// and the folded dust, whose marks a moving count does not read (E6-GT3)
		more, top, err := m.state.Index.readFolded(ctx, m.own, m.Cfg.Gno.Mailbox, H, d, nil)
		if err != nil {
			return err
		}
		credited += more
		markedTop = max(markedTop, top)
		m.marksAt = m.now()
	}
	// a poll that stops before it places its deliveries leaves its marks in doubt: the next poll reads them all (the review
	// of I3's follow-up 1, finding 1: a stale read in a poll a failed read cut short stood into the next)
	m.marksDoubt = true
	if credited > 0 {
		m.state.LastDelivery = m.now().Unix()
	}
	inflightIn, after := m.state.Index.inflight(snap.Block.Number, Hc)
	inflightOut := m.state.sendIndex.inflight()
	// a BROKEN reading waits while a delivery the index cannot place may be an honest burn above it (E4-GT1): then what
	// the providers show above the index — the most such a lag can carry (E5-GT1) — and the credited marks of those burns
	// are read, placed by the window's head as the index's are
	unplaced := d - m.state.Index.placedBy(Hc)
	broken := m.anyBroken(routes, migs, H, snap, inflightOut, inflightIn)
	if broken {
		// what one stale unset mark of a credited burn reads as — its amount in flight and in owed alike — is a BROKEN
		// reading, so every uncredited mark is read again before it is judged (the review of I3's follow-up 1, finding 2:
		// one stale read flagged at once past E5-GT1's bound, or as a pending break confirmed later though the next poll
		// held). On one node that read gives what the first gave; read again whatever is unplaced (round 6): on an endpoint
		// answering from two backends the count can be as stale as the mark (E6-GT2's shape (b): nothing unplaced, nothing
		// read again, flagged at once), and a mark read set at the window's head is then held to the count by the backends
		// rule below
		more, top, _, err := m.state.Index.markCredited(ctx, m.own, m.Cfg.Gno.Mailbox, H, d, true)
		if err != nil {
			return err
		}
		if more > 0 {
			m.state.LastDelivery = m.now().Unix()
		}
		markedTop = max(markedTop, top)
		inflightIn, after = m.state.Index.inflight(snap.Block.Number, Hc)
		unplaced = d - m.state.Index.placedBy(Hc)
		broken = m.anyBroken(routes, migs, H, snap, inflightOut, inflightIn)
		// a token with folded dust still BROKEN with a delivery unplaced: what a delivered folded burn reads as (its amount
		// in owed and in the folded sum) — its folded marks read too, before it is judged (E6-GT3)
		if syms := m.foldedBroken(routes, migs, H, snap, inflightOut, inflightIn); broken && unplaced > 0 && len(syms) > 0 {
			more, top, err := m.state.Index.readFolded(ctx, m.own, m.Cfg.Gno.Mailbox, H, d, syms)
			if err != nil {
				return err
			}
			if more > 0 {
				m.state.LastDelivery = m.now().Unix()
			}
			markedTop = max(markedTop, top)
			inflightIn, after = m.state.Index.inflight(snap.Block.Number, Hc)
			unplaced = d - m.state.Index.placedBy(Hc)
			broken = m.anyBroken(routes, migs, H, snap, inflightOut, inflightIn)
		}
	}
	broken = broken && unplaced > 0
	if broken {
		m.dispatchedAbove(ctx, F2.Number, mb.Domain).markAbove(ctx, m.own, m.Cfg.Gno.Mailbox, H)
	}
	m.marksDoubt = markDoubt(unplaced, broken, len(m.state.Pending))
	// one node (E6-GT2): every mark read at or below a head read after it, and the deliveries placed at most hlmailbox's
	// count read after the window's head — else the endpoint answers from more than one backend
	if err := m.holdToOneNode(ctx, creditTop(markedTop, m.above), d, Hc); err != nil {
		return err
	}
	if text := m.state.Gno.text(); text != "" {
		m.setCut("no verdict: " + text)
		m.Alarm.Flag(backendsKey, text)
		return nil
	}
	m.Alarm.Clear(backendsKey)

	// 6. The verdicts. A cut that moved is no verdict, but its BROKEN readings are acted on, and the rules that judge
	// earlier readings or single credits run whatever the cut did (E6-GT1).
	if w.move.why != "" {
		why := w.move.why + ": the node answers its latest state, so the cut is taken again next poll"
		m.setCut("no verdict: " + why)
		m.cutAlarm(why)
		if !w.move.counters {
			m.brokenAcrossAMove(ctx, mb, routes, migs, H, snap, inflightOut, inflightIn, Hc, unplaced)
		}
		return m.creditRules(ctx, mb, H, F2, unplaced)
	}
	cut.CreditedAfterBlock = after
	if ok, why := cut.Holds(); !ok {
		m.setCut("no verdict: " + why)
		m.cutAlarm("the order never holds: " + why)
		return m.creditRules(ctx, mb, H, F2, unplaced)
	}
	m.clearPersisted("cut")
	m.setCut(fmt.Sprintf("finalized block %d at %s, then height %d at %s", cut.EthBlock, cut.EthTime.UTC().Format(time.RFC3339), cut.GnoHeight, cut.GnoTime.UTC().Format(time.RFC3339)))
	view.Verdict = true
	// the deliveries the index cannot place (E4-GT1): a BROKEN reading waits while an honest burn above the index may be
	// among them; the pending readings are judged first, with the lag the index shows now
	order := m.cutOrder(mb, F2, Hc, w.time)
	m.judgePending(ctx, mb, F2)

	// the identity of every token — over the line's summed counters while a migration is open — and the diverted and
	// unindexed credits
	m.judgeIdentities(ctx, mb, routes, migs, H, snap, inflightOut, inflightIn, order)
	if err := m.divertedCredits(ctx, mb, H); err != nil {
		return err
	}
	if err := m.unindexedCredits(ctx, mb, H, F2, order.Unplaced); err != nil {
		return err
	}
	// the burns dust for an hour leave the state for the folded dust's file (E6-GT3)
	if n := m.state.Index.foldDust(m.now(), snap.Block.Number, d, realmFee(routes, migs), nil); n > 0 {
		log.Printf("monitor: %d burn(s) below their realm's fee for an hour folded out of the state (%d folded in all)", n, len(m.state.Index.folded()))
	}
	return nil
}

// foldedBroken are the configured tokens whose identity reads BROKEN and that hold folded dust (dust.go): their folded
// marks are read before they are judged.
func (m *Monitor) foldedBroken(routes map[string]*gnoside.Route, migs map[string]*gnoside.Migration, H int64, snap *ethSnapshot, inflightOut, inflightIn map[string]int64) map[string]bool {
	folded := m.state.Index.foldedSum()
	out := map[string]bool{}
	for _, r := range m.Cfg.Routes {
		if r.Token == "" || m.migErrs[r.Symbol] != nil || folded[r.Symbol][1] == 0 {
			continue
		}
		if v, _ := identityOf(r, routes[r.Symbol], migs[r.Symbol], H, snap.Supplies[r.Symbol], snap.Block.Number, inflightOut[r.Symbol], inflightIn[r.Symbol]).Verdict(); v == Broken {
			out[r.Symbol] = true
		}
	}
	return out
}

// creditRules are the rules a poll runs whatever its cut did once its index and marks are read (E6-GT1): the pending
// readings, each an earlier cut's, judged with the lag the index shows now (every honest burn credited at or below a
// reading's height has its mark read once hlmailbox's count moved past it), and the credits one by one — a diverted
// claim, a waiting claim no burn of the index carries (unplaced bounds how many are looked for: hlmailbox's last count
// less the deliveries the index places, at least the cut's own).
func (m *Monitor) creditRules(ctx context.Context, mb *gnoside.Mailbox, H int64, F2 *ethx.Block, unplaced int) error {
	m.judgePending(ctx, mb, F2)
	if err := m.divertedCredits(ctx, mb, H); err != nil {
		return err
	}
	return m.unindexedCredits(ctx, mb, H, F2, unplaced)
}

// ethSide is a poll's Ethereum side and its bookkeeping (§5.9's steps 7–9), run once the snapshot, the own node's head
// and the floor stand, whatever the gno side's cut gave (E6-GT1): the record, the stages, the decisions, the proxies'
// slots, the pause mirror and the caps against the locks; the stalls and ages; the runways and the flag keys' floats.
func (m *Monitor) ethSide(ctx context.Context, snap *ethSnapshot, g *gnoReading, H int64, view *monitorView) {
	// 7. The Ethereum side: the record, the stages, the decisions, the pause mirror, the caps against the locks.
	m.ethChecks(ctx, snap, g.mb, g.routes, H, view)

	// 8. Stalls and ages (a burn below the fee of the realm that would credit it is dust: the routes and every line read
	// this poll, or the fee is not known); 9. runways, and the flag keys' floats.
	if g.routes != nil && len(m.migErrs) == 0 {
		m.stalls(g.routes)
	} else {
		m.blind("the stalls and ages: the routes or a token's line of migrations did not read this poll (a burn below the fee of the realm that would credit it is dust, waiting for nobody)")
	}
	m.runways(ctx)
	m.flagKeys(ctx, snap)

	if g.mb == nil {
		return
	}
	m.mu.Lock()
	m.status.Flags = nil
	for _, path := range sortedSymbols(g.mb.Routes) {
		for _, f := range g.mb.Routes[path].Flags {
			m.status.Flags = append(m.status.Flags, path+": "+f)
		}
	}
	m.mu.Unlock()
}

// cutWindow is one taking of the cut's window (cutorder.go): hlmailbox as first read, the routes, the own node's head
// read after them and its block's time (the height every mark is placed by), whether the sends were read inside it,
// and what hlmailbox's second read found.
type cutWindow struct {
	first  *gnoside.Mailbox
	routes map[string]*gnoside.Route
	height int64
	time   int64
	sends  bool
	move   cutMove
}

// takeWindow takes the cut's window, again at once while it moves (cutTries in all): the next block's commit is the next
// a delivery or a dispatch can land at. The lines of migrations are read with the routes (readMigrations, the monitor's
// m.migs); the sends are read inside the window only while a token's can be refunded (their refund marks are held to
// the lock read in it, as the counters are). It answers the last taking, moved or not.
func (m *Monitor) takeWindow(ctx context.Context, H int64, snap *ethSnapshot) (*cutWindow, error) {
	var w *cutWindow
	for try := 1; try <= cutTries; try++ {
		w = &cutWindow{routes: map[string]*gnoside.Route{}}
		mb, err := gnoside.MailboxAt(ctx, m.own, m.Cfg.Gno.Mailbox, H)
		if err != nil {
			return w, fmt.Errorf("hlmailbox: %w", err)
		}
		m.state.Gno.mailbox(mb, "hlmailbox's first read", m.now())
		w.first = mb
		m.seat = monSeatOf(mb.Seats, m.Cfg.Gno.WatchAddress)
		for _, r := range m.Cfg.Routes {
			rj, err := gnoside.RouteAt(ctx, m.own, r.Gno, H)
			if err != nil {
				return w, fmt.Errorf("%s: %w", r.Symbol, err)
			}
			w.routes[r.Symbol] = rj
		}
		migs := m.readMigrations(ctx, w.routes, mb, H)
		if refundable(m.Cfg, w.routes, migs) {
			w.sends = true
			if err := m.sends(ctx, mb, w.routes, H, snap); err != nil {
				m.ethErr("the sends", err)
				return w, err
			}
		}
		w.height, w.time, err = headAfterCut(ctx, m.own)
		if err != nil {
			return w, fmt.Errorf("own node's head after the cut: %w", err)
		}
		m.state.Gno.head(w.height, "the own node's head inside the cut's window", m.now())
		w.move, err = cutMoved(ctx, m.own, m.Cfg, H, mb, w.routes, migs)
		if err != nil {
			return w, fmt.Errorf("the cut read again: %w", err)
		}
		m.state.Gno.mailbox(w.move.again, "hlmailbox's second read", m.now())
		if w.move.why == "" {
			break
		}
		log.Printf("monitor: the cut moved (taking %d of %d): %s", try, cutTries, w.move.why)
	}
	return w, nil
}

// refundable says whether a configured token's sends can be refunded (a successor's imported sends, or a line of
// migrations open): their refund marks are read inside the cut's window, where the lock is.
func refundable(cfg *Config, routes map[string]*gnoside.Route, lines map[string]*gnoside.Migration) bool {
	for _, r := range cfg.Routes {
		if rj, ok := routes[r.Symbol]; ok && r.Token != "" && gnoside.Refundable(rj, lines[r.Symbol]) {
			return true
		}
	}
	return false
}

// holdToOneNode holds a poll's reads to one node's (E6-GT2, backends.go): the head read after the marks to every head
// read before it and above the highest mark read, and the deliveries the index places at the window's head to
// hlmailbox's count read after it.
func (m *Monitor) holdToOneNode(ctx context.Context, top int64, delivered int, Hc int64) error {
	m.state.Gno.placed(m.state.Index.placedBy(Hc), delivered, Hc, m.now())
	end, err := m.own.Status(ctx)
	if err != nil {
		return fmt.Errorf("own node's head after the marks: %w", err)
	}
	m.state.Gno.head(end.Height, "the own node's head after the marks", m.now())
	m.state.Gno.marks(top, end.Height, m.now())
	return nil
}

// cutFlagAfter is how long a monitor may go without a verdict before `cut` is raised at flag level (E6-GT1): a cut kept
// moving, or an order that never holds, an hour on end is the watch judging nothing.
const cutFlagAfter = time.Hour

// cutAlarm raises `cut` for a cut that moved or an order that does not hold: once it has stood two minutes, and at
// flag level once the monitor has had no verdict for an hour (NoVerdictSince, kept in the state).
func (m *Monitor) cutAlarm(why string) {
	if since := m.state.NoVerdictSince; since != 0 && m.now().Sub(time.Unix(since, 0)) >= cutFlagAfter {
		m.Alarm.Flag("cut", fmt.Sprintf("NO VERDICT FOR AN HOUR (none since %s): %s — the identity is judged only across a moving cut's breaks, the pending readings and the credits one by one; the Ethereum side's rules still run", time.Unix(since, 0).UTC().Format(time.RFC3339), why))
		return
	}
	m.persist("cut", why, monCutFail)
}

// brokenAcrossAMove judges a cut that moved (a delivery or a dispatch landing in every taking of its window): every
// token whose reading is BROKEN with no delivery unplaced by hlmailbox's last count, or past the most an honest lag can
// carry (E5-GT1), is a break whatever landed — flagged as on a held cut (E6-GT1; cutorder.go's window shows why a
// delivery or a dispatch inside the window only takes from the right side). Anything else waits for a cut that holds.
func (m *Monitor) brokenAcrossAMove(ctx context.Context, mb *gnoside.Mailbox, routes map[string]*gnoside.Route, migs map[string]*gnoside.Migration, H int64, snap *ethSnapshot, inflightOut, inflightIn map[string]int64, Hc int64, unplaced int) {
	for _, r := range m.Cfg.Routes {
		if r.Token == "" || m.migErrs[r.Symbol] != nil {
			continue
		}
		id := identityOf(r, routes[r.Symbol], migs[r.Symbol], H, snap.Supplies[r.Symbol], snap.Block.Number, inflightOut[r.Symbol], inflightIn[r.Symbol])
		v, text := id.Verdict()
		if v != Broken {
			continue
		}
		if unplaced > 0 {
			most, ok := lagMost(r.Symbol, id.Block, Hc, &m.state.Index, m.above)
			if !ok || excessOf(id) <= most {
				continue
			}
			text = beyondLagText(text, excessOf(id), most, id.Block, m.above)
		}
		text += " — read across a cut that moved (a delivery or a dispatch landing inside the window takes from the right side only: E6-GT1)"
		m.setIdentity(r.Symbol, id, v.String(), text)
		m.short[r.Symbol] = 0
		m.breakFlags(ctx, m.liveRoute(r), mb, "identity:"+r.Symbol, text)
	}
}

// ethErr logs an Ethereum reading that failed; a disagreement between the providers is the alarm eth:providers (no
// flag rests on one provider's word; the rule is skipped this poll), a provider without the state is eth:no-state.
func (m *Monitor) ethErr(what string, err error) {
	log.Printf("monitor: %s: %v", what, err)
	switch {
	case errors.Is(err, ethx.ErrDisagree):
		m.Alarm.Alarm("eth:providers", what+": "+ethReadText(err)+" (the rule is skipped until they agree)")
	case ethx.IsNoState(err):
		m.Alarm.Alarm("eth:no-state", what+": "+ethReadText(err))
	}
}

// persist raises an alarm only once a failing condition has stood for d (a hiccup never alarms).
func (m *Monitor) persist(key, text string, d time.Duration) {
	if m.persisted(key, d) {
		m.Alarm.Alarm(key, text)
	}
}

// persisted says whether a condition has stood for d (its first sighting is recorded now).
func (m *Monitor) persisted(key string, d time.Duration) bool {
	first, ok := m.seen[key]
	if !ok {
		m.seen[key] = m.now()
		return d <= 0
	}
	return m.now().Sub(first) >= d
}

// clearPersisted ends a persisted condition: the alarm and its first sighting alike.
func (m *Monitor) clearPersisted(key string) {
	m.Alarm.Clear(key)
	delete(m.seen, key)
}

func (m *Monitor) setCut(s string) {
	m.mu.Lock()
	m.status.Cut = s
	m.mu.Unlock()
}

// blind names, on this poll's reading, a rule that could not run and why; behind a scan not yet at the finalized block.
// hlwatch's page is never green over either (E3-GT3, E3-GT10): a rule that did not run vouches for nothing.
func (m *Monitor) blind(text string) {
	if m.last != nil {
		m.last.Blind = append(m.last.Blind, text)
	}
}

func (m *Monitor) behind(text string) {
	if m.last != nil {
		m.last.Behind = append(m.last.Behind, text)
	}
}

// scanCursors are the places of the scans a poll advances (the burn index, the header record, the stages, the
// decisions recorded and executed, HlAdmin's numbers read for pending decisions, the routes' waiting indexes read): a
// View that catches up polls again while one moves.
func (m *Monitor) scanCursors() [7]uint64 {
	var waiting uint64 // the places read of every waiting index still read through a backlog
	for _, w := range m.state.Waiting {
		if w.More {
			waiting += uint64(w.Next - 1)
		}
	}
	return [7]uint64{m.state.Index.Cursor, m.state.Record.Cursor, m.state.Stages.Cursor, m.state.Decisions.Cursor, m.state.Decisions.ExecCursor, m.adminCursor, waiting}
}

func (m *Monitor) setIdentity(sym string, id Identity, verdict, text string) {
	m.mu.Lock()
	entry := map[string]any{"lock": id.Lock, "supply": id.Supply, "owed": id.Owed, "voided": id.Voided, "lost": id.Lost,
		"right": id.Right(), "gap": id.Gap(), "inflightOut": id.InflightOut, "inflightIn": id.InflightIn, "block": id.Block, "height": id.Height,
		"verdict": verdict, "ok": verdict == Holds.String(), "text": text}
	if mig := m.migs[sym]; mig != nil {
		entry["migration"] = mig.Text()
	}
	m.status.Identity[sym] = entry
	m.mu.Unlock()
	if m.last != nil {
		if m.last.Identities == nil {
			m.last.Identities = map[string]Identity{}
			m.last.IdentityText = map[string]string{}
		}
		m.last.Identities[sym] = id
		m.last.IdentityText[sym] = verdict + ": " + text
	}
}

// readMigrations reads every token's open migration at a height: the line each token is judged over, a successor
// mid-import alarmed; a token whose line could not be read is no verdict this poll, the rest runs.
func (m *Monitor) readMigrations(ctx context.Context, routes map[string]*gnoside.Route, mb *gnoside.Mailbox, H int64) map[string]*gnoside.Migration {
	m.migs, m.migErrs = map[string]*gnoside.Migration{}, map[string]error{}
	read := gnoside.RouteReaderAt(m.own, H)
	for _, r := range m.Cfg.Routes {
		mig, err := gnoside.MigrationOf(ctx, read, r.Symbol, r.Gno, r.Predecessor, routes[r.Symbol], mb)
		if err != nil {
			m.migErrs[r.Symbol] = err
			continue
		}
		if mig != nil {
			m.migs[r.Symbol] = mig
		}
	}
	m.mu.Lock()
	m.status.Migrations = map[string]string{}
	for sym, mig := range m.migs {
		m.status.Migrations[sym] = mig.Text()
	}
	for sym, err := range m.migErrs {
		m.status.Migrations[sym] = "NOT READ: " + err.Error()
	}
	m.mu.Unlock()
	for _, r := range m.Cfg.Routes {
		key := "route:importing:" + r.Symbol
		if mig := m.migs[r.Symbol]; mig.Importing() {
			m.Alarm.Alarm(key, fmt.Sprintf("%s's successor %s is IMPORTING %s's pages (%d so far): credits are refused there until the last page is in, the token is judged over the line's summed counters, and a burn addressed to a migrated realm waits", r.Symbol, mig.Successor, mig.Predecessor, mig.Succ.Imported))
		} else if m.migErrs[r.Symbol] == nil {
			m.Alarm.Clear(key)
		}
		lineKey := "route:line:" + r.Symbol
		if err := m.migErrs[r.Symbol]; err != nil {
			m.persist(lineKey, fmt.Sprintf("%s's line of migrations cannot be followed from the configured realm %s (%v): the token has no verdict — past sixteen migrations edit routes[].gno in the config to the live realm; a loop is a forged line, read the realms", r.Symbol, r.Gno, err), monCutFail)
			continue
		}
		m.clearPersisted(lineKey)
		end := ""
		if mig := m.migs[r.Symbol]; mig != nil {
			end = mig.Successor
		}
		changeKey := "route:successor-changed:" + r.Symbol
		if was, known := m.state.LineEnds[r.Symbol]; known && was != end && was != "" {
			m.Alarm.Alarm(changeKey, fmt.Sprintf("%s's line of migrations changed: its last realm was %s, now %s", r.Symbol, was, orNone(end)))
		} else {
			m.Alarm.Clear(changeKey)
		}
		m.state.LineEnds[r.Symbol] = end
	}
	return m.migs
}

// cutOrderView is a poll's reading of the deliveries the burn index cannot place (E4-GT1): how many, and whether an
// honest burn above the index may be among them (lag false: it cannot — a BROKEN reading is a verdict at once).
type cutOrderView struct {
	Unplaced int
	Lag      bool
	// Height is the own node's head read inside the cut's window, and Time its block's time: every credit in the state is
	// at or below it, and every mark is placed by it (E6-GT1)
	Height     int64
	Time       int64
	IndexBlock uint64
	// Above is what the providers showed above the index at the cut, with the credited marks (nil when no reading
	// read BROKEN with a delivery unplaced): the most an honest lag can carry (E5-GT1)
	Above *aboveIndex
}

// cutOrder counts the deliveries the index cannot place at this cut — whose window's head is h, its block's time t —
// and says whether the lag is ruled out: nothing unplaced, or every delivery unplaced now made by a height whose block
// time the index's finalized block has passed (the samples of hlmailbox's count, kept here — the monitor's index holds
// every burn).
func (m *Monitor) cutOrder(mb *gnoside.Mailbox, F2 *ethx.Block, h, t int64) cutOrderView {
	o := cutOrderView{Unplaced: mb.Delivered - m.state.Index.placedBy(h), Height: h, Time: t, IndexBlock: F2.Number, Above: m.above}
	if o.Unplaced > 0 {
		old := oldUnplaced(m.state.Samples, int64(F2.Time), &m.state.Index)
		o.Lag = old < 0 || o.Unplaced > old
	}
	m.state.Samples = noteSample(m.state.Samples, deliverySample{Height: h, Time: t, Delivered: mb.Delivered})
	return o
}

// anyBroken says whether a configured token's identity reads BROKEN over the cut's readings (judge's verdict, read
// ahead: the providers above the index are read only then).
func (m *Monitor) anyBroken(routes map[string]*gnoside.Route, migs map[string]*gnoside.Migration, H int64, snap *ethSnapshot, inflightOut, inflightIn map[string]int64) bool {
	for _, r := range m.Cfg.Routes {
		if r.Token == "" || m.migErrs[r.Symbol] != nil {
			continue
		}
		if v, _ := identityOf(r, routes[r.Symbol], migs[r.Symbol], H, snap.Supplies[r.Symbol], snap.Block.Number, inflightOut[r.Symbol], inflightIn[r.Symbol]).Verdict(); v == Broken {
			return true
		}
	}
	return false
}

// judgePending judges every pending break with the lag the index shows now (E4-GT1): explained by the lag, it ends;
// once the index's finalized block's time has passed the reading's, its excess less the lag found is a break, flagged
// as one; else it waits — alarmed once it has waited two minutes.
func (m *Monitor) judgePending(ctx context.Context, mb *gnoside.Mailbox, F2 *ethx.Block) {
	for _, sym := range sortedSymbols(m.state.Pending) {
		p := m.state.Pending[sym]
		key := "identity:" + sym + ":pending"
		switch p.judge(&m.state.Index, int64(F2.Time)) {
		case pendingLag:
			log.Printf("monitor: %s's reading at height %d was the lag of a provider: %d of burns credited above its block %d, the whole excess (E4-GT1)", sym, p.Height, p.lagSum(), p.Block)
			delete(m.state.Pending, sym)
			m.clearPersisted(key)
		case pendingConfirmed:
			r := m.Cfg.RouteBySymbol(sym)
			if r == nil {
				delete(m.state.Pending, sym) // a route no more configured
				m.clearPersisted(key)
				continue
			}
			if m.migErrs[sym] != nil {
				continue // its line is not read this poll: the flags' realm is not known, judged again next poll
			}
			delete(m.state.Pending, sym)
			m.clearPersisted(key)
			m.breakFlags(ctx, m.liveRoute(*r), mb, "identity:"+sym, p.confirmedText(F2.Number, int64(F2.Time)))
		default:
			text := fmt.Sprintf("%s's identity read BROKEN at height %d (the excess %d) while hlmailbox holds deliveries the burn index cannot place: it waits for the index's finalized block (now %d) to pass that block's time, %s (E4-GT1) — a provider trailing the chain's finalized block, or a forged credit: %s", sym, p.Height, p.Excess, F2.Number, time.Unix(p.Time, 0).UTC().Format(time.RFC3339), p.Text)
			if m.now().Sub(time.Unix(p.Since, 0)) >= pendingFlagAfter {
				// an hour unjudged: a stalled finality, or this machine's providers — a flag-level alarm (no flag on a
				// chain: no flag rests on one provider's word), for the operators and the guardians to read
				m.Alarm.Flag(key, "UNJUDGED FOR AN HOUR: "+text)
				continue
			}
			m.persist(key, text, monCutFail)
		}
	}
}

// liveRoute is a route as its flags go: on its line's live realm while a migration is open.
func (m *Monitor) liveRoute(r Route) Route {
	if mig := m.migs[r.Symbol]; mig != nil {
		r.Gno = mig.Live()
	}
	return r
}

// judgeIdentities applies every token's verdict at a poll: over its line while a migration is open; a route with no
// token, or a token whose line could not be read, is no verdict and nothing is flagged or cleared for it.
func (m *Monitor) judgeIdentities(ctx context.Context, mb *gnoside.Mailbox, routes map[string]*gnoside.Route, migs map[string]*gnoside.Migration, H int64, snap *ethSnapshot, inflightOut, inflightIn map[string]int64, order cutOrderView) {
	for _, r := range m.Cfg.Routes {
		id := identityOf(r, routes[r.Symbol], migs[r.Symbol], H, snap.Supplies[r.Symbol], snap.Block.Number, inflightOut[r.Symbol], inflightIn[r.Symbol])
		switch {
		case r.Token == "":
			m.setIdentity(r.Symbol, id, "no verdict", "no Ethereum token configured for this route")
		case m.migErrs[r.Symbol] != nil:
			m.setIdentity(r.Symbol, id, "no verdict", "the token's line of migrations could not be read this poll: "+m.migErrs[r.Symbol].Error())
		default:
			m.judge(ctx, r, mb, id, order)
		}
	}
	for sym := range m.state.Pending {
		if m.Cfg.RouteBySymbol(sym) == nil {
			delete(m.state.Pending, sym) // a route no more configured
		}
	}
}

// judge applies one token's verdict: a break flags credits and payouts (through the watch key) on the token's live
// realm, and the gate — unless the own node is the odd one out, in which case it alarms — or, while the burn index
// cannot place a delivery an honest lag may explain, waits as no verdict (E4-GT1); a persistent short side alarms; a
// held identity clears the alarms (never the flags: an unpause decision does that).
func (m *Monitor) judge(ctx context.Context, r Route, mb *gnoside.Mailbox, id Identity, order cutOrderView) {
	v, text := id.Verdict()
	if mig := m.migs[r.Symbol]; mig != nil {
		text += " (" + mig.Text() + ")"
		r.Gno = mig.Live()
	}
	if v == Broken && order.Lag {
		// the lag is bounded by what both providers show above the index (E5-GT1): an excess past the most an honest
		// lag can carry is a break at once; within it, the reading waits for the index (E4-GT1)
		if most, ok := lagMost(r.Symbol, id.Block, order.Height, &m.state.Index, order.Above); ok && excessOf(id) > most {
			text = beyondLagText(text, excessOf(id), most, id.Block, order.Above)
		} else {
			if m.state.Pending[r.Symbol] == nil {
				m.state.Pending[r.Symbol] = &pendingBreak{Symbol: r.Symbol, Height: order.Height, Time: order.Time, Block: id.Block, Excess: excessOf(id), Text: text, Since: m.now().Unix()}
			}
			m.short[r.Symbol] = 0
			m.setIdentity(r.Symbol, id, "no verdict", pendingText(text, order.Unplaced, id.Block, order.IndexBlock))
			return
		}
	}
	m.setIdentity(r.Symbol, id, v.String(), text)
	key := "identity:" + r.Symbol
	switch v {
	case Broken:
		m.short[r.Symbol] = 0
		m.breakFlags(ctx, r, mb, key, text)
	case Short:
		m.short[r.Symbol]++
		if m.short[r.Symbol] >= monShortPolls {
			m.Alarm.Alarm(key, "alarm only (a mistaken void, or more in flight than counted): "+text)
		}
	default:
		m.short[r.Symbol] = 0
		m.clearFinding(key)
	}
}

// breakFlags is what an invariant break does: gno credits and payouts on the route, and the gate's flag; while the
// own node is the odd one out, an alarm instead.
func (m *Monitor) breakFlags(ctx context.Context, r Route, mb *gnoside.Mailbox, key, text string) {
	if m.state.OddNode {
		m.Alarm.Alarm(key, "BROKEN but own node is the odd one out among the sources: alarm, no flag: "+text)
		return
	}
	m.Alarm.Flag(key+":flag", "invariant break: credits and payouts flagged on "+r.Gno+": "+text)
	for _, fn := range []string{"credits", "payouts"} {
		m.setGnoFlag(ctx, mb, r.Gno, fn, text)
	}
	m.flagEth(ctx, key+":eth", "invariant break on "+r.Symbol+": "+text, false)
}

// clearFinding ends a finding's alarms once it is cured: its own, its flag's and its Ethereum flag's (breakFlags raises
// all three; the flags themselves stay for an unpause decision).
func (m *Monitor) clearFinding(key string) {
	m.Alarm.Clear(key)
	m.Alarm.Clear(key + ":flag")
	m.Alarm.Clear(key + ":eth")
}

// setGnoFlag sets the watch key's flag on a function of a route unless it stands already (a watch key's flag never
// names ids). Without a watch key, or a seat, it alarms instead.
func (m *Monitor) setGnoFlag(ctx context.Context, mb *gnoside.Mailbox, route, fn, why string) {
	if m.ReadOnly {
		return
	}
	if m.watch == nil || m.Cfg.Gno.WatchKey == "" || m.seat == 0 {
		m.Alarm.Alarm("flag:"+route+":"+fn, "would set "+fn+" on "+route+" but this machine holds no watch seat (gno.watchKey/watchAddress): "+why)
		return
	}
	if rf, ok := mb.Routes[route]; ok {
		for _, f := range rf.StandingFlags() {
			if f.Seat == m.seat && f.Fn == fn {
				return // standing
			}
		}
	}
	if m.gnoFlagged[route+":"+fn] {
		return // set by another rule this poll
	}
	res, err := m.watch.Call(ctx, m.Cfg.Gno.Mailbox, "SetFlag", monFlagGas, route, fn, "")
	if err != nil {
		m.Alarm.Alarm("flag:"+route+":"+fn, "setting "+fn+" on "+route+" failed: "+err.Error())
		return
	}
	if m.gnoFlagged != nil {
		m.gnoFlagged[route+":"+fn] = true
	}
	m.Alarm.Clear("flag:" + route + ":" + fn)
	log.Printf("monitor: FLAG %s set on %s (seat %d): %s", fn, route, m.seat, res.Hash)
}

// clearGnoFlag clears the watch key's own flag on a function of a route (sends, which mirrors Ethereum's delivery pause
// and is cleared when it ends).
func (m *Monitor) clearGnoFlag(ctx context.Context, route, fn string) {
	if m.ReadOnly || m.watch == nil {
		return
	}
	if _, err := m.watch.Call(ctx, m.Cfg.Gno.Mailbox, "ClearFlag", monFlagGas, route, fn); err != nil {
		log.Printf("monitor: clearing %s on %s: %v", fn, route, err)
		return
	}
	log.Printf("monitor: %s cleared on %s (deliveries resumed)", fn, route)
}

// sourceLagMax is how far below the own node's head a source's head may be for the sources rule to compare at it.
const sourceLagMax = 100

// sourceBlock is a source's block hash at H, or — a source a few blocks behind the own node — at its own head, with the
// height compared; an error when it does not answer (E4-GT5: the rule is then said not to have run).
func sourceBlock(ctx context.Context, g gnoside.Reader, H int64) (string, int64, error) {
	b, err := g.Block(ctx, H)
	if err == nil {
		return b.Hash, H, nil
	}
	head, serr := g.Status(ctx)
	if serr != nil {
		return "", 0, err
	}
	if head.Height > 0 && head.Height < H && H-head.Height <= sourceLagMax {
		b, err := g.Block(ctx, head.Height)
		if err != nil {
			return "", 0, err
		}
		return b.Hash, head.Height, nil
	}
	return "", 0, err
}

// judgeSources compares the own node's block hash at H — or at the public source's head, a few blocks behind — with the
// public source's and, on a conflict, the third source's; own-node-odd flags credits (never payouts) on every route
// and the gate. A configured source that does not answer is said (blind): the rule did not run (E4-GT5); and so is no
// source configured at all (E5-GT2) — the sources rule never runs, and the second readings of the ahead rule, the stages
// and the client's trusted header rest on this node's word alone.
func (m *Monitor) judgeSources(ctx context.Context, H int64, ownHash string) {
	if len(m.sources) == 0 {
		m.blind("the sources rule: no second gno source is configured (gno.sources), so it never runs, and the ahead rule's, the stages' and the trusted header's second readings rest on this node's word alone (E5-GT2)")
		m.mu.Lock()
		m.status.Sources = "not judged: no second gno source is configured (gno.sources)"
		m.mu.Unlock()
		return
	}
	public, at, err := sourceBlock(ctx, m.sources[0], H)
	if err != nil {
		m.blind(fmt.Sprintf("the sources rule: the public gno source did not answer at height %d (%v)", H, err))
		m.mu.Lock()
		m.status.Sources = fmt.Sprintf("not judged at height %d: the public source did not answer", H)
		m.mu.Unlock()
		return
	}
	if at != H {
		b, err := m.own.Block(ctx, at)
		if err != nil {
			m.blind(fmt.Sprintf("the sources rule: this node's block at %d (the public source's head) could not be read (%v)", at, err))
			return
		}
		H, ownHash = at, b.Hash
	}
	third := ""
	if len(m.sources) > 1 {
		if b, err := m.sources[1].Block(ctx, H); err == nil {
			third = b.Hash
		}
	}
	v := JudgeSources(ownHash, public, third)
	m.mu.Lock()
	m.status.Sources = fmt.Sprintf("%s at height %d", v, H)
	m.mu.Unlock()
	switch v {
	case SourcesAgree:
		m.state.OddNode = false
		m.Alarm.Clear("sources")
	case SourcesLag:
	case OwnNodeOdd:
		m.state.OddNode = true
		m.Alarm.Flag("sources", fmt.Sprintf("own node is the odd one out at height %d (own %s, public %s, third %s): credits flagged", H, ownHash, public, third))
		if mb, err := gnoside.MailboxAt(ctx, m.own, m.Cfg.Gno.Mailbox, 0); err == nil {
			m.seat = monSeatOf(mb.Seats, m.Cfg.Gno.WatchAddress)
			for _, r := range m.Cfg.Routes {
				m.setGnoFlag(ctx, mb, r.Gno, "credits", "own node odd one out")
			}
		}
		m.flagEthGate(ctx, "sources:eth", fmt.Sprintf("own node is the odd one out at height %d: the gate flagged", H))
	default:
		m.Alarm.Alarm("sources", fmt.Sprintf("the sources disagree at height %d (%s): own %s, public %s, third %q", H, v, ownHash, public, third))
	}
}

// sends reads hlmailbox's new messages toward the routes' tokens on Ethereum, marks the ones Ethereum's mailbox
// delivered at the snapshot's final block (both providers), and the ones gno.land refunded (sends.go, the signers'
// reading too), then alarms a refunded send that sits undelivered and one delivered after its refund (minted twice).
func (m *Monitor) sends(ctx context.Context, mb *gnoside.Mailbox, routes map[string]*gnoside.Route, H int64, snap *ethSnapshot) error {
	delivered, _, refundErr, err := m.state.sendIndex.sync(ctx, m.own, m.pair, m.Cfg, mb, routes, m.migs, m.migErrs, H, snap.Pin, m.now(), 0)
	if err != nil {
		return err
	}
	if delivered > 0 {
		m.state.LastDelivery = m.now().Unix()
	}
	for n, s := range m.state.Sends {
		key, dkey := "send:refunded:"+n, "refund:delivered:"+s.ID
		switch {
		case s.doubleMinted():
			m.Alarm.Clear(key)
			m.Alarm.AlarmEvery(dkey, fmt.Sprintf("gno #%s (%s, %d) was refunded on gno.land and Ethereum delivered it after the refund: minted twice — the supply is above the lock by %d until GovDAO books it as lost with the route's write-off builder", n, s.Symbol, s.Amount, s.Amount), 24*time.Hour)
		case s.Refunded && !s.Delivered:
			m.Alarm.AlarmEvery(key, fmt.Sprintf("gno #%s (%s, %d) was refunded on gno.land and still sits undelivered in hlmailbox: it must never deliver (the amount left the lock)", n, s.Symbol, s.Amount), 24*time.Hour)
			m.Alarm.Clear(dkey)
		default:
			m.Alarm.Clear(key)
			m.Alarm.Clear(dkey)
		}
	}
	m.state.sendIndex.prune(routes, m.migs)
	return refundErr
}

// monRefundCount is a token's refund count: the route's, or the line's sum while a migration is open.
func monRefundCount(rj *gnoside.Route, mig *gnoside.Migration) int64 {
	if mig != nil {
		var n int64
		for _, j := range mig.Realms() {
			if j != nil {
				n += j.Refunded
			}
		}
		return n
	}
	if rj != nil {
		return rj.Refunded
	}
	return 0
}

// monSortedNonces are a sends map's keys in nonce order.
func monSortedNonces(sends map[string]*monSend) []string {
	out := make([]string, 0, len(sends))
	for n := range sends {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.ParseUint(out[i], 10, 64)
		b, _ := strconv.ParseUint(out[j], 10, 64)
		return a < b
	})
	return out
}

// divertedCredits checks every credited burn once: the claim gno.land wrote must name the recipient and amount of the
// Ethereum burn (verifier-credited claims only; a claim credited by GovDAO's vote is skipped, the realm marks it); a
// burn to no gno address must be a sweep of its amount. A difference is an invariant break: a diverted credit moves no
// supply, so the identity alone would miss it. A finding is kept in the state and judged again every poll until its
// cure — the claim voided, or gone (E4-GT6): its flag-level alarm raised again each poll, its flags set again when
// found cleared (an unpause), as the identity's are.
func (m *Monitor) divertedCredits(ctx context.Context, mb *gnoside.Mailbox, H int64) error {
	for _, id := range sortedSymbols(m.state.Diverted) {
		text := m.state.Diverted[id]
		key := "diverted:" + id
		rec := m.state.Index.Burns[id]
		if rec == nil {
			delete(m.state.Diverted, id)
			m.clearFinding(key)
			continue
		}
		r := m.Cfg.RouteBySymbol(rec.Symbol)
		if r == nil {
			delete(m.state.Diverted, id)
			m.clearFinding(key)
			continue
		}
		if m.migErrs[rec.Symbol] != nil {
			m.Alarm.Flag(key+":flag", "invariant break (its token's line not read this poll: judged again next poll): "+text)
			continue
		}
		c, _, err := gnoside.ClaimOnLine(ctx, m.own, r.Gno, m.migs[rec.Symbol], id, H)
		if err != nil {
			// a claim that does not read is said and stands; the poll goes on to the Ethereum rules (E4-RL1's class)
			m.blind(fmt.Sprintf("the diverted credit %s's claim on %s (judged again next poll): %v", short(id), rec.Symbol, err))
			m.Alarm.Flag(key+":flag", "invariant break (its claim not read this poll: judged again next poll): "+text)
			continue
		}
		switch divertedVerdict(c, rec) {
		case divertedCured:
			delete(m.state.Diverted, id)
			m.clearFinding(key)
		case divertedVoided:
			// voided: no flag while it stands, but kept — a void is restorable (a withheld claim at once, a forged
			// one after 7 days), and a restore of the diverted claim pays the wrong recipient again (the review's
			// finding 2)
			m.clearFinding(key)
		default:
			m.breakFlags(ctx, m.liveRoute(*r), mb, key, text)
		}
	}
	for _, id := range m.state.Index.ids() {
		rec := m.state.Index.Burns[id]
		if rec.Credited == 0 || rec.Checked {
			continue
		}
		r := m.Cfg.RouteBySymbol(rec.Symbol)
		if r == nil || m.migErrs[rec.Symbol] != nil {
			continue
		}
		c, _, err := gnoside.ClaimOnLine(ctx, m.own, r.Gno, m.migs[rec.Symbol], id, H)
		if err != nil {
			m.blind(fmt.Sprintf("the credited burn %s's claim on %s (the diverted-credit rule, judged next poll): %v", short(id), rec.Symbol, err))
			continue
		}
		rec.Checked = true
		if c == nil || c.By == "vote" {
			continue // paid by a sweep decision, or migrated; or GovDAO's own credit
		}
		if !creditMatches(c, rec) {
			to := rec.To
			if rec.Bad {
				to = "(no gno address: a sweep expected)"
			}
			text := fmt.Sprintf("diverted credit %s on %s: the claim names %s for %d (swept %d), the Ethereum burn %s for %d", id, rec.Symbol, c.To, c.Amount, c.Swept, to, rec.Amount)
			m.state.Diverted[id] = text
			m.breakFlags(ctx, *r, mb, "diverted:"+id, text)
		}
	}
	return nil
}

// creditMatches says whether a claim credits what the burn carried: a sweep of its amount, or its gno recipient and
// amount.
func creditMatches(c *gnoside.Claim, rec *burnRec) bool {
	return (c.Swept != 0 && c.Swept == rec.Amount) || (c.Swept == 0 && !rec.Bad && c.To == rec.To && c.Amount == rec.Amount)
}

// divertedState is where a diverted credit's claim stands.
type divertedState int

const (
	divertedStands divertedState = iota // open or paid out, still not the burn's: flagged
	divertedVoided                      // voided (withheld or forged): no flag, kept — a void is restorable
	divertedCured                       // gone, credited afresh by GovDAO's vote, or the burn's now: the finding ends
)

// divertedVerdict says where a diverted credit's claim stands (E4-GT6): cured when the claim is gone, credited afresh by
// GovDAO's vote (the cure of a diverted credit: void, then credit by vote) or naming the burn's recipient and amount
// now; voided while it stands withheld or forged — kept, since a restore brings the diverted claim back; standing
// otherwise (a diverted claim paid out stands until GovDAO voids it as forged, its paid part then booked lost).
func divertedVerdict(c *gnoside.Claim, rec *burnRec) divertedState {
	switch {
	case c == nil, c.By == "vote", creditMatches(c, rec):
		return divertedCured
	case c.State == "withheld", c.State == "forged":
		return divertedVoided
	}
	return divertedStands
}

// unindexedCredits is §5.9's other rule on a credit: a waiting claim the verifier credited (never one GovDAO voted)
// whose message id the burn index does not hold is a forged credit, and flags credits and payouts on its route, and
// the gate, as an invariant break does — judged once the index's finalized block's time has passed the credit's (E4-GT1:
// an honest burn credited by then lies at or below that block, so the index holds it; before then it may be a burn
// above the index, a provider trailing the signers' finalized block — for minutes, not a poll). The index is complete to
// the finalized block both providers agreed on, F2 (a scan that failed returned before this rule); an id that appears
// in it clears its sighting and its alarm (the flags set stay for an unpause decision, as every flag does).
func (m *Monitor) unindexedCredits(ctx context.Context, mb *gnoside.Mailbox, H int64, F2 *ethx.Block, unplaced int) error {
	m.state.WaitingPoll++
	m.pruneWaiting()
	base, baseErr := m.indexBase(ctx, H)
	if baseErr != nil {
		m.persist("credit:unindexed-base", "where the burn index begins against the deployment's messages could not be read ("+ethReadText(baseErr)+"): an unindexed claim is alarmed, never flagged, until it is", monCutFail)
	} else {
		m.clearPersisted("credit:unindexed-base")
	}
	beforeBase := 0
	// the waiting claims are read only while a delivery is unplaced (E4-RL1, E4-GT1): every claim is a delivery of
	// hlmailbox (a Process, or a vote's MarkDone; a migration's imported claims were delivered to the predecessor), so
	// with every delivery an indexed burn no waiting claim is unindexed; each realm's open claims the index may not
	// hold are read from its waiting index (waitingClaims: the entries since the last read, a page a seek wherever it
	// starts, and the earlier ones read again by id), stopping once as many unindexed claims were found as there are
	// unplaced deliveries
	found := 0
	var failed, bounded, unfinished, waiting []string
	var above *aboveIndex // the dispatches above the index, read once a poll when a waiting claim asks
	for _, r := range m.Cfg.Routes {
		if unplaced <= 0 || found >= unplaced {
			break
		}
		if r.Token == "" || m.migErrs[r.Symbol] != nil {
			continue
		}
		realms := []string{r.Gno}
		if mig := m.migs[r.Symbol]; mig != nil {
			realms = mig.Paths()
		}
		for _, realm := range realms {
			if found >= unplaced {
				break
			}
			claims, how, err := m.waitingClaims(ctx, realm, H)
			switch {
			case err != nil:
				// a page that does not read is said; the poll goes on to the Ethereum rules (E4-RL1)
				m.blind(fmt.Sprintf("the waiting claims of %s (the forged-credit rule): %v", realm, err))
				failed = append(failed, realm)
			case how == claimsBounded:
				bounded = append(bounded, realm)
			case how == claimsMore:
				unfinished = append(unfinished, realm)
			}
			for _, c := range claims {
				id := strings.ToLower(strings.TrimPrefix(c.ID, "0x"))
				if !m.state.Index.has(id) {
					found++ // an unplaced delivery found waiting (a vote's credit among them)
				}
				if c.By == "vote" {
					continue
				}
				key := "credit:unindexed:" + id
				if m.state.Index.has(id) {
					if _, seen := m.seen[key]; seen {
						m.clearPersisted(key)
						m.clearFinding(key)
					}
					delete(m.state.BeforeBase, id)
					continue
				}
				// an index that misses messages of the deployment (E3-GT4; G49): a claim whose burn may predate the
				// index's first block is alarmed, never flagged — nor while that cannot be read — with the others like
				// it, in one alarm (E5-CF3 item 4)
				if baseErr != nil || base.checkpointed() {
					if baseErr == nil && c.Height <= base.FromHeight {
						beforeBase++
					}
					m.state.BeforeBase[id] = fmt.Sprintf("%s on %s (credited at height %d to %s for %d)", id, r.Symbol, c.Height, c.To, c.Amount)
					continue
				}
				delete(m.state.BeforeBase, id) // alarmed while the base did not read, and judged now that it does
				if _, seen := m.seen[key]; !seen {
					m.seen[key] = m.now() // its sighting: the cure below ends it
				}
				at, known := m.creditTime(ctx, c)
				why := fmt.Sprintf("the finalized block %d both providers agree on, whose time %s is past the credit's (an honest burn credited by then lies at or below it)", F2.Number, time.Unix(int64(F2.Time), 0).UTC().Format(time.RFC3339))
				if !known || int64(F2.Time) < at {
					// an honest burn above the index may be it — unless neither provider, each current to its head,
					// shows a dispatch of its id above the index (one honest provider showing it keeps the wait)
					if above == nil {
						above = m.dispatchedAbove(ctx, F2.Number, mb.Domain)
					}
					if !above.known || above.ids[id] {
						when := "its time could not be read"
						if known {
							when = time.Unix(at, 0).UTC().Format(time.RFC3339)
						}
						waiting = append(waiting, fmt.Sprintf("%s on %s (height %d, %s)", short(id), r.Symbol, c.Height, when))
						continue
					}
					why = fmt.Sprintf("the finalized block %d both providers agree on, nor above it to either provider's head (%s): no message of its id was dispatched toward gno.land", F2.Number, above.heads)
				}
				m.breakFlags(ctx, r, mb, key, fmt.Sprintf("forged credit %s on %s: the claim names %s for %d (credited at height %d) and no burn of the token under its id is on Ethereum at or below %s", id, r.Symbol, c.To, c.Amount, c.Height, why))
			}
		}
	}
	if n := len(waiting); n > 0 {
		more := ""
		if n > 3 {
			more, waiting = fmt.Sprintf(" and %d more", n-3), waiting[:3]
		}
		m.behind(fmt.Sprintf("%d credit(s) in no burn of the index, which ends at the finalized block %d (%s), wait until that block's time passes theirs — an honest burn credited then may lie above it (E4-GT1): %s%s", n, F2.Number, time.Unix(int64(F2.Time), 0).UTC().Format(time.RFC3339), strings.Join(waiting, "; "), more))
	}
	if unplaced > found && len(failed)+len(bounded) > 0 {
		var why []string
		if len(failed) > 0 {
			why = append(why, "the waiting claims of "+strings.Join(failed, ", ")+" did not read")
		}
		if len(bounded) > 0 {
			why = append(why, fmt.Sprintf("the walk reads the first %d of %s, built before the waiting index (its claims/<from> walks every waiting claim before <from>, out of query gas past ≈1,600: E4-RL1)", claimsWalkMax+gnoside.ClaimsPage, strings.Join(bounded, ", ")))
		}
		m.blind(fmt.Sprintf("%d message(s) hlmailbox delivered are no burn the index holds and are not among the waiting claims read — %s; the identity still judges the supply", unplaced-found, strings.Join(why, "; ")))
	}
	if unplaced > found && len(unfinished) > 0 {
		m.behind(fmt.Sprintf("%d message(s) hlmailbox delivered are no burn the index holds and are not among the waiting claims read yet — the waiting index of %s is read %d pages a poll, on from where the last poll stopped, and the open claims found there that no burn of the index carries again by id, %d a poll in turn", unplaced-found, strings.Join(unfinished, ", "), waitingPagesPerPoll, waitingRefreshPerPoll))
	}
	m.mu.Lock()
	m.status.ClaimsBeforeBase = beforeBase
	m.mu.Unlock()
	// a claim alarmed as before the index's base ends with its cure: the claim voided, paid, or gone — read by id, but
	// for the waiting readings' suspects, which are read again in turn there and leave the alarm when dropped
	for _, id := range sortedSymbols(m.state.BeforeBase) {
		if m.waitingSuspect(id) {
			continue
		}
		if m.claimCured(ctx, id, H) {
			delete(m.state.BeforeBase, id)
		}
	}
	m.alarmBeforeBase(base, baseErr)
	// a suspect's sighting and flag alarm end with their cure: the claim voided, paid under a vote, or gone
	for key := range m.seen {
		if !strings.HasPrefix(key, "credit:unindexed:") {
			continue
		}
		if m.claimCured(ctx, strings.TrimPrefix(key, "credit:unindexed:"), H) {
			m.clearPersisted(key)
			m.clearFinding(key)
		}
	}
	return nil
}

// dispatchedAbove reads the dispatches toward gno.land in the blocks above the index's finalized block, through each
// provider to its head (E4-GT1's fast path for a forged credit, the review's finding 3; readAbove): a waiting claim no
// burn of the index carries, whose id neither provider — both current — shows dispatched above the index either, is no
// lag: no message of its id exists on Ethereum, and it is judged at once rather than after the finality lag. Sound
// against one lying provider: a liar can hide nothing the honest one shows (shown by either, the claim waits), and a
// liar's fake dispatch only keeps the wait. Not known when either provider does not answer or lags. A poll reads it
// once: the cut's reading (taken before its re-read when a BROKEN reading may wait, with the credited marks), else here.
func (m *Monitor) dispatchedAbove(ctx context.Context, from uint64, gnoDomain uint32) *aboveIndex {
	if m.above != nil && m.aboveFrom == from {
		return m.above
	}
	m.above, m.aboveFrom = readAbove(ctx, m.pair, m.Cfg, from, gnoDomain, m.now()), from
	return m.above
}

// creditTime is when a claim was credited (E4-GT1): its own "at" (the realm's clock at the credit, the block's time),
// else the block time of its height as the own node gives it; false when neither is known.
func (m *Monitor) creditTime(ctx context.Context, c gnoside.Claim) (int64, bool) {
	if c.At > 0 {
		return c.At, true
	}
	if c.Height > 0 {
		if b, err := m.own.Block(ctx, c.Height); err == nil && !b.Time.IsZero() {
			return b.Time.Unix(), true
		}
	}
	return 0, false
}

// claimCured says whether a suspect claim's alarm may end: its id is in the burn index now, or every route answered
// and none holds it waiting (voided, paid, swept, gone). A route that did not answer — its claim read failing, or its
// line of migrations not read this poll — may hold it open: another route's "no such claim" cures nothing (the Go
// trust audit's E5-GT4, item 1), and the claim stays suspect until a poll reads them all.
func (m *Monitor) claimCured(ctx context.Context, id string, H int64) bool {
	if m.state.Index.has(id) {
		return true
	}
	read := false
	for _, r := range m.Cfg.Routes {
		if r.Token == "" {
			continue
		}
		if m.migErrs[r.Symbol] != nil {
			return false
		}
		c, _, err := gnoside.ClaimOnLine(ctx, m.own, r.Gno, m.migs[r.Symbol], id, H)
		if err != nil {
			return false
		}
		read = true
		if c != nil && c.Swept == 0 && c.State == "open" {
			return false
		}
	}
	return read
}

// claimsRead says how far a realm's open claims were read this poll (waitingClaims).
type claimsRead int

const (
	claimsAll     claimsRead = iota // every claim open on the route that no burn of the index carries, read this poll
	claimsMore                      // the waiting index or the suspects not all read this poll: the next poll reads on
	claimsBounded                   // a route built before the index: its claims/<from> walk, the first pages only
)

// waitingPagesPerPoll bounds the waiting/<from> pages one realm's read takes a poll: each is a seek and a page,
// ≈219M query gas on a gno v1.5.1 node wherever it starts (F15d), so ten — a thousand claims — cost about what the
// claims walk's three first pages did; a longer backlog (a restart over thousands waiting) is read on from where the
// poll stopped, at the next poll (hlwatch's catch-up polls on while it moves). waitingRefreshPerPoll bounds the
// suspects read again by id a poll (claim/<id>, ≈19M gas each on a node): the least recently read first, so a
// thousand suspects (a checkpointed index's open claims from before it) are each read every ten polls, never all in
// one; a suspect is judged only at a poll that read it.
const (
	waitingPagesPerPoll   = 10
	waitingRefreshPerPoll = 100
)

// waitingRestart is how often a reading starts over from the index's first place, keeping its suspects: a node's odd
// answer (a page past the route's last place, a claim read as gone that is not) costs a day at most.
const waitingRestart = 24 * time.Hour

// waitingIndex is the monitor's reading of a route realm's waiting index (E4-RL1; the realms' waiting/<from>, fold
// F15d): the next place to read, and the suspects — the open claims read whose ids the burn index did not hold. Every
// claim open on the route is in the index once, and a claim open again takes a new place above every place given
// before, so the places below next hold nothing open the reading has not seen: a claim read there was in the burn
// index (it stays there), not open (it comes back only at a new place), or is a suspect, read again by id in turn. It
// starts over from place 1 when next passes the route's own last place (an odd answer) and once a day.
type waitingIndex struct {
	Next    int64                      `json:"next"`
	Suspect map[string]*waitingSuspect `json:"suspects,omitempty"`
	Older   bool                       `json:"older,omitempty"` // the route renders waiting/<from> as an unknown path: built before the index
	More    bool                       `json:"more,omitempty"`  // the last read stopped at its page budget: a backlog, read on at the next poll
	Start   int64                      `json:"start"`           // when the reading last started from place 1 (unix)
}

// waitingSuspect is a suspect as last read (its page entry or claim/<id>), and the poll that read it.
type waitingSuspect struct {
	gnoside.WaitingEntry
	Read uint64 `json:"read"`
}

// waitingSuspect says whether a claim id is a suspect of any realm's reading (read again by id in turn there).
func (m *Monitor) waitingSuspect(id string) bool {
	for _, w := range m.state.Waiting {
		if _, ok := w.Suspect[id]; ok {
			return true
		}
	}
	return false
}

// pruneWaiting drops the readings of realms no configured token's line holds any more (a route taken out of the config,
// a line's realm gone from it), once every line was read this poll: a reading kept in the state outlives the config it
// was read under.
func (m *Monitor) pruneWaiting() {
	if len(m.migErrs) > 0 {
		return
	}
	live := map[string]bool{}
	for _, r := range m.Cfg.Routes {
		live[r.Gno] = true
		if mig := m.migs[r.Symbol]; mig != nil {
			for _, p := range mig.Paths() {
				live[p] = true
			}
		}
	}
	for realm := range m.state.Waiting {
		if !live[realm] {
			delete(m.state.Waiting, realm)
		}
	}
}

// waitingClaims reads a realm's open claims the burn index may not hold, at a poll (E4-RL1): the waiting index's
// entries since the last read, waitingPagesPerPoll pages at most (an open entry whose id the index does not hold kept
// as a suspect, a suspect's entry no longer open dropped), then the suspects not read this poll again by id, the least
// recently read first, waitingRefreshPerPoll at most (one paid, voided, swept, gone or indexed since is dropped, its
// before-the-index alarm with it). It answers the suspects read this poll alone — a suspect whose read failed or did
// not come round is not judged on what it was — and how far the reading got: so every waiting claim is read once at a
// page's bounded gas and only the suspects again, at a bounded number of reads a poll. A route built before the index
// (waiting/<from> an unknown path) is walked as before (walkClaims: its first 300). A read that fails is answered with
// the suspects read this poll and its error; the next poll reads on from the place the reading reached.
func (m *Monitor) waitingClaims(ctx context.Context, realm string, H int64) ([]gnoside.Claim, claimsRead, error) {
	if m.state.Waiting == nil {
		m.state.Waiting = map[string]*waitingIndex{}
	}
	w := m.state.Waiting[realm]
	if w == nil {
		w = &waitingIndex{Next: 1, Suspect: map[string]*waitingSuspect{}, Start: m.now().Unix()}
		m.state.Waiting[realm] = w
	}
	if w.Older {
		claims, complete, err := m.walkClaims(ctx, realm, H)
		if err != nil || complete {
			return claims, claimsAll, err
		}
		return claims, claimsBounded, nil
	}
	poll := m.state.WaitingPoll
	var errs []error
	rj, err := gnoside.RouteAt(ctx, m.own, realm, H)
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("json: %w", err))
	case w.Next > rj.WaitingLast+1, m.now().Sub(time.Unix(w.Start, 0)) >= waitingRestart:
		w.Next, w.Start = 1, m.now().Unix() // past the route's own last place (an odd answer), or a day: read it again
	}
	w.More = true
	for page := 0; err == nil && page < waitingPagesPerPoll; page++ {
		entries, unknown, perr := gnoside.WaitingAt(ctx, m.own, realm, w.Next, H)
		if perr != nil {
			errs = append(errs, perr)
			break
		}
		if unknown {
			if w.Next > 1 || len(w.Suspect) > 0 {
				errs = append(errs, fmt.Errorf("%s renders waiting/%d as an unknown path after its waiting index read to place %d", realm, w.Next, w.Next-1))
				break
			}
			w.Older = true
			return m.waitingClaims(ctx, realm, H)
		}
		for _, e := range entries {
			id := strings.ToLower(strings.TrimPrefix(e.ID, "0x"))
			if indexed := m.state.Index.has(id); e.State == "open" && e.Swept == 0 && !indexed {
				w.Suspect[id] = &waitingSuspect{WaitingEntry: e, Read: poll}
			} else if _, ok := w.Suspect[id]; ok {
				m.dropSuspect(w, id) // left the queue, or its burn is indexed now
			}
		}
		if n := len(entries); n > 0 {
			w.Next = entries[n-1].Pos + 1
		}
		if len(entries) < gnoside.WaitingPage {
			w.More = false
			break
		}
	}
	// the suspects not read this poll, again by id, the least recently read first
	var stale []*waitingSuspect
	for _, sp := range w.Suspect {
		if sp.Read < poll {
			stale = append(stale, sp)
		}
	}
	sort.Slice(stale, func(i, j int) bool {
		if stale[i].Read != stale[j].Read {
			return stale[i].Read < stale[j].Read
		}
		return stale[i].Pos < stale[j].Pos
	})
	for i, sp := range stale {
		if i == waitingRefreshPerPoll {
			break
		}
		id := strings.ToLower(strings.TrimPrefix(sp.ID, "0x"))
		if m.state.Index.has(id) {
			m.dropSuspect(w, id)
			continue
		}
		c, cerr := gnoside.ClaimAt(ctx, m.own, realm, id, H)
		if cerr != nil {
			errs = append(errs, fmt.Errorf("claim/%s: %w", id, cerr))
			break // the rest wait their turn, unjudged
		}
		if c == nil || c.State != "open" || c.Swept != 0 {
			m.dropSuspect(w, id) // paid, voided, swept or gone: open again only at a new place
			continue
		}
		sp.Claim, sp.Read = *c, poll
	}
	var fresh []*waitingSuspect
	behind := w.More
	for _, sp := range w.Suspect {
		if sp.Read == poll {
			fresh = append(fresh, sp)
		} else {
			behind = true
		}
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].Pos < fresh[j].Pos })
	claims := make([]gnoside.Claim, len(fresh))
	for i, sp := range fresh {
		claims[i] = sp.Claim
	}
	how := claimsAll
	if behind {
		how = claimsMore
	}
	return claims, how, errors.Join(errs...)
}

// dropSuspect takes a claim out of a reading's suspects — it left the queue, or its burn is indexed — and ends its
// before-the-index alarm, which the reading kept current (the cure loop reads no suspect by id).
func (m *Monitor) dropSuspect(w *waitingIndex, id string) {
	delete(w.Suspect, id)
	delete(m.state.BeforeBase, id)
}

// claimsWalkMax is the highest offset the walk of a realm's waiting claims reads on a route built before the waiting
// index (E4-RL1): a route's claims/<from> walks every waiting claim before <from>, ≈1.5M query gas each on gno v1.5.1
// (claims/0 0.50B, /300 1.01B, /900 1.96B; past ≈1,600 out of the 3B), so the walk reads the pages at offsets 0, 100
// and 200 only — each under a third of the 3B (§5.1's render rule) — and names the rest as not read.
const claimsWalkMax = 200

// walkClaims reads a realm's waiting claims from the first, page by page up to claimsWalkMax: the claims, and whether
// every one was read (a page shorter than a full one is the last).
func (m *Monitor) walkClaims(ctx context.Context, realm string, H int64) ([]gnoside.Claim, bool, error) {
	var claims []gnoside.Claim
	for at := 0; at <= claimsWalkMax; at += gnoside.ClaimsPage {
		s, err := m.own.RenderAt(ctx, realm, "claims/"+strconv.Itoa(at), H)
		if err != nil {
			return claims, false, err
		}
		var page []gnoside.Claim
		if err := gnoside.JSON(s, &page); err != nil {
			return claims, false, err
		}
		claims = append(claims, page...)
		if len(page) < gnoside.ClaimsPage {
			return claims, true, nil
		}
	}
	return claims, false, nil
}

// indexBase reads, once, where the burn index begins against the deployment's messages (E3-GT4): Mailbox.nonce() at
// the block before the index's first, both providers agreeing, against the launch line the scan read. An index from
// block 1 misses nothing. Read again each poll until it is read; a checkpointed index takes the cut's height as its
// FromHeight at the first poll that judges the claims.
func (m *Monitor) indexBase(ctx context.Context, H int64) (*indexBase, error) {
	b := m.state.IndexBase
	if b == nil {
		from := m.state.From
		b = &indexBase{Line: m.state.Index.Line}
		if from > 1 {
			if m.Cfg.Eth.Mailbox == "" {
				return nil, errors.New("the config names no mailbox (eth.mailbox)")
			}
			h, err := m.pair.CanonicalHash(ctx, from-1)
			if err != nil {
				return nil, fmt.Errorf("the block before the index's first (%d): %w", from-1, err)
			}
			data, err := ethx.Mailbox.Pack("nonce")
			if err != nil {
				return nil, err
			}
			out, err := m.pair.CallAt(ctx, Address(m.Cfg.Eth.Mailbox), data, ethx.AtHash(h))
			if err != nil {
				return nil, fmt.Errorf("the mailbox's nonce() at block %d: %w", from-1, err)
			}
			if len(out) > 0 { // no code there yet (the index starts before the mailbox): nothing dispatched
				vals, err := ethx.Mailbox.Unpack("nonce", out)
				if err != nil || len(vals) != 1 {
					return nil, fmt.Errorf("the mailbox's nonce() at block %d: %v", from-1, err)
				}
				b.Nonce, _ = vals[0].(uint32)
			}
			if b.Nonce > b.Line {
				b.Missed = b.Nonce - b.Line
			}
		}
		m.state.IndexBase = b
		if b.Missed > 0 {
			log.Printf("monitor: the burn index begins at block %d, past %d of the deployment's messages (the mailbox's nonce %d there, the launch line %d): an unindexed claim is alarmed, never flagged (E3-GT4)", from, b.Missed, b.Nonce, b.Line)
		}
	}
	if b.checkpointed() && b.FromHeight == 0 {
		b.FromHeight = H
	}
	return b, nil
}

// beforeBaseKey is the one alarm the waiting claims a checkpointed index cannot judge raise together (E3-GT4): one a
// claim was up to a thousand a poll as a reading's pages reached them (the conformance audit's E5-CF3, item 4).
const beforeBaseKey = "credit:unindexed-before-base"

// beforeBaseShown is how many of those claims the alarm names; the rest are counted.
const beforeBaseShown = 3

// alarmBeforeBase raises the one alarm of the claims a checkpointed index cannot judge — how many, why they are alarmed
// and not flagged, the first by id — repeated daily while any stands, cleared once none does.
func (m *Monitor) alarmBeforeBase(b *indexBase, baseErr error) {
	n := len(m.state.BeforeBase)
	if n == 0 {
		m.Alarm.Clear(beforeBaseKey)
		return
	}
	ids := sortedSymbols(m.state.BeforeBase)
	var named []string
	for _, id := range ids[:min(n, beforeBaseShown)] {
		named = append(named, m.state.BeforeBase[id])
	}
	if n > beforeBaseShown {
		named = append(named, fmt.Sprintf("and %d more", n-beforeBaseShown))
	}
	why := "where the index begins against the deployment's messages could not be read (" + ethReadText(baseErr) + "): alarmed, never flagged, until it is"
	if baseErr == nil && b != nil {
		why = fmt.Sprintf("this index began past %d of the deployment's messages (a -from-block, or eth.deployBlock, after the deploy), so each one's burn may be one of them, credited late — one credited at or before height %d predates the index for certain — or there is none: alarmed, never flagged (the identity still judges the supply); an index from the deploy block judges them (E3-GT4)", b.Missed, b.FromHeight)
	}
	m.Alarm.AlarmEvery(beforeBaseKey, fmt.Sprintf("%d waiting claim(s) have no burn in this monitor's index — %s: %s", n, why, strings.Join(named, "; ")), 24*time.Hour)
}

// stalls raises the stall-with-backlog (30 minutes) and age alarms: a burn toward gno.land waiting two hours for its
// credit, a send toward Ethereum waiting seven (DeliveryAgeAfter: a send waits for its header by design — until its
// fees and its batch cover the whole cost, or six hours and the budget — so the stall counts a send only once it is
// six hours old, when it buys its own header; §5.5, §6.1). A burn below the fee of the realm that would credit it — the
// route, or its line's live successor — is dust the relayers leave to whoever pays for it (E5-GS5): it waits for
// nobody, so it raises neither (a stranger's dust stood `age` for good and `stall` after every quiet half hour: F17a's
// review R1, the Solana monitors' 50f5c01e); it is judged against the fee every poll, so a fee a decision lowers past
// it makes it a delivery again, its wait counted from the last poll that found it dust (the relayers judge it so each
// tick). routes are the routes' jsons as read this poll (nil: every burn waits).
func (m *Monitor) stalls(routes map[string]*gnoside.Route) {
	now := m.now()
	fee := realmFee(routes, m.migs)
	waiting, dust := map[string]int{}, map[string]int{}
	for sym, f := range m.state.Index.foldedSum() { // the folded dust (E6-GT3): dust for an hour already
		dust[sym] += int(f[1])
	}
	var oldestIn, oldestOut time.Time
	stalled := 0
	for _, rec := range m.state.Index.Burns {
		if rec.Credited != 0 {
			continue
		}
		if rec.Amount < fee(rec.Symbol) {
			rec.DustAt = now.Unix()
			dust[rec.Symbol]++
			continue
		}
		waiting["ethereum→gno "+rec.Symbol]++
		stalled++
		if t := time.Unix(max(rec.Seen, rec.DustAt), 0); oldestIn.IsZero() || t.Before(oldestIn) {
			oldestIn = t
		}
	}
	for _, s := range m.state.Sends {
		if s.inFlight() {
			waiting["gno→ethereum "+s.Symbol]++
			t := time.Unix(s.Seen, 0)
			if now.Sub(t) >= m.Cfg.Heartbeat() {
				stalled++
			}
			if oldestOut.IsZero() || t.Before(oldestOut) {
				oldestOut = t
			}
		}
	}
	m.mu.Lock()
	m.status.Waiting, m.status.Dust = waiting, dust
	m.mu.Unlock()
	if stalled == 0 {
		m.Alarm.Clear("stall")
	} else if since := now.Sub(time.Unix(m.state.LastDelivery, 0)); since > monStallAfter {
		m.Alarm.Alarm("stall", fmt.Sprintf("%d messages waiting past their time and no delivery for %s", stalled, since.Round(time.Minute)))
	} else {
		m.Alarm.Clear("stall")
	}
	var why []string
	if !oldestIn.IsZero() && now.Sub(oldestIn) > monAgeAfter {
		why = append(why, fmt.Sprintf("the oldest burn waiting for its credit on gno.land is %s old (over %s)", now.Sub(oldestIn).Round(time.Minute), monAgeAfter))
	}
	if !oldestOut.IsZero() && now.Sub(oldestOut) > DeliveryAgeAfter {
		why = append(why, fmt.Sprintf("the oldest send waiting for its delivery on Ethereum is %s old (over %s: its fees and the budget have not bought its header)", now.Sub(oldestOut).Round(time.Minute), DeliveryAgeAfter))
	}
	if len(why) > 0 {
		m.Alarm.Alarm("age", strings.Join(why, "; "))
	} else {
		m.Alarm.Clear("age")
	}
}

// runways samples the floats and alarms under 7 days of the trailing rate: the relayers' and the updater's ETH keys,
// the relayers' gno gas key, this machine's watch keys (§5.5).
func (m *Monitor) runways(ctx context.Context) {
	type float struct {
		name string
		read func() (float64, error)
	}
	var floats []float
	wei := func(a common.Address) func() (float64, error) {
		return func() (float64, error) {
			b, err := m.pair.A.BalanceAt(ctx, a, ethx.Latest)
			if err != nil {
				return 0, err
			}
			f, _ := new(big.Float).SetInt(b).Float64()
			return f, nil
		}
	}
	// a key's floor: what a decision's header needs at the emergency cap (E2-RT8, E4-FC5) — the updater's, which buys it,
	// and the relayers', its backstop when the updater's key cannot (E4-RT1)
	floor := func(name, key, addr string, say func(string, *big.Int, uint64, *big.Int) (string, bool)) {
		b, err := m.pair.A.BalanceAt(ctx, Address(addr), ethx.Latest)
		if err != nil {
			return
		}
		gas := m.Cfg.Eth.UpdateGas
		if gas == 0 {
			gas = headerGas(6, 5)
		}
		text, short := say(addr, b, gas, m.Cfg.EmergencyMaxBaseFee())
		m.mu.Lock()
		m.status.Floats[name] = text
		m.mu.Unlock()
		if short {
			m.Alarm.Alarm(key, text)
		} else {
			m.Alarm.Clear(key)
		}
	}
	if m.Cfg.Eth.Relayer != "" {
		floats = append(floats, float{"relayer-eth", wei(Address(m.Cfg.Eth.Relayer))})
		floor("relayer-floor", "float:relayer", m.Cfg.Eth.Relayer, relayerFloatText)
	}
	if m.Cfg.Eth.Updater != "" {
		floats = append(floats, float{"updater-eth", wei(Address(m.Cfg.Eth.Updater))})
		floor("updater-floor", "float:updater", m.Cfg.Eth.Updater, updaterFloatText)
	}
	if m.ethWatch != nil {
		floats = append(floats, float{"watch-eth", wei(m.ethWatch.Address())})
	}
	gnoBal := func(addr string) func() (float64, error) {
		return func() (float64, error) {
			b, err := m.Cfg.SourceClient(m.Cfg.Gno.RPC).Balance(ctx, addr)
			return float64(b), err
		}
	}
	if m.Cfg.Gno.Address != "" {
		floats = append(floats, float{"relayer-gno", gnoBal(m.Cfg.Gno.Address)})
	}
	for _, f := range floats {
		bal, err := f.read()
		if err != nil {
			log.Printf("monitor: runway %s: %v", f.name, err)
			continue
		}
		fl := m.state.Floats[f.name]
		if fl == nil {
			fl = &Float{Name: f.name}
			m.state.Floats[f.name] = fl
		}
		text, short := fl.Observe(m.now(), bal, m.Cfg.Runway)
		m.mu.Lock()
		m.status.Floats[f.name] = text
		m.mu.Unlock()
		if short {
			m.Alarm.Alarm("runway:"+f.name, text)
		} else {
			m.Alarm.Clear("runway:" + f.name)
		}
	}
}

// flagKeys reads the ETH of the gate's six keys — the four guardians' seats and the two watch slots — every few
// minutes and alarms float:key<n> for one under the 0.05 ETH a flag key keeps (E2-FC3: a flag costs 101–112K gas, so
// 0.01 ETH cannot send one above ≈37 gwei, in the spike in which flags must land).
func (m *Monitor) flagKeys(ctx context.Context, snap *ethSnapshot) {
	if m.now().Before(m.keysNext) {
		return
	}
	o := m.objects(ctx, snap)
	if o.gate == nil {
		return
	}
	m.keysNext = m.now().Add(floatEvery)
	for i, k := range o.gate.Keys {
		name, who := fmt.Sprintf("key%d", i+1), fmt.Sprintf("seat %d's key (a guardian)", i+1)
		if i >= 4 {
			who = fmt.Sprintf("watch slot %d's key", i+1)
		}
		if k == (common.Address{}) {
			continue
		}
		b, err := m.pair.A.BalanceAt(ctx, k, ethx.Latest)
		if err != nil {
			log.Printf("monitor: %s's float: %v", who, err)
			continue
		}
		bal, _ := new(big.Float).SetInt(b).Float64()
		text, short := flagKeyText(who, k.Hex(), bal)
		m.mu.Lock()
		m.status.Floats[name] = text
		m.mu.Unlock()
		if short {
			m.Alarm.Alarm("float:"+name, text)
		} else {
			m.Alarm.Clear("float:" + name)
		}
	}
}

// monSeatOf is the seat (1–6) an address holds in hlmailbox's list, 0 if none.
func monSeatOf(seats []string, addr string) int {
	if addr == "" {
		return 0
	}
	for i, s := range seats {
		if s == addr {
			return i + 1
		}
	}
	return 0
}

// Handler serves GET /v1/status from the last published snapshot.
func (m *Monitor) Handler() http.Handler { return m.box.handler() }

// Status is the monitor's last published snapshot.
func (m *Monitor) Status() map[string]any { return m.box.get() }

// buildStatus is the monitor's state for its status endpoint: the identity line per route first; built of copies.
func (m *Monitor) buildStatus() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	identity := map[string]any{}
	for sym, e := range m.status.Identity {
		entry := copyMap(e.(map[string]any))
		if m.stale != "" {
			entry["stale"] = m.stale
		}
		identity[sym] = entry
	}
	rec := m.state.Record
	var mismatch any
	if rec.Mismatch != nil {
		mismatch = *rec.Mismatch
	}
	return map[string]any{
		"identity": identity, "cut": m.status.Cut, "sources": m.status.Sources, "oddNode": m.state.OddNode, "stale": m.stale,
		"floats": copyMap(m.status.Floats), "flags": append([]string{}, m.status.Flags...), "waiting": copyMap(m.status.Waiting), "dust": copyMap(m.status.Dust), "seat": m.seat,
		"client": copyMap(m.status.Client), "gate": copyMap(m.status.Gate), "admin": copyMap(m.status.Admin), "decisions": copyMap(m.status.Decisions), "migrations": copyMap(m.status.Migrations),
		"record":    map[string]any{"count": rec.Count, "checked": rec.Checked, "mismatch": mismatch, "gap": rec.Gap, "cursor": rec.Cursor, "recent": append([]recordEntry{}, rec.Recent...)},
		"burns":     map[string]any{"cursor": m.state.Index.Cursor, "indexed": len(m.state.Index.Burns) + len(m.state.Index.folded()), "folded": len(m.state.Index.folded()), "waiting": m.state.Index.waiting(), "ignored": m.state.Index.IgnoredN, "launchLine": m.state.Index.Line},
		"indexBase": indexBaseStatus(m.state.IndexBase), "claimsBeforeBase": m.status.ClaimsBeforeBase, "floor": m.state.Floor,
		"sendsSet": m.state.SendsSet, "ethFlags": copyMap(m.state.EthFlags),
		"polls": m.status.Polls, "lastPoll": m.status.LastPoll, "lastErr": m.status.LastErr,
		"alarms": m.Alarm.Standing(), "node": ethx.Redact(m.Cfg.Gno.RPC),
	}
}

// indexBaseStatus is where the burn index begins, for the status (nil until read).
func indexBaseStatus(b *indexBase) any {
	if b == nil {
		return nil
	}
	return map[string]any{"nonce": b.Nonce, "line": b.Line, "missed": b.Missed, "fromHeight": b.FromHeight, "checkpointed": b.checkpointed()}
}

// hexOf is 32 bytes in hex.
func hexOf(b [32]byte) string { return hex.EncodeToString(b[:]) }

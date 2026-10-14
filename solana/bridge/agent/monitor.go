package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

const (
	monitorEvery  = 10 * time.Second
	stallAfter    = 30 * time.Minute // no delivery with a message waiting
	ageAfter      = 2 * time.Hour    // a message waiting this long
	ownLagAfter   = 5 * time.Minute  // the own node's head older than this: no verdict
	cutFailAfter  = 2 * time.Minute  // the order never holding for this long
	shortPollsMin = 3                // a short right side alarms once it has stood this many polls
	flagGas       = 45_000_000
	// solFlagEvery is how often the Solana flag is re-asserted while a condition stands (§5.9's "a fresh slot
	// on every poll", read as once a minute: every 10 s at 5,000 lamports drained the watch key's 0.05 SOL in a
	// day, G12).
	solFlagEvery = time.Minute
)

// Monitor is one of the two watch machines of PLAN.md §5.9 (one binary, run on each): every 10 s it takes the
// Solana snapshot from two agreeing providers (the supplies, the client's trusted height T and record), then
// reads gno.land through its own node at a height at or above T and after the snapshot — or at the node's head
// when the client is past it inside an hour of blocks — rebuilds the dispatch index, and judges the supply
// identity of every route, the diverted credits, the sources against one another (the third-source rule), the
// client's header record against the chain (the fork rule), every recorded decision against r/hlgov's, the
// stalls, the ages, the runways and the lags. Every Solana reading a flag rests on comes from both providers
// alike (G6). It sets the watch key's flags — credits and payouts on gno and the pausable ISM on an invariant
// break; the pausable ISM and the admin program on a header mismatch, a gap or a foreign decision; sends alone
// on gno while deliveries are paused — and alarms for everything else, so a pause can never feed its own flag.
type Monitor struct {
	Cfg   *Config
	State string
	Every time.Duration
	Alarm *Alarmer
	// RecordFrom is where an empty record starts (G29): "client" takes the client's own counter, running hash
	// and ring at the first read as the base, for an install on a provider without the full history; "" builds
	// the record from the deploy.
	RecordFrom string

	own      gnoReader
	watch    gnoCaller // the own node, signing with the watch key
	sources  []*gno.Client
	sol      [2]*rpc.Client
	both     solReader   // the two providers, agreeing
	solWatch *sol.Client // the Solana watch key, if this machine holds one
	now      func() time.Time

	mu          sync.Mutex
	state       monitorState
	status      monitorStatus
	seat        int // the watch key's seat in hlmailbox, 0 if none
	seen        map[string]time.Time
	short       map[string]int
	start       time.Time
	lastSolFlag time.Time
	box         statusBox
	// migs are the open migrations by symbol, read each poll (AQ3), and migErrs the tokens whose line could not
	// be read this poll (G44: no verdict for that token, the rest of the poll runs); admins the admin program's
	// line of succession (AQ6); diverted the ids whose diverted-credit flag stands (G35: cleared once the claim
	// is voided or gone); stale why the last poll gave no verdict, "" after a clean one (G30).
	migs     map[string]*migration
	migErrs  map[string]error
	admins   *adminChain
	diverted map[string]bool
	stale    string
}

type dispatchRec struct {
	Symbol    string `json:"symbol"`
	Nonce     uint32 `json:"nonce"`
	Slot      uint64 `json:"slot"`
	To        string `json:"to"`
	Amount    int64  `json:"amount"`
	Bad       bool   `json:"bad,omitempty"` // the recipient is no gno address (§13): the realm sweeps the burn
	Credited  int64  `json:"credited"`      // the gno height it was credited at, 0 while waiting
	Checked   bool   `json:"checked"`       // the diverted-credit check was made
	Seen      int64  `json:"seen"`          // unix time first seen
	Providers int    `json:"providers"`     // a bit per provider that showed it
	Sig       string `json:"sig,omitempty"` // the transaction it was first seen in (read again through both providers while disputed)
	// Disputed says what the second provider showed differently (G17: the slot, the amount or the recipient):
	// a disputed record counts for nothing — neither in flight nor credited after the snapshot — until a read
	// through both providers agrees; hlwatch prints it UNCONFIRMED.
	Disputed string `json:"disputed,omitempty"`
}

type sendRec struct {
	Symbol    string `json:"symbol"`
	Amount    int64  `json:"amount"`
	Height    int64  `json:"height"`
	Processed bool   `json:"processed"`
	Seen      int64  `json:"seen"`
	ID        string `json:"id,omitempty"` // the message id (hex), the key of the route's send record
	// Refunded says GovDAO's refund decision paid the sender back on gno.land (AQ2): the amount left the lock, so
	// the send is in flight no more; while it still sits undelivered in hlmailbox it must never deliver.
	// RefundRead is when its mark was last read (F23: read once, then only when the token's refund count rose).
	// WrittenOff says GovDAO booked a send Solana processed after its refund as lost (AR1: the double mint's cure).
	Refunded   bool  `json:"refunded,omitempty"`
	RefundRead int64 `json:"refundRead,omitempty"`
	WrittenOff bool  `json:"writtenOff,omitempty"`
}

// DoubleMinted says whether a send was refunded on gno.land and processed on Solana after all, with no write-off
// yet (AR1): the supply is above the lock by its amount until GovDAO books it as lost.
func (s *sendRec) DoubleMinted() bool { return s.Refunded && s.Processed && !s.WrittenOff }

// InFlight says whether a send still counts toward the identity's in-flight amount: undelivered and not
// refunded.
func (s *sendRec) InFlight() bool { return !s.Processed && !s.Refunded }

type monitorState struct {
	Cursors          map[string]string       `json:"cursors"`  // "SYM#i" → provider i's scan cursor
	Skipped          map[string][]scanSkip   `json:"skipped"`  // "SYM#i" → the transactions provider i could not read (retried each poll)
	Index            map[string]*dispatchRec `json:"index"`    // Solana dispatches toward gno.land, by id
	Sends            map[string]*sendRec     `json:"sends"`    // gno.land messages toward Solana, by nonce
	SendNext         uint32                  `json:"sendNext"` // the next hlmailbox nonce to read
	Floats           map[string][]sample     `json:"floats"`
	LastDelivery     int64                   `json:"lastDelivery"`
	OddNode          bool                    `json:"oddNode"` // own node is the odd one out among the sources
	Record           ClientRecord            `json:"record"`  // the client's header record, rebuilt
	DecisionsChecked uint64                  `json:"decisionsChecked"`
	SendsSet         bool                    `json:"sendsSet"`    // the monitor's own sends flags stand (deliveries paused)
	SolFlagsSet      int                     `json:"solFlagsSet"` // flag transactions sent to the Solana objects
	// ClaimsFrom is where each realm's claims walk resumes (G34); CapReached which tokens' locks reached the
	// expected first-week lock (C341); RefundCounts each token's refund count at the last poll (F23: the marks
	// are read again when it rises).
	ClaimsFrom   map[string]int   `json:"claimsFrom,omitempty"`
	CapReached   map[string]bool  `json:"capReached,omitempty"`
	RefundCounts map[string]int64 `json:"refundCounts,omitempty"`
	// LineEnds is each token's line's last realm at the last poll (M1h, AR2: a migrate re-pointed before page 0
	// changes it, alarmed).
	LineEnds map[string]string `json:"lineEnds,omitempty"`
	// IndexFromHeight is the own node's head at the checkpoint the dispatch index started from (G49, AS3: a claim
	// credited at or below it is trusted, not judged by the forged-credit rule); IndexSeeded whether the index
	// was seeded whole from the mailbox's dispatched-message accounts (the rule then judges every later claim);
	// IndexSeedTried when the seed was last tried, retried hourly while unseeded.
	IndexFromHeight int64 `json:"indexFromHeight,omitempty"`
	IndexSeeded     bool  `json:"indexSeeded,omitempty"`
	IndexSeedTried  int64 `json:"indexSeedTried,omitempty"`
}

type monitorStatus struct {
	Identity   map[string]any    `json:"identity"`
	Cut        string            `json:"cut"`
	Sources    string            `json:"sources"`
	Floats     map[string]string `json:"floats"`
	Flags      []string          `json:"flags"`
	Waiting    map[string]int    `json:"waiting"`
	Client     map[string]any    `json:"client"`
	Pausable   map[string]any    `json:"pausable"`
	Decisions  map[string]any    `json:"decisions"`
	Migrations map[string]string `json:"migrations"`
	Polls      int               `json:"polls"`
	LastPoll   string            `json:"lastPoll"`
	LastErr    string            `json:"lastErr"`
	// ClaimsBeforeBase counts the waiting claims the forged-credit rule passed over this poll as credited at or
	// below the checkpoint's height (G49: before the checkpoint, not judged).
	ClaimsBeforeBase int `json:"claimsBeforeBase"`
}

func (m *Monitor) load() error {
	if err := readJSON(m.State, &m.state); err != nil {
		return err
	}
	s := &m.state
	if s.Cursors == nil {
		s.Cursors = map[string]string{}
	}
	if s.Skipped == nil {
		s.Skipped = map[string][]scanSkip{}
	}
	if s.Index == nil {
		s.Index = map[string]*dispatchRec{}
	}
	if s.Sends == nil {
		s.Sends = map[string]*sendRec{}
	}
	if s.Floats == nil {
		s.Floats = map[string][]sample{}
	}
	if s.ClaimsFrom == nil {
		s.ClaimsFrom = map[string]int{}
	}
	if s.CapReached == nil {
		s.CapReached = map[string]bool{}
	}
	if s.RefundCounts == nil {
		s.RefundCounts = map[string]int64{}
	}
	if s.LineEnds == nil {
		s.LineEnds = map[string]string{}
	}
	return nil
}

func (m *Monitor) save() error {
	if m.State == "" {
		return nil
	}
	return writeJSON(m.State, m.state)
}

func (m *Monitor) init() error {
	if m.now == nil {
		m.now = time.Now
	}
	if m.Every == 0 {
		m.Every = monitorEvery
	}
	m.seen = map[string]time.Time{}
	m.short = map[string]int{}
	m.migs = map[string]*migration{}
	m.migErrs = map[string]error{}
	m.diverted = map[string]bool{}
	m.start = m.now()
	m.status.Identity = map[string]any{}
	m.status.Floats = map[string]string{}
	m.status.Waiting = map[string]int{}
	m.status.Migrations = map[string]string{}
	return m.load()
}

// Run polls until ctx ends.
func (m *Monitor) Run(ctx context.Context) error {
	if err := m.init(); err != nil {
		return err
	}
	urls, err := m.Cfg.TwoProviders()
	if err != nil {
		return err
	}
	m.sol = [2]*rpc.Client{sol.NewRPC(urls[0]), sol.NewRPC(urls[1])}
	m.both = twoProviders(m.sol)
	if m.own == nil {
		m.own = m.Cfg.SourceClient(m.Cfg.Gno.RPC)
	}
	if m.watch == nil {
		m.watch = m.Cfg.WatchClient()
	}
	for _, s := range m.Cfg.Gno.Sources {
		m.sources = append(m.sources, m.Cfg.SourceClient(s))
	}
	if len(m.sources) == 0 {
		log.Printf("monitor: WARNING no second gno source (gno.sources): the sources rule cannot run; §5.9 wants rpc.gno.land at least")
	}
	if m.Cfg.Gno.WatchKey == "" {
		log.Printf("monitor: WARNING no watch key (gno.watchKey): a break alarms, flags cannot be set")
	}
	if m.Cfg.Solana.WatchKey != "" {
		k, err := sol.PrivateKeyFromFile(m.Cfg.Solana.WatchKey)
		if err != nil {
			return err
		}
		m.solWatch = sol.NewClient(urls[0], k)
		log.Printf("monitor: Solana watch key %s", k.PublicKey())
	} else {
		log.Printf("monitor: WARNING no Solana watch key (solana.watchKey): the Solana objects cannot be flagged from here")
	}
	if !m.Cfg.HasPrograms() {
		log.Printf("monitor: WARNING no light client in the config (solana.client/pausable/admin): the Solana-side rules do not run")
	}
	if m.state.LastDelivery == 0 {
		m.state.LastDelivery = m.now().Unix()
	}
	if err := m.recordCheckpoint(ctx); err != nil {
		return err
	}
	log.Printf("monitor: own node %s, sources %v, providers %s and %s, every %s", m.Cfg.Gno.RPC, m.Cfg.Gno.Sources, urls[0], urls[1], m.Every)
	for {
		m.afterPoll(m.poll(ctx))
		if err := m.save(); err != nil {
			log.Printf("monitor: state: %v", err)
		}
		m.box.publish(m.buildStatus())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.Every):
		}
	}
}

// afterPoll sorts a poll's outcome: a poll that fails past the cut's own alarms is no verdict either (G30: an
// hour of failing renders or a rate-limited provider was a log line and a status field, while the signers alarm
// and halt in the same hour) — an alarm once it has stood two minutes, the identity marked stale meanwhile; a
// clean poll clears both.
func (m *Monitor) afterPoll(err error) {
	if err == nil {
		m.stale = ""
		m.clearPersisted("monitor:no-verdict")
		return
	}
	log.Printf("monitor: %v", err)
	m.mu.Lock()
	m.status.LastErr = err.Error()
	m.mu.Unlock()
	m.stale = m.now().UTC().Format(time.RFC3339) + ": " + err.Error()
	m.persist("monitor:no-verdict", "no verdict: the poll fails ("+err.Error()+"); the identity, the stalls and the runways are not judged until it passes", cutFailAfter)
}

// judgeHead applies judgeOwnHead to the own node's head: no verdict (the cut's alarm after two minutes) while
// it is no reading of the chain, and gno:own-lag raised while it serves an old head (G35: cleared after a good
// reading, never raised before).
func (m *Monitor) judgeHead(head gno.Head, snapAt time.Time) bool {
	ok, why := judgeOwnHead(head, snapAt, m.now())
	if !ok {
		m.setCut("no verdict: " + why)
		m.persist("gno:own", why, cutFailAfter)
		if !head.CatchingUp && m.now().Sub(head.Time) > ownLagAfter {
			m.Alarm.Alarm("gno:own-lag", fmt.Sprintf("own node's head %d is %s old (over %s): it serves an old chain — %s", head.Height, m.now().Sub(head.Time).Round(time.Second), ownLagAfter, why))
		}
		return false
	}
	m.clearPersisted("gno:own")
	m.Alarm.Clear("gno:own-lag")
	return true
}

// judgeOwnHead is the monitor's reading of its own node's head against the snapshot (G3): no verdict while the
// node is catching up, while its head is not after the snapshot (the cut's order), or while its head is older
// than the lag allowance — a node that just came back serves an old head, which is no reading of the chain.
func judgeOwnHead(head gno.Head, snapAt, now time.Time) (ok bool, why string) {
	switch {
	case head.CatchingUp:
		return false, fmt.Sprintf("own node is catching up (head %d)", head.Height)
	case !head.Time.After(snapAt):
		return false, fmt.Sprintf("own node's head %d (%s) is not after the Solana snapshot (%s)", head.Height, head.Time.UTC().Format(time.RFC3339), snapAt.UTC().Format(time.RFC3339))
	case now.Sub(head.Time) > ownLagAfter:
		return false, fmt.Sprintf("own node's head %d is %s old (over %s): no reading of the chain", head.Height, now.Sub(head.Time).Round(time.Second), ownLagAfter)
	}
	return true, ""
}

// poll is one round, in §5.9's order.
func (m *Monitor) poll(ctx context.Context) error {
	m.mu.Lock()
	m.status.Polls++
	m.status.LastPoll = m.now().UTC().Format(time.RFC3339)
	m.status.LastErr = ""
	m.mu.Unlock()

	// 1. The Solana snapshot, both providers agreeing: the supplies, then the client's trusted height T.
	snap, err := solSupplies(ctx, m.sol, m.Cfg.Routes)
	if err != nil {
		m.persist("solana:providers", "the Solana snapshot failed: "+err.Error(), cutFailAfter)
		return err
	}
	var cs *clientState
	if m.Cfg.HasPrograms() {
		if cs, err = m.readClientBoth(ctx); err != nil {
			m.persist("solana:providers", "the Solana snapshot failed: "+err.Error(), cutFailAfter)
			return err
		}
		if cs.Slot < snap.Slot {
			snap.Slot = cs.Slot
		}
	}
	m.clearPersisted("solana:providers")

	// 2. gno.land's head, waited for until it is after the snapshot; catching up, stale or not after it is no
	// verdict (gno:own); the client's trusted height T against it.
	head, err := headAfter(ctx, m.own, snap.At)
	if err != nil {
		m.persist("gno:own", "own node: "+err.Error(), cutFailAfter)
		return err
	}
	if !m.judgeHead(head, snap.At) {
		return nil
	}
	if cs != nil && uint64(head.Height) < cs.Head.TrustedHeight {
		T := cs.Head.TrustedHeight
		allowance := m.Cfg.AheadAllowance()
		if T-uint64(head.Height) > allowance {
			// past the allowance: judged as the signer judges it (§5.6), with a second source, and persisted
			// before any flag; the identity is still judged at the head (AN2)
			second, secondOK := m.secondHead(ctx)
			v, why := judgeAhead(T, uint64(head.Height), second, secondOK, allowance)
			switch v {
			case aheadStop:
				if m.persisted("header:ahead", cutFailAfter) {
					m.flagSolana(ctx, "header:mismatch", "header mismatch: the client is past the chain: "+why, true)
				} else {
					m.Alarm.Alarm("header:ahead", "the client is past the chain (flagged once it has stood "+cutFailAfter.String()+"): "+why)
				}
			default:
				m.setCut("no verdict: " + why)
				m.persist("gno:own", why, cutFailAfter)
				return nil
			}
		} else {
			m.clearPersisted("header:ahead")
		}
	} else {
		m.clearPersisted("header:ahead")
	}
	cut := Cut{SolSlot: snap.Slot, SolTime: snap.At, GnoHeight: head.Height, GnoTime: head.Time}
	if ok, why := cut.Holds(); !ok {
		m.setCut("no verdict: " + why)
		m.persist("cut", "the order never holds: "+why, cutFailAfter)
		return nil
	}
	H := head.Height

	// 3. The sources against one another at H (the third-source rule).
	m.judgeSources(ctx, H, head.Hash)

	// 4. gno.land at H: the mailbox and the routes.
	mb, err := mailboxAt(ctx, m.own, m.Cfg.Gno.Mailbox, H)
	if err != nil {
		return err
	}
	m.seat = seatOf(mb.Seats, m.Cfg.Gno.WatchAddress)
	routes := map[string]*routeJSON{}
	for _, r := range m.Cfg.Routes {
		rj, err := routeAt(ctx, m.own, r.Gno, H)
		if err != nil {
			return err
		}
		routes[r.Symbol] = rj
	}
	// the open migrations (AQ3, K28): each token is judged over its line, a successor mid-import alarmed; a
	// token whose line could not be read is no verdict this poll, the rest runs (G44)
	migs := m.readMigrations(ctx, routes, mb, H)

	// 5. The dispatch index (both providers) and the sends, then what each side has delivered at H / at the slot.
	m.retrySeed(ctx) // a checkpointed index still unseeded lists the mailbox's accounts again, hourly (G49)
	if err := m.index(ctx, mb.Domain); err != nil {
		return err
	}
	if err := m.sends(ctx, mb, routes, H); err != nil {
		m.solErr("the sends", err)
	}
	inflightIn, inflightOut := map[string]int64{}, map[string]int64{}
	delivered := 0
	for id, rec := range m.state.Index {
		if rec.Disputed != "" {
			continue // no reading of Solana (G17): out of the cut and the identity until the providers agree
		}
		if rec.Credited == 0 {
			at, err := deliveredAt(ctx, m.own, m.Cfg.Gno.Mailbox, id, H)
			if err != nil {
				return err
			}
			if at != 0 {
				rec.Credited = at
				delivered++
			}
		}
		if rec.Credited != 0 && rec.Slot > snap.Slot {
			cut.CreditedAfterSlot = append(cut.CreditedAfterSlot, id)
		}
		if rec.Credited == 0 && rec.Slot <= snap.Slot {
			inflightIn[rec.Symbol] += rec.Amount
		}
	}
	for _, s := range m.state.Sends {
		if s.InFlight() { // a refunded send left the lock: in flight no more (AQ2)
			inflightOut[s.Symbol] += s.Amount
		}
	}
	if delivered > 0 {
		m.state.LastDelivery = m.now().Unix()
	}
	if ok, why := cut.Holds(); !ok {
		m.setCut("no verdict: " + why)
		m.persist("cut", "the order never holds: "+why, cutFailAfter)
		return nil
	}
	m.clearPersisted("cut")
	m.setCut(fmt.Sprintf("slot %d at %s, then height %d at %s", cut.SolSlot, cut.SolTime.UTC().Format(time.RFC3339), cut.GnoHeight, cut.GnoTime.UTC().Format(time.RFC3339)))

	// 6. The identity of every token — over the line's summed counters while a migration is open (AQ3, AR6) —
	// and the diverted credits.
	m.judgeIdentities(ctx, mb, routes, migs, H, snap, inflightOut, inflightIn)
	if err := m.divertedCredits(ctx, mb, H); err != nil {
		return err
	}
	if err := m.unindexedCredits(ctx, mb, H); err != nil {
		return err
	}

	// 7. The Solana side: the header record, the decisions, the pause mirror, the caps against the locks, the
	// client's lag.
	if cs != nil {
		m.solChecks(ctx, cs, mb, routes, H)
	}

	// 8. Stalls and ages; 9. runways.
	m.stalls()
	m.runways(ctx)

	m.mu.Lock()
	m.status.Flags = nil
	for path, rf := range mb.Routes {
		for _, f := range rf.Flags {
			m.status.Flags = append(m.status.Flags, path+": "+f)
		}
	}
	m.mu.Unlock()
	return nil
}

// secondHead is the first other gno source's head (gno.sources[0]: rpc.gno.land in a deployment); ok false when
// none is configured or none answered.
func (m *Monitor) secondHead(ctx context.Context) (uint64, bool) {
	for _, src := range m.sources {
		if head, err := src.Status(ctx); err == nil && head.Height > 0 && !head.CatchingUp {
			return uint64(head.Height), true
		}
	}
	return 0, false
}

// solErr logs a Solana-side reading that failed; a disagreement between the providers is the alarm
// solana:providers (G6: no flag rests on one provider's word; the rule is skipped this poll).
func (m *Monitor) solErr(what string, err error) {
	log.Printf("monitor: %s: %v", what, err)
	if errors.Is(err, errProviders) {
		m.Alarm.Alarm("solana:providers", what+": "+err.Error()+" (the rule is skipped until they agree)")
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
		return false
	}
	return m.now().Sub(first) >= d
}

// clearPersisted ends a persisted condition: the alarm and its first sighting alike (G27: a sighting that
// outlived its clear made every later single-poll failure alarm at once — after one two-minute spell of
// solana:providers, a burst filled the channel with alarm/clear pairs).
func (m *Monitor) clearPersisted(key string) {
	m.Alarm.Clear(key)
	delete(m.seen, key)
}

func (m *Monitor) setCut(s string) {
	m.mu.Lock()
	m.status.Cut = s
	m.mu.Unlock()
}

func (m *Monitor) setIdentity(sym string, id Identity, verdict, text string) {
	m.mu.Lock()
	entry := map[string]any{"lock": id.Lock, "supply": id.Supply, "owed": id.Owed, "voided": id.Voided, "lost": id.Lost,
		"right": id.Right(), "gap": id.Gap(), "inflightOut": id.InflightOut, "inflightIn": id.InflightIn, "slot": id.Slot, "height": id.Height,
		"verdict": verdict, "ok": verdict == Holds.String(), "text": text}
	if mig := m.migs[sym]; mig != nil {
		entry["migration"] = mig.Text()
	}
	m.status.Identity[sym] = entry
	m.mu.Unlock()
}

// readMigrations reads every token's open migration at a height (AQ3, K28, AR6): the line each token is judged
// over, a successor mid-import alarmed (route:importing); a token whose line could not be read — a partner's
// render failing, its path pruned at H, a loop or a line past the bound — is kept in migErrs for the poll: no
// verdict for that token, the other token and the rest of the poll run (G44: before, the whole poll returned at
// this step for as long as the partner's render failed, no identity for either token, no stalls, no runways).
func (m *Monitor) readMigrations(ctx context.Context, routes map[string]*routeJSON, mb *mailboxJSON, H int64) map[string]*migration {
	migs, errs := migrationsOf(ctx, m.own, m.Cfg, routes, mb, H)
	m.migs, m.migErrs = migs, errs
	m.mu.Lock()
	m.status.Migrations = map[string]string{}
	for sym, mig := range migs {
		m.status.Migrations[sym] = mig.Text()
	}
	for sym, err := range errs {
		m.status.Migrations[sym] = "NOT READ: " + err.Error()
	}
	m.mu.Unlock()
	for _, r := range m.Cfg.Routes {
		key := "route:importing:" + r.Symbol
		if mig := migs[r.Symbol]; mig.Importing() {
			m.Alarm.Alarm(key, fmt.Sprintf("%s's successor %s is IMPORTING %s's pages (%d so far): credits are refused there until the last page is in, the token is judged over the line's summed counters, and a burn addressed to a migrated realm waits (K28, AQ1, AQ3)", r.Symbol, mig.Successor, mig.Predecessor, mig.Succ.Imported))
		} else if errs[r.Symbol] == nil {
			m.Alarm.Clear(key)
		}
		// a line that cannot be followed to its end for two minutes is alarmed with its chore (F24): past sixteen
		// migrations, or a loop, the token has no verdict until the config names a later realm
		lineKey := "route:line:" + r.Symbol
		if err := errs[r.Symbol]; err != nil {
			m.persist(lineKey, fmt.Sprintf("%s's line of migrations cannot be followed from the configured realm %s (%v): the token has no verdict, its cap ratio, diverted and unindexed rules skip it — past sixteen migrations edit routes[].gno in bridge.json to the live realm; a loop is a forged line, read the realms (F24, G44)", r.Symbol, r.Gno, err), cutFailAfter)
			continue
		}
		m.clearPersisted(lineKey)
		// the line's last realm, re-read each poll (M1h, AR2: a migrate to a successor that pulled nothing is
		// re-pointed before page 0, the first successor leaving the mailbox's routes): a change is alarmed once
		end := ""
		if mig := migs[r.Symbol]; mig != nil {
			end = mig.Successor
		}
		changeKey := "route:successor-changed:" + r.Symbol
		if was, known := m.state.LineEnds[r.Symbol]; known && was != end && was != "" {
			m.Alarm.Alarm(changeKey, fmt.Sprintf("%s's line of migrations changed: its last realm was %s, now %s (a migrate re-pointed before page 0, or a later migration; every rule follows the line as read this poll)", r.Symbol, was, orNone(end)))
		} else {
			m.Alarm.Clear(changeKey)
		}
		m.state.LineEnds[r.Symbol] = end
	}
	return migs
}

// judgeIdentities applies every token's verdict at a poll: over its line while a migration is open (AQ3, AR6);
// a route with no Solana program, or a token whose line could not be read this poll (G44), is no verdict and
// nothing is flagged or cleared for it.
func (m *Monitor) judgeIdentities(ctx context.Context, mb *mailboxJSON, routes map[string]*routeJSON, migs map[string]*migration, H int64, snap *Snapshot, inflightOut, inflightIn map[string]int64) {
	for _, r := range m.Cfg.Routes {
		id := identityOf(r, routes[r.Symbol], migs[r.Symbol], H, snap.Supplies[r.Symbol], snap.Slot, inflightOut[r.Symbol], inflightIn[r.Symbol])
		switch {
		case r.Token == "":
			m.setIdentity(r.Symbol, id, "no verdict", "no Solana program configured for this route")
		case m.migErrs[r.Symbol] != nil:
			m.setIdentity(r.Symbol, id, "no verdict", "the token's line of migrations could not be read this poll (G44): "+m.migErrs[r.Symbol].Error())
		default:
			m.judge(ctx, r, mb, id)
		}
	}
}

// lineUnread says whether a token's line of migrations could not be read this poll (G44): its rules that read
// the line's realms — the identity, the cap ratio, the diverted and unindexed credits — skip it, marking nothing.
func (m *Monitor) lineUnread(symbol string) bool { return m.migErrs[symbol] != nil }

// judge applies one token's verdict: a break flags credits and payouts (through the watch key) on the token's
// live realm — its successor while a migration is open, never the migrated or importing route alone (AQ3) —
// unless the own node is the odd one out, in which case it alarms; a persistent short side alarms; a held
// identity clears the alarms (never the flags: an unpause decision does that).
func (m *Monitor) judge(ctx context.Context, r Route, mb *mailboxJSON, id Identity) {
	v, text := id.Verdict()
	if mig := m.migs[r.Symbol]; mig != nil {
		text += " (" + mig.Text() + ")"
		r.Gno = mig.Live()
	}
	m.setIdentity(r.Symbol, id, v.String(), text)
	key := "identity:" + r.Symbol
	switch v {
	case Broken:
		m.short[r.Symbol] = 0
		m.breakFlags(ctx, r, mb, key, text)
	case Short:
		m.short[r.Symbol]++
		if m.short[r.Symbol] >= shortPollsMin {
			m.Alarm.Alarm(key, "alarm only (a mistaken void, or more in flight than counted): "+text)
		}
	default:
		m.short[r.Symbol] = 0
		m.Alarm.Clear(key)
		m.Alarm.Clear(key + ":flag")
	}
}

// breakFlags is what an invariant break does: gno credits and payouts on the route, and the pausable ISM's flag,
// re-asserted with a fresh slot while the break stands; while the own node is the odd one out, an alarm instead.
func (m *Monitor) breakFlags(ctx context.Context, r Route, mb *mailboxJSON, key, text string) {
	if m.state.OddNode {
		m.Alarm.Alarm(key, "BROKEN but own node is the odd one out among the sources: alarm, no flag: "+text)
		return
	}
	m.Alarm.Flag(key+":flag", "invariant break: credits and payouts flagged on "+r.Gno+": "+text)
	for _, fn := range []string{"credits", "payouts"} {
		m.setFlag(ctx, mb, r.Gno, fn, text)
	}
	if m.Cfg.HasPrograms() {
		m.flagSolana(ctx, key+":solana", "invariant break on "+r.Symbol+": "+text, false)
	}
}

// setFlag sets the watch key's flag on a function of a route unless it stands already (a watch key's flag never
// names ids). Without a watch key, or a seat, it alarms instead.
func (m *Monitor) setFlag(ctx context.Context, mb *mailboxJSON, route, fn, why string) {
	if m.Cfg.Gno.WatchKey == "" || m.seat == 0 {
		m.Alarm.Alarm("flag:"+route+":"+fn, "would set "+fn+" on "+route+" but this machine holds no watch seat (watchKey/watchAddress): "+why)
		return
	}
	if rf, ok := mb.Routes[route]; ok {
		for _, f := range rf.StandingFlags() {
			if f.Seat == m.seat && f.Fn == fn {
				return // standing
			}
		}
	}
	res, err := m.watch.Call(ctx, m.Cfg.Gno.Mailbox, "SetFlag", flagGas, route, fn, "")
	if err != nil {
		m.Alarm.Alarm("flag:"+route+":"+fn, "setting "+fn+" on "+route+" failed: "+err.Error())
		return
	}
	m.Alarm.Clear("flag:" + route + ":" + fn)
	log.Printf("monitor: FLAG %s set on %s (seat %d): %s", fn, route, m.seat, res.Hash)
}

// clearFlag clears the watch key's own flag on a function of a route (sends, which mirrors Solana's delivery
// pause and is cleared when it ends).
func (m *Monitor) clearFlag(ctx context.Context, route, fn string) {
	if m.Cfg.Gno.WatchKey == "" {
		return
	}
	if _, err := m.watch.Call(ctx, m.Cfg.Gno.Mailbox, "ClearFlag", flagGas, route, fn); err != nil {
		log.Printf("monitor: clearing %s on %s: %v", fn, route, err)
		return
	}
	log.Printf("monitor: %s cleared on %s (deliveries resumed)", fn, route)
}

// judgeSources compares the own node's block hash at H with the public source's and, on a conflict, the third
// source's; own-node-odd flags credits (never payouts) on every route.
func (m *Monitor) judgeSources(ctx context.Context, H int64, ownHash string) {
	if len(m.sources) == 0 {
		return
	}
	hashAt := func(c *gno.Client) string {
		b, err := c.Block(ctx, H)
		if err != nil {
			return ""
		}
		return b.Hash
	}
	public := hashAt(m.sources[0])
	third := ""
	if len(m.sources) > 1 {
		third = hashAt(m.sources[1])
	}
	m.applySources(ctx, H, JudgeSources(ownHash, public, third), ownHash, public, third)
}

// applySources acts on the sources' verdict at a height.
func (m *Monitor) applySources(ctx context.Context, H int64, v SourceVerdict, ownHash, public, third string) {
	m.mu.Lock()
	m.status.Sources = fmt.Sprintf("%s at height %d", v, H)
	m.mu.Unlock()
	switch v {
	case SourcesAgree:
		m.state.OddNode = false
		m.Alarm.Clear("sources")
	case SourcesLag:
		// never a flag, never an alarm by itself
	case OwnNodeOdd:
		m.state.OddNode = true
		m.Alarm.Flag("sources", fmt.Sprintf("own node is the odd one out at height %d (own %s, public %s, third %s): credits flagged", H, ownHash, public, third))
		if mb, err := mailboxAt(ctx, m.own, m.Cfg.Gno.Mailbox, 0); err == nil {
			m.seat = seatOf(mb.Seats, m.Cfg.Gno.WatchAddress)
			for _, r := range m.Cfg.Routes {
				m.setFlag(ctx, mb, r.Gno, "credits", "own node odd one out")
			}
		}
		if m.Cfg.HasPrograms() {
			m.flagSolana(ctx, "sources:solana", fmt.Sprintf("own node is the odd one out at height %d: the pausable ISM flagged", H), false)
		}
	default:
		m.Alarm.Alarm("sources", fmt.Sprintf("the sources disagree at height %d (%s): own %s, public %s, third %q", H, v, ownHash, public, third))
	}
}

// index scans both providers for every route's new dispatches toward gno.land and records them by id.
func (m *Monitor) index(ctx context.Context, gnoDomain uint32) error {
	return indexDispatches(ctx, m.sol, m.both, m.Cfg, gnoDomain, m.state.Cursors, m.state.Skipped, m.state.Index, m.now(), m.Alarm)
}

// sends reads hlmailbox's new messages toward the routes' Solana programs and marks the ones Solana has
// processed (the mailbox's processed-message record, finalized, on both providers) and the ones gno.land
// refunded (AQ2).
func (m *Monitor) sends(ctx context.Context, mb *mailboxJSON, routes map[string]*routeJSON, H int64) error {
	delivered, err := readSends(ctx, m.own, m.both, m.Cfg, mb, H, m.state.Sends, &m.state.SendNext, m.now(), sendReads{Routes: routes, Migs: m.migs, RefundCounts: m.state.RefundCounts})
	if delivered > 0 {
		m.state.LastDelivery = m.now().Unix()
	}
	for n, s := range m.state.Sends {
		key := "send:refunded:" + n
		delivered := "refund:delivered:" + s.ID
		switch {
		case s.DoubleMinted():
			// a refunded send Solana processed after its refund (AR1): minted twice — the supply is above the lock
			// by its amount (the identity reads BROKEN by it for good) until GovDAO books it as lost with the
			// route's write-off builder; a standing prompt, daily
			m.Alarm.Clear(key)
			m.Alarm.AlarmEvery(delivered, fmt.Sprintf("gno #%s (%s, %d) was refunded on gno.land and Solana processed it after the refund: minted twice — the supply is above the lock by %d until GovDAO books it as lost with the route's write-off builder (7 days; AR1)", n, s.Symbol, s.Amount, s.Amount), 24*time.Hour)
		case s.Refunded && !s.Processed:
			m.Alarm.AlarmEvery(key, fmt.Sprintf("gno #%s (%s, %d) was refunded on gno.land and still sits undelivered in hlmailbox: it must never deliver (the relayers refuse it; the amount left the lock, AQ2)", n, s.Symbol, s.Amount), 24*time.Hour)
			m.Alarm.Clear(delivered)
		default:
			m.Alarm.Clear(key)
			m.Alarm.Clear(delivered)
		}
	}
	return err
}

// recordCheckpoint takes the client's own record as an empty record's base when RecordFrom says "client" (G29:
// a provider without the full history never built the record from the deploy): the state read through both
// providers byte for byte (G45), the trusted header checked against the own node first — a differing one is a
// mismatch flagged at the checkpoint's slot, never a base (G37) — the ring in the program's order (F21), the
// open stages from the client's accounts (G37) and the index's cursors at the slot (G36). A record that holds
// entries keeps them.
func (m *Monitor) recordCheckpoint(ctx context.Context) error {
	if m.RecordFrom == "" || !m.Cfg.HasPrograms() {
		return nil
	}
	if m.RecordFrom != "client" {
		return fmt.Errorf("monitor: -record-from %q: \"client\" (the client's own counter and running hash as the base) is the one checkpoint", m.RecordFrom)
	}
	if m.state.Record.Count != 0 || m.state.Record.Cursor != "" {
		log.Printf("monitor: the record holds %d entries already: -record-from client ignored", m.state.Record.Count)
		return nil
	}
	cs, err := readClientState(ctx, m.both, m.Cfg.Programs().LightClient(), rpc.CommitmentFinalized)
	if err != nil {
		return fmt.Errorf("monitor: the checkpoint: %w", err)
	}
	return m.applyCheckpoint(ctx, cs)
}

// applyCheckpoint takes a read client state as an empty record's base (recordCheckpoint's work past the read).
// A trusted header the own node's block does not hold is §6.4's fork with its cut at the checkpoint's slot:
// the stroke on the pausable ISM and the admin program at once (G47: before, a webhook line, nothing on chain —
// a monitor rebuilt on a short-history provider the day a forger ran the client one header ahead never stroked),
// and no base.
func (m *Monitor) applyCheckpoint(ctx context.Context, cs *clientState) error {
	mm, err := checkpointBase(ctx, m.own, cs, &m.state.Record)
	if err != nil {
		return fmt.Errorf("monitor: the checkpoint: %w", err)
	}
	if mm != nil {
		text := checkpointMismatchText(mm)
		m.Alarm.Alarm("record:checkpoint", text)
		m.flagSolana(ctx, "header:mismatch", text, true)
		log.Printf("monitor: %s", text)
		return nil // no base: the record is built from what the provider serves, the scan's alarm says the rest
	}
	stages, err := seedStages(ctx, m.both, m.Cfg.Programs().LightClient(), &m.state.Record)
	if err != nil {
		log.Printf("monitor: the checkpoint's stages could not be listed (the transactions' window stands): %v", err)
	}
	cursors, err := seedCursors(ctx, [2]solReader{m.sol[0], m.sol[1]}, m.Cfg, cs.Slot, m.state.Cursors)
	if err != nil {
		return fmt.Errorf("monitor: the checkpoint's cursors: %w", err)
	}
	// the cut's height, and the index seeded whole from the mailbox's accounts where a provider serves the call
	// (G49, AS3); the fallback rule stands on the height until it does
	if head, err := m.own.Status(ctx); err == nil {
		m.state.IndexFromHeight = head.Height
	}
	seeded, seedErr := m.seedIndex(ctx)
	text := checkpointText(cs, stages, cursors, m.state.IndexFromHeight, seeded, seedErr)
	m.Alarm.Alarm("record:checkpoint", text)
	log.Printf("monitor: %s", text)
	return m.save()
}

// seedIndex lists the mailbox's dispatched-message accounts through the pair into the index (G49, AS3) and
// records whether the index is complete; the alarm record:checkpoint:index stands while it is not.
func (m *Monitor) seedIndex(ctx context.Context) (int, error) {
	m.state.IndexSeedTried = m.now().Unix()
	mb, err := mailboxAt(ctx, m.own, m.Cfg.Gno.Mailbox, 0)
	if err != nil {
		return 0, fmt.Errorf("hlmailbox's domain: %w", err)
	}
	n, err := seedDispatches(ctx, m.both, m.Cfg, sol.Mailbox{Program: m.Cfg.SolMailbox()}, mb.Domain, m.state.Index, m.now(), m.Alarm)
	if err != nil {
		m.state.IndexSeeded = false
		m.Alarm.Alarm("record:checkpoint:index", fmt.Sprintf("the mailbox's dispatched-message accounts could not be listed (%v): the dispatch index starts at the checkpoint — a claim credited at or below height %d is trusted, a later unindexed one is alarmed and never flagged (credit:unindexed-before-base) until they list; retried hourly (G49, AS3)", err, m.state.IndexFromHeight))
		return 0, err
	}
	m.state.IndexSeeded = true
	m.Alarm.Clear("record:checkpoint:index")
	return n, nil
}

// retrySeed lists the mailbox's accounts again, hourly, while a checkpointed index stays unseeded.
func (m *Monitor) retrySeed(ctx context.Context) {
	if m.state.IndexFromHeight == 0 || m.state.IndexSeeded || m.now().Unix()-m.state.IndexSeedTried < int64(indexSeedRetry/time.Second) {
		return
	}
	if n, err := m.seedIndex(ctx); err == nil {
		log.Printf("monitor: the dispatch index seeded whole from %d dispatched-message accounts of the mailbox: the forged-credit rule judges every claim credited after height %d now (G49)", n, m.state.IndexFromHeight)
	}
}

// checkpointedHeader compares the client's trusted header with the own node's block at its height on a
// checkpointed install — the record started from the client's own counter (`-record-from client`), base taken
// or refused, so its own check never reaches the trusted header (G47: hlwatch computed cv.Match every view, the
// monitor compared once at startup) — and answers the mismatch for the record's rule, nil when they agree, the
// node has no such block, or the record was built from the deploy (its own Check reaches the header).
func (m *Monitor) checkpointedHeader(ctx context.Context, cs *clientState) *Mismatch {
	if m.RecordFrom != "client" && m.state.Record.FromSlot == 0 {
		return nil
	}
	T := int64(cs.Head.TrustedHeight)
	b, err := m.own.Block(ctx, T)
	if err != nil || b.Hash == "" {
		return nil // no verdict: the node has no block there yet (a client past the chain is judged by the cut's step 2)
	}
	client := hex.EncodeToString(cs.Head.TrustedHash[:])
	if strings.EqualFold(b.Hash, client) {
		return nil
	}
	return &Mismatch{Height: T, Client: client, Chain: strings.ToLower(b.Hash), Slot: cs.Slot}
}

// checkpointedHeaderCheck applies checkpointedHeader's verdict with the record's patience (the tests' entry;
// solChecks does the same when the record holds no mismatch of its own).
func (m *Monitor) checkpointedHeaderCheck(ctx context.Context, cs *clientState) {
	m.recordMismatch(ctx, m.checkpointedHeader(ctx, cs))
}

// indexDispatches scans both providers for every route's new dispatches toward gno.land and records them by id
// (the dispatch index of §5.9, which the monitor and hlwatch both keep): a burn to no gno address is indexed
// with an empty recipient (the realm sweeps it); an amount-0 burn is not indexed (the relayer skips it, §5.5);
// a transaction a provider cannot read is skipped, alarmed and retried from a side list (K5). The second
// provider's sight of an indexed dispatch is compared with the first's (G17): a different slot, amount or
// recipient disputes the record, which then counts for nothing until a read through both providers (both)
// agrees — tried first on every scan.
func indexDispatches(ctx context.Context, providers [2]*rpc.Client, both solReader, cfg *Config, gnoDomain uint32, cursors map[string]string, skipped map[string][]scanSkip, index map[string]*dispatchRec, now time.Time, alarm *Alarmer) error {
	mailbox := sol.Mailbox{Program: cfg.SolMailbox()}
	resolveDisputes(ctx, both, cfg, mailbox, gnoDomain, index, alarm)
	var firstErr error
	for _, r := range cfg.Routes {
		if r.Token == "" {
			continue
		}
		for i, c := range providers {
			key := r.Symbol + "#" + strconv.Itoa(i)
			ds, next, skips, err := solScan(ctx, c, mailbox, r.TokenKey(), cfg.Solana.Domain, gnoDomain, cursors[key])
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s from provider %d: %w", r.Symbol, i+1, err)
				}
				continue
			}
			cursors[key] = next
			more, keep := retrySkipped(ctx, c, mailbox, r.TokenKey(), cfg.Solana.Domain, gnoDomain, skipped[key], now)
			keep = append(keep, skips...)
			if len(keep) > 0 {
				skipped[key] = keep
				alarm.Alarm("scan:unreadable:"+key, fmt.Sprintf("%d transactions of %s on provider %d could not be read (skipped and retried; the index rule waits on them): %s", len(keep), r.Symbol, i+1, skipText(keep)))
			} else {
				delete(skipped, key)
				alarm.Clear("scan:unreadable:" + key)
			}
			for _, d := range append(ds, more...) {
				foldDispatch(index, r.Symbol, i, d, now, alarm)
			}
		}
	}
	return firstErr
}

// foldDispatch records one provider's sight of a dispatch in the index: a new record, or the second provider's
// bit on an existing one — compared first with what the record holds (G17: a different slot, amount or
// recipient disputes it, alarmed under solana:providers:<id>).
func foldDispatch(index map[string]*dispatchRec, symbol string, provider int, d solDispatch, now time.Time, alarm *Alarmer) {
	id := d.IDHex()
	to, amount, bad, ok := d.token()
	if rec, found := index[id]; found {
		if rec.Providers&(1<<provider) == 0 && ok {
			if why := recordDiffers(rec, d.Slot, to, amount); why != "" {
				rec.Disputed = fmt.Sprintf("provider %d shows %s", provider+1, why)
				alarm.Alarm("solana:providers:"+id[:8], fmt.Sprintf("the providers disagree on dispatch %s (%s): %s; it counts for nothing until they agree", id, symbol, rec.Disputed))
			}
		}
		rec.Providers |= 1 << provider
		return
	}
	if !ok || amount == 0 {
		return
	}
	index[id] = &dispatchRec{Symbol: symbol, Nonce: d.Nonce, Slot: d.Slot, To: to, Amount: amount, Bad: bad, Seen: now.Unix(), Providers: 1 << provider, Sig: d.Sig}
}

// recordDiffers compares a record with another provider's sight of its dispatch (G17): the slot, the amount and
// the recipient; "" when they agree.
func recordDiffers(rec *dispatchRec, slot uint64, to string, amount int64) string {
	var parts []string
	if slot != rec.Slot {
		parts = append(parts, fmt.Sprintf("slot %d against %d", slot, rec.Slot))
	}
	if amount != rec.Amount {
		parts = append(parts, fmt.Sprintf("amount %d against %d", amount, rec.Amount))
	}
	if to != rec.To {
		parts = append(parts, fmt.Sprintf("recipient %q against %q", to, rec.To))
	}
	return strings.Join(parts, ", ")
}

// resolveDisputes reads every disputed record's transaction through both providers at once and, when they
// agree, writes the agreed slot, amount and recipient into the record and ends the dispute (G17).
func resolveDisputes(ctx context.Context, both solReader, cfg *Config, mailbox sol.Mailbox, gnoDomain uint32, index map[string]*dispatchRec, alarm *Alarmer) {
	for id, rec := range index {
		if rec.Disputed == "" {
			continue
		}
		r := cfg.RouteBySymbol(rec.Symbol)
		if r == nil || both == nil {
			continue
		}
		k, err := hl.ParseKey32(id)
		if err != nil {
			continue
		}
		d, err := agreedDispatch(ctx, both, mailbox, r.TokenKey(), cfg.Solana.Domain, gnoDomain, rec.Sig, k)
		if err != nil {
			log.Printf("dispatch %s stays disputed: %v", id, err)
			continue
		}
		to, amount, bad, _ := d.token()
		rec.Slot, rec.To, rec.Amount, rec.Bad, rec.Disputed = d.Slot, to, amount, bad, ""
		alarm.Clear("solana:providers:" + id[:8])
		log.Printf("dispatch %s: the providers agree again (slot %d, %d to %s)", id, d.Slot, amount, to)
	}
}

// sendReads is what readSends reads beside the sends: the routes' jsons at the height and their open migrations
// (where a refund mark can be set, and the refund counts that say when one may have been), and the refund counts
// seen at the last poll, persisted in the machine's state (F23).
type sendReads struct {
	Routes       map[string]*routeJSON
	Migs         map[string]*migration
	RefundCounts map[string]int64 // by symbol: the line's refund count at the last read
}

// refundCount is a token's refund count: the route's, or the line's sum while a migration is open.
func refundCount(rj *routeJSON, mig *migration) int64 {
	if mig != nil {
		var n int64
		for _, j := range mig.realms() {
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

// refundable says whether a token's sends can carry a refund mark: a refund is a successor's duty over the sends
// it imported (M1g, AQ2), so only a route that is a successor or migrated can set one — a live route's send is
// never refunded, and its mark is never read (G42).
func refundable(rj *routeJSON, mig *migration) bool {
	return mig != nil || (rj != nil && (rj.IsSuccessor() || rj.MigratedTo != ""))
}

// readSends reads hlmailbox's new messages toward the routes' Solana programs at a height (their ids kept, never
// re-read, F23), marks the ones Solana has processed — the mailbox's processed-message records, finalized,
// fetched by 100 through the reader as §5.7's walk does — and the ones gno.land refunded (AQ2: the route's send
// record under the message id carries the mark; a send never refunded moves to a successor with its page, so the
// line's other realms are read when the route holds none). A mark is read only where one can be set — a route
// that is a successor or migrated (G42) — and only for a send whose mark was never read, or when the token's
// refund count rose since the last poll (F23: under a quarantine's backlog of 500 sends a poll re-read every
// message and every mark, a thousand renders and a thousand provider reads repeating the last poll's answers);
// a processed send's mark is read on a rise too, since a refund landing after the delivery is a double mint (AR1).
// It reports how many it marked processed now. A message that cannot be read is skipped, never the rest (G8);
// the first error comes back at the end.
func readSends(ctx context.Context, own gnoReader, provider solReader, cfg *Config, mb *mailboxJSON, H int64, sends map[string]*sendRec, next *uint32, now time.Time, sr sendReads) (int, error) {
	var firstErr error
	for n := *next; n < mb.Count; n++ {
		s, err := own.RenderAt(ctx, cfg.Gno.Mailbox, "msg/"+strconv.FormatUint(uint64(n), 10), H)
		if err != nil {
			return 0, err // the walk cannot pass a nonce it has not read
		}
		msg, err := parseGnoMsg(s, mb.Domain, n)
		if err != nil {
			return 0, err
		}
		*next = n + 1
		rt := cfg.RouteByToken(solana.PublicKeyFromBytes(msg.Msg.Recipient[:]))
		if rt == nil || msg.Msg.Destination != cfg.Solana.Domain {
			continue
		}
		_, amount, _, err := hl.ParseTokenMessage(msg.Msg.Body)
		if err != nil || amount > 1<<63-1 {
			continue
		}
		sends[strconv.FormatUint(uint64(n), 10)] = &sendRec{Symbol: rt.Symbol, Amount: int64(amount), Height: msg.Height, Seen: now.Unix(), ID: hex.EncodeToString(msg.ID[:])}
	}
	nonces := sortedNonces(sends)
	// an id a state file from before M3h never stored is read once (the message page), then kept
	for _, n := range nonces {
		if s := sends[n]; s.ID == "" {
			nn, _ := strconv.ParseUint(n, 10, 32)
			msg, err := gnoMessage(ctx, own, cfg.Gno.Mailbox, mb.Domain, uint32(nn))
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("message %d: %w", nn, err)
				}
				continue
			}
			s.ID = hex.EncodeToString(msg.ID[:])
		}
	}
	// the processed-message records of every unprocessed send, by 100
	mailbox := sol.Mailbox{Program: cfg.SolMailbox()}
	var pending []string
	for _, n := range nonces {
		if s := sends[n]; !s.Processed && s.ID != "" {
			pending = append(pending, n)
		}
	}
	delivered := 0
	for start := 0; start < len(pending); start += 100 {
		end := min(start+100, len(pending))
		ids := make([][32]byte, 0, end-start)
		for _, n := range pending[start:end] {
			id, _ := hl.ParseKey32(sends[n].ID)
			ids = append(ids, id)
		}
		done, err := solProcessedMany(ctx, provider, mailbox, ids)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("the processed records of messages %s to %s: %w", pending[start], pending[end-1], err)
			}
			continue
		}
		for i, n := range pending[start:end] {
			if done[i] {
				sends[n].Processed = true
				delivered++
			}
		}
	}
	// the refund marks (AQ2): where one can be set, for a send never read or once the token's count rose (F23)
	rose := map[string]bool{}
	counts := map[string]int64{}
	for _, r := range cfg.Routes {
		counts[r.Symbol] = refundCount(sr.Routes[r.Symbol], sr.Migs[r.Symbol])
		last, known := sr.RefundCounts[r.Symbol]
		rose[r.Symbol] = !known || counts[r.Symbol] > last
	}
	failed := map[string]bool{}
	for _, n := range nonces {
		s := sends[n]
		rt := cfg.RouteBySymbol(s.Symbol)
		if rt == nil || s.ID == "" || !refundable(sr.Routes[s.Symbol], sr.Migs[s.Symbol]) {
			continue // a live route's send, which no refund reaches
		}
		switch {
		case s.DoubleMinted():
			// refunded, then processed on Solana after all (AR1): the record is read again once a minute for the
			// write-off that books the amount as lost
			if now.Unix()-s.RefundRead < 60 {
				continue
			}
		case s.Refunded:
			continue // marked for good: a refund is never undone
		case s.RefundRead != 0 && !rose[s.Symbol]:
			continue
		}
		sd, err := sendOnPair(ctx, own, rt.Gno, sr.Migs[s.Symbol], s.ID, H)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("message %s's send record: %w", n, err)
			}
			failed[s.Symbol] = true
			continue
		}
		s.RefundRead = now.Unix()
		if sd == nil {
			continue
		}
		if bool(sd.Refunded) && !s.Refunded {
			s.Refunded = true
			log.Printf("send #%s (%s, %d) was refunded on gno.land: in flight no more, and it must never deliver (AQ2)", n, s.Symbol, s.Amount)
		}
		if bool(sd.WrittenOff) && !s.WrittenOff {
			s.WrittenOff = true
			log.Printf("send #%s (%s, %d), refunded and delivered after all, was written off on gno.land: booked as lost (AR1)", n, s.Symbol, s.Amount)
		}
	}
	for sym, count := range counts {
		if !failed[sym] && sr.RefundCounts != nil {
			sr.RefundCounts[sym] = count // a read that failed keeps the rise for the next poll
		}
	}
	return delivered, firstErr
}

// sortedNonces are a sends map's keys in nonce order.
func sortedNonces(sends map[string]*sendRec) []string {
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

// divertedCredits checks every credited dispatch once: the claim gno.land wrote must name the recipient and
// amount of the Solana dispatch record (verifier-credited claims only; a claim credited by GovDAO's vote is
// skipped, the realm marks it); a burn to no gno address must be a sweep of its amount (K2). A difference is an
// invariant break.
func (m *Monitor) divertedCredits(ctx context.Context, mb *mailboxJSON, H int64) error {
	// a diverted credit's flag alarm ends with its cure (G35): the claim voided by GovDAO, or gone
	for id := range m.diverted {
		rec := m.state.Index[id]
		if rec == nil {
			delete(m.diverted, id)
			m.Alarm.Clear("diverted:" + id + ":flag")
			continue
		}
		r := m.Cfg.RouteBySymbol(rec.Symbol)
		if r == nil || m.lineUnread(rec.Symbol) {
			continue
		}
		c, _, err := claimOnPair(ctx, m.own, r.Gno, m.migs[rec.Symbol], id, H)
		if err != nil {
			return err
		}
		if c == nil || (c.State != "open" && c.Swept == 0) {
			delete(m.diverted, id)
			m.Alarm.Clear("diverted:" + id + ":flag")
		}
	}
	for id, rec := range m.state.Index {
		if rec.Credited == 0 || rec.Checked || rec.Disputed != "" {
			continue
		}
		r := m.Cfg.RouteBySymbol(rec.Symbol)
		if r == nil || m.lineUnread(rec.Symbol) {
			continue // a line not read this poll: the claim may sit on an unread realm, so nothing is marked (G44)
		}
		c, _, err := claimOnPair(ctx, m.own, r.Gno, m.migs[rec.Symbol], id, H) // on the successor too, once migrated (AQ3)
		if err != nil {
			return err
		}
		if c == nil {
			// the mark in hlmailbox and the claim (or the sweep entry) are written in one transaction, and the mark
			// was read at H, so nothing is pending here: the entry is gone from the realm at H — a sweep entry paid by
			// GovDAO's decision (M1f: its claim/<id> answers null once paid), or a claim migrated to a successor —
			// and there is nothing to compare; checked, never a break (a vote's payment is GovDAO's, as a voted
			// credit is)
			rec.Checked = true
			log.Printf("monitor: dispatch %s on %s: no claim or sweep entry at height %d (paid by a GovDAO sweep decision, or migrated): nothing to compare", id, rec.Symbol, H)
			continue
		}
		rec.Checked = true
		if c.By == "vote" {
			continue
		}
		ok := (c.Swept != 0 && c.Swept == rec.Amount) || (c.Swept == 0 && !rec.Bad && c.To == rec.To && c.Amount == rec.Amount)
		if !ok {
			to := rec.To
			if rec.Bad {
				to = "(no gno address: a sweep expected)"
			}
			text := fmt.Sprintf("diverted credit %s on %s: the claim names %s for %d (swept %d), the Solana dispatch %s for %d", id, rec.Symbol, c.To, c.Amount, c.Swept, to, rec.Amount)
			m.diverted[id] = true
			m.breakFlags(ctx, *r, mb, "diverted:"+id, text)
		}
	}
	return nil
}

// unindexedCredits is §5.9's other rule on a credit (AP1): a waiting claim the verifier credited (never one
// GovDAO voted) whose message id neither provider's dispatch index holds is a forged credit — the verdict
// hlwatch's flag -ids computes — and flags credits and payouts on its route, and the pausable ISM, as an invariant
// break does, once it has stood one more poll (a provider's finalized view a few slots behind the delivery is a
// lag, not a forgery). It runs only when both providers' scans succeeded this poll with nothing skipped (G21: an
// id absent from a failed or partial scan is not forged); an id that appears in the index clears its sighting
// and its alarm (the flags set stay for an unpause decision, as every flag does).
func (m *Monitor) unindexedCredits(ctx context.Context, mb *mailboxJSON, H int64) error {
	if len(m.state.Skipped) > 0 {
		return nil
	}
	beforeBase := 0
	for _, r := range m.Cfg.Routes {
		if r.Token == "" || m.lineUnread(r.Symbol) {
			continue
		}
		// the route's waiting claims and, while a migration is open, every realm of its line (AQ3, AR6): the walk
		// resumes where the last poll's stopped and wraps (G34)
		realms := []string{r.Gno}
		if mig := m.migs[r.Symbol]; mig != nil {
			realms = mig.Paths()
		}
		for _, realm := range realms {
			claims, next, _, err := openClaims(ctx, m.own, realm, H, m.state.ClaimsFrom[realm])
			m.state.ClaimsFrom[realm] = next
			if err != nil {
				return err
			}
			for _, c := range claims {
				if c.By == "vote" {
					continue
				}
				id := strings.ToLower(strings.TrimPrefix(c.ID, "0x"))
				key := "credit:unindexed:" + id
				baseKey := "credit:unindexed-before-base:" + id
				if _, ok := m.state.Index[id]; ok {
					if _, seen := m.seen[key]; seen {
						m.clearPersisted(key)
						m.Alarm.Clear(key + ":flag")
					}
					m.Alarm.Clear(baseKey)
					continue
				}
				// a checkpointed index (G49, AS3): a claim credited at or below the cut's height is trusted as the
				// headers below the checkpoint are; one credited after it, while the mailbox's accounts were never
				// listed, is alarmed and never flagged (its burn may predate the checkpoint's slot)
				switch judgeUnindexed(c.Height, m.state.IndexFromHeight, m.state.IndexSeeded) {
				case unindexedBeforeBase:
					beforeBase++
					continue
				case unindexedUnseeded:
					m.Alarm.AlarmEvery(baseKey, beforeBaseText(id, r.Symbol, c.To, c.Amount, c.Height, m.state.IndexFromHeight), 24*time.Hour)
					continue
				}
				m.Alarm.Clear(baseKey)
				if !m.persisted(key, m.Every) {
					continue
				}
				m.breakFlags(ctx, r, mb, key, fmt.Sprintf("forged credit %s on %s: the claim names %s for %d (credited at height %d) and neither provider's dispatch index holds a burn under its id", id, r.Symbol, c.To, c.Amount, c.Height))
			}
		}
	}
	m.mu.Lock()
	m.status.ClaimsBeforeBase = beforeBase
	m.mu.Unlock()
	// a suspect's sighting and flag alarm end with their cure (G35): the claim voided by GovDAO, paid under a
	// vote, or gone — read by id, since the walk may not visit it this poll
	for key := range m.seen {
		if !strings.HasPrefix(key, "credit:unindexed:") {
			continue
		}
		id := strings.TrimPrefix(key, "credit:unindexed:")
		if _, ok := m.state.Index[id]; ok {
			m.clearPersisted(key)
			m.Alarm.Clear(key + ":flag")
			continue
		}
		open, read := false, false
		for _, r := range m.Cfg.Routes {
			if r.Token == "" {
				continue
			}
			c, _, err := claimOnPair(ctx, m.own, r.Gno, m.migs[r.Symbol], id, H)
			if err != nil {
				log.Printf("monitor: the suspect claim %s on %s could not be read (its sighting stands): %v", id, r.Symbol, err)
				continue
			}
			read = true
			if c != nil && c.Swept == 0 && c.State == "open" {
				open = true
				break
			}
		}
		if read && !open {
			m.clearPersisted(key)
			m.Alarm.Clear(key + ":flag")
		}
	}
	return nil
}

// stalls raises the stall-with-backlog (30 minutes) and delivery-age (two hours) alarms. A disputed record is
// no message waiting (G27: a lying second sight whose Sig names no transaction stays disputed and raised the age
// for good); its own alarm, solana:providers:<id>, stands until the providers agree.
func (m *Monitor) stalls() {
	now := m.now()
	waiting := map[string]int{}
	var oldest time.Time
	for _, rec := range m.state.Index {
		if rec.Credited == 0 && rec.Disputed == "" {
			waiting["solana→gno "+rec.Symbol]++
			if t := time.Unix(rec.Seen, 0); oldest.IsZero() || t.Before(oldest) {
				oldest = t
			}
		}
	}
	for _, s := range m.state.Sends {
		if s.InFlight() { // a refunded send waits for nobody (AQ2)
			waiting["gno→solana "+s.Symbol]++
			if t := time.Unix(s.Seen, 0); oldest.IsZero() || t.Before(oldest) {
				oldest = t
			}
		}
	}
	m.mu.Lock()
	m.status.Waiting = waiting
	m.mu.Unlock()
	total := 0
	for _, n := range waiting {
		total += n
	}
	if total == 0 {
		m.Alarm.Clear("stall")
		m.Alarm.Clear("age")
		return
	}
	if since := now.Sub(time.Unix(m.state.LastDelivery, 0)); since > stallAfter {
		m.Alarm.Alarm("stall", fmt.Sprintf("%d messages waiting and no delivery for %s", total, since.Round(time.Minute)))
	} else {
		m.Alarm.Clear("stall")
	}
	if age := now.Sub(oldest); age > ageAfter {
		m.Alarm.Alarm("age", fmt.Sprintf("the oldest waiting message is %s old", age.Round(time.Minute)))
	} else {
		m.Alarm.Clear("age")
	}
}

// runways samples the floats and alarms under 7 days of the trailing rate: the five of §5.5 and the two watch
// keys of this machine (G12: a standing break re-asserts the Solana flag, which spends the watch key's float).
func (m *Monitor) runways(ctx context.Context) {
	type float struct {
		name string
		read func() (int64, error)
	}
	var floats []float
	lamports := func(k solana.PublicKey) func() (int64, error) {
		return func() (int64, error) {
			b, err := m.sol[0].GetBalance(ctx, k, rpc.CommitmentFinalized)
			if err != nil {
				return 0, err
			}
			return int64(b.Value), nil
		}
	}
	if m.Cfg.Solana.Payer != "" {
		if k, err := sol.PrivateKeyFromFile(m.Cfg.Solana.Payer); err == nil {
			floats = append(floats, float{"relayer-sol", lamports(k.PublicKey())})
		}
	}
	if m.Cfg.Solana.Updater != "" {
		if k, err := solana.PublicKeyFromBase58(m.Cfg.Solana.Updater); err == nil {
			floats = append(floats, float{"updater-sol", lamports(k)})
		}
	}
	if m.solWatch != nil {
		floats = append(floats, float{"watch-sol", lamports(m.solWatch.Payer.PublicKey())})
	}
	for _, r := range m.Cfg.Routes {
		if r.Token != "" {
			floats = append(floats, float{"ata-payer-" + r.Symbol, lamports(sol.Synthetic{Program: r.TokenKey()}.ATAPayer())})
		}
	}
	if m.Cfg.Gno.Address != "" {
		addr := m.Cfg.Gno.Address
		floats = append(floats, float{"relayer-gno", func() (int64, error) { return m.Cfg.SourceClient(m.Cfg.Gno.RPC).Balance(ctx, addr) }})
	}
	if m.Cfg.Gno.WatchAddress != "" {
		addr := m.Cfg.Gno.WatchAddress
		floats = append(floats, float{"watch-gno", func() (int64, error) { return m.Cfg.SourceClient(m.Cfg.Gno.RPC).Balance(ctx, addr) }})
	}
	for _, f := range floats {
		bal, err := f.read()
		if err != nil {
			log.Printf("monitor: runway %s: %v", f.name, err)
			continue
		}
		m.state.Floats[f.name] = addSample(m.state.Floats[f.name], sample{At: m.now(), Balance: bal})
		days, rate := runway(f.name, m.state.Floats[f.name], m.Cfg.Runway)
		text := runwayText(f.name, bal, days, rate)
		m.mu.Lock()
		m.status.Floats[f.name] = text
		m.mu.Unlock()
		if days < runwayDays {
			m.Alarm.Alarm("runway:"+f.name, text)
		} else {
			m.Alarm.Clear("runway:" + f.name)
		}
	}
}

// seatOf is the seat (1–6) an address holds in hlmailbox's list, 0 if none.
func seatOf(seats []string, addr string) int {
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

// Status is the monitor's last published snapshot (the loop publishes one at the end of each poll).
func (m *Monitor) Status() map[string]any { return m.box.get() }

// buildStatus is the monitor's state for its status endpoint: the identity line per route first; built by the
// loop, of copies.
func (m *Monitor) buildStatus() map[string]any {
	m.mu.Lock()
	identity := map[string]any{}
	for sym, e := range m.status.Identity {
		entry := copyMap(e.(map[string]any))
		if m.stale != "" {
			entry["stale"] = m.stale // no verdict since (G30)
		}
		identity[sym] = entry
	}
	floats := copyMap(m.status.Floats)
	waiting := copyMap(m.status.Waiting)
	flags := append([]string{}, m.status.Flags...)
	client, pausable, decisions, migrations := copyMap(m.status.Client), copyMap(m.status.Pausable), copyMap(m.status.Decisions), copyMap(m.status.Migrations)
	st := map[string]any{
		"identity": identity, "cut": m.status.Cut, "sources": m.status.Sources, "oddNode": m.state.OddNode, "stale": m.stale,
		"floats": floats, "flags": flags, "waiting": waiting, "seat": m.seat,
		"client": client, "pausable": pausable, "decisions": decisions, "migrations": migrations,
		"record":   map[string]any{"count": m.state.Record.Count, "checked": m.state.Record.Checked, "mismatch": m.state.Record.Mismatch, "gaps": m.state.Record.Gaps, "recent": append([]RecordEntry{}, m.state.Record.Recent...)},
		"sendsSet": m.state.SendsSet, "solFlagsSet": m.state.SolFlagsSet,
		"polls": m.status.Polls, "lastPoll": m.status.LastPoll, "lastErr": m.status.LastErr,
		"alarms": m.Alarm.Standing(), "node": m.Cfg.Gno.RPC, "sources_": append([]string{}, m.Cfg.Gno.Sources...),
	}
	m.mu.Unlock()
	return st
}

package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The exported views of the Solana side for hlwatch (PLAN.md §5.8): everything from the holder's own gno node
// and two agreeing Solana providers — the supply and the dispatch index, the identity with its in-flight
// tolerance, the forged-id rule, the client's headers against gno.land's, the two objects' seats, flags and
// holds, the pending decisions with their maturity, both counters against r/hlgov's, the foreign decisions and
// every void as forged of a claim whose dispatch record matches it. A section the two providers give differently is
// read from the first alone and marked UNCONFIRMED (G6): shown, never acted on — the flag command refuses to
// name ids on it.

// WatchState is what hlwatch keeps between runs: the scan cursors, the dispatch index, the sends, the record.
type WatchState struct {
	Cursors map[string]string `json:"cursors"`
	// Through is the slot each cursor's route was read through, by "SYM#i" (E6-SF1; the monitor's own), and ThroughHosts
	// the providers it was read through, by place (scheme://host)
	Through          map[string]uint64       `json:"through,omitempty"`
	ThroughHosts     []string                `json:"throughHosts,omitempty"`
	Skipped          map[string][]scanSkip   `json:"skipped"`
	Index            map[string]*dispatchRec `json:"index"`
	Sends            map[string]*sendRec     `json:"sends"`
	SendNext         uint32                  `json:"sendNext"`
	Record           ClientRecord            `json:"record"`
	DecisionsChecked uint64                  `json:"decisionsChecked"`
	Foreign          []string                `json:"foreign"`       // findings, kept
	Voids            map[string]bool         `json:"voids"`         // claim ids checked as voids
	ProposalsSeen    map[string]int64        `json:"proposalsSeen"` // GovDAO proposal id → when first seen open (the deadline is counted from it)
	// ForgedSeen when a waiting claim was first seen unindexed (G33: a FORGED CREDIT line waits for its second
	// consecutive poll); CapReached which tokens' locks reached the expected first-week lock (C341: mint:cap-ratio
	// judges the cap from then on).
	ForgedSeen   map[string]int64 `json:"forgedSeen,omitempty"`
	CapReached   map[string]bool  `json:"capReached,omitempty"`
	RefundCounts map[string]int64 `json:"refundCounts,omitempty"` // each token's refund count at the last read (F23)
	// IndexFromHeight is the own node's head at the checkpoint the index started from, IndexSeeded whether the
	// index was seeded whole from the mailbox's dispatched-message accounts, IndexSeedTried when the seed was
	// last tried (G49, AS3; the monitor's own three).
	IndexFromHeight int64 `json:"indexFromHeight,omitempty"`
	IndexSeeded     bool  `json:"indexSeeded,omitempty"`
	IndexSeedTried  int64 `json:"indexSeedTried,omitempty"`
	// Floor is the highest finalized slot a reading rested on (E3-GT9; the monitor's own): a snapshot below it is a
	// provider stepped back, and no reading of Solana.
	Floor uint64 `json:"floor,omitempty"`
	// Credits is hlmailbox's delivered count at the last reading whose cut held, and the windows of credits the index
	// has not placed yet (E4-GT1; the monitor's own).
	Credits creditBook `json:"credits"`
	// Unindexed are hlmailbox's delivered ids the index does not hold, judged by their claims (E4-RL1; the monitor's).
	Unindexed map[string]*unindexedCredit `json:"unindexed,omitempty"`

	minCtx  minContextCheck // the providers' minContextSlot self-check (E5-SF2), in memory: once a run, hourly in a watch
	marksAt time.Time       // when every uncredited mark was last read (markRefresh), in memory
}

func (s *WatchState) init() {
	if s.Cursors == nil {
		s.Cursors = map[string]string{}
	}
	if s.Through == nil {
		s.Through = map[string]uint64{}
	}
	if s.ProposalsSeen == nil {
		s.ProposalsSeen = map[string]int64{}
	}
	if s.Unindexed == nil {
		s.Unindexed = map[string]*unindexedCredit{}
	}
	if s.ForgedSeen == nil {
		s.ForgedSeen = map[string]int64{}
	}
	if s.CapReached == nil {
		s.CapReached = map[string]bool{}
	}
	if s.RefundCounts == nil {
		s.RefundCounts = map[string]int64{}
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
	if s.Voids == nil {
		s.Voids = map[string]bool{}
	}
}

// LoadWatchState reads the state file ("" for none).
func LoadWatchState(path string) (*WatchState, error) {
	s := &WatchState{}
	if path != "" {
		if err := readJSON(path, s); err != nil {
			return nil, err
		}
	}
	s.init()
	return s, nil
}

func (s *WatchState) Save(path string) error {
	if path == "" {
		return nil
	}
	return writeJSON(path, s)
}

// GuardiansView is one object's seats, flags and hold.
type GuardiansView struct {
	Keys      [sol.Keys]string
	Flags     [sol.Keys]*sol.Flag
	HoldSince *sol.Flag
	DipSince  *sol.Flag
	Hold      bool // a hold stands now by the program's rule
	Grace     int64
}

func guardiansView(g *sol.Guardians, now, grace int64) GuardiansView {
	v := GuardiansView{Flags: g.Flags, HoldSince: g.HoldSince, DipSince: g.DipSince, Hold: g.Hold(now, grace) != nil, Grace: grace}
	for i, k := range g.Keys {
		v.Keys[i] = k.String()
	}
	return v
}

// PendingSolDecision is one decision pending on the admin program.
type PendingSolDecision struct {
	Number    uint64
	Kind      byte
	Params    string // hex
	Text      string // what the parameters say (sol.DescribeParams): a cancel with its salt, kind 4 as reserved
	Delivered int64
	MaturesAt int64
	// Floor is a replacement's floor while a hold stands: 21 days after the earliest hold overlapping its pending
	// life, computed from the state's hold history (AN4; nothing is written); 0 with no hold standing.
	Floor     int64
	Deferred  string // what defers it beyond its clock, if anything
	Deliverer string
}

// HoldView is one hold that ended, from the admin state's history (AN4).
type HoldView struct {
	Start, End int64
}

// LimitView is a token's mint limits on the pausable ISM (AN5) with its bucket's level now, and the day cap
// against the token's lock on gno.land (AP6; C341: judged once the lock reached the expected first-week lock).
type LimitView struct {
	Symbol        string
	MaxPerMessage uint64
	DayCap        uint64
	Used          uint64 // the bucket's level now (what drained since the last charge is off)
	UsedAt        int64
	Lock          int64  // the token's lock at the view's height (the pair's sum while a migration is open)
	Expected      int64  // the expected first-week lock the config names (0 for none)
	Ratio         string // the mint:cap-ratio alarm's text when the day cap exceeds a fifth of the lock, "" within it
	Note          string // why the ratio is not judged yet (the lock below the expected one), "" otherwise
	// RouteMaximum is the live route realm's per-message maximum (base units; the line's last realm while a
	// migration is open) and Mismatch the limits:mismatch alarm's text while it exceeds MaxPerMessage (AS4: a send
	// between the two strands on Solana), "" within it.
	RouteMaximum int64
	Mismatch     string
}

// MigrationView is a token's open migration as hlwatch shows it (AQ3, K28): the line's first and last realm
// (AR6: the realms between them are in Line, first to last).
type MigrationView struct {
	Predecessor, Successor string
	Line                   []string
	Importing              bool
	Imported               int
	Ended                  bool
	Text                   string
}

// SolanaView is one reading of the Solana side at a gno height.
type SolanaView struct {
	Slot       uint64
	Supplies   map[string]int64
	Identities map[string]Identity
	Indexed    int
	Unindexed  int // records one provider showed and the other did not (yet)
	Client     ClientView
	Pausable   GuardiansView
	Admin      GuardiansView
	SetupDone  bool
	LastSeq    uint64
	Origin     uint32
	// The admin state's hold history and its pending decision accounts; Predecessor and Inherited for a successor.
	Holds       []HoldView
	PendingN    uint32
	Predecessor string
	Inherited   bool
	// AdminProgram is the admin program every section below reads — the live one of the line followed from the
	// configured id (AQ6), as base58, LiveHex the same as 32 bytes of hex (r/hlgov's recipient is rendered that
	// way), Retired the programs that handed over before it, as hex, oldest first.
	AdminProgram string
	LiveHex      string
	Retired      []string
	// Successor is the program the live admin program handed every authority to (a kind 9 executed on it that the
	// line could not follow), as hex; "" while none (AP3).
	Successor string
	// RecipientInherited says, when r/hlgov's recipient is another admin program than the configured one (a
	// successor), whether that program has inherited (its predecessor's kind 9 executed): nil when it is the
	// configured program, or its state could not be read (AP3: a delivery to a successor before the kind 9
	// answers NotInherited).
	RecipientInherited *bool
	// The pausable ISM's mint limits per token (0 GNOT, 1 BUBBLE).
	Limits  [2]LimitView
	GovLast uint64
	GovNext uint64
	Pending []PendingSolDecision
	Foreign []string
	// Forged are the waiting claims the verifier credited whose message id neither provider's dispatch index
	// holds (AP1: the verdict flag -ids computes, printed for every open claim when both scans are complete; G33:
	// on its second consecutive poll). BeforeBase counts the waiting claims credited at or below the checkpoint's
	// height, passed over ("before the checkpoint, not judged", G49); UnindexedBeforeBase the claims credited
	// after it that the unseeded index lacks — alarmed, never forged (AS3).
	Forged              []string
	BeforeBase          int
	IndexFromHeight     int64
	IndexSeeded         bool
	UnindexedBeforeBase []string
	VoidsMatch          []string
	ClockFactor         int64 // the client's head byte
	// AdminClockFactor is the live admin program's compiled factor (its state's byte 1): a kind 9's successor must
	// compile the same, or the program spends the hand-over (S-23, M2i).
	AdminClockFactor int64
	// Migrations are the tokens whose migration is open, by symbol (AQ3, K28); Refunded the sends GovDAO refunded
	// on gno.land that still sit undelivered in hlmailbox (AQ2: they must never deliver); RefundDelivered the
	// refunded sends Solana processed after all (AR1: minted twice, until written off); Processed every send's
	// message id Solana processed (hex), which a refund proposal's ids are read against.
	Migrations      map[string]MigrationView
	Refunded        []string
	RefundDelivered []string
	Processed       map[string]bool
	// Domain is gno.land's domain (hlmailbox's); Routers each token program's enrolled router for it, by symbol,
	// as read on Solana (F27, AS2: the refund's gate); Cancelled the live admin program's stored cancelled ranges
	// (a kind 6 in one never enrolled anyone).
	Domain      uint32
	Routers     map[string]RouterView
	Cancelled   []sol.CancelledRange
	Err         []string // what could not be read this round
	Unconfirmed []string // sections the two providers gave differently, read from the first alone (G6)
}

// ClientView is the light client's state and its record against the chain.
type ClientView struct {
	TrustedHeight   uint64
	TrustedHash     string
	ChainHash       string // the own node's hash at that height ("" when it has none)
	Match           bool
	Records         uint64
	LastAccepted    time.Time
	QuarantineEnd   int64
	SetFrom         int64
	SetSize         int
	NextSet         bool
	NextSetSize     int
	SetBound        string // the client:set-bound alarm's text when the current or announced set nears the bound (G19), "" below it
	RecordCount     uint64
	RecordChecked   uint64
	RecordUnchecked int // entries above this node's head, awaiting it (G15)
	RecordOK        bool
	RecordWhy       string
	Mismatch        *Mismatch
}

// ForgedVerdict is §5.8's rule for an id: "forged" only when both providers' scans found no dispatch record
// for it and the route's identity is broken by at least the claim's amount; "unindexed, re-read" otherwise; a
// record found says so, a disputed one that the providers disagree on it (G17).
func (v *SolanaView) ForgedVerdict(st *WatchState, id string, symbol string, amount int64) string {
	id = strings.ToLower(strings.TrimPrefix(id, "0x"))
	if rec, ok := st.Index[id]; ok {
		if rec.Disputed != "" {
			return fmt.Sprintf("has a dispatch record the providers dispute (%s): UNCONFIRMED, re-read", rec.Disputed)
		}
		if rec.Providers != 3 && rec.Credited == 0 {
			return fmt.Sprintf("has a dispatch record provider %d alone shows (%d to %s at slot %d): UNCONFIRMED, re-read (E5-SF4)", rec.Providers, rec.Amount, rec.To, rec.Slot)
		}
		return fmt.Sprintf("has a dispatch record: %d to %s at slot %d (providers %d)", rec.Amount, rec.To, rec.Slot, rec.Providers)
	}
	if ident, ok := v.Identities[symbol]; ok {
		if verdict, _ := ident.Verdict(); verdict == Broken && -ident.Gap() >= amount {
			return "forged: no dispatch record on either provider and the identity is broken by at least its amount"
		}
	}
	return "unindexed, re-read"
}

// Snapshot is Solana's supplies at a slot, both providers agreeing: the cut's first step (§5.9), which hlwatch
// takes before it reads gno.land's head (G33: read after the head, the lock at H lacked the sends whose mints
// the supply held, and a catching-up node read as BROKEN).
type Snapshot = supplySnapshot

// TakeSnapshot reads every route's synthetic supply from both providers (exported for hlwatch).
func TakeSnapshot(ctx context.Context, providers [2]*rpc.Client, cfg *Config) (*Snapshot, error) {
	return solSupplies(ctx, providers, cfg.Routes)
}

// GnoReader is the read side of a gno node for the commands (a fake in hlwatch's tests).
type GnoReader = gnoReader

// HeadAfter is the cut's second step for the commands: the own node's head once its block time is after the
// snapshot (G33).
func HeadAfter(ctx context.Context, g GnoReader, after time.Time) (gno.Head, error) {
	return headAfter(ctx, g, after)
}

// JudgeOwnHead is the monitor's reading of a head against the snapshot, for the commands (G33): a catching-up
// node, a head not after the snapshot or one older than the lag allowance is no reading of the chain.
func JudgeOwnHead(head gno.Head, snapAt, now time.Time) (bool, string) {
	return judgeOwnHead(head, snapAt, now)
}

// ReadSolanaAt reads the gno side's facts the Solana view needs (the mailbox and the routes at a height), then
// the Solana side at a snapshot taken before the height (G33).
func ReadSolanaAt(ctx context.Context, providers [2]*rpc.Client, own gnoReader, cfg *Config, st *WatchState, snap *Snapshot, H int64, now time.Time) (*SolanaView, error) {
	mb, err := mailboxAt(ctx, own, cfg.Gno.Mailbox, H)
	if err != nil {
		return nil, err
	}
	routes := map[string]*routeJSON{}
	for _, r := range cfg.Routes {
		rj, err := routeAt(ctx, own, r.Gno, H)
		if err != nil {
			return nil, err
		}
		routes[r.Symbol] = rj
	}
	return ReadSolana(ctx, providers, own, cfg, st, mb, routes, snap, H, now)
}

// ClaimView is a route's claim by message id, as hlwatch reads one.
type ClaimView struct {
	To     string
	Amount int64
	Paid   int64
	State  string
	Swept  int64
}

// ClaimOf reads a claim by id at a height; nil if the route knows no such id.
func ClaimOf(ctx context.Context, g gnoReader, route, id string, height int64) (*ClaimView, error) {
	c, err := claimAt(ctx, g, route, id, height)
	if err != nil || c == nil {
		return nil, err
	}
	return &ClaimView{To: c.To, Amount: c.Amount, Paid: c.Paid, State: c.State, Swept: c.Swept}, nil
}

// forgedNow are the unindexed claim ids to print and alarm this poll (G33): each on its second consecutive
// poll — seen is the first sightings, kept in the state between runs; an id not among the candidates now is
// forgotten (indexed, paid, voided or gone), and the candidates seen for the first time are recorded.
func forgedNow(seen map[string]int64, candidates []string, now int64) []string {
	var out []string
	keep := map[string]bool{}
	for _, id := range candidates {
		keep[id] = true
		if _, before := seen[id]; before {
			out = append(out, id)
		} else {
			seen[id] = now
		}
	}
	for id := range seen {
		if !keep[id] {
			delete(seen, id)
		}
	}
	return out
}

// ReadSolana reads the Solana side from two providers at a gno height, folding the scans into the state. Each
// section is read through both providers; one they give differently is read again from the first alone and
// marked unconfirmed. snap is the supplies snapshot taken before the height (G33); nil takes one now.
func ReadSolana(ctx context.Context, providers [2]*rpc.Client, own gnoReader, cfg *Config, st *WatchState, mb *mailboxJSON, routes map[string]*routeJSON, snap *Snapshot, H int64, now time.Time) (*SolanaView, error) {
	st.init()
	v := &SolanaView{Supplies: map[string]int64{}, Identities: map[string]Identity{}, Migrations: map[string]MigrationView{}, Domain: mb.Domain, Routers: map[string]RouterView{}}
	both := twoProviders(providers)
	// confirmed runs a section's read through both providers; on a disagreement it runs it again through the
	// first and marks the section unconfirmed. It reports whether the section was read at all.
	confirmed := func(what string, read func(c solReader) error) bool {
		err := read(both)
		if err == nil {
			return true
		}
		if !errors.Is(err, errProviders) {
			v.Err = append(v.Err, what+": "+err.Error())
			return false
		}
		v.Unconfirmed = append(v.Unconfirmed, what+": "+err.Error()+" (shown from provider 1 alone)")
		if err := read(providers[0]); err != nil {
			v.Err = append(v.Err, what+": "+err.Error())
			return false
		}
		return true
	}
	if snap == nil {
		var err error
		if snap, err = solSupplies(ctx, providers, cfg.Routes); err != nil {
			return nil, err
		}
	}
	// the floor (E3-GT9): a snapshot below the highest slot a reading already rested on — kept in the state between
	// runs — is a provider stepped back, and no reading of Solana
	if why := belowFloor(snap.Slot, st.Floor); why != "" {
		return nil, errors.New("no verdict: " + why)
	}
	st.Floor = snap.Slot
	v.Slot = snap.Slot
	v.Supplies = snap.Supplies
	// a provider whose listing ignores minContextSlot (E5-SF2): no identity and no forged-credit line rest on the index
	minCtxWhy := st.minCtx.check(ctx, providers, cfg, snap.Slot, now, &Alarmer{Source: "hlwatch"}, false)
	// the open migrations (AQ3, K28, AR6): each token judged over its line, the successor's import shown; a token
	// whose line could not be read has no identity this round, the other token has (G44)
	migs, migErrs := migrationsOf(ctx, own, cfg, routes, mb, H)
	for _, sym := range sortedKeys(migErrs) {
		v.Err = append(v.Err, "the migration of "+sym+": "+migErrs[sym].Error())
	}
	// hlmailbox's count again after the routes and their lines (E3-GT2): a send dispatched between the reads is in
	// the lock and not walked; the identity has no verdict this reading (the cut is read again after the marks too)
	moved := minCtxWhy
	if again, err := mailboxAt(ctx, own, cfg.Gno.Mailbox, H); err != nil {
		if moved == "" {
			moved = "hlmailbox read again: " + err.Error()
		}
	} else if again.Count != mb.Count && moved == "" {
		moved = movedText(fmt.Sprintf("hlmailbox dispatched %d message(s) while the routes were read (its count %d, then %d: a send between the reads is in the lock and not counted in flight)", again.Count-mb.Count, mb.Count, again.Count), "reading")
	}
	for sym, m := range migs {
		v.Migrations[sym] = MigrationView{Predecessor: m.Predecessor, Successor: m.Successor, Line: m.Paths(), Importing: m.Importing(), Imported: m.Succ.Imported, Ended: m.Ended(), Text: m.Text()}
	}
	alarm := &Alarmer{Source: "hlwatch"}
	// a checkpointed index still unseeded lists the mailbox's accounts again, hourly (G49)
	if st.IndexFromHeight != 0 && !st.IndexSeeded && now.Unix()-st.IndexSeedTried >= int64(indexSeedRetry/time.Second) {
		st.IndexSeedTried = now.Unix()
		if _, err := seedDispatches(ctx, both, cfg, sol.Mailbox{Program: cfg.SolMailbox()}, mb.Domain, st.Index, now, alarm); err == nil {
			st.IndexSeeded = true
		}
	}
	v.IndexFromHeight, v.IndexSeeded = st.IndexFromHeight, st.IndexSeeded
	var gaps historyGaps // a provider that cannot list a route back to where it was read (E6-SF1): this reading's
	indexErr := indexDispatches(ctx, providers, both, cfg, mb.Domain, st.Cursors, st.Through, st.minCtx.honouring(), st.Skipped, st.Index, snap.Slots, now, alarm, &gaps)
	if indexErr != nil {
		v.Err = append(v.Err, "the dispatch index: "+indexErr.Error())
	}
	gapped := gapsOf(gaps.why)
	for p := 1; p <= 2; p++ {
		if text := historyText(gaps.why, p); text != "" {
			v.Err = append(v.Err, "the dispatch index: "+text)
		}
	}
	for key, skipped := range st.Skipped {
		v.Unconfirmed = append(v.Unconfirmed, fmt.Sprintf("the dispatch index of %s: %d transactions unread (%s)", key, len(skipped), skipText(skipped)))
	}
	for _, id := range sortedKeys(st.Index) {
		if rec := st.Index[id]; rec.Disputed != "" {
			v.Unconfirmed = append(v.Unconfirmed, fmt.Sprintf("the dispatch index: %s (%s) is disputed — %s; it counts for nothing until both providers agree (G17)", id, rec.Symbol, rec.Disputed))
		}
	}
	// the sends, their processed marks through both providers alone (E4-SF2: no identity rests on one provider's
	// marks, nor on a mark unread this reading — the marks the providers give differently are read again next one)
	marked := map[string]uint64{}
	if _, err := readSends(ctx, own, both, cfg, mb, H, st.Sends, &st.SendNext, now, sendReads{Routes: routes, Migs: migs, LineErrs: migErrs, RefundCounts: st.RefundCounts, Alarm: alarm, Slots: snap.Slots, Marked: marked}); err != nil {
		if errors.Is(err, errProviders) {
			v.Unconfirmed = append(v.Unconfirmed, "the sends: "+err.Error()+" (read again next reading; no identity rests on one provider's marks)")
		} else {
			v.Err = append(v.Err, "the sends: "+err.Error())
		}
	}
	unread := marksUnread(st.Sends, marked, snap)
	inflightIn, inflightOut := map[string]int64{}, map[string]int64{}
	var creditedAfter []string
	// the burns' delivered marks, each read again only once hlmailbox's delivered count moved (E5-SF5); one that cannot
	// be read leaves no identity this reading (a credited burn counted in flight beside its owed reads over)
	allMarks := len(st.Credits.Open) > 0 || now.Sub(st.marksAt) >= markRefresh
	if _, err := readMarks(ctx, own, cfg.Gno.Mailbox, st.Index, mb.Delivered, H, allMarks); err != nil {
		if moved == "" {
			moved = "the burns' delivered marks: " + err.Error()
		}
	} else if allMarks {
		st.marksAt = now
	}
	unconfirmed := map[string]string{} // by symbol: a burn on one provider's word, or shown differently (E5-SF4, G17)
	for _, id := range sortedKeys(st.Index) {
		rec := st.Index[id]
		v.Indexed++
		if rec.Providers != 3 {
			v.Unindexed++
		}
		slot := snap.SlotOf(rec.Symbol) // against its own route's supply slot (E4-SF3)
		if why := unconfirmedOf(id, rec, slot); why != "" {
			if unconfirmed[rec.Symbol] == "" {
				unconfirmed[rec.Symbol] = why
			}
			if rec.Disputed == "" { // a disputed one has its line above
				v.Unconfirmed = append(v.Unconfirmed, "the dispatch index: "+why+" — UNCONFIRMED: it counts for nothing and "+rec.Symbol+" has no identity until both providers show it (E5-SF4)")
			}
			continue
		}
		if rec.Disputed != "" {
			continue
		}
		if rec.Credited != 0 && rec.Slot > slot {
			creditedAfter = append(creditedAfter, id) // the cut's order (E4-GT1: hlwatch never checked it)
		}
		if rec.Credited == 0 && rec.Slot <= slot {
			inflightIn[rec.Symbol] += rec.Amount
		}
	}
	// the height the delivered count is exact at (E4-GT1), read between the cut's two reads of hlmailbox
	atHead, headErr := own.Status(ctx)
	// every mark read: hlmailbox and the counters held to the first read (E3-GT2) — a credit, a refund or a
	// migration's page landing between the reads makes the reading short (or over): no identity this reading
	if moved == "" {
		why, err := cutMoved(ctx, own, cfg, H, mb, routes, migs, true)
		switch {
		case err != nil:
			moved = "the cut read again after the marks: " + err.Error()
		case why != "":
			moved = movedText(why, "reading")
		}
	}
	// the cut's order (E4-GT1): a burn credited in the gno state above its route's supply slot is in the supply while
	// owed counts it, and so may be a credit no index holds yet — no identity this reading
	if moved == "" {
		switch {
		case headErr != nil:
			moved = "the node's head: " + headErr.Error()
		case len(creditedAfter) > 0:
			sort.Strings(creditedAfter)
			moved = fmt.Sprintf("%d burns credited at height %d were dispatched after their route's supply slot: %s", len(creditedAfter), H, strings.Join(creditedAfter, ","))
		default:
			moved, _ = st.Credits.place(mb.Delivered, atHead.Height, atHead.Time, placedHeights(st.Index, st.Unindexed), snap.Time)
			if moved != "" && placeZeros(ctx, own, cfg, migs, migErrs, mb, H, &st.Credits, st.Index, st.Unindexed) { // a credit of nothing (E5-SF1)
				moved, _ = st.Credits.place(mb.Delivered, atHead.Height, atHead.Time, placedHeights(st.Index, st.Unindexed), snap.Time)
			}
		}
	}
	if moved != "" {
		v.Err = append(v.Err, "the identity: no verdict: "+moved)
	}
	v.Processed = map[string]bool{}
	for _, n := range sortedKeys(st.Sends) {
		s := st.Sends[n]
		if s.Processed && s.ID != "" {
			v.Processed[s.ID] = true
		}
		switch {
		case s.DoubleMinted():
			// refunded on gno.land, then processed on Solana after all (AR1): minted twice until written off
			v.RefundDelivered = append(v.RefundDelivered, fmt.Sprintf("gno #%s (%s, %d) was refunded on gno.land and Solana processed it after the refund: minted twice — the supply is above the lock by %d until GovDAO books it as lost with the route's write-off builder (7 days; AR1)", n, s.Symbol, s.Amount, s.Amount))
		case s.Processed:
			if s.InFlightAt(snap.SlotOf(s.Symbol)) { // processed after its route's supply slot: in flight against it (E3-GT9, E4-SF1)
				inflightOut[s.Symbol] += s.Amount
			}
		case s.Refunded:
			// refunded on gno.land (AQ2): the amount left the lock, so it is in flight no more; while it still sits
			// undelivered in hlmailbox nothing may deliver it, which the holder reads here
			v.Refunded = append(v.Refunded, fmt.Sprintf("gno #%s (%s, %d) was refunded on gno.land — must never deliver: the lock no longer holds it, and a delivery would mint against nothing (the relayers refuse it; a kind 6 enrolling a successor router retires the old route's sends for good)", n, s.Symbol, s.Amount))
		default:
			inflightOut[s.Symbol] += s.Amount
		}
	}
	for _, r := range cfg.Routes {
		rj := routes[r.Symbol]
		if rj == nil || r.Token == "" || migErrs[r.Symbol] != nil || moved != "" {
			continue // no identity for a token whose line was not read (G44): the line's sum is unknown; none over a moving cut (E3-GT2)
		}
		if why := unread[r.Symbol]; why != "" {
			v.Err = append(v.Err, "the identity of "+r.Symbol+": no verdict: the processed marks of its sends in flight were not all read this reading (E4-SF2): "+why)
			continue
		}
		if why := unconfirmed[r.Symbol]; why != "" {
			v.Err = append(v.Err, "the identity of "+r.Symbol+": no verdict: a burn toward gno.land is not on both providers' word: "+why+" (E5-SF4)")
			continue
		}
		if why := gapped[r.Symbol]; why != "" {
			v.Err = append(v.Err, "the identity of "+r.Symbol+": no verdict: its dispatch index rests on one provider's listing: "+why+" (E6-SF1)")
			continue
		}
		v.Identities[r.Symbol] = identityOf(r, rj, migs[r.Symbol], H, snap.Supplies[r.Symbol], snap.SlotOf(r.Symbol), inflightOut[r.Symbol], inflightIn[r.Symbol])
	}
	// each token program's enrolled router for gno.land's domain, named among the line's realms (F27, AS2: a
	// refund of a successor's imported sends waits on it, whatever the realm's note says)
	for _, r := range cfg.Routes {
		if r.Token == "" {
			continue
		}
		realms := []string{r.Gno}
		if m := migs[r.Symbol]; m != nil {
			realms = m.Paths()
		}
		confirmed("the "+r.Symbol+" token program's router", func(c solReader) error {
			data, err := accountDataAt(ctx, c, sol.Synthetic{Program: r.TokenKey()}.Token(), rpc.CommitmentFinalized)
			if err != nil {
				return err
			}
			rv, err := routerView(data, mb.Domain, realms)
			if err != nil {
				return err
			}
			v.Routers[r.Symbol] = rv
			return nil
		})
	}
	// a waiting claim with no dispatch record on either provider is forged (AP1): read only when both scans are
	// complete (G21: an id absent from a failed or partial scan is not forged) and the cut held — so every credit the
	// index has not placed is older than the snapshot by the margin, the index past its time (E4-GT1: a credit made
	// after the snapshot's slot may be a burn the trailing providers have not shown yet) — the credits no index holds
	// found by id (E4-RL1: hlmailbox's delivered ids, each judged by its claim read by id, never a walk of the routes'
	// claims pages), each id printed on its second consecutive poll (G33)
	oneHonours := minCtxWhy == "" && !st.minCtx.both() // the identities judged, the forged-credit lines wait (E6-SF2)
	if oneHonours {
		v.Unconfirmed = append(v.Unconfirmed, "the forged-credit lines: not read while a provider has not shown it honours minContextSlot on the listing — a burn the other hid could be in no index yet (E6-SF2)")
	}
	if indexErr == nil && len(st.Skipped) == 0 && len(gaps.why) == 0 && moved == "" && !oneHonours {
		if missing := unindexedMissing(mb.Delivered, st.Index, st.Unindexed); missing > 0 {
			if entries, err := doneIDs(ctx, own, cfg.Gno.Mailbox, mb.Delivered); err != nil {
				v.Err = append(v.Err, fmt.Sprintf("hlmailbox's delivered ids could not be read (%d credit(s) no index holds are not judged as forged until they read): %v", missing, err))
			} else {
				addUnindexed(entries, st.Index, st.Unindexed)
			}
		}
		var candidates []string
		texts := map[string]string{}
		for _, id := range sortedKeys(st.Unindexed) {
			u := st.Unindexed[id]
			if _, ok := st.Index[id]; ok {
				delete(st.Unindexed, id)
				continue
			}
			switch u.Class {
			case unindexedVote, unindexedNone, unindexedSweep, unindexedPaid:
				continue
			case unindexedBase:
				v.BeforeBase++
				continue
			}
			// a checkpointed index (G49, AS3, K40): a credit at or below the cut's height is before the checkpoint, not
			// judged; one after it on an unseeded index is alarmed, never forged
			base := judgeUnindexed(u.Height, st.IndexFromHeight, st.IndexSeeded)
			if base == unindexedBeforeBase {
				u.Class = unindexedBase
				v.BeforeBase++
				continue
			}
			c, r, err := creditClaimOf(ctx, own, cfg, migs, migErrs, id, H)
			if err != nil {
				v.Err = append(v.Err, "the claim of the unindexed credit "+id+": "+err.Error())
				continue
			}
			if class := unindexedClass(c); class != "" {
				u.Class = class // settled, never read again (E5-SF1: a sweep entry or a paid claim was read every reading)
				continue
			}
			if c.State != "open" {
				continue // voided: read again, a restore reopens it
			}
			if base == unindexedUnseeded {
				v.UnindexedBeforeBase = append(v.UnindexedBeforeBase, beforeBaseText(id, r.Symbol, c.To, c.Amount, u.Height, st.IndexFromHeight))
				continue
			}
			candidates = append(candidates, id)
			texts[id] = fmt.Sprintf("forged credit %s on %s: the claim names %s for %d (credited at height %d, paid %d so far) and neither provider's dispatch index holds a burn under its id — flag payouts naming it (hlwatch flag -route %s -fn payouts -ids %s)", id, r.Symbol, c.To, c.Amount, u.Height, c.Paid, r.Symbol, id)
		}
		for _, id := range forgedNow(st.ForgedSeen, candidates, now.Unix()) {
			v.Forged = append(v.Forged, texts[id])
		}
	}
	// voids as forged of a claim whose dispatch record matches it
	for id, rec := range st.Index {
		if st.Voids[id] || rec.Disputed != "" {
			continue
		}
		r := cfg.RouteBySymbol(rec.Symbol)
		if r == nil {
			continue
		}
		c, _, err := claimOnPair(ctx, own, r.Gno, migs[r.Symbol], id, H)
		if err != nil || c == nil || c.Swept != 0 {
			continue
		}
		if c.State == "forged" {
			st.Voids[id] = true
			if c.To == rec.To && c.Amount == rec.Amount {
				v.VoidsMatch = append(v.VoidsMatch, fmt.Sprintf("%s on %s: voided as forged, yet its dispatch record matches (%d to %s at slot %d): a mistaken or abusive void", id, rec.Symbol, rec.Amount, rec.To, rec.Slot))
			}
		}
	}
	if !cfg.HasPrograms() {
		v.Err = append(v.Err, "no light client in the config (solana.client/pausable/admin)")
		return v, nil
	}
	progs := cfg.Programs()
	// the client, from both providers
	var css [2]*clientState
	for i, c := range providers {
		cs, err := readClientState(ctx, c, progs.LightClient(), rpc.CommitmentFinalized)
		if err != nil {
			return v, fmt.Errorf("the client from provider %d: %w", i+1, err)
		}
		css[i] = cs
	}
	if css[0].Head.TrustedHash != css[1].Head.TrustedHash || css[0].Body.Records != css[1].Body.Records {
		return v, fmt.Errorf("%w on the client's state (trusted %d/%d)", errProviders, css[0].Head.TrustedHeight, css[1].Head.TrustedHeight)
	}
	cs := css[0]
	v.ClockFactor = int64(cs.Head.ClockFactor)
	cv := ClientView{TrustedHeight: cs.Head.TrustedHeight, TrustedHash: hex.EncodeToString(cs.Head.TrustedHash[:]), Records: cs.Body.Records,
		LastAccepted: time.Unix(cs.Head.LastAcceptedTime, 0), QuarantineEnd: cs.Head.QuarantineEnd, SetFrom: cs.Body.CurrentSetFrom(), SetSize: len(cs.Body.Set), NextSet: cs.Body.NextSet != nil,
		NextSetSize: len(cs.Body.NextSet), SetBound: setBoundText(len(cs.Body.Set), len(cs.Body.NextSet))}
	if b, err := own.Block(ctx, int64(cs.Head.TrustedHeight)); err == nil {
		cv.ChainHash = b.Hash
		cv.Match = strings.EqualFold(b.Hash, cv.TrustedHash)
	}
	confirmed("the client's record", func(c solReader) error { return st.Record.Sync(ctx, c, progs.LightClient(), progs.Mailbox, cs.Slot) })
	if _, err := st.Record.Check(ctx, ownHashes{own}); err != nil {
		v.Err = append(v.Err, "checking the record: "+err.Error())
	}
	cv.RecordCount, cv.RecordChecked, cv.Mismatch, cv.RecordUnchecked = st.Record.Count, st.Record.Checked, st.Record.Mismatch, st.Record.Unchecked()
	cv.RecordOK, cv.RecordWhy = st.Record.Consistent(cs.Body.Records, cs.Body.RunningHash, cs.Slot)
	v.Client = cv
	grace := sol.UnpauseDelay(v.ClockFactor)
	// the pausable ISM: its flags, and its mint limits (AN5) with each bucket's level now, the day cap against the
	// token's lock (AP6; C341: once the lock reached the expected first-week lock the config names)
	var pausableHold *sol.Flag
	confirmed("the pausable ISM", func(c solReader) error {
		ps, g, err := readPausable(ctx, c, progs.PausableISM(), rpc.CommitmentFinalized)
		if err != nil {
			return err
		}
		v.Pausable = guardiansView(g, now.Unix(), grace)
		pausableHold = g.Hold(now.Unix(), grace)
		for i, l := range ps.Limits {
			symbol := [2]string{"GNOT", "BUBBLE"}[i]
			v.Limits[i] = LimitView{Symbol: symbol, MaxPerMessage: l.MaxPerMessage, DayCap: l.DayCap, Used: l.Level(now.Unix(), grace), UsedAt: l.UsedAt}
			r := cfg.RouteBySymbol(symbol)
			// the identity judged this reading (none for an unread line, a moving cut or unread marks): its lock
			if id, judged := v.Identities[symbol]; judged && r != nil && r.Token != "" {
				rj := routes[symbol]
				cr := capRatio(symbol, l.DayCap, id.Lock, r.ExpectedLock, st.CapReached[symbol], capQuietEnds(mb), now.Unix())
				st.CapReached[symbol] = cr.Reached
				v.Limits[i].Lock, v.Limits[i].Expected, v.Limits[i].Ratio, v.Limits[i].Note = id.Lock, r.ExpectedLock, cr.Alarm, cr.Note
				// the live realm's per-message maximum against the ISM's (AS4): the line's last while a migration
				// is open
				routeMax := rj.Maximum
				if m := migs[symbol]; m != nil && m.Succ != nil {
					routeMax = m.Succ.Maximum
				}
				v.Limits[i].RouteMaximum, v.Limits[i].Mismatch = routeMax, limitsMismatch(symbol, routeMax, l)
			}
		}
		return nil
	})
	// the admin program — the live one of the line from the configured id (AQ6) — its record, its history and
	// its pending decisions
	var chain *adminChain
	if !confirmed("the admin program", func(c solReader) error {
		var err error
		chain, err = followSuccessors(ctx, c, progs.AdminProgram(), rpc.CommitmentFinalized)
		return err
	}) {
		return v, nil
	}
	ad := chain.State
	admin := chain.Live
	liveProgs := chain.Programs(progs)
	v.AdminProgram, v.LiveHex, v.Retired = admin.Program.String(), chain.LiveHex(), chain.RetiredHex()
	v.Admin = guardiansView(&ad.Guardians, now.Unix(), grace)
	v.SetupDone, v.LastSeq, v.Origin = ad.SetupDone == 1, ad.LastSeq, ad.OriginDomain
	v.AdminClockFactor = int64(ad.ClockFactor)
	v.Cancelled = ad.Cancelled
	adminHold := ad.Guardians.Hold(now.Unix(), grace)
	_ = pausableHold
	for _, h := range ad.Holds {
		v.Holds = append(v.Holds, HoldView{Start: h.Start, End: h.End})
	}
	v.PendingN = ad.Pending
	if !ad.Predecessor.IsZero() {
		v.Predecessor, v.Inherited = ad.Predecessor.String(), ad.Inherited == 1
	}
	if ad.Successor != nil {
		v.Successor = hex.EncodeToString(ad.Successor[:])
	}
	confirmed("the pending decisions", func(c solReader) error {
		var err error
		v.Pending, err = pendingDecisions(ctx, c, admin, ad, v.ClockFactor, adminHold)
		return err
	})
	if cfg.Gno.Gov != "" {
		if gov, err := ReadGov(ctx, own, cfg.Gno.Gov, H); err == nil {
			v.GovLast, v.GovNext = gov.Last, gov.Next
			// r/hlgov dispatching to another admin program than the live one — a successor the line does not
			// reach yet, or a stranger: has it inherited? (AP3)
			if rk, err := hl.ParseKey32(gov.Recipient); err == nil && rk != [32]byte(admin.Program) && !chain.IsRetired(solana.PublicKeyFromBytes(rk[:])) {
				confirmed("the recipient's admin program", func(c solReader) error {
					st, err := readAdmin(ctx, c, sol.Admin{Program: solana.PublicKeyFromBytes(rk[:])}, rpc.CommitmentFinalized)
					if err != nil {
						return err
					}
					inherited := st.Inherited == 1
					v.RecipientInherited = &inherited
					return nil
				})
			}
			confirmed("the decisions against r/hlgov", func(c solReader) error {
				findings, checked, err := foreignFindings(ctx, c, own, cfg, admin, ad, H, st.DecisionsChecked, ad.LastSeq, gov.Last, now.Unix(), foreignCtxOf(chain))
				if err != nil {
					return err
				}
				st.DecisionsChecked = checked
				st.Foreign = append(st.Foreign, findings...)
				return nil
			})
		} else {
			v.Err = append(v.Err, "r/hlgov: "+err.Error())
		}
	}
	v.Foreign = st.Foreign
	_ = liveProgs
	return v, nil
}

type ownHashes struct{ own gnoReader }

func (o ownHashes) BlockHash(ctx context.Context, height int64) (string, int64, error) {
	head, err := o.own.Status(ctx)
	if err != nil {
		return "", 0, err
	}
	if height > head.Height {
		return "", head.Height, nil
	}
	b, err := o.own.Block(ctx, height)
	if err != nil {
		return "", head.Height, err
	}
	return b.Hash, head.Height, nil
}

// pendingDecisions reads every pending decision's account (the last 500 numbers), with what defers each beyond
// its clock.
func pendingDecisions(ctx context.Context, c solReader, admin sol.Admin, ad *sol.AdminState, factor int64, adminHold *sol.Flag) ([]PendingSolDecision, error) {
	ds, err := readPendingDecisions(ctx, c, admin, ad, rpc.CommitmentFinalized)
	var out []PendingSolDecision
	for _, d := range ds {
		p := PendingSolDecision{Number: d.Number, Kind: d.Kind, Params: hex.EncodeToString(d.Params), Text: sol.DescribeParams(d.Kind, d.Params), Delivered: d.DeliveredTime, MaturesAt: d.MaturesAt(factor, adminHold, ad), Deliverer: d.Deliverer.String()}
		if adminHold != nil && d.HeldToTheDay() {
			p.Floor = d.FloorUnder(adminHold, ad, factor)
		}
		switch {
		case d.Kind == sol.KindReserved:
			p.Deferred = "reserved, will be spent"
		case adminHold != nil && d.HeldToTheDay():
			p.Deferred = fmt.Sprintf("held under the hold since %s: %s", time.Unix(adminHold.Time, 0).UTC().Format(time.RFC3339), floorText(d, ad, adminHold, factor))
		case d.HeldToTheDay():
			p.Deferred = "no hold stands now, so it lands on its own 7th day (a hold's floor, day 21 of the earliest hold overlapping its pending life, binds only while one stands)"
		case adminHold != nil && d.HeldUntilTheEnd():
			p.Deferred = "deferred by a hold on the admin program since " + time.Unix(adminHold.Time, 0).UTC().Format(time.RFC3339) + " until it ends"
		case d.HeldUntilTheEnd() && ad.Guardians.AnyFlag():
			p.Deferred = "deferred by a flag on the admin program"
		}
		if ad.IsCancelled(d.Number) {
			p.Deferred = "cancelled (closes at its next Execute)"
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, err
}

// floorText says where a replacement's floor stands while a hold does (AN4, plan v29 §6.5): day 21 of the
// earliest hold overlapping its pending life — the standing one, or an ended one of the state's history whose
// end is at or after the delivery — computed from the history, nothing written; it binds only while a hold
// stands and no later hold raises it.
func floorText(d *sol.Decision, st *sol.AdminState, hold *sol.Flag, factor int64) string {
	floor := d.FloorUnder(hold, st, factor)
	from := "the standing hold"
	if start := floor - sol.HoldReplacementDays*sol.Day/factor; start != hold.Time {
		from = "an earlier hold of the history (since " + time.Unix(start, 0).UTC().Format(time.RFC3339) + ") that overlapped its pending life"
	}
	return fmt.Sprintf("floor %s: day %d of %s, from the hold history", time.Unix(floor, 0).UTC().Format(time.RFC3339), sol.HoldReplacementDays, from)
}

// foreignFindings compares the numbers (from, last] the admin program recorded with r/hlgov's
// dispatched bodies at a gno height: a number r/hlgov never dispatched, or a pending body that is not r/hlgov's,
// is a foreign decision (§5.9). A number absent at H is read again at the own node's head before it is called
// foreign (G15: a decision dispatched after H and delivered before this read is a lag, not a forgery; it is
// checked at a later H); a re-read that errors is no reading at all — the poll is skipped at that number, which
// stays unchecked (G23: a closed connection or a timeout on that one call must not make a foreign decision of
// GovDAO's own). It returns the findings and the last number checked. A 7-day decision gone inside its week that
// no later cancel of r/hlgov's names is a foreign finding (plan v35 §5.9: cancelled or spent are the only ways
// out, and no validators' cancel exists). A number at or below a retired program's last is judged on that program's accounts (G38: a fresh
// install after a kind 9 walked the live program's numbers, and every decision pending on the predecessor at the
// transfer was "gone").
func foreignFindings(ctx context.Context, c solReader, own gnoReader, cfg *Config, admin sol.Admin, st *sol.AdminState, H int64, from, last, govLast uint64, now int64, fc foreignCtx) ([]string, uint64, error) {
	var out []string
	checked := from
	factor := int64(1)
	if st != nil && st.ClockFactor > 0 {
		factor = int64(st.ClockFactor)
	}
	// gone are the 7-day decisions of r/hlgov's whose account the admin program no longer holds inside their
	// week (G32), judged after the walk against the cancels r/hlgov dispatched at later numbers
	type goneDecision struct {
		n    uint64
		kind byte
		at   int64
	}
	var gone []goneDecision
	var cancels []sol.CancelRange
	for n := from + 1; n <= last; n++ {
		body, at, err := govDecision(ctx, own, cfg.Gno.Gov, n, H)
		if err != nil {
			return out, checked, err
		}
		if body == nil {
			again, err := govBody(ctx, own, cfg.Gno.Gov, n, 0)
			if err != nil {
				return out, checked, fmt.Errorf("decision %d at the head: %w", n, err)
			}
			if again != nil {
				break // dispatched after H: judged at a height that holds it
			}
			if n > govLast {
				out = append(out, fmt.Sprintf("foreign decision: the admin program recorded number %d, r/hlgov's counter is %d", n, govLast))
			} else {
				out = append(out, fmt.Sprintf("foreign decision: the admin program recorded number %d, which r/hlgov never dispatched", n))
			}
			checked = n
			continue
		}
		// the program the number was recorded on: a retired one of the line for a number at or below its last
		prog, pst := admin, st
		for _, r := range fc.Retired {
			if r.State != nil && n <= r.State.LastSeq {
				prog, pst = r.Admin, r.State
				break
			}
		}
		d, err := readDecision(ctx, c, prog, n, rpc.CommitmentFinalized)
		if err != nil {
			return out, checked, err
		}
		switch {
		case d != nil && (d.Kind != body[0] || hex.EncodeToString(d.Params) != hex.EncodeToString(body[9:])):
			out = append(out, fmt.Sprintf("foreign decision: number %d on the admin program (kind %d) is not what r/hlgov dispatched (kind %d)", n, d.Kind, body[0]))
		case d == nil && body[0] == sol.KindCancel:
			if p, err := sol.ParseParams(sol.KindCancel, body[9:]); err == nil {
				cancels = append(cancels, p.Cancel...)
			}
		case d == nil && sol.Delay(body[0], factor) > 0 && at > 0 && now < at+sol.Delay(body[0], factor) && (pst == nil || !pst.IsCancelled(n)):
			// a 7-day decision with no account inside its week: executed it cannot be; cancelled by a range the
			// state still holds it is not — a foreign cancel racing GovDAO's number closed it (G32), unless a later
			// cancel of r/hlgov's own names it
			gone = append(gone, goneDecision{n, body[0], at})
		}
		checked = n
	}
	for _, g := range gone {
		cancelled := false
		for _, rg := range cancels {
			if rg.From <= g.n && g.n <= rg.To {
				cancelled = true
			}
		}
		if cancelled {
			continue
		}
		// no validators' cancel exists: a 7-day decision gone inside its week that no later cancel of r/hlgov's
		// names was closed by a foreign one, whatever stands
		out = append(out, fmt.Sprintf("foreign decision: number %d's decision (kind %d) is gone though r/hlgov's matures in 7 days: a foreign decision took it (a cancel landed under its number before GovDAO's copy; the replacement is lost and must be proposed again)", g.n, g.kind))
	}
	return out, checked, nil
}

// foreignCtx is what the gone-decision rule reads beside the numbers (G38): the retired programs of the line,
// whose numbers are judged on their own accounts (no validators' cancel can close a decision).
type foreignCtx struct {
	Retired []retiredAdmin
}

// foreignCtxOf is the decision rule's context from the admin program's line.
func foreignCtxOf(chain *adminChain) foreignCtx {
	fc := foreignCtx{}
	if chain != nil {
		fc.Retired = chain.Retired
	}
	return fc
}

// RecordCheckpoint takes the client's own record as an empty watch record's base (G29, hlwatch's -record-from
// client): the counter, the running hash and the ring (in the program's order, F21) at a read through both
// providers, the trusted header checked against the holder's node first (G37: a differing one is named as the
// cut and no base is taken — the watch flags header:mismatch on every view after), the open stages from the
// client's accounts (G37), the index's cursors at the slot (G36); everything at or below that slot passed over.
// It answers what it did ("" when the record holds entries already).
func RecordCheckpoint(ctx context.Context, providers [2]*rpc.Client, own GnoReader, cfg *Config, st *WatchState, from string) (string, error) {
	if from != "client" {
		return "", fmt.Errorf("-record-from %q: \"client\" (the client's own counter and running hash as the base) is the one checkpoint", from)
	}
	if !cfg.HasPrograms() {
		return "", errors.New("-record-from: the config names no light client")
	}
	st.init()
	if st.Record.Count != 0 || st.Record.Cursor != "" {
		return "", nil
	}
	both := twoProviders(providers)
	cs, err := readClientState(ctx, both, cfg.Programs().LightClient(), rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("the checkpoint: %w", err)
	}
	mm, err := checkpointBase(ctx, own, cs, &st.Record)
	if err != nil {
		return "", fmt.Errorf("the checkpoint: %w", err)
	}
	if mm != nil {
		return checkpointMismatchText(mm), nil
	}
	stages, err := seedStages(ctx, both, cfg.Programs().LightClient(), &st.Record)
	if err != nil {
		return "", fmt.Errorf("the checkpoint's stages: %w", err)
	}
	cursors, err := seedCursors(ctx, [2]solReader{providers[0], providers[1]}, cfg, cs.Slot, st.Cursors, st.Through)
	if err != nil {
		return "", fmt.Errorf("the checkpoint's cursors: %w", err)
	}
	// the cut's height and the index seeded from the mailbox's accounts where the providers serve the call (G49,
	// AS3): a refusal leaves the fallback rule on the height, the seed retried hourly by the views
	if head, err := own.Status(ctx); err == nil {
		st.IndexFromHeight = head.Height
	}
	st.IndexSeedTried = time.Now().Unix()
	seeded, seedErr := 0, error(nil)
	if mb, err := mailboxAt(ctx, own, cfg.Gno.Mailbox, 0); err != nil {
		seedErr = fmt.Errorf("hlmailbox's domain: %w", err)
	} else {
		seeded, seedErr = seedDispatches(ctx, both, cfg, sol.Mailbox{Program: cfg.SolMailbox()}, mb.Domain, st.Index, time.Now(), &Alarmer{Source: "hlwatch"})
	}
	st.IndexSeeded = seedErr == nil
	return checkpointText(cs, stages, cursors, st.IndexFromHeight, seeded, seedErr), nil
}

// SeatOnSolana is the slot a key holds on the pausable ISM (1–6), 0 if none.
func SeatOnSolana(ctx context.Context, c solReader, progs sol.Programs, key solana.PublicKey) (int, error) {
	_, g, err := readPausable(ctx, c, progs.PausableISM(), rpc.CommitmentConfirmed)
	if err != nil {
		return 0, err
	}
	return g.Position(key) + 1, nil
}

// FmtFlag writes a flag's moment.
func FmtFlag(f *sol.Flag) string {
	if f == nil {
		return "none"
	}
	return fmt.Sprintf("slot %d at %s", f.Slot, time.Unix(f.Time, 0).UTC().Format(time.RFC3339))
}

// U64 is a number for the text views.
func U64(n uint64) string { return strconv.FormatUint(n, 10) }

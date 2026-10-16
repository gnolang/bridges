package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// SignedBurn is one signer's checkpoint signature for one burn, as it serves it at GET /v1/eth/<nonce>.
type SignedBurn struct {
	Origin    uint32 `json:"origin"`    // Ethereum's domain
	Nonce     uint32 `json:"nonce"`     // the message's nonce in our mailbox
	ID        string `json:"id"`        // the message id, hex
	Message   string `json:"message"`   // the message, hex
	Hook      string `json:"hook"`      // our MerkleTreeHook as 32 bytes, hex (this bridge's checkpoint convention)
	Root      string `json:"root"`      // zero, hex
	Block     uint64 `json:"block"`     // the burn's Ethereum block (at or below the verdict's finalized block)
	Tx        string `json:"tx"`        // the burn's transaction
	Signature string `json:"signature"` // ed25519 over the checkpoint digest
	Signer    string `json:"signer"`    // the ed25519 key, hex
}

// signerNoVerdictHalt is how long a signer keeps signing on an old verdict before it stops: an hour without a fresh
// verdict (a provider misreporting a supply, an outage of either, a finality stall) halts it until one returns.
const signerNoVerdictHalt = time.Hour

// finalityStall is how long Ethereum's finalized block may stand still before it alarms (§5.6: liveness, never
// safety — the signers sign nothing newer meanwhile, since they sign only at or below `finalized`).
const finalityStall = time.Hour

// Signer is one of the three signer machines of eth/bridge/PLAN.md §5.6, the Solana plan §5.6 carried over: it reads
// gno.land through its own node and Ethereum through two providers that must agree, signs ed25519 over Hyperlane's
// checkpoint digest once per burn toward gno.land — this bridge's convention: origin Ethereum's domain, our
// MerkleTreeHook as the hook, root 0, index the message's nonce — refusing two messages for one nonce (a nonce
// conflict stops it and alerts; it does not exit), serves its signatures, and stops signing the moment the supply
// identity it computes shows Ethereum's supply — the burns at or below the finalized block and not yet credited
// counted as supply, and the sends locked on gno.land and not yet delivered there taken from the lock's room (E2-RT9)
// — above the lock, saying why on its status endpoint, until a void as forged closes it again; it
// stops too when the client's trusted height runs more than an hour of blocks past both its own node's head and a
// second source's (the client is ahead of its chain), until the identity holds at a height at or above it. It signs
// a burn only once its block is at or below the finalized block of a verdict that counted it in flight, on no
// verdict older than one round, and halts after an hour without one. It signs nothing on Ethereum.
type Signer struct {
	Cfg   *Config
	Key   *hl.ValidatorKey
	Store string // directory: eth/<nonce>.json, state.json
	Every time.Duration
	Alarm *Alarmer
	// FromBlock is where a fresh burn index starts when given (0: the config's eth.deployBlock — finishSetup's
	// block — else that block found on the chain).
	FromBlock uint64

	pair    ethx.Pair
	gno     gnoside.Reader
	sources []gnoside.Reader // the other gno sources (gno.sources): the second source of §5.6's stop
	now     func() time.Time

	signed map[string]bool // ids in the store

	mu          sync.Mutex
	state       signerState
	halted      string // why signing stopped ("" while signing)
	nonceHalt   bool   // a nonce conflict: sticky until a human looks
	aheadHalt   bool   // §5.6's ahead-stop: cleared only by a verdict at a height at or above T
	verified    bool   // the identity held on the last check
	lastVerdict time.Time
	verdictAt   uint64 // the finalized block of the last verdict's snapshot
	identity    map[string]*Identity
	cutNote     string
	movingCuts  int // rounds in a row whose gno reads moved under them — a dispatch, a credit (no verdict, retried quietly)
	stepBacks   int // rounds in a row whose pair answered a finalized block below the floor (no verdict, retried quietly)
	apartRounds int // rounds in a row whose two finalized answers lay more than two epochs apart (no verdict, retried quietly)
	waitRounds  int // rounds in a row whose BROKEN reading waited for the index to place a credit (E4-GT1; retried quietly)
	lastCheck   time.Time
	finalSeen   uint64 // the highest finalized block seen (state.FinalSeen, for the status)
	indexedN    int
	waitingN    int
	signedN     int
	cursorN     uint64 // the index's cursor, its first block and its ignored count, as last published by the loop
	fromN       uint64
	ignoredN    int
	lineN       uint32 // the launch line the index last read (HlAdmin.launchNonce())
	// marksAt is when every uncredited delivered mark was last read (markRefresh), marksDoubt whether the last cut was in
	// doubt (markDoubt): either reads them all at the next round, which otherwise reads a mark again only once
	// hlmailbox's delivered count moved since it was read unset (the integration lane I3, the Solana machines' E5-SF5)
	marksAt    time.Time
	marksDoubt bool
	marksRead  int // the marks the last round read (the status)
}

type signerState struct {
	From  uint64    `json:"from"`  // the index's first block
	Index burnIndex `json:"index"` // the burns not yet credited, and the scan's place
	// Sends are the sends toward Ethereum the identity counts in flight (sends.go; the plan red team's E2-RT9): locked
	// on gno.land at the cut's height and not delivered at its finalized block
	Sends sendIndex `json:"sends"`
	// Floor is the highest finalized block a round took its Ethereum readings at (the Go trust audit's E3-GT9): what
	// it read there stays — a send's delivered mark, a credited burn signed and pruned, the index's cursor — so a
	// later round at a lower block would hold a supply short of a mint those marks count (a forged burn signed), or
	// over a burn already credited (a false halt). A finalized block never un-finalizes: a pair answering one below
	// the floor is a provider stepped back (a lagging backend, or a liar), and that round has no verdict.
	Floor uint64 `json:"floor,omitempty"`
	// AheadStop is §5.6's ahead-stop while it stands (E3-GT6): kept here so a restart never lifts it; cleared only as
	// a verdict at a height at or above the trusted height clears it (conclude).
	AheadStop *aheadStanding `json:"aheadStop,omitempty"`
	// Pending are the BROKEN readings whose cut's order is not shown, by symbol (cutorder.go, E4-GT1): a round with one
	// has no verdict, and each halts once the index's finalized block's time passes the reading's with its excess
	// standing; PrunedCredited counts the credited burns prune dropped from the index (each a delivery it placed).
	Pending        map[string]*pendingBreak `json:"pendingBreaks,omitempty"`
	PrunedCredited int                      `json:"prunedCredited,omitempty"`
	// FinalSeen is the highest finalized block the pair answered and FinalSince when this signer first saw it (unix):
	// kept here, so a restart never restarts a finality stall's clock (E5-RT1's sibling)
	FinalSeen  uint64 `json:"finalSeen,omitempty"`
	FinalSince int64  `json:"finalSince,omitempty"`
	// Gno is what the own node answered so far, held to one node's answers (backends.go, E6-GT2): a finding refuses every
	// verdict until the operator gives the signer a node of its own
	Gno gnoSeen `json:"gno"`
}

// aheadStanding is a standing ahead-stop: why, and the client's trusted height when it was judged.
type aheadStanding struct {
	T   uint64 `json:"t"`
	Why string `json:"why"`
	At  int64  `json:"at"`
}

func (s *Signer) load() error {
	if err := os.MkdirAll(filepath.Join(s.Store, "eth"), 0o700); err != nil {
		return err
	}
	if err := readJSON(filepath.Join(s.Store, "state.json"), &s.state); err != nil {
		return err
	}
	if err := s.state.Index.attachDust(filepath.Join(s.Store, "dust.jsonl")); err != nil { // the folded dust (E6-GT3)
		return err
	}
	s.state.Sends.init()
	if s.state.Pending == nil {
		s.state.Pending = map[string]*pendingBreak{}
	}
	s.signed = map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(s.Store, "eth", "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var sg SignedBurn
		if err := json.Unmarshal(b, &sg); err != nil || sg.ID == "" {
			return fmt.Errorf("signature file %s is %w: %v; restore it or delete it knowingly", f, errCorrupt, err)
		}
		s.signed[sg.ID] = true
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.identity == nil {
		s.identity = map[string]*Identity{}
	}
	if st := s.state.AheadStop; st != nil { // a restart never lifts §5.6's ahead-stop (E3-GT6)
		s.mu.Lock()
		s.aheadHalt = true
		s.mu.Unlock()
		s.halt("the client runs ahead of its chain (§5.6): " + st.Why + " (standing since " + time.Unix(st.At, 0).UTC().Format(time.RFC3339) + ", kept across a restart: lifted only by the identity holding at a height at or above the client's trusted height)")
	}
	return nil
}

func (s *Signer) save() error {
	return saveWithDust(&s.state.Index, func() error { return writeJSON(filepath.Join(s.Store, "state.json"), s.state) })
}

// Run signs until ctx ends.
func (s *Signer) Run(ctx context.Context) error {
	if err := s.load(); err != nil {
		return err
	}
	if err := s.Cfg.CheckTrustPath("signer"); err != nil { // the burns' mailbox, the client's trusted height, the launch line, the checkpoint's hook (E3-GT8)
		return err
	}
	if s.pair.A == nil {
		p, err := pairOf(ctx, s.Cfg)
		if err != nil {
			return err
		}
		s.pair = p
	}
	if s.gno == nil {
		s.gno = s.Cfg.SourceClient(s.Cfg.Gno.RPC)
	}
	s.state.Gno.begin(s.Cfg.Gno.RPC, s.Cfg.Gno.Mailbox)
	if s.sources == nil {
		for _, src := range s.Cfg.Gno.Sources {
			s.sources = append(s.sources, s.Cfg.SourceClient(src))
		}
	}
	if len(s.sources) == 0 {
		log.Printf("signer: WARNING no second gno source (gno.sources): §5.6's stop when the client runs ahead cannot be judged; it alarms instead")
	}
	if err := s.start(ctx); err != nil {
		return err
	}
	log.Printf("signer %s: own node %s, providers %s and %s, store %s, burns from block %d, the client may run %d blocks ahead",
		hex.EncodeToString(s.Key.EdPublic()), ethx.Redact(s.Cfg.Gno.RPC), s.pair.A.Provider, s.pair.B.Provider, s.Store, s.state.From, s.Cfg.AheadAllowance())
	for {
		s.round(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Every):
		}
	}
}

// start fixes where a fresh index begins: FromBlock, else the config's eth.deployBlock, else finishSetup's block found
// on the chain (E2-RT6: no burn before it is this deployment's).
func (s *Signer) start(ctx context.Context) error {
	if s.state.From != 0 {
		return nil
	}
	if from := s.Cfg.ScanFrom(s.FromBlock); from > 0 {
		s.state.From = from
		return s.save()
	}
	head, err := s.pair.A.BlockAt(ctx, ethx.Latest)
	if err != nil {
		return fmt.Errorf("signer: the head, to find the deployment's first block: %w", err)
	}
	from, err := deployBlock(ctx, s.pair.A, s.Cfg, head.Number)
	if err != nil {
		return fmt.Errorf("signer: %w (or set eth.deployBlock in the config)", err)
	}
	s.state.From = from
	return s.save()
}

// round is one pass: a verdict, then the signatures it allows.
func (s *Signer) round(ctx context.Context) {
	if err := s.checkIdentity(ctx); err != nil {
		log.Printf("signer: identity: %v", err)
	}
	if s.signing() {
		if err := s.signBurns(); err != nil {
			log.Printf("signer: signing: %v", err)
		}
	}
	s.prune()
	if err := s.save(); err != nil {
		log.Printf("signer: state: %v", err)
	}
	s.publishCounts()
}

// verdictMaxAge is how old the last verdict may be for this round to sign on it: the previous round's (a signer
// signs on no verdict older than one round; the round's own jitter allowed for).
func (s *Signer) verdictMaxAge() time.Duration {
	if s.Every <= 0 {
		return 2 * 3 * time.Second
	}
	return 2 * s.Every
}

// signing is whether this round may sign: no halt stands, the identity was verified, and that verdict is no older
// than one round.
func (s *Signer) signing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.halted == "" && s.verified && s.now().Sub(s.lastVerdict) <= s.verdictMaxAge()
}

func (s *Signer) halt(why string) {
	s.mu.Lock()
	was := s.halted
	s.halted = why
	s.mu.Unlock()
	if was != why {
		s.Alarm.Flag("signer:halt", "signing stopped: "+why)
	}
}

func (s *Signer) resume() {
	s.mu.Lock()
	was := s.halted
	if !s.nonceHalt {
		s.halted = ""
		s.aheadHalt = false
		s.state.AheadStop = nil
	}
	nonce := s.nonceHalt
	s.mu.Unlock()
	if was != "" && !nonce {
		log.Printf("signer: signing resumes (the identity holds again)")
		s.Alarm.Clear("signer:halt")
	}
}

// unverified is a round with no verdict: it alarms and changes nothing about whether the signer signs — this round
// and the next sign on the last verdict, a halted one stays halted, one that never had a verdict keeps waiting for
// its first — until an hour has passed without one, which halts the signer until a verdict returns.
func (s *Signer) unverified(key, why string) {
	s.noVerdict(key, why, true)
}

// movingCutAlarm is how many rounds in a row a moving cut (a send landing during the gno reads) may leave without a
// verdict before it alarms: one is the ordinary race of a busy chain, retried next round in silence.
const movingCutAlarm = 5

// movingCut is a round whose gno reads moved under it (a dispatch, a credit, a refund, a migration's page landing
// between them): no verdict, the cut taken again next round in silence, alarmed at the movingCutAlarm-th in a row.
func (s *Signer) movingCut(why string) {
	s.movingCuts++
	s.noVerdict("", fmt.Sprintf("%s: the node answers its latest state, so the cut is taken again next round (%d round(s) in a row)", why, s.movingCuts), s.movingCuts >= movingCutAlarm)
}

// belowFloor says why a finalized block below the floor is no reading (E3-GT9), "" when it is at or above it.
func belowFloor(F, floor uint64) string {
	if F >= floor {
		return ""
	}
	return fmt.Sprintf("the providers' finalized block %d is below %d, the highest a round already read at: a finalized block never un-finalizes, so a provider stepped back (a lagging backend, or a liar) — what was read at %d stays (the sends' delivered marks, the credited burns, the record), and a reading at the lower block is short or over by what moved between them: no verdict until the pair answers at or above it", F, floor, floor)
}

// noVerdict records a round without a verdict; alarm false keeps it quiet (the hour of no verdict halts all the same).
func (s *Signer) noVerdict(key, why string, alarm bool) {
	s.mu.Lock()
	s.cutNote = "no verdict: " + why
	s.lastCheck = s.now()
	stale := s.verified && !s.lastVerdict.IsZero() && s.now().Sub(s.lastVerdict) > signerNoVerdictHalt && s.halted == ""
	last := s.lastVerdict
	s.mu.Unlock()
	log.Printf("signer: no verdict this round: %s", why)
	if key == "" {
		key = "signer:no-verdict"
	}
	if alarm {
		s.Alarm.Alarm(key, why)
	}
	if stale {
		s.halt("no verdict for an hour (the last was at " + last.UTC().Format(time.RFC3339) + "): " + why)
	}
}

// checkIdentity evaluates §5.6's refusal rule over §5.9's ordered cut: the Ethereum snapshot first (the agreed
// finalized block F, each token's supply at F, the client's trusted height T at F), then gno.land through the own
// node at a height at or above T whose block time is after the snapshot — or, T above the head inside the allowance,
// at the head itself — then the burn index scanned to the pair's finalized block read after the gno read (a burn
// credited at that height was finalized before it, so the scan sees it: one above F breaks the order, no verdict),
// the burns at or below F not yet credited counted as supply. T more than an hour of blocks past both the own node's
// head and a second source's stops it (judgeAhead); a reading the node cannot give yet alarms and never stops a
// signer that was signing — but a round with no verdict leaves nothing to sign on past the next round, and an hour
// of them halts; otherwise only a broken identity stops it, and every stop but a nonce conflict clears itself once
// the identity holds at a height at or above T. A signer never signs before its first verdict.
func (s *Signer) checkIdentity(ctx context.Context) error {
	snap, err := takeEthSnapshot(ctx, s.pair, s.Cfg, s.now())
	if err != nil {
		if s.finalizedApart(err, "the Ethereum snapshot: ") {
			return err
		}
		key := "signer:no-verdict"
		if ethx.IsNoState(err) {
			key = "eth:no-state"
		} else if errors.Is(err, ethx.ErrDisagree) {
			key = "eth:providers"
		}
		s.unverified(key, "the Ethereum snapshot: "+ethReadText(err))
		return err
	}
	s.Alarm.Clear("eth:no-state")
	s.Alarm.Clear("eth:providers")
	s.noteFinality(snap.Block.Number)
	// the floor (E3-GT9): a finalized block below the highest one a round already read at is a provider stepped back —
	// no verdict, retried quietly, alarmed at the fifth such round in a row; never a reading
	if why := belowFloor(snap.Block.Number, s.state.Floor); why != "" {
		s.stepBacks++
		s.noVerdict("eth:finalized-floor", why, s.stepBacks >= movingCutAlarm)
		return nil
	}
	s.stepBacks = 0
	s.Alarm.Clear("eth:finalized-floor")
	s.state.Floor = snap.Block.Number
	var T uint64
	if snap.Client != nil && snap.Client.TrustedHeight > 0 {
		T = uint64(snap.Client.TrustedHeight)
	}
	head, err := gnoHeadAfter(ctx, s.gno, snap.At)
	if err != nil {
		s.unverified("", "own node: "+err.Error())
		return err
	}
	if head.CatchingUp {
		s.unverified("", fmt.Sprintf("own node is catching up (head %d)", head.Height))
		return nil
	}
	s.state.Gno.head(head.Height, "the own node's head after the Ethereum snapshot", s.now())
	if uint64(head.Height) < T {
		allowance := s.Cfg.AheadAllowance()
		if T-uint64(head.Height) > allowance { // past the allowance: the second source decides
			second, secondOK := gnoSecondHead(ctx, s.sources)
			v, why := judgeAhead(T, uint64(head.Height), second, secondOK, allowance)
			s.applyAhead(T, v, why)
			return nil
		}
		// inside the allowance: the identity is evaluated at the own node's head
	}
	cut := Cut{EthBlock: snap.Block.Number, EthTime: snap.At, GnoHeight: head.Height, GnoTime: head.Time}
	if ok, why := cut.Holds(); !ok {
		s.unverified("", why)
		return nil
	}
	// the cut's window (cutorder.go): hlmailbox, the routes and their lines of migrations, the own node's head, hlmailbox
	// again — taken again at once while it moves, and nothing else inside it; the index, the sends and the marks are read
	// after it, each mark placed in the window's state by its height (E6-GT1)
	w, ids, lines, err := s.takeWindow(ctx, head.Height, snap)
	if err != nil {
		s.unverified(disagreeKey(err), ethReadText(err))
		return err
	}
	mb, routes := w.first, w.routes
	// the burn index, scanned after the gno read to the pair's finalized block now
	F2, err := s.pair.FinalizedWithin(ctx, ethx.FinalizedApart)
	if err != nil {
		if !s.finalizedApart(err, "the finalized block after the gno read: ") {
			s.unverified("eth:providers", "the finalized block after the gno read: "+ethReadText(err))
		}
		return err
	}
	s.apartRounds = 0
	if F2.Number < snap.Block.Number {
		F2 = &snap.Block // a provider stepped back: the snapshot's block is final already
	}
	_, done, err := s.state.Index.catchUp(ctx, s.pair, s.Cfg.Eth.Mailbox, s.Cfg.Eth.Admin, s.Cfg.Eth.Domain, mb.Domain, burnRoutes(s.Cfg, lines), s.state.From, F2.Number, s.now())
	if err != nil {
		key := "signer:no-verdict"
		if errors.Is(err, ethx.ErrDisagree) {
			key = "eth:providers"
		}
		s.unverified(key, "the burn index: "+ethReadText(err))
		return err
	}
	if !done {
		s.unverified("", fmt.Sprintf("the burn index is at block %d, behind the finalized block %d: catching up (%d blocks a round)", s.state.Index.Cursor, F2.Number, burnScanMax))
		return nil
	}
	// the sends locked on gno.land at the cut's height and not delivered at its finalized block F (E2-RT9): in flight
	// out, subtracted from the lock's room, exactly as the monitors count them — a send waits hours for its header by
	// design, and left out it would be room for a forged mint the signers never see (read inside the window while a
	// token's sends can be refunded: their refund marks are held to the lock read there)
	if !w.sends {
		if err := s.syncSends(ctx, mb, routes, lines, head.Height, snap); err != nil {
			s.unverified(disagreeKey(err), ethReadText(err))
			return err
		}
	}
	if s.state.Sends.SendNext < mb.Count {
		s.unverified("", fmt.Sprintf("the sends in flight are read to gno nonce %d of %d: catching up (%d a round)", s.state.Sends.SendNext, mb.Count, sendsPerRound))
		return nil
	}
	// the delivered marks of the burns not credited yet, each read again only once hlmailbox's delivered count moved since
	// it was read unset — the burns nobody credits (the dust below a route's fee the relayers leave, E5-GS5) were read
	// every round, for good — and all of them while the last cut was in doubt and every markRefresh; d is the count read
	// last before them (the window's second read), Hc the window's head they are placed by
	d, Hc := w.move.again.Delivered, w.height
	allMarks := s.marksDoubt || s.now().Sub(s.marksAt) >= markRefresh
	_, markedTop, read, err := s.state.Index.markCredited(ctx, s.gno, s.Cfg.Gno.Mailbox, head.Height, d, allMarks)
	if err != nil {
		s.unverified("", err.Error())
		return err
	}
	if allMarks {
		// and the folded dust, whose marks a moving count does not read (E6-GT3)
		_, top, err := s.state.Index.readFolded(ctx, s.gno, s.Cfg.Gno.Mailbox, head.Height, d, nil)
		if err != nil {
			s.unverified("", err.Error())
			return err
		}
		markedTop = max(markedTop, top)
		s.marksAt = s.now()
	}
	// a round that stops before it places its deliveries leaves its marks in doubt: the next round reads them all (the
	// review of I3's follow-up 1, finding 1: a stale read in a round a failed read cut short stood into the next)
	s.marksDoubt = true
	s.mu.Lock()
	s.marksRead = read
	s.mu.Unlock()
	s.state.Sends.prune(routes, lines)
	out := s.state.Sends.inflight()
	in, after := s.state.Index.inflight(snap.Block.Number, Hc)
	for sym, id := range ids {
		id.InflightIn, id.InflightOut = in[sym], out[sym]
	}
	// the deliveries the index cannot place (E4-GT1): hlmailbox's count less the burns the index places in the window's
	// state, those prune dropped among them; while one is a BROKEN reading's possible lag, what the providers show above
	// the index — the most that lag can carry (E5-GT1) — and the credited marks of those burns are read, placed by the
	// window's head as the index's are
	unplaced := d - s.state.Index.placedBy(Hc) - s.state.PrunedCredited
	if anyStops(ids) {
		// what one stale unset mark of a credited burn reads as — its amount in flight and in owed alike — is a BROKEN
		// reading, so every uncredited mark is read again before it is judged (the review of I3's follow-up 1, finding 2:
		// one stale read halted the signer at once past E5-GT1's bound, or as a pending break confirmed later though the
		// next round held). On one node that read gives what the first gave; read again whatever is unplaced (round 6): on
		// an endpoint answering from two backends the count can be as stale as the mark (E6-GT2), and a mark read set at the
		// window's head is then held to the count by the backends rule below
		_, top, more, err := s.state.Index.markCredited(ctx, s.gno, s.Cfg.Gno.Mailbox, head.Height, d, true)
		if err != nil {
			s.unverified("", err.Error())
			return err
		}
		markedTop = max(markedTop, top)
		in, after = s.state.Index.inflight(snap.Block.Number, Hc)
		for sym, id := range ids {
			id.InflightIn = in[sym]
		}
		unplaced = d - s.state.Index.placedBy(Hc) - s.state.PrunedCredited
		s.mu.Lock()
		s.marksRead += more
		s.mu.Unlock()
		// a token with folded dust still BROKEN with a delivery unplaced: what a delivered folded burn reads as (its amount
		// in owed and in the folded sum) — its folded marks read too, before it is judged (E6-GT3)
		if syms := foldedBroken(ids, s.state.Index.foldedSum()); unplaced > 0 && len(syms) > 0 {
			_, top, err := s.state.Index.readFolded(ctx, s.gno, s.Cfg.Gno.Mailbox, head.Height, d, syms)
			if err != nil {
				s.unverified("", err.Error())
				return err
			}
			markedTop = max(markedTop, top)
			in, after = s.state.Index.inflight(snap.Block.Number, Hc)
			for sym, id := range ids {
				id.InflightIn = in[sym]
			}
			unplaced = d - s.state.Index.placedBy(Hc) - s.state.PrunedCredited
		}
	}
	s.marksDoubt = markDoubt(unplaced, anyStops(ids), len(s.state.Pending))
	var above *aboveIndex
	if unplaced > 0 && anyStops(ids) {
		above = readAbove(ctx, s.pair, s.Cfg, F2.Number, mb.Domain, s.now())
		above.markAbove(ctx, s.gno, s.Cfg.Gno.Mailbox, head.Height)
	}
	// one node (E6-GT2): every mark read at or below a head read after it, and the deliveries placed at most hlmailbox's
	// count read after the window's head — else the endpoint answers from more than one backend, and no verdict is taken
	if err := s.holdToOneNode(ctx, creditTop(markedTop, above), d, Hc); err != nil {
		s.unverified("", "own node's head after the marks: "+err.Error())
		return err
	}
	if text := s.state.Gno.text(); text != "" {
		s.Alarm.Flag(backendsKey, text)
		s.unverified("", text)
		return nil
	}
	s.Alarm.Clear(backendsKey)
	// a cut that moved is no verdict — but a reading BROKEN across it with nothing unplaced, or past the most an honest
	// lag can carry, is a break whatever landed (cutorder.go's window), and the pending readings are judged with the lag
	// the index shows now: either halts
	if w.move.why != "" {
		var breaks []string
		if !w.move.counters {
			breaks = brokenBeyondTheMove(ids, unplaced, snap.Block.Number, Hc, &s.state.Index, above)
		}
		breaks = append(breaks, s.judgePending(F2)...)
		if len(breaks) > 0 {
			s.halt("the supply identity is broken (§5.6: supply + uncredited burns + undelivered sends > lock − owed − voided + lost): " + strings.Join(breaks, "; "))
		}
		s.movingCut(w.move.why)
		return nil
	}
	s.movingCuts = 0
	cut.CreditedAfterBlock = after
	if ok, why := cut.Holds(); !ok {
		s.unverified("", why)
		return nil
	}
	// while any delivery is unplaced an honest burn above the index may be one, so a BROKEN reading waits as no verdict
	// (this signer signs nothing meanwhile) and halts once the index's finalized block's time has passed the reading's
	// with its excess, less the lag found, standing — or at once, past the most that lag can carry (E5-GT1); the
	// reading's height is the window's head (E6-GT1)
	confirmed := s.judgePending(F2)
	var broken, waits []string
	beyond := 0 // breaks past the most an honest lag can carry, while a delivery is unplaced (E5-GT1)
	for _, sym := range sortedSymbols(ids) {
		id := ids[sym]
		v, text := id.Verdict()
		if !id.SignerStops() && v != Broken {
			continue
		}
		if unplaced <= 0 {
			broken = append(broken, text)
			continue
		}
		if most, ok := lagMost(sym, snap.Block.Number, Hc, &s.state.Index, above); ok && excessOf(*id) > most {
			broken = append(broken, beyondLagText(text, excessOf(*id), most, snap.Block.Number, above))
			beyond++
			continue
		}
		if s.state.Pending[sym] == nil {
			s.state.Pending[sym] = &pendingBreak{Symbol: sym, Height: Hc, Time: w.time, Block: snap.Block.Number, Excess: excessOf(*id), Text: text, Since: s.now().Unix()}
		}
		waits = append(waits, pendingText(text, unplaced, snap.Block.Number, F2.Number))
	}
	if beyond > 0 {
		// halted at once, whatever another token's reading waits on; and the verdict below halts on every break of the
		// round, a pending reading confirmed in it among them
		confirmed = append(confirmed, broken...)
		broken = confirmed
	}
	if len(confirmed) > 0 {
		s.halt("the supply identity is broken (§5.6: supply + uncredited burns + undelivered sends > lock − owed − voided + lost): " + strings.Join(confirmed, "; "))
	}
	if len(waits) > 0 {
		s.waitRounds++
		s.noVerdict("", strings.Join(waits, "; "), s.waitRounds >= movingCutAlarm)
		return nil
	}
	s.waitRounds = 0
	for sym := range s.state.Pending {
		if _, ok := ids[sym]; !ok {
			delete(s.state.Pending, sym) // a token no more configured
		}
	}
	s.mu.Lock()
	s.identity = ids
	s.verified = true
	s.lastVerdict = s.now()
	s.verdictAt = snap.Block.Number
	s.cutNote = fmt.Sprintf("finalized block %d (%s) at %s (trusted height %d), then height %d at %s",
		snap.Block.Number, snap.Block.Hash.Hex(), snap.At.UTC().Format(time.RFC3339), T, head.Height, head.Time.UTC().Format(time.RFC3339))
	s.lastCheck = s.now()
	s.mu.Unlock()
	s.conclude(broken, head.Height, T)
	// the burns dust for an hour, signed, leave the state for the folded dust's file (E6-GT3)
	if n := s.state.Index.foldDust(s.now(), snap.Block.Number, d, realmFee(routes, lines), func(id string) bool { return s.signed[id] }); n > 0 {
		log.Printf("signer: %d burn(s) below their realm's fee for an hour folded out of the state (%d folded in all)", n, len(s.state.Index.folded()))
	}
	return nil
}

// foldedBroken are the tokens read BROKEN that hold folded dust (dust.go): their folded marks are read before they are
// judged.
func foldedBroken(ids map[string]*Identity, folded map[string][3]int64) map[string]bool {
	out := map[string]bool{}
	for sym, id := range ids {
		if id.SignerStops() && folded[sym][1] > 0 {
			out[sym] = true
		}
	}
	return out
}

// judgePending judges every pending break with the lag the index shows now (E4-GT1) and answers the confirmed ones'
// texts: explained by the lag, a reading ends; once the index's finalized block's time has passed the reading's, its
// excess less the lag found is a break. Sound whatever the round's own cut did: every honest burn credited at or below a
// reading's height has its mark read once hlmailbox's count moved past it.
func (s *Signer) judgePending(F2 *ethx.Block) []string {
	var confirmed []string
	for _, sym := range sortedSymbols(s.state.Pending) {
		p := s.state.Pending[sym]
		switch p.judge(&s.state.Index, int64(F2.Time)) {
		case pendingLag:
			log.Printf("signer: %s's reading at height %d was the lag of a provider: %d of burns credited above its block %d, the whole excess (E4-GT1)", sym, p.Height, p.lagSum(), p.Block)
			delete(s.state.Pending, sym)
		case pendingConfirmed:
			delete(s.state.Pending, sym)
			confirmed = append(confirmed, p.confirmedText(F2.Number, int64(F2.Time)))
		}
	}
	return confirmed
}

// brokenBeyondTheMove are the breaks a round whose cut moved halts on (E6-GT1): every token whose reading is BROKEN by
// §5.6's rule with no delivery unplaced by hlmailbox's last count, or past the most an honest lag can carry (E5-GT1) —
// a delivery or a dispatch landing inside the window takes from the right side only (cutorder.go).
func brokenBeyondTheMove(ids map[string]*Identity, unplaced int, F uint64, Hc int64, x *burnIndex, above *aboveIndex) []string {
	var out []string
	for _, sym := range sortedSymbols(ids) {
		id := ids[sym]
		if !id.SignerStops() {
			continue
		}
		_, text := id.Verdict()
		if unplaced > 0 {
			most, ok := lagMost(sym, F, Hc, x, above)
			if !ok || excessOf(*id) <= most {
				continue
			}
			text = beyondLagText(text, excessOf(*id), most, F, above)
		}
		out = append(out, text+" — read across a cut that moved (a delivery or a dispatch landing inside the window takes from the right side only: E6-GT1)")
	}
	return out
}

// takeWindow takes the cut's window, again at once while it moves (cutTries in all), and answers the last taking with
// the identities over its routes and lines (each token's supply the snapshot's; what is in flight is added after the
// window). The sends are read inside the window only while a token's can be refunded.
func (s *Signer) takeWindow(ctx context.Context, H int64, snap *ethSnapshot) (*cutWindow, map[string]*Identity, map[string]*gnoside.Migration, error) {
	var (
		w     *cutWindow
		ids   map[string]*Identity
		lines map[string]*gnoside.Migration
	)
	for try := 1; try <= cutTries; try++ {
		w = &cutWindow{}
		mb, err := gnoside.MailboxAt(ctx, s.gno, s.Cfg.Gno.Mailbox, H)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("hlmailbox: %w", err)
		}
		s.state.Gno.mailbox(mb, "hlmailbox's first read", s.now())
		w.first = mb
		ids, lines, w.routes, err = gnoIdentities(ctx, s.gno, s.Cfg, mb, H, snap)
		if err != nil {
			return nil, nil, nil, err
		}
		if refundable(s.Cfg, w.routes, lines) {
			w.sends = true
			if err := s.syncSends(ctx, mb, w.routes, lines, H, snap); err != nil {
				return nil, nil, nil, err
			}
		}
		w.height, w.time, err = headAfterCut(ctx, s.gno)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("own node's head after the cut: %w", err)
		}
		s.state.Gno.head(w.height, "the own node's head inside the cut's window", s.now())
		w.move, err = cutMoved(ctx, s.gno, s.Cfg, H, mb, w.routes, lines)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("the cut read again: %w", err)
		}
		s.state.Gno.mailbox(w.move.again, "hlmailbox's second read", s.now())
		if w.move.why == "" {
			break
		}
		log.Printf("signer: the cut moved (taking %d of %d): %s", try, cutTries, w.move.why)
	}
	return w, ids, lines, nil
}

// syncSends reads the sends in flight (sends.go) up to hlmailbox's count as the window first read it; a reading that
// fails is a round with no verdict.
func (s *Signer) syncSends(ctx context.Context, mb *gnoside.Mailbox, routes map[string]*gnoside.Route, lines map[string]*gnoside.Migration, H int64, snap *ethSnapshot) error {
	_, _, refundErr, err := s.state.Sends.sync(ctx, s.gno, s.pair, s.Cfg, mb, routes, lines, nil, H, snap.Pin, s.now(), sendsPerRound)
	if err == nil {
		err = refundErr
	}
	if err != nil {
		return fmt.Errorf("the sends in flight: %w", err)
	}
	return nil
}

// disagreeKey is a failed reading's alarm: eth:providers when the two providers disagreed, else the round's own.
func disagreeKey(err error) string {
	if errors.Is(err, ethx.ErrDisagree) {
		return "eth:providers"
	}
	return ""
}

// holdToOneNode holds a round's reads to one node's (E6-GT2, backends.go): the head read after the marks to every head
// read before it and above the highest mark read, and the deliveries placed at the window's head (the index's, and
// those pruned) to hlmailbox's count read after it.
func (s *Signer) holdToOneNode(ctx context.Context, top int64, delivered int, Hc int64) error {
	s.state.Gno.placed(s.state.Index.placedBy(Hc)+s.state.PrunedCredited, delivered, Hc, s.now())
	end, err := s.gno.Status(ctx)
	if err != nil {
		return err
	}
	s.state.Gno.head(end.Height, "the own node's head after the marks", s.now())
	s.state.Gno.marks(top, end.Height, s.now())
	return nil
}

// finalizedApart takes a round whose two finalized answers lay more than two epochs apart (E5-RT1) as no verdict,
// retried quietly and alarmed (eth:providers) at the movingCutAlarm-th such round in a row: two honest providers read
// seconds apart as the finalized block jumps several epochs at once disagree for a round, a stuck one for good. It
// answers false for any other error.
func (s *Signer) finalizedApart(err error, what string) bool {
	if !errors.Is(err, ethx.ErrFinalizedApart) {
		return false
	}
	s.apartRounds++
	s.noVerdict("eth:providers", what+ethReadText(err), s.apartRounds >= movingCutAlarm)
	return true
}

// anyStops says whether a token's identity, with what is in flight, reads BROKEN by §5.6's rule.
func anyStops(ids map[string]*Identity) bool {
	for _, id := range ids {
		if id.SignerStops() {
			return true
		}
	}
	return false
}

// noteFinality alarms a finality stall: the finalized block standing still for an hour (§5.6: the signers sign
// nothing newer meanwhile — liveness, never safety).
func (s *Signer) noteFinality(F uint64) {
	now := s.now()
	if F > s.state.FinalSeen {
		s.state.FinalSeen, s.state.FinalSince = F, now.Unix()
	}
	s.mu.Lock()
	s.finalSeen = s.state.FinalSeen
	s.mu.Unlock()
	since := time.Unix(s.state.FinalSince, 0)
	if s.state.FinalSince != 0 && now.Sub(since) > finalityStall {
		s.Alarm.Alarm("eth:finality-stall", fmt.Sprintf("Ethereum's finalized block has stood at %d since %s: nothing newer is signed until finality resumes (liveness, never safety, §5.6)", s.state.FinalSeen, since.UTC().Format(time.RFC3339)))
		return
	}
	s.Alarm.Clear("eth:finality-stall")
}

// conclude applies a verdict: a broken identity halts; a held one resumes — but §5.6's ahead-stop clears itself only
// once the identity holds at a height at or above T: a verdict at the head below T, inside the allowance, is one to
// sign on later, not the one that lifts that stop.
func (s *Signer) conclude(broken []string, head int64, T uint64) {
	s.Alarm.Clear("signer:no-verdict")
	if len(broken) > 0 {
		s.halt("the supply identity is broken (§5.6: supply + uncredited burns + undelivered sends > lock − owed − voided + lost): " + strings.Join(broken, "; "))
		return
	}
	s.mu.Lock()
	aheadHalt := s.aheadHalt
	s.mu.Unlock()
	if aheadHalt && uint64(head) < T {
		log.Printf("signer: the identity holds at height %d, below the trusted height %d: the stop stands until a verdict at or above it", head, T)
		return
	}
	s.resume()
}

// applyAhead acts on judgeAhead's verdict: a stop halts (the identity holding at a height at or above T clears it) and
// is kept in the state, so a restart never lifts it (E3-GT6); anything else is a round with no verdict, which alarms
// and never stops.
func (s *Signer) applyAhead(T uint64, v aheadVerdict, why string) {
	if v != aheadStop {
		s.unverified("", why)
		return
	}
	s.mu.Lock()
	s.cutNote = "no verdict: " + why
	s.aheadHalt = true
	s.state.AheadStop = &aheadStanding{T: T, Why: why, At: s.now().Unix()}
	s.mu.Unlock()
	if err := s.save(); err != nil { // the stop is on disk before the round goes on
		log.Printf("signer: state: %v", err)
	}
	log.Printf("signer: %s", why)
	s.halt("the client runs ahead of its chain (§5.6): " + why)
}

// signBurns signs the indexed burns not yet signed, in nonce order, each once its block is at or below the finalized
// block of the last verdict (a burn above it waits one round: the next verdict counts it in flight first). The
// checkpoint names our MerkleTreeHook and the message's nonce with a zero root: hlverifier checks neither root nor
// index, only the signatures over the message's id (PLAN.md §5.6, §13).
func (s *Signer) signBurns() error {
	hook := ethx.AddressTo32(Address(s.Cfg.Eth.MerkleTreeHook))
	s.mu.Lock()
	at := s.verdictAt
	s.mu.Unlock()
	for _, id := range s.state.Index.ids() {
		rec := s.state.Index.Burns[id]
		if s.signed[id] {
			continue
		}
		if rec.Block > at {
			log.Printf("signer: #%d %s waits: its block %d is above the verdict's finalized block %d (signed after the next verdict)", rec.Nonce, short(id), rec.Block, at)
			continue
		}
		raw, err := hex.DecodeString(rec.Message)
		if err != nil {
			return fmt.Errorf("burn %s: its message: %w", id, err)
		}
		m, err := hl.ParseMessage(raw)
		mid := hl.ID(raw)
		if err != nil || m.Nonce != rec.Nonce || hex.EncodeToString(mid[:]) != id {
			return fmt.Errorf("burn %s: the indexed message does not check out", id)
		}
		cp := hl.Checkpoint{Hook: hook, Index: rec.Nonce}
		sig := hl.SignEd25519(s.Key.Ed, cp, raw)
		sg := SignedBurn{Origin: m.Origin, Nonce: rec.Nonce, ID: id, Message: rec.Message, Hook: hex.EncodeToString(hook[:]), Root: hex.EncodeToString(cp.Root[:]),
			Block: rec.Block, Tx: rec.Tx, Signature: hex.EncodeToString(sig[:]), Signer: hex.EncodeToString(s.Key.EdPublic())}
		fresh, err := s.put(sg)
		if err != nil {
			return err
		}
		s.signed[id] = true
		if fresh {
			log.Printf("signer: #%d %s signed (%s %d to %s, block %d, tx %s)", rec.Nonce, short(id), rec.Symbol, rec.Amount, orNone(rec.To), rec.Block, rec.Tx)
		}
		if !s.signing() { // a nonce conflict halted us mid-batch
			return nil
		}
	}
	return nil
}

// prune drops the burns credited and signed from the index: what is in flight is what is left; each one dropped is
// counted, a delivery the index placed (E4-GT1).
func (s *Signer) prune() {
	for id, rec := range s.state.Index.Burns {
		if rec.Credited != 0 && s.signed[id] {
			delete(s.state.Index.Burns, id)
			s.state.PrunedCredited++
		}
	}
}

// put stores a signature, refusing to sign a second message under a nonce it already signed for: a signer that signs
// two messages for one place has equivocated. It stops signing and alerts (§5.6), and keeps serving what it signed.
// It reports whether the signature is new. A file that is not a signature is corruption, never a conflict.
func (s *Signer) put(sg SignedBurn) (bool, error) {
	p := filepath.Join(s.Store, "eth", nonceKey(sg.Nonce)+".json")
	if b, err := os.ReadFile(p); err == nil {
		var old SignedBurn
		if err := json.Unmarshal(b, &old); err != nil || old.ID == "" {
			return false, fmt.Errorf("signature file %s is %w: %v; restore it or delete it knowingly", p, errCorrupt, err)
		}
		if old.ID == sg.ID {
			return false, nil
		}
		why := fmt.Sprintf("nonce %d is signed for %s, now asked for %s", sg.Nonce, old.ID, sg.ID)
		s.mu.Lock()
		s.nonceHalt = true
		s.mu.Unlock()
		s.halt("a nonce conflict: " + why)
		return false, errors.New("REFUSING: " + why)
	}
	return true, writeJSON(p, sg)
}

// readSigned is a stored signature by nonce.
func (s *Signer) readSigned(nonce uint32) (*SignedBurn, error) {
	b, err := os.ReadFile(filepath.Join(s.Store, "eth", nonceKey(nonce)+".json"))
	if err != nil {
		return nil, err
	}
	var sg SignedBurn
	if err := json.Unmarshal(b, &sg); err != nil {
		return nil, err
	}
	return &sg, nil
}

// publishCounts copies the index's counts under the mutex for the status (the status never walks the index).
func (s *Signer) publishCounts() {
	n, w := len(s.state.Index.Burns)+len(s.state.Index.folded()), s.state.Index.waiting()
	s.mu.Lock()
	s.indexedN, s.waitingN, s.signedN = n, w, len(s.signed)
	s.cursorN, s.fromN, s.ignoredN, s.lineN = s.state.Index.Cursor, s.state.From, s.state.Index.IgnoredN, s.state.Index.Line
	s.mu.Unlock()
}

// Handler serves the public signature endpoint, GET /v1/eth/<nonce>, and nothing else: the status is served on a
// loopback listener of its own (StatusHandler), so a flood at the public endpoint reaches no loop state.
func (s *Signer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/eth/", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/v1/eth/"), 10, 32)
		if err != nil {
			http.Error(w, "bad nonce", http.StatusBadRequest)
			return
		}
		b, err := os.ReadFile(filepath.Join(s.Store, "eth", strconv.FormatUint(n, 10)+".json"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	return mux
}

// StatusHandler serves GET /v1/status (a loopback listener).
func (s *Signer) StatusHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		b, err := json.Marshal(s.Status())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ethx.Redact(string(b)))) // no provider URL's key in the status
	})
	return mux
}

// Status is what the status endpoint says: whether it signs, why not, and the identity it computed — copies made
// under the mutex, so the encoder never walks a map the loop writes.
func (s *Signer) Status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := map[string]any{}
	for sym, id := range s.identity {
		v, text := id.Verdict()
		ids[sym] = map[string]any{"lock": id.Lock, "supply": id.Supply, "owed": id.Owed, "voided": id.Voided, "lost": id.Lost, "uncredited": id.InflightIn, "undelivered": id.InflightOut,
			"right": id.Right(), "gap": id.Gap(), "block": id.Block, "height": id.Height, "verdict": v.String(), "stops": id.SignerStops(), "text": text}
	}
	fresh := s.verified && s.now().Sub(s.lastVerdict) <= s.verdictMaxAge()
	return map[string]any{
		"ed25519": hex.EncodeToString(s.Key.EdPublic()),
		"signing": s.halted == "" && fresh, "halted": s.halted, "nonceConflict": s.nonceHalt, "aheadHalt": s.aheadHalt, "verified": s.verified, "verdictFresh": fresh,
		"cut": s.cutNote, "lastCheck": s.lastCheck.UTC().Format(time.RFC3339), "lastVerdict": s.lastVerdict.UTC().Format(time.RFC3339), "verdictBlock": s.verdictAt,
		"identity": ids, "indexed": s.indexedN, "waiting": s.waitingN, "signed": s.signedN, "from": s.fromN, "cursor": s.cursorN,
		"ignored": s.ignoredN, "launchLine": s.lineN, "finalized": s.finalSeen, "marksRead": s.marksRead,
		"node": ethx.Redact(s.Cfg.Gno.RPC), "aheadAllowance": s.Cfg.AheadAllowance(), "alarms": s.Alarm.Standing(),
	}
}

// ------------------------------------------------------------------------------------------------- shared --

// gnoHeadWait is the longest a machine waits for a gno block after its Ethereum snapshot (mainnet's blocks come every
// 3.4 s; a wait past this means the node is stalled, which is no verdict).
const gnoHeadWait = 15 * time.Second

// gnoHeadAfter is the cut's second step: the own node's head once its block time is after t, polled until then or
// until gnoHeadWait has passed, in which case the head answered is not after t and the cut fails.
func gnoHeadAfter(ctx context.Context, g gnoside.Reader, t time.Time) (gno.Head, error) {
	deadline := time.Now().Add(gnoHeadWait)
	for {
		head, err := g.Status(ctx)
		if err != nil {
			return head, err
		}
		if head.Time.After(t) || time.Now().After(deadline) {
			return head, nil
		}
		select {
		case <-ctx.Done():
			return head, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// gnoSecondHead is the first other gno source's head (gno.sources: rpc.gno.land in a deployment); ok false when none
// is configured or none answered.
func gnoSecondHead(ctx context.Context, sources []gnoside.Reader) (uint64, bool) {
	for _, src := range sources {
		if head, err := src.Status(ctx); err == nil && head.Height > 0 && !head.CatchingUp {
			return uint64(head.Height), true
		}
	}
	return 0, false
}

// gnoIdentities reads every token's counters at a height for the identity — over the token's line of migrations while
// one is open, as the watch and hlwatch read (the Solana audit's K35: the configured route alone reads 0 against the
// live supply from page 0) — with each supply from the snapshot, and answers the lines read (the burn scan follows
// them) and the routes' renders (the sends' refund marks read them). mb is hlmailbox as the cut's window read it first
// (a line's migration is read against it). A route or a line that cannot be read is no verdict.
func gnoIdentities(ctx context.Context, g gnoside.Reader, cfg *Config, mb *gnoside.Mailbox, H int64, snap *ethSnapshot) (map[string]*Identity, map[string]*gnoside.Migration, map[string]*gnoside.Route, error) {
	read := gnoside.RouteReaderAt(g, H)
	ids := map[string]*Identity{}
	lines := map[string]*gnoside.Migration{}
	routes := map[string]*gnoside.Route{}
	for _, r := range cfg.Routes {
		if r.Token == "" {
			continue
		}
		rj, err := gnoside.RouteAt(ctx, g, r.Gno, H)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", r.Symbol, err)
		}
		routes[r.Symbol] = rj
		mig, err := gnoside.MigrationOf(ctx, read, r.Symbol, r.Gno, r.Predecessor, rj, mb)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s's line of migrations: %w", r.Symbol, err)
		}
		if mig != nil {
			lines[r.Symbol] = mig
		}
		id := identityOf(r, rj, mig, H, snap.Supplies[r.Symbol], snap.Block.Number, 0, 0)
		ids[r.Symbol] = &id
	}
	return ids, lines, routes, nil
}

// lineCounters are what a token's identity rests on as one read gave them — the route's counters, or its line's summed
// while a migration is open: lock, owed, voided, lost — with its refund count.
type lineCounters [5]int64

func countersOf(rj *gnoside.Route, mig *gnoside.Migration) lineCounters {
	var c lineCounters
	switch {
	case mig != nil:
		c[0], c[1], c[2], c[3] = mig.Sum()
	case rj != nil:
		c[0], c[1], c[2], c[3] = rj.Lock, rj.Owed, rj.Voided, rj.Lost
	}
	c[4] = monRefundCount(rj, mig)
	return c
}

// cutMove is what the cut's second read of hlmailbox found: why the cut moved ("" when it held), hlmailbox as read again,
// and whether it was a token's counters that moved (a refund, a migration's page) — the one move that can add to a
// reading — rather than a count, which only takes from it (E6-GT1: brokenAcrossAMove).
type cutMove struct {
	why      string
	again    *gnoside.Mailbox
	counters bool
}

// cutMoved is the cut's read-twice guard, taken at the end of its window (F7.3 item 1; the Go trust audit's E3-GT2, the
// plan red team's E3-RT3; E6-GT1). gno.land answers its latest state at any height — its realms' objects are not
// versioned — so the window's gno reads are a sequence of reads at whatever is latest: hlmailbox (first), the routes and
// their lines, the own node's head. A send dispatched after hlmailbox's first read and before the routes' is in the lock
// and not counted in flight; a credit landing after the routes' read is in neither owed nor in flight once its mark is
// read: either a reading short by its amount, room for a forged mint of that amount, burned back, to pass a round and
// have its burn signed. So hlmailbox is read again: its count moves with every dispatch and its delivered count with
// every credit (Process, and MarkDone's credit by vote). A token whose sends a refund can mark, or whose line of
// migrations is open, has its counters read again too: a refund applied, or a migration's page moving the counters
// between the line's realms, landing between the reads is the same slack (or a double count).
func cutMoved(ctx context.Context, g gnoside.Reader, cfg *Config, H int64, first *gnoside.Mailbox, routes map[string]*gnoside.Route, lines map[string]*gnoside.Migration) (cutMove, error) {
	again, err := gnoside.MailboxAt(ctx, g, cfg.Gno.Mailbox, H)
	if err != nil {
		return cutMove{}, fmt.Errorf("hlmailbox: %w", err)
	}
	mv := cutMove{again: again}
	var why []string
	if again.Count != first.Count {
		why = append(why, fmt.Sprintf("hlmailbox dispatched %d message(s) while the cut was read (its count %d, then %d: a send between hlmailbox's first read and the routes' is in the lock and not counted in flight)", int64(again.Count)-int64(first.Count), first.Count, again.Count))
	}
	if again.Delivered != first.Delivered {
		why = append(why, fmt.Sprintf("hlmailbox delivered %d message(s) while the cut was read (its delivered count %d, then %d: a credit after the routes' read is in neither owed nor in flight)", again.Delivered-first.Delivered, first.Delivered, again.Delivered))
	}
	read := gnoside.RouteReaderAt(g, H)
	for _, r := range cfg.Routes {
		rj, ok := routes[r.Symbol]
		if !ok || r.Token == "" {
			continue
		}
		mig := lines[r.Symbol]
		if mig == nil && !gnoside.Refundable(rj, nil) {
			continue // neither a refund nor a migration's page can move it: a credit moves the delivered count
		}
		rj2, err := gnoside.RouteAt(ctx, g, r.Gno, H)
		if err != nil {
			return cutMove{}, fmt.Errorf("%s: %w", r.Symbol, err)
		}
		mig2, err := gnoside.MigrationOf(ctx, read, r.Symbol, r.Gno, r.Predecessor, rj2, again)
		if err != nil {
			return cutMove{}, fmt.Errorf("%s's line of migrations: %w", r.Symbol, err)
		}
		if a, b := countersOf(rj, mig), countersOf(rj2, mig2); a != b {
			mv.counters = true
			why = append(why, fmt.Sprintf("%s's counters moved while the cut was read (lock, owed, voided, lost, refunds %v, then %v: a refund, or a migration's page, landing between the reads)", r.Symbol, a, b))
		}
	}
	mv.why = strings.Join(why, "; ")
	return mv, nil
}

// sortedSymbols are a map's keys, sorted.
func sortedSymbols[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ProviderRule checks §5.6's rule across the signer machines' configs: two providers each, no pair of providers shared
// by two signers, no provider in more than two signers — a forged burn then needs two signer machines, or three of
// the four providers, lying at once. Providers are compared by URL (a key in the URL makes two accounts two
// providers; the rule is the operator's to keep honest).
func ProviderRule(configs map[string]*Config) error {
	type pair struct{ a, b string }
	uses := map[string][]string{}
	pairs := map[pair]string{}
	names := sortedSymbols(configs)
	for _, name := range names {
		c := configs[name]
		if len(c.Eth.Providers) < 2 {
			return fmt.Errorf("%s: two Ethereum providers are needed (eth.providers lists %d)", name, len(c.Eth.Providers))
		}
		a, b := c.Eth.Providers[0].URL, c.Eth.Providers[1].URL
		if a == b {
			return fmt.Errorf("%s: the two providers are the same (%s)", name, ethx.Redact(a))
		}
		if b < a {
			a, b = b, a
		}
		if other, ok := pairs[pair{a, b}]; ok {
			return fmt.Errorf("%s and %s read Ethereum through the same pair of providers (%s, %s): no pair is shared by two signers (§5.6)", other, name, ethx.Redact(a), ethx.Redact(b))
		}
		pairs[pair{a, b}] = name
		for _, u := range []string{a, b} {
			uses[u] = append(uses[u], name)
			if len(uses[u]) > 2 {
				return fmt.Errorf("the provider %s is in %d signers (%s): no provider is in more than two (§5.6)", ethx.Redact(u), len(uses[u]), strings.Join(uses[u], ", "))
			}
		}
	}
	return nil
}

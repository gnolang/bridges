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

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// Signed is one signer's checkpoint signature for one message, as it serves it.
type Signed struct {
	Origin    uint32 `json:"origin"`
	Nonce     uint32 `json:"nonce"`
	ID        string `json:"id"`
	Message   string `json:"message"`
	Hook      string `json:"hook"`
	Root      string `json:"root"`
	Slot      uint64 `json:"slot,omitempty"` // the Solana slot of the dispatch
	Signature string `json:"signature"`      // ed25519 over the checkpoint digest
	Signer    string `json:"signer"`         // the ed25519 key in hex
}

// noVerdictHalt is how long a signer keeps signing on an old verdict before it stops: an hour without a fresh
// held cut (a provider misreporting a supply, an outage of either) halts it until one returns.
const noVerdictHalt = time.Hour

// Signer is one of the three signer machines of PLAN.md §5.6: it reads gno.land through its own node and Solana
// through two providers that must agree, signs ed25519 over Hyperlane's checkpoint digest once per message
// dispatched toward gno.land (refusing two messages for one nonce: a nonce conflict stops it and alerts, it does
// not exit), serves its signatures, and stops signing the moment the supply identity it computes shows Solana's
// supply — the burns dispatched and not yet credited counted as supply — above the lock, saying why on its
// status endpoint, until a void as forged closes it again; it stops too when the client's trusted height runs
// more than an hour of blocks past both its own node's head and a second source's (the client is ahead of its
// chain, the mismatch itself), until the identity holds at a height at or above that trusted height. It signs on
// no verdict older than one round, and halts after an hour without one. It signs nothing on Solana: the light
// client checks gno.land's validators there.
type Signer struct {
	Cfg   *Config
	Key   *hl.ValidatorKey
	Store string // directory: sol/<nonce>.json, state.json
	Every time.Duration
	Alarm *Alarmer

	gno     gnoReader     // the own node
	sources []*gno.Client // the other gno sources (gno.sources): the second source of §5.6's stop
	sol     [2]*rpc.Client
	now     func() time.Time

	// The dispatch index: every burn toward gno.land either provider's scan found, by id, with the providers
	// that showed it; the unsigned ones in provider 1's order; the scans' own cursors and side lists (in memory:
	// a restart puts back every burn it signed, from the store, and scans again from where the state says nothing
	// unsigned lies below — E5-SF3, E6-SF1).
	index    map[string]*signerDispatch
	unsigned []*signerDispatch
	icur     map[string]scanCursor // "SYM#i" → provider i's index cursor
	skipped  map[string][]scanSkip // "SYM#i" → the transactions provider i could not read
	signed   map[string]bool       // ids signed (the store's files)
	gaps     historyGaps           // the providers refused for a route's history (E6-SF1): no verdict while one stands

	mu    sync.Mutex
	state struct {
		SolCursors map[string]string `json:"solCursors"` // by route symbol + "#0": the signing cursor (the last signed dispatch's transaction)
		// Floor is the highest finalized slot a round's snapshot was taken at (the Go trust audit's E3-GT9): a later
		// snapshot below it is a provider stepped back, no verdict; kept here so a restart keeps it.
		Floor uint64 `json:"floor,omitempty"`
		// Credits counts hlmailbox's credits between rounds (E4-GT1), written with the floor: a restart goes on from
		// the last round written, never from one window over every credit (the index scans again from the signing
		// cursor only).
		Credits creditBook `json:"credits"`
		// SolThrough is, by "SYM#i", the slot a restart lists provider i's route back to (E6-SF1): the slot its listing
		// was read through, below every burn it listed that is neither signed (the store puts those back) nor credited,
		// and below every transaction it could not read (the side list is in memory) — so a restart lists again every
		// burn the index held that the store does not (E5-SF3's rule), and a quiet route, whose signing cursor leaves a
		// provider's day of history, is still listed from a slot inside it.
		SolThrough map[string]uint64 `json:"solThrough,omitempty"`
		// SolThroughHosts are the providers, by place, SolThrough was read through (a slot dropped when another takes it)
		SolThroughHosts []string `json:"solThroughHosts,omitempty"`
	}
	minCtx      minContextCheck      // the providers' minContextSlot self-check (E5-SF2)
	marksAt     time.Time            // when every uncredited mark was last read (markRefresh)
	halted      string               // why signing stopped ("" while signing)
	nonceHalt   bool                 // a nonce conflict: sticky until a human looks
	aheadHalt   bool                 // §5.6's ahead-stop: cleared only by a verdict at a height at or above T (K14)
	verified    bool                 // the identity held on the last check: the condition for signing
	lastVerdict time.Time            // when the last held cut gave a verdict
	verdictSlot uint64               // the Solana slot the last verdict's snapshot was taken at (G13)
	identity    map[string]*Identity // the last evaluation per route
	cutNote     string
	lastCheck   time.Time
	movingCuts  int // rounds in a row whose gno reads moved under them — a credit, a migration's page (no verdict, retried quietly)
	stepBacks   int // rounds in a row whose providers answered below the floor (no verdict, retried quietly)
	// unplacedSince is when the rounds with credits the index could not place yet began (E4-GT1: no verdict, retried
	// quietly, alarmed once it has stood two minutes); zero while there are none.
	unplacedSince time.Time
	// The index's counts as the status serves them (G24): published by the loop under the mutex after each scan
	// and each signing batch, so the status never walks the index or the unsigned list the loop writes.
	indexedN, unsignedN, disputedN int
}

// signerDispatch is one indexed burn: the dispatch as provider 1 reported it (or provider 2, when only it has),
// the route, the amount, which providers showed it (a bit each) and the gno height it was credited at (0 while
// waiting). A dispatch the two providers showed with a different slot, amount or recipient is disputed (G17):
// no verdict rests on it and it is not signed until a read through both providers agrees.
type signerDispatch struct {
	d         solDispatch
	symbol    string
	amount    int64
	bad       bool // the recipient is no gno address: the realm sweeps it
	providers int
	credited  int64
	disputed  string
	mark      markSeen // when its delivered mark was last read unset (E5-SF5: read again only once a credit landed)
}

func (s *Signer) load() error {
	if err := os.MkdirAll(filepath.Join(s.Store, "sol"), 0o700); err != nil {
		return err
	}
	if err := readJSON(filepath.Join(s.Store, "state.json"), &s.state); err != nil {
		return err
	}
	if s.state.SolCursors == nil {
		s.state.SolCursors = map[string]string{}
	}
	s.index = map[string]*signerDispatch{}
	s.icur = map[string]scanCursor{}
	s.skipped = map[string][]scanSkip{}
	s.signed = map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(s.Store, "sol", "*.json"))
	for _, f := range files {
		var sg Signed
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &sg); err != nil || sg.ID == "" {
			return fmt.Errorf("signature file %s is %w: %v; restore it or delete it knowingly", f, errCorrupt, err)
		}
		s.signed[sg.ID] = true
		if err := s.reindex(sg); err != nil {
			return fmt.Errorf("signature file %s is %w: %v; restore it or delete it knowingly", f, errCorrupt, err)
		}
	}
	if s.now == nil {
		s.now = time.Now
	}
	return nil
}

// reindex puts a stored signature's burn back into the index as a signed record (E5-SF3): the index began at the
// signing cursor after a restart, so a burn signed and not credited yet — waiting for the relayer's next tick, or for
// hours while the route's credits are flagged, which is when an operator restarts — was in no index, the reading
// short by it, and a forged mint of its amount burned straight back was signed by every restarted signer (a deploy
// restarts them all). Its credited height is read from hlmailbox on the next round, as any record's: the burn counts in
// flight until its credit, restart or not, and its credit is placed by it (E4-GT1: no quiet window over the credits
// behind the cursor). A message that does not hash to its file's id is corruption; a burn of nothing, or of a route
// the config no longer names, is no record.
func (s *Signer) reindex(sg Signed) error {
	raw, err := hex.DecodeString(sg.Message)
	if err != nil {
		return fmt.Errorf("its message is not hex: %v", err)
	}
	d := solDispatch{Raw: raw, Slot: sg.Slot}
	if err := d.fill(); err != nil {
		return fmt.Errorf("its message does not parse: %v", err)
	}
	if id := d.IDHex(); id != sg.ID {
		return fmt.Errorf("its message's id is %s, not the file's %s", id, sg.ID)
	}
	rt := s.Cfg.RouteByToken(solana.PublicKeyFromBytes(d.Msg.Sender[:]))
	_, amount, bad, ok := d.token()
	if rt == nil || !ok || amount == 0 {
		return nil
	}
	s.index[sg.ID] = &signerDispatch{d: d, symbol: rt.Symbol, amount: amount, bad: bad, providers: 3}
	return nil
}

func (s *Signer) save() error { return writeJSON(filepath.Join(s.Store, "state.json"), s.state) }

// Run signs until ctx ends.
func (s *Signer) Run(ctx context.Context) error {
	if err := s.load(); err != nil {
		return err
	}
	urls, err := s.Cfg.TwoProviders()
	if err != nil {
		return err
	}
	s.sol = [2]*rpc.Client{sol.NewRPC(urls[0]), sol.NewRPC(urls[1])}
	if s.state.SolThrough != nil {
		s.state.SolThroughHosts = keepThroughOf(s.state.SolThrough, s.state.SolThroughHosts, urls[:])
	} else {
		s.state.SolThroughHosts = keepThroughOf(nil, nil, urls[:])
	}
	s.gno = s.Cfg.GnoClient()
	for _, src := range s.Cfg.Gno.Sources {
		s.sources = append(s.sources, s.Cfg.SourceClient(src))
	}
	if len(s.sources) == 0 {
		log.Printf("signer: WARNING no second gno source (gno.sources): §5.6's stop when the client runs ahead cannot be judged; it alarms instead")
	}
	s.identity = map[string]*Identity{}
	log.Printf("signer %s: own node %s, sources %v, providers %s and %s, store %s, the client may run %d blocks ahead", hex.EncodeToString(s.Key.EdPublic()), s.Cfg.Gno.RPC, s.Cfg.Gno.Sources, urls[0], urls[1], s.Store, s.Cfg.AheadAllowance())
	for {
		if err := s.checkIdentity(ctx); err != nil {
			log.Printf("signer: identity: %v", err)
		}
		if s.signing() {
			if err := s.signSol(ctx); err != nil {
				log.Printf("signer: solana: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Every):
		}
	}
}

// verdictMaxAge is how old the last held cut's verdict may be for this round to sign on it: the previous round's
// (AN2: a signer signs on no verdict older than one round; the round's own jitter allowed for).
func (s *Signer) verdictMaxAge() time.Duration {
	if s.Every <= 0 {
		return 2 * 3 * time.Second
	}
	return 2 * s.Every
}

// signing is whether this round may sign: no halt stands, the identity was verified over a held cut, and that
// verdict is no older than one round.
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
	}
	s.mu.Unlock()
	if was != "" && !s.nonceHalt {
		log.Printf("signer: signing resumes (the identity holds again)")
		s.Alarm.Clear("signer:halt")
	}
}

// disputed is the first indexed dispatch the two providers showed differently (G17), by id, and why; "" when
// none stands.
func (s *Signer) disputed() (id, why string) {
	for _, id := range sortedKeys(s.index) {
		if rec := s.index[id]; rec.disputed != "" {
			return id, "the providers disagree on dispatch " + id + ": " + rec.disputed
		}
	}
	return "", ""
}

// oneSided names the first burn not credited yet that one provider alone lists at or below its route's supply slot
// (E5-SF4), "" when none.
func (s *Signer) oneSided(snap *supplySnapshot) string {
	for _, id := range sortedKeys(s.index) {
		rec := s.index[id]
		if slot := snap.SlotOf(rec.symbol); rec.credited == 0 && rec.disputed == "" && rec.providers != 3 && rec.d.Slot <= slot {
			return fmt.Sprintf("dispatch %s (%s, %d at slot %d) is listed by provider %d alone, the other's listing past its slot %d: no reading of Solana until both show it (E5-SF4)", id, rec.symbol, rec.amount, rec.d.Slot, rec.providers, slot)
		}
	}
	return ""
}

// verdictSlotNow is the slot of the last verdict's Solana snapshot (0 before the first).
func (s *Signer) verdictSlotNow() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verdictSlot
}

// publishCounts copies the index's counts under the mutex for the status (G24): called by the loop, the only
// writer of the index and the unsigned list, after each scan and each signing batch.
func (s *Signer) publishCounts() {
	disputed := 0
	for _, rec := range s.index {
		if rec.disputed != "" {
			disputed++
		}
	}
	s.mu.Lock()
	s.indexedN, s.unsignedN, s.disputedN = len(s.index), len(s.unsigned), disputed
	s.mu.Unlock()
}

// signerIdentities reads every token's counters at a height for the refusal rule — over the token's line of
// migrations while one is open, as the watch and hlwatch read (K35: the configured route alone read 0 against
// the live supply from page 0, so all three signers halted with signer:halt, a false break, until a config
// edit); a successor the config names before page 0 is found back to its predecessor through the mailbox's
// entries (mb, read before the routes: the read-twice guard holds it to a read after the marks, E3-GT2). A route or
// a partner that cannot be read is no verdict this round. It answers the routes and the open lines as read too.
func signerIdentities(ctx context.Context, g gnoReader, cfg *Config, H int64, snap *supplySnapshot, mb *mailboxJSON) (map[string]*Identity, map[string]*routeJSON, map[string]*migration, error) {
	ids := map[string]*Identity{}
	routes := map[string]*routeJSON{}
	for _, r := range cfg.Routes {
		if r.Token == "" {
			continue
		}
		rj, err := routeAt(ctx, g, r.Gno, H)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", r.Symbol, err)
		}
		routes[r.Symbol] = rj
	}
	migs, errs := migrationsOf(ctx, g, cfg, routes, mb, H)
	for sym, err := range errs {
		return nil, nil, nil, fmt.Errorf("%s's line of migrations: %w", sym, err)
	}
	for _, r := range cfg.Routes {
		if r.Token == "" {
			continue
		}
		id := identityOf(r, routes[r.Symbol], migs[r.Symbol], H, snap.Supplies[r.Symbol], snap.SlotOf(r.Symbol), 0, 0)
		ids[r.Symbol] = &id
	}
	return ids, routes, migs, nil
}

// checkIdentity evaluates §5.6's refusal rule over §5.9's ordered cut: the Solana snapshot first (both
// providers: the supplies, and the client's trusted height T), then gno.land through the own node at a height at
// or above T whose time is after the snapshot — or, T above the head inside the allowance, at the head itself
// (§5.9's "the chain's head when the client is past it": the identity is judged there, never skipped) — then the
// dispatch index against that height, the burns dispatched at or before the snapshot and not yet credited
// counted as supply. T more than an hour of blocks past both the own node's head and a second source's stops it
// (judgeAhead); a reading the node cannot give yet (the order not holding, the own node past the allowance
// alone) alarms and never stops a signer that was signing — but a round with no verdict leaves nothing to sign
// on past the next round, and an hour of them halts; otherwise only a broken identity stops it, and every stop
// but a nonce conflict clears itself once the identity holds at a height at or above T. A signer never signs
// before its first verdict.
func (s *Signer) checkIdentity(ctx context.Context) error {
	snap, err := solSupplies(ctx, s.sol, s.Cfg.Routes)
	if err != nil {
		s.unverified("the Solana snapshot: " + err.Error())
		return err
	}
	// the floor (E3-GT9): a snapshot below the highest slot a round already read at is a provider stepped back — no
	// verdict, retried quietly, alarmed at the fifth such round in a row; never a reading
	if why := belowFloor(snap.Slot, s.state.Floor); why != "" {
		s.stepBack(why)
		return nil
	}
	s.raiseFloor(snap.Slot)
	// a provider whose listing ignores minContextSlot feeds an index that cannot be held to the supplies' slot: a burn out
	// of the supply could be out of the index, the reading short by it (E5-SF2) — no verdict while the config names it
	if why := s.minCtx.check(ctx, s.sol, s.Cfg, snap.Slot, s.now(), s.Alarm, true); why != "" {
		s.unverified(why)
		return nil
	}
	var T uint64
	if s.Cfg.HasPrograms() {
		var css [2]*clientState
		for i, c := range s.sol {
			if css[i], err = readClientState(ctx, c, s.Cfg.Programs().LightClient(), rpc.CommitmentFinalized); err != nil {
				s.unverified("the client's state from provider " + strconv.Itoa(i+1) + ": " + err.Error())
				return err
			}
		}
		if css[0].Head.TrustedHeight != css[1].Head.TrustedHeight || css[0].Head.TrustedHash != css[1].Head.TrustedHash {
			s.unverified("the providers disagree on the client's trusted header")
			return nil
		}
		// both answered at or above the supplies' slot (E3-GT9: a lower answer lowered the snapshot's slot, and the
		// burns between — burned in the supply already — counted in flight no more)
		if why := clientBelow(min(css[0].Slot, css[1].Slot), snap.Slot); why != "" {
			s.stepBack(why)
			return nil
		}
		T = css[0].Head.TrustedHeight
	}
	s.stepBacks = 0
	s.Alarm.Clear("solana:finalized-floor")
	head, err := headAfter(ctx, s.gno, snap.At)
	if err != nil {
		s.unverified("own node: " + err.Error())
		return err
	}
	if head.CatchingUp {
		s.unverified(fmt.Sprintf("own node is catching up (head %d)", head.Height))
		return nil
	}
	if uint64(head.Height) < T {
		allowance := s.Cfg.AheadAllowance()
		if T-uint64(head.Height) > allowance { // past the allowance: the second source decides
			second, secondOK := s.secondHead(ctx)
			s.applyAhead(judgeAhead(T, uint64(head.Height), second, secondOK, allowance))
			return nil
		}
		// inside the allowance: the identity is evaluated at the own node's head (AN2)
	}
	cut := Cut{SolSlot: snap.Slot, SolTime: snap.At, GnoHeight: head.Height, GnoTime: head.Time}
	if ok, why := cut.Holds(); !ok {
		s.unverified(why)
		return nil
	}
	// hlmailbox before the routes, held to a read once every mark is read (cutMoved, below; E3-GT2): a gno node
	// answers its latest state at any height, so a credit landing between the routes' read and its burn's mark is in
	// neither owed nor in flight
	before, err := mailboxAt(ctx, s.gno, s.Cfg.Gno.Mailbox, head.Height)
	if err != nil {
		s.unverified("hlmailbox: " + err.Error())
		return err
	}
	ids, routes, migs, err := signerIdentities(ctx, s.gno, s.Cfg, head.Height, snap, before)
	if err != nil {
		s.unverified(err.Error())
		return err
	}
	// The dispatch index, refreshed after the gno read: a burn credited at this height was finalized before it,
	// so a scan now sees it. One dispatched after its route's supply slot and already credited breaks the order;
	// one dispatched at or before it and not credited is supply the lock already answers for (AN3) — each route's
	// listing reaching that slot (E4-SF3: a listing behind the supply's left a burn out of both).
	err = s.indexScan(ctx, snap)
	s.publishCounts()
	if err != nil {
		s.unverified("the dispatch index: " + err.Error())
		return err
	}
	// a provider that cannot list a route back to where it was read (E6-SF1): the index rests on the other provider's
	// word alone for that route — no verdict, never a guess, until a provider that keeps the route's history is named
	if len(s.gaps.why) > 0 {
		var why []string
		for _, sym := range sortedKeys(gapsOf(s.gaps.why)) {
			why = append(why, sym+": "+gapsOf(s.gaps.why)[sym])
		}
		s.unverified("the dispatch index rests on one provider's listing (E6-SF1): " + strings.Join(why, "; "))
		return nil
	}
	// a dispatch the providers show differently is no reading of Solana (G17): no verdict until they agree
	if id, why := s.disputed(); id != "" {
		s.Alarm.Alarm("solana:providers:"+id[:8], why)
		s.unverified(why)
		return nil
	}
	// the delivered marks of the burns not credited yet, each read again only once hlmailbox's delivered count moved
	// since it was read unset (E5-SF5: the burns nobody credits were read every round, for good) — all of them while the
	// book holds a window the index could not place, and every markRefresh (a stale read must not stand)
	allMarks := len(s.state.Credits.Open) > 0 || s.now().Sub(s.marksAt) >= markRefresh
	var unread []string
	for id, rec := range s.index {
		if rec.credited == 0 && (allMarks || rec.mark.stale(before.Delivered)) {
			unread = append(unread, id)
		}
	}
	marks, err := deliveredMarks(ctx, s.gno, s.Cfg.Gno.Mailbox, unread, before.Delivered, head.Height)
	if err != nil {
		s.unverified(err.Error())
		return err
	}
	if allMarks {
		s.marksAt = s.now()
	}
	for _, id := range unread {
		if rec := s.index[id]; marks[id] != 0 {
			rec.credited = marks[id]
		} else {
			rec.mark = markSeen{delivered: before.Delivered, read: true}
		}
	}
	// a burn not credited yet that one provider alone lists at or below its route's supply slot — the other's listing,
	// which reaches that slot, passed it — is no reading of Solana (E5-SF4: counted in flight, a lying provider's
	// fabricated burn halted the signer as a break; dropped, a provider hiding a real burn would leave the reading short
	// by it): no verdict until the other lists it too or its credit proves it (unconfirmedOf)
	if why := s.oneSided(snap); why != "" {
		s.unverified(why)
		return nil
	}
	for id, rec := range s.index {
		// each burn against its own route's supply slot (E4-SF3: one landing between two routes' reads, out of the
		// later route's supply, was out of flight against the lower slot — room for a forged mint of its amount)
		switch slot := snap.SlotOf(rec.symbol); {
		case rec.credited != 0 && rec.d.Slot > slot:
			cut.CreditedAfterSlot = append(cut.CreditedAfterSlot, id)
		case rec.credited == 0 && rec.d.Slot <= slot:
			if idn, ok := ids[rec.symbol]; ok {
				idn.InflightIn += rec.amount
			}
		}
	}
	// the height the delivered count is exact at (E4-GT1): read between the cut's two reads of hlmailbox
	atHead, err := s.gno.Status(ctx)
	if err != nil {
		s.unverified("own node: " + err.Error())
		return err
	}
	// every mark read: hlmailbox and the open lines held to the first read (E3-GT2) — a move is no verdict, quiet; but a
	// move of the delivered count alone makes a reading short, never over, so a token broken by more than the credits it
	// cannot place could explain is broken whatever landed (E6-GT1's Solana sibling): halted at once
	mv, err := cutMovedBy(ctx, s.gno, s.Cfg, head.Height, before, routes, migs, false)
	if err != nil {
		s.unverified("the cut read again after the marks: " + err.Error())
		return err
	}
	if mv.why != "" {
		s.movingCut(mv.why)
		if mv.creditsOnly {
			s.brokenPastTheCut(ctx, ids, migs, head.Height, mv.again.Delivered, snap, "read across a moving cut")
		}
		return nil
	}
	s.movingCuts = 0
	// the credits no index holds yet may be burns above the snapshot's slot (E4-GT1: a signer whose providers trail
	// the others' halted over an honest credit): no verdict, quiet, until the index places them or the snapshot passes
	// their time — but not over a token broken by more than they could explain
	if why, _ := s.state.Credits.place(before.Delivered, atHead.Height, atHead.Time, signerCreditedHeights(s.index), snap.Time); why != "" {
		s.unplacedRound(why)
		s.brokenPastTheCut(ctx, ids, migs, head.Height, before.Delivered, snap, "read with credits the index cannot place yet")
		return nil
	}
	s.unplacedSince = time.Time{}
	if ok, why := cut.Holds(); !ok {
		s.unverified(why)
		s.brokenPastTheCut(ctx, ids, migs, head.Height, before.Delivered, snap, "read with burns credited above their slot")
		return nil
	}
	var broken []string
	for _, sym := range sortedKeys(ids) {
		id := ids[sym]
		v, text := id.Verdict()
		if id.SignerStops() || v == Broken {
			broken = append(broken, text)
		}
	}
	s.mu.Lock()
	s.identity = ids
	s.verified = true
	s.lastVerdict = s.now()
	s.verdictSlot = snap.Slot
	s.cutNote = fmt.Sprintf("slot %d at %s (trusted height %d), then height %d at %s", cut.SolSlot, cut.SolTime.UTC().Format(time.RFC3339), T, cut.GnoHeight, cut.GnoTime.UTC().Format(time.RFC3339))
	s.lastCheck = s.now()
	s.mu.Unlock()
	s.conclude(broken, head.Height, T)
	return nil
}

// brokenPastTheCut halts the signer when a token reads broken by more than the credits this round cannot place could put
// it over by (overBound) and its burns in flight besides (a stale mark counts a credited burn in flight and owed alike:
// no verdict and heal, never a halt): a round whose cut moved by credits alone, or held with credits the index cannot
// place yet, or with burns credited above their slot (E6-GT1's Solana sibling: a forger's stream of credits kept every
// round at no verdict, the halt an hour away). Nothing else changes: a token within that bound waits for the next held cut.
func (s *Signer) brokenPastTheCut(ctx context.Context, ids map[string]*Identity, migs map[string]*migration, H int64, d int, snap *supplySnapshot, how string) {
	var stops []string
	for _, sym := range sortedKeys(ids) {
		if ids[sym].SignerStops() {
			stops = append(stops, sym)
		}
	}
	if len(stops) == 0 {
		return
	}
	// every burn the index holds above its route's supply slot, read credited or not (a mark read unset at a count that
	// has not moved, or by a backend behind: the review of lane s7, R1), by its amount (a round with a disputed record
	// stopped before its marks)
	after := map[string]int64{}
	for _, rec := range s.index {
		if rec.d.Slot > snap.SlotOf(rec.symbol) {
			after[rec.symbol] += rec.amount
		}
	}
	indexed := func(id string) bool { _, ok := s.index[id]; return ok }
	bound, ok := overBound(ctx, s.gno, s.Cfg, migs, nil, d, H, s.state.Credits.boundFrom(), snap.Time, indexed, after)
	if !ok {
		return
	}
	var broken []string
	for _, sym := range stops {
		id := ids[sym]
		if over := id.Right() + id.InflightOut - id.Lock; over > bound[sym] { // burns in flight left out
			_, text := id.Verdict()
			broken = append(broken, fmt.Sprintf("%s — %s, over by %d with no burn in flight counted: more than the %d the credits it cannot place could put it over by (E6-GT1)", text, how, over, bound[sym]))
		}
	}
	if len(broken) > 0 {
		s.halt("the supply identity is broken (§5.6: supply + uncredited burns > lock − owed − voided + lost): " + strings.Join(broken, "; "))
	}
}

// conclude applies a verdict: a broken identity halts; a held one resumes — but §5.6's ahead-stop clears
// itself only once the identity holds at a height at or above T (K14): a verdict at the head below T, inside
// the allowance, is one to sign on later, not the one that lifts that stop.
func (s *Signer) conclude(broken []string, head int64, T uint64) {
	s.Alarm.Clear("signer:no-verdict")
	if len(broken) > 0 {
		s.halt("the supply identity is broken (§5.6: supply + uncredited burns > lock − owed − voided + lost): " + strings.Join(broken, "; "))
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

// unverified is a round with no verdict: it alarms (the webhook once an hour while it lasts) and changes nothing
// about whether the signer signs — this round and the next sign on the last verdict, a halted one stays halted,
// one that never had a verdict keeps waiting for its first — until an hour has passed without one, which halts
// the signer until a verdict returns (G7).
func (s *Signer) unverified(why string) {
	s.noVerdict("", why, true)
}

// noVerdict records a round without a verdict under an alarm key ("" for signer:no-verdict); alarm false keeps it
// quiet — the hour without a verdict halts all the same.
func (s *Signer) noVerdict(key, why string, alarm bool) {
	s.mu.Lock()
	s.cutNote = "no verdict: " + why
	stale := s.verified && !s.lastVerdict.IsZero() && s.now().Sub(s.lastVerdict) > noVerdictHalt && s.halted == ""
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
		s.halt("no verdict for an hour (the last held cut was at " + last.UTC().Format(time.RFC3339) + "): " + why)
	}
}

// movingCutAlarm is how many rounds in a row a moving cut, or a snapshot below the floor, may leave without a
// verdict before it alarms: one is the ordinary race of a busy chain (or a provider's backend a slot behind),
// retried next round in silence.
const movingCutAlarm = 5

// movingCut is a round whose gno reads moved under it (E3-GT2: a credit, a migration's page landing between them):
// no verdict, the cut taken again next round in silence, alarmed (signer:no-verdict) at the movingCutAlarm-th in a
// row.
func (s *Signer) movingCut(why string) {
	s.movingCuts++
	s.noVerdict("", fmt.Sprintf("%s (%d round(s) in a row)", movedText(why, "round"), s.movingCuts), s.movingCuts >= movingCutAlarm)
}

// unplacedRound is a round with credits the index could not place yet (E4-GT1): no verdict, retried quietly, alarmed
// (signer:no-verdict) once such rounds have stood two minutes (a restart before the book was kept, or a provider
// trailing the chain, is a few rounds: the snapshot passes a credit's block by the margin ≈18 s after it, E5-SF6).
func (s *Signer) unplacedRound(why string) {
	if s.unplacedSince.IsZero() {
		s.unplacedSince = s.now()
	}
	since := s.now().Sub(s.unplacedSince)
	s.noVerdict("", fmt.Sprintf("%s (for %s)", why, since.Round(time.Second)), since >= cutFailAfter)
}

// signerCreditedHeights are the heights the signer's indexed burns were credited at (not the disputed ones).
func signerCreditedHeights(index map[string]*signerDispatch) []int64 {
	var out []int64
	for _, rec := range index {
		if rec.credited != 0 && rec.disputed == "" {
			out = append(out, rec.credited)
		}
	}
	return out
}

// stepBack is a round whose providers answered below the floor (E3-GT9): no verdict, retried quietly, alarmed
// (solana:finalized-floor) at the movingCutAlarm-th in a row; never a reading.
func (s *Signer) stepBack(why string) {
	s.stepBacks++
	s.noVerdict("solana:finalized-floor", fmt.Sprintf("%s (%d round(s) in a row)", why, s.stepBacks), s.stepBacks >= movingCutAlarm)
}

// raiseFloor keeps the highest snapshot slot a round read at, written to the state before the round goes on (E3-GT9:
// a restart keeps it). A write that fails is logged and the round goes on with the floor in memory: only a restart
// before the next good write would forget the rise.
func (s *Signer) raiseFloor(slot uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slot <= s.state.Floor {
		return
	}
	s.state.Floor = slot
	if err := s.save(); err != nil {
		log.Printf("signer: state: the floor %d not written: %v", slot, err)
	}
}

// aheadVerdict is §5.6's reading of the client's trusted height T when it is above the own node's head.
type aheadVerdict int

const (
	aheadInside   aheadVerdict = iota // T above the head inside the allowance: the identity is judged at the head; never a stop
	aheadOwnLags                      // T past the allowance on the own node, but a second source's head is within it: the own node lags; never a stop
	aheadNoSecond                     // T past the allowance on the own node and no second source answered: nothing to compare; never a stop
	aheadStop                         // T past the allowance on the own node and on a second source: the client is ahead of its chain, the mismatch itself; the signer stops
)

// judgeAhead applies §5.6's stop (pure): T the client's trusted height, own the own node's head (below T), second
// a second source's head (secondOK false when none answered), allowance the hour of blocks. The stop needs T to
// exceed both heads by more than the allowance; anything less is a reading the node cannot give yet.
func judgeAhead(T, own, second uint64, secondOK bool, allowance uint64) (aheadVerdict, string) {
	if T-own <= allowance {
		return aheadInside, fmt.Sprintf("own node's head %d is below the client's trusted height %d (inside the allowance of %d blocks): the identity is judged at the head", own, T, allowance)
	}
	switch {
	case !secondOK:
		return aheadNoSecond, fmt.Sprintf("own node's head %d is %d blocks below the client's trusted height %d, past the allowance of %d, and no second source answered: nothing to judge the client against", own, T-own, T, allowance)
	case second+allowance >= T: // the second source within the allowance below T, or past it (T−second would wrap)
		return aheadOwnLags, fmt.Sprintf("own node's head %d is %d blocks below the client's trusted height %d, past the allowance of %d, while the second source's head %d is within it: the own node lags", own, T-own, T, allowance, second)
	}
	return aheadStop, fmt.Sprintf("the client's trusted height %d is more than %d blocks (an hour) past the own node's head %d and the second source's head %d: the client runs ahead of its chain, the mismatch itself", T, allowance, own, second)
}

// applyAhead acts on judgeAhead's verdict: a stop halts (saying why on the status; the identity holding at a
// height at or above T clears it, as after a forgery voided as forged); anything else is a round with no
// verdict, which alarms and never stops.
func (s *Signer) applyAhead(v aheadVerdict, why string) {
	if v != aheadStop {
		s.unverified(why)
		return
	}
	s.mu.Lock()
	s.cutNote = "no verdict: " + why
	s.aheadHalt = true
	s.mu.Unlock()
	log.Printf("signer: %s", why)
	s.halt("the client runs ahead of its chain (§5.6): " + why)
}

// secondHead is the first other gno source's head (gno.sources: rpc.gno.land in a deployment); ok false when
// none is configured or none answered.
func (s *Signer) secondHead(ctx context.Context) (uint64, bool) {
	for _, src := range s.sources {
		if head, err := src.Status(ctx); err == nil && head.Height > 0 && !head.CatchingUp {
			return uint64(head.Height), true
		}
	}
	return 0, false
}

// indexScan scans both providers for new dispatches toward gno.land from each one's own cursor (G7: the scan
// never re-reads what it has seen, halted or not; a restart begins both at the signing cursor), records every
// dispatch by id with the providers that showed it, keeps provider 1's unsigned ones in order, and retries the
// transactions a provider could not read from its side list (K5). A disputed dispatch (G17) is read again
// through both providers first, and agreed facts end the dispute. Each route's listing must reach the slot its
// supply was read at in snap (E4-SF3; nil: no such bound).
func (s *Signer) indexScan(ctx context.Context, snap *supplySnapshot) error {
	mailbox := sol.Mailbox{Program: s.Cfg.SolMailbox()}
	gnoDomain, err := s.gnoDomain(ctx)
	if err != nil {
		return err
	}
	both := twoProviders(s.sol)
	for id, rec := range s.index {
		if rec.disputed == "" {
			continue
		}
		r := s.Cfg.RouteBySymbol(rec.symbol)
		if r == nil {
			continue
		}
		d, err := agreedDispatch(ctx, both, mailbox, r.TokenKey(), s.Cfg.Solana.Domain, gnoDomain, rec.d.Sig, rec.d.ID)
		if err != nil {
			log.Printf("signer: dispatch %s stays disputed: %v", id, err)
			continue
		}
		rec.d, rec.disputed = d, ""
		_, rec.amount, rec.bad, _ = d.token()
		s.Alarm.Clear("solana:providers:" + id[:8])
		log.Printf("signer: dispatch %s: the providers agree again (slot %d)", id, d.Slot)
	}
	var firstErr error
	for _, r := range s.Cfg.Routes {
		if r.Token == "" {
			continue
		}
		for i, c := range s.sol {
			key := r.Symbol + "#" + strconv.Itoa(i)
			cursor, started := s.icur[key]
			if !started {
				cursor = s.restartCursor(r.Symbol, i)
			}
			var minSlot uint64
			if snap != nil {
				minSlot = snap.SlotOf(r.Symbol)
			}
			ds, next, skipped, err := solScan(ctx, c, mailbox, r.TokenKey(), s.Cfg.Solana.Domain, gnoDomain, cursor, minSlot, s.minCtx.honouring()[i])
			if g, ok := asHistoryGap(err); ok {
				s.gaps.set(key, g.why, s.now()) // its cursor kept: listed again next round
				continue
			}
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s from provider %d: %w", r.Symbol, i+1, err)
				}
				continue
			}
			s.gaps.end(key)
			s.icur[key] = next
			more, keep := retrySkipped(ctx, c, mailbox, r.TokenKey(), s.Cfg.Solana.Domain, gnoDomain, s.skipped[key], s.now())
			keep = append(keep, skipped...)
			s.skipped[key] = keep
			if len(keep) > 0 {
				s.Alarm.Alarm("scan:unreadable:"+key, fmt.Sprintf("%d transactions of %s on provider %d could not be read (skipped, retried every round): %s", len(keep), r.Symbol, i+1, skipText(keep)))
			} else {
				s.Alarm.Clear("scan:unreadable:" + key)
			}
			for _, d := range append(ds, more...) {
				s.record(d, r.Symbol, i)
			}
		}
	}
	s.gaps.alarm(s.Alarm, s.now())
	through := s.restartThrough()
	s.mu.Lock()
	s.state.SolThrough = through // written with the next save (the floor's, a signature's): an older value is only lower
	s.mu.Unlock()
	return firstErr
}

// restartCursor is where provider i's listing of a route begins after a start (E5-SF3: the index is rebuilt from the
// store, so every burn it held that the store does not is listed again): back to the slot the state names (SolThrough,
// E6-SF1), or, from a state written before it, after the signing cursor (the last signed dispatch's transaction).
func (s *Signer) restartCursor(symbol string, i int) scanCursor {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.state.SolThrough[symbol+"#"+strconv.Itoa(i)]; t > 0 {
		return scanCursor{Through: t}
	}
	return scanCursor{Sig: s.state.SolCursors[symbol+"#0"]}
}

// restartThrough is SolThrough as the index stands (E6-SF1): by "SYM#i", the slot provider i's listing of the route was
// read through, lowered below every burn of the route the index holds that is neither signed nor credited — whichever
// provider listed it (one this provider left out is listed again by it after a restart, as its next `until` would have:
// the review of lane s7, R2) — and below every transaction it could not read; none for a cursor read through no slot yet;
// the state's own for a route not listed since the start (its listing failed: what the state said still holds).
func (s *Signer) restartThrough() map[string]uint64 {
	out := map[string]uint64{}
	s.mu.Lock()
	for key, t := range s.state.SolThrough {
		if _, listed := s.icur[key]; !listed {
			out[key] = t
		}
	}
	s.mu.Unlock()
	for key, cur := range s.icur {
		sym, _, _ := strings.Cut(key, "#")
		if cur.Through == 0 {
			continue
		}
		t := cur.Through
		for id, rec := range s.index {
			if rec.symbol == sym && !s.signed[id] && rec.credited == 0 && rec.d.Slot <= t {
				t = rec.d.Slot - min(rec.d.Slot, 1)
			}
		}
		for _, sk := range s.skipped[key] {
			if sk.Slot <= t {
				t = sk.Slot - min(sk.Slot, 1)
			}
		}
		if t > 0 {
			out[key] = t
		}
	}
	return out
}

// record folds one scanned dispatch into the index. The second provider's sight of an indexed dispatch is
// compared with the first's (G17): a different slot, amount or recipient disputes it. A burn of nothing is neither
// indexed nor signed (E5-SF1): the relayer and the watch skip it already (§5.5: nothing to credit, nothing to wait
// for), and its signature, served publicly, let a stranger credit it himself — a credit no monitor's index holds,
// each one a window of no verdict on both monitors and hlwatch, and a claim read again by id on every poll for good —
// while it stayed in this index uncredited for good, its delivered mark read again every round (E5-SF5).
func (s *Signer) record(d solDispatch, symbol string, provider int) {
	id := d.IDHex()
	rec, ok := s.index[id]
	if !ok {
		to, amount, bad, parsed := d.token()
		if !parsed || amount == 0 {
			return // not a token message, or a burn of nothing: nothing to credit, nothing to sign for
		}
		_ = to
		rec = &signerDispatch{d: d, symbol: symbol, amount: amount, bad: bad}
		s.index[id] = rec
		if s.signed[id] {
			rec.providers = 3 // already in the store: both providers showed it once
		}
	} else if rec.providers&(1<<provider) == 0 && !s.signed[id] {
		if why := dispatchDiffers(rec.d, d); why != "" {
			rec.disputed = fmt.Sprintf("provider %d shows %s", provider+1, why)
		}
	}
	if provider == 0 && rec.providers&1 == 0 && !s.signed[id] {
		rec.d = d
		s.unsigned = append(s.unsigned, rec)
	}
	rec.providers |= 1 << provider
}

// signSol signs the unsigned dispatches in provider 1's order, each once both providers showed the same
// transaction, finalized, with the same message bytes in the mailbox's record, and stops at the first one only
// one provider shows (the rest wait with it: the signing cursor is the last signed). A dispatch finalized above
// the slot of the verdict's snapshot waits too (G13): the verdict never covered it, so it is signed one round
// later, after a cut that counted it as a burn in flight; a disputed one waits for the providers to agree. The
// checkpoint names the mailbox (Solana's tree hook) and the message's nonce, with a zero root: a Solana
// mailbox's tree is shared by every sender on it, and the gno mailbox checks only who signed, so the signers
// vouch for the message, not for a root they would each read at a different moment.
func (s *Signer) signSol(ctx context.Context) error {
	mailbox := sol.Mailbox{Program: s.Cfg.SolMailbox()}
	for len(s.unsigned) > 0 {
		rec := s.unsigned[0]
		if rec.providers != 3 {
			log.Printf("signer: solana #%d %x waits: only provider 1 shows it yet", rec.d.Nonce, rec.d.ID[:6])
			return nil
		}
		if rec.disputed != "" {
			log.Printf("signer: solana #%d %x waits: the providers disagree on it (%s)", rec.d.Nonce, rec.d.ID[:6], rec.disputed)
			return nil
		}
		if slot := s.verdictSlotNow(); rec.d.Slot > slot {
			log.Printf("signer: solana #%d %x waits: finalized at slot %d, above the verdict's snapshot slot %d (signed after the next held cut)", rec.d.Nonce, rec.d.ID[:6], rec.d.Slot, slot)
			return nil
		}
		if err := s.sign(rec.d, mailbox); err != nil {
			return err
		}
		s.unsigned = s.unsigned[1:]
		s.mu.Lock()
		s.state.SolCursors[rec.symbol+"#0"] = rec.d.Sig
		s.unsignedN = len(s.unsigned)
		err := s.save()
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if !s.signing() { // a nonce conflict halted us mid-batch
			return nil
		}
	}
	return nil
}

func (s *Signer) sign(d solDispatch, mailbox sol.Mailbox) error {
	hook := mailbox.Program
	cp := hl.Checkpoint{Hook: hook, Index: d.Nonce}
	raw := []byte(d.Raw)
	sig := hl.SignEd25519(s.Key.Ed, cp, raw)
	sg := Signed{Origin: d.Msg.Origin, Nonce: d.Nonce, ID: d.IDHex(), Message: hex.EncodeToString(raw), Slot: d.Slot,
		Hook: hex.EncodeToString(hook[:]), Root: hex.EncodeToString(cp.Root[:]), Signature: hex.EncodeToString(sig[:]),
		Signer: hex.EncodeToString(s.Key.EdPublic())}
	fresh, err := s.put("sol", sg)
	if err != nil {
		return err
	}
	s.signed[sg.ID] = true
	if fresh {
		log.Printf("signer: solana #%d %x signed (slot %d, tx %s)", d.Nonce, d.ID[:6], d.Slot, d.Sig)
	}
	return nil
}

func (s *Signer) gnoDomain(ctx context.Context) (uint32, error) {
	m, err := mailboxAt(ctx, s.gno, s.Cfg.Gno.Mailbox, 0)
	if err != nil {
		return 0, err
	}
	return m.Domain, nil
}

// put stores a signature, refusing to sign a second message under a nonce it already signed for: a signer that
// signs two messages for one place in the tree has equivocated. It stops signing and alerts (§5.6), and keeps
// serving what it signed. It reports whether the signature is new. A file that is not a signature is corruption,
// named as such, never a conflict.
func (s *Signer) put(kind string, sg Signed) (bool, error) {
	p := filepath.Join(s.Store, kind, strconv.FormatUint(uint64(sg.Nonce), 10)+".json")
	if b, err := os.ReadFile(p); err == nil {
		var old Signed
		if err := json.Unmarshal(b, &old); err != nil || old.ID == "" {
			return false, fmt.Errorf("signature file %s is %w: %v; restore it or delete it knowingly", p, errCorrupt, err)
		}
		if old.ID == sg.ID {
			return false, nil
		}
		why := fmt.Sprintf("%s nonce %d is signed for %s, now asked for %s", kind, sg.Nonce, old.ID, sg.ID)
		s.mu.Lock()
		s.nonceHalt = true
		s.mu.Unlock()
		s.halt("a nonce conflict: " + why)
		return false, errors.New("REFUSING: " + why)
	}
	return true, writeJSON(p, sg)
}

// readSigned is a stored signature by kind and nonce.
func (s *Signer) readSigned(kind string, nonce uint32) (*Signed, error) {
	b, err := os.ReadFile(filepath.Join(s.Store, kind, strconv.FormatUint(uint64(nonce), 10)+".json"))
	if err != nil {
		return nil, err
	}
	var sg Signed
	if err := json.Unmarshal(b, &sg); err != nil {
		return nil, err
	}
	return &sg, nil
}

// Handler serves the public signature endpoint, GET /v1/sol/<nonce>, and nothing else: the status is served on
// a loopback listener of its own (StatusHandler), so a flood at the public endpoint reaches no loop state.
func (s *Signer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sol/", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/v1/sol/"), 10, 32)
		if err != nil {
			http.Error(w, "bad nonce", http.StatusBadRequest)
			return
		}
		b, err := os.ReadFile(filepath.Join(s.Store, "sol", strconv.FormatUint(n, 10)+".json"))
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
		w.Header().Set("Content-Type", "application/json")
		b, err := json.Marshal(s.Status())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(Redact(string(b)))) // no provider URL's key in the status (G16)
	})
	return mux
}

// Status is what the status endpoint says: whether it signs, why not, and the identity it computed — built as
// copies under the mutex, so the encoder never walks a map the loop writes.
func (s *Signer) Status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := map[string]any{}
	for sym, id := range s.identity {
		v, text := id.Verdict()
		ids[sym] = map[string]any{"lock": id.Lock, "supply": id.Supply, "owed": id.Owed, "voided": id.Voided, "lost": id.Lost, "uncredited": id.InflightIn,
			"right": id.Right(), "gap": id.Gap(), "slot": id.Slot, "height": id.Height, "verdict": v.String(), "stops": id.SignerStops(), "text": text}
	}
	fresh := s.verified && s.now().Sub(s.lastVerdict) <= s.verdictMaxAge()
	// the providers' URLs are not published (G16: a paid provider's key rides in its URL); the index's counts are
	// the loop's published copies, never a walk of the index (G24: a range over a map the loop writes aborts)
	st := map[string]any{
		"ed25519": hex.EncodeToString(s.Key.EdPublic()),
		"signing": s.halted == "" && fresh, "halted": s.halted, "nonceConflict": s.nonceHalt, "aheadHalt": s.aheadHalt, "verified": s.verified, "verdictFresh": fresh,
		"cut": s.cutNote, "lastCheck": s.lastCheck.UTC().Format(time.RFC3339), "lastVerdict": s.lastVerdict.UTC().Format(time.RFC3339), "verdictSlot": s.verdictSlot,
		"identity": ids, "solCursors": copyMap(s.state.SolCursors), "floor": s.state.Floor, "indexed": s.indexedN, "unsigned": s.unsignedN, "disputed": s.disputedN,
		"node": s.Cfg.Gno.RPC, "sources": append([]string{}, s.Cfg.Gno.Sources...), "aheadAllowance": s.Cfg.AheadAllowance(),
	}
	return st
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

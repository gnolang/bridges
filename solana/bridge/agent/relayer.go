package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

const (
	// BackupOffset is how far behind the primary the backup acts (PLAN.md §5.7: 60 s apart).
	BackupOffset = 60 * time.Second
	// signerBodyLimit is the most a signer's answer may be (§5.7: a body limit of 1 MB).
	signerBodyLimit = 1 << 20
	// reemitEvery is how often the Reemit rule walks the backlog when no attempt is due (G8: a walk is a read per
	// undelivered message; every tick was O(backlog) against the node and the provider).
	reemitEvery = time.Minute
	// ProposalDeadline is p/decisions' Deadline: how old a builder-made proposal may be when it executes. Past
	// it the closure returns an error, and GovDAO denies the proposal for good only when someone calls
	// ExecuteOrRejectProposal (F2: ExecuteProposal re-panics the error, leaving the proposal active).
	ProposalDeadline = 14 * 24 * time.Hour
)

// Relayer carries messages, pays claims, applies queued decisions on gno.land and executes matured ones on the
// admin program (executor_sol.go), re-emits, and finishes builder-made proposals past their deadline (PLAN.md
// §5.7). It decides nothing about what is genuine: the chains check the signatures, the routes and that no
// message is delivered twice, so a relayer that lies only wastes its own gas — from a dedicated gas-only gno
// key, with gnokey's simulation before every send. A primary and a backup run 60 s apart: the backup takes an
// action only once it has been possible for a minute and nobody took it.
type Relayer struct {
	Cfg      *Config
	State    string // file
	Every    time.Duration
	Role     string // "primary" or "backup"
	Offset   time.Duration
	GnoGas   int64 // gas wanted for a gno call (45M at the chain's minimum price, §5.7)
	SolUnits uint32
	Alarm    *Alarmer

	gno    gnoCaller
	light  lightSource
	sol    *sol.Client
	http   *http.Client
	client clientView
	now    func() time.Time
	// solProcessed says whether our Solana mailbox processed a message id, finalized (a fake in tests).
	solProcessed func(ctx context.Context, id [32]byte) (bool, error)
	// solSend sends a transaction from the relayer's key and solData reads an account's data at confirmed (the
	// Solana client's in Run; fakes in tests); solRent is what a growth of a program's data costs at the chain's
	// rent rate (sol.ExtendRent, AP5).
	solSend func(ctx context.Context, ixs []solana.Instruction) (solana.Signature, error)
	solData func(ctx context.Context, k solana.PublicKey) ([]byte, error)
	solRent func(ctx context.Context, program solana.PublicKey, add uint32) (uint64, error)

	mu           sync.Mutex
	state        relayerState
	last         map[string]string // what happened last, by action, for the status endpoint
	leafs        map[string]leafInfo
	packetTooBig map[string]bool // nonce → its proof by header exceeds one packet (F6): a Reemit is asked
	nextReemits  time.Time
	status       statusBox
	// routeCache is each route realm's json as read this tick (AQ1: the migration a delivery follows is read once
	// a tick, not once a message) and mailboxCache hlmailbox's (AR6: the line's predecessor entries); admins the
	// admin program's line of succession read this tick (AQ6).
	routeCache   map[string]*routeJSON
	mailboxCache *mailboxJSON
	admins       *adminChain
	// lines are the routes' open migrations by symbol, read once a step (K34: a send through a successor realm
	// is ours, and a burn addressed to any realm of the line); lineErrs the symbols whose line could not be read
	// this step (G46: a send from anyone but the configured realm is held for the step, never "other").
	lines    map[string]*migration
	lineErrs map[string]error
}

// resetStep empties the per-step caches: a step reads each route and the mailbox once.
func (r *Relayer) resetStep() {
	r.routeCache = map[string]*routeJSON{}
	r.mailboxCache = nil
	r.lines = nil
	r.lineErrs = nil
}

// readLines reads every route's line of migrations once a step (K34): what classify takes as ours. A route
// whose line cannot be read is kept in lineErrs for the step, alarmed while it stands (G46): a route message
// from anyone but the configured realm is held at the walk until the line reads — before, the configured realm
// alone was ours and a successor's send at GnoNext was walked past as "other" for good by one failing render.
func (r *Relayer) readLines(ctx context.Context) {
	r.lines = map[string]*migration{}
	r.lineErrs = map[string]error{}
	for _, rt := range r.Cfg.Routes {
		key := "route:line-unread:" + rt.Symbol
		mig, err := r.migrationFor(ctx, rt)
		if err != nil {
			r.lineErrs[rt.Symbol] = err
			r.Alarm.Alarm(key, fmt.Sprintf("%s's line of migrations could not be read this tick (%v): a send from anyone but the configured realm %s is held at the walk, never walked past, and the enrolled router is not judged, until the line reads (G46)", rt.Symbol, err, rt.Gno))
			continue
		}
		r.Alarm.Clear(key)
		if mig != nil {
			r.lines[rt.Symbol] = mig
		}
	}
}

// lineUnread says whether a route's line of migrations was not read this step — not read at all, or read with
// an error (G46).
func (r *Relayer) lineUnread(rt *Route) bool {
	return r.lines == nil || r.lineErrs[rt.Symbol] != nil
}

// lineSender says whether a message's sender is a route realm of the token's line: the configured realm, or any
// realm of its migrations read this step (K34: from the kind 6 that enrolls a successor as the token's router,
// every TransferRemote through the successor dispatches with the successor as sender — before, such a send was
// "other", walked past for good, locked on gno.land with nothing minted and no refund).
func (r *Relayer) lineSender(rt *Route, sender [32]byte) bool {
	addrOf := func(pkg string) [32]byte {
		a, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(pkg))
		return a
	}
	if addrOf(rt.Gno) == sender {
		return true
	}
	if mig := r.lines[rt.Symbol]; mig != nil {
		for _, p := range mig.Paths() {
			if addrOf(p) == sender {
				return true
			}
		}
	}
	return false
}

type relayerState struct {
	GnoNext        uint32                       `json:"gnoNext"`
	SolCursor      map[string]string            `json:"solCursor"` // by route symbol
	SolSkipped     map[string][]scanSkip        `json:"solSkipped"`
	Pending        []solDispatch                `json:"pending"`  // from Solana, not yet delivered on gno.land
	Sigs           map[string]map[string]string `json:"sigs"`     // message id → signer key → signature (cached on disk)
	Attempts       map[string]*attempt          `json:"attempts"` // by action key
	Ready          map[string]time.Time         `json:"ready"`    // when an action first became possible (the backup's clock)
	Claims         map[string]*claimState       `json:"claims"`   // by route symbol
	Reemits        map[string]int64             `json:"reemits"`  // nonce → height of the last Reemit sent
	Done           map[string]string            `json:"done"`     // gno nonce → how it ended on Solana: "delivered", "recorded", "taken", "other"
	Taken          map[string]bool              `json:"taken"`    // decision numbers reported as taken by another decision
	TakenSeen      map[string]int64             `json:"takenSeen"`
	DecisionCopies map[string][]decisionCopy    `json:"decisionCopies"` // decision number → every copy r/hlgov dispatched (across passes)
	Refused        map[string]string            `json:"refused"`        // Execute refusals on the admin program never retried (LayoutFixed), by decision
	ProposalsSeen  map[string]int64             `json:"proposalsSeen"`  // GovDAO proposal id → when this relayer first saw it open
	Extends        map[string]string            `json:"extends"`        // extend action key → the bytes planned (the cost alarmed once, G18)
}

// decisionCopy is one hlmailbox message carrying a decision number.
type decisionCopy struct {
	Nonce uint32 `json:"nonce"`
	ID    string `json:"id"`
}

func (r *Relayer) load() error {
	if err := readJSON(r.State, &r.state); err != nil {
		return err
	}
	s := &r.state
	if s.SolCursor == nil {
		s.SolCursor = map[string]string{}
	}
	if s.SolSkipped == nil {
		s.SolSkipped = map[string][]scanSkip{}
	}
	if s.Sigs == nil {
		s.Sigs = map[string]map[string]string{}
	}
	if s.Attempts == nil {
		s.Attempts = map[string]*attempt{}
	}
	if s.Ready == nil {
		s.Ready = map[string]time.Time{}
	}
	if s.Claims == nil {
		s.Claims = map[string]*claimState{}
	}
	if s.Reemits == nil {
		s.Reemits = map[string]int64{}
	}
	if s.Done == nil {
		s.Done = map[string]string{}
	}
	if s.Taken == nil {
		s.Taken = map[string]bool{}
	}
	if s.TakenSeen == nil {
		s.TakenSeen = map[string]int64{}
	}
	if s.DecisionCopies == nil {
		s.DecisionCopies = map[string][]decisionCopy{}
	}
	if s.Refused == nil {
		s.Refused = map[string]string{}
	}
	if s.ProposalsSeen == nil {
		s.ProposalsSeen = map[string]int64{}
	}
	if s.Extends == nil {
		s.Extends = map[string]string{}
	}
	for i := range s.Pending {
		if err := s.Pending[i].fill(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Relayer) save() error {
	if r.State == "" {
		return nil
	}
	return writeJSON(r.State, r.state)
}

func (r *Relayer) init() error {
	if r.now == nil {
		r.now = time.Now
	}
	if r.Offset == 0 {
		r.Offset = BackupOffset
	}
	if r.Role == "" {
		r.Role = "primary"
	}
	if r.Role != "primary" && r.Role != "backup" {
		return errors.New("relayer: the role is primary or backup")
	}
	if r.GnoGas == 0 {
		r.GnoGas = 45_000_000
	}
	if r.SolUnits == 0 {
		r.SolUnits = 400_000
	}
	r.last = map[string]string{}
	r.leafs = map[string]leafInfo{}
	r.packetTooBig = map[string]bool{}
	return r.load()
}

// Run relays until ctx ends.
func (r *Relayer) Run(ctx context.Context) error {
	if err := r.init(); err != nil {
		return err
	}
	if r.gno == nil {
		r.gno = r.Cfg.GnoClient()
	}
	if len(r.Cfg.Solana.RPCs) == 0 || r.Cfg.Solana.Payer == "" {
		return errors.New("relayer: solana.rpcs and solana.payer are needed")
	}
	payer, err := sol.PrivateKeyFromFile(r.Cfg.Solana.Payer)
	if err != nil {
		return err
	}
	r.sol = sol.NewClient(r.Cfg.Solana.RPCs[0], payer)
	if r.solSend == nil {
		r.solSend = func(ctx context.Context, ixs []solana.Instruction) (solana.Signature, error) {
			return r.sol.Send(ctx, ixs)
		}
	}
	if r.solData == nil {
		r.solData = func(ctx context.Context, k solana.PublicKey) ([]byte, error) { return accountData(ctx, r.sol.RPC, k) }
	}
	if r.solRent == nil {
		r.solRent = func(ctx context.Context, program solana.PublicKey, add uint32) (uint64, error) {
			return sol.ExtendRent(ctx, r.sol.RPC, program, add)
		}
	}
	r.http = &http.Client{Timeout: 10 * time.Second}
	if r.light == nil {
		r.light = r.Cfg.SourceClient(r.Cfg.Gno.RPC)
	}
	if r.client == nil {
		if r.Cfg.HasPrograms() {
			r.client = &solClientView{r: r}
		} else {
			log.Printf("relayer: WARNING no light client in the config (solana.client/pausable/admin): nothing is delivered to Solana and nothing is re-emitted")
			r.client = noClient{}
		}
	}
	if r.solProcessed == nil {
		mailbox := sol.Mailbox{Program: r.Cfg.SolMailbox()}
		r.solProcessed = func(ctx context.Context, id [32]byte) (bool, error) { return solDelivered(ctx, r.sol.RPC, mailbox, id) }
	}
	log.Printf("relayer (%s): solana payer %s, gno key %s, gas %d, light client %s", r.Role, payer.PublicKey(), r.Cfg.Gno.Key, r.GnoGas, r.Cfg.Solana.Client)
	for {
		r.tick(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.Every):
		}
	}
}

func (r *Relayer) tick(ctx context.Context) {
	r.resetStep()
	if err := r.solToGno(ctx); err != nil {
		log.Printf("relayer: solana → gno: %v", err)
	}
	if err := r.gnoToSol(ctx); err != nil && !errors.Is(err, errNotYet) {
		log.Printf("relayer: gno → solana: %v", err)
	}
	if err := r.claims(ctx); err != nil {
		log.Printf("relayer: claims: %v", err)
	}
	if err := r.executes(ctx); err != nil {
		log.Printf("relayer: executes: %v", err)
	}
	if err := r.finishProposals(ctx); err != nil {
		log.Printf("relayer: proposals: %v", err)
	}
	if err := r.solExecutes(ctx); err != nil {
		log.Printf("relayer: executes on the admin program: %v", err)
	}
	if r.reemitsDue() {
		if err := r.reemits(ctx); err != nil {
			log.Printf("relayer: reemits: %v", err)
		}
	}
	r.pruneDone()
	if err := r.save(); err != nil {
		log.Printf("relayer: state: %v", err)
	}
	r.status.publish(r.buildStatus())
}

// reemitsDue says whether the Reemit rule walks the backlog this tick: once a minute, or when a Reemit attempt's
// retry is due.
func (r *Relayer) reemitsDue() bool {
	now := r.now()
	if !now.Before(r.nextReemits) {
		r.nextReemits = now.Add(reemitEvery)
		return true
	}
	for key, a := range r.state.Attempts {
		if strings.HasPrefix(key, "reemit:") && a.due(now) {
			return true
		}
	}
	return false
}

// pruneDone keeps Done to the entries the walk still consults: the nonces at or past GnoNext (G8: the file
// grew by every nonce ever and was rewritten every tick).
func (r *Relayer) pruneDone() {
	for nk := range r.state.Done {
		n, err := strconv.ParseUint(nk, 10, 32)
		if err != nil || uint32(n) < r.state.GnoNext {
			delete(r.state.Done, nk)
		}
	}
}

// ---------------------------------------------------------------- roles --

// act says whether this relayer takes an action now: the primary at once, the backup once the action has been
// possible for Offset. The key is forgotten with forget when the action is no longer possible.
func (r *Relayer) act(key string) bool {
	now := r.now()
	first, ok := r.state.Ready[key]
	if !ok {
		r.state.Ready[key] = now
		first = now
	}
	if r.Role == "primary" {
		return true
	}
	return now.Sub(first) >= r.Offset
}

// forget drops an action's clock and attempts, and the parked alarm it may have raised (G12).
func (r *Relayer) forget(key string) {
	delete(r.state.Ready, key)
	if r.state.Attempts[key].parked() {
		r.Alarm.Clear("parked:" + key)
	}
	delete(r.state.Attempts, key)
}

// tryCall makes one gno call under an action key with the attempt bookkeeping of §5.7.
func (r *Relayer) tryCall(ctx context.Context, key, pkg, fn string, gas int64, args ...string) (*gno.Result, error) {
	a := r.state.Attempts[key]
	if !a.due(r.now()) {
		return nil, errNotYet
	}
	res, err := r.gno.Call(ctx, pkg, fn, gas, args...)
	if err != nil {
		if a == nil {
			a = &attempt{}
			r.state.Attempts[key] = a
		}
		if parkedNow := a.fail(r.now(), err, gno.Deterministic(err)); parkedNow || a.parked() {
			r.Alarm.Alarm("parked:"+key, fmt.Sprintf("%s parked after %d failures (retried hourly): %v", key, a.Failures, err))
		}
		r.note(key, "failed: "+err.Error())
		return nil, err
	}
	if a.parked() {
		r.Alarm.Clear("parked:" + key)
	}
	delete(r.state.Attempts, key)
	return res, nil
}

func (r *Relayer) note(key, what string) {
	r.mu.Lock()
	r.last[key] = r.now().UTC().Format(time.RFC3339) + " " + what
	r.mu.Unlock()
}

// -------------------------------------------------------- solana → gno --

// solToGno picks up each route's new dispatches toward gno.land (a transaction the provider cannot read is
// skipped with an alarm and retried from a side list, K5; an amount-0 burn is skipped, §5.5) and delivers every
// pending one that has the signatures hlmailbox's rule needs, through the route's Process.
func (r *Relayer) solToGno(ctx context.Context) error {
	r.resetStep()
	m, err := mailboxAt(ctx, r.gno, r.Cfg.Gno.Mailbox, 0)
	if err != nil {
		return err
	}
	rule, err := m.rule()
	if err != nil {
		return err
	}
	mailbox := sol.Mailbox{Program: r.Cfg.SolMailbox()}
	var scanErr error
	for _, rt := range r.Cfg.Routes {
		if rt.Token == "" {
			continue
		}
		ds, next, skipped, err := solScan(ctx, r.sol.RPC, mailbox, rt.TokenKey(), r.Cfg.Solana.Domain, m.Domain, r.state.SolCursor[rt.Symbol])
		if err != nil {
			scanErr = err
			continue
		}
		more, keep := retrySkipped(ctx, r.sol.RPC, mailbox, rt.TokenKey(), r.Cfg.Solana.Domain, m.Domain, r.state.SolSkipped[rt.Symbol], r.now())
		keep = append(keep, skipped...)
		r.state.SolSkipped[rt.Symbol] = keep
		if len(keep) > 0 {
			r.Alarm.Alarm("scan:unreadable:"+rt.Symbol, fmt.Sprintf("%d transactions of %s could not be read (skipped, retried every tick): %s", len(keep), rt.Symbol, skipText(keep)))
		} else {
			r.Alarm.Clear("scan:unreadable:" + rt.Symbol)
		}
		for _, d := range append(ds, more...) {
			if _, amount, _, ok := d.token(); ok && amount == 0 {
				log.Printf("relayer: solana #%d %x carries amount 0: skipped (§5.5)", d.Nonce, d.ID[:6])
				continue
			}
			if !r.pending(d.ID) {
				r.state.Pending = append(r.state.Pending, d)
			}
		}
		r.state.SolCursor[rt.Symbol] = next
	}
	var keep []solDispatch
	for _, d := range r.state.Pending {
		key := "process:" + d.IDHex()
		rt, target, wait, err := r.deliveryTarget(ctx, d)
		if err != nil {
			keep = append(keep, d)
			r.note(key, "the route's state could not be read: "+err.Error())
			continue
		}
		if rt == nil {
			continue // not for a route of ours: nobody here takes it
		}
		at, err := deliveredAt(ctx, r.gno, r.Cfg.Gno.Mailbox, d.IDHex(), 0)
		if err != nil {
			keep = append(keep, d)
			continue
		}
		if at != 0 {
			r.forget(key)
			delete(r.state.Sigs, d.IDHex())
			continue
		}
		keep = append(keep, d)
		if wait != "" {
			// a burn addressed to a migrated route waits for its successor's last page (AQ1): never attempted,
			// never parked, the status naming why; the backup's clock restarts when it becomes deliverable
			delete(r.state.Ready, key)
			r.note(key, "waits: "+wait)
			continue
		}
		cp := hl.Checkpoint{Hook: mailbox.Program, Index: d.Nonce}
		sigs, err := r.gather(ctx, rule, cp, d)
		if err != nil {
			delete(r.state.Ready, key)
			r.note(key, "waiting for signatures: "+err.Error())
			continue
		}
		if !r.act(key) {
			continue
		}
		meta := hl.GnoMetadata(cp, sigs)
		res, err := r.tryCall(ctx, key, target, "Process", r.GnoGas, hex.EncodeToString(meta), hex.EncodeToString(d.Raw))
		if err != nil {
			if !errors.Is(err, errNotYet) {
				log.Printf("relayer: solana #%d: %v", d.Nonce, err)
			}
			continue
		}
		r.note(key, "delivered: "+res.Hash)
		log.Printf("relayer: solana #%d %x delivered on gno (%s, through %s): %s", d.Nonce, d.ID[:6], rt.Symbol, target, res.Hash)
	}
	r.state.Pending = keep
	return scanErr
}

// routeState is a route realm's json at the head, read once a tick.
func (r *Relayer) routeState(ctx context.Context, pkg string) (*routeJSON, error) {
	if r.routeCache == nil {
		r.routeCache = map[string]*routeJSON{}
	}
	if rj, ok := r.routeCache[pkg]; ok {
		return rj, nil
	}
	rj, err := routeAt(ctx, r.gno, pkg, 0)
	if err != nil {
		return nil, err
	}
	r.routeCache[pkg] = rj
	return rj, nil
}

// mailboxState is hlmailbox's json at the head, read once a step (AR6: a line's predecessor entries).
func (r *Relayer) mailboxState(ctx context.Context) (*mailboxJSON, error) {
	if r.mailboxCache != nil {
		return r.mailboxCache, nil
	}
	mb, err := mailboxAt(ctx, r.gno, r.Cfg.Gno.Mailbox, 0)
	if err != nil {
		return nil, err
	}
	r.mailboxCache = mb
	return mb, nil
}

// migrationFor is a configured route's open migration at the head, nil for none (read once a tick): the line
// of `migratedTo` followed to its end (AR6, G41), and back to its first realm by the mailbox's entries.
func (r *Relayer) migrationFor(ctx context.Context, rt Route) (*migration, error) {
	rj, err := r.routeState(ctx, rt.Gno)
	if err != nil {
		return nil, err
	}
	mb, err := r.mailboxState(ctx)
	if err != nil {
		return nil, err
	}
	return migrationOf(ctx, r.routeState, rt, rj, mb)
}

// deliveryTarget is the realm whose Process takes a pending dispatch, and whether it must wait first (AQ1): a
// burn names the token's enrolled router as its recipient — the configured route, or, once kind 6 enrolled a
// successor, that one — and the realm that credits it is the route itself while it is live, the line's last
// successor once a migrate applied and that successor's import has ended (hlmailbox lets a successor take a
// message addressed to any realm before it in the line, AR6; before, the message waits — a migrated realm
// refuses every function and the successor refuses credits while pages arrive). rt is nil for a message that
// is nobody's here.
func (r *Relayer) deliveryTarget(ctx context.Context, d solDispatch) (rt *Route, target, wait string, err error) {
	rt = r.Cfg.RouteByToken(solana.PublicKeyFromBytes(d.Msg.Sender[:]))
	if rt == nil {
		return nil, "", "", nil
	}
	addrOf := func(pkg string) [32]byte {
		a, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(pkg))
		return a
	}
	mig, err := r.migrationFor(ctx, *rt)
	if err != nil {
		return rt, "", "", err
	}
	if mig == nil {
		if d.Msg.Recipient != addrOf(rt.Gno) {
			return nil, "", "", nil
		}
		return rt, rt.Gno, "", nil
	}
	mb, err := r.mailboxState(ctx)
	if err != nil {
		return rt, "", "", err
	}
	ours := func(path string) bool {
		if d.Msg.Recipient == addrOf(path) {
			return true
		}
		// a successor this hop's re-point dropped (M1i, R35): a route no more, a tombstone the line's last realm
		// takes the messages of, as it takes a predecessor's
		for _, dropped := range mb.Routes[path].Dropped {
			if d.Msg.Recipient == addrOf(dropped) {
				return true
			}
		}
		return false
	}
	for _, path := range mig.Paths() {
		if !ours(path) {
			continue
		}
		// addressed to the live successor itself (kind 6 enrolled it), to a migrated realm of the line or to a
		// dropped successor of one: the successor takes it once its import has ended — never before its first
		// page (AR3: before page 0 the lock is not there, so the realm would sweep the credit as one above the
		// lock; between pages it refuses every credit, AP4), never while pages arrive
		if !mig.Ended() {
			return rt, mig.Successor, mig.WaitText(), nil
		}
		return rt, mig.Successor, "", nil
	}
	return nil, "", "", nil
}

func (r *Relayer) pending(id [32]byte) bool {
	for _, p := range r.state.Pending {
		if p.ID == id {
			return true
		}
	}
	return false
}

// gather fetches the signers' signatures for a message (cached on disk once seen), checks each against the key
// hlmailbox holds for that signer, and picks exactly the ones the rule needs.
func (r *Relayer) gather(ctx context.Context, rule Rule, cp hl.Checkpoint, d solDispatch) ([]hl.EdSignature, error) {
	id := d.IDHex()
	cache := r.state.Sigs[id]
	if cache == nil {
		cache = map[string]string{}
		r.state.Sigs[id] = cache
	}
	have := map[int][64]byte{}
	known := 0
	for _, ref := range r.Cfg.Signers {
		key, err := ref.EdKey()
		if err != nil {
			continue
		}
		i := rule.Index(key)
		if i < 0 {
			continue // not in the chain's set: its signature would not count
		}
		known++
		sigHex, ok := cache[ref.Key]
		if !ok {
			s, err := r.fetch(ctx, ref.URL, "sol", d.Nonce)
			if err != nil || s.ID != id {
				continue
			}
			sigHex = s.Signature
		}
		b, err := hex.DecodeString(sigHex)
		if err != nil || !verifyOne(key, d.Msg.Origin, cp, d.ID, b) {
			log.Printf("relayer: solana #%d: signer %d's signature is not its own", d.Nonce, i)
			delete(cache, ref.Key)
			continue
		}
		cache[ref.Key] = sigHex
		var sig [64]byte
		copy(sig[:], b)
		have[i] = sig
	}
	if known == 0 {
		return nil, errNoSigners
	}
	return pickSignatures(rule, have)
}

// fetch asks a signer for one signature, over HTTPS (or a loopback address), reading at most 1 MB.
func (r *Relayer) fetch(ctx context.Context, base, kind string, nonce uint32) (*Signed, error) {
	if err := checkSignerURL(base, r.Cfg.AllowHTTP); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/v1/"+kind+"/"+strconv.FormatUint(uint64(nonce), 10), nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", base, resp.Status)
	}
	var s Signed
	if err := json.NewDecoder(io.LimitReader(resp.Body, signerBodyLimit)).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

// -------------------------------------------------------- gno → solana --

// gnoToSol delivers hlmailbox's messages for the routes and the admin program on Solana: a `process` under the
// pausable ISM and the light-client ISM with a staged proof (deliverUnderLightClient, relayer_sol.go).
func (r *Relayer) gnoToSol(ctx context.Context) error {
	if !r.Cfg.HasPrograms() {
		return nil
	}
	return r.deliverUnderLightClient(ctx)
}

// --------------------------------------------------------------- claims --

// claims runs Claim(20) on every route while a claim waits and the meter holds something (§5.4: the relayers
// call it continuously), backing off while a call pays nothing.
func (r *Relayer) claims(ctx context.Context) error {
	r.resetStep()
	var firstErr error
	for _, rt := range r.Cfg.Routes {
		rj, err := r.routeState(ctx, rt.Gno)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		key := "claim:" + rt.Symbol
		pkg := rt.Gno
		// a migrated route pays nothing: its successor does, once the import has ended (AQ1: the relayer follows
		// the migration it reads on the chain, the config naming the starting point)
		if mig, err := r.migrationFor(ctx, rt); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		} else if mig != nil {
			if !mig.Ended() {
				delete(r.state.Ready, key)
				r.note(key, "idle: "+mig.WaitText())
				continue
			}
			pkg, rj = mig.Successor, mig.Succ
		}
		cs := r.state.Claims[rt.Symbol]
		if cs == nil {
			cs = &claimState{}
			r.state.Claims[rt.Symbol] = cs
		}
		if rj.PaidTotal != cs.PaidTotal { // someone paid: the backup's clock restarts
			cs.PaidTotal = rj.PaidTotal
			delete(r.state.Ready, key)
		}
		want, why := wantClaim(rj)
		if !want {
			delete(r.state.Ready, key)
			r.note(key, "idle: "+why)
			continue
		}
		if !r.act(key) || r.now().Before(cs.Next) {
			continue
		}
		res, err := r.tryCall(ctx, key, pkg, "Claim", r.GnoGas, "20")
		if err != nil {
			if !errors.Is(err, errNotYet) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		paid := 0
		if v, ok := res.Attr("Claimed", "count"); ok {
			paid, _ = strconv.Atoi(v)
		}
		cs.after(r.now(), paid)
		r.note(key, fmt.Sprintf("paid %d claims: %s", paid, res.Hash))
		log.Printf("relayer: %s Claim(20) paid %d claims (%s)", rt.Symbol, paid, res.Hash)
	}
	return firstErr
}

// ------------------------------------------------------------- executes --

// realms are every realm with a decisions page: the mailbox, the routes (every realm of a migrated route's
// line too, AQ1, AR6: their own queued decisions are applied by the relayers like any realm's), the fee realm,
// r/hlgov.
func (r *Relayer) realms(ctx context.Context) []string {
	out := []string{r.Cfg.Gno.Mailbox}
	seen := map[string]bool{}
	for _, rt := range r.Cfg.Routes {
		paths := []string{rt.Gno}
		if mig, err := r.migrationFor(ctx, rt); err == nil && mig != nil {
			paths = mig.Paths()
		}
		for _, p := range paths {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	if r.Cfg.Gno.Fees != "" {
		out = append(out, r.Cfg.Gno.Fees)
	}
	if r.Cfg.Gno.Gov != "" {
		out = append(out, r.Cfg.Gno.Gov)
	}
	return out
}

// executes calls Execute(id) on every queued decision whose delay has passed by block time (§5.3: anyone
// applies them; the relayers do) — except a refund, which is GovDAO's own at-once apply (AT2: the realm's
// Execute refuses the kind, and the apply-refund builder applies it once the member has read hlwatch's gate —
// the token's enrolled router on Solana, which no realm can see): the relayer never calls it, logs once that
// it awaits GovDAO's apply, and keeps that in its status — never an error, never a park.
func (r *Relayer) executes(ctx context.Context) error {
	r.resetStep()
	head, err := r.gno.Status(ctx)
	if err != nil {
		return err
	}
	r.readLines(ctx)
	var firstErr error
	for _, realm := range r.realms(ctx) {
		ds, err := decisionsAt(ctx, r.gno, realm, 0)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, d := range dueDecisions(ds, head.Time.Unix()) {
			key := "execute:" + realm + "#" + strconv.FormatInt(d.ID, 10)
			if d.Kind == "refund" {
				r.awaitsApply(key, realm, d.ID)
				continue
			}
			if !r.act(key) {
				continue
			}
			res, err := r.tryCall(ctx, key, realm, "Execute", r.GnoGas, strconv.FormatInt(d.ID, 10))
			if err != nil {
				if !errors.Is(err, errNotYet) && firstErr == nil {
					firstErr = err
				}
				continue
			}
			r.forget(key)
			r.note(key, "applied: "+res.Hash)
			log.Printf("relayer: %s decision #%d (%s) applied: %s", realm, d.ID, d.Kind, res.Hash)
		}
	}
	return firstErr
}

// awaitsApply is a matured refund decision the relayer leaves to GovDAO (AT2): the realm's Execute refuses the
// kind — a refund applied while the predecessor is still the token's router on Solana lets a refunded sender
// deliver his send through it with the published tool, minted twice, and no realm can read that router — so
// the apply is GovDAO's own at-once proposal, made once hlwatch's gate shows the successor enrolled. The line
// is logged once per decision and kept in the status; nothing is attempted, so nothing can park.
func (r *Relayer) awaitsApply(key, realm string, id int64) {
	what := "awaiting GovDAO's apply (an apply-refund proposal, at once; Execute refuses a refund and the relayer never calls it — read hlwatch's gate first, AT2)"
	if !r.noted(key) {
		log.Printf("relayer: %s decision #%d (refund) is mature and %s", realm, id, what)
	}
	r.note(key, what)
}

// gate keeps a standing line in the status under a key and logs it once each time its words change (the
// finisher's wait for the yield, M1k).
func (r *Relayer) gate(key, what string) {
	r.mu.Lock()
	same := strings.HasSuffix(r.last[key], " "+what)
	r.mu.Unlock()
	if !same {
		log.Printf("relayer: %s: %s", key, what)
	}
	r.note(key, what)
}

// noted says whether an action key has a status line already.
func (r *Relayer) noted(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.last[key]
	return ok
}

// finishProposals calls r/gov/dao's ExecuteOrRejectProposal on every builder-made proposal that has the votes and
// is past its deadline (F2) — the realms' 14 days divided by their compiled clock factor, read from hlmailbox's
// clocks: the builder's closure returns the deadline error, which ExecuteProposal would re-panic and leave the
// proposal active for ever; ExecuteOrRejectProposal denies it for good. A lapsed Launch is one of them. The
// proposal's age is counted from when this relayer first saw it open (its creation time is not rendered), so the
// call never comes early: an older proposal is already past its deadline by the realm's own clock, and a proposal
// within it is left to its members. Only after the yield (M1k): until the governor yields on hlmailbox a
// builder's closure refuses GovDAO's proxy with a panic — which denies nothing and leaves the proposal pending
// for a retry after the yield — so the finisher reads `yielded` from the mailbox json once a tick and, while it
// is 0 with a governor compiled, neither reads r/gov/dao nor calls it; the proposals are first seen at the yield,
// so their days count from there.
func (r *Relayer) finishProposals(ctx context.Context) error {
	if r.Cfg.Gno.DAO == "" {
		return nil
	}
	mb, err := mailboxAt(ctx, r.gno, r.Cfg.Gno.Mailbox, 0)
	if err != nil {
		return err
	}
	if !mb.DAOGoverns() {
		r.gate("proposals", "awaits the yield: until the governor "+mb.Governor+" yields on hlmailbox a builder-made proposal is refused by its closure, and nothing of this relayer reads or calls "+r.Cfg.Gno.DAO)
		return nil
	}
	if mb.Governor == "" {
		r.gate("proposals", r.Cfg.Gno.DAO+" governs from the first block (no governor was compiled): a builder-made proposal with the votes and past its deadline is finished with ExecuteOrRejectProposal")
	} else {
		r.gate("proposals", "since the yield at height "+strconv.FormatInt(mb.Yielded, 10)+" ("+mb.Governor+" yielded): a builder-made proposal with the votes and past its deadline is finished with ExecuteOrRejectProposal")
	}
	head, err := r.gno.Status(ctx)
	if err != nil {
		return err
	}
	deadline := mb.Clocks.DeadlineDuration()
	// every realm of each token's line (G48: a lapsed proposal on a successor the config does not name)
	props, whyNot := ReadProposals(ctx, r.gno, r.Cfg, head.Height, DefaultSupermajority, r.realms(ctx)...)
	if whyNot != "" {
		return errors.New(whyNot)
	}
	open := map[string]bool{}
	now := r.now()
	var firstErr error
	for _, p := range props {
		pk := strconv.FormatInt(p.ID, 10)
		open[pk] = true
		first, seen := r.state.ProposalsSeen[pk]
		if !seen {
			r.state.ProposalsSeen[pk] = now.Unix()
			continue
		}
		key := "finish:" + pk
		if !p.Armed || now.Sub(time.Unix(first, 0)) < deadline+time.Minute {
			delete(r.state.Ready, key)
			continue
		}
		if !r.act(key) {
			continue
		}
		res, err := r.tryCall(ctx, key, r.Cfg.Gno.DAO, "ExecuteOrRejectProposal", r.GnoGas, pk)
		if err != nil {
			if !errors.Is(err, errNotYet) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		r.forget(key)
		r.note(key, "finished (executed, or denied past its deadline): "+res.Hash)
		log.Printf("relayer: GovDAO proposal #%d (%s) past its %s: ExecuteOrRejectProposal sent: %s", p.ID, p.Title, ProposalDeadline, res.Hash)
	}
	for pk := range r.state.ProposalsSeen {
		if !open[pk] {
			delete(r.state.ProposalsSeen, pk)
			r.forget("finish:" + pk)
		}
	}
	return firstErr
}

// -------------------------------------------------------------- reemits --

// clientView is what a Reemit decision asks of the light client on Solana (§5.1): the current validator set's
// first height, and a message's results leaf at a height — its size, and whether the block's leaves can be
// rebuilt at all (a failed transaction or an event with no schema beside the dispatch makes a proof impossible
// from that block). The relayer reads them from the client's state and the node's block results.
type clientView interface {
	CurrentSetFrom(ctx context.Context) (int64, error)
	LeafBytes(ctx context.Context, height int64, id [32]byte) (bytes int, buildable bool, err error)
}

// noClient is the view without a light client in the config: nothing is known and no message needs a Reemit.
type noClient struct{}

func (noClient) CurrentSetFrom(context.Context) (int64, error) { return 0, nil }
func (noClient) LeafBytes(context.Context, int64, [32]byte) (int, bool, error) {
	return 0, true, nil
}

// reemits calls Reemit(nonce) for every undelivered message of ours — a route's or a decision's (G11) — that
// needs a fresh Dispatch event. A message whose reads fail is skipped, never the pass (G8).
func (r *Relayer) reemits(ctx context.Context) error {
	setFrom, err := r.client.CurrentSetFrom(ctx)
	if err != nil {
		return err
	}
	m, err := mailboxAt(ctx, r.gno, r.Cfg.Gno.Mailbox, 0)
	if err != nil {
		return err
	}
	r.readLines(ctx) // the routes' lines, for classify (K34)
	var firstErr error
	fail := func(n uint32, err error) {
		if firstErr == nil {
			firstErr = fmt.Errorf("gno #%d: %w", n, err)
		}
	}
	for n := r.state.GnoNext; n < m.Count; n++ {
		nk := strconv.FormatUint(uint64(n), 10)
		if doneForReemit(r.state.Done[nk]) {
			continue
		}
		msg, err := gnoMessage(ctx, r.gno, r.Cfg.Gno.Mailbox, m.Domain, n)
		if err != nil {
			fail(n, err)
			continue
		}
		if kind, _ := r.classify(msg); kind != kindRoute && kind != kindDecision {
			continue // not ours, or a decision to a program the line does not reach (G31): a re-emit helps neither
		}
		delivered := false
		if r.solProcessed != nil {
			if delivered, err = r.solProcessed(ctx, msg.ID); err != nil {
				fail(n, err)
				continue
			}
		}
		eventHeight := msg.Height
		if h := r.state.Reemits[nk]; h > eventHeight {
			eventHeight = h
		}
		leaf, buildable, err := r.client.LeafBytes(ctx, eventHeight, msg.ID)
		if err != nil {
			fail(n, err)
			continue
		}
		need, why := needsReemit(reemitInput{Nonce: n, Delivered: delivered, DispatchHeight: msg.Height, CurrentSetFrom: setFrom, LeafBytes: leaf, LeafUnbuildable: !buildable, PacketTooBig: r.packetTooBig[nk], LastReemitHeight: r.state.Reemits[nk]})
		if !need {
			continue
		}
		key := "reemit:" + nk
		if !r.act(key) {
			continue
		}
		res, err := r.tryCall(ctx, key, r.Cfg.Gno.Mailbox, "Reemit", r.GnoGas, nk)
		if err != nil {
			if !errors.Is(err, errNotYet) {
				fail(n, err)
			}
			continue
		}
		r.state.Reemits[nk] = res.Height
		delete(r.packetTooBig, nk)
		r.forget(key)
		r.note(key, "re-emitted at "+strconv.FormatInt(res.Height, 10)+": "+why)
		log.Printf("relayer: gno #%d re-emitted at height %d (%s): %s", n, res.Height, why, res.Hash)
	}
	return firstErr
}

// ---------------------------------------------------------------- status --

// Handler serves GET /v1/status from the last published snapshot.
func (r *Relayer) Handler() http.Handler { return r.status.handler() }

// Status is the relayer's last published snapshot (what tests read; the loop publishes one at the end of each
// tick).
func (r *Relayer) Status() map[string]any { return r.status.get() }

// buildStatus is the relayer's state for its status endpoint, built by the loop itself (copies of what it
// publishes, so the encoder never walks a live map).
func (r *Relayer) buildStatus() map[string]any {
	var pending []string
	for _, d := range r.state.Pending {
		pending = append(pending, fmt.Sprintf("#%d %s", d.Nonce, d.IDHex()))
	}
	parked := map[string]any{}
	attempts := map[string]any{}
	for k, a := range r.state.Attempts {
		if !a.Parked.IsZero() {
			parked[k] = map[string]any{"since": a.Parked, "next": a.Next, "err": a.LastErr}
		} else {
			attempts[k] = map[string]any{"failures": a.Failures, "next": a.Next, "err": a.LastErr}
		}
	}
	r.mu.Lock()
	last := copyMap(r.last)
	r.mu.Unlock()
	claims := map[string]claimState{}
	for k, c := range r.state.Claims {
		claims[k] = *c
	}
	skipped := 0
	for _, s := range r.state.SolSkipped {
		skipped += len(s)
	}
	return map[string]any{
		"role": r.Role, "offset": r.Offset.String(), "gnoGas": r.GnoGas,
		"gnoNext": r.state.GnoNext, "solCursor": copyMap(r.state.SolCursor), "solSkipped": skipped, "pending": pending, "done": copyMap(r.state.Done), "taken": copyMap(r.state.Taken), "refused": copyMap(r.state.Refused),
		"claims": claims, "reemits": copyMap(r.state.Reemits), "parked": parked, "attempts": attempts, "proposalsSeen": copyMap(r.state.ProposalsSeen),
		"last": last, "alarms": r.Alarm.Standing(),
	}
}

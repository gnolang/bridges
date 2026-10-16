package agent

import (
	"context"
	"encoding/json"
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

// The relayer (eth/bridge/PLAN.md §5.7): a primary and a backup 60 s apart, sharing one Ethereum key (§5.5) — the
// backup acts only on what has been possible for a minute and nobody took, and sends on Ethereum only while the
// key has sent nothing of anybody else's for its 60 s (ethx.Sender.KeyQuiet). It decides nothing about what is
// genuine: the chains check the signatures, the routes and that no message is delivered twice, so a relayer that
// lies only wastes its own gas — and on Ethereum not even that, since every call is simulated before it is sent.
//
// A tick: gno.land → Ethereum (relayer_out.go: deliveries under accepted headers, decisions first, the batch
// re-emit, -proveOld's header proofs), HlAdmin's matured decisions and gno.land's queued ones (executor.go), the
// lapsed GovDAO proposals, then the jobs another file registered (the Ethereum → gno.land lane's Process and
// Claim loops) — each with the attempt bookkeeping of attempt.go.

const (
	// BackupOffset is how far behind the primary the backup acts (§5.7).
	BackupOffset = 60 * time.Second
	// reemitSpacing is the least time between two batch re-emits (one must land and be scanned first).
	reemitSpacing = 30 * time.Second
	// deliveriesPerTick bounds the Ethereum sends of one tick, so the other loops run between them.
	deliveriesPerTick = 8
	// scanPerTick bounds the gno blocks the event index reads in one tick.
	scanPerTick = 2000
)

// RelayerJob is a loop another file adds to the relayer's tick (the inbound lane's Process and Claim): its name
// keys its state in the relayer's state file.
type RelayerJob interface {
	Name() string
	Tick(ctx context.Context, r *Relayer) error
	State() any // a pointer the relayer loads and saves under Name()
}

var relayerJobs []func() RelayerJob

// RegisterRelayerJob adds a job to every relayer made after it (call it from an init function).
func RegisterRelayerJob(f func() RelayerJob) { relayerJobs = append(relayerJobs, f) }

// ethSender is the relayer's and the updater's Ethereum key (ethx.Sender; a fake in tests).
type ethSender interface {
	Send(ctx context.Context, c ethx.Call) (*types.Receipt, error)
	Settle(ctx context.Context) error
	KeyQuiet(ctx context.Context, d time.Duration) (bool, error)
	Tip(ctx context.Context) (*big.Int, error)
	Balance(ctx context.Context) (*big.Int, error)
	InFlight() string
	Address() common.Address
	// StateGas says whether the chain meters state gas (EIP-8037: the sender's probe), where a transaction's gas past
	// EIP-7825's cap is a reservoir for state growth
	StateGas(ctx context.Context) bool
}

// ethChain is Ethereum as the machines read it through one provider (ethx.Client; a fake in tests).
type ethChain interface {
	ethx.Reader
	BlockAt(ctx context.Context, at ethx.At) (*ethx.Block, error)
	BalanceAt(ctx context.Context, addr common.Address, at ethx.At) (*big.Int, error)
	StorageAt(ctx context.Context, addr common.Address, slot common.Hash, at ethx.At) (common.Hash, error)
	Logs(ctx context.Context, q ethx.LogQuery) ([]types.Log, error)
	LogsRange(ctx context.Context, q ethx.LogQuery, from, to uint64) ([]types.Log, error)
	Receipt(ctx context.Context, h common.Hash) (*types.Receipt, error)
	Transaction(ctx context.Context, h common.Hash) (*ethx.TxInput, error)
	// SignedTransaction is a transaction by hash as its signature makes it: its sender recovered, never the provider's
	// word (E5-RT3: the bookings rest on it)
	SignedTransaction(ctx context.Context, h common.Hash) (*ethx.TxInput, error)
}

// runner sends the batch re-emit, one transaction of Reemit MsgCalls (gnoside.Runner; a fake in tests).
type runner interface {
	Reemit(ctx context.Context, mailbox string, nonces []uint32, gasWanted int64) (*gno.Result, error)
}

// Relayer is the relayer machine.
type Relayer struct {
	Cfg    *Config
	State  string // file
	Every  time.Duration
	Role   string // "primary" or "backup"
	Offset time.Duration
	GnoGas int64 // gas wanted for a gno call (45M at the chain's minimum price)
	Alarm  *Alarmer
	Sender ethSender
	Eth    ethChain

	gno    gnoside.Caller
	light  gnoside.Light
	blocks gnoside.Blocks
	runner runner
	now    func() time.Time

	env    *outEnv
	leafs  map[string]leafInfo
	jobs   []RelayerJob
	mu     sync.Mutex
	state  relayerState
	last   map[string]string
	status statusBox
	quiet  bool // the backup's view of the shared key this tick
	sent   int  // Ethereum sends this tick
	float  floatWatch
	latest *ethx.Block // this tick's latest block (the delivery rule's price)
	// trusted is the client's trusted height at the last pass (the floor prices a decision's header at the set after it);
	// floor the floor's last line and floorAt when it was judged (at the float's own cadence)
	trusted int64
	floor   string
	floorAt time.Time
	prices  pricesWatch // eth.prices' date (prices:stale)
	// backstop buys the header a waiting decision needs when the updater's key cannot (decisionBackstop): an updater
	// over the relayers' key, its submissions alarmed as "backstop:…"
	backstop *Updater
	// proposalsBad is when GovDAO's list was first not read whole, since the last whole reading (zero: read whole)
	proposalsBad time.Time
}

type relayerState struct {
	Out            outState                   `json:"out"`
	Attempts       map[string]*attempt        `json:"attempts"`
	Ready          map[string]time.Time       `json:"ready"`
	Taken          map[string]bool            `json:"taken"`          // decision numbers reported taken
	TakenSeen      map[string]int64           `json:"takenSeen"`      // first look of a number past HlAdmin's last with no copy processed
	DecisionCopies map[string][]decisionCopy  `json:"decisionCopies"` // decision number → every copy r/hlgov dispatched
	LastBatch      time.Time                  `json:"lastBatch"`
	Reemits        map[string]time.Time       `json:"reemits,omitempty"` // nonce → this relayer's last re-emit of it (the poisoned re-emits' back-off)
	Batches        int                        `json:"batches"`
	LastBackstop   time.Time                  `json:"lastBackstop"` // the relayers' last header bought for a decision the updater did not buy
	Backstops      int                        `json:"backstops"`
	AdminLow       map[string]uint64          `json:"adminLow"`      // HlAdmin address → its cursor: every number below it was read at a finalized block
	AdminPending   map[string][]uint64        `json:"adminPending"`  // HlAdmin address → the numbers below the cursor still pending there (E3-GS9)
	GnoLow         map[string]int64           `json:"gnoLow"`        // a realm → the lowest decision number of its ledger that may still be pending (E3-RL4)
	Refused        map[string]string          `json:"refused"`       // executes refused for good, by key
	ProposalsSeen  map[string]int64           `json:"proposalsSeen"` // GovDAO proposal id → when first seen open
	SigsBound      int                        `json:"sigsBound"`     // -proveOld's per-call signature bound learnt (halved when a call does not fit the cap)
	Jobs           map[string]json.RawMessage `json:"jobs,omitempty"`
	// ProposalsFloor is where the reading of GovDAO's list may stop: every proposal below its Below was final when the
	// list was last read whole (gnoside.ProposalFloor; the integration lane I3)
	ProposalsFloor *gnoside.ProposalFloor `json:"proposalsFloor,omitempty"`
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
	s.Out.init()
	for _, m := range []*map[string]*attempt{&s.Attempts} {
		if *m == nil {
			*m = map[string]*attempt{}
		}
	}
	if s.Ready == nil {
		s.Ready = map[string]time.Time{}
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
	if s.AdminLow == nil {
		s.AdminLow = map[string]uint64{}
	}
	if s.AdminPending == nil {
		s.AdminPending = map[string][]uint64{}
	}
	if s.GnoLow == nil {
		s.GnoLow = map[string]int64{}
	}
	if s.Refused == nil {
		s.Refused = map[string]string{}
	}
	if s.ProposalsSeen == nil {
		s.ProposalsSeen = map[string]int64{}
	}
	if s.Reemits == nil {
		s.Reemits = map[string]time.Time{}
	}
	for _, j := range r.jobs {
		if raw, ok := s.Jobs[j.Name()]; ok {
			if err := json.Unmarshal(raw, j.State()); err != nil {
				return fmt.Errorf("%s: job %s's state: %w", r.State, j.Name(), err)
			}
		}
	}
	return nil
}

func (r *Relayer) save() error {
	if r.State == "" {
		return nil
	}
	if len(r.jobs) > 0 {
		r.state.Jobs = map[string]json.RawMessage{}
		for _, j := range r.jobs {
			b, err := json.Marshal(j.State())
			if err != nil {
				return err
			}
			r.state.Jobs[j.Name()] = b
		}
	}
	return writeJSON(r.State, r.state)
}

func (r *Relayer) init() error {
	if r.Cfg.HasContracts() {
		// it re-emits a batch only when the batch is due its header (headerDue), the updater's rule
		if err := r.Cfg.CheckPrices(); err != nil {
			return fmt.Errorf("relayer: %w", err)
		}
		// it delivers at no fee check only the sends the updater's headers booked, and holds the rest to their own fee:
		// without eth.updater it could tell no booked header, and a held send would buy header after header (E3-GS2)
		if r.Cfg.Eth.Updater == "" {
			return errors.New("relayer: eth.updater (the updater's key's address) is needed: the relayer delivers at no fee check only the sends the updater's headers booked")
		}
	}
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
	if r.Every == 0 {
		r.Every = 3 * time.Second
	}
	r.last = map[string]string{}
	r.leafs = map[string]leafInfo{}
	if r.jobs == nil {
		for _, f := range relayerJobs {
			r.jobs = append(r.jobs, f())
		}
	}
	r.env = &outEnv{cfg: r.Cfg, gno: r.gno, light: r.light, blocks: r.blocks, eth: r.Eth, alarm: r.Alarm, st: &r.state.Out}
	if r.Cfg.HasContracts() && r.backstop == nil {
		r.backstop = &Updater{Cfg: r.Cfg, Sender: r.Sender, Eth: r.Eth, Alarm: r.Alarm, gno: r.light, blocks: r.blocks, alarmPrefix: "backstop", now: func() time.Time { return r.now() }}
		if err := r.backstop.init(); err != nil {
			return err
		}
	}
	return r.load()
}

// Run relays until ctx ends. The gno node, the Ethereum provider and the key are the config's unless set before.
func (r *Relayer) Run(ctx context.Context) error {
	if r.Sender == nil || r.Eth == nil {
		return errors.New("relayer: an Ethereum provider and the relayers' key are needed")
	}
	if r.gno == nil {
		c := r.Cfg.GnoClient()
		r.gno, r.light, r.blocks = c, c, gnoside.Node{Client: c}
		r.runner = &gnoside.Runner{Client: c}
	}
	if err := r.init(); err != nil {
		return err
	}
	log.Printf("relayer (%s): eth key %s, gno key %s, mailbox %s, client %s, admin %s", r.Role, r.Sender.Address().Hex(), r.Cfg.Gno.Key, r.Cfg.Eth.Mailbox, r.Cfg.Eth.Client, r.Cfg.Eth.Admin)
	if !r.Cfg.HasContracts() {
		log.Printf("relayer: WARNING the config names no eth.mailbox/client/gate/admin: nothing is delivered to Ethereum and HlAdmin is not executed")
	}
	for {
		if r.Cfg.HasContracts() {
			r.env.probeLogs(ctx, r.now()) // three days of the client's logs: at start and every six hours (E5-GS2)
		}
		r.tick(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.Every):
		}
	}
}

func (r *Relayer) tick(ctx context.Context) {
	r.env.reset()
	if len(r.leafs) > 4096 {
		r.leafs = map[string]leafInfo{} // a block's results never change; the cache only bounds its size
	}
	r.sent = 0
	r.quiet = r.Role == "primary"
	if r.Role == "backup" {
		q, err := r.Sender.KeyQuiet(ctx, r.Offset)
		if err != nil {
			log.Printf("relayer: the shared key's nonce: %v", err)
		}
		r.quiet = q && err == nil
		if !r.quiet {
			r.note("key", "the backup sends nothing on Ethereum: the shared key sent within the last "+r.Offset.String())
		} else {
			r.note("key", "quiet: the backup may send")
		}
	}
	if err := r.outbound(ctx); err != nil && !errors.Is(err, errNotYet) {
		log.Printf("relayer: gno → ethereum: %v", err)
	}
	if err := r.adminExecutes(ctx); err != nil {
		log.Printf("relayer: HlAdmin's decisions: %v", err)
	}
	if err := r.executes(ctx); err != nil {
		log.Printf("relayer: gno.land's queued decisions: %v", err)
	}
	if err := r.finishProposals(ctx); err != nil {
		log.Printf("relayer: proposals: %v", err)
	}
	for _, j := range r.jobs {
		if err := j.Tick(ctx, r); err != nil {
			log.Printf("relayer: %s: %v", j.Name(), err)
		}
	}
	r.note("float", r.float.watch(ctx, r.now(), "relayer-eth", r.Sender.Balance, r.Cfg.Runway, r.Alarm))
	if floor := r.floatFloor(ctx); floor != "" {
		r.note("floor", floor)
	}
	r.state.Out.prune()
	for nk := range r.state.Reemits {
		if n, err := strconv.ParseUint(nk, 10, 32); err != nil || uint32(n) < r.state.Out.Next || r.state.Out.Done[nk] != "" {
			delete(r.state.Reemits, nk)
		}
	}
	if err := r.save(); err != nil {
		log.Printf("relayer: state: %v", err)
	}
	r.status.publish(r.buildStatus())
}

// floatFloor alarms float:relayer while the relayers' key holds less than a decision's header needs at the emergency
// cap (§5.5, §11.4; E4-RT1): the backstop's reserve, which the deliveries' runway cannot see — one such header is weeks
// of deliveries. Read from the float's own reading (every few minutes), priced at the set after the trusted height.
func (r *Relayer) floatFloor(ctx context.Context) string {
	if !r.Cfg.HasContracts() || r.float.bal == nil {
		return ""
	}
	now := r.now()
	if r.floor != "" && now.Sub(r.floorAt) < floatEvery {
		return r.floor
	}
	r.floorAt = now
	next := int64(0) // before a pass has read the client: the model's gnoland-1 set
	if r.trusted > 0 {
		next = r.trusted + 1
	}
	text, short := relayerFloatText(r.Sender.Address().Hex(), r.float.bal, r.env.headerGasAt(ctx, next), r.Cfg.EmergencyMaxBaseFee())
	if short {
		r.Alarm.Alarm("float:relayer", text)
	} else {
		r.Alarm.Clear("float:relayer")
	}
	r.floor = text
	return text
}

// --------------------------------------------------------------------- roles --

// act says whether this relayer takes an action now: the primary at once, the backup once the action has been
// possible for Offset.
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

// ethAct is act for an Ethereum send: the backup also needs the shared key quiet, and a tick sends at most
// deliveriesPerTick.
func (r *Relayer) ethAct(key string) bool {
	if !r.act(key) {
		return false
	}
	return r.quiet && r.sent < deliveriesPerTick
}

// forget drops an action's clock and attempts, and the parked alarm it may have raised.
func (r *Relayer) forget(key string) {
	delete(r.state.Ready, key)
	if r.state.Attempts[key].parked() {
		r.Alarm.Clear("parked:" + key)
	}
	delete(r.state.Attempts, key)
}

func (r *Relayer) note(key, what string) {
	r.mu.Lock()
	r.last[key] = r.now().UTC().Format(time.RFC3339) + " " + ethx.Redact(what)
	r.mu.Unlock()
}

func (r *Relayer) noted(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.last[key]
	return ok
}

// gate keeps a standing line in the status under a key and logs it once each time its words change.
func (r *Relayer) gate(key, what string) {
	r.mu.Lock()
	same := strings.HasSuffix(r.last[key], " "+what)
	r.mu.Unlock()
	if !same {
		log.Printf("relayer: %s: %s", key, what)
	}
	r.note(key, what)
}

// Note lets a registered job write a status line.
func (r *Relayer) Note(key, what string) { r.note(key, what) }

// Act lets a registered job use the primary/backup rule.
func (r *Relayer) Act(key string) bool { return r.act(key) }

// Forget lets a registered job drop an action's clock and attempts.
func (r *Relayer) Forget(key string) { r.forget(key) }

// GnoClient is the relayer's gno node and gas key (for a registered job).
func (r *Relayer) GnoClient() gnoside.Caller { return r.gno }

// TryCall lets a registered job make a gno call with the attempt bookkeeping.
func (r *Relayer) TryCall(ctx context.Context, key, pkg, fn string, args ...string) (*gno.Result, error) {
	return r.tryCall(ctx, key, pkg, fn, r.GnoGas, gasGiven, args...)
}

// TryCallGas is TryCall simulated under the gas given (a job that sizes its call's gas: the Claim batch).
func (r *Relayer) TryCallGas(ctx context.Context, key, pkg, fn string, gas int64, args ...string) (*gno.Result, error) {
	return r.tryCall(ctx, key, pkg, fn, gas, gasGiven, args...)
}

// CallGas is the gas the relayer's gno calls are simulated under (-gas).
func (r *Relayer) CallGas() int64 { return r.GnoGas }

// Backup says this relayer is the backup (a registered job's rule: the inbound Claims, E5-GS1).
func (r *Relayer) Backup() bool { return r.Role == "backup" }

// gasRule is how tryCall asks for a call's gas: gasGiven simulates it under the gas given (the gno client tries four
// times it on an out-of-gas, within the block's gas); gasUpToTheBlock raises the ceiling fourfold past what the client
// reached on each out-of-gas, as far as the block's gas (gnoside.CallUpToTheBlock) — an execution whose gas only a
// simulation knows: a realm decision's Execute, GovDAO's ExecuteOrRejectProposal of a builder-made proposal (a forged
// restore of 256 ids takes ≈2.53B to Execute on a gno v1.5.1 node, past the client's 180M under the 45M -gas: realms.md,
// audit round 5, "for the lead"). Either way the client sizes the broadcast from the simulation that fit.
type gasRule int

const (
	gasGiven gasRule = iota
	gasUpToTheBlock
)

// tryCall makes one gno call under an action key with the attempt bookkeeping of §5.7, its gas asked for by the rule.
func (r *Relayer) tryCall(ctx context.Context, key, pkg, fn string, gas int64, rule gasRule, args ...string) (*gno.Result, error) {
	a := r.state.Attempts[key]
	if !a.due(r.now()) {
		return nil, errNotYet
	}
	var res *gno.Result
	var err error
	if rule == gasUpToTheBlock {
		res, err = gnoside.CallUpToTheBlock(ctx, r.gno, pkg, fn, gas, args...)
	} else {
		res, err = r.gno.Call(ctx, pkg, fn, gas, args...)
	}
	if err != nil {
		r.gnoFailed(key, err)
		return nil, err
	}
	r.succeeded(key)
	return res, nil
}

func (r *Relayer) gnoFailed(key string, err error) {
	a := r.state.Attempts[key]
	if a == nil {
		a = &attempt{}
		r.state.Attempts[key] = a
	}
	if parkedNow := a.fail(r.now(), err, gno.Deterministic(err)); parkedNow || a.parked() {
		r.Alarm.Alarm("parked:"+key, fmt.Sprintf("%s parked after %d failures (retried hourly): %v", key, a.Failures, err))
	}
	r.note(key, "failed: "+err.Error())
}

func (r *Relayer) succeeded(key string) {
	if r.state.Attempts[key].parked() {
		r.Alarm.Clear("parked:" + key)
	}
	delete(r.state.Attempts, key)
}

// ethFailed sorts a failed Ethereum send under an action key: a base fee above the cap is a wait (no failure
// counted, the alarm standing while it lasts); a revert is deterministic, named on the attempt (its cause); the rest
// transient.
func (r *Relayer) ethFailed(key string, err error) {
	var ft *ethx.FeeTooHigh
	if errors.As(err, &ft) {
		r.note(key, "waits: "+ft.Error())
		r.Alarm.AlarmEvery("eth:fee-high", "Ethereum's "+ft.Error()+": route deliveries and their headers wait (decisions, executes and flags go under the emergency cap)", 6*time.Hour)
		return
	}
	cause := ""
	det := false
	if rv, ok := ethx.AsRevert(err); ok {
		det, cause = true, rv.Name
		if rv.Name == "Error" {
			cause = rv.Reason
		}
	} else if !ethx.Transient(err) {
		det = true
	}
	a := r.state.Attempts[key]
	if a == nil {
		a = &attempt{}
		r.state.Attempts[key] = a
	}
	if parkedNow := a.fail(r.now(), err, det, cause); parkedNow || a.parked() {
		r.Alarm.Alarm("parked:"+key, fmt.Sprintf("%s parked after %d failures (retried hourly): %v", key, a.Failures, err))
	}
	r.note(key, "failed: "+err.Error())
}

// ethSend sends one call for an action (counted against the tick's bound).
func (r *Relayer) ethSend(ctx context.Context, c ethx.Call) (*types.Receipt, error) {
	r.sent++
	return r.Sender.Send(ctx, c)
}

// ---------------------------------------------------------------------- status --

// Handler serves GET /v1/status from the last published snapshot.
func (r *Relayer) Handler() http.Handler { return r.status.handler() }

// Status is the relayer's last published snapshot.
func (r *Relayer) Status() map[string]any { return r.status.get() }

func (r *Relayer) buildStatus() map[string]any {
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
	var waiting []string
	for n, hs := range r.state.Out.Index.Heights {
		waiting = append(waiting, fmt.Sprintf("#%d at %s", n, eventsText(hs)))
	}
	sort.Strings(waiting)
	return map[string]any{
		"role": r.Role, "offset": r.Offset.String(), "gnoGas": r.GnoGas, "key": r.Sender.Address().Hex(), "inFlight": r.Sender.InFlight(),
		"gnoNext": r.state.Out.Next, "done": copyMap(r.state.Out.Done), "events": waiting, "scanned": r.state.Out.Index.Scanned,
		"taken": copyMap(r.state.Taken), "refused": copyMap(r.state.Refused), "batches": r.state.Batches, "lastBatch": r.state.LastBatch,
		"backstops": r.state.Backstops, "lastBackstop": r.state.LastBackstop,
		"parked": parked, "attempts": attempts, "last": last, "alarms": r.Alarm.Standing(),
	}
}

// The real Ethereum client and sender are what the machines' interfaces take.
var (
	_ ethChain  = (*ethx.Client)(nil)
	_ ethSender = (*ethx.Sender)(nil)
)

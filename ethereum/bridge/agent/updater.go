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
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The updater (eth/bridge/PLAN.md §6.1, §5.5): it feeds gno.land's headers to the light client on Ethereum, and only
// when a header buys something, since an update costs millions of gas there:
//
//   - the announcing header — the header whose NextValidatorsHash leaves the client's set, presented with the set
//     it announces — within a minute of a validator change executing (r/sys/validators' events arm it; the header
//     itself is found from the headers), at once, under the emergency cap;
//   - the header after the block the waiting dispatches' newest events share, when a header is due for the ones it
//     serves (headerDue, budget.go: a decision waits — under the emergency cap, outside the budget — or their own fees
//     cover the whole cost the header commits, the header's and every delivery's by its leaf, or the oldest of them has
//     waited six hours and the budget covers what their fees lack); when the waiting are split over blocks it gives
//     the relayer's batch re-emit BatchWait to land, then serves each block's group alone, the blocks holding a
//     decision first, then the oldest due (at once when no batch can gather them: each block already holds a batch);
//
// and nothing else: no header is bought with nothing waiting (§5.5: "only when they buy something"), and a stranger's
// update neither prices ours (the model prices a header, never a receipt whose gas its sender chose) nor holds it
// (the 2-minute spacing counts this updater's own updates) nor stands in for the six-hour heartbeat, which is the
// oldest waiting send's age (the plan red team's E2-RT4). Every header bought for route sends books them in the
// budget's ledger, kept in the state file (budget.go), and names them in the trailer of its calls, the (nonce, header)
// pairs the relayer delivers at no fee check (E4-GS2, E4-GS4). An update whose chosen signatures do not fit one call (the
// estimate plus 20% over EIP-7825's cap, or the configured bound) is staged, a stage left by a stop resumed from the
// chain (the slots its StageProgress transactions carried under the stage key the client's current epoch and expected
// set give — a stage another epoch began is orphaned, its slots sent again); a send waiting seven hours alarms
// (delivery:age), and so does a change gno.land announced and the client has not followed for 30 minutes. Its own
// Ethereum key is a float of §5.5: its floor a decision's header at the emergency cap (≈0.174 ETH at gnoland-1's set,
// float:updater), its route headers on top (E2-RT8, E4-FC5).

const (
	// AnnounceLagAfter alarms an announced change the client has not followed (§6.1: 30 minutes).
	AnnounceLagAfter = 30 * time.Minute
	// MinUpdateGap is the least time between two updates but an announcing one.
	MinUpdateGap = 2 * time.Minute
	// DefaultBatchWait is how long split waiting dispatches wait for the relayer's batch before the oldest is
	// served alone.
	DefaultBatchWait = 90 * time.Second
	// SetSizeWarn is the set size at which client:set-bound alarms (§9: a staged update's cost grows with the set).
	SetSizeWarn = 40
	// stageBack is how far back a stage's StageProgress logs are looked for (a day of blocks).
	stageBack = 7_200
)

// Updater is the updater machine.
type Updater struct {
	Cfg       *Config
	Sender    ethSender
	Eth       ethChain
	State     string
	Every     time.Duration
	MinGap    time.Duration
	BatchWait time.Duration
	Alarm     *Alarmer

	gno    gnoside.Light
	blocks gnoside.Blocks
	now    func() time.Time

	env   *outEnv
	float floatWatch
	// alarmPrefix names the alarms of a submission ("updater"; the relayers' backstop for a decision's header
	// submits with "backstop")
	alarmPrefix string
	sets        map[int64]int // the validator count at a height (cached)
	trusted     int64         // the client's trusted height at the last step (the float floor prices the set after it)
	scanBehind  time.Time     // since when the event scan has been behind the head (scanReached)
	prices      pricesWatch   // eth.prices' date (prices:stale)
	mu          sync.Mutex
	status      map[string]any
	box         statusBox
	state       updaterState
}

type updaterState struct {
	Out          outState  `json:"out"`
	Armed        int64     `json:"armed"`        // the height of a validator-set event the client has not followed
	AnnounceSeen time.Time `json:"announceSeen"` // when an announcing header the client lacks was first seen
	SplitSince   time.Time `json:"splitSince"`   // since when a batch of the split waiting has been due (the relayer re-emits it)
	LastSubmit   int64     `json:"lastSubmit"`
	LastSubmitAt time.Time `json:"lastSubmitAt"`
	Submits      int       `json:"submits"`
	SigsBound    int       `json:"sigsBound"` // the per-call signature bound learnt (halved when a call does not fit the cap)
	// Attempt is the updates' failures in a row (the relayer's backoff: a minute for a transient one or a refusal
	// the simulation answered; a minute to an hour, parked and tried hourly after three, for an update that spent
	// gas and failed — mined reverted, or every call sent and the header still not trusted); AnnAttempt the
	// announcing header's and DecAttempt a decision's headers', each kept apart (E3-GS3). An update a stranger's
	// landed before — it reverts HeightNotAboveTrusted and the client trusts its height or above — is no failure.
	Attempt    *attempt `json:"attempt,omitempty"`
	AnnAttempt *attempt `json:"annAttempt,omitempty"`
	DecAttempt *attempt `json:"decAttempt,omitempty"`
	// Budget is the fee-funded delivery budget's ledger (budget.go), durable here: a restart keeps its balance
	Budget budget `json:"budget"`
}

func (u *Updater) init() error {
	if err := u.Cfg.CheckPrices(); err != nil {
		return fmt.Errorf("updater: %w", err)
	}
	if u.now == nil {
		u.now = time.Now
	}
	if u.Every == 0 {
		u.Every = 5 * time.Second
	}
	if u.MinGap == 0 {
		u.MinGap = MinUpdateGap
	}
	if u.BatchWait == 0 {
		u.BatchWait = DefaultBatchWait
	}
	u.status = map[string]any{}
	u.sets = map[int64]int{}
	if u.alarmPrefix == "" {
		u.alarmPrefix = "updater"
		// the relayer delivers at no fee check only the sends named in the trailers of the headers eth.updater's key
		// records (bookedFor), and the updater judges every send as the relayer does: a config whose eth.updater is not
		// this key would have the updater book what the relayer then holds, or re-serve what it delivers — refused, never
		// run so (E3-GS2, E4-GS2)
		if u.Sender != nil {
			if a := u.Sender.Address(); u.Cfg.Eth.Updater == "" || Address(u.Cfg.Eth.Updater) != a {
				return fmt.Errorf("updater: eth.updater (%q) must be this key's address, %s: the relayer delivers at no fee check only the sends the headers of eth.updater's key booked", u.Cfg.Eth.Updater, a.Hex())
			}
		}
	}
	u.env = &outEnv{cfg: u.Cfg, gno: u.gno, light: u.gno, blocks: u.blocks, eth: u.Eth, alarm: u.Alarm, st: &u.state.Out}
	u.env.own = func(hash [32]byte, epoch uint64) *headerBook {
		h := hex.EncodeToString(hash[:])
		for _, pb := range u.state.Budget.Pending {
			if pb.Hash == h && pb.Epoch == epoch {
				return &headerBook{Epoch: epoch, Ours: true, Nonces: pb.Nonces}
			}
		}
		return nil
	}
	// the headers this key sent and has not settled: their events are read every step whatever else a stranger's re-emits
	// put after them (E5-GS4)
	u.env.pending = func() []int64 {
		var hs []int64
		for _, pb := range u.state.Budget.Pending {
			hs = append(hs, pb.Header)
		}
		return hs
	}
	if err := readJSON(u.State, &u.state); err != nil {
		return err
	}
	u.state.Out.init()
	u.state.Budget.init()
	return nil
}

func (u *Updater) save() error {
	if u.State == "" {
		return nil
	}
	return writeJSON(u.State, u.state)
}

// Run feeds the client until ctx ends.
func (u *Updater) Run(ctx context.Context) error {
	if u.Sender == nil || u.Eth == nil {
		return errors.New("updater: an Ethereum provider and the updater's key are needed")
	}
	if !u.Cfg.HasContracts() {
		return errors.New("updater: eth.mailbox, eth.client, eth.gate and eth.admin are needed")
	}
	if u.gno == nil {
		c := u.Cfg.SourceClient(u.Cfg.Gno.RPC)
		u.gno, u.blocks = c, gnoside.Node{Client: c}
	}
	if err := u.init(); err != nil {
		return err
	}
	log.Printf("updater: key %s, client %s, node %s, a waiting send buys its own header within the budget at %s; the budget's %s", u.Sender.Address().Hex(), u.Cfg.Eth.Client, u.Cfg.Gno.RPC, u.Cfg.Heartbeat(), u.state.Budget.text())
	for {
		u.env.probeLogs(ctx, u.now()) // three days of the client's logs: at start and every six hours (E5-GS2)
		if err := u.tick(ctx); err != nil {
			log.Printf("updater: %v", err)
			u.set("lastErr", err.Error())
		} else {
			u.set("lastErr", "")
		}
		u.set("float", u.float.watch(ctx, u.now(), "updater-eth", u.Sender.Balance, u.Cfg.Runway, u.Alarm))
		u.set("floor", u.floatFloor(ctx))
		u.state.Out.prune()
		u.state.Budget.prune(u.state.Out.Next)
		if err := u.save(); err != nil {
			log.Printf("updater: state: %v", err)
		}
		u.box.publish(u.buildStatus())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(u.Every):
		}
	}
}

func (u *Updater) set(k string, v any) {
	u.mu.Lock()
	u.status[k] = v
	u.mu.Unlock()
}

// tick is one round: the client, the lag, the backlog, the plan, the submission.
func (u *Updater) tick(ctx context.Context) error {
	e := u.env
	e.reset()
	u.set("plan", "")
	u.set("due", "")
	now := u.now()
	// every read up to the walk's, and the walk: one failing for minutes buys no header, a decision's included —
	// alarmed outbox:walk (E5-GS4)
	var walkErr error
	defer func() { e.walked(now, walkErr) }()
	client := Address(u.Cfg.Eth.Client)
	cs, err := ethx.ReadClient(ctx, u.Eth, client, ethx.Latest)
	if err != nil {
		walkErr = err
		return err
	}
	if cs.ChainID != u.Cfg.Gno.ChainID {
		return fmt.Errorf("the client follows chain %q, the config says %q", cs.ChainID, u.Cfg.Gno.ChainID)
	}
	latest, err := u.Eth.BlockAt(ctx, ethx.Latest)
	if err != nil {
		walkErr = err
		return err
	}
	T := cs.TrustedHeight
	u.trusted = T
	u.prices.check(u.Cfg, u.Alarm, now)
	// a booking an update left pending (a wait the sender stopped, a stop before the save) settles by the chain's word
	// before anything else draws on the budget (E3-GS5)
	u.resolveBookings(ctx)
	u.set("trustedHeight", T)
	u.set("trustedHash", hex.EncodeToString(cs.TrustedHash[:]))
	u.set("trustedTime", time.Unix(int64(cs.TrustedTime), 0).UTC().Format(time.RFC3339))
	u.set("epoch", cs.Epoch)
	u.set("budget", u.state.Budget.text())
	head, err := u.gno.Status(ctx)
	if err != nil {
		walkErr = err
		return err
	}
	u.set("gnoHead", head.Height)
	u.setBound(ctx, T)

	// 1. the announcing header, at once — planned before and apart from the backlog's reads, so a failing read of
	// the backlog never holds a validator change (or its lag alarm)
	if head.Height > T {
		ann, err := planUpdate(ctx, u.gno, cs, head.Height)
		if err != nil {
			u.set("plan", "none: "+err.Error())
			u.Alarm.Alarm("updater:plan", err.Error())
			return nil
		}
		u.Alarm.Clear("updater:plan")
		if ann.Announcing {
			if u.state.AnnounceSeen.IsZero() {
				u.state.AnnounceSeen = now
			}
			if now.Sub(u.state.AnnounceSeen) > AnnounceLagAfter {
				u.Alarm.Alarm("client:announce-lag", fmt.Sprintf("gno.land announced a validator change at header %d %s ago and the client has not followed it", ann.Target, now.Sub(u.state.AnnounceSeen).Round(time.Minute)))
			}
			if ok, why := u.mayUpdate(now, true, false); !ok {
				u.set("plan", fmt.Sprintf("the announcing header %d waits: %s", ann.Target, why))
				return nil
			}
			u.set("plan", fmt.Sprintf("the announcing header %d (a set of %d)", ann.Target, len(ann.NextSet)))
			if _, err := u.submit(ctx, cs, ann, true, "the announcing header"); err != nil && !errors.Is(err, errWaits) && !errors.Is(err, errOvertaken) {
				return err
			}
			return nil
		}
		u.state.AnnounceSeen = time.Time{}
		u.Alarm.Clear("client:announce-lag")
		if u.state.Armed != 0 && head.Height > u.state.Armed+3 {
			log.Printf("updater: the validator-set event at %d announced no change of the client's set", u.state.Armed)
			u.state.Armed = 0
		}
	}

	// the backlog: the mailbox, then the head the scan reaches (every message walked lies in a block it read), the
	// scan, then the walk
	mb, err := e.mailboxJSON(ctx)
	if err != nil {
		walkErr = err
		return err
	}
	if head, err = u.gno.Status(ctx); err != nil {
		walkErr = err
		return err
	}
	if err := e.checkContracts(mb); err != nil {
		u.set("plan", "none: "+err.Error())
		walkErr = err
		return err
	}
	if err := e.readAdmins(ctx); err != nil {
		log.Printf("updater: HlAdmin's line (the configured one stands in): %v", err)
	}
	e.readLines(ctx)
	start, err := e.scanStart(ctx, &u.state.Out, mb, head.Height)
	if err != nil {
		walkErr = err
		return err
	}
	found, err := e.scanEvents(ctx, &u.state.Out, head.Height, start, scanPerTick)
	if err != nil {
		log.Printf("updater: the event scan: %v", err)
	}
	for _, be := range found {
		if be.Valset && be.Height > T && be.Height > u.state.Armed {
			u.state.Armed = be.Height
			log.Printf("updater: a validator-set event at height %d", be.Height)
		}
	}
	decisions, routes, err := e.walkOut(ctx, &u.state.Out, cs, mb, head.Height)
	if err != nil {
		walkErr = err
		return err
	}
	// the events no step reads judged at the finalized block, a few a step (E5-GS4)
	if err := e.compact(ctx, now, &u.state.Out, append(append([]*outMsg{}, decisions...), routes...), head.Height); err != nil {
		log.Printf("updater: the events' compaction (judged again next step): %v", err)
	}
	// a send the ledger booked once adds neither its fee nor its delivery to a header's due rule again (E4-GS4)
	for _, t := range routes {
		t.feeSpent = u.state.Budget.settled(t.n)
	}
	// a decision copy HlAdmin recorded, cannot take yet, or takes from another copy buys nothing (E3-GS6); a proven send
	// held under a header that pays nothing for it waits as an unproven one does (E3-GS7)
	if err := e.markDecisions(ctx, decisions); err != nil {
		return err
	}
	if err := e.markHeld(ctx, u.Sender, latest, cs, now, routes); err != nil {
		log.Printf("updater: the proven sends' fees could not be judged (a send held by them waits for the next step): %v", err)
	}
	var live []*outMsg
	for _, t := range append(decisions, routes...) {
		if t.proofH < 0 {
			u.state.Out.end(t.n, "delivered")
			continue
		}
		// a send gno.land refunded is never delivered, so it buys no header (AQ2; the relayer alarms it)
		if t.kind == kindRoute && (t.proofH == 0 || t.held) && !t.settling {
			if refunded, why, err := e.sendRefunded(ctx, t); err == nil && refunded {
				u.state.Out.end(t.n, "refunded")
				log.Printf("updater: gno #%d ended: %s", t.n, why)
				continue
			}
		}
		live = append(live, t)
	}
	// a past copy ends here once the finalized block's counter covers its number (or its HlAdmin is retired): the
	// updater's own walk then moves past it (the relayer ends its copies with its own report)
	for _, t := range e.finalPast(ctx, decisions) {
		u.state.Out.end(t.n, "past")
	}
	// the waiting sends' age: seven hours alarms, whoever's updates kept the client fresh — a send proven under a
	// header that pays nothing for it included (§6.1; E3-GS7)
	e.deliveryAge(ctx, waitingOf(live), now)
	waiting, paused, err := e.unpausedOnly(ctx, waitingOf(live), cs, int64(latest.Time))
	if err != nil {
		return err
	}
	waiting, scanning := u.scanReached(waiting, head.Height, now)
	wp := planWaits(waiting, T, u.Cfg.ReemitBatch())
	u.set("waiting", map[string]any{"count": wp.Waiting, "shared": wp.Shared, "split": wp.Split, "below": len(wp.Below), "old": len(wp.Old), "paused": paused, "scanning": scanning})
	if head.Height <= T && len(wp.Old) == 0 {
		u.set("plan", "the chain is at the trusted height")
		return nil
	}

	// 2. the waiting dispatches: the header of the block they share, when the ones it serves are due one (§5.5, the
	// budget's rule); split, the relayer's batch is awaited, then each group's header, the blocks holding a decision
	// first, then the oldest due group — a group below the trusted height served by a header proof (E3-RT4), which the
	// batch does not gather and so never waits for
	hg := e.headerGasAt(ctx, T+1)
	bal := u.state.Budget.available()
	leafAt := func(h int64) func(*outMsg) (int, int) { return func(t *outMsg) (int, int) { return t.leaf(h) } }
	var target int64
	var prove bool
	var reason string
	var served []*outMsg
	emergency, draw := false, false
	switch {
	case wp.Shared > 0:
		facts, err := e.dueFacts(ctx, u.Sender, latest, waiting, now, hg, leafAt(wp.Shared))
		if err != nil {
			return err
		}
		facts.Budget = bal
		due, d, why := headerDue(facts)
		u.set("due", why)
		if due {
			target, reason, emergency, served, draw = wp.Shared+1, why, facts.Decisions > 0, waiting, d
		}
	case wp.Split:
		// the relayer re-emits a batch when the batch is due its header (its fees cover the whole cost, or its oldest
		// send's six hours): it is given BatchWait from the moment it became due; a batch not due, or none to gather,
		// is not waited for
		awaited := false
		if len(wp.Reemit) > 0 {
			batch := msgsOf(waiting, wp.Reemit)
			bf, err := e.dueFacts(ctx, u.Sender, latest, batch, now, hg, func(*outMsg) (int, int) { return len(batch), 0 })
			if err != nil {
				return err
			}
			bf.Lead, bf.AgeReemits = updateLead, true
			awaited, _, _ = headerDue(bf)
		}
		inWait := false
		if !awaited {
			u.state.SplitSince = time.Time{}
		} else {
			if u.state.SplitSince.IsZero() {
				u.state.SplitSince = now
			}
			inWait = now.Sub(u.state.SplitSince) < u.BatchWait
		}
		cands := make([]waitCand, 0, len(wp.Old)+len(wp.Groups))
		for _, g := range wp.Old {
			cands = append(cands, waitCand{waitGroup: g, prove: true})
		}
		if !inWait {
			for _, g := range wp.Groups {
				cands = append(cands, waitCand{waitGroup: g})
			}
		}
		var whys []string
		for _, c := range decisionsFirst(cands) {
			gas := hg
			if c.prove {
				gas = e.headerGasAt(ctx, c.Block+1)
			}
			facts, err := e.dueFacts(ctx, u.Sender, latest, c.Msgs, now, gas, leafAt(c.Block))
			if err != nil {
				return err
			}
			facts.Budget = bal
			due, d, why := headerDue(facts)
			whys = append(whys, fmt.Sprintf("block %d: %s", c.Block, why))
			if due {
				target, prove, emergency, served, draw = c.Block+1, c.prove, facts.Decisions > 0, c.Msgs, d
				if c.prove {
					reason = fmt.Sprintf("%s; block %d's %d proven by a header proof (below the trusted height %d: an update above it landed first, which no proof can be raced by, E3-RT4)", why, c.Block, len(c.Msgs), T)
				} else {
					reason = fmt.Sprintf("%s; block %d's %d served alone (the split waiting: no batch gathers them, or none came within %s)", why, c.Block, len(c.Msgs), u.BatchWait)
				}
				break
			}
		}
		u.set("due", strings.Join(whys, "; "))
		switch {
		case target == 0 && inWait:
			u.set("plan", "the waiting are split over blocks: the relayer's batch re-emit is awaited")
		case len(wp.Groups) == 0 && len(wp.Old) == 0:
			u.set("plan", "the waiting lie below the trusted height: the relayer re-emits them")
		}
	}
	if !wp.Split {
		u.state.SplitSince = time.Time{}
	}
	// (no header with nothing due: §5.5, "only when they buy something")
	if target == 0 {
		u.mu.Lock()
		plan, _ := u.status["plan"].(string)
		u.mu.Unlock()
		if plan == "" {
			u.set("plan", "none: nothing asks for a header")
		}
		return nil
	}
	if target > head.Height {
		u.set("plan", fmt.Sprintf("target %d awaits its block (head %d)", target, head.Height))
		return nil
	}
	if !prove && target <= T {
		u.set("plan", fmt.Sprintf("target %d is at or below the trusted height %d", target, T))
		return nil
	}
	// the spacing counts this updater's own updates: a stranger's, every 119 seconds, would otherwise hold ours for good
	if last := u.state.LastSubmitAt; !last.IsZero() {
		if gap := now.Sub(last); gap < u.MinGap {
			u.set("plan", fmt.Sprintf("target %d (%s) waits the 2-minute spacing: %s since this updater's last update", target, reason, gap.Round(time.Second)))
			return nil
		}
	}
	decision := false
	for _, t := range served {
		decision = decision || t.kind == kindDecision
	}
	if ok, why := u.mayUpdate(now, false, decision); !ok {
		u.set("plan", fmt.Sprintf("target %d (%s) waits: %s", target, reason, why))
		return nil
	}
	var p *updatePlan
	if prove {
		p, err = planProve(ctx, u.gno, cs, target)
	} else {
		p, err = planUpdate(ctx, u.gno, cs, target)
	}
	if err != nil {
		u.Alarm.Alarm("updater:plan", err.Error())
		return nil
	}
	p.Decision = decision
	if p.Announcing {
		emergency, reason = true, "the announcing header before "+reason
	}
	u.set("plan", fmt.Sprintf("target %d (announcing %v, a header proof %v), reason: %s", p.Target, p.Announcing, p.Prove, reason))
	// the booking stands in the state file before the update is sent (E3-GS5): an update that outlives the sender's
	// wait, or a stop before this step's save, is booked once the chain shows it recorded
	if p.Target == target {
		if err := u.pend(ctx, latest, cs, p, served, emergency, draw, reason); err != nil {
			return err
		}
	}
	rcs, err := u.submit(ctx, cs, p, emergency, reason)
	u.markIdle(u.now()) // a send refused, or mined, leaves nothing of this key in flight from now (E5-GS3)
	if err == nil {
		u.state.Out.noteHeader(p.Target, cs.Epoch) // its events are read every step from now on (E5-GS4)
	}
	if errors.Is(err, errOvertaken) || errors.Is(err, errWaits) {
		// a header at or above it landed first, or nothing was sent: the next step's resolveBookings settles its booking
		// by the chain's word — dropped when this key cannot record it any more, booked when an earlier transaction of
		// this key (one the sender's wait outlived) recorded it after all
		return nil
	}
	if err != nil {
		return err
	}
	if p.Target == target {
		u.settleBooking(ctx, cs, p.Target, rcs)
	}
	return nil
}

// scanBehindAlarm is how long the event scan may stay behind the head before updater:scan alarms (a restart after a gap
// catches up scanPerTick blocks a step: minutes).
const scanBehindAlarm = 10 * time.Minute

// scanReached keeps out of the plan, while the event scan is behind the head, the route sends whose newest event lies
// above it (E5-GS6): a send whose block the scan has not read is priced at no leaf — the model's least delivery, one
// Dispatch and no bytes — so a header bought on that books it at no fee check, and the relayer then delivers it from
// whatever leaf it has (up to MaxLeaf of a stranger's events). After a gap (a restart, a node down) the scan reads
// scanPerTick blocks a step, so they wait a few steps; the sends the scan has read, and decisions (never priced), do not
// wait — one block the node does not serve holds only what lies above it (the review of the lane, R7). A scan behind for
// scanBehindAlarm alarms updater:scan.
func (u *Updater) scanReached(waiting []*outMsg, head int64, now time.Time) ([]*outMsg, string) {
	scanned := u.state.Out.Index.Scanned
	if scanned >= head {
		u.scanBehind = time.Time{}
		u.Alarm.Clear("updater:scan")
		return waiting, ""
	}
	if u.scanBehind.IsZero() {
		u.scanBehind = now
	}
	var out []*outMsg
	held := 0
	for _, t := range waiting {
		if t.kind == kindRoute && max(t.latest(), t.msg.Height) > scanned {
			held++
			continue
		}
		out = append(out, t)
	}
	why := fmt.Sprintf("the event scan reads to %d of the head %d: %d route send(s) above it wait for it (a send's leaf it has not read would be priced at the least delivery, E5-GS6)", scanned, head, held)
	if now.Sub(u.scanBehind) >= scanBehindAlarm {
		u.Alarm.Alarm("updater:scan", fmt.Sprintf("%s — behind for %s: the gno node's blocks or results do not read", why, now.Sub(u.scanBehind).Round(time.Minute)))
	}
	return out, why
}

// waitCand is a group the updater may serve: by an update, or by a header proof (an Old group: E3-RT4).
type waitCand struct {
	waitGroup
	prove bool
}

// pend makes a header's booking before its update is sent (budget.go; E3-GS5), kept in the state file — saved here,
// before any wait — until the chain shows the header recorded (settleBooking, or resolveBookings at a later step or
// after a restart): every route send the header serves is named in the trailer its calls carry (p.Book: the (nonce,
// header) pairs the relayer delivers at no fee check, E4-GS2, E4-GS4), each send not booked before credited its own fee
// and debited its delivery's model cost at the price now (a send booked once is never credited or debited again), and
// the header's model cost, which the pending booking holds back from any other six-hour draw until its receipts are
// read. A decision's or a validator change's header (the emergency cap, outside the budget) books no route send: one
// in its block rides it only by its own fee (E4-GS2 — else a stranger's fee-short sends beside any decision would be
// delivered at no fee check).
func (u *Updater) pend(ctx context.Context, latest *ethx.Block, cs *ethx.ClientState, p *updatePlan, served []*outMsg, emergency, draw bool, reason string) error {
	p.Book = nil
	var sends []*outMsg
	for _, t := range served {
		if t.kind == kindRoute && !emergency {
			sends = append(sends, t)
		}
	}
	if len(sends) == 0 {
		u.state.Budget.drop(p.Target)
		return nil // a decision's or a validator change's header: nothing to book
	}
	sort.Slice(sends, func(i, j int) bool { return sends[i].n < sends[j].n })
	leaf := func(t *outMsg) (int, int) { return t.leaf(p.Target - 1) }
	amounts := map[string]sendAmounts{}
	var nonces []uint32
	for _, t := range sends {
		nonces = append(nonces, t.n)
		if u.state.Budget.settled(t.n) {
			continue // booked once already: its fee and its delivery are in the ledger
		}
		f, err := u.env.dueFacts(ctx, u.Sender, latest, []*outMsg{t}, u.now(), 0, leaf)
		if err != nil {
			return fmt.Errorf("the budget could not book header %d (no header is bought unbooked): %w", p.Target, err)
		}
		amounts[t.nk()] = sendAmounts{Fee: f.FeesWei.String(), Delivery: f.DeliveriesWei.String()}
	}
	price, err := u.env.price(ctx, u.Sender, latest)
	if err != nil {
		return fmt.Errorf("the budget could not book header %d (no header is bought unbooked): %w", p.Target, err)
	}
	why := "bought by its sends' fees"
	if draw {
		why = "a six-hour send's header, drawn on the budget"
	}
	blk, err := u.gno.BlockAt(ctx, p.Target)
	if err != nil {
		return err
	}
	u.state.Budget.pend(&pendingBooking{Header: p.Target, Hash: hex.EncodeToString(blk.Hash[:]), Prove: p.Prove, Epoch: cs.Epoch, Why: why, Reason: reason,
		Nonces: nonces, Sends: amounts, HeaderCost: weiOf(u.env.headerGasAt(ctx, p.Target), price).String(), At: u.now().UTC()})
	p.Book = nonces
	return u.save()
}

// settleBooking books the pending booking of a header this step's submission recorded (settleRecorded): this step's
// receipt that carries the header's HeaderRecorded log names this key's recording transaction, kept with the booking
// (E5-GS2: the booking settles from it, never only from a log read); its cost the receipts of every transaction of this
// key that carried the header (a stage's earlier calls too: headerCost), this step's receipts when the logs do not show
// them; a recording not read yet leaves it pending for the next step.
func (u *Updater) settleBooking(ctx context.Context, cs *ethx.ClientState, header int64, rcs []*types.Receipt) {
	pb := u.state.Budget.pendingAt(header)
	if pb == nil {
		return
	}
	if hash, err := hl.ParseKey32(pb.Hash); err == nil {
		for _, rc := range rcs {
			if _, ok := recordingOf(rc, Address(u.Cfg.Eth.Client), hash); ok && rc.BlockNumber != nil {
				pb.Tx, pb.Block = rc.TxHash.Hex(), rc.BlockNumber.Uint64()
			}
		}
	}
	u.settleRecorded(ctx, pb, rcs)
}

// recordingOf says whether a receipt is a successful transaction whose logs record a header on the client (its
// HeaderRecorded log), and at which epoch.
func recordingOf(rc *types.Receipt, client common.Address, hash [32]byte) (uint64, bool) {
	if rc == nil || rc.Status != types.ReceiptStatusSuccessful {
		return 0, false
	}
	for _, l := range rc.Logs {
		if l == nil || l.Address != client {
			continue
		}
		if ev, err := ethx.DecodeHeaderRecorded(*l); err == nil && ev.Hash == common.Hash(hash) {
			return ev.Epoch, true
		}
	}
	return 0, false
}

// ownRecording reads a pending booking's own recording transaction (pb.Tx; E5-GS2): its receipt — this step's, else
// read by hash — a success whose HeaderRecorded log records the header, and its calldata, signed by this key, a call
// of the client carrying the header: the trailer it names and the epoch it was recorded at. false when the
// transaction is not found, failed, or recorded nothing (overtaken; a reorganisation dropped it): the chain's log
// decides then.
func (u *Updater) ownRecording(ctx context.Context, pb *pendingBooking, hash [32]byte, rcs []*types.Receipt) ([]uint32, uint64, bool) {
	txh := common.HexToHash(pb.Tx)
	client := Address(u.Cfg.Eth.Client)
	var rc *types.Receipt
	for _, r := range rcs {
		if r != nil && r.TxHash == txh {
			rc = r
		}
	}
	if rc == nil {
		var err error
		if rc, err = u.Eth.Receipt(ctx, txh); err != nil {
			return nil, 0, false
		}
	}
	epoch, ok := recordingOf(rc, client, hash)
	if !ok {
		return nil, 0, false
	}
	tx, err := u.Eth.SignedTransaction(ctx, txh)
	if err != nil || tx.From != u.Sender.Address() || tx.To == nil || *tx.To != client {
		return nil, 0, false
	}
	if _, h, _, _, err := ethx.HeaderCallOf(tx.Input); err != nil || h != hash {
		return nil, 0, false
	}
	trailer, _ := trailerNonces(tx.Input)
	return trailer, epoch, true
}

// settleRecorded books a pending booking whose header the client recorded (E3-GS5, E4-GS2): from this key's own
// recording transaction when the booking names one (ownRecording, E5-GS2: no log read, which a provider that keeps no
// three days of logs cannot answer), else by the chain's word (bookingOf: the HeaderRecorded log, its transaction's
// signer and trailer, the relayer's reading) — the sends the trailer names, at the amounts the booking kept; recorded by
// another key's transaction, it is dropped (that header books nothing). It answers whether the booking ended (booked
// or dropped); until then it stands.
func (u *Updater) settleRecorded(ctx context.Context, pb *pendingBooking, rcs []*types.Receipt) bool {
	hash, err := hl.ParseKey32(pb.Hash)
	if err != nil {
		u.state.Budget.drop(pb.Header)
		return true
	}
	if pb.Tx != "" {
		if trailer, epoch, ok := u.ownRecording(ctx, pb, hash, rcs); ok {
			// its own view of the header: ours, the trailer's sends — provisional until the finalized block passes it,
			// read again as any booking is (E5-GS3), so no log read is needed to judge its sends booked (E5-GS2)
			b := &headerBook{Height: pb.Header, Epoch: epoch, Ours: true, Nonces: trailer, Tx: pb.Tx}
			u.env.keepBooking(ctx, u.now(), pb.Hash, hash, b, pb.Block, 0)
			u.bookPending(ctx, pb, trailer, rcs)
			return true
		}
	}
	es, err := ethx.ReadEntries(ctx, u.Eth, Address(u.Cfg.Eth.Client), [][32]byte{hash}, ethx.Latest)
	if err != nil || len(es) != 1 || es[0].Height == 0 {
		return false
	}
	b := u.env.bookingOf(ctx, u.now(), hash, es[0].Epoch, pb.Header)
	switch {
	case b == nil:
		return false
	case !b.Ours:
		log.Printf("updater: header %d was recorded by another key: its pending booking dropped (%s)", pb.Header, pb.Why)
		u.state.Budget.drop(pb.Header)
	default:
		u.bookPending(ctx, pb, b.Nonces, rcs)
	}
	return true
}

// resolveBookings settles the bookings a stopped wait or a restart left pending (E3-GS5): each booked once its header
// is recorded by this key — from its own recording transaction (E5-GS2), else the chain's log — and dropped when another
// key recorded it, or on the finalized block's word alone (E5-GS3: one read at latest answered by a node a block behind
// once dropped a header this key had just recorded, and the relayer delivered what the ledger never booked): the
// header not recorded there and either the client's epoch there past the booking's, or (an update) its trusted height
// there at or above the header, or nothing of this key in flight since a moment the finalized block's time has passed
// while no step has asked for the header for bookingStale (each attempt makes it again). A recorded header whose
// recording is not read is never dropped (E5-GS2: dropped after an hour, a fee-short send's header was bought again
// every hour, none booked): bookings:unread alarms it past an hour, and it stands, its draw held back from the budget.
func (u *Updater) resolveBookings(ctx context.Context) {
	now := u.now()
	u.markIdle(now)
	client := Address(u.Cfg.Eth.Client)
	var unread []string
	var fin *ethx.Block
	var finTrusted int64
	var finEpoch uint64
	finOK, finRead := false, false
	for _, pb := range append([]*pendingBooking{}, u.state.Budget.Pending...) {
		hash, err := hl.ParseKey32(pb.Hash)
		if err != nil {
			u.state.Budget.drop(pb.Header)
			continue
		}
		es, err := ethx.ReadEntries(ctx, u.Eth, client, [][32]byte{hash}, ethx.Latest)
		recorded := err == nil && len(es) == 1 && es[0].Height != 0
		if recorded || pb.Tx != "" {
			if u.settleRecorded(ctx, pb, nil) {
				continue
			}
			if recorded && now.Sub(pb.At) > time.Hour {
				unread = append(unread, fmt.Sprintf("header %d (%s)", pb.Header, pb.Why))
			}
			if recorded {
				continue
			}
		}
		// not recorded at latest, as one read says: ended only on the finalized block's word
		if !finRead {
			finRead = true
			if fin = u.env.finalized(ctx); fin != nil {
				if finTrusted, finEpoch, err = ethx.ReadTrust(ctx, u.Eth, client, ethx.AtHash(fin.Hash)); err == nil {
					finOK = true
				}
			}
		}
		if !finOK {
			continue // no verdict: it stands
		}
		ef, err := ethx.ReadEntries(ctx, u.Eth, client, [][32]byte{hash}, ethx.AtHash(fin.Hash))
		if err != nil || len(ef) != 1 || ef[0].Height != 0 {
			continue // no verdict, or recorded there (the read at latest lagged): settled at a later step
		}
		switch {
		case finEpoch > pb.Epoch || !pb.Prove && finTrusted >= pb.Header:
			log.Printf("updater: header %d can no longer be recorded by this key (the client trusts %d, epoch %d, at the finalized block %d): its pending booking dropped", pb.Header, finTrusted, finEpoch, fin.Number)
			u.state.Budget.drop(pb.Header)
		case !pb.IdleAt.IsZero() && fin.Time >= uint64(pb.IdleAt.Add(idleMargin).Unix()) && now.Sub(pb.At) > bookingStale:
			log.Printf("updater: header %d is not recorded at the finalized block %d, whose time is past the moment nothing of this key was in flight (%s), and no step has asked for it for %s: its pending booking dropped (%s)", pb.Header, fin.Number, pb.IdleAt.UTC().Format(time.RFC3339), now.Sub(pb.At).Round(time.Minute), pb.Why)
			u.state.Budget.drop(pb.Header)
		}
	}
	if len(unread) > 0 {
		u.Alarm.Alarm("bookings:unread", fmt.Sprintf("%s recorded on the client for over an hour, its recording transaction not read (no receipt of this key's, no log found): the booking stands — never dropped and bought again — its draw held back from the budget, until the chain's word is read (a provider that serves the logs and receipts; E5-GS2)", strings.Join(unread, "; ")))
	} else {
		u.Alarm.Clear("bookings:unread")
	}
}

// idleMargin is how far past the moment nothing of this key was in flight the finalized block's time must lie before
// its word on a pending header ends the booking (the machine's clock against the chain's).
const idleMargin = 2 * time.Minute

// markIdle notes on each pending booking the first moment this key was seen with nothing in flight — after its sends
// for the header mined, or none was sent — and forgets it while something is in flight (E5-GS3: the finalized block's
// word ends a booking only once that block's time is past it, so the block holds whatever this key sent).
func (u *Updater) markIdle(now time.Time) {
	idle := u.Sender.InFlight() == ""
	for _, pb := range u.state.Budget.Pending {
		switch {
		case !idle:
			pb.IdleAt = time.Time{}
		case pb.IdleAt.IsZero():
			pb.IdleAt = now
		}
	}
}

// bookingStale is how long a pending booking stands with nothing of the key in flight and no step asking for its header
// again: a refusal sent nothing, and a group served otherwise asks for nothing.
const bookingStale = 15 * time.Minute

// bookPending enters a pending booking into the ledger — the sends the recording transaction's trailer names, each not
// booked before credited its fee and debited its delivery (amountsOf), and the header's cost from receipts
// (headerCost) — and clears it.
func (u *Updater) bookPending(ctx context.Context, pb *pendingBooking, trailer []uint32, rcs []*types.Receipt) {
	var cost *big.Int
	if pb.HeaderCost != "" {
		hash, _ := hl.ParseKey32(pb.Hash)
		var ok bool
		if cost, ok = u.headerCost(ctx, hash); !ok {
			cost = new(big.Int)
			for _, rc := range rcs {
				if rc.EffectiveGasPrice != nil {
					cost.Add(cost, weiOf(rc.GasUsed, rc.EffectiveGasPrice))
				}
			}
			if cost.Sign() == 0 {
				cost = bigOf(pb.HeaderCost) // no receipt read: the model's cost, which sits above every measurement
			}
		}
	}
	fees, deliveries := u.state.Budget.amountsOf(pb, trailer)
	u.state.Budget.drop(pb.Header)
	u.state.Budget.book(u.now(), pb.Header, pb.Why+": "+pb.Reason, trailer, fees, deliveries, cost)
	u.set("budget", u.state.Budget.text())
	log.Printf("updater: header %d booked (%s): %d send(s), fees %s ETH, deliveries %s ETH, header %s ETH; the budget's %s", pb.Header, pb.Why, len(trailer), eth(fees), eth(deliveries), eth(cost), u.state.Budget.text())
}

// headerCost is what this key paid for a header: the receipts of its transactions that carried it — the one whose
// HeaderRecorded log recorded it and every staging call its StageProgress logs name (a stage's calls span steps,
// E3-GS5) — over the last day; false when none is found.
func (u *Updater) headerCost(ctx context.Context, hash [32]byte) (*big.Int, bool) {
	latest, err := u.Eth.BlockAt(ctx, ethx.Latest)
	if err != nil {
		return nil, false
	}
	from := uint64(0)
	if latest.Number > stageBack {
		from = latest.Number - stageBack
	}
	client := Address(u.Cfg.Eth.Client)
	me := u.Sender.Address()
	recorded, err := u.Eth.LogsRange(ctx, ethx.LogQuery{Addresses: []common.Address{client}, Topics: [][]common.Hash{{ethx.LightClient.Event("HeaderRecorded")}, {ethx.HashTopic(hash)}}}, from, latest.Number)
	if err != nil {
		return nil, false
	}
	staged, err := u.Eth.LogsRange(ctx, ethx.LogQuery{Addresses: []common.Address{client}, Topics: [][]common.Hash{{ethx.LightClient.Event("StageProgress")}, nil, {ethx.AddressTopic(me)}}}, from, latest.Number)
	if err != nil {
		return nil, false
	}
	txs := map[common.Hash]bool{}
	for _, l := range recorded {
		txs[l.TxHash] = true
	}
	for _, l := range staged {
		if sp, err := ethx.DecodeStageProgress(l); err == nil && sp.HeaderHash == hash && sp.Submitter == me {
			txs[l.TxHash] = true
		}
	}
	cost, found := new(big.Int), false
	for h := range txs {
		tx, err := u.Eth.Transaction(ctx, h)
		if err != nil || tx.From != me {
			continue
		}
		rc, err := u.Eth.Receipt(ctx, h)
		if err != nil || rc.EffectiveGasPrice == nil {
			continue
		}
		cost.Add(cost, weiOf(rc.GasUsed, rc.EffectiveGasPrice))
		found = true
	}
	return cost, found
}

// deliveryAge alarms delivery:age while a route send has waited DeliveryAgeAfter (seven hours) for its delivery —
// its dispatch block's time on gno.land against now, whoever's updates kept the client fresh (§6.1). Both machines
// judge it over the sends that wait in their own view (E5-GS3: a send the updater sees booked and the relayer holds
// waits in the relayer's view alone).
func (e *outEnv) deliveryAge(ctx context.Context, waiting []*outMsg, now time.Time) {
	var oldest *outMsg
	var at time.Time
	for _, t := range waiting {
		if t.kind != kindRoute {
			continue
		}
		if when, ok := e.blockTime(ctx, t.msg.Height); ok && (oldest == nil || when.Before(at)) {
			oldest, at = t, when
		}
	}
	if oldest != nil && now.Sub(at) >= DeliveryAgeAfter {
		e.alarm.Alarm("delivery:age", fmt.Sprintf("gno #%d (%s) has waited %s for its delivery on Ethereum (dispatched at height %d): neither its fees nor, past six hours, the budget have covered its header — it waits for one of them: its fees (a lower base fee, more sends to share the header) or the six-hour draw (a budget grown by later sends' surplus); the operator may buy its header outside the budget (the client's update is open to anyone)", oldest.n, kindName(oldest), now.Sub(at).Round(time.Minute), oldest.msg.Height))
		return
	}
	e.alarm.Clear("delivery:age")
}

// floatFloor alarms float:updater while the updater's key holds less than a decision's header needs at the emergency
// cap — ≈0.174 ETH at gnoland-1's set, priced at the set after the trusted height; its route headers are spent on top of
// it (§5.5, §11.4; E2-RT8, E4-FC5) — and says the base fee up to which it still sends one.
func (u *Updater) floatFloor(ctx context.Context) string {
	bal, err := u.Sender.Balance(ctx)
	if err != nil {
		return "the balance could not be read: " + err.Error()
	}
	// the header a decision needs next is signed by the set after the trusted height: priced at its size, never at
	// six validators whatever the set (E3-GS11)
	next := int64(0) // before a step has read the client: the model's gnoland-1 set
	if u.trusted > 0 {
		next = u.trusted + 1
	}
	text, short := updaterFloatText(u.Sender.Address().Hex(), bal, u.env.headerGasAt(ctx, next), u.Cfg.EmergencyMaxBaseFee())
	if short {
		u.Alarm.Alarm("float:updater", text)
	} else {
		u.Alarm.Clear("float:updater")
	}
	return text
}

// attemptOf is the failures in a row of the announcing header's updates, of a decision's headers, or of the others':
// a park the route headers earn never holds a validator change or a decision (E3-GS3).
func (u *Updater) attemptOf(announcing, decision bool) **attempt {
	switch {
	case announcing:
		return &u.state.AnnAttempt
	case decision:
		return &u.state.DecAttempt
	}
	return &u.state.Attempt
}

// mayUpdate says whether an update may be sent now: not while the last failure's backoff runs.
func (u *Updater) mayUpdate(now time.Time, announcing, decision bool) (bool, string) {
	a := *u.attemptOf(announcing, decision)
	if a.due(now) {
		return true, ""
	}
	return false, fmt.Sprintf("the last update failed (%s): tried again at %s", a.LastErr, a.Next.UTC().Format(time.RFC3339))
}

// failed records an update's failure (deterministic: it spent gas — mined and reverted, or every call went and the
// client does not trust the header).
func (u *Updater) failed(err error, deterministic bool, p *updatePlan) {
	ap := u.attemptOf(p.Announcing, p.Decision)
	if *ap == nil {
		*ap = &attempt{}
	}
	if (*ap).fail(u.now(), err, deterministic) {
		u.Alarm.Alarm(u.alarmPrefix+":parked", fmt.Sprintf("the updates failed %d times in a row (%v): tried hourly from now on", parkAfter, err))
	}
}

// decisionsFirst orders the waiting blocks for the split path: the blocks holding a decision first (ER6: a
// decision's header never waits behind a route header the base fee parks), then the others, each oldest first.
func decisionsFirst(gs []waitCand) []waitCand {
	var first, rest []waitCand
	for _, g := range gs {
		dec := false
		for _, t := range g.Msgs {
			dec = dec || t.kind == kindDecision
		}
		if dec {
			first = append(first, g)
		} else {
			rest = append(rest, g)
		}
	}
	return append(first, rest...)
}

// setBound alarms client:set-bound when the set at the trusted height reaches SetSizeWarn validators.
func (u *Updater) setBound(ctx context.Context, T int64) {
	n, ok := u.sets[T]
	if !ok {
		s, err := u.gno.ValidatorsAt(ctx, T+1)
		if err != nil {
			return
		}
		n = len(s.Validators)
		u.sets = map[int64]int{T: n}
	}
	if n >= SetSizeWarn {
		u.Alarm.Alarm("client:set-bound", fmt.Sprintf("gno.land's set has %d validators: an update stages more calls and costs more with each (the client's bound is 255)", n))
		return
	}
	u.Alarm.Clear("client:set-bound")
}

// updatePlan is the header to submit and, when it announces a change, the set it announces; Prove a header proof of a
// header at or below the trusted height (proveHeader, E3-RT4) instead of an update; Decision a header a decision waits
// for (its failures counted apart, E3-GS3).
type updatePlan struct {
	Target     int64
	Announcing bool
	Prove      bool
	Decision   bool
	NextSet    []gnolight.Validator
	Expect     [32]byte
	// Book are the route sends the header is bought for (pend): every call of it ends with their trailer
	// (bookingTrailer), the (nonce, header) pairs the relayer reads back (E4-GS2)
	Book []uint32
}

// planProve is the header proof of an old header (E3-RT4): the header at target, at or below the trusted height,
// signed by the client's current set — proveHeader refuses any other, and no later update can pre-empt it, so a
// stranger who keeps updating the client past every block the relayer re-emits into strands nothing.
func planProve(ctx context.Context, src gnoside.Light, cs *ethx.ClientState, target int64) (*updatePlan, error) {
	if target > cs.TrustedHeight {
		return nil, fmt.Errorf("header %d is above the trusted height %d: an update, not a header proof", target, cs.TrustedHeight)
	}
	blk, err := src.BlockAt(ctx, target)
	if err != nil {
		return nil, err
	}
	if blk.Header.ValidatorsHashArray() != cs.SetHash {
		return nil, fmt.Errorf("header %d is not signed by the client's current set: no header proof takes it (its messages are re-emitted)", target)
	}
	return &updatePlan{Target: target, Prove: true, Expect: cs.SetHash}, nil
}

// planUpdate chooses the header the client can take toward upTo (§6.1): upTo itself when its set is the one the
// client expects next (the current set, or the announced one once it waits for it); otherwise the first header
// whose NextValidatorsHash leaves that set — the announcing header, presented with the set it announces — found
// by a binary search over the sets by height.
func planUpdate(ctx context.Context, src gnoside.Light, cs *ethx.ClientState, upTo int64) (*updatePlan, error) {
	T := cs.TrustedHeight
	expect := cs.Expect()
	hashAt := func(h int64) ([32]byte, error) {
		s, err := src.ValidatorsAt(ctx, h)
		if err != nil {
			return [32]byte{}, err
		}
		hash, _ := s.Hash()
		return hash, nil
	}
	vh, err := hashAt(upTo)
	if err != nil {
		return nil, err
	}
	target := upTo
	if vh != expect {
		x, err := firstDiffering(T+1, upTo, expect, hashAt)
		if err != nil {
			return nil, err
		}
		target = x - 1
		if target <= T {
			return nil, fmt.Errorf("the set changed at height %d, right after the trusted header %d, which announced no change: the client cannot follow it, and it has no reset (§6.4: the deployment's ending)", x, T)
		}
	}
	blk, err := src.BlockAt(ctx, target)
	if err != nil {
		return nil, err
	}
	if blk.Header.ValidatorsHashArray() != expect {
		return nil, fmt.Errorf("header %d's ValidatorsHash is not the set the client expects", target)
	}
	plan := &updatePlan{Target: target, Expect: expect}
	if blk.Header.Announcing() {
		next, err := src.ValidatorsAt(ctx, target+1)
		if err != nil {
			return nil, fmt.Errorf("the set header %d announces: %w", target, err)
		}
		if nh, _ := next.Hash(); nh != blk.Header.NextValidatorsHashArray() {
			return nil, fmt.Errorf("the validators at %d do not hash to header %d's NextValidatorsHash", target+1, target)
		}
		plan.Announcing, plan.NextSet = true, next.Validators
	}
	return plan, nil
}

// firstDiffering is the smallest height in [lo, hi] whose set hash is not expect, by binary search, checked against
// its predecessor (a linear scan from lo if the sets were not monotone).
func firstDiffering(lo, hi int64, expect [32]byte, hashAt func(int64) ([32]byte, error)) (int64, error) {
	l, h := lo, hi
	for l < h {
		m := l + (h-l)/2
		mh, err := hashAt(m)
		if err != nil {
			return 0, err
		}
		if mh == expect {
			l = m + 1
		} else {
			h = m
		}
	}
	lh, err := hashAt(l)
	if err != nil {
		return 0, err
	}
	monotone := lh != expect
	if monotone && l > lo {
		ph, err := hashAt(l - 1)
		if err != nil {
			return 0, err
		}
		monotone = ph == expect
	}
	if monotone {
		return l, nil
	}
	for x := lo; x <= hi; x++ {
		xh, err := hashAt(x)
		if err != nil {
			return 0, err
		}
		if xh != expect {
			return x, nil
		}
	}
	return 0, fmt.Errorf("no height in %d..%d leaves the expected set", lo, hi)
}

// submit builds and sends the update of a plan — or its header proof (p.Prove) — one call, or a staged series whose
// calls an earlier stop already mined are left out (read back from the chain). It answers the receipts of the calls it
// sent; errWaits when it sent nothing and waits (a pending staging call, a base fee above the cap); errOvertaken when
// another submitter's update landed first (no failure: the next step plans from the client as it stands).
func (u *Updater) submit(ctx context.Context, cs *ethx.ClientState, p *updatePlan, emergency bool, reason string) ([]*types.Receipt, error) {
	var next []gnolight.Validator
	if !p.Prove {
		next = p.NextSet
	}
	// the header is hashed from its fields as the client hashes it and held to the node's hash of the block before
	// anything is sent: a difference is gno.land's encoding changed under the client (E3-AS3), alarmed gno:encoding
	sh, err := signedHeader(ctx, u.gno, u.Cfg.Gno.ChainID, p.Target, next)
	if err != nil {
		alarmEncoding(u.Alarm, err)
		return nil, err
	}
	u.Alarm.Clear("gno:encoding")
	if sh.Header.ValidatorsHashArray() != p.Expect {
		return nil, fmt.Errorf("header %d is not signed by the set the client expects", p.Target)
	}
	if !p.Prove && p.Announcing != sh.Header.Announcing() {
		return nil, fmt.Errorf("header %d: the plan and the header disagree on whether it announces", p.Target)
	}
	what := "update"
	if p.Prove {
		what = "proveHeader"
	}
	if p.Announcing {
		// a key of either set that anyone can sign for is alarmed before the client takes the change (E2-ED3); the
		// header goes all the same: the client follows what gno.land's validators sign
		smallOrderBeforeAnnouncing(u.Alarm, sh)
	}
	slots, err := ethx.ChoosePrecommits(u.Cfg.Gno.ChainID, p.Target, sh.Set, sh.Commit)
	if err != nil {
		return nil, err
	}
	bound := u.state.SigsBound
	if bound == 0 || (u.Cfg.Eth.MaxSigsPerCall > 0 && u.Cfg.Eth.MaxSigsPerCall < bound) {
		bound = u.Cfg.Eth.MaxSigsPerCall
	}
	hash := sh.Header.Hash()
	if bound > 0 && len(slots) > bound {
		// a staging call still pending is resolved first, so the chain's record of the stage is whole
		if err := u.Sender.Settle(ctx); err != nil {
			if errors.Is(err, ethx.ErrStillPending) {
				u.set("plan", fmt.Sprintf("target %d waits for its pending staging call", p.Target))
				return nil, errWaits
			}
			return nil, err
		}
		// the stage this key began for the header under the client's current epoch and expected set (a stage begun
		// under another is orphaned: its slots count nothing)
		key, err := ethx.StageKey(u.Sender.Address(), sh, cs.Epoch, p.Expect)
		var staged map[int]bool
		if err == nil {
			staged, err = u.stagedSlots(ctx, hash, key)
		}
		if err != nil {
			log.Printf("updater: header %d's earlier stage could not be read back (sent whole): %v", p.Target, err)
		}
		var rest []int
		for _, s := range slots {
			if !staged[s] {
				rest = append(rest, s)
			}
		}
		switch {
		case len(rest) == 0:
			// every chosen slot staged and the header still not trusted: the record is not the client's — sent whole
			log.Printf("updater: header %d's stage reads as complete but the client does not trust it: sent whole", p.Target)
		case len(rest) < len(slots):
			log.Printf("updater: header %d resumes its stage: %d of %d slots staged already", p.Target, len(slots)-len(rest), len(slots))
			slots = rest
		}
	}
	var calls []ethx.HeaderCall
	if p.Prove {
		calls, err = ethx.ProveHeaderCallsFor(sh, slots, bound)
	} else {
		calls, err = ethx.UpdateCallsFor(sh, slots, bound)
	}
	if err != nil {
		return nil, err
	}
	var last *types.Receipt
	var rcs []*types.Receipt
	trailer := bookingTrailer(p.Book)
	for i, c := range calls {
		call := ethx.Call{To: Address(u.Cfg.Eth.Client), Data: append(append([]byte{}, c.Data...), trailer...), Label: fmt.Sprintf("%s %d (%d/%d)", what, p.Target, i+1, len(calls))}
		if emergency {
			call.MaxBaseFee = u.Cfg.EmergencyMaxBaseFee()
		}
		rc, err := u.Sender.Send(ctx, call)
		if err == nil {
			last = rc
			rcs = append(rcs, rc)
		}
		if rv, ok := ethx.AsRevert(err); ok && !rv.Mined && rv.Named("NoNewSignature") {
			// the client has every signature this call carries counted already (fold F4: a call that adds none is
			// refused, and the estimate said so): nothing was sent, nothing is owed — the next call carries the rest
			log.Printf("updater: header %d, call %d of %d: every signature it carries is counted already (NoNewSignature): not sent", p.Target, i+1, len(calls))
			continue
		}
		if err != nil {
			if u.overtaken(ctx, p, rc, err) {
				// another submitter's update to this height or above landed first (E3-GS3): not this updater's
				// failure — nothing counted, no park, decisions' headers included; the header it bought serves what it
				// proves, and the messages left below the trusted height are proven by a header proof (E3-RT4). A
				// mined one was this key's transaction all the same: the spacing between its own updates runs from it
				if rv, _ := ethx.AsRevert(err); rv != nil && rv.Mined {
					u.state.LastSubmitAt = u.now()
				}
				u.set("plan", fmt.Sprintf("target %d (%s) was overtaken: another submitter's update landed first (%v); planned again from the client as it stands", p.Target, reason, err))
				log.Printf("%s: the update to header %d was overtaken by another submitter's: %v", u.alarmPrefix, p.Target, err)
				return rcs, errOvertaken
			}
			if errors.Is(err, ethx.ErrGasCap) && len(c.Slots) > 1 {
				// no failure counted: the next step stages it at half as many signatures a call
				nb := max(len(c.Slots)/2, 1)
				u.state.SigsBound = nb
				u.Alarm.Alarm(u.alarmPrefix+":staged", fmt.Sprintf("an update call of %d signatures does not fit the transaction gas cap with its margin: staged at %d a call from now on", len(c.Slots), nb))
				return rcs, err
			}
			var ft *ethx.FeeTooHigh
			if errors.As(err, &ft) {
				u.set("plan", fmt.Sprintf("target %d waits: %v", p.Target, ft))
				return rcs, errWaits
			}
			rv, refused := ethx.AsRevert(err)
			if refused {
				u.Alarm.Alarm(u.alarmPrefix+":refused", fmt.Sprintf("the %s of header %d was refused: %v", what, p.Target, err))
			}
			// a revert that spent gas (mined), or a call no transaction can carry, counts toward the park; a
			// refusal the simulation answered spent nothing and is retried in a minute
			deterministic := refused && rv.Mined || errors.Is(err, ethx.ErrGasCap)
			err = fmt.Errorf("%s %d (%s), call %d of %d: %w", what, p.Target, reason, i+1, len(calls), err)
			u.failed(err, deterministic, p)
			return rcs, err
		}
	}
	u.Alarm.Clear(u.alarmPrefix + ":refused")
	// the client read at the last call's own block (a provider behind a balancer may answer latest from a node a block
	// behind, where the update has not landed yet)
	at := ethx.Latest
	if last != nil && last.BlockHash != (common.Hash{}) {
		at = ethx.AtHash(last.BlockHash)
	}
	if done, have, err := u.recorded(ctx, p, sh.Header.Hash(), at); err == nil && !done {
		u.Alarm.Alarm(u.alarmPrefix+":unfinished", fmt.Sprintf("the %s of header %d went through every call but the client %s: its stage did not cross 2/3 (read back next step)", what, p.Target, have))
		err := fmt.Errorf("%s %d: every call sent, the client %s", what, p.Target, have)
		u.failed(err, true, p)
		return rcs, err
	}
	u.Alarm.Clear(u.alarmPrefix + ":unfinished")
	u.Alarm.Clear(u.alarmPrefix + ":parked")
	*u.attemptOf(p.Announcing, p.Decision) = nil
	now := u.now()
	u.state.LastSubmit, u.state.LastSubmitAt, u.state.Submits = p.Target, now, u.state.Submits+1
	if p.Announcing {
		u.state.Armed = 0
	}
	log.Printf("%s: header %d accepted (%s)", u.alarmPrefix, p.Target, reason)
	return rcs, nil
}

// recorded says whether the client holds a submitted header at a block: an update's height trusted (or passed), a
// header proof's entry at the client's epoch; have says what it holds otherwise.
func (u *Updater) recorded(ctx context.Context, p *updatePlan, hash [32]byte, at ethx.At) (done bool, have string, err error) {
	after, err := ethx.ReadClient(ctx, u.Eth, Address(u.Cfg.Eth.Client), at)
	if err != nil {
		return false, "", err
	}
	if !p.Prove {
		return after.TrustedHeight >= p.Target, fmt.Sprintf("trusts %d", after.TrustedHeight), nil
	}
	es, err := ethx.ReadEntries(ctx, u.Eth, Address(u.Cfg.Eth.Client), [][32]byte{hash}, at)
	if err != nil || len(es) != 1 {
		return false, "", fmt.Errorf("the entry of header %d: %v", p.Target, err)
	}
	return es[0].Proves(after.Epoch), fmt.Sprintf("holds no entry of header %d at epoch %d", p.Target, after.Epoch), nil
}

// overtaken says whether an update failed because another submitter's landed first (E3-GS3): mined and reverted
// HeightNotAboveTrusted (its reason read at the state before its block: an update at or above it was recorded
// already), or mined and reverted for any reason with the client at its own block trusting the target's height or
// above (the other landed before it in that block, which the reason's reading cannot see), or refused
// HeightNotAboveTrusted by the simulation with the client at latest trusting it — a plan from a stale read is refused
// for free and planned again.
func (u *Updater) overtaken(ctx context.Context, p *updatePlan, rc *types.Receipt, err error) bool {
	rv, ok := ethx.AsRevert(err)
	if !ok || p.Prove {
		return false
	}
	named := rv.Named("HeightNotAboveTrusted")
	if rv.Mined && named {
		return true
	}
	if !rv.Mined && !named {
		return false
	}
	at := ethx.Latest
	if rc != nil && rc.BlockHash != (common.Hash{}) {
		at = ethx.AtHash(rc.BlockHash)
	}
	after, rerr := ethx.ReadClient(ctx, u.Eth, Address(u.Cfg.Eth.Client), at)
	return rerr == nil && after.TrustedHeight >= p.Target
}

// errWaits is a submission that sent nothing and waits (a pending staging call, a base fee above the cap): the plan
// line says why, and the step ends without an error.
var errWaits = errors.New("the update waits")

// errOvertaken is an update another submitter's landed before (E3-GS3): no failure; the plan line says so.
var errOvertaken = errors.New("another submitter's update landed first")

// stagedSlots are the slots this key's earlier staging calls for a header carried under the stage key: its
// StageProgress logs over the last day, each call's calldata read back.
func (u *Updater) stagedSlots(ctx context.Context, hash, key [32]byte) (map[int]bool, error) {
	latest, err := u.Eth.BlockAt(ctx, ethx.Latest)
	if err != nil {
		return nil, err
	}
	from := uint64(0)
	if latest.Number > stageBack {
		from = latest.Number - stageBack
	}
	q := ethx.LogQuery{Addresses: []common.Address{Address(u.Cfg.Eth.Client)}, Topics: [][]common.Hash{{ethx.LightClient.Event("StageProgress")}, nil, {ethx.AddressTopic(u.Sender.Address())}}}
	logs, err := u.Eth.LogsRange(ctx, q, from, latest.Number)
	if err != nil {
		return nil, err
	}
	out := map[int]bool{}
	for _, l := range logs {
		sp, err := ethx.DecodeStageProgress(l)
		if err != nil || sp.Key != key || sp.HeaderHash != hash || sp.Submitter != u.Sender.Address() {
			continue
		}
		tx, err := u.Eth.Transaction(ctx, l.TxHash)
		if err != nil {
			return out, err
		}
		_, h, _, slots, err := ethx.HeaderCallOf(tx.Input)
		if err != nil || h != hash {
			continue
		}
		for _, s := range slots {
			out[s] = true
		}
	}
	return out, nil
}

// Handler serves GET /v1/status from the last published snapshot.
func (u *Updater) Handler() http.Handler { return u.box.handler() }

// Status is the updater's last published snapshot.
func (u *Updater) Status() map[string]any { return u.box.get() }

func (u *Updater) buildStatus() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := map[string]any{"key": u.Sender.Address().Hex(), "inFlight": u.Sender.InFlight(), "armed": u.state.Armed, "lastSubmit": u.state.LastSubmit,
		"lastSubmitAt": u.state.LastSubmitAt, "submits": u.state.Submits, "sigsBound": u.state.SigsBound, "scanned": u.state.Out.Index.Scanned, "attempt": u.state.Attempt, "announceAttempt": u.state.AnnAttempt, "alarms": u.Alarm.Standing()}
	for k, v := range u.status {
		st[k] = v
	}
	return st
}

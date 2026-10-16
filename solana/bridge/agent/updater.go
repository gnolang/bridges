package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

const (
	// UpdateInterval is how often a header goes to the client with nothing else asking for one (§6.1).
	UpdateInterval = 10 * time.Minute
	// ClientLagAfter is the lag that alarms the operator, the guardians and the members' channel (§6.1).
	ClientLagAfter = time.Hour
	// eventScanMax is how many gno blocks one tick scans for r/sys/validators' events (a catch-up is bounded;
	// the announcing header is found from the headers themselves whatever the scan missed).
	eventScanMax = 300
	// updateUnits is the compute budget of one staging transaction (M2-NOTES: ≈60k for a whole update).
	updateUnits = 400_000
)

// lightSource is the gno node as the updater and the relayer read it.
type lightSource interface {
	gnoReader
	BlockAt(ctx context.Context, height int64) (*gnolight.Block, error)
	CommitAt(ctx context.Context, height int64) (*gnolight.Commit, []*gnolight.Vote, error)
	ValidatorsAt(ctx context.Context, height int64) (*gnolight.ValidatorSet, error)
	BlockResults(ctx context.Context, height int64) ([]*gnolight.TxResult, error)
}

// Updater feeds headers to the light client (PLAN.md §6.1): a header every 10 minutes, whenever a dispatch
// waits, and the announcing header the moment r/sys/validators emits ValidatorAdded or ValidatorRemoved — found
// from the headers themselves too, since the client must be presented the header whose NextValidatorsHash
// differs before any header of the new set. It stages updates keyed by (its key, the header hash), resumes a
// stage a stop left behind, and alarms at one hour of lag. Its own SOL key is the fifth float of §5.5.
type Updater struct {
	Cfg      *Config
	Key      solana.PrivateKey
	State    string
	Every    time.Duration
	Interval time.Duration
	PerTx    int
	Alarm    *Alarmer

	gno    lightSource
	sol    *sol.Client
	client sol.LightClient
	now    func() time.Time

	mu     sync.Mutex
	state  updaterState
	status map[string]any
	box    statusBox
	// newest is the newest message's nonce and height, read once (a message never changes; a page that cannot be
	// rendered cost the node a query at its gas limit every tick, E3-RL1)
	newest struct {
		n uint32
		h int64
	}
	// newestErrSince is when the newest message's height first went unread (E4-SF4: the tick weighs its other reasons
	// all the same; updater:newest alarms once it has stood two minutes); zero while it reads.
	newestErrSince time.Time
}

type updaterState struct {
	Scanned      int64     `json:"scanned"`      // the last gno height scanned for r/sys/validators' events
	Armed        bool      `json:"armed"`        // an event was seen: the next header goes at once
	LastSubmit   int64     `json:"lastSubmit"`   // the last height submitted
	LastSubmitAt time.Time `json:"lastSubmitAt"` //
	Submits      int       `json:"submits"`
}

func (u *Updater) init() error {
	if u.now == nil {
		u.now = time.Now
	}
	if u.Every == 0 {
		u.Every = 5 * time.Second
	}
	if u.Interval == 0 {
		u.Interval = UpdateInterval
	}
	if u.PerTx == 0 {
		u.PerTx = 3
	}
	u.status = map[string]any{}
	return readJSON(u.State, &u.state)
}

func (u *Updater) save() error {
	if u.State == "" {
		return nil
	}
	return writeJSON(u.State, u.state)
}

// Run feeds the client until ctx ends.
func (u *Updater) Run(ctx context.Context) error {
	if err := u.init(); err != nil {
		return err
	}
	if !u.Cfg.HasPrograms() || len(u.Cfg.Solana.RPCs) == 0 {
		return errors.New("updater: solana.rpcs and solana.client/pausable/admin are needed")
	}
	if u.gno == nil {
		u.gno = u.Cfg.SourceClient(u.Cfg.Gno.RPC)
	}
	u.sol = sol.NewClient(u.Cfg.Solana.RPCs[0], u.Key)
	u.client = u.Cfg.Programs().LightClient()
	log.Printf("updater: key %s, client %s, node %s, a header every %s", u.Key.PublicKey(), u.client.Program, u.Cfg.Gno.RPC, u.Interval)
	var qc qevalCheck // E4-SF6: at the start and hourly
	for {
		qc.run(ctx, u.gno, u.Cfg.Gno.Mailbox, u.Alarm, u.now())
		if err := u.tick(ctx); err != nil {
			log.Printf("updater: %v", err)
			u.set("lastErr", err.Error())
		} else {
			u.set("lastErr", "")
		}
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

// tick is one round: the client's state, the lag, the chain's head, the events, the target, the submission.
func (u *Updater) tick(ctx context.Context) error {
	cs, err := readClientState(ctx, u.sol.RPC, u.client, rpc.CommitmentConfirmed)
	if err != nil {
		return err
	}
	now := u.now()
	lag := now.Sub(time.Unix(cs.Head.LastAcceptedTime, 0))
	u.set("trustedHeight", cs.Head.TrustedHeight)
	u.set("trustedHash", hex.EncodeToString(cs.Head.TrustedHash[:]))
	u.set("lastAccepted", time.Unix(cs.Head.LastAcceptedTime, 0).UTC().Format(time.RFC3339))
	u.set("nextSetAnnounced", cs.Body.NextSet != nil)
	if lag > ClientLagAfter {
		u.Alarm.Alarm("client:lag", fmt.Sprintf("the client accepted no header for %s (trusted height %d)", lag.Round(time.Minute), cs.Head.TrustedHeight))
	} else {
		u.Alarm.Clear("client:lag")
	}
	if cs.Body.ChainID != u.Cfg.Gno.ChainID {
		return fmt.Errorf("the client follows chain %q, the config says %q", cs.Body.ChainID, u.Cfg.Gno.ChainID)
	}
	// the validator set against the client's measured bound (G19/AO4): the heap frame is due before it
	u.setBound(len(cs.Body.Set), len(cs.Body.NextSet))
	head, err := u.gno.Status(ctx)
	if err != nil {
		return err
	}
	u.set("gnoHead", head.Height)
	T := int64(cs.Head.TrustedHeight)
	if head.Height <= T {
		u.set("plan", "the chain is at the trusted height")
		return nil
	}
	// the events of r/sys/validators arm the next submission
	if armed, err := u.scanEvents(ctx, T, head.Height); err != nil {
		log.Printf("updater: event scan: %v", err)
	} else if armed {
		u.state.Armed = true
	}
	plan, err := planUpdate(ctx, u.gno, cs, head.Height)
	if err != nil {
		u.set("plan", "none: "+err.Error())
		u.Alarm.Alarm("updater:plan", err.Error())
		return nil
	}
	u.Alarm.Clear("updater:plan")
	if plan.Announcing {
		u.setBound(len(cs.Body.Set), max(len(cs.Body.NextSet), len(plan.NextSet)))
	}
	waits, err := u.dispatchWaits(ctx, T)
	if err != nil {
		// the newest message's height unread — its page and the mailbox's record alike: a node that refuses or limits
		// vm/qeval, a backend without the message yet, a timeout. The tick returned here, so no header went at all while
		// it lasted, the announcing header of a validator change included (E4-SF4): it weighs the other reasons all the
		// same, and the last newest height it read (a message of ours dispatched before it still waits on it)
		waits = u.newest.h != 0 && u.newest.h+1 > T
		u.newestUnread(err)
	} else {
		u.newestErrSince = time.Time{}
		u.Alarm.Clear("updater:newest")
	}
	reason := ""
	switch {
	case plan.Announcing:
		reason = "the announcing header"
	case u.state.Armed:
		reason = "a validator-set event"
	case waits:
		reason = "a dispatch waits"
	case lag >= u.Interval:
		reason = fmt.Sprintf("%s since the last accepted header", lag.Round(time.Second))
	}
	u.set("plan", fmt.Sprintf("target %d (announcing %v), reason: %s", plan.Target, plan.Announcing, orNone(reason)))
	if reason == "" {
		return nil
	}
	if err := u.submit(ctx, cs, plan); err != nil {
		return fmt.Errorf("submitting %d (%s): %w", plan.Target, reason, err)
	}
	u.state.Armed = false
	u.state.LastSubmit, u.state.LastSubmitAt, u.state.Submits = plan.Target, now, u.state.Submits+1
	log.Printf("updater: header %d accepted (%s)", plan.Target, reason)
	return nil
}

// newestUnread logs a tick whose newest message's height went unread, alarmed (updater:newest) once it has stood two
// minutes: a dispatch of ours may wait on a header the tick cannot see it needs, until the interval's.
func (u *Updater) newestUnread(err error) {
	log.Printf("updater: the newest message's height: %v (the tick weighs the other reasons)", err)
	if u.newestErrSince.IsZero() {
		u.newestErrSince = u.now()
	}
	if since := u.now().Sub(u.newestErrSince); since >= cutFailAfter {
		u.Alarm.Alarm("updater:newest", fmt.Sprintf("hlmailbox's newest message's height has not read for %s (%v): a header for it goes only with the announcing header, a validator-set event or the interval's (E4-SF4)", since.Round(time.Second), err))
	}
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// setBound alarms client:set-bound when the current or an announced set has sol.SetSizeWarn validators or more
// (G19/AO4), and clears it below.
func (u *Updater) setBound(current, announced int) {
	if text := setBoundText(current, announced); text != "" {
		u.Alarm.Alarm("client:set-bound", text)
		return
	}
	u.Alarm.Clear("client:set-bound")
}

// dispatchWaits says whether hlmailbox's newest message sits in a block the client cannot prove from yet (its
// event is at height h; the proof needs header h+1 at or below the trusted height).
func (u *Updater) dispatchWaits(ctx context.Context, trusted int64) (bool, error) {
	mb, err := mailboxAt(ctx, u.gno, u.Cfg.Gno.Mailbox, 0)
	if err != nil {
		return false, err
	}
	if mb.Count == 0 {
		return false, nil
	}
	n := mb.Count - 1
	if u.newest.h == 0 || u.newest.n != n {
		h, err := u.messageHeight(ctx, mb.Domain, n)
		if err != nil {
			return false, err
		}
		u.newest.n, u.newest.h = n, h
	}
	return u.newest.h+1 > trusted, nil
}

// messageHeight is the block message n was dispatched in: from its page, or — a page the node cannot render, a
// stranger's big message as the newest failed every tick, so no header went at all (E3-RL1) — from the
// mailbox's record.
func (u *Updater) messageHeight(ctx context.Context, domain, n uint32) (int64, error) {
	msg, err := gnoMessage(ctx, u.gno, u.Cfg.Gno.Mailbox, domain, n)
	if err == nil {
		return msg.Height, nil
	}
	hr, ok := u.gno.(headerReader)
	if !ok {
		return 0, err
	}
	_, h, _, herr := msgRecord(ctx, hr, u.Cfg.Gno.Mailbox, domain, n)
	if herr != nil {
		return 0, fmt.Errorf("%w (nor its record: %v)", err, herr)
	}
	return h, nil
}

// scanEvents looks at the blocks since the last scan (bounded) for r/sys/validators' events.
func (u *Updater) scanEvents(ctx context.Context, trusted, head int64) (bool, error) {
	from := u.state.Scanned + 1
	if from <= trusted {
		from = trusted + 1
	}
	if head-from+1 > eventScanMax {
		from = head - eventScanMax + 1
	}
	armed := false
	for h := from; h <= head; h++ {
		blk, err := u.gno.BlockAt(ctx, h)
		if err != nil {
			return armed, err
		}
		if blk.NumTxs > 0 {
			rs, err := u.gno.BlockResults(ctx, h)
			if err != nil {
				return armed, err
			}
			if gno.ValsetEvent(rs) {
				armed = true
				log.Printf("updater: a validator-set event at height %d", h)
			}
		}
		u.state.Scanned = h
	}
	return armed, nil
}

// updatePlan is the header to submit next and, when it announces a set change, the set it announces.
type updatePlan struct {
	Target     int64
	Announcing bool
	NextSet    []gnolight.Validator
	Expect     [32]byte // the set hash the target's ValidatorsHash must carry
}

// planUpdate chooses the next header for the client (§6.1): the chain's head when the head's set is the one the
// client expects next (the current set, or the announced set once the client waits for it); otherwise the first
// header whose NextValidatorsHash leaves that set — the announcing header, which must be presented with the set
// it announces — found by a binary search over the sets by height (a set change is permanent along one chain).
func planUpdate(ctx context.Context, src lightSource, cs *clientState, head int64) (*updatePlan, error) {
	T := int64(cs.Head.TrustedHeight)
	expect := cs.Body.SetHash
	if cs.Body.NextSet != nil {
		expect = cs.Body.NextSetHash
	}
	hashAt := func(h int64) ([32]byte, error) {
		s, err := src.ValidatorsAt(ctx, h)
		if err != nil {
			return [32]byte{}, err
		}
		hash, _ := s.Hash()
		return hash, nil
	}
	vh, err := hashAt(head)
	if err != nil {
		return nil, err
	}
	target := head
	if vh != expect {
		x, err := firstDiffering(T+1, head, expect, hashAt)
		if err != nil {
			return nil, err
		}
		target = x - 1
		if target <= T {
			return nil, fmt.Errorf("the set changed at height %d, right after the trusted header %d, which the client cannot follow (its trusted header announces no change): a reset is the cure", x, T)
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
		nh, _ := next.Hash()
		if nh != blk.Header.NextValidatorsHashArray() {
			return nil, fmt.Errorf("the validators at %d do not hash to header %d's NextValidatorsHash", target+1, target)
		}
		plan.Announcing, plan.NextSet = true, next.Validators
	}
	return plan, nil
}

// firstDiffering is the smallest height in [lo, hi] whose set hash is not expect, by binary search, checked
// against its predecessor (a linear scan from lo if the sets were not monotone).
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
	// a monotone range has the first difference at l with its predecessor still the expected set
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

// submit stages and finishes the update for a plan, resuming a stage already on chain.
func (u *Updater) submit(ctx context.Context, cs *clientState, plan *updatePlan) error {
	blk, err := u.gno.BlockAt(ctx, plan.Target)
	if err != nil {
		return err
	}
	hdr := blk.Header
	if hdr.Hash() != blk.Hash {
		return fmt.Errorf("header %d hashes to %x here, the node says %x", plan.Target, hdr.Hash(), blk.Hash)
	}
	cm, _, err := u.gno.CommitAt(ctx, plan.Target)
	if err != nil {
		return err
	}
	set, err := u.gno.ValidatorsAt(ctx, plan.Target)
	if err != nil {
		return err
	}
	if sh, _ := set.Hash(); sh != hdr.ValidatorsHashArray() {
		return fmt.Errorf("the validators at %d do not hash to the header's ValidatorsHash", plan.Target)
	}
	keys := make([][32]byte, len(set.Validators))
	for i, v := range set.Validators {
		keys[i] = v.PubKey
	}
	submitter := u.Key.PublicKey()
	txs, err := u.client.UpdateTxs(submitter, cs.Body.ChainID, hdr, cm, keys, plan.NextSet, u.PerTx, updateUnits)
	if err != nil {
		return err
	}
	// a bigger set gets its room before the update that announces it (S-02): the body region grows toward what
	// the announcement and the switch will take, 10 KB a call, so the finish never answers NeedsGrow
	bodyCap := uint32(0)
	if plan.Announcing {
		if bodyCap, err = u.ensureBodyCap(ctx, cs, len(plan.NextSet)); err != nil {
			return err
		}
	}
	stage := u.client.UpdateStage(submitter, blk.Hash)
	err = driveStage(ctx, u.sol, u.client, stage, txs, bodyCap)
	if err != nil && solDeterministic(err) {
		u.Alarm.Alarm("updater:refused", err.Error())
	} else if err == nil {
		u.Alarm.Clear("updater:refused")
	}
	return err
}

// ensureBodyCap grows the client's body region to what a set change to n validators needs (sol.BodyCapNeeded),
// one Grow a call, and answers the target; a set past the client's bound (MaxBodyCap) is alarmed: the heap
// frame the plan names is a build item before any such set (M2-NOTES).
func (u *Updater) ensureBodyCap(ctx context.Context, cs *clientState, n int) (uint32, error) {
	need := cs.Body.BodyCapNeeded(n)
	if need >= sol.MaxBodyCap {
		u.Alarm.Alarm("updater:body-bound", fmt.Sprintf("a set of %d validators wants a body region at the client's bound (%d bytes): the announcement may fail with NeedsGrow for good", n, sol.MaxBodyCap))
	}
	cap := cs.Head.BodyCap()
	for calls := 0; cap < need; calls++ {
		if calls >= 12 {
			return 0, fmt.Errorf("the body region is %d bytes after %d Grow calls, %d needed", cap, calls, need)
		}
		if _, err := u.sol.Send(ctx, []solana.Instruction{u.client.GrowIx(u.Key.PublicKey(), uint32(need))}); err != nil {
			return 0, fmt.Errorf("growing the body region to %d: %w", need, err)
		}
		data, err := accountData(ctx, u.sol.RPC, u.client.State())
		if err != nil {
			return 0, err
		}
		head, err := sol.ParseClientHead(data)
		if err != nil {
			return 0, err
		}
		log.Printf("updater: the client's body region grown from %d to %d bytes for a set of %d (target %d)", cap, head.BodyCap(), n, need)
		cap = head.BodyCap()
	}
	return uint32(need), nil
}

// Handler serves GET /v1/status from the last published snapshot.
func (u *Updater) Handler() http.Handler { return u.box.handler() }

// Status is the updater's last published snapshot.
func (u *Updater) Status() map[string]any { return u.box.get() }

// buildStatus is the updater's state for its status endpoint, built by the loop at the end of a tick.
func (u *Updater) buildStatus() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := map[string]any{"key": u.Key.PublicKey().String(), "scanned": u.state.Scanned, "armed": u.state.Armed, "lastSubmit": u.state.LastSubmit, "lastSubmitAt": u.state.LastSubmitAt, "submits": u.state.Submits, "alarms": u.Alarm.Standing()}
	for k, v := range u.status {
		st[k] = v
	}
	return st
}

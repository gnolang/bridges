package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The guardians' readings (hlwatch, PLAN.md §5.8 via the Solana plan): the monitor run in read-only mode — the same
// ordered cut, the same identity by the signers' rule, the same record, stages and decision rules, never a flag —
// with the pages a holder reads beside it: who governs, the flags and the seats on all three objects, the holds, every
// pending decision on both chains and when it matures, both sequence counters, the builder-made proposals with no
// execution, and before a kind 8 or 9 the target's code against a reproducible build, an EIP-7702 account named, and
// the storage layouts' difference; every pending kind 8 or 9 whose target has no code yet — it pends on HlAdmin until
// code is deployed there (fold F2's NoCodeYet), so whoever can create at that address decides when it runs — listed,
// and one that has waited past its maturity longer than the configured age alarmed (PLAN.md §6.5: GovDAO cancels a
// decision that goes stale; a forgotten upgrade must not roll a proxy back years later).

// Watch is hlwatch's reader: a read-only monitor over the holder's own node and two agreeing providers.
type Watch struct {
	m *Monitor
	// Supermajority is the YES percent GovDAO's law needs (an armed proposal).
	Supermajority float64
	// Builds are reproducible builds a kind 8's implementation or a kind 9's successor (and its HlAdminExec) is held
	// to, by label (hlwatch -build): forge's artifacts, masked and read (targets.go), or a runtime's hash alone.
	Builds map[string]Build
	// CreationTxs are the transactions that created the targets, by target (hlwatch -creation-tx): each held to the
	// build its target is, the constructor's arguments read (E2-RT1); a target without one is NOT SHOWN IMMUTABLE.
	CreationTxs map[common.Address]common.Hash
	// Layouts are the storage layouts (forge's storageLayout JSON) a kind 8 is held against: the running one's and the
	// new one's (hlwatch -layout-old, -layout-new).
	LayoutOld, LayoutNew []byte
	// CodeWaitAge is how long a matured kind 8 or 9 may wait for its target's code before the watch alarms it
	// (decision:code-wait:<n>, and the page is not green): 0 is DefaultCodeWaitAge (hlwatch -code-wait).
	CodeWaitAge time.Duration
	// CatchUp bounds how long one View polls to bring the scans of a state that is behind — a fresh one, or one not run
	// for weeks — to the finalized block, 20,000 blocks a poll each (the Go trust audit's E3-GT10): 0 is
	// DefaultCatchUp. A scan still behind when it ends is named on the page, which is then not green.
	CatchUp time.Duration
}

// DefaultCatchUp is how long one View polls at most to bring its scans to the finalized block.
const DefaultCatchUp = 30 * time.Minute

// DefaultCodeWaitAge is how long a matured decision may wait for its target's code before the watch alarms: a week,
// the delay the decision itself waited — GovDAO's time to see that the code it voted for was never deployed.
const DefaultCodeWaitAge = 7 * 24 * time.Hour

// CodeWait is a pending kind 8 or 9 whose target has no code yet at the read block: HlAdmin refuses its execute
// (NoCodeYet) until code is deployed there, and whoever can create at that address decides when it runs.
type CodeWait struct {
	Number    uint64
	Kind      uint8
	Target    common.Address
	MaturesAt int64 // when it may execute by HlAdmin's clocks (it waits for code from then)
	Mature    bool
	Stale     bool // matured longer ago than the watch's age: alarmed
}

// NewWatch makes a holder's reader: the config's two providers (refused unless two), the holder's own node, its state
// file (the burn index and the record between runs; "" keeps none), the deployment's first block (0: found).
func NewWatch(ctx context.Context, cfg *Config, node, state string, fromBlock uint64, alarm *Alarmer) (*Watch, error) {
	if alarm == nil {
		alarm = &Alarmer{Source: "hlwatch"}
	}
	m := &Monitor{Cfg: cfg, State: state, Alarm: alarm, FromBlock: fromBlock, ReadOnly: true, own: cfg.SourceClient(node)}
	if err := m.Open(ctx); err != nil {
		return nil, err
	}
	return &Watch{m: m, Supermajority: gnoside.DefaultSupermajority}, nil
}

// Pair is the watch's two providers.
func (w *Watch) Pair() ethx.Pair { return w.m.pair }

// WatchView is one reading of both chains.
type WatchView struct {
	LaunchLine  uint32 // HlAdmin's launchNonce() as the burn scan last read it
	At          time.Time
	Height      int64
	BlockTime   time.Time
	Finalized   uint64
	Verdict     string // "" when the poll judged; else why there is no verdict
	Mailbox     *gnoside.Mailbox
	Routes      map[string]*gnoside.Route
	Identity    map[string]string // the monitor's lines, by symbol
	Broken      []string          // the symbols whose identity is broken
	Pending     []PendingGno      // queued decisions on gno.land
	Proposals   []gnoside.Proposal
	DAOErr      string
	Gov         *gnoside.Gov
	GovErr      string
	Client      *ethx.ClientState
	ChainHash   string // the own node's block hash at the client's trusted height ("" when it has none)
	Record      headerRecord
	Gate        *ethx.GateState
	GateErr     string
	Admin       *ethx.AdminState
	AdminAddr   common.Address
	AdminLine   string
	AdminErr    string
	Decisions   []PendingEth // HlAdmin's pending decisions with their maturity
	CodeWaits   []CodeWait   // the pending kind 8 and 9 whose target has no code yet
	CodeWaitAge time.Duration
	Checks      []string // kind 8 and 9 checks
	Alarms      map[string]string
	Unconfirmed []string // readings the providers gave differently
	// Symbols are the configured tokens: each must be judged, and hold, for the page to be green.
	Symbols []string
	// Flagged are the standing alarms raised as flags (what the watch would flag, were it not read-only).
	Flagged []string
	// Blind are the rules this reading could not run, with why (a scan or a read that failed); Behind the scans not yet
	// at the finalized block (catching up, 20,000 blocks a poll). Either keeps the page from green: a rule that did not
	// run vouches for nothing (E3-GT3, E3-GT10).
	Blind, Behind []string
	// UnreadLedgers are the realms whose decision ledger did not read this time, with why (E4-GT5, E4-CF3, E4-TL4): a
	// pending decision there is not listed, and the page says so — never "none" — and is not green; UnreadProposals the
	// open GovDAO proposals whose own page did not read
	UnreadLedgers, UnreadProposals []string
}

// PendingGno is a queued decision of a realm on gno.land.
type PendingGno struct {
	Realm string
	gnoside.Decision
}

// PendingEth is a pending decision on HlAdmin.
type PendingEth struct {
	ethx.Decision
	Text      string
	MaturesAt int64
	When      string // executeWhen's words
	Mature    bool
}

// View reads both chains: one poll, or as many as bring a state that is behind to the finalized block (catchUp).
func (w *Watch) View(ctx context.Context) (*WatchView, error) {
	m := w.m
	err := w.catchUp(ctx)
	v := &WatchView{At: m.now(), Alarms: m.Alarm.Standing(), Identity: map[string]string{}}
	pv := m.last
	if pv == nil || pv.Snap == nil {
		if err == nil {
			err = errors.New("no snapshot")
		}
		return nil, fmt.Errorf("no verdict: the Ethereum snapshot: %w", err)
	}
	v.Finalized = pv.Snap.Block.Number
	v.Client = pv.Snap.Client
	if pv.Head.Height == 0 {
		why := "no head read"
		if err != nil {
			why = err.Error()
		}
		return nil, fmt.Errorf("no verdict: the node: %s", ethx.Redact(why))
	}
	if ok, why := judgeOwnHead(pv.Head, pv.Snap.At, m.now()); !ok {
		return nil, fmt.Errorf("no verdict: %s (the node's head is read after the Ethereum snapshot and must be a reading of the chain, §5.9)", why)
	}
	v.Height, v.BlockTime = pv.Head.Height, pv.Head.Time
	switch {
	case !pv.Verdict:
		v.Verdict, _ = m.Status()["cut"].(string)
		if err != nil {
			v.Verdict += " (" + ethx.Redact(err.Error()) + ")"
		}
	case err != nil:
		// the identity's verdict was taken and the poll failed after it (E3-GT3): the diverted and unindexed credits,
		// the record, the stages and the decisions did not all run, so the reading vouches for none of them
		v.Verdict = "the poll failed after the identity's verdict (" + ethx.Redact(err.Error()) + "): the diverted and unindexed credits, the header record, the stages and the decisions were not all judged"
	}
	for _, r := range m.Cfg.Routes {
		v.Symbols = append(v.Symbols, r.Symbol)
	}
	v.Blind = append(v.Blind, pv.Blind...)
	v.Behind = append(v.Behind, pv.Behind...)
	for sym, text := range pv.IdentityText {
		v.Identity[sym] = text
		if strings.HasPrefix(text, Broken.String()) {
			v.Broken = append(v.Broken, sym)
		}
	}
	sort.Strings(v.Broken)
	H := v.Height
	if v.Mailbox = pv.Mailbox; v.Mailbox == nil {
		if v.Mailbox, err = gnoside.MailboxAt(ctx, m.own, m.Cfg.Gno.Mailbox, H); err != nil {
			return nil, err
		}
	}
	if v.Routes = pv.Routes; v.Routes == nil {
		v.Routes = map[string]*gnoside.Route{}
		for _, r := range m.Cfg.Routes {
			rj, err := gnoside.RouteAt(ctx, m.own, r.Gno, H)
			if err != nil {
				return nil, err
			}
			v.Routes[r.Symbol] = rj
		}
	}
	// gno.land's queued decisions: the mailbox, every route of each line, the fee realm, r/hlgov
	realms := []string{m.Cfg.Gno.Mailbox}
	for _, r := range m.Cfg.Routes {
		realms = append(realms, r.Gno)
		if mig := m.migs[r.Symbol]; mig != nil {
			realms = append(realms, mig.Paths()...)
		}
	}
	realms = append(realms, m.Cfg.Gno.Fees, m.Cfg.Gno.Gov)
	seen := map[string]bool{}
	for _, realm := range realms {
		if realm == "" || seen[realm] {
			continue
		}
		seen[realm] = true
		ds, err := gnoside.DecisionsAt(ctx, m.own, realm, H)
		if err != nil {
			// a ledger that does not read hides what it holds: said, never "none", and never green (E4-GT5, E4-CF3)
			why := ethx.Redact(err.Error())
			v.UnreadLedgers = append(v.UnreadLedgers, realm+" ("+why+")")
			v.Blind = append(v.Blind, "the pending decisions on gno.land of "+realm+" (a decision queued there is not listed): "+why)
			continue
		}
		for _, d := range ds {
			if d.Queued() {
				v.Pending = append(v.Pending, PendingGno{Realm: realm, Decision: d})
			}
		}
	}
	var props []string
	for r := range seen {
		props = append(props, r)
	}
	sort.Strings(props)
	// from the floor the state keeps: the list read down to the lowest proposal still open when it was last read whole
	var floor *gnoside.ProposalFloor
	v.Proposals, v.DAOErr, v.UnreadProposals, floor = gnoside.ReadProposalsFrom(ctx, m.own, m.Cfg.Gno.DAO, H, w.Supermajority, m.state.DAOFloor, props...)
	if floor != nil {
		m.state.DAOFloor = floor
		if err := m.save(); err != nil {
			log.Printf("hlwatch: state: %v", err)
		}
	}
	if v.DAOErr != "" {
		v.Blind = append(v.Blind, "GovDAO's proposals (a PRE-ARMED proposal of a bridge realm's builder is not listed): "+v.DAOErr)
	}
	for _, u := range v.UnreadProposals {
		v.Blind = append(v.Blind, "a GovDAO proposal's page ("+u+"): if it is a bridge realm's builder's, it is not listed")
	}
	if m.Cfg.Gno.Gov != "" {
		if g, err := gnoside.GovAt(ctx, m.own, m.Cfg.Gno.Gov, H); err == nil {
			v.Gov = g
		} else {
			v.GovErr = err.Error()
			v.Blind = append(v.Blind, "r/hlgov's counter and decision recipient: "+ethx.Redact(err.Error()))
		}
	}
	// Ethereum: the client against gno.land, the record, the gate, HlAdmin and its pending decisions
	if v.Client != nil {
		if b, err := m.own.Block(ctx, v.Client.TrustedHeight); err == nil {
			v.ChainHash = strings.ToLower(b.Hash)
		}
	}
	v.Record = m.state.Record
	o := pv.Objects
	if o == nil {
		o = m.objects(ctx, pv.Snap)
	}
	v.Gate = o.gate
	if o.gateErr != nil {
		v.GateErr = ethReadText(o.gateErr)
	}
	if o.admins != nil {
		v.Admin, v.AdminAddr, v.AdminLine = o.admins.State, o.admins.Live, o.admins.Text()
		v.LaunchLine = w.m.state.Index.Line
		now := v.BlockTime.Unix()
		factor := int64(max(v.Admin.ClockFactor, 1))
		for _, d := range o.pending {
			ok, why := executeWhen(d, v.Admin, now)
			v.Decisions = append(v.Decisions, PendingEth{Decision: *d, Text: DescribeKind(d.Kind, d.Params), MaturesAt: maturesAt(d, v.Admin, factor), When: why, Mature: ok})
		}
		// one -layout-old/-layout-new pair is one implementation's: with several kind 8s pending, none is held to it
		// (E3-CF3) — each is read alone with hlwatch check
		kind8, layoutWhy := 0, ""
		for _, d := range o.pending {
			if d.Kind == 8 {
				kind8++
			}
		}
		if kind8 > 1 && (len(w.LayoutOld) > 0 || len(w.LayoutNew) > 0) {
			layoutWhy = fmt.Sprintf("one -layout-old/-layout-new pair was given and %d kind 8s are pending, while a pair is one implementation's: read each with hlwatch check -decision", kind8)
		}
		for _, d := range o.pending {
			if d.Kind == 8 || d.Kind == 9 {
				v.Checks = append(v.Checks, w.targetCheck(ctx, pv.Snap, v.Admin, d.Number, d.Kind, d.Params, layoutWhy))
			}
		}
		w.codeWaits(ctx, v, o.pending, pv.Snap.Pin, now, factor)
	}
	if o.adminErr != nil {
		v.AdminErr = ethReadText(o.adminErr)
	}
	// what the realms cannot see on Ethereum, read before a vote and an apply (E4-CF5)
	w.crossChecks(ctx, v, pv.Snap, o)
	v.Alarms = m.Alarm.Standing() // the code waits' alarms among them
	v.Flagged = m.Alarm.StandingFlags()
	for k, text := range v.Alarms {
		if strings.HasPrefix(k, "eth:providers") {
			v.Unconfirmed = append(v.Unconfirmed, text)
		}
	}
	return v, nil
}

// catchUp polls once, and again while the reading's scans are behind the finalized block and moving (the Go trust
// audit's E3-GT10): a state that is fresh, or was not run for weeks, needs a poll for every 20,000 blocks of each scan —
// the burn index first, then the header record, the stages and the decisions — and one poll a run left `status` with no
// verdict for good past 20,000 blocks of the deployment. It stops once nothing is behind, when a poll moved no scan, at
// the first poll that fails, or at the CatchUp deadline; whatever is still behind is named on the page, never green.
func (w *Watch) catchUp(ctx context.Context) error {
	limit := w.CatchUp
	if limit <= 0 {
		limit = DefaultCatchUp
	}
	deadline := time.Now().Add(limit)
	for {
		was := w.m.scanCursors()
		err := w.m.Step(ctx)
		pv := w.m.last
		if err != nil || pv == nil || len(pv.Behind) == 0 || ctx.Err() != nil || w.m.scanCursors() == was || time.Now().After(deadline) {
			return err
		}
		log.Printf("hlwatch: catching up — %s", strings.Join(pv.Behind, "; "))
	}
}

// codeWaits lists every pending kind 8 or 9 whose target has no code at the read block — both providers agreeing on
// the empty code — and alarms one whose maturity is older than the watch's age (decision:code-wait:<n>, cleared once
// the decision is gone or its code is there; a code that did not read this time leaves its alarm as it was, E5-GT4).
func (w *Watch) codeWaits(ctx context.Context, v *WatchView, pending []*ethx.Decision, pin ethx.At, now, factor int64) {
	age := w.CodeWaitAge
	if age <= 0 {
		age = DefaultCodeWaitAge
	}
	v.CodeWaitAge = age
	standing := map[string]bool{}
	alarms := w.m.Alarm.Standing()
	for _, d := range pending {
		target, ok := ethx.CodeTarget(d.Kind, d.Params)
		if !ok {
			continue
		}
		code, err := w.agreedCode(ctx, pin, target)
		if err != nil {
			// unconfirmed (the check line says so): no reading of whether its code is there, so its alarm stands as it was
			key := fmt.Sprintf("decision:code-wait:%d", d.Number)
			if _, was := alarms[key]; was {
				standing[key] = true
			}
			continue
		}
		if len(code) != 0 {
			continue // code there
		}
		at := maturesAt(d, v.Admin, factor)
		cw := CodeWait{Number: d.Number, Kind: d.Kind, Target: target, MaturesAt: at, Mature: now >= at}
		key := fmt.Sprintf("decision:code-wait:%d", d.Number)
		if cw.Mature && time.Duration(now-at)*time.Second > age {
			cw.Stale = true
			standing[key] = true
			w.m.Alarm.Alarm(key, fmt.Sprintf("HlAdmin's decision #%d (kind %d) has waited for code at %s since it matured at %s, longer than %s: it pends (NoCodeYet) until code is deployed there — whoever can create at that address decides when it runs; GovDAO cancels it if it is no longer wanted (§6.5)", d.Number, d.Kind, target.Hex(), when(at), age))
		}
		v.CodeWaits = append(v.CodeWaits, cw)
	}
	for k := range w.m.Alarm.Standing() {
		if strings.HasPrefix(k, "decision:code-wait:") && !standing[k] {
			w.m.Alarm.Clear(k)
		}
	}
}

// agreedCode is an address's code at a pinned state as both providers give it (an error when they differ).
func (w *Watch) agreedCode(ctx context.Context, at ethx.At, addr common.Address) ([]byte, error) {
	ca, err := w.m.pair.A.CodeAt(ctx, addr, at)
	if err != nil {
		return nil, err
	}
	cb, err := w.m.pair.B.CodeAt(ctx, addr, at)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(ca, cb) {
		return nil, fmt.Errorf("the providers give %s's code differently", addr.Hex())
	}
	return ca, nil
}

// targetCheck is what a holder reads before a kind 8 or 9 pending on HlAdmin (§6.5, §8; targets.go): a kind 8's
// implementation held to a build, with its proxy and its call; a kind 9's successor with its state and bindings.
func (w *Watch) targetCheck(ctx context.Context, snap *ethSnapshot, admin *ethx.AdminState, number uint64, kind byte, params []byte, layoutWhy string) string {
	if len(params) < 64 || (kind == 8 && len(params) < 96) {
		return fmt.Sprintf("#%d kind %d: parameters too short to read (HlAdmin spends it)", number, kind)
	}
	// kind 8: proxy 32 ‖ implementation 32 ‖ codehash 32 ‖ call; kind 9: successor 32 ‖ codehash 32 (64 bytes, so a
	// kind 8's slices would run past its end)
	var target, want []byte
	label := "implementation"
	if kind == 9 {
		target, want, label = params[0:32], params[32:64], "successor"
	} else {
		target, want = params[32:64], params[64:96]
	}
	addr, ok := ethx.AddressFrom32([32]byte(target))
	if !ok {
		return fmt.Sprintf("#%d kind %d: the %s is no address (its top twelve bytes are not zero): HlAdmin spends it", number, kind, label)
	}
	if kind == 9 {
		return fmt.Sprintf("#%d kind 9: %s", number, w.SuccessorCheck(ctx, snap.Pin, admin.Address, admin.ClockFactor, addr, common.BytesToHash(want)))
	}
	proxy, ok := ethx.AddressFrom32([32]byte(params[0:32]))
	if !ok {
		return fmt.Sprintf("#%d kind 8: the proxy is no address (its top twelve bytes are not zero): HlAdmin spends it", number)
	}
	return fmt.Sprintf("#%d kind 8: %s", number, w.ImplementationCheck(ctx, snap.Pin, admin.Address, proxy, addr, common.BytesToHash(want), params[96:], layoutWhy))
}

// TargetSpec is what `hlwatch check` reads of a kind 8 or 9: the target (a kind 8's implementation, a kind 9's
// successor), the code hash the decision names and, for a kind 8, the proxy it upgrades and the call it carries — read
// from the decision itself wherever it can be (E3-GT7: a check of the flags typed said "no call (ProxyAdmin.upgrade)"
// for a decision that carried one, or held a build to a proxy the decision does not upgrade).
type TargetSpec struct {
	Kind     byte
	Proxy    common.Address
	Target   common.Address
	CodeHash common.Hash
	Call     []byte
	From     string // where the parameters were read
}

// SpecOfParams reads a kind 8's or 9's parameters as r/hlgov dispatches them and HlAdmin holds them: kind 8 `proxy 32 ‖
// implementation 32 ‖ codehash 32 ‖ call`, kind 9 `successor 32 ‖ codehash 32`.
func SpecOfParams(kind byte, params []byte, from string) (TargetSpec, error) {
	s := TargetSpec{Kind: kind, From: from}
	addr := func(b []byte, what string) (common.Address, error) {
		a, ok := ethx.AddressFrom32([32]byte(b))
		if !ok {
			return common.Address{}, fmt.Errorf("%s: the %s is no address (its top twelve bytes are not zero): HlAdmin spends it", from, what)
		}
		return a, nil
	}
	var err error
	switch kind {
	case 8:
		if len(params) < 96 {
			return s, fmt.Errorf("%s: a kind 8 of %d parameter bytes, short of 96: HlAdmin spends it", from, len(params))
		}
		if s.Proxy, err = addr(params[0:32], "proxy"); err != nil {
			return s, err
		}
		if s.Target, err = addr(params[32:64], "implementation"); err != nil {
			return s, err
		}
		s.CodeHash = common.BytesToHash(params[64:96])
		s.Call = append([]byte{}, params[96:]...)
	case 9:
		if len(params) != 64 {
			return s, fmt.Errorf("%s: a kind 9 of %d parameter bytes, not 64: HlAdmin spends it", from, len(params))
		}
		if s.Target, err = addr(params[0:32], "successor"); err != nil {
			return s, err
		}
		s.CodeHash = common.BytesToHash(params[32:64])
	default:
		return s, fmt.Errorf("%s is a kind %d, not a kind 8 or 9", from, kind)
	}
	return s, nil
}

// DecisionSpec reads r/hlgov's dispatched decision n (decision/<n>: the body r/hlgov sent HlAdmin, kind ‖ number ‖
// params) at the node's head: the decision exactly as HlAdmin received or will receive it.
func (w *Watch) DecisionSpec(ctx context.Context, n uint64) (TargetSpec, error) {
	from := fmt.Sprintf("r/hlgov's decision %d", n)
	if w.m.Cfg.Gno.Gov == "" {
		return TargetSpec{}, errors.New("the config names no r/hlgov (gno.gov)")
	}
	d, err := gnoside.GovDecisionAt(ctx, w.m.own, w.m.Cfg.Gno.Gov, n, 0)
	if err != nil {
		return TargetSpec{}, fmt.Errorf("%s: %w", from, err)
	}
	if d == nil {
		return TargetSpec{}, fmt.Errorf("%s: r/hlgov dispatched no such decision", from)
	}
	body, err := hex.DecodeString(strings.TrimPrefix(d.Body, "0x"))
	if err != nil || len(body) < 9 || binary.BigEndian.Uint64(body[1:9]) != n {
		return TargetSpec{}, fmt.Errorf("%s: its body %q is no decision numbered %d", from, d.Body, n)
	}
	return SpecOfParams(body[0], body[9:], from)
}

// The descriptions r/hlgov's builders write for a kind 8 and a kind 9 (NewUpgradeRequest, NewTransferToSuccessorRequest).
var (
	upgradeTextRE   = regexp.MustCompile(`upgrade (\S+) (0x[0-9a-fA-F]{40}) to implementation (0x[0-9a-fA-F]{40})(?: \(its launch implementation: a rollback\))? with code hash 0x([0-9a-fA-F]{64}), (?:no call \(upgrade\)|then call 0x([0-9a-fA-F]+) \((\d+) bytes, upgradeAndCall\))`)
	successorTextRE = regexp.MustCompile(`transfer every authority to (0x[0-9a-fA-F]{40}) \(code hash 0x([0-9a-fA-F]{64})\)`)
)

// ProposalSpec reads GovDAO proposal id before the vote: its description, which r/hlgov's builder writes from the very
// parameters its executor dispatches (a builder's request carries both, made together, and GovDAO's request has no
// setter), parsed back. GovDAO's page names the realm an executor was created in as the executor itself states it
// (gno v1.5: CreationRealm() is the Executor interface's), so a stranger's proposal can claim r/hlgov's and describe any
// target; it can never make r/hlgov dispatch, but its voters would read the wrong code. The reading says so, and once
// the decision is dispatched -decision reads r/hlgov's own record of it — and the page, HlAdmin's pending one — before it
// executes.
func (w *Watch) ProposalSpec(ctx context.Context, id int64) (TargetSpec, error) {
	from := fmt.Sprintf("GovDAO proposal #%d", id)
	if w.m.Cfg.Gno.Gov == "" {
		return TargetSpec{}, errors.New("the config names no r/hlgov (gno.gov)")
	}
	page, err := w.m.own.RenderAt(ctx, w.m.Cfg.Gno.DAO, strconv.FormatInt(id, 10), 0)
	if err != nil {
		return TargetSpec{}, fmt.Errorf("%s: %w", from, err)
	}
	if ex := gnoside.ExecutorRealm(page); ex != w.m.Cfg.Gno.Gov {
		return TargetSpec{}, fmt.Errorf("%s was not made with r/hlgov's builder (its executor names %q, not %s): nothing here reads its parameters", from, ex, w.m.Cfg.Gno.Gov)
	}
	text := gnoside.ProposalText(page)
	from += " (the parameters its description states — GovDAO shows the executor's realm as the executor states it, so this reads a proposal of r/hlgov's builder only if it is one: once dispatched, read it again with -decision, r/hlgov's own record)"
	if m := upgradeTextRE.FindAllStringSubmatch(text, -1); len(m) == 1 {
		x := m[0]
		s := TargetSpec{Kind: 8, Proxy: common.HexToAddress(x[2]), Target: common.HexToAddress(x[3]), CodeHash: common.HexToHash(x[4]), From: from}
		if x[5] != "" {
			if s.Call, err = hex.DecodeString(x[5]); err != nil || strconv.Itoa(len(s.Call)) != x[6] {
				return TargetSpec{}, fmt.Errorf("%s: its call 0x%s does not read as %s bytes", from, x[5], x[6])
			}
		}
		return s, nil
	} else if len(m) > 1 {
		return TargetSpec{}, fmt.Errorf("%s: its description holds %d upgrades, r/hlgov's builder writes one", from, len(m))
	}
	if m := successorTextRE.FindAllStringSubmatch(text, -1); len(m) == 1 {
		return TargetSpec{Kind: 9, Target: common.HexToAddress(m[0][1]), CodeHash: common.HexToHash(m[0][2]), From: from}, nil
	}
	return TargetSpec{}, fmt.Errorf("%s is no kind 8 or 9 of r/hlgov's builder (its description: %.200q)", from, text)
}

// TargetCheck is `hlwatch check`: a kind 8's implementation, with its proxy and call, or a kind 9's successor, read at a
// pinned state as the page reads a pending decision's, against the live HlAdmin (the config's, followed through its
// line of succession) and its clock factor; the reading says where its parameters were read.
func (w *Watch) TargetCheck(ctx context.Context, at ethx.At, spec TargetSpec) (string, error) {
	line, err := followAdmins(ctx, w.m.pair, Address(w.m.Cfg.Eth.Admin), at)
	if err != nil {
		return "", fmt.Errorf("HlAdmin: %w", err)
	}
	text := ""
	switch spec.Kind {
	case 8:
		text = w.ImplementationCheck(ctx, at, line.Live, spec.Proxy, spec.Target, spec.CodeHash, spec.Call, "")
	case 9:
		text = w.SuccessorCheck(ctx, at, line.Live, line.State.ClockFactor, spec.Target, spec.CodeHash)
	default:
		return "", fmt.Errorf("a kind %d: hlwatch check reads a kind 8 or 9", spec.Kind)
	}
	if spec.From != "" {
		text = spec.From + ": " + text
	}
	return text, nil
}

// LayoutAppendOnly is how LayoutDiff's answer begins when the new layout keeps the running one (anything else is a
// difference, or a layout that does not read: never "immutable").
const LayoutAppendOnly = "the storage layout is append-only"

// gapRE is a storage gap's type: a fixed array of uint256 reserved for later variables (OpenZeppelin's convention,
// `uint256[40] private __gap` in the gate and the client).
var gapRE = regexp.MustCompile(`^t_array\(t_uint256\)(\d+)_storage$`)

// LayoutDiff compares two storage layouts (forge's storageLayout JSON): every entry of the running layout must stand in
// the new one at the same slot and offset with the same label and the same type — the type compared through the
// layouts' "types" (the Go trust audit's E4-GT3: a type's name alone, its AST number stripped, read a reordered struct
// under the mailbox's delivered marks, or a fixed array that shrank, as "append-only"): a struct's members (label, slot,
// offset, type), a fixed array's length and element, a mapping's key and value, every type's numberOfBytes; a struct
// may only gain members in its own unused bytes (its size unchanged), and only the layout's last entry, a fixed array,
// may grow — and entries may only be added after them (§0: an upgrade keeps each layout append-only — a reordered
// layout could reset the mailbox's delivered marks or the tokens' balances) — or inside a storage gap of the running
// layout (`__gap`, a uint256 array kept zero for them: the plan's own append convention, E3-CF3), each new entry within
// the gap's slots and a smaller gap after them; a gap grown past its end, or an entry anywhere else in the running
// layout, differs. Each entry's slots are its type's numberOfBytes (the layout's "types"), else one.
func LayoutDiff(oldJSON, newJSON []byte) string {
	var a, b storageLayout
	if err := json.Unmarshal(oldJSON, &a); err != nil {
		return "the running layout does not read: " + err.Error()
	}
	if err := json.Unmarshal(newJSON, &b); err != nil {
		return "the new layout does not read: " + err.Error()
	}
	if len(a.Storage) == 0 {
		return "the running layout holds no entry: not a storage layout of the running implementation"
	}
	// slots is where an entry starts and how many slots it covers
	slots := func(l storageLayout, e layoutEntry) (int, int) {
		s, _ := strconv.Atoi(e.Slot)
		n := 1
		if t, ok := l.Types[e.Type]; ok {
			if bytes, err := strconv.Atoi(t.NumberOfBytes); err == nil && bytes > 0 {
				n = (e.Offset + bytes + 31) / 32
			}
		} else if m := gapRE.FindStringSubmatch(e.Type); m != nil {
			n, _ = strconv.Atoi(m[1])
		}
		return s, max(n, 1)
	}
	isGap := func(e layoutEntry) bool { return strings.HasSuffix(e.Label, "__gap") && gapRE.MatchString(e.Type) }
	key := func(e layoutEntry) string { return e.Slot + "/" + strconv.Itoa(e.Offset) }
	nb := map[string]layoutEntry{}
	for _, e := range b.Storage {
		nb[key(e)] = e
	}
	// the running layout's last entry (the only one a fixed array may grow at)
	lastKey, lastSlot := "", -1
	for _, e := range a.Storage {
		if s, _ := slots(a, e); s > lastSlot || (s == lastSlot && key(e) > lastKey) {
			lastKey, lastSlot = key(e), s
		}
	}
	var diffs []string
	type span struct{ from, to int } // [from, to)
	var gaps []span
	end := -1 // the running layout's last slot
	kept := map[string]bool{}
	for _, e := range a.Storage {
		s, n := slots(a, e)
		end = max(end, s+n-1)
		if isGap(e) {
			gaps = append(gaps, span{s, s + n})
			if g, ok := nb[key(e)]; ok && g.Label == e.Label && g.Type == e.Type {
				kept[key(e)] = true // the gap as it was
			}
			continue
		}
		n2, ok := nb[key(e)]
		switch {
		case !ok:
			diffs = append(diffs, fmt.Sprintf("%s (slot %s, offset %d) is gone", e.Label, e.Slot, e.Offset))
		case n2.Label != e.Label || typeBase(n2.Type) != typeBase(e.Type):
			diffs = append(diffs, fmt.Sprintf("slot %s offset %d was %s %s, is %s %s", e.Slot, e.Offset, e.Label, typeBase(e.Type), n2.Label, typeBase(n2.Type)))
		default:
			if why := sameStorageType(a, e.Type, b, n2.Type, key(e) == lastKey, 0); why != "" {
				diffs = append(diffs, fmt.Sprintf("%s (slot %s, offset %d, %s): %s", e.Label, e.Slot, e.Offset, typeBase(e.Type), why))
				continue
			}
			kept[key(e)] = true
		}
	}
	judged := map[string]bool{} // the running layout's places: an entry the new one has there was judged above
	for _, e := range a.Storage {
		if !isGap(e) {
			judged[key(e)] = true
		}
	}
	added, inGap := 0, 0
	for _, e := range b.Storage {
		if kept[key(e)] || judged[key(e)] {
			continue
		}
		s, n := slots(b, e)
		var in *span
		for i := range gaps {
			if s >= gaps[i].from && s < gaps[i].to {
				in = &gaps[i]
			}
		}
		switch {
		case in != nil && s+n <= in.to:
			inGap++ // a new entry, or the smaller gap after them, in a gap's slots (zero until now)
		case in != nil:
			diffs = append(diffs, fmt.Sprintf("%s at slot %s runs past the end of the running gap at slots %d–%d", e.Label, e.Slot, in.from, in.to-1))
		case s <= end:
			diffs = append(diffs, fmt.Sprintf("%s is added at slot %s, inside the running layout (at or below slot %d, outside its gaps)", e.Label, e.Slot, end))
		default:
			added++
		}
	}
	if len(diffs) > 0 {
		return "LAYOUT DIFFERS: " + strings.Join(diffs, "; ")
	}
	text := fmt.Sprintf("%s (%d entries kept, %d added after them", LayoutAppendOnly, len(kept), added)
	if inGap > 0 {
		text += fmt.Sprintf(", %d placed in a storage gap's slots", inGap)
	}
	return text + ")"
}

// storageLayout is forge's storageLayout JSON: the entries and their types.
type storageLayout struct {
	Storage []layoutEntry         `json:"storage"`
	Types   map[string]layoutType `json:"types"`
}

// layoutEntry is one variable of a layout, or one member of a struct type.
type layoutEntry struct {
	Label  string `json:"label"`
	Slot   string `json:"slot"`
	Offset int    `json:"offset"`
	Type   string `json:"type"`
}

// layoutType is one type of a layout: its size and, as its kind has them, its members, element, key and value.
type layoutType struct {
	Encoding      string        `json:"encoding"`
	Label         string        `json:"label"`
	NumberOfBytes string        `json:"numberOfBytes"`
	Members       []layoutEntry `json:"members"`
	Base          string        `json:"base"`
	Key           string        `json:"key"`
	Value         string        `json:"value"`
}

// typeKind is a type id's kind: mapping, array, struct, dynamic (bytes or string in storage) or value.
func typeKind(id string) string {
	switch {
	case strings.HasPrefix(id, "t_mapping("):
		return "mapping"
	case strings.HasPrefix(id, "t_array("):
		return "array"
	case strings.HasPrefix(id, "t_struct("):
		return "struct"
	case id == "t_bytes_storage", id == "t_string_storage":
		return "dynamic"
	}
	return "value"
}

// inner is what a type id holds between its kind's opening parenthesis and the one that closes it, and what follows.
func inner(id string) (string, string) {
	i := strings.IndexByte(id, '(')
	if i < 0 {
		return "", ""
	}
	depth := 0
	for j := i; j < len(id); j++ {
		switch id[j] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return id[i+1 : j], id[j+1:]
			}
		}
	}
	return "", ""
}

// mappingParts is a mapping's key and value: the type's fields, else read from its id.
func mappingParts(id string, t layoutType) (string, string) {
	if t.Key != "" && t.Value != "" {
		return t.Key, t.Value
	}
	in, _ := inner(id)
	depth := 0
	for j := 0; j < len(in); j++ {
		switch in[j] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				return in[:j], in[j+1:]
			}
		}
	}
	return in, ""
}

// arrayParts is an array's element and length ("dyn" for a dynamic one): the type's base, the length from its id.
func arrayParts(id string, t layoutType) (string, string) {
	in, rest := inner(id)
	base := in
	if t.Base != "" {
		base = t.Base
	}
	return base, strings.TrimSuffix(rest, "_storage")
}

// valueSize is a value type's bytes from its name (t_address 20, t_uintN N/8, t_bytesN N, …), -1 when it does not say.
func valueSize(id string) int {
	base := typeBase(id)
	switch {
	case base == "t_address", strings.HasPrefix(base, "t_contract("), base == "t_address_payable":
		return 20
	case base == "t_bool", strings.HasPrefix(base, "t_enum("):
		return 1
	}
	for _, p := range []string{"t_uint", "t_int"} {
		if strings.HasPrefix(base, p) {
			if n, err := strconv.Atoi(strings.TrimPrefix(base, p)); err == nil {
				return n / 8
			}
		}
	}
	if strings.HasPrefix(base, "t_bytes") {
		if n, err := strconv.Atoi(strings.TrimPrefix(base, "t_bytes")); err == nil {
			return n
		}
	}
	return -1
}

// sizeOf is a type's numberOfBytes as its layout gives it, else a value type's from its name; -1 when unknown.
func sizeOf(l storageLayout, id string) int {
	if t, ok := l.Types[id]; ok {
		if n, err := strconv.Atoi(t.NumberOfBytes); err == nil && n > 0 {
			return n
		}
	}
	if typeKind(id) == "value" {
		return valueSize(id)
	}
	return -1
}

// sameStorageType says why a type of the running layout is not kept by the new one ("" when it is): the same kind, the
// same size, a mapping's key and value, an array's element and length (at the layout's last entry a fixed array may
// grow), a struct's members each at its slot and offset with its label and type, new members only in the struct's own
// unused bytes (E4-GT3).
func sameStorageType(a storageLayout, at string, b storageLayout, bt string, last bool, depth int) string {
	if depth > 16 {
		return "nested past 16 levels: not compared"
	}
	ka, kb := typeKind(at), typeKind(bt)
	if ka != kb {
		return fmt.Sprintf("a %s became a %s (%s, then %s)", ka, kb, typeBase(at), typeBase(bt))
	}
	ta, tb := a.Types[at], b.Types[bt]
	na, nb := sizeOf(a, at), sizeOf(b, bt)
	grows := false
	switch ka {
	case "mapping":
		ak, av := mappingParts(at, ta)
		bk, bv := mappingParts(bt, tb)
		if typeBase(ak) != typeBase(bk) {
			return fmt.Sprintf("its key %s became %s", typeBase(ak), typeBase(bk))
		}
		if why := sameStorageType(a, av, b, bv, false, depth+1); why != "" {
			return "its value: " + why
		}
	case "array":
		ab, alen := arrayParts(at, ta)
		bb, blen := arrayParts(bt, tb)
		switch {
		case alen == blen:
		case alen == "dyn" || blen == "dyn":
			return fmt.Sprintf("an array of length %s became one of length %s", alen, blen)
		default:
			x, _ := strconv.Atoi(alen)
			y, _ := strconv.Atoi(blen)
			if !last || y < x {
				return fmt.Sprintf("its length %s became %s", alen, blen)
			}
			grows = true // the layout's last entry, grown: its new elements were never written
		}
		if why := sameStorageType(a, ab, b, bb, false, depth+1); why != "" {
			return "its element: " + why
		}
		if ea, eb := sizeOf(a, ab), sizeOf(b, bb); ea != eb {
			return fmt.Sprintf("its element's size %d bytes became %d", ea, eb)
		}
	case "struct":
		if len(ta.Members) == 0 || len(tb.Members) == 0 {
			return "the layouts' types do not give its members: not compared"
		}
		pos := func(m layoutEntry) int { s, _ := strconv.Atoi(m.Slot); return s*32 + m.Offset }
		newAt := map[int]layoutEntry{}
		for _, m := range tb.Members {
			newAt[pos(m)] = m
		}
		used := map[int]bool{}
		for _, m := range ta.Members {
			m2, ok := newAt[pos(m)]
			switch {
			case !ok:
				return fmt.Sprintf("its member %s (slot %s, offset %d) is gone", m.Label, m.Slot, m.Offset)
			case m2.Label != m.Label || typeBase(m2.Type) != typeBase(m.Type):
				return fmt.Sprintf("its member at slot %s offset %d was %s %s, is %s %s", m.Slot, m.Offset, m.Label, typeBase(m.Type), m2.Label, typeBase(m2.Type))
			}
			if why := sameStorageType(a, m.Type, b, m2.Type, false, depth+1); why != "" {
				return "its member " + m.Label + ": " + why
			}
			used[pos(m)] = true
			n := max(sizeOf(a, m.Type), 1)
			for i := 1; i < n; i++ {
				used[pos(m)+i] = true
			}
		}
		for _, m := range tb.Members {
			if p := pos(m); !used[p] {
				for i := 0; i < max(sizeOf(b, m.Type), 1); i++ {
					if used[p+i] {
						return fmt.Sprintf("its new member %s (slot %s, offset %d) overlaps a running one", m.Label, m.Slot, m.Offset)
					}
				}
			}
		}
	default:
		if typeBase(at) != typeBase(bt) {
			return fmt.Sprintf("%s became %s", typeBase(at), typeBase(bt))
		}
	}
	switch {
	case na < 0 || nb < 0:
		if ka == "struct" || ka == "array" && !grows {
			return "its size is not in the layouts' types: not compared"
		}
	case na != nb && !(grows && nb > na):
		return fmt.Sprintf("its size %d bytes became %d", na, nb)
	}
	return ""
}

// typeBase is a forge type id without its AST number ("t_mapping(t_bytes32,t_bool)" stays; "t_struct(Entry)123_storage"
// loses the 123).
func typeBase(t string) string {
	var b strings.Builder
	skip := false
	for i := 0; i < len(t); i++ {
		c := t[i]
		if c == ')' {
			b.WriteByte(c)
			skip = true
			continue
		}
		if skip && c >= '0' && c <= '9' {
			continue
		}
		skip = false
		b.WriteByte(c)
	}
	return b.String()
}

// DescribeKind says a decision's parameters in words (PLAN.md §13's Ethereum kinds).
func DescribeKind(kind byte, p []byte) string {
	addr := func(b []byte) string {
		if len(b) < 32 {
			return "?"
		}
		if a, ok := ethx.AddressFrom32([32]byte(b[:32])); ok {
			return a.Hex()
		}
		return "0x" + hex.EncodeToString(b[:32])
	}
	switch kind {
	case 1:
		return "cancel " + describeCancel(p)
	case 2:
		if len(p) >= 1 {
			return [2]string{"unpause the gate", "unpause HlAdmin"}[min(int(p[0]), 1)]
		}
	case 3:
		if len(p) >= 33 {
			return fmt.Sprintf("set token %d's ISM to %s", p[0], addr(p[1:]))
		}
	case 5:
		if len(p) >= 1 {
			var parts []string
			for i := 0; i < int(p[0]) && 1+33*i+33 <= len(p); i++ {
				o := 1 + 33*i
				parts = append(parts, fmt.Sprintf("seat %d → %s", p[o], addr(p[o+1:])))
			}
			return "set guardians and watch keys: " + strings.Join(parts, ", ")
		}
	case 6:
		if len(p) >= 37 {
			return fmt.Sprintf("enroll router for token %d, domain %d: %s", p[0], binary.BigEndian.Uint32(p[1:5]), "0x"+hex.EncodeToString(p[5:37]))
		}
	case 7:
		if len(p) >= 40 {
			return fmt.Sprintf("set protocol fee %d wei, beneficiary %s", binary.BigEndian.Uint64(p[:8]), addr(p[8:]))
		}
	case 8:
		if len(p) >= 96 {
			call := "no call"
			if len(p) > 96 {
				call = fmt.Sprintf("then call 0x%s (%d bytes)", hex.EncodeToString(p[96:min(len(p), 100)]), len(p)-96)
			}
			return fmt.Sprintf("upgrade %s to %s (code hash %s), %s", addr(p[0:]), addr(p[32:]), "0x"+hex.EncodeToString(p[64:96]), call)
		}
	case 9:
		if len(p) >= 64 {
			return fmt.Sprintf("transfer every authority to %s (code hash %s)", addr(p), "0x"+hex.EncodeToString(p[32:64]))
		}
	case 12:
		if len(p) >= 17 {
			return fmt.Sprintf("set token %d's mint limits: %d a message, %d a day", p[0], binary.BigEndian.Uint64(p[1:9]), binary.BigEndian.Uint64(p[9:17]))
		}
	case 4, 10, 11:
		return fmt.Sprintf("reserved kind %d (spent)", kind)
	}
	return fmt.Sprintf("kind %d, parameters %s", kind, hex.EncodeToString(p))
}

func describeCancel(p []byte) string {
	if len(p) < 9 {
		return "(unreadable)"
	}
	salt, form, rest := binary.BigEndian.Uint64(p[:8]), p[8], p[9:]
	switch form {
	case 0:
		if len(rest) < 1 {
			return "(unreadable list)"
		}
		var ns []string
		for i := 0; i < int(rest[0]) && 1+9*i+9 <= len(rest); i++ {
			o := 1 + 9*i
			ns = append(ns, strconv.FormatUint(binary.BigEndian.Uint64(rest[o+1:o+9]), 10))
		}
		return fmt.Sprintf("numbers %s (salt %d)", strings.Join(ns, ", "), salt)
	case 1:
		if len(rest) >= 17 {
			return fmt.Sprintf("numbers %d to %d (salt %d)", binary.BigEndian.Uint64(rest[1:9]), binary.BigEndian.Uint64(rest[9:17]), salt)
		}
	}
	return fmt.Sprintf("form %d (salt %d)", form, salt)
}

// Text writes the view as the holder reads it.
func (v *WatchView) Text(build string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nhlwatch at %s — node height %d, block time %s; Ethereum's finalized block %d\n", build, v.At.UTC().Format(time.RFC3339), v.Height, v.BlockTime.UTC().Format(time.RFC3339), v.Finalized)
	if v.Verdict != "" {
		fmt.Fprintf(&b, "- NO VERDICT: %s\n", v.Verdict)
	}
	for _, x := range v.Blind {
		fmt.Fprintf(&b, "- NOT READ (the rule did not run): %s\n", x)
	}
	for _, x := range v.Behind {
		fmt.Fprintf(&b, "- CATCHING UP (the rule waits for it): %s\n", x)
	}
	m := v.Mailbox
	fmt.Fprintf(&b, "\n## hlmailbox\n")
	if m.Live {
		fmt.Fprintf(&b, "- live since %s\n", time.Unix(m.LaunchedAt, 0).UTC().Format(time.RFC3339))
	} else {
		fmt.Fprintf(&b, "- NOT LAUNCHED\n")
	}
	switch {
	case m.Governor == "":
		fmt.Fprintf(&b, "- governor: none compiled — gno.land/r/gov/dao's proposals decide\n")
	case m.Yielded == 0:
		fmt.Fprintf(&b, "- governor: %s (until the yield) — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused\n", m.Governor)
	default:
		fmt.Fprintf(&b, "- governor: yielded at height %d — gno.land/r/gov/dao's proposals decide\n", m.Yielded)
	}
	if rule, err := m.Rule(); err == nil {
		fmt.Fprintf(&b, "- signers: %s\n", rule)
	}
	for i, k := range m.Signers.Keys {
		fmt.Fprintf(&b, "  - %d: %s\n", i, k)
	}
	fmt.Fprintf(&b, "- seats:\n")
	for i, s := range m.Seats {
		kind := "guardian"
		if i >= 4 {
			kind = "watch key"
		}
		fmt.Fprintf(&b, "  - %d (%s): %s\n", i+1, kind, orNone(s))
	}
	fmt.Fprintf(&b, "- flags:\n")
	n := 0
	for _, path := range sortedSymbols(m.Routes) {
		for _, f := range m.Routes[path].StandingFlags() {
			n++
			line := fmt.Sprintf("  - %s: %s by seat %d (%s) since %s", path, f.Fn, f.Seat, f.Addr, time.Unix(f.At, 0).UTC().Format(time.RFC3339))
			if len(f.IDs) > 0 {
				line += " naming " + strings.Join(f.IDs, ",")
			}
			fmt.Fprintln(&b, line)
		}
	}
	if n == 0 {
		fmt.Fprintln(&b, "  - none")
	}
	fmt.Fprintf(&b, "\n## pending decisions on gno.land (Execute(id) after the delay; the relayers do)\n")
	for _, u := range v.UnreadLedgers {
		fmt.Fprintf(&b, "- NOT READ: %s — what it holds pending is not listed\n", u)
	}
	if len(v.Pending) == 0 && len(v.UnreadLedgers) == 0 {
		fmt.Fprintln(&b, "- none")
	}
	for _, d := range v.Pending {
		left := time.Unix(d.Due, 0).Sub(v.BlockTime)
		when := fmt.Sprintf("matures in %s (at %s)", left.Round(time.Second), time.Unix(d.Due, 0).UTC().Format(time.RFC3339))
		if left <= 0 {
			when = "MATURE, awaiting Execute"
		}
		fmt.Fprintf(&b, "- %s #%d %s: %s — %s\n", d.Realm, d.ID, d.Kind, d.Text, when)
	}
	fmt.Fprintf(&b, "\n## builder-made GovDAO proposals with no execution\n")
	switch {
	case v.DAOErr != "":
		fmt.Fprintf(&b, "- (%s)\n", v.DAOErr)
	case len(v.Proposals) == 0:
		fmt.Fprintln(&b, "- none")
	}
	for _, p := range v.Proposals {
		armed := ""
		if p.Armed {
			armed = " — PRE-ARMED: has the votes"
			if !m.DAOGoverns() {
				armed += "; awaits the yield"
			}
		}
		fmt.Fprintf(&b, "- #%d %s (yes %.2f%%, %s): %s%s\n", p.ID, p.Title, p.Yes, p.Status, p.Text, armed)
	}
	fmt.Fprintf(&b, "\n## the identity (lock = supply + owed + voided − lost, in flight counted), from this node and two providers\n")
	for _, sym := range sortedSymbols(v.Routes) {
		r := v.Routes[sym]
		fmt.Fprintf(&b, "- %s: lock %d = sent %d − paid %d; owed %d, voided %d, lost %d; %d open claims; meter %d of cap %d; paused: %s\n",
			sym, r.Lock, r.SentTotal, r.PaidTotal, r.Owed, r.Voided, r.Lost, r.OpenClaims, r.Meter, r.Cap, orNone(r.Paused))
		if text, ok := v.Identity[sym]; ok {
			fmt.Fprintf(&b, "  - %s\n", text)
		}
	}
	if v.Gov != nil {
		fmt.Fprintf(&b, "\n## r/hlgov\n- %d decisions sent to %s; the next takes number %d\n", v.Gov.Last, v.Gov.Recipient, v.Gov.Next)
	} else if v.GovErr != "" {
		fmt.Fprintf(&b, "\n## r/hlgov\n- not read: %s\n", v.GovErr)
	}
	fmt.Fprintf(&b, "\n## Ethereum (read at the finalized block %d through two providers)\n", v.Finalized)
	for _, u := range v.Unconfirmed {
		fmt.Fprintf(&b, "- UNCONFIRMED: %s\n", u)
	}
	if c := v.Client; c != nil {
		match := "MISMATCH: this node's block there is " + orNone(v.ChainHash)
		switch {
		case v.ChainHash == "":
			match = "the node has no such height (the client past the chain)"
		case strings.EqualFold(v.ChainHash, hexOf(c.TrustedHash)):
			match = "matches gno.land's"
		}
		fmt.Fprintf(&b, "- the client: trusted height %d (%s — %s), epoch %d, %d headers recorded, next set announced: %v, quarantine until %s, clock factor %d\n",
			c.TrustedHeight, short(hexOf(c.TrustedHash)), match, c.Epoch, c.Records, c.Announced(), watchQuarantine(c.QuarantineEnd, v.BlockTime), c.ClockFactor)
	}
	rec := "consistent with the client's counter and running hash"
	if v.Record.Gap != "" {
		rec = "GAP: " + v.Record.Gap
	}
	fmt.Fprintf(&b, "- the header record: %d rebuilt from HeaderRecorded events, %d checked against this node (%d awaited): %s\n", v.Record.Count, v.Record.Checked, len(v.Record.Unchecked), rec)
	var small []string
	for k, text := range v.Alarms {
		if strings.HasPrefix(k, "valset:small-order:") {
			small = append(small, text)
		}
	}
	sort.Strings(small)
	if len(small) == 0 {
		fmt.Fprintf(&b, "- validator keys anyone can sign for (small order, E2-ED3): none in the client's sets or gno.land's, current and announced\n")
	}
	for _, s := range small {
		fmt.Fprintf(&b, "- SMALL-ORDER VALIDATOR KEY: %s\n", s)
	}
	if v.Record.Mismatch != nil {
		fmt.Fprintf(&b, "  - HEADER MISMATCH: %s\n", v.Record.Mismatch.String())
	}
	if g := v.Gate; g != nil {
		fmt.Fprintf(&b, "- the gate %s: %s\n", g.Address.Hex(), guardiansLine(g.Keys, g.Flags, g.Hold))
		for i, l := range g.Limits {
			fmt.Fprintf(&b, "  - token %d's mint limits: %d a message, %d a day, the bucket at %d\n", i, l.MaxPerMessage, l.DayCap, l.Level(v.BlockTime.Unix(), 86_400/int64(max(g.ClockFactor, 1))))
		}
	} else if v.GateErr != "" {
		fmt.Fprintf(&b, "- the gate: not read: %s\n", v.GateErr)
	}
	if a := v.Admin; a != nil {
		fmt.Fprintf(&b, "- HlAdmin %s: setup done %v, origin %d, last decision number %d", v.AdminAddr.Hex(), a.SetupDone, a.Origin, a.LastSeq)
		if v.Gov != nil {
			fmt.Fprintf(&b, " (r/hlgov's %d)", v.Gov.Last)
		}
		fmt.Fprintf(&b, ", clock factor %d; %s\n", a.ClockFactor, guardiansLine(a.Keys, a.Flags, a.Hold))
		fmt.Fprintf(&b, "  - the launch line (launchNonce): %d — no message below it is a burn of this deployment, and none is counted\n", v.LaunchLine)
		if v.AdminLine != "" {
			fmt.Fprintf(&b, "  - %s\n", v.AdminLine)
		}
		if v.Gov != nil && a.LastSeq != v.Gov.Last {
			fmt.Fprintf(&b, "  - COUNTERS DIFFER: HlAdmin's %d, r/hlgov's %d (a gap means a foreign decision took a number, or GovDAO's is still on its way)\n", a.LastSeq, v.Gov.Last)
		}
		fmt.Fprintf(&b, "- the hold history:")
		if len(a.Holds) == 0 {
			fmt.Fprint(&b, " no hold has ended")
		}
		fmt.Fprintln(&b)
		for i, h := range a.Holds {
			fmt.Fprintf(&b, "  - %d: from %s to %s\n", i, time.Unix(int64(h.Start), 0).UTC().Format(time.RFC3339), time.Unix(int64(h.End), 0).UTC().Format(time.RFC3339))
		}
		fmt.Fprintf(&b, "- pending decisions on HlAdmin:\n")
		if len(v.Decisions) == 0 {
			fmt.Fprintln(&b, "  - none")
		}
		for _, d := range v.Decisions {
			when := "matures at " + time.Unix(d.MaturesAt, 0).UTC().Format(time.RFC3339) + " (" + d.When + ")"
			if d.Mature {
				when = "MATURE, awaiting Execute (" + d.When + ")"
			}
			fmt.Fprintf(&b, "  - #%d kind %d (%s) delivered at block %d by %s — %s\n", d.Number, d.Kind, d.Text, d.DeliveredBlock, d.Deliverer.Hex(), when)
		}
	} else if v.AdminErr != "" {
		fmt.Fprintf(&b, "- HlAdmin: not read: %s\n", v.AdminErr)
	}
	if v.Admin != nil {
		fmt.Fprintf(&b, "- decisions waiting for code (each pends until code is deployed at its target; whoever can create there decides when it runs; alarmed past %s after its maturity, and GovDAO cancels a stale one):\n", v.CodeWaitAge)
		if len(v.CodeWaits) == 0 {
			fmt.Fprintln(&b, "  - none")
		}
		for _, c := range v.CodeWaits {
			state := "matures at " + when(c.MaturesAt)
			if c.Mature {
				state = fmt.Sprintf("matured at %s, waiting for code for %s", when(c.MaturesAt), (time.Duration(v.BlockTime.Unix()-c.MaturesAt) * time.Second).Round(time.Second))
			}
			if c.Stale {
				state += " — STALE: past the age; cancel it unless the code is coming"
			}
			fmt.Fprintf(&b, "  - #%d kind %d: no code at %s yet — %s\n", c.Number, c.Kind, c.Target.Hex(), state)
		}
	}
	for _, c := range v.Checks {
		fmt.Fprintf(&b, "- BEFORE IT EXECUTES: %s\n", c)
	}
	var keys []string
	for k := range v.Alarms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(&b, "\n## alarms\n")
	if len(keys) == 0 {
		fmt.Fprintln(&b, "- none")
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %s\n", k, v.Alarms[k])
	}
	return ethx.Redact(b.String())
}

// greenAlarms are the alarms hlwatch's page is never green over, by key prefix (beside every alarm raised as a flag):
// the rules' findings — a diverted or an unindexed credit (E3-GT3), an identity alarmed, the sources' and the
// encodings' disagreement, a proxy's EIP-1967 slots off its kind 8 (E3-RT8, E3-AS7) — and the readings that did not
// hold, a finalized block too old to read the Ethereum side at among them (E5-RT1).
var greenAlarms = []string{"decision:foreign", "decision:code-wait", "decision:counter", "decision:line-unfollowed", "valset:small-order", "stage:unknown",
	"header:", "eth:providers", "eth:no-state", "eth:finalized-floor", "eth:finality-stall", "diverted:", "credit:unindexed", "identity:", "sources", "gno:encoding",
	"refund:delivered:", "route:line:", "record:", "scan:", "monitor:no-verdict", "cut", "proxy:slots:",
	"refund:gate:", "maximum:proposal:", "switch:"} // the cross-chain gates (E4-CF5)

// Green says whether the view shows nothing a holder must act on: a verdict over a poll that ran every rule, every
// configured token judged and holding, no rule blind and no scan behind, no header mismatch or gap, no foreign
// decision, no flag-level alarm nor a finding's, the counters equal, no flag standing anywhere, and no pending kind 8 or 9
// NOT SHOWN IMMUTABLE.
func (v *WatchView) Green() (bool, []string) {
	var why []string
	if v.Verdict != "" {
		why = append(why, "no verdict: "+v.Verdict)
	}
	for _, sym := range v.Broken {
		why = append(why, sym+"'s identity is broken")
	}
	if v.Verdict == "" {
		for _, sym := range v.Symbols {
			text, ok := v.Identity[sym]
			switch {
			case !ok:
				why = append(why, sym+"'s identity was not judged")
			case strings.HasPrefix(text, Broken.String()):
				// listed above
			case !strings.HasPrefix(text, Holds.String()+":"):
				why = append(why, sym+"'s identity: "+verdictWord(text))
			}
		}
	}
	for _, b := range v.Blind {
		why = append(why, "not read: "+b)
	}
	for _, b := range v.Behind {
		why = append(why, "catching up: "+b)
	}
	if v.Record.Mismatch != nil {
		why = append(why, "a header mismatch")
	}
	if c := v.Client; c != nil && v.ChainHash != "" && !strings.EqualFold(v.ChainHash, hexOf(c.TrustedHash)) {
		why = append(why, "the client's trusted header is not this node's block at its height (MISMATCH, E4-TL3)")
	}
	if v.Record.Gap != "" {
		why = append(why, "a gap in the record")
	}
	flagged := map[string]bool{}
	for _, k := range v.Flagged {
		flagged[k] = true
		why = append(why, "flag-level alarm "+k)
	}
	for k := range v.Alarms {
		if flagged[k] {
			continue
		}
		for _, p := range greenAlarms {
			if strings.HasPrefix(k, p) {
				why = append(why, "alarm "+k)
				break
			}
		}
	}
	if v.GateErr != "" {
		why = append(why, "the gate was not read")
	}
	if v.AdminErr != "" {
		why = append(why, "HlAdmin was not read")
	}
	for _, c := range v.Checks {
		if strings.Contains(c, VerdictUnshown) {
			why = append(why, "a pending kind 8 or 9 is "+VerdictUnshown+": "+c)
		}
	}
	if v.Admin != nil && v.Gov != nil && v.Admin.LastSeq > v.Gov.Last {
		why = append(why, "HlAdmin's counter is past r/hlgov's")
	}
	if v.Gate != nil && v.Gate.AnyFlag() {
		why = append(why, "a flag stands on the gate")
	}
	if v.Admin != nil && v.Admin.AnyFlag() {
		why = append(why, "a flag stands on HlAdmin")
	}
	for _, path := range sortedSymbols(v.Mailbox.Routes) {
		if len(v.Mailbox.Routes[path].Flags) > 0 {
			why = append(why, "a flag stands on "+path)
		}
	}
	sort.Strings(why)
	return len(why) == 0, why
}

// verdictWord is an identity line's verdict ("holds", "BROKEN", "short", "no verdict").
func verdictWord(text string) string {
	v, _, _ := strings.Cut(text, ":")
	return v
}

func guardiansLine(keys [6]common.Address, flags [6]ethx.Flag, hold ethx.Flag) string {
	var parts []string
	for i, k := range keys {
		kind := "seat"
		n := i + 1
		if i >= 4 {
			kind = "watch slot"
		}
		state := "no flag"
		if flags[i].Set() {
			state = fmt.Sprintf("FLAG since block %d", flags[i].BlockNumber)
		}
		addr := "(empty)"
		if k != (common.Address{}) {
			addr = k.Hex()
		}
		parts = append(parts, fmt.Sprintf("%s %d %s %s", kind, n, addr, state))
	}
	h := "no hold"
	if hold.Set() {
		h = fmt.Sprintf("HOLD since %s", time.Unix(int64(hold.Time), 0).UTC().Format(time.RFC3339))
	}
	return strings.Join(parts, "; ") + "; " + h
}

func watchQuarantine(end uint64, now time.Time) string {
	if end == 0 || now.Unix() >= int64(end) {
		return "none standing"
	}
	return time.Unix(int64(end), 0).UTC().Format(time.RFC3339)
}

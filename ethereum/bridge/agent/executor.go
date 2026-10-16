package agent

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The executors (eth/bridge/PLAN.md §6.5, §5.3): anyone executes a matured decision on HlAdmin, and the relayers do
// — attempted once its clock has passed by the chain's own time at the compiled factor (a cancel at recording, an
// unpause a day after delivery, the 7-day group seven days after; a replacement of guardians, kind 5, under a hold
// on the later of its own 7th day and the 21st day of the earliest hold overlapping its pending life, from the hold
// history), never while a hold defers it until the hold ends or a lone flag defers the 7-day group (kind 5 aside); a
// cancelled decision at any time, since execute closes it. The call is simulated before it is sent (never gas on a
// call that would revert) and goes under the emergency cap (ER6: never parked under maxBaseFee); the contract's
// NotMature and Deferred are waits, and so is NoCodeYet — a matured kind 8 whose implementation, or kind 9 whose
// successor, has no code yet pends on HlAdmin (fold F2) until the code is there: nothing is sent, never parked, tried
// again every few minutes (hlwatch lists the decisions waiting for code and alarms one that waits too long);
// CallStarved — a kind 8's call left less than the 2,000,000 gas HlAdmin forwards it (audit round 3, E3-CT4) — is
// sent again with more gas than the estimate showed, never parked (once more gas starves too, it counts toward the
// park). And gno.land's own queued decisions: every realm's Execute(id) once its delay passed by block time (a refund
// is GovDAO's own apply, AT2), and r/gov/dao's ExecuteOrRejectProposal on a builder-made proposal past its deadline
// (F2), after the yield only (M1k) — the Solana relayer's loops, carried — each sized from its simulation as far as
// the block's gas (tryCall's gasUpToTheBlock).

const (
	kindCancel       = 1
	kindUnpause      = 2
	kindSetGuardians = 5
	day              = int64(86_400)
	// adminScan bounds the numbers one tick reads for pending decisions.
	adminScan = 256
	// codeRetry is how long a decision waiting for its target's code (NoCodeYet) waits before its next estimate.
	codeRetry = 5 * time.Minute
	// starvedGas is the gas limit an execute HlAdmin refused CallStarved is sent again with (audit round 3, E3-CT4):
	// HlAdmin forwards a kind 8's call exactly 2,000,000 gas (HlAdminExec.CALL_GAS: what the call runs on on an Osaka
	// chain; under EIP-8037 its state growth is paid from the transaction's reservoir first, E4-CT3), and only with
	// CALL_GAS·64/63 + 10,000 left before the call; the execution around it — the decision's reads before, the proxy's
	// bindings read again after (the least that executes the contracts' probe is 2,140,278) — fits in the million
	// beyond it.
	starvedGas = 3_000_000
	// starvedReservoir is the state-gas reservoir a starved execute carries on a chain that meters state gas (EIP-8037,
	// E4-CT3): the room the call's 2,000,000 had for fresh storage slots before EIP-8037 (≈90 at 22,100 apiece), at
	// EIP-8037's 97,920 a slot — the call grows its state from the reservoir first, its execution gas left whole.
	starvedReservoir = 90 * 97_920
)

// hlAdminSlot is HlAdmin's ERC-7201 namespace (HlAdminStorage.SLOT): its first word packs lastSeq (bytes 24–31 of the
// big-endian word) and startSeq (bytes 16–23), the number the sequence started after — a launch's start, or the number
// a successor inherited (successorInit).
var hlAdminSlot = common.HexToHash("0xa549383900acf2d7f17f70f3a8e005874b124005220e85417ec5628fc6dcfa00")

// adminStart is the number an HlAdmin's sequence started after (its stored startSeq): no number at or below it ever
// held a decision (HlAdmin's _isDecision: start < number ≤ last).
func adminStart(ctx context.Context, eth ethChain, admin common.Address, at ethx.At) (uint64, error) {
	w, err := eth.StorageAt(ctx, admin, hlAdminSlot, at)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(w[16:24]), nil
}

// starvedLimit is the gas limit a starved execute is sent again with (E3-CT4): starvedGas — or, on a chain whose
// estimates meter state gas (EIP-8037, Glamsterdam: Sepolia since 2026-10-06; the sender's probe), EIP-7825's cap and
// starvedReservoir above it: there the cap bounds execution gas only, and a transaction's gas past it is a reservoir
// passed whole to a kind 8's call for its state growth (a fresh slot is 97,920 state gas: without a reservoir the
// call's 2,000,000 hold ≈17), so a state-heavy kind 8 the 3,000,000-gas resend could never execute there carries one
// (E4-CT3). The sender holds it to EIP-8037's 2^32 − 1 and the head block's gas limit; a key short of its upfront cost
// fails as any send short of funds does (counted, parked hourly, alarmed).
func (r *Relayer) starvedLimit(ctx context.Context) uint64 {
	if r.Sender.StateGas(ctx) {
		return ethx.GasCap + starvedReservoir
	}
	return starvedGas
}

func sevenDay(kind uint8) bool { return kind >= 3 && kind <= 12 }

// heldUntilTheEnd says whether a hold on HlAdmin defers this decision until the hold ends: the 7-day group but kind
// 5, which a hold defers to a floor instead.
func heldUntilTheEnd(kind uint8) bool { return sevenDay(kind) && kind != kindSetGuardians }

// delay is a kind's clock after delivery at the compiled factor (kind 5's hold rule apart).
func delay(kind uint8, factor int64) int64 {
	switch kind {
	case kindCancel:
		return 0
	case kindUnpause:
		return day / factor
	}
	return 7 * day / factor
}

// replacementFloor is a replacement's floor while a hold stands: the earliest hold overlapping [its delivery, now] —
// the standing one, or an ended one whose end is at or after the delivery — plus 21 days at the factor.
func replacementFloor(holds []ethx.Hold, deliveredTime, standing, day21 int64) int64 {
	earliest := standing
	for _, h := range holds {
		if int64(h.End) >= deliveredTime && int64(h.Start) < earliest {
			earliest = int64(h.Start)
		}
	}
	return earliest + day21
}

// maturesAt is when a decision may execute by HlAdmin's clocks.
func maturesAt(d *ethx.Decision, st *ethx.AdminState, factor int64) int64 {
	at := int64(d.DeliveredTime) + delay(d.Kind, factor)
	if st.Hold.Set() && d.Kind == kindSetGuardians {
		if f := replacementFloor(st.Holds, int64(d.DeliveredTime), int64(st.Hold.Time), 21*day/factor); f > at {
			at = f
		}
	}
	return at
}

// executeWhen says whether execute is attempted now for a pending decision, and why or why not; now is the chain's
// time (the latest block's).
func executeWhen(d *ethx.Decision, st *ethx.AdminState, now int64) (bool, string) {
	factor := int64(max(st.ClockFactor, 1))
	if st.IsCancelled(d.Number) {
		return true, "cancelled: execute closes it"
	}
	if st.Hold.Set() {
		if heldUntilTheEnd(d.Kind) {
			return false, "deferred by the hold on HlAdmin since " + when(int64(st.Hold.Time)) + " until it ends"
		}
	} else if heldUntilTheEnd(d.Kind) && st.AnyFlag() {
		return false, "deferred by a flag on HlAdmin"
	}
	at := maturesAt(d, st, factor)
	if now < at {
		if st.Hold.Set() && d.Kind == kindSetGuardians && at > int64(d.DeliveredTime)+delay(d.Kind, factor) {
			return false, "matures at " + when(at) + " (the floor: day 21 of the earliest hold overlapping its pending life)"
		}
		return false, "matures at " + when(at)
	}
	return true, "mature since " + when(at)
}

// judgeExecuteErr sorts a failed execute: "wait" (the contract's clock says not yet: NotMature, Deferred — seconds of
// skew from this reading), "code" (NoCodeYet: a kind 8's implementation or a kind 9's successor has no code yet, and
// the decision pends until it has), "alarm" (ForeignExec: an HlAdmin's delegate is not the code its own runtime pins;
// ImplementationMoved and AuthorityLost: a kind 8 would leave a proxy on code no hash pinned, or an authority lost —
// each an alarm, never a retry that could succeed: the decision pends and GovDAO cancels it; M2-NOTES.md Fold F5,
// F5.1 item 3), "gas" (CallStarved: too little gas was left for a kind 8's call's fixed budget — sent again with
// starvedGas, never parked; audit round 3, E3-CT4), "fee" (the base fee is above even the emergency cap), "fail"
// (§5.7's parking).
func judgeExecuteErr(err error) string {
	var ft *ethx.FeeTooHigh
	switch {
	case errors.As(err, &ft):
		return "fee"
	case ethx.RevertIs(err, "NoCodeYet"):
		return "code"
	case ethx.RevertIs(err, "CallStarved"):
		return "gas"
	case ethx.RevertIs(err, "ForeignExec"), ethx.RevertIs(err, "ImplementationMoved"), ethx.RevertIs(err, "AuthorityLost"):
		return "alarm"
	case ethx.RevertIs(err, "NotMature"), ethx.RevertIs(err, "Deferred"), ethx.RevertIs(err, "not mature"), ethx.RevertIs(err, "deferred"):
		return "wait"
	}
	return "fail"
}

// adminExecutes executes HlAdmin's matured decisions (the live one of its line).
func (r *Relayer) adminExecutes(ctx context.Context) error {
	if !r.Cfg.HasContracts() {
		return nil
	}
	line := r.env.admins
	if line == nil {
		var err error
		if line, err = followAdmins(ctx, r.Eth, Address(r.Cfg.Eth.Admin), ethx.Latest); err != nil {
			return err
		}
	}
	admin := line.Live
	latest, err := r.Eth.BlockAt(ctx, ethx.Latest)
	if err != nil {
		return err
	}
	now := int64(latest.Time)
	// HlAdmin's state and its decisions read at this one block, pinned by its hash: a provider behind a balancer
	// answers each read from its own node, and a counter from one block beside decisions from an older one would
	// read a decision recorded between them as empty
	at := ethx.AtHash(latest.Hash)
	st, err := ethx.ReadAdmin(ctx, r.Eth, admin, at)
	if err != nil {
		return err
	}
	numbers := r.adminNumbers(ctx, admin, st.LastSeq)
	ds, err := ethx.ReadDecisions(ctx, r.Eth, admin, numbers, at)
	if err != nil {
		return err
	}
	pending := map[string]bool{}
	var firstErr error
	for _, d := range ds {
		if d == nil {
			continue
		}
		key := "execute:" + strconv.FormatUint(d.Number, 10)
		pending[key] = true
		if why, ok := r.state.Refused[key]; ok {
			r.note(key, "refused for good, never retried: "+why)
			continue
		}
		act, why := executeWhen(d, st, now)
		if !act {
			delete(r.state.Ready, key)
			r.note(key, "waits: "+why)
			continue
		}
		if !r.ethAct(key) || !r.state.Attempts[key].due(r.now()) {
			continue
		}
		data, err := ethx.ExecuteCall(d.Number)
		if err != nil {
			return err
		}
		call := ethx.Call{To: admin, Data: data, Label: fmt.Sprintf("execute decision %d (kind %d)", d.Number, d.Kind), MaxBaseFee: r.Cfg.EmergencyMaxBaseFee()}
		if a := r.state.Attempts[key]; a != nil {
			call.MinGas = a.MinGas // starved before: more gas than the estimate shows
		}
		_, err = r.ethSend(ctx, call)
		if err != nil {
			switch judgeExecuteErr(err) {
			case "gas":
				if limit := r.starvedLimit(ctx); call.MinGas < limit {
					// HlAdmin refused it CallStarved (E3-CT4): the transaction left a kind 8's call less than its fixed
					// 2,000,000 gas — an estimate short of what the call needs (a provider answering the gas used).
					// No failure: sent again at the next tick with starvedLimit's gas, never parked
					a := r.state.Attempts[key]
					if a == nil {
						a = &attempt{}
						r.state.Attempts[key] = a
					}
					a.fail(r.now(), err, false)
					a.MinGas, a.Next = limit, r.now()
					reservoir := ""
					if limit > ethx.GasCap {
						reservoir = fmt.Sprintf(" (the chain meters state gas, EIP-8037: the %d past EIP-7825's cap is the reservoir the call grows its state from, E4-CT3)", limit-ethx.GasCap)
					}
					r.note(key, fmt.Sprintf("starved: %v — HlAdmin forwards a kind 8's call exactly 2,000,000 gas and refuses an execution left less; sent again with a gas limit of %d%s", err, limit, reservoir))
					continue
				}
				// starved at starvedLimit's gas too: more gas is not what it lacks — a failure toward the park (alarmed
				// there)
			case "wait", "code":
				a := r.state.Attempts[key]
				if a == nil {
					a = &attempt{}
					r.state.Attempts[key] = a
				}
				a.fail(r.now(), err, false)
				if judgeExecuteErr(err) == "code" {
					// the estimate refused it, nothing was sent: the decision pends on HlAdmin until its target's code
					// is deployed (or GovDAO cancels it), so it is looked at again in a few minutes, never parked
					a.Next = r.now().Add(codeRetry)
					r.note(key, "waits for code: its "+codeTargetText(d)+" has no code yet (HlAdmin refuses it NoCodeYet; nothing is sent): it pends until code is deployed there, or GovDAO cancels it")
					continue
				}
				r.note(key, "the contract says wait: "+err.Error())
				continue
			case "alarm":
				// nothing was sent (the estimate refused it): looked at again hourly — a cancel ends it
				a := r.state.Attempts[key]
				if a == nil {
					a = &attempt{}
					r.state.Attempts[key] = a
				}
				a.fail(r.now(), err, true)
				a.Next = r.now().Add(time.Hour)
				r.Alarm.Alarm("execute:"+key, fmt.Sprintf("HlAdmin %s refuses decision %d (kind %d): %v — %s; it pends until GovDAO cancels it (§6.5)", admin.Hex(), d.Number, d.Kind, err, alarmMeaning(err)))
				r.note(key, "refused, alarmed: "+err.Error())
				continue
			case "fee":
				r.ethFailed(key, err)
				continue
			}
			r.ethFailed(key, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("decision %d: %w", d.Number, err)
			}
			continue
		}
		r.succeeded(key)
		after, rerr := ethx.ReadDecisions(ctx, r.Eth, admin, []uint64{d.Number}, ethx.Latest)
		switch {
		case rerr != nil:
			r.note(key, "executed; the decision could not be read back: "+rerr.Error())
		case after[0] != nil:
			r.note(key, "execute succeeded yet the decision still stands: looked at again next tick")
		default:
			r.forget(key)
			r.note(key, "executed, spent or closed")
			log.Printf("relayer: decision %d (kind %d) executed on HlAdmin %s", d.Number, d.Kind, admin.Hex())
		}
	}
	r.forgetGoneExecutes(pending)
	return firstErr
}

// alarmMeaning says what an execute's alarm revert means.
func alarmMeaning(err error) string {
	switch {
	case ethx.RevertIs(err, "ForeignExec"):
		return "the HlAdminExec it would delegate to is not the code its runtime pins (an initcode created other code there): a kind 9's successor refuses its inheritance, and nothing foreign runs"
	case ethx.RevertIs(err, "ImplementationMoved"):
		return "the upgrade's call re-pointed the proxy away from the decided implementation: the proxy would run code no hash pinned"
	}
	return "after the upgrade the proxy's admin or the contract's owner would no longer be the ProxyAdmin and HlAdmin, or a binding finishSetup holds that no decision moves would move (that proxy's checks 9–14, 17–23 and 29, read from the new code: E3-CT3)"
}

// codeTargetText names what a kind 8 or 9 waits for: the implementation or the successor, by its address when its
// parameters carry one.
func codeTargetText(d *ethx.Decision) string {
	if a, ok := ethx.CodeTarget(d.Kind, d.Params); ok {
		if d.Kind == 9 {
			return "successor " + a.Hex()
		}
		return "implementation " + a.Hex()
	}
	return fmt.Sprintf("target (kind %d)", d.Kind)
}

// adminNumbers are the numbers one tick reads on an HlAdmin for pending decisions (E3-GS9): every number found pending
// at the finalized block below the cursor (AdminPending), and the adminScan numbers from the cursor up to its last
// number now. The cursor is HlAdmin's next number at the finalized block as far as it has been read there: it passes
// every number it reads there — one with nothing pending (executed, spent or cancelled at the finalized block: no
// reorg brings one back) for good, and one still pending (a kind 8 waiting for code, a decision deferred by a lasting
// hold, one not mature yet) into AdminPending, which is read again every tick — so one decision pending for good
// never hides the numbers after it; a provider without the finalized state leaves the cursor where it stands.
func (r *Relayer) adminNumbers(ctx context.Context, admin common.Address, last uint64) []uint64 {
	cur, pend := r.adminCursor(ctx, admin)
	numbers := append([]uint64{}, pend...)
	for n := cur; n <= last && n < cur+adminScan; n++ {
		numbers = append(numbers, n)
	}
	return numbers
}

// adminCursor moves an HlAdmin's cursor by the finalized block (adminNumbers) and answers it with the numbers below
// it still pending there.
func (r *Relayer) adminCursor(ctx context.Context, admin common.Address) (uint64, []uint64) {
	key := admin.Hex()
	cur, pend := max(r.state.AdminLow[key], 1), r.state.AdminPending[key]
	fin, err := r.Eth.BlockAt(ctx, ethx.Finalized)
	if err != nil {
		return cur, pend
	}
	at := ethx.AtHash(fin.Hash)
	fst, err := ethx.ReadAdmin(ctx, r.Eth, admin, at)
	if err != nil {
		return cur, pend
	}
	// no number at or below the sequence's start ever held a decision: the cursor starts past it, never at 1 — a launch
	// whose start is high, or a successor that inherited a high number, would be read empty 256 numbers a tick before its
	// first execute (E4-TL8). Read while there are numbers to read; a failed read leaves the cursor as it stands
	if cur <= fst.LastSeq {
		if start, err := adminStart(ctx, r.Eth, admin, at); err == nil && start >= cur {
			cur = min(start, fst.LastSeq) + 1
			r.state.AdminLow[key] = cur
		}
	}
	numbers := append([]uint64{}, pend...)
	window := 0
	for n := cur; n <= fst.LastSeq && window < adminScan; n++ {
		numbers = append(numbers, n)
		window++
	}
	if len(numbers) == 0 {
		return cur, pend
	}
	ds, err := ethx.ReadDecisions(ctx, r.Eth, admin, numbers, at)
	if err != nil {
		return cur, pend
	}
	var still []uint64
	for i, d := range ds {
		if d != nil {
			still = append(still, numbers[i])
		}
	}
	cur += uint64(window)
	r.state.AdminLow[key], r.state.AdminPending[key] = cur, still
	if len(still) == 0 {
		delete(r.state.AdminPending, key)
	}
	if cur <= fst.LastSeq {
		r.note("executes:"+key, fmt.Sprintf("catching up: read to number %d of %d at the finalized block (%d a tick), %d pending below", cur-1, fst.LastSeq, adminScan, len(still)))
	} else {
		r.note("executes:"+key, fmt.Sprintf("read to its last number %d at the finalized block, %d pending below", fst.LastSeq, len(still)))
	}
	return cur, still
}

// forgetGoneExecutes drops the execute bookkeeping of every decision no longer pending (another relayer or a member
// executed it, a cancel closed it), its alarms with it (G27).
func (r *Relayer) forgetGoneExecutes(pending map[string]bool) {
	keys := map[string]bool{}
	for _, m := range []map[string]bool{keysOf(r.state.Ready), keysOf(r.state.Attempts), keysOf(r.state.Refused)} {
		for k := range m {
			keys[k] = true
		}
	}
	for key := range keys {
		if !strings.HasPrefix(key, "execute:") || pending[key] {
			continue
		}
		r.forget(key)
		if _, ok := r.state.Refused[key]; ok {
			delete(r.state.Refused, key)
			r.Alarm.Clear("execute:refused:" + key)
		}
	}
}

// ------------------------------------------------------------------- gno.land --

// realms are every realm with a decisions page: the mailbox, the routes (every realm of a migrated route's line
// too), the fee realm, r/hlgov.
func (r *Relayer) realms(ctx context.Context) []string {
	out := []string{r.Cfg.Gno.Mailbox}
	seen := map[string]bool{r.Cfg.Gno.Mailbox: true}
	for _, rt := range r.Cfg.Routes {
		paths := []string{rt.Gno}
		if mig, err := r.env.migration(ctx, rt); err == nil && mig != nil {
			paths = mig.Paths()
		}
		for _, p := range paths {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	for _, p := range []string{r.Cfg.Gno.Fees, r.Cfg.Gno.Gov} {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// executes calls Execute(id) on every queued decision whose delay has passed by block time — except a refund,
// which is GovDAO's own at-once apply (AT2): logged once, kept in the status, never attempted. Each is sized from its
// simulation as far as the block's gas (tryCall's gasUpToTheBlock); one that runs out of gas there is alarmed
// decision:too-big:<realm>#<id> and tried again hourly (tooBigFailed) until a member cancels it or it lands.
func (r *Relayer) executes(ctx context.Context) error {
	head, err := r.gno.Status(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	due, read := map[string]bool{}, map[string]bool{}
	for _, realm := range r.realms(ctx) {
		// the ledger by page (E3-RL4: the whole page runs out of query gas near 360 decisions), from the lowest
		// number that may still be pending: a decision applied, cancelled or spent never pends again
		ds, err := gnoside.DecisionsFrom(ctx, r.gno, realm, r.state.GnoLow[realm], 0)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		read[realm] = true
		low := r.state.GnoLow[realm]
		for _, d := range ds {
			if d.State == "pending" {
				break
			}
			low = d.ID + 1
		}
		if low > 1 {
			r.state.GnoLow[realm] = low
		}
		for _, d := range gnoside.DueDecisions(ds, head.Time.Unix()) {
			key := "gnoexec:" + realm + "#" + strconv.FormatInt(d.ID, 10)
			due[key] = true
			if d.Kind == "refund" {
				what := "awaiting GovDAO's apply (an apply-refund proposal, at once; Execute refuses a refund and the relayer never calls it — read hlwatch's gate first, AT2)"
				if !r.noted(key) {
					log.Printf("relayer: %s decision #%d (refund) is mature and %s", realm, d.ID, what)
				}
				r.note(key, what)
				continue
			}
			if !r.act(key) {
				continue
			}
			res, err := r.tryCall(ctx, key, realm, "Execute", r.GnoGas, gasUpToTheBlock, strconv.FormatInt(d.ID, 10))
			r.tooBigAlarm(key, "decision:too-big:"+realm+"#"+strconv.FormatInt(d.ID, 10),
				fmt.Sprintf("%s decision #%d (%s)", realm, d.ID, d.Kind),
				"a member cancels it and decides it again in parts (a void's range as several ranges by height, a list as shorter lists)")
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
	r.forgetGoneGnoExecutes(due, read)
	return firstErr
}

// forgetGoneGnoExecutes drops the bookkeeping of every queued decision of a realm whose ledger read this tick that is
// no longer due there — applied, cancelled or spent (a member's cancel is the cure of one too big for a block) — and
// its alarms with it, as forgetGoneExecutes does for HlAdmin's (G27).
func (r *Relayer) forgetGoneGnoExecutes(due, read map[string]bool) {
	keys := keysOf(r.state.Ready)
	for k := range r.state.Attempts {
		keys[k] = true
	}
	for key := range keys {
		rest, ok := strings.CutPrefix(key, "gnoexec:")
		if !ok || due[key] {
			continue
		}
		if realm, _, ok := strings.Cut(rest, "#"); !ok || !read[realm] {
			continue
		}
		r.forget(key)
		r.Alarm.Clear("decision:too-big:" + rest)
	}
}

// finishProposals calls r/gov/dao's ExecuteOrRejectProposal on every builder-made proposal that has the votes and is
// past its deadline (the realms' 14 days divided by their clock factor), counted from when this relayer first saw
// it open — after the yield only (M1k: before it a builder's closure refuses GovDAO and denies nothing).
func (r *Relayer) finishProposals(ctx context.Context) error {
	if r.Cfg.Gno.DAO == "" {
		return nil
	}
	mb, err := r.env.mailboxJSON(ctx)
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
	// from the floor the state keeps (the list read down to the lowest proposal open at its last whole reading); a list
	// not read whole is said — alarmed once it has stood two minutes — never read as "none" (F17b's Q3: the parse
	// failures hlwatch says, this relayer passed over in silence, finishing nothing and forgetting when it first saw
	// each proposal open); the proposals it did read are finished all the same
	props, why, unread, floor := gnoside.ReadProposalsFrom(ctx, r.gno, r.Cfg.Gno.DAO, head.Height, gnoside.DefaultSupermajority, r.state.ProposalsFloor, r.realms(ctx)...)
	if floor != nil {
		r.state.ProposalsFloor = floor
	}
	whole := why == "" && len(unread) == 0
	r.proposalsRead(why, unread)
	if props == nil && why != "" {
		return errors.New(why)
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
			r.Alarm.Clear("proposal:too-big:" + pk) // not tried while it lacks the votes
			continue
		}
		if !r.act(key) {
			continue
		}
		res, err := r.tryCall(ctx, key, r.Cfg.Gno.DAO, "ExecuteOrRejectProposal", r.GnoGas, gasUpToTheBlock, pk)
		r.tooBigAlarm(key, "proposal:too-big:"+pk, fmt.Sprintf("GovDAO proposal #%d (%s), past its deadline with the votes,", p.ID, p.Title),
			"GovDAO's members decide it anew in parts (a void's range as several ranges by height, a list as shorter lists); this proposal stays open")
		if err != nil {
			if !errors.Is(err, errNotYet) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		r.forget(key)
		r.note(key, "finished (executed, or denied past its deadline): "+res.Hash)
		log.Printf("relayer: GovDAO proposal #%d (%s) past its deadline: ExecuteOrRejectProposal sent: %s", p.ID, p.Title, res.Hash)
	}
	for pk := range r.state.ProposalsSeen {
		if whole && !open[pk] { // a list not read whole may hold it open still: its first sighting stays
			delete(r.state.ProposalsSeen, pk)
			r.forget("finish:" + pk)
			r.Alarm.Clear("proposal:too-big:" + pk)
		}
	}
	return firstErr
}

// proposalsUnreadAfter is how long GovDAO's list may go unread whole before the relayer alarms it (a node's hiccup
// passes; a render it cannot parse stands).
const proposalsUnreadAfter = 2 * time.Minute

// proposalsRead alarms proposals:unread once GovDAO's list has gone unread whole for proposalsUnreadAfter — a list this
// reader does not parse, one past its pages, an open proposal whose page did not read — and clears it at a reading of
// it whole.
func (r *Relayer) proposalsRead(why string, unread []string) {
	if why == "" && len(unread) == 0 {
		r.proposalsBad = time.Time{}
		r.Alarm.Clear("proposals:unread")
		r.mu.Lock()
		delete(r.last, "proposals:unread") // its status line goes with it (the review of I3's follow-ups, finding 6)
		r.mu.Unlock()
		return
	}
	now := r.now()
	if r.proposalsBad.IsZero() {
		r.proposalsBad = now
	}
	what := why
	if len(unread) > 0 {
		if what != "" {
			what += "; "
		}
		what += "open proposals whose page did not read: " + strings.Join(unread, "; ")
	}
	r.note("proposals:unread", what)
	if now.Sub(r.proposalsBad) >= proposalsUnreadAfter {
		r.Alarm.Alarm("proposals:unread", "GovDAO's list ("+r.Cfg.Gno.DAO+") is not read whole since "+r.proposalsBad.UTC().Format(time.RFC3339)+": "+what+" — a builder-made proposal there with the votes and past its deadline is not finished by this relayer meanwhile (ExecuteOrRejectProposal), and its first sighting is kept")
	}
}

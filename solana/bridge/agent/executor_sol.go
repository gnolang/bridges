package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The relayer's executor on the admin program (PLAN.md §6.5; M2-NOTES 11): anyone executes a matured decision,
// and the relayers do. Execute is attempted once a decision's clock has passed by the Clock's time at the
// compiled factor: for a replacement of guardians under a hold (kind 5) that is the later of its own 7th day and
// the 21st day of the earliest hold overlapping its pending life,
// read from the state's hold history (AN4: nothing is written, no earlier attempt is needed); for a cancelled
// decision at any time, since Execute closes it. A decision a hold defers until the hold ends, one a lone flag
// defers, and a kind 8 whose buffer is not staged are left alone. The program's NotMature and Deferred answers
// are waits, never failures; a LayoutFixed refusal is reported and never retried (nothing a retry
// could change); NeedsExtend is answered with one ExtendProgram for the whole excess of the buffer over the
// program data — the loader's own permissionless instruction, sent top-level (F1: mainnet's runtime lets no
// program CPI an extend), any amount at or above its 10,240-byte minimum, one extension a block (F7), under its
// own action bookkeeping with the cost alarmed first (G18) — then Execute again next tick; any other refusal
// follows §5.7's parking. The primary acts at once, the backup 60 s later.

// executeWhen says whether Execute is attempted now for a pending decision, and why (or why not).
func executeWhen(d *sol.Decision, st *sol.AdminState, hold *sol.Flag, adminProgram solana.PublicKey, factor, now int64) (bool, string) {
	if st.IsCancelled(d.Number) {
		return true, "cancelled: Execute closes it"
	}
	if d.Kind == sol.KindUpgrade && d.RefundKey == nil {
		if p, err := sol.ParseParams(d.Kind, d.Params); err == nil && !p.Program.Equals(adminProgram) {
			return false, "the upgrade's buffer is not staged (its writer's stage upgrade is awaited)"
		}
	}
	if hold != nil {
		if d.HeldUntilTheEnd() {
			return false, "deferred by the hold on the admin program since " + when(hold.Time) + " until it ends"
		}
	} else if d.HeldUntilTheEnd() && st.Guardians.AnyFlag() {
		return false, "deferred by a flag on the admin program"
	}
	at := d.MaturesAt(factor, hold, st)
	if now < at {
		if hold != nil && d.HeldToTheDay() && at > d.DeliveredTime+sol.Delay(d.Kind, factor) {
			return false, "matures at " + when(at) + " (the floor: day 21 of the earliest hold overlapping its pending life, from the hold history; binding while a hold stands)"
		}
		return false, "matures at " + when(at)
	}
	return true, "mature since " + when(at)
}

// judgeExecuteErr sorts a failed Execute: "wait" (NotMature, Deferred: the program's clock has the last word),
// "refused" (LayoutFixed: reported, never retried), "extend" (NeedsExtend: one growth step), "fail"
// (anything else: §5.7's parking).
func judgeExecuteErr(err error) string {
	switch {
	case solErrIs(err, "NotMature"), solErrIs(err, "Deferred"):
		return "wait"
	case solErrIs(err, "LayoutFixed"):
		return "refused"
	case solErrIs(err, "NeedsExtend"):
		return "extend"
	}
	return "fail"
}

func when(t int64) string { return time.Unix(t, 0).UTC().Format(time.RFC3339) }

// solExecutes attempts Execute on every pending decision of the admin program whose turn has come.
func (r *Relayer) solExecutes(ctx context.Context) error {
	if r.sol == nil || !r.Cfg.HasPrograms() {
		return nil
	}
	// the live admin program of the line (AQ6): what matures there is executed there; the configured program is
	// the starting point only
	chain, err := followSuccessors(ctx, r.sol.RPC, r.Cfg.Programs().AdminProgram(), rpc.CommitmentConfirmed)
	if err != nil {
		return err
	}
	r.admins = chain
	progs := chain.Programs(r.Cfg.Programs())
	admin := progs.AdminProgram()
	st := chain.State
	factor := int64(st.ClockFactor)
	if factor == 0 {
		factor = 1
	}
	now := r.now().Unix()
	hold := st.Guardians.Hold(now, sol.UnpauseDelay(factor))
	pending, err := readPendingDecisions(ctx, r.sol.RPC, admin, st, rpc.CommitmentConfirmed)
	if err != nil {
		return err
	}
	executor := r.sol.Payer.PublicKey()
	var firstErr error
	for _, d := range pending {
		key := fmt.Sprintf("solexec:%d", d.Number)
		if why, ok := r.state.Refused[key]; ok {
			r.note(key, "refused for good, never retried: "+why)
			continue
		}
		act, why := executeWhen(d, st, hold, admin.Program, factor, now)
		if !act {
			delete(r.state.Ready, key) // the backup's clock starts when the action becomes possible
			r.note(key, "waits: "+why)
			continue
		}
		if !r.act(key) {
			continue
		}
		a := r.state.Attempts[key]
		if !a.due(r.now()) {
			continue
		}
		sig, err := r.execute(ctx, progs, st, d, executor)
		if err != nil {
			switch judgeExecuteErr(err) {
			case "wait":
				// the program's clock has the last word (seconds of skew from this reading): a minute's wait,
				// counting for nothing toward parking
				if a == nil {
					a = &attempt{}
					r.state.Attempts[key] = a
				}
				a.fail(r.now(), err, false)
				r.note(key, "the program says wait ("+solErrName(err)+"); "+why)
				continue
			case "refused":
				name := solErrName(err)
				r.state.Refused[key] = name
				r.forget(key)
				r.Alarm.Alarm("execute:refused:"+key, fmt.Sprintf("decision %d (kind %d): Execute refused for good (%s), never retried: %v", d.Number, d.Kind, name, err))
				continue
			case "extend":
				r.extendOnce(ctx, d, key, executor)
				continue
			}
			if a == nil {
				a = &attempt{}
				r.state.Attempts[key] = a
			}
			if parkedNow := a.fail(r.now(), err, solDeterministic(err)); parkedNow || a.parked() {
				r.Alarm.Alarm("parked:"+key, fmt.Sprintf("Execute of decision %d parked after %d failures (retried hourly): %v", d.Number, a.Failures, err))
			}
			r.note(key, "failed: "+err.Error())
			if firstErr == nil {
				firstErr = fmt.Errorf("decision %d: %w", d.Number, err)
			}
			continue
		}
		if a.parked() {
			r.Alarm.Clear("parked:" + key)
		}
		delete(r.state.Attempts, key)
		// a success executes, spends or closes the decision: its account is gone
		after, rerr := readDecision(ctx, r.sol.RPC, admin, d.Number, rpc.CommitmentConfirmed)
		switch {
		case rerr != nil:
			r.note(key, "Execute sent ("+sig.String()+"); the decision could not be read back: "+rerr.Error())
		case after != nil:
			r.note(key, fmt.Sprintf("Execute succeeded (%s) yet the decision still stands: looked at again next tick", sig))
		default:
			r.forget(key)
			r.forgetExtend(key)
			r.note(key, "executed, spent or closed: "+sig.String())
			log.Printf("relayer: decision %d (kind %d) executed on the admin program: %s", d.Number, d.Kind, sig)
		}
	}
	r.forgetGone(pending)
	return firstErr
}

// forgetGone drops the Execute and extend bookkeeping of every decision no longer pending — the backup's clock,
// the attempts and their parked alarm, the extend plan and its cost alarm, a refusal recorded and its alarm (G27:
// a decision executed by the other relayer, by hlrecover execute or closed by a cancel left its solexec:/solext:
// entries in the state file and a standing extend alarm until a restart; only this relayer's own success branch
// forgot them).
func (r *Relayer) forgetGone(pending []*sol.Decision) {
	standing := map[string]bool{}
	for _, d := range pending {
		standing[fmt.Sprint(d.Number)] = true
	}
	keys := map[string]bool{}
	for _, m := range []map[string]bool{keysOf(r.state.Ready), keysOf(r.state.Attempts), keysOf(r.state.Extends), keysOf(r.state.Refused)} {
		for k := range m {
			keys[k] = true
		}
	}
	for key := range keys {
		var dec string
		switch {
		case strings.HasPrefix(key, "solexec:"):
			dec = strings.TrimPrefix(key, "solexec:")
		case strings.HasPrefix(key, "solext:"):
			dec = strings.TrimPrefix(key, "solext:")
		default:
			continue
		}
		if standing[dec] {
			continue
		}
		r.forget(key)
		r.forgetExtend("solexec:" + dec)
		if _, ok := r.state.Refused[key]; ok {
			delete(r.state.Refused, key)
			r.Alarm.Clear("execute:refused:" + key)
		}
	}
}

// keysOf is a map's keys as a set.
func keysOf[V any](m map[string]V) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// extendKey is the action key of a decision's program-data growth, beside its Execute key.
func extendKey(execKey string) string { return "solext:" + strings.TrimPrefix(execKey, "solexec:") }

// extendOnce sends the growth a staged kind 8 needs — the loader's own permissionless ExtendProgram, top-level,
// the executor paying the added rent (F1) — under its own action key (G18): the backup only after its offset, a
// refusal backed off and parked after three deterministic ones like any call, and the whole plan — the buffer's
// ELF against the program data, the bytes, the rent — read and alarmed (execute:extend:<decision>) before the
// first send. One ExtendProgram carries the whole excess (F7): the loader admits any amount at or above its
// 10,240-byte minimum, one extension a block, so a growth takes one block and Execute passes next tick.
func (r *Relayer) extendOnce(ctx context.Context, d *sol.Decision, execKey string, executor solana.PublicKey) {
	key := extendKey(execKey)
	dec := fmt.Sprint(d.Number)
	p, err := sol.ParseParams(d.Kind, d.Params)
	if err != nil {
		r.note(execKey, "the upgrade needs the program data extended, but its parameters do not parse: "+err.Error())
		return
	}
	if !r.act(key) {
		r.note(execKey, "the upgrade needs the program data extended; the backup extends after its offset")
		return
	}
	a := r.state.Attempts[key]
	if !a.due(r.now()) {
		r.note(execKey, "the upgrade needs the program data extended; the extend waits its backoff: "+a.LastErr)
		return
	}
	pd, err := r.solData(ctx, sol.ProgramData(p.Program))
	if err != nil {
		r.note(execKey, "the upgrade needs the program data extended; its programdata could not be read: "+err.Error())
		return
	}
	buf, err := r.solData(ctx, p.Buffer)
	if err != nil {
		r.note(execKey, "the upgrade needs the program data extended; its buffer could not be read: "+err.Error())
		return
	}
	pdLen, bufLen := len(pd)-sol.ProgramDataHeader, len(buf)-sol.BufferHeader
	add := sol.ExtendBy(pdLen, bufLen)
	if add == 0 {
		r.note(execKey, fmt.Sprintf("the program data (%d bytes) already holds the buffer's %d: Execute again next tick", pdLen, bufLen))
		return
	}
	if _, told := r.state.Extends[key]; !told {
		r.state.Extends[key] = strconv.FormatUint(uint64(add), 10)
		rent := "its rent at the chain's rate could not be read"
		if r.solRent != nil {
			if lamports, err := r.solRent(ctx, p.Program, add); err == nil {
				rent = "≈" + sol.SOL(lamports) + " of rent at the chain's rate"
			} else {
				rent += " (" + err.Error() + ")"
			}
		}
		r.Alarm.Alarm("execute:extend:"+dec, fmt.Sprintf("decision %s (kind 8): the program data of %s holds %d bytes, the buffer %s carries %d: one ExtendProgram of %d bytes, %s, from the relayer's float, before Execute", dec, p.Program, pdLen, p.Buffer, bufLen, add, rent))
	}
	sig, err := r.solSend(ctx, []solana.Instruction{sol.ExtendProgramIx(p.Program, executor, add)})
	if err != nil {
		if a == nil {
			a = &attempt{}
			r.state.Attempts[key] = a
		}
		if parkedNow := a.fail(r.now(), err, solDeterministic(err)); parkedNow || a.parked() {
			r.Alarm.Alarm("parked:"+key, fmt.Sprintf("the extend of decision %s's program data parked after %d failures (retried hourly): %v", dec, a.Failures, err))
		}
		r.note(execKey, "the upgrade needs the program data extended, which failed: "+err.Error())
		return
	}
	if a.parked() {
		r.Alarm.Clear("parked:" + key)
	}
	delete(r.state.Attempts, key)
	r.note(execKey, fmt.Sprintf("the program data extended by %d bytes in one ExtendProgram (%s); Execute again next tick", add, sig))
	log.Printf("relayer: decision %s: the program data of %s extended by %d bytes (%s)", dec, p.Program, add, sig)
}

// forgetExtend drops a decision's extend bookkeeping and its cost alarm once the decision is gone.
func (r *Relayer) forgetExtend(execKey string) {
	key := extendKey(execKey)
	r.forget(key)
	delete(r.state.Extends, key)
	r.Alarm.Clear("execute:extend:" + strings.TrimPrefix(execKey, "solexec:"))
}

// ExtendTerms is what the published tool's caller allows a growth to cost (AP5): a NeedsExtend is answered only
// after the growth's size and rent are printed to Out, and above MaxRent lamports only when Confirmed (-yes).
type ExtendTerms struct {
	MaxRent   uint64
	Confirmed bool
	Out       io.Writer
}

// extendPlan is the growth a NeedsExtend asks and whether the caller's terms allow it (AP5, pure): the bytes
// (sol.ExtendBy), the line that says the size and the rent, and a refusal above the bound without -yes.
func extendPlan(program solana.PublicKey, pdLen, bufLen int, rent uint64, terms ExtendTerms) (add uint32, line string, err error) {
	add = sol.ExtendBy(pdLen, bufLen)
	if add == 0 {
		return 0, "", fmt.Errorf("the program says NeedsExtend, yet the program data (%d bytes) holds the buffer's %d", pdLen, bufLen)
	}
	line = fmt.Sprintf("the program data of %s holds %d bytes, the buffer carries %d: one ExtendProgram of %d bytes costs ≈%s of rent at the chain's rate, from the payer, never returned", program, pdLen, bufLen, add, sol.SOL(rent))
	if rent > terms.MaxRent && !terms.Confirmed {
		return add, line, fmt.Errorf("%s — above -max-rent (%s); run again with -yes to pay it", line, sol.SOL(terms.MaxRent))
	}
	return add, line, nil
}

// ExecuteDecision is the published tool's hand on a pending decision (AO1: "anyone executes it then", §6.5 —
// the operator's relayer was the only sender): it reads the decision, judges its clock as the executor does,
// and sends one Execute with the kind's accounts from any funded key; a NeedsExtend is answered with one
// ExtendProgram for the whole excess (F7) — its size and rent printed first, refused above the caller's
// -max-rent without -yes (AP5) — and the caller runs it again next block. It answers what it did.
func ExecuteDecision(ctx context.Context, c *sol.Client, progs sol.Programs, number uint64, now time.Time, terms ExtendTerms) (string, error) {
	// the live admin program of the line (AQ6)
	chain, err := followSuccessors(ctx, c.RPC, progs.AdminProgram(), rpc.CommitmentConfirmed)
	if err != nil {
		return "", err
	}
	progs = chain.Programs(progs)
	admin := progs.AdminProgram()
	st := chain.State
	d, err := readDecision(ctx, c.RPC, admin, number, rpc.CommitmentConfirmed)
	if err != nil {
		return "", err
	}
	if d == nil {
		return "", fmt.Errorf("no pending decision %d (executed, cancelled, spent, or never recorded)", number)
	}
	factor := int64(st.ClockFactor)
	if factor == 0 {
		factor = 1
	}
	hold := st.Guardians.Hold(now.Unix(), sol.UnpauseDelay(factor))
	if act, why := executeWhen(d, st, hold, admin.Program, factor, now.Unix()); !act {
		return "", fmt.Errorf("decision %d (kind %d) is not executable now: %s", number, d.Kind, why)
	}
	executor := c.Payer.PublicKey()
	var extra solana.AccountMetaSlice
	var flagsW bool
	p, perr := sol.ParseParams(d.Kind, d.Params)
	switch {
	case st.IsCancelled(d.Number):
		if d.Kind == sol.KindUpgrade && d.RefundKey != nil && perr == nil {
			extra = admin.ExtraForCancelledUpgrade(p.Buffer, *d.RefundKey)
		}
	case perr == nil:
		if d.RefundKey != nil {
			p.RefundKey = *d.RefundKey
		}
		extra, flagsW = admin.ExtraFor(progs, d.Kind, p)
	}
	ix := admin.ExecuteIx(progs, d.Number, d.Deliverer, executor, extra, flagsW)
	sig, err := c.Send(ctx, []solana.Instruction{sol.ComputeLimitIx(400_000), ix})
	if err == nil {
		return fmt.Sprintf("decision %d (kind %d) executed: %s", number, d.Kind, sig), nil
	}
	if judgeExecuteErr(err) != "extend" || perr != nil {
		return "", fmt.Errorf("Execute of %d (%s): %w", number, orNone(solErrName(err)), err)
	}
	pd, err := accountData(ctx, c.RPC, sol.ProgramData(p.Program))
	if err != nil {
		return "", err
	}
	buf, err := accountData(ctx, c.RPC, p.Buffer)
	if err != nil {
		return "", err
	}
	pdLen, bufLen := len(pd)-sol.ProgramDataHeader, len(buf)-sol.BufferHeader
	rent, err := sol.ExtendRent(ctx, c.RPC, p.Program, sol.ExtendBy(pdLen, bufLen))
	if err != nil {
		return "", fmt.Errorf("the growth's rent could not be read: %w", err)
	}
	add, line, err := extendPlan(p.Program, pdLen, bufLen, rent, terms)
	if err != nil {
		return "", err
	}
	if terms.Out != nil {
		fmt.Fprintf(terms.Out, "decision %d (kind 8): %s\n", number, line)
	}
	esig, err := c.Send(ctx, []solana.Instruction{sol.ExtendProgramIx(p.Program, executor, add)})
	if err != nil {
		return "", fmt.Errorf("extending %s's program data by %d bytes: %w", p.Program, add, err)
	}
	return fmt.Sprintf("decision %d (kind 8): the program data of %s extended by %d bytes (≈%s of rent, %s); run execute again next block", number, p.Program, add, sol.SOL(rent), esig), nil
}

// execute sends one Execute with the kind's accounts after the six fixed ones: a cancelled staged upgrade's
// buffer return, else the kind's own (kinds.rs::describe); kind 4 (reserved) and parameters that do not parse
// take none, since Execute spends them.
func (r *Relayer) execute(ctx context.Context, progs sol.Programs, st *sol.AdminState, d *sol.Decision, executor solana.PublicKey) (solana.Signature, error) {
	admin := progs.AdminProgram()
	var extra solana.AccountMetaSlice
	var flagsW bool
	p, perr := sol.ParseParams(d.Kind, d.Params)
	switch {
	case st.IsCancelled(d.Number):
		if d.Kind == sol.KindUpgrade && d.RefundKey != nil && perr == nil {
			extra = admin.ExtraForCancelledUpgrade(p.Buffer, *d.RefundKey)
		}
	case perr == nil:
		if d.RefundKey != nil {
			p.RefundKey = *d.RefundKey
		}
		extra, flagsW = admin.ExtraFor(progs, d.Kind, p)
	}
	ix := admin.ExecuteIx(progs, d.Number, d.Deliverer, executor, extra, flagsW)
	return r.sol.Send(ctx, []solana.Instruction{sol.ComputeLimitIx(r.SolUnits), ix})
}

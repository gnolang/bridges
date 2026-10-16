package agent

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The monitor's Solana side (PLAN.md §5.9): the client's state from both providers inside the snapshot (the
// trusted height T the gno read must reach, or the client is past the chain), the header record held against the
// own node (the fork rule), the decisions against r/hlgov's list (a foreign decision), the watch
// key's flags on the pausable ISM and the admin program, `sends` on the gno routes while deliveries are paused,
// and the client's one-hour lag. Every reading here comes from both providers alike (G6): the record's
// transactions, the admin and pausable accounts, the pending decisions, the processed-message records; one they
// give differently alarms and skips its rule for the poll.

// readClientBoth reads the client's state from both providers; they must agree on what the watch reads.
func (m *Monitor) readClientBoth(ctx context.Context) (*clientState, error) {
	client := m.Cfg.Programs().LightClient()
	var out [2]*clientState
	for i, c := range m.sol {
		cs, err := readClientState(ctx, c, client, rpc.CommitmentFinalized)
		if err != nil {
			return nil, fmt.Errorf("the client from provider %d: %w", i+1, err)
		}
		out[i] = cs
	}
	a, b := out[0], out[1]
	if a.Head.TrustedHeight != b.Head.TrustedHeight || a.Head.TrustedHash != b.Head.TrustedHash || a.Body.Records != b.Body.Records || a.Body.RunningHash != b.Body.RunningHash {
		return nil, fmt.Errorf("%w on the client (trusted %d/%d, records %d/%d)", errProviders, a.Head.TrustedHeight, b.Head.TrustedHeight, a.Body.Records, b.Body.Records)
	}
	a.Slot = min(a.Slot, b.Slot) // the lower of the two answers, as the pair takes it (E3-GT9: the poll holds it to the supplies' slot)
	return a, nil
}

// solChecks runs the Solana-side rules once the cut holds: the record, the decisions, the pause mirror, the day
// caps against the locks, the lag.
func (m *Monitor) solChecks(ctx context.Context, cs *clientState, mb *mailboxJSON, routes map[string]*routeJSON, H int64) {
	progs := m.Cfg.Programs()
	// the client's lag (alarm only)
	if lag := m.now().Sub(time.Unix(cs.Head.LastAcceptedTime, 0)); lag > ClientLagAfter {
		m.Alarm.Alarm("client:lag", fmt.Sprintf("the client accepted no header for %s (trusted height %d)", lag.Round(time.Minute), cs.Head.TrustedHeight))
	} else {
		m.Alarm.Clear("client:lag")
	}
	m.mu.Lock()
	m.status.Client = map[string]any{"trustedHeight": cs.Head.TrustedHeight, "trustedHash": hex.EncodeToString(cs.Head.TrustedHash[:]), "records": cs.Body.Records,
		"lastAccepted": time.Unix(cs.Head.LastAcceptedTime, 0).UTC().Format(time.RFC3339), "quarantineEnd": cs.Head.QuarantineEnd, "setFrom": cs.Body.CurrentSetFrom(), "clockFactor": cs.Head.ClockFactor}
	m.mu.Unlock()

	// the validator set against the client's measured bound (G19/AO4): the heap frame is due before it
	m.setBound(len(cs.Body.Set), len(cs.Body.NextSet))

	// the record against the own node (the fork rule), and against the state's own counter (a gap); its
	// transactions from both providers, up to the snapshot's slot (G14); a scan that keeps failing — a
	// transaction the provider cannot serve or this build cannot read — alarms once it has stood two minutes
	// (G29: it was a log line, the record "behind" for good and the fork rule blind)
	if err := m.state.Record.Sync(ctx, m.both, progs.LightClient(), progs.Mailbox, cs.Slot); err != nil {
		m.solErr("the client's record", err)
		m.persist("record:scan", "the client's record scan fails (the fork and gap rules are blind until it passes; a fresh install on a provider without the full history starts from a checkpoint with -record-from client): "+err.Error(), cutFailAfter)
	} else {
		m.clearPersisted("record:scan")
	}
	mm, err := m.state.Record.Check(ctx, m)
	if err != nil {
		log.Printf("monitor: checking the record: %v", err)
	}
	if mm == nil {
		mm = m.checkpointedHeader(ctx, cs) // a checkpointed record never reaches the trusted header (G47)
	}
	m.recordMismatch(ctx, mm)
	if ok, why := m.state.Record.Consistent(cs.Body.Records, cs.Body.RunningHash, cs.Slot); ok {
		m.clearPersisted("header:gap")
	} else if len(why) > 7 && why[:7] == "behind:" {
		m.clearPersisted("header:gap") // a scan catching up is no gap, and no start of one (G14)
	} else {
		m.seenFor("header:gap", cutFailAfter, func() { m.flagSolana(ctx, "header:gap", "the client's header record: "+why, true) })
	}

	// the admin program's state, read once for the rules below (both providers): the live program of the line
	// followed from the configured id (AQ6: every rule and flag below acts on it from the block a kind 9 executes)
	var st *sol.AdminState
	chain, err := followSuccessors(ctx, m.both, progs.AdminProgram(), rpc.CommitmentFinalized)
	m.noteAdminLine(err)
	if err != nil {
		m.solErr("the admin program", err)
	} else {
		st = chain.State
		m.admins = chain
		if text := chain.Text(); text != "" {
			m.Alarm.AlarmEvery("admin:successor", text+": every rule and flag of this monitor acts on it; the config names the starting point only (AQ6)", 24*time.Hour)
		} else {
			m.Alarm.Clear("admin:successor")
		}
	}

	// every decision against r/hlgov's list
	if st != nil {
		m.foreignDecisions(ctx, chain, cs, H)
	}

	// deliveries paused → sends on the gno routes; resumed → cleared
	paused, why, ps, _, ok := m.deliveriesPaused(ctx, cs)
	if ok {
		m.mirrorSends(ctx, mb, paused, why)
		m.capRatios(ps, routes, mb)
	}
}

// noteAdminLine alarms decision:line-unfollowed once the admin program's line of succession has failed to read
// for two minutes (F24: past successorHops, or on a loop or an unparseable state, the decision rules went quiet
// and the relayer logged a line a tick — nothing named the chore); cleared by a read. The text names it: the
// plan's "a fifth admin successor returns the tools to a config edit".
func (m *Monitor) noteAdminLine(err error) {
	if err == nil {
		m.clearPersisted("decision:line-unfollowed")
		return
	}
	m.persist("decision:line-unfollowed", "the admin program's line of succession cannot be followed from the configured program ("+err.Error()+"): the decision rules, the pending list and the flags on the admin program are blind until it can — past four successions, edit solana.admin in bridge.json to the live program (§6.5: a fifth successor returns the tools to a config edit); a loop or an unparseable state is a forged line, which the pausable half of the stroke still pauses (F24, G40)", cutFailAfter)
}

// seenFor runs act once a condition has stood for d (the gap rule must not fire on a scan that is catching up).
func (m *Monitor) seenFor(key string, d time.Duration, act func()) {
	if m.persisted(key, d) {
		act()
	}
}

// recordMismatch applies the record's verdict with the cut's patience (G15): a mismatch flags the pausable ISM
// and the admin program once it has stood cutFailAfter, an alarm before that; none clears both.
func (m *Monitor) recordMismatch(ctx context.Context, mm *Mismatch) {
	if mm == nil {
		m.clearPersisted("header:mismatch")
		return
	}
	text := "header mismatch (a fork or a forged header): " + mm.String()
	if m.persisted("header:mismatch", cutFailAfter) {
		m.flagSolana(ctx, "header:mismatch", text, true)
		return
	}
	m.Alarm.Alarm("header:mismatch", "flagged once it has stood "+cutFailAfter.String()+": "+text)
}

// setBound alarms client:set-bound when the current or an announced validator set has sol.SetSizeWarn members
// or more (G19/AO4): the client's measured bound is about sol.SetSizeBound through any history (§6.1), and the
// heap frame that lifts it is due before gno.land nears it — an alarm here, not a wedged client later.
func (m *Monitor) setBound(current, announced int) {
	if text := setBoundText(current, announced); text != "" {
		m.Alarm.Alarm("client:set-bound", text)
		return
	}
	m.Alarm.Clear("client:set-bound")
}

// setBoundText is the client:set-bound alarm's text for a current and an announced set size, "" below the
// warning size.
func setBoundText(current, announced int) string {
	n := max(current, announced)
	if n < sol.SetSizeWarn {
		return ""
	}
	which := "the current validator set"
	if announced > current {
		which = "an announced validator set"
	}
	return fmt.Sprintf("%s has %d validators (current %d, announced %d): the client's measured bound is about %d through any history; the heap frame is due", which, n, current, announced, sol.SetSizeBound)
}

// BlockHash is the own node's block hash at a height ("" when it has none), and its head.
func (m *Monitor) BlockHash(ctx context.Context, height int64) (string, int64, error) {
	head, err := m.own.Status(ctx)
	if err != nil {
		return "", 0, err
	}
	if height > head.Height {
		return "", head.Height, nil
	}
	b, err := m.own.Block(ctx, height)
	if err != nil {
		return "", head.Height, err
	}
	return b.Hash, head.Height, nil
}

// foreignDecisions compares every number the admin program has recorded with r/hlgov's: a
// number r/hlgov never dispatched, a pending decision whose body is not r/hlgov's, or a 7-day decision of
// r/hlgov's gone inside its week (G32), is foreign (§5.9). The admin program is the live one of the line (AQ6).
func (m *Monitor) foreignDecisions(ctx context.Context, chain *adminChain, cs *clientState, H int64) {
	if m.Cfg.Gno.Gov == "" {
		return
	}
	st := chain.State
	gov, err := ReadGov(ctx, m.own, m.Cfg.Gno.Gov, H)
	if err != nil {
		log.Printf("monitor: r/hlgov: %v", err)
		return
	}
	// the gone-decision rule's context (G38): the retired programs of the line
	now := m.now().Unix()
	_ = cs
	findings, checked, err := foreignFindings(ctx, m.both, m.own, m.Cfg, chain.Live, st, H, m.state.DecisionsChecked, st.LastSeq, gov.Last, now, foreignCtxOf(chain))
	if err != nil {
		m.solErr("the decisions", err) // the poll is skipped at the erroring number: checked stays below it (G23)
	}
	m.applyForeign(ctx, findings, checked, err)
	m.mu.Lock()
	m.status.Decisions = map[string]any{"adminProgram": chain.Live.Program.String(), "retired": chain.RetiredHex(), "adminLast": st.LastSeq, "govLast": gov.Last, "checked": m.state.DecisionsChecked, "foreign": strings.Join(findings, "; ")}
	m.mu.Unlock()
	if st.LastSeq > gov.Last {
		m.Alarm.Alarm("decision:counter", fmt.Sprintf("the admin program's last number %d is past r/hlgov's %d: a foreign decision took a number, or GovDAO's was lost", st.LastSeq, gov.Last))
	} else {
		m.Alarm.Clear("decision:counter")
	}
}

// applyForeign applies the decision rule's findings with the cut's patience, as the record's mismatch is (G23):
// a foreign finding flags the pausable ISM and the admin program once it has stood cutFailAfter, the alarm
// decision:foreign:pending before that; the numbers checked advance past a finding only when it is flagged
// (examined once, as before: the flag on the objects is the record, and the monitor never clears it), and past
// a clean poll's numbers at once. A poll that errored (the head re-read among them) advances nothing, raises
// nothing and keeps a standing sighting; a clean poll with no finding clears the pending alarm and the sighting.
func (m *Monitor) applyForeign(ctx context.Context, findings []string, checked uint64, pollErr error) {
	if len(findings) == 0 {
		m.state.DecisionsChecked = checked
		if pollErr == nil {
			m.Alarm.Clear("decision:foreign:pending")
			delete(m.seen, "decision:foreign")
		}
		return
	}
	text := strings.Join(findings, "; ")
	if m.persisted("decision:foreign", cutFailAfter) {
		m.Alarm.Clear("decision:foreign:pending")
		delete(m.seen, "decision:foreign")
		m.flagSolana(ctx, "decision:foreign", text, true)
		m.state.DecisionsChecked = checked
		return
	}
	m.Alarm.Alarm("decision:foreign:pending", "flagged once it has stood "+cutFailAfter.String()+": "+text)
}

// govBody is the body r/hlgov dispatched under a number, nil if none.
func govBody(ctx context.Context, g gnoReader, gov string, n uint64, height int64) ([]byte, error) {
	b, _, err := govDecision(ctx, g, gov, n, height)
	return b, err
}

// govDecision is the body r/hlgov dispatched under a number and when it was first dispatched (the realm's `at`),
// nil and 0 if none.
func govDecision(ctx context.Context, g gnoReader, gov string, n uint64, height int64) ([]byte, int64, error) {
	s, err := g.RenderAt(ctx, gov, "decision/"+strconv.FormatUint(n, 10), height)
	if err != nil {
		return nil, 0, err
	}
	if trim(s) == "null" {
		return nil, 0, nil
	}
	var j struct {
		Body string `json:"body"`
		At   int64  `json:"at"`
	}
	if err := jsonUnmarshal(s, &j); err != nil {
		return nil, 0, err
	}
	b, err := hex.DecodeString(j.Body)
	if err != nil || len(b) < 9 {
		return nil, 0, fmt.Errorf("r/hlgov's decision %d: a body of %d bytes", n, len(b))
	}
	return b, j.At, nil
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\n' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// deliveriesPaused reads whether gno → Solana deliveries are paused: any flag on the pausable ISM (both
// providers agreeing), or a quarantine standing by the Clock's time; ok false when the flags could not be read.
// It answers the pausable ISM's state (the mint limits) and guardians too, for the rules that read them.
func (m *Monitor) deliveriesPaused(ctx context.Context, cs *clientState) (paused bool, why string, ps *sol.PausableState, g *sol.Guardians, ok bool) {
	ps, g, err := readPausable(ctx, m.both, m.Cfg.Programs().PausableISM(), rpc.CommitmentFinalized)
	if err != nil {
		m.solErr("the pausable ISM", err)
		return false, "", nil, nil, false
	}
	now := m.now().Unix()
	m.mu.Lock()
	m.status.Pausable = map[string]any{"anyFlag": g.AnyFlag(), "guardianFlags": g.GuardianFlags(), "hold": g.Hold(now, sol.UnpauseDelay(int64(cs.Head.ClockFactor))) != nil, "quarantineEnd": cs.Head.QuarantineEnd}
	m.mu.Unlock()
	paused, why = deliveriesPausedNow(g, cs.Head.QuarantineEnd, now)
	return paused, why, ps, g, true
}

// capRatios alarms mint:cap-ratio:<token> while a token's day cap on the pausable ISM exceeds a fifth of its
// lock on gno.land (AP6: §5.4 bounds a forged day's mint before the flags by a tenth of the lock, but the cap is
// an absolute number compiled from the lock the launch notice expected and moved by kind 12 alone — in the
// launch week, or after an exit wave, the lock can be far below it), cleared when the cap is within it. The lock
// is the pair's sum while a migration is open (AQ3); the rule is quiet while the lock is still below the
// expected first-week lock the cap was set against (C341: by construction it stood from launch until the lock
// reached half the expectation), and judges the cap from the poll the lock reaches it, for good — a lock that
// later shrinks below five caps alarms; the alarm repeats daily on the webhook, not hourly (G35).
func (m *Monitor) capRatios(ps *sol.PausableState, routes map[string]*routeJSON, mb *mailboxJSON) {
	for _, r := range m.Cfg.Routes {
		rj := routes[r.Symbol]
		i, ok := tokenIndex(r.Symbol)
		if rj == nil || !ok || r.Token == "" || m.lineUnread(r.Symbol) {
			continue // a line not read this poll: the lock is the line's sum, unknown (G44)
		}
		lock := rj.Lock
		if mig := m.migs[r.Symbol]; mig != nil {
			lock, _, _, _ = mig.Sum()
		}
		key := "mint:cap-ratio:" + r.Symbol
		cr := capRatio(r.Symbol, ps.Limits[i].DayCap, lock, r.ExpectedLock, m.state.CapReached[r.Symbol], capQuietEnds(mb), m.now().Unix())
		m.state.CapReached[r.Symbol] = cr.Reached
		if cr.Alarm != "" {
			m.Alarm.AlarmEvery(key, cr.Alarm, 24*time.Hour)
		} else {
			m.Alarm.Clear(key)
		}
		// the live realm's per-message maximum against the ISM's (AS4): the line's last realm takes the sends
		// while a migration is open
		routeMax := rj.Maximum
		if mig := m.migs[r.Symbol]; mig != nil && mig.Succ != nil {
			routeMax = mig.Succ.Maximum
		}
		mmKey := "limits:mismatch:" + r.Symbol
		if text := limitsMismatch(r.Symbol, routeMax, ps.Limits[i]); text != "" {
			m.Alarm.AlarmEvery(mmKey, text, 24*time.Hour)
		} else {
			m.Alarm.Clear(mmKey)
		}
	}
}

// capVerdict is the mint:cap-ratio rule's answer for one token (C341).
type capVerdict struct {
	Alarm   string // the alarm's text, "" when none
	Note    string // why the cap is not judged yet, "" when it is
	Reached bool   // the cap is judged from now on (kept between polls)
}

// capQuietEnds is when the cap-ratio quiet expires by the clock (AR5): the realms' 14-day deadline after
// hlmailbox's launch (`launchedAt`, at the chain's clock factor); 0 before the launch, or with no mailbox read.
func capQuietEnds(mb *mailboxJSON) int64 {
	if mb == nil || mb.LaunchedAt <= 0 {
		return 0
	}
	return mb.LaunchedAt + int64(mb.Clocks.DeadlineDuration()/time.Second)
}

// capRatio judges a token's day cap against its lock (AP6) once the quiet of C341 has ended: the lock reached
// the expected first-week lock the config names, or the later of two bounds on the quiet — the lock reached
// five day caps (G43: the alarm is silent there anyway, so a lie in `expectedLock` delays the first judgement by
// at most the climb to five caps and never the standing after it) or 14 days passed since the launch (AR5: a
// pool that never fills is exactly where the cap exceeds the lock, and the quiet had no clock). Before that the
// rule is quiet and says so; with no expected lock configured the cap is judged from the first poll, as before.
// reached is the token's standing from the last poll, kept for good; quietEnds the clock's bound (0 for none).
func capRatio(symbol string, dayCap uint64, lock, expected int64, reached bool, quietEnds, now int64) capVerdict {
	fiveCaps := dayCap > 0 && lock >= 0 && uint64(lock) >= 5*dayCap
	expired := quietEnds > 0 && now >= quietEnds
	v := capVerdict{Reached: reached || expected <= 0 || lock >= expected || fiveCaps || expired}
	if !v.Reached {
		until := "never by the clock: no launch time read"
		if quietEnds > 0 {
			until = time.Unix(quietEnds, 0).UTC().Format(time.RFC3339)
		}
		v.Note = fmt.Sprintf("%s's day cap (%d) is not judged against its lock (%d) yet: the lock is below the expected first-week lock of %d it was set against (C341); judged from the poll the lock reaches it, or five day caps (%d, G43), or 14 days after the launch (%s, AR5)", symbol, dayCap, lock, expected, 5*dayCap, until)
		return v
	}
	v.Alarm = capRatioText(symbol, dayCap, lock)
	return v
}

// limitsMismatch is the limits:mismatch alarm's text while a route's live per-message maximum exceeds the
// pausable ISM's for the token (AS4: a send between the two is taken on gno.land and parked for good on Solana,
// OverMax until a kind 12 raises the ISM's — §5.4's order is Solana first, gno.land after), "" at or below it.
func limitsMismatch(symbol string, routeMax int64, l sol.MintLimit) string {
	if routeMax <= 0 || uint64(routeMax) <= l.MaxPerMessage {
		return ""
	}
	return fmt.Sprintf("%s's per-message maximum on gno.land, %d, is above the pausable ISM's %d (base units): a send between the two is taken on gno.land and parked for good on Solana (OverMax, until a kind 12) — raise the pausable ISM's by kind 12 first, or lower the route's (§5.4, AS4)", symbol, routeMax, l.MaxPerMessage)
}

// capRatioText is the mint:cap-ratio alarm's text for a token's day cap against its lock, "" within a fifth.
func capRatioText(symbol string, dayCap uint64, lock int64) string {
	if lock > 0 && dayCap <= uint64(lock)/5 {
		return ""
	}
	return fmt.Sprintf("%s's day cap on the pausable ISM, %d, is above a fifth of its lock on gno.land, %d (§5.4 bounds a forged day's mint before the flags by a tenth of the lock; the cap is an absolute number moved by kind 12 alone): lower it by kind 12, or wait for the lock to grow", symbol, dayCap, lock)
}

// mirrorSends sets the watch key's `sends` flag on every gno route while deliveries are paused and clears its
// own when they resume (§5.9: never credits or payouts for that).
func (m *Monitor) mirrorSends(ctx context.Context, mb *mailboxJSON, paused bool, why string) {
	if paused {
		m.Alarm.Alarm("deliveries:paused", "gno → Solana deliveries are paused ("+why+"): sends flagged on the gno routes")
		for _, path := range m.liveRoutes() {
			m.setFlag(ctx, mb, path, "sends", why)
		}
		m.state.SendsSet = true
		return
	}
	m.Alarm.Clear("deliveries:paused")
	if !m.state.SendsSet {
		return
	}
	for _, path := range m.liveRoutes() {
		if rf, ok := mb.Routes[path]; ok {
			for _, f := range rf.StandingFlags() {
				if f.Seat == m.seat && f.Fn == "sends" {
					m.clearFlag(ctx, path, "sends")
				}
			}
		}
	}
	m.state.SendsSet = false
}

// liveRoutes are the realms a route flag goes on: every configured route and, while a migration is open, the
// token's successor too (AQ3: the live realm of the token is the successor; the predecessor refuses every
// function on its own).
func (m *Monitor) liveRoutes() []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range m.Cfg.Routes {
		for _, p := range []string{r.Gno, m.successorOf(r.Symbol)} {
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// successorOf is a token's successor realm while its migration is open, "" otherwise.
func (m *Monitor) successorOf(symbol string) string {
	if mig := m.migs[symbol]; mig != nil {
		return mig.Successor
	}
	return ""
}

// flagSolana sets the watch key's flag on the pausable ISM — and on the admin program too for the header and
// decision rules — through the Solana watch key, re-asserted with a fresh slot while the condition stands, at
// most once a minute across every condition (§5.9, G12). Without the key it alarms; while the own node is the
// odd one out, the header rules alarm only.
func (m *Monitor) flagSolana(ctx context.Context, key, text string, adminToo bool) {
	if m.state.OddNode && adminToo {
		m.Alarm.Alarm(key, "alarm only while the own node is the odd one out: "+text)
		return
	}
	m.Alarm.Flag(key, text)
	if m.solWatch == nil {
		m.Alarm.Alarm("flag:solana", "would flag the pausable ISM"+map[bool]string{true: " and the admin program", false: ""}[adminToo]+" but this machine holds no Solana watch key (solana.watchKey): "+text)
		return
	}
	if !m.lastSolFlag.IsZero() && m.now().Sub(m.lastSolFlag) < solFlagEvery {
		return // asserted within the minute: the slot on chain is fresh enough for an unpause
	}
	progs := m.Cfg.Programs()
	watch := m.solWatch.Payer.PublicKey()
	// the admin program is the live one of the line (AQ6); a retired one whose state still holds the watch key's
	// seat is flagged after it, best effort
	ixs, retired := strokeIxs(progs, m.admins, watch, true, adminToo)
	if _, err := m.solWatch.Send(ctx, ixs); err != nil {
		m.Alarm.Alarm("flag:solana", "setting the Solana flag failed ("+orNone(solErrName(err))+"): "+err.Error())
		return
	}
	m.lastSolFlag = m.now()
	m.Alarm.Clear("flag:solana")
	m.state.SolFlagsSet++
	log.Printf("monitor: FLAG set on the pausable ISM%s by the watch key %s: %s", map[bool]string{true: " and the admin program", false: ""}[adminToo], watch, text)
	if len(retired) > 0 {
		if _, err := m.solWatch.Send(ctx, retired); err != nil {
			log.Printf("monitor: the flag on %d retired admin program(s) still standing failed (the live objects are flagged): %v", len(retired), err)
		} else {
			log.Printf("monitor: the flag set on %d retired admin program(s) still standing too", len(retired))
		}
	}
}

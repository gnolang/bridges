package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// gno.land → Ethereum (eth/bridge/PLAN.md §5.7, §6.2): every hlmailbox message for a route or HlAdmin is delivered
// by Mailbox.process(metadata, message) under a header the client accepted — the metadata is the header's hash,
// the SimpleProof of the results leaf that carries the message's Dispatch event and the leaf (ethx.VerifyMetadata).
// Decisions go first, one copy per number, only the number HlAdmin expects next, under the emergency fee cap; a
// number another decision took is reported. Route messages go only while the gate would take them (no flag, no
// quarantine), under the day cap's parks; a send gno.land refunded never goes. A message no accepted header proves
// waits; when the waiting ones do not share one block above the trusted height, a batch of them (at most the batch
// size, the blocks holding a batch already left to their own headers) is re-emitted in one MsgRun when a header is
// due for the batch (headerDue: a decision in it, its fees cover an update, or the heartbeat comes), so one update
// serves the batch — or, under -proveOld, the header after a block holding messages below the trusted height is
// proven (proveHeader) when it is due for them.

// leafInfo is what a block's results say about one message: the leaf's index and size, or why the block's leaves
// cannot be rebuilt.
type leafInfo struct {
	index      int
	bytes      int
	dispatches int // the mailbox's Dispatch events in the leaf: every one costs the delivery ≈94,000 gas
	buildable  bool
	why        string
}

func leafKey(height int64, id [32]byte) string {
	return strconv.FormatInt(height, 10) + "/" + hex.EncodeToString(id[:8])
}

// leafAt is the leaf of block height's results that carries the Dispatch of id, with the block's leaves. A block found
// unbuildable — a type gnolight has no schema for (alarmed by name: a chain upgrade's new type would otherwise show
// only as re-emits), or by resultsMatch — stays so (G8: a block's results do not change): answered from the cache, no
// refetch each tick, no second alarm.
func (r *Relayer) leafAt(ctx context.Context, height int64, id [32]byte) (leafInfo, [][]byte, error) {
	key := leafKey(height, id)
	if li, ok := r.leafs[key]; ok && !li.buildable {
		return li, nil, nil
	}
	rs, err := r.light.BlockResults(ctx, height)
	if err != nil {
		return leafInfo{}, nil, err
	}
	leaves, err := gnolight.ResultLeaves(rs)
	if err != nil {
		li := leafInfo{index: -1, why: err.Error()}
		r.leafs[key] = li
		var se *gnolight.SchemaError
		if errors.As(err, &se) {
			r.Alarm.Alarm("results:schema:"+se.TypeURL, fmt.Sprintf("block %d cannot be proven: %v — a type gnolight (errortypes.go, rpcjson.go) has no schema for, so every message dispatched beside one is re-emitted into a later block; add the type", height, err))
		}
		return li, nil, nil
	}
	// the leaf the scan judged (gnoside.ProofLeaf: the smallest carrying the id, the lowest index among equals), never
	// merely the first: a stranger's big leaf earlier in the block is not the one proven (E3-RT2)
	idx, n := gnoside.ProofLeaf(leaves, r.Cfg.Gno.Mailbox, id)
	if idx < 0 {
		li := leafInfo{index: -1, why: fmt.Sprintf("block %d carries no Dispatch of %x", height, id[:8])}
		r.leafs[leafKey(height, id)] = li
		return li, leaves, nil
	}
	li := leafInfo{index: idx, bytes: len(leaves[idx]), dispatches: n, buildable: true}
	r.leafs[leafKey(height, id)] = li
	return li, leaves, nil
}

// outbound is one pass of gno.land → Ethereum.
func (r *Relayer) outbound(ctx context.Context) error {
	if !r.Cfg.HasContracts() {
		return nil
	}
	r.prices.check(r.Cfg, r.Alarm, r.now())
	e := r.env
	// every read up to the walk's, and the walk: one failing for minutes holds every delivery, decision and backstop
	// header to Ethereum, alarmed outbox:walk (E5-GS4)
	var walkErr error
	defer func() { e.walked(r.now(), walkErr) }()
	mb, err := e.mailboxJSON(ctx)
	if err != nil {
		walkErr = err
		return err
	}
	if mb.Count == 0 {
		return nil
	}
	if err := e.checkContracts(mb); err != nil {
		r.note("deliveries", "none: "+err.Error())
		walkErr = err
		return err
	}
	head, err := r.gno.Status(ctx)
	if err != nil {
		walkErr = err
		return err
	}
	cs, err := ethx.ReadClient(ctx, r.Eth, Address(r.Cfg.Eth.Client), ethx.Latest)
	if err != nil {
		walkErr = err
		return err
	}
	if cs.ChainID != r.Cfg.Gno.ChainID {
		r.Alarm.Alarm("client:chain", fmt.Sprintf("the client follows chain %q, the config says %q: nothing is delivered", cs.ChainID, r.Cfg.Gno.ChainID))
		return fmt.Errorf("the client follows chain %q", cs.ChainID) // alarmed on its own (no walk failure)
	}
	r.Alarm.Clear("client:chain")
	r.trusted = cs.TrustedHeight
	if err := e.readAdmins(ctx); err != nil {
		r.note("admin", "HlAdmin's line of succession could not be read (the configured HlAdmin stands in): "+err.Error())
	} else if text := e.admins.Text(); text != "" {
		r.note("admin", text)
	}
	e.readLines(ctx)
	r.checkRouters(ctx, mb.Domain)
	// the scan first, so the walk reads every block it reached (the leaf sizes the updater reads alike)
	start, err := e.scanStart(ctx, &r.state.Out, mb, head.Height)
	if err != nil {
		walkErr = err
		return err
	}
	if _, err := e.scanEvents(ctx, &r.state.Out, head.Height, start, scanPerTick); err != nil {
		log.Printf("relayer: the event scan: %v", err)
	}
	decisions, routes, err := e.walkOut(ctx, &r.state.Out, cs, mb, head.Height)
	if err != nil {
		walkErr = err
		return err
	}
	// the events no step reads judged at the finalized block, a few a step (E5-GS4)
	if err := e.compact(ctx, r.now(), &r.state.Out, append(append([]*outMsg{}, decisions...), routes...), head.Height); err != nil {
		log.Printf("relayer: the events' compaction (judged again next step): %v", err)
	}
	decisions, routes = r.endDelivered(decisions), r.endDelivered(routes)
	latest, err := r.Eth.BlockAt(ctx, ethx.Latest)
	if err != nil {
		return err
	}
	r.latest = latest
	var firstErr error
	if err := r.deliverDecisions(ctx, cs, decisions); err != nil {
		firstErr = err
	}
	if err := r.env.markDecisions(ctx, decisions); err != nil && firstErr == nil {
		firstErr = err
	} else if err == nil {
		if err := r.decisionBackstop(ctx, cs, latest, decisions); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// each proven send judged: a header the updater booked it under, its own fee, or held (E3-GS2, E3-GS7, E4-GS2)
	if err := r.env.markHeld(ctx, r.Sender, latest, cs, r.now(), routes); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := r.deliverRoutes(ctx, cs, latest, routes); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := r.waits(ctx, cs, latest, decisions, routes); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// endDelivered ends the messages Ethereum's mailbox shows delivered (whoever delivered them) and answers the rest.
func (r *Relayer) endDelivered(ts []*outMsg) []*outMsg {
	var keep []*outMsg
	for _, t := range ts {
		if t.kind == kindDecision {
			// one delivered at the finalized block: its number went through our mailbox, the verdict kept; one delivered
			// at latest: seen, read by every verdict at the finalized block until then (E6-GS1)
			r.rememberCopy(t, t.proofH < 0, t.settling)
		}
		if t.proofH < 0 {
			r.state.Out.end(t.n, "delivered")
			r.forget("deliver:" + t.nk())
			continue
		}
		keep = append(keep, t)
	}
	return keep
}

// ------------------------------------------------------------------- decisions --

// deliverDecisions delivers r/hlgov's decisions to HlAdmin: one copy per number, only the next it expects (its
// counter read at latest — a premature one would only revert in the simulation), under the emergency cap.
func (r *Relayer) deliverDecisions(ctx context.Context, cs *ethx.ClientState, ds []*outMsg) error {
	admins := map[common.Address]*ethx.AdminState{}
	// by number: its copies delivered at latest (settling, or delivered in this pass), which a verdict reads beside the
	// copies kept — the finalized block may show one by the time it reads — and whether a copy of it goes to the live
	// HlAdmin (its retired alarm then closes, and a copy to a retired one is no news)
	settling := map[uint64][]*outMsg{}
	live := map[uint64]bool{}
	for _, t := range ds {
		if t.settling {
			settling[t.seq] = append(settling[t.seq], t)
		}
		if rcpt, _ := ethx.AddressFrom32(t.msg.Msg.Recipient); r.env.adminLine().IsLive(rcpt) {
			live[t.seq] = true
		}
	}
	r.retiredPass(ctx, admins, live)
	var firstErr error
	for _, t := range ds {
		key := "deliver:" + t.nk()
		if t.settling {
			r.note(key, "delivered at latest: ends once the finalized block shows it")
			continue
		}
		rcpt, _ := ethx.AddressFrom32(t.msg.Msg.Recipient)
		if r.env.adminLine().IsRetired(rcpt) {
			t.past = true
			r.retireCopy(ctx, t, rcpt, admins, live[t.seq])
			continue
		}
		st := admins[rcpt]
		if st == nil {
			var err error
			if st, err = ethx.ReadAdmin(ctx, r.Eth, rcpt, ethx.Latest); err != nil {
				return err
			}
			admins[rcpt] = st
		}
		switch decisionGate(t.seq, st.LastSeq) {
		case "past":
			t.past = true
			r.pastDecision(ctx, t, rcpt, settling[t.seq])
			continue
		case "wait":
			delete(r.state.Ready, key)
			r.note(key, fmt.Sprintf("decision %d waits: HlAdmin expects %d next", t.seq, st.LastSeq+1))
			continue
		}
		if t.proofH == 0 {
			delete(r.state.Ready, key)
			r.note(key, fmt.Sprintf("decision %d waits for an accepted header (its events at %s; the client trusts %d)", t.seq, eventsText(t.events), cs.TrustedHeight))
			continue
		}
		if !r.ethAct(key) || !r.state.Attempts[key].due(r.now()) {
			continue
		}
		if err := r.deliverTodo(ctx, cs, t, true); err != nil && firstErr == nil {
			firstErr = err
		}
		if t.settling {
			settling[t.seq] = append(settling[t.seq], t)
			r.rememberCopy(t, false, true)
		}
		delete(admins, rcpt) // the next number is expected now
	}
	return firstErr
}

// ------------------------------------------------------------- decision copies --

// A decision number's copies (E6-GS1): anyone may re-send any number r/hlgov ever dispatched — hlgov.Redispatch, a
// MsgCall's fee apiece — each a new hlmailbox message. The walk reads the delivered mark of every copy not ended, every
// step, and ends one delivered at the finalized block (kept as its number's verdict: it went through our mailbox); a
// copy ended once its number was recorded can never record it (HlAdmin ignores a number at or below its last). So a
// number keeps a few copies — while its verdict is open its first, the first seenKept seen delivered at latest and its
// newest copiesNewest, the ones a verdict reads beside the copies delivered at latest this step; once it went through
// the one that did; once reported taken none — and a copy of a number gone through or reported taken ends at once,
// with no read. A number whose verdict is open is judged once a step: at the step's finalized block, HlAdmin's state
// there (once for each address) and the marks of its copies kept and delivered at latest, at most copyReadsPerStep
// marks a step for every number. A state file from before the bound (CopiesV 0) holds every copy, none marked: its
// lists are judged whole first (CopiesToJudge).
const (
	copiesNewest     = 4
	seenKept         = 2
	copiesBound      = 1 + seenKept + copiesNewest
	copyReadsPerStep = 64
)

// numberVerdict is a decision number's verdict past HlAdmin's counter: "recorded" (a copy of it went through our
// mailbox), "taken" (HlAdmin's counter at the finalized block covers it and no copy of ours is delivered there, on two
// passes), or none yet — why says what it waits for.
type numberVerdict struct {
	how, why string
}

// went says whether a number's copies hold the one that went through our mailbox.
func went(cs []decisionCopy) bool {
	for _, c := range cs {
		if c.Delivered {
			return true
		}
	}
	return false
}

// seenCount is how many of a number's copies were seen delivered at latest.
func seenCount(cs []decisionCopy) int {
	n := 0
	for _, c := range cs {
		if c.Seen {
			n++
		}
	}
	return n
}

// keptCopies are the copies a number keeps (E6-GS1), by nonce: none once it is reported taken, the one that went
// through once one did, else its first, the ones seen delivered at latest and its newest copiesNewest.
func (r *Relayer) keptCopies(sk string, cs []decisionCopy) []decisionCopy {
	if r.state.Taken[sk] {
		return nil
	}
	for _, c := range cs {
		if c.Delivered {
			return []decisionCopy{c}
		}
	}
	var out []decisionCopy
	for i, c := range cs {
		if i == 0 || c.Seen || i >= len(cs)-copiesNewest {
			out = append(out, c)
		}
	}
	return out
}

// setCopies stores a number's copies (none: the number's entry goes).
func (r *Relayer) setCopies(sk string, cs []decisionCopy) {
	if len(cs) == 0 {
		delete(r.state.DecisionCopies, sk)
		return
	}
	r.state.DecisionCopies[sk] = cs
}

// rememberCopy keeps a decision copy the walk read — delivered when the walk ended it so at the finalized block, seen
// when it is delivered at latest (settling, or delivered by this relayer) — as far as its number's bound keeps it.
func (r *Relayer) rememberCopy(t *outMsg, delivered, seen bool) {
	sk := strconv.FormatUint(t.seq, 10)
	cs := r.state.DecisionCopies[sk]
	i := sort.Search(len(cs), func(i int) bool { return cs[i].Nonce >= t.n })
	have := i < len(cs) && cs[i].Nonce == t.n
	c := decisionCopy{Nonce: t.n, ID: hex.EncodeToString(t.msg.ID[:])}
	if have {
		c = cs[i]
	}
	if delivered {
		r.keepWent(sk, c)
		return
	}
	switch {
	case seen && !c.Seen && seenCount(cs) < seenKept:
		c.Seen = true
	case have:
		return
	}
	if have {
		cs = append(append([]decisionCopy{}, cs[:i]...), append([]decisionCopy{c}, cs[i+1:]...)...)
	} else {
		cs = append(append([]decisionCopy{}, cs[:i]...), append([]decisionCopy{c}, cs[i:]...)...)
	}
	r.setCopies(sk, r.keptCopies(sk, cs))
}

// keepWent keeps a copy that went through our mailbox as its number's verdict (its one copy kept from now on): the
// number's first-look mark, its older copies still to judge and its retired alarm go.
func (r *Relayer) keepWent(sk string, c decisionCopy) {
	if r.state.Taken[sk] {
		return // reported taken: a copy delivered after is HlAdmin's to ignore, and the report stands
	}
	c.Delivered, c.Seen = true, false
	r.state.DecisionCopies[sk] = []decisionCopy{c}
	delete(r.state.TakenSeen, sk)
	delete(r.state.CopiesToJudge, sk)
	r.closeRetired(sk)
}

// queueOldCopies moves, once (CopiesV 0 → 1), every list a state file from before the bound kept into CopiesToJudge,
// whole and whatever its length — every copy kept, none marked delivered, so the one that went through may be any of
// them, an ended one included (n0 to a retired HlAdmin, n1 to its successor delivered) — except a number reported
// taken, which keeps none. A number's queue is judged before its verdict can be "taken", and no copy the walk reads
// later joins it (E6-GS1's review, findings 1 and 2: a list cut unread could drop the copy that went through, and one
// refilled by the walk was judged for good).
func (r *Relayer) queueOldCopies() {
	s := &r.state
	if s.CopiesV >= 1 {
		return
	}
	for sk, cs := range s.DecisionCopies {
		switch {
		case s.Taken[sk]:
			delete(s.DecisionCopies, sk)
		case went(cs):
			r.setCopies(sk, r.keptCopies(sk, cs))
		default:
			s.CopiesToJudge[sk] = append(s.CopiesToJudge[sk], cs...)
			delete(s.DecisionCopies, sk)
		}
	}
	s.CopiesV = 1
}

// pruneCopies keeps every number's copies to what its verdict needs (E6-GS1), and judges the copies an older state file
// kept (CopiesToJudge) at the step's finalized block, within the step's copyReadsPerStep marks: the numbers a verdict
// waited on this step first, each queue oldest first (the original dispatch is the copy that most often went
// through); one delivered there is kept as the number's verdict, the others are dropped.
func (r *Relayer) pruneCopies(ctx context.Context) {
	for sk, cs := range r.state.DecisionCopies {
		r.setCopies(sk, r.keptCopies(sk, cs))
	}
	var seqs []string
	for sk := range r.state.CopiesToJudge {
		if r.state.Taken[sk] || went(r.state.DecisionCopies[sk]) || len(r.state.CopiesToJudge[sk]) == 0 {
			delete(r.state.CopiesToJudge, sk)
			continue
		}
		seqs = append(seqs, sk)
	}
	if len(seqs) == 0 || !r.Cfg.HasContracts() {
		return
	}
	sort.Slice(seqs, func(i, j int) bool {
		_, ai := r.verdicts[seqs[i]]
		_, aj := r.verdicts[seqs[j]]
		if ai != aj {
			return ai
		}
		ni, _ := strconv.ParseUint(seqs[i], 10, 64)
		nj, _ := strconv.ParseUint(seqs[j], 10, 64)
		return ni < nj
	})
	fin := r.verdictFinalized(ctx)
	if fin == nil {
		return
	}
	at := ethx.AtHash(fin.Hash)
	for _, sk := range seqs {
		left := copyReadsPerStep - r.copyReads
		if left <= 0 {
			return
		}
		q := r.state.CopiesToJudge[sk]
		sort.Slice(q, func(i, j int) bool { return q[i].Nonce < q[j].Nonce })
		n := min(left, len(q))
		ids, judged := copyIDs(q[:n])
		r.copyReads += n
		marks, err := ethx.DeliveredMany(ctx, r.Eth, Address(r.Cfg.Eth.Mailbox), ids, at)
		if err != nil {
			return
		}
		if i := slices.Index(marks, true); i >= 0 {
			r.keepWent(sk, judged[i])
			continue
		}
		if rest := q[n:]; len(rest) > 0 {
			r.state.CopiesToJudge[sk] = rest
		} else {
			delete(r.state.CopiesToJudge, sk)
		}
	}
}

// retireCopy ends a decision copy addressed to a retired HlAdmin of the line (G39, AP3): it records nothing more. Its
// alarm is the number's, decision:retired:<number> (E6-GS1's sibling: anyone's Redispatch mints copies of any number
// to r/hlgov's recipient, and an alarm by the copy's nonce stood one apiece, old numbers included), opened unless the
// number needs nothing — a copy of it goes to the live HlAdmin this pass, its verdict is known, or the live HlAdmin's
// counter covers it (a successor inherits its predecessor's) — and judged again every pass (retiredPass: one opened
// while the counter could not be read is closed once it reads).
func (r *Relayer) retireCopy(ctx context.Context, t *outMsg, rcpt common.Address, admins map[common.Address]*ethx.AdminState, live bool) {
	key := "deliver:" + t.nk()
	sk := strconv.FormatUint(t.seq, 10)
	r.state.Out.end(t.n, "retired")
	r.forget(key)
	why := "retired: " + rcpt.Hex() + " handed every authority to its successor (kind 9) and takes no decision"
	switch {
	case live:
		r.note(key, why+"; a copy of its number goes to the live HlAdmin")
		return
	case r.state.Taken[sk] || went(r.state.DecisionCopies[sk]):
		r.note(key, why+"; its number's verdict is known")
		return
	}
	lv := r.env.adminLine().Live
	if st := r.liveAdmin(ctx, admins); st != nil && t.seq <= st.LastSeq {
		r.note(key, fmt.Sprintf("%s; the live HlAdmin %s's counter covers its number (%d of %d): recorded on the line", why, lv.Hex(), t.seq, st.LastSeq))
		return
	}
	r.note(key, why)
	r.openRetired(sk, fmt.Sprintf("gno #%d is r/hlgov's decision %d to %s, which handed every authority to its successor (kind 9): dead there; r/hlgov's set decision recipient to the live HlAdmin %s, then a re-dispatch, delivers it (§6.5)", t.n, t.seq, rcpt.Hex(), r.env.adminLine().Live.Hex()))
}

// openRetired opens a number's decision:retired alarm (RetiredOpen: judged again every pass, kept across a restart).
func (r *Relayer) openRetired(sk, text string) {
	if r.state.RetiredOpen == nil {
		r.state.RetiredOpen = map[string]string{}
	}
	r.state.RetiredOpen[sk] = text
	r.Alarm.AlarmEvery("decision:retired:"+sk, text, 6*time.Hour)
}

// closeRetired ends a number's decision:retired alarm.
func (r *Relayer) closeRetired(sk string) {
	delete(r.state.RetiredOpen, sk)
	r.Alarm.Clear("decision:retired:" + sk)
}

// retiredPass judges every open decision:retired alarm again: closed once a copy of its number goes to the live
// HlAdmin, its verdict is known or the live HlAdmin's counter covers it (recorded on the line: a successor inherits its
// predecessor's counter), raised again otherwise — so one opened while the live counter could not be read is not left
// standing, and a restart keeps the rest.
func (r *Relayer) retiredPass(ctx context.Context, admins map[common.Address]*ethx.AdminState, live map[uint64]bool) {
	if len(r.state.RetiredOpen) == 0 {
		return
	}
	st := r.liveAdmin(ctx, admins)
	for sk, text := range r.state.RetiredOpen {
		seq, err := strconv.ParseUint(sk, 10, 64)
		if err != nil || live[seq] || r.state.Taken[sk] || went(r.state.DecisionCopies[sk]) || (st != nil && seq <= st.LastSeq) {
			r.closeRetired(sk)
			continue
		}
		r.Alarm.AlarmEvery("decision:retired:"+sk, text, 6*time.Hour)
	}
}

// liveAdmin is the live HlAdmin's state at latest: the pass's own read, else the step's reading of the line (its live
// one's state), else read now (nil when it cannot be read).
func (r *Relayer) liveAdmin(ctx context.Context, admins map[common.Address]*ethx.AdminState) *ethx.AdminState {
	line := r.env.adminLine()
	if st := admins[line.Live]; st != nil {
		return st
	}
	if line.State != nil {
		return line.State
	}
	lv := line.Live
	st, err := ethx.ReadAdmin(ctx, r.Eth, lv, ethx.Latest)
	if err != nil {
		return nil
	}
	admins[lv] = st
	return st
}

// pastDecision ends a copy whose number HlAdmin has recorded (§5.7: "taken" is reported once, for r/hlgov's sync
// sequence), by its number's verdict (verdictOf); a number with none yet leaves its copy to the next pass.
func (r *Relayer) pastDecision(ctx context.Context, t *outMsg, rcpt common.Address, settling []*outMsg) {
	sk := strconv.FormatUint(t.seq, 10)
	key := "deliver:" + t.nk()
	v := r.verdictOf(ctx, t, rcpt, settling)
	switch v.how {
	case "recorded":
		r.state.Out.end(t.n, "recorded")
		r.forget(key)
		r.note(key, v.why)
	case "taken":
		r.state.Out.end(t.n, "taken")
		r.forget(key)
		r.note(key, v.why)
		if !r.state.Taken[sk] {
			r.state.Taken[sk] = true
			delete(r.state.TakenSeen, sk)
			delete(r.state.DecisionCopies, sk)
			delete(r.state.CopiesToJudge, sk)
			r.closeRetired(sk)
			r.Alarm.Alarm("decision:taken:"+sk, fmt.Sprintf("decision %d (kind %d) cannot be delivered: another decision has already taken its number on HlAdmin (no copy of r/hlgov's is delivered at the finalized block, on two passes); r/hlgov's sync sequence moves the counter past it and the proposal is re-made (§6.5)", t.seq, t.dk))
		}
	default:
		r.note(key, v.why)
	}
}

// verdictOf is a number's verdict past HlAdmin's counter: at once, with no read, for a number reported taken or gone
// through our mailbox; else judged once a step (judgeNumber).
func (r *Relayer) verdictOf(ctx context.Context, t *outMsg, rcpt common.Address, settling []*outMsg) numberVerdict {
	sk := strconv.FormatUint(t.seq, 10)
	switch {
	case r.state.Taken[sk]:
		return numberVerdict{"taken", fmt.Sprintf("taken: decision %d was reported taken (another decision has its number on HlAdmin)", t.seq)}
	case went(r.state.DecisionCopies[sk]):
		return numberVerdict{"recorded", fmt.Sprintf("recorded: a copy of decision %d went through our mailbox", t.seq)}
	}
	if r.verdicts == nil {
		r.verdicts = map[string]numberVerdict{}
	}
	if v, ok := r.verdicts[sk]; ok {
		return v
	}
	v := r.judgeNumber(ctx, t, sk, rcpt, settling)
	if v.how == "" {
		r.verdicts[sk] = v // the pass's other copies of the number wait as this one does; a verdict is kept by itself
	}
	return v
}

// judgeNumber judges a number past HlAdmin's counter whose verdict is open: "recorded" when a copy of it went through
// our mailbox — one this relayer's walk still has in Done (an older state file's), or one of its copies kept or
// delivered at latest marked at the finalized block — and "taken" only when HlAdmin's counter at the finalized block
// covers the number, no copy is delivered there, none of an older state file's copies of it waits to be judged, on a
// second pass (the admin state and the marks read at one block, pinned by its hash, G4). A provider without the
// finalized block's state is no verdict.
func (r *Relayer) judgeNumber(ctx context.Context, t *outMsg, sk string, rcpt common.Address, settling []*outMsg) numberVerdict {
	cs := r.state.DecisionCopies[sk]
	old := r.state.CopiesToJudge[sk]
	for _, c := range append(append([]decisionCopy{}, cs...), old...) {
		switch r.state.Out.Done[strconv.FormatUint(uint64(c.Nonce), 10)] {
		case "delivered":
			r.keepWent(sk, c)
			return numberVerdict{"recorded", fmt.Sprintf("recorded: a copy of decision %d went through our mailbox", t.seq)}
		case "recorded":
			return numberVerdict{"recorded", fmt.Sprintf("recorded: decision %d was found recorded through our mailbox", t.seq)}
		}
	}
	fin := r.verdictFinalized(ctx)
	if fin == nil {
		return numberVerdict{"", "decision " + sk + " is past HlAdmin's counter; the finalized block could not be read: no verdict"}
	}
	at := ethx.AtHash(fin.Hash)
	st, err := r.finAdmin(ctx, rcpt, at)
	if err != nil {
		return numberVerdict{"", fmt.Sprintf("decision %d: no verdict at the finalized block %d (%v)", t.seq, fin.Number, err)}
	}
	if st.LastSeq < t.seq {
		return numberVerdict{"", fmt.Sprintf("decision %d is recorded at latest, not yet at the finalized block %d: looked at again", t.seq, fin.Number)}
	}
	// the marks of its copies kept and of its copies delivered at latest, within the step's reads
	cands := append([]decisionCopy{}, cs...)
	for _, s := range settling {
		if i := sort.Search(len(cs), func(i int) bool { return cs[i].Nonce >= s.n }); i == len(cs) || cs[i].Nonce != s.n {
			cands = append(cands, decisionCopy{Nonce: s.n, ID: hex.EncodeToString(s.msg.ID[:])})
		}
	}
	n := min(max(copyReadsPerStep-r.copyReads, 0), len(cands))
	if n > 0 {
		ids, read := copyIDs(cands[:n])
		r.copyReads += n
		marks, err := ethx.DeliveredMany(ctx, r.Eth, Address(r.Cfg.Eth.Mailbox), ids, at)
		if err != nil {
			return numberVerdict{"", fmt.Sprintf("decision %d: no verdict at the finalized block %d (%v)", t.seq, fin.Number, err)}
		}
		if i := slices.Index(marks, true); i >= 0 {
			r.keepWent(sk, read[i])
			return numberVerdict{"recorded", fmt.Sprintf("recorded: a copy of decision %d (gno #%d) is delivered at the finalized block %d", t.seq, read[i].Nonce, fin.Number)}
		}
	}
	switch {
	case n < len(cands):
		return numberVerdict{"", fmt.Sprintf("decision %d is recorded on HlAdmin; %d of its copies' marks are unread, the step's %d reads spent: looked at again", t.seq, len(cands)-n, copyReadsPerStep)}
	case len(old) > 0:
		return numberVerdict{"", fmt.Sprintf("decision %d is recorded on HlAdmin; %d of its copies an older state file kept wait to be judged at the finalized block (%d marks a step) before it can be found taken: looked at again", t.seq, len(old), copyReadsPerStep)}
	}
	if _, seen := r.state.TakenSeen[sk]; !seen {
		r.state.TakenSeen[sk] = r.now().Unix()
		return numberVerdict{"", fmt.Sprintf("decision %d is recorded on HlAdmin and no copy of ours is delivered at the finalized block: looked at again next pass", t.seq)}
	}
	return numberVerdict{"taken", fmt.Sprintf("taken: decision %d is recorded on HlAdmin and no copy of ours is delivered at the finalized block, on two passes", t.seq)}
}

// copyIDs are the ids of copies, and the copies whose id parses, alike in order.
func copyIDs(cs []decisionCopy) ([][32]byte, []decisionCopy) {
	ids := make([][32]byte, 0, len(cs))
	ok := make([]decisionCopy, 0, len(cs))
	for _, c := range cs {
		if id, err := hl.ParseKey32(c.ID); err == nil {
			ids, ok = append(ids, id), append(ok, c)
		}
	}
	return ids, ok
}

// verdictFinalized is the finalized block the step's verdicts and judging read at, read once a step (nil when the
// provider answers none): every number judged this step is judged at one block.
func (r *Relayer) verdictFinalized(ctx context.Context) *ethx.Block {
	if !r.finRead {
		r.finRead = true
		if b, err := r.Eth.BlockAt(ctx, ethx.Finalized); err == nil {
			r.fin = b
		}
	}
	return r.fin
}

// finAdmin is an HlAdmin's state at a finalized block, read once a step for each address and block.
func (r *Relayer) finAdmin(ctx context.Context, rcpt common.Address, at ethx.At) (*ethx.AdminState, error) {
	k := rcpt.Hex() + "@" + at.Hash.Hex()
	if st, ok := r.finAdmins[k]; ok {
		return st, nil
	}
	st, err := ethx.ReadAdmin(ctx, r.Eth, rcpt, at)
	if err != nil {
		return nil, err
	}
	if r.finAdmins == nil {
		r.finAdmins = map[string]*ethx.AdminState{}
	}
	r.finAdmins[k] = st
	return st, nil
}

// --------------------------------------------------------------------- routes --

// deliverRoutes delivers the route messages unless the gate is paused (a flag or the quarantine), under the day
// cap's parks (AO5, AP2, AQ4) — oldest first, a park's amount reserved against younger sends of its token — and
// never a send gno.land refunded (AQ2).
func (r *Relayer) deliverRoutes(ctx context.Context, cs *ethx.ClientState, latest *ethx.Block, ts []*outMsg) error {
	if len(ts) == 0 {
		return nil
	}
	gate, err := r.env.gateState(ctx)
	if err != nil {
		return err
	}
	now := int64(latest.Time)
	if paused, why := deliveriesPaused(gate, cs.QuarantineEnd, now); paused {
		r.note("deliveries", fmt.Sprintf("paused: %s (%d route messages wait)", why, len(ts)))
		for _, t := range ts {
			delete(r.state.Ready, "deliver:"+t.nk())
		}
		return nil
	}
	r.note("deliveries", "open")
	day := int64(86_400) / int64(max(gate.ClockFactor, 1))
	parks := r.dropRefundedParks(ctx, capParks(ts, r.state.Attempts))
	for _, p := range overLimitParks(parks, gate) {
		key := "deliver:" + strconv.FormatUint(uint64(p.n), 10)
		if a := r.state.Attempts[key]; a != nil {
			a.Cause = overMaxByLimits
			r.note(key, fmt.Sprintf("parked (OverMax by the live limits): %d is above the per-message maximum %d or the day cap %d after a lowering kind 12; it reserves nothing and waits for a kind 12 raise (AQ4)", p.amount, gate.Limits[p.tok].MaxPerMessage, gate.Limits[p.tok].DayCap))
		}
		parks[p.tok] = nil
	}
	isms := map[common.Address]common.Address{}
	var passUsed [2]uint64
	var firstErr error
	for _, t := range ts {
		key := "deliver:" + t.nk()
		if r.state.Out.Done[t.nk()] != "" {
			continue
		}
		if t.settling {
			r.note(key, "delivered at latest: ends once the finalized block shows it")
			continue
		}
		if t.proofH == 0 {
			delete(r.state.Ready, key)
			r.note(key, fmt.Sprintf("waits for an accepted header (its events at %s; the client trusts %d)", eventsText(t.events), cs.TrustedHeight))
			continue
		}
		if t.held {
			delete(r.state.Ready, key)
			r.note(key, fmt.Sprintf("held: proven under header %d, and no header the updater's key booked it under proves it (no budget booked its delivery), and its own fee does not cover its delivery at the live price: it waits for its group's header (its six hours within the budget) or a lower base fee (E3-GS2, E3-GS7, E4-GS2)", t.proofH+1))
			continue
		}
		if !r.ethAct(key) {
			continue
		}
		a := r.state.Attempts[key]
		overCap := overDayCap(a)
		if !overCap && !a.due(r.now()) {
			continue
		}
		tok, amount, known := tokenAmount(t)
		if known && a.parked() && overLiveLimits(amount, gate.Limits[tok]) {
			r.note(key, fmt.Sprintf("parked (OverMax by the live limits): %d is above the per-message maximum %d or the day cap %d; waits for a kind 12 raise (AQ4)", amount, gate.Limits[tok].MaxPerMessage, gate.Limits[tok].DayCap))
			continue
		}
		var park *capPark
		if known {
			park = parks[tok]
		}
		if known && (overCap || park != nil && t.n > park.n) {
			if ok, why := capGate(t.n, amount, overCap, park, gate.Limits[tok], now, day, passUsed[tok]); !ok {
				r.note(key, why)
				continue
			}
		}
		if refunded, why, err := r.env.sendRefunded(ctx, t); err != nil {
			r.note(key, "waits: the route's send record could not be read (the refund mark is read before sending, AQ2): "+err.Error())
			continue
		} else if refunded {
			r.endRefunded(t, why)
			continue
		}
		// the token's ISM must be the gate: a delivery outside it is refused here (the Solana relayer's rule)
		token := t.rt.TokenAddress()
		ism, ok := isms[token]
		if !ok {
			if ism, err = r.recipientISM(ctx, token); err != nil {
				r.note(key, "the token's ISM could not be read: "+err.Error())
				continue
			}
			isms[token] = ism
		}
		if ism != Address(r.Cfg.Eth.Gate) {
			r.ethFailed(key, fmt.Errorf("refusing to deliver: %s's ISM is %s, not the gate %s", t.rt.Symbol, ism.Hex(), r.Cfg.Eth.Gate))
			continue
		}
		err := r.deliverTodo(ctx, cs, t, false)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if err == nil && known {
			passUsed[tok] += amount
			if park != nil && park.n == t.n {
				parks[tok] = nil
			}
		}
	}
	return firstErr
}

// recipientISM is the mailbox's ISM for a recipient (Mailbox.recipientIsm).
func (r *Relayer) recipientISM(ctx context.Context, recipient common.Address) (common.Address, error) {
	data, err := ethx.Mailbox.Pack("recipientIsm", recipient)
	if err != nil {
		return common.Address{}, err
	}
	out, err := r.Eth.CallAt(ctx, Address(r.Cfg.Eth.Mailbox), data, ethx.Latest)
	if err != nil {
		return common.Address{}, err
	}
	vals, err := ethx.Mailbox.Unpack("recipientIsm", out)
	if err != nil {
		return common.Address{}, err
	}
	return vals[0].(common.Address), nil
}

// deliveriesPaused says whether the gate refuses route deliveries now: any flag, or the quarantine by the chain's
// clock (the two facts its verify reads).
func deliveriesPaused(g *ethx.GateState, quarantineEnd uint64, now int64) (bool, string) {
	switch {
	case g != nil && g.AnyFlag():
		return true, "a flag stands on the gate"
	case int64(quarantineEnd) > now:
		return true, "the quarantine stands until " + when(int64(quarantineEnd))
	}
	return false, ""
}

// overDayCap says whether a delivery is parked on the gate's OverDayCap.
func overDayCap(a *attempt) bool { return a.parked() && a.causeIs("OverDayCap") }

// overMaxByLimits is the cause a park is re-marked with when its amount exceeds the live limits (AQ4).
const overMaxByLimits = "OverMax (by the live limits after a lowering kind 12)"

// capPark is a token's oldest delivery parked on OverDayCap: its amount is reserved against younger sends (AP2).
type capPark struct {
	n      uint32
	amount uint64
	tok    int
	t      *outMsg
}

// capParks finds each token's oldest OverDayCap park among the route messages (the walk is oldest first).
func capParks(ts []*outMsg, attempts map[string]*attempt) [2]*capPark {
	var out [2]*capPark
	for _, t := range ts {
		// a park delivered meanwhile (by anyone) reserves nothing: the bucket counts it already
		if t.settling || !overDayCap(attempts["deliver:"+t.nk()]) {
			continue
		}
		tok, amount, known := tokenAmount(t)
		if !known || out[tok] != nil {
			continue
		}
		out[tok] = &capPark{n: t.n, amount: amount, tok: tok, t: t}
	}
	return out
}

// dropRefundedParks ends a refunded park so it reserves nothing (G53); a mark that cannot be read keeps it.
func (r *Relayer) dropRefundedParks(ctx context.Context, parks [2]*capPark) [2]*capPark {
	for tok, p := range parks {
		if p == nil {
			continue
		}
		refunded, why, err := r.env.sendRefunded(ctx, p.t)
		if err != nil {
			r.note("deliver:"+strconv.FormatUint(uint64(p.n), 10), "parked (OverDayCap); its refund mark could not be read, so its amount stays reserved (G53): "+err.Error())
			continue
		}
		if refunded {
			r.endRefunded(p.t, why)
			parks[tok] = nil
		}
	}
	return parks
}

// overLiveLimits says whether an amount exceeds a token's live per-message maximum or day cap.
func overLiveLimits(amount uint64, l ethx.MintLimit) bool {
	return amount > l.MaxPerMessage || amount > l.DayCap
}

// overLimitParks are the parks whose amount exceeds the live limits (AQ4).
func overLimitParks(parks [2]*capPark, g *ethx.GateState) []*capPark {
	var out []*capPark
	for tok, p := range parks {
		if p != nil && overLiveLimits(p.amount, g.Limits[tok]) {
			out = append(out, p)
		}
	}
	return out
}

// capGate says whether a route message goes now under the day cap (AO5, AP2).
func capGate(n uint32, amount uint64, overCap bool, park *capPark, l ethx.MintLimit, now, day int64, used uint64) (bool, string) {
	var reserved uint64
	if park != nil && n > park.n {
		reserved = park.amount
	}
	if !overCap && reserved == 0 {
		return true, ""
	}
	ok, why := bucketRoom(l, now, day, used+reserved, amount)
	switch {
	case ok:
		return true, ""
	case overCap && reserved == 0:
		return false, "parked (OverDayCap), retried once the day's bucket has room: " + why
	case overCap:
		return false, fmt.Sprintf("parked (OverDayCap), retried once the day's bucket has room behind gno #%d's parked %d (oldest first, its amount reserved): %s", park.n, park.amount, why)
	}
	return false, fmt.Sprintf("held under the day cap behind gno #%d's parked %d (OverDayCap, oldest first: its amount is reserved against younger sends of the token, AP2): %s", park.n, park.amount, why)
}

// bucketRoom says whether a token's day bucket — its level now plus what this pass delivered — has room.
func bucketRoom(l ethx.MintLimit, now, day int64, delivered, amount uint64) (bool, string) {
	level := l.Level(now, day) + delivered
	if level+amount <= l.DayCap && amount <= l.MaxPerMessage {
		return true, ""
	}
	return false, fmt.Sprintf("the day's bucket holds %d of %d, %d asked", level, l.DayCap, amount)
}

// endRefunded ends a route message gno.land refunded: never delivered, alarmed once.
func (r *Relayer) endRefunded(t *outMsg, why string) {
	r.state.Out.end(t.n, "refunded")
	r.forget("deliver:" + t.nk())
	r.note("deliver:"+t.nk(), "refunded: "+why)
	r.Alarm.Alarm("send:refunded:"+t.nk(), fmt.Sprintf("gno #%d is not delivered: %s", t.n, why))
}

// checkRouters reads each token's enrolled router for gno.land's domain and alarms while it is no realm of the
// configured route's line (K34).
func (r *Relayer) checkRouters(ctx context.Context, gnoDomain uint32) {
	for _, rt := range r.Cfg.Routes {
		if rt.Token == "" {
			continue
		}
		key := "router:unknown:" + rt.Symbol
		if err := r.env.lineErrs[rt.Symbol]; err != nil {
			r.note(key, "not judged this step: the route's line of migrations could not be read: "+err.Error())
			continue
		}
		router, err := ethx.RouterFor(ctx, r.Eth, rt.TokenAddress(), gnoDomain, ethx.Latest)
		if err != nil {
			r.note(key, "the token's router could not be read: "+err.Error())
			continue
		}
		switch {
		case router == [32]byte{}:
			r.Alarm.Alarm(key, fmt.Sprintf("the %s token enrolls no router for gno.land's domain %d: no send of the token can mint until kind 6 enrolls one", rt.Symbol, gnoDomain))
		case !r.env.lineSender(&rt, router):
			r.Alarm.Alarm(key, fmt.Sprintf("the %s token's enrolled router for gno.land (%x) is no realm of the configured route's line (%s and its migrations): its sends are not delivered by this relayer — edit routes[].gno, or undo a kind 6 to a stranger (K34)", rt.Symbol, router[:], rt.Gno))
		default:
			r.Alarm.Clear(key)
		}
	}
}

// ----------------------------------------------------------------- one delivery --

// deliverTodo delivers one message with §5.7's bookkeeping.
func (r *Relayer) deliverTodo(ctx context.Context, cs *ethx.ClientState, t *outMsg, emergency bool) error {
	key := "deliver:" + t.nk()
	err := r.deliverOne(ctx, cs, t, emergency)
	switch {
	case err == nil:
		// ended by the walk once the finalized block shows the mark (a delivery a reorg drops is made again)
		r.succeeded(key)
		r.forget(key)
		t.settling = true
		r.note(key, fmt.Sprintf("delivered under header %d: ends once the finalized block shows it", t.proofH+1))
		log.Printf("relayer: gno #%d %x delivered on Ethereum (%s) under header %d", t.n, t.msg.ID[:6], kindName(t), t.proofH+1)
		return nil
	case errors.Is(err, errNotYet):
		r.note(key, "waits: "+err.Error())
		return nil
	case ethx.RevertIs(err, "RetiredAdmin"), ethx.RevertIs(err, "Retired"):
		r.state.Out.end(t.n, "retired")
		r.forget(key)
		// a decision's alarm is its number's, as retireCopy's (E6-GS1's sibling): once a number, whatever its copies
		text := fmt.Sprintf("gno #%d answers Retired: HlAdmin handed every authority to its successor (kind 9) — dead there, never parked; r/hlgov's set decision recipient to the successor, then a re-dispatch, delivers it (§6.5): %v", t.n, err)
		if t.kind == kindDecision {
			r.openRetired(strconv.FormatUint(t.seq, 10), text)
		} else {
			r.Alarm.Alarm("decision:retired:gno#"+t.nk(), text)
		}
		return nil
	}
	r.ethFailed(key, err)
	return fmt.Errorf("gno #%d: %w", t.n, err)
}

func kindName(t *outMsg) string {
	if t.kind == kindDecision {
		return "decision " + strconv.FormatUint(t.seq, 10)
	}
	if t.rt != nil {
		return t.rt.Symbol
	}
	return "?"
}

// deliverOne builds the proof under the accepted header and sends Mailbox.process; "already delivered" is success.
func (r *Relayer) deliverOne(ctx context.Context, cs *ethx.ClientState, t *outMsg, emergency bool) error {
	h := t.proofH
	li, leaves, err := r.leafAt(ctx, h, t.msg.ID)
	if err != nil {
		return err
	}
	if !li.buildable {
		r.state.Out.Unusable[eventKey(t.n, h)] = li.why
		return fmt.Errorf("%w: block %d's results cannot prove it (%s): a re-emit is due", errNotYet, h, li.why)
	}
	if li.bytes > r.Cfg.MaxLeaf() {
		r.state.Out.Unusable[eventKey(t.n, h)] = fmt.Sprintf("a results leaf of %d bytes", li.bytes)
		return fmt.Errorf("%w: its results leaf at %d is %d bytes, above the %d proven from: a re-emit is due", errNotYet, h, li.bytes, r.Cfg.MaxLeaf())
	}
	if t.kind == kindRoute {
		if err := r.routeDeliveryCovered(ctx, t, h, li.dispatches, li.bytes); err != nil {
			return err
		}
	}
	blk, err := r.light.BlockAt(ctx, h+1)
	if err != nil {
		return err
	}
	hdr := blk.Header
	if hdr.Hash() != blk.Hash || blk.Hash != t.proofHash {
		return fmt.Errorf("header %d hashes to %x here, the node says %x, the client accepted %x", h+1, hdr.Hash(), blk.Hash, t.proofHash)
	}
	root, ok := hdr.LastResultsHashArray()
	if !ok {
		return fmt.Errorf("header %d carries no LastResultsHash although block %d had transactions", h+1, h)
	}
	if err := r.resultsMatch(t, h, leaves, root); err != nil {
		return err
	}
	proof, err := gnolight.Proof(leaves, li.index)
	if err != nil {
		return err
	}
	meta, err := ethx.VerifyMetadata(blk.Hash, proof, leaves[li.index])
	if err != nil {
		return err
	}
	data, err := ethx.ProcessCall(meta, t.msg.Raw)
	if err != nil {
		return err
	}
	call := ethx.Call{To: Address(r.Cfg.Eth.Mailbox), Data: data, Label: fmt.Sprintf("deliver gno #%d (%s)", t.n, kindName(t))}
	if emergency {
		call.MaxBaseFee = r.Cfg.EmergencyMaxBaseFee()
	}
	_, err = r.ethSend(ctx, call)
	if ethx.RevertIs(err, "already delivered") {
		return nil
	}
	return err
}

// resultsMatch holds block h's rebuilt leaves to header h+1's LastResultsHash. Leaves that are not the chain's — a
// string the node's JSON cannot carry (bytes that are not UTF-8 read as U+FFFD, and anyone's event or panic message can
// hold them), a type whose fields changed, or a node serving other results — give no proof from that block: it is
// unbuildable (the index's mark, which the walk honours for every message in it, and this leaf's cache entry), the
// mismatch is alarmed, and the message is re-emitted into a later block rather than parked for good (the shared
// results fix's relayer half, as the Solana relayer's).
func (r *Relayer) resultsMatch(t *outMsg, h int64, leaves [][]byte, root [32]byte) error {
	got, _ := gnolight.SimpleHash(leaves)
	if got == root {
		return nil
	}
	why := fmt.Sprintf("block %d's re-encoded results hash to %x, header %d says %x", h, got, h+1, root)
	r.state.Out.Index.MarkUnbuildable(h, why)
	r.state.Out.Unusable[eventKey(t.n, h)] = why
	r.leafs[leafKey(h, t.msg.ID)] = leafInfo{index: -1, why: why}
	r.Alarm.Alarm("results:mismatch", why+": a string the node's JSON cannot carry exactly (bytes that are not UTF-8 read as U+FFFD), a type whose fields changed, or a node serving other results; the message is re-emitted into a later block")
	return fmt.Errorf("%w: %s; a re-emit is due", errNotYet, why)
}

// routeDeliveryCovered is the delivery rule for a route send (§5.5, §5.7; the plan red team's E2-RT2, E3-GS2, E4-GS2):
// never from a leaf of more than a batch's Dispatch events — anyone's Reemits can pile them up, each costing the
// delivery ≈94,000 gas — which marks the event unusable, so the send is re-emitted into the relayer's own batch; at no
// fee check only from a header the updater's key booked it under (bookedFor: its trailer names the send, the budget
// booked its fee and this delivery); from any other header — a stranger's update, the relayers' own backstop, a
// decision's, or the updater's bought for other sends, which a stranger's Reemit beside kilobytes of its own events can
// put any send under — only while the send's own fee covers its delivery's model cost from the leaf built (its Dispatch
// events and its bytes) at the live price. A send that waits so is held: it waits for its group's header as an unproven
// send does, the six-hour rule and delivery:age included (E3-GS7), and anyone may deliver it (Mailbox.process is open).
func (r *Relayer) routeDeliveryCovered(ctx context.Context, t *outMsg, h int64, n, bytes int) error {
	if batch := r.Cfg.ReemitBatch(); n > batch {
		r.state.Out.Unusable[eventKey(t.n, h)] = fmt.Sprintf("a results leaf of %d Dispatch events, more than a batch's %d", n, batch)
		return fmt.Errorf("%w: its results leaf at %d carries %d Dispatch events, more than a batch's %d (each costs its delivery ≈94,000 gas): re-emitted into the relayer's own batch", errNotYet, h, n, batch)
	}
	if t.booked && h == t.proofH {
		return nil
	}
	if r.latest == nil {
		return fmt.Errorf("%w: the price is not read yet", errNotYet)
	}
	f, err := r.env.dueFacts(ctx, r.Sender, r.latest, []*outMsg{t}, r.now(), 0, func(*outMsg) (int, int) { return n, bytes })
	if err != nil {
		return err
	}
	if f.FeesWei.Cmp(f.DeliveriesWei) >= 0 {
		return nil
	}
	t.held = true
	return fmt.Errorf("%w: header %d books nothing for it (no trailer of the updater's key names it there: no budget booked this delivery), and the send's own fee (%s ETH) does not cover its delivery from a leaf of %d Dispatch event(s) and %d bytes (%d gas, %s ETH at %s gwei): held — it waits for its group's header (its six hours within the budget) or a lower base fee", errNotYet, h+1, eth(f.FeesWei), n, bytes, f.DeliveryGas, eth(f.DeliveriesWei), gwei(f.Price))
}

// The relayers' backstop for a decision's header (PLAN.md §5.5; the plan red team's E2-RT8): the relayers' key buys the
// header a waiting decision needs — under the emergency cap, outside the budget — when the updater's key cannot pay
// for it (its ETH short of the header's gas × 1.2 at twice the base fee plus the tip), or has not bought it for
// backstopDown (the updater down). A decision is GovDAO's cancel of a forger's in a spike; the float it rides must
// never be the one thing that holds it.
const (
	backstopAfter = 5 * time.Minute  // a decision waiting this long since its dispatch, servable
	backstopDown  = 30 * time.Minute // … and this long whatever the updater's float: the updater is not buying it
)

// decisionBackstop buys the waiting decision's header — or proves it, below the trusted height (E3-RT4) — when the
// updater does not.
func (r *Relayer) decisionBackstop(ctx context.Context, cs *ethx.ClientState, latest *ethx.Block, ds []*outMsg) error {
	if r.backstop == nil {
		return nil
	}
	var t *outMsg
	for _, d := range ds {
		h := d.latest()
		if d.proofH != 0 || d.past || d.settling || d.early || d.dup || h == 0 || (h+1 <= cs.TrustedHeight && !d.provesOld()) {
			continue // proven, past, delivered, not HlAdmin's next, or below the trusted height with no header to prove (re-emitted first: waits)
		}
		rcpt, _ := ethx.AddressFrom32(d.msg.Msg.Recipient)
		st, err := ethx.ReadAdmin(ctx, r.Eth, rcpt, ethx.Latest)
		if err != nil || decisionGate(d.seq, st.LastSeq) != "deliver" {
			continue
		}
		t = d
		break
	}
	if t == nil {
		return nil
	}
	key := "backstop:" + t.nk()
	// the wait counts from the decision's dispatch, its oldest event: anyone may Reemit any nonce, which resets its newest
	// event — timed from that, a stranger re-emitting a cancel every few minutes would keep the backstop off for good
	// (E3-RT7)
	at, ok := r.env.blockTime(ctx, t.msg.Height)
	if !ok {
		return nil
	}
	waited := r.now().Sub(at)
	if waited < backstopAfter {
		return nil
	}
	gas := r.env.headerGasAt(ctx, cs.TrustedHeight+1)
	price, err := r.env.price(ctx, r.Sender, latest)
	if err != nil {
		return err
	}
	base := latest.BaseFee
	if base == nil {
		base = new(big.Int)
	}
	need := weiOf(gas*12/10, new(big.Int).Add(new(big.Int).Mul(base, big.NewInt(2)), new(big.Int).Sub(price, base)))
	short := "the config names no updater"
	if u := r.Cfg.Eth.Updater; u != "" {
		bal, err := r.Eth.BalanceAt(ctx, Address(u), ethx.Latest)
		if err != nil {
			return fmt.Errorf("the updater's balance: %w", err)
		}
		short = ""
		if bal.Cmp(need) < 0 {
			short = fmt.Sprintf("the updater's key holds %s ETH, short of the header's %s ETH (%d gas × 1.2 at twice the base fee plus the tip)", eth(bal), eth(need), gas)
		}
	}
	if short == "" && waited < backstopDown {
		r.note(key, fmt.Sprintf("decision %d waits %s for its header: the updater can pay for it", t.seq, waited.Round(time.Second)))
		return nil
	}
	if short == "" {
		short = fmt.Sprintf("the updater has not bought it for %s", waited.Round(time.Minute))
	}
	if ok, _ := r.backstop.mayUpdate(r.now(), false, true); !ok || !r.ethAct(key) || r.now().Sub(r.state.LastBackstop) < MinUpdateGap {
		return nil
	}
	var p *updatePlan
	if target := t.latest() + 1; target <= cs.TrustedHeight {
		p, err = planProve(ctx, r.light, cs, target)
	} else {
		p, err = planUpdate(ctx, r.light, cs, target)
	}
	if err != nil {
		r.note(key, "the backstop's plan: "+err.Error())
		return nil
	}
	p.Decision = true
	reason := fmt.Sprintf("decision %d's header, bought by the relayers' key: %s", t.seq, short)
	r.sent++
	if _, err := r.backstop.submit(ctx, cs, p, true, reason); err != nil {
		if errors.Is(err, errWaits) || errors.Is(err, errOvertaken) {
			return nil
		}
		r.note(key, "failed: "+err.Error())
		return err
	}
	r.state.LastBackstop = r.now()
	r.state.Backstops++
	r.state.Out.noteHeader(p.Target, cs.Epoch) // its events are read every step from now on (E5-GS4)
	r.Alarm.AlarmEvery("backstop:decision", "the relayers' key bought "+reason, 6*time.Hour)
	r.note(key, "bought: "+reason)
	log.Printf("relayer: %s (header %d)", reason, p.Target)
	return nil
}

// ------------------------------------------------------------------- the waiting --

// waits applies the waiting rule: when a header is due for the waiting messages and they do not share one block
// above the trusted height, the batch re-emit (or, under -proveOld, the old headers' proofs).
func (r *Relayer) waits(ctx context.Context, cs *ethx.ClientState, latest *ethx.Block, decisions, routes []*outMsg) error {
	// a decision copy HlAdmin cannot take yet, or whose number another copy serves, is re-emitted for nothing (E3-GS6)
	if err := r.env.markDecisions(ctx, decisions); err != nil {
		return err
	}
	var live []*outMsg
	for _, t := range append(append([]*outMsg{}, decisions...), routes...) {
		if r.state.Out.Done[t.nk()] == "" {
			live = append(live, t)
		}
	}
	// a send gno.land refunded never goes, so it buys nothing either (AQ2; read only where a mark can be set, G42)
	var kept []*outMsg
	for _, t := range live {
		if t.kind == kindRoute && (t.proofH == 0 || t.held) && !t.settling {
			if refunded, why, err := r.env.sendRefunded(ctx, t); err == nil && refunded {
				r.endRefunded(t, why)
				continue
			}
		}
		kept = append(kept, t)
	}
	// a send waiting seven hours alarms here too (E5-GS3): the updater judges it from its own view of the bookings
	r.env.deliveryAge(ctx, waitingOf(kept), r.now())
	waiting, paused, err := r.env.unpausedOnly(ctx, waitingOf(kept), cs, int64(latest.Time))
	if err != nil {
		return err
	}
	plan := planWaits(waiting, cs.TrustedHeight, r.Cfg.ReemitBatch())
	r.poisonedAlarm(waiting)
	if plan.Waiting == 0 {
		if paused != "" {
			r.note("waiting", "route sends wait for the pause to end ("+paused+"): no header or batch is bought for them")
		} else {
			r.note("waiting", "none")
		}
		return nil
	}
	status := fmt.Sprintf("%d wait for a header (shared block %d, %d block groups, %d below the trusted height %d, %d old blocks a header proof serves)", plan.Waiting, plan.Shared, len(plan.Groups), len(plan.Below), cs.TrustedHeight, len(plan.Old))
	proven := map[uint32]bool{}
	if r.Cfg.Eth.ProveOld && len(plan.Below) > 0 {
		for _, n := range r.proveOld(ctx, cs, latest, waiting, plan.Below) {
			proven[n] = true
		}
	}
	// a message whose re-emits keep landing in blocks nothing can be proven from — a stranger's transaction beside each
	// that the leaves cannot be rebuilt for — re-emits again only after a back-off of its own (a minute, doubling, at
	// most an hour), so whoever poisons every block the batch lands in cannot drain the relayer's GNOT
	var batch, backing []*outMsg
	for _, t := range msgsOf(waiting, plan.Reemit) {
		switch {
		case proven[t.n]:
		case r.now().Before(r.state.Reemits[t.nk()].Add(reemitBackoff(t.spoiled))):
			backing = append(backing, t)
		default:
			batch = append(batch, t)
		}
	}
	if len(backing) > 0 && len(batch) == 0 {
		r.note("waiting", fmt.Sprintf("%s: the re-emits back off (%d message(s) whose re-emits landed in blocks nothing can be proven from)", status, len(backing)))
		return nil
	}
	if len(batch) == 0 {
		switch {
		case plan.Shared > 0:
			r.note("waiting", status+": the updater buys their block's header when it is due")
		default:
			r.note("waiting", status+": no batch gathers them (each block holds a batch already, or a header proof serves it): the updater buys or proves each block's header when it is due")
		}
		return nil
	}
	// the batch is re-emitted when the fees of the ones it gathers cover the whole cost of the header it will need and
	// of their deliveries from its leaf (a batch's Dispatch events), or its oldest send has waited six hours (the updater
	// then buys its header within its budget): a re-emit costs gno gas, never ETH (§5.5)
	facts, err := r.env.dueFacts(ctx, r.Sender, latest, batch, r.now(), r.env.headerGasAt(ctx, cs.TrustedHeight+1), func(*outMsg) (int, int) { return len(batch), 0 })
	if err != nil {
		return err
	}
	facts.Lead, facts.AgeReemits = updateLead, true
	due, _, why := headerDue(facts)
	r.note("waiting", fmt.Sprintf("%s; a batch of %d: %s", status, len(batch), why))
	if !due || r.now().Sub(r.state.LastBatch) < reemitSpacing {
		return nil
	}
	nonces := make([]uint32, len(batch))
	for i, t := range batch {
		nonces[i] = t.n
	}
	return r.batchReemit(ctx, nonces)
}

// batchReemit re-emits the nonces in one transaction of Reemit MsgCalls — never a MsgRun, which gnoland-1's
// run_submitters refuses from any key but GovDAO's three T1 members (E3-AS1): their newest events share its block and
// its one results leaf, which one update serves.
func (r *Relayer) batchReemit(ctx context.Context, nonces []uint32) error {
	key := "reemit:batch"
	if !r.act(key) || !r.state.Attempts[key].due(r.now()) {
		return nil
	}
	res, err := r.runner.Reemit(ctx, r.Cfg.Gno.Mailbox, nonces, r.GnoGas)
	if err != nil {
		r.gnoFailed(key, err)
		return err
	}
	r.succeeded(key)
	r.forget(key)
	for _, n := range nonces {
		r.state.Out.Index.Note(n, res.Height)
		r.state.Reemits[strconv.FormatUint(uint64(n), 10)] = r.now()
	}
	r.state.LastBatch = r.now()
	r.state.Batches++
	r.note(key, fmt.Sprintf("re-emitted %d messages at height %d: %s", len(nonces), res.Height, res.Hash))
	log.Printf("relayer: re-emitted %v at gno height %d in one transaction (%s): one update serves them", nonces, res.Height, res.Hash)
	return nil
}

// reemitBackoff is how long a message waits after its last re-emit while its re-emits keep landing in blocks nothing can
// be proven from (spoiled: how many did): a minute after the first, doubling, at most an hour; none for a message none
// of whose re-emits was spoiled.
func reemitBackoff(spoiled int) time.Duration {
	if spoiled <= 0 {
		return 0
	}
	d := time.Minute << uint(min(spoiled-1, 6))
	return min(d, time.Hour)
}

// poisonedAlarm stands reemit:poisoned while waiting messages have re-emits in blocks nothing can be proven from.
func (r *Relayer) poisonedAlarm(waiting []*outMsg) {
	var parts []string
	for _, t := range waiting {
		if t.spoiled > 0 {
			parts = append(parts, fmt.Sprintf("gno #%d (%d re-emit(s) spoiled, the next after %s)", t.n, t.spoiled, reemitBackoff(t.spoiled)))
		}
	}
	if len(parts) == 0 {
		r.Alarm.Clear("reemit:poisoned")
		return
	}
	r.Alarm.AlarmEvery("reemit:poisoned", "re-emits keep landing in blocks nothing can be proven from (a transaction beside each whose results the leaves cannot rebuild — a type of no schema, or a string the node's JSON cannot carry): "+strings.Join(parts, "; ")+"; each message's re-emits back off to an hour, and one whose dispatch's header the client's set signs is proven by a header proof instead", 6*time.Hour)
}

// proveOld proves, under -proveOld, the header after each block holding the newest event below the trusted height of
// waiting messages the plan would re-emit (Below: a send still at its dispatch, which the updater's header proofs leave
// to a re-emit first), when the ones it serves are due it (§5.5), signed by the current set (proveHeader), and answers
// the nonces it proved for. A held send whose newest event's header is accepted already needs a fresh event, not a
// proof.
func (r *Relayer) proveOld(ctx context.Context, cs *ethx.ClientState, latest *ethx.Block, waiting []*outMsg, below []uint32) []uint32 {
	groups := map[int64][]*outMsg{}
	var hs []int64
	for _, t := range msgsOf(waiting, below) {
		if t.newestProven {
			continue
		}
		var h int64
		for i := len(t.events) - 1; i >= 0; i-- {
			if t.events[i]+1 <= cs.TrustedHeight {
				h = t.events[i]
				break
			}
		}
		if h == 0 {
			continue
		}
		if groups[h] == nil {
			hs = append(hs, h)
		}
		groups[h] = append(groups[h], t)
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i] < hs[j] })
	var out []uint32
	for _, h := range hs {
		g := groups[h]
		key := fmt.Sprintf("prove:%d", h+1)
		// a header proof costs about an update and is bought from the relayers' float: by the fees of the sends it
		// serves alone, never by the six hours (no budget here)
		facts, err := r.env.dueFacts(ctx, r.Sender, latest, g, r.now(), r.env.headerGasAt(ctx, h+1), func(t *outMsg) (int, int) { return t.leaf(h) })
		if err != nil {
			r.note(key, err.Error())
			continue
		}
		if due, _, why := headerDue(facts); !due {
			r.note(key, "not proven yet: "+why)
			continue
		}
		if !r.ethAct(key) || !r.state.Attempts[key].due(r.now()) {
			continue
		}
		sh, err := signedHeader(ctx, r.light, r.Cfg.Gno.ChainID, h+1, nil)
		if err != nil {
			alarmEncoding(r.Alarm, err)
			r.note(key, err.Error())
			continue
		}
		r.Alarm.Clear("gno:encoding")
		if sh.Header.ValidatorsHashArray() != cs.SetHash {
			r.note(key, "header "+strconv.FormatInt(h+1, 10)+" is signed by a set that is not the client's current one: a re-emit instead")
			continue
		}
		bound := r.state.SigsBound
		if bound == 0 || (r.Cfg.Eth.MaxSigsPerCall > 0 && r.Cfg.Eth.MaxSigsPerCall < bound) {
			bound = r.Cfg.Eth.MaxSigsPerCall
		}
		calls, err := ethx.ProveHeaderCalls(sh, bound)
		if err != nil {
			r.note(key, err.Error())
			continue
		}
		ok := true
		for i, c := range calls {
			call := ethx.Call{To: Address(r.Cfg.Eth.Client), Data: c.Data, Label: fmt.Sprintf("proveHeader %d (%d/%d)", h+1, i+1, len(calls))}
			if facts.Decisions > 0 {
				call.MaxBaseFee = r.Cfg.EmergencyMaxBaseFee()
			}
			if _, err := r.ethSend(ctx, call); err != nil {
				ok = false
				if errors.Is(err, ethx.ErrGasCap) && len(c.Slots) > 1 {
					// a call of these signatures does not fit one transaction: staged at half as many from now on,
					// tried again next step (no failure counted toward a park)
					r.state.SigsBound = max(len(c.Slots)/2, 1)
					r.note(key, fmt.Sprintf("a header proof call of %d signatures does not fit the transaction gas cap: staged at %d a call from now on", len(c.Slots), r.state.SigsBound))
					break
				}
				r.ethFailed(key, err)
				break
			}
		}
		if ok {
			r.succeeded(key)
			r.forget(key)
			r.state.Out.noteHeader(h+1, cs.Epoch) // its events are read every step from now on (E5-GS4)
			var ns []uint32
			for _, t := range g {
				ns = append(ns, t.n)
			}
			out = append(out, ns...)
			r.note(key, fmt.Sprintf("header %d proven for gno %v (-proveOld)", h+1, ns))
		}
	}
	return out
}

// encodingError is a header read from the node whose fields hash, as the client hashes them, to other than the node's
// own hash of the block (the assumptions audit's E3-AS3): a gno upgrade changed the header's encoding (or the node
// serves what no chain signed), and the client would refuse it — nothing is submitted, and the machine alarms
// gno:encoding (alarmEncoding), as the monitors do.
type encodingError struct{ text string }

func (e *encodingError) Error() string { return e.text }

// alarmEncoding raises gno:encoding for a header whose encoding is not the client's; any other error leaves it.
func alarmEncoding(a *Alarmer, err error) {
	var ee *encodingError
	if errors.As(err, &ee) {
		a.Alarm("gno:encoding", "gno.land's encodings differ from the client's — a gno upgrade changed what the client verifies, and it will refuse gno.land's headers until a kind 8 to a client that accepts them is executed (§6.5; E3-AS3): "+ee.text+"; nothing is submitted")
	}
}

// signedHeader reads a header, its commit, its set (and the next set when given) from the node, checked: the header
// hashed from its fields as the client hashes them must be the node's hash of the block (else an *encodingError).
func signedHeader(ctx context.Context, light gnoside.Light, chainID string, height int64, next []gnolight.Validator) (*ethx.SignedHeader, error) {
	blk, err := light.BlockAt(ctx, height)
	if err != nil {
		return nil, err
	}
	if got := blk.Header.Hash(); got != blk.Hash {
		return nil, &encodingError{fmt.Sprintf("header %d's fields hash to %x as the client hashes them, and the node's hash of the block is %x (the header's encoding)", height, got, blk.Hash)}
	}
	cm, _, err := light.CommitAt(ctx, height)
	if err != nil {
		return nil, err
	}
	set, err := light.ValidatorsAt(ctx, height)
	if err != nil {
		return nil, err
	}
	sh := &ethx.SignedHeader{ChainID: chainID, Header: blk.Header, Commit: cm, Set: set}
	if next != nil {
		sh.Next = &gnolight.ValidatorSet{Validators: next}
	}
	return sh, sh.Check()
}

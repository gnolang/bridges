package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The relayer's Solana side (PLAN.md §5.7, §6.2, §6.3): every hlmailbox message for a route or the admin program
// is delivered by a `process` under the pausable ISM (a route) or the light-client ISM (a decision) with a proof
// staged in the client's PDAs — the results leaf of the block that carries the Dispatch event, its SimpleProof
// over LastResultsHash, and the header after that block, by the ring when the client remembers it and otherwise
// with its commit, signed by the current set. Decisions go first, one copy per sequence number, only when it is
// the number the admin program expects next; a number another decision took is reported. A route message is
// staged only while the pausable ISM would take it: while any flag stands on it or the client's quarantine runs,
// nothing is staged (G2: a stage is rent the float pays; an unpause goes through the client, never the ISM).

// leafInfo is what a block's results say about one message: the leaf's index and size, or why the block's leaves
// cannot be rebuilt.
type leafInfo struct {
	index     int
	bytes     int
	buildable bool
	why       string
}

// solClientView answers the Reemit rule from the client's state and the node's block results.
type solClientView struct{ r *Relayer }

func (v *solClientView) CurrentSetFrom(ctx context.Context) (int64, error) {
	cs, err := readClientState(ctx, v.r.sol.RPC, v.r.Cfg.Programs().LightClient(), rpc.CommitmentConfirmed)
	if err != nil {
		return 0, err
	}
	return cs.Body.CurrentSetFrom(), nil
}

// LeafBytes answers from the leaf cache first (G8: a block's results do not change; one fetch per message and
// height, not one per tick).
func (v *solClientView) LeafBytes(ctx context.Context, height int64, id [32]byte) (int, bool, error) {
	if li, ok := v.r.leafs[leafKey(height, id)]; ok {
		return li.bytes, li.buildable, nil
	}
	li, _, err := v.r.leafAt(ctx, height, id)
	if err != nil {
		return 0, true, err
	}
	if !li.buildable {
		return 0, false, nil
	}
	return li.bytes, true, nil
}

func leafKey(height int64, id [32]byte) string {
	return strconv.FormatInt(height, 10) + "/" + hex.EncodeToString(id[:8])
}

// leafAt is the leaf of block height's results that carries the Dispatch of id, with the block's leaves (fetched:
// the proof needs them), the leaf's facts cached for the Reemit rule.
func (r *Relayer) leafAt(ctx context.Context, height int64, id [32]byte) (leafInfo, [][]byte, error) {
	key := leafKey(height, id)
	rs, err := r.light.BlockResults(ctx, height)
	if err != nil {
		return leafInfo{}, nil, err
	}
	leaves, err := gnolight.ResultLeaves(rs)
	if err != nil {
		li := leafInfo{index: -1, buildable: false, why: err.Error()}
		r.leafs[key] = li
		return li, nil, nil
	}
	idx := findLeaf(leaves, id, r.Cfg.Gno.Mailbox)
	if idx < 0 {
		return leafInfo{}, leaves, fmt.Errorf("block %d carries no Dispatch of %x", height, id[:8])
	}
	li := leafInfo{index: idx, bytes: len(leaves[idx]), buildable: true}
	r.leafs[key] = li
	return li, leaves, nil
}

// findLeaf is the index of the results leaf whose Dispatch event (of the mailbox realm) names the id, −1 if none.
func findLeaf(leaves [][]byte, id [32]byte, mailboxPkgPath string) int {
	for i, leaf := range leaves {
		ds, err := gnolight.DispatchEvents(leaf, mailboxPkgPath)
		if err != nil {
			continue
		}
		for _, d := range ds {
			if d.ID == id {
				return i
			}
		}
	}
	return -1
}

// msgKind is what a hlmailbox message is to the relayer: a route's token message, r/hlgov's decision to an
// admin program of the line (the live one, or a retired one — dead there, AP3), r/hlgov's decision to a program
// the line does not reach (G31: left undone and alarmed, never "other" — the operator's config names the line's
// start, and a successor the chain does not name yet is delivered once it does), or not ours (nobody here
// delivers it).
type msgKind int

const (
	kindOther msgKind = iota
	kindRoute
	kindDecision
	kindOtherAdmin
	// kindLineUnread is a route message whose sender is not the configured realm while the token's line of
	// migrations could not be read this tick (G46): held like kindOtherAdmin — never walked past as "other", since
	// the sender may be a successor the line names once it reads.
	kindLineUnread
)

// adminLine is the admin program's line as read this tick (AQ6), or the configured program alone before any
// read (the tests, a tick whose read failed).
func (r *Relayer) adminLine() *adminChain {
	if r.admins != nil {
		return r.admins
	}
	return &adminChain{Live: r.Cfg.Programs().AdminProgram()}
}

func (r *Relayer) classify(m *gnoMsg) (msgKind, *Route) {
	if m.Msg.Destination != r.Cfg.Solana.Domain {
		return kindOther, nil
	}
	rcpt := solana.PublicKeyFromBytes(m.Msg.Recipient[:])
	if gov, err := r.Cfg.GovSender(); err == nil && gov == m.Msg.Sender {
		if r.adminLine().Knows(rcpt) {
			return kindDecision, nil
		}
		if rt := r.Cfg.RouteByToken(rcpt); rt == nil {
			return kindOtherAdmin, nil
		}
	}
	if r.adminLine().Knows(rcpt) {
		return kindOther, nil // to an admin program from a stranger: the program refuses it
	}
	rt := r.Cfg.RouteByToken(rcpt)
	if rt == nil {
		return kindOther, nil
	}
	if !r.lineSender(rt, m.Msg.Sender) {
		if r.lineUnread(rt) {
			return kindLineUnread, rt // the sender may be a successor the line names once it reads (G46): held
		}
		return kindOther, nil // not from a route realm of the token's line: the token program would refuse it
	}
	return kindRoute, rt
}

// checkRouters reads each token program's enrolled router for gno.land's domain (sol.ParseToken, RouterFor —
// the "enrolled gno router" §5.7's `ours` rule names) and alarms router:unknown:<token> while it is no realm
// of the configured route's line (K34): the walk takes the line's realms as ours, so a router outside it is a
// config edit due, or a kind 6 to a stranger. Nothing is read without a Solana reader (the tests' relayer).
func (r *Relayer) checkRouters(ctx context.Context, gnoDomain uint32) {
	if r.solData == nil {
		return
	}
	for _, rt := range r.Cfg.Routes {
		if rt.Token == "" {
			continue
		}
		key := "router:unknown:" + rt.Symbol
		if err := r.lineErrs[rt.Symbol]; err != nil {
			r.note(key, "not judged this tick: the route's line of migrations could not be read (G46): "+err.Error())
			continue // the router is judged against the line, which is unread
		}
		data, err := r.solData(ctx, sol.Synthetic{Program: rt.TokenKey()}.Token())
		if err != nil {
			r.note(key, "the token's state could not be read: "+err.Error())
			continue
		}
		tok, err := sol.ParseToken(data)
		if err != nil {
			r.note(key, "the token's state does not parse: "+err.Error())
			continue
		}
		router, ok := tok.RouterFor(gnoDomain)
		switch {
		case !ok:
			r.Alarm.Alarm(key, fmt.Sprintf("the %s token program enrolls no router for gno.land's domain %d: no send of the token can mint until kind 6 enrolls one", rt.Symbol, gnoDomain))
		case !r.lineSender(&rt, router):
			if path, pred := r.droppedRoute(ctx, router); path != "" {
				// a re-pointed-away successor (M1i, R35; AS1): a route no more, whose sends gno.land refuses and
				// whose burns are the line's last realm's — nothing of the token mints until a kind 6 enrols the live
				// successor, and the alarm stands until then
				r.Alarm.Alarm(key, fmt.Sprintf("the %s token program's enrolled router for gno.land (%x) is a DROPPED route %s (re-pointed away by %s): a route no more — its sends are refused on gno.land and burns to it wait for the line's last realm; no send of the token mints until a kind 6 enrols the live successor (AS1)", rt.Symbol, router[:], path, pred))
				continue
			}
			r.Alarm.Alarm(key, fmt.Sprintf("the %s token program's enrolled router for gno.land (%x) is no realm of the configured route's line (%s and its migrations): the sends through it are not delivered by this relayer — edit routes[].gno, or undo a kind 6 to a stranger (K34)", rt.Symbol, router[:], rt.Gno))
		default:
			r.Alarm.Clear(key)
		}
	}
}

// droppedRoute names the dropped route a key is the address of, with the predecessor that re-pointed away from
// it (the mailbox's droppedRoutes, M1i); "" for none, or with the mailbox unread.
func (r *Relayer) droppedRoute(ctx context.Context, key [32]byte) (path, predecessor string) {
	mb, err := r.mailboxState(ctx)
	if err != nil {
		return "", ""
	}
	for p, pred := range mb.DroppedRoutes {
		if RealmKey(p) == key {
			return p, pred
		}
	}
	return "", ""
}

// decisionGate is the rule for a decision's number against the admin program's last: "deliver" when it is the
// next expected, "wait" when an earlier number must come first, "past" when the number is recorded already.
func decisionGate(seq, last uint64) string {
	switch {
	case seq <= last:
		return "past"
	case seq == last+1:
		return "deliver"
	}
	return "wait"
}

// deliveriesPausedNow says whether gno → Solana deliveries under the pausable ISM are refused now: any flag on
// it, or the client's quarantine standing by the Clock's time (§5.9's "deliveries paused", read from the same
// two facts the ISM's Verify reads).
func deliveriesPausedNow(g *sol.Guardians, quarantineEnd, now int64) (bool, string) {
	switch {
	case g != nil && g.AnyFlag():
		return true, "a flag stands on the pausable ISM"
	case quarantineEnd > now:
		return true, fmt.Sprintf("the quarantine stands until %s", time.Unix(quarantineEnd, 0).UTC().Format(time.RFC3339))
	}
	return false, ""
}

// todo is one undelivered message of ours in the walk.
type todo struct {
	n    uint32
	msg  *gnoMsg
	kind msgKind
	rt   *Route
	seq  uint64
	dk   byte
}

// deliverUnderLightClient walks hlmailbox's messages from the first not done, sorts ours, delivers the decisions
// first (the admin program's gate), then the routes' unless deliveries are paused.
func (r *Relayer) deliverUnderLightClient(ctx context.Context) error {
	m, err := mailboxAt(ctx, r.gno, r.Cfg.Gno.Mailbox, 0)
	if err != nil {
		return err
	}
	if m.Count == 0 {
		return nil
	}
	progs := r.Cfg.Programs()
	mailbox := sol.Mailbox{Program: progs.Mailbox}
	// the admin program's line of succession, read once a tick (AQ6): a decision is delivered to the program it
	// names when the line knows it, gated by that program's own counter (a test's relayer without a provider
	// keeps the configured program)
	if r.sol != nil && r.sol.RPC != nil {
		if chain, err := followSuccessors(ctx, r.sol.RPC, progs.AdminProgram(), rpc.CommitmentConfirmed); err != nil {
			log.Printf("relayer: the admin program's line of succession could not be read (the configured program stands in): %v", err)
			r.admins = nil
		} else {
			r.admins = chain
			if text := chain.Text(); text != "" {
				r.note("admin", text)
			}
		}
	}
	// the routes' lines, for classify (K34): a send through a successor realm is ours; the enrolled router checked
	// against the line once a tick
	r.readLines(ctx)
	r.checkRouters(ctx, m.Domain)
	var decisions, routes []todo
	advance := true
	for n := r.state.GnoNext; n < m.Count; n++ {
		nk := strconv.FormatUint(uint64(n), 10)
		if r.state.Done[nk] != "" {
			if advance {
				r.state.GnoNext = n + 1
			}
			continue
		}
		msg, err := gnoMessage(ctx, r.gno, r.Cfg.Gno.Mailbox, m.Domain, n)
		if err != nil {
			return err
		}
		kind, rt := r.classify(msg)
		if kind == kindOther {
			r.state.Done[nk] = "other"
			if advance {
				r.state.GnoNext = n + 1
			}
			continue
		}
		if kind == kindOtherAdmin {
			// r/hlgov's decision to a program the line does not reach (G31): undone, the walk held at it, alarmed —
			// a successor the chain names later is delivered then, never walked past as "other"
			advance = false
			rcpt := solana.PublicKeyFromBytes(msg.Msg.Recipient[:])
			r.note("deliver:"+nk, fmt.Sprintf("undone: r/hlgov addressed it to %s, which is neither the admin program %s nor a retired one of its line (G31)", rcpt, r.adminLine().Live.Program))
			// raised every tick, posted daily (G45: hourly on the webhook for the week a switch takes drowned the rest)
			r.Alarm.AlarmEvery("decision:other-program:"+nk, fmt.Sprintf("gno #%d is r/hlgov's decision to %s, a program the admin program's line (%s) does not name: left undone, never walked past — a successor the chain names later is delivered then; a stranger's program needs r/hlgov's recipient switch undone (G31)", n, rcpt, r.adminLine().Live.Program), 24*time.Hour)
			continue
		}
		if kind == kindLineUnread {
			// a route message from anyone but the configured realm while the token's line could not be read this
			// tick (G46): held, the walk stopped at it, never "other" — a successor's send is walked once the line
			// reads (readLines alarms the unread line)
			advance = false
			r.note("deliver:"+nk, fmt.Sprintf("held: %s's line of migrations could not be read this tick, so the sender (%x) may be a successor realm of it (G46); walked once the line reads", rt.Symbol, msg.Msg.Sender[:]))
			continue
		}
		r.Alarm.Clear("decision:other-program:" + nk)
		delivered, err := r.solProcessed(ctx, msg.ID)
		if err != nil {
			return err
		}
		key := "deliver:" + nk
		if delivered {
			r.closeDeliveredStage(ctx, progs.LightClient(), msg)
			r.state.Done[nk] = "delivered"
			r.forget(key)
			if advance {
				r.state.GnoNext = n + 1
			}
			continue
		}
		advance = false
		t := todo{n: n, msg: msg, kind: kind, rt: rt}
		if kind == kindDecision {
			dk, s, ok := gnolight.DecisionPrefix(msg.Msg.Body)
			if !ok {
				r.state.Done[nk] = "other" // a short body fails Handle; the realm never writes one
				continue
			}
			t.seq, t.dk = s, dk
			r.rememberCopy(s, n, msg.ID)
			decisions = append(decisions, t)
			continue
		}
		routes = append(routes, t)
	}
	var cs *clientState
	clientNow := func() (*clientState, error) {
		if cs == nil {
			cs, err = readClientState(ctx, r.sol.RPC, progs.LightClient(), rpc.CommitmentConfirmed)
		}
		return cs, err
	}
	var firstErr error
	// 1. the decisions, by number: one copy per number, only the next expected (the admin state at finalized,
	// as the processed-message records are read, G4) — the state of the program the message names (AQ6: the
	// live one, or a retired one whose delivery answers Retired, AP3)
	admins := map[solana.PublicKey]*sol.AdminState{}
	for _, t := range decisions {
		nk := strconv.FormatUint(uint64(t.n), 10)
		key := "deliver:" + nk
		rcpt := solana.PublicKeyFromBytes(t.msg.Msg.Recipient[:])
		if r.retireCopy(t, key, nk, rcpt) {
			continue // addressed to a retired program of the line: dead there, whatever its number (G39)
		}
		admin := admins[rcpt]
		if admin == nil {
			if admin, err = readAdmin(ctx, r.sol.RPC, sol.Admin{Program: rcpt}, rpc.CommitmentFinalized); err != nil {
				return err
			}
			admins[rcpt] = admin
		}
		switch decisionGate(t.seq, admin.LastSeq) {
		case "past":
			r.pastDecision(ctx, t.seq, t.dk, nk)
			continue
		case "wait":
			r.note(key, fmt.Sprintf("decision %d waits: the admin program expects %d next", t.seq, admin.LastSeq+1))
			continue
		}
		if !r.act(key) {
			continue
		}
		if !r.state.Attempts[key].due(r.now()) {
			continue
		}
		if _, err := clientNow(); err != nil {
			return err
		}
		if err := r.deliverTodo(ctx, cs, mailbox, t, key, nk); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(admins, rcpt) // the next number is expected now
	}
	// 2. the routes, unless deliveries are paused: a stage is rent, and the ISM would refuse the process
	if len(routes) > 0 {
		if _, err := clientNow(); err != nil {
			return err
		}
		_, g, err := readPausable(ctx, r.sol.RPC, progs.PausableISM(), rpc.CommitmentConfirmed)
		if err != nil {
			return err
		}
		if paused, why := deliveriesPausedNow(g, cs.Head.QuarantineEnd, r.now().Unix()); paused {
			r.note("deliveries", "paused: "+why+" ("+strconv.Itoa(len(routes))+" route messages wait, nothing staged)")
			for _, t := range routes {
				delete(r.state.Ready, "deliver:"+strconv.FormatUint(uint64(t.n), 10))
			}
			return firstErr
		}
		r.note("deliveries", "open")
	}
	// a delivery parked on the pausable ISM's OverDayCap is retried when the day's bucket, read at finalized once a
	// pass, has room for its amount — oldest first, the walk's order, what this pass delivered counted — and not
	// by the hour (AO5: a blind retry against a full bucket is a staging's rent for nothing); while a token's
	// oldest such park waits, its amount is reserved against every younger message of the token (AP2); a park
	// whose amount exceeds the live maximum or day cap (a lowering kind 12 since it parked) reserves nothing and
	// is re-marked OverMax, waiting for a raise (AQ4: it held every younger message of the token for good)
	var limits *sol.PausableState
	var passUsed [2]uint64
	parks := r.dropRefundedParks(ctx, capParks(routes, r.state.Attempts)) // a refunded park reserves nothing (G53)
	if parks[0] != nil || parks[1] != nil {
		var err error
		if limits, _, err = readPausable(ctx, r.sol.RPC, progs.PausableISM(), rpc.CommitmentFinalized); err != nil {
			r.note("deliveries", "the day cap's parks hold: the pausable ISM's state could not be read: "+err.Error())
			return firstErr
		}
		for _, p := range overLimitParks(parks, limits) {
			nk := strconv.FormatUint(uint64(p.n), 10)
			key := "deliver:" + nk
			if a := r.state.Attempts[key]; a != nil {
				a.Cause = overMaxByLimits
				r.note(key, fmt.Sprintf("parked (OverMax by the live limits): %d is above the per-message maximum %d or the day cap %d after a lowering kind 12; it reserves nothing and waits for a kind 12 raise (AQ4)", p.amount, limits.Limits[p.tok].MaxPerMessage, limits.Limits[p.tok].DayCap))
				r.Alarm.Alarm("parked:"+key, fmt.Sprintf("gno #%d parked (OverMax by the live limits): its %d is above the per-message maximum %d or the day cap %d after a lowering kind 12; it reserves nothing and waits for a kind 12 raise (AQ4)", p.n, p.amount, limits.Limits[p.tok].MaxPerMessage, limits.Limits[p.tok].DayCap))
			}
			parks[p.tok] = nil
		}
	}
	for _, t := range routes {
		nk := strconv.FormatUint(uint64(t.n), 10)
		key := "deliver:" + nk
		if r.state.Done[nk] != "" {
			continue // ended this pass (a refunded park, G53)
		}
		if !r.act(key) {
			continue
		}
		a := r.state.Attempts[key]
		overCap := overDayCap(a)
		if !overCap && !a.due(r.now()) {
			continue
		}
		tok, amount, known := tokenAmount(t)
		// a park above the live per-message maximum or the live day cap is not staged: the ISM would answer OverMax
		// or OverDayCap, a staging's rent for nothing an hour (AQ4; G45: the gate read the maximum alone, so a park
		// under it but over a lowered day cap was staged on every hourly retry); it waits for a kind 12 raise
		if known && limits != nil && a.parked() && overLiveLimits(amount, limits.Limits[tok]) {
			r.note(key, fmt.Sprintf("parked (OverMax by the live limits): %d is above the per-message maximum %d or the day cap %d; waits for a kind 12 raise (AQ4)", amount, limits.Limits[tok].MaxPerMessage, limits.Limits[tok].DayCap))
			continue
		}
		var park *capPark
		if known {
			park = parks[tok]
		}
		if known && (overCap || park != nil && t.n > park.n) {
			if limits == nil {
				var err error
				if limits, _, err = readPausable(ctx, r.sol.RPC, progs.PausableISM(), rpc.CommitmentFinalized); err != nil {
					r.note(key, "held under the day cap: the pausable ISM's state could not be read: "+err.Error())
					continue
				}
			}
			day := sol.UnpauseDelay(max(int64(limits.ClockFactor), 1))
			if ok, why := capGate(t.n, amount, overCap, park, limits.Limits[tok], r.now().Unix(), day, passUsed[tok]); !ok {
				r.note(key, why)
				continue
			}
		}
		// a send gno.land refunded must never deliver (AQ2): the mark is read before anything is staged — after
		// the cap gate, for the message about to be staged alone, and only where a mark can be set (G42: a live
		// route's send is never refunded; a dozen held sends were a render each every tick)
		if refunded, why, err := r.sendRefunded(ctx, t); err != nil {
			r.note(key, "waits: the route's send record could not be read (the refund mark is read before staging, AQ2): "+err.Error())
			continue
		} else if refunded {
			r.endRefunded(t, why)
			continue
		}
		err := r.deliverTodo(ctx, cs, mailbox, t, key, nk)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if err == nil && known {
			passUsed[tok] += amount
			if park != nil && park.n == t.n {
				parks[tok] = nil // the park went: its amount is counted in passUsed now, reserved no more
			}
		}
	}
	return firstErr
}

// overDayCap says whether a delivery is parked on the pausable ISM's OverDayCap (by the park's cause, G35: a
// transient retry no longer overwrites it).
func overDayCap(a *attempt) bool {
	return a.parked() && a.causeIs("OverDayCap")
}

// overMaxByLimits is the cause a park is re-marked with when its amount exceeds the live limits (AQ4).
const overMaxByLimits = "pausable ISM: OverMax (by the live limits after a lowering kind 12)"

// capPark is a token's oldest delivery parked on OverDayCap in the walk: while it waits, no younger message of
// its token is delivered that would leave it short — its amount is reserved (AP2: "oldest first" ordered the
// attempts but reserved nothing, so a parked large send starved behind younger ones that fit as the bucket
// drained; §5.7 lets a large send hold the younger ones for amount ÷ cap of a day).
type capPark struct {
	n      uint32
	amount uint64
	tok    int
	t      todo // the parked message itself (its refund mark is read before it reserves, G53)
}

// capParks finds each token's oldest OverDayCap park among the route messages (the walk is oldest first).
func capParks(routes []todo, attempts map[string]*attempt) [2]*capPark {
	var out [2]*capPark
	for _, t := range routes {
		if !overDayCap(attempts["deliver:"+strconv.FormatUint(uint64(t.n), 10)]) {
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

// dropRefundedParks reads the refund mark for each token's oldest OverDayCap park (one render a tick per token)
// and ends a refunded one — Done "refunded", its attempt forgotten, alarmed, as the walk's own refunded branch
// does — so it reserves nothing (G53): the mark is otherwise read for the message about to be staged alone
// (G42), which a park under a full bucket never is, and a refunded park held every younger send of its token
// until the bucket could have fitted it, a quarter of a day of flow. A mark that cannot be read keeps the
// reservation (the park is a park until the realm says otherwise).
func (r *Relayer) dropRefundedParks(ctx context.Context, parks [2]*capPark) [2]*capPark {
	for tok, p := range parks {
		if p == nil {
			continue
		}
		refunded, why, err := r.sendRefunded(ctx, p.t)
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

// endRefunded ends a route message gno.land refunded (AQ2): never delivered, its attempt and parked alarm gone,
// the holder alarmed once.
func (r *Relayer) endRefunded(t todo, why string) {
	nk := strconv.FormatUint(uint64(t.n), 10)
	key := "deliver:" + nk
	r.state.Done[nk] = "refunded"
	r.forget(key)
	r.note(key, "refunded: "+why)
	r.Alarm.Alarm("send:refunded:"+nk, fmt.Sprintf("gno #%d is not delivered: %s", t.n, why))
	log.Printf("relayer: gno #%d was refunded on gno.land: never delivered (AQ2)", t.n)
}

// overLiveLimits says whether an amount exceeds a token's live per-message maximum or day cap: a send the
// pausable ISM can never fit until a kind 12 raise (AQ4, G45).
func overLiveLimits(amount uint64, l sol.MintLimit) bool {
	return amount > l.MaxPerMessage || amount > l.DayCap
}

// overLimitParks are the parks whose amount exceeds the live per-message maximum or day cap (AQ4: after a
// lowering kind 12 such a park can never go — the bucket never holds it — so it reserves nothing; re-marked
// OverMax, it waits for a raise).
func overLimitParks(parks [2]*capPark, limits *sol.PausableState) []*capPark {
	var out []*capPark
	for tok, p := range parks {
		if p == nil {
			continue
		}
		if overLiveLimits(p.amount, limits.Limits[tok]) {
			out = append(out, p)
		}
	}
	return out
}

// sendRefunded says whether a route message is a send gno.land refunded (AQ2): the route's send record under
// the message id carries the mark — on the route, or on another realm of its line once the route migrated (a
// send never refunded moves with its page; a refunded one stays). A live route's send is never refunded (a
// refund is a successor's duty over the sends it imported), so its record is not read (G42). A realm that
// renders no such record is an error: the mark is read before anything is staged, never assumed.
func (r *Relayer) sendRefunded(ctx context.Context, t todo) (bool, string, error) {
	if t.rt == nil {
		return false, "", nil
	}
	rj, err := r.routeState(ctx, t.rt.Gno)
	if err != nil {
		return false, "", err
	}
	mig, err := r.migrationFor(ctx, *t.rt)
	if err != nil {
		return false, "", err
	}
	if !refundable(rj, mig) {
		return false, "", nil
	}
	id := hex.EncodeToString(t.msg.ID[:])
	sd, err := sendOnPair(ctx, r.gno, t.rt.Gno, mig, id, 0)
	if err != nil {
		return false, "", err
	}
	if sd == nil || !bool(sd.Refunded) {
		return false, "", nil
	}
	return true, fmt.Sprintf("send %s (%s, %d to %s) was refunded on gno.land by GovDAO's decision: the amount left the lock, so a delivery would mint against nothing — it must never deliver (AQ2)", id, t.rt.Symbol, sd.Amount, sd.From), nil
}

// capGate says whether a route message goes now under the day cap (AO5, AP2): one parked on OverDayCap when the
// bucket — its level now, plus what this pass delivered — has room for it; a message younger than its token's
// oldest park only when the bucket has room for the park's amount beside its own; the park itself and anything
// older at once. The text says why not.
func capGate(n uint32, amount uint64, overCap bool, park *capPark, l sol.MintLimit, now, day int64, used uint64) (bool, string) {
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

// TokenIndex is tokenIndex for the commands (hlwatch's kind-6 lookup names the token by it, F27).
func TokenIndex(symbol string) (int, bool) { return tokenIndex(symbol) }

// tokenIndex is a route symbol's place in the pausable ISM's limits (config::TOKENS' order: 0 GNOT, 1 BUBBLE).
func tokenIndex(symbol string) (int, bool) {
	switch symbol {
	case "GNOT":
		return 0, true
	case "BUBBLE":
		return 1, true
	}
	return 0, false
}

// tokenAmount is a route message's token (the pausable ISM's order: 0 GNOT, 1 BUBBLE) and amount.
func tokenAmount(t todo) (token int, amount uint64, ok bool) {
	if t.rt == nil {
		return 0, 0, false
	}
	token, ok = tokenIndex(t.rt.Symbol)
	if !ok {
		return 0, 0, false
	}
	_, amount, _, err := hl.ParseTokenMessage(t.msg.Msg.Body)
	return token, amount, err == nil
}

// bucketRoom says whether a token's day bucket — its level now, plus what this pass already delivered — has room
// for an amount (AO5); the text says why not.
func bucketRoom(l sol.MintLimit, now, day int64, delivered, amount uint64) (bool, string) {
	level := l.Level(now, day) + delivered
	if level+amount <= l.DayCap && amount <= l.MaxPerMessage {
		return true, ""
	}
	return false, fmt.Sprintf("the day's bucket holds %d of %d, %d asked", level, l.DayCap, amount)
}

// closeDeliveredStage closes a delivered message's proof stage when it still stands (G21: a process confirmed
// after Send gave up waiting left the stage on chain, ≈0.01 SOL of rent held for ever).
func (r *Relayer) closeDeliveredStage(ctx context.Context, client sol.LightClient, msg *gnoMsg) {
	if r.sol == nil || r.solData == nil || r.solSend == nil {
		return
	}
	submitter := r.sol.Payer.PublicKey()
	stage := client.ProofStage(submitter, gnolight.MessageHash(msg.Raw))
	if data, err := r.solData(ctx, stage); err != nil || len(data) == 0 {
		return
	}
	if _, err := r.solSend(ctx, []solana.Instruction{client.StageCloseIx(stage, submitter)}); err != nil {
		log.Printf("relayer: gno #%d is delivered; closing its proof stage failed: %v (the rent stays until it is closed)", msg.Msg.Nonce, err)
		return
	}
	log.Printf("relayer: gno #%d is delivered; its proof stage, still on chain, is closed (the rent comes back)", msg.Msg.Nonce)
}

// deliverTodo delivers one message with §5.7's attempt bookkeeping.
func (r *Relayer) deliverTodo(ctx context.Context, cs *clientState, mailbox sol.Mailbox, t todo, key, nk string) error {
	a := r.state.Attempts[key]
	if err := r.deliverOne(ctx, cs, mailbox, t.msg, t.kind, t.rt, t.seq); err != nil {
		return r.deliverFailed(t, key, nk, err)
	}
	if a.parked() {
		r.Alarm.Clear("parked:" + key)
	}
	delete(r.state.Attempts, key)
	r.state.Done[nk] = "delivered"
	r.forget(key)
	r.note(key, "delivered")
	return nil
}

// retireCopy ends a decision copy addressed to a retired program of the line at the gate (G39): the program
// records nothing more, so a copy whose number is past its last would "wait" for ever — the walk stuck at it,
// the Done map and the state file growing by every later nonce, the Reemit rule re-emitting the dead copy at
// every set change. Retired needs no attempt to be known: the line read this tick says it (AQ6); the ending,
// its alarm and the copy's record are deliverFailed's for an attempt that answered Retired (AP3). It answers
// whether the copy was ended.
func (r *Relayer) retireCopy(t todo, key, nk string, rcpt solana.PublicKey) bool {
	if !r.adminLine().IsRetired(rcpt) {
		return false
	}
	r.state.Done[nk] = "retired"
	r.forget(key)
	r.note(key, fmt.Sprintf("retired: the admin program %s handed every authority to its successor (kind 9) and takes no decision — dead there whatever its number %d (G39)", rcpt, t.seq))
	r.Alarm.Alarm("decision:retired:"+nk, fmt.Sprintf("gno #%d is r/hlgov's decision %d to %s, which handed every authority to its successor (kind 9) and takes no decision — the message is dead there, never attempted (the program records nothing more, so no number of it is ever next); r/hlgov's set decision recipient to the live program %s, then a re-dispatch, delivers it (§6.5, AP3, G39)", t.n, t.seq, rcpt, r.adminLine().Live.Program))
	log.Printf("relayer: gno #%d is addressed to the retired admin program %s: dead there, re-dispatched after r/hlgov's recipient switch (G39)", t.n, rcpt)
	return true
}

// deliverFailed sorts one delivery's failure: a wait (errNotYet); the admin program's Retired — it handed every
// authority to its successor (kind 9) and takes no decision, so the message is dead there and never parked for
// retry: r/hlgov's `set decision recipient` to the successor and a re-dispatch deliver it (§6.5, AP3); or §5.7's
// attempt bookkeeping toward parking (a program's refusal named first, on the attempt, the note, the alarm and
// the error alike — the pausable ISM's OverMax on a mint above the token's maximum, AN5; NotInherited among
// them: a successor before its predecessor's kind 9, deliverable once it executes, so an hourly retry is right).
func (r *Relayer) deliverFailed(t todo, key, nk string, err error) error {
	if errors.Is(err, errNotYet) {
		r.note(key, "waits: "+err.Error())
		return nil
	}
	if name := solErrName(err); name != "" {
		err = fmt.Errorf("%s: %w", name, err)
	}
	if solErrIs(err, "Retired") {
		r.state.Done[nk] = "retired"
		r.forget(key)
		r.note(key, "retired: "+err.Error())
		r.Alarm.Alarm("decision:retired:"+nk, fmt.Sprintf("gno #%d answers Retired: the admin program handed every authority to its successor (kind 9) and takes no decision — the message is dead there, never parked for retry; r/hlgov's set decision recipient to the successor, then a re-dispatch, delivers it (§6.5, AP3): %v", t.n, err))
		log.Printf("relayer: gno #%d is Retired on the admin program: dead there, re-dispatched after r/hlgov's recipient switch (AP3)", t.n)
		return nil
	}
	a := r.state.Attempts[key]
	if a == nil {
		a = &attempt{}
		r.state.Attempts[key] = a
	}
	if parkedNow := a.fail(r.now(), err, solDeterministic(err), solErrName(err)); parkedNow || a.parked() {
		r.Alarm.Alarm("parked:"+key, fmt.Sprintf("gno #%d parked after %d failures (retried hourly): %v", t.n, a.Failures, err))
	}
	r.note(key, "failed: "+err.Error())
	return fmt.Errorf("gno #%d: %w", t.n, err)
}

// doneForReemit says whether a message's end on Solana leaves nothing to re-emit: delivered, recorded (a
// decision's number), not ours, retired (AP3: a fresh Dispatch event of a message to the retired program is
// no help; GovDAO re-dispatches the decision to the successor), or refunded (AQ2: it must never deliver).
func doneForReemit(done string) bool {
	return done == "delivered" || done == "recorded" || done == "other" || done == "retired" || done == "refunded"
}

// rememberCopy records a message as a copy of a decision number (kept across passes, G4); a fresh copy of a
// number whose earlier copy answered Retired is the re-dispatch, which ends that copy's alarm (G35).
func (r *Relayer) rememberCopy(seq uint64, n uint32, id [32]byte) {
	sk := strconv.FormatUint(seq, 10)
	for _, c := range r.state.DecisionCopies[sk] {
		if c.Nonce == n {
			return
		}
	}
	for _, c := range r.state.DecisionCopies[sk] {
		ck := strconv.FormatUint(uint64(c.Nonce), 10)
		if r.state.Done[ck] == "retired" {
			r.Alarm.Clear("decision:retired:" + ck)
		}
	}
	r.state.DecisionCopies[sk] = append(r.state.DecisionCopies[sk], decisionCopy{Nonce: n, ID: hex.EncodeToString(id[:])})
}

// pastDecision ends a copy whose number the admin program has recorded, read at finalized as the records are
// (G4): "recorded" when any copy of the number went through our mailbox — one this relayer delivered or
// recorded in an earlier pass, or one whose processed-message record stands at finalized — and "taken" only when
// no copy's record is there on a second pass either (the first pass only notes it: a copy another relayer
// delivered seconds ago has its record at confirmed, not yet at finalized). A taken number is reported once
// (§5.7: for r/hlgov's sync sequence).
func (r *Relayer) pastDecision(ctx context.Context, seq uint64, kind byte, nk string) {
	sk := strconv.FormatUint(seq, 10)
	key := "deliver:" + nk
	processed := false
	for _, c := range r.state.DecisionCopies[sk] {
		ck := strconv.FormatUint(uint64(c.Nonce), 10)
		if d := r.state.Done[ck]; d == "delivered" || d == "recorded" {
			processed = true
			break
		}
		id, err := hl.ParseKey32(c.ID)
		if err != nil {
			continue
		}
		if ok, err := r.solProcessed(ctx, id); err == nil && ok {
			processed = true
			break
		}
	}
	if processed {
		r.state.Done[nk] = "recorded"
		delete(r.state.TakenSeen, sk)
		r.forget(key)
		return
	}
	if _, seen := r.state.TakenSeen[sk]; !seen {
		r.state.TakenSeen[sk] = r.now().Unix()
		r.note(key, fmt.Sprintf("decision %d is recorded on the admin program and no copy of ours is processed at finalized yet: looked at again next pass", seq))
		return
	}
	r.state.Done[nk] = "taken"
	r.forget(key)
	if !r.state.Taken[sk] {
		r.state.Taken[sk] = true
		r.Alarm.Alarm("decision:taken:"+sk, fmt.Sprintf("decision %d (kind %d) cannot be delivered: another decision has already taken its number on the admin program (no copy of r/hlgov's is processed at finalized, on two passes); r/hlgov's sync sequence moves the counter past it and the proposal is re-made (§6.5)", seq, kind))
	}
}

// deliverOne stages the proof for one message and processes it. A stage left on chain by another plan is closed
// and rebuilt (driveStage); a process the chain refuses for good closes the stage too, so the rent comes back
// and the next attempt starts from the plan of its day (G1).
func (r *Relayer) deliverOne(ctx context.Context, cs *clientState, mailbox sol.Mailbox, msg *gnoMsg, kind msgKind, rt *Route, seq uint64) error {
	progs := r.Cfg.Programs()
	client, pausable := progs.LightClient(), progs.PausableISM()
	// a decision goes to the admin program it names (AQ6: the live one of the line, or a retired one, which
	// answers Retired); the program's own id derives the state and decision accounts
	admin := sol.Admin{Program: solana.PublicKeyFromBytes(msg.Msg.Recipient[:])}
	nk := strconv.FormatUint(uint64(msg.Msg.Nonce), 10)
	eventHeight := msg.Height
	if h := r.state.Reemits[nk]; h > eventHeight {
		eventHeight = h
	}
	li, leaves, err := r.leafAt(ctx, eventHeight, msg.ID)
	if err != nil {
		return err
	}
	if !li.buildable {
		return fmt.Errorf("%w: block %d's results cannot be rebuilt (%s); a Reemit is asked", errNotYet, eventHeight, li.why)
	}
	if li.bytes > sol.MaxLeaf {
		return fmt.Errorf("%w: the results leaf is %d bytes, above the ISM's %d; a Reemit is asked", errNotYet, li.bytes, sol.MaxLeaf)
	}
	H := eventHeight + 1
	if uint64(H) > cs.Head.TrustedHeight {
		return fmt.Errorf("%w: header %d is above the client's trusted height %d (the updater brings it)", errNotYet, H, cs.Head.TrustedHeight)
	}
	blk, err := r.light.BlockAt(ctx, H)
	if err != nil {
		return err
	}
	hdr := blk.Header
	if hdr.Hash() != blk.Hash {
		return fmt.Errorf("header %d hashes to %x here, the node says %x", H, hdr.Hash(), blk.Hash)
	}
	root, ok := hdr.LastResultsHashArray()
	if !ok {
		return fmt.Errorf("header %d carries no LastResultsHash although block %d had transactions", H, eventHeight)
	}
	if got, _ := gnolight.SimpleHash(leaves); got != root {
		return fmt.Errorf("block %d's re-encoded results hash to %x, header %d says %x (an event type this relayer encodes differently)", eventHeight, got, H, root)
	}
	proof, err := gnolight.Proof(leaves, li.index)
	if err != nil {
		return err
	}
	leaf := leaves[li.index]
	submitter := r.sol.Payer.PublicKey()
	var txs [][]solana.Instruction
	switch {
	case cs.InRing(blk.Hash):
		txs, err = client.ProofTxs(submitter, cs.Body.ChainID, nil, blk.Hash, nil, nil, msg.Raw, proof, leaf, 3, updateUnits)
	case hdr.ValidatorsHashArray() != cs.Body.SetHash:
		return fmt.Errorf("%w: header %d is signed by a set that is not the client's current one; a Reemit is asked", errNotYet, H)
	default:
		cm, _, err := r.light.CommitAt(ctx, H)
		if err != nil {
			return err
		}
		set, err := r.light.ValidatorsAt(ctx, H)
		if err != nil {
			return err
		}
		if sh, _ := set.Hash(); sh != hdr.ValidatorsHashArray() {
			return fmt.Errorf("the validators at %d do not hash to header %d's ValidatorsHash", H, H)
		}
		keys := make([][32]byte, len(set.Validators))
		for i, v := range set.Validators {
			keys[i] = v.PubKey
		}
		txs, err = client.ProofTxs(submitter, cs.Body.ChainID, hdr, [32]byte{}, cm, keys, msg.Raw, proof, leaf, 3, updateUnits)
	}
	if errors.Is(err, sol.ErrPacket) {
		// a proof whose StageInit exceeds one packet (a header with a merkle proof of more than seven aunts: a block
		// of more than 128 results, F6) is staged from a fresh block: a Reemit is asked, as for a leaf too big
		r.packetTooBig[nk] = true
		return fmt.Errorf("%w: %v; a Reemit is asked", errNotYet, err)
	}
	if err != nil {
		return err
	}
	stage := client.ProofStage(submitter, gnolight.MessageHash(msg.Raw))
	if err := driveStage(ctx, r.sol, client, stage, txs, 0); err != nil {
		return err
	}
	// the delivery itself
	recipient := solana.PublicKeyFromBytes(msg.Msg.Recipient[:])
	clientAccts := client.VerifyAccounts(submitter, msg.Raw)
	var ism solana.PublicKey
	var ismAccts, verifyAccts, handle solana.AccountMetaSlice
	switch kind {
	case kindRoute:
		if ismAccts, err = r.sol.RecipientISMAccounts(ctx, recipient); err != nil {
			return err
		}
		if ism, err = r.sol.RecipientISM(ctx, mailbox, recipient, ismAccts); err != nil {
			return err
		}
		if !ism.Equals(pausable.Program) {
			return fmt.Errorf("simulation failed: %s's ISM is %s, not the pausable ISM %s: refusing to deliver outside it", rt.Symbol, ism, pausable.Program)
		}
		verifyAccts = pausable.VerifyAccounts(client, clientAccts)
		if handle, err = r.sol.HandleAccounts(ctx, recipient, msg.Msg.Origin, msg.Msg.Sender, msg.Msg.Body); err != nil {
			return r.closeOnRefusal(ctx, client, stage, submitter, nk, err)
		}
	case kindDecision:
		ism = client.Program
		verifyAccts = clientAccts
		dk, _, _ := gnolight.DecisionPrefix(msg.Msg.Body)
		// the program's HandleAccountMetas answer, asked by simulation as a token's is (v27: the state, the
		// decision PDA and the system program, for every kind), checked against what this relayer expects; the
		// payer and a cancel's accounts follow it in handle's order (sol.Admin.HandleAccounts)
		metas, err := r.sol.HandleAccounts(ctx, recipient, msg.Msg.Origin, msg.Msg.Sender, msg.Msg.Body)
		if err != nil {
			return r.closeOnRefusal(ctx, client, stage, submitter, nk, err)
		}
		if !sol.SameMetas(metas, admin.HandleMetas(seq)) {
			return r.closeOnRefusal(ctx, client, stage, submitter, nk, fmt.Errorf("simulation failed: the admin program's HandleAccountMetas answers %d accounts that are not the state, decision %d's PDA and the system program: not the v27 program this relayer delivers to", len(metas), seq))
		}
		var pairs []sol.CancelPair
		if dk == sol.KindCancel {
			// the parameters are the bytes past the 9-byte prefix: the salt, then the form (v27 §13); the pairs
			// passed are as many as the packet carries (F19)
			fits := func(pairs []sol.CancelPair) bool {
				h := admin.HandleAccounts(metas, dk, submitter, pairs)
				ix := sol.ProcessIx(mailbox, submitter, submitter.Bytes(), msg.Raw, msg.ID, recipient, ism, ismAccts, verifyAccts, h)
				return sol.Fits(submitter, []solana.Instruction{sol.ComputeLimitIx(r.SolUnits), ix})
			}
			pairs, err = r.cancelPairs(ctx, admin, msg.Msg.Body[9:], fits)
			if err != nil {
				return err
			}
		}
		handle = admin.HandleAccounts(metas, dk, submitter, pairs)
	}
	ixP := sol.ProcessIx(mailbox, submitter, submitter.Bytes(), msg.Raw, msg.ID, recipient, ism, ismAccts, verifyAccts, handle)
	sig, err := r.sol.Send(ctx, []solana.Instruction{sol.ComputeLimitIx(r.SolUnits), ixP})
	if err != nil {
		return r.closeOnRefusal(ctx, client, stage, submitter, nk, err)
	}
	log.Printf("relayer: gno #%d %x delivered on solana (%s): %s", msg.Msg.Nonce, msg.ID[:6], kindName(kind, rt), sig)
	if _, err := r.sol.Send(ctx, []solana.Instruction{client.StageCloseIx(stage, submitter)}); err != nil {
		log.Printf("relayer: closing the proof stage of #%d: %v (the rent stays until it is closed)", msg.Msg.Nonce, err)
	}
	return nil
}

// closeOnRefusal closes a message's proof stage when the delivery failed for good (a program refused it: the
// next attempt builds a fresh plan and stage), and leaves it for a failure that may pass next time (a dead RPC).
func (r *Relayer) closeOnRefusal(ctx context.Context, client sol.LightClient, stage, submitter solana.PublicKey, nk string, err error) error {
	if !solDeterministic(err) {
		return err
	}
	if _, cerr := r.sol.Send(ctx, []solana.Instruction{client.StageCloseIx(stage, submitter)}); cerr != nil {
		log.Printf("relayer: gno #%s: the delivery was refused and closing its proof stage failed too: %v", nk, cerr)
	} else {
		log.Printf("relayer: gno #%s: the delivery was refused; its proof stage is closed and rebuilt at the next attempt", nk)
	}
	return err
}

func kindName(k msgKind, rt *Route) string {
	if k == kindDecision {
		return "a decision"
	}
	if rt != nil {
		return rt.Symbol
	}
	return "?"
}

// cancelRanges are the ranges a cancel names, read past the salt its parameters start with (v27 §13: the body is
// kind ‖ seq ‖ salt u64 ‖ form ‖ …, and the parameters are the bytes after the 9-byte prefix); nil when the
// program would spend it instead (no parse — a channel byte other than 0 among them, M2j), so there is nothing
// to close.
func cancelRanges(params []byte) []sol.CancelRange {
	p, err := sol.ParseParams(sol.KindCancel, params)
	if err != nil {
		return nil
	}
	return p.Cancel
}

// cancelPairs are the pending decisions a mailbox cancel names, with their deliverers, as many as the packet
// carries (F19); the rest stay cancelled in the state and are closed by CloseCancelled.
func (r *Relayer) cancelPairs(ctx context.Context, admin sol.Admin, params []byte, fits func([]sol.CancelPair) bool) ([]sol.CancelPair, error) {
	ranges := cancelRanges(params)
	if ranges == nil {
		return nil, nil
	}
	return CancelPairsFor(ctx, r.sol.RPC, admin, ranges, fits)
}

// CancelPairsFor are the pending decisions a cancel's ranges name, each with its deliverer,
// at most MaxCancel of them and as many as the transaction's packet carries (F19: seven pairs with distinct
// deliverers, twelve with one, exceed the 1,232-byte packet, and a cancel of a flood was refused by the RPC on
// every retry and never recorded — the program needs none of the pairs: a number not passed is stored
// cancelled and CloseCancelled closes it). fits says whether a transaction built with the pairs fits (nil
// bounds by count alone); pairs are dropped from the end until it does. A closed number, and a staged upgrade
// whose buffer return needs more accounts than a Handle carries, are passed over.
func CancelPairsFor(ctx context.Context, c solReader, admin sol.Admin, ranges []sol.CancelRange, fits func([]sol.CancelPair) bool) ([]sol.CancelPair, error) {
	st, err := readAdmin(ctx, c, admin, rpc.CommitmentConfirmed)
	if err != nil {
		return nil, err
	}
	var pairs []sol.CancelPair
	for _, rg := range ranges {
		to := rg.To
		if to > st.LastSeq {
			to = st.LastSeq
		}
		for n := rg.From; n <= to && len(pairs) < sol.MaxCancel; n++ {
			d, err := readDecision(ctx, c, admin, n, rpc.CommitmentConfirmed)
			if err != nil {
				return nil, err
			}
			if d == nil || (d.Kind == sol.KindUpgrade && d.RefundKey != nil) {
				continue
			}
			pairs = append(pairs, sol.CancelPair{Number: n, Deliverer: d.Deliverer})
		}
	}
	return fitPairs(pairs, fits), nil
}

// fitPairs drops pairs from the end until the transaction built with them fits the packet (F19).
func fitPairs(pairs []sol.CancelPair, fits func([]sol.CancelPair) bool) []sol.CancelPair {
	if fits == nil {
		return pairs
	}
	for len(pairs) > 0 && !fits(pairs) {
		pairs = pairs[:len(pairs)-1]
	}
	return pairs
}

// AdminFacts is what a member's tool reads of the chain (exported for cmd/hlrecover): the client's state, the
// live admin program's state, both objects' holds at now, the programs with the live admin program in the admin
// program's place (AQ6) and the line's text — all from one reader.
type AdminFacts struct {
	Client   *clientState
	Admin    *sol.AdminState
	Pausable *sol.Guardians
	Factor   int64
	Hold     *sol.Flag // the admin program's hold at now
	PHold    *sol.Flag // the pausable ISM's hold at now
	Programs sol.Programs
	Line     string // the retired predecessors when the line moved, "" otherwise
}

func ReadAdminFacts(ctx context.Context, c solReader, progs sol.Programs, now int64) (*AdminFacts, error) {
	cs, err := readClientState(ctx, c, progs.LightClient(), rpc.CommitmentConfirmed)
	if err != nil {
		return nil, err
	}
	chain, err := followSuccessors(ctx, c, progs.AdminProgram(), rpc.CommitmentConfirmed)
	if err != nil {
		return nil, err
	}
	progs = chain.Programs(progs)
	ad := chain.State
	_, pg, err := readPausable(ctx, c, progs.PausableISM(), rpc.CommitmentConfirmed)
	if err != nil {
		return nil, err
	}
	f := &AdminFacts{Client: cs, Admin: ad, Pausable: pg, Factor: int64(ad.ClockFactor), Programs: progs, Line: chain.Text()}
	if f.Factor == 0 {
		f.Factor = 1
	}
	grace := sol.UnpauseDelay(f.Factor)
	f.Hold, f.PHold = ad.Guardians.Hold(now, grace), pg.Hold(now, grace)
	return f, nil
}

// ChainID is the client's chain id, TrustedHash its trusted header's hash, LastAccepted its last accepted time.
func (f *AdminFacts) ChainID() string       { return f.Client.Body.ChainID }
func (f *AdminFacts) TrustedHash() [32]byte { return f.Client.Head.TrustedHash }
func (f *AdminFacts) LastAccepted() int64   { return f.Client.Head.LastAcceptedTime }

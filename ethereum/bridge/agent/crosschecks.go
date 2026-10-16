package agent

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The reads of what the realms cannot see, before a vote and before an apply (the conformance audit's E4-CF5): the
// Solana hlwatch's three gates (bridge/agent/enrol.go RefundGate, bridge/cmd/hlwatch maximumCheck and switchNotes),
// carried to Ethereum, read with "Ethereum" for "Solana" (§0, §5.2–§5.4, §6.5).
//
//   - The refund's gate is Ethereum's enrolment, not the realm's note (F27, AS2, AR1): a refund of a successor's imported
//     sends is safe only once the token's enrolled router for gno.land's domain is the refunding successor — before that
//     the predecessor is still the router, and a refunded send's message is deliverable by anyone's Reemit and a header
//     proof: minted twice, closed by the write-off alone. The realm reads r/hlgov's enrolment note only (the kind 6's
//     dispatch, 14 days before the refund, R37), never Ethereum, where a kind 6 executes 7 days after its delivery,
//     deferred by any lone flag on HlAdmin or a hold, cancelled by a kind 1. So the watch reads the token's `routers`
//     itself and the kind 6's state on HlAdmin, and alarms NOT ENROLLED ON ETHEREUM on a refund proposal, on an apply,
//     and on a queued refund every poll until it applies; and prints DELIVERED beside an id Ethereum's mailbox processed
//     already (a refund of it is the same double mint). The realms tell members to read this gate first
//     (NewApplyRefundRequest), and so does the executor (it never applies a refund: AT2).
//   - A route's per-message maximum raised above the gate's (§5.4: the kind 12 first): a send between the two is taken on
//     gno.land and parked on Ethereum for good (OverMax) until a kind 12 raises the gate's; read before the vote.
//   - r/hlgov's decision-recipient switch proposed or queued while HlAdmin's kind 9 is unexecuted (§6.5: the switch
//     follows a kind 9): executed first, the successor answers NotInherited and the predecessor hears nothing — GovDAO's
//     own channel deadlocked under a hold; and r/hlgov dispatching to a retired HlAdmin (every delivery answers
//     Retired) or to a successor that has not inherited (NotInherited).
//
// Each finding alarms under its own key (refund:gate:…, maximum:proposal:…, switch:…), never green while it stands, and
// is cleared once its proposal or decision is gone or the condition ends.

var (
	refundTextRE      = regexp.MustCompile(`refund (.+?) of sends imported from the predecessor .*?to their senders: ([0-9a-fA-Fx, ]+)`)
	applyRefundTextRE = regexp.MustCompile(`^apply refund decision (\d+) at once`)
	maximumTextRE     = regexp.MustCompile(`^set maximum to ([0-9]+(?:\.[0-9]+)?) [A-Z0-9]+ \(now `)
	recipientTextRE   = regexp.MustCompile(`set decision recipient to (0x[0-9a-fA-F]{40})`)
)

// crossKeys are the alarm prefixes of the cross-chain gates (in greenAlarms).
var crossKeys = []string{"refund:gate:", "maximum:proposal:", "switch:"}

// RouterView is a token's enrolled router for gno.land's domain as read on Ethereum, named among the token's line.
type RouterView struct {
	Router [32]byte
	Realm  string // the realm of the token's line it is, "" when none
}

// Text names the router.
func (rv RouterView) Text() string {
	if rv.Router == ([32]byte{}) {
		return "none (no router is enrolled for gno.land's domain)"
	}
	if rv.Realm != "" {
		return "0x" + hex.EncodeToString(rv.Router[:]) + " (" + rv.Realm + ")"
	}
	return "0x" + hex.EncodeToString(rv.Router[:]) + " (no realm of the token's line)"
}

// Is says whether the router is a realm.
func (rv RouterView) Is(realm string) bool {
	return rv.Router != ([32]byte{}) && rv.Router == gnoside.RealmKey(realm)
}

// Kind6State is where r/hlgov's latest kind 6 enrolling a realm as a token's router stands on HlAdmin.
type Kind6State struct {
	Number uint64 // r/hlgov's number, 0 when none was dispatched (or r/hlgov was not read)
	State  string
}

// kind6Back is how many of r/hlgov's numbers are walked back for the kind 6 that enrolled a route.
const kind6Back = 200

// RefundGate is the refund's verdict on Ethereum for a realm: ok when the token's enrolled router for gno.land's domain
// is the realm; the text says why not — the router found and the kind 6's state — or that it is.
func RefundGate(symbol, realm string, domain uint32, rv *RouterView, k6 Kind6State) (string, bool) {
	if rv == nil {
		return "NOT JUDGED ON ETHEREUM: the " + symbol + " token's enrolled router was not read (the refund's gate is the enrolment on Ethereum, F27); a refund now is judged on the note alone", false
	}
	if rv.Is(realm) {
		return fmt.Sprintf("ENROLLED ON ETHEREUM: the %s token's router for gno.land's domain %d is this realm (the kind 6 executed; r/hlgov's #%d)", symbol, domain, k6.Number), true
	}
	return fmt.Sprintf("NOT ENROLLED ON ETHEREUM: the %s token's router for gno.land's domain %d is %s, not this realm, and the kind 6 enrolling it is %s — a refund now lets a refunded sender, or anyone, deliver the send through the predecessor's router (Reemit and a header proof): minted twice, closed by the write-off alone; the refund waits on the router whatever the note says (F27, AS2, E4-CF5)", symbol, domain, rv.Text(), k6.State), false
}

// Kind6StateOf judges a dispatched kind 6's number against HlAdmin: not recorded yet (above its last number), pending,
// cancelled (a stored range), or recorded and gone (executed, or spent) — in which case the router tells which.
func Kind6StateOf(n uint64, admin *ethx.AdminState, pending []PendingEth) Kind6State {
	switch {
	case n == 0:
		return Kind6State{State: "absent: r/hlgov dispatched no kind 6 enrolling this realm as the token's router"}
	case admin == nil:
		return Kind6State{n, fmt.Sprintf("dispatched as r/hlgov's #%d; HlAdmin was not read", n)}
	case n > admin.LastSeq:
		return Kind6State{n, fmt.Sprintf("dispatched as r/hlgov's #%d, not yet recorded on HlAdmin (its last number is %d): the relayer delivers it, then 7 days pass on Ethereum's clock", n, admin.LastSeq)}
	}
	for _, d := range pending {
		if d.Number == n {
			when := d.When
			if when == "" {
				when = "matures at " + time.Unix(d.MaturesAt, 0).UTC().Format(time.RFC3339)
			}
			return Kind6State{n, fmt.Sprintf("pending on HlAdmin as #%d, %s", n, when)}
		}
	}
	if admin.IsCancelled(n) {
		return Kind6State{n, fmt.Sprintf("CANCELLED on HlAdmin (#%d is in a cancelled range): the note stands on gno.land, the enrolment never happened — propose the kind 6 again", n)}
	}
	return Kind6State{n, fmt.Sprintf("recorded on HlAdmin as #%d and gone (executed, or spent on its own checks) while the router is not this realm: spent, or a later kind 6 enrolled another — read HlAdmin's records", n)}
}

// maximumCheck is a route's per-message-maximum proposal read against the gate's live maximum for the token (§5.4,
// AS4): a raise above the gate's strands every send between the two on Ethereum (OverMax until a kind 12). amount is the
// proposal's figure in whole tokens as the builder writes it (six decimals of base units); gate the gate's maximum.
func maximumCheck(amount string, gate uint64) (string, bool) {
	whole, frac, _ := strings.Cut(amount, ".")
	n, err := strconv.ParseUint(whole, 10, 64)
	if err != nil || n > (1<<64-1)/1_000_000 {
		return "the maximum could not be read from the proposal (" + amount + ")", false
	}
	base := n * 1_000_000
	if frac != "" {
		frac = (frac + "000000")[:6]
		f, _ := strconv.ParseUint(frac, 10, 64)
		base += f
	}
	switch {
	case base > gate:
		return fmt.Sprintf("RAISE ABOVE THE ETHEREUM MAXIMUM: %d base units against the gate's %d — sends between would strand on Ethereum (OverMax, parked for good until a kind 12 raises the gate's; §5.4: Ethereum first, gno.land after): vote the kind 12 first", base, gate), true
	case base == gate:
		return fmt.Sprintf("%d base units, at the gate's %d: no send strands", base, gate), false
	}
	return fmt.Sprintf("%d base units, under the gate's %d: no send strands", base, gate), false
}

// crossChecks reads, for every proposal and queued decision of the bridge's realms on gno.land, what the realm cannot
// see on Ethereum (the gates above), appends the reading to its text on the page, and alarms each finding while it
// stands.
func (w *Watch) crossChecks(ctx context.Context, v *WatchView, snap *ethSnapshot, o *ethObjects) {
	m := w.m
	raised := map[string]bool{}
	alarm := func(key, text string) {
		raised[key] = true
		m.Alarm.Alarm(key, text)
	}
	// a finding whose reading did not run this time stands as it was (the page says what was not read)
	var keep []string
	if v.DAOErr != "" || len(v.UnreadProposals) > 0 {
		keep = append(keep, "refund:gate:proposal:", "refund:gate:apply:", "maximum:proposal:", "switch:proposal:")
	}
	if v.Gate == nil {
		keep = append(keep, "maximum:proposal:")
	}
	if len(v.UnreadLedgers) > 0 {
		keep = append(keep, "refund:gate:queued:", "switch:queued:")
	}
	if o.admins == nil || v.Gov == nil {
		keep = append(keep, "switch:")
	}
	defer func() {
		for k := range m.Alarm.Standing() {
			kept := false
			for _, p := range keep {
				kept = kept || strings.HasPrefix(k, p)
			}
			for _, p := range crossKeys {
				if strings.HasPrefix(k, p) && !raised[k] && !kept {
					m.Alarm.Clear(k)
				}
			}
		}
	}()
	mb := v.Mailbox
	routers := map[string]*RouterView{}
	routerOf := func(r *Route) *RouterView {
		if rv, ok := routers[r.Symbol]; ok {
			return rv
		}
		var rv *RouterView
		if r.Token != "" {
			if b, err := ethx.RouterFor(ctx, m.pair, r.TokenAddress(), mb.Domain, snap.Pin); err == nil {
				rv = &RouterView{Router: b}
				for _, p := range w.lineOf(r) {
					if gnoside.RealmKey(p) == b {
						rv.Realm = p
					}
				}
			}
		}
		routers[r.Symbol] = rv
		return rv
	}
	type gate struct {
		text string
		ok   bool
	}
	gates := map[string]gate{} // by realm: r/hlgov's walk for the kind 6 made once a view
	gateFor := func(realm string, r *Route) (string, bool) {
		if g, ok := gates[realm]; ok {
			return g.text, g.ok
		}
		text, ok := RefundGate(r.Symbol, realm, mb.Domain, routerOf(r), w.kind6State(ctx, v, realm, r))
		gates[realm] = gate{text, ok}
		return text, ok
	}
	for i := range v.Proposals {
		p := &v.Proposals[i]
		r := w.routeOf(p.Realm)
		if r != nil {
			// a route's per-message maximum against the gate's (§5.4, AS4)
			if mm := maximumTextRE.FindStringSubmatch(p.Text); mm != nil {
				if tok, ok := TokenIndex(r.Symbol); ok && v.Gate != nil {
					text, finding := maximumCheck(mm[1], v.Gate.Limits[tok].MaxPerMessage)
					p.Text += " — " + text
					if finding {
						alarm("maximum:proposal:"+strconv.FormatInt(p.ID, 10), fmt.Sprintf("GovDAO #%d, %s's per-message maximum: %s", p.ID, r.Symbol, text))
					}
				}
			}
			// GovDAO's apply of a queued refund (AT2), told apart by its title first, then its description's head (its
			// description carries the refund decision's own text)
			if isApplyRefund(p) {
				id := int64(0)
				if mm := applyRefundTextRE.FindStringSubmatch(p.Text); mm != nil {
					id, _ = strconv.ParseInt(mm[1], 10, 64)
				}
				gate, ok := gateFor(p.Realm, r)
				text, finding := w.applyRefundCheck(ctx, v, snap, p.Realm, r.Symbol, id, gate, ok)
				p.Text += " — " + text
				if finding {
					alarm("refund:gate:apply:"+strconv.FormatInt(p.ID, 10), fmt.Sprintf("GovDAO #%d, the apply of %s's refund decision #%d (%s): %s — do not propose or vote the apply while this stands", p.ID, p.Realm, id, r.Symbol, text))
				}
			} else if mm := refundTextRE.FindStringSubmatch(p.Text); mm != nil {
				gate, ok := gateFor(p.Realm, r)
				text, finding := w.refundCheck(ctx, v, snap, p.Realm, r.Symbol, strings.Split(mm[2], ","), gate, ok)
				p.Text += " — " + text
				if finding {
					alarm("refund:gate:proposal:"+strconv.FormatInt(p.ID, 10), fmt.Sprintf("GovDAO #%d, a refund on %s (%s): %s", p.ID, p.Realm, r.Symbol, text))
				}
			}
		}
	}
	// a queued refund, every poll until it applies (F27, AS2)
	for i := range v.Pending {
		d := &v.Pending[i]
		if d.Kind != "refund" {
			continue
		}
		r := w.routeOf(d.Realm)
		if r == nil {
			continue
		}
		var ids []string
		if mm := refundTextRE.FindStringSubmatch(d.Text); mm != nil {
			ids = strings.Split(mm[2], ",")
		}
		gate, ok := gateFor(d.Realm, r)
		text, finding := w.refundCheck(ctx, v, snap, d.Realm, r.Symbol, ids, gate, ok)
		d.Text += " — " + text
		if finding {
			alarm("refund:gate:queued:"+d.Realm+"#"+strconv.FormatInt(d.ID, 10), fmt.Sprintf("%s decision #%d, a queued refund (%s), matures at %s, awaiting GovDAO's apply: %s — do not propose or vote the apply while this stands (cancel the decision if the kind 6 was cancelled)", d.Realm, d.ID, r.Symbol, time.Unix(d.Due, 0).UTC().Format(time.RFC3339), text))
		}
	}
	// r/hlgov's recipient switch against HlAdmin's line (§6.5, AP3)
	if o.admins != nil && v.Gov != nil {
		for _, n := range w.switchNotes(ctx, v, o.admins, snap) {
			alarm(n.key, n.text)
		}
	}
}

// lineOf is every realm of a route's line the router may name: the configured route, its line of migrations as read
// this poll, and the config's successor and predecessor.
func (w *Watch) lineOf(r *Route) []string {
	out := []string{r.Gno}
	if mig := w.m.migs[r.Symbol]; mig != nil {
		out = append(out, mig.Paths()...)
	}
	for _, p := range []string{r.Successor, r.Predecessor} {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// routeOf is the configured route a realm belongs to: the route itself, or a realm of its line; nil for none.
func (w *Watch) routeOf(realm string) *Route {
	for i := range w.m.Cfg.Routes {
		r := &w.m.Cfg.Routes[i]
		for _, p := range w.lineOf(r) {
			if p == realm {
				return r
			}
		}
	}
	return nil
}

// kind6State is where r/hlgov's latest kind 6 enrolling a realm as the token's router stands on HlAdmin: r/hlgov's
// numbers walked back (kind6Back) for a kind 6 naming the token, gno.land's domain and the realm.
func (w *Watch) kind6State(ctx context.Context, v *WatchView, realm string, r *Route) Kind6State {
	m := w.m
	if m.Cfg.Gno.Gov == "" || v.Gov == nil {
		return Kind6State{State: "not read (r/hlgov is not in this view)"}
	}
	tok, ok := TokenIndex(r.Symbol)
	if !ok {
		return Kind6State{State: "not read (" + r.Symbol + " is no token of the gate's)"}
	}
	key := gnoside.RealmKey(realm)
	for n := v.Gov.Last; n >= 1 && n+kind6Back > v.Gov.Last; n-- {
		gd, err := gnoside.GovDecisionAt(ctx, m.own, m.Cfg.Gno.Gov, n, v.Height)
		if err != nil {
			return Kind6State{State: "not read (r/hlgov's decision " + strconv.FormatUint(n, 10) + ": " + err.Error() + ")"}
		}
		if gd == nil {
			continue
		}
		body, err := hex.DecodeString(strings.TrimPrefix(gd.Body, "0x"))
		if err != nil || len(body) < 9+37 || body[0] != 6 {
			continue
		}
		p := body[9:]
		if int(p[0]) == tok && binary.BigEndian.Uint32(p[1:5]) == v.Mailbox.Domain && [32]byte(p[5:37]) == key {
			return Kind6StateOf(n, v.Admin, v.Decisions)
		}
	}
	return Kind6StateOf(0, v.Admin, v.Decisions)
}

// refundCheck is a successor's refund — a proposal, or a queued decision — read against what the realm cannot see:
// first the gate on Ethereum (RefundGate), then hlmailbox's note of the kind 6 that enrolled the successor (the realm's
// 14-day wait, R37: missing or younger, the realm refuses until then), and beside each id DELIVERED when Ethereum's
// mailbox processed it already. The second answer says whether it is a finding.
func (w *Watch) refundCheck(ctx context.Context, v *WatchView, snap *ethSnapshot, realm, symbol string, ids []string, gate string, gateOK bool) (string, bool) {
	m, mb := w.m, v.Mailbox
	delay := 7 * 24 * time.Hour
	if mb.Clocks.Delay > 0 {
		delay = time.Duration(mb.Clocks.Delay) * time.Second
	}
	wait := 2 * delay // the realm's 14-day wait on the note (R37)
	finding := !gateOK
	parts := []string{gate}
	rf, has := mb.Routes[realm]
	height, at := int64(0), int64(0)
	if has {
		height, at = rf.Enrollment()
	}
	switch {
	case !has || (height == 0 && at == 0):
		finding = true
		parts = append(parts, "NO NOTE: no kind 6 enrolling "+realm+" as the "+symbol+" router was dispatched from r/hlgov (hlmailbox holds no enrolment note for it): the realm refuses the refund until that note is 14 days old (R37) — vote kind 6 first")
	default:
		known := at > 0
		if !known && height > 0 {
			if b, err := m.own.Block(ctx, height); err == nil {
				at, known = b.Time.Unix(), true
			}
		}
		noted := time.Unix(at, 0)
		switch {
		case !known:
			finding = true
			parts = append(parts, fmt.Sprintf("the note is at height %d, whose time could not be read: the realm refuses the refund until the note is 14 days old (R37)", height))
		case v.BlockTime.Sub(noted) < wait:
			finding = true
			parts = append(parts, fmt.Sprintf("THE NOTE IS %s OLD, UNDER THE REALM'S 14-DAY WAIT (height %d at %s; R37): the realm refuses the refund until %s — a vote now lands it no earlier", v.BlockTime.Sub(noted).Round(time.Minute), height, noted.UTC().Format(time.RFC3339), noted.Add(wait).UTC().Format(time.RFC3339)))
		default:
			parts = append(parts, fmt.Sprintf("the note is %s old (height %d at %s), past the realm's 14-day wait (R37): the realm takes the refund", v.BlockTime.Sub(noted).Round(time.Hour), height, noted.UTC().Format(time.RFC3339)))
		}
	}
	var clean []string
	var keys [][32]byte
	for _, id := range ids {
		id = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "0x"))
		b, err := hex.DecodeString(id)
		if err != nil || len(b) != 32 {
			continue
		}
		clean = append(clean, id)
		keys = append(keys, [32]byte(b))
	}
	if len(clean) > 0 {
		delivered, err := ethx.DeliveredMany(ctx, m.pair, Address(m.Cfg.Eth.Mailbox), keys, snap.Pin)
		var lines []string
		for i, id := range clean {
			switch {
			case err != nil:
				lines = append(lines, id+" (delivery not judged: Ethereum's mailbox was not read: "+ethReadText(err)+")")
			case delivered[i]:
				finding = true
				lines = append(lines, id+" DELIVERED (Ethereum processed it already: a refund would be a double mint, cured only by the write-off builder, AR1)")
			default:
				lines = append(lines, id+" undelivered")
			}
		}
		if err != nil {
			finding = true
		}
		parts = append(parts, "ids: "+strings.Join(lines, "; "))
	}
	return strings.Join(parts, "; "), finding
}

// isApplyRefund tells an apply-refund proposal (AT2) by its title first — "<realm>: apply refund", the builder's — then
// by its description's head; a refund proposal's own text sits inside the apply's description.
func isApplyRefund(p *gnoside.Proposal) bool {
	return strings.HasSuffix(strings.TrimSpace(p.Title), ": apply refund") || applyRefundTextRE.MatchString(p.Text)
}

// applyRefundCheck is GovDAO's apply of a queued refund read against the decision it names on its realm: the decision's
// ids through refundCheck's gate, with its maturity; NO QUEUED REFUND when the realm holds none pending under that id,
// NOT A REFUND when the id is another kind's — each what the closure would refuse.
func (w *Watch) applyRefundCheck(ctx context.Context, v *WatchView, snap *ethSnapshot, realm, symbol string, id int64, gate string, gateOK bool) (string, bool) {
	for _, d := range v.Pending {
		if d.Realm != realm || d.ID != id {
			continue
		}
		if d.Kind != "refund" {
			return fmt.Sprintf("DECISION #%d ON %s IS A %s, NOT A REFUND: the closure would refuse the apply", id, realm, d.Kind), true
		}
		var ids []string
		if mm := refundTextRE.FindStringSubmatch(d.Text); mm != nil {
			ids = strings.Split(mm[2], ",")
		}
		text, finding := w.refundCheck(ctx, v, snap, realm, symbol, ids, gate, gateOK)
		when := "mature"
		if left := time.Unix(d.Due, 0).Sub(v.BlockTime); left > 0 {
			when = fmt.Sprintf("not mature until %s (in %s; the closure executes it only then, and a passed vote retries)", time.Unix(d.Due, 0).UTC().Format(time.RFC3339), left.Round(time.Minute))
		}
		return fmt.Sprintf("applies %s's queued refund #%d, %s: %s", realm, id, when, text), finding
	}
	return fmt.Sprintf("NO QUEUED REFUND #%d ON %s (applied, cancelled or never made): the closure would refuse the apply", id, realm), true
}

// switchNote is one finding on r/hlgov's recipient switch, keyed for the alarms.
type switchNote struct {
	key, text string
}

// switchNotes are the recipient-switch findings (AP3, §6.5): a `set decision recipient` proposed or queued while
// HlAdmin's kind 9 to it is unexecuted — executed first, the successor answers NotInherited and the predecessor hears
// nothing until the hold ends or the switch is undone, so the switch is voted only once this page shows the kind 9
// executed; r/hlgov dispatching to a retired HlAdmin (every delivery answers Retired, the message dead there until the
// switch lands and GovDAO re-dispatches); and r/hlgov's recipient a successor that has not inherited (NotInherited until
// its predecessor's kind 9 lands). HlAdmin's line is the one read from the chain (followAdmins).
func (w *Watch) switchNotes(ctx context.Context, v *WatchView, line *adminLine, snap *ethSnapshot) []switchNote {
	var out []switchNote
	unexecuted := func(target common.Address) string {
		switch {
		case target == line.Live:
			return "" // the kind 9 that made it the live HlAdmin executed (or it is the HlAdmin of the launch)
		case len(line.Retired) > 0:
			return "HlAdmin's line ends at " + line.Live.Hex() + ", not this one"
		}
		return "HlAdmin's kind 9 is UNEXECUTED (no successor recorded on " + line.Live.Hex() + ")"
	}
	warn := func(key, what string, target common.Address) {
		if why := unexecuted(target); why != "" {
			out = append(out, switchNote{key, fmt.Sprintf("%s to %s while %s: executed first, it deadlocks GovDAO's own channel — the successor answers NotInherited and the predecessor hears nothing until the hold ends or the switch is undone (§6.5, AP3); vote the switch only once this page shows the kind 9 executed", what, target.Hex(), why)})
		}
	}
	gov := w.m.Cfg.Gno.Gov
	for _, p := range v.Proposals {
		if p.Realm != gov {
			continue
		}
		if mm := recipientTextRE.FindStringSubmatch(p.Text); mm != nil {
			warn("switch:proposal:"+strconv.FormatInt(p.ID, 10), fmt.Sprintf("the recipient switch proposed (GovDAO #%d)", p.ID), common.HexToAddress(mm[1]))
		}
	}
	for _, d := range v.Pending {
		if d.Realm != gov {
			continue
		}
		if mm := recipientTextRE.FindStringSubmatch(d.Text); mm != nil {
			warn("switch:queued:"+strconv.FormatInt(d.ID, 10), fmt.Sprintf("the recipient switch queued (r/hlgov decision #%d, matures at %s)", d.ID, time.Unix(d.Due, 0).UTC().Format(time.RFC3339)), common.HexToAddress(mm[1]))
		}
	}
	if v.Gov.Recipient == "" {
		return out
	}
	recipient := common.HexToAddress(v.Gov.Recipient)
	if recipient == line.Live {
		return out
	}
	st, err := ethx.ReadAdmin(ctx, w.m.pair, recipient, snap.Pin)
	switch {
	case err != nil:
		out = append(out, switchNote{"switch:recipient", "r/hlgov dispatches to " + recipient.Hex() + ", which could not be read as an HlAdmin (" + ethReadText(err) + "); the live HlAdmin of the configured line is " + line.Live.Hex() + ": its deliveries may reach nothing that executes"})
	case st.Retired():
		out = append(out, switchNote{"switch:retired", "RETIRED: HlAdmin " + recipient.Hex() + " handed every authority to " + st.Successor.Hex() + " (kind 9 executed; the live one is " + line.Live.Hex() + ") and r/hlgov still dispatches to it: every delivery answers Retired, the message dead there — set decision recipient to the live HlAdmin, then re-dispatch each decision (§6.5, AP3)"})
	case st.Predecessor != (common.Address{}) && !st.Inherited:
		out = append(out, switchNote{"switch:not-inherited", "NOT INHERITED: r/hlgov dispatches to " + recipient.Hex() + ", a successor that has not inherited: every delivery answers NotInherited until its predecessor's kind 9 executes (AP3)"})
	default:
		out = append(out, switchNote{"switch:recipient", "r/hlgov dispatches to " + recipient.Hex() + ", an HlAdmin that is not the live one of the configured line (" + line.Live.Hex() + "): read eth.admin in the config and r/hlgov's recipient"})
	}
	return out
}

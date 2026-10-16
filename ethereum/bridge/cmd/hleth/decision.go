package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// The members' commands on HlAdmin (the Solana tools' hlrecover, PLAN.md §5.8, §6.5): `decision status` reads the
// numbers, the pending decisions and when each matures, the holds and the flags; `decision body` writes the body
// r/hlgov's builder dispatches for a kind, to compare a proposal's bytes; `decision execute` executes a matured
// decision from any funded key, so no decision ever waits on the operator's relayer. Nothing here records, stages
// or settles anything: HlAdmin acts on r/hlgov's proven messages alone.

// The kinds of §13 (r/hlgov's KindCancel … KindMintLimits; HlAdmin's _knownKind: 1–9 and 12, kind 4 reserved and
// spent at execution, 10 and 11 recorded and spent at recording).
const (
	kindCancel     = 1
	kindUnpause    = 2
	kindTokenISM   = 3
	kindReserved   = 4
	kindGuardians  = 5
	kindRouter     = 6
	kindFee        = 7
	kindUpgrade    = 8
	kindSuccessor  = 9
	kindMintLimits = 12

	maxBody   = 512 // HlAdmin spends a longer body at recording; r/hlgov refuses one when made (MaxDecisionBody)
	maxCancel = 16  // the most numbers one list cancel names
	maxSeats  = 4   // the most seat changes one kind 5 carries
	maxCall   = maxBody - 9 - 96
)

// decisionBody is kind u8 ‖ seq u64 BE ‖ params (§13).
func decisionBody(kind uint8, seq uint64, params []byte) []byte {
	b := append([]byte{kind}, binary.BigEndian.AppendUint64(nil, seq)...)
	return append(b, params...)
}

func pad32(a common.Address) []byte { return common.LeftPadBytes(a.Bytes(), 32) }

// cancelListParams is kind 1's form 0: salt u64 ‖ 0 ‖ count u8 ‖ (channel 0 ‖ number u64)…
func cancelListParams(salt uint64, numbers []uint64) ([]byte, error) {
	if len(numbers) == 0 || len(numbers) > maxCancel {
		return nil, fmt.Errorf("a list cancel names 1 to %d numbers", maxCancel)
	}
	seen := map[uint64]bool{}
	p := binary.BigEndian.AppendUint64(nil, salt)
	p = append(p, 0, byte(len(numbers)))
	for _, n := range numbers {
		if n == 0 || seen[n] {
			return nil, fmt.Errorf("number %d is zero or named twice", n)
		}
		seen[n] = true
		p = append(p, 0)
		p = binary.BigEndian.AppendUint64(p, n)
	}
	return p, nil
}

// cancelRangeParams is kind 1's form 1: salt u64 ‖ 1 ‖ channel 0 ‖ from u64 ‖ to u64.
func cancelRangeParams(salt, from, to uint64) ([]byte, error) {
	if from == 0 || to < from {
		return nil, errors.New("a range runs from a lower number to a higher (or the same) one")
	}
	p := binary.BigEndian.AppendUint64(nil, salt)
	p = append(p, 1, 0)
	p = binary.BigEndian.AppendUint64(p, from)
	return binary.BigEndian.AppendUint64(p, to), nil
}

// seatKey is one kind-5 change.
type seatKey struct {
	Seat uint8
	Key  common.Address
}

// guardiansParams is kind 5: count u8 ‖ (seat u8 ‖ key 32)… — seats 1–4 guardians (two at most), 5–6 watch slots.
func guardiansParams(pairs []seatKey) ([]byte, error) {
	if len(pairs) == 0 || len(pairs) > maxSeats {
		return nil, fmt.Errorf("a kind 5 carries 1 to %d seat changes", maxSeats)
	}
	g := 0
	seen := map[uint8]bool{}
	keys := map[common.Address]bool{}
	p := []byte{byte(len(pairs))}
	for _, s := range pairs {
		switch {
		case s.Seat < 1 || s.Seat > 6:
			return nil, fmt.Errorf("seat %d: seats are 1–4 (guardians) and 5–6 (watch slots)", s.Seat)
		case seen[s.Seat]:
			return nil, fmt.Errorf("seat %d named twice", s.Seat)
		case s.Key == (common.Address{}):
			return nil, fmt.Errorf("seat %d: the zero address", s.Seat)
		case keys[s.Key]:
			return nil, fmt.Errorf("%s named twice", s.Key.Hex())
		}
		seen[s.Seat], keys[s.Key] = true, true
		if s.Seat <= 4 {
			g++
		}
		p = append(p, s.Seat)
		p = append(p, pad32(s.Key)...)
	}
	if g > 2 {
		return nil, errors.New("two guardian seats at most per decision")
	}
	return p, nil
}

func tokenISMParams(token uint8, ism common.Address) []byte {
	return append([]byte{token}, pad32(ism)...)
}

func routerParams(token uint8, domain uint32, router [32]byte) []byte {
	p := append([]byte{token}, binary.BigEndian.AppendUint32(nil, domain)...)
	return append(p, router[:]...)
}

func feeParams(fee uint64, beneficiary common.Address) []byte {
	return append(binary.BigEndian.AppendUint64(nil, fee), pad32(beneficiary)...)
}

func upgradeParams(proxy, impl common.Address, codehash [32]byte, call []byte) ([]byte, error) {
	if len(call) > maxCall {
		return nil, fmt.Errorf("the call is %d bytes; at most %d fit in the %d-byte body", len(call), maxCall, maxBody)
	}
	p := append(pad32(proxy), pad32(impl)...)
	p = append(p, codehash[:]...)
	return append(p, call...), nil
}

func successorParams(succ common.Address, codehash [32]byte) []byte {
	return append(pad32(succ), codehash[:]...)
}

func mintLimitsParams(token uint8, max, dayCap uint64) []byte {
	p := append([]byte{token}, binary.BigEndian.AppendUint64(nil, max)...)
	return binary.BigEndian.AppendUint64(p, dayCap)
}

// addrAt reads a 32-byte left-padded address; false when its top 12 bytes are not zero (HlAdminExec's _addr).
func addrAt(p []byte, at int) (common.Address, bool) {
	if len(p) < at+32 || !isZero(p[at:at+12]) {
		return common.Address{}, false
	}
	return common.BytesToAddress(p[at+12 : at+32]), true
}

// describeParams says what a kind's parameters do, with HlAdmin's own checks (HlAdmin._parseCancel and
// HlAdminExec.run): an error is the reason HlAdmin spends the decision ("params").
func describeParams(kind uint8, p []byte) (string, error) {
	tok := func(t byte) (string, error) {
		switch t {
		case 0:
			return "GNOT (token 0)", nil
		case 1:
			return "BUBBLE (token 1)", nil
		}
		return "", fmt.Errorf("token %d: 0 (GNOT) or 1 (BUBBLE)", t)
	}
	switch kind {
	case kindCancel:
		if len(p) < 10 {
			return "", errors.New("a cancel shorter than its salt and form")
		}
		salt := binary.BigEndian.Uint64(p[:8])
		switch p[8] {
		case 0:
			n := int(p[9])
			if n == 0 || n > maxCancel || len(p) != 10+9*n {
				return "", fmt.Errorf("a list of %d numbers in %d bytes", n, len(p))
			}
			var ns []string
			for i := 0; i < n; i++ {
				at := 10 + 9*i
				if p[at] != 0 {
					return "", fmt.Errorf("channel %d: the one channel is 0 (the mailbox)", p[at])
				}
				ns = append(ns, strconv.FormatUint(binary.BigEndian.Uint64(p[at+1:at+9]), 10))
			}
			return fmt.Sprintf("cancel %s (salt %016x)", strings.Join(ns, ", "), salt), nil
		case 1:
			if len(p) != 26 || p[9] != 0 {
				return "", errors.New("a range is channel 0 ‖ from ‖ to, 26 bytes with the salt")
			}
			from, to := binary.BigEndian.Uint64(p[10:18]), binary.BigEndian.Uint64(p[18:26])
			if from > to {
				return "", fmt.Errorf("the range %d–%d runs backwards", from, to)
			}
			return fmt.Sprintf("cancel the range %d–%d (salt %016x)", from, to, salt), nil
		}
		return "", fmt.Errorf("form %d: 0 a list, 1 a range", p[8])
	case kindUnpause:
		if len(p) != 1 || p[0] > 1 {
			return "", errors.New("an unpause names its object: 0 the gate, 1 HlAdmin")
		}
		if p[0] == 0 {
			return "unpause the gate (clears the flags set before its delivery, 24 hours after it, unless a hold stands)", nil
		}
		return "unpause HlAdmin (clears the flags set before its delivery, 24 hours after it, unless a hold stands)", nil
	case kindTokenISM:
		if len(p) != 33 {
			return "", fmt.Errorf("%d bytes: token u8 ‖ ism 32", len(p))
		}
		t, err := tok(p[0])
		if err != nil {
			return "", err
		}
		ism, ok := addrAt(p, 1)
		if !ok {
			return "", errors.New("the ISM is no address (its top 12 bytes are not zero)")
		}
		return fmt.Sprintf("set %s's ISM to %s (HlAdmin takes only the gate or zero)", t, ism.Hex()), nil
	case kindReserved:
		return "reserved (kind 4): recorded, deferred as the 7-day group, and spent at execution", nil
	case kindGuardians:
		if len(p) == 0 || p[0] == 0 || p[0] > maxSeats || len(p) != 1+33*int(p[0]) {
			return "", errors.New("count u8 (1–4) ‖ (seat u8 ‖ key 32)…")
		}
		var parts []string
		for i := 0; i < int(p[0]); i++ {
			at := 1 + 33*i
			k, ok := addrAt(p, at+1)
			if !ok {
				return "", fmt.Errorf("seat %d's key is no address", p[at])
			}
			what := "guardian seat"
			if p[at] > 4 {
				what = "watch slot"
			}
			parts = append(parts, fmt.Sprintf("%s %d to %s", what, p[at], k.Hex()))
		}
		return "set " + strings.Join(parts, ", ") + " (spent when it names a seat's current key)", nil
	case kindRouter:
		if len(p) != 37 {
			return "", fmt.Errorf("%d bytes: token u8 ‖ domain u32 ‖ router 32", len(p))
		}
		t, err := tok(p[0])
		if err != nil {
			return "", err
		}
		domain := binary.BigEndian.Uint32(p[1:5])
		if isZero(p[5:37]) {
			return fmt.Sprintf("unenroll %s's router for domain %d", t, domain), nil
		}
		return fmt.Sprintf("enroll %s's router for domain %d: 0x%x", t, domain, p[5:37]), nil
	case kindFee:
		if len(p) != 40 {
			return "", fmt.Errorf("%d bytes: fee u64 ‖ beneficiary 32", len(p))
		}
		b, ok := addrAt(p, 8)
		fee := binary.BigEndian.Uint64(p[:8])
		if !ok || b == (common.Address{}) || fee > defaultMaxProtocolFee.Uint64() {
			return "", errors.New("the fee is at most 0.001 ETH and the beneficiary a non-zero address")
		}
		return fmt.Sprintf("set the protocol fee to %d wei for %s", fee, b.Hex()), nil
	case kindUpgrade:
		if len(p) < 96 {
			return "", errors.New("proxy 32 ‖ implementation 32 ‖ codehash 32 ‖ call")
		}
		proxy, ok1 := addrAt(p, 0)
		impl, ok2 := addrAt(p, 32)
		if !ok1 || !ok2 {
			return "", errors.New("the proxy or the implementation is no address")
		}
		call := "no call (upgrade)"
		if len(p) > 96 {
			call = fmt.Sprintf("then call 0x%x (%d bytes, upgradeAndCall)", p[96:], len(p)-96)
		}
		return fmt.Sprintf("upgrade the proxy %s to %s with code hash 0x%x, %s", proxy.Hex(), impl.Hex(), p[64:96], call), nil
	case kindSuccessor:
		if len(p) != 64 {
			return "", fmt.Errorf("%d bytes: successor 32 ‖ codehash 32", len(p))
		}
		s, ok := addrAt(p, 0)
		if !ok {
			return "", errors.New("the successor is no address")
		}
		return fmt.Sprintf("transfer every authority to %s (code hash 0x%x)", s.Hex(), p[32:64]), nil
	case kindMintLimits:
		if len(p) != 17 || p[0] >= 2 {
			return "", errors.New("token u8 (0 or 1) ‖ maximum u64 ‖ day cap u64")
		}
		t, _ := tok(p[0])
		return fmt.Sprintf("set %s's mint limits on the gate: %d a message, %d a day (base units)", t, binary.BigEndian.Uint64(p[1:9]), binary.BigEndian.Uint64(p[9:17])), nil
	}
	return "", fmt.Errorf("kind %d is no kind of §13 (HlAdmin records it and spends it at once)", kind)
}

// --------------------------------------------------------------------------------------------- maturity --

// clocks are HlAdmin's delays at its clock factor (HlAdmin._delay, HlAdminStorage.day(), _replacementFloor).
type clocks struct{ day, week, day21 int64 }

func clocksOf(factor uint64) clocks {
	if factor == 0 {
		factor = 1
	}
	f := int64(factor)
	return clocks{day: 86_400 / f, week: 7 * 86_400 / f, day21: 21 * 86_400 / f}
}

// sevenDay is HlAdmin's _sevenDay: kinds 3–9 and 12.
func sevenDay(kind uint8) bool { return (kind >= 3 && kind <= 9) || kind == kindMintLimits }

// Maturity is when a pending decision can execute, as HlAdmin.execute judges it at a time.
type Maturity struct {
	At       int64  // the time it matures (0: never by itself, deferred)
	Ready    bool   // execute would act now (run it, or close it cancelled)
	Deferred string // what holds it beyond its clock, "" when nothing
}

// maturityOf mirrors HlAdmin.execute: a decision inside a cancelled range closes at its next execute; an unpause
// matures 24 hours after delivery (and is spent under a hold on its object); kind 5 matures 7 days after delivery
// through any lone flag, and under a hold no sooner than the 21st day of the earliest hold overlapping its pending
// life; every other kind of the 7-day group matures 7 days after delivery and is deferred by any flag on HlAdmin
// and by a hold.
func maturityOf(d *ethx.Decision, st *ethx.AdminState, gateHold ethx.Flag, now int64) Maturity {
	c := clocksOf(st.ClockFactor)
	if st.IsCancelled(d.Number) {
		return Maturity{At: int64(d.DeliveredTime), Ready: true, Deferred: "cancelled by a range: its next execute closes it"}
	}
	delivered := int64(d.DeliveredTime)
	hold := st.Hold
	switch {
	case d.Kind == kindUnpause:
		m := Maturity{At: delivered + c.day}
		obj := "HlAdmin"
		held := hold.Set()
		if len(d.Params) == 1 && d.Params[0] == 0 {
			obj, held = "the gate", gateHold.Set()
		}
		if held {
			m.Deferred = "a hold stands on " + obj + " now: executed under it, the unpause is spent without effect"
		}
		m.Ready = now >= m.At
		return m
	case d.Kind == kindGuardians:
		m := Maturity{At: delivered + c.week}
		if hold.Set() {
			floor := int64(hold.Time)
			for _, h := range st.Holds {
				if int64(h.End) >= delivered && int64(h.Start) < floor {
					floor = int64(h.Start)
				}
			}
			floor += c.day21
			if floor > m.At {
				m.At = floor
				m.Deferred = fmt.Sprintf("a hold stands: no sooner than the 21st day of the earliest hold overlapping its pending life (%s)", time.Unix(floor, 0).UTC().Format(time.RFC3339))
			}
		}
		m.Ready = now >= m.At
		return m
	case sevenDay(d.Kind):
		m := Maturity{At: delivered + c.week}
		switch {
		case hold.Set():
			m.Deferred = "deferred by the hold on HlAdmin since " + time.Unix(int64(hold.Time), 0).UTC().Format(time.RFC3339) + ", until it ends"
		case st.AnyFlag():
			m.Deferred = "deferred by a flag on HlAdmin, until it is cleared"
		}
		m.Ready = now >= m.At && m.Deferred == ""
		return m
	}
	return Maturity{Deferred: fmt.Sprintf("kind %d never pends", d.Kind)}
}

// ------------------------------------------------------------------------------------------ the commands --

// adminFacts are the live HlAdmin's and the gate's states and the pending decisions at one block, with the kind 8 and
// 9 targets that have no code yet there (each such decision pends: HlAdmin refuses its execute NoCodeYet).
type adminFacts struct {
	Line    []common.Address   // the launch HlAdmin, then each successor a kind 9 named: the last is Live
	Retired []*ethx.AdminState // the line's HlAdmins before the live one, in order
	Live    common.Address
	Admin   *ethx.AdminState // the live HlAdmin's
	Gate    *ethx.GateState
	Pending []*ethx.Decision
	Block   *ethx.Block
	NoCode  map[uint64]common.Address // decision number → its implementation or successor, without code
}

// readAdminFacts reads the line of HlAdmins — the deployment's, then each successor a kind 9 handed every authority
// to (successor(), as the audit's line, at most lineHops), so the members' commands act on the live one after a
// hand-over, never only on the retired one (E4-TL6) — the live one and the gate, and finds every pending decision of
// the live one: `pending()` of them, searched from the last number down (a decision pends below later ones that
// executed at once — a cancel, an unpause matured).
func readAdminFacts(ctx context.Context, cn *conn, d *Deployment) (*adminFacts, error) {
	b, err := cn.c.BlockAt(ctx, ethx.Latest)
	if err != nil {
		return nil, err
	}
	at := ethx.AtNumber(b.Number)
	f := &adminFacts{Block: b}
	cur := d.Addr("hlAdmin")
	seen := map[common.Address]bool{cur: true}
	for {
		f.Line = append(f.Line, cur)
		st, err := ethx.ReadAdmin(ctx, cn.c, cur, at)
		if err != nil {
			return nil, err
		}
		if !st.Retired() {
			f.Live, f.Admin = cur, st
			break
		}
		if len(f.Retired) >= lineHops || seen[st.Successor] {
			return nil, fmt.Errorf("HlAdmin %s's line of successors runs past %d hops or loops at %s", d.Addr("hlAdmin").Hex(), lineHops, st.Successor.Hex())
		}
		f.Retired = append(f.Retired, st)
		seen[st.Successor] = true
		cur = st.Successor
	}
	if f.Gate, err = ethx.ReadGate(ctx, cn.c, d.Addr("gate"), at); err != nil {
		return nil, err
	}
	want := int(f.Admin.Pending)
	for top := f.Admin.LastSeq; top > 0 && len(f.Pending) < want; {
		lo := uint64(1)
		if top > 100 {
			lo = top - 99
		}
		var ns []uint64
		for n := top; n >= lo; n-- {
			ns = append(ns, n)
		}
		ds, err := ethx.ReadDecisions(ctx, cn.c, f.Live, ns, at)
		if err != nil {
			return nil, err
		}
		for _, x := range ds {
			if x != nil {
				f.Pending = append(f.Pending, x)
			}
		}
		top = lo - 1
	}
	sort.Slice(f.Pending, func(i, k int) bool { return f.Pending[i].Number < f.Pending[k].Number })
	f.NoCode = map[uint64]common.Address{}
	for _, x := range f.Pending {
		if target, ok := ethx.CodeTarget(x.Kind, x.Params); ok {
			code, err := cn.code(ctx, at, target)
			if err != nil {
				return nil, err
			}
			if len(code) == 0 {
				f.NoCode[x.Number] = target
			}
		}
	}
	return f, nil
}

// waitingForCode says what a kind 8 or 9 whose target has no code waits for (fold F2: it pends, never spent, until
// code is deployed there).
func waitingForCode(target common.Address) string {
	return "NO CODE at " + target.Hex() + " yet: it pends (HlAdmin refuses its execute NoCodeYet) until code is deployed there — whoever can create at that address decides when it runs; GovDAO cancels it if it goes stale"
}

// printStatus is `decision status`.
func printStatus(w io.Writer, d *Deployment, f *adminFacts) {
	st, now := f.Admin, int64(f.Block.Time)
	for i, r := range f.Retired {
		fmt.Fprintf(w, "HlAdmin %s RETIRED: every authority went to the successor %s (kind 9), last decision number %d; nothing pending there executes\n", f.Line[i].Hex(), r.Successor.Hex(), r.LastSeq)
	}
	live := "HlAdmin"
	if len(f.Retired) > 0 {
		live = "the live HlAdmin"
	}
	fmt.Fprintf(w, "%s %s at block %d (%s): setup done %v, clock factor %d, origin %d; last decision number %d (the next takes %d), %d pending\n",
		live, f.Live.Hex(), f.Block.Number, time.Unix(now, 0).UTC().Format(time.RFC3339), st.SetupDone, st.ClockFactor, st.Origin, st.LastSeq, st.LastSeq+1, st.Pending)
	fmt.Fprintf(w, "flags on HlAdmin: %s; its hold: %s\n", flagsText(st.Keys, st.Flags), holdText(st.Hold))
	fmt.Fprintf(w, "flags on the gate: %s; its hold: %s\n", flagsText(f.Gate.Keys, f.Gate.Flags), holdText(f.Gate.Hold))
	if len(st.Holds) == 0 {
		fmt.Fprintln(w, "the hold history: no hold has ended")
	}
	for i, h := range st.Holds {
		fmt.Fprintf(w, "the hold history %d: from %s to %s\n", i, time.Unix(int64(h.Start), 0).UTC().Format(time.RFC3339), time.Unix(int64(h.End), 0).UTC().Format(time.RFC3339))
	}
	for _, r := range st.Cancelled {
		fmt.Fprintf(w, "cancelled range: %d–%d (every pending decision in it closes at its next execute)\n", r[0], r[1])
	}
	fmt.Fprintln(w, "pending decisions:")
	if len(f.Pending) == 0 {
		fmt.Fprintln(w, "  - none")
	}
	for _, x := range f.Pending {
		text, err := describeParams(x.Kind, x.Params)
		if err != nil {
			text = "PARAMETERS HlAdmin SPENDS (" + err.Error() + ")"
		}
		m := maturityOf(x, st, f.Gate.Hold, now)
		when := fmt.Sprintf("matures at %s (in %s)", time.Unix(m.At, 0).UTC().Format(time.RFC3339), time.Duration(m.At-now)*time.Second)
		if m.At <= now {
			when = "MATURE, awaiting execute (anyone: hleth decision execute -number " + strconv.FormatUint(x.Number, 10) + ")"
		}
		if m.Deferred != "" {
			when += "; " + m.Deferred
		}
		if target, ok := f.NoCode[x.Number]; ok {
			when += "; " + waitingForCode(target)
		}
		fmt.Fprintf(w, "  - #%d kind %d: %s; params %s; delivered at block %d (%s) by %s — %s\n", x.Number, x.Kind, text, hex.EncodeToString(x.Params), x.DeliveredBlock,
			time.Unix(int64(x.DeliveredTime), 0).UTC().Format(time.RFC3339), x.Deliverer.Hex(), when)
	}
	fmt.Fprintln(w, "a hold defers GovDAO's decisions until it ends, a replacement of guardians to the hold's 21st day at the earliest (§6.5); there is no recovery path.")
}

func flagsText(keys [6]common.Address, flags [6]ethx.Flag) string {
	var parts []string
	for i, f := range flags {
		if f.Set() {
			what := "guardian seat"
			if i >= 4 {
				what = "watch slot"
			}
			parts = append(parts, fmt.Sprintf("%s %d (%s) since block %d", what, i+1, keys[i].Hex(), f.BlockNumber))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "; ")
}

func holdText(h ethx.Flag) string {
	if !h.Set() {
		return "none"
	}
	return fmt.Sprintf("since %s (block %d)", time.Unix(int64(h.Time), 0).UTC().Format(time.RFC3339), h.BlockNumber)
}

// revertName is a revert's custom error name, as ethx decodes it by its selector from the ABIs it embeds: HlAdmin's,
// HlAdminExec's (the file-level SetupIncomplete, NotDelegated, AuthorityLost, NoCodeYet among them) and the Guardians
// library's — every error HlAdmin can answer (E-M3b's Q5).
func revertName(err error) string {
	r, ok := ethx.AsRevert(err)
	if !ok {
		return ""
	}
	return r.Name
}

// executeDecision is `decision execute`: the maturity read first (an immature or deferred decision is refused with
// when it matures, nothing sent), then HlAdmin.execute(number) from the given key.
func executeDecision(ctx context.Context, w io.Writer, cn *conn, d *Deployment, tx *txer, number uint64) error {
	f, err := readAdminFacts(ctx, cn, d)
	if err != nil {
		return err
	}
	var x *ethx.Decision
	for _, p := range f.Pending {
		if p.Number == number {
			x = p
		}
	}
	if x == nil {
		return fmt.Errorf("hleth decision execute: HlAdmin %s holds no pending decision %d (last %d, %d pending): executed, cancelled, spent, or never recorded", f.Live.Hex(), number, f.Admin.LastSeq, f.Admin.Pending)
	}
	now := int64(f.Block.Time)
	m := maturityOf(x, f.Admin, f.Gate.Hold, now)
	if !m.Ready {
		when := time.Unix(m.At, 0).UTC().Format(time.RFC3339)
		if m.Deferred != "" {
			return fmt.Errorf("hleth decision execute: decision %d (kind %d) is not executable now: matures at %s; %s (HlAdmin would refuse it: NotMature or Deferred)", number, x.Kind, when, m.Deferred)
		}
		return fmt.Errorf("hleth decision execute: decision %d (kind %d) matures at %s, in %s (HlAdmin would refuse it: NotMature)", number, x.Kind, when, time.Duration(m.At-now)*time.Second)
	}
	if target, ok := f.NoCode[number]; ok {
		return fmt.Errorf("hleth decision execute: decision %d (kind %d) is mature and waits for code: %s", number, x.Kind, waitingForCode(target))
	}
	data, err := ethx.ExecuteCall(number)
	if err != nil {
		return err
	}
	rc, err := tx.call(ctx, fmt.Sprintf("HlAdmin.execute(%d)", number), f.Live, data, nil)
	if err != nil {
		if n := revertName(err); n != "" {
			return fmt.Errorf("%w (HlAdmin refuses it: %s)", err, n)
		}
		return err
	}
	fmt.Fprintf(w, "decision %d (kind %d) executed by HlAdmin %s: tx %s, block %v, %d gas\n", number, x.Kind, f.Live.Hex(), rc.TxHash.Hex(), rc.BlockNumber, rc.GasUsed)
	return nil
}

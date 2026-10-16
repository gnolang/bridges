package agent

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// The relayer's decisions (PLAN.md §5.7), pure, so a fake chain can test them.

// attempt is one action's history: deterministic failures back off exponentially (a minute to an hour), three
// of them park it, and a parked one is retried every hour; a transient failure is retried in a minute and
// counts for nothing.
type attempt struct {
	Failures int       `json:"failures"` // deterministic failures in a row
	Tries    int       `json:"tries"`
	Next     time.Time `json:"next"`
	Parked   time.Time `json:"parked,omitempty"`
	LastErr  string    `json:"lastErr,omitempty"`
	// Cause is the program's refusal the action parked on, in its own field (G35: known by LastErr before, a
	// transient failure on a park's retry overwrote it and the day cap's reservation dropped until the next
	// refusal restored it); a park re-marked by the walk carries the mark here too (AQ4: OverMax by the live
	// limits).
	Cause string `json:"cause,omitempty"`
}

// causeIs says whether the action parked on the named refusal of one of our programs (the cause recorded, or,
// for a state file from before it, the last error's text).
func (a *attempt) causeIs(name string) bool {
	if a == nil {
		return false
	}
	if a.Cause != "" {
		return strings.Contains(a.Cause, name)
	}
	return strings.Contains(a.LastErr, name)
}

const (
	parkAfter   = 3
	backoffMin  = time.Minute
	backoffMax  = time.Hour
	parkedRetry = time.Hour
)

// due says whether the action may be tried now.
func (a *attempt) due(now time.Time) bool { return a == nil || !now.Before(a.Next) }

// parked says whether the action stands parked (retried hourly); each retry's failure re-raises the parked alarm
// (G27: raised at the parking alone, the webhook heard a parked message once, not hourly as §5.7's alert reads).
func (a *attempt) parked() bool { return a != nil && !a.Parked.IsZero() }

// fail records a failure; it reports whether this one parked the action. cause names the program's refusal
// behind a deterministic failure ("" for none): the park's cause, kept through a transient retry (G35).
func (a *attempt) fail(now time.Time, err error, deterministic bool, cause ...string) (parkedNow bool) {
	a.Tries++
	a.LastErr = err.Error()
	if !deterministic {
		a.Next = now.Add(backoffMin)
		return false
	}
	if len(cause) > 0 && cause[0] != "" {
		a.Cause = cause[0]
	}
	a.Failures++
	if a.Failures >= parkAfter {
		first := a.Parked.IsZero()
		if first {
			a.Parked = now
		}
		a.Next = now.Add(parkedRetry)
		return first
	}
	d := backoffMin << uint(a.Failures-1)
	if d > backoffMax {
		d = backoffMax
	}
	a.Next = now.Add(d)
	return false
}

// claimState is one route's Claim loop: it calls again at once while a Claim pays a full batch of a fee's worth a claim
// and more claims wait, and backs off otherwise (E5-GS1).
type claimState struct {
	Backoff   time.Duration `json:"backoff"`
	Next      time.Time     `json:"next"`
	LastPaid  time.Time     `json:"lastPaid"`
	PaidTotal int64         `json:"paidTotal"` // the route's paidTotal at the last look
	Calls     int           `json:"calls"`
	Paid      int           `json:"paid"` // claims paid something on, ever
	// Credited is the route's count of claims ever credited at the last look (E5-GS1): a claim credited since wakes the
	// loop for its first installment.
	Credited int64 `json:"credited,omitempty"`
	// Batch is how many claims the next Claim asks to pay (0: claimBatchMax), Gas the gas the client simulates it under
	// (0: the relayer's -gno-gas), Over the least batch known to need more than the room (0: none) — all sized from the
	// last Claims (claimSize, sized, outOfGas; I2's, carried from bridge-eth).
	Batch int   `json:"batch,omitempty"`
	Gas   int64 `json:"gas,omitempty"`
	Over  int   `json:"over,omitempty"`
}

// claimBatchMax is the most claims one Claim pays: §5.4's Claim(20).
const claimBatchMax = 20

// The Claim batch and its gas (the integration lane I2 on bridge-eth, from F15d's measure, carried here: the live relayer
// had the same loop). gno.Client.Call simulates a call under the gas it is given and once more under four times it when
// that runs out — the relayer's 45M, so 180M at most — and p/hlroute's Claim(20) took ≈140M of it on a gno v1.5.1 node at
// 400 waiting claims, its reads growing with the state; an out-of-gas is a transient failure, retried each minute for
// good, so a Claim(20) past 180M paid nothing ever and alarmed nothing. So the batch is sized from the last Claims to a
// room of three quarters of the client's ceiling: one past the room shrinks it in proportion and is remembered as over
// (Over); a full batch under the room grows it by its own projection (n × room / used, which over-counts the call's fixed
// cost, so it is safe) and by one at least, never to a size known over, until a projection says that size fits now (the
// state shrank); an out-of-gas halves it and is over too. A Claim of one that runs out asks four times the gas next —
// p/hlroute's first Claim after Launch walks every queue (ResetMeters: 429M at 400 queues) — as far as the client
// simulates (the block's gas), alarmed (claim:gas:<symbol>) until a Claim goes or the route's payouts move otherwise, and
// tried hourly once nothing more can be asked; the gas goes back to the relayer's once a Claim fits its room again. The
// gno key must hold the fee of the gas a simulation asks for, and pays a broadcast's.

// claimSize is the next Claim's batch and the gas the client simulates it under; base is the relayer's -gno-gas.
func (c *claimState) claimSize(base int64) (int, int64) {
	n := c.Batch
	if n <= 0 || n > claimBatchMax {
		n = claimBatchMax
	}
	return n, max(c.Gas, base)
}

// claimRoom is the most gas a Claim batch is sized to use when simulated under gas: three quarters of the client's
// ceiling, four times it — what a Claim grown with the state between two calls still fits in.
func claimRoom(gas int64) int64 { return 3 * gas }

// claimReach is the most gas the gno client simulates a call under when given gas: four times it, when that is within
// the block's gas; else gas alone (gno.Client.Call).
func claimReach(gas int64) int64 {
	if 4*gas <= gno.MaxBlockGas {
		return 4 * gas
	}
	return gas
}

// sized records a Claim of n simulated under gas that paid paid claims using used gas (above).
func (c *claimState) sized(n, paid int, gas, used, base int64) {
	if used <= 0 {
		return
	}
	room := claimRoom(gas)
	if c.Gas > base && used <= claimRoom(base) {
		c.Gas, c.Over, room = 0, 0, claimRoom(base) // back to the relayer's gas: its room, nothing known over it
	}
	proj := int(min(int64(n)*room/used, claimBatchMax))
	next := n
	switch {
	case used > room:
		c.Over, next = n, proj
	case paid >= n:
		if c.Over > 0 && proj >= c.Over {
			c.Over = 0 // what went over before fits by the projection now: the state shrank
		}
		next = max(n+1, proj)
		if c.Over > 0 {
			next = min(next, c.Over-1)
		}
	}
	c.Batch = min(max(next, 1), claimBatchMax)
}

// outOfGas records a Claim of n that ran out of the gas the client simulated it under: half the batch, n over; for a
// batch of one, four times the gas as far as the client simulates — the block's (raised) — or nothing left to ask
// (stuck).
func (c *claimState) outOfGas(n int, gas int64) (raised, stuck bool) {
	if n > 1 {
		c.Batch, c.Over = n/2, n
		return false, false
	}
	if claimReach(gas) >= gno.MaxBlockGas {
		return false, true
	}
	next := min(4*gas, gno.MaxBlockGas/4)
	if next <= gas {
		next = gno.MaxBlockGas // a -gno-gas past a quarter of the block's: simulated once under the block's
	}
	c.Gas, c.Over = next, 0
	return true, false
}

// outOfGasErr says whether a gno call failed for gas: the client's simulation ran out under its ceiling.
func outOfGasErr(err error) bool {
	var ce *gno.CallError
	if !errors.As(err, &ce) {
		return false
	}
	return strings.Contains(strings.ToLower(ce.Output), "out of gas") || (ce.Err != nil && strings.Contains(strings.ToLower(ce.Err.Error()), "out of gas"))
}

// wantClaim says whether a Claim call could pay anything now: the route is live, payouts are not paused, it is
// not migrated, a claim waits and the bridge meter holds at least the fee (a claim's first installment is the
// fee, whole; one mid-way needs less, and waits the few seconds the refill takes).
func wantClaim(rj *routeJSON) (bool, string) {
	switch {
	case !rj.Live:
		return false, "not launched"
	case rj.MigratedTo != "":
		return false, "migrated to " + rj.MigratedTo
	case rj.pausedFor("payouts"):
		return false, "payouts paused"
	case rj.OpenClaims == 0:
		return false, "no open claim"
	case rj.Meter <= 0 || rj.Meter < rj.Fee:
		return false, "the meter holds less than the fee"
	}
	return true, ""
}

// The Claim loop's cadence (E5-GS1). p/hlroute pays a claim in installments under two meters — the route's, a day's cap
// a day, and each recipient's, a quarter of that, empty at its first credit — so a claim larger than its recipient's
// meter (any claim to a fresh recipient: its first installment, the fee, is paid once the meter holds it, ≈173 s at the
// launch cap, and the rest as it refills, 2,500 GNOT a day) is paid a sliver a Claim: ≈0.1 GNOT a tick for ≈0.035 GNOT
// of the relayer's gas. The loop reset its backoff on any claim paid something, so it called Claim every tick for the
// claim's whole life — ≈5,400 Claims, ≈189 GNOT of the relayer's gno key for one transfer of 1,000 GNOT, the 5 GNOT fee
// going to r/hlfees. A Claim is worth calling again at once only when it paid a full batch, a fee's worth a claim at
// least, and more claims stay open (more may be payable now); one that paid less than a fee's worth a claim — slivers —
// counts as nothing paid, and one that paid fewer claims than it asked — every waiting recipient's meter emptied — waits
// too: both double the backoff, a minute to an hour. Waiting loses no payout: each meter holds a day's refill, so the
// Claim after the wait pays what the wait refilled (≈104 GNOT an hour to one recipient at the launch cap). A claim
// credited since the last look pulls the loop in to the time a fresh recipient's meter takes to hold the fee.

// fillWait is how long a fresh recipient's meter takes to hold a route's fee: a quarter of the day cap refills a day.
func fillWait(rj *routeJSON) time.Duration {
	if rj.Cap <= 0 || rj.Fee <= 0 {
		return 0
	}
	return time.Duration(float64(rj.Fee) * 4 / float64(rj.Cap) * float64(24*time.Hour))
}

// woken pulls the loop in for claims credited since the last look (rj.Claims past Credited): its backoff starts over and
// the next Claim comes no later than a fresh recipient's meter holds the fee — never later for a backoff an older
// claim's installments built up, never earlier than it was due.
func (c *claimState) woken(now time.Time, rj *routeJSON) {
	if rj.Claims <= c.Credited {
		return
	}
	c.Credited = rj.Claims
	c.Backoff = 0
	if due := now.Add(fillWait(rj)); c.Next.After(due) {
		c.Next = due
	}
}

// claimWorthMin is the least "a fee's worth" a claim is: one token (six decimals, both routes) — a route whose fee a
// decision lowered to nothing must not make slivers count as worth calling again at once (the review of lane s6, F6).
const claimWorthMin = 1_000_000

// after records a Claim call's result: paid claims paid something on, value in all (the route's paidTotal moved by it;
// negative when unread, judged by the count alone), the batch n it asked, the route's fee and whether claims stay open
// after it (above).
func (c *claimState) after(now time.Time, paid, n int, value, fee int64, open bool) {
	c.Calls++
	if paid > 0 {
		c.Paid += paid
		c.LastPaid = now
	}
	if paid > 0 && paid >= n && open && (value < 0 || value >= max(fee, claimWorthMin)*int64(paid)) {
		c.Backoff = 0
		c.Next = now
		return
	}
	if c.Backoff == 0 {
		c.Backoff = backoffMin
	} else {
		c.Backoff *= 2
		if c.Backoff > backoffMax {
			c.Backoff = backoffMax
		}
	}
	c.Next = now.Add(c.Backoff)
}

// dueDecisions are the queued decisions whose delay has passed by the chain's own clock (block time).
func dueDecisions(ds []decisionJSON, chainTime int64) []decisionJSON {
	var out []decisionJSON
	for _, d := range ds {
		if d.Queued() && chainTime >= d.Due {
			out = append(out, d)
		}
	}
	return out
}

// maxLeafBytes is the biggest results leaf the light-client ISM accepts for a proof (PLAN.md §6.2).
const maxLeafBytes = 8192

// reemitInput is what decides a Reemit for one dispatched message (§5.1): the light client delivers a message
// from a header signed by the current validator set, so one dispatched before that set's first height needs
// a fresh Dispatch event; so does one whose results leaf is bigger than the ISM accepts.
type reemitInput struct {
	Nonce            uint32
	Delivered        bool  // on Solana
	DispatchHeight   int64 // the block the message's own event is in
	CurrentSetFrom   int64 // the client's current set's first height; 0 = unknown
	LeafBytes        int   // the results leaf's size at the event's height (the last Reemit's, if any); 0 = unknown
	LeafUnbuildable  bool  // the block's leaves cannot be rebuilt (a type with no schema, or a string JSON cannot carry, beside it)
	PacketTooBig     bool  // the proof by header exceeds one packet (a block of more than 128 results, F6)
	LastReemitHeight int64 // the height of the last Reemit this relayer sent for the nonce, 0 if none
	// UnbuildableTries is how many Reemits in a row this relayer sent for the nonce because the block was
	// unbuildable, the last at LastReemitAt; a further one waits unbuildableBackoff(UnbuildableTries) from it (Now).
	UnbuildableTries int
	LastReemitAt     time.Time
	Now              time.Time
}

// unbuildableBackoff is the wait before the next Reemit of a message whose re-emitted blocks keep coming back
// unbuildable: none before the first, a minute after it, doubling, at most an hour. Each Reemit is paid in GNOT, and
// someone who puts a result no rebuild can read (a string the node's JSON cannot carry) into every block would
// otherwise drain the relayer's float at a Reemit a minute (E3-RT2's residual).
func unbuildableBackoff(tries int) time.Duration {
	switch {
	case tries <= 0:
		return 0
	case tries > 7:
		return time.Hour
	}
	return min(time.Hour, time.Minute<<(tries-1))
}

// needsReemit decides. It never asks twice for the same reason: a Reemit already sent at or after the current
// set's start covers the set change, and one sent at all covers the leaf size (a Reemit's leaf is small) and an
// unbuildable block (a Reemit lands in a fresh block; a second one is asked only if that block is unbuildable
// too, which is what the event height's check sees).
func needsReemit(in reemitInput) (bool, string) {
	if in.Delivered {
		return false, "delivered"
	}
	eventHeight := in.DispatchHeight
	if in.LastReemitHeight > eventHeight {
		eventHeight = in.LastReemitHeight
	}
	if in.CurrentSetFrom > 0 && eventHeight < in.CurrentSetFrom {
		return true, fmt.Sprintf("its event is at height %d, before the current validator set's first height %d", eventHeight, in.CurrentSetFrom)
	}
	if in.LeafBytes > maxLeafBytes && in.LastReemitHeight == 0 {
		return true, fmt.Sprintf("its results leaf is %d bytes, above the %d the ISM accepts", in.LeafBytes, maxLeafBytes)
	}
	if in.PacketTooBig && in.LastReemitHeight == 0 {
		return true, "its proof by header exceeds one packet (a block of more than 128 results, F6): a fresh block"
	}
	if in.LeafUnbuildable {
		if wait := unbuildableBackoff(in.UnbuildableTries); wait > 0 && in.Now.Sub(in.LastReemitAt) < wait {
			return false, fmt.Sprintf("block %d's results cannot be rebuilt here, the %d Reemits before landed in unbuildable blocks too: the next waits until %s", eventHeight, in.UnbuildableTries, in.LastReemitAt.Add(wait).UTC().Format(time.RFC3339))
		}
		return true, fmt.Sprintf("block %d's results cannot be rebuilt here (a result of a type with no schema, or a string the node's JSON cannot carry, beside the dispatch)", eventHeight)
	}
	return false, ""
}

package agent

import (
	"fmt"
	"strings"
	"time"
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

// claimState is one route's Claim loop: it calls while something is paid and backs off while nothing is.
type claimState struct {
	Backoff   time.Duration `json:"backoff"`
	Next      time.Time     `json:"next"`
	LastPaid  time.Time     `json:"lastPaid"`
	PaidTotal int64         `json:"paidTotal"` // the route's paidTotal at the last look
	Calls     int           `json:"calls"`
	Paid      int           `json:"paid"` // claims paid something on, ever
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

// after records a Claim call's result: paid > 0 resets the backoff; nothing paid doubles it (a minute to an
// hour), since the meters refill on their own clock.
func (c *claimState) after(now time.Time, paid int) {
	c.Calls++
	if paid > 0 {
		c.Paid += paid
		c.LastPaid = now
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
	LeafUnbuildable  bool  // the block's leaves cannot be rebuilt (a failed transaction or an unknown event type beside it)
	PacketTooBig     bool  // the proof by header exceeds one packet (a block of more than 128 results, F6)
	LastReemitHeight int64 // the height of the last Reemit this relayer sent for the nonce, 0 if none
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
		return true, fmt.Sprintf("block %d's results cannot be rebuilt here (a failed transaction or an event with no schema beside the dispatch)", eventHeight)
	}
	return false, ""
}

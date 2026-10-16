package agent

import (
	"strings"
	"time"
)

// An action's history (the Solana plan §5.7, carried): deterministic failures back off exponentially (a minute to
// an hour), three of them park it, and a parked one is retried every hour; a transient failure is retried in a
// minute and counts for nothing. On Ethereum a deterministic failure is a revert the simulation answered (nothing
// was spent) or a mined one; a base fee above the maximum is neither — the action waits for it to fall.

type attempt struct {
	Failures int       `json:"failures"` // deterministic failures in a row
	Tries    int       `json:"tries"`
	Next     time.Time `json:"next"`
	Parked   time.Time `json:"parked,omitempty"`
	LastErr  string    `json:"lastErr,omitempty"`
	// Cause is the contract's refusal the action parked on, kept through a transient retry (G35).
	Cause string `json:"cause,omitempty"`
	// MinGas is the least gas limit the action's next transaction carries: an execute HlAdmin refused CallStarved
	// (audit round 3, E3-CT4) is sent again with more gas than its estimate showed (executor.go, starvedGas).
	MinGas uint64 `json:"minGas,omitempty"`
}

// causeIs says whether the action parked on the named refusal (the cause recorded, or the last error's text).
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

// parked says whether the action stands parked (retried hourly).
func (a *attempt) parked() bool { return a != nil && !a.Parked.IsZero() }

// fail records a failure; it reports whether this one parked the action. cause names the contract's refusal behind
// a deterministic failure ("" for none).
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

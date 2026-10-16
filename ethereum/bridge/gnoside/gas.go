package gnoside

import (
	"context"
	"errors"
	"strings"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// A call whose gas only a simulation knows (a realm decision's Execute, GovDAO's ExecuteOrRejectProposal, a guardian's
// SetFlag naming ids): gno.Client.Call simulates a call under the gas it is given and once more under four times it,
// within the block's gas, then sizes the broadcast from the simulation that fit, a fifth above it (gno.GasFor). Under
// the machines' 45M that reaches 180M — and on a gno v1.5.1 node a guardian's payouts flag naming 256 ids costs ≈475M
// (hlmailbox keeps the list sorted, fold-r6/f18b.md; ≈555M in round 5, when the tool set ≈70 ids at most), the
// Execute of a queued range void of 100 credits ≈0.8B and of a queued restore of 40 forged claims ≈0.62B at 10,000
// open claims (fold-r6/f18b.md, i4.md; a forged restore of 256 ids was ≈2.53B in round 5, realms.md "for the lead"):
// such a call ran out of gas in its simulation for good, and needed a member's own hand.

// CallUpToTheBlock makes such a call: through the client, under gas first; on an out-of-gas the ceiling is raised
// fourfold past what the client reached (16 times gas, then the block's), as far as the block's gas, gno.MaxBlockGas —
// the broadcast sized by the client from the simulation that fit, never from a guess. Any other failure, or an
// out-of-gas at the block's gas, is the answer. The key must hold the fee of the gas each simulation asks for (3 GNOT
// at the block's gas at gnoland-1's minimum price): the ante holds a simulation's fee to the balance as a
// transaction's.
func CallUpToTheBlock(ctx context.Context, c Caller, pkgPath, fn string, gas int64, args ...string) (*gno.Result, error) {
	for {
		res, err := c.Call(ctx, pkgPath, fn, gas, args...)
		if err == nil || !OutOfGas(err) {
			return res, err
		}
		next := NextCeiling(gas)
		if next <= gas {
			return nil, err
		}
		gas = next
	}
}

// Reach is the most gas gno.Client.Call simulates a call under when given gas: four times it when that is within the
// block's gas, else gas alone.
func Reach(gas int64) int64 {
	if 4*gas <= gno.MaxBlockGas {
		return 4 * gas
	}
	return gas
}

// NextCeiling is the gas the next call is given after one given gas ran out of it: four times what the client reached
// (Reach), at most the block's gas; gas itself when the client reached the block's already (nothing more to ask).
func NextCeiling(gas int64) int64 {
	r := Reach(gas)
	if r >= gno.MaxBlockGas {
		return gas
	}
	return min(4*r, gno.MaxBlockGas)
}

// OutOfGas says whether a gno call failed for gas: its simulation (or the block) ran out under its ceiling.
func OutOfGas(err error) bool {
	var ce *gno.CallError
	if !errors.As(err, &ce) {
		return false
	}
	return strings.Contains(strings.ToLower(ce.Output), "out of gas") || (ce.Err != nil && strings.Contains(strings.ToLower(ce.Err.Error()), "out of gas"))
}

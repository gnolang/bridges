package hlbubble

import "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"

// The installments rule (the round-5 go-spend audit's E5-GS1, its realm half). p/hlroute's Claim pays a claim's first
// installment only when the meters hold the fee, whole, but every later one as soon as they hold anything: a recipient
// whose claim outruns its meter (a quarter of the day cap a day: 2,500 tokens at the launch floor, ≈0.03 a second) is
// paid a sliver at every visit, and the relayers, who call Claim again whenever it paid something, paid ≈28M gas a call
// for ≈0.1 GNOT paid out on the GNOT route — ≈300–580 GNOT a day of their gno key for one large transfer. p/hlroute is
// shared with the live Solana routes and immutable, so this realm keeps the rule around it: a later installment is paid
// only when the bridge meter and the recipient's meter both hold at least min(what the claim still owes, the fee) — a
// visit that pays something pays a fee's worth, or the claim's whole remainder — and a recipient whose meters hold less
// is passed over, as p/hlroute passes over a dry one, its meter refilling for a later turn.
//
// How. Claim(n) is n turns of p/hlroute's Claim of one (at most four recipients looked at and one claim paid a turn:
// n claims and 4n recipients at most, p/hlroute's own bounds for n). Within a turn a later installment reaches Env.Pay
// with no Env.PayFee before it (a first installment pays the fee first); Pay refuses a sliver by panicking with
// sliver{} before anything moves, and the turn recovers it. The visit then leaves what p/hlroute's own pass over a dry
// recipient leaves — its cursor on the recipient, the two meters refilled to now — and nothing else, since p/hlroute
// books an installment only after Pay returns. The panic crosses no realm boundary (p/hlroute's method on this realm's
// own state, and this realm's closure, run under this realm: interrealm v2 §4, §6), so the recover is this realm's;
// any other panic goes on as it came. A sliver is told from the meters: p/hlroute pays min(what the claim owes, the
// meters, the lock), so an installment below the fee and not below the meters (st.MeterLevels at the same time, after
// the same refill: a read) is the meters' own; the claim's remainder, or the lock, bounds any other.

// sliver is the panic value a turn's Pay raises to pass over an installment the rule refuses; nothing else raises it.
type sliver struct{}

// claimTurns pays up to n claims (n ≤ hlroute.MaxClaims) in n turns under the installments rule, at time t under the
// route's payouts epoch, and returns how many it paid something on. An n below 1 is p/hlroute's refusal.
func claimTurns(cur realm, n int, t, epoch, epochAt int64) int {
	e := env(cur)
	if n < 1 {
		return st.Claim(t, n, epoch, epochAt, e) // refused: n must be positive
	}
	if n > hlroute.MaxClaims {
		n = hlroute.MaxClaims
	}
	pay, payFee := e.Pay, e.PayFee
	feePaid := false
	e.PayFee = func(amount int64) {
		feePaid = true
		payFee(amount)
	}
	e.Pay = func(to address, amount int64) {
		if !feePaid && isSliver(to, amount, t, epoch, epochAt) {
			panic(sliver{})
		}
		pay(to, amount)
	}
	paid := 0
	for turn := 0; turn < n; turn++ {
		feePaid = false
		paid += claimTurn(e, t, epoch, epochAt)
	}
	return paid
}

// claimTurn is one turn: p/hlroute's Claim of one, a sliver passed over (0 paid).
func claimTurn(e hlroute.Env, t, epoch, epochAt int64) (paid int) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(sliver); !ok {
				// echo-ok(r): a panic of p/hlroute's Claim or of this realm's payment, re-raised as it came (sliver{} aside): each holds to its own checks
				panic(r)
			}
			paid = 0
		}
	}()
	return st.Claim(t, 1, epoch, epochAt, e)
}

// isSliver is whether a later installment of amount to `to` is one the rule refuses: below the fee, and as large as
// the meters hold (so the meters bound it — the claim's remainder or the lock bounds one below them).
func isSliver(to address, amount, t, epoch, epochAt int64) bool {
	if amount >= st.Fee {
		return false
	}
	bridge, recipient := st.MeterLevels(t, to, epoch, epochAt) // echo-ok(to): the recipient p/hlroute's own Claim pays (a queue's key); MeterLevels reads, it refuses nothing
	if recipient < bridge {
		bridge = recipient
	}
	return amount >= bridge
}

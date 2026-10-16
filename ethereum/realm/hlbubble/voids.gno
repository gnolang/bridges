package hlbubble

import (
	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	"gno.land/p/nt/avl/v0"
)

// The void's bounds (the round-6 realms audit's E6-RL1). p/hlroute's void takes a list of up to hlroute.MaxIDs = 256
// ids, or a range of up to hlroute.MaxRangeClaims = 1,000 credits, and its work an id or a credit — two CleanIDs and
// two tree reads, a guardian's flags asked (hlmailbox.Flagged), a void's dequeue — grows with the route: on a gno
// v1.5.1 node the governor's Govern of a void of 256 ids at once (the builder and the closure in one transaction) was
// 3.02B gas at 5,000 open claims and 3.45B at 10,000 with four guardians' lists standing, and a range of 1,000 credits
// 4.44B at once and 3.58B Executed — past gnoland-1's block of 3,000,000,000. p/hlroute is shared with the live Solana
// routes and immutable, so this realm bounds what it hands it: a list of maxVoidIDs ids (the draft's own reading, its
// VoidParams), and a range the credit tally counts at most maxVoidCredits credits in, each held at its node figure by
// eth/scripts/gnotest.sh's void gate. The builder closes a range at the block before the one it is made in (closedAt
// at height − 1, passed to DraftVoid as its height): every credit after the proposal is made comes in at that block or
// a later one — Process's and a credit by vote's at the height now, a predecessor's pages never (a range void is
// refused while they arrive, and a successor credits nothing before them) — so what a range covers can never grow
// after its builder counted it, and its closure and its Execute walk what was counted. It lives here, in a file the
// three routes keep the same (gnotest.sh compares them), so every route bounds alike.
const (
	maxVoidIDs     = 64
	maxVoidCredits = 100
)

// creditsAt is the credit tally: how many credits p/hlroute's index — its credits by height, what a range void walks —
// took at each height (posKey(height) → int64): every credit of Process, a credit by vote's when its Execute applies it,
// and the claims and sweep entries a predecessor's page brings at their own heights (a successor's Adopt). It only
// counts up: an entry p/hlroute drops later (a sweep payment's, the claim a credit by vote replaces, a page pulled)
// stays counted, so the tally over a range is never below what the range's walk visits.
var creditsAt avl.Tree

// countCredit tallies one credit p/hlroute's index took at height h.
func countCredit(h int64) {
	k := posKey(h)
	n := int64(1)
	if v := creditsAt.Get(k); v != nil {
		n += v.(int64)
	}
	creditsAt.Set(k, n)
}

// creditsIn is the tally over the heights the closed range r covers, read until it passes maxVoidCredits: at most
// maxVoidCredits+1 heights are read, whatever the range spans.
func creditsIn(r hlroute.Range) int64 {
	n := int64(0)
	creditsAt.Iterate(posKey(r.From), posKey(r.To+1), func(_ string, v any) bool {
		n += v.(int64)
		return n > maxVoidCredits
	})
	return n
}

// closedAt is the range as p/hlroute's void takes it when made at height (its Range.closed, unexported there): a
// Since range, or one that reaches past height, ends at height.
func closedAt(r hlroute.Range, height int64) hlroute.Range {
	if r.Since && (r.To == 0 || r.To > height) || !r.Since && r.To > height {
		r.To = height
	}
	return r
}

// rangeRefusal is why a builder refuses a range void before p/hlroute walks it, or "": its credits, the range closed as
// the builder closes it (closedAt, at the block before the proposal's), counted by the tally above maxVoidCredits.
func rangeRefusal(r hlroute.Range) string {
	if creditsIn(r) > maxVoidCredits {
		return "a void of " + r.String() + " takes more than " + itoa(maxVoidCredits) + " credits (this realm's tally of the credits by height): split the range by heights"
	}
	return ""
}

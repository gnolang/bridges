package agent

import (
	"context"
	"fmt"
	"time"
)

// The credits the dispatch index cannot place (the Go trust audit's E4-GT1, round 4, whose Solana sibling this is).
//
// The cut's order needs every burn credited in the gno state to be at or below the Solana snapshot's slot, or, being
// above it, known to be (Cut.CreditedAfterSlot): a burn above the slot is still in the snapshot's supply while owed
// counts it, and the reading is over by its amount — a break. Only the dispatch index could place a credit, so a
// credited burn the index did not hold yet was in neither: a monitor whose two providers trail the signers' (an
// ordinary lagging backend; a liar at will) read an honest credit as BROKEN — credits and payouts flagged, the
// pausable ISM paused for every token until GovDAO unpauses — a signer halted, and hlwatch printed broken and, a
// reading later, FORGED CREDIT for the honest claim (reproduced, TestS5*Trailing*). The index cannot place such a
// credit until its providers reach the burn; what the cut can know is how many credits there are: hlmailbox's
// delivered count, which every credit raises (Process, and a credit by vote) and which the cut already holds still
// between its first and last read (E3-GT2). So each reading counts the credits made since the last one — a window of
// heights whose count is exact, the delivered count being read between the cut's two reads of hlmailbox, where it
// does not move — and the index's burns credited in that window. A window whose credits the index has not all placed
// is no verdict while it may hold a burn above the snapshot: until the snapshot's slot was produced after the
// window's last block (Solana's own clock on that slot, read with the supplies, against gno.land's block time) by a
// margin, since a credited burn was finalized — 32 slots after it was produced, ≈7 s at today's ≈0.21 s a slot (142
// slots in 30 s of Solana's own clock, measured on mainnet 2026-10-15; E5-SF6) — before it was credited. A
// window the index places in full closes; one whose time the snapshot has passed closes too, its unplaced credits
// then no lag: a credit by vote of an id the index never held, or a forged credit, which the identity and the
// unindexed-credit rule judge as they stand.

// creditMargin is how far past a window's last block the snapshot's slot must have been produced before the window's
// unplaced credits are judged as they stand (the clocks of the two chains are each within a second or two of the
// time — measured on mainnet 2026-10-15: the finalized slot's Clock ≈8 s behind the wall on all four live providers,
// so Solana's clock ≈1 s behind the time; the finalization delay, ≈7 s, is the rest of the slack). A window so closes
// ≈13 s after the reading that counts it, ≈18 s after its last block's stamp (gno.land's head ≈3.5–7 s behind the
// wall), so a forged credit is judged ≈20 s after its block and a restarted signer whose book did not place its
// credits has its first verdict ≈16 s after the restart (E5-SF6).
const creditMargin = 10 * time.Second

// creditBook is the cut's count of credits between readings (E4-GT1): hlmailbox's delivered count at the last reading
// whose cut held, the height it was exact at, and the windows whose credits the index had not all placed.
type creditBook struct {
	Delivered int            `json:"delivered"`
	At        int64          `json:"at"` // a height at which the delivered count was exactly Delivered (0: none yet)
	Open      []creditWindow `json:"open,omitempty"`
}

// creditWindow is a run of heights (From, To] holding N credits, To's block made at Time (unix): every credit in it was
// made at or before Time.
type creditWindow struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
	N    int   `json:"n"`
	Time int64 `json:"time"`
}

// place takes a reading whose cut held still: hlmailbox's delivered count D, exact at height at (whose block was made
// at atTime), the heights the index's burns were credited at, and the time Solana stamped on the snapshot's slot (zero
// when unread). It answers why the reading is no verdict — "" when every credit is placed or older than the snapshot
// — and how many unplaced credits it closed as no lag this reading (for the unindexed-credit rule to find).
func (b *creditBook) place(D int, at int64, atTime time.Time, credited []int64, snapTime time.Time) (why string, closed int) {
	switch {
	case b.At == 0 || at < b.At || D < b.Delivered:
		// the first reading, or a node that answers below the last one: every credit so far is one window
		b.Open = nil
		if D > 0 {
			b.Open = append(b.Open, creditWindow{From: 0, To: at, N: D, Time: atTime.Unix()})
		}
	case D > b.Delivered:
		b.Open = append(b.Open, creditWindow{From: b.At, To: at, N: D - b.Delivered, Time: atTime.Unix()})
	}
	b.Delivered, b.At = D, at
	if D <= len(credited) {
		// the index places as many credits as there are (each a delivered id of its own): none unplaced anywhere
		b.Open = nil
		return "", 0
	}
	var open []creditWindow
	for _, w := range b.Open {
		placed := 0
		for _, h := range credited {
			if h > w.From && h <= w.To {
				placed++
			}
		}
		unplaced := w.N - placed
		switch {
		case unplaced <= 0:
			continue
		case !snapTime.IsZero() && !snapTime.Before(time.Unix(w.Time, 0).Add(creditMargin)):
			closed += unplaced
			continue
		}
		open = append(open, w)
		if why == "" {
			stamped := "Solana's clock on the snapshot's slot unread"
			if !snapTime.IsZero() {
				stamped = "the snapshot's slot was produced at " + snapTime.UTC().Format(time.RFC3339)
			}
			why = fmt.Sprintf("%d credit(s) made at heights %d-%d (by %s) are in no dispatch index yet, and %s: a burn among them may lie above the slot, still in the supply while owed counts it — no verdict until the index places them or the snapshot passes their time by %s (E4-GT1)",
				unplaced, w.From+1, w.To, time.Unix(w.Time, 0).UTC().Format(time.RFC3339), stamped, creditMargin)
		}
	}
	b.Open = open
	return why, closed
}

// creditedHeights are the heights the index's placed burns were credited at: every record credited and not disputed (a
// disputed record's slot is no reading, so its credit is placed by time alone).
func creditedHeights(index map[string]*dispatchRec) []int64 {
	var out []int64
	for _, rec := range index {
		if rec.Credited != 0 && rec.Disputed == "" {
			out = append(out, rec.Credited)
		}
	}
	return out
}

// overBound is, by symbol, the most the credits a reading cannot place could put it over by (E6-GT1's Solana sibling): the
// burns the index holds above their route's supply slot (owed, if credited, while their supply is not burned at the
// snapshot: above, by symbol, the caller's), and every credit no index holds that may still be a burn above the snapshot's slot — made
// above height from (the last height the book counted exactly, or an open window's start) in a block less than
// creditMargin before Solana stamped the snapshot's slot (snapTime; the book's own rule: an older one is placed or no
// lag) — by its claim's amount (such a burn credited before the routes were read is owed, its supply not burned yet). A
// move of the delivered count does nothing else that puts a reading over (cutMove). ok false when the done tree, a
// block or a claim could not be read: nothing is judged on it.
func overBound(ctx context.Context, g gnoReader, cfg *Config, migs map[string]*migration, lineErrs map[string]error, d int, H, from int64, snapTime time.Time, indexed func(id string) bool, above map[string]int64) (map[string]int64, bool) {
	out := map[string]int64{}
	for sym, a := range above {
		out[sym] += a
	}
	entries, err := doneIDs(ctx, g, cfg.Gno.Mailbox, d)
	if err != nil {
		return nil, false
	}
	recent, err := recentFrom(ctx, g, H, from, snapTime)
	if err != nil {
		return nil, false
	}
	for _, e := range entries {
		if e.Height <= from || e.Height < recent || indexed(e.ID) {
			continue
		}
		c, r, err := creditClaimOf(ctx, g, cfg, migs, lineErrs, e.ID, H)
		if err != nil {
			return nil, false
		}
		if c != nil && r != nil {
			out[r.Symbol] += max(c.Amount, c.Swept)
		}
	}
	return out, true
}

// recentWalk bounds recentFrom's walk back from the head: past it every credit above from counts (≈4 minutes of blocks).
const recentWalk = 64

// recentFrom is the lowest height whose credits may still be burns above the snapshot's slot: back from H, the first
// block made at least creditMargin before snapTime ends it (that block and the ones below are placed or no lag); every
// height above from with Solana's clock unread, or past the walk.
func recentFrom(ctx context.Context, g gnoReader, H, from int64, snapTime time.Time) (int64, error) {
	if snapTime.IsZero() {
		return from + 1, nil
	}
	for h, n := H, 0; h > from; h, n = h-1, n+1 {
		if n >= recentWalk {
			return from + 1, nil
		}
		b, err := g.Block(ctx, h)
		if err != nil {
			return 0, err
		}
		if !snapTime.Before(b.Time.Add(creditMargin)) {
			return h + 1, nil
		}
	}
	return from + 1, nil
}

// boundFrom is the lowest height the book does not count as placed: its last exact height, or an open window's start.
func (b *creditBook) boundFrom() int64 {
	from := b.At
	for _, w := range b.Open {
		from = min(from, w.From)
	}
	return from
}

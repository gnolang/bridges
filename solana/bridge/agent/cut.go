package agent

import (
	"context"
	"fmt"
	"strings"
)

// The cut's two guards from the Ethereum port's third audit round (the Go trust audit's E3-GT2 and E3-GT9), kept
// here for the signers, the monitors and hlwatch alike.
//
// The read-twice guard (E3-GT2). gno.land answers its latest state at any height — its realms' objects are not
// versioned (gno.land mounts its base store unversioned; a "render at H" is a render of now) — so the cut's gno reads
// are a sequence of reads, each at whatever is latest: hlmailbox, the routes and their lines of migrations, then,
// after the Solana scans, the burns' delivered marks (and on the monitors, the sends' messages and refund marks). A
// credit landing after a route was read and before its burn's mark is in neither owed nor in flight: the reading is
// short by its amount, room for a forged mint of that amount, burned back toward gno.land, to pass the round — one
// round with no break lifts a signer's halt and has every burn at or below the verdict's slot signed, the forged one
// too. So hlmailbox is read before the routes and again once the last mark is read: its delivered count moves with
// every credit (Process, and a credit by vote), as its count moves with every dispatch. A token whose line of
// migrations is open, or (on the machines that read refund marks) whose sends a refund can mark, has its realms'
// counters read again too: a migration's page moving the counters between the line's realms, or a refund taking a
// send out of the lock, between the reads is the same slack (or a double count, a false break). Any move is no
// verdict: the cut is taken again next round.
//
// The floor (E3-GT9). A Solana provider answers an account at its own finalized slot, and the snapshot takes the
// lower of the two providers' slots. A finalized slot never un-finalizes, so one answer below a slot a machine
// already read at is a provider stepped back — a lagging backend, or a liar — and what was read at the higher slot
// stays: the sends' processed marks, the burns' in-flight count by their slots, a halt. Read against the older slot
// the books are short by what moved between them (the supplies must still agree, which the stepped-back provider
// meets whenever the supply returned to an earlier value — a burn of one's own tokens does that): one round that
// holds over a forged mint lifts a signer's halt and signs its burn, and a monitor reads a break as holding. So the
// highest slot a reading rested on is kept in the machine's state, and a snapshot below it is no verdict.

// realmCounters are one realm's figures as one read gave them: what the identity sums — lock, owed, voided, lost —
// and its refund count (a refund moves the lock and a send's mark together).
type realmCounters struct {
	Path                               string
	Lock, Owed, Voided, Lost, Refunded int64
}

func (c realmCounters) String() string {
	return fmt.Sprintf("%s lock %d owed %d voided %d lost %d refunds %d", c.Path, c.Lock, c.Owed, c.Voided, c.Lost, c.Refunded)
}

// countersOf are a token's realms' counters as read: the line's, realm by realm in its order, while a migration is
// open; else the route's own.
func countersOf(path string, rj *routeJSON, mig *migration) []realmCounters {
	of := func(p string, j *routeJSON) realmCounters {
		c := realmCounters{Path: p}
		if j != nil {
			c.Lock, c.Owed, c.Voided, c.Lost, c.Refunded = j.Lock, j.Owed, j.Voided, j.Lost, j.Refunded
		}
		return c
	}
	if mig != nil {
		var out []realmCounters
		realms := mig.realms()
		for i, p := range mig.Paths() {
			out = append(out, of(p, realms[i]))
		}
		return out
	}
	return []realmCounters{of(path, rj)}
}

func sameCounters(a, b []realmCounters) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func countersText(cs []realmCounters) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = c.String()
	}
	return strings.Join(parts, "; ")
}

// cutMoved is the read-twice guard, taken once every mark of the cut is read: hlmailbox again, its delivered count
// held to first's (read before the routes), and every token whose line of migrations is open — and, with refunds,
// every token whose sends a refund can mark — read again, realm by realm. It answers why the cut moved, "" when it
// held. The count is held by the monitors right after the routes' read (a send landing there is in the lock and
// not walked); the signers read no sends, so a dispatch moves only a lock their rule already reads short by every
// send in flight (AP1).
func cutMoved(ctx context.Context, g gnoReader, cfg *Config, H int64, first *mailboxJSON, routes map[string]*routeJSON, migs map[string]*migration, refunds bool) (string, error) {
	mv, err := cutMovedBy(ctx, g, cfg, H, first, routes, migs, refunds)
	return mv.why, err
}

// cutMove is what the read-twice guard found: why the cut moved ("" when it held), hlmailbox as read again, and whether
// only its delivered count moved — every counter it reads again held — which can make a reading short, never over: a
// credit of an indexed burn landing between the routes' read and its mark's is in neither owed nor in flight, and one of
// a burn no index holds is in owed only if it landed before the routes were read, its amount then the most it puts a
// reading over by (E6-GT1's Solana sibling, credits.go's overBound).
type cutMove struct {
	why         string
	again       *mailboxJSON
	creditsOnly bool
}

// cutMovedBy is cutMoved, saying what moved: the counters are read again even when the delivered count moved.
func cutMovedBy(ctx context.Context, g gnoReader, cfg *Config, H int64, first *mailboxJSON, routes map[string]*routeJSON, migs map[string]*migration, refunds bool) (cutMove, error) {
	again, err := mailboxAt(ctx, g, cfg.Gno.Mailbox, H)
	if err != nil {
		return cutMove{}, fmt.Errorf("hlmailbox: %w", err)
	}
	mv := cutMove{again: again}
	if again.Delivered != first.Delivered {
		mv.why = fmt.Sprintf("hlmailbox delivered %d message(s) while the cut was read (its delivered count %d, then %d: a credit between the routes' read and the delivered marks' is in neither owed nor in flight)", again.Delivered-first.Delivered, first.Delivered, again.Delivered)
	}
	why, err := countersMoved(ctx, g, cfg, H, again, routes, migs, refunds)
	if err != nil {
		return cutMove{}, err
	}
	switch {
	case why != "" && mv.why == "":
		mv.why = why
	case why == "" && mv.why != "":
		// (a send dispatched meanwhile is in a lock the walk did not count, or in neither: short, never over)
		mv.creditsOnly = true
	}
	return mv, nil
}

// countersMoved reads again, realm by realm, every token whose line of migrations is open — and, with refunds, every
// token whose sends a refund can mark — and says why its counters moved since routes, "" when they held.
func countersMoved(ctx context.Context, g gnoReader, cfg *Config, H int64, again *mailboxJSON, routes map[string]*routeJSON, migs map[string]*migration, refunds bool) (string, error) {
	read := routeReaderAt(g, H)
	for _, r := range cfg.Routes {
		rj := routes[r.Symbol]
		if rj == nil || r.Token == "" {
			continue
		}
		mig := migs[r.Symbol]
		if mig == nil && !(refunds && refundable(rj, nil)) {
			continue // one realm, read in one render, and no mark of its read after it: a credit moves the delivered count
		}
		rj2, err := routeAt(ctx, g, r.Gno, H)
		if err != nil {
			return "", fmt.Errorf("%s read again: %w", r.Symbol, err)
		}
		var mig2 *migration
		if mig != nil {
			if mig2, err = migrationOf(ctx, read, r, rj2, again); err != nil {
				return "", fmt.Errorf("%s's line of migrations read again: %w", r.Symbol, err)
			}
		}
		if a, b := countersOf(r.Gno, rj, mig), countersOf(r.Gno, rj2, mig2); !sameCounters(a, b) {
			return fmt.Sprintf("%s's counters moved while the cut was read (%s, then %s: a migration's page, or a refund, landing between the reads)", r.Symbol, countersText(a), countersText(b)), nil
		}
	}
	return "", nil
}

// movedText is a moving cut's line for a status and an alarm.
func movedText(why, round string) string {
	return why + ": the node answers its latest state, so the cut is taken again next " + round
}

// belowFloor says why a snapshot slot below the floor is no reading (E3-GT9), "" when it is at or above it.
func belowFloor(slot, floor uint64) string {
	if slot >= floor {
		return ""
	}
	return fmt.Sprintf("the providers' finalized slot %d is below %d, the highest a reading already rested on: a finalized slot never un-finalizes, so a provider stepped back (a lagging backend, or a liar) — what was read at %d stays (the sends' processed marks, the burns in flight by their slots, a halt), and a reading at the lower slot is short or over by what moved between them: no verdict until the pair answers at or above it", slot, floor, floor)
}

// clientBelow says why a client state answered below the supplies' slot is no reading (E3-GT9), "" when it was
// answered at or above it (client: the lower of the two providers' answers). Before, the lower answer lowered the
// snapshot's slot, so the burns dispatched between it and the supplies' slot — burned in the supply already — were
// counted in flight no more: the reading short by them, one provider's stale answer of an unchanged state enough.
func clientBelow(client, supplies uint64) string {
	if client >= supplies {
		return ""
	}
	return fmt.Sprintf("the client's state was answered at slot %d, below the supplies' slot %d: a provider stepped back (a finalized slot never un-finalizes), and the burns dispatched between the two slots are burned in the supply and would count in flight no more; no verdict until both providers answer at or above the supplies' slot", client, supplies)
}

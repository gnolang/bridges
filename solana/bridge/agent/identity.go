package agent

import (
	"fmt"
	"strings"
	"time"
)

// The supply identity of PLAN.md §5.9, `lock = sentTotal − paidTotal = supply + owed + voided − lost`, holds up to
// messages in flight; a forged credit breaks it by its amount until GovDAO voids it as forged. A signer stops
// signing while it is broken (§5.6: `supply > lock − owed − voided + lost`, the same inequality) and a monitor
// flags credits and payouts; a short right side — a mistaken void, or more in flight than counted — alarms only.
//
// Both evaluate it over an ordered cut: the Solana snapshot first (the supply from two agreeing providers, at a
// slot), then gno.land's state at a height whose time is after the snapshot, with the snapshot's slot at or above
// the dispatch slot of every burn credited in that state; until the order holds the result is no verdict at all.
// Over such a cut the amounts in flight are exact — a send locked and not yet minted, a burn dispatched at or
// before the slot and not yet credited — so the right side plus what is in flight equals the lock to the unit,
// and the break test is that exact sum over the lock (AP1: the tolerant form, the right side alone over the lock,
// let a forged credit hide inside what was in flight — tens of thousands for days under a hold or a full day
// bucket); the short side keeps its tolerance, since a lag only ever makes it short.

// Identity is one route's identity over one cut.
type Identity struct {
	Symbol string
	// gno.land's counters at Height.
	Lock, Owed, Voided, Lost int64
	Height                   int64
	// Solana's synthetic supply at Slot, both providers agreeing.
	Supply int64
	Slot   uint64
	// In flight: sends locked on gno.land whose mint Solana has not recorded at Slot, and burns dispatched on
	// Solana at or before Slot that gno.land has not credited at Height. Both make the right side short, never
	// over, so the verdict tolerates them.
	InflightOut, InflightIn int64
}

// Right is the right side: supply + owed + voided − lost.
func (id Identity) Right() int64 { return id.Supply + id.Owed + id.Voided - id.Lost }

// Gap is lock − right: zero when nothing is in flight, positive by the in-flight amounts, negative on a break.
func (id Identity) Gap() int64 { return id.Lock - id.Right() }

// Inflight is what the right side may fall short by.
func (id Identity) Inflight() int64 { return id.InflightOut + id.InflightIn }

// Verdict is what the identity says.
type Verdict int

const (
	Holds  Verdict = iota // lock = right + in flight
	Broken                // the right side with what is in flight exceeds the lock: a forged credit, or a supply nobody locked for
	Short                 // the right side is short beyond what is in flight: a mistaken void, or a lag — alarm only
)

func (v Verdict) String() string {
	switch v {
	case Holds:
		return "holds"
	case Broken:
		return "BROKEN"
	}
	return "short"
}

// Verdict judges the identity; the text says the numbers. Broken is the cut's exact test, the right side with
// what is in flight over the lock (AP1); Short keeps the tolerance on the other side.
func (id Identity) Verdict() (Verdict, string) {
	gap := id.Gap()
	line := fmt.Sprintf("%s: lock %d = supply %d + owed %d + voided %d − lost %d (right %d, gap %d, in flight %d out + %d in; slot %d, height %d)",
		id.Symbol, id.Lock, id.Supply, id.Owed, id.Voided, id.Lost, id.Right(), gap, id.InflightOut, id.InflightIn, id.Slot, id.Height)
	switch {
	case id.SignerStops():
		return Broken, "the right side with what is in flight exceeds the lock by " + fmt.Sprint(id.Inflight()-gap) + ": " + line
	case gap > id.Inflight():
		return Short, "the right side is short by " + fmt.Sprint(gap-id.Inflight()) + " beyond what is in flight: " + line
	}
	return Holds, line
}

// SignerStops is §5.6's refusal rule, tight, with the burns dispatched at or before the snapshot and not yet
// credited counted as supply (AN3) and the sends locked and not yet minted beside them (AP1: the signer reads
// no sends, so its in-flight-out is 0 and the rule is supply + uncredited > lock − owed − voided + lost; the
// monitors and hlwatch, which read both, judge the same sum): right + in flight > lock. An honest burn in flight
// leaves the sum where the supply was before it; a forged mint burned toward gno.land before any snapshot saw it
// is the uncredited part, and breaks it. A void as forged books the paid part as lost and drops the remainder
// from owed, which closes it.
func (id Identity) SignerStops() bool { return id.Right()+id.Inflight() > id.Lock }

// Cut is the order a snapshot was taken in.
type Cut struct {
	SolSlot   uint64    // the Solana snapshot's slot (the lower of the two providers')
	SolTime   time.Time // when the snapshot was taken
	GnoHeight int64     // the gno state's height
	GnoTime   time.Time // that block's time
	// CreditedAfterSlot are burns credited in the gno state whose dispatch slot is above SolSlot: the snapshot
	// predates them, so the supply still counts what gno.land already owes.
	CreditedAfterSlot []string
}

// Holds says whether the order holds: the gno block is after the snapshot and no burn credited in it was
// dispatched after the snapshot's slot. Until it holds there is no verdict (an alarm if it never does).
func (c Cut) Holds() (bool, string) {
	if !c.GnoTime.After(c.SolTime) {
		return false, fmt.Sprintf("gno.land's block %d (%s) is not after the Solana snapshot (%s)", c.GnoHeight, c.GnoTime.UTC().Format(time.RFC3339), c.SolTime.UTC().Format(time.RFC3339))
	}
	if len(c.CreditedAfterSlot) > 0 {
		return false, fmt.Sprintf("%d burns credited at height %d were dispatched after slot %d: %s", len(c.CreditedAfterSlot), c.GnoHeight, c.SolSlot, strings.Join(c.CreditedAfterSlot, ","))
	}
	return true, ""
}

// SourceVerdict is the third-source rule of §5.9 for a disagreement between gno sources: conflicting block
// hashes at one height flag only once a third source shows the monitor's own node to be the odd one out; a
// lag or an outage never counts; anything less than a third source's word is an alarm.
type SourceVerdict int

const (
	SourcesAgree SourceVerdict = iota
	SourcesLag                 // a source has not reached the height, or did not answer: no comparison
	OwnNodeOdd                 // own ≠ public and the third agrees with the public: flag credits, never payouts
	PublicOdd                  // own ≠ public and the third agrees with own: alarm, the public endpoint is wrong
	Unresolved                 // own ≠ public and no third source to decide, or it disagrees with both: alarm
)

func (v SourceVerdict) String() string {
	return [...]string{"agree", "lag", "OWN NODE ODD", "public odd", "unresolved"}[v]
}

// JudgeSources compares the hashes at one height: own (the monitor's node), public (the first other source) and
// third (the deciding one); "" means the source had no block at that height yet or did not answer.
func JudgeSources(own, public, third string) SourceVerdict {
	if own == "" || public == "" {
		return SourcesLag
	}
	if own == public {
		return SourcesAgree
	}
	switch third {
	case "":
		return Unresolved
	case public:
		return OwnNodeOdd
	case own:
		return PublicOdd
	}
	return Unresolved
}

package agent

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The supply identity of the Solana plan §5.9, carried to Ethereum (eth/bridge/PLAN.md §5.6, §5.9):
// `lock = sentTotal − paidTotal = supply + owed + voided − lost` holds up to the messages in flight; a forged credit
// breaks it by its amount until GovDAO voids it as forged. A signer stops signing while it is broken (§5.6:
// `supply + burns dispatched and not yet credited > lock − owed − voided + lost`) and a monitor flags credits and
// payouts; a short right side — a mistaken void, or more in flight than counted — alarms only.
//
// Both evaluate it over an ordered cut: the Ethereum snapshot first — the `finalized` block F two providers agree
// on, each token's supply pinned to F's hash, the light client's trusted height T at F — then gno.land's state at a
// height whose block time is after the moment the snapshot was taken, with F at or above the block of every burn
// credited in that state; until the order holds there is no verdict at all. Over such a cut the amounts in flight
// are exact — a send locked on gno.land and not yet delivered on Ethereum at F, a burn at or below F not yet
// credited at the height — so the right side plus what is in flight equals the lock to the unit, and the break
// test is that exact sum over the lock (the Solana audit's AP1: the tolerant form let a forged credit hide inside
// what was in flight); the short side keeps its tolerance, since a lag only ever makes it short.

// Identity is one route's identity over one cut.
type Identity struct {
	Symbol string
	// gno.land's counters at Height (the route's, or its line's summed while a migration is open).
	Lock, Owed, Voided, Lost int64
	Height                   int64
	// Ethereum's synthetic supply at Block (the agreed finalized block), both providers agreeing.
	Supply int64
	Block  uint64
	// In flight: sends locked on gno.land whose delivery Ethereum's mailbox has not recorded at Block, and burns at
	// or below Block that gno.land has not credited at Height.
	InflightOut, InflightIn int64
}

// Right is the right side: supply + owed + voided − lost (saturating: a supply past any lock stays past it).
func (id Identity) Right() int64 {
	return satAdd(satAdd(id.Supply, id.Owed), satSub(id.Voided, id.Lost))
}

// Gap is lock − right: zero when nothing is in flight, positive by the in-flight amounts, negative on a break.
func (id Identity) Gap() int64 { return satSub(id.Lock, id.Right()) }

// Inflight is what the right side may fall short by.
func (id Identity) Inflight() int64 { return satAdd(id.InflightOut, id.InflightIn) }

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

// Verdict judges the identity; the text says the numbers.
func (id Identity) Verdict() (Verdict, string) {
	gap := id.Gap()
	line := fmt.Sprintf("%s: lock %d = supply %d + owed %d + voided %d − lost %d (right %d, gap %d, in flight %d out + %d in; block %d, height %d)",
		id.Symbol, id.Lock, id.Supply, id.Owed, id.Voided, id.Lost, id.Right(), gap, id.InflightOut, id.InflightIn, id.Block, id.Height)
	switch {
	case id.SignerStops():
		return Broken, "the right side with what is in flight exceeds the lock by " + fmt.Sprint(satSub(id.Inflight(), gap)) + ": " + line
	case gap > id.Inflight():
		return Short, "the right side is short by " + fmt.Sprint(gap-id.Inflight()) + " beyond what is in flight: " + line
	}
	return Holds, line
}

// SignerStops is §5.6's refusal rule, tight: right + in flight > lock — `supply + uncredited burns + undelivered sends
// > lock − owed − voided + lost` — judged alike by the signers, the monitors and hlwatch, each reading both directions
// over the cut (the plan red team's E2-RT9: a signer that read no sends was loose by every send waiting for its header,
// hours on Ethereum by design, and a forged mint up to their sum passed it). An honest burn or send in flight leaves the
// sum at the lock; a forged mint — still on Ethereum, or burned back toward gno.land before any snapshot saw it —
// breaks it by its own amount. A void as forged books the paid part as lost and drops the remainder from owed, which
// closes it.
func (id Identity) SignerStops() bool { return satAdd(id.Right(), id.Inflight()) > id.Lock }

// identityOf is a token's identity over its route, or over the line's summed counters while a migration is open
// (the Solana audit's AQ3, AR6).
func identityOf(r Route, rj *gnoside.Route, mig *gnoside.Migration, H int64, supply int64, block uint64, inflightOut, inflightIn int64) Identity {
	id := Identity{Symbol: r.Symbol, Height: H, Supply: supply, Block: block, InflightOut: inflightOut, InflightIn: inflightIn}
	if mig != nil {
		id.Lock, id.Owed, id.Voided, id.Lost = mig.Sum()
		return id
	}
	if rj != nil {
		id.Lock, id.Owed, id.Voided, id.Lost = rj.Lock, rj.Owed, rj.Voided, rj.Lost
	}
	return id
}

// supplyUnits is a token's supply (a uint256) as the identity counts it: base units, saturated at the largest
// count the arithmetic keeps exact — a supply past it is past any lock gno.land can hold, which the verdict says.
func supplyUnits(s *big.Int) int64 {
	if s == nil || s.Sign() < 0 {
		return 0
	}
	if !s.IsInt64() || s.Int64() > maxUnits {
		return maxUnits
	}
	return s.Int64()
}

// maxUnits bounds every count the identity adds (a quarter of int64's range: four of them never overflow).
const maxUnits = math.MaxInt64 / 4

func satAdd(a, b int64) int64 {
	s := a + b
	switch {
	case a > 0 && b > 0 && s < 0:
		return math.MaxInt64
	case a < 0 && b < 0 && s >= 0:
		return math.MinInt64
	}
	return s
}

func satSub(a, b int64) int64 {
	if b == math.MinInt64 {
		return satAdd(satAdd(a, math.MaxInt64), 1)
	}
	return satAdd(a, -b)
}

// Cut is the order a snapshot was taken in.
type Cut struct {
	EthBlock  uint64    // the Ethereum snapshot's block: the agreed finalized block
	EthTime   time.Time // when the snapshot was taken (the wall clock at the read)
	GnoHeight int64     // the gno state's height
	GnoTime   time.Time // that block's time
	// CreditedAfterBlock are burns credited in the gno state whose Ethereum block is above EthBlock: the snapshot
	// predates them, so the supply still counts what gno.land already owes.
	CreditedAfterBlock []string
}

// Holds says whether the order holds: the gno block is after the snapshot and no burn credited in it is above the
// snapshot's block. Until it holds there is no verdict (an alarm if it never does).
func (c Cut) Holds() (bool, string) {
	if !c.GnoTime.After(c.EthTime) {
		return false, fmt.Sprintf("gno.land's block %d (%s) is not after the Ethereum snapshot (%s)", c.GnoHeight, c.GnoTime.UTC().Format(time.RFC3339), c.EthTime.UTC().Format(time.RFC3339))
	}
	if len(c.CreditedAfterBlock) > 0 {
		return false, fmt.Sprintf("%d burns credited at height %d are above the snapshot's block %d: %s", len(c.CreditedAfterBlock), c.GnoHeight, c.EthBlock, strings.Join(c.CreditedAfterBlock, ","))
	}
	return true, ""
}

// SourceVerdict is the third-source rule of §5.9 for a disagreement between gno sources: conflicting block hashes at
// one height flag only once a third source shows the monitor's own node to be the odd one out; a lag or an outage
// never counts; anything less than a third source's word is an alarm.
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
	if strings.EqualFold(own, public) {
		return SourcesAgree
	}
	switch {
	case third == "":
		return Unresolved
	case strings.EqualFold(third, public):
		return OwnNodeOdd
	case strings.EqualFold(third, own):
		return PublicOdd
	}
	return Unresolved
}

// aheadVerdict is §5.6's reading of the client's trusted height T when it is above the own node's head.
type aheadVerdict int

const (
	aheadInside   aheadVerdict = iota // T above the head inside the allowance: the identity is judged at the head; never a stop
	aheadOwnLags                      // T past the allowance on the own node, but a second source's head is within it: the own node lags; never a stop
	aheadNoSecond                     // T past the allowance on the own node and no second source answered: nothing to compare; never a stop
	aheadStop                         // T past the allowance on the own node and on a second source: the client is ahead of its chain, the mismatch itself
)

// judgeAhead applies §5.6's stop (pure): T the client's trusted height, own the own node's head (below T), second a
// second source's head (secondOK false when none answered), allowance an hour of blocks. The stop needs T to exceed
// both heads by more than the allowance; anything less is a reading the node cannot give yet.
func judgeAhead(T, own, second uint64, secondOK bool, allowance uint64) (aheadVerdict, string) {
	if T-own <= allowance {
		return aheadInside, fmt.Sprintf("own node's head %d is below the client's trusted height %d (inside the allowance of %d blocks): the identity is judged at the head", own, T, allowance)
	}
	switch {
	case !secondOK:
		return aheadNoSecond, fmt.Sprintf("own node's head %d is %d blocks below the client's trusted height %d, past the allowance of %d, and no second source answered: nothing to judge the client against", own, T-own, T, allowance)
	case T <= second || T-second <= allowance:
		return aheadOwnLags, fmt.Sprintf("own node's head %d is %d blocks below the client's trusted height %d, past the allowance of %d, while the second source's head %d is within it: the own node lags", own, T-own, T, allowance, second)
	}
	return aheadStop, fmt.Sprintf("the client's trusted height %d is more than %d blocks (an hour) past the own node's head %d and the second source's head %d: the client runs ahead of its chain, the mismatch itself", T, allowance, own, second)
}

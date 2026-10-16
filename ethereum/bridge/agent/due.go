package agent

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// When a header is bought, and a batch re-emitted (eth/bridge/PLAN.md §5.5, §5.7, §6.1; budget.go's rule). A header
// for waiting route sends is due only while the fees of the sends it serves — each send's own fee, read beside its
// Dispatch by the scan — cover the whole cost it commits: the header's (the model's gas for the client's set) and
// every delivery's (the model's gas for the leaf each would be proven from), at the live base fee plus the tip a call
// would pay. Failing that, the oldest waiting send's age decides: at six hours the updater buys the header within its
// budget (the fees and the ledger's balance together must cover the cost), and the relayer re-emits the batch (gno
// gas, never ETH) so one header serves it. A waiting decision is always due: GovDAO's decisions never wait for fees,
// and their headers, their deliveries and HlAdmin.execute go under the emergency cap, outside the budget. The prices
// are required (Config.CheckPrices: the updater and the relayer refuse to start without them), so the rule never
// lapses — an empty table once turned it off (round 2's fact check, E2-FC7).

// updateLead is how far before a send's six hours the relayer re-emits its batch (the batch lands, the update follows).
const updateLead = 2 * time.Minute

// dueFacts is what decides whether the waiting messages are due a header.
type dueFacts struct {
	Decisions     int
	Routes        int
	FeesWei       *big.Int // the waiting sends' own fees at the config's prices
	HeaderWei     *big.Int // the header's model cost at the live price
	DeliveriesWei *big.Int // every waiting send's delivery's model cost (by its leaf) at the live price
	CostWei       *big.Int // the whole cost: the header's and the deliveries'
	HeaderGas     uint64
	DeliveryGas   uint64
	Price         *big.Int      // the live base fee plus the tip, wei a gas
	Oldest        time.Duration // the oldest waiting route send's age (from its dispatch block's time)
	Heartbeat     time.Duration // the age at which a waiting send buys its own header within the budget (six hours)
	Lead          time.Duration // how early the age counts (the relayer's re-emit: updateLead)
	// Budget is the updater's ledger balance a six-hour send may draw on; nil where the judge holds no budget: the
	// relayer, whose six-hour batch re-emit costs gno gas only (Reemit), and whose header proof is bought by fees alone.
	Budget *big.Int
	// AgeReemits says the age alone makes the batch due (the relayer's re-emit: the updater then judges its header by
	// the budget).
	AgeReemits bool
	Unpriced   []string // waiting tokens the config prices not (their fees count nothing; CheckPrices leaves none)
	FeeNow     int      // waiting sends counted at their route's fee now (their own fee was never read)
}

// dueFacts gathers them over the waiting messages: each route send's own fee and the model's cost of its delivery from
// the leaf leafOf(t) gives (its Dispatch events and its bytes), the model's cost of the header (headerGas, the client's
// set's), the live price, and the oldest send's age.
func (e *outEnv) dueFacts(ctx context.Context, sender ethSender, latest *ethx.Block, waiting []*outMsg, now time.Time, headerGas uint64, leafOf func(*outMsg) (int, int)) (dueFacts, error) {
	f := dueFacts{FeesWei: new(big.Int), HeaderGas: headerGas, Heartbeat: e.cfg.Heartbeat()}
	unpriced := map[string]bool{}
	var oldest time.Time
	for _, t := range waiting {
		if t.kind == kindDecision {
			f.Decisions++
			continue
		}
		f.Routes++
		if at, ok := e.blockTime(ctx, t.msg.Height); ok && (oldest.IsZero() || at.Before(oldest)) {
			oldest = at
		}
		if t.feeSpent {
			continue // booked once: its fee credited and its delivery debited already (E4-GS4)
		}
		f.DeliveryGas += deliveryGas(leafOf(t))
		if t.rt == nil {
			continue
		}
		fee := t.fee
		if !t.feeKnown {
			rj, err := e.route(ctx, t.rt.Gno)
			if err != nil {
				return f, err
			}
			fee = rj.Fee
			f.FeeNow++
		}
		w, ok := e.cfg.FeeWei(t.rt.Symbol, fee)
		if !ok {
			unpriced[t.rt.Symbol] = true
			continue
		}
		f.FeesWei.Add(f.FeesWei, w)
	}
	for s := range unpriced {
		f.Unpriced = append(f.Unpriced, s)
	}
	if !oldest.IsZero() {
		f.Oldest = now.Sub(oldest)
	}
	price, err := e.price(ctx, sender, latest)
	if err != nil {
		return f, err
	}
	f.Price = price
	f.HeaderWei = weiOf(f.HeaderGas, price)
	f.DeliveriesWei = weiOf(f.DeliveryGas, price)
	f.CostWei = new(big.Int).Add(f.HeaderWei, f.DeliveriesWei)
	return f, nil
}

// price is the live base fee plus the tip a call would pay (the tip read once a step).
func (e *outEnv) price(ctx context.Context, sender ethSender, latest *ethx.Block) (*big.Int, error) {
	if e.tip == nil {
		tip, err := sender.Tip(ctx)
		if err != nil || tip == nil {
			tip = new(big.Int)
		}
		e.tip = tip
	}
	base := latest.BaseFee
	if base == nil {
		base = new(big.Int)
	}
	return new(big.Int).Add(base, e.tip), nil
}

// headerDue says whether the waiting messages are due a header now, whether it draws on the budget, and why.
func headerDue(f dueFacts) (due bool, draw bool, why string) {
	cost := func() string {
		return fmt.Sprintf("%s ETH: the header %s (%d gas) and the deliveries %s (%d gas) at %s gwei", eth(f.CostWei), eth(f.HeaderWei), f.HeaderGas, eth(f.DeliveriesWei), f.DeliveryGas, gwei(f.Price))
	}
	aged := f.Oldest >= f.Heartbeat-f.Lead
	switch {
	case f.Decisions > 0:
		return true, false, fmt.Sprintf("%d decision(s) wait: a decision never waits for fees (outside the budget, the emergency cap)", f.Decisions)
	case f.Routes == 0:
		return false, false, "nothing waits"
	case f.FeesWei.Cmp(f.CostWei) >= 0:
		return true, false, fmt.Sprintf("the waiting sends' own fees (%s ETH) cover the whole cost (%s)", eth(f.FeesWei), cost())
	case aged && f.AgeReemits:
		return true, false, fmt.Sprintf("a send has waited %s: its batch is re-emitted, and the updater buys its header within its budget", f.Oldest.Round(time.Minute))
	case aged && f.Budget != nil && new(big.Int).Add(f.FeesWei, f.Budget).Cmp(f.CostWei) >= 0:
		return true, true, fmt.Sprintf("a send has waited %s: its fees (%s ETH) and the budget (%s ETH) cover the whole cost (%s)", f.Oldest.Round(time.Minute), eth(f.FeesWei), eth(f.Budget), cost())
	}
	why = fmt.Sprintf("the waiting sends' fees (%s ETH) do not cover the whole cost (%s)", eth(f.FeesWei), cost())
	switch {
	case aged && f.Budget != nil:
		why += fmt.Sprintf("; a send has waited %s and the budget (%s ETH) does not cover what its fees lack: it waits (delivery:age alarms at %s)", f.Oldest.Round(time.Minute), eth(f.Budget), DeliveryAgeAfter)
	case aged:
		why += "; a send has waited six hours, but a header proof is bought by its fees alone"
	default:
		why += fmt.Sprintf("; waits for a lower base fee, more sends to share the header, or the oldest send's %s (in %s)", f.Heartbeat, (f.Heartbeat - f.Lead - f.Oldest).Round(time.Minute))
	}
	if len(f.Unpriced) > 0 {
		why += "; no price for " + strings.Join(f.Unpriced, ", ") + " (its fees count nothing)"
	}
	if f.FeeNow > 0 {
		why += fmt.Sprintf("; %d send(s) counted at their route's fee now (their own was not read)", f.FeeNow)
	}
	return false, false, why
}

// eth writes wei as ETH.
func eth(w *big.Int) string {
	if w == nil {
		return "0"
	}
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(w), big.NewFloat(1e18)).Float64()
	return fmt.Sprintf("%.6f", f)
}

// gwei writes wei a gas as gwei.
func gwei(w *big.Int) string {
	if w == nil {
		return "0"
	}
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(w), big.NewFloat(1e9)).Float64()
	return fmt.Sprintf("%.3f", f)
}

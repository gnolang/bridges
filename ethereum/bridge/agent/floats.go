package agent

import (
	"context"
	"fmt"
	"math/big"
	"time"
)

// The floats of eth/bridge/PLAN.md §5.5, each monitored at 7 days of the trailing rate: the relayers' ETH key
// (primary and backup share it), the updater's ETH key, the relayers' gno gas key, the guardians' and watch keys'
// ETH. The rate is the spend observed over the trailing day, never below the planned launch-day rate, so a quiet
// day does not hide a float that the first busy day would empty (the Solana agent's rule, carried).
//
// The planned rates are E-M2b's measured model (budget.go), not the placeholders they once were (plan red team's
// E3-CF8(b): the updater's 30 × 6,000,000 gas read the plan's 0.2 ETH as 5.6 days, so runway:updater-eth stood from
// launch): 50 deliveries a day at the model's 311,300 gas, 30 updates a day at the model's header of gnoland-1's set
// (1,448,600 gas: six validators, five signatures), both at 0.2 gwei — above the 30-day median base fee of 0.128
// gwei, below its p95 — and 50 Ethereum → gno.land credits a day at creditGnoUgnot (≈1.8 GNOT: the storage deposit a
// credit locks, measured, and its gas — once 0.11 GNOT, the Solana plan's unmeasured guess, which read the 500 GNOT key
// as 90 days where it lasts ≈6: E5-GS5, E5-RT5); a deployment sets its own in the config's `runway`, in wei or ugnot a
// day. The observed trailing rate replaces a planned one it exceeds.

// creditGnoUgnot is what one Ethereum → gno.land credit costs the relayers' gno key (E5-GS5; the plan red team's
// E5-RT5): the storage deposit its Process locks at 100 ugnot a byte — measured on gno v1.5.1 nodes at 0.87 GNOT for one
// claim to a fresh recipient (audit round 5's claim/run-gnot.out), 1.28 GNOT a credit in 200-credit batches before the
// waiting index and 1.70 with it (fold F15d's ab2 runs) — the most of them, since none comes back to the key (a paid
// claim keeps its record; a release refunds the storage fee collector while ugnot is restricted), and ≈0.1 GNOT of its
// Process's and Claim's gas (GasFor at gnoland-1's 0.001 ugnot a gas).
const creditGnoUgnot = 1_800_000

const (
	runwayDays   = 7
	runwayWindow = 24 * time.Hour
	runwayMinObs = time.Hour

	weiPerETH    = 1e18
	ugnotPerGNOT = 1_000_000
)

var plannedPerDay = map[string]float64{
	"relayer-eth": 50 * deliveryGasOne * 0.2e9,
	"updater-eth": 30 * float64(headerGas(6, 5)) * 0.2e9,
	"relayer-gno": 50 * creditGnoUgnot,
	"watch-eth":   2 * flagGas * 0.2e9, // a flag and its clear a day at most, at rest nothing
}

// A flag key's float (PLAN.md §5.5; round 2's fact check, E2-FC3): each guardian's and watch key's ETH, 0.05 apiece. A
// flag is one transaction per object — 101,693–112,448 gas measured for the gate's setFlag (cold, a whole
// transaction), 105,675 for HlAdmin's — and ethx's sender must hold the estimate plus 20% at twice the base fee plus
// the tip, so a key sends a gate flag only while its balance covers 1.2 × 112,448 gas at twice the base fee: 0.05
// ETH up to ≈185 gwei (the 30-day maximum was 24.9) and ≈270 flags at the 30-day p95; 0.01 ETH none above ≈37 gwei,
// in the spike in which flags must never park.
const (
	FlagKeyFloatWei = 5e16    // 0.05 ETH
	flagGas         = 112_448 // the gate's setFlag, the costlier object
)

// FlagCeilingGwei is the highest base fee at which a balance still sends one gate flag through ethx's sender (the
// estimate plus 20% at twice the base fee; the tip left out).
func FlagCeilingGwei(balanceWei float64) float64 { return balanceWei / (1.2 * flagGas * 2) / 1e9 }

// flagKeyText says a flag key's float in one line, and whether it is short of FlagKeyFloatWei.
func flagKeyText(who string, addr string, balanceWei float64) (string, bool) {
	text := fmt.Sprintf("%s %s holds %.6f ETH: it sends a gate flag (%d gas) while the base fee is under ≈%.0f gwei", who, addr, balanceWei/weiPerETH, flagGas, FlagCeilingGwei(balanceWei))
	if balanceWei < FlagKeyFloatWei {
		return text + fmt.Sprintf(" — under the %.2f ETH a flag key keeps (≈%.0f gwei): top it up", FlagKeyFloatWei/weiPerETH, FlagCeilingGwei(FlagKeyFloatWei)), true
	}
	return text, false
}

// The floors of the two keys that pay decisions' headers (PLAN.md §5.5, §11.4; the plan red team's E2-RT8, E4-RT1, the
// fact check's E4-FC5): each keeps what one decision's header needs at the emergency cap — the sender holds the
// estimate plus 20% at twice the base fee, so a header of gnoland-1's set (1,448,600 gas) at 50 gwei needs ≈0.174 ETH
// (EmergencyHeaderWei; the set after the client's trusted height prices it) — and its spending on top: the updater's
// route headers (the planned 30 a day), the relayers' deliveries. GovDAO's cancels must land in a spike: the updater's
// key buys a decision's header, and when it cannot pay, the relayers' key does (relayer_out.go, decisionBackstop), so
// each floor alarms on its own key — float:updater and float:relayer — under that need, never under a figure the
// route traffic spends through from its first header (E4-FC5).

// EmergencyHeaderWei is what a header of the gas given needs through ethx's sender at the emergency cap: the estimate
// plus 20% at twice the base fee (the tip left out).
func EmergencyHeaderWei(gas uint64, emergency *big.Int) *big.Int {
	w := new(big.Int).Mul(new(big.Int).SetUint64(gas*12/10), emergency)
	return w.Mul(w, big.NewInt(2))
}

// HeaderCeilingGwei is the highest base fee at which a balance still sends a header of the gas given through ethx's
// sender (the estimate plus 20% at twice the base fee; the tip left out).
func HeaderCeilingGwei(balanceWei float64, gas uint64) float64 {
	return balanceWei / (1.2 * float64(gas) * 2) / 1e9
}

// floorText says a key's floor in one line, and whether it is short of a decision's header at the emergency cap.
func floorText(who, addr string, balance *big.Int, gas uint64, emergency *big.Int, short string) (string, bool) {
	bal, _ := new(big.Float).SetInt(balance).Float64()
	capGwei, _ := new(big.Float).Quo(new(big.Float).SetInt(emergency), big.NewFloat(1e9)).Float64()
	need, _ := new(big.Float).SetInt(EmergencyHeaderWei(gas, emergency)).Float64()
	text := fmt.Sprintf("%s %s holds %.6f ETH: it sends a decision's header (≈%d gas) while the base fee is under ≈%.0f gwei (the emergency cap is %.0f)", who, addr, bal/weiPerETH, gas, HeaderCeilingGwei(bal, gas), capGwei)
	if balance.Cmp(EmergencyHeaderWei(gas, emergency)) < 0 {
		return text + fmt.Sprintf(" — under the %.3f ETH a decision's header needs at the cap: top it up (%s)", need/weiPerETH, short), true
	}
	return text, false
}

// updaterFloatText says the updater's floor in one line, and whether it is short of a decision's header at the
// emergency cap (float:updater).
func updaterFloatText(addr string, balance *big.Int, gas uint64, emergency *big.Int) (string, bool) {
	return floorText("the updater's key", addr, balance, gas, emergency, "meanwhile the relayers' key buys a waiting decision's header the updater cannot")
}

// relayerFloatText says the relayers' floor in one line, and whether it is short of a decision's header at the
// emergency cap (float:relayer, E4-RT1): the backstop that buys a waiting decision's header when the updater's key cannot
// would find nothing to pay with — the deliveries' runway (runway:relayer-eth) cannot see it, one such header being
// weeks of deliveries.
func relayerFloatText(addr string, balance *big.Int, gas uint64, emergency *big.Int) (string, bool) {
	return floorText("the relayers' key", addr, balance, gas, emergency, "it is the backstop for a decision's header the updater's key cannot buy")
}

// sample is one reading of a float's balance.
type sample struct {
	At      time.Time `json:"at"`
	Balance float64   `json:"balance"`
}

// floatKind is the planned-rate key of a float by its name ("relayer-eth-backup" → "relayer-eth").
func floatKind(name string) string {
	for k := range plannedPerDay {
		if len(name) >= len(k) && name[:len(k)] == k {
			return k
		}
	}
	return name
}

// addSample appends a reading and drops what is older than the window.
func addSample(samples []sample, s sample) []sample {
	samples = append(samples, s)
	cut := s.At.Add(-runwayWindow)
	i := 0
	for i < len(samples)-1 && samples[i].At.Before(cut) {
		i++
	}
	return samples[i:]
}

// observedPerDay is the spend over the samples, scaled to a day: the sum of every decrease (a top-up counts for
// nothing); 0 when the observation is too short to say.
func observedPerDay(samples []sample) float64 {
	if len(samples) < 2 {
		return 0
	}
	span := samples[len(samples)-1].At.Sub(samples[0].At)
	if span < runwayMinObs {
		return 0
	}
	var spent float64
	for i := 1; i < len(samples); i++ {
		if d := samples[i-1].Balance - samples[i].Balance; d > 0 {
			spent += d
		}
	}
	return spent * float64(24*time.Hour) / float64(span)
}

// runway is a float's days left at the trailing rate (planned at least), and the rate used.
func runway(name string, samples []sample, override map[string]float64) (days float64, rate float64) {
	rate = plannedPerDay[floatKind(name)]
	if o, ok := override[name]; ok && o > 0 {
		rate = o
	}
	if obs := observedPerDay(samples); obs > rate {
		rate = obs
	}
	if len(samples) == 0 {
		return 0, rate
	}
	bal := samples[len(samples)-1].Balance
	if rate <= 0 {
		return 1e9, 0
	}
	return bal / rate, rate
}

// runwayText says a float's state in one line.
func runwayText(name string, balance float64, days, rate float64) string {
	if floatKind(name) == "relayer-gno" {
		return fmt.Sprintf("%s holds %.0f ugnot, %.1f days at %.0f ugnot/day (alarm under %d)", name, balance, days, rate, runwayDays)
	}
	return fmt.Sprintf("%s holds %.6f ETH, %.1f days at %.6f ETH/day (alarm under %d)", name, balance/weiPerETH, days, rate/weiPerETH, runwayDays)
}

// Float is one float a machine watches: its samples and the alarm when its runway is under 7 days.
type Float struct {
	Name    string   `json:"name"`
	Samples []sample `json:"samples"`
}

// Observe adds a reading and answers the runway's line and whether it is short.
func (f *Float) Observe(at time.Time, balance float64, override map[string]float64) (text string, short bool) {
	f.Samples = addSample(f.Samples, sample{At: at, Balance: balance})
	days, rate := runway(f.Name, f.Samples, override)
	return runwayText(f.Name, balance, days, rate), days < runwayDays
}

// floatEvery is how often a machine reads its own key's balance.
const floatEvery = 5 * time.Minute

// floatWatch is a machine's watch of its own key's float (the relayers' and the updater's ETH, §5.5): a reading every
// few minutes, the runway at the trailing rate, the alarm runway:<name> under 7 days. The monitor watches every float
// of the deployment besides.
type floatWatch struct {
	f    Float
	next time.Time
	line string
	bal  *big.Int // the last reading (nil before one)
}

func (w *floatWatch) watch(ctx context.Context, now time.Time, name string, read func(context.Context) (*big.Int, error), override map[string]float64, alarm *Alarmer) string {
	if now.Before(w.next) {
		return w.line
	}
	w.next = now.Add(floatEvery)
	bal, err := read(ctx)
	if err != nil {
		w.line = "the balance could not be read: " + err.Error()
		return w.line
	}
	w.bal = bal
	f, _ := new(big.Float).SetInt(bal).Float64()
	w.f.Name = name
	text, short := w.f.Observe(now, f, override)
	if short {
		alarm.Alarm("runway:"+name, text)
	} else {
		alarm.Clear("runway:" + name)
	}
	w.line = text
	return text
}

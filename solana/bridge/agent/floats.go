package agent

import (
	"fmt"
	"time"
)

// The five floats of PLAN.md §5.5, each monitored at 7 days of the trailing rate: the relayers' SOL key, the two
// tokens' ATA payers, the relayers' gno gas key and the updater's SOL key. The rate is the spend observed over
// the trailing day, never below the planned launch-day rate (50 transfers a day), so a quiet day does not hide a
// float that the first busy day would empty.

const (
	runwayDays   = 7
	runwayWindow = 24 * time.Hour
	runwayMinObs = time.Hour // observations shorter than this say nothing about the rate

	lamportsPerSOL = 1_000_000_000
	ugnotPerGNOT   = 1_000_000
)

// plannedPerDay is the launch-day planned spend of each float, in its base unit a day (§5.5's figures: 0.00095
// SOL a delivery record, 0.0015 SOL a first-time recipient's token account, relayerGnoPerTransfer a Solana → gno
// transfer; the updater's four transactions of 5,000 lamports a header at a header every ten minutes — 0.00288 SOL a
// day, so its 0.3 SOL is ≈3.4 months, as §5.5 measures it; the earlier "0.3 SOL over five months" sat 30% below that).
var plannedPerDay = map[string]float64{
	"relayer-sol": 50 * 0.00095 * lamportsPerSOL,
	"ata-payer":   25 * 0.0015 * lamportsPerSOL,
	"relayer-gno": 50 * relayerGnoPerTransfer * ugnotPerGNOT,
	"updater-sol": 144 * 4 * 5_000,
}

// relayerGnoPerTransfer is what one Solana → gno transfer costs the relayers' gno key, in GNOT (E5-GS5's Solana side,
// measured on a gno v1.5.1 node with the routes at mainnet's values, lane s6): its Process — 44M to 60M gas, ≈0.07 GNOT at
// the floor price — the storage deposit its credit locks — ≈1.49 GNOT for a claim to a fresh recipient, ≈1.00 to one
// whose queue is open, ≈0.76 for a sweep entry, of which ≈0.31 comes back with the Claim that pays a claim in full (a
// runway counts the spend, never the refund) — and its Claims at E5-GS1's cadence (≈0.15 GNOT for a small claim, ≈0.55
// for 1,000 GNOT to a fresh recipient). §5.5 planned ≈0.11 GNOT, the Process alone: at 50 transfers a day the key spends
// ≈93 GNOT a day, so the runway's seven days need ≈650 GNOT. A burn below its route's fee costs it nothing: the relayer
// leaves it to whoever pays for it (E5-GS5).
const relayerGnoPerTransfer = 0.07 + 1.49 + 0.3

// sample is one reading of a float's balance.
type sample struct {
	At      time.Time `json:"at"`
	Balance int64     `json:"balance"`
}

// floatKind is the planned-rate key of a float by its name ("ata-payer-BUBBLE" → "ata-payer").
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

// observedPerDay is the spend over the samples, scaled to a day: the sum of every decrease (a top-up is an
// increase and counts for nothing). It is 0 when the observation is too short to say.
func observedPerDay(samples []sample) float64 {
	if len(samples) < 2 {
		return 0
	}
	span := samples[len(samples)-1].At.Sub(samples[0].At)
	if span < runwayMinObs {
		return 0
	}
	var spent int64
	for i := 1; i < len(samples); i++ {
		if d := samples[i-1].Balance - samples[i].Balance; d > 0 {
			spent += d
		}
	}
	return float64(spent) * float64(24*time.Hour) / float64(span)
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
	bal := float64(samples[len(samples)-1].Balance)
	if rate <= 0 {
		return 1e9, 0
	}
	return bal / rate, rate
}

// runwayText says a float's state in one line.
func runwayText(name string, balance int64, days, rate float64) string {
	unit := "lamports"
	if floatKind(name) == "relayer-gno" {
		unit = "ugnot"
	}
	return fmt.Sprintf("%s holds %d %s, %.1f days at %.0f %s/day (alarm under %d)", name, balance, unit, days, rate, unit, runwayDays)
}

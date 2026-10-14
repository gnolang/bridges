package agent

import (
	"strings"
	"testing"
	"time"
)

// The identity (PLAN.md §5.9): lock = supply + owed + voided − lost, up to in-flight messages; the right side
// over the lock is a break, under it beyond what is in flight an alarm.
func TestIdentityVerdict(t *testing.T) {
	cases := []struct {
		name string
		id   Identity
		want Verdict
	}{
		{"closed", Identity{Lock: 300, Supply: 300}, Holds},
		{"a claim credited and unpaid", Identity{Lock: 500, Supply: 300, Owed: 200}, Holds},
		{"a withheld void", Identity{Lock: 500, Supply: 300, Voided: 200}, Holds},
		{"a forged void after a payout", Identity{Lock: 250, Supply: 300, Lost: 50}, Holds},
		{"a forged credit", Identity{Lock: 300, Supply: 300, Owed: 200}, Broken},
		{"supply nobody locked for", Identity{Lock: 300, Supply: 301}, Broken},
		{"a send in flight", Identity{Lock: 400, Supply: 300, InflightOut: 100}, Holds},
		{"a burn in flight", Identity{Lock: 300, Supply: 200, InflightIn: 100}, Holds},
		{"short beyond what is in flight", Identity{Lock: 400, Supply: 300, InflightOut: 50}, Short},
		{"a mistaken void as forged", Identity{Lock: 300, Supply: 300, Lost: 100}, Short},
		// AP1: a forged credit inside what is in flight is a break — the exact sum, not the tolerant right side
		{"a forged credit hidden by a send in flight", Identity{Lock: 400, Supply: 300, Owed: 100, InflightOut: 100}, Broken},
		{"a forged credit hidden by a burn in flight", Identity{Lock: 300, Supply: 200, Owed: 100, InflightIn: 100}, Broken},
		{"a forged credit of one unit under a hold's sends in flight", Identity{Lock: 60_000, Supply: 0, Owed: 1, InflightOut: 60_000}, Broken},
	}
	for _, c := range cases {
		v, text := c.id.Verdict()
		if v != c.want {
			t.Errorf("%s: %s, want %s (%s)", c.name, v, c.want, text)
		}
		if (v == Broken) != c.id.SignerStops() {
			t.Errorf("%s: the signer's rule (%v) and the verdict (%s) disagree", c.name, c.id.SignerStops(), v)
		}
	}
}

// §5.6: a forgery stops the signers; GovDAO's void as forged drops the unpaid remainder from owed and books the
// paid part as lost, and they resume.
func TestSignerRefusalAndResume(t *testing.T) {
	honest := Identity{Lock: 300, Supply: 300}
	if honest.SignerStops() {
		t.Fatal("stops with nothing wrong")
	}
	forged := honest
	forged.Owed += 200 // a credit with no burn behind it
	if !forged.SignerStops() {
		t.Fatal("does not stop on a forged credit")
	}
	paid := forged
	paid.Owed -= 50 // 50 of it paid out before anyone looked
	paid.Lock -= 50
	if !paid.SignerStops() {
		t.Fatal("does not stop after part of the forgery was paid")
	}
	voided := paid
	voided.Owed -= 150 // the remainder dropped
	voided.Lost += 50  // the paid part booked as lost
	if voided.SignerStops() {
		t.Fatalf("still stops after the void as forged: %+v", voided)
	}
	if v, _ := voided.Verdict(); v != Holds {
		t.Fatalf("after the void: %s", v)
	}
	withheld := paid
	withheld.Owed -= 150
	withheld.Voided += 150 // a void as withheld keeps the remainder booked: the identity stays broken
	if !withheld.SignerStops() {
		t.Fatal("a void as withheld must not resume the signers")
	}
}

func TestCutHolds(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	c := Cut{SolSlot: 100, SolTime: t0, GnoHeight: 50, GnoTime: t0.Add(2 * time.Second)}
	if ok, why := c.Holds(); !ok {
		t.Fatal(why)
	}
	c.GnoTime = t0
	if ok, _ := c.Holds(); ok {
		t.Fatal("a gno block at the snapshot's time is not after it")
	}
	c.GnoTime = t0.Add(time.Second)
	c.CreditedAfterSlot = []string{"ab"}
	if ok, why := c.Holds(); ok || !strings.Contains(why, "dispatched after slot 100") {
		t.Fatalf("a burn credited from after the slot must break the order: %v %s", ok, why)
	}
}

func TestJudgeSources(t *testing.T) {
	cases := []struct {
		own, public, third string
		want               SourceVerdict
	}{
		{"a", "a", "", SourcesAgree},
		{"a", "a", "b", SourcesAgree},
		{"a", "", "a", SourcesLag},
		{"", "a", "a", SourcesLag},
		{"a", "b", "b", OwnNodeOdd},
		{"a", "b", "a", PublicOdd},
		{"a", "b", "", Unresolved},
		{"a", "b", "c", Unresolved},
	}
	for _, c := range cases {
		if got := JudgeSources(c.own, c.public, c.third); got != c.want {
			t.Errorf("own %q public %q third %q: %s, want %s", c.own, c.public, c.third, got, c.want)
		}
	}
}

func TestProviderPairs(t *testing.T) {
	mk := func(a, b string) *Config {
		c := &Config{}
		c.Solana.RPCs = []string{a, b}
		return c
	}
	ok := map[string]*Config{"s1": mk("A", "B"), "s2": mk("B", "C"), "s3": mk("C", "D")}
	if err := ProviderPairs(ok); err != nil {
		t.Fatal(err)
	}
	shared := map[string]*Config{"s1": mk("A", "B"), "s2": mk("A", "B"), "s3": mk("C", "D")}
	if err := ProviderPairs(shared); err == nil || !strings.Contains(err.Error(), "shared") {
		t.Fatalf("a shared pair passed: %v", err)
	}
	three := map[string]*Config{"s1": mk("A", "B"), "s2": mk("A", "C"), "s3": mk("A", "D")}
	if err := ProviderPairs(three); err == nil || !strings.Contains(err.Error(), "at most two") {
		t.Fatalf("a provider in three signers passed: %v", err)
	}
	same := map[string]*Config{"s1": mk("A", "A"), "s2": mk("B", "C")}
	if err := ProviderPairs(same); err == nil {
		t.Fatal("the same provider twice passed")
	}
}

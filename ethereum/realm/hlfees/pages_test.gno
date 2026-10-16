package hlfees

import (
	"testing"

	"gno.land/p/nt/testutils/v0"
)

// The render-gas probes of the pages the machines read here (E4-RL2): json, and decisions.json/<from> at its bound
// for this realm — twenty decisions, as many as a page holds, of its one builder's text (set treasurer). The file runs
// after hlfees_test.gno.

var gasFirst int64 // the first of the twenty

func TestFeesPagesSetup(cur realm, t *testing.T) {
	gasFirst = ledger.Last() + 1
	for i := 0; i < 20; i++ {
		pass(cur, NewSetTreasurerRequest(cross(cur), testutils.TestAddress("hl-fees-probe")))
	}
	if ledger.Last() != gasFirst+19 {
		t.Fatalf("the twenty: ledger at %d", ledger.Last())
	}
}

// TestRenderGasFeesJSON: the json the machines read r/hlfees by.
func TestRenderGasFeesJSON(cur realm, t *testing.T) {
	if p := Render("json"); len(p) < 200 {
		t.Fatalf("json: %s", p)
	}
}

// TestRenderGasFeesDecisions: decisions.json/<from>, a full page of twenty.
func TestRenderGasFeesDecisions(cur realm, t *testing.T) {
	if p := Render("decisions.json/" + itoa(gasFirst)); len(p) < 20*150 {
		t.Fatalf("decisions.json/%d: %d bytes", gasFirst, len(p))
	}
}

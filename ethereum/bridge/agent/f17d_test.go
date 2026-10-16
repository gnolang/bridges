package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// Round 5's fold lane F17d in the agent: the relayer's executions on gno.land sized from their simulation.

// gasGno is the fake chain behind the gno client's gas rule (gno.Client.Call): a call is simulated under the gas it is
// given and once more under four times it within the block's gas; one that needs more runs out of gas in its
// simulation, nothing sent.
type gasGno struct {
	*fakeGno
	need  map[string]int64 // "pkg.fn" → the gas it uses
	asked map[string][]int64
}

func (g *gasGno) Call(ctx context.Context, pkg, fn string, gas int64, args ...string) (*gno.Result, error) {
	g.asked[pkg+"."+fn] = append(g.asked[pkg+"."+fn], gas)
	reach := gas
	if 4*gas <= gno.MaxBlockGas {
		reach = 4 * gas
	}
	if need := g.need[pkg+"."+fn]; need > reach {
		return nil, &gno.CallError{Pkg: pkg, Fn: fn, Err: errors.New("exit status 1"), Output: "--= Error =--\nData: out of gas in location: CPUCycles\n"}
	}
	return g.fakeGno.Call(ctx, pkg, fn, gas, args...)
}

// realms.md (audit round 5, "for the lead"): the relayer called a due decision's Execute under its -gas, 45M — the
// client simulating it under 45M and 180M — so a decision costing more (a forged restore of 256 ids, ≈2.53B on a gno
// v1.5.1 node) ran out of gas in its simulation every tick and was applied by nobody but a member's own call. Now its
// ceiling is raised past the client's reach as far as the block's gas, and the broadcast sized from the simulation
// that fit: applied. A GovDAO proposal's ExecuteOrRejectProposal goes the same way (tryCall's gasUpToTheBlock).
func TestExecutorSizesAnExecuteFromItsSimulation(t *testing.T) {
	w := newWorld(t, 100, "primary")
	now := w.g.timeAt(w.g.head).Unix()
	w.g.renders[testGNOT+":decisions.json"] = fmt.Sprintf(`[{"id":7,"kind":"restore","state":"pending","made":%d,"recorded":%d,"due":%d}]`, now-100, now-100, now-10)
	g := &gasGno{fakeGno: w.g, need: map[string]int64{testGNOT + ".Execute": 2_527_554_200}, asked: map[string][]int64{}}
	w.r.gno = g
	w.tick()
	if calls := w.g.callsOf(testGNOT + ".Execute"); len(calls) != 1 || calls[0] != testGNOT+".Execute(7)" {
		t.Fatalf("a due decision costing 2.53B: executed %v (asked %v); %s", calls, g.asked[testGNOT+".Execute"], w.r.last["gnoexec:"+testGNOT+"#7"])
	}
	if got := fmt.Sprint(g.asked[testGNOT+".Execute"]); got != "[45000000 720000000]" {
		t.Fatalf("the ceilings asked: %s (want 45M, then 720M: the client reaching 2.88B)", got)
	}
	if !strings.Contains(w.r.last["gnoexec:"+testGNOT+"#7"], "applied") {
		t.Fatalf("decision 7: %s", w.r.last["gnoexec:"+testGNOT+"#7"])
	}
}

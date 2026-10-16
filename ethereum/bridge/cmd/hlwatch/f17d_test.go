package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// gasNode is a gno node behind the client's gas rule (gno.Client.Call): a call is simulated under the gas it is given
// and once more under four times it within the block's gas; one that needs more runs out of gas, nothing sent.
type gasNode struct {
	need  map[string]int64 // fn → the gas it uses
	asked map[string][]int64
	sent  []string
}

func (g *gasNode) RenderAt(context.Context, string, string, int64) (string, error) { return "", nil }
func (g *gasNode) Status(context.Context) (gno.Head, error)                        { return gno.Head{}, nil }
func (g *gasNode) Block(context.Context, int64) (gno.Head, error)                  { return gno.Head{}, nil }

func (g *gasNode) Call(_ context.Context, pkg, fn string, gas int64, args ...string) (*gno.Result, error) {
	g.asked[fn] = append(g.asked[fn], gas)
	reach := gas
	if 4*gas <= gno.MaxBlockGas {
		reach = 4 * gas
	}
	if g.need[fn] > reach {
		return nil, &gno.CallError{Pkg: pkg, Fn: fn, Err: errors.New("exit status 1"), Output: "--= Error =--\nData: out of gas in location: CPUCycles\n"}
	}
	g.sent = append(g.sent, fmt.Sprintf("%s %v", fn, args))
	return &gno.Result{Hash: "TX" + fn}, nil
}

// realms.md (audit round 5, "for the lead"): hlwatch's flag sent SetFlag at 45M — the client simulating it under 45M
// and 180M — and a guardian's payouts flag naming 256 ids costs ≈555M on a gno v1.5.1 node (64 ids ≈156M): the tool
// set ≈70 ids at most, and a flag naming more ran out of gas. Now the call is sized from its simulation as far as the
// block's gas; a clear the same way.
func TestFlagGnoSizesTheCallFromItsSimulation(t *testing.T) {
	g := &gasNode{need: map[string]int64{"SetFlag": 555_610_597, "ClearFlag": 2_000_000}, asked: map[string][]int64{}}
	r, err := flagGno(context.Background(), g, "gno.land/r/x/eth/hlmailbox", true, "gno.land/r/x/eth/hlbubble", "payouts", "1,2,3")
	if err != nil || r == nil || r.Hash != "TXSetFlag" || fmt.Sprint(g.asked["SetFlag"]) != "[45000000 720000000]" {
		t.Fatalf("a payouts flag naming 256 ids (≈555M): %v %v, asked %v", r, err, g.asked["SetFlag"])
	}
	if len(g.sent) != 1 || g.sent[0] != "SetFlag [gno.land/r/x/eth/hlbubble payouts 1,2,3]" {
		t.Fatalf("what was sent: %v", g.sent)
	}
	if r, err := flagGno(context.Background(), g, "gno.land/r/x/eth/hlmailbox", false, "gno.land/r/x/eth/hlbubble", "payouts", ""); err != nil || r.Hash != "TXClearFlag" || fmt.Sprint(g.asked["ClearFlag"]) != "[45000000]" {
		t.Fatalf("the clear: %v %v, asked %v", r, err, g.asked["ClearFlag"])
	}
}

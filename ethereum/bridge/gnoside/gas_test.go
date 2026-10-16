package gnoside

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// gasNode is a gno node behind the client's gas rule (gno.Client.Call): a call is simulated under the gas it is
// given and once more under four times it within the block's gas; one that needs more runs out of gas, nothing sent.
type gasNode struct {
	need  int64 // the gas the call uses
	fail  error // any other failure, when set
	asked []int64
}

func (g *gasNode) RenderAt(context.Context, string, string, int64) (string, error) { return "", nil }
func (g *gasNode) Status(context.Context) (gno.Head, error)                        { return gno.Head{}, nil }
func (g *gasNode) Block(context.Context, int64) (gno.Head, error)                  { return gno.Head{}, nil }

func (g *gasNode) Call(_ context.Context, pkg, fn string, gas int64, _ ...string) (*gno.Result, error) {
	g.asked = append(g.asked, gas)
	if g.fail != nil {
		return nil, g.fail
	}
	if g.need > Reach(gas) {
		return nil, &gno.CallError{Pkg: pkg, Fn: fn, Err: errors.New("exit status 1"), Output: "--= Error =--\nData: out of gas in location: CPUCycles\n"}
	}
	return &gno.Result{Hash: "tx", GasUsed: g.need}, nil
}

// A call whose gas only its simulation knows is raised past the client's reach as far as the block's gas (realms.md,
// audit round 5: a 256-id payouts flag at ≈555M, a 256-id forged restore's Execute at ≈2.53B); one past the block's
// fails at the block's; any other failure is the answer at once.
func TestCallUpToTheBlockRaisesTheCeiling(t *testing.T) {
	for _, c := range []struct {
		need  int64
		asked string
		ok    bool
	}{
		{10_000_000, "[45000000]", true},
		{180_000_000, "[45000000]", true},
		{555_610_597, "[45000000 720000000]", true},
		{2_527_554_200, "[45000000 720000000]", true},
		{2_900_000_000, "[45000000 720000000 3000000000]", true},
		{3_000_000_001, "[45000000 720000000 3000000000]", false},
	} {
		g := &gasNode{need: c.need}
		res, err := CallUpToTheBlock(context.Background(), g, "gno.land/r/x/eth/hlmailbox", "SetFlag", 45_000_000, "a", "payouts", "1,2")
		if (err == nil) != c.ok || fmt.Sprint(g.asked) != c.asked || (c.ok && res == nil) || (!c.ok && !OutOfGas(err)) {
			t.Fatalf("a call using %d: asked %v (want %s), %v", c.need, g.asked, c.asked, err)
		}
	}
	g := &gasNode{fail: &gno.CallError{Pkg: "p", Fn: "f", Err: errors.New("exit status 1"), Output: "hlmailbox: not a seat"}}
	if _, err := CallUpToTheBlock(context.Background(), g, "p", "f", 45_000_000); err == nil || OutOfGas(err) || len(g.asked) != 1 {
		t.Fatalf("a refusal: asked %v, %v", g.asked, err)
	}
	if NextCeiling(3_000_000_000) != 3_000_000_000 || NextCeiling(1_000_000_000) != 3_000_000_000 || Reach(1_000_000_000) != 1_000_000_000 {
		t.Fatal("the ceilings at the block")
	}
}

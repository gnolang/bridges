package gno

import (
	"errors"
	"testing"
)

// F9: auth/gasprice's answer in the forms a node gives it, and the gas a measured use becomes.
func TestGasPriceParse(t *testing.T) {
	cases := []struct {
		in           string
		gas, amount  int64
		denom        string
		perGasTimes1 float64
	}{
		{`{"gas":"1000","price":{"denom":"ugnot","amount":"1"}}`, 1000, 1, "ugnot", 0.001},
		{`{"gas":1000,"price":"1ugnot"}`, 1000, 1, "ugnot", 0.001},
		{`"3ugnot/2000gas"`, 2000, 3, "ugnot", 0.0015},
		{`null`, 0, 0, "", 0},
	}
	for _, c := range cases {
		gp, err := ParseGasPrice([]byte(c.in))
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if gp.Gas != c.gas || gp.Amount != c.amount || gp.Denom != c.denom || gp.PerGas() != c.perGasTimes1 {
			t.Fatalf("%s: %+v (per gas %g)", c.in, gp, gp.PerGas())
		}
	}
	if _, err := ParseGasPrice([]byte(`"nonsense"`)); err == nil {
		t.Fatal("nonsense parsed")
	}
	c := &Client{}
	if f := c.feeAt(45_000_000, 0); f != "45000ugnot" {
		t.Fatalf("the floor's fee: %s", f)
	}
	if f := c.feeAt(45_000_000, 0.0015); f != "67500ugnot" {
		t.Fatalf("a risen price's fee: %s", f)
	}
	if f := c.feeAt(45_000_000, 0.0001); f != "45000ugnot" {
		t.Fatalf("a chain price below the floor lowered the fee: %s", f)
	}
	if GasFor(50_000_000) != 61_000_000 || GasFor(MaxBlockGas) != MaxBlockGas {
		t.Fatal("the gas margin")
	}
}

// F14: a refusal under a node's own minimum gas price names the fee it asks; the call pays it when under the bound
// (the client's maxNodeFee, or the greater of ten times the block-price fee and 1 GNOT) and is parked with the
// node's text above it — never retried on backoff for ever.
func TestNodeMinFee(t *testing.T) {
	refusal := &CallError{Pkg: "r", Fn: "f", Err: errors.New("exit status 1"),
		Output: "insufficient fees; got: {Gas-Wanted: 45000000, Gas-Fee 45000ugnot}, fee required: 90000 with {Gas:1000 Price:2ugnot} as minimum gas price set by the node"}
	need, carried := NodeMinFee(refusal)
	if need != 90_000 || carried != 45_000 {
		t.Fatalf("parsed %d asked, %d carried", need, carried)
	}
	block := &CallError{Pkg: "r", Fn: "f", Err: errors.New("exit status 1"), Output: "insufficient fees; got: {Gas-Wanted: 45000000, Gas-Fee 45000ugnot}, fee required: {Gas:1000 Price:2ugnot} as block gas price"}
	if n, _ := NodeMinFee(block); n != 0 {
		t.Fatalf("a block-price refusal read as the node's: %d", n)
	}
	if n, _ := NodeMinFee(errors.New("dial tcp: connection refused")); n != 0 {
		t.Fatal("a plain error read as the node's minimum")
	}
	c := &Client{}
	if b := c.nodeFeeBound(45_000); b != 1_000_000 {
		t.Fatalf("the default bound at the floor: %d, want 1 GNOT", b)
	}
	if b := c.nodeFeeBound(200_000); b != 2_000_000 {
		t.Fatalf("the default bound above the floor: %d, want ten times the block-price fee", b)
	}
	if b := (&Client{MaxNodeFee: 70_000}).nodeFeeBound(45_000); b != 70_000 {
		t.Fatalf("a configured bound: %d", b)
	}
	if !nodeFeeDecision(90_000, 1_000_000) || nodeFeeDecision(90_000, 70_000) {
		t.Fatal("the pay-or-park decision")
	}
	// the refusal the client makes above the bound parks (deterministic); the node's own text stays transient,
	// since the next call pays the fee it names
	refused := &CallError{Pkg: "r", Fn: "f", Err: errors.New("the node's minimum gas price is above the bound"), Output: nodeFeeRefused + ": fee required 90000 ugnot, the bound 70000 ugnot (gno.maxNodeFee): parked with the node's text\n" + refusal.Output}
	if !Deterministic(refused) {
		t.Fatal("the bound's refusal did not park")
	}
	if Deterministic(refusal) {
		t.Fatal("the node's own refusal parked before the fee it asks was paid")
	}
}

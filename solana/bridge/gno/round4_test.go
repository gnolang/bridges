package gno

import (
	"errors"
	"strings"
	"testing"
)

// G35: the ante's third refusal — the node's minimum gas prices name no price in the fee's denom, "required
// (one of): …" — is deterministic (parked with the node's text), never retried on backoff for ever under
// transientRE's "insufficient fee"; and a refusal made here heads its error with the REFUSING line, the two fees
// in it, before gnokey's tail.
func TestNodeFeeOtherDenomParksAndTheHeadPrintsFirst(t *testing.T) {
	oneOf := &CallError{Pkg: "r", Fn: "f", Err: errors.New("exit status 1"),
		Output: `insufficient fees; got: {Gas-Wanted: 45000000, Gas-Fee 45000ugnot}, required (one of): "1000000uatom"`}
	if !Deterministic(oneOf) {
		t.Fatal("a node minimum in another denom retried for ever")
	}
	if n, _ := NodeMinFee(oneOf); n != 0 {
		t.Fatalf("the other-denom refusal read as a fee to pay: %d", n)
	}
	// the block price's and the node's own ugnot refusals stay transient (re-priced, or paid once)
	for _, out := range []string{
		"insufficient fees; got: {Gas-Wanted: 45000000, Gas-Fee 45000ugnot}, fee required: {Gas:1000 Price:2ugnot} as block gas price",
		"insufficient fees; got: {Gas-Wanted: 45000000, Gas-Fee 45000ugnot}, fee required: 90000 with {Gas:1000 Price:2ugnot} as minimum gas price set by the node",
	} {
		if Deterministic(&CallError{Pkg: "r", Fn: "f", Err: errors.New("exit status 1"), Output: out}) {
			t.Fatalf("a ugnot refusal parked: %s", out)
		}
	}
	tail := strings.Repeat("a line of gnokey's output\n", 20)
	refused := &CallError{Pkg: "r", Fn: "f", Err: errors.New("the node's minimum gas price is above the bound"), Output: nodeFeeRefused + ": fee required 90000 ugnot, the bound 70000 ugnot (gno.maxNodeFee): parked with the node's text\n" + tail}
	s := refused.Error()
	if !strings.Contains(s, "REFUSING the node's minimum gas price: fee required 90000 ugnot, the bound 70000 ugnot") {
		t.Fatalf("the error lost the REFUSING head: %s", s)
	}
	if strings.Index(s, "REFUSING") > strings.Index(s, "a line of gnokey's output") {
		t.Fatalf("the head is not printed first: %s", s)
	}
}

package ethx

import (
	"context"
	"errors"
	"testing"
)

// The integration lane I1's tests of ethx (audit round 3's fold, the cross-lane follow-ups).

// A call's MinGas (E3-CT4: an execute HlAdmin refused CallStarved is sent again with more gas than its estimate shows):
// the transaction carries the larger of the estimate with its 20% margin and MinGas, never above the transaction gas
// cap — one past it is refused, nothing sent.
func TestSenderMinGas(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{})
	c.autoMine = true
	ctx := context.Background()
	for i, x := range []struct{ min, want uint64 }{{0, 120_000}, {110_000, 120_000}, {3_000_000, 3_000_000}} {
		if _, err := s.Send(ctx, Call{To: target, Data: []byte{byte(i)}, Label: "x", MinGas: x.min}); err != nil {
			t.Fatal(err)
		}
		if g := c.sent[i].Gas(); g != x.want {
			t.Fatalf("MinGas %d: gas %d, want %d", x.min, g, x.want)
		}
	}
	if _, err := s.Send(ctx, Call{To: target, Data: []byte{9}, Label: "past the cap", MinGas: GasCap + 1}); !errors.Is(err, ErrGasCap) {
		t.Fatalf("a MinGas past the cap: %v", err)
	}
	if len(c.sent) != 3 {
		t.Fatalf("%d sent: a call past the cap went", len(c.sent))
	}
}

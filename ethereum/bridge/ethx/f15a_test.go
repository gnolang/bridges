package ethx

// Audit round 4's go-spend fold (lane F15a): E4-CT3's sender half.

import (
	"context"
	"errors"
	"testing"
)

// E4-CT3: on a chain whose estimates meter EIP-8037 state gas, a call may ask for a gas limit past EIP-7825's cap (its
// MinGas: a starved kind 8's resend, whose gas past the cap is the reservoir its call grows its state from), bounded by
// 2^32 − 1 and the head block's gas limit; on a chain without state gas the 2^24 rule stands for it.
func TestMinGasPastTheCapUnderStateGas(t *testing.T) {
	const want = GasCap + 8_812_800
	for _, ch := range []struct {
		name     string
		perByte  uint64 // a probe's code deposit a byte
		gasLimit uint64 // the head block's
		sent     bool
	}{
		{"Sepolia (EIP-8037)", 1530, 60_000_000, true},
		{"mainnet (Osaka)", 200, 60_000_000, false},
		{"EIP-8037, the head block's gas limit under it", 1530, 20_000_000, false},
	} {
		t.Run(ch.name, func(t *testing.T) {
			s, c, _ := testSender(t, SendConfig{})
			c.autoMine = true
			c.gasLimit = ch.gasLimit
			c.estimate = func(data []byte) (uint64, error) {
				if len(data) == 6 && data[0] == 0x61 && data[3] == 0x60 && data[5] == 0xf3 { // the probe: PUSH2 n PUSH1 0 RETURN
					n := uint64(data[1])<<8 | uint64(data[2])
					return 53_100 + ch.perByte*n, nil
				}
				return 2_000_000, nil
			}
			if got := s.StateGas(context.Background()); got != (ch.perByte >= stateGasPerByte) {
				t.Fatalf("StateGas says %v at %d a byte", got, ch.perByte)
			}
			_, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, MinGas: want, Label: "a starved execute's resend"})
			if ch.sent != (err == nil) || (!ch.sent && !errors.Is(err, ErrGasCap)) {
				t.Fatalf("a call asking for %d gas: %v", uint64(want), err)
			}
			if ch.sent {
				if g := c.sent[len(c.sent)-1].Gas(); g != want {
					t.Fatalf("sent with %d gas, want %d", g, uint64(want))
				}
			}
		})
	}
}

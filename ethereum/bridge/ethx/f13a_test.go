package ethx

// Audit round 3's go-spend fold (lane F13a): each test fails on eb7cebb4's sender and passes with the fold.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// E3-GS8: the backup (the same key) replaced the primary's stuck transaction at its nonce and stopped, and the base fee
// passed the backup's version too. The primary prices over the pool's version within its own bounds — never 12.5% over
// its own version, refused window after window — so its version takes the nonce, is mined once the base fee meets it,
// and the key's next call goes.
func TestSenderOutbidsTheKeysOtherVersion(t *testing.T) {
	s, c, now := testSender(t, SendConfig{Poll: 10 * time.Second, Wait: 4 * time.Minute})
	ctx := context.Background()
	if _, err := s.Send(ctx, Call{To: target, Data: []byte{1}, Label: "primary A"}); !errors.Is(err, ErrStillPending) {
		t.Fatalf("A: %v", err)
	}
	b, err := NewSender(s.Key, s.ChainID, s.Clients, SendConfig{Poll: 10 * time.Second, Wait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	b.now, b.sleep = s.now, s.sleep
	if _, err := b.Send(ctx, Call{To: target, Data: []byte{2}, Label: "backup B"}); !errors.Is(err, ErrStillPending) {
		t.Fatalf("B: %v", err)
	}
	B := c.pool[0]
	c.baseFee = GweiOf(3) // under maxBaseFee (5), above B's cap: the backup is gone
	*now = now.Add(5 * time.Minute)
	if _, err := s.Send(ctx, Call{To: target, Data: []byte{10}, Label: "primary's next call"}); !errors.Is(err, ErrStillPending) {
		t.Fatalf("the next call: %v", err)
	}
	v := c.pool[0]
	if v.Hash() == B.Hash() || v.Data()[0] != 1 {
		t.Fatalf("the backup's version still holds the nonce (tip %s, cap %s)", FormatGwei(v.GasTipCap()), FormatGwei(v.GasFeeCap()))
	}
	if v.GasTipCap().Cmp(bumped(B.GasTipCap())) < 0 || v.GasFeeCap().Cmp(bumped(B.GasFeeCap())) < 0 || v.GasFeeCap().Cmp(GweiOf(2*5+2)) > 0 || v.GasTipCap().Cmp(GweiOf(2)) > 0 {
		t.Fatalf("the outbid: tip %s, cap %s against B's %s, %s (bounds: tip 2 gwei, cap 12)", FormatGwei(v.GasTipCap()), FormatGwei(v.GasFeeCap()), FormatGwei(B.GasTipCap()), FormatGwei(B.GasFeeCap()))
	}
	c.mine() // the primary's version meets the base fee
	c.autoMine = true
	*now = now.Add(time.Minute)
	if _, err := s.Send(ctx, Call{To: target, Data: []byte{11}, Label: "the key's next call"}); err != nil {
		t.Fatalf("the key's next call waits: %v", err)
	}
}

// E3-AS2: on a chain whose estimates count EIP-8037 state gas — Sepolia since Glamsterdam, where a creation leaving
// 24,000 bytes of code estimated 37,229,846 gas (mainnet: 4,896,150) — a creation past 2^24 with its 20% margin is sent,
// bounded by 2^32−1 and the block's gas limit instead: EIP-7825's cap bounds only its execution there, which a
// successful estimate already shows fits. On a chain that prices code deposit at 200 a byte the 2^24 rule stands for
// creations, and a call that creates nothing keeps it everywhere.
func TestCreationUnderStateGas(t *testing.T) {
	code := make([]byte, 24_100) // stands for a creation that leaves 24,000 bytes
	for _, ch := range []struct {
		name       string
		perByte    uint64 // a probe's code deposit a byte
		newAccount uint64
		creation   uint64 // the 24,000-byte creation's estimate
		call       uint64 // a call's
		sent       bool   // the creation goes
	}{
		{"Sepolia (EIP-8037)", 1530, 183_600, 37_229_846, 15_000_000, true},
		{"mainnet (Osaka), a creation over the cap", 200, 0, 15_000_000, 15_000_000, false},
		{"mainnet (Osaka), the 24,000 bytes", 200, 0, 4_896_150, 100_000, true},
	} {
		t.Run(ch.name, func(t *testing.T) {
			s, c, _ := testSender(t, SendConfig{})
			c.autoMine = true
			c.gasLimit = 200_000_000
			c.estimate = func(data []byte) (uint64, error) {
				switch {
				case len(data) == 6 && data[0] == 0x61 && data[3] == 0x60 && data[5] == 0xf3: // the probe: PUSH2 n PUSH1 0 RETURN
					n := uint64(data[1])<<8 | uint64(data[2])
					return 53_100 + ch.newAccount + ch.perByte*n, nil
				case len(data) == len(code):
					return ch.creation, nil
				}
				return ch.call, nil
			}
			_, err := s.Send(context.Background(), Call{Create: true, Data: code, Label: "a creation"})
			if ch.sent != (err == nil) || (!ch.sent && !errors.Is(err, ErrGasCap)) {
				t.Fatalf("the creation: %v", err)
			}
			if ch.sent {
				if g := c.sent[len(c.sent)-1].Gas(); g != ch.creation+ch.creation/5 {
					t.Fatalf("sent with %d gas, want the estimate and a fifth (%d)", g, ch.creation+ch.creation/5)
				}
			}
			if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "a call"}); (ch.call*12/10 > GasCap) != errors.Is(err, ErrGasCap) {
				t.Fatalf("a call of %d gas: %v", ch.call, err)
			}
		})
	}
	// past the block's gas limit, a state-gas chain's creation is refused too
	s, c, _ := testSender(t, SendConfig{})
	c.gasLimit = 40_000_000
	c.estimate = func(data []byte) (uint64, error) {
		if len(data) == 6 {
			n := uint64(data[1])<<8 | uint64(data[2])
			return 236_700 + 1530*n, nil
		}
		return 37_229_846, nil
	}
	if _, err := s.Send(context.Background(), Call{Create: true, Data: code, Label: "a creation"}); !errors.Is(err, ErrGasCap) {
		t.Fatalf("a creation past the block's gas limit: %v", err)
	}
}

package ethx

import (
	"context"
	"errors"
	"testing"
)

// Fold F17b (E5-RT1): two providers' finalized answers more than two epochs apart — one standing still at an old but
// real block, or one running ahead — are a disagreement for the machines that judge by them (FinalizedWithin), never a
// reading at the lower one; up to two epochs is the honest seconds one provider's tag updates later. Finalized alone
// keeps the lower (hlwatch's stroke reads by it).
func TestF17bPairFinalizedWithinTwoEpochs(t *testing.T) {
	ctx := context.Background()
	if b, err := pairOf(t, &provider{finalized: 164, seed: 1}, &provider{finalized: 100, seed: 1}).FinalizedWithin(ctx, FinalizedApart); err != nil || b.Number != 100 {
		t.Fatalf("two epochs apart: %+v %v", b, err)
	}
	if _, err := pairOf(t, &provider{finalized: 165, seed: 1}, &provider{finalized: 100, seed: 1}).FinalizedWithin(ctx, FinalizedApart); !errors.Is(err, ErrDisagree) {
		t.Fatalf("65 blocks apart: %v", err)
	}
	if _, err := pairOf(t, &provider{finalized: 100, seed: 1}, &provider{finalized: 900, seed: 1}).FinalizedWithin(ctx, FinalizedApart); !errors.Is(err, ErrDisagree) {
		t.Fatalf("one far ahead: %v", err)
	}
	if b, err := pairOf(t, &provider{finalized: 165, seed: 1}, &provider{finalized: 100, seed: 1}).Finalized(ctx); err != nil || b.Number != 100 {
		t.Fatalf("Finalized keeps the lower: %+v %v", b, err)
	}
}

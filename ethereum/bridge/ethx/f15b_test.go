package ethx

import (
	"context"
	"errors"
	"testing"
)

// Fold F15b (E4-GT1 under one lying provider, the review's finding 1): the pair's finalized block agreed on its hash
// alone, its time taken from one provider's JSON — and the machines judge by that time (a credit the burn index cannot
// place waits until it passes the credit's). Two providers that give one finalized block different times disagree, at
// one number or with one ahead.
func TestF15bPairFinalizedAgreesOnTheTime(t *testing.T) {
	ctx := context.Background()
	if _, err := pairOf(t, &provider{finalized: 100, seed: 1}, &provider{finalized: 100, seed: 1, timeX: 86_400}).Finalized(ctx); !errors.Is(err, ErrDisagree) {
		t.Fatalf("one finalized block at two times: %v", err)
	}
	if _, err := pairOf(t, &provider{finalized: 103, seed: 1}, &provider{finalized: 100, seed: 1, timeX: 86_400}).Finalized(ctx); !errors.Is(err, ErrDisagree) {
		t.Fatalf("the lower provider's block at another time than the other's: %v", err)
	}
	if b, err := pairOf(t, &provider{finalized: 103, seed: 1}, &provider{finalized: 100, seed: 1}).Finalized(ctx); err != nil || b.Time != 1_800_000_000+100*12 {
		t.Fatalf("an honest pair: %+v %v", b, err)
	}
}

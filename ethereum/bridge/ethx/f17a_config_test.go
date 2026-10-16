package ethx

import (
	"context"
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

// Lane F17a (E5-GS4): a provider's batch size is its own, in the config beside its log range (batchSize), and a pair
// batches at the smaller of its two.
func TestF17aProviderBatchSize(t *testing.T) {
	f := newFakeRPC()
	f.on("eth_call", func([]any) (any, error) { return hexutil.Bytes{1}, nil })
	sizes := []int{}
	c := NewClient(Provider{Name: "set", URL: "https://t.example/", BatchSize: 25}, &batchCapRPC{fakeRPC: f, limit: 1000, sizes: &sizes})
	if _, _, err := c.CallBatch(context.Background(), make([]CallReq, 60), Latest); err != nil || fmt.Sprint(sizes) != "[25 25 10]" {
		t.Fatalf("a provider's configured batchSize of 25: batches %v (%v)", sizes, err)
	}
	d := NewClient(Provider{Name: "default", URL: "https://d.example/"}, f)
	if p := (Pair{A: c, B: d}); p.BatchSize() != 25 || d.BatchSize() != DefaultBatchSize {
		t.Fatalf("a pair of 25 and the default batches at %d; the default %d", p.BatchSize(), d.BatchSize())
	}
}

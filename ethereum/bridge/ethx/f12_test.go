package ethx

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

// rpcErr is a JSON-RPC error with a code and data, as go-ethereum hands one up (a provider's 200 with an error).
type rpcErr struct {
	code int
	msg  string
	data any
}

func (e *rpcErr) Error() string          { return e.msg }
func (e *rpcErr) ErrorCode() int         { return e.code }
func (e *rpcErr) ErrorData() interface{} { return e.data }

// E3-AS4: the providers' refusals of a log range too wide, as each answered on 2026-10-14 and as go-ethereum hands them
// up — drpc's free plan an HTTP 400 whose body says "ranges over 10000 blocks are not supported on free plan" (for
// 1,001 blocks), Tenderly a 200 with "invalid params" and the reason in its data, publicnode "query exceeds max results
// 20000" — halve the range until it answers; publicnode's refusal of a range 10,000 blocks back ("Archive requests
// require a personal token", HTTP 403) is a provider without that history: no verdict, never halved.
func TestF12LogsRangeHalvesOnTheProvidersRefusals(t *testing.T) {
	refusals := map[string]func(lo, hi uint64) error{
		"drpc": func(lo, hi uint64) error {
			if hi-lo+1 <= 100 {
				return nil
			}
			return rpc.HTTPError{StatusCode: 400, Status: "400 Bad Request", Body: []byte(`{"id":1,"jsonrpc":"2.0","error":{"message":"ranges over 10000 blocks are not supported on free plan","code":35}}`)}
		},
		"tenderly": func(lo, hi uint64) error {
			if hi-lo+1 <= 100 {
				return nil
			}
			return &rpcErr{code: -32602, msg: "invalid params", data: "Query returned more than 20000 results. Try with this block range [0x18fabfb, 0x18fadef]."}
		},
		"publicnode": func(lo, hi uint64) error {
			if hi-lo+1 <= 100 {
				return nil
			}
			return &rpcErr{code: -32602, msg: "query exceeds max results 20000, retry with the range 26192891-26193084"}
		},
	}
	for name, refuse := range refusals {
		f := newFakeRPC()
		f.on("eth_getLogs", func(args []any) (any, error) {
			q := args[0].(map[string]any)
			lo, _ := hexutil.DecodeUint64(q["fromBlock"].(string))
			hi, _ := hexutil.DecodeUint64(q["toBlock"].(string))
			if err := refuse(lo, hi); err != nil {
				return nil, err
			}
			return []types.Log{}, nil
		})
		c := NewClient(Provider{Name: name, URL: "https://" + name + ".example/"}, f)
		if _, err := c.LogsRange(context.Background(), LogQuery{}, 1, 1000); err != nil {
			t.Errorf("%s: a range its refusal says is too wide was not halved: %v", name, err)
		}
	}
	// publicnode's archive refusal: no state for that history — named so, and not halved block by block
	f := newFakeRPC()
	f.on("eth_getLogs", func([]any) (any, error) {
		return nil, rpc.HTTPError{StatusCode: 403, Status: "403 Forbidden", Body: []byte(`{"jsonrpc":"2.0","error":{"code":-32602,"message":"Archive requests require a personal token. Get one at: https://www.allnodes.com/publicnode"},"id":1}`)}
	})
	c := NewClient(Provider{Name: "publicnode", URL: "https://p.example/"}, f)
	_, err := c.LogsRange(context.Background(), LogQuery{}, 1, 1000)
	if err == nil || !IsNoState(err) || f.count("eth_getLogs") != 1 {
		t.Fatalf("an archive refusal: %v (no state %v, %d calls)", err, IsNoState(err), f.count("eth_getLogs"))
	}
	if !strings.Contains(err.Error(), "Archive requests require a personal token") || errors.Is(err, ErrDisagree) {
		t.Fatalf("the refusal's words: %v", err)
	}
}

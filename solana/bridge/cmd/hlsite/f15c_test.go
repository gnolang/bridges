package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Round 4's site findings (fold lane F15c): E4-TL7 (= E4-RL3, E4-CF6 item 10) — the status read every Ethereum
// realm's decisions.json whole, and one realm past ≈360 decisions blanked the chain's whole pending list; E4-AS3 —
// the site's own Ethereum reader dropped a non-200's body, so drpc's range refusal (HTTP 400) never halved the log
// window. Each test was run on 1a6770b5's code first, where it fails (fold-r4/f15c.md).

// bigLedger is the Ethereum deployment's r/hlgov after 400 decisions, the last two pending.
func bigLedger() string {
	var ds []string
	for i := 1; i <= 400; i++ {
		state := "applied"
		if i >= 399 {
			state = "pending"
		}
		ds = append(ds, fmt.Sprintf(`{"id":%d,"kind":"set guardians","text":"set guardians and watch keys: seat 5 to 0x%040x","state":%q,"made":1,"recorded":1,"due":604801,"applied":0,"cancelled":0,"note":""}`, i, i, state))
	}
	return "[" + strings.Join(ds, ",") + "]"
}

// bigLedgerGno is the mock node with that r/hlgov: its whole decisions.json out of vm/qrender's gas (≈360 decisions
// and up, E3-RL4), its pages as the realm renders them — and the paths the Solana realms were asked.
type bigLedgerGno struct {
	*mockGno
	mu       sync.Mutex
	pageErr  map[string]error
	solPaths []string
}

func (g *bigLedgerGno) RenderAt(ctx context.Context, pkgPath, path string, height int64) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !strings.HasPrefix(pkgPath, mockEthNS("")) {
		g.solPaths = append(g.solPaths, path)
	}
	if pkgPath == mockEthNS("hlgov") {
		switch {
		case path == "decisions.json":
			return "", errors.New("gno query vm/qrender: map[@type:/std.OutOfGasError] out of gas in location: CPUCycles")
		case strings.HasPrefix(path, "decisions.json/"):
			if err := g.pageErr[path]; err != nil {
				return "", err
			}
			return ledgerPage(bigLedger(), strings.TrimPrefix(path, "decisions.json/"))
		}
	}
	return g.mockGno.RenderAt(ctx, pkgPath, path, height)
}

// E4-TL7: the Ethereum status reads each realm's ledger by page — r/hlgov past 400 decisions, whose whole ledger is out
// of query gas, lists its two pending decisions beside hlfees' — and one realm that does not read is its own error
// beside the others' decisions; the Solana status reads its realms' ledgers whole, as it always did (no page asked).
func TestEthStatusReadsTheLedgersByPage(t *testing.T) {
	s, g, _, _ := mockBoth(false, siteDir, fixedNow)
	s.statusTTL = 0
	big := &bigLedgerGno{mockGno: g, pageErr: map[string]error{}}
	s.gno = big
	status := func(chain string) statusJSON {
		t.Helper()
		rr := httptest.NewRecorder()
		s.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/"+chain+"/status", nil))
		var st statusJSON
		if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &st) != nil {
			t.Fatalf("%s status: %d %s", chain, rr.Code, rr.Body.String())
		}
		return st
	}
	listed := func(st statusJSON) string {
		var out []string
		for _, d := range st.Decisions {
			out = append(out, fmt.Sprintf("%s#%d", d.Realm[strings.LastIndex(d.Realm, "/")+1:], d.ID))
		}
		return strings.Join(out, " ")
	}
	pendingErrors := func(st statusJSON) []string {
		var out []string
		for _, e := range st.Errors {
			if strings.HasPrefix(e, "the pending decisions") {
				out = append(out, e)
			}
		}
		return out
	}
	st := status("ethereum")
	if got := listed(st); got != "hlfees#1 hlgov#399 hlgov#400" || len(pendingErrors(st)) != 0 {
		t.Fatalf("the Ethereum status over r/hlgov's 400 decisions: listed %q, errors %v", got, st.Errors)
	}
	// r/hlgov's second page fails: hlfees' decision stays listed, r/hlgov's failure is named alone
	big.mu.Lock()
	big.pageErr["decisions.json/21"] = errors.New("gno rpc abci_query: 502 Bad Gateway")
	big.mu.Unlock()
	st = status("ethereum")
	if got, errs := listed(st), pendingErrors(st); got != "hlfees#1" || len(errs) != 1 || errs[0] != "the pending decisions: "+mockEthNS("hlgov")+": gno rpc abci_query: 502 Bad Gateway" {
		t.Fatalf("one realm's page failing: listed %q, errors %v", got, errs)
	}
	// the Solana deployment's realms have no pages: read whole, as before, not one page asked
	big.mu.Lock()
	big.solPaths = nil
	big.mu.Unlock()
	if sol := status("solana"); listed(sol) != "hlfees#1" || len(pendingErrors(sol)) != 0 {
		t.Fatalf("the Solana status: listed %q, errors %v", listed(sol), sol.Errors)
	}
	big.mu.Lock()
	defer big.mu.Unlock()
	whole := 0
	for _, p := range big.solPaths {
		if strings.HasPrefix(p, "decisions.json/") {
			t.Fatalf("a Solana realm was asked a page: %v", big.solPaths)
		}
		if p == "decisions.json" {
			whole++
		}
	}
	if whole != 5 {
		t.Fatalf("the Solana realms' ledgers read whole %d times (want the five realms once): %v", whole, big.solPaths)
	}
}

// E4-AS3: drpc's free plan refuses an eth_getLogs past ≈100 blocks with HTTP 400 and a JSON-RPC error in the body
// ("ranges over 10000 blocks are not supported on free plan", code 35). The site's reader dropped every non-200's body,
// so the refusal never read as a range refusal, the window (logRange, 1000 in the example config) never halved and the
// activity stalled; now the body's error is kept (scrubbed of the URL's key) and the window halves until the provider
// answers.
func TestEthLogsDrpcRangeRefusalHalvesTheWindow(t *testing.T) {
	const key = "S3CRETkey0123456789abcdef"
	s, _, _, me := mockBoth(true, siteDir, fixedNow)
	refused := 0
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Method == "eth_getLogs" && len(req.Params) > 0 {
			var f struct{ FromBlock, ToBlock string }
			_ = json.Unmarshal(req.Params[0], &f)
			a, _ := parseQty(f.FromBlock)
			b, _ := parseQty(f.ToBlock)
			if b-a+1 > 101 {
				mu.Lock()
				refused++
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"id":1,"jsonrpc":"2.0","error":{"message":"ranges over 10000 blocks are not supported on free plan","code":35}}`))
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		me.ServeHTTP(w, r)
	}))
	defer srv.Close()
	ch := s.cfg.Chain("ethereum")
	e := newEthereum(*ch, newEthHTTP(srv.URL+"/?dkey="+key), mockGnoDomain, func() []string { return s.realmAddresses() })
	s.others["ethereum"] = e
	var st *activityState
	for i := 0; i < 40 && (st == nil || st.other.eth == nil || !st.other.eth.done); i++ {
		st = s.activityNow(ctx, ch, e)
		for _, msg := range st.errors {
			if strings.Contains(msg, key) {
				t.Fatalf("an error carries the provider's key: %s", msg)
			}
		}
	}
	p := st.other.eth
	if p == nil {
		t.Fatalf("no Ethereum history read: %v", st.errors)
	}
	if p.rng > 101 || !p.done || refused == 0 {
		t.Fatalf("drpc's range refusal over HTTP 400: the window %d blocks (want at most 101), history done %v, %d refusals; errors %v", p.rng, p.done, refused, st.errors)
	}
	for _, id := range []string{mockEthQID, mockEthInID} {
		if _, ok := st.other.Burns[id]; !ok {
			t.Errorf("burn %s not read through the narrower windows", id)
		}
	}
	// the refusal itself reads as a range refusal, its text kept, no key in it
	err := newEthHTTP(srv.URL+"/?dkey="+key).Call(ctx, "eth_getLogs", []any{map[string]any{"fromBlock": "0x1", "toBlock": "0x400"}}, nil)
	if err == nil || !rangeRefused(err) || !strings.Contains(err.Error(), "ranges over 10000 blocks are not supported on free plan") || !strings.Contains(err.Error(), "HTTP 400") || strings.Contains(err.Error(), key) {
		t.Fatalf("drpc's refusal: %v (a range refusal %v)", err, rangeRefused(err))
	}
}

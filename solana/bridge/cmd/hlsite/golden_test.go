package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// The Solana side stays as it was (E-M4's promise): testdata/solana-golden.json holds every answer the Solana
// deployment's API gave on bridge-eth 27b32879, the commit before Ethereum came — written there by this file's
// captureSolana, copied unchanged — and TestSolanaUnchanged asks the same questions of this code, Solana served
// alone and served beside Ethereum, and wants every answer byte for byte. The burn builder's throwaway key is
// random per request, so the bytes it decides (the transaction, the key, the dispatch record) are masked.

var goldenMask = regexp.MustCompile(`"(tx|unique|messageAccount)":"[^"]*"`)

// captureSolana asks a server everything the Solana pages and the tracker ask: the chains (when withChains), the
// status, the activity, every transfer by every kind of reference, the builder and its refusals — in the done
// world, then in the staged world after each step. newServer makes a server of the mock (progress: the staged
// world).
func captureSolana(t testing.TB, newServer func(progress bool) *server, withChains bool) map[string]string {
	out := map[string]string{}
	ask := func(h http.Handler, method, path, body string) string {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return fmt.Sprintf("%d %s", rr.Code, goldenMask.ReplaceAllString(rr.Body.String(), `"$1":"masked"`))
	}
	other := sum256("another tx")
	refs := map[string]string{"out id": mockOutID, "out id 0x upper": "0x" + strings.ToUpper(mockOutID), "gno tx": mockGnoTx, "burn sig": mockBurnTx, "in id": mockInID,
		"p id": mockPID, "q id": mockQID, "junk": "deadbeef", "unknown id": strings.Repeat("ab", 32), "unknown gno tx": base64.StdEncoding.EncodeToString(other),
		"unknown sig": mockSig("unknown").String()}
	names := make([]string, 0, len(refs))
	for n := range refs {
		names = append(names, n)
	}
	sort.Strings(names)
	s := newServer(false)
	h := s.handler()
	if withChains {
		out["chains"] = ask(h, "GET", "/api/chains", "")
	}
	out["status"] = ask(h, "GET", "/api/solana/status", "")
	out["activity"] = ask(h, "GET", "/api/solana/activity", "")
	for _, n := range names {
		out["transfer "+n] = ask(h, "GET", "/api/solana/transfer/"+refs[n], "")
	}
	alice := mockPub("alice").String()
	for name, body := range map[string]string{
		"valid":      `{"token":"BUBBLE","sender":"` + alice + `","recipient":"` + mockRecipient + `","amount":"200000000"}`,
		"realm":      `{"token":"BUBBLE","sender":"` + alice + `","recipient":"` + hl.GnoPackageAddress(mockNS+"hlgnot") + `","amount":1}`,
		"bad sender": `{"token":"GNOT","sender":"nope","recipient":"` + mockRecipient + `","amount":1}`,
		"bad to":     `{"token":"GNOT","sender":"` + alice + `","recipient":"g1nope","amount":1}`,
		"zero":       `{"token":"GNOT","sender":"` + alice + `","recipient":"` + mockRecipient + `","amount":0}`,
		"token":      `{"token":"ETH","sender":"x","recipient":"y","amount":1}`,
		"fraction":   `{"token":"GNOT","sender":"x","recipient":"y","amount":"1.5"}`,
		"not json":   `not json`,
	} {
		out["burn-tx "+name] = ask(h, "POST", "/api/solana/burn-tx", body)
	}
	out["burn-tx GET"] = ask(h, "GET", "/api/solana/burn-tx", "")
	out["transfer too long"] = ask(h, "GET", "/api/solana/transfer/"+strings.Repeat("x", 200), "")
	// the staged world, step by step: the activity and both transfers after each
	s = newServer(true)
	h = s.handler()
	out["stage 0 activity"] = ask(h, "GET", "/api/solana/activity", "")
	for i, flow := range []string{"out", "out", "out", "in", "in", "in", "in", "in", "down"} {
		out[fmt.Sprintf("stage %d step", i+1)] = ask(h, "POST", "/api/mock/step?flow="+flow, "")
		out[fmt.Sprintf("stage %d activity", i+1)] = ask(h, "GET", "/api/solana/activity", "")
		out[fmt.Sprintf("stage %d out", i+1)] = ask(h, "GET", "/api/solana/transfer/"+mockOutID, "")
		out[fmt.Sprintf("stage %d in", i+1)] = ask(h, "GET", "/api/solana/transfer/"+mockBurnTx, "")
		out[fmt.Sprintf("stage %d status", i+1)] = ask(h, "GET", "/api/solana/status", "")
	}
	return out
}

const goldenFile = "testdata/solana-golden.json"

// TestSolanaGoldenWrite writes the golden answers (HLSITE_GOLDEN_WRITE=1): run on the commit before E-M4, the file
// is what this one must match.
func TestSolanaGoldenWrite(t *testing.T) {
	if os.Getenv("HLSITE_GOLDEN_WRITE") == "" {
		t.Skip("HLSITE_GOLDEN_WRITE=1 writes " + goldenFile)
	}
	got := captureSolana(t, func(progress bool) *server { s, _, _ := mockServer(progress, siteDir, fixedNow); return s }, true)
	b, _ := json.MarshalIndent(got, "", " ")
	_ = os.MkdirAll(filepath.Dir(goldenFile), 0o755)
	if err := os.WriteFile(goldenFile, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSolanaUnchanged(t *testing.T) {
	b, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) < 60 {
		t.Fatalf("the golden file holds %d answers", len(want))
	}
	for _, c := range []struct {
		name string
		make func(progress bool) *server
		all  bool
	}{
		{"Solana alone", func(progress bool) *server { s, _, _ := mockServer(progress, siteDir, fixedNow); return s }, true},
		{"Solana beside Ethereum", func(progress bool) *server { s, _, _, _ := mockBoth(progress, siteDir, fixedNow); return s }, false},
	} {
		got := captureSolana(t, c.make, c.all)
		n := 0
		for k, w := range want {
			g, ok := got[k]
			if !ok {
				if k == "chains" && !c.all {
					continue // beside Ethereum the chains list two
				}
				t.Errorf("%s: no answer to %q", c.name, k)
				continue
			}
			if strings.HasSuffix(k, " step") {
				// the step's answer lists the Ethereum stages too: only the Solana ones must agree
				var gm, wm map[string]any
				_ = json.Unmarshal([]byte(strings.SplitN(g, " ", 2)[1]), &gm)
				_ = json.Unmarshal([]byte(strings.SplitN(w, " ", 2)[1]), &wm)
				for kk, v := range wm {
					if fmt.Sprint(gm[kk]) != fmt.Sprint(v) {
						t.Errorf("%s: %s: %s is %v, was %v", c.name, k, kk, gm[kk], v)
					}
				}
				n++
				continue
			}
			if !bytes.Equal([]byte(g), []byte(w)) {
				t.Errorf("%s: %q changed:\n got %.600s\nwant %.600s", c.name, k, g, w)
			}
			n++
		}
		t.Logf("%s: %d answers byte for byte", c.name, n)
	}
}

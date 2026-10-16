package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// fakeGno is a gno node as the setup and the audit read it: blocks by height (hash and time; a height given a header
// serves it whole, as /block's block.header in amino's JSON, its hash computed from it unless hashes overrides it),
// the validators at a height, r/hlgov's Render("json") (its counter, start and decision recipient), its page (its six
// Ethereum seats) and decision/<n> (a dispatched body) — or the keeper's not-found while the realm is absent — and
// any other realm's render from renders ("pkg:path"), the keeper's not-found for every other realm.
type fakeGno struct {
	mu         sync.Mutex
	hashes     map[int64][]byte
	headers    map[int64]json.RawMessage
	validators [][32]byte // power 10 each
	hlgov      string     // r/hlgov's path
	last       uint64
	start      uint64
	recipient  string            // r/hlgov's decision recipient (EIP-55)
	former     []string          // its former recipients
	seats      []string          // its six Ethereum seats as its page shows them
	decisions  map[uint64]string // its decision/<n> renders
	absent     bool
	renders    map[string]string
}

// headerHash is the hash of a header in /block's amino JSON.
func headerHash(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	h, err := gnolight.HeaderFromJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	x := h.Hash()
	return x[:]
}

func (f *fakeGno) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params map[string]string `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		reply := func(v any) {
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": v})
			_, _ = w.Write(b)
		}
		render := func(s string) {
			reply(map[string]any{"response": map[string]any{"ResponseBase": map[string]any{"Error": nil, "Data": base64.StdEncoding.EncodeToString([]byte(s))}}})
		}
		// the keeper's not-found as a node answers it: the error's type, its %#v as the log (its message trace)
		notFound := func(pkg string) {
			reply(map[string]any{"response": map[string]any{"ResponseBase": map[string]any{"Error": map[string]any{"@type": "/vm.InvalidPkgPathError"},
				"Log": "--= Error =--\nData: vm.InvalidPkgPathError{abciError:vm.abciError{}}\nMsg Traces:\n    0  gno.land/pkg/sdk/vm/keeper.go:1883 - package not found: " + pkg + "\n--= /Error =--\n"}}})
		}
		switch req.Method {
		case "block":
			h, _ := strconv.ParseInt(req.Params["height"], 10, 64)
			hash := f.hashes[h]
			raw, whole := f.headers[h]
			if whole && hash == nil {
				hash = headerHash(t, raw)
			}
			meta := map[string]any{"block_id": map[string]any{"hash": base64.StdEncoding.EncodeToString(hash), "parts": map[string]any{"total": "1", "hash": base64.StdEncoding.EncodeToString(make([]byte, 32))}},
				"header": map[string]any{"height": strconv.FormatInt(h, 10), "time": "2026-10-14T10:00:00Z"}}
			if !whole {
				reply(map[string]any{"block_meta": meta})
				return
			}
			var hd struct {
				Time string `json:"time"`
			}
			_ = json.Unmarshal(raw, &hd)
			meta["header"].(map[string]any)["time"] = hd.Time
			reply(map[string]any{"block_meta": meta, "block": map[string]any{"header": raw, "data": map[string]any{"txs": nil}, "last_commit": nil}})
		case "validators":
			var vs []any
			for _, pk := range f.validators {
				vs = append(vs, map[string]any{"pub_key": map[string]any{"@type": "/tm.PubKeyEd25519", "value": base64.StdEncoding.EncodeToString(pk[:])}, "voting_power": "10"})
			}
			reply(map[string]any{"validators": vs})
		case "abci_query":
			data, _ := base64.StdEncoding.DecodeString(req.Params["data"])
			pkg, path, _ := strings.Cut(string(data), ":")
			if req.Params["path"] != "vm/qrender" {
				notFound(pkg)
				return
			}
			if s, ok := f.renders[pkg+":"+path]; ok {
				render(s)
				return
			}
			if pkg != f.hlgov || f.absent {
				notFound(pkg)
				return
			}
			switch {
			case path == "json":
				former, _ := json.Marshal(append([]string{}, f.former...))
				render(`{"last":` + strconv.FormatUint(f.last, 10) + `,"next":` + strconv.FormatUint(f.last+1, 10) + `,"start":` + strconv.FormatUint(f.start, 10) +
					`,"recipient":` + strconv.Quote(f.recipient) + `,"former":` + string(former) + `}`)
			case path == "":
				var b strings.Builder
				b.WriteString("# GovDAO's decisions for Ethereum\n\n- Ethereum seats as last set here:\n")
				for i, s := range f.seats {
					kind := "guardian"
					if i >= 4 {
						kind = "watch key"
					}
					b.WriteString("  - seat " + strconv.Itoa(i+1) + " (" + kind + "): `" + s + "`\n")
				}
				render(b.String())
			case strings.HasPrefix(path, "decision/"):
				n, _ := strconv.ParseUint(path[len("decision/"):], 10, 64)
				if s, ok := f.decisions[n]; ok {
					render(s)
				} else {
					render("null")
				}
			default:
				render("unknown path; see the page")
			}
		default:
			http.Error(w, "no such method in the fake: "+req.Method, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

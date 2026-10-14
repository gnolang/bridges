package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// server is the site: the static pages and the read-only API. It proxies every chain read (the page never calls
// an RPC: connect-src 'self'), rate-limits the API by client and logs nothing personal — no address, no
// amount, no client IP ever reaches a log line.
type server struct {
	cfg    *Config
	gno    gnoNode
	others map[string]Other
	site   string // the static files' directory
	now    func() time.Time
	lim    *limiter

	mu       sync.Mutex
	statuses map[string]*cached
	chains   *cached
	transfer map[string]*cached
	mailbox  *agent.MailboxView // the last mailbox view, for the realm addresses a burn may not name
	// mockInfo is served at /api/mock under -mock alone: the canned deployment's ids for the browser suite.
	mockInfo map[string]string
	// preview serves the canned deployment as a public preview before the bridge is deployed: the pages say so,
	// the figures are examples, and the builder refuses — nobody is handed a transaction for programs that do not
	// exist (SITE.md).
	preview bool
}

type cached struct {
	at   time.Time
	code int
	body []byte
}

func newServer(cfg *Config, g gnoNode, others map[string]Other, site string) *server {
	s := &server{cfg: cfg, gno: g, others: others, site: site, now: time.Now, statuses: map[string]*cached{}, transfer: map[string]*cached{}}
	s.lim = newLimiter(2, 30, func() time.Time { return s.now() }) // two requests a second, a burst of thirty, per client
	return s
}

// rememberMailbox keeps the last mailbox view: a burn's recipient is checked against every route it registers.
func (s *server) rememberMailbox(mb agent.MailboxView) {
	s.mu.Lock()
	s.mailbox = &mb
	s.mu.Unlock()
}

// realmAddresses are the pkgpaths whose addresses a burn may not name (§5.2, K21): the configured realms and
// every route the mailbox registers or dropped.
func (s *server) realmAddresses() []string {
	out := s.cfg.Realms()
	s.mu.Lock()
	mb := s.mailbox
	s.mu.Unlock()
	if mb != nil {
		out = append(out, mb.RoutePaths...)
		for p := range mb.DroppedRoutes {
			out = append(out, p)
		}
	}
	return out
}

// handler is the whole site.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/chains", s.handleChains)
	mux.HandleFunc("GET /api/{chain}/status", s.handleStatus)
	mux.HandleFunc("GET /api/{chain}/transfer/{ref}", s.handleTransfer)
	mux.HandleFunc("POST /api/{chain}/burn-tx", s.handleBurnTx)
	mux.HandleFunc("/api/{chain}/burn-tx", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "POST")
		jsonError(w, 405, "POST a JSON body: {token, sender, recipient, amount}")
	})
	if s.mockInfo != nil {
		mux.HandleFunc("GET /api/mock", func(w http.ResponseWriter, r *http.Request) {
			body, _ := json.Marshal(s.mockInfo)
			writeJSON(w, 200, body)
		})
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { jsonError(w, 404, "no such endpoint") })
	mux.HandleFunc("/", s.handleStatic)
	return s.headers(mux)
}

// csp is the page's policy: nothing from anywhere but this origin; the wallet extensions inject themselves and
// are not subject to it.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func (s *server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
			if !s.lim.allow(clientKey(r)) {
				h.Set("Retry-After", "5")
				jsonError(w, 429, "too many requests; try again in a few seconds")
				return
			}
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

// clientKey is what the limiter counts by: the first X-Forwarded-For address (nginx sets it), or the peer's.
// It is used as a map key and never logged.
func clientKey(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			xff = xff[:i]
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------- /api/chains --

type chainsJSON struct {
	Preview bool        `json:"preview,omitempty"`
	Gno     gnoInfo     `json:"gno"`
	Chains  []chainInfo `json:"chains"`
}

type gnoInfo struct {
	ChainID   string        `json:"chainId"`
	Mailbox   string        `json:"mailbox"`
	Fees      string        `json:"fees"`
	Gov       string        `json:"gov"`
	Bubble    string        `json:"bubble"`
	RealmLink string        `json:"realmLink"`
	TxLink    string        `json:"txLink"`
	GasWanted int64         `json:"gasWanted"`
	GasFee    int64         `json:"gasFee"` // ugnot for gasWanted at the chain's price now
	GasPrice  *gno.GasPrice `json:"gasPrice"`
}

type chainInfo struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Domain   uint32      `json:"domain"`
	Explorer string      `json:"explorer"`
	Cluster  string      `json:"cluster"`
	Tokens   []tokenInfo `json:"tokens"`
}

type tokenInfo struct {
	Symbol       string `json:"symbol"`
	Program      string `json:"program"`
	Mint         string `json:"mint"`
	Realm        string `json:"realm"`
	RealmAddress string `json:"realmAddress"` // the route realm's g1… address: BUBBLE's Approve names it as the spender
	Decimals     int    `json:"decimals"`
}

const chainsTTL = time.Minute

func (s *server) chainsBody(ctx context.Context) []byte {
	s.mu.Lock()
	c := s.chains
	s.mu.Unlock()
	if c != nil && s.now().Sub(c.at) < chainsTTL {
		return c.body
	}
	out := chainsJSON{Preview: s.preview, Chains: []chainInfo{}}
	out.Gno = gnoInfo{ChainID: s.cfg.Gno.ChainID, Mailbox: s.cfg.Gno.Mailbox, Fees: s.cfg.Gno.Fees, Gov: s.cfg.Gno.Gov, Bubble: s.cfg.Gno.Bubble, RealmLink: s.cfg.Gno.RealmLink, TxLink: s.cfg.Gno.TxLink, GasWanted: s.cfg.Gno.GasWanted}
	price := gno.MinGasPrice
	if gp, err := s.gno.GasPrice(ctx); err == nil && (gp.Denom == "" || gp.Denom == "ugnot") {
		if p := gp.PerGas(); p > price {
			price = p
		}
		out.Gno.GasPrice = &gp
	}
	out.Gno.GasFee = int64(float64(s.cfg.Gno.GasWanted)*price + 0.999999)
	for _, ch := range s.cfg.Chains {
		ci := chainInfo{ID: ch.ID, Name: ch.Name, Domain: ch.Domain, Explorer: ch.Explorer, Cluster: ch.Cluster, Tokens: []tokenInfo{}}
		for _, t := range ch.Tokens {
			ci.Tokens = append(ci.Tokens, tokenInfo{Symbol: t.Symbol, Program: t.Program, Mint: t.Mint, Realm: t.Realm, RealmAddress: hl.GnoPackageAddress(t.Realm), Decimals: decimals})
		}
		out.Chains = append(out.Chains, ci)
	}
	body, _ := json.Marshal(out)
	s.mu.Lock()
	s.chains = &cached{at: s.now(), body: body}
	s.mu.Unlock()
	return body
}

func (s *server) handleChains(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.chainsBody(r.Context()))
}

// ---------------------------------------------------------- /api/<chain>/… --

func (s *server) chainOf(w http.ResponseWriter, r *http.Request) (*ChainConfig, Other, bool) {
	id := r.PathValue("chain")
	ch := s.cfg.Chain(id)
	other := s.others[id]
	if ch == nil || other == nil {
		jsonError(w, 404, "no such chain")
		return nil, nil, false
	}
	return ch, other, true
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ch, other, ok := s.chainOf(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	c := s.statuses[ch.ID]
	s.mu.Unlock()
	if c != nil && s.now().Sub(c.at) < statusTTL {
		writeJSON(w, c.code, c.body)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	st, err := s.status(ctx, ch, other)
	var body []byte
	code := 200
	if err != nil {
		code = 503
		body, _ = json.Marshal(map[string]string{"error": err.Error()})
		log.Printf("status %s: %v", ch.ID, err)
	} else {
		body, _ = json.Marshal(st)
	}
	s.mu.Lock()
	s.statuses[ch.ID] = &cached{at: s.now(), code: code, body: body}
	s.mu.Unlock()
	writeJSON(w, code, body)
}

const transferTTL = 5 * time.Second

func (s *server) handleTransfer(w http.ResponseWriter, r *http.Request) {
	ch, other, ok := s.chainOf(w, r)
	if !ok {
		return
	}
	ref := strings.TrimSpace(r.PathValue("ref"))
	if ref == "" || len(ref) > 128 {
		jsonError(w, 400, "a message id, a gno.land transaction hash or a "+ch.Name+" signature")
		return
	}
	key := ch.ID + "/" + ref
	s.mu.Lock()
	c := s.transfer[key]
	s.mu.Unlock()
	if c != nil && s.now().Sub(c.at) < transferTTL {
		writeJSON(w, c.code, c.body)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	t, err := s.resolve(ctx, ch, other, ref)
	var body []byte
	code := 200
	switch {
	case errors.Is(err, errUnknownTransfer):
		code = 404
		body, _ = json.Marshal(map[string]string{"error": err.Error()})
	case err != nil:
		code = 503
		body, _ = json.Marshal(map[string]string{"error": err.Error()})
		log.Printf("transfer %s: a read failed: %s", ch.ID, agent.Redact(err.Error()))
	default:
		body, _ = json.Marshal(t)
	}
	s.mu.Lock()
	if len(s.transfer) > 2000 {
		s.transfer = map[string]*cached{}
	}
	s.transfer[key] = &cached{at: s.now(), code: code, body: body}
	s.mu.Unlock()
	writeJSON(w, code, body)
}

type burnRequest struct {
	Token     string          `json:"token"`
	Sender    string          `json:"sender"`
	Recipient string          `json:"recipient"`
	Amount    json.RawMessage `json:"amount"` // base units, as a number or a string
}

func (s *server) handleBurnTx(w http.ResponseWriter, r *http.Request) {
	if s.preview {
		jsonError(w, 503, "a preview: the bridge is not deployed yet, so sending opens at launch")
		return
	}
	ch, other, ok := s.chainOf(w, r)
	if !ok {
		return
	}
	var req burnRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		jsonError(w, 400, "a JSON body with token, sender, recipient and amount (base units)")
		return
	}
	t := ch.Token(req.Token)
	if t == nil {
		jsonError(w, 400, "unknown token")
		return
	}
	amount, err := strconv.ParseUint(strings.Trim(string(req.Amount), `"`), 10, 64)
	if err != nil {
		jsonError(w, 400, "the amount must be a whole number of base units")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tx, err := other.BurnTx(ctx, *t, req.Sender, req.Recipient, amount)
	if err != nil {
		if strings.HasPrefix(err.Error(), "a recent blockhash") {
			jsonError(w, 503, err.Error())
			return
		}
		jsonError(w, 400, err.Error())
		return
	}
	body, _ := json.Marshal(tx)
	writeJSON(w, 200, body)
}

// ------------------------------------------------------------------- static --

// pages maps the site's paths to its files; anything else is a file under the site directory by its extension.
var pages = map[string]string{"/": "index.html", "/bridge": "bridge.html", "/status": "status.html", "/help": "help.html"}

var staticExt = map[string]string{".html": "text/html; charset=utf-8", ".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon", ".txt": "text/plain; charset=utf-8", ".webmanifest": "application/manifest+json"}

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "method not allowed", 405)
		return
	}
	p := path.Clean("/" + r.URL.Path)
	name, ok := pages[p]
	if !ok {
		if strings.Contains(p[1:], "/") {
			http.NotFound(w, r)
			return
		}
		name = p[1:]
	}
	ctype, ok := staticExt[filepath.Ext(name)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(s.site, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// ------------------------------------------------------------------ limiter --

// limiter is a token bucket per client key: rate tokens a second up to burst. Keys are kept in memory and
// dropped when idle; nothing is written anywhere.
type limiter struct {
	mu    sync.Mutex
	rate  float64
	burst float64
	now   func() time.Time
	m     map[string]*bucket
	calls int
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(rate, burst float64, now func() time.Time) *limiter {
	return &limiter{rate: rate, burst: burst, now: now, m: map[string]*bucket{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.calls++
	if l.calls%1000 == 0 {
		for k, b := range l.m {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.m, k)
			}
		}
	}
	b := l.m[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.m[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

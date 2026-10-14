package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

func fixedNow() time.Time { return time.Unix(1_800_000_000, 0) }

var ctx = context.Background()

// siteDir is the repo's bridge/site, the pages the server serves (the tests run in cmd/hlsite).
const siteDir = "../../site"

func TestConfigChecks(t *testing.T) {
	good := mockConfig()
	mutate := func(f func(c *Config)) error {
		b, _ := json.Marshal(good)
		var c Config
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		f(&c)
		return c.check()
	}
	cases := []struct {
		name string
		f    func(c *Config)
		want string
	}{
		{"a wrong mint", func(c *Config) { c.Chains[0].Tokens[0].Mint = mockPub("other").String() }, "mint"},
		{"a chain twice", func(c *Config) { c.Chains = append(c.Chains, c.Chains[0]) }, "twice"},
		{"an unknown kind", func(c *Config) { c.Chains[0].Kind = "ethereum" }, "not implemented"},
		{"a realm outside gno.land/r/", func(c *Config) { c.Chains[0].Tokens[1].Realm = "gno.land/p/x" }, "realm"},
		{"no bubble realm", func(c *Config) { c.Gno.Bubble = "" }, "gno.bubble"},
		{"a bad chain id", func(c *Config) { c.Chains[0].ID = "Sol ana" }, "chain id"},
		{"a bad program", func(c *Config) { c.Chains[0].Programs.Admin = "nope" }, "programs.admin"},
		{"no domain", func(c *Config) { c.Chains[0].Domain = 0 }, "domain"},
		{"no node", func(c *Config) { c.Gno.RPC = "" }, "gno.rpc"},
	}
	for _, tc := range cases {
		err := mutate(tc.f)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
	// the defaults from a file: the realm link, the gas, the mint derived from the program
	b, _ := json.Marshal(good)
	var c Config
	_ = json.Unmarshal(b, &c)
	c.Gno.RealmLink, c.Gno.GasWanted, c.Chains[0].Tokens[0].Mint = "", 0, ""
	b, _ = json.Marshal(c)
	path := filepath.Join(t.TempDir(), "site.json")
	_ = os.WriteFile(path, b, 0o600)
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Gno.RealmLink != defaultRealmLink || loaded.Gno.GasWanted != defaultGasWanted || loaded.Chains[0].Tokens[0].Mint != good.Chains[0].Tokens[0].Mint {
		t.Errorf("defaults: %+v", loaded.Gno)
	}
	if got := loaded.Realms(); len(got) != 6 || got[0] != mockNS+"hlmailbox" {
		t.Errorf("realms: %v", got)
	}
}

func TestSolanaSnapshot(t *testing.T) {
	s, _, _ := mockServer(false, siteDir, fixedNow)
	snap := s.others["solana"].Snapshot(ctx, fixedNow())
	if len(snap.Errors) > 0 {
		t.Fatalf("errors: %v", snap.Errors)
	}
	if snap.Supplies["GNOT"] != 1_000_000*one || snap.Supplies["BUBBLE"] != 2_500_000*one-300*one || snap.Slot == 0 {
		t.Errorf("supplies: %+v at slot %d", snap.Supplies, snap.Slot)
	}
	l := snap.Limits["BUBBLE"]
	if l.MaxPerMessage != uint64(25_000*one) || l.DayCap != uint64(100_000*one) || l.Used != 0 {
		t.Errorf("limits: %+v (the bucket drained what was charged two hours ago)", l)
	}
	if snap.Pausable == nil || snap.Admin == nil || len(snap.Pausable.Keys) != 6 || snap.Pausable.Hold || snap.Admin.Hold || snap.Pausable.Flags[0] != nil {
		t.Errorf("guardians: %+v %+v", snap.Pausable, snap.Admin)
	}
	if snap.Pausable.Keys[0] != mockPub("g1").String() || snap.Admin.Keys[4] != mockPub("watch5").String() {
		t.Errorf("seats: %v", snap.Pausable.Keys)
	}
	if snap.Client == nil || snap.Client.TrustedHeight != 1299 || snap.Client.ClockFactor != 1 || snap.Client.QuarantineEnd != 0 {
		t.Errorf("client: %+v", snap.Client)
	}
	if snap.LastSeq != 3 || !snap.SetupDone || len(snap.Pending) != 1 {
		t.Fatalf("admin: last %d setup %v pending %+v", snap.LastSeq, snap.SetupDone, snap.Pending)
	}
	p := snap.Pending[0]
	if p.Number != 3 || p.Kind != 7 || p.KindName != sol.KindName(7) || !strings.Contains(p.Text, "1000") || p.MaturesAt != p.Delivered+7*86400 || p.Deferred != "" {
		t.Errorf("pending: %+v", p)
	}
	// a hold: two guardians' flags on both objects; the 7-day decision waits for its end
	s, _, _ = mockServer(false, siteDir, fixedNow, withHold)
	snap = s.others["solana"].Snapshot(ctx, fixedNow())
	if !snap.Pausable.Hold || !snap.Admin.Hold || snap.Admin.HoldSince == nil || snap.Pausable.Flags[1] == nil {
		t.Errorf("hold: %+v %+v", snap.Pausable, snap.Admin)
	}
	if len(snap.Pending) != 1 || !strings.Contains(snap.Pending[0].Deferred, "hold") {
		t.Errorf("deferred: %+v", snap.Pending)
	}
}

func TestBurnTx(t *testing.T) {
	s, _, _ := mockServer(false, siteDir, fixedNow)
	other := s.others["solana"]
	bubble := *s.cfg.Chains[0].Token("BUBBLE")
	alice := mockPub("alice")
	bt, err := other.BurnTx(ctx, bubble, alice.String(), mockRecipient, uint64(200*one))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(bt.Tx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := solana.TransactionFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !tx.Message.AccountKeys[0].Equals(alice) || len(tx.Signatures) != 2 || !tx.Signatures[0].IsZero() {
		t.Fatalf("the payer is the wallet and its signature slot is empty: keys %v, signatures %v", tx.Message.AccountKeys, tx.Signatures)
	}
	unique := solana.MustPublicKeyFromBase58(bt.Unique)
	msg, _ := tx.Message.MarshalBinary()
	if !tx.Message.AccountKeys[1].Equals(unique) || !ed25519.Verify(ed25519.PublicKey(unique[:]), msg, tx.Signatures[1][:]) {
		t.Fatal("the throwaway key signed in the second slot")
	}
	if tx.Message.RecentBlockhash != mockBlockhash || bt.Blockhash != mockBlockhash.String() {
		t.Errorf("blockhash %s", tx.Message.RecentBlockhash)
	}
	// exactly hlsol send's instructions: the compute budget, then transfer_remote with its accounts in order
	rcpt, _ := hl.GnoAddressToBytes32(mockRecipient)
	want := sol.Synthetic{Program: mockPub("bubble")}.TransferRemoteIx(sol.Mailbox{Program: mockPub("mailbox")}, alice, unique, mockGnoDomain, rcpt, uint64(200*one))
	wantData, _ := want.Data()
	budget, _ := sol.ComputeLimitIx(400_000).Data()
	ix0, ix1 := tx.Message.Instructions[0], tx.Message.Instructions[1]
	if !tx.Message.AccountKeys[ix0.ProgramIDIndex].Equals(sol.ComputeBudget) || !bytes.Equal(ix0.Data, budget) {
		t.Errorf("instruction 0: %x", ix0.Data)
	}
	if !tx.Message.AccountKeys[ix1.ProgramIDIndex].Equals(mockPub("bubble")) || !bytes.Equal(ix1.Data, wantData) {
		t.Errorf("instruction 1: %x", ix1.Data)
	}
	if len(ix1.Accounts) != len(want.Accounts()) {
		t.Fatalf("%d accounts, want %d", len(ix1.Accounts), len(want.Accounts()))
	}
	for i, idx := range ix1.Accounts {
		if !tx.Message.AccountKeys[idx].Equals(want.Accounts()[i].PublicKey) {
			t.Errorf("account %d: %s, want %s", i, tx.Message.AccountKeys[idx], want.Accounts()[i].PublicKey)
		}
	}
	if bt.MessageAccount != (sol.Mailbox{Program: mockPub("mailbox")}).DispatchedMessage(unique).String() {
		t.Errorf("message account %s", bt.MessageAccount)
	}
	// the refusals: a bridge realm's address (K21), a bad recipient, a bad sender, a zero amount
	for _, tc := range []struct {
		sender, to string
		amount     uint64
		want       string
	}{
		{alice.String(), hl.GnoPackageAddress(mockNS + "hlbubble"), 1, "bridge realm"},
		{alice.String(), strings.ToUpper(hl.GnoPackageAddress(mockNS + "hlgnot")), 1, "bridge realm"},
		{alice.String(), "g1nope", 1, "gno.land address"},
		{"nope", mockRecipient, 1, "Solana address"},
		{alice.String(), mockRecipient, 0, "above zero"},
	} {
		_, err := other.BurnTx(ctx, bubble, tc.sender, tc.to, tc.amount)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s → %s × %d: %v, want %q", tc.sender, tc.to, tc.amount, err, tc.want)
		}
	}
}

func TestResolveTransfers(t *testing.T) {
	s, _, _ := mockServer(true, siteDir, fixedNow)
	ch, other := &s.cfg.Chains[0], s.others["solana"]
	// out, by message id: dispatched on the first two looks, delivered on the third
	for i, want := range []string{"dispatched", "dispatched", "delivered"} {
		tr, err := s.resolve(ctx, ch, other, "0x"+strings.ToUpper(mockOutID))
		if err != nil {
			t.Fatal(err)
		}
		if tr.Direction != "out" || tr.Token != "GNOT" || tr.Amount != 10*one || tr.From != mockSender || tr.State != want || tr.Kind != "message" || len(tr.Steps) != 2 || tr.Steps[1].Done != (want == "delivered") {
			t.Errorf("look %d: %+v", i, tr)
		}
		if want == "delivered" && (!strings.Contains(tr.Steps[1].Link, "explorer.solana.com/tx/") || !strings.Contains(tr.Steps[1].Link, "cluster=devnet")) {
			t.Errorf("the delivery's link: %s", tr.Steps[1].Link)
		}
	}
	// out, by the wallet's gno.land transaction hash
	tr, err := s.resolve(ctx, ch, other, mockGnoTx)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Kind != "gnotx" || tr.ID != mockOutID || tr.Ref != mockGnoTx || !strings.Contains(tr.Steps[0].Link, "txhash="+mockGnoTx) {
		t.Errorf("by gno tx: %+v", tr)
	}
	// in, by the Solana signature: credited, credited, paying, paid
	for i, want := range []string{"credited", "credited", "paying", "paid"} {
		tr, err := s.resolve(ctx, ch, other, mockBurnTx)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Direction != "in" || tr.Kind != "signature" || tr.Token != "BUBBLE" || tr.Amount != 200*one || tr.To != mockRecipient || tr.ID != mockInID || tr.State != want || len(tr.Steps) != 3 {
			t.Errorf("look %d: %+v", i, tr)
		}
		if !tr.Steps[0].Done || !strings.Contains(tr.Steps[0].Link, "/tx/"+mockBurnTx) || !tr.Steps[1].Done || !strings.Contains(tr.Steps[1].Text, "height 1203") || tr.Steps[2].Done != (want == "paid") {
			t.Errorf("look %d steps: %+v", i, tr.Steps)
		}
		if want == "paying" && tr.Paid != 100*one {
			t.Errorf("paying: paid %d", tr.Paid)
		}
	}
	// in, by its message id
	if tr, err := s.resolve(ctx, ch, other, mockInID); err != nil || tr.Direction != "in" || tr.Kind != "message" || tr.State != "paid" {
		t.Errorf("by id: %+v %v", tr, err)
	}
	// unknown references
	for _, ref := range []string{"deadbeef", strings.Repeat("ab", 32), "", strings.Repeat("x", 200)} {
		if _, err := s.resolve(ctx, ch, other, ref); !errors.Is(err, errUnknownTransfer) {
			t.Errorf("%q: %v", ref, err)
		}
	}
	h := sha512.Sum512([]byte("another signature"))
	if _, err := s.resolve(ctx, ch, other, solana.SignatureFromBytes(h[:]).String()); !errors.Is(err, errUnknownTransfer) || !strings.Contains(err.Error(), "no transaction") {
		t.Errorf("an unknown signature: %v", err)
	}
	if _, err := s.resolve(ctx, ch, other, base64.StdEncoding.EncodeToString(sum256("another tx"))); !errors.Is(err, errUnknownTransfer) {
		t.Errorf("an unknown gno tx: %v", err)
	}
}

func TestIdentityLine(t *testing.T) {
	s, g, ms := mockServer(false, siteDir, fixedNow)
	st, err := s.status(ctx, &s.cfg.Chains[0], s.others["solana"])
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Errors) > 0 || len(st.Tokens) != 2 {
		t.Fatalf("%+v", st)
	}
	for _, tok := range st.Tokens {
		if tok.Identity != "holds" || tok.Gap != 0 || tok.Supply == nil || !strings.HasPrefix(tok.IdentityLine, "holds — ") {
			t.Errorf("%s: %s gap %d: %s", tok.Symbol, tok.Identity, tok.Gap, tok.IdentityLine)
		}
	}
	if st.Tokens[1].Owed != 300*one || st.Tokens[1].OpenClaims != 1 || st.Tokens[0].Fee != 5*one || st.Tokens[1].Minimum != 200*one || st.Tokens[0].Maximum != 25_000*one {
		t.Errorf("figures: %+v", st.Tokens)
	}
	if st.Tokens[0].Limit == nil || st.Tokens[0].Limit.DayCap != uint64(100_000*one) || st.Tokens[0].Mismatch != "" {
		t.Errorf("limits: %+v", st.Tokens[0].Limit)
	}
	if !strings.Contains(st.Gno.GovernorLine, mockGovernor) || st.Gno.DAOGoverns || st.Gno.Signers != 3 || st.Gno.Rule != "2 of 3 signers" || len(st.Gno.Seats) != 6 || st.Gno.Gov == nil || st.Gno.Gov.Last != 3 {
		t.Errorf("gno: %+v", st.Gno)
	}
	if len(st.Decisions) != 1 || st.Decisions[0].Kind != "set treasurer" || st.Decisions[0].Realm != mockNS+"hlfees" {
		t.Errorf("decisions: %+v", st.Decisions)
	}
	// a supply above the lock breaks it; what is in flight shows as the gap
	ms.supplies[sol.Synthetic{Program: mockPub("gnot")}.Mint()] = 1_000_000*one + 1
	g.polls = map[string]int{}
	st, _ = s.status(ctx, &s.cfg.Chains[0], s.others["solana"])
	if st.Tokens[0].Identity != "BROKEN" || !strings.HasPrefix(st.Tokens[0].IdentityLine, "BROKEN: the right side exceeds the lock by 1") {
		t.Errorf("%s: %s", st.Tokens[0].Identity, st.Tokens[0].IdentityLine)
	}
	ms.supplies[sol.Synthetic{Program: mockPub("gnot")}.Mint()] = 1_000_000*one - 5*one
	st, _ = s.status(ctx, &s.cfg.Chains[0], s.others["solana"])
	if st.Tokens[0].Identity != "holds" || st.Tokens[0].Gap != 5*one || !strings.HasPrefix(st.Tokens[0].IdentityLine, "holds; 5000000 in flight") {
		t.Errorf("%s gap %d: %s", st.Tokens[0].Identity, st.Tokens[0].Gap, st.Tokens[0].IdentityLine)
	}
	// a flag on gno.land and a paused function reach the status
	g.flags["hlbubble"] = []string{"credits by seat 5 (" + mockSender + ") at 1800000000"}
	g.paused["hlbubble"] = "credits"
	st, _ = s.status(ctx, &s.cfg.Chains[0], s.others["solana"])
	if st.Tokens[1].Paused != "credits" {
		t.Errorf("paused: %q", st.Tokens[1].Paused)
	}
	if len(st.Gno.Flags) != 1 || st.Gno.Flags[0].Fn != "credits" || st.Gno.Flags[0].Seat != 5 || st.Gno.Flags[0].Addr != mockSender || st.Gno.Flags[0].Route != mockNS+"hlbubble" {
		t.Errorf("flags: %+v", st.Gno.Flags)
	}
}

func TestAPI(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	s, _, _ := mockServer(false, siteDir, fixedNow)
	h := s.handler()
	do := func(method, path, body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if key != "" {
			req.Header.Set("X-Forwarded-For", key+", 10.0.0.1")
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	// the chains
	rr := do("GET", "/api/chains", "", "")
	var chains chainsJSON
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &chains) != nil || len(chains.Chains) != 1 || chains.Chains[0].ID != "solana" || len(chains.Chains[0].Tokens) != 2 {
		t.Fatalf("chains: %d %s", rr.Code, rr.Body.String())
	}
	if chains.Gno.GasFee != 60_000 || chains.Gno.GasWanted != defaultGasWanted || chains.Gno.GasPrice == nil || chains.Chains[0].Tokens[1].RealmAddress != hl.GnoPackageAddress(mockNS+"hlbubble") {
		t.Errorf("gno: %+v tokens %+v", chains.Gno, chains.Chains[0].Tokens)
	}
	// the headers, on the API and on the pages
	for _, p := range []string{"/api/chains", "/", "/bridge", "/status", "/help", "/site.css", "/bridge.js"} {
		rr := do("GET", p, "", "")
		csp := rr.Header().Get("Content-Security-Policy")
		if rr.Code != 200 || !strings.Contains(csp, "connect-src 'self'") || !strings.Contains(csp, "default-src 'none'") || rr.Header().Get("X-Frame-Options") != "DENY" || rr.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: %d %v", p, rr.Code, rr.Header())
		}
		if strings.HasPrefix(p, "/api/") && rr.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: cache %q", p, rr.Header().Get("Cache-Control"))
		}
		if !strings.HasPrefix(p, "/api/") && strings.Contains(strings.ToLower(rr.Body.String()), "claude") {
			t.Errorf("%s names the model", p)
		}
	}
	for _, p := range []string{"/nope", "/e2e/run.js", "/go.mod", "/deploy.sh", "/site.json", "/api/nope", "/api/ethereum/status", "/api/solana/transfer/deadbeef"} {
		if rr := do("GET", p, "", ""); rr.Code != 404 {
			t.Errorf("%s: %d, want 404", p, rr.Code)
		}
	}
	// the status
	rr = do("GET", "/api/solana/status", "", "")
	var st statusJSON
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &st) != nil || len(st.Tokens) != 2 || st.Tokens[0].Identity != "holds" || st.Other == nil || len(st.Other.Pending) != 1 {
		t.Fatalf("status: %d %s", rr.Code, rr.Body.String())
	}
	// the transfers
	rr = do("GET", "/api/solana/transfer/"+mockBurnTx, "", "")
	var tr transferJSON
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &tr) != nil || tr.State != "paid" {
		t.Fatalf("transfer: %d %s", rr.Code, rr.Body.String())
	}
	// the burn builder
	for _, tc := range []struct {
		body string
		code int
		want string
	}{
		{`{"token":"BUBBLE","sender":"` + mockPub("alice").String() + `","recipient":"` + mockRecipient + `","amount":"200000000"}`, 200, `"tx":"`},
		{`{"token":"BUBBLE","sender":"` + mockPub("alice").String() + `","recipient":"` + mockRecipient + `","amount":200000000}`, 200, `"messageAccount"`},
		{`{"token":"BUBBLE","sender":"` + mockPub("alice").String() + `","recipient":"` + hl.GnoPackageAddress(mockNS+"hlgnot") + `","amount":1}`, 400, "bridge realm"},
		{`{"token":"ETH","sender":"x","recipient":"y","amount":1}`, 400, "unknown token"},
		{`{"token":"GNOT","sender":"x","recipient":"y","amount":"1.5"}`, 400, "whole number"},
		{`not json`, 400, "JSON body"},
	} {
		rr := do("POST", "/api/solana/burn-tx", tc.body, "")
		if rr.Code != tc.code || !strings.Contains(rr.Body.String(), tc.want) {
			t.Errorf("%s: %d %s, want %d %q", tc.body, rr.Code, rr.Body.String(), tc.code, tc.want)
		}
	}
	if rr := do("GET", "/api/solana/burn-tx", "", ""); rr.Code != 405 || rr.Header().Get("Allow") != "POST" {
		t.Errorf("GET burn-tx: %d %v", rr.Code, rr.Header())
	}
	// the limiter: a burst of thirty, the thirty-first refused, another client unaffected
	s2, _, _ := mockServer(false, siteDir, fixedNow)
	h2 := s2.handler()
	last := 0
	for i := 0; i < 31; i++ {
		req := httptest.NewRequest("GET", "/api/chains", nil)
		req.Header.Set("X-Forwarded-For", "203.0.113.9")
		rr := httptest.NewRecorder()
		h2.ServeHTTP(rr, req)
		last = rr.Code
		if i < 30 && rr.Code != 200 {
			t.Fatalf("request %d: %d", i, rr.Code)
		}
		if i == 30 && rr.Header().Get("Retry-After") == "" {
			t.Error("no Retry-After")
		}
	}
	if last != 429 {
		t.Errorf("the thirty-first: %d", last)
	}
	req := httptest.NewRequest("GET", "/api/chains", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	rr = httptest.NewRecorder()
	h2.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("another client: %d", rr.Code)
	}
	// a second later the bucket has two tokens again
	s2.now = func() time.Time { return fixedNow().Add(time.Second) }
	req = httptest.NewRequest("GET", "/api/chains", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rr = httptest.NewRecorder()
	h2.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("after a second: %d", rr.Code)
	}
	// nothing personal in the log: no client key, no address, no amount
	for _, secret := range []string{"203.0.113", "10.0.0.1", mockRecipient, mockPub("alice").String(), "200000000"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the log carries %q:\n%s", secret, logs.String())
		}
	}
	if s.mockInfo == nil {
		t.Fatal("the mock serves its ids")
	}
	if rr := do("GET", "/api/mock", "", ""); rr.Code != 200 || !strings.Contains(rr.Body.String(), mockOutID) {
		t.Errorf("/api/mock: %d %s", rr.Code, rr.Body.String())
	}
}

func TestStatusCachesAndReads503(t *testing.T) {
	s, _, _ := mockServer(false, siteDir, fixedNow)
	h := s.handler()
	get := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/solana/status", nil))
		return rr
	}
	if rr := get(); rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	// the node going away is served from the cache for ten seconds, then a 503 that says so
	s.gno = failingGno{}
	if rr := get(); rr.Code != 200 {
		t.Errorf("cached: %d", rr.Code)
	}
	s.now = func() time.Time { return fixedNow().Add(statusTTL + time.Second) }
	if rr := get(); rr.Code != 503 || !strings.Contains(rr.Body.String(), "gno.land") {
		t.Errorf("after the cache: %d %s", rr.Code, rr.Body.String())
	}
}

// failingGno is a node that answers nothing.
type failingGno struct{}

func (failingGno) RenderAt(context.Context, string, string, int64) (string, error) {
	return "", errors.New("connection refused")
}
func (failingGno) Status(context.Context) (gno.Head, error) {
	return gno.Head{}, errors.New("connection refused")
}
func (failingGno) Block(context.Context, int64) (gno.Head, error) {
	return gno.Head{}, errors.New("connection refused")
}
func (failingGno) TxEvents(context.Context, string) (*gnoTx, error) {
	return nil, errors.New("connection refused")
}
func (failingGno) GasPrice(context.Context) (gno.GasPrice, error) {
	return gno.GasPrice{}, errors.New("connection refused")
}

// TestWalletsUnderCSP pins the policy the pages are served under: nothing from anywhere but this origin, so the
// page can neither call an RPC nor load a script from a CDN (the wallet extensions inject themselves).
func TestWalletsUnderCSP(t *testing.T) {
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("the policy lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe") || strings.Contains(csp, "http") {
		t.Errorf("the policy opens something: %s", csp)
	}
	// the pages reference no outside origin
	for _, f := range []string{"index.html", "bridge.html", "status.html", "help.html", "common.js", "wallets.js", "bridge.js", "status.js", "home.js", "help.js", "site.css"} {
		b, err := os.ReadFile(filepath.Join(siteDir, f))
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, bad := range []string{"src=\"http", "href=\"http://", "fetch('http", "fetch(\"http", "import 'http", "cdn.", "unpkg", "jsdelivr"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s loads from outside: %q", f, bad)
			}
		}
		// no inline script: the policy (script-src 'self') refuses one, so a page carrying one is broken
		if strings.HasSuffix(f, ".html") && strings.Contains(src, "<script>") || strings.Contains(src, "<script type=\"module\">") {
			t.Errorf("%s has an inline script", f)
		}
	}
	// the wallet-facing message a GNOT send carries is the exact envelope the realm requires: amount + fee in ugnot
	b, _ := os.ReadFile(filepath.Join(siteDir, "bridge.js"))
	if !strings.Contains(string(b), "ugnot") || !strings.Contains(string(b), "TransferRemote") || !strings.Contains(string(b), "Approve") {
		t.Error("bridge.js does not build the realm's calls")
	}
}

func init() {
	// the mock's known burn id is the message's id, as the realm computes it
	if id := mockBurnMessage().ID(); !strings.EqualFold(mockInID, hexOf(id[:])) {
		panic("the mock's burn id")
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 2*len(b))
	for i, c := range b {
		out[2*i], out[2*i+1] = digits[c>>4], digits[c&15]
	}
	return string(out)
}

// TestPreview: the public preview before the bridge is deployed says so in /api/chains, serves no /api/mock and
// refuses the builder — a valid burn request included — so no wallet is ever handed a transaction for programs that
// do not exist; the pages carry the banner's hook and the bridge page disables its wallet buttons.
func TestPreview(t *testing.T) {
	s, _, _ := mockServer(false, siteDir, fixedNow)
	s.preview, s.mockInfo = true, nil
	h := s.handler()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rr
	}
	var chains chainsJSON
	if rr := do("GET", "/api/chains", ""); rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &chains) != nil || !chains.Preview {
		t.Fatalf("chains under preview: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do("GET", "/api/mock", ""); rr.Code == 200 {
		t.Errorf("/api/mock served under preview: %s", rr.Body.String())
	}
	valid := `{"token":"BUBBLE","sender":"` + mockPub("alice").String() + `","recipient":"` + mockRecipient + `","amount":"200000000"}`
	if rr := do("POST", "/api/solana/burn-tx", valid); rr.Code != 503 || !strings.Contains(rr.Body.String(), "not deployed") || strings.Contains(rr.Body.String(), `"tx"`) {
		t.Errorf("burn-tx under preview: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do("GET", "/api/solana/status", ""); rr.Code != 200 {
		t.Errorf("status under preview: %d", rr.Code)
	}
	// without the flag the same server builds the transaction and says nothing of a preview
	s2, _, _ := mockServer(false, siteDir, fixedNow)
	rr := httptest.NewRecorder()
	s2.handler().ServeHTTP(rr, httptest.NewRequest("POST", "/api/solana/burn-tx", strings.NewReader(valid)))
	if rr.Code != 200 {
		t.Errorf("burn-tx without preview: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	s2.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/chains", nil))
	if strings.Contains(rr.Body.String(), `"preview"`) {
		t.Errorf("chains without preview mention it: %s", rr.Body.String())
	}
	for _, f := range []struct{ file, want string }{
		{"common.js", "the bridge is not deployed yet"},
		{"bridge.js", "if (state.c.preview)"},
		{"status.js", "PREVIEW: example figures"},
	} {
		b, err := os.ReadFile(siteDir + "/" + f.file)
		if err != nil || !strings.Contains(string(b), f.want) {
			t.Errorf("%s does not carry %q", f.file, f.want)
		}
	}
}

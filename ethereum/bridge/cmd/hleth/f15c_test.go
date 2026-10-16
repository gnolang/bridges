package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// Round 4's tools findings (fold lane F15c): E4-TL1 (the audit PASSed on the deployer's seats, watch keys,
// beneficiary and day caps; through -config the locks were matched by the record's own symbols), E4-TL2 (one gno
// source's "not found" turned the others' FAIL into a PASS), E4-TL6 (an honest governed bridge FAILed while an r/hlgov
// decision was in flight; the seat flags are the launch values; the members' decision commands did not follow a
// kind 9). Each test was run on 1a6770b5's code first, where it fails (fold-r4/f15c.md).

// launchFlags gives an audit the member's launch values as the setup used them: the seats, the watch keys, the
// beneficiary, and each token's expected lock — the setup's, or on a local chain with none one that compiles §5.4's
// ceilings (a million: a day cap of 100,000, a maximum of 25,000), what such a setup deploys.
func withLaunchFlags(a auditConfig, cfg setupConfig) auditConfig {
	var seats, watch []string
	for _, s := range cfg.Seats {
		seats = append(seats, s.Hex())
	}
	for _, w := range cfg.Watch {
		watch = append(watch, w.Hex())
	}
	a.Seats, a.Watch, a.Beneficiary = strings.Join(seats, ","), strings.Join(watch, ","), cfg.Beneficiary.Hex()
	a.ExpectedLock = cfg.ExpectedLock
	for i := range a.ExpectedLock {
		if a.ExpectedLock[i] == 0 {
			a.ExpectedLock[i] = 1_000_000_000_000
		}
	}
	return a
}

func auditOf(t *testing.T, c auditConfig) (*auditResult, string) {
	t.Helper()
	var b bytes.Buffer
	res, err := runAuditResult(context.Background(), c, &b)
	if err != nil {
		t.Fatalf("audit: %v\n%s", err, b.String())
	}
	return res, b.String()
}

func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// E4-TL1 (1): a deployer seats keys of its own, names itself the beneficiary and takes the ceilings as its day caps;
// the member audits before the realms go up with a gno source and the four path flags. Every other check agrees with
// the deployer's record, so without the member's -seats, -watch, -beneficiary and expected locks the verdict is
// UNVERIFIED, naming each, never PASS (it PASSed); with the launch notice's published values the deployment FAILs on
// each; with values that are the deployment's, PASS.
func TestAnvilAuditUnverifiedWithoutTheLaunchValues(t *testing.T) {
	artifactsOrSkip(t)
	url, _ := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cfg := testSetupConfig(t, url, dir)
	own := []string{"0x14dC79964da2C08b23698B3D3cc7Ca32193d9955", "0x23618e81E3f5cdF7f54C3d65f7FBc0aBf5B21E8f", "0xa0Ee7A142d267C1f36714E4a8F75612F20a79720",
		"0xBcd4042DE499D14e55001CcbB24a551F3b954096", "0x71bE63f3384f5fb98995898A86B02Fb2426c5788", "0xFABB0ac9d68B0B445fB7357272Ff202C5651694a"}
	published := cfg // the launch notice's: anvil's accounts 1–6, the treasurer account 1, a lock of 50,000 each
	for i := 0; i < 4; i++ {
		cfg.Seats[i] = common.HexToAddress(own[i])
	}
	cfg.Watch[0], cfg.Watch[1] = common.HexToAddress(own[4]), common.HexToAddress(own[5])
	cfg.Beneficiary = common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266") // the deployer itself
	var out bytes.Buffer
	if _, err := runSetup(ctx, cfg, &out); err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	fg := honestGno(t, cfg, d)
	fg.absent = true // the realms are not up: the audit the plan runs before their upload
	base := auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest", GnoRPC: fg.serve(t).URL,
		MailboxPkgPath: cfg.MailboxPkgPath, Hlgov: cfg.Hlgov, GnotRoute: cfg.GnotRoute, BubbleRoute: cfg.BubbleRoute}

	res, text := auditOf(t, base)
	if len(res.Fails) != 0 || strings.Contains(text, "hleth audit: PASS") || !strings.Contains(text, "hleth audit: UNVERIFIED (4)") {
		t.Fatalf("the deployer's own seats, beneficiary and caps, audited with a gno source and the four paths alone: fails %d, unverified %d — must be UNVERIFIED (4), never PASS\n%s", len(res.Fails), len(res.Unverified), text)
	}
	for _, want := range []string{"the guardians and watch keys at launch (" + strings.Join(own, ","), "the protocol fee's beneficiary at launch (0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266)",
		"token 0's launch limits (25000000000 a message, 100000000000 a day): the deployment's record, bounded only by §5.4's ceilings — give -expected-lock-gnot",
		"token 1's launch limits (25000000000 a message, 100000000000 a day): the deployment's record, bounded only by §5.4's ceilings — give -expected-lock-bubble"} {
		if !hasLine(res.Unverified, want) {
			t.Fatalf("no UNVERIFIED line says %q:\n%s", want, strings.Join(res.Unverified, "\n"))
		}
	}

	// the launch notice's published values: the deployment FAILs on each
	c := withLaunchFlags(base, published)
	c.ExpectedLock = [2]uint64{50_000_000_000, 50_000_000_000} // a testing phase's 50,000: a day cap of 5,000
	c.LaunchMaximum = 5_000_000_000
	res, text = auditOf(t, c)
	if len(res.Unverified) != 0 || strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("the published launch values: unverified %d\n%s", len(res.Unverified), text)
	}
	for _, want := range []string{"the gate's seats 1–4 and watch slots 5–6 are", "the ProtocolFee's beneficiary is 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266 (must be " + published.Beneficiary.Hex(),
		"the gate's limits for token 0: 25000000000 a message, 100000000000 a day (must be 5000000000 and 5000000000"} {
		if !hasLine(res.Fails, want) {
			t.Fatalf("no failure says %q:\n%s", want, strings.Join(res.Fails, "\n"))
		}
	}

	// the member's values are the deployment's: PASS
	res, text = auditOf(t, withLaunchFlags(base, cfg))
	if len(res.Fails) != 0 || len(res.Unverified) != 0 || !strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("every launch value given and held: fails %d unverified %d\n%s", len(res.Fails), len(res.Unverified), text)
	}
}

// E4-TL1 (2): the expected locks are matched to the tokens by their routes, never by the record's own symbols. A
// deployer names token 0 — the GNOT route's, by -gnot-route — "BUBBLE" and compiles its gate limits from BUBBLE's lock
// (a day cap of 100,000), token 1's from GNOT's (30,000); the member gives GNOT's lock as -expected-lock-gnot (token
// 0's, the -gnot-route's). Matched by the record's symbols, both caps passed; by route, both FAIL. A config's routes'
// locks — matched to the tokens by address — are the operator's since E5-CF3 item 3: noted, never taken, so with the
// config alone both limits are UNVERIFIED (fold-r5 F17d; this test took them from the config before).
func TestAnvilAuditMatchesTheConfigsLocksByAddress(t *testing.T) {
	artifactsOrSkip(t)
	url, _ := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cfg := testSetupConfig(t, url, dir)
	cfg.Name0, cfg.Symbol0, cfg.Name1, cfg.Symbol1 = "BUBBLE", "BUBBLE", "GNOT", "GNOT"
	cfg.MaxPerMessage, cfg.DayCap = [2]uint64{}, [2]uint64{}
	cfg.ExpectedLock = [2]uint64{2_000_000_000_000, 300_000_000_000} // BUBBLE's lock on token 0, GNOT's on token 1
	var out bytes.Buffer
	if _, err := runSetup(ctx, cfg, &out); err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Launch.DayCap != [2]uint64{100_000_000_000, 30_000_000_000} {
		t.Fatalf("the deployer's caps: %v", d.Launch.DayCap)
	}
	fg := honestGno(t, cfg, d)
	fg.absent = true
	conf := filepath.Join(dir, "bridge.json")
	b, _ := json.Marshal(map[string]any{"gno": map[string]any{"rpc": fg.serve(t).URL},
		"routes": []map[string]any{{"symbol": "GNOT", "gno": cfg.GnotRoute, "token": d.Addr("token0").Hex(), "expectedLock": 300_000_000_000},
			{"symbol": "BUBBLE", "gno": cfg.BubbleRoute, "token": d.Addr("token1").Hex(), "expectedLock": 2_000_000_000_000}}})
	if err := os.WriteFile(conf, b, 0o644); err != nil {
		t.Fatal(err)
	}
	c := withLaunchFlags(auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest", Config: conf,
		MailboxPkgPath: cfg.MailboxPkgPath, Hlgov: cfg.Hlgov, GnotRoute: cfg.GnotRoute, BubbleRoute: cfg.BubbleRoute}, cfg)
	c.ExpectedLock = [2]uint64{300_000_000_000, 2_000_000_000_000} // the launch notice's: GNOT's (-gnot-route's), BUBBLE's
	res, text := auditOf(t, c)
	if strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("the locks matched by the record's symbols: PASS\n%s", text)
	}
	for _, want := range []string{"the gate's limits for token 0: 25000000000 a message, 100000000000 a day (must be 25000000000 and 30000000000",
		"the gate's limits for token 1: 25000000000 a message, 30000000000 a day (must be 25000000000 and 100000000000"} {
		if !hasLine(res.Fails, want) {
			t.Fatalf("no failure says %q:\n%s", want, strings.Join(res.Fails, "\n"))
		}
	}
	if !strings.Contains(text, "compiled from its expected lock 300000000000 (-expected-lock-gnot)") {
		t.Fatalf("token 0's lock is not -expected-lock-gnot's:\n%s", text)
	}
	// the config's locks alone: the operator's, noted by the token's address, never taken
	c.ExpectedLock = [2]uint64{}
	res, text = auditOf(t, c)
	if hasLine(res.Fails, "the gate's limits for token 0") || strings.Contains(text, "hleth audit: PASS") ||
		!hasLine(res.Unverified, "the operator's config (-config's route of token 0's address "+d.Addr("token0").Hex()+", its realm "+cfg.GnotRoute+") expects 300000000000") {
		t.Fatalf("the config's locks alone: %d failures (want none of the limits), the config's lock noted:\n%s", len(res.Fails), text)
	}
}

// fakeSource is one gno node for gnoAgree: r/hlgov's json, or the keeper's not-found for it, or any other answer.
func fakeSource(t *testing.T, answer func(w http.ResponseWriter, pkg string)) gnoSource {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params map[string]string `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		data := req.Params["data"]
		answer(w, data)
	}))
	t.Cleanup(srv.Close)
	return gnoSource{url: srv.URL, c: &gno.Client{RPC: srv.URL}}
}

// E4-TL2, the reading itself: every source is read before judging; the realm is absent only when every source answers
// the keeper's own not-found for its package; one source's not-found beside another's answer is a disagreement (a
// node behind the realm's upload), never the realm's absence; any other "not found" (a JSON-RPC "Method not found",
// another package's) is a failure to read.
func TestGnoAgreeAbsentOnlyWhenEverySourceSaysSo(t *testing.T) {
	const pkg = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlgov"
	reply := func(w http.ResponseWriter, v any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": v})
		_, _ = w.Write(b)
	}
	up := fakeSource(t, func(w http.ResponseWriter, _ string) {
		reply(w, map[string]any{"response": map[string]any{"ResponseBase": map[string]any{"Error": nil, "Data": "eyJsYXN0IjozfQ=="}}}) // {"last":3}
	})
	notFoundOf := func(path string) gnoSource {
		return fakeSource(t, func(w http.ResponseWriter, _ string) {
			reply(w, map[string]any{"response": map[string]any{"ResponseBase": map[string]any{"Error": map[string]any{"@type": "/vm.InvalidPkgPathError"},
				"Log": "--= Error =--\nData: vm.InvalidPkgPathError{}\nMsg Traces:\n    0  gno.land/pkg/sdk/vm/keeper.go:1883 - package not found: " + path + "\n--= /Error =--\n"}}})
		})
	}
	lagging, lagging2 := notFoundOf(pkg), notFoundOf(pkg)
	other := notFoundOf(pkg + "2") // another package's not-found: a path that begins with this one's
	method := fakeSource(t, func(w http.ResponseWriter, _ string) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32601, "message": "Method not found"}})
		_, _ = w.Write(b)
	})
	read := func(srcs ...gnoSource) (string, error) {
		a := &audit{out: io.Discard, gnos: srcs}
		return a.gnoRender(context.Background(), pkg, "json")
	}
	if s, err := read(up); err != nil || s != `{"last":3}` {
		t.Fatalf("one source up: %q %v", s, err)
	}
	if _, err := read(lagging, lagging2); !realmAbsent(err) {
		t.Fatalf("every source says the keeper's not-found: not absent (%v)", err)
	}
	for name, srcs := range map[string][]gnoSource{
		"a lagging source first, one up after": {lagging, up},
		"one up first, a lagging source after": {up, lagging},
		"two lagging, one up":                  {lagging, up, lagging2},
	} {
		_, err := read(srcs...)
		if err == nil || realmAbsent(err) || !strings.Contains(err.Error(), "the gno sources disagree") || !strings.Contains(err.Error(), "not on the chain") {
			t.Errorf("%s: read as %v (want a disagreement, never the realm's absence)", name, err)
		}
	}
	for name, srcs := range map[string][]gnoSource{
		"a JSON-RPC Method not found":          {method},
		"another package's not-found":          {other},
		"a lagging source and a failing one":   {lagging, method},
		"the keeper's not-found and another's": {lagging, other},
	} {
		if _, err := read(srcs...); err == nil || realmAbsent(err) {
			t.Errorf("%s: read as %v (want a failure to read, never the realm's absence)", name, err)
		}
	}
	if keeperNotFound(fmt.Errorf("gno query vm/qrender: map[@type:/vm.InvalidPkgPathError] package not found: %s", pkg), pkg) != true {
		t.Error("the keeper's not-found of the package itself is not recognised")
	}
}

// E4-TL2's class, the reading of a route: its json carries the meter, which refills by the block time, so two sources
// a block apart (or one source read across a block) never render it byte for byte alike; the sources agree on what
// the audit reads of it — the per-message maximum and the lock — and differ there only as a disagreement.
func TestRouteReadAgreesOnTheFiguresRead(t *testing.T) {
	const route = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlgnot"
	source := func(maximum, lock, meter int64) gnoSource {
		return fakeSource(t, func(w http.ResponseWriter, _ string) {
			s := fmt.Sprintf(`{"address":"g1x","live":true,"lock":%d,"meter":%d,"cap":100000000000,"maximum":%d,"minimum":1000000}`, lock, meter, maximum)
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"response": map[string]any{"ResponseBase": map[string]any{"Error": nil, "Data": []byte(s)}}}})
			_, _ = w.Write(b)
		})
	}
	read := func(srcs ...gnoSource) (*gnoside.Route, error) {
		a := &audit{out: io.Discard, gnos: srcs}
		return a.routeRead(context.Background(), route)
	}
	r, err := read(source(25_000_000_000, 40_000_000_000, 354_524_074_074), source(25_000_000_000, 40_000_000_000, 355_324_074_074))
	if err != nil || r == nil || r.Maximum != 25_000_000_000 || r.Lock != 40_000_000_000 {
		t.Fatalf("two sources a block apart (the meter refilled between them): %+v %v", r, err)
	}
	if _, err := read(source(25_000_000_000, 40_000_000_000, 1), source(30_000_000_000, 40_000_000_000, 1)); err == nil || !strings.Contains(err.Error(), "the gno sources disagree") {
		t.Fatalf("two sources whose maximums differ: %v", err)
	}
}

// E4-TL2 on a deployment: the member gives every launch value; r/hlgov is up on their first source, its Ethereum seat
// 3 another key than the deployment's — FAIL — and their second source is a node behind the realms' upload. The
// lagging source made r/hlgov read as absent and the audit PASSed; now the two disagree, and the reading FAILs.
func TestAnvilAuditLaggingSourceIsNoAbsence(t *testing.T) {
	artifactsOrSkip(t)
	url, _ := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cfg := testSetupConfig(t, url, dir)
	var out bytes.Buffer
	if _, err := runSetup(ctx, cfg, &out); err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	upG := honestGno(t, cfg, d)
	upG.seats[2] = "0xa0Ee7A142d267C1f36714E4a8F75612F20a79720" // r/hlgov's seat 3: not the deployment's
	lagG := honestGno(t, cfg, d)
	lagG.absent = true
	upURL, lagURL := upG.serve(t).URL, lagG.serve(t).URL
	base := withLaunchFlags(auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest",
		MailboxPkgPath: cfg.MailboxPkgPath, Hlgov: cfg.Hlgov, GnotRoute: cfg.GnotRoute, BubbleRoute: cfg.BubbleRoute}, cfg)
	c := base
	c.GnoRPC = upURL
	res, text := auditOf(t, c)
	if !hasLine(res.Fails, "seat 3: the line holds "+d.Launch.Seats[2]+", but r/hlgov's Ethereum seat is 0xa0Ee7A142d267C1f36714E4a8F75612F20a79720") {
		t.Fatalf("r/hlgov's seat 3 another key, one source: %d failures\n%s", len(res.Fails), text)
	}
	c.GnoRPC = upURL + "," + lagURL
	res, text = auditOf(t, c)
	if len(res.Fails) == 0 || strings.Contains(text, "hleth audit: PASS") || !hasLine(res.Fails, "the gno sources disagree") {
		t.Fatalf("the same with a source behind the realms' upload: %d failures (want a disagreement, never r/hlgov's absence)\n%s", len(res.Fails), text)
	}
	// every source behind the upload (the realms not up, as the plan's audit before the upload): absent, held as such
	lag2 := honestGno(t, cfg, d)
	lag2.absent = true
	c.GnoRPC = lagURL + "," + lag2.serve(t).URL
	if res, text = auditOf(t, c); len(res.Fails) != 0 || !strings.Contains(text, "hleth audit: PASS") || !strings.Contains(text, "is not on the chain yet") {
		t.Fatalf("every source says r/hlgov is not up: %d failures\n%s", len(res.Fails), text)
	}
}

// E4-TL6 (a, b): an honest governed bridge, audited by a member (a) while r/hlgov's kind 5 is dispatched and not yet
// recorded on Ethereum — the ≈15–35 minutes every decision spends there — PASSes, the decision said in flight (it
// FAILed the counter); a number in that gap r/hlgov does not render still FAILs; (b) once the kind 5 ran, a member
// who gives the watch keys as they stand today rather than the launch's FAILs on the creation inputs, which now say
// the launch values are what they carry.
func TestAnvilAuditDecisionsInFlight(t *testing.T) {
	artifactsOrSkip(t)
	url, raw := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cfg := testSetupConfig(t, url, dir)
	var out bytes.Buffer
	if _, err := runSetup(ctx, cfg, &out); err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	cn, err := dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	admin, mailbox := d.Addr("hlAdmin"), d.Addr("mailbox")
	rig := &rl9Rig{t: t, raw: raw, cn: cn, admin: admin}
	rig.must(raw.Call(nil, "anvil_impersonateAccount", mailbox))
	rig.must(raw.Call(nil, "anvil_setBalance", mailbox, "0x56BC75E2D63100000"))
	fg := honestGno(t, cfg, d)
	acfg := withLaunchFlags(auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest", GnoRPC: fg.serve(t).URL,
		MailboxPkgPath: cfg.MailboxPkgPath, Hlgov: cfg.Hlgov, GnotRoute: cfg.GnotRoute, BubbleRoute: cfg.BubbleRoute}, cfg)
	fresh := common.HexToAddress("0x000000000000000000000000000000000000fe11")
	params, err := guardiansParams([]seatKey{{6, fresh}})
	rig.must(err)
	body := decisionBody(kindGuardians, 1, params)
	// (a) r/hlgov dispatched decision 1 (a kind 5) and moved its mirror; Ethereum has not recorded it
	fg.mu.Lock()
	fg.decisions[1] = fmt.Sprintf(`{"number":1,"kind":5,"body":"%x","id":"","count":1,"at":0,"text":""}`, body)
	fg.last = 1
	fg.seats[5] = fresh.Hex()
	fg.mu.Unlock()
	res, text := auditOf(t, acfg)
	if len(res.Fails) != 0 || !strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("an honest bridge with one r/hlgov decision in flight: %d failures\n%s", len(res.Fails), text)
	}
	for _, want := range []string{"ok   the line's last number 0 is r/hlgov's counter 1 less its 1 decision(s) in flight (#1–#1",
		"     r/hlgov's decision #1 (kind 5) is in flight: dispatched by r/hlgov, not recorded on Ethereum at block"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the audit lacks %q:\n%s", want, text)
		}
	}
	// a number in the gap r/hlgov never dispatched (it renders null): no delivery explains it
	fg.mu.Lock()
	fg.last = 2
	fg.mu.Unlock()
	res, text = auditOf(t, acfg)
	if !hasLine(res.Fails, "the line's last number 0 is r/hlgov's counter 2") || !hasLine(res.Fails, "#2 is none r/hlgov dispatched (it renders null)") {
		t.Fatalf("a gap r/hlgov does not render: %d failures\n%s", len(res.Fails), text)
	}
	fg.mu.Lock()
	fg.last = 1
	fg.mu.Unlock()
	// delivered and executed after its 7 days: PASS with the launch's -watch
	data, err := ethx.Admin.Pack("handle", d.GnoDomain, realm32(d.Launch.Hlgov), body)
	rig.must(err)
	rig.send(mailbox, &admin, data)
	rig.warp(7*86_400 + 60)
	rig.mustExecute(1)
	if res, text = auditOf(t, acfg); len(res.Fails) != 0 || !strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("the kind 5 executed, the launch's -watch: %d failures\n%s", len(res.Fails), text)
	}
	// (b) the watch keys as they stand today: the creation inputs carry the launch's — the failure says so
	c := acfg
	c.Watch = d.Launch.Watch[0] + "," + fresh.Hex()
	res, text = auditOf(t, c)
	if len(res.Fails) == 0 {
		t.Fatalf("today's -watch: no failure\n%s", text)
	}
	for _, f := range res.Fails {
		if strings.Contains(f, "creation input is not the build") && !strings.Contains(f, "the launch values given differ from the deployer's record in the seats and watch slots (-seats, -watch): -seats, -watch, -beneficiary and -expected-lock-gnot/-bubble are the LAUNCH values") {
			t.Fatalf("a creation-input failure does not name the launch values given: %s", f)
		}
	}
	if !hasLine(res.Fails, "gate's creation input is not the build") {
		t.Fatalf("today's -watch: the gate's creation input does not fail:\n%s", strings.Join(res.Fails, "\n"))
	}
}

// E4-TL6 (c): after a kind 9 the members' commands act on the live HlAdmin — `decision status` shows the successor's
// pending decision and `decision execute` executes it from any key — where they read the retired one: RETIRED, no
// pending decision, and "HlAdmin holds no pending decision N" for every decision of the successor.
func TestAnvilDecisionCommandsFollowTheSuccessor(t *testing.T) {
	arts := artifactsOrSkip(t)
	url, raw := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cfg := testSetupConfig(t, url, dir)
	var out bytes.Buffer
	if _, err := runSetup(ctx, cfg, &out); err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	cn, err := dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	admin, mailbox, deployer := d.Addr("hlAdmin"), d.Addr("mailbox"), common.HexToAddress(d.Deployer)
	l := d.Launch
	rig := &rl9Rig{t: t, raw: raw, cn: cn, admin: admin}
	rig.must(raw.Call(nil, "anvil_impersonateAccount", mailbox))
	rig.must(raw.Call(nil, "anvil_setBalance", mailbox, "0x56BC75E2D63100000"))
	handle := func(to common.Address, kind uint8, seq uint64, params []byte) {
		t.Helper()
		data, err := ethx.Admin.Pack("handle", d.GnoDomain, realm32(l.Hlgov), decisionBody(kind, seq, params))
		rig.must(err)
		rig.send(mailbox, &to, data)
	}
	// the hand-over: an honest successor, named by a kind 9 and executed after its 7 days
	art, v, err := creationOf("hlAdmin", d, l, arts)
	rig.must(err)
	c := v["c"].(Values)
	c["predecessor"], c["seats"], c["watch"], c["startsequence"] = admin, [4]common.Address{}, [2]common.Address{}, uint64(0)
	code, err := art.CreationCode(v)
	rig.must(err)
	rc := rig.send(deployer, nil, code)
	if rc.Status != types.ReceiptStatusSuccessful {
		t.Fatal("the successor's creation failed")
	}
	succ := rc.ContractAddress
	sc, err := cn.code(ctx, ethx.Latest, succ)
	rig.must(err)
	handle(admin, kindSuccessor, 1, successorParams(succ, crypto.Keccak256Hash(sc)))
	rig.warp(7*86_400 + 60)
	rig.expect(rig.mustExecute(1), "Retired", 0, 0, "")
	// r/hlgov's next decision goes to the successor: a fee, pending 7 days there
	handle(succ, kindFee, 2, feeParams(3, common.HexToAddress("0x000000000000000000000000000000000000beef")))

	f, err := readAdminFacts(ctx, cn, d)
	if err != nil {
		t.Fatal(err)
	}
	var st strings.Builder
	printStatus(&st, d, f)
	for _, want := range []string{"HlAdmin " + admin.Hex() + " RETIRED: every authority went to the successor " + succ.Hex(),
		"the live HlAdmin " + succ.Hex() + " at block", "  - #2 kind 7: set the protocol fee to 3 wei"} {
		if !strings.Contains(st.String(), want) {
			t.Fatalf("decision status after the hand-over lacks %q:\n%s", want, st.String())
		}
	}
	key := filepath.Join(dir, "member.key")
	rig.must(os.WriteFile(key, []byte("59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d\n"), 0o600)) // anvil's account 1
	tx, err := keyTxer(cn, key, ethx.SendConfig{Poll: 50 * time.Millisecond})
	rig.must(err)
	var w bytes.Buffer
	if err := executeDecision(ctx, &w, cn, d, tx, 2); err == nil || !strings.Contains(err.Error(), "decision 2 (kind 7) matures at") {
		t.Fatalf("the successor's decision before its maturity: %v (want NotMature's words, from the live HlAdmin)", err)
	}
	rig.warp(7*86_400 + 60)
	if err := executeDecision(ctx, &w, cn, d, tx, 2); err != nil {
		t.Fatalf("a member's execute of the successor's matured decision: %v", err)
	}
	if !strings.Contains(w.String(), "decision 2 (kind 7) executed by HlAdmin "+succ.Hex()) {
		t.Fatalf("the execute: %s", w.String())
	}
	if fee, err := read[*big.Int](ctx, cn, ethx.Latest, d.Addr("protocolFee"), arts["ProtocolFee"], "protocolFee"); err != nil || fee.Int64() != 3 {
		t.Fatalf("the protocol fee after the member's execute: %v %v", fee, err)
	}
}

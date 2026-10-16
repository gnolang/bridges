package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// Round 5's conformance findings in the tools (fold lane F17d): E5-CF1 (the audit trusted a log of HlAdmin's own
// creation transaction, and a planted hold start older than HlAdmin once a real HoldEnded log carried it), E5-CF3 item
// 2 (two honest gno sources a block apart disagreed on a route's lock or r/hlgov's renders, and FAILed the bridge),
// E5-CF3 item 3 (-config made the operator's bridge.json the source of gno.land and of the expected locks). The tests
// that read a behaviour the parent had were run on 04441d95's code first, where they fail (fold-r5/f17d.md).

// sendFrom sends a call from one of anvil's unlocked default accounts and waits for its receipt.
func sendFrom(t *testing.T, c *rpc.Client, from, to common.Address, data []byte) common.Hash {
	t.Helper()
	var h common.Hash
	if err := c.Call(&h, "eth_sendTransaction", map[string]any{"from": from, "to": to, "data": hexutil.Bytes(data)}); err != nil {
		t.Fatal(err)
	}
	waitReceipt(t, c, h)
	return h
}

// E5-CF1, the auditor's PoC's second plant on a real deployment: HlAdmin's stored hold moments of a hold whose dip ran
// out long ago and was never settled (holdBlock 1, holdTime 0, dipBlock 1, dipTime 1: no view shows them), then the
// first guardian flag after the setup, which settles them into the hold history with a REAL HoldEnded(0, …) log — the
// start that, once eight more holds end, merges into the oldest entry and lands a kind 5 on day 7 of the guardians'
// ninth hold. The audit of 04441d95 read neither: no failure. Now the moments word is read whole and every hold start
// is held to HlAdmin's life. A real guardian hold, standing, proves the word's place.
func TestAnvilAuditHoldsTheHoldRecordToHlAdminsLife(t *testing.T) {
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
	admin := d.Addr("hlAdmin")
	audit := func() ([]string, string) {
		t.Helper()
		var b bytes.Buffer
		fails, err := runAudit(ctx, auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest"}, &b)
		if err != nil {
			t.Fatalf("audit: %v\n%s", err, b.String())
		}
		return fails, b.String()
	}
	if fails, text := audit(); len(fails) != 0 {
		t.Fatalf("the honest launch: %d failures\n%s", len(fails), text)
	}
	setFlag, _ := ethx.Admin.Pack("setFlag")
	var snap hexutil.Uint64
	if err := raw.Call(&snap, "evm_snapshot"); err != nil {
		t.Fatal(err)
	}
	// the plant: the moments of an unsettled hold from before HlAdmin existed
	var w common.Hash
	binary.BigEndian.PutUint64(w[24:32], 1) // holdBlock 1, holdTime 0
	binary.BigEndian.PutUint64(w[8:16], 1)  // dipBlock 1
	binary.BigEndian.PutUint64(w[0:8], 1)   // dipTime 1
	slot := common.BigToHash(new(big.Int).Add(hlAdminSlot.Big(), big.NewInt(wordHoldMoments)))
	if err := raw.Call(nil, "anvil_setStorageAt", admin, slot, w); err != nil {
		t.Fatal(err)
	}
	fails, text := audit()
	if !hasLine(fails, "HlAdmin's stored hold moments (its namespace's word 12, read whole: the hold's start block 1, time 0; its dip's block 1, time 1)") ||
		!hasLine(fails, "NOT for the hold's start's block 1 and time 0 (one set alone), its dip at block 1, time 1 — planted") {
		t.Fatalf("the planted moments, unsettled: %d failures, none naming them\n%s", len(fails), text)
	}
	// the first guardian flag after the setup settles them: a real HoldEnded log now carries the planted start
	sendFrom(t, raw, cfg.Seats[0], admin, setFlag)
	var hs []ethx.Hold
	cn, err := dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	arts, _ := LoadArtifacts(cfg.ContractsOut, cfg.HyperlaneDir)
	if hs, err = read[[]ethx.Hold](ctx, cn, ethx.Latest, admin, arts["HlAdmin"], "holds"); err != nil || len(hs) != 1 || hs[0].Start != 0 {
		t.Fatalf("the settled hold history: %v %v", hs, err)
	}
	fails, text = audit()
	want := fmt.Sprintf("HlAdmin's hold history (1) each starts at or after HlAdmin's creation (block %d", d.stepOf("hlAdmin").Block)
	if !hasLine(fails, want) || !hasLine(fails, fmt.Sprintf("NOT for 0–%d — planted", hs[0].End)) {
		t.Fatalf("the planted start under a real HoldEnded log: %d failures, none saying %q\n%s", len(fails), want, text)
	}
	if !strings.Contains(text, "ok   HlAdmin's hold history (1) each starts and ends where a HoldEnded log put it") {
		t.Fatalf("the HoldEnded log is real (the log check alone cannot see the plant):\n%s", text)
	}
	// the control: the guardians' own hold, seats 1 and 2, on the launch's state — it stands, and its start is the
	// word's (the place proved), nothing fails
	var reverted bool
	if err := raw.Call(&reverted, "evm_revert", snap); err != nil || !reverted {
		t.Fatalf("evm_revert: %v %v", reverted, err)
	}
	sendFrom(t, raw, cfg.Seats[0], admin, setFlag)
	sendFrom(t, raw, cfg.Seats[1], admin, setFlag)
	fails, text = audit()
	if len(fails) != 0 || !strings.Contains(text, "ok   HlAdmin's standing hold (block ") || !strings.Contains(text, "ok   HlAdmin's stored hold moments (its namespace's word 12") {
		t.Fatalf("a real hold of two guardian flags: %d failures (want none, its start the word's)\n%s", len(fails), text)
	}
}

// loggingInitcode is a creation that logs HlAdmin's FlagSet(key, 1) as the contract it creates, then returns a STOP:
// what an initcode running at HlAdmin's address can log beside a flag it plants (E5-CF1). quiet logs nothing.
func loggingInitcode(key common.Address, quiet bool) []byte {
	code := []byte{0x60, 0x01, 0x60, 0x00, 0x52} // mstore(0, 1): the slot, abi-encoded
	if !quiet {
		code = append(code, 0x73)
		code = append(code, key.Bytes()...) // PUSH20 key: topic 1
		code = append(code, 0x7f)
		code = append(code, ethx.Admin.Event("FlagSet").Bytes()...) // PUSH32 topic 0
		code = append(code, 0x60, 0x20, 0x60, 0x00, 0xa2)           // log2(0, 32, topic0, topic1)
	}
	return append(code, 0x60, 0x01, 0x60, 0x00, 0xf3) // return(0, 1): the runtime 0x00
}

// E5-CF1's class (setupDoneBy refused only a SetupDone): any log of HlAdmin's creation transaction fails — an honest
// constructor logs nothing — and none of them explains HlAdmin's record. A creation logging a FlagSet as the contract
// it creates, on anvil, read through the audit's own reads.
func TestAuditRefusesEveryLogOfHlAdminsCreation(t *testing.T) {
	url, raw := startAnvil(t)
	ctx := context.Background()
	deployer := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	key := common.HexToAddress(anvilAccounts[0])
	create := func(quiet bool) (common.Hash, common.Address, uint64) {
		var h common.Hash
		if err := raw.Call(&h, "eth_sendTransaction", map[string]any{"from": deployer, "data": hexutil.Bytes(loggingInitcode(key, quiet))}); err != nil {
			t.Fatal(err)
		}
		waitReceipt(t, raw, h)
		var rc struct {
			ContractAddress common.Address `json:"contractAddress"`
			BlockNumber     hexutil.Uint64 `json:"blockNumber"`
		}
		if err := raw.Call(&rc, "eth_getTransactionReceipt", h); err != nil {
			t.Fatal(err)
		}
		return h, rc.ContractAddress, uint64(rc.BlockNumber)
	}
	cn, err := dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	judge := func(h common.Hash, at common.Address) (*audit, creationAt, string) {
		var b bytes.Buffer
		a := &audit{out: &b, cn: cn, d: &Deployment{HlAdmin: at.Hex(), Steps: []Step{{Name: "hlAdmin", Tx: h.Hex()}}}}
		return a, a.adminCreation(ctx), b.String()
	}
	// an honest creation: nothing logged
	h, at, block := create(true)
	a, cr, text := judge(h, at)
	if len(a.fails) != 0 || !cr.ok || cr.block != block || !strings.Contains(text, "ok   HlAdmin's creation transaction "+h.Hex()+" logged nothing") {
		t.Fatalf("a creation that logs nothing: %v %+v\n%s", a.fails, cr, text)
	}
	// a creation logging FlagSet as the contract it creates
	h, at, _ = create(false)
	a, cr, text = judge(h, at)
	if len(a.fails) != 1 || !strings.Contains(a.fails[0], "it logged HlAdmin's FlagSet (log 0) — an initcode's") || !cr.ok || cr.tx != h {
		t.Fatalf("a creation logging FlagSet as HlAdmin: %v %+v\n%s", a.fails, cr, text)
	}
	// its log explains nothing: the record's reading drops it
	ls, err := cn.c.LogsRange(ctx, ethx.LogQuery{Addresses: []common.Address{at}, Topics: [][]common.Hash{{ethx.Admin.Event("FlagSet")}}}, 0, cr.block)
	if err != nil || len(ls) != 1 || ls[0].TxHash != h {
		t.Fatalf("the creation's FlagSet log: %v %v", ls, err)
	}
	if left := explainable(ls, cr.tx); len(left) != 0 {
		t.Fatalf("a log of HlAdmin's creation transaction still explains: %v", left)
	}
	if left := explainable(ls, common.HexToHash("0x1234")); len(left) != 1 {
		t.Fatalf("a log of another transaction dropped: %v", left)
	}
}

// The moments' and the history's bounds (E5-CF1), by case: what the keys' flags leave passes; a start before HlAdmin's
// creation, a moment past the block read, a block or a time set alone, a dip with no hold before it, an end before
// its start or past the block read, fail.
func TestHoldBoundsByCase(t *testing.T) {
	cr := creationAt{block: 100, time: 1_000, ok: true}
	for _, c := range []struct {
		m    moments
		want string
	}{
		{moments{}, ""},
		{moments{holdBlock: 100, holdTime: 1_000}, ""},
		{moments{holdBlock: 120, holdTime: 1_200, dipBlock: 130, dipTime: 1_300}, ""},
		{moments{holdBlock: 120, holdTime: 1_200, dipBlock: 120, dipTime: 1_200}, ""},
		{moments{holdBlock: 1, dipBlock: 1, dipTime: 1}, "the hold's start's block 1 and time 0 (one set alone)"},
		{moments{holdBlock: 99, holdTime: 999}, "the hold's start at block 99, time 999"},
		{moments{holdBlock: 120, holdTime: 1_200, dipBlock: 201, dipTime: 1_300}, "its dip at block 201, time 1300"},
		{moments{holdBlock: 120, holdTime: 2_001}, "the hold's start at block 120, time 2001"},
		{moments{dipBlock: 130, dipTime: 1_300}, "a dip (block 130) with no hold before it (block 0)"},
		{moments{holdBlock: 130, holdTime: 1_300, dipBlock: 120, dipTime: 1_200}, "a dip (block 120) with no hold before it (block 130)"},
	} {
		got := strings.Join(c.m.outside(cr, 200, 2_000), "; ")
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Fatalf("%+v: %q, want %q", c.m, got, c.want)
		}
	}
	got := earlyHolds([]ethx.Hold{{Start: 1_000, End: 1_500}, {Start: 0, End: 86_401}, {Start: 1_600, End: 1_550}, {Start: 1_700, End: 2_001}}, 1_000, 2_000)
	if strings.Join(got, ",") != "0–86401,1600–1550,1700–2001" {
		t.Fatalf("the history's bounds: %v", got)
	}
}

// liveGno is a gno node whose head moves: its height (moved by the test, or by itself after some status calls), and
// renders that answer the state at the head.
type liveGno struct {
	mu      sync.Mutex
	height  int64
	moveAt  int  // status calls after which the head moves one block (0: never)
	always  bool // the head moves at every status call: never read still
	statuss int
	render  func(h int64, path string) string
}

func (g *liveGno) serve(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params map[string]string `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		g.mu.Lock()
		defer g.mu.Unlock()
		reply := func(v any) {
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": v})
			_, _ = w.Write(b)
		}
		switch req.Method {
		case "status":
			g.statuss++
			if (g.moveAt > 0 && g.statuss == g.moveAt) || g.always {
				g.height++
			}
			reply(map[string]any{"sync_info": map[string]any{"latest_block_height": strconv.FormatInt(g.height, 10), "latest_block_time": "2026-10-15T00:00:00Z"}})
		case "abci_query":
			data, _ := base64.StdEncoding.DecodeString(req.Params["data"])
			_, path, _ := strings.Cut(string(data), ":")
			reply(map[string]any{"response": map[string]any{"ResponseBase": map[string]any{"Error": nil, "Data": []byte(g.render(g.height, path))}}})
		default:
			http.Error(w, "no such method", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// E5-CF3 item 2: a route's lock moves with every send, so two honest sources a block apart — one at height 11, a send
// of 7 just taken; one still at 10 — answer two locks. 04441d95's audit FAILed it, "the gno sources disagree"; now the
// two are read again at one height (each answer bracketed by its head) and agree. Two sources apart at ONE height still
// disagree, and a source that never reaches the others' height leaves the disagreement standing.
func TestRouteReadPinsTheLockToOneHeight(t *testing.T) {
	defer func(w, p time.Duration) { pinWait, pinPoll = w, p }(pinWait, pinPoll)
	pinWait, pinPoll = 2*time.Second, 20*time.Millisecond
	const route = testNS + "hlgnot"
	lockAt := func(h int64) int64 { return 40_000_000_000 + 7*max(h-10, 0) }
	json := func(lock int64) string {
		return fmt.Sprintf(`{"address":"g1x","live":true,"lock":%d,"meter":1,"maximum":25000000000,"minimum":1000000}`, lock)
	}
	honest := func(h int64, path string) string { return json(lockAt(h)) }
	read := func(srcs ...*liveGno) (*gnoRoute, error, string) {
		var b bytes.Buffer
		a := &audit{out: &b}
		for _, s := range srcs {
			url := s.serve(t)
			a.gnos = append(a.gnos, gnoSource{url: url, c: &gno.Client{RPC: url}, own: true})
		}
		r, err := a.routeRead(context.Background(), route)
		if r == nil {
			return nil, err, b.String()
		}
		return &gnoRoute{r.Maximum, r.Lock}, err, b.String()
	}
	// one at 11, one at 10 that moves to 11 on its second status call
	r, err, text := read(&liveGno{height: 11, render: honest}, &liveGno{height: 10, moveAt: 2, render: honest})
	if err != nil || r == nil || r.lock != lockAt(11) || r.maximum != 25_000_000_000 {
		t.Fatalf("two honest sources a block apart: %+v %v\n%s", r, err, text)
	}
	if !strings.Contains(text, "the sources answered alike once read at one height, 11") {
		t.Fatalf("the pinned reading is not said:\n%s", text)
	}
	// at one height, two locks: the disagreement it is
	other := func(h int64, path string) string { return json(lockAt(h) + 1) }
	if _, err, _ := read(&liveGno{height: 11, render: honest}, &liveGno{height: 11, render: other}); err == nil || !strings.Contains(err.Error(), "the gno sources disagree at one height, 11") {
		t.Fatalf("two locks at one height: %v", err)
	}
	// a source that never reaches the other's height: the first disagreement stands, said
	if _, err, _ := read(&liveGno{height: 11, render: honest}, &liveGno{height: 10, render: honest}); err == nil || !strings.Contains(err.Error(), "the gno sources disagree") || !strings.Contains(err.Error(), "not brought to one height") {
		t.Fatalf("a source stuck a block behind: %v", err)
	}
	// heads that move across every read: never pinned, never judged alike
	if _, err, _ := read(&liveGno{height: 11, always: true, render: honest}, &liveGno{height: 10, always: true, render: other}); err == nil || !strings.Contains(err.Error(), "never read with its head still") {
		t.Fatalf("sources whose heads move across every read: %v", err)
	}
}

type gnoRoute struct{ maximum, lock int64 }

// E5-CF3 item 2's sibling: r/hlgov's renders move with every dispatch — its json's counter here, 5 on a source a block
// behind, 6 on the other — and are read again at one height the same way.
func TestGovReadPinsToOneHeight(t *testing.T) {
	defer func(w, p time.Duration) { pinWait, pinPoll = w, p }(pinWait, pinPoll)
	pinWait, pinPoll = 2*time.Second, 20*time.Millisecond
	gov := func(h int64, path string) string {
		last := 5 + max(h-10, 0)
		return fmt.Sprintf(`{"last":%d,"next":%d,"start":0,"recipient":"0x00000000000000000000000000000000000000ad","former":[]}`, last, last+1)
	}
	var b bytes.Buffer
	a := &audit{out: &b, hlgovPath: testNS + "hlgov"}
	for _, s := range []*liveGno{{height: 11, render: gov}, {height: 10, moveAt: 3, render: gov}} {
		url := s.serve(t)
		a.gnos = append(a.gnos, gnoSource{url: url, c: &gno.Client{RPC: url}, own: true})
	}
	g := a.govRead(context.Background())
	if g.Err != nil || g.Gov == nil || g.Gov.Last != 6 {
		t.Fatalf("r/hlgov's json from two sources a block apart: %+v\n%s", g, b.String())
	}
}

// E5-CF3 item 3 (E4-TL1's threat by another door): the machines' config is the operator's file. A deployer compiled
// the caps from a lock ten times the launch notice's and runs the operator's machines, whose bridge.json names its own
// gno node and that lock. A member who passed it with the other launch values and no -gno-rpc or lock flag got a PASS
// from 04441d95: gno.land and the caps held to the operator's word. Now that is UNVERIFIED, naming the config's sources
// and each lock (the operator's expectedLock noted, never taken); with -gno-rpc and the launch notice's locks the caps
// FAIL; with the deployment's own, PASS, the config's sources read beside the member's.
func TestAnvilAuditTakesNoLaunchValueFromTheOperatorsConfig(t *testing.T) {
	artifactsOrSkip(t)
	url, _ := startAnvil(t)
	dir := t.TempDir()
	cfg := testSetupConfig(t, url, dir)
	cfg.MaxPerMessage, cfg.DayCap = [2]uint64{}, [2]uint64{}
	cfg.ExpectedLock = [2]uint64{500_000_000_000, 500_000_000_000} // the deployer's: a day cap of 50,000
	var out bytes.Buffer
	if _, err := runSetup(context.Background(), cfg, &out); err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	fg := honestGno(t, cfg, d)
	fg.absent = true
	theirs := fg.serve(t).URL
	conf := filepath.Join(dir, "bridge.json")
	b, _ := json.Marshal(map[string]any{"gno": map[string]any{"rpc": theirs},
		"routes": []map[string]any{{"symbol": "GNOT", "gno": cfg.GnotRoute, "token": d.Addr("token0").Hex(), "expectedLock": cfg.ExpectedLock[0]},
			{"symbol": "BUBBLE", "gno": cfg.BubbleRoute, "token": d.Addr("token1").Hex(), "expectedLock": cfg.ExpectedLock[1]}}})
	if err := os.WriteFile(conf, b, 0o644); err != nil {
		t.Fatal(err)
	}
	c := withLaunchFlags(auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest", Config: conf,
		MailboxPkgPath: cfg.MailboxPkgPath, Hlgov: cfg.Hlgov, GnotRoute: cfg.GnotRoute, BubbleRoute: cfg.BubbleRoute}, cfg)
	c.ExpectedLock = [2]uint64{}
	res, text := auditOf(t, c)
	if strings.Contains(text, "hleth audit: PASS") || len(res.Fails) != 0 {
		t.Fatalf("the operator's config alone for gno.land and the locks: fails %d (want none), and never a PASS\n%s", len(res.Fails), text)
	}
	for _, want := range []string{"gno.land is read only from the operator's config (-config",
		"token 0's launch limits (25000000000 a message, 50000000000 a day): the deployment's record, bounded only by §5.4's ceilings — give -expected-lock-gnot",
		"the operator's config (-config's route of token 0's address " + d.Addr("token0").Hex() + ", its realm " + cfg.GnotRoute + ") expects 500000000000 — the operator's word, never taken",
		"token 1's launch limits (25000000000 a message, 50000000000 a day)"} {
		if !hasLine(res.Unverified, want) {
			t.Fatalf("no UNVERIFIED line says %q:\n%s", want, strings.Join(res.Unverified, "\n"))
		}
	}
	if !strings.Contains(text, "(the config's: the operator's)") {
		t.Fatalf("the config's source is not named the operator's:\n%s", text)
	}
	// the member's own node and the launch notice's locks (50,000 each: a cap of 5,000): the caps FAIL, the config's
	// node read beside the member's
	mine := honestGno(t, cfg, d)
	mine.absent = true
	c.GnoRPC = mine.serve(t).URL
	c.ExpectedLock = [2]uint64{50_000_000_000, 50_000_000_000}
	c.LaunchMaximum = 5_000_000_000
	res, text = auditOf(t, c)
	if !hasLine(res.Fails, "the gate's limits for token 0: 25000000000 a message, 50000000000 a day (must be 5000000000 and 5000000000") ||
		!strings.Contains(text, "gno.land is read from 2 source(s)") || !strings.Contains(text, "NOTE the operator's config (-config's route of token 0's address") {
		t.Fatalf("the launch notice's locks against the deployer's caps: %d failures\n%s", len(res.Fails), text)
	}
	// the member's values are the deployment's: PASS, two sources read
	c.ExpectedLock, c.LaunchMaximum = cfg.ExpectedLock, 0
	res, text = auditOf(t, c)
	if len(res.Fails) != 0 || len(res.Unverified) != 0 || !strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("the member's own node and locks, the config's node beside: fails %d unverified %d\n%s", len(res.Fails), len(res.Unverified), text)
	}
}

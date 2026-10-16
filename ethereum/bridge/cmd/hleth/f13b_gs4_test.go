package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// E3-GS4 (with E3-CF4's audit half): every launch value the audit holds comes from a source independent of the
// deployer where one exists — the start sequence from r/hlgov (its compiled start, its counter, its decision
// recipient), the seats and watch slots from -seats and -watch and from r/hlgov's Ethereum seats, the limits from the
// expected lock (-expected-lock-*, or the config's route of each token's address) and the routes' maximums, the beneficiary from
// -beneficiary — and a deployment that differs from any of them FAILs, though its own record agrees with it.
func TestAnvilAuditHoldsTheLaunchValuesToIndependentReadings(t *testing.T) {
	artifactsOrSkip(t)
	url, _ := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cfg := testSetupConfig(t, url, dir)
	// the limits compiled from the locks the launch notice expects (E3-CF4): GNOT 300,000 → a day cap of 30,000,
	// BUBBLE 2,000,000 → 100,000 (the ceiling); both maximums 25,000
	cfg.MaxPerMessage, cfg.DayCap = [2]uint64{}, [2]uint64{}
	cfg.ExpectedLock = [2]uint64{300_000_000_000, 2_000_000_000_000}
	var out bytes.Buffer
	if _, err := runSetup(ctx, cfg, &out); err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Launch.MaxPerMessage != [2]uint64{25_000_000_000, 25_000_000_000} || d.Launch.DayCap != [2]uint64{30_000_000_000, 100_000_000_000} || d.Launch.ExpectedLock != cfg.ExpectedLock {
		t.Fatalf("the launch limits from the expected locks: %+v", d.Launch)
	}
	var seats, watch []string
	for _, a := range cfg.Seats {
		seats = append(seats, a.Hex())
	}
	for _, a := range cfg.Watch {
		watch = append(watch, a.Hex())
	}
	fg := honestGno(t, cfg, d)
	base := auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest", GnoRPC: fg.serve(t).URL,
		MailboxPkgPath: cfg.MailboxPkgPath, Hlgov: cfg.Hlgov, GnotRoute: cfg.GnotRoute, BubbleRoute: cfg.BubbleRoute,
		Seats: strings.Join(seats, ","), Watch: strings.Join(watch, ","), Beneficiary: cfg.Beneficiary.Hex(), ExpectedLock: cfg.ExpectedLock}
	audit := func(c auditConfig) ([]string, string) {
		t.Helper()
		var b bytes.Buffer
		fails, err := runAudit(ctx, c, &b)
		if err != nil {
			t.Fatalf("audit: %v\n%s", err, b.String())
		}
		return fails, b.String()
	}
	failsWith := func(fails []string, text, want string) {
		t.Helper()
		for _, f := range fails {
			if strings.Contains(f, want) {
				return
			}
		}
		t.Fatalf("no failure says %q (%d failures):\n%s", want, len(fails), text)
	}

	// honest, every value held: PASS, each source said
	fails, text := audit(base)
	if len(fails) != 0 || !strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("an honest deployment held to every independent reading: %d failures\n%s", len(fails), text)
	}
	for _, want := range []string{
		"     the start sequence: 0 — r/hlgov's own compiled start",
		"     the seats and watch slots: " + strings.Join(append(seats, watch...), ",") + " — -seats and -watch",
		"     token 0's launch limits: 25000000000 a message, 30000000000 a day — compiled from its expected lock 300000000000 (-expected-lock-gnot)",
		"token 0's day cap 30000000000 beside the expected lock 300000000000 it was compiled from (-expected-lock-gnot): 10.0% of it",
		"ok   seat 3 is " + seats[2] + ", r/hlgov's Ethereum seat as it last set it",
		"ok   r/hlgov sends its decisions to HlAdmin " + d.Addr("hlAdmin").Hex(),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the honest audit lacks %q:\n%s", want, text)
		}
	}

	// another expected lock: the limits the gate, HlAdmin's creation and its delegate's immutables hold are not it
	c := base
	c.ExpectedLock = [2]uint64{1_000_000_000_000, 2_000_000_000_000}
	fails, text = audit(c)
	failsWith(fails, text, "the gate's limits for token 0: 25000000000 a message, 30000000000 a day (must be 25000000000 and 100000000000")
	failsWith(fails, text, "hlAdminExec's masked immutable HlAdminExec.dayCap0 = 30000000000, but it must be 100000000000")
	failsWith(fails, text, "gate's creation input is not the build of TransparentUpgradeableProxy with the setup's arguments")

	// a start sequence the member expects that HlAdmin does not carry (r/hlgov not up)
	c = base
	five := uint64(5)
	c.StartSequence = &five
	fails, text = audit(c)
	failsWith(fails, text, "hlAdmin's masked immutable HlAdmin._startSequence = 0, but it must be 5")
	failsWith(fails, text, "HlAdmin's lastSeq() 0 is the launch's start 5 plus its 0 DecisionRecorded log(s)")

	// the member's published seats against the deployment's: seat 3 another key
	c = base
	stranger := common.HexToAddress("0x14dC79964da2C08b23698B3D3cc7Ca32193d9955")
	c.Seats = strings.Join([]string{seats[0], seats[1], stranger.Hex(), seats[3]}, ",")
	fails, text = audit(c)
	failsWith(fails, text, "the gate's seats 1–4 and watch slots 5–6 are")
	failsWith(fails, text, "HlAdmin's seats and watch slots are")

	// the beneficiary the launch notice publishes, another
	c = base
	c.Beneficiary = stranger.Hex()
	fails, text = audit(c)
	failsWith(fails, text, "the ProtocolFee's beneficiary is "+cfg.Beneficiary.Hex()+" (must be "+stranger.Hex())

	// r/hlgov's own readings: its Ethereum seat 3 another, its compiled start another, its decision recipient another
	fg.mu.Lock()
	fg.seats[2] = stranger.Hex()
	fg.mu.Unlock()
	fails, text = audit(base)
	failsWith(fails, text, "seat 3: the line holds "+seats[2]+", but r/hlgov's Ethereum seat is "+stranger.Hex())
	fg.mu.Lock()
	fg.seats[2], fg.start, fg.last = seats[2], 7, 7
	fg.mu.Unlock()
	fails, text = audit(base)
	failsWith(fails, text, "hlAdmin's masked immutable HlAdmin._startSequence = 0, but it must be 7")
	fg.mu.Lock()
	fg.start, fg.last, fg.recipient = 0, 0, stranger.Hex()
	fg.mu.Unlock()
	fails, text = audit(base)
	failsWith(fails, text, "r/hlgov sends its decisions to HlAdmin "+d.Addr("hlAdmin").Hex()+" (its recipient is "+stranger.Hex())
	fg.mu.Lock()
	fg.recipient = d.Addr("hlAdmin").Hex()
	// the GNOT route up, its per-message maximum above the gate's: a send between the two would park for good
	fg.renders[cfg.GnotRoute+":json"] = `{"address":"g1x","live":true,"maximum":30000000000,"lock":40000000000,"minimum":1000000}`
	fg.mu.Unlock()
	fails, text = audit(base)
	if len(fails) != 1 {
		t.Fatalf("a route maximum above the gate's: %d failures\n%s", len(fails), text)
	}
	failsWith(fails, text, "the gate's maximum for token 0, 25000000000 a message, is at or above its route "+cfg.GnotRoute+"'s maximum 30000000000")
	if !strings.Contains(text, "token 0's day cap 30000000000 beside the lock on gno.land as it stands, 40000000000: 75.0% of it") {
		t.Fatalf("the cap beside the lock as it stands:\n%s", text)
	}
	fg.mu.Lock()
	delete(fg.renders, cfg.GnotRoute+":json")
	fg.mu.Unlock()

	// a config instead of the flags: the operator's file (E5-CF3 item 3) — its gno sources alone and its routes'
	// expected locks hold nothing to the member's word, UNVERIFIED; beside the member's -gno-rpc and lock flags, PASS
	conf := filepath.Join(dir, "bridge.json")
	b, _ := json.Marshal(map[string]any{"gno": map[string]any{"rpc": base.GnoRPC, "sources": []string{base.GnoRPC}},
		"routes": []map[string]any{{"symbol": "GNOT", "gno": cfg.GnotRoute, "token": d.Addr("token0").Hex(), "expectedLock": cfg.ExpectedLock[0]},
			{"symbol": "BUBBLE", "gno": cfg.BubbleRoute, "token": d.Addr("token1").Hex(), "expectedLock": cfg.ExpectedLock[1]}}}) // noted by the tokens' addresses (E4-TL1)
	if err := os.WriteFile(conf, b, 0o644); err != nil {
		t.Fatal(err)
	}
	c = base
	c.GnoRPC, c.ExpectedLock, c.Config = "", [2]uint64{}, conf
	var res *auditResult
	if res, text = auditOf(t, c); len(res.Fails) != 0 || strings.Contains(text, "hleth audit: PASS") || !strings.Contains(text, "hleth audit: UNVERIFIED (3)") ||
		!strings.Contains(text, "gno.land is read from 1 source(s)") || !hasLine(res.Unverified, "gno.land is read only from the operator's config") ||
		!hasLine(res.Unverified, "the operator's config (-config's route of token 0's address "+d.Addr("token0").Hex()) {
		t.Fatalf("the config's gno sources and expected locks alone: %d failures, never a PASS\n%s", len(res.Fails), text)
	}
	c.GnoRPC, c.ExpectedLock = base.GnoRPC, cfg.ExpectedLock
	if fails, text = audit(c); len(fails) != 0 || !strings.Contains(text, "hleth audit: PASS") || !strings.Contains(text, "gno.land is read from 1 source(s)") ||
		!strings.Contains(text, "compiled from its expected lock 300000000000 (-expected-lock-gnot)") {
		t.Fatalf("the config beside the member's own node and locks: %d failures\n%s", len(fails), text)
	}
}

// E3-GS4's two scenarios, each a deployment the deployer's own record agrees with: (a) HlAdmin started far from
// r/hlgov's counter — at 2^32, the most its constructor takes since fold F11 (near 2^63, the auditor's case, it now
// refuses), so r/hlgov's next decision is OutOfOrder until a sync proposal — audited before the realms are up, the
// counter was held to the record's start and PASSed; now it is held to a chain's first deployment's 0 and FAILs;
// (b) Ethereum seats of the deployer's choosing — audited once r/hlgov is up, whose Ethereum seats are the guardians',
// the seats were held to the record and PASSed; now they are held to r/hlgov's and FAIL. (Written to the audit's API
// before the fold too: on eb7cebb4 both PASS.)
func TestAnvilAuditHoldsTheStartAndTheSeatsToGnoLand(t *testing.T) {
	artifactsOrSkip(t)
	ctx := context.Background()
	deploy := func(start uint64) (setupConfig, *Deployment, string) {
		t.Helper()
		url, _ := startAnvil(t)
		cfg := testSetupConfig(t, url, t.TempDir())
		cfg.StartSequence = start
		var out bytes.Buffer
		if _, err := runSetup(ctx, cfg, &out); err != nil {
			t.Fatalf("setup: %v\n%s", err, out.String())
		}
		d, err := LoadDeployment(cfg.Out, false)
		if err != nil {
			t.Fatal(err)
		}
		return cfg, d, url
	}
	audit := func(cfg setupConfig, url, gnoRPC string) ([]string, string) {
		t.Helper()
		var b bytes.Buffer
		fails, err := runAudit(ctx, auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest", GnoRPC: gnoRPC,
			MailboxPkgPath: cfg.MailboxPkgPath, Hlgov: cfg.Hlgov, GnotRoute: cfg.GnotRoute, BubbleRoute: cfg.BubbleRoute}, &b)
		if err != nil {
			t.Fatalf("audit: %v\n%s", err, b.String())
		}
		return fails, b.String()
	}

	// (a) a start at 2^32, r/hlgov not up yet
	cfg, d, url := deploy(1 << 32)
	fg := honestGno(t, cfg, d)
	fg.absent = true
	fails, text := audit(cfg, url, fg.serve(t).URL)
	if len(fails) == 0 || strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("HlAdmin started at 2^32 before r/hlgov is up: %d failures\n%s", len(fails), text)
	}
	failsWith := func(fails []string, text, want string) {
		t.Helper()
		for _, f := range fails {
			if strings.Contains(f, want) {
				return
			}
		}
		t.Fatalf("no failure says %q (%d failures):\n%s", want, len(fails), text)
	}
	failsWith(fails, text, "hlAdmin's masked immutable HlAdmin._startSequence = 4294967296, but it must be 0")

	// (b) seats of the deployer's choosing; r/hlgov up, its Ethereum seats the guardians'
	cfg, d, url = deploy(0)
	fg = honestGno(t, cfg, d)
	guardians := []string{"0x14dC79964da2C08b23698B3D3cc7Ca32193d9955", "0x23618e81E3f5cdF7f54C3d65f7FBc0aBf5B21E8f", "0xa0Ee7A142d267C1f36714E4a8F75612F20a79720", "0xBcd4042DE499D14e55001CcbB24a551F3b954096"}
	copy(fg.seats, guardians)
	fails, text = audit(cfg, url, fg.serve(t).URL)
	if len(fails) == 0 || strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("seats of the deployer's choosing against r/hlgov's: %d failures\n%s", len(fails), text)
	}
	failsWith(fails, text, "seat 1: the line holds "+d.Launch.Seats[0]+", but r/hlgov's Ethereum seat is "+guardians[0])
}

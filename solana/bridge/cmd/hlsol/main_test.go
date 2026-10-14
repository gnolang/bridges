package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// K21: a bridge realm's own address is refused as a send's recipient; a wallet is not.
func TestRealmOf(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Gno.Mailbox = "gno.land/r/test/hlmailbox"
	cfg.Gno.Fees = "gno.land/r/test/hlfees"
	cfg.Gno.Gov = "gno.land/r/test/hlgov"
	cfg.Routes = []agent.Route{{Symbol: "BUBBLE", Gno: "gno.land/r/test/hlbubble"}, {Symbol: "GNOT", Gno: "gno.land/r/test/hlgnot"}}
	for _, p := range []string{cfg.Gno.Mailbox, cfg.Gno.Fees, cfg.Gno.Gov, cfg.Routes[0].Gno, cfg.Routes[1].Gno} {
		if got := realmOf(cfg, hl.GnoPackageAddress(p)); got != p {
			t.Errorf("%s's address: %q", p, got)
		}
	}
	if got := realmOf(cfg, "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5"); got != "" {
		t.Errorf("a wallet: %q", got)
	}
	// bech32 is case-insensitive: an upper-cased realm address is the same address (G27)
	if got := realmOf(cfg, strings.ToUpper(hl.GnoPackageAddress(cfg.Routes[0].Gno))); got != cfg.Routes[0].Gno {
		t.Errorf("an upper-cased route address passed: %q", got)
	}
	// M1i (R39): a registered route the config does not name — a successor — is a bridge realm too, read from
	// the mailbox's routes
	succ := "gno.land/r/test/hlgnot2"
	if got := realmOf(cfg, hl.GnoPackageAddress(succ), succ); got != succ {
		t.Errorf("a registered successor's address passed: %q", got)
	}
	if got := realmOf(cfg, hl.GnoPackageAddress(succ)); got != "" {
		t.Errorf("an unregistered path's address was refused: %q", got)
	}
	// AT6: a route the mailbox dropped (a successor re-pointed away) is a realm's address still — swept by the
	// line's last realm — so the send refuses it with the registered ones; mailboxRealms lists both, the
	// dropped in order
	dropped := "gno.land/r/test/hlgnotx"
	mb := agent.MailboxView{RoutePaths: []string{cfg.Routes[1].Gno, succ}, DroppedRoutes: map[string]string{dropped: cfg.Routes[1].Gno, "gno.land/r/test/hlgnota": cfg.Routes[1].Gno}}
	if got := mailboxRealms(mb); strings.Join(got, " ") != cfg.Routes[1].Gno+" "+succ+" gno.land/r/test/hlgnota "+dropped {
		t.Errorf("mailboxRealms: %v", got)
	}
	if got := realmOf(cfg, hl.GnoPackageAddress(dropped), mailboxRealms(mb)...); got != dropped {
		t.Errorf("a dropped route's address passed: %q", got)
	}
	if got := realmOf(cfg, hl.GnoPackageAddress(dropped), mb.RoutePaths...); got != "" {
		t.Errorf("the registered routes alone refused the dropped one: %q (the old reading)", got)
	}
}

// G25: the audit's curl lines and header never carry the provider URL's key — the line takes $RPC, the header
// the scheme and host alone — so an audit log posted to the members' channel hands nobody a key.
func TestAuditLinesRedactTheURL(t *testing.T) {
	line := curlLine("getAccountInfo", []byte(`["abc",{"commitment":"finalized"}]`))
	if !strings.HasPrefix(line, `  rpc> curl -s "$RPC" -H`) || !strings.Contains(line, `"method":"getAccountInfo","params":["abc",{"commitment":"finalized"}]`) {
		t.Fatalf("the curl line: %s", line)
	}
	for _, u := range []string{"https://mainnet.helius-rpc.com/?api-key=SECRET", "https://solana-mainnet.g.alchemy.com/v2/SECRET"} {
		h := auditHeader(u, time.Unix(1_800_000_000, 0))
		if strings.Contains(h, "SECRET") || !strings.Contains(h, "hlsol audit of https://") || !strings.Contains(h, "/…") {
			t.Fatalf("the header keeps the key, or names no host: %s", h)
		}
	}
}

// F10: the pausable ISM's launch day cap is the smaller of 100,000 and a tenth of the expected lock — one
// constant for both tokens as built, so the smaller of the two tokens' values.
func TestLaunchDayCap(t *testing.T) {
	one := uint64(1_000_000)
	cases := []struct {
		gnot, bubble, want uint64
	}{
		{1_000_000 * one, 1_000_000 * one, 100_000 * one},
		{5_000_000 * one, 2_000_000 * one, 100_000 * one},
		{500_000 * one, 1_000_000 * one, 50_000 * one},
		{1_000_000 * one, 300_000 * one, 30_000 * one},
		{0, 1_000_000 * one, 0},
	}
	for _, c := range cases {
		if got := launchDayCap([2]uint64{c.gnot, c.bubble}); got != c.want {
			t.Errorf("launchDayCap(%d, %d) = %d, want %d", c.gnot, c.bubble, got, c.want)
		}
	}
}

// G51: under -no-build the pausable ISM's .so is checked before the first deploy — the launch day cap and
// per-message maximum the expected locks compile must be bound in its ELF (as lddw immediates, the build's
// form), and AR4's refusal runs too; a .so built with other values is refused with nothing deployed.
func TestNoBuildChecksTheELF(t *testing.T) {
	one := uint64(1_000_000)
	lddw := func(v uint64) []byte { // one sBPF lddw: op 0x18, the low half at 4, the high half at 12
		ins := make([]byte, 16)
		ins[0] = 0x18
		for i := 0; i < 4; i++ {
			ins[4+i] = byte(v >> (8 * i))
			ins[12+i] = byte(v >> (32 + 8*i))
		}
		return ins
	}
	dir := t.TempDir()
	write := func(dayCap, maximum uint64) {
		elf := append(append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 60)...), lddw(dayCap)...)
		elf = append(elf, lddw(maximum)...)
		if err := os.WriteFile(filepath.Join(dir, "pausable_ism.so"), elf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &setup{noBuild: true, soDir: dir, expectedLock: [2]uint64{1_000_000 * one, 1_000_000 * one}}
	// the .so built with the notice's values: taken
	write(100_000*one, 25_000*one)
	if err := s.launchValues(); err != nil {
		t.Fatalf("a .so built with the expected locks' values was refused: %v", err)
	}
	// built with another day cap (a development value): refused before any deploy, naming the figure
	write(1_000_000*one, 25_000*one)
	if err := s.launchValues(); err == nil || !strings.Contains(err.Error(), "does not bind DEFAULT_DAY_CAP = 100000000000") {
		t.Fatalf("a .so built with another day cap passed: %v", err)
	}
	// built for a smaller lock than the command line says: the maximum differs, refused
	write(15_000*one, 15_000*one)
	if err := s.launchValues(); err == nil || !strings.Contains(err.Error(), "does not bind DEFAULT_DAY_CAP") {
		t.Fatalf("a .so built for a smaller lock passed: %v", err)
	}
	// AR4's refusal runs under -no-build too: a lock whose tenth is below the realms' maximum
	s.expectedLock = [2]uint64{150_000 * one, 1_000_000 * one}
	write(15_000*one, 15_000*one)
	if err := s.launchValues(); err == nil || !strings.Contains(err.Error(), "below the realms' compiled maximum") {
		t.Fatalf("AR4's refusal did not run under -no-build: %v", err)
	}
	// the locks are needed
	s.expectedLock = [2]uint64{}
	if err := s.launchValues(); err == nil || !strings.Contains(err.Error(), "-no-build needs -expected-lock-gnot") {
		t.Fatalf("-no-build without the locks passed: %v", err)
	}
	// the day cap is a line in the audit's eyes: the compiled figure against the expected locks
	if launchDayCap([2]uint64{1_000_000 * one, 1_000_000 * one}) != 100_000*one {
		t.Fatal("the day cap the audit wants")
	}
}

// AT3: -start-sequence is read against r/hlgov's counter before the first deploy — r/hlgov on the chain, the
// number must be its Last() (a fresh deployment under the same r/hlgov; a mismatch refuses, naming the number
// to pass); r/hlgov not on the chain (a chain's first deployment), the number is taken as given; the counter
// unreadable, refused. The e2e's setup runs before the realms go up, so it takes the absent branch.
func TestStartSequenceVerdict(t *testing.T) {
	absent := errors.New("InvalidPkgPathError: package not found")
	if line, err := startSequenceVerdict(37, 37, false, nil); err != nil || !strings.Contains(line, "start sequence 37: r/hlgov's counter (last 37; the next decision takes 38)") {
		t.Fatalf("the number equal to the counter: %q %v", line, err)
	}
	for _, c := range []struct {
		start, last uint64
	}{{0, 37}, {36, 37}, {38, 37}, {37, 0}} {
		_, err := startSequenceVerdict(c.start, c.last, false, nil)
		want := fmt.Sprintf("-start-sequence %d is not r/hlgov's counter (last %d; the next decision takes %d)", c.start, c.last, c.last+1)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), fmt.Sprintf("pass -start-sequence %d", c.last)) || !strings.Contains(err.Error(), "Nothing was built or deployed") {
			t.Fatalf("start %d against the counter %d: %v", c.start, c.last, err)
		}
	}
	if line, err := startSequenceVerdict(37, 0, realmAbsent(absent), nil); err != nil || !strings.Contains(line, "start sequence 37: r/hlgov is not on the chain yet, so the number is taken as given") {
		t.Fatalf("r/hlgov absent: %q %v", line, err)
	}
	if line, err := startSequenceVerdict(0, 0, true, nil); err != nil || !strings.Contains(line, "start sequence 0: r/hlgov is not on the chain yet") {
		t.Fatalf("the e2e's case: %q %v", line, err)
	}
	if _, err := startSequenceVerdict(0, 0, false, errors.New("dial tcp: connection refused")); err == nil || !strings.Contains(err.Error(), "r/hlgov's counter could not be read (dial tcp: connection refused)") {
		t.Fatalf("the counter unreadable: %v", err)
	}
	if !realmAbsent(absent) || realmAbsent(errors.New("timeout")) || realmAbsent(nil) {
		t.Fatal("realmAbsent")
	}
}

// AT3 (the audit's half): r/hlgov's counter against the admin program's last number is a FAIL on any gap, each
// direction named — below r/hlgov's, the fresh deployment that started low (or decisions undelivered); above,
// a foreign decision or GovDAO's lost — and the e2e's expected line stands as the prefix of the agreeing case.
func TestCounterLine(t *testing.T) {
	if ok, text := counterLine(4, 4, 5); !ok || !strings.HasPrefix(text, "r/hlgov's counter: last 4 (the next takes 5) against the admin program's last sequence number 4: gap 0") {
		t.Fatalf("the counters agreeing: %v %q", ok, text)
	}
	ok, text := counterLine(0, 37, 38)
	if ok || !strings.HasPrefix(text, "r/hlgov's counter: last 37 (the next takes 38) against the admin program's last sequence number 0: gap -37") || !strings.Contains(text, "THE ADMIN PROGRAM'S NUMBER IS BELOW r/hlgov's: a fresh deployment started below r/hlgov's counter (hlsol setup -start-sequence must be r/hlgov's Last(), 37, at the init") || !strings.Contains(text, "r/hlgov's next decision #38 is refused OutOfOrder for ever, AT3") || !strings.Contains(text, "decisions 1–37 are dispatched and not yet delivered") {
		t.Fatalf("the admin program below r/hlgov: %v %q", ok, text)
	}
	if ok, text := counterLine(5, 4, 5); ok || !strings.Contains(text, "gap 1 (a foreign decision took a number, or GovDAO's was lost: sync sequence)") {
		t.Fatalf("the admin program above r/hlgov: %v %q", ok, text)
	}
}

// AR4: the pausable ISM's launch per-message maximum is the smaller of 25,000 and the day cap it compiles, and
// an expected lock whose tenth compiles that maximum below the realms' is refused unless -launch-maximum says
// the realms were lowered to match (the ISM's maximum is never below gno.land's).
func TestLaunchMaxPerMessage(t *testing.T) {
	one := uint64(1_000_000)
	for _, c := range []struct{ dayCap, want uint64 }{
		{100_000 * one, 25_000 * one}, // the 1,000,000 lock of the notice: the ceiling
		{25_000 * one, 25_000 * one},  // a 250,000 lock: exactly the ceiling
		{15_000 * one, 15_000 * one},  // a 150,000 lock: the day cap binds
		{0, 0},
	} {
		if got := launchMaxPerMessage(c.dayCap); got != c.want {
			t.Errorf("launchMaxPerMessage(%d) = %d, want %d", c.dayCap, got, c.want)
		}
	}
	// the realms at their source's 25,000: a 150,000 lock is refused, naming the figures and the cure
	err := launchMaximumCheck(15_000*one, launchMaximumDefault)
	if err == nil || !strings.Contains(err.Error(), "per-message maximum is 15000000000") || !strings.Contains(err.Error(), "below the realms' compiled maximum of 25000000000") || !strings.Contains(err.Error(), "-launch-maximum 15000000000") {
		t.Fatalf("a lock whose tenth is below the realms' maximum passed: %v", err)
	}
	// the realms lowered to match: taken; lowered below the ISM's: taken too (gno.land's maximum may be lower)
	if err := launchMaximumCheck(15_000*one, 15_000*one); err != nil {
		t.Fatalf("the realms lowered to match were refused: %v", err)
	}
	if err := launchMaximumCheck(100_000*one, 10_000*one); err != nil {
		t.Fatalf("realms below the ISM's maximum were refused: %v", err)
	}
	if err := launchMaximumCheck(100_000*one, launchMaximumDefault); err != nil {
		t.Fatalf("the notice's lock was refused: %v", err)
	}
	// the realms raised above the ISM's ceiling: refused (the ISM would refuse what gno.land takes)
	if err := launchMaximumCheck(100_000*one, 50_000*one); err == nil {
		t.Fatal("realms above the ISM's ceiling passed")
	}
}

// TestDeployArgs: the program deploy's command line — the local run's default sends the writes through the RPC;
// the mainnet launch's tpu, attempts and priority fee are passed when set and never otherwise.
func TestDeployArgs(t *testing.T) {
	s := &setup{rpcURL: "https://rpc.example", deployerPath: "/k/deployer.json", soDir: "/so", keys: &programKeys{files: map[string]string{"mailbox": "/k/mailbox.json"}}}
	got := strings.Join(s.deployArgs("mailbox", 211024), " ")
	if !strings.Contains(got, "--use-rpc") || strings.Contains(got, "--max-sign-attempts") || strings.Contains(got, "--with-compute-unit-price") || !strings.Contains(got, "--max-len 211024") || !strings.Contains(got, "--program-id /k/mailbox.json") {
		t.Errorf("default: %s", got)
	}
	s.deploySend, s.maxSignAttempts, s.cuPrice = "tpu", 50, 20000
	got = strings.Join(s.deployArgs("mailbox", 211024), " ")
	if strings.Contains(got, "--use-rpc") || !strings.Contains(got, "--max-sign-attempts 50") || !strings.Contains(got, "--with-compute-unit-price 20000") || !strings.HasSuffix(got, "mailbox.so") {
		t.Errorf("tpu: %s", got)
	}
}

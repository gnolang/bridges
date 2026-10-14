package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The setup (PLAN.md §5.8): the launch values written into the three programs' config.rs and the reproducible
// build; six deployments one at a time by the deployer key, each token with no paymaster, no gas config and no
// fee config, --max-len at the ELF's size, Hyperlane's three inits sent right after each deploy; then the setup
// step in its binding order — settings (routers, ISM pointers, the default ISM, the beneficiary, metadata), then
// the owners, then the upgrade authorities through the admin program, the admin program's own burn last — and
// finish_setup. Every step reads the chain first and repeats nothing already done.

type setup struct {
	ctx           context.Context
	c             *sol.Client
	rpcURL        string
	deployer      solana.PrivateKey
	deployerPath  string // the key file, which the CLI signs the deploys with
	keys          *programKeys
	soDir         string
	programsDir   string
	factor        int
	noBuild       bool
	solanaBin     string
	gnoRPC        string
	gnoChain      string
	gnoDomain     uint32
	localDomain   uint32
	mailboxPkg    string
	hlgov         [32]byte
	routers       [2][32]byte
	seats         []solana.PublicKey
	watch         []solana.PublicKey
	treasurer     solana.PublicKey
	trustedHeight int64
	allowOld      bool // take a trusted header older than an hour (never for a deployment)
	ataFund       uint64
	authorityFund uint64
	// deploySend, maxSignAttempts and cuPrice shape the CLI's program deploys: "rpc" sends the write transactions
	// through -rpc (the local run; a free endpoint's rate limit can drop a burst of writes, as mainnet's first try
	// showed), "tpu" straight to the leaders; more sign attempts outlast a congested hour; a priority fee in
	// micro-lamports a compute unit lets the writes in when blocks are full. Zero values keep the CLI's own.
	deploySend      string
	maxSignAttempts int
	cuPrice         uint64
	decimals        uint8
	keepMeta        bool
	names           [2][3]string
	expectedLock    [2]uint64 // the locks the launch notice expects, base units (0 GNOT, 1 BUBBLE): the day cap's source (F10)
	launchMaximum   uint64    // the realms' compiled per-message maximum, base units (AR4): launchMaximumDefault unless -launch-maximum says otherwise
	// startSequence is the number the admin program's sequence starts at (AT3): 0 for a chain's first deployment,
	// r/hlgov's Last() for a fresh deployment under the same r/hlgov (§6.4's exit), checked against the realm at
	// hlgovPath (-hlgov as given) on the configured node before anything is built or deployed.
	startSequence uint64
	hlgovPath     string
}

// realmAbsent says whether a render failed because the realm is not on the chain (the keeper's not-found).
func realmAbsent(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "InvalidPkgPathError") || strings.Contains(err.Error(), "package not found"))
}

// startSequenceVerdict is the setup's reading of -start-sequence against r/hlgov's counter (AT3): the line it
// prints, or the refusal. A fresh deployment under the same r/hlgov starts its admin program at r/hlgov's
// Last(), since the counter never goes down and a program started below it answers OutOfOrder to r/hlgov's
// next decision for ever (a third deployment the only cure, and a new r/hlgov could never write the enrolment
// note); so a number that is not the counter's refuses the first deploy. absent says r/hlgov is not on the
// chain (a chain's first deployment: the realms go up after the Solana side passes its audit, §5.8), and the
// number is taken as given — r/hlgov compiles it as its start (launchSequence; 0 for a chain's first) and the
// audit reads the counter once the realm is up; readErr is any other failure of the read, which refuses too,
// since the check is what stands between the deploy and an ungovernable program.
func startSequenceVerdict(start, last uint64, absent bool, readErr error) (string, error) {
	switch {
	case readErr != nil:
		return "", fmt.Errorf("hlsol setup: r/hlgov's counter could not be read (%v): -start-sequence %d is checked against it before the first deploy (AT3: a fresh deployment under the same r/hlgov starts its admin program at r/hlgov's Last(), a chain's first at 0) — nothing was built or deployed", readErr, start)
	case absent:
		return fmt.Sprintf("start sequence %d: r/hlgov is not on the chain yet, so the number is taken as given (a chain's first deployment starts at 0; a new chain's r/hlgov compiles its predecessor's last number as its start; the audit reads the counter against the admin program's once the realm is up)", start), nil
	case start != last:
		return "", fmt.Errorf("hlsol setup: -start-sequence %d is not r/hlgov's counter (last %d; the next decision takes %d): a fresh deployment under the same r/hlgov starts its admin program at r/hlgov's Last(), or r/hlgov's next decision is refused OutOfOrder for ever — the counter never goes down (AT3, §6.4); pass -start-sequence %d. Nothing was built or deployed", start, last, last+1, last)
	}
	return fmt.Sprintf("start sequence %d: r/hlgov's counter (last %d; the next decision takes %d) — the admin program starts there and takes #%d as its first", start, last, last+1, last+1), nil
}

// startSequenceCheck reads r/hlgov's counter from the configured node and judges -start-sequence by
// startSequenceVerdict, before the build and the first deploy (AT3). With -hlgov an address, not a pkgpath, the
// counter cannot be rendered and the number is taken as given, said so.
func (s *setup) startSequenceCheck() error {
	if !strings.HasPrefix(s.hlgovPath, "gno.land/") {
		s.say("start sequence %d: taken as given (-hlgov is an address, so r/hlgov's counter cannot be rendered here; the audit reads it by pkgpath)", s.startSequence)
		return nil
	}
	g := &gno.Client{RPC: s.gnoRPC}
	var gov struct {
		Last uint64 `json:"last"`
	}
	err := g.RenderJSON(s.ctx, s.hlgovPath, "json", &gov)
	absent := realmAbsent(err)
	if absent {
		err = nil
	}
	line, err := startSequenceVerdict(s.startSequence, gov.Last, absent, err)
	if err != nil {
		return err
	}
	s.say("%s", line)
	return nil
}

// launchDayCapMax is the pausable ISM's day cap ceiling at launch (§5.4: 100,000 of each token, base units).
const launchDayCapMax = 100_000_000_000

// launchMaximumDefault is the per-message maximum the realms compile (hlgnot.gno and hlbubble.gno's
// `launchMaximum`, 25,000 of each token, base units) and the pausable ISM's ceiling for its own (§5.4).
const launchMaximumDefault = 25_000_000_000

// launchDayCap is the pausable ISM's launch day cap (F10, plan v30 §5.4): the smaller of 100,000 and a tenth of
// the lock the launch notice expects — one constant for both tokens as the program is built (DEFAULT_DAY_CAP,
// processor.rs:108 gives both tokens the same), so the smaller of the two tokens' values.
func launchDayCap(expected [2]uint64) uint64 {
	cap := uint64(launchDayCapMax)
	for _, lock := range expected {
		if tenth := lock / 10; tenth < cap {
			cap = tenth
		}
	}
	return cap
}

// launchMaxPerMessage is the pausable ISM's launch per-message maximum (AR4, §5.4): the smaller of 25,000 and
// the day cap it compiles — a maximum above the day cap is a send the ISM can never fit (OverDayCap for ever,
// parked "until a kind 12 raise" from launch day).
func launchMaxPerMessage(dayCap uint64) uint64 {
	return min(uint64(launchMaximumDefault), dayCap)
}

// launchMaximumCheck refuses an expected lock whose tenth compiles a per-message maximum below the realms'
// (AR4): gno.land would take a send the pausable ISM can never fit. The realms' figure is launchMaximumDefault
// unless -launch-maximum names what they compile — passed to match when GovDAO lowered `launchMaximum` in the
// route realms to the same figure (§5.4: the ISM's maximum must never be below gno.land's).
func launchMaximumCheck(dayCap, realms uint64) error {
	if ism := launchMaxPerMessage(dayCap); ism < realms {
		return fmt.Errorf("hlsol setup: the expected locks compile a day cap of %d base units, so the pausable ISM's per-message maximum is %d (the smaller of %d and the day cap), below the realms' compiled maximum of %d: gno.land would take a send between %d and %d that the ISM can never fit (OverDayCap for ever, AR4) — lower the route realms' launchMaximum to %d and pass -launch-maximum %d, or expect a larger lock (a tenth of it at or above %d)",
			dayCap, ism, uint64(launchMaximumDefault), realms, ism, realms, ism, ism, realms)
	}
	return nil
}

func (s *setup) say(format string, a ...any) { fmt.Printf("setup: "+format+"\n", a...) }

func (s *setup) progs() sol.Programs { return s.keys.programs() }

func (s *setup) bin(name string) string {
	if s.solanaBin == "" {
		return name
	}
	return filepath.Join(s.solanaBin, name)
}

func (s *setup) run() error {
	me := s.deployer.PublicKey()
	s.say("deployer %s on %s", me, s.rpcURL)
	// -start-sequence against r/hlgov's counter first (AT3): a mismatch refuses before anything is built or deployed
	if err := s.startSequenceCheck(); err != nil {
		return err
	}
	if err := s.launchValues(); err != nil {
		return err
	}
	if err := s.build(); err != nil {
		return err
	}
	// 1. the six deployments, Hyperlane's inits right after each of its programs
	for _, n := range programNames {
		if err := s.deploy(n); err != nil {
			return err
		}
		switch n {
		case "mailbox":
			if err := s.initMailbox(); err != nil {
				return err
			}
		case "gnot", "bubble":
			if err := s.initToken(n); err != nil {
				return err
			}
		}
	}
	// 2. our three inits
	if err := s.initClient(); err != nil {
		return err
	}
	if err := s.initPausable(); err != nil {
		return err
	}
	if err := s.initAdmin(); err != nil {
		return err
	}
	// 3. the settings
	if err := s.settings(); err != nil {
		return err
	}
	// 4. the owners
	if err := s.owners(); err != nil {
		return err
	}
	// 5. the upgrade authorities, the admin program's own burn last; finish_setup; the authority PDA's float
	if err := s.authorities(); err != nil {
		return err
	}
	return s.finish()
}

// ------------------------------------------------------------- the build --

// launchValues writes the program ids and the gno-side constants into the three programs' config.rs
// (M2-NOTES 21: development values until the setup writes the launch ones).
func (s *setup) launchValues() error {
	if s.noBuild {
		return s.prebuiltValues()
	}
	p := s.progs()
	arr := func(k [32]byte) string {
		parts := make([]string, 32)
		for i, b := range k {
			parts[i] = fmt.Sprintf("0x%02x", b)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	key := func(k solana.PublicKey) string { return "Pubkey::new_from_array(" + arr(k) + ")" }
	dayCap := launchDayCap(s.expectedLock)
	maxPerMessage := launchMaxPerMessage(dayCap)
	realms := s.launchMaximum
	if realms == 0 {
		realms = launchMaximumDefault
	}
	if err := launchMaximumCheck(dayCap, realms); err != nil {
		return err
	}
	edits := []struct {
		file   string
		values map[string]string
	}{
		{"gno-light-client-ism/src/config.rs", map[string]string{"ID": key(p.Client), "MAILBOX_PKG_PATH": strconv.Quote(s.mailboxPkg)}},
		// the pausable ISM carries the mailbox and the tokens too (AN5: its Verify meters the tokens' mints inside the
		// mailbox's deliveries); the admin program's build asserts the two files' literals equal, so both are written
		{"pausable-ism/src/config.rs", map[string]string{"ID": key(p.Pausable), "MAILBOX": key(p.Mailbox), "TOKENS": "[" + key(p.Tokens[0]) + ", " + key(p.Tokens[1]) + "]", "DEFAULT_DAY_CAP": strconv.FormatUint(dayCap, 10), "DEFAULT_MAX_PER_MESSAGE": strconv.FormatUint(maxPerMessage, 10)}},
		{"admin-program/src/config.rs", map[string]string{"ID": key(p.Admin), "MAILBOX": key(p.Mailbox), "TOKENS": "[" + key(p.Tokens[0]) + ", " + key(p.Tokens[1]) + "]", "HLGOV_SENDER": arr(s.hlgov)}},
	}
	s.say("the pausable ISM's launch day cap: %d base units for each token (the smaller of %d and a tenth of the expected locks %d GNOT / %d BUBBLE base units; one DEFAULT_DAY_CAP for both tokens as built, F10)", dayCap, uint64(launchDayCapMax), s.expectedLock[0], s.expectedLock[1])
	s.say("the pausable ISM's launch per-message maximum: %d base units for each token (the smaller of %d and the day cap, DEFAULT_MAX_PER_MESSAGE as built, AR4); the realms' compiled maximum is %d, never above it", maxPerMessage, uint64(launchMaximumDefault), realms)
	for _, e := range edits {
		path := filepath.Join(s.programsDir, e.file)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out := string(src)
		for name, value := range e.values {
			re := regexp.MustCompile(`(?m)^(pub const ` + name + `: [^=]+=) .*;$`)
			if !re.MatchString(out) {
				return fmt.Errorf("%s: no `pub const %s` line to write", path, name)
			}
			out = re.ReplaceAllString(out, "${1} "+value+";")
		}
		if out != string(src) {
			if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
				return err
			}
			s.say("launch values written into %s", e.file)
		} else {
			s.say("launch values already in %s", e.file)
		}
	}
	return nil
}

// prebuiltValues is launchValues under -no-build (G51): the .so files are used as they are, so the launch day
// cap and per-message maximum the expected locks compile are checked against the pausable ISM's ELF before the
// first deploy — AR4's refusal (launchMaximumCheck) and the ELF's own DEFAULT_DAY_CAP and DEFAULT_MAX_PER_MESSAGE
// read from its bytes (sol.ELFBindsU64, as the audit reads the ids it binds) — never after ≈9 SOL of rent and
// the admin program's burn, when the cure is a kind 12 after launch.
func (s *setup) prebuiltValues() error {
	if s.expectedLock[0] == 0 || s.expectedLock[1] == 0 {
		return errors.New("hlsol setup: -no-build needs -expected-lock-gnot and -expected-lock-bubble too: the pausable ISM's launch day cap and per-message maximum compiled into the .so are checked against them before the first deploy (G51)")
	}
	dayCap := launchDayCap(s.expectedLock)
	maxPerMessage := launchMaxPerMessage(dayCap)
	realms := s.launchMaximum
	if realms == 0 {
		realms = launchMaximumDefault
	}
	if err := launchMaximumCheck(dayCap, realms); err != nil {
		return err
	}
	elf, err := os.ReadFile(s.soFile("pausable"))
	if err != nil {
		return fmt.Errorf("hlsol setup: -no-build: %w", err)
	}
	elf = elf[:sol.ELFEnd(elf)]
	for _, c := range []struct {
		name  string
		value uint64
	}{{"DEFAULT_DAY_CAP", dayCap}, {"DEFAULT_MAX_PER_MESSAGE", maxPerMessage}} {
		if !sol.ELFBindsU64(elf, c.value) {
			return fmt.Errorf("hlsol setup: -no-build: %s does not bind %s = %d, the figure the expected locks compile (a tenth of the smaller lock, at most 100,000; the maximum the smaller of 25,000 and the day cap): the .so was built with other launch values — rebuild it with these locks, or pass the locks it was built with (G51; checked before any deploy, since the audit's check after the setup comes after the rent and the admin program's burn)", s.soFile("pausable"), c.name, c.value)
		}
	}
	s.say("launch values: -no-build, the .so files are used as they are; the pausable ISM's ELF binds the launch day cap %d and per-message maximum %d the expected locks compile (G51)", dayCap, maxPerMessage)
	return nil
}

// build runs cargo-build-sbf for the three programs when their sources or the factor changed since the last
// build (a stamp beside the .so files), the devnet factor as a feature.
func (s *setup) build() error {
	if s.noBuild {
		return nil
	}
	stamp := filepath.Join(s.soDir, "build-stamp")
	h := sha256.New()
	for _, crate := range []string{"gno-light-core", "sol-util", "gno-light-client-ism", "pausable-ism", "admin-program"} {
		dir := filepath.Join(s.programsDir, crate)
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".rs") && !strings.HasSuffix(path, "Cargo.toml") {
				return nil
			}
			b, _ := os.ReadFile(path)
			h.Write([]byte(path))
			h.Write(b)
			return nil
		})
	}
	fmt.Fprintf(h, "factor %d", s.factor)
	want := hex.EncodeToString(h.Sum(nil))
	have, _ := os.ReadFile(stamp)
	built := true
	for _, so := range []string{"gno_light_client_ism.so", "pausable_ism.so", "admin_program.so"} {
		if _, err := os.Stat(filepath.Join(s.soDir, so)); err != nil {
			built = false
		}
	}
	if built && strings.TrimSpace(string(have)) == want {
		s.say("build: up to date (stamp %s)", want[:12])
		return nil
	}
	for _, crate := range []string{"gno-light-client-ism", "pausable-ism", "admin-program"} {
		args := []string{"--manifest-path", filepath.Join(s.programsDir, crate, "Cargo.toml"), "--sbf-out-dir", s.soDir}
		if s.factor == 8 {
			args = append(args, "--features", "clock-factor-8")
		} else if s.factor != 1 {
			return fmt.Errorf("the programs compile a factor of 1 or 8, not %d", s.factor)
		}
		s.say("build: cargo-build-sbf %s", strings.Join(args, " "))
		cmd := exec.CommandContext(s.ctx, "nice", append([]string{"-n", "10", s.bin("cargo-build-sbf")}, args...)...)
		cmd.Dir = s.programsDir
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("building %s: %w", crate, err)
		}
	}
	return os.WriteFile(stamp, []byte(want+"\n"), 0o644)
}

// ------------------------------------------------------------ the deploys --

func (s *setup) soFile(name string) string {
	switch name {
	case "mailbox":
		return filepath.Join(s.soDir, "mailbox.so")
	case "gnot", "bubble":
		return filepath.Join(s.soDir, "token.so")
	case "client":
		return filepath.Join(s.soDir, "gno_light_client_ism.so")
	case "pausable":
		return filepath.Join(s.soDir, "pausable_ism.so")
	}
	return filepath.Join(s.soDir, "admin_program.so")
}

// deploy puts one program on chain under the deployer's upgrade authority, --max-len at the ELF's size, any
// buffer of a failed deploy closed first; a program already executable is left alone.
func (s *setup) deploy(name string) error {
	id := s.keys.id(name)
	if info, err := s.c.RPC.GetAccountInfoWithOpts(s.ctx, id, &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed}); err == nil && info.Value != nil && info.Value.Executable {
		s.say("deploy %s: %s is deployed", name, id)
		return nil
	}
	elf, err := os.ReadFile(s.soFile(name))
	if err != nil {
		return err
	}
	_ = s.cli("program", "close", "--buffers", "--url", s.rpcURL, "--keypair", s.deployerPath, "--commitment", "confirmed")
	s.say("deploy %s: %s from %s (%d bytes, --max-len %d)", name, id, s.soFile(name), len(elf), len(elf))
	if err := s.cli(s.deployArgs(name, len(elf))...); err != nil {
		return err
	}
	return s.waitUsable(name)
}

// deployArgs is the CLI's program deploy for one program: the loader's account at --max-len, the deployer its
// upgrade authority until the setup burns or moves it, the write transactions' path, attempts and fee as set.
func (s *setup) deployArgs(name string, size int) []string {
	args := []string{"program", "deploy", "--url", s.rpcURL, "--keypair", s.deployerPath, "--program-id", s.keys.files[name], "--upgrade-authority", s.deployerPath,
		"--max-len", strconv.Itoa(size), "--commitment", "confirmed"}
	if s.deploySend != "tpu" {
		args = append(args, "--use-rpc")
	}
	if s.maxSignAttempts > 0 {
		args = append(args, "--max-sign-attempts", strconv.Itoa(s.maxSignAttempts))
	}
	if s.cuPrice > 0 {
		args = append(args, "--with-compute-unit-price", strconv.FormatUint(s.cuPrice, 10))
	}
	return append(args, s.soFile(name))
}

// waitUsable returns once a program just deployed can be invoked: the loader refuses a program in the slot it
// was deployed in ("Program is not deployed"), so the slot must have moved past the programdata's.
func (s *setup) waitUsable(name string) error {
	id := s.keys.id(name)
	for i := 0; i < 60; i++ {
		d, err := s.data(sol.ProgramData(id))
		if err == nil {
			deployed, _, _, perr := sol.ParseProgramData(d)
			slot, serr := s.c.RPC.GetSlot(s.ctx, rpc.CommitmentConfirmed)
			if perr == nil && serr == nil && slot > deployed {
				if info, err := s.c.RPC.GetAccountInfoWithOpts(s.ctx, id, &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed}); err == nil && info.Value != nil && info.Value.Executable {
					return nil
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("deploy %s: not usable after 30 s", name)
}

func (s *setup) cli(args ...string) error {
	cmd := exec.CommandContext(s.ctx, s.bin("solana"), args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// ----------------------------------------------------------------- inits --

func (s *setup) exists(k solana.PublicKey) bool {
	info, err := s.c.RPC.GetAccountInfoWithOpts(s.ctx, k, &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed})
	return err == nil && info.Value != nil && len(info.Value.Data.GetBinary()) > 0
}

func (s *setup) data(k solana.PublicKey) ([]byte, error) {
	info, err := s.c.RPC.GetAccountInfoWithOpts(s.ctx, k, &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed})
	if err != nil {
		return nil, err
	}
	if info.Value == nil {
		return nil, errors.New("no account")
	}
	return info.Value.Data.GetBinary(), nil
}

func (s *setup) send(what string, ixs ...solana.Instruction) error {
	var sig solana.Signature
	var err error
	for i := 0; i < 10; i++ {
		sig, err = s.c.Send(s.ctx, ixs)
		if err == nil || !strings.Contains(err.Error(), "UnsupportedProgramId") {
			break
		}
		time.Sleep(time.Second) // a program deployed this very slot (the loader's "Program is not deployed")
	}
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	s.say("%s: %s", what, sig)
	return nil
}

// initMailbox sends Hyperlane's mailbox init: our domain, the pausable ISM as the default ISM (never a raw
// one), the 0.05 SOL fee maximum, a fee of 0, the treasurer as the beneficiary.
func (s *setup) initMailbox() error {
	m := sol.Mailbox{Program: s.keys.id("mailbox")}
	if s.exists(m.Inbox()) {
		s.say("mailbox init: done")
		return nil
	}
	return s.send("mailbox init", m.InitIx(s.deployer.PublicKey(), s.localDomain, s.keys.id("pausable"), sol.MaxProtocolFee, 0, s.treasurer))
}

// initToken sends a token's init (the pausable ISM as its ISM, no paymaster), the Token-2022 mint setup, the
// metadata, the mint authority to the mint itself, and the ATA payer's float — each step checked.
func (s *setup) initToken(name string) error {
	idx := 0
	if name == "bubble" {
		idx = 1
	}
	t := sol.Synthetic{Program: s.keys.id(name)}
	me := s.deployer.PublicKey()
	mint := t.Mint()
	pausable := s.keys.id("pausable")
	if !s.exists(t.Token()) {
		ixs := []solana.Instruction{sol.ComputeLimitIx(1_000_000), t.InitIx(me, s.keys.id("mailbox"), &pausable, s.decimals, s.decimals)}
		ixs = append(ixs, sol.MintSetupIxs(mint, me, s.decimals)...)
		if err := s.send(name+" init and mint", ixs...); err != nil {
			return err
		}
	} else {
		s.say("%s init: done", name)
	}
	md, err := s.data(mint)
	if err != nil {
		return err
	}
	m2, err := sol.ParseMint2022(md)
	if err != nil {
		return err
	}
	nm := s.names[idx]
	if !m2.HasMetadata {
		extra := uint64(4 + 32 + 32 + 4 + len(nm[0]) + 4 + len(nm[1]) + 4 + len(nm[2]) + 4)
		need, err := s.c.RPC.GetMinimumBalanceForRentExemption(s.ctx, uint64(len(md))+extra+64, rpc.CommitmentConfirmed)
		if err != nil {
			return err
		}
		have, err := s.c.RPC.GetBalance(s.ctx, mint, rpc.CommitmentConfirmed)
		if err != nil {
			return err
		}
		top := uint64(0)
		if need > have.Value {
			top = need - have.Value
		}
		if err := s.send(name+" metadata", sol.MetadataIxs(mint, me, nm[0], nm[1], nm[2], top)...); err != nil {
			return err
		}
	} else {
		s.say("%s metadata: done (%s %s)", name, m2.Name, m2.Symbol)
	}
	if m2.MintAuthority != nil && !m2.MintAuthority.Equals(mint) {
		if err := s.send(name+" mint authority to the mint", sol.MintAuthorityToSelfIx(mint, me)); err != nil {
			return err
		}
	} else {
		s.say("%s mint authority: the mint", name)
	}
	bal, err := s.c.RPC.GetBalance(s.ctx, t.ATAPayer(), rpc.CommitmentConfirmed)
	if err != nil {
		return err
	}
	if bal.Value < s.ataFund {
		if err := s.send(fmt.Sprintf("%s ATA payer funded with %d lamports", name, s.ataFund-bal.Value), sol.TransferIx(me, t.ATAPayer(), s.ataFund-bal.Value)); err != nil {
			return err
		}
	} else {
		s.say("%s ATA payer: %d lamports", name, bal.Value)
	}
	return nil
}

// trustedHeader is the header the client starts from: the latest non-announcing header at or below the asked
// height (the one before an announcing one cannot be followed, see M3-NOTES), less than an hour old.
func (s *setup) trustedHeader(g *gno.Client) (*gnolight.Block, *gnolight.ValidatorSet, error) {
	head, err := g.Status(s.ctx)
	if err != nil {
		return nil, nil, err
	}
	h := s.trustedHeight
	if h == 0 || h > head.Height {
		h = head.Height
	}
	for ; h >= 1; h-- {
		blk, err := g.BlockAt(s.ctx, h)
		if err != nil {
			return nil, nil, err
		}
		if blk.Header.Announcing() {
			s.say("trusted header: %d announces a set change; taking the one before", h)
			continue
		}
		set, err := g.ValidatorsAt(s.ctx, h)
		if err != nil {
			return nil, nil, err
		}
		if sh, _ := set.Hash(); sh != blk.Header.ValidatorsHashArray() {
			return nil, nil, fmt.Errorf("the validators at %d do not hash to the header's ValidatorsHash", h)
		}
		if age := time.Since(time.Unix(blk.Header.Time.Secs, 0)); age > time.Hour {
			if !s.allowOld {
				return nil, nil, fmt.Errorf("trusted header %d is %s old: §6.1 wants one less than an hour old (the node lags, or -trusted-height names an old one); -allow-old-header takes it anyway, never for a deployment", h, age.Round(time.Minute))
			}
			s.say("WARNING trusted header %d is %s old (§6.1 asks for one less than an hour old; taken on -allow-old-header)", h, age.Round(time.Minute))
		}
		return blk, set, nil
	}
	return nil, nil, errors.New("no non-announcing header to trust")
}

func (s *setup) initClient() error {
	client := sol.LightClient{Program: s.keys.id("client")}
	me := s.deployer.PublicKey()
	if !s.exists(client.State()) {
		g := &gno.Client{RPC: s.gnoRPC}
		chain := s.gnoChain
		if chain == "" {
			blk, err := g.BlockAt(s.ctx, 1)
			if err != nil {
				return err
			}
			chain = blk.Header.ChainID
		}
		blk, set, err := s.trustedHeader(g)
		if err != nil {
			return err
		}
		s.say("client init: chain %s, origin %d, trusted header %d %x (%d validators, set hash %x)", chain, s.gnoDomain, blk.Header.Height, blk.Hash, len(set.Validators), blk.Header.ValidatorsHash)
		admin := sol.Admin{Program: s.keys.id("admin")}.Authority()
		if err := s.send("client init", client.InitIx(me, me, chain, s.gnoDomain, uint64(blk.Header.Height), blk.Hash, set.Validators, admin)); err != nil {
			return err
		}
	} else {
		s.say("client init: done")
	}
	for i := 0; i < 8; i++ {
		data, err := s.data(client.State())
		if err != nil {
			return err
		}
		head, err := sol.ParseClientHead(data)
		if err != nil {
			return err
		}
		if sol.RingCapacity(head, len(data)) >= sol.Ring {
			s.say("client state: %d bytes, the ring holds %d entries", len(data), sol.Ring)
			return nil
		}
		if err := s.send("client grow", client.GrowIx(me, 0)); err != nil {
			return err
		}
	}
	return errors.New("the client's state did not reach its full size")
}

func (s *setup) initPausable() error {
	p := sol.Pausable{Program: s.keys.id("pausable")}
	if s.exists(p.State()) {
		s.say("pausable ISM init: done")
		return nil
	}
	me := s.deployer.PublicKey()
	var seats [sol.Seats]solana.PublicKey
	var watch [sol.Watch]solana.PublicKey
	copy(seats[:], s.seats)
	copy(watch[:], s.watch)
	return s.send("pausable ISM init", p.InitIx(me, me, sol.Admin{Program: s.keys.id("admin")}.Authority(), s.keys.id("client"), sol.ClientStateVersion, seats, watch))
}

func (s *setup) initAdmin() error {
	a := sol.Admin{Program: s.keys.id("admin")}
	if s.exists(a.State()) {
		s.say("admin program init: done")
		return nil
	}
	me := s.deployer.PublicKey()
	var seats [sol.Seats]solana.PublicKey
	var watch [sol.Watch]solana.PublicKey
	copy(seats[:], s.seats)
	copy(watch[:], s.watch)
	// the launch deployment: no predecessor (a successor's init is `hlsol successor-init`, C227); the sequence
	// starts at -start-sequence, checked against r/hlgov's counter before the first deploy (AT3)
	return s.send(fmt.Sprintf("admin program init (the sequence starts at %d)", s.startSequence), a.InitIx(me, me, seats, watch, s.gnoDomain, solana.PublicKey{}, s.startSequence))
}

// -------------------------------------------------------------- settings --

// settings: the routers, the ISM pointers, the default ISM, the beneficiary, the metadata authorities.
func (s *setup) settings() error {
	me := s.deployer.PublicKey()
	m := sol.Mailbox{Program: s.keys.id("mailbox")}
	pausable := s.keys.id("pausable")
	for i, name := range []string{"gnot", "bubble"} {
		t := sol.Synthetic{Program: s.keys.id(name)}
		td, err := s.data(t.Token())
		if err != nil {
			return err
		}
		tok, err := sol.ParseToken(td)
		if err != nil {
			return err
		}
		if r, ok := tok.RouterFor(s.gnoDomain); !ok || r != s.routers[i] {
			if err := s.send(fmt.Sprintf("%s router for domain %d", name, s.gnoDomain), t.EnrollRemoteRouterIx(me, s.gnoDomain, s.routers[i])); err != nil {
				return err
			}
		} else {
			s.say("%s router for domain %d: enrolled", name, s.gnoDomain)
		}
		if tok.ISM == nil || !tok.ISM.Equals(pausable) {
			if err := s.send(name+" ISM pointer to the pausable ISM", t.SetISMIx(me, &pausable)); err != nil {
				return err
			}
		} else {
			s.say("%s ISM pointer: the pausable ISM", name)
		}
		if tok.IGP != nil {
			if err := s.send(name+" paymaster cleared", t.ClearIGPIx(me)); err != nil {
				return err
			}
		}
		md, err := s.data(t.Mint())
		if err != nil {
			return err
		}
		m2, err := sol.ParseMint2022(md)
		if err != nil {
			return err
		}
		if s.keepMeta {
			s.say("%s metadata authorities: kept (-keep-metadata-authority)", name)
		} else {
			if m2.HasMetadata && m2.UpdateAuthority != nil {
				if err := s.send(name+" metadata update authority to none", sol.MetadataUpdateAuthorityToNoneIx(t.Mint(), me)); err != nil {
					return err
				}
			} else {
				s.say("%s metadata update authority: none", name)
			}
			if m2.HasPointer && m2.PointerAuthority != nil {
				if err := s.send(name+" metadata pointer authority to none", sol.PointerAuthorityToNoneIx(t.Mint(), me)); err != nil {
					return err
				}
			} else {
				s.say("%s metadata pointer authority: none", name)
			}
		}
	}
	ind, err := s.data(m.Inbox())
	if err != nil {
		return err
	}
	inbox, err := sol.ParseInbox(ind)
	if err != nil {
		return err
	}
	if !inbox.DefaultISM.Equals(pausable) {
		if err := s.send("mailbox default ISM to the pausable ISM", m.SetDefaultISMIx(me, pausable)); err != nil {
			return err
		}
	} else {
		s.say("mailbox default ISM: the pausable ISM")
	}
	od, err := s.data(m.Outbox())
	if err != nil {
		return err
	}
	outbox, err := sol.ParseOutbox(od)
	if err != nil {
		return err
	}
	if !outbox.Beneficiary.Equals(s.treasurer) || outbox.Fee != 0 {
		if err := s.send("mailbox fee 0, beneficiary the treasurer", m.SetProtocolFeeIx(me, 0, s.treasurer)); err != nil {
			return err
		}
	} else {
		s.say("mailbox fee beneficiary: the treasurer %s", s.treasurer)
	}
	return nil
}

// ---------------------------------------------------------------- owners --

func (s *setup) owners() error {
	me := s.deployer.PublicKey()
	pda := sol.Admin{Program: s.keys.id("admin")}.Authority()
	m := sol.Mailbox{Program: s.keys.id("mailbox")}
	od, err := s.data(m.Outbox())
	if err != nil {
		return err
	}
	outbox, err := sol.ParseOutbox(od)
	if err != nil {
		return err
	}
	if outbox.Owner == nil || !outbox.Owner.Equals(pda) {
		if err := s.send("mailbox owner to the admin program", m.TransferOwnershipIx(me, &pda)); err != nil {
			return err
		}
	} else {
		s.say("mailbox owner: the admin program")
	}
	for _, name := range []string{"gnot", "bubble"} {
		t := sol.Synthetic{Program: s.keys.id(name)}
		td, err := s.data(t.Token())
		if err != nil {
			return err
		}
		tok, err := sol.ParseToken(td)
		if err != nil {
			return err
		}
		if tok.Owner == nil || !tok.Owner.Equals(pda) {
			if err := s.send(name+" owner to the admin program", t.TransferOwnershipIx(me, &pda)); err != nil {
				return err
			}
		} else {
			s.say("%s owner: the admin program", name)
		}
	}
	pd, err := s.data(sol.Pausable{Program: s.keys.id("pausable")}.State())
	if err != nil {
		return err
	}
	ps, err := sol.ParsePausableState(pd)
	if err != nil {
		return err
	}
	if !ps.Owner.Equals(pda) {
		return fmt.Errorf("the pausable ISM's owner is %s, not the admin program's PDA: its init was wrong", ps.Owner)
	}
	s.say("pausable ISM owner: the admin program")
	return nil
}

// ----------------------------------------------------------- authorities --

func (s *setup) authorities() error {
	me := s.deployer.PublicKey()
	a := sol.Admin{Program: s.keys.id("admin")}
	pda := a.Authority()
	for _, name := range []string{"mailbox", "gnot", "bubble", "client", "pausable"} {
		prog := s.keys.id(name)
		pd, err := s.data(sol.ProgramData(prog))
		if err != nil {
			return err
		}
		_, auth, _, err := sol.ParseProgramData(pd)
		if err != nil {
			return err
		}
		switch {
		case auth != nil && auth.Equals(pda):
			s.say("%s upgrade authority: the admin program", name)
		case auth != nil && auth.Equals(me):
			if err := s.send(name+" upgrade authority to the admin program (through it)", a.TransferAuthorityInIx(prog, me)); err != nil {
				return err
			}
		case auth == nil:
			return fmt.Errorf("%s's upgrade authority is burned: the admin program can never upgrade it", name)
		default:
			return fmt.Errorf("%s's upgrade authority is %s, neither the deployer nor the admin program", name, auth)
		}
	}
	pd, err := s.data(sol.ProgramData(a.Program))
	if err != nil {
		return err
	}
	_, auth, _, err := sol.ParseProgramData(pd)
	if err != nil {
		return err
	}
	if auth == nil {
		s.say("admin program upgrade authority: burned")
	} else if auth.Equals(me) {
		if err := s.send("admin program upgrade authority burned (last)", sol.SetAuthorityIx(sol.ProgramData(a.Program), me, nil)); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("the admin program's upgrade authority is %s, not the deployer", auth)
	}
	return nil
}

func (s *setup) finish() error {
	a := sol.Admin{Program: s.keys.id("admin")}
	ad, err := s.data(a.State())
	if err != nil {
		return err
	}
	st, err := sol.ParseAdminState(ad)
	if err != nil {
		return err
	}
	if st.SetupDone == 1 {
		s.say("finish_setup: done")
	} else if err := s.send("finish_setup (with the pausable ISM's flags: the three programs' lists, clocks and origin compared, S-06)", a.FinishSetupIx(s.progs())); err != nil {
		return err
	}
	bal, err := s.c.RPC.GetBalance(s.ctx, a.Authority(), rpc.CommitmentConfirmed)
	if err != nil {
		return err
	}
	if bal.Value < s.authorityFund {
		return s.send(fmt.Sprintf("authority PDA funded with %d lamports", s.authorityFund-bal.Value), sol.TransferIx(s.deployer.PublicKey(), a.Authority(), s.authorityFund-bal.Value))
	}
	s.say("authority PDA: %d lamports", bal.Value)
	return nil
}

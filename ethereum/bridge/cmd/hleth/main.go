// Command hleth is the Ethereum deploy's tool (eth/bridge/PLAN.md §5.8, §8), the Solana tools' hlsol and hlrecover
// carried to Ethereum: the deploy and the setup step in their binding order, resumable from the chain; the audit a
// member repeats by hand; the members' commands on HlAdmin; the test sends.
//
//	hleth setup   -rpc URL -deployer KEY -contracts DIR -hyperlane DIR -out deployment.json
//	              -gno-domain N -mailbox-pkgpath PATH -hlgov PATH -gnot-route PATH -bubble-route PATH
//	              -seats A,B,C,D -watch E,F -beneficiary ADDR (-gno-rpc URL [-gno-chain-id ID] [-trusted-height H] | -trusted-file FILE)
//	              -expected-lock-gnot N -expected-lock-bubble N (base units; required but on a local chain) [-launch-maximum N]
//	              [-local-domain N] [-max-per-message N,N] [-day-cap N,N] [-start-sequence N] [-max-protocol-fee WEI]
//	              [-name0 .. -symbol0 .. -name1 .. -symbol1 ..] [-max-base-fee-gwei G] [-poll 1s] [-config-out eth.json]
//	hleth audit   -rpc URL -deployment deployment.json -contracts DIR -hyperlane DIR [-factor N] [-at finalized|latest|safe|N]
//	              -gno-rpc URL[,URL]     (a gno node of your own: without one the trust root is UNVERIFIED, no PASS)
//	              [-config bridge.json]  (the operator's: its gno sources read beside yours, never alone — alone,
//	                  UNVERIFIED; its routes' expectedLock only noted, never a launch value: E5-CF3 item 3)
//	              -mailbox-pkgpath PATH -hlgov PATH -gnot-route PATH -bubble-route PATH   (without each, UNVERIFIED)
//	              -seats A,B,C,D -watch E,F -beneficiary ADDR -expected-lock-gnot N -expected-lock-bubble N
//	                  (the LAUNCH values, the launch notice's — never as they stand after a kind 5, 7 or 12; without
//	                  each, UNVERIFIED)
//	              [-start-sequence N -launch-maximum N]
//	hleth decision status  -rpc URL -deployment deployment.json
//	hleth decision body    -kind K [-cancel 5,7 | -cancel 5-9] [-salt N] [-object 0|1] [-token T -ism ADDR] [-seat-changes 1=ADDR,5=ADDR]
//	                       [-token T -domain D -router ADDR|HEX32] [-fee WEI -beneficiary ADDR] [-proxy ADDR -impl ADDR -codehash HEX [-call HEX]]
//	                       [-successor ADDR -codehash HEX] [-token T -max N -day-cap N] | -kind K -params HEX
//	hleth decision execute -rpc URL -deployment deployment.json -key KEY -number N
//	hleth send    -rpc URL -key KEY -token ADDR -to g1… -amount N -gno-domain N [-refuse-realms PATH,PATH]
//	hleth balance -rpc URL -token ADDR -owner ADDR
//	hleth keygen  -o FILE [-seed TEXT]        (a seed: a deterministic key for LOCAL runs only)
//	hleth version
//
// Every flag that names a deployment is required: no default points at another deployment. `setup` reads the chain
// before every step and repeats nothing done; its done condition is "no human key holds an authority" and
// HlAdmin's own finishSetup; it ends "hleth setup: DONE (sent nothing: …)" on a run that found everything done.
// `audit` prints every RPC call it makes before its answer, as a curl line against $RPC, and ends "hleth audit:
// PASS", "hleth audit: FAIL (n)", or "hleth audit: UNVERIFIED (n)" — no failure, but a binding, the trust root or a
// launch value held to the deployer's record alone (no gno source, a path flag, the seats, the watch keys, the
// beneficiary or an expected lock missing: E3-CF2, E3-GS4, E4-TL1), or gno.land read from the operator's config alone
// (E5-CF3 item 3) — both with exit status 1.
// After governed changes it holds each moved value to the executed decision that moved it (E3-CF9). The contracts: -contracts is a forge out directory of
// eth/bridge/contracts built with `forge build --force --ast` (the clock factor chosen by its bridge-clock
// remapping); -hyperlane is local/hyperlane-artifacts.sh's output, the Hyperlane 12.2.0 release's own compile.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

const usage = "usage: hleth setup|audit|decision status|decision body|decision execute|send|balance|keygen|version [flags]"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "decision" {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		cmd, args = "decision "+args[0], args[1:]
	}
	log.SetOutput(ethx.RedactWriter{W: os.Stderr}) // no provider URL's key in a log line (G16)
	log.SetFlags(0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cmd, args); err != nil {
		log.Fatal(ethx.Redact(err.Error()))
	}
}

func run(ctx context.Context, cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	rpcURL := fs.String("rpc", "", "an Ethereum provider (its URL is never printed)")
	deployer := fs.String("deployer", "", "setup: the deployer's key file (64 hex digits, mode 0600)")
	key := fs.String("key", "", "decision execute, send: the sender's key file")
	contracts := fs.String("contracts", "", "setup, audit: a forge out directory of eth/bridge/contracts, built with --force --ast")
	hyperlane := fs.String("hyperlane", "", "setup, audit: the Hyperlane 12.2.0 release's compile (local/hyperlane-artifacts.sh)")
	out := fs.String("out", "", "setup: the deployment's record (deployment.json), written after every step")
	deployment := fs.String("deployment", "", "audit, decision: the deployment's record")
	localDomain := fs.Uint("local-domain", 0, "setup: Ethereum's Hyperlane domain (1 mainnet, 11155111 Sepolia, 31337 locally; the chain id when 0)")
	gnoDomain := fs.Uint("gno-domain", 0, "gno.land's domain (1198419788 gnoland-1, 1198419796 any other chain)")
	gnoRPC := fs.String("gno-rpc", "", "a gno node: the trusted header (setup); audit: your own, one or more (comma separated, each read, all must agree) — the client's trust root, r/hlgov's counter and seats, the routes")
	configPath := fs.String("config", "", "audit: the machines' config (bridge.json), the operator's file: its gno.rpc and gno.sources are read beside -gno-rpc's, every reading compared (alone they hold gno.land to the operator's word: UNVERIFIED); its routes' expectedLock is only noted, never the launch value (E5-CF3 item 3)")
	expectedGNOT := fs.Uint64("expected-lock-gnot", 0, "setup, audit: the GNOT lock (token 0's) the launch notice expects, base units (§5.4: the gate's launch day cap is the smaller of 100,000 and a tenth of it, the maximum the smaller of 25,000 and the cap); audit: the launch value, yours — without it, UNVERIFIED (a config's expectedLock is the operator's, never taken)")
	expectedBUBBLE := fs.Uint64("expected-lock-bubble", 0, "setup, audit: the BUBBLE lock (token 1's) the launch notice expects, base units; audit: the launch value, yours — without it, UNVERIFIED (a config's expectedLock is the operator's, never taken)")
	launchMax := fs.Uint64("launch-maximum", 0, "setup, audit: the per-message maximum the route realms compile, base units (0: their source's 25,000); the gate's is never below it")
	gnoChain := fs.String("gno-chain-id", "", "setup: the gno chain id (the node's when empty)")
	pkgPath := fs.String("mailbox-pkgpath", "", "the deployment's hlmailbox path (gno.land/r/<ns>/eth/hlmailbox): compiled into the client as its keccak256")
	hlgov := fs.String("hlgov", "", "r/hlgov's path: the one sender HlAdmin obeys, its 32-byte address")
	gnotRoute := fs.String("gnot-route", "", "r/hlgnot's path: token 0's router for gno.land")
	bubbleRoute := fs.String("bubble-route", "", "r/hlbubble's path: token 1's router for gno.land")
	seats := fs.String("seats", "", "setup, audit: the four guardians' Ethereum addresses AT LAUNCH, seats 1–4 (3 and 4 the outsiders'); audit: the launch notice's — what the creation inputs carry and every kind 5 is replayed from — never the seats as they stand after a kind 5; without it, UNVERIFIED")
	watch := fs.String("watch", "", "setup, audit: the two watch keys' Ethereum addresses AT LAUNCH, slots 5–6; audit: the launch notice's, never as they stand after a kind 5; without it, UNVERIFIED")
	beneficiary := fs.String("beneficiary", "", "setup, audit: the protocol fee's beneficiary AT LAUNCH (the treasurer's Ethereum address); audit: the launch notice's, never as a kind 7 set it since; without it, UNVERIFIED; decision body, kind 7: the new beneficiary")
	maxPer := fs.String("max-per-message", "", "setup: the gate's per-message maximum of token 0 and token 1, base units (what the expected lock compiles; 25,000 each on a local chain with none)")
	dayCapPair := fs.String("day-cap", "", "setup: the gate's day cap of token 0 and token 1, base units (what the expected lock compiles; 100,000 each on a local chain with none); decision body, kind 12: the day cap")
	startSeq := fs.Uint64("start-sequence", 0, "setup: the number HlAdmin's sequence starts after: r/hlgov's counter (0 for a chain's first deployment; at most 2^32); audit: the start expected while r/hlgov is not up (0 when not given)")
	trustedHeight := fs.Int64("trusted-height", 0, "setup: the newest gno height to trust (the node's head when 0); an announcing header is passed over")
	configOut := fs.String("config-out", "", "setup: write the machines' config's deployment part (bridge.json's eth contracts, domains and eth.deployBlock — finishSetup's block, where every machine's scan starts — and the routes' tokens) once the setup is done")
	trustedFile := fs.String("trusted-file", "", "setup: an offline trusted header ({header: the node's /block block.header, validators: [{pubkey, power}]}) instead of -gno-rpc's; checked as a node's: no change announced, within 30 minutes of Ethereum's newest block")
	maxFee := fs.String("max-protocol-fee", "", "setup: the ProtocolFee's immutable maximum in wei (0.001 ETH: HlAdmin's compiled bound, which finishSetup checks)")
	name0 := fs.String("name0", "GNOT", "setup: token 0's name")
	symbol0 := fs.String("symbol0", "GNOT", "setup: token 0's symbol")
	name1 := fs.String("name1", "BUBBLE", "setup: token 1's name")
	symbol1 := fs.String("symbol1", "BUBBLE", "setup: token 1's symbol")
	maxBase := fs.Float64("max-base-fee-gwei", 50, "setup, decision execute, send: no transaction is sent while the base fee is above it")
	poll := fs.Duration("poll", time.Second, "setup, decision execute, send: how often a transaction's receipt is asked for")
	factor := fs.Uint64("factor", 1, "audit: the clock factor the contracts must carry (1 mainnet, 8 devnet; a local run's own)")
	at := fs.String("at", "finalized", "audit: the block every reading is made at (finalized for a holder's audit; latest right after the setup)")
	kind := fs.Uint("kind", 0, "decision body: the kind (§13)")
	cancel := fs.String("cancel", "", "decision body, kind 1: a list (5,7) or a range (5-9) of decision numbers")
	salt := fs.Uint64("salt", 0, "decision body, kind 1: the salt (0: from the clock)")
	object := fs.Uint("object", 0, "decision body, kind 2: 0 the gate, 1 HlAdmin")
	token := fs.String("token", "", "decision body: the token (0 GNOT, 1 BUBBLE); send, balance: the token's address")
	ism := fs.String("ism", "", "decision body, kind 3: the gate's address or the zero address")
	seatChanges := fs.String("seat-changes", "", "decision body, kind 5: seat=ADDR pairs (seats 1–4 guardians, 5–6 watch slots)")
	domain := fs.Uint("domain", 0, "decision body, kind 6: the domain")
	router := fs.String("router", "", "decision body, kind 6: the router (an address, or 64 hex digits)")
	feeWei := fs.Uint64("fee", 0, "decision body, kind 7: the fee in wei")
	proxy := fs.String("proxy", "", "decision body, kind 8: the proxy")
	impl := fs.String("impl", "", "decision body, kind 8: the implementation")
	codehash := fs.String("codehash", "", "decision body, kinds 8 and 9: the code hash (64 hex digits)")
	call := fs.String("call", "", "decision body, kind 8: the call (hex, may be empty)")
	successor := fs.String("successor", "", "decision body, kind 9: the successor")
	maxN := fs.Uint64("max", 0, "decision body, kind 12: the per-message maximum, base units")
	params := fs.String("params", "", "decision body: raw parameters in hex, for any kind")
	number := fs.Uint64("number", 0, "decision execute: the decision's number")
	to := fs.String("to", "", "send: the gno recipient (g1…)")
	amount := fs.String("amount", "", "send: base units")
	refuse := fs.String("refuse-realms", "", "send: the bridge realms' paths, whose addresses are refused as recipients")
	owner := fs.String("owner", "", "balance: the owner's address")
	o := fs.String("o", "", "keygen: the key file to write")
	seed := fs.String("seed", "", "keygen: a deterministic key from this text (LOCAL runs only)")
	_ = fs.Parse(args)
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if cmd == "decision body" {
		// kind 12's flag -day-cap is one number here, not setup's pair
		return decisionBodyCmd(bodyFlags{Kind: byte(*kind), Cancel: *cancel, Salt: *salt, Object: byte(*object), Token: *token, ISM: *ism,
			SeatChanges: *seatChanges, Domain: uint32(*domain), Router: *router, Fee: *feeWei, Beneficiary: *beneficiary, Proxy: *proxy, Impl: *impl,
			Codehash: *codehash, Call: *call, Successor: *successor, Max: *maxN, DayCap: *dayCapPair, Params: *params})
	}
	sendCfg := ethx.SendConfig{MaxBaseFee: ethx.GweiOf(*maxBase), Poll: *poll}
	switch cmd {
	case "version":
		fmt.Println(buildHash())
		return nil
	case "setup":
		cfg := setupConfig{RPC: *rpcURL, DeployerKey: *deployer, ContractsOut: *contracts, HyperlaneDir: *hyperlane, Out: *out,
			LocalDomain: uint32(*localDomain), GnoDomain: uint32(*gnoDomain), GnoRPC: *gnoRPC, GnoChainID: *gnoChain,
			MailboxPkgPath: *pkgPath, Hlgov: *hlgov, GnotRoute: *gnotRoute, BubbleRoute: *bubbleRoute, StartSequence: *startSeq,
			TrustedHeight: *trustedHeight, TrustedFile: *trustedFile, ConfigOut: *configOut,
			Name0: *name0, Symbol0: *symbol0, Name1: *name1, Symbol1: *symbol1, Send: sendCfg,
			ExpectedLock: [2]uint64{*expectedGNOT, *expectedBUBBLE}, LaunchMaximum: *launchMax}
		var err error
		if cfg.Seats, err = addrs4(*seats); err != nil {
			return fmt.Errorf("-seats: %w", err)
		}
		if cfg.Watch, err = addrs2(*watch); err != nil {
			return fmt.Errorf("-watch: %w", err)
		}
		if *beneficiary != "" {
			if cfg.Beneficiary, err = ethx.ParseAddress(*beneficiary); err != nil {
				return fmt.Errorf("-beneficiary: %w", err)
			}
		}
		if *maxPer != "" {
			if cfg.MaxPerMessage, err = u64Pair(*maxPer); err != nil {
				return fmt.Errorf("-max-per-message: %w", err)
			}
		}
		if *dayCapPair != "" {
			if cfg.DayCap, err = u64Pair(*dayCapPair); err != nil {
				return fmt.Errorf("-day-cap: %w", err)
			}
		}
		if *maxFee != "" {
			v, ok := new(big.Int).SetString(*maxFee, 10)
			if !ok || v.Sign() < 0 {
				return fmt.Errorf("-max-protocol-fee %q: wei, a decimal number", *maxFee)
			}
			cfg.MaxProtocolFee = v
		}
		_, err = runSetup(ctx, cfg, os.Stdout)
		return err
	case "audit":
		acfg := auditConfig{RPC: *rpcURL, Deployment: *deployment, ContractsOut: *contracts, HyperlaneDir: *hyperlane, GnoRPC: *gnoRPC, Config: *configPath,
			MailboxPkgPath: *pkgPath, Hlgov: *hlgov, GnotRoute: *gnotRoute, BubbleRoute: *bubbleRoute, Factor: *factor, At: *at,
			Seats: *seats, Watch: *watch, Beneficiary: *beneficiary, ExpectedLock: [2]uint64{*expectedGNOT, *expectedBUBBLE}, LaunchMaximum: *launchMax}
		if given["start-sequence"] {
			acfg.StartSequence = startSeq
		}
		res, err := runAuditResult(ctx, acfg, os.Stdout)
		if err != nil {
			return err
		}
		if len(res.Fails) > 0 || len(res.Unverified) > 0 {
			os.Exit(1)
		}
		return nil
	case "decision status", "decision execute":
		if *deployment == "" {
			return errors.New("-deployment is required")
		}
		d, err := LoadDeployment(*deployment, false)
		if err != nil {
			return err
		}
		cn, err := dial(ctx, *rpcURL, nil)
		if err != nil {
			return err
		}
		if cmd == "decision status" {
			f, err := readAdminFacts(ctx, cn, d)
			if err != nil {
				return err
			}
			printStatus(os.Stdout, d, f)
			return nil
		}
		if *key == "" || *number == 0 {
			return errors.New("hleth decision execute: -key and -number are required")
		}
		tx, err := keyTxer(cn, *key, sendCfg)
		if err != nil {
			return err
		}
		return executeDecision(ctx, os.Stdout, cn, d, tx, *number)
	case "send":
		if *key == "" || *token == "" || *to == "" || *amount == "" || *gnoDomain == 0 {
			return errors.New("hleth send: -key, -token, -to, -amount and -gno-domain are required")
		}
		t, err := ethx.ParseAddress(*token)
		if err != nil {
			return fmt.Errorf("-token: %w", err)
		}
		rcpt, err := burnRecipient(*to, strings.Split(*refuse, ","))
		if err != nil {
			return err
		}
		n, ok := new(big.Int).SetString(*amount, 10)
		if !ok || n.Sign() <= 0 {
			return fmt.Errorf("-amount %q: a positive number of base units", *amount)
		}
		cn, err := dial(ctx, *rpcURL, nil)
		if err != nil {
			return err
		}
		tx, err := keyTxer(cn, *key, sendCfg)
		if err != nil {
			return err
		}
		return sendBurn(ctx, os.Stdout, cn, tx, t, uint32(*gnoDomain), rcpt, n)
	case "balance":
		t, err := ethx.ParseAddress(*token)
		if err != nil {
			return fmt.Errorf("-token: %w", err)
		}
		ow, err := ethx.ParseAddress(*owner)
		if err != nil {
			return fmt.Errorf("-owner: %w", err)
		}
		cn, err := dial(ctx, *rpcURL, nil)
		if err != nil {
			return err
		}
		b, err := balanceOf(ctx, cn, t, ow)
		if err != nil {
			return err
		}
		fmt.Println(b.String())
		return nil
	case "keygen":
		if *o == "" {
			return errors.New("hleth keygen: -o is required")
		}
		a, err := keygen(*o, *seed)
		if err != nil {
			return err
		}
		fmt.Println(a.Hex())
		return nil
	}
	return errors.New(usage)
}

// keyTxer is a sender for a key file (decision execute, send).
func keyTxer(cn *conn, path string, cfg ethx.SendConfig) (*txer, error) {
	k, err := ethx.LoadKey(path)
	if err != nil {
		return nil, err
	}
	return newTxer(cn, k, cfg, func(format string, a ...any) { fmt.Printf(format+"\n", a...) })
}

func addrsN(s string, n int) ([]common.Address, error) {
	parts := strings.Split(s, ",")
	if s == "" || len(parts) != n {
		return nil, fmt.Errorf("%d addresses, comma separated", n)
	}
	out := make([]common.Address, n)
	for i, p := range parts {
		a, err := ethx.ParseAddress(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		out[i] = a
	}
	return out, nil
}

func addrs4(s string) ([4]common.Address, error) {
	var out [4]common.Address
	as, err := addrsN(s, 4)
	if err == nil {
		copy(out[:], as)
	}
	return out, err
}

func addrs2(s string) ([2]common.Address, error) {
	var out [2]common.Address
	as, err := addrsN(s, 2)
	if err == nil {
		copy(out[:], as)
	}
	return out, err
}

func u64Pair(s string) ([2]uint64, error) {
	var out [2]uint64
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return out, errors.New("two numbers, comma separated (token 0's, token 1's)")
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(strings.ReplaceAll(p, "_", "")), 10, 64)
		if err != nil {
			return out, err
		}
		out[i] = n
	}
	return out, nil
}

// buildHash is the running binary's sha256 and what the Go toolchain recorded of its source: the reproducible build
// hash a member compares with their own compile (§5.8), as the Solana tools print theirs.
func buildHash() string {
	out := "hleth build:"
	if exe, err := os.Executable(); err == nil {
		if f, err := os.Open(exe); err == nil {
			h := sha256.New()
			if _, err := io.Copy(h, f); err == nil {
				out += " sha256 " + hex.EncodeToString(h.Sum(nil))
			}
			f.Close()
		}
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" || s.Key == "vcs.modified" {
				out += " " + s.Key + "=" + s.Value
			}
		}
		out += " go=" + bi.GoVersion
	}
	return out
}

// bodyFlags are `decision body`'s flags.
type bodyFlags struct {
	Kind                        byte
	Cancel                      string
	Salt                        uint64
	Object                      byte
	Token, ISM, SeatChanges     string
	Domain                      uint32
	Router                      string
	Fee                         uint64
	Beneficiary                 string
	Proxy, Impl, Codehash, Call string
	Successor                   string
	Max                         uint64
	DayCap, Params              string
}

// decisionBodyCmd is `decision body`: the body (sequence 0) r/hlgov's builder dispatches for a kind, built from the
// kind's flags or raw parameters, checked by HlAdmin's own rules.
func decisionBodyCmd(f bodyFlags) error {
	p, err := buildParams(f)
	if err != nil {
		return err
	}
	text, err := describeParams(f.Kind, p)
	if err != nil {
		return fmt.Errorf("the parameters fail HlAdmin's checks for kind %d (it would spend the decision): %w", f.Kind, err)
	}
	b := decisionBody(f.Kind, 0, p)
	if len(b) > maxBody {
		return fmt.Errorf("the body is %d bytes, above the %d HlAdmin records", len(b), maxBody)
	}
	fmt.Printf("body: %x\n", b)
	fmt.Printf("(kind %d, sequence 0: %s — r/hlgov dispatches this body under its own number; a proposal's bytes are compared with these)\n", f.Kind, text)
	return nil
}

// buildParams writes a kind's parameters from its flags (or takes -params as given).
func buildParams(f bodyFlags) ([]byte, error) {
	if f.Params != "" {
		return hexBytes(f.Params)
	}
	tok := func() (uint8, error) {
		n, err := strconv.ParseUint(f.Token, 10, 8)
		if err != nil || n > 1 {
			return 0, errors.New("-token is 0 (GNOT) or 1 (BUBBLE)")
		}
		return uint8(n), nil
	}
	hash := func() ([32]byte, error) {
		h, err := hl.ParseKey32(strings.TrimPrefix(f.Codehash, "0x"))
		if err != nil || h == ([32]byte{}) {
			return h, errors.New("-codehash is 64 hex digits, never zero")
		}
		return h, nil
	}
	switch f.Kind {
	case kindCancel:
		salt := f.Salt
		if salt == 0 {
			salt = uint64(time.Now().UnixNano()) // random to the reader: no cancel's body is predictable (§13)
		}
		if from, to, ok := strings.Cut(f.Cancel, "-"); ok {
			a, err1 := strconv.ParseUint(strings.TrimSpace(from), 10, 64)
			b, err2 := strconv.ParseUint(strings.TrimSpace(to), 10, 64)
			if err := errors.Join(err1, err2); err != nil {
				return nil, fmt.Errorf("-cancel %q: %w", f.Cancel, err)
			}
			return cancelRangeParams(salt, a, b)
		}
		var ns []uint64
		for _, x := range strings.Split(f.Cancel, ",") {
			if strings.TrimSpace(x) == "" {
				continue
			}
			n, err := strconv.ParseUint(strings.TrimSpace(x), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("-cancel %q: %w", f.Cancel, err)
			}
			ns = append(ns, n)
		}
		return cancelListParams(salt, ns)
	case kindUnpause:
		if f.Object > 1 {
			return nil, errors.New("-object is 0 (the gate) or 1 (HlAdmin)")
		}
		return []byte{f.Object}, nil
	case kindTokenISM:
		t, err := tok()
		if err != nil {
			return nil, err
		}
		a := common.Address{}
		if f.ISM != "" && f.ISM != "0" {
			if a, err = ethx.ParseAddress(f.ISM); err != nil {
				return nil, fmt.Errorf("-ism: %w", err)
			}
		}
		return tokenISMParams(t, a), nil
	case kindGuardians:
		var pairs []seatKey
		for _, item := range strings.Split(f.SeatChanges, ",") {
			s, k, ok := strings.Cut(strings.TrimSpace(item), "=")
			if !ok {
				return nil, fmt.Errorf("-seat-changes: %q is not seat=ADDR", item)
			}
			n, err := strconv.ParseUint(s, 10, 8)
			if err != nil {
				return nil, err
			}
			a, err := ethx.ParseAddress(k)
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, seatKey{Seat: uint8(n), Key: a})
		}
		return guardiansParams(pairs)
	case kindRouter:
		t, err := tok()
		if err != nil {
			return nil, err
		}
		var r [32]byte
		if a, err := ethx.ParseAddress(f.Router); err == nil {
			copy(r[12:], a[:])
		} else if r, err = hl.ParseKey32(strings.TrimPrefix(f.Router, "0x")); err != nil {
			return nil, errors.New("-router is an address or 64 hex digits")
		}
		return routerParams(t, f.Domain, r), nil
	case kindFee:
		b, err := ethx.ParseAddress(f.Beneficiary)
		if err != nil {
			return nil, fmt.Errorf("-beneficiary: %w", err)
		}
		return feeParams(f.Fee, b), nil
	case kindUpgrade:
		pa, err1 := ethx.ParseAddress(f.Proxy)
		ia, err2 := ethx.ParseAddress(f.Impl)
		if err := errors.Join(err1, err2); err != nil {
			return nil, err
		}
		h, err := hash()
		if err != nil {
			return nil, err
		}
		c, err := hexBytes(f.Call)
		if err != nil {
			return nil, fmt.Errorf("-call: %w", err)
		}
		return upgradeParams(pa, ia, h, c)
	case kindSuccessor:
		s, err := ethx.ParseAddress(f.Successor)
		if err != nil {
			return nil, err
		}
		h, err := hash()
		if err != nil {
			return nil, err
		}
		return successorParams(s, h), nil
	case kindMintLimits:
		t, err := tok()
		if err != nil {
			return nil, err
		}
		dc, err := strconv.ParseUint(strings.ReplaceAll(f.DayCap, "_", ""), 10, 64)
		if err != nil || f.Max == 0 || dc < f.Max {
			return nil, errors.New("kind 12 needs -max at least 1 and -day-cap at least -max (base units)")
		}
		return mintLimitsParams(t, f.Max, dc), nil
	}
	return nil, fmt.Errorf("-kind %d: a kind of §13 with a builder (1, 2, 3, 5, 6, 7, 8, 9, 12), or -params HEX for any", f.Kind)
}

func hexBytes(s string) ([]byte, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	if s == "" {
		return nil, nil
	}
	b := make([]byte, len(s)/2)
	if len(s)%2 != 0 {
		return nil, errors.New("an odd number of hex digits")
	}
	for i := range b {
		v, err := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
		if err != nil {
			return nil, err
		}
		b[i] = byte(v)
	}
	return b, nil
}

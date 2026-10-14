// Command hlsol is the Solana deploy's tool (PLAN.md §5.8): the six program deployments and the setup step in
// its binding order, resumable; the audit a member repeats by hand; the test sends.
//
//	hlsol setup   -rpc URL -deployer KEY.json -keys DIR -so DIR -programs DIR [-factor 1|8] [-no-build]
//	              -gno-rpc URL -gno-domain N -local-domain N -mailbox-pkgpath PATH -hlgov PATH -gnot-router PATH -bubble-router PATH
//	              -guardians K1,K2,K3,K4 -watch K5,K6 -treasurer PUBKEY -expected-lock-gnot N -expected-lock-bubble N
//	              [-start-sequence N] [-trusted-height H] [-ata-fund N] [-authority-fund N]
//	              [-gnot-name .. -gnot-symbol .. -gnot-uri .. -bubble-name .. -bubble-symbol .. -bubble-uri ..] [-keep-metadata-authority]
//	hlsol audit   -rpc URL -keys DIR|-mailbox ID -gnot ID -bubble ID -client ID -pausable ID -admin ID [-so DIR] [-gno-rpc URL -mailbox-pkgpath PATH -hlgov PATH] [-deployer PUBKEY]
//	              [-expected-lock-gnot N -expected-lock-bubble N]   (the day cap printed beside the lock it was compiled from)
//	hlsol keygen  -seed TEXT -o FILE          a deterministic keypair for LOCAL runs only (the e2e's stable program ids)
//	hlsol send    -rpc URL -payer KEY.json -token ID -mailbox ID -gno-domain N -to g1… -amount N [-config bridge.json]
//	              (with -config, a bridge realm's own address as the recipient is refused: the realm would sweep the burn, K21)
//	hlsol balance -rpc URL -token ID -owner PUBKEY
//	hlsol close-stage -rpc URL -payer KEY.json -client ID -stage PDA      close a staged update or proof (its submitter at any time, anyone after 7 days)
//	hlsol flag    -rpc URL -key KEY.json -pausable ID -admin ID [-clear]  a seat's flag on the pausable ISM and the admin program, Solana alone (hlwatch flag is the one stroke with gno.land)
//	hlsol successor-init -rpc URL -deployer KEY.json -successor ID -predecessor ID -gno-domain N [-burn]
//	              a successor admin program's init (C227): zero seats and watch slots, its predecessor named — the live
//	              numbers, seats, record and hold history reach it at the transfer (kind 9) — then, with -burn, its upgrade
//	              authority burned (kind 9 refuses a mutable successor); the deployer is its upgrade authority until then
//
// Every flag that names a deployment is required: no default points at a shared deployment (§5.9). `setup` reads
// the chain before every step and repeats nothing already done; its done condition is the audit's "no human key
// holds an authority"; it refuses a trusted header older than an hour unless -allow-old-header; it compiles the
// pausable ISM's launch day cap from the locks the launch notice expects (F10, §5.4: the smaller of 100,000 and
// a tenth of the lock — one constant for both tokens as built, so the smaller of the two tokens' values); it
// starts the admin program's sequence at -start-sequence (AT3: 0 for a chain's first deployment, r/hlgov's
// Last() for a fresh deployment under the same r/hlgov, §6.4's exit), which must equal r/hlgov's counter read
// from -gno-rpc when the realm is on the chain — a mismatch refuses before anything is built or deployed.
// `audit` prints every RPC call it makes before the answer, so a member can repeat each, and FAILS unless each
// deployed ELF carries the ids it was compiled with (G20) and r/hlgov's counter is the admin program's (K15;
// the admin program's number below r/hlgov's is AT3's ungovernable deployment).
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// programNames are the six deployments' key files in -keys, in deploy order.
var programNames = []string{"mailbox", "gnot", "bubble", "client", "pausable", "admin"}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hlsol setup|audit|keygen|send|balance|close-stage|flag|successor-init [flags]")
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	rpcURL := fs.String("rpc", "", "Solana RPC (required)")
	deployer := fs.String("deployer", "", "setup: the deployer keypair file; audit: its pubkey, for the balance")
	keysDir := fs.String("keys", "", "the six program keypairs (mailbox, gnot, bubble, client, pausable, admin .json)")
	soDir := fs.String("so", "", "the program files: mailbox.so, token.so, and our three builds")
	programsDir := fs.String("programs", "", "setup: bridge/programs, whose config.rs files take the launch values before the build")
	factor := fs.Int("factor", 1, "setup: the compiled clock factor (1 mainnet, 8 devnet and the local run)")
	noBuild := fs.Bool("no-build", false, "setup: use the .so files as they are")
	solanaBin := fs.String("solana-bin", "", "the Solana CLI's bin directory (solana, cargo-build-sbf); $PATH when empty")
	gnoRPC := fs.String("gno-rpc", "", "the gno node: the trusted header (setup), the validators and hlmailbox's seats (audit)")
	gnoChain := fs.String("gno-chain-id", "", "setup: the gno chain id the client follows (read from the node when empty)")
	gnoDomain := fs.Uint("gno-domain", 0, "gno.land's domain (the client's origin, the routers' domain)")
	localDomain := fs.Uint("local-domain", 0, "setup: the mailbox's domain, Solana's own")
	mailboxPkg := fs.String("mailbox-pkgpath", "", "r/hlmailbox's pkgpath (the Dispatch events' pkg_path, compiled into the client)")
	hlgov := fs.String("hlgov", "", "setup: r/hlgov's pkgpath or g1… address (the admin program's accepted sender)")
	gnotRouter := fs.String("gnot-router", "", "setup: r/hlgnot's pkgpath or g1… address (the GNOT route's remote router)")
	bubbleRouter := fs.String("bubble-router", "", "setup: r/hlbubble's pkgpath or g1… address")
	guardians := fs.String("guardians", "", "setup: the four guardians' Solana pubkeys, seats 1–4")
	watch := fs.String("watch", "", "setup: the two watch keys' Solana pubkeys, slots 5–6")
	treasurer := fs.String("treasurer", "", "setup: the treasurer's Solana key, the mailbox's fee beneficiary")
	trustedHeight := fs.Int64("trusted-height", 0, "setup: the client's trusted header (0: the latest non-announcing header)")
	allowOld := fs.Bool("allow-old-header", false, "setup: take a trusted header older than an hour (§6.1 refuses one; never for a deployment)")
	stagePDA := fs.String("stage", "", "close-stage: the stage account")
	keyFile := fs.String("key", "", "flag: the seat's Solana key file")
	clear := fs.Bool("clear", false, "flag: clear the flag instead of setting it")
	ataFund := fs.Uint64("ata-fund", 250_000_000, "setup: lamports to each token's ATA payer (§5.8: 0.25 SOL at launch)")
	deploySend := fs.String("deploy-send", "rpc", "setup: the program deploys' write transactions: rpc (through -rpc) or tpu (straight to the leaders)")
	maxSignAttempts := fs.Int("max-sign-attempts", 0, "setup: the deploys' re-sign attempts after a blockhash expires (0: the CLI's own)")
	cuPrice := fs.Uint64("cu-price", 0, "setup: the deploys' priority fee, micro-lamports a compute unit (0: none)")
	authorityFund := fs.Uint64("authority-fund", 10_000_000, "setup/successor-init: lamports to the admin program's authority PDA, or a successor's (M2-NOTES 17b; S-25: the hand-over sweeps the predecessor's float to the successor's PDA too)")
	decimals := fs.Uint("decimals", 6, "setup: the tokens' decimals, here and in messages")
	gnotName := fs.String("gnot-name", "Gno", "setup: token metadata")
	gnotSymbol := fs.String("gnot-symbol", "GNOT", "")
	gnotURI := fs.String("gnot-uri", "", "")
	bubbleName := fs.String("bubble-name", "Bubble", "")
	bubbleSymbol := fs.String("bubble-symbol", "BUBBLE", "")
	bubbleURI := fs.String("bubble-uri", "", "")
	keepMeta := fs.Bool("keep-metadata-authority", false, "setup: keep the deployer as the metadata update and pointer authority (§11.5 not yet final)")
	mailboxID := fs.String("mailbox", "", "audit/send: the mailbox program id (or -keys)")
	gnotID := fs.String("gnot", "", "audit: the GNOT token program (or -keys)")
	bubbleID := fs.String("bubble", "", "audit: the BUBBLE token program (or -keys)")
	clientID := fs.String("client", "", "audit: the light-client ISM (or -keys)")
	pausableID := fs.String("pausable", "", "audit: the pausable ISM (or -keys)")
	adminID := fs.String("admin", "", "audit: the admin program (or -keys)")
	payerFile := fs.String("payer", "", "send: the payer keypair file")
	tokenID := fs.String("token", "", "send/balance: a token program")
	to := fs.String("to", "", "send: the g1… recipient on gno.land")
	amount := fs.Uint64("amount", 0, "send: base units")
	owner := fs.String("owner", "", "balance: the wallet")
	seed := fs.String("seed", "", "keygen: the text the key is derived from")
	out := fs.String("o", "", "keygen: the file to write")
	successorID := fs.String("successor", "", "successor-init: the successor admin program's id (deployed under the deployer's upgrade authority)")
	predecessorID := fs.String("predecessor", "", "successor-init: the admin program it succeeds (whose kind 9 will hand over to it)")
	burn := fs.Bool("burn", false, "successor-init: burn the successor's upgrade authority after its init (kind 9 refuses a mutable successor)")
	expectedLockGNOT := fs.Uint64("expected-lock-gnot", 0, "setup/audit: the GNOT lock the launch notice expects, base units (F10: the day cap is the smaller of 100,000 and a tenth of it; the audit prints the cap beside it)")
	expectedLockBUBBLE := fs.Uint64("expected-lock-bubble", 0, "setup/audit: the BUBBLE lock the launch notice expects, base units")
	launchMaximum := fs.Uint64("launch-maximum", 0, "setup/audit: the per-message maximum the route realms compile (launchMaximum), base units; 0 is their source's 25,000 — the setup refuses an expected lock whose tenth compiles the pausable ISM's maximum below it unless this says the realms were lowered to match (AR4)")
	startSequence := fs.Uint64("start-sequence", 0, "setup: the number the admin program's sequence starts at — 0 for a chain's first deployment; r/hlgov's Last() for a fresh deployment under the same r/hlgov (AT3, §6.4): checked against the realm on -gno-rpc before the first deploy, a mismatch refuses")
	configPath := fs.String("config", "", "send: the deployment's bridge.json, whose realms' addresses are refused as recipients (K21)")
	_ = fs.Parse(args)

	log.SetOutput(agent.RedactWriter{W: os.Stderr}) // no provider URL's key in a log line: check() prints solana-go's errors, URL included (G25)
	ctx := context.Background()
	switch cmd {
	case "keygen":
		if *seed == "" || *out == "" {
			log.Fatal("hlsol keygen: -seed and -o are needed (LOCAL runs only: a key anyone can derive)")
		}
		check(writeKeypair(*out, *seed))
		k, _ := sol.PrivateKeyFromFile(*out)
		fmt.Println(k.PublicKey())
		return
	}
	if *rpcURL == "" {
		log.Fatal("hlsol: -rpc is required (no default points at a shared deployment, §5.9)")
	}
	switch cmd {
	case "setup":
		need := map[string]string{"-deployer": *deployer, "-keys": *keysDir, "-so": *soDir, "-gno-rpc": *gnoRPC, "-mailbox-pkgpath": *mailboxPkg, "-hlgov": *hlgov,
			"-gnot-router": *gnotRouter, "-bubble-router": *bubbleRouter, "-guardians": *guardians, "-watch": *watch, "-treasurer": *treasurer}
		for k, v := range need {
			if v == "" {
				log.Fatalf("hlsol setup: %s is required", k)
			}
		}
		if *gnoDomain == 0 || *localDomain == 0 {
			log.Fatal("hlsol setup: -gno-domain and -local-domain are required")
		}
		if !*noBuild && *programsDir == "" {
			log.Fatal("hlsol setup: -programs (bridge/programs) is required unless -no-build")
		}
		if *expectedLockGNOT == 0 || *expectedLockBUBBLE == 0 {
			log.Fatal("hlsol setup: -expected-lock-gnot and -expected-lock-bubble (base units) are required: the pausable ISM's launch day cap is compiled from them (F10, §5.4), and under -no-build the .so is checked against them before the first deploy (G51)")
		}
		dk, err := sol.PrivateKeyFromFile(*deployer)
		check(err)
		keys, err := loadProgramKeys(*keysDir)
		check(err)
		s := &setup{
			ctx: ctx, c: sol.NewClient(*rpcURL, dk), rpcURL: *rpcURL, deployer: dk, deployerPath: *deployer, keys: keys, soDir: *soDir, programsDir: *programsDir, factor: *factor, noBuild: *noBuild,
			solanaBin: *solanaBin, gnoRPC: *gnoRPC, gnoChain: *gnoChain, gnoDomain: uint32(*gnoDomain), localDomain: uint32(*localDomain), mailboxPkg: *mailboxPkg,
			hlgov: gnoKey32(*hlgov), routers: [2][32]byte{gnoKey32(*gnotRouter), gnoKey32(*bubbleRouter)}, treasurer: solana.MustPublicKeyFromBase58(*treasurer),
			trustedHeight: *trustedHeight, allowOld: *allowOld, ataFund: *ataFund, authorityFund: *authorityFund, decimals: uint8(*decimals), keepMeta: *keepMeta,
			names: [2][3]string{{*gnotName, *gnotSymbol, *gnotURI}, {*bubbleName, *bubbleSymbol, *bubbleURI}}, expectedLock: [2]uint64{*expectedLockGNOT, *expectedLockBUBBLE}, launchMaximum: *launchMaximum,
			startSequence: *startSequence, hlgovPath: *hlgov,
			deploySend: *deploySend, maxSignAttempts: *maxSignAttempts, cuPrice: *cuPrice,
		}
		if *deploySend != "rpc" && *deploySend != "tpu" {
			log.Fatalf("hlsol setup: -deploy-send is rpc or tpu, not %q", *deploySend)
		}
		s.seats, err = parseKeys(*guardians, sol.Seats)
		check(err)
		s.watch, err = parseKeys(*watch, sol.Watch)
		check(err)
		check(s.run())
		a := &audit{ctx: ctx, rpcURL: *rpcURL, programs: keys.programs(), soDir: *soDir, gnoRPC: *gnoRPC, mailboxPkg: *mailboxPkg, hlgov: *hlgov, deployer: dk.PublicKey().String(), quiet: true, commitment: rpc.CommitmentConfirmed, expectedLock: s.expectedLock, launchMaximum: s.launchMaximum}
		ok, err := a.run()
		check(err)
		if !ok {
			log.Fatal("hlsol setup: the audit finds a human key holding an authority: NOT DONE")
		}
		fmt.Println("hlsol setup: DONE — no human key holds an authority (the audit passed)")
	case "audit":
		var progs sol.Programs
		if *keysDir != "" {
			keys, err := loadProgramKeys(*keysDir)
			check(err)
			progs = keys.programs()
		} else {
			for _, p := range []struct {
				name string
				id   *string
				dst  *solana.PublicKey
			}{{"-mailbox", mailboxID, &progs.Mailbox}, {"-gnot", gnotID, &progs.Tokens[0]}, {"-bubble", bubbleID, &progs.Tokens[1]}, {"-client", clientID, &progs.Client}, {"-pausable", pausableID, &progs.Pausable}, {"-admin", adminID, &progs.Admin}} {
				if *p.id == "" {
					log.Fatalf("hlsol audit: %s is required (or -keys)", p.name)
				}
				*p.dst = solana.MustPublicKeyFromBase58(*p.id)
			}
		}
		a := &audit{ctx: ctx, rpcURL: *rpcURL, programs: progs, soDir: *soDir, gnoRPC: *gnoRPC, mailboxPkg: *mailboxPkg, hlgov: *hlgov, deployer: *deployer, commitment: rpc.CommitmentFinalized, expectedLock: [2]uint64{*expectedLockGNOT, *expectedLockBUBBLE}, launchMaximum: *launchMaximum}
		ok, err := a.run()
		check(err)
		if !ok {
			os.Exit(1)
		}
	case "send":
		if *mailboxID == "" || *tokenID == "" || *payerFile == "" {
			log.Fatal("hlsol send: -mailbox, -token and -payer are required")
		}
		if *configPath != "" {
			cfg, err := agent.LoadConfig(*configPath)
			check(err)
			// every route the mailbox registers — a successor the config does not name too (M1i, R39: the realm
			// sweeps a burn to any registered route's address) — and every route it dropped (AT6: a re-pointed-away
			// successor's address is a realm's still, swept by the line's last realm), read from the config's gno
			// node when it answers
			var registered []string
			if cfg.Gno.RPC != "" && cfg.Gno.Mailbox != "" {
				if mb, err := agent.ReadMailbox(ctx, cfg.SourceClient(cfg.Gno.RPC), cfg.Gno.Mailbox, 0); err == nil {
					registered = mailboxRealms(mb)
				} else {
					log.Printf("hlsol send: the mailbox's routes could not be read (%v): the config's realms alone are refused as recipients", err)
				}
			}
			if realm := realmOf(cfg, *to, registered...); realm != "" {
				log.Fatalf("hlsol send: %s is the address of %s, a bridge realm: a burn to it is swept on gno.land and paid only by a GovDAO proposal (§5.2, K21); name a wallet", *to, realm)
			}
		}
		payer, err := sol.PrivateKeyFromFile(*payerFile)
		check(err)
		c := sol.NewClient(*rpcURL, payer)
		token := sol.Synthetic{Program: solana.MustPublicKeyFromBase58(*tokenID)}
		mailbox := sol.Mailbox{Program: solana.MustPublicKeyFromBase58(*mailboxID)}
		rcpt, err := hl.GnoAddressToBytes32(*to)
		check(err)
		unique := solana.NewWallet().PrivateKey
		sig, err := c.Send(ctx, []solana.Instruction{
			sol.ComputeLimitIx(400_000),
			token.TransferRemoteIx(mailbox, payer.PublicKey(), unique.PublicKey(), uint32(*gnoDomain), rcpt, *amount),
		}, unique)
		check(err)
		fmt.Println("sent", *amount, "to", *to, "tx", sig, "message account", mailbox.DispatchedMessage(unique.PublicKey()))
	case "balance":
		if *tokenID == "" || *owner == "" {
			log.Fatal("hlsol balance: -token and -owner are required")
		}
		c := sol.NewClient(*rpcURL, nil)
		token := sol.Synthetic{Program: solana.MustPublicKeyFromBase58(*tokenID)}
		w := solana.MustPublicKeyFromBase58(*owner)
		fmt.Println(tokenBalance(ctx, c, sol.ATA(w, token.Mint())))
	case "close-stage":
		if *payerFile == "" || *clientID == "" || *stagePDA == "" {
			log.Fatal("hlsol close-stage: -payer, -client and -stage are required")
		}
		payer, err := sol.PrivateKeyFromFile(*payerFile)
		check(err)
		c := sol.NewClient(*rpcURL, payer)
		client := sol.LightClient{Program: solana.MustPublicKeyFromBase58(*clientID)}
		stage := solana.MustPublicKeyFromBase58(*stagePDA)
		info, err := c.RPC.GetAccountInfoWithOpts(ctx, stage, &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed})
		check(err)
		if info.Value == nil || !info.Value.Owner.Equals(client.Program) {
			log.Fatalf("hlsol close-stage: %s is not a stage of the client %s", stage, client.Program)
		}
		st, err := sol.ParseStage(info.Value.Data.GetBinary())
		check(err)
		fmt.Printf("stage %s: kind %d, submitter %s, key %x, created at slot %d (%s), %d lamports\n", stage, st.Kind, st.Submitter, st.Key[:8], st.CreatedSlot, time.Unix(st.CreatedTime, 0).UTC().Format(time.RFC3339), info.Value.Lamports)
		sig, err := c.Send(ctx, []solana.Instruction{client.StageCloseIx(stage, payer.PublicKey())})
		check(err)
		fmt.Println("closed, the rent to", payer.PublicKey(), "tx", sig)
	case "flag":
		if *keyFile == "" || *pausableID == "" || *adminID == "" {
			log.Fatal("hlsol flag: -key, -pausable and -admin are required")
		}
		k, err := sol.PrivateKeyFromFile(*keyFile)
		check(err)
		progs := sol.Programs{Pausable: solana.MustPublicKeyFromBase58(*pausableID), Admin: solana.MustPublicKeyFromBase58(*adminID)}
		c := sol.NewClient(*rpcURL, k)
		sig, err := agent.FlagSolana(ctx, c, progs, !*clear)
		what := "set"
		if *clear {
			what = "cleared"
		}
		if agent.LineUnreadAfterStroke(sig, err) {
			// the pausable half landed (G40): the line's failure is a warning beside the signature
			fmt.Printf("the flag of %s %s on the pausable ISM and the configured admin program: %s\n", k.PublicKey(), what, sig)
			log.Fatalf("WARNING: %v", err)
		}
		check(err)
		fmt.Printf("the flag of %s %s on the pausable ISM and the admin program: %s\n", k.PublicKey(), what, sig)
	case "successor-init":
		if *deployer == "" || *successorID == "" || *predecessorID == "" || *gnoDomain == 0 {
			log.Fatal("hlsol successor-init: -deployer, -successor, -predecessor and -gno-domain are required")
		}
		dk, err := sol.PrivateKeyFromFile(*deployer)
		check(err)
		c := sol.NewClient(*rpcURL, dk)
		succ := sol.Admin{Program: solana.MustPublicKeyFromBase58(*successorID)}
		pred := solana.MustPublicKeyFromBase58(*predecessorID)
		me := dk.PublicKey()
		info, err := c.RPC.GetAccountInfoWithOpts(ctx, succ.State(), &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed})
		if err == nil && info.Value != nil && len(info.Value.Data.GetBinary()) > 0 {
			st, err := sol.ParseAdminState(info.Value.Data.GetBinary())
			check(err)
			if !st.Predecessor.Equals(pred) {
				log.Fatalf("hlsol successor-init: %s is initialised already with predecessor %s, not %s", succ.Program, st.Predecessor, pred)
			}
			fmt.Printf("successor %s: initialised already (predecessor %s, inherited %d)\n", succ.Program, st.Predecessor, st.Inherited)
		} else {
			sig, err := c.Send(ctx, []solana.Instruction{succ.SuccessorInitIx(me, me, uint32(*gnoDomain), pred)})
			check(err)
			fmt.Printf("successor %s initialised with zero seats and predecessor %s (origin %d): %s\n", succ.Program, pred, *gnoDomain, sig)
		}
		// the successor's authority PDA holds the realloc float as the launch one does (M2-NOTES 17b; S-25, M2i:
		// the hand-over sweeps the predecessor's float to it too, but a predecessor spent down leaves nothing to
		// sweep, and a kind 6 on the successor would then die in Hyperlane's realloc top-up)
		pda := succ.Authority()
		bal, err := c.RPC.GetBalance(ctx, pda, rpc.CommitmentConfirmed)
		check(err)
		if bal.Value < *authorityFund {
			sig, err := c.Send(ctx, []solana.Instruction{sol.TransferIx(me, pda, *authorityFund-bal.Value)})
			check(err)
			fmt.Printf("its authority PDA %s funded with %d lamports (the realloc float, 17b; it holds %d): %s\n", pda, *authorityFund-bal.Value, *authorityFund, sig)
		} else {
			fmt.Printf("its authority PDA %s holds %d lamports (the realloc float, 17b)\n", pda, bal.Value)
		}
		if !*burn {
			fmt.Println("its upgrade authority is still the deployer's: burn it (-burn) before the predecessor's kind 9, which refuses a mutable successor")
			return
		}
		pd, err := c.RPC.GetAccountInfoWithOpts(ctx, sol.ProgramData(succ.Program), &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed})
		check(err)
		_, auth, _, err := sol.ParseProgramData(pd.Value.Data.GetBinary())
		check(err)
		switch {
		case auth == nil:
			fmt.Println("its upgrade authority is burned already")
		case auth.Equals(me):
			sig, err := c.Send(ctx, []solana.Instruction{sol.SetAuthorityIx(sol.ProgramData(succ.Program), me, nil)})
			check(err)
			fmt.Printf("its upgrade authority burned (immutable, as kind 9 requires): %s\n", sig)
		default:
			log.Fatalf("hlsol successor-init: the successor's upgrade authority is %s, not the deployer", auth)
		}
	default:
		log.Fatalf("unknown command %s", cmd)
	}
}

// programKeys are the six program keypairs.
type programKeys struct {
	files map[string]string
	keys  map[string]solana.PrivateKey
}

func loadProgramKeys(dir string) (*programKeys, error) {
	pk := &programKeys{files: map[string]string{}, keys: map[string]solana.PrivateKey{}}
	for _, n := range programNames {
		f := filepath.Join(dir, n+".json")
		k, err := sol.PrivateKeyFromFile(f)
		if err != nil {
			return nil, fmt.Errorf("the %s program's keypair: %w", n, err)
		}
		pk.files[n], pk.keys[n] = f, k
	}
	return pk, nil
}

func (p *programKeys) id(name string) solana.PublicKey { return p.keys[name].PublicKey() }

func (p *programKeys) programs() sol.Programs {
	return sol.Programs{Mailbox: p.id("mailbox"), Tokens: [2]solana.PublicKey{p.id("gnot"), p.id("bubble")}, Client: p.id("client"), Pausable: p.id("pausable"), Admin: p.id("admin")}
}

// realmOf is the bridge realm a g1… address belongs to in a deployment's config — the mailbox, the fee realm,
// r/hlgov, the routes — or "" for a wallet (K21: the client refuses a realm address as a recipient). The compare
// ignores case (G27: bech32 reads an upper-cased address as the same one, and the realm would sweep the burn).
func realmOf(cfg *agent.Config, addr string, registered ...string) string {
	realms := []string{cfg.Gno.Mailbox, cfg.Gno.Fees, cfg.Gno.Gov}
	for _, r := range cfg.Routes {
		realms = append(realms, r.Gno)
	}
	realms = append(realms, registered...) // every route the mailbox registers, successors included (M1i, R39)
	for _, p := range realms {
		if p != "" && strings.EqualFold(hl.GnoPackageAddress(p), addr) {
			return p
		}
	}
	return ""
}

// mailboxRealms are the route realms the mailbox knows beyond the config: every registered route (M1i, R39) and
// every dropped one (AT6: hlmailbox's droppedRoutes, a re-pointed-away successor whose address a burn may still
// name — the line's last realm sweeps it, so the client refuses it as a recipient like any realm's).
func mailboxRealms(mb agent.MailboxView) []string {
	out := append([]string(nil), mb.RoutePaths...)
	var dropped []string
	for p := range mb.DroppedRoutes {
		dropped = append(dropped, p)
	}
	sort.Strings(dropped)
	return append(out, dropped...)
}

// gnoKey32 is a gno realm as the programs carry it: its pkgpath's address, or a g1… address, as 32 bytes.
func gnoKey32(s string) [32]byte {
	if strings.HasPrefix(s, "gno.land/") {
		s = hl.GnoPackageAddress(s)
	}
	k, err := hl.GnoAddressToBytes32(s)
	check(err)
	return k
}

func parseKeys(s string, n int) ([]solana.PublicKey, error) {
	var out []solana.PublicKey
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		k, err := solana.PublicKeyFromBase58(p)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", p, err)
		}
		out = append(out, k)
	}
	if len(out) != n {
		return nil, fmt.Errorf("%d keys in %q, want %d", len(out), s, n)
	}
	return out, nil
}

// writeKeypair derives an ed25519 keypair from a seed text and writes it in the CLI's file form (64 bytes as a
// JSON array). For local runs only: anyone can derive it.
func writeKeypair(path, seed string) error {
	s := sha256.Sum256([]byte("hlsol keygen (local runs only): " + seed))
	priv := ed25519.NewKeyFromSeed(s[:])
	var raw [ed25519.PrivateKeySize]byte // solana-keygen's file: a JSON array of the 64 bytes (a slice would marshal as base64)
	copy(raw[:], priv)
	b, _ := json.Marshal(raw)
	return os.WriteFile(path, b, 0o600)
}

func tokenBalance(ctx context.Context, c *sol.Client, ata solana.PublicKey) uint64 {
	info, err := c.RPC.GetAccountInfoWithOpts(ctx, ata, &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed})
	if err != nil {
		return 0
	}
	d := info.Value.Data.GetBinary()
	if len(d) < 72 {
		return 0
	}
	return binary.LittleEndian.Uint64(d[64:72]) // token account: mint 32, owner 32, amount u64
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

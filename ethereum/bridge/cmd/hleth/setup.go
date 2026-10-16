package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// The Ethereum deploy and the setup step (PLAN.md §5.8), in its binding order — lane E-M2b's
// contracts/script/Setup.s.sol is the same order as a Foundry script — each step reading the chain first and
// skipped when it is done, so a stopped setup resumes and a finished one sends nothing:
//
//  1. the ProxyAdmin, and in the deployer's next transaction its ownership handed to HlAdmin's address-to-be — the
//     CREATE address of the deployer's nonce the plan gives HlAdmin — before any proxy exists, so the deployer never
//     holds upgrade power (the code audit's E2-CT1; HlAdmin's constructor refuses to be built without it);
//  2. the Mailbox (localDomain = Ethereum's domain) and its proxy, initialized in the proxy's own constructor call
//     with the deployer as owner and the ProxyAdmin standing in for the default ISM and both hooks until step 9;
//  3. the MerkleTreeHook over the mailbox; the ProtocolFee (its immutable maximum, fee 0, the treasurer's address
//     as beneficiary, the deployer as owner);
//  4. the Ed25519Verifier (no storage, no owner, no arguments: this tree's build, whose EXTCODEHASH HlAdmin pins), then
//     the GnoLightClient (the hlmailbox path's keccak256 and the verifier's address compiled in) and its proxy,
//     initialized with the chain id, gno.land's domain, the trusted header and its validator set, the deployer as
//     admin;
//  5. two HypERC20 (decimals 6, scale 1/1, the mailbox) and their proxies, initialized with supply 0, the name and
//     the symbol, hook 0, ISM 0, the deployer as owner;
//  6. the PausableGate and its proxy, initialized with the mailbox, the client, the tokens, the seats and watch
//     slots, the launch mint limits, the deployer as owner;
//  7. HlAdmin (plain code: immutable from its deployment) over its Config in the struct's order — the contracts, the
//     verifier and its code hash (this build's), Ethereum's domain, gno.land's, r/hlgov's and the routes' 32-byte
//     addresses, the hlmailbox path's hash, the seats and watch slots, the gate's launch mint limits, the start
//     sequence, no predecessor — whose constructor creates HlAdminExec (recorded from HlAdmin.exec());
//  8. each token's ISM → the gate, each token's router for gno.land's domain → its route realm;
//  9. the mailbox's default ISM → the gate, default hook → the MerkleTreeHook, required hook → the ProtocolFee;
//  10. ownership to HlAdmin in this order: the MerkleTreeHook, the ProtocolFee, both tokens, the gate, the mailbox;
//     the client's admin — after which the deployer holds nothing (the ProxyAdmin is HlAdmin's since step 1);
//  11. HlAdmin.finishSetup(), permissionless and self-checking (checks 1–31 and 34 at a launch — 32–33, that nothing
//     was ever dispatched, retired by fold F5: anyone may dispatch after step 9 — and it records the mailbox's nonce as
//     the launch line, launchNonce(): no message below it is a burn of this deployment).
//
// Every proxy is initialized in its own constructor call, so no initializer is ever open to a front-runner. Steps 1–7
// follow the nonce plan (M2-NOTES.md, Fold F4): from the deployer's nonce n, n the ProxyAdmin, n+1 its hand-over,
// n+2–3 the mailbox and its proxy, n+4 the MerkleTreeHook, n+5 the ProtocolFee, n+6 the Ed25519Verifier, n+7–8 the
// client and its proxy, n+9–12 the tokens, n+13–14 the gate, n+15 HlAdmin (HlAdminExec is HlAdmin's CREATE at its
// nonce 1). Each of those transactions is simulated before it is sent (ethx's estimate: one that would revert is
// refused, its nonce untouched — a reverted transaction still takes its nonce and would move HlAdmin off the address
// the ProxyAdmin went to), sent only at its planned nonce, and the setup stops, sending nothing, when the deployer's
// nonce leaves the plan. Every transaction — the creations too — goes through ethx.Sender.

const (
	// launchMaximumDefault is the per-message maximum the route realms compile (hlgnot's and hlbubble's
	// `launchMaximum`, 25,000 of each token, base units) and the gate's ceiling for its own (§5.4).
	launchMaximumDefault = 25_000_000_000
	// launchDayCapMax is the gate's launch day cap's ceiling (§5.4: 100,000 of a token, base units).
	launchDayCapMax = 100_000_000_000
	// defaultMaxPerMessage and defaultDayCap are a local chain's limits when no lock is expected (§5.4's ceilings).
	defaultMaxPerMessage = launchMaximumDefault
	defaultDayCap        = launchDayCapMax
	// localChainID is anvil's (and the e2e's) chain: the one chain the setup takes without an expected lock.
	localChainID = 31337
	// maxStartSequence is the highest start HlAdmin's constructor takes at a launch (its MAX_START, 2^32; audit round
	// 3, E3-GS4): r/hlgov's `sync sequence` raises its counter by at most 2^32 a proposal, so a start above it is out of
	// one proposal's reach (2^64−1 ignores every decision for good), and the constructor refuses it OutOfOrder — at
	// step 7, after six steps' gas, unless the setup refuses it first.
	maxStartSequence = 1 << 32
)

// launchLimits are a token's launch limits on the gate from the lock the launch notice expects of it (§5.4, the
// Solana tool's F10, E3-CF4): the day cap the smaller of 100,000 and a tenth of the lock — a forged day's mint before
// the flags is bounded by a tenth of the lock — and the per-message maximum the smaller of 25,000 and the day cap (a
// maximum above the day cap is a send the gate can never fit). Each token's own: the gate keeps one pair a token.
func launchLimits(lock uint64) (maxPerMessage, dayCap uint64) {
	dayCap = min(uint64(launchDayCapMax), lock/10)
	return min(uint64(launchMaximumDefault), dayCap), dayCap
}

// launchMaximumCheck refuses a launch maximum below the realms' compiled one (AR4, §5.4: the gate's maximum is never
// below gno.land's): gno.land would take a send between the two that the gate parks OverMax until a kind 12.
func launchMaximumCheck(token int, maxPerMessage, realms uint64) error {
	if maxPerMessage < realms {
		return fmt.Errorf("hleth setup: token %d's expected lock compiles a gate maximum of %d base units a message (the smaller of 25,000 and its day cap), below the route realm's compiled maximum %d: gno.land would take a send between %d and %d that the gate refuses OverMax for good (AR4) — lower the route realm's launchMaximum to %d and pass -launch-maximum %d, or expect a larger lock (a tenth of it at or above %d)",
			token, maxPerMessage, realms, maxPerMessage, realms, maxPerMessage, maxPerMessage, realms)
	}
	return nil
}

// defaultMaxProtocolFee is ProtocolFee's immutable maximum at launch: 0.001 ETH (§1, the red team's ER11).
var defaultMaxProtocolFee = big.NewInt(1_000_000_000_000_000)

type setupConfig struct {
	RPC            string
	DeployerKey    string
	ContractsOut   string
	HyperlaneDir   string
	Out            string
	LocalDomain    uint32 // 0: the chain id
	GnoDomain      uint32
	GnoRPC         string
	GnoChainID     string
	MailboxPkgPath string
	Hlgov          string
	GnotRoute      string
	BubbleRoute    string
	Seats          [4]common.Address
	Watch          [2]common.Address
	Beneficiary    common.Address
	// ExpectedLock is the lock the launch notice expects of each token (base units; 0 GNOT, 1 BUBBLE): each token's
	// launch limits are compiled from it (launchLimits, E3-CF4) — required on every chain but a local one.
	ExpectedLock [2]uint64
	// LaunchMaximum is the per-message maximum the route realms compile (base units; 0: their source's 25,000).
	LaunchMaximum uint64
	// MaxPerMessage and DayCap: given, each must be what the expected lock compiles; on a local chain with no lock
	// expected, §5.4's ceilings when not given.
	MaxPerMessage  [2]uint64
	DayCap         [2]uint64
	StartSequence  uint64
	TrustedHeight  int64
	TrustedFile    string
	ConfigOut      string // the machines' config's deployment part, written once the setup is done (E2-RT6)
	MaxProtocolFee *big.Int
	Name0, Symbol0 string
	Name1, Symbol1 string
	Send           ethx.SendConfig
}

type setup struct {
	cfg  setupConfig
	out  io.Writer
	arts Artifacts
	cn   *conn
	tx   *txer
	d    *Deployment
	me   common.Address
}

func (s *setup) say(format string, a ...any) {
	fmt.Fprintf(s.out, "setup: "+format+"\n", a...)
}

func (s *setup) save() error { return s.d.Save(s.cfg.Out) }

// runSetup performs the setup to its done condition and answers how many transactions it sent.
func runSetup(ctx context.Context, cfg setupConfig, out io.Writer) (int, error) {
	s := &setup{cfg: cfg, out: out}
	if err := s.prepare(ctx); err != nil {
		return 0, err
	}
	if err := s.run(ctx); err != nil {
		return s.tx.sent, err
	}
	if s.tx.sent == 0 {
		fmt.Fprintln(out, "hleth setup: DONE (sent nothing: every step was done already)")
	} else {
		fmt.Fprintf(out, "hleth setup: DONE (sent %d transactions)\n", s.tx.sent)
	}
	return s.tx.sent, nil
}

func (s *setup) prepare(ctx context.Context) error {
	c := &s.cfg
	if c.Out == "" || c.ContractsOut == "" || c.HyperlaneDir == "" || c.DeployerKey == "" {
		return errors.New("hleth setup: -out, -contracts, -hyperlane and -deployer are required")
	}
	if c.GnoDomain == 0 || c.MailboxPkgPath == "" || c.Hlgov == "" || c.GnotRoute == "" || c.BubbleRoute == "" {
		return errors.New("hleth setup: -gno-domain, -mailbox-pkgpath, -hlgov, -gnot-route and -bubble-route are required (the deployment's compiled bindings: no default points at another deployment)")
	}
	for i, a := range c.Seats {
		if a == (common.Address{}) {
			return fmt.Errorf("hleth setup: -seats needs four guardian addresses (seat %d is missing)", i+1)
		}
	}
	for i, a := range c.Watch {
		if a == (common.Address{}) {
			return fmt.Errorf("hleth setup: -watch needs two watch keys' addresses (slot %d is missing)", i+5)
		}
	}
	if c.Beneficiary == (common.Address{}) {
		return errors.New("hleth setup: -beneficiary (the treasurer's Ethereum address, the protocol fee's beneficiary) is required")
	}
	if c.StartSequence > maxStartSequence {
		return fmt.Errorf("hleth setup: -start-sequence %d is above 2^32 (%d): HlAdmin's constructor refuses a launch start that r/hlgov's sync sequence cannot reach in one proposal (OutOfOrder at step 7, after six steps' gas; E3-GS4) — nothing is sent", c.StartSequence, uint64(maxStartSequence))
	}
	if c.GnoRPC == "" && c.TrustedFile == "" {
		return errors.New("hleth setup: -gno-rpc (the trusted header from the node) or -trusted-file (an offline header) is required")
	}
	if c.MaxProtocolFee == nil {
		c.MaxProtocolFee = new(big.Int).Set(defaultMaxProtocolFee)
	}
	var err error
	if s.arts, err = LoadArtifacts(c.ContractsOut, c.HyperlaneDir); err != nil {
		return err
	}
	if s.d, err = LoadDeployment(c.Out, true); err != nil {
		return err
	}
	key, err := ethx.LoadKey(c.DeployerKey)
	if err != nil {
		return err
	}
	if s.cn, err = dial(ctx, c.RPC, nil); err != nil {
		return err
	}
	if err := s.limits(); err != nil {
		return err
	}
	if s.tx, err = newTxer(s.cn, key, c.Send, s.say); err != nil {
		return err
	}
	s.me = s.tx.from
	if c.LocalDomain == 0 {
		if !s.cn.chainID.IsUint64() || s.cn.chainID.Uint64() > 1<<32-1 {
			return fmt.Errorf("hleth setup: chain id %s is no Hyperlane domain: give -local-domain", s.cn.chainID)
		}
		c.LocalDomain = uint32(s.cn.chainID.Uint64())
	}
	d := s.d
	switch {
	case d.Deployer == "" && d.ProxyAdmin == "":
		d.ChainID, d.LocalDomain, d.GnoDomain, d.Deployer = s.cn.chainID.Uint64(), c.LocalDomain, c.GnoDomain, s.me.Hex()
	case !strings.EqualFold(d.Deployer, s.me.Hex()):
		return fmt.Errorf("hleth setup: %s was written by the deployer %s, not %s: resume it with that key", c.Out, d.Deployer, s.me.Hex())
	case d.ChainID != s.cn.chainID.Uint64() || d.LocalDomain != c.LocalDomain || d.GnoDomain != c.GnoDomain:
		return fmt.Errorf("hleth setup: %s is chain %d's deployment with domains %d/%d, not chain %s with %d/%d", c.Out, d.ChainID, d.LocalDomain, d.GnoDomain, s.cn.chainID, c.LocalDomain, c.GnoDomain)
	}
	if err := s.launchValues(); err != nil {
		return err
	}
	if c.Send.State == "" {
		s.tx.sender.Cfg.State = c.Out + ".sender.json"
	}
	s.say("deployer %s on chain %s (Ethereum's domain %d, gno.land's %d); the deployment's record is %s", s.me.Hex(), s.cn.chainID, c.LocalDomain, c.GnoDomain, c.Out)
	if bal, err := s.cn.c.BalanceAt(ctx, s.me, ethx.Latest); err == nil {
		s.say("the deployer holds %s wei", bal)
	}
	return s.startSequenceCheck(ctx)
}

// limits fixes each token's launch limits on the gate (E3-CF4; §5.4, as `hlsol setup` does, F10): compiled from the
// lock the launch notice expects of the token — the day cap the smaller of 100,000 and a tenth of it, the maximum the
// smaller of 25,000 and the day cap — and refused below the route realms' compiled maximum. An expected lock is
// required on every chain but a local one (anvil's 31337), where §5.4's ceilings stand in when none is expected;
// -max-per-message and -day-cap, given beside a lock, must be what it compiles.
func (s *setup) limits() error {
	c := &s.cfg
	realms := c.LaunchMaximum
	if realms == 0 {
		realms = launchMaximumDefault
	}
	expected := c.ExpectedLock[0] != 0 || c.ExpectedLock[1] != 0
	switch {
	case expected && (c.ExpectedLock[0] == 0 || c.ExpectedLock[1] == 0):
		return errors.New("hleth setup: -expected-lock-gnot and -expected-lock-bubble go together: each token's launch day cap is compiled from its own")
	case !expected && s.cn.chainID.Uint64() != localChainID:
		return fmt.Errorf("hleth setup: -expected-lock-gnot and -expected-lock-bubble (base units) are required on chain %s: the gate's launch day cap of each token is compiled from the lock the launch notice expects (the smaller of 100,000 and a tenth of it, §5.4; E3-CF4) — nothing was deployed", s.cn.chainID)
	}
	for i := range c.MaxPerMessage {
		if expected {
			mx, dc := launchLimits(c.ExpectedLock[i])
			if (c.MaxPerMessage[i] != 0 && c.MaxPerMessage[i] != mx) || (c.DayCap[i] != 0 && c.DayCap[i] != dc) {
				return fmt.Errorf("hleth setup: token %d's -max-per-message %d and -day-cap %d are not what its expected lock %d compiles (%d a message, %d a day: the smaller of 25,000 and the day cap, the smaller of 100,000 and a tenth of the lock, §5.4) — give the lock alone", i, c.MaxPerMessage[i], c.DayCap[i], c.ExpectedLock[i], mx, dc)
			}
			c.MaxPerMessage[i], c.DayCap[i] = mx, dc
			s.say("token %d's launch limits: %d a message, %d a day (base units), compiled from the expected lock %d (the day cap a tenth of it at most 100,000, the maximum at most 25,000 and the day cap; the realms' compiled maximum is %d)", i, mx, dc, c.ExpectedLock[i], realms)
		} else {
			if c.MaxPerMessage[i] == 0 {
				c.MaxPerMessage[i] = defaultMaxPerMessage
			}
			if c.DayCap[i] == 0 {
				c.DayCap[i] = defaultDayCap
			}
			s.say("token %d's launch limits: %d a message, %d a day (base units): a local chain's, no lock expected", i, c.MaxPerMessage[i], c.DayCap[i])
		}
		if c.MaxPerMessage[i] == 0 || c.DayCap[i] < c.MaxPerMessage[i] {
			return fmt.Errorf("hleth setup: token %d's limits: a per-message maximum of at least 1 and a day cap at least the maximum (base units)", i)
		}
		if err := launchMaximumCheck(i, c.MaxPerMessage[i], realms); err != nil {
			return err
		}
	}
	return nil
}

// launchValues records the values the setup compiles into the contracts — or, resuming, checks that the ones given
// are the ones an earlier run deployed with: a resumed setup never mixes two sets of bindings.
func (s *setup) launchValues() error {
	c := &s.cfg
	hash := crypto.Keccak256Hash([]byte(c.MailboxPkgPath))
	l := Launch{
		MailboxPkgPath: c.MailboxPkgPath, MailboxPkgPathHash: hash.Hex(),
		Hlgov: c.Hlgov, Hlgov32: realm32Hex(c.Hlgov), GnotRoute: c.GnotRoute, BubbleRoute: c.BubbleRoute,
		Route0: realm32Hex(c.GnotRoute), Route1: realm32Hex(c.BubbleRoute), Ed25519CodeHash: s.arts.Ed25519CodeHash().Hex(),
		MaxPerMessage: c.MaxPerMessage, DayCap: c.DayCap, ExpectedLock: c.ExpectedLock, Beneficiary: c.Beneficiary.Hex(), MaxProtocolFee: c.MaxProtocolFee.String(),
		Name0: c.Name0, Symbol0: c.Symbol0, Name1: c.Name1, Symbol1: c.Symbol1, StartSequence: c.StartSequence,
	}
	for i, a := range c.Seats {
		l.Seats[i] = a.Hex()
	}
	for i, a := range c.Watch {
		l.Watch[i] = a.Hex()
	}
	old := s.d.Launch
	if old.MailboxPkgPath == "" {
		s.d.Launch = l
		return nil
	}
	// the trusted header is the client step's own (read when it runs): kept from the earlier run
	l.GnoChainID, l.TrustedHeight, l.TrustedHash, l.TrustedTime, l.Validators, l.TrustedLeaves = old.GnoChainID, old.TrustedHeight, old.TrustedHash, old.TrustedTime, old.Validators, old.TrustedLeaves
	a, _ := json.Marshal(old)
	b, _ := json.Marshal(l)
	if string(a) != string(b) {
		return fmt.Errorf("hleth setup: the launch values given differ from those %s was deployed with (earlier: %s; now: %s): a resumed setup takes the same values, or a fresh deployment starts from a fresh deployer key and a new record", s.cfg.Out, a, b)
	}
	return nil
}

// realm32 is a realm's address as Hyperlane carries it: its package address, twelve zero bytes first.
func realm32(pkgPath string) [32]byte {
	k, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(pkgPath))
	return k
}

func realm32Hex(pkgPath string) string {
	k := realm32(pkgPath)
	return "0x" + hex.EncodeToString(k[:])
}

// startSequenceCheck reads r/hlgov's counter and refuses a -start-sequence that is not it (the Solana plan's AT3,
// §6.4: a fresh deployment under the same r/hlgov starts HlAdmin at r/hlgov's Last(), or every later decision is
// refused OutOfOrder; the counter never goes down). Without a gno node, or with r/hlgov not on the chain yet (a
// chain's first deployment: the realms go up after this side passes its audit), the number is taken as given, said
// so; the audit reads the counter once the realm is up.
func (s *setup) startSequenceCheck(ctx context.Context) error {
	c := s.cfg
	if c.GnoRPC == "" {
		s.say("start sequence %d: taken as given (no -gno-rpc to read r/hlgov's counter from; the audit reads it once the realm is up)", c.StartSequence)
		return nil
	}
	g := &gno.Client{RPC: c.GnoRPC}
	var gov struct {
		Last uint64 `json:"last"`
	}
	err := g.RenderJSON(ctx, c.Hlgov, "json", &gov)
	switch {
	case keeperNotFound(err, c.Hlgov):
		s.say("start sequence %d: r/hlgov (%s) is not on the chain yet, so the number is taken as given (a chain's first deployment starts at 0; the audit reads the counter against HlAdmin's once the realm is up)", c.StartSequence, c.Hlgov)
	case err != nil:
		return fmt.Errorf("hleth setup: r/hlgov's counter could not be read (%v): -start-sequence %d is checked against it before the first deploy — nothing was deployed", err, c.StartSequence)
	case gov.Last != c.StartSequence:
		return fmt.Errorf("hleth setup: -start-sequence %d is not r/hlgov's counter (last %d; its next decision takes %d): HlAdmin starts at r/hlgov's Last(), or r/hlgov's next decision is refused OutOfOrder for ever — pass -start-sequence %d. Nothing was deployed", c.StartSequence, gov.Last, gov.Last+1, gov.Last)
	default:
		s.say("start sequence %d: r/hlgov's counter (its next decision takes %d, HlAdmin's first)", c.StartSequence, gov.Last+1)
	}
	return nil
}

// --------------------------------------------------------------------------------------------- the steps --

func (s *setup) run(ctx context.Context) error {
	if err := s.tx.resumeCreate(ctx, s.d, s.save); err != nil {
		return err
	}
	a := s.arts
	// each creation's values are the shared builder's (creation.go), from the record: what the audit holds every
	// creation transaction's input to, byte for byte (E2-RT1)
	built := func(field string) func() (Values, error) {
		return func() (Values, error) {
			_, v, err := creationOf(field, s.d, s.d.Launch, s.arts)
			return v, err
		}
	}
	steps := []func(context.Context) error{
		// 1: the ProxyAdmin at the plan's first nonce, then at once its hand-over to HlAdmin's address-to-be
		func(ctx context.Context) error {
			if err := s.fixPlan(ctx); err != nil {
				return err
			}
			return s.deploy(ctx, "proxyAdmin", "the ProxyAdmin", a["ProxyAdmin"], built("proxyAdmin"))
		},
		s.handOver,
		// 2
		func(ctx context.Context) error {
			return s.deploy(ctx, "mailboxImpl", "the Mailbox's implementation", a["Mailbox"], built("mailboxImpl"))
		},
		func(ctx context.Context) error {
			return s.deployProxy(ctx, "mailbox", "the Mailbox's proxy", built("mailbox"))
		},
		// 3
		func(ctx context.Context) error {
			return s.deploy(ctx, "merkleTreeHook", "the MerkleTreeHook", a["MerkleTreeHook"], built("merkleTreeHook"))
		},
		func(ctx context.Context) error {
			return s.deploy(ctx, "protocolFee", "the ProtocolFee", a["ProtocolFee"], built("protocolFee"))
		},
		// 4: the Ed25519 verifier the client calls (E-M2a's Ed25519Verifier: no storage, no owner, no arguments, never
		// behind a proxy), its code this build's (HlAdmin pins its hash), then the client
		func(ctx context.Context) error {
			if err := s.deploy(ctx, "ed25519Verifier", "the Ed25519 verifier", a["Ed25519Verifier"], built("ed25519Verifier")); err != nil {
				return err
			}
			return s.verifierCode(ctx)
		},
		func(ctx context.Context) error {
			return s.deploy(ctx, "clientImpl", "the light client's implementation", a["GnoLightClient"], built("clientImpl"))
		},
		func(ctx context.Context) error {
			// the trusted header is read (and recorded) just before the proxy is sent, then built from the record
			return s.deployProxy(ctx, "client", "the light client's proxy", func() (Values, error) {
				if err := s.clientInit(ctx); err != nil {
					return nil, err
				}
				return built("client")()
			})
		},
		// 5
		func(ctx context.Context) error { return s.token(ctx, 0, built) },
		func(ctx context.Context) error { return s.token(ctx, 1, built) },
		// 6
		func(ctx context.Context) error {
			return s.deploy(ctx, "gateImpl", "the gate's implementation", a["PausableGate"], built("gateImpl"))
		},
		func(ctx context.Context) error {
			return s.deployProxy(ctx, "gate", "the gate's proxy", built("gate"))
		},
		// 7
		func(ctx context.Context) error { return s.hlAdmin(ctx) },
		// 8, 9, 10, 11
		s.settings,
		s.ownership,
		s.finish,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// planIndex is each creation's place in the nonce plan after the deployer's first nonce (the hand-over is 1).
var planIndex = map[string]uint64{"proxyAdmin": 0, "mailboxImpl": 2, "mailbox": 3, "merkleTreeHook": 4, "protocolFee": 5, "ed25519Verifier": 6,
	"clientImpl": 7, "client": 8, "token0Impl": 9, "token0": 10, "token1Impl": 11, "token1": 12, "gateImpl": 13, "gate": 14, "hlAdmin": 15}

// handOverIndex is the hand-over's place in the plan; planHlAdmin is HlAdmin's.
const (
	handOverIndex = 1
	planHlAdmin   = 15
)

// fixPlan fixes the nonce plan before the ProxyAdmin is sent: its first nonce the deployer's own now, HlAdmin's
// nonce and address from it. A record that already created something keeps its plan; one that created nothing yet
// (a first run, or one stopped before its first transaction) takes the deployer's nonce now.
func (s *setup) fixPlan(ctx context.Context) error {
	d := s.d
	if d.ProxyAdmin != "" || d.Pending != nil {
		if d.AdminAt == "" {
			return fmt.Errorf("hleth setup: %s created contracts with no nonce plan (an earlier build's record): a fresh deployment starts from a new record", s.cfg.Out)
		}
		return nil
	}
	n, err := s.cn.c.NonceAt(ctx, s.me, ethx.Latest)
	if err != nil {
		return err
	}
	if p, err := s.cn.c.NonceAt(ctx, s.me, ethx.Pending); err == nil && p != n {
		return fmt.Errorf("hleth setup: the deployer has %d transaction(s) pending: the nonce plan starts at its next nonce, once they are mined", p-n)
	}
	d.FirstNonce, d.AdminNonce = n, n+planHlAdmin
	d.AdminAt = crypto.CreateAddress(s.me, d.AdminNonce).Hex()
	s.say("the nonce plan: the ProxyAdmin at the deployer's nonce %d, its hand-over at %d, HlAdmin at %d — %s, which owns the ProxyAdmin from step 1", n, n+handOverIndex, d.AdminNonce, d.AdminAt)
	return s.save()
}

// onPlan says a step's planned nonce, refusing when the deployer's nonce is not it: a transaction outside the plan
// took one (another of the key's, or a reverted one of the setup's), so HlAdmin can no longer land at the address the
// ProxyAdmin was handed to. Nothing is sent: the deployment starts over from a new record (nothing was minted).
func (s *setup) onPlan(ctx context.Context, label string, index uint64) (uint64, error) {
	planned := s.d.FirstNonce + index
	n, err := s.cn.c.NonceAt(ctx, s.me, ethx.Latest)
	if err != nil {
		return 0, err
	}
	p, err := s.cn.c.NonceAt(ctx, s.me, ethx.Pending)
	if err != nil {
		return 0, err
	}
	if n != planned || p != planned {
		return 0, fmt.Errorf("hleth setup: %w: %s goes at the deployer's nonce %d, and its nonce is %d (pending %d): a transaction outside the plan took a nonce, so HlAdmin can no longer be created at %s, the address the ProxyAdmin went to — nothing more is sent; the deployment starts over from a fresh record (nothing was minted, and a ProxyAdmin owned by an address no code will reach is inert)", errPlan, label, planned, n, p, s.d.AdminAt)
	}
	return planned, nil
}

// handOver is step 1's second transaction: the ProxyAdmin's ownership to HlAdmin's address-to-be, at the plan's
// first nonce + 1, before any proxy exists (done when the ProxyAdmin is that address's already).
func (s *setup) handOver(ctx context.Context) error {
	pa, at := s.d.Addr("proxyAdmin"), common.HexToAddress(s.d.AdminAt)
	art := s.arts["ProxyAdmin"]
	owner, err := s.owner(ctx, pa, art)
	if err != nil {
		return err
	}
	label := "the ProxyAdmin's owner → HlAdmin's address-to-be " + at.Hex()
	switch owner {
	case at:
		s.say("%s: done", label)
		return nil
	case s.me:
	default:
		return fmt.Errorf("hleth setup: the ProxyAdmin's owner is %s, neither the deployer nor HlAdmin's planned address %s: a fresh deployment", owner.Hex(), at.Hex())
	}
	planned, err := s.onPlan(ctx, "the hand-over of the ProxyAdmin", handOverIndex)
	if err != nil {
		return err
	}
	data, err := art.CallData("transferOwnership", valuesOne(art, "transferOwnership", at))
	if err != nil {
		return err
	}
	rc, err := s.tx.callAt(ctx, label, pa, data, planned)
	if err != nil {
		return err
	}
	if o, err := s.owner(ctx, pa, art); err != nil || o != at {
		return fmt.Errorf("hleth setup: the hand-over was mined (tx %s) and the ProxyAdmin's owner reads %s (%v)", rc.TxHash.Hex(), o.Hex(), err)
	}
	s.d.stepDone("proxyAdminHandOver", rc.TxHash.Hex(), rc.BlockNumber.Uint64())
	s.say("%s: tx %s (nonce %d)", label, rc.TxHash.Hex(), planned)
	return s.save()
}

// deploy creates a contract unless the deployment records one with code at its address — at the plan's nonce for
// its field.
func (s *setup) deploy(ctx context.Context, field, label string, art *Artifact, vals func() (Values, error)) error {
	if addr := s.d.Addr(field); addr != (common.Address{}) {
		code, err := s.cn.code(ctx, ethx.Latest, addr)
		if err != nil {
			return err
		}
		if len(code) == 0 {
			return fmt.Errorf("hleth setup: %s is recorded at %s but no code is there (another chain, or a reset one): a fresh deployment starts from a new record", label, addr.Hex())
		}
		s.say("%s: done (%s)", label, addr.Hex())
		return nil
	}
	index, ok := planIndex[field]
	if !ok {
		return fmt.Errorf("hleth setup: %s has no place in the nonce plan", field)
	}
	planned, err := s.onPlan(ctx, label, index)
	if err != nil {
		return err
	}
	v, err := vals()
	if err != nil {
		return err
	}
	code, err := art.CreationCode(v)
	if err != nil {
		return err
	}
	addr, rc, err := s.tx.create(ctx, s.d, s.save, field, label, code, planned)
	if err != nil {
		return err
	}
	s.d.stepDone(field, rc.TxHash.Hex(), rc.BlockNumber.Uint64())
	if field == "proxyAdmin" || s.d.FirstBlock == 0 {
		s.d.FirstBlock = rc.BlockNumber.Uint64()
	}
	s.say("%s: %s (tx %s, block %d, %d gas)", label, addr.Hex(), rc.TxHash.Hex(), rc.BlockNumber.Uint64(), rc.GasUsed)
	return s.save()
}

// deployProxy creates a TransparentUpgradeableProxy over the recorded implementation, its initializer's call in the
// constructor (so it is never open to a front-runner): vals are the proxy's constructor values (creationOf's).
func (s *setup) deployProxy(ctx context.Context, field, label string, vals func() (Values, error)) error {
	return s.deploy(ctx, field, label, s.arts["TransparentUpgradeableProxy"], vals)
}

// token deploys one HypERC20 and its proxy.
func (s *setup) token(ctx context.Context, i int, built func(string) func() (Values, error)) error {
	symbol := s.cfg.Symbol0
	if i == 1 {
		symbol = s.cfg.Symbol1
	}
	impl, proxy := fmt.Sprintf("token%dImpl", i), fmt.Sprintf("token%d", i)
	if err := s.deploy(ctx, impl, fmt.Sprintf("token %d's implementation (%s)", i, symbol), s.arts["HypERC20"], built(impl)); err != nil {
		return err
	}
	return s.deployProxy(ctx, proxy, fmt.Sprintf("token %d's proxy (%s)", i, symbol), built(proxy))
}

// verifierCode holds the deployed verifier's code against this build: its EXTCODEHASH is what HlAdmin's Config pins
// (ed25519CodeHash) and what finishSetup's check 28 requires, so a verifier whose code is another build's stops the
// setup here, before HlAdmin is deployed over it.
func (s *setup) verifierCode(ctx context.Context) error {
	v := s.d.Addr("ed25519Verifier")
	code, err := s.cn.code(ctx, ethx.Latest, v)
	if err != nil {
		return err
	}
	want := s.arts.Ed25519CodeHash()
	if got := crypto.Keccak256Hash(code); got != want {
		return fmt.Errorf("hleth setup: the Ed25519 verifier at %s has code hash %s, not this build's %s (%d bytes against %d): HlAdmin pins the build's hash, and finishSetup would refuse it (check 28) — a fresh deployment", v.Hex(), got.Hex(), want.Hex(), len(code), len(s.arts["Ed25519Verifier"].Runtime))
	}
	return nil
}

// hlAdmin deploys HlAdmin over its Config (M2-NOTES.md §1 D: the fields by name, packed in the struct's order) and
// records the HlAdminExec its constructor created.
func (s *setup) hlAdmin(ctx context.Context) error {
	d := s.d
	err := s.deploy(ctx, "hlAdmin", "HlAdmin", s.arts["HlAdmin"], func() (Values, error) {
		_, v, err := creationOf("hlAdmin", d, d.Launch, s.arts)
		return v, err
	})
	if err != nil {
		return err
	}
	if at := common.HexToAddress(d.AdminAt); d.Addr("hlAdmin") != at {
		return fmt.Errorf("hleth setup: HlAdmin is at %s, not %s, the address the plan handed the ProxyAdmin to", d.Addr("hlAdmin").Hex(), at.Hex())
	}
	if d.HlAdminExec == "" {
		exec, err := read[common.Address](ctx, s.cn, ethx.Latest, d.Addr("hlAdmin"), s.arts["HlAdmin"], "exec")
		if err != nil {
			return err
		}
		d.HlAdminExec = exec.Hex()
		s.say("HlAdminExec (created by HlAdmin's constructor): %s", exec.Hex())
		return s.save()
	}
	return nil
}

// clientInit reads the client's trusted header just before step 4's proxy is sent (fold F4: initialize binds its
// trust root) — a node's newest fit header, or an offline file's, every check made whatever the source — and records
// it: the chain id, the height, the hash, the time, the set and the sixteen leaves the proxy's initializer takes
// (creationOf builds them from the record, and the audit rebuilds the proxy's creation input from them, E2-RT1).
func (s *setup) clientInit(ctx context.Context) error {
	th, err := s.trusted(ctx)
	if err != nil {
		return err
	}
	if err := s.checkTrusted(ctx, th); err != nil {
		return err
	}
	hash := th.Header.Hash()
	l := &s.d.Launch
	l.GnoChainID, l.TrustedHeight, l.TrustedHash, l.TrustedTime = th.Header.ChainID, th.Header.Height, "0x"+hex.EncodeToString(hash[:]), th.Header.Time.Secs
	l.Validators, l.TrustedLeaves = nil, nil
	for _, v := range th.Set.Validators {
		l.Validators = append(l.Validators, ValidatorJSON{PubKey: "0x" + hex.EncodeToString(v.PubKey[:]), Power: v.Power})
	}
	for _, leaf := range ethx.HeaderLeaves(th.Header) {
		l.TrustedLeaves = append(l.TrustedLeaves, "0x"+hex.EncodeToString(leaf))
	}
	sh, _ := th.Set.Hash()
	s.say("the client's start: chain %s, origin %d, trusted header %d %x of %s (%d validators, set hash %x)", th.Header.ChainID, s.cfg.GnoDomain, th.Header.Height, hash,
		time.Unix(th.Header.Time.Secs, 0).UTC().Format(time.RFC3339), len(th.Set.Validators), sh)
	return s.save()
}

// trustedHeader is the header the client starts from, with its set.
type trustedHeader struct {
	Header *gnolight.Header
	Set    *gnolight.ValidatorSet
	From   string // where it was read
}

// trustedFileJSON is -trusted-file's shape: an offline header and its set (a test, or a node read elsewhere) — the
// header as a node's /block serves it (its `block.header`), so every check of a node's header is made on it too.
type trustedFileJSON struct {
	Header     json.RawMessage `json:"header"`
	Validators []ValidatorJSON `json:"validators"`
}

// maxTrustAge is how far the trusted header's time may lie from the chain's newest block when step 4 goes: the client
// refuses more than an hour either way (TrustedTime), and the setup keeps the other half hour for the steps between.
const maxTrustAge = 30 * time.Minute

// trusted reads the trusted header: from -trusted-file, or the newest header of the node at or below -trusted-height
// that announces no change (§5.8 step 4: never one announcing a change).
func (s *setup) trusted(ctx context.Context) (*trustedHeader, error) {
	if s.cfg.TrustedFile != "" {
		b, err := os.ReadFile(s.cfg.TrustedFile)
		if err != nil {
			return nil, err
		}
		var j trustedFileJSON
		if err := json.Unmarshal(b, &j); err != nil {
			return nil, fmt.Errorf("-trusted-file %s: %w", s.cfg.TrustedFile, err)
		}
		if len(j.Header) == 0 {
			return nil, fmt.Errorf("-trusted-file %s: no header (the node's /block block.header, every field)", s.cfg.TrustedFile)
		}
		hd, err := gnolight.HeaderFromJSON(j.Header)
		if err != nil {
			return nil, fmt.Errorf("-trusted-file %s: the header: %w", s.cfg.TrustedFile, err)
		}
		th := &trustedHeader{Header: hd, Set: &gnolight.ValidatorSet{}, From: s.cfg.TrustedFile}
		for _, v := range j.Validators {
			pk, err := hl.ParseKey32(v.PubKey)
			if err != nil {
				return nil, fmt.Errorf("-trusted-file: a validator key: %w", err)
			}
			th.Set.Validators = append(th.Set.Validators, gnolight.Validator{PubKey: pk, Power: v.Power})
		}
		return th, nil
	}
	g := &gno.Client{RPC: s.cfg.GnoRPC}
	chain := s.cfg.GnoChainID
	if chain == "" {
		blk, err := g.BlockAt(ctx, 1)
		if err != nil {
			return nil, err
		}
		chain = blk.Header.ChainID
	}
	head, err := g.Status(ctx)
	if err != nil {
		return nil, err
	}
	h := s.cfg.TrustedHeight
	if h == 0 || h > head.Height {
		h = head.Height
	}
	for ; h >= 1; h-- {
		blk, err := g.BlockAt(ctx, h)
		if err != nil {
			return nil, err
		}
		if blk.Header.ChainID != chain {
			return nil, fmt.Errorf("the node's block %d is of chain %q, not %q", h, blk.Header.ChainID, chain)
		}
		if blk.Header.Announcing() {
			s.say("trusted header: %d announces a set change; taking the one before", h)
			continue
		}
		if blk.Header.Hash() != blk.Hash {
			return nil, fmt.Errorf("the node's block %d: its header's fields hash to %x, the node says %x", h, blk.Header.Hash(), blk.Hash)
		}
		set, err := g.ValidatorsAt(ctx, h)
		if err != nil {
			return nil, err
		}
		return &trustedHeader{Header: blk.Header, Set: set, From: "the node"}, nil
	}
	return nil, errors.New("no header to trust: every one at or below the height announces a change")
}

// checkTrusted is what the client's initialize will check of the trusted header, checked first here on a header from
// either source (fold F4): a sane header of a chain id; its set sane and hashing to its ValidatorsHash; no change
// announced (its NextValidatorsHash the same set's); its time within maxTrustAge of the newest Ethereum block's (the
// client allows an hour either way of the block that initializes it).
func (s *setup) checkTrusted(ctx context.Context, th *trustedHeader) error {
	hd := th.Header
	if err := hd.Check(); err != nil {
		return fmt.Errorf("the trusted header from %s: %w", th.From, err)
	}
	if s.cfg.GnoChainID != "" && hd.ChainID != s.cfg.GnoChainID {
		return fmt.Errorf("the trusted header from %s is of chain %q, not -gno-chain-id %q", th.From, hd.ChainID, s.cfg.GnoChainID)
	}
	if err := th.Set.Check(); err != nil {
		return fmt.Errorf("the trusted header's set (from %s): %w", th.From, err)
	}
	sh, _ := th.Set.Hash()
	if sh != hd.ValidatorsHashArray() {
		return fmt.Errorf("the trusted header %d (from %s): its validators hash to %x, not its ValidatorsHash %x", hd.Height, th.From, sh, hd.ValidatorsHashArray())
	}
	if hd.Announcing() {
		return fmt.Errorf("the trusted header %d (from %s) announces a set change (its NextValidatorsHash is not its set's): the client refuses it (§5.8 step 4)", hd.Height, th.From)
	}
	head, err := s.cn.c.BlockAt(ctx, ethx.Latest)
	if err != nil {
		return err
	}
	if d := time.Duration(int64(head.Time)-hd.Time.Secs) * time.Second; d > maxTrustAge || d < -maxTrustAge {
		return fmt.Errorf("the trusted header %d (from %s) is of %s, %s from Ethereum's newest block (%s): the client takes one within an hour of its initialization, and the setup wants it within %s — read a newer header", hd.Height, th.From,
			time.Unix(hd.Time.Secs, 0).UTC().Format(time.RFC3339), d, time.Unix(int64(head.Time), 0).UTC().Format(time.RFC3339), maxTrustAge)
	}
	return nil
}

// ------------------------------------------------------------------------------------------- the settings --

// setting sends a call unless done() says it is done, then checks it is.
func (s *setup) setting(ctx context.Context, label string, done func() (bool, error), owner common.Address, to common.Address, art *Artifact, method string, vals Values) error {
	ok, err := done()
	if err != nil {
		return err
	}
	if ok {
		s.say("%s: done", label)
		return nil
	}
	if owner != s.me {
		return fmt.Errorf("hleth setup: %s is not done and the contract's owner is %s, not the deployer: the setup cannot set it (a fresh deployment)", label, owner.Hex())
	}
	data, err := art.CallData(method, vals)
	if err != nil {
		return err
	}
	rc, err := s.tx.call(ctx, label, to, data, nil)
	if err != nil {
		return err
	}
	if ok, err := done(); err != nil || !ok {
		return fmt.Errorf("hleth setup: %s was mined (tx %s) and is not done: %v", label, rc.TxHash.Hex(), err)
	}
	s.d.stepDone(label, rc.TxHash.Hex(), rc.BlockNumber.Uint64())
	s.say("%s: tx %s", label, rc.TxHash.Hex())
	return s.save()
}

func (s *setup) owner(ctx context.Context, a common.Address, art *Artifact) (common.Address, error) {
	return read[common.Address](ctx, s.cn, ethx.Latest, a, art, "owner")
}

// settings are steps 8 and 9.
func (s *setup) settings(ctx context.Context) error {
	d, hy := s.d, s.arts["HypERC20"]
	gate := d.Addr("gate")
	for i, route := range []string{s.cfg.GnotRoute, s.cfg.BubbleRoute} {
		t := d.Addr(fmt.Sprintf("token%d", i))
		owner, err := s.owner(ctx, t, hy)
		if err != nil {
			return err
		}
		err = s.setting(ctx, fmt.Sprintf("token %d's ISM → the gate", i), func() (bool, error) {
			ism, err := read[common.Address](ctx, s.cn, ethx.Latest, t, hy, "interchainSecurityModule")
			return ism == gate, err
		}, owner, t, hy, "setInterchainSecurityModule", valuesOne(hy, "setInterchainSecurityModule", gate))
		if err != nil {
			return err
		}
		want := realm32(route)
		err = s.setting(ctx, fmt.Sprintf("token %d's router for gno.land's domain %d → %s", i, s.cfg.GnoDomain, route), func() (bool, error) {
			r, err := read[[32]byte](ctx, s.cn, ethx.Latest, t, hy, "routers", s.cfg.GnoDomain)
			return r == want, err
		}, owner, t, hy, "enrollRemoteRouter", Values{"domain": s.cfg.GnoDomain, "router": want})
		if err != nil {
			return err
		}
	}
	mb, mailbox := s.arts["Mailbox"], d.Addr("mailbox")
	owner, err := s.owner(ctx, mailbox, mb)
	if err != nil {
		return err
	}
	for _, x := range []struct {
		get, set, what string
		want           common.Address
	}{
		{"defaultIsm", "setDefaultIsm", "the mailbox's default ISM → the gate", gate},
		{"defaultHook", "setDefaultHook", "the mailbox's default hook → the MerkleTreeHook", d.Addr("merkleTreeHook")},
		{"requiredHook", "setRequiredHook", "the mailbox's required hook → the ProtocolFee", d.Addr("protocolFee")},
	} {
		x := x
		err := s.setting(ctx, x.what, func() (bool, error) {
			v, err := read[common.Address](ctx, s.cn, ethx.Latest, mailbox, mb, x.get)
			return v == x.want, err
		}, owner, mailbox, mb, x.set, valuesOne(mb, x.set, x.want))
		if err != nil {
			return err
		}
	}
	return nil
}

// valuesOne names a one-parameter method's argument by the ABI's own name.
func valuesOne(a *Artifact, method string, v any) Values {
	m := a.ABI.Methods[method]
	if len(m.Inputs) != 1 {
		return Values{}
	}
	return Values{norm(m.Inputs[0].Name): v}
}

// ownership is step 10, in §5.8's order: the hooks, the tokens, the gate, the mailbox, the client's admin (the
// ProxyAdmin is HlAdmin's since step 1).
func (s *setup) ownership(ctx context.Context) error {
	d := s.d
	admin := d.Addr("hlAdmin")
	for _, x := range []struct {
		field string
		art   *Artifact
		what  string
	}{
		{"merkleTreeHook", s.arts["MerkleTreeHook"], "the MerkleTreeHook"},
		{"protocolFee", s.arts["ProtocolFee"], "the ProtocolFee"},
		{"token0", s.arts["HypERC20"], "token 0"},
		{"token1", s.arts["HypERC20"], "token 1"},
		{"gate", s.arts["PausableGate"], "the gate"},
		{"mailbox", s.arts["Mailbox"], "the mailbox"},
	} {
		addr := d.Addr(x.field)
		owner, err := s.owner(ctx, addr, x.art)
		if err != nil {
			return err
		}
		err = s.setting(ctx, x.what+"'s owner → HlAdmin", func() (bool, error) {
			o, err := s.owner(ctx, addr, x.art)
			return o == admin, err
		}, owner, addr, x.art, "transferOwnership", valuesOne(x.art, "transferOwnership", admin))
		if err != nil {
			return err
		}
	}
	gc, client := s.arts["GnoLightClient"], d.Addr("client")
	cadmin, err := read[common.Address](ctx, s.cn, ethx.Latest, client, gc, "admin")
	if err != nil {
		return err
	}
	// last: the deployer holds nothing after it (the ProxyAdmin is HlAdmin's since step 1)
	return s.setting(ctx, "the client's admin → HlAdmin", func() (bool, error) {
		a, err := read[common.Address](ctx, s.cn, ethx.Latest, client, gc, "admin")
		return a == admin, err
	}, cadmin, client, gc, "setAdmin", valuesOne(gc, "setAdmin", admin))
}

// finish is step 11: HlAdmin.finishSetup(), then the done condition — setupDone, and no human key holds an
// authority.
func (s *setup) finish(ctx context.Context) error {
	d, ha := s.d, s.arts["HlAdmin"]
	admin := d.Addr("hlAdmin")
	done, err := read[bool](ctx, s.cn, ethx.Latest, admin, ha, "setupDone")
	if err != nil {
		return err
	}
	if done {
		s.say("HlAdmin.finishSetup: done")
	} else {
		data, err := ha.CallData("finishSetup", Values{})
		if err != nil {
			return err
		}
		rc, err := s.tx.call(ctx, "HlAdmin.finishSetup", admin, data, nil)
		if err != nil {
			return fmt.Errorf("%w (SetupIncomplete(n) names the check: HlAdminExec.checkSetup's table)", err)
		}
		s.d.stepDone("finishSetup", rc.TxHash.Hex(), rc.BlockNumber.Uint64())
		s.d.DeployBlock = rc.BlockNumber.Uint64()
		s.say("HlAdmin.finishSetup: tx %s (block %d: every scan of the machines starts there, eth.deployBlock)", rc.TxHash.Hex(), s.d.DeployBlock)
		if err := s.save(); err != nil {
			return err
		}
	}
	if d.DeployBlock == 0 {
		// finishSetup was mined by an earlier run (or by anyone: it is permissionless) and the record has no block for
		// it: its SetupDone log says where
		b, err := setupDoneBlock(ctx, s.cn, admin, d.FirstBlock)
		if err != nil {
			return err
		}
		d.DeployBlock = b
		s.say("HlAdmin's SetupDone is at block %d: eth.deployBlock", b)
		if err := s.save(); err != nil {
			return err
		}
	}
	if d.Factor == 0 {
		f, err := read[uint64](ctx, s.cn, ethx.Latest, d.Addr("client"), s.arts["GnoLightClient"], "clockFactor")
		if err != nil {
			return err
		}
		d.Factor = f
		if err := s.save(); err != nil {
			return err
		}
	}
	// the done condition: every owner and admin is HlAdmin
	for _, x := range []struct {
		field, method string
		art           *Artifact
	}{
		{"proxyAdmin", "owner", s.arts["ProxyAdmin"]}, {"mailbox", "owner", s.arts["Mailbox"]}, {"token0", "owner", s.arts["HypERC20"]},
		{"token1", "owner", s.arts["HypERC20"]}, {"gate", "owner", s.arts["PausableGate"]}, {"merkleTreeHook", "owner", s.arts["MerkleTreeHook"]},
		{"protocolFee", "owner", s.arts["ProtocolFee"]}, {"client", "admin", s.arts["GnoLightClient"]},
	} {
		o, err := read[common.Address](ctx, s.cn, ethx.Latest, d.Addr(x.field), x.art, x.method)
		if err != nil {
			return err
		}
		if o != admin {
			return fmt.Errorf("hleth setup: %s's %s is %s, not HlAdmin: the setup is not done", x.field, x.method, o.Hex())
		}
	}
	s.say("no human key holds an authority: every owner and admin is HlAdmin %s; the clock factor %d", admin.Hex(), d.Factor)
	if s.cfg.ConfigOut != "" {
		if err := writeMachineConfig(s.cfg.ConfigOut, d); err != nil {
			return err
		}
		s.say("the machines' config (the deployment's part: the contracts, the domains, eth.deployBlock %d) is %s", d.DeployBlock, s.cfg.ConfigOut)
	}
	return nil
}

// setupDoneBlock is the block of HlAdmin's one SetupDone log (finishSetup's), read from the deployment's first block.
func setupDoneBlock(ctx context.Context, cn *conn, admin common.Address, from uint64) (uint64, error) {
	logs, err := cn.c.LogsRange(ctx, ethx.LogQuery{Addresses: []common.Address{admin}, Topics: [][]common.Hash{{ethx.Admin.Event("SetupDone")}}}, from, latestNumber(ctx, cn))
	if err != nil {
		return 0, fmt.Errorf("HlAdmin's SetupDone log: %w", err)
	}
	if len(logs) != 1 {
		return 0, fmt.Errorf("HlAdmin %s has %d SetupDone logs from block %d (one is finishSetup's)", admin.Hex(), len(logs), from)
	}
	return logs[0].BlockNumber, nil
}

func latestNumber(ctx context.Context, cn *conn) uint64 {
	b, err := cn.c.BlockAt(ctx, ethx.Latest)
	if err != nil {
		return 0
	}
	return b.Number
}

// machineConfig is the part of the machines' config (agent.Config, bridge.json) the deployment fixes: the Ethereum
// side's contracts, domains and eth.deployBlock — finishSetup's block, where every scan of the signers, the monitors,
// the inbound relayer and hlwatch starts (E2-RT6) — and the routes' tokens. Each machine adds its providers, its gno
// node and its keys; the launch notice publishes this part.
type machineConfig struct {
	Gno struct {
		Mailbox string `json:"mailbox"`
		Gov     string `json:"gov"`
	} `json:"gno"`
	Eth struct {
		ChainID        uint64 `json:"chainId"`
		Domain         uint32 `json:"domain"`
		Mailbox        string `json:"mailbox"`
		MerkleTreeHook string `json:"merkleTreeHook"`
		ProtocolFee    string `json:"protocolFee"`
		Client         string `json:"client"`
		Gate           string `json:"gate"`
		Admin          string `json:"admin"`
		ProxyAdmin     string `json:"proxyAdmin"`
		DeployBlock    uint64 `json:"deployBlock"`
	} `json:"eth"`
	Routes []machineRoute `json:"routes"`
}

type machineRoute struct {
	Symbol string `json:"symbol"`
	Gno    string `json:"gno"`
	Token  string `json:"token"`
}

// machineConfigOf is a finished deployment's part of the machines' config.
func machineConfigOf(d *Deployment) (*machineConfig, error) {
	if d.DeployBlock == 0 {
		return nil, errors.New("the deployment's finishSetup block is not recorded: the setup is not done")
	}
	var c machineConfig
	c.Gno.Mailbox, c.Gno.Gov = d.Launch.MailboxPkgPath, d.Launch.Hlgov
	e := &c.Eth
	e.ChainID, e.Domain, e.DeployBlock = d.ChainID, d.LocalDomain, d.DeployBlock
	e.Mailbox, e.MerkleTreeHook, e.ProtocolFee, e.Client, e.Gate, e.Admin, e.ProxyAdmin = d.Mailbox, d.MerkleTreeHook, d.ProtocolFee, d.Client, d.Gate, d.HlAdmin, d.ProxyAdmin
	c.Routes = []machineRoute{{Symbol: d.Launch.Symbol0, Gno: d.Launch.GnotRoute, Token: d.Token0}, {Symbol: d.Launch.Symbol1, Gno: d.Launch.BubbleRoute, Token: d.Token1}}
	return &c, nil
}

// writeMachineConfig writes it (0644: it holds no secret).
func writeMachineConfig(path string, d *Deployment) error {
	c, err := machineConfigOf(d)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

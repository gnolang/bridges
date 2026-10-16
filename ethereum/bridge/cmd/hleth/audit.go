package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
	"github.com/gnolang/bridges/ethereum/bridge/repro"
)

// The audit (PLAN.md §5.8, §8): every authority and setting of the deployment read from the chain at one block,
// every RPC call printed before its answer (as a curl line against $RPC, so a member repeats each by hand with
// their own provider), each of the sixteen deployments' runtime code against its reproducible build with every
// masked immutable's value printed beside the value it must have, the Ed25519 verifier the client calls held three
// ways (the client's ED25519() beside HlAdmin's ed25519(), its EXTCODEHASH beside this tree's build and beside
// HlAdmin's ed25519CodeHash()), each proxy's EIP-1967 slots, and the verdict: "hleth audit: PASS", "hleth audit:
// FAIL (n)", or "hleth audit: UNVERIFIED (n)" — nothing failed, yet a binding rests on the deployer's record alone —
// and a non-zero exit for both. The expected values come from sources independent of the deployer where they exist
// (the member's own flags, gno.land read through the gno sources: audit_expect.go, audit_gno.go), else from the
// deployment's record of its launch values, said so; the trust root and the realms' paths are never PASSed on the
// record alone (E3-CF2, E3-RT5, E3-GS4). After governed changes each moved value is held to the executed decision
// that moved it (E3-CF9, audit_line.go).

type auditConfig struct {
	RPC            string
	Deployment     string
	ContractsOut   string
	HyperlaneDir   string
	GnoRPC         string // the member's own gno node or several, comma separated: each read, all must agree
	Config         string // the machines' config (bridge.json), the operator's: its gno.rpc and gno.sources read beside GnoRPC's (alone: UNVERIFIED), its routes' expectedLock only noted (E5-CF3 item 3)
	MailboxPkgPath string
	Hlgov          string
	GnotRoute      string
	BubbleRoute    string
	Factor         uint64
	At             string // latest, finalized, safe, or a block number
	// the member's own launch values (E3-GS4), each held against the deployment when given
	Seats, Watch  string    // four and two addresses
	Beneficiary   string    //
	StartSequence *uint64   // nil: r/hlgov's when it is up, else a chain's first deployment's 0
	ExpectedLock  [2]uint64 // base units, the member's own; zero: none, UNVERIFIED (§5.4's ceilings bound the limits)
	LaunchMaximum uint64    // the route realms' compiled maximum (0: their source's 25,000)
}

type audit struct {
	cfg    auditConfig
	out    io.Writer
	cn     *conn
	at     ethx.At
	atNum  uint64 // the block every reading is at
	atTime int64  // … and its time
	arts   Artifacts
	d      *Deployment
	fails  []string
	// the expected bindings
	pkgPath            string
	hlgov32            [32]byte
	route              [2][32]byte
	routePath          [2]string
	hlgovPath          string
	mailboxPkgPathHash common.Hash
	warns              []string
	// unverified are the readings the verdict cannot vouch for: a binding held to the deployer's record alone
	unverified []string
	// launchDiffers names the launch values the member gave that differ from the deployer's record (each with its
	// flag): a creation input that then fails says so (E4-TL6)
	launchDiffers []string
	// the independent readings: the gno sources, the config's facts, the launch values held (audit_expect.go),
	// r/hlgov, the HlAdmin line and its governed changes (audit_line.go)
	gnos         []gnoSource
	cf           *configFacts
	launch       Launch
	gov          *govFacts
	govDecisions map[uint64]*gnoside.GovDecision
	line         *lineFacts
}

// auditResult is the audit's verdict: what failed, and what rests on the deployer's record alone.
type auditResult struct {
	Fails, Unverified []string
}

// unverify records a reading the audit cannot hold to anything but the deployer's record: no failure, but no PASS.
func (a *audit) unverify(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	fmt.Fprintln(a.out, "UNVERIFIED "+text)
	a.unverified = append(a.unverified, text)
}

func (a *audit) check(ok bool, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if ok {
		fmt.Fprintln(a.out, "ok   "+text)
		return
	}
	fmt.Fprintln(a.out, "FAIL "+text)
	a.fails = append(a.fails, text)
}

func (a *audit) note(format string, args ...any) { fmt.Fprintf(a.out, "     "+format+"\n", args...) }

// warn records a finding that fails nothing but a member must read (a dispatch before the setup was done, which no
// machine reads: E2-RT6).
func (a *audit) warn(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	fmt.Fprintln(a.out, "WARN "+text)
	a.warns = append(a.warns, text)
}

func (a *audit) section(title string) { fmt.Fprintf(a.out, "\n== %s\n", title) }

// fail records a reading that could not be made: the audit cannot vouch for what it did not read.
func (a *audit) fail(what string, err error) {
	a.check(false, "%s: not read (%s)", what, ethx.Redact(err.Error()))
}

// runAudit audits a deployment and answers the failures (runAuditResult's, without what it left unverified).
func runAudit(ctx context.Context, cfg auditConfig, out io.Writer) ([]string, error) {
	r, err := runAuditResult(ctx, cfg, out)
	if err != nil {
		return nil, err
	}
	return r.Fails, nil
}

// runAuditResult audits a deployment and answers its verdict.
func runAuditResult(ctx context.Context, cfg auditConfig, out io.Writer) (*auditResult, error) {
	a := &audit{cfg: cfg, out: out, govDecisions: map[uint64]*gnoside.GovDecision{}}
	var err error
	if a.d, err = LoadDeployment(cfg.Deployment, false); err != nil {
		return nil, err
	}
	if a.arts, err = LoadArtifacts(cfg.ContractsOut, cfg.HyperlaneDir); err != nil {
		return nil, err
	}
	if a.cfg.Factor == 0 {
		a.cfg.Factor = 1 // mainnet's build: the 7-day delays at 7 days
	}
	if a.gnos, a.cf, err = gnoSourcesOf(cfg.GnoRPC, cfg.Config); err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "hleth audit of %s at %s (the rpc> lines take RPC=<this provider's URL>, whose key is never printed)\n", ethx.Redact(cfg.RPC), time.Now().UTC().Format(time.RFC3339))
	if len(a.gnos) > 0 {
		fmt.Fprintf(out, "gno.land is read from %d source(s): %s (the gno> lines; every source must agree)\n", len(a.gnos), a.gnoWhose())
		if ownSources(a.gnos) == 0 {
			// the machines' config is the operator's file: its nodes alone hold gno.land to the operator's word (E5-CF3
			// item 3, E4-TL1's threat by another door) — read, compared, failed on, never a PASS
			a.unverify("gno.land is read only from the operator's config (-config: bridge.json's gno.rpc and gno.sources): every gno.land reading below — the client's trust root, r/hlgov's counter, seats and decisions, the routes — rests on the operator's nodes (E5-CF3 item 3). Give -gno-rpc, a gno node of your own: the config's are then read beside it, every reading compared")
		}
	} else {
		fmt.Fprintln(out, "gno.land is read from no source (-gno-rpc): the client's trust root cannot be held to gno.land, so the verdict cannot be PASS")
	}
	if a.cn, err = dial(ctx, cfg.RPC, out); err != nil {
		return nil, err
	}
	if err := a.pin(ctx); err != nil {
		return nil, err
	}
	a.bindings()
	a.section("the launch values the deployment is held to, each from a source independent of the deployer where one exists (E3-GS4)")
	a.expectations(ctx)
	a.run(ctx)
	warned := ""
	if len(a.warns) > 0 {
		warned = fmt.Sprintf(" (%d WARN line(s) above to read)", len(a.warns))
	}
	switch {
	case len(a.fails) > 0 && len(a.unverified) > 0:
		fmt.Fprintf(out, "\nhleth audit: FAIL (%d), and %d UNVERIFIED%s\n", len(a.fails), len(a.unverified), warned)
	case len(a.fails) > 0:
		fmt.Fprintf(out, "\nhleth audit: FAIL (%d)%s\n", len(a.fails), warned)
	case len(a.unverified) > 0:
		fmt.Fprintf(out, "\nhleth audit: UNVERIFIED (%d) — no failure, but not a PASS: the deployment is held to the deployer's own record for:%s\n", len(a.unverified), warned)
		for _, u := range a.unverified {
			fmt.Fprintln(out, "  - "+u)
		}
	default:
		fmt.Fprintln(out, "\nhleth audit: PASS"+warned)
	}
	return &auditResult{Fails: a.fails, Unverified: a.unverified}, nil
}

// pin fixes the block every read is made at.
func (a *audit) pin(ctx context.Context) error {
	at := ethx.Finalized
	switch a.cfg.At {
	case "", "finalized":
	case "latest":
		at = ethx.Latest
	case "safe":
		at = ethx.Safe
	default:
		n, err := strconv.ParseUint(a.cfg.At, 10, 64)
		if err != nil {
			return fmt.Errorf("-at %q: latest, finalized, safe or a block number", a.cfg.At)
		}
		at = ethx.AtNumber(n)
	}
	b, err := a.cn.c.BlockAt(ctx, at)
	if err != nil {
		return fmt.Errorf("the block to read at (%s): %w", at, err)
	}
	a.at, a.atNum, a.atTime = ethx.AtNumber(b.Number), b.Number, int64(b.Time)
	fmt.Fprintf(a.out, "every reading below is at block %d (%s, the %s block; time %s)\n", b.Number, b.Hash.Hex(), at, time.Unix(int64(b.Time), 0).UTC().Format(time.RFC3339))
	if b.Number < a.d.historyFrom() || b.Number < a.d.DeployBlock {
		return fmt.Errorf("block %d is before the deployment's first block %d or its finishSetup's %d (read at -at latest right after the setup, or wait for finality)", b.Number, a.d.historyFrom(), a.d.DeployBlock)
	}
	return nil
}

// bindings are the values the deployment must be bound to: the flags' when given, else the record's — UNVERIFIED
// (E3-GS4): nothing on chain names the bridge's namespace, and a deployment bound to realms of the deployer's own
// agrees with its own record everywhere.
func (a *audit) bindings() {
	l := a.d.Launch
	pick := func(flag, rec, what string) string {
		if flag != "" {
			if rec != "" && rec != flag {
				fmt.Fprintf(a.out, "     NOTE the record's %s is %s; the flag's %s is audited\n", what, rec, flag)
			}
			return flag
		}
		a.unverify("%s: %s, from the deployment's record — pass the flag to hold the deployment to your own reading of the bridge's realms", what, rec)
		return rec
	}
	a.pkgPath = pick(a.cfg.MailboxPkgPath, l.MailboxPkgPath, "the hlmailbox path (-mailbox-pkgpath)")
	a.hlgovPath = pick(a.cfg.Hlgov, l.Hlgov, "r/hlgov's path (-hlgov)")
	a.hlgov32 = realm32(a.hlgovPath)
	a.routePath[0] = pick(a.cfg.GnotRoute, l.GnotRoute, "the GNOT route's path (-gnot-route)")
	a.routePath[1] = pick(a.cfg.BubbleRoute, l.BubbleRoute, "the BUBBLE route's path (-bubble-route)")
	a.route[0], a.route[1] = realm32(a.routePath[0]), realm32(a.routePath[1])
	a.mailboxPkgPathHash = crypto.Keccak256Hash([]byte(a.pkgPath))
}

// addr reads an address view.
func (a *audit) addr(ctx context.Context, at common.Address, art *Artifact, method string, args ...any) (common.Address, bool) {
	v, err := read[common.Address](ctx, a.cn, a.at, at, art, method, args...)
	if err != nil {
		a.fail(art.Name+"."+method, err)
		return common.Address{}, false
	}
	return v, true
}

// is checks an address view against the address it must be.
func (a *audit) is(ctx context.Context, what string, at common.Address, art *Artifact, method string, want common.Address, wantWhat string, args ...any) {
	v, ok := a.addr(ctx, at, art, method, args...)
	if ok {
		a.check(v == want, "%s is %s (must be %s, %s)", what, v.Hex(), want.Hex(), wantWhat)
	}
}

func (a *audit) run(ctx context.Context) {
	d := a.d
	A := func(n string) common.Address { return d.Addr(n) }
	admin := A("hlAdmin")
	ar := a.arts

	a.section("the chain and the record")
	a.check(a.cn.chainID.Uint64() == d.ChainID, "chain id %s (the deployment's %d)", a.cn.chainID, d.ChainID)
	for _, f := range contractFields {
		if A(f) == (common.Address{}) {
			a.check(false, "the record names no %s: the setup did not finish", f)
		}
	}
	if len(a.fails) > 0 {
		return
	}
	a.note("Ethereum's domain %d, gno.land's %d, the first block %d, finishSetup's block %d (eth.deployBlock: every machine's scan starts there), the deployer %s", d.LocalDomain, d.GnoDomain, d.historyFrom(), d.DeployBlock, d.Deployer)
	if bal, err := a.cn.c.BalanceAt(ctx, common.HexToAddress(d.Deployer), a.at); err == nil {
		a.note("the deployer holds %s wei (it funds nothing more: it holds no authority after the setup)", bal)
	}

	a.section("the governed changes (E3-CF9): the HlAdmin line, its decisions' ends from its logs, each executed decision's parameters held to r/hlgov's — every value they moved is held below to the decision that moved it")
	a.readLine(ctx)
	a.lineSection()
	f := a.line
	live, holder := f.Live, "HlAdmin"
	if live != admin {
		holder = "the live HlAdmin of the line, " + live.Hex()
	}
	for i, ad := range f.Admins[:len(f.Admins)-1] {
		x, next := f.Retired[ad], f.Admins[i+1]
		a.check(x != nil && common.BytesToAddress(x.Params[12:32]) == next, "HlAdmin %s's successor() is %s%s", ad.Hex(), next.Hex(), retiredBy(x, next))
	}

	a.section("owners and admins (§5.8 step 10: no human key holds an authority — every one is HlAdmin, or the successor a kind 9 handed every one to)")
	a.is(ctx, "the ProxyAdmin's owner", A("proxyAdmin"), ar["ProxyAdmin"], "owner", live, holder)
	a.is(ctx, "the mailbox's owner", A("mailbox"), ar["Mailbox"], "owner", live, holder)
	a.is(ctx, "token 0's owner", A("token0"), ar["HypERC20"], "owner", live, holder)
	a.is(ctx, "token 1's owner", A("token1"), ar["HypERC20"], "owner", live, holder)
	a.is(ctx, "the gate's owner", A("gate"), ar["PausableGate"], "owner", live, holder)
	a.is(ctx, "the MerkleTreeHook's owner", A("merkleTreeHook"), ar["MerkleTreeHook"], "owner", live, holder)
	a.is(ctx, "the ProtocolFee's owner", A("protocolFee"), ar["ProtocolFee"], "owner", live, holder)
	a.is(ctx, "the client's admin", A("client"), ar["GnoLightClient"], "admin", live, holder)

	a.section("the setup's history (the code audit's E2-CT1, M2-NOTES.md Fold F4): the nonce plan, the ProxyAdmin's owners, the proxies' upgrades, the setup done once at eth.deployBlock, every dispatch before it listed")
	a.history(ctx)

	a.section("the creation transactions (the plan red team's E2-RT1): each of the deployer's fifteen creations' input against this tree's (and Hyperlane's release's) creation code and the constructor's arguments, byte for byte")
	a.creations(ctx)

	a.section("the five proxies' EIP-1967 slots (eth_getStorageAt): the admin the ProxyAdmin, the implementation the recorded one — or the one an executed kind 8 put there")
	for _, p := range [][2]string{{"mailbox", "mailboxImpl"}, {"token0", "token0Impl"}, {"token1", "token1Impl"}, {"client", "clientImpl"}, {"gate", "gateImpl"}} {
		impl, err := a.cn.c.StorageAt(ctx, A(p[0]), ethx.SlotImplementation, a.at)
		if err != nil {
			a.fail(p[0]+"'s implementation slot", err)
			continue
		}
		want, what := f.Impl[p[0]], "its "+p[1]
		if x := f.Why["impl "+p[0]]; x != nil {
			what = "the implementation " + x.String() + " upgraded it to — its code hash the decision's, its code held to no build here (hlwatch held it before the vote); the launch's was " + A(p[1]).Hex()
		}
		a.check(common.BytesToAddress(impl[12:]) == want && isZero(impl[:12]), "%s's implementation slot holds %s (must be %s, %s)", p[0], common.BytesToAddress(impl[12:]).Hex(), want.Hex(), what)
		if x := f.Why["impl "+p[0]]; x != nil && len(x.Params) >= 96 {
			if code, err := a.cn.code(ctx, a.at, want); err != nil {
				a.fail(p[0]+"'s implementation's code", err)
			} else {
				a.check(crypto.Keccak256Hash(code) == common.BytesToHash(x.Params[64:96]), "%s's implementation %s holds the code %s pinned (its EXTCODEHASH %s)", p[0], want.Hex(), x, crypto.Keccak256Hash(code).Hex())
			}
		}
		adm, err := a.cn.c.StorageAt(ctx, A(p[0]), ethx.SlotAdmin, a.at)
		if err != nil {
			a.fail(p[0]+"'s admin slot", err)
			continue
		}
		a.check(common.BytesToAddress(adm[12:]) == A("proxyAdmin") && isZero(adm[:12]), "%s's admin slot holds %s (must be the ProxyAdmin %s)", p[0], common.BytesToAddress(adm[12:]).Hex(), A("proxyAdmin").Hex())
	}

	a.section("the mailbox")
	mb := ar["Mailbox"]
	if v, err := read[uint32](ctx, a.cn, a.at, A("mailbox"), mb, "localDomain"); err != nil {
		a.fail("the mailbox's localDomain", err)
	} else {
		a.check(v == d.LocalDomain, "the mailbox's localDomain is %d (must be Ethereum's domain %d)", v, d.LocalDomain)
	}
	a.is(ctx, "the mailbox's default ISM", A("mailbox"), mb, "defaultIsm", A("gate"), "the gate: never a raw client, so a token whose pointer is unset still goes through it")
	a.is(ctx, "the mailbox's default hook", A("mailbox"), mb, "defaultHook", A("merkleTreeHook"), "the MerkleTreeHook")
	a.is(ctx, "the mailbox's required hook", A("mailbox"), mb, "requiredHook", A("protocolFee"), "the ProtocolFee")
	if n, err := read[uint32](ctx, a.cn, a.at, A("mailbox"), mb, "nonce"); err == nil {
		a.note("the mailbox has dispatched %d messages", n)
	}

	a.section("the two tokens (HypERC20 behind their proxies)")
	hy := ar["HypERC20"]
	names := [2][2]string{{d.Launch.Name0, d.Launch.Symbol0}, {d.Launch.Name1, d.Launch.Symbol1}}
	for i := 0; i < 2; i++ {
		t := A(fmt.Sprintf("token%d", i))
		w := fmt.Sprintf("token %d", i)
		a.is(ctx, w+"'s mailbox()", t, hy, "mailbox", A("mailbox"), "ours: a token built over a stand-in mailbox could be minted by it directly, past the gate and the client")
		ismWhat := "the gate"
		if x := f.Why[fmt.Sprintf("ism %d", i)]; x != nil {
			ismWhat = "what " + x.String() + " set (the gate, or zero: the mailbox's default, the gate)"
		}
		a.is(ctx, w+"'s ISM", t, hy, "interchainSecurityModule", f.ISM[i], ismWhat)
		a.is(ctx, w+"'s hook", t, hy, "hook", common.Address{}, "none: the mailbox's default")
		a.is(ctx, w+"'s fee recipient", t, hy, "feeRecipient", common.Address{}, "none: no mint but a delivery's")
		a.is(ctx, w+"'s fee hook", t, hy, "feeHook", common.Address{}, "none")
		if r, err := read[[32]byte](ctx, a.cn, a.at, t, hy, "routers", d.GnoDomain); err != nil {
			a.fail(w+"'s router", err)
		} else {
			want := f.Routers[i][d.GnoDomain]
			a.check(r == want, "%s's router for gno.land's domain %d is 0x%x (must be 0x%x, its route realm's%s)", w, d.GnoDomain, r, want, f.explained(fmt.Sprintf("router %d %d", i, d.GnoDomain)))
		}
		if ds, err := read[[]uint32](ctx, a.cn, a.at, t, hy, "domains"); err != nil {
			a.fail(w+"'s enrolled domains", err)
		} else {
			want := map[uint32]bool{}
			for dom := range f.Routers[i] {
				want[dom] = true
			}
			same := len(ds) == len(want)
			for _, dom := range ds {
				same = same && want[dom]
			}
			a.check(same, "%s's enrolled domains are %v (must be %v: gno.land's %d alone at launch, each kind 6 enrolling or unenrolling one%s)", w, ds, sortedDomains(want), d.GnoDomain, f.explained(fmt.Sprintf("domains %d", i)))
			for dom, r := range f.Routers[i] {
				if dom == d.GnoDomain {
					continue
				}
				if got, err := read[[32]byte](ctx, a.cn, a.at, t, hy, "routers", dom); err != nil {
					a.fail(fmt.Sprintf("%s's router for domain %d", w, dom), err)
				} else {
					a.check(got == r, "%s's router for domain %d is 0x%x (must be 0x%x%s)", w, dom, got, r, f.explained(fmt.Sprintf("router %d %d", i, dom)))
				}
			}
		}
		if v, err := read[uint8](ctx, a.cn, a.at, t, hy, "decimals"); err != nil {
			a.fail(w+"'s decimals", err)
		} else {
			a.check(v == 6, "%s's decimals are %d (must be 6, gno.land's)", w, v)
		}
		num, err1 := read[*big.Int](ctx, a.cn, a.at, t, hy, "scaleNumerator")
		den, err2 := read[*big.Int](ctx, a.cn, a.at, t, hy, "scaleDenominator")
		if err := errors.Join(err1, err2); err != nil {
			a.fail(w+"'s scale", err)
		} else {
			a.check(num.Cmp(big.NewInt(1)) == 0 && den.Cmp(big.NewInt(1)) == 0, "%s's scale is %s/%s (must be 1/1)", w, num, den)
		}
		if v, err := read[uint32](ctx, a.cn, a.at, t, hy, "localDomain"); err != nil {
			a.fail(w+"'s localDomain", err)
		} else {
			a.check(v == d.LocalDomain, "%s's localDomain is %d (must be %d)", w, v, d.LocalDomain)
		}
		name, err1 := read[string](ctx, a.cn, a.at, t, hy, "name")
		sym, err2 := read[string](ctx, a.cn, a.at, t, hy, "symbol")
		if err := errors.Join(err1, err2); err != nil {
			a.fail(w+"'s name and symbol", err)
		} else {
			a.check(name == names[i][0] && sym == names[i][1], "%s is %q (%s) (the launch's %q (%s))", w, name, sym, names[i][0], names[i][1])
		}
		if s, err := read[*big.Int](ctx, a.cn, a.at, t, hy, "totalSupply"); err == nil {
			a.note("%s's supply is %s base units (0 at the setup; minted only by deliveries from gno.land)", w, s)
		}
	}

	a.section("the gate (PausableGate behind its proxy)")
	gt := ar["PausableGate"]
	a.is(ctx, "the gate's mailbox", A("gate"), gt, "mailbox", A("mailbox"), "ours")
	a.is(ctx, "the gate's client", A("gate"), gt, "client", A("client"), "ours")
	a.is(ctx, "the gate's token 0", A("gate"), gt, "token", A("token0"), "ours", uint8(0))
	a.is(ctx, "the gate's token 1", A("gate"), gt, "token", A("token1"), "ours", uint8(1))
	if ks, err := read[[6]common.Address](ctx, a.cn, a.at, A("gate"), gt, "keys"); err != nil {
		a.fail("the gate's seats", err)
	} else {
		a.check(ks == f.Seats, "the gate's seats 1–4 and watch slots 5–6 are %s (must be %s: the launch's%s)", addrList(ks[:]), addrList(f.Seats[:]), f.seatsExplained())
	}
	a.seatChecks(ctx)
	a.factor(ctx, "the gate", A("gate"), gt)
	routes := [2]*gnoside.Route{}
	if len(a.gnos) > 0 {
		for i := 0; i < 2; i++ {
			r, err := a.routeRead(ctx, a.routePath[i])
			switch {
			case err != nil:
				a.fail(fmt.Sprintf("the route %s's json (its per-message maximum)", a.routePath[i]), err)
			case r == nil:
				a.note("the route %s is not on the chain yet: its per-message maximum is held to the gate's once it is (the realms go up after this audit, §5.8)", a.routePath[i])
			default:
				routes[i] = r
			}
		}
	}
	for i := 0; i < 2; i++ {
		l, err := read[ethx.MintLimit](ctx, a.cn, a.at, A("gate"), gt, "limits", uint8(i))
		if err != nil {
			a.fail(fmt.Sprintf("the gate's limits for token %d", i), err)
			continue
		}
		want := f.Limits[i]
		a.check(l.MaxPerMessage == want[0] && l.DayCap == want[1], "the gate's limits for token %d: %d a message, %d a day (must be %d and %d: the launch's%s), the bucket holds %d", i, l.MaxPerMessage, l.DayCap, want[0], want[1], f.explained(fmt.Sprintf("limits %d", i)), l.Used)
		if lock, from := a.expectedLock(); lock[i] != 0 {
			a.note("token %d's day cap %d beside the expected lock %d it was compiled from (%s): %.1f%% of it (§5.4: a tenth at most, 100,000 at most)", i, l.DayCap, lock[i], from[i], 100*float64(l.DayCap)/float64(lock[i]))
		}
		if r := routes[i]; r != nil {
			a.check(int64(l.MaxPerMessage) >= r.Maximum, "the gate's maximum for token %d, %d a message, is at or above its route %s's maximum %d (§5.4: a send between the two is taken on gno.land and parked OverMax on Ethereum)", i, l.MaxPerMessage, a.routePath[i], r.Maximum)
			if r.Lock > 0 {
				a.note("token %d's day cap %d beside the lock on gno.land as it stands, %d: %.1f%% of it (the monitors alarm mint:cap-ratio above a fifth)", i, l.DayCap, r.Lock, 100*float64(l.DayCap)/float64(r.Lock))
			}
		}
	}
	if fl, err := read[[6]ethx.Flag](ctx, a.cn, a.at, A("gate"), gt, "flags"); err == nil {
		a.note("the gate's flags: %s", flagList(fl))
	}

	a.section("the light client (GnoLightClient behind its proxy)")
	gc, client := ar["GnoLightClient"], A("client")
	if v, err := read[string](ctx, a.cn, a.at, client, gc, "chainId"); err != nil {
		a.fail("the client's chain id", err)
	} else {
		a.check(v == d.Launch.GnoChainID, "the client's chain id is %q (the launch's %q)", v, d.Launch.GnoChainID)
	}
	if v, err := read[uint32](ctx, a.cn, a.at, client, gc, "originDomain"); err != nil {
		a.fail("the client's origin domain", err)
	} else {
		a.check(v == d.GnoDomain, "the client's origin domain is %d (must be gno.land's %d)", v, d.GnoDomain)
	}
	if v, err := read[[32]byte](ctx, a.cn, a.at, client, gc, "mailboxPkgPathHash"); err != nil {
		a.fail("the client's hlmailbox path hash", err)
	} else {
		a.check(common.Hash(v) == a.mailboxPkgPathHash, "the client's hlmailbox path hash is %x (must be keccak256(%q) = %x)", v, a.pkgPath, a.mailboxPkgPathHash)
	}
	a.factor(ctx, "the client", client, gc)
	a.trusted(ctx, client, gc)

	a.section("the Ed25519 verifier the client calls (step 4: plain code, no owner, no storage; M2-NOTES.md §1 A–E)")
	a.verifier(ctx, client, gc, admin, ar["HlAdmin"])

	a.section("HlAdmin (plain code: immutable from its deployment)")
	ha := ar["HlAdmin"]
	if v, err := read[bool](ctx, a.cn, a.at, admin, ha, "setupDone"); err == nil {
		a.note("HlAdmin's setupDone() is %v — storage an initcode can plant, so proof of nothing: the setup's history above holds its one SetupDone to a call of finishSetup and runs finishSetup again (M2-NOTES.md Fold F5, F5.1 item 13)", v)
	}
	if v, err := read[uint32](ctx, a.cn, a.at, admin, ha, "origin"); err != nil {
		a.fail("HlAdmin's origin", err)
	} else {
		a.check(v == d.GnoDomain, "HlAdmin's origin is %d (must be gno.land's %d)", v, d.GnoDomain)
	}
	if v, err := read[uint32](ctx, a.cn, a.at, admin, ha, "localDomain"); err != nil {
		a.fail("HlAdmin's localDomain", err)
	} else {
		a.check(v == d.LocalDomain, "HlAdmin's localDomain() is %d (must be Ethereum's domain %d: finishSetup holds the mailbox's and both tokens' to it)", v, d.LocalDomain)
	}
	if v, err := read[[32]byte](ctx, a.cn, a.at, admin, ha, "hlgov"); err != nil {
		a.fail("HlAdmin's hlgov", err)
	} else {
		a.check(v == a.hlgov32, "HlAdmin's hlgov() is 0x%x (must be r/hlgov's address %s as 32 bytes, 0x%x: the one sender it obeys)", v, a.hlgovPath, a.hlgov32)
	}
	for i := 0; i < 2; i++ {
		if v, err := read[[32]byte](ctx, a.cn, a.at, admin, ha, "route", uint8(i)); err != nil {
			a.fail(fmt.Sprintf("HlAdmin's route %d", i), err)
		} else {
			a.check(v == a.route[i], "HlAdmin's route(%d) is 0x%x (must be 0x%x)", i, v, a.route[i])
		}
	}
	for _, x := range []struct {
		method string
		args   []any
		field  string
	}{{"mailbox", nil, "mailbox"}, {"client", nil, "client"}, {"gate", nil, "gate"}, {"token", []any{uint8(0)}, "token0"}, {"token", []any{uint8(1)}, "token1"},
		{"proxyAdmin", nil, "proxyAdmin"}, {"merkleTreeHook", nil, "merkleTreeHook"}, {"protocolFee", nil, "protocolFee"}, {"exec", nil, "hlAdminExec"}} {
		a.is(ctx, "HlAdmin's "+x.method+"()", admin, ha, x.method, A(x.field), "the deployment's "+x.field, x.args...)
	}
	if len(f.Admins) > 1 {
		// held to the kind 9 that named it with the line, above
		a.note("HlAdmin's successor is %s: retired%s", f.Admins[1].Hex(), retiredBy(f.Retired[admin], f.Admins[1]))
	} else {
		a.is(ctx, "HlAdmin's successor", admin, ha, "successor", common.Address{}, "none: not retired")
	}
	a.is(ctx, "HlAdmin's predecessor", admin, ha, "predecessor", common.Address{}, "none: a launch HlAdmin")
	if ks, err := read[[6]common.Address](ctx, a.cn, a.at, admin, ha, "keys"); err != nil {
		a.fail("HlAdmin's seats", err)
	} else {
		a.check(ks == f.AdminSeats, "HlAdmin's seats and watch slots are %s (must be %s: the launch's%s)", addrList(ks[:]), addrList(f.AdminSeats[:]), f.seatsExplained())
	}
	a.factor(ctx, "HlAdmin", admin, ha)
	a.counter(ctx, admin, ha)
	a.recordExplained(ctx, admin, ha)

	a.section("the hooks")
	mth, pf := ar["MerkleTreeHook"], ar["ProtocolFee"]
	a.is(ctx, "the MerkleTreeHook's mailbox", A("merkleTreeHook"), mth, "mailbox", A("mailbox"), "ours")
	if n, err := read[uint32](ctx, a.cn, a.at, A("merkleTreeHook"), mth, "count"); err == nil {
		a.note("the MerkleTreeHook holds %d ids", n)
	}
	maxFee, _ := new(big.Int).SetString(d.Launch.MaxProtocolFee, 10)
	if maxFee == nil {
		maxFee = defaultMaxProtocolFee
	}
	if v, err := read[*big.Int](ctx, a.cn, a.at, A("protocolFee"), pf, "MAX_PROTOCOL_FEE"); err != nil {
		a.fail("the ProtocolFee's maximum", err)
	} else {
		a.check(v.Cmp(defaultMaxProtocolFee) == 0, "the ProtocolFee's immutable maximum is %s wei (must be 0.001 ETH, %s: HlAdmin's compiled bound for kind 7)", v, defaultMaxProtocolFee)
	}
	if v, err := read[*big.Int](ctx, a.cn, a.at, A("protocolFee"), pf, "protocolFee"); err != nil {
		a.fail("the protocol fee", err)
	} else {
		a.check(v.IsUint64() && v.Uint64() == f.Fee, "the protocol fee is %s wei (must be %d: 0 at launch%s)", v, f.Fee, f.explained("fee"))
	}
	benWhat := "the treasurer's (the launch's)"
	if x := f.Why["beneficiary"]; x != nil {
		benWhat = "the one " + x.String() + " named"
	}
	a.is(ctx, "the ProtocolFee's beneficiary", A("protocolFee"), pf, "beneficiary", f.Beneficiary, benWhat)

	a.section("the initializers: our implementations' closed by their constructors, and every one of the five proxies initialized in its construction (§5.8; E2-CT1), so no stranger initializes one")
	one := func(n byte) common.Address { return common.BytesToAddress([]byte{0xa0, n}) }
	leaves := make([][]byte, 16)
	for i := range leaves {
		leaves[i] = []byte{0x0a, 0x01, byte(i)}
	}
	clientInit := Values{"chainid": "x", "origindomain": uint32(1), "headerleaves": leaves, "validators": []Values{{"pubkey": [32]byte{2}, "power": int64(1)}}, "admin": one(1)}
	gateInit := Values{"owner": one(1), "mailbox": one(2), "client": one(3), "tokens": [2]common.Address{one(4), one(5)},
		"seats": [4]common.Address{one(6), one(7), one(8), one(9)}, "watch": [2]common.Address{one(10), one(11)},
		"maxpermessage": [2]uint64{1, 1}, "daycap": [2]uint64{1, 1}}
	a.closedInit(ctx, "clientImpl", gc, clientInit)
	a.closedInit(ctx, "gateImpl", gt, gateInit)
	a.note("Hyperlane's implementations (the mailbox's, the tokens') keep their initializers open, as the release builds them: they are only ever used through their proxies, initialized in their construction — which each proxy's answer below shows")
	a.closedInit(ctx, "mailbox", ar["Mailbox"], Values{"owner": one(1), "defaultism": one(2), "defaulthook": one(3), "requiredhook": one(4)})
	for _, tk := range []string{"token0", "token1"} {
		a.closedInit(ctx, tk, ar["HypERC20"], Values{"totalsupply": new(big.Int), "name": "x", "symbol": "x", "hook": one(1), "interchainsecuritymodule": one(2), "owner": one(3)})
	}
	a.closedInit(ctx, "client", gc, clientInit)
	a.closedInit(ctx, "gate", gt, gateInit)

	a.section("the code: each contract's runtime code against its reproducible build, the immutables masked and read")
	a.code(ctx, maxFee)
}

// closedInit checks an implementation refuses its initializer (a simulated call from a stranger reverts
// "Initializable: contract is already initialized").
func (a *audit) closedInit(ctx context.Context, field string, art *Artifact, vals Values) {
	data, err := art.CallData("initialize", vals)
	if err != nil {
		a.fail(field+"'s initializer", err)
		return
	}
	_, err = a.cn.c.CallFrom(ctx, common.HexToAddress("0x000000000000000000000000000000000000c0de"), a.d.Addr(field), data, nil, a.at)
	r, reverted := ethx.AsRevert(err)
	switch {
	case err == nil:
		a.check(false, "%s's initialize answers a stranger: its initializers are open (anyone could initialize the implementation)", field)
	case !reverted:
		a.fail(field+"'s initializer", err)
	default:
		a.check(strings.Contains(r.Reason+r.Message, "initialized"), "%s refuses initialize from a stranger (%s)", field, strings.TrimSpace(r.Error()))
	}
}

// verifier holds the Ed25519 verifier three ways: the client's ED25519() and HlAdmin's ed25519() are the recorded
// verifier; its code is no EIP-7702 designator, and its EXTCODEHASH is this tree's build's and HlAdmin's
// ed25519CodeHash() (finishSetup's checks 27 and 28, read again by a member).
func (a *audit) verifier(ctx context.Context, client common.Address, gc *Artifact, admin common.Address, ha *Artifact) {
	v := a.d.Addr("ed25519Verifier")
	if x := a.line.Why["impl client"]; x != nil {
		// a kind 8 on the client (a verifier fix, CF5's case) runs another implementation, whose ED25519 is its own
		// immutable: said, and held to hold code; the recorded verifier is still held below (HlAdmin pins its hash)
		if got, ok := a.addr(ctx, client, gc, "ED25519"); ok {
			code, err := a.cn.code(ctx, a.at, got)
			a.check(err == nil && len(code) > 0 && !bytes.HasPrefix(code, []byte{0xef, 0x01, 0x00}), "the client's ED25519() is %s, the verifier of the implementation %s upgraded the client to (the launch's was %s): it holds code (%d bytes, keccak256 %s), not held to a build here", got.Hex(), x, v.Hex(), len(code), crypto.Keccak256Hash(code).Hex())
		}
	} else {
		a.is(ctx, "the client's ED25519()", client, gc, "ED25519", v, "the recorded Ed25519Verifier (an immutable of the client's implementation)")
	}
	a.is(ctx, "HlAdmin's ed25519()", admin, ha, "ed25519", v, "the recorded Ed25519Verifier, the client's ED25519()")
	code, err := a.cn.code(ctx, a.at, v)
	if err != nil {
		a.fail("the verifier's code", err)
		return
	}
	got := crypto.Keccak256Hash(code)
	build := a.arts.Ed25519CodeHash()
	a.check(len(code) > 0 && !(len(code) >= 3 && code[0] == 0xef && code[1] == 0x01 && code[2] == 0x00), "the verifier %s holds code (%d bytes), not an EIP-7702 delegation designator", v.Hex(), len(code))
	a.check(got == build, "the verifier's EXTCODEHASH %s is this tree's build of Ed25519Verifier, %s", got.Hex(), build.Hex())
	if pinned, err := read[[32]byte](ctx, a.cn, a.at, admin, ha, "ed25519CodeHash"); err != nil {
		a.fail("HlAdmin's ed25519CodeHash", err)
	} else {
		a.check(common.Hash(pinned) == got, "HlAdmin's ed25519CodeHash() %s is the verifier's EXTCODEHASH %s (and the build's %s)", common.Hash(pinned).Hex(), got.Hex(), build.Hex())
	}
}

// logsOf are a contract's logs of one event from the deployment's first block to the audited one.
func (a *audit) logsOf(ctx context.Context, addr common.Address, topic common.Hash) ([]types.Log, error) {
	return a.cn.c.LogsRange(ctx, ethx.LogQuery{Addresses: []common.Address{addr}, Topics: [][]common.Hash{{topic}}}, a.d.historyFrom(), a.atNum)
}

// history reads what no state shows, only the logs (the code audit's E2-CT1; M2-NOTES.md Fold F4, item 8): the
// contracts at the nonce plan's CREATE addresses, HlAdmin at the address the ProxyAdmin went to; the ProxyAdmin's two
// owners exactly — the deployer at its creation, HlAdmin's address in the deployer's next transaction, before any
// proxy existed; each proxy's one Upgraded and one AdminChanged, both of its construction; the client's first record
// the launch's header (and the node's, with -gno-rpc); no Dispatch and no Process of the mailbox before SetupDone. A
// deployer that ran other code than the setup could occupy HlAdmin's address, upgrade a proxy through it and hand the
// ProxyAdmin on to a HlAdmin made later: HlAdmin's constructor and finishSetup pass that, these refuse it.
func (a *audit) history(ctx context.Context) {
	d := a.d
	A := d.Addr
	deployer := common.HexToAddress(d.Deployer)
	if d.AdminAt == "" {
		a.check(false, "the record names no nonce plan (a setup of an earlier build): the ProxyAdmin's history cannot be held to one")
		return
	}
	fields := make([]string, 0, len(planIndex))
	for f := range planIndex {
		fields = append(fields, f)
	}
	sort.Slice(fields, func(i, k int) bool { return planIndex[fields[i]] < planIndex[fields[k]] })
	for _, f := range fields {
		n := d.FirstNonce + planIndex[f]
		want := crypto.CreateAddress(deployer, n)
		a.check(A(f) == want, "%s is %s, the deployer's CREATE at nonce %d (%s) by the nonce plan", f, A(f).Hex(), n, want.Hex())
	}
	admin := A("hlAdmin")
	a.check(admin == common.HexToAddress(d.AdminAt) && d.AdminNonce == d.FirstNonce+planHlAdmin, "HlAdmin is %s, the plan's address (the deployer's nonce %d = its first %d + %d)", admin.Hex(), d.AdminNonce, d.FirstNonce, planHlAdmin)
	a.check(A("hlAdminExec") == crypto.CreateAddress(admin, 1), "HlAdminExec is %s, HlAdmin's own CREATE at its nonce 1", A("hlAdminExec").Hex())

	// (a) the ProxyAdmin's owners: the deployer, HlAdmin, then each successor of the line in its predecessor's kind 9
	f := a.line
	created, handed := d.stepOf("proxyAdmin"), d.stepOf("proxyAdminHandOver")
	logs, err := a.logsOf(ctx, A("proxyAdmin"), ethx.ProxyAdmin.Event("OwnershipTransferred"))
	if err != nil {
		a.fail("the ProxyAdmin's OwnershipTransferred logs", err)
	} else {
		moves := 2 + len(f.Admins) - 1
		a.check(len(logs) == moves, "the ProxyAdmin's ownership moved %d time(s) since its creation (must be %d: the deployer at its creation, then HlAdmin's address%s)", len(logs), moves, map[bool]string{true: ", then each successor of the line in the kind 9 that named it", false: ""}[moves > 2])
		for i := 2; i < len(logs) && i-1 < len(f.Admins); i++ {
			from, to, x := f.Admins[i-2], f.Admins[i-1], f.Retired[f.Admins[i-2]]
			l := logs[i]
			a.check(x != nil && len(l.Topics) == 3 && l.Topics[1] == ethx.AddressTopic(from) && l.Topics[2] == ethx.AddressTopic(to) && l.TxHash == x.EndLog.TxHash,
				"the ProxyAdmin went from %s to its successor %s in the transaction of the kind 9 that named it%s", from.Hex(), to.Hex(), retiredBy(x, to))
		}
		if len(logs) >= 1 && created != nil {
			l := logs[0]
			a.check(len(l.Topics) == 3 && l.Topics[1] == (common.Hash{}) && l.Topics[2] == ethx.AddressTopic(deployer) && l.TxHash == common.HexToHash(created.Tx),
				"the ProxyAdmin's first owner is the deployer, in its creation transaction %s", created.Tx)
		}
		if len(logs) >= 2 && handed != nil {
			l := logs[1]
			a.check(len(l.Topics) == 3 && l.Topics[1] == ethx.AddressTopic(deployer) && l.Topics[2] == ethx.AddressTopic(admin) && l.TxHash == common.HexToHash(handed.Tx),
				"the ProxyAdmin went from the deployer to HlAdmin %s in the hand-over %s", admin.Hex(), handed.Tx)
			if tx, err := a.cn.c.Transaction(ctx, l.TxHash); err != nil {
				a.fail("the hand-over's transaction", err)
			} else {
				a.check(tx.From == deployer && tx.Nonce == d.FirstNonce+handOverIndex, "the hand-over is the deployer's transaction at nonce %d (the plan's %d: the next after the ProxyAdmin's, before the first proxy's at %d)", tx.Nonce, d.FirstNonce+handOverIndex, d.FirstNonce+planIndex["mailbox"])
			}
		} else if handed == nil {
			a.check(false, "the record names no hand-over of the ProxyAdmin")
		}
	}

	// (b) each proxy's upgrades and admin changes: its construction's alone
	for _, p := range []string{"mailbox", "token0", "token1", "client", "gate"} {
		st := d.stepOf(p)
		if st == nil || st.Tx == "" {
			a.check(false, "the record names no creation transaction of %s", p)
			continue
		}
		up, err1 := a.logsOf(ctx, A(p), ethx.Proxy.Event("Upgraded"))
		ad, err2 := a.logsOf(ctx, A(p), ethx.Proxy.Event("AdminChanged"))
		if err := errors.Join(err1, err2); err != nil {
			a.fail(p+"'s Upgraded and AdminChanged logs", err)
			continue
		}
		impl := A(p + "Impl")
		kind8 := f.Upgrades[p]
		upOK := len(up) == 1+len(kind8) && up[0].TxHash == common.HexToHash(st.Tx) && len(up[0].Topics) == 2 && up[0].Topics[1] == ethx.AddressTopic(impl)
		extra := ""
		if len(kind8) > 0 {
			extra = fmt.Sprintf(", then once in each of the %d kind 8(s) executed on it", len(kind8))
		}
		a.check(upOK, "%s was upgraded %d time(s): must be once, to %s, in its creation transaction %s%s", p, len(up), impl.Hex(), st.Tx, extra)
		for i, x := range kind8 {
			if i+1 >= len(up) {
				break
			}
			u, to := up[i+1], common.BytesToAddress(x.Params[44:64])
			a.check(u.TxHash == x.EndLog.TxHash && len(u.Topics) == 2 && u.Topics[1] == ethx.AddressTopic(to), "%s's upgrade %d, to %s, is %s's, in its transaction", p, i+2, to.Hex(), x)
		}
		// AdminChanged(previousAdmin, newAdmin), both unindexed (OpenZeppelin 4.9's ERC1967Upgrade)
		adOK := len(ad) == 1 && ad[0].TxHash == common.HexToHash(st.Tx) && len(ad[0].Data) == 64 && common.BytesToAddress(ad[0].Data[32:]) == A("proxyAdmin")
		a.check(adOK, "%s's admin changed %d time(s): must be once, to the ProxyAdmin %s, in its creation transaction", p, len(ad), A("proxyAdmin").Hex())
	}

	// (d) the client's first record: the launch's trusted header, gno.land's
	if logs, err := a.logsOf(ctx, A("client"), ethx.LightClient.Event("HeaderRecorded")); err != nil {
		a.fail("the client's HeaderRecorded logs", err)
	} else {
		var first *ethx.HeaderRecorded
		for _, l := range logs {
			if h, err := ethx.DecodeHeaderRecorded(l); err == nil && h.Record == 1 {
				first = h
				break
			}
		}
		want, _ := hl.ParseKey32(d.Launch.TrustedHash)
		switch {
		case first == nil:
			a.check(false, "the client's first record (HeaderRecorded, record 1) is not among its logs")
		default:
			a.check(first.Hash == want && first.Height == d.Launch.TrustedHeight, "the client's first record is header %d %x (the launch's %d %s)", first.Height, first.Hash, d.Launch.TrustedHeight, d.Launch.TrustedHash)
			if len(a.gnos) > 0 {
				if blk, err := a.gnoBlock(ctx, first.Height); err != nil {
					a.fail(fmt.Sprintf("gno.land's block %d", first.Height), err)
				} else {
					a.check(blk.Hash == first.Hash, "the client's first record is gno.land's block %d (%x)", first.Height, blk.Hash)
				}
			}
		}
	}

	// (e) the setup done once, at the record's eth.deployBlock (where every machine's scan starts), by finishSetup —
	// setupDone() is storage an initcode can plant, and HlAdmin's creation can log a SetupDone as HlAdmin: the one
	// SetupDone is in another transaction than HlAdmin's creation (after it HlAdmin's code is the build's, held below,
	// whose only SetupDone follows finishSetup's checks), and finishSetup passes again on that block's state with
	// setupDone false; then the launch line (M2-NOTES.md Fold F5, F5.1 items 12–13)
	done, err := a.logsOf(ctx, admin, ethx.Admin.Event("SetupDone"))
	if err != nil {
		a.fail("HlAdmin's SetupDone log", err)
		return
	}
	if !a.check2(len(done) == 1, "HlAdmin's setup was done once (%d SetupDone logs)", len(done)) {
		return
	}
	sd := done[0]
	a.check(sd.BlockNumber == d.DeployBlock, "HlAdmin's SetupDone is at block %d, the record's eth.deployBlock %d (where every scan of the signers, the monitors, the inbound relayer and hlwatch starts)", sd.BlockNumber, d.DeployBlock)
	a.setupDoneBy(ctx, admin, sd)
	a.rerunFinishSetup(ctx, admin, sd.BlockNumber)
	a.launchLine(ctx, admin, sd)
}

// setupDoneBy holds the SetupDone log to a call of finishSetup: not HlAdmin's creation transaction (whose initcode
// could log one as HlAdmin), and, as hleth setup sends it, a top-level call of finishSetup() on HlAdmin — anything
// else (a stranger's contract calling it: finishSetup is anyone's) is said, and fails nothing, since after its
// creation HlAdmin's code is the build's.
func (a *audit) setupDoneBy(ctx context.Context, admin common.Address, sd types.Log) {
	if st := a.d.stepOf("hlAdmin"); st != nil && common.HexToHash(st.Tx) == sd.TxHash {
		a.check(false, "HlAdmin's SetupDone was logged by its creation transaction %s: its initcode, not finishSetup — setupDone() planted", sd.TxHash.Hex())
		return
	}
	tx, err := a.cn.c.Transaction(ctx, sd.TxHash)
	if err != nil {
		a.fail("SetupDone's transaction "+sd.TxHash.Hex(), err)
		return
	}
	sel := ethx.Admin.ABI.Methods["finishSetup"].ID
	if tx.To != nil && *tx.To == admin && bytes.Equal(tx.Input, sel) {
		a.check(true, "HlAdmin's SetupDone is finishSetup's: logged by a top-level call of finishSetup() on HlAdmin (tx %s from %s), not by HlAdmin's creation", sd.TxHash.Hex(), tx.From.Hex())
		return
	}
	a.warn("HlAdmin's SetupDone was logged inside tx %s (from %s to %s), not by a top-level call of finishSetup(): finishSetup is anyone's, and after its creation HlAdmin's code is the build's (held below), whose only SetupDone follows finishSetup's checks — finishSetup is run again below", sd.TxHash.Hex(), tx.From.Hex(), toText(tx.To))
}

// hlAdminSlot is HlAdmin's ERC-7201 namespace (HlAdminStorage.SLOT): its first word packs lastSeq (bytes 24–31 of the
// big-endian word), startSeq (16–23), pending (12–15), setupDone (11), inherited (10) and launchNonce (6–9).
var hlAdminSlot = common.HexToHash("0xa549383900acf2d7f17f70f3a8e005874b124005220e85417ec5628fc6dcfa00")

const (
	wordSetupDone   = 11
	wordLaunchNonce = 6 // four bytes
	wordPending     = 12
	wordLastSeq     = 24
)

// rerunFinishSetup runs finishSetup again by eth_call on the state of SetupDone's block, HlAdmin's setupDone stored
// false (a state override of its first word): checks 1–31 and 34 must pass on the launch's own state, read by a member
// (M2-NOTES.md Fold F5, F5.1 item 13). The override's place is proved on the chain first: through it setupDone()
// reads false and nothing else of the word moves.
func (a *audit) rerunFinishSetup(ctx context.Context, admin common.Address, block uint64) {
	at := ethx.AtNumber(block)
	word, err := a.cn.c.StorageAt(ctx, admin, hlAdminSlot, at)
	if err != nil {
		a.fail(fmt.Sprintf("HlAdmin's first word at block %d (a provider that keeps that block's state: an archive node, or an audit within ~128 blocks of the setup)", block), err)
		return
	}
	if !a.check2(word[wordSetupDone] == 1, "HlAdmin's first word at block %d holds setupDone where HlAdminStorage puts it (byte %d: %d)", block, wordSetupDone, word[wordSetupDone]) {
		return
	}
	cleared := word
	cleared[wordSetupDone] = 0
	over := map[common.Address]map[common.Hash]common.Hash{admin: {hlAdminSlot: cleared}}
	from := common.HexToAddress(a.d.Deployer)
	data, _ := ethx.Admin.Pack("setupDone")
	if out, err := a.cn.c.CallOverride(ctx, from, admin, data, at, over); err != nil || len(out) != 32 || out[31] != 0 {
		a.check(false, "HlAdmin's setupDone() through the override reads %x (%v), not false: the override's place is not setupDone's", out, err)
		return
	}
	data, _ = ethx.Admin.Pack("finishSetup")
	_, err = a.cn.c.CallOverride(ctx, from, admin, data, at, over)
	if r, reverted := ethx.AsRevert(err); reverted {
		a.check(false, "finishSetup refuses again on block %d's state with setupDone false: %s (SetupIncomplete(n) names the check: HlAdminExec.checkSetup's table)", block, strings.TrimSpace(r.Error()))
		return
	} else if err != nil {
		a.fail(fmt.Sprintf("finishSetup again at block %d", block), err)
		return
	}
	a.check(true, "finishSetup passes again on block %d's state with setupDone stored false (eth_call, HlAdmin's first word overridden): checks 1–31 and 34 hold on the launch's own state", block)
}

// launchLine reads the launch line and lists every message below it (PLAN.md §5.6, §5.8 step 10; M2-NOTES.md Fold
// F5, F5.1 item 12): launchNonce() is the number of messages the mailbox dispatched before SetupDone, and each
// Dispatch numbered below it and each Process logged before SetupDone is judged by judgeEarly.
func (a *audit) launchLine(ctx context.Context, admin common.Address, sd types.Log) {
	A := a.d.Addr
	line, err := read[uint32](ctx, a.cn, a.at, admin, a.arts["HlAdmin"], "launchNonce")
	if err != nil {
		a.fail("HlAdmin's launchNonce()", err)
		return
	}
	var early []earlyLog
	before := 0
	for _, ev := range []string{"Dispatch", "Process"} {
		ls, err := a.logsOf(ctx, A("mailbox"), ethx.Mailbox.Event(ev))
		if err != nil {
			a.fail("the mailbox's "+ev+" logs", err)
			return
		}
		for _, l := range ls {
			if l.BlockNumber > sd.BlockNumber || (l.BlockNumber == sd.BlockNumber && l.Index > sd.Index) {
				continue
			}
			e := earlyLog{Event: ev, Block: l.BlockNumber, Index: l.Index, Tx: l.TxHash}
			if len(l.Topics) == 4 {
				e.Who = common.BytesToAddress(l.Topics[map[string]int{"Dispatch": 1, "Process": 3}[ev]][12:])
			}
			if ev == "Dispatch" {
				before++
				if vals, err := ethx.Mailbox.ABI.Events["Dispatch"].Inputs.NonIndexed().Unpack(l.Data); err == nil && len(vals) == 1 {
					if m, err := hl.ParseMessage(vals[0].([]byte)); err == nil {
						e.Nonce, e.Parsed = m.Nonce, true
						if len(m.Body) >= 64 {
							e.Amount = new(big.Int).SetBytes(m.Body[32:64])
						}
					}
				}
			}
			early = append(early, e)
		}
	}
	a.check(int(line) == before, "HlAdmin's launchNonce() is %d, the number of messages the mailbox dispatched before SetupDone (%d): the launch line — no machine counts a message below it as a burn", line, before)
	warns, fails := judgeEarly(early, line, [2]common.Address{A("token0"), A("token1")}, []common.Address{A("token0"), A("token1"), admin})
	for _, w := range warns {
		a.warn("%s", w)
	}
	for _, f := range fails {
		a.check(false, "%s", f)
	}
	if len(warns) == 0 && len(fails) == 0 {
		a.check(true, "the mailbox logged no Dispatch and no Process before the setup was done (block %d): nothing lies below the launch line %d", sd.BlockNumber, line)
	}
}

// earlyLog is one Dispatch or Process of the mailbox before SetupDone: its place, a Dispatch's sender and message
// (its nonce and, a token message's, its amount), a Process's recipient.
type earlyLog struct {
	Event  string
	Block  uint64
	Index  uint
	Tx     common.Hash
	Who    common.Address // a Dispatch's sender, a Process's recipient
	Nonce  uint32         // a Dispatch's message nonce
	Parsed bool           // the Dispatch's message was read
	Amount *big.Int       // a token message's amount; nil when the body is none
}

// judgeEarly sorts the mailbox's logs before SetupDone against the launch line (M2-NOTES.md Fold F5, F5.1 item 12):
// every one is listed. A failure only where no stranger can reach — a token's Dispatch of a positive amount below the
// line (a burn: something was minted before the launch, the deployer's through a stand-in ISM) and a Process delivered
// to the deployment (a token or HlAdmin: a mint or a decision before the realms exist) — or a Dispatch before SetupDone
// numbered at or above the line (the line does not draw what the logs show). Anything anyone can do is a warning, so
// no stranger fails the audit (the veto E2-RT6 found in finishSetup's checks 32–33): after step 9 anyone may dispatch
// — to any recipient, a token's transferRemote of 0 among them — and anyone may have the mailbox deliver to a
// recipient of their own through an ISM of their own. No machine counts any of them: each is below the line.
func judgeEarly(ls []earlyLog, line uint32, tokens [2]common.Address, ours []common.Address) (warns, fails []string) {
	isToken := func(a common.Address) bool { return a == tokens[0] || a == tokens[1] }
	isOurs := func(a common.Address) bool {
		for _, o := range ours {
			if a == o {
				return true
			}
		}
		return false
	}
	for _, l := range ls {
		at := fmt.Sprintf("block %d (tx %s, log %d)", l.Block, l.Tx.Hex(), l.Index)
		switch {
		case l.Event == "Process" && isOurs(l.Who):
			fails = append(fails, fmt.Sprintf("the mailbox delivered a message to %s (the deployment's) at %s, before the launch: a mint or a decision before the realms exist — through a stand-in ISM; a fresh deployment", l.Who.Hex(), at))
		case l.Event == "Process":
			warns = append(warns, fmt.Sprintf("the mailbox delivered a message to %s at %s, before the launch: a recipient of its own with an ISM of its own (anyone's), nothing of the deployment's", l.Who.Hex(), at))
		case !l.Parsed:
			fails = append(fails, fmt.Sprintf("the mailbox's Dispatch by %s at %s, before the launch, carries no message that reads", l.Who.Hex(), at))
		case l.Nonce >= line:
			fails = append(fails, fmt.Sprintf("the mailbox's Dispatch #%d by %s at %s precedes SetupDone yet is not below the launch line %d: the line does not draw what the logs show", l.Nonce, l.Who.Hex(), at, line))
		case isToken(l.Who) && l.Amount != nil && l.Amount.Sign() > 0:
			fails = append(fails, fmt.Sprintf("a token's burn below the launch line: #%d by %s at %s, %s base units — something was minted before the launch (the deployer's, through a stand-in ISM); no machine counts it, but the launch is not clean: a fresh deployment", l.Nonce, l.Who.Hex(), at, l.Amount))
		case isToken(l.Who):
			warns = append(warns, fmt.Sprintf("a token's dispatch of nothing below the launch line: #%d by %s at %s (anyone's transferRemote of 0 after step 9): no machine counts it", l.Nonce, l.Who.Hex(), at))
		default:
			warns = append(warns, fmt.Sprintf("a stranger's dispatch below the launch line: #%d by %s at %s (anyone may dispatch after step 9): no machine counts it", l.Nonce, l.Who.Hex(), at))
		}
	}
	return warns, fails
}

// creationFields are the deployer's fifteen creations, in the nonce plan's order (HlAdminExec is HlAdmin's).
var creationFields = []string{"proxyAdmin", "mailboxImpl", "mailbox", "merkleTreeHook", "protocolFee", "ed25519Verifier", "clientImpl", "client",
	"token0Impl", "token0", "token1Impl", "token1", "gateImpl", "gate", "hlAdmin"}

// creations holds each creation transaction to the build (E2-RT1): the deployer's top-level creation at its planned
// nonce, its receipt's contract the recorded address, its input this tree's (or Hyperlane's release's) creation code
// and the constructor's arguments the setup used — from the record, with the bindings the audit holds — byte for byte.
// A contract's runtime code says nothing of what its constructor wrote: an initcode could plant a decision record in
// HlAdmin, a header entry in the client, a balance in a token, and return the build's runtime exactly.
func (a *audit) creations(ctx context.Context) {
	d := a.d
	l := a.launch // the launch values held (audit_expect.go): the bindings, the start, the seats, the limits, the beneficiary
	deployer := common.HexToAddress(d.Deployer)
	for _, f := range creationFields {
		st := d.stepOf(f)
		if st == nil || st.Tx == "" {
			a.check(false, "the record names no creation transaction of %s", f)
			continue
		}
		h := common.HexToHash(st.Tx)
		tx, err := a.cn.c.Transaction(ctx, h)
		if err != nil {
			a.fail(f+"'s creation transaction "+st.Tx, err)
			continue
		}
		rc, err := a.cn.c.Receipt(ctx, h)
		if err != nil {
			a.fail(f+"'s creation receipt "+st.Tx, err)
			continue
		}
		nonce := d.FirstNonce + planIndex[f]
		a.check(tx.From == deployer && tx.Nonce == nonce && tx.To == nil, "%s's creation %s is the deployer's top-level creation at its planned nonce %d (from %s, nonce %d, to %s)", f, st.Tx, nonce, tx.From.Hex(), tx.Nonce, toText(tx.To))
		a.check(rc.Status == types.ReceiptStatusSuccessful && rc.ContractAddress == d.Addr(f), "%s's creation receipt created %s (must be the recorded %s), status %d", f, rc.ContractAddress.Hex(), d.Addr(f).Hex(), rc.Status)
		want, art, err := creationBytes(f, d, l, a.arts)
		if err != nil {
			a.check(false, "%s's creation input cannot be rebuilt from the record: %v", f, err)
			continue
		}
		if bytes.Equal(tx.Input, want) {
			a.check(true, "%s's creation input is the build of %s and the constructor's arguments the setup used, byte for byte (%d bytes: %d of code, %d of arguments)", f, art.Name, len(want), len(art.Creation), len(want)-len(art.Creation))
			continue
		}
		why := creationDiff(tx.Input, want, art)
		if len(a.launchDiffers) > 0 {
			// the member's launch values are what the creation inputs carry: a flag given as the value stands today
			// (a seat a kind 5 moved since) fails here, and says which (E4-TL6)
			why += fmt.Sprintf(" — the launch values given differ from the deployer's record in the %s: %s", strings.Join(a.launchDiffers, ", the "), launchFlags)
		}
		a.check(false, "%s's creation input is not the build of %s with the setup's arguments: %s", f, art.Name, why)
	}
	a.check(d.Addr("hlAdminExec") == crypto.CreateAddress(d.Addr("hlAdmin"), 1), "hlAdminExec %s is created by HlAdmin's constructor (HlAdmin's CREATE at its nonce 1) inside HlAdmin's creation transaction, whose input is held above", d.Addr("hlAdminExec").Hex())
}

// creationDiff says how a creation input differs from the one expected: the build's code, or which argument.
func creationDiff(got, want []byte, art *Artifact) string {
	g := repro.SplitCreation(got, art)
	if g.Diff != "" {
		return g.Diff
	}
	w := repro.SplitCreation(want, art)
	ins := art.ABI.Constructor.Inputs
	var parts []string
	for i, in := range ins {
		if i < len(g.Args) && i < len(w.Args) && !reflect.DeepEqual(g.Args[i], w.Args[i]) {
			parts = append(parts, fmt.Sprintf("its argument %s is %s, the setup's %s", in.Name, repro.ArgText(ins[i:i+1], g.Args[i:i+1]), repro.ArgText(ins[i:i+1], w.Args[i:i+1])))
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d bytes against the expected %d", len(got), len(want))
	}
	return strings.Join(parts, "; ")
}

func toText(a *common.Address) string {
	if a == nil {
		return "none (a creation)"
	}
	return a.Hex()
}

// check2 is check that answers whether it held.
func (a *audit) check2(ok bool, format string, args ...any) bool {
	a.check(ok, format, args...)
	return ok
}

// launchKeys are the launch's seats then watch slots.
func launchKeys(l Launch) [6]common.Address {
	var out [6]common.Address
	for i, s := range l.Seats {
		out[i] = common.HexToAddress(s)
	}
	for i, s := range l.Watch {
		out[4+i] = common.HexToAddress(s)
	}
	return out
}

func addrList(as []common.Address) string {
	var parts []string
	for _, x := range as {
		parts = append(parts, x.Hex())
	}
	return strings.Join(parts, ",")
}

func flagList(fl [6]ethx.Flag) string {
	var parts []string
	for i, f := range fl {
		if f.Set() {
			parts = append(parts, fmt.Sprintf("slot %d since block %d", i+1, f.BlockNumber))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "; ")
}

// factor checks a contract's compiled clock factor against -factor.
func (a *audit) factor(ctx context.Context, what string, at common.Address, art *Artifact) {
	v, err := read[uint64](ctx, a.cn, a.at, at, art, "clockFactor")
	if err != nil {
		a.fail(what+"'s clock factor", err)
		return
	}
	a.check(v == a.cfg.Factor, "%s's clock factor is %d (must be %d: -factor; mainnet's is 1)", what, v, a.cfg.Factor)
}

// trusted checks the client's trusted header and its set: the set the client started from against the launch's, the
// trust root — the header and the set initialize took — against gno.land's own block and validators at its height
// (trustRoot: E3-CF2, E3-RT5), and the header the client trusts now against gno.land's block at its height. Without a
// gno source the trust root is UNVERIFIED: the record, the creation input and the chain all say the same thing a
// deployer of a forged root wrote.
func (a *audit) trusted(ctx context.Context, client common.Address, gc *Artifact) {
	th, err1 := read[int64](ctx, a.cn, a.at, client, gc, "trustedHeight")
	hash, err2 := read[[32]byte](ctx, a.cn, a.at, client, gc, "trustedHash")
	sh, err3 := read[[32]byte](ctx, a.cn, a.at, client, gc, "setHash")
	if err := errors.Join(err1, err2, err3); err != nil {
		a.fail("the client's trusted header", err)
		return
	}
	a.note("the client's trusted height is %d (hash %x), its current set hash %x", th, hash, sh)
	l := a.d.Launch
	set, launchSet, err := launchSetHash(l)
	if err != nil {
		a.check(false, "%v", err)
		return
	}
	first, err := read[ethx.SetEntry](ctx, a.cn, a.at, client, gc, "history", big.NewInt(0))
	if err != nil {
		a.fail("the client's first set", err)
		return
	}
	a.check(first.SetHash == launchSet && first.FromHeight == l.TrustedHeight, "the client started from height %d with set hash %x (the launch's %d validators at height %d hash to %x)", first.FromHeight, first.SetHash, len(set.Validators), l.TrustedHeight, launchSet)
	if len(a.gnos) == 0 {
		a.unverify("the client's trust root — the header (height %d, hash %s) and the %d validators its initialize took — is held to the deployer's record alone: initialize checks only their own consistency and their hour, so a client rooted in a header and a set of the deployer's making passes every other check (E3-CF2). Give -gno-rpc: a gno node of your own", l.TrustedHeight, l.TrustedHash, len(l.Validators))
		return
	}
	a.trustRoot(ctx, first)
	blk, err := a.gnoBlock(ctx, th)
	if err != nil {
		a.fail(fmt.Sprintf("gno.land's block %d (the header the client trusts now)", th), err)
		return
	}
	a.check(blk.Hash == hash, "the client's trusted hash at height %d is gno.land's block hash %x", th, blk.Hash)
}

// counter checks the line's last number against r/hlgov's counter (the Solana plan's K15, AT3): equal is the only
// governable state at rest — HlAdmin above r/hlgov is a foreign decision's number, below it r/hlgov's next decision is
// refused OutOfOrder for ever — but for r/hlgov's decisions dispatched and not yet recorded at the block read, each
// of which must render as r/hlgov's own (inFlight, E4-TL6). The number read is the live HlAdmin's: a kind 9's
// successor took its predecessor's number and counts on. While r/hlgov is not up, HlAdmin has recorded nothing and its
// number is the start held above (r/hlgov's own, or a chain's first deployment's 0).
func (a *audit) counter(ctx context.Context, admin common.Address, ha *Artifact) {
	live := a.line.Live
	last, err := read[uint64](ctx, a.cn, a.at, live, ha, "lastSeq")
	if err != nil {
		a.fail("the live HlAdmin's last number", err)
		return
	}
	pend, _ := read[uint32](ctx, a.cn, a.at, live, ha, "pending")
	a.note("the live HlAdmin %s's last decision number %d (the next takes %d), %d pending", live.Hex(), last, last+1, pend)
	start := a.launch.StartSequence
	gov := a.govRead(ctx)
	switch {
	case len(a.gnos) == 0:
		a.note("r/hlgov's counter is not read without a gno source: HlAdmin's number is held to its own logs below")
	case gov.Absent:
		a.note("r/hlgov (%s) is not on the chain yet: its counter is compared once the realms are up", a.hlgovPath)
		a.check(last == start, "HlAdmin's last number %d is the start sequence %d (no decision can be recorded before r/hlgov is up)", last, start)
	case gov.Gov == nil:
		a.fail("r/hlgov's counter", gov.Err)
	default:
		g := gov.Gov
		switch {
		case last < g.Last:
			a.inFlight(ctx, last, g.Last)
		default:
			a.check(last == g.Last, "the line's last number %d is r/hlgov's counter %d (a gap means a foreign decision took a number or HlAdmin started off r/hlgov's counter)", last, g.Last)
		}
		a.check(start <= g.Last && start >= g.Start, "HlAdmin's start sequence %d lies in r/hlgov's numbers (its compiled start %d, its counter %d): HlAdmin takes r/hlgov's decision %d first", start, g.Start, g.Last, start+1)
		a.check(strings.EqualFold(g.Recipient, live.Hex()) || live != admin, "r/hlgov sends its decisions to HlAdmin %s (its recipient is %s)", live.Hex(), g.Recipient)
		if live != admin {
			a.note("r/hlgov's decision recipient is %s (the line's live HlAdmin is %s; a recipient swap points r/hlgov at a successor)", g.Recipient, live.Hex())
		}
	}
}

// inFlightMax bounds the decisions the counter check reads as in flight: a gap wider than this is no delivery window.
const inFlightMax = 64

// inFlightOld is how old a dispatch may be, still not recorded on Ethereum, before the audit warns: a decision's
// header and its finality take ≈15–35 minutes (E3-GS6).
const inFlightOld = 2 * time.Hour

// inFlight is the counter check when the line's last number is below r/hlgov's (E4-TL6): every decision r/hlgov
// dispatches spends ≈15–35 minutes between its dispatch and its record at a finalized block (its header, then
// finality), so on an honest bridge the numbers last+1 … govLast are r/hlgov's decisions in flight — each must render
// as r/hlgov's own under that number (decision/<n>: a body that reads, carrying n), said with its age; one that does
// not (null, another number in its body) is a gap no delivery explains, FAIL, as is a gap past inFlightMax. HlAdmin
// records numbers in order (OutOfOrder otherwise), so none of them is recorded on Ethereum at the block read.
func (a *audit) inFlight(ctx context.Context, last, govLast uint64) {
	gap := govLast - last
	if gap > inFlightMax {
		a.check(false, "the line's last number %d is r/hlgov's counter %d (a gap means a foreign decision took a number or HlAdmin started off r/hlgov's counter): %d numbers apart, more than any delivery leaves in flight (%d)", last, govLast, gap, inFlightMax)
		return
	}
	var bad []string
	for n := last + 1; n <= govLast; n++ {
		gd, err := a.govDecision(ctx, n)
		switch {
		case err != nil:
			bad = append(bad, fmt.Sprintf("#%d does not read (%s)", n, ethx.Redact(err.Error())))
			continue
		case gd == nil:
			bad = append(bad, fmt.Sprintf("#%d is none r/hlgov dispatched (it renders null)", n))
			continue
		}
		kind, _, err := govBody(gd, n)
		if err != nil {
			bad = append(bad, fmt.Sprintf("#%d: %v", n, err))
			continue
		}
		age := ""
		if gd.At > 0 {
			d := time.Duration(a.atTime-gd.At) * time.Second
			age = fmt.Sprintf(", dispatched at %s (%s before the block read)", time.Unix(gd.At, 0).UTC().Format(time.RFC3339), d.Round(time.Second))
			if d > inFlightOld {
				a.warn("r/hlgov's decision #%d (kind %d) was dispatched %s before the block read and is not recorded on Ethereum: a decision's delivery takes ≈15–35 minutes — its header or its delivery waits (the relayer's status and hlwatch say which)", n, kind, d.Round(time.Minute))
			}
		}
		a.note("r/hlgov's decision #%d (kind %d) is in flight: dispatched by r/hlgov, not recorded on Ethereum at block %d%s", n, kind, a.atNum, age)
	}
	if len(bad) > 0 {
		a.check(false, "the line's last number %d is r/hlgov's counter %d (a gap means a foreign decision took a number or HlAdmin started off r/hlgov's counter): of r/hlgov's numbers %d–%d not recorded on Ethereum, %s", last, govLast, last+1, govLast, strings.Join(bad, "; "))
		return
	}
	a.check(true, "the line's last number %d is r/hlgov's counter %d less its %d decision(s) in flight (#%d–#%d: each dispatched by r/hlgov under its number, not recorded on Ethereum at block %d — a delivery's ≈15–35 minutes, E4-TL6)", last, govLast, gap, last+1, govLast, a.atNum)
}

// recordExplained holds HlAdmin's stored record to its logs (M2-NOTES.md Fold F5, F5.1 item 13): an initcode can write
// any storage — the creation-input comparison refuses that outright, and these reads check it. lastSeq() is the start
// plus one for each DecisionRecorded (handle logs every number it takes, once); pending() at most the decisions
// recorded to pend and not yet ended; every cancelled range a RangeCancelled's ends; every standing flag a FlagSet of
// its slot at its block; a standing hold's start a FlagSet's block; every hold of the history a HoldEnded's ends. The
// initcode runs as HlAdmin, so it can log any of these beside the entry it plants (E5-CF1): HlAdmin's creation
// transaction must log nothing — the build's constructor and HlAdminExec's emit no event — and no log of it explains
// an entry. And what no log dates: a planted hold start older than HlAdmin rides into a real HoldEnded log when the
// next flag settles the stored hold, and a ninth hold merges it into the oldest entry, lowering kind 5's floor — so
// every hold start of the history, and every moment of the stored hold (its storage word, read whole: no view shows
// a hold whose dip ran out), lies between HlAdmin's creation and the block read: a hold is two guardian flags set on
// HlAdmin, never before it existed. Check 34 bounds the record from above only (E5-CF1: a bound below would be an
// immutable, past HlAdmin's EIP-3860 margin).
func (a *audit) recordExplained(ctx context.Context, admin common.Address, ha *Artifact) {
	cr := a.adminCreation(ctx)
	ev := func(name string) []types.Log {
		ls, err := a.logsOf(ctx, admin, ethx.Admin.Event(name))
		if err != nil {
			a.fail("HlAdmin's "+name+" logs", err)
			return nil
		}
		return explainable(ls, cr.tx)
	}
	data := func(name string, l types.Log) []any {
		vals, err := ethx.Admin.ABI.Events[name].Inputs.NonIndexed().Unpack(l.Data)
		if err != nil {
			return nil
		}
		return vals
	}
	start := a.launch.StartSequence // the start held (audit_expect.go), not the record's
	recorded := ev("DecisionRecorded")
	ended := map[uint64]bool{}
	for _, name := range []string{"DecisionExecuted", "DecisionSpent", "DecisionCancelled"} {
		for _, l := range ev(name) {
			if len(l.Topics) > 1 {
				ended[new(big.Int).SetBytes(l.Topics[1][:]).Uint64()] = true
			} else if v := data(name, l); len(v) > 0 {
				if n, ok := v[0].(uint64); ok {
					ended[n] = true
				}
			}
		}
	}
	pendable := 0
	for _, l := range recorded {
		v := data("DecisionRecorded", l)
		if len(v) != 2 || len(l.Topics) < 2 {
			continue
		}
		kind, _ := v[0].(uint8)
		mature, _ := v[1].(uint64)
		n := new(big.Int).SetBytes(l.Topics[1][:]).Uint64()
		if kind != 1 && mature != 0 && !ended[n] {
			pendable++
		}
	}
	if last, err := read[uint64](ctx, a.cn, a.at, admin, ha, "lastSeq"); err != nil {
		a.fail("HlAdmin's lastSeq()", err)
	} else {
		a.check(last == start+uint64(len(recorded)), "HlAdmin's lastSeq() %d is the launch's start %d plus its %d DecisionRecorded log(s): every number it took, it logged", last, start, len(recorded))
	}
	if p, err := read[uint32](ctx, a.cn, a.at, admin, ha, "pending"); err != nil {
		a.fail("HlAdmin's pending()", err)
	} else {
		a.check(int(p) <= pendable, "HlAdmin's pending() %d is at most the %d decision(s) its logs recorded to pend and did not end", p, pendable)
	}
	froms, tos := map[uint64]bool{}, map[uint64]bool{}
	for _, l := range ev("RangeCancelled") {
		if v := data("RangeCancelled", l); len(v) == 2 {
			froms[v[0].(uint64)], tos[v[1].(uint64)] = true, true
		}
	}
	if rs, err := read[[][2]uint64](ctx, a.cn, a.at, admin, ha, "cancelledRanges"); err != nil {
		a.fail("HlAdmin's cancelledRanges()", err)
	} else {
		bad := []string{}
		for _, r := range rs {
			if !froms[r[0]] || !tos[r[1]] {
				bad = append(bad, fmt.Sprintf("%d–%d", r[0], r[1]))
			}
		}
		a.check(len(bad) == 0, "HlAdmin's %d cancelled range(s) each end where a RangeCancelled log put it%s", len(rs), unexplained(bad))
	}
	setAt := map[[2]uint64]bool{} // (slot, block)
	setBlocks := map[uint64]bool{}
	for _, l := range ev("FlagSet") {
		if v := data("FlagSet", l); len(v) == 1 {
			setAt[[2]uint64{uint64(v[0].(uint8)), l.BlockNumber}] = true
			setBlocks[l.BlockNumber] = true
		}
	}
	if fl, err := read[[6]ethx.Flag](ctx, a.cn, a.at, admin, ha, "flags"); err != nil {
		a.fail("HlAdmin's flags()", err)
	} else {
		bad := []string{}
		for i, f := range fl {
			if f.Set() && !setAt[[2]uint64{uint64(i + 1), f.BlockNumber}] {
				bad = append(bad, fmt.Sprintf("slot %d at block %d", i+1, f.BlockNumber))
			}
		}
		a.check(len(bad) == 0, "HlAdmin's standing flags (%s) each have their FlagSet log at their block%s", flagList(fl), unexplained(bad))
	}
	if h, err := read[ethx.Flag](ctx, a.cn, a.at, admin, ha, "hold"); err != nil {
		a.fail("HlAdmin's hold()", err)
	} else if h.Set() {
		a.check(setBlocks[h.BlockNumber], "HlAdmin's hold stands since block %d, where a FlagSet log brought its guardian flags to two", h.BlockNumber)
	}
	starts, ends := map[uint64]bool{}, map[uint64]bool{}
	for _, l := range ev("HoldEnded") {
		if v := data("HoldEnded", l); len(v) == 2 {
			starts[v[0].(uint64)], ends[v[1].(uint64)] = true, true
		}
	}
	if hs, err := read[[]ethx.Hold](ctx, a.cn, a.at, admin, ha, "holds"); err != nil {
		a.fail("HlAdmin's holds()", err)
	} else {
		bad := []string{}
		for _, h := range hs {
			if !starts[h.Start] || !ends[h.End] {
				bad = append(bad, fmt.Sprintf("%d–%d", h.Start, h.End))
			}
		}
		a.check(len(bad) == 0, "HlAdmin's hold history (%d) each starts and ends where a HoldEnded log put it%s", len(hs), unexplained(bad))
		if cr.ok {
			early := earlyHolds(hs, cr.time, uint64(a.atTime))
			a.check(len(early) == 0, "HlAdmin's hold history (%d) each starts at or after HlAdmin's creation (block %d, time %d) and ends by the block read (time %d): a hold is two guardian flags set on HlAdmin, never before it existed (E5-CF1)%s", len(hs), cr.block, cr.time, a.atTime, outside(early))
		}
	}
	if cr.ok {
		a.holdMoments(ctx, admin, ha, cr)
	}
}

// unexplained lists the entries no log explains: planted.
func unexplained(bad []string) string {
	if len(bad) == 0 {
		return ""
	}
	return ": NO LOG for " + strings.Join(bad, ", ") + " — planted"
}

// outside lists the entries dated outside HlAdmin's life: planted.
func outside(bad []string) string {
	if len(bad) == 0 {
		return ""
	}
	return ": NOT for " + strings.Join(bad, ", ") + " — planted"
}

// creationAt is HlAdmin's creation as the audit reads it: its transaction (the record's), the block and time it ran at;
// ok when all three were read.
type creationAt struct {
	tx          common.Hash
	block, time uint64
	ok          bool
}

// adminCreation reads HlAdmin's creation transaction and holds it to log nothing (E5-CF1): the build's constructor and
// HlAdminExec's emit no event, while an initcode running as HlAdmin can log FlagSet, HoldEnded, DecisionRecorded,
// RangeCancelled or SetupDone beside the record it plants — setupDoneBy's case, as a class. Every log of that
// transaction fails, whatever contract logged it (an honest one has none).
func (a *audit) adminCreation(ctx context.Context) creationAt {
	st := a.d.stepOf("hlAdmin")
	if st == nil || st.Tx == "" {
		a.check(false, "the record names no creation transaction of HlAdmin: its logs cannot be told from those of the record its initcode may have planted")
		return creationAt{}
	}
	cr := creationAt{tx: common.HexToHash(st.Tx)}
	rc, err := a.cn.c.Receipt(ctx, cr.tx)
	if err != nil {
		a.fail("HlAdmin's creation receipt "+st.Tx, err)
		return cr
	}
	logged := creationLogs(rc.Logs, a.d.Addr("hlAdmin"))
	a.check(len(logged) == 0, "HlAdmin's creation transaction %s logged nothing: the build's constructor and HlAdminExec's emit no event, and an initcode running as HlAdmin could log a record's explanation beside the record it plants (E5-CF1)%s", st.Tx, plantedLogs(logged))
	if rc.BlockNumber == nil {
		a.check(false, "HlAdmin's creation receipt %s names no block", st.Tx)
		return cr
	}
	b, err := a.cn.c.BlockAt(ctx, ethx.AtNumber(rc.BlockNumber.Uint64()))
	if err != nil {
		a.fail(fmt.Sprintf("HlAdmin's creation block %d", rc.BlockNumber.Uint64()), err)
		return cr
	}
	cr.block, cr.time, cr.ok = b.Number, b.Time, true
	return cr
}

// creationLogs names each log of HlAdmin's creation transaction: an event of HlAdmin's by its name, any other by its
// address and first topic.
func creationLogs(logs []*types.Log, admin common.Address) []string {
	var out []string
	for _, l := range logs {
		name := "an event"
		if len(l.Topics) > 0 {
			name = "topic " + l.Topics[0].Hex()
			if l.Address == admin {
				for n, e := range ethx.Admin.ABI.Events {
					if e.ID == l.Topics[0] {
						name = n
					}
				}
			}
		}
		who := l.Address.Hex()
		if l.Address == admin {
			who = "HlAdmin"
		}
		out = append(out, fmt.Sprintf("%s's %s (log %d)", who, name, l.Index))
	}
	sort.Strings(out)
	return out
}

func plantedLogs(logged []string) string {
	if len(logged) == 0 {
		return ""
	}
	return ": it logged " + strings.Join(logged, ", ") + " — an initcode's, not the build's constructor: none of them explains HlAdmin's record below"
}

// explainable are the logs that may explain HlAdmin's record: every one but its creation transaction's, whose initcode
// can log anything as HlAdmin (E5-CF1).
func explainable(ls []types.Log, creation common.Hash) []types.Log {
	if creation == (common.Hash{}) {
		return ls
	}
	out := ls[:0:0]
	for _, l := range ls {
		if l.TxHash != creation {
			out = append(out, l)
		}
	}
	return out
}

// earlyHolds are the hold history's entries no flag on HlAdmin could have left: a start before its creation (created,
// the creation block's time), an end before the start, or one after the block read (at).
func earlyHolds(hs []ethx.Hold, created, at uint64) []string {
	var bad []string
	for _, h := range hs {
		if h.Start < created || h.End < h.Start || h.End > at {
			bad = append(bad, fmt.Sprintf("%d–%d", h.Start, h.End))
		}
	}
	return bad
}

// wordHoldMoments is the word of HlAdmin's namespace (HlAdminStorage.SLOT + 12) holding its guardians' hold moments:
// Guardians.State's last word, packed from the low bytes up — holdBlock (bytes 24–31 of the big-endian word), holdTime
// (16–23), dipBlock (8–15), dipTime (0–7). The namespace's first word, then the successor's, then the State: six keys,
// two words of flag blocks, two of flag times, then this one.
const wordHoldMoments = 12

// holdMoments reads the stored hold's moments whole (no view shows a hold whose dip ran out and was never settled: the
// next flag settles it into the history, its start with it, under a real HoldEnded log) and holds each set one between
// HlAdmin's creation and the block read, a dip never before its hold (E5-CF1). The word's place is proved where a
// view can: a standing hold's start is hold()'s.
func (a *audit) holdMoments(ctx context.Context, admin common.Address, ha *Artifact, cr creationAt) {
	slot := common.BigToHash(new(big.Int).Add(hlAdminSlot.Big(), big.NewInt(wordHoldMoments)))
	w, err := a.cn.c.StorageAt(ctx, admin, slot, a.at)
	if err != nil {
		a.fail(fmt.Sprintf("HlAdmin's hold moments (its namespace's word %d)", wordHoldMoments), err)
		return
	}
	m := momentsOf(w)
	if h, err := read[ethx.Flag](ctx, a.cn, a.at, admin, ha, "hold"); err == nil && h.Set() {
		if !a.check2(m.holdBlock == h.BlockNumber && m.holdTime == h.Time, "HlAdmin's standing hold (block %d, time %d) is the start its namespace's word %d holds (block %d, time %d): the word read is the hold's", h.BlockNumber, h.Time, wordHoldMoments, m.holdBlock, m.holdTime) {
			return
		}
	}
	bad := m.outside(cr, a.atNum, uint64(a.atTime))
	a.check(len(bad) == 0, "HlAdmin's stored hold moments (its namespace's word %d, read whole: the hold's start block %d, time %d; its dip's block %d, time %d) are none, or lie between HlAdmin's creation (block %d, time %d) and the block read (%d, time %d), a dip never before its hold (E5-CF1)%s", wordHoldMoments, m.holdBlock, m.holdTime, m.dipBlock, m.dipTime, cr.block, cr.time, a.atNum, a.atTime, outside(bad))
}

// moments are the guardians' hold moments as HlAdmin stores them (Guardians.State).
type moments struct{ holdBlock, holdTime, dipBlock, dipTime uint64 }

func momentsOf(w common.Hash) moments {
	return moments{holdBlock: binary.BigEndian.Uint64(w[24:32]), holdTime: binary.BigEndian.Uint64(w[16:24]), dipBlock: binary.BigEndian.Uint64(w[8:16]), dipTime: binary.BigEndian.Uint64(w[0:8])}
}

// outside names the moments no flag on HlAdmin could have stored: a block or a time set alone, one before HlAdmin's
// creation or after the block read, a dip without a hold or before it.
func (m moments) outside(cr creationAt, atNum, atTime uint64) []string {
	var bad []string
	pair := func(what string, b, t uint64) {
		switch {
		case (b == 0) != (t == 0):
			bad = append(bad, fmt.Sprintf("%s's block %d and time %d (one set alone)", what, b, t))
		case b != 0 && (b < cr.block || b > atNum || t < cr.time || t > atTime):
			bad = append(bad, fmt.Sprintf("%s at block %d, time %d", what, b, t))
		}
	}
	pair("the hold's start", m.holdBlock, m.holdTime)
	pair("its dip", m.dipBlock, m.dipTime)
	if m.dipBlock != 0 && (m.holdBlock == 0 || m.dipBlock < m.holdBlock || m.dipTime < m.holdTime) {
		bad = append(bad, fmt.Sprintf("a dip (block %d) with no hold before it (block %d)", m.dipBlock, m.holdBlock))
	}
	return bad
}

// code compares each of the sixteen contracts' runtime code with its build.
func (a *audit) code(ctx context.Context, maxFee *big.Int) {
	d, ar := a.d, a.arts
	A := func(n string) common.Address { return d.Addr(n) }
	local, gnoD := uint64(d.LocalDomain), uint64(d.GnoDomain)
	block := func(field string) (Expect, bool) {
		st := d.stepOf(field)
		if st == nil || st.Tx == "" {
			return Expect{}, false
		}
		rc, err := a.cn.c.Receipt(ctx, common.HexToHash(st.Tx))
		if err != nil {
			a.fail(fmt.Sprintf("the creation receipt of %s (tx %s)", field, st.Tx), err)
			return Expect{}, false
		}
		if rc.ContractAddress != A(field) {
			a.check(false, "the recorded creation of %s (tx %s) created %s, not %s", field, st.Tx, rc.ContractAddress.Hex(), A(field).Hex())
			return Expect{}, false
		}
		return expectU64(rc.BlockNumber.Uint64(), fmt.Sprintf("the block of its creation, tx %s", st.Tx)), true
	}
	mailboxClient := func(field string) map[string]Expect {
		m := map[string]Expect{
			"MailboxClient.mailbox":     expectAddr(A("mailbox"), "our mailbox"),
			"MailboxClient.localDomain": expectU64(local, "Ethereum's domain"),
		}
		if e, ok := block(field); ok {
			m["Indexed.deployedBlock"] = e
		}
		return m
	}
	token := func(field string) map[string]Expect {
		m := mailboxClient(field)
		m["HypERC20._decimals"] = expectU64(6, "6, gno.land's")
		m["TokenRouter.scaleNumerator"] = expectU64(1, "scale 1/1")
		m["TokenRouter.scaleDenominator"] = expectU64(1, "scale 1/1")
		return m
	}
	mailboxImpl := map[string]Expect{"Mailbox.localDomain": expectU64(local, "Ethereum's domain")}
	if e, ok := block("mailboxImpl"); ok {
		mailboxImpl["Indexed.deployedBlock"] = e
	}
	adminExp := map[string]Expect{
		"HlAdmin.mailbox":         expectAddr(A("mailbox"), "our mailbox"),
		"HlAdmin.client":          expectAddr(A("client"), "our client"),
		"HlAdmin.gate":            expectAddr(A("gate"), "our gate"),
		"HlAdmin._token0":         expectAddr(A("token0"), "token 0"),
		"HlAdmin._token1":         expectAddr(A("token1"), "token 1"),
		"HlAdmin.proxyAdmin":      expectAddr(A("proxyAdmin"), "our ProxyAdmin"),
		"HlAdmin.merkleTreeHook":  expectAddr(A("merkleTreeHook"), "our MerkleTreeHook"),
		"HlAdmin.protocolFee":     expectAddr(A("protocolFee"), "our ProtocolFee"),
		"HlAdmin.ed25519":         expectAddr(A("ed25519Verifier"), "our Ed25519Verifier"),
		"HlAdmin.ed25519CodeHash": expectBytes32(ar.Ed25519CodeHash(), "this tree's build of Ed25519Verifier"),
		"HlAdmin.localDomain":     expectU64(local, "Ethereum's domain"),
		"HlAdmin.origin":          expectU64(gnoD, "gno.land's domain"),
		"HlAdmin.hlgov":           expectBytes32(a.hlgov32, "r/hlgov's address "+a.hlgovPath+", 32 bytes"),
		"HlAdmin._route0":         expectBytes32(a.route[0], "the GNOT route realm's address"),
		"HlAdmin._route1":         expectBytes32(a.route[1], "the BUBBLE route realm's address"),
		"HlAdmin.predecessor":     expectAddr(common.Address{}, "none: a launch HlAdmin"),
		"HlAdmin.exec":            expectAddr(A("hlAdminExec"), "the HlAdminExec its constructor created"),
		// fold F5's three (M2-NOTES.md F5.1 item 2): the path's hash kind 9 compares, the delegate's code pinned, the
		// launch's start no planted storage moves
		"HlAdmin.mailboxPkgPathHash": expectBytes32(a.mailboxPkgPathHash, "keccak256 of "+a.pkgPath),
		"HlAdmin._startSequence":     expectU64(a.launch.StartSequence, "the launch's startSequence, r/hlgov's counter at the deploy (its source above)"),
	}
	if code, err := a.cn.code(ctx, a.at, A("hlAdminExec")); err != nil {
		a.fail("hlAdminExec's code (HlAdmin._execCodeHash's expected value)", err)
	} else {
		adminExp["HlAdmin._execCodeHash"] = expectBytes32(crypto.Keccak256Hash(code), "the EXTCODEHASH of the recorded hlAdminExec "+A("hlAdminExec").Hex()+", whose code is held to the build below")
	}
	execExp := map[string]Expect{
		"HlAdminExec.admin":              expectAddr(A("hlAdmin"), "HlAdmin: the only address it runs at"),
		"HlAdminExec.mailbox":            expectAddr(A("mailbox"), "our mailbox"),
		"HlAdminExec.client":             expectAddr(A("client"), "our client"),
		"HlAdminExec.gate":               expectAddr(A("gate"), "our gate"),
		"HlAdminExec.token0":             expectAddr(A("token0"), "token 0"),
		"HlAdminExec.token1":             expectAddr(A("token1"), "token 1"),
		"HlAdminExec.proxyAdmin":         expectAddr(A("proxyAdmin"), "our ProxyAdmin"),
		"HlAdminExec.merkleTreeHook":     expectAddr(A("merkleTreeHook"), "our MerkleTreeHook"),
		"HlAdminExec.protocolFee":        expectAddr(A("protocolFee"), "our ProtocolFee"),
		"HlAdminExec.ed25519":            expectAddr(A("ed25519Verifier"), "our Ed25519Verifier"),
		"HlAdminExec.ed25519CodeHash":    expectBytes32(ar.Ed25519CodeHash(), "this tree's build of Ed25519Verifier"),
		"HlAdminExec.localDomain":        expectU64(local, "Ethereum's domain"),
		"HlAdminExec.maxPerMessage0":     expectU64(a.launch.MaxPerMessage[0], "token 0's launch mint limit a message"),
		"HlAdminExec.maxPerMessage1":     expectU64(a.launch.MaxPerMessage[1], "token 1's launch mint limit a message"),
		"HlAdminExec.dayCap0":            expectU64(a.launch.DayCap[0], "token 0's launch day cap"),
		"HlAdminExec.dayCap1":            expectU64(a.launch.DayCap[1], "token 1's launch day cap"),
		"HlAdminExec.launch":             expectU64(1, "true: a launch HlAdmin, no predecessor"),
		"HlAdminExec.origin":             expectU64(gnoD, "gno.land's domain"),
		"HlAdminExec.route0":             expectBytes32(a.route[0], "the GNOT route realm's address"),
		"HlAdminExec.route1":             expectBytes32(a.route[1], "the BUBBLE route realm's address"),
		"HlAdminExec.mailboxPkgPathHash": expectBytes32(a.mailboxPkgPathHash, "keccak256 of "+a.pkgPath),
	}
	none := map[string]Expect{}
	clientExp := map[string]Expect{"GnoLightClient.mailboxPkgPathHash": expectBytes32(a.mailboxPkgPathHash, "keccak256 of "+a.pkgPath)}
	// the client calls the verifier at an address its implementation holds as an immutable (ED25519): every such
	// immutable is the recorded Ed25519Verifier, whose own code fills no immutable
	for _, im := range ar["GnoLightClient"].Immutables {
		if n := strings.ToLower(im.Name); strings.Contains(n, "verifier") || strings.Contains(n, "ed25519") {
			clientExp[im.Name] = expectAddr(A("ed25519Verifier"), "our Ed25519Verifier")
		}
	}
	type codeCheck struct {
		field, art string
		want       map[string]Expect
	}
	for _, x := range []codeCheck{
		{"proxyAdmin", "ProxyAdmin", none},
		{"mailboxImpl", "Mailbox", mailboxImpl},
		{"mailbox", "TransparentUpgradeableProxy", none},
		{"merkleTreeHook", "MerkleTreeHook", mailboxClient("merkleTreeHook")},
		{"protocolFee", "ProtocolFee", map[string]Expect{"ProtocolFee.MAX_PROTOCOL_FEE": expectUint(maxFee, "0.001 ETH, the launch's")}},
		{"ed25519Verifier", "Ed25519Verifier", none},
		{"clientImpl", "GnoLightClient", clientExp},
		{"client", "TransparentUpgradeableProxy", none},
		{"token0Impl", "HypERC20", token("token0Impl")},
		{"token0", "TransparentUpgradeableProxy", none},
		{"token1Impl", "HypERC20", token("token1Impl")},
		{"token1", "TransparentUpgradeableProxy", none},
		{"gateImpl", "PausableGate", none},
		{"gate", "TransparentUpgradeableProxy", none},
		{"hlAdmin", "HlAdmin", adminExp},
		{"hlAdminExec", "HlAdminExec", execExp},
	} {
		code, err := a.cn.code(ctx, a.at, A(x.field))
		if err != nil {
			a.fail(x.field+"'s code", err)
			continue
		}
		a.note("%s: %d bytes of code, keccak256 %x (the build's %d bytes, %x before its immutables are filled)", x.field, len(code), crypto.Keccak256(code), len(ar[x.art].Runtime), crypto.Keccak256(ar[x.art].Runtime))
		for _, j := range JudgeCode(x.field, A(x.field), code, ar[x.art], x.want) {
			a.check(j.OK, "%s", j.Text)
		}
	}
}

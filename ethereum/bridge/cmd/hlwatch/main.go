// Command hlwatch is the guardians' tool (eth/bridge/PLAN.md §5.8, the Solana plan's §5.8 carried over): from the
// holder's own gno node and two agreeing Ethereum providers it shows who governs, the flags and the seats on all
// three objects (hlmailbox, the gate, HlAdmin), the holds and the hold history, every pending decision on both chains
// and when it matures, the builder-made GovDAO proposals with no execution, both sequence counters (r/hlgov's and
// HlAdmin's), the supply identity by the signers' rule, the client's trusted header against gno.land's, the header
// record and its fork rule, every stage of a header gno.land does not have, every foreign decision — and before a
// kind 8 or 9 executes, its target's code held to a reproducible build with every immutable masked and each printed
// beside the value it must have (the plan red team's E2-RT3: on Ethereum the immutables carry every binding), its
// creation transaction held to the build's creation code and the constructor's arguments (E2-RT1: an initcode can
// write any storage and return the build's runtime), an EIP-7702 account named as one, the storage layouts'
// difference, a kind 8's call decoded against the implementation's ABI (E2-RT10), a kind 9's successor's state and
// bindings beside this HlAdmin's and its HlAdminExec held to its build — and every pending kind 8 or 9 whose target has
// no code yet (it pends until code is deployed there: whoever can create at that address decides when it runs),
// alarmed once it has waited past its maturity longer than -code-wait. `watch` alarms the holder on every change;
// `flag`/`unflag` set or clear the holder's flag on hlmailbox and on both Ethereum objects (two Ethereum transactions,
// each confirmed final, either re-sent when a reorganisation dropped it; each of the three sent whatever another
// answered, a failed one named and the exit non-zero); `check` reads a kind 8's or 9's target
// before the vote, with the verdict: "immutable", "MUTABLE: would be spent", "NO CODE YET: pends" or "NOT SHOWN
// IMMUTABLE" (no build matches, an immutable or an argument nothing here vouches for, no creation transaction given).
//
//	hlwatch status -config bridge.json -node http://127.0.0.1:26657 [-state hlwatch.json] [-from-block N]
//	               [-build LABEL=artifact.json …] [-creation-tx ADDR=0xTX …] [-layout-old old.json -layout-new new.json] [-code-wait 168h]
//	hlwatch watch  -config bridge.json -node URL [-state hlwatch.json] [-every 10s] [-webhook URL] [-code-wait 168h]
//	hlwatch flag   -config bridge.json -node URL -home DIR -key NAME -eth-key FILE -route BUBBLE|GNOT|pkgpath -fn sends|credits|payouts [-ids a,b]
//	hlwatch unflag -config bridge.json -node URL -home DIR -key NAME -eth-key FILE -route ... -fn ...
//	hlwatch check  -config bridge.json -node URL -proposal ID | -decision N | -target 0x… [-kind 8|9] [-codehash 0x…] [-proxy 0x… -call 0x…|none]
//	               [-creation-tx 0xTX] [-build LABEL=artifact.json …] [-layout-old F -layout-new F]
//	hlwatch version
//
// `check` reads a decision's own parameters (E3-GT7): -proposal a GovDAO proposal made with r/hlgov's builder, before
// the vote (the parameters its description states, which the builder writes from those its executor dispatches; GovDAO
// shows the executor's realm as the executor states it, so read the decision again with -decision once dispatched);
// -decision a number r/hlgov dispatched (its own record of the body HlAdmin receives). Typed by hand, a kind 8 names
// its -proxy and its -call (-call none for a bare upgrade): a reading never says "no call" of a call it was not shown.
// A kind 8 is "immutable" only with both storage layouts read and append-only (E3-CF3: -layout-old, -layout-new).
// `status` polls until its scans reach Ethereum's finalized block (-catch-up bounds it, E3-GT10): give it -state, or
// every run reads the deployment's history from eth.deployBlock again.
//
// Every outsider compiles their own; the build hash (printed by `version` and at the head of every status) is in the
// launch notice. Without a node, or without two providers in the config, it refuses: on anything less it would alarm
// only. A -build is forge's artifact (in its out directory, built with --ast: the immutables are named by the
// directory's ASTs; or Hyperlane's release compile, local/hyperlane-artifacts.sh) or a file holding a runtime code's
// keccak256, which matches only code with no immutable.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/ethereum/bridge/agent"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
	"github.com/gnolang/bridges/ethereum/bridge/repro"
)

type builds map[string]string

func (b builds) String() string { return fmt.Sprint(map[string]string(b)) }

func (b builds) Set(v string) error {
	label, file, ok := strings.Cut(v, "=")
	if !ok {
		return errors.New("-build is LABEL=FILE")
	}
	b[label] = file
	return nil
}

// creations are -creation-tx's: a target's creation transaction ("TX" for check's -target, or "ADDR=TX").
type creations []string

func (c *creations) String() string { return strings.Join(*c, ",") }

func (c *creations) Set(v string) error {
	*c = append(*c, v)
	return nil
}

// creationTxs reads -creation-tx's into a map by target (a bare hash is the -target's).
func creationTxs(cs creations, target string) (map[common.Address]common.Hash, error) {
	out := map[common.Address]common.Hash{}
	for _, c := range cs {
		addr, tx, ok := strings.Cut(c, "=")
		if !ok {
			addr, tx = target, c
		}
		a, err := ethx.ParseAddress(addr)
		if err != nil {
			return nil, fmt.Errorf("-creation-tx %s: the target %w (ADDR=TX, or a bare TX with -target)", c, err)
		}
		h, err := hexutil.Decode(tx)
		if err != nil || len(h) != 32 {
			return nil, fmt.Errorf("-creation-tx %s: the transaction is 0x and 64 hex digits", c)
		}
		out[a] = common.BytesToHash(h)
	}
	return out, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hlwatch status|watch|flag|unflag|check|version [flags]")
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "version" {
		fmt.Println(buildHash())
		return
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	config := fs.String("config", "bridge.json", "the deployment's facts (the launch notice publishes it)")
	node := fs.String("node", "", "the holder's own gno node (required; a public endpoint is not a node)")
	state := fs.String("state", "", "the burn index and the client's record between runs (a file; \"\" keeps none, and every run reads the deployment's history from eth.deployBlock again)")
	fromBlock := fs.Uint64("from-block", 0, "the Ethereum block a fresh state's scans start at (0: the config's eth.deployBlock, finishSetup's block, else that block found on the chain)")
	home := fs.String("home", "", "flag/unflag: gnokey's keybase")
	key := fs.String("key", "", "flag/unflag: the holder's gno key, a guardian's or a watch key's")
	ethKey := fs.String("eth-key", "", "flag/unflag: the holder's Ethereum key file (the same seat on the gate and HlAdmin)")
	route := fs.String("route", "", "flag/unflag: a route by symbol (BUBBLE, GNOT) or pkgpath")
	fn := fs.String("fn", "", "flag/unflag: sends, credits or payouts")
	ids := fs.String("ids", "", "flag: a guardian's payouts flag may name the message ids it suspects (comma separated)")
	every := fs.Duration("every", 10*time.Second, "watch: how often to look")
	codeWait := fs.Duration("code-wait", agent.DefaultCodeWaitAge, "status/watch: how long a matured kind 8 or 9 may wait for its target's code before it is alarmed (GovDAO cancels a stale one)")
	catchUp := fs.Duration("catch-up", agent.DefaultCatchUp, "status/watch: how long one reading polls to bring a state that is behind (a fresh -state, or none) to the finalized block, 20,000 blocks a poll for each scan; a scan still behind is named and the page is not green")
	webhook := fs.String("webhook", "", "watch: alarms as one JSON POST each")
	target := fs.String("target", "", "check: a kind 8's implementation or a kind 9's successor, typed (else -proposal or -decision)")
	proxy := fs.String("proxy", "", "check, kind 8 typed: the proxy the decision upgrades (its build is held to the one the proxy runs)")
	call := fs.String("call", "", "check, kind 8 typed: the decision's call, hex (decoded against the implementation's ABI), or none for a bare upgrade")
	proposal := fs.Int64("proposal", -1, "check: a GovDAO proposal made with r/hlgov's builder, before the vote: its kind 8's or 9's parameters are read from it")
	decision := fs.Uint64("decision", 0, "check: a decision r/hlgov dispatched (its number): its kind 8's or 9's parameters are read from r/hlgov's record of it")
	var cts creations
	fs.Var(&cts, "creation-tx", "status/check: a target's creation transaction, ADDR=0xTX (or a bare 0xTX for check's -target), held to the build (repeatable)")
	codehash := fs.String("codehash", "", "check: the code hash the decision names")
	kind := fs.Uint("kind", 8, "check: the decision's kind: 8 (an upgrade's implementation) or 9 (a successor: its state is read against the live HlAdmin's)")
	layoutOld := fs.String("layout-old", "", "status/check: the running implementation's storage layout (forge's storageLayout JSON)")
	layoutNew := fs.String("layout-new", "", "status/check: the new implementation's")
	bs := builds{}
	fs.Var(bs, "build", "status/check: LABEL=FILE, a reproducible build a target is held against (repeatable)")
	_ = fs.Parse(args)

	log.SetOutput(ethx.RedactWriter{W: os.Stderr}) // no provider URL's key in a log line
	cfg, err := agent.LoadConfig(*config)
	if err != nil {
		log.Fatal(err)
	}
	if *node == "" {
		log.Fatal("hlwatch: -node is required: the holder's own gno node (§5.8: on anything less it alarms only)")
	}
	if len(cfg.Eth.Providers) < 2 {
		log.Fatal("hlwatch: two Ethereum providers that must agree are required in the config (eth.providers)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	alarm := &agent.Alarmer{URL: *webhook, Source: "hlwatch"}
	w, err := agent.NewWatch(ctx, cfg, *node, *state, *fromBlock, alarm)
	if err != nil {
		log.Fatal(err)
	}
	w.CodeWaitAge = *codeWait
	w.CatchUp = *catchUp
	w.Builds, err = readBuilds(bs)
	if err != nil {
		log.Fatal(err)
	}
	if cmd != "check" { // check's target may come from the decision: its creation is read once the target is known
		if w.CreationTxs, err = creationTxs(cts, *target); err != nil {
			log.Fatal(err)
		}
	}
	if *layoutOld != "" || *layoutNew != "" {
		if w.LayoutOld, err = os.ReadFile(*layoutOld); err != nil {
			log.Fatal(err)
		}
		if w.LayoutNew, err = os.ReadFile(*layoutNew); err != nil {
			log.Fatal(err)
		}
	}
	switch cmd {
	case "status":
		v, err := w.View(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(v.Text(buildHash()))
		if ok, why := v.Green(); ok {
			fmt.Println("\nhlwatch: GREEN")
		} else {
			fmt.Println("\nhlwatch: NOT GREEN — " + strings.Join(why, "; "))
		}
	case "watch":
		loop(ctx, w, alarm, *every)
	case "flag", "unflag":
		if *home == "" || *key == "" || *route == "" || *fn == "" || *ethKey == "" {
			log.Fatal("hlwatch: -home, -key, -eth-key, -route and -fn are needed")
		}
		if err := stroke(ctx, w, cfg, *node, cmd == "flag", *home, *key, *ethKey, *route, *fn, *ids); err != nil {
			log.Fatal(err)
		}
	case "check":
		var spec agent.TargetSpec
		switch {
		case *proposal >= 0 || *decision > 0:
			if *proposal >= 0 && *decision > 0 {
				log.Fatal("hlwatch check: -proposal or -decision, not both")
			}
			if *target != "" || *codehash != "" || *proxy != "" || *call != "" {
				log.Fatal("hlwatch check: -proposal and -decision read the decision's own parameters: drop -target, -codehash, -proxy and -call")
			}
			if *proposal >= 0 {
				spec, err = w.ProposalSpec(ctx, *proposal)
			} else {
				spec, err = w.DecisionSpec(ctx, *decision)
			}
		default:
			spec, err = typedSpec(*kind, *target, *codehash, *proxy, *call)
		}
		if err != nil {
			log.Fatal(err)
		}
		if w.CreationTxs, err = creationTxs(cts, spec.Target.Hex()); err != nil {
			log.Fatal(err)
		}
		F, err := w.Pair().Finalized(ctx)
		if err != nil {
			log.Fatal(err)
		}
		text, err := w.TargetCheck(ctx, ethx.AtHash(F.Hash), spec)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(text)
		fmt.Println(agent.FinalizedAgeLine(F, time.Now())) // the block it was read at, and its age (E6-GT4)
	default:
		log.Fatalf("unknown command %s", cmd)
	}
}

// buildHash is the running binary's sha256 and what the Go toolchain recorded of its source (the reproducible build
// hash the launch notice pins).
func buildHash() string {
	out := "hlwatch build: "
	if exe, err := os.Executable(); err == nil {
		if f, err := os.Open(exe); err == nil {
			h := sha256.New()
			if _, err := io.Copy(h, f); err == nil {
				out += "sha256 " + hex.EncodeToString(h.Sum(nil))
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

// readBuilds reads each -build file: forge's artifact — its runtime with its immutables' places, named from its out
// directory's ASTs (or the release compile's names), its creation code, its ABI (repro.ReadBuildFile) — or a file
// holding a runtime's keccak256, which matches only code with no immutable.
func readBuilds(bs builds) (map[string]agent.Build, error) {
	out := map[string]agent.Build{}
	for label, file := range bs {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		s := strings.TrimSpace(string(b))
		if len(strings.TrimPrefix(s, "0x")) == 64 {
			out[label] = agent.Build{Hash: common.HexToHash(s)}
			continue
		}
		var probe struct {
			DeployedBytecode struct {
				Object string `json:"object"`
			} `json:"deployedBytecode"`
		}
		if err := json.Unmarshal(b, &probe); err != nil || probe.DeployedBytecode.Object == "" {
			return nil, fmt.Errorf("-build %s: neither a code hash nor forge's artifact with a deployedBytecode", file)
		}
		a, err := repro.ReadBuildFile(file)
		if err != nil {
			return nil, fmt.Errorf("-build %s: %w", file, err)
		}
		out[label] = agent.BuildOf(a)
	}
	return out, nil
}

// typedSpec is a check's parameters typed by hand: the target, the code hash and, for a kind 8, the proxy and the call
// — both needed (-call none for a bare upgrade), so a reading never says "no call (ProxyAdmin.upgrade)" for a decision
// that carries one, nor holds a build to a proxy the decision does not upgrade (E3-GT7).
func typedSpec(kind uint, target, codehash, proxy, call string) (agent.TargetSpec, error) {
	if kind != 8 && kind != 9 {
		return agent.TargetSpec{}, errors.New("hlwatch check: -kind is 8 or 9")
	}
	if target == "" {
		return agent.TargetSpec{}, errors.New("hlwatch check: -proposal ID (a GovDAO proposal of r/hlgov's builder), -decision N (one r/hlgov dispatched), or -target typed by hand")
	}
	addr, err := ethx.ParseAddress(target)
	if err != nil {
		return agent.TargetSpec{}, fmt.Errorf("-target: %w", err)
	}
	s := agent.TargetSpec{Kind: byte(kind), Target: addr, From: "as typed"}
	if codehash != "" {
		h, err := hexutil.Decode(ensure0x(codehash))
		if err != nil || len(h) != 32 {
			return agent.TargetSpec{}, errors.New("-codehash is 0x and 64 hex digits")
		}
		s.CodeHash = common.BytesToHash(h)
	}
	if kind == 9 {
		if proxy != "" || call != "" {
			return agent.TargetSpec{}, errors.New("hlwatch check: -proxy and -call are a kind 8's")
		}
		return s, nil
	}
	if proxy == "" || call == "" {
		return agent.TargetSpec{}, errors.New("hlwatch check -kind 8: -proxy and -call are needed (-call none for a bare upgrade) — or read them from the decision itself: -proposal ID before the vote, -decision N once r/hlgov dispatched it")
	}
	if s.Proxy, err = ethx.ParseAddress(proxy); err != nil {
		return agent.TargetSpec{}, fmt.Errorf("-proxy: %w", err)
	}
	if call != "none" {
		if s.Call, err = hexutil.Decode(ensure0x(call)); err != nil || len(s.Call) == 0 {
			return agent.TargetSpec{}, fmt.Errorf("-call is calldata in hex, or none for a bare upgrade, not %q", call)
		}
	}
	return s, nil
}

func ensure0x(s string) string {
	if strings.HasPrefix(s, "0x") {
		return s
	}
	return "0x" + s
}

// loop reads both chains every period and alarms the holder on every change: a flag set or cleared anywhere, a
// decision pending or gone on either chain, an identity's verdict, the counters, every standing alarm of the watch.
func loop(ctx context.Context, w *agent.Watch, alarm *agent.Alarmer, every time.Duration) {
	var last *agent.WatchView
	for {
		v, err := w.View(ctx)
		if err != nil {
			log.Print(err)
			alarm.Alarm("watch:no-verdict", "no verdict: "+err.Error())
		} else {
			alarm.Clear("watch:no-verdict")
			if last == nil {
				fmt.Print(v.Text(buildHash()))
			} else {
				diff(alarm, last, v)
			}
			last = v
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func diff(alarm *agent.Alarmer, a, b *agent.WatchView) {
	lines := func(v *agent.WatchView) map[string]string {
		out := map[string]string{}
		for _, path := range sortedKeys(v.Mailbox.Routes) {
			for _, f := range v.Mailbox.Routes[path].StandingFlags() {
				out[fmt.Sprintf("flag:%s:%s:%d", path, f.Fn, f.Seat)] = fmt.Sprintf("%s flagged on %s by seat %d (%s)", f.Fn, path, f.Seat, f.Addr)
			}
		}
		if v.Gate != nil {
			for i, f := range v.Gate.Flags {
				if f.Set() {
					out[fmt.Sprintf("flag:gate:%d", i+1)] = fmt.Sprintf("the gate flagged by slot %d (%s) at block %d", i+1, v.Gate.Keys[i].Hex(), f.BlockNumber)
				}
			}
		}
		if v.Admin != nil {
			for i, f := range v.Admin.Flags {
				if f.Set() {
					out[fmt.Sprintf("flag:admin:%d", i+1)] = fmt.Sprintf("HlAdmin flagged by slot %d (%s) at block %d", i+1, v.Admin.Keys[i].Hex(), f.BlockNumber)
				}
			}
		}
		for _, d := range v.Pending {
			out[fmt.Sprintf("pending:%s#%d", d.Realm, d.ID)] = fmt.Sprintf("%s #%d %s pending: %s", d.Realm, d.ID, d.Kind, d.Text)
		}
		for _, d := range v.Decisions {
			out[fmt.Sprintf("pending:hladmin#%d", d.Number)] = fmt.Sprintf("HlAdmin #%d kind %d pending: %s", d.Number, d.Kind, d.Text)
		}
		for sym, text := range v.Identity {
			out["identity:"+sym] = text
		}
		return out
	}
	was, now := lines(a), lines(b)
	for k, text := range now {
		if old, ok := was[k]; !ok || (strings.HasPrefix(k, "identity:") && verdictOf(old) != verdictOf(text)) {
			alarm.Alarm("watch:"+k, text)
		}
	}
	for k := range was {
		if _, ok := now[k]; !ok {
			alarm.Clear("watch:" + k)
			log.Printf("hlwatch: gone: %s", was[k])
		}
	}
}

func verdictOf(text string) string { v, _, _ := strings.Cut(text, ":"); return v }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// stroke sets (or clears) the holder's flag on hlmailbox, then on the gate and on HlAdmin — two Ethereum transactions,
// each confirmed final (its block at or below the finalized block, still canonical, the flag read there), either
// re-sent when a reorganisation dropped it. Each of the three legs is sent whatever another answered (the plan
// conformance audit's E6-PC4, round 6): gno.land's leg went first and any failure returned — a gno key short of a sized
// flag's simulation fee, the holder's node down, gno.land halted — before the Ethereum brake the plan leans on when
// gno.land is the trouble, and an Ethereum key no object seats stopped gno.land's flag too. Every leg's outcome is
// printed; the error names each leg that failed (hlwatch exits non-zero).
func stroke(ctx context.Context, w *agent.Watch, cfg *agent.Config, node string, set bool, home, key, ethKey, route, fn, ids string) error {
	path := route
	if r := cfg.RouteBySymbol(strings.ToUpper(route)); r != nil {
		path = r.Gno
	}
	if fn != "sends" && fn != "credits" && fn != "payouts" {
		return fmt.Errorf("-fn is sends, credits or payouts, not %q", fn)
	}
	if ids != "" && (fn != "payouts" || !set) {
		return errors.New("only a payouts flag names ids")
	}
	verb := map[bool]string{true: "set", false: "cleared"}[set]
	c := cfg.SourceClient(node)
	c.Home, c.Key = home, key
	eth := &ethLegs{pair: w.Pair(), cfg: cfg, ethKey: ethKey, set: set}
	legs := []struct {
		name string
		run  func() (string, error)
	}{
		{"gno.land (hlmailbox, " + fn + " on " + path + ")", func() (string, error) { return gnoLeg(ctx, c, cfg.Gno.Mailbox, set, path, fn, ids) }},
		{"the gate", func() (string, error) { return eth.gate(ctx) }},
		{"HlAdmin", func() (string, error) { return eth.admin(ctx) }},
	}
	var failed []string
	for _, l := range legs {
		res, err := l.run()
		if err != nil {
			fmt.Printf("%s: the flag is NOT %s: %v\n", l.name, verb, err)
			failed = append(failed, l.name+": "+err.Error())
			continue
		}
		fmt.Printf("%s: %s\n", l.name, res)
	}
	if len(failed) > 0 {
		return fmt.Errorf("the flag is not %s on %d of the 3 objects (every leg was sent whatever another answered) — %s", verb, len(failed), strings.Join(failed, "; "))
	}
	return nil
}

// gnoLeg is a stroke's gno.land leg: the holder's seat read from hlmailbox through its node, then its flag set or
// cleared there (flagGno).
func gnoLeg(ctx context.Context, c *gno.Client, mailbox string, set bool, path, fn, ids string) (string, error) {
	addr, err := c.KeyAddress(ctx)
	if err != nil {
		return "", err
	}
	mb, err := gnoside.MailboxAt(ctx, c, mailbox, 0)
	if err != nil {
		return "", err
	}
	seat := 0
	for i, s := range mb.Seats {
		if s == addr {
			seat = i + 1
		}
	}
	if seat == 0 {
		return "", fmt.Errorf("%s (%s) holds no seat in hlmailbox: only a guardian or a watch key flags", c.Key, addr)
	}
	r, err := flagGno(ctx, c, mailbox, set, path, fn, ids)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s on %s by seat %d (%s): %s", fn, map[bool]string{true: "set", false: "cleared"}[set], path, seat, addr, r.Hash), nil
}

// ethLegs are a stroke's two Ethereum legs, the gate's and HlAdmin's: the holder's key and a sender over the config's
// providers, made at the first leg (a failure there is both legs'), then each object read at the finalized block for
// the key's slot there and its flag stroked (finalStroke) — one leg's failure is never the other's.
type ethLegs struct {
	pair   ethx.Pair
	cfg    *agent.Config
	ethKey string
	set    bool
	made   bool
	me     common.Address
	snd    *ethx.Sender
	err    error
}

func (e *ethLegs) sender(ctx context.Context) error {
	if e.made {
		return e.err
	}
	e.made = true
	k, err := ethx.LoadKey(e.ethKey)
	if err != nil {
		e.err = fmt.Errorf("the Ethereum key: %w", err)
		return e.err
	}
	e.me = ethx.KeyAddress(k)
	var clients []*ethx.Client
	for _, p := range e.cfg.Eth.Providers {
		cl, err := ethx.Dial(ctx, p)
		if err != nil {
			e.err = err
			return err
		}
		clients = append(clients, cl)
	}
	e.snd, e.err = ethx.NewSender(k, e.cfg.EthChainID(), clients, e.cfg.SendConfig(""))
	return e.err
}

func (e *ethLegs) gate(ctx context.Context) (string, error) {
	return e.stroke(ctx, "the gate", ethx.Gate, func(at ethx.At) (common.Address, [6]common.Address, error) {
		g, err := ethx.ReadGate(ctx, e.pair, agent.Address(e.cfg.Eth.Gate), at)
		if err != nil {
			return common.Address{}, [6]common.Address{}, err
		}
		return g.Address, g.Keys, nil
	})
}

// admin is HlAdmin's leg, its successor line followed (liveAdmin).
func (e *ethLegs) admin(ctx context.Context) (string, error) {
	return e.stroke(ctx, "HlAdmin", ethx.Admin, func(at ethx.At) (common.Address, [6]common.Address, error) {
		a, err := liveAdmin(ctx, e.pair, agent.Address(e.cfg.Eth.Admin), at)
		if err != nil {
			return common.Address{}, [6]common.Address{}, err
		}
		return a.Address, a.Keys, nil
	})
}

// stroke reads the object at the finalized block, finds the key's slot and sends its flag (finalStroke).
func (e *ethLegs) stroke(ctx context.Context, name string, c *ethx.Contract, read func(ethx.At) (common.Address, [6]common.Address, error)) (string, error) {
	if err := e.sender(ctx); err != nil {
		return "", err
	}
	F, err := e.pair.Finalized(ctx)
	if err != nil {
		return "", err
	}
	addr, keys, err := read(ethx.AtHash(F.Hash))
	if err != nil {
		return "", err
	}
	slot := -1
	for i, a := range keys {
		if a == e.me {
			slot = i
			break
		}
	}
	if slot < 0 {
		return "", fmt.Errorf("%s holds no slot on %s: only a guardian or a watch key flags", e.me.Hex(), name)
	}
	method := "setFlag"
	if !e.set {
		method = "clearFlag"
	}
	if err := finalStroke(ctx, e.pair, e.snd, e.cfg, name, addr, c, method, slot, e.set); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s by slot %d (%s), final", method, slot+1, e.me.Hex()), nil
}

// flagGas is the gas the holder's flag call is first simulated under on gno.land (a flag naming no ids takes a few
// million).
const flagGas = 45_000_000

// flagGno sets (or clears) the holder's flag on hlmailbox. A guardian's payouts flag may name the ids it suspects,
// and on a gno v1.5.1 node SetFlag naming 256 ids, hlmailbox's most, costs ≈475M (the list kept sorted,
// fold-r6/f18b.md; ≈555M in round 5, 64 ids ≈156M), past the 180M the client reaches from 45M — the tool set ≈70 ids
// at most then (realms.md, audit round 5, "for the lead") — so the call is sized from its simulation, its ceiling
// raised as far as the block's gas (gnoside.CallUpToTheBlock); a clear the same way (TestI4FlagOf256IdsIsSent).
func flagGno(ctx context.Context, c gnoside.Caller, mailbox string, set bool, path, fn, ids string) (*gno.Result, error) {
	if set {
		return gnoside.CallUpToTheBlock(ctx, c, mailbox, "SetFlag", flagGas, path, fn, ids)
	}
	return gnoside.CallUpToTheBlock(ctx, c, mailbox, "ClearFlag", flagGas, path, fn)
}

// liveAdmin is HlAdmin's live program, its successor line followed.
func liveAdmin(ctx context.Context, r ethx.Reader, start common.Address, at ethx.At) (*ethx.AdminState, error) {
	cur := start
	for hop := 0; hop < 5; hop++ {
		st, err := ethx.ReadAdmin(ctx, r, cur, at)
		if err != nil {
			return nil, err
		}
		if !st.Retired() {
			return st, nil
		}
		cur = st.Successor
	}
	return nil, errors.New("HlAdmin's line runs past four successors: edit eth.admin in the config")
}

// finalStroke sends one flag transaction and waits until it is final: its block at or below the pair's finalized
// block, its block hash still the canonical one there, the flag read as wanted at that block; a transaction a
// reorganisation dropped is sent again (three tries).
func finalStroke(ctx context.Context, pair ethx.Pair, snd *ethx.Sender, cfg *agent.Config, name string, to common.Address, c *ethx.Contract, method string, slot int, set bool) error {
	data, err := c.Pack(method)
	if err != nil {
		return err
	}
	for try := 1; try <= 3; try++ {
		rc, err := snd.Send(ctx, ethx.Call{To: to, Data: data, Label: method + " on " + name, MaxBaseFee: cfg.EmergencyMaxBaseFee()})
		if err != nil {
			return err
		}
		block := rc.BlockNumber.Uint64()
		fmt.Printf("%s on %s (slot %d): %s in block %d, waiting for it to be final\n", method, name, slot+1, rc.TxHash.Hex(), block)
		deadline := time.Now().Add(30 * time.Minute)
		for {
			F, err := pair.Finalized(ctx)
			if err == nil && F.Number >= block {
				h, herr := pair.CanonicalHash(ctx, block)
				if herr == nil && h != rc.BlockHash {
					fmt.Printf("%s on %s: block %d was reorganised away: sending it again\n", method, name, block)
					break
				}
				flags, ferr := readFlags(ctx, pair, to, c, ethx.AtHash(F.Hash))
				if ferr == nil && flags[slot].Set() == set {
					fmt.Printf("%s on %s is final (block %d at or below the finalized block %d)\n", method, name, block, F.Number)
					return nil
				}
				if ferr == nil {
					return fmt.Errorf("%s landed at block %d, final, but the flag reads %v at the finalized block %d (another stroke since?)", method, block, flags[slot].Set(), F.Number)
				}
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%s at block %d is not final after 30 minutes (a finality stall?)", method, block)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
	}
	return fmt.Errorf("%s on %s was dropped three times", method, name)
}

func readFlags(ctx context.Context, r ethx.Reader, addr common.Address, c *ethx.Contract, at ethx.At) ([6]ethx.Flag, error) {
	if c == ethx.Gate {
		g, err := ethx.ReadGate(ctx, r, addr, at)
		if err != nil {
			return [6]ethx.Flag{}, err
		}
		return g.Flags, nil
	}
	a, err := ethx.ReadAdmin(ctx, r, addr, at)
	if err != nil {
		return [6]ethx.Flag{}, err
	}
	return a.Flags, nil
}

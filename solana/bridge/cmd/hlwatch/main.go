// Command hlwatch is the guardians' tool (PLAN.md §5.8): from the holder's own gno node and two agreeing Solana
// providers it shows the flags and the seats on all three objects (hlmailbox, the pausable ISM, the admin
// program), who governs (the compiled governor until the yield's height, GovDAO after; M1k), every pending
// decision and when it matures, every builder-made GovDAO proposal with no execution (pre-armed ones marked —
// "awaits the yield" while the governor has not yielded — and ones past their 14 days), the supply identity from the holder's own sources with
// the dispatch index behind it, the client's accepted and proof headers against gno.land's, r/hlgov's counter
// against the admin program's, every foreign decision, and every void as forged whose dispatch record matches;
// `watch` alarms the holder on every change;
// `flag`/`unflag` set the holder's flag on hlmailbox and on both Solana objects in one stroke, a payouts flag
// naming the ids it suspects with each id's verdict (forged, or unindexed: re-read); `finish` denies a
// builder-made proposal past its deadline for good through ExecuteOrRejectProposal.
//
//	hlwatch status -config bridge.json -node http://127.0.0.1:26657 [-state hlwatch.json]
//	hlwatch watch  -config bridge.json -node http://127.0.0.1:26657 [-state hlwatch.json] [-every 10s] [-webhook URL]
//	hlwatch flag   -config bridge.json -node URL -home DIR -key NAME -sol-key FILE -route BUBBLE|GNOT|pkgpath -fn sends|credits|payouts [-ids a,b]
//	hlwatch unflag -config bridge.json -node URL -home DIR -key NAME -sol-key FILE -route ... -fn ...
//	hlwatch finish -config bridge.json -node URL -home DIR -key NAME [-pid N]
//	hlwatch version
//
// Every outsider compiles their own; the build hash (printed by `version` and at the head of every status) is in
// the launch notice (§5.8). Without a node, or without two providers in the config, it refuses: on anything less
// it would alarm only, and its payouts flag command refuses (§5.8). A section the two providers give differently
// is read from the first alone and printed UNCONFIRMED; the flag command names no id on such a reading.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hlwatch status|watch|flag|unflag|finish|version [flags]")
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
	state := fs.String("state", "", "the dispatch index and the client's record between runs (a file; \"\" keeps none)")
	home := fs.String("home", "", "flag/unflag/finish: gnokey's keybase")
	key := fs.String("key", "", "flag/unflag/finish: the holder's gno key, a guardian's or a watch key's")
	solKey := fs.String("sol-key", "", "flag/unflag: the holder's Solana key file (the same seat on the pausable ISM and the admin program)")
	route := fs.String("route", "", "flag/unflag: a route by symbol (BUBBLE, GNOT) or pkgpath")
	fn := fs.String("fn", "", "flag/unflag: sends, credits or payouts")
	ids := fs.String("ids", "", "flag: a guardian's payouts flag may name the message ids it suspects (comma separated)")
	pid := fs.Int64("pid", 0, "finish: the proposal to finish (0: every builder-made one past its deadline by this state's first sight)")
	every := fs.Duration("every", 10*time.Second, "watch: how often to look")
	webhook := fs.String("webhook", "", "watch: alarms as one JSON POST each")
	supermajority := fs.Float64("supermajority", agent.DefaultSupermajority, "the YES percent GovDAO's law needs")
	recordFrom := fs.String("record-from", "", "status/watch: where an empty record starts — \"client\" takes the client's own counter and running hash as the base (an install on a provider without the full history, G29); by default the record is built from the deploy")
	_ = fs.Parse(args)

	log.SetOutput(agent.RedactWriter{W: os.Stderr}) // no provider URL's key in a log line (G16)
	cfg, err := agent.LoadConfig(*config)
	if err != nil {
		log.Fatal(err)
	}
	if *node == "" {
		log.Fatal("hlwatch: -node is required: the holder's own gno node (PLAN.md §5.8: on anything less it alarms only and the flag command refuses)")
	}
	urls, err := cfg.TwoProviders()
	if err != nil {
		log.Fatal("hlwatch: two Solana providers are required in the config (solana.rpcs): " + err.Error())
	}
	st, err := agent.LoadWatchState(*state)
	if err != nil {
		log.Fatal(err)
	}
	w := &watcher{cfg: cfg, gno: cfg.SourceClient(*node), nodeURL: *node, super: *supermajority, providers: [2]*rpc.Client{sol.NewRPC(urls[0]), sol.NewRPC(urls[1])}, urls: urls, st: st, statePath: *state}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *recordFrom != "" {
		// an empty record starts from the client's own counter (G29: a provider without the full history never
		// built it from the deploy); the headers before the checkpoint are not checked, those after it are
		if text, err := agent.RecordCheckpoint(ctx, w.providers, w.gno, cfg, st, *recordFrom); err != nil {
			log.Fatal(err)
		} else if text != "" {
			fmt.Println(text)
			_ = st.Save(*state)
		}
	}
	switch cmd {
	case "status":
		v, err := w.view(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(v.text())
		_ = st.Save(*state)
	case "watch":
		w.alarm = &agent.Alarmer{URL: *webhook, Source: "hlwatch"}
		w.loop(ctx, *every)
	case "flag", "unflag":
		if *home == "" || *key == "" || *route == "" || *fn == "" || *solKey == "" {
			log.Fatal("hlwatch: -home, -key, -sol-key, -route and -fn are needed")
		}
		if err := w.flag(ctx, cmd == "flag", *home, *key, *solKey, *route, *fn, *ids); err != nil {
			log.Fatal(err)
		}
		_ = st.Save(*state)
	case "finish":
		if *home == "" || *key == "" {
			log.Fatal("hlwatch: -home and -key are needed")
		}
		if err := w.finish(ctx, *home, *key, *pid); err != nil {
			log.Fatal(err)
		}
		_ = st.Save(*state)
	default:
		log.Fatalf("unknown command %s", cmd)
	}
}

// buildHash is the running binary's sha256 and what the Go toolchain recorded of its source (the reproducible
// build hash of §5.8: each outsider compiles their own and compares; agent.BuildHash, which hlrecover prints too,
// K36).
func buildHash() string { return agent.BuildHash("hlwatch") }

type watcher struct {
	cfg       *agent.Config
	gno       agent.GnoReader // the holder's own node (a fake in tests)
	nodeURL   string          // its URL, for the commands that sign through it
	super     float64
	alarm     *agent.Alarmer
	last      *view
	providers [2]*rpc.Client
	urls      [2]string
	st        *agent.WatchState
	statePath string
}

// view is one reading of both sides at one gno height.
type view struct {
	At        time.Time
	Head      gno.Head
	Mailbox   agent.MailboxView
	Routes    []agent.RouteView
	Pending   []agent.PendingDecision
	Proposals []agent.Proposal
	Gov       agent.GovView
	GovRecent []agent.GovDecision // r/hlgov's last dispatched decisions, highest first (a cancel with its salt)
	GovErr    string
	DAOErr    string
	Sol       *agent.SolanaView
	SolErr    string
	Seen      map[string]int64 // proposal id → first seen open
	// Lines are each token's line of migrations at the view's height, by symbol, first realm to last (G48: the
	// line's realms' proposals and queued decisions are listed and checked, the configured realm being the
	// predecessor after a migration).
	Lines    map[string][]string
	Switches []switchNote // r/hlgov's recipient switch against the admin program's kind 9 (AP3)
	// Findings are the proposal checks that alarm the holder while the proposal stands (AR1: a refund before kind
	// 6 or of a delivered send; S-20: a successor with decisions pending; S-21: a successor of another layout),
	// keyed so a standing one posts once and clears when the proposal closes.
	Findings []switchNote
}

func (w *watcher) view(ctx context.Context) (*view, error) {
	// §5.9's order (G33): the Solana snapshot first, then the node's head after it — a catching-up node, a head
	// not after the snapshot or a stale one is no reading of the chain, never a BROKEN line
	var snap *agent.Snapshot
	if w.cfg.HasPrograms() || len(w.cfg.Routes) > 0 {
		var err error
		if snap, err = agent.TakeSnapshot(ctx, w.providers, w.cfg); err != nil {
			return nil, fmt.Errorf("the Solana snapshot: %w", err)
		}
	}
	snapAt := time.Now()
	if snap != nil {
		snapAt = snap.At
	}
	head, err := agent.HeadAfter(ctx, w.gno, snapAt)
	if err != nil {
		return nil, fmt.Errorf("the node: %w", err)
	}
	if ok, why := agent.JudgeOwnHead(head, snapAt, time.Now()); !ok {
		return nil, fmt.Errorf("no verdict: %s (the node's head is read after the Solana snapshot and must be a reading of the chain, §5.9)", why)
	}
	H := head.Height
	v := &view{At: time.Now(), Head: head}
	if v.Mailbox, err = agent.ReadMailbox(ctx, w.gno, w.cfg.Gno.Mailbox, H); err != nil {
		return nil, err
	}
	for _, r := range w.cfg.Routes {
		rv, err := agent.ReadRoute(ctx, w.gno, r, H)
		if err != nil {
			return nil, err
		}
		v.Routes = append(v.Routes, rv)
	}
	// the lines of migrations at H (G48): the proposals and queued decisions of every realm of each line are
	// listed and checked, the configured realm being the predecessor after a migration
	var extra []string
	v.Lines, extra = agent.LineRealms(ctx, w.gno, w.cfg, H)
	if v.Pending, err = agent.ReadPending(ctx, w.gno, w.cfg, H, extra...); err != nil {
		return nil, err
	}
	if w.cfg.Gno.Gov != "" {
		if v.Gov, err = agent.ReadGov(ctx, w.gno, w.cfg.Gno.Gov, H); err != nil {
			return nil, err
		}
		if v.GovRecent, err = agent.ReadGovDecisions(ctx, w.gno, w.cfg.Gno.Gov, v.Gov.Last, 5, H); err != nil {
			v.GovErr = err.Error()
		}
	}
	v.Proposals, v.DAOErr = agent.ReadProposals(ctx, w.gno, w.cfg, H, w.super, extra...)
	open := map[string]bool{}
	for _, p := range v.Proposals {
		pk := strconv.FormatInt(p.ID, 10)
		open[pk] = true
		if _, ok := w.st.ProposalsSeen[pk]; !ok {
			w.st.ProposalsSeen[pk] = v.At.Unix()
		}
	}
	for pk := range w.st.ProposalsSeen {
		if !open[pk] {
			delete(w.st.ProposalsSeen, pk)
		}
	}
	v.Seen = w.st.ProposalsSeen
	sv, err := agent.ReadSolanaAt(ctx, w.providers, w.gno, w.cfg, w.st, snap, H, v.At)
	if err != nil {
		v.SolErr = err.Error()
	}
	v.Sol = sv
	w.programChecks(ctx, v)
	return v, nil
}

var (
	upgradeTextRE   = regexp.MustCompile(`upgrade ([0-9a-f]{64}) from buffer ([0-9a-f]{64})`)
	transferTextRE  = regexp.MustCompile(`transfer every authority to ([0-9a-f]{64})`)
	recipientTextRE = regexp.MustCompile(`set decision recipient to ([0-9a-f]{64})`)
	migrateTextRE   = regexp.MustCompile(`migrate to (gno\.land/r/[^\s()]+)`)
	refundTextRE    = regexp.MustCompile(`refund (.+?) of sends imported from the predecessor .*?to their senders: ([0-9a-fA-Fx, ]+)`)
	// GovDAO's apply of a queued refund (AT2, M1j's NewApplyRefundRequest): the title is "<realm>: apply refund"
	// and the description "apply refund decision N at once (<the refund decision's text, ids included>), after
	// hlwatch's check that the token's router on Solana is this realm" — read by the title first, since the
	// description carries the refund's own text and refundTextRE would read it as a refund proposal
	applyRefundTextRE = regexp.MustCompile(`^apply refund decision (\d+) at once`)
	maximumTextRE   = regexp.MustCompile(`^set maximum to ([0-9]+(?:\.[0-9]+)?) [A-Z]+ \(now `)
	enrolTextRE     = regexp.MustCompile(`enroll router for token (\d+), domain (\d+): ([0-9a-f]{64})`)
	restoreTextRE   = regexp.MustCompile(`^restore: ([0-9a-fA-Fx, ]+)`)
)

// enrolCheck is a kind 6 proposal read against the mailbox (M1i: R35, R36): the router it names must be a
// registered route of the token's own — the launch route by the config's symbol, a successor by the route it
// announced for — neither migrated nor dropped, and a successor only once it pulled page 0 (a migrate that
// pulled nothing may be re-pointed, and Solana must never name a dropped route); each failure is what the
// closure refuses, read here before the vote. The second answer says whether the holder is alarmed.
func enrolCheck(mb agent.MailboxView, cfg *agent.Config, token int, routerHex string) (string, bool) {
	tokenSym := "token " + strconv.Itoa(token)
	if sym := [2]string{"GNOT", "BUBBLE"}; token >= 0 && token < len(sym) {
		tokenSym = sym[token]
	}
	key, err := hl.ParseKey32(routerHex)
	if err != nil {
		return "the router could not be read (" + err.Error() + ")", false
	}
	path := ""
	for _, p := range mb.RoutePaths {
		if agent.RealmKey(p) == key {
			path = p
		}
	}
	for p, pred := range mb.DroppedRoutes {
		if agent.RealmKey(p) == key {
			return "the router " + p + " is A DROPPED ROUTE (re-pointed away by " + pred + "): the closure refuses it; enrol the line's live successor (AS1)", true
		}
	}
	if path == "" {
		return "the router " + routerHex + " is NOT A REGISTERED ROUTE: the closure refuses it (a kind 6 names the token's own live route)", true
	}
	sym := mb.SuccessorRoutes[path]
	for _, r := range cfg.Routes {
		if r.Gno == path {
			sym = r.Symbol
		}
	}
	switch {
	case sym == "":
		return "the router " + path + " is registered for no token (it never announced the route it succeeds): the closure refuses it", true
	case sym != tokenSym:
		return "the router " + path + " is THE " + sym + " ROUTE'S, NOT " + tokenSym + "'S: the closure refuses it (R36)", true
	}
	for _, s := range mb.Successions {
		if strings.HasPrefix(s, path+" → ") {
			return "the router " + path + " is A MIGRATED ROUTE (" + s + "): the closure refuses it, since its successor is the token's router", true
		}
	}
	if pred := mb.Predecessors[path]; pred != "" {
		if !mb.Pulled[path] {
			return "the router " + path + " is NOT PULLED: page 0 is not in (" + path + " succeeds " + pred + " and has pulled nothing): the closure refuses it — a migrate that pulled nothing may be re-pointed, and Solana must never name a dropped route (R35)", true
		}
		return "the router " + path + " is the " + tokenSym + " route, a successor that pulled page 0: the closure takes it", false
	}
	return "the router " + path + " is the " + tokenSym + " route, live and registered: the closure takes it", false
}

// maximumCheck is a route's per-message-maximum proposal read against the pausable ISM's live maximum for the
// token (AS4): the realm cannot read Solana, and a raise above the ISM's strands every send between the two on
// Solana (OverMax until a kind 12) — §5.4's order is the kind 12 first. amount is the proposal's figure in
// whole tokens as the builder writes it (six decimals of base units); ism the ISM's maximum. The second answer
// says whether the holder is alarmed.
func maximumCheck(amount string, ism uint64) (string, bool) {
	whole, frac, _ := strings.Cut(amount, ".")
	n, err := strconv.ParseUint(whole, 10, 64)
	if err != nil {
		return "the maximum could not be read from the proposal (" + amount + ")", false
	}
	base := n * 1_000_000
	if frac != "" {
		frac = (frac + "000000")[:6]
		f, _ := strconv.ParseUint(frac, 10, 64)
		base += f
	}
	switch {
	case base > ism:
		return fmt.Sprintf("RAISE ABOVE THE SOLANA MAXIMUM: %d base units against the pausable ISM's %d — sends between would strand on Solana (OverMax, parked for good until a kind 12 raises the ISM's; §5.4: Solana first, gno.land after; AS4): vote the kind 12 first", base, ism), true
	case base == ism:
		return fmt.Sprintf("%d base units, at the pausable ISM's %d: no send strands", base, ism), false
	}
	return fmt.Sprintf("%d base units, under the pausable ISM's %d: no send strands", base, ism), false
}

// refundCheck is a successor's refund — a proposal, or a queued decision — read against what the realm cannot
// see. First the gate on Solana (F27, AS2: the text and verdict RefundGate made of the token program's enrolled
// router and the kind 6's state — the refund waits on the router, whatever the note says, since the note's days
// always expire before a kind 6 can execute there, and a cancelled, held or lost kind 6 leaves the note
// standing). Then hlmailbox's note of the kind 6 that enrolled the successor (AR1): missing, the realm refuses;
// younger than the realm's 14-day wait (R37: twice the realms' delay, a week for the delivery and a week for
// Solana's clock), the realm refuses until then — and, beside each id, DELIVERED when Solana has processed it
// already (a refund of a delivered send is the same double mint, cured by the write-off alone). now is the
// view's chain time; blockTime reads a block's time by height when the note names no time; processed is the
// Solana side's processed ids (nil when it was not read). The second answer says whether the holder is alarmed.
func refundCheck(mb agent.MailboxView, realm, symbol string, ids []string, processed map[string]bool, now time.Time, blockTime func(int64) (time.Time, error), gate string, gateOK bool) (string, bool) {
	delay := 7 * 24 * time.Hour
	if mb.Clocks.Delay > 0 {
		delay = time.Duration(mb.Clocks.Delay) * time.Second
	}
	wait := 2 * delay // the realm's 14-day wait on the note (R37)
	alarm := !gateOK
	parts := []string{gate}
	e, ok := mb.Enrolled[realm]
	switch {
	case !ok:
		alarm = true
		parts = append(parts, "NO NOTE: no kind 6 enrolling "+realm+" as the "+symbol+" router was dispatched from r/hlgov (hlmailbox holds no enrolment note for it): the realm refuses the refund until that note is 14 days old (R37) — vote kind 6 first")
	default:
		at := time.Unix(e.At, 0)
		known := e.At > 0
		if !known && blockTime != nil {
			if t, err := blockTime(e.Height); err == nil {
				at, known = t, true
			}
		}
		switch {
		case !known:
			alarm = true
			parts = append(parts, fmt.Sprintf("the note is at height %d, whose time could not be read: the realm refuses the refund until the note is 14 days old (R37)", e.Height))
		case now.Sub(at) < wait:
			alarm = true
			parts = append(parts, fmt.Sprintf("THE NOTE IS %s OLD, UNDER THE REALM'S 14-DAY WAIT (height %d at %s; R37): the realm refuses the refund until %s — a vote now lands it no earlier", now.Sub(at).Round(time.Minute), e.Height, at.UTC().Format(time.RFC3339), at.Add(wait).UTC().Format(time.RFC3339)))
		default:
			parts = append(parts, fmt.Sprintf("the note is %s old (height %d at %s), past the realm's 14-day wait (R37): the realm takes the refund", now.Sub(at).Round(time.Hour), e.Height, at.UTC().Format(time.RFC3339)))
		}
	}
	var lines []string
	for _, id := range ids {
		id = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "0x"))
		if id == "" {
			continue
		}
		switch {
		case processed == nil:
			lines = append(lines, id+" (delivery not judged: the Solana side was not read)")
		case processed[id]:
			alarm = true
			lines = append(lines, id+" DELIVERED (Solana processed it already: a refund would be a double mint, cured only by the write-off builder, AR1)")
		default:
			lines = append(lines, id+" undelivered")
		}
	}
	if len(lines) > 0 {
		parts = append(parts, "ids: "+strings.Join(lines, "; "))
	}
	return strings.Join(parts, "; "), alarm
}

// isApplyRefund tells an apply-refund proposal (AT2) by its title first — "<realm>: apply refund", which the
// builder writes as "<realm name>: " + the kind — then by its description's head; a refund proposal's own text
// sits inside the apply's description, so this is asked before refundTextRE is.
func isApplyRefund(p *agent.Proposal) bool {
	return strings.HasSuffix(strings.TrimSpace(p.Title), ": apply refund") || applyRefundTextRE.MatchString(p.Text)
}

// applyRefundCheck is GovDAO's apply of a queued refund (AT2: the realm's Execute refuses the kind, the
// apply-refund builder applies it at once, and the realm cannot see Solana) read against the decision it names
// on its realm: the decision's ids through refundCheck's gate — the token's enrolled router on Solana, the note,
// the delivered ids — with its maturity (the closure executes it only past its delay); NO QUEUED REFUND when the
// realm holds none pending under that id (applied, cancelled or never made), NOT A REFUND when the id is
// another kind's — each what the closure would refuse. The second answer says whether the holder is alarmed.
func applyRefundCheck(pending []agent.PendingDecision, mb agent.MailboxView, realm, symbol string, id int64, processed map[string]bool, now time.Time, blockTime func(int64) (time.Time, error), gate string, gateOK bool) (string, bool) {
	for _, d := range pending {
		if d.Realm != realm || d.ID != id {
			continue
		}
		if d.Kind != "refund" {
			return fmt.Sprintf("DECISION #%d ON %s IS A %s, NOT A REFUND: the closure would refuse the apply", id, shortPath(realm), d.Kind), true
		}
		var ids []string
		if m := refundTextRE.FindStringSubmatch(d.Text); m != nil {
			ids = strings.Split(m[2], ",")
		}
		text, alarm := refundCheck(mb, realm, symbol, ids, processed, now, blockTime, gate, gateOK)
		when := "mature"
		if left := time.Unix(d.Due, 0).Sub(now); left > 0 {
			when = fmt.Sprintf("not mature until %s (in %s; the closure executes it only then, and a passed vote retries)", time.Unix(d.Due, 0).UTC().Format(time.RFC3339), left.Round(time.Minute))
		}
		return fmt.Sprintf("applies %s's queued refund #%d, %s: %s", shortPath(realm), id, when, text), alarm
	}
	return fmt.Sprintf("NO QUEUED REFUND #%d ON %s (applied, cancelled or never made): the closure would refuse the apply", id, shortPath(realm)), true
}

// migrateCheck is a route's migrate proposal read against the mailbox (AQ5): the successor it names must have
// announced itself for this route — the closure refuses one unannounced or announced for the other route, and
// the member reads it here before the vote — and a successor registered already is a migrate that applied.
func migrateCheck(mb agent.MailboxView, realm, symbol, to string) string {
	for _, s := range mb.Successions {
		switch {
		case s == realm+" → "+to:
			return "successor " + to + " is registered already (" + s + "): the migrate applied"
		case strings.HasSuffix(s, " → "+to):
			return "SUCCESSOR " + to + " IS ANOTHER ROUTE'S SUCCESSOR ALREADY (" + s + "): the closure would refuse it"
		}
	}
	switch announced := mb.SuccessorRoutes[to]; announced {
	case "":
		return "SUCCESSOR " + to + " NOT ANNOUNCED: it never called hlmailbox.AnnounceSuccessor from its own code; the closure would refuse it (AQ5)"
	case symbol:
		return "successor " + to + " announced itself as the " + symbol + " route's successor (hlmailbox.SuccessorFor): the closure takes it once the vote passes"
	}
	return "SUCCESSOR " + to + " ANNOUNCED FOR THE " + mb.SuccessorRoutes[to] + " ROUTE, NOT THIS " + symbol + " ONE: the closure would refuse it (AQ5)"
}

// programChecks reads, for every r/hlgov kind 8 or 9 proposal in the list, what the realm cannot (K16, §13):
// a kind 9's successor programdata, whose upgrade authority must be none (the admin program spends a kind 9 to
// a mutable successor), and a kind 8's program and buffer, whose authorities say whether the buffer is staged
// and the program upgradeable; the finding is appended to the proposal's text. Then the recipient switch against
// the admin program's kind 9 (AP3).
func (w *watcher) programChecks(ctx context.Context, v *view) {
	var blockTime func(int64) (time.Time, error)
	if w.gno != nil {
		blockTime = func(h int64) (time.Time, error) {
			head, err := w.gno.Block(ctx, h)
			return head.Time, err
		}
	}
	var processed map[string]bool
	if v.Sol != nil {
		processed = v.Sol.Processed
	}
	// a refund's gate on Solana for a realm (F27, AS2): the token's enrolled router and the kind 6's state
	gateFor := func(realm, symbol string) (string, bool) {
		if v.Sol == nil {
			return agent.RefundGate(symbol, realm, 0, nil, agent.Kind6State{})
		}
		var rv *agent.RouterView
		if r, ok := v.Sol.Routers[symbol]; ok {
			rv = &r
		}
		return agent.RefundGate(symbol, realm, v.Sol.Domain, rv, w.kind6State(ctx, v, realm, symbol))
	}
	for i := range v.Proposals {
		p := &v.Proposals[i]
		// a route's migrate: the successor's announcement, read from the mailbox (AQ5); a successor's refund: the
		// enrolment on Solana (F27), the kind 6's note and whether Solana delivered an id already (AR1) — on any
		// realm of the token's line, the proposal's own (G48: the refund and a second migrate are the successor's,
		// which the config does not name)
		if r := v.routeOf(w.cfg, p.Realm); r != nil {
			if m := migrateTextRE.FindStringSubmatch(p.Text); m != nil {
				p.Text += " — " + migrateCheck(v.Mailbox, p.Realm, r.Symbol, m[1])
			}
			// a route's per-message maximum against the ISM's live one (AS4)
			if m := maximumTextRE.FindStringSubmatch(p.Text); m != nil && v.Sol != nil {
				if tok, ok := agent.TokenIndex(r.Symbol); ok && v.Sol.Limits[tok].Symbol != "" {
					text, alarm := maximumCheck(m[1], v.Sol.Limits[tok].MaxPerMessage)
					p.Text += " — " + text
					if alarm {
						v.Findings = append(v.Findings, switchNote{"maximum:proposal:" + strconv.FormatInt(p.ID, 10), fmt.Sprintf("GovDAO #%d, %s's per-message maximum: %s", p.ID, r.Symbol, text)})
					}
				}
			}
			// GovDAO's apply of a queued refund (AT2): the realm's Execute refuses the kind, so the apply is a
			// proposal, at once, and the member reads the same gate here before proposing or voting it — told
			// apart by its title first ("<realm>: apply refund", the builder's), then by its description's head,
			// since the description carries the refund decision's own text
			if isApplyRefund(p) {
				id := int64(0)
				if m := applyRefundTextRE.FindStringSubmatch(p.Text); m != nil {
					id, _ = strconv.ParseInt(m[1], 10, 64)
				}
				gate, ok := gateFor(p.Realm, r.Symbol)
				text, alarm := applyRefundCheck(v.Pending, v.Mailbox, p.Realm, r.Symbol, id, processed, v.Head.Time, blockTime, gate, ok)
				p.Text += " — " + text
				if alarm {
					v.Findings = append(v.Findings, switchNote{"refund:apply:" + strconv.FormatInt(p.ID, 10), fmt.Sprintf("GovDAO #%d, the apply of %s's refund decision #%d (%s): %s — do not propose or vote the apply while this stands", p.ID, shortPath(p.Realm), id, r.Symbol, text)})
				}
			} else if m := refundTextRE.FindStringSubmatch(p.Text); m != nil {
				gate, ok := gateFor(p.Realm, r.Symbol)
				text, alarm := refundCheck(v.Mailbox, p.Realm, r.Symbol, strings.Split(m[2], ","), processed, v.Head.Time, blockTime, gate, ok)
				p.Text += " — " + text
				if alarm {
					v.Findings = append(v.Findings, switchNote{"refund:proposal:" + strconv.FormatInt(p.ID, 10), fmt.Sprintf("GovDAO #%d, a refund on %s (%s): %s", p.ID, p.Realm, r.Symbol, text)})
				}
			}
			// a restore naming a send id (M1i, R38): a written-off send's restore undoes the write-off; the realm
			// refuses a send that is not written off
			if m := restoreTextRE.FindStringSubmatch(p.Text); m != nil && w.gno != nil {
				p.Text += w.restoreSends(ctx, p.Realm, strings.Split(m[1], ","), v.Head.Height)
			}
		}
		if p.Realm != w.cfg.Gno.Gov {
			continue
		}
		if m := enrolTextRE.FindStringSubmatch(p.Text); m != nil {
			// a kind 6 against the mailbox (M1i): the route it names must be the token's own, live, registered and
			// — a successor — pulled
			token, _ := strconv.Atoi(m[1])
			text, alarm := enrolCheck(v.Mailbox, w.cfg, token, m[3])
			p.Text += " — " + text
			if alarm {
				v.Findings = append(v.Findings, switchNote{"enrol:proposal:" + strconv.FormatInt(p.ID, 10), fmt.Sprintf("GovDAO #%d, a kind 6: %s", p.ID, text)})
			}
		}
		if m := transferTextRE.FindStringSubmatch(p.Text); m != nil {
			clientFactor, adminFactor := v.factors()
			text, finding, note := w.successorCheck(ctx, m[1], clientFactor, adminFactor)
			p.Text += " — " + text
			if finding {
				v.Findings = append(v.Findings, switchNote{"successor:proposal:" + strconv.FormatInt(p.ID, 10), fmt.Sprintf("GovDAO #%d, a kind 9 hand-over: %s", p.ID, text)})
			}
			if note != "" {
				// a field appended past the version-1 form (G52, AS8): a note beside the successor line, alarmed
				// under its own key, never the LAYOUT DIFFERS finding — the hand-over reads none of it
				p.Text += "; NOTE: " + note
				v.Findings = append(v.Findings, switchNote{"successor:note:" + strconv.FormatInt(p.ID, 10), fmt.Sprintf("GovDAO #%d, a kind 9 hand-over: %s", p.ID, note)})
			}
		} else if m := upgradeTextRE.FindStringSubmatch(p.Text); m != nil {
			p.Text += " — " + w.upgradeCheck(ctx, m[1], m[2])
		}
	}
	// a queued refund decision, every poll until it applies (F27, AS2): the same gate — the realm applies it at
	// the first tick past its note's days, and only the router on Solana says whether that is safe; the relayer
	// waits on the same reading, and a member cancels a queued refund hlwatch alarms
	for i := range v.Pending {
		d := &v.Pending[i]
		if d.Kind != "refund" {
			continue
		}
		r := v.routeOf(w.cfg, d.Realm)
		if r == nil {
			continue
		}
		var ids []string
		if m := refundTextRE.FindStringSubmatch(d.Text); m != nil {
			ids = strings.Split(m[2], ",")
		}
		gate, ok := gateFor(d.Realm, r.Symbol)
		text, alarm := refundCheck(v.Mailbox, d.Realm, r.Symbol, ids, processed, v.Head.Time, blockTime, gate, ok)
		d.Text += " — " + text
		if alarm {
			// who applies (M1k): GovDAO's apply-refund proposal, or the governor's own Govern before the yield
			who, _ := v.applier()
			verb := "propose or vote"
			if !v.Mailbox.DAOGoverns() {
				verb = "make (by Govern)"
			}
			v.Findings = append(v.Findings, switchNote{"refund:queued:" + d.Realm + "#" + strconv.FormatInt(d.ID, 10), fmt.Sprintf("%s decision #%d, a queued refund (%s), matures at %s, awaiting %s apply: %s — do not %s the apply while this stands (cancel the decision if the kind 6 was cancelled)", shortPath(d.Realm), d.ID, r.Symbol, time.Unix(d.Due, 0).UTC().Format(time.RFC3339), who, text, verb)})
		}
	}
	if w.cfg.HasPrograms() {
		v.Switches = switchNotes(v, w.cfg.Gno.Gov)
	}
}

// restoreSends reads the send ids among a restore proposal's ids on its realm (M1i, R38): a written-off send's
// restore undoes the write-off (lost falls by its amount, queued 7 days); a send not written off the closure
// refuses; an id that is no send of the realm (a claim's) says nothing here.
func (w *watcher) restoreSends(ctx context.Context, realm string, ids []string, H int64) string {
	var parts []string
	for _, id := range ids {
		id = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "0x"))
		if id == "" {
			continue
		}
		sd, err := agent.SendOf(ctx, w.gno, realm, id, H)
		switch {
		case err != nil || sd == nil:
			continue
		case sd.WrittenOff:
			parts = append(parts, fmt.Sprintf("%s: a written-off send (%d from %s): its restore undoes the write-off — lost falls by %d, queued 7 days (R38)", id, sd.Amount, sd.From, sd.Amount))
		default:
			parts = append(parts, id+": a send that is not written off: the closure refuses it")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " — " + strings.Join(parts, "; ")
}

// kind6State is where r/hlgov's latest kind 6 enrolling a realm as the token's router stands on the admin
// program (F27): walked back over r/hlgov's numbers at the view's height, then judged against the admin
// program's last number, its pending accounts and its cancelled ranges.
func (w *watcher) kind6State(ctx context.Context, v *view, realm, symbol string) agent.Kind6State {
	if w.cfg.Gno.Gov == "" || v.Sol == nil || w.gno == nil {
		return agent.Kind6State{State: "not read (r/hlgov or the Solana side is not in this view)"}
	}
	tok, ok := agent.TokenIndex(symbol)
	if !ok {
		return agent.Kind6State{State: "not read (" + symbol + " is no token of the pausable ISM's)"}
	}
	n, err := agent.FindEnrolment(ctx, w.gno, w.cfg.Gno.Gov, v.Gov.Last, v.Head.Height, byte(tok), v.Sol.Domain, agent.RealmKey(realm))
	if err != nil {
		return agent.Kind6State{State: "not read (r/hlgov's decisions: " + err.Error() + ")"}
	}
	return agent.Kind6StateOf(n, v.Sol.LastSeq, v.Sol.Pending, v.Sol.Cancelled)
}

// routeOf is the configured route a realm belongs to: the route itself, or a realm of its line of migrations at
// the view's height (G48); nil for a realm of no route.
func (v *view) routeOf(cfg *agent.Config, realm string) *agent.Route {
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if realm == r.Gno {
			return r
		}
		for _, p := range v.Lines[r.Symbol] {
			if p == realm {
				return r
			}
		}
	}
	return nil
}

// switchNote is one finding on r/hlgov's recipient switch (AP3), keyed for the watch's alarms.
type switchNote struct {
	key, text string
}

// switchNotes are the recipient-switch findings (AP3, §6.5): a `set decision recipient` proposed or queued
// while the admin program's kind 9 is unexecuted (no successor recorded, or another one) — executed first it
// deadlocks GovDAO's own channel under a hold: the successor answers NotInherited and the predecessor hears
// nothing until the hold ends or the switch is undone, so the switch is voted only once this page shows the kind
// 9 executed; a kind 9 executed while r/hlgov still dispatches to the retired program — every delivery answers
// Retired, the message dead there until the switch lands and GovDAO re-dispatches; and a recipient that is a
// successor which has not inherited — every delivery answers NotInherited until the predecessor's kind 9 lands.
// The admin program is the live one of the line read from the chain (AQ6): a switch to it is the executed kind
// 9's own; one to another program is judged against the line.
func switchNotes(v *view, gov string) []switchNote {
	if v.Sol == nil {
		return nil
	}
	var out []switchNote
	retired := map[string]bool{}
	for _, r := range v.Sol.Retired {
		retired[r] = true
	}
	unexecuted := func(target string) string {
		switch {
		case target == v.Sol.LiveHex, target != "" && target == v.Sol.Successor:
			return "" // the kind 9 that made it the live program executed (or named it, where the line stops)
		case v.Sol.Successor != "":
			return "the admin program's successor is " + v.Sol.Successor + ", not this one"
		case len(v.Sol.Retired) > 0:
			return "the admin program's line ends at " + v.Sol.LiveHex + ", not this one"
		}
		return "the admin program's kind 9 is UNEXECUTED (no successor recorded)"
	}
	warn := func(key, what, target string) {
		if why := unexecuted(target); why != "" {
			out = append(out, switchNote{key, fmt.Sprintf("%s to %s while %s: executed first, it deadlocks GovDAO's own channel — the successor answers NotInherited and the predecessor hears nothing until the hold ends or the switch is undone (§6.5, AP3); vote the switch only once this page shows the kind 9 executed", what, target, why)})
		}
	}
	for _, p := range v.Proposals {
		if p.Realm != gov {
			continue
		}
		if m := recipientTextRE.FindStringSubmatch(p.Text); m != nil {
			warn("switch:proposal:"+strconv.FormatInt(p.ID, 10), fmt.Sprintf("the recipient switch proposed (GovDAO #%d)", p.ID), m[1])
		}
	}
	for _, d := range v.Pending {
		if d.Realm != gov {
			continue
		}
		if m := recipientTextRE.FindStringSubmatch(d.Text); m != nil {
			warn("switch:queued:"+strconv.FormatInt(d.ID, 10), fmt.Sprintf("the recipient switch queued (r/hlgov decision #%d, matures at %s)", d.ID, time.Unix(d.Due, 0).UTC().Format(time.RFC3339)), m[1])
		}
	}
	switch {
	case retired[v.Gov.Recipient]:
		out = append(out, switchNote{"switch:retired", "RETIRED: the admin program " + v.Gov.Recipient + " handed every authority on (kind 9 executed; the live program is " + v.Sol.LiveHex + ") and r/hlgov still dispatches to it: every delivery answers Retired, the message dead there — set decision recipient to the live program, then re-dispatch each decision (§6.5, AP3)"})
	case v.Sol.Successor != "" && v.Gov.Recipient == v.Sol.LiveHex:
		out = append(out, switchNote{"switch:retired", "RETIRED: the admin program handed every authority to " + v.Sol.Successor + " (kind 9 executed) and r/hlgov still dispatches to it: every delivery answers Retired, the message dead there — set decision recipient to the successor, then re-dispatch each decision (§6.5, AP3)"})
	}
	if v.Gov.Recipient != v.Sol.LiveHex && !retired[v.Gov.Recipient] && v.Sol.RecipientInherited != nil && !*v.Sol.RecipientInherited {
		out = append(out, switchNote{"switch:not-inherited", "NOT INHERITED: r/hlgov dispatches to " + v.Gov.Recipient + ", a successor that has not inherited: every delivery answers NotInherited until its predecessor's kind 9 executes (AP3)"})
	}
	return out
}

// authorityOf is a programdata or buffer account's upgrade authority as the loader keeps it, and the bytes it
// holds past its header (the growth a kind 8 needs is measured on them, AP5), read through both providers (G26:
// the K16 verdicts a member reads before voting rest on it; a disagreement is agent.ErrProviders and the verdict
// says UNCONFIRMED).
func (w *watcher) authorityOf(ctx context.Context, account solana.PublicKey, buffer bool) (*solana.PublicKey, int, error) {
	info, err := agent.TwoProviders(w.providers).GetAccountInfoWithOpts(ctx, account, &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentFinalized})
	if err != nil {
		return nil, 0, err
	}
	if info.Value == nil {
		return nil, 0, fmt.Errorf("no account %s", account)
	}
	data := info.Value.Data.GetBinary()
	if buffer {
		auth, _, err := sol.ParseBuffer(data)
		return auth, len(data) - sol.BufferHeader, err
	}
	_, auth, _, err := sol.ParseProgramData(data)
	return auth, len(data) - sol.ProgramDataHeader, err
}

// unconfirmed is the text a program read the two providers gave differently gets (G26).
const unconfirmed = "UNCONFIRMED (the providers disagree)"

func keyFromHex(h string) (solana.PublicKey, error) {
	k, err := hl.ParseKey32(h)
	if err != nil {
		return solana.PublicKey{}, err
	}
	return solana.PublicKeyFromBytes(k[:]), nil
}

// successorCheck is a kind 9's verdict on its successor: the admin program itself is refused (S-15: Execute
// spends a self-transfer); its state is judged as the program's `can_inherit` judges it before any authority
// moves (G52) — the account its own, its bytes parsing as the version-1 prefix (a tolerant borsh load: the bytes
// past `inherited` are read by nothing of the hand-over), naming this admin program as predecessor, not
// inherited, with no successor of its own — each failure a finding, since the program spends the hand-over on
// it; a non-zero reserve past the prefix is the note (AS8: a field a later build appended, as M2h's contract
// allows — before, it was LAYOUT DIFFERS and the vote waited on a build that zeroed a field the contract let it
// set); its compiled clock factor must be the deployment's (S-23, M2i: can_inherit spends a kind 9 to a
// successor of another factor; clientFactor and adminFactor are the client's head byte and the live admin
// program's, 0 when unread), its authority PDA's balance is printed beside the verdict (S-25: the hand-over
// sweeps the predecessor's float to it; hlsol successor-init funds it); and its programdata must have no upgrade
// authority — immutable — or the decision would be spent.
func (w *watcher) successorCheck(ctx context.Context, hexKey string, clientFactor, adminFactor int64) (text string, finding bool, note string) {
	succ, err := keyFromHex(hexKey)
	if err != nil {
		return "successor: " + err.Error(), false, ""
	}
	admin := w.cfg.Programs().AdminProgram()
	if succ.Equals(admin.Program) {
		return "SUCCESSOR " + succ.String() + " IS THE ADMIN PROGRAM ITSELF: Execute spends a self-transfer (S-15); the proposal is void", false, ""
	}
	both := agent.TwoProviders(w.providers)
	info, err := both.GetAccountInfoWithOpts(ctx, sol.Admin{Program: succ}.State(), &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentFinalized})
	switch {
	case errors.Is(err, agent.ErrProviders):
		return "successor " + succ.String() + ": its state " + unconfirmed + " — kind 9 is not judged on one provider's word", false, ""
	case errors.Is(err, rpc.ErrNotFound) || err == nil && info.Value == nil:
		return "SUCCESSOR " + succ.String() + " HAS NO STATE: kind 9 would die on its SuccessorInit (NotInherited) — hlsol successor-init -predecessor " + admin.Program.String() + " first", false, ""
	case err != nil:
		return "successor " + succ.String() + ": its state could not be read (" + err.Error() + ")", false, ""
	}
	data := info.Value.Data.GetBinary()
	if !info.Value.Owner.Equals(succ) {
		return "SUCCESSOR " + succ.String() + " ITS STATE IS NOT ITS OWN (owned by " + info.Value.Owner.String() + "): can_inherit refuses it — kind 9 would be spent (G52)", true, ""
	}
	st, err := sol.ParseAdminState(data)
	switch {
	case err != nil:
		// the hand-over's wire contract (S-21): the version-1 prefix must parse — a field inserted before
		// `inherited` moves what the peek reads, and the program spends the hand-over on it
		return "SUCCESSOR " + succ.String() + " LAYOUT DIFFERS: its state does not parse with the version-1 layout (" + err.Error() + "): the hand-over would be spent or die (S-21): a successor keeps State's borsh layout append-only and SuccessorInit (variant 14) byte-identical", true, ""
	case !st.Predecessor.Equals(admin.Program):
		return "SUCCESSOR " + succ.String() + " NAMES PREDECESSOR " + st.Predecessor.String() + ", NOT THIS ADMIN PROGRAM: can_inherit refuses it — kind 9 would be spent (a field inserted before `predecessor` reads so too, S-21)", true, ""
	case st.Inherited == 1:
		return "SUCCESSOR " + succ.String() + " HAS INHERITED ALREADY: a second hand-over cannot be asked of it — kind 9 would be spent", true, ""
	case st.Successor != nil:
		return "SUCCESSOR " + succ.String() + " HAS A SUCCESSOR OF ITS OWN (" + hex.EncodeToString(st.Successor[:]) + "): can_inherit refuses it — kind 9 would be spent (G52)", true, ""
	}
	// bytes past the version-1 form that are not zero: a field this build does not read (AS8, G52) — a note,
	// never a finding; the hand-over reads none of it
	note = sol.AdminStateReserve(st, data)
	// the successor's compiled clock factor against the deployment's (S-23): the program spends such a kind 9
	if f := int64(st.ClockFactor); clientFactor > 0 && f != clientFactor || adminFactor > 0 && f != adminFactor {
		return fmt.Sprintf("SUCCESSOR %s AT CLOCK FACTOR %d, THE DEPLOYMENT'S IS %d (the client's head) AND %d (the admin program's): a build of another factor — can_inherit refuses it, kind 9 would be spent (S-23); deploy the successor from the mainnet-factor build", succ, f, clientFactor, adminFactor), true, note
	}
	// the successor's authority PDA: its float, printed beside the verdict (S-25)
	pdaText := ""
	switch bal, err := both.GetAccountInfoWithOpts(ctx, sol.Admin{Program: succ}.Authority(), &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentFinalized}); {
	case errors.Is(err, rpc.ErrNotFound) || err == nil && bal.Value == nil:
		pdaText = "; its authority PDA holds 0 lamports (hlsol successor-init -authority-fund funds it; the hand-over sweeps the predecessor's float to it, S-25)"
	case err != nil:
		pdaText = "; its authority PDA's balance could not be read (" + err.Error() + ")"
	default:
		pdaText = fmt.Sprintf("; its authority PDA holds %d lamports (the realloc float; the hand-over sweeps the predecessor's to it, S-25)", bal.Value.Lamports)
	}
	// nothing pends on a successor before the hand-over (S-20): a decision recorded there would run with its
	// authority after it, at a number the inherited counter reuses; the program refuses Handle and Execute on it
	// now (NotInherited), so a non-zero count is from before that build, or a stranger's program
	if st.Pending > 0 || st.LastSeq > 0 {
		list := ""
		if ds, err := agent.ReadPendingDecisions(ctx, both, sol.Admin{Program: succ}, st); err != nil {
			list = " (the list could not be read: " + err.Error() + ")"
		} else {
			var parts []string
			for _, d := range ds {
				parts = append(parts, fmt.Sprintf("#%d kind %d", d.Number, d.Kind))
			}
			list = " (" + orNone(strings.Join(parts, ", ")) + ")"
		}
		return fmt.Sprintf("SUCCESSOR %s HAS A DECISION PENDING BEFORE THE HAND-OVER: %d pending%s, last number %d — a decision pending there executes with the successor's authority after kind 9, at a number the inherited counter reuses (S-20); cancel or close them first", succ, st.Pending, list, st.LastSeq), true, note
	}
	auth, _, err := w.authorityOf(ctx, sol.ProgramData(succ), false)
	if errors.Is(err, agent.ErrProviders) {
		return "successor " + succ.String() + ": " + unconfirmed + " — kind 9 is not judged on one provider's word", false, note
	}
	if err != nil {
		return "successor " + succ.String() + ": its programdata could not be read (" + err.Error() + ")", false, note
	}
	if auth == nil {
		return fmt.Sprintf("successor %s immutable (its upgrade authority is none; its state is its own and names this admin program as predecessor, not yet inherited, no successor of its own, the version-1 layout, clock factor %d as the deployment's, nothing pending: kind 9 can execute)%s", succ, st.ClockFactor, pdaText), false, note
	}
	return "SUCCESSOR " + succ.String() + " MUTABLE (upgrade authority " + auth.String() + "): kind 9 would be spent — burn it first (hlsol successor-init -burn)" + pdaText, false, note
}

// factors are the deployment's compiled clock factors as the view read them — the client's head byte and the
// live admin program's — 0 when the Solana side was not read.
func (v *view) factors() (client, admin int64) {
	if v.Sol == nil {
		return 0, 0
	}
	return v.Sol.ClockFactor, v.Sol.AdminClockFactor
}

// upgradeCheck is a kind 8's reading of its program and buffer: the program's upgrade authority (the admin PDA,
// or the decision cannot execute) and the buffer's (the admin PDA once staged, else its writer's), then the
// sizes and the growth's rent (AP5: a padded buffer sinks unrecoverable program-data rent from whoever executes;
// the vote reads the number here).
func (w *watcher) upgradeCheck(ctx context.Context, programHex, bufferHex string) string {
	program, err := keyFromHex(programHex)
	if err != nil {
		return "program: " + err.Error()
	}
	buffer, err := keyFromHex(bufferHex)
	if err != nil {
		return "buffer: " + err.Error()
	}
	pda := w.cfg.Programs().AdminProgram().Authority()
	parts := []string{}
	pdLen, bufLen := -1, -1
	if auth, n, err := w.authorityOf(ctx, sol.ProgramData(program), false); errors.Is(err, agent.ErrProviders) {
		parts = append(parts, "program "+program.String()+": "+unconfirmed)
	} else if err != nil {
		parts = append(parts, "program "+program.String()+": its programdata could not be read ("+err.Error()+")")
	} else if pdLen = n; auth == nil {
		parts = append(parts, "program "+program.String()+" is IMMUTABLE: an upgrade would be spent")
	} else if auth.Equals(pda) {
		parts = append(parts, "program "+program.String()+" upgradeable by the admin program")
	} else {
		parts = append(parts, "program "+program.String()+" has upgrade authority "+auth.String()+", NOT the admin program: an upgrade would be spent")
	}
	if auth, n, err := w.authorityOf(ctx, buffer, true); errors.Is(err, agent.ErrProviders) {
		parts = append(parts, "buffer "+buffer.String()+": "+unconfirmed)
	} else if err != nil {
		parts = append(parts, "buffer "+buffer.String()+": not readable ("+err.Error()+")")
	} else if bufLen = n; auth != nil && auth.Equals(pda) {
		parts = append(parts, "buffer staged (its authority is the admin PDA)")
	} else if auth != nil {
		parts = append(parts, "buffer not yet staged (its authority is "+auth.String()+": its writer's stage upgrade is awaited once the decision is recorded)")
	} else {
		parts = append(parts, "buffer has no authority: it cannot be staged")
	}
	if pdLen >= 0 && bufLen >= 0 {
		parts = append(parts, w.growthText(ctx, program, pdLen, bufLen))
	}
	if program.Equals(w.cfg.Programs().Client) {
		// an upgrade of the client (S-22, M2i): the state's layout contract is the build's to keep — the head's
		// version byte stays 1 for the deployment's life (§13, kind 4); a new layout is a successor pair by kind 9
		// first — and a fix build's hash is compared here with what the proposal names
		parts = append(parts, "THE CLIENT: its state layout is this build's to keep (§13, kind 4: the head's version byte 1 for the deployment's life; a new layout is a successor pair by kind 9 first, and a fix build writes version 1 back, S-22); the buffer's ELF sha256 is "+w.bufferHash(ctx, buffer)+" — compare it with the reproducible fix build's hash the proposal names")
	}
	return strings.Join(parts, "; ")
}

// bufferHash is a buffer's ELF sha256 (the bytes after the loader's header, to the ELF's end) through both
// providers, or why not.
func (w *watcher) bufferHash(ctx context.Context, buffer solana.PublicKey) string {
	info, err := agent.TwoProviders(w.providers).GetAccountInfoWithOpts(ctx, buffer, &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentFinalized})
	if err != nil || info.Value == nil {
		return "not readable"
	}
	_, elf, err := sol.ParseBuffer(info.Value.Data.GetBinary())
	if err != nil {
		return "not readable (" + err.Error() + ")"
	}
	sum := sha256.Sum256(elf[:sol.ELFEnd(elf)])
	return hex.EncodeToString(sum[:])
}

// growthText says what a kind 8's growth costs (AP5): the program data's and the buffer's bytes, and for a buffer
// past the program data one ExtendProgram's size with its rent at the chain's rate (read from provider 1: a number
// for the vote, no verdict rests on it).
func (w *watcher) growthText(ctx context.Context, program solana.PublicKey, pdLen, bufLen int) string {
	add := sol.ExtendBy(pdLen, bufLen)
	if add == 0 {
		return fmt.Sprintf("the program data holds %d bytes, the buffer %d: no growth needed", pdLen, bufLen)
	}
	rent, err := sol.ExtendRent(ctx, w.providers[0], program, add)
	if err != nil {
		return fmt.Sprintf("the program data holds %d bytes, the buffer %d: GROWTH of one ExtendProgram of %d bytes, its rent at the chain's rate not readable (%s)", pdLen, bufLen, add, err.Error())
	}
	return fmt.Sprintf("the program data holds %d bytes, the buffer %d: GROWTH of one ExtendProgram of %d bytes ≈ %s of rent at the chain's rate, from whoever executes, never returned (the relayer's float, or hlrecover execute's payer above its -max-rent only with -yes)", pdLen, bufLen, add, sol.SOL(rent))
}

// applier names who applies a queued refund (AT2's apply-refund decision; M1k): GovDAO's proposal once the
// governor yielded (or from the first block, when none was compiled), the governor's own decision by Govern
// before the yield.
func (v *view) applier() (who, way string) {
	if v.Mailbox.DAOGoverns() {
		return "GovDAO's", "proposal"
	}
	return "the governor's", "decision by Govern"
}

// pastDeadline says whether a proposal has been open longer than the builders' deadline (14 days divided by the
// realms' clock factor, from hlmailbox's clocks) by this watch's first sight of it (its creation time is not
// rendered; a proposal seen late is older still).
func (v *view) pastDeadline(p agent.Proposal) bool {
	first, ok := v.Seen[strconv.FormatInt(p.ID, 10)]
	return ok && v.At.Sub(time.Unix(first, 0)) >= v.Mailbox.Clocks.DeadlineDuration()
}

func (v *view) text() string {
	var b strings.Builder
	chainTime := v.Head.Time
	fmt.Fprintf(&b, "%s\nhlwatch at %s — node height %d, block time %s\n\n", buildHash(), v.At.UTC().Format(time.RFC3339), v.Head.Height, chainTime.UTC().Format(time.RFC3339))
	m := v.Mailbox
	fmt.Fprintf(&b, "## hlmailbox\n")
	if m.Live {
		fmt.Fprintf(&b, "- live since %s\n", time.Unix(m.LaunchedAt, 0).UTC().Format(time.RFC3339))
	} else {
		fmt.Fprintf(&b, "- NOT LAUNCHED\n")
	}
	// who governs (M1k): the compiled governor until the yield's height, GovDAO after — the realms' own line
	fmt.Fprintf(&b, "- %s\n", m.GovernorLine())
	fmt.Fprintf(&b, "- verifier for origin %d: %s (registered: %v)\n", m.LaunchOrigin, m.ActiveVerifier, m.VerifierRegistered)
	fmt.Fprintf(&b, "- clocks: %s (§8.3 wants factor 1 on mainnet; the meters are never divided)\n", m.Clocks)
	fmt.Fprintf(&b, "- signers: %s\n", m.Rule)
	for i, k := range m.SignerKeys {
		fmt.Fprintf(&b, "  - %d: %s\n", i, k)
	}
	fmt.Fprintf(&b, "- seats:\n")
	for i, s := range m.Seats {
		kind := "guardian"
		if i >= 4 {
			kind = "watch key"
		}
		if s == "" {
			s = "(empty)"
		}
		fmt.Fprintf(&b, "  - %d (%s): %s\n", i+1, kind, s)
	}
	fmt.Fprintf(&b, "- flags:\n")
	n := 0
	for _, f := range m.Flags {
		n++
		line := fmt.Sprintf("  - %s: %s by seat %d (%s) since %s", shortPath(f.Route), f.Fn, f.Seat, f.Addr, time.Unix(f.At, 0).UTC().Format(time.RFC3339))
		if len(f.IDs) > 0 {
			line += " naming " + strings.Join(f.IDs, ",")
		}
		fmt.Fprintln(&b, line)
	}
	if n == 0 {
		fmt.Fprintln(&b, "  - none")
	}
	if len(m.Successions) > 0 {
		fmt.Fprintf(&b, "- migrated routes (the successor takes the predecessor's messages once its last page is in): %s\n", strings.Join(m.Successions, "; "))
	}
	if len(m.SuccessorRoutes) > 0 {
		var parts []string
		for p, sym := range m.SuccessorRoutes {
			parts = append(parts, p+" ("+sym+")")
		}
		sort.Strings(parts)
		fmt.Fprintf(&b, "- successors announced, each for a route (a migrate names only one announced for its own): %s\n", strings.Join(parts, ", "))
	}
	who, way := v.applier()
	fmt.Fprintf(&b, "\n## pending decisions on gno.land (apply with Execute(id) after the delay; the relayers do — a refund by %s apply-refund %s alone, AT2)\n", who, way)
	if len(v.Pending) == 0 {
		fmt.Fprintln(&b, "- none")
	}
	for _, d := range v.Pending {
		left := time.Unix(d.Due, 0).Sub(chainTime)
		when := fmt.Sprintf("matures in %s (at %s)", left.Round(time.Minute), time.Unix(d.Due, 0).UTC().Format(time.RFC3339))
		if left <= 0 {
			when = "MATURE, awaiting Execute"
		}
		if d.Kind == "refund" {
			// AT2: the realm's Execute refuses a refund; the apply-refund decision applies it, at once, after the
			// member read the gate above — GovDAO's proposal, or the governor's own Govern before the yield (M1k)
			when += "; awaiting " + who + " apply (an apply-refund " + way + "; Execute refuses a refund and the relayer never calls it)"
		}
		fmt.Fprintf(&b, "- %s #%d %s: %s — %s\n", shortPath(d.Realm), d.ID, d.Kind, d.Text, when)
	}
	fmt.Fprintf(&b, "\n## builder-made GovDAO proposals with no execution\n")
	if v.DAOErr != "" {
		fmt.Fprintf(&b, "- (%s)\n", v.DAOErr)
	} else if len(v.Proposals) == 0 {
		fmt.Fprintln(&b, "- none")
	}
	for _, p := range v.Proposals {
		armed := ""
		if p.Armed {
			armed = " — PRE-ARMED: has the votes, anyone may execute it"
			if !v.Mailbox.DAOGoverns() {
				// M1k: until the governor yields a builder's closure refuses GovDAO's proxy — a panic, which
				// denies nothing — so the proposal waits, pending, for the yield (within its 14 days)
				armed = " — PRE-ARMED: has the votes; awaits the yield (until the governor yields, the builder's closure refuses gno.land/r/gov/dao: executing it now panics and leaves it pending, within its 14 days)"
			}
		}
		if v.pastDeadline(p) {
			armed += " — PAST DEADLINE: open for over " + v.Mailbox.Clocks.DeadlineDuration().String() + " (first seen " + time.Unix(v.Seen[strconv.FormatInt(p.ID, 10)], 0).UTC().Format(time.RFC3339) + "); its closure errors now, and only ExecuteOrRejectProposal denies it for good (hlwatch finish; the relayers do it for a pre-armed one)"
		}
		fmt.Fprintf(&b, "- #%d %s (yes %.2f%%, %s): %s%s\n", p.ID, p.Title, p.Yes, p.Status, p.Text, armed)
	}
	fmt.Fprintf(&b, "\n## the identity (lock = sentTotal − paidTotal = supply + owed + voided − lost), from this node and two providers\n")
	for _, r := range v.Routes {
		line := fmt.Sprintf("- %s: lock %d = sent %d − paid %d; owed %d (sweep %d), voided %d, lost %d → Solana's supply should be %d; %d open claims; meter %d of cap %d; paused: %s",
			r.Symbol, r.Lock, r.SentTotal, r.PaidTotal, r.Owed, r.Sweep, r.Voided, r.Lost, r.ImpliedSupply(), r.OpenClaims, r.Meter, r.Cap, orNone(r.Paused))
		if r.Importing {
			line += fmt.Sprintf("; IMPORTING (%d pages)", r.Imported) // a successor mid-import (K28)
		} else if r.MigratedTo != "" {
			line += "; MIGRATED to " + r.MigratedTo
		}
		if r.Refunded > 0 {
			line += fmt.Sprintf("; %d sends refunded (%d, out of the lock)", r.Refunded, r.RefundedTotal) // AQ2
		}
		if r.WrittenOff > 0 {
			line += fmt.Sprintf("; %d refunded sends delivered after all and written off (%d, lost)", r.WrittenOff, r.WrittenOffTotal) // AR1
		}
		if v.Sol != nil {
			if m, ok := v.Sol.Migrations[r.Symbol]; ok {
				line += "\n  - MIGRATION: " + m.Text // the pair's summed counters below (AQ3)
			}
			if id, ok := v.Sol.Identities[r.Symbol]; ok {
				verdict, text := id.Verdict()
				line += fmt.Sprintf("\n  - Solana supply %d at slot %d: %s — %s", id.Supply, id.Slot, strings.ToUpper(verdict.String()), text)
			}
		}
		fmt.Fprintln(&b, line)
	}
	if v.Sol != nil {
		for _, l := range v.Sol.Refunded {
			fmt.Fprintf(&b, "- REFUNDED: %s\n", l) // refunded on gno.land — must never deliver (AQ2)
		}
		for _, l := range v.Sol.RefundDelivered {
			fmt.Fprintf(&b, "- REFUNDED AND DELIVERED: %s\n", l) // minted twice until the write-off (AR1)
		}
	}
	if v.Gov.Address != "" {
		fmt.Fprintf(&b, "\n## r/hlgov\n- %d decisions sent to %s; the next takes number %d\n", v.Gov.Last, v.Gov.Recipient, v.Gov.Next)
		if len(v.Gov.Former) > 0 {
			// R29: retired admin programs, refused as a kind 9's successor and as the recipient for good
			fmt.Fprintf(&b, "- former recipients (retired; never a successor, never the recipient again): %s\n", strings.Join(v.Gov.Former, ", "))
		}
		if v.GovErr != "" {
			fmt.Fprintf(&b, "- recent decisions not read: %s\n", v.GovErr)
		}
		for _, d := range v.GovRecent {
			fmt.Fprintf(&b, "- #%d kind %d: %s (dispatched %d×, last id %s)\n", d.Number, d.Kind, d.Text, d.Count, d.ID)
		}
		for _, n := range v.Switches {
			fmt.Fprintf(&b, "- RECIPIENT SWITCH: %s\n", n.text)
		}
	}
	fmt.Fprintf(&b, "\n## Solana\n")
	if v.SolErr != "" {
		fmt.Fprintf(&b, "- NOT READ: %s\n", v.SolErr)
	}
	s := v.Sol
	if s == nil {
		return agent.Redact(b.String())
	}
	for _, e := range s.Err {
		fmt.Fprintf(&b, "- not read: %s\n", e)
	}
	for _, u := range s.Unconfirmed {
		fmt.Fprintf(&b, "- UNCONFIRMED: %s\n", u)
	}
	fmt.Fprintf(&b, "- dispatch index: %d burns toward gno.land (%d seen by one provider only)\n", s.Indexed, s.Unindexed)
	if s.IndexFromHeight != 0 {
		// a checkpointed index (G49, AS3, K40): what the forged rule passes over, and what it alarms without judging
		seeded := "seeded whole from the mailbox's dispatched-message accounts: every claim credited after it is judged"
		if !s.IndexSeeded {
			seeded = "NOT SEEDED (the mailbox's accounts could not be listed; retried hourly): a claim credited after it that the index lacks is alarmed, never forged"
		}
		fmt.Fprintf(&b, "  - the index starts at a checkpoint (the cut's height %d; %s); %d waiting claims credited at or below it: before the checkpoint, not judged\n", s.IndexFromHeight, seeded, s.BeforeBase)
	}
	for _, l := range s.UnindexedBeforeBase {
		fmt.Fprintf(&b, "- UNINDEXED BEFORE THE BASE: %s\n", l)
	}
	c := s.Client
	match := "MISMATCH"
	if c.Match {
		match = "matches gno.land's"
	} else if c.ChainHash == "" {
		match = "the node has no such height (the client past the chain)"
	}
	fmt.Fprintf(&b, "- the client: trusted height %d (%s — %s), %d headers recorded, last accepted %s (%s ago), set of %d from height %d (the measured bound is about %d; an alarm from %d), next set announced: %v (%d), quarantine until %s, clock factor %d\n",
		c.TrustedHeight, c.TrustedHash, match, c.Records, c.LastAccepted.UTC().Format(time.RFC3339), time.Since(c.LastAccepted).Round(time.Second), c.SetSize, c.SetFrom, sol.SetSizeBound, sol.SetSizeWarn, c.NextSet, c.NextSetSize, quarantine(c.QuarantineEnd), s.ClockFactor)
	if c.SetBound != "" {
		fmt.Fprintf(&b, "  - SET BOUND: %s\n", c.SetBound)
	}
	rec := "consistent with the client's counter and running hash"
	if !c.RecordOK {
		rec = c.RecordWhy
	}
	fmt.Fprintf(&b, "- the header record: %d rebuilt from instruction data, %d checked against this node (%d above its head, awaited): %s\n", c.RecordCount, c.RecordChecked, c.RecordUnchecked, rec)
	if c.Mismatch != nil {
		fmt.Fprintf(&b, "  - HEADER MISMATCH: %s\n", c.Mismatch.String())
	}
	fmt.Fprintf(&b, "- the pausable ISM: %s\n", guardiansText(s.Pausable))
	fmt.Fprintf(&b, "- the admin program: setup done %v, origin %d, last decision number %d (r/hlgov's %d), pending accounts %d; %s\n", s.SetupDone, s.Origin, s.LastSeq, s.GovLast, s.PendingN, guardiansText(s.Admin))
	if len(s.Retired) > 0 {
		fmt.Fprintf(&b, "  - the live admin program is %s, a successor by kind 9 (the line followed from the config's: %s retired); every section here reads it (AQ6)\n", s.AdminProgram, strings.Join(s.Retired, " → "))
	}
	if s.LastSeq != s.GovLast {
		fmt.Fprintf(&b, "  - COUNTERS DIFFER: the admin program's %d, r/hlgov's %d (a gap means a foreign decision took a number, or GovDAO's was lost)\n", s.LastSeq, s.GovLast)
	}
	if s.Predecessor != "" {
		fmt.Fprintf(&b, "  - a successor of %s, inherited: %v\n", s.Predecessor, s.Inherited)
	}
	fmt.Fprintf(&b, "- the hold history (a replacement's floor under a hold is day %d of the earliest hold overlapping its pending life):", sol.HoldReplacementDays)
	if len(s.Holds) == 0 {
		fmt.Fprint(&b, " no hold has ended")
	}
	fmt.Fprintln(&b)
	for i, h := range s.Holds {
		fmt.Fprintf(&b, "  - %d: from %s to %s\n", i, time.Unix(h.Start, 0).UTC().Format(time.RFC3339), time.Unix(h.End, 0).UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "- the mint limits on the pausable ISM (base units; kind 12 moves them):\n")
	for _, l := range s.Limits {
		fmt.Fprintf(&b, "  - %s: %d a message, %d a day; the day's bucket holds %d now; the lock on gno.land %d; the route's maximum %d\n", l.Symbol, l.MaxPerMessage, l.DayCap, l.Used, l.Lock, l.RouteMaximum)
		if l.Mismatch != "" {
			fmt.Fprintf(&b, "  - LIMITS MISMATCH: %s\n", l.Mismatch) // gno.land's maximum above the ISM's (AS4)
		}
		if l.Ratio != "" {
			fmt.Fprintf(&b, "  - CAP RATIO: %s\n", l.Ratio)
		}
		if l.Note != "" {
			fmt.Fprintf(&b, "  - cap ratio quiet: %s\n", l.Note) // the lock below the expected first-week lock (C341)
		}
	}
	fmt.Fprintf(&b, "- pending decisions on the admin program:\n")
	if len(s.Pending) == 0 {
		fmt.Fprintln(&b, "  - none")
	}
	for _, d := range s.Pending {
		left := time.Unix(d.MaturesAt, 0).Sub(v.At)
		when := fmt.Sprintf("matures in %s (at %s)", left.Round(time.Minute), time.Unix(d.MaturesAt, 0).UTC().Format(time.RFC3339))
		if left <= 0 {
			when = "MATURE, awaiting Execute"
		}
		if d.Deferred != "" {
			when += "; " + d.Deferred
		}
		fmt.Fprintf(&b, "  - #%d kind %d (%s; params %s) delivered %s by %s — %s\n", d.Number, d.Kind, d.Text, d.Params, time.Unix(d.Delivered, 0).UTC().Format(time.RFC3339), d.Deliverer, when)
	}
	for _, f := range s.Foreign {
		fmt.Fprintf(&b, "- FOREIGN: %s\n", f)
	}
	for _, f := range s.Forged {
		fmt.Fprintf(&b, "- FORGED CREDIT: %s\n", f)
	}
	for _, f := range s.VoidsMatch {
		fmt.Fprintf(&b, "- VOID: %s\n", f)
	}
	// no provider URL's key in what a guardian pastes: SolErr, Err and Unconfirmed carry solana-go's errors, URL
	// included (G25; the watch's alarms go through the Alarmer, which redacts on its own)
	return agent.Redact(b.String())
}

func quarantine(end int64) string {
	if end == 0 || time.Now().Unix() >= end {
		return "none standing"
	}
	return time.Unix(end, 0).UTC().Format(time.RFC3339)
}

func guardiansText(g agent.GuardiansView) string {
	var parts []string
	flags := 0
	for i, k := range g.Keys {
		kind := "guardian"
		if i >= sol.Seats {
			kind = "watch"
		}
		s := fmt.Sprintf("%d (%s) %s", i+1, kind, k)
		if g.Flags[i] != nil {
			s += " FLAG " + agent.FmtFlag(g.Flags[i])
			flags++
		}
		parts = append(parts, s)
	}
	hold := "no hold"
	if g.Hold {
		hold = "HOLD since " + agent.FmtFlag(g.HoldSince)
		if g.DipSince != nil {
			hold += " (a dip since " + agent.FmtFlag(g.DipSince) + ")"
		}
	}
	return fmt.Sprintf("%d flags, %s; seats: %s", flags, hold, strings.Join(parts, "; "))
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func shortPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return "r/" + p[i+1:]
	}
	return p
}

// short is a hash's first sixteen hex digits.
func short(s string) string {
	if len(s) > 16 {
		return s[:16] + "…"
	}
	return s
}

// ---------------------------------------------------------------- watch --

// loop prints the view, then every change: a flag set or cleared on any object, a decision recorded or matured
// on either chain, a proposal pre-armed or past its deadline, a quarantine start, a header mismatch, the counters
// parting, a route's pause, a section unconfirmed; each change is an alarm to the holder.
func (w *watcher) loop(ctx context.Context, every time.Duration) {
	for {
		v, err := w.view(ctx)
		if err != nil {
			log.Print(err)
			// a reading refused — the node catching up or stale, the snapshot failing — is no verdict, and the
			// holder hears it (G33, G29): an alarm while it stands
			w.alarm.Alarm("watch:no-verdict", "no verdict: "+err.Error())
		} else {
			w.alarm.Clear("watch:no-verdict")
			if w.last == nil {
				fmt.Print(v.text())
			} else {
				w.diff(w.last, v)
			}
			w.last = v
			_ = w.st.Save(w.statePath)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (w *watcher) diff(a, b *view) {
	flagKey := func(f agent.FlagView) string { return f.Route + "|" + f.Fn + "|" + strconv.Itoa(f.Seat) }
	was := map[string]agent.FlagView{}
	for _, f := range a.Mailbox.Flags {
		was[flagKey(f)] = f
	}
	now := map[string]agent.FlagView{}
	for _, f := range b.Mailbox.Flags {
		now[flagKey(f)] = f
		if _, ok := was[flagKey(f)]; !ok {
			w.alarm.Flag("flag:"+flagKey(f), fmt.Sprintf("%s: %s flagged by seat %d (%s)", shortPath(f.Route), f.Fn, f.Seat, f.Addr))
		}
	}
	for k, f := range was {
		if _, ok := now[k]; !ok {
			w.alarm.Alarm("unflag:"+k, fmt.Sprintf("%s: %s flag of seat %d cleared", shortPath(f.Route), f.Fn, f.Seat))
			w.alarm.Clear("flag:" + k)
		}
	}
	pend := map[string]agent.PendingDecision{}
	for _, d := range a.Pending {
		pend[d.Realm+"#"+strconv.FormatInt(d.ID, 10)] = d
	}
	for _, d := range b.Pending {
		k := d.Realm + "#" + strconv.FormatInt(d.ID, 10)
		old, ok := pend[k]
		if !ok {
			w.alarm.Alarm("decision:"+k, fmt.Sprintf("%s decision #%d recorded (%s): %s — matures at %s", shortPath(d.Realm), d.ID, d.Kind, d.Text, time.Unix(d.Due, 0).UTC().Format(time.RFC3339)))
		}
		if b.Head.Time.Unix() >= d.Due && (!ok || a.Head.Time.Unix() < old.Due) {
			text := fmt.Sprintf("%s decision #%d (%s) is mature: %s", shortPath(d.Realm), d.ID, d.Kind, d.Text)
			if d.Kind == "refund" {
				who, way := b.applier()
				text += " — awaiting " + who + " apply (an apply-refund " + way + "; read the gate on the status page first, AT2)"
			}
			w.alarm.Alarm("mature:"+k, text)
		}
	}
	// the yield (M1k): one alarm the moment GovDAO governs — the governor's pre-armed proposals may execute then
	if !a.Mailbox.DAOGoverns() && b.Mailbox.DAOGoverns() {
		w.alarm.Alarm("governor:yielded", fmt.Sprintf("the governor %s yielded at height %d: %s decides from here, and a pre-armed proposal may execute", b.Mailbox.Governor, b.Mailbox.Yielded, agent.DAOPath))
	}
	armed := map[int64]bool{}
	for _, p := range a.Proposals {
		armed[p.ID] = p.Armed
	}
	for _, p := range b.Proposals {
		if p.Armed && !armed[p.ID] {
			text := fmt.Sprintf("GovDAO proposal #%d (%s) has the votes and no execution: %s", p.ID, p.Title, p.Text)
			if !b.Mailbox.DAOGoverns() {
				text += " — awaits the yield (its closure refuses gno.land/r/gov/dao until the governor yields)"
			}
			w.alarm.Alarm("prearmed:"+strconv.FormatInt(p.ID, 10), text)
		}
		if b.pastDeadline(p) && !a.pastDeadline(p) {
			w.alarm.Alarm("deadline:"+strconv.FormatInt(p.ID, 10), fmt.Sprintf("GovDAO proposal #%d (%s) is past its 14 days: ExecuteOrRejectProposal denies it for good (hlwatch finish)", p.ID, p.Title))
		}
	}
	for i := range b.Routes {
		if i < len(a.Routes) && b.Routes[i].Paused != a.Routes[i].Paused {
			w.alarm.Alarm("paused:"+b.Routes[i].Symbol, fmt.Sprintf("%s paused: %s (was: %s)", b.Routes[i].Symbol, orNone(b.Routes[i].Paused), orNone(a.Routes[i].Paused)))
		}
		// a successor mid-import (K28): an alarm while it stands, cleared when the last page is in
		r := b.Routes[i]
		key := "route:importing:" + r.Symbol
		importing := r.Importing
		if b.Sol != nil {
			if m, ok := b.Sol.Migrations[r.Symbol]; ok && m.Importing {
				importing = true
			}
		}
		if importing {
			w.alarm.Alarm(key, fmt.Sprintf("%s: a successor is IMPORTING a predecessor's pages (%d so far): credits are refused there until the last page is in; the identity is judged over the pair (K28, AQ3)", r.Symbol, r.Imported))
		} else {
			w.alarm.Clear(key)
		}
	}
	// what could not be read (G29: a stalled record scan was one "not read" line in status and silent in watch):
	// an alarm per section while it stands, cleared when it reads again
	if b.SolErr != "" {
		w.alarm.Alarm("not-read:solana", "the Solana side could not be read: "+b.SolErr)
	} else if a.SolErr != "" {
		w.alarm.Clear("not-read:solana")
	}
	notRead := map[string]bool{}
	if b.Sol != nil {
		for _, e := range b.Sol.Err {
			k := "not-read:" + sectionOf(e)
			notRead[k] = true
			w.alarm.Alarm(k, "not read: "+e)
		}
	}
	if a.Sol != nil {
		for _, e := range a.Sol.Err {
			if k := "not-read:" + sectionOf(e); !notRead[k] {
				w.alarm.Clear(k)
			}
		}
	}
	// the recipient switch against the kind 9 (AP3) and the proposal findings (AR1, S-20, S-21): an alarm while
	// a finding stands, cleared when it goes
	w.noteDiff(a.Switches, b.Switches)
	w.noteDiff(a.Findings, b.Findings)
	if a.Sol == nil || b.Sol == nil {
		return
	}
	w.identityDiff(b)
	// a token's line re-pointed (M1h, AR2: a migrate to a successor that pulled nothing is re-pointed, the first
	// successor leaving the mailbox's routes) or ended: alarmed on the change, cleared after
	for _, sym := range sortedSymbols(a.Sol.Migrations, b.Sol.Migrations) {
		key := "route:successor-changed:" + sym
		was, now := a.Sol.Migrations[sym], b.Sol.Migrations[sym]
		if was.Successor != now.Successor && was.Successor != "" {
			w.alarm.Alarm(key, fmt.Sprintf("%s's line of migrations changed: its last realm was %s, now %s (a migrate re-pointed before page 0, or a later migration; the relayers and the monitors follow the line as read each tick)", sym, was.Successor, orNone(now.Successor)))
		} else {
			w.alarm.Clear(key)
		}
	}
	// a refunded send still sitting in hlmailbox (AQ2): an alarm while it stands, cleared once it is gone; one
	// Solana processed after its refund (AR1): a standing alarm until the write-off
	w.lineDiff(a.Sol.Refunded, b.Sol.Refunded, "refunded:", "REFUNDED: ")
	w.lineDiff(a.Sol.RefundDelivered, b.Sol.RefundDelivered, "refund:delivered:", "REFUNDED AND DELIVERED: ")
	// a claim credited after the checkpoint that an unseeded index lacks (G49, AS3): alarmed while it stands,
	// never a flag
	w.lineDiff(a.Sol.UnindexedBeforeBase, b.Sol.UnindexedBeforeBase, "credit:unindexed-before-base:", "UNINDEXED BEFORE THE BASE: ")
	if b.Sol.Client.SetBound != "" {
		w.alarm.Alarm("client:set-bound", b.Sol.Client.SetBound)
	} else {
		w.alarm.Clear("client:set-bound")
	}
	for _, obj := range []struct {
		name   string
		before agent.GuardiansView
		after  agent.GuardiansView
	}{{"the pausable ISM", a.Sol.Pausable, b.Sol.Pausable}, {"the admin program", a.Sol.Admin, b.Sol.Admin}} {
		for i := range obj.after.Keys {
			if obj.after.Keys[i] != obj.before.Keys[i] {
				w.alarm.Alarm("seat:"+obj.name+":"+strconv.Itoa(i+1), fmt.Sprintf("%s: seat %d is now %s (was %s)", obj.name, i+1, obj.after.Keys[i], obj.before.Keys[i]))
			}
			if obj.after.Flags[i] != nil && obj.before.Flags[i] == nil {
				w.alarm.Flag("solflag:"+obj.name+":"+strconv.Itoa(i+1), fmt.Sprintf("%s: seat %d (%s) flagged at %s", obj.name, i+1, obj.after.Keys[i], agent.FmtFlag(obj.after.Flags[i])))
			}
			if obj.after.Flags[i] == nil && obj.before.Flags[i] != nil {
				w.alarm.Alarm("solunflag:"+obj.name+":"+strconv.Itoa(i+1), fmt.Sprintf("%s: seat %d's flag cleared", obj.name, i+1))
				w.alarm.Clear("solflag:" + obj.name + ":" + strconv.Itoa(i+1))
			}
		}
		if obj.after.Hold && !obj.before.Hold {
			w.alarm.Flag("hold:"+obj.name, fmt.Sprintf("%s: a HOLD stands since %s", obj.name, agent.FmtFlag(obj.after.HoldSince)))
		}
		if !obj.after.Hold && obj.before.Hold {
			w.alarm.Alarm("hold-ended:"+obj.name, obj.name+": the hold ended")
			w.alarm.Clear("hold:" + obj.name)
		}
	}
	have := map[string]bool{}
	for _, d := range a.Sol.Pending {
		have[fmt.Sprint(d.Number)] = true
	}
	for _, d := range b.Sol.Pending {
		k := fmt.Sprint(d.Number)
		if !have[k] {
			w.alarm.Alarm("soldecision:"+k, fmt.Sprintf("decision %s (kind %d: %s) recorded on the admin program; matures at %s %s", k, d.Kind, d.Text, time.Unix(d.MaturesAt, 0).UTC().Format(time.RFC3339), d.Deferred))
		}
	}
	// a replacement's floor under a hold is a function of the hold history (AN4): an alarm when it appears or
	// moves (a hold began, or an earlier overlapping hold came into view)
	floors := map[string]int64{}
	for _, d := range a.Sol.Pending {
		floors[fmt.Sprint(d.Number)] = d.Floor
	}
	for _, d := range b.Sol.Pending {
		k := fmt.Sprint(d.Number)
		if d.Floor != 0 && floors[k] != d.Floor && have[k] {
			w.alarm.Alarm("solfloor:"+k, fmt.Sprintf("decision %s (kind %d): under a hold it lands no earlier than %s (day %d of the earliest hold overlapping its pending life, from the hold history)", k, d.Kind, time.Unix(d.Floor, 0).UTC().Format(time.RFC3339), sol.HoldReplacementDays))
		}
	}
	// the hold history grew: a hold ended (a dip of the grace's length, or a replacement's own clear)
	if len(b.Sol.Holds) > len(a.Sol.Holds) || len(b.Sol.Holds) > 0 && len(a.Sol.Holds) > 0 && b.Sol.Holds[len(b.Sol.Holds)-1] != a.Sol.Holds[len(a.Sol.Holds)-1] {
		h := b.Sol.Holds[len(b.Sol.Holds)-1]
		w.alarm.Alarm("hold-history:"+strconv.FormatInt(h.Start, 10), fmt.Sprintf("the admin program's hold history records a hold that ended: from %s to %s", time.Unix(h.Start, 0).UTC().Format(time.RFC3339), time.Unix(h.End, 0).UTC().Format(time.RFC3339)))
	}
	for i := range b.Sol.Limits {
		if x, y := a.Sol.Limits[i], b.Sol.Limits[i]; x.MaxPerMessage != y.MaxPerMessage || x.DayCap != y.DayCap {
			w.alarm.Alarm("limits:"+y.Symbol, fmt.Sprintf("%s's mint limits moved: %d a message, %d a day (were %d and %d)", y.Symbol, y.MaxPerMessage, y.DayCap, x.MaxPerMessage, x.DayCap))
		}
		// the day cap against the lock (AP6): an alarm while it exceeds a fifth of it — under the plan's key, the
		// monitors' own (K37: a consumer keyed on mint:cap-ratio missed the holder's), posted daily (G45, G35)
		if y := b.Sol.Limits[i]; y.Ratio != "" {
			w.alarm.AlarmEvery("mint:cap-ratio:"+y.Symbol, y.Ratio, 24*time.Hour)
		} else if y.Symbol != "" {
			w.alarm.Clear("mint:cap-ratio:" + y.Symbol)
		}
		// gno.land's per-message maximum above the ISM's (AS4): an alarm while it stands, daily
		if y := b.Sol.Limits[i]; y.Mismatch != "" {
			w.alarm.AlarmEvery("limits:mismatch:"+y.Symbol, y.Mismatch, 24*time.Hour)
		} else if y.Symbol != "" {
			w.alarm.Clear("limits:mismatch:" + y.Symbol)
		}
	}
	govSeen := map[uint64]bool{}
	for _, d := range a.GovRecent {
		govSeen[d.Number] = true
	}
	for _, d := range b.GovRecent {
		if !govSeen[d.Number] {
			w.alarm.Alarm("govdecision:"+strconv.FormatUint(d.Number, 10), fmt.Sprintf("r/hlgov dispatched decision %d (kind %d): %s", d.Number, d.Kind, d.Text))
		}
	}
	if b.Sol.Client.QuarantineEnd > a.Sol.Client.QuarantineEnd {
		w.alarm.Alarm("quarantine", fmt.Sprintf("a validator-set change: the quarantine stands until %s", quarantine(b.Sol.Client.QuarantineEnd)))
	}
	if b.Sol.Client.Mismatch != nil && a.Sol.Client.Mismatch == nil {
		w.alarm.Flag("header:mismatch", "HEADER MISMATCH: "+b.Sol.Client.Mismatch.String())
	}
	// the client's trusted header against this node's block at its height (G37): a difference is the fork of
	// §6.4 whether or not the record reaches it — a checkpointed record never does — flagged when it appears
	if c := b.Sol.Client; !c.Match && c.ChainHash != "" && (a.Sol.Client.Match || a.Sol.Client.ChainHash == "" || a.Sol.Client.TrustedHash != c.TrustedHash) {
		w.alarm.Flag("header:mismatch", fmt.Sprintf("HEADER MISMATCH: the client's trusted header %d hashes to %s, this node's block to %s — a fork or a forged header (§6.4: the cut is the slot it was accepted at; a checkpointed record has no entry for it)", c.TrustedHeight, short(c.TrustedHash), short(c.ChainHash)))
	}
	if b.Sol.LastSeq != b.Sol.GovLast && a.Sol.LastSeq == a.Sol.GovLast {
		w.alarm.Alarm("counters", fmt.Sprintf("the counters part: the admin program's %d, r/hlgov's %d", b.Sol.LastSeq, b.Sol.GovLast))
	}
	for i := len(a.Sol.Foreign); i < len(b.Sol.Foreign); i++ {
		w.alarm.Flag("foreign:"+strconv.Itoa(i), b.Sol.Foreign[i])
	}
	// a forged credit (AP1): a flag to the holder while the claim waits unindexed, cleared once it is indexed or gone
	forgedWas := map[string]bool{}
	for _, f := range a.Sol.Forged {
		forgedWas[forgedKey(f)] = true
	}
	forgedNow := map[string]bool{}
	for _, f := range b.Sol.Forged {
		forgedNow[forgedKey(f)] = true
		w.alarm.Flag(forgedKey(f), "FORGED CREDIT: "+f)
	}
	for k := range forgedWas {
		if !forgedNow[k] {
			w.alarm.Clear(k)
		}
	}
	for _, f := range b.Sol.VoidsMatch {
		w.alarm.Alarm("void:"+f[:16], f)
	}
	if len(b.Sol.Unconfirmed) > 0 && len(a.Sol.Unconfirmed) == 0 {
		w.alarm.Alarm("providers", "the two providers disagree: "+strings.Join(b.Sol.Unconfirmed, "; "))
	}
	if len(b.Sol.Unconfirmed) == 0 && len(a.Sol.Unconfirmed) > 0 {
		w.alarm.Clear("providers")
	}
}

// forgedKey is a forged-credit line's alarm key: its claim id ("forged credit <id> on …").
func forgedKey(line string) string {
	fields := strings.Fields(line)
	if len(fields) > 2 {
		return "forged:" + fields[2]
	}
	return "forged:" + line
}

// sortedSymbols are the symbols of two migration maps, sorted and without repeats.
func sortedSymbols(a, b map[string]agent.MigrationView) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]agent.MigrationView{a, b} {
		for sym := range m {
			if !seen[sym] {
				seen[sym] = true
				out = append(out, sym)
			}
		}
	}
	sort.Strings(out)
	return out
}

// refundedKey is a refunded-send line's alarm key: its nonce ("gno #<n> (…") under a prefix.
func refundedKey(line string) string { return nonceKey("refunded:", line) }

func nonceKey(prefix, line string) string {
	fields := strings.Fields(line)
	if len(fields) > 1 {
		return prefix + strings.TrimPrefix(fields[1], "#")
	}
	return prefix + line
}

// noteDiff alarms every keyed note of the new view and clears the ones of the old view that are gone.
func (w *watcher) noteDiff(was, now []switchNote) {
	wasKeys := map[string]bool{}
	for _, n := range was {
		wasKeys[n.key] = true
	}
	nowKeys := map[string]bool{}
	for _, n := range now {
		nowKeys[n.key] = true
		w.alarm.Alarm(n.key, n.text)
	}
	for k := range wasKeys {
		if !nowKeys[k] {
			w.alarm.Clear(k)
		}
	}
}

// lineDiff alarms every per-send line of the new view under its nonce's key and clears the old view's gone ones.
func (w *watcher) lineDiff(was, now []string, keyPrefix, textPrefix string) {
	wasKeys := map[string]bool{}
	for _, l := range was {
		wasKeys[nonceKey(keyPrefix, l)] = true
	}
	nowKeys := map[string]bool{}
	for _, l := range now {
		k := nonceKey(keyPrefix, l)
		nowKeys[k] = true
		w.alarm.Alarm(k, textPrefix+l)
	}
	for k := range wasKeys {
		if !nowKeys[k] {
			w.alarm.Clear(k)
		}
	}
}

// sectionOf is the section a not-read line names ("the client's record: …" → "the client's record").
func sectionOf(line string) string {
	s, _, _ := strings.Cut(line, ":")
	return strings.TrimSpace(s)
}

// identityDiff alarms the holder on each route's identity verdict (K13, §5.8: "alarm its holder directly"): a
// flag on BROKEN (the right side exceeds the lock: a forged credit, or a supply nobody locked for), an alarm on
// a short right side beyond what is in flight, and the clear once it holds.
func (w *watcher) identityDiff(b *view) {
	for _, r := range b.Routes {
		id, ok := b.Sol.Identities[r.Symbol]
		if !ok {
			continue
		}
		key := "identity:" + r.Symbol
		switch v, text := id.Verdict(); v {
		case agent.Broken:
			w.alarm.Flag(key, "BROKEN: "+text)
		case agent.Short:
			w.alarm.Alarm(key, "short (a mistaken void, or more in flight than counted): "+text)
		default:
			w.alarm.Clear(key)
		}
	}
}

// ----------------------------------------------------------------- flag --

// flag sets or clears the holder's flag in one stroke: hlmailbox.SetFlag / ClearFlag through the holder's gno
// key from the holder's node, and SetFlag / ClearFlag on the pausable ISM and the admin program through the
// holder's Solana key. A payouts flag may name ids; each is checked against the dispatch index and the identity
// and its verdict printed (forged, a dispatch record, or unindexed — re-read); on a reading the two providers
// did not confirm, no id is named.
func (w *watcher) flag(ctx context.Context, set bool, home, key, solKey, route, fn, ids string) error {
	path := route
	symbol := strings.ToUpper(route)
	if r := w.cfg.RouteBySymbol(symbol); r != nil {
		path = r.Gno
	} else {
		for _, r := range w.cfg.Routes {
			if r.Gno == route {
				symbol = r.Symbol
			}
		}
	}
	if fn != "sends" && fn != "credits" && fn != "payouts" {
		return fmt.Errorf("-fn is sends, credits or payouts, not %q", fn)
	}
	if ids != "" && (fn != "payouts" || !set) {
		return fmt.Errorf("only a payouts flag names ids")
	}
	sk, err := sol.PrivateKeyFromFile(solKey)
	if err != nil {
		return fmt.Errorf("the Solana key: %w", err)
	}
	if !w.cfg.HasPrograms() {
		return fmt.Errorf("the config names no pausable ISM and admin program: a flag on gno alone is not the one-stroke flag of §5.8")
	}
	progs := w.cfg.Programs()
	slot, err := agent.SeatOnSolana(ctx, agent.TwoProviders(w.providers), progs, sk.PublicKey()) // both providers (G26)
	if errors.Is(err, agent.ErrProviders) {
		return fmt.Errorf("the seat on the pausable ISM: %s: %w", unconfirmed, err)
	}
	if err != nil {
		return err
	}
	if slot == 0 {
		return fmt.Errorf("%s holds no seat on the pausable ISM: only a guardian or a watch key flags", sk.PublicKey())
	}
	c := w.cfg.SourceClient(w.nodeURL)
	c.Home, c.Key = home, key
	addr, err := c.KeyAddress(ctx)
	if err != nil {
		return err
	}
	mb, err := agent.ReadMailbox(ctx, w.gno, w.cfg.Gno.Mailbox, 0)
	if err != nil {
		return err
	}
	seat := 0
	for i, s := range mb.Seats {
		if s == addr {
			seat = i + 1
		}
	}
	if seat == 0 {
		return fmt.Errorf("%s (%s) holds no seat in hlmailbox: only a guardian or a watch key flags", key, addr)
	}
	// the ids a payouts flag names, each with its verdict (the index and the identity from this node and two providers)
	var list []string
	if ids != "" {
		v, err := w.view(ctx)
		if err != nil {
			return err
		}
		if v.Sol == nil {
			return fmt.Errorf("the Solana side could not be read (%s): a payouts flag naming ids is refused on anything less than a node and two providers", v.SolErr)
		}
		if len(v.Sol.Unconfirmed) > 0 {
			return fmt.Errorf("the two providers disagree (%s): a payouts flag naming ids is refused on anything less than two agreeing providers", strings.Join(v.Sol.Unconfirmed, "; "))
		}
		for _, e := range v.Sol.Err {
			if strings.HasPrefix(e, "the dispatch index") {
				return fmt.Errorf("%s: a payouts flag naming ids is refused while the dispatch index is incomplete on a provider (an id absent from a failed scan is not forged, G21)", e)
			}
		}
		// a range in place of a list (M1c, AN6): "heights:<from>-<to>" covers every claim credited at those
		// heights, hand credits too; "since:<from>" the verifier's credits from that height on — passed as the one
		// string SetFlag takes, no id to judge
		if strings.HasPrefix(ids, "heights:") || strings.HasPrefix(ids, "since:") {
			if strings.Contains(ids, ",") {
				return fmt.Errorf("a range (%q) stands alone: no list beside it", ids)
			}
			fmt.Printf("%s: a range of credits (the realm voids by heights; the dispatch index cannot judge a range id by id)\n", ids)
			list = append(list, ids)
		}
		for _, id := range strings.Split(ids, ",") {
			id = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(id, "0x")))
			if id == "" || len(list) == 1 && list[0] == ids {
				continue
			}
			k, err := hl.ParseKey32(id)
			if err != nil {
				return fmt.Errorf("id %q is not a 32-byte message id (or a range: heights:<from>-<to>, since:<from>)", id)
			}
			amount := int64(0)
			if c, err := agent.ClaimOf(ctx, w.gno, path, hex.EncodeToString(k[:]), v.Head.Height); err == nil && c != nil {
				amount = c.Amount
			}
			fmt.Printf("%s: %s\n", id, v.Sol.ForgedVerdict(w.st, id, symbol, amount))
			list = append(list, hex.EncodeToString(k[:]))
		}
	}
	var res *gno.Result
	if set {
		res, err = c.Call(ctx, w.cfg.Gno.Mailbox, "SetFlag", 45_000_000, path, fn, strings.Join(list, ","))
	} else {
		res, err = c.Call(ctx, w.cfg.Gno.Mailbox, "ClearFlag", 45_000_000, path, fn)
	}
	if err != nil {
		return err
	}
	what := "set"
	if !set {
		what = "cleared"
	}
	fmt.Printf("%s %s on %s by seat %d (%s): %s\n", fn, what, shortPath(path), seat, addr, res.Hash)
	sig, err := agent.FlagSolana(ctx, sol.NewClient(w.urls[0], sk), progs, set)
	if agent.LineUnreadAfterStroke(sig, err) {
		// the pausable half landed (G40): the holder's pause stands; the admin program's line is the warning
		fmt.Printf("the flag of slot %d %s on the pausable ISM and the configured admin program (%s): %s\n", slot, what, sk.PublicKey(), sig)
		return fmt.Errorf("WARNING: %w", err)
	}
	if err != nil {
		return fmt.Errorf("the gno flag is %s; the Solana stroke failed: %w", what, err)
	}
	fmt.Printf("the flag of slot %d %s on the pausable ISM and the admin program (%s): %s\n", slot, what, sk.PublicKey(), sig)
	return nil
}

// finish calls r/gov/dao's ExecuteOrRejectProposal for one proposal, or for every builder-made one with the votes
// that is past its 14 days by this state's first sight of it (F2): the builder's closure returns the deadline
// error, and only this call denies the proposal for good.
func (w *watcher) finish(ctx context.Context, home, key string, pid int64) error {
	c := w.cfg.SourceClient(w.nodeURL)
	c.Home, c.Key = home, key
	var pids []int64
	if pid != 0 {
		pids = []int64{pid}
	} else {
		v, err := w.view(ctx)
		if err != nil {
			return err
		}
		if v.DAOErr != "" {
			return fmt.Errorf("%s", v.DAOErr)
		}
		for _, p := range v.Proposals {
			if p.Armed && v.pastDeadline(p) {
				pids = append(pids, p.ID)
			}
		}
		if len(pids) == 0 {
			fmt.Println("no builder-made proposal with the votes is past its 14 days")
			return nil
		}
	}
	for _, p := range pids {
		res, err := c.Call(ctx, w.cfg.Gno.DAO, "ExecuteOrRejectProposal", 45_000_000, strconv.FormatInt(p, 10))
		if err != nil {
			return fmt.Errorf("proposal %d: %w", p, err)
		}
		fmt.Printf("proposal %d finished (executed, or denied past its deadline): %s\n", p, res.Hash)
	}
	return nil
}

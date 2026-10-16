package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The audit's gno.land side (E3-CF2, E3-RT5, E3-GS4): the deployment's record is the deployer's, and initialize checks
// only its trusted header's own consistency and its hour — a deployer can root the client in a header and a set of
// its own keys, and every on-chain and creation-input check then agrees with that same record. So the trust root is
// held to gno.land itself, through gno sources independent of the deployer (-gno-rpc, or the config's gno.rpc and
// gno.sources: each read, all must agree); without one the audit's verdict is UNVERIFIED, never PASS. The same
// sources give the other independent readings where they exist: r/hlgov's counter, start and decision recipient,
// its Ethereum seats, the routes' maximums, and every decision r/hlgov dispatched (the governed changes, E3-CF9).

// gnoSource is one gno node the audit reads, by its URL (printed redacted); own when the member named it (-gno-rpc),
// not when it is only the operator's config's.
type gnoSource struct {
	url string
	c   *gno.Client
	own bool
}

// gnoSourcesOf are the gno sources: -gno-rpc's (comma separated), the member's own, and the config's gno.rpc and
// gno.sources — the operator's nodes (bridge.json is the machines' config, the operator's file: E5-CF3 item 3) — each
// once. Every source is read and all must agree, so the operator's nodes beside the member's can only fail a reading,
// never carry one; read alone they hold gno.land to the operator's word, and the verdict is UNVERIFIED
// (runAuditResult).
func gnoSourcesOf(flagValue, configPath string) ([]gnoSource, *configFacts, error) {
	var own, theirs []string
	for _, u := range strings.Split(flagValue, ",") {
		if u = strings.TrimSpace(u); u != "" {
			own = append(own, u)
		}
	}
	var cf *configFacts
	if configPath != "" {
		var err error
		if cf, err = readConfigFacts(configPath); err != nil {
			return nil, nil, err
		}
		theirs = append([]string{cf.Gno.RPC}, cf.Gno.Sources...)
	}
	seen := map[string]bool{}
	var out []gnoSource
	for i, u := range append(own, theirs...) {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, gnoSource{url: u, c: &gno.Client{RPC: u}, own: i < len(own)})
	}
	return out, cf, nil
}

// ownSources counts the gno sources the member named (-gno-rpc).
func ownSources(gs []gnoSource) int {
	n := 0
	for _, s := range gs {
		if s.own {
			n++
		}
	}
	return n
}

// configFacts is what the audit reads of a machines' config (bridge.json) — the operator's file: its gno sources, read
// beside the member's own, and the lock each route expects, only noted beside the member's -expected-lock-* (E5-CF3
// item 3). Nothing else of it is taken: the deployment's own values come from the chain.
type configFacts struct {
	Gno struct {
		RPC     string   `json:"rpc"`
		Sources []string `json:"sources"`
	} `json:"gno"`
	Routes []struct {
		Symbol       string `json:"symbol"`
		Gno          string `json:"gno"`
		Token        string `json:"token"` // the HypERC20's address: what the expected lock is matched to (E4-TL1)
		ExpectedLock uint64 `json:"expectedLock"`
	} `json:"routes"`
}

func readConfigFacts(path string) (*configFacts, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c configFacts
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("-config %s: %w", path, err)
	}
	return &c, nil
}

// gnoLine prints a gno read before its answer, as the Ethereum reads print a curl line: a member repeats it against
// their own node (the source's URL is never printed whole).
func (a *audit) gnoLine(method, arg string) {
	fmt.Fprintf(a.out, "  gno> %s %s (from %d source(s): %s)\n", method, arg, len(a.gnos), a.gnoNames())
}

func (a *audit) gnoNames() string {
	var parts []string
	for _, s := range a.gnos {
		parts = append(parts, ethx.Redact(s.url))
	}
	return strings.Join(parts, ", ")
}

// gnoWhose names each source with whose it is: the member's (-gno-rpc) or the operator's config's.
func (a *audit) gnoWhose() string {
	var parts []string
	for _, s := range a.gnos {
		whose := "the config's: the operator's"
		if s.own {
			whose = "-gno-rpc: yours"
		}
		parts = append(parts, ethx.Redact(s.url)+" ("+whose+")")
	}
	return strings.Join(parts, ", ")
}

// errRealmAbsent is the reading of a realm that is not on the chain: every gno source answers the keeper's not-found
// for its package (gnoAgree).
var errRealmAbsent = errors.New("the realm is not on the chain: every gno source answers the keeper's \"package not found\" for it")

// realmAbsent says whether a reading found the realm absent — every source agreeing (E4-TL2), never one source's
// "not found" beside another's answer.
func realmAbsent(err error) bool { return errors.Is(err, errRealmAbsent) }

// keeperNotFound says whether a render failed because the package is not on that node's chain: the VM keeper's own
// answer, an InvalidPkgPathError whose message is "package not found: <pkgPath>" for this very path (gno
// v1.2.0–v1.5.1: withQueryEvalMachine; the query's log is the error's %#v, its message trace inside) — never any
// other "not found" (a JSON-RPC "method not found", a proxy's 404 page, another package's), which is a failure to
// read, not the realm's absence.
func keeperNotFound(err error, pkgPath string) bool {
	if err == nil || pkgPath == "" {
		return false
	}
	e := err.Error()
	if !strings.Contains(e, "InvalidPkgPathError") {
		return false
	}
	want := "package not found: " + pkgPath
	for rest := e; ; {
		i := strings.Index(rest, want)
		if i < 0 {
			return false
		}
		rest = rest[i+len(want):]
		if rest == "" || !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./", rune(rest[0])) {
			return true // the path ends there: this package, not one whose path it begins
		}
	}
}

// gnoAgree reads one thing from every source as bytes, every source before judging (E4-TL2): the reading is the
// sources' common answer; a source that fails fails it, and sources that differ are a disagreement — the first
// source's error is never the reading while another answers. absent, when not nil, names the one failure sources may
// agree on: the realm is not on their chain (keeperNotFound). Every source saying so is errRealmAbsent; a mix of it
// with answers is a disagreement — a source behind the realm's upload, which must not turn the others' reading (and a
// FAIL on it) into the realm's absence.
func (a *audit) gnoAgree(what string, read func(g *gno.Client) ([]byte, error), absent func(error) bool) ([]byte, error) {
	if len(a.gnos) == 0 {
		return nil, errors.New("no gno source")
	}
	got := make([]gnoAnswer, len(a.gnos))
	for i, s := range a.gnos {
		got[i] = answerOf(read, s.c, absent)
	}
	v, err, _ := a.judgeAnswers(what, got, "")
	return v, err
}

// gnoAnswer is one source's answer to a reading: its bytes, or its failure (absent: the realm is not on its chain);
// when the reading was pinned (gnoAgreeLive), read with the source's head at height before and after it.
type gnoAnswer struct {
	v      []byte
	err    error
	absent bool
	pinned bool
	height int64
}

func answerOf(read func(g *gno.Client) ([]byte, error), c *gno.Client, absent func(error) bool) gnoAnswer {
	v, err := read(c)
	return gnoAnswer{v: v, err: err, absent: err != nil && absent != nil && absent(err)}
}

// judgeAnswers is the sources' common answer (gnoAgree's rule), and whether a failure is the sources' disagreement —
// what a reading that moves with gno.land's state may show between two honest sources a block apart (gnoAgreeLive).
// at names the height the answers were all read at, when pinned.
func (a *audit) judgeAnswers(what string, got []gnoAnswer, at string) ([]byte, error, bool) {
	nAbsent := 0
	var failed []string
	for i, g := range got {
		switch {
		case g.absent:
			nAbsent++
		case g.err != nil:
			failed = append(failed, fmt.Sprintf("gno source %s: %s", ethx.Redact(a.gnos[i].url), ethx.Redact(g.err.Error())))
		}
	}
	if len(failed) > 0 {
		return nil, fmt.Errorf("%s (%s)", what, strings.Join(failed, "; ")), false
	}
	if nAbsent == len(got) {
		return nil, fmt.Errorf("%s: %w", what, errRealmAbsent), false
	}
	var first []byte
	firstAt := -1
	for i, g := range got {
		if g.absent {
			continue
		}
		if firstAt < 0 {
			first, firstAt = g.v, i
			continue
		}
		if !bytes.Equal(g.v, first) {
			return nil, fmt.Errorf("%s: the gno sources disagree%s (%s answers %.120q, %s %.120q)", what, at, ethx.Redact(a.gnos[firstAt].url), first, ethx.Redact(a.gnos[i].url), g.v), true
		}
	}
	if nAbsent > 0 {
		var none []string
		for i, g := range got {
			if g.absent {
				none = append(none, ethx.Redact(a.gnos[i].url))
			}
		}
		return nil, fmt.Errorf("%s: the gno sources disagree%s (%s answers %.120q; %s say(s) the package is not on the chain — a node behind the realm's upload, or not this chain's: every source must agree)", what, at, ethx.Redact(a.gnos[firstAt].url), first, strings.Join(none, ", ")), true
	}
	return first, nil, false
}

// pinWait bounds the pinned re-read of a reading that moves with gno.land's state (gnoAgreeLive): how long its sources
// are read again to bring them to one height — a few of gno.land's blocks; a source still apart past it is not a
// block behind but lagging, and the disagreement stands. pinPoll is how often a source behind is read again.
var (
	pinWait = 30 * time.Second
	pinPoll = 300 * time.Millisecond
)

// gnoAgreeLive is gnoAgree for a reading that moves with gno.land's state — a render, which gno nodes answer at their
// latest state whatever height is asked: a route's lock moves with every send and payout, r/hlgov's renders with
// every dispatch, so two honest sources a block apart may answer differently (E5-CF3 item 2). A disagreement is read
// again at a pinned moment before it is one: each source's answer bracketed by its head (status) before and after and
// kept only when the head did not move across it, and the sources behind the highest head read again until each
// answers at that one height (the target rising with any source that passes it), for pinWait at most. Answers at one
// height must be equal — a difference there is the disagreement it was; a source that cannot be brought to the others'
// height (its head does not read, or it stays apart past pinWait) leaves the first disagreement standing, said so.
func (a *audit) gnoAgreeLive(ctx context.Context, what string, read func(g *gno.Client) ([]byte, error), absent func(error) bool) ([]byte, error) {
	if len(a.gnos) == 0 {
		return nil, errors.New("no gno source")
	}
	got := make([]gnoAnswer, len(a.gnos))
	for i, s := range a.gnos {
		got[i] = answerOf(read, s.c, absent)
	}
	v, err, disagree := a.judgeAnswers(what, got, "")
	if !disagree {
		return v, err
	}
	pinned, h, why := a.pinnedAnswers(ctx, read, absent)
	if why != "" {
		return nil, fmt.Errorf("%w — read again at a pinned moment: %s", err, why)
	}
	v, perr, _ := a.judgeAnswers(what, pinned, fmt.Sprintf(" at one height, %d (every source read again at a pinned moment: its head the same before and after)", h))
	if perr != nil {
		return nil, perr
	}
	a.note("%s: the sources answered alike once read at one height, %d (first read a block or more apart: a reading that moves with gno.land's state)", what, h)
	return v, nil
}

// pinnedAnswers reads every source at one height (gnoAgreeLive): each answer kept only with its source's head the same
// before and after it; the sources behind the highest head read again until each answers there. why says what kept
// them apart (a head that did not read, pinWait passed).
func (a *audit) pinnedAnswers(ctx context.Context, read func(g *gno.Client) ([]byte, error), absent func(error) bool) ([]gnoAnswer, int64, string) {
	deadline := time.Now().Add(pinWait)
	got := make([]gnoAnswer, len(a.gnos))
	var target int64
	for {
		for i, s := range a.gnos {
			if got[i].pinned && got[i].height == target {
				continue // read at the target already
			}
			h0, err := s.c.Status(ctx)
			if err != nil {
				return nil, 0, fmt.Sprintf("gno source %s's head did not read (%s)", ethx.Redact(s.url), ethx.Redact(err.Error()))
			}
			if h0.Height < target {
				continue // behind: read again next round
			}
			ans := answerOf(read, s.c, absent)
			h1, err := s.c.Status(ctx)
			if err != nil {
				return nil, 0, fmt.Sprintf("gno source %s's head did not read (%s)", ethx.Redact(s.url), ethx.Redact(err.Error()))
			}
			if h1.Height != h0.Height {
				continue // its head moved across the read: read again
			}
			ans.pinned, ans.height = true, h1.Height
			got[i] = ans
			if ans.height > target {
				target = ans.height
			}
		}
		at := true
		for _, g := range got {
			at = at && g.pinned && g.height == target
		}
		if at {
			return got, target, ""
		}
		if time.Now().After(deadline) {
			var heads []string
			for i, g := range got {
				at := "never read with its head still"
				if g.pinned {
					at = fmt.Sprintf("at %d", g.height)
				}
				heads = append(heads, ethx.Redact(a.gnos[i].url)+" "+at)
			}
			return nil, 0, fmt.Sprintf("the sources were not brought to one height in %s (%s): a source that far apart is lagging, not a block behind", pinWait, strings.Join(heads, ", "))
		}
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err().Error()
		case <-time.After(pinPoll):
		}
	}
}

// gnoRender is a realm's Render(path) at the latest height, agreed by every source (read again at one height when they
// differ: gnoAgreeLive); errRealmAbsent when every source answers the keeper's not-found for pkg.
func (a *audit) gnoRender(ctx context.Context, pkg, path string) (string, error) {
	a.gnoLine("abci_query vm/qrender", strconv.Quote(pkg+":"+path))
	b, err := a.gnoAgreeLive(ctx, pkg+":"+path, func(g *gno.Client) ([]byte, error) {
		s, err := g.Render(ctx, pkg, path)
		return []byte(s), err
	}, func(err error) bool { return keeperNotFound(err, pkg) })
	return string(b), err
}

// gnoBlock is gno.land's block at a height, agreed by every source (the header's every field and the hash the node
// gives it), its hash checked against its header's fields.
func (a *audit) gnoBlock(ctx context.Context, h int64) (*gnolight.Block, error) {
	a.gnoLine("block", "height="+strconv.FormatInt(h, 10))
	var blk *gnolight.Block
	_, err := a.gnoAgree(fmt.Sprintf("block %d", h), func(g *gno.Client) ([]byte, error) {
		b, err := g.BlockAt(ctx, h)
		if err != nil {
			return nil, err
		}
		if blk == nil {
			blk = b
		}
		var key []byte
		for _, l := range b.Header.Leaves() {
			key = append(key, l...)
		}
		return append(key, b.Hash[:]...), nil
	}, nil)
	if err != nil {
		return nil, err
	}
	if got := blk.Header.Hash(); got != blk.Hash {
		return nil, fmt.Errorf("block %d: its header's fields hash to %x, the node says %x", h, got, blk.Hash)
	}
	return blk, nil
}

// gnoSet is gno.land's validator set at a height, agreed by every source (by its hash).
func (a *audit) gnoSet(ctx context.Context, h int64) (*gnolight.ValidatorSet, [32]byte, error) {
	a.gnoLine("validators", "height="+strconv.FormatInt(h, 10))
	var set *gnolight.ValidatorSet
	b, err := a.gnoAgree(fmt.Sprintf("validators at %d", h), func(g *gno.Client) ([]byte, error) {
		s, err := g.ValidatorsAt(ctx, h)
		if err != nil {
			return nil, err
		}
		if set == nil {
			set = s
		}
		sh, ok := s.Hash()
		if !ok {
			return nil, errors.New("the set does not hash")
		}
		return sh[:], nil
	}, nil)
	if err != nil {
		return nil, [32]byte{}, err
	}
	var sh [32]byte
	copy(sh[:], b)
	return set, sh, nil
}

// launchSetHash is the record's launch validator set and its hash (false when a key does not read).
func launchSetHash(l Launch) (*gnolight.ValidatorSet, [32]byte, error) {
	set := &gnolight.ValidatorSet{}
	for _, v := range l.Validators {
		pk, err := hl.ParseKey32(v.PubKey)
		if err != nil {
			return nil, [32]byte{}, fmt.Errorf("the record's validator key %q: %w", v.PubKey, err)
		}
		set.Validators = append(set.Validators, gnolight.Validator{PubKey: pk, Power: v.Power})
	}
	h, ok := set.Hash()
	if !ok {
		return nil, [32]byte{}, errors.New("the record's launch set does not hash")
	}
	return set, h, nil
}

// trustRoot holds the header and the validator set the client's initialize took to gno.land's own block at that
// height (E3-CF2, E3-RT5): initialize took the sixteen leaves the record keeps — the client's creation input carries
// them byte for byte, held below — so each must be the node's header's; the hash, the height, the time and the
// validators hash are said one by one, and the set the client took (the record's, which its first set entry hashes
// to) must be the node's validators at that height. A client rooted in a header of the deployer's own making — a
// fabricated header over a set of its keys, consistent and within the hour — fails here and nowhere else.
func (a *audit) trustRoot(ctx context.Context, first ethx.SetEntry) {
	l := a.d.Launch
	h0 := l.TrustedHeight
	blk, err := a.gnoBlock(ctx, h0)
	if err != nil {
		a.fail(fmt.Sprintf("gno.land's block %d, the client's trust root (a gno node that keeps that height)", h0), err)
		return
	}
	hd := blk.Header
	node := ethx.HeaderLeaves(hd)
	same := len(l.TrustedLeaves) == len(node)
	for i := 0; same && i < len(node); i++ {
		b, err := hex.DecodeString(strings.TrimPrefix(l.TrustedLeaves[i], "0x"))
		same = err == nil && bytes.Equal(b, node[i])
	}
	a.check(same, "the header the client's initialize took (the record's sixteen leaves, which the client's creation input carries byte for byte) is gno.land's block %d's header, leaf for leaf", h0)
	a.check(strings.EqualFold(strings.TrimPrefix(l.TrustedHash, "0x"), hex.EncodeToString(blk.Hash[:])), "the trust root's hash %s is gno.land's block %d hash %x", l.TrustedHash, h0, blk.Hash)
	a.check(hd.Height == h0, "the trust root's height %d is gno.land's block's %d", h0, hd.Height)
	a.check(hd.Time.Secs == l.TrustedTime, "the trust root's time %s is gno.land's block %d time %s", time.Unix(l.TrustedTime, 0).UTC().Format(time.RFC3339), h0, time.Unix(hd.Time.Secs, 0).UTC().Format(time.RFC3339))
	a.check(hd.ChainID == l.GnoChainID, "the trust root's chain id %q is gno.land's block's %q", l.GnoChainID, hd.ChainID)
	_, recSet, err := launchSetHash(l)
	if err != nil {
		a.check(false, "%v", err)
		return
	}
	a.check(hd.ValidatorsHashArray() == recSet, "the trust root's validators hash %x is gno.land's block %d's %x", recSet, h0, hd.ValidatorsHashArray())
	set, nodeSet, err := a.gnoSet(ctx, h0)
	if err != nil {
		a.fail(fmt.Sprintf("gno.land's validators at height %d, the client's trust root", h0), err)
		return
	}
	a.check(nodeSet == recSet && first.SetHash == recSet, "the validator set the client took (the record's %d validators, hash %x; the client's first set entry %x) is gno.land's set at height %d: %d validators, hash %x", len(l.Validators), recSet, first.SetHash, h0, len(set.Validators), nodeSet)
}

// ---------------------------------------------------------------------------------------- r/hlgov --

// govFacts is r/hlgov as the gno sources render it: its json, and whether it is on the chain at all.
type govFacts struct {
	Absent bool
	Gov    *gnoside.Gov
	Err    error
}

// gov reads r/hlgov's json once (cached): its counter, its compiled start, its decision recipient and former ones.
func (a *audit) govRead(ctx context.Context) *govFacts {
	if a.gov != nil {
		return a.gov
	}
	a.gov = &govFacts{}
	if len(a.gnos) == 0 {
		a.gov.Err = errors.New("no gno source")
		return a.gov
	}
	s, err := a.gnoRender(ctx, a.hlgovPath, "json")
	switch {
	case realmAbsent(err):
		a.gov.Absent = true
	case err != nil:
		a.gov.Err = err
	default:
		var g gnoside.Gov
		if err := gnoside.JSON(s, &g); err != nil {
			a.gov.Err = err
		} else {
			a.gov.Gov = &g
		}
	}
	return a.gov
}

// govDecision is r/hlgov's decision/<n>: the body it dispatched under that number (nil: none), cached.
func (a *audit) govDecision(ctx context.Context, n uint64) (*gnoside.GovDecision, error) {
	if d, ok := a.govDecisions[n]; ok {
		return d, nil
	}
	s, err := a.gnoRender(ctx, a.hlgovPath, "decision/"+strconv.FormatUint(n, 10))
	if err != nil {
		return nil, err
	}
	var d *gnoside.GovDecision
	if strings.TrimSpace(s) != "null" {
		d = &gnoside.GovDecision{}
		if err := gnoside.JSON(s, d); err != nil {
			return nil, err
		}
	}
	a.govDecisions[n] = d
	return d, nil
}

// govBody is the kind and parameters of r/hlgov's decision n (its body: kind u8 ‖ number u64 ‖ params).
func govBody(d *gnoside.GovDecision, n uint64) (uint8, []byte, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(d.Body, "0x"))
	if err != nil || len(b) < 9 {
		return 0, nil, fmt.Errorf("r/hlgov's decision %d renders no body that reads (%q)", n, d.Body)
	}
	if got := binaryU64(b[1:9]); got != n {
		return 0, nil, fmt.Errorf("r/hlgov's decision %d carries the number %d in its body", n, got)
	}
	return b[0], b[9:], nil
}

func binaryU64(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}

// govSeatRE is a seat line of r/hlgov's page ("Ethereum seats as last set here").
var govSeatRE = regexp.MustCompile("(?m)^\\s*- seat ([1-6]) \\((?:guardian|watch key)\\): `(0x[0-9a-fA-F]{40})`")

// govSeats are r/hlgov's Ethereum seats as it last set them (its page: the launch seats it compiles, each moved by a
// kind 5 it dispatched), seats 1–4 and watch slots 5–6.
func (a *audit) govSeats(ctx context.Context) ([6]common.Address, error) {
	var out [6]common.Address
	s, err := a.gnoRender(ctx, a.hlgovPath, "")
	if err != nil {
		return out, err
	}
	found := 0
	for _, m := range govSeatRE.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[1])
		if out[n-1] == (common.Address{}) {
			out[n-1] = common.HexToAddress(m[2])
			found++
		}
	}
	if found != 6 {
		return out, fmt.Errorf("r/hlgov's page shows %d of its six Ethereum seats", found)
	}
	return out, nil
}

// routeFigures are the figures of a route's json the audit reads: what its sources must agree on.
type routeFigures struct {
	Maximum int64 `json:"maximum"`
	Lock    int64 `json:"lock"`
}

// routeRead is a route realm's json (nil, nil when it is not on the chain), its sources agreeing on the figures the
// audit reads — the per-message maximum and the lock — not on the whole render: the meter in it refills by the block
// time, so two nodes a block apart, or one node read across a block, never render it alike, and a member's several
// sources would disagree on every audit (gno nodes answer a render at the latest state whatever height is asked). The
// lock moves with every send and payout, so sources that differ on it are read again at one height (gnoAgreeLive,
// E5-CF3 item 2) before they disagree.
func (a *audit) routeRead(ctx context.Context, path string) (*gnoside.Route, error) {
	a.gnoLine("abci_query vm/qrender", strconv.Quote(path+":json")+" (its maximum and lock)")
	b, err := a.gnoAgreeLive(ctx, path+":json's maximum and lock", func(g *gno.Client) ([]byte, error) {
		s, err := g.Render(ctx, path, "json")
		if err != nil {
			return nil, err
		}
		var r gnoside.Route
		if err := gnoside.JSON(s, &r); err != nil {
			return nil, err
		}
		return json.Marshal(routeFigures{Maximum: r.Maximum, Lock: r.Lock})
	}, func(err error) bool { return keeperNotFound(err, path) })
	if realmAbsent(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f routeFigures
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return &gnoside.Route{Maximum: f.Maximum, Lock: f.Lock}, nil
}

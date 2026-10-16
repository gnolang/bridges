package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// ethereumChain is Ethereum as the site reads it (bridge/SITE.md, E-M4; eth/bridge/PLAN.md §5.8): our deployment of
// Hyperlane's Mailbox and two HypERC20 tokens, the PausableGate, HlAdmin and the GnoLightClient, through one
// provider (the monitors and the signers read two that must agree, PLAN.md §5.6; the site shows, it never flags).
// The status's cut reads the supplies at the finalized block, each call pinned to its hash; everything else —
// the flags, the limits, the client, the pending decisions, a transfer's progress — is read at the newest block,
// as the Solana side reads those at confirmed.
type ethereumChain struct {
	cfg       ChainConfig
	c         ethCaller
	gnoDomain uint32
	// realms are every bridge realm's address on gno.land, refused as a burn's recipient (§5.2, K21)
	realms func() []string

	mailbox, gate, admin, client ethAddr
	tokens                       []ethToken

	mu      sync.Mutex
	chainOK bool                   // the provider answered the configured chain id once
	gateOK  bool                   // the gate's token(i) were checked against the config once
	blocks  map[uint64]ethBlock    // final blocks read, by number: a block at or below the finalized one never changes
	headers map[[32]byte]ethHeader // the headers the client recorded, by hash, as the activity read them
	set     []int64                // the powers of the set the newest accepted update carried (its calldata), for the picture
	setTx   string
	txs     map[string]*processRead // delivering calls decoded, by transaction hash
	factors map[ethAddr]uint64      // the contracts' compiled clock factors
}

// ethToken is one route on Ethereum: the token contract (its own router), its place in the gate's limits, and the
// route realm's address on gno.land as a message's recipient carries it.
type ethToken struct {
	TokenConfig
	addr  ethAddr
	index int
	route [32]byte
}

// ethHeader is one header the client recorded, as its HeaderRecorded log says.
type ethHeader struct {
	Height int64
	Time   int64
	Block  uint64
	Tx     string
	Proven bool
}

func newEthereum(cfg ChainConfig, c ethCaller, gnoDomain uint32, realms func() []string) *ethereumChain {
	must := func(s string) ethAddr {
		a, err := parseEthAddress(s)
		if err != nil {
			panic("hlsite: an unchecked Ethereum config: " + s)
		}
		return a
	}
	e := &ethereumChain{cfg: cfg, c: c, gnoDomain: gnoDomain, realms: realms,
		mailbox: must(cfg.Programs.Mailbox), gate: must(cfg.Programs.Pausable), admin: must(cfg.Programs.Admin), client: must(cfg.Programs.Client)}
	for _, t := range cfg.Tokens {
		route, err := hl.GnoAddressToBytes32(hl.GnoPackageAddress(t.Realm))
		if err != nil {
			panic("hlsite: a route realm with no address: " + t.Realm)
		}
		e.tokens = append(e.tokens, ethToken{TokenConfig: t, addr: must(t.Program), index: tokenIndex(t.Symbol), route: route})
	}
	e.resetCaches()
	return e
}

// resetCaches forgets what was read (the mock's reset: its world starts again).
func (e *ethereumChain) resetCaches() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.chainOK, e.gateOK = false, false
	e.blocks, e.headers, e.txs, e.factors = map[uint64]ethBlock{}, map[[32]byte]ethHeader{}, map[string]*processRead{}, map[ethAddr]uint64{}
	e.set, e.setTx = nil, ""
}

func (e *ethereumChain) link(kind, k string) string {
	if e.cfg.Explorer == "" {
		return ""
	}
	return strings.TrimRight(e.cfg.Explorer, "/") + "/" + kind + "/" + k
}

func (e *ethereumChain) TxLink(h string) string      { return e.link("tx", h) }
func (e *ethereumChain) AccountLink(a string) string { return e.link("address", a) }
func (e *ethereumChain) KindName(kind byte) string   { return ethKindName(kind) }

// Address is an Ethereum address from 32 bytes: its EIP-55 form when the top twelve bytes are zero, else the 32
// bytes whole (no address: Hyperlane's bytes32ToAddress would revert a delivery to it).
func (e *ethereumChain) Address(b [32]byte) string {
	if a, ok := addrOf32(b); ok {
		return a.String()
	}
	return hex0x(b[:])
}

// IsSignature is whether a reference is an Ethereum transaction hash: 0x and 64 hex digits. A message id and a gno
// transaction hash in hex have the same form, so the tracker tries those first (transfer.go).
func (e *ethereumChain) IsSignature(ref string) bool { return isTxHash(ref) }

func isTxHash(s string) bool {
	if len(s) != 66 || s[:2] != "0x" {
		return false
	}
	_, err := hex.DecodeString(s[2:])
	return err == nil
}

func (e *ethereumChain) tokenByAddr(a ethAddr) *ethToken {
	for i := range e.tokens {
		if e.tokens[i].addr == a {
			return &e.tokens[i]
		}
	}
	return nil
}

func (e *ethereumChain) tokenBySymbol(s string) *ethToken {
	for i := range e.tokens {
		if e.tokens[i].Symbol == s {
			return &e.tokens[i]
		}
	}
	return nil
}

// ------------------------------------------------------------------ reads --

// latest and atHash are eth_call's block parameter: the newest block, or one block by its hash (EIP-1898), so that
// reads meant for one block never land on another.
const latest = "latest"

func atHash(h [32]byte) map[string]string { return map[string]string{"blockHash": hex0x(h[:])} }

// call is an eth_call's answer.
func (e *ethereumChain) call(ctx context.Context, to ethAddr, data []byte, at any) ([]byte, error) {
	var res string
	if err := e.c.Call(ctx, "eth_call", []any{map[string]string{"to": to.String(), "data": hex0x(data)}, at}, &res); err != nil {
		return nil, err
	}
	return parseData(res)
}

// word is an eth_call's first word.
func (e *ethereumChain) word(ctx context.Context, to ethAddr, data []byte, at any) ([32]byte, error) {
	b, err := e.call(ctx, to, data, at)
	if err != nil {
		return [32]byte{}, err
	}
	return wordAt(b, 0)
}

func (e *ethereumChain) u64(ctx context.Context, to ethAddr, sel [4]byte, args ...[32]byte) (uint64, error) {
	w, err := e.word(ctx, to, calldata(sel, args...), latest)
	if err != nil {
		return 0, err
	}
	return u64Of(w)
}

// blockBy is a block by tag ("latest", "finalized") or number; false when the provider holds none.
func (e *ethereumChain) blockBy(ctx context.Context, tag string) (ethBlock, bool, error) {
	var raw json.RawMessage
	if err := e.c.Call(ctx, "eth_getBlockByNumber", []any{tag, false}, &raw); err != nil {
		return ethBlock{}, false, err
	}
	if isNull(raw) {
		return ethBlock{}, false, nil
	}
	var j ethBlockJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return ethBlock{}, false, fmt.Errorf("eth_getBlockByNumber: %v", err)
	}
	b, err := j.parse()
	if err != nil {
		return b, false, fmt.Errorf("eth_getBlockByNumber %s: %v", tag, err)
	}
	return b, true, nil
}

// tagged is a block by tag, an error when the provider names none.
func (e *ethereumChain) tagged(ctx context.Context, tag string) (ethBlock, error) {
	b, ok, err := e.blockBy(ctx, tag)
	if err == nil && !ok {
		err = fmt.Errorf("the provider names no %s block", tag)
	}
	return b, err
}

// canonical is the block the provider holds at a number now; one at or below fin (a finalized number) is read once.
func (e *ethereumChain) canonical(ctx context.Context, n, fin uint64) (ethBlock, error) {
	e.mu.Lock()
	b, ok := e.blocks[n]
	e.mu.Unlock()
	if ok {
		return b, nil
	}
	b, found, err := e.blockBy(ctx, qty(n))
	if err != nil {
		return b, err
	}
	if !found || b.Number != n {
		return b, fmt.Errorf("the provider holds no block %d", n)
	}
	if n <= fin {
		e.mu.Lock()
		if len(e.blocks) > 2000 {
			e.blocks = map[uint64]ethBlock{}
		}
		e.blocks[n] = b
		e.mu.Unlock()
	}
	return b, nil
}

// forget drops a block read before: the provider contradicted it (a log or a receipt names another hash at its
// number), so neither is trusted until it agrees with itself.
func (e *ethereumChain) forget(n uint64) {
	e.mu.Lock()
	delete(e.blocks, n)
	e.mu.Unlock()
}

// checkChain asks the provider's chain id once: a provider of another chain is refused, never read.
func (e *ethereumChain) checkChain(ctx context.Context) error {
	e.mu.Lock()
	ok := e.chainOK
	e.mu.Unlock()
	if ok {
		return nil
	}
	var s string
	if err := e.c.Call(ctx, "eth_chainId", nil, &s); err != nil {
		return err
	}
	id, err := parseQty(s)
	if err != nil {
		return fmt.Errorf("eth_chainId: %v", err)
	}
	if id != e.cfg.ChainID {
		return fmt.Errorf("the provider serves chain %d, not %s's %d", id, e.cfg.Name, e.cfg.ChainID)
	}
	e.mu.Lock()
	e.chainOK = true
	e.mu.Unlock()
	return nil
}

// logs are our contracts' logs of the given events in blocks from … to.
func (e *ethereumChain) logs(ctx context.Context, from, to uint64, addrs []ethAddr, topics [][][32]byte) ([]ethLog, error) {
	as := make([]string, len(addrs))
	for i, a := range addrs {
		as[i] = a.String()
	}
	ts := make([][]string, len(topics))
	for i, alts := range topics {
		for _, t := range alts {
			ts[i] = append(ts[i], hex0x(t[:]))
		}
	}
	var res []ethLogJSON
	if err := e.c.Call(ctx, "eth_getLogs", []any{map[string]any{"fromBlock": qty(from), "toBlock": qty(to), "address": as, "topics": ts}}, &res); err != nil {
		return nil, err
	}
	out := make([]ethLog, 0, len(res))
	for _, j := range res {
		l, err := j.parse()
		if err != nil {
			return nil, fmt.Errorf("eth_getLogs: %v", err)
		}
		out = append(out, l)
	}
	return out, nil
}

// ------------------------------------------------------------- the snapshot --

// keysAndFlags reads an object's six seats and their flags (the gate's and HlAdmin's keys() and flags()), and the
// standing hold's start (hold(): zero when none stands now).
func (e *ethereumChain) keysAndFlags(ctx context.Context, obj ethAddr) (*GuardiansJSON, *FlagJSON, error) {
	kb, err := e.call(ctx, obj, calldata(selKeys), latest)
	if err != nil {
		return nil, nil, err
	}
	fb, err := e.call(ctx, obj, calldata(selFlags), latest)
	if err != nil {
		return nil, nil, err
	}
	hb, err := e.call(ctx, obj, calldata(selHold), latest)
	if err != nil {
		return nil, nil, err
	}
	g := &GuardiansJSON{}
	for i := uint64(0); i < 6; i++ {
		w, err := wordAt(kb, 32*i)
		if err != nil {
			return nil, nil, fmt.Errorf("keys(): %v", err)
		}
		a, err := addrWord(w)
		if err != nil {
			return nil, nil, fmt.Errorf("keys(): %v", err)
		}
		g.Keys = append(g.Keys, a.String())
		f, err := flagAt(fb, 64*i)
		if err != nil {
			return nil, nil, fmt.Errorf("flags(): %v", err)
		}
		g.Flags = append(g.Flags, f)
	}
	hold, err := flagAt(hb, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("hold(): %v", err)
	}
	g.HoldSince, g.Hold = hold, hold != nil
	return g, hold, nil
}

// flagAt reads a Flag (uint64 blockNumber, uint64 time): nil when its block is zero (no flag).
func flagAt(b []byte, off uint64) (*FlagJSON, error) {
	bw, err := wordAt(b, off)
	if err != nil {
		return nil, err
	}
	tw, err := wordAt(b, off+32)
	if err != nil {
		return nil, err
	}
	block, err := u64Of(bw)
	if err != nil {
		return nil, err
	}
	t, err := u64Of(tw)
	if err != nil || t > 1<<62 {
		return nil, errors.New("a flag's time out of range")
	}
	if block == 0 {
		return nil, nil
	}
	return &FlagJSON{Slot: block, Time: int64(t)}, nil
}

// Snapshot reads the Ethereum side: the supplies at the finalized block (the cut's first step, §5.9), each call
// pinned to that block's hash; at the newest block the client, the gate's flags and limits, and HlAdmin's flags and
// pending decisions. The parts are read side by side (a public provider's latency is the cost); a part that cannot
// be read is named in Errors, in a fixed order, and the rest is served.
func (e *ethereumChain) Snapshot(ctx context.Context, now time.Time) *OtherSnapshot {
	v := &OtherSnapshot{At: now.Unix(), Supplies: map[string]int64{}, Limits: map[string]LimitJSON{}}
	if err := e.checkChain(ctx); err != nil {
		v.Errors = append(v.Errors, "the provider: "+agent.Redact(err.Error()))
		return v
	}
	const parts = 5
	var errs [parts][]string
	fail := func(i int, what string, err error) { errs[i] = append(errs[i], what+": "+agent.Redact(err.Error())) }
	var wg sync.WaitGroup
	run := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	supplies := map[string]int64{}
	limits := map[string]LimitJSON{}
	run(func() {
		fin, err := e.tagged(ctx, "finalized")
		if err != nil {
			fail(0, "the finalized block", err)
			return
		}
		v.Slot = fin.Number
		for _, t := range e.tokens {
			w, err := e.word(ctx, t.addr, calldata(selTotalSupply), atHash(fin.Hash))
			n := bigWord(w)
			if err == nil && (!n.IsInt64() || n.Int64() > 1<<62) {
				err = fmt.Errorf("%s is out of range", n)
			}
			if err != nil {
				fail(0, t.Symbol+" supply", err)
				continue
			}
			supplies[t.Symbol] = n.Int64()
		}
		if head, err := e.tagged(ctx, latest); err == nil {
			v.Head = head.Number
		}
	})
	run(func() { // the client: its trusted height and header, the quarantine, the set it checks against
		cl, err := e.clientNow(ctx)
		if err != nil {
			fail(1, "the light client", err)
			return
		}
		v.Client = cl
	})
	run(func() { // the gate: its seats' flags and a hold
		g, _, err := e.keysAndFlags(ctx, e.gate)
		if err != nil {
			fail(2, "the gate's flags", err)
			return
		}
		v.Pausable = g
	})
	run(func() { // each token's mint limits, with the bucket's level now
		if err := e.limitsNow(ctx, now, limits); err != nil {
			fail(3, "the gate's limits", err)
		}
	})
	run(func() { // HlAdmin: its seats' flags, a hold, the pending decisions and when each matures
		g, hold, err := e.keysAndFlags(ctx, e.admin)
		if err != nil {
			fail(4, "HlAdmin", err)
			return
		}
		v.Admin = g
		if err := e.pendingNow(ctx, g, hold, v); err != nil {
			fail(4, "HlAdmin's pending decisions", err)
		}
	})
	wg.Wait()
	v.Supplies, v.Limits = supplies, limits
	for _, es := range errs {
		v.Errors = append(v.Errors, es...)
	}
	return v
}

// factor is a contract's compiled clock factor (clockFactor() is pure: read once), 1 when it answers 0.
func (e *ethereumChain) factor(ctx context.Context, a ethAddr) (uint64, error) {
	e.mu.Lock()
	f, ok := e.factors[a]
	e.mu.Unlock()
	if ok {
		return f, nil
	}
	f, err := e.u64(ctx, a, selClockFactor)
	if err != nil {
		return 0, err
	}
	if f == 0 || f > 1<<20 {
		return 0, fmt.Errorf("a clock factor of %d", f)
	}
	e.mu.Lock()
	e.factors[a] = f
	e.mu.Unlock()
	return f, nil
}

func (e *ethereumChain) clientNow(ctx context.Context) (*ClientJSON, error) {
	hw, err := e.word(ctx, e.client, calldata(selTrustedHeight), latest)
	if err != nil {
		return nil, err
	}
	height, err := i64Of(hw)
	if err != nil || height < 0 {
		return nil, fmt.Errorf("trustedHeight(): %v", err)
	}
	q, err := e.u64(ctx, e.client, selQuarantineEnd)
	if err != nil {
		return nil, err
	}
	tt, err := e.u64(ctx, e.client, selTrustedTime)
	if err != nil {
		return nil, err
	}
	factor, err := e.factor(ctx, e.client)
	if err != nil {
		return nil, err
	}
	hash, err := e.word(ctx, e.client, calldata(selTrustedHash), latest)
	if err != nil {
		return nil, err
	}
	if q > 1<<62 || tt > 1<<62 {
		return nil, errors.New("the client's figures are out of range")
	}
	cl := &ClientJSON{TrustedHeight: uint64(height), QuarantineEnd: int64(q), TrustedTime: int64(tt), ClockFactor: int64(factor), Powers: []int64{}}
	e.mu.Lock()
	if h, ok := e.headers[hash]; ok {
		cl.LastAccepted = h.Time // the block time the activity read for the trusted header's update
	}
	cl.Powers = append(cl.Powers, e.set...)
	e.mu.Unlock()
	cl.Validators = len(cl.Powers)
	return cl, nil
}

// limitsNow reads each token's mint limits on the gate into out, checking once that the gate's token i is the
// config's (a gate that limits another token would show its limits as ours).
func (e *ethereumChain) limitsNow(ctx context.Context, now time.Time, out map[string]LimitJSON) error {
	e.mu.Lock()
	checked := e.gateOK
	e.mu.Unlock()
	if !checked {
		for _, t := range e.tokens {
			w, err := e.word(ctx, e.gate, calldata(selToken, u64Word(uint64(t.index))), latest)
			if err != nil {
				return err
			}
			a, err := addrWord(w)
			if err != nil {
				return fmt.Errorf("token(%d): %v", t.index, err)
			}
			if a != t.addr {
				return fmt.Errorf("the gate's token %d is %s; the config's %s is %s", t.index, a, t.Symbol, t.addr)
			}
		}
		e.mu.Lock()
		e.gateOK = true
		e.mu.Unlock()
	}
	factor, err := e.factor(ctx, e.gate)
	if err != nil {
		return err
	}
	for _, t := range e.tokens {
		b, err := e.call(ctx, e.gate, calldata(selLimits, u64Word(uint64(t.index))), latest)
		if err != nil {
			return err
		}
		var f [4]uint64
		for i := range f {
			w, err := wordAt(b, 32*uint64(i))
			if err == nil {
				f[i], err = u64Of(w)
			}
			if err != nil {
				return fmt.Errorf("limits(%d): %v", t.index, err)
			}
		}
		l := sol.MintLimit{MaxPerMessage: f[0], DayCap: f[1], Used: f[2], UsedAt: int64(min(f[3], 1<<62))}
		out[t.Symbol] = LimitJSON{MaxPerMessage: l.MaxPerMessage, DayCap: l.DayCap, Used: l.Level(now.Unix(), sol.Day/int64(factor))}
	}
	return nil
}

// pendingMax bounds how far back from lastSeq the pending decisions are looked for.
const pendingMax = 64

// pendingNow reads HlAdmin's last number, its setup flag and its pending decisions — from the last number down,
// until as many records as pending() counts are found — with when each matures and what defers it.
func (e *ethereumChain) pendingNow(ctx context.Context, g *GuardiansJSON, hold *FlagJSON, v *OtherSnapshot) error {
	last, err := e.u64(ctx, e.admin, selLastSeq)
	if err != nil {
		return err
	}
	done, err := e.word(ctx, e.admin, calldata(selSetupDone), latest)
	if err != nil {
		return err
	}
	setup, err := boolWord(done)
	if err != nil {
		return err
	}
	v.LastSeq, v.SetupDone = last, setup
	count, err := e.u64(ctx, e.admin, selPending)
	if err != nil {
		return err
	}
	factor, err := e.factor(ctx, e.admin)
	if err != nil {
		return err
	}
	anyFlag := false
	for _, f := range g.Flags {
		anyFlag = anyFlag || f != nil
	}
	for n := last; n >= 1 && n+pendingMax > last && uint64(len(v.Pending)) < count; n-- {
		b, err := e.call(ctx, e.admin, calldata(selDecision, u64Word(n)), latest)
		if err != nil {
			return err
		}
		d, err := decodeDecision(b)
		if err != nil {
			return fmt.Errorf("decision(%d): %v", n, err)
		}
		if d.Number == 0 {
			continue // ended, or never recorded
		}
		p := PendingJSON{Number: d.Number, Kind: d.Kind, KindName: ethKindName(d.Kind), Text: describeEthParams(d.Kind, d.Params), Delivered: d.DeliveredTime}
		p.MaturesAt = d.DeliveredTime + ethDelay(d.Kind)/int64(factor)
		switch {
		case hold != nil && d.Kind == 5:
			// a replacement under a hold lands on the hold's 21st day at the earliest (HlAdmin reads every hold that
			// overlapped its pending life; the standing one is the one shown here)
			p.MaturesAt = max(p.MaturesAt, hold.Time+21*sol.Day/int64(factor))
			p.Deferred = "two guardians hold HlAdmin: it lands on the hold's 21st day at the earliest"
		case hold != nil && ethSevenDay(d.Kind):
			p.Deferred = "two guardians hold HlAdmin: it waits until the hold ends"
		case anyFlag && ethSevenDay(d.Kind) && d.Kind != 5:
			p.Deferred = "a flag stands on HlAdmin: it waits for the flag to clear"
		}
		v.Pending = append(v.Pending, p)
	}
	return nil
}

// ethDecision is HlAdmin's decision(n).
type ethDecision struct {
	Number, DeliveredBlock uint64
	Kind                   byte
	DeliveredTime          int64
	Deliverer              ethAddr
	Params                 []byte
}

// decodeDecision reads decision(n)'s answer: one tuple (uint64 number, uint8 kind, uint64 deliveredBlock, uint64
// deliveredTime, address deliverer, bytes params), dynamic for its params, so the answer opens with its offset.
func decodeDecision(b []byte) (ethDecision, error) {
	var d ethDecision
	ow, err := wordAt(b, 0)
	if err != nil {
		return d, err
	}
	base, err := u64Of(ow)
	if err != nil {
		return d, err
	}
	var f [4]uint64
	for i, at := range []uint64{0, 32, 64, 96} {
		w, err := wordAt(b, base+at)
		if err == nil {
			f[i], err = u64Of(w)
		}
		if err != nil {
			return d, err
		}
	}
	if f[1] > 255 || f[3] > 1<<62 {
		return d, errors.New("a kind or a time out of range")
	}
	aw, err := wordAt(b, base+128)
	if err != nil {
		return d, err
	}
	if d.Deliverer, err = addrWord(aw); err != nil {
		return d, err
	}
	if d.Params, err = bytesAt(b, base, 160); err != nil {
		return d, err
	}
	d.Number, d.Kind, d.DeliveredBlock, d.DeliveredTime = f[0], byte(f[1]), f[2], int64(f[3])
	return d, nil
}

// ------------------------------------------------------------- the burns --

// burnOf reads one log as a burn toward gno.land, or false. A burn is a Dispatch our mailbox emitted whose sender
// is one of our token routers, toward gno.land's domain, to that token's route realm — and whose message says the
// same (ER5: anyone may dispatch on the mailbox toward any recipient; a stranger's dispatch is no burn).
func (e *ethereumChain) burnOf(l ethLog) (Burn, bool) {
	if l.Removed || l.Address != e.mailbox || len(l.Topics) != 4 || l.Topics[0] != topicDispatch {
		return Burn{}, false
	}
	sender, ok := addrOf32(l.Topics[1])
	tok := e.tokenByAddr(sender)
	if !ok || tok == nil || l.Topics[2] != u64Word(uint64(e.gnoDomain)) || l.Topics[3] != tok.route {
		return Burn{}, false
	}
	raw, err := bytesAt(l.Data, 0, 0)
	if err != nil {
		return Burn{}, false
	}
	m, err := hl.ParseMessage(raw)
	if err != nil || m.Version != hl.Version || m.Origin != e.cfg.Domain || m.Destination != e.gnoDomain || m.Sender != l.Topics[1] || m.Recipient != tok.route {
		return Burn{}, false
	}
	id := hl.ID(raw)
	b := Burn{ID: hex.EncodeToString(id[:]), Token: tok.Symbol, Nonce: m.Nonce, Slot: l.Block, Sig: l.Tx}
	if rcpt, amt, _, err := hl.ParseTokenMessage(m.Body); err == nil && amt <= 1<<63-1 {
		b.Amount = int64(amt)
		if to, err := hl.Bytes32ToGnoAddress(rcpt); err == nil {
			b.To = to
		} else {
			b.BadTo = true
		}
	}
	return b, true
}

// Burn reads the burns toward gno.land one transaction made, from its receipt at the newest block: the
// transaction's block must still be the one the provider holds at its number (else a reorganization took it
// back, and it is read again at the next look); each burn says the finalized block, before which the signers sign
// nothing.
func (e *ethereumChain) Burn(ctx context.Context, ref string) ([]Burn, error) {
	if !isTxHash(ref) {
		return nil, errors.New("not an Ethereum transaction hash")
	}
	h := strings.ToLower(ref)
	var raw json.RawMessage
	if err := e.c.Call(ctx, "eth_getTransactionReceipt", []any{h}, &raw); err != nil {
		return nil, err
	}
	if isNull(raw) {
		return nil, fmt.Errorf("%w: no transaction %s in a block on %s yet (it may be pending, dropped or never sent)", errUnknownTransfer, h, e.cfg.Name)
	}
	var r ethReceiptJSON
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("eth_getTransactionReceipt: %v", err)
	}
	n, err := parseQty(r.BlockNumber)
	if err != nil {
		return nil, fmt.Errorf("the receipt's block: %v", err)
	}
	bh, err := parseHash(r.BlockHash)
	if err != nil {
		return nil, fmt.Errorf("the receipt's block: %v", err)
	}
	if r.Status != "0x1" {
		return nil, fmt.Errorf("the transaction failed on %s (it reverted): it burned nothing", e.cfg.Name)
	}
	fin, err := e.tagged(ctx, "finalized")
	if err != nil {
		return nil, err
	}
	block, err := e.canonical(ctx, n, fin.Number)
	if err != nil {
		return nil, err
	}
	if block.Hash != bh {
		e.forget(n)
		return nil, txAnswer{fmt.Sprintf("%v: the block %d that held transaction %s was taken back on %s (a reorganization); it is read again at the next look", errUnknownTransfer, n, h, e.cfg.Name)}
	}
	var out []Burn
	for _, j := range r.Logs {
		l, err := j.parse()
		if err != nil || l.Block != n || l.BlockHash != bh || l.Tx != h {
			continue
		}
		if b, ok := e.burnOf(l); ok {
			b.Time, b.Finalized = block.Time, fin.Number
			out = append(out, b)
		}
	}
	return out, nil
}

// DeliveryOf is the transaction that delivered one of gno.land's messages here, from the mailbox's own record at
// the newest block (processedAt: the block it was processed in, 0 when never) and that block's ProcessId log. A log
// whose block hash is not the one the provider holds at that number names a transaction taken back: the delivery
// stands by the record, its transaction shown once the provider agrees with itself.
func (e *ethereumChain) DeliveryOf(ctx context.Context, id [32]byte) (*TxRef, error) {
	w, err := e.word(ctx, e.mailbox, calldata(selProcessedAt, id), latest)
	if err != nil {
		return nil, err
	}
	n, err := u64Of(w)
	if err != nil || n == 0 {
		return nil, err
	}
	ref := &TxRef{Slot: n, Link: e.AccountLink(e.mailbox.String())}
	logs, err := e.logs(ctx, n, n, []ethAddr{e.mailbox}, [][][32]byte{{topicProcessID}, {id}})
	if err != nil {
		return nil, err
	}
	fin, err := e.tagged(ctx, "finalized")
	if err != nil {
		return ref, nil
	}
	block, err := e.canonical(ctx, n, fin.Number)
	if err != nil {
		return ref, nil
	}
	ref.Time = block.Time
	for _, l := range logs {
		if !l.Removed && l.Block == n && l.BlockHash == block.Hash && len(l.Topics) == 2 && l.Topics[1] == id {
			ref.Sig, ref.Link = l.Tx, e.TxLink(l.Tx)
			return ref, nil
		}
	}
	if len(logs) > 0 {
		e.forget(n)
	}
	return ref, nil
}

// --------------------------------------------------------------- the builder --

// quote is one entry of quoteTransferRemote's answer.
type quote struct {
	Token  ethAddr
	Amount *big.Int
}

// decodeQuotes reads quoteTransferRemote's Quote[] (address token, uint256 amount).
func decodeQuotes(b []byte) ([]quote, error) {
	start, n, err := dynAt(b, 0, 0)
	if err != nil {
		return nil, err
	}
	if n > 16 {
		return nil, errors.New("more than sixteen quotes")
	}
	var out []quote
	for i := uint64(0); i < n; i++ {
		tw, err := wordAt(b, start+64*i)
		if err != nil {
			return nil, err
		}
		a, err := addrWord(tw)
		if err != nil {
			return nil, err
		}
		aw, err := wordAt(b, start+64*i+32)
		if err != nil {
			return nil, err
		}
		out = append(out, quote{Token: a, Amount: bigWord(aw)})
	}
	return out, nil
}

// BurnTx is the call the wallet sends: the token's transferRemote(gno.land's domain, the recipient, the amount),
// with the native value the token's own quote asks (quoteTransferRemote, @hyperlane-xyz/core 12.2.0: index 0 the
// dispatch's fee in ETH — the mailbox's protocol fee — index 1 the tokens it burns, index 2 an external fee). The
// value must be exact: the mailbox's default hook refuses any excess, and too little fails the protocol fee. A
// token that asks more than the amount in tokens, or its gas in tokens, is not built for. The sender must hold the
// amount. Nothing here signs: the wallet signs and sends through its own provider.
func (e *ethereumChain) BurnTx(ctx context.Context, t TokenConfig, sender, recipient string, amount uint64) (*BurnTx, error) {
	from, err := parseEthWire(sender) // the wallet's own account, as it answers it (often in lower case): no one typed it
	if err != nil {
		return nil, fmt.Errorf("the sender is not an Ethereum address: %v", err)
	}
	to := strings.TrimSpace(recipient)
	rcpt, err := hl.GnoAddressToBytes32(to)
	if err != nil {
		return nil, errors.New("the recipient is not a gno.land address (g1…)")
	}
	if realm := realmOfAddress(e.realms, to); realm != "" {
		return nil, fmt.Errorf("%s is the address of %s, a bridge realm: a burn to it is swept on gno.land and paid only by a governance decision; name a wallet", to, realm)
	}
	if amount == 0 || amount > 1<<63-1 {
		return nil, errors.New("the amount must be above zero")
	}
	tok := e.tokenBySymbol(t.Symbol)
	if tok == nil {
		return nil, errors.New("unknown token")
	}
	if err := e.checkChain(ctx); err != nil {
		return nil, unavailable{err}
	}
	bw, err := e.word(ctx, tok.addr, calldata(selBalanceOf, from.word()), latest)
	if err != nil {
		return nil, unavailable{err}
	}
	if bal := bigWord(bw); bal.Cmp(new(big.Int).SetUint64(amount)) < 0 {
		return nil, fmt.Errorf("the wallet %s holds %s %s on %s, less than %s", from, units(bal), t.Symbol, e.cfg.Name, units(new(big.Int).SetUint64(amount)))
	}
	qb, err := e.call(ctx, tok.addr, calldata(selQuoteTransfer, u64Word(uint64(e.gnoDomain)), rcpt, u64Word(amount)), latest)
	if err != nil {
		return nil, unavailable{err}
	}
	quotes, err := decodeQuotes(qb)
	if err != nil || len(quotes) < 2 {
		return nil, unavailable{fmt.Errorf("the token's quote does not read: %v", err)}
	}
	if quotes[0].Token != (ethAddr{}) {
		return nil, errors.New("the token asks its dispatch fee in tokens (a fee hook): the site builds no such transfer")
	}
	if quotes[1].Token != tok.addr || quotes[1].Amount.Cmp(new(big.Int).SetUint64(amount)) != 0 {
		return nil, errors.New("the token charges more than the amount in tokens (a fee recipient): the site builds no such transfer")
	}
	for _, q := range quotes[2:] {
		if q.Amount.Sign() != 0 {
			return nil, errors.New("the token asks an external fee: the site builds no such transfer")
		}
	}
	data := calldata(selTransfer, u64Word(uint64(e.gnoDomain)), rcpt, u64Word(amount))
	return &BurnTx{From: from.String(), To: tok.addr.String(), Data: hex0x(data), Value: "0x" + quotes[0].Amount.Text(16), ChainID: qty(e.cfg.ChainID)}, nil
}

// units shows base units (six decimals) as a decimal amount.
func units(v *big.Int) string {
	q, r := new(big.Int).QuoRem(v, big.NewInt(1_000_000), new(big.Int))
	if r.Sign() == 0 {
		return q.String()
	}
	return q.String() + "." + strings.TrimRight(fmt.Sprintf("%06d", r.Int64()), "0")
}

// ------------------------------------------------------------- decisions --

// ethKindName is a decision kind's name on Ethereum (eth/bridge/PLAN.md §13's table: 4, 10 and 11 reserved).
func ethKindName(kind byte) string {
	names := [...]string{"", "cancel", "unpause", "set token ISM", "reserved", "set guardians and watch keys", "enroll router", "set protocol fee", "upgrade", "transfer to successor", "reserved", "reserved", "set mint limits"}
	if int(kind) < len(names) && kind != 0 {
		return names[kind]
	}
	return "unknown"
}

// ethSevenDay is whether a kind is in the 7-day group (3–9 and 12, §13).
func ethSevenDay(kind byte) bool { return kind >= 3 && kind <= 9 || kind == 12 }

// ethDelay is a kind's clock after delivery at factor 1: a cancel at once, an unpause after a day, the rest a week.
func ethDelay(kind byte) int64 {
	switch kind {
	case 1:
		return 0
	case 2:
		return sol.Day
	}
	return 7 * sol.Day
}

// beBytes reads big-endian fields in order.
type beBytes struct {
	b   []byte
	pos int
	err error
}

func (r *beBytes) take(n int) []byte {
	if r.err != nil || r.pos+n > len(r.b) {
		r.err = errors.New("short")
		return make([]byte, n)
	}
	out := r.b[r.pos : r.pos+n]
	r.pos += n
	return out
}

func (r *beBytes) u8() byte    { return r.take(1)[0] }
func (r *beBytes) u32() uint32 { return binary.BigEndian.Uint32(r.take(4)) }
func (r *beBytes) u64() uint64 { return binary.BigEndian.Uint64(r.take(8)) }
func (r *beBytes) w32() (w [32]byte) {
	copy(w[:], r.take(32))
	return w
}

// describeEthParams says what a decision's parameters mean (§13's encodings: every integer big-endian, every
// address 32 bytes left-padded — a non-zero top twelve bytes is no address and spends the decision).
func describeEthParams(kind byte, params []byte) string {
	r := &beBytes{b: params}
	addr := func() string {
		w := r.w32()
		if a, ok := addrOf32(w); ok {
			if a == (ethAddr{}) {
				return "none"
			}
			return a.String()
		}
		return hex0x(w[:]) + " (no address: spent)"
	}
	tok := func(i byte) string {
		switch i {
		case 0:
			return "GNOT"
		case 1:
			return "BUBBLE"
		}
		return fmt.Sprintf("token %d", i)
	}
	var s string
	switch kind {
	case 1:
		salt := r.u64()
		switch r.u8() {
		case 0:
			n := int(r.u8())
			var nums []string
			for i := 0; i < n && r.err == nil; i++ {
				r.u8()
				nums = append(nums, fmt.Sprintf("#%d", r.u64()))
			}
			s = "cancel " + strings.Join(nums, ", ")
		case 1:
			r.u8()
			from, to := r.u64(), r.u64()
			s = fmt.Sprintf("cancel #%d to #%d", from, to)
		default:
			r.err = errors.New("form")
		}
		s += fmt.Sprintf(" (salt 0x%016x)", salt)
	case 2:
		if r.u8() == 1 {
			s = "unpause HlAdmin"
		} else {
			s = "unpause the gate"
		}
	case 3:
		t := r.u8()
		s = fmt.Sprintf("set %s's ISM to %s", tok(t), addr())
	case 4, 10, 11:
		return fmt.Sprintf("reserved (kind %d): spent", kind)
	case 5:
		n := int(r.u8())
		var parts []string
		for i := 0; i < n && r.err == nil; i++ {
			seat := r.u8()
			parts = append(parts, fmt.Sprintf("seat %d → %s", seat, addr()))
		}
		s = "set guardians and watch keys: " + strings.Join(parts, ", ")
	case 6:
		t, d, router := r.u8(), r.u32(), r.w32()
		s = fmt.Sprintf("enroll %s's router for domain %d: %s", tok(t), d, hex0x(router[:]))
	case 7:
		fee := r.u64()
		s = fmt.Sprintf("set the protocol fee to %s ETH, beneficiary %s", eth18(fee), addr())
	case 8:
		proxy, impl, h := addr(), addr(), r.w32()
		s = fmt.Sprintf("upgrade %s to %s (code hash %x…)", proxy, impl, h[:6])
	case 9:
		succ, h := addr(), r.w32()
		s = fmt.Sprintf("hand every authority to %s (code hash %x…)", succ, h[:6])
	case 12:
		t, mx, day := r.u8(), r.u64(), r.u64()
		s = fmt.Sprintf("set %s's mint limits: %s a transfer, %s a day", tok(t), units(new(big.Int).SetUint64(mx)), units(new(big.Int).SetUint64(day)))
	default:
		return fmt.Sprintf("unknown kind %d: recorded and spent", kind)
	}
	if r.err != nil || r.pos != len(params) {
		return ethKindName(kind) + ": the parameters do not parse, spent at execution"
	}
	return s
}

// eth18 shows wei as ETH.
func eth18(wei uint64) string {
	q, r := wei/1e18, wei%1e18
	if r == 0 {
		return fmt.Sprint(q)
	}
	return fmt.Sprintf("%d.%s", q, strings.TrimRight(fmt.Sprintf("%018d", r), "0"))
}

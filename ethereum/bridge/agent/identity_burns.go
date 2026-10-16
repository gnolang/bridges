package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The Ethereum side of §5.9's ordered cut, as the signers, the monitors and hlwatch take it (eth/bridge/PLAN.md §5.6):
// the `finalized` block F two providers agree on (number and hash), each token's `totalSupply` at F and the light
// client's state at F — every read an eth_call pinned to F's hash (EIP-1898, requireCanonical) through both providers,
// compared byte for byte — and the burn index: every burn toward gno.land at or below F, by message id, read with
// lane E-M3's scanner (ethx.ScanBurns: a token router's own `Dispatch` to gno.land's domain and a realm of the token's
// line — its route realm, or a successor a migration named — beside that token's `SentTransferRemote` of the same
// recipient and amount in the same transaction; a stranger's dispatch is ignored with why, never counted, since
// `Mailbox.dispatch` is open to anyone — the red team's ER5). The line is read from gno.land before every scan, and a
// scan waits while a token's line cannot be read: a burn addressed to a successor the scan did not know would be
// passed over for good.
//
// A provider that cannot serve F's state is no verdict, never a zero: public endpoints that keep only the last
// ~100–128 blocks of state (publicnode, 1rpc, blockrazor; notes/research-ethereum.md §5) fail an eth_call at F, which
// trails the head by 64–95 blocks, so a signer's and a monitor's providers must serve full-history state and logs
// (drpc, mevblocker, Tenderly, blastapi and nodies answered eth_call at any height when measured). Logs differ by
// provider (the assumptions audit's E3-AS4, measured 2026-10-14): drpc's free plan answers eth_getLogs over ~100
// blocks a call at most (set its logRange to 100; a wider range is halved until it answers, ethx.LogsRange), and its
// documented monthly free quota is below a signer's steady ≈17M calls a month (a paid plan for a signer); publicnode
// refuses logs 10,000 blocks back ("Archive requests require a personal token"), so a fresh install's scan from
// eth.deployBlock cannot use it once the deployment is days old. An empty log answer is one provider's word and never
// a verdict alone: the pair compares (a provider has answered a busy window with no logs and no error).

// ethSnapshot is the Ethereum side of one cut.
type ethSnapshot struct {
	Block    ethx.Block          // the agreed finalized block F
	At       time.Time           // the wall clock when the snapshot was taken (the gno block read after it must be later)
	Supplies map[string]int64    // by route symbol: base units, saturated (supplyUnits)
	Raw      map[string]*big.Int // the supplies as read
	Client   *ethx.ClientState   // the client at F; nil when the config names no client
	Pin      ethx.At             // F by hash, for every later read of the cut
	Tokens   map[string]bool     // the configured route symbols with a token
}

// pairOf dials a machine's two providers (the first two of eth.providers): a signer and a monitor read Ethereum through
// two that must agree (§5.6). The same provider twice is refused unless the config says a local run (allowHTTP).
func pairOf(ctx context.Context, cfg *Config) (ethx.Pair, error) {
	if len(cfg.Eth.Providers) < 2 {
		return ethx.Pair{}, fmt.Errorf("two Ethereum providers that must agree are needed (eth.providers lists %d): a signer and a monitor read Ethereum through two (§5.6)", len(cfg.Eth.Providers))
	}
	a, err := ethx.Dial(ctx, cfg.Eth.Providers[0])
	if err != nil {
		return ethx.Pair{}, err
	}
	b, err := ethx.Dial(ctx, cfg.Eth.Providers[1])
	if err != nil {
		a.Close()
		return ethx.Pair{}, err
	}
	p, err := ethx.NewPair(a, b, cfg.AllowHTTP)
	if err != nil {
		a.Close()
		b.Close()
		return ethx.Pair{}, err
	}
	return p, nil
}

// burnRoutes are the configured tokens as the burn scan matches them: the HypERC20, its route realm's 32 bytes and
// the other realms of its line of migrations — the line read from gno.land (lines, by symbol: nil for a token with no
// migration open) and the config's successor and predecessor — each a recipient a burn of the token may name: a kind
// 6 names a successor as the token's router, and every burn after it is addressed there (hlmailbox hands a message
// addressed to any realm of the line to the line's live successor; the Solana signers and relayer follow the line).
func burnRoutes(cfg *Config, lines map[string]*gnoside.Migration) []ethx.BurnRoute {
	var out []ethx.BurnRoute
	for _, r := range cfg.Routes {
		if r.Token == "" {
			continue
		}
		br := ethx.BurnRoute{Symbol: r.Symbol, Token: r.TokenAddress(), Realm: gnoside.RealmKey(r.Gno)}
		var paths []string
		if mig := lines[r.Symbol]; mig != nil {
			paths = mig.Paths()
		}
		paths = append(paths, r.Successor, r.Predecessor)
		seen := map[[32]byte]bool{br.Realm: true}
		for _, p := range paths {
			if p == "" {
				continue
			}
			if k := gnoside.RealmKey(p); !seen[k] {
				seen[k] = true
				br.Line = append(br.Line, k)
			}
		}
		out = append(out, br)
	}
	return out
}

// readLines reads every configured token's line of migrations at a height (nil for a token with none open); a line
// that cannot be read is an error naming the token: the burn scans wait for it.
func readLines(ctx context.Context, g gnoside.Reader, cfg *Config, mb *gnoside.Mailbox, H int64) (map[string]*gnoside.Migration, error) {
	read := gnoside.RouteReaderAt(g, H)
	out := map[string]*gnoside.Migration{}
	for _, r := range cfg.Routes {
		rj, err := gnoside.RouteAt(ctx, g, r.Gno, H)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.Symbol, err)
		}
		mig, err := gnoside.MigrationOf(ctx, read, r.Symbol, r.Gno, r.Predecessor, rj, mb)
		if err != nil {
			return nil, fmt.Errorf("%s's line of migrations: %w", r.Symbol, err)
		}
		if mig != nil {
			out[r.Symbol] = mig
		}
	}
	return out, nil
}

// takeEthSnapshot is the cut's first step: F, then each token's supply at F, then the client's state at F, all
// through the pair. Any failure is no verdict this round (the caller alarms with ethReadText's words).
func takeEthSnapshot(ctx context.Context, p ethx.Pair, cfg *Config, now time.Time) (*ethSnapshot, error) {
	F, err := p.FinalizedWithin(ctx, ethx.FinalizedApart) // two answers more than two epochs apart are no reading (E5-RT1)
	if err != nil {
		return nil, fmt.Errorf("the finalized block: %w", err)
	}
	snap := &ethSnapshot{Block: *F, At: now, Supplies: map[string]int64{}, Raw: map[string]*big.Int{}, Pin: ethx.AtHash(F.Hash), Tokens: map[string]bool{}}
	for _, r := range cfg.Routes {
		if r.Token == "" {
			continue
		}
		s, err := ethx.TotalSupply(ctx, p, r.TokenAddress(), snap.Pin)
		if err != nil {
			return nil, fmt.Errorf("%s's supply at block %d: %w", r.Symbol, F.Number, err)
		}
		if s == nil {
			return nil, fmt.Errorf("%s's supply at block %d: no answer", r.Symbol, F.Number)
		}
		snap.Raw[r.Symbol] = s
		snap.Supplies[r.Symbol] = supplyUnits(s)
		snap.Tokens[r.Symbol] = true
	}
	if cfg.Eth.Client != "" {
		cs, err := ethx.ReadClient(ctx, p, Address(cfg.Eth.Client), snap.Pin)
		if err != nil {
			return nil, fmt.Errorf("the light client at block %d: %w", F.Number, err)
		}
		snap.Client = cs
	}
	return snap, nil
}

// ethReadText says what an Ethereum reading's failure means for a verdict: a provider without the state of F (a
// full-history provider is needed), the two providers disagreeing (no verdict until they agree), or the error.
func ethReadText(err error) string {
	switch {
	case err == nil:
		return ""
	case ethx.IsNoState(err):
		return "a provider keeps no state for the finalized block (" + ethx.Redact(err.Error()) + "): no verdict — a signer's and a monitor's two providers must serve full-history state (§5.6; publicnode, 1rpc and blockrazor keep ~100–128 blocks)"
	case errors.Is(err, ethx.ErrDisagree):
		return "the two providers disagree (" + ethx.Redact(err.Error()) + "): no verdict until they agree"
	}
	return ethx.Redact(err.Error())
}

// burnRec is one burn toward gno.land in the index.
type burnRec struct {
	Symbol   string `json:"symbol"`
	Nonce    uint32 `json:"nonce"`
	Block    uint64 `json:"block"`
	Tx       string `json:"tx"`
	To       string `json:"to"`            // the gno recipient (g1…); "" when the 32 bytes are no gno address
	Bad      bool   `json:"bad,omitempty"` // the recipient is no gno address: the realm sweeps the burn (§13)
	Amount   int64  `json:"amount"`        // base units
	Message  string `json:"message"`       // the message, hex (what a signer signs and serves)
	Credited int64  `json:"credited"`      // the gno height it was credited at; 0 while waiting
	Checked  bool   `json:"checked"`       // the diverted-credit check was made (the monitor)
	Seen     int64  `json:"seen"`          // unix time first indexed
	// DustAt is when a monitor's poll last found it below the fee of the realm that would credit it (unix; 0 never): the
	// relayers leave such dust (E5-GS5), so it waits for nobody — its wait, once a lowered fee makes it a delivery,
	// counts from then, never from Seen (the integration lane I3)
	DustAt int64 `json:"dustAt,omitempty"`
	// mark is when its delivered mark was last read unset (in memory): read again only once hlmailbox's delivered
	// count moved (markSeen)
	mark markSeen
}

// markSeen is when a burn's delivered mark was last read unset: hlmailbox's delivered count read last before it (the
// cut window's second read: the marks are read after the window, E6-GT1). A mark is set only with that count's rise —
// Process and a credit by vote each set one, in the transaction that raises the count, and none is ever unset — so while
// a later reading's count is the same, a mark read unset after it is unset still, and is not read again (the Solana
// machines' E5-SF5 fix, carried over by the integration lane I3: the burns nobody credits — the dust below a route's fee
// the relayers leave, E5-GS5, a stranger's at an Ethereum fee each — were read again on every signer's round and every
// monitor's poll, for good, one render each).
type markSeen struct {
	delivered int
	read      bool
}

// stale says whether the mark is to be read at a reading whose last read of hlmailbox before the marks gave the
// delivered count d.
func (m markSeen) stale(d int) bool { return !m.read || m.delivered != d }

// markRefresh is how often every uncredited mark is read again whatever the count (beside a reading in doubt: a token
// BROKEN, or waiting as a pending break, with a delivery the index cannot place — markDoubt): the count and the marks
// are separate reads, and a node that answers them from two backends a block apart (a load-balanced endpoint, which
// §11.10's own node rules out) can give a mark read unset at a count that already holds its credit — kept, that one
// stale read would leave the credited burn in flight while owed counts it (the Solana review's F1, lane S6).
const markRefresh = 10 * time.Minute

// markDoubt says whether a cut's marks are in doubt, so the next reading reads every uncredited mark again: hlmailbox
// delivered something the index cannot place while a token read BROKEN or a BROKEN reading waits — exactly what one
// stale unset mark of a credited burn reads as (its amount in flight and in owed alike, its delivery unplaced); a burn
// above the index or a vote's credit makes it too, for as long as it stands.
func markDoubt(unplaced int, broken bool, pending int) bool {
	return unplaced > 0 && (broken || pending > 0)
}

// deliveredMarks reads the delivered marks of ids at a reading whose last read of hlmailbox gave the delivered count d:
// the height each delivered one was delivered at (the others absent). One render each — or, for more ids than a page and
// than the done tree has pages (a restart's first reading of every burn its index holds, a pile of dust after a credit
// moved the count), the done tree whole by vm/qeval, a hundred ids a read, an id it does not hold being undelivered; a
// node that answers no vm/qeval, or fails one, is read id by id.
func deliveredMarks(ctx context.Context, g gnoside.Reader, mailbox string, ids []string, d int, H int64) (map[string]int64, error) {
	out := map[string]int64{}
	if len(ids) > gnoside.DonePage && len(ids) > (d+gnoside.DonePage-1)/gnoside.DonePage {
		entries, err := gnoside.DoneIDs(ctx, g, mailbox, d, H)
		if err != nil && !errors.Is(err, gnoside.ErrNoQuery) {
			log.Printf("hlmailbox's done tree did not read (%v): %d delivered marks read one by one", err, len(ids))
		}
		if err == nil {
			want := make(map[string]bool, len(ids))
			for _, id := range ids {
				want[id] = true
			}
			for _, e := range entries {
				if want[e.ID] {
					out[e.ID] = e.Height
				}
			}
			return out, nil
		}
	}
	for _, id := range ids {
		at, err := gnoside.DeliveredAt(ctx, g, mailbox, id, H)
		if err != nil {
			return nil, fmt.Errorf("the delivery of burn %s: %w", short(id), err)
		}
		if at != 0 {
			out[id] = at
		}
	}
	return out, nil
}

// burnIndex is every burn toward gno.land at or below the scan's cursor, by message id (hex), with the scan's place.
type burnIndex struct {
	Cursor   uint64              `json:"cursor"`            // the last block scanned (an agreed finalized block or below); 0 before the first scan
	Burns    map[string]*burnRec `json:"burns"`             //
	IgnoredN int                 `json:"ignoredN"`          // dispatches toward gno.land the scan did not count, ever
	Ignored  []string            `json:"ignored,omitempty"` // the last of them, with why (bounded)
	// Line is the launch line the last scan read (HlAdmin.launchNonce(), ethx.LaunchLine): no message numbered below
	// it is a burn of this deployment (PLAN.md §5.6; M2-NOTES.md Fold F5, F5.1 item 12).
	Line uint32 `json:"line"`
	// DustLines is how many lines of the folded dust's file this index holds (dust.go, E6-GT3): the burns that have been
	// dust for an hour are kept there, out of the state written every round, and counted in flight by their sum
	DustLines int `json:"dustLines,omitempty"`
	dust      *dustFile
}

// burnScanMax bounds the blocks one scan reads: an index far behind catches up over several rounds, with no verdict
// until its cursor reaches the snapshot's block (what is in flight is known only then).
const burnScanMax = 20_000

// ignoredKeep is how many ignored dispatches the index remembers by name.
const ignoredKeep = 32

func (x *burnIndex) init() {
	if x.Burns == nil {
		x.Burns = map[string]*burnRec{}
	}
}

// catchUp scans the blocks after the cursor (from the deployment's first block on a fresh index) up to `to`, at most
// burnScanMax of them, through the pair, and folds the burns in — none numbered below the launch line, read from the
// configured HlAdmin (back to the launch's) at the last block scanned: every scan starts at eth.deployBlock, so the
// line is drawn there already; an index that holds a burn below it (a scan started before the line, by a guessed
// start) drops it. It answers the burns new to the index and whether the cursor reached `to`.
func (x *burnIndex) catchUp(ctx context.Context, p ethx.Pair, mailbox, admin string, origin, gnoDomain uint32, routes []ethx.BurnRoute, from, to uint64, now time.Time) ([]*burnRec, bool, error) {
	x.init()
	lo := x.Cursor + 1
	if x.Cursor == 0 && from > 0 {
		lo = from
	}
	if lo > to {
		return nil, true, nil
	}
	hi := to
	if hi-lo+1 > burnScanMax {
		hi = lo + burnScanMax - 1
	}
	if admin == "" {
		return nil, false, fmt.Errorf("the config names no HlAdmin (eth.admin): the launch line (HlAdmin.launchNonce()) cannot be read, and no burn counts without it (PLAN.md §5.6)")
	}
	line, err := ethx.ReadLaunchLine(ctx, p, Address(admin), ethx.AtNumber(hi))
	if err != nil {
		return nil, false, fmt.Errorf("at block %d: %w", hi, err)
	}
	x.Line = line.Nonce
	for _, id := range x.ids() {
		if rec := x.Burns[id]; rec.Nonce < line.Nonce {
			delete(x.Burns, id)
			x.IgnoredN++
			x.Ignored = append(x.Ignored, fmt.Sprintf("tx %s: message #%d is below the launch line %d (HlAdmin.launchNonce()): dropped from the index, none of the bridge's burns", rec.Tx, rec.Nonce, line.Nonce))
		}
	}
	for id, rec := range x.folded() {
		if rec.Nonce < line.Nonce {
			x.unfold(id, 0)
			delete(x.Burns, id)
			x.IgnoredN++
		}
	}
	burns, ignored, err := ethx.ScanBurns(ctx, p, Address(mailbox), origin, gnoDomain, line.Nonce, routes, lo, hi)
	if err != nil {
		return nil, false, fmt.Errorf("the burns of blocks %d–%d: %w", lo, hi, err)
	}
	var added []*burnRec
	for _, b := range burns {
		rec, why := burnRecOf(b, now)
		id := hex.EncodeToString(b.ID[:])
		if why != "" {
			ignored = append(ignored, ethx.Ignored{Tx: b.Tx, LogIndex: b.LogIndex, Why: why})
			continue
		}
		if x.has(id) {
			continue
		}
		x.Burns[id] = rec
		added = append(added, rec)
	}
	for _, ig := range ignored {
		x.IgnoredN++
		x.Ignored = append(x.Ignored, fmt.Sprintf("tx %s log %d: %s", ig.Tx.Hex(), ig.LogIndex, ig.Why))
	}
	if len(x.Ignored) > ignoredKeep {
		x.Ignored = x.Ignored[len(x.Ignored)-ignoredKeep:]
	}
	x.Cursor = hi
	return added, hi == to, nil
}

// burnRecOf reads a scanned burn as the index keeps it, or says why it is not counted: an amount of zero moves
// nothing (the relayer skips it, §5.5, and nobody signs it), and an amount past what gno.land counts in base units
// can never be credited (the token message's amount is a uint256; the routes keep int64).
func burnRecOf(b ethx.Burn, now time.Time) (*burnRec, string) {
	if b.Amount == nil || b.Amount.Sign() <= 0 {
		return nil, "an amount of zero: nothing to credit, nothing to sign"
	}
	if !b.Amount.IsInt64() || b.Amount.Int64() > maxUnits {
		return nil, "an amount of " + b.Amount.String() + " base units, past what gno.land counts: never credited, never signed"
	}
	rec := &burnRec{Symbol: b.Symbol, Nonce: b.Nonce, Block: b.Block, Tx: b.Tx.Hex(), Amount: b.Amount.Int64(), Message: hex.EncodeToString(b.Message), Seen: now.Unix()}
	if to, err := hl.Bytes32ToGnoAddress(b.Recipient); err == nil {
		rec.To = to
	} else {
		rec.Bad = true
	}
	return rec, ""
}

// markCredited reads, for the burns not yet known credited, the height hlmailbox delivered each at — each read again
// only once hlmailbox's delivered count d (read last before the marks: the cut window's second read) moved since it was
// read unset, or all of them (all: a reading in doubt, or markRefresh passed) — and answers how many it found credited,
// the highest height among them (held to a head read after it: backends.go) and how many marks it read. A cut places
// each by its height (creditedAt): the marks are read after its window (E6-GT1).
func (x *burnIndex) markCredited(ctx context.Context, g gnoside.Reader, mailbox string, H int64, d int, all bool) (int, int64, int, error) {
	var unread []string
	for _, id := range x.ids() {
		if rec := x.Burns[id]; rec.Credited == 0 && (all || rec.mark.stale(d)) {
			unread = append(unread, id)
		}
	}
	marks, err := deliveredMarks(ctx, g, mailbox, unread, d, H)
	if err != nil {
		return 0, 0, 0, err
	}
	n, top := 0, int64(0)
	for _, id := range unread {
		rec := x.Burns[id]
		if at := marks[id]; at != 0 {
			rec.Credited, rec.mark = at, markSeen{}
			n++
			top = max(top, at)
		} else {
			rec.mark = markSeen{delivered: d, read: true}
		}
	}
	return n, top, len(unread), nil
}

// inflight sums by symbol the burns at or below block F that the state a cut's window read at head h does not credit —
// their marks unset, or set above h: after the window (E6-GT1); the folded dust among them (dust.go: uncredited as last
// read) — and lists the ids it credits whose burn is above F (the cut's order fails on them).
func (x *burnIndex) inflight(F uint64, h int64) (in map[string]int64, creditedAfter []string) {
	in = map[string]int64{}
	for _, id := range x.ids() {
		rec := x.Burns[id]
		switch {
		case rec.creditedAt(h) && rec.Block > F:
			creditedAfter = append(creditedAfter, id)
		case !rec.creditedAt(h) && rec.Block <= F:
			in[rec.Symbol] = satAdd(in[rec.Symbol], rec.Amount)
		}
	}
	// every folded burn is at or below F: folded at or below the snapshot's block of its round, and no reading is taken
	// below the floor (E3-GT9)
	for sym, st := range x.foldedSum() {
		in[sym] = satAdd(in[sym], st[0])
	}
	return in, creditedAfter
}

// ids are the index's ids in nonce order.
func (x *burnIndex) ids() []string {
	out := make([]string, 0, len(x.Burns))
	for id := range x.Burns {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := x.Burns[out[i]], x.Burns[out[j]]
		if a.Nonce != b.Nonce {
			return a.Nonce < b.Nonce
		}
		return out[i] < out[j]
	})
	return out
}

// waiting counts the burns not yet credited, the folded dust among them.
func (x *burnIndex) waiting() int {
	n := len(x.folded())
	for _, rec := range x.Burns {
		if rec.Credited == 0 {
			n++
		}
	}
	return n
}

// deployBlock is where a fresh scan starts when neither a -from-block nor the config's eth.deployBlock names it:
// finishSetup's block — the first block at which HlAdmin answers setupDone() true, found by a binary search over
// eth_call at past blocks (a full-history provider). It is the scans' start only (PLAN.md §5.6): what is no burn of
// this deployment is drawn by the launch line, HlAdmin's launchNonce(), which every scan filters by (catchUp); a
// planted setupDone could only move the start earlier, where the line still holds. A config that names no HlAdmin
// falls back to the first block holding the mailbox's code (and its scans count nothing: no line).
func deployBlock(ctx context.Context, c *ethx.Client, cfg *Config, head uint64) (uint64, error) {
	if cfg.Eth.Admin != "" && head > 0 {
		return setupDoneBlock(ctx, c, Address(cfg.Eth.Admin), head)
	}
	return mailboxCodeBlock(ctx, c, cfg.Eth.Mailbox, head)
}

// firstTrue is the first block in [1, head] at which a monotone reading holds, by binary search (it must hold at head).
func firstTrue(head uint64, holds func(uint64) (bool, error)) (uint64, error) {
	lo, hi := uint64(1), head // the first block it holds at is in (lo-1, hi]
	for lo < hi {
		mid := lo + (hi-lo)/2
		ok, err := holds(mid)
		if err != nil {
			return 0, err
		}
		if ok {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, nil
}

// setupDoneBlock is the first block at which HlAdmin's setupDone() is true: finishSetup's block.
func setupDoneBlock(ctx context.Context, c *ethx.Client, admin common.Address, head uint64) (uint64, error) {
	data, err := ethx.Admin.Pack("setupDone")
	if err != nil {
		return 0, err
	}
	done := func(n uint64) (bool, error) {
		out, err := c.CallAt(ctx, admin, data, ethx.AtNumber(n))
		if err != nil {
			return false, fmt.Errorf("HlAdmin's setupDone() at block %d (the scans' start is finishSetup's block, found by eth_call at past blocks: a full-history provider, or set eth.deployBlock): %w", n, err)
		}
		if len(out) == 0 {
			return false, nil // no HlAdmin there yet
		}
		vals, err := ethx.Admin.Unpack("setupDone", out)
		if err != nil || len(vals) != 1 {
			return false, fmt.Errorf("HlAdmin's setupDone() at block %d: %v", n, err)
		}
		b, _ := vals[0].(bool)
		return b, nil
	}
	ok, err := done(head)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("HlAdmin %s's setup is not done at block %d: the machines start once finishSetup is mined (set eth.deployBlock to its block)", admin.Hex(), head)
	}
	return firstTrue(head, done)
}

// mailboxCodeBlock is the first block holding the mailbox's code (a config that names no HlAdmin), else 1.
func mailboxCodeBlock(ctx context.Context, c *ethx.Client, mailbox string, head uint64) (uint64, error) {
	if mailbox == "" || head == 0 {
		return 1, nil
	}
	addr := Address(mailbox)
	has := func(n uint64) (bool, error) {
		code, err := c.CodeAt(ctx, addr, ethx.AtNumber(n))
		if err != nil {
			return false, fmt.Errorf("the mailbox's code at block %d (the scan's start is found by eth_getCode at past blocks: a full-history provider, or set eth.deployBlock): %w", n, err)
		}
		return len(code) > 0, nil
	}
	ok, err := has(head)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("no code at the mailbox %s at block %d", mailbox, head)
	}
	return firstTrue(head, has)
}

// nonceKey is a nonce as the store names its file.
func nonceKey(n uint32) string { return strconv.FormatUint(uint64(n), 10) }

package agent

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The cut's order over credits the burn index cannot place (the Go trust audit's E4-GT1). §5.9's cut needs the
// snapshot's finalized block F at or above the block of every burn credited in the gno state it reads; the order check
// (Cut.CreditedAfterBlock) sees only the burns in the index, which is scanned to the pair's finalized block — the
// lower of two providers' answers. When one of this machine's providers trails the signers' (an epoch's finalized tag
// updated a few seconds later, a lagging backend, or a liar answering the previous epoch), an honest burn the other
// signers saw final, signed and the relayer credited lies above the index: in no index, so the order check cannot name
// it, while the supply at F still counts its amount and owed counts it too — a reading BROKEN by its amount that is
// no reading at all.
//
// hlmailbox counts every message id it marked delivered (done.Size(): every Process and every MarkDone, nothing
// removes one), and the cut holds that count across its reads. Every delivered id the index holds is placed (its burn's
// block is known); the rest — the count less the index's credited burns — are delivered messages the index cannot
// place: an honest burn above the index (the lag), a forged credit, a credit by vote of an id no burn of the index
// carries, a message of a checkpointed index's missed range. While any is unplaced and the lag cannot be ruled out, a
// BROKEN reading is no verdict: it is kept pending and judged once the index's finalized block's time has passed the
// latest time any credit in it can have — every honest burn credited by then lies at or below that block, so the
// index holds it — with the honest lag the reading carried (the burns credited at or below its height whose block is
// above its F) taken off its excess. A held or short reading needs no such wait: an unplaced credit only adds to the
// right side, so it never hides a break. The lag is ruled out at once when nothing is unplaced, or — the monitors,
// which keep every burn — when every delivery unplaced now was already delivered by a height whose block time the
// index's finalized block has passed (deliverySample): a vote's credit or an old forgery then never delays the next
// verdict.

// pendingBreak is a BROKEN reading of a token whose cut's order is not shown (E4-GT1): kept, and judged once the
// index's finalized block's time passes Time.
type pendingBreak struct {
	Symbol string `json:"symbol"`
	Height int64  `json:"height"` // the gno height the reading's state was read at, at most (every credit in it is at or below)
	Time   int64  `json:"time"`   // that block's time
	Block  uint64 `json:"block"`  // the reading's finalized block F
	Excess int64  `json:"excess"` // by how much the right side with what is in flight exceeded the lock
	Text   string `json:"text"`
	Since  int64  `json:"since"` // when it was read (the machine's clock)
	// Lag is the honest lag the reading carried, found since: burns of the token credited at or below Height whose
	// block is above Block, by id — their amounts sat in the supply at F and in owed alike
	Lag map[string]int64 `json:"lag,omitempty"`
}

// lagSum is the lag found so far.
func (p *pendingBreak) lagSum() int64 {
	var s int64
	for _, a := range p.Lag {
		s = satAdd(s, a)
	}
	return s
}

// noteLag adds every burn of the index the reading carried as a lag: the token's, credited at or below the reading's
// height, its block above the reading's F.
func (p *pendingBreak) noteLag(x *burnIndex) {
	for id, rec := range x.Burns {
		if rec.Symbol == p.Symbol && rec.Credited != 0 && rec.Credited <= p.Height && rec.Block > p.Block {
			if p.Lag == nil {
				p.Lag = map[string]int64{}
			}
			p.Lag[id] = rec.Amount
		}
	}
}

// pendingVerdict is what becomes of a pending break at a cut whose index reaches a block of time indexTime.
type pendingVerdict int

const (
	pendingWaits     pendingVerdict = iota // the index's block time has not passed the reading's yet
	pendingLag                             // the lag found explains the whole excess: no break
	pendingConfirmed                       // the index's block time passed the reading's and the excess stands
)

// judgePending notes the lag the index shows and says what becomes of the pending break.
func (p *pendingBreak) judge(x *burnIndex, indexTime int64) pendingVerdict {
	p.noteLag(x)
	switch {
	case p.lagSum() >= p.Excess:
		return pendingLag
	case indexTime >= p.Time:
		return pendingConfirmed
	}
	return pendingWaits
}

// confirmedText is a confirmed pending break's text: the reading, the lag taken off, and why it is a verdict now.
func (p *pendingBreak) confirmedText(indexBlock uint64, indexTime int64) string {
	return fmt.Sprintf("%s — read at height %d (%s), held until the burn index's finalized block %d (%s) passed that time: every honest burn credited by then lies at or below it, so the excess of %d, less the lag of %d found since, stands (E4-GT1)",
		p.Text, p.Height, time.Unix(p.Time, 0).UTC().Format(time.RFC3339), indexBlock, time.Unix(indexTime, 0).UTC().Format(time.RFC3339), p.Excess, p.lagSum())
}

// pendingText is the no-verdict line of a reading kept pending.
func pendingText(text string, unplaced int, block uint64, indexBlock uint64) string {
	return fmt.Sprintf("BROKEN as read, but %d message(s) hlmailbox delivered are no burn the index holds, and the index ends at the finalized block %d (the snapshot's %d): an honest burn above it credited already counts in the supply and in owed alike, so this reading waits until the index's finalized block's time passes the reading's (E4-GT1) — %s", unplaced, indexBlock, block, text)
}

// excessOf is by how much a reading's right side with what is in flight exceeds the lock (≤ 0 when it does not).
func excessOf(id Identity) int64 { return satSub(satAdd(id.Right(), id.Inflight()), id.Lock) }

// placed counts the index's burns marked credited.
func (x *burnIndex) placed() int {
	n := 0
	for _, rec := range x.Burns {
		if rec.Credited != 0 {
			n++
		}
	}
	return n
}

// placedBy counts the index's burns credited at or below a height.
func (x *burnIndex) placedBy(h int64) int {
	n := 0
	for _, rec := range x.Burns {
		if rec.Credited != 0 && rec.Credited <= h {
			n++
		}
	}
	return n
}

// deliverySample is hlmailbox's delivered count as a cut read it, with a height its state was at or below (the own
// node's head read after the reads) and that block's time: every delivery it counts was made at or below the height.
type deliverySample struct {
	Height    int64 `json:"height"`
	Time      int64 `json:"time"`
	Delivered int   `json:"delivered"`
}

// samplesKeep bounds the samples kept: one a minute at most, so an hour and a half of them (the index's finalized
// block trails the head by 13–19 minutes in the ordinary run).
const samplesKeep = 96

// pendingFlagAfter is how long a pending reading may wait unjudged before its alarm is raised to flag level.
const pendingFlagAfter = time.Hour

// noteSample keeps a sample, one a minute at most whatever the count did (any sample is a lower bound, so thinning
// keeps the rule sound; one a poll under traffic left no sample older than the index's block: the review's finding 5).
func noteSample(ss []deliverySample, s deliverySample) []deliverySample {
	if n := len(ss); n > 0 {
		last := ss[n-1]
		if s.Height <= last.Height || s.Time-last.Time < 60 {
			return ss
		}
	}
	ss = append(ss, s)
	if len(ss) > samplesKeep {
		ss = ss[len(ss)-samplesKeep:]
	}
	return ss
}

// oldUnplaced is how many deliveries the index cannot place were made at or below the newest sample whose block time is
// at or before indexTime — no honest lag, since a burn credited by then lies at or below the index's finalized block —
// a lower bound (the sample counts no delivery above the state it was read at); -1 when no sample is that old.
func oldUnplaced(ss []deliverySample, indexTime int64, x *burnIndex) int {
	i := sort.Search(len(ss), func(i int) bool { return ss[i].Time > indexTime }) - 1
	if i < 0 {
		return -1
	}
	return max(0, ss[i].Delivered-x.placedBy(ss[i].Height))
}

// headAfterCut reads the own node's head once a cut's marks are read and before its re-read (cutMoved): every credit
// the cut's state holds was made at or below it (a gno node answers its latest state at any height), and a credit at or
// below it that the state lacks moves the delivered count the re-read holds — so the credits at or below it are the
// state's exactly.
func headAfterCut(ctx context.Context, g gnoside.Reader) (int64, int64, error) {
	h, err := g.Status(ctx)
	if err != nil {
		return 0, 0, err
	}
	return h.Height, h.Time.Unix(), nil
}

// The dispatches above the burn index (the Go trust audit's E5-GT1, the plan red team's E5-RT6). A BROKEN reading
// waits while a delivery the index cannot place may be an honest burn above the index — but what both providers show
// above it bounds that lag: an honest burn credited on gno.land was signed at or below a signer's finalized block, so
// it lies below either provider's head while that head is fresh, and an honest provider shows its Dispatch, which only
// the token itself can send (Mailbox.dispatch's sender is its caller). So, for a token, the burns the index holds above
// the reading's block that the cut's state credits, and every burn of the token either provider shows above the index
// that the cut's state credits (or whose mark did not read), sum to the most an honest lag can carry: an excess past it
// is a break at once, flagged or halted on, never kept waiting the finality lag (a forged credit paid out in its own
// block sits in no waiting index, so the dispatch check on waiting claims never sees it). A liar can only add burns —
// which widens the bound, the old wait — and hides none the honest one shows; a burn's token and amount are its
// message's, which its id binds, never a log's topics. Residual, as the fast path's (E5-GT4 item 2): both providers
// omitting an honest burn's log above the index — two faults — flags its lag at once. The credited marks are read before the
// cut's re-read, so with the cut holding they are the state's: the highest of them, and of the index's, raises the
// reading's height, which a load-balanced endpoint can answer from a backend a block behind the one that served the
// state (E5-RT6) — an honest credit there is then counted as the lag it is.

// aboveIndex is what the providers show dispatched toward gno.land above the burn index's finalized block, each to its
// own head: every message id (anyone's dispatch: the forged-credit rule's fast path), and the configured tokens' own
// burns with their amounts and, read at a cut, their credited marks. Known only when both providers answered, each
// with a head no older than headFresh and no more than aboveRangeMax blocks above the index — a provider that lags or
// does not answer cannot vouch for an absence, and a long stall is the finality rule's (E5-RT1).
type aboveIndex struct {
	ids   map[string]bool
	burns map[string]*aboveBurn
	known bool
	heads string
}

// aboveBurn is one burn of a configured token above the index, as either provider shows it.
type aboveBurn struct {
	Symbol   string
	Amount   int64
	Block    uint64
	Credited int64 // the height hlmailbox delivered it at in the cut's state; 0 not delivered there; -1 not read
}

// headFresh is how old a provider's head may be for its logs to say a message was never dispatched above the index.
const headFresh = 2 * time.Minute

// aboveRangeMax bounds the blocks above the index a reading asks of each provider: two hours of blocks, past which the
// finalized block is far older than the finality rule allows (E5-RT1) and nothing above it is vouched for.
const aboveRangeMax = 600

// aboveMarksMax bounds the credited marks a cut reads for the burns above the index (each a render of hlmailbox): one
// not read counts as credited — the bound only widens, the old wait.
const aboveMarksMax = 200

// readAbove reads the dispatches toward gno.land in the blocks above `from` (the index's finalized block), through each
// provider to its own head. The burns of the configured tokens are kept with their amounts, the token and the amount
// read from the message itself (its sender, its token message), which its id binds: a provider that alters a log's
// topics or omits a log can neither move an honest burn to another token nor hide one the other provider shows.
func readAbove(ctx context.Context, p ethx.Pair, cfg *Config, from uint64, gnoDomain uint32, now time.Time) *aboveIndex {
	a := &aboveIndex{ids: map[string]bool{}, burns: map[string]*aboveBurn{}}
	if cfg.Eth.Mailbox == "" || p.A == nil || p.B == nil {
		return a
	}
	tokens := map[common.Address]string{}
	for _, r := range cfg.Routes {
		if r.Token != "" {
			tokens[r.TokenAddress()] = r.Symbol
		}
	}
	q := ethx.LogQuery{Addresses: []common.Address{Address(cfg.Eth.Mailbox)}, Topics: [][]common.Hash{{ethx.Mailbox.Event("Dispatch")}, nil, {ethx.Uint32Topic(gnoDomain)}}}
	known := true
	var heads []string
	for _, c := range []*ethx.Client{p.A, p.B} {
		head, err := c.BlockAt(ctx, ethx.Latest)
		if err != nil {
			known = false
			continue
		}
		heads = append(heads, fmt.Sprintf("%s's %d", c.Provider.Name, head.Number))
		if now.Sub(time.Unix(int64(head.Time), 0)) > headFresh {
			known = false // its logs are still read: a burn it shows is a burn either provider shows
		}
		if head.Number <= from {
			continue
		}
		to := head.Number
		if to-from > aboveRangeMax {
			known, to = false, from+aboveRangeMax
		}
		logs, err := c.LogsRange(ctx, q, from+1, to)
		if err != nil {
			known = false
			continue
		}
		for _, l := range logs {
			vals, err := ethx.Mailbox.ABI.Events["Dispatch"].Inputs.NonIndexed().Unpack(l.Data)
			if err != nil || len(vals) != 1 {
				continue
			}
			raw, ok := vals[0].([]byte)
			if !ok {
				continue
			}
			mid := hl.ID(raw)
			id := hex.EncodeToString(mid[:])
			a.ids[id] = true
			if a.burns[id] != nil {
				continue // the same bytes (the id is their hash): the same token and amount
			}
			// the token and the amount are the message's own, which its id binds — never the log's topics, which a
			// lying provider can change under an honest message (filing an honest burn under the other token would
			// leave it out of its own token's bound: the review of this fold's finding 1)
			msg, err := hl.ParseMessage(raw)
			if err != nil || len(msg.Body) < 64 {
				continue
			}
			sender, ok := ethx.AddressFrom32(msg.Sender)
			sym, ours := tokens[sender]
			if !ok || !ours {
				continue
			}
			a.burns[id] = &aboveBurn{Symbol: sym, Amount: supplyUnits(new(big.Int).SetBytes(msg.Body[32:64])), Block: l.BlockNumber, Credited: -1}
		}
	}
	a.known, a.heads = known && len(heads) == 2, strings.Join(heads, ", ")
	return a
}

// markAbove reads, for the burns above the index, the height hlmailbox delivered each at as of H (aboveMarksMax at
// most, by id; one that does not read, and the rest, stay -1: counted as credited).
func (a *aboveIndex) markAbove(ctx context.Context, g gnoside.Reader, mailbox string, H int64) {
	if a == nil {
		return
	}
	ids := sortedSymbols(a.burns)
	for i, id := range ids {
		if i == aboveMarksMax {
			return
		}
		at, err := gnoside.DeliveredAt(ctx, g, mailbox, id, H)
		if err != nil {
			return
		}
		a.burns[id].Credited = at
	}
}

// creditTop is the highest credit a cut's read of the state shows: the marks this cut read for the index's burns
// (markedTop; a mark an earlier read gave may be a backend's or a node's the state is not at) and for the burns above
// the index — a height the state was read at or above, the cut holding (E5-RT6).
func creditTop(markedTop int64, a *aboveIndex) int64 {
	top := markedTop
	if a != nil {
		for _, b := range a.burns {
			top = max(top, b.Credited)
		}
	}
	return top
}

// lagMost is the most an honest lag can carry for a token in a reading at block F (E5-GT1): the burns of the index
// above F that the state credits, and the burns either provider shows above the index that it credits or whose mark did
// not read; false when the providers' reading above the index is not known (the wait, as before).
func lagMost(sym string, F uint64, x *burnIndex, a *aboveIndex) (int64, bool) {
	if a == nil || !a.known {
		return 0, false
	}
	var most int64
	for _, rec := range x.Burns {
		if rec.Symbol == sym && rec.Block > F && rec.Credited != 0 {
			most = satAdd(most, rec.Amount)
		}
	}
	for _, b := range a.burns {
		if b.Symbol == sym && b.Credited != 0 {
			most = satAdd(most, b.Amount)
		}
	}
	return most, true
}

// beyondLagText says why a BROKEN reading is a verdict at once although a delivery is unplaced (E5-GT1).
func beyondLagText(text string, excess, most int64, F uint64, a *aboveIndex) string {
	return fmt.Sprintf("%s — the excess of %d is more than any honest lag the reading can carry (%d: the token's burns above its block %d that the cut's state credits, the index's and those either provider shows to its head, %s): a break at once, not kept waiting for the index (E5-GT1)", text, excess, most, F, a.heads)
}

// raiseToCredits raises a reading's height and time to the highest credit its state shows, when that is above the head
// read after the cut (a load-balanced endpoint's backend a block behind: E5-RT6): the time is that block's as the node
// gives it, else the machine's clock (a later time only waits longer).
func raiseToCredits(ctx context.Context, g gnoside.Reader, markedTop int64, a *aboveIndex, h, t int64, now time.Time) (int64, int64) {
	top := creditTop(markedTop, a)
	if top <= h {
		return h, t
	}
	if b, err := g.Block(ctx, top); err == nil && !b.Time.IsZero() && b.Time.Unix() >= t {
		return top, b.Time.Unix()
	}
	return top, max(t, now.Unix())
}

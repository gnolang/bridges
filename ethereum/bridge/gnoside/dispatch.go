package gnoside

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// The Dispatch events in gno.land's blocks: a message's event is where it can be proven from (header H+1's
// LastResultsHash commits to block H's results), and a message has one event per dispatch and per Reemit — anyone
// may re-emit, and the relayer's batch re-emit (§5.7) moves a waiting message's newest event into one shared block.
// The updater and the relayer each keep an index of the heights every message's events are at, scanning the
// chain's blocks forward: tm2's `blockchain` listing (twenty block metas a request) finds the blocks with
// transactions, and only those are read for results. r/sys/validators' events are noted too (the updater's
// trigger for the announcing header, §6.1), and a block whose results leaves cannot be rebuilt (an error or event
// type gnolight has no schema for: a failed transaction's leaf is rebuilt from tm2's closed set of error types) is
// marked, and so is one whose rebuilt root is not the next header's LastResultsHash (Mismatch): nothing in it can be
// proven. A route's send
// leaves its own fee in its SentTransferRemote event beside its Dispatch, in the same transaction: the scan keeps it
// by message, and a waiting send's header is bought by the fee that send paid (§5.5), never the route's fee now.

// Blocks is a gno node's blocks as the scan reads them.
type Blocks interface {
	NumTxs(ctx context.Context, from, to int64) (map[int64]int64, error)
	BlockResults(ctx context.Context, height int64) ([]*gnolight.TxResult, error)
}

// Node is a gno client as Blocks and Light.
type Node struct{ *gno.Client }

// NumTxs is every height's transaction count in [from, to], from tm2's `blockchain` route (twenty metas a request,
// highest first); a height the node does not answer for is an error, never a block without transactions.
func (n Node) NumTxs(ctx context.Context, from, to int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	for hi := to; hi >= from; hi -= 20 {
		lo := max(from, hi-19)
		var res struct {
			BlockMetas []struct {
				Header struct {
					Height json.Number `json:"height"`
					NumTxs json.Number `json:"num_txs"`
				} `json:"header"`
			} `json:"block_metas"`
		}
		if err := n.Request(ctx, "blockchain", map[string]any{"minHeight": strconv.FormatInt(lo, 10), "maxHeight": strconv.FormatInt(hi, 10)}, &res); err != nil {
			return nil, err
		}
		for _, m := range res.BlockMetas {
			h, err1 := m.Header.Height.Int64()
			txs, err2 := numOr0(m.Header.NumTxs)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("gno blockchain %d–%d: a meta of height %q, %q transactions", lo, hi, m.Header.Height, m.Header.NumTxs)
			}
			out[h] = txs
		}
		for h := lo; h <= hi; h++ {
			if _, ok := out[h]; !ok {
				return nil, fmt.Errorf("gno blockchain %d–%d: no meta for height %d", lo, hi, h)
			}
		}
	}
	return out, nil
}

func numOr0(n json.Number) (int64, error) {
	if n == "" {
		return 0, nil
	}
	return n.Int64()
}

// BlockEvents is what one block's results say: hlmailbox's Dispatch events, r/sys/validators' events, whether its
// leaves rebuild (and the root they hash to), and the size of the leaf each message would be proven from.
type BlockEvents struct {
	Height     int64
	Dispatches []gnolight.Dispatch
	Valset     bool
	Buildable  bool
	Why        string // why the leaves do not rebuild
	// Schema is the type URL of an error or event the leaves cannot be rebuilt for (gnolight's SchemaError: a type a
	// chain upgrade added), "" for none: alarmed by name, since it would show only as re-emits otherwise
	Schema string
	// Root is the rebuilt leaves' simple hash, which header Height+1's LastResultsHash must equal for anything in the
	// block to be proven (a string the node's JSON cannot carry — bytes that are not UTF-8 read as U+FFFD — rebuilds
	// other leaves than the chain hashed)
	Root [32]byte
	// Leaves is, by message id, the byte size of the results leaf a proof of it is built from — the smallest leaf
	// whose Dispatch events (read from the leaf's own bytes, as a proof is built) carry it, the lowest index among
	// equals (ProofLeaf) — −1 when no leaf does. Both machines judge an event's usability from it alike (a leaf over the
	// bound proves nothing). The smallest, never the first: a stranger's transaction landing before the relayer's
	// batch in its block, re-emitting the batch's nonces beside kilobytes of its own events, would otherwise make the
	// batch's leaf its own (the plan red team's E3-RT2).
	Leaves map[[32]byte]int
	// LeafDispatches is, by message id, the number of the mailbox's Dispatch events in that leaf: the client reads
	// every one whole, so each costs every delivery proven from the leaf ≈94,000 gas (E-M2b), and a leaf of more than a
	// batch's is never proven from for a route (eth/bridge/PLAN.md §5.7; the plan red team's E2-RT2).
	LeafDispatches map[[32]byte]int
	// Fees is, by message id, the fee a send paid as its route's SentTransferRemote event in the same transaction as
	// its Dispatch records it, with the realm that emitted the event (a send's own realm: the message's sender).
	Fees map[[32]byte]SendFee
}

// SendFee is the fee one send paid (base units of its token) and the realm whose SentTransferRemote event says so.
type SendFee struct {
	Realm string `json:"realm"`
	Fee   int64  `json:"fee"`
}

// SentTransferRemote is the event a route realm emits for a send (r/hlgnot's, r/hlbubble's: destination,
// recipient, amount, fee, from, id).
const SentTransferRemote = "SentTransferRemote"

// sendFees are the fees the sends of one transaction paid, by message id: each SentTransferRemote event whose id is a
// Dispatch of the mailbox in the same transaction and whose fee is a number.
func sendFees(r *gnolight.TxResult, mailbox string) map[[32]byte]SendFee {
	ids := map[[32]byte]bool{}
	for _, e := range r.Events {
		if e.Type == gnolight.DispatchType && e.PkgPath == mailbox {
			if d, err := gnolight.DispatchFromEvent(e); err == nil {
				ids[d.ID] = true
			}
		}
	}
	var out map[[32]byte]SendFee
	for _, e := range r.Events {
		if e.Type != SentTransferRemote || e.PkgPath == "" {
			continue
		}
		var idHex, feeText string
		for _, a := range e.Attributes {
			switch a.Key {
			case "id":
				idHex = a.Value
			case "fee":
				feeText = a.Value
			}
		}
		b, err := hex.DecodeString(strings.TrimPrefix(idHex, "0x"))
		fee, ferr := strconv.ParseInt(feeText, 10, 64)
		if err != nil || len(b) != 32 || ferr != nil || fee < 0 {
			continue
		}
		var id [32]byte
		copy(id[:], b)
		if !ids[id] {
			continue
		}
		if out == nil {
			out = map[[32]byte]SendFee{}
		}
		if _, seen := out[id]; !seen {
			out[id] = SendFee{Realm: e.PkgPath, Fee: fee}
		}
	}
	return out
}

// ScanBlocks reads [from, to], at most maxBlocks of them from `from` up, and answers the blocks with an event or an
// unbuildable leaf, oldest first, and the last height read (from−1 when none was).
func ScanBlocks(ctx context.Context, b Blocks, mailbox string, from, to int64, maxBlocks int64) ([]BlockEvents, int64, error) {
	if to < from {
		return nil, from - 1, nil
	}
	if maxBlocks > 0 && to-from+1 > maxBlocks {
		to = from + maxBlocks - 1
	}
	txs, err := b.NumTxs(ctx, from, to)
	if err != nil {
		return nil, from - 1, err
	}
	var out []BlockEvents
	for h := from; h <= to; h++ {
		if txs[h] == 0 {
			continue
		}
		rs, err := b.BlockResults(ctx, h)
		if err != nil {
			return out, h - 1, err
		}
		be := BlockEvents{Height: h, Buildable: true, Valset: gno.ValsetEvent(rs)}
		leaves, err := gnolight.ResultLeaves(rs)
		if err != nil {
			be.Buildable, be.Why = false, err.Error()
			var se *gnolight.SchemaError
			if errors.As(err, &se) {
				be.Schema = se.TypeURL
			}
		} else {
			be.Root, _ = gnolight.SimpleHash(leaves)
		}
		for _, r := range rs {
			for _, e := range r.Events {
				if e.Type != gnolight.DispatchType || e.PkgPath != mailbox {
					continue
				}
				d, err := gnolight.DispatchFromEvent(e)
				if err != nil {
					continue // not the seven attributes of §13: no message of ours can be proven by it
				}
				be.Dispatches = append(be.Dispatches, d)
			}
			for id, f := range sendFees(r, mailbox) {
				if be.Fees == nil {
					be.Fees = map[[32]byte]SendFee{}
				}
				if _, seen := be.Fees[id]; !seen {
					be.Fees[id] = f
				}
			}
		}
		if be.Buildable && len(be.Dispatches) > 0 {
			be.Leaves, be.LeafDispatches = leafSizes(leaves, mailbox, be.Dispatches)
		}
		if len(be.Dispatches) > 0 || be.Valset || !be.Buildable {
			out = append(out, be)
		}
	}
	return out, to, nil
}

// leafSizes is, for each dispatch, the byte size of the leaf a proof of it is built from (ProofLeaf's choice; −1 for
// none) and the number of the mailbox's Dispatch events in that leaf — from one pass over the block's leaves
// (ProofLeaves), never a pass per dispatch: anyone's block of small Reemit transactions made that dispatches × leaves
// parses, minutes of both machines' ticks (E4-GS3).
func leafSizes(leaves [][]byte, mailbox string, ds []gnolight.Dispatch) (map[[32]byte]int, map[[32]byte]int) {
	best := ProofLeaves(leaves, mailbox)
	sizes, counts := map[[32]byte]int{}, map[[32]byte]int{}
	for _, d := range ds {
		if b, ok := best[d.ID]; ok {
			sizes[d.ID], counts[d.ID] = len(leaves[b.Index]), b.Dispatches
		} else {
			sizes[d.ID] = -1
		}
	}
	return sizes, counts
}

// LeafChoice is the leaf a message's proof is built from: its index in the block's leaves and its count of the
// mailbox's Dispatch events.
type LeafChoice struct {
	Index, Dispatches int
}

// ProofLeaves is ProofLeaf's choice for every id a block's leaves carry, in one pass (E4-GS3): each leaf is parsed once
// (gnolight.DispatchEvents: a failed transaction's leaf, or one that does not parse, carries nothing), and each id is
// given the smallest leaf whose own Dispatch events of the mailbox realm carry it, the lowest index among equals —
// gnolight.DispatchLeaf's rule, so the scan and the relayer judge the same leaf.
func ProofLeaves(leaves [][]byte, mailbox string) map[[32]byte]LeafChoice {
	out := map[[32]byte]LeafChoice{}
	for i, leaf := range leaves {
		ds, err := gnolight.DispatchEvents(leaf, mailbox)
		if err != nil {
			continue
		}
		for _, d := range ds {
			if b, ok := out[d.ID]; !ok || len(leaf) < len(leaves[b.Index]) {
				out[d.ID] = LeafChoice{Index: i, Dispatches: len(ds)}
			}
		}
	}
	return out
}

// ProofLeaf is the leaf of a block's results a message's proof is built from — gnolight.DispatchLeaf's: the smallest
// leaf whose own Dispatch events (of the mailbox realm) carry the id, the lowest index among equals, a failed
// transaction's leaf never — the scan's choice and the relayer's alike, so both machines judge the same leaf, with its
// count of the mailbox's Dispatch events; −1 when none does. Never merely the first: a stranger's transaction landing
// before the relayer's batch in its block, re-emitting its nonces beside kilobytes of its own events, would make the
// batch's leaf its own (E3-RT2). Every Dispatch is ≈450 bytes of fixed form, so no leaf with more Dispatch events than
// a leaf of Dispatch events alone is smaller than it: a stranger's leaf chosen over the batch's is never dearer.
func ProofLeaf(leaves [][]byte, mailbox string, id [32]byte) (index, dispatches int) {
	i := gnolight.DispatchLeaf(leaves, id, mailbox)
	if i < 0 {
		return -1, 0
	}
	ds, _ := gnolight.DispatchEvents(leaves[i], mailbox)
	return i, len(ds)
}

// EventIndex is the heights each message's Dispatch events are at — its dispatch and every Reemit the scan saw — the
// size of the leaf each would be proven from, and the blocks that cannot be proven from, with the scan's position.
// It is kept in a machine's state file.
type EventIndex struct {
	Heights     map[uint32][]int64       `json:"heights"`
	Leaves      map[uint32]map[int64]int `json:"leaves,omitempty"` // nonce → height → the leaf's bytes (−1: no leaf carries it)
	Counts      map[uint32]map[int64]int `json:"counts,omitempty"` // nonce → height → the mailbox's Dispatch events in that leaf
	Fees        map[uint32]SendFee       `json:"fees,omitempty"`   // nonce → the fee its send paid (its SentTransferRemote)
	Unbuildable map[int64]string         `json:"unbuildable"`
	// Roots are the rebuilt results roots of the blocks holding our messages' events (hex), held to the next header's
	// LastResultsHash before anything is proven from them (Mismatch)
	Roots   map[int64]string `json:"roots,omitempty"`
	Scanned int64            `json:"scanned"` // the last height read
}

func (x *EventIndex) init() {
	if x.Heights == nil {
		x.Heights = map[uint32][]int64{}
	}
	if x.Leaves == nil {
		x.Leaves = map[uint32]map[int64]int{}
	}
	if x.Counts == nil {
		x.Counts = map[uint32]map[int64]int{}
	}
	if x.Fees == nil {
		x.Fees = map[uint32]SendFee{}
	}
	if x.Unbuildable == nil {
		x.Unbuildable = map[int64]string{}
	}
	if x.Roots == nil {
		x.Roots = map[int64]string{}
	}
}

// Add records a scanned block's events for the destination's messages (the other destinations' are not ours); a
// block that cannot be proven from is marked only when it carries one of them (no other block is any message's
// event, so the marks never grow by the chain's failed transactions).
func (x *EventIndex) Add(be BlockEvents, destination uint32) {
	x.init()
	for _, d := range be.Dispatches {
		if d.Destination != destination {
			continue
		}
		if !be.Buildable {
			x.Unbuildable[be.Height] = be.Why
		} else {
			x.Roots[be.Height] = hex.EncodeToString(be.Root[:])
		}
		x.Note(d.Nonce, be.Height)
		if n, ok := be.Leaves[d.ID]; ok {
			if x.Leaves[d.Nonce] == nil {
				x.Leaves[d.Nonce] = map[int64]int{}
			}
			if _, seen := x.Leaves[d.Nonce][be.Height]; !seen {
				x.Leaves[d.Nonce][be.Height] = n
			}
		}
		if n, ok := be.LeafDispatches[d.ID]; ok {
			if x.Counts[d.Nonce] == nil {
				x.Counts[d.Nonce] = map[int64]int{}
			}
			if _, seen := x.Counts[d.Nonce][be.Height]; !seen {
				x.Counts[d.Nonce][be.Height] = n
			}
		}
		if f, ok := be.Fees[d.ID]; ok {
			if _, seen := x.Fees[d.Nonce]; !seen {
				x.Fees[d.Nonce] = f
			}
		}
	}
}

// FeeOf is the fee a message's send paid as the scan read it from its SentTransferRemote event; false when the scan
// read none (a decision, a stranger's dispatch, or a block an older index scanned without fees).
func (x *EventIndex) FeeOf(nonce uint32) (SendFee, bool) {
	f, ok := x.Fees[nonce]
	return f, ok
}

// LeafOf is the size of the leaf a message's event at a height would be proven from, as the scan read it (−1: no
// leaf carries it); false when the scan has not read that block.
func (x *EventIndex) LeafOf(nonce uint32, height int64) (int, bool) {
	n, ok := x.Leaves[nonce][height]
	return n, ok
}

// CountOf is the number of the mailbox's Dispatch events in the leaf a message's event at a height would be proven
// from, as the scan read it; false when the scan has not read that block (or read it before it counted them).
func (x *EventIndex) CountOf(nonce uint32, height int64) (int, bool) {
	n, ok := x.Counts[nonce][height]
	return n, ok
}

// Note records one event of a message (a dispatch's height read from the realm, a Reemit's own result).
func (x *EventIndex) Note(nonce uint32, height int64) {
	x.init()
	hs := x.Heights[nonce]
	for _, h := range hs {
		if h == height {
			return
		}
	}
	hs = append(hs, height)
	sort.Slice(hs, func(i, j int) bool { return hs[i] < hs[j] })
	x.Heights[nonce] = hs
}

// Of is a message's event heights known, ascending, with its dispatch height (the realm's own record) among them;
// the heights of unbuildable blocks are left out (nothing can be proven from them).
func (x *EventIndex) Of(nonce uint32, dispatched int64) []int64 {
	x.init()
	seen := map[int64]bool{}
	var out []int64
	for _, h := range append([]int64{dispatched}, x.Heights[nonce]...) {
		if h <= 0 || seen[h] {
			continue
		}
		seen[h] = true
		if _, bad := x.Unbuildable[h]; bad {
			continue
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Drop forgets one event of a message: one no header can prove any more (the walk's compaction at the finalized block,
// E5-GS4: anyone's Reemit adds an event a block, and every event kept was read every step).
func (x *EventIndex) Drop(nonce uint32, height int64) {
	x.init()
	hs := x.Heights[nonce]
	out := make([]int64, 0, len(hs))
	for _, h := range hs {
		if h != height {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		delete(x.Heights, nonce)
	} else {
		x.Heights[nonce] = out
	}
	if m := x.Leaves[nonce]; m != nil {
		delete(m, height)
		if len(m) == 0 {
			delete(x.Leaves, nonce)
		}
	}
	if m := x.Counts[nonce]; m != nil {
		delete(m, height)
		if len(m) == 0 {
			delete(x.Counts, nonce)
		}
	}
}

// Forget drops a message ended (delivered, refunded, not ours).
func (x *EventIndex) Forget(nonce uint32) {
	x.init()
	delete(x.Heights, nonce)
	delete(x.Leaves, nonce)
	delete(x.Counts, nonce)
	delete(x.Fees, nonce)
}

// Mismatch holds a block's rebuilt root to the LastResultsHash the header after it carries: when they differ the block
// is marked unbuildable — nothing in it can be proven, its messages are re-emitted — and true is answered with why
// (the leaves rebuilt from the node's JSON are not the chain's: a string the JSON cannot carry, a type whose fields
// changed, or a node serving other results). A block the index holds no root for is not judged.
func (x *EventIndex) Mismatch(h int64, lastResults [32]byte) (bool, string) {
	x.init()
	root, ok := x.Roots[h]
	if !ok || root == hex.EncodeToString(lastResults[:]) {
		return false, ""
	}
	why := fmt.Sprintf("block %d's re-encoded results hash to %s, header %d says %x", h, root, h+1, lastResults)
	x.Unbuildable[h] = why
	delete(x.Roots, h)
	return true, why
}

// Spoiled is how many of a message's events past its dispatch lie in blocks nothing can be proven from: re-emits that
// landed beside a stranger's transaction the leaves cannot be rebuilt for.
func (x *EventIndex) Spoiled(nonce uint32, dispatched int64) int {
	x.init()
	n := 0
	for _, h := range x.Heights[nonce] {
		if _, bad := x.Unbuildable[h]; bad && h != dispatched {
			n++
		}
	}
	return n
}

// MarkUnbuildable marks a block nothing can be proven from (a rebuilt root the chain's header does not carry).
func (x *EventIndex) MarkUnbuildable(h int64, why string) {
	x.init()
	x.Unbuildable[h] = why
	delete(x.Roots, h)
}

// PruneBelow drops the unbuildable marks and the roots of blocks below a height (no message waiting is that old).
func (x *EventIndex) PruneBelow(h int64) {
	x.init()
	for b := range x.Unbuildable {
		if b < h {
			delete(x.Unbuildable, b)
		}
	}
	for b := range x.Roots {
		if b < h {
			delete(x.Roots, b)
		}
	}
}

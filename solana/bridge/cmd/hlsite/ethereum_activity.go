package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// Ethereum's activity (E-M4): what its own logs say happened, read lazily by block ranges within the provider's
// limits and resumed where the last read stopped. One eth_getLogs a window asks for every event of ours at once —
// the mailbox's Dispatch (the burns toward gno.land: ER5's filter), Process and ProcessId (the deliveries of
// gno.land's messages), the tokens' ReceivedTransferRemote, the client's HeaderRecorded — and each delivering call
// is read once for its message and its proof (process(metadata, message): the header it is proven under, the
// results tree's shape). Blocks at or below the finalized one are read once and kept; the blocks past it (the
// tail) are read again at every read, so a reorganization takes back what it held. The first read covers the
// newest windows; later reads go forward over what was finalized since, and back over the history a few windows
// at a time until the deployment's first block (fromBlock), thirty days, or the kept lists are full.

const (
	ethWindows  = 6      // eth_getLogs calls a read makes at most, the tail's among them
	ethHistory  = 216000 // blocks of history read back at most: thirty days of twelve-second blocks
	ethMinRange = 10     // the smallest window a refusing provider is asked for
)

// ethProgress is how far the index has read; it rides in OtherActivity and is copied with it at each refresh.
type ethProgress struct {
	started bool
	hi, lo  uint64          // the logs of blocks lo … hi are folded in for good (each at or below the finalized block when read)
	done    bool            // the history is read back as far as it goes
	rng     uint64          // blocks per eth_getLogs now: the config's, halved at each refusal
	tent    map[string]bool // what the tail added at the last read, taken out before the next
	hdr     map[[32]byte]int64
}

func (p *ethProgress) clone() *ethProgress {
	if p == nil {
		return nil
	}
	c := *p
	c.tent, c.hdr = map[string]bool{}, map[[32]byte]int64{}
	for k, v := range p.tent {
		c.tent[k] = v
	}
	for k, v := range p.hdr {
		c.hdr[k] = v
	}
	return &c
}

// dropTentative takes out of the index what the last read's tail put in: the tail is read again now.
func (p *ethProgress) dropTentative(a *OtherActivity) {
	for k := range p.tent {
		kind, key, _ := strings.Cut(k, ":")
		switch kind {
		case "b":
			delete(a.Burns, key)
		case "d":
			delete(a.Deliveries, key)
		case "p":
			delete(a.Proofs, key)
		case "h":
			h, _ := strconv.ParseInt(key, 10, 64)
			kept := a.Headers[:0]
			for _, x := range a.Headers {
				if x.Height != h {
					kept = append(kept, x)
				}
			}
			a.Headers = kept
		}
	}
	p.tent = map[string]bool{}
}

// watched are the contracts whose logs the index reads.
func (e *ethereumChain) watched() []ethAddr {
	out := []ethAddr{e.mailbox, e.client}
	for _, t := range e.tokens {
		out = append(out, t.addr)
	}
	return out
}

var ethEvents = [][][32]byte{{topicDispatch, topicProcess, topicProcessID, topicHeaderRecorded, topicReceived}}

// Activity folds what is new on Ethereum into a.
func (e *ethereumChain) Activity(ctx context.Context, a *OtherActivity, pending bool) {
	a.init()
	a.Errors = nil
	defer a.summarize()
	if a.eth == nil {
		a.eth = &ethProgress{rng: max(e.cfg.LogRange, ethMinRange), tent: map[string]bool{}, hdr: map[[32]byte]int64{}}
	}
	p := a.eth
	p.dropTentative(a)
	defer e.settle(a)
	c, cancel := withBudget(ctx)
	fin, err := e.tagged(c, "finalized")
	cancel()
	if err != nil {
		a.fail("the finalized block", err)
		return
	}
	c, cancel = withBudget(ctx)
	head, err := e.tagged(c, latest)
	cancel()
	if err != nil {
		a.fail("the newest block", err)
		return
	}
	c, cancel = withBudget(ctx)
	if w, err := e.word(c, e.client, calldata(selTrustedHeight), latest); err != nil {
		a.fail("the light client", err)
	} else if h, err := i64Of(w); err == nil && h >= 0 {
		a.Trusted = uint64(h)
	}
	cancel()
	if !p.started {
		p.started, p.hi, p.lo = true, fin.Number, fin.Number+1
	}
	floor := e.cfg.FromBlock
	if fin.Number > ethHistory && fin.Number-ethHistory > floor {
		floor = fin.Number - ethHistory
	}
	budget := ethWindows - 1 // the last is the tail's
	// forward: what was finalized since the last read, oldest first
	for budget > 0 && p.hi < fin.Number {
		from := p.hi + 1
		to := min(fin.Number, from+p.rng-1)
		budget--
		if !e.readRange(ctx, a, p, from, to, fin.Number, false) {
			break
		}
		p.hi = to
	}
	// back: the history before the first read, newest first
	for budget > 0 && !p.done {
		if p.lo <= max(floor, 1) || e.full(a) {
			p.done = true
			break
		}
		to := p.lo - 1
		from := max(floor, 1)
		if to-from+1 > p.rng {
			from = to - p.rng + 1
		}
		budget--
		if !e.readRange(ctx, a, p, from, to, fin.Number, false) {
			break
		}
		p.lo = from
	}
	// the tail: the blocks past the finalized one, read again at every read
	if base := max(p.hi, fin.Number); head.Number > base {
		from := base + 1
		if head.Number-from+1 > p.rng {
			from = head.Number - p.rng + 1
		}
		e.readRange(ctx, a, p, from, head.Number, fin.Number, true)
	}
	e.noteSet(ctx, a)
	a.Read = true
}

// settle orders and bounds what the index keeps, and counts the burns: one past the newest burn's nonce (the
// mailbox's own nonce counts any stranger's dispatch too, ER5, and the signers are asked about burns alone).
func (e *ethereumChain) settle(a *OtherActivity) {
	sort.Slice(a.Headers, func(i, j int) bool { return a.Headers[i].Height > a.Headers[j].Height })
	if len(a.Headers) > activityKeep {
		a.Headers = a.Headers[:activityKeep]
	}
	trimBurns(a.Burns)
	trimDeliveries(a.Deliveries)
	trimProofs(a.Proofs)
	a.Dispatched = 0
	for _, b := range a.Burns {
		a.Dispatched = max(a.Dispatched, uint64(b.Nonce)+1)
	}
	a.Processed = 0 // the mailbox keeps no count of what it processed
	if len(a.eth.hdr) > 1000 {
		a.eth.hdr = map[[32]byte]int64{}
	}
}

// full is whether every kept list is full, so that older events would be trimmed as soon as read.
func (e *ethereumChain) full(a *OtherActivity) bool {
	return len(a.Burns) >= activityKeep && len(a.Deliveries) >= activityKeep && len(a.Headers) >= activityKeep
}

// readRange folds in the events of blocks from … to (tentative: the tail's, taken out again at the next read). It
// reports whether the whole range was folded; a range left is read again. A provider that refuses the range has
// its window halved for the next try.
func (e *ethereumChain) readRange(ctx context.Context, a *OtherActivity, p *ethProgress, from, to, fin uint64, tentative bool) bool {
	c, cancel := withBudget(ctx)
	logs, err := e.logs(c, from, to, e.watched(), ethEvents)
	cancel()
	if err != nil {
		if rangeRefused(err) && p.rng > ethMinRange {
			p.rng = max(ethMinRange, p.rng/2)
			err = fmt.Errorf("%w (the window is %d blocks from now on)", err, p.rng)
		}
		a.fail("the logs", err)
		return false
	}
	// every block that holds a log must be the one the provider holds at that number: a log of a block taken back
	// is no event, and the range is read again
	blocks := map[uint64]ethBlock{}
	var live []ethLog
	for _, l := range logs {
		if l.Removed {
			continue
		}
		if l.Block < from || l.Block > to {
			a.fail("the logs", fmt.Errorf("the provider answered a log of block %d for blocks %d to %d", l.Block, from, to))
			return false
		}
		b, ok := blocks[l.Block]
		if !ok {
			c, cancel := withBudget(ctx)
			b, err = e.canonical(c, l.Block, fin)
			cancel()
			if err != nil {
				a.fail("a block", err)
				return false
			}
			blocks[l.Block] = b
		}
		if b.Hash != l.BlockHash {
			e.forget(l.Block)
			a.fail("the logs", fmt.Errorf("block %d's logs name a block the provider no longer holds at that number (a reorganization); the blocks are read again", l.Block))
			return false
		}
		live = append(live, l)
	}
	sort.SliceStable(live, func(i, j int) bool {
		if live[i].Block != live[j].Block {
			return live[i].Block < live[j].Block
		}
		return live[i].Index < live[j].Index
	})
	mark := func(k string) {
		if tentative {
			p.tent[k] = true
		}
	}
	for i := 0; i < len(live); {
		j := i
		for j < len(live) && live[j].Tx == live[i].Tx {
			j++
		}
		if !e.foldTx(ctx, a, p, live[i:j], blocks[live[i].Block], fin, tentative, mark) {
			return false
		}
		i = j
	}
	return true
}

// foldTx folds in one transaction's logs: its burns, its deliveries (with the message and the proof from the
// delivering call), its headers.
func (e *ethereumChain) foldTx(ctx context.Context, a *OtherActivity, p *ethProgress, logs []ethLog, blk ethBlock, fin uint64, tentative bool, mark func(string)) bool {
	for i, l := range logs {
		if len(l.Topics) == 0 {
			continue
		}
		switch {
		case l.Address == e.mailbox && l.Topics[0] == topicDispatch:
			if b, ok := e.burnOf(l); ok {
				b.Time, b.Finalized = blk.Time, fin
				a.Burns[b.ID] = b
				mark("b:" + b.ID)
			}
		case l.Address == e.mailbox && l.Topics[0] == topicProcessID && len(l.Topics) == 2:
			d, proof, ok := e.delivery(ctx, a, p, logs, i, blk, tentative)
			if !ok {
				return false
			}
			a.Deliveries[d.ID] = d
			mark("d:" + d.ID)
			if proof != nil {
				if _, ok := a.Proofs[d.Hash]; !ok {
					a.Proofs[d.Hash] = *proof
					mark("p:" + d.Hash)
				}
			}
		case l.Address == e.client && l.Topics[0] == topicHeaderRecorded && len(l.Topics) == 3:
			height, err := i64Of(l.Topics[2])
			if err != nil {
				continue
			}
			w, err := wordAt(l.Data, 96)
			proven, err2 := boolWord(w)
			if err != nil || err2 != nil {
				continue
			}
			p.hdr[l.Topics[1]] = height
			e.mu.Lock()
			if len(e.headers) > 500 {
				e.headers = map[[32]byte]ethHeader{}
			}
			e.headers[l.Topics[1]] = ethHeader{Height: height, Time: blk.Time, Block: l.Block, Tx: l.Tx, Proven: proven}
			e.mu.Unlock()
			if proven {
				continue // a header proved at or below the trusted height: a proof's, not the head's
			}
			known := false
			for _, h := range a.Headers {
				known = known || h.Height == height
			}
			if !known {
				a.Headers = append(a.Headers, HeaderEvent{Height: height, Time: blk.Time, Slot: l.Block, Sig: l.Tx})
				mark("h:" + strconv.FormatInt(height, 10))
			}
		}
	}
	return true
}

// delivery reads the ProcessId log at logs[i]: the message id, what it delivered (the Process log just before it
// names the recipient: a token is a mint, HlAdmin a decision), and — from the delivering call's own input — the
// message's amount and recipient, its sha256 (the key a proof is kept under) and its proof. A call that is not the
// mailbox's process of this message (a relayer's batch, say), or one the provider no longer indexes in a final
// block (a node keeps its transaction index for about a year), leaves the logs alone to tell it: the token's
// ReceivedTransferRemote then gives the amount and the recipient, and no proof is shown.
func (e *ethereumChain) delivery(ctx context.Context, a *OtherActivity, p *ethProgress, logs []ethLog, i int, blk ethBlock, tentative bool) (DeliveryEvent, *ProofEvent, bool) {
	l := logs[i]
	id := l.Topics[1]
	d := DeliveryEvent{ID: hex.EncodeToString(id[:]), Sig: l.Tx, Slot: l.Block, Time: blk.Time, Kind: "other", Link: e.TxLink(l.Tx)}
	var tok *ethToken
	for k := i - 1; k >= 0; k-- {
		x := logs[k]
		if x.Address == e.mailbox && len(x.Topics) == 4 && x.Topics[0] == topicProcess {
			if rcpt, ok := addrOf32(x.Topics[3]); ok {
				switch {
				case rcpt == e.admin:
					d.Kind = "decision"
				case e.tokenByAddr(rcpt) != nil:
					tok = e.tokenByAddr(rcpt)
					d.Kind, d.Token = "mint", tok.Symbol
				}
			}
			break
		}
	}
	c, cancel := withBudget(ctx)
	pr, err := e.processInput(c, l.Tx, id, !tentative)
	cancel()
	if err != nil {
		a.fail("a delivery's transaction", err)
		return d, nil, false
	}
	if pr == nil || pr.msg == nil {
		if tok != nil { // the logs alone: the token's ReceivedTransferRemote after it
			for k := i + 1; k < len(logs); k++ {
				x := logs[k]
				if x.Address == tok.addr && len(x.Topics) == 3 && x.Topics[0] == topicReceived {
					if w, err := wordAt(x.Data, 0); err == nil {
						if amt, err := u64Of(w); err == nil && amt <= 1<<62 {
							d.Amount, d.To = int64(amt), e.Address(x.Topics[2])
						}
					}
					break
				}
			}
		}
		return d, nil, true
	}
	sum := sha256.Sum256(pr.msg)
	d.Hash = hex.EncodeToString(sum[:])
	if m, err := hl.ParseMessage(pr.msg); err == nil && tok != nil {
		if to, amt, _, err := hl.ParseTokenMessage(m.Body); err == nil && amt <= 1<<62 {
			d.Amount, d.To = int64(amt), e.Address(to)
		}
	}
	if pr.meta == nil {
		return d, nil, true
	}
	proof := &ProofEvent{Ring: true, Total: pr.meta.total, Index: pr.meta.index, Aunts: pr.meta.aunts, Time: blk.Time, Sig: l.Tx}
	proof.Height = e.headerHeight(ctx, p, pr.meta.header)
	return d, proof, true
}

// headerHeight is the height of a header the client recorded, by its hash: from the logs read, else the client's
// own entry for it (0 when neither says).
func (e *ethereumChain) headerHeight(ctx context.Context, p *ethProgress, hash [32]byte) int64 {
	if h, ok := p.hdr[hash]; ok {
		return h
	}
	e.mu.Lock()
	h, ok := e.headers[hash]
	e.mu.Unlock()
	if ok {
		return h.Height
	}
	c, cancel := withBudget(ctx)
	defer cancel()
	b, err := e.call(c, e.client, calldata(selEntry, hash), latest)
	if err != nil {
		return 0
	}
	w, err := wordAt(b, 32) // Entry (uint64 epoch, int64 height, bytes32 lastResultsHash)
	if err != nil {
		return 0
	}
	height, err := i64Of(w)
	if err != nil || height <= 0 {
		return 0
	}
	p.hdr[hash] = height
	return height
}

// processRead is a delivering call as read: the message it carried and its proof (nil when the call is not the
// mailbox's process of the message).
type processRead struct {
	msg  []byte
	meta *proofMeta
}

// proofMeta is GnoLightClient.verify's metadata: abi.encode(bytes32 headerHash, uint32 total, uint32 index,
// bytes32[] aunts, bytes leaf) (eth/bridge/PLAN.md §13).
type proofMeta struct {
	header       [32]byte
	total, index uint32
	aunts        int
}

func decodeProofMeta(b []byte) (*proofMeta, error) {
	hw, err := wordAt(b, 0)
	if err != nil {
		return nil, err
	}
	var f [2]uint64
	for i := range f {
		w, err := wordAt(b, 32*uint64(i+1))
		if err == nil {
			f[i], err = u64Of(w)
		}
		if err != nil || f[i] > 1<<32-1 {
			return nil, errors.New("a proof's total or index out of range")
		}
	}
	_, n, err := dynAt(b, 0, 96)
	if err != nil || n > 64 {
		return nil, errors.New("a proof's aunts do not read")
	}
	if _, err := bytesAt(b, 0, 128); err != nil {
		return nil, err
	}
	return &proofMeta{header: hw, total: uint32(f[0]), index: uint32(f[1]), aunts: int(n)}, nil
}

// processInput reads a delivering transaction once: when it is the mailbox's process(metadata, message) of this
// very message, the message and its proof. final says the transaction's block is final: a provider that holds no
// such transaction then has dropped its index (the logs alone tell the delivery); in the tail it may only lag, and
// the transaction is asked for again.
func (e *ethereumChain) processInput(ctx context.Context, tx string, id [32]byte, final bool) (*processRead, error) {
	e.mu.Lock()
	pr, ok := e.txs[tx]
	e.mu.Unlock()
	if ok {
		return pr, nil
	}
	pr = &processRead{}
	input, to, err := e.txInput(ctx, tx)
	if errors.Is(err, errNoTxIndex) && final {
		input, to, err = nil, ethAddr{}, nil
	}
	if err != nil {
		return nil, err
	}
	if to == e.mailbox && len(input) >= 4 && [4]byte(input[:4]) == selProcess {
		args := input[4:]
		meta, err1 := bytesAt(args, 0, 0)
		msg, err2 := bytesAt(args, 0, 32)
		if err1 == nil && err2 == nil && hl.ID(msg) == id {
			pr.msg = msg
			pr.meta, _ = decodeProofMeta(meta) // a proof that does not read is shown as none; the delivery stands
		}
	}
	e.mu.Lock()
	if len(e.txs) > 500 {
		e.txs = map[string]*processRead{}
	}
	e.txs[tx] = pr
	e.mu.Unlock()
	return pr, nil
}

// txInput is a transaction's input and its destination.
func (e *ethereumChain) txInput(ctx context.Context, tx string) ([]byte, ethAddr, error) {
	var raw json.RawMessage
	if err := e.c.Call(ctx, "eth_getTransactionByHash", []any{tx}, &raw); err != nil {
		return nil, ethAddr{}, err
	}
	if isNull(raw) {
		return nil, ethAddr{}, fmt.Errorf("%w %s", errNoTxIndex, tx)
	}
	var j ethTxJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, ethAddr{}, fmt.Errorf("eth_getTransactionByHash: %v", err)
	}
	input, err := parseData(j.Input)
	if err != nil {
		return nil, ethAddr{}, fmt.Errorf("eth_getTransactionByHash: %v", err)
	}
	var to ethAddr
	if tb, err := parseData(j.To); err == nil && len(tb) == 20 {
		copy(to[:], tb)
	}
	return input, to, nil
}

// errNoTxIndex is a provider answering null for a transaction its logs name.
var errNoTxIndex = errors.New("the provider holds no transaction")

// noteSet keeps the set the newest accepted header's update carried (the client stores hashes alone; the update's
// calldata is the set, checked against the stored hash by the call that succeeded), for the status's picture of
// the validators. A failure leaves the last set read.
func (e *ethereumChain) noteSet(ctx context.Context, a *OtherActivity) {
	if len(a.Headers) == 0 {
		return
	}
	newest := a.Headers[0]
	for _, h := range a.Headers {
		if h.Height > newest.Height {
			newest = h
		}
	}
	e.mu.Lock()
	same := e.setTx == newest.Sig
	e.mu.Unlock()
	if same || newest.Sig == "" {
		return
	}
	c, cancel := withBudget(ctx)
	input, to, err := e.txInput(c, newest.Sig)
	cancel()
	if err != nil {
		a.fail("the validators' set", err)
		return
	}
	powers, err := decodeUpdateSet(input)
	if to != e.client || err != nil {
		powers = nil // not the client's update (a staged call): the set is not known from it
	}
	e.mu.Lock()
	e.set, e.setTx = powers, newest.Sig
	e.mu.Unlock()
}

// decodeUpdateSet reads the set an update(bytes[] headerLeaves, Commit commit, Validator[] set, Validator[]
// nextSet) call carries: each validator (bytes32 pubkey, int64 power), static, inline in the array.
func decodeUpdateSet(input []byte) ([]int64, error) {
	if len(input) < 4 || [4]byte(input[:4]) != selUpdate {
		return nil, errors.New("not an update")
	}
	args := input[4:]
	start, n, err := dynAt(args, 0, 64)
	if err != nil {
		return nil, err
	}
	if n == 0 || n > 512 {
		return nil, fmt.Errorf("a set of %d validators", n)
	}
	out := make([]int64, 0, n)
	for i := uint64(0); i < n; i++ {
		w, err := wordAt(args, start+64*i+32)
		if err != nil {
			return nil, err
		}
		p, err := i64Of(w)
		if err != nil || p <= 0 {
			return nil, errors.New("a validator's power out of range")
		}
		out = append(out, p)
	}
	return out, nil
}

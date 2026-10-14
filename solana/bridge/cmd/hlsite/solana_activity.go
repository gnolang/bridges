package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The far side's activity (M7): what Solana's own records say happened, read lazily and once. One read asks for
// three accounts at once — the client's state, the mailbox's outbox and inbox — and lists an account's newest
// transactions only when its counter moved (or a read left work): the outbox's for the burns toward gno.land, the
// inbox's for the deliveries, the client's for the headers it accepted and the proofs staged under them. Every
// transaction is read once, oldest first (a finish after its stage), and kept by its signature: the first read
// takes the whole listing, every later one at most activityReads new ones; a public provider sees a handful of
// calls a minute.

const (
	activityKeep  = 40 // events of each kind kept
	activityList  = 20 // signatures listed per account and read
	clientList    = 60 // the client's: four transactions a header, three a proof — two hours of them
	activityReads = 15 // new transactions read per account and read after the first; the rest wait for the next
)

func (a *OtherActivity) init() {
	if a.seen == nil {
		a.seen, a.stages, a.again = map[string]bool{}, map[string]int64{}, map[string]bool{}
		a.Proofs, a.Deliveries, a.Burns = map[string]ProofEvent{}, map[string]DeliveryEvent{}, map[string]Burn{}
	}
}

// Activity folds what is new on Solana into a.
func (s *solanaChain) Activity(ctx context.Context, a *OtherActivity, pending bool) {
	a.init()
	a.Errors = nil
	defer a.summarize()
	mb, client := s.progs.MailboxProgram(), s.progs.LightClient()
	trusted, dispatched, processed := a.Trusted, a.Dispatched, a.Processed
	c, cancel := withBudget(ctx)
	res, err := sol.GetMultipleChunked(c, s.c, []solana.PublicKey{mb.Outbox(), mb.Inbox()}, &rpc.GetMultipleAccountsOpts{Commitment: rpc.CommitmentFinalized})
	cancel()
	if err == nil && len(res.Value) != 2 {
		err = errors.New("the provider answered another number of accounts")
	}
	if err != nil {
		a.fail("the mailbox", err)
		return
	}
	if v := res.Value[0]; v != nil {
		if ob, err := sol.ParseOutbox(v.Data.GetBinary()); err == nil {
			dispatched = ob.Count
		}
	}
	if v := res.Value[1]; v != nil {
		if ib, err := sol.ParseInbox(v.Data.GetBinary()); err == nil {
			processed = ib.ProcessedCount
		}
	}
	// the client's head alone: its trusted height (the whole account, its ring among it, is read only when a proof
	// names a header by its ring hash)
	c, cancel = withBudget(ctx)
	head, err := s.c.GetAccountInfoWithOpts(c, client.State(), &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentFinalized, DataSlice: &rpc.DataSlice{Offset: ptr(uint64(0)), Length: ptr(uint64(sol.HeadLen))}})
	cancel()
	if err != nil {
		a.fail("the light client", err)
		return
	}
	if head.Value != nil {
		if h, err := sol.ParseClientHead(head.Value.Data.GetBinary()); err == nil {
			trusted = h.TrustedHeight
		}
	}
	ring := func() map[[32]byte]int64 { // the client's ring: a header hash it holds, by height
		out := map[[32]byte]int64{}
		c, cancel := withBudget(ctx)
		defer cancel()
		data, ok, err := s.account(c, client.State(), rpc.CommitmentFinalized)
		if err != nil || !ok {
			return out
		}
		if _, _, entries, err := sol.ParseClientState(data); err == nil {
			for _, e := range entries {
				out[e.Hash] = e.Height
			}
		}
		return out
	}
	first := !a.Read
	if first || dispatched != a.Dispatched || a.again["burns"] {
		a.again["burns"] = !s.readBurns(ctx, a, mb.Outbox())
	}
	delivered := len(a.Deliveries)
	if first || processed != a.Processed || a.again["deliveries"] {
		a.again["deliveries"] = !s.readDeliveries(ctx, a, mb.Inbox())
	}
	if first || trusted != a.Trusted || pending || len(a.Deliveries) != delivered || a.again["client"] {
		a.again["client"] = !s.readClient(ctx, a, client, trusted, ring)
	}
	a.Trusted, a.Dispatched, a.Processed, a.Read = trusted, dispatched, processed, true
}

// fail notes a read that failed; a read's failures of one kind are one line (summarize), not one a transaction.
func (a *OtherActivity) fail(what string, err error) {
	if a.failed == nil {
		a.failed = map[string]*failure{}
	}
	f := a.failed[what]
	if f == nil {
		f = &failure{}
		a.failed[what] = f
		a.failOrder = append(a.failOrder, what)
	}
	f.n++
	f.last = agent.Redact(err.Error())
}

type failure struct {
	n    int
	last string
}

// summarize turns the read's failures into its Errors.
func (a *OtherActivity) summarize() {
	for _, w := range a.failOrder {
		f := a.failed[w]
		if f.n == 1 {
			a.Errors = append(a.Errors, w+": "+f.last)
		} else {
			a.Errors = append(a.Errors, fmt.Sprintf("%s: %d could not be read (%s); they are read again at the next refresh", w, f.n, f.last))
		}
	}
	a.failed, a.failOrder = nil, nil
}

// unread are the successful transactions of an account's newest n that are not folded in yet, oldest first — all
// of them on the first read, at most activityReads after it (more says some were left for the next read); a
// failed transaction is marked read, as nothing of it stands.
func (s *solanaChain) unread(ctx context.Context, a *OtherActivity, k solana.PublicKey, n int) (todo []*rpc.TransactionSignature, more bool, err error) {
	c, cancel := withBudget(ctx)
	defer cancel()
	sigs, err := s.c.GetSignaturesForAddressWithOpts(c, k, &rpc.GetSignaturesForAddressOpts{Limit: &n, Commitment: rpc.CommitmentFinalized})
	if err != nil {
		return nil, false, err
	}
	for i := len(sigs) - 1; i >= 0; i-- { // the provider lists newest first
		sg := sigs[i]
		if sg == nil || a.seen[sg.Signature.String()] {
			continue
		}
		if sg.Err != nil {
			a.seen[sg.Signature.String()] = true
			continue
		}
		todo = append(todo, sg)
	}
	if a.Read && len(todo) > activityReads {
		todo, more = todo[:activityReads], true
	}
	return todo, more, nil
}

// readBurns folds the burns of the outbox's newest transactions in (Burn reads each as the tracker does: the
// dispatch record the mailbox wrote in that slot). It reports whether nothing was left for the next read.
func (s *solanaChain) readBurns(ctx context.Context, a *OtherActivity, outbox solana.PublicKey) bool {
	todo, more, err := s.unread(ctx, a, outbox, activityList)
	if err != nil {
		a.fail("the burns", err)
		return false
	}
	complete := !more
	type read struct {
		burns []Burn
		err   error
	}
	reads := make([]read, len(todo))
	inParallel(len(todo), func(i int) {
		c, cancel := withBudget(ctx)
		defer cancel()
		reads[i].burns, reads[i].err = s.Burn(c, todo[i].Signature.String())
	})
	for i, sg := range todo {
		burns, err := reads[i].burns, reads[i].err
		if err != nil && !errors.Is(err, errNoSuchTx) {
			a.fail("a burn's transaction", err)
			complete = false
			continue
		}
		for _, b := range burns {
			a.Burns[b.ID] = b
		}
		a.seen[sg.Signature.String()] = true
	}
	trimBurns(a.Burns)
	return complete
}

// readDeliveries folds in the messages the inbox's newest transactions processed, each read from its process
// instruction's own bytes: a route's message is a mint, the admin program's a decision.
func (s *solanaChain) readDeliveries(ctx context.Context, a *OtherActivity, inbox solana.PublicKey) bool {
	todo, more, err := s.unread(ctx, a, inbox, activityList)
	if err != nil {
		a.fail("the deliveries", err)
		return false
	}
	complete := !more
	mailbox := s.progs.Mailbox
	txs, errs := s.readTxs(ctx, todo)
	for i, sg := range todo {
		tx, err := txs[i], errs[i]
		if err != nil {
			a.fail("a delivery's transaction", err)
			complete = false
			continue
		}
		for _, ix := range tx.ixs {
			if !ix.program.Equals(mailbox) || len(ix.data) == 0 || ix.data[0] != 1 {
				continue
			}
			raw, ok := processMessage(ix.data)
			if !ok {
				continue
			}
			if d, ok := s.delivery(raw, tx, sg.Signature.String()); ok {
				a.Deliveries[d.ID] = d
			}
		}
		a.seen[sg.Signature.String()] = true
	}
	trimDeliveries(a.Deliveries)
	return complete
}

// delivery reads one processed message: its id, what it was (a route's transfer, a decision) and its time.
func (s *solanaChain) delivery(raw []byte, tx *readTxn, sig string) (DeliveryEvent, bool) {
	m, err := hl.ParseMessage(raw)
	if err != nil {
		return DeliveryEvent{}, false
	}
	id, sum := hl.ID(raw), sha256.Sum256(raw)
	d := DeliveryEvent{ID: hex.EncodeToString(id[:]), Sig: sig, Slot: tx.slot, Time: tx.time, Kind: "other", Hash: hex.EncodeToString(sum[:]), Link: s.TxLink(sig)}
	recipient := solana.PublicKeyFromBytes(m.Recipient[:])
	switch {
	case recipient.Equals(s.progs.Admin):
		d.Kind = "decision"
	default:
		for _, t := range s.cfg.Tokens {
			if p, err := solana.PublicKeyFromBase58(t.Program); err == nil && p.Equals(recipient) {
				d.Kind, d.Token = "mint", t.Symbol
				if to, amt, _, err := hl.ParseTokenMessage(m.Body); err == nil && amt <= 1<<62 {
					d.Amount, d.To = int64(amt), solana.PublicKeyFromBytes(to[:]).String()
				}
			}
		}
	}
	return d, true
}

// readClient folds in the headers the client accepted (an update's finish, its height from the update's
// StageInit) and the proofs staged (a proof's StageInit: the message's hash, the header, the tree), oldest first
// so a finish finds its stage. A finish whose StageInit is older than the listing is the trusted header when it
// is the newest finish of all; otherwise its height is unknown and it is passed over.
func (s *solanaChain) readClient(ctx context.Context, a *OtherActivity, client sol.LightClient, trusted uint64, ringOf func() map[[32]byte]int64) bool {
	var ring map[[32]byte]int64 // read once, the first time a proof names a header by its ring hash
	todo, more, err := s.unread(ctx, a, client.State(), clientList)
	if err != nil {
		a.fail("the light client's transactions", err)
		return false
	}
	complete := !more
	// the newest finish of the batch whose stage was never seen: the trusted header, when nothing newer is known
	var orphan *HeaderEvent
	txs, errs := s.readTxs(ctx, todo)
	for i, sg := range todo {
		tx, err := txs[i], errs[i]
		if err != nil {
			a.fail("a light client's transaction", err)
			complete = false
			continue
		}
		sig := sg.Signature.String()
		for _, ix := range tx.ixs {
			if !ix.program.Equals(client.Program) || len(ix.data) == 0 {
				continue
			}
			switch ix.data[0] {
			case 1: // StageInit: an update's header, or a proof
				st, ok, err := sol.DecodeStageInit(ix.data)
				if err != nil || !ok || len(ix.accounts) < 2 {
					continue
				}
				switch st.Kind {
				case sol.StageKindUpdate:
					if st.Header != nil {
						a.stages[ix.accounts[1].String()] = st.Header.Height
					}
				case sol.StageKindProof:
					key := hex.EncodeToString(st.MessageHash[:])
					if _, ok := a.Proofs[key]; ok {
						continue // the first staging is the one the delivery used; a relayer may stage again after
					}
					p := ProofEvent{Ring: st.Header == nil, Time: tx.time, Sig: sig}
					if st.Header != nil {
						p.Height = st.Header.Height
					} else if ring == nil {
						ring = ringOf()
					}
					if h, ok := ring[st.RingHash]; ok && st.Header == nil {
						p.Height = h // a header the client held: its height from the ring
					}
					if st.Proof != nil {
						p.Total, p.Index, p.Aunts = st.Proof.Total, st.Proof.Index, len(st.Proof.Aunts)
					}
					a.Proofs[key] = p
				}
			case 5: // UpdateFinish: the header is accepted
				if len(ix.accounts) == 0 {
					continue
				}
				stage := ix.accounts[0].String()
				if h, ok := a.stages[stage]; ok {
					a.Headers = append(a.Headers, HeaderEvent{Height: h, Time: tx.time, Slot: tx.slot, Sig: sig})
					orphan = nil
				} else {
					orphan = &HeaderEvent{Time: tx.time, Slot: tx.slot, Sig: sig}
				}
				delete(a.stages, stage)
			case 6: // StageClose: an abandoned stage
				if len(ix.accounts) > 0 {
					delete(a.stages, ix.accounts[0].String())
				}
			}
		}
		a.seen[sig] = true
	}
	if orphan != nil && trusted > 0 && (len(a.Headers) == 0 || maxHeight(a.Headers) < int64(trusted)) {
		orphan.Height = int64(trusted) // the newest finish set the trusted height this read saw
		a.Headers = append(a.Headers, *orphan)
	}
	sort.Slice(a.Headers, func(i, j int) bool { return a.Headers[i].Height > a.Headers[j].Height })
	if len(a.Headers) > activityKeep {
		a.Headers = a.Headers[:activityKeep]
	}
	trimProofs(a.Proofs)
	if len(a.stages) > activityKeep {
		a.stages = map[string]int64{} // stages nobody finished: forgotten
	}
	return complete
}

// readTxn is a finalized transaction as the activity reads it: the top-level instructions with their accounts
// resolved, its slot and its block time.
type readTxn struct {
	ixs  []rawIx
	slot uint64
	time int64
}

type rawIx struct {
	program  solana.PublicKey
	accounts []solana.PublicKey
	data     []byte
}

func (s *solanaChain) readTx(ctx context.Context, sig solana.Signature) (*readTxn, error) {
	v := maxTxVersion
	c, cancel := withBudget(ctx)
	defer cancel()
	res, err := s.c.GetTransaction(c, sig, &rpc.GetTransactionOpts{Encoding: solana.EncodingBase64, Commitment: rpc.CommitmentFinalized, MaxSupportedTransactionVersion: &v})
	if err != nil {
		return nil, err
	}
	if res == nil || res.Transaction == nil {
		return nil, errNoSuchTx
	}
	tx, err := res.Transaction.GetTransaction()
	if err != nil {
		return nil, err
	}
	keys := append(solana.PublicKeySlice{}, tx.Message.AccountKeys...)
	if res.Meta != nil {
		keys = append(keys, res.Meta.LoadedAddresses.Writable...)
		keys = append(keys, res.Meta.LoadedAddresses.ReadOnly...)
	}
	out := &readTxn{slot: res.Slot}
	if res.BlockTime != nil {
		out.time = int64(*res.BlockTime)
	}
	for _, ci := range tx.Message.Instructions {
		if int(ci.ProgramIDIndex) >= len(keys) {
			continue
		}
		ix := rawIx{program: keys[ci.ProgramIDIndex], data: []byte(ci.Data)}
		for _, k := range ci.Accounts {
			if int(k) < len(keys) {
				ix.accounts = append(ix.accounts, keys[k])
			}
		}
		out.ixs = append(out.ixs, ix)
	}
	return out, nil
}

// processMessage is the message an InboxProcess carries: its data is the variant, then the metadata and the
// message, each a borsh vector (a little-endian u32 length, then the bytes).
func processMessage(data []byte) ([]byte, bool) {
	if len(data) < 1 || data[0] != 1 {
		return nil, false
	}
	rest := data[1:]
	vec := func() ([]byte, bool) {
		if len(rest) < 4 {
			return nil, false
		}
		n := binary.LittleEndian.Uint32(rest)
		if uint64(n) > uint64(len(rest)-4) {
			return nil, false
		}
		v := rest[4 : 4+n]
		rest = rest[4+n:]
		return v, true
	}
	if _, ok := vec(); !ok {
		return nil, false
	}
	return vec()
}

// The kept maps are bounded: the newest activityKeep by time.

func trimBurns(m map[string]Burn) {
	if len(m) <= activityKeep {
		return
	}
	type kv struct {
		k string
		t int64
	}
	var all []kv
	for k, v := range m {
		all = append(all, kv{k, v.Time})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t > all[j].t })
	for _, x := range all[activityKeep:] {
		delete(m, x.k)
	}
}

func trimDeliveries(m map[string]DeliveryEvent) {
	if len(m) <= activityKeep {
		return
	}
	type kv struct {
		k string
		t int64
	}
	var all []kv
	for k, v := range m {
		all = append(all, kv{k, v.Time})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t > all[j].t })
	for _, x := range all[activityKeep:] {
		delete(m, x.k)
	}
}

func trimProofs(m map[string]ProofEvent) {
	if len(m) <= activityKeep {
		return
	}
	type kv struct {
		k string
		t int64
	}
	var all []kv
	for k, v := range m {
		all = append(all, kv{k, v.Time})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t > all[j].t })
	for _, x := range all[activityKeep:] {
		delete(m, x.k)
	}
}

func ptr[T any](v T) *T { return &v }

// txReaders is how many transactions a read asks for at once: a public provider's latency is the cold read's cost
// (a second a call is seen), and four at a time stays well inside its limits.
const txReaders = 4

// inParallel runs f for 0 … n−1, txReaders at a time, and waits for all.
func inParallel(n int, f func(i int)) {
	sem := make(chan struct{}, txReaders)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			f(i)
		}(i)
	}
	wg.Wait()
}

// readTxs reads the listed transactions, txReaders at a time, in the listing's order.
func (s *solanaChain) readTxs(ctx context.Context, todo []*rpc.TransactionSignature) ([]*readTxn, []error) {
	txs, errs := make([]*readTxn, len(todo)), make([]error, len(todo))
	inParallel(len(todo), func(i int) { txs[i], errs[i] = s.readTx(ctx, todo[i].Signature) })
	return txs, errs
}

func maxHeight(hs []HeaderEvent) int64 {
	m := int64(0)
	for _, h := range hs {
		m = max(m, h.Height)
	}
	return m
}

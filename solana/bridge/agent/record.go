package agent

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The client's header record (PLAN.md §6.2): one counter and one running hash in its state over every header it
// accepted as an update or verified for a fresh proof — (hash, height, slot) each — the record itself being the
// staging transactions' instruction data, which the watch rebuilds here: every transaction that touched the
// state PDA, its StageInit (the header), its UpdateFinish or the mailbox's process that consumed a proof stage
// (the entry at that slot), its Init and Reset. The rebuilt chain is then held against the own node's headers
// at those heights (the fork rule: the cut is the Solana slot S of the first header that differs, §6.4) and
// against the state's own counter and hash (an unfillable gap).

// RecordEntry is one header the client accepted or proved.
type RecordEntry struct {
	Hash   string `json:"hash"`
	Height int64  `json:"height"`
	Slot   uint64 `json:"slot"`
	Kind   string `json:"kind"` // init, update, proof
	Sig    string `json:"sig"`
}

// Mismatch is the first header of the record that differs from gno.land's.
type Mismatch struct {
	Height int64  `json:"height"`
	Client string `json:"client"` // the header hash the client accepted
	Chain  string `json:"chain"`  // the own node's, "" when the chain has no such height
	Slot   uint64 `json:"slot"`   // S: the cut of §6.4
	Sig    string `json:"sig"`
}

type stageRef struct {
	Hash   string `json:"hash"`
	Height int64  `json:"height"`
	Vals   string `json:"vals,omitempty"` // the header's ValidatorsHash: a change of it at an accepted update is the set switch that clears the ring
}

// ClientRecord is the rebuilt record and the scan's position, kept in the machine's state file. It mirrors the
// client's ring of accepted and proven header hashes (S-10: a header-based proof is a record entry only when
// its header is new to the client — an accepted update pushes its header, a fresh proof records and pushes, a
// repeated proof of a header the ring holds records nothing; the ring is cleared at a set switch, and evicts
// past sol.Ring entries).
type ClientRecord struct {
	Cursor   string `json:"cursor"` // the newest signature folded in
	ScanSlot uint64 `json:"scanSlot"`
	Count    uint64 `json:"count"`
	Hash     string `json:"hash"`
	// FromSlot is the checkpoint a fresh install started from (G29: the client's own counter and running hash at
	// a slot, taken as the base, so a provider without the full history can build the record forward): every
	// transaction at or below it is passed over unread; 0 for a record built from the deploy.
	FromSlot  uint64              `json:"fromSlot,omitempty"`
	Stages    map[string]stageRef `json:"stages"`  // stage PDA → the header it carries (updates and proofs by header)
	Recent    []RecordEntry       `json:"recent"`  // the last entries, for the status
	Checked   uint64              `json:"checked"` // entries compared with the own node so far
	Mismatch  *Mismatch           `json:"mismatch,omitempty"`
	Gaps      int                 `json:"gaps"`               // finishes whose stage was never seen
	Ring      []string            `json:"ring,omitempty"`     // the client's ring as rebuilt: header hashes, oldest first
	LastVals  string              `json:"lastVals,omitempty"` // the last accepted update's ValidatorsHash
	unchecked []RecordEntry
}

const recordRecent = 64

// ringHas says whether the mirrored ring holds a header hash.
func (r *ClientRecord) ringHas(hash string) bool {
	for _, h := range r.Ring {
		if strings.EqualFold(h, hash) {
			return true
		}
	}
	return false
}

// ringPush adds a header hash to the mirrored ring, evicting the oldest past the client's capacity.
func (r *ClientRecord) ringPush(hash string) {
	r.Ring = append(r.Ring, hash)
	if len(r.Ring) > sol.Ring {
		r.Ring = r.Ring[len(r.Ring)-sol.Ring:]
	}
}

func (r *ClientRecord) init() {
	if r.Stages == nil {
		r.Stages = map[string]stageRef{}
	}
	if r.Hash == "" {
		r.Hash = hex.EncodeToString(make([]byte, 32))
	}
}

func (r *ClientRecord) fold(e RecordEntry) {
	prev, _ := hex.DecodeString(r.Hash)
	hash, _ := hex.DecodeString(e.Hash)
	var height, slot [8]byte
	binary.LittleEndian.PutUint64(height[:], uint64(e.Height))
	binary.LittleEndian.PutUint64(slot[:], e.Slot)
	h := sha256.New()
	h.Write(prev)
	h.Write(hash)
	h.Write(height[:])
	h.Write(slot[:])
	r.Hash = hex.EncodeToString(h.Sum(nil))
	r.Count++
	r.Recent = append(r.Recent, e)
	if len(r.Recent) > recordRecent {
		r.Recent = r.Recent[len(r.Recent)-recordRecent:]
	}
	r.unchecked = append(r.unchecked, e)
}

// txInstruction is one top-level instruction of a transaction as the record reads it.
type txInstruction struct {
	Program  solana.PublicKey
	Accounts []solana.PublicKey
	Data     []byte
}

// Fold reads one successful transaction's instructions at a slot (in order) into the record.
func (r *ClientRecord) Fold(client sol.LightClient, mailbox solana.PublicKey, ixs []txInstruction, allKeys []solana.PublicKey, slot uint64, sig string) {
	r.init()
	for _, ix := range ixs {
		switch {
		case ix.Program.Equals(client.Program) && len(ix.Data) > 0:
			switch ix.Data[0] {
			case 0: // Init: the trusted header it installs; the ring starts empty (no reset exists since M2j)
				height, hash, ok, err := sol.DecodeTrustedHeader(ix.Data)
				if ok && err == nil {
					r.fold(RecordEntry{Hash: hex.EncodeToString(hash[:]), Height: int64(height), Slot: slot, Kind: "init", Sig: sig})
					r.Ring, r.LastVals = nil, ""
				}
			case 1: // StageInit
				a, ok, err := sol.DecodeStageInit(ix.Data)
				if err == nil && ok && a.Header != nil && len(ix.Accounts) > 1 {
					h := a.Header.Hash()
					r.Stages[ix.Accounts[1].String()] = stageRef{Hash: hex.EncodeToString(h[:]), Height: a.Header.Height, Vals: hex.EncodeToString(a.Header.ValidatorsHash)}
				}
			case 5: // UpdateFinish: the header is recorded and joins the ring; a set switch clears the ring first
				if len(ix.Accounts) > 0 {
					key := ix.Accounts[0].String()
					if ref, ok := r.Stages[key]; ok {
						if r.LastVals != "" && ref.Vals != "" && ref.Vals != r.LastVals {
							r.Ring = nil
						}
						r.fold(RecordEntry{Hash: ref.Hash, Height: ref.Height, Slot: slot, Kind: "update", Sig: sig})
						r.ringPush(ref.Hash)
						r.LastVals = ref.Vals
						delete(r.Stages, key)
					} else {
						r.Gaps++
					}
				}
			case 6: // StageClose
				if len(ix.Accounts) > 0 {
					delete(r.Stages, ix.Accounts[0].String())
				}
			}
		case ix.Program.Equals(mailbox) && len(ix.Data) > 0 && ix.Data[0] == 1: // InboxProcess: a proof stage consumed
			for _, k := range allKeys {
				if ref, ok := r.Stages[k.String()]; ok {
					if r.ringHas(ref.Hash) {
						continue // a header the client holds: verified, recorded nothing (S-10)
					}
					r.fold(RecordEntry{Hash: ref.Hash, Height: ref.Height, Slot: slot, Kind: "proof", Sig: sig})
					r.ringPush(ref.Hash)
				}
			}
		}
	}
}

// Sync folds every new transaction of the client's state account (finalized, oldest first) into the record, up
// to the snapshot's slot: a transaction finalized above maxSlot is left for the next scan, the cursor stopping
// before it (G14: the snapshot counted the records at its slot; a header landing every tick under a steady flow
// would otherwise show as one entry too many on every poll and flag a gap). maxSlot 0 folds everything. A scan
// that folded every listed transaction at or below maxSlot stands at the snapshot (ScanSlot = maxSlot, G22):
// the record is complete there, so Consistent can call a counter above it an unfillable gap; a scan that errored
// midway keeps the last folded slot and stays "behind".
func (r *ClientRecord) Sync(ctx context.Context, c solReader, client sol.LightClient, mailbox solana.PublicKey, maxSlot uint64) error {
	r.init()
	sigs, err := signaturesSince(ctx, c, client.State(), r.Cursor)
	if err != nil {
		return err
	}
	for _, s := range sigs {
		if maxSlot != 0 && s.Slot > maxSlot {
			break
		}
		switch {
		case s.Err != nil:
		case s.Slot > r.FromSlot:
			ixs, keys, err := transactionInstructions(ctx, c, s.Signature)
			if err != nil {
				return fmt.Errorf("transaction %s: %w", s.Signature, err)
			}
			r.Fold(client, mailbox, ixs, keys, s.Slot, s.Signature.String())
		case s.Slot+recordSeedSlots > r.FromSlot:
			// at or below the checkpoint: counted in its base already (G29); the stages opened shortly before it
			// are remembered, so a finish or a process after the checkpoint finds its header (best effort: a
			// transaction the provider no longer serves is passed over)
			if ixs, _, err := transactionInstructions(ctx, c, s.Signature); err == nil {
				r.foldStages(client, ixs)
			}
		}
		r.Cursor = s.Signature.String()
		if s.Slot > r.ScanSlot {
			r.ScanSlot = s.Slot
		}
	}
	if maxSlot > r.ScanSlot {
		r.ScanSlot = maxSlot // complete up to the snapshot (G22)
	}
	return nil
}

// recordSeedSlots is how far below a checkpoint the stages are read from the transactions: 50,000 slots, three
// hours at today's 0.218 s a slot (F22: 5,000 was sized at 0.4 s a slot and read "≈33 minutes" — 18 minutes in
// fact — while the updater resumes a stage a stop left behind at any age, so a stage opened before the window
// and finished after the checkpoint was a false "unfillable gap"); a few dozen transactions to read. The open
// stage accounts themselves seed the record too, whatever their age (G37).
const recordSeedSlots = 50_000

// foldStages remembers the stages a transaction opened or closed, recording nothing (the seed of a checkpointed
// record, G29).
func (r *ClientRecord) foldStages(client sol.LightClient, ixs []txInstruction) {
	r.init()
	for _, ix := range ixs {
		if !ix.Program.Equals(client.Program) || len(ix.Data) == 0 {
			continue
		}
		switch ix.Data[0] {
		case 1: // StageInit
			if a, ok, err := sol.DecodeStageInit(ix.Data); err == nil && ok && a.Header != nil && len(ix.Accounts) > 1 {
				h := a.Header.Hash()
				r.Stages[ix.Accounts[1].String()] = stageRef{Hash: hex.EncodeToString(h[:]), Height: a.Header.Height, Vals: hex.EncodeToString(a.Header.ValidatorsHash)}
			}
		case 5, 6: // UpdateFinish, StageClose: the stage is gone
			if len(ix.Accounts) > 0 {
				delete(r.Stages, ix.Accounts[0].String())
			}
		}
	}
}

// StartFrom takes the client's own record at a slot as this record's base (G29: a fresh hlwatch or monitor on
// a provider without the full history never built the record — `signaturesSince("")` listed every transaction
// since the deploy and the oldest answered null on every poll): the counter and the running hash the state
// carries, the ring it holds (the proof rule, S-10) and its current set's hash (a set switch clears the ring),
// everything at or below the slot passed over, the record folded forward from there and checked against the
// chain from there on, as the trusted header is taken. Only an empty record takes a base.
func (r *ClientRecord) StartFrom(records uint64, running [32]byte, slot uint64, ring [][32]byte, setHash [32]byte) error {
	if r.Count != 0 || r.Cursor != "" || r.FromSlot != 0 {
		return fmt.Errorf("the record holds %d entries from a scan at slot %d already: a checkpoint is taken by an empty record only", r.Count, r.ScanSlot)
	}
	r.init()
	r.Count, r.Hash, r.FromSlot, r.ScanSlot = records, hex.EncodeToString(running[:]), slot, slot
	r.Ring = nil
	for _, h := range ring {
		r.ringPush(hex.EncodeToString(h[:]))
	}
	r.LastVals = hex.EncodeToString(setHash[:])
	return nil
}

// signaturesSince are the signatures that touched an account after a cursor, finalized, oldest first.
func signaturesSince(ctx context.Context, c solReader, addr solana.PublicKey, cursor string) ([]*rpc.TransactionSignature, error) {
	var until solana.Signature
	if cursor != "" {
		s, err := solana.SignatureFromBase58(cursor)
		if err != nil {
			return nil, err
		}
		until = s
	}
	var all []*rpc.TransactionSignature
	var before solana.Signature
	limit := 1000
	for {
		page, err := c.GetSignaturesForAddressWithOpts(ctx, addr, &rpc.GetSignaturesForAddressOpts{Limit: &limit, Commitment: rpc.CommitmentFinalized, Until: until, Before: before})
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < limit {
			break
		}
		before = page[len(page)-1].Signature
	}
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return all, nil
}

// transactionInstructions are a finalized transaction's instructions in execution order — each top-level one,
// then the inner instructions it invoked (G9: an instruction of the client sent by CPI is an inner one; read at
// the top level alone the record would miss it and the client's counter run ahead for ever — no program CPIs
// the client since M2j, and the order rule stands for any) — with their accounts resolved, and every account the
// transaction names.
func transactionInstructions(ctx context.Context, c solReader, sig solana.Signature) ([]txInstruction, []solana.PublicKey, error) {
	res, err := getTransaction(ctx, c, sig) // at the pinned version, a build gap named (G28)
	if err != nil {
		return nil, nil, err
	}
	if res == nil || res.Meta == nil {
		return nil, nil, fmt.Errorf("no transaction")
	}
	tx, err := res.Transaction.GetTransaction()
	if err != nil {
		return nil, nil, err
	}
	keys := append(solana.PublicKeySlice{}, tx.Message.AccountKeys...)
	keys = append(keys, res.Meta.LoadedAddresses.Writable...)
	keys = append(keys, res.Meta.LoadedAddresses.ReadOnly...)
	return instructionsInOrder(tx.Message.Instructions, res.Meta.InnerInstructions, keys), keys, nil
}

// instructionsInOrder resolves a transaction's compiled instructions against its key table in execution order:
// top-level instruction i, then the inner instructions recorded under index i, then i+1.
func instructionsInOrder(top []solana.CompiledInstruction, inner []rpc.InnerInstruction, keys []solana.PublicKey) []txInstruction {
	resolve := func(program uint16, accounts []uint16, data []byte) (txInstruction, bool) {
		if int(program) >= len(keys) {
			return txInstruction{}, false
		}
		ix := txInstruction{Program: keys[program], Data: data}
		for _, a := range accounts {
			if int(a) < len(keys) {
				ix.Accounts = append(ix.Accounts, keys[a])
			}
		}
		return ix, true
	}
	var out []txInstruction
	for i, ci := range top {
		if ix, ok := resolve(ci.ProgramIDIndex, ci.Accounts, []byte(ci.Data)); ok {
			out = append(out, ix)
		}
		for _, in := range inner {
			if int(in.Index) != i {
				continue
			}
			for _, ici := range in.Instructions {
				if ix, ok := resolve(ici.ProgramIDIndex, ici.Accounts, []byte(ici.Data)); ok {
					out = append(out, ix)
				}
			}
		}
	}
	return out
}

// Consistent judges the rebuilt record against the client's state at a snapshot slot: ok when the counter and
// the running hash agree; "behind" when the scan has not reached the snapshot yet; otherwise what differs.
func (r *ClientRecord) Consistent(records uint64, running [32]byte, snapshotSlot uint64) (ok bool, why string) {
	if r.Count == records && r.Hash == hex.EncodeToString(running[:]) {
		return true, ""
	}
	if r.ScanSlot < snapshotSlot && r.Count <= records {
		return false, fmt.Sprintf("behind: %d of %d entries rebuilt, the scan at slot %d, the snapshot at %d", r.Count, records, r.ScanSlot, snapshotSlot)
	}
	if r.Count < records {
		return false, fmt.Sprintf("an unfillable gap: %d entries rebuilt, the client counts %d (%d finishes with no staged header seen)", r.Count, records, r.Gaps)
	}
	if r.Count > records {
		return false, fmt.Sprintf("%d entries rebuilt, the client counts %d", r.Count, records)
	}
	return false, fmt.Sprintf("the running hash differs over %d entries (%s here, %s in the state)", r.Count, r.Hash[:16], hex.EncodeToString(running[:8]))
}

// blockHashes is the own node as the record check reads it.
type blockHashes interface {
	BlockHash(ctx context.Context, height int64) (hash string, head int64, err error)
}

// Check holds every entry not yet checked against the own node's headers; the first difference is the fork's
// cut (kept until a human looks). An entry above the own node's head stays unchecked, for the next look (G15:
// a node a few seconds behind the updater's is no fork; a client past the chain is judged by step 2 of the cut,
// with the allowance, a second source and two minutes' persistence).
func (r *ClientRecord) Check(ctx context.Context, chain blockHashes) (*Mismatch, error) {
	for len(r.unchecked) > 0 {
		e := r.unchecked[0]
		hash, head, err := chain.BlockHash(ctx, e.Height)
		if err != nil {
			return r.Mismatch, err
		}
		if hash == "" && e.Height > head {
			return r.Mismatch, nil
		}
		r.unchecked = r.unchecked[1:]
		r.Checked++
		if !strings.EqualFold(hash, e.Hash) && r.Mismatch == nil {
			r.Mismatch = &Mismatch{Height: e.Height, Client: e.Hash, Chain: hash, Slot: e.Slot, Sig: e.Sig}
		}
	}
	return r.Mismatch, nil
}

// Unchecked is how many entries wait for the own node to reach their height.
func (r *ClientRecord) Unchecked() int { return len(r.unchecked) }

func short(s string) string {
	if len(s) > 16 {
		return s[:16] + "…"
	}
	return s
}

func (m *Mismatch) String() string {
	if m.Chain == "" {
		return fmt.Sprintf("the client holds header %d (%s) past the chain's head, verified at slot %d (%s)", m.Height, short(m.Client), m.Slot, m.Sig)
	}
	return fmt.Sprintf("the client holds header %d as %s, gno.land's is %s: the cut is Solana slot %d (%s)", m.Height, short(m.Client), short(m.Chain), m.Slot, m.Sig)
}

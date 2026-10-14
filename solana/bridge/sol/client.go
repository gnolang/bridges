package sol

import (
	"errors"
	"fmt"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// The gno.land light-client ISM (bridge/programs/gno-light-client-ism): its PDAs, its instructions (borsh, by
// variant), its state account (the 64-byte head, the borsh body, the ring) and a staged update or proof — and the
// transactions that stage one, as the programs' testlib does them (tests/testlib/src/client.rs).

var (
	Ed25519Program = solana.MustPublicKeyFromBase58("Ed25519SigVerify111111111111111111111111111")
	SysvarIxs      = solana.MustPublicKeyFromBase58("Sysvar1nstructions1111111111111111111111111")
	SysvarRent     = solana.MustPublicKeyFromBase58("SysvarRent111111111111111111111111111111111")
	SysvarClock    = solana.MustPublicKeyFromBase58("SysvarC1ock11111111111111111111111111111111")
)

const (
	StageKindUpdate = 0
	StageKindProof  = 1
	// MaxLeaf is the results leaf a proof may carry (§6.2).
	MaxLeaf = 8192
	// Ring is how many accepted header hashes the client remembers for proofs.
	Ring = 256
	// HeadLen is the client state's fixed head.
	HeadLen = 64
	// ClientStateVersion is the layout version the pausable ISM reads at launch.
	ClientStateVersion = 1
	// RingEntryLen is one ring entry: hash, height LE, LastResultsHash.
	RingEntryLen = 72
	// LeafChunk is how much of a results leaf one StageLeaf carries (under the transaction limit).
	LeafChunk = 900
	// MaxGrowth is the most an account grows per instruction.
	MaxGrowth = 10_240
	// PacketLimit is Solana's transaction size: one packet of 1,232 bytes, which every staged transaction must
	// fit (F6: the program-test harness enforces none, so only the builders and their test hold the line).
	PacketLimit = 1232
	// NextSetChunk is the most validators one StageNextSet appends (its transaction's room: 40 bytes each,
	// after the compute-budget instruction, the three accounts and the signature; 25 made 1,284 bytes, F6).
	NextSetChunk = 23
	// PrecommitChunk is the most precommit slots one StagePrecommits carries beside a StageInit (20 bytes each;
	// the init transaction with a header, its BlockID and the budget instruction fits 17, F6) — the rest go in
	// further StagePrecommits transactions through the offset the instruction takes, 40 a transaction.
	PrecommitChunk = 16
	// PrecommitsPerTx is how many precommit slots a StagePrecommits transaction of its own carries.
	PrecommitsPerTx = 40
	// SetSizeWarn is the validator-set size at which the machines alarm client:set-bound (G19/AO4): the
	// client's measured heap bound is about SetSizeBound through any history (§6.1), and the heap frame that
	// lifts it toward 255 is due before gno.land nears it.
	SetSizeWarn  = 40
	SetSizeBound = 50
	// MaxBodyCap is the most Grow makes of the body region (S-02: what a body of 255-validator sets throughout
	// takes, with the slack).
	MaxBodyCap = 81_920
	// StateSlack is the room the program keeps past the body when it sizes the region for a body (its
	// wanted_cap: the size plus the slack, rounded to 8).
	StateSlack = 2048
	// HistoryMax is how deep the client's set history is (S-01: the newest settled entry and four after it).
	HistoryMax = 5
)

// LightClient is one deployment of the program.
type LightClient struct{ Program solana.PublicKey }

func (c LightClient) State() solana.PublicKey { return PDA(c.Program, b("state")) }

func (c LightClient) UpdateStage(submitter solana.PublicKey, headerHash [32]byte) solana.PublicKey {
	return PDA(c.Program, b("update"), submitter[:], headerHash[:])
}

func (c LightClient) ProofStage(submitter solana.PublicKey, messageHash [32]byte) solana.PublicKey {
	return PDA(c.Program, b("proof"), submitter[:], messageHash[:])
}

// InitIx is the one-shot init, signed by the program's current upgrade authority.
func (c LightClient) InitIx(payer, authority solana.PublicKey, chainID string, origin uint32, trustedHeight uint64, trustedHash [32]byte, set []gnolight.Validator, admin solana.PublicKey) solana.Instruction {
	var e enc
	e.u8(0)
	e.str(chainID)
	e.u32(origin)
	e.u64(trustedHeight)
	e.arr32(trustedHash)
	e.validators(set)
	e.key(admin)
	return ix(c.Program, e.Bytes(), w(c.State()), ws(payer), rs(authority), r(c.Program), r(ProgramData(c.Program)), r(SystemProgram))
}

// GrowIx grows the state account by at most 10 KB, the payer paying: first the body region to the larger of what
// the body wants and bodyCap (a target in bytes, at or above the current region, bounded by MaxBodyCap; 0 asks
// for nothing beyond the body's own want — the deploy's three calls give the ring its 256 entries), then the
// ring. S-02: a bigger validator set gets its room ahead, step by step, before the update that announces it.
func (c LightClient) GrowIx(payer solana.PublicKey, bodyCap uint32) solana.Instruction {
	var e enc
	e.u8(8)
	e.u32(bodyCap)
	return ix(c.Program, e.Bytes(), w(c.State()), ws(payer), r(SystemProgram))
}

// StageInitArgs is StageInit's data: an update carries the header, its commit's BlockID and, when announcing,
// the next set's length and hash (the set itself follows by StageNextSet, S-02); a proof carries the message
// hash, the SimpleProof and the leaf's length, and either the header with its BlockID (signed by the current
// set) or a ring hash.
type StageInitArgs struct {
	Kind     byte
	Header   *gnolight.Header
	RingHash [32]byte
	BlockID  gnolight.BlockID
	// Round is the commit's one round (S-08: tm2's Commit.ValidateBasic requires every precommit's round to be
	// the commit's, the first present precommit's), from which the client builds every slot's sign bytes; a
	// StagePrecommits item of another round is refused (RoundMismatch). 0 for a ring proof, never negative.
	Round       int64
	NSlots      uint16
	MessageHash [32]byte
	NextSetLen  uint16   // 0 when the header announces nothing
	NextSetHash [32]byte // the header's NextValidatorsHash
	Proof       *gnolight.SimpleProof
	LeafLen     uint32
}

// Announce names the next set an update announces: its length and hash (nothing for an empty set).
func (a *StageInitArgs) Announce(next []gnolight.Validator) {
	a.NextSetLen, a.NextSetHash = uint16(len(next)), SetHash(next)
}

// SetHash is a validator set's hash as the client checks it (zero for an empty set, as the program's
// unwrap_or writes it for a header announcing nothing).
func SetHash(vs []gnolight.Validator) [32]byte {
	if len(vs) == 0 {
		return [32]byte{}
	}
	h, _ := (&gnolight.ValidatorSet{Validators: vs}).Hash()
	return h
}

func (a *StageInitArgs) data() []byte {
	var e enc
	e.u8(1)
	e.u8(a.Kind)
	if a.Header == nil {
		e.u8(0)
	} else {
		e.u8(1)
		e.header(a.Header)
	}
	e.arr32(a.RingHash)
	e.blockID(a.BlockID)
	e.i64(a.Round)
	e.u16(a.NSlots)
	e.arr32(a.MessageHash)
	e.u16(a.NextSetLen)
	e.arr32(a.NextSetHash)
	if a.Proof == nil {
		e.u8(0)
	} else {
		e.u8(1)
		e.proof(a.Proof)
	}
	e.u32(a.LeafLen)
	return e.Bytes()
}

// DecodeTrustedHeader reads the trusted header an Init (variant 0) installs: the record's entry at that slot; ok
// false for any other instruction (no reset exists since M2j: variant 7 is SetAdmin).
func DecodeTrustedHeader(data []byte) (height uint64, hash [32]byte, ok bool, err error) {
	if len(data) == 0 || data[0] != 0 {
		return 0, hash, false, nil
	}
	d := newDec(data[1:])
	d.str()
	d.u32()
	height = d.u64()
	hash = d.arr32()
	return height, hash, true, d.done()
}

// DecodeStageInit reads a StageInit instruction's data (the header record of §6.2, which the watch rebuilds from
// instruction data); ok false for any other instruction of the program.
func DecodeStageInit(data []byte) (*StageInitArgs, bool, error) {
	if len(data) == 0 || data[0] != 1 {
		return nil, false, nil
	}
	d := newDec(data[1:])
	a := &StageInitArgs{}
	a.Kind = d.u8()
	if d.option() {
		a.Header = d.header()
	}
	a.RingHash = d.arr32()
	a.BlockID = d.blockID()
	a.Round = d.i64()
	a.NSlots = d.u16()
	a.MessageHash = d.arr32()
	a.NextSetLen = d.u16()
	a.NextSetHash = d.arr32()
	if d.option() {
		a.Proof = d.proof()
	}
	a.LeafLen = d.u32()
	if err := d.done(); err != nil {
		return nil, true, err
	}
	return a, true, nil
}

func (c LightClient) StageInitIx(submitter, stage solana.PublicKey, a *StageInitArgs) solana.Instruction {
	return ix(c.Program, a.data(), ws(submitter), w(stage), r(c.State()), r(SystemProgram))
}

// StageNextSetIx fills an announcing update's next set from offset, in order (each call appends at the set's
// current length), over as many transactions as the set needs; UpdateFinish checks the whole set's hash (S-02).
func (c LightClient) StageNextSetIx(submitter, stage solana.PublicKey, offset uint16, items []gnolight.Validator) solana.Instruction {
	var e enc
	e.u8(9)
	e.u16(offset)
	e.validators(items)
	return ix(c.Program, e.Bytes(), ws(submitter), w(stage), r(SystemProgram))
}

// NextSetTxs are the chunk transactions of a next set, NextSetChunk validators each (tests/testlib's
// next_set_txs), every one with its compute budget first.
func (c LightClient) NextSetTxs(submitter, stage solana.PublicKey, next []gnolight.Validator, budget uint32) [][]solana.Instruction {
	var txs [][]solana.Instruction
	for off := 0; off < len(next); off += NextSetChunk {
		end := min(off+NextSetChunk, len(next))
		txs = append(txs, withBudget(budget, c.StageNextSetIx(submitter, stage, uint16(off), next[off:end])))
	}
	return txs
}

// StagedPrecommit is what a present precommit needs beyond the header and the commit's BlockID.
type StagedPrecommit struct {
	Round int64
	Secs  int64
	Nanos int32
}

func (c LightClient) StagePrecommitsIx(submitter, stage solana.PublicKey, offset uint16, items []StagedPrecommit) solana.Instruction {
	var e enc
	e.u8(2)
	e.u16(offset)
	e.u32(uint32(len(items)))
	for _, p := range items {
		e.i64(p.Round)
		e.i64(p.Secs)
		e.i32(p.Nanos)
	}
	return ix(c.Program, e.Bytes(), rs(submitter), w(stage))
}

func (c LightClient) StageLeafIx(submitter, stage solana.PublicKey, chunk []byte) solana.Instruction {
	var e enc
	e.u8(3)
	e.vec(chunk)
	return ix(c.Program, e.Bytes(), ws(submitter), w(stage), r(SystemProgram))
}

// StageVerifyIx marks the slots the ed25519 precompile instruction at ixIndex verified: entry j is slot slots[j].
func (c LightClient) StageVerifyIx(stage solana.PublicKey, ixIndex uint16, slots []uint16) solana.Instruction {
	var e enc
	e.u8(4)
	e.u16(ixIndex)
	e.u32(uint32(len(slots)))
	for _, s := range slots {
		e.u16(s)
	}
	return ix(c.Program, e.Bytes(), w(stage), r(c.State()), r(SysvarIxs))
}

func (c LightClient) UpdateFinishIx(stage, submitter solana.PublicKey) solana.Instruction {
	return ix(c.Program, []byte{5}, w(stage), w(c.State()), w(submitter))
}

func (c LightClient) StageCloseIx(stage, closer solana.PublicKey) solana.Instruction {
	return ix(c.Program, []byte{6}, w(stage), ws(closer))
}

func (c LightClient) SetAdminIx(admin, newAdmin solana.PublicKey) solana.Instruction {
	var e enc
	e.u8(7)
	e.key(newAdmin)
	return ix(c.Program, e.Bytes(), w(c.State()), rs(admin))
}

// VerifyAccounts are the accounts the client's Verify reads: its state (writable: a fresh proof header is
// recorded) and the proof stage of this submitter and message, as its VerifyAccountMetas answers them.
func (c LightClient) VerifyAccounts(submitter solana.PublicKey, message []byte) solana.AccountMetaSlice {
	return solana.AccountMetaSlice{w(c.State()), r(c.ProofStage(submitter, gnolight.MessageHash(message)))}
}

// ------------------------------------------------------------ the precompile --

// Ed25519Entry is one (pubkey, message, signature) the precompile verifies.
type Ed25519Entry struct {
	PubKey    [32]byte
	Signature [64]byte
	Message   []byte
}

// Ed25519Ix is the precompile instruction carrying the entries, every offset inside its own data (sol-util's
// ed25519::instruction_data): a header (count, 0), fourteen bytes of offsets per entry, then pubkey ‖ signature ‖
// message per entry.
func Ed25519Ix(entries []Ed25519Entry) solana.Instruction {
	const headerLen, offsetsLen = 2, 14
	n := len(entries)
	var data enc
	data.u8(uint8(n))
	data.u8(0)
	cursor := headerLen + offsetsLen*n
	var body enc
	for _, e := range entries {
		pkOff := cursor
		sigOff := pkOff + 32
		msgOff := sigOff + 64
		for _, v := range []uint16{uint16(sigOff), 0xffff, uint16(pkOff), 0xffff, uint16(msgOff), uint16(len(e.Message)), 0xffff} {
			data.u16(v)
		}
		body.Write(e.PubKey[:])
		body.Write(e.Signature[:])
		body.Write(e.Message)
		cursor = msgOff + len(e.Message)
	}
	data.Write(body.Bytes())
	return ix(Ed25519Program, data.Bytes())
}

// --------------------------------------------------------------- staging --

// Staged is the precommit data of a commit, slot by slot (empty slots zero), and the present slots.
func Staged(c *gnolight.Commit) (items []StagedPrecommit, present []int) {
	for i, p := range c.Precommits {
		if p == nil {
			items = append(items, StagedPrecommit{})
			continue
		}
		items = append(items, StagedPrecommit{Round: p.Round, Secs: p.Time.Secs, Nanos: p.Time.Nanos})
		present = append(present, i)
	}
	return items, present
}

// withBudget is a staged transaction's final instruction list: the compute-budget instruction first when budget
// is set, then the instructions as built. A verify instruction names the precompile's position in this list, so
// nothing may be prepended to it later (sol-util's introspection reads the instruction at that index and answers
// NotThePrecompile to any other).
func withBudget(budget uint32, ixs ...solana.Instruction) []solana.Instruction {
	if budget == 0 {
		return ixs
	}
	return append([]solana.Instruction{ComputeLimitIx(budget)}, ixs...)
}

// verifyTxs are the [budget + precompile + StageVerify] transactions for the present slots of a commit, perTx at a
// time; StageVerify names the precompile's index past the budget instruction.
func (c LightClient) verifyTxs(stage solana.PublicKey, chainID string, height int64, cm *gnolight.Commit, keys [][32]byte, present []int, perTx int, budget uint32) ([][]solana.Instruction, error) {
	var txs [][]solana.Instruction
	precompileAt := uint16(len(withBudget(budget))) // 0, or 1 past the budget instruction
	for start := 0; start < len(present); start += perTx {
		end := start + perTx
		if end > len(present) {
			end = len(present)
		}
		var entries []Ed25519Entry
		var slots []uint16
		for _, i := range present[start:end] {
			if i >= len(keys) || len(cm.Signatures[i]) != 64 {
				return nil, fmt.Errorf("slot %d has no key or no 64-byte signature", i)
			}
			var sig [64]byte
			copy(sig[:], cm.Signatures[i])
			entries = append(entries, Ed25519Entry{PubKey: keys[i], Signature: sig, Message: cm.SignBytes(chainID, height, i)})
			slots = append(slots, uint16(i))
		}
		txs = append(txs, withBudget(budget, Ed25519Ix(entries), c.StageVerifyIx(stage, precompileAt, slots)))
	}
	return txs, nil
}

// UpdateTxs are the transactions that stage and finish an update: [init + precommits] · [next set chunk]… ·
// [precompile + verify]… · [finish]. keys are the set's keys in slot order (the set the header's ValidatorsHash
// names); nextSet is the set an announcing header announces (its length and hash go in the init, the set itself
// in chunks of NextSetChunk, S-02). Every transaction carries its compute budget (budget units; 0 for none) as its
// first instruction: the sender sends each list as it is.
func (c LightClient) UpdateTxs(submitter solana.PublicKey, chainID string, h *gnolight.Header, cm *gnolight.Commit, keys [][32]byte, nextSet []gnolight.Validator, perTx int, budget uint32) ([][]solana.Instruction, error) {
	if perTx <= 0 {
		perTx = 3
	}
	hash := h.Hash()
	stage := c.UpdateStage(submitter, hash)
	items, present := Staged(cm)
	if len(items) != len(keys) {
		return nil, fmt.Errorf("%d precommit slots for %d validators", len(items), len(keys))
	}
	init := &StageInitArgs{Kind: StageKindUpdate, Header: h, BlockID: cm.BlockID, Round: cm.Round(), NSlots: uint16(len(items))}
	init.Announce(nextSet)
	txs, err := c.initTxs(submitter, stage, init, items, budget)
	if err != nil {
		return nil, err
	}
	txs = append(txs, c.NextSetTxs(submitter, stage, nextSet, budget)...)
	vt, err := c.verifyTxs(stage, chainID, h.Height, cm, keys, present, perTx, budget)
	if err != nil {
		return nil, err
	}
	txs = append(txs, vt...)
	txs = append(txs, withBudget(budget, c.UpdateFinishIx(stage, submitter)))
	return txs, nil
}

// ErrPacket is a staging whose StageInit alone exceeds one packet: a header with a merkle proof of more than
// seven aunts (a block of more than 128 results). The ring form of the proof carries no header, and a Reemit
// lands the message in a fresh block.
var ErrPacket = errors.New("over the packet limit")

// TxSize is a transaction's size on the wire when sent by payer (a size-only transaction: a zero blockhash).
func TxSize(payer solana.PublicKey, ixs []solana.Instruction) (int, error) {
	tx, err := solana.NewTransaction(ixs, solana.Hash{}, solana.TransactionPayer(payer))
	if err != nil {
		return 0, err
	}
	b, err := tx.MarshalBinary()
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func txFits(payer solana.PublicKey, ixs []solana.Instruction) bool {
	n, err := TxSize(payer, ixs)
	return err == nil && n <= PacketLimit
}

// Fits says whether a transaction of these instructions, sent by payer, fits one packet (F19: a cancel's
// delivery passes only as many pending pairs as fit).
func Fits(payer solana.PublicKey, ixs []solana.Instruction) bool { return txFits(payer, ixs) }

// initTxs are the transactions that stage a header with its precommit slots under the packet limit (F6): the
// init with as many of the first slots beside it as fit (at most PrecommitChunk, measured on the serialized
// transaction: a header and a merkle proof take most of the packet), then the rest PrecommitsPerTx a transaction
// through the offset StagePrecommits takes (the program fills the slots at the offset, so the order of the
// transactions after the init does not matter). An init that fits alone in no packet is ErrPacket.
func (c LightClient) initTxs(submitter, stage solana.PublicKey, init *StageInitArgs, items []StagedPrecommit, budget uint32) ([][]solana.Instruction, error) {
	initIx := c.StageInitIx(submitter, stage, init)
	first := min(PrecommitChunk, len(items))
	for ; first > 0; first-- {
		if txFits(submitter, withBudget(budget, initIx, c.StagePrecommitsIx(submitter, stage, 0, items[:first]))) {
			break
		}
	}
	var head []solana.Instruction
	if first > 0 {
		head = withBudget(budget, initIx, c.StagePrecommitsIx(submitter, stage, 0, items[:first]))
	} else {
		head = withBudget(budget, initIx)
		if !txFits(submitter, head) {
			aunts := 0
			if init.Proof != nil {
				aunts = len(init.Proof.Aunts)
			}
			n, _ := TxSize(submitter, head)
			return nil, fmt.Errorf("%w: the StageInit alone is %d bytes (a header with a merkle proof of %d aunts; %d is the limit)", ErrPacket, n, aunts, PacketLimit)
		}
	}
	txs := [][]solana.Instruction{head}
	for off := first; off < len(items); off += PrecommitsPerTx {
		end := min(off+PrecommitsPerTx, len(items))
		txs = append(txs, withBudget(budget, c.StagePrecommitsIx(submitter, stage, uint16(off), items[off:end])))
	}
	return txs, nil
}

// ProofTxs are the transactions that stage a proof: by a ring hash (no signatures), or with the header and its
// commit signed by the current set; then the leaf, 900 bytes a transaction. The mailbox's process consumes it.
// Every transaction carries its compute budget first, as UpdateTxs' do.
func (c LightClient) ProofTxs(submitter solana.PublicKey, chainID string, h *gnolight.Header, ringHash [32]byte, cm *gnolight.Commit, keys [][32]byte, message []byte, proof *gnolight.SimpleProof, leaf []byte, perTx int, budget uint32) ([][]solana.Instruction, error) {
	if perTx <= 0 {
		perTx = 3
	}
	if len(leaf) == 0 || len(leaf) > MaxLeaf {
		return nil, fmt.Errorf("a leaf of %d bytes (the ISM takes 1 to %d)", len(leaf), MaxLeaf)
	}
	msgHash := gnolight.MessageHash(message)
	stage := c.ProofStage(submitter, msgHash)
	a := &StageInitArgs{Kind: StageKindProof, MessageHash: msgHash, Proof: proof, LeafLen: uint32(len(leaf)), RingHash: ringHash}
	var vt [][]solana.Instruction
	if h != nil {
		if cm == nil {
			return nil, errors.New("a proof by header needs its commit")
		}
		items, present := Staged(cm)
		if len(items) != len(keys) {
			return nil, fmt.Errorf("%d precommit slots for %d validators", len(items), len(keys))
		}
		a.Header, a.BlockID, a.Round, a.NSlots = h, cm.BlockID, cm.Round(), uint16(len(items))
		var err error
		if vt, err = c.verifyTxs(stage, chainID, h.Height, cm, keys, present, perTx, budget); err != nil {
			return nil, err
		}
	}
	var txs [][]solana.Instruction
	if h != nil {
		items, _ := Staged(cm)
		var err error
		if txs, err = c.initTxs(submitter, stage, a, items, budget); err != nil { // the init with its precommits in chunks (F6)
			return nil, err
		}
	} else {
		txs = [][]solana.Instruction{withBudget(budget, c.StageInitIx(submitter, stage, a))}
		if !txFits(submitter, txs[0]) {
			n, _ := TxSize(submitter, txs[0])
			return nil, fmt.Errorf("%w: the ring proof's StageInit is %d bytes (a merkle proof of %d aunts; %d is the limit)", ErrPacket, n, len(proof.Aunts), PacketLimit)
		}
	}
	txs = append(txs, vt...)
	for start := 0; start < len(leaf); start += LeafChunk {
		end := start + LeafChunk
		if end > len(leaf) {
			end = len(leaf)
		}
		txs = append(txs, withBudget(budget, c.StageLeafIx(submitter, stage, leaf[start:end])))
	}
	return txs, nil
}

// ---------------------------------------------------------------- the state --

// ClientHead is the 64-byte head of the state account (sol-util's client_head), which every reader checks.
type ClientHead struct {
	Version          byte
	ClockFactor      byte
	RingCount        uint16
	RingNext         uint16
	BodyCap8         uint16
	QuarantineEnd    int64
	LastAcceptedTime int64
	TrustedHeight    uint64
	TrustedHash      [32]byte
}

func (h *ClientHead) BodyCap() int { return int(h.BodyCap8) * 8 }

// SetEntry is one validator-set change in the client's history.
type SetEntry struct {
	FromHeight    int64
	SetHash       [32]byte
	Set           []gnolight.Validator
	AnnouncedSlot uint64
	AnnouncedTime int64
	QuarantineEnd int64
}

type Trusted struct {
	Height             int64
	Hash               [32]byte
	ValidatorsHash     [32]byte
	NextValidatorsHash [32]byte
	TimeSecs           int64
}

// ClientBody is the borsh body after the head.
type ClientBody struct {
	Admin        solana.PublicKey
	ChainID      string
	OriginDomain uint32
	Trusted      Trusted
	Set          []gnolight.Validator
	SetHash      [32]byte
	NextSet      []gnolight.Validator // nil when none
	NextSetHash  [32]byte
	History      []SetEntry
	Records      uint64
	RunningHash  [32]byte
	// RingEpoch counts the ring's clears (a set change at UpdateFinish), one up each (S-16): a by-ring
	// stage copies the entry's height and results hash under the epoch of its staging, and Verify reads the copy
	// while the epoch and the set are still the staging's, so the ring's churn evicts nothing a staged proof needs.
	RingEpoch uint64
}

// CurrentSetFrom is the first height the current set signs from (the history entry whose set hash is the
// current set's), 0 if unknown.
func (b *ClientBody) CurrentSetFrom() int64 {
	for i := len(b.History) - 1; i >= 0; i-- {
		if b.History[i].SetHash == b.SetHash {
			return b.History[i].FromHeight
		}
	}
	return 0
}

// SetKeys are the current set's keys in slot order.
func (b *ClientBody) SetKeys() [][32]byte { return keysOf(b.Set) }

// Size is the body's borsh length as the program stores it (state.rs Body::size).
func (b *ClientBody) Size() int { return len(b.encode()) }

// encode is the body's borsh form as the program stores it (state.rs Body), the ring epoch last (S-16).
func (b *ClientBody) encode() []byte {
	var e enc
	e.key(b.Admin)
	e.str(b.ChainID)
	e.u32(b.OriginDomain)
	e.i64(b.Trusted.Height)
	e.arr32(b.Trusted.Hash)
	e.arr32(b.Trusted.ValidatorsHash)
	e.arr32(b.Trusted.NextValidatorsHash)
	e.i64(b.Trusted.TimeSecs)
	e.validators(b.Set)
	e.arr32(b.SetHash)
	if b.NextSet == nil {
		e.u8(0)
	} else {
		e.u8(1)
		e.validators(b.NextSet)
	}
	e.arr32(b.NextSetHash)
	e.u32(uint32(len(b.History)))
	for _, h := range b.History {
		e.i64(h.FromHeight)
		e.arr32(h.SetHash)
		e.validators(h.Set)
		e.u64(h.AnnouncedSlot)
		e.i64(h.AnnouncedTime)
		e.i64(h.QuarantineEnd)
	}
	e.u64(b.Records)
	e.arr32(b.RunningHash)
	e.u64(b.RingEpoch)
	return e.Bytes()
}

// SetEntrySize is a history entry's borsh length for a set of n validators.
func SetEntrySize(n int) int { return 8 + 32 + 4 + 40*n + 8 + 8 + 8 }

// BodyCapFor is the body region a body of size bytes wants: the size with the program's slack, rounded to 8
// (state.rs wanted_cap), bounded by MaxBodyCap.
func BodyCapFor(size int) int {
	cap := (size + StateSlack + 7) / 8 * 8
	if cap > MaxBodyCap {
		cap = MaxBodyCap
	}
	return cap
}

// BodyCapNeeded is the body region a set change to n validators needs before the update that announces it
// (S-02): the body as it stands grows by the announced set at the announcement (next_set) and by a history
// entry at the switch — with the set it replaces moving into the history and the next_set emptying; the largest
// of those states, with the slack. A body past the bound asks for the bound (the heap, not the region, is the
// real limit then).
func (b *ClientBody) BodyCapNeeded(n int) int {
	size := b.Size()
	announced := size + 4 + 40*n
	switched := size + SetEntrySize(len(b.Set)) + 40*(n-len(b.Set))
	return BodyCapFor(max(announced, switched))
}

func keysOf(vs []gnolight.Validator) [][32]byte {
	out := make([][32]byte, len(vs))
	for i, v := range vs {
		out[i] = v.PubKey
	}
	return out
}

type RingEntry struct {
	Hash            [32]byte
	Height          int64
	LastResultsHash [32]byte
}

// ParseClientHead reads the head alone.
func ParseClientHead(data []byte) (*ClientHead, error) {
	if len(data) < HeadLen {
		return nil, errors.New("client state: shorter than its head")
	}
	d := newDec(data[:HeadLen])
	h := &ClientHead{Version: d.u8(), ClockFactor: d.u8(), RingCount: d.u16(), RingNext: d.u16(), BodyCap8: d.u16(), QuarantineEnd: d.i64(), LastAcceptedTime: d.i64(), TrustedHeight: d.u64(), TrustedHash: d.arr32()}
	return h, d.done()
}

// ParseClientState reads the head, the body and the ring.
func ParseClientState(data []byte) (*ClientHead, *ClientBody, []RingEntry, error) {
	h, err := ParseClientHead(data)
	if err != nil {
		return nil, nil, nil, err
	}
	if h.Version != ClientStateVersion {
		return nil, nil, nil, fmt.Errorf("client state: layout version %d, not %d", h.Version, ClientStateVersion)
	}
	d := newDec(data[HeadLen:])
	b := &ClientBody{}
	b.Admin = d.key()
	b.ChainID = d.str()
	b.OriginDomain = d.u32()
	b.Trusted = Trusted{Height: d.i64(), Hash: d.arr32(), ValidatorsHash: d.arr32(), NextValidatorsHash: d.arr32(), TimeSecs: d.i64()}
	b.Set = d.validators()
	b.SetHash = d.arr32()
	if d.option() {
		b.NextSet = d.validators()
	}
	b.NextSetHash = d.arr32()
	n := d.u32()
	if d.err == nil && n > 16 {
		return nil, nil, nil, errors.New("client state: a history longer than the program keeps")
	}
	for i := uint32(0); i < n && d.err == nil; i++ {
		e := SetEntry{FromHeight: d.i64(), SetHash: d.arr32()}
		e.Set = d.validators()
		e.AnnouncedSlot = d.u64()
		e.AnnouncedTime = d.i64()
		e.QuarantineEnd = d.i64()
		b.History = append(b.History, e)
	}
	b.Records = d.u64()
	b.RunningHash = d.arr32()
	b.RingEpoch = d.u64() // S-16: after running_hash, the last field
	if err := d.done(); err != nil {
		return nil, nil, nil, fmt.Errorf("client state body: %w", err)
	}
	off := HeadLen + h.BodyCap()
	cap := 0
	if len(data) > off {
		cap = (len(data) - off) / RingEntryLen
	}
	count := int(h.RingCount)
	if count > cap {
		count = cap
	}
	var ring []RingEntry
	for i := 0; i < count; i++ {
		at := off + i*RingEntryLen
		var e RingEntry
		copy(e.Hash[:], data[at:at+32])
		e.Height = int64(uint64(data[at+32]) | uint64(data[at+33])<<8 | uint64(data[at+34])<<16 | uint64(data[at+35])<<24 | uint64(data[at+36])<<32 | uint64(data[at+37])<<40 | uint64(data[at+38])<<48 | uint64(data[at+39])<<56)
		copy(e.LastResultsHash[:], data[at+40:at+72])
		ring = append(ring, e)
	}
	return h, b, ring, nil
}

// RingCapacity is how many entries the account holds now (256 once grown).
func RingCapacity(h *ClientHead, dataLen int) int {
	off := HeadLen + h.BodyCap()
	if dataLen <= off {
		return 0
	}
	return (dataLen - off) / RingEntryLen
}

// Stage is a staged update or proof as the account holds it (the leaf follows the borsh bytes).
type Stage struct {
	Version, Kind, Bump byte
	Submitter           solana.PublicKey
	CreatedSlot         uint64
	CreatedTime         int64
	Key                 [32]byte
	ByRing              bool
	HeaderHash          [32]byte
	Header              *gnolight.Header
	BlockID             gnolight.BlockID
	Round               int64 // the commit's one round (S-08), from StageInit
	SetHash             [32]byte
	NSlots              uint16
	Precommits          []StagedPrecommit
	Filled, Verified    []byte
	// NextSet is an announcing update's new set as filled so far by StageNextSet, up to NextSetLen entries (0
	// for a header announcing nothing); NextSetHash the header's NextValidatorsHash, checked at the finish.
	NextSet             []gnolight.Validator
	NextSetLen          uint16
	NextSetHash         [32]byte
	Proof               *gnolight.SimpleProof
	LeafLen, LeafFilled uint32
	// A by-ring proof's copy of its entry (S-16): the body's ring epoch at the staging, the entry's height and
	// its LastResultsHash — 48 bytes at the stage's end, zero in an update and in a header proof; Verify reads
	// the copy while the epoch and the set are still the staging's, needing no entry. A proof's leaf follows the
	// borsh bytes, so its offset moved by 48 with them.
	RingEpoch   uint64
	RingHeight  int64
	RingResults [32]byte
}

// Announcing says whether the stage's update announces a set, and NextSetComplete whether the set is whole.
func (s *Stage) Announcing() bool      { return s.NextSetLen > 0 }
func (s *Stage) NextSetComplete() bool { return len(s.NextSet) == int(s.NextSetLen) }

func bit(m []byte, i int) bool { return i/8 < len(m) && m[i/8]&(1<<(i%8)) != 0 }

func (s *Stage) IsFilled(i int) bool   { return bit(s.Filled, i) }
func (s *Stage) IsVerified(i int) bool { return bit(s.Verified, i) }

// ParseStage reads a stage account.
func ParseStage(data []byte) (*Stage, error) {
	d := newDec(data)
	s := &Stage{Version: d.u8(), Kind: d.u8(), Bump: d.u8(), Submitter: d.key(), CreatedSlot: d.u64(), CreatedTime: d.i64(), Key: d.arr32(), ByRing: d.boolean(), HeaderHash: d.arr32()}
	s.Header = d.header()
	s.BlockID = d.blockID()
	s.Round = d.i64()
	s.SetHash = d.arr32()
	s.NSlots = d.u16()
	n := d.u32()
	if d.err == nil && n > 1024 {
		return nil, errors.New("stage: too many slots")
	}
	for i := uint32(0); i < n && d.err == nil; i++ {
		s.Precommits = append(s.Precommits, StagedPrecommit{Round: d.i64(), Secs: d.i64(), Nanos: d.i32()})
	}
	s.Filled = d.bytes()
	s.Verified = d.bytes()
	s.NextSet = d.validators()
	s.NextSetLen = d.u16()
	s.NextSetHash = d.arr32()
	if d.option() {
		s.Proof = d.proof()
	}
	s.LeafLen = d.u32()
	s.LeafFilled = d.u32()
	s.RingEpoch = d.u64() // S-16: the three fields after leaf_filled
	s.RingHeight = d.i64()
	s.RingResults = d.arr32()
	if err := d.done(); err != nil {
		return nil, fmt.Errorf("stage: %w", err)
	}
	return s, nil
}

// ClientError is the program's refusal code behind a custom error (1000 + n).
func ClientError(code uint32) string {
	names := []string{"AlreadyInitialized", "NotInitialized", "WrongVersion", "WrongAuthority", "WrongStage", "WrongSubmitter", "WrongSet", "HeightNotAboveTrusted", "HeightAboveTrusted", "ChainId", "ValidatorsHash", "NextValidatorsHash", "NextSetMissing", "NextSetUnexpected", "SlotOutOfRange", "SlotNotFilled", "SlotAlreadyFilled", "SignatureMismatch", "NotEnoughPower", "LeafTooBig", "LeafIncomplete", "ProofMissing", "ProofMismatch", "NoResultsHash", "NotInRing", "OriginMismatch", "DispatchMismatch", "NotAbandoned", "NotAdmin", "Metadata", "BadMessage", "Overflow", "NeedsGrow", "StageIncomplete", "HistoryFull", "ChunkOutOfOrder", "RoundMismatch"}
	if code >= 1000 && int(code-1000) < len(names) {
		return "client: " + names[code-1000]
	}
	return ""
}

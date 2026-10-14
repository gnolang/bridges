package gnolight

import (
	"crypto/sha256"
	"errors"
	"sort"
)

// tm2's block header (tm2/pkg/bft/types/block.go) and its hash: SimpleHashFromByteSlices over the sixteen
// amino-encoded fields in order, each marshalled on its own (an empty field is an empty leaf, sha256(0x00)).
// The fields are the wire form the programs stage (borsh in bridge/sol): a nil hash is an empty slice.

var ErrBadHeader = errors.New("gnolight: a header field is out of its range")

type PartSetHeader struct {
	Total uint32
	Hash  []byte
}

type BlockID struct {
	Hash  []byte
	Parts PartSetHeader
}

func (b BlockID) IsEmpty() bool {
	return len(b.Hash) == 0 && b.Parts.Total == 0 && len(b.Parts.Hash) == 0
}

// aminoHeaderForm is tm2's BlockID as a header field or a commit's: PartSetHeader.Total is a Go int, zigzag.
func (b BlockID) aminoHeaderForm() []byte {
	var parts []byte
	parts = putZigzagField(parts, 1, int64(b.Parts.Total))
	parts = putBytesField(parts, 2, b.Parts.Hash)
	var out []byte
	out = putBytesField(out, 1, b.Hash)
	return putBytesField(out, 2, parts)
}

// aminoCanonicalForm is CanonicalBlockID, the sign bytes' form: Total is a uint32, a plain uvarint.
func (b BlockID) aminoCanonicalForm() []byte {
	var parts []byte
	parts = putUvarintField(parts, 1, uint64(b.Parts.Total))
	parts = putBytesField(parts, 2, b.Parts.Hash)
	var out []byte
	out = putBytesField(out, 1, b.Hash)
	return putBytesField(out, 2, parts)
}

type Time struct {
	Secs  int64
	Nanos int32
}

func (t Time) aminoBare() []byte { return timeBare(t.Secs, t.Nanos) }

type Header struct {
	Version            string
	ChainID            string
	Height             int64
	Time               Time
	NumTxs             int64
	TotalTxs           int64
	AppVersion         string
	LastBlockID        BlockID
	LastCommitHash     []byte
	DataHash           []byte
	ValidatorsHash     []byte
	NextValidatorsHash []byte
	ConsensusHash      []byte
	AppHash            []byte
	LastResultsHash    []byte
	// ProposerAddress is the bech32 string (g1…), which is what amino writes for a crypto.Address.
	ProposerAddress string
}

// wrappedBytes is a top-level string or byte field: amino.Marshal of a non-struct wraps it as field 1.
func wrappedBytes(b []byte) []byte { return putBytesField(nil, 1, b) }

func wrappedZigzag(v int64) []byte { return putZigzagField(nil, 1, v) }

// Leaves are the sixteen leaves, each the amino bytes of one field (empty where the field is empty).
func (h *Header) Leaves() [16][]byte {
	return [16][]byte{
		wrappedBytes([]byte(h.Version)),
		wrappedBytes([]byte(h.ChainID)),
		wrappedZigzag(h.Height),
		h.Time.aminoBare(),
		wrappedZigzag(h.NumTxs),
		wrappedZigzag(h.TotalTxs),
		wrappedBytes([]byte(h.AppVersion)),
		h.LastBlockID.aminoHeaderForm(),
		wrappedBytes(h.LastCommitHash),
		wrappedBytes(h.DataHash),
		wrappedBytes(h.ValidatorsHash),
		wrappedBytes(h.NextValidatorsHash),
		wrappedBytes(h.ConsensusHash),
		wrappedBytes(h.AppHash),
		wrappedBytes(h.LastResultsHash),
		wrappedBytes([]byte(h.ProposerAddress)),
	}
}

// Hash is Header.Hash(): the merkle root of the leaves.
func (h *Header) Hash() [32]byte {
	leaves := h.Leaves()
	items := make([][]byte, 16)
	for i := range leaves {
		items[i] = leaves[i]
	}
	root, _ := SimpleHash(items)
	return root
}

// Check is the sanity the client asks of a header: a positive height, 32-byte hashes where a hash must stand, a
// chain id and version that fit the staging accounts.
func (h *Header) Check() error {
	if h.Height <= 0 || h.NumTxs < 0 || h.TotalTxs < 0 {
		return ErrBadHeader
	}
	if h.ChainID == "" || len(h.ChainID) > 64 || len(h.Version) > 64 || len(h.AppVersion) > 64 {
		return ErrBadHeader
	}
	if len(h.ValidatorsHash) != 32 || len(h.NextValidatorsHash) != 32 {
		return ErrBadHeader
	}
	for _, opt := range [][]byte{h.LastCommitHash, h.DataHash, h.ConsensusHash, h.AppHash, h.LastResultsHash, h.LastBlockID.Hash, h.LastBlockID.Parts.Hash} {
		if !(len(opt) == 0 || len(opt) == 32) {
			return ErrBadHeader
		}
	}
	if len(h.ProposerAddress) > 64 {
		return ErrBadHeader
	}
	return nil
}

func arr32(b []byte) (out [32]byte) {
	copy(out[:], b)
	return out
}

func (h *Header) ValidatorsHashArray() [32]byte     { return arr32(h.ValidatorsHash) }
func (h *Header) NextValidatorsHashArray() [32]byte { return arr32(h.NextValidatorsHash) }

// LastResultsHashArray is LastResultsHash, or ok false when the previous block had no transactions.
func (h *Header) LastResultsHashArray() (out [32]byte, ok bool) {
	if len(h.LastResultsHash) != 32 {
		return out, false
	}
	return arr32(h.LastResultsHash), true
}

// Announcing is whether this header announces a validator-set change (NextValidatorsHash ≠ ValidatorsHash): the
// header the client must be presented at a rotation (§6.1).
func (h *Header) Announcing() bool { return string(h.ValidatorsHash) != string(h.NextValidatorsHash) }

// ----------------------------------------------------------------- votes --

const (
	PrecommitType = 2
	PrevoteType   = 1
)

// CanonicalVote is a vote's sign bytes: amino.MarshalSized(CanonicalVote) (§13): uvarint(len) · 08 type · 11 +
// 8-byte LE height · [19 + 8-byte LE round, omitted when 0] · 22 len {block id} · 2A len {time} · 32 len chain id.
// Built per precommit from its own BlockID and timestamp; a nil precommit (an empty BlockID) signs without field 4.
func CanonicalVote(typ byte, chainID string, height, round int64, blockID BlockID, t Time) []byte {
	var body []byte
	body = putUvarintField(body, 1, uint64(typ))
	body = putFixed64Field(body, 2, uint64(height))
	body = putFixed64Field(body, 3, uint64(round))
	body = putBytesField(body, 4, blockID.aminoCanonicalForm())
	body = putBytesField(body, 5, t.aminoBare())
	body = putBytesField(body, 6, []byte(chainID))
	return sized(body)
}

// PrecommitSignBytes is a precommit's sign bytes.
func PrecommitSignBytes(chainID string, height, round int64, blockID BlockID, t Time) []byte {
	return CanonicalVote(PrecommitType, chainID, height, round, blockID, t)
}

// Precommit is one present precommit as the client stages it: its round and its own timestamp (the signature
// travels in the ed25519 precompile instruction).
type Precommit struct {
	Round int64
	Time  Time
}

// Commit is a block's commit as the client stages it: one slot per validator, a present precommit only where it
// is for the commit's BlockID (a nil precommit or one for another block is dropped to an empty slot, M2-NOTES 1).
type Commit struct {
	BlockID    BlockID
	Precommits []*Precommit
	// Signatures are each present slot's signature, in slot order (nil where the slot is empty).
	Signatures [][]byte
}

// SignBytes are the sign bytes of the precommit in slot i, or nil for an empty slot.
func (c *Commit) SignBytes(chainID string, height int64, i int) []byte {
	if i < 0 || i >= len(c.Precommits) || c.Precommits[i] == nil {
		return nil
	}
	p := c.Precommits[i]
	return PrecommitSignBytes(chainID, height, p.Round, c.BlockID, p.Time)
}

// Round is the commit's one round: the first present precommit's, as tm2's Commit.Round reads it (0 with none).
// tm2's Commit.ValidateBasic requires every precommit's round to be it, and the client stages it once (S-08).
func (c *Commit) Round() int64 {
	for _, p := range c.Precommits {
		if p != nil {
			return p.Round
		}
	}
	return 0
}

// Present are the slots that carry a precommit.
func (c *Commit) Present() []int {
	var out []int
	for i, p := range c.Precommits {
		if p != nil {
			out = append(out, i)
		}
	}
	return out
}

// CommitRule is the chain's own rule, weighted by power: more than two thirds.
func CommitRule(tallied, total int64) bool {
	return tallied >= 0 && total > 0 && tallied*3 > total*2
}

// ------------------------------------------------------------- validators --

const (
	PubKeyTypeURL = "/tm.PubKeyEd25519"
	MaxValidators = 255
)

type Validator struct {
	PubKey [32]byte
	Power  int64
}

// Address is crypto.PubKey.Address() for ed25519: the first twenty bytes of sha256 of the key.
func (v Validator) Address() [20]byte {
	d := sha256.Sum256(v.PubKey[:])
	var a [20]byte
	copy(a[:], d[:20])
	return a
}

// Leaf is Validator.Bytes(): amino of struct{ PubKey crypto.PubKey; VotingPower int64 }:
// 0A37 { 0A11"/tm.PubKeyEd25519" 1222 { 0A20<pk> } } 10 <zigzag power>.
func (v Validator) Leaf() []byte {
	pk := putBytesField(nil, 1, v.PubKey[:])
	a := Any(PubKeyTypeURL, pk)
	var out []byte
	out = putBytesField(out, 1, a)
	return putZigzagField(out, 2, v.Power)
}

type ValidatorSet struct {
	Validators []Validator
}

// Hash is ValidatorSet.Hash(); ok false for an empty set.
func (s *ValidatorSet) Hash() ([32]byte, bool) {
	items := make([][]byte, len(s.Validators))
	for i, v := range s.Validators {
		items[i] = v.Leaf()
	}
	return SimpleHash(items)
}

func (s *ValidatorSet) TotalPower() int64 {
	var t int64
	for _, v := range s.Validators {
		t += v.Power
	}
	return t
}

// Tallied is the power of the slots verified says yes to.
func (s *ValidatorSet) Tallied(verified func(i int) bool) int64 {
	var t int64
	for i, v := range s.Validators {
		if verified(i) {
			t += v.Power
		}
	}
	return t
}

// Check is what a set the client stores must be: 1–255 validators, positive powers, addresses strictly ascending.
func (s *ValidatorSet) Check() error {
	if len(s.Validators) == 0 || len(s.Validators) > MaxValidators {
		return errors.New("gnolight: a validator set of " + itoa(int64(len(s.Validators))) + " validators")
	}
	var prev *[20]byte
	for _, v := range s.Validators {
		if v.Power <= 0 {
			return errors.New("gnolight: a validator with no power")
		}
		a := v.Address()
		if prev != nil && string(a[:]) <= string(prev[:]) {
			return errors.New("gnolight: validators out of address order")
		}
		prev = &a
	}
	return nil
}

// Sort orders the set by address, as tm2 keeps it.
func (s *ValidatorSet) Sort() {
	sort.Slice(s.Validators, func(i, j int) bool {
		a, b := s.Validators[i].Address(), s.Validators[j].Address()
		return string(a[:]) < string(b[:])
	})
}

// Index is the slot of a key in the set, −1 if none.
func (s *ValidatorSet) Index(pk [32]byte) int {
	for i, v := range s.Validators {
		if v.PubKey == pk {
			return i
		}
	}
	return -1
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

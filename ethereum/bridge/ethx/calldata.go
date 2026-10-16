package ethx

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// The calldata the machines send to the light client and the mailbox (eth/bridge/PLAN.md §6.1–§6.3, §13), built
// from gnolight's types — the same bytes the Solana programs were staged from, in the interface's tuples:
//
//   - update(headerLeaves, commit, set, nextSet): the sixteen amino leaves of the header; the commit's part set,
//     its round and the precommits for the commit's BlockID that just cross 2/3 of the power — the fewest
//     signatures, the largest powers first, ties by slot, in slot order (a nil or other-block precommit is never
//     carried, the submitter leaves its slot out); the signing set in full; the next set when the header announces a
//     change. When the chosen signatures exceed a per-call bound, the update is staged: the same header, set and
//     next set in every call, the precommits split in slot order, the last call the one that crosses 2/3;
//   - proveHeader(headerLeaves, commit, set): the same for a header at or below the trusted height;
//   - the verify metadata, abi.encode(bytes32 headerHash, uint32 total, uint32 index, bytes32[] aunts, bytes leaf),
//     the aunts in tm2's SimpleProof order (the leaf's sibling first, up to a child of the root);
//   - Mailbox.process(metadata, message).

// Validator is one validator as the client takes it (IGnoLightClient.Validator).
type Validator struct {
	Pubkey [32]byte
	Power  int64
}

// Precommit is one carried precommit (IGnoLightClient.Precommit): its slot, its own timestamp, its signature.
type Precommit struct {
	Index     uint16
	TimeSecs  int64
	TimeNanos int32
	R         [32]byte
	S         [32]byte
}

// Commit is a commit's shared part with the carried precommits (IGnoLightClient.Commit).
type Commit struct {
	PartsTotal uint32
	PartsHash  [32]byte
	Round      int64
	Precommits []Precommit
}

// HeaderLeaves are a header's sixteen amino-encoded fields, the header hash's leaves.
func HeaderLeaves(h *gnolight.Header) [][]byte {
	ls := h.Leaves()
	out := make([][]byte, len(ls))
	for i := range ls {
		out[i] = append([]byte{}, ls[i]...)
	}
	return out
}

// SetArgs is a validator set as the client takes it, in the set's own order.
func SetArgs(vs []gnolight.Validator) []Validator {
	out := make([]Validator, len(vs))
	for i, v := range vs {
		out[i] = Validator{Pubkey: v.PubKey, Power: v.Power}
	}
	return out
}

// SignedHeader is what an update or a header proof carries: the header, its commit, the set that signed it and,
// for an update of a header announcing a change, the set it announces.
type SignedHeader struct {
	ChainID string
	Header  *gnolight.Header
	Commit  *gnolight.Commit
	Set     *gnolight.ValidatorSet
	Next    *gnolight.ValidatorSet
}

// Check is what the chain will check of the bytes, checked first here: the header's sanity and hash, the commit for
// it, the set hashing to its ValidatorsHash, every key ed25519 (by construction), the next set given exactly when
// the header announces a change and hashing to its NextValidatorsHash, a slot per validator.
func (s *SignedHeader) Check() error {
	h := s.Header
	if h == nil || s.Commit == nil || s.Set == nil {
		return errors.New("a signed header needs its header, commit and set")
	}
	if err := h.Check(); err != nil {
		return fmt.Errorf("header %d: %w", h.Height, err)
	}
	if s.ChainID != "" && h.ChainID != s.ChainID {
		return fmt.Errorf("header %d is of chain %q, not %q", h.Height, h.ChainID, s.ChainID)
	}
	hash := h.Hash()
	if string(s.Commit.BlockID.Hash) != string(hash[:]) {
		return fmt.Errorf("header %d: the commit is for block %x, the header hashes to %x", h.Height, s.Commit.BlockID.Hash, hash)
	}
	if err := s.Set.Check(); err != nil {
		return fmt.Errorf("header %d's set: %w", h.Height, err)
	}
	if sh, _ := s.Set.Hash(); sh != h.ValidatorsHashArray() {
		return fmt.Errorf("header %d: the set hashes to %x, its ValidatorsHash is %x", h.Height, sh, h.ValidatorsHash)
	}
	if len(s.Commit.Precommits) != len(s.Set.Validators) {
		return fmt.Errorf("header %d: %d precommit slots for %d validators", h.Height, len(s.Commit.Precommits), len(s.Set.Validators))
	}
	if s.Next != nil {
		if !h.Announcing() {
			return fmt.Errorf("header %d announces no change, yet a next set is given", h.Height)
		}
		if err := s.Next.Check(); err != nil {
			return fmt.Errorf("header %d's next set: %w", h.Height, err)
		}
		if nh, _ := s.Next.Hash(); nh != h.NextValidatorsHashArray() {
			return fmt.Errorf("header %d: the next set hashes to %x, its NextValidatorsHash is %x", h.Height, nh, h.NextValidatorsHash)
		}
	}
	return nil
}

// ChoosePrecommits picks the slots an update carries: among the precommits for the commit's BlockID, of the
// commit's round, whose signatures verify here over their sign bytes (a node that served a bad one is not carried
// into a revert), the fewest whose power is more than 2/3 of the set's — the largest powers first, ties by slot —
// answered in slot order. An error when the verifying precommits do not reach 2/3.
func ChoosePrecommits(chainID string, height int64, set *gnolight.ValidatorSet, cm *gnolight.Commit) ([]int, error) {
	round := cm.Round()
	var cands []int
	for _, i := range cm.Present() {
		if i >= len(set.Validators) || cm.Precommits[i].Round != round || i >= len(cm.Signatures) || len(cm.Signatures[i]) != ed25519.SignatureSize {
			continue
		}
		pk := set.Validators[i].PubKey
		if !ed25519.Verify(pk[:], cm.SignBytes(chainID, height, i), cm.Signatures[i]) {
			continue
		}
		cands = append(cands, i)
	}
	sort.SliceStable(cands, func(a, b int) bool {
		pa, pb := set.Validators[cands[a]].Power, set.Validators[cands[b]].Power
		if pa != pb {
			return pa > pb
		}
		return cands[a] < cands[b]
	})
	total := set.TotalPower()
	var tally int64
	var out []int
	for _, i := range cands {
		out = append(out, i)
		tally += set.Validators[i].Power
		if gnolight.CommitRule(tally, total) {
			sort.Ints(out)
			return out, nil
		}
	}
	return nil, fmt.Errorf("height %d: the precommits that verify carry %d of %d power, not more than 2/3", height, tally, total)
}

// carried is a slot's precommit as the client takes it.
func carried(cm *gnolight.Commit, i int) Precommit {
	p := cm.Precommits[i]
	var r, s [32]byte
	copy(r[:], cm.Signatures[i][:32])
	copy(s[:], cm.Signatures[i][32:64])
	return Precommit{Index: uint16(i), TimeSecs: p.Time.Secs, TimeNanos: p.Time.Nanos, R: r, S: s}
}

// commitArg is the commit with the given slots' precommits.
func commitArg(cm *gnolight.Commit, slots []int) Commit {
	var ph [32]byte
	copy(ph[:], cm.BlockID.Parts.Hash)
	c := Commit{PartsTotal: cm.BlockID.Parts.Total, PartsHash: ph, Round: cm.Round(), Precommits: []Precommit{}}
	for _, i := range slots {
		c.Precommits = append(c.Precommits, carried(cm, i))
	}
	return c
}

// HeaderCall is one call of an update or a header proof: its calldata, the slots it carries, whether it is the
// call that crosses 2/3.
type HeaderCall struct {
	Data  []byte
	Slots []int
	Final bool
}

// chunks splits slots into runs of at most n, in order.
func chunks(slots []int, n int) [][]int {
	if n < 1 {
		n = len(slots)
	}
	var out [][]int
	for len(slots) > n {
		out = append(out, slots[:n])
		slots = slots[n:]
	}
	return append(out, slots)
}

// UpdateCalls builds an update of a signed header: one call when its chosen precommits fit maxSigs (0 for no
// bound), else a staged series, the last one crossing 2/3.
func UpdateCalls(sh *SignedHeader, maxSigs int) ([]HeaderCall, error) {
	if err := sh.Check(); err != nil {
		return nil, err
	}
	if sh.Header.Announcing() && sh.Next == nil {
		return nil, fmt.Errorf("header %d announces a change: the next set must be given", sh.Header.Height)
	}
	slots, err := ChoosePrecommits(sh.Header.ChainID, sh.Header.Height, sh.Set, sh.Commit)
	if err != nil {
		return nil, err
	}
	return updateCallsFor(sh, slots, maxSigs)
}

// UpdateCallsFor builds an update carrying the given slots, in slot order (a staged update's remaining ones: the
// slots an earlier call staged are left out by the caller).
func UpdateCallsFor(sh *SignedHeader, slots []int, maxSigs int) ([]HeaderCall, error) {
	if err := sh.Check(); err != nil {
		return nil, err
	}
	if sh.Header.Announcing() && sh.Next == nil {
		return nil, fmt.Errorf("header %d announces a change: the next set must be given", sh.Header.Height)
	}
	for _, i := range slots {
		if i < 0 || i >= len(sh.Commit.Precommits) || sh.Commit.Precommits[i] == nil || len(sh.Commit.Signatures[i]) != ed25519.SignatureSize {
			return nil, fmt.Errorf("header %d: slot %d carries no precommit", sh.Header.Height, i)
		}
	}
	sorted := append([]int{}, slots...)
	sort.Ints(sorted)
	return updateCallsFor(sh, sorted, maxSigs)
}

func updateCallsFor(sh *SignedHeader, slots []int, maxSigs int) ([]HeaderCall, error) {
	leaves := HeaderLeaves(sh.Header)
	set := SetArgs(sh.Set.Validators)
	next := []Validator{}
	if sh.Next != nil {
		next = SetArgs(sh.Next.Validators)
	}
	parts := chunks(slots, maxSigs)
	out := make([]HeaderCall, 0, len(parts))
	for k, part := range parts {
		data, err := LightClient.Pack("update", leaves, commitArg(sh.Commit, part), set, next)
		if err != nil {
			return nil, err
		}
		out = append(out, HeaderCall{Data: data, Slots: part, Final: k == len(parts)-1})
	}
	return out, nil
}

// ProveHeaderCalls builds a header proof (a header at or below the trusted height, signed by the current set).
func ProveHeaderCalls(sh *SignedHeader, maxSigs int) ([]HeaderCall, error) {
	if err := sh.Check(); err != nil {
		return nil, err
	}
	slots, err := ChoosePrecommits(sh.Header.ChainID, sh.Header.Height, sh.Set, sh.Commit)
	if err != nil {
		return nil, err
	}
	return proveHeaderCallsFor(sh, slots, maxSigs)
}

// ProveHeaderCallsFor builds a header proof carrying the given slots, in slot order (a staged proof's remaining ones:
// the slots an earlier call staged are left out by the caller), as UpdateCallsFor does for an update.
func ProveHeaderCallsFor(sh *SignedHeader, slots []int, maxSigs int) ([]HeaderCall, error) {
	if err := sh.Check(); err != nil {
		return nil, err
	}
	for _, i := range slots {
		if i < 0 || i >= len(sh.Commit.Precommits) || sh.Commit.Precommits[i] == nil || len(sh.Commit.Signatures[i]) != ed25519.SignatureSize {
			return nil, fmt.Errorf("header %d: slot %d carries no precommit", sh.Header.Height, i)
		}
	}
	sorted := append([]int{}, slots...)
	sort.Ints(sorted)
	return proveHeaderCallsFor(sh, sorted, maxSigs)
}

func proveHeaderCallsFor(sh *SignedHeader, slots []int, maxSigs int) ([]HeaderCall, error) {
	leaves := HeaderLeaves(sh.Header)
	set := SetArgs(sh.Set.Validators)
	parts := chunks(slots, maxSigs)
	out := make([]HeaderCall, 0, len(parts))
	for k, part := range parts {
		data, err := LightClient.Pack("proveHeader", leaves, commitArg(sh.Commit, part), set)
		if err != nil {
			return nil, err
		}
		out = append(out, HeaderCall{Data: data, Slots: part, Final: k == len(parts)-1})
	}
	return out, nil
}

// StageKey is the key a staged tally lives under (lane E-M2b's GnoLightClient.stageKey, a pure function):
// keccak256(abi.encode(submitter, headerHash, partsTotal, partsHash, round, epoch, setHash)), with the epoch the
// client's at the call and setHash the set the header must be signed by — for an update the announced set's while
// one stands, else the current set's (ClientState.Expect); for a header proof the current set's. A stage begun under
// another epoch or set is orphaned: its slots count nothing toward this key's tally.
func StageKey(submitter common.Address, sh *SignedHeader, epoch uint64, setHash [32]byte) ([32]byte, error) {
	m, ok := LightClient.ABI.Methods["stageKey"]
	if !ok {
		return [32]byte{}, errors.New("the light client's ABI has no stageKey")
	}
	c := commitArg(sh.Commit, nil)
	b, err := m.Inputs.Pack(submitter, sh.Header.Hash(), c.PartsTotal, c.PartsHash, c.Round, epoch, setHash)
	if err != nil {
		return [32]byte{}, fmt.Errorf("stageKey: %w", err)
	}
	return crypto.Keccak256Hash(b), nil
}

// HeaderCallOf decodes an update's or a header proof's calldata (a staged update's resumption reads which slots
// an earlier call carried): the method, the header hash its leaves give, the round and the slots.
func HeaderCallOf(data []byte) (method string, hash [32]byte, round int64, slots []int, err error) {
	if len(data) < 4 {
		return "", hash, 0, nil, errors.New("calldata shorter than a selector")
	}
	m, err := LightClient.ABI.MethodById(data[:4])
	if err != nil {
		return "", hash, 0, nil, err
	}
	if m.Name != "update" && m.Name != "proveHeader" {
		return m.Name, hash, 0, nil, fmt.Errorf("a call of %s, not an update or a header proof", m.Name)
	}
	vals, err := m.Inputs.Unpack(data[4:])
	if err != nil {
		return m.Name, hash, 0, nil, err
	}
	leaves, ok := vals[0].([][]byte)
	if !ok {
		return m.Name, hash, 0, nil, errors.New("the header leaves do not decode")
	}
	hash, _ = gnolight.SimpleHash(leaves)
	cm, err := convert[Commit](vals[1])
	if err != nil {
		return m.Name, hash, 0, nil, err
	}
	for _, p := range cm.Precommits {
		slots = append(slots, int(p.Index))
	}
	return m.Name, hash, cm.Round, slots, nil
}

var metadataArgs = func() abi.Arguments {
	t := func(s string) abi.Type {
		ty, err := abi.NewType(s, "", nil)
		if err != nil {
			panic(err)
		}
		return ty
	}
	return abi.Arguments{{Type: t("bytes32")}, {Type: t("uint32")}, {Type: t("uint32")}, {Type: t("bytes32[]")}, {Type: t("bytes")}}
}()

// VerifyMetadata is the light client's verify metadata for a message under an accepted header: the header's hash
// (header H+1, whose LastResultsHash commits to block H's results), the SimpleProof of the results leaf, the leaf.
func VerifyMetadata(headerHash [32]byte, proof *gnolight.SimpleProof, leaf []byte) ([]byte, error) {
	if proof == nil {
		return nil, errors.New("no proof")
	}
	if gnolight.LeafHash(leaf) != proof.LeafHash {
		return nil, errors.New("the leaf is not the proof's")
	}
	aunts := make([][32]byte, len(proof.Aunts))
	copy(aunts, proof.Aunts)
	return metadataArgs.Pack(headerHash, proof.Total, proof.Index, aunts, leaf)
}

// MetadataOf decodes verify metadata (the tests' round trip, and a reader of a delivery's calldata).
func MetadataOf(b []byte) (headerHash [32]byte, total, index uint32, aunts [][32]byte, leaf []byte, err error) {
	vals, err := metadataArgs.Unpack(b)
	if err != nil {
		return headerHash, 0, 0, nil, nil, err
	}
	return vals[0].([32]byte), vals[1].(uint32), vals[2].(uint32), vals[3].([][32]byte), vals[4].([]byte), nil
}

// ProcessCall is Mailbox.process(metadata, message).
func ProcessCall(metadata, message []byte) ([]byte, error) {
	return Mailbox.Pack("process", metadata, message)
}

// ExecuteCall is HlAdmin.execute(number).
func ExecuteCall(number uint64) ([]byte, error) { return Admin.Pack("execute", number) }

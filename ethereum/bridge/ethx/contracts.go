package ethx

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// Typed readers of the contracts' state, through one provider (*Client) or two that must agree (Pair), at one
// state (a tag for one provider; a pinned block for a pair). The structs mirror the interfaces' (field order and
// types as the ABI's tuples: the decoder copies by position).

// Reader is what a view is read through.
type Reader interface {
	CallAt(ctx context.Context, to common.Address, data []byte, at At) ([]byte, error)
	CallBatch(ctx context.Context, reqs []CallReq, at At) ([][]byte, []error, error)
}

// ErrEmptyAnswer is a view call answered with no bytes where the ABI has outputs: no contract at the address, or one
// that does not have the view (a fallback that returns nothing).
var ErrEmptyAnswer = errors.New("an empty answer")

// view is one view call to make and where its decoded value goes.
type view struct {
	c      *Contract
	to     common.Address
	method string
	args   []any
	into   func(out []any) error
}

// batchSizer is a reader that says how many calls one batch of it may carry (a Client: its provider's; a Pair: the
// smaller of its two).
type batchSizer interface{ BatchSize() int }

// batchSizeOf is the most calls readViews puts in one batch to a reader: its own BatchSize, else DefaultBatchSize.
func batchSizeOf(r Reader) int {
	if b, ok := r.(batchSizer); ok && b.BatchSize() > 0 {
		return b.BatchSize()
	}
	return DefaultBatchSize
}

// readViews makes every view at one state in batches of at most the reader's batch size (E5-GS4: one batch of every
// view failed whole past a provider's cap) — each alone when the provider refuses a batch — and decodes each into its
// place; the first failure is the answer.
func readViews(ctx context.Context, r Reader, at At, vs []view) error {
	reqs := make([]CallReq, len(vs))
	for i, v := range vs {
		data, err := v.c.Pack(v.method, v.args...)
		if err != nil {
			return err
		}
		reqs[i] = CallReq{To: v.to, Data: data}
	}
	res, errs := make([][]byte, len(reqs)), make([]error, len(reqs))
	size := batchSizeOf(r)
	for lo := 0; lo < len(reqs); lo += size {
		hi := min(lo+size, len(reqs))
		out, es, err := r.CallBatch(ctx, reqs[lo:hi], at)
		if err != nil || len(out) != hi-lo || len(es) != hi-lo {
			for i := lo; i < hi; i++ {
				res[i], errs[i] = r.CallAt(ctx, reqs[i].To, reqs[i].Data, at)
			}
			continue
		}
		copy(res[lo:hi], out)
		copy(errs[lo:hi], es)
	}
	for i, v := range vs {
		if errs[i] != nil {
			return fmt.Errorf("%s.%s at %s: %w", v.c.Name, v.method, at, errs[i])
		}
		if len(res[i]) == 0 && len(v.c.ABI.Methods[v.method].Outputs) > 0 {
			return fmt.Errorf("%s.%s at %s: %w — no contract at %s, or not a %s", v.c.Name, v.method, at, ErrEmptyAnswer, v.to.Hex(), v.c.Name)
		}
		out, err := v.c.Unpack(v.method, res[i])
		if err != nil {
			return err
		}
		if err := v.into(out); err != nil {
			return fmt.Errorf("%s.%s: %w", v.c.Name, v.method, err)
		}
	}
	return nil
}

// convert copies a decoded value into a typed one (by position for a tuple); a mismatch is an error, never a panic.
func convert[T any](in any) (out T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("decoding %T into %T: %v", in, out, r)
		}
	}()
	p := abi.ConvertType(in, new(T)).(*T)
	return *p, nil
}

func into[T any](dst *T) func([]any) error {
	return func(out []any) error {
		if len(out) != 1 {
			return fmt.Errorf("%d values, 1 expected", len(out))
		}
		v, err := convert[T](out[0])
		if err != nil {
			return err
		}
		*dst = v
		return nil
	}
}

// --------------------------------------------------------------- the client --

// Entry is a header the client accepted or proved (IGnoLightClient.Entry): its epoch then, its height, its
// LastResultsHash.
type Entry struct {
	Epoch           uint64
	Height          int64
	LastResultsHash [32]byte
}

// SetEntry is one entry of the client's set history.
type SetEntry struct {
	FromHeight     int64
	SetHash        [32]byte
	AnnouncedBlock uint64
	AnnouncedTime  uint64
	QuarantineEnd  uint64
}

// ClientState is the light client's state as the machines read it.
type ClientState struct {
	Address            common.Address
	TrustedHeight      int64
	TrustedHash        [32]byte
	TrustedTime        uint64
	SetHash            [32]byte
	NextSetHash        [32]byte // zero when no change is announced
	Epoch              uint64
	QuarantineEnd      uint64
	ChainID            string
	OriginDomain       uint32
	MailboxPkgPathHash [32]byte
	ClockFactor        uint64
	Records            uint64
	RunningHash        [32]byte
	Admin              common.Address
	HistoryLength      uint64
}

// Announced says whether the client waits for an announced set (its next header must be the new set's).
func (s *ClientState) Announced() bool { return s.NextSetHash != [32]byte{} }

// Expect is the set hash the client's next update must be signed by: the announced set's when one is announced,
// else the current one's.
func (s *ClientState) Expect() [32]byte {
	if s.Announced() {
		return s.NextSetHash
	}
	return s.SetHash
}

// ReadClient reads the client's state at one state.
func ReadClient(ctx context.Context, r Reader, addr common.Address, at At) (*ClientState, error) {
	s := &ClientState{Address: addr}
	var hl *big.Int
	c := LightClient
	vs := []view{
		{c, addr, "trustedHeight", nil, into(&s.TrustedHeight)},
		{c, addr, "trustedHash", nil, into(&s.TrustedHash)},
		{c, addr, "trustedTime", nil, into(&s.TrustedTime)},
		{c, addr, "setHash", nil, into(&s.SetHash)},
		{c, addr, "nextSetHash", nil, into(&s.NextSetHash)},
		{c, addr, "epoch", nil, into(&s.Epoch)},
		{c, addr, "quarantineEnd", nil, into(&s.QuarantineEnd)},
		{c, addr, "chainId", nil, into(&s.ChainID)},
		{c, addr, "originDomain", nil, into(&s.OriginDomain)},
		{c, addr, "mailboxPkgPathHash", nil, into(&s.MailboxPkgPathHash)},
		{c, addr, "clockFactor", nil, into(&s.ClockFactor)},
		{c, addr, "records", nil, into(&s.Records)},
		{c, addr, "runningHash", nil, into(&s.RunningHash)},
		{c, addr, "admin", nil, into(&s.Admin)},
		{c, addr, "historyLength", nil, into(&hl)},
	}
	if err := readViews(ctx, r, at, vs); err != nil {
		return nil, err
	}
	if hl != nil {
		s.HistoryLength = hl.Uint64()
	}
	return s, nil
}

// ReadTrust is the client's trusted height and epoch at one state (two views of ReadClient's fifteen).
func ReadTrust(ctx context.Context, r Reader, addr common.Address, at At) (trusted int64, epoch uint64, err error) {
	c := LightClient
	err = readViews(ctx, r, at, []view{{c, addr, "trustedHeight", nil, into(&trusted)}, {c, addr, "epoch", nil, into(&epoch)}})
	return trusted, epoch, err
}

// ReadEntry is the client's entry for a header hash (zero when it has none).
func ReadEntry(ctx context.Context, r Reader, client common.Address, headerHash [32]byte, at At) (Entry, error) {
	var e Entry
	err := readViews(ctx, r, at, []view{{LightClient, client, "entry", []any{headerHash}, into(&e)}})
	return e, err
}

// ReadEntries reads several headers' entries in one batch, in order.
func ReadEntries(ctx context.Context, r Reader, client common.Address, hashes [][32]byte, at At) ([]Entry, error) {
	out := make([]Entry, len(hashes))
	vs := make([]view, len(hashes))
	for i, h := range hashes {
		vs[i] = view{LightClient, client, "entry", []any{h}, into(&out[i])}
	}
	if err := readViews(ctx, r, at, vs); err != nil {
		return nil, err
	}
	return out, nil
}

// Proves says whether an entry proves messages now: recorded at the client's current epoch with a results hash
// (PLAN.md §6.2: a header of a rotated-out set proves nothing after the client followed the rotation).
func (e Entry) Proves(epoch uint64) bool {
	return e.Height > 0 && e.Epoch == epoch && e.LastResultsHash != [32]byte{}
}

// ReadHistory is the client's set history, oldest first.
func ReadHistory(ctx context.Context, r Reader, client common.Address, n uint64, at At) ([]SetEntry, error) {
	out := make([]SetEntry, n)
	vs := make([]view, n)
	for i := range out {
		vs[i] = view{LightClient, client, "history", []any{big.NewInt(int64(i))}, into(&out[i])}
	}
	if err := readViews(ctx, r, at, vs); err != nil {
		return nil, err
	}
	return out, nil
}

// StagePower is a stage's tally so far (zero for none).
func StagePower(ctx context.Context, r Reader, client common.Address, key [32]byte, at At) (int64, error) {
	var p int64
	err := readViews(ctx, r, at, []view{{LightClient, client, "stagePower", []any{key}, into(&p)}})
	return p, err
}

// ----------------------------------------------------------------- the gate --

// Flag is a flag or a hold's moment (IPausableGate.Flag): the block and time it was set; zero block = none.
type Flag struct {
	BlockNumber uint64
	Time        uint64
}

// Set says whether the flag stands.
func (f Flag) Set() bool { return f.BlockNumber != 0 }

// MintLimit is a token's mint limits (IPausableGate.MintLimit).
type MintLimit struct {
	MaxPerMessage uint64
	DayCap        uint64
	Used          uint64
	UsedAt        uint64
}

// Level is the bucket's level at a time: what was charged, less what drained since at DayCap per day (day is 24
// hours divided by the clock factor) — the gate's arithmetic (the Solana pausable ISM's, sol.MintLimit.Level).
func (l MintLimit) Level(now, day int64) uint64 {
	elapsed := now - int64(l.UsedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	if day < 1 {
		day = 1
	}
	drained := new(big.Int).Mul(big.NewInt(elapsed), new(big.Int).SetUint64(l.DayCap))
	drained.Div(drained, big.NewInt(day))
	if !drained.IsUint64() || drained.Uint64() >= l.Used {
		return 0
	}
	return l.Used - drained.Uint64()
}

// GateState is the PausableGate's state.
type GateState struct {
	Address     common.Address
	Owner       common.Address
	Mailbox     common.Address
	Client      common.Address
	Tokens      [2]common.Address
	ClockFactor uint64
	Keys        [6]common.Address
	Flags       [6]Flag
	Hold        Flag // the standing hold's start, zero when none stands at the read's block time
	HoldSince   Flag
	DipSince    Flag
	Limits      [2]MintLimit
}

// AnyFlag says whether any seat's or watch slot's flag stands (one flag pauses deliveries).
func (g *GateState) AnyFlag() bool {
	for _, f := range g.Flags {
		if f.Set() {
			return true
		}
	}
	return false
}

// ReadGate reads the gate's state at one state.
func ReadGate(ctx context.Context, r Reader, addr common.Address, at At) (*GateState, error) {
	g := &GateState{Address: addr}
	c := Gate
	vs := []view{
		{c, addr, "owner", nil, into(&g.Owner)},
		{c, addr, "mailbox", nil, into(&g.Mailbox)},
		{c, addr, "client", nil, into(&g.Client)},
		{c, addr, "token", []any{uint8(0)}, into(&g.Tokens[0])},
		{c, addr, "token", []any{uint8(1)}, into(&g.Tokens[1])},
		{c, addr, "clockFactor", nil, into(&g.ClockFactor)},
		{c, addr, "keys", nil, into(&g.Keys)},
		{c, addr, "flags", nil, into(&g.Flags)},
		{c, addr, "hold", nil, into(&g.Hold)},
		{c, addr, "holdSince", nil, into(&g.HoldSince)},
		{c, addr, "dipSince", nil, into(&g.DipSince)},
		{c, addr, "limits", []any{uint8(0)}, into(&g.Limits[0])},
		{c, addr, "limits", []any{uint8(1)}, into(&g.Limits[1])},
	}
	if err := readViews(ctx, r, at, vs); err != nil {
		return nil, err
	}
	return g, nil
}

// ---------------------------------------------------------------- the admin --

// Hold is a hold that ended (IHlAdmin.Hold).
type Hold struct {
	Start uint64
	End   uint64
}

// Decision is a pending decision of HlAdmin (IHlAdmin.Decision).
type Decision struct {
	Number         uint64
	Kind           uint8
	DeliveredBlock uint64
	DeliveredTime  uint64
	Deliverer      common.Address
	Params         []byte
}

// AdminState is HlAdmin's state.
type AdminState struct {
	Address        common.Address
	LastSeq        uint64
	Pending        uint32
	Keys           [6]common.Address
	Flags          [6]Flag
	Hold           Flag
	Holds          []Hold
	Cancelled      [][2]uint64
	SetupDone      bool
	Successor      common.Address
	Predecessor    common.Address
	Inherited      bool
	ClockFactor    uint64
	Origin         uint32
	Hlgov          [32]byte
	Mailbox        common.Address
	Client         common.Address
	Gate           common.Address
	Tokens         [2]common.Address
	ProxyAdmin     common.Address
	MerkleTreeHook common.Address
	ProtocolFee    common.Address
}

// AnyFlag says whether any flag stands on HlAdmin (a lone flag defers the 7-day group but kind 5, §6.5).
func (s *AdminState) AnyFlag() bool {
	for _, f := range s.Flags {
		if f.Set() {
			return true
		}
	}
	return false
}

// IsCancelled says whether a number lies in a cancelled range.
func (s *AdminState) IsCancelled(n uint64) bool {
	for _, r := range s.Cancelled {
		if r[0] <= n && n <= r[1] {
			return true
		}
	}
	return false
}

// Retired says whether HlAdmin handed every authority to a successor (kind 9): it records nothing more.
func (s *AdminState) Retired() bool { return s.Successor != (common.Address{}) }

// ReadAdmin reads HlAdmin's state at one state.
func ReadAdmin(ctx context.Context, r Reader, addr common.Address, at At) (*AdminState, error) {
	s := &AdminState{Address: addr}
	c := Admin
	vs := []view{
		{c, addr, "lastSeq", nil, into(&s.LastSeq)},
		{c, addr, "pending", nil, into(&s.Pending)},
		{c, addr, "keys", nil, into(&s.Keys)},
		{c, addr, "flags", nil, into(&s.Flags)},
		{c, addr, "hold", nil, into(&s.Hold)},
		{c, addr, "holds", nil, into(&s.Holds)},
		{c, addr, "cancelledRanges", nil, into(&s.Cancelled)},
		{c, addr, "setupDone", nil, into(&s.SetupDone)},
		{c, addr, "successor", nil, into(&s.Successor)},
		{c, addr, "predecessor", nil, into(&s.Predecessor)},
		{c, addr, "inherited", nil, into(&s.Inherited)},
		{c, addr, "clockFactor", nil, into(&s.ClockFactor)},
		{c, addr, "origin", nil, into(&s.Origin)},
		{c, addr, "hlgov", nil, into(&s.Hlgov)},
		{c, addr, "mailbox", nil, into(&s.Mailbox)},
		{c, addr, "client", nil, into(&s.Client)},
		{c, addr, "gate", nil, into(&s.Gate)},
		{c, addr, "token", []any{uint8(0)}, into(&s.Tokens[0])},
		{c, addr, "token", []any{uint8(1)}, into(&s.Tokens[1])},
		{c, addr, "proxyAdmin", nil, into(&s.ProxyAdmin)},
		{c, addr, "merkleTreeHook", nil, into(&s.MerkleTreeHook)},
		{c, addr, "protocolFee", nil, into(&s.ProtocolFee)},
	}
	if err := readViews(ctx, r, at, vs); err != nil {
		return nil, err
	}
	return s, nil
}

// SuccessorState is what HlAdmin's kind 9 reads of the successor before it hands every authority over
// (HlAdminExec._transfer): its predecessor, whether it has inherited, its own successor, its clock factor.
type SuccessorState struct {
	Predecessor common.Address
	Inherited   bool
	Successor   common.Address
	ClockFactor uint64
}

// ReadSuccessor reads a kind 9's successor's four answers at one state — the four views HlAdmin's execute calls, and
// no other (a successor of a later build need answer nothing more).
func ReadSuccessor(ctx context.Context, r Reader, addr common.Address, at At) (*SuccessorState, error) {
	s := &SuccessorState{}
	c := Admin
	vs := []view{
		{c, addr, "predecessor", nil, into(&s.Predecessor)},
		{c, addr, "inherited", nil, into(&s.Inherited)},
		{c, addr, "successor", nil, into(&s.Successor)},
		{c, addr, "clockFactor", nil, into(&s.ClockFactor)},
	}
	if err := readViews(ctx, r, at, vs); err != nil {
		return nil, err
	}
	return s, nil
}

// LaunchLine is the launch line (PLAN.md §5.6, §5.8 step 10; M2-NOTES.md Fold F5, F5.1 item 12): the mailbox's nonce
// a launch HlAdmin's finishSetup recorded as launchNonce(). A message numbered below it was dispatched before the
// launch — a stranger's after step 9, or the deployer's while it owned the mailbox and the tokens (a mint through a
// stand-in ISM and a burn back) — and is none of the bridge's burns: the signers, the monitors, the inbound relayer and
// hlwatch count none. A block number cannot draw it (the deployer's burn can sit in finishSetup's own block); the
// line is read from the launch HlAdmin — a successor's launchNonce() is 0, so predecessor() is followed back to the one
// that has none.
type LaunchLine struct {
	Nonce  uint32
	Launch common.Address // the launch HlAdmin, whose launchNonce() it is
}

// launchHops bounds the predecessors followed back to the launch HlAdmin.
const launchHops = 8

// ReadLaunchLine reads the launch line at a block, from the configured HlAdmin back through its predecessors.
func ReadLaunchLine(ctx context.Context, r Reader, admin common.Address, at At) (LaunchLine, error) {
	cur, seen := admin, map[common.Address]bool{admin: true}
	for hop := 0; ; hop++ {
		var pred common.Address
		if err := readViews(ctx, r, at, []view{{Admin, cur, "predecessor", nil, into(&pred)}}); err != nil {
			return LaunchLine{}, fmt.Errorf("the launch line (HlAdmin.launchNonce()): %w", err)
		}
		if pred == (common.Address{}) {
			break
		}
		if hop >= launchHops || seen[pred] {
			return LaunchLine{}, fmt.Errorf("the launch line: HlAdmin %s's predecessors run past %d hops or loop at %s", admin.Hex(), launchHops, pred.Hex())
		}
		seen[pred] = true
		cur = pred
	}
	l := LaunchLine{Launch: cur}
	if err := readViews(ctx, r, at, []view{{Admin, cur, "launchNonce", nil, into(&l.Nonce)}}); err != nil {
		return LaunchLine{}, fmt.Errorf("the launch line (HlAdmin.launchNonce()): %w", err)
	}
	return l, nil
}

// Bindings are an HlAdmin's bindings as its views answer them: every contract it holds or reads, the ed25519 verifier
// and its code hash, the domains, r/hlgov, the routes — and the hlmailbox path's hash where its build answers one. A
// kind 9's successor must answer the eleven HlAdmin compares the same as the HlAdmin it succeeds (PLAN.md §6.5; the
// plan red team's E2-RT3; HlAdminExec._sameBindings): they live in its immutables, which the decision's code hash pins
// and no reader of the hash sees. The other five — the verifier and its code hash, the two routes, the path's hash —
// kind 9 never reads (fold F8): a successor of a later build need not answer them at all.
type Bindings struct {
	Mailbox, Client, Gate                            common.Address
	Tokens                                           [2]common.Address
	ProxyAdmin, MerkleTreeHook, ProtocolFee, Ed25519 common.Address
	Ed25519CodeHash                                  [32]byte
	LocalDomain, Origin                              uint32
	Hlgov                                            [32]byte
	Routes                                           [2][32]byte
	MailboxPkgPathHash                               [32]byte
	// Unread are the optional views (OptionalBindings' names) that did not answer — a revert, or no bytes — each with
	// why; their fields stay zero. Empty when all five answered.
	Unread map[string]string
}

// RequiredBindings are the eleven views kind 9 compares between HlAdmin and its successor (HlAdminExec._sameBindings),
// and OptionalBindings the five it never reads (E3-CF10), by the names Bindings.Unread uses.
var (
	RequiredBindings = []string{"mailbox()", "client()", "gate()", "token(0)", "token(1)", "proxyAdmin()", "merkleTreeHook()", "protocolFee()", "localDomain()", "origin()", "hlgov()"}
	OptionalBindings = []string{"ed25519()", "ed25519CodeHash()", "route(0)", "route(1)", "mailboxPkgPathHash()"}
)

// Answered says whether an optional view (OptionalBindings' name) answered; a required one always did.
func (b *Bindings) Answered(name string) bool {
	_, unread := b.Unread[name]
	return !unread
}

// ReadBindings reads an HlAdmin's bindings at one state: the eleven kind 9 compares in one batch, any failure the
// answer; then the five it never reads apart (E3-CF10), so a successor whose optional views revert — a later build
// that dropped them — still reads: such a view's field stays zero and Unread names it. A failure that is no answer of
// the contract's (a provider's error) is the answer for an optional view too.
func ReadBindings(ctx context.Context, r Reader, addr common.Address, at At) (*Bindings, error) {
	b := &Bindings{}
	c := Admin
	required := []view{
		{c, addr, "mailbox", nil, into(&b.Mailbox)},
		{c, addr, "client", nil, into(&b.Client)},
		{c, addr, "gate", nil, into(&b.Gate)},
		{c, addr, "token", []any{uint8(0)}, into(&b.Tokens[0])},
		{c, addr, "token", []any{uint8(1)}, into(&b.Tokens[1])},
		{c, addr, "proxyAdmin", nil, into(&b.ProxyAdmin)},
		{c, addr, "merkleTreeHook", nil, into(&b.MerkleTreeHook)},
		{c, addr, "protocolFee", nil, into(&b.ProtocolFee)},
		{c, addr, "localDomain", nil, into(&b.LocalDomain)},
		{c, addr, "origin", nil, into(&b.Origin)},
		{c, addr, "hlgov", nil, into(&b.Hlgov)},
	}
	if err := readViews(ctx, r, at, required); err != nil {
		return nil, err
	}
	optional := []view{
		{c, addr, "ed25519", nil, into(&b.Ed25519)},
		{c, addr, "ed25519CodeHash", nil, into(&b.Ed25519CodeHash)},
		{c, addr, "route", []any{uint8(0)}, into(&b.Routes[0])},
		{c, addr, "route", []any{uint8(1)}, into(&b.Routes[1])},
		{c, addr, "mailboxPkgPathHash", nil, into(&b.MailboxPkgPathHash)},
	}
	if readViews(ctx, r, at, optional) == nil {
		return b, nil
	}
	// one by one: which answered (a view whose read fails sets nothing)
	for i, v := range optional {
		err := readViews(ctx, r, at, []view{v})
		if err == nil {
			continue
		}
		if _, reverted := AsRevert(err); !reverted && !errors.Is(err, ErrEmptyAnswer) {
			return nil, err
		}
		if b.Unread == nil {
			b.Unread = map[string]string{}
		}
		b.Unread[OptionalBindings[i]] = err.Error()
	}
	return b, nil
}

// CodeTarget is the address whose code a pending kind 8 (its implementation) or kind 9 (its successor) waits for —
// HlAdmin refuses its execute NoCodeYet while that address has no code (fold F2) — as PLAN.md §13 lays out their
// parameters (kind 8: proxy 32 ‖ implementation 32 ‖ codehash 32 ‖ call; kind 9: successor 32 ‖ codehash 32); false
// for another kind, or parameters that carry no address (HlAdmin spends those).
func CodeTarget(kind uint8, params []byte) (common.Address, bool) {
	var w [32]byte
	switch {
	case kind == 8 && len(params) >= 96:
		copy(w[:], params[32:64])
	case kind == 9 && len(params) >= 64:
		copy(w[:], params[0:32])
	default:
		return common.Address{}, false
	}
	return AddressFrom32(w)
}

// ReadDecisions reads the decisions of the numbers asked, in order; nil where none is pending (a zero record, or
// a read the contract refuses).
func ReadDecisions(ctx context.Context, r Reader, admin common.Address, numbers []uint64, at At) ([]*Decision, error) {
	out := make([]*Decision, len(numbers))
	if len(numbers) == 0 {
		return out, nil
	}
	reqs := make([]CallReq, len(numbers))
	for i, n := range numbers {
		data, err := Admin.Pack("decision", n)
		if err != nil {
			return nil, err
		}
		reqs[i] = CallReq{To: admin, Data: data}
	}
	res, errs, err := r.CallBatch(ctx, reqs, at)
	if err != nil {
		res, errs = make([][]byte, len(reqs)), make([]error, len(reqs))
		for i, q := range reqs {
			res[i], errs[i] = r.CallAt(ctx, q.To, q.Data, at)
		}
	}
	for i := range numbers {
		if errs[i] != nil {
			if _, ok := AsRevert(errs[i]); ok {
				continue // the contract refuses a number it holds nothing for
			}
			return nil, errs[i]
		}
		vals, err := Admin.Unpack("decision", res[i])
		if err != nil {
			return nil, err
		}
		d, err := convert[Decision](vals[0])
		if err != nil {
			return nil, err
		}
		if d.Number == 0 && d.DeliveredBlock == 0 {
			continue
		}
		out[i] = &d
	}
	return out, nil
}

// ------------------------------------------------------------ the Hyperlane side --

// Delivered says whether the mailbox processed a message id.
func Delivered(ctx context.Context, r Reader, mailbox common.Address, id [32]byte, at At) (bool, error) {
	var ok bool
	err := readViews(ctx, r, at, []view{{Mailbox, mailbox, "delivered", []any{id}, into(&ok)}})
	return ok, err
}

// DeliveredMany reads several ids' delivered marks in one batch.
func DeliveredMany(ctx context.Context, r Reader, mailbox common.Address, ids [][32]byte, at At) ([]bool, error) {
	out := make([]bool, len(ids))
	vs := make([]view, len(ids))
	for i, id := range ids {
		vs[i] = view{Mailbox, mailbox, "delivered", []any{id}, into(&out[i])}
	}
	if err := readViews(ctx, r, at, vs); err != nil {
		return nil, err
	}
	return out, nil
}

// TotalSupply is a token's supply in base units.
func TotalSupply(ctx context.Context, r Reader, token common.Address, at At) (*big.Int, error) {
	var s *big.Int
	err := readViews(ctx, r, at, []view{{HypERC20, token, "totalSupply", nil, into(&s)}})
	return s, err
}

// RouterFor is the router a token enrolled for a domain (zero for none).
func RouterFor(ctx context.Context, r Reader, token common.Address, domain uint32, at At) ([32]byte, error) {
	var b [32]byte
	err := readViews(ctx, r, at, []view{{HypERC20, token, "routers", []any{domain}, into(&b)}})
	return b, err
}

// ------------------------------------------------------------------ helpers --

// AddressTo32 is an Ethereum address as Hyperlane carries one: twelve zero bytes, then the twenty.
func AddressTo32(a common.Address) [32]byte {
	var out [32]byte
	copy(out[12:], a[:])
	return out
}

// AddressFrom32 is the address in 32 bytes whose top twelve are zero; false otherwise (Hyperlane's
// bytes32ToAddress reverts on them).
func AddressFrom32(b [32]byte) (common.Address, bool) {
	for _, x := range b[:12] {
		if x != 0 {
			return common.Address{}, false
		}
	}
	return common.BytesToAddress(b[12:]), true
}

// ParseAddress reads an Ethereum address a person typed — a config's, a flag's (hleth's -seats, -watch,
// -beneficiary, -token, -owner, a decision body's fields; hlwatch's), the machines' config — as 0x and 40 hex digits in
// its EIP-55 case exactly (eth/bridge/PLAN.md §5.2: "the site and the tools accept only EIP-55 checksummed input").
// An address in one case carries no checksum, so a typo in it would be taken silently — a seat or a watch key nobody
// holds, a beneficiary nobody owns — and is refused, unless its EIP-55 form is itself one case (no letter, or every
// letter's hash nibble on one side). The refusal names no checksummed form: that form would be a typo's checksum too
// (round 3's E3-CF6).
func ParseAddress(s string) (common.Address, error) {
	if !common.IsHexAddress(s) || len(s) != 42 || s[:2] != "0x" {
		return common.Address{}, fmt.Errorf("%q is not an address (0x and 40 hex digits)", s)
	}
	a := common.HexToAddress(s)
	if a.Hex() == s {
		return a, nil
	}
	lower, upper := true, true
	for _, c := range s[2:] {
		if c >= 'a' && c <= 'f' {
			upper = false
		}
		if c >= 'A' && c <= 'F' {
			lower = false
		}
	}
	if lower || upper {
		return common.Address{}, errors.New(s + " is all in one case, so it carries no EIP-55 checksum and a typo in it would go unseen: copy it again in its checksummed (mixed) case")
	}
	return common.Address{}, errors.New(s + " is mixed case and not its EIP-55 checksum (a typo?)")
}

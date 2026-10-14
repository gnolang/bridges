package sol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/gagliardetto/solana-go"
)

// The admin program (bridge/programs/admin-program): its PDAs, its instructions, the kind table (§13: parameters
// big-endian), its state and decision accounts, and the accounts each kind's Execute needs after the six fixed
// ones (kinds.rs::describe). One channel (M2j): no channel byte in Execute, StageUpgrade, CloseCancelled or a
// decision's PDA.

const (
	MaxCancel = 16

	KindCancel      = 1
	KindUnpause     = 2
	KindSetTokenISM = 3
	// KindReserved is kind 4 (v27 §13): the client's state layout the pausable ISM and the admin program read is
	// fixed at version 1 for the deployment's life, so the kind has no builder; the program records one like any
	// decision of the 7-day group and spends it at Execute, whatever its bytes.
	KindReserved     = 4
	KindSetGuardians = 5
	KindEnrollRouter = 6
	KindSetFee       = 7
	KindUpgrade      = 8
	KindTransfer     = 9
	// Kinds 10 and 11 are reserved (§13): unknown, recorded and spent at recording like any unknown kind.
	// KindSetMintLimits is kind 12 (AN5, plan v29 §13): a token's per-message maximum and rolling day cap on the
	// pausable ISM (token u8 ‖ max u64 ‖ day cap u64, base units; the 7-day group).
	KindSetMintLimits = 12

	// MaxProtocolFee is the mailbox's max_protocol_fee at init (0.05 SOL), which finish_setup reads back.
	MaxProtocolFee = 50_000_000
	// MaxHolds is how many ended holds the admin state keeps (AN4); a ninth merges the two oldest.
	MaxHolds = 8
	// MaxCancelled is how many cancelled ranges the admin state keeps.
	MaxCancelled = 64
)

// Programs are the six deployments' ids (§5.8), which the admin program's Execute names by position.
type Programs struct {
	Mailbox  solana.PublicKey
	Tokens   [2]solana.PublicKey // 0 GNOT, 1 BUBBLE
	Client   solana.PublicKey
	Pausable solana.PublicKey
	Admin    solana.PublicKey
}

func (p Programs) LightClient() LightClient { return LightClient{Program: p.Client} }
func (p Programs) PausableISM() Pausable    { return Pausable{Program: p.Pausable} }
func (p Programs) AdminProgram() Admin      { return Admin{Program: p.Admin} }
func (p Programs) MailboxProgram() Mailbox  { return Mailbox{Program: p.Mailbox} }

// Admin is one deployment of the program.
type Admin struct{ Program solana.PublicKey }

func (a Admin) State() solana.PublicKey     { return PDA(a.Program, b("state")) }
func (a Admin) Authority() solana.PublicKey { return PDA(a.Program, b("authority")) }

// Decision is the pending decision's PDA: (decision, number big-endian) — no channel byte since M2j.
func (a Admin) Decision(number uint64) solana.PublicKey {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], number)
	return PDA(a.Program, b("decision"), n[:])
}

// HoldReplacementDays is how long after a hold's start a replacement of guardians (kind 5) may land through it
// (plan v26: the 21st day; v25 had the 15th).
const HoldReplacementDays = 21

// InitIx is the one-shot init: the guardian lists as init data, the origin domain, the predecessor — zero for
// the launch deployment — and the number the sequence starts at, the last field (AT3: 0 for a chain's first
// deployment, r/hlgov's Last() for a fresh deployment under the same r/hlgov, §6.4's exit, since r/hlgov's
// counter never goes down and a program started at 0 would refuse its next decision OutOfOrder for ever;
// `hlsol setup -start-sequence` sets it, checked against r/hlgov). A successor (C227) names its predecessor's
// program id and gives zero seats, watch slots and number (the live numbers, seats, watch slots and hold history
// reach it by SuccessorInit at the transfer, a CPI of the predecessor's kind 9 that no tool sends), and takes no
// decision before it inherited.
func (a Admin) InitIx(payer, authority solana.PublicKey, seats [Seats]solana.PublicKey, watch [Watch]solana.PublicKey, origin uint32, predecessor solana.PublicKey, lastSeq uint64) solana.Instruction {
	var e enc
	e.u8(0)
	for _, k := range seats {
		e.key(k)
	}
	for _, k := range watch {
		e.key(k)
	}
	e.u32(origin)
	e.key(predecessor)
	e.u64(lastSeq)
	return ix(a.Program, e.Bytes(), w(a.State()), ws(payer), rs(authority), r(a.Program), r(ProgramData(a.Program)), r(SystemProgram))
}

// SuccessorInitIx is a successor's init (C227): zero seats, watch slots and number, its predecessor named; sent
// before the successor's upgrade authority is burned (kind 9 requires an immutable successor).
func (a Admin) SuccessorInitIx(payer, authority solana.PublicKey, origin uint32, predecessor solana.PublicKey) solana.Instruction {
	return a.InitIx(payer, authority, [Seats]solana.PublicKey{}, [Watch]solana.PublicKey{}, origin, predecessor, 0)
}

// SuccessorInitData is the hand-over's instruction data (tag 8; S-21, the wire contract the predecessor's kind 9
// sends the successor by CPI — no tool sends it): the origin domain, the last number, the seats, the watch slots
// and the hold history, as the vectors pin it.
func SuccessorInitData(origin uint32, lastSeq uint64, seats [Seats]solana.PublicKey, watch [Watch]solana.PublicKey, holds []Hold) []byte {
	var e enc
	e.u8(8)
	e.u32(origin)
	e.u64(lastSeq)
	for _, k := range seats {
		e.key(k)
	}
	for _, k := range watch {
		e.key(k)
	}
	e.u32(uint32(len(holds)))
	for _, h := range holds {
		e.i64(h.Start)
		e.i64(h.End)
	}
	return e.Bytes()
}

// TransferAuthorityInIx moves a program's upgrade authority to the authority PDA (SetAuthorityChecked, the PDA
// co-signing), only before finish_setup; the current authority signs.
func (a Admin) TransferAuthorityInIx(program, current solana.PublicKey) solana.Instruction {
	return ix(a.Program, []byte{1}, r(a.State()), r(a.Authority()), w(ProgramData(program)), rs(current), r(LoaderID))
}

// FinishSetupIx is the permissionless, self-checking end of the setup step; account 14 is the pausable ISM's
// flags (S-06: its guardian list, the clock factors and the client's origin domain must be the admin program's).
func (a Admin) FinishSetupIx(p Programs) solana.Instruction {
	accts := solana.AccountMetaSlice{w(a.State()), r(a.Authority())}
	for _, prog := range []solana.PublicKey{p.Mailbox, p.Tokens[0], p.Tokens[1], p.Client, p.Pausable, a.Program} {
		accts = append(accts, r(ProgramData(prog)))
	}
	m := Mailbox{Program: p.Mailbox}
	accts = append(accts, r(m.Outbox()), r(m.Inbox()), r(Synthetic{Program: p.Tokens[0]}.Token()), r(Synthetic{Program: p.Tokens[1]}.Token()), r(p.PausableISM().State()), r(p.LightClient().State()), r(p.PausableISM().Flags()))
	return ix(a.Program, []byte{2}, accts...)
}

// numbered is a variant's data with the decision's number (little-endian, as borsh writes it): Execute,
// StageUpgrade and CloseCancelled.
func numbered(variant byte, number uint64) []byte {
	var e enc
	e.u8(variant)
	e.u64(number)
	return e.Bytes()
}

// ExecuteIx is Execute with the six fixed accounts — the state, the decision, its deliverer, the executor
// (signer, payer), the authority PDA, the pausable ISM's flags — then the kind's own (ExtraFor). The client's
// state is among no kind's fixed accounts since M2j (no decision reads the client's head); kind 9 names it in
// its own list.
func (a Admin) ExecuteIx(p Programs, number uint64, deliverer, executor solana.PublicKey, extra solana.AccountMetaSlice, flagsWritable bool) solana.Instruction {
	fl := r(p.PausableISM().Flags())
	if flagsWritable {
		fl = w(p.PausableISM().Flags())
	}
	accts := solana.AccountMetaSlice{w(a.State()), w(a.Decision(number)), w(deliverer), ws(executor), w(a.Authority()), fl}
	accts = append(accts, extra...)
	return ix(a.Program, numbered(3, number), accts...)
}

// ExtraFor is a kind's accounts after the six fixed ones (kinds.rs::describe), and whether the pausable flags
// are written. A kind 8's refund key is the caller's. Kind 9 names, after the pausable ISM's state and program,
// the client's state (writable: its admin role moves), the client program, the successor's state (writable) and
// the system program (the predecessor's Execute CPIs the successor's SuccessorInit, the executor paying its
// growth); kind 12 the pausable ISM's state (writable) and program. Kind 4 (reserved), an unknown kind (10 and
// 11 since M2j) and a kind whose parameters do not parse take none: Execute spends them.
func (a Admin) ExtraFor(p Programs, kind byte, params *Params) (extra solana.AccountMetaSlice, flagsWritable bool) {
	pz := p.PausableISM()
	switch kind {
	case KindUnpause:
		if params != nil && params.Object == 0 {
			return solana.AccountMetaSlice{r(pz.State()), r(p.Pausable)}, true
		}
		return nil, false
	case KindSetTokenISM:
		if params != nil && int(params.Token) < len(p.Tokens) {
			t := p.Tokens[params.Token]
			return solana.AccountMetaSlice{w(Synthetic{Program: t}.Token()), r(t)}, false
		}
	case KindSetGuardians:
		return solana.AccountMetaSlice{r(pz.State()), r(p.Pausable)}, true
	case KindEnrollRouter:
		if params != nil && int(params.Token) < len(p.Tokens) {
			t := p.Tokens[params.Token]
			return solana.AccountMetaSlice{r(SystemProgram), w(Synthetic{Program: t}.Token()), r(t)}, false
		}
	case KindSetFee:
		return solana.AccountMetaSlice{w(Mailbox{Program: p.Mailbox}.Outbox()), r(p.Mailbox)}, false
	case KindUpgrade:
		if params != nil {
			refund := params.RefundKey
			return solana.AccountMetaSlice{w(ProgramData(params.Program)), w(params.Program), w(params.Buffer), w(refund), r(SysvarRent), r(SysvarClock), r(SystemProgram), r(LoaderID)}, false
		}
	case KindTransfer:
		if params != nil {
			succ := params.Program
			succAuth := PDA(succ, b("authority"))
			// the successor's authority PDA is writable (S-25, M2i): the hand-over sweeps this program's PDA float
			// to it by a system transfer the PDA signs; sent read-only the sweep's CPI fails and the decision pends
			v := solana.AccountMetaSlice{r(succ), r(ProgramData(succ)), r(LoaderID), w(succAuth)}
			for _, prog := range []solana.PublicKey{p.Mailbox, p.Tokens[0], p.Tokens[1], p.Client, p.Pausable} {
				v = append(v, w(ProgramData(prog)))
			}
			v = append(v, w(Mailbox{Program: p.Mailbox}.Outbox()), r(p.Mailbox))
			for _, t := range p.Tokens {
				v = append(v, w(Synthetic{Program: t}.Token()), r(t))
			}
			v = append(v, w(pz.State()), r(p.Pausable), w(p.LightClient().State()), r(p.Client), w(PDA(succ, b("state"))), r(SystemProgram))
			return v, false
		}
	case KindSetMintLimits:
		return solana.AccountMetaSlice{w(pz.State()), r(p.Pausable)}, false
	}
	return nil, false
}

// ExtraForCancelledUpgrade is what Execute reads after the six fixed accounts to close a cancelled kind 8 whose
// buffer is staged: the buffer, its refund key and the loader (the buffer goes back first).
func (a Admin) ExtraForCancelledUpgrade(buffer, refund solana.PublicKey) solana.AccountMetaSlice {
	return solana.AccountMetaSlice{w(buffer), w(refund), r(LoaderID)}
}

// StageUpgradeIx hands a kind-8 decision's buffer to the authority PDA; the buffer's writer signs and becomes
// its refund key.
func (a Admin) StageUpgradeIx(number uint64, buffer, writer solana.PublicKey) solana.Instruction {
	return ix(a.Program, numbered(4, number), r(a.State()), w(a.Decision(number)), w(buffer), rs(writer), r(a.Authority()), r(LoaderID))
}

func (a Admin) SetFlagIx(key solana.PublicKey) solana.Instruction {
	return ix(a.Program, []byte{5}, w(a.State()), rs(key))
}

func (a Admin) ClearFlagIx(key solana.PublicKey) solana.Instruction {
	return ix(a.Program, []byte{6}, w(a.State()), rs(key))
}

// CloseCancelledIx closes a pending decision in a stored cancelled range (tag 7 since M2j), its rent to the
// deliverer.
func (a Admin) CloseCancelledIx(number uint64, deliverer solana.PublicKey) solana.Instruction {
	return ix(a.Program, numbered(7, number), w(a.State()), w(a.Decision(number)), w(deliverer), r(a.Authority()))
}

// CancelPair is a pending decision a cancel closes now, with its deliverer.
type CancelPair struct {
	Number    uint64
	Deliverer solana.PublicKey
}

// HandleMetas is what the program's HandleAccountMetas answers since v27 (M2-NOTES 9): the accounts `handle`
// reads before the deliverer — the state, the decision PDA, the system program — for every kind. The relayer
// asks the program by simulation (Client.HandleAccounts) and checks the answer against this.
func (a Admin) HandleMetas(seq uint64) solana.AccountMetaSlice {
	return solana.AccountMetaSlice{w(a.State()), w(a.Decision(seq)), r(SystemProgram)}
}

// HandleAccounts are the admin program's Handle accounts as the mailbox's process lists them: the program's
// HandleAccountMetas answer (metas: HandleMetas, or the simulated one), then the payer appended as the next
// account (signer, writable: the deliverer, whose rent comes back), and for a cancel the authority PDA and then
// the pairs to close now — the order `handle` reads them (v27; the v26 answer put a cancel's authority PDA before
// the payer, which failed every cancel delivered from a simulated answer with WrongAccount).
func (a Admin) HandleAccounts(metas solana.AccountMetaSlice, kind byte, payer solana.PublicKey, pairs []CancelPair) solana.AccountMetaSlice {
	out := append(solana.AccountMetaSlice{}, metas...)
	out = append(out, ws(payer))
	if kind == KindCancel {
		out = append(out, r(a.Authority()))
		for _, p := range pairs {
			out = append(out, w(a.Decision(p.Number)), w(p.Deliverer))
		}
	}
	return out
}

// SameMetas says whether two account lists name the same accounts in the same order (the flags aside, which a
// simulated answer carries as the program set them).
func SameMetas(a, b solana.AccountMetaSlice) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].PublicKey.Equals(b[i].PublicKey) {
			return false
		}
	}
	return true
}

// ------------------------------------------------------------------- kinds --

// Params are a decision's parameters, by kind.
type Params struct {
	Kind byte
	// 1 cancel: a salt the program ignores (v27 §13: r/hlgov draws one in the executing transaction, so two
	// cancels of one list differ), then inclusive ranges (a list entry is a range of one).
	Salt   uint64
	Cancel []CancelRange
	// 2 unpause: 0 the pausable ISM, 1 the admin program.
	Object byte
	// 3 set token ISM / 6 enroll router: the token (0 GNOT, 1 BUBBLE).
	Token byte
	ISM   solana.PublicKey
	// 4 is reserved: no parameters parse (ErrReserved).
	// 5
	Seats []SeatChange
	// 6
	Domain uint32
	Router [32]byte
	// 7
	Lamports    uint64
	Beneficiary solana.PublicKey
	// 8: the program, the buffer, the ELF hash; 9: the successor program.
	Program solana.PublicKey
	Buffer  solana.PublicKey
	Hash    [32]byte
	// RefundKey is a kind 8's, read from the decision account once staged (not part of the body).
	RefundKey solana.PublicKey
	// 12: the token (Token above), its per-message maximum and rolling day cap, in base units.
	Max, DayCap uint64
}

// CancelRange is one range a cancel names. Channel is the body's channel byte, which r/hlgov still writes and
// which must be 0 (M2j: the one channel; a cancel naming another value does not parse and is spent).
type CancelRange struct {
	Channel  byte
	From, To uint64
}

// SevenDay is whether a kind is in the 7-day group (3–12).
func SevenDay(kind byte) bool { return kind >= 3 && kind <= 12 }

// MailboxKind is whether the program records a kind (1–9 and 12, kind 4 reserved among them); the rest — 10
// and 11 since M2j — are unknown, recorded and spent at recording.
func MailboxKind(kind byte) bool { return kind >= 1 && kind <= 9 || kind == KindSetMintLimits }

// Delay is a kind's clock after delivery at the compiled factor, without kind 5's hold rule.
func Delay(kind byte, factor int64) int64 {
	switch kind {
	case KindCancel:
		return 0
	case KindUnpause:
		return UnpauseDelay(factor)
	}
	return Week(factor)
}

type beReader struct {
	b   []byte
	pos int
	err error
}

func (r *beReader) take(n int) []byte {
	if r.err != nil || r.pos+n > len(r.b) {
		if r.err == nil {
			r.err = errShort
		}
		return make([]byte, n)
	}
	out := r.b[r.pos : r.pos+n]
	r.pos += n
	return out
}
func (r *beReader) u8() byte    { return r.take(1)[0] }
func (r *beReader) u32() uint32 { return binary.BigEndian.Uint32(r.take(4)) }
func (r *beReader) u64() uint64 { return binary.BigEndian.Uint64(r.take(8)) }
func (r *beReader) arr() (out [32]byte) {
	copy(out[:], r.take(32))
	return out
}
func (r *beReader) key() solana.PublicKey { return solana.PublicKeyFromBytes(r.take(32)) }

var (
	ErrParams = errors.New("admin: the parameters do not parse (the decision would be spent)")
	// ErrReserved is kind 4's answer: reserved, spent at Execute whatever it carries (v27 §13).
	ErrReserved = fmt.Errorf("%w: kind 4 is reserved", ErrParams)
)

// ParseParams reads a kind's parameters as the program does at execution (kinds.rs::parse); an error means the
// program would spend the decision. A cancel's parameters start with its salt (v27 §13), so a mailbox body's
// parameters are the bytes after the 9-byte prefix, salt included.
func ParseParams(kind byte, params []byte) (*Params, error) {
	r := &beReader{b: params}
	p := &Params{Kind: kind}
	switch kind {
	case KindCancel:
		p.Salt = r.u64()
		switch r.u8() {
		case 0:
			n := int(r.u8())
			if n == 0 || n > MaxCancel {
				return nil, ErrParams
			}
			for i := 0; i < n; i++ {
				ch := r.u8()
				if ch != 0 {
					return nil, ErrParams
				}
				x := r.u64()
				p.Cancel = append(p.Cancel, CancelRange{ch, x, x})
			}
		case 1:
			ch := r.u8()
			from, to := r.u64(), r.u64()
			if ch != 0 || from > to {
				return nil, ErrParams
			}
			p.Cancel = []CancelRange{{ch, from, to}}
		default:
			return nil, ErrParams
		}
	case KindUnpause:
		p.Object = r.u8()
		if p.Object > 1 {
			return nil, ErrParams
		}
	case KindSetTokenISM:
		p.Token, p.ISM = r.u8(), r.key()
	case KindReserved:
		return nil, ErrReserved
	case KindSetGuardians:
		n := int(r.u8())
		if n == 0 || n > 4 {
			return nil, ErrParams
		}
		for i := 0; i < n; i++ {
			p.Seats = append(p.Seats, SeatChange{Seat: r.u8(), Key: r.key()})
		}
	case KindEnrollRouter:
		p.Token, p.Domain, p.Router = r.u8(), r.u32(), r.arr()
	case KindSetFee:
		p.Lamports, p.Beneficiary = r.u64(), r.key()
	case KindUpgrade:
		p.Program, p.Buffer, p.Hash = r.key(), r.key(), r.arr()
	case KindTransfer:
		p.Program = r.key()
	case KindSetMintLimits:
		p.Token = r.u8()
		if p.Token > 1 {
			return nil, ErrParams // a token that is none of ours: spent
		}
		p.Max, p.DayCap = r.u64(), r.u64()
	default:
		return nil, ErrParams
	}
	if r.err != nil || r.pos != len(params) {
		return nil, ErrParams
	}
	return p, nil
}

// EncodeParams writes parameters as r/hlgov and the programs' tests do (kinds.rs::encode); kind 4, reserved,
// has no encoding and writes nothing.
func EncodeParams(p *Params) []byte {
	var v []byte
	be32 := func(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }
	be64 := func(n uint64) []byte { return binary.BigEndian.AppendUint64(nil, n) }
	switch p.Kind {
	case KindCancel:
		v = append(v, be64(p.Salt)...)
		if len(p.Cancel) == 1 && p.Cancel[0].From != p.Cancel[0].To {
			v = append(v, 1, p.Cancel[0].Channel)
			v = append(v, be64(p.Cancel[0].From)...)
			v = append(v, be64(p.Cancel[0].To)...)
		} else {
			v = append(v, 0, byte(len(p.Cancel)))
			for _, c := range p.Cancel {
				v = append(v, c.Channel)
				v = append(v, be64(c.From)...)
			}
		}
	case KindUnpause:
		v = append(v, p.Object)
	case KindSetTokenISM:
		v = append(append(v, p.Token), p.ISM[:]...)
	case KindSetGuardians:
		v = append(v, byte(len(p.Seats)))
		for _, s := range p.Seats {
			v = append(append(v, s.Seat), s.Key[:]...)
		}
	case KindEnrollRouter:
		v = append(append(append(v, p.Token), be32(p.Domain)...), p.Router[:]...)
	case KindSetFee:
		v = append(append(v, be64(p.Lamports)...), p.Beneficiary[:]...)
	case KindUpgrade:
		v = append(append(append(v, p.Program[:]...), p.Buffer[:]...), p.Hash[:]...)
	case KindTransfer:
		v = append(v, p.Program[:]...)
	case KindSetMintLimits:
		v = append(append(append(v, p.Token), be64(p.Max)...), be64(p.DayCap)...)
	}
	return v
}

// KindName names a kind of §13's table ("unknown" past it, and for 10 and 11 since M2j).
func KindName(kind byte) string {
	names := [...]string{"", "cancel", "unpause", "set token ISM", "reserved", "set guardians and watch keys", "enroll router", "set protocol fee", "upgrade", "transfer to successor", "unknown", "unknown", "set mint limits"}
	if int(kind) < len(names) && kind != 0 {
		return names[kind]
	}
	return "unknown"
}

// DescribeParams says what a decision's parameters mean, for hlwatch and the audit: a cancel with its salt and
// the numbers it names, kind 4 as reserved (spent at Execute), a kind unknown to the table as recorded and spent,
// parameters that do not parse as spent.
func DescribeParams(kind byte, params []byte) string {
	if kind == KindReserved {
		return "reserved (kind 4), will be spent at Execute"
	}
	if !MailboxKind(kind) {
		return fmt.Sprintf("unknown kind %d: recorded and spent", kind)
	}
	p, err := ParseParams(kind, params)
	if err != nil {
		return KindName(kind) + ": the parameters do not parse, spent at Execute"
	}
	switch kind {
	case KindCancel:
		var parts []string
		for _, c := range p.Cancel {
			if c.From == c.To {
				parts = append(parts, fmt.Sprintf("%d/%d", c.Channel, c.From))
			} else {
				parts = append(parts, fmt.Sprintf("%d/%d-%d", c.Channel, c.From, c.To))
			}
		}
		return fmt.Sprintf("cancel %s (channel/number), salt 0x%016x", strings.Join(parts, ","), p.Salt)
	case KindUnpause:
		if p.Object == 1 {
			return "unpause the admin program"
		}
		return "unpause the pausable ISM"
	case KindSetTokenISM:
		ism := p.ISM.String()
		if p.ISM.IsZero() {
			ism = "none"
		}
		return fmt.Sprintf("set token %d's ISM to %s", p.Token, ism)
	case KindSetGuardians:
		var parts []string
		for _, s := range p.Seats {
			parts = append(parts, fmt.Sprintf("seat %d → %s", s.Seat, s.Key))
		}
		return "set guardians and watch keys: " + strings.Join(parts, ", ")
	case KindEnrollRouter:
		return fmt.Sprintf("enroll router for token %d, domain %d: %x", p.Token, p.Domain, p.Router)
	case KindSetFee:
		return fmt.Sprintf("set protocol fee %d lamports, beneficiary %s", p.Lamports, p.Beneficiary)
	case KindUpgrade:
		return fmt.Sprintf("upgrade %s from buffer %s (ELF sha256 %x)", p.Program, p.Buffer, p.Hash)
	case KindTransfer:
		return "transfer every authority to successor " + p.Program.String()
	}
	return fmt.Sprintf("set token %d's mint limits: %d a message, %d a day (base units)", p.Token, p.Max, p.DayCap)
}

// DecisionBody is kind ‖ seq u64 BE ‖ params (§13).
func DecisionBody(kind byte, seq uint64, params []byte) []byte {
	b := []byte{kind}
	b = binary.BigEndian.AppendUint64(b, seq)
	return append(b, params...)
}

// ------------------------------------------------------------------- state --

// CancelledRange is a stored cancelled range of numbers (no channel since M2j).
type CancelledRange struct {
	From, To uint64
}

// Hold is a hold that ended (AN4): when its guardian flags reached two, and when it ended (Clock seconds).
type Hold struct {
	Start, End int64
}

// AdminState is the state PDA (M2j's layout: the version, clock factor and setup flag, the origin domain, the
// last number, the guardians, the successor, the authority bump, the cancelled ranges, the hold history, the one
// pending count, the predecessor and whether a successor inherited).
type AdminState struct {
	Version, ClockFactor, SetupDone byte
	OriginDomain                    uint32
	LastSeq                         uint64
	Guardians                       Guardians
	Successor                       *solana.PublicKey
	AuthorityBump                   byte
	Cancelled                       []CancelledRange
	// Holds are the last eight holds that ended, oldest first (a ninth merged the two oldest with the earliest
	// start kept); the standing hold is Guardians.HoldSince.
	Holds []Hold
	// Pending counts the pending decision accounts (S-04): at zero the stored ranges are pruned.
	Pending uint32
	// Predecessor is a successor's predecessor (C227; zero for the launch deployment); Inherited is 1 once the
	// predecessor's kind 9 wrote the live numbers, seats, watch slots and hold history into this state.
	Predecessor solana.PublicKey
	Inherited   byte
	// Len is the borsh form's length as parsed with this layout (version 1): where `inherited` ends; the bytes
	// after it are the account's reserve, zero (S-21: the hand-over's wire contract).
	Len int
}

// AwaitsInheritance says whether a successor has not inherited yet (the program's awaits_inheritance, S-20): its
// predecessor named and `inherited` 0 — such a program answers NotInherited on Handle and Execute until the
// predecessor's kind 9 lands.
func (s *AdminState) AwaitsInheritance() bool { return !s.Predecessor.IsZero() && s.Inherited == 0 }

// AdminStateFreshLen is the borsh length of a state with no flag, no successor, no cancelled range and no hold
// (a successor at its init): 262 bytes, `predecessor` at 229 and `inherited` at 261 (pinned by
// TestAdminStateLayout against the bytes); with every option present the form is 377 bytes, and the account is
// AdminStateAccountLen from init for the program's life (64 ranges and eight holds reserved: it never grows).
const (
	AdminStateFreshLen   = 262
	AdminStateAccountLen = 1_574
)

// AdminStateReserve names the bytes past the version-1 borsh form that are not zero, "" when the reserve is
// zero. The hand-over's wire contract (S-21) keeps the layout append-only: kind 9's peek (`can_inherit`) loads
// the version-1 prefix with a borsh deserialize that leaves trailing bytes alone, reads `predecessor`,
// `inherited` and `successor` from it, and SuccessorInit writes the prefix — so a field a later build appended
// after `inherited` is read by nothing of the hand-over, and a non-zero reserve is a note for the member, not a
// layout finding (G52, AS8: before, it was LAYOUT DIFFERS, stricter than the program; the form's length was
// compared with the vectors' arithmetic too, which the parse itself fixes). A field inserted before
// `inherited` shows as the prefix failing to parse, or as a predecessor that is not this program: those are the
// findings, as the program's peek spends the hand-over on them.
func AdminStateReserve(s *AdminState, data []byte) string {
	if s.Len > len(data) {
		return ""
	}
	first, n := -1, 0
	for i, b := range data[s.Len:] {
		if b != 0 {
			if first < 0 {
				first = i
			}
			n++
		}
	}
	if n == 0 {
		return ""
	}
	bytes := "bytes"
	if n == 1 {
		bytes = "byte"
	}
	return fmt.Sprintf("byte %d, past the version-1 form's %d bytes, is %#x (%d non-zero %s in the reserve): a field this build does not read, appended as the contract allows (S-21); the hand-over reads none of it", s.Len+first, s.Len, data[s.Len+first], n, bytes)
}

// ReplacementFloor is a replacement's floor while a hold stands (AN4, State::replacement_floor): the earliest
// hold overlapping [its delivery, now] — the standing one (standing is its start), or an ended one whose end is
// at or after the delivery — plus day21 (21 days at the clock factor). It binds only while a hold stands.
func (s *AdminState) ReplacementFloor(deliveredTime, standing, day21 int64) int64 {
	earliest := standing
	for _, h := range s.Holds {
		if h.End >= deliveredTime && h.Start < earliest {
			earliest = h.Start
		}
	}
	return earliest + day21
}

func (s *AdminState) IsCancelled(number uint64) bool {
	for _, r := range s.Cancelled {
		if r.From <= number && number <= r.To {
			return true
		}
	}
	return false
}

// ParseAdminState reads the state account in M2j's layout (AdminState's field order; a vector is borsh's u32
// count, then its entries).
func ParseAdminState(data []byte) (*AdminState, error) {
	d := newDec(data)
	s := &AdminState{Version: d.u8(), ClockFactor: d.u8(), SetupDone: d.u8(), OriginDomain: d.u32(), LastSeq: d.u64()}
	s.Guardians = d.guardians()
	s.Successor = d.optKey()
	s.AuthorityBump = d.u8()
	n := d.u32()
	if d.err == nil && n > MaxCancelled {
		return nil, errors.New("admin state: more cancelled ranges than the program keeps")
	}
	for i := uint32(0); i < n && d.err == nil; i++ {
		s.Cancelled = append(s.Cancelled, CancelledRange{d.u64(), d.u64()})
	}
	n = d.u32()
	if d.err == nil && n > MaxHolds {
		return nil, errors.New("admin state: more holds than the program keeps")
	}
	for i := uint32(0); i < n && d.err == nil; i++ {
		s.Holds = append(s.Holds, Hold{Start: d.i64(), End: d.i64()})
	}
	s.Pending = d.u32()
	s.Predecessor = d.key()
	s.Inherited = d.u8()
	s.Len = d.pos
	// the account is AdminStateAccountLen from init and never grows: the bytes after the borsh form are the
	// reserve, zero
	if err := d.done(); err != nil {
		return nil, fmt.Errorf("admin state: %w", err)
	}
	if s.Version != 1 {
		return nil, errors.New("admin state: an unknown layout version")
	}
	return s, nil
}

// Decision is a pending decision's account (M2j's layout: version ‖ number ‖ kind ‖ params ‖ delivered slot ‖
// delivered time ‖ deliverer ‖ refund key — no channel, trigger or marker; no floor is written: a replacement's
// floor under a hold is a function of the state's hold history, ReplacementFloor).
type Decision struct {
	Version       byte
	Number        uint64
	Kind          byte
	Params        []byte
	DeliveredSlot uint64
	DeliveredTime int64
	Deliverer     solana.PublicKey
	RefundKey     *solana.PublicKey
}

// DecisionOverhead is a decision account's bytes beside its parameters (the program's DECISION_OVERHEAD): the
// layout with the refund key present.
const DecisionOverhead = 95

// ParseDecision reads a pending decision's account in M2j's layout, the refund key its last field. The length is
// checked exactly, so an account of another layout (the pre-M2j one with its channel, trigger and marker among
// them) is refused rather than read short: the program allocates every decision at Decision::size, the layout
// with the refund key present, so an account whose refund key is None carries 32 spare zero bytes after it and
// one whose key is set carries none.
func ParseDecision(data []byte) (*Decision, error) {
	d := newDec(data)
	x := &Decision{Version: d.u8(), Number: d.u64(), Kind: d.u8()}
	x.Params = d.bytes()
	x.DeliveredSlot = d.u64()
	x.DeliveredTime = d.i64()
	x.Deliverer = d.key()
	x.RefundKey = d.optKey()
	if err := d.done(); err != nil {
		return nil, fmt.Errorf("decision: %w", err)
	}
	spare := 0
	if x.RefundKey == nil {
		spare = 32
	}
	rest := data[d.pos:]
	if len(rest) != spare || bytes.ContainsFunc(rest, func(r rune) bool { return r != 0 }) {
		return nil, fmt.Errorf("decision: %d bytes past the layout (%d expected: the account's room for a refund key), not a layout this reader knows", len(rest), spare)
	}
	return x, nil
}

// MaturesAt is when a decision may execute by the program's clocks (§6.5, plan v35): the kind's delay after
// delivery; under a hold on the admin program, a replacement of guardians (kind 5) lands on the later of that
// and its floor — the 21st day of the earliest hold overlapping its pending life, from the state's hold history
// (FloorUnder; AN4: nothing written, no earlier Execute needed, no later hold raises it). A lone flag's deferral
// of the 7-day group, and a hold's deferral of its other kinds until the hold ends, are not times and are not
// counted here. The state may be nil when no hold stands (the floor then binds nothing).
func (x *Decision) MaturesAt(factor int64, adminHold *Flag, st *AdminState) int64 {
	at := x.DeliveredTime + Delay(x.Kind, factor)
	if adminHold != nil && x.HeldToTheDay() {
		if f := x.FloorUnder(adminHold, st, factor); f > at {
			at = f
		}
	}
	return at
}

// FloorUnder is the earliest day a replacement lands on while a hold stands (AN4): 21 days after the start of
// the earliest hold overlapping [its delivery, now] — the standing hold, or an ended one of the history whose end
// is at or after the delivery; so two keys re-flagging at the grace's last second begin a hold whose own 21st day
// is never the floor. With no history (st nil) the standing hold's own 21st day.
func (x *Decision) FloorUnder(hold *Flag, st *AdminState, factor int64) int64 {
	day21 := HoldReplacementDays * Day / factor
	if st == nil {
		return hold.Time + day21
	}
	return st.ReplacementFloor(x.DeliveredTime, hold.Time, day21)
}

// HeldToTheDay says whether a hold defers this decision to a floor (the hold's 21st day) rather than until the
// hold ends: a replacement of guardians (kind 5).
func (x *Decision) HeldToTheDay() bool { return x.Kind == KindSetGuardians }

// HeldUntilTheEnd says whether a hold on the admin program defers this decision until the hold ends: a decision
// of the 7-day group other than a replacement (§6.5).
func (x *Decision) HeldUntilTheEnd() bool { return SevenDay(x.Kind) && x.Kind != KindSetGuardians }

// AdminError is the program's refusal behind a custom error (3000 + n; M2j's compacted table, 0–27).
func AdminError(code uint32) string {
	names := []string{"AlreadyInitialized", "NotInitialized", "WrongVersion", "WrongAuthority", "SetupDone", "SetupIncomplete", "NotTheMailbox", "WrongOrigin", "WrongSender", "ShortBody", "OutOfOrder", "WrongDecision", "NotMature", "Deferred", "UnknownKey", "DuplicateKey", "BadSeat", "TooManyGuardians", "TooManyWatch", "CurrentKey", "Hold", "NotStaged", "WrongBuffer", "WrongAccount", "WrongDeliverer", "Retired", "NeedsExtend", "NotInherited"}
	if code >= 3000 && int(code-3000) < len(names) {
		return "admin program: " + names[code-3000]
	}
	return ""
}

// ProgramErrorName names a custom error code of any of the three programs ("" for another).
func ProgramErrorName(code uint32) string {
	for _, f := range []func(uint32) string{ClientError, PausableError, AdminError} {
		if s := f(code); s != "" {
			return s
		}
	}
	return ""
}

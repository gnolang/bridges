package sol

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/gagliardetto/solana-go"
)

// The pausable ISM (bridge/programs/pausable-ism) and the guardians' model it shares with the admin program
// (sol-util's guardians): four seats, two watch slots, a flag per key with the moment it was last set, the object's
// hold (holdSince, kept through a dip shorter than the grace) — the same arithmetic as the programs', so a reader
// sees a hold the way the chain will judge it.

const (
	Seats = 4
	Watch = 2
	Keys  = Seats + Watch
)

// Flag is a moment: the Clock's slot and unix time.
type Flag struct {
	Slot uint64
	Time int64
}

// Guardians is the seats, the watch slots and every flag, as both programs keep them.
type Guardians struct {
	Keys      [Keys]solana.PublicKey // seats 1–4 then watch slots 5–6, by index 0–5
	Flags     [Keys]*Flag
	HoldSince *Flag
	DipSince  *Flag
}

func (g *Guardians) Position(k solana.PublicKey) int {
	for i, x := range g.Keys {
		if x.Equals(k) {
			return i
		}
	}
	return -1
}

func (g *Guardians) AnyFlag() bool {
	for _, f := range g.Flags {
		if f != nil {
			return true
		}
	}
	return false
}

func (g *Guardians) GuardianFlags() int {
	n := 0
	for _, f := range g.Flags[:Seats] {
		if f != nil {
			n++
		}
	}
	return n
}

// Hold is the hold standing at now (its start), as the programs' pure function judges it: hold_since while the
// guardian flags are at two or more, or through a dip shorter than the grace.
func (g *Guardians) Hold(now, grace int64) *Flag {
	if g.HoldSince == nil {
		return nil
	}
	if g.GuardianFlags() >= 2 {
		return g.HoldSince
	}
	if g.DipSince != nil && now-g.DipSince.Time < grace {
		return g.HoldSince
	}
	return nil
}

func (d *dec) flag() *Flag {
	if !d.option() {
		return nil
	}
	return &Flag{Slot: d.u64(), Time: d.i64()}
}

func (d *dec) guardians() Guardians {
	var g Guardians
	for i := range g.Keys {
		g.Keys[i] = d.key()
	}
	for i := range g.Flags {
		g.Flags[i] = d.flag()
	}
	g.HoldSince = d.flag()
	g.DipSince = d.flag()
	return g
}

// Pausable is one deployment of the program.
type Pausable struct{ Program solana.PublicKey }

func (p Pausable) State() solana.PublicKey { return PDA(p.Program, b("state")) }
func (p Pausable) Flags() solana.PublicKey { return PDA(p.Program, b("flags")) }

// InitIx is the one-shot init: the owner (the admin program's authority PDA), the client it fronts and the layout
// version it reads, the seats and watch slots from the same list the admin program's init carries.
func (p Pausable) InitIx(payer, authority, owner, target solana.PublicKey, targetVersion byte, seats [Seats]solana.PublicKey, watch [Watch]solana.PublicKey) solana.Instruction {
	var e enc
	e.u8(0)
	e.key(owner)
	e.key(target)
	e.u8(targetVersion)
	for _, k := range seats {
		e.key(k)
	}
	for _, k := range watch {
		e.key(k)
	}
	return ix(p.Program, e.Bytes(), w(p.State()), w(p.Flags()), ws(payer), rs(authority), r(p.Program), r(ProgramData(p.Program)), r(SystemProgram))
}

func (p Pausable) SetFlagIx(key solana.PublicKey) solana.Instruction {
	return ix(p.Program, []byte{1}, w(p.Flags()), rs(key))
}

func (p Pausable) ClearFlagIx(key solana.PublicKey) solana.Instruction {
	return ix(p.Program, []byte{2}, w(p.Flags()), rs(key))
}

func (p Pausable) UnpauseIx(owner solana.PublicKey, beforeSlot uint64) solana.Instruction {
	var e enc
	e.u8(3)
	e.u64(beforeSlot)
	return ix(p.Program, e.Bytes(), w(p.Flags()), r(p.State()), rs(owner))
}

// SeatChange is one entry of a set-guardians decision: a seat (1–6) and the key that takes it.
type SeatChange struct {
	Seat byte
	Key  solana.PublicKey
}

func (p Pausable) SetGuardiansIx(owner solana.PublicKey, pairs []SeatChange) solana.Instruction {
	var e enc
	e.u8(4)
	e.u32(uint32(len(pairs)))
	for _, c := range pairs {
		e.u8(c.Seat)
		e.key(c.Key)
	}
	return ix(p.Program, e.Bytes(), w(p.Flags()), r(p.State()), rs(owner))
}

// TransferOwnershipIx is kind 9's hand-over. Discriminator 5 is SetLayoutVersion, which the program refuses
// outright since v27 (LayoutFixed: the client's state layout is fixed at version 1 for the deployment's life,
// kind 4 reserved); it stays in the program's enum only so this one keeps its number, and no builder sends it.
func (p Pausable) TransferOwnershipIx(owner, newOwner solana.PublicKey) solana.Instruction {
	var e enc
	e.u8(6)
	e.key(newOwner)
	return ix(p.Program, e.Bytes(), w(p.State()), rs(owner))
}

// SetMintLimitsIx is kind 12's owner-only setter (AN5): a token's per-message maximum and rolling day cap, in
// the token's base units; the bucket's level stands. The admin program's Execute of a kind 12 sends it by CPI.
func (p Pausable) SetMintLimitsIx(owner solana.PublicKey, token byte, max, dayCap uint64) solana.Instruction {
	var e enc
	e.u8(7)
	e.u8(token)
	e.u64(max)
	e.u64(dayCap)
	return ix(p.Program, e.Bytes(), w(p.State()), rs(owner))
}

// VerifyAccounts are the accounts the pausable ISM's Verify takes, as its VerifyAccountMetas answers them (it
// derives its own and trusts none the relayer passes): its flags, its state (writable: the mint bucket is
// charged), the client's state (writable) and program, the Instructions sysvar (AN5: the delivery's top-level
// instruction must be the mailbox's InboxProcess of this message), then the client's own.
func (p Pausable) VerifyAccounts(client LightClient, clientAccounts solana.AccountMetaSlice) solana.AccountMetaSlice {
	out := solana.AccountMetaSlice{r(p.Flags()), w(p.State()), w(client.State()), r(client.Program), r(SysvarIxs)}
	for _, m := range clientAccounts {
		if !m.PublicKey.Equals(client.State()) {
			out = append(out, m)
		}
	}
	return out
}

// MintLimit is a token's mint limits (AN5): the most one message may carry, and a rolling day's cap — a bucket
// that drains at the cap per day (24 hours at the clock factor) from the moment it was last charged.
type MintLimit struct {
	MaxPerMessage uint64
	DayCap        uint64
	Used          uint64 // what the bucket held when last charged
	UsedAt        int64  // when
}

// Level is the bucket's level at now (state.rs MintLimit::level): what was charged, less what drained since.
func (l MintLimit) Level(now, day int64) uint64 {
	elapsed := now - l.UsedAt
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

// PausableState is the state PDA (the audit-round-1 layout: the mint limits per token, in config::TOKENS'
// order — 0 GNOT, 1 BUBBLE — after the target's layout version).
type PausableState struct {
	Version       byte
	ClockFactor   byte
	Owner         solana.PublicKey
	Target        solana.PublicKey
	TargetVersion byte
	Limits        [2]MintLimit
}

func ParsePausableState(data []byte) (*PausableState, error) {
	d := newDec(data)
	s := &PausableState{Version: d.u8(), ClockFactor: d.u8(), Owner: d.key(), Target: d.key(), TargetVersion: d.u8()}
	for i := range s.Limits {
		s.Limits[i] = MintLimit{MaxPerMessage: d.u64(), DayCap: d.u64(), Used: d.u64(), UsedAt: d.i64()}
	}
	if err := d.done(); err != nil {
		return nil, fmt.Errorf("pausable state: %w", err)
	}
	if s.Version != 1 {
		return nil, errors.New("pausable state: an unknown layout version")
	}
	return s, nil
}

// ParsePausableFlags reads the flags PDA: a version byte then the guardians.
func ParsePausableFlags(data []byte) (*Guardians, error) {
	d := newDec(data)
	v := d.u8()
	g := d.guardians()
	if err := d.done(); err != nil {
		return nil, fmt.Errorf("pausable flags: %w", err)
	}
	if v != 1 {
		return nil, errors.New("pausable flags: an unknown layout version")
	}
	return &g, nil
}

// PausableError is the program's refusal behind a custom error (2000 + n).
func PausableError(code uint32) string {
	names := []string{"AlreadyInitialized", "NotInitialized", "WrongVersion", "WrongAuthority", "NotOwner", "UnknownKey", "DuplicateKey", "BadSeat", "TooManyGuardians", "TooManyWatch", "CurrentKey", "Hold", "Paused", "Quarantine", "WrongTarget", "WrongClientState", "MissingClientProgram", "LayoutFixed", "NotTheMailbox", "BadTokenMessage", "OverMax", "OverDayCap", "BadToken"}
	if code >= 2000 && int(code-2000) < len(names) {
		return "pausable ISM: " + names[code-2000]
	}
	return ""
}

// Clock durations as the programs divide them by the compiled factor (sol-util's clock).
const (
	Hour = int64(3600)
	Day  = int64(86400)
)

func UnpauseDelay(factor int64) int64 { return Day / factor }
func Week(factor int64) int64         { return 7 * Day / factor }
func Fortnight(factor int64) int64    { return 14 * Day / factor }

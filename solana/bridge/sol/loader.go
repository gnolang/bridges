package sol

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// The upgradeable loader (sol-util's loader): account layouts (bincode: a u32 tag, then the fields) and the one
// instruction the setup step sends it directly, the burn of the admin program's own upgrade authority.

var LoaderID = solana.MustPublicKeyFromBase58("BPFLoaderUpgradeab1e11111111111111111111111")

const (
	ProgramDataHeader = 45
	BufferHeader      = 37
	// ExtendStep is the least one ExtendProgram may add (SIMD-0431's minimum of 10,240 bytes, active on mainnet
	// since 2026-07-14, F7); the loader bounds one extension only by the program-data maximum, so a growth is
	// one instruction for the whole excess (ExtendBy), one extension a block. The 10,240-per-instruction rule
	// is the BPF realloc's, not the loader's (the round-1 reading).
	ExtendStep = 10_240
	// MaxProgramDataLen is the loader's bound on a program-data account (10 MiB).
	MaxProgramDataLen = 10 * 1024 * 1024
)

// ExtendRent is what one ExtendProgram of add bytes costs its payer: the loader tops the program data up to rent
// exemption at its new length (rent.minimum_balance(new_len) − the account's lamports), read live from the chain
// (AP5, F15: the compiled 6,960 lamports a byte was the test validator's rate, mainnet's is 5,080 — every rent
// figure an alarm, a preview or a status prints comes from getMinimumBalanceForRentExemption now).
func ExtendRent(ctx context.Context, c *rpc.Client, program solana.PublicKey, add uint32) (uint64, error) {
	info, err := c.GetAccountInfoWithOpts(ctx, ProgramData(program), &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed})
	if err != nil {
		return 0, fmt.Errorf("the programdata of %s: %w", program, err)
	}
	if info.Value == nil {
		return 0, fmt.Errorf("no programdata for %s", program)
	}
	need, err := c.GetMinimumBalanceForRentExemption(ctx, uint64(len(info.Value.Data.GetBinary()))+uint64(add), rpc.CommitmentConfirmed)
	if err != nil {
		return 0, fmt.Errorf("the rent-exempt minimum: %w", err)
	}
	if need <= info.Value.Lamports {
		return 0, nil
	}
	return need - info.Value.Lamports, nil
}

// SOL writes lamports as SOL for a line.
func SOL(lamports uint64) string { return fmt.Sprintf("%.4f SOL", float64(lamports)/1e9) }

// ExtendBy is the one ExtendProgram's size for a program data of pdLen bytes (past its header) that must hold
// an ELF of bufLen (F7): the excess rounded up to the loader's minimum, or exactly the headroom below the
// 10 MiB bound when that is less; 0 when nothing is needed.
func ExtendBy(pdLen, bufLen int) uint32 {
	if bufLen <= pdLen {
		return 0
	}
	add := bufLen - pdLen
	if add < ExtendStep {
		add = ExtendStep
	}
	if room := MaxProgramDataLen - ProgramDataHeader - pdLen; add > room {
		add = room
	}
	if add <= 0 {
		return 0
	}
	return uint32(add)
}

// ELFBindsKey says whether a compiled program's bytes carry a 32-byte constant (G20: the ids `hlsol setup`
// wrote into config.rs — the pausable ISM's MAILBOX and TOKENS, the admin program's MAILBOX, TOKENS and
// HLGOV_SENDER): either contiguous in .rodata, or as the four 64-bit immediates of sBPF `lddw` instructions
// (opcode 0x18, the low half at bytes 4–8 and the high half at bytes 12–16 of the 16-byte instruction), which
// is how the compiler laid out the pausable ISM's MAILBOX and the admin program's HLGOV_SENDER in the built
// ELFs (measured on the e2e's builds).
func ELFBindsKey(elf []byte, key [32]byte) bool {
	if bytes.Contains(elf, key[:]) {
		return true
	}
	for i := 0; i < 32; i += 8 {
		if !lddwImmediate(elf, key[i:i+8]) {
			return false
		}
	}
	return true
}

// lddwImmediate says whether an 8-byte value appears as a `lddw` immediate anywhere in the bytes.
func lddwImmediate(elf, v []byte) bool {
	for i := 0; i+16 <= len(elf); i++ {
		if elf[i] != 0x18 || elf[i+2] != 0 || elf[i+3] != 0 {
			continue
		}
		if bytes.Equal(elf[i+4:i+8], v[:4]) && elf[i+8] == 0 && elf[i+9] == 0 && elf[i+10] == 0 && elf[i+11] == 0 && bytes.Equal(elf[i+12:i+16], v[4:]) {
			return true
		}
	}
	return false
}

// ELFBindsText says whether a compiled program's bytes carry a string constant (the client's MAILBOX_PKG_PATH).
func ELFBindsText(elf []byte, text string) bool { return bytes.Contains(elf, []byte(text)) }

// ELFBindsU64 says whether a compiled program's bytes carry a 64-bit constant (G51: the pausable ISM's
// DEFAULT_DAY_CAP and DEFAULT_MAX_PER_MESSAGE, which `hlsol setup -no-build` checks before the first deploy):
// contiguous in .rodata, or as the immediate of an sBPF `lddw` (the low half at bytes 4–8, the high half at
// bytes 12–16 of the 16-byte instruction) — how the compiler laid out both limits in the e2e's build (one
// `lddw` each; a `pub const u64` is inlined, never stored).
func ELFBindsU64(elf []byte, v uint64) bool {
	var le [8]byte
	for i := range le {
		le[i] = byte(v >> (8 * i))
	}
	return bytes.Contains(elf, le[:]) || lddwImmediate(elf, le[:])
}

func ProgramData(program solana.PublicKey) solana.PublicKey { return PDA(LoaderID, program[:]) }

// ParseProgram is a program account's programdata address.
func ParseProgram(data []byte) (solana.PublicKey, error) {
	if len(data) < 36 || data[0] != 2 || data[1] != 0 || data[2] != 0 || data[3] != 0 {
		return solana.PublicKey{}, errors.New("not an upgradeable program account")
	}
	return solana.PublicKeyFromBytes(data[4:36]), nil
}

// ParseProgramData is a programdata account's slot, upgrade authority (nil when burned) and ELF.
func ParseProgramData(data []byte) (slot uint64, authority *solana.PublicKey, elf []byte, err error) {
	if len(data) < ProgramDataHeader || data[0] != 3 || data[1] != 0 || data[2] != 0 || data[3] != 0 {
		return 0, nil, nil, errors.New("not a programdata account")
	}
	d := newDec(data[4:ProgramDataHeader])
	slot = d.u64()
	authority = d.optKey()
	return slot, authority, data[ProgramDataHeader:], d.done()
}

// ParseBuffer is a buffer account's authority and ELF.
func ParseBuffer(data []byte) (authority *solana.PublicKey, elf []byte, err error) {
	if len(data) < BufferHeader || data[0] != 1 || data[1] != 0 || data[2] != 0 || data[3] != 0 {
		return nil, nil, errors.New("not a buffer account")
	}
	d := newDec(data[4:BufferHeader])
	authority = d.optKey()
	return authority, data[BufferHeader:], d.done()
}

// SetAuthorityIx is the loader's SetAuthority (tag 4) on a programdata or buffer: the current authority signs;
// a nil new authority burns it.
func SetAuthorityIx(target, current solana.PublicKey, newAuthority *solana.PublicKey) solana.Instruction {
	accts := solana.AccountMetaSlice{w(target), rs(current)}
	if newAuthority != nil {
		accts = append(accts, r(*newAuthority))
	}
	return ix(LoaderID, []byte{4, 0, 0, 0}, accts...)
}

// ExtendProgramIx is the loader's own permissionless ExtendProgram (tag 6, u32 additional bytes; sol-util's
// loader::extend_program): programdata (w), program (w), the system program, the payer (signer, w) — no
// authority signs, the payer funds the added rent. It is sent top-level by the executor before a kind-8 upgrade
// whose ELF outgrows the program data (the admin program answers NeedsExtend): mainnet's runtime lets no program
// CPI an extend and its loader has no ExtendProgramChecked (the audit's F1), one 10,240-byte step a block.
func ExtendProgramIx(program, payer solana.PublicKey, additional uint32) solana.Instruction {
	var e enc
	e.u32(6)
	e.u32(additional)
	return ix(LoaderID, e.Bytes(), w(ProgramData(program)), w(program), r(SystemProgram), ws(payer))
}

// ELFEnd is where an ELF's bytes end inside a programdata or buffer account (its section header table's end),
// since the account is often allocated past the ELF.
func ELFEnd(elf []byte) int {
	if len(elf) < 0x40 || string(elf[:4]) != "\x7fELF" {
		return len(elf)
	}
	shoff := int(le64(elf[0x28:0x30]))
	shentsize := int(le16(elf[0x3a:0x3c]))
	shnum := int(le16(elf[0x3c:0x3e]))
	end := shoff + shentsize*shnum
	if end <= 0 || end > len(elf) {
		return len(elf)
	}
	return end
}

func le16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }
func le64(b []byte) uint64 {
	var v uint64
	for i := 7; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}

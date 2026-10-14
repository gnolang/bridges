//! The ed25519 precompile, read back through the Instructions sysvar (§6.3).
//!
//! The runtime verifies every ed25519 precompile instruction of a transaction before any program runs (strictly:
//! canonical S, no non-canonical points, ed25519-dalek's `verify_strict`). A program that wants to know *what* was
//! verified reads the precompile instruction's data: a header `num_signatures u8, padding u8`, then per signature
//! fourteen bytes of little-endian u16 offsets — signature offset and instruction index, public key offset and
//! index, message offset, message size and index — and, at the offsets, the signature (64), the key (32) and the
//! message. The checks here: the instruction is the precompile's, the header's count is the entries', every index
//! names the precompile instruction itself (its own index or u16::MAX) so every offset points inside its own data,
//! and the message length is the entry's.

use solana_program::{account_info::AccountInfo, ed25519_program, program_error::ProgramError, sysvar::instructions::load_instruction_at_checked};

pub const SIGNATURE_LEN: usize = 64;
pub const PUBKEY_LEN: usize = 32;
pub const OFFSETS_LEN: usize = 14;
pub const HEADER_LEN: usize = 2;

/// One verified (pubkey, message, signature) the precompile instruction carried.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Entry {
    pub pubkey: [u8; 32],
    pub signature: [u8; 64],
    pub message: Vec<u8>,
}

/// Why an introspection failed; the programs map it onto their own error codes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum IntrospectError {
    NotThePrecompile,
    BadHeader,
    OffsetOutside,
    OtherInstruction,
}

impl From<IntrospectError> for ProgramError {
    fn from(e: IntrospectError) -> Self {
        ProgramError::Custom(0x6000 + e as u32)
    }
}

/// The entries of the precompile instruction at `index` of the current transaction.
pub fn precompile_entries(instructions_sysvar: &AccountInfo, index: usize) -> Result<Vec<Entry>, ProgramError> {
    let ix = load_instruction_at_checked(index, instructions_sysvar)?;
    if ix.program_id != ed25519_program::ID {
        return Err(IntrospectError::NotThePrecompile.into());
    }
    let data = &ix.data;
    if data.len() < HEADER_LEN {
        return Err(IntrospectError::BadHeader.into());
    }
    let n = data[0] as usize;
    if n == 0 || data[1] != 0 {
        return Err(IntrospectError::BadHeader.into());
    }
    let mut entries = Vec::with_capacity(n);
    for i in 0..n {
        let o = HEADER_LEN + OFFSETS_LEN * i;
        if data.len() < o + OFFSETS_LEN {
            return Err(IntrospectError::BadHeader.into());
        }
        let at = |k: usize| u16::from_le_bytes([data[o + k], data[o + k + 1]]) as usize;
        let (sig_off, sig_ix, pk_off, pk_ix, msg_off, msg_len, msg_ix) = (at(0), at(2), at(4), at(6), at(8), at(10), at(12));
        for ixi in [sig_ix, pk_ix, msg_ix] {
            if !(ixi == u16::MAX as usize || ixi == index) {
                return Err(IntrospectError::OtherInstruction.into());
            }
        }
        let sig = data.get(sig_off..sig_off + SIGNATURE_LEN).ok_or(IntrospectError::OffsetOutside)?;
        let pk = data.get(pk_off..pk_off + PUBKEY_LEN).ok_or(IntrospectError::OffsetOutside)?;
        let msg = data.get(msg_off..msg_off + msg_len).ok_or(IntrospectError::OffsetOutside)?;
        let mut pubkey = [0u8; 32];
        pubkey.copy_from_slice(pk);
        let mut signature = [0u8; 64];
        signature.copy_from_slice(sig);
        entries.push(Entry { pubkey, signature, message: msg.to_vec() });
    }
    Ok(entries)
}

/// The precompile instruction's data for the given entries, as a client builds it (the tests and the tools): all
/// offsets inside this instruction.
pub fn instruction_data(entries: &[Entry]) -> Vec<u8> {
    let n = entries.len();
    let mut data = Vec::with_capacity(HEADER_LEN + n * (OFFSETS_LEN + SIGNATURE_LEN + PUBKEY_LEN) + entries.iter().map(|e| e.message.len()).sum::<usize>());
    data.push(n as u8);
    data.push(0);
    let mut cursor = HEADER_LEN + OFFSETS_LEN * n;
    let mut body = Vec::new();
    for e in entries {
        let pk_off = cursor;
        let sig_off = pk_off + PUBKEY_LEN;
        let msg_off = sig_off + SIGNATURE_LEN;
        for v in [sig_off as u16, u16::MAX, pk_off as u16, u16::MAX, msg_off as u16, e.message.len() as u16, u16::MAX] {
            data.extend_from_slice(&v.to_le_bytes());
        }
        body.extend_from_slice(&e.pubkey);
        body.extend_from_slice(&e.signature);
        body.extend_from_slice(&e.message);
        cursor = msg_off + e.message.len();
    }
    data.extend_from_slice(&body);
    data
}

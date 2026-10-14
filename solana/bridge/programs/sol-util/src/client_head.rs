//! The fixed head of the light client's state account, which the pausable ISM and the admin program read without
//! the client's code (§13: the layout version is 1 for the deployment's life, both readers pin it, and a reader
//! fails closed on any other; a new layout is a successor pair of programs). 64 bytes at the start of the
//! account, little-endian:
//!
//! `[0]` layout version · `[1]` the compiled clock factor · `[2..4]` ring count u16 · `[4..6]` ring next u16 ·
//! `[6..8]` the body region's capacity in units of 8 bytes (the ring region follows it) · `[8..16]` quarantine end
//! (unix seconds; 0 when none) · `[16..24]` the time of the last accepted header · `[24..32]` the trusted height
//! u64 · `[32..64]` the trusted header's hash.

use solana_program::{account_info::AccountInfo, program_error::ProgramError, pubkey::Pubkey};

pub const HEAD_LEN: usize = 64;
pub const STATE_SEED: &[u8] = b"state";
pub const VERSION_1: u8 = 1;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Head {
    pub version: u8,
    pub clock_factor: u8,
    pub ring_count: u16,
    pub ring_next: u16,
    pub body_cap8: u16,
    pub quarantine_end: i64,
    pub last_accepted_time: i64,
    pub trusted_height: u64,
    pub trusted_hash: [u8; 32],
}

impl Head {
    pub fn read(data: &[u8]) -> Result<Head, ProgramError> {
        if data.len() < HEAD_LEN {
            return Err(ProgramError::InvalidAccountData);
        }
        let u16_at = |i: usize| u16::from_le_bytes([data[i], data[i + 1]]);
        let i64_at = |i: usize| i64::from_le_bytes(data[i..i + 8].try_into().unwrap());
        let mut trusted_hash = [0u8; 32];
        trusted_hash.copy_from_slice(&data[32..64]);
        Ok(Head {
            version: data[0],
            clock_factor: data[1],
            ring_count: u16_at(2),
            ring_next: u16_at(4),
            body_cap8: u16_at(6),
            quarantine_end: i64_at(8),
            last_accepted_time: i64_at(16),
            trusted_height: i64_at(24) as u64,
            trusted_hash,
        })
    }

    pub fn write(&self, data: &mut [u8]) {
        data[0] = self.version;
        data[1] = self.clock_factor;
        data[2..4].copy_from_slice(&self.ring_count.to_le_bytes());
        data[4..6].copy_from_slice(&self.ring_next.to_le_bytes());
        data[6..8].copy_from_slice(&self.body_cap8.to_le_bytes());
        data[8..16].copy_from_slice(&self.quarantine_end.to_le_bytes());
        data[16..24].copy_from_slice(&self.last_accepted_time.to_le_bytes());
        data[24..32].copy_from_slice(&self.trusted_height.to_le_bytes());
        data[32..64].copy_from_slice(&self.trusted_hash);
    }

    /// The body region's capacity in bytes.
    pub fn body_cap(&self) -> usize {
        self.body_cap8 as usize * 8
    }
}

/// The client's state PDA for a client program id.
pub fn state_address(client: &Pubkey) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[STATE_SEED], client)
}

/// Reads the head of a client state account after the checks a reader must make: the account is the fixed-seed
/// PDA of `client`, owned by `client`, with the layout version the reader knows.
pub fn read_checked(account: &AccountInfo, client: &Pubkey, version: u8) -> Result<Head, ProgramError> {
    if account.key != &state_address(client).0 {
        return Err(ProgramError::InvalidSeeds);
    }
    if account.owner != client {
        return Err(ProgramError::IllegalOwner);
    }
    let data = account.try_borrow_data()?;
    let head = Head::read(&data)?;
    if head.version != version {
        return Err(ProgramError::InvalidAccountData);
    }
    Ok(head)
}

//! The state account: head (sol-util::client_head) · the body region (borsh, `body_cap` bytes) · the ring region
//! (72-byte entries, as many as the account holds, 256 at most).
//!
//! Solana lets an account grow by 10 KB per instruction, so the account is created at 10 KB and anyone grows it
//! (`Grow`, rent paid by the caller) until the ring has its 256 entries; a bigger validator set needs a bigger body
//! region, which `Grow` makes by moving the ring.

use crate::{
    config::{HISTORY_MAX, RING, STATE_SLACK},
    error::ClientError,
};
use borsh::{BorshDeserialize, BorshSerialize};
use gno_light_core::valset::ValidatorSet;
use solana_program::{program_error::ProgramError, pubkey::Pubkey};
pub use sol_util::client_head::{Head, HEAD_LEN, STATE_SEED};

pub const RING_ENTRY: usize = 72;
/// The most an account grows per instruction (the runtime's MAX_PERMITTED_DATA_INCREASE).
pub const MAX_GROWTH: usize = 10_240;

/// A set change: from which height the set signs, its hash and keys, when it was announced, and when its
/// quarantine ends (0 for the set the client started with, settled by definition).
#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub struct SetEntry {
    pub from_height: i64,
    pub set_hash: [u8; 32],
    pub set: ValidatorSet,
    pub announced_slot: u64,
    pub announced_time: i64,
    pub quarantine_end: i64,
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub struct Trusted {
    pub height: i64,
    pub hash: [u8; 32],
    pub validators_hash: [u8; 32],
    pub next_validators_hash: [u8; 32],
    pub time_secs: i64,
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub struct Body {
    /// The admin program's authority PDA: the only key that may hand this role on (kind 9's `SetAdmin`); with the
    /// upgrade authority it is the client's whole set of controls (M2j: there is no reset).
    pub admin: Pubkey,
    pub chain_id: String,
    pub origin_domain: u32,
    pub trusted: Trusted,
    /// The set of the trusted header's height, and its hash.
    pub set: ValidatorSet,
    pub set_hash: [u8; 32],
    /// A set announced by the trusted header, signing from the next height on.
    pub next_set: Option<ValidatorSet>,
    pub next_set_hash: [u8; 32],
    pub history: Vec<SetEntry>,
    /// The record of every header accepted or proven: a counter and a running hash over (hash, height, slot).
    pub records: u64,
    pub running_hash: [u8; 32],
    /// The ring's epoch (S-16): one up at every `Ring::clear` (a set change). A ring proof copies its
    /// entry into its stage under the epoch of its staging, and `Verify` reads the copy while the epoch and the
    /// set are still the staging's — so the ring turning over (256 later headers, accepted or proven) evicts
    /// nothing a staged proof needs, while a clear still voids every proof staged before it.
    pub ring_epoch: u64,
}

impl Body {
    pub fn load(data: &[u8]) -> Result<Box<Body>, ProgramError> {
        let mut slice = data.get(HEAD_LEN..).ok_or(ClientError::NotInitialized)?;
        Body::deserialize(&mut slice).map(Box::new).map_err(|_| ProgramError::InvalidAccountData)
    }

    pub fn size(&self) -> usize {
        borsh::object_length(self).expect("borsh")
    }

    /// The body capacity a body wants: its size with slack, rounded to 8.
    pub fn wanted_cap(&self) -> usize {
        (self.size() + STATE_SLACK).div_ceil(8) * 8
    }

    /// Writes the body into its region, straight into the account (no buffer on the heap, whose 32 KB a body of
    /// big sets would strain); refuses a body bigger than the region (`Grow` first).
    pub fn store(&self, head: &Head, data: &mut [u8]) -> Result<(), ProgramError> {
        let len = self.size();
        if len > head.body_cap() {
            return Err(ClientError::NeedsGrow.into());
        }
        let end = HEAD_LEN + head.body_cap();
        let region = data.get_mut(HEAD_LEN..end).ok_or(ProgramError::AccountDataTooSmall)?;
        let (dst, rest) = region.split_at_mut(len);
        let mut w: &mut [u8] = dst;
        self.serialize(&mut w).map_err(|_| ProgramError::BorshIoError)?;
        rest.fill(0);
        Ok(())
    }

    /// Records a header: the counter and the running hash.
    pub fn record(&mut self, h: gno_light_core::Hasher, hash: &[u8; 32], height: i64, slot: u64) {
        self.records = self.records.saturating_add(1);
        self.running_hash = h(&[&self.running_hash, hash, &height.to_le_bytes(), &slot.to_le_bytes()]);
    }

    /// Adds a set change to the history. The newest entry whose quarantine has ended by the Clock is never
    /// evicted, nor any entry after it (S-01): only an entry older than it makes room, and with none the history
    /// is full (a fifth unsettled announcement answers `HistoryFull` until the oldest of them settles by the
    /// clock, a week after it). Nothing on chain reads the history since M2j (the admin program's record of a
    /// settled set went with the recovery path); the watch and the tools do.
    pub fn push_history(&mut self, entry: SetEntry, now: i64) -> Result<(), ClientError> {
        if self.history.len() >= HISTORY_MAX {
            match self.history.iter().rposition(|e| now > e.quarantine_end) {
                Some(i) if i > 0 => {
                    self.history.remove(0);
                }
                _ => return Err(ClientError::HistoryFull),
            }
        }
        self.history.push(entry);
        Ok(())
    }
}

/// One ring entry: the header hash, its height, its LastResultsHash (zero when the block before had no
/// transactions).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct RingEntry {
    pub hash: [u8; 32],
    pub height: i64,
    pub last_results_hash: [u8; 32],
}

pub struct Ring;

impl Ring {
    pub fn offset(head: &Head) -> usize {
        HEAD_LEN + head.body_cap()
    }

    /// How many entries the account holds.
    pub fn capacity(head: &Head, len: usize) -> usize {
        len.saturating_sub(Self::offset(head)) / RING_ENTRY
    }

    /// Empties the ring at a set change and moves the body to the next ring epoch (S-16: every staged ring proof
    /// is void).
    pub fn clear(head: &mut Head, body: &mut Body) {
        head.ring_count = 0;
        head.ring_next = 0;
        body.ring_epoch = body.ring_epoch.saturating_add(1);
    }

    /// Pushes an entry if the ring has any room (an account not yet grown keeps no ring). While the ring has free
    /// slots the entry takes the first of them, `ring_count` (S-17: a ring whose capacity grew past its entries —
    /// `Grow` leaves the region where it is while the account grows, and a moved region's ring grows back to 256
    /// — filled from `ring_next`, which stood below the old count, so live entries were overwritten while free
    /// slots stood); a full ring overwrites its oldest, at `ring_next`.
    pub fn push(head: &mut Head, data: &mut [u8], e: &RingEntry) {
        let cap = Self::capacity(head, data.len()).min(RING);
        if cap == 0 {
            return;
        }
        let count = head.ring_count as usize;
        let slot = if count < cap {
            count
        } else if head.ring_next as usize >= cap {
            0
        } else {
            head.ring_next as usize
        };
        let at = Self::offset(head) + slot * RING_ENTRY;
        data[at..at + 32].copy_from_slice(&e.hash);
        data[at + 32..at + 40].copy_from_slice(&e.height.to_le_bytes());
        data[at + 40..at + 72].copy_from_slice(&e.last_results_hash);
        head.ring_next = ((slot + 1) % cap) as u16;
        if count < cap {
            head.ring_count = (count + 1) as u16;
        }
    }

    pub fn find(head: &Head, data: &[u8], hash: &[u8; 32]) -> Option<RingEntry> {
        let cap = Self::capacity(head, data.len());
        for i in 0..(head.ring_count as usize).min(cap) {
            let at = Self::offset(head) + i * RING_ENTRY;
            if &data[at..at + 32] == hash {
                let mut e = RingEntry { hash: *hash, height: 0, last_results_hash: [0; 32] };
                e.height = i64::from_le_bytes(data[at + 32..at + 40].try_into().unwrap());
                e.last_results_hash.copy_from_slice(&data[at + 40..at + 72]);
                return Some(e);
            }
        }
        None
    }

    /// Moves the ring region to a new offset (a bigger body region), keeping the entries that still fit — every
    /// one when `grow` calls it (S-09: it waits for an account that holds them all), the next write where it was.
    pub fn relocate(head: &mut Head, data: &mut [u8], new_body_cap: usize) {
        let old = Self::offset(head);
        let count = (head.ring_count as usize).min(Self::capacity(head, data.len()));
        let new = HEAD_LEN + new_body_cap;
        let keep = ((data.len().saturating_sub(new)) / RING_ENTRY).min(count);
        // copy entries from the end so an overlapping move is safe
        for i in (0..keep).rev() {
            let (s, d) = (old + i * RING_ENTRY, new + i * RING_ENTRY);
            data.copy_within(s..s + RING_ENTRY, d);
        }
        let next = head.ring_next as usize;
        head.body_cap8 = (new_body_cap / 8) as u16;
        head.ring_count = keep as u16;
        let cap = Self::capacity(head, data.len()).max(1);
        let next = if keep == count && next < cap { next } else { keep % cap };
        head.ring_next = next as u16;
    }
}

/// The whole account length wanted: the head, the body region, a full ring.
pub fn full_len(body_cap: usize) -> usize {
    HEAD_LEN + body_cap + RING * RING_ENTRY
}

/// The length of a body region for a body: its wanted capacity.
pub fn body_cap_for(body: &Body) -> usize {
    body.wanted_cap()
}

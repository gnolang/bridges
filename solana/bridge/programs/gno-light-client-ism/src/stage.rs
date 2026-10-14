//! A staged update or proof: a PDA the submitter fills over several transactions (§6.1, §6.3). The account holds
//! the borsh `Stage`, then — for a proof — the results leaf as raw bytes, read in place (never copied through the
//! heap).

use borsh::{BorshDeserialize, BorshSerialize};
use gno_light_core::{
    commit::{Commit, Precommit},
    header::{BlockId, Header, Time},
    merkle::SimpleProof,
    valset::Validator,
};
use solana_program::{program_error::ProgramError, pubkey::Pubkey};

pub const KIND_UPDATE: u8 = 0;
pub const KIND_PROOF: u8 = 1;

/// What a present precommit needs for its sign bytes beyond the header and the commit's BlockID: its own
/// timestamp, and its round, which must be the stage's (S-08: one round per commit).
#[derive(BorshSerialize, BorshDeserialize, Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct StagedPrecommit {
    pub round: i64,
    pub secs: i64,
    pub nanos: i32,
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub struct Stage {
    pub version: u8,
    pub kind: u8,
    pub bump: u8,
    pub submitter: Pubkey,
    pub created_slot: u64,
    pub created_time: i64,
    /// The PDA's key: the header hash of an update, the message hash of a proof.
    pub key: [u8; 32],
    /// A proof by a ring entry: no header and no commit here.
    pub by_ring: bool,
    pub header_hash: [u8; 32],
    pub header: Header,
    pub block_id: BlockId,
    /// The commit's one round (S-08), from `StageInit`: every slot's sign bytes are built from it.
    pub round: i64,
    /// The set the signatures are checked against (the current or the announced one at staging time).
    pub set_hash: [u8; 32],
    pub n_slots: u16,
    pub precommits: Vec<StagedPrecommit>,
    /// Bitmaps over the slots: which carry a precommit, which have been verified.
    pub filled: Vec<u8>,
    pub verified: Vec<u8>,
    /// An announcing update's new set, filled in order by `StageNextSet` up to `next_set_len` entries (0 for a
    /// header announcing nothing); its hash must be the header's NextValidatorsHash, checked at the finish.
    pub next_set: Vec<Validator>,
    pub next_set_len: u16,
    pub next_set_hash: [u8; 32],
    /// A proof's path; its leaf follows the borsh bytes in the account.
    pub proof: Option<SimpleProof>,
    pub leaf_len: u32,
    pub leaf_filled: u32,
    /// A ring proof's copy of its entry (S-16): the height and the LastResultsHash the ring held for
    /// `header_hash` at the staging, under the body's ring epoch of that moment. `Verify` reads the copy while
    /// the body's epoch and set hash are still the staging's, so a later eviction of the entry — the ring turned
    /// over by 256 fresh headers, accepted or proven by a stranger — strands nothing; a set change clears the
    /// ring and bumps the epoch, and the entry must stand again (it never does). Zero in an update
    /// and in a proof by header.
    pub ring_epoch: u64,
    pub ring_height: i64,
    pub ring_results: [u8; 32],
}

pub fn bit(map: &[u8], i: usize) -> bool {
    map.get(i / 8).map(|b| b & (1 << (i % 8)) != 0).unwrap_or(false)
}

pub fn set_bit(map: &mut [u8], i: usize) {
    map[i / 8] |= 1 << (i % 8);
}

impl Stage {
    pub fn load(data: &[u8]) -> Result<Box<Stage>, ProgramError> {
        let mut slice = data;
        Stage::deserialize(&mut slice).map(Box::new).map_err(|_| ProgramError::InvalidAccountData)
    }

    /// Writes the stage straight into the account (no buffer on the heap: a stage with a big set is sizeable).
    pub fn store(&self, data: &mut [u8]) -> Result<(), ProgramError> {
        let len = self.size();
        let target = data.get_mut(..len).ok_or(ProgramError::AccountDataTooSmall)?;
        let mut w: &mut [u8] = target;
        self.serialize(&mut w).map_err(|_| ProgramError::BorshIoError)
    }

    /// The borsh length, which is also the leaf's offset in the account.
    pub fn size(&self) -> usize {
        borsh::object_length(self).expect("borsh")
    }

    pub fn account_len(&self) -> usize {
        self.size() + self.leaf_len as usize
    }

    pub fn is_filled(&self, slot: usize) -> bool {
        bit(&self.filled, slot)
    }

    pub fn is_verified(&self, slot: usize) -> bool {
        bit(&self.verified, slot)
    }

    /// The commit as the core sees it: the verified slots only (unverified precommits count for nothing), every
    /// one at the stage's round.
    pub fn verified_commit(&self) -> Commit {
        let precommits = (0..self.n_slots as usize)
            .map(|i| {
                if self.is_verified(i) {
                    let p = self.precommits[i];
                    Some(Precommit { round: self.round, time: Time { secs: p.secs, nanos: p.nanos } })
                } else {
                    None
                }
            })
            .collect();
        Commit { block_id: self.block_id.clone(), precommits }
    }

    /// The sign bytes of a filled slot: the stage's round, the slot's own timestamp.
    pub fn sign_bytes(&self, chain_id: &str, slot: usize) -> Vec<u8> {
        let p = self.precommits[slot];
        gno_light_core::vote::precommit_sign_bytes(chain_id, self.header.height, self.round, &self.block_id, &Time { secs: p.secs, nanos: p.nanos })
    }
}

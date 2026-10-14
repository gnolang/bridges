//! A commit as the client stages it, and the commit rule.
//!
//! tm2's `VerifyCommit` (validator_set.go:632): as many precommit slots as validators, counted by slot index; a nil
//! slot is skipped; every present precommit's signature must verify over its own sign bytes; only a precommit for
//! the commit's BlockID is tallied; `tallied > total·2/3`, which is `3·tallied > 2·total`. It runs
//! `Commit.ValidateBasic` (block.go:552) first, which requires every precommit's round to be the commit's — the
//! first present precommit's, `Commit.Round` — so a commit is of one round (S-08): `check_shape` requires it, and
//! the client carries that round in the stage and builds every slot's sign bytes from it.
//!
//! The client stages, per slot, either nothing or a precommit *for the commit's BlockID*: a precommit for nil or
//! for another block is a real signature that must not be tallied, so the submitter drops it to an empty slot and
//! the client never pays to verify it (M2-NOTES). What a present precommit needs for its sign bytes beyond the
//! header's height and chain id, the commit's BlockID and its round is its own timestamp; its signature travels
//! in the ed25519 precompile instruction, never in the account.

use crate::{
    header::{BlockId, Time},
    valset::ValidatorSet,
    vote, Error,
};
use borsh::{BorshDeserialize, BorshSerialize};

#[derive(BorshSerialize, BorshDeserialize, Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Precommit {
    pub round: i64,
    pub time: Time,
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, Default, PartialEq, Eq)]
pub struct Commit {
    pub block_id: BlockId,
    pub precommits: Vec<Option<Precommit>>,
}

impl Commit {
    /// The commit's round: the first present precommit's, as tm2's `Commit.Round` reads it (0 with none).
    pub fn round(&self) -> i64 {
        self.precommits.iter().flatten().next().map(|p| p.round).unwrap_or(0)
    }

    /// The sign bytes of the precommit in slot `idx`, or None for an empty slot.
    pub fn sign_bytes(&self, chain_id: &str, height: i64, idx: usize) -> Option<Vec<u8>> {
        let p = self.precommits.get(idx)?.as_ref()?;
        Some(vote::precommit_sign_bytes(chain_id, height, p.round, &self.block_id, &p.time))
    }

    /// The shape a commit must have for a header and a set: its BlockID is the header's hash with a one-or-more
    /// part set and a 32-byte parts hash, it has exactly one slot per validator, and its precommits are of one
    /// round (tm2's `Commit.ValidateBasic`, S-08).
    pub fn check_shape(&self, header_hash: &[u8; 32], set_len: usize) -> Result<(), Error> {
        if self.block_id.hash.as_slice() != header_hash {
            return Err(Error::BadCommit);
        }
        if self.block_id.parts.total == 0 || self.block_id.parts.hash.len() != 32 {
            return Err(Error::BadCommit);
        }
        if self.precommits.len() != set_len {
            return Err(Error::BadCommit);
        }
        let round = self.round();
        for p in self.precommits.iter().flatten() {
            if p.round != round || p.round < 0 || p.time.nanos < 0 || p.time.nanos > 999_999_999 {
                return Err(Error::BadCommit);
            }
        }
        Ok(())
    }

    /// The slots that carry a precommit.
    pub fn present(&self) -> impl Iterator<Item = usize> + '_ {
        self.precommits.iter().enumerate().filter(|(_, p)| p.is_some()).map(|(i, _)| i)
    }
}

/// The power of the verified slots.
pub fn tallied_power(set: &ValidatorSet, verified: impl Fn(usize) -> bool) -> Result<i64, Error> {
    let mut t: i64 = 0;
    for (i, v) in set.validators.iter().enumerate() {
        if verified(i) {
            t = t.checked_add(v.power).ok_or(Error::Overflow)?;
        }
    }
    Ok(t)
}

/// The chain's own rule, weighted by power: more than two thirds.
pub fn commit_rule(tallied: i64, total: i64) -> bool {
    tallied >= 0 && total > 0 && (tallied as i128) * 3 > (total as i128) * 2
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn two_thirds() {
        assert!(commit_rule(201, 300));
        assert!(!commit_rule(200, 300));
        assert!(commit_rule(240, 300));
        // the six-validator set of height 720,333: 60,60,60,1,60,60 = 301
        assert!(commit_rule(240, 301));
        assert!(!commit_rule(181, 301));
        assert!(commit_rule(201, 301));
        assert!(!commit_rule(200, 301));
        assert!(!commit_rule(0, 0));
        assert!(commit_rule(i64::MAX / 8, i64::MAX / 8));
    }

    /// A commit's precommits are of one round, the first present precommit's (S-08).
    #[test]
    fn one_round_per_commit() {
        use crate::header::PartSetHeader;
        let p = |round: i64| Some(Precommit { round, time: Time { secs: 1, nanos: 0 } });
        let block_id = BlockId { hash: vec![7; 32], parts: PartSetHeader { total: 1, hash: vec![8; 32] } };
        let mut c = Commit { block_id, precommits: vec![None, p(3), p(3), None] };
        assert_eq!(c.round(), 3);
        c.check_shape(&[7; 32], 4).unwrap();
        c.precommits[3] = p(0);
        assert_eq!(c.round(), 3, "the first present precommit's");
        assert!(c.check_shape(&[7; 32], 4).is_err(), "mixed rounds");
        c.precommits[3] = p(3);
        c.check_shape(&[7; 32], 4).unwrap();
        assert_eq!(Commit::default().round(), 0);
    }
}

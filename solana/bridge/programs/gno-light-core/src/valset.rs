//! Validators and their set: the leaf `0A37 { 0A11"/tm.PubKeyEd25519" 1222 { 0A20<pk> } } 10 <zigzag power>`,
//! the set hash (a simple merkle over the leaves in set order), the order (by address = `sha256(pk)[:20]`,
//! ascending), and the arithmetic of power (tm2 validator.go, validator_set.go).

use crate::{
    amino::*,
    merkle::{self, Hasher},
    Error,
};
use borsh::{BorshDeserialize, BorshSerialize};

pub const PUBKEY_TYPE_URL: &str = "/tm.PubKeyEd25519";
/// The client accepts up to 255 validators (a staged set's count byte).
pub const MAX_VALIDATORS: usize = 255;
/// tm2's MaxTotalVotingPower.
pub const MAX_TOTAL_POWER: i64 = i64::MAX / 8;

#[derive(BorshSerialize, BorshDeserialize, Clone, Copy, Debug, PartialEq, Eq)]
pub struct Validator {
    pub pubkey: [u8; 32],
    pub power: i64,
}

impl Validator {
    /// `crypto.PubKey.Address()` for ed25519: the first twenty bytes of sha256 of the key.
    pub fn address(&self, h: Hasher) -> [u8; 20] {
        let d = h(&[&self.pubkey]);
        let mut a = [0u8; 20];
        a.copy_from_slice(&d[..20]);
        a
    }

    /// `Validator.Bytes()`: amino of `struct{ PubKey crypto.PubKey; VotingPower int64 }`.
    pub fn leaf(&self) -> Vec<u8> {
        let mut pk = Vec::with_capacity(34);
        put_bytes_field(&mut pk, 1, &self.pubkey);
        let any = any(PUBKEY_TYPE_URL, &pk);
        let mut out = Vec::with_capacity(70);
        put_bytes_field(&mut out, 1, &any);
        put_zigzag_field(&mut out, 2, self.power);
        out
    }
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, Default, PartialEq, Eq)]
pub struct ValidatorSet {
    pub validators: Vec<Validator>,
}

impl ValidatorSet {
    pub fn len(&self) -> usize {
        self.validators.len()
    }

    pub fn is_empty(&self) -> bool {
        self.validators.is_empty()
    }

    /// `ValidatorSet.Hash()`; None for an empty set.
    pub fn hash(&self, h: Hasher) -> Option<[u8; 32]> {
        let leaves: Vec<Vec<u8>> = self.validators.iter().map(|v| v.leaf()).collect();
        let refs: Vec<&[u8]> = leaves.iter().map(|l| l.as_slice()).collect();
        merkle::simple_hash(h, &refs)
    }

    pub fn total_power(&self) -> Result<i64, Error> {
        let mut t: i64 = 0;
        for v in &self.validators {
            t = t.checked_add(v.power).ok_or(Error::Overflow)?;
        }
        Ok(t)
    }

    /// What a set the client stores must be: 1–255 validators, each with a positive power, the total within tm2's
    /// bound, addresses strictly ascending (so unique).
    pub fn check(&self, h: Hasher) -> Result<(), Error> {
        if self.validators.is_empty() || self.validators.len() > MAX_VALIDATORS {
            return Err(Error::BadValidatorSet);
        }
        let mut prev: Option<[u8; 20]> = None;
        for v in &self.validators {
            if v.power <= 0 {
                return Err(Error::BadValidatorSet);
            }
            let a = v.address(h);
            if let Some(p) = prev {
                if a <= p {
                    return Err(Error::BadValidatorSet);
                }
            }
            prev = Some(a);
        }
        if self.total_power()? > MAX_TOTAL_POWER {
            return Err(Error::BadValidatorSet);
        }
        Ok(())
    }

    /// Sorts by address, as tm2 keeps a set (the tools build sets this way; the chain's `/validators` answer is
    /// already in this order).
    pub fn sort(&mut self, h: Hasher) {
        self.validators.sort_by_key(|v| v.address(h));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn leaf_form() {
        let v = Validator { pubkey: [0xaa; 32], power: 60 };
        let l = v.leaf();
        assert_eq!(&l[..2], &[0x0a, 0x37]);
        assert_eq!(&l[2..4], &[0x0a, 0x11]);
        assert_eq!(&l[4..21], PUBKEY_TYPE_URL.as_bytes());
        assert_eq!(&l[21..25], &[0x12, 0x22, 0x0a, 0x20]);
        assert_eq!(&l[57..], &[0x10, 0x78]);
        assert_eq!(l.len(), 59);
    }
}

//! tm2's block header (gno v1.2.0 tm2/pkg/bft/types/block.go) and its hash: `SimpleHashFromByteSlices` over the
//! sixteen amino-encoded fields in order, each field marshalled on its own (`bytesOrNil`: an empty field is an
//! empty leaf, whose hash is `sha256(0x00)`), §13's "header leaves".
//!
//! The borsh derives are the wire form the programs stage: every string and byte field length-prefixed, so a nil
//! hash is an empty vector.

use crate::{
    amino::*,
    merkle::{self, Hasher},
    Error,
};
use borsh::{BorshDeserialize, BorshSerialize};

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, Default, PartialEq, Eq)]
pub struct PartSetHeader {
    pub total: u32,
    pub hash: Vec<u8>,
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, Default, PartialEq, Eq)]
pub struct BlockId {
    pub hash: Vec<u8>,
    pub parts: PartSetHeader,
}

impl BlockId {
    pub fn is_empty(&self) -> bool {
        self.hash.is_empty() && self.parts.total == 0 && self.parts.hash.is_empty()
    }

    /// tm2's `BlockID` as a header field or a commit's: `PartSetHeader.Total` is a Go `int`, a zigzag varint.
    pub fn amino_header_form(&self) -> Vec<u8> {
        let mut parts = Vec::with_capacity(40);
        put_zigzag_field(&mut parts, 1, self.parts.total as i64);
        put_bytes_field(&mut parts, 2, &self.parts.hash);
        let mut out = Vec::with_capacity(80);
        put_bytes_field(&mut out, 1, &self.hash);
        put_bytes_field(&mut out, 2, &parts);
        out
    }

    /// `CanonicalBlockID`, the sign bytes' form: `Total` is a uint32, a plain uvarint.
    pub fn amino_canonical_form(&self) -> Vec<u8> {
        let mut parts = Vec::with_capacity(40);
        put_uvarint_field(&mut parts, 1, self.parts.total as u64);
        put_bytes_field(&mut parts, 2, &self.parts.hash);
        let mut out = Vec::with_capacity(80);
        put_bytes_field(&mut out, 1, &self.hash);
        put_bytes_field(&mut out, 2, &parts);
        out
    }
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Time {
    pub secs: i64,
    pub nanos: i32,
}

impl Time {
    pub fn amino_bare(&self) -> Vec<u8> {
        time_bare(self.secs, self.nanos)
    }
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, Default, PartialEq, Eq)]
pub struct Header {
    pub version: String,
    pub chain_id: String,
    pub height: i64,
    pub time: Time,
    pub num_txs: i64,
    pub total_txs: i64,
    pub app_version: String,
    pub last_block_id: BlockId,
    pub last_commit_hash: Vec<u8>,
    pub data_hash: Vec<u8>,
    pub validators_hash: Vec<u8>,
    pub next_validators_hash: Vec<u8>,
    pub consensus_hash: Vec<u8>,
    pub app_hash: Vec<u8>,
    pub last_results_hash: Vec<u8>,
    /// The bech32 string (`g1…`), which is what amino writes for a `crypto.Address`.
    pub proposer_address: String,
}

/// A top-level string or byte field: `amino.Marshal` of a non-struct wraps it as field 1.
fn wrapped_bytes(b: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(b.len() + 3);
    put_bytes_field(&mut out, 1, b);
    out
}

fn wrapped_zigzag(v: i64) -> Vec<u8> {
    let mut out = Vec::with_capacity(11);
    put_zigzag_field(&mut out, 1, v);
    out
}

impl Header {
    /// The sixteen leaves, each the amino bytes of one field (empty where the field is empty).
    pub fn leaves(&self) -> [Vec<u8>; 16] {
        [
            wrapped_bytes(self.version.as_bytes()),
            wrapped_bytes(self.chain_id.as_bytes()),
            wrapped_zigzag(self.height),
            self.time.amino_bare(),
            wrapped_zigzag(self.num_txs),
            wrapped_zigzag(self.total_txs),
            wrapped_bytes(self.app_version.as_bytes()),
            self.last_block_id.amino_header_form(),
            wrapped_bytes(&self.last_commit_hash),
            wrapped_bytes(&self.data_hash),
            wrapped_bytes(&self.validators_hash),
            wrapped_bytes(&self.next_validators_hash),
            wrapped_bytes(&self.consensus_hash),
            wrapped_bytes(&self.app_hash),
            wrapped_bytes(&self.last_results_hash),
            wrapped_bytes(self.proposer_address.as_bytes()),
        ]
    }

    /// `Header.Hash()`: the merkle root of the leaves.
    pub fn hash(&self, h: Hasher) -> [u8; 32] {
        let leaves = self.leaves();
        let refs: Vec<&[u8]> = leaves.iter().map(|l| l.as_slice()).collect();
        merkle::simple_hash(h, &refs).expect("sixteen leaves")
    }

    /// The sanity of a header the client is asked to trust or verify: a positive height, 32-byte hashes where a
    /// hash must stand, a chain id and version that fit the staging accounts.
    pub fn check(&self) -> Result<(), Error> {
        if self.height <= 0 || self.num_txs < 0 || self.total_txs < 0 {
            return Err(Error::BadHeader);
        }
        if self.chain_id.is_empty() || self.chain_id.len() > 64 || self.version.len() > 64 || self.app_version.len() > 64 {
            return Err(Error::BadHeader);
        }
        if self.validators_hash.len() != 32 || self.next_validators_hash.len() != 32 {
            return Err(Error::BadHeader);
        }
        for opt in [&self.last_commit_hash, &self.data_hash, &self.consensus_hash, &self.app_hash, &self.last_results_hash] {
            if !(opt.is_empty() || opt.len() == 32) {
                return Err(Error::BadHeader);
            }
        }
        if !(self.last_block_id.hash.is_empty() || self.last_block_id.hash.len() == 32)
            || !(self.last_block_id.parts.hash.is_empty() || self.last_block_id.parts.hash.len() == 32)
        {
            return Err(Error::BadHeader);
        }
        if self.proposer_address.len() > 64 {
            return Err(Error::BadHeader);
        }
        Ok(())
    }

    pub fn validators_hash_array(&self) -> [u8; 32] {
        let mut a = [0u8; 32];
        a.copy_from_slice(&self.validators_hash);
        a
    }

    pub fn next_validators_hash_array(&self) -> [u8; 32] {
        let mut a = [0u8; 32];
        a.copy_from_slice(&self.next_validators_hash);
        a
    }

    /// `LastResultsHash` as an array, or None when the previous block had no transactions.
    pub fn last_results_hash_array(&self) -> Option<[u8; 32]> {
        if self.last_results_hash.len() == 32 {
            let mut a = [0u8; 32];
            a.copy_from_slice(&self.last_results_hash);
            Some(a)
        } else {
            None
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn leaf_forms_match_the_appendix() {
        // §13: Version `0A0B"v1.0.0-rc.0"`, ChainID `0A09"gnoland-1"`, Height `08`+zigzag, an empty field is empty.
        let mut h = Header::default();
        h.version = "v1.0.0-rc.0".into();
        h.chain_id = "gnoland-1".into();
        h.height = 701862;
        let leaves = h.leaves();
        assert_eq!(leaves[0], [&[0x0a, 0x0b][..], b"v1.0.0-rc.0"].concat());
        assert_eq!(leaves[1], [&[0x0a, 0x09][..], b"gnoland-1"].concat());
        assert_eq!(leaves[2], [0x08, 0xcc, 0xd6, 0x55]); // zigzag(701862) = 1403724 = 0x156b4c
        assert!(leaves[4].is_empty()); // NumTxs 0
        assert!(leaves[7].is_empty()); // LastBlockID empty
        // LastBlockID: `0A20<h> 1224 {0802 1220 <ph>}`
        h.last_block_id = BlockId { hash: vec![7u8; 32], parts: PartSetHeader { total: 1, hash: vec![9u8; 32] } };
        let l = h.leaves()[7].clone();
        assert_eq!(&l[..2], &[0x0a, 0x20]);
        assert_eq!(&l[34..40], &[0x12, 0x24, 0x08, 0x02, 0x12, 0x20]);
        assert_eq!(l.len(), 2 + 32 + 2 + 36);
    }
}

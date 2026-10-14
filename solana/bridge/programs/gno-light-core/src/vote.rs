//! A precommit's sign bytes: `amino.MarshalSized(CanonicalVote)` (tm2 vote.go:73, canonical.go), §13:
//!
//! `uvarint(len)` · `08 02` (Type precommit) · `11` + 8-byte LE height · [`19` + 8-byte LE round, omitted when 0]
//! · `22 len { 0A 20 <blockhash> 12 24 { 08 uvarint(total) 12 20 <partshash> } }` · `2A len { 08 uvarint(sec)
//! [10 uvarint(ns)] }` · `32 len <chain id>`.
//!
//! Built per precommit from its own BlockID and its own timestamp; a nil precommit (an empty BlockID) signs the
//! same bytes without field 4.

use crate::{
    amino::*,
    header::{BlockId, Time},
};

pub const PRECOMMIT_TYPE: u8 = 2;
pub const PREVOTE_TYPE: u8 = 1;

/// The sign bytes of a vote of the given type.
pub fn canonical_vote(typ: u8, chain_id: &str, height: i64, round: i64, block_id: &BlockId, time: &Time) -> Vec<u8> {
    let mut body = Vec::with_capacity(128);
    put_uvarint_field(&mut body, 1, typ as u64);
    put_fixed64_field(&mut body, 2, height as u64);
    put_fixed64_field(&mut body, 3, round as u64);
    put_bytes_field(&mut body, 4, &block_id.amino_canonical_form());
    put_bytes_field(&mut body, 5, &time.amino_bare());
    put_bytes_field(&mut body, 6, chain_id.as_bytes());
    sized(&body)
}

/// A precommit's sign bytes.
pub fn precommit_sign_bytes(chain_id: &str, height: i64, round: i64, block_id: &BlockId, time: &Time) -> Vec<u8> {
    canonical_vote(PRECOMMIT_TYPE, chain_id, height, round, block_id, time)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::header::PartSetHeader;

    /// §13's full example at height 701,862, round 0, one part.
    #[test]
    fn appendix_example() {
        let want = hex_to_vec(
            "6D080211A6B50A000000000022480A20B0D79144E283B51F02881A930A56E0D98DB717702D116DFF92271FA5A75A872D1224080112205AE9C4415816870AB1B14D7024D797C7E3DC3098CC3DDB01F11E5F309360112F2A0B08A9C9A8D60610DFC2B9133209676E6F6C616E642D31",
        );
        let block_id = BlockId {
            hash: hex_to_vec("B0D79144E283B51F02881A930A56E0D98DB717702D116DFF92271FA5A75A872D"),
            parts: PartSetHeader { total: 1, hash: hex_to_vec("5AE9C4415816870AB1B14D7024D797C7E3DC3098CC3DDB01F11E5F309360112F") },
        };
        // 2A 0B { 08 A9C9A8D606 10 DFC2B913 }: secs 1791632553, nanos 40788319
        let time = Time { secs: 1791632553, nanos: 40788319 };
        let got = precommit_sign_bytes("gnoland-1", 701862, 0, &block_id, &time);
        assert_eq!(hex::encode_upper(&got), hex::encode_upper(&want));
        assert_eq!(got.len(), 110);
    }

    fn hex_to_vec(s: &str) -> Vec<u8> {
        hex::decode(s).unwrap()
    }
}

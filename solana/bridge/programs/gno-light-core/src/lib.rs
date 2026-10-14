//! gno-light-core: the pure logic of the gno.land light client, with no Solana dependency.
//!
//! What it knows (PLAN.md §13, verified against gnoland-1's own headers in `../fixtures` by `cargo test`):
//! the header hash (a 16-leaf RFC 6962 merkle over the amino-encoded fields), each precommit's sign bytes (its own
//! BlockID and timestamp, the part set's Total, the chain id), the commit rule (count by slot index, nil precommits
//! untallied, `3·tallied > 2·total` by power), the validator leaf and ValidatorsHash, the `SimpleProof` over
//! `LastResultsHash`, the ABCIResult leaf and the `Dispatch` event, and a decision body's prefix.
//!
//! Hashing is a function the caller passes (`merkle::Hasher`): sha256 on the host, Solana's `hashv` syscall on
//! chain, so the same code runs in the programs and in the tests.
pub mod amino;
pub mod commit;
pub mod decision;
pub mod error;
pub mod header;
pub mod merkle;
pub mod results;
pub mod valset;
pub mod vote;

#[cfg(feature = "fixtures")]
pub mod fixtures;
#[cfg(feature = "testkit")]
pub mod testkit;

pub use error::Error;
pub use merkle::Hasher;

/// sha256 of the concatenation, for hosts (tests, tools). The programs pass the syscall instead.
#[cfg(any(feature = "testkit", feature = "fixtures", test))]
pub fn sha256(parts: &[&[u8]]) -> [u8; 32] {
    use sha2::Digest;
    let mut h = sha2::Sha256::new();
    for p in parts {
        h.update(p);
    }
    h.finalize().into()
}

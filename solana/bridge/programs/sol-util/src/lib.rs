//! sol-util: what the three bridge programs share on Solana.
//!
//! - `ed25519`: the precompile introspection of §6.3 (the program id, `num_signatures`, every offset inside the
//!   precompile instruction's own data, the message length);
//! - `pda`: creating, growing and closing program accounts;
//! - `hl`: Hyperlane's interface discriminators, the message, the return data of the simulated questions, the
//!   mailbox's and the token's PDAs, instructions and account layouts (from the sealevel sources at the mainnet
//!   builds' commit);
//! - `loader`: the upgradeable loader's account layouts and instructions (bincode, by hand);
//! - `guardians`: four seats, two watch slots, a flag per key with its set-slot, holds, the unpause and the
//!   replacement rules — one code for the pausable ISM and the admin program, so the two lists never differ;
//! - `clock`: the durations, divided by a compiled factor.
pub mod client_head;
pub mod clock;
pub mod ed25519;
pub mod guardians;
pub mod hl;
pub mod loader;
pub mod pda;

use solana_program::hash::hashv;

/// The sha256 syscall in the shape gno-light-core wants.
pub fn sha256(parts: &[&[u8]]) -> [u8; 32] {
    hashv(parts).to_bytes()
}

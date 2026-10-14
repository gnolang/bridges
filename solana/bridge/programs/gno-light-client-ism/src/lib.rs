//! The gno.land light-client ISM (PLAN.md §6.1–§6.3, §13).
//!
//! State (one PDA, seed `state`): a 64-byte head the other programs read (version, clock factor, the quarantine's
//! end, the last accepted header's time, the trusted height and hash), a ring of the last 256 accepted header
//! hashes of the current set, and a borsh body: the chain id and origin domain, the trusted header, the current
//! set, an announced next set, the set history with its quarantine ends, the record counter and running hash.
//!
//! Updates and proofs are staged in PDAs keyed by (submitter, header hash) or (submitter, message hash): one
//! transaction writes the header and the commit's timestamps, one or two carry ed25519 precompile instructions
//! the program reads back (sol-util::ed25519) and marks verified slot by slot, and one finishes — an update moves
//! the trusted header, a proof is consumed by the mailbox's `Verify`.
pub mod config;
pub mod error;
pub mod instruction;
pub mod processor;
pub mod stage;
pub mod state;

pub use config::{id, ID};

#[cfg(not(feature = "no-entrypoint"))]
solana_program::entrypoint!(process_instruction);

pub fn process_instruction(program_id: &solana_program::pubkey::Pubkey, accounts: &[solana_program::account_info::AccountInfo], data: &[u8]) -> solana_program::entrypoint::ProgramResult {
    processor::process(program_id, accounts, data)
}

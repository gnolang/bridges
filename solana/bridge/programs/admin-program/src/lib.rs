//! The admin program (PLAN.md §6.5, §5.8, §13): immutable, a mailbox recipient under the light-client ISM, the
//! holder of every Solana authority from deploy. It records GovDAO's decisions as they arrive from r/hlgov (the
//! 9-byte prefix: kind, sequence number), executes each after its clock once no flag or hold defers it, and keeps
//! its own guardian seats, watch slots and flags. Every change on Solana is a light-client proof of a GovDAO
//! decision: there is no second channel (M2j took the validators' recovery path out — a lapsed client stays
//! lapsed, and the exit is a fresh deployment).
pub mod config;
pub mod error;
pub mod instruction;
pub mod kinds;
pub mod processor;
pub mod state;

pub use config::{id, ID};

#[cfg(not(feature = "no-entrypoint"))]
solana_program::entrypoint!(process_instruction);

pub fn process_instruction(program_id: &solana_program::pubkey::Pubkey, accounts: &[solana_program::account_info::AccountInfo], data: &[u8]) -> solana_program::entrypoint::ProgramResult {
    processor::process(program_id, accounts, data)
}

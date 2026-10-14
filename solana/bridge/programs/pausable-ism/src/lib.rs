//! The pausable ISM (PLAN.md §5.8, §6.5, §13): the program in front of the light-client ISM where the
//! deliveries' flags live. It keeps the four guardian seats and two watch slots, a flag per key with its set-slot,
//! and the client it fronts; its owner is the admin program's authority PDA. `Verify` derives its own flag and
//! state accounts, requires the client's state account to be the fixed-seed PDA owned by the client it stores
//! with layout version 1 — fixed for the deployment's life (v27 §13) — refuses while any flag is set or a
//! quarantine stands, and forwards the rest to the client.
pub mod config;
pub mod error;
pub mod instruction;
pub mod processor;
pub mod state;

pub use config::{id, ID};

#[cfg(not(feature = "no-entrypoint"))]
solana_program::entrypoint!(process_instruction);

pub fn process_instruction(program_id: &solana_program::pubkey::Pubkey, accounts: &[solana_program::account_info::AccountInfo], data: &[u8]) -> solana_program::entrypoint::ProgramResult {
    processor::process(program_id, accounts, data)
}

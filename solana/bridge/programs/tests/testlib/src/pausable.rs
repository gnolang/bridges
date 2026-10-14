//! Instruction builders for the pausable ISM.

use pausable_ism::{
    config::{FLAGS_SEED, STATE_SEED},
    instruction::Instruction as Ix,
    ID,
};
use sol_util::hl;
use solana_sdk::{
    instruction::{AccountMeta, Instruction},
    pubkey::Pubkey,
};
use solana_system_interface::program as system_program;

pub fn state() -> Pubkey {
    Pubkey::find_program_address(&[STATE_SEED], &ID).0
}

pub fn flags() -> Pubkey {
    Pubkey::find_program_address(&[FLAGS_SEED], &ID).0
}

fn data(ix: &Ix) -> Vec<u8> {
    borsh::to_vec(ix).unwrap()
}

pub fn init(payer: &Pubkey, authority: &Pubkey, owner: Pubkey, target: Pubkey, target_version: u8, seats: [Pubkey; 4], watch: [Pubkey; 2]) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: vec![
            AccountMeta::new(state(), false),
            AccountMeta::new(flags(), false),
            AccountMeta::new(*payer, true),
            AccountMeta::new_readonly(*authority, true),
            AccountMeta::new_readonly(ID, false),
            AccountMeta::new_readonly(sol_util::loader::programdata_address(&ID), false),
            AccountMeta::new_readonly(system_program::ID, false),
        ],
        data: data(&Ix::Init { owner, target, target_version, seats, watch }),
    }
}

pub fn set_flag(key: &Pubkey) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(flags(), false), AccountMeta::new_readonly(*key, true)], data: data(&Ix::SetFlag) }
}

pub fn clear_flag(key: &Pubkey) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(flags(), false), AccountMeta::new_readonly(*key, true)], data: data(&Ix::ClearFlag) }
}

pub fn unpause(owner: &Pubkey, before_slot: u64) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(flags(), false), AccountMeta::new_readonly(state(), false), AccountMeta::new_readonly(*owner, true)], data: data(&Ix::Unpause { before_slot }) }
}

pub fn set_guardians(owner: &Pubkey, pairs: Vec<(u8, Pubkey)>) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(flags(), false), AccountMeta::new_readonly(state(), false), AccountMeta::new_readonly(*owner, true)], data: data(&Ix::SetGuardians { pairs }) }
}

pub fn set_layout_version(owner: &Pubkey, target: Pubkey, version: u8) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(state(), false), AccountMeta::new_readonly(*owner, true)], data: data(&Ix::SetLayoutVersion { target, version }) }
}

pub fn transfer_ownership(owner: &Pubkey, new_owner: Pubkey) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(state(), false), AccountMeta::new_readonly(*owner, true)], data: data(&Ix::TransferOwnership { new_owner }) }
}

pub fn set_mint_limits(owner: &Pubkey, token: u8, max: u64, day_cap: u64) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(state(), false), AccountMeta::new_readonly(*owner, true)], data: data(&Ix::SetMintLimits { token, max, day_cap }) }
}

/// This ISM's `Verify` accounts as `VerifyAccountMetas` answers them: flags, state (w), the client's state (w),
/// the client program, the Instructions sysvar, then the client's own.
pub fn verify_accounts(client: &Pubkey, client_state: &Pubkey, client_accounts: Vec<AccountMeta>) -> Vec<AccountMeta> {
    let mut accounts = vec![
        AccountMeta::new_readonly(flags(), false),
        AccountMeta::new(state(), false),
        AccountMeta::new(*client_state, false),
        AccountMeta::new_readonly(*client, false),
        AccountMeta::new_readonly(solana_sdk::sysvar::instructions::ID, false),
    ];
    accounts.extend(client_accounts);
    accounts
}

/// The ISM `Verify` called directly (never by the mailbox): refused, since the top-level instruction is not the
/// mailbox's `InboxProcess`.
pub fn verify(client: &Pubkey, client_state: &Pubkey, client_accounts: Vec<AccountMeta>, metadata: Vec<u8>, message: Vec<u8>) -> Instruction {
    Instruction { program_id: ID, accounts: verify_accounts(client, client_state, client_accounts), data: hl::encode_interface(hl::ISM_VERIFY, &hl::VerifyInstruction { metadata, message }) }
}

/// `InboxProcess` of a message to `recipient` through this ISM (the mailbox's default ISM, which a recipient
/// answering no ISM of its own gets): the mailbox's accounts, the ISM's, the recipient program, then `handle`.
pub fn process_through(payer: &Pubkey, message: &[u8], submitter: &Pubkey, recipient: &Pubkey, handle: Vec<AccountMeta>) -> Instruction {
    let mailbox = pausable_ism::config::MAILBOX;
    let id = hl::Message::id(message);
    let msg_hash = gno_light_core::sha256(&[message]);
    let metadata = submitter.to_bytes().to_vec();
    let mut d = vec![1u8];
    d.extend_from_slice(&(metadata.len() as u32).to_le_bytes());
    d.extend_from_slice(&metadata);
    d.extend_from_slice(&(message.len() as u32).to_le_bytes());
    d.extend_from_slice(message);
    let mut accounts = vec![
        AccountMeta::new(*payer, true),
        AccountMeta::new_readonly(system_program::ID, false),
        AccountMeta::new(hl::mailbox_inbox(&mailbox), false),
        AccountMeta::new_readonly(hl::mailbox_process_authority(&mailbox, recipient), false),
        AccountMeta::new(hl::mailbox_processed_message(&mailbox, &id), false),
        // no accounts for the recipient's InterchainSecurityModule question
        AccountMeta::new_readonly(crate::admin::NOOP, false),
        AccountMeta::new_readonly(ID, false),
    ];
    accounts.extend(verify_accounts(&gno_light_client_ism::ID, &crate::client::state(), vec![AccountMeta::new_readonly(crate::client::proof_stage(submitter, &msg_hash), false)]));
    accounts.push(AccountMeta::new_readonly(*recipient, false));
    accounts.extend(handle);
    Instruction { program_id: mailbox, accounts, data: d }
}

pub fn verify_account_metas(client: Option<&Pubkey>, metadata: Vec<u8>, message: Vec<u8>) -> Instruction {
    let mut accounts = vec![AccountMeta::new_readonly(flags(), false), AccountMeta::new_readonly(state(), false)];
    if let Some(c) = client {
        accounts.push(AccountMeta::new_readonly(*c, false));
    }
    Instruction { program_id: ID, accounts, data: hl::encode_interface(hl::ISM_VERIFY_ACCOUNT_METAS, &hl::VerifyInstruction { metadata, message }) }
}

pub fn ism_type(client: &Pubkey) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new_readonly(state(), false), AccountMeta::new_readonly(*client, false)], data: hl::ISM_TYPE.to_vec() }
}

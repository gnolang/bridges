//! Instruction builders for the admin program, and the mailbox delivery of a decision.

use admin_program::{
    config::{AUTHORITY_SEED, CLIENT, MAILBOX, PAUSABLE, STATE_SEED, TOKENS},
    instruction::Instruction as Ix,
    kinds::{self, Params},
    state::Decision,
    ID,
};
use sol_util::{hl, loader};
use solana_sdk::{
    instruction::{AccountMeta, Instruction},
    pubkey::Pubkey,
    sysvar,
};
use solana_system_interface::program as system_program;

pub const NOOP: Pubkey = solana_sdk::pubkey!("noopb9bkMVfRPU8AsbpTUg8AQkHtKwMYZiFUjNRtMmV");

pub fn state() -> Pubkey {
    Pubkey::find_program_address(&[STATE_SEED], &ID).0
}

pub fn authority() -> Pubkey {
    authority_of(&ID)
}

pub fn decision(number: u64) -> Pubkey {
    decision_of(&ID, number)
}

/// The authority PDA of an admin program at another id (a successor).
pub fn authority_of(program: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(&[AUTHORITY_SEED], program).0
}

/// A decision PDA of an admin program at another id.
pub fn decision_of(program: &Pubkey, number: u64) -> Pubkey {
    Decision::address(program, number).0
}

fn data(ix: &Ix) -> Vec<u8> {
    borsh::to_vec(ix).unwrap()
}

/// The launch deployment's `init`, starting at `last_seq` (0 for a chain's first deployment; r/hlgov's number for
/// a fresh deployment under the same r/hlgov, AT3).
pub fn init(payer: &Pubkey, upgrade_authority: &Pubkey, seats: [Pubkey; 4], watch: [Pubkey; 2], origin_domain: u32, last_seq: u64) -> Instruction {
    init_of(&ID, payer, upgrade_authority, seats, watch, origin_domain, Pubkey::default(), last_seq)
}

/// A successor's `init` at program `succ`: zero seats and number, its predecessor named (the live lists and the
/// number come at the transfer).
pub fn init_successor(succ: &Pubkey, payer: &Pubkey, upgrade_authority: &Pubkey, origin_domain: u32, predecessor: &Pubkey) -> Instruction {
    init_of(succ, payer, upgrade_authority, [Pubkey::default(); 4], [Pubkey::default(); 2], origin_domain, *predecessor, 0)
}

pub fn init_of(program: &Pubkey, payer: &Pubkey, upgrade_authority: &Pubkey, seats: [Pubkey; 4], watch: [Pubkey; 2], origin_domain: u32, predecessor: Pubkey, last_seq: u64) -> Instruction {
    Instruction {
        program_id: *program,
        accounts: vec![
            AccountMeta::new(state_of(program), false),
            AccountMeta::new(*payer, true),
            AccountMeta::new_readonly(*upgrade_authority, true),
            AccountMeta::new_readonly(*program, false),
            AccountMeta::new_readonly(loader::programdata_address(program), false),
            AccountMeta::new_readonly(system_program::ID, false),
        ],
        data: data(&Ix::Init { seats, watch, origin_domain, predecessor, last_seq }),
    }
}

/// The state PDA of an admin program at another id (a successor).
pub fn state_of(program: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(&[STATE_SEED], program).0
}

pub fn transfer_authority_in(program: &Pubkey, current: &Pubkey) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: vec![
            AccountMeta::new_readonly(state(), false),
            AccountMeta::new_readonly(authority(), false),
            AccountMeta::new(loader::programdata_address(program), false),
            AccountMeta::new_readonly(*current, true),
            AccountMeta::new_readonly(loader::LOADER_ID, false),
        ],
        data: data(&Ix::TransferAuthorityIn),
    }
}

pub fn finish_setup() -> Instruction {
    let mut accounts = vec![AccountMeta::new(state(), false), AccountMeta::new_readonly(authority(), false)];
    for p in [MAILBOX, TOKENS[0], TOKENS[1], CLIENT, PAUSABLE, ID] {
        accounts.push(AccountMeta::new_readonly(loader::programdata_address(&p), false));
    }
    accounts.push(AccountMeta::new_readonly(hl::mailbox_outbox(&MAILBOX), false));
    accounts.push(AccountMeta::new_readonly(hl::mailbox_inbox(&MAILBOX), false));
    accounts.push(AccountMeta::new_readonly(hl::token_pda(&TOKENS[0]), false));
    accounts.push(AccountMeta::new_readonly(hl::token_pda(&TOKENS[1]), false));
    accounts.push(AccountMeta::new_readonly(crate::pausable::state(), false));
    accounts.push(AccountMeta::new_readonly(crate::client::state(), false));
    accounts.push(AccountMeta::new_readonly(crate::pausable::flags(), false));
    Instruction { program_id: ID, accounts, data: data(&Ix::FinishSetup) }
}

pub fn set_flag(key: &Pubkey) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(state(), false), AccountMeta::new_readonly(*key, true)], data: data(&Ix::SetFlag) }
}

pub fn clear_flag(key: &Pubkey) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(state(), false), AccountMeta::new_readonly(*key, true)], data: data(&Ix::ClearFlag) }
}

pub fn stage_upgrade(number: u64, buffer: &Pubkey, writer: &Pubkey) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: vec![
            AccountMeta::new_readonly(state(), false),
            AccountMeta::new(decision(number), false),
            AccountMeta::new(*buffer, false),
            AccountMeta::new_readonly(*writer, true),
            AccountMeta::new_readonly(authority(), false),
            AccountMeta::new_readonly(loader::LOADER_ID, false),
        ],
        data: data(&Ix::StageUpgrade { number }),
    }
}

/// `Execute` with the six fixed accounts, then the kind's own (`extra`). The pausable flags are writable when the
/// kind writes them.
pub fn execute(number: u64, deliverer: &Pubkey, executor: &Pubkey, extra: Vec<AccountMeta>, flags_writable: bool) -> Instruction {
    execute_of(&ID, number, deliverer, executor, extra, flags_writable)
}

/// `Execute` on an admin program at `program` (the launch one or a successor): its own state, decision and
/// authority PDAs; the pausable ISM's flags are the shared ones.
pub fn execute_of(program: &Pubkey, number: u64, deliverer: &Pubkey, executor: &Pubkey, extra: Vec<AccountMeta>, flags_writable: bool) -> Instruction {
    let mut accounts = vec![
        AccountMeta::new(state_of(program), false),
        AccountMeta::new(decision_of(program, number), false),
        AccountMeta::new(*deliverer, false),
        AccountMeta::new(*executor, true),
        AccountMeta::new(authority_of(program), false),
        if flags_writable { AccountMeta::new(crate::pausable::flags(), false) } else { AccountMeta::new_readonly(crate::pausable::flags(), false) },
    ];
    accounts.extend(extra);
    Instruction { program_id: *program, accounts, data: data(&Ix::Execute { number }) }
}

/// The kind's extra accounts for the plain cases (the tests spell out the others), and whether the pausable
/// flags are written.
pub fn extra_for(kind: u8, params: &Params) -> (Vec<AccountMeta>, bool) {
    match (kind, params) {
        (2, Params::Unpause(0)) => (vec![AccountMeta::new_readonly(crate::pausable::state(), false), AccountMeta::new_readonly(PAUSABLE, false)], true),
        (2, _) => (vec![], false),
        (3, Params::SetTokenIsm { token, .. }) => {
            let t = TOKENS[*token as usize];
            (vec![AccountMeta::new(hl::token_pda(&t), false), AccountMeta::new_readonly(t, false)], false)
        }
        (4, _) | (1, _) => (vec![], false),
        (5, _) => (vec![AccountMeta::new_readonly(crate::pausable::state(), false), AccountMeta::new_readonly(PAUSABLE, false)], true),
        (6, Params::EnrollRouter { token, .. }) => {
            let t = TOKENS[*token as usize];
            (vec![AccountMeta::new_readonly(system_program::ID, false), AccountMeta::new(hl::token_pda(&t), false), AccountMeta::new_readonly(t, false)], false)
        }
        (7, _) => (vec![AccountMeta::new(hl::mailbox_outbox(&MAILBOX), false), AccountMeta::new_readonly(MAILBOX, false)], false),
        (8, Params::Upgrade { program, buffer, .. }) => (
            vec![
                AccountMeta::new(loader::programdata_address(program), false),
                AccountMeta::new(*program, false),
                AccountMeta::new(*buffer, false),
                AccountMeta::new(Pubkey::default(), false), // the refund key: the test fills it
                AccountMeta::new_readonly(sysvar::rent::ID, false),
                AccountMeta::new_readonly(sysvar::clock::ID, false),
                AccountMeta::new_readonly(system_program::ID, false),
                AccountMeta::new_readonly(loader::LOADER_ID, false),
            ],
            false,
        ),
        (9, Params::Transfer { program }) => {
            let succ_authority = Pubkey::find_program_address(&[AUTHORITY_SEED], program).0;
            // the successor's authority PDA is writable (S-25): the hand-over sweeps the predecessor PDA's
            // lamports into it; the client's state (w) and program come among the kind's own accounts (M2j: the
            // fixed accounts read nothing of the client)
            let mut v = vec![
                AccountMeta::new_readonly(*program, false),
                AccountMeta::new_readonly(loader::programdata_address(program), false),
                AccountMeta::new_readonly(loader::LOADER_ID, false),
                AccountMeta::new(succ_authority, false),
            ];
            for p in [MAILBOX, TOKENS[0], TOKENS[1], CLIENT, PAUSABLE] {
                v.push(AccountMeta::new(loader::programdata_address(&p), false));
            }
            v.push(AccountMeta::new(hl::mailbox_outbox(&MAILBOX), false));
            v.push(AccountMeta::new_readonly(MAILBOX, false));
            for t in TOKENS {
                v.push(AccountMeta::new(hl::token_pda(&t), false));
                v.push(AccountMeta::new_readonly(t, false));
            }
            v.push(AccountMeta::new(crate::pausable::state(), false));
            v.push(AccountMeta::new_readonly(PAUSABLE, false));
            v.push(AccountMeta::new(crate::client::state(), false));
            v.push(AccountMeta::new_readonly(CLIENT, false));
            v.push(AccountMeta::new(state_of(program), false));
            v.push(AccountMeta::new_readonly(system_program::ID, false));
            (v, false)
        }
        (12, _) => (vec![AccountMeta::new(crate::pausable::state(), false), AccountMeta::new_readonly(PAUSABLE, false)], false),
        _ => (vec![], false),
    }
}

/// A cancel's extra accounts: the authority PDA, then pairs of (decision, deliverer).
pub fn cancel_pairs(pairs: &[(u64, Pubkey)]) -> Vec<AccountMeta> {
    let mut v = vec![AccountMeta::new_readonly(authority(), false)];
    for (n, deliverer) in pairs {
        v.push(AccountMeta::new(decision(*n), false));
        v.push(AccountMeta::new(*deliverer, false));
    }
    v
}

/// One 10 KB growth step of a program's data before a kind-8 upgrade: the loader's own permissionless
/// `ExtendProgram`, sent top-level (never by CPI, which mainnet's runtime refuses), the payer funding the rent.
pub fn extend_program(program: &Pubkey, payer: &Pubkey) -> Instruction {
    loader::extend_program(program, payer, loader::EXTEND_STEP)
}

pub fn close_cancelled(number: u64, deliverer: &Pubkey) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: vec![AccountMeta::new(state(), false), AccountMeta::new(decision(number), false), AccountMeta::new(*deliverer, false), AccountMeta::new_readonly(authority(), false)],
        data: data(&Ix::CloseCancelled { number }),
    }
}

/// A decision's body for the mailbox: kind, number, parameters.
pub fn body(kind: u8, seq: u64, p: &Params) -> Vec<u8> {
    kinds::body(kind, seq, p)
}

// ---- the mailbox -------------------------------------------------------------------------------------------------

/// Hyperlane's mailbox `Init` (borsh: local_domain, default_ism, max_protocol_fee, protocol_fee{fee, beneficiary}).
pub fn mailbox_init(payer: &Pubkey, local_domain: u32, default_ism: &Pubkey, max_fee: u64, fee: u64, beneficiary: &Pubkey) -> Instruction {
    let mut d = vec![0u8];
    d.extend_from_slice(&local_domain.to_le_bytes());
    d.extend_from_slice(default_ism.as_ref());
    d.extend_from_slice(&max_fee.to_le_bytes());
    d.extend_from_slice(&fee.to_le_bytes());
    d.extend_from_slice(beneficiary.as_ref());
    Instruction {
        program_id: MAILBOX,
        accounts: vec![AccountMeta::new(system_program::ID, false), AccountMeta::new(*payer, true), AccountMeta::new(hl::mailbox_inbox(&MAILBOX), false), AccountMeta::new(hl::mailbox_outbox(&MAILBOX), false)],
        data: d,
    }
}

/// The recipient's `HandleAccountMetas` question for a message, as the relayer simulates it (the admin program
/// reads no account of it; Hyperlane's relayer passes the question's PDA, which it ignores).
pub fn handle_account_metas(message: &[u8]) -> Instruction {
    let m = hl::Message::parse(message).unwrap();
    let h = hl::HandleInstruction { origin: m.origin, sender: m.sender, message: m.body.to_vec() };
    Instruction { program_id: ID, accounts: vec![AccountMeta::new_readonly(Pubkey::find_program_address(hl::HANDLE_ACCOUNT_METAS_SEEDS, &ID).0, false)], data: hl::encode_interface(hl::RECIPIENT_HANDLE_ACCOUNT_METAS, &h) }
}

/// The admin program's `Handle` accounts as `handle` reads them: the state, the decision, the system program, the
/// payer (signer, writable; the deliverer), then the kind's own (`extra`: for a cancel the authority PDA and the
/// pairs to close now).
pub fn handle_accounts(payer: &Pubkey, seq: u64, extra: Vec<AccountMeta>) -> Vec<AccountMeta> {
    handle_accounts_of(&ID, payer, seq, extra)
}

/// `InboxProcess` of a message to the admin program under the light-client ISM: the accounts as the mailbox
/// reads them (bridge/sol/sol.go's ProcessIx), the payer as the deliverer at the end.
pub fn process_decision(payer: &Pubkey, message: &[u8], submitter: &Pubkey, extra: Vec<AccountMeta>) -> Instruction {
    let seq = gno_light_core::decision::decision_prefix(&message[hl::MESSAGE_HEADER_LEN..]).map(|(_, s)| s).unwrap_or(0);
    process_with_handle_accounts(payer, message, submitter, handle_accounts(payer, seq, extra))
}

/// `InboxProcess` with the recipient's `Handle` accounts given whole (after the recipient program).
pub fn process_with_handle_accounts(payer: &Pubkey, message: &[u8], submitter: &Pubkey, handle: Vec<AccountMeta>) -> Instruction {
    process_to(&ID, payer, message, submitter, handle)
}

/// The `Handle` accounts of an admin program at another id (a successor), as `handle` reads them.
pub fn handle_accounts_of(program: &Pubkey, payer: &Pubkey, seq: u64, extra: Vec<AccountMeta>) -> Vec<AccountMeta> {
    let mut v = vec![AccountMeta::new(state_of(program), false), AccountMeta::new(decision_of(program, seq), false), AccountMeta::new_readonly(system_program::ID, false), AccountMeta::new(*payer, true)];
    v.extend(extra);
    v
}

/// `InboxProcess` to an admin program at `recipient` (the launch one or a successor) under the light-client ISM.
pub fn process_to(recipient: &Pubkey, payer: &Pubkey, message: &[u8], submitter: &Pubkey, handle: Vec<AccountMeta>) -> Instruction {
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
        AccountMeta::new(hl::mailbox_inbox(&MAILBOX), false),
        AccountMeta::new_readonly(hl::mailbox_process_authority(&MAILBOX, recipient), false),
        AccountMeta::new(hl::mailbox_processed_message(&MAILBOX, &id), false),
        // no accounts for the recipient's InterchainSecurityModule question
        AccountMeta::new_readonly(NOOP, false),
        AccountMeta::new_readonly(CLIENT, false),
        // the client's Verify accounts
        AccountMeta::new(crate::client::state(), false),
        AccountMeta::new_readonly(crate::client::proof_stage(submitter, &msg_hash), false),
        // the recipient and its Handle accounts
        AccountMeta::new_readonly(*recipient, false),
    ];
    accounts.extend(handle);
    Instruction { program_id: MAILBOX, accounts, data: d }
}

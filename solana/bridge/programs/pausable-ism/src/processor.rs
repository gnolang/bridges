use crate::{
    config::*,
    error::{guardian_err, PausableError},
    instruction::Instruction,
    state::{Flag, Flags, Guardians, MintLimit, State},
};
use borsh::BorshDeserialize;
use sol_util::{client_head, clock, hl, loader, pda};
use solana_program::{
    account_info::{next_account_info, AccountInfo},
    entrypoint::ProgramResult,
    instruction::{AccountMeta, Instruction as SolInstruction},
    program::{get_return_data, invoke, set_return_data},
    program_error::ProgramError,
    pubkey::Pubkey,
};

pub fn process(program_id: &Pubkey, accounts: &[AccountInfo], data: &[u8]) -> ProgramResult {
    if let Some(call) = hl::decode_interface(data)? {
        return match call {
            hl::InterfaceCall::IsmType => forward_type(program_id, accounts),
            hl::InterfaceCall::IsmVerify(v) => verify(program_id, accounts, &v),
            hl::InterfaceCall::IsmVerifyAccountMetas(v) => verify_account_metas(program_id, accounts, &v),
            _ => Err(ProgramError::InvalidInstructionData),
        };
    }
    match Instruction::try_from_slice(data).map_err(|_| ProgramError::InvalidInstructionData)? {
        Instruction::Init { owner, target, target_version, seats, watch } => init(program_id, accounts, owner, target, target_version, seats, watch),
        Instruction::SetFlag => flag(program_id, accounts, true),
        Instruction::ClearFlag => flag(program_id, accounts, false),
        Instruction::Unpause { before_slot } => unpause(program_id, accounts, before_slot),
        Instruction::SetGuardians { pairs } => set_guardians(program_id, accounts, &pairs),
        Instruction::SetLayoutVersion { target, version } => set_layout_version(program_id, accounts, target, version),
        Instruction::TransferOwnership { new_owner } => transfer_ownership(program_id, accounts, new_owner),
        Instruction::SetMintLimits { token, max, day_cap } => set_mint_limits(program_id, accounts, token, max, day_cap),
    }
}

pub fn state_address(program_id: &Pubkey) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[STATE_SEED], program_id)
}

pub fn flags_address(program_id: &Pubkey) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[FLAGS_SEED], program_id)
}

fn own(program_id: &Pubkey, account: &AccountInfo, seed: &[u8]) -> Result<(), ProgramError> {
    if account.owner != program_id {
        return Err(PausableError::NotInitialized.into());
    }
    if account.key != &Pubkey::find_program_address(&[seed], program_id).0 {
        return Err(ProgramError::InvalidSeeds);
    }
    Ok(())
}

fn load_state(program_id: &Pubkey, account: &AccountInfo) -> Result<State, ProgramError> {
    own(program_id, account, STATE_SEED)?;
    let s = State::load(&account.try_borrow_data()?)?;
    if s.version != STATE_VERSION {
        return Err(PausableError::WrongVersion.into());
    }
    Ok(s)
}

fn load_flags(program_id: &Pubkey, account: &AccountInfo) -> Result<Flags, ProgramError> {
    own(program_id, account, FLAGS_SEED)?;
    let f = Flags::load(&account.try_borrow_data()?)?;
    if f.version != STATE_VERSION {
        return Err(PausableError::WrongVersion.into());
    }
    Ok(f)
}

fn require_owner(state: &State, owner: &AccountInfo) -> Result<(), ProgramError> {
    if !owner.is_signer || owner.key != &state.owner {
        return Err(PausableError::NotOwner.into());
    }
    Ok(())
}

fn init(program_id: &Pubkey, accounts: &[AccountInfo], owner: Pubkey, target: Pubkey, target_version: u8, seats: [Pubkey; 4], watch: [Pubkey; 2]) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let flags = next_account_info(it)?;
    let payer = next_account_info(it)?;
    let authority = next_account_info(it)?;
    let program = next_account_info(it)?;
    let programdata = next_account_info(it)?;
    let system = next_account_info(it)?;
    let (state_key, state_bump) = state_address(program_id);
    let (flags_key, flags_bump) = flags_address(program_id);
    if state.key != &state_key || flags.key != &flags_key {
        return Err(ProgramError::InvalidSeeds);
    }
    if !state.data_is_empty() || state.owner == program_id || !flags.data_is_empty() {
        return Err(PausableError::AlreadyInitialized.into());
    }
    if !payer.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    loader::require_upgrade_authority(program_id, program, programdata, authority).map_err(|_| PausableError::WrongAuthority)?;
    if target_version != client_head::VERSION_1 {
        // the client's state layout is fixed for the deployment's life (v27 §13)
        return Err(PausableError::LayoutFixed.into());
    }
    let guardians = Guardians::new(seats, watch).map_err(guardian_err)?;
    let limits = [MintLimit::new(DEFAULT_MAX_PER_MESSAGE, DEFAULT_DAY_CAP); 2];
    let s = State { version: STATE_VERSION, clock_factor: CLOCK_FACTOR as u8, owner, target, target_version, limits };
    let f = Flags { version: STATE_VERSION, guardians };
    pda::create_pda_account(payer, state, system, program_id, borsh::object_length(&s).unwrap(), &[STATE_SEED, &[state_bump]])?;
    pda::create_pda_account(payer, flags, system, program_id, Flags::size(), &[FLAGS_SEED, &[flags_bump]])?;
    s.store(&mut state.try_borrow_mut_data()?)?;
    f.store(&mut flags.try_borrow_mut_data()?)
}

fn flag(program_id: &Pubkey, accounts: &[AccountInfo], set: bool) -> ProgramResult {
    let it = &mut accounts.iter();
    let flags = next_account_info(it)?;
    let key = next_account_info(it)?;
    if !key.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    let mut f = load_flags(program_id, flags)?;
    let (slot, time) = clock::now()?;
    let now = Flag { slot, time };
    let grace = clock::unpause_delay(CLOCK_FACTOR);
    if set {
        f.guardians.set_flag(key.key, now, grace).map_err(guardian_err)?;
    } else {
        f.guardians.clear_flag(key.key, now, grace).map_err(guardian_err)?;
    }
    f.store(&mut flags.try_borrow_mut_data()?)
}

fn unpause(program_id: &Pubkey, accounts: &[AccountInfo], before_slot: u64) -> ProgramResult {
    let it = &mut accounts.iter();
    let flags = next_account_info(it)?;
    let state = next_account_info(it)?;
    let owner = next_account_info(it)?;
    let s = load_state(program_id, state)?;
    require_owner(&s, owner)?;
    let mut f = load_flags(program_id, flags)?;
    let (slot, time) = clock::now()?;
    f.guardians.unpause(before_slot, Flag { slot, time }, clock::unpause_delay(CLOCK_FACTOR)).map_err(guardian_err)?;
    f.store(&mut flags.try_borrow_mut_data()?)
}

fn set_guardians(program_id: &Pubkey, accounts: &[AccountInfo], pairs: &[(u8, Pubkey)]) -> ProgramResult {
    let it = &mut accounts.iter();
    let flags = next_account_info(it)?;
    let state = next_account_info(it)?;
    let owner = next_account_info(it)?;
    let s = load_state(program_id, state)?;
    require_owner(&s, owner)?;
    let mut f = load_flags(program_id, flags)?;
    let (slot, time) = clock::now()?;
    f.guardians.replace(pairs, Flag { slot, time }, clock::unpause_delay(CLOCK_FACTOR)).map_err(guardian_err)?;
    f.store(&mut flags.try_borrow_mut_data()?)
}

/// Refused whatever its arguments (v27 §13: the client's state layout is fixed for the deployment's life, version
/// 1; a new layout is a successor pausable ISM by kind 9). The instruction keeps its place so the numbering of
/// `TransferOwnership` stands.
fn set_layout_version(_program_id: &Pubkey, _accounts: &[AccountInfo], _target: Pubkey, _version: u8) -> ProgramResult {
    Err(PausableError::LayoutFixed.into())
}

fn transfer_ownership(program_id: &Pubkey, accounts: &[AccountInfo], new_owner: Pubkey) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let owner = next_account_info(it)?;
    let mut s = load_state(program_id, state)?;
    require_owner(&s, owner)?;
    s.owner = new_owner;
    s.store(&mut state.try_borrow_mut_data()?)
}

/// Kind 12: a token's mint limits, by the owner (the admin program's authority PDA); the bucket's level stands.
fn set_mint_limits(program_id: &Pubkey, accounts: &[AccountInfo], token: u8, max: u64, day_cap: u64) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let owner = next_account_info(it)?;
    let mut s = load_state(program_id, state)?;
    require_owner(&s, owner)?;
    let l = s.limits.get_mut(token as usize).ok_or(PausableError::BadToken)?;
    l.max_per_message = max;
    l.day_cap = day_cap;
    s.store(&mut state.try_borrow_mut_data()?)
}

/// Whether an `InboxProcess` instruction's data (`1 ‖ u32 metadata length ‖ metadata ‖ u32 message length ‖
/// message`) carries exactly `message`.
pub fn inbox_process_carries(data: &[u8], message: &[u8]) -> bool {
    let u32_at = |i: usize| data.get(i..i + 4).map(|b| u32::from_le_bytes(b.try_into().unwrap()) as usize);
    if data.first() != Some(&1) {
        return false;
    }
    let Some(meta_len) = u32_at(1) else { return false };
    let after_meta = 5 + meta_len;
    let Some(len) = u32_at(after_meta) else { return false };
    let start = after_meta + 4;
    data.len() == start + len && &data[start..] == message
}

/// `Verify` runs only inside a delivery: the transaction's current top-level instruction must be the mailbox's
/// `InboxProcess` of this very message (AN5). The mailbox checks the message unprocessed before it calls the
/// ISM and marks it processed after, so a charge lands only when the delivery does; a direct call, or one wrapped
/// in another program, is refused. (The mailbox's own per-ISM process authority would do the same, but the
/// mainnet build this deployment uses does not sign with it — the suite showed it — so the sysvar it is.)
fn require_delivery(sysvar: &AccountInfo, message: &[u8]) -> ProgramResult {
    use solana_program::sysvar::instructions::{load_current_index_checked, load_instruction_at_checked};
    let index = load_current_index_checked(sysvar)? as usize;
    let ix = load_instruction_at_checked(index, sysvar)?;
    if ix.program_id != MAILBOX || !inbox_process_carries(&ix.data, message) {
        return Err(PausableError::NotTheMailbox.into());
    }
    Ok(())
}

/// `Type`: forwarded to the client, whose program account is passed as account 1 (account 0 is the state).
fn forward_type(program_id: &Pubkey, accounts: &[AccountInfo]) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let client = next_account_info(it).map_err(|_| PausableError::MissingClientProgram)?;
    let s = load_state(program_id, state)?;
    if client.key != &s.target {
        return Err(PausableError::WrongTarget.into());
    }
    invoke(&SolInstruction { program_id: s.target, accounts: vec![], data: hl::ISM_TYPE.to_vec() }, &[client.clone()])?;
    if let Some((from, data)) = get_return_data() {
        if from == s.target {
            set_return_data(&data);
        }
    }
    Ok(())
}

/// `VerifyAccountMetas`: this ISM's flag and state accounts (the state writable: the mint bucket), the client's
/// state account and the client program, the Instructions sysvar (the delivery's own top-level instruction is
/// read through it), then whatever the client answers (its program account passed as account 2, after the flags
/// and state the relayer names; without it the five are returned alone).
fn verify_account_metas(program_id: &Pubkey, accounts: &[AccountInfo], v: &hl::VerifyInstruction) -> ProgramResult {
    let it = &mut accounts.iter();
    let flags = next_account_info(it)?;
    let state = next_account_info(it)?;
    let _ = flags;
    let s = load_state(program_id, state)?;
    let client_state = client_head::state_address(&s.target).0;
    let mut metas: Vec<hl::SerializableAccountMeta> = vec![
        AccountMeta::new_readonly(flags_address(program_id).0, false).into(),
        AccountMeta::new(state_address(program_id).0, false).into(),
        AccountMeta::new(client_state, false).into(),
        AccountMeta::new_readonly(s.target, false).into(),
        AccountMeta::new_readonly(solana_program::sysvar::instructions::ID, false).into(),
    ];
    if let Ok(client) = next_account_info(it) {
        if client.key != &s.target {
            return Err(PausableError::WrongTarget.into());
        }
        invoke(&SolInstruction { program_id: s.target, accounts: vec![], data: hl::encode_interface(hl::ISM_VERIFY_ACCOUNT_METAS, v) }, &[client.clone()])?;
        if let Some((from, data)) = get_return_data() {
            if from == s.target {
                let theirs: hl::SimulationReturnData<Vec<hl::SerializableAccountMeta>> = hl::SimulationReturnData::try_from_slice(&data).map_err(|_| ProgramError::InvalidAccountData)?;
                // the client's own state account comes first in its list and is already ours to pass
                for m in theirs.return_data {
                    if m.pubkey != client_state {
                        metas.push(m);
                    }
                }
            }
        }
    }
    set_return_data(&hl::return_data(metas));
    Ok(())
}

/// `Verify`: accounts 0 flags · 1 state (w) · 2 the client's state (w) · 3 the client program · 4 the
/// Instructions sysvar (the top-level instruction must be the mailbox's `InboxProcess` of this message, so a
/// direct call is refused and the mint bucket is charged by real deliveries alone, AN5) · 5… the client's. A
/// message to one of the two tokens is a token message whose amount must be at or below the token's per-message
/// maximum and fit the token's rolling day; a message to any other recipient mints nothing of ours and passes
/// unmetered.
fn verify(program_id: &Pubkey, accounts: &[AccountInfo], v: &hl::VerifyInstruction) -> ProgramResult {
    let it = &mut accounts.iter();
    let flags = next_account_info(it)?;
    let state = next_account_info(it)?;
    let client_state = next_account_info(it)?;
    let client = next_account_info(it)?;
    let sysvar = next_account_info(it)?;
    let mut s = load_state(program_id, state)?;
    let f = load_flags(program_id, flags)?;
    if client.key != &s.target || !client.executable {
        return Err(PausableError::WrongTarget.into());
    }
    require_delivery(sysvar, &v.message)?;
    // the layout version is pinned at 1 for the deployment's life (v27 §13), never read from the state
    let head = client_head::read_checked(client_state, &s.target, client_head::VERSION_1).map_err(|_| PausableError::WrongClientState)?;
    let (_, now) = clock::now()?;
    if head.quarantine_end > now {
        return Err(PausableError::Quarantine.into());
    }
    if f.guardians.any_flag() {
        return Err(PausableError::Paused.into());
    }
    let m = hl::Message::parse(&v.message)?;
    if let Some(token) = TOKENS.iter().position(|t| t.to_bytes() == m.recipient) {
        let t = hl::TokenMessage::parse(m.body).ok_or(PausableError::BadTokenMessage)?;
        s.limits[token].charge(t.amount, now, clock::unpause_delay(CLOCK_FACTOR))?;
        s.store(&mut state.try_borrow_mut_data()?)?;
    }
    let mut infos = vec![client_state.clone()];
    let mut metas = vec![AccountMeta { pubkey: *client_state.key, is_signer: false, is_writable: client_state.is_writable }];
    for a in it {
        infos.push(a.clone());
        metas.push(AccountMeta { pubkey: *a.key, is_signer: a.is_signer, is_writable: a.is_writable });
    }
    infos.push(client.clone());
    invoke(&SolInstruction { program_id: s.target, accounts: metas, data: hl::encode_interface(hl::ISM_VERIFY, v) }, &infos)
}

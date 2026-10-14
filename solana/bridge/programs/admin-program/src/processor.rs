//! The handlers: init and the setup step, the mailbox's `Handle`, the flags, `stage upgrade`, `Execute`, and the
//! closing of cancelled decisions.

use crate::{
    config::*,
    error::{guardian_err, AdminError},
    instruction::Instruction,
    kinds::{self, Params, KIND_CANCEL, KIND_RESERVED, KIND_SET_GUARDIANS, KIND_UPGRADE},
    state::{authority_address, state_address, Decision, Hold, Range, State, MAX_HOLDS},
};
use borsh::BorshDeserialize;
use gno_light_client_ism::state::Body as ClientBody;
use gno_light_core::decision::decision_prefix;
use sol_util::{
    client_head, clock,
    guardians::{Flag, Guardians},
    hl, loader, pda, sha256,
};
use solana_program::{
    account_info::{next_account_info, AccountInfo},
    entrypoint::ProgramResult,
    instruction::{AccountMeta, Instruction as SolInstruction},
    msg,
    program::{invoke, invoke_signed, set_return_data},
    program_error::ProgramError,
    pubkey::Pubkey,
};
use solana_system_interface::{instruction as system_instruction, program as system_program};

pub fn process(program_id: &Pubkey, accounts: &[AccountInfo], data: &[u8]) -> ProgramResult {
    if let Some(call) = hl::decode_interface(data)? {
        return match call {
            hl::InterfaceCall::RecipientIsm => {
                set_return_data(&borsh::to_vec(&Some(CLIENT)).unwrap());
                Ok(())
            }
            hl::InterfaceCall::RecipientIsmAccountMetas => {
                let none: Vec<hl::SerializableAccountMeta> = vec![];
                set_return_data(&hl::return_data(none));
                Ok(())
            }
            hl::InterfaceCall::Handle(h) => handle(program_id, accounts, &h),
            hl::InterfaceCall::HandleAccountMetas(h) => handle_account_metas(program_id, &h),
            _ => Err(ProgramError::InvalidInstructionData),
        };
    }
    let ix = Box::new(Instruction::try_from_slice(data).map_err(|_| ProgramError::InvalidInstructionData)?);
    match *ix {
        Instruction::Init { seats, watch, origin_domain, predecessor, last_seq } => init(program_id, accounts, seats, watch, origin_domain, predecessor, last_seq),
        Instruction::TransferAuthorityIn => transfer_authority_in(program_id, accounts),
        Instruction::FinishSetup => finish_setup(program_id, accounts),
        Instruction::Execute { number } => execute(program_id, accounts, number),
        Instruction::StageUpgrade { number } => stage_upgrade(program_id, accounts, number),
        Instruction::SetFlag => flag(program_id, accounts, true),
        Instruction::ClearFlag => flag(program_id, accounts, false),
        Instruction::CloseCancelled { number } => close_cancelled(program_id, accounts, number),
        Instruction::SuccessorInit { origin_domain, last_seq, seats, watch, holds } => successor_init(program_id, accounts, origin_domain, last_seq, seats, watch, holds),
    }
}

pub fn load_state(program_id: &Pubkey, state: &AccountInfo) -> Result<State, ProgramError> {
    if state.owner != program_id {
        return Err(AdminError::NotInitialized.into());
    }
    if state.key != &state_address(program_id).0 {
        return Err(ProgramError::InvalidSeeds);
    }
    let s = State::load(&state.try_borrow_data()?)?;
    if s.version != STATE_VERSION {
        return Err(AdminError::WrongVersion.into());
    }
    Ok(s)
}

/// Where kind 9's `Execute` finds the successor's state among the accounts after the successor's program, its
/// programdata and the loader: the successor's authority PDA, the five programdatas, the outbox and the mailbox
/// program, each token's PDA and program, the pausable ISM's state and program, the client's state and program —
/// then it.
const SUCCESSOR_STATE_AT: usize = 1 + 5 + 2 + 2 * TOKENS.len() + 2 + 2;

/// Whether a successor's state can take kind 9's hand-over (S-18): the account is its own (the successor owns
/// its state PDA), names this program as its predecessor, has neither inherited nor retired — what
/// `successor_init` and `handle` require of it, read before any authority moves rather than after nine CPIs —
/// and (S-23) was compiled at this build's clock factor. `finish_setup` proved this program's factor equal to
/// the client's and the pausable ISM's (S-06), and a successor reaches `finish_setup` only after the hand-over,
/// so nothing else stood between a devnet-factor build deployed on mainnet by mistake (the drill's `FACTOR8=1`
/// build, its hash matching what the proposer built) and every brake of the deployment running eight times
/// faster from the transfer on: the week in 21 hours, the fortnight in 42, the grace in 3, the pausable ISM's
/// 24-hour hold judged ended after 3. Such a transfer is spent like the others, nothing moved.
fn can_inherit(program_id: &Pubkey, successor: &Pubkey, state: &AccountInfo) -> bool {
    if state.owner != successor {
        return false;
    }
    let Ok(data) = state.try_borrow_data() else { return false };
    match State::load(&data) {
        Ok(s) => s.predecessor == *program_id && s.inherited == 0 && s.successor.is_none() && s.clock_factor == CLOCK_FACTOR as u8,
        Err(_) => false,
    }
}

/// Whether this program is a successor still waiting for kind 9's hand-over (C227): its `init` named a
/// predecessor and `successor_init` has not run. Such a program runs nothing of its own (S-20): `handle` and
/// `execute` answer `NotInherited`, so no decision can pend here — outside the predecessor's hold, since the
/// seats here are still zero keys — to execute with this program's authority once it inherited. The flags are
/// dead until then by themselves (no signer holds a zero key), and a kind-8 buffer or a cancelled number cannot
/// exist without a decision; `successor_init` and `finish_setup` stay, as does `transfer_authority_in` (a
/// program's current authority moving it here is that authority's own act, writing nothing to this state).
pub fn awaits_inheritance(s: &State) -> bool {
    s.predecessor != Pubkey::default() && s.inherited == 0
}

pub fn require_authority(program_id: &Pubkey, state: &State, authority: &AccountInfo) -> Result<(), ProgramError> {
    let expect = Pubkey::create_program_address(&[AUTHORITY_SEED, &[state.authority_bump]], program_id).map_err(|_| ProgramError::InvalidSeeds)?;
    if authority.key != &expect {
        return Err(AdminError::WrongAccount.into());
    }
    Ok(())
}

fn now_flag() -> Result<Flag, ProgramError> {
    let (slot, time) = clock::now()?;
    Ok(Flag { slot, time })
}

pub fn grace() -> i64 {
    clock::unpause_delay(CLOCK_FACTOR)
}

/// The client's state account, checked (the fixed-seed PDA of the compiled client, owned by it, at layout
/// version 1), and its body: `finish_setup` reads it, nothing else does (M2j).
pub fn client_body(client_state: &AccountInfo) -> Result<(client_head::Head, Box<ClientBody>), ProgramError> {
    let head = client_head::read_checked(client_state, &CLIENT, CLIENT_STATE_VERSION)?;
    let body = ClientBody::load(&client_state.try_borrow_data()?)?;
    Ok((head, body))
}

/// The pausable ISM's hold at `now`, read from its flags account (checked by seed and owner).
pub fn pausable_hold(flags: &AccountInfo, now: i64) -> Result<Option<Flag>, ProgramError> {
    if flags.key != &Pubkey::find_program_address(&[pausable_ism::config::FLAGS_SEED], &PAUSABLE).0 || flags.owner != &PAUSABLE {
        return Err(AdminError::WrongAccount.into());
    }
    let f = pausable_ism::state::Flags::load(&flags.try_borrow_data()?)?;
    Ok(f.guardians.hold(now, grace()))
}

/// This program's own hold at `now`.
pub fn admin_hold(state: &State, now: i64) -> Option<Flag> {
    state.guardians.hold(now, grace())
}

/// The one-shot init. `last_seq` is the number the sequence starts at (AT3): 0 for a chain's first deployment,
/// r/hlgov's `Last()` for a fresh deployment under the same r/hlgov — `handle` then takes `last_seq + 1` first,
/// which is what r/hlgov dispatches next. A successor gives 0: its number is the predecessor's, written by
/// `successor_init`.
#[inline(never)]
fn init(program_id: &Pubkey, accounts: &[AccountInfo], seats: [Pubkey; 4], watch: [Pubkey; 2], origin_domain: u32, predecessor: Pubkey, last_seq: u64) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let payer = next_account_info(it)?;
    let authority = next_account_info(it)?;
    let program = next_account_info(it)?;
    let programdata = next_account_info(it)?;
    let system = next_account_info(it)?;
    let (state_key, bump) = state_address(program_id);
    if state.key != &state_key {
        return Err(ProgramError::InvalidSeeds);
    }
    if !state.data_is_empty() || state.owner == program_id {
        return Err(AdminError::AlreadyInitialized.into());
    }
    if !payer.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    loader::require_upgrade_authority(program_id, program, programdata, authority).map_err(|_| AdminError::WrongAuthority)?;
    let guardians = if predecessor == Pubkey::default() {
        Guardians::new(seats, watch).map_err(guardian_err)?
    } else {
        // a successor's seats and watch slots are the predecessor's live ones, by successor_init (C227): none here
        if seats != [Pubkey::default(); 4] || watch != [Pubkey::default(); 2] || predecessor == *program_id {
            return Err(AdminError::BadSeat.into());
        }
        if last_seq != 0 {
            // and so is its number: a successor takes none before successor_init wrote its predecessor's
            return Err(AdminError::OutOfOrder.into());
        }
        Guardians::default()
    };
    let (_, authority_bump) = authority_address(program_id);
    let s = State {
        version: STATE_VERSION,
        clock_factor: CLOCK_FACTOR as u8,
        setup_done: 0,
        origin_domain,
        last_seq,
        guardians,
        successor: None,
        authority_bump,
        cancelled: Vec::new(),
        holds: Vec::new(),
        pending: 0,
        predecessor,
        inherited: 0,
    };
    pda::create_pda_account(payer, state, system, program_id, State::init_size(), &[STATE_SEED, &[bump]])?;
    s.store(&mut state.try_borrow_mut_data()?)
}

/// Kind 9's hand-over into this successor (C227): the predecessor's authority PDA writes the live number, seats,
/// watch slots and hold history, once. The account was made for eight holds, so nothing grows.
#[inline(never)]
fn successor_init(program_id: &Pubkey, accounts: &[AccountInfo], origin_domain: u32, last_seq: u64, seats: [Pubkey; 4], watch: [Pubkey; 2], holds: Vec<Hold>) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let predecessor_authority = next_account_info(it)?;
    let mut s = load_state(program_id, state)?;
    if !awaits_inheritance(&s) {
        return Err(AdminError::NotInherited.into());
    }
    if predecessor_authority.key != &Pubkey::find_program_address(&[AUTHORITY_SEED], &s.predecessor).0 || !predecessor_authority.is_signer {
        return Err(AdminError::WrongAuthority.into());
    }
    if holds.len() > MAX_HOLDS {
        return Err(AdminError::WrongDecision.into());
    }
    s.origin_domain = origin_domain;
    s.last_seq = last_seq;
    s.guardians = Guardians::new(seats, watch).map_err(guardian_err)?;
    s.holds = holds;
    s.inherited = 1;
    s.store(&mut state.try_borrow_mut_data()?)
}

/// The setup step: a program's upgrade authority moves to the authority PDA; the current authority and the PDA
/// both sign the loader's `SetAuthorityChecked`.
#[inline(never)]
fn transfer_authority_in(program_id: &Pubkey, accounts: &[AccountInfo]) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let authority = next_account_info(it)?;
    let programdata = next_account_info(it)?;
    let current = next_account_info(it)?;
    let loader_program = next_account_info(it)?;
    let s = load_state(program_id, state)?;
    if s.setup_done != 0 {
        return Err(AdminError::SetupDone.into());
    }
    require_authority(program_id, &s, authority)?;
    if programdata.key == &loader::programdata_address(program_id) {
        // the admin program's own upgrade authority is burned in the setup step (§5.8), never moved to its own
        // PDA: no instruction could move it again, and finish_setup could never flip (S-05)
        msg!("the admin program's own programdata: its authority is burned, never transferred in");
        return Err(AdminError::WrongAccount.into());
    }
    if !current.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    let ix = loader::set_authority_checked(programdata.key, current.key, authority.key);
    invoke_signed(&ix, &[programdata.clone(), current.clone(), authority.clone(), loader_program.clone()], &[&[AUTHORITY_SEED, &[s.authority_bump]]])
}

/// Permissionless and self-checking: every upgrade authority and owner is the admin program or none, and the
/// settings are the launch ones. It records nothing (M2j).
#[inline(never)]
fn finish_setup(program_id: &Pubkey, accounts: &[AccountInfo]) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let authority = next_account_info(it)?;
    let mut s = load_state(program_id, state)?;
    if s.setup_done != 0 {
        return Err(AdminError::SetupDone.into());
    }
    require_authority(program_id, &s, authority)?;
    let pda_key = *authority.key;
    let programs = [MAILBOX, TOKENS[0], TOKENS[1], CLIENT, PAUSABLE];
    for p in programs.iter() {
        let pd = next_account_info(it)?;
        if pd.key != &loader::programdata_address(p) || pd.owner != &loader::LOADER_ID {
            return Err(AdminError::WrongAccount.into());
        }
        let (_, a) = loader::parse_programdata(&pd.try_borrow_data()?)?;
        if a != Some(pda_key) {
            msg!("upgrade authority of {} is not the admin PDA", p);
            return Err(AdminError::SetupIncomplete.into());
        }
    }
    let own = next_account_info(it)?;
    if own.key != &loader::programdata_address(program_id) || own.owner != &loader::LOADER_ID {
        return Err(AdminError::WrongAccount.into());
    }
    let (_, a) = loader::parse_programdata(&own.try_borrow_data()?)?;
    if a.is_some() {
        msg!("the admin program's own upgrade authority is not burned");
        return Err(AdminError::SetupIncomplete.into());
    }
    let outbox = next_account_info(it)?;
    if outbox.key != &hl::mailbox_outbox(&MAILBOX) || outbox.owner != &MAILBOX {
        return Err(AdminError::WrongAccount.into());
    }
    let ob = hl::Outbox::parse(&outbox.try_borrow_data()?)?;
    if ob.owner != Some(pda_key) || ob.max_protocol_fee != MAX_PROTOCOL_FEE {
        msg!("the mailbox's owner or max_protocol_fee");
        return Err(AdminError::SetupIncomplete.into());
    }
    let inbox = next_account_info(it)?;
    if inbox.key != &hl::mailbox_inbox(&MAILBOX) || inbox.owner != &MAILBOX {
        return Err(AdminError::WrongAccount.into());
    }
    if hl::Inbox::parse(&inbox.try_borrow_data()?)?.default_ism != PAUSABLE {
        msg!("the mailbox's default ISM is not the pausable ISM");
        return Err(AdminError::SetupIncomplete.into());
    }
    for t in TOKENS.iter() {
        let acc = next_account_info(it)?;
        if acc.key != &hl::token_pda(t) || acc.owner != t {
            return Err(AdminError::WrongAccount.into());
        }
        let data = acc.try_borrow_data()?;
        if hl::token_owner(&data)? != Some(pda_key) || hl::token_ism(&data)? != Some(PAUSABLE) {
            msg!("a token's owner or ISM pointer");
            return Err(AdminError::SetupIncomplete.into());
        }
    }
    let pstate = next_account_info(it)?;
    if pstate.key != &Pubkey::find_program_address(&[pausable_ism::config::STATE_SEED], &PAUSABLE).0 || pstate.owner != &PAUSABLE {
        return Err(AdminError::WrongAccount.into());
    }
    let ps = pausable_ism::state::State::load(&pstate.try_borrow_data()?)?;
    if ps.owner != pda_key || ps.target != CLIENT {
        msg!("the pausable ISM's owner or target");
        return Err(AdminError::SetupIncomplete.into());
    }
    let cstate = next_account_info(it)?;
    let (chead, cbody) = client_body(cstate)?;
    if cbody.admin != pda_key {
        msg!("the client's admin");
        return Err(AdminError::SetupIncomplete.into());
    }
    // what the three programs must agree on (S-06): the guardian lists (a kind 5 passing here and failing the
    // pausable ISM's CPI would pend for good), the clock factors (a devnet build beside mainnet ones would run
    // its clocks eight times faster unseen), the origin domain
    let pflags = next_account_info(it)?;
    if pflags.key != &Pubkey::find_program_address(&[pausable_ism::config::FLAGS_SEED], &PAUSABLE).0 || pflags.owner != &PAUSABLE {
        return Err(AdminError::WrongAccount.into());
    }
    let pf = pausable_ism::state::Flags::load(&pflags.try_borrow_data()?)?;
    if pf.guardians.keys != s.guardians.keys {
        msg!("the pausable ISM's guardian list is not this program's");
        return Err(AdminError::SetupIncomplete.into());
    }
    if chead.clock_factor != s.clock_factor || ps.clock_factor != s.clock_factor {
        msg!("a clock factor differs: client {}, pausable ISM {}, this program {}", chead.clock_factor, ps.clock_factor, s.clock_factor);
        return Err(AdminError::SetupIncomplete.into());
    }
    if cbody.origin_domain != s.origin_domain {
        msg!("the client's origin domain {} is not this program's {}", cbody.origin_domain, s.origin_domain);
        return Err(AdminError::SetupIncomplete.into());
    }
    s.setup_done = 1;
    s.store(&mut state.try_borrow_mut_data()?)
}

/// The mailbox's `Handle`: accounts 0 the process authority (signer) · 1 state (w) · 2 the decision (w) · 3 the
/// system program · 4 the payer (signer, w; the deliverer, appended by the relayer); a cancel adds 5 the authority
/// PDA and pairs of a decision (w) and its deliverer (w) to close now.
#[inline(never)]
fn handle(program_id: &Pubkey, accounts: &[AccountInfo], h: &hl::HandleInstruction) -> ProgramResult {
    let it = &mut accounts.iter();
    let process_authority = next_account_info(it)?;
    let state = next_account_info(it)?;
    let decision = next_account_info(it)?;
    let system = next_account_info(it)?;
    let payer = next_account_info(it)?;
    if process_authority.key != &hl::mailbox_process_authority(&MAILBOX, program_id) || !process_authority.is_signer {
        return Err(AdminError::NotTheMailbox.into());
    }
    let mut s = load_state(program_id, state)?;
    if awaits_inheritance(&s) {
        // a successor takes no number before successor_init wrote its predecessor's (C227): the message stays
        // deliverable
        return Err(AdminError::NotInherited.into());
    }
    if s.successor.is_some() {
        // retired by kind 9 (S-12): the successor holds every authority and inherited the sequence, so a decision
        // still dispatched here — r/hlgov's recipient switch is a separate act (§5.3) — would take, dead, the
        // number the successor expects next; nothing is recorded and the message stays deliverable (the relayer
        // parks it, hlwatch shows it)
        return Err(AdminError::Retired.into());
    }
    if h.origin != s.origin_domain {
        return Err(AdminError::WrongOrigin.into());
    }
    if h.sender != HLGOV_SENDER {
        return Err(AdminError::WrongSender.into());
    }
    let (kind, seq) = decision_prefix(&h.message).ok_or(AdminError::ShortBody)?;
    if seq <= s.last_seq {
        msg!("decision {} already recorded: ignored", seq);
        return Ok(());
    }
    // a checked step: at the sequence's end a decision is ignored, never a panic that would leave the mailbox
    // retrying it for good (AN8)
    let Some(next) = s.last_seq.checked_add(1) else {
        msg!("the sequence is at its end: decision {} ignored", seq);
        return Ok(());
    };
    if seq != next {
        return Err(AdminError::OutOfOrder.into());
    }
    s.last_seq = seq;
    if !kinds::known_kind(kind) || h.message.len() - 9 > MAX_PARAMS {
        msg!("decision {} of kind {}: unknown, recorded and spent", seq, kind);
        return s.store(&mut state.try_borrow_mut_data()?);
    }
    if kind == KIND_CANCEL {
        // executes in the transaction that records it
        match kinds::parse(kind, &h.message[9..]) {
            Some(Params::Cancel { ranges, .. }) => {
                let authority = next_account_info(it).ok();
                if let Some(a) = authority {
                    require_authority(program_id, &s, a)?;
                }
                apply_cancel(program_id, &mut s, &ranges, authority, it)?;
            }
            _ => msg!("cancel {}: does not parse, spent", seq),
        }
        return s.store(&mut state.try_borrow_mut_data()?);
    }
    if !payer.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    let (dkey, bump) = Decision::address(program_id, seq);
    if decision.key != &dkey {
        return Err(ProgramError::InvalidSeeds);
    }
    let now = now_flag()?;
    let d = Decision { version: STATE_VERSION, number: seq, kind, params: h.message[9..].to_vec(), delivered_slot: now.slot, delivered_time: now.time, deliverer: *payer.key, refund_key: None };
    pda::create_pda_account(payer, decision, system, program_id, d.size(), &[DECISION_SEED, &seq.to_be_bytes(), &[bump]])?;
    d.store(&mut decision.try_borrow_mut_data()?)?;
    s.pending = s.pending.saturating_add(1);
    s.store(&mut state.try_borrow_mut_data()?)
}

/// `HandleAccountMetas`: the accounts `handle` reads before the deliverer, in its order — the state, the decision
/// PDA, the system program — for every kind. The relayer appends its payer (signer, writable) as the deliverer,
/// and for a cancel the authority PDA and then the pairs it wants closed now, exactly as `handle` reads them (a
/// cancel's authority PDA answered here, before the payer, once put the payer in the PDA's place: WrongAccount on
/// every cancel delivered from a simulated answer).
fn handle_account_metas(program_id: &Pubkey, h: &hl::HandleInstruction) -> ProgramResult {
    let (_, seq) = decision_prefix(&h.message).ok_or(AdminError::ShortBody)?;
    let metas: Vec<hl::SerializableAccountMeta> = vec![
        AccountMeta::new(state_address(program_id).0, false).into(),
        AccountMeta::new(Decision::address(program_id, seq).0, false).into(),
        AccountMeta::new_readonly(system_program::ID, false).into(),
    ];
    set_return_data(&hl::return_data(metas));
    Ok(())
}

/// Cancels the ranges: every passed pair (a decision of a number inside them, its deliverer) is closed now, a
/// staged kind-8 buffer returned to its refund key (which needs the authority PDA); a range the closed pairs do
/// not cover whole is then stored, so a number not passed is dead too (`Execute` and `CloseCancelled` close it).
/// A pair naming a closed or unrecorded number is a no-op. A full store merges rather than refuses (AN7).
fn apply_cancel<'a, 'b>(program_id: &Pubkey, s: &mut State, ranges: &[(u64, u64)], authority: Option<&AccountInfo<'b>>, it: &mut core::slice::Iter<'a, AccountInfo<'b>>) -> ProgramResult {
    let mut closed: Vec<u64> = Vec::new();
    while let Some(target) = it.next() {
        let deliverer = next_account_info(it)?;
        if target.owner != program_id || target.data_is_empty() {
            continue; // already closed, or never recorded: a no-op
        }
        let td = Decision::load(&target.try_borrow_data()?)?;
        if target.key != &Decision::address(program_id, td.number).0 {
            return Err(AdminError::WrongDecision.into());
        }
        if !ranges.iter().any(|r| r.0 <= td.number && td.number <= r.1) {
            return Err(AdminError::WrongDecision.into());
        }
        if td.kind == KIND_UPGRADE && td.refund_key.is_some() {
            let buffer = next_account_info(it)?;
            let refund = next_account_info(it)?;
            let loader_program = next_account_info(it)?;
            let authority = authority.ok_or(AdminError::WrongAccount)?;
            return_buffer(s, &td, buffer, refund, authority, loader_program)?;
        }
        close_decision(s, target, deliverer, &td)?;
        closed.push(td.number);
    }
    for r in ranges {
        // a range cancels the decisions pending when it executes, never numbers recorded later (v25 §6.4)
        let to = r.1.min(s.last_seq);
        if r.0 > to {
            continue;
        }
        // nothing to store when the pairs closed now cover the clamped range whole (S-04)
        let covered = closed.iter().filter(|n| r.0 <= **n && **n <= to).count() as u64;
        if (to - r.0).checked_add(1) == Some(covered) {
            continue;
        }
        s.add_cancelled(Range { from: r.0, to });
    }
    Ok(())
}

/// Notes in the hold history a hold that a change of the guardians ended (AN4): by a dip of the grace's length,
/// at the moment the dip reached it; by a replacement's own clear, now. `before` is the guardians before the
/// change.
fn note_hold_end(s: &mut State, before: &Guardians, now: i64) {
    let Some(start) = before.hold_since else { return };
    if s.guardians.hold_since == Some(start) {
        return;
    }
    let grace = grace();
    let end = match before.dip_since {
        Some(d) if now.saturating_sub(d.time) >= grace => d.time.saturating_add(grace),
        _ => now,
    };
    s.push_hold(Hold { start: start.time, end });
}

#[inline(never)]
fn flag(program_id: &Pubkey, accounts: &[AccountInfo], set: bool) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let key = next_account_info(it)?;
    if !key.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    let mut s = load_state(program_id, state)?;
    let now = now_flag()?;
    let before = s.guardians.clone();
    if set {
        s.guardians.set_flag(key.key, now, grace()).map_err(guardian_err)?;
    } else {
        s.guardians.clear_flag(key.key, now, grace()).map_err(guardian_err)?;
    }
    note_hold_end(&mut s, &before, now.time);
    s.store(&mut state.try_borrow_mut_data()?)
}

fn load_decision(program_id: &Pubkey, account: &AccountInfo, number: u64) -> Result<Decision, ProgramError> {
    if account.owner != program_id || account.key != &Decision::address(program_id, number).0 {
        return Err(AdminError::WrongDecision.into());
    }
    let d = Decision::load(&account.try_borrow_data()?)?;
    if d.number != number {
        return Err(AdminError::WrongDecision.into());
    }
    Ok(d)
}

/// The buffer writer hands a kind-8 decision's buffer to the authority PDA and is recorded as its refund key.
#[inline(never)]
fn stage_upgrade(program_id: &Pubkey, accounts: &[AccountInfo], number: u64) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let decision = next_account_info(it)?;
    let buffer = next_account_info(it)?;
    let writer = next_account_info(it)?;
    let authority = next_account_info(it)?;
    let loader_program = next_account_info(it)?;
    let s = load_state(program_id, state)?;
    require_authority(program_id, &s, authority)?;
    if s.successor.is_some() {
        // retired by kind 9 (S-13): the successor holds every upgrade authority, so no upgrade can run from here
        // again, and a buffer handed to this PDA now would only wait for CloseCancelled to return it
        return Err(AdminError::Retired.into());
    }
    let mut d = load_decision(program_id, decision, number)?;
    if d.kind != KIND_UPGRADE || d.refund_key.is_some() {
        return Err(AdminError::WrongDecision.into());
    }
    let Some(Params::Upgrade { program, buffer: named, .. }) = kinds::parse(d.kind, &d.params) else { return Err(AdminError::WrongDecision.into()) };
    if program == *program_id {
        // the admin program is immutable (§13's kind 8 never names it): no buffer is staged for such a decision,
        // which Execute spends
        return Err(AdminError::WrongDecision.into());
    }
    if buffer.key != &named || buffer.owner != &loader::LOADER_ID {
        return Err(AdminError::WrongBuffer.into());
    }
    if loader::parse_buffer(&buffer.try_borrow_data()?)? != Some(*writer.key) || !writer.is_signer {
        return Err(AdminError::WrongBuffer.into());
    }
    invoke(&loader::set_authority(buffer.key, writer.key, Some(authority.key)), &[buffer.clone(), writer.clone(), authority.clone(), loader_program.clone()])?;
    d.refund_key = Some(*writer.key);
    d.store(&mut decision.try_borrow_mut_data()?)
}

/// Closes a decision to its deliverer, counting it off the pending count; at zero the stored ranges are pruned
/// (S-04). The caller writes the state.
fn close_decision(s: &mut State, decision: &AccountInfo, deliverer: &AccountInfo, d: &Decision) -> ProgramResult {
    if deliverer.key != &d.deliverer {
        return Err(AdminError::WrongDeliverer.into());
    }
    pda::close_account(decision, deliverer)?;
    s.pending = s.pending.saturating_sub(1);
    if s.pending == 0 {
        s.prune();
    }
    Ok(())
}

/// Closes a decision and writes the state.
fn close_and_store(s: &mut State, state: &AccountInfo, decision: &AccountInfo, deliverer: &AccountInfo, d: &Decision) -> ProgramResult {
    close_decision(s, decision, deliverer, d)?;
    s.store(&mut state.try_borrow_mut_data()?)
}

/// A staged kind-8 buffer goes back to its refund key when the decision ends without the upgrade.
fn return_buffer<'a>(s: &State, d: &Decision, buffer: &AccountInfo<'a>, refund: &AccountInfo<'a>, authority: &AccountInfo<'a>, loader_program: &AccountInfo<'a>) -> ProgramResult {
    let Some(refund_key) = d.refund_key else { return Ok(()) };
    let Some(Params::Upgrade { buffer: named, .. }) = kinds::parse(d.kind, &d.params) else { return Ok(()) };
    if buffer.key != &named || refund.key != &refund_key {
        return Err(AdminError::WrongBuffer.into());
    }
    invoke_signed(&loader::close_buffer(buffer.key, refund.key, authority.key), &[buffer.clone(), refund.clone(), authority.clone(), loader_program.clone()], &[&[AUTHORITY_SEED, &[s.authority_bump]]])
}

/// Closes a cancelled decision (one a cancel named without closing): its rent to its deliverer. Once kind 9
/// retired this program, any pending decision (S-13).
#[inline(never)]
fn close_cancelled(program_id: &Pubkey, accounts: &[AccountInfo], number: u64) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let decision = next_account_info(it)?;
    let deliverer = next_account_info(it)?;
    let authority = next_account_info(it)?;
    let mut s = load_state(program_id, state)?;
    require_authority(program_id, &s, authority)?;
    // once kind 9 retired this program (S-13) every pending decision is dead: Execute can run none of them (the
    // successor holds every authority, so a kind 8 fails at the loader with its hash matching and nothing
    // returns its buffer), and no cancel reaches a retired program (Retired); so any of them closes here — the
    // buffer to its refund key, the rent to the deliverer, as a cancelled one does
    if !s.is_cancelled(number) && s.successor.is_none() {
        return Err(AdminError::WrongDecision.into());
    }
    let d = load_decision(program_id, decision, number)?;
    if d.kind == KIND_UPGRADE && d.refund_key.is_some() {
        let buffer = next_account_info(it)?;
        let refund = next_account_info(it)?;
        let loader_program = next_account_info(it)?;
        return_buffer(&s, &d, buffer, refund, authority, loader_program)?;
    }
    close_and_store(&mut s, state, decision, deliverer, &d)
}

/// Anyone executes a matured decision (§6.5): the clocks by kind, a lone flag or a hold deferring the 7-day group
/// (kind 5 excepted: through a lone flag, and through a hold on the later of its own 7th day and the 21st day of
/// the earliest hold overlapping its pending life — from the hold history, no write and no earlier attempt
/// needed, binding while a hold stands); a cancelled one is closed instead. Nothing of the client is read (M2j).
#[inline(never)]
fn execute(program_id: &Pubkey, accounts: &[AccountInfo], number: u64) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let decision = next_account_info(it)?;
    let deliverer = next_account_info(it)?;
    let executor = next_account_info(it)?;
    let authority = next_account_info(it)?;
    let pausable_flags = next_account_info(it)?;
    let mut s = load_state(program_id, state)?;
    if awaits_inheritance(&s) {
        // a successor executes nothing before successor_init wrote its predecessor's numbers (S-20): no decision
        // can pend here (handle refuses), and none may run with this program's authority
        return Err(AdminError::NotInherited.into());
    }
    require_authority(program_id, &s, authority)?;
    if !executor.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    let d = load_decision(program_id, decision, number)?;
    let now = now_flag()?;
    let admin_hold = admin_hold(&s, now.time);

    if s.is_cancelled(number) {
        msg!("decision {}: cancelled", number);
        if d.kind == KIND_UPGRADE && d.refund_key.is_some() {
            let buffer = next_account_info(it)?;
            let refund = next_account_info(it)?;
            let loader_program = next_account_info(it)?;
            return_buffer(&s, &d, buffer, refund, authority, loader_program)?;
        }
        return close_and_store(&mut s, state, decision, deliverer, &d);
    }

    // the clock, and what defers (§6.5): a lone flag or a hold defers the 7-day group, kind 5 excepted — a
    // replacement of guardians lands through a lone flag, and through a hold on the later of its own 7th day
    // and the 21st day of the earliest hold overlapping [its delivery, now] (the hold history, AN4): GovDAO's own
    // cancel of a forged replacement, by a decision through the client, has that week to land; so no later hold
    // — two keys re-flagging at the grace's last second to begin a new one — raises it, and no Execute need have
    // been attempted under the first. The floor binds only while a hold stands: with none standing the
    // replacement lands on its own 7th day
    let mut mature_at = d.delivered_time.saturating_add(kinds::delay(d.kind));
    if d.kind == KIND_SET_GUARDIANS {
        if let Some(h) = admin_hold {
            let floor = s.replacement_floor(d.delivered_time, h.time, 21 * clock::DAY / CLOCK_FACTOR);
            mature_at = mature_at.max(floor);
        }
    } else if kinds::seven_day(d.kind) && (admin_hold.is_some() || s.guardians.any_flag()) {
        // a hold defers the 7-day group, read from holdSince (a dip changes nothing); so does a lone flag
        return Err(AdminError::Deferred.into());
    }
    if now.time < mature_at {
        return Err(AdminError::NotMature.into());
    }
    if d.kind == KIND_RESERVED {
        // v27 §13: the client's state layout, head and body, is fixed for the deployment's life (version 1); a new
        // layout is a successor admin program and pausable ISM by kind 9
        msg!("decision {}: kind 4 is reserved, spent", number);
        return close_and_store(&mut s, state, decision, deliverer, &d);
    }
    let Some(params) = kinds::parse(d.kind, &d.params) else {
        msg!("decision {}: parameters do not parse, spent", number);
        return close_and_store(&mut s, state, decision, deliverer, &d);
    };
    if matches!(&params, Params::Upgrade { program, .. } if program == program_id) {
        // the admin program is immutable (§6.5; §13's kind 8 never names it): spent, and since stage_upgrade
        // refuses its buffer there is none to return
        msg!("decision {}: an upgrade of the admin program itself, spent", number);
        return close_and_store(&mut s, state, decision, deliverer, &d);
    }
    if matches!(&params, Params::Transfer { program } if program == program_id) {
        // a successor is another program (§13's kind 9): this one's own programdata has no authority — burned in
        // the setup step — so the immutability check alone would pass it, and the transfer would re-point every
        // authority to this same PDA and then die on its own SuccessorInit, pending for good (S-15); spent, no
        // account beyond the fixed six read
        msg!("decision {}: a transfer to the admin program itself, spent", number);
        return close_and_store(&mut s, state, decision, deliverer, &d);
    }
    let seeds: &[&[u8]] = &[AUTHORITY_SEED, &[s.authority_bump]];
    let spent = match params {
        Params::Cancel { .. } => {
            // a cancel executes as it is recorded; a pending one is a recorded form that never parsed there
            msg!("decision {}: a cancel pending is spent", number);
            true
        }
        Params::Unpause(object) => {
            if object == 0 {
                let pstate = next_account_info(it)?;
                let pprogram = next_account_info(it)?;
                if pausable_hold(pausable_flags, now.time)?.is_some() {
                    msg!("unpause: a hold stands on the pausable ISM, spent");
                    true
                } else {
                    let ix = SolInstruction {
                        program_id: PAUSABLE,
                        accounts: vec![AccountMeta::new(*pausable_flags.key, false), AccountMeta::new_readonly(*pstate.key, false), AccountMeta::new_readonly(*authority.key, true)],
                        data: borsh::to_vec(&pausable_ism::instruction::Instruction::Unpause { before_slot: d.delivered_slot }).unwrap(),
                    };
                    invoke_signed(&ix, &[pausable_flags.clone(), pstate.clone(), authority.clone(), pprogram.clone()], &[seeds])?;
                    false
                }
            } else if admin_hold.is_some() {
                msg!("unpause: a hold stands on the admin program, spent");
                true
            } else {
                let before = s.guardians.clone();
                s.guardians.unpause(d.delivered_slot, now, grace()).map_err(guardian_err)?;
                note_hold_end(&mut s, &before, now.time);
                false
            }
        }
        Params::SetTokenIsm { token, ism } => match (TOKENS.get(token as usize), ism == PAUSABLE || ism == Pubkey::default()) {
            (Some(program), true) => {
                let token_pda = next_account_info(it)?;
                let tprogram = next_account_info(it)?;
                let new = if ism == Pubkey::default() { None } else { Some(ism) };
                invoke_signed(&hl::token_set_ism(program, authority.key, new), &[token_pda.clone(), authority.clone(), tprogram.clone()], &[seeds])?;
                false
            }
            _ => true,
        },
        Params::SetGuardians(pairs) => {
            let mut g = s.guardians.clone();
            if kinds::names_current_key(&s, &pairs) || g.replace(&pairs, now, grace()).is_err() {
                true
            } else {
                let pstate = next_account_info(it)?;
                let pprogram = next_account_info(it)?;
                let ix = SolInstruction {
                    program_id: PAUSABLE,
                    accounts: vec![AccountMeta::new(*pausable_flags.key, false), AccountMeta::new_readonly(*pstate.key, false), AccountMeta::new_readonly(*authority.key, true)],
                    data: borsh::to_vec(&pausable_ism::instruction::Instruction::SetGuardians { pairs }).unwrap(),
                };
                invoke_signed(&ix, &[pausable_flags.clone(), pstate.clone(), authority.clone(), pprogram.clone()], &[seeds])?;
                let before = core::mem::replace(&mut s.guardians, g);
                note_hold_end(&mut s, &before, now.time);
                false
            }
        }
        Params::EnrollRouter { token, domain, router } => match TOKENS.get(token as usize) {
            Some(program) => {
                let system = next_account_info(it)?;
                let token_pda = next_account_info(it)?;
                let tprogram = next_account_info(it)?;
                let r = if router == [0; 32] { None } else { Some(router) };
                invoke_signed(&hl::token_enroll_remote_router(program, authority.key, domain, r), &[system.clone(), token_pda.clone(), authority.clone(), tprogram.clone()], &[seeds])?;
                false
            }
            None => true,
        },
        Params::SetFee { lamports, beneficiary } => {
            if lamports > MAX_PROTOCOL_FEE {
                true
            } else {
                let outbox = next_account_info(it)?;
                let mprogram = next_account_info(it)?;
                invoke_signed(&hl::mailbox_set_protocol_fee(&MAILBOX, authority.key, lamports, &beneficiary), &[outbox.clone(), authority.clone(), mprogram.clone()], &[seeds])?;
                false
            }
        }
        Params::Upgrade { program, buffer: named, hash } => {
            let programdata = next_account_info(it)?;
            let program_acc = next_account_info(it)?;
            let buffer = next_account_info(it)?;
            let refund = next_account_info(it)?;
            let rent = next_account_info(it)?;
            let clock_acc = next_account_info(it)?;
            let system = next_account_info(it)?;
            let loader_program = next_account_info(it)?;
            let Some(refund_key) = d.refund_key else { return Err(AdminError::NotStaged.into()) };
            if program_acc.key != &program || programdata.key != &loader::programdata_address(&program) || buffer.key != &named || refund.key != &refund_key {
                return Err(AdminError::WrongAccount.into());
            }
            let (ok, elf_len) = {
                let data = buffer.try_borrow_data()?;
                let elf = loader::buffer_elf(&data)?;
                (sha256(&[elf]) == hash, elf.len())
            };
            if !ok {
                msg!("upgrade: the buffer's ELF hash is not the decision's, spent");
                return_buffer(&s, &d, buffer, refund, authority, loader_program)?;
                true
            } else {
                let have = programdata.data_len().saturating_sub(loader::PROGRAMDATA_HEADER);
                if elf_len > have {
                    // the program data grows first, by the loader's own permissionless `ExtendProgram` sent
                    // top-level by the executor — one extension of the whole excess, at or above the loader's
                    // 10,240-byte minimum (SIMD-0431), the loader taking one extension of a program a block;
                    // mainnet's runtime lets no program CPI an extend (`check_authorized_program` admits only
                    // Upgrade, SetAuthority and Close, the audit's F1); pending until it has
                    msg!("upgrade: the program data needs {} more bytes first", elf_len - have);
                    return Err(AdminError::NeedsExtend.into());
                }
                let _ = system;
                invoke_signed(
                    &loader::upgrade(&program, buffer.key, authority.key, refund.key),
                    &[programdata.clone(), program_acc.clone(), buffer.clone(), refund.clone(), rent.clone(), clock_acc.clone(), authority.clone(), loader_program.clone()],
                    &[seeds],
                )?;
                false
            }
        }
        Params::Transfer { program } => {
            let succ_program = next_account_info(it)?;
            let succ_programdata = next_account_info(it)?;
            let loader_program = next_account_info(it)?;
            if succ_program.key != &program || !succ_program.executable || succ_program.owner != &loader::LOADER_ID {
                return Err(AdminError::WrongAccount.into());
            }
            let pd_key = loader::parse_program(&succ_program.try_borrow_data()?)?;
            if succ_programdata.key != &pd_key {
                return Err(AdminError::WrongAccount.into());
            }
            let (_, a) = loader::parse_programdata(&succ_programdata.try_borrow_data()?)?;
            // the successor's state, at its place after the client program (the Go side builds this order), read
            // before any authority moves (S-18): an immutable program whose state can never take the hand-over —
            // never initialised (its init needs the upgrade authority it no longer has), initialised naming
            // another predecessor, inherited already, or retired itself — passed the immutability check, and the
            // execution re-pointed nine authorities to die on its SuccessorInit, reverting whole and pending
            // until a cancel; the account is checked by key, so an executor's choice of account spends nothing
            let succ_state = it.as_slice().get(SUCCESSOR_STATE_AT).ok_or(ProgramError::NotEnoughAccountKeys)?;
            if succ_state.key != &state_address(&program).0 {
                return Err(AdminError::WrongAccount.into());
            }
            if a.is_some() {
                msg!("transfer: the successor still has an upgrade authority, spent");
                true
            } else if !can_inherit(program_id, &program, succ_state) {
                msg!("transfer: the successor's state cannot take the hand-over (none, another predecessor, inherited, retired, or another clock factor), spent");
                true
            } else {
                // the successor's authority PDA: the loader's new authority is an account of its instruction
                let succ_authority = next_account_info(it)?;
                let successor_authority = Pubkey::find_program_address(&[AUTHORITY_SEED], &program).0;
                if succ_authority.key != &successor_authority {
                    return Err(AdminError::WrongAccount.into());
                }
                for p in [MAILBOX, TOKENS[0], TOKENS[1], CLIENT, PAUSABLE] {
                    let pd = next_account_info(it)?;
                    if pd.key != &loader::programdata_address(&p) {
                        return Err(AdminError::WrongAccount.into());
                    }
                    invoke_signed(&loader::set_authority(pd.key, authority.key, Some(&successor_authority)), &[pd.clone(), authority.clone(), succ_authority.clone(), loader_program.clone()], &[seeds])?;
                }
                let outbox = next_account_info(it)?;
                let mprogram = next_account_info(it)?;
                invoke_signed(&hl::mailbox_transfer_ownership(&MAILBOX, authority.key, Some(successor_authority)), &[outbox.clone(), authority.clone(), mprogram.clone()], &[seeds])?;
                for t in TOKENS.iter() {
                    let token_pda = next_account_info(it)?;
                    let tprogram = next_account_info(it)?;
                    invoke_signed(&hl::token_transfer_ownership(t, authority.key, Some(successor_authority)), &[token_pda.clone(), authority.clone(), tprogram.clone()], &[seeds])?;
                }
                let pstate = next_account_info(it)?;
                let pprogram = next_account_info(it)?;
                let ix = SolInstruction {
                    program_id: PAUSABLE,
                    accounts: vec![AccountMeta::new(*pstate.key, false), AccountMeta::new_readonly(*authority.key, true)],
                    data: borsh::to_vec(&pausable_ism::instruction::Instruction::TransferOwnership { new_owner: successor_authority }).unwrap(),
                };
                invoke_signed(&ix, &[pstate.clone(), authority.clone(), pprogram.clone()], &[seeds])?;
                // the client's admin role (C234): its state and program, which the client checks itself
                let client_state = next_account_info(it)?;
                let cprogram = next_account_info(it)?;
                let ix = SolInstruction {
                    program_id: CLIENT,
                    accounts: vec![AccountMeta::new(*client_state.key, false), AccountMeta::new_readonly(*authority.key, true)],
                    data: borsh::to_vec(&gno_light_client_ism::instruction::Instruction::SetAdmin { new_admin: successor_authority }).unwrap(),
                };
                invoke_signed(&ix, &[client_state.clone(), authority.clone(), cprogram.clone()], &[seeds])?;
                // the live number, seats and hold history into the successor (C227): its state, this program's
                // authority PDA signing; nothing grows there
                let succ_state = next_account_info(it)?;
                let system = next_account_info(it)?;
                let ix = SolInstruction {
                    program_id: program,
                    accounts: vec![AccountMeta::new(*succ_state.key, false), AccountMeta::new_readonly(*authority.key, true)],
                    data: borsh::to_vec(&Instruction::SuccessorInit {
                        origin_domain: s.origin_domain,
                        last_seq: s.last_seq,
                        seats: [s.guardians.keys[0], s.guardians.keys[1], s.guardians.keys[2], s.guardians.keys[3]],
                        watch: [s.guardians.keys[4], s.guardians.keys[5]],
                        holds: s.holds.clone(),
                    })
                    .unwrap(),
                };
                invoke_signed(&ix, &[succ_state.clone(), authority.clone(), succ_program.clone()], &[seeds])?;
                // and the float (S-25): this program's authority PDA — system-owned and data-less, funded by hlsol
                // setup with the rent Hyperlane's EnrollRemoteRouters draws from the owner when a token account
                // grows (M2-NOTES 17b) — sends every lamport it holds to the successor's PDA, which started with
                // none, so the first kind 6 there never waits on a hand, and nothing stays stranded on a retired
                // program's PDA, which no instruction moves; the PDA signs the system transfer, the successor's
                // PDA being writable in the account list
                let float = authority.lamports();
                if float > 0 {
                    invoke_signed(&system_instruction::transfer(authority.key, succ_authority.key, float), &[authority.clone(), succ_authority.clone(), system.clone()], &[seeds])?;
                }
                s.successor = Some(program);
                false
            }
        }
        Params::SetMintLimits { token, max, day_cap } => {
            // the pausable ISM's owner-only setter, this program's authority PDA signing (AN5)
            let pstate = next_account_info(it)?;
            let pprogram = next_account_info(it)?;
            let ix = SolInstruction {
                program_id: PAUSABLE,
                accounts: vec![AccountMeta::new(*pstate.key, false), AccountMeta::new_readonly(*authority.key, true)],
                data: borsh::to_vec(&pausable_ism::instruction::Instruction::SetMintLimits { token, max, day_cap }).unwrap(),
            };
            invoke_signed(&ix, &[pstate.clone(), authority.clone(), pprogram.clone()], &[seeds])?;
            false
        }
    };
    if spent {
        msg!("decision {}: spent", number);
    }
    close_and_store(&mut s, state, decision, deliverer, &d)
}

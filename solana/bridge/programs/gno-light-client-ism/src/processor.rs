//! The handlers.

use crate::{
    config::*,
    error::{core_err, ClientError},
    instruction::Instruction,
    stage::{set_bit, Stage, StagedPrecommit, KIND_PROOF, KIND_UPDATE},
    state::{full_len, Body, Head, Ring, RingEntry, SetEntry, Trusted, MAX_GROWTH, RING_ENTRY, STATE_SEED},
};
use borsh::BorshDeserialize;
use gno_light_core::{
    commit,
    header::{BlockId, Header},
    merkle::{self, SimpleProof},
    results,
    valset::{Validator, ValidatorSet},
};
use sol_util::{clock, ed25519, hl, loader, pda, sha256};
use solana_program::{
    account_info::{next_account_info, AccountInfo},
    entrypoint::ProgramResult,
    instruction::AccountMeta,
    program::set_return_data,
    program_error::ProgramError,
    pubkey::Pubkey,
};

pub fn process(program_id: &Pubkey, accounts: &[AccountInfo], data: &[u8]) -> ProgramResult {
    if let Some(call) = hl::decode_interface(data)? {
        return match call {
            hl::InterfaceCall::IsmType => {
                set_return_data(&hl::return_data(ISM_TYPE));
                Ok(())
            }
            hl::InterfaceCall::IsmVerify(v) => verify(program_id, accounts, &v.metadata, &v.message),
            hl::InterfaceCall::IsmVerifyAccountMetas(v) => verify_account_metas(program_id, &v.metadata, &v.message),
            _ => Err(ProgramError::InvalidInstructionData),
        };
    }
    let ix = Box::new(Instruction::try_from_slice(data).map_err(|_| ProgramError::InvalidInstructionData)?);
    match *ix {
        Instruction::Init { chain_id, origin_domain, trusted_height, trusted_hash, validators, admin } => {
            init(program_id, accounts, chain_id, origin_domain, trusted_height, trusted_hash, validators, admin)
        }
        Instruction::StageInit { kind, header, ring_hash, block_id, round, n_slots, message_hash, next_set_len, next_set_hash, proof, leaf_len } => {
            let args = Box::new(StageInitArgs { kind, header, ring_hash, block_id, round, n_slots, message_hash, next_set_len, next_set_hash, proof, leaf_len });
            stage_init(program_id, accounts, args)
        }
        Instruction::StagePrecommits { offset, items } => stage_precommits(program_id, accounts, offset, items),
        Instruction::StageNextSet { offset, items } => stage_next_set(program_id, accounts, offset, items),
        Instruction::StageLeaf { chunk } => stage_leaf(program_id, accounts, &chunk),
        Instruction::StageVerify { ix_index, slots } => stage_verify(program_id, accounts, ix_index, &slots),
        Instruction::UpdateFinish => update_finish(program_id, accounts),
        Instruction::StageClose => stage_close(program_id, accounts),
        Instruction::SetAdmin { new_admin } => set_admin(program_id, accounts, new_admin),
        Instruction::Grow { body_cap } => grow(program_id, accounts, body_cap as usize),
    }
}

/// The state PDA: ours, by seed and owner.
fn check_state(program_id: &Pubkey, state: &AccountInfo) -> Result<(), ProgramError> {
    if state.owner != program_id {
        return Err(ClientError::NotInitialized.into());
    }
    if state.key != &Pubkey::find_program_address(&[STATE_SEED], program_id).0 {
        return Err(ProgramError::InvalidSeeds);
    }
    Ok(())
}

fn load_stage(program_id: &Pubkey, stage: &AccountInfo) -> Result<Box<Stage>, ProgramError> {
    if stage.owner != program_id {
        return Err(ClientError::WrongStage.into());
    }
    let st = Stage::load(&stage.try_borrow_data()?)?;
    if st.version != STAGE_VERSION {
        return Err(ClientError::WrongVersion.into());
    }
    Ok(st)
}

fn new_set(validators: Vec<Validator>) -> Result<(ValidatorSet, [u8; 32]), ProgramError> {
    let set = ValidatorSet { validators };
    set.check(sha256).map_err(core_err)?;
    let hash = set.hash(sha256).ok_or(ClientError::WrongSet)?;
    Ok((set, hash))
}

fn start_body(admin: Pubkey, chain_id: String, origin_domain: u32, trusted_height: u64, trusted_hash: [u8; 32], validators: Vec<Validator>, slot: u64, now: i64) -> Result<Body, ProgramError> {
    if chain_id.is_empty() || chain_id.len() > 64 || trusted_height == 0 || trusted_height > i64::MAX as u64 {
        return Err(ClientError::ChainId.into());
    }
    let (set, set_hash) = new_set(validators)?;
    let mut body = Body {
        admin,
        chain_id,
        origin_domain,
        trusted: Trusted { height: trusted_height as i64, hash: trusted_hash, validators_hash: set_hash, next_validators_hash: set_hash, time_secs: 0 },
        set: set.clone(),
        set_hash,
        next_set: None,
        next_set_hash: [0; 32],
        history: vec![SetEntry { from_height: trusted_height as i64, set_hash, set, announced_slot: slot, announced_time: now, quarantine_end: 0 }],
        records: 0,
        running_hash: [0; 32],
        ring_epoch: 0,
    };
    body.record(sha256, &trusted_hash, trusted_height as i64, slot);
    Ok(body)
}

#[allow(clippy::too_many_arguments)]
#[inline(never)]
fn init(program_id: &Pubkey, accounts: &[AccountInfo], chain_id: String, origin_domain: u32, trusted_height: u64, trusted_hash: [u8; 32], validators: Vec<Validator>, admin: Pubkey) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let payer = next_account_info(it)?;
    let authority = next_account_info(it)?;
    let program = next_account_info(it)?;
    let programdata = next_account_info(it)?;
    let system = next_account_info(it)?;

    let (state_key, bump) = Pubkey::find_program_address(&[STATE_SEED], program_id);
    if state.key != &state_key {
        return Err(ProgramError::InvalidSeeds);
    }
    if !state.data_is_empty() || state.owner == program_id {
        return Err(ClientError::AlreadyInitialized.into());
    }
    if !payer.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    // the program's current upgrade authority signs
    if program.key != program_id || program.owner != &loader::LOADER_ID {
        return Err(ClientError::WrongAuthority.into());
    }
    let pd_key = loader::parse_program(&program.try_borrow_data()?)?;
    if programdata.key != &pd_key || programdata.owner != &loader::LOADER_ID {
        return Err(ClientError::WrongAuthority.into());
    }
    let (_, upgrade_authority) = loader::parse_programdata(&programdata.try_borrow_data()?)?;
    if upgrade_authority != Some(*authority.key) || !authority.is_signer {
        return Err(ClientError::WrongAuthority.into());
    }

    let (slot, now) = clock::now()?;
    let body = start_body(admin, chain_id, origin_domain, trusted_height, trusted_hash, validators, slot, now)?;
    let body_cap = body.wanted_cap();
    let len = full_len(body_cap).min(MAX_GROWTH);
    if crate::state::HEAD_LEN + body_cap > len {
        return Err(ClientError::NeedsGrow.into());
    }
    pda::create_pda_account(payer, state, system, program_id, len, &[STATE_SEED, &[bump]])?;
    let mut data = state.try_borrow_mut_data()?;
    let head = Head { version: STATE_VERSION, clock_factor: CLOCK_FACTOR as u8, ring_count: 0, ring_next: 0, body_cap8: (body_cap / 8) as u16, quarantine_end: 0, last_accepted_time: now, trusted_height, trusted_hash };
    head.write(&mut data);
    body.store(&head, &mut data)
}

/// Grows the state account by at most 10 KB per call: the body region first, to the larger of what the body
/// wants and the caller's target (moving the ring once the account holds the new region and a ring of the
/// capacity it had), then the ring to 256.
#[inline(never)]
fn grow(program_id: &Pubkey, accounts: &[AccountInfo], body_cap: usize) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let payer = next_account_info(it)?;
    let system = next_account_info(it)?;
    check_state(program_id, state)?;
    if !payer.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    let (mut head, body) = {
        let data = state.try_borrow_data()?;
        (Head::read(&data)?, Body::load(&data)?)
    };
    // the target is bounded (MAX_BODY_CAP) and rounded to 8, never below the current region or the body's want
    let requested = body_cap.min(MAX_BODY_CAP).div_ceil(8) * 8;
    let wanted = body.wanted_cap().max(head.body_cap()).max(requested);
    let target = full_len(wanted);
    let len = state.data_len();
    if len >= target && wanted == head.body_cap() {
        return Ok(());
    }
    let new_len = target.min(len + MAX_GROWTH);
    if new_len > len {
        pda::resize_account(state, payer, system, new_len)?;
    }
    let mut data = state.try_borrow_mut_data()?;
    // the body region moves to its new capacity only once the account holds the new region and a ring of the
    // capacity it had before this call (S-17; S-09 waited for the entries the ring held, so a stranger's Grow to
    // the bound over a cleared ring moved the region as soon as it fit, leaving a ring of 9 slots until two more
    // paid calls grew it back, and S-09's first build moved it as soon as the region alone fit, dropping the
    // entries past the end); until then the account grows and the region stays, so a region bigger than one step
    // takes more calls and loses neither an entry nor a slot
    let had = Ring::capacity(&head, len).min(RING);
    if wanted > head.body_cap() && crate::state::HEAD_LEN + wanted + had * RING_ENTRY <= new_len {
        Ring::relocate(&mut head, &mut data, wanted);
    }
    head.write(&mut data);
    Ok(())
}

pub struct StageInitArgs {
    pub kind: u8,
    pub header: Option<Header>,
    pub ring_hash: [u8; 32],
    pub block_id: BlockId,
    pub round: i64,
    pub n_slots: u16,
    pub message_hash: [u8; 32],
    pub next_set_len: u16,
    pub next_set_hash: [u8; 32],
    pub proof: Option<SimpleProof>,
    pub leaf_len: u32,
}

#[inline(never)]
fn stage_init(program_id: &Pubkey, accounts: &[AccountInfo], args: Box<StageInitArgs>) -> ProgramResult {
    let StageInitArgs { kind, header, ring_hash, block_id, round, n_slots, message_hash, next_set_len, next_set_hash, proof, leaf_len } = *args;
    let it = &mut accounts.iter();
    let submitter = next_account_info(it)?;
    let stage = next_account_info(it)?;
    let state = next_account_info(it)?;
    let system = next_account_info(it)?;
    if !submitter.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    if round < 0 {
        return Err(ProgramError::InvalidInstructionData);
    }
    check_state(program_id, state)?;
    let data = state.try_borrow_data()?;
    let head = Head::read(&data)?;
    let body = Body::load(&data)?;

    let mut st = Box::new(Stage {
        version: STAGE_VERSION,
        kind,
        bump: 0,
        submitter: *submitter.key,
        created_slot: 0,
        created_time: 0,
        key: [0; 32],
        by_ring: false,
        header_hash: [0; 32],
        header: Header::default(),
        block_id: BlockId::default(),
        round,
        set_hash: [0; 32],
        n_slots,
        precommits: vec![StagedPrecommit::default(); n_slots as usize],
        filled: vec![0; (n_slots as usize + 7) / 8],
        verified: vec![0; (n_slots as usize + 7) / 8],
        next_set: Vec::new(),
        next_set_len: 0,
        next_set_hash: [0; 32],
        proof: None,
        leaf_len,
        leaf_filled: 0,
        ring_epoch: 0,
        ring_height: 0,
        ring_results: [0; 32],
    });
    let seed: &[u8] = match kind {
        KIND_UPDATE => {
            let header = header.ok_or(ClientError::BadMessage)?;
            header.check().map_err(core_err)?;
            if header.chain_id != body.chain_id {
                return Err(ClientError::ChainId.into());
            }
            if header.height <= body.trusted.height {
                return Err(ClientError::HeightNotAboveTrusted.into());
            }
            let (set_hash, set_len) = match &body.next_set {
                Some(ns) => (body.next_set_hash, ns.len()),
                None => (body.set_hash, body.set.len()),
            };
            if header.validators_hash_array() != set_hash {
                return Err(ClientError::ValidatorsHash.into());
            }
            if n_slots as usize != set_len {
                return Err(ClientError::SlotOutOfRange.into());
            }
            let hash = header.hash(sha256);
            if block_id.hash.as_slice() != hash || block_id.parts.total == 0 || block_id.parts.hash.len() != 32 {
                return Err(ProgramError::Custom(1000 + ClientError::WrongStage as u32));
            }
            if header.next_validators_hash != header.validators_hash {
                // the next set arrives by StageNextSet; its length and hash are bound here, the hash to the
                // header's NextValidatorsHash, and the whole set is checked against it at the finish
                if next_set_len == 0 || next_set_len as usize > gno_light_core::valset::MAX_VALIDATORS {
                    return Err(ClientError::NextSetMissing.into());
                }
                if next_set_hash != header.next_validators_hash_array() {
                    return Err(ClientError::NextValidatorsHash.into());
                }
                st.next_set_len = next_set_len;
                st.next_set_hash = next_set_hash;
            } else if next_set_len != 0 {
                return Err(ClientError::NextSetUnexpected.into());
            }
            if proof.is_some() || leaf_len != 0 {
                return Err(ClientError::ProofMismatch.into());
            }
            st.key = hash;
            st.header_hash = hash;
            st.header = header;
            st.block_id = block_id;
            st.set_hash = set_hash;
            UPDATE_SEED
        }
        KIND_PROOF => {
            if leaf_len == 0 || leaf_len as usize > MAX_LEAF {
                return Err(ClientError::LeafTooBig.into());
            }
            let proof = proof.ok_or(ClientError::ProofMissing)?;
            if proof.total == 0 || proof.index >= proof.total || proof.aunts.len() > 100 {
                return Err(ClientError::ProofMismatch.into());
            }
            if next_set_len != 0 {
                return Err(ClientError::NextSetUnexpected.into());
            }
            match header {
                Some(header) => {
                    header.check().map_err(core_err)?;
                    if header.chain_id != body.chain_id {
                        return Err(ClientError::ChainId.into());
                    }
                    if header.height > body.trusted.height {
                        return Err(ClientError::HeightAboveTrusted.into());
                    }
                    if header.validators_hash_array() != body.set_hash {
                        return Err(ClientError::ValidatorsHash.into());
                    }
                    if n_slots as usize != body.set.len() {
                        return Err(ClientError::SlotOutOfRange.into());
                    }
                    let hash = header.hash(sha256);
                    if block_id.hash.as_slice() != hash || block_id.parts.total == 0 || block_id.parts.hash.len() != 32 {
                        return Err(ClientError::WrongStage.into());
                    }
                    st.header_hash = hash;
                    st.header = header;
                    st.block_id = block_id;
                }
                None => {
                    let e = Ring::find(&head, &data, &ring_hash).ok_or(ClientError::NotInRing)?;
                    if n_slots != 0 {
                        return Err(ClientError::SlotOutOfRange.into());
                    }
                    if round != 0 {
                        // a ring proof carries no commit, so no round
                        return Err(ProgramError::InvalidInstructionData);
                    }
                    st.by_ring = true;
                    st.header_hash = ring_hash;
                    // the entry's height and results hash are copied under the ring's epoch (S-16): the entry
                    // itself may be evicted before the delivery — a stranger's 256 fresh header proofs turn the
                    // ring over — and Verify reads the copy while the epoch and the set are still these
                    st.ring_epoch = body.ring_epoch;
                    st.ring_height = e.height;
                    st.ring_results = e.last_results_hash;
                }
            }
            st.key = message_hash;
            st.set_hash = body.set_hash;
            st.proof = Some(proof);
            PROOF_SEED
        }
        _ => return Err(ProgramError::InvalidInstructionData),
    };
    drop(data);

    let (stage_key, bump) = Pubkey::find_program_address(&[seed, submitter.key.as_ref(), &st.key], program_id);
    if stage.key != &stage_key {
        return Err(ProgramError::InvalidSeeds);
    }
    let (slot, now) = clock::now()?;
    st.bump = bump;
    st.created_slot = slot;
    st.created_time = now;
    let len = st.account_len().min(MAX_GROWTH);
    if st.size() > len {
        return Err(ClientError::NeedsGrow.into());
    }
    pda::create_pda_account(submitter, stage, system, program_id, len, &[seed, submitter.key.as_ref(), &st.key, &[bump]])?;
    st.store(&mut stage.try_borrow_mut_data()?)
}

/// A stage grows toward its full length as it is filled (the submitter paying), 10 KB at most per instruction.
fn ensure_stage_len<'a>(stage: &AccountInfo<'a>, submitter: &AccountInfo<'a>, system: Option<&AccountInfo<'a>>, needed: usize) -> ProgramResult {
    let len = stage.data_len();
    if needed <= len {
        return Ok(());
    }
    let system = system.ok_or(ClientError::NeedsGrow)?;
    pda::resize_account(stage, submitter, system, needed.min(len + MAX_GROWTH))
}

#[inline(never)]
fn stage_precommits(program_id: &Pubkey, accounts: &[AccountInfo], offset: u16, items: Vec<StagedPrecommit>) -> ProgramResult {
    let it = &mut accounts.iter();
    let submitter = next_account_info(it)?;
    let stage = next_account_info(it)?;
    let mut st = load_stage(program_id, stage)?;
    if !submitter.is_signer || submitter.key != &st.submitter {
        return Err(ClientError::WrongSubmitter.into());
    }
    if st.by_ring {
        return Err(ClientError::WrongStage.into());
    }
    for (j, p) in items.into_iter().enumerate() {
        let i = offset as usize + j;
        if i >= st.n_slots as usize {
            return Err(ClientError::SlotOutOfRange.into());
        }
        if st.is_filled(i) {
            return Err(ClientError::SlotAlreadyFilled.into());
        }
        if p.round != st.round {
            // one round per commit (S-08): the stage's, from which every slot's sign bytes are built
            return Err(ClientError::RoundMismatch.into());
        }
        if p.nanos < 0 || p.nanos > 999_999_999 {
            return Err(ProgramError::InvalidInstructionData);
        }
        st.precommits[i] = p;
        set_bit(&mut st.filled, i);
    }
    st.store(&mut stage.try_borrow_mut_data()?)
}

/// Appends to an announcing update's next set, in order; the stage grows with it (the submitter paying).
#[inline(never)]
fn stage_next_set(program_id: &Pubkey, accounts: &[AccountInfo], offset: u16, items: Vec<Validator>) -> ProgramResult {
    let it = &mut accounts.iter();
    let submitter = next_account_info(it)?;
    let stage = next_account_info(it)?;
    let mut st = load_stage(program_id, stage)?;
    if !submitter.is_signer || submitter.key != &st.submitter {
        return Err(ClientError::WrongSubmitter.into());
    }
    if st.kind != KIND_UPDATE || st.next_set_len == 0 {
        return Err(ClientError::NextSetUnexpected.into());
    }
    if items.is_empty() || items.len() > NEXT_SET_CHUNK || offset as usize != st.next_set.len() || st.next_set.len() + items.len() > st.next_set_len as usize {
        return Err(ClientError::ChunkOutOfOrder.into());
    }
    st.next_set.extend_from_slice(&items);
    let system = next_account_info(it).ok();
    ensure_stage_len(stage, submitter, system, st.account_len())?;
    st.store(&mut stage.try_borrow_mut_data()?)
}

#[inline(never)]
fn stage_leaf(program_id: &Pubkey, accounts: &[AccountInfo], chunk: &[u8]) -> ProgramResult {
    let it = &mut accounts.iter();
    let submitter = next_account_info(it)?;
    let stage = next_account_info(it)?;
    let mut st = load_stage(program_id, stage)?;
    if !submitter.is_signer || submitter.key != &st.submitter {
        return Err(ClientError::WrongSubmitter.into());
    }
    if st.kind != KIND_PROOF {
        return Err(ClientError::WrongStage.into());
    }
    let start = st.leaf_filled as usize;
    let end = start + chunk.len();
    if end > st.leaf_len as usize {
        return Err(ClientError::LeafTooBig.into());
    }
    let off = st.size();
    let system = next_account_info(it).ok();
    ensure_stage_len(stage, submitter, system, off + end)?;
    let mut data = stage.try_borrow_mut_data()?;
    data[off + start..off + end].copy_from_slice(chunk);
    st.leaf_filled = end as u32;
    st.store(&mut data)
}

#[inline(never)]
fn stage_verify(program_id: &Pubkey, accounts: &[AccountInfo], ix_index: u16, slots: &[u16]) -> ProgramResult {
    let it = &mut accounts.iter();
    let stage = next_account_info(it)?;
    let state = next_account_info(it)?;
    let sysvar = next_account_info(it)?;
    let mut st = load_stage(program_id, stage)?;
    if st.by_ring {
        return Err(ClientError::WrongStage.into());
    }
    check_state(program_id, state)?;
    let body = Body::load(&state.try_borrow_data()?)?;
    let set = if st.set_hash == body.set_hash {
        &body.set
    } else if body.next_set.is_some() && st.set_hash == body.next_set_hash {
        body.next_set.as_ref().unwrap()
    } else {
        return Err(ClientError::WrongSet.into());
    };
    let entries = ed25519::precompile_entries(sysvar, ix_index as usize)?;
    if entries.is_empty() || entries.len() != slots.len() {
        return Err(ClientError::SignatureMismatch.into());
    }
    for (e, &slot) in entries.iter().zip(slots) {
        let i = slot as usize;
        if i >= st.n_slots as usize {
            return Err(ClientError::SlotOutOfRange.into());
        }
        if !st.is_filled(i) {
            return Err(ClientError::SlotNotFilled.into());
        }
        if e.pubkey != set.validators[i].pubkey || e.message != st.sign_bytes(&body.chain_id, i) {
            return Err(ClientError::SignatureMismatch.into());
        }
        set_bit(&mut st.verified, i);
    }
    st.store(&mut stage.try_borrow_mut_data()?)
}

/// The tally of a stage's verified slots against a set.
fn tally(st: &Stage, set: &ValidatorSet) -> Result<(), ProgramError> {
    let c = st.verified_commit();
    c.check_shape(&st.header_hash, set.len()).map_err(core_err)?;
    let tallied = commit::tallied_power(set, |i| st.is_verified(i)).map_err(core_err)?;
    let total = set.total_power().map_err(core_err)?;
    if !commit::commit_rule(tallied, total) {
        return Err(ClientError::NotEnoughPower.into());
    }
    Ok(())
}

#[inline(never)]
fn update_finish(program_id: &Pubkey, accounts: &[AccountInfo]) -> ProgramResult {
    let it = &mut accounts.iter();
    let stage = next_account_info(it)?;
    let state = next_account_info(it)?;
    let submitter = next_account_info(it)?;
    let mut st = load_stage(program_id, stage)?;
    if st.kind != KIND_UPDATE {
        return Err(ClientError::WrongStage.into());
    }
    if submitter.key != &st.submitter {
        return Err(ClientError::WrongSubmitter.into());
    }
    check_state(program_id, state)?;
    let (mut head, mut body) = {
        let data = state.try_borrow_data()?;
        (Head::read(&data)?, Body::load(&data)?)
    };
    // an announcing update's next set, whole and checked against the header's NextValidatorsHash (S-02: the set
    // arrived in chunks, its hash bound at the staging)
    let announced = if st.header.next_validators_hash != st.header.validators_hash {
        if st.next_set.len() != st.next_set_len as usize {
            return Err(ClientError::StageIncomplete.into());
        }
        let (ns, ns_hash) = new_set(core::mem::take(&mut st.next_set))?;
        if ns_hash != st.next_set_hash || ns_hash != st.header.next_validators_hash_array() {
            return Err(ClientError::NextValidatorsHash.into());
        }
        Some((ns, ns_hash))
    } else {
        None
    };
    if st.header.chain_id != body.chain_id {
        return Err(ClientError::ChainId.into());
    }
    if st.header.height <= body.trusted.height {
        return Err(ClientError::HeightNotAboveTrusted.into());
    }
    let using_next = body.next_set.is_some();
    let expect = if using_next { body.next_set_hash } else { body.set_hash };
    if st.set_hash != expect || st.header.validators_hash_array() != expect {
        return Err(ClientError::WrongSet.into());
    }
    {
        let set = if using_next { body.next_set.as_ref().unwrap() } else { &body.set };
        tally(&st, set)?;
    }
    let (slot, now) = clock::now()?;
    if using_next {
        body.set = body.next_set.take().unwrap();
        body.set_hash = body.next_set_hash;
        body.next_set_hash = [0; 32];
        Ring::clear(&mut head, &mut body);
    }
    let height = st.header.height;
    body.trusted = Trusted {
        height,
        hash: st.header_hash,
        validators_hash: st.header.validators_hash_array(),
        next_validators_hash: st.header.next_validators_hash_array(),
        time_secs: st.header.time.secs,
    };
    head.trusted_height = height as u64;
    head.trusted_hash = st.header_hash;
    head.last_accepted_time = now;
    if let Some((ns, ns_hash)) = announced {
        let end = head.quarantine_end.max(now.saturating_add(clock::week(CLOCK_FACTOR)));
        head.quarantine_end = end;
        // the update stays staged when the history is full (S-01): nothing is lost, and the next entry to
        // settle by the clock makes room
        body.push_history(SetEntry { from_height: height + 1, set_hash: ns_hash, set: ns.clone(), announced_slot: slot, announced_time: now, quarantine_end: end }, now)?;
        body.next_set = Some(ns);
        body.next_set_hash = ns_hash;
    }
    body.record(sha256, &st.header_hash, height, slot);
    {
        let mut data = state.try_borrow_mut_data()?;
        body.store(&head, &mut data)?;
        Ring::push(&mut head, &mut data, &RingEntry { hash: st.header_hash, height, last_results_hash: st.header.last_results_hash_array().unwrap_or([0; 32]) });
        head.write(&mut data);
    }
    pda::close_account(stage, submitter)
}

#[inline(never)]
fn stage_close(program_id: &Pubkey, accounts: &[AccountInfo]) -> ProgramResult {
    let it = &mut accounts.iter();
    let stage = next_account_info(it)?;
    let closer = next_account_info(it)?;
    let st = load_stage(program_id, stage)?;
    if !closer.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    if closer.key != &st.submitter {
        let (_, now) = clock::now()?;
        if now < st.created_time.saturating_add(clock::week(CLOCK_FACTOR)) {
            return Err(ClientError::NotAbandoned.into());
        }
    }
    pda::close_account(stage, closer)
}

/// The proof PDA a Verify names: (`proof`, the submitter in the metadata, sha256 of the message).
fn proof_address(program_id: &Pubkey, metadata: &[u8], message: &[u8]) -> Result<(Pubkey, [u8; 32]), ProgramError> {
    if metadata.len() != 32 {
        return Err(ClientError::Metadata.into());
    }
    let msg_hash = sha256(&[message]);
    Ok((Pubkey::find_program_address(&[PROOF_SEED, metadata, &msg_hash], program_id).0, msg_hash))
}

#[inline(never)]
fn verify(program_id: &Pubkey, accounts: &[AccountInfo], metadata: &[u8], message: &[u8]) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let stage = next_account_info(it)?;
    let m = hl::Message::parse(message)?;
    if m.version != hl::MESSAGE_VERSION {
        return Err(ClientError::BadMessage.into());
    }
    let (stage_key, msg_hash) = proof_address(program_id, metadata, message)?;
    if stage.key != &stage_key {
        return Err(ProgramError::InvalidSeeds);
    }
    let st = load_stage(program_id, stage)?;
    if st.kind != KIND_PROOF || st.key != msg_hash {
        return Err(ClientError::WrongStage.into());
    }
    if st.leaf_filled != st.leaf_len {
        return Err(ClientError::LeafIncomplete.into());
    }
    check_state(program_id, state)?;
    let (mut head, mut body) = {
        let data = state.try_borrow_data()?;
        (Head::read(&data)?, Body::load(&data)?)
    };
    if m.origin != body.origin_domain {
        return Err(ClientError::OriginMismatch.into());
    }
    let (height, root, fresh) = if st.by_ring {
        // the copy taken at the staging serves while the ring's epoch and the set are still the staging's (S-16):
        // no entry is needed, so the ring turning over — 256 later headers, accepted or proven by a stranger —
        // evicts nothing a staged proof reads; after a set change (the ring cleared, the epoch up) the entry
        // must stand again, which an old header never does under a new set: NotInRing
        let (height, root) = if st.ring_epoch == body.ring_epoch && st.set_hash == body.set_hash {
            (st.ring_height, st.ring_results)
        } else {
            let data = state.try_borrow_data()?;
            let e = Ring::find(&head, &data, &st.header_hash).ok_or(ClientError::NotInRing)?;
            (e.height, e.last_results_hash)
        };
        if root == [0; 32] {
            return Err(ClientError::NoResultsHash.into());
        }
        (height, root, false)
    } else {
        if st.set_hash != body.set_hash || st.header.validators_hash_array() != body.set_hash {
            return Err(ClientError::WrongSet.into());
        }
        if st.header.chain_id != body.chain_id {
            return Err(ClientError::ChainId.into());
        }
        if st.header.height > body.trusted.height {
            return Err(ClientError::HeightAboveTrusted.into());
        }
        tally(&st, &body.set)?;
        let root = st.header.last_results_hash_array().ok_or(ClientError::NoResultsHash)?;
        // a header the ring holds was recorded when it was accepted or first proven: only a fresh one is recorded
        // (S-10: a repeated Verify of one header cannot inflate the record), and it joins the ring
        let fresh = Ring::find(&head, &state.try_borrow_data()?, &st.header_hash).is_none();
        (st.header.height, root, fresh)
    };
    let proof = st.proof.as_ref().ok_or(ClientError::ProofMissing)?;
    {
        let data = stage.try_borrow_data()?;
        let off = st.size();
        let leaf = &data[off..off + st.leaf_len as usize];
        merkle::verify_proof(sha256, proof, &root, leaf).map_err(|_| ClientError::ProofMismatch)?;
        let dispatches = results::dispatch_events(leaf, MAILBOX_PKG_PATH).map_err(core_err)?;
        let id = hl::Message::id(message);
        let found = dispatches.iter().any(|d| {
            d.id == id && d.hash == msg_hash && d.nonce == m.nonce && d.origin == m.origin && d.destination == m.destination && d.sender == m.sender && d.recipient == m.recipient
        });
        if !found {
            return Err(ClientError::DispatchMismatch.into());
        }
    }
    if fresh {
        let (slot, _) = clock::now()?;
        body.record(sha256, &st.header_hash, height, slot);
        let mut data = state.try_borrow_mut_data()?;
        body.store(&head, &mut data)?;
        Ring::push(&mut head, &mut data, &RingEntry { hash: st.header_hash, height, last_results_hash: root });
        head.write(&mut data);
    }
    Ok(())
}

#[inline(never)]
fn verify_account_metas(program_id: &Pubkey, metadata: &[u8], message: &[u8]) -> ProgramResult {
    let (stage_key, _) = proof_address(program_id, metadata, message)?;
    let state_key = Pubkey::find_program_address(&[STATE_SEED], program_id).0;
    let metas: Vec<hl::SerializableAccountMeta> = vec![AccountMeta::new(state_key, false).into(), AccountMeta::new_readonly(stage_key, false).into()];
    set_return_data(&hl::return_data(metas));
    Ok(())
}

/// Kind 9's hand-over of the admin role: the one control of the client beside its upgrade authority (M2j: no
/// reset — a lapsed client stays lapsed, and the exit is a new deployment).
#[inline(never)]
fn set_admin(program_id: &Pubkey, accounts: &[AccountInfo], new_admin: Pubkey) -> ProgramResult {
    let it = &mut accounts.iter();
    let state = next_account_info(it)?;
    let admin = next_account_info(it)?;
    check_state(program_id, state)?;
    let mut data = state.try_borrow_mut_data()?;
    let head = Head::read(&data)?;
    let mut body = Body::load(&data)?;
    if !admin.is_signer || admin.key != &body.admin {
        return Err(ClientError::NotAdmin.into());
    }
    body.admin = new_admin;
    body.store(&head, &mut data)
}

//! Instruction builders for the light-client ISM, and the staging of an update or a proof over the ed25519
//! precompile, as the updater and the relayer (M3) will do it.

use gno_light_client_ism::{
    config::{PROOF_SEED, UPDATE_SEED},
    instruction::Instruction as Ix,
    stage::{StagedPrecommit, KIND_PROOF, KIND_UPDATE},
    state::STATE_SEED,
    ID,
};
use gno_light_core::{
    commit::Commit,
    header::{BlockId, Header},
    merkle::SimpleProof,
    valset::Validator,
};
use sol_util::{ed25519, hl};
use solana_sdk::{
    instruction::{AccountMeta, Instruction},
    pubkey::Pubkey,
    sysvar,
};
use solana_system_interface::program as system_program;

pub fn state() -> Pubkey {
    Pubkey::find_program_address(&[STATE_SEED], &ID).0
}

pub fn update_stage(submitter: &Pubkey, header_hash: &[u8; 32]) -> Pubkey {
    Pubkey::find_program_address(&[UPDATE_SEED, submitter.as_ref(), header_hash], &ID).0
}

pub fn proof_stage(submitter: &Pubkey, message_hash: &[u8; 32]) -> Pubkey {
    Pubkey::find_program_address(&[PROOF_SEED, submitter.as_ref(), message_hash], &ID).0
}

fn data(ix: &Ix) -> Vec<u8> {
    borsh::to_vec(ix).unwrap()
}

pub fn init(payer: &Pubkey, authority: &Pubkey, chain_id: &str, origin_domain: u32, trusted_height: u64, trusted_hash: [u8; 32], validators: Vec<Validator>, admin: Pubkey) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: vec![
            AccountMeta::new(state(), false),
            AccountMeta::new(*payer, true),
            AccountMeta::new_readonly(*authority, true),
            AccountMeta::new_readonly(ID, false),
            AccountMeta::new_readonly(sol_util::loader::programdata_address(&ID), false),
            AccountMeta::new_readonly(system_program::ID, false),
        ],
        data: data(&Ix::Init { chain_id: chain_id.into(), origin_domain, trusted_height, trusted_hash, validators, admin }),
    }
}

/// `StageInit`; an announcing update names its next set's length and hash (`next_set`), the set itself following
/// by `stage_next_set`. `round` is the commit's one round (S-08: every slot's sign bytes are built from it; 0 for
/// a ring proof).
#[allow(clippy::too_many_arguments)]
pub fn stage_init(submitter: &Pubkey, stage: &Pubkey, kind: u8, header: Option<Header>, ring_hash: [u8; 32], block_id: BlockId, round: i64, n_slots: u16, message_hash: [u8; 32], next_set: Option<&[Validator]>, proof: Option<SimpleProof>, leaf_len: u32) -> Instruction {
    let (next_set_len, next_set_hash) = match next_set {
        Some(v) => (v.len() as u16, gno_light_core::valset::ValidatorSet { validators: v.to_vec() }.hash(gno_light_core::sha256).unwrap_or([0; 32])),
        None => (0, [0; 32]),
    };
    stage_init_with(submitter, stage, kind, header, ring_hash, block_id, round, n_slots, message_hash, next_set_len, next_set_hash, proof, leaf_len)
}

#[allow(clippy::too_many_arguments)]
pub fn stage_init_with(submitter: &Pubkey, stage: &Pubkey, kind: u8, header: Option<Header>, ring_hash: [u8; 32], block_id: BlockId, round: i64, n_slots: u16, message_hash: [u8; 32], next_set_len: u16, next_set_hash: [u8; 32], proof: Option<SimpleProof>, leaf_len: u32) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: vec![AccountMeta::new(*submitter, true), AccountMeta::new(*stage, false), AccountMeta::new_readonly(state(), false), AccountMeta::new_readonly(system_program::ID, false)],
        data: data(&Ix::StageInit { kind, header, ring_hash, block_id, round, n_slots, message_hash, next_set_len, next_set_hash, proof, leaf_len }),
    }
}

pub fn stage_precommits(submitter: &Pubkey, stage: &Pubkey, offset: u16, items: Vec<StagedPrecommit>) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new_readonly(*submitter, true), AccountMeta::new(*stage, false)], data: data(&Ix::StagePrecommits { offset, items }) }
}

/// One chunk of an announcing update's next set.
pub fn stage_next_set(submitter: &Pubkey, stage: &Pubkey, offset: u16, items: Vec<Validator>) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(*submitter, true), AccountMeta::new(*stage, false), AccountMeta::new_readonly(system_program::ID, false)], data: data(&Ix::StageNextSet { offset, items }) }
}

/// The chunk transactions of a next set, `per` validators each (25 at most, the instruction's room).
pub fn next_set_txs(submitter: &Pubkey, stage: &Pubkey, next: &[Validator], per: usize) -> Vec<Vec<Instruction>> {
    next.chunks(per).enumerate().map(|(i, c)| vec![stage_next_set(submitter, stage, (i * per) as u16, c.to_vec())]).collect()
}

pub fn stage_leaf(submitter: &Pubkey, stage: &Pubkey, chunk: Vec<u8>) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(*submitter, true), AccountMeta::new(*stage, false), AccountMeta::new_readonly(system_program::ID, false)], data: data(&Ix::StageLeaf { chunk }) }
}

pub fn stage_verify(stage: &Pubkey, ix_index: u16, slots: Vec<u16>) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: vec![AccountMeta::new(*stage, false), AccountMeta::new_readonly(state(), false), AccountMeta::new_readonly(sysvar::instructions::ID, false)],
        data: data(&Ix::StageVerify { ix_index, slots }),
    }
}

pub fn update_finish(stage: &Pubkey, submitter: &Pubkey) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(*stage, false), AccountMeta::new(state(), false), AccountMeta::new(*submitter, false)], data: data(&Ix::UpdateFinish) }
}

pub fn stage_close(stage: &Pubkey, closer: &Pubkey) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(*stage, false), AccountMeta::new(*closer, true)], data: data(&Ix::StageClose) }
}

/// `Grow` toward what the body wants and the full ring.
pub fn grow(payer: &Pubkey) -> Instruction {
    grow_to(payer, 0)
}

/// `Grow` toward a body region of `body_cap` bytes (one 10 KB step a call).
pub fn grow_to(payer: &Pubkey, body_cap: u32) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(state(), false), AccountMeta::new(*payer, true), AccountMeta::new_readonly(system_program::ID, false)], data: data(&Ix::Grow { body_cap }) }
}

/// `SetAdmin`: the client's one control beside its upgrade authority (M2j: no reset).
pub fn set_admin(admin: &Pubkey, new_admin: Pubkey) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(state(), false), AccountMeta::new_readonly(*admin, true)], data: data(&Ix::SetAdmin { new_admin }) }
}

/// An instruction of the client with raw data under `tag` — the old `Reset`'s bytes, say — at the admin's
/// signature, to show it fits no variant.
pub fn raw(admin: &Pubkey, data: Vec<u8>) -> Instruction {
    Instruction { program_id: ID, accounts: vec![AccountMeta::new(state(), false), AccountMeta::new_readonly(*admin, true)], data }
}

/// The ISM `Verify` call as the mailbox would make it: the state and the proof stage.
pub fn verify(submitter: &Pubkey, message: &[u8]) -> Instruction {
    let msg_hash = gno_light_core::sha256(&[message]);
    let v = hl::VerifyInstruction { metadata: submitter.to_bytes().to_vec(), message: message.to_vec() };
    Instruction {
        program_id: ID,
        accounts: vec![AccountMeta::new(state(), false), AccountMeta::new_readonly(proof_stage(submitter, &msg_hash), false)],
        data: hl::encode_interface(hl::ISM_VERIFY, &v),
    }
}

pub fn verify_account_metas(submitter: &Pubkey, message: &[u8]) -> Instruction {
    let v = hl::VerifyInstruction { metadata: submitter.to_bytes().to_vec(), message: message.to_vec() };
    Instruction { program_id: ID, accounts: vec![], data: hl::encode_interface(hl::ISM_VERIFY_ACCOUNT_METAS, &v) }
}

pub fn ism_type() -> Instruction {
    Instruction { program_id: ID, accounts: vec![], data: hl::ISM_TYPE.to_vec() }
}

/// The ed25519 precompile instruction carrying these (pubkey, message, signature) entries.
pub fn ed25519_ix(entries: &[ed25519::Entry]) -> Instruction {
    Instruction { program_id: solana_sdk::ed25519_program::ID, accounts: vec![], data: ed25519::instruction_data(entries) }
}

/// The precommit data of a core commit, slot by slot (empty slots default, and absent from `present`).
pub fn staged(commit: &Commit) -> (Vec<StagedPrecommit>, Vec<usize>) {
    let mut items = Vec::new();
    let mut present = Vec::new();
    for (i, p) in commit.precommits.iter().enumerate() {
        match p {
            Some(p) => {
                items.push(StagedPrecommit { round: p.round, secs: p.time.secs, nanos: p.time.nanos });
                present.push(i);
            }
            None => items.push(StagedPrecommit::default()),
        }
    }
    (items, present)
}

/// The transactions that stage and finish an update: [init + precommits] · [next set chunk]… · [precompile +
/// verify]… · [finish]. `pubkeys` are the set's keys in slot order; `sigs` each slot's signature.
pub fn update_txs(submitter: &Pubkey, chain_id: &str, header: &Header, commit: &Commit, pubkeys: &[[u8; 32]], sigs: &[Option<[u8; 64]>], next_set: Option<Vec<Validator>>, per_tx: usize) -> Vec<Vec<Instruction>> {
    let hash = header.hash(gno_light_core::sha256);
    let stage = update_stage(submitter, &hash);
    let (items, present) = staged(commit);
    let n = commit.precommits.len() as u16;
    let mut txs = vec![vec![
        stage_init(submitter, &stage, KIND_UPDATE, Some(header.clone()), [0; 32], commit.block_id.clone(), commit.round(), n, [0; 32], next_set.as_deref(), None, 0),
        stage_precommits(submitter, &stage, 0, items),
    ]];
    if let Some(next) = &next_set {
        txs.extend(next_set_txs(submitter, &stage, next, gno_light_client_ism::config::NEXT_SET_CHUNK));
    }
    for chunk in present.chunks(per_tx) {
        let entries: Vec<ed25519::Entry> = chunk
            .iter()
            .map(|&i| ed25519::Entry { pubkey: pubkeys[i], signature: sigs[i].unwrap(), message: commit.sign_bytes(chain_id, header.height, i).unwrap() })
            .collect();
        txs.push(vec![ed25519_ix(&entries), stage_verify(&stage, 0, chunk.iter().map(|&i| i as u16).collect())]);
    }
    txs.push(vec![update_finish(&stage, submitter)]);
    txs
}

/// The transactions that stage a proof with its own header and commit, then the leaf.
#[allow(clippy::too_many_arguments)]
pub fn proof_txs(submitter: &Pubkey, chain_id: &str, header: Option<&Header>, ring_hash: [u8; 32], commit: &Commit, pubkeys: &[[u8; 32]], sigs: &[Option<[u8; 64]>], message: &[u8], proof: SimpleProof, leaf: &[u8], per_tx: usize) -> Vec<Vec<Instruction>> {
    let msg_hash = gno_light_core::sha256(&[message]);
    let stage = proof_stage(submitter, &msg_hash);
    let (items, present) = staged(commit);
    let n = if header.is_some() { commit.precommits.len() as u16 } else { 0 };
    let round = if header.is_some() { commit.round() } else { 0 };
    let mut first = vec![stage_init(submitter, &stage, KIND_PROOF, header.cloned(), ring_hash, commit.block_id.clone(), round, n, msg_hash, None, Some(proof), leaf.len() as u32)];
    if header.is_some() {
        first.push(stage_precommits(submitter, &stage, 0, items));
    }
    let mut txs = vec![first];
    if let Some(h) = header {
        for chunk in present.chunks(per_tx) {
            let entries: Vec<ed25519::Entry> = chunk
                .iter()
                .map(|&i| ed25519::Entry { pubkey: pubkeys[i], signature: sigs[i].unwrap(), message: commit.sign_bytes(chain_id, h.height, i).unwrap() })
                .collect();
            txs.push(vec![ed25519_ix(&entries), stage_verify(&stage, 0, chunk.iter().map(|&i| i as u16).collect())]);
        }
    }
    for chunk in leaf.chunks(900) {
        txs.push(vec![stage_leaf(submitter, &stage, chunk.to_vec())]);
    }
    txs
}

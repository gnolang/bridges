//! Prints the bytes of known instructions, bodies, PDAs and digests as the programs encode them, for the Go
//! side's byte-for-byte test (bridge/sol/vectors_test.go pins the output in bridge/sol/testdata/vectors.txt).
//! Every line is `name=hex`; the Go test rebuilds each from the same fixture and compares.

use admin_program::{
    config::{AUTHORITY_SEED, STATE_SEED},
    instruction::Instruction as AdminIx,
    kinds::{self, Params},
    state::{Decision, Hold},
};
use gno_light_client_ism::{
    config::{PROOF_SEED, UPDATE_SEED},
    instruction::Instruction as ClientIx,
    stage::{StagedPrecommit, KIND_PROOF, KIND_UPDATE},
};
use gno_light_core::{fixtures, merkle, sha256};
use pausable_ism::instruction::Instruction as PausableIx;
use sol_util::ed25519;
use solana_program::pubkey::Pubkey;
use std::path::Path;

const ADMIN: Pubkey = Pubkey::new_from_array([0x33; 32]);
const CLIENT: Pubkey = Pubkey::new_from_array([0x11; 32]);
const PAUSABLE: Pubkey = Pubkey::new_from_array([0x22; 32]);

fn line(name: &str, bytes: &[u8]) {
    println!("{name}={}", hex::encode(bytes));
}

fn staged(commit: &gno_light_core::commit::Commit) -> (Vec<StagedPrecommit>, Vec<usize>) {
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

fn main() {
    let dir = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../programs/fixtures");
    let fx = fixtures::load_dir(dir);
    let by = |h: i64| fx.iter().find(|f| f.height == h).expect("fixture");
    let f3 = by(487883);
    let f2 = by(487882);
    let f1 = by(487881);
    let fr = by(740913);
    let keys: Vec<[u8; 32]> = f3.validators.validators.iter().map(|v| v.pubkey).collect();
    let (c3, s3) = f3.commit();
    let (items, present) = staged(&c3);

    // the client
    // the commit's one round rides in the init (S-08): every fixture commit is round 0
    let ix = ClientIx::StageInit { kind: KIND_UPDATE, header: Some(f3.header.clone()), ring_hash: [0; 32], block_id: c3.block_id.clone(), round: c3.round(), n_slots: keys.len() as u16, message_hash: [0; 32], next_set_len: 0, next_set_hash: [0; 32], proof: None, leaf_len: 0 };
    line("client_stage_init_update_487883", &borsh::to_vec(&ix).unwrap());
    let (c2, _) = f2.commit();
    // an announcing update names its next set's length and hash (S-02); the set itself follows in chunks
    let ix = ClientIx::StageInit { kind: KIND_UPDATE, header: Some(f2.header.clone()), ring_hash: [0; 32], block_id: c2.block_id.clone(), round: c2.round(), n_slots: f2.validators.len() as u16, message_hash: [0; 32], next_set_len: f3.validators.len() as u16, next_set_hash: f3.validators.hash(sha256).unwrap(), proof: None, leaf_len: 0 };
    line("client_stage_init_announcing_487882", &borsh::to_vec(&ix).unwrap());
    // M2j: the client's Reset (7) is gone, so StageNextSet is 9, Grow 8 and SetAdmin 7
    line("client_stage_next_set_487883", &borsh::to_vec(&ClientIx::StageNextSet { offset: 0, items: f3.validators.validators.clone() }).unwrap());
    line("client_stage_precommits_487883", &borsh::to_vec(&ClientIx::StagePrecommits { offset: 0, items: items.clone() }).unwrap());
    line("client_stage_verify_012", &borsh::to_vec(&ClientIx::StageVerify { ix_index: 0, slots: vec![0, 1, 2] }).unwrap());
    line("client_update_finish", &borsh::to_vec(&ClientIx::UpdateFinish).unwrap());
    line("client_stage_close", &borsh::to_vec(&ClientIx::StageClose).unwrap());
    line("client_grow", &borsh::to_vec(&ClientIx::Grow { body_cap: 0 }).unwrap());
    line("client_grow_8192", &borsh::to_vec(&ClientIx::Grow { body_cap: 8192 }).unwrap());
    let entries: Vec<ed25519::Entry> = present[..2].iter().map(|&i| ed25519::Entry { pubkey: keys[i], signature: s3[i].unwrap(), message: c3.sign_bytes("gnoland-1", f3.height, i).unwrap() }).collect();
    line("ed25519_487883_first_two", &ed25519::instruction_data(&entries));
    let leaves = fr.results.as_ref().unwrap();
    let refs: Vec<&[u8]> = leaves.iter().map(|l| l.as_slice()).collect();
    let proof = merkle::simple_proof(sha256, &refs, 0).unwrap();
    let message = b"a message".to_vec();
    let ix = ClientIx::StageInit { kind: KIND_PROOF, header: None, ring_hash: [7; 32], block_id: Default::default(), round: 0, n_slots: 0, message_hash: sha256(&[&message]), next_set_len: 0, next_set_hash: [0; 32], proof: Some(proof.clone()), leaf_len: leaves[0].len() as u32 };
    line("client_stage_init_proof_ring_740913", &borsh::to_vec(&ix).unwrap());
    let ix = ClientIx::StageInit { kind: KIND_PROOF, header: Some(f3.header.clone()), ring_hash: [0; 32], block_id: c3.block_id.clone(), round: c3.round(), n_slots: keys.len() as u16, message_hash: sha256(&[&message]), next_set_len: 0, next_set_hash: [0; 32], proof: Some(proof.clone()), leaf_len: leaves[0].len() as u32 };
    line("client_stage_init_proof_header_487883", &borsh::to_vec(&ix).unwrap());
    line("client_stage_leaf", &borsh::to_vec(&ClientIx::StageLeaf { chunk: leaves[0][..100].to_vec() }).unwrap());
    let ix = ClientIx::Init { chain_id: "gnoland-1".into(), origin_domain: 1198419788, trusted_height: f1.height as u64, trusted_hash: f1.block_hash, validators: f1.validators.validators.clone(), admin: ADMIN };
    line("client_init_487881", &borsh::to_vec(&ix).unwrap());
    line("client_set_admin", &borsh::to_vec(&ClientIx::SetAdmin { new_admin: Pubkey::new_from_array([6; 32]) }).unwrap());
    line("client_state_pda", Pubkey::find_program_address(&[sol_util::client_head::STATE_SEED], &CLIENT).0.as_ref());
    line("client_update_stage_pda", Pubkey::find_program_address(&[UPDATE_SEED, &[4; 32], &[9; 32]], &CLIENT).0.as_ref());
    line("client_proof_stage_pda", Pubkey::find_program_address(&[PROOF_SEED, &[4; 32], &sha256(&[&message])], &CLIENT).0.as_ref());

    // the pausable ISM
    let seats = [Pubkey::new_from_array([11; 32]), Pubkey::new_from_array([12; 32]), Pubkey::new_from_array([13; 32]), Pubkey::new_from_array([14; 32])];
    let watch = [Pubkey::new_from_array([15; 32]), Pubkey::new_from_array([16; 32])];
    let ix = PausableIx::Init { owner: Pubkey::new_from_array([1; 32]), target: CLIENT, target_version: 1, seats, watch };
    line("pausable_init", &borsh::to_vec(&ix).unwrap());
    line("pausable_set_flag", &borsh::to_vec(&PausableIx::SetFlag).unwrap());
    line("pausable_clear_flag", &borsh::to_vec(&PausableIx::ClearFlag).unwrap());
    line("pausable_unpause_777", &borsh::to_vec(&PausableIx::Unpause { before_slot: 777 }).unwrap());
    line("pausable_set_guardians", &borsh::to_vec(&PausableIx::SetGuardians { pairs: vec![(3, Pubkey::new_from_array([7; 32])), (6, Pubkey::new_from_array([8; 32]))] }).unwrap());
    // discriminator 5 is SetLayoutVersion, refused outright since v27 (LayoutFixed) and kept in the enum only so
    // TransferOwnership stays 6; the Go side carries no builder for it, so no vector pins it
    line("pausable_transfer", &borsh::to_vec(&PausableIx::TransferOwnership { new_owner: Pubkey::new_from_array([6; 32]) }).unwrap());
    // kind 12's owner-only setter (AN5)
    line("pausable_set_mint_limits", &borsh::to_vec(&PausableIx::SetMintLimits { token: 1, max: 7_000_000_000, day_cap: 9_000_000_000 }).unwrap());
    line("pausable_flags_pda", Pubkey::find_program_address(&[pausable_ism::config::FLAGS_SEED], &PAUSABLE).0.as_ref());
    line("pausable_state_pda", Pubkey::find_program_address(&[pausable_ism::config::STATE_SEED], &PAUSABLE).0.as_ref());

    // the admin program (M2j: one channel — no recovery instructions, no channel byte in Execute, StageUpgrade,
    // CloseCancelled or a decision's PDA; CloseCancelled is 7 and SuccessorInit 8)
    // the launch init starts the sequence at 0; a fresh deployment under the same r/hlgov starts at r/hlgov's
    // number (AT3: `last_seq`, the last field)
    let ix = AdminIx::Init { seats, watch, origin_domain: 1198419788, predecessor: Pubkey::default(), last_seq: 0 };
    line("admin_init", &borsh::to_vec(&ix).unwrap());
    let ix = AdminIx::Init { seats, watch, origin_domain: 1198419788, predecessor: Pubkey::default(), last_seq: 37 };
    line("admin_init_fresh_37", &borsh::to_vec(&ix).unwrap());
    // a successor's init: zero seats, watch slots and number, its predecessor named (C227)
    let ix = AdminIx::Init { seats: [Pubkey::default(); 4], watch: [Pubkey::default(); 2], origin_domain: 1198419788, predecessor: ADMIN, last_seq: 0 };
    line("admin_init_successor", &borsh::to_vec(&ix).unwrap());
    line("admin_transfer_authority_in", &borsh::to_vec(&AdminIx::TransferAuthorityIn).unwrap());
    line("admin_finish_setup", &borsh::to_vec(&AdminIx::FinishSetup).unwrap());
    line("admin_execute_5", &borsh::to_vec(&AdminIx::Execute { number: 5 }).unwrap());
    line("admin_stage_upgrade_2", &borsh::to_vec(&AdminIx::StageUpgrade { number: 2 }).unwrap());
    line("admin_set_flag", &borsh::to_vec(&AdminIx::SetFlag).unwrap());
    line("admin_clear_flag", &borsh::to_vec(&AdminIx::ClearFlag).unwrap());
    line("admin_close_cancelled_9", &borsh::to_vec(&AdminIx::CloseCancelled { number: 9 }).unwrap());
    // the hand-over's wire contract (S-21, M2j's form): sent by the predecessor's kind 9, never by a tool, but
    // hlwatch reads a successor's state against the same layout
    let ix = AdminIx::SuccessorInit { origin_domain: 1198419788, last_seq: 300, seats, watch, holds: vec![Hold { start: 1_700_000_000, end: 1_700_100_000 }] };
    line("admin_successor_init", &borsh::to_vec(&ix).unwrap());
    // a kind 8's growth is the loader's own ExtendProgram (tag 6, one 10 KB step), sent top-level by the executor (F1)
    line("loader_extend_program_step", &sol_util::loader::extend_program(&PAUSABLE, &Pubkey::new_from_array([4; 32]), sol_util::loader::EXTEND_STEP).data);
    line("admin_state_pda", Pubkey::find_program_address(&[STATE_SEED], &ADMIN).0.as_ref());
    line("admin_authority_pda", Pubkey::find_program_address(&[AUTHORITY_SEED], &ADMIN).0.as_ref());
    line("admin_decision_5_pda", Decision::address(&ADMIN, 5).0.as_ref());
    line("admin_decision_300_pda", Decision::address(&ADMIN, 300).0.as_ref());

    // bodies (kinds.rs::body), as r/hlgov writes them; a cancel's starts with its salt (v27 §13) and keeps its
    // channel byte, 0 (M2j: the one channel); kind 4 is reserved (no body: the program spends whatever it
    // carries), and kinds 10 and 11 are unknown (recorded and spent at recording)
    line("body_cancel_range", &kinds::body(1, 0, &Params::Cancel { salt: 0x1122334455667788, ranges: vec![(2, 9)] }));
    line("body_cancel_list", &kinds::body(1, 4, &Params::Cancel { salt: 0x99aabbccddeeff00, ranges: vec![(2, 2), (5, 5)] }));
    line("body_unpause_1", &kinds::body(2, 5, &Params::Unpause(1)));
    line("body_token_ism", &kinds::body(3, 6, &Params::SetTokenIsm { token: 1, ism: PAUSABLE }));
    line("body_guardians", &kinds::body(5, 8, &Params::SetGuardians(vec![(3, Pubkey::new_from_array([7; 32])), (6, Pubkey::new_from_array([8; 32]))])));
    line("body_router", &kinds::body(6, 9, &Params::EnrollRouter { token: 1, domain: 1198419788, router: [0xab; 32] }));
    line("body_fee", &kinds::body(7, 3, &Params::SetFee { lamports: 1000, beneficiary: Pubkey::new_from_array([9; 32]) }));
    line("body_upgrade", &kinds::body(8, 10, &Params::Upgrade { program: PAUSABLE, buffer: Pubkey::new_from_array([0x88; 32]), hash: [0x77; 32] }));
    line("body_transfer", &kinds::body(9, 11, &Params::Transfer { program: Pubkey::new_from_array([0x99; 32]) }));
    line("body_mint_limits", &kinds::body(12, 12, &Params::SetMintLimits { token: 1, max: 7_000_000_000, day_cap: 9_000_000_000 }));

    // sanity: the header hash and a sign bytes the Go side already checks
    line("header_hash_487883", &f3.header.hash(sha256));
    line("sign_bytes_487883_slot0", &c3.sign_bytes("gnoland-1", f3.height, present[0]).unwrap());
}

#![allow(deprecated)]
//! The pausable ISM under program-test, in front of the real client: `cargo test-sbf -p pausable-ism` after
//! `cargo build-sbf` of the client (both .so files are loaded).

use gno_light_client_ism::{config::MAILBOX_PKG_PATH, ID as CLIENT};
use gno_light_core::{sha256, testkit};
use pausable_ism::{
    config::CLOCK_FACTOR,
    error::PausableError,
    state::{Flags, State},
    ID,
};
use sol_util::{clock, hl};
use solana_sdk::{
    instruction::AccountMeta,
    signature::{Keypair, Signer},
    transaction::Transaction,
};
use testlib::{client as c, custom_code, keypair, pausable as p, tokio, Harness, ProgramSpec};

const CHAIN: &str = "gno-test-1";
const ORIGIN: u32 = 1198419796;
const DESTINATION: u32 = 1399811151;

fn code(e: PausableError) -> Option<u32> {
    Some(2000 + e as u32)
}

struct World {
    h: Harness,
    authority: Keypair,
    owner: Keypair,
    seats: Vec<Keypair>,
    watch: Vec<Keypair>,
}

async fn world() -> World {
    let authority = keypair(1);
    let h = Harness::new(
        &[
            ProgramSpec { name: "gno_light_client_ism", id: CLIENT, authority: Some(authority.pubkey()), path: None },
            ProgramSpec { name: "pausable_ism", id: ID, authority: Some(authority.pubkey()), path: None },
        ],
        vec![],
    )
    .await;
    let owner = keypair(2);
    let seats: Vec<Keypair> = (10..14).map(keypair).collect();
    let watch: Vec<Keypair> = (14..16).map(keypair).collect();
    let mut w = World { h, authority, owner, seats, watch };
    for k in w.seats.iter().chain(w.watch.iter()).chain(std::iter::once(&w.owner)) {
        w.h.fund(&k.pubkey(), 100_000_000).await;
    }
    let payer = w.h.payer();
    let seat_keys = [w.seats[0].pubkey(), w.seats[1].pubkey(), w.seats[2].pubkey(), w.seats[3].pubkey()];
    let watch_keys = [w.watch[0].pubkey(), w.watch[1].pubkey()];
    // the layout version is 1 for the deployment's life (v27 §13): an init naming another is refused
    let e = w.h.send("pausable: init", &[p::init(&payer.pubkey(), &w.authority.pubkey(), w.owner.pubkey(), CLIENT, 2, seat_keys, watch_keys)], &[&w.authority]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::LayoutFixed));
    w.h.send("pausable: init", &[p::init(&payer.pubkey(), &w.authority.pubkey(), w.owner.pubkey(), CLIENT, 1, seat_keys, watch_keys)], &[&w.authority]).await.unwrap();
    w
}

async fn flags(h: &mut Harness) -> Flags {
    Flags::load(&h.data(&p::flags()).await).unwrap()
}

#[tokio::test]
async fn flags_holds_unpause_and_replacement() {
    let mut w = world().await;
    let s = State::load(&w.h.data(&p::state()).await).unwrap();
    assert_eq!((s.version, s.owner, s.target, s.target_version, s.clock_factor as i64), (1, w.owner.pubkey(), CLIENT, 1, CLOCK_FACTOR));
    // init runs once
    let payer = w.h.payer();
    let e = w.h.send("pausable: init", &[p::init(&payer.pubkey(), &w.authority.pubkey(), w.owner.pubkey(), CLIENT, 1, [w.seats[0].pubkey(); 4], [w.watch[0].pubkey(); 2])], &[&w.authority]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::AlreadyInitialized));
    // a stranger's flag
    let stranger = keypair(40);
    w.h.fund(&stranger.pubkey(), 10_000_000).await;
    let e = w.h.send("pausable: set_flag", &[p::set_flag(&stranger.pubkey())], &[&stranger]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::UnknownKey));
    // seat 1 and watch 5 flag; the owner's unpause (before a later slot) clears both; a hold refuses it
    w.h.send("pausable: set_flag", &[p::set_flag(&w.seats[0].pubkey())], &[&w.seats[0]]).await.unwrap();
    w.h.send("pausable: set_flag", &[p::set_flag(&w.watch[0].pubkey())], &[&w.watch[0]]).await.unwrap();
    let f = flags(&mut w.h).await;
    assert!(f.guardians.flags[0].is_some() && f.guardians.flags[4].is_some());
    assert_eq!(f.guardians.flags[4].unwrap().slot, w.h.slot, "a flag carries the slot it was set at");
    assert_eq!(f.guardians.flags[0].unwrap().slot, w.h.slot - 1);
    w.h.advance(10).await;
    let e = w.h.send("pausable: unpause", &[p::unpause(&stranger.pubkey(), w.h.slot)], &[&stranger]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::NotOwner));
    w.h.send("pausable: unpause", &[p::unpause(&w.owner.pubkey(), w.h.slot)], &[&w.owner]).await.unwrap();
    assert!(!flags(&mut w.h).await.guardians.any_flag());
    // two guardians: a hold
    w.h.send("pausable: set_flag", &[p::set_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.advance(5).await;
    w.h.send("pausable: set_flag", &[p::set_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    let f = flags(&mut w.h).await;
    let grace = clock::unpause_delay(CLOCK_FACTOR);
    let since = f.guardians.hold(w.h.time, grace).unwrap();
    assert_eq!(since.slot, w.h.slot, "the hold began when the second guardian flag landed");
    w.h.advance(5).await;
    let e = w.h.send("pausable: unpause", &[p::unpause(&w.owner.pubkey(), w.h.slot)], &[&w.owner]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::Hold));
    // a key clears its own (a dip) and re-sets within the grace: a fresh flag slot, the hold's start unchanged
    w.h.send("pausable: clear_flag", &[p::clear_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    let f = flags(&mut w.h).await;
    assert_eq!(f.guardians.hold(w.h.time, grace), Some(since), "a dip shorter than the grace keeps the hold");
    assert_eq!(f.guardians.dip_since.unwrap().slot, w.h.slot);
    w.h.advance(100).await;
    w.h.send("pausable: set_flag", &[p::set_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    let f = flags(&mut w.h).await;
    assert_eq!(f.guardians.flags[3].unwrap().slot, w.h.slot);
    assert_eq!(f.guardians.hold_since, Some(since));
    assert!(f.guardians.dip_since.is_none());
    // three keys taking turns never move the start
    w.h.send("pausable: set_flag", &[p::set_flag(&w.seats[1].pubkey())], &[&w.seats[1]]).await.unwrap();
    w.h.send("pausable: clear_flag", &[p::clear_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.send("pausable: set_flag", &[p::set_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.send("pausable: clear_flag", &[p::clear_flag(&w.seats[1].pubkey())], &[&w.seats[1]]).await.unwrap();
    assert_eq!(flags(&mut w.h).await.guardians.hold_since, Some(since));
    // a dip of the grace's length ends the hold: the unpause goes through
    w.h.send("pausable: clear_flag", &[p::clear_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    w.h.advance(grace - 60).await;
    let e = w.h.send("pausable: unpause", &[p::unpause(&w.owner.pubkey(), w.h.slot)], &[&w.owner]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::Hold));
    w.h.advance(120).await;
    w.h.send("pausable: unpause", &[p::unpause(&w.owner.pubkey(), w.h.slot)], &[&w.owner]).await.unwrap();
    let f = flags(&mut w.h).await;
    assert!(!f.guardians.any_flag() && f.guardians.hold_since.is_none());
    // the hold again, for the replacement
    w.h.send("pausable: set_flag", &[p::set_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.send("pausable: set_flag", &[p::set_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    assert!(flags(&mut w.h).await.guardians.hold(w.h.time, grace).is_some());
    // the replacement: a seat's current key refused, three guardians refused, two plus a watch slot land through
    // the hold and clear the replaced keys' flags
    let n = |i: u8| keypair(50 + i).pubkey();
    let e = w.h.send("pausable: set_guardians", &[p::set_guardians(&w.owner.pubkey(), vec![(3, w.seats[2].pubkey())])], &[&w.owner]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::CurrentKey));
    let e = w.h.send("pausable: set_guardians", &[p::set_guardians(&w.owner.pubkey(), vec![(1, n(1)), (2, n(2)), (3, n(3))])], &[&w.owner]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::TooManyGuardians));
    let e = w.h.send("pausable: set_guardians", &[p::set_guardians(&w.owner.pubkey(), vec![(3, w.seats[0].pubkey())])], &[&w.owner]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::DuplicateKey));
    w.h.send("pausable: set_guardians", &[p::set_guardians(&w.owner.pubkey(), vec![(3, n(3)), (4, n(4)), (5, n(5))])], &[&w.owner]).await.unwrap();
    let f = flags(&mut w.h).await;
    assert_eq!((f.guardians.keys[2], f.guardians.keys[3], f.guardians.keys[4]), (n(3), n(4), n(5)));
    assert!(!f.guardians.any_flag());
    assert!(f.guardians.hold_since.is_none() && f.guardians.hold(w.h.time, grace).is_none(), "the replacement ends the hold at once");
    // the old key is unknown now
    let e = w.h.send("pausable: set_flag", &[p::set_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::UnknownKey));
    // the layout version never moves (v27 §13: fixed at 1 for the deployment's life), whoever asks and whatever
    // the arguments; the owner moves, and only the new owner's unpause goes through
    for (target, version) in [(CLIENT, 2), (CLIENT, 1), (ID, 2)] {
        let e = w.h.send("pausable: set_layout_version", &[p::set_layout_version(&w.owner.pubkey(), target, version)], &[&w.owner]).await.unwrap_err();
        assert_eq!(custom_code(&e), code(PausableError::LayoutFixed));
    }
    assert_eq!(State::load(&w.h.data(&p::state()).await).unwrap().target_version, 1);
    let new_owner = keypair(60);
    w.h.fund(&new_owner.pubkey(), 10_000_000).await;
    w.h.send("pausable: transfer_ownership", &[p::transfer_ownership(&w.owner.pubkey(), new_owner.pubkey())], &[&w.owner]).await.unwrap();
    let e = w.h.send("pausable: unpause", &[p::unpause(&w.owner.pubkey(), w.h.slot)], &[&w.owner]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::NotOwner));
    w.h.send("pausable: unpause", &[p::unpause(&new_owner.pubkey(), w.h.slot)], &[&new_owner]).await.unwrap();
    w.h.report("pausable flags");
}

#[tokio::test]
async fn verify_goes_through_only_when_nothing_stands() {
    let mut w = world().await;
    let payer = w.h.payer();
    // the client with a synthetic chain and a dispatch proven by the ring
    let vals = testkit::validators(7, &[60, 60, 60, 60]);
    let set = testkit::set_of(&vals);
    let keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
    let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &set, &set, None, [0; 32]);
    w.h.send("client: init", &[c::init(&payer.pubkey(), &w.authority.pubkey(), CHAIN, ORIGIN, 100, h100.hash(sha256), set.validators.clone(), keypair(3).pubkey())], &[&w.authority]).await.unwrap();
    for _ in 0..3 {
        w.h.send("client: grow", &[c::grow(&payer.pubkey())], &[]).await.unwrap();
    }
    let submitter = keypair(4);
    w.h.fund(&submitter.pubkey(), 2_000_000_000).await;
    let sender = [0xaa; 32];
    let recipient = [0xbb; 32];
    let message = hl::encode_message(7, ORIGIN, &sender, DESTINATION, &recipient, b"hello");
    let d = gno_light_core::results::Dispatch { id: hl::Message::id(&message), nonce: 7, origin: ORIGIN, destination: DESTINATION, sender, recipient, hash: sha256(&[&message]) };
    let leaves = testkit::dispatch_leaves(MAILBOX_PKG_PATH, &[d], 1);
    let (root, proof) = testkit::results_proof(&leaves, 0);
    let h101 = testkit::header(CHAIN, 101, 1_700_000_010, &set, &set, Some(root), h100.hash(sha256));
    let (c101, s101) = testkit::sign(CHAIN, &h101, &vals, &[0, 1, 2, 3]);
    for tx in c::update_txs(&submitter.pubkey(), CHAIN, &h101, &c101, &keys, &s101, None, 3) {
        w.h.send("client: update", &tx, &[&submitter]).await.unwrap();
    }
    let empty = gno_light_core::commit::Commit::default();
    for tx in c::proof_txs(&submitter.pubkey(), CHAIN, None, h101.hash(sha256), &empty, &keys, &[], &message, proof.clone(), &leaves[0], 3) {
        w.h.send("client: stage proof", &tx, &[&submitter]).await.unwrap();
    }
    let stage = c::proof_stage(&submitter.pubkey(), &sha256(&[&message]));
    let client_accounts = vec![AccountMeta::new_readonly(stage, false)];
    let metadata = submitter.pubkey().to_bytes().to_vec();
    let verify = || p::verify(&CLIENT, &c::state(), client_accounts.clone(), metadata.clone(), message.clone());

    // the account metas: five of the ISM's own — the flags, the state (writable: the mint bucket), the client's
    // state, the client program, the Instructions sysvar — then the client's (its state not repeated)
    let blockhash = w.h.ctx.banks_client.get_latest_blockhash().await.unwrap();
    let tx = Transaction::new_signed_with_payer(&[p::verify_account_metas(Some(&CLIENT), metadata.clone(), message.clone())], Some(&payer.pubkey()), &[&payer], blockhash);
    let sim = w.h.ctx.banks_client.simulate_transaction(tx).await.unwrap();
    let rd = sim.simulation_details.unwrap().return_data.unwrap().data;
    assert_eq!(rd.len(), 4 + 6 * 34 + 1, "flags, state, client state, client program, the Instructions sysvar, the proof");
    assert_eq!(&rd[4..36], p::flags().as_ref());
    assert_eq!(&rd[38..70], p::state().as_ref());
    assert_eq!(rd[70..72], [0, 1], "the state writable");
    assert_eq!(&rd[72..104], c::state().as_ref());
    assert_eq!(rd[104..106], [0, 1]);
    assert_eq!(&rd[106..138], CLIENT.as_ref());
    assert_eq!(&rd[140..172], solana_sdk::sysvar::instructions::ID.as_ref());
    assert_eq!(rd[172..174], [0, 0]);
    assert_eq!(&rd[174..206], stage.as_ref());
    // Type forwarded
    let blockhash = w.h.ctx.banks_client.get_latest_blockhash().await.unwrap();
    let tx = Transaction::new_signed_with_payer(&[p::ism_type(&CLIENT)], Some(&payer.pubkey()), &[&payer], blockhash);
    let sim = w.h.ctx.banks_client.simulate_transaction(tx).await.unwrap();
    assert_eq!(sim.simulation_details.unwrap().return_data.unwrap().data, vec![0, 0, 0, 0, 0xff]);

    // Verify called directly — by anyone but the mailbox's inbox_process, whose per-ISM process authority alone
    // signs it (AN5) — is refused whatever stands; the deliveries themselves, the flags, the quarantine and the
    // client's accounts are tested through the mailbox in tests/limits.rs
    let e = w.h.send("pausable: verify (direct)", &[verify()], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::NotTheMailbox));
    // the wrong client program is refused before anything else
    let wrong_program = p::verify(&ID, &c::state(), client_accounts.clone(), metadata.clone(), message.clone());
    let e = w.h.send("pausable: verify", &[wrong_program], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::WrongTarget));
    let _ = (h101, keys, vals, set);
    w.h.report("pausable verify");
}

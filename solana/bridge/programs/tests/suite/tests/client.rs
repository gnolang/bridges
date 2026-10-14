#![allow(deprecated)]
//! The light-client ISM under program-test: `cargo test-sbf -p gno-light-client-ism` (the .so built by
//! cargo-build-sbf, so the compute units are real).

use gno_light_client_ism::{
    config::{CLOCK_FACTOR, MAILBOX_PKG_PATH, MAX_BODY_CAP},
    error::ClientError,
    stage::{Stage, KIND_UPDATE},
    state::{full_len, Body, Head, Ring, HEAD_LEN, RING_ENTRY},
    ID,
};
use gno_light_core::{
    fixtures, sha256,
    testkit,
    valset::ValidatorSet,
};
use sol_util::{clock, hl};
use solana_sdk::signature::{Keypair, Signer};
use testlib::{client as c, custom_code, keypair, tokio, Harness, ProgramSpec};

const CHAIN: &str = "gno-test-1";
const ORIGIN: u32 = 1198419796;
const DESTINATION: u32 = 1399811151;

async fn harness() -> (Harness, Keypair) {
    let authority = keypair(1);
    let h = Harness::new(&[ProgramSpec { name: "gno_light_client_ism", id: ID, authority: Some(authority.pubkey()), path: None }], vec![]).await;
    (h, authority)
}

async fn head(h: &mut Harness) -> Head {
    Head::read(&h.data(&c::state()).await).unwrap()
}

async fn body(h: &mut Harness) -> Body {
    *Body::load(&h.data(&c::state()).await).unwrap()
}

fn code(e: ClientError) -> Option<u32> {
    Some(1000 + e as u32)
}

/// Grows the state account until its ring holds 256 entries (three calls at 10 KB each).
async fn grow(h: &mut Harness) {
    let payer = h.payer();
    for _ in 0..3 {
        h.send("grow", &[c::grow(&payer.pubkey())], &[]).await.unwrap();
    }
}

/// Stages and finishes an update, returning the last transaction's result.
async fn update(h: &mut Harness, submitter: &Keypair, chain: &str, header: &gno_light_core::header::Header, commit: &gno_light_core::commit::Commit, pubkeys: &[[u8; 32]], sigs: &[Option<[u8; 64]>], next: Option<Vec<gno_light_core::valset::Validator>>, label: &str) -> Result<(), solana_sdk::transaction::TransactionError> {
    let txs = c::update_txs(&submitter.pubkey(), chain, header, commit, pubkeys, sigs, next, 3);
    let n = txs.len();
    for (i, tx) in txs.into_iter().enumerate() {
        let l = if i == 0 {
            "update: stage_init+precommits".to_string()
        } else if i == n - 1 {
            format!("update: finish ({label})")
        } else if tx[0].program_id == solana_sdk::ed25519_program::ID {
            format!("update: precompile+verify x{}", tx[0].data[0])
        } else {
            "update: stage_next_set (chunk)".to_string()
        };
        h.send(&l, &tx, &[submitter]).await?;
    }
    Ok(())
}

#[tokio::test]
async fn init_needs_the_upgrade_authority_and_runs_once() {
    let (mut h, authority) = harness().await;
    let vals = testkit::validators(7, &[60, 60, 60, 60]);
    let set = testkit::set_of(&vals);
    let payer = h.payer();
    let stranger = keypair(2);
    let ix = c::init(&payer.pubkey(), &stranger.pubkey(), CHAIN, ORIGIN, 100, [1; 32], set.validators.clone(), keypair(3).pubkey());
    let e = h.send("init", &[ix], &[&stranger]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::WrongAuthority));
    let ix = c::init(&payer.pubkey(), &authority.pubkey(), CHAIN, ORIGIN, 100, [1; 32], set.validators.clone(), keypair(3).pubkey());
    h.send("init", &[ix.clone()], &[&authority]).await.unwrap();
    grow(&mut h).await;
    let hd = head(&mut h).await;
    assert_eq!((hd.version, hd.clock_factor as i64, hd.trusted_height, hd.quarantine_end), (1, CLOCK_FACTOR, 100, 0));
    assert_eq!(gno_light_client_ism::state::Ring::capacity(&hd, h.data(&c::state()).await.len()), 256);
    assert_eq!(hd.last_accepted_time, h.time);
    let b = body(&mut h).await;
    assert_eq!(b.set, set);
    assert_eq!(b.history.len(), 1);
    assert_eq!(b.records, 1);
    h.advance(1).await;
    let e = h.send("init", &[ix], &[&authority]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::AlreadyInitialized));
    h.report("client init");
}

#[tokio::test]
async fn an_update_is_accepted_and_a_forged_header_refused() {
    let (mut h, authority) = harness().await;
    let vals = testkit::validators(7, &[60, 60, 60, 60]);
    let set = testkit::set_of(&vals);
    let keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
    let payer = h.payer();
    let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &set, &set, None, [0; 32]);
    let hash100 = h100.hash(sha256);
    h.send("init", &[c::init(&payer.pubkey(), &authority.pubkey(), CHAIN, ORIGIN, 100, hash100, set.validators.clone(), keypair(3).pubkey())], &[&authority]).await.unwrap();
    grow(&mut h).await;
    let submitter = keypair(4);
    h.fund(&submitter.pubkey(), 1_000_000_000).await;

    // 101 signed by all four
    let h101 = testkit::header(CHAIN, 101, 1_700_000_010, &set, &set, None, hash100);
    let (c101, s101) = testkit::sign(CHAIN, &h101, &vals, &[0, 1, 2, 3]);
    let before = h.lamports(&submitter.pubkey()).await;
    update(&mut h, &submitter, CHAIN, &h101, &c101, &keys, &s101, None, "4 of 4").await.unwrap();
    let hd = head(&mut h).await;
    assert_eq!(hd.trusted_height, 101);
    assert_eq!(hd.trusted_hash, h101.hash(sha256));
    assert_eq!(hd.ring_count, 1);
    let b = body(&mut h).await;
    assert_eq!(b.records, 2);
    assert_eq!(b.trusted.height, 101);
    // the stage is closed and its rent came back (minus fees)
    assert!(h.account(&c::update_stage(&submitter.pubkey(), &h101.hash(sha256))).await.is_none());
    assert!(h.lamports(&submitter.pubkey()).await + 100_000 > before, "rent returned");

    // 102 signed by two of four: 120 of 240 is not more than two thirds
    let h102 = testkit::header(CHAIN, 102, 1_700_000_020, &set, &set, None, h101.hash(sha256));
    let (c2, s2) = testkit::sign(CHAIN, &h102, &vals, &[0, 1]);
    let e = update(&mut h, &submitter, CHAIN, &h102, &c2, &keys, &s2, None, "2 of 4").await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::NotEnoughPower));
    // the abandoned stage is closed by its submitter
    h.send("stage_close", &[c::stage_close(&c::update_stage(&submitter.pubkey(), &h102.hash(sha256)), &submitter.pubkey())], &[&submitter]).await.unwrap();
    // three of four carry it
    let (c3, s3) = testkit::sign(CHAIN, &h102, &vals, &[1, 2, 3]);
    update(&mut h, &submitter, CHAIN, &h102, &c3, &keys, &s3, None, "3 of 4").await.unwrap();
    assert_eq!(head(&mut h).await.trusted_height, 102);

    // a forged 103: the right ValidatorsHash, signed by someone else's keys
    let forgers = testkit::validators(9, &[60, 60, 60, 60]);
    let h103 = testkit::header(CHAIN, 103, 1_700_000_030, &set, &set, None, h102.hash(sha256));
    let (cf, sf) = testkit::sign(CHAIN, &h103, &forgers, &[0, 1, 2, 3]);
    let forged_keys: Vec<[u8; 32]> = forgers.iter().map(|v| v.pubkey()).collect();
    let e = update(&mut h, &submitter, CHAIN, &h103, &cf, &forged_keys, &sf, None, "forged").await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::SignatureMismatch));
    h.send("stage_close", &[c::stage_close(&c::update_stage(&submitter.pubkey(), &h103.hash(sha256)), &submitter.pubkey())], &[&submitter]).await.unwrap();
    // the real keys' signatures presented under the real pubkeys but over other bytes: the precompile fails the
    // transaction before the program runs
    let (cr, sr) = testkit::sign(CHAIN, &h103, &vals, &[0, 1, 2, 3]);
    let mut bad = sr.clone();
    bad[0].as_mut().unwrap()[5] ^= 1;
    let e = update(&mut h, &submitter, CHAIN, &h103, &cr, &keys, &bad, None, "bad sig").await.unwrap_err();
    assert_eq!(custom_code(&e), Some(2), "the precompile refused the signature (InvalidSignature) before the program ran: {e:?}");
    h.send("stage_close", &[c::stage_close(&c::update_stage(&submitter.pubkey(), &h103.hash(sha256)), &submitter.pubkey())], &[&submitter]).await.unwrap();
    // a header whose ValidatorsHash is another set's is refused at staging
    let other = testkit::set_of(&forgers);
    let hx = testkit::header(CHAIN, 103, 1_700_000_030, &other, &other, None, h102.hash(sha256));
    let (cx, sx) = testkit::sign(CHAIN, &hx, &forgers, &[0, 1, 2, 3]);
    let e = update(&mut h, &submitter, CHAIN, &hx, &cx, &forged_keys, &sx, None, "other set").await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::ValidatorsHash));
    // a height at or below the trusted one
    let e = update(&mut h, &submitter, CHAIN, &h102, &c3, &keys, &s3, None, "replay").await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::HeightNotAboveTrusted));
    // another chain id
    let hc = testkit::header("gno-other", 103, 1_700_000_030, &set, &set, None, h102.hash(sha256));
    let (cc, sc) = testkit::sign("gno-other", &hc, &vals, &[0, 1, 2, 3]);
    let e = update(&mut h, &submitter, "gno-other", &hc, &cc, &keys, &sc, None, "chain").await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::ChainId));
    // the honest 103 lands
    update(&mut h, &submitter, CHAIN, &h103, &cr, &keys, &sr, None, "4 of 4").await.unwrap();
    assert_eq!(head(&mut h).await.trusted_height, 103);
    assert_eq!(body(&mut h).await.records, 4);
    h.report("client updates");
}

#[tokio::test]
async fn the_real_chain_replays_its_three_set_changes() {
    let (mut h, authority) = harness().await;
    let fx = fixtures::load_dir(fixtures::dir());
    let first = &fx[0];
    assert_eq!(first.height, 487881);
    let payer = h.payer();
    h.send("init", &[c::init(&payer.pubkey(), &authority.pubkey(), "gnoland-1", 1198419788, first.height as u64, first.block_hash, first.validators.validators.clone(), keypair(3).pubkey())], &[&authority]).await.unwrap();
    grow(&mut h).await;
    let submitter = keypair(4);
    h.fund(&submitter.pubkey(), 5_000_000_000).await;

    // the announcing header must be presented: 487,883 straight from 487,881 is refused
    let f3 = &fx[2];
    assert_eq!(f3.height, 487883);
    let (c3, s3) = f3.commit();
    let k3: Vec<[u8; 32]> = f3.validators.validators.iter().map(|v| v.pubkey).collect();
    let e = update(&mut h, &submitter, "gnoland-1", &f3.header, &c3, &k3, &s3, None, "skip").await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::ValidatorsHash));

    let mut announcements = 0;
    let mut quarantine_ends = Vec::new();
    for i in 1..fx.len() {
        let f = &fx[i];
        let (commit, sigs) = f.commit();
        let keys: Vec<[u8; 32]> = f.validators.validators.iter().map(|v| v.pubkey).collect();
        let announcing = f.header.validators_hash != f.header.next_validators_hash;
        let next = if announcing { Some(fx[i + 1].validators.validators.clone()) } else { None };
        h.advance(60).await;
        let label = if announcing { "announcing" } else if f.validators.len() == 6 { "6 validators" } else { "5 validators" };
        update(&mut h, &submitter, "gnoland-1", &f.header, &commit, &keys, &sigs, next, label).await.unwrap_or_else(|e| panic!("height {}: {e:?}", f.height));
        let hd = head(&mut h).await;
        let b = body(&mut h).await;
        assert_eq!(hd.trusted_height as i64, f.height);
        assert_eq!(hd.trusted_hash, f.block_hash);
        assert_eq!(b.set, f.validators, "the set of the trusted height");
        if announcing {
            announcements += 1;
            assert!(b.next_set.is_some());
            assert_eq!(b.next_set.as_ref().unwrap(), &fx[i + 1].validators);
            assert_eq!(hd.quarantine_end, h.time + clock::week(CLOCK_FACTOR), "the quarantine starts at the announcing update");
            quarantine_ends.push(hd.quarantine_end);
            assert_eq!(b.history.last().unwrap().from_height, f.height + 1);
        } else {
            assert!(b.next_set.is_none());
        }
        if i >= 1 && fx[i - 1].header.validators_hash != f.header.validators_hash {
            assert_eq!(hd.ring_count, 1, "the ring is cleared at a set change");
        }
    }
    assert_eq!(announcements, 3);
    let b = body(&mut h).await;
    assert_eq!(b.records as usize, fx.len());
    assert_eq!(b.trusted.height, fx.last().unwrap().height);
    assert_eq!(b.set.len(), 6);
    assert_eq!(b.history.len(), 4);
    assert_eq!(quarantine_ends.len(), 3);
    h.report("client: the real chain");
}

#[tokio::test]
async fn a_proof_is_verified_against_the_trusted_header() {
    let (mut h, authority) = harness().await;
    let vals = testkit::validators(7, &[60, 60, 60, 60]);
    let set = testkit::set_of(&vals);
    let keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
    let payer = h.payer();
    let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &set, &set, None, [0; 32]);
    h.send("init", &[c::init(&payer.pubkey(), &authority.pubkey(), CHAIN, ORIGIN, 100, h100.hash(sha256), set.validators.clone(), keypair(3).pubkey())], &[&authority]).await.unwrap();
    grow(&mut h).await;
    let submitter = keypair(4);
    h.fund(&submitter.pubkey(), 2_000_000_000).await;

    // block 101 carries a dispatch; header 102 commits to its results
    let sender = [0xaa; 32];
    let recipient = [0xbb; 32];
    let message = hl::encode_message(7, ORIGIN, &sender, DESTINATION, &recipient, b"hello");
    let d = gno_light_core::results::Dispatch { id: hl::Message::id(&message), nonce: 7, origin: ORIGIN, destination: DESTINATION, sender, recipient, hash: sha256(&[&message]) };
    let leaves = testkit::dispatch_leaves(MAILBOX_PKG_PATH, &[d], 2);
    let (root, proof) = testkit::results_proof(&leaves, 0);
    let h101 = testkit::header(CHAIN, 101, 1_700_000_010, &set, &set, None, h100.hash(sha256));
    let h102 = testkit::header(CHAIN, 102, 1_700_000_020, &set, &set, Some(root), h101.hash(sha256));
    for hh in [&h101, &h102] {
        let (cm, sg) = testkit::sign(CHAIN, hh, &vals, &[0, 1, 2, 3]);
        update(&mut h, &submitter, CHAIN, hh, &cm, &keys, &sg, None, "4 of 4").await.unwrap();
    }
    assert_eq!(head(&mut h).await.ring_count, 2);

    // by the ring: no signatures
    let (c102, s102) = testkit::sign(CHAIN, &h102, &vals, &[0, 1, 2, 3]);
    let empty = gno_light_core::commit::Commit::default();
    for tx in c::proof_txs(&submitter.pubkey(), CHAIN, None, h102.hash(sha256), &empty, &keys, &[], &message, proof.clone(), &leaves[0], 3) {
        h.send("proof: stage (ring)", &tx, &[&submitter]).await.unwrap();
    }
    let records = body(&mut h).await.records;
    h.send("proof: verify (ring)", &[c::verify(&submitter.pubkey(), &message)], &[]).await.unwrap();
    assert_eq!(body(&mut h).await.records, records, "a ring hit records nothing new");
    // the account metas a relayer is told
    let metas = c::verify_account_metas(&submitter.pubkey(), &message);
    let blockhash = h.ctx.banks_client.get_latest_blockhash().await.unwrap();
    let tx = solana_sdk::transaction::Transaction::new_signed_with_payer(&[metas], Some(&payer.pubkey()), &[&payer], blockhash);
    let sim = h.ctx.banks_client.simulate_transaction(tx).await.unwrap();
    let rd = sim.simulation_details.unwrap().return_data.unwrap().data;
    assert_eq!(rd.len(), 4 + 2 * 34 + 1);
    assert_eq!(&rd[4..36], c::state().as_ref());
    assert_eq!(rd[36..38], [0, 1]);
    assert_eq!(&rd[38..70], c::proof_stage(&submitter.pubkey(), &sha256(&[&message])).as_ref());
    assert_eq!(rd[70..72], [0, 0]);
    assert_eq!(rd[72], 0xff);
    // the submitter closes the proof and takes the rent back
    h.send("proof: close", &[c::stage_close(&c::proof_stage(&submitter.pubkey(), &sha256(&[&message])), &submitter.pubkey())], &[&submitter]).await.unwrap();

    // with the header and its commit (a header at or below the trusted height, signed by the current set)
    for tx in c::proof_txs(&submitter.pubkey(), CHAIN, Some(&h102), [0; 32], &c102, &keys, &s102, &message, proof.clone(), &leaves[0], 3) {
        h.send("proof: stage (header)", &tx, &[&submitter]).await.unwrap();
    }
    h.send("proof: verify (header)", &[c::verify(&submitter.pubkey(), &message)], &[]).await.unwrap();
    assert_eq!(body(&mut h).await.records, records, "a header the ring holds was recorded at its update: nothing new (S-10)");
    assert_eq!(head(&mut h).await.ring_count, 2);
    h.send("proof: close", &[c::stage_close(&c::proof_stage(&submitter.pubkey(), &sha256(&[&message])), &submitter.pubkey())], &[&submitter]).await.unwrap();

    // another message under the same leaf: the Dispatch does not match it
    let other = hl::encode_message(8, ORIGIN, &sender, DESTINATION, &recipient, b"hello");
    for tx in c::proof_txs(&submitter.pubkey(), CHAIN, None, h102.hash(sha256), &empty, &keys, &[], &other, proof.clone(), &leaves[0], 3) {
        h.send("proof: stage (ring)", &tx, &[&submitter]).await.unwrap();
    }
    let e = h.send("proof: verify", &[c::verify(&submitter.pubkey(), &other)], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::DispatchMismatch));
    // a wrong leaf under a right proof
    let mut wrong = leaves[0].clone();
    wrong.push(0);
    let stage = c::proof_stage(&submitter.pubkey(), &sha256(&[&other]));
    h.send("proof: close", &[c::stage_close(&stage, &submitter.pubkey())], &[&submitter]).await.unwrap();
    for tx in c::proof_txs(&submitter.pubkey(), CHAIN, None, h102.hash(sha256), &empty, &keys, &[], &other, proof.clone(), &wrong, 3) {
        h.send("proof: stage (ring)", &tx, &[&submitter]).await.unwrap();
    }
    let e = h.send("proof: verify", &[c::verify(&submitter.pubkey(), &other)], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::ProofMismatch));
    h.send("proof: close", &[c::stage_close(&stage, &submitter.pubkey())], &[&submitter]).await.unwrap();
    // a header above the trusted height cannot carry a proof
    let h103 = testkit::header(CHAIN, 103, 1_700_000_030, &set, &set, Some(root), h102.hash(sha256));
    let (c103, s103) = testkit::sign(CHAIN, &h103, &vals, &[0, 1, 2, 3]);
    let tx = &c::proof_txs(&submitter.pubkey(), CHAIN, Some(&h103), [0; 32], &c103, &keys, &s103, &message, proof.clone(), &leaves[0], 3)[0];
    let e = h.send("proof: stage", tx, &[&submitter]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::HeightAboveTrusted));
    // a leaf over 8 KB
    let big = vec![0u8; 8193];
    let tx = &c::proof_txs(&submitter.pubkey(), CHAIN, None, h102.hash(sha256), &empty, &keys, &[], &message, proof.clone(), &big, 3)[0];
    let e = h.send("proof: stage", tx, &[&submitter]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::LeafTooBig));
    // a header the ring does not hold, at or below the trusted height and signed by the current set — the client
    // jumps from 102 to 104, so 103 is one — is recorded by its first proof and put in the ring; a second proof
    // of the same header records nothing (S-10: a stranger's repeated Verify cannot inflate the record)
    let h104 = testkit::header(CHAIN, 104, 1_700_000_040, &set, &set, None, h103.hash(sha256));
    let (c104, s104) = testkit::sign(CHAIN, &h104, &vals, &[0, 1, 2, 3]);
    update(&mut h, &submitter, CHAIN, &h104, &c104, &keys, &s104, None, "4 of 4").await.unwrap();
    let records = body(&mut h).await.records;
    assert_eq!(head(&mut h).await.ring_count, 3, "101, 102, 104");
    for pass in 0..2 {
        for tx in c::proof_txs(&submitter.pubkey(), CHAIN, Some(&h103), [0; 32], &c103, &keys, &s103, &message, proof.clone(), &leaves[0], 3) {
            h.send("proof: stage (header)", &tx, &[&submitter]).await.unwrap();
        }
        h.send("proof: verify (header)", &[c::verify(&submitter.pubkey(), &message)], &[]).await.unwrap();
        let data = h.data(&c::state()).await;
        let hd = Head::read(&data).unwrap();
        assert_eq!((body(&mut h).await.records, hd.ring_count), (records + 1, 4), "pass {pass}: the fresh header is recorded once and put in the ring");
        assert!(Ring::find(&hd, &data, &h103.hash(sha256)).is_some());
        h.send("proof: close", &[c::stage_close(&c::proof_stage(&submitter.pubkey(), &sha256(&[&message])), &submitter.pubkey())], &[&submitter]).await.unwrap();
    }
    // Type is 0
    let blockhash = h.ctx.banks_client.get_latest_blockhash().await.unwrap();
    let tx = solana_sdk::transaction::Transaction::new_signed_with_payer(&[c::ism_type()], Some(&payer.pubkey()), &[&payer], blockhash);
    let sim = h.ctx.banks_client.simulate_transaction(tx).await.unwrap();
    assert_eq!(sim.simulation_details.unwrap().return_data.unwrap().data, vec![0, 0, 0, 0, 0xff]);
    h.report("client proofs");
}

#[tokio::test]
async fn a_next_set_of_forty_is_staged_in_chunks_and_followed() {
    let (mut h, authority) = harness().await;
    let vals = testkit::validators(7, &[60, 60, 60, 60]);
    let set = testkit::set_of(&vals);
    let keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
    let payer = h.payer();
    let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &set, &set, None, [0; 32]);
    h.send("init", &[c::init(&payer.pubkey(), &authority.pubkey(), CHAIN, ORIGIN, 100, h100.hash(sha256), set.validators.clone(), keypair(3).pubkey())], &[&authority]).await.unwrap();
    grow(&mut h).await;
    let submitter = keypair(4);
    h.fund(&submitter.pubkey(), 5_000_000_000).await;
    // the room first (S-02): Grow to an 8 KB body region in one step, the ring kept whole
    let before = head(&mut h).await;
    h.send("grow (8 KB body)", &[c::grow_to(&payer.pubkey(), 8192)], &[]).await.unwrap();
    let hd = head(&mut h).await;
    assert_eq!(hd.body_cap(), 8192);
    assert!(hd.body_cap() > before.body_cap());
    assert_eq!(gno_light_client_ism::state::Ring::capacity(&hd, h.data(&c::state()).await.len()), 256);
    // a target below the current region, or 0, grows nothing more
    h.send("grow (nothing)", &[c::grow_to(&payer.pubkey(), 1024)], &[]).await.unwrap();
    assert_eq!(head(&mut h).await.body_cap(), 8192);
    // a set of 40 announced at 101: the init binds its length and hash, the set arrives in chunks of 25 and 15
    let next_vals = testkit::validators(8, &[60; 40]);
    let next = testkit::set_of(&next_vals);
    let next_keys: Vec<[u8; 32]> = next_vals.iter().map(|v| v.pubkey()).collect();
    let h101 = testkit::header(CHAIN, 101, 1_700_000_010, &set, &next, None, h100.hash(sha256));
    let (c101, s101) = testkit::sign(CHAIN, &h101, &vals, &[0, 1, 2, 3]);
    let txs = c::update_txs(&submitter.pubkey(), CHAIN, &h101, &c101, &keys, &s101, Some(next.validators.clone()), 3);
    assert_eq!(txs.len(), 1 + 2 + 2 + 1, "init, two chunks, two signature transactions, the finish");
    let stage = c::update_stage(&submitter.pubkey(), &h101.hash(sha256));
    h.send("update: stage_init+precommits (announcing 40)", &txs[0], &[&submitter]).await.unwrap();
    // the finish before the set is whole, and a chunk out of order, are refused
    let e = h.send("update: finish", &txs[5], &[&submitter]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::StageIncomplete));
    let e = h.send("update: stage_next_set", &[c::stage_next_set(&submitter.pubkey(), &stage, 5, next.validators[5..10].to_vec())], &[&submitter]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::ChunkOutOfOrder));
    for tx in &txs[1..3] {
        h.send("update: stage_next_set (chunk)", tx, &[&submitter]).await.unwrap();
    }
    let e = h.send("update: stage_next_set", &[c::stage_next_set(&submitter.pubkey(), &stage, 40, next.validators[..1].to_vec())], &[&submitter]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::ChunkOutOfOrder), "past the announced length");
    for tx in &txs[3..5] {
        h.send("update: precompile+verify", tx, &[&submitter]).await.unwrap();
    }
    h.send("update: finish (announcing 40)", &txs[5], &[&submitter]).await.unwrap();
    let b = body(&mut h).await;
    assert_eq!(b.next_set.as_ref().unwrap(), &next);
    assert_eq!(b.history.len(), 2);
    assert_eq!(b.history[1].set.len(), 40);
    assert!(head(&mut h).await.quarantine_end > h.time);
    // the first header of the new set, signed by 27 of 40 (1,620 of 2,400: more than two thirds): followed
    let h102 = testkit::header(CHAIN, 102, 1_700_000_020, &next, &next, None, h101.hash(sha256));
    let signers: Vec<usize> = (0..27).collect();
    let (c102, s102) = testkit::sign(CHAIN, &h102, &next_vals, &signers);
    update(&mut h, &submitter, CHAIN, &h102, &c102, &next_keys, &s102, None, "40 validators").await.unwrap();
    let b = body(&mut h).await;
    assert_eq!((b.set.len(), b.next_set.is_none(), head(&mut h).await.trusted_height), (40, true, 102));
    assert_eq!(b.set, next);
    // 26 of 40 (1,560) is not more than two thirds
    let h103 = testkit::header(CHAIN, 103, 1_700_000_030, &next, &next, None, h102.hash(sha256));
    let few: Vec<usize> = (0..26).collect();
    let (c103, s103) = testkit::sign(CHAIN, &h103, &next_vals, &few);
    let e = update(&mut h, &submitter, CHAIN, &h103, &c103, &next_keys, &s103, None, "26 of 40").await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::NotEnoughPower));
    h.send("stage_close", &[c::stage_close(&c::update_stage(&submitter.pubkey(), &h103.hash(sha256)), &submitter.pubkey())], &[&submitter]).await.unwrap();
    // a set staged under the right length and hash whose chunks make another set: the finish refuses it
    let other_vals = testkit::validators(12, &[60; 40]);
    let other = testkit::set_of(&other_vals);
    let h104 = testkit::header(CHAIN, 104, 1_700_000_040, &next, &other, None, h102.hash(sha256));
    let (c104, s104) = testkit::sign(CHAIN, &h104, &next_vals, &signers);
    let txs = c::update_txs(&submitter.pubkey(), CHAIN, &h104, &c104, &next_keys, &s104, Some(other.validators.clone()), 3);
    let stage = c::update_stage(&submitter.pubkey(), &h104.hash(sha256));
    h.send("update: stage_init+precommits (announcing 40)", &txs[0], &[&submitter]).await.unwrap();
    let mut doctored = other.validators.clone();
    doctored[0].power = 61;
    for tx in c::next_set_txs(&submitter.pubkey(), &stage, &doctored, 25) {
        h.send("update: stage_next_set (chunk)", &tx, &[&submitter]).await.unwrap();
    }
    for tx in &txs[3..txs.len() - 1] {
        h.send("update: precompile+verify", tx, &[&submitter]).await.unwrap();
    }
    let e = h.send("update: finish", &txs[txs.len() - 1], &[&submitter]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::NextValidatorsHash), "the chunks' hash is not the header's");
    h.send("stage_close", &[c::stage_close(&stage, &submitter.pubkey())], &[&submitter]).await.unwrap();
    assert_eq!(head(&mut h).await.trusted_height, 102);
    h.report("client: a next set of 40");
}

#[tokio::test]
async fn a_commit_carries_one_round() {
    let (mut h, authority) = harness().await;
    let vals = testkit::validators(7, &[60, 60, 60, 60]);
    let set = testkit::set_of(&vals);
    let keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
    let payer = h.payer();
    let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &set, &set, None, [0; 32]);
    h.send("init", &[c::init(&payer.pubkey(), &authority.pubkey(), CHAIN, ORIGIN, 100, h100.hash(sha256), set.validators.clone(), keypair(3).pubkey())], &[&authority]).await.unwrap();
    grow(&mut h).await;
    let submitter = keypair(4);
    h.fund(&submitter.pubkey(), 2_000_000_000).await;
    // a commit of round 2 throughout — the round in the init, every slot's sign bytes built from it: accepted
    let h101 = testkit::header(CHAIN, 101, 1_700_000_010, &set, &set, None, h100.hash(sha256));
    let (c101, s101) = testkit::sign_at_round(CHAIN, &h101, &vals, &[0, 1, 2, 3], 2);
    assert_eq!(c101.round(), 2);
    update(&mut h, &submitter, CHAIN, &h101, &c101, &keys, &s101, None, "round 2").await.unwrap();
    assert_eq!(head(&mut h).await.trusted_height, 101);
    // the same signatures staged under round 0 (the init's round, the items agreeing): the stage builds every
    // slot's sign bytes from its own round, so the precompile's messages — the real round-2 bytes — are not the
    // stage's, and no slot verifies
    let h102 = testkit::header(CHAIN, 102, 1_700_000_020, &set, &set, None, h101.hash(sha256));
    let (c102, s102) = testkit::sign_at_round(CHAIN, &h102, &vals, &[0, 1, 2, 3], 2);
    let stage = c::update_stage(&submitter.pubkey(), &h102.hash(sha256));
    let (mut items, _) = c::staged(&c102);
    for p in items.iter_mut() {
        p.round = 0;
    }
    let init = c::stage_init(&submitter.pubkey(), &stage, KIND_UPDATE, Some(h102.clone()), [0; 32], c102.block_id.clone(), 0, 4, [0; 32], None, None, 0);
    h.send("update: stage_init+precommits", &[init, c::stage_precommits(&submitter.pubkey(), &stage, 0, items)], &[&submitter]).await.unwrap();
    let entries: Vec<_> = (0..3).map(|i| sol_util::ed25519::Entry { pubkey: keys[i], signature: s102[i].unwrap(), message: c102.sign_bytes(CHAIN, 102, i).unwrap() }).collect();
    let e = h.send("update: precompile+verify", &[c::ed25519_ix(&entries), c::stage_verify(&stage, 0, vec![0, 1, 2])], &[&submitter]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::SignatureMismatch));
    h.send("stage_close", &[c::stage_close(&stage, &submitter.pubkey())], &[&submitter]).await.unwrap();
    // a mixed-round commit — two precommits of round 0 and two of round 2, each a real signature over its own
    // bytes — is refused at the staging: a commit's precommits are of one round (tm2's Commit.ValidateBasic,
    // S-08), and the client's rule is the chain's
    let (c0, s0) = testkit::sign_at_round(CHAIN, &h102, &vals, &[0, 1], 0);
    let (c2, s2) = testkit::sign_at_round(CHAIN, &h102, &vals, &[2, 3], 2);
    let mut mixed = c0.clone();
    let mut sigs = s0.clone();
    for i in 2..4 {
        mixed.precommits[i] = c2.precommits[i];
        sigs[i] = s2[i];
    }
    assert_eq!(mixed.round(), 0, "the first present precommit's");
    assert!(mixed.check_shape(&h102.hash(sha256), 4).is_err(), "the core refuses the shape");
    let e = update(&mut h, &submitter, CHAIN, &h102, &mixed, &keys, &sigs, None, "mixed rounds").await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::RoundMismatch));
    assert!(h.account(&stage).await.is_none(), "nothing staged");
    // a negative round is malformed
    let bad = c::stage_init(&submitter.pubkey(), &stage, KIND_UPDATE, Some(h102.clone()), [0; 32], c102.block_id.clone(), -1, 4, [0; 32], None, None, 0);
    let e = h.send("update: stage_init", &[bad], &[&submitter]).await.unwrap_err();
    assert!(matches!(e, solana_sdk::transaction::TransactionError::InstructionError(_, solana_sdk::instruction::InstructionError::InvalidInstructionData)), "{e:?}");
    // the honest round-2 commit lands
    update(&mut h, &submitter, CHAIN, &h102, &c102, &keys, &s102, None, "round 2").await.unwrap();
    assert_eq!(head(&mut h).await.trusted_height, 102);
    h.report("client: one round per commit");
}

/// Fills the ring with `n` synthetic entries in the bank (as `n` accepted headers would), the next write at
/// `next`; the entries' hashes.
async fn fill_ring(h: &mut Harness, n: usize, next: u16) -> Vec<[u8; 32]> {
    let mut acc = h.account(&c::state()).await.unwrap();
    let mut hd = Head::read(&acc.data).unwrap();
    let off = Ring::offset(&hd);
    let mut hashes = Vec::new();
    for i in 0..n {
        let hash = sha256(&[b"ring", &(i as u64).to_le_bytes()]);
        let at = off + i * RING_ENTRY;
        acc.data[at..at + 32].copy_from_slice(&hash);
        acc.data[at + 32..at + 40].copy_from_slice(&(200 + i as i64).to_le_bytes());
        acc.data[at + 40..at + 72].copy_from_slice(&sha256(&[b"results", &hash]));
        hashes.push(hash);
    }
    hd.ring_count = n as u16;
    hd.ring_next = next;
    hd.write(&mut acc.data);
    h.ctx.set_account(&c::state(), &acc.into());
    hashes
}

/// Reshapes the state account in the bank: the body region at `body_cap` bytes (the body stays where it is, at the
/// head's end), exactly `slots` ring slots behind it, the ring empty, rent-exempt at the new length — the shape
/// the S-09 build left after a stranger's `Grow` to the bound over a cleared ring (9 slots, S-17).
async fn reshape(h: &mut Harness, body_cap: usize, slots: usize) {
    let mut acc = h.account(&c::state()).await.unwrap();
    let mut hd = Head::read(&acc.data).unwrap();
    let body = Body::load(&acc.data).unwrap();
    let len = HEAD_LEN + body_cap + slots * RING_ENTRY;
    acc.data.resize(len, 0);
    hd.body_cap8 = (body_cap / 8) as u16;
    hd.ring_count = 0;
    hd.ring_next = 0;
    hd.write(&mut acc.data);
    body.store(&hd, &mut acc.data).unwrap();
    acc.lamports = acc.lamports.max(solana_sdk::rent::Rent::default().minimum_balance(len));
    h.ctx.set_account(&c::state(), &acc.into());
}

/// A stranger's `Grow` to the body region's bound with `n` ring entries (the next write at `next`): the ring
/// moves only on the call whose account holds the new region and a ring of the capacity it had, 256 (S-17: the
/// S-09 rule waited for the entries alone, so over a cleared ring it moved the region as soon as that fit, with
/// room for 9 slots, until two more paid calls grew the ring back; the first build moved it as soon as the region
/// fit and dropped the entries past the end), so the ring never has fewer slots than it had, every entry is found
/// after every call, and the next write is kept.
async fn grow_to_the_bound_keeps_the_ring(n: usize, next: u16) {
    let (mut h, authority) = harness().await;
    let vals = testkit::validators(7, &[60, 60, 60, 60]);
    let set = testkit::set_of(&vals);
    let keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
    let payer = h.payer();
    let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &set, &set, None, [0; 32]);
    h.send("init", &[c::init(&payer.pubkey(), &authority.pubkey(), CHAIN, ORIGIN, 100, h100.hash(sha256), set.validators.clone(), keypair(3).pubkey())], &[&authority]).await.unwrap();
    grow(&mut h).await;
    let stranger = keypair(5);
    h.fund(&stranger.pubkey(), 5_000_000_000).await;
    let hashes = fill_ring(&mut h, n, next).await;
    let before = head(&mut h).await;
    let full = full_len(MAX_BODY_CAP);
    let mut calls = 0;
    while h.data(&c::state()).await.len() < full {
        h.send("grow (to the bound)", &[c::grow_to(&stranger.pubkey(), MAX_BODY_CAP as u32)], &[&stranger]).await.unwrap();
        calls += 1;
        let data = h.data(&c::state()).await;
        let hd = Head::read(&data).unwrap();
        let holds_ring = HEAD_LEN + MAX_BODY_CAP + 256 * RING_ENTRY <= data.len();
        assert_eq!(hd.body_cap() == MAX_BODY_CAP, holds_ring, "call {calls}: the ring moves exactly when the account holds the new region and a ring of the capacity it had");
        if !holds_ring {
            assert_eq!(hd.body_cap(), before.body_cap(), "call {calls}: grown, not moved");
        }
        assert!(Ring::capacity(&hd, data.len()) >= 256, "call {calls}: fewer slots than the ring had");
        assert_eq!((hd.ring_count as usize, hd.ring_next), (n, next), "call {calls}");
        for x in &hashes {
            assert!(Ring::find(&hd, &data, x).is_some(), "call {calls}: an entry lost");
        }
        assert!(calls <= 12, "the bound is a few calls away");
    }
    let hd = head(&mut h).await;
    assert_eq!((hd.body_cap(), Ring::capacity(&hd, full)), (MAX_BODY_CAP, 256));
    println!("a ring of {n}: {calls} calls to the bound, the region moved on the last");
    // a further call grows nothing, and an update still lands, its header pushed at the next write
    h.send("grow (nothing)", &[c::grow_to(&stranger.pubkey(), MAX_BODY_CAP as u32)], &[&stranger]).await.unwrap();
    assert_eq!(h.data(&c::state()).await.len(), full);
    let submitter = keypair(4);
    h.fund(&submitter.pubkey(), 1_000_000_000).await;
    let h101 = testkit::header(CHAIN, 101, 1_700_000_010, &set, &set, None, h100.hash(sha256));
    let (c101, s101) = testkit::sign(CHAIN, &h101, &vals, &[0, 1, 2, 3]);
    update(&mut h, &submitter, CHAIN, &h101, &c101, &keys, &s101, None, "after the growth").await.unwrap();
    let hd = head(&mut h).await;
    assert_eq!((hd.trusted_height, hd.ring_count as usize, hd.ring_next), (101, (n + 1).min(256), (next + 1) % 256));
    assert!(Ring::find(&hd, &h.data(&c::state()).await, &h101.hash(sha256)).is_some());
    h.report(&format!("client: a stranger's Grow with a ring of {n}"));
}

#[tokio::test]
async fn a_strangers_grow_to_the_bound_keeps_every_ring_entry() {
    grow_to_the_bound_keeps_the_ring(256, 17).await;
    grow_to_the_bound_keeps_the_ring(100, 100).await;
}

#[tokio::test]
async fn a_strangers_grow_over_an_empty_ring_keeps_the_rings_capacity() {
    // a set change cleared the ring (the count 0): S-09's rule waited for the entries alone, so the sixth call
    // moved the region into an account with room for 9 slots, and until two more paid calls a header was
    // forgotten after nine later ones — ring proofs of a backlog NotInRing, relayers falling to header proofs;
    // the deploy's 256 slots are kept now (S-17), the region moving on the call that holds them all
    grow_to_the_bound_keeps_the_ring(0, 0).await;
}

#[tokio::test]
async fn a_ring_grown_past_its_entries_fills_its_free_slots_first() {
    let (mut h, authority) = harness().await;
    let vals = testkit::validators(7, &[60, 60, 60, 60]);
    let set = testkit::set_of(&vals);
    let keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
    let payer = h.payer();
    let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &set, &set, None, [0; 32]);
    h.send("init", &[c::init(&payer.pubkey(), &authority.pubkey(), CHAIN, ORIGIN, 100, h100.hash(sha256), set.validators.clone(), keypair(3).pubkey())], &[&authority]).await.unwrap();
    grow(&mut h).await;
    // the shape the S-09 build left on the sixth call of a stranger's Grow over a cleared ring: the region at its
    // bound with 9 slots behind it — filled since, the next overwrite at 3, its oldest entry
    reshape(&mut h, MAX_BODY_CAP, 9).await;
    let hashes = fill_ring(&mut h, 9, 3).await;
    let stranger = keypair(5);
    h.fund(&stranger.pubkey(), 5_000_000_000).await;
    // the seventh call: the account grows 10 KB, the region stays at its bound, and the ring has 151 slots
    h.send("grow (the ring)", &[c::grow_to(&stranger.pubkey(), MAX_BODY_CAP as u32)], &[&stranger]).await.unwrap();
    let data = h.data(&c::state()).await;
    let hd = Head::read(&data).unwrap();
    assert_eq!((hd.body_cap(), Ring::capacity(&hd, data.len()), hd.ring_count, hd.ring_next), (MAX_BODY_CAP, 151, 9, 3));
    // two headers accepted: each takes the first free slot (9, then 10), not the slot ring_next names — the old
    // push wrote at 3 and 4, over live entries, while 142 slots stood free (S-17)
    let submitter = keypair(4);
    h.fund(&submitter.pubkey(), 1_000_000_000).await;
    let h101 = testkit::header(CHAIN, 101, 1_700_000_010, &set, &set, None, h100.hash(sha256));
    let (c101, s101) = testkit::sign(CHAIN, &h101, &vals, &[0, 1, 2, 3]);
    update(&mut h, &submitter, CHAIN, &h101, &c101, &keys, &s101, None, "slot 9").await.unwrap();
    let h102 = testkit::header(CHAIN, 102, 1_700_000_020, &set, &set, None, h101.hash(sha256));
    let (c102, s102) = testkit::sign(CHAIN, &h102, &vals, &[0, 1, 2, 3]);
    update(&mut h, &submitter, CHAIN, &h102, &c102, &keys, &s102, None, "slot 10").await.unwrap();
    let data = h.data(&c::state()).await;
    let hd = Head::read(&data).unwrap();
    assert_eq!((hd.ring_count, hd.ring_next), (11, 11));
    for (i, x) in hashes.iter().enumerate() {
        assert!(Ring::find(&hd, &data, x).is_some(), "entry {i} overwritten");
    }
    for (slot, hh) in [(9, &h101), (10, &h102)] {
        let at = Ring::offset(&hd) + slot * RING_ENTRY;
        assert_eq!(&data[at..at + 32], &hh.hash(sha256), "header {} at slot {slot}", hh.height);
    }
    // the eighth call completes the ring at 256 with everything kept
    h.send("grow (the ring)", &[c::grow_to(&stranger.pubkey(), MAX_BODY_CAP as u32)], &[&stranger]).await.unwrap();
    let data = h.data(&c::state()).await;
    let hd = Head::read(&data).unwrap();
    assert_eq!((data.len(), Ring::capacity(&hd, data.len()), hd.ring_count, hd.ring_next), (full_len(MAX_BODY_CAP), 256, 11, 11));
    for x in hashes.iter().chain([h101.hash(sha256), h102.hash(sha256)].iter()) {
        assert!(Ring::find(&hd, &data, x).is_some());
    }
    h.report("client: a ring grown past its entries");
}

#[tokio::test]
async fn a_staged_ring_proof_survives_the_rings_churn() {
    let (mut h, authority) = harness().await;
    let vals = testkit::validators(7, &[60, 60, 60, 60]);
    let set = testkit::set_of(&vals);
    let keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
    let payer = h.payer();
    let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &set, &set, None, [0; 32]);
    h.send("init", &[c::init(&payer.pubkey(), &authority.pubkey(), CHAIN, ORIGIN, 100, h100.hash(sha256), set.validators.clone(), keypair(3).pubkey())], &[&authority]).await.unwrap();
    grow(&mut h).await;
    let relayer = keypair(4);
    let stranger = keypair(5);
    h.fund(&relayer.pubkey(), 2_000_000_000).await;
    h.fund(&stranger.pubkey(), 50_000_000_000).await;
    // block 101 carries the relayer's dispatch; header 102 commits to its results; the client follows both
    let sender = [0xaa; 32];
    let recipient = [0xbb; 32];
    let message = hl::encode_message(7, ORIGIN, &sender, DESTINATION, &recipient, b"hello");
    let d = gno_light_core::results::Dispatch { id: hl::Message::id(&message), nonce: 7, origin: ORIGIN, destination: DESTINATION, sender, recipient, hash: sha256(&[&message]) };
    let leaves = testkit::dispatch_leaves(MAILBOX_PKG_PATH, &[d], 2);
    let (root, proof) = testkit::results_proof(&leaves, 0);
    let h101 = testkit::header(CHAIN, 101, 1_700_000_010, &set, &set, None, h100.hash(sha256));
    let h102 = testkit::header(CHAIN, 102, 1_700_000_020, &set, &set, Some(root), h101.hash(sha256));
    for hh in [&h101, &h102] {
        let (cm, sg) = testkit::sign(CHAIN, hh, &vals, &[0, 1, 2, 3]);
        update(&mut h, &relayer, CHAIN, hh, &cm, &keys, &sg, None, "4 of 4").await.unwrap();
    }
    // the relayer stages its proof by the ring, a minute before its delivery: the entry's height and results hash
    // are copied into the stage under the ring's epoch (S-16)
    let msg_hash = sha256(&[&message]);
    let empty = gno_light_core::commit::Commit::default();
    for tx in c::proof_txs(&relayer.pubkey(), CHAIN, None, h102.hash(sha256), &empty, &keys, &[], &message, proof.clone(), &leaves[0], 3) {
        h.send("proof: stage (ring)", &tx, &[&relayer]).await.unwrap();
    }
    let st = Stage::load(&h.data(&c::proof_stage(&relayer.pubkey(), &msg_hash)).await).unwrap();
    assert_eq!((st.by_ring, st.ring_epoch, st.ring_height, st.ring_results), (true, 0, 102, root));
    assert_eq!(body(&mut h).await.ring_epoch, 0);
    // meanwhile the client jumps to 360, and a stranger proves 257 old headers of the current set — 103 to 359,
    // each with a Dispatch under its LastResultsHash (the same leaf) — one after another: every one is fresh to
    // the ring, so every one is recorded once and pushed (S-10), and the ring turns over whole, 101 and 102 among
    // the evicted (the first build's ring proof died here: NotInRing)
    let h360 = testkit::header(CHAIN, 360, 1_700_002_600, &set, &set, Some(root), [1; 32]);
    let (c360, s360) = testkit::sign(CHAIN, &h360, &vals, &[0, 1, 2, 3]);
    update(&mut h, &relayer, CHAIN, &h360, &c360, &keys, &s360, None, "to 360").await.unwrap();
    let records = body(&mut h).await.records;
    let churn_stage = c::proof_stage(&stranger.pubkey(), &msg_hash);
    let churn_header = |height: i64| testkit::header(CHAIN, height, 1_700_000_000 + height * 10, &set, &set, Some(root), [2; 32]);
    for height in 103..=359i64 {
        let hx = churn_header(height);
        let (cx, sx) = testkit::sign(CHAIN, &hx, &vals, &[0, 1, 2]);
        for tx in c::proof_txs(&stranger.pubkey(), CHAIN, Some(&hx), [0; 32], &cx, &keys, &sx, &message, proof.clone(), &leaves[0], 3) {
            h.send("churn: stage (header)", &tx, &[&stranger]).await.unwrap();
        }
        h.send("churn: verify (header)", &[c::verify(&stranger.pubkey(), &message)], &[]).await.unwrap();
        h.send("churn: close", &[c::stage_close(&churn_stage, &stranger.pubkey())], &[&stranger]).await.unwrap();
    }
    let data = h.data(&c::state()).await;
    let hd = Head::read(&data).unwrap();
    let b = Body::load(&data).unwrap();
    assert_eq!((hd.ring_count, b.records, b.ring_epoch), (256, records + 257, 0), "257 fresh headers recorded once each, the ring full, no clear");
    assert!(Ring::find(&hd, &data, &h102.hash(sha256)).is_none(), "the relayer's entry was evicted");
    assert!(Ring::find(&hd, &data, &h101.hash(sha256)).is_none() && Ring::find(&hd, &data, &h360.hash(sha256)).is_none(), "everything before the churn is gone");
    assert!(Ring::find(&hd, &data, &churn_header(359).hash(sha256)).is_some());
    // the relayer's staged proof delivers all the same: Verify reads the copy — the epoch and the set are still the
    // staging's — and, a ring hit, records nothing and pushes nothing
    h.send("proof: verify (ring, after the churn)", &[c::verify(&relayer.pubkey(), &message)], &[]).await.unwrap();
    let data = h.data(&c::state()).await;
    let hd = Head::read(&data).unwrap();
    assert_eq!((hd.ring_count, Body::load(&data).unwrap().records), (256, records + 257), "a ring hit records nothing");
    assert!(Ring::find(&hd, &data, &h102.hash(sha256)).is_none());
    h.send("proof: close", &[c::stage_close(&c::proof_stage(&relayer.pubkey(), &msg_hash), &relayer.pubkey())], &[&relayer]).await.unwrap();
    // a set change still voids a staged ring proof: one of 359 (the newest churned header, in the ring) staged
    // before the change finds the ring cleared and the epoch moved after it — its copy serves nothing — and is
    // NotInRing
    let h359 = churn_header(359);
    for tx in c::proof_txs(&relayer.pubkey(), CHAIN, None, h359.hash(sha256), &empty, &keys, &[], &message, proof.clone(), &leaves[0], 3) {
        h.send("proof: stage (ring)", &tx, &[&relayer]).await.unwrap();
    }
    assert_eq!(Stage::load(&h.data(&c::proof_stage(&relayer.pubkey(), &msg_hash)).await).unwrap().ring_height, 359);
    let next_vals = testkit::validators(8, &[60, 60, 60, 60]);
    let next = testkit::set_of(&next_vals);
    let next_keys: Vec<[u8; 32]> = next_vals.iter().map(|v| v.pubkey()).collect();
    let h361 = testkit::header(CHAIN, 361, 1_700_002_610, &set, &next, None, h360.hash(sha256));
    let (c361, s361) = testkit::sign(CHAIN, &h361, &vals, &[0, 1, 2, 3]);
    update(&mut h, &relayer, CHAIN, &h361, &c361, &keys, &s361, Some(next.validators.clone()), "announcing").await.unwrap();
    let h362 = testkit::header(CHAIN, 362, 1_700_002_620, &next, &next, None, h361.hash(sha256));
    let (c362, s362) = testkit::sign(CHAIN, &h362, &next_vals, &[0, 1, 2, 3]);
    update(&mut h, &relayer, CHAIN, &h362, &c362, &next_keys, &s362, None, "new set").await.unwrap();
    let data = h.data(&c::state()).await;
    assert_eq!((Head::read(&data).unwrap().ring_count, Body::load(&data).unwrap().ring_epoch), (1, 1), "the ring cleared at the set change, the epoch up");
    let e = h.send("proof: verify (ring, stale)", &[c::verify(&relayer.pubkey(), &message)], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::NotInRing));
    h.send("proof: close", &[c::stage_close(&c::proof_stage(&relayer.pubkey(), &msg_hash), &relayer.pubkey())], &[&relayer]).await.unwrap();
    h.report("client: a staged ring proof through the ring's churn");
}

/// The real limit of the staging path (M2-NOTES): `PROBE_N` validators a set, `PROBE_ROTATIONS` set changes in a
/// row (the history fills to five), the body region grown to its bound first. Prints what passed and the first
/// refusal. `test.sh suite -- --ignored probe_next_set --nocapture` with the variables set.
#[tokio::test]
#[ignore]
async fn probe_next_set() {
    let n: usize = std::env::var("PROBE_N").ok().and_then(|v| v.parse().ok()).unwrap_or(40);
    let rotations: usize = std::env::var("PROBE_ROTATIONS").ok().and_then(|v| v.parse().ok()).unwrap_or(5);
    let (mut h, authority) = harness().await;
    let mut vals = testkit::validators(7, &[60, 60, 60, 60]);
    let mut set = testkit::set_of(&vals);
    let mut keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
    let payer = h.payer();
    let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &set, &set, None, [0; 32]);
    let mut last_hash = h100.hash(sha256);
    h.send("init", &[c::init(&payer.pubkey(), &authority.pubkey(), CHAIN, ORIGIN, 100, last_hash, set.validators.clone(), keypair(3).pubkey())], &[&authority]).await.unwrap();
    grow(&mut h).await;
    let submitter = keypair(4);
    h.fund(&submitter.pubkey(), 50_000_000_000).await;
    let cap = gno_light_client_ism::config::MAX_BODY_CAP as u32;
    for _ in 0..12 {
        h.send("grow (to the bound)", &[c::grow_to(&payer.pubkey(), cap)], &[]).await.unwrap();
    }
    println!("probe: body cap {} bytes, account {} bytes", head(&mut h).await.body_cap(), h.data(&c::state()).await.len());
    let mut height = 100i64;
    for r in 0..rotations {
        let next_vals = testkit::validators(20 + r as u8, &vec![60; n]);
        let next = testkit::set_of(&next_vals);
        let next_keys: Vec<[u8; 32]> = next_vals.iter().map(|v| v.pubkey()).collect();
        let all: Vec<usize> = (0..vals.len()).collect();
        let ha = testkit::header(CHAIN, height + 1, 1_700_000_000 + (height + 1) * 10, &set, &next, None, last_hash);
        let (cm, sg) = testkit::sign(CHAIN, &ha, &vals, &all);
        let txs = c::update_txs(&submitter.pubkey(), CHAIN, &ha, &cm, &keys, &sg, Some(next.validators.clone()), 3);
        let m = txs.len();
        let mut failed = None;
        for (i, tx) in txs.into_iter().enumerate() {
            let l = if i == 0 { "announce: init".to_string() } else if i == m - 1 { "announce: finish".to_string() } else if tx[0].program_id == solana_sdk::ed25519_program::ID { "announce: verify".to_string() } else { "announce: next set chunk".to_string() };
            if let Err(e) = h.send(&l, &tx, &[&submitter]).await {
                failed = Some((l, e));
                break;
            }
        }
        if let Some((l, e)) = failed {
            println!("probe: rotation {} to {} validators FAILED at {l}: {e:?}", r + 1, n);
            h.report("probe");
            return;
        }
        let hb = testkit::header(CHAIN, height + 2, 1_700_000_000 + (height + 2) * 10, &next, &next, None, ha.hash(sha256));
        let signers: Vec<usize> = (0..(2 * n / 3 + 1)).collect();
        let (cm, sg) = testkit::sign(CHAIN, &hb, &next_vals, &signers);
        let txs = c::update_txs(&submitter.pubkey(), CHAIN, &hb, &cm, &next_keys, &sg, None, 3);
        let m = txs.len();
        for (i, tx) in txs.into_iter().enumerate() {
            let l = if i == 0 { "follow: init".to_string() } else if i == m - 1 { "follow: finish".to_string() } else { "follow: verify".to_string() };
            if let Err(e) = h.send(&l, &tx, &[&submitter]).await {
                println!("probe: rotation {} to {} validators FAILED at {l}: {e:?}", r + 1, n);
                h.report("probe");
                return;
            }
        }
        height += 2;
        last_hash = hb.hash(sha256);
        vals = next_vals;
        set = next;
        keys = next_keys;
        let b = body(&mut h).await;
        println!("probe: rotation {} to {} validators ok; history {}, body {} bytes", r + 1, n, b.history.len(), b.size());
    }
    h.report("probe");
}

#[tokio::test]
async fn abandoned_stages_close_after_seven_days_and_nothing_resets_the_client() {
    let (mut h, authority) = harness().await;
    let vals = testkit::validators(7, &[60, 60, 60, 60]);
    let set = testkit::set_of(&vals);
    let keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
    let payer = h.payer();
    let admin = keypair(3);
    let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &set, &set, None, [0; 32]);
    h.send("init", &[c::init(&payer.pubkey(), &authority.pubkey(), CHAIN, ORIGIN, 100, h100.hash(sha256), set.validators.clone(), admin.pubkey())], &[&authority]).await.unwrap();
    grow(&mut h).await;
    let submitter = keypair(4);
    let stranger = keypair(5);
    h.fund(&submitter.pubkey(), 1_000_000_000).await;
    h.fund(&stranger.pubkey(), 1_000_000_000).await;
    let h101 = testkit::header(CHAIN, 101, 1_700_000_010, &set, &set, None, h100.hash(sha256));
    let (c101, s101) = testkit::sign(CHAIN, &h101, &vals, &[0, 1, 2, 3]);
    let txs = c::update_txs(&submitter.pubkey(), CHAIN, &h101, &c101, &keys, &s101, None, 3);
    h.send("update: stage_init+precommits", &txs[0], &[&submitter]).await.unwrap();
    let stage = c::update_stage(&submitter.pubkey(), &h101.hash(sha256));
    let e = h.send("stage_close", &[c::stage_close(&stage, &stranger.pubkey())], &[&stranger]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::NotAbandoned));
    h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    let before = h.lamports(&stranger.pubkey()).await;
    h.send("stage_close (abandoned)", &[c::stage_close(&stage, &stranger.pubkey())], &[&stranger]).await.unwrap();
    assert!(h.lamports(&stranger.pubkey()).await > before);
    assert!(h.account(&stage).await.is_none());

    // the admin role: the admin alone moves it, and the client has no other control beside its upgrade
    // authority (M2j)
    let new_admin = keypair(6);
    h.fund(&new_admin.pubkey(), 1_000_000_000).await;
    let e = h.send("set_admin", &[c::set_admin(&stranger.pubkey(), new_admin.pubkey())], &[&stranger]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::NotAdmin));
    h.send("set_admin", &[c::set_admin(&admin.pubkey(), new_admin.pubkey())], &[&admin]).await.unwrap();
    assert_eq!(body(&mut h).await.admin, new_admin.pubkey());
    let e = h.send("set_admin", &[c::set_admin(&admin.pubkey(), admin.pubkey())], &[&admin]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(ClientError::NotAdmin), "the old admin is nobody now");
    h.send("set_admin", &[c::set_admin(&new_admin.pubkey(), admin.pubkey())], &[&new_admin]).await.unwrap();
    assert_eq!(body(&mut h).await.admin, admin.pubkey());
    // nothing resets the client (M2j: the validators' recovery path is gone, and with it kind 11 and the
    // client's Reset): the removed instruction's bytes — its old tag 7 before a new chain id, origin, trusted
    // header and set, in its old borsh form — signed by the admin fit no variant, under tag 7 (SetAdmin now: a
    // 32-byte key, so the longer payload is InvalidInstructionData) or any other tag, past the enum's end
    // included; the state is byte-identical after every attempt. The tags themselves are pinned: SetAdmin is
    // 7, Grow 8 and StageNextSet 9, the enum's last (the Go side's discriminators)
    #[derive(borsh::BorshSerialize)]
    struct OldReset {
        chain_id: String,
        origin_domain: u32,
        trusted_height: u64,
        trusted_hash: [u8; 32],
        validators: Vec<gno_light_core::valset::Validator>,
    }
    let vals2 = testkit::validators(8, &[10, 10, 10]);
    let set2 = testkit::set_of(&vals2);
    let payload = borsh::to_vec(&OldReset { chain_id: "gno-new".into(), origin_domain: 42, trusted_height: 7, trusted_hash: [9; 32], validators: set2.validators.clone() }).unwrap();
    assert_eq!(borsh::to_vec(&gno_light_client_ism::instruction::Instruction::SetAdmin { new_admin: admin.pubkey() }).unwrap()[0], 7);
    assert_eq!(borsh::to_vec(&gno_light_client_ism::instruction::Instruction::Grow { body_cap: 0 }).unwrap(), vec![8, 0, 0, 0, 0]);
    assert_eq!(borsh::to_vec(&gno_light_client_ism::instruction::Instruction::StageNextSet { offset: 0, items: vec![] }).unwrap(), vec![9, 0, 0, 0, 0, 0, 0]);
    let before = h.data(&c::state()).await;
    for tag in 0..=12u8 {
        let mut data = vec![tag];
        data.extend_from_slice(&payload);
        let e = h.send("reset (removed)", &[c::raw(&admin.pubkey(), data)], &[&admin]).await.unwrap_err();
        if tag == 7 || tag >= 10 {
            assert!(matches!(e, solana_sdk::transaction::TransactionError::InstructionError(0, solana_sdk::instruction::InstructionError::InvalidInstructionData)), "tag {tag}: {e:?}");
        }
        assert_eq!(h.data(&c::state()).await, before, "tag {tag}: the client untouched");
    }
    let (hd, b) = (head(&mut h).await, body(&mut h).await);
    assert_eq!((hd.trusted_height, b.chain_id.as_str(), b.origin_domain, b.set.clone(), b.ring_epoch), (100, CHAIN, ORIGIN, set.clone(), 0));
    let _ = ValidatorSet::default();
    h.report("client close and the admin role");
}

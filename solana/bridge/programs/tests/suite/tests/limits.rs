#![allow(deprecated)]
//! The pausable ISM's mint limits (AN5) under program-test with the real mailbox build: the SPL noop program is
//! loaded at the two token ids, so a message to a "token" is delivered through the mailbox — the pausable ISM
//! as the default ISM, the client's proof, noop's `handle` — and the ISM's `Verify` runs as a real delivery runs
//! it, under the mailbox's per-ISM process authority. Run through ../../../test.sh.

use gno_light_client_ism::{config::MAILBOX_PKG_PATH, error::ClientError, ID as CLIENT};
use gno_light_core::{sha256, testkit, testkit::TestValidator, valset::ValidatorSet};
use pausable_ism::{
    config::{CLOCK_FACTOR, DEFAULT_DAY_CAP, DEFAULT_MAX_PER_MESSAGE, MAILBOX, TOKENS},
    error::PausableError,
    state::State,
    ID as PAUSABLE,
};
use sol_util::{clock, hl};
use solana_sdk::{
    pubkey::Pubkey,
    signature::{Keypair, Signer},
    transaction::TransactionError,
};
use testlib::{admin as a, client as c, custom_code, keypair, pausable as p, tokio, Harness, ProgramSpec};

const CHAIN: &str = "gno-test-1";
const ORIGIN: u32 = 1198419788;
const LOCAL: u32 = 1399811151;
const REF: &str = "/Users/jae/.config/bubblerumble-jarvis/bridge-ref/so";

fn code(e: PausableError) -> Option<u32> {
    Some(2000 + e as u32)
}

fn day() -> i64 {
    clock::unpause_delay(CLOCK_FACTOR)
}

/// A token message of `amount` to a fixed recipient.
fn token(amount: u64) -> Vec<u8> {
    hl::encode_token_message(&[0xbb; 32], amount, b"")
}

struct World {
    h: Harness,
    owner: Keypair,
    vals: Vec<TestValidator>,
    set: ValidatorSet,
    keys: Vec<[u8; 32]>,
    watch: Vec<Keypair>,
    submitter: Keypair,
    height: i64,
    last_hash: [u8; 32],
    nonce: u32,
}

impl World {
    async fn new() -> World {
        let authority = keypair(1);
        let programs = vec![
            ProgramSpec { name: "gno_light_client_ism", id: CLIENT, authority: Some(authority.pubkey()), path: None },
            ProgramSpec { name: "pausable_ism", id: PAUSABLE, authority: Some(authority.pubkey()), path: None },
            ProgramSpec { name: "mailbox", id: MAILBOX, authority: Some(authority.pubkey()), path: Some(format!("{REF}/mailbox.so")) },
            ProgramSpec { name: "noop", id: a::NOOP, authority: None, path: Some(format!("{REF}/noop.so")) },
            // the two "tokens": noop, so their handle accepts anything and the ISM's metering is what decides
            ProgramSpec { name: "noop", id: TOKENS[0], authority: None, path: Some(format!("{REF}/noop.so")) },
            ProgramSpec { name: "noop", id: TOKENS[1], authority: None, path: Some(format!("{REF}/noop.so")) },
        ];
        let h = Harness::new(&programs, vec![]).await;
        let vals = testkit::validators(7, &[60, 60, 60, 60]);
        let set = testkit::set_of(&vals);
        let keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
        let seats: Vec<Keypair> = (10..14).map(keypair).collect();
        let watch: Vec<Keypair> = (14..16).map(keypair).collect();
        let owner = keypair(2);
        let submitter = keypair(4);
        let mut w = World { h, owner, vals, set, keys, watch, submitter, height: 100, last_hash: [0; 32], nonce: 1 };
        for k in seats.iter().chain(w.watch.iter()).chain(std::iter::once(&w.owner)) {
            w.h.fund(&k.pubkey(), 100_000_000).await;
        }
        w.h.fund(&w.submitter.pubkey(), 5_000_000_000).await;
        let payer = w.h.payer();
        let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &w.set, &w.set, None, [0; 32]);
        w.last_hash = h100.hash(sha256);
        w.h.send("client: init", &[c::init(&payer.pubkey(), &authority.pubkey(), CHAIN, ORIGIN, 100, w.last_hash, w.set.validators.clone(), keypair(3).pubkey())], &[&authority]).await.unwrap();
        for _ in 0..3 {
            w.h.send("client: grow", &[c::grow(&payer.pubkey())], &[]).await.unwrap();
        }
        let seat_keys = [seats[0].pubkey(), seats[1].pubkey(), seats[2].pubkey(), seats[3].pubkey()];
        let watch_keys = [w.watch[0].pubkey(), w.watch[1].pubkey()];
        w.h.send("pausable: init", &[p::init(&payer.pubkey(), &authority.pubkey(), w.owner.pubkey(), CLIENT, 1, seat_keys, watch_keys)], &[&authority]).await.unwrap();
        w.h.send("mailbox: init", &[a::mailbox_init(&payer.pubkey(), LOCAL, &PAUSABLE, 50_000_000, 0, &payer.pubkey())], &[]).await.unwrap();
        w
    }

    fn state(&mut self) -> impl std::future::Future<Output = State> + '_ {
        async { State::load(&self.h.data(&p::state()).await).unwrap() }
    }

    /// One header accepted at the next height, signed by the current set.
    async fn tick(&mut self) {
        let hh = testkit::header(CHAIN, self.height + 1, 1_700_000_000 + (self.height + 1) * 10, &self.set, &self.set, None, self.last_hash);
        let all: Vec<usize> = (0..self.vals.len()).collect();
        let (cm, sg) = testkit::sign(CHAIN, &hh, &self.vals, &all);
        for tx in c::update_txs(&self.submitter.pubkey(), CHAIN, &hh, &cm, &self.keys, &sg, None, 3) {
            self.h.send("client: update", &tx, &[&self.submitter]).await.unwrap();
        }
        self.height += 1;
        self.last_hash = hh.hash(sha256);
    }

    /// The client follows a set change to `next_vals` (the announcement, then the new set's first header).
    async fn rotate(&mut self, next_vals: Vec<TestValidator>) {
        let next = testkit::set_of(&next_vals);
        let next_keys: Vec<[u8; 32]> = next_vals.iter().map(|v| v.pubkey()).collect();
        let all: Vec<usize> = (0..self.vals.len()).collect();
        let ha = testkit::header(CHAIN, self.height + 1, 1_700_000_000 + (self.height + 1) * 10, &self.set, &next, None, self.last_hash);
        let (cm, sg) = testkit::sign(CHAIN, &ha, &self.vals, &all);
        for tx in c::update_txs(&self.submitter.pubkey(), CHAIN, &ha, &cm, &self.keys, &sg, Some(next.validators.clone()), 3) {
            self.h.send("client: update (announcing)", &tx, &[&self.submitter]).await.unwrap();
        }
        let hb = testkit::header(CHAIN, self.height + 2, 1_700_000_000 + (self.height + 2) * 10, &next, &next, None, ha.hash(sha256));
        let all_next: Vec<usize> = (0..next_vals.len()).collect();
        let (cm, sg) = testkit::sign(CHAIN, &hb, &next_vals, &all_next);
        for tx in c::update_txs(&self.submitter.pubkey(), CHAIN, &hb, &cm, &next_keys, &sg, None, 3) {
            self.h.send("client: update (new set)", &tx, &[&self.submitter]).await.unwrap();
        }
        self.height += 2;
        self.last_hash = hb.hash(sha256);
        self.vals = next_vals;
        self.set = next;
        self.keys = next_keys;
    }

    /// A message from gno.land to `recipient` with `body`, dispatched in a block the client is updated past,
    /// proven by the ring and processed through the mailbox under the pausable ISM; the proof is staged and left
    /// for the direct-call test when `keep_proof`.
    async fn deliver_keeping(&mut self, recipient: &Pubkey, body: &[u8], keep_proof: bool) -> (Vec<u8>, Result<(), TransactionError>) {
        let message = hl::encode_message(self.nonce, ORIGIN, &[0xaa; 32], LOCAL, &recipient.to_bytes(), body);
        self.nonce += 1;
        let m = hl::Message::parse(&message).unwrap();
        let d = gno_light_core::results::Dispatch { id: hl::Message::id(&message), nonce: m.nonce, origin: m.origin, destination: m.destination, sender: m.sender, recipient: m.recipient, hash: sha256(&[&message]) };
        let leaves = testkit::dispatch_leaves(MAILBOX_PKG_PATH, &[d], 1);
        let (root, proof) = testkit::results_proof(&leaves, 0);
        let all: Vec<usize> = (0..self.vals.len()).collect();
        let h1 = testkit::header(CHAIN, self.height + 1, 1_700_000_000 + (self.height + 1) * 10, &self.set, &self.set, None, self.last_hash);
        let h2 = testkit::header(CHAIN, self.height + 2, 1_700_000_000 + (self.height + 2) * 10, &self.set, &self.set, Some(root), h1.hash(sha256));
        for hh in [&h1, &h2] {
            let (cm, sg) = testkit::sign(CHAIN, hh, &self.vals, &all);
            for tx in c::update_txs(&self.submitter.pubkey(), CHAIN, hh, &cm, &self.keys, &sg, None, 3) {
                self.h.send("client: update", &tx, &[&self.submitter]).await.unwrap();
            }
        }
        self.height += 2;
        self.last_hash = h2.hash(sha256);
        let empty = gno_light_core::commit::Commit::default();
        for tx in c::proof_txs(&self.submitter.pubkey(), CHAIN, None, h2.hash(sha256), &empty, &self.keys, &[], &message, proof, &leaves[0], 3) {
            self.h.send("client: stage proof (ring)", &tx, &[&self.submitter]).await.unwrap();
        }
        let payer = self.h.payer();
        let r = self.h.send("mailbox: process -> pausable verify -> client verify -> noop", &[p::process_through(&payer.pubkey(), &message, &self.submitter.pubkey(), recipient, vec![])], &[]).await.map(|_| ());
        if !keep_proof {
            self.h.send("client: close proof", &[c::stage_close(&c::proof_stage(&self.submitter.pubkey(), &sha256(&[&message])), &self.submitter.pubkey())], &[&self.submitter]).await.unwrap();
        }
        (message, r)
    }

    async fn deliver(&mut self, recipient: &Pubkey, body: &[u8]) -> Result<(), TransactionError> {
        self.deliver_keeping(recipient, body, false).await.1
    }
}

#[tokio::test]
async fn mint_limits_meter_deliveries_and_only_deliveries() {
    let mut w = World::new().await;
    let (gnot, bubble) = (TOKENS[0], TOKENS[1]);
    let (max, cap) = (DEFAULT_MAX_PER_MESSAGE, DEFAULT_DAY_CAP);
    let s = w.state().await;
    assert_eq!((s.limits[0].max_per_message, s.limits[0].day_cap, s.limits[1].max_per_message, s.limits[1].day_cap), (max, cap, max, cap), "the launch limits: 25,000 a message, 100,000 a day, both tokens");
    // the maximum passes and charges the day's bucket; one unit above is refused
    w.deliver(&gnot, &token(max)).await.unwrap();
    assert_eq!(w.state().await.limits[0].used, max);
    let e = w.deliver(&gnot, &token(max + 1)).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::OverMax));
    // three more of the maximum fill the day (4 × 25,000 = 100,000); a fifth, of one unit, would pass the cap
    for _ in 0..3 {
        w.deliver(&gnot, &token(max)).await.unwrap();
    }
    assert_eq!(w.state().await.limits[0].used, cap);
    let e = w.deliver(&gnot, &token(1)).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::OverDayCap));
    // the other token has a bucket of its own
    w.deliver(&bubble, &token(max)).await.unwrap();
    assert_eq!(w.state().await.limits[1].used, max);
    // a quarter of a day on, a quarter of the cap has drained: the maximum passes once more, one unit more does
    // not (the clock factor divides the day)
    w.h.advance(day() / 4).await;
    assert_eq!(w.state().await.limits[0].level(w.h.time, day()), cap - cap / 4);
    w.deliver(&gnot, &token(max)).await.unwrap();
    let e = w.deliver(&gnot, &token(1)).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::OverDayCap));
    // a whole day on the bucket is empty
    w.h.advance(day()).await;
    assert_eq!(w.state().await.limits[0].level(w.h.time, day()), 0);
    w.deliver(&gnot, &token(max)).await.unwrap();
    // a recipient that is no token of ours mints nothing of ours: unmetered, whatever its body
    w.deliver(&a::NOOP, &token(u64::MAX)).await.unwrap();
    w.deliver(&a::NOOP, b"not a token message").await.unwrap();
    // a message to a token that is no token message is refused
    let e = w.deliver(&gnot, b"short").await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::BadTokenMessage));
    let mut past64 = token(1);
    past64[40] = 1;
    let e = w.deliver(&gnot, &past64).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::BadTokenMessage));
    // the setter: the owner lowers GNOT's maximum to 10,000 and its day to 30,000; the bucket's level stands
    let (stranger, owner) = (keypair(40), w.owner.insecure_clone());
    w.h.fund(&stranger.pubkey(), 10_000_000).await;
    let e = w.h.send("pausable: set_mint_limits", &[p::set_mint_limits(&stranger.pubkey(), 0, 1, 1)], &[&stranger]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::NotOwner));
    let e = w.h.send("pausable: set_mint_limits", &[p::set_mint_limits(&owner.pubkey(), 2, 1, 1)], &[&owner]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::BadToken));
    w.h.send("pausable: set_mint_limits", &[p::set_mint_limits(&owner.pubkey(), 0, 10_000_000_000, 30_000_000_000)], &[&owner]).await.unwrap();
    let s = w.state().await;
    assert_eq!((s.limits[0].max_per_message, s.limits[0].day_cap, s.limits[0].used), (10_000_000_000, 30_000_000_000, max));
    assert_eq!((s.limits[1].max_per_message, s.limits[1].day_cap), (max, cap), "the other token's untouched");
    let e = w.deliver(&gnot, &token(20_000_000_000)).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::OverMax));
    let e = w.deliver(&gnot, &token(10_000_000_000)).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::OverDayCap), "25,000 in the bucket already: 10,000 more passes the 30,000 day");
    w.h.advance(day()).await;
    w.deliver(&gnot, &token(10_000_000_000)).await.unwrap();
    // a direct Verify, outside any delivery, is refused — the mailbox's per-ISM process authority did not sign
    // — and charges nothing
    let before = w.state().await.limits[0].used;
    let (message, r) = w.deliver_keeping(&gnot, &token(1), true).await;
    r.unwrap();
    let stage = c::proof_stage(&w.submitter.pubkey(), &sha256(&[&message]));
    let direct = p::verify(&CLIENT, &c::state(), vec![solana_sdk::instruction::AccountMeta::new_readonly(stage, false)], w.submitter.pubkey().to_bytes().to_vec(), message.clone());
    let e = w.h.send("pausable: verify (direct)", &[direct], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::NotTheMailbox));
    assert_eq!(w.state().await.limits[0].used, before + 1, "the delivery charged one unit; the direct call nothing");
    w.h.send("client: close proof", &[c::stage_close(&stage, &w.submitter.pubkey())], &[&w.submitter]).await.unwrap();
    // what stood before the limits still stands, read through a delivery: a flag pauses, a quarantine pauses
    w.h.send("pausable: set_flag", &[p::set_flag(&w.watch[1].pubkey())], &[&w.watch[1]]).await.unwrap();
    let e = w.deliver(&gnot, &token(1)).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::Paused));
    w.h.send("pausable: clear_flag", &[p::clear_flag(&w.watch[1].pubkey())], &[&w.watch[1]]).await.unwrap();
    w.deliver(&gnot, &token(1)).await.unwrap();
    w.rotate(testkit::validators(8, &[60, 60, 60, 60, 60])).await;
    let e = w.deliver(&gnot, &token(1)).await.unwrap_err();
    assert_eq!(custom_code(&e), code(PausableError::Quarantine));
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.tick().await;
    w.deliver(&gnot, &token(1)).await.unwrap();
    let _ = ClientError::NotInitialized;
    w.h.report("pausable: the mint limits");
}

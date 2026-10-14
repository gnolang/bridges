#![allow(deprecated)]
//! The admin program under program-test with the real mailbox build (Hyperlane's mainnet mailbox.so and the SPL
//! noop, dumped by bridge/local/dump-programs.py), the light client and the pausable ISM: decisions delivered
//! through the mailbox under the light-client ISM, the clocks at the compiled factor, flags and holds, kind 5
//! through a hold, cancels (at recording, by list and by range), the upgrade with an extension, and the transfer
//! to a successor. One channel since M2j: every change on Solana is a light-client proof of a GovDAO decision.
//!
//! Run through ../../../test.sh (SBF_OUT_DIR at the built .so files).

use admin_program::{
    config::{CLIENT, CLOCK_FACTOR, DECISION_OVERHEAD, HLGOV_SENDER, MAILBOX, PAUSABLE, TOKENS},
    error::AdminError,
    instruction::Instruction as AdminIx,
    kinds::Params,
    state::{Decision, Hold, Range, State},
    ID as ADMIN,
};
use gno_light_core::{sha256, testkit, testkit::TestValidator, valset::ValidatorSet};
use sol_util::{client_head::Head, clock, guardians::{Flag, Guardians}, hl, loader};
use solana_sdk::{
    account::Account,
    instruction::{AccountMeta, InstructionError},
    pubkey::Pubkey,
    signature::{Keypair, Signer},
    transaction::{Transaction, TransactionError},
};
use solana_system_interface::program as system_program;
use std::sync::atomic::{AtomicU64, Ordering};
use testlib::{admin as a, client as c, custom_code, keypair, pausable as p, tokio, Harness, ProgramSpec};

const CHAIN: &str = "gno-test-1";
const ORIGIN: u32 = 1198419788;
const LOCAL: u32 = 1399811151;
const REF: &str = "/Users/jae/.config/bubblerumble-jarvis/bridge-ref/so";

fn code(e: AdminError) -> Option<u32> {
    Some(3000 + e as u32)
}

fn grace() -> i64 {
    clock::unpause_delay(CLOCK_FACTOR)
}

/// A cancel's parameters under a salt of this run's own (§13: kind 1's body is `salt u64 ‖ form u8 ‖ …`, every
/// cancel's body its own; the program ignores the value).
fn cancel(ranges: Vec<(u64, u64)>) -> Params {
    static SALT: AtomicU64 = AtomicU64::new(0x5a17_0000);
    Params::Cancel { salt: SALT.fetch_add(1, Ordering::Relaxed), ranges }
}

/// Everything deployed and set up: the client on a synthetic chain, the pausable ISM, the admin program owning
/// all of it, the mailbox with the pausable ISM as its default ISM.
struct World {
    h: Harness,
    authority: Keypair,
    /// The client's current set.
    vals: Vec<TestValidator>,
    set: ValidatorSet,
    keys: Vec<[u8; 32]>,
    seats: Vec<Keypair>,
    watch: Vec<Keypair>,
    submitter: Keypair,
    height: i64,
    last_hash: [u8; 32],
    next_seq: u64,
    successor: Pubkey,
    /// Two more copies of the admin program for S-18's never-inheriting successors: one immutable from its
    /// deployment and never initialised, one initialised naming another predecessor (then burned, in the test).
    orphan: Pubkey,
    stray: Pubkey,
    /// A spare upgradeable program (the pausable ISM's build at another id) for the kind-8 test.
    spare: Pubkey,
}

impl World {
    async fn new() -> World {
        World::new_from(0).await
    }

    /// The world with the admin program's sequence started at `start` (AT3: 0 for a chain's first deployment,
    /// r/hlgov's number for a fresh deployment under the same r/hlgov).
    async fn new_from(start: u64) -> World {
        let authority = keypair(1);
        let successor = Pubkey::new_from_array([0x77; 32]);
        let orphan = Pubkey::new_from_array([0x78; 32]);
        let stray = Pubkey::new_from_array([0x79; 32]);
        let spare = Pubkey::new_from_array([0x99; 32]);
        let mut programs = vec![
            ProgramSpec { name: "pausable_ism", id: spare, authority: Some(authority.pubkey()), path: None },
            ProgramSpec { name: "gno_light_client_ism", id: CLIENT, authority: Some(authority.pubkey()), path: None },
            ProgramSpec { name: "pausable_ism", id: PAUSABLE, authority: Some(authority.pubkey()), path: None },
            ProgramSpec { name: "admin_program", id: ADMIN, authority: Some(authority.pubkey()), path: None },
            // a second copy, kind 9's successor: initialised under the test authority, then burned (immutable)
            ProgramSpec { name: "admin_program", id: successor, authority: Some(authority.pubkey()), path: None },
            // S-18's: the orphan has no upgrade authority from the start (nothing can ever sign its init), the
            // stray is initialised in its test naming another predecessor and burned
            ProgramSpec { name: "admin_program", id: orphan, authority: None, path: None },
            ProgramSpec { name: "admin_program", id: stray, authority: Some(authority.pubkey()), path: None },
            ProgramSpec { name: "mailbox", id: MAILBOX, authority: Some(authority.pubkey()), path: Some(format!("{REF}/mailbox.so")) },
            ProgramSpec { name: "noop", id: a::NOOP, authority: None, path: Some(format!("{REF}/noop.so")) },
        ];
        // the token programs: Hyperlane's build, upgradeable under the deployer; their PDAs are made by hand
        // (the plugin's mint setup is Token-2022's, outside these tests)
        for t in TOKENS {
            programs.push(ProgramSpec { name: "token", id: t, authority: Some(authority.pubkey()), path: Some(format!("{REF}/token.so")) });
        }
        // the loader as mainnet has it: no ExtendProgramChecked (the audit's F1)
        let h = Harness::new_with_features(&programs, vec![], &[testlib::ENABLE_EXTEND_PROGRAM_CHECKED]).await;
        let vals = testkit::validators(7, &[60, 60, 60, 60]);
        let set = testkit::set_of(&vals);
        let keys: Vec<[u8; 32]> = vals.iter().map(|v| v.pubkey()).collect();
        let seats: Vec<Keypair> = (10..14).map(keypair).collect();
        let watch: Vec<Keypair> = (14..16).map(keypair).collect();
        let submitter = keypair(4);
        let mut w = World { h, authority, vals, set, keys, seats, watch, submitter, height: 100, last_hash: [0; 32], next_seq: start + 1, successor, orphan, stray, spare };
        for k in w.seats.iter().chain(w.watch.iter()) {
            w.h.fund(&k.pubkey(), 100_000_000).await;
        }
        w.h.fund(&w.submitter.pubkey(), 5_000_000_000).await;
        let payer = w.h.payer();
        // the client
        let h100 = testkit::header(CHAIN, 100, 1_700_000_000, &w.set, &w.set, None, [0; 32]);
        w.last_hash = h100.hash(sha256);
        w.h.send("client: init", &[c::init(&payer.pubkey(), &w.authority.pubkey(), CHAIN, ORIGIN, 100, w.last_hash, w.set.validators.clone(), a::authority())], &[&w.authority]).await.unwrap();
        for _ in 0..3 {
            w.h.send("client: grow", &[c::grow(&payer.pubkey())], &[]).await.unwrap();
        }
        // the pausable ISM, owned by the admin PDA
        let seat_keys = [w.seats[0].pubkey(), w.seats[1].pubkey(), w.seats[2].pubkey(), w.seats[3].pubkey()];
        let watch_keys = [w.watch[0].pubkey(), w.watch[1].pubkey()];
        w.h.send("pausable: init", &[p::init(&payer.pubkey(), &w.authority.pubkey(), a::authority(), CLIENT, 1, seat_keys, watch_keys)], &[&w.authority]).await.unwrap();
        // the admin program, its sequence started where the deployment's r/hlgov stands (AT3)
        w.h.send("admin: init", &[a::init(&payer.pubkey(), &w.authority.pubkey(), seat_keys, watch_keys, ORIGIN, start)], &[&w.authority]).await.unwrap();
        assert_eq!(w.state().await.last_seq, start, "the sequence starts at the number init was given");
        // the successor: its own init names its predecessor and no seats and no number (C227, AT3), then its
        // authority is burned
        let e = w.h.send("successor: init", &[a::init_of(&w.successor, &payer.pubkey(), &w.authority.pubkey(), seat_keys, watch_keys, ORIGIN, ADMIN, 0)], &[&w.authority]).await.unwrap_err();
        assert_eq!(custom_code(&e), code(AdminError::BadSeat), "a successor's init takes no seats");
        let e = w.h.send("successor: init", &[a::init_of(&w.successor, &payer.pubkey(), &w.authority.pubkey(), [Pubkey::default(); 4], [Pubkey::default(); 2], ORIGIN, ADMIN, start + 1)], &[&w.authority]).await.unwrap_err();
        assert_eq!(custom_code(&e), code(AdminError::OutOfOrder), "a successor's init takes no number: successor_init writes its predecessor's");
        w.h.send("successor: init", &[a::init_successor(&w.successor, &payer.pubkey(), &w.authority.pubkey(), ORIGIN, &ADMIN)], &[&w.authority]).await.unwrap();
        let burn = loader::set_authority(&loader::programdata_address(&w.successor), &w.authority.pubkey(), None);
        w.h.send("loader: burn (successor)", &[burn], &[&w.authority]).await.unwrap();
        // the mailbox: our domain, the pausable ISM as the default, the fee cap of the plan
        w.h.send("mailbox: init", &[a::mailbox_init(&payer.pubkey(), LOCAL, &PAUSABLE, 50_000_000, 0, &payer.pubkey())], &[]).await.unwrap();
        w
    }

    /// The setup step: owners and upgrade authorities to the admin program, its own burned, then finish_setup.
    async fn setup(&mut self) {
        self.setup_steps().await;
        self.h.send("admin: finish_setup", &[a::finish_setup()], &[]).await.unwrap();
        assert_eq!(self.state().await.setup_done, 1);
        let e = self.h.send("admin: transfer_authority_in", &[a::transfer_authority_in(&CLIENT, &self.authority.pubkey())], &[&self.authority]).await.unwrap_err();
        assert_eq!(custom_code(&e), code(AdminError::SetupDone));
    }

    /// The setup step up to and including the burn of the admin program's own authority: everything finish_setup
    /// reads is in place.
    async fn setup_steps(&mut self) {
        let payer = self.h.payer();
        // owners: the mailbox (its init made the payer the owner)
        self.h.send("mailbox: transfer_ownership", &[hl::mailbox_transfer_ownership(&MAILBOX, &payer.pubkey(), Some(a::authority()))], &[]).await.unwrap();
        // the tokens' PDAs, written by hand in the token's layout with the admin PDA as owner and the pausable ISM
        // as the ISM pointer (Hyperlane's Init needs Token-2022's mint setup)
        for t in TOKENS {
            let bump = Pubkey::find_program_address(hl::HANDLE_ACCOUNT_METAS_SEEDS, &t).1;
            let dispatch_bump = Pubkey::find_program_address(&[b"hyperlane_dispatcher", b"-", b"dispatch_authority"], &t).1;
            let mut data = vec![1u8, bump];
            data.extend_from_slice(MAILBOX.as_ref());
            data.extend_from_slice(hl::mailbox_process_authority(&MAILBOX, &t).as_ref());
            data.extend_from_slice(&[dispatch_bump, 6, 6]);
            data.push(1);
            data.extend_from_slice(a::authority().as_ref());
            data.push(1);
            data.extend_from_slice(PAUSABLE.as_ref());
            data.push(0); // igp None
            data.extend_from_slice(&0u32.to_le_bytes()); // destination gas
            data.extend_from_slice(&0u32.to_le_bytes()); // remote routers
            data.extend_from_slice(&[0; 34]); // the synthetic plugin's mint and bumps (unused here)
            // no slack, exact rent: a growth (an enrolled router) must realloc, and the owner — the authority
            // PDA — pays the added rent (M2-NOTES 17b)
            let acc = Account { lamports: solana_sdk::rent::Rent::default().minimum_balance(data.len()), data, owner: t, executable: false, rent_epoch: 0 };
            self.h.ctx.set_account(&hl::token_pda(&t), &acc.into());
        }
        // the authority PDA's float (hlsol setup funds ≈0.01 SOL): what those top-ups draw on
        self.h.fund(&a::authority(), 10_000_000).await;
        // upgrade authorities: through the admin program, the deployer signing (the spare too)
        for prog in [MAILBOX, TOKENS[0], TOKENS[1], CLIENT, PAUSABLE, self.spare] {
            self.h.send("admin: transfer_authority_in", &[a::transfer_authority_in(&prog, &self.authority.pubkey())], &[&self.authority]).await.unwrap();
        }
        // not done yet: the admin program's own authority stands
        let e = self.h.send("admin: finish_setup", &[a::finish_setup()], &[]).await.unwrap_err();
        assert_eq!(custom_code(&e), code(AdminError::SetupIncomplete));
        // the admin program's own programdata is never transferred in (S-05): its authority is burned, and one
        // moved to its own PDA could never be moved again, so finish_setup would never flip
        let e = self.h.send("admin: transfer_authority_in (own)", &[a::transfer_authority_in(&ADMIN, &self.authority.pubkey())], &[&self.authority]).await.unwrap_err();
        assert_eq!(custom_code(&e), code(AdminError::WrongAccount));
        let (_, own) = loader::parse_programdata(&self.h.data(&loader::programdata_address(&ADMIN)).await).unwrap();
        assert_eq!(own, Some(self.authority.pubkey()), "untouched");
        // burn it with the loader directly
        let burn = loader::set_authority(&loader::programdata_address(&ADMIN), &self.authority.pubkey(), None);
        self.h.send("loader: burn", &[burn], &[&self.authority]).await.unwrap();
    }

    /// Doctors one account's bytes in the bank (writes `with` at `at`); the original, for `restore`.
    async fn doctor(&mut self, key: &Pubkey, at: usize, with: &[u8]) -> Account {
        let orig = self.h.account(key).await.unwrap();
        let mut acc = orig.clone();
        acc.data[at..at + with.len()].copy_from_slice(with);
        self.h.ctx.set_account(key, &acc.into());
        orig
    }

    fn restore(&mut self, key: &Pubkey, orig: Account) {
        self.h.ctx.set_account(key, &orig.into());
    }

    async fn state(&mut self) -> State {
        State::load(&self.h.data(&a::state()).await).unwrap()
    }

    async fn decision(&mut self, number: u64) -> Option<Decision> {
        self.h.account(&a::decision(number)).await.and_then(|acc| if acc.data.is_empty() { None } else { Some(Decision::load(&acc.data).unwrap()) })
    }

    /// A message from r/hlgov to the admin program.
    fn message(&self, kind: u8, seq: u64, params: &Params) -> Vec<u8> {
        hl::encode_message(seq as u32, ORIGIN, &HLGOV_SENDER, LOCAL, &ADMIN.to_bytes(), &a::body(kind, seq, params))
    }

    /// Delivers messages through the mailbox with the given extra Handle accounts after the four `handle` always
    /// reads (the state, the decision, the system program, the payer).
    async fn deliver_with(&mut self, messages: &[Vec<u8>], extra: Vec<Vec<AccountMeta>>) -> Vec<Result<(), TransactionError>> {
        let payer = self.h.payer();
        let handle: Vec<Vec<AccountMeta>> = messages
            .iter()
            .enumerate()
            .map(|(i, m)| {
                let seq = gno_light_core::decision::decision_prefix(&m[hl::MESSAGE_HEADER_LEN..]).map(|(_, s)| s).unwrap_or(0);
                a::handle_accounts(&payer.pubkey(), seq, extra.get(i).cloned().unwrap_or_default())
            })
            .collect();
        self.deliver_handle(messages, handle).await
    }

    /// Delivers messages through the mailbox, each with its Handle accounts given whole: one block carries their
    /// dispatches, the client is updated to the header after it, each is proven by the ring and processed.
    async fn deliver_handle(&mut self, messages: &[Vec<u8>], handle: Vec<Vec<AccountMeta>>) -> Vec<Result<(), TransactionError>> {
        self.deliver_handle_to(&ADMIN, messages, handle).await
    }

    /// As `deliver_handle`, to an admin program at `recipient` (the launch one or the successor).
    async fn deliver_handle_to(&mut self, recipient: &Pubkey, messages: &[Vec<u8>], handle: Vec<Vec<AccountMeta>>) -> Vec<Result<(), TransactionError>> {
        let dispatches: Vec<_> = messages
            .iter()
            .map(|m| {
                let msg = hl::Message::parse(m).unwrap();
                gno_light_core::results::Dispatch { id: hl::Message::id(m), nonce: msg.nonce, origin: msg.origin, destination: msg.destination, sender: msg.sender, recipient: msg.recipient, hash: sha256(&[m]) }
            })
            .collect();
        let leaves = testkit::dispatch_leaves(gno_light_client_ism::config::MAILBOX_PKG_PATH, &dispatches, 1);
        let (root, proof) = testkit::results_proof(&leaves, 0);
        // the dispatch block, then the header committing to its results
        let h1 = testkit::header(CHAIN, self.height + 1, 1_700_000_000 + (self.height + 1) * 10, &self.set, &self.set, None, self.last_hash);
        let h2 = testkit::header(CHAIN, self.height + 2, 1_700_000_000 + (self.height + 2) * 10, &self.set, &self.set, Some(root), h1.hash(sha256));
        for hh in [&h1, &h2] {
            let (cm, sg) = testkit::sign(CHAIN, hh, &self.vals, &[0, 1, 2, 3]);
            for tx in c::update_txs(&self.submitter.pubkey(), CHAIN, hh, &cm, &self.keys, &sg, None, 3) {
                self.h.send("client: update", &tx, &[&self.submitter]).await.unwrap();
            }
        }
        self.height += 2;
        self.last_hash = h2.hash(sha256);
        let payer = self.h.payer();
        let mut out = Vec::new();
        for (i, m) in messages.iter().enumerate() {
            let empty = gno_light_core::commit::Commit::default();
            for tx in c::proof_txs(&self.submitter.pubkey(), CHAIN, None, h2.hash(sha256), &empty, &self.keys, &[], m, proof.clone(), &leaves[0], 3) {
                self.h.send("client: stage proof (ring)", &tx, &[&self.submitter]).await.unwrap();
            }
            let r = self.h.send("mailbox: process -> client verify -> admin handle", &[a::process_to(recipient, &payer.pubkey(), m, &self.submitter.pubkey(), handle[i].clone())], &[]).await.map(|_| ());
            self.h.send("client: close proof", &[c::stage_close(&c::proof_stage(&self.submitter.pubkey(), &sha256(&[m])), &self.submitter.pubkey())], &[&self.submitter]).await.unwrap();
            out.push(r);
        }
        out
    }

    async fn deliver(&mut self, messages: &[Vec<u8>]) -> Vec<Result<(), TransactionError>> {
        self.deliver_with(messages, vec![]).await
    }

    /// Delivers one decision of the given kind, taking the next sequence number.
    async fn decide(&mut self, kind: u8, params: &Params) -> u64 {
        self.decide_with(kind, params, vec![]).await
    }

    async fn decide_with(&mut self, kind: u8, params: &Params, extra: Vec<AccountMeta>) -> u64 {
        let seq = self.next_seq;
        let m = self.message(kind, seq, params);
        self.deliver_with(&[m], vec![extra]).await.remove(0).unwrap();
        self.next_seq += 1;
        seq
    }

    /// Executes a decision; its deliverer is the payer.
    async fn execute(&mut self, number: u64, kind: u8, params: &Params) -> Result<u64, TransactionError> {
        let payer = self.h.payer();
        let (extra, fw) = a::extra_for(kind, params);
        let label = format!("admin: execute kind {kind}");
        self.h.send(&label, &[a::execute(number, &payer.pubkey(), &payer.pubkey(), extra, fw)], &[]).await
    }
}

#[tokio::test]
async fn setup_then_decisions_arrive_through_the_mailbox_and_run_on_their_clocks() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();

    // a decision from another sender is refused by Handle (the message stays deliverable)
    let bad_sender = hl::encode_message(1, ORIGIN, &[9; 32], LOCAL, &ADMIN.to_bytes(), &a::body(7, 1, &Params::SetFee { lamports: 1, beneficiary: payer.pubkey() }));
    let r = w.deliver(&[bad_sender]).await.remove(0).unwrap_err();
    assert_eq!(custom_code(&r), code(AdminError::WrongSender));
    // a body shorter than nine bytes fails Handle
    let short = hl::encode_message(2, ORIGIN, &HLGOV_SENDER, LOCAL, &ADMIN.to_bytes(), &[7, 0, 0]);
    let r = w.deliver(&[short]).await.remove(0).unwrap_err();
    assert_eq!(custom_code(&r), code(AdminError::ShortBody));
    // out of order: number 2 before 1 is refused, 1 is recorded, 2 then lands; 1 again is ignored
    let m2 = w.message(7, 2, &Params::SetFee { lamports: 1000, beneficiary: payer.pubkey() });
    let m1 = w.message(7, 1, &Params::SetFee { lamports: 500, beneficiary: payer.pubkey() });
    let r = w.deliver(&[m2.clone()]).await.remove(0).unwrap_err();
    assert_eq!(custom_code(&r), code(AdminError::OutOfOrder));
    w.deliver(&[m1.clone(), m2]).await.into_iter().for_each(|r| r.unwrap());
    assert_eq!(w.state().await.last_seq, 2);
    assert!(w.decision(1).await.is_some() && w.decision(2).await.is_some());
    // the mailbox refuses a second delivery of the same id, so a re-dispatch is a fresh message of the same number
    w.next_seq = 3;
    let m1b = hl::encode_message(99, ORIGIN, &HLGOV_SENDER, LOCAL, &ADMIN.to_bytes(), &a::body(7, 1, &Params::SetFee { lamports: 500, beneficiary: payer.pubkey() }));
    w.deliver(&[m1b]).await.remove(0).unwrap();
    assert_eq!(w.state().await.last_seq, 2, "a number at or below the last is accepted and ignored");
    // an unknown kind is recorded and spent: the sequence moves, no account
    let unknown = hl::encode_message(100, ORIGIN, &HLGOV_SENDER, LOCAL, &ADMIN.to_bytes(), &a::body(42, 3, &Params::Unpause(0)));
    w.deliver(&[unknown]).await.remove(0).unwrap();
    assert_eq!(w.state().await.last_seq, 3);
    assert!(w.decision(3).await.is_none());
    w.next_seq = 4;

    // the 7-day group waits 7 days
    let fee = Params::SetFee { lamports: 500, beneficiary: payer.pubkey() };
    let e = w.execute(1, 7, &fee).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    w.h.advance(clock::week(CLOCK_FACTOR) - 5).await;
    let e = w.execute(1, 7, &fee).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    w.h.advance(6).await;
    // a lone flag defers it
    w.h.send("admin: set_flag", &[a::set_flag(&w.watch[0].pubkey())], &[&w.watch[0]]).await.unwrap();
    let e = w.execute(1, 7, &fee).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::Deferred));
    w.h.send("admin: clear_flag", &[a::clear_flag(&w.watch[0].pubkey())], &[&w.watch[0]]).await.unwrap();
    let before = w.h.lamports(&payer.pubkey()).await;
    w.execute(1, 7, &fee).await.unwrap();
    let ob = hl::Outbox::parse(&w.h.data(&hl::mailbox_outbox(&MAILBOX)).await).unwrap();
    assert_eq!((ob.fee, ob.beneficiary, ob.owner), (500, payer.pubkey(), Some(a::authority())));
    assert!(w.decision(1).await.is_none(), "closed, its rent back to the deliverer");
    assert!(w.h.lamports(&payer.pubkey()).await > before - 10_000);
    // a fee above the maximum is spent without effect
    let big = Params::SetFee { lamports: 50_000_001, beneficiary: payer.pubkey() };
    let n = w.decide(7, &big).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(n, 7, &big).await.unwrap();
    assert!(w.decision(n).await.is_none());
    assert_eq!(hl::Outbox::parse(&w.h.data(&hl::mailbox_outbox(&MAILBOX)).await).unwrap().fee, 500);

    // kind 1 (decision 2 is still pending): a cancel executes in the transaction that records it, closing the
    // decision whose account the relayer passed, to its deliverer; its body carries the salt r/hlgov prepends to
    // every cancel (v27 §13), which the program reads and ignores, and the channel byte, 0 (M2j: the one channel)
    let c2 = cancel(vec![(2, 2)]);
    let Params::Cancel { salt, .. } = c2 else { unreachable!() };
    assert_eq!(&a::body(1, 0, &c2)[9..20], &[salt.to_be_bytes().as_slice(), &[0, 1, 0]].concat()[..], "salt u64 BE, then form 0 (a list), a count of 1, channel 0");
    let before = w.h.lamports(&payer.pubkey()).await;
    let n = w.decide_with(1, &c2, a::cancel_pairs(&[(2, payer.pubkey())])).await;
    assert!(w.decision(2).await.is_none() && w.decision(n).await.is_none());
    assert!(w.h.lamports(&payer.pubkey()).await > before - 50_000, "the rent came back");
    assert!(w.state().await.cancelled.is_empty(), "closed now: nothing stored");
    // a cancel naming any channel but 0 — the plan's recovery channel before M2j, or any byte — does not parse
    // and is spent
    let n7 = w.decide(7, &Params::SetFee { lamports: 7, beneficiary: payer.pubkey() }).await;
    let seq = w.next_seq;
    let mut body = vec![1u8];
    body.extend_from_slice(&seq.to_be_bytes());
    body.extend_from_slice(&0x5a17_ffffu64.to_be_bytes());
    body.extend_from_slice(&[0, 1, 1]); // form 0, one entry, channel 1
    body.extend_from_slice(&n7.to_be_bytes());
    let m = hl::encode_message(seq as u32, ORIGIN, &HLGOV_SENDER, LOCAL, &ADMIN.to_bytes(), &body);
    w.deliver(&[m]).await.remove(0).unwrap();
    w.next_seq += 1;
    assert_eq!(w.state().await.last_seq, seq);
    assert!(w.decision(seq).await.is_none());
    assert!(w.state().await.cancelled.is_empty() && w.decision(n7).await.is_some(), "spent: nothing cancelled");
    // a list cancel without the accounts is stored, and a later Execute closes the decision as cancelled
    let _ = w.decide(1, &cancel(vec![(n7, n7)])).await;
    assert!(w.decision(n7).await.is_some());
    assert_eq!(w.state().await.cancelled.len(), 1);
    w.execute(n7, 7, &Params::SetFee { lamports: 7, beneficiary: payer.pubkey() }).await.unwrap();
    assert!(w.decision(n7).await.is_none());
    assert_eq!(hl::Outbox::parse(&w.h.data(&hl::mailbox_outbox(&MAILBOX)).await).unwrap().fee, 500, "cancelled, not executed");

    // kinds 3, 4, 6 against the real programs; the authority PDA's lamports watched for the rent a growth of a
    // token account draws from it
    let pda0 = w.h.lamports(&a::authority()).await;
    let n = w.decide(3, &Params::SetTokenIsm { token: 0, ism: Pubkey::default() }).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(n, 3, &Params::SetTokenIsm { token: 0, ism: Pubkey::default() }).await.unwrap();
    assert_eq!(hl::token_ism(&w.h.data(&hl::token_pda(&TOKENS[0])).await).unwrap(), None);
    let n = w.decide(3, &Params::SetTokenIsm { token: 0, ism: PAUSABLE }).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(n, 3, &Params::SetTokenIsm { token: 0, ism: PAUSABLE }).await.unwrap();
    assert_eq!(hl::token_ism(&w.h.data(&hl::token_pda(&TOKENS[0])).await).unwrap(), Some(PAUSABLE));
    // an ISM that is neither the pausable ISM nor zero: spent
    let n = w.decide(3, &Params::SetTokenIsm { token: 1, ism: CLIENT }).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(n, 3, &Params::SetTokenIsm { token: 1, ism: CLIENT }).await.unwrap();
    assert_eq!(hl::token_ism(&w.h.data(&hl::token_pda(&TOKENS[1])).await).unwrap(), Some(PAUSABLE));
    let pda1 = w.h.lamports(&a::authority()).await;
    println!("authority PDA: the ISM pointer cleared and set again drew {} lamports", pda0 - pda1);
    let router = Params::EnrollRouter { token: 1, domain: ORIGIN, router: [0xab; 32] };
    let n = w.decide(6, &router).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    let len_before = w.h.data(&hl::token_pda(&TOKENS[1])).await.len();
    w.execute(n, 6, &router).await.unwrap();
    let (pda2, len_after) = (w.h.lamports(&a::authority()).await, w.h.data(&hl::token_pda(&TOKENS[1])).await.len());
    let rent = solana_sdk::rent::Rent::default();
    assert!(len_after > len_before, "the router map outgrew the account");
    assert_eq!(pda1 - pda2, rent.minimum_balance(len_after) - rent.minimum_balance(len_before), "the authority PDA paid the growth's rent");
    println!("authority PDA: an enrolled router grew the token account by {} bytes ({} to {}); the PDA paid {} lamports at program-test's rent", len_after - len_before, len_before, len_after, pda1 - pda2);
    // kind 4 is reserved (v27 §13): recorded as any decision of the 7-day group — here in its v26 form, the
    // launch client's id and a version — and spent at execution; the pausable ISM's version stays 1
    let seq = w.next_seq;
    let mut body4 = vec![4u8];
    body4.extend_from_slice(&seq.to_be_bytes());
    body4.extend_from_slice(CLIENT.as_ref());
    body4.push(2);
    let m4 = hl::encode_message(seq as u32, ORIGIN, &HLGOV_SENDER, LOCAL, &ADMIN.to_bytes(), &body4);
    w.deliver(&[m4]).await.remove(0).unwrap();
    w.next_seq += 1;
    assert!(w.decision(seq).await.is_some(), "recorded, pending its 7 days");
    let e = w.h.send("admin: execute kind 4", &[a::execute(seq, &payer.pubkey(), &payer.pubkey(), vec![], false)], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.h.send("admin: execute kind 4 (spent)", &[a::execute(seq, &payer.pubkey(), &payer.pubkey(), vec![], false)], &[]).await.unwrap();
    assert!(w.decision(seq).await.is_none(), "spent");
    let ps = pausable_ism::state::State::load(&w.h.data(&p::state()).await).unwrap();
    assert_eq!((ps.target, ps.target_version), (CLIENT, 1), "the layout version is 1 for the deployment's life");
    w.h.report("admin: setup and decisions");
}

/// finish_setup answers SetupIncomplete and flips nothing.
async fn refused(w: &mut World) {
    let e = w.h.send("admin: finish_setup", &[a::finish_setup()], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::SetupIncomplete));
    assert_eq!(w.state().await.setup_done, 0);
}

#[tokio::test]
async fn finish_setup_requires_the_three_programs_to_agree_and_records_nothing() {
    let mut w = World::new().await;
    w.setup_steps().await;
    // the pausable ISM's guardian list differing by one key (its flags account doctored in the bank: seat 1's
    // key sits after the version byte)
    let orig = w.doctor(&p::flags(), 1, keypair(99).pubkey().as_ref()).await;
    refused(&mut w).await;
    w.restore(&p::flags(), orig);
    // the client's clock factor not this build's (the head's second byte)
    let orig = w.doctor(&c::state(), 1, &[CLOCK_FACTOR as u8 ^ 9]).await;
    refused(&mut w).await;
    w.restore(&c::state(), orig);
    // the pausable ISM's clock factor not this build's (its state's second byte)
    let orig = w.doctor(&p::state(), 1, &[CLOCK_FACTOR as u8 ^ 9]).await;
    refused(&mut w).await;
    w.restore(&p::state(), orig);
    // the client's origin domain not this program's (borsh: the admin key, the chain id, then the domain)
    let at = sol_util::client_head::HEAD_LEN + 32 + 4 + CHAIN.len();
    let orig = w.doctor(&c::state(), at, &(ORIGIN + 1).to_le_bytes()).await;
    refused(&mut w).await;
    w.restore(&c::state(), orig);
    // agreeing, it flips — and records nothing (M2j): the state is the account its init made, 1,574 bytes with
    // every store at its bound, and holds no validator key or set hash; the launch set lives in the client alone
    w.h.send("admin: finish_setup", &[a::finish_setup()], &[]).await.unwrap();
    let s = w.state().await;
    assert_eq!(s.setup_done, 1);
    let data = w.h.data(&a::state()).await;
    assert_eq!((data.len(), State::init_size()), (1_574, 1_574));
    assert_eq!(s.size(), 262, "the stores empty, no flag, no hold, no successor");
    assert_eq!(State::init_size() - s.size(), 6 * 16 + 16 + 16 + 32 + 64 * 16 + 8 * 16, "the reserve: six flags, a hold, a dip, a successor, 64 ranges and eight holds");
    let set_hash = w.set.hash(sha256).unwrap();
    for k in w.keys.iter().chain(std::iter::once(&set_hash)) {
        assert!(!data.windows(32).any(|x| x == k), "the admin state never holds a validator key or a set hash");
    }
    w.h.report("admin: finish_setup's agreement");
}

#[tokio::test]
async fn handle_account_metas_answer_the_order_handle_reads() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();
    // the relayer's question, simulated: the state, the decision PDA and the system program, for a fee decision
    // and for a cancel alike — the payer goes after them, and a cancel's authority PDA and pairs after the payer,
    // as handle reads them (a cancel's authority PDA answered before the payer once failed every cancel)
    let fee_msg = w.message(7, 1, &Params::SetFee { lamports: 9, beneficiary: payer.pubkey() });
    let cancel_msg = w.message(1, 2, &cancel(vec![(1, 1)]));
    let mut answers = Vec::new();
    for (m, seq) in [(&fee_msg, 1u64), (&cancel_msg, 2)] {
        let blockhash = w.h.ctx.banks_client.get_latest_blockhash().await.unwrap();
        let tx = Transaction::new_signed_with_payer(&[a::handle_account_metas(m)], Some(&payer.pubkey()), &[&payer], blockhash);
        let sim = w.h.ctx.banks_client.simulate_transaction(tx).await.unwrap();
        let rd = sim.simulation_details.unwrap().return_data.unwrap().data;
        let answer: hl::SimulationReturnData<Vec<hl::SerializableAccountMeta>> = borsh::BorshDeserialize::try_from_slice(&rd).unwrap();
        let metas: Vec<AccountMeta> = answer.return_data.into_iter().map(|m| AccountMeta { pubkey: m.pubkey, is_signer: m.is_signer, is_writable: m.is_writable }).collect();
        assert_eq!(metas, vec![AccountMeta::new(a::state(), false), AccountMeta::new(a::decision(seq), false), AccountMeta::new_readonly(system_program::ID, false)]);
        answers.push(metas);
    }
    // the fee decision delivered with the answer and the payer appended
    let mut h1 = answers[0].clone();
    h1.push(AccountMeta::new(payer.pubkey(), true));
    w.deliver_handle(&[fee_msg], vec![h1]).await.remove(0).unwrap();
    assert!(w.decision(1).await.is_some());
    // the cancel delivered with the answer, the payer, then the authority PDA and the pair: decision 1 closed now
    let mut h2 = answers[1].clone();
    h2.push(AccountMeta::new(payer.pubkey(), true));
    h2.extend(a::cancel_pairs(&[(1, payer.pubkey())]));
    w.deliver_handle(&[cancel_msg], vec![h2]).await.remove(0).unwrap();
    assert!(w.decision(1).await.is_none());
    assert_eq!(w.state().await.last_seq, 2);
    w.h.report("admin: handle account metas");
}

#[tokio::test]
async fn unpause_in_24_hours_and_kind_5_through_a_hold() {
    let mut w = World::new().await;
    w.setup().await;
    // a watch key pauses the pausable ISM; GovDAO's unpause clears it 24 hours after delivery, but not a flag set
    // after the delivery
    w.h.send("pausable: set_flag", &[p::set_flag(&w.watch[0].pubkey())], &[&w.watch[0]]).await.unwrap();
    let unpause = Params::Unpause(0);
    let n = w.decide(2, &unpause).await;
    w.h.advance(10).await;
    w.h.send("pausable: set_flag", &[p::set_flag(&w.seats[0].pubkey())], &[&w.seats[0]]).await.unwrap();
    let e = w.execute(n, 2, &unpause).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    w.h.advance(clock::unpause_delay(CLOCK_FACTOR)).await;
    w.execute(n, 2, &unpause).await.unwrap();
    let f = pausable_ism::state::Flags::load(&w.h.data(&p::flags()).await).unwrap();
    assert!(f.guardians.flags[4].is_none(), "the watch flag, set before delivery, is cleared");
    assert!(f.guardians.flags[0].is_some(), "the guardian's later flag stands");
    // a hold on the pausable ISM: the unpause is spent without effect
    w.h.send("pausable: set_flag", &[p::set_flag(&w.seats[1].pubkey())], &[&w.seats[1]]).await.unwrap();
    let n = w.decide(2, &unpause).await;
    w.h.advance(clock::unpause_delay(CLOCK_FACTOR) + 1).await;
    w.execute(n, 2, &unpause).await.unwrap();
    let f = pausable_ism::state::Flags::load(&w.h.data(&p::flags()).await).unwrap();
    assert!(f.guardians.hold(w.h.time, grace()).is_some(), "the hold stands");
    assert!(w.decision(n).await.is_none(), "spent");

    // kind 5 lands through the pausable hold and clears the replaced keys' flags
    let n3 = keypair(50).pubkey();
    let n4 = keypair(51).pubkey();
    let replace = Params::SetGuardians(vec![(1, n3), (2, n4), (6, keypair(52).pubkey())]);
    let n = w.decide(5, &replace).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(n, 5, &replace).await.unwrap();
    let f = pausable_ism::state::Flags::load(&w.h.data(&p::flags()).await).unwrap();
    assert_eq!((f.guardians.keys[0], f.guardians.keys[1], f.guardians.keys[5]), (n3, n4, keypair(52).pubkey()));
    assert!(!f.guardians.any_flag());
    let s = w.state().await;
    assert_eq!((s.guardians.keys[0], s.guardians.keys[1]), (n3, n4), "the two lists never differ");
    // a kind 5 naming a seat's current key is spent
    let noop = Params::SetGuardians(vec![(3, w.seats[2].pubkey())]);
    let n = w.decide(5, &noop).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(n, 5, &noop).await.unwrap();
    assert_eq!(w.state().await.guardians.keys[2], w.seats[2].pubkey());

    // a hold on the admin program: the 7-day group waits; kind 5 lands on the later of its 7th day and the
    // hold's 21st (21 × 24 hours after holdSince; 63 hours at the devnet factor) — the week GovDAO's own cancel
    // of a forged replacement has to land through the client (M2j: no validators' window opens any more)
    w.h.send("admin: set_flag", &[a::set_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.advance(100).await;
    w.h.send("admin: set_flag", &[a::set_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    let hold_since = w.state().await.guardians.hold_since.unwrap();
    assert_eq!(hold_since.time, w.h.time);
    let payer = w.h.payer();
    let fee = Params::SetFee { lamports: 7, beneficiary: payer.pubkey() };
    let nf = w.decide(7, &fee).await;
    let replace2 = Params::SetGuardians(vec![(3, keypair(53).pubkey())]);
    let n5 = w.decide(5, &replace2).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    let e = w.execute(nf, 7, &fee).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::Deferred));
    // 7 days passed, the hold's 21st day has not: under the hold the replacement answers NotMature (its floor
    // is read from the hold history, nothing is written)
    let e = w.execute(n5, 5, &replace2).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    assert_eq!(w.state().await.guardians.keys[2], w.seats[2].pubkey(), "nothing executed");
    // three keys taking turns under the hold never move its start (seat 4 is the third)
    w.h.send("admin: set_flag", &[a::set_flag(&w.seats[0].pubkey())], &[&w.seats[0]]).await.unwrap_err(); // replaced: unknown
    let third = keypair(50); // seat 1's key now
    w.h.fund(&third.pubkey(), 10_000_000).await;
    w.h.send("admin: set_flag", &[a::set_flag(&third.pubkey())], &[&third]).await.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.advance(3600).await;
    w.h.send("admin: set_flag", &[a::set_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    w.h.advance(3600).await;
    w.h.send("admin: set_flag", &[a::set_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&third.pubkey())], &[&third]).await.unwrap();
    assert_eq!(w.state().await.guardians.hold_since, Some(hold_since));
    // the 15th day of v23–v25 is no longer enough; to the hold's 21st day
    let twenty_first = hold_since.time + 21 * clock::DAY / CLOCK_FACTOR;
    w.h.set_clock(w.h.slot + 1000, twenty_first - 6 * clock::DAY / CLOCK_FACTOR).await;
    let e = w.execute(n5, 5, &replace2).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    w.h.set_clock(w.h.slot + 1000, twenty_first - 1).await;
    let e = w.execute(n5, 5, &replace2).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    w.h.set_clock(w.h.slot + 10, twenty_first + 1).await;
    w.execute(n5, 5, &replace2).await.unwrap();
    let s = w.state().await;
    assert_eq!(s.guardians.keys[2], keypair(53).pubkey());
    assert!(s.guardians.flags[2].is_none(), "the replaced key's flag is cleared");
    assert!(s.guardians.hold_since.is_none() && s.guardians.hold(w.h.time, grace()).is_none(), "one flag left: the replacement ends the hold at once");
    // the fee decision is still deferred by the lone flag, and runs once it clears
    let e = w.execute(nf, 7, &fee).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::Deferred));
    w.h.send("admin: clear_flag", &[a::clear_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    w.execute(nf, 7, &fee).await.unwrap();

    // "a hold stands" is holdSince, dip or no dip: two flags, then both cleared within the day — no flag stands,
    // yet the 7-day group is deferred, an unpause of the admin program is spent, and the pausable ISM's owner
    // unpause is refused, until the dip reaches 24 hours
    // (the seats now: 1 = keypair(50) `third`, 2 = keypair(51), 3 = keypair(53), 4 = seats[3]; on both objects)
    let g1 = third;
    let g4 = w.seats[3].insecure_clone();
    let nf2 = w.decide(7, &Params::SetFee { lamports: 8, beneficiary: payer.pubkey() }).await;
    w.h.send("admin: set_flag", &[a::set_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.send("admin: set_flag", &[a::set_flag(&g4.pubkey())], &[&g4]).await.unwrap();
    w.h.send("pausable: set_flag", &[p::set_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.send("pausable: set_flag", &[p::set_flag(&g4.pubkey())], &[&g4]).await.unwrap();
    let nu = w.decide(2, &Params::Unpause(1)).await;
    let nu0 = w.decide(2, &Params::Unpause(0)).await;
    w.h.send("admin: clear_flag", &[a::clear_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&g4.pubkey())], &[&g4]).await.unwrap();
    w.h.send("pausable: clear_flag", &[p::clear_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.send("pausable: clear_flag", &[p::clear_flag(&g4.pubkey())], &[&g4]).await.unwrap();
    let s = w.state().await;
    assert!(!s.guardians.any_flag() && s.guardians.hold(w.h.time, grace()).is_some());
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await; // past the unpauses' 24 hours and the fee's 7 days; the dip is older than the grace now
    // so first, back inside the grace: re-set and clear again to make a fresh dip, then test within it
    w.h.send("admin: set_flag", &[a::set_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.send("admin: set_flag", &[a::set_flag(&g4.pubkey())], &[&g4]).await.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&g4.pubkey())], &[&g4]).await.unwrap();
    w.h.send("pausable: set_flag", &[p::set_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.send("pausable: set_flag", &[p::set_flag(&g4.pubkey())], &[&g4]).await.unwrap();
    w.h.send("pausable: clear_flag", &[p::clear_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.send("pausable: clear_flag", &[p::clear_flag(&g4.pubkey())], &[&g4]).await.unwrap();
    w.h.advance(3600).await;
    let e = w.execute(nf2, 7, &Params::SetFee { lamports: 8, beneficiary: payer.pubkey() }).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::Deferred), "no flag stands, the hold does (a dip inside the grace)");
    w.execute(nu, 2, &Params::Unpause(1)).await.unwrap();
    assert!(w.decision(nu).await.is_none(), "spent: a hold stands on the admin program, dip or no dip");
    w.execute(nu0, 2, &Params::Unpause(0)).await.unwrap();
    assert!(w.decision(nu0).await.is_none(), "spent: a hold stands on the pausable ISM, dip or no dip");
    let f = pausable_ism::state::Flags::load(&w.h.data(&p::flags()).await).unwrap();
    assert!(f.guardians.hold(w.h.time, grace()).is_some());
    // the dip reaches the grace: the holds end, the fee decision runs
    w.h.advance(grace()).await;
    w.execute(nf2, 7, &Params::SetFee { lamports: 8, beneficiary: payer.pubkey() }).await.unwrap();
    assert_eq!(hl::Outbox::parse(&w.h.data(&hl::mailbox_outbox(&MAILBOX)).await).unwrap().fee, 8);
    w.h.report("admin: unpause and kind 5");
}

#[tokio::test]
async fn a_replacements_floor_comes_from_the_hold_history_and_no_later_hold_raises_it() {
    let mut w = World::new().await;
    w.setup().await;
    let day = clock::DAY / CLOCK_FACTOR;
    // seats 3 and 4 hold the admin program: holdSince (day 0)
    w.h.send("admin: set_flag", &[a::set_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.send("admin: set_flag", &[a::set_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    let since = w.state().await.guardians.hold_since.unwrap();
    let twenty_first = since.time + 21 * day;
    assert!(w.state().await.holds.is_empty());
    // day 1: GovDAO's replacement of the holders is delivered; nobody attempts an Execute under this hold (AN4:
    // the operator's machines are the only executor, and the operator may be the holder)
    w.h.advance(day).await;
    let replace = Params::SetGuardians(vec![(3, keypair(53).pubkey()), (4, keypair(54).pubkey())]);
    let n5 = w.decide(5, &replace).await;
    // day 19: both holders clear; at the grace's last second (day 20) they re-flag — the dip reached the grace, so
    // the old hold ended (day 20) and a new one begins with a fresh holdSince, whose own 21st day would be day 41
    w.h.set_clock(w.h.slot + 1000, since.time + 19 * day).await;
    w.h.send("admin: clear_flag", &[a::clear_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    w.h.advance(grace()).await;
    assert!(w.state().await.guardians.hold(w.h.time, grace()).is_none(), "the dip reached the grace: the hold ended");
    w.h.send("admin: set_flag", &[a::set_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.send("admin: set_flag", &[a::set_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    let s = w.state().await;
    let since_b = s.guardians.hold_since.unwrap();
    assert_eq!(since_b.time, since.time + 20 * day, "a new hold at day 20");
    assert_eq!(s.holds, vec![Hold { start: since.time, end: since.time + 20 * day }], "the first hold in the history, ended the moment its dip reached the grace");
    // the first Execute ever, under hold B: the floor is the earliest hold overlapping [day 1, now] — hold A's
    // 21st day — so the replacement lands on day 21, not on hold B's 41st
    w.h.set_clock(w.h.slot + 10, twenty_first - 1).await;
    let e = w.execute(n5, 5, &replace).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    w.h.set_clock(w.h.slot + 10, twenty_first + 1).await;
    assert_eq!(w.state().await.guardians.hold(w.h.time, grace()), Some(since_b), "the new hold stands");
    w.execute(n5, 5, &replace).await.unwrap();
    let end_b = w.h.time;
    let s = w.state().await;
    assert_eq!((s.guardians.keys[2], s.guardians.keys[3]), (keypair(53).pubkey(), keypair(54).pubkey()));
    assert!(s.guardians.hold_since.is_none(), "the holders replaced: the hold ends at once");
    assert_eq!(s.holds.len(), 2);
    assert_eq!(s.holds[1], Hold { start: since_b.time, end: end_b }, "hold B ended by the replacement's clear, now");
    // the floor binds only while a hold stands: seats 1 and 2 hold anew (hold C); a replacement of them
    // delivered on day 1 is NotMature on day 2; the holders lift on day 3 — the dip reaches the grace on day 4
    // and the hold ends — and the replacement lands on its own 7th day, hold C's 21st day moot
    let (g1, g2) = (w.seats[0].insecure_clone(), w.seats[1].insecure_clone());
    w.h.send("admin: set_flag", &[a::set_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.send("admin: set_flag", &[a::set_flag(&g2.pubkey())], &[&g2]).await.unwrap();
    let since_c = w.state().await.guardians.hold_since.unwrap();
    w.h.advance(day).await;
    let replace2 = Params::SetGuardians(vec![(1, keypair(55).pubkey()), (2, keypair(56).pubkey())]);
    let n5b = w.decide(5, &replace2).await;
    w.h.advance(day).await;
    let e = w.execute(n5b, 5, &replace2).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    let d = w.decision(n5b).await.unwrap();
    w.h.advance(day).await;
    w.h.send("admin: clear_flag", &[a::clear_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&g2.pubkey())], &[&g2]).await.unwrap();
    w.h.advance(grace()).await;
    assert!(w.state().await.guardians.hold(w.h.time, grace()).is_none(), "lifted on day 3: the hold ended on day 4");
    let seventh = d.delivered_time + clock::week(CLOCK_FACTOR);
    w.h.set_clock(w.h.slot + 10, seventh - 1).await;
    let e = w.execute(n5b, 5, &replace2).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    w.h.set_clock(w.h.slot + 10, seventh + 1).await;
    w.execute(n5b, 5, &replace2).await.unwrap();
    let s = w.state().await;
    assert_eq!((s.guardians.keys[0], s.guardians.keys[1]), (keypair(55).pubkey(), keypair(56).pubkey()), "its own 7th day: no hold stands, the floor is moot");
    // hold C's end is learnt at the first change of the guardians after its dip reached the grace — the
    // replacement itself — and written as that moment
    assert_eq!(s.holds.len(), 3);
    assert_eq!(s.holds[2], Hold { start: since_c.time, end: since_c.time + 3 * day + grace() });
    // a ninth hold merges the two oldest: the earliest start is never pushed out by churn (the keys now: seats 1
    // and 2 are keypair(55) and keypair(56))
    let (g1, g2) = (keypair(55), keypair(56));
    w.h.fund(&g1.pubkey(), 10_000_000).await;
    w.h.fund(&g2.pubkey(), 10_000_000).await;
    let mut acc = w.h.account(&a::state()).await.unwrap();
    let mut st = State::load(&acc.data).unwrap();
    while st.holds.len() < 8 {
        let last = st.holds.last().unwrap().end;
        st.holds.push(Hold { start: last + day, end: last + 2 * day });
    }
    st.store(&mut acc.data).unwrap();
    w.h.ctx.set_account(&a::state(), &acc.into());
    w.h.send("admin: set_flag", &[a::set_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.send("admin: set_flag", &[a::set_flag(&g2.pubkey())], &[&g2]).await.unwrap();
    let since_d = w.state().await.guardians.hold_since.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    w.h.advance(grace() + 1).await;
    w.h.send("admin: set_flag", &[a::set_flag(&g1.pubkey())], &[&g1]).await.unwrap();
    let s = w.state().await;
    assert_eq!(s.holds.len(), 8);
    assert_eq!(s.holds[0].start, since.time, "the earliest start kept");
    assert_eq!(s.holds[0].end, end_b, "spanning the two oldest");
    assert_eq!(s.holds[7].start, since_d.time);
    assert!(s.guardians.hold_since.is_some() && s.guardians.hold_since != Some(since_d), "a fresh hold stands");
    w.h.report("admin: the replacement's floor");
}

#[tokio::test]
async fn upgrade_extends_then_upgrades_and_the_successor_takes_everything() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();
    let spare = w.spare;
    // a buffer with the client's ELF, bigger than the spare's programdata (the pausable ISM's build): the extension
    // runs before the upgrade
    let new_elf = testlib::elf_of(&ProgramSpec { name: "gno_light_client_ism", id: CLIENT, authority: None, path: None });
    let spare_elf = testlib::elf_of(&ProgramSpec { name: "pausable_ism", id: PAUSABLE, authority: None, path: None });
    assert!(new_elf.len() > spare_elf.len());
    // the spare's programdata pinned to its ELF (--max-len at the ELF's size, §5.8)
    let pd = loader::programdata_address(&spare);
    let mut pd_acc = w.h.account(&pd).await.unwrap();
    pd_acc.data.truncate(45 + spare_elf.len());
    pd_acc.lamports = solana_sdk::rent::Rent::default().minimum_balance(pd_acc.data.len());
    w.h.ctx.set_account(&pd, &pd_acc.into());
    let writer = keypair(60);
    w.h.fund(&writer.pubkey(), 1_000_000_000).await;
    let buffer = Pubkey::new_from_array([0x88; 32]);
    let mut bdata = vec![1, 0, 0, 0, 1];
    bdata.extend_from_slice(writer.pubkey().as_ref());
    bdata.extend_from_slice(&new_elf);
    let blamports = solana_sdk::rent::Rent::default().minimum_balance(bdata.len());
    w.h.ctx.set_account(&buffer, &Account { lamports: blamports, data: bdata, owner: loader::LOADER_ID, executable: false, rent_epoch: 0 }.into());
    let hash = sha256(&[&new_elf]);
    let upgrade = Params::Upgrade { program: spare, buffer, hash };
    let n = w.decide(8, &upgrade).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    // not staged: pending for a retry
    let (mut extra, fw) = a::extra_for(8, &upgrade);
    extra[3] = AccountMeta::new(writer.pubkey(), false);
    let e = w.h.send("admin: execute kind 8", &[a::execute(n, &payer.pubkey(), &payer.pubkey(), extra.clone(), fw)], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotStaged));
    // the writer stages the buffer: its authority is the admin PDA now
    w.h.send("admin: stage_upgrade", &[a::stage_upgrade(n, &buffer, &writer.pubkey())], &[&writer]).await.unwrap();
    assert_eq!(loader::parse_buffer(&w.h.data(&buffer).await).unwrap(), Some(a::authority()));
    assert_eq!(w.decision(n).await.unwrap().refund_key, Some(writer.pubkey()));
    let writer_before = w.h.lamports(&writer.pubkey()).await;
    // the program data is too short: the upgrade waits for the extension, anyone paying (the executor sends the
    // whole excess at once; the suite sends it in 10 KB steps, the loader's minimum, to show it takes one a block)
    let e = w.h.send("admin: execute kind 8", &[a::execute(n, &payer.pubkey(), &payer.pubkey(), extra.clone(), fw)], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NeedsExtend));
    let need = new_elf.len() - spare_elf.len();
    let steps = need.div_ceil(loader::EXTEND_STEP as usize);
    // the growth is the loader's own permissionless ExtendProgram (tag 6), sent top-level by the executor —
    // mainnet's runtime lets no program CPI an extend, and its loader has no ExtendProgramChecked (the audit's
    // F1; this World runs with that feature deactivated, as mainnet is) — one step a block: the loader refuses a
    // second extension of a program in the same slot
    let step_ix = a::extend_program(&spare, &payer.pubkey());
    for _ in 0..steps {
        w.h.send("loader: extend_program (10 KB)", &[step_ix.clone()], &[]).await.unwrap();
    }
    let e = w.h.send("loader: extend_program (10 KB)", &[step_ix.clone(), step_ix.clone()], &[]).await.unwrap_err();
    assert!(matches!(e, TransactionError::InstructionError(1, InstructionError::InvalidArgument)), "a second step in one block: {e:?}");
    w.h.send("admin: execute kind 8 (upgrade)", &[a::execute(n, &payer.pubkey(), &payer.pubkey(), extra, fw)], &[]).await.unwrap();
    let pd_acc = w.h.account(&pd).await.unwrap();
    assert_eq!(pd_acc.data.len(), 45 + spare_elf.len() + steps * loader::EXTEND_STEP as usize, "extended in whole steps");
    assert_eq!(&pd_acc.data[45..45 + new_elf.len()], &new_elf[..]);
    assert!(pd_acc.data[45 + new_elf.len()..].iter().all(|b| *b == 0), "the tail past the ELF is zeroed");
    assert!(w.h.account(&buffer).await.map(|a| a.data.is_empty()).unwrap_or(true), "the buffer was spilled to the refund key");
    assert!(w.h.lamports(&writer.pubkey()).await > writer_before, "the refund key took the buffer's lamports");
    assert!(w.decision(n).await.is_none());
    // a hash mismatch: spent, the buffer returned to the writer
    let buffer2 = Pubkey::new_from_array([0x89; 32]);
    let mut bdata = vec![1, 0, 0, 0, 1];
    bdata.extend_from_slice(writer.pubkey().as_ref());
    bdata.extend_from_slice(&spare_elf);
    w.h.ctx.set_account(&buffer2, &Account { lamports: blamports, data: bdata, owner: loader::LOADER_ID, executable: false, rent_epoch: 0 }.into());
    let wrong = Params::Upgrade { program: PAUSABLE, buffer: buffer2, hash: [1; 32] };
    let n = w.decide(8, &wrong).await;
    w.h.send("admin: stage_upgrade", &[a::stage_upgrade(n, &buffer2, &writer.pubkey())], &[&writer]).await.unwrap();
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    let (mut extra, fw) = a::extra_for(8, &wrong);
    extra[3] = AccountMeta::new(writer.pubkey(), false);
    let before = w.h.lamports(&writer.pubkey()).await;
    w.h.send("admin: execute kind 8 (spent)", &[a::execute(n, &payer.pubkey(), &payer.pubkey(), extra, fw)], &[]).await.unwrap();
    assert!(w.h.account(&buffer2).await.map(|a| a.data.is_empty()).unwrap_or(true));
    assert!(w.h.lamports(&writer.pubkey()).await > before);
    assert!(w.decision(n).await.is_none());
    // a kind 8 naming the admin program itself (immutable): its buffer cannot be staged for it, and it is spent at
    // execution with the buffer left the writer's
    let buffer3 = Pubkey::new_from_array([0x8a; 32]);
    let mut bdata = vec![1, 0, 0, 0, 1];
    bdata.extend_from_slice(writer.pubkey().as_ref());
    bdata.extend_from_slice(&spare_elf);
    w.h.ctx.set_account(&buffer3, &Account { lamports: blamports, data: bdata, owner: loader::LOADER_ID, executable: false, rent_epoch: 0 }.into());
    let own = Params::Upgrade { program: ADMIN, buffer: buffer3, hash: sha256(&[&spare_elf]) };
    let n = w.decide(8, &own).await;
    let e = w.h.send("admin: stage_upgrade", &[a::stage_upgrade(n, &buffer3, &writer.pubkey())], &[&writer]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::WrongDecision));
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    let (mut extra, fw) = a::extra_for(8, &own);
    extra[0] = AccountMeta::new_readonly(loader::programdata_address(&ADMIN), false);
    extra[1] = AccountMeta::new_readonly(ADMIN, false);
    extra[3] = AccountMeta::new(writer.pubkey(), false);
    w.h.send("admin: execute kind 8 (spent)", &[a::execute(n, &payer.pubkey(), &payer.pubkey(), extra, fw)], &[]).await.unwrap();
    assert!(w.decision(n).await.is_none());
    assert_eq!(loader::parse_buffer(&w.h.data(&buffer3).await).unwrap(), Some(writer.pubkey()), "the buffer stays the writer's");

    // the successor must be immutable: the (mutable) client as the counter-example
    let mutable = Params::Transfer { program: CLIENT };
    let n = w.decide(9, &mutable).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    let (mut extra, fw) = a::extra_for(9, &mutable);
    extra[0] = AccountMeta::new_readonly(CLIENT, false);
    extra[1] = AccountMeta::new_readonly(loader::programdata_address(&CLIENT), false);
    w.h.send("admin: execute kind 9 (spent)", &[a::execute(n, &payer.pubkey(), &payer.pubkey(), extra, fw)], &[]).await.unwrap();
    assert!(w.state().await.successor.is_none());
    // a flood before the transfer (C227): the successor must expect the number after the last one taken here,
    // not the one its own init saw; and the successor takes nothing before it inherited
    let flood: Vec<Vec<u8>> = (0..3).map(|i| w.message(7, w.next_seq + i, &Params::SetFee { lamports: 2, beneficiary: payer.pubkey() })).collect();
    w.deliver(&flood).await.into_iter().for_each(|r| r.unwrap());
    w.next_seq += 3;
    let last = w.state().await.last_seq;
    let succ_state = a::state_of(&w.successor);
    let before = State::load(&w.h.data(&succ_state).await).unwrap();
    assert_eq!((before.predecessor, before.inherited, before.last_seq, before.guardians.keys), (ADMIN, 0, 0, [Pubkey::default(); 6]));
    let early = hl::encode_message(900, ORIGIN, &HLGOV_SENDER, LOCAL, &w.successor.to_bytes(), &a::body(7, 1, &Params::SetFee { lamports: 3, beneficiary: payer.pubkey() }));
    let h = a::handle_accounts_of(&w.successor, &payer.pubkey(), 1, vec![]);
    let e = w.deliver_handle_to(&w.successor.clone(), &[early], vec![h]).await.remove(0).unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotInherited), "nothing taken before the hand-over");
    // a hold that ended, so the history carries one entry to hand over
    w.h.send("admin: set_flag", &[a::set_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.send("admin: set_flag", &[a::set_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&w.seats[2].pubkey())], &[&w.seats[2]]).await.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&w.seats[3].pubkey())], &[&w.seats[3]]).await.unwrap();
    w.h.advance(grace() + 1).await;
    w.h.send("admin: set_flag", &[a::set_flag(&w.watch[0].pubkey())], &[&w.watch[0]]).await.unwrap();
    w.h.send("admin: clear_flag", &[a::clear_flag(&w.watch[0].pubkey())], &[&w.watch[0]]).await.unwrap();
    assert_eq!(w.state().await.holds.len(), 1);
    // the real successor
    let transfer = Params::Transfer { program: w.successor };
    let n = w.decide(9, &transfer).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    let (extra, fw) = a::extra_for(9, &transfer);
    w.h.send("admin: execute kind 9", &[a::execute(n, &payer.pubkey(), &payer.pubkey(), extra, fw)], &[]).await.unwrap();
    let succ_auth = Pubkey::find_program_address(&[admin_program::config::AUTHORITY_SEED], &w.successor).0;
    for prog in [MAILBOX, TOKENS[0], TOKENS[1], CLIENT, PAUSABLE] {
        let (_, auth) = loader::parse_programdata(&w.h.data(&loader::programdata_address(&prog)).await).unwrap();
        assert_eq!(auth, Some(succ_auth), "{prog}");
    }
    assert_eq!(hl::Outbox::parse(&w.h.data(&hl::mailbox_outbox(&MAILBOX)).await).unwrap().owner, Some(succ_auth));
    assert_eq!(hl::token_owner(&w.h.data(&hl::token_pda(&TOKENS[0])).await).unwrap(), Some(succ_auth));
    assert_eq!(pausable_ism::state::State::load(&w.h.data(&p::state()).await).unwrap().owner, succ_auth);
    assert_eq!(gno_light_client_ism::state::Body::load(&w.h.data(&c::state()).await).unwrap().admin, succ_auth);
    let s = w.state().await;
    assert_eq!(s.successor, Some(w.successor));
    // the successor inherited the live number, seats and hold history (C227) — and no settled set, no recovery
    // counter (M2j): its state is the account its init made, holding no validator key
    let succ = State::load(&w.h.data(&succ_state).await).unwrap();
    assert_eq!((succ.inherited, succ.last_seq, succ.origin_domain), (1, s.last_seq, s.origin_domain));
    assert_eq!(succ.last_seq, last + 1, "the flood's numbers and the transfer's own");
    assert_eq!(succ.guardians.keys, s.guardians.keys);
    assert!(!succ.guardians.any_flag() && succ.guardians.hold_since.is_none());
    assert_eq!(succ.holds, s.holds);
    assert_eq!(succ.pending, 0);
    let sd = w.h.data(&succ_state).await;
    assert_eq!(sd.len(), State::init_size(), "the hand-over grew nothing");
    assert!(!w.keys.iter().any(|k| sd.windows(32).any(|x| x == k)), "no validator key reached the successor");
    // and expects the next number: one past it is out of order, the next lands
    let seq = succ.last_seq + 1;
    let fee = Params::SetFee { lamports: 4, beneficiary: payer.pubkey() };
    let far = hl::encode_message(901, ORIGIN, &HLGOV_SENDER, LOCAL, &w.successor.to_bytes(), &a::body(7, seq + 1, &fee));
    let h = a::handle_accounts_of(&w.successor, &payer.pubkey(), seq + 1, vec![]);
    let e = w.deliver_handle_to(&w.successor.clone(), &[far], vec![h]).await.remove(0).unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::OutOfOrder));
    let next = hl::encode_message(902, ORIGIN, &HLGOV_SENDER, LOCAL, &w.successor.to_bytes(), &a::body(7, seq, &fee));
    let h = a::handle_accounts_of(&w.successor, &payer.pubkey(), seq, vec![]);
    w.deliver_handle_to(&w.successor.clone(), &[next], vec![h]).await.remove(0).unwrap();
    let succ = State::load(&w.h.data(&succ_state).await).unwrap();
    assert_eq!((succ.last_seq, succ.pending), (seq, 1));
    assert!(w.h.account(&Decision::address(&w.successor, seq).0).await.is_some());
    // the predecessor is retired (S-12): a decision dispatched to it in between — r/hlgov's recipient switch is
    // a separate act (§5.3) — records nothing and is refused with Retired, so the message stays deliverable (the
    // relayer parks it) instead of taking, dead, the number the successor expects next
    let seq0 = w.state().await.last_seq;
    let late = w.message(7, seq0 + 1, &Params::SetFee { lamports: 5, beneficiary: payer.pubkey() });
    let e = w.deliver(&[late]).await.remove(0).unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::Retired));
    assert_eq!(w.state().await.last_seq, seq0, "nothing recorded");
    assert!(w.decision(seq0 + 1).await.is_none());
    // a second hand-over is refused
    let e = w.h.send("admin: execute kind 9 again", &[a::execute(n, &payer.pubkey(), &payer.pubkey(), vec![], false)], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::WrongDecision), "the decision is closed");
    w.h.report("admin: upgrade and successor");
}

#[tokio::test]
async fn cancel_ranges_are_stored_only_when_needed_pruned_and_merged() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();
    let fee = Params::SetFee { lamports: 5, beneficiary: payer.pubkey() };
    // three pending decisions; a range cancel passing all three pairs stores nothing (S-04)
    let n1 = w.decide(7, &fee).await;
    let n2 = w.decide(7, &fee).await;
    let n3 = w.decide(7, &fee).await;
    assert_eq!(w.state().await.pending, 3);
    w.decide_with(1, &cancel(vec![(n1, n3)]), a::cancel_pairs(&[(n1, payer.pubkey()), (n2, payer.pubkey()), (n3, payer.pubkey())])).await;
    let s = w.state().await;
    assert!(s.cancelled.is_empty(), "covered by the pairs closed now: nothing stored");
    assert_eq!(s.pending, 0);
    assert!(w.decision(n1).await.is_none() && w.decision(n3).await.is_none());
    // a range without its pairs is stored; once the last pending decision closes, every stored range is dead
    // and pruned
    let n4 = w.decide(7, &fee).await;
    let n5 = w.decide(7, &fee).await;
    w.decide(1, &cancel(vec![(n4, n5)])).await;
    let s = w.state().await;
    assert_eq!((s.cancelled.len(), s.pending), (1, 2));
    w.h.send("admin: close_cancelled", &[a::close_cancelled(n4, &payer.pubkey())], &[]).await.unwrap();
    let s = w.state().await;
    assert_eq!((s.cancelled.len(), s.pending), (1, 1), "one still pending: the range stays");
    w.execute(n5, 7, &fee).await.unwrap();
    let s = w.state().await;
    assert_eq!((s.cancelled.len(), s.pending), (0, 0), "nothing pending at or below the last number: pruned");
    assert!(w.decision(n5).await.is_none());
    // a full store (64 disjoint ranges, doctored in) merges a new range with the nearest stored one rather than
    // refuse (AN7): the gap's numbers die too, never a jammed cancel
    let n6 = w.decide(7, &fee).await;
    let n7 = w.decide(7, &fee).await;
    let mut acc = w.h.account(&a::state()).await.unwrap();
    let mut st = State::load(&acc.data).unwrap();
    st.cancelled = (0..64u64).map(|i| Range { from: 1000 + 2 * i, to: 1000 + 2 * i }).collect();
    st.store(&mut acc.data).unwrap();
    w.h.ctx.set_account(&a::state(), &acc.into());
    w.decide(1, &cancel(vec![(n6, n7)])).await;
    let s = w.state().await;
    assert_eq!(s.cancelled.len(), 64);
    assert!(s.is_cancelled(n6) && s.is_cancelled(n7));
    assert!(s.cancelled.iter().any(|r| r.from == n6 && r.to == 1000), "merged with the nearest: [n6, 1000]");
    assert!(w.decision(n6).await.is_some(), "not closed now (no pair passed): dead in the state");
    // the dead decisions close as cancelled: nothing pends and every range goes with them
    for n in [n6, n7] {
        w.h.send("admin: close_cancelled", &[a::close_cancelled(n, &payer.pubkey())], &[]).await.unwrap();
    }
    let s = w.state().await;
    assert_eq!((s.pending, s.cancelled.len()), (0, 0));
    w.h.report("admin: the cancelled store");
}

#[tokio::test]
async fn kind_12_sets_a_tokens_mint_limits() {
    let mut w = World::new().await;
    w.setup().await;
    let defaults = (pausable_ism::config::DEFAULT_MAX_PER_MESSAGE, pausable_ism::config::DEFAULT_DAY_CAP);
    let ps = pausable_ism::state::State::load(&w.h.data(&p::state()).await).unwrap();
    assert_eq!((ps.limits[1].max_per_message, ps.limits[1].day_cap), defaults);
    // GovDAO's kind 12 for BUBBLE (token 1): the 7-day group, then the pausable ISM's owner-only setter by CPI
    let limits = Params::SetMintLimits { token: 1, max: 7_000_000_000, day_cap: 9_000_000_000 };
    assert_eq!(a::body(12, 1, &limits)[9..].len(), 17, "token u8, max u64, day cap u64");
    let n = w.decide(12, &limits).await;
    let e = w.execute(n, 12, &limits).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(n, 12, &limits).await.unwrap();
    let ps = pausable_ism::state::State::load(&w.h.data(&p::state()).await).unwrap();
    assert_eq!((ps.limits[1].max_per_message, ps.limits[1].day_cap), (7_000_000_000, 9_000_000_000));
    assert_eq!((ps.limits[0].max_per_message, ps.limits[0].day_cap), defaults, "GNOT's untouched");
    // a token that is none of ours: the parameters do not parse, spent
    let seq = w.next_seq;
    let mut body = vec![12u8];
    body.extend_from_slice(&seq.to_be_bytes());
    body.push(2);
    body.extend_from_slice(&[0u8; 16]);
    let m = hl::encode_message(seq as u32, ORIGIN, &HLGOV_SENDER, LOCAL, &ADMIN.to_bytes(), &body);
    w.deliver(&[m]).await.remove(0).unwrap();
    w.next_seq += 1;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.h.send("admin: execute kind 12 (spent)", &[a::execute(seq, &w.h.payer().pubkey(), &w.h.payer().pubkey(), vec![], false)], &[]).await.unwrap();
    assert!(w.decision(seq).await.is_none());
    w.h.report("admin: kind 12");
}

#[tokio::test]
async fn kinds_10_and_11_are_unknown_now_and_the_sequence_never_panics() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();
    // kind 10 (set sequence) and kind 11 (reset client) arrived by the validators' recovery channel alone; since
    // M2j there is no such channel, and a decision of either kind through the mailbox is what any unknown kind
    // is: recorded (the sequence moves) and spent at recording (no account, nothing executed) — a kind 10 of 500
    // leaves the sequence at its own number, a kind 11 of a new chain leaves the client as it was, in the old
    // formats of both (§13 before M2j: u64; chain id len u8 then bytes, origin u32, hash 32, height u64, count u8
    // then pubkey 32 ‖ power i64)
    let s0 = w.state().await;
    let b0 = gno_light_client_ism::state::Body::load(&w.h.data(&c::state()).await).unwrap();
    let seq10 = w.next_seq;
    let mut body10 = vec![10u8];
    body10.extend_from_slice(&seq10.to_be_bytes());
    body10.extend_from_slice(&500u64.to_be_bytes());
    let seq11 = seq10 + 1;
    let new_set = testkit::set_of(&testkit::validators(9, &[1, 1, 1]));
    let mut body11 = vec![11u8];
    body11.extend_from_slice(&seq11.to_be_bytes());
    body11.push(8);
    body11.extend_from_slice(b"gno-next");
    body11.extend_from_slice(&1198419796u32.to_be_bytes());
    body11.extend_from_slice(&[5u8; 32]);
    body11.extend_from_slice(&1u64.to_be_bytes());
    body11.push(new_set.len() as u8);
    for v in &new_set.validators {
        body11.extend_from_slice(&v.pubkey);
        body11.extend_from_slice(&(v.power as u64).to_be_bytes());
    }
    let m10 = hl::encode_message(seq10 as u32, ORIGIN, &HLGOV_SENDER, LOCAL, &ADMIN.to_bytes(), &body10);
    let m11 = hl::encode_message(seq11 as u32, ORIGIN, &HLGOV_SENDER, LOCAL, &ADMIN.to_bytes(), &body11);
    w.deliver(&[m10, m11]).await.into_iter().for_each(|r| r.unwrap());
    w.next_seq += 2;
    let s = w.state().await;
    assert_eq!(s.last_seq, seq11, "both recorded: the sequence moved by two, to its own numbers, never to 500");
    assert!(w.decision(seq10).await.is_none() && w.decision(seq11).await.is_none(), "spent at recording: no account");
    assert_eq!(s.pending, 0);
    // the client is untouched by the kind 11 (its chain, origin and set), and the admin program's origin too
    let b = gno_light_client_ism::state::Body::load(&w.h.data(&c::state()).await).unwrap();
    assert_eq!((b.chain_id.as_str(), b.origin_domain, &b.set), (CHAIN, ORIGIN, &b0.set));
    assert_eq!(s.origin_domain, s0.origin_domain);
    // and the next decision takes the number after them, and pends as usual
    let fee = Params::SetFee { lamports: 1, beneficiary: payer.pubkey() };
    let n = w.decide(7, &fee).await;
    assert_eq!(n, seq11 + 1);
    assert!(w.decision(n).await.is_some());
    // the sequence at its very end (doctored in the bank; nothing can take it there now that kind 10 is gone): a
    // delivery is accepted and ignored, never a panic that would leave the message deliverable for good (AN8)
    let mut acc = w.h.account(&a::state()).await.unwrap();
    let mut s = State::load(&acc.data).unwrap();
    s.last_seq = u64::MAX;
    s.store(&mut acc.data).unwrap();
    w.h.ctx.set_account(&a::state(), &acc.into());
    let m = w.message(7, u64::MAX, &fee);
    w.deliver(&[m]).await.remove(0).unwrap();
    assert_eq!(w.state().await.last_seq, u64::MAX, "ignored");
    w.h.report("admin: kinds 10 and 11 unknown");
}

/// A loader buffer at `key` holding `elf` under `writer`'s authority, written into the bank.
fn buffer_account(w: &mut World, key: Pubkey, writer: &Pubkey, elf: &[u8]) {
    let mut bdata = vec![1, 0, 0, 0, 1];
    bdata.extend_from_slice(writer.as_ref());
    bdata.extend_from_slice(elf);
    let lamports = solana_sdk::rent::Rent::default().minimum_balance(bdata.len());
    w.h.ctx.set_account(&key, &Account { lamports, data: bdata, owner: loader::LOADER_ID, executable: false, rent_epoch: 0 }.into());
}

#[tokio::test]
async fn a_retired_predecessor_closes_what_pends_there() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();
    // GovDAO's batch: a kind 8 (the pausable ISM upgraded to its own build again, its buffer staged by the writer
    // under this program's PDA), a second kind 8 never staged, a fee — all pending when the kind 9 lands first
    let pausable_elf = testlib::elf_of(&ProgramSpec { name: "pausable_ism", id: PAUSABLE, authority: None, path: None });
    let writer = keypair(60);
    w.h.fund(&writer.pubkey(), 2_000_000_000).await;
    let buffer = Pubkey::new_from_array([0x8b; 32]);
    buffer_account(&mut w, buffer, &writer.pubkey(), &pausable_elf);
    let upgrade = Params::Upgrade { program: PAUSABLE, buffer, hash: sha256(&[&pausable_elf]) };
    let n8 = w.decide(8, &upgrade).await;
    w.h.send("admin: stage_upgrade", &[a::stage_upgrade(n8, &buffer, &writer.pubkey())], &[&writer]).await.unwrap();
    assert_eq!(loader::parse_buffer(&w.h.data(&buffer).await).unwrap(), Some(a::authority()));
    let buffer2 = Pubkey::new_from_array([0x8c; 32]);
    buffer_account(&mut w, buffer2, &writer.pubkey(), &pausable_elf);
    let unstaged = Params::Upgrade { program: PAUSABLE, buffer: buffer2, hash: sha256(&[&pausable_elf]) };
    let n8b = w.decide(8, &unstaged).await;
    let fee = Params::SetFee { lamports: 13, beneficiary: payer.pubkey() };
    let nf = w.decide(7, &fee).await;
    assert_eq!(w.state().await.pending, 3);
    // on a live program CloseCancelled takes a cancelled decision alone
    let e = w.h.send("admin: close_cancelled", &[a::close_cancelled(nf, &payer.pubkey())], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::WrongDecision));
    // the kind 9 lands first
    let transfer = Params::Transfer { program: w.successor };
    let n9 = w.decide(9, &transfer).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    let (extra, fw) = a::extra_for(9, &transfer);
    w.h.send("admin: execute kind 9", &[a::execute(n9, &payer.pubkey(), &payer.pubkey(), extra, fw)], &[]).await.unwrap();
    assert_eq!(w.state().await.successor, Some(w.successor));
    // every Execute of the staged kind 8 now fails at the loader — the successor's PDA is the upgrade authority —
    // with its hash matching, so nothing returns the buffer: before S-13 the buffer (≈0.84 SOL of rent for a real
    // ELF) and every pending decision's rent were stranded for good
    let (mut extra, fw) = a::extra_for(8, &upgrade);
    extra[3] = AccountMeta::new(writer.pubkey(), false);
    let e = w.h.send("admin: execute kind 8 (retired)", &[a::execute(n8, &payer.pubkey(), &payer.pubkey(), extra, fw)], &[]).await.unwrap_err();
    assert!(matches!(e, TransactionError::InstructionError(0, InstructionError::IncorrectAuthority)), "{e:?}");
    assert!(w.decision(n8).await.is_some(), "pending still");
    assert_eq!(loader::parse_buffer(&w.h.data(&buffer).await).unwrap(), Some(a::authority()), "the buffer still the PDA's");
    // no buffer can be staged on the retired program any more
    let e = w.h.send("admin: stage_upgrade (retired)", &[a::stage_upgrade(n8b, &buffer2, &writer.pubkey())], &[&writer]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::Retired));
    assert_eq!(loader::parse_buffer(&w.h.data(&buffer2).await).unwrap(), Some(writer.pubkey()), "the buffer stays the writer's");
    // CloseCancelled closes any pending decision of a retired program: the staged kind 8, its buffer back to the
    // writer and its rent to the deliverer; the unstaged one; the fee
    let writer_before = w.h.lamports(&writer.pubkey()).await;
    let payer_before = w.h.lamports(&payer.pubkey()).await;
    let mut ix = a::close_cancelled(n8, &payer.pubkey());
    ix.accounts.extend([AccountMeta::new(buffer, false), AccountMeta::new(writer.pubkey(), false), AccountMeta::new_readonly(loader::LOADER_ID, false)]);
    w.h.send("admin: close_cancelled (retired, a staged kind 8)", &[ix], &[]).await.unwrap();
    assert!(w.decision(n8).await.is_none());
    assert!(w.h.account(&buffer).await.map(|a| a.data.is_empty()).unwrap_or(true), "the buffer closed");
    assert!(w.h.lamports(&writer.pubkey()).await > writer_before, "its lamports to the writer");
    assert!(w.h.lamports(&payer.pubkey()).await > payer_before, "the decision's rent to the deliverer");
    for n in [n8b, nf] {
        w.h.send("admin: close_cancelled (retired)", &[a::close_cancelled(n, &payer.pubkey())], &[]).await.unwrap();
        assert!(w.decision(n).await.is_none(), "{n} closed");
    }
    assert_eq!(w.state().await.pending, 0);
    w.h.report("admin: a retired predecessor");
}

#[tokio::test]
async fn the_hand_over_sweeps_the_predecessors_float_to_the_successors_pda() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();
    let succ = w.successor;
    let succ_auth = a::authority_of(&succ);
    // S-25: a successor's authority PDA started with no lamports and the predecessor's float — hlsol setup's
    // 0.01 SOL, twenty enrolments' worth (M2-NOTES 17b) — stayed on the retired program's PDA, which no
    // instruction ever moved; so GovDAO's next kind 6 on the successor (a route's migration, §5.3) died in
    // Hyperlane's EnrollRemoteRouters for want of the realloc top-up it draws from the owner and pended until a
    // hand sent lamports, against §6.5's "no decision ever waits on the operator's hand". Now kind 9's hand-over
    // sends every lamport of the predecessor's PDA to the successor's by a system transfer the PDA signs (the
    // successor's PDA writable in the account list); hlsol successor-init funds the PDA too, as setup funds the
    // launch one, and hlwatch's successor check prints the balance (the Go lane's)
    let float = w.h.lamports(&a::authority()).await;
    assert!(float > 0, "the launch PDA's float");
    assert_eq!(w.h.lamports(&succ_auth).await, 0, "the successor's PDA starts with nothing");
    let transfer = Params::Transfer { program: succ };
    let n9 = w.decide(9, &transfer).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(n9, 9, &transfer).await.unwrap();
    assert_eq!(w.state().await.successor, Some(succ));
    // the next kind 6 reaches the successor and executes there, the successor's PDA paying the token account's
    // growth as the launch PDA did
    let router = Params::EnrollRouter { token: 0, domain: ORIGIN, router: [0xcd; 32] };
    let seq = State::load(&w.h.data(&a::state_of(&succ)).await).unwrap().last_seq + 1;
    let m = hl::encode_message(910, ORIGIN, &HLGOV_SENDER, LOCAL, &succ.to_bytes(), &a::body(6, seq, &router));
    let h = a::handle_accounts_of(&succ, &payer.pubkey(), seq, vec![]);
    w.deliver_handle_to(&succ, &[m], vec![h]).await.remove(0).unwrap();
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    let (extra, fw) = a::extra_for(6, &router);
    let len_before = w.h.data(&hl::token_pda(&TOKENS[0])).await.len();
    let pda_before = w.h.lamports(&succ_auth).await;
    w.h.send("successor: execute kind 6", &[a::execute_of(&succ, seq, &payer.pubkey(), &payer.pubkey(), extra, fw)], &[]).await.unwrap();
    let len_after = w.h.data(&hl::token_pda(&TOKENS[0])).await.len();
    let rent = solana_sdk::rent::Rent::default();
    assert!(len_after > len_before, "the router map outgrew the account");
    assert_eq!(pda_before - w.h.lamports(&succ_auth).await, rent.minimum_balance(len_after) - rent.minimum_balance(len_before), "the successor's PDA paid the growth's rent");
    // the float went with the authorities: the successor's PDA held it whole, the predecessor's nothing
    assert_eq!(pda_before, float, "the successor's PDA holds the predecessor's float");
    assert_eq!(w.h.lamports(&a::authority()).await, 0, "nothing stranded on the retired program's PDA");
    w.h.report("admin: the hand-over's float");
}

/// A kind-7 body of `n` parameter bytes (garbage: it never parses, so it is spent at execution).
fn fee_body_of(n: usize) -> Vec<u8> {
    let mut b = vec![7u8];
    b.extend_from_slice(&0u64.to_be_bytes());
    b.extend(std::iter::repeat(0x5au8).take(n));
    b
}

#[tokio::test]
async fn a_decision_body_is_bounded_by_what_one_account_holds() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();
    // the bound (S-14): a decision account is 95 bytes beside its parameters (105 before M2j took the channel,
    // the trigger and the marker out), and one instruction creates at most 10,240 bytes (the runtime's
    // MAX_PERMITTED_DATA_INCREASE), so MAX_PARAMS is 10,145 — the old 11,000 admitted a body no account could
    // hold, which handle let through to die in create_pda_account, its number untaken and every later number
    // OutOfOrder
    let empty = Decision { version: 1, number: 0, kind: 7, params: vec![], delivered_slot: 0, delivered_time: 0, deliverer: Pubkey::default(), refund_key: None };
    assert_eq!((empty.size(), DECISION_OVERHEAD), (95, 95));
    assert_eq!(admin_program::config::MAX_PARAMS, gno_light_client_ism::state::MAX_GROWTH - 95);
    assert_eq!(admin_program::config::MAX_PARAMS, 10_145);
    // the mailbox never reaches the program's bound: Hyperlane's mailbox (the mainnet build the deployment uses)
    // copies the message through its own 32 KB heap — the instruction, the ISM's Verify data, the recipient's
    // Handle data — and dies on it before any ISM or recipient runs; measured, a body of 2,250 parameter bytes is
    // delivered and one of 2,500 is not (the real chain's 1,232-byte packet bounds a body lower still, at 538
    // bytes). So a body past the bound, or a 2,500-byte one, leaves the number untaken — the stuck sequence S-14
    // names, only in the mailbox's hands, which r/hlgov's builder must never author — and handle's own check is
    // defence in depth above it
    let last = w.state().await.last_seq;
    for n in [admin_program::config::MAX_PARAMS + 1, 2_500] {
        let seq = w.next_seq;
        let m = hl::encode_message(seq as u32, ORIGIN, &HLGOV_SENDER, LOCAL, &ADMIN.to_bytes(), &{ let mut b = fee_body_of(n); b[1..9].copy_from_slice(&seq.to_be_bytes()); b });
        let e = w.deliver(&[m]).await.remove(0).unwrap_err();
        assert!(custom_code(&e).is_none() && matches!(e, TransactionError::InstructionError(0, InstructionError::ProgramFailedToComplete)), "{n}: the mailbox's own death, before handle: {e:?}");
        assert_eq!(w.state().await.last_seq, last, "{n}: the number untaken");
    }
    // a 2,250-byte body is carried, recorded whole under the bound and, its parameters parsing as no kind 7,
    // spent at Execute
    let seq = w.next_seq;
    let m = hl::encode_message(seq as u32, ORIGIN, &HLGOV_SENDER, LOCAL, &ADMIN.to_bytes(), &{ let mut b = fee_body_of(2_250); b[1..9].copy_from_slice(&seq.to_be_bytes()); b });
    w.deliver(&[m]).await.remove(0).unwrap();
    w.next_seq += 1;
    assert_eq!(w.decision(seq).await.expect("recorded").params.len(), 2_250);
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.h.send("admin: execute kind 7 (2,250 bytes, spent)", &[a::execute(seq, &payer.pubkey(), &payer.pubkey(), vec![], false)], &[]).await.unwrap();
    assert!(w.decision(seq).await.is_none(), "spent");
    w.h.report("admin: the decision body's bound");
}

#[tokio::test]
async fn a_transfer_to_the_admin_program_itself_is_spent() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();
    // a kind 9 naming this program as its own successor (a tool's default, a copied id): its programdata has no
    // authority — burned in the setup step — so the immutability check alone passed it, and the execution
    // re-pointed every authority to this same PDA and then failed on its own SuccessorInit, pending for good
    // until GovDAO's cancel a week on (S-15); now spent, no account beyond the fixed six read
    let own = Params::Transfer { program: ADMIN };
    let n = w.decide(9, &own).await;
    let e = w.execute(n, 9, &own).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotMature));
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.h.send("admin: execute kind 9 (self, spent)", &[a::execute(n, &payer.pubkey(), &payer.pubkey(), vec![], false)], &[]).await.unwrap();
    assert!(w.decision(n).await.is_none(), "spent");
    authorities_untouched(&mut w).await;
    // and the program goes on taking decisions
    let fee = Params::SetFee { lamports: 6, beneficiary: payer.pubkey() };
    let nf = w.decide(7, &fee).await;
    assert!(w.decision(nf).await.is_some());
    w.h.report("admin: a self-transfer");
}

/// Every authority and owner a kind 9 would move is still this program's PDA, and no successor is set.
async fn authorities_untouched(w: &mut World) {
    assert!(w.state().await.successor.is_none(), "not retired");
    for prog in [MAILBOX, TOKENS[0], TOKENS[1], CLIENT, PAUSABLE] {
        let (_, auth) = loader::parse_programdata(&w.h.data(&loader::programdata_address(&prog)).await).unwrap();
        assert_eq!(auth, Some(a::authority()), "{prog}: untouched");
    }
    assert_eq!(hl::Outbox::parse(&w.h.data(&hl::mailbox_outbox(&MAILBOX)).await).unwrap().owner, Some(a::authority()));
    assert_eq!(hl::token_owner(&w.h.data(&hl::token_pda(&TOKENS[0])).await).unwrap(), Some(a::authority()));
    assert_eq!(pausable_ism::state::State::load(&w.h.data(&p::state()).await).unwrap().owner, a::authority());
    assert_eq!(gno_light_client_ism::state::Body::load(&w.h.data(&c::state()).await).unwrap().admin, a::authority());
}

#[tokio::test]
async fn a_transfer_to_a_successor_that_can_never_inherit_is_spent() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();
    // S-15's spend reached the self-case alone: a kind 9 naming an immutable program whose state can never take
    // the hand-over — burned before its init, initialised naming another predecessor, inherited already, or
    // retired itself — passed the immutability check, re-pointed nine authorities and died on the successor's
    // own SuccessorInit (NotInherited, WrongAuthority), the execution reverting whole and the decision pending
    // until a second proposal cancelled it (S-18); now the successor's state is read first, and such a decision
    // is spent with nothing moved
    // never initialised: the orphan has had no upgrade authority since its deployment, so its init can never be
    // signed
    let e = w.h.send("orphan: init", &[a::init_successor(&w.orphan, &payer.pubkey(), &w.authority.pubkey(), ORIGIN, &ADMIN)], &[&w.authority]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::WrongAuthority), "nothing can sign the orphan's init");
    assert!(w.h.account(&a::state_of(&w.orphan)).await.is_none());
    // another predecessor: the stray's init names a copied id, then it is burned
    w.h.send("stray: init", &[a::init_successor(&w.stray, &payer.pubkey(), &w.authority.pubkey(), ORIGIN, &w.successor)], &[&w.authority]).await.unwrap();
    let burn = loader::set_authority(&loader::programdata_address(&w.stray), &w.authority.pubkey(), None);
    w.h.send("loader: burn (stray)", &[burn], &[&w.authority]).await.unwrap();
    assert_eq!(State::load(&w.h.data(&a::state_of(&w.stray)).await).unwrap().predecessor, w.successor);
    // inherited already, retired itself, and (S-23) compiled at another clock factor — the devnet drill's
    // FACTOR8=1 build deployed, initialised and burned on mainnet by mistake, its hash matching what the proposer
    // built: nothing before the vote read the byte, and after the hand-over every brake of the successor would
    // have run eight times faster (the week in 21 hours, the grace in 3), the guard finish_setup gives the launch
    // programs (S-06) not being on the successor's path; now can_inherit requires the successor's factor to be
    // this build's, the transfer spent otherwise like the cases before it — the real successor's state, doctored
    // in the bank each time
    let succ_state = a::state_of(&w.successor);
    let orig = w.h.account(&succ_state).await.unwrap();
    let cases: Vec<(&str, Pubkey, Option<fn(&mut State)>)> = vec![
        ("never initialised", w.orphan, None),
        ("naming another predecessor", w.stray, None),
        ("inherited already", w.successor, Some(|s: &mut State| s.inherited = 1)),
        ("retired itself", w.successor, Some(|s: &mut State| s.successor = Some(Pubkey::new_from_array([0x7a; 32])))),
        // the devnet factor on a mainnet build, and the mirror at the devnet build
        ("at another clock factor", w.successor, Some(|s: &mut State| s.clock_factor = if CLOCK_FACTOR == 8 { 1 } else { 8 })),
    ];
    for (what, program, doctor) in cases {
        if let Some(f) = doctor {
            let mut acc = orig.clone();
            let mut s = State::load(&acc.data).unwrap();
            f(&mut s);
            s.store(&mut acc.data).unwrap();
            w.h.ctx.set_account(&succ_state, &acc.into());
        }
        let transfer = Params::Transfer { program };
        let n = w.decide(9, &transfer).await;
        w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
        let as_doctored = w.h.account(&succ_state).await.unwrap();
        let cu = w.execute(n, 9, &transfer).await.unwrap_or_else(|e| panic!("{what}: {e:?}"));
        assert!(w.decision(n).await.is_none(), "{what}: spent");
        authorities_untouched(&mut w).await;
        assert_eq!(w.h.account(&succ_state).await.unwrap(), as_doctored, "{what}: nothing handed over");
        println!("a kind 9 to a successor {what}: spent in {cu} CU");
    }
    w.restore(&succ_state, orig);
    // the successor as its init left it takes the hand-over: the rule's other side
    let transfer = Params::Transfer { program: w.successor };
    let n = w.decide(9, &transfer).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(n, 9, &transfer).await.unwrap();
    assert_eq!(w.state().await.successor, Some(w.successor));
    assert_eq!(State::load(&w.h.data(&succ_state).await).unwrap().inherited, 1);
    w.h.report("admin: a transfer to a successor that can never inherit");
}

#[tokio::test]
async fn a_successor_runs_nothing_of_its_own_before_it_inherited() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();
    let succ = w.successor;
    let succ_state = a::state_of(&succ);
    // the successor as its init left it: its predecessor named, not inherited, no number, zero seats
    let s0 = State::load(&w.h.data(&succ_state).await).unwrap();
    assert_eq!((s0.predecessor, s0.inherited, s0.last_seq, s0.guardians.keys), (ADMIN, 0, 0, [Pubkey::default(); 6]));
    // S-20: a successor runs nothing of its own before it inherited — Handle refuses every decision (tested with
    // the hand-over), and Execute refuses before any decision is looked for (NotInherited, where a missing
    // decision would be WrongDecision), so nothing could ever run with its authority once it inherited. M2j
    // took the other three entries S-20 closed (Settle, RecoveryInit, RecoveryRecord) out of the program
    let e = w.h.send("successor: execute (not inherited)", &[a::execute_of(&succ, 1, &payer.pubkey(), &payer.pubkey(), vec![], false)], &[]).await.unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::NotInherited));
    assert!(w.h.account(&a::decision_of(&succ, 1)).await.is_none());
    // the hand-over: the successor inherits the predecessor's number and seats, and nothing else
    let transfer = Params::Transfer { program: succ };
    let n9 = w.decide(9, &transfer).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(n9, 9, &transfer).await.unwrap();
    let s = State::load(&w.h.data(&succ_state).await).unwrap();
    let pred = w.state().await;
    assert_eq!((s.inherited, s.last_seq, s.guardians.keys, s.pending), (1, pred.last_seq, pred.guardians.keys, 0));
    // now a decision to the successor runs there with its authority: a fee set through the mailbox, whose owner
    // is the successor's PDA
    let seq = s.last_seq + 1;
    let fee = Params::SetFee { lamports: 5, beneficiary: payer.pubkey() };
    let m = hl::encode_message(920, ORIGIN, &HLGOV_SENDER, LOCAL, &succ.to_bytes(), &a::body(7, seq, &fee));
    let h = a::handle_accounts_of(&succ, &payer.pubkey(), seq, vec![]);
    w.deliver_handle_to(&succ, &[m], vec![h]).await.remove(0).unwrap();
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    let (extra, fw) = a::extra_for(7, &fee);
    w.h.send("successor: execute kind 7", &[a::execute_of(&succ, seq, &payer.pubkey(), &payer.pubkey(), extra, fw)], &[]).await.unwrap();
    let ob = hl::Outbox::parse(&w.h.data(&hl::mailbox_outbox(&MAILBOX)).await).unwrap();
    assert_eq!((ob.fee, ob.owner), (5, Some(a::authority_of(&succ))));
    assert_eq!(State::load(&w.h.data(&succ_state).await).unwrap().pending, 0);
    w.h.report("admin: a successor before and after it inherited");
}

/// The hand-over's wire contract (S-21), spelled out byte by byte in M2j's form. Kind 9's `Execute` parses the
/// *successor's* state with this crate's `State` (`can_inherit`: `predecessor`, `inherited`, `successor` and
/// `clock_factor`) and encodes the successor's `SuccessorInit` with this crate's `Instruction`, and the Go side
/// (`ParseAdminState`, hlwatch's pre-vote check) reads the state with the same layout — so a successor of
/// another build keeps `State`'s v1 borsh layout append-only (every field at its place, `predecessor` and
/// `inherited` the last 33 bytes, a new field only after them) and `SuccessorInit` byte-identical (variant 8, its
/// arguments in this order, `Hold` and `Guardians` as they are). A reorder, an insertion before `inherited` or a
/// widened field fails here; so does a moved instruction tag.
#[test]
fn the_hand_overs_wire_contract_is_frozen() {
    let key = |b: u8| Pubkey::new_from_array([b; 32]);
    let flag = |slot: u64, time: i64| Flag { slot, time };
    let holds = vec![Hold { start: 11, end: -12 }, Hold { start: 13, end: 14 }];
    let s = State {
        version: 1,
        clock_factor: 8,
        setup_done: 1,
        origin_domain: 0x0102_0304,
        last_seq: 0x1112_1314_1516_1718,
        guardians: Guardians { keys: [key(0x31), key(0x32), key(0x33), key(0x34), key(0x35), key(0x36)], flags: [Some(flag(1, 2)), None, Some(flag(3, -4)), None, None, Some(flag(5, 6))], hold_since: Some(flag(7, 8)), dip_since: None },
        successor: Some(key(0x41)),
        authority_bump: 0xfe,
        cancelled: vec![Range { from: 9, to: 10 }],
        holds: holds.clone(),
        pending: 15,
        predecessor: key(0x61),
        inherited: 1,
    };
    // borsh: little-endian integers, a u32 count before a vector's items, an Option as 0 or 1 ‖ the value
    let some_flag = |w: &mut Vec<u8>, slot: u64, time: i64| {
        w.push(1);
        w.extend_from_slice(&slot.to_le_bytes());
        w.extend_from_slice(&time.to_le_bytes());
    };
    let mut want: Vec<u8> = vec![1, 8, 1];
    want.extend_from_slice(&0x0102_0304u32.to_le_bytes());
    want.extend_from_slice(&0x1112_1314_1516_1718u64.to_le_bytes());
    for b in 0x31..=0x36u8 {
        want.extend_from_slice(&[b; 32]);
    }
    some_flag(&mut want, 1, 2);
    want.push(0);
    some_flag(&mut want, 3, -4);
    want.push(0);
    want.push(0);
    some_flag(&mut want, 5, 6);
    some_flag(&mut want, 7, 8); // hold_since
    want.push(0); // dip_since
    want.push(1);
    want.extend_from_slice(&[0x41; 32]); // successor
    want.push(0xfe); // authority_bump
    assert_eq!(want.len(), 313, "the head before `cancelled`: three flags, a hold and a successor present");
    want.extend_from_slice(&1u32.to_le_bytes()); // cancelled: from u64 ‖ to u64 each
    want.extend_from_slice(&9u64.to_le_bytes());
    want.extend_from_slice(&10u64.to_le_bytes());
    let holds_bytes = {
        // u32 count ‖ (start i64 ‖ end i64) each
        let mut v = 2u32.to_le_bytes().to_vec();
        for (start, end) in [(11i64, -12i64), (13, 14)] {
            v.extend_from_slice(&start.to_le_bytes());
            v.extend_from_slice(&end.to_le_bytes());
        }
        v
    };
    want.extend_from_slice(&holds_bytes);
    want.extend_from_slice(&15u32.to_le_bytes()); // pending
    want.extend_from_slice(&[0x61; 32]); // predecessor
    want.push(1); // inherited
    let bytes = borsh::to_vec(&s).unwrap();
    assert_eq!(bytes, want, "State's v1 layout, field by field");
    assert_eq!(bytes.len(), 313 + 20 + 36 + 4 + 33);
    // the two fields the peek and hlwatch read sit at the end, whatever the vectors hold: predecessor at len − 33,
    // inherited at len − 1
    assert_eq!(&bytes[bytes.len() - 33..bytes.len() - 1], &[0x61; 32]);
    assert_eq!(bytes[bytes.len() - 1], 1);
    assert_eq!(State::load(&bytes).unwrap(), s);
    // append-only: this build's parse reads nothing after `inherited`, so a successor's appended field (or the
    // account's zero reserve) is tolerated, where an inserted one would move the two fields
    assert_eq!(State::load(&[bytes.as_slice(), &[0xab; 100]].concat()).unwrap(), s);
    // the account at init, and for the program's life: the head with every option present (377 bytes), 64
    // cancelled ranges and eight holds reserved — 1,574 bytes, as the Go side expects; nothing ever grows it
    assert_eq!(State::init_size(), 377 + (4 + 64 * 16) + (4 + 8 * 16) + 4 + 32 + 1);
    assert_eq!(State::init_size(), 1_574);
    // SuccessorInit: variant 8, then origin_domain u32 ‖ last_seq u64 ‖ seats 4 × 32 ‖ watch 2 × 32 ‖ holds, as above
    let ix = AdminIx::SuccessorInit { origin_domain: 0x0102_0304, last_seq: 0x1112_1314_1516_1718, seats: [key(0x31), key(0x32), key(0x33), key(0x34)], watch: [key(0x35), key(0x36)], holds: holds.clone() };
    let mut want = vec![8u8];
    want.extend_from_slice(&0x0102_0304u32.to_le_bytes());
    want.extend_from_slice(&0x1112_1314_1516_1718u64.to_le_bytes());
    for b in 0x31..=0x36u8 {
        want.extend_from_slice(&[b; 32]);
    }
    want.extend_from_slice(&holds_bytes);
    assert_eq!(borsh::to_vec(&ix).unwrap(), want, "SuccessorInit's encoding");
    assert_eq!(want.len(), 1 + 4 + 8 + 192 + 36);
    // and the init, which the Go side sends for a launch deployment (InitIx) and for a successor before the burn
    // (SuccessorInitIx): variant 0, seats 4 × 32 ‖ watch 2 × 32 ‖ origin_domain u32 ‖ predecessor 32 ‖ last_seq
    // u64 (AT3: the starting number, the last field; 0 for a successor)
    let ix = AdminIx::Init { seats: [key(0x31), key(0x32), key(0x33), key(0x34)], watch: [key(0x35), key(0x36)], origin_domain: 0x0102_0304, predecessor: key(0x61), last_seq: 0x1112_1314_1516_1718 };
    let mut want = vec![0u8];
    for b in 0x31..=0x36u8 {
        want.extend_from_slice(&[b; 32]);
    }
    want.extend_from_slice(&0x0102_0304u32.to_le_bytes());
    want.extend_from_slice(&[0x61; 32]);
    want.extend_from_slice(&0x1112_1314_1516_1718u64.to_le_bytes());
    assert_eq!(borsh::to_vec(&ix).unwrap(), want, "Init's encoding");
    assert_eq!(want.len(), 1 + 192 + 4 + 32 + 8);
    // the other tags, as the Go builders send them (M2j: one channel, so a number alone; CloseCancelled is 7)
    assert_eq!(borsh::to_vec(&AdminIx::TransferAuthorityIn).unwrap(), vec![1]);
    assert_eq!(borsh::to_vec(&AdminIx::FinishSetup).unwrap(), vec![2]);
    assert_eq!(borsh::to_vec(&AdminIx::Execute { number: 5 }).unwrap(), [&[3u8][..], &5u64.to_le_bytes()].concat());
    assert_eq!(borsh::to_vec(&AdminIx::StageUpgrade { number: 2 }).unwrap(), [&[4u8][..], &2u64.to_le_bytes()].concat());
    assert_eq!(borsh::to_vec(&AdminIx::SetFlag).unwrap(), vec![5]);
    assert_eq!(borsh::to_vec(&AdminIx::ClearFlag).unwrap(), vec![6]);
    assert_eq!(borsh::to_vec(&AdminIx::CloseCancelled { number: 9 }).unwrap(), [&[7u8][..], &9u64.to_le_bytes()].concat());
    // a decision's PDA: (`decision`, the number big-endian) — no channel byte
    assert_eq!(Decision::address(&ADMIN, 5).0, Pubkey::find_program_address(&[b"decision", &5u64.to_be_bytes()], &ADMIN).0);
}

#[tokio::test]
async fn no_decision_reads_the_clients_head() {
    let mut w = World::new().await;
    w.setup().await;
    let payer = w.h.payer();
    // S-22's scenario: §13's kind-4 route run in the wrong order — GovDAO's kind 8 that upgrades the client to a
    // new state layout lands before the kind 9 to the successor pair built to read it, and the upgrade's
    // migration writes the new version byte into the head. The pausable ISM then refuses every delivery, by
    // design; but Execute once read the client's head, pinned at version 1, for every kind, so the decision that
    // would cure the mistake — the kind 8 that re-upgrades the client from a fix build — died at the version
    // check with it, every decision pending for good. Since M2j no decision reads the client at all (the one
    // reader of the head is finish_setup, a setup-only step; kind 9 hands the admin role on through the client's
    // own SetAdmin, which reads no version)
    // the corrective kind 8 — the client upgraded from a buffer of its own build, which is what a fix build is to
    // the loader — and a fee decision beside it, delivered and staged while the head still says version 1
    let client_elf = testlib::elf_of(&ProgramSpec { name: "gno_light_client_ism", id: CLIENT, authority: None, path: None });
    let writer = keypair(61);
    w.h.fund(&writer.pubkey(), 3_000_000_000).await;
    let buffer = Pubkey::new_from_array([0x8d; 32]);
    buffer_account(&mut w, buffer, &writer.pubkey(), &client_elf);
    let upgrade = Params::Upgrade { program: CLIENT, buffer, hash: sha256(&[&client_elf]) };
    let n8 = w.decide(8, &upgrade).await;
    w.h.send("admin: stage_upgrade", &[a::stage_upgrade(n8, &buffer, &writer.pubkey())], &[&writer]).await.unwrap();
    let fee = Params::SetFee { lamports: 11, beneficiary: payer.pubkey() };
    let n7 = w.decide(7, &fee).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    // the client's head says layout version 2 (what a layout change's migration writes), doctored in the bank:
    // the client itself runs on, the pausable ISM refuses deliveries, and the admin program is asked to execute
    let orig = w.doctor(&c::state(), 0, &[2]).await;
    assert_eq!(Head::read(&w.h.data(&c::state()).await).unwrap().version, 2);
    w.execute(n7, 7, &fee).await.unwrap();
    assert_eq!(hl::Outbox::parse(&w.h.data(&hl::mailbox_outbox(&MAILBOX)).await).unwrap().fee, 11, "a kind 7 executed under the unknown head");
    let (mut extra, fw) = a::extra_for(8, &upgrade);
    extra[3] = AccountMeta::new(writer.pubkey(), false);
    w.h.send("admin: execute kind 8 (the client, its head at version 2)", &[a::execute(n8, &payer.pubkey(), &payer.pubkey(), extra, fw)], &[]).await.unwrap();
    assert!(w.decision(n8).await.is_none(), "executed");
    let pd = w.h.account(&loader::programdata_address(&CLIENT)).await.unwrap();
    assert_eq!(&pd.data[45..45 + client_elf.len()], &client_elf[..], "the client upgraded from the fix build's buffer");
    assert!(w.h.account(&buffer).await.map(|a| a.data.is_empty()).unwrap_or(true), "the buffer spilled to the writer");
    // and a kind 9 to the successor pair executes under it too: the hand-over's SetAdmin reads no version
    let transfer = Params::Transfer { program: w.successor };
    let n9 = w.decide_with_head_doctored(9, &transfer).await;
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(n9, 9, &transfer).await.unwrap();
    assert_eq!(w.state().await.successor, Some(w.successor));
    assert_eq!(gno_light_client_ism::state::Body::load(&w.h.data(&c::state()).await).unwrap().admin, a::authority_of(&w.successor));
    assert_eq!(Head::read(&w.h.data(&c::state()).await).unwrap().version, 2, "the head as doctored: nothing of ours wrote it");
    w.restore(&c::state(), orig);
    w.h.report("admin: no decision reads the client's head");
}

impl World {
    /// Delivers a decision while the client's head is doctored to another version: the pausable ISM would refuse
    /// the delivery, so the head is put back for the delivery alone and doctored again after it.
    async fn decide_with_head_doctored(&mut self, kind: u8, params: &Params) -> u64 {
        let doctored = self.h.data(&c::state()).await[0];
        self.doctor(&c::state(), 0, &[1]).await;
        let n = self.decide(kind, params).await;
        self.doctor(&c::state(), 0, &[doctored]).await;
        n
    }
}

/// AT3 (the final check): §6.4's exit is a fresh deployment under the same r/hlgov, whose counter never goes
/// down — so `init` takes the number the sequence starts at, and a fresh program initialised at r/hlgov's
/// `Last()` takes r/hlgov's next decision as its first. Here r/hlgov stands at 37: the program's init at 37, the
/// setup step passing with it, a copy of #37 (recorded on the old deployment) accepted and ignored, #39 refused
/// `OutOfOrder`, #38 recorded pending and executed on its 7th day — where a program started at 0 would have
/// refused #38 for ever (and `sync sequence` cannot lower r/hlgov's counter).
#[tokio::test]
async fn a_fresh_deployment_starts_its_sequence_at_r_hlgovs_number() {
    let mut w = World::new_from(37).await;
    w.setup().await;
    let payer = w.h.payer();
    let s = w.state().await;
    assert_eq!((s.last_seq, s.pending, s.setup_done), (37, 0, 1), "started at 37, nothing pending, the setup step passed");
    let fee = Params::SetFee { lamports: 700, beneficiary: payer.pubkey() };
    // a copy of the old deployment's #37, re-dispatched by anyone: at or below the last, accepted and ignored
    let m37 = w.message(7, 37, &fee);
    w.deliver(&[m37]).await.remove(0).unwrap();
    assert_eq!(w.state().await.last_seq, 37, "a number at or below the start is ignored");
    assert!(w.decision(37).await.is_none(), "nothing recorded for it");
    // #39 before #38: out of order, the message stays deliverable
    let m39 = w.message(7, 39, &fee);
    let e = w.deliver(&[m39]).await.remove(0).unwrap_err();
    assert_eq!(custom_code(&e), code(AdminError::OutOfOrder));
    // #38, r/hlgov's next decision: recorded pending, and executed on its 7th day
    let n = w.decide(7, &fee).await;
    assert_eq!(n, 38);
    let s = w.state().await;
    assert_eq!((s.last_seq, s.pending), (38, 1));
    assert_eq!(w.decision(38).await.unwrap().kind, 7);
    assert_eq!(custom_code(&w.execute(38, 7, &fee).await.unwrap_err()), code(AdminError::NotMature));
    w.h.advance(clock::week(CLOCK_FACTOR) + 1).await;
    w.execute(38, 7, &fee).await.unwrap();
    assert!(w.decision(38).await.is_none(), "executed");
    assert_eq!(hl::Outbox::parse(&w.h.data(&hl::mailbox_outbox(&MAILBOX)).await).unwrap().fee, 700, "the fee set by r/hlgov's #38");
    // then #39 lands
    let n = w.decide(7, &Params::SetFee { lamports: 701, beneficiary: payer.pubkey() }).await;
    assert_eq!((n, w.state().await.last_seq), (39, 39));
    w.h.report("admin: a fresh deployment started at r/hlgov's number");
}

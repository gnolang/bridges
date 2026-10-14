//! The programs' test harness.
//!
//! `Harness::new` starts program-test with each named program installed as an *upgradeable* program under a test
//! authority (so `init` can demand the upgrade authority's signature, and the admin program can move authorities),
//! or as a plain program (Hyperlane's mainnet builds). `send` runs one transaction and records its compute units
//! under a label; `report` prints them. The clock is driven by writing the Clock sysvar.

#![allow(deprecated)]

pub mod admin;
pub mod client;
pub mod pausable;

use solana_program_test::{find_file, read_file, BanksClientError, ProgramTest, ProgramTestContext};
use solana_sdk::{
    account::Account,
    clock::Clock,
    instruction::Instruction,
    pubkey::Pubkey,
    rent::Rent,
    signature::{Keypair, Signer},
    transaction::{Transaction, TransactionError},
};
use std::collections::BTreeMap;

pub use solana_program_test::tokio;

pub const LOADER_ID: Pubkey = sol_util::loader::LOADER_ID;
pub const BPF_LOADER_V2: Pubkey = solana_sdk::pubkey!("BPFLoader2111111111111111111111111111111111");
/// The feature behind the loader's `ExtendProgramChecked`, abandoned with SIMD-0164 and absent on mainnet,
/// testnet and devnet, which program-test still carries: the admin suite deactivates it, so the suite runs
/// against the loader mainnet has (the audit's F1).
pub const ENABLE_EXTEND_PROGRAM_CHECKED: Pubkey = solana_sdk::pubkey!("2oMRZEDWT2tqtYMofhmmfQ8SsjqUFzT6sYXppQDavxwz");

/// A program to install: its `.so` name (found through SBF_OUT_DIR or the fixtures directories), its id, and an
/// upgrade authority (None: a plain, non-upgradeable program).
pub struct ProgramSpec {
    pub name: &'static str,
    pub id: Pubkey,
    pub authority: Option<Pubkey>,
    /// An explicit file instead of the name lookup (Hyperlane's dumped builds).
    pub path: Option<String>,
}

pub fn elf_of(spec: &ProgramSpec) -> Vec<u8> {
    match &spec.path {
        Some(p) => std::fs::read(p).unwrap_or_else(|e| panic!("{p}: {e}")),
        None => {
            let file = find_file(&format!("{}.so", spec.name)).unwrap_or_else(|| panic!("{}.so not found: build with cargo build-sbf and run under cargo test-sbf (or set SBF_OUT_DIR)", spec.name));
            read_file(file)
        }
    }
}

/// The two accounts of an upgradeable program with the given authority, `max_len` bytes of program data.
pub fn upgradeable_accounts(id: &Pubkey, elf: &[u8], authority: Option<Pubkey>, max_len: usize) -> Vec<(Pubkey, Account)> {
    let rent = Rent::default();
    let programdata = sol_util::loader::programdata_address(id);
    let mut program_data = vec![2, 0, 0, 0];
    program_data.extend_from_slice(programdata.as_ref());
    let mut pd = vec![3, 0, 0, 0];
    pd.extend_from_slice(&0u64.to_le_bytes());
    match authority {
        Some(a) => {
            pd.push(1);
            pd.extend_from_slice(a.as_ref());
        }
        None => pd.push(0),
    }
    // the loader's programdata metadata is 45 bytes whatever the option holds: the ELF starts there (an
    // immutable program's ELF written at byte 13 was "Unsupported program id")
    pd.resize(sol_util::loader::PROGRAMDATA_HEADER, 0);
    pd.extend_from_slice(elf);
    pd.resize(45 + max_len.max(elf.len()), 0);
    vec![
        (*id, Account { lamports: rent.minimum_balance(36), data: program_data, owner: LOADER_ID, executable: true, rent_epoch: u64::MAX }),
        (programdata, Account { lamports: rent.minimum_balance(pd.len()), data: pd, owner: LOADER_ID, executable: false, rent_epoch: u64::MAX }),
    ]
}

pub fn plain_program_account(id: &Pubkey, elf: &[u8]) -> (Pubkey, Account) {
    (*id, Account { lamports: Rent::default().minimum_balance(elf.len()), data: elf.to_vec(), owner: BPF_LOADER_V2, executable: true, rent_epoch: u64::MAX })
}

pub struct Harness {
    pub ctx: ProgramTestContext,
    pub cu: Vec<(String, u64)>,
    pub slot: u64,
    pub time: i64,
}

impl Harness {
    pub async fn new(programs: &[ProgramSpec], extra_accounts: Vec<(Pubkey, Account)>) -> Harness {
        Self::new_with_features(programs, extra_accounts, &[]).await
    }

    /// As `new`, with the named runtime features deactivated (program-test activates every feature it knows,
    /// mainnet has not).
    pub async fn new_with_features(programs: &[ProgramSpec], extra_accounts: Vec<(Pubkey, Account)>, deactivated: &[Pubkey]) -> Harness {
        let mut pt = ProgramTest::default();
        pt.prefer_bpf(true);
        pt.set_compute_max_units(1_400_000);
        for f in deactivated {
            pt.deactivate_feature(*f);
        }
        for spec in programs {
            let elf = elf_of(spec);
            if spec.path.is_some() && spec.authority.is_none() {
                let (k, a) = plain_program_account(&spec.id, &elf);
                pt.add_account(k, a);
            } else {
                for (k, a) in upgradeable_accounts(&spec.id, &elf, spec.authority, elf.len() + 65536) {
                    pt.add_account(k, a);
                }
            }
        }
        for (k, a) in extra_accounts {
            pt.add_account(k, a);
        }
        let ctx = pt.start_with_context().await;
        let clock: Clock = ctx.banks_client.get_sysvar().await.unwrap();
        let mut h = Harness { ctx, cu: Vec::new(), slot: clock.slot, time: clock.unix_timestamp };
        // a known, round clock
        h.set_clock(1_000, 1_800_000_000).await;
        h
    }

    pub fn payer(&self) -> Keypair {
        self.ctx.payer.insecure_clone()
    }

    /// Runs one transaction, the payer paying, and records its compute units under `label`. Every transaction
    /// lands in a new slot (a fresh blockhash, so identical instructions are never a duplicate), at the harness's
    /// own time.
    pub async fn send(&mut self, label: &str, ixs: &[Instruction], signers: &[&Keypair]) -> Result<u64, TransactionError> {
        let (slot, time) = (self.slot + 1, self.time);
        self.set_clock(slot, time).await;
        let blockhash = self.ctx.banks_client.get_latest_blockhash().await.unwrap();
        let payer = self.payer();
        let message = solana_sdk::message::Message::new_with_blockhash(ixs, Some(&payer.pubkey()), &blockhash);
        let required: Vec<Pubkey> = message.signer_keys().iter().map(|k| **k).collect();
        // only the keys the message needs sign it (a submitter is passed for every transaction of a staging)
        let mut all: Vec<&Keypair> = vec![&payer];
        for s in signers {
            if required.contains(&s.pubkey()) && !all.iter().any(|k| k.pubkey() == s.pubkey()) {
                all.push(s);
            }
        }
        let tx = Transaction::new(all.as_slice(), message, blockhash);
        let out = self.ctx.banks_client.process_transaction_with_metadata(tx).await.map_err(|e| match e {
            BanksClientError::TransactionError(t) => t,
            other => panic!("banks: {other}"),
        })?;
        let cu = out.metadata.as_ref().map(|m| m.compute_units_consumed).unwrap_or(0);
        match out.result {
            Ok(()) => {
                self.cu.push((label.to_string(), cu));
                Ok(cu)
            }
            Err(e) => {
                if let Some(m) = &out.metadata {
                    for l in &m.log_messages {
                        if l.contains("failed") || l.contains("Error") || l.contains("error") {
                            eprintln!("    {l}");
                        }
                    }
                }
                Err(e)
            }
        }
    }

    pub async fn account(&mut self, key: &Pubkey) -> Option<Account> {
        self.ctx.banks_client.get_account(*key).await.unwrap()
    }

    pub async fn data(&mut self, key: &Pubkey) -> Vec<u8> {
        self.account(key).await.map(|a| a.data).unwrap_or_default()
    }

    pub async fn lamports(&mut self, key: &Pubkey) -> u64 {
        self.account(key).await.map(|a| a.lamports).unwrap_or(0)
    }

    /// Moves to `slot` (a new bank, a new blockhash) and sets the Clock sysvar to it at `time`.
    pub async fn set_clock(&mut self, slot: u64, time: i64) {
        let slot = slot.max(self.slot + 1);
        self.slot = slot;
        self.time = time;
        self.ctx.warp_to_slot(slot).expect("warp");
        let clock = Clock { slot, epoch_start_timestamp: time, epoch: 0, leader_schedule_epoch: 1, unix_timestamp: time };
        self.ctx.set_sysvar(&clock);
    }

    /// Moves the clock forward by `secs` (and the slot by secs·2.5).
    pub async fn advance(&mut self, secs: i64) {
        let slot = self.slot + (secs as u64) * 5 / 2 + 1;
        let time = self.time + secs;
        self.set_clock(slot, time).await;
    }

    pub async fn fund(&mut self, key: &Pubkey, lamports: u64) {
        let payer = self.payer();
        let ix = solana_system_interface::instruction::transfer(&payer.pubkey(), key, lamports);
        self.send("fund", &[ix], &[]).await.unwrap();
    }

    /// Prints the compute units of every labelled transaction: the maximum per label and the count.
    pub fn report(&self, title: &str) {
        let mut by: BTreeMap<&str, (u64, u64, usize)> = BTreeMap::new();
        for (l, cu) in &self.cu {
            let e = by.entry(l.as_str()).or_insert((0, u64::MAX, 0));
            e.0 = e.0.max(*cu);
            e.1 = e.1.min(*cu);
            e.2 += 1;
        }
        println!("\n== compute units: {title} ==");
        for (l, (max, min, n)) in by {
            if l == "fund" {
                continue;
            }
            println!("  {l:<40} max {max:>8}  min {min:>8}  ({n} tx)");
        }
    }
}

/// The custom error code of a failed transaction, if that is what failed.
pub fn custom_code(e: &TransactionError) -> Option<u32> {
    match e {
        TransactionError::InstructionError(_, solana_sdk::instruction::InstructionError::Custom(c)) => Some(*c),
        _ => None,
    }
}

pub fn keypair(seed: u8) -> Keypair {
    let mut s = [0u8; 32];
    s[0] = seed;
    s[31] = 0x77;
    Keypair::new_from_array(s)
}

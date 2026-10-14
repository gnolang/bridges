//! The compiled constants. The program id and the mailbox realm's path are the M2 development values; `hlsol
//! setup` (M3) writes the launch values here before the reproducible build, and the launch notice prints them.

use solana_program::pubkey::Pubkey;

/// The program's own id (a development value: the deploy keypair's pubkey replaces it).
pub const ID: Pubkey = Pubkey::new_from_array([0x11; 32]);

pub fn id() -> Pubkey {
    ID
}

/// The gno.land mailbox realm whose `Dispatch` events the proofs carry (§13: address-namespaced under
/// r/sys/names at launch).
pub const MAILBOX_PKG_PATH: &str = "gno.land/r/g1deployer/hlmailbox";

/// The compiled clock factor (§5.9): 1 on mainnet, 8 on the devnet build.
pub const CLOCK_FACTOR: i64 = if cfg!(feature = "clock-factor-8") { 8 } else { 1 };

pub const STATE_VERSION: u8 = 1;
pub const STAGE_VERSION: u8 = 1;

pub const UPDATE_SEED: &[u8] = b"update";
pub const PROOF_SEED: &[u8] = b"proof";

/// The results leaf a proof may carry (§6.2).
pub const MAX_LEAF: usize = 8192;
/// Accepted header hashes remembered for proofs (§6.2).
pub const RING: usize = 256;
/// Set changes remembered with their quarantine ends: the newest entry settled by the clock and up to four after
/// it; an older entry is evicted for a new one, and with none to evict the announcement waits (`HistoryFull`,
/// S-01). Nothing on chain reads it since M2j; the watch and the tools do.
pub const HISTORY_MAX: usize = 5;
/// The space the state account keeps free past its body for growth (a bigger set is a realloc, paid by the
/// update that announces it).
pub const STATE_SLACK: usize = 2048;
/// The most `Grow` makes of the body region: what a body of 255-validator sets throughout (the current set, an
/// announced one, a full history of five, a 64-byte chain id) takes, 72,073 bytes, with the slack — 80 KB.
pub const MAX_BODY_CAP: usize = 81_920;
/// The most validators one `StageNextSet` call appends (its transaction's room: 40 bytes a validator).
pub const NEXT_SET_CHUNK: usize = 25;

/// The ISM type `Type` answers: 0, Hyperlane's `Unused`, so a stock relayer skips this route rather than guess
/// a metadata format; the bridge's own relayer builds the proofs.
pub const ISM_TYPE: u32 = 0;

use solana_program::pubkey::Pubkey;

/// The program's own id (a development value: the deploy keypair's pubkey replaces it).
pub const ID: Pubkey = Pubkey::new_from_array([0x22; 32]);

pub fn id() -> Pubkey {
    ID
}

pub const CLOCK_FACTOR: i64 = if cfg!(feature = "clock-factor-8") { 8 } else { 1 };

pub const STATE_VERSION: u8 = 1;
pub const STATE_SEED: &[u8] = b"state";
pub const FLAGS_SEED: &[u8] = b"flags";

/// Our deployment of Hyperlane's mailbox, whose `InboxProcess` must be the top-level instruction of every
/// `Verify` (AN5): the same literal as the admin program's, which asserts the two equal at build time; `hlsol
/// setup` writes both.
pub const MAILBOX: Pubkey = Pubkey::new_from_array([0x66; 32]);
/// The two token programs the mint limits are kept for (0 GNOT, 1 BUBBLE), the admin program's literal again.
pub const TOKENS: [Pubkey; 2] = [Pubkey::new_from_array([0x44; 32]), Pubkey::new_from_array([0x55; 32])];
/// The mint limits at launch (AN5; §5.4's per-message cap, in base units of 6 decimals): 25,000 a message and
/// 100,000 a rolling day, for each token; kind 12 moves them.
pub const DEFAULT_MAX_PER_MESSAGE: u64 = 25_000_000_000;
pub const DEFAULT_DAY_CAP: u64 = 100_000_000_000;

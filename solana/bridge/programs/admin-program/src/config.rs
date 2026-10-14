//! The compiled constants (§6.5: "constants, printed by the audit"). Development values; `hlsol setup` (M3)
//! writes the launch values before the reproducible build.

use solana_program::pubkey::Pubkey;

pub const ID: Pubkey = Pubkey::new_from_array([0x33; 32]);

pub fn id() -> Pubkey {
    ID
}

/// Our deployment of Hyperlane's mailbox.
pub const MAILBOX: Pubkey = Pubkey::new_from_array([0x66; 32]);
/// The two token programs (kind 3 and 6's `token u8`: 0 GNOT, 1 BUBBLE).
pub const TOKENS: [Pubkey; 2] = [Pubkey::new_from_array([0x44; 32]), Pubkey::new_from_array([0x55; 32])];
/// The light-client ISM, this program's own ISM: `finish_setup` reads its state (the admin role, the clock
/// factor, the origin domain) and kind 9 hands its admin role on; no decision's clock reads it (M2j).
pub const CLIENT: Pubkey = gno_light_client_ism::ID;
/// The pausable ISM, kind 3's only allowed non-zero ISM and the mailbox's default ISM.
pub const PAUSABLE: Pubkey = pausable_ism::ID;
/// The layout version of the client's state this program reads at `finish_setup`: 1 for the deployment's life
/// (v27 §13; kind 4 is reserved, and a new layout is a successor by kind 9).
pub const CLIENT_STATE_VERSION: u8 = sol_util::client_head::VERSION_1;
/// r/hlgov's address as the message carries it (the gno address, twelve zero bytes in front).
pub const HLGOV_SENDER: [u8; 32] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a];
/// The mailbox's `max_protocol_fee` at init (0.05 SOL), which `finish_setup` reads back.
pub const MAX_PROTOCOL_FEE: u64 = 50_000_000;

/// The pausable ISM carries the same mailbox and token ids (its `Verify` meters the tokens' mints inside the
/// mailbox's deliveries, AN5); `hlsol setup` writes both files, and a build whose literals differ fails here.
#[allow(dead_code)]
const fn same_key(a: Pubkey, b: Pubkey) -> bool {
    let (a, b) = (a.to_bytes(), b.to_bytes());
    let mut i = 0;
    while i < 32 {
        if a[i] != b[i] {
            return false;
        }
        i += 1;
    }
    true
}
const _: () = assert!(same_key(MAILBOX, pausable_ism::config::MAILBOX), "the pausable ISM's MAILBOX is not the admin program's");
const _: () = assert!(same_key(TOKENS[0], pausable_ism::config::TOKENS[0]) && same_key(TOKENS[1], pausable_ism::config::TOKENS[1]), "the pausable ISM's TOKENS are not the admin program's");

pub const CLOCK_FACTOR: i64 = if cfg!(feature = "clock-factor-8") { 8 } else { 1 };

pub const STATE_VERSION: u8 = 1;
pub const STATE_SEED: &[u8] = b"state";
pub const AUTHORITY_SEED: &[u8] = b"authority";
/// A pending decision: (`decision`, its number u64 BE) — one channel since M2j.
pub const DECISION_SEED: &[u8] = b"decision";

/// The most cancel entries one decision may carry.
pub const MAX_CANCEL: usize = 16;
/// The bytes of a decision account beside its parameters (`Decision::size`, the refund key counted as `Some`).
pub const DECISION_OVERHEAD: usize = 95;
/// The most a decision's parameters may be: what one decision account holds beside its overhead, the account
/// being at most the 10,240 bytes one instruction may create or grow it by (the runtime's
/// `MAX_PERMITTED_DATA_INCREASE`). A body past it is spent at `handle` rather than dying in `create_pda_account`
/// with its number untaken and every later number `OutOfOrder` (S-14) — defence in depth, since the mailbox
/// itself carries no body near it (M2-NOTES, S-14).
pub const MAX_PARAMS: usize = 10_240 - DECISION_OVERHEAD;

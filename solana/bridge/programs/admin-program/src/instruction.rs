//! The admin program's own instructions; the mailbox's calls (`InterchainSecurityModule`, `Handle` and their
//! account-metas questions) are told apart by their discriminators first. Nine variants since M2j: the recovery
//! channel's five (`RecoveryInit`, `RecoveryVerify`, `RecoveryRecord`, `RecoveryClose`, `RecoveryChunk`) and
//! `Settle` are gone, and `CloseCancelled` and `SuccessorInit` moved down to 7 and 8.

use borsh::{BorshDeserialize, BorshSerialize};
use solana_program::pubkey::Pubkey;

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub enum Instruction {
    /// 0. One-shot; the program's current upgrade authority signs. The launch deployment's guardian lists are its
    /// init data (§6.5) and it starts at the sequence number given — 0 for a chain's first deployment, r/hlgov's
    /// `Last()` for a fresh deployment under the same r/hlgov (AT3, §6.4's exit: r/hlgov's counter never goes
    /// down, so a new program starting at 0 would answer `OutOfOrder` to every decision for ever; `hlsol setup
    /// -start-sequence` sets it and refuses a number that is not r/hlgov's). A successor names its predecessor
    /// and gives zero seats, watch slots and number: the live number, seats, watch slots and hold history reach
    /// it by `SuccessorInit` at the transfer (C227); it starts with no flags, no hold and no pending decisions,
    /// and takes no decision before it inherited.
    ///
    /// Accounts: 0 state (w) · 1 payer (signer, w) · 2 the upgrade authority (signer) · 3 this program · 4 its
    /// programdata · 5 the system program.
    Init { seats: [Pubkey; 4], watch: [Pubkey; 2], origin_domain: u32, predecessor: Pubkey, last_seq: u64 },
    /// 1. The setup step: moves a program's upgrade authority to the authority PDA (`SetAuthorityChecked`, the
    /// PDA co-signing). Only before `finish_setup`.
    ///
    /// Accounts: 0 state · 1 the authority PDA · 2 the programdata (w) · 3 the current authority (signer) · 4 the
    /// loader.
    TransferAuthorityIn,
    /// 2. Permissionless, self-checking: flips `setup_done` when every upgrade authority and owner reads as the
    /// admin program or none, the settings are the launch ones, and the three programs agree (S-06: the pausable
    /// ISM's guardian list is this program's, the client's and the pausable ISM's clock factors are this
    /// program's, the client's origin domain is this program's). It records nothing (M2j: no settled set).
    ///
    /// Accounts: 0 state (w) · 1 the authority PDA · 2–6 the programdata of the mailbox, token 0, token 1, the
    /// client, the pausable ISM · 7 this program's programdata · 8 the mailbox outbox · 9 the mailbox inbox · 10
    /// token 0's PDA · 11 token 1's PDA · 12 the pausable ISM's state · 13 the client's state · 14 the pausable
    /// ISM's flags.
    FinishSetup,
    /// 3. Anyone executes a matured decision (a cancelled one is closed instead). A replacement of guardians
    /// (kind 5) under a hold answers `NotMature` until the later of its own 7th day and the 21st day of the
    /// earliest hold overlapping [its delivery, now], read from the state's hold history (AN4: nothing is
    /// written, no earlier call is needed, and no later hold raises it); with no hold standing it lands on its
    /// own 7th day. `NotInherited` on a successor before it inherited (S-20: nothing pends there, nothing runs
    /// with its authority). Nothing of the client is read here (M2j): kind 9 passes the client's state, among
    /// its own accounts, to the client's `SetAdmin`, which checks it itself.
    ///
    /// Accounts: 0 state (w) · 1 the decision (w) · 2 its deliverer (w) · 3 the executor (signer, w) · 4 the
    /// authority PDA (w: kind 9 sweeps its lamports into the successor's, S-25) · 5 the pausable ISM's flags ·
    /// then the kind's own (kinds.rs).
    Execute { number: u64 },
    /// 4. The buffer writer hands a kind-8 decision's buffer to the authority PDA and becomes its refund key.
    /// `Retired` once kind 9 named a successor (S-13: no upgrade can run from here again).
    ///
    /// Accounts: 0 state · 1 the decision (w) · 2 the buffer (w) · 3 the writer (signer) · 4 the authority PDA ·
    /// 5 the loader.
    StageUpgrade { number: u64 },
    /// 5. A guardian or watch key sets its flag on this program.
    ///
    /// Accounts: 0 state (w) · 1 the key (signer).
    SetFlag,
    /// 6. A key clears its own flag.
    ClearFlag,
    /// 7. Closes a decision a cancel named without closing: its rent to its deliverer. Once kind 9 named a
    /// successor, any pending decision (S-13: nothing pending on a retired program can execute, and no cancel
    /// reaches it), a staged buffer back to its refund key.
    ///
    /// Accounts: 0 state (w) · 1 the decision (w) · 2 its deliverer (w) · 3 the authority PDA; for a staged kind 8
    /// also the buffer (w), the refund key (w) and the loader.
    CloseCancelled { number: u64 },
    /// 8. Kind 9's hand-over into a successor (C227): the predecessor's authority PDA — the one `Init` named —
    /// writes the live origin domain, sequence number (the last recorded, which is the high-water mark: every
    /// stored cancelled range is clamped at it), seats, watch slots and hold history into the successor's
    /// state; once. The predecessor's `Execute` of kind 9 sends it by CPI. The state never grows (the hold
    /// history is at most eight, reserved at init), so no payer is needed.
    ///
    /// Its encoding is the hand-over's wire contract (S-21): the predecessor builds it with *its* crate's
    /// `Instruction`, so a successor of another build keeps variant 8 and this argument list byte-identical —
    /// `0x08 ‖ origin_domain u32 ‖ last_seq u64 ‖ seats 4 × 32 ‖ watch 2 × 32 ‖ holds (u32 count ‖ (start i64 ‖
    /// end i64) each)`, little-endian — with the variants before it in their places (a new instruction is
    /// appended; variant 0's `Init` is the Go side's `SuccessorInitIx` too). An encoding the successor cannot read
    /// fails its CPI after the nine authorities moved: the transaction reverts whole and the decision pends until
    /// a cancel. The suite pins the bytes (`the_hand_overs_wire_contract_is_frozen`); `State`'s layout is the
    /// contract's other half (state.rs).
    ///
    /// Accounts: 0 state (w) · 1 the predecessor's authority PDA (signer).
    SuccessorInit { origin_domain: u32, last_seq: u64, seats: [Pubkey; 4], watch: [Pubkey; 2], holds: Vec<crate::state::Hold> },
}

use borsh::{BorshDeserialize, BorshSerialize};
use solana_program::pubkey::Pubkey;

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub enum Instruction {
    /// 0. One-shot; the program's current upgrade authority signs. `target_version` must be 1, the client's state
    /// layout for the deployment's life (v27 §13).
    ///
    /// Accounts: 0 state (w) · 1 flags (w) · 2 payer (signer, w) · 3 the upgrade authority (signer) · 4 this
    /// program · 5 its programdata · 6 the system program.
    Init { owner: Pubkey, target: Pubkey, target_version: u8, seats: [Pubkey; 4], watch: [Pubkey; 2] },
    /// 1. A guardian or watch key sets its own flag (fresh slot on every set).
    ///
    /// Accounts: 0 flags (w) · 1 the key (signer).
    SetFlag,
    /// 2. A key clears its own flag.
    ///
    /// Accounts: 0 flags (w) · 1 the key (signer).
    ClearFlag,
    /// 3. An `unpause` decision: clears the flags last set before the slot, refused under a hold.
    ///
    /// Accounts: 0 flags (w) · 1 state · 2 the owner (signer).
    Unpause { before_slot: u64 },
    /// 4. A `set guardians and watch keys` decision, through any hold.
    ///
    /// Accounts: 0 flags (w) · 1 state · 2 the owner (signer).
    SetGuardians { pairs: Vec<(u8, Pubkey)> },
    /// 5. Refused (`LayoutFixed`): the client's state layout is fixed at version 1 for the deployment's life (v27
    /// §13; kind 4 is reserved). Kept so the numbering of 6 stands.
    SetLayoutVersion { target: Pubkey, version: u8 },
    /// 6. Kind 9's hand-over.
    ///
    /// Accounts: 0 state (w) · 1 the owner (signer).
    TransferOwnership { new_owner: Pubkey },
    /// 7. Kind 12 `set mint limits` (AN5): a token's per-message maximum and rolling day cap, in the token's base
    /// units; the bucket's level stands. Owner-only.
    ///
    /// Accounts: 0 state (w) · 1 the owner (signer).
    SetMintLimits { token: u8, max: u64, day_cap: u64 },
}

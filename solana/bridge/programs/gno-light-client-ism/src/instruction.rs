//! The client's own instructions (borsh, by variant); the Hyperlane interface calls (`Type`, `Verify`,
//! `VerifyAccountMetas`) are told apart by their 8-byte discriminators first.

use crate::stage::StagedPrecommit;
use borsh::{BorshDeserialize, BorshSerialize};
use gno_light_core::{
    header::{BlockId, Header},
    merkle::SimpleProof,
    valset::Validator,
};
use solana_program::pubkey::Pubkey;

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub enum Instruction {
    /// 0. Writes the state: one-shot, signed by the program's current upgrade authority.
    ///
    /// Accounts: 0 state (w) · 1 payer (signer, w) · 2 the upgrade authority (signer) · 3 this program's account ·
    /// 4 its programdata account · 5 the system program.
    Init {
        chain_id: String,
        origin_domain: u32,
        trusted_height: u64,
        trusted_hash: [u8; 32],
        validators: Vec<Validator>,
        /// The admin program's authority PDA.
        admin: Pubkey,
    },
    /// 1. Creates a staged update or proof (the header is the record of §6.2, in this instruction's data). An
    /// announcing update names its next set's length and hash here (the header's NextValidatorsHash) and fills
    /// the set by `StageNextSet` over as many transactions as it takes (S-02).
    ///
    /// Accounts: 0 submitter (signer, w) · 1 stage (w) · 2 state · 3 the system program.
    StageInit {
        kind: u8,
        /// The header, or None for a proof by a ring entry.
        header: Option<Header>,
        /// The header hash of a ring proof.
        ring_hash: [u8; 32],
        /// The commit's BlockID (empty for a ring proof).
        block_id: BlockId,
        /// The commit's one round (tm2's `Commit.ValidateBasic`: every precommit's round is the commit's, S-08):
        /// every slot's sign bytes are built from it, and a `StagePrecommits` item of another round is refused
        /// (`RoundMismatch`). Never negative; 0 for a ring proof.
        round: i64,
        n_slots: u16,
        /// A proof's key: the message hash (sha256 of the message bytes).
        message_hash: [u8; 32],
        /// An announcing update's next set: its length (0 when the header announces nothing) and its hash.
        next_set_len: u16,
        next_set_hash: [u8; 32],
        proof: Option<SimpleProof>,
        leaf_len: u32,
    },
    /// 2. Fills precommit slots from `offset` (each slot once); an item's round must be the stage's (S-08).
    ///
    /// Accounts: 0 submitter (signer) · 1 stage (w).
    StagePrecommits { offset: u16, items: Vec<StagedPrecommit> },
    /// 3. Appends to a proof's leaf.
    ///
    /// Accounts: 0 submitter (signer) · 1 stage (w).
    StageLeaf { chunk: Vec<u8> },
    /// 4. Marks the slots whose signatures the ed25519 precompile instruction at `ix_index` of this transaction
    /// verified: entry j of the precompile is slot `slots[j]`.
    ///
    /// Accounts: 0 stage (w) · 1 state · 2 the Instructions sysvar.
    StageVerify { ix_index: u16, slots: Vec<u16> },
    /// 5. Applies a verified update; closes the stage, its rent to the submitter.
    ///
    /// Accounts: 0 stage (w) · 1 state (w) · 2 the submitter (w).
    UpdateFinish,
    /// 6. Closes a stage: by its submitter, or by anyone after 7 days, the rent to the closer.
    ///
    /// Accounts: 0 stage (w) · 1 closer (signer, w).
    StageClose,
    /// 7. Kind 9's hand-over: the admin PDA names its successor's. The client's controls are its upgrade
    /// authority (kind 8, the admin PDA's) and this: there is no reset (M2j). A client that has lapsed — no
    /// accepted header, a set change it cannot follow, a fork — stays lapsed, freezing gno → Solana for good;
    /// burns back to gno.land go on through the signers, and the exit is a fresh deployment.
    ///
    /// Accounts: 0 state (w) · 1 the admin PDA (signer).
    SetAdmin { new_admin: Pubkey },
    /// 8. Grows the state account (10 KB at most per call, the caller paying the rent): first the body region to
    /// the larger of what the body wants and `body_cap` (a target capacity in bytes, at or above the current one,
    /// bounded by `MAX_BODY_CAP`; 0 asks for nothing beyond the body's own want), then the ring to its 256
    /// entries. The ring moves to the new region only once the account holds that region and every entry the
    /// ring has (S-09: a stranger's growth loses no entry; the region moves on a later call otherwise). Anyone may
    /// call it; it is how the deploy finishes the account and how a bigger validator set gets room before the
    /// update that announces it (S-02: a target lets the room be made ahead, step by step).
    ///
    /// Accounts: 0 state (w) · 1 payer (signer, w) · 2 the system program.
    Grow { body_cap: u32 },
    /// 9. Fills an announcing update's next set from `offset`, in order (each call appends at the set's current
    /// length), over as many transactions as the set needs; `UpdateFinish` checks the whole set's hash (S-02).
    ///
    /// Accounts: 0 submitter (signer, w) · 1 stage (w) · 2 the system program (the stage grows with the set).
    StageNextSet { offset: u16, items: Vec<Validator> },
}

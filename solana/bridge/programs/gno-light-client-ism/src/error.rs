use solana_program::program_error::ProgramError;

/// The program's refusals, as `ProgramError::Custom(1000 + n)`; the core's errors map onto 1100 + theirs.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u32)]
pub enum ClientError {
    AlreadyInitialized = 0,
    NotInitialized,
    WrongVersion,
    WrongAuthority,
    WrongStage,
    WrongSubmitter,
    WrongSet,
    HeightNotAboveTrusted,
    HeightAboveTrusted,
    ChainId,
    ValidatorsHash,
    NextValidatorsHash,
    NextSetMissing,
    NextSetUnexpected,
    SlotOutOfRange,
    SlotNotFilled,
    SlotAlreadyFilled,
    SignatureMismatch,
    NotEnoughPower,
    LeafTooBig,
    LeafIncomplete,
    ProofMissing,
    ProofMismatch,
    NoResultsHash,
    NotInRing,
    OriginMismatch,
    DispatchMismatch,
    NotAbandoned,
    NotAdmin,
    Metadata,
    BadMessage,
    Overflow,
    NeedsGrow,
    StageIncomplete,
    /// The set history holds the newest settled-by-clock entry and four unsettled after it: a fifth unsettled
    /// announcement waits (the update stays staged) until the oldest of them settles by the clock (S-01).
    HistoryFull,
    /// A `StageNextSet` chunk not at the set's current length, or past its announced length (S-02).
    ChunkOutOfOrder,
    /// A `StagePrecommits` item whose round is not the stage's: a commit's precommits are of one round (tm2's
    /// `Commit.ValidateBasic`), the one `StageInit` carries (S-08).
    RoundMismatch,
}

impl From<ClientError> for ProgramError {
    fn from(e: ClientError) -> Self {
        ProgramError::Custom(1000 + e as u32)
    }
}

pub fn core_err(e: gno_light_core::Error) -> ProgramError {
    ProgramError::Custom(1100 + e as u32)
}

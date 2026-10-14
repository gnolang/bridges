use sol_util::guardians::GuardianError;
use solana_program::program_error::ProgramError;

/// `ProgramError::Custom(2000 + n)`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u32)]
pub enum PausableError {
    AlreadyInitialized = 0,
    NotInitialized,
    WrongVersion,
    WrongAuthority,
    NotOwner,
    UnknownKey,
    DuplicateKey,
    BadSeat,
    TooManyGuardians,
    TooManyWatch,
    CurrentKey,
    Hold,
    Paused,
    Quarantine,
    WrongTarget,
    WrongClientState,
    MissingClientProgram,
    /// The client's state layout is fixed at version 1 for the deployment's life (v27 §13): an init naming another
    /// version, or `SetLayoutVersion` at all.
    LayoutFixed,
    /// `Verify` outside the mailbox's `InboxProcess` of the message (the transaction's top-level instruction):
    /// a direct call, never a delivery (AN5).
    NotTheMailbox,
    /// A message to a token whose body is not a token message (shorter than 64 bytes, or an amount past 64 bits).
    BadTokenMessage,
    /// A mint above the token's per-message maximum.
    OverMax,
    /// A mint that would pass the token's rolling day cap.
    OverDayCap,
    /// `SetMintLimits` naming no token of this deployment.
    BadToken,
}

impl From<PausableError> for ProgramError {
    fn from(e: PausableError) -> Self {
        ProgramError::Custom(2000 + e as u32)
    }
}

pub fn guardian_err(e: GuardianError) -> ProgramError {
    match e {
        GuardianError::UnknownKey => PausableError::UnknownKey,
        GuardianError::DuplicateKey => PausableError::DuplicateKey,
        GuardianError::BadSeat => PausableError::BadSeat,
        GuardianError::TooManyGuardians => PausableError::TooManyGuardians,
        GuardianError::TooManyWatch => PausableError::TooManyWatch,
        GuardianError::CurrentKey => PausableError::CurrentKey,
        GuardianError::Hold => PausableError::Hold,
    }
    .into()
}

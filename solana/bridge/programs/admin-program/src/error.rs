use sol_util::guardians::GuardianError;
use solana_program::program_error::ProgramError;

/// `ProgramError::Custom(3000 + n)`. Compacted at M2j: the recovery path's refusals (`NoWindow`, `WindowClosed`,
/// `NotSettled`, `StaleStage`, `SignatureMismatch`, `NotEnoughPower`, `CancelSpent`, `NothingToSettle`,
/// `ChunkOutOfOrder`, `BodyIncomplete`) and the two kept-for-numbering names (`StoreFull`, `LongEnough`) are
/// gone, and every name after `WrongAccount` moved down.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u32)]
pub enum AdminError {
    AlreadyInitialized = 0,
    NotInitialized,
    WrongVersion,
    WrongAuthority,
    SetupDone,
    SetupIncomplete,
    NotTheMailbox,
    WrongOrigin,
    WrongSender,
    ShortBody,
    OutOfOrder,
    WrongDecision,
    NotMature,
    Deferred,
    UnknownKey,
    DuplicateKey,
    BadSeat,
    TooManyGuardians,
    TooManyWatch,
    CurrentKey,
    Hold,
    NotStaged,
    WrongBuffer,
    WrongAccount,
    WrongDeliverer,
    /// A decision to a predecessor that kind 9 retired (S-12): nothing is recorded, the message stays deliverable.
    /// Also a `StageUpgrade` there (S-13): nothing may start pending on a retired program.
    Retired,
    NeedsExtend,
    /// A successor asked to take a decision before `successor_init` wrote its predecessor's numbers (C227), or
    /// a `successor_init` on a program that is no successor or inherited already. Also (S-20) an `Execute` on a
    /// successor before it inherited: it runs nothing of its own until the hand-over, so no decision pends there
    /// outside the predecessor's hold.
    NotInherited,
}

impl From<AdminError> for ProgramError {
    fn from(e: AdminError) -> Self {
        ProgramError::Custom(3000 + e as u32)
    }
}

pub fn guardian_err(e: GuardianError) -> ProgramError {
    match e {
        GuardianError::UnknownKey => AdminError::UnknownKey,
        GuardianError::DuplicateKey => AdminError::DuplicateKey,
        GuardianError::BadSeat => AdminError::BadSeat,
        GuardianError::TooManyGuardians => AdminError::TooManyGuardians,
        GuardianError::TooManyWatch => AdminError::TooManyWatch,
        GuardianError::CurrentKey => AdminError::CurrentKey,
        GuardianError::Hold => AdminError::Hold,
    }
    .into()
}

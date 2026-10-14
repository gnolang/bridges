//! Every Solana delay in the plan is measured by the Clock sysvar's unix time (§6.1); the devnet build divides the
//! mainnet constants by a compiled factor of 8 (§5.9), which each program's `clock-factor-8` feature sets.

use solana_program::{clock::Clock, program_error::ProgramError, sysvar::Sysvar};

pub const HOUR: i64 = 3_600;
pub const DAY: i64 = 86_400;

/// 24 hours, 7 days, 14 days at the given factor.
pub fn unpause_delay(factor: i64) -> i64 {
    DAY / factor
}
pub fn week(factor: i64) -> i64 {
    7 * DAY / factor
}
pub fn fortnight(factor: i64) -> i64 {
    14 * DAY / factor
}

/// The Clock's slot and unix time.
pub fn now() -> Result<(u64, i64), ProgramError> {
    let c = Clock::get()?;
    Ok((c.slot, c.unix_timestamp))
}

use borsh::{BorshDeserialize, BorshSerialize};
use solana_program::{program_error::ProgramError, pubkey::Pubkey};

pub use sol_util::guardians::{Flag, Guardians};

/// The `state` PDA.
#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub struct State {
    pub version: u8,
    pub clock_factor: u8,
    /// The admin program's authority PDA.
    pub owner: Pubkey,
    /// The light-client program this ISM fronts, and the layout version of its state: always 1 (v27 §13: fixed
    /// for the deployment's life; `Verify` pins the constant and never reads this field, kept for the layout).
    pub target: Pubkey,
    pub target_version: u8,
    /// The mint limits per token (AN5), in the order of `config::TOKENS`.
    pub limits: [MintLimit; 2],
}

/// A token's mint limits: the most one message may carry, and a rolling day's cap — a bucket that drains at the
/// cap per day (24 hours at the clock factor) from the moment it was last charged.
#[derive(BorshSerialize, BorshDeserialize, Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct MintLimit {
    pub max_per_message: u64,
    pub day_cap: u64,
    /// What the day's bucket held when last charged, and when.
    pub used: u64,
    pub used_at: i64,
}

impl MintLimit {
    pub fn new(max_per_message: u64, day_cap: u64) -> Self {
        Self { max_per_message, day_cap, used: 0, used_at: 0 }
    }

    /// The bucket's level at `now`: what was charged, less what drained since (the cap a day).
    pub fn level(&self, now: i64, day: i64) -> u64 {
        let elapsed = now.saturating_sub(self.used_at).max(0) as u128;
        let drained = (elapsed * self.day_cap as u128 / day.max(1) as u128).min(u64::MAX as u128) as u64;
        self.used.saturating_sub(drained)
    }

    /// Charges a mint: refused above the per-message maximum, or when the bucket would pass the day's cap.
    pub fn charge(&mut self, amount: u64, now: i64, day: i64) -> Result<(), crate::error::PausableError> {
        if amount > self.max_per_message {
            return Err(crate::error::PausableError::OverMax);
        }
        let level = self.level(now, day);
        let next = level.saturating_add(amount);
        if next > self.day_cap {
            return Err(crate::error::PausableError::OverDayCap);
        }
        self.used = next;
        self.used_at = now;
        Ok(())
    }
}

impl State {
    pub fn load(data: &[u8]) -> Result<State, ProgramError> {
        let mut s = data;
        State::deserialize(&mut s).map_err(|_| ProgramError::InvalidAccountData)
    }

    pub fn store(&self, data: &mut [u8]) -> Result<(), ProgramError> {
        let b = borsh::to_vec(self).map_err(|_| ProgramError::BorshIoError)?;
        data.get_mut(..b.len()).ok_or(ProgramError::AccountDataTooSmall)?.copy_from_slice(&b);
        Ok(())
    }
}

/// The `flags` PDA: the seats, the watch slots and every flag.
#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub struct Flags {
    pub version: u8,
    pub guardians: Guardians,
}

impl Flags {
    pub fn load(data: &[u8]) -> Result<Flags, ProgramError> {
        let mut s = data;
        Flags::deserialize(&mut s).map_err(|_| ProgramError::InvalidAccountData)
    }

    pub fn store(&self, data: &mut [u8]) -> Result<(), ProgramError> {
        let b = borsh::to_vec(self).map_err(|_| ProgramError::BorshIoError)?;
        data.get_mut(..b.len()).ok_or(ProgramError::AccountDataTooSmall)?.copy_from_slice(&b);
        Ok(())
    }

    /// The account size: fixed, every flag and memory present.
    pub fn size() -> usize {
        let full = Flags {
            version: 0,
            guardians: Guardians { keys: [Pubkey::default(); 6], flags: [Some(Flag::default()); 6], hold_since: Some(Flag::default()), dip_since: Some(Flag::default()) },
        };
        borsh::object_length(&full).expect("borsh")
    }
}

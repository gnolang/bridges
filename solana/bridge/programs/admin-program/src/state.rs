use crate::config::*;
use borsh::{BorshDeserialize, BorshSerialize};
use sol_util::guardians::{Flag, Guardians};
use solana_program::{program_error::ProgramError, pubkey::Pubkey};

/// The state PDA, borsh, layout version 1 — the hand-over's wire contract (S-21). Kind 9's `Execute` parses the
/// *successor's* state with this crate's `State` (`processor::can_inherit` reads `predecessor`, `inherited`,
/// `successor` and `clock_factor` before any authority moves), and the Go side's `ParseAdminState` and hlwatch's
/// pre-vote check read it with this layout; so a successor of another build keeps it append-only: every field
/// below at its place and type, `predecessor` and `inherited` the last two (33 bytes at the end of the borsh form,
/// whatever the vectors hold), any new field after `inherited`, where this build's parse does not read. A field
/// inserted before them would move the two the peek reads — the transfer spent with nothing moved — and a reorder
/// the same; the suite pins the bytes (`the_hand_overs_wire_contract_is_frozen`). The version byte is the
/// successor's own to judge (its `load_state`); the peek reads the layout, not the version.
///
/// M2j took the recovery counter, the recorded settled set and the second channel out: the layout is `version
/// u8 ‖ clock_factor u8 ‖ setup_done u8 ‖ origin_domain u32 ‖ last_seq u64 ‖ guardians ‖ successor Option<Pubkey>
/// ‖ authority_bump u8 ‖ cancelled (u32 count ‖ from u64 ‖ to u64 each) ‖ holds (u32 count ‖ start i64 ‖ end i64
/// each) ‖ pending u32 ‖ predecessor 32 ‖ inherited u8`, little-endian. The account never grows: 64 ranges and
/// eight holds are reserved at init (1,574 bytes), and neither bound is ever exceeded.
#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub struct State {
    pub version: u8,
    pub clock_factor: u8,
    pub setup_done: u8,
    /// The origin domain whose messages are decisions: gno.land's, given at init and never changed (M2j: no
    /// reset); `finish_setup` requires the client's to equal it.
    pub origin_domain: u32,
    /// The last number recorded. It only rises (a decision records at `last_seq + 1`; nothing else sets it since
    /// M2j) and every stored cancelled range is clamped at it when it executes, so it is the sequence's
    /// high-water mark — what the hand-over gives a successor.
    pub last_seq: u64,
    pub guardians: Guardians,
    /// Kind 9's successor, once executed.
    pub successor: Option<Pubkey>,
    pub authority_bump: u8,
    /// Cancelled numbers whose accounts were not closed in the cancelling transaction: a decision in one of these
    /// ranges is dead (Execute closes it, CloseCancelled too), merged and bounded (MAX_CANCELLED).
    pub cancelled: Vec<Range>,
    /// The holds that ended, newest last (AN4): written by the flag instructions, an unpause of this program's
    /// flags and a replacement, each the moment it learns a hold ended (a dip that reached the grace, at the
    /// moment it did; a replacement's own clear, then). The standing hold is `guardians.hold_since`. A
    /// replacement's floor under a hold is the earliest hold overlapping [its delivery, now] + 21 days, so no
    /// re-flagging after a hold's end raises it and no `Execute` need have been attempted. The last eight; a
    /// ninth merges the two oldest (the earliest start kept), so churn can never push the earliest out.
    pub holds: Vec<Hold>,
    /// Pending decision accounts (S-04): every record counts one, every close counts one off. At zero no number
    /// at or below `last_seq` is pending, so every stored range is dead and pruned.
    pub pending: u32,
    /// A successor's predecessor (C227): the program whose authority PDA alone may call `successor_init`, which
    /// writes the live sequence number, seats, watch slots and hold history at the transfer; zero for the launch
    /// deployment. `inherited` is 1 once it did; a successor's `Handle` refuses until then, so no number is taken
    /// below the predecessor's.
    pub predecessor: Pubkey,
    pub inherited: u8,
}

/// A hold that ended: when its guardian flags reached two, and when it ended (Clock seconds).
#[derive(BorshSerialize, BorshDeserialize, Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Hold {
    pub start: i64,
    pub end: i64,
}

pub const MAX_HOLDS: usize = 8;

/// An inclusive range of cancelled numbers.
#[derive(BorshSerialize, BorshDeserialize, Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Range {
    pub from: u64,
    pub to: u64,
}

pub const MAX_CANCELLED: usize = 64;

impl State {
    pub fn is_cancelled(&self, number: u64) -> bool {
        self.cancelled.iter().any(|r| r.from <= number && number <= r.to)
    }

    /// Records a hold that ended; past eight, the two oldest merge into one spanning both.
    pub fn push_hold(&mut self, h: Hold) {
        if self.holds.len() >= MAX_HOLDS {
            let second = self.holds.remove(1);
            self.holds[0].end = self.holds[0].end.max(second.end);
        }
        self.holds.push(h);
    }

    /// A replacement's floor while a hold stands (AN4): the earliest hold overlapping [its delivery, now] — the
    /// standing one, or an ended one whose end is at or after the delivery — plus `day21` (21 days at the clock
    /// factor).
    pub fn replacement_floor(&self, delivered_time: i64, standing: i64, day21: i64) -> i64 {
        self.holds.iter().filter(|h| h.end >= delivered_time).map(|h| h.start).fold(standing, i64::min).saturating_add(day21)
    }

    /// Adds a cancelled range, merging with any it overlaps or touches. When the store is full it merges the new
    /// range with the nearest stored one rather than refuse (AN7): the gap's numbers die too, which only kills
    /// pending decisions (never executes one), and a cancel is never jammed.
    pub fn add_cancelled(&mut self, mut r: Range) {
        if r.from > r.to {
            return;
        }
        let mut i = 0;
        while i < self.cancelled.len() {
            let c = self.cancelled[i];
            if c.from <= r.to.saturating_add(1) && r.from <= c.to.saturating_add(1) {
                r.from = r.from.min(c.from);
                r.to = r.to.max(c.to);
                self.cancelled.remove(i);
            } else {
                i += 1;
            }
        }
        if self.cancelled.len() >= MAX_CANCELLED {
            let gap = |c: &Range| -> u64 {
                if c.to < r.from {
                    r.from - c.to
                } else if r.to < c.from {
                    c.from - r.to
                } else {
                    0
                }
            };
            if let Some(i) = self.cancelled.iter().enumerate().min_by_key(|(_, c)| gap(c)).map(|(i, _)| i) {
                let c = self.cancelled.remove(i);
                r.from = r.from.min(c.from);
                r.to = r.to.max(c.to);
            }
        }
        self.cancelled.push(r);
    }

    /// Drops every stored range: called when the pending count reaches zero, when no number at or below
    /// `last_seq` is pending and every stored range is dead weight.
    pub fn prune(&mut self) {
        self.cancelled.clear();
    }
}

impl State {
    pub fn load(data: &[u8]) -> Result<State, ProgramError> {
        let mut s = data;
        State::deserialize(&mut s).map_err(|_| ProgramError::InvalidAccountData)
    }

    /// Writes the state straight into the account (no buffer on the heap), zeroing the rest.
    pub fn store(&self, data: &mut [u8]) -> Result<(), ProgramError> {
        let len = self.size();
        let t = data.get_mut(..len).ok_or(ProgramError::AccountDataTooSmall)?;
        let mut w: &mut [u8] = t;
        self.serialize(&mut w).map_err(|_| ProgramError::BorshIoError)?;
        if let Some(rest) = data.get_mut(len..) {
            rest.fill(0);
        }
        Ok(())
    }

    pub fn size(&self) -> usize {
        borsh::object_length(self).expect("borsh")
    }

    /// The account size at init: every optional field present, the stores at their bounds — the most the state
    /// is ever.
    pub fn init_size() -> usize {
        let full = State {
            version: 0,
            clock_factor: 0,
            setup_done: 0,
            origin_domain: 0,
            last_seq: 0,
            guardians: Guardians { keys: [Pubkey::default(); 6], flags: [Some(Flag::default()); 6], hold_since: Some(Flag::default()), dip_since: Some(Flag::default()) },
            cancelled: vec![Range::default(); MAX_CANCELLED],
            successor: Some(Pubkey::default()),
            authority_bump: 0,
            holds: vec![Hold::default(); MAX_HOLDS],
            pending: 0,
            predecessor: Pubkey::default(),
            inherited: 0,
        };
        full.size()
    }
}

/// A pending decision: its own account, rent paid by the deliverer and returned when it ends. Its account is
/// `DECISION_OVERHEAD` (95) bytes beside the parameters (version 1 ‖ number 8 ‖ kind 1 ‖ params u32 length 4 ‖
/// delivered slot 8 ‖ delivered time 8 ‖ deliverer 32 ‖ refund key as `Some` 33), which bounds `MAX_PARAMS`
/// (S-14).
#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub struct Decision {
    pub version: u8,
    pub number: u64,
    pub kind: u8,
    pub params: Vec<u8>,
    pub delivered_slot: u64,
    pub delivered_time: i64,
    pub deliverer: Pubkey,
    /// Kind 8: the buffer writer `stage upgrade` recorded, the loader's spill and the buffer's recipient.
    pub refund_key: Option<Pubkey>,
}

impl Decision {
    pub fn load(data: &[u8]) -> Result<Decision, ProgramError> {
        let mut s = data;
        Decision::deserialize(&mut s).map_err(|_| ProgramError::InvalidAccountData)
    }

    pub fn store(&self, data: &mut [u8]) -> Result<(), ProgramError> {
        let b = borsh::to_vec(self).map_err(|_| ProgramError::BorshIoError)?;
        let t = data.get_mut(..b.len()).ok_or(ProgramError::AccountDataTooSmall)?;
        t.copy_from_slice(&b);
        Ok(())
    }

    pub fn size(&self) -> usize {
        let mut full = self.clone();
        full.refund_key = Some(Pubkey::default());
        borsh::object_length(&full).expect("borsh")
    }

    /// The decision PDA: (`decision`, the number big-endian).
    pub fn address(program_id: &Pubkey, number: u64) -> (Pubkey, u8) {
        Pubkey::find_program_address(&[DECISION_SEED, &number.to_be_bytes()], program_id)
    }
}

pub fn authority_address(program_id: &Pubkey) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[AUTHORITY_SEED], program_id)
}

pub fn state_address(program_id: &Pubkey) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[STATE_SEED], program_id)
}

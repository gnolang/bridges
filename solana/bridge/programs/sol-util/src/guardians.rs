//! The guardians' seats, the watch slots and their flags, as the pausable ISM and the admin program both keep them
//! (§5.8, §6.1, §6.5, plan v24): four numbered seats (3 and 4 the outsiders'), two watch slots, no key in two
//! places; a flag per key carrying one slot, the moment it was last set, fresh on every set, which only an unpause
//! reads; and the object's hold: `hold_since`, the moment its standing guardian flags reached two, kept while they
//! stay at two or more and through a dip below two shorter than the grace (24 hours at the build's clock factor),
//! ended by a dip of the grace's length, after which the next two flags begin a new one. An unpause clears the
//! flags set before a slot and is refused under a hold; a replacement (at most two guardians plus either watch slot
//! per decision, never a seat's current key) clears the replaced keys' flags and lands through any hold.

use borsh::{BorshDeserialize, BorshSerialize};
use solana_program::pubkey::Pubkey;

pub const SEATS: usize = 4;
pub const WATCH: usize = 2;
pub const KEYS: usize = SEATS + WATCH;

/// A moment: the Clock's slot and unix time.
#[derive(BorshSerialize, BorshDeserialize, Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Flag {
    pub slot: u64,
    pub time: i64,
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, Default, PartialEq, Eq)]
pub struct Guardians {
    /// Seats 1–4 then watch slots 5–6, by index 0–5.
    pub keys: [Pubkey; KEYS],
    /// Each key's flag: the moment it was last set.
    pub flags: [Option<Flag>; KEYS],
    /// The object's hold: when its standing guardian flags reached two; None when no hold stands.
    pub hold_since: Option<Flag>,
    /// While a hold stands, the moment its guardian flags last dipped below two (None at two or more).
    pub dip_since: Option<Flag>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum GuardianError {
    UnknownKey,
    DuplicateKey,
    BadSeat,
    TooManyGuardians,
    TooManyWatch,
    CurrentKey,
    Hold,
}

impl Guardians {
    pub fn new(seats: [Pubkey; SEATS], watch: [Pubkey; WATCH]) -> Result<Self, GuardianError> {
        let mut keys = [Pubkey::default(); KEYS];
        keys[..SEATS].copy_from_slice(&seats);
        keys[SEATS..].copy_from_slice(&watch);
        for i in 0..KEYS {
            if keys[i] == Pubkey::default() {
                return Err(GuardianError::UnknownKey);
            }
            for j in 0..i {
                if keys[i] == keys[j] {
                    return Err(GuardianError::DuplicateKey);
                }
            }
        }
        Ok(Self { keys, flags: [None; KEYS], hold_since: None, dip_since: None })
    }

    pub fn position(&self, key: &Pubkey) -> Option<usize> {
        self.keys.iter().position(|k| k == key)
    }

    pub fn is_guardian(i: usize) -> bool {
        i < SEATS
    }

    pub fn any_flag(&self) -> bool {
        self.flags.iter().any(|f| f.is_some())
    }

    pub fn guardian_flags(&self) -> usize {
        self.flags[..SEATS].iter().filter(|f| f.is_some()).count()
    }

    /// The hold standing at `now`, if any: its start. A pure function of the stored moments and the Clock, so a
    /// reader of another program's state sees a dip that has run its course as the hold's end.
    pub fn hold(&self, now: i64, grace: i64) -> Option<Flag> {
        let since = self.hold_since?;
        if self.guardian_flags() >= 2 {
            return Some(since);
        }
        match self.dip_since {
            Some(d) if now.saturating_sub(d.time) < grace => Some(since),
            _ => None,
        }
    }

    /// Brings the stored moments up to date before a change: a dip that reached the grace ended the hold.
    fn settle(&mut self, now: Flag, grace: i64) {
        if self.hold_since.is_some() {
            if let Some(d) = self.dip_since {
                if now.time.saturating_sub(d.time) >= grace {
                    self.hold_since = None;
                    self.dip_since = None;
                }
            }
        }
    }

    /// After a change: two standing guardian flags start a hold (or end a dip); fewer start a dip.
    fn recompute(&mut self, now: Flag) {
        if self.guardian_flags() >= 2 {
            if self.hold_since.is_none() {
                self.hold_since = Some(now);
            }
            self.dip_since = None;
        } else if self.hold_since.is_some() && self.dip_since.is_none() {
            self.dip_since = Some(now);
        }
    }

    /// Sets a key's flag at `now` (fresh on every set).
    pub fn set_flag(&mut self, key: &Pubkey, now: Flag, grace: i64) -> Result<(), GuardianError> {
        let i = self.position(key).ok_or(GuardianError::UnknownKey)?;
        self.settle(now, grace);
        self.flags[i] = Some(now);
        self.recompute(now);
        Ok(())
    }

    pub fn clear_flag(&mut self, key: &Pubkey, now: Flag, grace: i64) -> Result<(), GuardianError> {
        let i = self.position(key).ok_or(GuardianError::UnknownKey)?;
        self.settle(now, grace);
        self.flags[i] = None;
        self.recompute(now);
        Ok(())
    }

    /// An `unpause` decision: clears every flag whose last set slot is before `before_slot`, refused while a hold
    /// stands. Returns how many it cleared.
    pub fn unpause(&mut self, before_slot: u64, now: Flag, grace: i64) -> Result<usize, GuardianError> {
        self.settle(now, grace);
        if self.hold(now.time, grace).is_some() {
            return Err(GuardianError::Hold);
        }
        let mut n = 0;
        for f in self.flags.iter_mut() {
            if matches!(f, Some(x) if x.slot < before_slot) {
                *f = None;
                n += 1;
            }
        }
        self.recompute(now);
        Ok(n)
    }

    /// A `set guardians and watch keys` decision: pairs of (seat 1–6, key), at most four — two guardian seats and
    /// the two watch slots; a seat's current key refused; no key in two places afterwards; the replaced keys'
    /// flags cleared.
    pub fn replace(&mut self, pairs: &[(u8, Pubkey)], now: Flag, grace: i64) -> Result<(), GuardianError> {
        if pairs.is_empty() || pairs.len() > 4 {
            return Err(GuardianError::BadSeat);
        }
        let mut next = self.keys;
        let mut replaced = [false; KEYS];
        let (mut guardians, mut watch) = (0, 0);
        for (seat, key) in pairs {
            let i = match seat {
                1..=6 => (*seat - 1) as usize,
                _ => return Err(GuardianError::BadSeat),
            };
            if replaced[i] {
                return Err(GuardianError::BadSeat);
            }
            if *key == Pubkey::default() {
                return Err(GuardianError::UnknownKey);
            }
            if self.keys[i] == *key {
                return Err(GuardianError::CurrentKey);
            }
            if Self::is_guardian(i) {
                guardians += 1;
            } else {
                watch += 1;
            }
            next[i] = *key;
            replaced[i] = true;
        }
        if guardians > 2 {
            return Err(GuardianError::TooManyGuardians);
        }
        if watch > WATCH {
            return Err(GuardianError::TooManyWatch);
        }
        for i in 0..KEYS {
            for j in 0..i {
                if next[i] == next[j] {
                    return Err(GuardianError::DuplicateKey);
                }
            }
        }
        self.settle(now, grace);
        let mut cleared_guardian = false;
        for i in 0..KEYS {
            if replaced[i] {
                if Self::is_guardian(i) && self.flags[i].is_some() {
                    cleared_guardian = true;
                }
                self.flags[i] = None;
            }
        }
        self.keys = next;
        // a replacement's clear that leaves fewer than two guardian flags ends the hold at once (v25 §6.1): the
        // 24-hour grace is for a holder's own clearing, never for GovDAO's replacement of the holders. The clear
        // must be the replacement's own — a standing guardian flag it cleared; one that replaces nobody holding
        // (a watch slot, a seat whose flag is down) during a dip ends nothing, and the dip runs on as before
        if cleared_guardian && self.guardian_flags() < 2 {
            self.hold_since = None;
            self.dip_since = None;
        } else {
            self.recompute(now);
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const DAY: i64 = 86_400;

    fn keys() -> [Pubkey; KEYS] {
        let mut k = [Pubkey::default(); KEYS];
        for (i, x) in k.iter_mut().enumerate() {
            *x = Pubkey::new_from_array([i as u8 + 1; 32]);
        }
        k
    }

    fn g() -> Guardians {
        let k = keys();
        Guardians::new([k[0], k[1], k[2], k[3]], [k[4], k[5]]).unwrap()
    }

    fn at(t: i64) -> Flag {
        Flag { slot: t as u64, time: t }
    }

    #[test]
    fn flags_and_unpause() {
        let k = keys();
        let mut g = g();
        g.set_flag(&k[0], at(100), DAY).unwrap();
        assert!(g.hold(100, DAY).is_none());
        g.set_flag(&k[4], at(110), DAY).unwrap(); // a watch key never makes a hold
        assert!(g.hold(110, DAY).is_none());
        assert_eq!(g.unpause(111, at(120), DAY).unwrap(), 2);
        assert!(!g.any_flag());
        // a flag's slot is fresh on every set
        g.set_flag(&k[0], at(200), DAY).unwrap();
        g.set_flag(&k[0], at(300), DAY).unwrap();
        assert_eq!(g.flags[0], Some(at(300)));
        assert_eq!(g.unpause(300, at(310), DAY).unwrap(), 0); // set at 300, not before 300
        assert_eq!(g.unpause(301, at(310), DAY).unwrap(), 1);
        assert_eq!(g.set_flag(&Pubkey::new_from_array([9; 32]), at(1), DAY).unwrap_err(), GuardianError::UnknownKey);
    }

    #[test]
    fn holds_belong_to_the_object() {
        let k = keys();
        let mut g = g();
        g.set_flag(&k[2], at(1000), DAY).unwrap();
        g.set_flag(&k[3], at(1500), DAY).unwrap();
        assert_eq!(g.hold(1500, DAY), Some(at(1500)));
        assert_eq!(g.unpause(9999, at(1600), DAY).unwrap_err(), GuardianError::Hold);
        // three keys taking turns: the start never moves
        g.set_flag(&k[1], at(2000), DAY).unwrap();
        g.clear_flag(&k[2], at(2100), DAY).unwrap();
        assert_eq!(g.hold(2100, DAY), Some(at(1500)));
        g.set_flag(&k[2], at(2200), DAY).unwrap();
        g.clear_flag(&k[3], at(2300), DAY).unwrap();
        g.clear_flag(&k[1], at(2400), DAY).unwrap(); // down to one: a dip begins
        assert_eq!(g.dip_since, Some(at(2400)));
        assert_eq!(g.hold(2400 + DAY - 1, DAY), Some(at(1500)), "a dip shorter than the grace keeps the hold");
        g.set_flag(&k[3], at(2400 + DAY - 100), DAY).unwrap(); // back to two before the grace: the dip ends
        assert_eq!(g.dip_since, None);
        assert_eq!(g.hold(2400 + DAY + 5000, DAY), Some(at(1500)));
        // a dip of the grace's length ends the hold; the next two flags begin a new one
        g.clear_flag(&k[3], at(10_000), DAY).unwrap();
        assert_eq!(g.hold(10_000 + DAY - 1, DAY), Some(at(1500)));
        assert_eq!(g.hold(10_000 + DAY, DAY), None, "read after the grace: ended");
        assert_eq!(g.unpause(u64::MAX, at(10_000 + DAY), DAY).unwrap(), 1, "the stale hold is settled at the change");
        assert_eq!(g.hold_since, None);
        g.set_flag(&k[0], at(20_000), DAY).unwrap();
        g.set_flag(&k[1], at(20_500), DAY).unwrap();
        assert_eq!(g.hold(20_500, DAY), Some(at(20_500)));
        // a dip that ran its course without any change is settled by the next set, which starts a fresh hold
        g.clear_flag(&k[0], at(21_000), DAY).unwrap();
        g.set_flag(&k[0], at(21_000 + 2 * DAY), DAY).unwrap();
        assert_eq!(g.hold_since, Some(at(21_000 + 2 * DAY)));
    }

    #[test]
    fn replacement() {
        let k = keys();
        let mut g = g();
        g.set_flag(&k[2], at(20), DAY).unwrap();
        g.set_flag(&k[3], at(25), DAY).unwrap();
        let n1 = Pubkey::new_from_array([77; 32]);
        let n2 = Pubkey::new_from_array([78; 32]);
        let n3 = Pubkey::new_from_array([79; 32]);
        let n4 = Pubkey::new_from_array([80; 32]);
        assert_eq!(g.replace(&[(3, k[2])], at(30), DAY).unwrap_err(), GuardianError::CurrentKey);
        assert_eq!(g.replace(&[(3, k[0])], at(30), DAY).unwrap_err(), GuardianError::DuplicateKey);
        assert_eq!(g.replace(&[(1, n1), (2, n2), (3, n3)], at(30), DAY).unwrap_err(), GuardianError::TooManyGuardians);
        assert_eq!(g.replace(&[(7, n1)], at(30), DAY).unwrap_err(), GuardianError::BadSeat);
        assert_eq!(g.replace(&[(3, n1), (3, n2)], at(30), DAY).unwrap_err(), GuardianError::BadSeat);
        assert_eq!(g.replace(&[(1, n1), (2, n2), (5, n3), (6, n4), (3, k[0])], at(30), DAY).unwrap_err(), GuardianError::BadSeat);
        // through the hold: seats 3 and 4 replaced, their flags cleared, both watch slots too; the hold ends at
        // once, no grace
        g.replace(&[(3, n1), (4, n2), (5, n3), (6, n4)], at(30), DAY).unwrap();
        assert_eq!(g.keys[2], n1);
        assert_eq!(g.keys[3], n2);
        assert_eq!(g.keys[4], n3);
        assert_eq!(g.keys[5], n4);
        assert!(!g.any_flag());
        assert_eq!(g.hold_since, None);
        assert_eq!(g.dip_since, None);
        assert_eq!(g.hold(30, DAY), None);
        // a replacement of one holder of three leaves two: the hold stands, its start unchanged
        let k = keys();
        let mut g = Guardians::new([k[0], k[1], k[2], k[3]], [k[4], k[5]]).unwrap();
        g.set_flag(&k[1], at(10), DAY).unwrap();
        g.set_flag(&k[2], at(20), DAY).unwrap();
        g.set_flag(&k[3], at(30), DAY).unwrap();
        g.replace(&[(2, n1)], at(40), DAY).unwrap();
        assert_eq!(g.hold(40, DAY), Some(at(20)));
        // during a dip (one flag), a replacement of the standing holder ends the hold at once too
        g.clear_flag(&k[2], at(50), DAY).unwrap();
        assert_eq!(g.hold(60, DAY), Some(at(20)));
        g.replace(&[(4, n2)], at(60), DAY).unwrap();
        assert_eq!(g.hold(60, DAY), None);
    }

    #[test]
    fn a_replacement_of_nobody_holding_ends_no_hold() {
        let k = keys();
        let n5 = Pubkey::new_from_array([85; 32]);
        let n1 = Pubkey::new_from_array([81; 32]);
        let n2 = Pubkey::new_from_array([82; 32]);
        // seats 3 and 4 hold; a dip to one flag
        let mut g = Guardians::new([k[0], k[1], k[2], k[3]], [k[4], k[5]]).unwrap();
        g.set_flag(&k[2], at(100), DAY).unwrap();
        g.set_flag(&k[3], at(200), DAY).unwrap();
        g.clear_flag(&k[3], at(300), DAY).unwrap();
        assert_eq!(g.dip_since, Some(at(300)));
        // a replacement of a watch slot during the dip clears no guardian flag: the hold stands, the dip runs on
        g.replace(&[(5, n5)], at(400), DAY).unwrap();
        assert_eq!(g.hold_since, Some(at(200)));
        assert_eq!(g.dip_since, Some(at(300)), "the dip's start unchanged");
        assert_eq!(g.hold(300 + DAY - 1, DAY), Some(at(200)));
        // a replacement of a seat whose flag is down (seat 1) during the dip: the same
        g.replace(&[(1, n1)], at(500), DAY).unwrap();
        assert_eq!((g.hold_since, g.dip_since), (Some(at(200)), Some(at(300))));
        // the dip reaches the grace: the hold ends by the dip, not by any replacement
        assert_eq!(g.hold(300 + DAY, DAY), None);
        // with two flags standing, a replacement of a non-holder leaves the hold as it was
        let mut g = Guardians::new([k[0], k[1], k[2], k[3]], [k[4], k[5]]).unwrap();
        g.set_flag(&k[2], at(100), DAY).unwrap();
        g.set_flag(&k[3], at(200), DAY).unwrap();
        g.replace(&[(2, n2), (6, n5)], at(300), DAY).unwrap();
        assert_eq!(g.hold(300, DAY), Some(at(200)));
        assert!(g.dip_since.is_none());
        // and the replacement of one holder of two — its own clear — ends it at once, dip or no dip
        g.replace(&[(3, n1)], at(400), DAY).unwrap();
        assert_eq!(g.hold(400, DAY), None);
    }
}

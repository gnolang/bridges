//! The kind table (§13) and the execution of each kind.
//!
//! Parameters are big-endian, as the sequence number is: 1 `cancel` (salt u64 — random, ignored here; r/hlgov
//! prepends it to every cancel it dispatches — then form u8: 0 a list, count u8 ≤ 16 then `channel u8 ‖ number
//! u64` each; 1 a range, `channel u8 ‖ from u64 ‖ to u64` inclusive; the channel byte stays on the wire and must
//! be 0 — the one channel since M2j — else the cancel does not parse and is spent) · 2 `unpause` (object u8) · 3
//! `set token ISM` (token u8; ISM 32) · 4 reserved (refused at execution: the client's state layout is fixed for
//! the deployment's life, v27 §13) · 5 `set guardians` (count u8 ≤ 4, then `seat u8 ‖ pubkey 32`) · 6 `enroll
//! router` (token u8; domain u32; router 32) · 7 `set protocol fee` (lamports u64; beneficiary 32) · 8 `upgrade`
//! (program 32, never the admin program's own id; buffer 32; ELF sha256 32) · 9 `transfer to successor` (program
//! 32, never the admin program's own id: spent, S-15) · 12 `set mint limits` (token u8; max u64; day cap u64 —
//! the pausable ISM's per-message maximum and rolling day cap for the token, AN5). Kinds 10 (`set sequence`) and
//! 11 (`reset client`) arrived by the recovery channel alone and are unknown since M2j: recorded and spent at
//! recording like any unknown kind.

use crate::{config::*, error::AdminError, state::State};
use solana_program::pubkey::Pubkey;

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Params {
    /// A salt the program ignores (it makes every cancel's body its own, §13), then inclusive ranges of
    /// (from, to); a list entry is a range of one.
    Cancel { salt: u64, ranges: Vec<(u64, u64)> },
    Unpause(u8),
    SetTokenIsm { token: u8, ism: Pubkey },
    SetGuardians(Vec<(u8, Pubkey)>),
    EnrollRouter { token: u8, domain: u32, router: [u8; 32] },
    SetFee { lamports: u64, beneficiary: Pubkey },
    Upgrade { program: Pubkey, buffer: Pubkey, hash: [u8; 32] },
    Transfer { program: Pubkey },
    /// Kind 12 (AN5): a token's per-message maximum and rolling day cap, in its base units.
    SetMintLimits { token: u8, max: u64, day_cap: u64 },
}

pub const KIND_CANCEL: u8 = 1;
pub const KIND_UNPAUSE: u8 = 2;
pub const KIND_SET_TOKEN_ISM: u8 = 3;
/// Reserved (v27 §13): recorded like any kind of the 7-day group, refused — spent — at execution.
pub const KIND_RESERVED: u8 = 4;
pub const KIND_SET_GUARDIANS: u8 = 5;
pub const KIND_ENROLL_ROUTER: u8 = 6;
pub const KIND_SET_FEE: u8 = 7;
pub const KIND_UPGRADE: u8 = 8;
pub const KIND_TRANSFER: u8 = 9;
/// `set mint limits` (AN5): token u8 ‖ max u64 ‖ day cap u64; the 7-day group.
pub const KIND_SET_MINT_LIMITS: u8 = 12;

/// The kinds the program records as pending (the rest — 10 and 11 among them since M2j — are unknown, recorded
/// and spent at recording). Kind 4, reserved, is recorded and spent at execution like any decision whose
/// parameters fail.
pub fn known_kind(kind: u8) -> bool {
    (1..=9).contains(&kind) || kind == KIND_SET_MINT_LIMITS
}

/// Kinds 3–9 and 12 are the 7-day group.
pub fn seven_day(kind: u8) -> bool {
    (3..=9).contains(&kind) || kind == KIND_SET_MINT_LIMITS
}

struct Reader<'a> {
    b: &'a [u8],
    pos: usize,
}

impl<'a> Reader<'a> {
    fn take(&mut self, n: usize) -> Option<&'a [u8]> {
        let s = self.b.get(self.pos..self.pos + n)?;
        self.pos += n;
        Some(s)
    }
    fn u8(&mut self) -> Option<u8> {
        Some(self.take(1)?[0])
    }
    fn u32(&mut self) -> Option<u32> {
        Some(u32::from_be_bytes(self.take(4)?.try_into().unwrap()))
    }
    fn u64(&mut self) -> Option<u64> {
        Some(u64::from_be_bytes(self.take(8)?.try_into().unwrap()))
    }
    fn arr(&mut self) -> Option<[u8; 32]> {
        Some(self.take(32)?.try_into().unwrap())
    }
    fn key(&mut self) -> Option<Pubkey> {
        Some(Pubkey::new_from_array(self.arr()?))
    }
    fn done(&self) -> bool {
        self.pos == self.b.len()
    }
}

/// The one channel's byte in a cancel's body: r/hlgov writes it (§13), and any other value is a cancel that
/// does not parse.
const CHANNEL: u8 = 0;

/// Parses a kind's parameters; None where they do not parse (the decision is spent).
pub fn parse(kind: u8, params: &[u8]) -> Option<Params> {
    let mut r = Reader { b: params, pos: 0 };
    let p = match kind {
        KIND_CANCEL => {
            let salt = r.u64()?;
            match r.u8()? {
                0 => {
                    let n = r.u8()? as usize;
                    if n == 0 || n > MAX_CANCEL {
                        return None;
                    }
                    let mut v = Vec::with_capacity(n);
                    for _ in 0..n {
                        if r.u8()? != CHANNEL {
                            return None;
                        }
                        let x = r.u64()?;
                        v.push((x, x));
                    }
                    Params::Cancel { salt, ranges: v }
                }
                1 => {
                    let ch = r.u8()?;
                    let from = r.u64()?;
                    let to = r.u64()?;
                    if ch != CHANNEL || from > to {
                        return None;
                    }
                    Params::Cancel { salt, ranges: vec![(from, to)] }
                }
                _ => return None,
            }
        }
        KIND_UNPAUSE => {
            let o = r.u8()?;
            if o > 1 {
                return None;
            }
            Params::Unpause(o)
        }
        KIND_SET_TOKEN_ISM => Params::SetTokenIsm { token: r.u8()?, ism: r.key()? },
        KIND_RESERVED => return None,
        KIND_SET_GUARDIANS => {
            let n = r.u8()? as usize;
            if n == 0 || n > 4 {
                return None;
            }
            let mut v = Vec::with_capacity(n);
            for _ in 0..n {
                v.push((r.u8()?, r.key()?));
            }
            Params::SetGuardians(v)
        }
        KIND_ENROLL_ROUTER => Params::EnrollRouter { token: r.u8()?, domain: r.u32()?, router: r.arr()? },
        KIND_SET_FEE => Params::SetFee { lamports: r.u64()?, beneficiary: r.key()? },
        KIND_UPGRADE => Params::Upgrade { program: r.key()?, buffer: r.key()?, hash: r.arr()? },
        KIND_TRANSFER => Params::Transfer { program: r.key()? },
        KIND_SET_MINT_LIMITS => {
            let token = r.u8()?;
            if token as usize >= TOKENS.len() {
                return None;
            }
            Params::SetMintLimits { token, max: r.u64()?, day_cap: r.u64()? }
        }
        _ => return None,
    };
    if !r.done() {
        return None;
    }
    Some(p)
}

/// Encodes parameters (the tests and the tools; r/hlgov writes the same bytes).
pub fn encode(p: &Params) -> Vec<u8> {
    let mut v = Vec::new();
    match p {
        Params::Cancel { salt, ranges: list } => {
            v.extend_from_slice(&salt.to_be_bytes());
            if list.len() == 1 && list[0].0 != list[0].1 {
                v.push(1);
                v.push(CHANNEL);
                v.extend_from_slice(&list[0].0.to_be_bytes());
                v.extend_from_slice(&list[0].1.to_be_bytes());
            } else {
                v.push(0);
                v.push(list.len() as u8);
                for (from, _) in list {
                    v.push(CHANNEL);
                    v.extend_from_slice(&from.to_be_bytes());
                }
            }
        }
        Params::Unpause(o) => v.push(*o),
        Params::SetTokenIsm { token, ism } => {
            v.push(*token);
            v.extend_from_slice(ism.as_ref());
        }
        Params::SetGuardians(pairs) => {
            v.push(pairs.len() as u8);
            for (s, k) in pairs {
                v.push(*s);
                v.extend_from_slice(k.as_ref());
            }
        }
        Params::EnrollRouter { token, domain, router } => {
            v.push(*token);
            v.extend_from_slice(&domain.to_be_bytes());
            v.extend_from_slice(router);
        }
        Params::SetFee { lamports, beneficiary } => {
            v.extend_from_slice(&lamports.to_be_bytes());
            v.extend_from_slice(beneficiary.as_ref());
        }
        Params::Upgrade { program, buffer, hash } => {
            v.extend_from_slice(program.as_ref());
            v.extend_from_slice(buffer.as_ref());
            v.extend_from_slice(hash);
        }
        Params::Transfer { program } => v.extend_from_slice(program.as_ref()),
        Params::SetMintLimits { token, max, day_cap } => {
            v.push(*token);
            v.extend_from_slice(&max.to_be_bytes());
            v.extend_from_slice(&day_cap.to_be_bytes());
        }
    }
    v
}

/// The kind of a decision body and the body itself (the tests and the tools).
pub fn body(kind: u8, seq: u64, p: &Params) -> Vec<u8> {
    let mut v = vec![kind];
    v.extend_from_slice(&seq.to_be_bytes());
    v.extend_from_slice(&encode(p));
    v
}

/// What a kind needs beyond the executor's accounts, as `Execute` reads them after its six fixed accounts.
pub fn describe(kind: u8) -> &'static str {
    match kind {
        KIND_CANCEL => "executes at recording: the authority PDA, then pairs of a decision (w) and its deliverer (w) to close now (a staged kind 8 adds its buffer (w), refund key (w) and the loader); the numbers not passed stay cancelled in the state",
        KIND_UNPAUSE => "object 0: the pausable ISM's state, the pausable ISM program (its flags, account 5, writable)",
        KIND_SET_TOKEN_ISM => "the token's PDA (w), the token program",
        KIND_RESERVED => "none: reserved, spent at execution",
        KIND_SET_GUARDIANS => "the pausable ISM's state, the pausable ISM program (its flags, account 5, writable)",
        KIND_ENROLL_ROUTER => "the system program, the token's PDA (w), the token program",
        KIND_SET_FEE => "the mailbox outbox (w), the mailbox program",
        KIND_UPGRADE => "programdata (w), program (w), buffer (w), the refund key (w), rent, clock, the system program, the loader",
        KIND_TRANSFER => "successor program, successor programdata, the loader, the successor's authority PDA (w: the hand-over sweeps this program's PDA's lamports into it, S-25), five programdata (w), outbox (w), mailbox, token 0 PDA (w), token 0, token 1 PDA (w), token 1, pausable state (w), pausable, the client's state (w), the client program, the successor's state (w), the system program; none when the successor named is this program itself: spent",
        10 | 11 => "unknown since M2j (the recovery channel is gone): recorded and spent at recording, never pending",
        KIND_SET_MINT_LIMITS => "the pausable ISM's state (w), the pausable ISM program",
        _ => "unknown",
    }
}

/// Whether a key is one of this program's seats or slots (a kind 5 naming a current key is refused at
/// execution, as r/hlgov refuses it at creation).
pub fn names_current_key(state: &State, pairs: &[(u8, Pubkey)]) -> bool {
    pairs.iter().any(|(_, k)| state.guardians.position(k).is_some())
}

/// The clock of a kind (seconds after delivery), without the hold rule of kind 5.
pub fn delay(kind: u8) -> i64 {
    match kind {
        KIND_CANCEL => 0,
        KIND_UNPAUSE => sol_util::clock::unpause_delay(CLOCK_FACTOR),
        _ => sol_util::clock::week(CLOCK_FACTOR),
    }
}

pub fn err_code(e: AdminError) -> u32 {
    3000 + e as u32
}

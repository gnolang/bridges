# Audit 5, round 4 — the three Solana programs as built (bridge/programs, branch `bridge` at da9567d, 2026-10-13)

Read: PLAN.md §6.1–6.6, §5.8, §13; M2-NOTES.md (M2e, M2f, the toolchain facts, the compute table); every file under
gno-light-core/src, sol-util/src, gno-light-client-ism/src, pausable-ism/src, admin-program/src; tests/testlib and
tests/suite (client, pausable, limits, admin); the Go `ParseStage` and `ParseClientState` (bridge/sol/client.go).
References: Hyperlane's sealevel mailbox (`inbox_process`, processor.rs:195-440), agave-precompiles 3.1.12
(`ed25519::verify`, `get_data_slice`), solana-instructions-sysvar 3.0.1 (`load_current_index_checked`,
`store_current_index`), tm2 at the pinned module. No build, no network. Paths below are relative to
`bridge/programs/`.

## Findings

### S-17 · low · `Grow` keeps the ring's entries but not its capacity: a growth over an empty ring leaves a 9-entry ring until two more paid calls
`gno-light-client-ism/src/processor.rs:196-199` (the move waits for `count` entries — the ones the ring holds, zero
after a clear — never for the capacity it had), `state.rs:184-200` (`relocate` keeps `ring_next` below the old
count, so a later capacity growth overwrites live entries before using the free slots), `:150-166` (`push`
increments `ring_count` past the slots written), tests/suite/tests/client.rs:528-580 (the S-09 test observes the
entries and the count, never the capacity).

Scenario: the suite's deploy account (21,216 bytes, a set of 4) holds 256 entries. A set change clears the ring. A
stranger sends `Grow { 81920 }` six times (≈0.6 SOL, theirs, unrecoverable): on the sixth the account is 82,656
bytes, the region moves, and the ring is `(82,656 − 81,984) / 72` = 9 entries — 151 after a seventh call, 256
after an eighth, which nobody pays. Until then a header is forgotten after nine later ones: ring proofs of a
backlog answer `NotInRing` at staging, relayers fall to header proofs (+2 transactions, +36k CU each), and S-10's
record inflation returns at ten headers. When the capacity grows again, the next pushes overwrite slots
`ring_next..8` while 247 slots stand free.

Fix: in `grow`, move only when `HEAD_LEN + wanted + min(RING, Ring::capacity(&head, len)) × RING_ENTRY ≤ new_len`
(the old capacity, not the count); in `push`, while `ring_count < cap` write at `ring_count` and set `ring_next`
to `ring_count + 1`. Test a growth over an empty ring, then over a full 9-entry one.

### S-18 · low · a kind 9 naming a successor that can never inherit pends on its own `SuccessorInit`; S-15's spend covers only the self-case
`admin-program/src/processor.rs:823-897` (nine authority CPIs before the successor's state is read), `:156-160`
(`successor_init`: `NotInherited` for a program with no `predecessor` or one already inherited, `WrongAuthority`
for one naming another predecessor), `:95-122` with `sol-util/src/loader.rs:125-138` (`init` needs the upgrade
authority's signature, which an immutable program no longer has).

Scenario: GovDAO's kind 9 names X, immutable as required. X's `init` named another program as its predecessor (a
copied id); or X was burned before its `init`, so it can never be initialised; or X already inherited from an
earlier transfer. Every `Execute` re-points nine authorities, dies in the CPI, reverts whole, and the decision
pends until a second proposal cancels it — a dead transfer for a week or more, and the one decision a hold waits
for. The pre-vote check of the proposer and `hlwatch` reads X's programdata, not X's state.

Fix: in `Params::Transfer`, take the successor's state PDA first, `State::load` it (owner `program`) and spend
unless `predecessor == *program_id && inherited == 0 && successor.is_none()`; have r/hlgov's builder and
`hlwatch` read the same three fields before the vote.

### S-19 · low · a retired predecessor still opens recovery stages whose record can never land, spending a validators' signing round
`admin-program/src/recovery.rs:186-212` (`init` reads `settled`, never `successor`), `:216-273` (chunks and
signatures accepted on it), `:298-302` (`Retired` only at `record`).

Scenario: kind 9 executed; r/hlgov's recipient switch is a day away; the chain halts, and fourteen days later a
lapse window stands on predecessor and successor alike. `hlrecover`, pointed at the predecessor, stages the body;
five operators sign the digest on hardware (a day of coordination); `RecoveryRecord` answers `Retired`. The
digest binds the predecessor's id, so nothing of it serves the successor and the round is repeated there; the
stage's rent is the submitter's to recover.

Fix: `recovery::init` answers `Retired` once `successor` is set, as `record` does; `hlrecover` refuses a program
whose state shows a successor before it asks for signatures.

## Regression (round 3)

- S-13 fixed: `admin-program/src/processor.rs:517-521` (`StageUpgrade` answers `Retired`), `:587-594`
  (`CloseCancelled` takes any pending decision of either channel once `successor` is set, the buffer back by
  `return_buffer` under the PDA that still owns it), `recovery.rs:298-302` (`RecoveryRecord` answers `Retired`);
  tested admin.rs:1646-1717. Holds — `recovery::init` is the gap S-19 names.
- S-14 fixed: `config.rs:93` (`MAX_PARAMS = 10_240 − 105`), `state.rs:225-228, 260-264` (`Decision::size` counts
  the refund key as `Some`: 105 + the parameters, so `create_pda_account(d.size())` never asks past 10,240),
  `processor.rs:364` (spent at `handle`), `recovery.rs:196-198` (refused at `RecoveryInit`); a kind 11's parameters
  are 46 + L + 40n, so 252 validators at a 9-byte chain id and 250 at 64; tested admin.rs:1728-1777. Holds.
- S-15 fixed: `processor.rs:689-696` (a transfer naming this program is spent before any extra account); tested
  admin.rs:1780-1808. Holds — the other never-inheriting successors of its own scenario are S-18.
- S-16 fixed: `state.rs:59-63, 142-147` (the body's epoch, one up at every `Ring::clear`), `processor.rs:581-586`
  and `:755-761` (the two clears: a set change at the finish, a reset keeping and bumping the epoch),
  `:356-364` (a by-ring `StageInit` copies the entry's height and results hash under the epoch), `:672-687`
  (`Verify` reads the copy while the epoch and the set are the staging's, else the entry must stand), `stage.rs:59-67`
  (48 bytes at the end, zero for updates and header proofs), the Go `sol/client.go:802-834` reading the three
  fields after `LeafFilled` and `:726` the body's last field, its `done()` tolerant of the trailing leaf; tested
  client.rs:583-676 and :788. Holds.

## Checked and held

- The epoch: no third clear exists (`grow` relocates and keeps every entry); within an epoch the set hash, the
  chain id and the origin are constant and the trusted height only rises, so a copied height never stands above
  the trusted one and the `set_hash` test is a second lock on the same door; a stage across a clear falls to
  `Ring::find` on a cleared ring (`NotInRing`), a rotation A→B→A included (two bumps); the saturating bump is
  unreachable; `Stage` accounts exist only by `stage_init`, so `by_ring` cannot be forged; a zero results hash
  copied at the staging is refused at `Verify` (`NoResultsHash`).
- The retired predecessor: `Execute` of any pending kind that touches a shared object dies at that object's owner
  or authority (the loader, the mailbox, the tokens, the pausable ISM, the client's admin) since kind 9 moved
  them all; the two kinds that touch only its own state (unpause of its own flags, kind 10) move nothing anyone
  reads; `CloseCancelled` by anyone, the rent to `d.deliverer` and the buffer to `d.refund_key` only; the
  successor's numbers and accounts are its own, so nothing closed here was executed there.
- The self-transfer: spent before the eighth account; a transfer's nine CPIs and its `SuccessorInit` revert whole
  on any failure, so no authority moves without the hand-over.
- The pausable ISM reads bytes `[0]` and `[8..16]` of the 64-byte head through `read_checked` (seed, owner,
  version 1 pinned); the admin program reads `[16..24]` and `[32..64]` and the body through the client crate,
  whose `Body::deserialize` stops at `ring_epoch` and ignores what follows — an append-only body keeps both
  readers, a reorder would not (§13's fixed layout, kind 4 reserved).
- ed25519 introspection against agave-precompiles 3.1.12: the same `num_signatures`, the same fourteen-byte
  offsets, the same slices; an index naming another instruction is refused (`OtherInstruction`) where the
  precompile would read that instruction; the padding byte checked where the precompile ignores it; a slice past
  the data refused by both; a corrupted signature fails the transaction before any program runs.
- `require_delivery`: `load_current_index_checked` reads the two bytes the runtime writes per top-level
  instruction (sysvar lib.rs:167-190), which no CPI changes, so a wrapped `InboxProcess` or a direct call is
  `NotTheMailbox`; two `InboxProcess` in one transaction are two indices, each checked against its own data;
  `inbox_process_carries` requires the exact length; a recipient's `Handle` can reach the pausable's `Verify`
  only with its own message, which no token's bucket meters.
- The mailbox: the inbox refmut guards reentrancy across the ISM and the recipient; the processed-message PDA is
  checked before `Verify` and created after; the per-ISM process authority never reaches our ISM.
- Account checks, re-initialisation, realloc: every PDA by seed and owner; `init` ×3, `successor_init` and
  `finish_setup` once; a stranger's lamports at any address topped up and assigned; every `resize_account` under
  10 KB per top-level instruction (a stage chunk ≤ 1,232 B, the admin state's growth ≤ 10,040 B for 255
  validators); a decision account ≤ 10,240 B.
- Arithmetic: saturating clock sums and epoch; `(to − from) + 1` checked; `recovery_counter + 1` under
  `overflow-checks`; `len − 9` behind `decision_prefix`; `body_cap8` fits u16 at the 80 KB bound; the u128 bucket;
  `Ring::relocate`'s `cap.max(1)`; a power past i64 refused by `set.check`.
- Compute against M2f's table: nothing new runs on a hot path; a header-proof delivery with a full ring
  (44.6k + 45k + 94–126k) and the eight-dispatch delivery still need the Go side's compute budget above 200k.
- The clock factor reaches every clock (grace, week, fortnight, the 21 days, the bucket's day, the stage closes);
  the admin program's feature does not propagate to its crate dependencies, which carry no clock, and
  `finish_setup` compares the three deployed bytes.
- The real-chain fixtures: 85 precommits over 17 heights, every round 0 (walked in the JSON) — the round-above-0
  blind spot stands, its wire form checked only against the test kit's own encoder.

## Tests' blind spots

No malformed precompile instruction (another index, an outside offset, non-zero padding); no `Verify` through a
wrapping CPI nor two `InboxProcess` in one transaction; the Clock written by hand, never a validators' median; no
real-chain commit at a round above 0; the heap probes `#[ignore]`; no staged ring proof dying across a reset (only
the epoch's value is asserted, client.rs:788); no `Grow` over an empty ring, and no test reads the ring's capacity
(S-17); no kind 9 to an uninitialised, wrongly-initialised or inherited successor (S-18); no recovery stage on a
retired program (S-19); no `Execute` of kinds 2, 3, 5–7, 10–12 on a retired predecessor — the owner checks that
stop them are Hyperlane's and the loader's, never asserted here; the tokens in limits.rs are the noop program.

## Verdict

Round 3's four fixes hold at the lines above; the ring epoch, the retired predecessor, the bounded body and the
spent self-transfer withstood what I tried — no forged header, proof, charge or decision, no number reused, no
hold moved, no proof surviving a clear. Three lows remain, each a liveness cost at an edge of an earlier fix:
S-09 kept the ring's entries but not its capacity, S-15's spend reaches only the self-case, and S-13's `Retired`
stops at `record`. Nothing above low: the programs converge this round.

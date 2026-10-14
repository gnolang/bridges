# M2 — the three Solana programs: what was built, what was chosen where the plan is silent, what is left

Built 2026-10-12 against PLAN.md v25 (§6.1–§6.5, §5.8, §13), brought to v26 the same day (§6.4's anchored
cancel digest and its record, the hold's 21st day, the lapse-recorded recovery decisions' deferral, kind 8 never
the admin program) and to v27 (M2c, the red team's AM series: kind 10 raise-only above a high-water mark, the
replacement's 21st-day floor written once and binding while a hold stands, the salted cancel body and the
self-allocated cancel record, kind 4 reserved with the client's state layout pinned at version 1 — and, found by
M3b's e2e, `HandleAccountMetas` answering the order `handle` reads), and on 2026-10-13 through the audit loop's
round 1 (AUDIT.md; the section "Audit round 1" below: the recorded settlement, the chunked staging, the hold
history, the mint limits, the cancelled store, the successor's inheritance, the setup checks, the loader's real
extend), and on 2026-10-13 through M2j, Jae's design change: the validators' recovery path is gone (the section
"M2j" below), so the sections before it describe a recovery channel that no longer exists — each stays as the
record of what was built and why, and the choices M2j took out are tagged where they stand. Nothing here edits
the plan; every choice below is one the plan leaves open, with the reason. Numbers are from program-test on the
SBF builds (`programs/test.sh`).

## Audit round 1 (2026-10-13): what changed, one commit each on `bridge`

- **S-01 / AN1 / C226 / K1 — the settled set is a recorded fact.** The admin state keeps `settled` (hash, the
  validators with their powers, the from-height); `finish_setup` records the launch set; a permissionless
  `Settle` copies the client's newest settled entry (§6.1's rule, both holds read) into it when that entry is
  newer (a higher from-height), answering `NothingToSettle` otherwise; a kind 11 records the set it installs; the
  recovery path (`RecoveryInit`, `RecoveryVerify`, `RecoveryRecord`) signs against the record alone and never
  reads the client's history. The client never evicts its newest settled-by-clock entry (choice 6). Four forged
  rotations under a hold — the fifth evicting the launch entry from the client a week on — leave the record
  signable and the thief's keys refused (tested).
- **S-02 — chunked staging.** `StageInit` takes the next set's length and hash, `StageNextSet { offset, items }`
  fills it over transactions (25 validators a call), `UpdateFinish` checks the whole set's hash; `Grow { body_cap
  }` takes a bounded target (80 KB); a recovery body is staged by `RecoveryInit { body_len, digest }` and
  `RecoveryChunk { offset, chunk }`. A 40-validator set is announced in two chunks and followed; a 40-validator
  kind 11 is staged in two chunks and executed (tested). The real limits are the heap's, not the transaction's
  any more — measured below under "Toolchain facts".
- **S-03 — a replacement ends a hold only by its own clear**: of a standing guardian flag, leaving fewer than two;
  a watch slot or an idle seat replaced during a dip ends nothing (the dip runs on; unit-tested).
- **AN4 / C228 — the hold history.** The admin state keeps the last eight holds that ended (start, end), written
  by the flag instructions, an unpause of its own flags and a replacement; a replacement's floor under a hold is
  the earliest hold overlapping [its delivery, now] + 21 days, computed at `Execute` (nothing written, no earlier
  attempt needed), binding only while a hold stands. The decision's `floor` field is gone. Holds at days 0–20 and
  20–∞ land a day-1 replacement on day 21 with no `Execute` before day 21 (tested).
- **AN5 — mint limits.** The pausable ISM's `Verify` parses Hyperlane's token message and refuses an amount above
  the token's per-message maximum (`OverMax`) or past its rolling 24-hour bucket (`OverDayCap`): 25,000 a
  message and 100,000 a day for each token at launch (base units of 6 decimals), set by kind 12 `set mint
  limits` (token u8 ‖ max u64 ‖ day cap u64; the 7-day group; the admin program CPIs the owner-only setter).
  The bucket is charged only inside the mailbox's own `InboxProcess` of the message (the Instructions sysvar),
  so a direct `Verify` meters nothing (choice 26). Both refusals, the refill, the setter and the direct call are
  tested through the real mailbox build.
- **AN7 / S-04 — the cancelled store never jams**: a range the pairs closed now cover whole is not stored; a
  channel's ranges are pruned when its pending count (kept exact by every record and close) reaches zero; a full
  store merges the new range with the nearest of its channel rather than refuse (`StoreFull` is gone).
- **AN8 — kind 10's reach**: spent above `high_water + 2^32`; `handle`'s next number is a checked add that
  ignores (never panics).
- **S-05** — `transfer_authority_in` refuses the admin program's own programdata. **S-06** — `finish_setup` takes
  the pausable flags and requires the pausable ISM's guardian list, the client's and the pausable ISM's clock
  factors and the client's origin domain to be the admin program's own. **S-07** — `Outbox::parse` takes the
  tree's offset from the owner tag (an owner-less outbox parses). **C227 / K11** — kind 9's execution writes the
  live origin, sequence number (the high-water mark), recovery counter, seats, watch slots, recorded settled set
  and hold history into the successor by `SuccessorInit`, which only the predecessor's authority PDA may sign;
  the successor's own `init` names its predecessor and takes none of those, and its `Handle` takes no number
  before it inherited. **F1** (the assumptions audit) — the loader's `ExtendProgramChecked` does not exist on
  mainnet, and mainnet's runtime lets no program CPI any extend: the growth step is the loader's own permissionless
  `ExtendProgram`, sent top-level (choice 17).

## M2e — audit round 2 (2026-10-13): the five program findings, one commit each on `bridge-m2e`

Round 2 (AUDIT.md; the findings in audit-r2-programs.md, S-08–S-12, all low) is folded here. One format changes
(S-08), which the Go side follows — sol/client.go's `StageInitArgs` and `DecodeStageInit`, `ParseStage`,
`UpdateTxs` and `ProofTxs`, the `ClientError` names, and the vectors (bridge/tools/vectors, sol/testdata) — and
nothing else changes shape: every other format and error code is byte-identical.

- **S-08 — one round per commit.** tm2's `Commit.ValidateBasic` (block.go:552), which `VerifyCommit` runs first,
  requires every precommit's round to be the commit's (`Commit.Round`, the first present precommit's); the client
  tallied precommits of any rounds. Now `StageInit` carries the commit's round and the stage keeps it: every slot's
  sign bytes are built from it, and a `StagePrecommits` item whose round differs is refused. The core's
  `Commit::round()` reads that round and `check_shape` refuses a mixed-round commit (unit-tested). The change,
  exactly:
  - `StageInit` (the client's variant 1) gains `round: i64` — 8 bytes, little-endian, borsh `i64` — immediately
    after `block_id` and before `n_slots`: `0x01 ‖ kind u8 ‖ header Option<Header> (0x00, or 0x01 ‖ the borsh
    Header) ‖ ring_hash [32] ‖ block_id (hash: u32 LE length ‖ bytes; parts: total u32 LE ‖ hash: u32 LE length ‖
    bytes) ‖ round i64 LE ‖ n_slots u16 LE ‖ message_hash [32] ‖ next_set_len u16 LE ‖ next_set_hash [32] ‖ proof
    Option<SimpleProof> ‖ leaf_len u32 LE`. With 32-byte block and parts hashes `round` starts 76 bytes after
    `ring_hash` ends; for a ring proof (no header, an empty BlockID of 12 zero bytes) it is bytes 47..55 of the
    data and must be 0. A negative round, or a ring proof naming one, is `InvalidInstructionData`.
  - The `Stage` account (`ParseStage` on the Go side) gains the same `round: i64` after `block_id`, before
    `set_hash`.
  - `StagePrecommits` and its items are unchanged (`round i64 ‖ secs i64 ‖ nanos i32` each); an item's round must
    be the stage's, else the new error `RoundMismatch` = `1000 + 36` (custom 1036, after `ChunkOutOfOrder`).
  - The Go side's work: `StageInitArgs` gains `Round int64`, written after `BlockID` and read by
    `DecodeStageInit`; `UpdateTxs` and the header form of `ProofTxs` take the commit's round (the first present
    precommit's, as `Commit.Round`), the ring form writes 0; `ParseStage` reads the stage's; `ClientError`'s names
    gain `"RoundMismatch"` at 36; the four `client_stage_init_*` vectors change (8 zero bytes after the BlockID —
    every fixture commit is round 0), and bridge/tools/vectors's four `ClientIx::StageInit` literals need the
    field before the tool compiles again.
  - Tested: a round-2 commit is followed (the round in every slot's sign bytes); the same signatures staged under
    round 0 verify no slot (`SignatureMismatch`: the sign bytes are the stage's round, not the item's); a mixed
    commit — two precommits of round 0 and two of round 2, each a real signature over its own bytes — is refused
    at the staging (`RoundMismatch`, nothing staged); a negative round is malformed.
- **S-09 — `Grow` keeps the ring.** The body region moves to its new capacity only once the account holds the new
  region *and every entry the ring has* (`HEAD_LEN + wanted + ring_count × 72 ≤ the new length`; M2g's S-17 made
  it the ring's *capacity* — a cleared ring holds no entry, and the rule kept 9 of its 256 slots); until then the
  account grows and the region stays, a later call moving it, and `relocate` keeps the next write where it was. A
  stranger's `Grow { 81920 }` from the deploy's 21,208-byte account is 8 calls to 100,416 bytes: the region moves
  on the 8th with a full ring (the first build moved it on the 6th, keeping 9 of 256 entries), on the 7th with a
  ring of 100; the 80 KB bound stays. Tested with a full ring of 256 and a partial one of 100: every entry found
  after every call, the write position kept, an update landing after. The moving call is 21,700 CU with a full
  ring (the copy), the others 9,000–14,200.
- **S-10 — a header-based `Verify` records only a fresh header.** A header the ring does not hold is recorded once
  and pushed into the ring (its LastResultsHash as the entry's); a header the ring holds — accepted as an update,
  or proven before — is verified but not recorded again, so a stranger's thousand calls of one staged proof add
  nothing to the record. The watch's contract (hlwatch): a header-based proof is a record entry only when its
  header is new to the client; the same header counts again only after the ring evicted it (256 later headers) or
  a set change or reset cleared the ring. `Verify` by header is 34,200 CU (33,800 before: the look-up and the
  push). Tested: a proof of a header the ring holds records nothing; two proofs of a header it does not hold leave
  one record and one ring entry.
- **S-11 — a reset's chain id is checked before the CPI.** `Execute`'s `Params::Reset` spends a decision whose
  chain id is empty or longer than 64 bytes (the client's own bound), as the set and the height already were;
  21,500 CU spent. Tested: a 65-byte and an empty chain id, each recorded, matured and spent, the client and the
  record untouched.
- **S-12 — `Retired` is answered.** Once kind 9 set `successor`, `handle` records nothing and answers `Retired`
  (`3000 + 31`, which existed unused): the message stays deliverable and the relayer parks it, so a decision
  dispatched to the predecessor between the transfer and r/hlgov's recipient switch (§5.3) never takes, dead,
  the number the successor expects next. Tested: a decision to the retired predecessor is refused with `Retired`,
  nothing recorded.

Sizes after M2e (release): factor 1 — client 272,464 B (+1,576 on round 1's build), pausable ISM 164,432
(unchanged), admin program 397,760 (+120); factor 8 (`FACTOR8=1`) — 272,464 · 164,432 · 397,744. Rent at 5,080
lamports/B is 1.38 + 0.84 + 2.02 SOL, as before to the second decimal. Compute: `StageInit`+`StagePrecommits`
36,700 (4 validators), 48,500 (the real chain's 6), 61,700 (40 slots) against round 1's 35,000–47,000 and
61,500; `Verify` by header 34,200; the delivery row below is unchanged by M2e (one `is_some()` in `handle`); the
successor test's block of three dispatches delivers at 149,200 (the leaf's events, as the eight-dispatch block's
227,500). The suite is 28 program-test tests (three new: `a_commit_carries_one_round`,
`a_strangers_grow_to_the_bound_keeps_every_ring_entry`, `a_reset_whose_chain_id_the_client_refuses_is_spent`) and
the core's 12 unit tests (one new), all green at both factors.

## M2f — audit round 3 (2026-10-13): the four program findings, one commit each on `bridge-m2f`

Round 3 (AUDIT.md; the findings in audit-r3-programs.md, S-13–S-16, all low) is folded here. Two account layouts
change, both the client's and both by appending (S-16): the `Stage` account gains 48 bytes at its end and the
client `Body` 8 bytes at its end. Every instruction format, every PDA, every error code and the client state's
64-byte head stay byte-identical, the head's version byte at 1 (the pausable ISM and the admin program read the
head at fixed offsets and are unchanged). The Go side follows: sol/client.go's `ParseStage` (and whatever takes
the leaf after the stage's borsh bytes, which move by 48), `ParseClientState` and `ClientBody.Size` (hence
`BodyCapNeeded`); the vectors are untouched, no instruction having changed.

- **S-13 — a retired predecessor closes what pends there.** Once kind 9 set `successor`, `CloseCancelled` closes
  any pending decision of either channel — the rent to its deliverer, a staged kind-8 buffer back to its refund
  key (the retired program's PDA still signs the loader's `Close`) — not only a cancelled one; `StageUpgrade` and
  `RecoveryRecord` answer `Retired` (3031), so nothing new starts pending there (a stage stays its submitter's to
  close). Before, a kind 8 staged under the predecessor and executed after the kind 9 failed every `Execute` at
  the loader (`IncorrectAuthority`: the successor's PDA holds the upgrade authority) with its hash matching, so
  `return_buffer` never ran, no mailbox cancel could reach the retired program (`Retired`) and a recovery cancel
  needed a 14-day window: the buffer (≈0.84 SOL of rent for a real ELF) and every pending decision's rent were
  stranded for good, and a writer could even stage a new buffer there. Tested: a kind 8 staged, a second
  unstaged, a fee and a lapse-recorded kind 10 all pending when the kind 9 executes; the staged kind 8's
  `Execute` then fails at the loader exactly so; `StageUpgrade` of the unstaged one and a fresh `RecoveryRecord`
  under a new lapse answer `Retired`; `CloseCancelled` closes all four (the buffer's lamports to the writer, the
  rents to the deliverers, `pending` back to `[0, 0]`) — while on a live program it still refuses an uncancelled
  one (`WrongDecision`).
- **S-14 — `MAX_PARAMS` is what one decision account holds.** `10_240 − 105` = 10,135 bytes of parameters: a
  decision account is 105 bytes beside them (`Decision::size`, the refund key counted as `Some`), and one
  instruction creates or grows an account by at most 10,240 bytes (`MAX_PERMITTED_DATA_INCREASE`; `InvalidRealloc`
  past it, a fresh account included) — the old 11,000 let a body of 10,136–11,000 bytes through `handle`'s spend
  check to die in `create_pda_account`, its number untaken and every later number `OutOfOrder` until a recovery
  kind 10. Now such a body is spent at `handle` and refused at `RecoveryInit` (`WrongDecision`). The bounds as
  they really stand: by the account a kind 11 carries 252 validators with a 9-byte chain id, 250 with a 64-byte
  one (the old comment's and §6.1's 255 was never true); the heap bounds a kind 11 far lower (50 validators
  through any history, 60 recorded and executed, 100 recorded, 200 dying at `RecoveryRecord` — "Toolchain facts"
  below); and the mailbox channel never reaches the program's bound at all: Hyperlane's mailbox (the mainnet
  build this deployment uses) copies the message through its own 32 KB heap — the instruction, the ISM's
  `Verify` data, the recipient's `Handle` data — and dies on it before any ISM or recipient runs; measured under
  program-test, a body of 2,250 parameter bytes is delivered and one of 2,500 is not, and the real chain's
  1,232-byte packet bounds a mailbox decision lower still: 538 bytes of body (529 of parameters) since round 4,
  and r/hlgov refuses above 512 (the assumptions audit's F25 corrected this line's "near 600"). So a mailbox decision
  above what the mailbox carries is the stuck sequence S-14 names in a form no program of ours can cure (the
  number is never taken, the message never deliverable): r/hlgov's builder must never author one, and `handle`'s
  check is defence in depth above it. Tested: the overhead is 105 and the constant `MAX_GROWTH − 105`; a
  10,136-byte body is refused at `RecoveryInit`, a 10,135-byte one staged; on the mailbox channel a 10,136-byte
  and a 2,500-byte body die in the mailbox (`ProgramFailedToComplete`, no code of ours) with the number untaken,
  and a 2,250-byte one is carried, recorded whole and spent at `Execute` (a kind 7 it is not).
- **S-15 — a transfer to the admin program itself is spent.** `Execute`'s `Params::Transfer` naming this program
  is spent before any extra account is read (as kind 8's self-upgrade is): its own programdata has no authority
  (burned in the setup step), so the immutability check alone passed it, and the execution re-pointed every
  authority to this same PDA and then died on its own `SuccessorInit` (`NotInherited`: `predecessor` zero),
  pending for good until GovDAO's cancel a week on. r/hlgov's builder and hlwatch should refuse it before the
  vote and read the successor's `predecessor` and `inherited` (theirs to do, not this lane's). Tested: recorded,
  `NotMature`, spent on its 7th day with no account beyond the fixed seven, every upgrade authority, owner and
  the client's admin untouched, no `successor`, and the program taking the next decision.
- **S-16 — a staged ring proof survives the ring's churn.** S-10's push of every fresh proof header into the ring
  let a stranger's 257 header proofs of old headers (public headers, commits and messages, ≈0.07 SOL of
  returnable rent each) turn the ring over in a minute, so a relayer's ring proof staged a minute before its
  `InboxProcess` answered `NotInRing` and deliveries fell back to header proofs or parked. Now a by-ring
  `StageInit` copies the entry's height and `LastResultsHash` into the stage under the body's **ring epoch** — a
  counter the body keeps, one up at every `Ring::clear` (a set change at `UpdateFinish`, a reset) — and a by-ring
  `Verify` reads the copy when `st.ring_epoch == body.ring_epoch` and `st.set_hash == body.set_hash`, needing no
  entry (otherwise the entry must stand, as before, which an old header never does under a new set:
  `NotInRing`). Eviction costs a staged proof nothing; a set change or a reset still voids every ring proof staged
  before it. The formats, exactly:
  - the client `Body` (borsh, after the 64-byte head) gains `ring_epoch: u64` — 8 bytes, little-endian — at its
    end, after `running_hash`; everything before it is byte-identical. The body's size, so `wanted_cap` and the
    body region, grow by 8 (the suite's deploy account, a set of 4, is 21,216 bytes, from 21,208). The Go side's
    `ParseClientState` must read the field (its `done()` fails until it does) and `ClientBody.Size` must count it
    (`BodyCapNeeded` follows). The admin program reads the body through the client crate and follows by build.
  - the `Stage` account — every stage: updates, header proofs and ring proofs alike — gains 48 bytes at its end,
    after `leaf_filled`: `ring_epoch u64 LE ‖ ring_height i64 LE ‖ ring_results [u8; 32]`, set only for a ring
    proof (the entry's height and LastResultsHash, the body's epoch), zero in an update and in a header proof. A
    proof's leaf follows the borsh bytes as before, so its offset (`Stage::size()`) moves by 48. The Go side's
    `ParseStage` must read the three fields (its `done()` fails until it does), and anything taking the leaf
    after the stage's borsh bytes moves with it.
  - `StageInit` and every other instruction are unchanged, the four `client_stage_init_*` vectors with them; no
    error code moves. A by-ring `Verify` no longer scans the ring: 28,900 CU (29,100 before), the same after
    the churn; a by-header `Verify` still looks its header up (S-10) and costs 34,300 with a near-empty ring,
    44,600 with a full one (the churn's own figure).
  Tested: a ring proof of 102 staged (its copy: height 102, the root, epoch 0), the client jumped to 360, a
  stranger proving 257 old headers 103–359 one after another — each recorded once and pushed, the ring full, 101,
  102 and 360 all evicted — and the staged proof delivering after it (nothing recorded, nothing pushed); a ring
  proof of 359 staged before a set change answering `NotInRing` after it (the ring cleared, the epoch at 1); a
  reset moving the epoch (in the reset test).
- The stale "10 KB a step" comments (processor.rs's `NeedsExtend`, loader.rs's `EXTEND_STEP`, instruction.rs's
  note on the old instruction 12, the suite's) now say what M3f's F7 does: one `ExtendProgram` of the whole
  excess, at or above the loader's 10,240-byte minimum, which the loader takes once a block; the suite still sends
  10,240-byte steps to show that rule (choice 17 below corrected to match).

Sizes after M2f (release): factor 1 — client 273,840 B (+1,376 on M2e's 272,464), pausable ISM 164,432
(unchanged), admin program 398,752 (+992 on 397,760); factor 8 (`FACTOR8=1`) — 273,792 · 164,432 · 398,720.
Rent at 5,080 lamports/B is 1.39 + 0.84 + 2.03 SOL (4.26 together). Compute (factor 1, the maximum per labelled
transaction): `Verify` by ring 28,900 (29,100 before: no ring scan; the same after the churn) and a ring proof's
`StageInit` 17,500 (17,200: the 48-byte copy); `CloseCancelled` on a retired program 11,500–14,500, 18,900 with a
staged kind 8's buffer returned; a self-transfer spent 18,200; `RecoveryInit` of a 10,135-byte body 12,800; a
2,250-byte mailbox decision delivered at 319,700 — the mailbox's and `handle`'s copies of the body; a compute
budget well above the 200k default, as the eight-dispatch delivery's 227,500 already needs — and spent at
18,200; a header proof's `Verify` with a full ring 44,600 (S-10's look-up over 256 entries; 34,300 with a
near-empty one). Nothing else moved: kind 9 163,900–165,100, the kind 8 upgrade 167,700 (a bigger ELF
hashed), `stage_upgrade` 17,800. The suite is 32 program-test tests
(four new: `a_retired_predecessor_closes_what_pends_there`, `a_decision_body_is_bounded_by_what_one_account_holds`,
`a_transfer_to_the_admin_program_itself_is_spent`, `a_staged_ring_proof_survives_the_rings_churn`) and the core's
12 unit tests, all green at both factors.

## M2g — audit round 4 (2026-10-13): the three program findings, one commit each on `bridge-m2g`

Round 4 (AUDIT.md; the findings in audit-r4-programs.md, S-17–S-19, all low, each a liveness cost at the edge of
an earlier fix) is folded here. Nothing changes shape: every instruction format, account order, PDA, account
layout and error code is byte-identical — S-19 answers `Retired` (3031) on one more path, S-18 reads an account
kind 9's `Execute` already took, at its place — and the Go side follows nothing new. One rule of the client's head
changes meaning without changing a byte: while the ring has free slots (`ring_count` below its capacity) a push
lands at slot `ring_count`, and `ring_next` names the next overwrite only once the ring is full; the Go reader
(`ParseClientState`) walks `0..ring_count`, which is exactly where the entries are, and never reads `ring_next`.

- **S-17 — `Grow` keeps the ring's capacity, and a grown ring fills its free slots first.** S-09's move waited for
  the entries the ring held, which after a clear (a set change, a reset) is none: a stranger's `Grow { 81920 }`
  over the deploy's cleared 256-slot ring (21,216 bytes) moved the region on the 6th call into an account with
  room for 9 slots, and until a 7th call (151 slots) and an 8th (256) — ≈0.2 SOL more of a stranger's rent,
  which nobody pays — a header was forgotten after nine later ones: ring proofs of a backlog `NotInRing` at the
  staging, relayers falling to header proofs (+2 transactions, +36k CU each), S-10's record inflation back at
  ten headers. And `relocate` kept `ring_next` below the old count, so once the capacity grew back the next
  pushes overwrote slots `ring_next..8` while 247 stood free. Now `grow` moves the region only once the account
  holds the new region *and a ring of the capacity it had before the call* — `HEAD_LEN + wanted + min(256, the old
  capacity) × 72 ≤ the new length` — so over the deploy account the move is the 8th call, at the full 100,416
  bytes, every call before it growing the account with the region in place (a multi-step growth reads the
  capacity it had from the stale region, which counts the grown bytes as slots, capped at 256: the move waits
  for the full length, which the growth reaches on its last call anyway, so it is never earlier than S-09's and
  never a slot short); and `Ring::push` writes at `ring_count` while `ring_count < capacity`, so a ring of 9
  entries whose capacity grew to 151 takes the next header at slot 9, not over its oldest. The updater's
  `ensureBodyCap` loops until the region is at its need (12 calls at most; the bound is 8 from the deploy) and
  the stager retries on `NeedsGrow`, so neither counts calls. Tested: a stranger's `Grow` to the bound over an
  empty ring keeps 256 slots after every call, the region moving on the 8th, an update landing at slot 0 (the
  S-09 cases, 256 and 100 entries, now assert the capacity too: the ring of 100 moves on the 8th call, not the
  7th); a full 9-slot ring — the shape the S-09 build left, written into the bank with its next overwrite at 3 —
  grown to 151 slots takes two headers at slots 9 and 10 with its nine entries kept, the 8th call completing
  the ring at 256 with everything found. `Grow`'s compute: a call that grows the account with the region in place
  9,100–9,500; the call that moves the region 21,700 with a full ring (as M2e measured) and 14,300 with a ring of
  100 (on the 8th call now, the 7th before); a call growing the ring behind a region already at its bound 9,300
  — a growth to the bound is the same 8 calls as before, the move only later in them.
- **S-18 — a kind 9 to a successor that can never inherit is spent.** S-15's spend reached the self-case alone.
  An immutable program whose state can never take the hand-over — never initialised (its `init` needs the
  upgrade authority's signature, which a program burned before its `init` no longer has), initialised naming
  another predecessor (a copied id), inherited from an earlier transfer already, or retired itself — passed the
  immutability check, the only thing `Execute` read of it, and the execution re-pointed the five upgrade
  authorities, the mailbox's and the tokens' owners, the pausable ISM's owner and the client's admin, then died
  on the successor's own `SuccessorInit` (`NotInherited`, `WrongAuthority`): the transaction reverted whole and
  the decision pended, dying again at every `Execute`, until a second proposal cancelled it a week or more on —
  a dead transfer, and the one decision a hold waits for. Now `Execute` reads the successor's state PDA — the
  account it already took after the client program (the Go builder's order stands; the program finds it by its
  place, `SUCCESSOR_STATE_AT`, without consuming the accounts the CPIs read in order) — before any CPI: the
  account must be at the PDA's address (any other is `WrongAccount`, an error, so an executor's choice of
  accounts can spend nothing), and the decision is spent unless the account is the successor's own (its owner),
  parses as a `State`, names this program as `predecessor`, has `inherited` 0 and no `successor` — the three
  fields `successor_init` and `handle` require, which hlwatch's pre-vote check reads since 6448cc1 and r/hlgov's
  builder should read before the vote (the realm lane's). A transfer to a mutable successor is spent as before,
  first. Spent at 26,300–31,700 CU with nothing moved (26,300 with no state, 27,200 naming another predecessor,
  28,700 inherited, 31,700 retired; a mutable successor's spend is 25,500 as before); the hand-over itself is
  169,000–170,500, from M2f's 163,900–165,100 — the ≈5k of parsing the successor's 6.8 KB state before the
  CPIs, in a transaction that runs once in a program's life. Tested: a kind 9 to
  an immutable program never initialised (its init refused, `WrongAuthority`), to one initialised naming another
  predecessor and burned, to the successor with `inherited` doctored to 1 and with a `successor` of its own —
  each spent on its 7th day with every upgrade authority, owner and the client's admin untouched and no
  `successor` set — then the successor as its init left it taking the hand-over (`inherited` 1).
- **S-19 — `RecoveryInit` answers `Retired`.** S-13 stopped `RecoveryRecord` on a retired predecessor;
  `RecoveryInit` still opened a stage there, `RecoveryChunk` and `RecoveryVerify` filled and signed it, and only
  the record refused — so `hlrecover` pointed at the predecessor (the chain halted a day before r/hlgov's
  recipient switch, and fourteen days on a lapse window stands on predecessor and successor alike) staged a body
  and had five operators sign the digest on hardware, a day of coordination, for a `Retired` at the record; the
  digest binds the predecessor's id, so nothing of the round served the successor, where it was repeated, and
  the stage's rent was the submitter's to recover. Now `RecoveryInit` answers `Retired` (3031, as `record` does)
  once `successor` is set, before any rent or signature. A stage opened before the retirement is still refused
  at its record and closed by its submitter (`RecoveryClose` reads no state); `RecoveryChunk` reads none either
  (its accounts — the submitter, the stage, the system program — are unchanged) and `RecoveryVerify` is left
  alone: a stage opened while the program was live is its submitter's to close. `hlrecover` should refuse a
  program whose state shows a successor before it asks for signatures (the Go lane's). Tested: a stage opened
  while live (a kind 10 for counter 1, whole and signed by three), the kind 9 executed, fourteen days on
  `RecoveryInit` answering `Retired` with no stage account made, the earlier stage refused at its record
  (`Retired`) and closed by its submitter with the rent back, the counter at 0.

Sizes after M2g (release): factor 1 — client 273,768 B (−72 on M2f's 273,840: the push's slot choice and the
capacity rule), pausable ISM 164,432 (unchanged), admin program 399,632 (+880 on 398,752: the successor's state
read, the retirement check at the staging); factor 8 (`FACTOR8=1`) — 273,720 · 164,432 · 399,600. Rent at 5,080
lamports/B is 1.39 + 0.84 + 2.03 SOL (4.26 together), as before to the second decimal. Compute (factor 1, the
maximum per labelled transaction): `Grow` to the bound 9,100–9,500 a call that grows the account in place,
21,700 the call that moves a full ring and 14,300 a ring of 100 (the 8th call now), 9,300 a call growing the ring
behind a region at its bound; a kind 9 spent for its successor's state 26,300–31,700, the hand-over itself
169,000–170,500 (+≈5k on M2f: the successor's state parsed first); `RecoveryInit` 11,300 and `UpdateFinish`
13,300 (4 validators), both unchanged — the push's slot choice and the retirement check cost nothing
measurable. The suite is 36
program-test tests (four new: `a_strangers_grow_over_an_empty_ring_keeps_the_rings_capacity`,
`a_ring_grown_past_its_entries_fills_its_free_slots_first`, `a_transfer_to_a_successor_that_can_never_inherit_is_spent`,
`a_recovery_stage_is_refused_on_a_retired_program`) and the core's 12 unit tests, all green at both factors.

## M2h — audit round 5 (2026-10-13): the two program findings, one commit each on `bridge-m2h`

Round 5 (AUDIT.md; the findings in audit-r5-programs.md, S-20 medium, S-21 low) is folded here. Nothing changes
shape: every instruction format, account order, PDA, account layout and error code is byte-identical — S-20 answers
`NotInherited` (3039, which `Handle` and `SuccessorInit` answered already) on four more paths — and the Go side
follows nothing new; S-21 writes down, and pins in the suite, what the Go side and any successor build are already
built against. The assumptions audit's F25 corrected one line of M2f's S-14 note above (the real chain's packet
bounds a mailbox decision at 538 bytes of body, 529 of parameters; r/hlgov refuses above 512; "near 600" was wrong).

- **S-20 — a successor runs nothing of its own before it inherited.** Until kind 9's hand-over a successor's
  `Handle` alone answered `NotInherited`; its `Settle`, its recovery path (`RecoveryInit`, `RecoveryChunk`,
  `RecoveryVerify`, `RecoveryRecord`) and its `Execute` ran as on any program whose state loads — outside the
  predecessor's hold, since a successor's seats are zero keys until the hand-over writes the live ones, so no hold
  can ever stand on it. The scenario: GovDAO prepares successor S (initialised naming P, burned); a thief with the
  current set's keys rotates the client to his own keys; the guardians hold P (the quarantine already pausing
  deliveries, not the pausable ISM) and a week on his set settles — blocked on P by its hold, not on S, where
  anyone's `Settle` records it (S's record was empty and the client's newest settled entry is his); he starves the
  client 14 days, stages a kind 9 (S → his program) on S, three of his keys sign, `RecoveryRecord` lands at
  counter 1 under the lapse; when the holders lift and P's kind 9 executes, his `Execute(1, 1)` on S — inherited
  now, every authority its PDA's — moves every authority to his program, in the same transaction if he executes.
  With honest keys the same path leaves a decision at a number the inherited counter reuses, and the validators'
  first recovery on S dies at `record` (`AccountAlreadyInitialized`) after a signing round; §6.5 promises a
  successor "with no flags, no hold and no pending decisions". Now `processor::awaits_inheritance` (`predecessor
  ≠ 0 && inherited == 0`, the test `handle` and `successor_init` already made) refuses with `NotInherited`:
  `settle` (its record is its predecessor's, written at the hand-over, which overwrites whatever stood),
  `recovery::init` (before any rent or signature, as S-19's `Retired` does), `recovery::record` (the write itself:
  the decision, the counter, the pending count) and `execute` (right after the state loads, before the authority
  or a decision is read: nothing can pend there, nothing runs with its authority). The rest, checked and left:
  `RecoveryChunk` reads no state (its accounts are the submitter, the stage and the system program — a check
  would change its format) and appends only to a stage `init` must have opened; `RecoveryVerify` writes a stage's
  bits alone, and no stage exists; `RecoveryClose` closes a stage and reads no state; `SetFlag`/`ClearFlag` write
  what the hand-over overwrites wholesale (`guardians`, `holds`) and are dead until then anyway, no signer holding
  a zero key (`UnknownKey`); `StageUpgrade` and `CloseCancelled` need a decision (`WrongDecision`);
  `TransferAuthorityIn` moves a program's authority at that authority's own signature and writes nothing here;
  `FinishSetup` cannot flip before the hand-over (every authority is still P's PDA: `SetupIncomplete`) and after
  it is the cosmetic step round 5 noted; `Init` and `SuccessorInit` stay. So the entries an un-inherited successor
  refuses are exactly `Handle`, `Execute`, `Settle`, `RecoveryInit` and `RecoveryRecord`. hlwatch's successor
  check should list S's pending decisions and recovery counter before the vote, and `hlrecover init` refuse a
  program whose state shows `predecessor` set and `inherited` 0 (the Go lane's). Tested
  (`a_successor_runs_nothing_of_its_own_before_it_inherited`): on the successor as its init left it, `Settle`
  with the launch entry settled in the client refused and nothing recorded; the launch record doctored into its
  state (what a `Settle` wrote there before) and a lapse standing, `RecoveryInit` of a kind 9 to a stranger's
  program refused with nothing staged; that stage written into the bank whole and signed by three of the record's
  four, `RecoveryRecord` refused with the counter at 0 and no decision; `Execute(1, 1)` refused before any
  decision is looked for; the stage closed by its submitter; then the hand-over, after which `Settle` records a
  set change the client followed (once; `NothingToSettle` again) and a kind 10 staged on the successor, signed by
  the record's set, is recorded under a lapse at the inherited counter's next number and executed with the
  successor's authority. With the helper disabled the test fails at its first step (the `Settle` lands, 17,520
  CU): the test is live.
- **S-21 — the hand-over's wire contract, written down and pinned.** Kind 9's `Execute` parses the *successor's*
  state with the predecessor's crate's `State` (`can_inherit`: `predecessor`, `inherited`, `successor`, read before
  any authority moves, S-18) and encodes the successor's `SuccessorInit` with the predecessor's crate's
  `Instruction`; nothing froze either for a successor of another build (§13 pins the client's state layout alone,
  kind 4), so a successor that inserted a field before `inherited`, reordered its instruction enum or widened
  `Hold` would be spent by the peek with nothing moved (an inserted field moves the two it reads) or — an encoding
  change the peek cannot see — re-point nine authorities and die at its `SuccessorInit`, the transaction
  reverting whole and the decision pending until a cancel (S-18's dead transfer again, a week or more on). The
  contract, now in comments at `State` (state.rs) and `SuccessorInit` (instruction.rs) and here. **A successor
  keeps `State`'s borsh layout append-only:** `version u8 ‖ clock_factor u8 ‖ setup_done u8 ‖ origin_domain u32 ‖
  last_seq u64 ‖ recovery_counter u64 ‖ guardians (keys 6 × 32 ‖ flags 6 × Option<Flag> ‖ hold_since Option<Flag>
  ‖ dip_since Option<Flag>; a `Flag` is slot u64 ‖ time i64, an Option 0 or 1 ‖ the value) ‖ successor
  Option<Pubkey> ‖ authority_bump u8 ‖ cancelled (u32 count ‖ channel u8 ‖ from u64 ‖ to u64 each) ‖ settled
  (set_hash 32 ‖ u32 count ‖ (pubkey 32 ‖ power i64) each ‖ from_height i64) ‖ holds (u32 count ‖ start i64 ‖ end
  i64 each) ‖ pending 2 × u32 ‖ predecessor 32 ‖ inherited u8`, little-endian — every field at its place and type,
  `predecessor` and `inherited` the last 33 bytes whatever the vectors hold, any new field after `inherited`
  (this build's parse reads nothing past it, as it reads nothing of the account's zero reserve, and neither does
  the Go side's `ParseAdminState`); the head with every option present is 385 bytes and the account at init 6,814.
  **And `SuccessorInit` byte-identical:** variant 14 — `0x0e ‖ origin_domain u32 ‖ last_seq u64 ‖
  recovery_counter u64 ‖ seats 4 × 32 ‖ watch 2 × 32 ‖ settled ‖ holds`, with `Settled` and `Hold` as above — the
  fourteen variants before it (0–13) in their places, a new instruction appended (variant 0's `Init { seats,
  watch, origin_domain, predecessor }` is what the Go side's `SuccessorInitIx` sends before the burn). The version
  byte is the successor's own to judge (its `load_state`); the peek reads the layout. A successor of another build
  runs through §6.4's devnet drill with the real build before its vote, and hlwatch should check the successor's
  state against this layout — the head's shape and the two fields at the end — before the vote (the Go lane's).
  Tested (`the_hand_overs_wire_contract_is_frozen`, a plain unit test in the suite crate, no bank): a `State` with
  every field at a distinctive value serialized and compared byte by byte with the layout spelled out by hand (543
  bytes: a 321-byte head with three flags, a hold and a successor present), `predecessor` at len − 33 and
  `inherited` at len − 1, the round trip, the same bytes parsed with 100 bytes appended (append-only),
  `init_size` as the sum of its parts and 6,814; `SuccessorInit`'s and `Init`'s encodings against their bytes
  spelled out. A reorder, an insertion before `inherited` or a widened field fails the suite.

Sizes after M2h (release): factor 1 — client 273,768 B (unchanged), pausable ISM 164,432 (unchanged), admin
program 400,280 (+648 on M2g's 399,632: the inheritance check on four paths); factor 8 (`FACTOR8=1`) — 273,720 ·
164,432 · 400,248. Rent at 5,080 lamports/B is 1.39 + 0.84 + 2.03 SOL (4.26 together), as before to the second
decimal. Compute (factor 1): nothing moved — the check is a 33-byte compare right after the state loads: `settle`
18,300, `RecoveryInit` 11,300–21,800, `RecoveryRecord` 20,400–24,200, `Execute` of a kind 10 16,600–20,800, the
kind 9 hand-over 169,100–170,600 (M2g's 169,000–170,500), all as before; a successor's own path after the hand-over
measures as the launch program's (`settle` 18,300, the staging 15,800 + 4,900, `recovery_verify` ×3 9,400,
`recovery_record` 23,600, `Execute` of a kind 10 19,500); a refusal is not in the report (program-test records the
compute of transactions that land). The suite is 38 program-test tests (two new:
`a_successor_runs_nothing_of_its_own_before_it_inherited`, `the_hand_overs_wire_contract_is_frozen`) and the
core's 12 unit tests, all green at both factors.

## M2i — audit round 6 (2026-10-13): the three program findings, one commit each on `bridge-m2i`

Round 6 (AUDIT.md; the findings in audit-r6-programs.md: S-22 medium-low, S-23 medium-low, S-25 low — S-24, the
hold-carried kind 9 under a recovery window, waits on Jae's decision about the recovery path and is not here; the
recovery path's code is as M2h left it) is folded here. No instruction encoding, PDA, account layout or error
code changes, and no account list changes its order; **one account changes its flag: kind 9's `Execute` passes the
successor's authority PDA writable** (S-25 below), which the Go builder follows. The sizes, the compute and the Go
lane's list are at the end.

- **S-22 — `Execute` reads the client's head for a recovery decision alone.** `execute` read the client's state
  head (`client_head::read_checked`, pinned at layout version 1) before the dispatch, for every kind of either
  channel, though only a recovery decision's `window_stands` needs it. So §13's kind-4 route run in the wrong
  order — GovDAO's kind 8 that upgrades the client to a new state layout landing before the kind 9 to the
  successor pair built to read it, the upgrade's migration writing the new version byte into the head — bricked
  every decision: the pausable ISM refusing deliveries is the designed fail-closed, but the admin program's
  `Execute` of the corrective kind 8 (a fix build that writes version 1 back; a mailbox decision needing no
  client read) and of a kind 9 to the new pair died at the version check too, every `RecoveryRecord` with them,
  the decisions pending for good and only cancels landing: a liveness end with no exit but a new deployment (§1).
  Now the head is read under `is_recovery` alone and only `window_stands` sees it. A mailbox-channel decision
  never reads the client (kinds 9 and 11 pass account 5 on to the client's own `SetAdmin` and `Reset`, which
  check it themselves), and the recovery path — `record` and `settle` read the body for the digest's chain id —
  keeps failing closed on an unknown layout until the cure lands, as the pausable ISM does. A side effect: every
  mailbox `Execute` is ≈3,100 CU cheaper, the client's PDA derivation gone from its path (measured on the spent
  kind 9 cases, the same tests before and after: 26,279 → 23,182, 27,202 → 24,099, 28,711 → 25,610, 31,733 →
  28,635). Tested (`a_mailbox_decision_never_reads_the_clients_head`): a kind 8 for the client from a buffer of
  the client's own build and a kind 7 delivered, the buffer staged, while the head says 1; the head's version
  byte doctored to 2 in the bank; the kind 7 executes (the fee moves) and the kind 8 executes (the client's
  programdata holds the buffer's ELF, the buffer spilled to the writer); a kind 10 staged and signed under a
  lapse is refused at `RecoveryRecord` with `InvalidAccountData`, the counter at 0 and nothing recorded; the head
  restored (what the fix build's migration writes), the same body records at counter 1. With the fix disabled the
  test fails at the kind 7: `invalid account data for instruction`.
- **S-23 — `can_inherit` requires the successor's compiled clock factor to be this build's.** Kind 9 handed
  everything to a successor whose `clock_factor` (byte 1 of its state, written by its own `init` from its own
  build) the peek parsed and never compared; the one guard against a devnet-factor build beside mainnet ones,
  `finish_setup`'s S-06 check of the three factors, is reached by a successor only after the hand-over, and
  nothing requires it of a successor at all. So the drill's `FACTOR8=1` build deployed, initialised and burned
  on mainnet by mistake, its hash matching what the proposer built, passed hlwatch ("immutable … nothing
  pending") and the transfer, and from the hand-over on the week matured in 21 hours, a replacement's 21-day
  floor in 63, a hold's window and a lapse's in 42, the grace in 3 — and `settle`, judging the pausable ISM's
  24-hour hold with the admin program's grace, recorded 3 hours into a dip a set that ISM still held against.
  Now `can_inherit` also requires `s.clock_factor == CLOCK_FACTOR as u8` — this program's, proved equal to the
  client's and the pausable ISM's at its own `finish_setup` — and such a transfer is spent like the other
  cannot-inherit cases, nothing moved, no `successor` set; a successor of the right factor still proves the
  three equal at its own `finish_setup` after the hand-over. Tested (the fifth case of
  `a_transfer_to_a_successor_that_can_never_inherit_is_spent`): the successor's `clock_factor` doctored to 8 in
  the bank (to 1 under `FACTOR8=1`, the mirror), the kind 9 spent in 27,117 CU with every authority and owner
  untouched and the successor's state byte-identical to the doctored one (each of the five cases checks that
  now); restored, the hand-over lands. With the fix disabled the hand-over goes through: the test fails at "not
  retired".
- **S-25 — the hand-over sweeps the predecessor PDA's lamports to the successor's PDA.** The authority PDA is
  system-owned and data-less, funded by `hlsol setup` with ≈0.01 SOL for the rent Hyperlane's
  `EnrollRemoteRouters` draws from the owner when a token account grows (17b: ≈0.0005 SOL an enrolment). A
  successor's PDA started with nothing, nothing in the tools funded it, and no instruction of the admin program
  ever moved a PDA's lamports; so the first kind 6 on a successor died in Hyperlane's realloc top-up (the
  system program's `0x1`, insufficient lamports) and pended until a hand sent lamports — against §6.5's "no
  decision ever waits on the operator's hand" — while the predecessor's float stayed on a retired program's PDA
  for good. Now kind 9's `Execute`, after the successor's `SuccessorInit`, sends every lamport this program's PDA
  holds to the successor's PDA by a system `transfer` the PDA signs (`invoke_signed`; nothing sent at 0), so the
  float goes with the authorities and the predecessor's PDA ends at 0 (an existing address, data-less; a later
  `CloseCancelled` or `return_buffer` signs with it as before, a signature needing no lamports). **The round's one
  format change: the successor's authority PDA — the fourth of kind 9's extra accounts, after the loader — is
  passed writable**; kind 9's order is as it was and no other instruction changes. The Go builder's `ExtraFor`
  (bridge/sol/admin.go, `case KindTransfer`) makes it `w(succAuth)` in place of `r(succAuth)`; a kind 9 sent with
  the old list fails at the sweep's CPI (a writable privilege the instruction did not grant), the transaction
  reverting whole and the decision pending for a retry with the right list, nothing spent. Tested
  (`the_hand_over_sweeps_the_predecessors_float_to_the_successors_pda`): the launch PDA at 10,000,000 lamports
  and the successor's at 0; the hand-over; a kind 6 delivered to the successor and executed there (44,483 CU),
  the token account growing with the successor's PDA paying exactly the rent difference; the successor's PDA held
  the whole float before that and the predecessor's 0. With the fix disabled the kind 6 dies with `Custom(1)`
  from the system program.

For the Go lane (M3j), what follows from these three:
- the kind-9 builder: `ExtraFor`'s `case KindTransfer` passes the successor's authority PDA writable
  (`w(succAuth)`, S-25) — the one list that changes; `kinds::describe(9)` and the `Execute` doc in instruction.rs
  say so;
- `hlsol successor-init` funds the successor's authority PDA as `setup` funds the launch one (`-authority-fund`,
  10,000,000 lamports by default, 17b), so a successor whose predecessor's float was spent down still has one,
  and prints the balance; `hlwatch`'s `successorCheck` prints the successor's PDA balance beside its verdict
  (S-25);
- `successorCheck` prints the successor's `ClockFactor` (byte 1 of its state, `ParseAdminState`) beside the
  client's head byte and the configured admin program's, and alarms a difference — the program spends such a
  kind 9, and the vote should never reach it (S-23);
- `hlwatch`'s kind-8 pre-vote check for the client program (`upgradeCheck`) says that the layout contract is the
  build's to keep (§13, kind 4: the head's version byte 1 for the deployment's life; a new layout is a successor
  pair by kind 9 *first*) and prints the buffer's ELF hash beside the reproducible fix build's (S-22); the
  monitors and `hlrecover` read a `RecoveryRecord` or `Settle` failing with `InvalidAccountData` as the layout
  drift it is (the client's head not at version 1), not as a staging problem.

Sizes after M2i (release): factor 1 — client 273,768 B (unchanged), pausable ISM 164,432 (unchanged), admin
program 401,448 (+1,168 on M2h's 400,280: the factor compare, the sweep's CPI, the head read under its branch);
factor 8 (`FACTOR8=1`) — 273,720 · 164,432 · 401,416 (+1,168). Rent at 5,080 lamports/B is 1.39 + 0.84 + 2.04
SOL (4.27 together; M2h's 2.03 and 4.26 at the second decimal). Compute (factor 1, the suite-wide maxima): the
kind 9 hand-over 167,700–169,200 (M2h's 169,100–170,600: the sweep's system transfer ≈+1,700, the head read
≈−3,100); a spent kind 9 at another clock factor 27,117, the other cannot-inherit cases 23,200–28,600 (M2g's
26,300–31,700), a self-transfer 15,141 (M2f's 18,200), a mutable successor 25,513; a successor's first kind 6
44,483 (the launch program's 42,990); the client's kind 8 under a version-2 head 164,758 (164,509 for the spare's,
as before); every mailbox `Execute` ≈3,100 lower — kind 2 28,100, kind 3 24,900, kind 4 spent 13,500, kind 7
26,600–31,600, kind 12 27,700 and spent 15,100; a recovery decision's `Execute` reads the head as before (kind 10
18,000–20,800, kind 11 122,900); `RecoveryRecord` 20,300–31,700, `settle` 18,300, `finish_setup` 47,700, the flags
7,700–9,100, all as before. The suite is 40 program-test tests (two new,
`a_mailbox_decision_never_reads_the_clients_head` and
`the_hand_over_sweeps_the_predecessors_float_to_the_successors_pda`, and one case added) and the core's 12 unit
tests, all green at both factors.

## M2j — the recovery path removed (2026-10-13): Jae's design change, one branch `bridge-m2j`

Jae's decision of 2026-10-13: the validators do nothing but validate. Every change on Solana is a light-client
proof of a GovDAO decision; a lapsed light client — no accepted header, a set change it cannot follow, a fork —
freezes gno → Solana for good (burns back to gno.land keep working through the signers), and the exit is a fresh
deployment, never a hand-over by validators' signatures. So the recovery channel is gone whole, and with it the
second channel of everything: the decision PDA, `Execute`, `StageUpgrade` and `CloseCancelled` take a number
alone, the state keeps one pending count and ranges without a channel byte, and no decision's clock reads the
client. Nothing is deployed, so formats changed rather than kept dead fields (S-24, the hold-carried kind 9 under
a recovery window, is moot). What went, what stayed, the Go lane's list, the sizes and the compute follow.

- **Gone from the admin program.** The recovery channel: `RecoveryInit` (7), `RecoveryVerify` (8),
  `RecoveryRecord` (9), `RecoveryClose` (10), `RecoveryChunk` (13), the stage account and its PDA (`recovery`,
  submitter, digest), the recovery digest (`gno-bridge-recovery`), the cancel-by-recovery digest
  (`gno-bridge-cancel`), its anchors (`0x00 ‖ holdSince`, `0x01 ‖ the last accepted hash`) and its record PDA
  (`cancel`, digest); the window logic (`window`, `window_stands`, `TRIGGER_LAPSE`/`TRIGGER_HOLD`, the 14-day
  lapse and hold windows, a decision's `trigger` and `marker`); the recorded settled set, `Settle` (12) and
  `finish_setup`'s "launch set recorded" check; the recovery counter and `high_water` (with no kind 10
  `last_seq` only rises and every stored range is clamped at it, so it is the mark); kinds 10 (`set sequence`)
  and 11 (`reset client`), unknown now — recorded and spent at recording like any unknown kind; the client's
  head read in `Execute` (M2i left it under `is_recovery`) and the client's state among `Execute`'s fixed
  accounts; `store_state`'s growth (the state never grows: 64 ranges and eight holds are reserved at init and
  neither bound is exceeded); `processor::recovery.rs` whole, its helpers (`client_body`, `grace`,
  `pausable_hold`, `admin_hold`) moved into processor.rs. From the client: `Reset` (7) and the reset authority
  as a role — `Body.admin` stays as the one key that may hand the admin role on, and the client's controls are
  its upgrade authority and `SetAdmin` alone. From the core: `recovery.rs` (the digest) — `decision_prefix`
  lives in `decision.rs`.
- **Kept, unchanged in rule.** The quarantine (any ValidatorsHash change, 7 days; nothing shortens it now —
  kind 11 was the one thing that did), `holdSince`, the dip rule and the grace, the hold history and kind 5's
  21-day floor under a hold (its rationale is GovDAO's own cancel of a forged replacement through the client,
  which has that week to land — no validators' window opens any more), the ring epoch, kinds 1–9 and 12 and
  their clocks, the lone flag's deferral, the hand-over (`successor_init` carries the origin, `last_seq`, the
  seats, the watch slots and the hold history — no settled set, no recovery counter, no payer), `can_inherit`
  (S-18, S-23), `awaits_inheritance` on `handle` and `execute` (the other three entries S-20 closed are gone),
  `finish_setup`'s other checks (S-05, S-06), `CloseCancelled` for the one channel and for a retired program
  (S-13), the float's sweep (S-25), the pausable ISM whole. Kind 1's body is unchanged on the wire: the channel
  byte r/hlgov writes stays and must be 0 (any other value is a cancel that does not parse, spent) — a realm
  change may drop it later, in both places at once.
- **The client's history stays, read by nothing on chain** (`push_history`'s eviction rule, `HistoryFull`): it
  served the admin program's `settle`. Taking it out is a client-layout change (the body, `ParseClientState`,
  the S-01 and S-02 tests) for a later lane if Jae wants it; the watch and the tools read it meanwhile.
- **Tested** (ten admin tests — eight of the recovery, two of the reset by recovery — and the ignored
  `probe_reset` are gone; the client's reset test is re-made; one test is new): kinds 10 and 11
  in their old formats recorded and spent at recording, the sequence moved by two, no account, the client's
  chain, origin and set and the admin program's origin untouched, and a delivery at a sequence doctored to its
  end ignored (`kinds_10_and_11_are_unknown_now_and_the_sequence_never_panics`); `finish_setup` flipping with
  nothing recorded — the state at its 1,574 bytes (262 of them used), no validator key or set hash in it
  (`finish_setup_requires_the_three_programs_to_agree_and_records_nothing`); the hand-over without a settled
  set — the successor's state still the 1,574 bytes its init made, no validator key in it, the live number,
  seats and holds in (`upgrade_extends_then_upgrades_and_the_successor_takes_everything`); the wire contract
  re-pinned byte by byte in M2j's form, every tag with it (`the_hand_overs_wire_contract_is_frozen`); the
  client refusing the removed reset by instruction tag — the old `Reset`'s bytes signed by the admin under tag
  7 and every other tag 0–12 fit no variant, the state byte-identical after each, `SetAdmin` 7, `Grow` 8 and
  `StageNextSet` 9 pinned (`abandoned_stages_close_after_seven_days_and_nothing_resets_the_client`); a kind 9
  to the successor executing under a client head doctored to version 2, the head left as doctored
  (`no_decision_reads_the_clients_head`); a successor refusing `Execute` with `NotInherited` before any
  decision is looked for, then executing a kind 7 with its own authority after the hand-over.

For the Go lane (M3k), every format that changed — the programs are deployed nowhere, so nothing is kept for
compatibility:
- **the admin `Instruction` enum, nine variants:** 0 `Init { seats 4 × 32, watch 2 × 32, origin_domain u32,
  predecessor 32 }` (unchanged) · 1 `TransferAuthorityIn` · 2 `FinishSetup` (accounts 0–14 unchanged) · 3
  `Execute { number: u64 }` — `0x03 ‖ number u64 LE`, no channel — accounts 0 state (w) · 1 the decision (w) ·
  2 its deliverer (w) · 3 the executor (signer, w) · 4 the authority PDA (w) · 5 the pausable ISM's flags ·
  then the kind's own (**six** fixed accounts: the client's state is no longer among them) · 4 `StageUpgrade {
  number: u64 }` (accounts unchanged) · 5 `SetFlag` · 6 `ClearFlag` · 7 `CloseCancelled { number: u64 }`
  (accounts unchanged; was 11) · 8 `SuccessorInit { origin_domain u32, last_seq u64, seats 4 × 32, watch 2 ×
  32, holds (u32 count ‖ start i64 ‖ end i64 each) }` (was 14, with a recovery counter and a settled set),
  accounts 0 state (w) · 1 the predecessor's authority PDA (signer) — sent by kind 9's CPI, never by a tool;
  gone: `RecoveryInit` (7), `RecoveryVerify` (8), `RecoveryRecord` (9), `RecoveryClose` (10), `Settle` (12),
  `RecoveryChunk` (13);
- **kind 9's account list** after the six fixed: the successor program · its programdata · the loader · the
  successor's authority PDA (w) · the five programdata (w: the mailbox, token 0, token 1, the client, the
  pausable ISM) · the outbox (w) · the mailbox program · token 0's PDA (w) · token 0 · token 1's PDA (w) · token
  1 · the pausable ISM's state (w) · the pausable ISM program · **the client's state (w)** · the client program ·
  the successor's state (w) · the system program — the client's state moved into the list, before the client
  program (`ExtraFor`'s `case KindTransfer`); the other kinds' lists are unchanged, kind 2 (object 0) and kind
  5 reading the pausable flags at fixed account 5 (was 6);
- **the decision PDA:** (`decision`, number u64 BE) — no channel byte; **`Decision`:** `version u8 ‖ number u64
  ‖ kind u8 ‖ params (u32 len ‖ bytes) ‖ delivered_slot u64 ‖ delivered_time i64 ‖ deliverer 32 ‖ refund_key
  Option<Pubkey>`, 95 bytes beside the parameters (105 before: channel, trigger and marker gone), `MAX_PARAMS`
  10,145 (`ParseDecision`, `DecisionAddress`);
- **the admin `State` layout** (borsh, little-endian; `ParseAdminState`, hlwatch's successor check): `[0]`
  version · `[1]` clock_factor · `[2]` setup_done · `[3..7]` origin_domain u32 · `[7..15]` last_seq u64 ·
  `[15..207]` guardians.keys 6 × 32 · then flags 6 × Option<Flag>, hold_since Option<Flag>, dip_since
  Option<Flag> (0, or 1 ‖ slot u64 ‖ time i64) · successor Option<Pubkey> · authority_bump u8 · cancelled (u32
  count ‖ from u64 ‖ to u64 each — 16 bytes a range, no channel) · holds (u32 count ‖ start i64 ‖ end i64 each)
  · pending u32 (one count) · predecessor 32 · inherited u8 — gone: `recovery_counter` (was `[15..23]`, so
  `guardians` starts at 15, not 23), `settled` (was after `cancelled`), `pending`'s second slot; fresh with
  nothing set the form is 262 bytes, with every option present 377, and the account is 1,574 bytes at init and
  for the program's life (6,814 before) — the expected-length formula is 262 + 16 per flag, hold or dip present
  + 32 for a successor + 16 per range + 16 per hold;
- **the error codes** (`AdminError`, `3000 + n`), compacted: 0 AlreadyInitialized, 1 NotInitialized, 2
  WrongVersion, 3 WrongAuthority, 4 SetupDone, 5 SetupIncomplete, 6 NotTheMailbox, 7 WrongOrigin, 8
  WrongSender, 9 ShortBody, 10 OutOfOrder, 11 WrongDecision, 12 NotMature, 13 Deferred, 14 UnknownKey, 15
  DuplicateKey, 16 BadSeat, 17 TooManyGuardians, 18 TooManyWatch, 19 CurrentKey, 20 Hold, 21 NotStaged, 22
  WrongBuffer, 23 WrongAccount, 24 WrongDeliverer (was 30), 25 Retired (was 31), 26 NeedsExtend (was 33), 27
  NotInherited (was 39); gone: NoWindow, WindowClosed, NotSettled, StaleStage, SignatureMismatch,
  NotEnoughPower, StoreFull, LongEnough, CancelSpent, NothingToSettle, ChunkOutOfOrder, BodyIncomplete;
- **the seeds and constants:** `recovery` and `cancel` are no seeds; `CHANNEL_MAILBOX`, `CHANNEL_RECOVERY` and
  `SET_SEQUENCE_REACH` are gone, `DECISION_OVERHEAD` (95) is new; `kinds::describe(10)` and `(11)` say
  "unknown";
- **the client's `Instruction` enum:** `Reset` (7) is gone, so `SetAdmin` is 7, `Grow` 8 and `StageNextSet` 9
  (the vectors `client_set_admin`, `client_grow`, `client_grow_8192`, `client_stage_next_set_487883` change
  their first byte; `client_reset` is gone); the client's state layout, head and body, is unchanged
  (`Body.admin` stays), and so are its error codes;
- **the core:** `gno_light_core::recovery` is `gno_light_core::decision` (`decision_prefix` alone); the
  `gno-bridge-recovery` and `gno-bridge-cancel` prefixes exist nowhere (bridge/gnolight/results.go names one);
- **the vectors** (bridge/tools/vectors, regenerated by the Go lane into sol/testdata/vectors.txt):
  `admin_execute_5`, `admin_stage_upgrade_2`, `admin_close_cancelled_9`, `admin_decision_5_pda`,
  `admin_decision_300_pda` replace the channelled names; `admin_successor_init` is new; `admin_recovery_*`,
  `admin_settle`, `admin_recovery_digest_1`, `admin_recovery_stage_pda`, `admin_cancel_*` and `body_reset` are
  gone; `body_cancel_range` and `body_cancel_list` are byte-identical (the channel byte 0 stays);
- **the tools:** `hlrecover` has no program path left (the staging, the signing round, `settle`); `hlwatch`'s
  SETTLE DUE alarm, the recovery window and counter, and `hlrecover execute` as a name go, while its
  successor check reads the new layout (the predecessor at len − 33, inherited at len − 1, as before); `hlsol
  successor-init` is unchanged (`Init` is); the relayer's `HandleAccountMetas` answer and `Handle` accounts are
  unchanged; `ExecuteIx`, `StageUpgradeIx`, `CloseCancelledIx` take a number alone and `ExecuteIx` six fixed
  accounts.

Sizes after M2j (release): factor 1 — client 270,120 B (−3,648 on M2i's 273,768: the reset), pausable ISM
164,432 (unchanged), admin program 316,656 (−84,792 on 401,448: the recovery channel, the settled set, the
window logic); factor 8 (`FACTOR8=1`) — 270,072 · 164,432 · 316,648. Rent at 5,080 lamports/B is 1.37 +
0.84 + 1.61 SOL (3.82 together; 4.27 before). The admin state account is 1,574 B at init (0.008 SOL; 6,814 and
0.035 before) and never grows. Compute (factor 1, the suite-wide maxima per labelled transaction): the kind 9
hand-over 161,900–165,200 (M2i's 167,700–169,200: no settled set in the CPI, a smaller state parsed and
written); a kind 9 spent for a successor that cannot inherit 20,100–25,400 (never initialised 20,116, another
predecessor 22,381, inherited 25,392, retired 23,917, another clock factor 23,899; M2i's 23,200–28,600), a
mutable successor 20,947 (25,513), a self-transfer 11,934 (15,141); the kind 8 upgrade 159,400 (166,200), the
client's kind 8 under a version-2 head 159,700 (164,758), a kind 8 spent 104,900 (110,000); kind 2 24,900
(28,100), kind 3 24,700 (24,900), kind 4 spent 16,300 (13,500: the decision PDA's bump search differs by seed),
kind 5 26,400–27,400 (31,200), kind 6 41,300 (43,000), kind 7 23,400–24,900 (26,600–31,600), a cancelled kind
7 closed 15,200, a 2,250-byte kind 7 spent 11,900 (18,200), kind 12 19,700 (27,700) and spent 13,400 (15,100);
`finish_setup` 45,900 (47,700), `init` 12,600 (15,000), a successor's `init` 15,500 (17,800),
`transfer_authority_in` 14,800 (15,100), `stage_upgrade` 15,100–16,600 (17,800), the flags 6,500–8,000
(7,700–9,100: a 1.5 KB state written, not 6.8 KB), `close_cancelled` 10,400–20,600 (13,100–22,900), on a
retired program 11,800 and 16,100 with a buffer returned (18,900); a successor's first kind 6 42,800 (44,483),
a successor's kind 7 27,900; a decision delivered 93,100–125,200 (the mailbox's `process`, the client's
`Verify`, `handle`), 143,300–146,400 for a block of three dispatches, 317,100 for the 2,250-byte body
(319,700). The suite is 31 program-test tests (ten admin tests of the recovery and the reset gone with the
ignored `probe_reset`, one new, two re-made, the client's ignored probe left) and the core's 28, all green at
both factors.

## M2k — the final check's AT3 (2026-10-13): the admin program's init takes the starting number, one commit on `bridge-m3k`

The final check's red team (audit 7, AT3, medium): §6.4's exit — a fresh deployment under the same r/hlgov — could
not be governed. `init` wrote `last_seq: 0`, r/hlgov's counter never goes down (`sync sequence` only raises it),
and `handle` refuses a number above last + 1 (`OutOfOrder`): the first decision after the recipient switch,
r/hlgov's #38 against a program expecting #1, would have stayed deliverable for ever with every decision behind
it, and a new r/hlgov at another path could never write hlmailbox's enrolment note. The fix is the one the red
team named: `init` takes the number the sequence starts at. One commit (3844bee, with the Go's half; M3k).

- **The instruction.** `Init { seats 4 × 32, watch 2 × 32, origin_domain u32, predecessor 32, last_seq u64 }` —
  the number appended as the last field, so the bytes before it are unchanged: `0x00 ‖ seats ‖ watch ‖ origin ‖
  predecessor ‖ last_seq LE`, 237 bytes (229 before). `init` writes it into the state, and `handle` records
  `last_seq + 1` first, as before: 0 for a chain's first deployment, r/hlgov's `Last()` for a fresh deployment
  under the same r/hlgov (`hlsol setup -start-sequence` sets it and refuses a number that is not r/hlgov's; the
  audit's counter line fails on the gap, naming this direction — M3k). A successor's init gives 0 as it gives
  zero seats: a non-zero number is refused `OutOfOrder`, since its number is the predecessor's, written by
  `successor_init` at the hand-over — nothing of `successor_init`, `can_inherit` or `awaits_inheritance` moved.
  The program cannot read gno.land, so nothing on chain checks the number against r/hlgov: the setup and the
  audit are the check, and a fresh r/hlgov for a new chain compiles the same number as its start
  (`launchSequence`).
- **The wire contract** (`the_hand_overs_wire_contract_is_frozen`) pins `Init`'s encoding with the number
  (`0x1112_1314_1516_1718` after the predecessor, 1 + 192 + 4 + 32 + 8 bytes); `SuccessorInit` (variant 8) and
  `State`'s layout are as M2j left them.
- **Tested** (`a_fresh_deployment_starts_its_sequence_at_r_hlgovs_number`, a world built by `World::new_from(37)`
  — `World::new()` is `new_from(0)`, and every world asserts the state's `last_seq` after the init): the setup
  step passes with the sequence at 37; a copy of the old deployment's #37 is accepted and ignored (nothing
  recorded); #39 before #38 is `OutOfOrder`; #38 (a kind 7) is recorded pending, refused `NotMature` on day 0 and
  executed on its 7th day (the fee set); then #39 lands. In every world's `new`, a successor's init naming a
  number (start + 1) is refused `OutOfOrder` beside the seats' `BadSeat`. The harness's `init`, `init_of` and
  `init_successor` (testlib) take the number. Red first: the harness against the old `Init` is a type error (no
  field `last_seq`), and the Go side's regenerated vectors against the old builder failed byte for byte (M3k).
- **The vectors tool** prints `admin_init` (the launch init, 0), `admin_init_fresh_37` (new: a fresh deployment
  at 37) and `admin_init_successor` (0), the two old names eight bytes longer; the Go lane regenerated
  `sol/testdata/vectors.txt` from it.

For the Go lane: `InitIx(payer, authority, seats, watch, origin, predecessor, lastSeq)`, `SuccessorInitIx`
passing 0; nothing else of the admin program's instructions, state, decision or error table moved.

Sizes after M2k (release): factor 1 — client 270,120 B (unchanged), pausable ISM 164,432 (unchanged), admin
program 316,512 (−144 on M2j's 316,656); factor 8 — 270,072 · 164,432 · 316,504 (−144). Rent unchanged to the
figure (1.61 SOL for the admin program). Compute (the new test, the two factors alike): `init` 12,611 (M2j's
12,600 — the eight bytes cost nothing measurable), `finish_setup` 45,927, `transfer_authority_in` 14,036–14,809,
the kind 7's execute 26,438 at factor 1 and 26,396 at factor 8. The suite is 32 program-test tests (18 admin) and
the core's 28, green at both factors.

## Where things are

```
bridge/tools/gnoheaders.py          fetches gnoland-1 headers, commits, validator sets, block results → fixtures
bridge/programs/                    the Cargo workspace built by cargo-build-sbf (platform-tools v1.51)
  gno-light-core/                   pure logic: amino, merkle, header hash, sign bytes, validator sets, commit rule,
                                    results leaf + Dispatch, a decision body's prefix; fixtures loader and a test kit (features)
  sol-util/                         shared on Solana: ed25519 precompile introspection, PDAs, Hyperlane interface,
                                    loader-v3 layouts, the guardians' seats/flags/holds, the client's state head
  gno-light-client-ism/             the light client ISM
  pausable-ism/                     the pausable ISM
  admin-program/                    the admin program
  fixtures/                         17 heights of gnoland-1 (the three real set changes, §13's blocks, the head)
  tests/                            a second workspace: the program-test harness (testlib) and the suite
  test.sh                           core · sbf · suite · all (FACTOR8=1 builds the devnet clocks)
```

Run: `programs/test.sh` (everything), or `test.sh core` (the core against the chain's fixtures, the shared crate's
units), `test.sh sbf` (the three `.so` in `programs/target/deploy`), `test.sh suite -- --nocapture` (the programs
under program-test, with the compute-unit report per labelled transaction).

## Toolchain facts that shaped the layout

- The SBF toolchain is Solana 3.0.14's platform-tools v1.51: cargo/rustc 1.84. Crates with `edition = "2024"` or
  `rust-version ≥ 1.85` cannot even be *parsed* by it, and the resolver picks them freely. The programs' lockfile is
  therefore pinned to the versions Hyperlane's sealevel lockfile uses with the same tools (`zeroize 1.8`,
  `solana-pubkey 4.0`, `borsh 1.6`, …), and the integration tests live in a **separate workspace**
  (`programs/tests/`) so program-test's tree (blake3 → digest 0.11, edition 2024) never enters the programs' lock.
  A `cargo update` in `programs/` must be re-checked against this (the scanner is the `align.py` logic: every
  crate's manifest edition and rust-version).
- The ed25519 precompile is cheaper than the curve25519 syscalls (no sha512 syscall exists for the challenge
  hash), so every signature goes through the precompile and §6.3's introspection (`sol-util::ed25519`): the
  program id, `num_signatures`, every instruction index naming the precompile instruction itself, every offset
  inside its data, the message length. Program-test runs the precompile: a corrupted signature fails the
  transaction before the program runs (tested).
- An account may grow by 10 KB per instruction, and a CPI `create_account` is capped at 10 KB. The client's state
  (64-byte head, body, a 256 × 72-byte ring) is created at 10 KB and grown by a permissionless `Grow` instruction
  (three calls at the deploy, rent paid by the caller) — layout `head ‖ body region (capacity in the head) ‖ ring
  region`. A bigger validator set needs `Grow` before the update that announces it (`NeedsGrow` otherwise: the
  update stays staged, nothing is lost). Staged proofs grow the same way as their leaf is appended.
- The default heap is 32 KB, and the programs' allocator is the entrypoint's bump allocator (freed memory is
  never reused). With the staging chunked (S-02) the transaction size bounds nothing any more; the heap does.
  Measured on the SBF builds, the body region grown to its 80 KB bound first, consecutive unsettled rotations
  filling the history to five entries (`test.sh suite -- --ignored probe_next_set` with `PROBE_N` and
  `PROBE_ROTATIONS`; `probe_reset` with `PROBE_N`): a set of **40** validators is announced and followed through
  four unsettled rotations in a row (the history full at five entries; the fifth announcement is `HistoryFull`,
  the rule itself, the body 8,775 bytes); **50** the same (10,775 bytes); **60** through three (the fourth
  announcement, the history going from four entries to five, dies in `UpdateFinish` with "memory allocation
  failed"); **70** through two; **80** and **100** through one; **120** and above not at all. A kind 11 with
  **50** validators records and executes (146,700 CU), **60** too (169,700 CU); **80** and **100** record but
  their execution dies in the admin program; **200** and above die at `RecoveryRecord`. So the serving limit as
  built is 50 validators through any history, 60 with a settled history, 100 for a single change — gno.land has
  6 — and a larger set needs the heap frame the plan names (a custom allocator over `request_heap_frame`, up to
  256 KB) and a leaner body: this is the build item before any such set, as §6.1 says. The bodies and stages
  now serialize straight into their accounts (no buffer), which is what lifted the earlier ~9-validator
  transaction bound to these figures. Beside the heap's bound stands the account's (M2f, S-14): a decision's
  parameters are at most `MAX_PARAMS` = 10,135 bytes, what one 10,240-byte account holds beside the decision's
  105 bytes of overhead, so a kind 11 can carry at most 252 validators with a 9-byte chain id (250 with a
  64-byte one) — never the 255 the old comment and §6.1 named — a bound the heap frame will not lift. *(M2j: no
  kind 11 and no recovery staging; of these figures the client's own bounds remain — 50 validators through any
  history, 60 with a settled one, 100 for a single change — and `MAX_PARAMS`, 10,145 now, bounds the mailbox's
  decisions alone.)*
- The loader's `ExtendProgramChecked` (tag 9) does not exist on mainnet's loader (agave v4.3.0 handles tags 0–7;
  the checked form was behind `enable_extend_program_checked`, abandoned with SIMD-0164, the feature account absent
  on mainnet, testnet and devnet), and mainnet's runtime lets no program reach the loader by CPI except for
  Upgrade, SetAuthority(Checked) and Close (`check_authorized_program`), so no extend of any form can be sent from
  the admin program. program-test still carries the feature, which is why the v27 suite passed; the admin suite
  now deactivates it (`Harness::new_with_features`), so it runs against the loader mainnet has.
- The mailbox build this deployment uses (REF's `mailbox.so`, mainnet's) signs an ISM's `Verify` with no per-ISM
  process authority (the newer sealevel source derives one, `process_authority ‖ ism`, "so that ISMs that require
  the Mailbox to co-sign (e.g. RateLimited)" get a signer; the suite showed the build does not): the pausable
  ISM binds its metering to the delivery through the Instructions sysvar instead (choice 26).

## Choices where the plan is silent

1. **Precommit slots.** A staged slot carries a precommit only when it is for the commit's BlockID; a nil precommit
   or one for another block is dropped to an empty slot by the submitter and never verified (it could never be
   tallied; verifying it would only cost CU). The chain's own `VerifyCommit` verifies strays too, but only tallies
   matches — the safety-relevant part is the same. Each slot carries its own timestamp; the height is the
   header's, and the round is the commit's one round, carried by `StageInit` (M2e, S-08: a slot whose round
   differs is refused).
2. **A proof's key** is `sha256(message bytes)` — the `Dispatch` event's own `hash` attribute — so a proof PDA is
   (`proof`, submitter, message hash). The ISM's `metadata` is the submitter's pubkey (32 bytes); `Verify` derives
   the proof account from it and refuses any other.
3. **`Type` answers 0** (Hyperlane's `Unused`): a stock Hyperlane relayer skips the route instead of guessing a
   metadata format; the bridge's own relayer (M3) builds the proofs.
4. **The record** (counter and running hash) starts with the trusted header `init` writes and continues across a
   kind-11 reset (the reset's trusted header is recorded too), so the watch's chain has no gap. *(M2j: there is
   no reset; the record starts at `init` and runs on.)*
5. *(Gone at M2j: no reset, and nothing clears the quarantine.)* **A reset clears the quarantine** (the set it installs is settled by definition, and the admin program records
   it as its settled set, choice 25; a hold the guardians want keeps standing on the pausable ISM) and sets the
   last-accepted time, which closes a lapse window: a kind 11 executed first voids every other lapse-opened
   recovery decision still pending (they are void at their next `Execute`), so the reset is the last decision of
   a recovery procedure (§6.4) and the validators' tool orders it last, after every other decision of the batch
   has executed (C236).
6. **Set history depth** is 5 entries, each with its full set, and the eviction rule is the settled anchor's
   (S-01): the newest entry whose quarantine has ended by the Clock — the entry the admin program's `Settle`
   copies — is never evicted, nor any entry after it; only an entry older than it makes room, and with none the
   announcement answers `HistoryFull` (the update stays staged; the oldest unsettled entry settles by the clock a
   week after its announcement and makes room). The client reads no holds, so under a hold its view is the
   clock's alone; the record in the admin state is what the recovery signs with (choice 25).
7. **Holds (v25).** A flag carries one moment, the one it was last set at, fresh on every set; only an unpause
   reads its slot. The hold belongs to the object: `hold_since` is set when the standing guardian flags reach two,
   kept while they stay at two or more and through a dip below two shorter than the grace (24 hours of Clock
   time at the clock factor; `dip_since` records the dip's start), and ended by a dip of the grace's length, after
   which the next two flags begin a new one. The one clear that ends a hold at once is a kind-5 replacement's
   own — of a standing guardian flag — when it leaves fewer than two guardian flags (S-03: a replacement of a
   watch slot or of a seat whose flag is down, during a dip, ends nothing; the dip runs on as it would have).
   "A hold stands" is `hold()` — a pure function of those moments
   and the Clock — for every rule: the deferral of the 7-day group (a lone flag or a hold), the spending of an
   unpause, the owner's clearing of the pausable ISM's flags, the recovery window and its voiding, kind 5's
   15-day floor; deliveries alone follow the flags (any one refuses `Verify`). The admin program reads the
   pausable ISM's hold from its flags account without any transaction having settled it; a change (set, clear,
   unpause, replacement) settles an expired dip first. Settlement compares `hold_since`'s time with the
   quarantine's end (both unix seconds); the slots stored beside the times are only for the unpause rule.
8. **Decision parameters are big-endian** throughout (the sequence number is); kind 11's chain id was a u8 length
   then the bytes, a validator's power an i64 in 8 bytes (a kind 11 is unknown since M2j).
9. **The deliverer.** `HandleAccountMetas` answers the state, the decision PDA and the system program — the
   accounts `handle` reads before the deliverer, in its order, for every kind; the relayer appends its own payer
   (signer, writable) as the next `Handle` account — that payer is the deliverer whose rent comes back — and for a
   cancel the authority PDA and then the pairs it wants closed now, exactly as `handle` reads them (v27: the v26
   answer put a cancel's authority PDA before the payer, so a relayer taking the metas by simulation and
   appending its payer failed every cancel with WrongAccount; the suite now delivers from the simulated answer).
   Hyperlane's relayer never appends a signer; ours (M3) does for decisions.
10. **Unknown kinds** — kinds 10 and 11 among them since M2j — advance the sequence and leave no account ("recorded
    and spent": the transaction is the trace). Kind 4, reserved (v27 §13), is a known kind: recorded as a pending
    account like the rest of the 7-day group and spent at `Execute`, whatever its bytes.
11. **Kind 5 under a hold on the admin program** matures on the later of its own 7th day and the 21st day of the
    earliest hold overlapping its pending life (v26's 21st day; the 15th in v23–v25: 21 × 24 hours after
    `holdSince`, 63 hours at the devnet factor); a lone flag never defers it; two may pend together. **The floor
    is a function of the hold history (AN4, C228; v27 wrote it into the decision at the first deferred
    `Execute`, which only the operator's machines sent).** The admin state keeps `holds`, the last eight holds
    that ended as (start, end): a hold ended by a dip of the grace's length is written at the first change of
    the flags that learns it (`set_flag`, `clear_flag`, an unpause of this program's flags, a replacement), with
    the end the moment the dip reached the grace; one ended by a replacement's own clear is written then, with
    the end then. The standing hold is `guardians.hold_since`. At `Execute` a replacement's floor under a
    standing hold is `min(the standing hold's start, the start of every ended hold whose end is at or after the
    decision's delivery) + 21 days` — the earliest hold overlapping [delivery, now] — so two keys re-flagging at
    the grace's last second begin a hold whose own 21st day is never the floor, and no `Execute` need have been
    attempted under the first hold. The floor binds only while a hold stands: with none standing the replacement
    lands on its own 7th day (a hold lifted on day 3 lets a day-1 replacement land on day 8). A ninth hold merges
    the two oldest into one spanning both (the earliest start kept), so churn — eight one-day holds in a row —
    can never push the earliest overlapping hold out; a merged entry can only make a floor earlier, which harms
    the churners alone. `Decision.floor` is gone from the layout (the Go side's `ParseDecision` follows).
12. *(Gone at M2j, the recovery channel with it.)* **The recovery digest's counter** is the number the recovery will take (the counter plus one), so a stage
    built for number n is dead once n is taken (`StaleStage`: re-stage). **The stage (S-02)** is created by
    `RecoveryInit { body_len, digest }` — the digest is the tool's claim, computed from the body, the chain id,
    the program id and the counter it will take or the window's anchor — keyed by (`recovery`, submitter, digest)
    as before, and the body arrives by `RecoveryChunk { offset, chunk }` in order (900 bytes a transaction in the
    suite), the stage growing with it, the submitter paying; the chunk that completes it must make a recovery body
    (kind 1–12, number 0). `RecoveryVerify` and `RecoveryRecord` refuse an incomplete body (`BodyIncomplete`), and
    `RecoveryRecord` recomputes the digest from the whole body and the live state and refuses any other
    (`StaleStage`), so a digest that is not the body's — or not the standing window's anchor — is signable but
    never recordable; the staging itself no longer checks it. The signers are the recorded settled set (choice
    25): `RecoveryInit` takes its size from the record, `RecoveryVerify` reads the admin state (not the client's)
    for the slots, and a record moved since the staging makes the stage stale. A recovery decision records its trigger
    (1 a lapse, 2 a hold) and a marker (the client's last-accepted time for a lapse, the object's `hold_since` for a
    hold) and is void at execution if the marker no longer matches — a fresh header, or a hold that ended (a dip of
    the grace's length) and, if remade, began anew; a shorter dip changes nothing. A void one is closed by any
    `Execute`, mature or not (v27; v26 answered `NotMature` first), its rent back to the submitter. The trigger
    also decides what defers it (v26 §6.4, refined at v27): one recorded while a hold-opened window stood is never
    deferred; one recorded while only a lapse window stood is deferred by a hold on the admin program exactly like
    a mailbox decision — its 7-day group answers `Deferred` until the hold ends (a kind 7 pending past the hold's
    21st day runs once the hold ends, its lapse window standing), except a kind 5, which takes the stored floor
    of choice 11 and lands on the later of its own 7th day and that floor, ending the hold if fewer than two
    flags remain, as GovDAO's kind 5 does. The plan names the hold alone, so a lone flag defers none of them, and
    a carried `unpause` is spent under a hold as a mailbox one is. When both windows stand the hold is the
    trigger: the hold-opened window stands, so the decision is never deferred, and it is void once that hold ends
    even while the lapse goes on.
13. *(M2j: the recovery cancel, its anchors and its digest record are gone; a cancel is a mailbox decision alone,
    its body unchanged on the wire — the channel byte stays and must be 0; the store, the pairs and the clamp at
    the last recorded number stand.)* **Cancels (v26)** execute in the transaction that records them. The accounts after the fixed ones are the
    authority PDA, then pairs of a decision and its deliverer: every pair inside the cancel's ranges is closed now
    (rent to the deliverer, a staged kind-8 buffer back to its refund key); a pair naming a closed or unrecorded
    number is a no-op. A range the pairs closed now do not cover whole — and a list entry not closed now — is
    then stored in the state (merged, at most 64 disjoint ranges in all), so a number not passed is dead too:
    `Execute` closes it instead of running it, and `CloseCancelled` closes it at any time. **The store never
    jams (AN7, S-04):** a range whose every number the passed pairs closed is not stored; the state counts the
    pending decision accounts per channel (`pending`, one up at every record, one off at every close — Execute,
    CloseCancelled, a cancel's pair), and at zero no number at or below the channel's last is pending, so every
    stored range of the channel is pruned; and a full store merges the new range with the nearest stored range of
    its channel — or, with none of its channel stored, merges the two nearest stored ranges of the other channel
    with each other — rather than refuse: the gap's numbers die too, which only kills pending decisions (a
    cancelled decision never executes; GovDAO's lost ones are re-made), never a jammed cancel. `StoreFull` is no
    longer answered. A mailbox cancel naming channel 1 is spent. A recovery cancel carries no counter: its digest is
    `sha256("gno-bridge-cancel" ‖ chain id ‖ admin program id ‖ anchor ‖ body)`, the anchor `0x00 ‖ holdSince i64
    BE` for a hold-opened window and `0x01 ‖ the client's trusted hash (32)` — the last accepted header's — for a
    lapse-opened one (§13). `recovery_init` takes a stage for either anchor that exists at staging (the hold's
    whenever a hold stands on the admin program, its 14th day reached or not, so the signatures can be ready the
    moment the window opens; the lapse's always), the submitter choosing by the stage address it passes;
    `recovery_record` accepts a cancel only with the anchor of the window standing at that moment — the hold's
    (`holdSince`, its 14th day reached) when a hold-opened window stands, the lapse's (the last accepted header's
    hash, 14 days old) when only a lapse window does, the hold's when both stand, exactly as a decision recorded
    then is the hold's — and answers `StaleStage` to any other, re-staging under the standing anchor being the
    cure. Each executed cancel's digest is kept in a PDA (`cancel` ‖ digest: version, anchor
    kind, submitter, slot, time — 50 bytes, ≈0.0013 SOL of rent paid by the submitter), which `recovery_record`
    takes as account 3 in a decision's place and creates; a repeat is refused (`CancelSpent`) in the same window,
    and in a later one it cannot even be staged (a new hold has a new `holdSince`, a closed lapse a new last
    header). The record is never closed: a hold's start never recurs, but a reset (kind 11) could reinstall an old
    trusted hash under the same chain id, and the rent is the price of an executed cancel, which is rare. The same
    ranges re-signed under a new anchor are a new cancel, which executes once more (a no-op on numbers already
    closed; the ranges merge into the store). A range is clamped at the channel's last recorded number when it
    executes, so it cancels only decisions pending then, never numbers recorded later. The relayer appends the
    authority PDA and the pairs it wants closed after its payer (choice 9), having read each decision's deliverer
    from its account. **v27:** kind 1's body is `salt u64 ‖ form u8 ‖ …` on both channels (§13; r/hlgov prepends
    the salt to every mailbox cancel it dispatches, M1b): the program reads the salt and ignores it, and a
    recovery cancel's digest covers it, so two cancels of the same list under different salts are two cancels,
    each executing once with its own record (the second a no-op on the numbers the first closed). The record is
    the program's own allocation — `pda::create_pda_account`: `create_account`, or, when a stranger's lamports
    already sit at the address (which would make `create_account` fail), a top-up to the rent by the submitter
    and then `allocate` and `assign` under the PDA's signature — so a pre-funded record address blocks nothing;
    lamports above the rent stay in the record (tested at the least a transfer may leave at a new address, an
    empty account's rent, and at 0.01 SOL).
14. **Kind 4 is reserved (v27 §13).** The client's state layout the pausable ISM and the admin program read —
    head and body — is fixed at version 1 for the deployment's life: the pausable ISM's `Verify` pins
    `client_head::VERSION_1` (never the stored field), its init refuses any other `target_version`
    (`LayoutFixed`) and its `SetLayoutVersion` is refused outright, kept in the instruction enum only so
    `TransferOwnership` keeps its number (the Go builders' discriminators stand); `State.target_version` stays in
    the layout, always 1. The admin program's `CLIENT_STATE_VERSION` is the same constant. A kind 4 is recorded
    like any 7-day decision and spent at `Execute`; a new layout is a successor pair of programs by kind 9.
15. **A short body** (under nine bytes) fails `Handle`; the mailbox leaves the message deliverable.
16. **Kind 2 under a hold** is spent by the admin program itself (it reads the pausable ISM's flags) rather than by
    a failing CPI, as §6.5 asks ("spent without effect").
17. **The loader's extend (F1, the assumptions audit)** is the loader's own permissionless `ExtendProgram` (tag
    6: programdata (w), program (w), the system program, the payer (signer, w); no authority signs, the payer
    funds the added rent), sent top-level by the executor — never by the admin program: `ExtendProgramChecked`
    (tag 9, which v27 sent by CPI) is not on mainnet's loader, and mainnet's runtime refuses every extend by CPI
    (`check_authorized_program` admits Upgrade, SetAuthority(Checked) and Close alone), so `ExtendForUpgrade` is
    gone from the admin program. Two runtime limits shape the growth: the loader allows one extension of a
    program per block, and SIMD-0431's minimum is 10,240 bytes an extension — so a kind 8's growth is one
    `ExtendProgram` of the whole excess (at or above that minimum) from the executor's own key (M3f's F7; the
    first build sent 10,240-byte steps, one a block), and `Execute` of the kind 8 answers `NeedsExtend` until the
    program data is long enough, then upgrades (the tail past the ELF zeroed). A 123 KB growth is one
    transaction; a 10,240-byte step is 2,520 CU (the v27 CPI step was 21,200), which the suite still sends, one
    a block, to show the loader's rule. The admin suite runs with `enable_extend_program_checked` deactivated,
    as mainnet is.
17b. **The authority PDA is passed writable to `Execute`**: Hyperlane's `EnrollRemoteRouters` marks the owner
    writable (it pays a realloc top-up when the router map outgrows the token account), and a CPI may not
    escalate privileges. For that top-up the PDA needs lamports: `hlsol setup` funds it with a small float
    (≈0.01 SOL), which the audit prints. Measured (the suite's token accounts carry no slack): the first enrolled
    router grew the token account by 101 bytes (178 → 279: the 36-byte map entry and Hyperlane's realloc slack)
    and the PDA paid its rent, 702,960 lamports at program-test's 6,960 lamports/B — ≈513,000 lamports, 0.0005
    SOL, at mainnet's 5,080/B; setting or clearing a token's ISM pointer draws nothing. So the 0.01 SOL float is
    ≈20 router enrolments, and §11.4's 0.1 SOL ≈200.
18. **`finish_setup`** reads: the five programdata authorities = the admin PDA, its own = none, the mailbox outbox's
    owner and `max_protocol_fee` (0.05 SOL), the inbox's default ISM = the pausable ISM, each token PDA's owner and
    ISM pointer, the pausable ISM's owner and target, the client's reset authority; and (S-06, account 14 the
    pausable ISM's flags) the pausable ISM's guardian list = this program's, the client's and the pausable ISM's
    stored clock factors = this program's, the client's origin domain = this program's — each a
    `SetupIncomplete` otherwise. It then records the launch set (choice 25): the client's newest settled entry,
    both holds read — at launch the first entry, settled by definition; a successor already carrying its
    predecessor's record keeps it unless the client shows a newer one — and refuses to flip with none recorded
    *(M2j: it records nothing; the state holds no set, and "the client's reset authority" is its admin role)*.
    `transfer_authority_in` refuses the admin program's own programdata (S-05: its authority is burned, never
    moved to its own PDA, from which nothing could move it again).
19. **`VerifyAccountMetas` on the pausable ISM** CPIs the client's when the relayer passes the client program
    account as account 2; without it the four own accounts are answered alone.
20. **The mailbox's master-tree per-ISM process authority**: the mainnet build this deployment uses adds none
    to the `Verify` accounts and signs with none (the suite showed it: a `Verify` requiring that signer was
    `NotTheMailbox` through the real build); both ISMs derive their own accounts and ignore extras, and the
    pausable ISM's delivery binding is the Instructions sysvar's (choice 26). `hlsol setup` must write `MAILBOX`
    and `TOKENS` into the pausable ISM's `config.rs` as well as the admin program's.
21. **Program ids and launch constants** (`config.rs` of each program: the ids, the mailbox realm's pkg_path,
    r/hlgov's address, the token ids) are development values; `hlsol setup` (M3) writes the launch values before
    the reproducible build and the audit prints them.
22. **The successor** (kind 9) inherits at the transfer (C227, K11; v27 took the numbers and seats as its own
    init data, which a flood between the init and the transfer left stale — a successor expecting L0+1 against
    r/hlgov's L1+1 for good). Its own `init` — signed by its upgrade authority, so before the burn kind 9
    requires — takes its origin domain and its predecessor's program id, and zero seats and watch slots (any
    other is `BadSeat`); the predecessor's `Execute` of kind 9, after moving the authorities, CPIs the
    successor's `SuccessorInit { origin_domain, last_seq, recovery_counter, seats, watch, settled, holds }` —
    the live origin domain, the mailbox channel's high-water mark as the sequence number, the recovery counter,
    the four seats and two watch slots as they stand, the recorded settled set and the hold history — which only
    the predecessor's authority PDA (the one `init` named) may sign and which runs once (`NotInherited` again);
    the executor pays the successor state's growth for a bigger settled set. Until it inherited a successor's
    `Handle` refuses every decision (`NotInherited`; the message stays deliverable), so no number below the
    predecessor's is ever taken; it starts with no flags, no hold and no pending decisions, and its state and
    decision PDAs are its own. Kind 9 moves three things of the client (C234): its upgrade authority (the loader),
    the pausable ISM's and tokens' owners beside it, and the client's reset authority — `SetAdmin`, the third
    control of the client beside its upgrade authority and the reset of kind 11, which the audit prints.
    Measured: kind 9 with the hand-over is 163,900 CU; the successor's `SuccessorInit` carries the settled set in
    its data, so a set past ~250 validators would exceed the CPI data limit (10 KB) — far past the heap's own
    bound. *(M2j: `SuccessorInit { origin_domain, last_seq, seats, watch, holds }` — no recovery counter, no
    settled set, no payer, since the state never grows; and the client's third control, the reset, is gone: kind
    9 moves its upgrade authority and its admin role.)*
23. **Stage closing** is the submitter's at any time and anyone's after 7 days (updates and proofs; recovery
    stages too, before M2j); `StageVerify` and `UpdateFinish` need no signer (they only add verified bits or move
    the trusted header, and the rent goes to the recorded submitter).
24. *(Gone at M2j: no kind 10; `last_seq` only rises and is the mark.)* **Kind 10 only raises (v27 §6.4), and by at most 2^32 (AN8).** It lands only strictly above the mailbox
    channel's high-water mark — `State::high_water()`: every number ever recorded, a stored channel-0 cancelled
    range's end among them — and at most `high_water + 2^32`, spent at or below the mark and spent past the
    reach, so the sequence never nears its end; `handle`'s next number is a checked add that ignores a decision
    at the end rather than panic (a panic would have left the message deliverable for good). The mark needs no
    new field: `last_seq` only rises (a decision records at `last_seq + 1`, kind 10 raises it alone), a pending
    account's number is a recorded one, and every stored range is clamped at `last_seq` when it executes; the
    ranges are folded in for the record. So no number is ever reused against a cancelled range or a pending
    account, which a lowering kind 10 could have caused in v26 (a re-recorded number meeting its old pending
    account would have failed `Handle` for good).
25. *(Gone at M2j: the admin state records no set; the client's history stays, read by nothing on chain.)* **The recorded settled set (S-01, AN1, C226, K1).** The admin state's `settled` — hash, the validators with
    their powers, the height it signs from — is what a recovery is signed against, never the client's history,
    whose eviction can then lose nothing the validators sign with (a thief holding the current set's leaked keys
    rotates the client four times within the hour under a hold; the history is his sets alone; v27's
    `recovery_init` answered `NotSettled`). It is written by `finish_setup` (the launch set), by the
    permissionless `Settle` (accounts: state (w), the client's state, the pausable ISM's flags, a payer (signer,
    w) for the account's growth, the system program) whenever the client's newest settled entry — §6.1's rule,
    both holds read — is newer than the record by from-height (`NothingToSettle` otherwise: a hold blocks every
    later entry, or nothing settled since), and by a kind 11, whose set is settled by definition (it also checks
    the set as the client would and spends a bad one rather than pend on a failing CPI). The executor calls
    `Settle` after every quarantine; a thief who meets no hold for a week after his rotation settles it — the
    plan's own rule. The state reserves room for a settled set of 128 at init (6,814 bytes, 0.035 SOL at
    mainnet's rent; v27's was ≈1.5 KB); a bigger set grows the account at the write (`Settle`'s payer, a kind
    11's executor, a successor's executor), at most 10 KB a step, which a set of 255 fits.
26. **The mint limits (AN5).** The pausable ISM's state keeps, per token (`config::TOKENS`, the admin program's
    literal again, equal at build by a const assertion, as is `MAILBOX`), a per-message maximum and a rolling
    day's bucket (`used` and the moment it was last charged; the bucket drains at the day cap per 24 hours at the
    clock factor from that moment): `Verify` of a message whose recipient is a token parses the body as
    Hyperlane's token message (`recipient 32 ‖ amount u256 BE ‖ metadata`; `BadTokenMessage` when shorter than 64
    bytes or past 64 bits), refuses an amount above the maximum (`OverMax`) or one the bucket's level plus it
    would carry past the cap (`OverDayCap`), and charges the bucket; a message to any other recipient mints
    nothing of ours and passes unmetered (an unknown recipient under the default ISM is a third party's program).
    Launch values 25,000 / 100,000 a day for each token in base units of 6 decimals; kind 12 `set mint limits`
    (token u8 ‖ max u64 ‖ day cap u64, the 7-day group, by mailbox or recovery; spent for a token that is none of
    ours) CPIs the owner-only `SetMintLimits`, which leaves the bucket's level standing. The charge binds to the
    delivery: `Verify` takes the Instructions sysvar (account 4) and requires the transaction's current top-level
    instruction to be the mailbox's `InboxProcess` carrying this very message — the mailbox checks the message
    unprocessed before it calls the ISM and marks it processed after, so a charge lands only when the delivery
    does, and a direct `Verify`, or one under another program's wrapper, is `NotTheMailbox` and meters nothing
    (the griefing a direct call would otherwise do: four calls of a real 25,000 message empty a token's day).
    `VerifyAccountMetas` answers the flags, the state (writable now), the client's state, the client program, the
    Instructions sysvar, then the client's; the relayer's transaction must mark the pausable ISM's state
    writable. The pausable ISM's `Verify` can no longer be exercised outside the mailbox, so the suite delivers
    through the real mailbox build with the SPL noop program loaded at the two token ids (`tests/limits.rs`).
27. **`Grow { body_cap }` (S-02)** takes a target body region in bytes: the region grows to the larger of what the
    body wants and the target (bounded by `MAX_BODY_CAP`, 81,920 — what a body of 255-validator sets throughout
    takes — and rounded to 8), 10 KB of account a call, the ring moving to the new region only once the account
    holds the region and a ring of the capacity it had before the call (M2g, S-17; M2e's S-09 waited for the
    entries the ring held, which after a clear is none, so a growth to the bound over a cleared ring left a ring
    of 9 slots until two more paid calls; the first build moved it as soon as the region alone fit, keeping the
    entries that still fit, which emptied a full ring), and a ring whose capacity grew past its entries filling
    its free slots before it overwrites one; 0 asks for nothing beyond the
    body's want (the deploy's three calls). So a bigger set gets its room ahead, step by step, before the announcement
    — a set of 40 needs an 8 KB region, one call from the deploy's. The suite's
    `update_txs` sends the announcing `StageInit` (the next set's length and hash), the `StageNextSet` chunks
    (25 validators each), the signature transactions, the finish.

## Gaps the v24 rules open (none change the programs' shape)

*(M2j: the recovery bullets below describe a channel that is gone, and the Go-side list is round 1's — M2j's
list above supersedes it where they differ.)*

- A list cancel of 16 numbers closed in one transaction needs 32 pair accounts beside the fixed ones: more than a
  transaction holds without an address lookup table. The relayer (M3) uses a lookup table, or passes the pairs it
  can and lets the rest be stored and closed by `CloseCancelled`.
- The stored cancelled ranges are pruned when a channel's pending count reaches zero and merged when the store is
  full (choice 13); a cancel is never refused for the store.
- A hold-opened window's recovery decisions are void once a dip of the grace's length ends the hold — the plan's
  own rule; a tool should execute every pending recovery decision before the guardians lift for a day.
- A range cancel is clamped at its channel's last recorded number on either channel — channel 1's at the recovery
  counter — so it cancels only the decisions pending when it executes, never a recovery recorded later (§6.4; the
  code always did this on both channels; an earlier note here said channel 1 was open-ended, which was wrong).
- Kind 5's floor under a hold comes from the hold history (choice 11) and binds while a hold stands; v26 computed
  it from the hold standing at execution, v27 wrote it at the first deferred `Execute`. A lapse-recorded
  recovery decision's 7-day group is deferred until the hold ends, its kind 5 alone taking the floor (choice 12).
- The Go side (M3c) follows these layouts and formats, and after audit round 1 these new ones:
  - the admin `State` layout: after `cancelled` come `settled` (hash 32 ‖ the set as borsh `Vec<Validator>`
    (u32 count, then pubkey 32 ‖ power i64 LE each) ‖ from_height i64), `holds` (u32 count, then start i64 ‖ end
    i64 each, at most 8), `pending` ([u32; 2]), `predecessor` (32) and `inherited` (u8); the account is 6,814
    bytes at init (a settled set of 128 reserved) and may be longer after a `Settle`;
  - `Decision` lost its trailing `floor` (`ParseDecision`'s exact length moves back 8 bytes);
  - the admin `Instruction` enum: `Init { seats, watch, origin_domain, predecessor }` (no counters); `RecoveryInit {
    body_len: u32, digest: [u8; 32] }` with accounts submitter, stage, state, system (no client state, no flags);
    `RecoveryVerify` reads the admin state as account 1 (not the client's); `RecoveryRecord` without the
    pausable flags (accounts submitter, stage, state, decision, client state, system, then a cancel's authority
    and pairs); `ExtendForUpgrade` (12) gone — the executor sends the loader's own `ExtendProgram` (tag 6 as u32
    LE ‖ additional u32 LE; programdata (w), program (w), system, payer (signer, w)) top-level, one extension of
    the whole excess at or above 10,240 bytes, which the loader takes once a block (M3f's F7); `Settle` (12:
    state (w), client state, pausable flags, payer (signer, w), system);
    `RecoveryChunk { offset: u32, chunk: Vec<u8> }` (13: submitter (signer, w), stage (w), system);
    `SuccessorInit { origin_domain, last_seq, recovery_counter, seats, watch, settled, holds }` (14; sent by the
    predecessor's kind 9, never by a tool);
  - `Execute`'s extra accounts: kind 9 adds the successor's state (w) and the system program after the client
    program; kind 11 adds the system program after the client program; kind 12 takes the pausable ISM's state
    (w) and the pausable ISM program;
  - kind 12 `set mint limits`: `token u8 ‖ max u64 BE ‖ day cap u64 BE` (17 bytes; `Params::SetMintLimits`);
  - `finish_setup` takes the pausable ISM's flags as account 14;
  - the client `Instruction` enum: `StageInit` carries `next_set_len: u16 ‖ next_set_hash: [u8; 32]` where
    `next_set: Option<Vec<Validator>>` was; `Grow { body_cap: u32 }` (0 for the old behaviour);
    `StageNextSet { offset: u16, items: Vec<Validator> }` (10: submitter (signer, w), stage (w), system);
  - the client's history is 5 deep; `HistoryFull` is 1000 + 34, `ChunkOutOfOrder` 1000 + 35;
  - the pausable `State` gained `limits: [MintLimit; 2]` (max u64 ‖ day cap u64 ‖ used u64 ‖ used_at i64 each)
    after `target_version`; its `Instruction` enum gained `SetMintLimits { token: u8, max: u64, day_cap: u64 }`
    (7); its `Verify` accounts are flags, state (w), client state (w), client program, the Instructions sysvar,
    then the client's (`VerifyAccountMetas` answers them so); its errors gained `NotTheMailbox` (2000 + 18),
    `BadTokenMessage` (19), `OverMax` (20), `OverDayCap` (21), `BadToken` (22); the pausable ISM's `config.rs`
    carries `MAILBOX` and `TOKENS`, which `hlsol setup` must write there too (a build whose literals differ from
    the admin program's fails);
  - the admin program's errors gained `NothingToSettle` (3000 + 36), `ChunkOutOfOrder` (37), `BodyIncomplete`
    (38), `NotInherited` (39); `StoreFull` and `LongEnough` are never answered now;
  - the executor's duties: `Settle` after every quarantine's end; a kind 8's growth by the loader's
    `ExtendProgram`; the successor's `init` before its burn, with the predecessor named.
- Kind 8 refuses the admin program's own id (v26 §13): `stage_upgrade` will not take a buffer for such a decision
  and `Execute` spends it. (The program's own upgrade authority is none from the setup step, which `finish_setup`
  requires, so the loader would refuse the upgrade anyway; the refusal spends the decision instead of leaving it
  pending on a failing CPI.)
- A kind 5 that replaces one holder of three leaves two flags: the hold stands with its start unchanged, as §6.1
  says ("fewer than two remain" is the test). A kind 5 executed during a dip that replaces the one standing holder
  ends the hold at once (the clear is a replacement's).
- The programs keep `hold_since`/`dip_since` as (slot, time) pairs; every rule reads the time. The stored slot is
  informational (the audit prints it).

## What is tested, and what it costs

`test.sh core`: 11 unit tests, 8 fixture tests (the 17 real heights: every header hash and ValidatorsHash equal
to the chain's, every precommit signature verified over our sign bytes, the commit rule, the three set changes,
LastResultsHash from the re-encoded results with a SimpleProof of each leaf, the event parse), 8 shared-crate
tests (four on the guardians — a replacement of nobody holding ending no hold among them — the Hyperlane
discriminators, the message, the outbox with and without an owner, the token message).

`test.sh suite` (program-test on the SBF builds; the same 31 pass with `FACTOR8=1`; one ignored probe measures the
staging limit):
- client: init needs the upgrade authority and runs once · an update accepted (4 of 4, 3 of 4), refused at 2 of
  4, a forged set's signatures refused by the precompile and by the ValidatorsHash check, a replayed height, a
  wrong chain id · the real chain's 17 headers replayed through its three set changes (the announcing header
  required, the quarantine starting at it, the ring cleared) · a next set of 40 (S-02): `Grow` to an 8 KB region
  in one step with the ring kept, a target below the current region growing nothing, the set announced in two
  chunks (the finish before the set is whole and a chunk out of order or past the length refused), followed by
  27 of 40, 26 of 40 refused, a set staged under the right length and hash whose chunks make another refused at
  the finish · a proof by the ring and by a header with its commit, `VerifyAccountMetas`, a wrong message, a
  wrong leaf, a header above the trusted height, an 8 KB+ leaf, `Type` · an abandoned stage closed by a stranger
  after 7 days, the admin role moved by the admin alone and nothing resetting the client — the removed `Reset`'s
  bytes refused under its old tag and every other, the state byte-identical after each (M2j) · (M2e) a round-2 commit followed, the same signatures
  staged under round 0 verifying nothing, a mixed-round commit refused at the staging, a negative round malformed
  (S-08) · a stranger's `Grow` to the bound with a full ring and with a partial one, every entry found after every
  call and an update landing after (S-09) · a proof of a header the ring holds recording nothing, two proofs of a
  fresh header leaving one record and one ring entry (S-10) · (M2f) a ring proof staged with its entry's copy
  under the ring's epoch, a stranger's 257 header proofs turning the ring over (101, 102 and 360 evicted, each
  header recorded once), the staged proof delivering after them, a ring proof staged before a set change
  `NotInRing` after it (S-16) · (M2g) a stranger's `Grow` to the bound over an empty
  ring keeping 256 slots after every call, the region moving on the 8th, an update landing at slot 0 (the S-09
  cases asserting the capacity too); a full 9-slot ring (the shape the S-09 build left) grown to 151 slots
  taking two headers at slots 9 and 10 with its nine entries kept, then completed at 256 (S-17).
- pausable ISM: an init at a layout version other than 1 refused · flags with their slot, a hold and its
  unchanged start under three keys taking turns, a dip shorter than the grace keeping it and one of the grace's
  length ending it, unpause under a hold refused, a replacement through a hold ending it at once, the layout
  version never moving (`SetLayoutVersion` refused whatever its arguments), the owner · `VerifyAccountMetas`
  answering the flags, the state (writable), the client's state, the client program, the Instructions sysvar and
  the proof; `Type` forwarded; a direct `Verify` refused (`NotTheMailbox`), the wrong client program refused.
- the mint limits (`tests/limits.rs`, the real mailbox build with the SPL noop program at the two token ids, so
  every `Verify` runs inside an `InboxProcess`): the launch limits read back; the maximum passing and charging
  the bucket, one unit above refused; four maxima filling the day and a fifth of one unit refused; the other
  token's own bucket; a quarter day draining a quarter of the cap (the maximum passing, one unit more not); a
  whole day emptying it; a recipient that is no token passing unmetered whatever its body; a short body and an
  amount past 64 bits refused; the setter by the owner (a stranger `NotOwner`, a third token `BadToken`), the
  bucket's level standing under new limits; a direct `Verify` on a real staged proof refused and charging
  nothing; a flag pausing a delivery; a set change's quarantine pausing for a week.
- admin program (with Hyperlane's mainnet mailbox and noop builds, `enable_extend_program_checked` deactivated
  as mainnet is; one channel since M2j): the setup step and `finish_setup`, the admin program's own programdata
  refused by `transfer_authority_in` (S-05), `finish_setup` refusing a pausable ISM whose guardian list differs
  by a key, a client or pausable ISM whose clock factor is not this build's, a client whose origin domain is
  another, each doctored in the bank (S-06), and flipping without recording anything — the state at its 1,574
  bytes with every store at its bound and no validator key or set hash in it (M2j) · a wrong sender, a short
  body, out-of-order, an ignored repeat, an unknown kind spent · the 7-day clock, a lone flag's deferral · a fee
  above the maximum spent · a cancel at recording (its salted body with its channel byte 0, closing now, stored;
  one naming channel 1 not parsing and spent) · kinds 3 and 6 against the real token and mailbox builds, kind 4
  recorded and spent with the pausable ISM's version left at 1 · kind 12 for BUBBLE by mailbox (NotMature on day
  1, applied on day 8, GNOT's untouched), a third token spent · kinds 10 and 11 in their old formats recorded and
  spent at recording — the sequence moved by two, no account, the client's chain, origin and set and the admin
  program's origin untouched, the next decision pending as usual — and a delivery at a sequence doctored to its
  end ignored, never a panic (AN8; M2j) · `HandleAccountMetas` simulated for a fee decision and a cancel, the
  answer delivered with the payer appended, and a cancel's authority PDA and pair after it · the 24-hour unpause
  clearing only earlier flags, spent under a hold · kind 5 through a hold, clearing the replaced keys' flags, a
  current key spent, the 21-day floor under a hold on the admin program (NotMature until it), three keys
  rotating under it, the hold ending at once at the replacement, the hold rules read through a dip with no flag
  standing · the replacement's floor from the hold history (AN4): holds at days 0–20 and 20–∞ landing a day-1
  replacement on day 21 with no `Execute` attempted before day 21, the first hold in the history ended the
  moment its dip reached the grace, the second ended by the replacement's clear; a hold lifted on day 3 letting
  a day-1 replacement land on its own 7th day; a hold's end learnt at the next change of the guardians; a ninth
  hold merging the two oldest with the earliest start kept · kind 8 staged, grown 10 KB a block by the loader's
  own `ExtendProgram` (a second step in one block refused by the loader), upgraded, a hash mismatch spent with
  the buffer returned, one naming the admin program itself unstageable and spent · kind 9 refused for a mutable
  successor; the successor's own init refusing seats, named with its predecessor, burned; a flood before the
  transfer; the successor refusing a decision before it inherited; the transfer moving every authority and
  owner and writing the live number, seats and hold history into the successor — its state still the 1,574
  bytes its init made, no validator key in it (M2j) — which then refuses the number past the next and records
  the next (C227) · the cancelled store (AN7, S-04): a range covered by the pairs closed now storing nothing, a
  stored range pruned once the last pending decision closes, a full store merging the new range with the
  nearest · a staged kind 8, an unstaged one and a fee pending when kind 9 retires the program: the kind 8's
  `Execute` failing at the loader, `StageUpgrade` answering `Retired`, `CloseCancelled` closing all three with
  the buffer back to its writer, while a live program still refuses an uncancelled one (S-13) · the decision
  overhead of 95 and `MAX_PARAMS` = `MAX_GROWTH − 95` = 10,145, a body past the bound and a 2,500-byte one dying
  in the mailbox with the number untaken, a 2,250-byte one recorded whole and spent (S-14) · a kind 9 naming the
  admin program itself spent with no extra account, every authority untouched (S-15) · a kind 9 to an immutable
  program never initialised (its init refused for want of an authority), to one initialised naming another
  predecessor, to the successor with `inherited` already 1, with a `successor` of its own and at the other
  build's clock factor (each doctored in the bank) spent with every authority and owner untouched, the
  successor's state byte-identical and no `successor` set, then the successor as its init left it taking the
  hand-over (S-18, S-23) · the successor as its init left it refusing an `Execute` with `NotInherited` before
  any decision is looked for, then taking the hand-over and executing a kind 7 with its own authority (S-20) ·
  the hand-over's wire contract pinned byte by byte in M2j's form: `State`'s layout (406 bytes with three flags,
  a hold, a successor, one range and two holds), the two fields at its end, the parse of appended bytes,
  `init_size` 1,574, `SuccessorInit` (variant 8) and `Init`, every other tag, and the decision PDA's seeds
  (S-21, a plain unit test in the suite) · a kind 7, the client's kind 8 (from a buffer of its own build, staged
  before) and a kind 9 to the successor executing under a client head doctored to version 2, the head left as
  doctored (S-22; M2j: no decision reads the client) · the hand-over moving the launch PDA's whole float to the
  successor's PDA, the predecessor's left at 0, and a kind 6 delivered to the successor executing there with its
  PDA paying the token account's growth (S-25).

Compute units (program-test, factor 1; the maximum seen per labelled transaction, 4–6-validator sets unless
said):

| transaction | CU |
|---|---|
| client `init` / `grow` (×3) / `Grow` to an 8 KB region / `Grow` to the bound, a call growing the account with the region in place (M2g) / the call that moves a full ring (M2e; a ring of 100, M2g: the 8th call) | 15,700 / 9,000 / 9,100 / 9,100–9,500 / 21,700 (14,300) |
| client `StageInit`+`StagePrecommits` (one tx; 4 validators; 6; 40 slots; M2e, the round) | 36,700; 48,500; 61,700 |
| client `StageNextSet` (a chunk of 25 / 15 validators) | 11,700–22,700 |
| client precompile + `StageVerify` ×1 / ×2 / ×3 signatures | 14,100–16,200 / 15,200–25,800 / 16,300–27,300 (the top of each range with 40-validator sets in the body) |
| client `UpdateFinish` (4, 5, 6 validators; announcing 5–6; 40 validators; announcing 40) | 13,000 / 15,900 / 17,500 / 23,600 / 29,100 / 60,400 |
| **one update, 5 validators (3 transactions)** | **≈60,000 in all** |
| client proof stage by ring (M2f: the entry's copy) / by header (with its commit) | 17,500 / 36,700 |
| client `Verify` by ring (M2f: the copy, no ring scan) / by header (M2e: the ring look-up and the push; with a full ring) | 28,900 / 34,300 (44,600) |
| mailbox `process` → pausable `Verify` (a token message metered) → client `Verify` → noop `handle` | 101,500–119,500 |
| mailbox `process` → client `Verify` → admin `Handle` (a decision delivered; M2j's figures) | 93,100–125,200 (143,300–146,400 for a block of three dispatches, 227,500 for eight: the client's parse of the leaf grows with its events — not a cancel's pairs, as the v26 note said; 317,100 for a 2,250-byte body) |
| pausable `set_flag` / `clear_flag` / `unpause` / `set_guardians` / `set_mint_limits` / `transfer_ownership` | 3,800 / 3,800 / 8,800 / 9,300 / 5,800 / 5,800 |
| admin `set_flag` / `clear_flag` (M2j: a 1.5 KB state, the hold history written) | 6,500–8,000 / 6,500–7,600 |
| admin `Execute` kind 2 / 3 / 4 (spent) / 5 / 6 / 7 / 12 / 12 (spent) — M2j: no decision reads the client; kinds 10 and 11 are unknown, spent at recording | 24,900 / 24,700 / 16,300 / 26,400–27,400 / 41,300 (with the token account's realloc) / 23,400–24,900 / 19,700 / 13,400 |
| admin `stage_upgrade` / the loader's `ExtendProgram` (one 10 KB step, top-level) / kind 8 upgrade (270 KB ELF) / kind 8 spent | 15,100–16,600 / 2,520 / 159,400 / 104,900 |
| admin kind 9 (five authorities, four owners, the client's admin, the successor's inheritance, the float swept; M2j: no settled set in the hand-over, the client's state among the kind's own accounts) / spent (a mutable successor; naming itself; a successor that can never inherit, five cases) | 161,900–165,200 / 20,900 (11,900; 20,100–25,400) |
| a successor's first kind 6 after the hand-over, its PDA paying the token account's realloc / a successor's kind 7 / the client's kind 8 (a 270 KB ELF) under a client head at version 2 | 42,800 / 27,900 / 159,700 |
| admin `close_cancelled` (on a retired program: 11,800, 16,100 with a buffer returned) | 10,400–20,600 |
| admin `finish_setup` / `transfer_authority_in` / `init` / a successor's `init` (M2j) | 45,900 / 14,800 / 12,600 / 15,500 |
| mailbox `init` / `transfer_ownership` (Hyperlane's build) | 15,500 / 20,100 |

Program sizes (release, factor 1, the audit-round-1 build): client 270,888 B · pausable ISM 164,432 B · admin
program 397,640 B — rent at mainnet's 5,080 lamports/B ≈ 1.38 + 0.84 + 2.02 SOL (4.23 together; the v27 build
was 271,160 · 146,808 · 337,576 B, 3.84 SOL; the admin program grew 60 KB with the chunked recovery, the
settlement, the hold history, the successor's hand-over and kind 12, the pausable ISM 18 KB with the token
message and the limits), against §5.8's guesses of 1.5 + 0.3 + 1.0; the reproducible build fixes the final sizes.
The devnet build (`FACTOR8=1`) is 270,888 · 164,432 · 397,624 B; `test.sh` leaves whichever it built last in
`target/deploy`, so a deploy rebuilds at its own factor. The admin state account is 6,814 B at init (0.035 SOL).
After M2e (audit round 2) the builds are 272,464 · 164,432 · 397,760 B at factor 1 and 272,464 · 164,432 ·
397,744 B at factor 8 (the round and its checks, the ring look-up at a proof, the chain-id and retirement checks).
After M2f (audit round 3) they are 273,840 · 164,432 · 398,752 B at factor 1 and 273,792 · 164,432 · 398,720 B at
factor 8 (the ring proof's copy and epoch, the retirement checks on three more paths, the self-transfer's spend).
After M2g (audit round 4) they are 273,768 · 164,432 · 399,632 B at factor 1 and 273,720 · 164,432 · 399,600 B at
factor 8 (the client 72 B smaller for the push's slot choice and the capacity rule, the admin program 880 B
bigger for the successor's state read and the retirement check at the staging).
After M2h (audit round 5) they are 273,768 · 164,432 · 400,280 B at factor 1 and 273,720 · 164,432 · 400,248 B at
factor 8 (the admin program 648 B bigger for the inheritance check on four more paths; the client and the pausable
ISM untouched).
After M2i (audit round 6) they are 273,768 · 164,432 · 401,448 B at factor 1 and 273,720 · 164,432 · 401,416 B at
factor 8 (the admin program 1,168 B bigger for the clock-factor compare, the sweep's system transfer and the head
read moved under the recovery branch; the client and the pausable ISM untouched).
After M2j (the recovery path removed) they are 270,120 · 164,432 · 316,656 B at factor 1 and 270,072 · 164,432 ·
316,648 B at factor 8 (the admin program 84,792 B smaller for the recovery channel, the settled set and the window
logic, the client 3,648 B smaller for the reset; the pausable ISM untouched); rent 1.37 + 0.84 + 1.61 SOL, 3.82
together. The admin state account is 1,574 B at init (0.008 SOL) and never grows.

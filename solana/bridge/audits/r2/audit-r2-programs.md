# Audit 5, round 2 — the three Solana programs as built (bridge/programs, branch `bridge` at 22d28c8, 2026-10-13)

Read: PLAN.md §6.1–6.6, §5.8, §13; M2-NOTES.md (the audit-round-1 section, the toolchain facts, choices 1–27, the
compute table); every file under gno-light-core/src, sol-util/src, gno-light-client-ism/src, pausable-ism/src,
admin-program/src; tests/testlib and tests/suite (limits.rs included). References: Hyperlane's sealevel mailbox
(`inbox_process`, `get_recipient_ism`, `pda_seeds.rs`, the instruction enum), tm2 at 5d9c678 (`Header.Hash`,
`CanonicalVote`, `Vote.SignBytes`, `Commit.ValidateBasic`, `VerifyCommit`, `Validator.Bytes`, `ValidatorSet.Hash`),
agave's `solana-instructions-sysvar` 3.0.0 (`load_current_index_checked`, `load_instruction_at_checked`). No build,
no network. Paths below are relative to `bridge/programs/`.

## Findings

### S-08 · low · a commit is tallied across precommits of different rounds, where tm2 requires one round
`gno-light-core/src/commit.rs:20-57` (a `round` per `Precommit`; `check_shape` checks none against another),
`gno-light-client-ism/src/stage.rs:19-23`, `processor.rs:393-406` (`StagePrecommits` takes any round per slot);
tm2 `pkg/bft/types/block.go:552-582` (`Commit.ValidateBasic`: every precommit's `Round` equals the commit's),
`validator_set.go:632-633` (`VerifyCommit` calls it first).

Scenario: §6.1 says the client applies "the chain's own rule". The chain refuses a commit whose precommits are not
all of one round; the client accepts > 2/3 of power assembled from round-0 and round-3 precommits for the same
header. Under the plan's assumption (less than a third of the power Byzantine) this forges nothing: honest
validators that precommitted B in any round are locked on B, so no other block can be committed at that height,
and a header so assembled is either the committed block or a halted height's only candidate. It is still a weaker
rule than the plan states, and a reviewer comparing the two finds a difference the notes do not mention.

Fix: carry one `round` in `StageInit` (the stage, beside `block_id`), refuse a `StagePrecommits` item whose round
differs, build every slot's sign bytes from it; or state the relaxation in M2-NOTES and §6.1.

### S-09 · low · a stranger's `Grow` to the 80 KB bound empties the ring for a while and sinks ≈0.5 SOL of rent into the state
`gno-light-client-ism/src/processor.rs:165-198` (anyone may name `body_cap` up to `MAX_BODY_CAP`; the region moves
"as soon as the account holds the new region"), `state.rs:176-189` (`relocate` keeps `(len − new) / 72` entries:
zero on the call that first fits the region).

Scenario: with the ring full and relayers proving by the ring, a stranger sends `Grow { body_cap: 81920 }` nine
times (≈10 KB a call, ≈0.5–0.7 SOL of rent, theirs, never recoverable). On the ninth the region relocates with
room for no entry: the 256 accepted hashes are dropped, every staged by-ring proof answers `NotInRing` at its
delivery, and the relayers fall back to header proofs (36k CU, two more transactions each) until headers refill
the ring; three deterministic failures park a message on the Go side. Nothing is forged; the plan's "anyone may
grow" has a cost only its payer meets, but the ring's loss is the readers'.

Fix: in `grow`, relocate only when the account also holds the current entries
(`HEAD_LEN + wanted + ring_count × RING_ENTRY ≤ new_len`), growing first otherwise; keep the bound.

### S-10 · low · the client's `Verify` is callable by anyone, consumes nothing, and appends to the record on every header-based call
`gno-light-client-ism/src/processor.rs:621-691` (no caller check; the stage stays; `684-689` records when
`fresh`), the suite calls it top-level (`tests/suite/tests/client.rs:270,291`).

Scenario: the record (§6.2's counter and running hash) is what `hlwatch` rebuilds "from instruction data" to see
a never-existed header. A stranger with any staged header proof — their own, from a real dispatch to any
recipient, or a relayer's, naming the relayer's key as metadata — calls `Verify` a thousand times: a thousand
records of one header, no delivery behind them. The watch's rebuild must treat a repeated or delivery-less
record as benign or it alarms on noise; the Go side's reading of the record is not stated in the notes. Safety
is untouched: nothing is written but the counter and hash.

Fix: record a header-based proof only the first time (push the header into the ring on a fresh proof and skip the
record when the ring already holds it), or write in the watch's contract that repeats and non-deliveries are
expected.

### S-11 · low · a kind 11 whose chain id the client refuses pends on a failing CPI instead of being spent
`admin-program/src/processor.rs:898-923` (checks the set and the height, not the chain id),
`gno-light-client-ism/src/processor.rs:93-95` (`start_body` refuses an empty or > 64-byte chain id, `ChainId`),
`admin-program/src/kinds.rs:158-169` (a u8 length admits 255 bytes).

Scenario: the validators' reset carries a 65-byte chain id (a tool's slip). It records, matures on day 7, and
every `Execute` aborts in the client's `Reset` — §6.5's "a CPI that fails leaves the decision pending", but no
retry can ever pass. The window stays open while the lapse stands; the batch needs a recovery cancel of the
decision and a re-made reset, one more signing round for five organisations with hardware keys. The same path
cannot check §13's "a header that is not itself announcing a set change", which only the tool can.

Fix: in `execute`'s `Params::Reset`, spend when `chain_id.is_empty() || chain_id.len() > 64` (the client's own
bounds), as the set and height already are.

### S-12 · low · after kind 9 the predecessor keeps recording decisions; `Retired` is never answered
`admin-program/src/processor.rs:867` (`s.successor = Some(program)`, read nowhere), `:320-388` (`handle` has no
retirement check), `error.rs:39` (`Retired`, unused), `successor_init` `:149-173` (the successor takes
`last_seq = high_water()` at the transfer).

Scenario: kind 9 lands on Solana a week after its dispatch; r/hlgov's `set decision recipient` is a separate act
(§5.3). A GovDAO decision dispatched to the predecessor in between is recorded there as number hw+1, dead (its
authority PDA now holds nothing, so its execution fails every CPI, and no one closes it). The successor, expecting
hw+1, answers `OutOfOrder` to hw+2 and everything after until `sync sequence` moves r/hlgov's counter back and
the decision is re-made. The cure exists on the realm; the program's own `Retired` error was meant to make the
gap visible and does not exist in the code path.

Fix: in `handle`, when `s.successor.is_some()` record nothing and return `Retired` (the message stays
deliverable and the relayer parks it, which `hlwatch` shows); M3's procedure dispatches the recipient switch with
the kind 9.

## Regression (round 1)

- S-01 fixed: `gno-light-client-ism/src/state.rs:102-113` never evicts the newest settled-by-clock entry;
  `admin-program/src/state.rs:29`, `recovery.rs:79-134` record the settled set; `recovery.rs:186-212, 243-273,
  279-385` sign against the record alone (tested, admin.rs:1525).
- S-02 fixed: `StageNextSet` `processor.rs:412-430` (25 a call, in order), `Grow { body_cap }` `:165-198`,
  `RecoveryInit { body_len, digest }` + `RecoveryChunk` `recovery.rs:186-240`, the whole hash checked at
  `update_finish` `:529-540` and the digest recomputed at `record` `:313-329`.
- S-03 fixed: `sol-util/src/guardians.rs:199-218` ends a hold only by the replacement's own clear of a standing
  guardian flag leaving fewer than two (unit-tested, `:343-374`).
- S-04 fixed: `admin-program/src/processor.rs:434-447` stores no range the pairs covered whole; `:538-544` prunes
  a channel at zero pending; `state.rs:135-169` merges when full (`StoreFull` unanswered).
- S-05 fixed: `admin-program/src/processor.rs:190-195` refuses the admin program's own programdata.
- S-06 fixed: `admin-program/src/processor.rs:283-299` compares the pausable's guardian list, the two clock
  factors and the client's origin domain.
- S-07 fixed: `sol-util/src/hl.rs:249-253` takes the tree's offset from the owner tag (unit-tested, `:404-414`).

## Checked and held

- Account checks on every instruction: PDA seeds and bumps, owner, signer and writable flags, the system program
  through `create_pda_account`; stages and decisions are only ever created by their own program, so an owner
  check suffices where the address is not rechecked; duplicate pair accounts are no-ops after `close_account`
  reassigns to the system program; a pre-funded PDA address is topped up, allocated and assigned.
- The Instructions sysvar: both readers check the sysvar's address (`load_*_checked`); the pausable's `Verify`
  reads the *current top-level* index, which under a CPI is the mailbox's `InboxProcess`, and compares the whole
  instruction data (tag 1, metadata, the exact message, no tail). Two `InboxProcess` in one transaction each meter
  their own message; the same message twice fails at the mailbox's processed-message PDA before the second charge;
  a wrapping CPI or a direct call is `NotTheMailbox` (a liveness constraint on relayers, never a bypass); a
  recipient-run `Handle` cannot charge another message (bytes compared) and a token's `Handle` runs no code of
  ours. The mailbox checks "processed" before the ISM and creates the PDA before `Handle`.
- ed25519 introspection: program id, `num_signatures`, zero padding, every index the precompile's own, every
  offset and the message length inside its data; entries bound slot by slot to the set's key at that index and to
  the staged precommit's own sign bytes (chain id, height, round, BlockID, time), or to the recovery digest.
- gno-light-core against tm2 at 5d9c678: the 16 leaves and `bytesOrNil`, the canonical vote (fixed64 height and
  round, `CanonicalPartSetHeader.Total` a uvarint, the header form's a zigzag), the validator leaf and address
  order, powers positive with the total ≤ MaxInt64/8, i128 for the rule, nil slots never tallied, the SimpleProof
  walk, `/tm.Event` with the seven attributes in order, a leaf carrying an error refused, lowercase hex only.
- Client state: the trusted height only rises; announcements followed in order through NextValidatorsHash with
  the announcing header required; quarantine `max(old, now + 7d)`; a half-staged set, a chunk out of order or
  repeated, and a set whose hash is not the header's all refused; stages keyed by (submitter, hash) so nobody
  pre-empts; rent back at the finish or to a closer after 7 days; `Verify` only for the current set at height ≤
  trusted, leaf ≤ 8 KB, origin and every attribute matched; the ring cleared at a set change and a reset; the
  record continues across a reset; reset and set-admin by the admin PDA alone. At the heap bound an allocation
  aborts the instruction with nothing written (fail closed); a stored body always loads, so a reset stays
  possible; the frame is the queued item.
- Pausable ISM: a flag per key with its slot, `holdSince` kept through a dip under 24 h and ended by one of 24 h,
  three keys taking turns never move it, the quarantine read from the fixed-seed state with owner and version 1
  pinned, flags of any key refuse deliveries, the owner's unpause refused under a hold and clearing only earlier
  slots, a replacement through any hold, the layout pinned. Mint limits: a 64-byte-minimum token message with the
  top 24 amount bytes zero, `OverMax`, a continuous 24-hour drain in u128 at the clock factor, a failed delivery
  reverts the charge, limits of 0 refuse every non-zero mint, kind 12 by the owner alone.
- Admin program: `Handle` only under the mailbox's `process_authority ‖ recipient` PDA as a signer (seeds equal to
  Hyperlane's `pda_seeds.rs`), r/hlgov's sender and the origin, 9-byte prefix, ≤ last ignored and > last+1
  refused, unknown kinds spent, parameters parsed at execution with `done()` (kind 12's 17 bytes), 24 h / 7 d
  clocks, a lone flag deferring the 7-day group except kind 5, a hold deferring all but kind 5's floor; the hold
  history of eight with the two oldest merged, the floor "the earliest hold overlapping [delivery, now] + 21 days"
  binding only while a hold stands, a replacement's own clear noted then and a dip's end at the dip + grace.
- `settle`: permissionless, both holds read, newer by from-height only, the launch set at `finish_setup`, a kind
  11's set recorded by the execution, the stage stale on any record change (hash compared at verify and record).
- Cancels: list and range, the salt covered by the digest, the anchor `0x00 ‖ holdSince` or `0x01 ‖ trusted hash`
  chosen by the standing window at recording, the record PDA allocated by the program and never closed, a repeat
  `CancelSpent`, ranges clamped at the channel's last number, channel 1 named by recovery alone.
- Recovery: chunks append in order only (no overwrite), the completing chunk must make a recovery body, the
  digest recomputed over the whole body with the counter it takes, signatures of the recorded set and > 2/3 by
  power, the window by lapse (`last_accepted_time`, updates only) or by a 14-day hold with the hold preferred,
  lapse-recorded decisions deferred by a hold like mailbox ones, hold-recorded ones never, void at any `Execute`
  once the marker moved, a reset closing the lapse (last by procedure).
- Kinds 8, 9, 10: the ELF hash over the buffer after its 37-byte header, `NeedsExtend` until the programdata
  holds it (the loader's `ExtendProgram` top-level), the buffer back to the writer on spend or cancel; kind 9
  refused for a mutable successor, every authority and owner moved, `SuccessorInit` signed by the predecessor's
  PDA alone and once, the successor's `Handle` refusing before it; kind 10 strictly above the high-water mark and
  within 2^32, `handle`'s next number a checked add.
- Arithmetic: `overflow-checks` in release, saturating clock sums, checked i64 power sums, u128 for the bucket,
  no realloc past 10,240 in one instruction; re-initialisation refused everywhere.
- Compute and size against M2-NOTES: a 6-validator update ≈ 100–120k CU over three or four transactions (47k to
  stage, 16–27k per signature transaction, 17.5k to finish), a 25-validator chunk ≈ 1,150 B (tight but inside
  1,232), a 16-pair cancel needs a lookup table (the notes say so).
- The clock-factor feature reaches every clock: the grace, the week, the fortnight, the 21 days, the bucket's day,
  the stage closes; and the stored byte each `init` writes, which `finish_setup` compares.

## Tests' blind spots

No malformed precompile instruction (another index, an outside offset, non-zero padding); no `Verify` through a
wrapping CPI or two `InboxProcess` in one transaction; the tokens in `limits.rs` are the noop program (the real
token program's mint and decimals scaling is the Go e2e's); the Clock is written by hand, never a validators'
median; no transaction-size enforcement under program-test; the heap probes are `#[ignore]`; no stranger's `Grow`
during a staged update or with a partial ring (S-09); no kind 11 with a long chain id (S-11); no decision to the
predecessor after kind 9 (S-12); no mixed-round commit (S-08).

## Verdict

Round 1's seven fixes hold, each verifiable at the lines above. The new code — the recorded settlement, chunked
staging and `Grow`, the hold history, the mint limits bound through the Instructions sysvar, the successor's
hand-over, the cancel store — withstood the attacks tried: no forged header, proof, charge or decision, no
bypass of the bucket, no hold moved. Five lows remain: a stated-rule gap (mixed rounds), a ring-emptying `Grow`,
an inflatable record, a reset that pends on its chain id, and a dead `Retired`. Nothing above medium: the
programs converge this round.

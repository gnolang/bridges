# Audit 5, round 3 — the three Solana programs as built (bridge/programs, branch `bridge` at 777ceae, 2026-10-13)

Read: PLAN.md §6.1–6.6, §5.8, §13; M2-NOTES.md (audit round 1, M2e, the toolchain facts, the test list and the
compute table); every file under gno-light-core/src, sol-util/src, gno-light-client-ism/src, pausable-ism/src,
admin-program/src; tests/testlib and tests/suite (client, pausable, limits, admin). References: tm2 at 5d9c678
(`Commit.ValidateBasic` block.go:552, `VerifyCommit` validator_set.go:632, `CanonicalVote` canonical.go:34),
Hyperlane's sealevel mailbox (`Instruction` enum, `inbox_process`), agave 3.1.12 (`agave-precompiles` ed25519,
`solana-program-runtime` cpi.rs, `solana-instructions-sysvar` 3.0.1). No build, no network. Paths below are
relative to `bridge/programs/`.

## Findings

### S-13 · low · a pending decision on a retired predecessor can never be closed; a kind-8 buffer staged there is stranded
`admin-program/src/processor.rs:336-342` (`Retired` is answered by `handle` alone), `:507-536` (`stage_upgrade`
hands a buffer to the PDA with no retirement check), `:573-592` (`close_cancelled` needs `is_cancelled`),
`:762-802` (kind 8's `loader::upgrade` signs with the predecessor's PDA, no longer the upgrade authority),
`recovery.rs:279-385` (`record` has no retirement check).

Scenario: GovDAO's batch carries a kind 8 (an upgrade of the pausable ISM, its 164 KB buffer staged by the
operator, ≈0.84 SOL of rent under the predecessor's PDA) and a kind 9. A lone flag defers the kind 8; the kind 9
lands first. Now every `Execute` of the kind 8 fails at the loader (`IncorrectAuthority`), its hash matches so
`return_buffer` never runs, no mailbox cancel can reach the retired program (`Retired`), and a recovery cancel
needs a 14-day window on it. The buffer and every pending decision's rent are stranded for good; a writer may
even stage a new buffer there after the transfer.

Fix: once `s.successor.is_some()`, let `close_cancelled` close any pending decision of either channel (buffer
returned to its refund key, rent to the deliverer) and have `stage_upgrade` and `recovery::record` answer
`Retired`.

### S-14 · low · `MAX_PARAMS` admits a decision body no decision account can hold, so an oversize mailbox decision sticks the sequence instead of being spent
`admin-program/src/config.rs:52-53` (`MAX_PARAMS = 11_000`, "kind 11 with 255 validators is 10.3 KB"),
`processor.rs:364-367` (spent only above it), `:385-392` (`create_pda_account(…, d.size())`),
`state.rs:257-261` (`Decision::size` = 105 + params), `recovery.rs:196-198`, `:379`; agave
`solana-program-runtime-3.1.12/src/cpi.rs:1165-1173` (a CPI may grow an account by `MAX_PERMITTED_DATA_INCREASE`
= 10,240 bytes over its original length: `InvalidRealloc` past it, a fresh account included).

Scenario: a decision of a known kind whose parameters run 10,136–11,000 bytes passes `handle`'s spend check and
dies in `create_pda_account`; nothing is stored, the message stays deliverable, and every later number answers
`OutOfOrder` until a recovery kind 10 — which needs a 14-day window. Only r/hlgov can author it (a builder bug or
a future kind), so it is defence in depth. On the recovery channel the same cap bounds a kind 11 at 252
validators with a 9-byte chain id, 250 with a 64-byte one — the comment and §6.1 say 255 — a bound the heap frame
will not lift.

Fix: `MAX_PARAMS = 10_240 − 105` (the account's overhead), so an oversize body is spent at `handle` and refused
at `RecoveryInit`; state the 250-validator bound of kind 11 beside the heap's in M2-NOTES.

### S-15 · low · a kind 9 naming the admin program itself passes the immutability check and pends on its own `SuccessorInit` for good
`admin-program/src/processor.rs:803-818` (the only refusal is a mutable successor), `:857-874` (`SuccessorInit`
by CPI into `program`), `:149-157` (`successor_init` answers `NotInherited` for a program whose `predecessor` is
zero), `:672-677` (kind 8's symmetric case is spent), `kinds.rs:263-280` (`describe` names no such rule).

Scenario: a proposal names the admin program as its own successor (a tool default, a copied id). The proposer's
and `hlwatch`'s check reads the admin program's programdata — no authority, "immutable" — and passes it. At
execution every authority is re-pointed to the same PDA, then the CPI into itself fails and the whole `Execute`
reverts; the decision pends until GovDAO cancels it by a second proposal, the 7-day group behind it. The same
pend meets a successor never initialised or one whose `init` named another predecessor (`WrongAuthority`).

Fix: in `execute`'s `Params::Transfer`, spend when `program == *program_id` (as kind 8 does); have r/hlgov's
builder and `hlwatch` refuse it and read the successor's `predecessor` and `inherited` before the vote.

### S-16 · low · a proven header pushed into the ring evicts accepted headers: a stranger can churn the ring and kill every staged ring proof
`gno-light-client-ism/src/processor.rs:683-686` and `:703-710` (S-10: a fresh proof header is recorded and
pushed), `state.rs:143-159` (`push` overwrites the oldest entry), `:343-356` (a ring proof is staged only while
its entry stands), `:664-670` (and delivered only while it still stands).

Scenario: a stranger stages header proofs of 257 old headers of the current set that carry a `Dispatch` (public
headers, commits and messages; ≈0.07 SOL of returnable rent each) and calls `Verify` on them top-level, one a
slot, round and round. Each call that is fresh again evicts the oldest entry: the ring turns over every minute,
so a relayer's ring proof staged a minute before its `InboxProcess` answers `NotInRing`, deliveries fall back to
header proofs (+2 transactions, +36k CU each) or park after repeated refusals, and the record inflates once more
at one entry a call — S-10's fix holds only below 257 distinct headers.

Fix: at a by-ring `StageInit` copy the entry's height and results hash into the stage with a ring epoch the body
keeps (bumped at every `Ring::clear`); `Verify` by ring uses the copy when `st.ring_epoch == body.ring_epoch`
and `st.set_hash == body.set_hash`, needing no entry — eviction then costs nothing.

## Regression (round 2)

- S-08 fixed: `gno-light-client-ism/src/instruction.rs:44` carries the round, `processor.rs:228-230` and
  `:350-353` refuse a negative one and a ring proof naming one, `:413-416` answers `RoundMismatch` (1036),
  `stage.rs:102-120` builds every slot's sign bytes and the tallied commit from the stage's round, the core's
  `commit.rs:36-39, 60-65` refuses a mixed commit; tested client.rs:443-500.
- S-09 fixed: `processor.rs:191-198` moves the region only when `HEAD_LEN + wanted + count × 72 ≤ new_len`,
  `state.rs:177-193` keeps every entry and the write position (copying from the end, so the overlap is safe);
  tested with a full and a partial ring, client.rs:528-580.
- S-10 fixed: `processor.rs:683-686, 703-710` records and pushes a header only when the ring lacks it; tested
  client.rs:326-344 — but the push it adds is S-16's lever.
- S-11 fixed: `admin-program/src/processor.rs:905-913` spends a reset whose chain id is empty or over 64 bytes
  (the set and the height beside); tested admin.rs:1608-1633. A set bigger than the client's region still pends
  on `NeedsGrow`, curable by anyone's `Grow` (tested admin.rs:1699-1704).
- S-12 fixed: `admin-program/src/processor.rs:336-342` answers `Retired` (3031) once `successor` is set, nothing
  recorded; tested admin.rs:1016-1024 — `handle` alone (S-13).

## Checked and held

- The round: immutable in the stage after `StageInit`, every slot's sign bytes (`19` + fixed64, omitted at 0, tm2
  canonical.go:37) and the tallied commit built from it, an item of another round refused before it is stored,
  `check_shape` re-checking at the finish; a nil or other-block precommit never tallied; tm2's `ValidateBasic`
  (block.go:552-584) is exactly this rule, and `VerifyCommit` runs it first.
- `grow`: the target bounded and rounded to 8, never below the region or the body's want; at most 10 KB a call;
  the ring's capacity measured at the old offset until the move; after the move `ring_next` kept when it still
  fits, else wrapped to `keep % cap`; the body region's stale tail harmless (borsh ignores it, `store` zeroes it);
  a growth during a staged update changes nothing the finish reads, and a finish that needs more room fails whole
  (`NeedsGrow`, the stage kept).
- `Verify`: the record and the push only after the proof, the leaf's `Dispatch` and the message's id, sha256,
  nonce, origin, destination, sender and recipient all matched; a ring proof after a set change or a reset
  `NotInRing` (the ring cleared, and an old header can never re-enter under the new set's hash); a stale header
  proof `WrongSet`, `ChainId` or `HeightAboveTrusted`; `last_accepted_time` moved by updates and resets alone.
- `Params::Reset`: the chain id, the set (`check`: 1–255, positive powers, ascending addresses, the total bound)
  and the height pre-checked and spent; the record continues across the reset (one entry, not two — the
  `start_body` record is overwritten), the quarantine cleared, the ring cleared, the lapse closed, the admin kept,
  the admin program's origin and settled record following.
- `Retired`: the successor's first number is `high_water() + 1` (which equals `last_seq + 1`, ranges being clamped
  at `last_seq`); a message parked at the predecessor is dead for good (its recipient is fixed) and r/hlgov's
  `sync sequence` is the cure the plan names; a recovery signed for the predecessor cannot land on the successor
  (the digest carries the program id).
- The pausable ISM: one `MintLimit` per token, kind 12 per token, the bucket's level kept under new limits,
  the drain in u128 at the clock factor's day, a charge written before the client's CPI and reverted with it,
  a charge only inside the mailbox's own top-level `InboxProcess` of the same bytes (`load_current_index_checked`
  reads the top-level index, so a wrapping CPI or a direct call is `NotTheMailbox`), the mailbox checking the
  processed PDA before the ISM and creating it after.
- ed25519 introspection against agave's precompile: the program id, `num_signatures`, a zero padding byte
  (stricter than the precompile), every index the precompile's own or `u16::MAX` (`get_data_slice`), every
  offset and the message length inside its data; a corrupted signature fails the transaction before any program
  runs; the Instructions sysvar's address checked by both readers.
- Account checks on every instruction: seeds and bumps, owners, signers, `create_pda_account` robust to a
  pre-funded address and refusing a non-system or non-empty one; stages, decisions, records and recovery stages
  only ever created by their own program, so an owner check suffices where the address is derived; a cancel's
  pairs re-derived from their own channel and number; re-initialisation refused everywhere (`init` ×3,
  `successor_init` once, `finish_setup` once); a retired predecessor's PDA still signs, which S-13 uses.
- Arithmetic: `overflow-checks` in release; saturating clock sums; the i128 rule; checked i64 power sums; the
  u128 bucket; `h.message.len() − 9` and `body_len − 9` guarded; `(to − from) + 1` guarded; `body_cap8` fits
  u16 at the 80 KB bound; the Clock's unix time is monotonic on mainnet, and every rule tolerates a stall.
- Compute against M2e's table: nothing new runs in a hot path beyond S-10's look-up (34,200 CU by header) and
  the ring copy on a moving `Grow` (21,700); the eight-dispatch delivery (227,500) still needs a compute-budget
  instruction above the 200k default, which the Go side sets; the heap stays the known bound (queued).
- The clock-factor feature reaches every clock (the grace, the week, the fortnight, the 21 days, the bucket's
  day, the stage closes) and the stored byte `finish_setup` compares across the three programs.
- Transaction size: the program bounds `StageNextSet` at 25 (the Go side sends 23), `StagePrecommits` and
  `RecoveryChunk` only by the packet; nothing the programs assume breaks when the Go side chunks smaller.

## Tests' blind spots

No malformed precompile instruction (another index, an outside offset, non-zero padding); no `Verify` through a
wrapping CPI nor two `InboxProcess` in one transaction; the Clock written by hand, never a validators' median;
no transaction-size enforcement under program-test (the 1,232-byte packet is the Go test's); the heap probes
`#[ignore]`; no real-chain fixture at a round above 0 (the round's wire form is checked only against the
testkit's own encoder — an encoding slip would strand the client at an announcing header committed in round 1,
since a gap may skip ordinary headers but never the announcing one); no ring proof dying between its staging and
its delivery (S-16); no pending decision or staged buffer on a retired predecessor (S-13); no decision body
between 10,136 and 11,000 bytes (S-14); no kind 9 naming the admin program itself (S-15); no `NeedsGrow` at an
update's finish cured by `Grow` (only a reset's); kind 6's realloc only with the authority PDA funded; the tokens
in limits.rs are the noop program.

## Verdict

Round 2's five fixes hold at the lines above. The new code — the one-round stage, the relocation rule, the
recorded-once proof, the pre-checked reset, `Retired` — withstood what I tried: no forged header, proof, charge
or decision, no number reused, no hold moved. Four lows remain, three at the edges of round 2's own fixes: a
retired predecessor strands what pends there, `MAX_PARAMS` exceeds what an account can hold, a self-transfer
pends, and proof headers now churn the ring. Nothing above medium: the programs converge this round.

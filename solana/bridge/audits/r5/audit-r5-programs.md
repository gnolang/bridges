# Audit 5, round 5 — the three Solana programs as built (bridge/programs, branch `bridge` at b24c9e9, 2026-10-13)

Read: PLAN.md §6.1–6.6, §5.8, §13; M2-NOTES.md (M2f, M2g, the test list, the compute table); every file under
gno-light-core/src, sol-util/src, gno-light-client-ism/src, pausable-ism/src, admin-program/src; tests/testlib and
tests/suite (client, pausable, limits, admin); the Go `ParseClientState` ring walk (bridge/sol/client.go:735-751)
and the kind-9 account builder (bridge/sol/admin.go:191-205). No build, no network. Paths below are relative to
`bridge/programs/`.

## Findings

### S-20 · medium · a successor that has not inherited runs its own `Settle` and recovery path, outside the predecessor's hold; a decision can pend there before the hand-over and execute with the successor's authority after it
`admin-program/src/recovery.rs:113-134` (`settle` on any program whose state loads; with an empty record it copies
the client's newest settled entry), `:79-84` (the holds read are the pausable ISM's and the *caller's own* — a
successor's seats are zero keys, so no hold can ever stand on it), `:186-218` (`init` refuses a retired program
and an empty record, never an un-inherited one), `:285-396` (`record` under a lapse needs no hold),
`processor.rs:350-354` (`handle` alone answers `NotInherited`), `:168-192` (`successor_init` overwrites the
counters, seats and record but leaves `pending`, `cancelled` and every decision account), `:842-928` (an executed
kind 9 then runs with the successor's PDA, which holds everything). Tested on an un-inherited successor:
`handle` only (tests/suite/tests/admin.rs:972-978).

Scenario: GovDAO prepares successor S (initialised naming P, burned). A thief with the current set's keys rotates
the client to his own keys; the guardians hold P but, the quarantine already pausing deliveries, not the pausable
ISM; a week on his set settles — blocked on P by its hold, not on S, where anyone's `Settle` records it. He starves
the client 14 days, stages a kind 9 (S → his program) on S, three of his keys sign, `RecoveryRecord` lands:
counter 1, trigger lapse. When the holders lift and P's kind 9 executes, his `Execute(1, 1)` on S — in the same
transaction, if he executes — moves every authority to his program. With honest keys instead, the same path
leaves a decision account at a number the inherited counter reuses, and the validators' first recovery on S dies
at `record` (`AccountAlreadyInitialized`) after a signing round. §6.5 promises a successor "with no flags, no
hold and no pending decisions".

Fix: in `recovery::settle` and `recovery::init`, answer `NotInherited` while `predecessor != Pubkey::default()
&& inherited == 0`, as `handle` does; have `hlwatch`'s successor check list S's pending decisions and recovery
counter before the vote, and `hlrecover init` refuse an un-inherited program.

### S-21 · low · the hand-over's wire contract — the predecessor's `State` layout and `SuccessorInit` encoding — is frozen nowhere, so a successor of another build is spent by the peek or dies at the CPI
`admin-program/src/processor.rs:89-98` (`can_inherit` parses the successor's state with *this* crate's `State`,
no version read; `predecessor` and `inherited` are the last two fields, so any field inserted before them moves
them), `:911-925` (`SuccessorInit` encoded with this crate's `Instruction` — variant 14 — and its `Settled`,
`Hold` and `Guardians` layouts), `instruction.rs:120`, `state.rs:7-47`; §13 freezes only the client's state
layout (kind 4), M2-NOTES states no contract for a successor build.

Scenario: a successor built to read a new client layout (§13's kind-4 route) adds a field to its own `State`
before `inherited`, or reorders its instruction enum, or widens `Hold`. Pre-vote `hlwatch` decodes the three
fields with the v1 layout and either passes or alarms on a mis-read; at execution either the peek spends
GovDAO's transfer with nothing moved (an inserted field) or — an encoding change the peek cannot see — the nine
authorities re-point and the successor's `successor_init` fails to deserialize, the transaction reverts whole and
the decision pends until a cancel: S-18's dead transfer again, a week or more on.

Fix: state in §13 and M2-NOTES that a successor keeps `State`'s borsh layout append-only and `SuccessorInit`
(variant 14, its argument layouts) byte-identical; run §6.4's devnet drill with the real successor build; have
`hlwatch` compare the successor's `State` prefix length against the v1 size before the vote.

## Regression (round 4)

- S-17 fixed: `gno-light-client-ism/src/processor.rs:199-202` (the move waits for `HEAD_LEN + wanted + min(256, the
  capacity before the call) × 72 ≤ the new length`; until then the account grows in place), `state.rs:154-175`
  (`push` at `ring_count` while below the capacity, `ring_next` always `< cap`); tested client.rs:548-610 (rings of
  0, 100 and 256 to the bound, the region moving on the 8th call) and :613-662 (the 9-slot ring to 151 then 256).
  Holds; the Go reader (bridge/sol/client.go:739-751) walks `0..min(RingCount, capacity)` and never reads `RingNext`.
- S-18 fixed: `admin-program/src/processor.rs:84` (`SUCCESSOR_STATE_AT` = 15, the order the Go builder sends,
  bridge/sol/admin.go:191-205, and testlib admin.rs:173-196 alike), `:89-98` (`can_inherit`: owner, parse,
  predecessor, `inherited` 0, no successor), `:860-870` (the PDA address checked before either spend — a wrong
  account or a short list is `WrongAccount`/`NotEnoughAccountKeys`, never a spend; the mutable spend first);
  tested admin.rs:1831-1885. Holds — the residual is the build contract, S-21.
- S-19 fixed: `admin-program/src/recovery.rs:196-201` (`init` answers `Retired` once `successor` is set, before
  any rent or signature); tested admin.rs:1888-1923. Holds — the un-inherited successor's `init` is S-20.

## Checked and held

- The ring's invariants: `ring_count ≤ min(capacity, 256)` always (push bounds it, relocate resets it, capacity never shrinks), so grow's `keep == count` and `ring_next < cap` on every move; a partial stranger's growth never blocks the updater (a target below the account's length relocates at once); a relocation bumps no epoch, so staged ring proofs and updates read nothing stale; `push` after a move fills the free slots and the ring's eviction order is merely no longer oldest-first (a cache; the copy of S-16 protects every staged proof).
- The peek: `it.as_slice().get(15)` is the account the CPI takes at the 16th `next_account_info`; the owner check suffices since only the successor can sign its `state` PDA; a forged or misplaced account is an error; a successor's `version` is the successor's own `load_state` to judge.
- The retired predecessor: every pending kind that reaches a shared object dies at that object's owner or authority — a second kind 9 included (the loader refuses P's PDA) — and reverts whole; `Unpause(1)` and kind 10 write only its own state; `settle`, the flags and `recovery::chunk/verify/close` write nothing anyone reads; `close_cancelled` takes anything.
- ed25519 introspection: an index naming another instruction is `OtherInstruction`, a slice past the data `OffsetOutside`, a non-zero padding or zero count `BadHeader`; the precompile cannot be CPI'd, so its instruction is top-level and runtime-verified.
- `require_delivery`: the sysvar's current index is the top-level instruction's, so a wrapping CPI or a direct call is `NotTheMailbox`; two `InboxProcess` in one transaction each meter their own message; a charge is written before the client's CPI and reverts with it.
- The epoch: a staged ring proof across a reset falls to `Ring::find` on a cleared ring, `NotInRing`; within an epoch the chain id, origin and set are constant and the trusted height only rises.
- Account checks and re-initialisation: `init` ×3 refuse a non-empty or own-owned state, `successor_init` and `finish_setup` once; stages, decisions, records and recovery stages exist only by their program; a pre-funded PDA is topped up, allocated and assigned, and no stranger can put data at one.
- Realloc and arithmetic: every resize ≤ 10,240 per instruction (a stage chunk ≤ 1,232 B, the admin state's growth ≤ 5,080 B at 255 validators, a decision ≤ 10,240); saturating clock sums; `(to − from) + 1` checked; the u128 bucket; `len − 9` behind `decision_prefix`; a power past i64 refused by `set.check`; a zero or duplicate key refused by `Guardians::new` and `replace`, so the hand-over's `Guardians::new` cannot fail.
- The hold history's merge can only move a floor earlier when the second-oldest hold ended after the delivery with seven holds ending after it — impossible while the honest holders' flags stand, since no hold then ends and nothing is pushed.
- Compute: nothing new on a hot path; kind 9 at 169–170k grows with the settled set and hold history (≈+8k at 255 validators), so the executor's budget stays above 200k; the full-ring header proof and the eight-dispatch delivery as round 4 said.
- The clock factor: the grace, the week, the fortnight, the 21 days, the bucket's day, both stage closes and the quarantine all divide by the compiled factor; `finish_setup` compares the three stored bytes.
- A successor's `finish_setup` is cosmetic (nothing reads `setup_done` but `transfer_authority_in`, harmless once inherited) and fails for good after a kind 3 cleared a token's ISM pointer — a note for the tools, not a finding.

## Tests' blind spots

No malformed precompile instruction (another index, an outside offset, non-zero padding); no `Verify` through a
wrapping CPI nor two `InboxProcess` in one transaction; the Clock written by hand, never a validators' median; no
real-chain commit at a round above 0 (the round's wire form checked only against the testkit's own encoder); no
staged ring proof dying across a reset (only the epoch's value asserted); no stranger's `Grow` during a staged
update; no kind 9 with the successor's state at the wrong place (`WrongAccount` by reading alone); no `Execute`
of kinds 2, 3, 5–7, 10–12 on a retired predecessor; nothing but `handle` tried on an un-inherited successor
(S-20); no successor of another build (S-21); the heap probes `#[ignore]`; the tokens in limits.rs are the noop
program.

## Verdict

Round 4's three fixes hold at the lines above, and four rounds' guards — the ring's capacity and epoch, the
successor peek, the retired program, the bounded body, the hold history — withstood what I tried: no forged
header, proof, charge or decision, no number reused, no hold moved. One medium: an un-inherited successor runs its
own `Settle` and recovery path outside the predecessor's hold, so a decision can pend there before the hand-over
and execute with the successor's authority after it. One low: the hand-over's wire contract is frozen nowhere.
Both are a few lines; with S-20 closed the programs converge.

# Audit 5, round 1 — the three Solana programs as built (bridge/programs, branch `bridge`, 2026-10-13)

Read: PLAN.md §6.1–6.5, §5.8, §13; M2-NOTES.md; every file under gno-light-core/src, sol-util/src,
gno-light-client-ism/src, pausable-ism/src, admin-program/src, tests/ (testlib + suite) and gno-light-core/tests;
Hyperlane's sealevel mailbox (`inbox_process`, `get_recipient_ism`, pda_seeds), token instruction enum and
`HyperlaneToken` layout; tm2's `Header.Hash`, `bytesOrNil`, `CanonicalVote`, `Vote.SignBytes`, `VerifyCommit`,
`Validator.Bytes`, `ValidatorSet.Hash`, `ValidatorsByAddress`, `PubKeyEd25519.Address`, `ABCIResult`,
`EncodeTimeValue`; gno's `chain.Event` and its amino package. Sizes below were computed by hand from the fixtures
(no build).

## Findings

### S-01 · high · the settled set can be pushed out of the client's 4-entry history, which kills the recovery the plan relies on under a leaked set
`gno-light-client-ism/src/state.rs:95-100` (`push_history` evicts index 0 at `HISTORY_MAX = 4`),
`gno-light-client-ism/src/processor.rs:527-537` (every announcing update pushes),
`admin-program/src/recovery.rs:73-80` (`settled_set` searches only `body.history`), `:147` and `:231` (`NotSettled`).

Scenario: a thief holding the current set's leaked keys delivers a forged kind 5 (new guardian keys), then submits
four announcing updates in a row (each new set is keys he made; each update needs only the previous set's
signatures). The history now holds only those four entries, every one quarantined after the guardians'
`holdSince`, so `settled_set` finds nothing: `recovery_init` answers `NotSettled` and the validators cannot stage
the cancel §6.5's "week of margin" exists for. No lapse opens either, since he keeps feeding headers. On the hold's
21st day the forged kind 5 lands, clears the holders' flags, the hold ends, and his other decisions run. Four
legitimate rotations within a hold cause the same loss of the cure until the hold ends.

Fix: pin the settled set where the client cannot evict it — when `set_flag` on the admin program makes
`hold_since` (`SetFlag` takes the client state and the pausable flags), copy the then-settled entry's hash and
validators into the admin state; `recovery_init`/`record` use that pin for a hold-opened window. Alternatively,
`UpdateFinish` reads both flags accounts and refuses to evict while a hold stands (`HistoryFull`).

### S-02 · high · the built staging path serves a next set of about nine validators, not "about 100"; `Grow` cannot pre-size the body; a kind-11 reset is capped near twenty
`gno-light-client-ism/src/instruction.rs:31-45` and `processor.rs:208-358` (`StageInit` carries the whole
`next_set` in one instruction's data), `processor.rs:163-193` with `state.rs:71-74` (`Grow` only ever grants
`size + STATE_SLACK` = 2,048 bytes over the *current* body), `admin-program/src/recovery.rs:128-173`
(`RecoveryInit { body }` in one instruction), M2-NOTES "sets of about 100", PLAN §6.1 "up to 255 … never a change".

Scenario: a header of gnoland-1 is 376 borsh bytes; an announcing `StageInit` alone is 855 + 40·n bytes of
transaction, so a next set of 10 is 1,201 bytes (1,232 is the limit, and a header whose DataHash and
LastResultsHash are both present adds 64 more), 11 does not fit; with the precommits in the same transaction, as
testlib packages it, 8 does not fit. gno.land went 4→5→6 this year: at the ninth or tenth validator the client
cannot follow the announcement, the trusted height freezes at the announcing header, and every later dispatch is
unprovable. A rotation step adding more than 2,048 bytes of body (a next set of ≥25) answers `NeedsGrow` forever,
and a kind 11 with >20 validators exceeds one transaction. program-test enforces no transaction size, so the suite
cannot see any of this.

Fix: add `StageNextSet { offset, items }` to fill a stage's next set in chunks (StageInit takes its length and
hash); let `Grow { body_cap }` accept a target capacity ≥ current (bounded, caller pays); stage a recovery body in
chunks the same way. Correct §6.1 and M2-NOTES to the real limits meanwhile.

### S-03 · medium · a replacement that replaces nobody holding ends a hold caught in a dip, voiding hold-opened recovery decisions
`sol-util/src/guardians.rs:198-212` (`replace`: after clearing the replaced keys' flags, `guardian_flags() < 2`
ends the hold outright — the count of flags remaining, not of flags the replacement cleared), read by
`admin-program/src/processor.rs:622-638` and `pausable-ism/src/processor.rs:147-158`; `recovery.rs:99-105`
(`window_stands` compares `holdSince`).

Scenario: a kind 5 naming only watch slot 5 is pending (GovDAO's, or forged before the hold). Three holders take
turns; at some moment one flag stands (a dip inside the grace). Whoever executes the kind 5 at that moment — anyone
may — ends the hold at once: `hold_since` is dropped though no holder was replaced. Every recovery decision recorded
under that hold-opened window is void at its next `Execute`, the next two flags begin a hold with a fresh
`holdSince`, and its window opens 14 days later. §6.1 names the end only for a replacement "when it leaves fewer
than two flags", meaning the holders' own.

Fix: in `replace`, end the hold only when the replacement cleared a standing guardian flag and fewer than two
remain; otherwise `recompute(now)` (a dip continues as before). Add the dip case to the guardians' unit test.

### S-04 · low · a range cancel is stored even when every number it names was closed in the transaction, so a full store blocks it despite the accounts
`admin-program/src/processor.rs:324-339` (`single_closed` skips a list entry only; the range form always calls
`add_cancelled`), `state.rs:50-71` (`MAX_CANCELLED = 64`, never pruned), M2-NOTES "the cure is a cancel with the
accounts passed".

Scenario: after 64 disjoint ranges have accumulated (floods cancelled piecemeal over the deployment's life), a
validators' range cancel answers `StoreFull` even when the relayer passes every pending pair, because the range is
stored regardless; a list cancel still works, 16 numbers per signature. The documented cure is wrong for the range
form.

Fix: in the range branch, skip `add_cancelled` when the pairs closed now cover the clamped range (count the closed
numbers inside `[from, to]` against `to − from + 1`); and prune a stored range whose `to` is below the channel's
lowest pending number when a cancel executes.

### S-05 · low · `transfer_authority_in` accepts the admin program's own programdata, after which `finish_setup` can never flip
`admin-program/src/processor.rs:118-135` (no check that `programdata` is not `programdata_address(program_id)`),
`:162-170` (`finish_setup` demands the own authority be none; no instruction can set it to none from the PDA),
PLAN §5.8 (`setup_done` is a launch gate).

Scenario: the deployer's `hlsol setup` (or a hand-sent instruction) moves the admin program's upgrade authority to
its own authority PDA instead of burning it. Every kind refuses to upgrade the admin program, so nothing can move
it again; `finish_setup` answers `SetupIncomplete` forever and the launch audit reads `setup_done = 0`. The
deployment is in fact immutable, but the gate the vote reads never passes and `transfer_authority_in` stays open.

Fix: refuse `programdata.key == loader::programdata_address(program_id)` in `transfer_authority_in`.

### S-06 · low · `finish_setup` never compares what the three programs must agree on: the guardian lists, the clock factors, the origin domain
`admin-program/src/processor.rs:140-216` (reads the pausable's owner and target only; never its flags account,
the client head's `clock_factor` or `origin_domain`), `sol-util/src/client_head.rs:6` (the factor is stored),
`pausable-ism/src/state.rs:9`, `admin-program/src/state.rs:9`.

Scenario: the pausable ISM is initialised with a list that differs by one key from the admin program's. Nothing on
chain notices; a kind 5 later passes the admin program's `replace` and fails the pausable's CPI (`DuplicateKey`),
so the transaction aborts and the replacement stays pending for good — the one decision a hold waits for. A devnet
`.so` (factor 8) deployed beside mainnet builds would likewise run its clocks eight times faster unseen. §5.9's
hand check is the only guard.

Fix: `finish_setup` takes the pausable flags account and requires `flags.guardians.keys == state.guardians.keys`,
the client head's and the pausable state's `clock_factor` equal to its own, and the client body's `origin_domain`
equal to its own.

### S-07 · low · `Outbox::parse` reads `max_protocol_fee`, `fee` and `beneficiary` at offsets that assume an owner is set
`sol-util/src/hl.rs:201-219` (`at = 39 + 1032` hard-codes a 33-byte `Option<Pubkey>` owner; Hyperlane's
`Outbox { local_domain, bump, owner: Option<Pubkey>, tree, max_protocol_fee, protocol_fee }` writes one byte for
`None`).

Scenario: every path in this deployment keeps `owner = Some(...)` (kind 9 transfers to `Some(successor)`), so the
bug is latent; a future owner-less mailbox, or a successor that burns ownership, would make `finish_setup` and
kind 7's `max_protocol_fee` check read 32 bytes into the merkle tree and refuse or mis-accept fees.

Fix: compute the tree's offset from the owner tag (`6 + 1` or `6 + 33`), as `token_ism` already does for the
token account.

## Checked and held

- Account checks on every instruction: PDA seeds and bumps, owner, signer and writable flags, the system program
  through `create_pda_account`, the Instructions sysvar through `load_instruction_at_checked`; duplicate accounts in
  cancel pairs are no-ops after `close_account` reassigns to the system program.
- ed25519 introspection: program id, `num_signatures`, every index the precompile's own or `u16::MAX`, every offset
  and the message length inside the instruction's data; entries bound slot by slot to the staged precommit's own
  round and time, the header's height and the commit's BlockID, and to the set's pubkey at that index.
- gno-light-core against tm2 at `5d9c678`: the 16 leaves and `bytesOrNil`, the canonical vote (fixed64 height and
  round, uvarint time), the validator leaf and address order, the commit rule in i128 with powers checked at the set
  (positive, unique, total ≤ MaxInt64/8), nil precommits never tallied, `SimpleProof` matching `computeHashFromAunts`,
  `/tm.Event` = gno's `chain.Event {type, attrs, pkg_path}`; 17 real heights confirm hashes, signatures and
  LastResultsHash.
- Client state: trusted height only rises; NextValidatorsHash followed in order with the announcing header required;
  quarantine `max(old, now + 7d)`; the 256 ring cleared at a set change; the record continues across kind 11; stages
  keyed by (submitter, hash), rent returned, pre-funded addresses handled, strangers close only after 7 days;
  `Verify` only for the current set at height ≤ trusted, leaf ≤ 8 KB, origin and all seven attributes matched;
  reset and set-admin by the admin PDA alone; `init` gated on the programdata's upgrade authority.
- Pausable ISM: flags with their slot, `holdSince` kept through a dip under 24 h and ended by one of 24 h, the
  quarantine read from the fixed-seed state with owner and version 1 pinned, `Verify` deriving its own accounts and
  refusing another client or state, unpause refused under a hold, replacement through any hold, layout fixed.
- Admin program: `Handle` only under the mailbox's `process_authority` for this recipient (seeds verified against
  Hyperlane's `pda_seeds.rs`; the mailbox passes the deliverer's signer flag through), 9-byte prefix, ≤ last ignored
  and > last+1 refused, unknown kinds spent, parameters parsed at execution with every failure spent, 24 h / 7 d /
  21-day floor written once and binding only under a hold, cancels executed at recording with clamped ranges and a
  self-allocated digest record, recovery digest bound to chain id, program id and the counter it will take, the
  settled set's > 2/3 by power, voiding by marker, kind 8's hash over the buffer's ELF with 10 KB extend steps and
  the writer as refund key, kind 9 refused for a mutable successor and moving all nine authorities, kind 10 above
  the high-water mark, the authority PDA writable for the token's realloc.
- Arithmetic: `overflow-checks = true` in release; saturating clock sums; i64 power sums checked; no realloc past
  10,240 per instruction.
- Compute: 60k CU per 5-validator update, 126k per delivery, 164k per 271 KB upgrade — all measured on the SBF
  builds; a 255-validator set hash and tally stay under ~90k CU.
- The clock-factor feature reaches each program's own constant and the stored factor byte; nothing else.
- Hyperlane constants: token instruction numbers 3/5/7, mailbox 2/9/11, Inbox and token layouts, `get_recipient_ism`
  defaulting, the per-ISM process authority ignored safely.
- Tests' blind spots beyond S-02: no transaction-size enforcement under program-test; no hold-with-rotations case
  (S-01); no malformed precompile instruction (other index, outside offset); the token accounts are hand-written,
  not Hyperlane-initialised with Token-2022 mints; the Clock is set by hand, never voted; no 16-pair cancel with a
  lookup table.

## Verdict

The core matches tm2 byte for byte on the chain's own blocks, and the account and signature checks are sound; I
found no way to forge a header, a proof or a decision. Two findings are above medium and both are about the
validator set's size and history, not signatures: the recovery under a leaked set can be starved of a settled set
(S-01), and the staging path serves about nine validators where the plan promises a hundred (S-02). Both need code
before launch; S-03 needs a one-line rule. The rest are setup guards. Not converged this round.

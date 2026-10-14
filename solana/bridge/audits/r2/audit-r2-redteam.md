# Audit round 2, audit 1 — the plan red team with the code in hand (PLAN.md v29, branch `bridge` at 22d28c8, 2026-10-13)

Read: PLAN.md §1–§11 and §13; AUDIT.md; M1-, M2-, M3-NOTES.md whole (their audit-round-1 sections included); REVIEWS.md
rounds 21–22. Code read where a finding or a regression line rests on it: `programs/admin-program/src/{state,processor,
recovery,kinds,config}.rs`, `programs/sol-util/src/{guardians,client_head,clock}.rs`, `programs/pausable-ism/src/
{processor,state,config}.rs`, `programs/gno-light-client-ism/src/{state,config}.rs` and its `update_finish`/`reset`,
`agent/{identity,signer,executor_sol,settle,relayer_sol}.go`, `cmd/{hlsol,hlwatch,hlrecover,hlbridge}` command tables,
`realm/hlmailbox/gov.gno`, `realm/hlgov/hlgov.gno`, `realm/p/hlroute/v0/hlroute.gno` (`Credit`, the sweep queue), and
gno v1.2.0's `r/gov/dao/proxy.gno` and `impl/v0/govdao.gno` from the module cache (no network). Read-only; nothing built
or run. Not spent on: r/hlgov's unbuilt kind-12 builder; the unbuilt heap frame.

## Findings (the AO series)

### AO1 — medium — "anyone executes it then" has no hand but the operator's

Plan: §6.5 "matures 7 days after delivery, and anyone executes it then"; §1 "anyone, a GovDAO member with the published
tool included, can update the light client, relay, re-emit and claim".

Code: the only sender of the admin program's `Execute`, and of the loader's `ExtendProgram` a growing upgrade needs, is
the operator's relayer (`agent/executor_sol.go:74–181, 209–215`). The published tools send none: `hlsol`
setup|audit|keygen|send|balance|close-stage|flag|successor-init (`cmd/hlsol/main.go:52`), `hlwatch`
status|watch|flag|unflag|finish|version (`cmd/hlwatch:51`), `hlrecover` status|body|init|sign|submit|record|close|settle
(`cmd/hlrecover:59`) — `hlrecover` records a recovery decision and cannot execute it.

Scenario: GovDAO (moul and g1aeddl) passes a replacement of the operator's two seats, which hold the admin program; it
lands on the hold's 21st day — if someone sends `Execute`. The operator stops its relayer the day the proposal passes.
Day 21, 22, 30: the decision sits mature; the hold stands; §1's "a hold lasts until GovDAO replaces the holders" is
false as built. The same hand executes the validators' recovery decisions (kind 9, 11, 5) against "a GovDAO that is
the problem" when that GovDAO pair includes the operator. The way out is a member running `hlbridge relayer` with their
own keys, which no document says and §1 does not list.

Fix: an `execute` command in `hlrecover` (any pending decision by channel/number, the kind's accounts from
`sol.Admin.ExtraFor`, a `NeedsExtend` answered with one `ExtendProgramIx` step), funded like the guardians' keys; §1 and
§6.5 name it as the published tool's duty beside updating and relaying.

### AO2 — medium — a forged sweep entry halts the signers for good: the queue has no void

Plan: §5.6 "a stop clears itself once the identity holds at a height at or above T, as it does after a forgery is
voided as forged"; §5.4 "A burn below the fee, like a burn to a realm address (§5.2), is credited to the sweep queue —
counted as owed, paying nobody until a GovDAO proposal directs it."

Code: `Credit` books a swept amount into `Owed` (`hlroute.gno:465–469`: dust, a realm-address or non-gno recipient, an
amount above the lock); the void and restore builders walk `claims` only, and `Import` copies `Sweeps` whole
(`hlroute.gno:1361, 1378`); the signers halt on `supply + uncredited > lock − owed − voided + lost` and resume only when
it holds (`identity.go:80`, `signer.go:324–328`). M1-NOTES and AUDIT.md name the gap.

Scenario: Two compromised signer machines (or three lying providers, or a verifier bug) sign one burn that never
happened, of lock + 1 GNOT, or of 3 GNOT to r/hlfees' address. `Process` credits it: swept, owed up by that amount, the
identity broken by it on every honest reading. All three signers halt — the honest one reads the same counters — and
the watch keys set `credits` and `payouts`. GovDAO's closure of §1 ("void the forged claims … reopen payouts") cannot
name a sweep entry; nothing drops the amount from owed; the signers never resume. Every exit is a credit by vote until
`migrate` to a successor realm whose `Import` is hand-patched to drop the entry: new code, a review and 7 days.

Fix: extend the void builder (and a guardian's `payouts` flag's ids) to sweep entries by message id — as withheld
(booked, restorable) or as forged (dropped from `Owed` and `Sweep`) — under §5.3's existing at-once rule; `Import`
unchanged.

### AO3 — low-medium — a rotated-out set keeps the recovery until someone sends `Settle`

Plan: §6.1 "A set GovDAO has voted out loses this power once the set replacing it has settled"; §11.9 "never hold
through the quarantine of a rotation that removes leaked keys — lift, so the new set settles and the leaked one loses
the recovery".

Code: a recovery is signed against the record alone (`recovery.rs:253–257, 327`); the record moves only when `Settle`
is sent — by the operator's updater and executor each tick (`settle.go`) or by `hlrecover settle` — and `settled_set`
does not block an entry for a hold begun after its quarantine's end (`recovery.rs:79–84`).

Scenario: GovDAO rotates out leaked keys; the guardians lift as ordered; the quarantine ends on day 7. The operator's
machines are down (a float ran dry, a provider outage — §1's "its absence delays") and nobody else sends `Settle`;
hlwatch prints SETTLE DUE to two people. The updater being down, no header lands either: day 21 the client lapses. The
thief, holding the old keys — still the recorded set — records a lapse-opened kind 9 to a successor of its own; no hold
stands, nothing defers it; it lands day 28 unless a member's replay tool submits one header before then (a fresh header
voids it). The live validators cannot sign anything.

Fix: §6.1 and §11.9 say "recorded", not "settled": the rotated-out set loses the recovery, and the assent's 30 days
start, at `Settle`; hlwatch's SETTLE DUE becomes a standing order for either outsider (`hlrecover settle`, any funded
key), and the monitor alarms the members' channel while it stands.

### AO4 — low — the validator-set bound is unwatched; its first sign is a wedged client

Plan: §6.1 "a next set of about 50 can be followed through any history, 60 with a settled one, 100 for a single change
… the heap frame that lifts the bound toward 255 is the next build item, due before gno.land nears 50 validators."

Code: the updater alarms `updater:body-bound` only when a body region reaches `MaxBodyCap` (`updater.go:427–432`),
the 255-validator bound of the account, not the heap's; nothing in the updater, monitor, hlwatch or audit counts
validators or compares an announced set with the measured limits (M2-NOTES: 60 with a full history dies in
`UpdateFinish` with "memory allocation failed").

Scenario: 2028, gno.land at 58 validators after a year of one-at-a-time additions, four of them within a month so the
history holds four unsettled entries. GovDAO adds the 59th. The updater stages the announcing header; `UpdateFinish`
dies in the heap; the stage stays, the client never follows the rotation, every later send is unprovable; after 14
days a lapse opens and the recorded set signs a reset of 60 — the cure, drilled nowhere, reached with no warning
before the break.

Fix: the updater, monitor and hlwatch alarm when `/validators` or an announced set exceeds 40 (`validators:near-bound`),
and `hlsol audit` prints the count against the measured bound; §6.1 names the alarm as the trigger for the heap-frame
build.

### AO5 — low — the day bucket is a griefer's lever, and a send above it strands rather than waits

Plan: AUDIT.md "Honest inflow above the day's bucket waits for the next day"; §5.4 "A per-message maximum of 25,000
GNOT and 25,000 BUBBLE (a rail against fat fingers, GovDAO-settable) is enforced at send on gno.land".

Code: `MintLimit::charge` refuses past the cap and drains linearly (`pausable state.rs:38–57`); the relayer parks a
refused delivery after three failures and retries hourly (`relayer_sol.go:314–345`); the realms' maximum and the ISM's
limits are set by different builders with no check between them; kind 12 accepts `max > day_cap`.

Scenario: A griefer sends 1,000 transfers of 100 GNOT a day (fees 5,000 GNOT ≈ $345; the lock returns through the exit
at the meter's rate). Each day's bucket is full within an hour; as it drains, a thousand parked griefer messages retried
by backoff take most of the room, and an honest 5,000 GNOT send waits days. Separately: GovDAO raises the route's
maximum to 200,000 (7 days) without a kind 12; a 150,000 send leaves gno.land, is `OverDayCap` forever, parked hourly
for good — "waits for the next day" is false above the cap, and the sender's exit is `migrate`'s refund by id.

Fix: kind 12's builder refuses `max > day_cap`, the route's maximum builder refuses a value above the ISM's day cap
(a compiled constant GovDAO moves together), and §9 states the griefing cost; the relayer retries `OverDayCap` parks
oldest-first when the bucket's level, read from the pausable state, has room.

## Regression — AN1–AN10

- AN1 (the settled set evicted): fixed — the record `settled` (`admin state.rs:29`), `settle`/`record_settled`/
  `settled_set` (`recovery.rs:79–134`), `record` signs against the record (`recovery.rs:327`), the client evicts only
  below its newest clock-settled entry (`client state.rs:102–113`); §6.1, §6.4. Holds; AO3 is its residue.
- AN2 (signers and monitor blinded inside the allowance): fixed — `signer.go:253–261` judges the identity at the head
  inside the allowance, `judgeAhead` past it; the monitor's `judgeOwnHead` (M3-NOTES G3); §5.6, §5.9. Holds.
- AN3 (a burned forged mint): fixed — `SignerStops` counts `InflightIn` (`identity.go:80`), filled from the index for
  burns at or before the snapshot's slot and uncredited at the head (`signer.go:296–301`); §5.6. Holds.
- AN4 (the floor written by the operator's hand): fixed — `holds`, `push_hold`, `replacement_floor` (`admin
  state.rs:36–114`), `note_hold_end` on every flag change, unpause and replacement (`processor.rs:454–465`), the floor
  computed at `Execute` (`processor.rs:642–646`); §6.5. Holds (the merge rule checked below).
- AN5 (nothing bounds a forged mint): fixed — `charge` (`pausable state.rs:45`), `verify` (`processor.rs:306–311`),
  `require_delivery` (`processor.rs:211–219`), kind 12 (`admin processor.rs:871–882`); §1, §13. Holds; AO5 is the cost.
- AN6 (256 ids): fixed in M1c — `hlroute.Range`, `heights:`/`since:` for the flag and the void (M1-NOTES AN6); §5.3.
  Holds (restore and credit stay lists, as the note says).
- AN7 (the cancelled store jammed): fixed — `add_cancelled` merges when full (`state.rs:135–169`), pruned at zero
  pending (`processor.rs:541`); §6.4 "spans from 1". Holds.
- AN8 (kind 10 to 2^64−1): fixed — `SET_SEQUENCE_REACH` (`config.rs`), the spend (`processor.rs:890`), the checked add
  (`processor.rs:349`); §13 kind 10. Holds.
- AN9 (the seat mirror refuses): fixed — `hlgov.gno:429–452` warns in the description; §5.3. Holds.
- AN10 (a front-run init): fixed in order — setup and audit to PASS before any realm, each Hyperlane init in the first
  slot with retries (M3-NOTES AN10); §5.8. Holds; the private RPC path is a deployment setting, not code.

## Checked and held

- The Instructions-sysvar binding: a CPI wrapper, a direct `Verify`, a foreign mailbox and a second `InboxProcess` of
  the same message all refuse (`processor.rs:211–219`); a charge reverts with a failed `handle`; stock relayers skip
  the route (`Type` 0), so the writable pausable state is ours alone.
- The hold history: a ninth hold's merge keeps the earliest start, but `replacement_floor` counts an ended hold only
  with `end ≥ delivery`, so churn before a delivery lowers no floor and churn after it needs the honest flags down —
  §1's own concession; a dip under the grace keeps the floor; four holders cost one extra week at most, two replacements
  pending together none.
- The recorded settlement under a hold: four forged rotations evict the launch entry from the client a week on and the
  record keeps it; a hold begun after a quarantine's end blocks nothing (§11.9's order); kind 11 assigns the record
  directly, so a reset to a lower height still wins; a hold ending without a reset settles the forged entries — the
  abandonment §1 states.
- The signers: an eclipsed signer node agreeing with a forged client is braked by the monitors' third-source rule on
  `credits`; a burn credited past the head is counted as uncredited at the head, consistently.
- `ExecuteOrRejectProposal` at v1.2.0 (`impl/v0/govdao.gno:119–176`): `PreExecuteProposal` marks Accepted before the
  executor runs, an error denies, a panic reverts the mark; a vote-less call panics ("didn't reach supermajority"), so
  nobody kills an open proposal; the relayer's call from first sight never comes early.
- `successor_init`: a successor never initialised, or initialised to another predecessor, fails kind 9's CPI whole —
  the transfer stays pending until GovDAO cancels it, nothing moves half-way; one burned before its init is the same;
  `hlsol successor-init` orders init then burn.
- Kind 10's reach, the checked add and the clamp at `last_seq`; the two-group pure removal never removes a partner at
  once (R2); two guardian seats per proposal on both chains, the void's option included (`gov.gno:300`, `replace`).
- Chunked staging: keyed by submitter and header hash, chunks by the submitter, the finish checks the whole set's hash
  and length, strangers close after 7 days; a griefer's `Grow` to 80 KB costs it ≈0.4 SOL and the ring only its
  shortcut.
- The economics: 7.36 + 8 against 8.95 of rent, 0.15 of state and 5.2 of floats and keys leaves ≈1.06 of margin as §11.4
  says; unbudgeted and small: `ExtendProgram`'s rent on a growing upgrade (0.052 SOL per 10 KB step from the
  relayers' float) and `Settle`'s growth past 128 validators.
- The leak walk with the limits: a forged delivery in the minute before the flags mints at most 100,000 of each token;
  the pool's depth is the bound below that (§11.11); a forged kind 12 or unpause meets the 7-day and hold rules.
- The relayer's walk: a parked `OverMax`/`OverDayCap` message blocks nothing behind it (`relayer_sol.go:190–237`
  marks only the first undone), so a stranded send is its own.
- The lift and the window: a thief's hold-opened recovery kind 5 landing on day 21 voids its other hold-opened
  recoveries (`window_stands`) while its held mailbox decisions wait for the hold's end — the stand-off as §1 states
  it, a machine race on daily range cancels.

## Verdict

Honest for a GovDAO member on every ending §1 promises, with two qualifications the member must hear: as built, the
only machine that executes a matured decision on Solana — GovDAO's replacement of the operator's holding seats, the
validators' override, a growing upgrade — is the operator's relayer (AO1), and a forged entry in a route's sweep queue
closes the automatic exit for the deployment's life, since no void reaches it (AO2). Both are buildable without a new
rule: AO1 is a command the plan already describes as "anyone", AO2 is §5.3's void by message id reaching one more
ledger. AO3–AO5 are stated limits to write down and small guards. Round 1's ten findings hold in the code and the text.
Nothing above medium remains from this audit; AO1 and AO2 should fold before the next round, AO3 with them if the
realms lane is open.

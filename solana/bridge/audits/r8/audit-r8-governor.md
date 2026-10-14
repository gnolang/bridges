# Closing check — the governor and the yield (plan v36; the code of M1k and M3l) — 2026-10-14, on ee8a9074

Scope: security of the new entry (`Govern`, `Yield`, `launchGovernor`), conformance of plan v36's governor sentences
against the realms, the Go and the e2e, and regression of the closed loop's rules. Read first: AUDIT.md (head, the
design-change block, the final check), PLAN.md v36 (glossary, §1, §3, §5.3, §5.8, §5.9, §6.6, §8, §9, §11.16),
M1-NOTES M1k, M3-NOTES M3l, gno-interrealm-v2.md and gno-security-guide.md in full (spec §2, §3.2, §4.1–4.3, §5.1–5.5,
§8.2, §9, §11.3, §12; guide §2.1–2.4, §3, §5.1–5.8, §8), then the pinned gno v1.2.0 where a claim rested on it
(r/gov/dao types.gno and proxy.gno, impl/v0/govdao.gno; gnovm uverse.go `realmIsUserRun`, the keeper's `AddPackage`
path rule, `chain.PackageAddress`). `python3 scripts/check-interrealm.py`: hl*, hlgnot2 and p/ clean; its one line is
realm/bubbletreasury/treasury.gno:223 (known). No builds, no validator, no e2e, no network, no test run: the only
commits that touched `realm/` since 852ddc35 are M1k's four (12f9c8f7, 740dfaaa, a5a144ff, 514f500b), so the realm
suites at this tree are the ones M1k ran green; M3l's vet, race and the 741 s e2e ran on the merged tree.
Severities as AUDIT.md defines them (high / medium / medium-low / low). Known-and-queued items spent no finding.

## Findings

### GV1 — medium-low — the plan says a GovDAO proposal made with a builder is "refused at creation"; the code refuses it only when executed, so GovDAO can pre-arm decisions against the yield, and the plan never says so (nor that the relayers' finisher waits for the yield)

Words: PLAN.md §5.3, the who-decides rule: "a GovDAO proposal made with a builder is refused at creation (the
builder panics, as it does before `Launch`) and by its closure if one were made"; §5.3 later: "the relayers and
`hlwatch` call it [ExecuteOrRejectProposal] for any builder-made proposal past its 14 days" — nothing about the yield.
Code: realm/hlmailbox/gov.gno:118-139 (`propose`/`request`: any caller gets a `Request`; nothing reads the yield when
a builder runs), :97-108 (`mustBeGovernor`, the one refusal, inside the closure, at execution); the same shape in
hlgnot.gno:409-444, hlbubble.gno:367-402, hlgnot2.gno:427-462, hlfees.gno:111-146, hlgov.gno:201-212 and :349-366.
The e2e makes it plain: bridge/local/e2e.sh:575-603 creates and votes a GovDAO proposal before the yield, hlwatch
lists it "PRE-ARMED: has the votes; awaits the yield", its execution is refused, and :621-628 executes it as GovDAO's
first decision the block after the yield; hlwatch's own alarm says "a pre-armed proposal may execute" (main.go:1349);
the finisher's gate is relayer.go:917-919 (M3l), unstated in the plan.
Scenario: before the yield two members pre-arm an `unpause`, a seat removal or a `void` naming flagged ids (at-once
kinds), or a `migrate`, `rotate signers` or `set verifier` (7-day kinds) — all GovDAO's lawful powers from the yield.
A governor who trusts §5.3's sentence expects nothing can stand armed against his `Yield`, yields on the vote, and the
at-once ones apply within a block while the 7-day ones are now GovDAO's alone to cancel. hlwatch shows them, but no
step of §11.16's order says to read that list before `Yield`. Nothing here exceeds what GovDAO may do after the
yield by a vote; what the wrong sentence costs is the governor's one chance to decline.
Fix: rewrite §5.3's sentence — "a GovDAO proposal made with a builder is created and voted like any other, listed by
`hlwatch` as PRE-ARMED and 'awaits the yield', and refused when executed: the closure panics before anything, so the
proposal survives, within its 14 days, and may execute the block after the yield"; add to §11.16's order, before
"his `Yield`": "read hlwatch's pre-armed list and yield only to proposals he accepts"; add the finisher's gate to
§5.3/§5.7 (the relayers read nothing of r/gov/dao until the yield and count a proposal's 14 days from it).

### GV2 — low — texts that name GovDAO for an act that is the governor's before the yield

Lines: realm/p/hlroute/v0/hlroute.gno:1619 (`": applied by GovDAO's own apply refund decision after, never by
Execute"`), :1629 (`"applied by GovDAO's apply refund decision "`), :1649 (Execute's refusal: "a refund is applied by
GovDAO's own at-once decision (apply refund) …"); realm/hlgov/hlgov.gno:914 (the page title "# GovDAO's decisions for
Solana"); bridge/agent/relayer.go:871 ("awaiting GovDAO's apply (an apply-refund proposal, at once; …)" on the
relayer's status line). M1k kept them knowingly ("hlwatch and the Go tests match them"); hlwatch itself names the
applier by the yield (main.go:957-963, tested).
Scenario: in the testing phase the governor refunds imported sends on a successor and applies the refund by Govern;
the successor's decisions page then reads "applied by GovDAO's apply refund decision 8" two lines under "governor: g1…
(until the yield) — … gno.land/r/gov/dao's proposals are refused"; §6.6's member, reading "every decision the governor
made" on the pages, sees GovDAO credited with an act it could not make, and the relayer's status waits on "GovDAO's
apply" for an apply only the governor can make.
Fix: neutral words in the three hlroute strings, the relayer's line and the hlgov title — "applied by the apply-refund
decision N (the governor's by Govern until the yield, GovDAO's after)", "Decisions for Solana" — and the test strings
that match them (hlgnot_test.gno:826/832/836/861, hlgnot2_test.gno:539, hlroute_test.gno:831, hlwatch's round-5 and
round-7 tests, TestRefundIsNeverExecutedByTheRelayer) moved with them.

### GV3 — low — before the yield nothing can deny a stale builder-made proposal, yet hlwatch says the relayers do, and `hlwatch finish` has no yield gate

Lines: every closure checks `mustBeGovernor` before `decisions.Expired` (gov.gno:123-128, hlgnot.gno:423-432,
hlgov.gno:355-360, hlfees.gno:131-140), so before the yield a GovDAO execution panics before the deadline error can
return and deny; hlwatch main.go:1063 prints "PAST DEADLINE … only ExecuteOrRejectProposal denies it for good (hlwatch
finish; the relayers do it for a pre-armed one)" whatever the yield says; main.go:1798-1829 `finish` calls
`ExecuteOrRejectProposal` with no `DAOGoverns` check; relayer.go:917 reads nothing of the DAO before the yield.
Scenario: a member's builder proposal is 15 days old before the yield; hlwatch shows it PRE-ARMED, "awaits the yield"
and PAST DEADLINE with "the relayers do it"; a guardian runs `hlwatch finish -pid N`; the chain refuses with the
governor's words and the gas is spent; after the yield the relayers deny it 14 days later (first seen at the yield).
Harmless for the lock (the deadline error returns once the yield lets the closure run); wrong for the operator.
Fix: when `!v.Mailbox.DAOGoverns()` hlwatch prints "PAST DEADLINE … denied after the yield (by `hlwatch finish`, or by
the relayers 14 days after it)" and `finish` refuses before the yield with the same line (one check of the mailbox
view in `finish`); one clause in §5.3 beside the relayers' call.

### GV4 — low — that a GovDAO proposal refused before the yield survives on chain is proven by reading, not by a run

Lines: e2e.sh:317-320 (`run_refused` asserts no "OK!" and the words — gnokey's simulation refuses, nothing is
broadcast) and :599-602 (the decision pending and the proposal pre-armed after it — trivially, since nothing landed);
realm/hlmailbox/mailbox_test.gno:124-137 (`refusedByDAO` executes with `dao.ExecuteProposal`, not
`ExecuteOrRejectProposal`, and the harness keeps state past an abort). The reading itself holds: the pinned DAO
cross-calls the closure (r/gov/dao/types.gno:232 `e.callback(cross(cur))`), impl/v0/govdao.gno:162-166 marks `Denied`
only on a returned error, proxy.gno:243-269 rejects only on an error, and the closure's panic crosses the executor's
boundary, which aborts the transaction before any mark (spec §9); a failed transaction writes nothing.
Scenario: none harmful today. The claim "survives for a retry after the yield, within its 14 days" (M1k, hlwatch's
text, the relayer's gate) rests on the DAO's code at v1.2.0; a later DAO impl that recovered executor panics and
marked the proposal denied would leave the relayers and hlwatch waiting on a proposal already dead, and no test here
would go red.
Fix: `refusedByDAO` in the six suites calls `dao.ExecuteOrRejectProposal` and asserts the proposal's status is still
ACTIVE afterwards (one line each); the e2e broadcasts the pre-yield execution once with `-simulate skip` and reads the
proposal's status from r/gov/dao's page after the failed transaction.

## Checked, no finding — the security of the new entry, question by question

- The governor's identity is read from `cur.Previous()` of the realm's own live `cur` in every realm and never from a
  passed value: `mustBeGovernor(cur)` (gov.gno:97, hlgnot.gno:409, hlbubble.gno:367, hlfees.gno:111, hlgov.gno:201,
  hlgnot2.gno:427) and `Yield` (gov.gno:186-189); `Govern` takes `cur` and a `Request`, no `rlm`; the checker is clean.
  `prev.IsUser()` is, at v1.2.0 (uverse.go:1655-1663), `path == "" || realmIsUserRun(addr, path)`, and
  `realmIsUserRun` (uverse.go:681-687) is exactly `pkgPath == "<domain>/e/<addr>/run`, the path the keeper itself
  builds from the signer (keeper.go:1530) and whose `PackageAddress` is the signer's (chain/address.gno:15-20); the
  keeper refuses an `addpkg` to any path that is not `/r/` or `/p/` and reserves the run path (keeper.go:832-840,
  mempackage.go:81-97). So `IsUser() && Address() == launchGovernor` holds for the governor's MsgCall and his own
  MsgRun and for nothing else: no deployed realm, no sub-realm (`IsUser` false, spec §5.5), no stranger's script.
- A stranger cannot reach a builder's closure through `Govern`: a `Request` is an `/r/`-declared struct with
  unexported fields (construction and conversion guards, spec §3.2, §8.2); a builder is callable by anyone but its
  closure refuses everyone but the governor (before) or the proxy's exact pkgpath (after); a zero `Request` is refused
  ("not a builder's request"); a realm calling `Govern` on a user's behalf, by `cross(cur)` or a sub-token, presents
  its own pkgpath and is refused (TestLaunch: a stranger's, a code realm's, a zero request); a non-crossing call of
  `Govern` from another package is rejected by the VM (spec §5.1); the `dao.ProposalRequest` a stranger can take from
  `req.Proposal()` keeps its executor unexported and `SimpleExecutor.Execute` admits only r/gov/dao's own frame
  (types.gno:214-233), so the only thing to do with it is a member's `MustCreateProposal`.
- GovDAO's proxy before the yield: refused by the closure (`!prev.IsUser()`), nothing recorded (TestGovernor and every
  realm's TestYield; the e2e at 327 s). The governor after the yield: `Govern` refused in every realm
  (`DAOGoverns` true), the closure refuses him as anyone (`prev.PkgPath() != DAOPath`), and the only path to a closure
  is a voted proposal (TestYield in six suites; the e2e's `govern_after`).
- The yield is one-way and one for all: `yielded` has one writer (gov.gno:190), `Yield` refuses a second call, a
  stranger, a code realm and a `""` governor; the five realms read `hlmailbox.Yielded()` on every decision, a
  successor included (hlgnot2.gno:429, :474); a realm cannot disagree, since each `init` refuses a deploy whose
  constant differs from the mailbox's (hlgnot.gno:85, hlbubble.gno:76, hlfees.gno:41, hlgov.gno:140, hlgnot2.gno:87 —
  the compile-time check holds at `addpkg`, the e2e swaps all six or fails to upload); a successor deployed after the
  yield compiles the same constant and finds `Govern` closed from its first block.
- `Govern` runs the same closure as GovDAO's path with the same tests, queue, delay and record: the DAO cross-calls
  the closure (types.gno:232), so under GovDAO it runs in its own frame of the realm with `Previous()` = r/gov/dao;
  under `Govern` it runs non-crossing in Govern's frame with `Previous()` = the governor — the same `CurrentRealm`
  either way, so every `cross(cur)` inside (hlgov's `Dispatch` and `NoteEnrolled`, hlgnot's `env(cur)` into the
  mailbox) presents the realm itself to hlmailbox in both paths. Per builder: hlmailbox's `propose` (the launch's own
  closure too), hlfees' `propose`, hlgnot's/hlbubble's/hlgnot2's `propose` (mustBeLive unless cancel, the payouts
  pause for an apply refund, `st.Decide`), hlgov's `propose`/`proposeKind` — one closure each, `mustBeGovernor` then
  `Expired` then the effect; nothing skipped, nothing doubled (Govern's own `mustBeGovernor` repeats the closure's
  with the same `cur`; one ledger record, one event). The deadline is met by construction (`made` and `t` in one
  transaction).
- `Execute(id)` stays anyone's: `caller(cur)` only (gov.gno:631, hlgnot.gno:602, hlgov.gno:885, hlfees.gno:204); the
  e2e lands the governor's queued swap by the relayer's own key (:860-870). The apply refund is the governor's at-once
  act by Govern, the route's `Execute` refuses a refund (hlroute.gno:1647-1650), the relayers skip one
  (relayer.go:843, TestRefundIsNeverExecutedByTheRelayer), hlwatch names the applier.
- Renders and json are truthful: `GovernorLine`/`GovernorJSON` read the live constant and `yielded` (decisions.gno:54-67;
  render.gno:154, :294; each realm's page and json), tested in every suite and read by the e2e on five realms.

## Conformance — plan v36 against the code and the e2e

Every sentence naming the governor, `Govern`, the yield, the launch, the yield vote, the testing phase, the notice
and §11.16's order was read against the code: the glossary, §1 (the trust statement, "every builder of §5.3, `Launch`
and r/hlgov's decisions to Solana are his direct call"), §3.1–3.5, §4's table, §5.3's who-decides rule, §5.8's hlwatch
line, §5.9's acceptance items, §6.6, §8.3 and §8.6, §9's first row, §11.16. All hold at the code's lines above, with
GV1's one false sentence. hlwatch's governor line (main.go:986) is the realms' word for word; "awaits the yield"
(:1059, :1359) and the yield's alarm (:1349) are tested (TestWatchPrintsTheGovernor); the finisher's gate
(relayer.go:917-924, TestFinishProposalsAwaitTheYield) is as M3l reads it. The e2e's new sections prove, by numbers:
the governor's `Launch` by MsgRun (`"governor":"<test1>","yielded":0`, live); hlfees decision 1 queued with
`due − recorded = 600` and later `applied ≥ due` by the relayer's `Execute(1)`; hlmailbox decision 2 at once
(`due == recorded`, seat 4 "(empty)") and 3 queued 600 s; r/hlgov's decisions 1 (kind 7), 2 (kind 12) and 3 (the
cancel, salt non-zero) recorded on the admin program with both counters equal; a GovDAO proposal pre-armed with
"awaits the yield", its execution refused with the realm's words (by simulation — GV4), no relayer line
"ExecuteOrRejectProposal sent"; `Yield` by MsgCall with `yielded = H > 0` on all five realms' json, a second `Yield`
and a `Govern` refused with the height; the proposal "anyone may execute it", the finisher "since the yield at height
H"; GovDAO's first decision cancelling hlmailbox 3 (decision 4 applied); the hlwatch green check with "gno.land/r/gov/dao
since height H". hlgnot2 staged, not uploaded (known).

## Regression — the closed loop's rules, by reading

The refund gate (`mustBeEnrolled` at creation and apply, hlroute.gno:1251-1262; TestRefundWaitsForEnrolment,
TestApplyRefund; kind 6's note and the cancel's clearing, hlgov.gno:690-693 and :464-473, TestCancelClearsEnrolment),
the dropped-route tombstones (`Dropped` on `Dispatch` and `Process`, mailbox.gno:363-370; TestRepointSuccessor
:1293-1365), kind 6's gates (hlgov.gno:652-680; hlgov_test :247-259, :914-932), `MustSend`/`MustTake` (hlgnot.gno:215,
:281; TestMustSend, TestMustTake, TestSuccessorAddressedBurnWaitsForTheFirstPage), the checkpoint (the six
TestCheckpoint… and TestRecordStartsFromACheckpoint), the line of migrations (TestMigrationLineIsFollowedToItsEnd,
TestRelayerFollowsTheMigration, TestSignerJudgesOverTheLine, TestToolsFollowTheSuccessor), the caps
(TestDayCapReservation, TestCapRatio…, TestLoweredCapReMarksThePark; hlroute's TestCapAndFirstDay, TestWorstDay), the
signers' rule (TestSignerStopsOnUncreditedBurns, TestIdentityVerdict, TestSignerRefusalAndResume,
TestSignerStopsWhenTheClientRunsAhead) — all present, their code untouched by M1k (which changed the builders' return
type and the identity check only) and by M3l (which touched no realm). The realms' own guards at creation still hold
for the governor (TestGovernor: nonsense refused "as for anyone").

## Verdict

Nothing high, nothing medium. The entry is sound under the Interrealm Specification v2 and the security guide: the
governor is an identity read from a live `cur`, forgeable by nobody without the key; `Govern` runs the one closure in
the realm's own frame; the yield is one write on one realm read by all; GovDAO is refused before it and the governor
after it; `Execute(id)`, the apply refund and the Solana side are untouched. Four findings: GV1 (medium-low) is the
plan's own sentence — the code never refused a GovDAO proposal at creation, so pre-armed proposals wait for the yield
and execute the block after, which hlwatch shows and the order of §11.16 should tell the governor to read; GV2–GV4
(low) are texts that still say GovDAO for the governor's own acts, hlwatch's deadline text and `finish` before the
yield, and a proof that rests on a reading of v1.2.0 rather than a test. The plan edits and the three small code
changes can fold by hand; none needs a new round. The tree at ee8a9074 is what M4 reads.

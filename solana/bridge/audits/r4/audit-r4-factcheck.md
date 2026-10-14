# Audit round 4, audit 2 — the plan fact check with the builders' notes (PLAN.md v31, tree da9567d + docs, 2026-10-13)

Read whole: PLAN.md v31, AUDIT.md (every "Changes from what Jae was told" bullet, rounds 1–3, checked against the plan), REVIEWS.md (preamble, rounds 21–22, the final fact check), the NOTES (M1f, M2f, M3g first), BUILD.md, RESUME.md, README.md, the r1–r3 fact checks. Live facts once each (c). Code opened where a note's number needed settling (named). No row on the heap frame, the one launch day cap, the seven-aunt proof or the mailbox's own body bound. IDs from C313.

## Regression: C284–C312

- C284 half: §1 and §5.3 say "refused when made"; §11.14 and §5.9's drill still say 7 days / queued (C324).
- C285, C286, C287 folded (§5.3). C288 folded (§5.6, §5.9). C289 folded (§6.2).
- C290 folded in §5.1; §8.3's list never got the declaring-realm check (C328).
- C291 folded (§5.9). C292 stated in §5.4 and §13; §1's "per token" survives (queued, no row). C293 folded (§6.5).
- C294 half: the pause and the watch's read are in §5.3; "the successor credits nothing" overstates (C317).
- C295, C296, C297 folded (§5.3, §5.5, §11.9).
- C298 half: §1 and §6.1 say "recorded by `settle`"; §9's leak row still says "once its replacement has settled" (C327).
- C299–C302 folded (§6.3; §6.3/§6.5; §2/§7.3/§13; §2).
- C303 not folded: §5.5 still "an estimate until it is built" (C326).
- C304 folded (§9). C305–C307 folded (glossary). C308–C312 folded (§5.3, §13, §11.11, §13, §5.8).

AUDIT.md's bullets, rounds 1–3: all in the plan but one of round 3's — "a turn above the lock skips instead of reverting" (M1f R26) appears nowhere (C318).

## (a) Readings in the notes' round-3 sections the plan does not state or states differently

**C313 (medium-low) — the body bounds and kind 11's real count.** Plan §6.1: "The client's count byte allows 255 validators, but the program's 32 KB heap binds as built"; §6.5: "a message shorter than 9 bytes fails `Handle`" — nothing bounds a body above. M1f: "`MaxDecisionBody = 600` bytes … a body no one can deliver would stick r/hlgov's sequence with its number untaken, in a form no program of ours cures"; M2f S-14: "`10_240 − 105` = 10,135 bytes of parameters … a kind 11 carries 252 validators with a 9-byte chain id, 250 with a 64-byte one (… §6.1's 255 was never true)". Change the plan (§5.3, §6.1, §6.5, §13): r/hlgov refuses a body over 600 bytes at creation and at dispatch; the admin program spends at recording, and refuses at `RecoveryInit`, parameters over 10,135 bytes; a reset carries at most 252 validators (250 with a 64-byte chain id) — the account's bound, which the heap frame will not lift; "255" goes. (Recomputed: (10,135 − 10 − 45)/40 = 252.)

**C314 (low-medium) — a kind 9 naming the admin program itself.** Plan §13 kind 9: "refused unless the successor's programdata account shows no upgrade authority"; §5.3 names a self-refusal for kind 8 only. M1f: "refuses a successor equal to the decision recipient … when the proposal is made and again in the closure"; M2f S-15: "its own programdata has no authority (burned in the setup step), so the immutability check alone passed it" — spent now; M3g: hlwatch "refuses a successor that is the admin program itself … and reads the successor's state before 'immutable': no state, a predecessor other than this admin program, or inherited already is named". Change the plan (§5.3, §13, §8.4): the builder refuses the recipient's own id, the program spends one, and "immutable" needs the successor's state to name this program as predecessor, not yet inherited — the burned authority makes "no upgrade authority" true of the program itself.

**C315 (low-medium) — what pends on a retired predecessor.** Plan §6.5: "once kind 9 has executed the predecessor records nothing more (`Retired`)"; "it has no close path for any program"; the successor starts "with no … pending decisions". M2f S-13: "Once kind 9 set `successor`, `CloseCancelled` closes any pending decision of either channel — the rent to its deliverer, a staged kind-8 buffer back to its refund key … — not only a cancelled one; `StageUpgrade` and `RecoveryRecord` answer `Retired`"; before, "the buffer (≈0.84 SOL …) and every pending decision's rent were stranded for good". Change the plan (§6.5): what pends on the predecessor at the transfer never executes and anyone closes it after; nothing is staged or recorded there again.

**C316 (low-medium) — a node's own minimum fee.** Plan §5.7: "an 'insufficient fees' or out-of-gas refusal re-priced and retried, never parked". M3g F14: "reads the ante's second refusal ('fee required: N with … as minimum gas price set by the node'), pays N once when under the bound — `gno.maxNodeFee` …, or by default the greater of ten times the fee the call carried at the block price and 1 GNOT — and otherwise refuses with the node's text in a form `Deterministic` parks". Change the plan (§5.7): the chain's price is re-priced; a node's own minimum is paid once under `gno.maxNodeFee` and parked above it — "never parked" is false for that refusal, and the operator sets a bound the plan never names.

**C317 (low) — vote credits during a migration's import.** Plan §5.3: "from that page until the last the predecessor refuses every function and the successor credits nothing". M1f R20: "the successor refuses `Credit` … a hand credit, a restore and a credit by vote are not gated — their places cannot collide once the counter is raised". Change the plan: "credits nothing by the verifier; GovDAO's hand credit, restore and credit by vote land during the import" — the watch (§5.9) reads the successor's counters as a pause and must expect owed to move.

**C318 (low) — lists skip; a dry lock skips.** Plan §5.3: "the void's test is made once, per id, in the transaction that executes the proposal, and applied there" (nothing on an id no longer voidable); §5.4: "skipping a recipient whose meter is dry" (nothing on the lock). M1f R22: "skip and count an id the void cannot take as it stands — voided by another decision since the proposal was made, paid in full under a void as withheld, or gone — … instead of a panic that blocks the proposal, its unpause option and its seat removals for its whole deadline"; the restore and refund lists the same. R26: "a dry lock is a skip, never a `SendCoins` revert that drops the cursor and stalls every recipient behind it". Change the plan (§5.3, §5.4): both, and that the leak ending's one closure applies its unpause and removals though an id is skipped.

**C319 (low) — the sweep payment's refusals.** Plan §5.3: "pay the named entries, by message id, to an address — never more than the lock holds; the paid entries leave the queue". M1f R19: "`DraftSweep(ids, to)` names entries (… up to 256) …; an entry voided or gone by execution is skipped …; a sum the lock no longer covers waits; a voided entry is refused by the draft ('restore it first')". Change the plan: a voided entry is paid only after its restore, an entry gone is skipped, a sum the lock no longer covers waits, 256 ids a decision.

**C320 (low) — an announcement is anyone's, once, for ever.** Plan §5.3: "the successor announces itself first (`AnnounceSuccessor`, its exact path), and the builder refuses an unannounced one". M1f R25: "from the successor's own code (`init(cur realm)` …): an `/r/` realm only, … once (… nothing is ever withdrawn) … the announcement proves a realm exists at the path and chose to be a successor". Change the plan: the announcement is the realm's own init, never withdrawn, proving existence and intent only; the member's reading of the source (§8) is the check. "Announces itself first" reads as GovDAO's step; it is a stranger's for the price of a deployment.

**C321 (low) — the ring epoch and the staged copy.** Plan §6.2: "One of the last 256 accepted header hashes … may stand in for the header and commit (a cost shortcut, no safety role; the ring is cleared at a set change)". M2f S-16: "a by-ring `StageInit` copies the entry's height and `LastResultsHash` into the stage under the body's ring epoch — a counter the body keeps, one up at every `Ring::clear` … Eviction costs a staged proof nothing; a set change or a reset still voids every ring proof staged before it"; the `Stage` +48 B, the `Body` +8 B. Change the plan (§6.2, §13): a staged ring proof survives the ring's churn (a stranger's 257 old-header proofs no longer void it) and dies at a set change or reset; the body carries the epoch, so §2's 21,160 B is 21,168 and §13's "head and body … fixed (version byte 1)" should date the body's shape to M2f.

**C322 (low) — the unindexed credit: "at once" is a second poll.** Plan §5.9: "a credited id that neither provider's dispatch index holds flags `credits` and `payouts` at once". M3g AP1: "once the id has stood one more poll — only when both scans succeeded with nothing skipped, a voted claim never". Change the plan: "on the second poll, when both scans are complete" — "at once" promises a flag no code sends and hides the case in which none is sent.

**C323 (low) — `hlwatch`'s forged-credit rule, two wordings.** Plan §5.8: "calls an id forged only when both its providers' scans find no dispatch record for it and the supply identity is short by at least that claim's amount". M3g AP1: "hlwatch prints FORGED CREDIT lines from the same reading [the unindexed-id rule] and flags its holder". One is loose; say which in §5.8.

## (b) Internal contradictions in the plan

**C324** §11.14 "a signer removal that would leave the verifier's rule unsatisfiable waits 7 days" and §5.9's drill "a rule-breaking signer removal queued for 7 days" vs §5.3 "a signer removal that would leave the rule unsatisfiable is refused when made" and §1 "(… refused when made, §5.3)" — C284's residues; the drill names the in-between case or goes.
**C325** §1 (the leak walk) "Within 1 minute the operator's watch sees the fork and flags", §6.1 "flags a mismatch within a minute", §9 "flags a header that differs from gno.land's within a minute" and "flags any mailbox-channel decision absent from r/hlgov's list, within a minute" vs §1 (the operator bullet) "two minutes for the client-ahead and header rules" and §5.9 "the flag waits two minutes like the record's" — four residues of C265; the leak walk's "nothing minted in the minute before the flags" is two minutes of §5.4's cap.
**C326** §5.5 "`Claim`'s share an estimate until it is built" vs §10 "`Claim` is built; its gas is measured on devnet, M4" (C303).
**C327** §9 "A set already rotated out is powerless once its replacement has settled (§6.1)" vs §6.1 "settled and been recorded by `settle`" and §6.4 "the record, not the client's view, is what moves the recovery's power" (C298).
**C328** §5.1 "an implementation is a type declared in the registering realm — the one gate the mailbox cannot apply, which §8.3's member checks in the source" vs §8.3's list, which names no such check.
**C329** §5.3 "may name a range … so one flag and one closure cover a flood" vs §5.3 "a range the scan finds holding more than 1,000 credits, voidable or not …, is refused — split by heights": at §10's 50 credits a day a range spans 20 days, at 500 a day two; one closure covers a flood only under 1,000 credits of every kind.
**C330** §6.5 "names the decision's refund key as the loader's spill" vs §6.5 "the refund key is the writer's by construction and the decision carries none" and §13 kind 8.
**C331** §6.5 "(a growing upgrade's extend steps included)" vs §6.5 "the executor sends the whole excess at once" and §6.3 "its one growth extension" — C300's residue.
**C332** §5.8 "Three tools (…): `hlsol setup` … `hlsol audit` … `hlwatch`" vs the glossary's "`hlrecover` the validators' recovery tool, which also settles and executes for any member" — four; and §5.8 pins `hlwatch`'s build hash in the launch notice, §7.3 the comparator's, nobody `hlrecover`'s.

## (c) Numbers recomputed (live, read once each: mainnet RPC at slot 456,270,204; rpc.gno.land/validators at height 764,805; r/gov/dao:10 and :13)

| Item | Plan | Recomputed / live | |
|---|---|---|---|
| Rent rate | §2 5,080/B; 0 B → 650,240 | 0 B 650,240; 1 B 655,320 → 5,080/B | ✓ |
| Deployer | 7.36 | 7,356,770,000 lamports = 7.3568 | ✓ |
| Validators | 6 × 60 = 360; forging 5 of 6 (241; any 4 = 240); override 180/180/240; halt 120 | six at 60; 3s > 720 → 241; 180/180/240 < 241; moul + Berty 120 | ✓ |
| Prop #10, #13 | #10 open, moul to 1: 301, 201, 181 (60%), 180/180/181, halt 61; #13 passed | #10 "open for votes", YES 33.33%; 201; 181 = 60.1%; all < 201; 61 < 101; #13 "ACCEPTED" 66.67% | ✓ |
| ELF sizes | §2, §5.8: 273,840 / 164,432 / 398,752 → 1.39 / 0.84 / 2.03 | M2f the same bytes; (size + 45 + 128) × 5,080 → 1.39199 / 0.83619 / 2.02654 = 4.2547 | ✓ |
| Rent sum | ≈9.0 | 1.07288 + 2 × 1.81822 + 4.2547 = 8.964; + six program accounts 0.005 = 8.969 | ✓ |
| Need, shortfall, margin, splits | ≈10.6; ≈3.2; ≈1.1; splits 8.0 | 8.969 + 0.15 + 1.5 = 10.619; − 7.357 = 3.262; 8 − 3.26 − 3.7 = 1.04; both splits 8.0 | ✓ at the plan's rounding (3.3 / 1.0 to the lamport) |
| State accounts (+8 B) | §2 0.15 = 21,160 B ≈0.11 + 6,814 B ≈0.035 + ≈0.01 | the body +8 (S-16): 21,168 B → 0.1082 (the suite's set of 4: 21,216 → 0.1084); admin 0.0353; the `Stage` +48 B = +0.00024 a staging | ✓; 21,160 stale by 8 B (C321) |
| Decision rents | 0.0014–0.002; cancel 0.0009 | M2f's 105 + params: kind 7 145 B → 0.00139, kind 5 238 B → 0.00186; 50 B → 0.00090 | ✓ to the rounding |
| Clocks at factor 8 | 24 h, 7, 14, 21, 28 d; dips 25/23 h → 3.1/2.9 h; 14 d in 42 h | 3, 21, 42, 63, 84 h; 3.125/2.875 h; M2f and M3g agree | ✓ |
| Blocks; slots | 176,327; 352,653; an hour 1,050; 24 h ≈ 390,000; 7 d ≈ 2.75M; an epoch ≈ 26 h | 176,326.5; 352,653.1; 1,049.6; 392,727; 2,749,091; 26.4 h | ✓ |
| Mint limits, base units | §13 25,000 a message, 100,000 a day; the cap min(100,000, lock/10) | pausable config.rs 25,000,000,000 / 100,000,000,000; hlsol setup.go `launchDayCapMax` 100,000,000,000; the e2e's audit "100000000000, 10.0% of it" | ✓ |
| Updater's float | 0.3 SOL ≈3.4 months at four 5,000-lamport transactions a header every 10 min; ≈23 days at 500/d; 0.086 SOL/month ($9.5); 0.39 | 144 × 4 × 5,000 = 0.00288/d → 104 d; 644/d → 23.3 d; 0.0864 = $9.47; 0.386; agent/floats.go:30 is 144 × 4 × 5,000 | ✓ |
| CU, §6.3 vs M2f | stage 37/49/62k; 14–27k a signature tx; 13–18k finish; ≈100–120k an update; proof 29k ring / 34k header; pausable 45k; delivery 94–126k, 228k; kinds 18–46k; kind 11 123/170k; upgrade 166k + 2.5k; transfer 164k; `finish_setup` 48k | all ✓ but two: header 34.3k only with a near-empty ring, **44.6k with a full one** (the steady state from the second day); the upgrade **167.7k**; unlisted: a ring `StageInit` 17.5k, `CloseCancelled` 11.5–18.9k, a 2,250-byte decision's delivery 319.7k — the relayer's budget defaults to 400,000 CU (agent/relayer.go:182; unstated, harmless) | two stale (C333) |
| Kind 11 by the account | §6.1 255 | (10,135 − 10 − 45)/40 = 252; 250 with 65 chain-id bytes | ✗ (C313) |
| r/hlgov's largest body | unstated | a 16-entry cancel 9 + 8 + 1 + 1 + 144 = 163 B; kind 5 with four pairs 142 B; all under 600 | unstated (C313) |
| Day-bucket griefing; signable power | §9 20 GNOT, ≈$6,900; §11.10 120 of 360, three more | 4 × 5 = 20; 100,000 × $0.069 = $6,900; 241 − 120 = 121 → three at 60 | ✓ |
| Documents | AUDIT.md round 3 on da9567d, 1,071 s; README 68 encodings, kinds 1–9 and 12 | README ✓ (vectors.txt holds 68); RESUME.md "Plan v30", "round 2 → v30", "≈12 minutes" (1,071 s ≈ 18), "after audit round 2"; BUILD.md M4 "the 72-hour run" (C243, four rounds) | ✗ stale |

**C333 (low)** §6.3's header-proof figure is the empty-ring one: S-10's look-up makes a by-header `Verify` 44,600 CU once the ring is full (M2f), which it is from the second day; the upgrade is 167.7k. Say 34–45k and 168k.

## (d) Terms before definition, parameters without a value

**C334** `gno.maxNodeFee` (default max(10× the block-price fee, 1 GNOT)) and the relayer's compute budget (400,000 CU): neither named (C316).
**C335** The list bounds: a void, restore, refund or sweep list and a `payouts` flag's list are 256 ids (C254's residue), the successor and verifier listings 20 a page; only the cancel's 16 and the registry's twenty are in the plan.
**C336** "ring epoch": the client's body and every stage carry it since S-16; undefined (C321).
**C337** `MaxDecisionBody` (600) and `MAX_PARAMS` (10,135): no value in the plan (C313).
**C338** `predecessor` and `inherited` — the successor's state fields `hlwatch` reads before "immutable" — are unnamed; §6.5's "its own `init` carries none of them" leaves unsaid that the successor's init names its predecessor (C314).
**C339** "the decision's refund key" (§6.5) before the sentence saying the decision carries none (C330).
**C340** `hlrecover`'s reproducible build hash: `hlwatch`'s is in the launch notice and the vote (§5.8), the comparator's pinned (§7.3); the tool five validators sign with has no pin (C332).
**C341** `mint:cap-ratio`'s start: the cap is a tenth of the expected first-week lock (§5.4, §11.11) and the alarm fires above a fifth of the lock, so it stands from the first poll until the lock reaches half the expectation — the e2e asserts it standing (M3g); the plan never says when it falls silent.

## (e) What a careful GovDAO member still refuses

1. The slow path for a signer removal is written three ways — refused when made (§5.3), waits 7 days (§11.14), drilled as queued (§5.9); he cannot tell which proposal he may make (C324).
2. `hlrecover`, the tool five validators sign recoveries with, has no reproducible hash in the vote or the notice, and no validator key has signed with it (C340; M4).
3. `mint:cap-ratio` stands by design from launch until the lock reaches half the expectation; an alarm that always stands is noise where §5.9 wants a signal (C341).
4. A reset carries at most 252 validators and the heap serves 50; §6.1 still says 255 (C313).
5. A range void is capped at 1,000 credits of every kind: at the stress case's 500 a day a range covers two days; "one closure covers a flood" is the quiet case (C329).
6. The watch's flag lands "within a minute" in §1's leak walk, §6.1 and §9 and in two minutes in §5.9; the leak ending's bound is two minutes of the cap (C325).
7. §5.5 calls `Claim`'s gas an estimate "until it is built" a round after §10 said it is built (C326).
8. RESUME.md briefs a reader on v30, round 2 and a 12-minute e2e beside a v31 plan and an 18-minute run; BUILD.md's M4 still runs one 72-hour run (C243, four rounds unfolded).
9. The shortfall is 3.26 SOL to the lamport and the margin 1.04; §5.8's "≈3.2" and "≈1.1" hold at their rounding only, and the next growth of the admin program rounds the margin to 1.0.
10. Nothing on a chain has executed a kind 8, 9 or 11, a `Retired` re-dispatch, a recipient switch, a day-cap park with a younger send or a node-minimum fee — "each is a unit test" (M3g); §8.4's "drilled" is still M4's promise.

## (f) Verdict

Closest yet: the live facts hold to the lamport, twenty-five of the twenty-nine regressions are in the text, and every number of the ask, the clocks, the limits and the float recomputes. Not at rest: three readings of round 3 are missing or misstated (the 600-byte and 10,135-byte bounds with kind 11's 252, the kind-9 self-refusal, the retired predecessor's pending decisions), §5.7's "never parked" is false under a node's minimum fee, and four residues of earlier rounds survive (C284 in §11.14 and §5.9, C265's "within a minute", C303, C298 in §9). Fold; one bounded round, then the documents.

# Audit round 3, audit 2 — the plan fact check with the builders' notes (PLAN.md v30, tree 777ceae + docs, 2026-10-13)

Read whole: PLAN.md v30, AUDIT.md (every "Changes from what Jae was told" bullet checked against the plan), REVIEWS.md (preamble, rounds 21–22), the NOTES (M1d, M1e, M2e, M3f first), BUILD.md, RESUME.md, README.md, the r1/r2 fact checks. Live facts once each (c); code opened twice, where a note's number needed settling (named). Not spent on the heap frame or the one day cap (one line in (a)). IDs from C284.

## Regression: C256–C283

- C256 folded (§1, §5.3).
- C257 folded (§5.3 the refund builder); §5.9's counters still omit it (harmless).
- C258 folded (§5.3 "the identity closes on the amount GovDAO names").
- C259 folded (§6.1 kind 11).
- C260 folded (§6.1 "by the Clock alone").
- C261 folded (§5.7, §9, §13).
- C262 folded (§5.8 five accounts).
- C263 folded, the bound inverted (C291).
- C264 folded (§5.2 r/hlgov).
- C265 folded (§1 two minutes).
- C266 folded (§5.4 "after `Launch`").
- C267 folded (§5.3, §8.4: the proposer and `hlwatch`).
- C268 folded (§5.4 kind 12 first).
- C269 folded (twelve kinds).
- C270 folded (§2).
- C271 folded for the bytes; the "(M2d …)" label survives (C302).
- C272 folded in §1 and §6.4; §6.1 and §9 residues (C298).
- C273 half: the decision rents "measured as built" (§5.8); §5.5's `Claim` residue (C303).
- C274 folded (§1 the tight form).
- C275 folded in §5.5 and §10 (four); §6.3's "three or four" residue (C299).
- C276 folded (§6.5). C277 folded (glossary).
- C278 folded (§6.1). C279 folded (glossary).
- C280 folded (§6.5).
- C281 not folded (the local 0.001; 0.01 sent against 0.1 budgeted, unsaid).
- C282 not folded ("salt u64 — random"; harmless).
- C283 folded (§13).

AUDIT.md's bullets: all in the plan but two — the range's closing height and 1,000 cap (C285), removals by the key held at creation (C286) — and one stated backwards (C291).

## (a) Readings in the notes' round-2 sections the plan does not state or states differently

**C284 (medium-low) — a signer removal the rule cannot survive is refused, not queued.** Plan §5.3: "a signer removal that would leave the rule unsatisfiable … takes effect 7 days after execution like a raise"; §1 and §11.14 likewise. M1-NOTES K19: "asks `satisfiable(ks)` when a strict-subset pure removal is made and refuses one the active verifier says the rule cannot survive; … (a removal that fails it then — another landed in between — is queued)". Change the plan: refused when made (its own "refuse nonsense early"); the 7-day path is a rotation naming a rule the remaining set meets; only a removal made unsatisfiable between creation and execution queues. The week holds; the words name a proposal GovDAO cannot make.

**C285 (medium-low) — the range closes at creation and is capped at 1,000.** Plan §5.3: "may name a range of credited claims (between two block heights, or by the verifier since a height) … so one flag and one closure cover a flood" — no closing height, no cap. M1d R14: "a `since:` range is closed there [the creation height] … refuses more than `MaxRangeClaims = 1000` ('split the range by heights') … A payouts flag's range … is not capped". AUDIT.md told Jae; the plan never says it. Change the plan (§5.3): both rules, and that the gas (≈0.4M a claim by the chain's table) is unmeasured on a chain.

**C286 (low-medium) — a removal names the holder at creation.** Plan §5.3 names seats only ("no proposal vacates more than two guardian seats"). M1d R17: "records each seat's holder in `VoidParams.RemoveAddrs` … vacates only while the holder still has the seat … `seats M skipped (the occupant changed since the proposal was made)`". Change the plan (§5.3): a removal names the key held when the proposal was made and skips a changed seat, so a queued removal never evicts a replacement landed in between.

**C287 (low-medium) — the sweep void is forged-only; its restore waits 7 days.** Plan §5.3: "a sweep entry is voided by its message id the same way — as forged it leaves owed and the sweep queue, restorable within 7 days". M1d R11: "voided **as forged only** — it is withheld by nature …; the restore reverses it after 7 days like a forged claim's; a credit by id replaces an entry — voided or not". Change the plan: "as forged only, restorable after 7 days like a forged claim, a credit by id replacing it voided or not" — "within 7 days" is a deadline nothing keeps.

**C288 (medium-low) — the burn above the verdict's slot; the scan's bound.** Plan §5.6: "a signer never signs on a verdict older than one round"; §5.9: the snapshot carries "the dispatch slots". M3f G13: "`signSol` leaves a dispatch finalized above it [the verdict's slot] unsigned — the batch stops there … until a held cut whose snapshot is at or past its slot counted it as a burn in flight; it is signed one round later"; G14: "`ClientRecord.Sync` … folds nothing finalized above it [the snapshot's slot] … `header:gap` … dropped while the scan is 'behind'". Change the plan (§5.6, §5.9): a burn is signed only after a verdict whose snapshot is at or past its slot counted it (a round's wait on every burn — the rule that catches the mint-and-burn); the record is read to the snapshot's slot, a gap counting only once the scan reaches it.

**C289 (low-medium) — a proof by header is bounded at 128 results a block.** Plan §6.2: "at most 8 KB; a bigger leaf means a `Reemit`"; §6.1: "every staging transaction stays under Solana's 1,232-byte packet". M3f F6: "a proof's `StageInit` alone … exceeds the packet past seven aunts, a block of more than 128 results; `ProofTxs` answers `ErrPacket` then, and the relayer asks a Reemit … (a second one is not asked)"; "a second block as large stays stuck until a hand re-emits". Change the plan (§6.2): such a block delivers by the ring or after one Reemit; a chain that stays that busy needs a hand — a liveness limit "never left behind" does not name.

**C290 (low) — the verifier registry.** Plan §5.1: "`RegisterVerifier` only from an /r/ realm, stored with its pkgpath". M1e X1: "refuses `cur.Previous().Subpath() != ""` … the listing is bounded … `AddRoute` and `DraftMigrate` refuse a `#` path"; X2: "a /p/-declared type with no object to anchor on runs under the mailbox's own authority, which `RegisterVerifier` cannot tell apart". Change the plan (§5.1, §8.3): a registrant's own identity, never a sub-realm's; the registry paged; and the member's check that the active verifier's type is declared in its own realm — the gate the mailbox cannot apply.

**C291 (low) — the re-assertion's bound is inverted.** Plan §5.9: "re-asserts its Solana flag … at least once a minute while an invariant is broken (so 0.05 SOL lasts a week of it)". M3-NOTES G12: "at most once a minute across every condition". Change the plan: "at most" — "at least" admits every ten-second poll, at which 0.05 SOL (10,000 signatures) lasts 28 hours.

**C292 (low) — one day cap for both tokens.** Plan §1: "a day cap of a tenth of the expected lock, at most 100,000, per token". M3f F10: "one constant for both tokens …, so the smaller of the two tokens' values is compiled". Queued; noted once.

**C293 (low) — the extend alarm prices rent at the local rate.** Plan §6.5: "≈0.052 SOL a step" (10,240 B at §2's 5,080). M3f G18: "the rent at 6,960 lamports a byte — read and alarmed"; `sol/loader.go:27` `RentPerByte = 6960`, the test validator's rate (§2). Change the code (read the rent) or say the alarm overstates mainnet by 37%.

**C294 (low) — between pages both realms are frozen.** Plan §5.3: "in pages of 500 records the successor pulls, the lock with the first page". M1d R13: "the counters move whole with page 0 … both refuse every function (migrated; not yet adopted) and the monitor's identity reads the counters"; left: "a successor's `Adopt` imports page by page". Change the plan (§5.3, §5.9): the pages are a pause of both realms, the watch reads the successor from page 0, and no successor realm exists yet.

## (b) Internal contradictions in the plan

**C295** §5.3 "restorable within 7 days" (the sweep void) vs §5.4 "restores them (at once if withheld, after 7 days if forged)" — a restore is a 7-day change; nothing expires (C287).
**C296** §5.5 "`Execute(migrate)`'s caller collects the migrated records' [deposits]" vs §5.3 "in pages of 500 records the successor pulls" — the records leave at each `Pull`, so the deposits go to the successor's caller page by page; `Execute` only names the successor.
**C297** §6.4 "`hlwatch`'s SETTLE DUE is a standing order for either outsider" vs §11.9's standing orders "written into the launch notice: look at `hlwatch` weekly …" — SETTLE DUE is not among them.
**C298** §6.1 "only the settled set for a recovery (§6.4) once its replacement has settled" and §9 "powerless once its replacement has settled (§6.1)" vs §6.4 "the record, not the client's view, is what moves the recovery's power" and §1 "settled and been recorded by `settle`".
**C299** §6.3 "≈80k CU per 6-validator update over 4 transactions (… 35–47k to stage …)" vs its own "(three or four transactions per header at gno.land's six validators)" and §5.5/§10 "four transactions … for gno.land's six validators"; its band sums to 89–120k, not 80k (M2e: 48,500 to stage six).
**C300** §6.5 "the executor sends the whole excess at once and a growth takes one block" vs §6.5 "≈0.052 SOL a step, alarmed with the whole cost before the first step" and §6.3 "164k for an upgrade plus 21k per 10 KB growth step" — one step, of the excess, at 2,520 CU (M2e).
**C301** §2 "An epoch is 432,000 slots ≈ 2 days" and §7.3 "the roster changes every two days" vs §13 "at today's 0.22-second slot … an epoch ≈ 26 hours" and §7.3 "(≈2.2 days at today's 26-hour epoch)" — 432,000 × 0.22 s = 26.4 h.
**C302** §2 "ours as built on 2026-10-13 (M2d …): the light client (272,464 B) …" vs the same sentence's "as built after the second audit round (M2e …)" — M2d's client was 270,888 B (M2-NOTES, BUILD.md).
**C303** §5.5 "`Claim`'s share an estimate until it is built" vs §10 "`Claim` is built; its gas is measured on devnet, M4".
**C304** §9 "A griefer filling 100,000 a day pays 5,000 GNOT of fees a day (≈$345)" vs §5.4 "A per-message maximum of 25,000 GNOT" and §5.5 "a flat fee … (5 GNOT …)" — four sends at the maximum fill the day for 20 GNOT (≈$1.40); the row prices the attack 250× too high.

## (c) Numbers recomputed (live, read once each: mainnet RPC at slot 456,237,689; rpc.gno.land/validators at height 762,848; r/gov/dao:10 and :13)

| Item | Plan | Recomputed / live | |
|---|---|---|---|
| Rent rate | §2 5,080/B; 0 B → 650,240 | 0 B 650,240; 1 B 655,320 → 5,080/B | ✓ |
| Deployer | 7.36 | 7,356,770,000 lamports = 7.3568 SOL | ✓ |
| Validators | 6 × 60 = 360; forging 5 of 6 (241; any 4 = 240); override 180/180/240; halt 120 = 1/3 | six at 60; 3s > 720 → 241; the others 180 / 180 / 240 (Berty with moul), all < 241; moul + Berty 120 | ✓ |
| Prop #10, #13 | #10 open, moul to 1: 301; 4 of the five (201); share 181 (60%); 180/180/181; halt 61; #13 passed | #10 "open for votes", YES 33.33%; 3s > 602 → 201 ≤ 240; 181 = 60.1%; 180/180/181 < 201; 61 < 101; #13 "ACCEPTED" 66.67% | ✓ |
| ELF sizes | §2, §5.8: 272,464 / 164,432 / 397,760 → 1.39 / 0.84 / 2.02 | M2e the same bytes; (size + 45 + 128) × 5,080 → 1.38500 / 0.83619 / 2.02150 = 4.243 (M2-NOTES: 1.38, the rounding boundary) | ✓ |
| Rent sum | ≈9.0 | 1.07288 + 2 × 1.81822 + 4.243 = 8.952; + six program accounts 0.005 = 8.957 | ✓ (8.96) |
| Need, shortfall, margin, splits | ≈10.6; ≈3.2; ≈1.1; each split 8.0 | 8.957 + 0.15 + 1.5 = 10.607; − 7.357 = 3.250; 8 − 3.25 − 3.7 = 1.05; both splits 8.0 — 3.2 and 1.1 at the boundary | ✓ |
| State accounts | §2 0.15 = 0.11 + 0.035 + 0.01 | client 21,160 B → 0.108 (M2e S-09 names 21,208 B, +0.0002); admin 6,814 B → 0.0353; the rest ≈0.01 | ✓ |
| Decision rents | 0.0014–0.002; cancel record 0.0009 | 153 B → 0.00143; 246 B → 0.0019; 50 B → 0.00090 | ✓ |
| Authority PDA | §5.8 "≈0.001 per router enrolment as built"; 0.1 | M2 17b 513,000 lamports at 5,080 = 0.0005 (0.001 is the local 702,960); `hlsol setup` funds 0.01 → ≈20 enrolments; 0.1 → ≈200 | ✗ (C281) |
| Clocks at factor 8 | 24 h, 7, 14, 21, 28 d; dips 25/23 h → 3.1/2.9 h; 14 d in 42 h | 3 h, 21 h, 42 h, 63 h, 84 h; 3.125/2.875 h; the notes agree | ✓ |
| Blocks | 176,327; 352,653; an hour 1,050 | 604,800/3.43 = 176,326.5; 352,653.1; 1,049.6 | ✓ |
| Slots at 0.22 s | 24 h ≈ 390,000; 7 d ≈ 2.75M; an epoch ≈ 26 h; griefing 8–16 SOL a day; §2 "≈ 2 days" | 392,727; 2,749,091; 95,040 s = 26.4 h; 4–8 tx × 5,000 × 392,727 = 7.9–15.7 SOL | ✓ but §2 (C301) |
| Mint limits, base units | §13 25,000 a message, 100,000 a day; the day cap min(100,000, lock/10) | config.rs 25,000,000,000 / 100,000,000,000; the e2e's kind 12 `0000000ba43b7400` = 50,000,000,000, `0000002e90edd000` = 200,000,000,000; one constant, the smaller (C292) | ✓ |
| Updater's float | 0.3 SOL ≈3.4 months at four 5,000-lamport transactions a header every 10 min; ≈23 days at 500/d; 0.086 SOL/month ($9.5); 0.39 | 20,000 × 144/d = 0.00288/d → 104 d; 644/d → 23.3 d; 0.0864 = $9.47; 0.386 | ✓; agent/floats.go:24's planned floor ("0.3 SOL over five months", 0.002/d) is 30% below |
| CU, §6.3 vs M2e | stage 35–47k; 14–27k a signature tx; finish 13–18k; ≈80k a 6-validator update; proof 29k/33k; pausable 45k; delivery 105–130k, 223k with eight dispatches; kinds 2, 3, 5–7, 10, 11 18–44k; upgrade 164k + 21k per 10 KB step; transfer 147k; `finish_setup` 43k | 36.7k (4) / **48.5k (6)** / 61.7k (40); 14.1–27.3k ✓; 13.0–17.5k ✓; **98.6–120.6k**; 29.1k / **34.2k**; 44.6k (round 1) ✓; 93.7–126k ✓, **227.5k**; 31.2 / 28.0 / 31.3 / **46.1** / 31.6 / 20.7 / **122.8k–169.7k**, kind 12 27.7k unlisted; **166.2k + 2,520, one step**; **163.9k**; **47.6k** | nine stale; round 2's "seven" never folded |
| Range void | §5.3 silent | M1d ≈0.4M gas a claim by the chain's table → 1,000 ≈ 0.4B; 3B/0.4M = 7,500 (AUDIT.md "≈7,000") | unstated (C285) |
| Day-bucket griefing | §9 5,000 GNOT (≈$345) | 100,000/25,000 = 4 sends × 5 GNOT = 20 GNOT ≈ $1.38 | ✗ (C304) |
| Signable power | §11.10 120 of 360; "three of them must sign" | 2 × 60; 241 − 120 = 121 → three more at 60 | ✓ |
| Economics | prices of 2026-10-10 | not re-read (not a named fact); round 1's rows stand | — |
| Documents | AUDIT.md/BUILD.md: the e2e 729 s on 777ceae; 68 vectors; kinds 1–9 and 12 | RESUME.md "≈4 minutes", "8.9 of rent … after audit round 1"; README.md "58 encodings", "kinds 1–9" | ✗ stale |

## (d) Terms before definition, parameters without a value

**C305** "SETTLE DUE" (§1, the "Nothing more is promised" bullet) before §5.8/§6.4 say what it is; no glossary entry.
**C306** "the ring" (§6.1 "a growth moves the ring") before §6.2 defines it; no glossary entry.
**C307** The signer's round ("a verdict older than one round", §5.6) has no length; §1 gives only the monitors' ten seconds.
**C308** `MaxRangeClaims` (1,000) and the range's closing height: no value (C285).
**C309** The precommit chunk (40 a `StagePrecommits`) is unnamed beside §6.1's 23-validator chunk.
**C310** "The expected lock" — the input of every launch cap — has no figure and is not a listed precondition as §11.4's SOL is.
**C311** §13's commit rules omit the round (one per commit, carried by the stage, a mixed commit refused) that §6.1 states.
**C312** The authority PDA's other 0.09 SOL (0.1 budgeted, 0.01 sent): where it sits (C281).

## (e) What a careful GovDAO member still refuses

1. §9's deterrent against filling the day bucket is 20 GNOT, not 5,000: four sends at the maximum close gno → Solana to honest senders for a day at ≈$1.40, repeatable (C304).
2. The 7-day signer-removal path of §1/§5.3/§11.14 is a proposal the realm refuses when made; the real slow path (a rotation with a new rule) is unwritten (C284).
3. §6.3's "measured" compute figures are stale a second round — kind 11 at 123–170k, the transfer 164k, the step 2,520 — unfolded since round 2.
4. No validator key has ever signed a recovery: hlrecover passes the e2e by refusal, the YubiHSM path is "untested against a device", and §8.4's "drilled" is all M4.
5. §6.4 calls SETTLE DUE "a standing order for either outsider"; §11.9's orders — the launch notice's — never give it (C297).
6. The pausable ISM compiles one day cap for both tokens, the smaller; §1 promises "per token" (C292, queued).
7. The migration's deposits reward the successor's caller page by page, not `Execute(migrate)`'s as §5.5 says — and no successor realm or `Adopt` exists (C296).
8. A block of more than 128 results strands a proof by header after one Reemit; "never left behind" does not name it (C289).
9. RESUME.md and README.md still brief on v27/M2b, 58 vectors, "kinds 1–9", "≈4 minutes" and 8.9 SOL beside a v30 plan.
10. The authority PDA's float — 0.01 sent, 0.1 budgeted, "≈0.001 as built" at the local rate — is three rounds unchanged (C281, C312).

## (f) Verdict

Consistent where round 2 folded: the live facts hold to the lamport, twenty-five of the twenty-eight regressions are in the text, and the rents, clocks, limits and float recompute. Not at rest: two readings the member was told are not in the plan (the range cap and closing height, the occupant rule), the signer-removal slow path names a proposal the realm refuses (C284), the burn-after-verdict and 128-result bounds are unstated (C288, C289), §6.3's compute table is stale a second round, and §9 prices the day-bucket attack 250× too high (C304). Fold; one bounded round, with the compute table re-measured on the reproducible build.

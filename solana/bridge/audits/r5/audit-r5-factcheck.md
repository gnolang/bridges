# Audit round 5, audit 2 — the plan fact check with the builders' notes (PLAN.md v32, tree b24c9e9 + a5225e9's docs, 2026-10-13)

Read whole: PLAN.md v32, AUDIT.md (all four rounds' bullets checked against the plan), REVIEWS.md (preamble, rounds 21–22, the final fact check), M1-NOTES.md (M1g first), M2-NOTES.md (M2g first), M3-NOTES.md, BUILD.md, RESUME.md, README.md, the r3/r4 fact checks. **M3-NOTES.md has no M3h section** (`git log --all -- bridge/M3-NOTES.md` ends at 3e53f59, M3g), so the Go lane's round-4 readings were read from its fifteen commit messages (b24c9e9 ^da9567d) and the code they name. Live facts once each (c). No row on the heap frame, the one launch day cap, the seven-aunt proof, the mailbox's own body bound, the successor's `Adopt` or U10. IDs from C342.

## Regression: C313–C341

- C313 folded (§6.1 252/250, §6.5 512 and 10,135; "255" is now the count byte's only).
- C314 folded (§5.3, §13 kind 9, §8.4). C315 folded (§6.5). C316 folded (§5.7 `gno.maxNodeFee`). C317 folded (§5.3).
- C318 folded (§5.3 lists skip, the closure's unpause and removals apply; §5.4 a dry lock skips). C319, C320 folded (§5.3).
- C321 half: the ring epoch is in §6.2 and the glossary; §2's "21,160 B" stands (21,168 after M2f's +8 B; M2g names the deploy account at 21,216).
- C322 folded (§5.9 "on the second poll"). C323 folded (§5.8). C324 folded (§11.14, §5.9's drill). C325 folded (§1, §6.1, §9 ×2: "two minutes").
- C326 folded (§5.5). C327 folded (§9). C328 folded (§8.3).
- C329 not folded: §5.3 still "so one flag and one closure cover a flood" beside the 1,000-credit cap (C358).
- C330 folded (§6.5). C331 not folded: §6.5 still "(a growing upgrade's extend steps included)" (C354). C332 folded (§5.8 "Four tools", `hlrecover`'s hash). C333 folded (§6.3).
- C334 half: `gno.maxNodeFee` named; the 400,000 CU budget still unnamed (C370).
- C335 half: the sweep's 256 and the registry's twenty are in; the void/restore/refund lists' and a `payouts` flag's 256 are not.
- C336–C340 folded (glossary; §6.5; §6.5/§13; §6.5; §5.8).
- C341 not folded in the plan: the code fixed it (M3h 5af98ff) and AUDIT.md's round-4 bullet says so; §5.4 states the alarm without its quiet period (C342).

AUDIT.md's bullets, rounds 1–4: all in the plan but two of round 4's — the `mint:cap-ratio` quiet rule (C342) and "the relayer refuses to deliver a refunded send" (C348); a third, "a growth over an empty ring keeps the ring's capacity", is in §2's parenthetical while §6.1 states the old rule (C344).

## (a) Readings in the notes' round-4 sections (and M3h's commits) the plan does not state or states differently

**C342 (medium-low) — `mint:cap-ratio`'s silence is a config value.** Plan §5.4: "the monitors and `hlwatch` alarm `mint:cap-ratio` when a token's day cap exceeds a fifth of its lock". M3h 5af98ff / `capRatio` (monitor_sol.go:354): "the config now carries each token's expected first-week lock (`routes[].expectedLock`), the rule is quiet while the lock is still below it …, judges the cap from the poll the lock reaches it and for good after (a lock that later shrinks below five caps alarms; the standing kept in the state)". Change the plan (§5.4, §5.9): judged only once the lock has reached the expectation the cap was set against, sticky after, the number in each monitor's and `hlwatch`'s config — the operator's config decides when the operator's own alarm speaks; a claim, not a rule.

**C343 (medium-low) — a checkpointed watch takes the client's word for its past.** Plan §5.9: "a fresh install starts from a checkpoint of the client's record rather than the deploy"; §6.2: "the watch rebuilds the chain from instruction data, flags a mismatch or an unfillable gap, and so sees a never-existed header proven at a past height as surely as a forged update". M3h ce64846 / `RecordCheckpoint` (views_sol.go:811): "everything at or below that slot passed over … the headers before it are not checked against the chain; from here on they are". Change the plan (§5.9, §11.9): a monitor or `hlwatch` started with `-record-from client` audits nothing before its base — the client's own counter, the thing it exists to check — so an outsider's first run starts from the deploy or from a record copied off a trusted run; the standing orders say which.

**C344 (low-medium) — the ring's growth rule is the superseded one.** Plan §6.1: "a growth moves the ring only when the account holds every entry, so no stranger's growth empties it". M2g S-17: "`grow` moves the region only once the account holds the new region *and a ring of the capacity it had before the call* … and `Ring::push` writes at `ring_count` while `ring_count < capacity`" — "every entry" was the bug (after a clear the entries are none). §2 says "keeps the ring's capacity"; §6.1's rule should.

**C345 (low-medium) — the transfer's compute moved.** Plan §6.3: "164k for a transfer to a successor". M2g S-18: "the hand-over itself 169,000–170,500, from M2f's 163,900–165,100 — the ≈5k of parsing the successor's 6.8 KB state before the CPIs"; "spent at 26,300–31,700 CU with nothing moved". Change §6.3: 169–171k, the spend 26–32k, the label.

**C346 (low) — the chain of predecessors, 16 deep, one migration a route.** Plan §5.3: "hlmailbox lets the successor take a message addressed to its predecessor once its import has ended". M1g AQ1 / flags.gno:67–69, 378: a route takes "a message whose recipient is any route in its chain of predecessors … bounded by `maxChain` [16]"; `AddRoute` "refuses a path that is a route already and a second migration of one route". Change §5.3: any predecessor in the chain, at most 16 deep (a 17th strands burns to the first realm); a route migrates once.

**C347 (low) — a queued range the pages overfill stays pending.** Plan §5.3: "more than 1,000 credits … is refused — split by heights"; "a change that fails its at-once test there is queued 7 days, not refused". M1g R28: "`mustFitRange`, a pre-walk … at once the proposal survives for a retry or a cancel, after the delay the decision stays pending". Change the plan: the count is made again in the executing transaction, and a queued range the pages have since overfilled is neither applied nor spent — pending until GovDAO cancels it, a third state §5.3 does not name.

**C348 (low) — the relayer refuses a refunded send.** Plan §5.3: "the send marked `refunded` — a mark the monitors and `hlwatch` read and drop from in flight". M3h f905071: "refused by the relayer, which reads the mark before staging … — must never deliver while it still sits in hlmailbox (an alarm per send on both, posted daily)". Say both (§5.3 or §5.7).

**C349 (low) — the line of succession is four hops.** Plan §6.5: the tools "read the admin program's `successor` from the chain and follow it". M3h 2ff822a / successor_sol.go:25: "`successorHops = 4` … a loop refused … r/hlgov's decision to a program the line does not reach is left undone and alarmed". A fifth successor returns the tools to the config edit AQ6 removed.

**C350 (low) — the forged-credit latency grows with the backlog.** Plan §5.9: "flags … on the second poll that finds it so". M3h 2dcdfcf / chain.go:362: "ten pages from 0 each poll never reached a claim behind a thousand waiting ones — a forged claim at a late address was paid before any poll listed it; now every waiting claim is visited within ⌈N/1,000⌉ polls". Change §5.9: two polls plus ⌈N/1,000⌉ over N waiting claims — 100 s for a flood of 10,000, the meter bounding what `Claim` pays meanwhile.

**C351 (low) — a cancel's rent return is no longer at the cancel.** Plan §6.5: "returned to it when the decision … is cancelled"; §5.7: "the program needing none of them". M3h d7b970a / `CancelPairsFor`: "a number not passed is stored cancelled and CloseCancelled closes it". Change §6.5: at the cancel for the pairs its packet carried, by anyone's `CloseCancelled` for the rest.

**C352 (low) — the Go's round-4 readings have no notes.** AUDIT.md defines audits 2 and 3 as "the NOTES' readings against the plan's words"; M1g and M2g have sections, M3h none. Add it to M3-NOTES.md in M3g's shape before round 6.

**C353 (low) — `hlwatch` holds the monitors' order too.** Plan §5.8: `hlwatch` alarms "from a node the holder runs and two agreeing Solana providers; on anything less it alarms only". M3h 260735a: "the view reads in §5.9's order … a catching-up, stale or not-after head refused as no verdict … a FORGED CREDIT line … on its second consecutive poll". Say so in §5.8.

## (b) Internal contradictions in the plan

**C354** §6.5 "any member by `hlrecover execute` (a growing upgrade's extend steps included)" vs §6.5 "the executor sends the whole excess at once and a growth takes one block" and §6.3 "its one growth extension" — C331's residue.
**C355** §5.8 "the three measured on the built ELFs' bytes after audit round 3" and §10 "measured as built after audit round 3" vs §2 "ours as built on 2026-10-13 (M2g …) … after the fourth audit round" — M2g's sizes, labelled round 3 twice.
**C356** §6.3 "measured … after the second audit round" vs its own "34–45k by header", "168k for an upgrade" (M2f's) and "164k for a transfer" (overtaken by M2g, C345).
**C357** §1 "a flag lands within a minute … — two minutes for the client-ahead and header rules, which wait for a second reading" vs §5.9 "on the second poll" (the unindexed credit) and "the flag waits two minutes like the record's" (the foreign decision) — four two-minute rules, two named.
**C358** §5.3 "so one flag and one closure cover a flood" vs §5.3 "more than 1,000 credits … is refused — split by heights" — C329, a second round.
**C359** §6.1 "moves the ring only when the account holds every entry" vs §2 "(M2g: a growth keeps the ring's capacity …)" — two rules (C344).
**C360** §5.4 "alarm `mint:cap-ratio` when a token's day cap exceeds a fifth of its lock" vs §5.4 "at launch it is the smaller of 100,000 and a tenth of the lock the launch notice expects" — by its own arithmetic the alarm stands from launch until the lock reaches half the expectation; the code is quiet (C342).
**C361** §5.9 "a fresh install starts from a checkpoint of the client's record" vs §6.2 "sees a never-existed header proven at a past height as surely as a forged update" — not before the checkpoint (C343).
**C362** §6.5 "returned to it when the decision … is cancelled" vs §5.7 "the program needing none of them" — the rest return by `CloseCancelled` (C351).
**C363** §8.4 "'immutable' only of a successor whose state names the admin program as predecessor, not yet inherited" vs §6.5 "… and with no successor of its own — the fields `hlwatch` reads" — §8.4 drops the third field.

## (c) Numbers recomputed (live, read once each: mainnet RPC at slot 456,298,458; rpc.gno.land/validators at height 766,505; r/gov/dao:10 and :13)

| Item | Plan | Recomputed / live | |
|---|---|---|---|
| Rent rate | §2 5,080/B; 0 B → 650,240 | 0 B 650,240; 1 B 655,320 → 5,080/B | ✓ |
| Deployer | 7.36 | 7,356,770,000 lamports = 7.3568 | ✓ |
| Validators | 6 × 60 = 360; forging 5 of 6 (241; any 4 = 240); override 180/180/240; halt 120 | six at 60; 3s > 720 → 241; 180/180/240 < 241; moul + Berty 120 | ✓ |
| Prop #10, #13 | #10 open, moul to 1: 301, 201, 181 (60%), 180/180/181, halt 61; #13 passed | #10 "open for votes", YES 33.33%; 3s > 602 → 201; 181 = 60.1%; all < 201; 61 < 101; #13 "ACCEPTED" 66.67% | ✓ |
| ELF sizes | §2 273,768 / 164,432 / 399,632 → 1.39 / 0.84 / 2.03 | M2g the same bytes; (size + 45 + 128) × 5,080 → 1.39162 / 0.83619 / 2.03101 = 4.2588 (M2g "4.26") | ✓ |
| Rent sum | ≈9.0 | 1.07288 + 2 × 1.81822 + 4.2588 = 8.968; + six program accounts 0.005 = 8.973 | ✓ |
| Need, shortfall, margin, splits | ≈10.6; ≈3.2; ≈1.1; 8.0 | 8.973 + 0.15 + 1.5 = 10.623; − 7.357 = 3.266; 8 − 3.27 − 3.7 = 1.034; splits 8.0 | ✓, but 3.27 reads "≈3.3" and 1.03 "≈1.0" now |
| State accounts | §2 0.15 = 21,160 B ≈0.11 + 6,814 B ≈0.035 + ≈0.01 | 21,168 → 0.1082 (the deploy account 21,216 → 0.1084); 6,814 → 0.0353 | ✓; 21,160 stale (C321) |
| Decision rents | 0.0014–0.002; cancel 0.0009 | no format change in M2g: 0.00139 / 0.00186; 0.00090 | ✓ |
| Clocks at factor 8 | 24 h, 7, 14, 21, 28 d; dips 25/23 h → 3.1/2.9 h; 14 d in 42 h | 3, 21, 42, 63, 84 h; 3.125/2.875 h | ✓ |
| Blocks; slots | 176,327; 352,653; ≈176,000/≈353,000; 24 h ≈ 390,000; 7 d ≈ 2.75M; epoch ≈ 26 h | 176,326.5; 352,653.1; 392,727; 2,749,091; 26.4 h | ✓ |
| Day cap, base units | §13 25,000 a message, min(100,000, lock/10) a day | setup.go:159 `launchDayCap(expectedLock)`, `launchDayCapMax` 100,000,000,000; config.go:86 `expectedLock` per route (C342) | ✓ |
| Updater's float | 0.3 SOL ≈3.4 months (four 5,000-lamport tx a header every 10 min); ≈23 d at 500/d; 0.086/month ($9.5); 0.39 | 144 × 4 × 5,000 = 0.00288/d → 104 d; 644/d → 23.3 d; 0.0864 = $9.47; 0.386 | ✓ |
| Body bounds | §6.5 512; 538 by a legacy delivery (14 keys, 127 fixed); largest builder body 163 | 1,232 − (448 + 127 + 119 of signature, header, blockhash, lengths) = 538; 9 + 8 + 1 + 1 + 16 × 9 = 163 | ✓ |
| CU, §6.3 vs M2g | stage 37/49/62k; 14–27k; 13–18k; ≈100–120k; 29k / 34–45k; 45k; 94–126k, 228k; kinds 18–46k; kind 11 123/170k; upgrade 168k + 2.5k; **transfer 164k**; `finish_setup` 48k | all as M2f/M2g but the transfer: **169,000–170,500**; unlisted: the kind-9 spend 26–32k, `Grow` 9–22k a call | one stale (C345) |
| Day-bucket griefing; signable power | §9 20 GNOT, ≈$6,900; §11.10 120 of 360, three more | 4 × 5 = 20; 100,000 × $0.069 = $6,900; 241 − 120 = 121 → three at 60 | ✓ |
| Documents | AUDIT.md/BUILD.md 872 s on b24c9e9; README 68 encodings | both ✓; RESUME.md §3.4 "after audit round 3", "≈18 minutes" (872 s ≈ 14.5); BUILD.md M4 "the 72-hour run" (C243), M2 "three real set changes" (four); README "in-flight tolerance" (exact since AP1), "plan v26 … (M2b)", "the light-client delivery is unmeasured" | ✗ stale |

## (d) Terms before definition, parameters without a value

**C368** `expectedLock`: the per-route config value of each monitor and `hlwatch` that silences `mint:cap-ratio`, and the "five caps" shrink rule — neither named (C342).
**C369** `successorHops` (4) and `maxChain` (16): no value (C346, C349).
**C370** The 400,000 CU budget of the relayer, the executor and the updater — C334's residue.
**C371** The claims walk's budget (ten pages, 1,000 claims a poll, the `ClaimsFrom` offset) — the latency bound unstated (C350).
**C372** "the cut" has two senses: the glossary's "the fork … cut at the Solana slot S" and §5.9's "a poll that fails past the cut" (the monitors' ordered reading); only the first is defined.
**C373** "importing", "imported", "the pair" (§5.3, §5.9): a route whose pages still arrive, one whose last page is in, predecessor plus successor — used, never defined; the json fields the tools read unnamed.
**C374** Alarm cadences: daily (`AlarmEvery`) for `mint:cap-ratio`, a refunded send in hlmailbox, `admin:successor`; hourly for parks — only SETTLE DUE's hourly is in the plan (§6.4).
**C375** "kind 12" is used in §5.3's refund builder ("waits for a kind 12") before §5.4 names kind 12 the mint limits; the glossary's "kind" points to §13.

## (e) What a careful GovDAO member still refuses

1. The Go lane's round-4 readings have no notes section; fifteen commit messages stand in (C352).
2. `mint:cap-ratio`'s silence is set by a number in the operator's monitor config; the plan says the alarm fires above a fifth of the lock, full stop (C342).
3. A monitor or `hlwatch` installed from the checkpoint takes the client's own count as true and audits nothing before it — the outsiders' first run must start from the deploy, and nothing says so (C343).
4. §6.1's ring rule is the one round 4 found wrong; §2 and §6.1 disagree (C344).
5. §6.3's "measured" table is labelled round 2 and carries a transfer figure round 4 moved (C345, C356).
6. A queued range void the pages overfilled stays pending for ever unless cancelled — a third state the at-once rule does not name (C347).
7. "One flag and one closure cover a flood" sits beside the 1,000-credit cap a second round (C358).
8. No chain has run a migration's successor-taking, a refund, a Retired re-dispatch, a recipient switch or a kind 8 growth: no successor realm exists (§5.3), M3g's gap list stands, §8.4's "drilled" is M4's.
9. `hlrecover` has still signed with no validator key (M4; r4's item 2 stands).
10. RESUME.md, BUILD.md and README.md — the three documents a member reads first — are each a round or more behind the plan ((c)'s last row).

## (f) Verdict

Closest yet: every live fact holds to the lamport, every number recomputes, twenty-three of twenty-nine regressions are in the text. Not at rest: two of round 4's own changes are not in the plan (the cap-ratio quiet rule, the relayer's refusal of a refunded send), §6.1 states the ring rule round 4 replaced, a checkpointed watch's blind spot is unsaid, the transfer's compute and three labels are stale, and the Go's round-4 readings have no notes. Fold; write M3h's notes; one bounded round, then the documents.

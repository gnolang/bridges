# Audit round 2, audit 2 — the plan fact check with the builders' notes (PLAN.md v29, tree 22d28c8, 2026-10-13)

Read whole: PLAN.md v29, AUDIT.md, REVIEWS.md (preamble, rounds 21–22), M1-/M2-/M3-NOTES.md, BUILD.md, RESUME.md, README.md, audits/r1/audit-r1-factcheck.md. Live facts read once each (c). Code opened only where a note's number needed settling (named). Not spent on: r/hlgov's missing kind-12 builder, the heap frame. IDs continue from C256.

## Regression: C226–C255

- C226 folded: §6.1 (five entries, the newest settled never evicted, `HistoryFull`), §6.4 (`settle`, the record); M2d S-01.
- C227 folded: §6.5 `successor_init` signed by the predecessor's PDA; M2d choice 22.
- C228 folded: §6.5 the floor from the last eight holds, computed at `Execute`; M2d AN4.
- C229 half: §13 kind 9 "a realm has no RPC, so the proposer and `hlwatch` check it"; §5.3's unqualified "refused when it is made" stands, §8.4 lists no proposer check (C267).
- C230 folded in §5.4's first sentence; "one cap in the first 24 hours after a deploy" survives (C266).
- C231 not folded: §1 and §5.3 name only the lowered threshold (C256).
- C232 not folded: no refund builder in §5.3, no refund rule in §5.9 (C257).
- C233 not folded: §5.3 "so the identity does not move" (C258).
- C234, C235, C236 folded (§5.8, §13, §6.4; `hlrecover record` orders the reset last).
- C237 folded in §13 kind 11 ("as the deploy's"); §6.1's deploy sentence still says only "less than an hour old".
- C238 folded with C227. C239 half (C230). C240, C241, C242 folded. C243 folded (§10 "the four 72-hour runs"; BUILD.md M4 still singular). C244 folded as 223k — stale again (c). C245, C246 folded. C247 not folded (C256).
- C248, C249, C250, C251 folded (the store's 64 unstated, harmless). C252 half (C278). C253 not folded (C273). C254 not folded (the 256 cap unstated; harmless). C255 not folded: go.mod's `go 1.26.0` is a floor; the plan says nothing.

## (a) Readings in the notes the plan does not state or states differently

**C256 (medium-low) — any signer-rule change waits 7 days.** Plan §1 slow list: "a signer-rule change that lowers the threshold"; §5.3 instant: "removing a signer while the verifier says its rule stays satisfiable by the remaining set" — by silence the same proposal may change the rule. M1-NOTES §5.1: "Everything else — an addition, any rule change (a raised threshold too: the plan's instant list names only removals), a removal the rule does not survive — waits 7 days." AUDIT.md lists "any signer-rule change waits 7 days" among the things Jae was told; the plan never says it. Change the plan: §1 "a signer-rule change of any kind"; §5.3 "a pure removal, the rule unchanged" — a removal plus a raised threshold at once is the instant closing §1 forbids.

**C257 (medium-low) — the refund builder and the identity.** Plan §5.3's builder list has no refund (only "a successor realm, which refunds by id"); §5.9's counters say nothing of one. M1-NOTES: "Refund by id is a builder on the route itself (7 days, from the lock to the sender, the send record marked) … the same code serves the predecessor"; "a refund by id reduces sentTotal and the lock together". A funds move by vote §3.1 does not name, and a counter rule the monitors must evaluate. Change the plan (§5.3, §5.9).

**C258 (low-medium) — credit by id for a voided claim.** Plan §5.3: "replaces the restored remainder with the recipient and amount GovDAO names … so the identity does not move". M1-NOTES: "Owed moves by (amount − old remainder); the identity holds only when GovDAO names the remainder". Change the plan: the identity closes on the amount named; the watch expects owed to move.

**C259 (medium-low) — a reset clears the quarantine.** Plan §6.1: "overlapping changes extend it to the latest; no decision shortens it". M2-NOTES choice 5: "A reset clears the quarantine (the set it installs is settled by definition … a hold the guardians want keeps standing on the pausable ISM)". The code is right (a reset installs a new chain, its set settled by definition, §6.1); change the plan: "no decision but kind 11 shortens it".

**C260 (low-medium) — "settled" has two meanings.** Plan §6.1: "the newest settled one and everything after it never evicted" — the glossary's "settled" reads holds. M2-NOTES choice 6: "the newest entry whose quarantine has ended by the Clock … The client reads no holds, so under a hold its view is the clock's alone; the record in the admin state is what the recovery signs with." Under a hold the client may evict the plan-settled entry (harmless since S-01; the sentence overstates). Change the plan: "settled by the clock alone — the client reads no holds".

**C261 (medium-low) — honest inflow above the day bucket.** AUDIT.md told Jae: "Honest inflow above the day's bucket waits for the next day." The plan has only §13's "refuses one above the maximum or beyond the day's bucket". M3e: a refused send "parks after three refusals … its retry at its hourly retry"; M2 choice 26: the bucket is rolling, "drains at the day cap per 24 hours … from that moment". Gno.land bounds no day of sends (§5.4 meters payouts), so a busy day queues on Solana at 100,000 a token a day, parked and retried hourly; a send above the Solana maximum parks until a kind 12 (C268). Change the plan (§5.7, §13).

**C262 (low) — the pausable ISM's `Verify` accounts and the unmetered recipient.** Plan §5.8: "answers `VerifyAccountMetas` with its own flag account and the client's state account prepended to the client's". M2-NOTES choice 26: "the flags, the state (writable now), the client's state, the client program, the Instructions sysvar, then the client's"; "a message to any other recipient mints nothing of ours and passes unmetered". Change the plan (§5.8): five accounts; the meter is the two tokens' only.

**C263 (low) — the watch key's re-assertion.** Plan §5.9: "re-asserts its Solana flag with a fresh slot on every poll while an invariant is broken" (polls every 10 s). M3-NOTES G12: "at most once a minute … once a minute is fresh for an unpause that reads the slot". Change the plan: once a minute — at every poll §5.6's 0.05 SOL (10,000 signatures) lasts ≈28 hours, at once a minute 7 days.

**C264 (low) — the refused recipients.** Plan §5.2: "a route realm, the mailbox, the fee realm or the bubble realm". M1-NOTES §5.2: "any of the six realms' addresses (the mailbox, both routes, the fee realm, hlgov, the bubble token)". Change the plan: add r/hlgov.

**C265 (low) — the client-ahead flag's delay.** Plan §1: "a flag lands within a minute of a reading that passes §5.9's consistency checks"; §5.9: "a lead beyond an hour of blocks is itself the mismatch flag". M3-NOTES G3: "a stop persists two minutes before `header:mismatch` flags both Solana objects". Change the plan: two minutes for that rule (the second source's say).

Below the bar: the proposal title is the builder's "realm: kind", not "GovDAO's own field" (§5.3, M1).

## (b) Internal contradictions in the plan

**C266** §5.4 "both meters start empty at `Launch` (the realms are deployed weeks before it)" vs §5.4 "one cap in the first 24 hours after a deploy or an unpause of `payouts`" — C230's residue; "after `Launch`".
**C267** §5.3 "its builders validate parameters with the admin program's own rules, so a proposal that would fail on Solana is refused when it is made" vs §13 kind 9 "a realm has no RPC, so the proposer and `hlwatch` check it before the vote, as they check kind 8's program" — kinds 8 and 9 reach Solana unrefused; §8.4 names no such check.
**C268** §5.3 "set per-message maximum" (§5.4: 25,000, GovDAO-settable, "enforced at send") vs §13 kind 12, the pausable ISM's own 25,000 a message by a Solana decision — one limit, two votes on two chains, no rule binding or ordering them; a gno.land raise alone strands every send between the maxima (`OverMax`, parked) until a kind 12 lands 7 days after delivery. M3e: "the two sides' maxima are equal at launch by design" — the plan says nowhere that they move together, kind 12 first.
**C269** Glossary "a 'kind' is one of the eleven decision types of §13" vs §13's twelve, §6.5's kind 12, §13 "Kinds 3–9 and 12 are the 7-day group".
**C270** §2 "the state accounts … ≈0.13 together … the admin program's ≈0.01" vs §5.8 "≈0.15 of state and program accounts as built", §10 "≈0.15", AUDIT.md "≈0.15" — the admin state is 6,814 B → 0.035 (M2 choice 25): 0.11 + 0.035 + 0.01 ≈ 0.15; §2 carries v27's breakdown.
**C271** §2 "ours as built on 2026-10-12 (M2b …): … (397,640 B) … as built after the first audit round" — M2b's bytes were 271,160 / 147,760 / 338,128 (BUILD.md); the bytes named are M2d's of 10-13.
**C272** §1 "A set GovDAO has voted out loses this power once the set replacing it has settled (§6.1)", §6.1 "only the settled set for a recovery (§6.4) once its replacement has settled" vs §6.4 "the last settled set as the admin program has recorded it (a permissionless `settle` … the executor, and anyone, calls it after each quarantine)" — the power moves at the call, not at settlement, and nothing clocks the call (M3e: the updater and the executor send it).
**C273** §5.5 "`Claim`'s share an estimate until it is built", §10 the same, §5.8 "a pending decision or a staged recovery holds ≈0.002–0.003 more … an estimate until built" vs §10 "built on 2026-10-12" and §2's "as built" — the decision is built and measured (c); `Claim` is built and unmeasured.
**C274** §1 "The signers stop crediting the moment Solana's supply, the burns in flight counted, exceeds the lock (§5.6)" vs §5.6 "tight, not loose: … `supply + burns … > lock − owed − voided + lost`" — §1 states the loose form; with claims owed the signers stop below the lock.
**C275** §5.5 "three transactions of 5,000 lamports each", §10 "three transactions … a header", §6.3 "two or three transactions per header", "a 5-validator update over 3 transactions" vs §6.3 "five or six signatures exceed one transaction" and §2's six validators: the updater sends [init + precommits] · [precompile + verify, three signatures] … · [finish] (sol/client.go `UpdateTxs`, agent/updater.go `PerTx` 3) — four transactions for five or six validators, three only up to three.

## (c) Numbers recomputed (live: mainnet RPC at slot 456,195,643; rpc.gno.land/validators at height 760,314; r/gov/dao:10 and :13)

| Item | Plan | Recomputed / live | |
|---|---|---|---|
| Rent rate | §2 5,080/B; 0 B → 650,240 | 0 B 650,240; 1 B 655,320 → 5,080/B | ✓ |
| Deployer FagcHCr… | 7.36 | 7,356,770,000 lamports = 7.3568 SOL | ✓ |
| Validators | 6 × 60 = 360; forging 5 of 6 (241; any 4 = 240); override 180/180/240; halt 120 = 1/3 | six at 60, 360; 3s > 720 → 241; the others 180 (moul + g1aeddl), 180 (Jae + moul, Berty with moul), 240 (Jae + g1aeddl); moul + Berty 120 | ✓ |
| Prop #10, #13 | #10 open, moul to 1: 301; forging 4 of the five at 60 (201); share 181 (60%); 180/180/181; halt 61 of 301; #13 passed | #10 "open for votes", YES 33.33%; 301; 3s > 602 → 201; 1 + 3 × 60 = 181 = 60.1%; 180/180/181 < 201; 61 < 101; #13 "ACCEPTED" 66.67% | ✓ |
| ELF sizes | §2, §5.8: 270,888 / 164,432 / 397,640 → 1.38 / 0.84 / 2.02 | M2-NOTES the same bytes; (size + 45 + 128) × 5,080 → 1.3770 / 0.8362 / 2.0209 = 4.234 | ✓ |
| Rent sum | ≈8.9 | 1.0731 + 2 × 1.8184 + 4.234 = 8.944 (+0.005 of program accounts) | ✓ |
| "grew by a third" | §2, §11.4, AUDIT.md | 337,576 → 397,640 B: +60,064 = +17.8%; rent 1.716 → 2.021, +17.8% — a sixth | ✗ |
| State | §5.8, §10 ≈0.15; §2 0.13 = 0.11 + 0.01 + 0.01 | client 64 + 2,664 (two 6-validator entries 616 + 2,048 slack) + 256 × 72 = 21,160 B → 0.108; admin 6,814 B → 0.0353; pausable and Hyperlane PDAs ≈0.01 → 0.15 | §2 stale |
| Ask | need ≈10.6; shortfall ≈3.2; §5.8 3.2+1+1+1+0.2+0.1+0.3+0.1+1.1; §11.4 3.2+2+1+0.3+0.3+0.1+1.1; margin ≈1.1 | 8.944 + 0.15 + 1.5 = 10.59; − 7.357 = 3.24; both splits 8.0; 8 − 3.24 − 3 − 0.7 = 1.06 | ✓ |
| Authority PDA | §5.8 "≈0.001 per router enrolment as built"; 0.1 budgeted | M2 17b: 101 B → 702,960 lamports local (6,960/B) = 513,080 at 5,080 = 0.0005; `hlsol setup` funds 10,000,000 lamports = 0.01 (cmd/hlsol/main.go default) → 20 enrolments; 0.1 → 195 | 0.001 is local; 0.01 funded vs 0.1 budgeted |
| Decision rents | §5.8 "≈0.002–0.003 … until built" | kind 7: 153 B → 0.00143; kind 5 (four pairs): 246 B → 0.0019; cancel record 50 B → 0.00090 (M2's 0.0013 is local) | built: 0.0009–0.002 |
| Clocks at factor 8 | 24 h, 7, 14, 21, 28 d; dips 25 h / 23 h → 3.1 / 2.9 h; 14 d in 42 h | 3 h, 21 h, 42 h, 63 h, 84 h; 3.125 / 2.875; §5.9, M2 (63 h), M3 (21 h, 42 h, the 3-hour grace) agree | ✓ |
| Blocks | 176,327; 352,653; ≈353,000; an hour 1,050 | 604,800 / 3.43 = 176,326.5; 1,209,600 / 3.43 = 352,653.1; 3,600 / 3.43 = 1,049.6 (M3c `aheadBlocks` 1,050) | ✓ |
| Mint limits, base units | §13 25,000 GNOT / BUBBLE a message, 100,000 a day, 6 decimals | pausable config.rs 25,000,000,000 / 100,000,000,000; the e2e's kind 12 50,000,000,000 / 200,000,000,000 = 50,000 / 200,000 BUBBLE (M3e); its 30,000 × 10⁶ > 2.5 × 10¹⁰ → `OverMax`; the stand-in builder requires day cap ≥ maximum | ✓ |
| Updater's float | 0.3 SOL ≈4.6 months at 3 × 5,000 lamports a header every 10 min, ≈1 month at 500/d; §10 0.065 SOL/month, 0.3 at 500/d | at three transactions the four figures hold; as the code sends (four, 20,000 lamports): 144/d → 104 d = 3.4 months; 644/d → 23 d; 0.086 SOL/month (≈$9.5), 0.39 at 500/d | ✗ by one transaction |
| CU, §6.3 vs M2d's table | stage 32–45k; 13–17k a signature tx; finish 12–16k; proof 29/33k; delivery 105–130k, 223k with eight dispatches; kinds 2, 3, 5–7, 10, 11 18–44k; upgrade 164k + 21k a step; transfer 147k; `finish_setup` 43k | 35–47k; 14.1–27.3k; 13–17.5k; 29.1/33.8k ✓; 93.7–126k, 227.5k; kind 6 46.1k, kind 11 122.8k (40 validators) / 169.7k (60); upgrade 166.2k + 2,520 a top-level step; kind 9 163.9k; `finish_setup` 47.6k | seven stale |
| Economics | unchanged since v25 | not re-read; round 1's rows stand | — |

## (d) Terms before definition, parameters without a value

**C276** "high-water mark" (§6.5's successor list): undefined; M2 choice 24 defines it (`last_seq`, pending numbers, stored ranges).
**C277** "the executor" (§6.4 `settle`, §6.5 `ExtendProgram`): a Solana role the glossary never names — the relayer's loop (agent/executor_sol.go), primary and backup; the updater sends `settle` too.
**C278** "a named capacity" (§6.1): none named — 81,920 B body region, 10 KB a `Grow`, 2,048 slack; §2's 0.11 underived.
**C279** `hlrecover` (§11.10): first named undefined; the glossary names hlwatch and hlsol only.
**C280** The hold history's merge rule: a ninth hold merges the two oldest, earliest start kept (M2 choice 11) — unstated.
**C281** The authority PDA: 0.01 funded by `hlsol setup`, 0.1 budgeted — which the setup sends, where the rest sits, unsaid.
**C282** §13 "salt u64 — random": r/hlgov's is sha256 over block data (M1-NOTES); harmless, not random.
**C283** "the day's bucket" (§13): rolling, draining at cap/24 h from the last charge (M2 choice 26); AUDIT.md's "the next day" has no next day.

## (e) What a careful GovDAO member still refuses

1. The 7-day wait on any signer-rule change he was told of is not in the plan; §5.3's instant removal admits a rule change by silence (C256).
2. One limit, two votes: a gno.land raise of the maximum strands sends until a separate 7-day Solana decision; nothing orders them (C268).
3. The recovery's signers move only when someone calls `settle`; a voted-out set keeps the recovery until then (C272).
4. "Grew by a third" and §2's 0.13 are wrong numbers in the ask; the margin covers them, the text does not.
5. The updater's float is 3.4 months as the code sends headers, not 4.6 (C275).
6. §6.3's compute figures are stale one round later; kind 11 at 123–170k is three times the band stated.
7. hlrecover's YubiHSM path is "untested against a device" (M3-NOTES) and Jae's validator is the YubiHSM one; the signable power is uncounted.
8. The loop has executed no kind 8, 9 or 11, no real `settle`, no hold that ended, no `successor-init` on a chain (M3e): §8.4's "drilled" is all M4.
9. RESUME.md briefs a reader on v28, "6 more SOL" and "a permanent capture" beside a v29 plan.
10. `Claim`'s gas and the deposits stay "an estimate until built" after the build (C273); §10's cost line rests on them.

## (f) Verdict

Closer than v28, not at rest: the live facts hold to the lamport and the four rules round 1 forced are stated; the plan's own bookkeeping is off — two readings the member was told (any rule change waits, inflow above the bucket waits) are not in the text (C256, C261), the refund and credit arithmetic is misstated (C257, C258), a reset's clearing of the quarantine contradicts §6.1 (C259), the mint maxima are two unbound votes (C268), and five numbers are stale or wrong (a third, 0.13, three transactions, the CU table, 0.01 vs 0.1). Fold; one bounded round.

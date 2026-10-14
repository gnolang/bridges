# Audit round 1, audit 2 — the plan fact check with the builders' notes (PLAN.md v28, tree be69e1c, 2026-10-13)

Read whole: PLAN.md v28, REVIEWS.md's preamble and rounds 21–22 with the final fact check, M1-, M2-, M3-NOTES.md, BUILD.md, README.md. Live facts read once each (section c). Where a note's reading needed the code to settle it, the file and line are named. IDs continue the C series from C226.

## (a) Readings in the notes that the plan does not state or states differently

**C226 (high) — the client can evict its settled set.** Plan §6.1: "the validator-set history (one entry per set change …)" — no depth; §6.4: "more than 2/3 of the power of the last *settled* set". M2-NOTES §6: "Set history depth is 4 entries …; a recovery needs a settled entry among them." Code: `HISTORY_MAX = 4` (client config.rs:31; its comment "the settled one before them stays" is not implemented), `push_history` removes index 0 unconditionally (state.rs:95–100), and the admin program's `settled_set` searches that history alone, answering `NotSettled` (recovery.rs:75, 147, 231). Scenario: a hold stands, so nothing settles; four announced changes inside it — four one-block power votes by a pair, or four forged rotations under leaked keys — evict the pre-hold settled entry; the 14th day opens a window no set can sign in; the replacement of the holders lands on day 21. §11.15's override and §1's leak ending rest on this. Change the code: never evict the newest settled entry (evict unsettled ones past it; refuse a fifth unsettled announcement — a liveness cost only under a hold); then the plan states the depth and the pin.

**C227 (high) — the successor's counters and seats are init data.** Plan §6.5: "a successor starts at the predecessor's sequence number and recovery counter, takes its four seats and watch slots as they stand at the transfer". M2-NOTES §22: "initialised by its own `init` with the predecessor's last sequence number and recovery counter as init data". Code: `init` takes `last_seq`, `recovery_counter`, `guardians` (admin processor.rs:110); kind 9 moves authorities and nothing else (processor.rs:711–747); §13 refuses a kind 9 unless the successor is already immutable and §5.8 makes `init` need the authority's signature, so its numbers are at least 7 days older than the transfer. Scenario: §1's flood, then its cure — the successor inited at L0, the thief records to L1 > L0 until the transfer, r/hlgov (which only rises) sends L1+1, the successor expects L0+1: "refused and stays deliverable"; no hold on the fresh successor, so no window and no kind 10 — GovDAO's Solana channel is wedged for good. Change the code: kind 9's execution writes the live counters and seats into the successor through an instruction only the predecessor's authority PDA may sign; the plan says so.

**C228 (medium) — the 21st-day floor exists only if `Execute` is attempted under the hold.** Plan §6.5: "the admin program writes that floor into the decision the first time a hold defers it". M2-NOTES §11: "the first `Execute` that finds a hold standing … stores holdSince + 21 days … A hold whose 21st day has passed without any `Execute` attempt writes nothing … The executor's duty follows" — and the only executor built is the operator's relayer (M3-NOTES). Scenario: the operator's two seats hold (§1 allows it), its relayers stay quiet, the holders clear on day 19 and re-flag at the grace's last second: the new hold's 21st day — AM2's veto, back. Change the plan: the floor is written by the first `Execute` call under the hold, by anyone; a member's standing order (§11.9) and hlwatch's "not yet written" line are the guard.

**C229 (medium) — r/hlgov cannot check kinds 8 and 9.** Plan §5.3: "its builders validate parameters with the admin program's own rules, so a proposal that would fail on Solana is refused when it is made"; §13 kind 9: "the builder checks the same by RPC at creation". M1-NOTES: "What a builder cannot check on chain: kind 8's program (any non-zero key is accepted …) and kind 9's 'successor shows no upgrade authority' (a member checks by RPC before proposing, as §13 says the builder would)." Change the plan: a realm has no RPC; the proposer and hlwatch check the two kinds that move code and authorities, and §8.4 lists it.

**C230 (medium) — the meters are empty at `Launch`, not at deploy.** Plan §5.4: "both meters start empty at deploy and after every unpause"; §5.3: the realms "are deployed before the vote and refuse sends and credits until … `Launch`". M1-NOTES: "`Launch` also empties both meters of each route (the plan's 'empty at deploy' would leave them full by the launch vote, weeks later)". Change the plan: "empty at `Launch`" — else §5.4's "one cap in the first 24 hours after a deploy" is two caps on launch day.

**C231 (medium-low) — any signer-rule change waits 7 days.** Plan §5.3: "lowering the threshold is a raise of power and waits 7 days"; instant: "removing a signer while the verifier says its rule stays satisfiable". M1-NOTES §5.1: "Rotate signers at once only as a pure removal: the new set a subset of the old, the rule unchanged … any rule change (a raised threshold too: the plan's instant list names only removals) … waits 7 days." Change the plan: "any rule change waits 7 days" — a raised threshold can leave the rule unsatisfiable, the instant closing §1 forbids.

**C232 (medium-low) — the refund-by-id builder and the identity.** Plan §5.3's builder list has no refund; §5.9: "lock = sentTotal − paidTotal". M1-NOTES: "Refund by id is a builder on the route itself (7 days, from the lock to the sender …)"; "a refund by id reduces sentTotal and the lock together". Change the plan: list the builder (a funds move by vote, §3.1) and the counter rule the monitors and hlwatch evaluate.

**C233 (low-medium) — credit by id for a voided claim.** Plan §5.3: "replaces the restored remainder with the recipient and amount GovDAO names … so the identity does not move". M1-NOTES: "Owed moves by (amount − old remainder); the identity holds only when GovDAO names the remainder". Change the plan: "so the identity closes on the amount GovDAO names" — the diverted-credit cure wants the true amount, and the watch must expect owed to move.

**C234 (low-medium) — the client's third control.** Plan §5.8: "its two controls are its upgrade authority and the reset of kind 11". Code: `Instruction::SetAdmin` (client instruction.rs:74), which kind 9 CPIs to hand the reset role on (admin processor.rs:747); M2-NOTES: "reset and set-admin by the admin only". Change the plan: name it as what kind 9 moves; the audit prints the reset authority already.

**C235 (low) — the staged proof's seed and metadata.** Plan §13: "a staged proof (`proof`, submitter, proof hash)". M2-NOTES §2: "(`proof`, submitter, message hash). The ISM's `metadata` is the submitter's pubkey". Change the plan (§13).

**C236 (low) — a reset closes the lapse window.** Plan §6.4: "a fresh accepted header closes a lapse-opened one". M2-NOTES §5: "A reset … sets the last-accepted time, which closes a lapse window — the reset is the last decision of a recovery procedure, so earlier ones in the same batch must execute first." Change the plan: a kind 11 executed first voids every other lapse-opened recovery decision; §6.4's procedure and §11.10's tool order it last.

**C237 (low) — the trusted header must not be announcing.** Plan §6.1: "the deploy writes a trusted header less than an hour old"; §13 kind 11 names any "trusted header hash … and height". M3-NOTES: "A trusted header that is itself announcing cannot be followed: the client's init takes the set of the trusted height only … `hlsol setup` takes the header before it". Change the plan: the deploy's and kind 11's header is non-announcing (a reset to one strands the client until the next reset); the validators' tool checks it.

Below the bar, not counted: §13's "salt u64 — random" is in r/hlgov sha256 over block data (M1-NOTES) — harmless, not random; §5.7's "no single transaction's error pins the cursor" is read by the signer as a *delivery* error, a burn-scan fetch error does pin (M3-NOTES §5.6); M2-NOTES (16 pairs exceed a transaction) and M3-NOTES (the relayer passes 16, no lookup table) disagree — for the Go audit; §5.6's public checkpoint store is unbuilt.

## (b) Internal contradictions in the plan

**C238** §6.5 "as they stand at the transfer" vs §13 kind 9 "refused unless the successor's programdata account shows no upgrade authority" and §5.8 "`init` requires the signature of that program's current upgrade authority": init, burn, propose, 7 days — "at the transfer" cannot be init data (C227).
**C239** §5.4 "empty at deploy … one cap in the first 24 hours after a deploy" vs §5.3 "deployed before the vote … until `Launch`" (C230).
**C240** §13 "the builder checks the same by RPC at creation" vs §2's realm model ("a closure that returns an error … a panic leaves it for a retry") — no outside call exists (C229).
**C241** §1 "a proposal passes the instant the votes are in" vs §2 "acceptance is tallied at execution": under impl v0 the status moves at the execute call, which is why §5.3's "pre-armed" proposals exist.
**C242** §5.8 "its two controls" vs §13 kind 9 "transfer every authority to a successor": the reset role is an authority kind 9 must move (C234).
**C243** §5.9 "four runs of up to 72 h each" vs §10 "the 72 h run and the drills" (BUILD.md M4 too).
**C244** §6.3 "105–130k (… every figure the maximum seen per transaction)" vs §6.2's 8 KB leaves: M2-NOTES measured 223,000 CU for a leaf carrying eight dispatches.
**C245** §10 "the v27 code changes follow within days" vs the status line's v28 of 2026-10-12 and BUILD.md's M1b/M2c/M3c landed that day; §2's "as built" bytes are v26's (c, row 7).
**C246** §6.4 "‖ recovery counter ‖" and §13 "‖ counter u64 BE ‖" never say stored-or-next; M2-NOTES §12 built "the counter plus one" — a tool signing the stored value signs a dead digest.
**C247** §1 ¶2 names only "a signer-rule change that lowers the threshold" slow and §5.3's instant list names removals only; a raised threshold is in neither (C231).

## (c) Numbers recomputed (live facts read once: mainnet RPC, rpc.gno.land/validators, r/gov/dao:10)

| Item | Plan | Recomputed / live | |
|---|---|---|---|
| Rent rate | §2 5,080/B; 0 B → 650,240 | 0 B 650,240; 1 B 655,320 → 5,080/B | ✓ |
| Deployer FagcHCr… | 7.36 | 7,356,770,000 lamports = 7.3568 SOL (slot 456,141,154) | ✓ |
| Validators | 6 × 60 = 360; forging 5 of 6 (300; 241 needed); override 180/180/240; halt 120 = 1/3 | height 757,033: 6 × 60 = 360; 3·s > 720 → 241; the sums as stated | ✓ |
| Prop #10 | "open, moul to 1"; 301; 201; 181 (60%); 180/180/181; 61 | render: open, YES 33.33%; 301; 3·s > 602 → 201; 1+60+60+60 = 181 = 60.1%; 180/180/181; 61 | ✓ |
| Hyperlane rents | 1.07; 1.82 × 2 | (211,069+128)·5,080 = 1.07288; (357,789+128)·5,080 = 1.81822 | ✓ |
| Ours, v26 bytes | §2 1.38 / 0.75 / 1.72 = 3.85 | 271,160 → 1.37814; 147,760 → 0.75127; 338,128 → 1.71834; Σ 3.84775 | ✓ |
| Ours, v27 bytes (M2-NOTES, BUILD: 271,160 / 146,808 / 337,576) | plan carries v26's | 1.37814 / 0.74643 / 1.71554 = 3.84011 (M2-NOTES' "1.71" truncates) | §2/§5.8 stale by 0.008 |
| Six programs | ≈8.6 | v26 8.55707; v27 8.54943 (rounds to 8.5). All omit the 45-B programdata header and 36-B program account: +0.00106 each, +0.0064 in all | ✓ at v26 |
| State accounts | §2 "≈0.05 together (0.04 of state …)" | client state after three `Grow`: 64 + 2,872 (822-B body + 2,048 slack) + 256·72 = 21,368 B → 0.1092 SOL (0.150 locally); admin state ≈1.5 KB → ≈0.008; pausable and Hyperlane PDAs ≈0.01 → ≈0.13 | ✗ 0.13, inside the 0.5 margin |
| Need, shortfall | ≈10.1; ≈2.8 | 8.557 + 0.05 + 1.5 = 10.107; − 7.357 = 2.750 (2.83 with the row above) | ✓ |
| The 7 SOL split | §5.8 2.8+1+1+1+0.2+0.1+0.3+0.1+0.5; §11.4 2.8+2+1+0.3+0.3+0.1+0.5 | 7.0 and 7.0; 4·0.05 and 2·0.05 per §5.6 | ✓ |
| Floats after funding | 2 SOL ≈2,100 records, 42/4 d; 1.25 ≈830, 33/3 d; 0.25 trips the alarm | 2,105; 42.1; 4.2; 833; 33.3; 3.3; 0.25 → 167 → 6.7 d < 7 | ✓ |
| Records behind the rents | 0.00095 / 0.0015 / 0.0016 (0.0013 / 0.0021 / 0.0022 locally) | 59 / 167 / 187 B at 5,080; the same bytes at 6,960 → 0.00130 / 0.00205 / 0.00219 | ✓ |
| Authority PDA | §5.8 "≈0.001 per router enrolment as built"; 0.1 SOL | M2-NOTES: 101 B → 702,960 lamports locally (101·6,960) → 513,080 at 5,080 = 0.00051; 0.1/0.00051 = 195 | 0.001 is the local figure |
| Pending decision rent | §5.8 "≈0.002–0.003 … until built" | `Decision` 113 B + params: kind 7 → 153 B → 0.00143; kind 5 → 246 B → 0.0019; cancel record 50 B → 0.00090 (M2-NOTES' 0.0013 is at 6,960) | built: 0.0014–0.002 |
| Clocks at factor 8 | 24 h, 7, 14, 21, 28 d; dips 25 h / 23 h → 3.1 / 2.9 h | 3 h, 21 h, 42 h, 63 h, 84 h; 3.125 / 2.875 — §5.9, M2 (63 h), M3 (21 h) agree | ✓ |
| Blocks and slots | 176,327; 352,653; 216,000; 1,512,000; an hour 1,050 blocks (M3c) | 604,800/3.43 = 176,326.5; 1,209,600/3.43 = 352,653.1; 86,400/0.4; ×7; 3,600/3.43 = 1,049.6 | ✓ |
| CU, §6.3 vs M2-NOTES | update ≈60k (32–45k; 13–17k; 12–16k); proof 29/33k; pausable 45k; delivery 105–130k; kinds 2,3,5–7,10,11 "18–42k"; upgrade 164k +21k/step; transfer 147k; finish_setup 43k | 32–45k; 12.6–17.1k; 11.9–15.6k; 29.1/32.8; 44.6; 105–126k but 223k with eight dispatches; 17.6k (kind 10) … 44.2k (kind 6); 164.3; 21.2; kind 9 145.5k; 42.8 | three stale: 223k, 44.2k, 145.5k |
| Economics (prices of 2026-10-10, not re-read) | $0.054 / $0.021; $0.10 / $0.16; $5.50; 1.4 + 0–2.3 SOL ($150–400); 135 GNOT; $1,035; 500/d 14–37 SOL, 1,350 GNOT, ≈$10,000; updater 0.04–0.07 SOL, 5–7 months; $41; $1,800–2,900; 52% / 57%; 8–10 d; $0.27 | 0.0537 / 0.0208; 0.104 / 0.164; 5.48; 1.425 / 2.25 → $153–405; 135; 1,035; 14.25–36.75, 1,350, 10,350; 0.043–0.065, 4.6–7.0; 41.4; 1,770–2,880; 0.522 / 0.570; 8.3–10; 0.27 | ✓ |
| Test-validator ratio | 37% | 6,960/5,080 = 1.370 | ✓ |

## (d) Terms before definition, parameters without a value

**C248** "pre-armed" (§5.3, §5.8, §8) is never defined; M3-NOTES: a builder-made proposal with the votes GovDAO's law needs and no execution.
**C249** the digest's "recovery counter": stored or next is unsaid; built as next (C246).
**C250** the set history's depth and eviction rule: none in the plan (C226).
**C251** the stored cancelled ranges: no bound and no refusal in the plan; 64 disjoint per channel, `StoreFull` refuses the cancel at recording (M2-NOTES §13) — for a recovery cancel a window-time failure §11.10's tool must answer by passing pairs.
**C252** the client state account: no size, no `Grow`; §2's "0.04 of state" has no derivation.
**C253** "an estimate until built" after the build: §5.5 and §10 on `Claim`, §5.5's deposit "unmeasured", §5.8's decision rent — M1 built `Claim`, M2 the decision; none re-measured.
**C254** a guardian's `payouts` id list: no cap in the plan; 256 in the code (M1-NOTES).
**C255** "compiled by themselves to the published hash" (§5.8, §8.1): nothing pins hlwatch's toolchain (M2-NOTES pins platform-tools v1.51 for the programs; the Go version is nowhere).

## (e) What a careful GovDAO member still refuses

1. The recovery can be made unsignable: four set changes under a hold evict the settled set (C226); the override and the leak ending are then words.
2. The cure for a wedged or thieved admin program wedges its successor: init-data counters after a flood (C227).
3. The 21st-day floor is written only by an `Execute` the operator's machines make, and the operator may be the holder (C228); no order tells a member to call it.
4. "Refused when made" is false for the two kinds that move code and authorities (C229); the member is the check.
5. The validators' signing tool, the checkpoint store and the comparator are unbuilt; no machine has driven the recovery path ("exercised by the vectors only", M3-NOTES) — §11.10's gate and §5.9's drills have no tool yet.
6. Nothing in §8.4's "drilled" list has been drilled: the e2e executes no matured Solana decision, no hold, no recovery, no kind 8 or 9; the fixtures replay three of the four set changes (741,862 is not among them).
7. The ask's arithmetic is stale: v26 bytes, the client's state at 0.11 SOL not 0.04, three CU figures — the margin covers it, the text does not.
8. r/hlgov's seat mirror can sit ahead of the truth after a failed kind 5 (M1-NOTES); its page is not the seats.
9. The signers' one-hour stop is judged against rpc.gno.land, a GovDAO member's endpoint.
10. The launch-day "one cap" bound is the code's doing (`Launch` empties the meters), not the plan's (C230).

## (f) Verdict

Not consistent end to end: two readings the notes record as choices are rules the plan never made and the code got wrong — the settled set can be evicted under a hold (C226) and a successor inherits stale counters (C227) — and each voids a promise §1 makes (the validators' override, the cure by kind 9). Fix both in the code first, then fold C228–C237 and the ten contradictions into v29. The live facts hold to the lamport; the stale numbers are v26 bytes, the client's state rent and three CU figures. One bounded round after the fold.

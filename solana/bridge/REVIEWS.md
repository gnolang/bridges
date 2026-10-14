# Review log of bridge/PLAN.md (its §12)

Moved out of PLAN.md on 2026-10-12 (plan v22) to keep the plan readable; the section numbers cited are PLAN.md's,
and the current rule is always the one in PLAN.md §1–§11 and §13. v22 (Jae's decision of 2026-10-12) removed the
guardians' veto, the standing veto, the affirmation rule and the 28-day stretch that v5–v21 carried; findings and
answers about them (rounds 5–18) are history, and "a veto" in an answer below reads "a hold, until GovDAO
replaces the holders".

v23 (round 19's fold, 2026-10-12): a replacement of guardians under a hold lands on the hold's 15th day at the
earliest (the 21st since v26), and in the window a 14-day hold opens the last settled validators may cancel any
pending decision (§6.4–§6.5, §11.15); so "a hold, until GovDAO replaces the holders" reads "a hold of 21 days at
least, until GovDAO's replacement lands uncancelled". Also history at v22–v23, marked ⟂ v23 below and listed here (the fact
check's C189–C197): AA4 and every held line on "an awake outsider with an honest partner" (rounds 12–18 — a stolen
or rogue key is out when GovDAO's replacement lands); the lines that read "a replacement is deferred or refused
under a hold" (C31, V6, X6, F1: it lands on the 21st day since v26, the 15th in v23–v25); AB8/AC7's refusal of a mixed-seat kind 5 (gone with the
veto count); Y5's seat numbering (kept for the audit and the notice only); round-16 (d)'s clock factor 16 (now 8);
C52's "veto the backlog" (now: a recovery cancel of each foreign decision, then the corrective decision, then
lift); and the chains whose last ⟂ points at a v22-dead rule (C30, F4, G9, T4, Z8, U3) read as first written,
with v23's 15 days — 21 since v26.

v24 (round 20's fold, 2026-10-12): the hold's start is the object's `holdSince` (a dip below two flags shorter than
24 hours keeps it; a dip of 24 hours ends the hold), flags carry one fresh slot, and a recovery cancel carries no
counter and may name a range (§6.1, §6.4, §13). The fact check's C198–C204 are marked below: Z5 (the channel byte
is back), AG7 (the start rule), AE14 (two lost keys are a hold), AA4 (the missing mark), round-16 (b)6 (one closure,
not three proposals: the void builder's `andUnpause` takes the unpause builder's parameters), and AJ2/AJ3's answers
corrected in place.

v28 (the final fact check's residues, 2026-10-12): `Launch` carries the 14-day deadline (AG9 and round-16 (d) read
"no deadline"); kind 4 is reserved (round-20 (b) B9–B11, AH3, round-17 (b)1 and its chain, AB7 read it as a live
setter); a rotated-out set is powerless once its replacement has settled (round-6's held line read "once
followed"); the economics are AI8's band (round-17 (b)6's $510–880 is superseded); a lapse-opened recovery
decision waits until the hold ends, its kind 5 landing on the 21st day; the written floor binds only while a hold
stands. Rows marked ⟂ v28 (C219–C225).

v25 (round 21's fold, 2026-10-12): Prop #13 executed at block 741,860 (18 minutes before v24 was committed), so
the set is 6 × 60 = 360, forging is 5 of 6, the member-linked share is exactly 2/3 and the validators' override can
be signed against no GovDAO pair today or under Prop #10 (§1); a replacement's clear that leaves fewer than two
flags ends a hold at once (the 24-hour grace is for a holder's own clear), "a hold stands" reads `holdSince` for
every rule, and the dip is Clock time (§6.1, §6.4, §6.5, §13). The fact check's C205–C210 are marked below: AJ9
and round-19 (d)'s sticky slot (gone at v24), AJ6's "at any time", AJ3's `set sequence` (now `sync sequence`), F3
and AA6 (the counter: not on a recovery cancel), AJ7 and round-20 (b) B6 (overtaken by Prop #13).

v26 (round 21's red team, 2026-10-12): a recovery cancel's digest carries the window's anchor and is spent once
executed (AL1); a replacement of guardians under a hold lands on the hold's 21st day, not the 15th (AL4 — a week
for five of six validators to sign), so every "15 to 22 days" of v24–v25 reads "21 to 28"; a lapse-opened recovery
decision is deferred by a hold like a mailbox decision (AL2); the three Solana programs were built and measured
(M2): rent ≈8.6 SOL, the ask 7 SOL.

v27 (round 22's fact check, 2026-10-12): the 21-day sweep's paraphrases ("22 days", "a day", "the window's second
day", "15 to 22" wrapped) are the 21st day everywhere; §6.5's bound reads "7 to 21 days after delivery for a
replacement delivered under the hold, 21 to 28 for one delivered up to 7 days before it"; a lapse-opened recovery
decision's deferral is in §6.5 too; the rents are at the built ELFs' bytes at v26 (3.85, ≈8.6 in all, the ask still 7);
§5.9's run is three runs; §10's build weeks are spent. Rows marked ⟂ v27 below (C211–C218): AJ1, AJ2, AJ11, AK8,
AK2, round-20 (b) B3/B13 and (b) B5, round-21 (b) B10/B11, W5, C52 (the never-deferred recovery), round-13 (c) and
the C66 chain (the rent and the ask).

## The log

Round 1 (2026-10-10): six reviewers — the gno light client on Solana (L), the Alpenglow side (A), governance and
operations (G), economics and liveness (E), and two adversarial code reviews of the Phase 0 realms (R) and the agents
and Solana setup (S). The section numbers inside the E, G, A and R answers refer to plan v1 (v1 §5.4 → §5.5, §5.5 → §5.6, §5.6 → §5.8,
§5.7 → §5.9, §6.4–6.6 → §6.5–6.7, the v1 §7.4/§9 burn rows → §3.6/§6.5, the lazy-execution row → §7.1); S and L
answers use v2 numbers, which v3–v8 kept. The rounds are listed in the order they ran, on 2026-10-10 and 11. An
answer a later round overturned is marked ⟂ with the finding that overturned it; the current rule is always the one in
§1–§11; a "checked and held" line is a reviewer's holding at the time and is not marked (the four on a stolen
outsider key — rounds 7, 8, 9 and 10 — were overturned by AA4: a thief with a silent partner keeps the seat). Where
an answer below says "validators" of the three machines, read "signers". v16 (2026-10-11) removed Phase 0 — the
temporary signer bridge, the interim multisig (Squads, Jae's two devices, the one-day lock), the hand-over ("the
bootstrap" in rounds 1–13), the Hyperlane multisig ISM and its rotation window (kind 12), and the signers' software
quarantine — so the findings and answers about them are history, among them G1, T10, U10's multisig unpause, C24,
C31, C53, C60–C62, X5, AA9, B-4/B-5 and AD1, and (round 14's list) S4, G12, E7, R7, F7's software quarantine, U12,
C32, C77, round-7 (b)4 and (b)9–(b)12, AC3, round-9 (b)1 and (b)3–(b)7, AB7, round-11 (a)'s kind 12, round-12 (b)4's Solana threshold, B6–B13's
"why Phase 0 first" and round-8 (e); a pointer to §6.7 reads §5.4 (the maximum) or §6.6 (the vote), a pointer to
§11.3 reads "moot"; the held lines of rounds 1 (Squads), 7 (the queued bootstrap) and 9 (both outsiders lost in 7
days) are history too. Where an answer below says "Phase 1", read the launch; "Phase 2", Proposal 2; a deployer's `Bootstrap`, GovDAO's
`Launch` builder (v18); "copied into the pausable ISM", written by its own init (v18); "the setup transaction", the
setup step of several transactions (v17). The findings about the realms, the claims, the signers' gno.land work, the guardians, the
admin program and the light client stand.

### Round 1 — economics, liveness, incentives

Prices used: GNOT $0.069, SOL $109.6. Findings, each with the plan's answer (applied in v2 where a section is named):

| # | Sev | Finding (confirmed unless marked) | Answer |
|---|---|---|---|
| E1 | critical | Anyone halts gno→Solana for $0.003: `hlmailbox.Dispatch` is open to any caller, the relayer picks messages by destination+recipient only, and its loop stops at the first failing message and retries it forever (relayer.go:107-149; a non-router sender fails Solana preflight). | Relayer: `ours` requires sender == the enrolled gno router; a per-message failure count parks a message after N deterministic failures and moves on, with an alert; backoff per message. Mailbox: Dispatch stays open (it is Hyperlane's shape) but the routes are the only senders Solana accepts. §5 |
| E2 | high | ATA-payer drain: 0.1 SOL funds 47 first-time recipients; then every delivery to a new wallet fails and (E1) blocks the queue. We pay ≈$0.37 per gno→Solana delivery (rent for the delivery record is never recoverable, no close instruction), the sender pays ≈$0.005. Dust floods cost us 75× what they cost the attacker. | A flat fee (start 5 GNOT, GovDAO-settable) and a minimum transfer (10 GNOT; BUBBLE equivalents), paid to a fee realm the treasurer draws from; the Solana mailbox initialized with `max_protocol_fee` 0.05 SOL and fee 0, so a fee can be switched on later without a new mailbox (E8). ⟂ U7, then ⟂ C66: the floats are 1 + 0.25 + 0.25 SOL at launch, 2 / 1.25 / 1.25 after §11.4's 4 SOL, and the alarm is 7 days of the trailing rate, not 100 recipients (§5.5, §5.8, §11.4). §5.5, §5.8 |
| E3 | high | "Loss bounded to one day's ceiling" is wrong: Solana minting is uncapped (forged signatures mint without limit, sold into whatever liquidity exists); the pause guardian is the same 2-of-3 machines, so with two compromised nobody can pause; GovDAO takes days; signers trust one RPC each (if the three share rpc.gno.land, 2-of-3 is 1-of-1 on that endpoint). BUBBLE cap in code is 1,000,000/day vs 10,000 in the plan. | Each signer runs its own gno full node and uses distinct Solana RPC providers over TLS; the code's cap is to be replaced by §5.4's meter before launch (§5.9 — still 1,000,000 on the branch); §9 restated: the bound is cap × days to detect (⟂ C51: one cap from an empty meter, two caps in the worst 24 hours), plus Solana liquidity for synthetic dumps. ⟂ T4/U4: no guardian key on a signer host; four guardians and the watch keys (§1, §3.3). ⟂ U10: Solana's unpause is the multisig's until the bootstrap, then a decision (§6.5). ⟂ round-3 fact check: the counters are sentTotal and paidTotal (defined in §5.9). "A release burst" is not a trigger: a signer that detects a violation stops signing and alerts, it holds no pause key (§5.3, §5.6). §5.3, §5.5, §9; ⟂ v16: a 24-hour `unpause` decision from the first block (§6.5). |
| E4 | high | gno cost model 30× off: the relayer pays a flat 1 GNOT fee with 100M gas wanted and tm2 refunds nothing; a failed tx still pays. Two active relayers race and one loses the fee per message. | Gas wanted ~45M, fee at the chain's minimum price; simulate first (gnokey's default); primary/backup relayers 60 s apart (§5.7), never two actives. §10 derives 1,350 GNOT/month at 500/day (Process + Claim) and 135 at 50/day. |
| E5 | medium | Caps: UTC reset doubles at midnight; no per-message max, so a message above the cap is stuck forever with the synthetic already burned (no refund path on either side); one holder can monopolize the return path daily; first-come ordering starves large transfers; users see no queue position. | Token bucket (rate cap/86,400 per second, burst cap); a per-message maximum enforced at send on gno and documented for Solana (⟂ C16: a fixed 2,500, a quarter of the floor, raised by vote, not cap/4); cap = max(floor, 10% of Locked). Two-step delivery: `Process` always marks delivered and credits a claim. ⟂ T9/U8: the per-recipient daily cap became a per-recipient bucket, `Claim` pays per recipient queue in credit order and, since F6, in turns (§5.4); the render shows position, not an ETA. The cap base went to outstanding liability in v3 and back to the lock in v5 (F10). §5.4 — ⟂ v16: 25,000 GNOT and 25,000 BUBBLE from launch (§5.4). |
| E6 | medium | A signer `log.Fatal`s on a nonce conflict (a forked RPC or a chain reset kills it, leaving 2 with zero margin); signatures live only on each signer's disk and HTTP, so delivery of any message needs two live endpoints and a dead signer's past signatures are gone. | Relayer caches signatures on disk; signers also publish to a shared store (as Hyperlane does with S3); a conflict pauses signing and alerts instead of exiting. §5.6 |
| E7 | medium (hand count) | Legacy Solana transactions fit at most 4 signatures (1,249 bytes at 5 > 1,232); a threshold of 5 would make every message undeliverable. | Threshold stays ≤ 4 until the delivery uses address lookup tables; noted in §5.6. |
| E8 | low | The mailbox's max protocol fee is fixed at init (0 in our setup). | See E2. |
| E9 | low | lock = supply only holds with nothing in flight. | Monitor checks the identity up to in-flight messages; since v4 the counters are sentTotal and paidTotal, defined in §5.9 with the identity `lock = sentTotal − paidTotal = Solana supply + owed + voided` — ⟂ B7: `− lost` since v9. |

Checked and held: decimals and overflow handling; gno recipients never undeliverable; Solana→gno junk filtered by sender; refusals precede the delivered mark; storage deposits separate from the lock; outage behaviour (finalized-only signing and counting); the relayer decides nothing; delivery is permissionless but nobody will relay for free given unrecoverable rent, so it is not a liveness guarantee.

Costs restated (500 transfers/day each way): ≈35 SOL/month of unrecoverable Solana rent (delivery records and new recipients' token accounts) ≈ $3,850/month, plus ≈1,300 GNOT/month on gno once tuned; only program rent is held. This is why the routes charge a fee (E2). (v5: at the mainnet rent rate the figure is 14–37 SOL; §10.)

### Round 1 — the agents and the Solana setup, adversarial

| # | Sev | Finding | Answer |
|---|---|---|---|
| S1 | critical | The signers are unauthenticated-RPC oracles: a signer signs whatever one gno RPC's `vm/qrender` (no proof) and one Solana RPC return; the shared config gives all three the same endpoints, so a compromised or hijacked endpoint makes 3 of 3 sign a forged token message (unbounded synthetic on Solana; `dayCap` a day from the lock). The equivocation guard trips only after the forged signature is served. | Each signer runs its own gno full node and needs two independent Solana RPC providers to agree, no pair shared (§5.6); the gno node's commit verification is the proof. |
| S2 | high | Anyone stalls gno→Solana for 0.03 GNOT: `Dispatch` is public, the relayer selects by destination+recipient only, and its loop never advances past a message that fails deterministically. | Sender filter, parking after N failures, backoff (§5.7). |
| S3 | high | One Solana transaction with >100 account keys poisons the scan for every agent: `GetMultipleAccounts` is capped at 100, the error pins the cursor, and a v0 transaction with a lookup table that names the route program (~0.02 SOL) halts signing and relaying of every later dispatch. | Chunk by 100, fetch only candidate accounts, never let one transaction's error pin the cursor (§5.7). |
| S4 | high | Every Solana control key stays with the hot deployer (as G1). | §5.8 hand-over of every authority; `hlsol audit` (to be built) reads them back. |
| S5 | high | The relayer's gno key is passwordless and may be the realms' admin (the e2e runs it as the admin key; in production the hotkey). | A dedicated gas-only key; GovDAO admin from the first block (§5.3, §5.7). |
| S6 | medium | The payer can be smuggled into CPIs as a signer: dynamic account metas are refused only when `is_signer`, not when `pubkey == payer`; the compiled transaction folds a payer meta into the signer slot and the mailbox forwards `is_signer: true` (Hyperlane's relayer rejects exactly this). | Reject any dynamic meta equal to the payer (§5.7). |
| S7 | medium | Relayer-pays economics inverted; amount-0 transfers are legal on Solana and cost the relayer 1 GNOT each; the ATA payer is a griefing lever (as E2). | Fees both ways, minimums, amount-0 refused and skipped, ATA runway alerts (§5.5). |
| S8 | medium | A non-"0" render answer (an error text, a changed render) is taken as "delivered" and the message is dropped with the cursor already past it. | Parse as an integer; keep the message otherwise (§5.7). |
| S9 | medium | The cap bound is understated: 2× `dayCap` across UTC midnight, code default 1,000,000 BUBBLE, no Solana-side cap or pause. | Token bucket, caps aligned, pausable ISM on Solana (§5.3, §5.4). — ⟂ round-5 (a): the code's day cap is pre-plan; "aligned" was false. |
| S10 | medium | Stalls and equivocation are silent: `log.Fatalf` crash-loops under a supervisor; a disagreement makes the relayer wait forever with a log line; a stall looks like "nothing to do". | Alerts on no-progress-with-backlog, on a conflict, on delivery age (§5.6, §5.9). |
| S11 | low | Equivocation store keyed by nonce only; `hlsol` defaults point at Hyperlane's shared mailbox and the shared test domain; the dumped ISM binary predates the low-S and dedup strings in the source read (our signatures are canonical anyway); plain HTTP and no body limit on the relayer's client; `sent` kept in memory (one duplicate send after a restart); a Solana sender pasting a raw pubkey as the gno recipient loses funds; a recipient equal to hlbubble's address sinks the amount. | Fresh store per deployment; required flags and pinned binary hashes (§5.9); HTTPS and a body limit; recipient checks in the client (§5.7); `to == Address()` refused and such burns swept (§5.2, §5.4). |

Held: the dispatched-message record layout, owner, slot and nonce checks are correct and a forged record is impossible (the route's dispatch-authority PDA must sign); the checkpoint digest matches Hyperlane byte for byte and signatures cannot cross directions, domains or hooks; the relayer verifies every signer response; the ISM the simulation reports is re-derived on chain; replay protection on both chains; router padding; decimals; no shell injection through gnokey arguments; key files 0600; state files atomic.

### Round 1 — governance, keys, operations

| # | Sev | Finding | Answer |
|---|---|---|---|
| G1 | critical | Phase 0's deploy leaves every Solana owner on the hot deployer key: mailbox owner, ISM owner (no transfer instruction even exists in our tool), both token owners, both mints' metadata-pointer and metadata-update authorities. The plan hands over only the four upgrade authorities. The ISM owner alone can set `[attacker], threshold 1` and mint without touching a signer; the token owner can enroll an attacker realm as the gno router; the metadata authorities can rename the token forever. | `hlsol audit` and `hlsol transfer` (both still to be built, §5.8): the audit prints every authority; the transfer moves all of them in one transaction (five programs in v3+: SetAuthorityChecked ×5, five TransferOwnerships, metadata update authority → none after name/symbol/URI are final, metadata pointer authority → none) and audits again; the acceptance test reads all of them back. The interim holder is a Squads v4 multisig (Jae's two hardware devices) with a one-day time lock, not a lone device; the devices sign offline-built, independently decoded transactions. §5.8, §8 |
| G2 | critical (trust claim) | "No trusted party" is false in every phase: GovDAO on gnoland-1 is three T1 members with a 2-of-3 supermajority, the same keys that set the validator set and can replace GovDAO's own rules. The light clients then prove "what 2 of 3 NT keys decided". | Said plainly in §1, §3 and every proposal: "GovDAO = N members, this vote is K people". Considered: BUBBLE's own holder governor for the BUBBLE route; GovDAO for GNOT and the mailbox. §1, §3, §8 |
| G3 | high | "Admin = GovDAO" cannot work with the realms as written: a GovDAO proposal executes a callback whose `cur.Previous()` is r/gov/dao only inside the callback's own realm; a cross-call from it reaches our realm as the wrapper, not GovDAO. The working v1.2.0 pattern (r/sys/validators, r/gnops/valopers) is that the governed realm exports `New…ProposalRequest` builders whose closures call its gated functions non-crossing, gated on `Previous().PkgPath() == "gno.land/r/gov/dao"`. Executors must return errors, not panic. Once GovDAO is admin the hotkey cannot do Phase 0 setup. | Realms get builders; `SetAdmin/AcceptAdmin` dropped; Phase 0 signers and routers set by a one-shot deployer `Bootstrap` that disables itself. The week's delay is built into our realms (`Queue` → anyone executes after N blocks → GovDAO `Cancel`), since GovDAO itself has no voting period, time lock or expiry (G7). ⟂ F12: a closure returns an error only past its deadline and panics on anything transient, because the proxy denies for good on an error (§5.3). §5.3 — ⟂ AE2, AF3: every launch value is compiled in and GovDAO's `Launch` builder makes the realms live (§5.3). |
| G4 | high | The guardian pause cannot stop gno→Solana: Hyperlane's token program and ISM have no pause; only the token owner can act (un-enroll the router, set a deny ISM). Two stolen signer keys at 02:00 mint unbounded synthetic until a human signs. | Our own ~100-line pausable ISM program on Solana from day one (registered keys set flags; `verify` errors while any flag is set, else CPIs the real ISM), set as both tokens' ISM — ⟂ T4: the guardians and the watch keys, not the signer machines, pause; §3.3, §5.8. |
| G5 | high | The signers' real root of trust is one RPC URL per chain and one cloud account: a signer signs whatever `vm/qrender` (no proof) and one Solana RPC say; three droplets in one DigitalOcean account with the token on the Mac collapse to one account. | Each droplet runs its own gno full node (its BFT commit verification is the proof) and requires two independent Solana RPC providers to agree; three providers or accounts; no shared API token on the Mac; keys generated on-box and never backed up; a refusal rule: Solana supply > lock + in-flight ⇒ stop signing and alert — ⟂ T4: no pause key on a signer; ⟂ round-7 (b)5, then ⟂ B7: the rule is `supply > lock − owed − voided + lost` (§5.6). §5.6 |
| G6 | high | The admin program as specified: proving "the proposal's result" means parsing GovDAO's internal bptree, which changes with every impl upgrade; the light client is its only input, so a light-client bug plus the week replaces the light client itself; the loader's Upgrade takes effect at once; a program cannot upgrade itself from its own top-level transaction; a gno chain-id change kills the light client forever and, after a burn, strands Solana. | Decisions travel as Hyperlane messages dispatched by our own governance realm when GovDAO executes a proposal; the admin program is a mailbox recipient under the light-client ISM (replay = the processed-message record). ⟂ T8/U2/F2: it checks origin, sender and the sequence number, not origin and sender only (§6.5). Tiny, immutable admin program with a "transfer every authority to a successor" decision; time lock on the Upgrade CPI (decision recorded with its slot, executed permissionlessly after N slots, cancellable by a later decision); buffer authority = admin PDA before the vote, ELF hash in the proposal; guardian flags on the admin program; ⟂ T5/U5 (one rule), U4 (the 2-of-4 veto; ⟂ X1: or one outsider's), F8 (the last settled set): §6.4; ⟂ V7: no age check — origin, sender and the sequence number only; "after N slots" is by the Clock since C29. §6.4, §6.5 — ⟂ AG12: the buffer is staged through the admin program once the decision is recorded. |
| G7 | medium-high | GovDAO has no voting period, time lock or expiry; a proposal passes the instant 66.66% of power votes yes and anyone executes it. "Unpause by GovDAO only" means a false-positive pause is days of halt; two lost T1 keys freeze the bridge in its last configuration forever. | The delay lives in our realms (G3); ⟂ T4: guardians never unpause another's flag (§3.3); a dead GovDAO leaves the bridge pausable in its last configuration (§1). §3, §5.3 |
| G8 | medium | Head-of-line blocking plus a 0.1 SOL ATA payer (as E1/E2). Signature endpoints are not stated to be public, so "anyone can relay" is not true as written. | As E1/E2; endpoints public (or an S3-style checkpoint store). §5.6 |
| G9 | medium | The guardian is not built; pause-only still freezes burned-and-waiting funds (a coercion lever) and lets a captured guardian re-pause after every unpause; guardian keys on the signer droplets fall with the signer keys. | Guardian keys separate from signer keys; GovDAO can remove or rotate a guardian; a burn that arrives during a pause is credited as a claim (E5) once the route is unpaused, so a pause delays, never strands; a captured key's re-pausing is bounded by its replacement (§3.3, §9) — ⟂ W2: on Solana that replacement is a 7-day decision the stolen key stretches to 28, so the freeze is continuous until then. §5.3 ⟂ v23 (see the preamble). |
| G10 | medium | The verifier slot: a realm may store and call another realm's object (GovDAO does); safe only if (i) objects, never bare function values, and only from an /r/ realm; (ii) `Verify` is non-crossing (no `cur`) and `done` is marked before it, or a verifier can re-enter `Process` and release twice; (iii) a panicking verifier halts its origin until GovDAO swaps it; (iv) verifier realms must not be `private`. | Exactly that design: `type Verifier interface { Verify(origin uint32, metadata, message []byte) error }`, `RegisterVerifier` accepted only from a realm, GovDAO activates by pkgpath per origin, `done.Set(id)` before `Verify` and an error is a panic, the active pkgpath rendered. §5.1 — ⟂ AF6: the interface also has `Satisfiable`. |
| G11 | medium | After the burn: an upgrade authority can also close a program for good (a brick power); burning upgrade authorities leaves the owner powers (ISM swap, router enrollment, fees) as the residual surface and escape hatch; gno's verifier slot stays GovDAO-settable so the lock can always be released under a new rule; the Solana side cannot recover from a dead light client. The plan's "funds can still be released by the proven path" is false when the bug is in the proven path. | Burn policy: burn only the admin program's upgrade key at bootstrap; keep the light client and the pausable ISM upgradable under GovDAO plus the week forever (⟂ F1/X1: 28 days once vetoed, void on two vetoes or one outsider's); never burn the token owners; no key may close a program after Phase 0. §6.5, §9 — ⟂ v16: the admin program's key is burned in the setup step before the vote, and no key may close a program after it (§5.8). |
| G12 | low-medium | Keys in plaintext files; the relayer needs a passwordless gnokey key (if it is the hotkey, a droplet compromise is admin before hand-over); keys must never enter the box-backup; HTTPS not in the code; §8 items 2 and 4 are "trust the audit"; README says cold key, plan says GovDAO; code cap 1,000,000 BUBBLE vs 10,000. | A dedicated gas-only gno key for the relayer; admin hand-over is a launch gate; keys of every kind excluded from every backup (§5.6); HTTPS on the endpoints; §8 rewritten around what a laptop can check (`hlsol audit`, byte-comparing our programs to Hyperlane's mainnet ids, the vote's head count); the README aligned, the code's cap to be replaced before launch (§5.9). |

Held: the mailbox fee max is immutable at init; no human can mint or freeze (mint authority = the mint PDA, no freeze authority); realms are immutable (no `private`); `IsCurrent` guards every entry; GovDAO executors run only from its proxy; the relayer verifies every signature; Squads v4 time locks exist for the interim holder.

### Round 1 — Alpenglow light client on gno.land

This review changes Phase 2 materially.

| # | Sev | Finding | Answer |
|---|---|---|---|
| A1 | critical | §7.1's last link does not exist: Solana's bank hash is now `sha256(sha256(parent ‖ sig_count ‖ last_blockhash) ‖ lattice_hash[2048 B])` — the merkle accounts-delta hash was removed (SIMD-0223, activated) and SIMD-0215 states the lattice hash supports no inclusion proofs. No proof can show the mailbox's burn record from a certified bank hash. The only trustless alternative is novel: a "lattice-delta" proof that reveals both bank-hash preimages and every account the block modified, pre and post, and re-sums the lattice; sound under LtHash collision resistance, heavy (thousands of accounts, blake3 XOF per account), and a block with a 10 MB write makes that burn's proof expensive. Transaction-inclusion proofs are cheap but Solana includes failed transactions and receipts (SIMD-0064) are stagnant, so minting on inclusion is exploitable. | Phase 2's proof is the lattice-delta proof inside the ZK program (it also proves the burn succeeded, since it yields pre and post state). Cost budgeted at tens of minutes on one GPU or a prover network; proofs batched per block. The prover captures pre- and post-images of every account written per slot from its own node. §7.1, §10 |
| A2 | critical | The epoch stake table (BLS keys, stakes, ranks) is built in memory from vote accounts and committed nowhere; §9's "proves it from the previous epoch's certified state" is wrong. SIMD-0511 (open PR, no date) would write it as a per-epoch account, after which the chain closes (table(E−1) → certificate → bank hash → lattice-delta proof of the boundary block). With ≥20% stake Alpenglow's safety fails; ≥60% forges any certificate; certificates never expire, so old keys of a past epoch can forge past-epoch certificates (long range). | Until SIMD-0511: "trustless per block, trusted per epoch rotation" — Proposal 2 says so. ⟂ U6: the roster is not a GovDAO-settable input (a vote every two days is impossible) but is submitted per epoch by the signers with a comparator (§7.3). Phase 2 is gated on SIMD-0511 for the fully trustless form. §7.3, §8 |
| A3 | high | Inclusion ≠ success: a burn transaction that fails is still in the block. | Only the lattice-delta proof (post-state) is accepted; never inclusion. §7.1 |
| A4 | high | 20% vs 1/3: with ≥20% Byzantine stake and a partition, two conflicting blocks can both certify; the bridge would mint against a burn in the abandoned fork. | The realm tracks one chain by the parent-link leaf in the double merkle; two certified block ids at one slot is proof of a safety failure ⇒ auto-pause; caps and a delay stay after Phase 2. §7.1, §9 — ⟂ round-12 (d): the verifier realm refuses every proof past that slot, the watch keys set `credits` and `payouts`, and GovDAO's unpause names the chain it follows. |
| A5 | medium | "A finalization certificate over a block id" is only the fast path (80%). The slow path is `Notarize(block)` at 60% plus `Finalize(slot)` at 60%: two aggregate signatures, two bitmaps, both checked. | §7.1 |
| A6 | medium | Timeline: testnet activation the week of 2026-09-22; Sept 28 and Nov 9 are generic feature-activation resumptions, not Alpenglow dates; migration is X+5000 slots after activation. Earliest live ≈ late Nov 2026; realistic Dec 2026–Q1 2027. With a month incident-free plus a shadow month, Proposal 2's earliest vote ≈ Feb–Mar 2027, and only if A2 is solved. | §2, §10 corrected; the GovDAO explainer video (the narrated film that accompanies this plan) says "no date yet"; since v5 Proposal 2 follows Proposal 1 (Q2 at the earliest, Q3 2027 realistically). |
| A7 | medium | Lazy execution (SIMD-0301, closed stale but on the roadmap) would shift the footer's bank hash by one slot; "replay" is the wrong fix (replay needs pre-states, which only the lattice-delta provides). | Anchor block N's state with the certificate of N+1. §7.1 |
| A8 | low | The native BLS12-381 path covers only the certificate: the state proof needs blake3 over megabytes per block (no gno native), so the ZK prover is needed regardless; native BLS is a chain upgrade plus gas recalibration. | Native path dropped; ZK path only. §7.2 |
| A9 | low | ZK cost: certificate ≈ 100–200M SP1 cycles (1–3 min on one 4090, ~$0.1–0.2 on a prover network); the lattice-delta part 10–30× that. | §7.2 (the figures), §10 (the per-block cost). |
| A10 | low | Implementer details omitted: `shred_version` inside the signed vote payload; base-3 bitmaps for mixed certs; rank ordering and duplicate-key exclusion; the 2,000-validator cap; the Genesis certificate at migration; hard-fork mixing into the bank hash; Rotor may change the block-id definition. | Carried into §13. |

Held: the footer carries the block's own post-execution bank hash under the block id; BLS12-381 min-pk over SHA256, 60/80% thresholds; votes are not transactions; gno's bn254 natives suffice for Groth16 (cometblszk is a working pattern); the double merkle's parent leaf lets one certificate anchor every ancestor block id.

### Round 1 — the Phase 0 realms, adversarial

No critical in the code as written: nothing releases BUBBLE or returns a message without threshold signatures over the exact bytes.

| # | Sev | Finding | Answer |
|---|---|---|---|
| R1 | high | A Solana→gno transfer larger than `dayCap` can never be delivered (confirmed by test); the burn is already done; a whale delivering first exhausts the day for everyone at no cost. | Claims (E5): `Process` always marks delivered and credits a claim of any size; `Claim` pays in turns under the bridge's meter and the recipient's (no per-message bound on claims; the per-message maximum is on gno sends). |
| R2 | high (pre-emptive, GNOT route) | The send envelope is ambient: a route reading `unsafe.OriginSend()` without checking the caller is a direct user call dispatches token messages for coins it never received (a wrapper realm keeps the coins and calls `TransferRemote` N times under one envelope; MsgRun's send is a no-op yet reported). | hlgnot: `runtime.AssertOriginCall()` plus `cur.Previous().IsUserCall()`, the envelope consumed exactly once per message, exactly `{ugnot}` accepted, `BankerTypeOriginSend` for refunds, never a stored realm banker. (The alternative, wugnot as a GRC20 like BUBBLE, was not taken; §5.2 locks native ugnot.) §5.2 |
| R3 | medium | UTC-day cap releases 2× the cap in 20 seconds across midnight (confirmed by test). | Token bucket (E5). |
| R4 | medium | The cap and signer set are admin-mutable instantly, with no delay and no lower-only rule; the cap bounds signer compromise only if the admin key lives nowhere near the signers. | Delays on raises and set changes (lower/remove instant); admin ≠ signer hosts; GovDAO from day one (G3). — ⟂ AE1, AF6: a signer removal that leaves the rule unsatisfiable waits 7 days (§5.3). |
| R5 | medium | "Admin = GovDAO" cannot drive the current API (as G3). | As G3. |
| R6 | low | Token messages to this realm, the bubble realm, the mailbox or any code-less realm sink the amount; a self-recipient consumes the cap and breaks `Locked == sent − received`. | Refuse `to == Address()` and the known realm addresses at send; credit such burns to the sweep queue GovDAO directs (§5.2, §5.4); ⟂ E9/v4: the monitor checks the counters' identity of §5.9, the sweep queue counted as owed. |
| R7 | low | The merkle hook in the signed digest comes from relayer metadata, so a checkpoint signed for any hook verifies when the metadata names it (Hyperlane's own shape). | Each signer signs exactly one Solana mailbox per origin domain; harmless while the enrolled-router check holds. |
| R8 | low | Open `Dispatch` lets anyone store junk forever and makes the signers checkpoint it. | Relayer filters by sender (E1); the mailbox stays open. |
| R9 | low | `Bytes32ToAddress` ignores the top 12 bytes; counters wrap only after ~10^4 full-supply round trips; re-enrolling a router strands in-flight messages from the old one; `SetAdmin` accepts an uppercase address; Go's ed25519 accepts non-canonical encodings (key hygiene). | The top 12 bytes must be zero and the client refuses raw pubkeys (§13, §5.7); a 7-day grace on router changes and refunds by id after a migrate (§5.3); strict ed25519 (§13); the rest noted. |

Held (confirmed by tests): caller spoofing (MsgRun, sub-realms, entering without `cross`); message binding in `Process` (version, domain, strict recipient, `done` before signatures); signature rules (strict signer order, index bounds, only `threshold` read, the digest binds everything, canonical S); route binding before the mark; amount bounds and cap arithmetic; `TransferRemote` always spends the caller's own allowance with no reentrancy; admin two-step and `SetValidators` validation; no exported mutable state, bounded Render, no `private`.

### Round 1 — gno light client on Solana

| # | Sev | Finding | Answer |
|---|---|---|---|
| L1 | critical | gno's object graph is not soundly merkleized: a parent saved before its child in one transaction stores a zero hash for the child reference and is never re-saved (realm.go:2057, realm.go:931); confirmed live on gnoland-1 and in gno's fixture map31b.gno. A verifier following such a link either cannot verify or accepts anything. §2's "an object's hash covers its children's" was false as stated. | The bridge proves events through `LastResultsHash`, never realm state (§6.2). ⟂ round-3 fact check: the gap is not "reported" by the plan; filing it is Jae's (§11.8), with the reproduction and patch in notes/. |
| L2 | critical (implementer) | The proof system named was wrong: the `main` store is a B+32 tree with ICS23 proofs, path `.store/main/key`; the rootmulti "multistore" op never appears; iavl/store.go is dead code for gnoland-1. | §2 corrected; moot for the events route except the AppHash map proof, which the results route does not need. |
| L3 | high | 20-byte object hashes: ~80-bit collision resistance against attacker-chosen bytes; a birthday search is within reach of today's hashrate. | Events route: 32-byte sha256 end to end. |
| L4 | high | 1/3 overlap for skipping lets 2 trusted keys plus 4 attacker keys forge a set change; gno.land has no bonding or slashing, so Tendermint's economic argument does not apply. | ⟂ T6: power-weighted +2/3 (§6.1); sequential set changes. §6.1 |
| L5 | high | No unbonding window exists on gno.land: ex-validator keys stay forgery-capable forever against a client whose trusted header predates a rotation. | Continuous updates, strictly increasing height, ⟂ T6/U1: power-weighted +2/3 of the current set; ⟂ F5: the "short trusting period" and "proof headers within it" are gone — a time limit buys nothing without unbonding; only the current set counts for updates and proofs and anyone updates; ⟂ V2: until the client has followed a rotation the old set is the current set and a leak of it is a permanent capture, not a window of minutes (§1, §6.1, §6.2, §9). |
| L6 | high | A halt longer than the trusting period bricks Solana, and "re-bootstrap by GovDAO decision" is circular once the client expired; gnoland-1 runs at the liveness edge (4 of 5 precommits seen at the tip). | §6.4's non-circular recovery, signed by the validators rather than proven through the client, drilled on devnet before Proposal 1. |
| L7 | high | Proof data availability: object bytes are in the unprovable `base` store, `vm/qobject_binary` does not hash to the stored hash, default pruning is ~28 days, object paths are kilobytes. | Events route: `/block_results` is served and never pruned. |
| L8 | high | Packaging: a 5-signature update exceeds one Solana transaction; object proofs would need 6+ staging transactions. | Staged signature PDA or curve25519 syscalls; results proofs are a few hundred bytes. §6.3 |
| L9 | medium | Commit rules that decide forge/reject: count by slot index, `len(precommits) == len(valset)`, sign bytes from each precommit's own BlockID tallied only when equal to the commit's, nil votes are real signatures, `Type == 0x02`, `3·tallied > 2·total`. | §6.1 |
| L10 | medium | Leaf encodings that reject genuine headers if wrong (zigzag varints, empty leaves, bech32 addresses, bare Timestamp, Any-wrapped pubkeys, validators sorted by sha256(pk)[:20]). | Carried into §13 with the verified bytes. |
| L11 | medium | Rollback/replay; §6.4's "proposal result as realm state" inherits L1; hard forks with the same chain id unreachable through NextValidatorsHash. | Height strictly increasing, proofs bound by header hash; decisions as events/messages; §6.4. ⟂ T2, then ⟂ F5: not "latest header only" — any header of the current set, with the 256 ring as a shortcut (§6.2). |
| L12 | low | Solana clock vs BFT time; precompile introspection; non-canonical tip commits; a future non-ed25519 validator; strict vs RFC 8032 ed25519. | Since v5 header time is not used at all; every Solana delay, the quarantine and the recovery trigger included, is measured by the Clock sysvar's unix time (§6.1, §6.4); precompile offsets (§6.3); a non-ed25519 validator, strict ed25519 and over-long commits are refusals (§13). |

Held: sign bytes, header hash, validator hash and commit verification all reproduced against height 701,862 with the chain's own code; AppHash map structure; events carry a VM-stamped pkg_path; structural amino parsing would have defeated the smuggled-hash attack (the real hole was L1).

### Round 2 — fact check and consistency

Found: the log held five reviews, not six (the agents review was answered but never logged; now logged); the delay,
the unpause rule, the recovery rule, cap parameters and the guardian set had no values or two versions; "code
aligned" was false; the README still described the v1 trust; §1 said "no person holds a key" while Phase 0's
interim multisig is one person; the vote timeline conflated earliest and realistic; "validators" named both the chain
validators and the signer machines. Answers: v3 §1 (the trust in people), §3.3/§5.3 (one unpause rule, 7-day delay,
72-hour inbox expiry — ⟂ F4: the expiry and cooldown are gone since v5), §5.4 (bucket, per-recipient bucket,
per-message maximum, floor, void), §5.8 (five programs, counts), §6.4 (one recovery rule), §10 (numbers derived),
§12's v1→v2 pointer note, the README rewritten, the words "signers" and "validators" separated.

### Round 2 — red team on v2

| # | Sev | Finding | Answer |
|---|---|---|---|
| T1 | high | The light client could never be fixed once it refused valid headers: its upgrade authority was the admin program, which acts only on decisions proven by the client; re-pointing does not change code. | §6.4: the last settled set's +2/3 may carry any decision, an upgrade included, after the veto and the delay; the pausable ISM paused meanwhile — ⟂ round-12 (b)8: starved of new headers, older headers of the current set still deliverable, nothing paused. |
| T2 | high | "Stores only the latest header" let any updater evict the header a proof needs — permanent cheap censorship of every proof and decision. | §6.2: proofs carry their own header and commit, or one of the last 256 accepted header hashes. ⟂ U1/F5: not "any header in the set history" — the current set's headers only; older messages are re-emitted. |
| T3 | high | Claims made a forged message a durable liability the pause did not stop: paid at the bucket's rate for days, blocking honest claims, with no void. | §5.3/§5.4: `NewVoidClaimsProposalRequest` executes at once; while paused nothing is credited; owed claims are in the invariant. — ⟂ AE1, AF7, AG1, AH1: at once only for the ids a guardian's `payouts` flag names, 7 days otherwise (§5.3). |
| T4 | high | A guardian pause of the admin inbox locked governance out; guardian unpause let compromised signer hosts (which held guardian keys) unpause themselves. | §3.3: no guardian unpauses another's flag; guardian keys only on non-signer machines. ⟂ U4, then ⟂ F4: the 72-hour expiry and the 7-day cooldown are gone; one flag is lifted by a decision 24 hours after delivery, two guardians' flags hold; ⟂ V1: every decision is recorded through any flag, and flags defer execution only (§6.5). — ⟂ AB1, AC2: one outsider's flag kept by a standing veto also holds. ⟂ v23 (see the preamble). |
| T5 | high | The recovery rule gave the old keys the re-point power behind a veto only, "last trusted set" could be a rotated-out set, 4 of 5 might not exist, and three recovery rules coexisted. | One rule (§6.4), its actors in §1, power-weighted, the pausable ISM paused through a lapse-opened recovery (no header arrives; a hold-opened one pauses the admin program's decisions only), the updater lag alarmed at one hour — ⟂ round-12 (b)8: a lapse starves gno → Solana of new headers, it pauses nothing. |
| T6 | medium-high | "4 of 5" is wrong on the live chain: Prop #10 would set one validator's power to 1; the chain commits with three big validators; a signer-counting client stalls. | Power-weighted +2/3 everywhere; "4 of 5" removed. |
| T7 | medium-high | The GovDAO gate: any realm implements Executor and the "created in" line is forgeable; an exported gated mutator could be reached by a custom executor under a misleading proposal text. | §5.3: no exported mutators; builders mint decision ids and write descriptions; the realm renders decisions; §8 checks the render. |
| T8 | medium | Decisions had no expiry at any stage (accepted proposals never expire; messages had no deadline; the delay started at delivery); the buffer hash was trusted to a freeze. | §5.3/§6.5: deadlines in the closures, sequence numbers, the buffer hashed at execution. ⟂ V7: the admin program's age bound was vacuous and is gone; on Solana the sequence, the clock and `cancel` govern, the deadline is the proposal's. |
| T9 | medium | Claim semantics stalled or stranded: shared outbox nonces, claims above the burst unpayable, a daily per-recipient cap reintroduced the midnight double, no caller, scaling by balance. | §5.4: credit order, installments, per-recipient bucket, permissionless Claim, the head-of-line delay stated. ⟂ F10: the cap scales with the lock again (10% of what is locked, never below the floor) — scaling by what is outstanding collapsed it in an exit wave. |
| T10 | medium | §1 understated Phase 0: one person holds every Solana authority through his own 2-of-2 with a one-day lock; guardians, recovery actors and operators unlisted. | §1 rewritten; the monitor watches the multisig queue. |
| T11 | medium | Pausable ISM: a paused verify is a deterministic failure that parks messages for good; the wrapper must forward Type and VerifyAccountMetas; one guardian is a global halt lever. | §5.7 retries parked messages — ⟂ U7: hourly, not only after an unpause; §5.8 forwards; §1 names the lever. |
| T12 | medium | "A user resubmits in a cheaper block" is impossible (a burn is final in its block); a block producer can inflate the lattice-delta proof by touching large accounts. | §7.2: priced into the fee, a per-block budget and queue, the blake3 precompile question checked; the sentence removed. |
| T13 | low-medium | Lattice-delta soundness details: owner and sender bindings, pre = identity for creations, one entry per pubkey, order independence, the LtHash assumption. | §7.1. |
| T14 | low | A dead GovDAO can never rotate a compromised signer set. | §1: the bridge ends paused with the lock intact — ⟂ B6: paused for as long as the flagging guardian keeps its flag. |
| T15 | low | Stale facts: 5 validators only since 487,883; equal power; L6's "liveness edge" not reproduced (every sampled commit carried every precommit). | §2 corrected. |

Held: event pkg_path is VM-set and cannot be forged by a callee or a closure; failed transactions carry Error; the mailbox verifies before creating the processed-message record; per-ISM process-authority seeds stop a wrapper from forwarding a signer; ugnot is unrestricted; `done.Set` before Verify; sender-bound Dispatch; the commit rules of L9.

### Round 2 — independent reproduction of the gno object-hash gap

Correct, and broader than claimed: the hash is zero because `MarkDirty` zeroes it and `toRefValue` writes it
unconditionally; three orderings trigger it, including modifying any two package-level objects in one call, which
leaves the escaped package block with a zero link. hlmailbox's own `Dispatch` triggers it twice (`Tree.Insert`, then
`sent.Set`). Minimal filetest, keeper-level harness, a live walk of r/gnoland/blog (4 zero links of 476), and a fix
(`saveDirtyChildrenFirst`) that takes the suite from 32 debug-assert panics to 1 (a transient cross-realm case). The
bridge's design is unaffected (events, not state); the write-up is notes/gno-object-hash-gap.md.

### Round 3 — red team on v3

| # | Sev | Finding | Answer |
|---|---|---|---|
| U1 | critical | §6.2's "any header in the set history" reopened L5: a rotated-out set keeps +2/3 of its era's power forever and could forge a header with a fake `LastResultsHash`, hence a fake Dispatch or decision. | v4: proof headers ≤ the trusted height and within 14 days of the trusted header's time. ⟂ F5/C9: that compared a time the forger writes; v5 drops the window for the rule that only the current set's headers prove anything, with `Reemit` (§5.1) for older messages (§6.2). ⟂ V2: the current set itself, leaked, is a capture (§1). |
| U2 | critical | The 72-hour inbox pause outlasts the 20,000-block decision age bound (≈19 h), and nothing re-dispatched, so one pause bricked Solana governance; the operator's 19-hour absence did the same. | §5.3/§6.5: anyone may re-dispatch an executed decision under the same number; the admin program ignores numbers ≤ last. ⟂ V7: the age bound is gone. |
| U3 | critical (trust) | Two GovDAO keys replace the validator set in one block (r/sys/validators applies at execution), so the light client and every 7-day delay reduce to those two people plus the guardians. | §1 says it first; §6.1 quarantines any set change for 7 days with gno → Solana paused; since v5 two guardians hold and veto (§3.3); the structural fix is gno.land's (§11.1). — ⟂ AC2: two guardians, or one outsider (§3.3). ⟂ v23 (see the preamble). |
| U4 | high | Guardians could chain four 72-hour pauses into a permanent freeze; a 1-of-4 unbounded veto blocked every recovery; three guardian hosts sat on one DigitalOcean account. | v4: one inbox pause per 7 days in total, a 2-of-4 veto, rotate-guardians bypassing the inbox pause, guardians on distinct providers (⟂ C10/C13: guardians are people with their own keys since v5; the providers rule survives for the watch keys). ⟂ F4/C4: the arithmetic was one pause per 10 days, and the expiry model is gone — v5's one-flag/two-flag rule (§3.3, §6.5) bounds a lone key by the 24-hour unpause and makes two guardians a deliberate brake; the veto itself ⟂ X1, then Y1/AA4: two guardians or either outsider alone; an outsider seat's replacement by the other outsider, or by itself unless the other affirms. |
| U5 | high | §6.4 bypassed GovDAO without saying so; "last trusted set" undefined after a partial rotation; updater outages and a griefable staging PDA omitted; recovery sign bytes undefined; the veto was one person. | §6.4 rewritten: the set of the last accepted header (since v5: the last settled set); domain-separated sign bytes (since v5 with a counter and the program id, F3); the staging PDA keyed by (submitter, header hash); a devnet drill; the trust named in §1. |
| U6 | high | The roster as "a GovDAO-set input" could not work (epochs are two days; a vote every two days is impossible), so a delegated 1-of-1 oracle would have set Solana's validator table; Phase 2 did not retire the signers before SIMD-0511. | §7.3 rewritten: the signers submit the roster per epoch (2 of 3) with a comparator monitor; Proposal 2 says they vouch per epoch; Proposal 3 removes them after SIMD-0511. |
| U7 | high (liveness) | A 0.4 SOL relayer float lasted hours at the stress volume; monthly hand conversion could not sustain it; parked messages were retried only after an unpause; the fee realm's withdrawal was unspecified. | §5.5: 1 SOL float, runway alarms at 7 days, hourly retries, r/hlfees with a GovDAO-set treasurer; §11.4 asks for more SOL — ⟂ C66: 4 SOL, not 3, with the split in §5.8. |
| U8 | medium | Void by range took honest claims with no restore; dust burns to a capped recipient made every `Claim` call pay for nothing. | §5.4: void and restore by message id; per-recipient queues; `Claim(n)` in turns. |
| U9 | medium | `hlsol audit` did not print ISM pointers, routers, guardians, flags, the default ISM, the client's set hash, the admin's sequence number or its burned authority; no recovery drill; the per-message maximum "raised" had no number. | §5.8, §6.3, §6.7 (2,500 → 25,000). |
| U10 | medium | Contradictions: unpause delay (§1 vs §5.3); Phase 0 Solana unpause is the multisig's; guardians "nothing else" vs the veto; "nothing is unfixable" vs immutable realms with no migrate; no set-router builder. | §1, §3.3, §5.3 (migrate, set router with grace, cancel) rewritten; §1 ¶1 finished in v5 (C1/C2). |
| U11 | medium | The mailbox passes `metadata` by value, so header + commit + proof (kilobytes) need a staged proof account; headers above the trusted height were undefined for proofs. | §6.2/§6.3: staged proof accounts; proof headers ≤ trusted height. |
| U12 | low | hlmailbox verifies against the current signer set only (a rotated-in signer must backfill); the multisig-queue monitor self-triggers; builders spammable. | §5.6 backfill; §1 the operator's announced changes excluded; §5.3: a builder call records nothing, decisions exist only when a proposal passes, so no deposit is needed (v4's deposit is gone). |

Held: the GovDAO executor cannot be re-wrapped (no getter), so the builders' gate holds; the mailbox's verify → record → handle order and per-ISM process authority; splitting across recipients is bounded by the global bucket; the 256 ring has no safety role; Squads v4 is immutable; §2's gno facts match the live chain.

### Round 3 — fact check and consistency of v3

Found: twenty answers in §12 with stale pointers, superseded text or partial coverage (eight marked ⟂ in v4; the
rest in v5 after the round-4 fact check); §1 said unpausing waits 7 days (it is immediate on gno.land) and that
GovDAO governs Solana from launch (the interim multisig does); the guardian lockout arithmetic defeated §3.3; the
any-height proof path (U1); the launch SOL margin was 0.11 SOL against the README's rougher rents; the relayer float
lasted hours; the 500/day case contradicted the cap; `received` had to mean paid; the "reported to gno" claim was
false (it is filed by Jae); the review-round counts and the hand-over word were ambiguous; "exact bytes in §12, L"
pointed at nothing. Answers: v4 §1 (the operator named as one party, the multisig's close power, the device-loss
risk), §5.4 (paidTotal, owed), §5.8 (rents labelled from program sizes, the 7.35 arithmetic), §10 (base and stress
cases, break-even), §11 (more SOL — 3 then, 4 since C66, 6 since round-13 (c) — a third multisig key, token metadata), §13 (the bytes),
the ⟂ marks, and the README brought to the plan.

### Round 4 — fact check and consistency of v4

Verdict given: "one editing pass away"; four things a member would catch (§1 ¶1 vs §6.5 on Solana and unpausing; the
program rents; the guardians all the operator's; the proof window on a field the forger writes), then the cooldown
arithmetic, the per-delivery rent, the Proposal 1 month, the present-tense tools, the sweep queue pointing at
nothing, and nine unmarked overturns. All folded into v5; one claim was wrong and is answered with a measurement:

| # | Found | v5 |
|---|---|---|
| C1, C2 | §1 ¶1 gave GovDAO Solana from launch and promised instant unpause while §6.5 delayed every decision 7 days; a `cancel` that waits 7 days cancels nothing. | §1 ¶1 rewritten; §6.5 has three timing groups: cancel at once, unpause after 24 h, the rest after 7 days. |
| C3 | "7 days after the vote" vs after execution (up to 21 days). | §1: after execution, with the 14-day deadline named. |
| C4 | 72 h + 7 days = one pause per 10 days, not 7. | The expiry and cooldown are gone (F4). |
| C5 | §9 said the recovery "needs" the guardians; a veto is not an approval. | §9 reworded. |
| C6, C7 | Proposal 1 "three months in" vs April 2027; Proposal 2 earliest before Proposal 1. | About four months; Proposal 2 follows Proposal 1: earliest Q2, realistically Q3 2027 (§1, §7.4, §10). — ⟂ v16: launch about February 2027 (§10). |
| C8 | Three rounds vs four. | Four at v5 (§5.9, §11.2). |
| C9 | §6.2's 14-day window compared the forged header's own `Time`. | Gone: only the current set's headers prove anything (F5, §6.2). |
| C10 | The four guardian keys were all the operator's, so the veto was one party's. | Four people, two outside the operator and GovDAO (§1, §11.9); watch keys separate. |
| C11 | "Unpaid by the bridge" vs the treasurer's withdrawals. | The operator is paid from the fees (§1, §5.5, §10, §11.12). |
| C12 | Four pause objects with different clearing rules, never enumerated. | One model for all three objects (§3.3, §5.3, §6.5). |
| C13 | A monitor that pauses holds a guardian key — which, where? | Watch keys on the operator's two monitor machines (§1, §5.6). |
| C14 | `hlsol audit`/`transfer` in the present tense; `cmd/hlsol` has setup/send/balance. | Future tense, "to be built" (§5.8, G1, S4). |
| C15 | The sweep queue pointed at nothing. | Defined in §5.4 with a builder in §5.3. |
| C16 | Per-message maximum 2,500 → 25,000 with no builder and no BUBBLE figure. | Builder in §5.3; 25,000 GNOT and 25,000 BUBBLE (§6.7). |
| C17 | "Rotate signers" said nothing about which set. | hlmailbox's (§5.3). |
| C18 | Updater lag listed as a Phase 0 monitor. | Moved to §6.1. |
| C19 | Proposal 3 absent from §1, §8, §10. | Added (§1, §7.5, §8, §10). |
| C20 | "Launch under the caps" vs unbounded Solana minting. | §9 and §11.11: the Solana bound is pause latency × pool depth. |
| C21 | Metadata final before the transfer, undecided in §11.5. | §11.5 has the deadline. |
| C22 | "Its first byte is an amino length" is not the separation. | Length and the domain prefix (§6.4). |

Numbers: the rents were disputed — the reviewer computed 6,960 lamports/byte from the documentation and 7.9 SOL for
the deploy, which would have made the 7.36 SOL short. Measured instead: mainnet and devnet answer 5,080 per byte
(`getMinimumBalanceForRentExemption`, 0 B → 650,240 lamports; 211,069 B → 1.07288 SOL), so §2/§5.8/§10's 1.07 / 0.84 /
1.82 stand; 6,960 is what a fresh test validator's genesis uses, which is why the local e2e's accounts (2,074,080
lamports for an ATA) cost more than mainnet's. The per-delivery rent is now stated by composition (0.00095 record +
0.0015 first token account, §5.5) with the monthly ranges that follow (§10); the recovery's total (v5 said ≥28 days;
v6 said ≥22, C23; 21 since C64), the floats' runways (v5's "8 days / a day" charged the ATAs to the relayer; v6 separates the
three floats, §5.5), the conversions and the rounding are in §5.5, §6.4, §13. Parameters without values (the
floor, the per-message maximum, the recipient bucket's burst, the quarantine's clock, the sweep queue, "Pending",
"the box's store", key types, updater cadence, who runs the validators, the jargon) all have one now. The skeptic's
list is answered in §1 (what Phase 1 buys; reputation), §5.9 (results published), §6.4 (validators' assent, §11.10),
§7.2 (unprovable blocks), §9, §10 (what GovDAO gets), §11 (the review, the pool). The nine unmarked overturns (T2,
T4, A2, L1, L11, E3, E5, G5, T11) and the dangling "T-float/F3" are marked; E2/E5's editing debris is gone.

### Round 4 — red team on v4

| # | Sev | Finding | Answer |
|---|---|---|---|
| F1 | high | The decision path escaped the quarantine and the guardians' pause: the admin program sat under the raw light-client ISM, "set ISM pointer" was a non-vetoable 7-day decision, so two GovDAO keys could rotate the set, sign a fake decision on day 0 and on day 7 remove the pausable ISM from the token's path with no lever left. The quarantine and the hold added nothing beyond the delay the attacker already faced. | §3.3/§6.5: every decision in the 7-day group — ISM pointers, the pausable ISM's target, guardians, routers, fees, upgrades, the successor — is vetoable by two of four guardians, their own replacement included (⟂ X1, then Y1/AA4: or by either outsider alone; an outsider seat's replacement by the other outsider, or by itself unless the other affirms); two of the four are outside the operator and GovDAO (§11.9). The guardians are a second house with a brake and no accelerator; §1 says so. ⟂ v23 (see the preamble). |
| F2 | high (build) | Decision validity at delivery was unspecified: a decision the admin program rejects in `handle` reverts its record (Hyperlane), so one bad decision at seq k stuck the sequence forever, and `cancel(k)` would itself be seq k+1. | §6.5 lifecycle: a decision that parses at last+1 is recorded whatever its parameters; parameters are validated at execution and a failed execution is spent; ≤ last is ignored, > last+1 refused; r/hlgov's builders validate up front. |
| F3 | high (build) | Recovery decisions had no nonce, no binding to the admin program and no stated relation to r/hlgov's sequence: last year's recovery bytes replayed, or a devnet drill's on mainnet; a recovery consuming last+1 would silently void r/hlgov's next decision. | §6.4: the digest binds the admin program id and a recovery counter; recoveries keep their own counter; a `set sequence` kind exists for a chain reset. ⟂ v25 (see the preamble). |
| F4 | medium-high | After the bootstrap "unpausing at once" was false (every decision 7 days), a direction pause had no cooldown and a guardian removal was a 7-day swap: a stolen key froze a direction for ≥14 days on Solana. | §6.5: `unpause` executes 24 hours after delivery and is void only against two guardians' flags; §1 states the durations. The expiry/cooldown model is gone (U4 ⟂). ⟂ V1: every decision is recorded through any flag (not only three kinds); ⟂ V5/W1/W2: a stolen guardian key is a continuous freeze of up to 28 days, not "a day per cycle until its 7-day replacement". ⟂ v23 (see the preamble). |
| F5 | medium | A rotated-out set kept forging power for 14 days (the proof window) while the quarantine covered 7; the client stores sets, not every header, so a never-existed header below the rotation height signed by the old set verified. | §6.2: proof headers must be signed by the current set; `Reemit` (anyone, cheap, automatic in the relayer) delivers pre-rotation messages from a fresh header; the 256 ring is cleared at a set change; no trusting period at all (§6.1). |
| F6 | medium | The global bucket was allocated by `Claim` call order: a party with four recipients and a bot took every refill, and honest recipients found a trickle for as long as it ran (≈20 GNOT of fees a cycle). | §5.4: `Claim` pays in turns across recipients (oldest first within each); the Solana → gno fee (§5.5) is paid once per burn and a burn holds its turn for amount ÷ (cap/4) days; the bound is stated — k addresses take at most k/(k+1) (⟂ round-9 (c): min(k/4, k/(k+1)), the quarter-meters binding below three) — and §9 names GovDAO's answers (the fee, the floor). |
| F7 | medium (trust) | §1 omitted: the Solana RPC providers as the Solana → gno root of trust in Phases 0–1; `migrate` as GovDAO's power to move the whole lock; `void` as a freeze of one person's claim; monitors as pause levers; the quarantine existing only from Phase 1; BUBBLE's own minter key; Hyperlane's and Squads' code as trusted. | All in §1; the signers' provider pairs are distinct (§5.6); the signers quarantine in software from launch (§5.3). |
| F8 | medium-low | After a legitimate rotation and a stopped updater, the recovery set ("the last accepted header's set") was the old set, and the veto was four keys of one party. | §6.4: the recovery set is the last *settled* set, the announced set once settled; members' tools submit updates and receive the lag alarm (§6.1, §8). |
| F9 | low-medium | Hyperlane keeps one router per domain, so an "enroll router" decision stranded every old-route message in flight, whose GNOT `migrate` then moved. | §5.3: the gno set-router keeps the old router accepted 7 days; a successor realm refunds undelivered sends by id; the relayer drains the old route before GovDAO proposes the swap (§5.7). |
| F10 | low | The cap base (sent − paid − owed) fell as burns were credited: in an exit wave the cap collapsed to the floor exactly when the queue was longest; §3.4 claimed the opposite. | §5.4: the cap is 10% of what is locked, never below the floor; §3.4 reworded. |
| F11 | low | Any ValidatorsHash change, a power tweak like Prop #10 included, meant a week's gno → Solana outage; overlapping changes needed max(). | §6.1: so stated, extended to the latest on overlap, and the cost named: GovDAO times its validator votes. |
| F12 | low | §12 G3's "executors must return errors, not panic" was backwards for transient conditions: the proxy denies an accepted proposal for good on an error. | §5.3: error only past the deadline, panic on anything transient; G3 marked ⟂. |
| F13 | low | `Redispatch(seq)` spam made a naive relayer pay rent per copy. | §5.7: one copy per sequence number, only when it is the next expected. |
| F14 | low | The roster comparator was the operator's, so "caught within an epoch" held only if independent. | §7.3: a published tool a GovDAO member runs with a guardian key against their own provider — ⟂ round-9 (b)2: an outside guardian, since the only member with a guardian key is Jae. — ⟂ round-14 (b)10: the comparator alarms its holder; the watch keys flag (§7.3). |
| F15 | low | A zero fee reopened E2 (a 10-GNOT send cost the attacker 0.034 GNOT in gas and us $0.26 of rent). | §5.3: the builder refuses a fee under the fee floor; §5.5 gives GovDAO the break-even. ⟂ V8: the floor is settable, 2 GNOT / 40 BUBBLE at launch, not a compiled 1 / 20. |

Consistency items C1–C22 answered as in the fact-check table above (the two reviews overlapped on C1, C2, C4, C9,
C10, C14, C15, C16). §8 gaps: the admin program's own ISM, its accepted origin and sender, the recovery and
quarantine constants are in §8.3–8.4; §8.7 lists what no member can check. The build-readiness list (the decision
lifecycle, the recovery bytes, the quarantine's mechanics, the proof-path set rule, the mailbox's panic-on-error, the
Dispatch encoding and the leaf bound, the claim allocation and the monitor's identity, one guardian list per chain
with one decision updating both Solana copies, the closure error rule, the fee realm's gate and floor, one copy per
sequence, old-route draining, staged PDA ownership and closing, precompile offsets) is in §5.1, §5.3–5.7, §6.1–6.5
and §13.

Checked and held: events of a reverted sub-call cannot exist (revive is test-only; a panic across a realm boundary
aborts the transaction); event `pkg_path` is the emitting package's; the GovDAO gate holds under the proxy's
`Execute` whoever invokes it, a proposal cannot execute twice and only members create them; Hyperlane's verify →
record → handle order with the inbox as a reentrancy guard and per-ISM process authority; a recovery digest cannot
equal a `CanonicalVote`; a non-crossing `Verify` keeps state in its own realm and cannot usefully re-enter `Process`;
`MsgCall` rejects non-crossing functions, so `Reemit` and the builders take `cur`; `private` realms are
re-uploadable and the plan's rule is right; Phase 0 signers read hlmailbox state, not events; Dispatch's sender is
`cur.Previous().Address()` under `IsCurrent`; hlbubble spends only the caller's allowance; Hyperlane's token program
has no amount-0 refusal, so the relayer skip is the right place.

### Round 5 — fact check and consistency of v5

Verdict given: "one honest pass away". Confirmed by re-measurement: the mainnet rent rate (5,080 per byte, 128-byte
overhead; the ELF sizes equal the plan's minus the 45-byte header), that agave's loader drains the buffer into the
payer inside the deploy instruction, and that Squads v4 applies its time lock to every executed proposal. Found and
folded into v6:

| # | Found | v6 |
|---|---|---|
| C23 | Recovery timing: §6.4 said 7 days of veto plus 7 of delay (28 from the lapse), §6.5 one 7-day group; a recovery is submitted, not delivered. | One rule: a recovery follows its kind's timing in §6.5; ≥22 days from the lapse (§6.4) — ⟂ C64: 21. |
| C24 | Phase 0's owner "clears at once" while every Squads execution waits its day. | "After its one-day lock" (§5.3, §5.9). |
| C25 | "k fees per cycle" — the fee is per burn; "cycle" undefined. | One fee per burn; a burn holds its turn amount ÷ (cap/4) days (§5.4, F6). |
| C26 | §1's hold unqualified by chain. | "On Solana" (§1). |
| C27 | "The trust path" vs "the 7-day group" (fees included). | The 7-day group everywhere (§1, §3.3, §6.5; §9 and §11.9 brought to it in v9). |
| C28 | §8.3 had r/hlgov rendering a recovery counter it never sees. | Printed by `hlsol audit`; zero at Proposal 1 (§6.4, §8.3). |
| C29 | Slots vs clock. | Every Solana delay by the Clock sysvar's unix time; slot figures "≈" (§6.1, §13). |
| C30 | "7 days at most" for a stolen key is a floor. | "7 days after the replacement decision is delivered" (§1, §9) — ⟂ W2: a stolen guardian key is 28 days, continuous (v7). ⟂ v23 (see the preamble). |
| C31 | The Phase 0 owner could replace two holding guardians after a day and clear. | The pausable ISM refuses a replacement while two guardians' flags stand and takes at most two per call; the owner can still swap a token's ISM, as §1 says (§5.3, §5.8). ⟂ v23 (see the preamble). |
| C32 | Proposal 1 is three proposals on two clocks. | Said so (§6.7). |
| C33 | A full bucket pays a cap at once and another within the day: 2× in the worst 24 hours; "a tenth of the lock a day" was a fifth on day one. | Meters start empty at deploy and after an unpause; "at most two days' cap in any 24 hours"; the first-day bound stated (§5.4, §9) — ⟂ C51/W7: v6's "the floor, or a fifth" mixed one cap and two; v7 says one cap from an empty meter, two caps otherwise. — ⟂ AF1: only an unpause that clears a standing `payouts` flag resets a meter. |
| C34 | The immediate-effect lists differed between §1 and §5.3. | Aligned, `cancel` and guardian removal included (§1, §5.3). |
| C35 | "A set the client has settled on" vs "the last settled set"; "settled" defined in §6.1 not §6.4. | Reworded with the pointer (§1, §6.4). |
| C36 | Buffers: the loader drains the buffer inside the deploy, so the peak is one rent per program. | §2, §5.8 reworded. |
| (a) | F9's old-route drain was claimed but absent; "k fees per cycle" (F6); E3/G12 said the code's cap was aligned (it is 1,000,000 on the branch); the counters sentTotal/paidTotal were named in §12 only; G6's ⟂ named the wrong findings; L12, G9, R2, L6, A6 wording; the round headers carried clock times that put round 4 before round 3. | §5.7 has the drain; §5.9 defines the counters; the marks and words fixed; the headers carry order only. |
| (c) | The runway charged the ATAs to the relayer (1 SOL is ≈21 days at 50/day for records alone; the two ATA payers at 0.25 SOL trip the 7-day alarm on launch day); the deploy budget omitted ≈0.04 SOL of state accounts and five program accounts, so 7.36 is short by hundredths; the pausable ISM's 0.3 SOL is a guess; machines at $80/month were low ($100–170); income at 50/day (≈$1,035) was unstated; `Claim`'s gas is an estimate. | §5.5, §5.8, §10, §11.4 (the extra SOL is a precondition — 3 then, ⟂ C66: 4, with the guardian and watch keys budgeted; the cost range ⟂ round-10/11 (c): $450–765, two fifths to three quarters). |
| (d) | No value for: the veto mechanism, the admin program's initial guardian list, where its flags live and who sets them, whether a paused gno route refuses sends, how a 7-day gno change takes effect, the relayer's gas float, the ATA split, who pays flag transactions, the number of watch keys, BUBBLE's fee basis, the initial set's settled status, Phase 2's figures, the launch notice, the detection latency; "bucket", "route", "T1", "owed/voided/in flight" used before definition. | All given (status paragraph, §5.3–§5.6, §6.5, §7.2, §5.9). |
| (e) | The skeptic's list: the brake undecided and unnamed; no override for two guardians; Phase 1 does not bind the two members; the validators' assent as a gate; the Solana loss bound unnumbered; the fee floor below the record's cost; no operator exit; the drill never rehearsed a successor; guardian lists diverging by chain; "checkable with a laptop" is "when built". | §1 (no override, why; the operator's exit; what Phase 1 buys), §6.6 (assent a gate; the successor drill), §5.3 (fee floor 2 GNOT; the gno list follows Solana's), §3.7, §11.9. |

### Round 5 — red team on v5

| # | Sev | Finding | Answer |
|---|---|---|---|
| V1 | high | One flag plus one in-flight decision wedged Solana governance forever: a 7-day-group decision at k+1 was refused under a flag, so GovDAO's `unpause` at k+2 was "> last+1, refused" and nothing could ever be recorded — one rogue key, not two, ended the Solana side, and a monitor that paused and died did the same. | §6.5: every decision whose 9-byte prefix parses is recorded at last+1 whatever flags stand; flags gate execution only — a lone flag defers the 7-day group except `set guardians`, a hold defers the whole group, `unpause` and `cancel` are never deferred. §9 has the row. |
| V2 | high (trust) | A leak of the *current* set's keys was called a window of minutes; it is permanent capture: the forger runs the client ahead of the real chain before any rotation reaches it, no quarantine fires, honest headers are "not above trusted", and the recovery set is the leaked set. | Said plainly in §1, §3.6, §6.1, §6.4, §9: a leak ends the Solana side (hold, exits, a new deployment); the watch compares every accepted header with gno.land's and flags a mismatch within a minute (§5.9, §8.1); the accelerator (a 4-of-4 re-point after a hold) is put to Jae with a recommendation against (§11.9). |
| V3 | medium | Rolling `unpause`s shrank a lone honest guardian's 24 hours to minutes: nothing bound an unpause to the flags that existed at its delivery, and a matured one could be bundled with a forged mint. | §3.3, §5.8, §6.5: flags record their set slot; an unpause clears only flags set before its delivery; a later flag needs a later unpause. §5.9 drills it. |
| V4 | medium | The launch bound was understated (a full bucket paid min(lock, 10,000) within minutes), and the fee taken at credit moved lock → r/hlfees on every forged `Process`, outside the buckets and the void, breaking the identity by twice the fee per voided claim. | §5.4/§5.5: meters start empty at deploy and after every unpause; the fee is the first installment at payment, inside the meter; a voided claim pays no fee; the first-day bound is stated (⟂ C51/W7: one cap from an empty meter, two caps otherwise). — ⟂ AF1. |
| V5 | medium (trust) | The veto assumed both outsiders act within every 7-day window (with the insiders GovDAO's, `{O1→X}` then `{O2→Y}` passes against one veto each), had no eyes of their own, and §1 did not say the operator alone holds two keys. | §6.5: a vetoed replacement waits 28 days (⟂ W1: v6 stretched it only on the replaced guardian's own veto, which GovDAO could aim around; v7 on any one veto), so one outsider acting within a week, then four weeks for the other, suffices (⟂ X1: either outsider alone voids, so one awake outsider suffices — ⟂ Z1: not against its own seat's replacement; ⟂ AA4: true again since v12 under the affirmation rule, with a standing veto against floods); `hlwatch` is theirs and alarms them directly (§5.8); §1 says the operator's two keys can hold, and so end, the Solana side alone, block any 7-day decision, and never re-point. |
| V6 | medium-low (build) | (a) The pausable ISM's owner `SetGuardians` could replace two holders and clear; (b) `hlsol setup` initializes the mailbox with the raw multisig ISM as default, so an unset token ISM pointer bypassed the pausable ISM; (c) the pausable ISM's read of the client's state had to fail closed. | §5.8: no replacement while two guardians' flags stand, at most two per call, four fixed; the mailbox's default ISM is the pausable ISM; fail closed on an unknown layout. ⟂ v23 (see the preamble). |
| V7 | low-medium (build) | Credit-by-vote had to mark `done` and refuse done ids; zero credits must not enter a queue; the 20,000-block age bound was vacuous (the relayer proves against any current-set header) yet §8.4 sold it as a deadline; "parses" had to mean the prefix only. | §5.3 (done mark), §5.4 (never queued), §6.5 and §8.4 (no expiry on Solana; the sequence, the clock, `cancel`, and GovDAO's 14-day proposal deadline), §13 (the 9-byte prefix). |
| V8 | low | The 1 GNOT fee floor was compiled into an immutable realm; on gno a stolen guardian key flagged every route after each unpause for 7 days. | §5.3: a settable fee floor (2 GNOT, a guard against a mistaken zero); gno unpause may remove the key in the same proposal, at once (§1, §3.3, §9). |

Checked and held: Hyperlane's verify → record → `handle` order with the Inbox as the reentrancy guard; dispatch needs
the sender's or its dispatch authority's signature; the token's `handle` needs the process authority and the enrolled
router; one router per domain; the token ISM is owner-set; the multisig ISM has no pause. GovDAO: an error denies for
good and a panic reverts for a retry; only members create and anyone executes; `SimpleExecutor.Execute` is gated on
the proxy and the closure runs under its construction realm, so r/hlgov's closure dispatches as r/hlgov. Validator
timing: executed at A, announced at A+1, signing from A+2; the live set and counts match §2. `pkg_path` is the
emitting package's; `revive` is test-only; `AssertOriginCall`, `IsUserCall` and the `BankerTypeOriginSend` gate exist
as §5.2 uses them. The recovery digest cannot be a vote; the counter and program id bind it; a hold leaves the exits
open under a live GovDAO. Provider pairs: three providers to fool two signers, one bad provider halts two (liveness).
`Reemit` keeps the id; the k/(k+1) bound; forged `cancel`s stay off recovery decisions; the pre-plan `done.Set` after
verify in `mailbox.gno` is consistent with §5.1 once the verifier slot lands.

### Round 6 — fact check and consistency of v6

Verdict given: the architecture is consistent and every re-measured number stands (rents, ELF sizes, the deployer's
balance 7.357 SOL, the runways, the monthly costs, the conversions); the trust statement was not ready because §1 and
§9 contradicted the body in four places. Folded into v7:

| # | Found | v7 |
|---|---|---|
| C37–C44 | Unmarked overturns: T8 and U2 (the age bound, V7); G6's mark asserted the dropped age check; F4 and T4 (V1/V5); F15 (V8); L5 and U1 (V2); E5 (C16); R6 (E9). | Marked. |
| C45, C46, C47 | §12's own C30 vs V5 (7 vs 28 days); C34 claimed an alignment §1 ¶1 lacked; C8 "four rounds" in §11.2. | C30's row marked ⟂ W2; §1 ¶1's lists aligned with §5.3 (C50); six rounds everywhere. |
| C48 | A stolen guardian key is a continuous 28-day freeze (the thief vetoes its own replacement and re-flags on every unpause), not "a day at a time … 7 days". | §1, §9 say so, and why the plan accepts it. |
| C49 | "Every exit open" under a forgery was false: forged synthetic puts supply above the lock, the signers' refusal rule stops crediting, and no rule told honest burns from forged ones. | §1: the exits stay open as far as the lock reaches; burns after the fork are credited by vote in claim order (⟂ B-3: pro rata, a haircut for all); the shortfall is GovDAO's to allocate. |
| C50 | The at-once and 7-day lists differed between §1 ¶1 and §5.3. | Aligned. |
| C51 | The first-day bound mixed one cap and two; "a fifth" is two caps. | One cap from an empty meter (the floor or a tenth), two caps otherwise (20,000 or a fifth) (§5.4, §9). |
| C52 | A hold-opened recovery was deferred by the hold that opened it. | Recovery decisions are never deferred; the procedure (veto the backlog, execute, lift) is in §6.4. ⟂ v23 (see the preamble). ⟂ v27 (see the preamble). |
| C53 | §1's brake was stated for launch; until the bootstrap the multisig can replace guardians after a day and route around the ISM. | §1 says so. |
| C54 | "A direction" on gno.land pauses a route both ways; the pausable ISM's "second direction" had nothing to gate. | One flag per key per object; a gno route pause stops sends; the watch keys mirror a Solana delivery pause onto the gno route (§1, §5.3, §5.9) — ⟂ B1–B3: three flags per key per route on gno.land since v9. |
| C55, C56 | Review counts; three floats vs four. | Six rounds; four floats (§5.5, §5.9) — ⟂ round-15 (c): five. |
| C57 | The treasurer and the outsiders were asserted in the body and open in §11. | "As proposed" with the §11 pointers (§5.5, §1). |
| C58 | The storage-deposit line mixed directions. | Split by direction (§5.5). |
| C59 | Dust and the sweep queue were outside the identity. | Dust goes to the sweep queue; the sweep queue is owed; voided is the unpaid remainder (§5.4, §5.9). |
| C60 | Three guardian lists, one checked. | The bootstrap writes the admin program's from the pausable ISM's; §6.6 and §8.3 check all three — ⟂ AC3 (the admin program's list is init data), and moot in v16: the setup transaction copies that list into the pausable ISM (§6.5). |
| C61, C62 | Proposal 1's gno raises could land while the Solana decision was vetoed; the irreversible hand-over preceded the vote it claimed to be. | The bootstrap executes on Proposal 1's vote, never before; the raises are a second vote after the first decision executed (§6.6, §6.7, §8.3). |
| C63 | README: the pausable ISM unlisted, `hlsol setup`'s shared defaults unmarked, `hlwatch` missing. | README fixed. |
| C64–C70 | ≥22 days is 21; the first-day bound; the 3 SOL did not close the gap (guardian and watch keys unbudgeted); Phase 2's fee and budget below the per-block cost; the 50 GNOT gas float trips on launch day at stress; "two more programs ≈ 3 SOL" unlabelled; "positive only if the fees are GNOT" needed the share. | 21 days (§6.4); 4 SOL with the split (§5.8, §11.4); a 50 GNOT fee and $100/day (§7.2 — ⟂ round-8 (c): the day's proving fees with a $100 floor); a 500 GNOT gas float and the four runways (§5.5); labelled a guess (§10); "while roughly half or more are GNOT" (§10; ⟂ round-7 (c) and round-8 (c): a third to two thirds; ⟂ round-10/11 (c): two fifths to three quarters). — ⟂ round-13 (c): 6 SOL since v16 (six program deployments, §5.8, §11.4); every "⟂ C66" above reads 6. — ⟂ round-15 (c): five floats. |
| (d) | The BUBBLE fee floor; the decision body's unused dispatch height; "meter" vs "bucket"; "inbox"; the launch notice; "the trailing rate" on launch day; the Solana words. | 40 BUBBLE (§5.3); the height dropped (§5.3, §13); tied in §5.4; "its decisions" (§3.6); defined in §5.3; "the planned first-week rate" (§5.5); glossed in §13. |
| (e) | The skeptic's list: the outsiders hypothetical and a validator operator would sit in both houses; the operator cannot be removed from its seats; what the week buys on gno.land; §11.1 has no owner; the agents get model review only; the review scope; a build order and "who verifies the verifier". | §11.9 (outsiders outside the validators too, with backups); §1 (co-owned; half the lock leaves in a week; §11.1 Jae's, no date); §11.2 (the agents in scope); §5.8/§8.3 (the audit prints its RPC calls for a member to repeat). |

### Round 6 — red team on v6

Nothing critical or high; three medium findings on the brake's edges and three build gaps.

| # | Sev | Finding | Answer |
|---|---|---|---|
| W1 | medium-high | The 28-day stretch applied only to the replaced guardian's own veto, so GovDAO could name the sleeping outsider: the awake one's lone veto left the 7-day clock, and the brake was gone in ~35 days. | §6.5: any one guardian's veto stretches a `set guardians` to 28 days; §1 states the liveness the brake needs (one outsider within a week, then four weeks for the other — ⟂ X1: since v8 either outsider alone voids any decision but a replacement, so one awake outsider suffices; ⟂ Z1/AA4: not against its own removal until v12's affirmation rule). |
| W2 | medium | A stolen guardian key is a continuous freeze of up to 28 days (re-flag the moment each unpause arrives; veto the replacement), not "a day at a time"; a watch key 7 days. | §1, §9 say so; the plan prefers it to a brake one sleeping outsider loses. |
| W3 | medium | The watch compared only the trusted header; a forged *proof* header at a past height carrying a fake decision moved no supply and tripped nothing, and nothing matched recorded decisions against r/hlgov's list. | §6.2: the client logs every proof header it verifies; §5.9/§5.8: the watch and `hlwatch` compare each with gno.land's, flag any recorded decision absent from r/hlgov's executed list, read two independent sources, and re-assert flags with a fresh slot every poll while an invariant is broken. — ⟂ AD3: the record is instruction data, one counter over updates and proofs. |
| W4 | medium (build) | The successor hand-over was dead: r/hlgov dispatched to a fixed recipient and Hyperlane delivers by the address in the message. | §5.3: r/hlgov's `set decision recipient` builder (7 days); §6.5: a successor receives decisions only once it names it. |
| W5 | medium | A hold-opened recovery was deferred by the hold; "≥22 days" was 21. | §6.4: recovery decisions are never deferred; the procedure; 21 days. ⟂ v27 (see the preamble). |
| W6 | medium (trust) | §1 omitted that the operator's two vetoes block any 7-day decision forever and that it can never be removed from its seats; `hlwatch` did not check the supply identity; the operator's share of validator power was unknown. | §1: "co-owned: GovDAO proposes, the operator and the outsiders can each block"; `hlwatch` checks the identity from its holder's sources (§5.8); §1/§8.1: the five validators resolved to operators in Proposal 1, and if the operator holds two thirds Phase 1's claim is hollow and the proposal says so. |
| W7 | low-medium | The first-day bound mixed 1× and 2×; dust and partially paid voided claims were outside the identity. | As C51/C59 (§5.4, §5.9). |
| W8 | low-medium | Vetoes were not said to survive a guardian's replacement, nor to void a deferred decision. | §6.5: a veto stays with the decision; two vetoes void at maturity whether or not a flag deferred it; a replacement clears the replaced key's flags; "two guardians' flags" is per object. |
| W9 | low-medium | After a rotation the client never followed, the recovery set is the rotated-out one, whose assent nobody has. | §6.4: the assent covers 30 days after leaving the set; GovDAO times rotations while the updater is live. |
| W10 | low (build) | The pausable ISM's second direction had no enforcement point; an unknown kind byte at last+1 would refuse (V1 again); a matured `unpause` under a hold must be spent; 22 → 21. | §5.3 (one flag per key per object — ⟂ B1–B3: three per key per route on gno.land since v9; the watch keys mirror a delivery pause onto the gno route's `sends`), §6.5 (unknown kinds recorded and spent; a matured `unpause` under a hold is spent), §6.4 (21). |

Checked and held: §6.5's recording rule closes V1 and `cancel` is channel-bound; the gno-side powers are stated as
GovDAO's sovereignty; a set already rotated out is powerless once followed and the pre-follow window needs +2/3 of
the old set; Hyperlane's verify → record → handle order, per-ISM process authority, default ISM only when the
recipient names none, owner-only ISM pointers and routers, no pause in the multisig ISM; GovDAO's gate (the proxy or
a `gno.land/r/gov/dao/…` subpackage — slightly wider than "=="), error = denied, panic = retry, three T1 members
live (g1aeddl…, g1ecsu…, g1manfred…); validators applied at execution, announced at A+1, signing from A+2; VM-stamped
`pkg_path`; a cross-realm panic aborts; the identity with the fee as a first installment; the k/(k+1) bound; the
Phase 0 code as the plan says it is (pre-plan).

### Round 7 — fact check and consistency of v7

Verdict given: the architecture is consistent and every measured number stands; the trust statement had two
substantive faults (the watch's mirror closed the exits §1 promised; §1 never said one person holds a GovDAO vote,
the operator and a guardian seat) and an editing pass. Folded into v8:

| # | Found | v8 |
|---|---|---|
| C71 | §3.3 still carried v6's "the guardian being replaced can alone stretch" rule. | §3.3 rewritten with the v8 veto rule. |
| C72 | §10's review line kept the old scope, weeks and price. | Aligned with §11.2 (2–3 weeks, ≈$20–35k, the agents in scope). |
| C73–C76, C79 | U7, E2's mark, the round-3 and round-5 notes still said 3 SOL; C23's row and the round-4 numbers said ≥22; G9 read as if removal were GovDAO's free act. | Marked ⟂ C66, ⟂ C64, ⟂ W2. |
| C77, C78 | Pointers: the raises were not in §8; the mirror was not in §5.3. | §8.3 checks the first decision executed before the raise votes; §5.3 defines the `sends` flag the mirror sets. |
| (b)1 | The mirror plus the all-or-nothing route pause shut `Process` and `Claim` in every ending where §1 promised the exits open. | Three flags per key per route (`sends`, `credits`, `payouts`); the mirror sets `sends` only; a watch key's invariant break sets `credits` and `payouts`; §1's leak ending names GovDAO's at-once proposal (void, reopen, retire the watch keys — ⟂ B7, then Z6: v9 dropped the retirement for "the identity closes", v11 restores it, since unburned forged synthetic keeps the supply side broken). — ⟂ round-16 (b)6: three proposals, the void first. |
| (b)4 | "Within a day of the vote" needed the Squads proposal queued beforehand. | §6.6 says so, announced to the monitors. |
| (b)5 | The refusal rule `supply > lock + in-flight` was loose by owed + voided. | `supply > lock − owed − voided` (§5.6) — ⟂ B7: `+ lost` since v9. |
| (b)6 | §1, §6.6, §8.3 omitted the validator-set exclusion for the outsiders. | Added. |
| (b)7, (b)8 | "Up to 28 days" is a floor; "four weeks for the other" is 28 − t₁. | "Until 28 days after the replacement is delivered" (§1, §9); the second outsider's clock is moot since X1 (one outsider alone voids). |
| (b)9–(b)12 | §9 stated §11.3 as done; "one SOL key"; §1 ¶1's three proposals vs three votes; the credit builder took one id. | "Would add"; "single SOL key"; "(the first of them three votes, §6.7)"; void, restore and credit take lists. |
| (c) | Stress top-ups weekly (the ATA payers last ≈3 days at 250 new recipients a day); the gas key 8–10 days; costs $360–670 and the GNOT share ≈ two thirds; §9's 1/(k+1) only for k ≥ 3; April vs May 2027; the Phase 2 per-block value and the sub-55-GNOT burn. | §5.5, §10, §9, §7.2 (twice a week; 8–10 days; two thirds; min(1/4, 1/(k+1)); April if Phase 1's work overlaps Phase 0's finish, May if not; $20 per block, a week's carry-over, the router minimum and the warning) — ⟂ round-8 (c): every three days, $365–680, a third to two thirds; ⟂ round-10 (c) and round-11 (c): $450–765 with the stipends, two fifths to three quarters; ⟂ round-9 (b)1: no router minimum exists in Hyperlane's program, the client and the documentation carry it. |
| (d) | The timing lists omitted set treasurer, set fee floor, set verifier and fee lowering; whether a late veto counts; thresholds; "the members' channel"; undefined bootstrap/builders/updater/admin program/pausable ISM/ATA payer; the BUBBLE Bootstrap fee. | §5.3 (complete lists; lists of ids); §6.5 (counted at execution); §5.9 (30 min, 2 h); the glossary; §5.5 (100 at Bootstrap). |
| (e) | Jae holds a GovDAO vote (g1ecsu…), the operator, a guardian seat and the interim multisig; the outsiders hypothetical; replacements re-proposable monthly; the validators' operators unresolved; Phase 2 burns under ≈55 GNOT a total loss; the conversion chore's successor; "when built". | §1 ¶1 says Jae's roles first; §11.9 (backups, on call); one replacement per seat per 90 days (§3.3, §6.5 — ⟂ Y4: dropped in v10, since a voided replacement would have protected a thief's seat; the plan now acknowledges monthly re-proposals and prices them at one veto each); §1 (the five resolved) and §8.1; §7.2 (the router minimum); §1 (GovDAO names the next treasurer; the fees fund the next relayer). |

### Round 7 — red team on v7

| # | Sev | Finding | Answer |
|---|---|---|---|
| X1 | high (trust) | The 28-day stretch protected only `set guardians`: against a GovDAO pair that includes Jae (g1ecsu… is his cold key and a T1 member, so his guardian key sits with the pair) the brake was the two outsiders both acting within 7 days of every re-pointing decision, and a lone veto was "withdrawable until maturity". | §3.3/§6.5/§6.4: any one guardian's veto stretches every 7-day-group decision, recoveries included, to 28 days; either outsider alone voids any such decision except a guardian's replacement (two vetoes, so a stolen outsider key cannot keep itself — ⟂ Y1/AA4: since v10 an outsider seat's replacement is voided by the other outsider alone, and since v12 by its own veto unless the other affirms; only an operator seat's replacement still takes two); vetoes are never withdrawn and count at execution; §1 says the outsiders are the whole brake against that pair and what one awake outsider buys. |
| X2 | medium | The watch keys' mirror closed the gno exits in every ending §1 promised them open. | As (b)1 above: the mirror sets `sends` only. |
| X3 | medium (trust) | §1 did not say one captured, bought or dead outsider was the whole brake against that pair, nor that a thief could withdraw standing vetoes. | §1: a stolen outsider key blocks governance until 28 days after its replacement is delivered (⟂ AA4: which needs the partner's affirmation, and a silent partner leaves the thief in place) and re-points nothing; both outsiders lost at once is the brake lost until replacements land; vetoes cannot be withdrawn (§6.5); named backups (§11.9). |
| X4 | medium-low (build) | A hold-opened recovery window never closed. | §6.4: a window stays open only while its trigger stands; a recovery decision whose window closed before execution is void. |
| X5 | low-medium (build order) | An immutable Phase 0 r/hlgov targeted a Phase 1 program's kinds and encodings. | r/hlgov is deployed in Phase 1 with the admin program, once §13's kind table is frozen (§4, §5.3, §10). |
| X6 | low-medium (build) | "Spent on a failed execution" cannot be written when a CPI aborts the transaction; a `set guardians` meeting a hold on the pausable ISM would burn a 28-day replacement. | §6.5: spent only on the admin program's own pre-validation; a failed CPI leaves the decision pending; a replacement meeting a hold is deferred. ⟂ v23 (see the preamble). |
| X7 | low (build) | A proof-header ring could be flushed by cheap deliveries between polls. | §6.2: a counter and running hash over every proof header, emitted as program logs; the watch indexes them and checks for gaps. — ⟂ AD3: the record is the staging transactions' instruction data, one counter over updates and proofs (§6.2). |
| X8 | low (build) | Which monitor conditions flag and which alarm was unsaid; a liveness metric that flags feeds its own pause. | §5.9 names the flagging invariants; stalls, ages, runways and lag alarm only. |

Checked and held: one rogue guardian cannot stretch forever (its own replacement once, then it is out); vetoes
surviving replacement and the per-object hold rule create no lock-out; Hyperlane's `inbox_process` order and the
default ISM only on none; the admin program must implement the full recipient interface; GovDAO's error/panic
rule; the bootstrap's Squads transaction queued before the vote leaves no exploitable window; a voided decision
re-dispatched to a successor gets a fresh, vetoable clock; a lapse-opened forged recovery dies to any fresh header,
a hold-opened one to its holders' veto; the watch against a forged chain is sound while the outsiders' `hlwatch`
reads their own nodes; §5.4/§5.5's meter, dust, void and fee rules; the Phase 0 code as the README says (pre-plan).

### Round 8 — fact check and consistency of v8

Verdict given: the structure, the timing model, the veto rule and every measured number stand (rents re-measured
on 10-11 to the lamport; the chain live at 708,606); one day of editing and one design sentence away. Folded into v9:

| # | Found | v9 |
|---|---|---|
| C80–C89 | Unmarked overturns and stale pointers: G5's refusal rule; F1, G6, U4, V5, W1, G11 on the veto (X1); the round-7 (b)7/8 row's claimed phrase was nowhere in §1/§9; "half or more GNOT"; C27's "everywhere" while §9/§11.9 said "trust-path decision"; T5's "pausable ISM paused during recovery"; "below" pointing at nothing; E3's "cap × days". | Marked; §9 and §11.9 say "any decision in the 7-day group". |
| B1, B2, B3 | The three-flag model was half-applied: §5.9 said "and nothing else", §5.1 kept one `Paused(route)`, §1 said "(both ways)", §5.8 "per direction", §3.3 omitted the gno flags. | One model: three flags per key per route on gno.land, `Paused(route, fn)`; the watch keys set `sends` to mirror a delivery pause and `credits`/`payouts` on an invariant break; one flag per key on the pausable ISM; §1, §3.3, §5.1, §5.3, §5.8, §5.9 agree. |
| B7 | A voided forgery kept the identity broken forever (the amount moved from owed to voided), so the refusal rule would stop every signer for good after the first one. | A void has two forms: withheld (restorable; the remainder stays booked) and forged (the unpaid remainder dropped, the paid part booked as `lost`); the identity is `lock = supply + owed + voided − lost` and the refusal rule `supply > lock − owed − voided + lost`, so the signers resume once GovDAO voids as forged (§5.3, §5.6, §5.9, §1). |
| B4, B5 | §4 still said "2-of-4 veto"; §11.9 said one outsider "can block for 28 days" (the W1 rule). | Both carry the v8 rule. |
| B6, B8–B13 | A dead GovDAO "stays paused" only if the flagging guardian does not clear; six vs seven rounds; §10 "out of the trust path" (only gno → Solana at Proposal 1) and "under the chain's own governance" (co-owned); "§13's kind table" did not exist; §5.9 said the monitors both alarm and flag on a source disagreement; §3.3's "two houses" without the Jae caveat; §1's guardians bullet lacked the validator-set exclusion. | Fixed: the caveats, "eight rounds", §10's wording and the reason Phase 0 launches first, the kind table in §13 (kinds 1–10 with parameters), "flags", §3.3's caveat, the exclusion. |
| Prop #10 | §1's "four of the five" is right today and wrong if Prop #10 passes (moul to power 1: any three of the four 60-power validators hold 75%). | §1 conditions the arithmetic on Prop #10. |
| (c) | "Twice a week" at stress is every three days (the ATA payers last 3.3 days at 250 new recipients); costs $365–680 and the GNOT share a third to two thirds (⟂ round-10/11 (c): $450–765, two fifths to three quarters); hlmailbox's send record ≈0.02–0.04 GNOT; the Phase 2 daily budget below its own base case ($50–300 of proving against $100). | §5.5, §10, §7.2 (the day's proving budget is the day's proving fees, floor $100). |
| (d) | The 90-day rule's start (⟂ Y4: the rule was dropped in v10); when an unknown kind is spent; the flagging conditions' objects; r/hlgov's first recipient; the per-message maximum's group; the mirror's clearing; the outsiders' gno keys; `hlwatch`'s medium; the kind table; undefined synthetic, foreign decision, the fork, the loader and buffer, SetAuthorityChecked, KMS, T2/T3, FEC/shreds. | All given (§6.5, §13, §5.9, §5.3, §5.6, the glossary). |
| (e) | A bought outsider plus the operator captures the brake for good (the mirror of the operator's own seats); who runs gno-core-validator-1; an outsider-run monitor from Phase 0; why launch Phase 0 before Phase 1; the shortfall's allocation rule. | §1 (the mirror image stated; gno-core-validator-1 is the gno core team's, its people named in Proposal 1; pro rata among post-fork burns as proposed); `hlwatch` from launch (§5.6, §10); §10 (why Phase 0 first). |

### Round 8 — red team on v8

| # | Sev | Finding | Answer |
|---|---|---|---|
| Y1 | medium-high (trust) | A `set guardians` could name both outsider seats and was void only on two vetoes, so with one outsider silent for 28 days a GovDAO pair including Jae replaced both and re-pointed on day 35; §1's "one awake outsider" was wrong by one. | §3.3/§6.5: a replacement names one outsider seat at most and is void on the *other* outsider's veto alone (the named seat's own veto counts as one of two, so a stolen key still cannot keep itself); §1 states the residual — both seats silent for 28 days in turn, about nine weeks (⟂ Z1, AA1: a silent seat falls in 7 days, so the figures were a week and five; ⟂ AA4: since v12 the named seat's own veto voids unless the other outsider affirms, so one awake outsider cannot be removed) — and that each seat's key is held by its person and a named backup. |
| Y2 | medium (trust) | Until Phase 2 the Solana → gno exit is the operator's signers, so "exits open" and "never strands" were conditional on the operator: it alone can close that exit for 7 days plus a rotation, a pair including it for good. | §1's signers bullet and §9 say so; "never strands" is now gno → Solana from Phase 1 only. |
| Y3 | medium (build) | Whether a stolen watch key's own flag deferred its replacement was unstated; read literally it made the freeze permanent. | §6.5: a lone flag never defers the `set guardians and watch keys` kind, whichever keys it names. |
| Y4 | medium-low | The 90-day rule had no clock, and either clock had a cost: a voided replacement protecting a thief's seat, or monthly re-proposals returning. | The rule is dropped; §1 and §6.5 acknowledge a re-proposal every 28 days and price it at one veto of the awake outsider, alarmed by `hlwatch` (⟂ AA2: since v12 a standing veto makes re-proposals cost the outsider nothing). |
| Y5 | medium-low (build) | The admin program was never told which seats were outsiders, nor when a veto's seat was fixed; a pair could name an insider into an outsider seat. | §5.8/§6.5: four numbered seats, 3 and 4 the outsiders', fixed at deploy; a veto names a recorded decision and carries its seat; no key in two seats or in a seat and a watch slot. ⟂ v23 (see the preamble). |
| Y6 | low-medium (trust) | `hlwatch` is the operator's tool and nothing pinned it. | §5.8/§8.1/§11.2: a reproducible build hash in the launch notice and Proposal 1, in the review scope, compiled by each outsider. |
| Y7 | low-medium | The supply identity misses a diverted credit (a real burn credited to the signers' own address moves no supply). | §5.9: a credited claim whose recipient or amount differs from its dispatch record flags. |
| Y8 | low (fact) | The forging margin is one outside validator; Prop #10 changes the count. | §1 says both. |
| Y9 | low (build) | Unsaid: anyone executes a matured decision; a counter gap in the proof-header log flags; `hlwatch` alarms on a quarantine start; a recovery `set guardians` follows the exception; a veto names a recorded decision; the Phase 0 pausable ISM with no client reads "no quarantine"; the assent may lapse mid-recovery; GovDAO's gno `unpause` reopened crediting with payouts. | All in §5.3, §5.8, §5.9, §6.4, §6.5. |

Checked and held: a stolen outsider key cannot keep itself and is out 28 days after its replacement's delivery; a
lone flag never blocks a replacement; a hold defers it and a 14-day hold opens the recovery no flag defers; the
operator's seats and two rogue outsiders are symmetric endings; decisions are delivered under the raw light-client
ISM, so no flag blocks delivery (V1 holds) and a forged decision from an unsettled set is vetoable like any other;
a forged `cancel` touches only GovDAO's own pending decisions; a hold-opened recovery needs the settled set, which a
rotation under a hold never becomes; vetoes counted at execution keep a deferred decision's window open; the three
gno flags and the mirror; Jae's roles; the five validators; the recovery digest and window; r/hlgov in Phase 1;
the pair's cheapest path is still `migrate` on gno.land, disclosed in §1.

### Round 9 — red team on v10

Nothing critical or high. One trust sentence, three Phase 1 build gaps, the rest low.

| # | Sev | Finding | Answer |
|---|---|---|---|
| Z1 | medium-high (trust) | "Both seats silent for 28 days in turn" was wrong by one: once a pair holds one outsider seat, the survivor's own veto cannot void the replacement of its own seat, so one 28-day silence at the wrong time loses both seats in about nine weeks, the survivor awake or not. | Said plainly in §1, §9, §11.9; the backups are the whole mitigation. A rule with a 56-day grace for the survivor's own veto was considered and not taken: it would let a stolen key keep itself for 56 days after any legitimate replacement of the other seat. ⟂ round-9 (b), AA1: the clocks were a week and five, not three and nine; ⟂ AA4: v12 adopts the affirmation rule instead, under which an awake outsider cannot be removed at all. |
| Z2 | medium (build) | §6.4 promised re-pointing the client and a new chain id, but kinds 1–10 could not express it and the admin program's accepted origin was a constant; the successor's seats were unspecified. | §13: kind 11 `reset client` (recovery only: chain id, origin domain, trusted header, set) — the only way the accepted origin changes; §6.5: a successor takes the four seats and watch slots as they stand; a recovery body carries sequence 0 (§6.4). |
| Z3 | medium (build) | Kind 5 carried pubkeys, no seats, while every rule is seat-based. | §13: pairs `seat ‖ pubkey`, at most two guardian seats and one of 3–4 per decision; watch slots 5–6. |
| Z4 | medium-low (build) | A diverted credit had no cure but `migrate` (the id was done; `credit` refused done ids); a forgery voided as withheld kept the identity broken with no re-void. | §5.3: `credit` replaces a voided claim's record with the recipient and amount GovDAO names; a withheld void may be re-voided as forged; `restore` reverses either form (a forged one back out of `lost`). |
| Z5 | medium-low (build) | Kind 1's channel byte invited a cross-channel cancel: a forged mailbox cancel could kill a hold-opened recovery at once. | §6.5/§13: a cancel touches only decisions of the channel it arrived by; the byte is gone. ⟂ v24 (see the preamble). |
| Z6 | low-medium (trust) | §1's leak ending said voiding closes the identity and the watch keys stand down; forged synthetic still unburned keeps the supply side broken. | §1: GovDAO retires the watch keys in the same proposal; automatic crediting is over for that deployment. — ⟂ round-16 (b)6, AG1: three steps in order, the void first, for the ids a guardian's flag names. |
| Z7 | low | A mistaken void as forged of an honest paid claim raised the refusal threshold for good. | §5.3's `restore` of a forged void; `hlwatch` alarms on a forged void of a claim whose dispatch record matches (§5.8). |
| Z8 | low (trust) | Against forged deliveries the lever is flags: a lone outsider is a 24-hour re-flag loop against un-vetoable unpauses; two hold. | §1 says so — ⟂ AB1: since v13 an outsider's standing veto voids unpauses, so one outsider's flag holds for as long as its seat stands. ⟂ v23 (see the preamble). |
| Z9 | low (trust) | In Phase 0 an operator stop closes both directions and GovDAO can rotate only hlmailbox's set. | §9's row says so. — ⟂ v16: gno → Solana is unaffected, no signer is on that path (§9). |

Implementer items now written down: the seat encoding (Z3); kind 4 carries the client's state account (⟂ round-17 (b)1: derived from the target since v20); the
successor's seats (Z2); recovery bodies at sequence 0 with the counter in the digest; vetoes name a channel and
number; the void state machine (Z4); the Phase 0 mirror reads the signers' published quarantine (§5.9); `hlwatch`
indexes dispatched-message accounts by id (§5.8).

Checked and held: GovDAO's error/panic rule; Hyperlane's verify → record → `handle` order, the inbox default on
none, a per-ISM process authority for `Verify` and a per-recipient one for `handle`; Prop #10 still open; BUBBLE's
admin has no burn, freeze or clawback; a stolen outsider key cannot keep itself and a rogue outsider blocks at most
28 days plus delivery; watch keys add nothing to the operator's two seats; the identity algebra under void as
forged; a hold-opened recovery is always vetoable by its holders; `unpause` under a hold is spent; anyone executes a
matured decision; both outsiders lost = replacements in 7 days each; the pair's cheapest path is `migrate`.

### Round 9 — fact check and consistency of v10

Verdict given: every v9/v10 fix is really in the text, the architecture and every measured number stand; not yet a
trust statement because one number a member weighs was three times its true value and one independence claim
named Jae to check Jae. Folded into v11:

| # | Found | v11 |
|---|---|---|
| C90–C98 | Unmarked overturns: E9's identity; C54's and W10's "one flag per key per object"; C64–C70's "$100/day"; round-7 (b)1's "retire the watch keys" and (b)5's rule; round-7 (c)'s figures; G5's pointer; T14's "ends paused". | Marked. |
| (b) "nine weeks" | A silent seat does not veto, so with both outsiders ignoring every alert the pair replaces them and re-points in 7 + 7 + 7 = three weeks; nine is the case where each named seat objects to its own removal. | §1: three weeks if both sleep, six if one is awake and objects, nine if each objects to its own removal only; §9 and §11.9 follow — ⟂ B-1/AA1/AA4: a week if both sleep, never if one watches with an honest partner (v12). |
| (b)2 | §7.3's comparator "a GovDAO member with a guardian key" is Jae checking Jae's signers. | An outside guardian runs it (§7.3). |
| (b)1, (b)3–(b)7 | The Solana router has no minimum (Hyperlane's program, byte-identical); the multisig ISM's set between the hand-over and the first decision's execution was unstated; kind 5 carried no seats, kind 1 a channel byte, "re-pointing the client" had no kind; the README's identity lacked `− lost`. | §7.2 (the client and the documentation carry the minimum); §5.3 (the window: the operator's three under the pausable ISM, 7 days, 28 if vetoed); §13 (kinds 5, 1, 11 — with Z2/Z3/Z5); the README. |
| (b)8–(b)16 | "The only seats he does not hold"; the unpause builder's wording; §5.8's `Verify` omitted the quarantine; "meters start empty after every unpause" (of `payouts`); a stolen key's voided decisions each need a fresh proposal; "never strands" unqualified; §1 ¶1 stated only the two-guardian veto and asserted the outsiders; §2's gate wording. | All reworded (§1, §2, §5.3, §5.4, §5.5, §5.8). — ⟂ AF1 (the unpause that resets). |
| (c) | k/(k+1) binds only for k ≥ 3 (min(k/4, k/(k+1))); Phase 2's dear end ($6 a block × 50) exceeds the fee income, making credit by vote routine. | §5.4; §7.2 (Proposal 2 sets the fee to the measured cost, ≈90 GNOT at the dear end). |
| (d) | Where the proving fee goes; the first-week rate; the watch keys' gno funding; Phase 1's SOL; Phase 2's audit budget; who runs replacement signers; the outsiders' compensation; the Phase 0 quarantine as a mirror source; terms (the 7-day group, quarantine, lapse, "the watch" vs `hlwatch`). | §7.2, §5.5, §5.6, §11.12, §11.13, §5.9, the glossary. |
| (e) | A provider outage halts two signers (no row); a non-operator signer from Phase 0; Phase 2's economics; the comparator; Jae's concentration; the assent's evidence; funding. | §9 (the provider row); §11.13 (a third-party signer, recommended); §7.2; §7.3; the rest stand as §11's open items. |

### Round 10 — fact check and consistency of v11

Verdict given: editing, not design — the architecture, the state machine, the kind table, the identity and every
measured number stand; the one sentence a member weighs most (how long the outsiders may sleep) had moved the
wrong way, §9/§11.9 kept the older figure, and the multisig ISM had no rotation kind after the bootstrap. Folded
into v12:

| # | Found | v12 |
|---|---|---|
| C99–C105 | F6 (k/(k+1)), F14 (the comparator), Y1, Z1, V5/W1 (the clocks) unmarked; the round-9 rows claimed §9/§11.9 and §8's comparator check were fixed when only §1 and §7.3 were. | Marked; §9, §11.9 and §8 Proposal 2.2 fixed. |
| B-1 | The clocks: a silent seat never vetoes, so with neither outsider looking everything passes and both seats go in one week; one awake and objecting with the other silent 28 days: five weeks; nine only if the sleeper wakes at day 28 and vetoes everything. | §1, §9, §11.9 restated — then restated again under AA4's rule (one week if both sleep; never if one watches). |
| B-3 | Claim order and pro rata both proposed for a short lock. | Pro rata (§1). |
| B-4, B-5 | Round counts; no kind rotated the multisig ISM's set between the hand-over and the first decision, and a voided first decision stretched that window. | "The rounds of §12"; kind 12 `set multisig ISM signers` (§5.3, §13). |
| B-6–B-11 | "Leave the path" (trust, not liveness); min(k/4, k/(k+1)) wording; kind 11's timing group; §3.1's "never by a key" vs the signers' credits; glossary gaps (voided, lost, stranger/foreign); §9's capture and provider rows. | All reworded. |
| (c) | 66.66%; "forged claims" (four recipients' worth); 21 or 42 days; the stipend in §10 ($395–710, two fifths to two thirds — ⟂ round-11 (c): $450–765, two fifths to three quarters); ≈95 GNOT at the dear end; the 0.3 SOL guess's range in §11.4; impl v0 vs v3 on master. | §1, §5.4, §6.4, §10, §7.2, §11.4, §2. |
| (d) | Restore and credit-over-void's counter effects; the comparator's flag, pinning and review; the per-message maximum in the raise list; abandoned staged update PDAs; when the stipend starts; glossary (the lock, SIMD, a decision includes a recovery). | §5.3, §7.3, §6.1, §11.12, the glossary. |
| (e) | Phase 2 pricing for small holders. | §7.2: the path is for hundreds of GNOT and up; small holders pool. |

### Round 10 — red team on v11

| # | Sev | Finding | Answer |
|---|---|---|---|
| AA1 | medium (trust) | §1's clocks (three/six/nine weeks) contradicted §6.5 and §1's own "within a week": with neither outsider vetoing a re-point executes on day 7 with no replacement; one awake: its seat stretched to 28, the sleeper's gone at 7, the re-point at 35 — five weeks; "each objects only to its own removal" lets the re-point pass on day 7. | §1, §9, §11.9: a week if both sleep; under AA4's rule, never if one watches. |
| AA2 | medium (trust, build) | Vetoes were one per seat per decision, so a flood of 1,000 identical decisions (≈$120) or an unpause a minute defeated a human outsider; §1's "one awake outsider" meant "one outsider with a bot". | §3.3/§6.5: a per-seat standing veto that vetoes every 7-day-group decision recorded and every `unpause` delivered while it stands; pending decisions are per-decision accounts paid by the deliverer. |
| AA3 | medium (Phase 2 trust) | The lattice-delta cost is attacker-controlled by anyone for ≈1 SOL a day (one-lamport transfers to large program-data accounts force their re-hash), so every burn's block can be made "over the budget" and the trustless path degrades to credit by vote. | §7.2, §9: said plainly — the trustless claim holds for un-griefed blocks; the griefed cost is measured before Proposal 2 and designed around (per-account commitments across proofs, or a blake3 precompile budget) or the degradation is stated as the rule. |
| AA4 | medium-low (design) | A replacement rule the plan had missed: void on the named seat's own veto unless the other outsider affirms — one awake outsider survives its own removal, a stolen key is still out when the partner affirms, a stolen key with a silent partner keeps only a brake. | Adopted (§1, §3.3, §6.5, §11.9); the cost (a stolen key plus a silent partner and backup freezes governance until one affirms) is stated. ⟂ v24 (see the preamble). |
| AA5 | low-medium (build) | Restore and credit-over-void's counter effects were unspecified; a wrong reading broke the identity by a claim and paused the bridge for a week. | §5.3: restore is the full inverse of either void; credit over a void restores then re-credits; §5.9: the watch's identity flag is one-sided. |
| AA6 | low (build) | When the recovery counter advances was unsaid; at execution, a voided or window-closed recovery's signatures replayed. | §6.4: the counter advances and the signatures are checked at recording, against the chain id of that moment. ⟂ v25 (see the preamble). |
| AA7 | low (build) | The pausable ISM must prepend its own accounts to the inner ISM's `VerifyAccountMetas`, not forward verbatim; kind 4's state account needs an owner and layout check. | §5.8, §13. — ⟂ round-17 (b)1: kind 4 carries the target and a layout version; the state account is derived from the target. |
| AA8 | low (build) | "Disagreement between sources" undefined (a public-RPC stall would pause every ten seconds); removal clearing flags vs the retired watch keys' `sends` mirror. | §5.9 (conflicting hashes at one height only); §1 (a guardian re-sets `sends` in the retiring proposal). |
| AA9 | low (trust) | No rotation of the multisig ISM's set in the post-bootstrap window. | Kind 12 (as B-5). |

Checked and held: kind 11 adds no power (the recovery set can already forge any header) and every carried kind
keeps its clocks and vetoes; a mailbox-channel kind 10/11 is spent; old-origin messages are refused after a reset;
the hold-opened recovery is vetoable by its holders and a lapse-opened forged one dies to a fresh header; the pair
cannot reach +2/3 of today's set; a stolen outsider key cannot keep itself; the identity algebra and the one-sided
refusal rule; Hyperlane's order and process authorities; the Phase 0 code is pre-plan; the pair's cheapest path is
`migrate`.

### Round 11 — red team on v12

Nothing above medium; three trust sentences and five build details.

| # | Sev | Finding | Answer |
|---|---|---|---|
| AB1 | medium | A veto's effect on an `unpause` was undefined, so the standing veto meant either a one-outsider hold (unsaid) or nothing (AA2 vacuous); §1 still called one outsider "a 24-hour re-flag loop". | §6.5: an `unpause` vetoed by either outsider seat before its 24 hours run is spent without effect; §1: one outsider's flag, kept by a standing veto, holds deliveries for as long as its seat stands; a standing veto also voids every recovery, so it is left only for absences short of a lapse. — ⟂ round-12 (b)5–(b)8: before it executes. |
| AB2 | medium (trust) | "If one is watching the pair can remove neither" was false against a bought or stolen partner, who affirms the watcher's removal; the "mirror image" sentence gave the wrong mechanism. | §1, §9: the brake needs both outsider keys honest and one awake; a bought partner plus the pair ends it in five weeks; a bought seat is permanent because the operator's two vetoes void its replacement; `hlwatch` shows every affirmation and the key a replacement names (§5.8). |
| AB3 | medium-low (trust) | §1's end state and signers bullet sold Phase 2 as fully trustless; griefing only the burn-carrying blocks costs cents; the fee on a vote-credited claim was unsaid. | §1 (both places): for un-griefed blocks; a griefed block's exit is GovDAO's vote in every phase; §7.2: the fee is charged all the same. |
| AB4 | low-medium (build) | Precedence of a seat's standing veto, explicit veto and affirmation was unstated. | §6.5: an explicit instruction beats the standing veto for that decision; a seat's veto beats its own affirmation; an affirmation is one per seat per decision, never withdrawn. |
| AB5 | low-medium (build) | A standing veto covered only decisions recorded while it stood; a flood recorded before it was set needed a bot. | §6.5: it also vetoes every decision already pending when set. |
| AB6 | low (build) | A recovery's clock ran "from submission" while its counter advanced at recording. | §6.4: from recording. |
| AB7 | low (build) | Kind 12 lacked the origin domain and bounds; a client upgrade changing the state layout would stop deliveries until the pausable ISM's own upgrade; decision accounts' rent had no return; credit-over-void had to replace, not add. | §13 (origin domain, 1 ≤ threshold ≤ count ≤ 4, the two upgrades proposed together); §6.5 (rent returned to the deliverer on execution, void or spend); §5.3 (replaces the restored remainder). ⟂ v28 (see the preamble). |
| AB8 | low (ops) | The outsiders' standing orders were written nowhere; a kind 5 naming an operator seat and an outsider seat together would let the operator's veto plus a thief's make two. | §11.9 (the standing orders); §13 (r/hlgov's builder refuses such a kind 5). ⟂ v23 (see the preamble). |

Checked and held: Hyperlane's multisig ISM `verify` needs no signer and the mailbox signs `Verify` with its
per-ISM authority only for a matching meta, so the pausable ISM's CPI works; "a week if both sleep, never if one
watches" holds against a silent partner on every channel, recoveries and kind 11 included; a stolen outsider key's
powers are as §1 states; affirmations name a decision and carry a seat, so no forgery or replay; restore and
void-as-forged algebra; the recovery counter at recording; `set guardians` under a lone flag executes and under a
hold is deferred; every round-10 answer is in the text.

### Round 11 — fact check and consistency of v12

Verdict given: close — the architecture, the state machine, the identity and every measured number stand (rents to
the lamport on 10-11); one design sentence and an editing pass. Folded into v14 (with v13's red-team half):

| # | Found | v14 |
|---|---|---|
| (a) B-5, B-10, AA4 | Kinds 11 and 12 were missing from §6.5's list (so r/hlgov had no kind-12 builder); "stranger" survived in §5.9; §1's "prefers that to a brake one sleeping outsider loses" referred to a rule that no longer existed. | §6.5 lists them; "foreign"; the sentence reworded. |
| C106–C112 | Unmarked overturns: C49 (claim order → pro rata); the round-9 "nine weeks" row; X1's exception; F1's and U4's marks; the cost rows; Y4's "one veto each"; Z8's re-flag loop. | Marked. |
| (b)1 | The standing veto's effect on an `unpause` was undefined — either a one-outsider hold (unsaid) or a dead clause. | Defined (AB1): a vetoed `unpause` is spent; one outsider's flag with a standing veto holds for as long as its seat stands, which the plan accepts since a stolen key already froze for as long; §1, §3.3, §9 say so. |
| (b)2, (b)3 | §1's trusted list still had "a comparator a GovDAO member runs"; §1 ¶1, the end state, §4, §9 and §10 still said "on proofs" without the griefed-block qualification. | All qualified. |
| (b)4–(b)6, (b)8–(b)10 | The stolen key's clock (from delivery, the affirmation a condition); both outsider keys stolen by one party is permanent (each vetoes the other's replacement; a recovery's replacement falls under the same rule); a bought seat can also affirm the honest one's removal; a flag is a guardian's own transaction, not a proposal's; §9's captured-key row lacked the outsider case; the glossary lacked seats, affirmations and standing vetoes. | §1, §6.5, §9, the status paragraph. |
| (c) | "≈1 SOL a day" of griefing was 5–10 (fifty 5–10 MB accounts a slot need four to nine transactions under the 64 MiB limit; 54 if one each), cents only for a block producer or someone who sees the burn coming; the 200 GNOT stipend (≈$14) covered neither a droplet nor an RPC account. | §7.2, §9, §1 (5–10 SOL a day; cents for a producer); 600 GNOT a month (§11.12); costs $450–765 and two fifths to three quarters (§10). — ⟂ round-14 (c), round-17 (b)6: $510–880 and 50%–85% (§10). ⟂ v28 (see the preamble). |
| (d) | Credit-over-void must replace, not add; when an affirmation may be given and whether it counts at execution; Phase 0's quarantine clock. | §5.3 (replaces), §6.5 (before or after maturity, counted at execution, never withdrawn), §5.3 (wall time from the announcing block). |
| (e) | Names (§11.9, §11.13, §11.3, §11.12); the capture endings; Phase 2's buy; the assent; economics; what is checkable; the watcher's tooling; the pool; the fork procedure. | Stand as §11's open items; the endings are now all in §1; a written procedure for "the fork" and which burns count is added to §6.4's recovery drill and the launch notice (§6.4). |

### Round 12 — red team on v14

Nothing above medium. Folded into v15:

| # | Sev | Finding | Answer |
|---|---|---|---|
| AC1 | medium | The fork procedure cut at the forged header's gno height — which the forger chooses, since a proof header can sit at any height of the current set's era — and spoke of "claims credited from a header", a category error. | §6.4: the cut is the Solana slot S at which the client verified the first differing header; deliveries at or after S are forged for the identity; claims credited before S stand; burns after S are credited by vote pro rata. |
| AC2 | medium (trust) | The one-outsider hold was in §1's guardians bullet and §6.5 only; §1 ¶1, the end state, §3.3, §3.6, §4, §9 and §11.9 kept the two-guardian rule. | Stated in each. |
| AC3 | medium-low | The pre-vote list check did not bind the list the bootstrap wrote. | §6.5: the admin program's lists are init data; §6.6: the bootstrap names the keys and the admin program refuses a differing hand-over; §8.3: the member decodes the queued Squads transaction. |
| AC4 | medium-low (ops) | The standing orders protected a rogue partner ("affirm only on the partner's word") and missed three timing rules. | §11.9: affirm on the evidence, check the key named, affirm before maturity, set the standing veto before flagging. |
| AC5 | low-medium (trust) | "Both outsider keys honest" is four people (the backups hold the keys); an outsider's replacement also needs the operator's seats not to veto. | §1, §11.9. |
| AC6 | low (build) | The watch would flag every recovery as a foreign decision. | §5.9: foreign = a mailbox-channel decision absent from r/hlgov's list; recoveries are checked against the validators' signatures. |
| AC7 | low (build) | Only r/hlgov refused a kind 5 naming an operator seat and an outsider seat; a recovery body bypasses r/hlgov. | §13: the admin program refuses it on either channel. ⟂ v23 (see the preamble). |
| AC8 | low | §1 ¶1 said no operator machine signs on the gno → Solana path "from then on", a week early; "while two guardians veto". | §1 ¶1 reworded. |

Checked and held: no path for a pair including Jae to remove an awake outsider with an honest partner (direct, by recovery, by kind 9/11, by `set decision recipient`, by rotation and hold, by cancel-and-re-propose, by flood); a rogue outsider cannot stop its own replacement once affirmed; a dead seat with a standing veto plus a lapse is recoverable by a recovery-carried kind 5 with the partner's affirmation; the pair's cheapest path is still `migrate`; round-11's answers are in the text except (b)1's "§3.3, §9", fixed here.

### Round 12 — fact check and consistency of v14

Verdict given: one pass, mostly editing — the sentence a member weighs most was answered "two guardians" in seven places and "one outsider" in two, and two sentences carried wrong substance. Folded into v15:

| # | Found | v15 |
|---|---|---|
| C113–C118, C121–C125 | Unmarked: the round-8 and round-10 cost rows and C64–C70's pointer; three "checked and held" lines and round-7's "cannot stretch forever" (a stolen or rogue outsider keeps the seat while the partner will not affirm); X3 (the affirmation condition); U4 (guardians on providers); A9's pointer; "(below)"; the flag-is-a-transaction fact's place. | Marked, with the "checked and held" lines left as history under this note. |
| (b)1, C119, C120 | The one-outsider hold (AC2) and the standing veto's "pending" scope were in §1/§6.5 only. | §1, §3.3, §3.6, §4, §9, §11.9, the glossary. |
| (b)3 | The fork procedure's mechanism (AC1). | §6.4. |
| (b)4 | §11.13's arithmetic: with 2 of 3 the operator's two machines still sign alone. | §11.13: on gno.land hlmailbox's verifier requires the partner's signature plus one of the operator's; on Solana a threshold only, so the partner buys forgery resistance there, not the exit. |
| (b)5–(b)8 | The admin program's list did not exist before the vote; r/hlgov had builders for recovery-only kinds; the unpause's veto window ("before its 24 hours run" vs counted at execution); "21 days from the lapse" and "paused" under a lapse. | §6.5 (init data; "before it executes"), §5.3 (no builders for 10–11), §6.4 (21 days from the last accepted header; starved of new headers, older ones still deliverable). |
| (b)9–(b)13 | §1 ¶1's "from then on"; the glossary's two definitions of a decision; §9's RPC row; "the hand-over" naming two events; the Phase 0 verifier's activation absent from the Bootstrap list. | §1 ¶1 (once the first decision executes); the glossary; §9; §8's title defines both words; the Bootstrap list names the active verifier. |
| (d) | "channel" and "kind" undefined; §7.1's auto-pause; the drills did not rehearse the affirmation, the standing veto or a vetoed unpause. | The glossary; §7.1 (the verifier refuses past the slot, the watch keys flag, GovDAO's unpause names the chain); §6.6 (the guardians' drill). |
| (e) | Two unnamed people as the whole brake; a good-faith deadlock between outsiders; Jae's concentration; §11.1's date; the review; Phase 2 as a hope; the assent; economics; the pool; the admin program no longer "tiny". | §11's open items; a good-faith deadlock ends as a hold does, with exits (§1); "tiny" dropped from §6.5. |

### Round 13 — red team on v15

Nothing above medium. Folded into v16, which also removed Phase 0 (the preamble):

| # | Sev | Finding | Answer |
|---|---|---|---|
| AD1 | medium | The bootstrap day was the operator's: between a member's pre-vote check and the hand-over, the interim multisig could queue a pausable-ISM guardian swap or an admin-program upgrade (both matured in a day) and execute the hand-over after; the admin program's refusal covered its own list, not the ISM's; §8 had no post-hand-over dump. | Moot in v16: no interim multisig, no hand-over. The setup transaction moves every authority to the admin program and burns its own before the vote is called (§5.8), so the pre-vote dump and audit are of the final deployment (§6.6, §8.2–§8.3, §9's deployer row). |
| AD2 | medium | §11.13's partner signer was a one-person liveness lever over the only exit, and §1 did not carry it; `rotate signers` said nothing about which signer is required. | §1's signers bullet (if adopted, the partner alone can close the exit for 7 days plus a rotation, as the operator can); §11.13 rewritten (it buys forgery resistance, not the exit); §5.3's `rotate signers` names both groups. |
| AD3 | medium | The proof-header record was a program log (≈10 KB a transaction, then truncated): one delivery could drop an entry, the watch would see a gap and flag both programs every poll, and no unpause clears a flag re-set after it — one transfer pauses gno → Solana for a week; the counter did not cover accepted updates, so S's mechanism missed an update-first fork. | §6.2: one counter and running hash in state over accepted updates and proof headers, the record being the staging transactions' instruction data, the log a hint; the watch rebuilds the chain from instruction data and flags a mismatch or an unfillable gap (§5.9); S is the slot of the entry (§6.4). |
| AD4 | low-medium | `Bootstrap` had no caller gate; front-runnable in the block after `addpkg`. | §5.3: callable once, by the deployer's address alone. — ⟂ AF3: `Bootstrap` is gone; GovDAO's `Launch` builder. |
| AD5 | low-medium | §1's operator bullet omitted the prover: after Proposal 2 the operator's node is the only one capturing account images. | §1 (the prover; without it exits are credits by vote until another prover runs); §9's "operator stops proving" row. |
| AD6 | low | Kind 7 set the mailbox's fee but not its beneficiary (`set_protocol_fee_config` replaces both; `claim_protocol_fees` pays the beneficiary), so a raise would pay whatever key the setup wrote, forever. | §13: kind 7 carries the beneficiary; §5.8: the setup transaction sets it to the treasurer's key and the audit prints it; §8.3 checks it. |
| AD7 | low | Both outsider keys lost with standing vetoes up is permanent, not "7 days a seat"; whether an affirmation lets any decision through a standing veto was undefined; T5 unmarked. | §1 (permanent while standing vetoes stand); §6.5 (an affirmation on any decision is that seat's let-through, a veto the reverse); T5 marked ⟂. |

Checked and held: the fork cut cannot be gamed by a forger (its first forged delivery is at or after S), a griefer (no differing header without the current set's keys) or the operator without keys; the counter and running hash cannot be back-dated; round 12's holding re-walked under v15 against every path (direct kind 5, recovery kind 5, kinds 4, 8, 9, 11, `set decision recipient`, cancel-and-re-propose, floods, unpause loops) — each is in the 7-day group or spent by the standing veto; the 24-hour unpause, "flags set before its delivery", the per-poll re-assert, "a lone flag never defers kind 5" and the per-object hold are mutually consistent; §11.13's Solana arithmetic matched `multisig.rs` (moot in v16: no signer on Solana).

### Round 13 — fact check and consistency of v15

Verdict given: one short pass away, three sentences of substance. Folded into v16:

| # | Found | v16 |
|---|---|---|
| C126–C130 | Unmarked: round-10 (c)'s stipend row; C60 (overturned by AC3); T1 and T5's "paused" (overturned by round-12 (b)8); round-8 (d)'s 90-day rule (dropped by Y4); four "checked and held" lines on a stolen outsider key against AA4. | Marked; the preamble states the "checked and held" exception. |
| C131, (b)3 | AC6's answer claimed a fix not in the text: the glossary and §5.9 still defined a foreign decision as any the admin program recorded that r/hlgov never executed, which made every recovery foreign and flagged. | The glossary and §5.9: foreign = a mailbox-channel decision r/hlgov never executed; recoveries are checked against the validators' signatures. |
| C132, C133, (b)8, (b)9 | §9's rows answered "two guardians" only, "paused meanwhile", "auto-pause". | §9 rewritten; A4's answer marked ⟂ round-12 (d). |
| (b)1 | §11.13 inverted: requiring the partner's signature stops a forgery by the operator alone, and either party's stop closes the exit. | §11.13, §1, §9. |
| (b)2 | §1 ¶2's "time to leave" was the operator's to close against a pair that includes it. | §1 ¶2: "unless the pair includes the operator". |
| (b)4 | "Hold" meant two guardians in §3.6, §6.1 and §6.4 and one outsider in the glossary; §1 ¶2 applied the one-outsider hold to Phase 0's signers. | §3.6, §6.1, §6.4: "a hold — two guardians' flags, or one outsider's kept by its standing veto"; the Phase 0 sentence is gone. |
| (b)5 | §5.9's Bootstrap list lacked the active verifier. | Added. |
| (b)6 | The glossary's "fork" and §1's "burns after the fork" kept AC1's category error. | The glossary (cut at the Solana slot S); §1 (after the cut S). |
| (b)7 | The standing veto's scope excluded `cancel` in §6.5 only. | §1, §3.3: "(a cancel excepted)". |
| (b)10 | §1 said "reviewed and audited" while §11.2 left the audit open and §6.6 made it a gate of Proposal 1 only. | §1's code bullet: reviewed and audited before the launch vote or within the first quarter, as §11.2 decides; §11.2 recommends both before launch, since the three programs are the whole trust path from the first block. |
| (b)11 | "Bootstrap" (the gno deployer's call) and "the bootstrap" (the hand-over) were two events under one word. | v16 has only the gno call; the glossary says so. |
| (b)12 | §1's mirror image understated: the bought seat's own veto plus one operator seat already makes two. | §1: a bought outsider plus one operator seat makes that seat permanent. |
| (c) | Every number recomputed stood; the rents re-measured live to the lamport (5,080/byte + 128 B); "two fifths" rounds 43% down; §6.3's ~30k CU and §5.5's BUBBLE fee basis unlabelled. | The SOL arithmetic is redone for v16's five programs (≈7.5 of rent, three guessed; 6 more SOL — §5.8, §10, §11.4); the CU figure stays an estimate until measured. ⟂ v27 (see the preamble). |
| (d) | No value for: the rent of a pending decision or a staged recovery; the reserve in money; whether backups share the stipend; `hlwatch`'s poll cadence; "the interim transfer", "watch slot", "an invariant break" before definition. | §5.8 (≈0.002–0.003 SOL, an estimate; 10 s), §11.12 (≈$1,400–2,300; backups share), the glossary (seat, watch slot), §5.3's pointer to §5.9; "the interim transfer" is gone. — ⟂ round-17 (b)6: the reserve is ≈$1,500–2,600. |
| (e) | The skeptic's list: unnamed outsiders and backups; Jae's concentration (one seat plus a bought outsider); the exit his to close; the pausable ISM on mainnet before an audit; the assent; economics (positive above a 43% GNOT share); Proposal 2 as a hope; the watch flagging recoveries; a stale init list between deploy and bootstrap; §11.1 without an owner or a date. | §11's open items; (b)12, (b)1–(b)2 and C131 fixed; the audit is recommended before launch (§11.2) and the pausable ISM, the client and the admin program reach mainnet together; the stale-list case is moot (no window between the deployment and the vote — the lists are init data, copied by the setup transaction). |
| (f) | §1's guardians bullet rewritten for a non-engineer. | Adopted in §1. |

### Round 14 — red team on v16

One high (a trust sentence), the rest low-medium build rules. Folded into v17:

| # | Sev | Finding | Answer |
|---|---|---|---|
| AE1 | high (trust) | "Time to leave" was false against any hostile pair: `rotate signers` to the empty set and `void` by list both took effect at once, so two members closed the Solana → gno exit and froze every pending claim in ten minutes with no flag, no veto and no operator. | A rule, not words (§5.3, §11.14): a signer removal that would drop the live set below the verifier's threshold waits 7 days (one compromised signer is still cut at once); a void takes effect at once only on a route a guardian or watch key has flagged, 7 days otherwise. §1 ¶1, ¶2, the GovDAO and signers bullets and §9 restate the week accordingly: a pair without the operator's watch keys has it, a pair with them does not. — ⟂ AF6, AF7: `Satisfiable` rather than a threshold; a `payouts` flag only, tested once at execution. — ⟂ AG1: a guardian's flag only, for the ids it names (v20). |
| AE2 | medium-low | The gno half of the trust path was outside the vote: no realm paths or source hashes approved, `Bootstrap` took parameters, and it was sequenced before the vote in §5.9 and after it in §6.6. | §5.3: every launch value is compiled in and written by `init()`; the realms are deployed before the vote and refuse sends and credits until a parameterless `Bootstrap`; §6.6: the vote approves the package paths and each file's hash; §8.3 checks the source, the compiled values, the routers against the realm addresses and the admin program's sender constant; §5.9 reworded. — ⟂ AF3: `Launch`, not a deployer's `Bootstrap`. |
| AE3 | medium-low (trust) | A bug in the immutable admin program had no row and no ending. | §1's code bullet, §9's row, §6.5: the recovery path is a separate, minimal code path; a bug that spares it is cured by a 14-day hold and a transfer to a successor, otherwise a new deployment with the exits open. |
| AE4 | medium-low (build) | `--max-len` pinned to the ELF forbade any upgrade that grows, and no kind could extend. | §5.8, §6.5, §13 kind 8: only the admin program is pinned; kind 8's execution extends the program data by the buffer's excess (the deliverer paying the rent) before it upgrades. — ⟂ AF8: the executor pays the extend; a refund key takes the buffer. |
| AE5 | low-medium (build) | Deployer-set token settings no kind can undo (gas paymaster, destination gas configs, fee config, mailbox pointer, decimals; the mailbox's local domain; the client's state PDA) were outside the audit. | §5.8's deploy sets them to none and the audit prints them; §8.3 checks them. |
| AE6 | low-medium (build) | When a held set settles was undefined, and the text killed the recovery after an honest hold. | §6.1: a set settles at the later of its quarantine's end and the lifting of any hold standing then, re-evaluated at every update. — ⟂ AF9: evaluated at every read. |
| AE7 | low-medium (build) | §13's sign bytes were one block's: `08 01` is the part set's Total, which varies, and each precommit carries its own timestamp. | §13 and §6.1 say so; §5.9's fuzz corpus includes multi-part blocks. |
| AE8 | low (build) | `gno.land/r/hlmailbox` is not a deployable path under r/sys/names. | §13: the real address-namespaced paths, fixed in the constants and printed in the launch notice; the short names are shorthand. |
| AE9 | low (build) | The watch could evaluate the identity at mismatched heights and flag itself after a restart. | §5.9: gno state at or above the client's trusted height, Solana state at or above its last entry; it waits for its sources. |
| AE10 | low (build) | Whether two operator vetoes spend an unpause was unsaid. | §6.5: an unpause is spent on either outsider's veto or any two guardians', as the group's rule. |
| AE11 | low (economics) | A vetoed upgrade's buffer (≈1.5 SOL) was stranded under the admin PDA. | §6.5, §13: closed to the decision's deliverer when the decision is voided or spent. — ⟂ AF8: to the decision's refund key, on cancel too. |
| AE12 | low (ops) | A standing veto plus a monitor's false flag is an outage as long as the absence. | §9's row; §11.9: a standing veto for a few days, the backup's key beyond that. |
| AE13 | low (economics) | A BUBBLE price collapse makes the 200-BUBBLE minimum a 7-day griefing lag with no named brake. | §9's row: a guardian's `sends` flag on the BUBBLE route by judgment, or the relayer parks BUBBLE deliveries, until the raise lands. |
| AE14 | low | "4 of the 5" in §1 ¶1 and §4; "the three lists equal before the vote" when hlmailbox's exists only after `Bootstrap`; "7 days a seat" for two lost keys. | §1, §4 (more than 2/3 of the current power); §6.5 (hlmailbox's compiled defaults); §1 (the two decisions pend together). ⟂ v24 (see the preamble). |

Checked and held: every governance path against an awake outsider with an honest partner (kinds 3, 4, 6, 8, 9, recovery kind 11, `set decision recipient`, cancel-and-re-propose, floods, unpause loops, rotation plus forged decisions during the quarantine); the deployer window after the setup step; only-current-set proofs plus `Reemit`; the staged PDAs; the counter and running hash; the recovery digest; 21/42 days; the claims algebra and the refusal rule; the three flags and the mirror; the 6-SOL arithmetic; one-entry-per-pubkey in the lattice-delta proof; Hyperlane's mailbox and token semantics as §2 states them; the GovDAO gate against master.

### Round 14 — fact check and consistency of v16

Verdict given: not yet a trust statement, close; every number stood (the rents re-measured live). Folded into v17:

| # | Found | v17 |
|---|---|---|
| C134–C138, C143, C150, C152 | Unmarked answers stating an old rule: the six "⟂ C66" marks (4 SOL), E5 (2,500), X7 (program logs), U3 (two guardians only), G11 (burned at the bootstrap), Z9 (both directions), C6/C7 and round-7 (c) (four months, April/May), E3 (the multisig's unpause). | Marked on C66 (which every "⟂ C66" reaches), E5, X7, U3, G11, Z9, C6/C7, E3. |
| C139–C142, C144–C149, C151, C153 | Answers made moot by v16 that the preamble's note did not list; pointers to §6.7 and §11.3; three held lines. | The preamble's v16 note lists them and says how to read the dead pointers. |
| (b)1 | "Five programs" are six deployments (the token program twice). | "Six program deployments" in §2, §4, §5.8, §10, README. |
| (b)2, AE14 | "4 of 5" as the rule in §1 ¶1 and §4. | More than 2/3 of the current power, 4 of 5 today. |
| (b)3, AE2 | `Bootstrap` before the vote in §5.9, after it in §6.6. | Compiled values, deployed before the vote, a parameterless `Bootstrap` after it (§5.3, §5.9, §6.6, §8.3). — ⟂ AF3. |
| (b)4, (e)10 | The guardian-list copy was missing from the setup transaction, and `SetAuthorityChecked` needs the new authority's signature. | §5.8: the init list is copied into the pausable ISM; the authorities move through the admin program; the setup step is one logical step in as many transactions as fit, each printed. — ⟂ AF4: no copy instruction; the pausable ISM's own init. |
| (b)5 | §5.3 said flags are "cleared only by an unpause decision". | "By its own key or by an unpause decision." |
| (b)6 | An affirmation "before or after maturity" (§6.5) vs "before the decision matures" (§11.9). | §6.5: any time before execution, counted at execution; since anyone may execute at maturity, an outsider affirms before it; §11.9 matches. |
| (b)7 | "In front of Hyperlane's" (glossary, §3.3). | In front of the light-client ISM. |
| (b)8 | §1's outsider clock ran from the affirmation. | From the replacement's delivery, once affirmed. |
| (b)9 | §6.5's kind list lacked kind 7's beneficiary. | Added. |
| (b)10 | §7.3's comparator made a guardian's key an automatic pauser. | The operator's monitors run the comparator and flag with the watch keys; the outsider's copy alarms its holder, who flags by hand. |
| (b)11 | 0.04 vs 0.05 of state accounts. | 0.05 (0.04 of state plus the program accounts), §2, §5.8, §10. |
| (b)12, (b)13 | README's tool count and "owned by the admin program". | README. |
| (b)14 | §1 ¶1 omitted the explicit veto on an unpause. | Added. |
| (b)15 | Where the floats live. | §5.8, §9: the deployer funds the floats' own keys and keeps nothing else. |
| (b)16 | "No human key anywhere" vs the treasurer's beneficiary key. | "No human key holds an authority." |
| (c) | Every number stood; the updater's fees were outside the cost band; unlabelled estimates (30k CU, the deposits, `Claim`'s gas, "≈100 lines"); the audit-fix loop unbudgeted. | §10: $460–775 with the updater's fees, an upper bound; labels in §5.5, §6.3; "small" for the pausable ISM; the restart of the two-week run noted. — ⟂ round-17 (b)6: $510–880, 50%–85%. ⟂ v28 (see the preamble). |
| (d) | No value for: the treasurer's Solana key; the client's initial header; the relayer's backoff and the signer client's body limit; kind 4's layout version; the comparator's threshold. Terms before definition: the supply identity, the meter, LtHash, Alpenglow, r/gnops/valopers. | §11.12 (the treasurer's Solana key); §6.1 (a header under an hour old at deploy); §5.7 (exponential, a minute to an hour; 1 MB); §13 (a version byte); §7.3 (any difference); the glossary. |
| (e) | The skeptic's list: the two members own the lock; unnamed outsiders and a tool their counterparty writes; the three programs unbuilt and the audit a recommendation; Jae's concentration; the way home is the operator's until Proposal 2; decisions skip the pausable ISM; thin economics; the unsigned assent; the deployment final before the vote; the setup mechanics; "two independent sources" both the operator's; the unsized pool; the leak; the one-outsider hold voiding its own recovery; "≈100 lines". | §11's open items; (e)6 is now in §1's validators bullet; (e)10 in §5.8; (e)11 in §5.9 (rpc.gno.land, never a signer's node); (e)14 in §6.4; the rest stand as stated. |
| (f) | §1's validators bullet rewritten for a non-engineer. | Adopted. |

### Round 15 — red team on v17

Two highs (one a trust sentence, one a check), two mediums. Folded into v18:

| # | Sev | Finding | Answer |
|---|---|---|---|
| AF1 | high (trust) | An `unpause payouts` with nothing to clear still emptied the meters, so a pair without the operator could drain the exit to 1/(N+1) of the cap by executing unpauses every block. | §5.3, §5.4: an unpause resets a meter only when it clears a standing `payouts` flag, and the builder refuses one that would clear nothing; §1 ¶1, ¶2, §9. |
| AF2 | high (check) | "Realms are immutable" rested on gnomod's `private` flag, which nobody was told to check: a private package is re-uploadable by its namespace owner, `init()` runs again and the package address keeps the lock. | §5.3: none of the five realms nor any verifier realm sets `private`; the keeper rewrites gnomod.toml at `addpkg`, so the vote's hashes are of the served files; §8.3 reads each served gnomod.toml; §9's deployer row. |
| AF3 | medium (trust) | `Bootstrap` was the deployer's call, not the vote's: the realms could go live without, or against, the vote. | `Bootstrap` is gone; the launch vote is the proposal made with hlmailbox's one-shot `Launch` builder, whose execution makes the realms live; the deployer holds no call on gno.land (§5.3, §4, §6.6, §8.3, the glossary). |
| AF4 | medium (build) | The copy of the init list into the pausable ISM had only a tool-level once-guard; a replay would restore a replaced thief's key and drop the honest flags. | §5.8, §6.5: the pausable ISM's list is written by its own init from the same list; no copy instruction exists; every setup-only instruction is one-shot on chain behind a `setup_done` byte the audit prints. |
| AF5 | medium-low (trust) | A restore of a claim voided as forged was at once and re-broke the identity, handing a pair the watch's automatic flags. | §5.3, §5.4: restoring a forged void joins the 7-day group; a withheld void's restore stays at once. |
| AF6 | medium-low | Under §11.13's partner rule, removing the partner kept the set "at the threshold" and closed the exit at once. | §5.1, §5.3: the verifier's `Satisfiable` is asked by the builder; a removal that leaves the rule unsatisfiable waits 7 days. |
| AF7 | low-medium | When the void's flag test runs and which flag counts were open; a `credits` flag set by an outsider unlocked the pair's at-once void. | §5.3: only a `payouts` flag, tested once in the executing transaction, never accelerated later; §11.9's standing orders; §5.9's run. — ⟂ AG1. — ⟂ AH1. |
| AF8 | low-medium | Kind 8's buffer went to "the deliverer", the extend's payer could not be the deliverer, and a cancelled upgrade stranded its buffer. | §6.5, §13: a refund key in the decision (the loader's spill; the buffer's recipient on void, spend or cancel); the executor pays the extend; the hash is over the ELF bytes after the loader's header. — ⟂ AG12, round-17 (b)8: `stage upgrade` records the refund key; the decision carries none. |
| AF9 | low-medium | Settlement "at every update" let a stopped updater leave an honest set unsettled when a lapse opened recovery. | §6.1: a pure function of the quarantine's end, the holds and the Clock, evaluated at every read, the recovery-recording instruction included. |
| AF10 | low-medium | The mints' mint and freeze authorities were unaudited; the monitors' Solana sources were unnamed. | §5.8, §8.3 (mint authority = the mint PDA, freeze authority none); §5.6 (two Solana providers per monitor, no pair shared with a signer). |
| AF11 | low (trust) | The week's price and the one-of-three-pairs fact were unsaid as numbers. | §1 ¶2, §11.14: about half the lock (1 − 0.9^7 ≈ 52%) if nobody flags for the week; the week binds one pair of three. |
| AF12 | low (build) | `hlsol setup` "refuses to run twice" fought a halfway stop; the step order was not binding. | §5.8: resumable from chain state, a binding order (settings, owners, upgrade authorities, the admin program's burn last), "no human key holds an authority" the done condition. |
| AF13 | low (trust) | "A separate, minimal code path" promised more than two paths can give. | §6.5, §1: recording, clocks and veto count are bypassed; a kind's execution is shared, and kind 9 is its cure. |
| AF14 | low (ops) | Two Solana objects to hold; a kind 5 naming a seat's current key cleared its flags. | §5.8: `hlwatch` flags both in one stroke; §6.5, §13: such a kind 5 is refused. |

Checked and held: every other at-once power of a pair without the operator (lowering — now never below the minimum — removing guardians, watch keys, one signer, cancel; everything else 7 days); rent paid by the deliverer is buildable (the mailbox forwards the extra accounts' signer flags); the token and mailbox instruction sets match the kinds; BUBBLE's admin can only dilute; the light client, the quarantine on a round-trip, only-current-set proofs, the staged PDAs, the counter and running hash, the recovery digest; the affirmation rule and the standing veto re-attacked; claims and meters; kind 8's extend; a setup stopped halfway is caught by the audit; Proposal 2, the roster, the economics.

### Round 15 — fact check and consistency of v17

Verdict given: one design hole (the at-once void openable by a watch key's source-disagreement flag) and an editing pass. Facts re-verified live: the rents to the lamport, five validators at 60, 3.43 s a block, the mailbox's order and fee instructions, GovDAO's T1 power and 66.66%. Folded into v18:

| # | Found | v18 |
|---|---|---|
| C154–C158, C161 | T3, R4, G3, F14 and round-11 (c) stated old rules unmarked; "Phase 1/2" had no reading rule. | Marked; the preamble reads Phase 1 as the launch and Phase 2 as Proposal 2. |
| (b)1 | §5.4 still said voids are at once. | §5.4 carries §5.3's rule, restores included. |
| (b)2, (e)2 | Which flag opened the at-once void was loose, and the watch keys' source-disagreement flag — openable by whoever serves rpc.gno.land — would have opened it. | A `payouts` flag only; the disagreement rule sets `credits` and the pausable ISM, never `payouts`; the identity is evaluated from the monitor's own node and its two Solana providers; rpc.gno.land named as the gno core team's. — ⟂ AG1. — ⟂ AH1. |
| (b)3 | Two guardians' vetoes spending an unpause was in §6.5 only. | §1 ¶1, §3.3, §9. |
| (b)4 | §3.5's "deadline". | The proposal's, on gno.land; none on Solana. |
| (b)5 | 7-, 14- and 28-day clocks "drilled" in a 72-hour run. | §5.9: a devnet build with the clocks divided by a compiled factor the audit and the dump show; §8.4 checks the mainnet factor is 1. |
| (b)6, (b)7, (b)8, (b)9 | "The Bootstrap value"; a voided recovery's rent; "before the vote" for a buffer; §6.5's kind list without §13's kind-5 refusals. | §5.5, §6.4, §6.5. |
| (b)10, (b)11, (e)11, (e)12 | §2 said nothing of Hyperlane's pauses; the GovDAO gate and error semantics differ on master. | §2: the composite ISM and why the plan's flag model is preferred; master's `SimpleExecutor` and proxy semantics, the closures' own gate, the impl named in the launch notice. |
| (b)14 | The light-client ISM's owner was unsaid. | §5.8: none; its upgrade authority is its only control. — ⟂ round-16 (b)4: two controls, both the admin PDA's. |
| (c) | Every number stood; the gas key's 8–10 days needs the deposit; "two fifths" rounds 44% down; "cents a block" overstates by 10×; the updater had no key or float; the relayers' hosts were uncounted. | §5.5 (gas plus deposit; a fifth float, the updater's 0.3 SOL, from the margin; the relayers and the updater on the monitor machines), §10 (45%–75%), §7.2/§1/§9 (under a cent). — ⟂ round-17 (b)6: $510–880, 50%–85%. ⟂ v28 (see the preamble). |
| (d) | The verifier's threshold in `rotate signers`; the realms' clock; the mailbox's local domain; BUBBLE's mint figures; Prop #10's status; the validator-count bound; terms (sysvar, amino, bech32, ICS23, RFC 6962, the zkVMs, GnoSwap, the cold key, devnet, precompile). | §5.3 (the set and its rule together; block time), §13 (Solana's domain ids), §1 (realm/bubble's render; GovDAO proposal #10, pending), §6.1 (255), the glossary. |
| (e)1, (e)3 | The halting threshold (more than 1/3 — one member's two validators today) and a replaced set's control of what enters blocks were unsaid. | §1's validators bullet. |
| (e) | The rest of the skeptic's list — the pair owns the lock, the unnamed outsiders, the unbuilt programs, Jae's concentration, the way home until Proposal 2, the drills, the economics, the unsigned assent. | §11's open items; stated as the plan states them. |
| (f) | §1's signers bullet rewritten with numerals. | Adopted. |
| C159, C160, (b)12, (b)13 | Counts and wording in §12's history ("five programs", "the setup transaction"); the glossary and §4's table found consistent. | History left as written; the preamble's reading rules cover both words; nothing to change. |

### Round 16 — red team on v18

Two highs (one trust rule, one wrong build fact), the rest low-medium. Folded into v19:

| # | Sev | Finding | Answer |
|---|---|---|---|
| AG1 | high (trust) | Any `payouts` flag unlocked the at-once void, a watch key's included: a pair could pre-vote a void and fire it in the flicker of a machine's flag (a provider lag, an inflated supply reading, a monitor bug). | §5.3, §1, §5.4, §11.9, §11.14: only a *guardian's* `payouts` flag unlocks the at-once void; a watch key's flag pauses payouts and never accelerates a void. — ⟂ AH1: for the ids the flag names (v20). |
| AG2 | high (build) | "The mailbox passes exactly the accounts the relayer's simulation returned" was false: `inbox_process` passes whatever the relayer lists, so a pausable ISM that trusted the list was bypassable by any deliverer. | §5.8: the pausable ISM derives its flag account from its own seeds, requires the client's state account to be the fixed-seed PDA of the client program it stores, refuses anything else; the client derives its own accounts likewise. |
| AG3 | medium-low (build) | What set `setup_done` and who may `init` were unsaid; a published deployer key invites a front-run init. | §5.8: a permissionless, self-checking `finish_setup`; our inits require the program's current upgrade authority's signature; Hyperlane's two inits in the first slot after each deploy, the audit's owner check the residual. — ⟂ round-17 (b)9: three inits. |
| AG4 | medium-low (trust) | One guardian's `payouts` flag is the pair's accelerator, the outsiders' included. | Said in §1 ¶2, the signers bullet and §11.9. |
| AG5 | medium-low (build) | The identity's consistent cut was one-sided: a provider lag produced a false `payouts` flag. | §5.9: the Solana read at or above the dispatch slot of every burn credited in the gno state read; until then, alarm only. |
| AG6 | low-medium (ops) | rpc.gno.land's operator held a pause switch (`credits` and the deliveries) by serving one conflicting hash. | §5.9: a disagreement flags only once a third source shows the monitor's own node to be the odd one out; otherwise an alarm. |
| AG7 | low-medium (build) | Settlement's inputs were unsaid (which object's hold, when a hold began). | §6.1: a hold on either object counts; a hold's start is the later set-slot of its flags; settled iff the Clock is past the quarantine's end and no hold that began by then stands. ⟂ v24 (see the preamble). |
| AG8 | low-medium (design) | An at-once cut of one of three signers leaves no fault margin, and GovDAO's inaction has no clock. | §11.14 names the trade (keep the at-once cut; §11.13's partner as a fourth signer restores the margin). |
| AG9 | low (build) | `Launch`'s edges: late, twice, and the other builders before it. | §5.3: no 14-day deadline on `Launch`; a second errors; before it every builder but `cancel` is refused. ⟂ v28 (see the preamble). |
| AG10 | low (fact) | r/sys/names allows only an address's own namespace; imported packages are public and immutable. | §13, §5.3, §8.3. |
| AG11 | low (economics) | Storage deposits refund to whoever frees the storage, from a separate deposit address. | §5.5, §5.3: `Claim(n)` pays its caller the freed deposits; `migrate` does not count the deposit address as lock. |
| AG12 | low (economics) | Kind 8's refund key was a GovDAO parameter while the buffer was already the PDA's. | §6.5: the buffer is handed over once the decision is recorded; the refund key is the writer's. |
| AG13 | low (build) | GovDAO's corrective credit tripped the diverted-credit rule. | §5.9: the realm marks vote-credited claims; the watch compares verifier-credited ones only. |
| AG14 | low (ops) | The "unpause within seconds" standing order was a reflex, not a check. | §11.9: the tell is a dispatched, undelivered unpause in `hlwatch`. |
| AG15 | low (build) | The identity watch stalled when the client was past the chain's head. | §5.9: or the chain's head, which is itself the mismatch flag. |

Checked and held: every at-once power against the exit once more (none pushes the identity up at once); the rotate-and-forge path to a `payouts` flag is closed (the mismatch, gap, foreign-decision and disagreement rules never touch gno `payouts`, now said); decisions skipping the pausable ISM; the halting threshold today and under Prop #10; the provider pairs; every rent, float, runway and monthly figure; GovDAO on master; Hyperlane's order, authorities and bindings; the standing veto and affirmation rule under every thief case; extend-then-upgrade; the recovery digest, counter and window; the claims algebra; Proposal 2's bindings and griefing statement.

### Round 16 — fact check and consistency of v18

Verdict given: one short pass of substance, then editing; every number stood, the five validators resolved live, Prop #10 open. Folded into v19:

| # | Found | v19 |
|---|---|---|
| C162–C175 | Unmarked answers overtaken by rounds 14–15 (AD4, AE2, round-14 (b)3 and (b)4, AE4, AE11, AE6, AE1's wording, T3/R4's pointers, C33, V4, round-9 (b)8–(b)16, C55/C56, C64–C70, round-3's prose, G10, T4, W3); the preamble's round list; four round-15 items unaccounted for; a duplicate clause in §6.5. | Marked; the preamble reads a deployer's `Bootstrap` as `Launch`, "copied into the pausable ISM" as its own init, and names rounds 7, 8, 9 and 10; the round-15 table carries C159/C160/(b)12/(b)13; §6.5's duplicate removed. |
| (b)1, (b)2, (b)3 | §11.14 stated the pre-round-15 rule; "threshold" where the rule is `Satisfiable`; §1 ¶1 against itself on restores. | §11.14, §1 ¶1, §5.9. |
| (b)4 | "Its upgrade authority is its only control" was false against kind 11's reset. | §5.8: two controls, both the admin PDA's; the audit prints the reset authority. |
| (b)5, (e)8 | All five Solana programs are upgradable by a 7-day decision, the token code included; §1 and §3.6 said only the client and the pausable ISM. | §3.6, §1's code bullet. |
| (b)6 | The leak ending's three proposals had no order. | §1: the void first, under a guardian's flag; §5.9 drills the order. ⟂ v24 (see the preamble). |
| (b)7 | A monitor's false flag was described as lasting the whole absence. | §9, §11.9: it ends when the reading returns to normal or the operator fixes it; a sticky one lasts the absence. |
| (b)9, (b)10, (b)11, (b)12, (b)13, (b)15, (b)16, (b)17 | The glossary's "decision" left out the gno side; §4 omitted the recovery channel and the partner rule; "one bad provider halts up to two signers" needed "no provider in more than two"; README's owners and the signers' roster role; the updater's runway (≈1 month at stress); "flags live" in three places; "at launch" for a compiled fee; §2's date. | The glossary, §4, §5.6, README, §5.5, §10, §2. |
| (b)14 | A forged `cancel` runs at once and a forged `unpause` in 24 hours; §1 said only the 7-day veto. | §1's validators bullet. |
| (c) | Every number stood; the week's price is up to 57% from a full meter; the halting count changes under Prop #10; §7.2's griefing count and per-transaction lamports; the margin against a 450 KB client; the draft gas table; the prices behind §10's dollars; two weeks of slack in the review-only date. | §1 ¶2, §11.14 (52%, up to 57%); §1 (25% under Prop #10); §7.2; §5.8; §2; §10. |
| (d) | The devnet clock factor; `Launch`'s deadline; abandoned proof PDAs; a proof's CU; Proposal 2's minimum; the three functions, `Type`, `VerifyAccountMetas`, `SetBufferAuthority`, the two hash fields before definition. | §5.9 (16), §5.3 (no deadline), §6.3, §7.2 (500 GNOT), the glossary. ⟂ v28 (see the preamble). |
| (e)12, (e)13 | The watch keys share machines with the floats' keys; nobody was named to post S. | §5.6, §6.4. |
| (f) | §1 ¶2 rewritten in numerals and bullets. | Adopted. |

### Round 17 — red team on v19

One high, one medium, the rest at or below medium-low; "nothing above medium remains once AH1 and AH2 are folded; nothing critical was found." Folded into v20:

| # | Sev | Finding | Answer |
|---|---|---|---|
| AH1 | high (trust) | A guardian's `payouts` flag was a route-wide, pre-armable trigger: a pair could pass a void of every open claim, wait for an honest guardian's flag (an outsider's `hlwatch` on a third party's RPC account), and execute it in the same block. | §5.3, §5.1, §1, §5.4, §9, §11.9, §11.14: a guardian's `payouts` flag carries the ids it suspects, and the at-once void reaches only ids a standing guardian flag names (a flag with no list pauses and unlocks nothing); `hlwatch`'s `payouts` flag command takes its identity from a node the holder runs and two agreeing Solana providers, else refuses (§5.8, §11.12); builders write their parameters into the proposal's description and `hlwatch` lists every builder-made proposal with the votes and no execution (§5.3). |
| AH2 | medium (build) | When the at-once tests run was unsaid for everything but the void; tested at creation, two removals made in one block emptied the signers at once. | §5.3, §5.1: every at-once test (`Satisfiable`, the void's flag and ids, raise-versus-lower, an unpause's effect) is decided in the executing transaction against the live state; a change that fails there is queued 7 days, not refused. |
| AH3 | medium-low (build) | Kind 4's "successor client" at a new program id could not be adopted by the admin program (ISM fixed at deploy; the accept-authority instruction dead with `setup_done`). | §13: kind 4's target must equal the launch client's program id — a new client is a kind-8 upgrade in place; one state account for the quarantine, the lapse and settlement; §5.3: a mis-encoding r/hlgov is cured by kind 9 or recovery. ⟂ v28 (see the preamble). |
| AH4 | low-medium (build) | The admin program's `Handle` was bound to nothing but the body's origin and sender; the glossary named the wrong PDA. | §6.5: `Handle` must be signed by our mailbox's process authority for the admin program (per-recipient seeds under our mailbox's id); §13, §2: per recipient for `Handle`, per ISM for `Verify`. |
| AH5 | low-medium (check) | `max_protocol_fee` bounds kind 7, is fixed at init, and was not audited; a front-run init could set it to 1,000 SOL. | §5.8, §8.3: 0.05 SOL in the audit; `finish_setup` reads it. |
| AH6 | low-medium (build) | The identity's cut was ordered on one side; the mismatch rule's source was unnamed; an odd-one-out node still fed the identity. | §5.9: the Solana snapshot first, then gno at a height ≥ T; the mismatch rule reads the own node; while the own node is the odd one out, alarms only. |
| AH7 | low-medium (trust) | The partner of §11.13 was unconstrained: a member's company as partner makes that pair's stop at once. | §11.13, §1: the partner is held to the outsiders' rule. |
| AH8 | low | A removal that clears a `payouts` flag reset no meter. | §5.3: it resets it as an unpause would. |
| AH9 | low (ops) | A hold set after the quarantine's end leaves the new set settled; the updater's tick was the rotated-out set's window. | §11.9 (hold before a distrusted change's quarantine ends); §6.1 (the updater submits the announcing header on the valset event). |
| AH10 | low (build) | "Refused" before `Launch` had no point; r/hlgov's builders were not covered; the maximum could be lowered to zero through the floors. | §5.3: the builder panics at creation and the closure re-checks; r/hlgov's builders too; the maximum's floor is the compiled launch minimum. |
| AH11 | low (wording) | "A decision exists only when a proposal passes"; the glossary's `Type`/`VerifyAccountMetas` as the mailbox's questions; §2's per-ISM process authority; "the remaining live set". | §5.3 (executes), the glossary (the relayer's questions, by simulation), §2 (per recipient; per ISM in the master tree), §5.3 (the remaining set). |

Checked and held: every at-once power against the exit and every automatic flag, once more; the void algebra; the signer rule with the partner; the pausable ISM after AG2 against the mailbox's account passing and record order; Hyperlane's bindings (payer-as-owner inits, the per-recipient process authority on `handle`, the mint PDA as mint authority, owner-only settings); `finish_setup`'s logic and order; `Launch`'s edges; the light client's rules; decisions skipping the pausable ISM; the recovery digest and window; the standing veto and affirmation under every thief case; BUBBLE's admin can only dilute; gno master's proxy, validators and keeper facts; every number; round 16's folds all present.

### Round 17 — fact check and consistency of v19

Verdict given: "nothing of design substance remains open in the text"; one short pass of editing. Every number stood (the rents live, the storage price and refund rule on master, GovDAO's 66.66% and T1 power, r/sys/validators' executor, Hyperlane's `inbox_process`, the branch's pre-plan code). Folded into v20:

| # | Found | v20 |
|---|---|---|
| C176–C183 | Unmarked answers overtaken by AG1 (AE1, AF7, round-15 (b)2/(e)2, T3's chain), AG12 (G6), round-16 (b)4 (round-15 (b)14), round-16 (b)6 (Z6, round-7 (b)1), v16 (round-12 (b)4), round-5 (a) (S9), round-12 (b)5–(b)8 (AB1); README's owners and roster role not landed. | Marked; the preamble's list carries round-12 (b)4; README fixed (four owners; the roster role to Proposal 3); the status line's words. |
| (b)1 | Kind 4 carried a state-account parameter the pausable ISM also derived. | §13: the account is derived from the target by the fixed seed `state`; kind 4 carries the layout version instead. ⟂ v28 (see the preamble). |
| (b)2 | §9's forged-claim row promised an at-once void under the watch's own flag. | §9: at once for the ids a guardian names from the watch's alarm. |
| (b)3 | §1 ¶1's "only"/"every" omitted the validators' channel and `Launch`'s missing deadline. | §1 ¶1 rewritten (the round's (f)); the validators' channel in the first paragraph. |
| (b)4 | §1's signers bullet gave the bridge's rate for one claim. | 1/40 a day for one claim, 1/10 across 4 or more recipients. |
| (b)5 | §8.5 and §5.9 presupposed a review or audit §11.2 does not require. | "If either was chosen"; on §12's rounds alone, the proposal says so. |
| (b)6 | §5.6's six to eight provider accounts vs §10's four. | §10: six to eight, ≈$150–200; the band $510–880, the share 50%–85%, the reserve ≈$1,500–2,600. ⟂ v28 (see the preamble). |
| (b)8 | Kind 8's refund key was a parameter stated as a rule. | §6.5, §13: the buffer is staged through the admin program's `stage upgrade` instruction, which records the signer as the refund key; the decision carries none. |
| (b)9, (b)10, (b)11, (b)12, (b)13 | "Two inits" for three deployments; a removed key's flags on gno.land; §6.4's "recovery-only" list; the glossary's "decision"; "within a minute". | §5.8 (three), §5.3 (flags clear with the key), §6.4, the glossary, §1. |
| (c) | `Claim(n)`'s refund overstated (the done mark stays); the gas figures are upper bounds; the 52%/57% conditions (a lock above 100,000 GNOT, 4 or more recipients, a single holder at 1/40); the ATA runway's assumption; the state-account estimate; the status date. | §5.5, §10, §1 ¶2, §11.14, §5.8, the status line. |
| (d) | The fixed seeds; kind 4's version at launch (1); `Reemit` and `Execute(id)` gas; terms (realm, GovDAO, `addpkg`, the keeper, gnomod.toml, `vm/qfile`, programdata, precommit, sign bytes, LastResultsHash, ABCIResult, lazy execution, wincode, hashv, Rotor). | §13, §5.5, the glossary. |
| (e)7 | A pre-passed void of honest claims fired on an honest guardian's flag. | The ids-named rule (AH1). |
| (e) | The rest of the skeptic's list — the pair owns the lock, Jae's concentration, the unnamed brake, the unbuilt programs, the way home until Proposal 2, the unsigned assent, the thin economics, the unbuilt tools, the unsized pool. | §11's open items, stated as the plan states them. |
| (f) | §1 ¶1 from "After launch…" rewritten as slow/instant lists in numerals. | Adopted. |

### Round 18 — red team on v20

Nothing above medium; nothing critical or high; "round 17's claim holds". Folded into v21, the last version of this loop:

| # | Sev | Finding | Answer |
|---|---|---|---|
| AI1 | medium-low (build/ops) | A void under a flag and an unpause of `payouts` voted together could be executed in the wrong order by anyone, turning the at-once void into a 7-day one with payouts open. | §5.3: the void builder's `andUnpause` option voids the named ids and clears the function's flags in one closure; §1's leak ending uses it; §11.9: never vote a separate unpause while a flagged void is unexecuted. |
| AI2 | low-medium (build) | What the alarm "shows forged" was unstated; an indexing gap at a provider would name honest ids, and a void as forged would freeze them 7 days. | §5.8: `hlwatch` calls an id forged only when both providers' scans find no dispatch record and the identity is short by that claim's amount, else "unindexed, re-read"; §11.9: void as withheld first, as forged once a third source confirms. |
| AI3 | low-medium (trust) | §1 described only the fail-closed admin-program bug; a fail-open one is an unauthorized token upgrade and a mint. | §1's code bullet and §9's row say so, and why the audit comes first for that program. |
| AI4 | low (trust) | The cap floor could be lowered at once, revoking the small-lock exit §1 ¶2 promises. | §5.3: the cap floor never below the compiled launch floor. |
| AI5 | low (build) | Kind 3 accepted any ISM, the raw light client included, which would take one token out from under the flags and the quarantine. | §13, §6.5: the pausable ISM's id or none; the default ISM is fixed for the life of the deployment. |
| AI6 | low (build) | Kind 9 did not require the successor to be immutable. | §13, §6.5, §8.4: refused unless the successor's programdata shows no upgrade authority; drilled. |
| AI7 | low (ops/wording) | A builder's description carried the member's free text first; the outsiders' own nodes and providers are claims too. | §5.3: builders take no free text; `hlwatch` and the page show the parameters, never the title; §8.7. |
| AI8 | low (economics) | The stipend did not cover the source rule's node plus two providers. | §11.12: the operator pays each outsider's two provider accounts in kind; §10's band $590–960, share 55%–95%; the reserve ≈$1,800–2,900. |

Checked and held: round 17's folds all in the text (AH1–AH11, (b)1–(b)13, (c), (d), (e)7, (f)); the ids-named flag against a pair without the operator (no at-once action pushes the identity up; a diverted credit cannot be faked to an outsider's own node; claim ids are message hashes; a pre-armed void is never accelerated); the at-once tests at execution; Hyperlane's bindings; GovDAO on master (tallied at execution over the whole membership); the light client, quarantine, settlement and recovery; the standing veto and affirmation under every thief case; kind 8's staging; the numbers; the branch's pre-plan code.

### Round 18 — fact check and consistency of v20

Verdict given: "the document is consistent end to end on every rule a member weighs; every number stands; nothing of design substance remains". Folded into v21:

| # | Found | v21 |
|---|---|---|
| C184–C188 | Unmarked answers overtaken by rounds 16–17 (round 9's implementer line and AA7 on kind 4's state account; AF8 on the refund key; the cost chain's end on $460–775; AG3's two inits; AG1 and three marks citing it on the route-wide flag). | Marked. |
| (b)1 | An unpause that fails its at-once test was "queued", against "at once", "refused" and Solana's "spent". | §5.3: spent, never queued. |
| (b)2–(b)10 | §9's "provider pairs are in §5.6"; §5.4's meter reset without the removal case; one claim's floor (2,500); the glossary's standing veto without the cancel exception; §1 ¶1's slow list missing two items; the comparator outside §11.2's list; §2's literal `valset:proposed` and the unnamed event; §6.6's missing third outcome; the `Satisfiable` gloss. | §9, §5.4, §1, the glossary, §11.2, §2 (`NewPropRequest`; `ValidatorAdded`/`ValidatorRemoved`), §6.1, §6.6, §5.3. |
| (c) | Every number stood; the updater's per-header fee and the stipend's sufficiency unlabelled. | §5.5, §10, §11.12. |
| (d) | The gno deployer's address (the launch notice); the event names; "the setup step", "the floor", "the loader's spill", `--max-len`, "the deployer" before definition. | §13, §2, §6.1, §1 ¶2, the glossary. |
| (e) | The skeptic's list — the pair owns the lock, Jae's concentration, the unnamed brake, the unbuilt programs, the way home until Proposal 2, the unsigned assent, the thin economics, the unbuilt tools, the drilled binary, the liquidity bound, the outsiders' node on a stipend, Prop #10. | §11's open items, stated as the plan states them. |
| (f) | §1's code bullet rewritten in numerals, with the four bug endings. | Adopted. |


Round 19 (2026-10-12, on v22 — the no-veto fold; both reports folded into v23). Red team (the AJ series) and fact
check (C189–C197 and (a)–(g)). The round's finding: v22's rule that a replacement of guardians lands through any
hold at 7 days left the hold worth 7 days against anyone able to deliver a decision, so the validators' recovery
(14 days of hold) was unreachable against a forger, a client bug or a fail-open admin program, and §1's leak,
forged-header and bug endings were still v21's.

| ID | Finding | Answer (v23) |
|---|---|---|
| AJ1 (critical) | Anyone who can deliver one foreign decision delivers two kind 5s; they land through the hold at day 7 and clear it; the hold-opened recovery never opens; the leak, forged-header and fail-open endings in §1 and §9 are v21's. | Rule: under a hold a kind 5 lands on the later of its own 7th day and the hold's 15th day — the day after the window opens (§6.5, §13); in the window the settled validators cancel it (§6.4). Endings rewritten: a leak is a stand-off (the same keys on both sides cancel each other; a freeze the validators keep up; the exits; a new deployment); a fail-open bug is cancelled and handed to a successor from day 14 if the recovery path is sound (§1, §9). §11.15 records the override for Jae. ⟂ v27 (see the preamble). |
| AJ2 (high) | A pair that bundles the replacement with its decision pays no extra day; "a week or more", "7 more", "worth exactly that week" were false. | With AJ1's rule the hold is 15 days from its start, up to 22 from a decision's delivery, against any pair (round 20), then the validators' judgment where the pair's own validators are a minority; said in §1 ¶1, the guardians bullet, §9, §11.9. "One kind 5 pending at a time" not taken (two may pend; the window, not the count, is the brake). ⟂ v27 (see the preamble). |
| AJ3 (high) | After a foreign decision GovDAO's next number is taken and its decision silently lost; against an active forger the mailbox channel is dead; nothing said so. | r/hlgov's `sync sequence` (at once; named `set sequence` in v23) re-syncs its counter; `cancel` takes a list; `hlwatch` and `hlsol audit` print both counters and the gap; §6.5 says a taken number loses the proposal; §1 and §5.3 say that against a forger who races numbers the brake is the hold and the recovery, GovDAO's cancel helping only if it lands (§5.3, §5.7, §5.8, §6.5). |
| AJ4 (high) | The recovery cancel could not name the decision the hold was set for (recorded before the hold began) and cannot know "foreign". | While a window stands a recovery cancel may name any pending mailbox decision, whenever recorded and whoever proposed it; "foreign" is the validators' procedure (§6.4). |
| AJ5 (medium) | "Never past GovDAO's will" hid the 14-day override and a day-6 race. | With AJ1's rule there is no race: the window always opens before a replacement lands; the override is stated in §1, §3.3, §6.5, §9 and decided in §11.15 (keep). |
| AJ6 (medium) | Kind 1 had no channel byte. | Kind 1 = count ‖ (channel ‖ number)…; a mailbox cancel names channel 0 only; a recovery cancel names 1 at any time and 0 in a window (§13, §6.5). ⟂ v25 (see the preamble). |
| AJ7 (low-medium) | §1's validator arithmetic stale. | Folded from the chain before the report (60e0cc5): six validators, Jae's own at power 1 since 720,333, Props #10 and #13 open; the report's "Prop #10 passed" came from RESUME.md's wrong note, since corrected. ⟂ v25 (see the preamble). |
| AJ8 (low-medium) | Residues: "(against a lone flag)", "ends only by executing…", "wait the same". | All three reworded (§9, §6.5, §6.4). |
| AJ9 (low) | The hold's start with three or four flags; the standing order's "before the quarantine ends". | Start = the second-earliest set slot among standing guardian flags, recomputed on a clear; a flag re-set within 24 hours keeps its slot, so a holder's own re-setting never restarts the 14-day clock; both holders flag before the end (§6.1, §11.9). ⟂ v25 (see the preamble). |
| AJ10 (low) | The dead-GovDAO bullet was gno.land-only. | On Solana a 14-day hold lets the settled validators carry decisions for it (§1, §9). |
| AJ11 (low) | §5.9's drills predate the fold. | Added: the recovery cancel of a pre-hold decision, the kind 5 landing on day 15 and cancelled on day 14, the lost number and re-sync, two kind 5s in one block and the deferred decisions executing (§5.9). ⟂ v27 (see the preamble). |
| AJ12 (low) | A stolen key clears as well as sets. | Said in §1, §9, §11.9: the honest seats hold without it. |
| C189–C197 (fact check (a)) | Nine logged answers or chains stating a rule v22 or v23 no longer holds, unmarked. | Marked ⟂ v23 on the rows and listed in the preamble. |
| (b)1–(b)14 | The leak ending vs §6.5; §1 vs §11.9 on who reaches 14 days; the hold's clock; "(against a lone flag)"; kind 1's channel; the §6.4 window missing the decisions that caused the hold; §1 ¶1 omitting Jae's validator seat (three houses); one set change of three (487,883 at power 1, 495,707 to 60, 720,333); "1 of 3 things"; the realms' 7-day delay with no devnet factor; README's "the quarantine". | All folded: §1 ¶1 and §3.3 (three houses), §5.9, §8.1 and §13 (three set changes), "1 of 4 things", the realms' factor and the delay constant in §5.3's list and §8.3, README fixed; the rest under AJ1–AJ9. (b)14 verified §2's builder claim on the chain. |
| (c) | Every number holds to the lamport; griefing "5–10 SOL/day" rounds high — 4.3–8.6 by the plan's own inputs. | "≈4–9 SOL a day" (§1, §7.2, §9). The validator thresholds under today's set and both open proposals confirmed. |
| (d)1–(d)7 | The realms' delay constant unlisted; kind 1's channel; kind 5's count; two kind 5s pending; the hold clock's restart; §8.1's share as a number. | Listed (§5.3, §8.3); count ≤ 4 and two may pend (§6.5, §13); the 24-hour sticky slot (§6.1); "1 of 301 today, 60 of 360 under Prop #13" (§8.1). ⟂ v25 (see the preamble). |
| (e) | The skeptic's list under the no-veto rule: a 7-day brake against a forger; 0 days against a prepared pair; the unreachable recovery; one person in three houses; one outside validator short; nothing clocks GovDAO's inaction; a lone awake outsider is a re-flag loop; the drills skip the v22 path. | The first three are AJ1 and AJ2's fold; the three houses and the re-flag loop are now in §1; "one short" and GovDAO's inaction stay as stated limits. |
| (f) | The leak paragraph rewritten as a numbered walk. | Adopted in that shape, with v23's stand-off ending (§1). |
| (g) | Verdict: not consistent end to end after the fold; decide the forged-kind-5 hole first — A, accept 7 days; B, an unsettled set's decisions wait for settlement. | Neither A nor B: B does not cover a leak of the real keys (no rotation is needed to forge); v23's window rule covers the leak, the bug and the pair alike, at the price §11.15 states. Round 20 reviews v23. |


Round 20 (2026-10-12, on v23; both reports folded into v24). Red team (the AK series) and fact check (C198–C204 and
(a)–(g)). The round's finding: v23's clock lived on flags — three keys could rotate it forever, and a per-flag
sticky slot broke the unpause test — and its recovery cancel was bound to a counter a thief with the same keys
could spend every slot.

| ID | Finding | Answer (v24) |
|---|---|---|
| AK1 (high) | Three guardian keys wedge the Solana side forever: the start was the second-earliest standing flag, so rotating the earliest one every 13 days kept the window from ever opening. | The hold's start belongs to the object: `holdSince` is set when standing guardian flags reach two, kept through a dip below two shorter than 24 hours, reset by a dip of 24 hours; settlement, the window and the kind-5 floor read it (§6.1, §6.4, §6.5, §13). |
| AK2 (high) | The stand-off was a counter race: a thief with the same keys spends every recovery counter value within a slot, so the validators' cancel never lands. | A recovery cancel carries no counter (digest `gno-bridge-cancel ‖ chain id ‖ program id ‖ body`), is accepted whenever a window stands, cancels each named decision once, replays as a no-op, and may name a range; the counter stays on the 7-day recovery kinds (§6.4, §13); §1 calls the stand-off a machine race run by the validators' tool. ⟂ v27 (see the preamble). |
| AK3 (medium-high) | One sticky slot per flag let a queue of unpauses clear every re-flag, and contradicted §5.9's fresh slot. | Flags carry one slot, fresh on every set, which only an unpause reads; the sticky rule moved to the hold (§5.8, §6.1, §6.5). |
| AK4 (medium) | §1 said "cancel"; the override carries anything and GovDAO cannot cancel a recovery. | Said in §1 ¶1 and the hard truth: "except 2 people — and, on Solana, except the validator majority once a hold has stood 14 days". |
| AK5 (medium) | A hold the operator's two seats set long before makes "15 days" 7 against the pair that includes the operator. | Said in the guardians bullet and §9: the outsiders' flags add nothing then; the window is open throughout. |
| AK6 (medium) | A stolen early flag closed the window by a clear and reopened it by a re-set. | Moot under AK1's rule: a clear that leaves two flags changes nothing; a dip under 24 hours closes nothing (§6.4). |
| AK7 (low-medium) | A stolen seat's clear could settle a forged set. | Moot under AK1's rule (the count never dropped below two); §11.9: three or four seats hold when they can. |
| AK8 (low-medium) | GovDAO's own pending kind 5 lands on day 15, ends the hold, voids the recovery, and the deferred flood executes. | §6.4's procedure cancels every pending kind 5 first, GovDAO's own included; a dip under 24 hours no longer closes the window; §11.9: GovDAO proposes no kind 5 under a hold but the holders' own. ⟂ v27 (see the preamble). |
| AK9 (low) | "A duty once a week" understated a free flood of forged decisions. | The leak walk: a machine race; range cancels; the deployment abandoned for a new one the day the thief begins (§1). |
| AK10 (low) | A message under 9 bytes had no outcome. | `Handle` fails; the mailbox leaves it deliverable; the relayer parks it (§6.5). |
| AK11 (low) | Kind 7 at its 0.05 SOL maximum prices small exits out in 7 days. | Said in §1's signers bullet. |
| C198–C204 | Z5, AG7, AE14 and round-16 (b)6 unmarked; AA4's mark missing; AJ2/AJ3's answers overstated. | Marked and corrected in place; the preamble lists them. |
| (a) | §1 ¶1 and §4 still carried v22's "lands at 7 days whatever flags stand". | Both rewritten. |
| (b) B3, B13 | "15 days" is 15 from the hold's start and up to 22 from a decision's delivery; §11.15's "8 days more" was 15. | "15 to 22 days" in §1 (×4), §3.3, §9 (×2), §11.9, §11.15; the glossary defines the hold's Nth day as N × 24 h after `holdSince`. ⟂ v27 (see the preamble). |
| (b) B4 | The leak walk's "holders leave at the meter's rate" contradicted §5.6 after any forged mint. | Conditioned: at the meter's rate only if nothing minted in the minute before the flags; else by vote, 7 days a batch. |
| (b) B5 | "Once a week" vs a thief's free flood and a 16-entry cancel. | Range cancels and the validators' tool (§1, §6.4, §13). ⟂ v27 (see the preamble). |
| (b) B6 | The override is unreachable against two of today's three pairs (the member-linked validators hold 60%). | The numbers in §1's validators bullet, §1 ¶1, §3.3, §8.1 and §11.15: today against Jae + g1aeddl only; under Prop #13 against none; under Prop #10 against the two pairs that include Jae. ⟂ v25 (see the preamble). |
| (b) B7 | "1 proposal that does 3 things" had no builder. | The void builder's `andUnpause` takes the unpause builder's parameters, keys to remove included (§5.3, §1, §5.9). |
| (b) B8 | The sticky slot vs the unpause test; a day-long clear's consequences unsaid. | AK3's fix; §6.1 and §6.4 say what a 24-hour dip does. |
| (b) B9–B11 | "at any time"; two things named `set sequence`; kind 4 is really a layout-version setter. | A recovery cancel exists only while a window stands; r/hlgov's builder is `sync sequence`; kind 4 is `set client layout version`. ⟂ v28 (see the preamble). |
| (b) B12 | §1 promised the validators' cancel unconditionally. | Qualified: not under a leak, and never by a set a pair announced under a hold. |
| (c) | Every number holds; the updater's float lasts 5–7 months at the plan's own fee, not 4; §10's fee band. | §5.5 and §10 corrected. |
| (d) | "the hold's Nth day", "settled", "window", "object", the kinds' numbering, a cancel's execution moment, the factor's reach, "the compiled launch minimum", the signing helper's review. | Glossary entries; §6.5 (a cancel executes in its recording transaction); §5.9 (the factor divides every compiled delay); §5.3 (the transfer minimum); §11.2's scope. |
| (e)–(g) | The skeptic's list; the guardians bullet in numerals; the verdict: decide B3/B8 and add B6 first. | B3 accepted and written; B8 fixed by AK1/AK3; B6 written; the (f) rewrite's numbers adopted in the text, its shape not. Round 21 reviews v24. |


Round 21 (2026-10-12, on v24). The fact check (C205–C210 and (a)–(g)), folded into v25; the red team's AL series
follows, folded into v26.

| ID | Finding | Answer (v25) |
|---|---|---|
| (c) Prop #13 | Executed at block 741,860, 18 minutes before v24's commit: 6 × 60 = 360; forging 5 of 6; member-linked 240 = exactly 2/3; halting any 2 (moul + Berty = 120); the override reaches no pair today or under Prop #10; Jae's share 60 of 360. | Every "today" restated (§1 ¶1, the validators bullet, the leak walk, §2, §4, §5.9, §6.1, §8.1, §11.15, §13); §1 now says that against GovDAO itself there is no override today. |
| (b) B1, B3 | A replacement's clear was a "dip", so the hold ended on its 16th day; which rules read `holdSince` and which the live count was undefined. | A replacement's clear that leaves fewer than two flags ends the hold at once (the grace is for a holder's own clear); "a hold stands" reads `holdSince` for the deferral, the unpause test, the owner's clearing and the window; deliveries follow the flags (§5.8, §6.1, §6.4, §6.5). |
| (b) B2 | "A minority" was not the rule. | "Under 1/3 of the settled power" (§1, §3.3, §11.15). |
| (b) B4 | §13's dip in slots. | Clock time (§6.1, §13). |
| (b) B5 | The glossary froze settlement at the quarantine's end. | Rewritten: settled once the quarantine ended and no hold begun by then still stands. |
| (b) B6 | §11.9's parenthetical inverted the mechanism. | Rewritten: only a holder's seat's replacement ends a hold; the lift's day. |
| (b) B7, B8 | §1 ¶1 lacked ¶2's operator qualifier and `sync sequence` in the instant list. | Both added. |
| (b) B9 | "No expiry" vs a recovery decision dying with its window. | Qualified (§3.5, §6.5). |
| (b) B10, B11 | "22 at most" unqualified; the pre-held pair's 7 days is 8 with the grace. | "22 after the replacement's delivery when delivered within the hold's first 8 days, its own 7 later"; "7 to 8 when a hold was already standing" (§1, §6.5, §9). ⟂ v27 (see the preamble). |
| (c) updater | "$5–8" rounds high. | "$5–7" (§10). |
| (d) | The tool's cancel period; range semantics; kind 9's flags and hold; the void closure's skipped unpause; the lift's lag; §13's unit. | At least daily; pending-when-executed only; a successor starts with no flags, hold or pending decisions; skipped, resets no meter; said in §1, §6.4 and §11.9; Clock time. |
| C205–C210 | Seven logged rows stating a dead rule or an overtaken fact. | Marked ⟂ v25 and listed in the preamble. |
| (e)–(g) | The skeptic's list (the override reaches no pair; Jae in four houses; a standing hold is a standing window; the stand-off's unbuilt tool; the quarantine at GovDAO's cadence ≈ half the chain's life paused); the §6.1 rewrite; the verdict. | The first two are now §1's own words; the standing window is the design (§11.15); the rest are stated limits. |


Round 21, the red team (2026-10-12, on v25, bounded to 12 findings after an unbounded attempt overran the output
limit; folded into v26 with M2's measurements).

| ID | Finding | Answer (v26) |
|---|---|---|
| AL1 (high) | A signed recovery cancel never died: its bytes, valid for the same settled set forever, could be replayed into any later window — one range cancel signed for a bug in March becomes a permanent veto in a stranger's hands in August. | The digest carries the window's anchor (`holdSince` for a hold-opened window, the last accepted header's hash for a lapse-opened one) and the admin program records each executed cancel's digest and refuses a repeat (§6.4, §13). |
| AL2 (medium) | The lapse window needs no guardian, and no hold brakes a lapse-opened recovery decision: five validators who halt the chain take the Solana side on day 21 with no brake. | A recovery decision records which window opened it; a lapse-opened one of the 7-day group is deferred by a hold like a mailbox decision, landing on the later of its 7th day and the hold's 21st day; hold-opened ones stay undeferred; said in §1 and §9 (§6.4). |
| AL3 (medium-low) | The lapse recovery cannot cure a halt by keys lost for good: the halting third is the blocking third. | Said in §6.4 and §9: the exit is burning on the restarted chain and `migrate`; §11.10 may name a separate recovery key. |
| AL4 (medium) | One day for five of six validators at five organisations, keys in hardware signers, to assemble a cancel before a forged replacement lands. | A replacement under a hold lands on the hold's 21st day (a week's margin); "15 to 22" is "21 to 28" everywhere; the margin stated in §11.15. |
| AL5 (low-medium) | A standing hold freezes settlement, so after rotations the recovery's signers may be out of the set and out of assent. | The assent runs while in the last settled set and 30 days after the client settles a set without it, measured on Solana; §11.9 says a hold older than a rotation keeps the signers at the pre-hold set (§6.4, §11.9). |
| AL6 (low) | §8.3's check let the admin program be its own upgrade authority; kind 8 could name the admin program. | §8.3: its own = none; `finish_setup` requires it; kind 8 refuses the admin program's own id (§5.8, §8.3, §13). |
| AL7 (low) | The signers' refusal rule had no reading order, so a lagging node could stop two signers for good. | §5.9's order for the signers; a stale reading alarms, never stops; a stop clears itself once the identity holds (§5.6). |
| AL8 (low) | §9's rows still promised the validators' cancel against a pair unqualified. | "(against no pair today, §1)" on both rows. |
| held | A lone flag survives a flood of unpauses only if re-set daily. | §11.9's orders: re-set a lone flag daily. |
| M2 | The three programs built: 271/148/330 KB (≈3.9 SOL of rent against the 2.8 guessed); ≈60k CU per update, 29–33k a proof, 105–130k a delivery, 164k an upgrade (+21k per 10 KB step, one step per block), 147k a transfer; sets past ~100 validators need a chunked stage; the admin authority PDA needs a float. | §2, §5.8, §6.1, §6.3, §6.5, §9, §10, §11.4: rent ≈8.6 SOL, the ask 7 SOL (≈2.8 shortfall, 0.1 to the authority PDA), the figures in place of the estimates. Round 22 reviews v26. |


Round 22, the fact check (2026-10-12, on v26; folded into v27).

| ID | Finding | Answer (v27) |
|---|---|---|
| C211–C218 | The v23 preamble note still read "15th day"; AJ1, AJ2, AJ11, AK8, AK2, W5/C52, the C66 chain and round-13 (c) state 15 days, an un-anchored cancel, never-deferred recoveries or the old rent. | The note rewritten; every row marked ⟂ v27. |
| (a)9, (b) B1–B4 | Five paraphrased 15-day sentences survived the sweep (§1 ¶2 "15 to 22" wrapped, §3.3 "22 days", §6.4 "the window's second day", §9 "a day" twice). | All five read the 21st day / 28 days / a week. |
| (b) B5 | §6.5 "recovery decisions are never deferred" vs §6.4's lapse-opened deferral. | §6.5 states both cases. |
| (b) B6 | §6.5's "28 at most when delivered within the hold's first 14 days" was the wrong qualifier; §1's "7 to 8 when a hold was already standing" likewise. | "7 to 21 days after delivery for a replacement delivered under the hold; 21 to 28 for one delivered up to 7 days before it; its own 7 days after the hold's 14th day"; §1: "a hold already 14 days old". |
| (b) B7 | One 72-hour run cannot hold the 63-hour replacement clock, the 84-hour bound or the 63-hour lapse drill with hourly transfers. | Three runs of up to 72 hours on the factor-8 build, named in §5.9. |
| (b) B8 | The "measured" rents were priced at KiB-rounded sizes (0.08–0.09 SOL high). | The built ELFs' bytes at v26 (M2b): 1.38, 0.75, 1.72 (3.85); ≈8.6 in all, the shortfall ≈2.8, the ask still 7 (§2, §5.8, §10, §11.4). |
| (b) B9 | §10 still scheduled a build BUILD.md records as landed; §5.8 "three tools still to be built" after M3a built hlwatch's half. | §10 says what landed on 2026-10-12 and what follows (M2b, M3b); §5.8 corrected. |
| (b) B10 | "The holders' lift is its only brake" omitted GovDAO's replacement of a holder's seat. | §1 names both brakes. |
| (b) B11 | "Either watch slot" vs §13's count ≤ 4 and the drill's "both watch slots". | "Both watch slots". |
| (d) | "The anchor", "the lift", "the grace" undefined; two windows at once; the authority PDA's float (0.01 built vs 0.1 budgeted); the run count; §8.4's constants; a channel-1 range's reach. | Glossary entries; the hold's anchor when both windows stand and a decision recorded then is hold-opened; 0.1 budgeted with ≈0.001 a router enrolment built, printed by the audit; three runs; §8.4 lists the 21-day floor; the code follows the plan (pending-when-executed only; M2b told). |
| (c) | Every number stands to the lamport except the KiB rents; the measured programs implement v25 (the 15-day floor, the un-anchored cancel, no lapse deferral). | M2b rebuilt them to v26 the same day (15 program tests green at both factors); the sizes above are its. |
| (e)–(g) | The skeptic's list (no override against a pair; one person in four houses; a standing operator hold is a standing validator window; the programs as built are v25's; five validators in a week asserted, not drilled; the audit a recommendation; the quarantine at GovDAO's cadence; the money); the §6.5 rewrite in numerals; the verdict. | The §6.5 rewrite's numbers adopted; the rest are stated limits or the build's next steps. |

Round 22, the red team (2026-10-12, on v26, bounded; folded into v27). Verdict: nothing above medium — the stop
condition Jae set ("until a round returns nothing above medium") is met.

| ID | Finding | Answer (v27) |
|---|---|---|
| AM1 (medium) | Kind 10 and `sync sequence` could lower the sequence and reuse numbers against cancelled ranges and pending accounts, wedging GovDAO's channel. | Both only raise, strictly above every number recorded; a fresh r/hlgov is deployed with its predecessor's last number compiled in (§5.3, §6.4, §13). |
| AM2 (medium) | Two holders re-flagging at the grace's last second began a new hold and moved the 21st day, repeatably. | The floor is written into the decision the first time a hold defers it; no later hold raises it (§6.5). |
| AM3 (low) | Which window a decision belongs to when both stand was unwritten; the then-built program took the lapse first. | The hold's, written in §6.4 with the reason (a fresh forged header ends only the lapse and voids nothing hold-opened); the v26 programs already do this. |
| AM4 (low) | A spent digest spent for both sides: the same daily body refused, bodies pre-burnable, the digest account jammable by a stranger's lamports. | A random salt in the cancel body, covered by the digest; the account allocated and assigned by the program (§6.4, §13). |
| AM5 (medium-low) | A hold through a leak-driven rotation kept the leaked set as the settled set; §1 and §9 said a rotated-out set was powerless once followed. | "Powerless once its replacement has settled"; §11.9's orders: never hold through the quarantine of a rotation that removes leaked keys (§1, §9, §11.9). |
| AM6 (medium) | A pair with the operator can drain the lock at the meter's rate from day 1 past awake outsiders (forged claims, the flaggers removed at once); §1 said only that it can close the exit. | Said in §1's week paragraph and §3.3 ("drain", not "move"). |
| AM7 (medium) | The immutable admin program parses the client's state with a compiled layout; a client upgrade with a new layout bricks every setting; kind 4 invited it. | The layout is fixed for the deployment's life (version 1); a client upgrade keeps it; kind 4 is reserved and refused; a new layout is a successor by kind 9 (§6.5, §13). |
| AM8 (low) | `Launch` never expired, so a passed vote nobody executed was a standing trap after a redeploy. | `Launch` carries the 14-day deadline; §6.6 says a passed, unexecuted vote lapses (§1, §6.6). |
| AM9 (low-medium) | The signers never stopped when the client ran ahead of the chain; the stop §1 credits them with was the monitors'. | A trusted height beyond both heads by more than an hour of blocks is the mismatch itself: the signers stop (§5.6). |
| AM10 (low) | §3.3's "22 days"; §6.5's 28 on the wrong case. | Fixed with the fact check's B2/B6. |
| AM11 (low) | §5.8's rents reproduced only at 5,200 lamports a byte. | The built ELFs' bytes at 5,080 (fixed with B8). |
| AM12 (low-medium) | Against a pair including Jae, one captured or coerced outsider leaves no hold. | Said in §9; §11.9 puts a fifth seat (a third outsider) to Jae — a pre-launch choice. |
| held | Twelve lines, among them: a spent cancel cannot replay within a window, after a dip, or across resets; the majority-through-a-halt limit is stated; the week against moul + g1aeddl holds; the setup step and `finish_setup` hold; the 7 SOL adds up. | — |

Code to follow the text (small, after the e2e of M3b is green): the programs — kind 10 raise-only, the floor written once, the salt and the allocate-and-assign record, kind 4 refused (M2c); the realms — `Launch` with a deadline, `sync sequence` raise-only with a compiled start, hlgov's kind-4 builder refused (M1b); the Go — the signers' stop on a trusted height beyond both heads, hlwatch and hlsol for kind 4 and the salt (M3c).


The final fact check (2026-10-12, on v27; folded into v28). Editing only: §5.3's `Launch` "no deadline", three
kind-4 residues (§5.3, §5.8, §6.3), §6.1's "once followed", the lapse-opened clock stated once in §6.4 and §1 (wait
until the hold ends; a kind 5 among them lands on the 21st day), §6.5's floor binding only while a hold stands and
its "within the hold's first 14 days", §2's M2b, §10's landed milestones, the leak walk's hour-of-blocks limit on
the exit, §5.9's four runs, §11.9's five places a fifth seat renumbers, the glossary's `holdSince` and `hlsol`;
every changed number recomputed to the lamport at the live rate; C219–C225 marked. Verdict: consistent, at rest.

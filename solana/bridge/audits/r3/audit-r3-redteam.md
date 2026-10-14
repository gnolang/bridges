# Audit round 3, audit 1 — the plan red team with the code in hand (PLAN.md v30, branch `bridge` at 777ceae plus documentation commits, 2026-10-13)

Read: PLAN.md §1–§11 and §13 whole; AUDIT.md; M1-, M2-, M3-NOTES.md whole (M1d, M1e, M2e, M3f especially); REVIEWS.md rounds 21–22; the predecessors' files (AN1–AN10, AO1–AO5, X1–X2). Code read where a finding or a regression line rests on it: `realm/p/hlroute/v0/hlroute.gno` (Credit, the void, restore, DraftVoid, Decide, apply, creditByVote, Export, Import), `realm/hlgnot/hlgnot.gno` (Process, Pull), `realm/hlgov/hlgov.gno` (kinds 9 and 12, `set decision recipient`), `realm/hlmailbox/{mailbox,flags}.gno`, `programs/admin-program/src/{processor,recovery}.rs`, `programs/pausable-ism/src/config.rs`, `agent/{executor_sol,relayer_sol,signer,identity,redact,monitor}.go`, `gno/gno.go`, `cmd/{hlrecover,hlsol,hlwatch}`, `sol/{client,loader}.go`, and gno v1.2.0's `tm2/pkg/sdk/auth/keeper.go` from the module cache. Read-only; nothing built or run; no network. Not spent on: the heap frame, the one day-cap constant for both tokens, the seven-aunt proof packet (known and queued).

## Findings (the AP series)

### AP1 — medium-low — the monitors tolerate a forged credit up to what is in flight; the signers got the exact rule, the watch keys did not

Plan: §1 "The signers stop crediting the moment Solana's supply, the burns in flight counted, exceeds what §5.9's identity allows … and the watch keys stop payouts"; §5.9 "the identity … holds up to in-flight messages; a forged credit breaks it by its amount until GovDAO voids it".

Code: `Identity.Verdict` (agent/identity.go:62–73) answers Broken only when `gap < 0` and Holds while the right side is short by at most `InflightOut + InflightIn`; `SignerStops` (line 80) is the exact rule AN3 gave the signers; the monitor (monitor.go:390–394) and hlwatch (cmd/hlwatch/main.go:785–787) judge by `Verdict`. M3-NOTES AN3: "The monitor's verdict is unchanged (in flight only shortens the right side there)". No rule flags a credited id that neither provider's index holds; that verdict exists only in `hlwatch flag -ids`.

Scenario: A hold stands (a drill, a disputed rotation); the sends made in the minute before the `sends` mirror landed, 60,000 GNOT, stay in flight for the hold's three weeks; exits are open by order. Two signer machines are compromised. Day 3: they credit 60,000 of forged claims to their wallets. Both monitors and both outsiders' hlwatch read "holds" (gap 0 ≤ in flight); the honest signer halts and alarms the operator — a human path. `Claim` pays the forger at the meter's rate, 10,000 GNOT a day, until a hand flags payouts. The same shadow stands for hours under a full day bucket (parked sends) and for good under a stranded send.

Fix: the monitor's and hlwatch's Broken test is the cut's exact one — `supply + inflightOut + inflightIn + owed + voided − lost > lock` (`Right() + Inflight() > Lock`), the short-side patience kept; and a credited id absent from both providers' dispatch index flags `credits` and `payouts` at once. §5.9 lists both.

### AP2 — medium-low — a parked `OverDayCap` delivery starves behind younger sends: "oldest first" orders the attempts, reserves nothing

Plan: §5.7 "a delivery the pausable ISM refuses for its day bucket (`OverDayCap`) is retried, oldest first, as soon as the bucket read at finalized has room"; §9 "honest sends above the room park and retry oldest-first as the bucket drains".

Code: relayer_sol.go:302–334 — a parked message is skipped when `bucketRoom` lacks its amount and the walk goes on; a younger, unparked message meets no `bucketRoom` test at all and is delivered whenever the ISM accepts it, `passUsed` counting it afterwards; nothing holds room for the park.

Scenario: Day cap 100,000 GNOT. A griefer keeps 1,000 sends of 90 GNOT a day flowing (fees 5,000 GNOT a day ≈ $345, the coins back through the exit at the meter's rate); or simply a busy week at three quarters of the cap. An honest 25,000 send arrives when the bucket is full: `OverDayCap`, parked. As the bucket drains (69 GNOT a minute) every younger 90-GNOT send fits and takes the room; the free room never reaches 25,000. The honest send waits for a lull that does not come — days, weeks — its only exit a refund by id after a 7-day vote.

Fix: reserve the room: while a token's oldest `OverDayCap` park waits, the relayer delivers no younger message of that token that would leave it short (the park's amount counted into `passUsed` as reserved); §5.7 says a large send may hold younger ones for amount ÷ cap of a day.

### AP3 — low-medium — the recipient switch under a hold deadlocks GovDAO's own channel; a `Retired` message is dead, not parked

Plan: §6.5 "[the successor] receives decisions only once r/hlgov's `set decision recipient` (§5.3) names it — GovDAO proposes that switch with the transfer … (`Retired`: the message stays deliverable and the relayer parks it until the switch lands)"; §6.5 "a hold defers the whole group".

Code: processor.rs:330–342 — `NotInherited` and `Retired` are answered before the kind is read, so a `cancel` is refused with the rest; processor.rs:650–655 — kind 9 is `Deferred` under a hold; hlgov.gno:626–637 — the switch lands 7 days after execution by gno.land's clock, blind to Solana; `Retired` and `NotInherited` occur nowhere in agent/ or hlwatch but the error-name table (sol/admin.go:917).

Scenario: Day 0: a suspect decision pends; both outsiders hold. GovDAO follows §6.5 and proposes kind 9 and the switch together; both execute on day 1. Day 8: the switch lands; the kind 9 sits deferred on the predecessor until the hold ends. From day 8 every GovDAO decision — its cancel of the suspect decision, its replacement of the holders — goes to the successor, which answers `NotInherited`; the predecessor hears nothing. "A hold lasts until GovDAO replaces the holders" and "GovDAO cancels it if its channel lands" are false until the holders lift (a day) or GovDAO switches back (7 more days); hlwatch says nothing. And after kind 9 executes, a message dispatched to the predecessor is `Retired` for good — its recipient is the predecessor — so "parks it until the switch lands" never delivers; only `Redispatch` after the switch does.

Fix: §5.3 and §6.5: the switch is proposed only once hlwatch shows the kind 9 executed; a `Retired` message is re-dispatched, never parked for retry. hlwatch alarms a pending or queued switch while its kind 9 is unexecuted, and names `Retired`/`NotInherited` on a delivery.

### AP4 — low-medium — the paged migration collides a live successor's own credits with the places it imports

Plan: §5.3 "migrate (the lock, every claim, the sweep entries and the ids of undelivered sends go to a successor realm in pages of 500 records the successor pulls, the lock with the first page)". M1-NOTES R13: "the successor places every imported claim at a per-queue `base + old seq` (the base fixed at the queue's first import, its own claims credited before the import staying ahead), so the order holds whatever order the pages arrive in".

Code: hlroute.gno:1614–1632 — `q.base = q.next` at a queue's first import, then `q.next = max(q.next, base + old seq)`; a native credit between pages takes `q.next + 1` (enqueue, 515–530); a later page's claim at `base + seq` equal to it panics when open (1626–1628) and silently shares the place when not — a later `restore` (788) then overwrites the native claim's slot. `Credit` refuses nothing while pages are outstanding; a successor is live once `AddRoute` registered it (1442) and kind 6 enrolled it.

Scenario: GovDAO migrates hlgnot: 2,000 recipients, six pages pulled over an afternoon. Page 1 imports Bob's claims 1–3. Between pages a burn to Bob is credited on the successor (place base+4). Page 4 carries Bob's old claim 4: open → the import panics and page 4 refuses until Bob's native claim pays — with payouts flagged, as a migration under duress has them, the migration and every page behind it stall; withheld → no panic, two claims at one place, and GovDAO's restore of the withheld one later orphans Bob's native claim: owed, never paid, `claim/<id>` says open.

Fix: the route refuses `Credit` from page 0's import until a page with `More == false` arrives (an `importing` flag; the message stays undelivered, the relayer retries) — the reading the notes already claim ("both refuse every function"); or `Import` places a colliding claim at `q.next + 1` and says so in the event.

### AP5 — low — `hlrecover execute` sinks a growth's rent unasked; hlwatch shows no growth before the vote; "≈0.052 SOL a step" is stale

Plan: §6.5 "the executor paying the added rent, ≈0.052 SOL a step, alarmed with the whole cost before the first step and parked after three refusals like any call"; §6.5 "any member by `hlrecover execute` (a growing upgrade's extend steps included)".

Code: executor_sol.go:305–321 — `ExecuteDecision` computes `add` and sends `ExtendProgramIx` at once, naming the rent afterwards; `extendOnce` (225–229) alarms and sends in the same tick; hlwatch's `upgradeCheck` (main.go:300–327) prints a kind 8's authorities, never the sizes; `ExtendBy` is the whole excess. The hash runs before the length test (processor.rs:775–792) under the tool's 400,000-CU limit (executor_sol.go:298; relayer.go:180), so a buffer up to ≈800 KB is hashable.

Scenario: Two members pass a kind 8 for the client (272 KB) whose buffer is a padded 790 KB ELF; GovDAO saw a hash and a buffer id, hlwatch said "staged". The relayer's 2 SOL float cannot pay the 2.6 SOL extension: parked, alarmed, nothing lost. A member who runs `hlrecover execute` from a key holding 3 SOL pays 2.6 SOL of unrecoverable program-data rent in one command, told afterwards; the buffer's own rent returns to its writer at the upgrade.

Fix: `hlrecover execute` prints the growth and its rent and refuses above `-max-rent` (0.5 SOL by default) without `-yes`; hlwatch prints a kind 8's buffer and program-data sizes with the growth's rent beside "immutable"; §6.5 states one extension of the whole excess, its rent a number of the vote.

### AP6 — low — the launch day cap is a tenth of the lock the notice expects, not of the lock; nothing watches the ratio

Plan: §5.4 "at launch it is the smaller of 100,000 and a tenth of the lock the launch notice expects (§11.11 …), and GovDAO moves it by kind 12 as the lock grows: a forged day's mint before the flags is then bounded by a tenth of the lock, the payout meter's own bound."

Code: setup.go:159–170 compiles `launchDayCap(expectedLock)` once into `DEFAULT_DAY_CAP` (pausable config.rs:25); the bucket is an absolute number; kind 12 is its only mover, 7 days after delivery; no monitor or hlwatch rule compares a cap with its lock.

Scenario: The notice expects a 1,000,000 GNOT lock: 100,000 is compiled. Launch week: the lock is 20,000, the pool's seed. A forged delivery in the minute before the flags mints 100,000 — five times the lock, sold into a pool seeded for 20,000; "a tenth of the lock" is false for months, the pool's depth the only bound (§11.11). A year on, an exit wave shrinks the lock from 1,000,000 to 150,000; the cap stays 100,000, two thirds of the lock, until someone notices and a kind 12 lands 7 days later.

Fix: compile the cap from the lock expected in the first week (the pool's seed) and raise it by kind 12 in steps; the monitors and hlwatch alarm `mint:cap-ratio` when a token's day cap exceeds a fifth of its lock; the notice states each cap as an absolute number beside the lock it was set against.

### AP7 — low — "restorable within 7 days" reads as a deadline; the code has none, and the restore itself takes 7 days

Plan: §5.3 "a sweep entry is voided by its message id the same way — as forged it leaves owed and the sweep queue, restorable within 7 days"; AUDIT.md round 2 "voided by its message id like a claim, restorable within 7 days, and a credit by id may replace it".

Code: `voidSweep`/`restoreSweep` (hlroute.gno:719–736) keep the entry with a `Voided` mark for good; `Decide` queues a sweep entry's restore 7 days like a forged claim's (1291–1296); nothing expires it.

Scenario: A member votes a range void as forged over a flood knowing a mistaken sweep entry among it "is restorable within 7 days", and plans the restore for day 10 once the dispute is read. The vote on day 10 passes; the restore lands on day 17 — the entry was never lost, but the member was told a deadline that does not exist and not told the week the undo takes; the reverse mistake (waiting on an imagined deadline to void) is the same misreading.

Fix: §5.3 and AUDIT.md: "restorable, after 7 days, like a claim voided as forged" — and `DraftVoid`'s description for a sweep entry says so.

## Regression — AO1–AO5, X1–X2

- AO1 (no tool executes): fixed — `agent.ExecuteDecision` (executor_sol.go:261–322) and `hlrecover execute` (cmd/hlrecover/main.go:307–316, 401); §1 and §6.5 name it. Holds; AP5 is its cost preview.
- AO2 (the sweep queue's void): fixed — `SweepEntry.Voided`, `voidSweep`/`restoreSweep` (hlroute.gno:719–740), the list and range forms in `DraftVoid` (1004–1008, 1021–1028), at once under a flag (1212–1234), a credit by id replacing an entry (1469–1477); §5.3, §5.4. Holds; AP7 is the wording.
- AO3 (the rotated-out set until `Settle`): fixed — permissionless `settle` (recovery.rs:113–134) over `settled_set` with both holds read (79–85); the updater and executor call it each tick (executor_sol.go:94–100), `hlrecover settle`, the monitor's `settle:due`; §1 and §6.4 say "recorded by `settle`". Holds.
- AO4 (the set bound unwatched): fixed — `SetSizeWarn` 40 / `SetSizeBound` 50 (sol/client.go:52–56), the audit's line (audit.go:261), the machines' `client:set-bound`; §6.1. Holds; the heap frame stays queued.
- AO5 (the day bucket): fixed — the kind-12 builder refuses a maximum below the route's (hlgov.gno:575–579, at creation and in the closure), `OverDayCap` retried on `bucketRoom` (relayer_sol.go:299–334); §5.4, §5.7, §9. Holds as stated; AP2 is the residue — the order of attempts is not a reservation.
- X1 (a sub-identity registers): fixed — `RegisterVerifier` refuses `cur.Previous().Subpath() != ""` (mailbox.gno:347), the listing at 20 with a count and `verifiers/<from>`; `AddRoute` and `DraftMigrate` refuse a `#` path (hlroute.gno:1124). Holds.
- X2 (the key slice): fixed — `satisfiable` hands each verifier `append([]Key(nil), ks...)` (mailbox.gno:369), the signer set and rule copied on read (244–245). Holds.

## Checked and held

- `hlrecover execute` by a hostile member: the program checks every passed account against the record — the deliverer (processor.rs:540–544), a kind 8's program, program data, buffer and refund key (766–774), a kind 9's successor, its program data and authority PDA (812–823); NotMature and Deferred are the program's clocks; the decision's rent goes to the deliverer, the member pays fees and growth only. Nothing early, nothing diverted.
- The recovery's power at `settle`: `settled_set` blocks an entry only by a hold begun at or before its quarantine's end, so §11.9's week to hold before a distrusted set settles is the whole brake, and a thief with no guardian key can neither block a settle nor settle his own set before a week with no hold — the plan's own rule.
- The signer's verdict slot (G13): a burn above the snapshot waits one round and is counted in flight once a snapshot passes its slot (signer.go:335–349, 560–563); none is dropped. The exposure is liveness: a provider whose finalized view lags the credit pipeline by more than ~10 s makes `CreditedAfterSlot` fail the cut every round under a steady flow, and an hour of it halts that signer — one provider, two signers, §9's row.
- The disputed records (G17): a provider that disputes everything blinds its signers (no verdict, an hour, halt) and its monitor's diverted-credit check for those ids, never the identity's Broken side (the supply must agree first). Liveness, as stated.
- Provider URLs: `Redact` strips userinfo, path, query and fragment; providers are bare URLs (config.go:48), so no header carries a key; a key in a host label would survive — none of the usual providers puts one there.
- The extend: `ExtendBy` is the whole excess to 10 MiB; the relayer's float and the 400k-CU hash bound what an execute can sink (≈2.7 SOL for the client), AP5's number.
- The ELF binding (G20): `ELFBindsKey` (sol/loader.go:56) proves the deployed bytes contain each 32-byte constant, contiguous or as four `lddw` immediates — a wrong launch value is caught; that the code compares against it is proved by the reproducible build hash (§8.2) alone, which §5.8's "carry the compiled bindings" says honestly.
- The gas price (F9): the relayer pays whatever `auth/gasprice` names, no ceiling (gno.go:60–72); gno's rule (keeper.go:410–480: +4.3% a full block at target 70 and compressor 10, −10% an under-target block, capped at 10¹²) makes a spike cost its maker ≈3 GNOT × multiplier a block against ≈0.045 × multiplier a relayer call — a 100× spike costs ≈$300 to reach and ≈$20 a block to hold; "insufficient fee" is re-priced, never parked. The fifth's margin fails only when a broadcast does 20% more than its simulation (a `Claim` whose meter refilled between): transient, re-measured next call.
- 23-validator chunks and chunked precommits: `TestStagedTxsFitThePacket` over 6, 17, 18, 60 and 255; one round per commit is tm2's `Commit.ValidateBasic` — a block decided at round r carries round-r precommits only, and a halted height has no commit to stage.
- The sub-identity refusal and the open `Dispatch`: a spam realm's messages cost a deposit each (≈0.02 GNOT), are paged, and the relayer skips a non-route sender once (`classify` → "other", `GnoNext` advances); no other prefix-keyed registry remains in the hl* realms.
- The economics: §11.4's 8 SOL sums to the lamport (3.2 + 2 + 1 + 0.3 + 0.3 + 0.1 + 1.1) against 9.0 of rent, 0.15 of state and 1.5 of floats; unbudgeted and small: a successor's own deployment (≈2 SOL, a cure) and the client's `Grow` toward 80 KB (≈0.4 SOL from the updater's float as the set grows).
- Ranges: `DraftVoid` scans at creation and refuses past 1,000 voidable credits (1004–1015), the range closed there (997) and walked whole at execution; a flood past the cap costs GovDAO proposals, not safety; a range void over a flood interleaved with honest claims voids those too — the list form (256 ids) is the cure, as §5.3 has it; a `since:` flag still covers later forged claims, so a later void of them is at once.

## Verdict

Honest for a GovDAO member on every ending §1 promises, with one qualification he must hear: as built, the operator's monitors — the automatic brake on payouts — read "holds" against a forged credit up to the amounts in flight (AP1), which under a hold or a full day bucket is tens of thousands for days; round 1 gave the signers the exact rule and left the monitors with the tolerant one, a three-line fix and a flag rule the plan already implies. The rest are stated limits to write down and small guards: a large send starving behind a steady flow (AP2), the recipient switch deadlocking GovDAO's own channel under a hold and a `Retired` message that is dead, not parked (AP3), a live successor's credits colliding with the pages it imports (AP4), a blind growth rent (AP5), the launch cap against the actual lock (AP6), a deadline that does not exist (AP7). All buildable without a new rule. Nothing above medium; AO1–AO5 and X1–X2 hold.

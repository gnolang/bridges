# Building the bridge — milestones, in order

The spec is `PLAN.md` (§5–§6 and §13 are the build spec; §8 is what the deployment's checklist and the yield vote check). This file is the work
plan: what gets built, in what order, and what "done" means for each piece. Started 2026-10-12.

## Toolchain

- gno: `gno`, `gnodev`, `gnokey` in `~/.local/bin` (gno v1.2.0 sources are the reference for the realms); tests run
  with `gno test`; the local chain with `bridge/local/e2e.sh` (stages `examples` packages beside the realms).
- Go: `bridge/` is a Go module (`go.mod`); `go test ./...`.
- Solana: CLI 3.0.14 at `~/.local/share/solana-3.0.14/solana-release/bin` (`cargo-build-sbf`, `cargo-test-sbf`,
  `solana-test-validator --url mainnet --clone-feature-set --clone-upgradeable-program TokenzQd…`); Rust via
  rustup (`~/.cargo/bin`), installed 2026-10-12 for the three programs.
- Hyperlane reference: `~/.config/bubblerumble-jarvis/bridge-ref/hlsvm/rust/sealevel/` (mailbox, token, ISM
  interfaces) and the dumped mainnet `.so` files (`bridge/local/dump-programs.py`).

## M1 — gno.land realms to the plan's shape (§5.1–§5.5, §5.3's builders, §13's encodings)

`realm/p/hyperlane/v0` stays (Hyperlane's formats, checked on its vectors). New or rewritten realms:

1. `realm/hlmailbox` — the verifier slot (`Verifier` interface with `Verify` and `Satisfiable`; `RegisterVerifier`
   from /r/ only; GovDAO activates by pkgpath), `Dispatch` with the §13 event, `Reemit(nonce)`, `Process` with
   `done.Set(id)` before `Verify` and panic-on-error, the guardian and watch-key lists, three flags per key per
   route with an id list on a guardian's `payouts` flag, `Paused(route, fn)`, the `Launch` builder (one-shot, no
   deadline, refuses every other builder before it), decisions page (last 100 pending and executed per kind),
   builders: set verifier, rotate signers (set + rule), rotate guardians/watch keys, unpause, cancel, migrate
   plumbing. Every launch value compiled in and written by `init()`.
2. `realm/hlgnot` — the GNOT lock (origin-call only, exact `{ugnot}`, refund via origin send), caps, claims,
   meters (bridge and per-recipient quarter), `Claim(n ≤ 20)` in turns with the fee as first installment, the sweep
   queue, per-message maximum, builders: set cap floor, set maximum, set fee and minimum, set fee floor, set
   treasurer, set router, void (withheld/forged, `andUnpause`), restore, credit by id, direct sweep, migrate.
3. `realm/hlbubble` — the same, for BUBBLE (the day cap becomes the meter; `SetPaused`/admin removed).
4. `realm/hlfees` — fees in, `Withdraw` by the treasurer only.
5. `realm/hlgov` — kinds 1–9 as builders (+ `set decision recipient`), §13's body encoding, dispatch to the admin
   program, re-dispatch until recorded; refuses builders before `Launch`.
6. The ed25519 verifier realm with the two-group rule (`Satisfiable`).
7. Tests: every builder's clock (7-day, at-once, the at-once tests at execution), the void's per-id rule, the
   meters' arithmetic (§5.4), the identity counters (§5.9), migrate; `e2e.sh` updated to the new shape.

Done when: `gno test` is green on every realm, `e2e.sh` locks, mints, burns and claims against the new realms with
the three-signer verifier, and the realms' renders show every launch value.

**Status 2026-10-12: M1 landed on `bridge` (merge 98762d9, built at plan v24).** The realms above plus
`realm/p/decisions/v0` (the decision ledger) and `realm/p/hlroute/v0` (the route state machine the two locks
share); 16 test suites green (`scripts/gnotest.sh`), `e2e.sh` green (GovDAO launches, 500 BUBBLE out, 200 back as
a claim paid with the fee first, the identity holds). Every reading chosen where the plan left room is in
`M1-NOTES.md`. Left for later milestones: the relayers' `Claim`/`Execute`/`Reemit` loops (M3), the devnet domain
and the real launch values (M4; every launch value is a placeholder constant today), a successor realm's `Adopt`.

## M2 — the three Solana programs (Rust, §6, §5.8, §13)

1. `sol/light-client` — the gno.land light-client ISM: `Update(header, commit)` with per-precommit sign bytes,
   power-weighted +2/3, NextValidatorsHash in order, the quarantine, settlement, the 256 ring, the counter and
   running hash in state, staged PDAs keyed by (submitter, hash), `Verify` for `process` with a `SimpleProof` over
   `LastResultsHash` and the `Dispatch` event check, the recovery hooks (lapse, hold), kind-11 reset by the admin
   PDA, `init` gated on the upgrade authority, a clock factor constant.
2. `sol/pausable-ism` — seats, slots, flags with set-slot, holds, the quarantine read from the client's state PDA,
   `Type`/`VerifyAccountMetas`/`Verify` with own-seed accounts, owner = the admin program (unpause by slot,
   replacement through any hold).
3. `sol/admin` — a mailbox recipient under the light-client ISM, `Handle` bound to our mailbox's process authority,
   the decision lifecycle (record on the 9-byte prefix, kinds 1–11, clocks, flags and holds, kind 5 through any hold,
   `stage upgrade`, extend-then-upgrade, kind 9's immutability check, `finish_setup`, the recovery channel with the
   digest and counter), `init` with the guardian lists.
4. Tests with `cargo-test-sbf` / program-test against real gnoland-1 headers (captured by the Go tool), the 487,883
   rotation replay, multi-part sign bytes, forged-header refusal, the quarantine, a hold and a replacement through
   it, recovery by both triggers at factor 8.

Done when: the three programs build reproducibly, the tests above pass, and a local validator run mints a GNOT
transfer end to end under the light client.

**Status 2026-10-12: M2 landed on `bridge` (merge 008af47, built at plan v25).** `bridge/programs/`: a Cargo
workspace — `gno-light-core` (no Solana dependencies; header hash, sign bytes, validator leaves, the commit rule,
SimpleProof, the results leaf and `Dispatch`, the recovery digests), `sol-util` (the precompile reader, Hyperlane's
interface and layouts, the loader's, the seats/flags/`holdSince` model), the three programs, a program-test
harness and suite under `programs/tests/` (its own workspace), fixtures for 17 heights fetched by
`bridge/tools/gnoheaders.py` (the four real set changes among them), `programs/test.sh` (`core | sbf | suite |
all`, `FACTOR8=1` for the devnet clocks). 24 host tests (8 against the chain's own blocks) and 13/13 program tests
at factor 1 and at factor 8. Compute units and the 23 readings chosen where the plan is silent are in
`M2-NOTES.md`; the ELF sizes (271/148/330 KB) put the three programs' rent near 3.9 SOL, above §5.8's 2.8 guess.
The local-validator end-to-end mint is M3b's `e2e.sh`.
**M2b landed the same day (merge 1bf9908; 15 program tests and 24 host tests green at both factors on the merged
tree): the programs at plan v26 — the recovery cancel's digest carries the window's anchor and executed digests are
recorded (`cancel ‖ digest` PDA) and refused again; a replacement under a hold lands on the hold's 21st day;
lapse-recorded recoveries are held like mailbox decisions; kind 8 never the admin program; channel-1 ranges clamped.
Sizes as built: client 271,160 B, pausable ISM 147,760 B, admin program 338,128 B (≈3.85 SOL of rent).**

## M3 — the Go side (§5.6–§5.8)

1. `cmd/hlsol`: `setup` (six deployments, the setup step in order, resumable, the done condition), `audit` (§5.8's
   list, printing its RPC calls), `send`, `balance`.
2. `cmd/hlwatch`: the guardians' tool (§5.8), the identity from the holder's own node and two providers, the
   forged-id rule, the flag command (both objects, one stroke; refuses without a node), pre-armed proposals.
3. The updater (headers on a clock and on the valset events), the relayers (messages with merkle paths, decisions by
   sequence number, `Claim`, `Execute(id)`, `Reemit`, parking and retries), the signers (own gno node, two
   providers, the refusal rule, the signer rule), the monitors (the ordered identity cut, the third-source rule, the
   flags they may set), the roster comparator (Proposal 2, later).

Done when: `e2e.sh` runs the full loop with these binaries against the local chains, and `hlsol audit` prints a
setup that matches §5.8 line by line.

M3a landed 2026-10-12 (merge ccddc24; `go test ./...` and `e2e.sh` green on the merged tree; `M3-NOTES.md`): the signers, the relayers' gno.land side
(`Process`, `Claim`, `Execute`, `Reemit`, parking), the monitors, `hlwatch`'s gno.land half, and `e2e.sh` with the
machines running — everything that needs only the realms. M3b landed 2026-10-12 (after M2, against the v25
programs): the updater, the gno → Solana delivery under the light client, the decisions to the admin program with
the taken-number report, the monitors' Solana side (the header record, the fork rule, the foreign-decision rule,
the Solana flags), `hlwatch`'s Solana half, `hlsol setup` and `hlsol audit`, and `e2e.sh` running the full loop
under the light client green from nothing against the v26 programs (both mints from real local headers, the
claim, both identities, a decision recorded pending at its factor-8 maturity and its cancel executed, `hlsol
audit` PASS, hlwatch green; the five tooling edges the runs found and fixed are in `M3-NOTES.md`). The merge's own run (56b7f5f, 2026-10-12) is green: `go test` and
`e2e.sh` from nothing — both mints under the light client, the claim, both identities, the decision and its cancel,
`hlsol audit` PASS with 22 RPC calls. **M1b landed (54d0637; 16 suites green on the merge): the realms at v27 —
`Launch`'s deadline, `sync sequence` raise-only with a compiled start, kind 4 refused, the salted cancel body.** **M2c landed (04cca1c; 18 program tests and 24 host tests green at both factors on the merge): the programs at v27
— kind 10 raise-only, the replacement floor written once and binding only while a hold stands, lapse-recorded
recoveries waiting for the hold's end, the salted and self-allocated cancel record, `HandleAccountMetas` in
`handle`'s order, kind 4 reserved and the client's layout pinned at version 1; ELF sizes 271,160 / 146,808 /
337,576 B.** **M3c landed (30ac71a; `go vet`, `go test` and `e2e.sh` from nothing green on the merge, 2026-10-12): the Go side
at v27 — the salted cancel body, the decision's floor, a Solana executor (`agent/executor_sol.go`), `HandleAccounts`
in `handle`'s order, the signers' stop when the client runs ahead, kind 4 reserved in hlwatch and hlsol, 61 vectors
regenerated. The loop now ends with two salted cancels executed and the audit's 44 lines passing.** M1–M3 are done;
M4 (devnet) waits for Jae's go. Left: the roster comparator
(Proposal 2), the public checkpoint store.

## The audit loop (AUDIT.md), from 2026-10-13

Round 1 (seven audits on be69e1c) folded into plan v29 and three fix milestones, each verified on the merge: M1c the
realms (4dbb4a5; 16 suites), M2d the programs (9e774e0; 25 suites at both factors — the recorded settlement, chunked
staging, the hold history, the mint limits, `ExtendProgram` top-level, `successor_init`), M3d the Go (940f0bf; vet and
race green, the e2e verified through the unpause), M3e the Go at M2d's formats (ef039ff; vet and race green; the e2e
from nothing passed whole in 582 s — the chunked next set and `Grow`, `settle`, kind 12 delivered, the 30,000 send
refused and parked, hlrecover's chunked bodies, the final audit's 49 lines, hlwatch green). Round 2 reads ef039ff.
Known to round 2 before it ran: r/hlgov had no kind-12 builder yet (the e2e staged bridge/local/hlgov_mintlimits.gno
into its realm copy); the heap frame for the validator-set bound is unbuilt.
Round 2 (seven audits on 22d28c8) folded into plan v30 (1fdaf17, 1ebce69) and three fix milestones: M1d the realms
(1d216b0; 16 suites green; the e2e from nothing passed in 575 s on this code tree — the kind-12 builder, the sweep
queue's void, voided claims leaving the queue, the paged migration, ranges closed at creation and capped at 1,000,
the two maxima ordered, seat removals by holder, `sync sequence` bounded), M2e the programs (28 suites at
both factors — one round per commit, which changes `StageInit`'s format; the ring kept through a growth; a proof
header recorded once; a reset's chain id; `Retired`) and M3f the Go (G13–G21, K13–K16, K21, AO1 `hlrecover
execute`, AO3–AO5, F6 23-validator chunks under the packet limit, F7 one extension a block, F9 the gas price read,
the day cap from `-expected-lock-gnot/-bubble`), merged together as 27275fc: vet and race green, the e2e from
nothing passed in 775 s on that code tree (M3f's run 5; its run 4 died when the Mac's disk filled). M1e the realms
(the focused interrealm/security-guide pass: X1 the sub-identity registration refused and the listing bounded, X2
the key slice copied) merged as 777ceae (16 suites green; its own e2e 593 s); the whole tree's e2e from nothing passed in 729 s on 777ceae
(log: ~/.config/bubblerumble-jarvis/bridge-e2e-logs/round2-whole-tree-777ceae.log). Round 3 reads 777ceae.
Round 3 (seven audits on 777ceae) folded into plan v31 (b46bf91, 774181c) and three fix milestones: M1f the realms
(9c73f0f; 16 suites; its own e2e 2,097 s under a foreign load — the sweep queue by id, the migration's gate and
counters, an announced successor, removals by holder, list voids skipping, the range cap counting all, the page
count, credits and turns bounded by the lock, r/hlgov's body bound and no self-transfer), M2f the programs (32
suites at both factors — a retired predecessor closes what pends, MAX_PARAMS by the account, a self-transfer spent,
a staged ring proof kept through the ring's churn: the Stage +48 B and the Body +8 B) and M3g the Go (G22–G27, AP1–
AP3, AP5, AP6, F14, F15, K26, S-15's hlwatch half, the S-16 layouts), merged together as da9567d: vet and race
green, the whole tree's e2e from nothing passed in 1,071 s on that tree (log: m3g-run1.log). Round 4 reads da9567d.
Round 4 (seven audits on da9567d) folded into plan v32 (1706dd9, fda5525) and three fix milestones: M2g the programs
(a7736a8; 36 suites at both factors — a growth keeps the ring's capacity, a kind 9 to a successor that cannot inherit
spent, no recovery stage on a retired program; no format change), M1g the realms (75a1562; 16 suites; its own e2e
827 s — a successor takes its predecessor's messages after its import, refunds for imported sends only with the mark
readable, the announced route, the flags following the lock, no range void or migrate mid-import, former recipients
refused, refunds bounded by the lock, the body bound 512) and M3h the Go (b24c9e9: transaction reads at version 1,
the stalled scan and failing poll alarms, a checkpointed record, the migration followed and judged over the pair,
refunded sends dropped, a lowered cap re-marking a park, the tools following the chain's successor, the cap-ratio
alarm quiet under the expected lock, a cancel's pairs under the packet, the alarms' hygiene): vet and race green, the
whole tree's e2e from nothing passed in 872 s on that tree (log: m3h-run1.log). Round 5 reads b24c9e9.
Round 5 (seven audits on b24c9e9) folded into plan v33 (360a018) and three fix milestones: M2h the programs
(5e5ddd0; 38 suites at both factors — an un-inherited successor runs nothing of its own before the hand-over, the
hand-over's wire contract pinned byte by byte; no format change; the admin ELF 400,280 B), M1h the realms (e003ee7;
16 suites; its own e2e 755 s — a migrate waits for the successor's own import and refuses a registered path, a
successor that pulled nothing re-pointed by a second migrate, nothing credited on a successor before its last page,
refunded sends travelling in the pages, a successor's meters starting empty, a refund waiting 7 days on kind 6's
enrolment note, a write-off for a send delivered after its refund) and M3i the Go (f769e12: the line of migrations
followed sixteen deep by the relayer, the monitors and the signers, a successor-addressed burn waiting on the
import's end, the cap-ratio quiet's two expiries, the launch maximum compiled by hlsol setup, the monitors reading
an unprocessed send once, hlwatch judging a refund against the enrolment note, the successor check over pending
decisions, recovery counter and layout with hlrecover init refusing an un-inherited successor and hlrecover version,
the relayer taking the token's line as its own with the enrolled router checked, the checkpoint's header checked
against the own node and its ring in the program's eviction order with the index checkpointed, the seed 50,000
slots, the gone decision a note under a window or hold, the hop bounds alarmed, hlwatch's cap-ratio key, the
alarms' hygiene): vet and race green, the whole tree's e2e from nothing passed in 730 s on c89f315, the tree the
merge carries (log: m3i-run1.log). Round 6 reads the commit after this one.
Design change (2026-10-13, Jae): the validators' recovery path removed — plan v35; M2j the programs (no recovery
channel, stages, digest, counter, window or settled set; kinds 10 and 11 reserved; the client without a reset), M3k
the Go (`hlrecover` cut to `version`, `status`, `body`, `execute`; the recovery drills out of the e2e); round 7 reads
the result.

## M4 — devnet and the drills (§5.9)

Deploy to Solana devnet against gnoland-1; the client follows the chain for two weeks; the four 72-hour runs with every
case in §5.9's list on the factor-8 build; the hand-over drill and the fork drill; `hlwatch` run by two outsiders (or, while they are unnamed, by the operator's stand-ins on two of its own machines, the named outsiders repeating the run before the yield vote) on
their own nodes. Then the mainnet deploy with the governor — the setup step, the audit, the checklist, his `Launch` — and the
testing phase with a small lock, the review or audit §11.2 decides on running meanwhile; the outsiders seated, the
notice, the yield vote, his `Yield` (plan v36, §6.6, §11.16).

## Order and parallelism

M1 and M2 run in parallel (different languages, different people or agents); M3 follows M1 (the realm interfaces)
and M2 (the program instructions); M4 follows all three. Each milestone lands on branch `bridge` in small commits
pushed to the box; nothing deploys anywhere but local validators and devnet until Jae says go.

Round 6 (seven audits on 95a7252) folded into plan v34 (d5579a3) and three fix milestones: M2i the programs (ab1444c; 40
suites at both factors — a mailbox decision never reads the client, a kind 9 spent at another clock factor, the
hand-over sweeping the authority float), M1i the realms (86d60b5; 17 suites — a successor takes no send before its last
page, kind 6 only after page 0 and only the token's own live route, a dropped route inert, the refund's 14 days and the
cancel clearing the note, a write-off's restore, every route's address swept, the reference successor realm hlgnot2 with
a permissionless Adopt and the migration driven end to end in its suite) and M3j the Go (8f04e566: the refund gate
read on Solana, the line's proposals and decisions listed, an unread line holding, the checkpoint trusting the claims
before it and comparing its header every poll, the two maxima compared, a failed stroke printed as failed, hlrecover
cut to version/status/body/execute, the executor and monitors without Settle, the vectors regenerated). Then the design
change: plan v35 (0a4ae4a, 8c0872e), M2j the programs (80ebfec; 31 program-test tests at both factors; the admin ELF
316,656 B, the client 270,120 B), the Go's removal inside M3j; vet and race green, the whole tree's e2e from nothing
passed in 731 s on f900c338, the tree 8f04e566 merges (log: m3j-run3.log; runs 1 and 2 stopped on the e2e's own stale texts, not the Go). The final check (two audits) reads 8f04e566.

The final check (two audits on 8f04e566) folded into the plan (469d35c2) and two last milestones: M1j the realms (c738edfd;
17 suites — a refund applied by GovDAO's at-once `apply refund` after hlwatch's Solana check and never by `Execute`, a
dropped route's address swept, a successor sending nothing before its kind 6 is dispatched) and M2k/M3k the programs and
the Go (57b47988; 32 program-test tests at both factors — `Init` carries r/hlgov's counter (+8 bytes; the admin ELF
316,512 B), `hlsol setup -start-sequence` and the audit's counter FAIL, the relayers skipping refunds, hlwatch's
apply-refund check, the vectors regenerated): vet and race green, the whole tree's e2e from nothing passed in 718 s
on 0ffa726b, the tree 57b47988 merges (log: m3k-run1.log). The audit loop ends at 57b47988; M4 reads it on Jae's go.

Design change (2026-10-14, Jae): the governor and the yield — plan v36 (each realm compiles a governor address; every
builder, `Launch` and r/hlgov's decisions are his direct call through `Govern` until his one-way `Yield` on hlmailbox,
GovDAO's proposals refused before it and the only way after; the launch vote became his `Launch` on a published
checklist plus GovDAO's yield vote; the Solana programs untouched); M1k the realms, M3l the Go; a closing conformance
check reads the result.

The governor and the yield folded into plan v36 (c58b977c) and two milestones: M1k the realms (83fcb9dd; 17 suites, the checker
clean — `Request`, `Govern`, `mustBeGovernor`, one `Yield` on hlmailbox, the compiled `launchGovernor` checked at init, the renders
and json) and M3l the Go (ee8a9074; the governor line and "awaits the yield" in hlwatch, the finisher gated on the yield, the e2e
staging test1 as the governor with p/decisions at a clock factor of 1008 so a 7-day decision lands inside the run): vet and race
green, the whole tree's e2e from nothing passed in 741 s (log: m3l-run2.log). The closing check: nothing high or medium; GV1 (medium-low) the plan's own sentence — a GovDAO proposal made before the yield is refused at execution, not at creation, so pre-armed proposals execute the block after the yield (folded: §5.3, §11.16's order, the relayers' gate stated); GV2–GV4 (low) texts that still say GovDAO for the governor's own apply, `hlwatch finish` without a yield gate, and a refusal proven by reading rather than a test — listed residues. M4 reads
this branch's head after the GV1 edits.

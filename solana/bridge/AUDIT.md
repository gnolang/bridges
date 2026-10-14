# The audit loop — top down and bottom up, until converged

Jae's order of 2026-10-13: audit until converged, then deploy; tell him when a step is his; make no assumptions —
verify every assumption the design rests on; and tell him whenever anything changes from what was discussed. The plan's own review
loop (REVIEWS.md, 22 rounds) covered the design; this loop covers the whole stack as built, and the design again
against what the code taught. One round = seven bounded audits run in parallel on the committed tree, read-only;
their findings are folded (plan edits by hand, code fixes by one agent per layer, every fix re-verified by the
suites and the end-to-end run); the next round reads the result. Converged when a round returns nothing above
medium in every audit. Every audit writes its findings to a file as it finds them and returns under 3,500 words,
at most 12 findings, each with an ID, a severity, the words of the plan or the lines of code, a scenario and the
smallest fix.

## The seven audits of a round

From the top:
1. **Plan red team** (PLAN.md): the attacks of REVIEWS.md's rounds, now with the code in hand — where the code's
   readings (M1-, M2-, M3-NOTES.md) changed a rule or filled a gap, is the plan still honest and buildable.
2. **Plan fact check**: consistency, numbers, terms, the NOTES' readings against the plan's words, what a member
   still refuses.
3. **Conformance** (plan ↔ code): for every rule of §5, §6 and §13, where it is in the realms, the programs and
   the Go, and every rule the code implements differently or not at all; every reading in the NOTES that the plan
   does not state.

From the bottom:
4. **The realms** (`realm/`): authorization and realm boundaries, the gno v1.2.0 semantics (crossing calls,
   origin calls, bankers, panics vs errors in GovDAO closures, block time) — audited against the Interrealm
   Specification v2 (`~/src/jaekwon/gno/docs/resources/gno-interrealm-v2.md`, its ADR `gnovm/adr/interrealm_v2.md`)
   and the gno security guide (`docs/resources/gno-security-guide.md`) rule by rule, with the review method of
   `docs/resources/gno-ai-contract-review.md` and the interrealm checker `kourt/scripts/check-interrealm.py` (Jae,
   2026-10-13: "make sure you audit with gno-interrealm-v2 and security-guide.md in mind"; a focused pass X1… ran
   between rounds 2 and 3, and every realm audit from round 3 on reads them first), arithmetic, the meters and queues, the
   flags and seats, the at-once rules decided in the executing transaction, the void/restore/credit state machine
   and the identity counters, `Process` and `Dispatch` against §13, storage and gas against floods, the tests'
   blind spots.
5. **The Solana programs** (`bridge/programs/`): account ownership, signers and PDA seeds on every instruction,
   the precompile introspection, the header and merkle code against tm2, the commit rule and its arithmetic,
   staging and rent, the quarantine, `holdSince` and settlement, the admin program's lifecycle (prefix, sequence,
   clocks, floor, flags and holds, cancels with their anchor, salt and record, the recovery path, kinds 8 and 9,
   `finish_setup`), the pausable ISM's `Verify`, the state layout pin, fail-closed error handling, compute, the
   clock-factor feature, the tests' blind spots.
6. **The Go side** (`bridge/`): keys and secrets, transport, provider agreement, the identity and the ordered cut,
   the signers' rules, the relayer's and executor's loops (idempotence, parking, crashes, state files), the
   updater, the monitors' flags, `hlwatch`, `hlsol setup`'s binding order and resumability, `hlsol audit` against
   §5.8's list, races, the e2e's coverage.

Across both:
7. **Assumptions** (every assumption the plan and the code rest on, stated in §2 or not): each named, with the
   source that settles it (the chain, the pinned source at its tag, the live RPC, the protocol's own documents),
   the check made, and a verdict — verified, false, or unverifiable and why — and what changes if it is false.
   The trust statement's assumptions first: tm2's header hash, sign bytes and commit rule; gnoland-1's software,
   chain id and governance as deployed (which r/gov/dao, its threshold and executor semantics, r/sys/validators'
   events and timing, the keeper's `private` rule); gno v1.2.0's semantics the realms use; Solana's rent, precompile,
   loader, CPI and Clock behaviour; Hyperlane's mainnet programs' layouts and behaviour; Alpenglow's and the SIMDs'
   status; the economics' inputs.

## Changes from what Jae was told

(appended whenever a fold changes a rule, a number or an ending he discussed or was told; each line says what it
was, what it is, and why)

Round 1 (2026-10-13, plan v29):
- The admin program now records the settled validator set itself (a permissionless `settle`), because the client
  keeps four history entries and four rotations under a hold could evict the set the recovery signs with — the
  validators' recovery was unsignable in that case, so the leak ending was a capture on day 21.
- A replacement's 21-day floor is computed from a recorded hold history, not written by an `Execute` call, because
  the only executor was the operator's and the operator may be the holder.
- The pausable ISM bounds what a message and a day can mint (25,000 a message, 100,000 a day per token at launch,
  GovDAO-settable by a new kind 12), because nothing on Solana bounded a forged mint: one forged delivery could mint
  ten times the lock in the minute before the flags. Honest inflow above the day's bucket waits for the next day.
- The signers count burns in flight as supply and evaluate the identity at their node's head when the client runs
  ahead inside the allowance, because a forged mint burned back toward gno.land stopped nobody.
- A next validator set is staged in chunks; the first build fit about nine validators in one transaction, not the
  hundred the plan claimed (the tenth validator would have frozen the client).
- A successor admin program receives its counters and seats live at the transfer (`successor_init`), because init
  data at least 7 days old wedged the cure by kind 9 after a flood.
- The growth step of a program upgrade uses the loader's `ExtendProgram`: the checked form the code sent never
  shipped on mainnet (the test harness still had it).
- The recovery's signing tool is per backend: a YubiHSM signs outside tmkms, a key file signs directly, a Ledger
  cannot sign a recovery; the assent letter asks each validator which backend holds its key.
- A GovDAO proposal past its deadline is denied for good only under `ExecuteOrRejectProposal`; the tools call it.
- Smaller rules stated: the meters start empty at `Launch`; any signer-rule change waits 7 days; no proposal
  vacates more than two guardian seats; a credited amount above the lock is swept; a `payouts` flag and a void may
  name a range of claims; kind 10 is bounded; a reset comes last in a recovery; a trusted header is never an
  announcing one; the realms carry the clock factor; a message to a malformed recipient is swept.
- Numbers: the Solana state accounts cost ≈0.15 SOL as built, not 0.05; the updater's float lasts ≈4.6 months at
  15,000 lamports a header; and after the round's fixes the admin program grew by a third (397,640 B), so the six
  deployments' rent is ≈8.9 SOL — ≈9.0 after rounds 2–4's program fixes (the client 273,768 B, the admin program 399,632 B) — and **the ask is 8 SOL, not 7**.
- The validator-set size the client can follow is bound by the program's 32 KB heap at about 50 through any history
  (60 with a settled one, 100 for one change), not 255 — the plan said 255 and the build had said "about 100"; the
  heap frame is the next build item, due before gno.land nears 50 validators.
- A program upgrade's growth step is sent top-level by the executor, since the loader admits no extend by CPI.
- The pausable ISM's mint limit is bound to the delivery through the Instructions sysvar, because the mainnet
  mailbox build (unlike the current source) signs no per-ISM process authority.

Round 2 (2026-10-13, plan v30):
- The published tool executes: `hlrecover execute` lands any matured Solana decision — a replacement of the
  operator's own seats included — so no decision waits on the operator's relayer (the only executor as built).
- The sweep queue gets a void: a swept amount (dust, a realm address, a malformed recipient, an amount above the
  lock) is voided by its message id like a claim, restorable within 7 days, and a credit by id may replace it —
  a forged sweep entry used to keep the identity open, and the signers stopped, for the deployment's life.
- A voided claim leaves its recipient's queue (a restore re-inserts it), so a voided flood never stands in front of
  an open claim; the void that cured a flood used to stop every payout of the route.
- A `since:` range closes at the proposal's creation height and a range holding more than 1,000 voidable claims is
  refused (split by heights): the walk costs ≈0.4M gas a claim and a range past ≈7,000 could never execute.
- The migration is paged: the successor pulls 500 records a page, the lock with the first page; the one-shot export
  would have outgrown a block within months of normal use.
- The two per-message maxima are ordered: gno.land's must never exceed the pausable ISM's (a send between them
  strands), so a raise goes to Solana first (kind 12) and to gno.land after; r/hlgov's kind-12 builder refuses a
  maximum below the route's current one.
- Honest inflow above the day's bucket does not "wait for the next day" (round 1's words): the bucket drains
  continuously at the day cap per 24 hours from each charge, and a parked `OverDayCap` delivery is retried,
  oldest first, as soon as the bucket has room.
- "Any signer-rule change waits 7 days" is now in the plan's text (round 1 told it; v29 said only "lowers the
  threshold"); an addition of a signer waits too.
- A reset (kind 11) clears the quarantine — its set is settled by definition; v29's "no decision shortens it" stands
  for every other kind.
- The recovery's power moves at `settle`, not at settlement: `hlwatch`'s SETTLE DUE is a standing order for either
  outsider, and the monitors alarm it hourly.
- The validator-set bound gets its watch: `client:set-bound` at 40 validators (the heap frame's trigger).
- Seat removals name the key held at creation and skip a changed occupant; `sync sequence` is bounded by 2^32.
- Corrections of round 1's numbers: the admin program grew by a sixth (337,576 → 397,640 B), not a third; the
  updater's float lasts ≈3.4 months, not 4.6 (four transactions a header at six validators), its fees ≈0.086
  SOL/month; the state accounts ≈0.15 with the admin's at 0.035; a pending decision holds 0.0014–0.002, measured.
- Smaller readings stated: `Verify` takes five of the pausable ISM's accounts; the watch key re-asserts once a
  minute; r/hlgov's address is a refused recipient; the client-ahead and header rules flag after two minutes; the
  client's "settled" is by the Clock alone; the hold history merges the two oldest; the high-water mark defined;
  kinds 8 and 9 are checked by the proposer and `hlwatch` ("immutable" / "MUTABLE"); a sweep payment or refund
  waits under a `payouts` flag; a commit is one round; a stranger's growth never empties the ring; a proof header is
  recorded once; a reset with a bad chain id is spent; a retired predecessor answers `Retired`.
- From the assumptions audit: the pausable ISM's day cap is tied to the lock — at launch the smaller of 100,000 and a
  tenth of the lock the launch notice expects (§11.11 now asks Jae for that figure), moved by kind 12 as the lock
  grows — because 100,000 a day was an absolute number against a lock of any size; a program upgrade's growth is one
  extension a block of the whole excess (the 10 KB step was a choice, not the loader's rule); the staging chunk is 23
  validators and a commit's precommits are staged in chunks, since the first build's transactions would have
  overflowed Solana's 1,232-byte packet past 17 validators (latent: the set is six); the relayer reads the chain's
  gas price before each call and takes its gas from a simulation, re-pricing an "insufficient fees" refusal
  instead of parking (gnoland-1's price is dynamic above its 0.001 floor); Solana's slot is 0.22 s today, so every
  slot figure of scale was 1.8× off (no rule reads one); the public gno node's `.app/version` answers `dev` — the
  running commit is unknowable from outside and the assent letter asks each validator its version; the letter's
  question on signer backends is now verbatim in §11.10, with the known signable power (120 of 360).


Round 3 (2026-10-13, plan v31):
- The monitors judge the identity by the signers' exact rule (the in-flight amounts added to the right side), and a
  credited id neither provider's index holds flags at once — as built they tolerated a forged credit up to what was
  in flight, tens of thousands under a hold.
- A large send parked for the day bucket reserves its room against younger sends, so it never starves behind a
  steady flow.
- The sweep queue is settled by message id (the paid entries leave it), because a payment by amount left the entry
  live and a credit by id could pay it a second time with the identity reading closed.
- The migration pauses both realms from the first page to the last, each page carrying the recipients' counters, so
  a credit between pages can neither wedge nor collide; a successor announces itself first, so a mistyped path cannot
  strand the lock; a hand credit is never above the lock, and a turn above the lock skips instead of reverting.
- The recipient switch to a successor is proposed only once hlwatch shows the kind 9 executed (proposed together,
  a hold would deadlock GovDAO's own channel), and a message that reached a retired program is re-dispatched, not
  parked.
- A signer removal the rule cannot survive is refused when made (v30 said it waits 7 days; the realm refuses it);
  the slow path is a rotation naming a rule the remaining set meets.
- The range cap counts every credit the scan visits and the note names the last height voided; a seat removal of any
  kind names the holder at creation (the rotate-seats path had missed the rule).
- A growth's rent is a number of the vote (hlwatch shows the sizes), `hlrecover execute` refuses one above half a
  SOL without the member's say-so, and the rent is read live (the compiled 6,960 was the test validator's rate).
- The launch day cap is a tenth of the lock expected in the first week — the pool's seed — and the monitors alarm
  `mint:cap-ratio` when a cap exceeds a fifth of its lock; one launch value serves both tokens (the smaller), kind 12
  moves each after.
- The gap rule works again (a complete scan stands at the snapshot) and a transient read error no longer makes a
  permanent foreign-decision flag; the flag waits two minutes like the record's.
- Corrections of my numbers: the day-bucket attack costs capital (100,000 GNOT locked per day of delay), not 5,000
  GNOT of fees — four sends at the maximum fill it for 20 GNOT; the compute table is M2e's (a 6-validator update
  ≈100–120k CU over four transactions; kind 11 123–170k); an epoch is ≈26 hours (four slot-time features, 200 ms
  nominal since 2026-10-08); the token bytes date from 2026, the mailbox's from 2024; 1,232 bytes is the legacy
  transaction's limit (a v1 transaction carries 4,096 since 2026-09-15; a later build may use it).
- Stated: a burn is signed only after a verdict whose snapshot passed its slot (one round's wait); a block of more
  than 128 results delivers by the ring or after one Reemit; the Clock's bound; SETTLE DUE is a standing order in
  the launch notice; the watch key re-asserts at most once a minute; the predecessor's deposits go to the
  successor's caller page by page; a credit names one id; the registry is the registrant's own identity, listed
  twenty at a time, and the member checks a verifier's declaring realm.

Round 4 (2026-10-13, plan v32):
- The scanners read every transaction version Solana serves: every transaction read was pinned to version 0, so
  one v1 transaction (live on mainnet since September, free to send) would have blinded the fork and gap rules,
  muted the index rule, refused `flag -ids` and stranded a burn sent that way — a one-constant fix the audits
  reached only now.
- A migration no longer strands the burns made before kind 6 enrolls the successor: hlmailbox lets the successor take
  a message addressed to its predecessor once its import has ended (they wait meanwhile), the watch judges a
  migrating token over the pair's summed counters instead of flagging the move as a forgery, the predecessor's
  standing flags move with the lock, and a successor announces the route it succeeds from its own init — a
  stranger's act for the price of a deployment, so the member reads its source and its gnomod for `private`.
- A refund is a successor's duty for the sends it imported, never a live route's: a refund voids nothing on Solana,
  so a stranded send waits for a kind 12 instead; the monitors read the `refunded` mark and drop it from in flight
  (they read every refund as a break before), and the relayer refuses to deliver a refunded send.
- A lowering kind 12 never holds a token's flow behind a parked send that no longer fits; a cancel's delivery passes
  only as many pending pairs as the packet carries (seven with distinct deliverers overflowed it — a cancel of a
  flood would never have been recorded); r/hlgov's body bound is 512 bytes (a legacy delivery carries 538; 600 sat
  above it).
- From the block a kind 9 executes the tools follow the admin program's successor read from the chain — decisions,
  the foreign check, the pending list, the one-stroke flag on both states — rather than a config edit each outsider
  would have to make inside the leak's week.
- A stalled record scan and a failing monitor poll alarm within two minutes (both were silent); a fresh install
  starts from a checkpoint of the client's record.
- `mint:cap-ratio` is quiet while a token's lock is still growing toward the first-week expectation it was set
  against (it would have stood from launch for months); it alarms once a cap exceeds a fifth of a lock that reached it.
- A kind 9 to a successor that can never inherit (uninitialised, another's, inherited already, or a former
  recipient) is spent or refused, not pending for good; a retired predecessor opens no recovery stage and closes
  what pends there; a growth over an empty ring keeps the ring's capacity.
- Stated: a signer removal the rule cannot survive is refused when made (§11.14 and the drill said 7 days); the
  flags land within two minutes, not one, in the leak walk and §9; a node's own minimum fee is paid once under
  `gno.maxNodeFee` (ten times the block-price fee or 1 GNOT) and parked above it; the sweep payment's refusals; a
  list skips an id no longer voidable and a dry lock skips; GovDAO's hand credits land during a migration's pages;
  the ring epoch; 252 validators in a reset's account, not 255; the four tools, `hlrecover`'s build hash pinned;
  `Claim` is built; the header proof's 45k CU with a full ring; the unindexed credit flags on the second poll.

Round 5 (2026-10-13, plan v33):
- A refund is ordered after kind 6: r/hlgov's kind-6 closure records the enrolment in hlmailbox and a refund is
  refused until that record is 7 days old — a refund landing first (a hold's lift gives a day in which kind 6
  cannot land) let a stranger's re-emit mint the refunded send twice with nothing to close the identity; a `write
  off` builder books such a send as lost; hlwatch alarms a refund proposal for an unenrolled successor and prints
  DELIVERED beside a processed id.
- A successor migrates on only after its import has ended, and a predecessor whose successor pulled nothing
  accepts a second migrate that re-points it — before page 0 nothing has moved, yet both doors were one-way, so a
  successor migrated on, or one that never pulled, stranded the lock for good.
- An un-inherited successor admin program is inert — it records, settles, stages and executes nothing until the
  hand-over — because it ran its own `Settle` and recovery path outside the predecessor's hold, where a decision
  could pend before the hand-over and execute with the successor's authority after it; hlwatch checks a
  successor's pending decisions, recovery counter and state layout before the vote, and the hand-over's wire
  contract is pinned (the state's layout append-only, the hand-over instruction byte-identical).
- The dispatch index starts at a checkpoint as the record does, so a fresh install on a provider without full
  history no longer mutes the forged-credit rule for good; a checkpoint's base is refused when the own node's block
  at the trusted height differs from the client's header; an outsider's first run starts from the deploy.
- The gone-decision rule flags only while no recovery window stands and no hold overlaps the decision's week —
  it would have called the validators' own cancel a foreign decision in the middle of a recovery.
- The one-stroke flag's pausable half never fails with the admin program's line of succession, which a forged
  successor could make unreadable.
- A successor credits nothing before its first page (a burn addressed to it was swept against an empty lock); the
  predecessor's flags move with the lock; refunded sends travel in the pages; a successor starts with empty meters.
- The launch per-message maximum is the smaller of 25,000 and the day cap, so a small first-week lock cannot
  compile a cap below the maximum and strand every send between them from launch day; `hlsol setup` refuses the
  mismatch.
- The cap-ratio alarm's quiet expires 14 days after `Launch` and whenever the lock reaches five caps, so a pool
  that never fills, or a lie in the config, cannot keep it quiet.
- The tools follow a route's line of migrations as the realm does (sixteen deep), not one hop.
- The outsiders' provider accounts and node are in their own names, reimbursed — never accounts the operator
  could let lapse in the month it is the party under suspicion.
- Stated: the cap-ratio alarm's quiet is the operator's config figure and the notice says it; a checkpointed watch
  audits nothing before its base; the ring's growth rule (the capacity kept, the free slots filled first); the
  transfer's compute 169–171k; one flag and one closure cover a flood of up to 1,000 credits; a queued range the
  pages overfilled stays pending until cancelled; the relayer refuses a refunded send; a cancel's dropped pairs
  return their rent by `close cancelled`; the chain of predecessors is sixteen deep and a route migrates once; the
  400,000-CU budget; the Go lane's round-4 notes written.
- A checkpoint over a wrapped ring mirrors the program's evictions (it took the ring in slot order and evicted the
  wrong entries for 256 pushes, a false pause of both objects); the checkpoint's stage seed is three hours (it was
  18 minutes at today's slot, and a resumed stage can be older); the monitors read an unprocessed send once, not
  three times a poll (a 500-send backlog made a poll minutes long); running past the tools' hop bounds alarms.
- Stated: Alpenglow's calendar as Anza publishes it (October 2026 with Agave 4.3, the gate still absent); the
  successor's `Pull` imports a page and takes its lock in one call; the mailbox decision's bound in the notes.
- The relayer takes a send from a route's successor as its own (after kind 6 enrolled the successor as router, every
  send through it was classified "other", walked past and never delivered — locked on gno.land with nothing minted
  and no refund, since a live route refunds nothing); the three signers judge the identity over the pair as the
  watch does (they halted on the configured route alone from page 0); `hlrecover version` prints the build hash
  the launch notice pins (only hlwatch did); hlwatch's cap-ratio alarm carries the plan's key.
- Left as found, named for round 6: r/hlgov's kind-6 builder checks that the route it enrols for gno.land's domain is
  a registered route, not that it is the token's own — GovDAO's vote and the proposer's `hlwatch` line stand in front.

Round 6 (2026-10-13, plan v34):
- A refund's gate is Solana's enrolment, not the realm's note: the note's 7 days always expired before the admin program
  executed the kind 6 (seven days after delivery, deferred by a flag or a hold), so a refund could apply while the
  predecessor was still the router and a stranger's re-emit minted the send twice — the realm now waits 14 days and a
  cancel clears the note, `hlwatch` reads the token's router on Solana and the kind 6's state (NOT ENROLLED ON SOLANA on
  the proposal and on the queued refund), and the relayer executes no refund while the router is not the successor.
- A re-pointed-away successor can never be Solana's router: a successor takes no send before its last page is in,
  kind 6 enrols a successor only once its page 0 is in (and only the token's own live route), and a dropped route is
  inert — the mailbox remembers it, refuses its `Dispatch` and `Process`, and the line's last realm takes a message
  addressed to it. Before, a dropped successor could mint against a lock no tool counted and strand burns for good.
- The relayer holds a successor's sends while a route's line is unreadable for a tick instead of marking them "other"
  for good; `hlwatch` and the relayer list every realm of a token's line, so a successor's refund proposal, migrate and
  queued decisions are checked (they were invisible with the config on the predecessor).
- A checkpointed install trusts the claims credited before its checkpoint instead of flagging every waiting honest claim
  as forged on the second poll (a false pause of both sides); the checkpoint's header is compared every poll and a
  difference strokes the flag; the index is seeded from the mailbox's dispatched-message accounts where a provider
  serves them.
- A write-off has a restore; a burn to a successor's address is swept, not paid into the realm's account; the two
  maxima are compared live (`limits:mismatch`); a failed stroke prints as failed; the hop bounds alarm.
- The reference successor realm hlgnot2 exists — hlgnot's code at another path with a permissionless `Adopt` — and the
  migration (announce, migrate under a flag, every page by a stranger, kind 6, the 14-day refund, a write-off and its
  restore, a re-point refused after page 0) runs end to end in its suite; the devnet drill is M4's.
- The admin program: a mailbox decision never reads the client (a client at another layout version bricked every
  corrective decision); a kind 9 is spent unless the successor's clock factor is this build's; the hand-over sweeps the
  predecessor's authority float to the successor's PDA.
- Stated: the launch per-message maximum one way (the smaller of 25,000 and the day cap); `lost` has a second source;
  the cap-ratio quiet's third expiry; the gone-decision rule's wording; the layout contract's appended field; the
  deploy's token inits under 1,000,000 CU; no head count as a fact of the design; README's hlrecover row.

Design change (2026-10-13, plan v35):
- The validators' recovery path is removed, on Jae's word ("validators should do nothing than validate"): no
  recovery channel, stages, digest, counter, window, lapse rule, settled set or `Settle` in the admin program, no
  reset of the light client (its controls are its upgrade authority and `SetAdmin`, the admin PDA's), kinds 10 and
  11 reserved, `hlrecover` cut to a members' tool (`version`, `status`, `body`, `execute`); every change on Solana
  is a light-client proof of a GovDAO decision, and the validators are asked nothing (no assent letter, no
  key-backend question).
- The lapsed-client ending is now stated plainly: a client that can no longer verify gno.land (a halt the chain
  never resumes from under its last set, a set change it cannot follow, a fork, a verification bug) ends gno →
  Solana for good — every send and every decision needs a proof, the kind-9 hand-over included — while burns back
  keep working on the signers; GovDAO's exit is a fresh deployment (new programs and synthetic tokens, the old ones
  redeemable only by burning back; the rent, the pool's seed and the launch procedure again).
- The leaked-set ending is no longer a stand-off kept up by the validators' cancels: nothing on Solana tells a
  forged header from a real one, the brakes are the 7-day delay, the guardians' flags, the hold's 21-day floor
  (kept, its rationale now GovDAO's own cancel) and GovDAO's cancel per forged decision or range under the honest
  chain's headers, which the forger answers by taking sequence numbers first; the sane end is a rotation on
  gno.land and a fresh deployment, and without a hold a forged replacement of the guardians lands on day 7 together with the forged kind 12, 8 and
  9 of that week, so the day caps per token bound the first week alone and the side is then the thief's (the
  guardians' standing order: two flags on day 0).

Design change (2026-10-14, plan v36):
- The bridge launches under one governor, not under GovDAO, on Jae's word ("I want to change this so I can deploy
  things slowly and test it out. Until I yield to GovDAO I want to be GovDAO alone"): each realm compiles a governor
  address, and until his one-way `Yield` on hlmailbox (read by every realm) every decision — every builder of §5.3,
  `Launch`, the verifier's activation, r/hlgov's decisions to Solana — is his direct call through the realm's
  `Govern` entry, with the same closures, checks, queues and 7-day delays; GovDAO's proposals are refused before the
  yield and are the only way after it; `Execute(id)` stays anyone's, the apply-refund rule stays the governor's
  at-once act, and the Solana side is untouched (the admin program checks that r/hlgov sent a decision, never who
  decided).
- The trust statement until the yield is one person: Jae alone can change any setting, within the delays, and move
  the lock after 7 days; the four guardian seats may be the operator's until the outsiders are named, which is at
  the yield, when the launch notice goes out; the caps and brakes still apply to his decisions (a 7-day decision of
  his waits 7 days, two of his own guardian flags hold it); a leaked governor key before the yield is a leaked lock
  unless the yield comes inside the thief's first decision's 7 days (the yield, one-way and at once, is the one
  cure, since the key is fixed by the deploy — the cold key g1ecsu… for any deployment holding real funds), so the
  lock stays small until the yield and he yields before inviting real holders.
- The launch vote is two things now: his `Launch` on a checklist published with the deployment (§5.9's gates, §8's
  checks, made by him), and GovDAO's yield vote — its acceptance of the bridge as it stands, the same gates read by
  the members, the outsiders seated 7 days before by his own rotation, on which he yields by his own call; §10's
  dates follow (the mainnet deploy about mid-December 2026, the yield vote about February 2027 with the audit
  first), and a fix to the admin program found by an audit after the mainnet deploy is a fresh deployment, its rent
  again.

## The rounds

(appended per round: the date, the tree's commit, each audit's count above medium, what was folded and where)

Round 1 — 2026-10-13, on be69e1c (plan v28, the code of M1–M3 at v27). Above medium: plan red team 2 (AN1 the
settled set evicted; AN2 the signers and monitor blinded by a client inside the allowance), plan fact check 2
(C226 the same eviction; C227 the successor's stale counters), conformance 0 (K1–K3 medium), realms 0 (R1, R2
medium), programs 2 (S-01 the eviction; S-02 the staging limit), Go 4 (G1 a stale stage strands messages; G2
staging under a pause drains the float; G3 a catching-up node flags both objects; G4 a false taken-number report),
assumptions 1 (F1 `ExtendProgramChecked` absent on mainnet; F2 and F3 medium). Folded: plan v29 (every rule above
and the §13 encodings); code by M2d (programs), M1c (realms), M3d (the Go and the e2e), each verified on the merge.
The findings files: scratchpad audit-r1-{redteam,factcheck,conformance,realms,programs,go,assumptions}.md, copied
to bridge/audits/r1/.
Open for round 2 (found while fixing): the sweep queue has no void — a forged entry there (dust, a realm-address or
malformed recipient, an above-lock credit) keeps the identity open by its amount, since a sweep payment moves real
coins and cannot drop it; a range void's gas for a thousand claims is unmeasured; restore and credit stay lists.

Round 2 — 2026-10-13, on 22d28c8 (plan v29, the code of M1c, M2d, M3d and M3e). Above medium: plan red team 0 (AO1
no published tool executes a matured decision, AO2 the sweep queue's missing void — both medium), plan fact check 0
(C256–C283), conformance 0 (K13 hlwatch never alarms on an identity break, K20 the sweep void — medium; K8, K9 and
half of K10 were round-1 wording never folded), realms 2 (R11 the sweep void; R12 voided claims as tombstones stop
every payout; R13 the one-shot export and R14 the range void's gas medium), programs 0 (S-08–S-12 low), Go 2 (G13 a
signer signs a burn above its verdict's snapshot; G14 the monitor's record scan runs ahead of its snapshot; G15–G18
medium), assumptions 0 (F6 the packet limit, F9 the gas price and F10 the unstated lock medium; F7, F8 low; U1 and
U4 unverifiable from outside). Folded: plan v30 (every rule and number above); code by M1d (the realms: the kind-12
builder, R11–R18, K19, K22), M2e (the programs: S-08–S-12), M3f (the Go: G13–G21, K13–K16, K21, AO1, AO3–AO5, F6,
F7, F9), each verified on its merge (M1d 1d216b0, M2e+M3f 27275fc, M1e 777ceae), and the whole tree's e2e from
nothing passed in 729 s on 777ceae. The findings files: bridge/audits/r2/.
Round 2x — 2026-10-13, on 9ef5f53 (the realms of M1d): the focused pass against the Interrealm Specification v2,
its ADR and the gno security guide, rule by rule, with the review method and the repo's interrealm checker. Above
medium: 0 (X1 medium — `RegisterVerifier` accepts a sub-realm identity, so one realm registers verifiers without
bound and the mailbox's json and page walk them all for good; X2 low — `satisfiable` hands its own key slice to an
unchecked interface implementation). The checker: the hl* realms and p/ clean; one hit outside the bridge
(realm/bubbletreasury's `Do`, filed as work row `interrealm-treasury` for the lanes). Folded by M1e (the realms).
The findings file: bridge/audits/r2/audit-r2x-interrealm.md.

Round 3 — 2026-10-13, on 777ceae (plan v30; the code of M1d, M1e, M2e, M3f). Above medium: plan red team 0 (AP1 the
monitors' tolerant verdict and AP2 the unreserved bucket room, medium-low; AP3–AP7 lower), plan fact check 0
(C284–C312), conformance 0 (K23–K26 low), realms 0 above medium (R19 the sweep payment by amount, R20 the paged
migration's collision — medium; R21–R26 low), programs 0 (S-13–S-16 low), Go 0 above medium (G22 the dead gap rule,
G23 the permanent foreign flag — medium; G24–G27 low), assumptions 0 (F11–F16 low; U2 settled: the suites run on the
v1.2.0 module; U8 bounded at 29 accounts). Folded: plan v31 (every rule and number above); code by M1f (the realms:
R19–R26, AP4), M2f (the programs: S-13–S-16), M3g (the Go: G22–G27, AP1–AP3, AP5, AP6, F14, F15, K26), each verified
on its merge (M1f 9c73f0f; M2f and M3g da9567d), and the whole tree's e2e from nothing passed in 1,071 s on
da9567d. The findings files: bridge/audits/r3/.

Round 4 — 2026-10-13, on da9567d (plan v31; the code of M1f, M2f, M3g). Above medium: plan red team 0 above medium
(AQ1 a migration strands the burns made before kind 6 enrolls the successor, AQ2 a refunded send stays deliverable
and reads as a break — medium; AQ3–AQ6 lower), plan fact check 0 (C313–C341), conformance 0 (K27–K33 low), realms
0 (R27–R30 low), programs 0 (S-17–S-19 low), Go 1 (G28 every transaction read pinned to version 0 — high; G29 a
stalled scan and G30 a failing poll silent — medium; G31–G35 lower), assumptions 0 (F17–F20 low; U10 new:
rpc.gno.land's own minimum fee, settled by one probe of Jae's). Folded: plan v32 (every rule and number above); code
by M1g (the realms: R27–R30, AQ1, AQ2, AQ5, F18), M2g (the programs: S-17–S-19), M3h (the Go: G28–G35, AQ1–AQ4, AQ6,
C341, F19, K28), each verified on its merge (M2g a7736a8; M1g 75a1562; M3h b24c9e9), and the whole tree's e2e from
nothing passed in 872 s on b24c9e9. The findings files: bridge/audits/r4/.

Round 5 — 2026-10-13, on b24c9e9 (plan v32; the code of M1g, M2g, M3h). Above medium: plan red team 0 above medium
(AR1 a refund landing before kind 6 lets a stranger's re-emit mint the send twice, AR2 a successor migrated on, or
one that never pulled, strands the lock — medium; AR3–AR7 lower), plan fact check 0 (C342–C375), conformance 0 (K34
the relayer marks a migrated route's successor's sends "other" after kind 6 and never delivers them — medium;
K35–K37 low), realms 0 (R31–R34 low), programs 0 (S-20 an un-inherited successor settles and stages outside the
predecessor's hold — medium; S-21 low), Go 0 (G36 the dispatch index has no checkpoint — medium; G37–G45 lower),
assumptions 0 (F21 a checkpoint over a wrapped ring evicts the wrong entries, F23 the monitors read every
unprocessed send three times a poll — medium-low; F22, F24–F26 low). The written rule — nothing above medium in
every audit — is met for the first time; the five mediums change code in every layer, so round 6 reads the folded
tree before the loop is called converged. Folded: plan v33 (every rule and number above); code by M1h (the realms:
R31–R34, AR1, AR2), M2h (the programs: S-20, S-21, F25), M3i (the Go: AR1, AR3–AR6, G36–G45, S-20/S-21's tools,
F21–F24, K34–K37), each verified on its merge (M2h 5e5ddd0; M1h e003ee7; M3i f769e12), and the whole tree's e2e
from nothing passed in 730 s on c89f315, the tree f769e12 merges. The findings files: bridge/audits/r5/.

Round 6 — 2026-10-13, on 95a7252 (plan v33; the code of M1h, M2h, M3i). Above medium: plan red team 0 above medium
(AS1 a re-pointed-away successor still Solana's router, AS2 the refund's gate r/hlgov's dispatch not Solana's
execution — medium; AS3 medium-low; AS4–AS8 low), plan fact check 0 (C376–C384; C381, C384 medium-low), conformance 0
(K40 the checkpointed index flags honest claims — medium; K38, K39 medium-low; K41, K42 low), realms 0 (R35 the dropped
successor — medium; R36 medium-low; R37–R39 low), programs 0 (S-22–S-24 medium-low, S-25 low), Go 0 (G46 an unread line
marks a successor's sends "other", G48 only the configured realms' proposals listed, G49 the checkpointed index —
medium; G47, G50 medium-low; G51–G53 low), assumptions 0 (F27 the enrolment note's 7 days — medium; F28 medium-low;
F29 low). The rule met again; the mediums all at the seams of round 5's migration, refund and checkpoint fixes, found by
three or four audits each. Folded: plan v34 (every rule and number above); code by M1i (the realms: R35–R39, F28, AS1,
AS2's realm half, AS5, AS6, C384 — the reference successor realm hlgnot2), M2i (the programs: S-22, S-23, S-25; S-24 made
moot by the design change below), M3j (the Go: F27, F29, AS2–AS4, AS7, AS8, G46–G53, K38–K40, M1i's and M2i's shapes),
each verified on its merge (M2i ab1444c; M1i 86d60b5; M3j 8f04e566). Then Jae's design change of 2026-10-13 — the
validators' recovery path removed — folded as plan v35 (0a4ae4a, 8c0872e), M2j (80ebfec: the programs) and M3j's last
part (the Go), and the whole tree's e2e from nothing passed in 731 s on f900c338, the tree 8f04e566 merges. The findings files:
bridge/audits/r6/. Jae's order of 2026-10-13 ("lets wrap this up"): the loop ends with a final check of two audits
(conformance and red team over the removal and round 6's fixes) rather than a seventh full round.

The final check — 2026-10-13, on 8f04e566 (plan v35; the code of M1i, M2i, M2j, M3j), two audits in place of a seventh
round (Jae: "lets wrap this up"). Conformance and regression: the removal complete and every kept rule in place with a
proving test, round 6's fixes at their lines; K43–K48 low (wordings, comments, the records), folded by hand (02ee372c).
Red team: nothing high; AT2 (a refund's apply was anyone's `Execute` while the Solana gate lived in the relayer's loop — a
kind 6 deferred by one flag let a refunded sender mint his send twice) and AT3 (a fresh deployment's admin program
started its sequence at 0 against r/hlgov's counter, so the exit both endings rest on could not be governed) — medium;
AT1 (without a hold the day caps bound the first week alone) — medium-low; AT4–AT8 low. Folded: the plan (469d35c2), M1j
(the realms: AT2's at-once `apply refund` builder with `Execute` refusing a refund, AT6, AT7, AT8; c738edfd), M2k/M3k (the
programs and the Go: `Init` carrying r/hlgov's counter with `hlsol setup -start-sequence` and the audit failing on a gap,
the relayers skipping refunds, hlwatch reading the apply proposal, AT8's texts; 57b47988); vet and race green, the whole
tree's e2e from nothing passed in 718 s on 0ffa726b, the tree 57b47988 merges. AT5 (hlwatch's own diverted-credit list) stays a listed
residue. The findings files: bridge/audits/r7/. The loop ends here: the written rule held from round 5 on, every medium
since was found at the seams of the previous round's fixes and folded with a proving test, and the tree at 57b47988
is what M4 (devnet) reads on Jae's go.

The governor and the yield — 2026-10-14, Jae's second design change ("until I yield to GovDAO I want to be GovDAO alone"), folded
as plan v36 (c58b977c), M1k (the realms: every builder returns a realm `Request`, `Govern` runs its closure for the compiled
governor until one `Yield` on hlmailbox, read by every realm, hands every decision to GovDAO — the same closures, queues and
delays; 83fcb9dd) and M3l (the Go: the views and hlwatch read who governs, "awaits the yield" beside a pre-armed proposal, the
relayers' finisher only after the yield, the e2e driving the launch and the first decisions by the governor, GovDAO refused,
the yield, and the rest by GovDAO; ee8a9074); vet and race green, the whole tree's e2e from nothing passed in 741 s on
the M3l tree. The closing check (one audit, GV1–GV4, bridge/audits/r8/): nothing high or medium; GV1 (medium-low) the plan's own sentence — a GovDAO proposal made before the yield is refused at execution, not at creation, so pre-armed proposals execute the block after the yield (folded: §5.3, §11.16's order, the relayers' gate stated); GV2–GV4 (low) texts that still say GovDAO for the governor's own apply, `hlwatch finish` without a yield gate, and a refusal proven by reading rather than a test — listed residues. The tree at this branch's head after the GV1 edits is what
M4 reads on Jae's go.

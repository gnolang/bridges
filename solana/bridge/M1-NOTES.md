# M1 notes — what the realms chose where the plan left room

The gno.land realms of milestone M1 (BUILD.md) are built to PLAN.md v22 §5.1–§5.5, §5.9 and §13, with the v23 and
v24 changes relayed while building (r/hlgov's list-or-range cancel, `sync sequence`, kind 5's four entries; the
void builder's unpause option taking the unpause builder's parameters — functions to clear and seats to vacate)
and v27's AM series (M1b: `Launch` under the 14-day deadline, `sync sequence` raise-only from a compiled start,
kind 4 reserved and refused, the salted cancel body), and the fourteen fixes of the audit loop's round 1
(AUDIT.md; the section "Audit round 1" below says what each changed and the reading chosen). Where the plan is
ambiguous or silent, this file says what the code does and why; it never changes the plan. Run the tests with
`scripts/gnotest.sh` (every package and realm) or `scripts/gnotest.sh hlgnot` (one); the local chain end to end
with `bridge/local/e2e.sh`.

## What is where

- `realm/p/decisions/v0` — the decision ledger every bridge realm keeps: numbering, the 7-day delay and the 14-day
  deadline by block time, cancel, the page (per kind, pending up to 100 and the last 100 executed) and its JSON.
- `realm/p/hlroute/v0` — the route state machine r/hlgnot and r/hlbubble share: sends, claims in per-recipient
  queues, the two meters, the sweep queue, the identity counters, the void/restore/credit/sweep/refund/migrate
  decisions and their at-once tests, the snapshot a successor imports. It holds no asset and makes no call: the
  realm supplies an `Env` of closures (flag questions, the mailbox's done marks, payments).
- `realm/hlmailbox` — messages out (§13's `Dispatch` event, `Reemit`) and in (`Process`, mark-then-verify), the
  verifier slot, the one signer set and its rule, the six seats and their flags, the routes' payouts epochs, the
  `Launch` builder and the mailbox's other builders.
- `realm/hlverifier` — the ed25519 verifier (threshold or two groups), registered at deploy.
- `realm/hlgnot`, `realm/hlbubble` — the locks, thin over p/hlroute; `realm/hlfees` — the fee realm and the
  treasurer; `realm/hlgov` — GovDAO's decisions as messages to the admin program.
- `realm/hlgnot2` — the reference successor realm of the GNOT route (M1i): hlgnot's code with the predecessor
  compiled in, announced from its init, a permissionless `Adopt(page)` that pulls and imports a page in one call.

## Readings and gaps, by section

### §5.1 hlmailbox and the verifier

- **One signer set, not one per origin.** §5.3 calls it "the only signer set there is"; `Satisfiable(signers)`
  carries no origin. hlmailbox keeps one set and one rule; the active verifier is per origin (one at launch).
- **The rule lives in hlmailbox** (`Rule{Threshold}` or `Rule{GroupA, GroupB}`, §11.13's "one of A and one of
  B"); the verifier reads it at every check. `Satisfiable` judges a candidate set against the rule hlmailbox holds
  *now*; a rotation that changes the rule is checked structurally at creation (threshold within the set, groups
  non-empty, disjoint and within the set) and waits 7 days, so no at-once path ever needs the new rule.
- **Rotate signers at once** only as a pure removal: the new set a strict subset of the old, the proposed rule
  equal to the live rule *pruned of the removed keys* (the same threshold — a 2-of-3 rule is still met by the 2
  that remain; the groups less the keys that leave, compared as sets — so under §11.13's rule removing a
  compromised group member is at once, while removing a partner, a group's only member, empties a group no
  proposal can name and so never is; R2), and the active verifier's `Satisfiable` true in the executing
  transaction. Everything else — an addition, any rule change (a raised threshold too: the plan's instant list
  names only removals), a removal the rule does not survive — waits 7 days. A proposal that changes nothing is
  refused. **A queued rotation keeps the signer set and rule it was decided against** (`oldKeys`, `oldRule` in its
  params, copied when the proposal executed) and `Execute` applies it only while they still stand: one made
  stale by a change in between — a key cut at once on day 3 — panics and stays pending until GovDAO cancels it
  and proposes afresh, so an older addition can never re-admit a removed key on its 7th day (R1). A queued
  `rotate seats` keeps all six seats the same way (the simplest reading of "the seats it was decided against":
  any seat change in between, a removal elsewhere included, makes it stale).
- **`Verify` checks every signature in the metadata** (increasing signer order, each naming a signer) and then the
  rule, rather than Hyperlane's "first threshold signatures only": a bad extra signature refuses the message. The
  relayer sends exactly the signatures the rule needs.
- **`RegisterVerifier` from a realm under `gno.land/r/`, once per path**; a second registration from the same
  path is refused (a realm is immutable, so a re-registration could only be a bug).
- **`Dispatch` stays open to any realm**; `Reemit` is a crossing function so its event carries hlmailbox's path.
  The event carries exactly §13's seven attributes (no `root`); the merkle root the Go signers of the local run
  still need stays in `Render("msg/<nonce>")`.
- **Domains.** The launch Solana domain is Solana mainnet's (1399811149) on gnoland-1 and the local validator's
  (1399811150, Hyperlane's "solanatestnet", what e2e.sh runs) on every other chain; devnet (1399811151) is a
  constant change for M4.

### §5.3 the builders

- **No free text at all.** A builder takes parameters only; the proposal's title is "realm: kind" and the
  description is the parameters as the builder wrote them (the plan says the title is GovDAO's own field; with no
  free text in the call there is nothing else to put there).
- **The closure's errors.** The only error a closure returns is the 14-day deadline (GovDAO then denies the
  proposal for good); everything else panics, so the proposal survives a retry. The deadline is by block time
  (`time.Now()` at execution against the builder call's time), as the 7-day delay is.
- **A failed at-once test queues; a failed unpause is spent** — as the plan says. The unpause builder refuses one
  that would clear nothing *for the functions it names* on the route at the time it is made; a seat removal alone
  does not count as "something to clear".
- **Rotate guardians and watch keys** uses six numbered seats (1–4 guardians, 5–6 watch keys) as Solana does; a
  change is `seat:address` with an empty address vacating the seat. A proposal of removals only applies at once;
  one with any replacement queues whole (a removal and a replacement wanted on different clocks are two
  proposals). A removed or replaced seat loses its flags; a payouts flag among them restarts the route's meters.
- **Flags are keyed by seat**, not by address, so a replaced key's flags cannot outlive it. A guardian re-asserting
  its payouts flag replaces what it names (the monitors re-assert with a fresh time on every poll). A watch key's
  payouts flag may name nothing. **What a payouts flag names is a list of ids (capped at 256) or a range**
  (AN6; `hlroute.Range`, one grammar for the flag and the void builder): `heights:<from>-<to>` — every claim
  credited at a block height from..to inclusive, whoever credited it — or `since:<from>` — every claim the
  verifier credited at a height at or above from, open above while the flag stands, so the verifier's later
  credits are covered too; a hand credit (`ByVote`) never. Every claim carries the height it was credited at
  (`Claim.Height`, in `claim/<id>` and the page) and the route indexes its claims by height, so a range walks only
  what it covers. `hlmailbox.Flagged(route, id, height, verifier)` answers by list or by range.
- **The void is one decision with a split**: the claims a guardian's standing payouts flag names — by id, or by
  a range covering the height they were credited at — are voided in the executing transaction; the rest stay in
  the same decision, queued 7 days with only those (the list of ids left, or the range), and `Execute` applies
  them. **A void may name a range too** (AN6; the same two forms): at execution the range is closed at the current
  height (a `since` range, or a height range reaching past it — claims credited after the vote were not decided
  on; the frozen range is what the queued decision carries and what `Execute` walks again), the builder refuses a
  range that covers no claim the void can take when it is made, and a claim in the range the void cannot take as
  it stands (forged already; withheld or paid in full, for a void as withheld) is skipped and counted in the
  note, not refused — a list names each id and is refused for one. A claim voided at once and restored before the
  queued remainder lands is voided again by it (GovDAO cancels the queued void first if it means the restore);
  restore and credit stay lists (256). A range void walks every claim it covers in one transaction: GovDAO sizes
  the range, and the gas of a thousand-claim void is unmeasured (a gap for M4). The unpause option (v24: the
  unpause builder's parameters — the functions whose flags to clear and the seats to vacate; **two guardian seats
  at most among them**, §5.3's cap as `rotate seats` has it, R3) is applied at execution, after the at-once
  voids, whatever the split: GovDAO asked for it in the same closure as the void, which is the point. A seat
  vacated this way loses its flags like any removal (a payouts flag among them restarts the meters); the builder
  refuses an empty seat at creation and the closure skips one emptied since. hlmailbox exposes `ClearFlags(fn)`
  and `VacateSeat(seat)` to the route realms for it. A void of a claim already paid in full is allowed as forged
  (its paid part becomes lost, which is what closes the identity) and refused as withheld.
- **Restore** is the same split: withheld ids at once, forged ids queued.
- **Credit by id for a voided claim** ("restores it and then replaces the restored remainder with the recipient
  and amount GovDAO names, at the back of that recipient's queue, so the identity does not move"): the code
  restores, drops the old claim, and queues a fresh claim with the named recipient and amount (paid 0, so the fee
  is its first installment again). Owed moves by (amount − old remainder); the identity holds only when GovDAO
  names the remainder, which is the case the sentence describes. A credit for an id never done marks it done in
  hlmailbox (`MarkDone`, callable by the routes) and queues the claim.
- **The treasurer lives in r/hlfees**, with its own `set treasurer` builder (7 days), not in each route: one fee
  realm has one treasurer. BUILD.md's listing of `set treasurer` under hlgnot is read as §5.3's general list.
- **Refund by id** is a builder on the route itself (7 days, from the lock to the sender, the send record marked):
  the successor's cure for sends a migration stranded, and the same code serves the predecessor. Only sends never
  refunded qualify; whether a send was delivered on Solana is GovDAO's judgment.
- **Migrate** applies after 7 days by naming the successor and registering it as a route in hlmailbox
  (`AddRoute`, callable by routes); the successor then calls the predecessor's `Pull` from its own code, receives
  the lock (paid to its address) and a `Snapshot` it imports with `hlroute.Import` (tested in p/hlroute). The
  successor's source adds an import of the predecessor and an `Adopt(cur)` of three lines when the time comes;
  after `Pull` the predecessor refuses every function.
- **Set router** accepts any non-zero key; the old router's messages are taken for 7 days after the swap applies.
- **The maximum** must stay at or above the current minimum and the compiled launch minimum; the fee at or above
  the fee floor and **at most a quarter of the cap floor** (R10: a recipient's meter holds a quarter of the cap
  at most and the fee is the first installment, whole, so a fee above it would stall every new recipient's first
  payment with no flag and no alarm; the cap-floor builder refuses a floor whose quarter is below the fee the
  same way, and both are re-checked when a queued change applies — a fee raise that finds the floor lowered in
  between panics there); the minimum positive and at most the maximum; the fee floor at most the fee.
- **Launch** also empties both meters of each route (the plan's "empty at deploy" would leave them full by the
  launch vote, weeks later); a route reads hlmailbox's launch time and resets once.
- **Launch carries the 14-day deadline** like every builder (v27, AM8: a passed launch nobody executed was a
  standing trap). Its closure returns the deadline error when the proposal is older than 14 days, GovDAO denies it
  for good, nothing goes live and no decision is recorded; the launch is proposed again. The mailbox's page says so
  while not launched. (§5.3's sentence "it alone carries no 14-day deadline" predates v27; §1 and AM8 are the rule.)

### §5.2 the GNOT lock

- `TransferRemote` runs its own checks first (live, paused, route, amount, recipient, a user call, the exact
  envelope `{amount + fee ugnot}`) and `runtime.AssertOriginCall()` last, before any effect: the order keeps the
  refusals testable under `gno test` (the harness counts frames and refuses the assertion inside `revive`).
  The fee is forwarded to r/hlfees with a `BankerTypeOriginSend` banker; payouts use a fresh
  `BankerTypeRealmSend` banker each time; no banker is stored.
- A recipient equal to any of the six realms' addresses (the mailbox, both routes, the fee realm, hlgov, the
  bubble token) is refused at send and swept on the way in. **A recipient whose top twelve bytes are not zero is
  no gno address** (§13; K2): `p/hyperlane`'s `Bytes32ToAddress` errors on it and both routes sweep the credit —
  owed, paying nobody until GovDAO credits it by id to its owner — never the address its last twenty bytes
  happen to spell (the `Credited` event then carries the 32 bytes in hex). BUBBLE sends need no origin call: the
  allowance (`bubble.Approve` of amount + fee to the route) is the envelope.

### §5.4 caps and claims

- **The cap follows the lock down within one `Claim` call** (a tenth of what remains, re-read per turn), so a
  day's draw above the floor is at most the cap the day began with, not exactly it; where the floor binds the
  figures are exact — one cap from an empty meter in the first 24 hours, two in the worst 24 hours, a quarter for
  one recipient — and that is what the tests pin.
- **Refills are exact**: the meter carries the refill's remainder (base units × seconds, below a day), so refills
  in any pieces sum to the day's cap.
- **A recipient's meter starts empty when its queue is made** (first credit), not full — and **at the payouts
  epoch of that moment** (R5: `Credit` and `Import` take the epoch, a decision's credit asks the realm through
  `Env.Epoch`), so a queue made after an unpause's reset never takes that reset, which would fill it from the
  reset's time, a whole quarter at once ten days later.
- **A credited amount above the lock is swept** (R7): the supply is never above the lock (a mint needs a lock, a
  burn needs supply), so such a burn cannot be honest; it goes to the sweep queue like a realm-address burn —
  owed, paying nobody until GovDAO directs it — which also bounds every claim by the lock. An amount that would
  overflow what is owed is refused outright (the message stays undelivered). The gap this leaves (a gap before
  it too, for dust and realm-address burns): **the sweep queue has no void** — a forged entry there keeps the
  identity open by its amount until a sweep-void exists (a sweep payment moves real coins and cannot drop it);
  for round 2.
- **The pages are bounded** (R6): the route's page lists the first 100 waiting claims (recipients by address,
  each queue oldest first) and the first 100 sweep entries, with a count of the rest; `claims/<from>` and
  `sweeps/<from>` page the whole lists by hundreds as JSON (`claim/<id>`'s objects, the sweep entries as
  `{"id","swept"}`), as hlmailbox's `msgs/<from>` does.
- **The fee as first installment, whole**: a claim's first turn needs the meters to hold at least the fee; a claim
  whose amount is below the fee at credit goes to the sweep queue, one equal to it pays the fee alone. A claim
  stays at the head of its recipient's queue until paid in full; paid claims keep their record (state `done`) so
  a forgery paid in full can still be voided as forged.
- **`Claim(n)`** counts claims paid something on, skips dry recipients, and visits at most 4n recipients per call
  so a long ring of dry recipients cannot burn a caller's gas. The sweep queue pays only by the sweep builder.

### §5.9 the identity

`lock = sentTotal − paidTotal`, `paidTotal` counting fees and sweep payments, `owed` including the sweep queue; a
refund by id reduces `sentTotal` and the lock together (the send is undone, nothing was minted); the page prints
the supply the identity implies.

### r/hlgov (§5.3, §6.5, §13 at v24)

- Kinds 1–3, 5–9 and 12 (M1d, below) as builders; **kind 4 is reserved** (v27, AM7): `NewSetClientLayoutRequest` refuses every
  call ("kind 4 is reserved"), so no kind-4 message ever leaves gno.land; the launch client's id stays compiled
  (`launchClient`, checked by `mustKey` at init) and on the page as the pausable ISM's target whose state layout
  is fixed for the deployment's life at version 1. Kind 1 (`NewCancelRequest`) takes either a list of 1–16
  numbers ("2,3": form 0, `00 ‖ count ‖ (00 ‖ number)…`) or a range ("4-7": form 1, `01 ‖ 00 ‖ from ‖ to`,
  inclusive), always channel 0, and only numbers this realm has dispatched (the highest named is checked against
  the counter at creation and again at execution). **The body is `01 ‖ seq ‖ salt u64 ‖ form ‖ …`** (v27, AM4):
  gno.land gives a realm no block hash and the closure never learns its proposal id, so the salt is the first 8
  bytes of sha256 over the chain id, the block height, the block time, the mailbox's nonce, the decision number
  and the ledger's next id, all drawn in the executing transaction — the decision number alone makes two cancels
  of one list differ, the rest make the salt as unpredictable as the block it lands in. The description names the
  numbers only (the salt does not exist at creation); the ledger's note and `decision/<n>` show it; a re-dispatch
  carries the same salt. `sync sequence` applies at once and **only ever raises the counter** (v27, AM1): the next
  number it names must be above the next number now, checked when the proposal is made and again when it executes
  (decisions dispatched in between may have moved the counter past it; a sync that would not raise it then
  panics, so the proposal waits for a retry or its deadline), and so a number the admin program recorded is never
  reused. The counter **starts at a compiled number** (`launchSequence`, written by `init()`): 0 for a chain's
  first r/hlgov; a successor r/hlgov for a new chain is deployed with its predecessor's last number there. The page
  and `json` show the start and the counter both. `set decision recipient` is a 7-day change applied with
  `Execute`; a queued one is cancelled with `NewCancelQueuedRequest` (the local cancel), distinct from
  `NewCancelRequest` (kind 1, a message to Solana).
- Kind 5 keeps a **mirror of Solana's seats as this realm last set them**, seeded with the compiled launch lists
  and shown on the page; the real lists live on Solana, and the mirror can sit ahead of the truth (a decision the
  admin program spent) or behind it (a recovery's kind 5 moved a seat), so **it is no ground to refuse** (AN9):
  a key that holds a seat as last set here is warned about in the proposal's description, and the admin
  program's own check spends a decision naming a seat's current key — a true no-op. `hlwatch` prints both.
- Kind 6 for gno.land's own domain enrolls only **a route realm hlmailbox registers** — the launch routes and
  the successors their migrations named (`AddRoute`) — as the router, as 32 bytes, checked when the proposal is
  made and when it executes (R4): the synthetic program takes every `Dispatch` from the router it enrolls as a
  mint order and `hlmailbox.Dispatch` is open to any realm, so any other address would mint what no lock backs.
  Another domain's router is any non-zero key. The rule does not tie the token to its route (GNOT's router to
  r/hlgnot): a successor is registered without a token, so a member reads the pairing.
- Kind 8 refuses the admin program's own id — the decision recipient — at creation and at execution (R8, §13).
- What a builder cannot check on chain: kind 8's buffer and ELF hash (any non-zero keys; the admin program hashes
  the buffer at execution or the decision is spent) and kind 9's "successor shows no upgrade authority" (a member
  checks by RPC before proposing, as §13 says the builder would).
- `Redispatch(n)` sends the same body in a new message (a new nonce and id); the admin program ignores a number it
  recorded. Nothing on gno.land knows when the admin program recorded a decision, so re-dispatch is allowed at any
  time by anyone.
- Every kind's message leaves at execution with the next number (`last + 1`), as v23 says; the proposal records a
  decision in the realm's ledger as applied.

### The clock factor (§5.9, K4)

`p/decisions` carries a compiled `Factor` (1 at launch) that divides `Delay` (7 days) and `Deadline` (14 days)
and, through `Delay`, the old router's window after a swap; a devnet build is deployed with 8 there, so its 14
days pass in 42 hours as the Solana programs' `clock-factor-8` build does. Every realm's page and `json` show it
(`decisions.Clocks()`, `"clocks":{"factor","delay","deadline"}`) so §8.3's check reads the factor in the served
source and on the page and wants 1. The meters (§5.4) are rates per real day and are not divided: they are
limits, not delays.

### Launch values that are placeholders

Compiled in, written by `init()`, and replaced in the deployed source before the launch vote (§5.3, §8.3): the
three signer keys (the test vectors' signers), the six seats' gno addresses (seat 1 the cold key, the rest
`testutils` addresses), both routers (the test routes), r/hlfees' treasurer (the deployer's hotkey), r/hlgov's
admin program, pausable ISM, client and the six Solana seat keys (patterned hex). bridge/local/e2e.sh swaps the
signers and the BUBBLE router for its run's, and BUBBLE's premine to test1 as before. r/hlgov's starting number
(`launchSequence`, 0) is compiled the same way but is no placeholder: it stays 0 for a chain's first r/hlgov and
is set to the predecessor's last number only in a successor's source.

### The test harness (for whoever edits the tests)

- Realm tests drive the real GovDAO: `r/gov/dao/init/v0.InitWithUsers` makes one T1 member (the test chain id is
  `dev`), a proposal is created, voted and executed through `r/gov/dao`, so every closure runs the way a proposal's
  execution runs it. Test time moves 5 s a height (`testing.SkipHeights`). The tests' `pass` executes with
  `ExecuteProposal`; the deadline tests use `ExecuteOrRejectProposal`, the call a member makes for a builder-made
  proposal (R9: under it the deadline error denies the proposal for good; under `ExecuteProposal` the error
  reverts the transaction instead and the proposal stands — the mailbox page says so; `bridge/local/e2e.sh`
  still calls `ExecuteProposal`, which is the Go side's to change).
- **Each test function has its own height counter** (as it has its own coin table): `runtime.ChainHeight()`
  restarts near 123 in every test function and `SkipHeights` advances it from there, so a claim credited in a
  later test function can carry a lower height than one credited earlier. A test about ranges reads heights
  from the claims it made and never assumes order across functions.
- `revive` catches only panics that cross a realm boundary, so p/ packages' refusals are tested at the realm level.
- `runtime.AssertOriginCall()` passes only on a direct call from the test function (the harness counts frames).
- A code-realm `testing.SetRealm` moves the origin-send envelope's recipient; a user-realm one leaves it.
- The harness's coin table does not survive from one test function to the next (the realm's state does), so a
  test that pays out re-funds the realm's account to its lock first (`fundLock`).
- A realm cannot convert a value into another realm's named type (`hlmailbox.Key([32]byte{…})` is refused), nor
  copy into a slice of such an array; tests take keys from `hlmailbox.Signers()`.

### The e2e (bridge/local/e2e.sh)

GovDAO is seeded with gnodev's test1 (`InitWithUsers`) and launches the realms with `Launch` from a `maketx run`
script; the staged hlbubble carries a cap floor of 100,000,000 BUBBLE a day so the claim's fee refills in seconds
(the real floor would take a quarter of an hour); the script calls `Claim` itself after the relayer's `Process`,
since the relayers' `Claim` loop is M3. 500 BUBBLE go out (fee 100), 200 come back as a claim paid with the fee
first, the fee realm holds 200, the lock equals Solana's supply at 300 and nothing is owed. The Go side is
unchanged but for the vector generator (`bridge/hl/gen_test.go`), which now also writes hlgnot's and
hlverifier's vectors; it encodes no decision body, so v27's salt changes nothing there. What v27 leaves to M2c
and M3c (REVIEWS.md's v27 note): the admin program's and `bridge/sol`'s kind-1 layout (`EncodeParams`,
`ParseParams`, the relayer's cancel handling, `hlwatch`) still begin at `form`, 8 bytes before r/hlgov's body now
does, so the e2e's cancel step disagrees with the realm until they carry the salt.

### Audit round 1 (2026-10-13): the fourteen fixes and the readings chosen

One commit each on branch `bridge`, every one with a test; the findings are AUDIT.md's round-1 files (the realms'
R1–R10, the red team's AN6 and AN9, the conformance audit's K2 and K4). Where the plan is silent the reading is
the simplest safe one, and it is named here:

- **R1** a queued signer rotation keeps the set and rule it was decided against; `Execute` panics unless they
  still equal the live ones (the proposal stays pending; cancel and propose afresh). Reading: a queued `rotate
  seats` keeps all six seats, so any seat change in between makes it stale, a removal elsewhere included.
- **R2** under the two-group rule a pure removal is at once when the live rule pruned of the removed keys equals
  the proposed rule (groups as sets), the keys are a strict subset and `Satisfiable` holds in the executing
  transaction. Reading: a threshold is never pruned; a partner's removal is never at once.
- **R3** the void builder's unpause option vacates two guardian seats at most (`ParseSeats`). Reading: the
  unpause builder takes one seat, so it is within the cap by construction; it was not widened to a list.
- **R4** kind 6 for gno.land's domain requires the router to be a registered route's address, re-checked in the
  closure. Reading: any registered route, the token not tied to its route.
- **R5** a fresh queue starts its meter at the current payouts epoch. Reading: `Credit`/`Import` take the epoch
  as data; the decision path asks the realm (`Env.Epoch`), as it asks the flags.
- **R6** the pages cap both lists at 100 with a count; `claims/<from>` and `sweeps/<from>` page them. Reading:
  the page's order (recipient, then position) is the paging order; the sweep queue pages by id.
- **R7** an amount above the lock is swept; an amount that would overflow `Owed` is refused. Gap: no sweep void
  (above).
- **R8** kind 8 refuses the admin program's own id, at creation and execution.
- **R9** the mailbox page names `ExecuteOrRejectProposal`; this file too. The e2e's `ExecuteProposal` calls are
  the Go side's.
- **R10** the fee at most a quarter of the cap floor, the cap floor's quarter at least the fee, re-checked when a
  queued change applies.
- **K2** a non-gno recipient (top twelve bytes not zero) is swept; `Bytes32ToAddress` errors. Reading: swept
  rather than refused (the audit's own fix), so the message is done and GovDAO credits it by id to its owner,
  the same cure as a realm-address burn; hlbubble's `dirtyBob250` vector now tests the sweep.
- **AN6** a payouts flag and a void may name a range: `heights:<from>-<to>` or `since:<from>`; the at-once rule
  applies to the claims a flag's range covers; the list cap stays. Readings: a height range covers hand credits
  too, a `since` range the verifier's only; a void closes its range at the executing height; a claim the void
  cannot take is skipped; restore and credit stay lists; a range void's gas is unmeasured.
- **AN9** kind 5 warns about a seat's current key in the description instead of refusing.
- **K4** the compiled clock factor (above).

What the Go side must follow (nothing in `bridge/` was changed here): `hlwatch`'s flag command passes a payouts
flag's ids joined by commas — a range is the one string `heights:<from>-<to>` or `since:<from>` in `SetFlag`'s
`ids` argument, and the void builder takes the same string as its `ids`; the monitor's and `hlwatch`'s readers of
`claim/<id>` see a new `"height"` field (ignored today) and may page the lists at `claims/<from>` and
`sweeps/<from>` instead of the page; every realm's `json` carries `"clocks"`; `hlmailbox.Flagged` takes `(route,
id, height, verifier)`; `bridge/hl`'s `Bytes32ToGnoAddress` still truncates — the monitor's diverted-credit
check sees a swept non-gno recipient as a sweep that matches the dispatch's amount (no false flag), but the
reader should error on a non-zero top twelve bytes so `hlwatch` names such a credit; the e2e's `ExecuteProposal`
calls should become `ExecuteOrRejectProposal`; the vectors needed no regeneration.

### M1d (2026-10-13): r/hlgov's kind-12 builder

- **Kind 12, `set mint limits`, is a builder of r/hlgov** — `NewSetMintLimitsRequest(cur, token, maximum, dayCap)`
  — adopted from the stand-in the e2e staged beside its copy of the realm (`bridge/local/hlgov_mintlimits.gno`,
  gone: the e2e stages nothing beside the realm now, and its kind-12 section runs against the realm's own
  builder). It is `proposeKind` like kinds 2, 3 and 6–9: refused before Launch, checked when the proposal is made,
  dispatched at execution with the number current then (GovDAO's closure, under the 14-day deadline) and recorded
  in the ledger as applied ("dispatched as decision n"); the 7 days are the admin program's clock, as for every
  kind of the 7-day group — the realm queues nothing. The body is §13's `12 ‖ seq u64 ‖ token u8 ‖ maximum u64 ‖
  day cap u64` (17 parameter bytes, big-endian, base units), the bytes the admin program (`kinds.rs`) and
  `bridge/sol` parse; the test writes them out.
- **The bounds are the realm's own early refusals.** The admin program and the pausable ISM check only the token
  (0 or 1) and take any maximum and day cap. The builder refuses a maximum of 0 (every mint refused — a pause by
  other means, where the pause is the guardians' and watch keys' flags, §6.5) and a day cap below the maximum (a
  message at the maximum could never pass), when the proposal is made; a day cap equal to the maximum (one such message a day) is
  allowed. Nothing is re-checked at execution: the values are the parameters, and nothing on gno.land they could be
  checked against changes — the gno route's own per-message maximum is §5.4's separate rail, the plan ties the two
  nowhere, and the e2e sets them apart on purpose.
- **No mirror of the limits on the page.** The seats are mirrored because kind 5 warns against them; the limits
  have no such use, their truth is the pausable ISM's state (`hlwatch` and `hlsol audit` print it; the e2e checks
  the launch values there), and a mirror would be one more placeholder able to drift. The realm compiles no launch
  limits either: §5.3's list of values compiled into the realms does not name them, and they are the pausable ISM's
  (`config.rs`, 25,000 a message and 100,000 a day of each token, printed by the audit). The decision renders as
  every dispatched one does — the Sent line `#n kind 12: set BUBBLE's mint limits: … a message, … a day (base
  units)`, `decision/<n>` and the ledger's `### set mint limits` — which the test pins. The description is the
  parameters in words and nothing else, as §5.3 wants: the numbers as GovDAO passed them, in base units, never
  divided for display (a member compares them with the audit's lines, which print base units too).
- Tests: `TestMintLimits` (last in the file, so no earlier test's numbers move) — the three refusals move nothing;
  the body byte for byte for BUBBLE 50,000 / 200,000; the GNOT edge (cap = maximum, 1 / 1); a pre-armed proposal
  (voted, unexecuted) records nothing and takes no number, then takes the number current when it executes after
  another decision; a pre-armed one left 15 days is denied for good under `ExecuteOrRejectProposal` and dispatches
  nothing; `TestLaunchValues` has the before-Launch refusal.
- Verified on `bridge-m1d` (fefdd99): the 16 gno suites green under the pinned v1.2.0 (`scripts/gnotest.sh`), and
  the e2e from nothing `PASS in 583s` with the kind-12 section against the realm's own builder — decision 2
  recorded on the admin program (`channel 0 #2 kind 12`, params `01 ‖ 0000000ba43b7400 ‖ 0000002e90edd000`), the
  executor waiting on its factor-8 maturity, the final audit and hlwatch green with the kind 12 still pending.

#### Audit round 2 (2026-10-13): R11–R18, K19, K22 — the fixes and the readings chosen

Three commits on `bridge-m1d` (1220dc2 hlmailbox, 78bea89 p/hlroute and both routes, 5fb0926 r/hlgov), every fix
with a test in the realms' style, both routes alike. Where the plan is silent the reading is named:

- **R11, the sweep record.** A sweep entry is `{ID, Amount, Height, By, Voided}` (`hlroute.SweepEntry`), indexed
  in `credits` by its height like a claim, so a void names it by id or by range and the flag test is the same
  (`Flagged(id, height, verifier)`). Readings: an entry is voided **as forged only** — it is withheld by nature
  (owed, paying nobody), so a void as withheld refuses it in the list form and skips it in the range form; the
  void takes its amount out of `Sweep` and `Owed` and books nothing lost (nothing was paid); the restore reverses
  it after 7 days like a forged claim's; a credit by id replaces an entry — voided or not — with the claim GovDAO
  names (its amount leaves `Sweep` and `Owed` unless the void took it already; the claim is owed instead; no
  mailbox mark, the message was delivered), the cure the realms' comments promised for a realm-address or non-gno
  burn and for a diverted credit; a sweep payment is at most `min(Sweep, Lock)`, re-checked when it applies. The
  entry's JSON (`claim/<id>`, `sweeps/<from>`) keeps `"swept"` for the agents and adds `height`, `by`, `voided`;
  the page line names the crediter, the height and a void. The identity is asserted after every transition in
  `TestSweepVoid` and the realm's counters in `TestSweepVoidAndGuards`.
- **R12, voided claims leave the queue.** `void` dequeues an open claim (and drops the recipient from the ring
  with its last open claim); `restore` re-inserts it at its seq (making the recipient's queue if the successor has
  none yet); `creditByVote` no longer restores first; nothing but open claims ever sits in a queue, so `oldestOpen`
  reads the head. `TestVoidedClaimsLeaveTheQueue`: 300 voided ahead of one open, paid in one call.
- **R13, the paged migration.** `Pull(cur, page)` → `Export(page, 500)`: page 0 carries the seven counters (the
  realm pays the lock with it) and every page up to 500 records — the claims first, every claim ever credited
  (open, voided and paid, so a void or a restore reaches them on the successor), then the sweep entries, then the
  sends never refunded (refunded ones are walked off and dropped) — each leaving the predecessor as it goes; the
  pages go in order (`page N is the next to pull`), a page pulled already exports nothing and pays nothing, an empty
  next page advances nothing. Readings: the counters move whole with page 0, so between pages the successor owes
  claims it does not hold yet and the predecessor holds records with zeroed counters — both refuse every function
  (migrated; not yet adopted) and the monitor's identity reads the counters, which are consistent; the successor
  places every imported claim at a per-queue `base + old seq` (the base fixed at the queue's first import, its own
  claims credited before the import staying ahead), so the order holds whatever order the pages arrive in, and a
  non-open claim gets its place too for a later restore. The ring `waiting` is now exact (a recipient is in it
  while its queue holds an open claim) and is what `eachWaiting`, `waitingCount` and the page walk — the audit's
  `nonEmpty`. `TestPagedExport` runs pages of three across claims, sweeps and sends, and a successor that credited
  first; hlgnot's `TestMigrate` pulls page 0 twice and page 2 out of order.
- **R14, the range closed at creation and the cap.** `DraftVoid` takes the block height the proposal is made at
  (the realms pass `runtime.ChainHeight()`): a `since:` range is closed there (`up to height H` in the description)
  and a `heights:` range reaching past it the same, so what a range covers never grows before execution; the
  draft's scan counts the credits the void can take (claims and sweep entries) and refuses more than
  `MaxRangeClaims = 1000` ("split the range by heights"); the second closing at execution is gone (redundant; it
  also broke a test whose recorder answered height 0). A payouts flag's range still walks nothing, so it is not
  capped. Gas: the harness's GAS lines put `TestRangeVoidThousand` (1,002 credits, the refused draft's scan of
  1,001, the accepted draft's scan of 1,000, the at-once void of 1,000) at 3,596,711,782 against its baseline
  (`TestRangeCapBaseline`, the 1,002 credits alone) at 2,724,680,525 — ≈0.87B for the two scans and the thousand
  voids, ≈0.87M a claim in the harness's model, which charges VM work, not the chain's storage reads; by the
  chain's table (the audit's loads: two `credits` nodes and ≈four `claims` nodes read at 59,000, one claim written
  at 24,000) the thousand is ≈0.4B, well inside a 3B block, which is why the cap is 1,000. Measure on the devnet
  build (M4).
- **R15, the kind-12 floor.** r/hlgov imports both routes and refuses a per-message maximum below the token's
  `Maximum()` on gno.land, when the proposal is made and in the closure (a pre-armed kind 12 at 25,000 is refused
  once hlgnot's maximum was raised to 30,000 in between: `TestMintLimits`); equal is fine. The route's maximum
  builder's description ends with "raise the Solana limit (kind 12) first; a send above the Solana maximum strands".
  So a raise is kind 12 first, the route after; a lowering the other way round. The e2e's staged hlbubble (maximum
  50,000) and its kind 12 (50,000 a message) sit at the edge and pass.
- **R16, payouts flag at Execute.** Both routes' `Execute` call `mustNotBePaused(payouts)` when the decision is a
  sweep payment or a refund — a panic, so the decision waits (hlgnot's refund, hlbubble's sweep, each under a
  guardian's flag, then after the clear).
- **R17, seat:address.** The void builder's `DraftVoid` takes the seat holder lookup (`hlmailbox.Seat`) and records
  each seat's holder in `VoidParams.RemoveAddrs`, the description writing `removing seats 2:g1…`; at execution
  `Env.RemoveSeat(seat, holder)` → `hlmailbox.VacateSeat(cur, seat, holder)` vacates only while the holder still
  has the seat and reports it, the note listing `seats N vacated` and `seats M skipped (the occupant changed since
  the proposal was made)`. hlmailbox's unpause records `removeAddr` the same way and notes a skipped seat, spent
  or applied. Reading: the input stays a seat number (the builder reads the holder), since GovDAO means "the key in
  seat 2 today"; an empty seat is still refused when the proposal is made.
- **R18.** `mustRaise` refuses `n > last + 1 + 2^32` (kind 10's reach), at creation and in the closure.
- **K19.** `NewRotateSignersRequest` asks `satisfiable(ks)` when a strict-subset pure removal is made and refuses
  one the active verifier says the rule cannot survive; the at-once test at execution is unchanged (a removal that
  fails it then — another landed in between — is queued, as §5.3 says). The test verifier gets a switch to say no.
- **K22.** `Figures` and `JSON` take hlmailbox's `LaunchedAt()` and show the meter as the launch reset leaves it
  (`meterView`: a copy reset at the launch time when `LaunchSeen` lags, then refilled) — a pure read; the first
  `Claim` still applies the reset. Exact in `TestLaunchResetView` (an hour's refill shown where the raw meter is
  full); the realm pages show the meter at 0 right after Launch.
- Verified: the 16 suites green (`scripts/gnotest.sh`, pinned v1.2.0) and the e2e from nothing `PASS in 575s`
  on the fixed realms — the kind 12 at the R15 edge recorded, the unpause under R17, the final audit and hlwatch
  green. Not changed on the Go side: the routes' JSON keeps every field the agents read (`swept` on a sweep entry;
  `meter`, `owed`, `sweep`, … on the route) and only adds `height`, `by`, `voided` to a sweep entry; `Pull`'s page
  argument and `Export`'s paging are the successor's (no successor realm exists yet), `VacateSeat`'s holder is the
  routes' call. Left: measure the range void on the devnet build; a successor's `Adopt` imports page by page.

### M1e (2026-10-13): audit round 2x — the realms against the Interrealm Specification v2 and the security guide

Two findings (AUDIT.md's `audits/r2/audit-r2x-interrealm.md`, read whole: its "checked and held" is the context),
both at the mailbox's open registration; two commits on `bridge-m1e` (hlmailbox; p/hlroute's sibling with hlgnot's
test), every fix with a test that was red on the unfixed realm first. Read first, as AGENTS.md asks:
`docs/gno-specs/gno-interrealm-v2.md` (§3.2 storage = authority, §4.1–§4.4 the borrow rules and the no-anchor case,
§5.2 `cur` as a capability token, §5.5 sub-realm identities, §11.3 the public-API checklist) and
`gno-security-guide.md` (§2.1 the declaration-site rule, §3(C), §5.3–§5.4, §5.6, §8); then the pinned v1.2.0 VM
(`gnovm/pkg/gnolang/uverse.go`: `Sub`'s four guards; `Subpath` derived from the pkgpath after the first `#`, so it
answers for a test-built `#` path too) and the harness's `SetRealm` (`tests/stdlibs/testing/context_testing.go`:
it rewrites the calling frame's captured cur, after which `Sub` refuses that cur as outside its namespace).

- **X1 (medium), a sub-identity registers; the listing unbounded.** `RegisterVerifier` now refuses
  `cur.Previous().Subpath() != ""` ("a realm registers with its own cur, not a sub-identity"), after the `/r/`
  prefix test. Spec §5.5: accepting sub-identities is opt-in for a callee, and PkgPath-keyed auth must be anchored
  — a bare prefix also matches `host#subpath`; guide §5.6 and §8 (identity from `cur.Previous()` under the
  `IsCurrent()` guard, which the function already had — the gap was what it accepted). Reading: refused outright,
  not the anchored idiom's `host || host#…`, because the registry's keys are what GovDAO names and what the page
  walks — an entry must cost a package deployment, round 2's assumption now enforced (nothing ever unregisters).
  The listing is bounded like R6's pages: `json`'s `registered` is the first 20 paths in path order and a new
  `registeredCount` beside it (the Go's `mailboxJSON` reads `registered` as before and ignores the new field;
  `ReadMailbox` uses only the active verifier's `registered` flag, unchanged — the e2e greps that flag); the page
  names 20 with the count and links `verifiers/20`; `verifiers/<from>` pages by 20 (`IterateByOffset`). Siblings,
  by a grep of the `gno.land/r/` prefix tests: `AddRoute` (hlmailbox) and `DraftMigrate` (p/hlroute) check a path
  GovDAO's migrate names, not a caller — both now refuse a `#` path as well, since `mustBeRoute` is an exact match
  and a route at `host#sub` would hand the route powers (`MarkDone`, `ClearFlags`, `VacateSeat`, `AddRoute`) to a
  token its host mints for free; no deployed realm has such a path. Tests: `TestRegisterVerifierSub` mints a real
  `cur.Sub("spam")` in the harness — first thing in its function, before any `SetRealm` — and is refused, the count
  unmoved (red first: the unfixed realm registered `…/hlmailbox#spam`, and the listing test's dump showed it);
  `TestRegisteredListing` registers 25 more code realms and pins the json's 20 and count, `verifiers/0`,
  `verifiers/20`, an offset past the end and a bad offset (`[]`), and the page line; `TestFlags` and hlgnot's
  `TestMigrate` the `#` refusals.
- **X2 (low), the mailbox's slice handed to an unchecked interface.** `satisfiable` passes each active verifier
  `append([]Key(nil), ks...)` — its own copy, so one verifier's write cannot reach the next's view either — and
  `Verifier`'s doc says an implementation is a type declared in the registering realm (its methods then run
  under that realm's authority, guide §2.1 / spec §4.1) and names the gap: a /p/-declared type with no object to
  anchor on runs under the mailbox's own authority (spec §4.2's no-anchor case, §4.4; guide §3(C), §5.3), which
  `RegisterVerifier` cannot tell apart — §11.3's canonical-type gate needs a type the mailbox can name, and a
  foreign realm's type is not one; so GovDAO activates a verifier whose source it has read, and the mailbox hands
  a verifier nothing it keeps: `Verify`'s bytes are the recipient's (readonly to the mailbox), `Satisfiable`'s keys
  a copy. The builders' order (gov.gno: `subset` before `satisfiable` at creation, the same test again in the
  closure, then `signerKeys = ks`) is unchanged. Test `TestSatisfiableCopy`: a verifier declared in the mailbox's
  own package — so its write into the slice lands exactly as a no-anchor /p/ verifier's would — registered and
  activated through GovDAO's 7-day path, then the direct call returns the slice as it went and a pure removal at
  once stores the keys the proposal named, the written key nowhere (red first: `eeee…` replaced the first signer
  in the slice `satisfiable` was handed).
- Verified on `bridge-m1e` (a00c6d8): the 16 gno suites green under the pinned v1.2.0 (`scripts/gnotest.sh`);
  `python3 scripts/check-interrealm.py` with the hl* realms and the p/ packages clean (its one line is
  realm/bubbletreasury/treasury.gno:223 `func Do`, a position-2 `rlm`, outside the bridge); the e2e from nothing
  `PASS in 593s` on the fixed realms, run 1, started once the Go lane's run and its validator had left the Mac —
  a watch for that must match a shell running the script and the validator binary (`pgrep -f '^(/bin/)?(ba|z)?sh
  [^ ]*bridge/local/e2e[.]sh'`, `'^[^ ]*/solana-test-[v]alidator'`), since a bare `pgrep -f bridge/local/e2e.sh`
  also matches any shell whose command text names the script, its own included, and never clears. Not changed on
  the Go side: `mailboxJSON` reads `registered` as before (now the first 20 paths) and may add `registeredCount`
  to `MailboxView` when hlwatch wants the count; `ReadMailbox` and the e2e's `"registered":true` grep are
  unaffected.

### M1f (2026-10-13): audit round 3 — the realms' R19–R26 and the red team's AP4

Eight findings (AUDIT.md's round-3 files: the realms' R19–R26 and the red team's AP4, the paged migration's hole
seen from the other side), eight commits on `bridge-m1f`, every fix with a test proved red on the unfixed code
first, the siblings of each pattern found by a grep across p/hlroute, hlgnot, hlbubble and hlmailbox. Read first,
as AGENTS.md asks: `docs/gno-specs/gno-interrealm-v2.md` §3.2 (storage = authority: a migration page is plain
values the successor copies into state of its own), §4.1–§4.3 (the borrow rules: every `Env` closure runs under
the realm that declared it, so a payment or a mailbox call made by p/hlroute's code is the route's own act), §5.2–
§5.3 (`cur` as the capability token; realm values ephemeral — the announcement keeps a pkgpath string), §5.5
(sub-realm identities: PkgPath-keyed auth never accepts `host#subpath` by a prefix), §9 (a panic crossing a boundary
aborts the whole transaction — why a closure that must survive a changed id skips instead of panicking), §11.3;
`gno-security-guide.md` §2.1, §3(C), §5.1/§5.1a (no pointer into route state leaves a realm: `st` stays unexported,
`Pull` returns value copies), §5.6 (identity from `cur.Previous()` under `IsCurrent()`), §6.1, §8; the pinned
v1.2.0's `uverse.go` (`Subpath`) as in M1e. The readings chosen:

- **R19, the sweep queue settled by id.** `DraftSweep(ids, to)` names entries (comma separated, up to 256): their
  sum, at most the lock, goes to `to` when the decision applies and the entries are dropped from `sweeps` and
  `credits` as a credit by id drops one — so a paid entry never stands to be credited by id (the message is done
  in the mailbox, and with no record left `DraftCredit` refuses it as "done already") or voided again, and the
  counters never go negative; the by-amount form is gone. Readings: an entry voided or gone by execution is skipped
  and the note says so (R22's rule; the lock re-check still panics — a sum the lock no longer covers waits); a
  voided entry is refused by the draft ("restore it first") and a claim's id is no entry's; `voidSweep` and
  `creditByVote` refuse an entry above `Sweep`, an invariant guard unreachable through the builders and tested
  white-box; a paid entry's `claim/<id>` answers null and the page lists it no more — the decision's text and note
  carry the ids and the sum. Both routes: `NewSweepRequest(cur, ids string, to address)`.
- **R20 and AP4, the paged migration.** `Export` writes each claim's recipient counter into the page
  (`Claim.QueueNext`, the predecessor's final `q.next` — final because nothing credits there after the migrate
  decision applied; 0 on a stored claim, not in the JSON); `Import` raises the queue's counter to `base +
  QueueNext` on first seeing the recipient, so the successor's own credits land above every place a later page can
  claim; and from page 0's import until the page with `More == false` the successor refuses `Credit` (the message
  stays undelivered; the relayer brings it again) — shown as **importing** on the page, with the pages so far, and
  as `importing`/`imported` in the json. Readings: `Import` takes the pages in order, each once ("page N is the
  next to import"), because a re-pulled page is empty with `More == false` and would otherwise end the import early;
  a hand credit, a restore and a credit by vote are not gated — their places cannot collide once the counter is
  raised, and a hand credit of an id on a page still to come is "done already" in the mailbox — which is what the
  test credits between two pages carrying one recipient's consecutive seqs (red: "a claim at place 2 of bob's
  queue exists").
- **R21, rotate seats' removals.** `rotateSeatsParams.held` records each seat's holder when the proposal is made
  (the description names it already); `applySeats` vacates a seat only while it still holds that key and reports
  the seats vacated and skipped — the at-once note "removals: seats 3 vacated, seats 5 skipped (the occupant
  changed since the proposal was made)", the decision spent when nothing was left to remove; the queued path keeps
  its whole-seats check and says the same of a removal it skips. The siblings held already: the void builder's and
  the unpause's removals (R17).
- **R22, a list skips a changed id.** `Decide`'s list branch and `apply`'s skip and count an id the void cannot
  take as it stands — voided by another decision since the proposal was made, paid in full under a void as
  withheld, or gone — "n skipped (not voidable as it stands)" in the note at once and after the delay, instead of
  a panic that blocks the proposal, its unpause option and its seat removals for its whole deadline. The siblings by
  the same pattern, fixed the same way: a restore list ("skipped (not voided as it stands)") and a refund list
  ("skipped (refunded already, or gone)"); a credit by id stays a single-id decision whose refusals are what a
  member wants to hear. `apply` returns the note's tail and `Execute` writes "applied after the delay; …". hlgnot's
  `TestVoidAndFlags` turned around: the queued void of a claim paid in full applies with "0 voided, 1 skipped".
- **R23, the range cap and the last height.** `DraftVoid`'s scan counts every credit it visits toward
  `MaxRangeClaims`, voidable or not ("more than 1000 credits in …, voidable or not: split the range by heights"),
  since the walk at execution covers them all; the cap is tested before "no claim to void" (a scan that stops at
  the cap cannot know what follows); a range decision's note ends with the last height it voided, at once and after
  the delay ("last voided at height H"), where the next `heights:` range starts. The test: the forged thousand plus
  998 open credits under `since:` refused where the old count (998 voidable) accepted it — the red — and then with
  2,000 open credits as the finding states.
- **R24, the page's count.** `Figures` takes the waiting total from `nOpen` (R12's invariant makes it the walk's
  number); `waitingCount` stays for the tests. The 2,000-recipient test's GAS line, the same page before and after:
  10,765,962,064 → 10,503,811,422.
- **R25, a successor announces itself.** `hlmailbox.AnnounceSuccessor(cur)` records the caller's exact pkgpath, from
  the successor's own code (`init(cur realm)` with `cross(cur)`, as hlverifier registers): an `/r/` realm only, a
  sub-identity refused as in X1, once ("announced itself already": a repeat is a bug; nothing is ever withdrawn);
  `SuccessorAnnounced(path)` reads it; `DraftMigrate(to, self, announced)` refuses a path not announced and the
  closure asks again through `Env.Announced`. The listing is bounded like X1's: the json's `successors` (the first
  20 paths) and `successorCount`, `successors/<from>` by 20, the page's "successors announced (n)" line. Reading:
  the announcement proves a realm exists at the path and chose to be a successor — GovDAO reads its source before
  the vote as before; the mailbox keeps no realm value, a string.
- **R26, the lock bound.** `DraftCredit` and `creditByVote` refuse `amount > Lock` (a hand credit above the lock
  cannot be honest, R7's rule for the verifier's credits), when the proposal is made and again when it would apply
  (a queued credit the lock no longer covers waits); `Claim` caps a turn's `avail` at the lock before the fee test,
  so a claim standing above the lock — honest claims after a forgery drained part of it — is paid what the lock
  holds, and a dry lock is a skip, never a `SendCoins` revert that drops the cursor and stalls every recipient
  behind it. The test drove the old code's lock to −1,000.

Part 2, two r/hlgov guards the programs lane found it cannot enforce (its M2-NOTES "M2f" has the measurements), the
same rules, each with a test red first:

- **The decision body's bound.** `MaxDecisionBody = 600` bytes at M1f, 512 since M1g part 2 (kind, number and
  parameters together): Hyperlane's mainnet mailbox copies a message through its own 32 KB heap and dies on it past
  about 2,250 parameter bytes, before any ISM or recipient runs, and a legacy Solana delivery of a decision carries
  at most 538 bytes of body, 529 of parameters — 14 account keys and 127 bytes of fixed instruction data leave that
  of the 1,232-byte packet (the round-4 assumptions audit's F18; M1f's "near 600" sat 62 bytes above it) — a body
  no one can deliver would stick r/hlgov's sequence with its number untaken, in a form no program of ours cures. One
  guard where the realm dispatches: `mustFit` in `dispatch` (every kind's closure, the cancel's salted body
  included) and at creation in `proposeKind` and the cancel builder. Every builder's body is far below (a cancel at
  most 163 bytes with its salt, kind 5 at most 4 entries), so `TestDecisionBodyBound` exercises the two choke
  points directly: a body one byte above the bound refused at both, one at the bound accepted (red first at M1f:
  the old `proposeKind` returned a request for the 601-byte body and `dispatch` would have sent it; red again at
  M1g part 2: the 513-byte body passed under 600).
- **Kind 9 never names the admin program itself.** `NewTransferToSuccessorRequest` refuses a successor equal to the
  decision recipient the realm knows, when the proposal is made and again in the closure (the `check` `proposeKind`
  runs at both, as kind 8's self-upgrade has it): a recipient swap landing between the vote and the execution would
  otherwise turn a pre-armed transfer into one the admin program only spends (S-15: it re-points every authority to
  itself and dies on its own `SuccessorInit`, pending until a cancel a week on). `TestSuccessorIsNotTheRecipient`:
  the pre-armed transfer to a key that then becomes the recipient is refused at execution and dispatches nothing
  (red first: it dispatched). hlwatch's own refusal and its read of the successor's `predecessor`/`inherited` are
  the Go lane's, as the finding says.

What the Go side must follow (nothing in `bridge/` was changed here): `NewSweepRequest(cur, ids string, to
address)` on both routes — the sweep payment names entries by message id (comma separated, up to 256) instead of an
amount, and its text reads "pay X of the sweep queue to g1…: id,…"; a paid entry's `claim/<id>` answers `null`
afterwards (the monitor's `divertedCredits` treats a null as a lag and never flags it; a record checked before the
payment stays checked); the routes' `json` adds `importing` and `imported`; the mailbox's `json` adds `successors`
and `successorCount` after `routes`, with a `successors/<from>` page; `hlroute.Env` gains `Announced` for any realm
building one; a successor realm calls `hlmailbox.AnnounceSuccessor(cross(cur))` from its init before GovDAO's
migrate; decision notes now carry skip counts and a range's last voided height (`hlwatch` lists decisions by text,
unchanged); every field the agents read keeps its name (`swept`, `meter`, `owed`, `sweep`, `registered`, …).

- Verified on `bridge-m1f` (cc4de66): the 16 gno suites green under the pinned v1.2.0 (`scripts/gnotest.sh`) on
  the final tree; `python3 scripts/check-interrealm.py` with the hl* realms and the p/ packages clean (its one line
  is realm/bubbletreasury/treasury.gno:223 `func Do`, a position-2 `rlm`, outside the bridge); the e2e from nothing
  `PASS in 2097s` (run 1, logged at `~/.config/bubblerumble-jarvis/bridge-e2e-logs/m1f-run1.log`, started once no
  e2e or validator ran on the Mac and slow under a foreign load of 50–100 — all 32 sections, the four expected FAIL
  lines of the negative audit and the config refusal before the PASS). The realms it staged were the tree at
  8e89a4c — R19–R26 and AP4; part 2's r/hlgov guards landed while it ran and are covered by the suites and the
  checker, the whole-tree e2e being the Go lane's run (no second run here, by the coordinator's word, the Mac's
  load being Jae's).

### M1g (2026-10-13): audit round 4 — the realms' R27–R30, the red team's AQ1, AQ2 and AQ5, and the assumptions audit's F18

Eight findings (AUDIT.md's round-4 files: the realms' R27–R30, the realm parts of the red team's AQ1, AQ2 and AQ5,
and the assumptions audit's F18 as part 2), eight commits on `bridge-m1g`, every fix with a test proved red on the
unfixed code first — a behavioral red wherever the test could be written against the old API (the refused refund
accepted, the flags not following, the other token's successor taken, the 513-byte body passing), a compile-red
where the test needs a reader the old code lacks — the siblings of each pattern found by a grep across p/hlroute,
hlgnot, hlbubble, hlmailbox and hlgov. Read first, as AGENTS.md asks: `docs/gno-specs/gno-interrealm-v2.md` §2 (the
two contexts: a successor's `Process` runs under the successor and the mailbox's mark under the mailbox, so the
successor's refusal before the mark is the route's own panic, the mark reverting with it on chain), §3.2 (storage =
authority: a flag copied to the successor's path is the mailbox's own allocation; a page's sends are plain values
the successor allocates as its own, the `Imported` mark with them), §4.1 (every `Env` closure runs under the realm
that declared it — `AnnouncedFor` and `AddRoute` are the route's own calls into the mailbox), §5.2–§5.3 (`cur` as
the capability: `Process` reads the caller's pkgpath from `cur.Previous()` under `IsCurrent()` and walks the
succession from that path, never from a parameter; the succession, the announcement's route and the former
recipients are strings, no realm value kept), §5.5 (pkgpath-keyed auth is exact: `successorOf` walks `routes`' exact
keys, so a `host#subpath` token is nobody's successor and `AddRoute` still refuses it), §9 (a panic crossing a
boundary aborts the whole transaction — why a range's pre-count, a refund's lock floor and a stale swap are panics
that hold the decision for a cancel, and why the predecessor refuses before the mailbox is asked), §11.3;
`docs/gno-specs/gno-security-guide.md` §2.1, §3(C), §5.1/§5.1a (`SendJSON`, `RefundsPage`, `Former` return strings
and copies; no pointer into route or mailbox state leaves a realm), §5.6 (identity from `cur.Previous()` under
`IsCurrent()`, in `Process` and `AddRoute`), §6.1, §8; the pinned v1.2.0's `uverse.go` (`Subpath`) as in M1e. The
readings chosen:

- **AQ1, a migrated route's burns are its successor's to credit.** Until kind 6 enrolls the successor every burn's
  message names the predecessor as recipient, which refuses everything after its migrate applied, so the burns of
  that week were uncreditable by either realm. Now `hlmailbox.AddRoute` records the succession both ways on the
  route records (`routeRec.predecessor`, `routeRec.successor`, in the json's `routes` entries and on the page: "successor
  of", "migrated to") and refuses a path that is a route already and a second migration of one route (a chain never
  cycles, bounded by `maxChain` all the same); `Process` lets a caller take a message whose recipient is any route in
  its chain of predecessors ("only the message's recipient, or a successor of it, can take it"), the mark being the
  shared `done`, so the id is credited once. The route decides the moment: `hlroute.MustHaveImported` refuses a
  message addressed to another realm until the predecessor's last page is in ("addressed to another realm: a
  successor takes its predecessor's messages once the last page of the migration is in (N pages so far)") — before
  page 0 the lock is not there and the credit would be swept as one above the lock, between pages `Credit` refuses
  anyway — asked in both routes' `Process` before the mailbox is asked, so the message waits undelivered and the
  relayer brings it again; a launch route, which succeeded none, refuses such a message for good. A migrated route
  refuses in `Process` before `hlmailbox.Process` too ("migrated to X: the successor takes this message"): the
  harness keeps a mark an abort left, and on chain it spares a verification. Tests: `TestRouteSuccession` (hlbubble
  → hlbubble2 → hlbubble3: a stranger, hlgnot and hlgnot's own successor refused, the successor taking it, the
  predecessor finding it delivered, two hops, a successor's own message not the predecessor's to take),
  `TestInheritedMessageWaitsForTheLastPage`, hlgnot's gate on a new vector addressed to hlbubble (`inOtherRealm60`,
  from the generator) and the successor taking `inBob60` after the migrate.
- **AQ2, a refund is a successor's duty, over the sends it imported.** A refunded send stays deliverable on Solana,
  so a live route's own send is never refunded — stranded over the per-message maximum, it waits for a kind 12
  (§5.4). `Send.Imported` is set by `Import` alone; `DraftRefund` refuses an unimported send ("this route's own send:
  a stranded send waits for a kind 12 on Solana, never a refund") and `apply` skips one; the description reads
  "refund X of sends imported from the predecessor (after kind 6 enrolled this realm) to their senders: ids" — the
  realm cannot see kind 6, the member checks hlwatch. The mark is readable: `send/<id>` answers
  `{"id","from","amount","at","imported","refunded"}` or `null`, `refunds/<from>` pages the refunded sends by
  hundreds (the index `refunds`, cleared with the sends on export), the json adds `refunded` (a count) and
  `refundedTotal`, the page's line reads "sends recorded: N, refunded K (X GNOT)". The tests that refunded a live
  route's send (`TestSendsRefundMigrate`, `TestPagedExport`, `TestListSkipsChangedIDs`, hlgnot's `TestSettings` and
  the R16 part of `TestSweepVoidAndGuards`) now feed the route a page carrying sends (`inherit`, the lock and
  sentTotal raised with them as page 0 carries them); `TestSendsRefundMigrate` follows the successor through its
  refund and on to the next migration, where a refunded send stays behind.
- **AQ5, the announcement names the route.** `hlmailbox.AnnounceSuccessor(cur, symbol)` records GNOT or BUBBLE
  (anything else refused; `SymbolGNOT`, `SymbolBUBBLE`), `SuccessorFor(path)` reads it, the page shows it beside each
  announced path and the json adds `successorRoutes` (the first 20, path → route; `successors` stays a list of paths).
  `hlroute.DraftMigrate(to, self, announcedFor)` takes the symbol in place of the bool: "" is unannounced as before,
  another route's is refused ("announced itself as the BUBBLE route's successor, not this GNOT route's"), both
  when the proposal is made and, through `Env.AnnouncedFor` (which replaces `Announced`), when it would apply; the
  decision's text names the route ("migrate to X (announced as the GNOT route's successor)"). The `private` rule
  for a successor's gnomod is the plan's (§5.3, §8.3) and hlwatch's, as the finding says.
- **R27, the flags follow the lock.** `AddRoute` copies every flag standing on the migrating route to the new path
  — the seat, the time, the ids or the range — so the successor's `Claim` and `Process`, which ask their own path's
  flags, find the pause the moment the migrate applies, and it clears there as any flag does: by the seat
  (`ClearFlag`, each copy the seat's own), by GovDAO's unpause, or with the seat's removal (`clearSeat` walks every
  route). `Pull`'s page 0, which moves the lock, waits under a standing payouts flag like a sweep payment (R16) on
  both routes; the later pages carry records only. Reading: the flag is copied at the migrate's apply, which is
  the right clock — a flag set on the predecessor after that guards nothing (it refuses everything anyway) and a
  guardian flags the successor by name from then on.
- **R28, a partial record set.** `DraftVoid`'s range form and `DraftMigrate` refuse while `importing` ("the
  predecessor's pages are still arriving (N so far): a range void waits for the last page"): a page brings claims
  at their old heights, inside any range closed earlier, and a migrate drafted mid-import would strand the pages
  still at the first predecessor. The list form stays open (ids name what stands). And since a range can be drafted
  before page 0 over a height the pages then fill, `Decide`'s ranged branch and `apply`'s count the credits the
  range covers now (`mustFitRange`, a pre-walk) before voiding anything and refuse above `MaxRangeClaims` ("more
  than 1000 credits in … now (a predecessor's pages brought more since the proposal was made): cancel it and split
  the range by heights"): at once the proposal survives for a retry or a cancel, after the delay the decision stays
  pending. `TestRangeAcrossPages`: 1,001 claims at one height migrated in three pages onto a successor holding two
  of its own and a queued range over that height — refused at both clocks with nothing voided.
- **R29, former recipients.** r/hlgov keeps `former`, every recipient `Execute` replaced (`Former()`, the json's
  `former`, a page line), and `mustNotBeFormer` refuses any of them as a kind 9's successor and as the recipient —
  in `NewTransferToSuccessorRequest`'s `check` and `NewSetDecisionRecipientRequest`'s (now run at creation and in
  the closure), and in `Execute`, which also refuses a swap whose target is the recipient already (a second swap to
  one program): a retired program answers Retired to every decision and NotInherited to a kind 9's
  `successor_init`, so the vote would be wasted or pend for good. `TestFormerRecipientRefused`: both retired
  programs refused at creation; a swap and a kind 9 pre-armed while their target was nobody refused once it served
  and retired (two swaps queued together and applied back to back, inside the 14 days); a stale queued swap refused
  at execution and left pending.
- **R30, the refund's lock floor.** `DraftRefund` sums its list against the lock when the proposal is made ("the
  sends sum to X, above the lock's Y") and `apply` sums the live sends again before any payment ("the lock (Y)
  holds less than the sends' X now"), the sweep payment's shape: a forgery paid before its void can leave the lock
  short of the sends a successor imported, and a payment that fails on the coin balance alone would succeed the
  moment a stranger sends the difference, the lock reading negative and the identity broken by that much.
  `TestRefundAboveTheLock`: 500 of 3,000 left, both forms refused, the queued refund waiting, applied after a refill
  with the identity whole.
- **F18 (part 2), the body bound.** `MaxDecisionBody` 600 → 512: a legacy Solana delivery of a decision carries at
  most 538 bytes of body, 529 of parameters (14 account keys and 127 bytes of fixed instruction data leave that of
  the 1,232-byte packet), so 600 sat 62 bytes above what can ever be delivered; the builders' largest body is 163
  bytes (a cancel of 16 numbers with its salt). `TestDecisionBodyBound` refuses 513 at both choke points and takes
  512 (red first: 513 passed under 600); the M1f wording above follows.

What the Go side must follow (nothing in `bridge/` was changed here but the vector generator): the relayer's
`routeFor` must no longer drop a message addressed to a migrated route — it delivers it through the successor's
`Process` (the mailbox json's `routes[path].successor`; the successor refuses it until its last page is in, a
retry like any), and the monitors read a route's `send/<id>` (`refunded` ≠ 0) and `refunds/<from>` to drop a
refunded send from what is in flight (the json's `refunded`/`refundedTotal` say when to look); hlwatch prints the
route a successor announced for beside a migrate proposal (`SuccessorFor`, the mailbox json's `successorRoutes`)
and r/hlgov's former recipients (the json's `former`). Signature changes: `hlmailbox.AnnounceSuccessor(cur realm,
symbol string)` — a successor realm calls `hlmailbox.AnnounceSuccessor(cross(cur), "GNOT")` from its init;
`hlroute.DraftMigrate(to, self, announcedFor string)` (was `announced bool`); `hlroute.Env.AnnouncedFor
func(path string) string` replaces `Announced func(path string) bool`; `hlmailbox.AddRoute(cur, path)` keeps its
shape but refuses a path that is a route already and a second migration of the caller. New readers:
`hlmailbox.Predecessor(path)`, `Successor(path)`, `SuccessorFor(path)`; `hlroute.Imported()`, `MustHaveImported()`,
`SendJSON(id)`, `RefundsPage(from)`; `hlgov.Former()`. JSON: every field the agents read keeps its name; the
mailbox's `routes[path]` adds `predecessor` and `successor`, its json adds `successorRoutes`; the routes' json adds
`refunded` and `refundedTotal` and the paths `send/<id>` and `refunds/<from>`; r/hlgov's json adds `former`.
Texts: the mailbox's refusal "only the message's recipient, or a successor of it, can take it"; a refund's "refund
X of sends imported from the predecessor (after kind 6 enrolled this realm) to their senders: ids"; a migrate's
"migrate to X (announced as the GNOT route's successor)"; a new hlgnot vector, `inOtherRealm60`, from the
generator (`HL_GEN=<repo root> go test -run TestGenGnoVectors ./hl/`), the others byte-identical.

- Verified on `bridge-m1g` (34dab6b): the 16 gno suites green under the pinned v1.2.0 (`scripts/gnotest.sh`) on the
  final tree; `python3 scripts/check-interrealm.py` with the hl* realms and the p/ packages clean (its one line is
  realm/bubbletreasury/treasury.gno:223 `func Do`, a position-2 `rlm`, outside the bridge); the e2e from nothing
  `== PASS in 827s (827s)` (run 1, logged at `~/.config/bubblerumble-jarvis/bridge-e2e-logs/m1g-run1.log`, started
  once no e2e or validator ran on the Mac and the load average was under 15 — all 32 sections, the four expected
  FAIL lines of the negative audit at lines 191–194 and nothing else before the PASS). The realms it staged were the
  tree at 34dab6b, every fix of this section included (hlwatch's build stamp `vcs.revision=34dab6b
  vcs.modified=false`), with the Go side unchanged: the json fields added here (`predecessor`, `successor`,
  `successorRoutes`, `refunded`, `refundedTotal`, `former`) break no reader, since the agents decode with
  `encoding/json` and no `DisallowUnknownFields`.

### M1h (2026-10-13): audit round 5 — the realms' R31–R34 and the red team's AR1 and AR2 (AR3's realm part is R32)

Six findings (AUDIT.md's round-5 files: the realms' R31–R34, all low, and the red team's AR1 and AR2, both
medium; AR3's realm part is R32), six commits on `bridge-m1h`, every fix with a test proved red on the unfixed
code first — a behavioural red wherever the test could be written against the old API (R31's realm refusals
naming the wrong reason, AR2's re-point refused and the mailbox's second registration refused, R34's full meter
paying at the successor's first Claim, AR1's refund taken with no enrolment), a compile-red where the test needs
a reader or a builder the old code lacks (R31's `DraftMigrate(to, self, env)`, R32's `MustTake`, R33's
`SendItem.Refunded`, AR1's `Enrolled` and `NewWriteOffRequest`) — the siblings of each pattern found by a grep
across p/hlroute, hlgnot, hlbubble, hlmailbox and hlgov. Read first, as AGENTS.md asks:
`docs/gno-specs/gno-interrealm-v2.md` §2 (the two contexts: a route's `Process` refuses under its own authority
before the mailbox marks, so a successor's wait before page 0 is the route's own panic and the mark reverts with
it; r/hlgov's kind-6 closure crosses into the mailbox to note an enrolment, the note the mailbox's own
allocation), §3.2 (storage = authority: a page's sends are plain values the successor allocates as its own, the
refund and write-off marks with them; a re-point drops the mailbox's own route record and flag copies and
allocates new ones), §4.1 (every `Env` closure runs under the realm that declared it — `Predecessor`, `IsRoute`
and `Enrolled` are the route's own reads of the mailbox), §5.2–§5.3 (`cur` as the capability: `NoteEnrolled`
reads r/hlgov's exact pkgpath from `cur.Previous()` under `IsCurrent()`, `AddRoute`'s re-point from the route's;
the enrolment, the succession and every mark are ints and strings, no realm value kept; `DraftMigrate` and
`DraftRefund` take an `Env` by value and store none of it — a `Draft` carries parameters only), §5.5 (pkgpath
keys stay exact: `hlgovPath` and `mustBeRoute` compare whole paths, so no `host#subpath` token notes or
re-points), §9 (a panic crossing a boundary aborts the whole transaction: every new gate — page 0 not in, a
migration under way, a route already, an enrolment younger than 7 days — is a panic that leaves the proposal or
the decision pending, never spent, and a re-point refused at `AddRoute` reverts `MigratedTo` with it), §11.3;
`docs/gno-specs/gno-security-guide.md` §2.1, §3(C) (no caller-supplied function or interface value is invoked:
the `Env` closures are the route's own), §5.1/§5.1a (`Enrolled`, `MustTake`, `SendJSON` return ints, nothing or
strings; no pointer into route or mailbox state leaves a realm), §5.6 (identity from `cur.Previous()` under
`IsCurrent()` in `NoteEnrolled` and `AddRoute`), §6.1, §8; `python3 scripts/check-interrealm.py` clean on hl*
and p/ (its one line stays realm/bubbletreasury/treasury.gno:223 `Do`, outside the bridge). The readings chosen:

- **R31, a migrate of a successor that pulled nothing is refused.** `DraftMigrate(to, self, env)` (the `Env` in
  place of the announced symbol) and the migration's apply ask the mailbox for this route's predecessor
  (`Env.Predecessor`, hlmailbox.Predecessor of the realm's own path) and refuse while a registered successor has
  imported no page ("page 0 is not in yet: this route succeeds X and has pulled nothing; a migrate waits for the
  last page of its own import"): its `MigratedTo` would make its `Import` refuse, the predecessor's `Pull` revert
  for ever and the lock sit on the first predecessor, which refuses everything, with no decision that reaches it
  (R28's "still arriving" covers the pages after the first). A path the mailbox registers as a route already is
  refused when the proposal is made (`Env.IsRoute`: "the successor X is a route already"), whatever it
  announced, not a week on by `AddRoute`. Both checks live in `mustMigrate`, asked at every clock (AR2 adds
  `Decide`). Reading: a realm announced but not yet registered is a launch route to its state (its predecessor
  ""), so a migrate drafted then and registered by the time it applies is caught at the apply.
- **AR2, before page 0 nothing has moved, so a migration is re-pointed and never stranded.** A migrated route
  whose successor pulled nothing (`exported == 0`: page 0 moves the lock and the counters, and `Export` counts
  page 0 whatever it carries) takes a second migrate to another successor, after 7 days like the first
  (`mustAllowMigrate`: the same successor refused — "a re-point names another successor" — and every other
  builder refusing as before; once page 0 is pulled "a migration under way is never re-pointed", at the draft,
  at `Decide` and when a decision taken before the pull would apply, pending for a cancel, the one decision
  left). `Decide` re-checks every migrate against the live state and `Execute` lets a migrate through on a
  migrated route; a migrate of a successor whose pages are still arriving is refused at all three clocks.
  `hlmailbox.AddRoute` re-points: the first successor's record and the flags copied to it are dropped (it is a
  route no more — nothing addressed to the predecessor is its to take, no route power is its; `RouteDropped`),
  the new path registered with the flags standing on the route now; a first successor that migrated on holds
  the lock and is refused ("the migration is under way and cannot be re-pointed"). The mailbox trusts the
  route's own `exported` (the route is the authority on its migration); the announcement of the dropped
  successor stays, as announcements always do.
- **R32 (AR3's realm part), a successor credits nothing before its last page is in, its own messages included.**
  `hlroute.MustTake(toSelf, predecessor)` is the gate both routes' `Process` ask before the mailbox is (the
  predecessor from `hlmailbox.Predecessor(pkgPath)`): while the route has a predecessor every message waits until
  the import has ended ("this route succeeds X: nothing is credited before the last page of the migration is in
  (N pages so far)"), not only one addressed to the predecessor (`MustHaveImported`, AQ1) — before page 0 the
  lock is not here, so a burn to a successor that kind 6 enrolled the day the migrate applied was swept as one
  above the lock, owed and paying nobody until a sweep payment by vote. A launch route takes its own as before
  and refuses another realm's for good. Reading: the mailbox's record of the succession, not the realm's own
  code, is what makes it a successor — storage is authority — so the realm asks the mailbox at every message.
  The relayer's side (waiting for the successor's import before delivering a successor-addressed burn) is the
  Go lane's, AR3.
- **R33, a refunded send's mark survives the next migration.** The page carries every send with its refund time
  (`SendItem.Refunded`); `Import` keeps the mark, puts the send in the `refunds` index and counts it (the json's
  `refunded` and `refundedTotal`, `refunds/<from>`); `Export` takes the counters down as the records leave; a
  write-off's mark (AR1) travels the same way. Before, `Export` dropped the refunded sends while the
  predecessor's json kept counting them and its `send/<id>` answered null, so a monitor re-deriving what is in
  flight found no realm that called the send refunded and carried it for good. Reading: the counters follow the
  records ("refunded here" is "the refunded sends held here"), so after a migration the predecessor says 0 and
  the successor counts them — one realm always answers; the Go's `sendOnPair` asks the route and then its
  migration partner and so finds the moved mark within a pair.
- **R34, a successor's meters start empty.** `Import(now, epoch, launchedAt, snap)` empties the bridge meter and
  every quarter as of now when page 0 lands (`ResetMeters(now)`) and takes hlmailbox's launch time as the one
  the meters were last emptied for (`LaunchSeen`), so the realm's first `Claim` applies no launch reset: before,
  the successor's `syncLaunch` found `LaunchSeen` 0, rewound every meter to the launch, months back, and the
  refill filled them all at once — a third cap in the 24 hours the predecessor closed, drawn by four addresses
  before a watch's second poll while the page (`meterView`) showed the meter full. The queues a later page makes
  start empty at their first claim, as every queue does; the view and the json show the meter empty after page
  0 (K22's rule). Left as is: a successor's page before page 0 still shows the launch reset's full meter — it
  holds nothing to pay then (R32 refuses every credit, a hand credit is bounded by a lock of 0).
- **AR1, a refund waits for kind 6, and a refunded send delivered after all is written off.** r/hlgov's kind-6
  closure, for a router of this chain's domain, notes with the mailbox the route it enrolled and the height
  (`hlmailbox.NoteEnrolled(cross(cur), route, height)`, r/hlgov's exact pkgpath alone — `mustBeHlgov`; the
  decision's note says "X noted enrolled on Solana at height H"); `Enrolled(path)` reads the height and the time,
  the json's `routes[path]` adds `enrolled` and `enrolledAt`, the page a line. `DraftRefund(ids, now, env)` and
  the refund's apply refuse until that note is 7 days old (`Env.Enrolled`, `mustBeEnrolled`: "no kind 6 has
  enrolled this route on Solana yet (none dispatched from r/hlgov): a refund waits 7 days after the enrolment",
  "kind 6 enrolling this route was dispatched at height H, N s ago: a refund waits 7 days after it (until T)";
  the gate before the ids, since it is the fact that matters most) — a refund that landed before kind 6 could (a
  hold's lift leaves a day in which it cannot) let one stranger's `Reemit` and delivery of the refunded send mint
  it twice, with no builder to close the identity. Reading: the latest note counts — a later kind 6 for the same
  route restarts the wait; a refund is irreversible and a week is cheap. Necessary, not sufficient: the realm
  cannot see Solana, so hlwatch's alarm on a refund proposal whose successor the token has not enrolled, and its
  DELIVERED line beside any id Solana processed, are the Go lane's. The write-off (`NewWriteOffRequest`,
  `DraftWriteOff`, `KindWriteOff` "write off", 7 days) books a refunded send delivered on Solana after its refund
  as lost (`Lost += amount`, so lock = sentTotal − paidTotal = supply + owed + voided − lost closes) and marks it
  (`Send.WrittenOff`; `send/<id>`'s `writtenOff`, the json's `writtenOff` and `writtenOffTotal`, the page's
  ", written off W (Y)" when any, the mark carried by the page): only a refunded send, once ("is not a refunded
  send: only a send delivered on Solana after its refund is written off", "is written off already"); one written
  off by another decision since the proposal was made is skipped and counted, R22's rule. The description names
  hlwatch's DELIVERED line, since the realm cannot see the delivery. No coins move, so no payouts flag gates it.

What the Go side must follow (nothing in `bridge/` was changed here): a refunded send now moves with its page
(relayer_sol.go's "a refunded one stays" is wrong; `sendOnPair` finds the mark on the partner already, and a
route's `refunded`/`refundedTotal` fall to 0 after its last page while the successor's rise); a route's
`migratedTo` and the mailbox's `routes[path].successor` can change to another path before page 0 (a re-point),
the old successor's entry vanishing from `routes` — `migrationOf`/`migrationFor` re-read, never cache, and the
monitors alarm a change; hlwatch reads a refund proposal against the mailbox's `routes[path].enrolled` and
`enrolledAt` (none or younger than 7 days: alarm) and prints DELIVERED beside any send id Solana processed,
and reads a write-off proposal's ids against that set the other way (an id Solana never processed is a write-off
of nothing); the relayer's successor-addressed branch waits for `imported > 0 && !importing` (the realm refuses
with "this route succeeds X: nothing is credited before the last page" — a retry, not a deterministic refusal
to park on), AR3's Go part; hlwatch and the monitors read `writtenOff` beside `refunded` (a written-off send
is lost, not in flight). Signature changes: `hlroute.DraftMigrate(to, self string, env Env)` (was `announcedFor
string`); `hlroute.DraftRefund(ids string, now int64, env Env)` (was `ids`); `hlroute.Import(now, epoch,
launchedAt int64, snap *Snapshot)` (was `now, epoch, snap`); `hlroute.Env` gains `Predecessor func() string`,
`IsRoute func(path string) bool`, `Enrolled func() (height, at int64)` — a realm sets all three;
`hlroute.SendItem` gains `Refunded` and `WrittenOff`, `hlroute.Send` gains `WrittenOff`. New: `hlroute.MustTake`,
`DraftWriteOff`, `KindWriteOff`; `hlmailbox.NoteEnrolled`, `Enrolled`; `hlgnot.NewWriteOffRequest`,
`hlbubble.NewWriteOffRequest`; `hlmailbox.AddRoute` re-points a migrated caller's successor (a second call
while the first pulled nothing). JSON: every field the agents read keeps its name; the mailbox's `routes[path]`
adds `enrolled` (height) and `enrolledAt` (unix time); a route's json adds `writtenOff` and `writtenOffTotal`;
`send/<id>` adds `writtenOff`. Page: the mailbox's route line adds ", enrolled on Solana by kind 6 at height H
(T)"; a route's counts line adds ", written off W (Y)" when any; the decisions page gets "### write off". Events:
`RouteDropped` (pkgpath, flags), `RouteEnrolled` (pkgpath, height). Texts: a migrate's "page 0 is not in yet: …",
"the successor X is a route already", "migrated to X already (nothing pulled yet): a re-point names another
successor", "migrated to X: the successor pulled N pages already (the lock moved with page 0); a migration under
way is never re-pointed"; the mailbox's "X migrated to Y, which migrated on to Z already: the migration is under
way and cannot be re-pointed", "only …/hlgov notes an enrolment, not …"; a message's "this route succeeds X:
nothing is credited before the last page of the migration is in (N pages so far)"; a refund's two enrolment
refusals above; a write-off's "write off X of refunded sends delivered on Solana after their refund (hlwatch's
DELIVERED line names the id): ids". Not done, named for a later round: kind 6 does not check that the route it
enrolls is the token's own (a kind 6 enrolling hlbubble2 as GNOT's router passes `routePathOf`; the mailbox
could answer a route's symbol from its chain's root); AR4–AR7 are not realm items.

- Verified on `bridge-m1h` (95e3869): the 16 gno suites green under the pinned v1.2.0 (`scripts/gnotest.sh`) on the
  final tree; `python3 scripts/check-interrealm.py` with the hl* realms and the p/ packages clean (its one line is
  realm/bubbletreasury/treasury.gno:223 `func Do`, a position-2 `rlm`, outside the bridge); the e2e from nothing
  `== PASS in 755s (755s)` (run 1, logged at `~/.config/bubblerumble-jarvis/bridge-e2e-logs/m1h-run1.log`,
  started once no e2e or validator ran on the Mac, the load average under 15 and 21 GB free — all 32 sections,
  the four expected FAIL lines of the negative audit at lines 191–194 and nothing else before the PASS). The realms it staged were the tree at 95e3869,
  every fix of this section included, with the Go side unchanged: the json fields added here (`enrolled`,
  `enrolledAt`, `writtenOff`, `writtenOffTotal`) break no reader, since the agents decode with `encoding/json`
  and no `DisallowUnknownFields`; the e2e drives no kind 6, refund, write-off or migration.

### M1i (2026-10-13): audit round 6 — the realms' R35–R39, the red team's AS1, AS2, AS5 and AS6, the assumptions audit's F27 and F28, the fact check's C384

Six commits on `bridge-m1i`, one per finding (or per findings on the same lines), every fix with a test proved
red first — a behavioural red wherever the gate could be reached on the old code (a dropped route's `Dispatch`
and `Process` taken, `MustSend` silent, a kind 6 enrolling an un-pulled or the other token's route, a refund at 7
days, a restore refusing a send's id, a send to a registered successor's address taken), a compile-red where the
test needs a reader the old code lacks (`Dropped`, `Pulled`, `NotePulled`, `Importing`, `RouteSymbol`,
`ClearEnrolled`), each then run again with the reader alone before the gate was wired — and the siblings of each
pattern found by a grep across p/hlroute, hlgnot, hlbubble, hlmailbox and hlgov (hlgnot2 now among them). Read
first, as AGENTS.md asks: `docs/gno-specs/gno-interrealm-v2.md` §2 (the two contexts: every new gate panics
under the route's or the mailbox's own authority before a mark or a coin moves, so a refused send, credit, kind 6
or cancel leaves nothing — `MustSend` before the fee moves, the dropped-route refusals before `done.Set`), §3.2
(storage = authority: the tombstones, the `dropped` lists, `pulled` and the cleared note are the mailbox's own
allocations; hlgnot2's `Import` copies a page's plain values into allocations of its own, the predecessor's
`*Snapshot` read and never kept), §4.1 (every `Env` closure and every builder closure runs under the realm that
declared it; hlgnot2's `Adopt` crosses into hlgnot's `Pull` from hlgnot2's own frame, so `Pull` reads hlgnot2's
exact pkgpath from `cur.Previous()`), §5.2–§5.3 (`cur` as the capability: `NotePulled` reads the predecessor's
exact pkgpath, `ClearEnrolled` r/hlgov's, `Dispatch` and `Process` the caller's, each under `IsCurrent()`; the
dropped list, the tombstone, `pulled`, the route and height a kind 6 keeps are strings and ints, no realm value
kept; hlgnot2's `init(cur realm)` crosses once at deploy as hlverifier's does), §5.5 (pkgpath keys stay exact:
`Dropped`, `NotePulled`'s successor check, `RouteSymbol`'s switch, hlgnot2's compiled predecessor), §9 (a panic
crossing a boundary aborts the whole transaction: a refused `Adopt` reverts `Pull`'s payment and export with it,
so the pair's sum never breaks for a block — the identity `TestMigration` reads at every step), §11.3 and §12.3
(hlgnot2's `init` runs with the deployer as previous realm and keeps nothing of it); `docs/gno-specs/gno-
security-guide.md` §2.1, §3(C) (no caller-supplied function or interface value is invoked under a realm's
authority: hlgnot2's test verifier is a type declared in the test's own realm, as hlgnot's), §5.1/§5.1a (`Dropped`,
`Pulled`, `RouteSymbol`, `Importing`, `Imported`, `Predecessor` return strings, bools and ints; `Adopt` returns
nothing; no pointer into route or mailbox state leaves a realm), §5.5 (hlgnot2's `TransferRemote` keeps
`IsUserCall()` and `AssertOriginCall()`), §5.6, §5.8 (hlgnot2's `unsafe.OriginSend()` beside `cur` is the one
`OriginSend` v1.2.0 offers, as hlgnot's; `PreviousRealm()` appears nowhere), §6.1, §8; `gno-ai-contract-review.md`
cases 1, 3, 6, 9 and the checklist; `python3 scripts/check-interrealm.py` clean on hl* and p/ (its one line stays
realm/bubbletreasury/treasury.gno:223 `Do`, outside the bridge). The readings chosen:

- **R35, AS1, F28: a re-pointed-away successor is never Solana's router and never holds anything — by
  construction, with belt and braces.** (a) A successor takes no send before its last page is in:
  `hlroute.MustSend(predecessor)` is the mirror of `MustTake`, asked by both routes' `TransferRemote` (and
  hlgnot2's) right after the pause check and before the fee moves ("this route succeeds X: nothing is sent
  before the last page of the migration is in (N pages so far)"). Before page 0 nothing has moved and the
  migration may still be re-pointed, dropping the realm with the sender's coins in it — a realm Solana never
  enrols, that cannot refund (its own send) and cannot migrate (a route no more). (b) r/hlgov's kind-6 builder
  and its closure refuse a successor that has pulled nothing: the predecessor's `Pull` notes page 0 with the
  mailbox (`hlmailbox.NotePulled(cross(cur), successor)`, the predecessor's exact pkgpath, for its own successor
  alone, idempotent), the mailbox answers `Pulled(path)`, and kind 6 requires `Predecessor(path) == "" ||
  Pulled(path)` ("X succeeds Y and has pulled nothing yet (page 0 is not in): a kind 6 enrolling it waits for its
  first page, since a migrate that pulled nothing may be re-pointed and Solana must never name a dropped route").
  A re-point admits only a successor that pulled nothing (AR2's `exported == 0`), and `AddRoute` now refuses one
  whose successor the mailbox knows pulled ("X migrated to Y, which pulled page 0 already: the migration is under
  way and cannot be re-pointed") — two sources of the same fact, in lockstep since both are the predecessor's own
  `Pull`. So no re-point can ever drop a route Solana names. (c) Belt and braces: the mailbox remembers every
  dropped successor on its predecessor's record (`routeRec.dropped`, the json's `dropped` list) and as a tombstone
  (`droppedRoutes`, `Dropped(path)` = the predecessor that re-pointed away from it; cleared by `undrop` if a later
  migrate registers the path afresh — a re-point back while the newer successor pulled nothing, or a later hop
  naming it); `Dispatch` refuses a dropped caller ("X is a route no more (its predecessor Y re-pointed the
  migration to another successor): it sends nothing"); `Process` refuses it before anything ("…: the line's last
  realm takes this message"), a message addressed to itself included — as a launch route it would have taken that
  against a lock of 0 (R35's strand); both routes' `TransferRemote`, `Process` and `Claim` refuse a dropped self
  (`mustNotBeDropped`: "a route no more: Y re-pointed its migration to another successor; the line's last realm
  takes every message and send"); and `successorOf` counts each hop's dropped successors as that hop's, so the
  line's live last realm takes a message addressed to a dropped successor once its import has ended — at the
  realm it is `MustTake(false, pred)`, the same wait as a predecessor-addressed message. Reading: the dropped
  realm is a tombstone in the mailbox, not a hop of the chain (nothing addressed to it is ever its own again), and
  the fact "page 0 pulled" lives where a re-point and a kind 6 both read it. The closure case of (b) is
  unreachable by construction — `pulled` never goes true→false, a path is a successor from its registration on,
  and every ordering refuses at creation — so the closure's refusal is proven by the migrated case (R36), the
  same `check` at both clocks.
- **R36: kind 6 enrols the token's own live route alone.** `hlmailbox.RouteSymbol(path)`: the launch paths'
  compiled symbols, else `SuccessorFor(path)` (a registered successor always announced, AQ5), "" otherwise.
  r/hlgov's kind-6 `check`, at creation and in the closure: `RouteSymbol(path) == the token named` ("X is the
  BUBBLE route's, not GNOT's: a kind 6 names the token's own route"; "X is registered for no token (it never
  announced the route it succeeds)…" for a path registered without an announcement, reachable only by the
  mailbox's own call in tests), then `Successor(path) == ""` ("X migrated to Y: never a migrated route, since its
  successor is the token's router"), then R35's pulled check. The note then lands only on the token's own live
  route. The closure case is tested with a kind 6 pre-armed for hlgnot before its migrate applied, refused at
  execution with the note untouched.
- **R37, AS2, F27: a refund waits 14 days on the enrolment note, and a cancelled kind 6 clears it.**
  `hlroute.EnrolmentWait = 2*decisions.Delay`; `mustBeEnrolled` refuses until `now ≥ at + EnrolmentWait` ("kind 6
  enrolling this route was dispatched at height H, N s ago: a refund waits 14 days after it (until T): a week for
  the delivery and a week for Solana's own clock, which executes a kind 6 7 days after recording it; a hold defers
  it further, which hlwatch reads there"; "no kind 6 has enrolled this route on Solana yet (none dispatched from
  r/hlgov): a refund waits 14 days after the enrolment, …"); the refund's description reads "(14 days after kind
  6's dispatch enrolled this realm)". `hlmailbox.ClearEnrolled(cross(cur), path)` — r/hlgov's exact pkgpath alone,
  a route, a no-op when nothing is noted — and r/hlgov's kind-1 closure calls it for every number it cancels that
  is a kind 6 of this chain's domain whose noted height is still the route's latest note (the kind-6 closure keeps
  `route` and `height` on its `decision`; a list and a range cancel alike, the range walked over the dispatched
  numbers only); the cancel's note adds "; the enrolment note of X cleared (kind 6 #n cancelled): its refund
  clock restarts with a re-proposal". Reading: the realm's gate is a floor that now covers Solana's own clock in
  the normal case; what it still cannot see — a hold, a lost number — hlwatch and the relayer read on Solana (the
  Go lane's half: F27's `refundCheck` reading the token's enrolled router, `executes` not applying a refund while
  the router is not the successor). A cancel of a kind 6 Solana had executed already clears the note too: the
  safe direction (refunds refused until a re-proposal the admin program spends as a no-op restarts the clock).
  Two kind 6 for one route at one height would be cleared together by a cancel of either — the same direction.
- **R38: a write-off has its inverse.** `DraftRestore` accepts a written-off send's id (`WrittenOff != 0`; a
  send not written off: "X is a send that is not written off: nothing to restore"), queued 7 days like a forged
  claim's restore (the note "N withheld restored at once, M forged, or written off, queued 7 days"), and the
  apply clears the mark and takes the amount back out of lost with the counters (`restoreWriteOff`:
  `WrittenOff = 0`, `Lost −= amount`, `nWrittenOff−−`, `writtenOffTotal −= amount`), the note "; N restored (K
  write-offs undone: the sends are lost no more)"; `send/<id>` and the json show the mark gone; the refund mark
  stays and the send may be written off again; the restore reaches the mark on the next route of the line too.
- **R39: a burn naming a registered route's address is swept.** `isRealm` in both routes (and hlgnot2) also
  matches every route the mailbox registers (`hlmailbox.Routes()`, `chain.PackageAddress`): refused as a recipient
  at send, at a credit or sweep payment by vote, and swept at the credit, to be credited to its owner by id — a
  handful of reads, the routes grown by migration alone.
- **AS5, AS6, C384: `realm/hlgnot2`, the migration run end to end.** A GNOT route realm at
  `gno.land/r/<ns>/hlgnot2` (the e2e's and gnotest.sh's path; mirror of hlgnot's gnomod and layout, the symbol
  GNOT, `predecessorPath = …/hlgnot` compiled in, the predecessor among its compiled `realms`), which calls
  `hlmailbox.AnnounceSuccessor(cross(cur), "GNOT")` from `init(cur realm)`; a permissionless
  `Adopt(cur realm, page int)` — anyone, page by page from 0, as `Claim` is: it refuses before Launch, a mailbox
  predecessor other than the compiled one ("no migrate decision of …/hlgnot names this realm yet: nothing to
  adopt"; "this realm succeeds …/hlgnot by its source, and the mailbox registers it as X's successor"), a page out
  of order ("page N is the next to adopt, not M") and a page past the last ("the migration's last page is in
  already: nothing more to adopt"), then calls `hlgnot.Pull(cross(cur), page)` and
  `st.Import(now, epoch, hlmailbox.LaunchedAt(), snap)` in the same call (page 0 under the predecessor's payouts
  flag waits there; the meters start empty, R34; `importing` cleared at the last page) and emits `Adopted`. Every
  rule hlgnot has is kept — the same `TransferRemote`, `Process`, `Claim`, builders, `Execute`, `Pull` (with
  `NotePulled`), pages — the shared code staying in p/hlroute (`MustSend`, `Importing` added there, nothing
  copied). Its json adds `predecessor` (the compiled path); its page a succession line ("succeeds `X`: migrate
  applied, page 0 not adopted yet (anyone may call Adopt(0)); nothing is sent or credited before the last page" /
  "…: N pages adopted, more to come (anyone may call Adopt(N))…" / "…: the migration is complete (N pages
  adopted); a route like any other"). `TestMigration` (realm/hlgnot2/hlgnot2_test.gno) drives the whole thing in
  gno under the real GovDAO: hlgnot takes 499 sends and three burns (502 records: two pages), its migrate names a
  first successor that pulls nothing (hlgnotx; kind 6 refuses it), GovDAO re-points to hlgnot2 under a
  guardian's payouts flag (hlgnotx dropped: its `Dispatch`, `Process` and `MarkDone` refused), before page 0
  hlgnot2 takes no send, credits nothing (a message to itself, to hlgnotx, to hlgnot all wait), is no router
  (kind 6 refused) and `Adopt(0)` waits under the flag, then a stranger's `Adopt(0)` moves the lock, the counters
  and 500 records with the pair's five counters, lock and balance summed unmoved (`samePair` at every step), a
  re-point is refused ("pulled 1 pages already"), kind 6 enrols hlgnot2, between pages the same refusals hold and
  `Claim` finds empty meters, another stranger's `Adopt(1)` ends the import, hlgnot2 takes the three waiting
  messages and sends, a refund of an imported send is refused at once and at 13 days and taken at 14 (7 more to
  apply), a write-off books lost and its restore closes the identity on the old supply, a burn to a registered
  route's address is swept and credited by id, and hlgnot2 migrates on to hlgnot3, whose `Pull` of page 0 is noted
  pulled. Two harness facts learned there, written into the test: a user override `testing.SetRealm` makes
  inside a helper binds to the helper's frame and a crossing call into ANOTHER package after it does not see it
  (`IsUserCall()` fails) while an in-package call does, so hlgnot2's test sets the user realm in its own frame
  before each cross-package `TransferRemote`; and the harness carries what earlier envelopes spent (the fees)
  into every later context, so a loop of small sends needs `testing.SetOriginSpend(nil)` per envelope (hlgnot's
  tests never noticed: their envelopes are large). Not in the page: a successor compiles its own settings (the
  router, fee, floors, maximum) at deploy — `Snapshot` carries counters and records, as before — so GovDAO re-sets
  on the successor any that moved on the predecessor since its deploy (the e2e drill below swaps hlgnot2's
  `launchRouter` as it swaps hlgnot's).

What the Go side must follow (nothing in `bridge/` was changed here). JSON: the mailbox's `routes[path]` adds
`pulled` (bool: this successor pulled page 0) and `dropped` (the successors this route's re-points dropped, a
list of pkgpaths), and the mailbox json adds `droppedRoutes` (object: dropped pkgpath → the predecessor that
re-pointed away from it) between `routes` and `successors`; hlgnot2's json adds `predecessor` (its compiled
predecessor's pkgpath) after `live`; every field the agents read keeps its name (`enrolled`, `enrolledAt`,
`writtenOff`, `importing`, `imported` unchanged). Page: the mailbox's route line adds ", page 0 pulled" and
", dropped `a`, `b`", and a "- dropped (routes no more, re-pointed away): `X` (by `Y`)" line before the
announced successors; hlgnot2's page the succession line above. Behaviour the tools must mirror: a route's
line now has tombstones — a path in `droppedRoutes` is no hop of any line (`followLine`/`predecessorsOf` never
reach it; its `migratedTo` and json are a dead realm's) but a burn addressed to its address is the line's last
realm's to deliver once its import ended (the relayer's successor-addressed branch applies to a dropped-route
address too: `deliveryTarget` = the line's last realm, waiting on `imported > 0 && !importing`); `router:unknown`
should alarm, not note, while the enrolled router is a dropped route (AS1's ask); hlwatch's refund rule reads 14
days from `enrolledAt` (its "UNDER THE 7-DAY DELAY" text becomes 14), and reads Solana's enrolled router and the
kind 6's state beside the note (F27, AS2 — the realm's note can now be cleared by a cancel, so an `enrolled` of 0
after a dispatch means a cancel); the realm refuses a kind 6 for a successor with `pulled` false (hlwatch prints
"NOT PULLED: page 0 is not in" beside such a proposal) and for a route whose `RouteSymbol` is not the token's or
whose `successor` is set; a restore proposal may name a send id (a written-off send: hlwatch prints it as a
write-off's undo); `isRealm` covers every registered route's address (hlsol's "is the address of X, a bridge
realm" refusal should read the mailbox's routes too). Signature changes: none to existing functions. New:
`hlroute.MustSend(predecessor string)`, `(*State).Importing() bool`, `hlroute.EnrolmentWait` (= 2·Delay);
`hlmailbox.Pulled(path) bool`, `NotePulled(cur, path)` (a route, for its own successor), `Dropped(path) string`,
`RouteSymbol(path) string`, `ClearEnrolled(cur, path)` (r/hlgov alone); `hlgnot2.Adopt(cur, page)`,
`hlgnot2.Predecessor() string`, `Imported() int`, `Importing() bool` and hlgnot's full surface under
`gno.land/r/<ns>/hlgnot2`. Events: `RouteDropped` gains `predecessor`; new `RouteUndropped` (pkgpath,
predecessor), `RoutePulled` (pkgpath, predecessor), `RouteEnrolmentCleared` (pkgpath), `Adopted` (from, page,
lock, claims, sweeps, sends, more). Texts: listed under each finding above; a dropped realm's "a route no more…"
family; the refund's "14 days" family and description; a kind 6's three refusals; the restore's "is a send that
is not written off: nothing to restore", "forged, or written off, queued 7 days", "(K write-offs undone: the
sends are lost no more)"; the cancel's "; the enrolment note of X cleared (kind 6 #n cancelled): its refund clock
restarts with a re-proposal"; hlgnot2's "page N is the next to adopt, not M", "the migration's last page is in
already: nothing more to adopt", "no migrate decision of X names this realm yet: nothing to adopt".

The e2e (the Go lane extends bridge/local/e2e.sh after this merge; the one whole-tree run is its): stage and
`addpkg` hlgnot2 after hlgnot (its `init` announces it; `swap` its `launchRouter` as hlgnot's is swapped —
the same GNOT program); the 7-day clocks must pass on a local chain, so stage `p/decisions` with `Factor`
swapped (e.g. `Factor = int64(6048)`: `Delay` 100 s, `Deadline` 200 s, `EnrolmentWait` 200 s — every builder's
proposal must then execute within 200 s of its creation, which `propose_and_execute` does at once) or run the
drill on the factor-8 build with a longer clock; then: `propose_and_execute` hlgnot's `NewMigrateRequest` naming
hlgnot2, `sleep` past `Delay`, `callpath …/hlgnot Execute <id>` (the id from `hlgnot:json`'s `decisions`), check
`hlmailbox:json`'s `routes[hlgnot2].predecessor` and hlgnot's `migratedTo`; a burn to hlgnot (addressed to the
predecessor) and one to hlgnot2 delivered now must both be refused by the realm ("nothing is credited before the
last page") and park; `callpath …/hlgnot2 Adopt 0` from any key (test1 is fine) and `Adopt 1` if `hlgnot2:json`
says `importing` (one page in the e2e's sizes), then `hlgnot:json` reads `lock` 0, `sends` 0 and `hlgnot2:json`
the whole lock, the summed counters unmoved (hlwatch's and the monitors' line sum); the relayer then delivers
both parked burns to hlgnot2 (the line's last realm) and takes hlgnot2's sends as the line's; `propose_and_execute`
hlgov's kind 6 for token 0 naming hlgnot2's address (refused before `Adopt 0`, taken after; `hlmailbox:json`'s
`routes[hlgnot2].enrolled`), the admin program executing it after its clock (7 days ÷ factor on the programs'
side); a `TransferRemote` on hlgnot2 refused before `Adopt 0` and taken after; a refund proposal of an imported
send refused before `EnrolmentWait` and taken after, `Execute` after `Delay`; a write-off of it and the restore
(two 7-day decisions); and, as a separate drill from a fresh chain, the re-point: a migrate to a second announced
path that never adopts, a re-point to hlgnot2 after `Delay`, `hlmailbox:json`'s `droppedRoutes` naming the first,
its `Dispatch` refused, and a kind 6 naming it refused at creation. Each step's realm answers are the texts above.

- Verified on `bridge-m1i` (the final tree): the 17 gno suites green under the pinned v1.2.0 (`scripts/gnotest.sh`,
  hlgnot2 the 17th, 46 s wall under nice); `python3 scripts/check-interrealm.py` with the hl* realms, hlgnot2 and
  the p/ packages clean (its one line is realm/bubbletreasury/treasury.gno:223 `func Do`); gofmt clean on
  realm/. The e2e was not run here (the Go lane runs the one whole-tree e2e after the merge); the json fields
  added break no reader, since the agents decode with `encoding/json` and no `DisallowUnknownFields`.

### M1j (2026-10-13): the final check's realm fixes — the red team's AT2, AT6, AT7 and AT8

Four commits on `bridge-m1j` (cut from `bridge` at 02ee372c), one per finding, every fix with a test proved red
first — a compile-red where the test needs a builder, a reader or a signature the old code lacks
(`DraftApplyRefund`/`NewApplyRefundRequest`, `DroppedRoutes`, `MustSend`'s second parameter), then the same test
run again with the builder or the signature in and the gate out, red on the behaviour (`Execute` paying a matured
refund in p/hlroute, hlgnot, hlbubble and hlgnot2; a dropped route's address passing `isRealm` in hlgnot, hlbubble
and hlgnot2, each send running on to the next check; a successor's send taken with no enrolment note in p/hlroute
and hlgnot2), then green — and the siblings of each pattern found by a grep across p/hlroute, hlgnot, hlbubble,
hlgnot2, hlmailbox and hlgov. Read first, as AGENTS.md asks: `docs/gno-specs/gno-interrealm-v2.md` §2 (the two
contexts: the apply's refund pays through the realm's own `Env.Pay` closure under the realm's authority, and every
new refusal — `Execute`'s on a refund, `refundToApply`'s, `MustSend`'s note gate, `isRealm`'s dropped walk —
panics before a coin or a mark moves), §3.2 (storage = authority: the apply-refund decision is the realm's own
ledger's allocation; `DroppedRoutes` hands out a fresh slice of strings, never the mailbox's tree), §4.1 and §4.3
(the builders' closures declared in a realm run under it; `MustSend`'s `Env` is the realm's own closures, built by
its `env(cur)`, so a /p/ gate invoking them runs the realm's own authority and nobody else's, as every `Env` call
before), §5.2–§5.3 (`cur` as the capability: `NewApplyRefundRequest` goes through `propose`, which proves
`cur.IsCurrent()`, and its closure reads GovDAO's exact pkgpath from `cur.Previous()`; `applyRefundParams` holds
an `int64`, no realm value kept), §5.5 (pkgpath keys exact: a dropped path's address by `chain.PackageAddress`
equality), §9 (a panic crossing a boundary aborts the whole transaction: `Decide`'s apply-refund branch makes
every check and the apply before it records, so a refused apply leaves no record even where a test's `recover`
does not roll back — `TestRefundAboveTheLock` reads `Ledger.Last()` unmoved), §11.2–§11.3 and §12.1 (the new
public function takes `cur`; `DroppedRoutes` returns values); `docs/gno-specs/interrealm_v2.adr.md` (the `cur
realm` token and `cross(cur)`; `testing.SetRealm` observation-only, why hlgnot2's test sets the user realm in its
own frame before each `TransferRemote`); `docs/gno-specs/gno-security-guide.md` §2.1, §3(C) (no caller-supplied
function or interface value runs under a realm's authority: `MustSend(predecessor, env)` takes the realm's own
`Env`, as `mustBeEnrolled` and `mustMigrate` do), §5.1/§5.1a (`DroppedRoutes` a copy; `DraftApplyRefund` a `Draft`
value; no pointer into route or mailbox state leaves a realm), §5.5 (`TransferRemote` keeps `IsUserCall()` and
`AssertOriginCall()` behind the new gate), §5.6, §5.8 (no `unsafe.PreviousRealm`), §6.1, §8;
`gno-ai-contract-review.md` cases 1, 3, 4, 6, 9 and 10 (the apply's description is the refund's own builder-made
text and a decision number, no free text) and the checklist; `python3 scripts/check-interrealm.py` clean on hl*,
hlgnot2 and p/ (its one line stays realm/bubbletreasury/treasury.gno:223 `func Do`). The readings chosen:

- **AT2: a refund's apply is GovDAO's own at-once act, never anyone's `Execute`.** The realm cannot see Solana:
  its gate is the note's 14 days (R37), while the gate that matters — the token's router on Solana is the
  refunding successor and the kind 6 executed there — lived in the relayer's `refundGate` and hlwatch's alarm, and
  anyone's `Execute(id)` skipped both: a refunded sender, his id in the proposal's description, could apply his
  matured refund the day a kind 6 deferred by one flag left the predecessor the router, then deliver his send
  there with the published tool, minted twice, the lock short for good. Now `hlroute.State.Execute` refuses the
  kind before `Due` ("hlroute: decision N is a refund: a refund is applied by GovDAO's own at-once decision (apply
  refund) after hlwatch's Solana check, never by Execute") — one place, so the three realms and any successor
  built on p/hlroute refuse alike; the realms' `Execute` asks the payouts flag of a sweep alone. The apply is a
  builder of its own, at once like an unpause: `NewApplyRefundRequest(cur, id)` on hlgnot, hlbubble and hlgnot2
  (`hlroute.DraftApplyRefund(id, now, env)`, the kind `apply refund`), which — at creation and again in the
  closure (`refundToApply`) — wants a decision that exists ("no decision N"), a refund ("decision N is of kind K,
  not refund: an apply refund names a queued refund decision"), pending ("refund decision N is applied|cancelled,
  not pending"), due ("refund decision N is queued until T (S s more): its 7 days have not passed") and the note 14
  days old (`mustBeEnrolled`'s own texts); the closure then runs the refund branch of `apply` — `mustBeEnrolled`
  again, the lock's bound, a send refunded since skipped — before anything is recorded, so a refusal is a panic
  that leaves no record and the proposal survives a retry until its 14-day deadline (the harness keeps the DAO's
  accepted mark, so hlgnot's test makes a fresh one after the flag clears); the realm's `propose` closure asks
  `mustNotBePaused(payouts)` of the kind first, since a refund moves coins out of the lock and waits under a
  standing payouts flag like a claim. Notes: the refund decision's queued note is "queued 7 days: applied by
  GovDAO's own apply refund decision after, never by Execute"; applied, "applied by GovDAO's apply refund decision
  M; n refunded[, k skipped (refunded already, or gone)]"; the apply decision's own "at once: refund decision N
  applied; n refunded…". Description: "apply refund decision N at once (" + the refund decision's text, its ids
  included + "), after hlwatch's check that the token's router on Solana is this realm". Reading: the member reads
  hlwatch before the vote on the apply (NOT ENROLLED ON SOLANA, DELIVERED beside an id); the realm's own gate stays
  the note — necessary, not sufficient — and the vote between a refund's maturity and its coins is the cost of
  the one check that reaches Solana. The red team's standing order (a guardian's payouts flag on the refunding
  realm while hlwatch alarms) is no longer needed.
- **AT6: a dropped route's address stays a realm of the bridge.** `hlmailbox.DroppedRoutes() []string` beside
  `Routes()` lists the tombstones' pkgpaths (`Dropped(path)` still names each one's predecessor), and `isRealm` in
  hlgnot, hlbubble and hlgnot2 walks the compiled list, `Routes()` and `DroppedRoutes()` — at the send's refusal,
  at a credit's or sweep payment's recipient by vote and at the credit's sweep — so a holder who pasted a
  re-pointed-away successor's address while its page printed it as the lock's holder to be is swept and credited
  to his owner by id, never credited as a claim paid into a realm nothing of the line can move coins out of.
  Reading: the tombstones are bounded by the re-points a bridge sees in its life, as the routes are by its
  migrations — a handful of reads; a dropped path registered afresh (`undrop`) leaves the dropped list for the
  registered one, a realm of the bridge either way.
- **AT7: a successor sends nothing before r/hlgov has dispatched the kind 6 naming it.** `hlroute.MustSend(predecessor
  string, env Env)`: a launch route returns at once (no note ever — the setup step enrols it); a successor is
  refused before its last page as before ("… nothing is sent before the last page of the migration is in (N pages
  so far)") and then, while `Env.Enrolled` answers height 0 (never noted, or cleared by r/hlgov's cancel), with
  "hlroute: this route succeeds X: nothing is sent before r/hlgov has dispatched the kind 6 enrolling this realm
  as the token's router on Solana (no enrolment note yet): a send before it would park there until a kind 6
  lands, unrefundable as the route's own"; the three realms pass `env(cur)`. Reading: the pages first, the note
  second, so no text a reader saw changes its order; the week between the dispatch and Solana's execution stays (a
  send parks a week at most, the guardians' `sends` flag on the successor the brake under a hold), and a cancel of
  the kind 6 closes the gate again — the safe direction, as for the refund. hlgnot2's drill now passes the kind 6
  after the last page, with a send refused before it and taken after.
- **AT8: the words.** hlgov.gno's two comments (`syncReach`, `mustRaise`) say "the realm's own bound (§13; kind 10
  is reserved since v35)"; `grep -n -i recover` over the bridge realms' sources (hl*, hlgnot2, p/hlroute, hyperlane,
  decisions) finds nothing — realm/'s only hits are p/governor's Go-`recover` comments and v5-royalty.md, outside
  the bridge.

What the Go side must follow (nothing in `bridge/` was changed here). Builders: `NewApplyRefundRequest(cur realm, id
int64)` on hlgnot, hlbubble and hlgnot2 — the proposal titles "hlgnot: apply refund", "hlbubble: apply refund",
"hlgnot2: apply refund"; the description "apply refund decision <N> at once (<the refund decision's text>), after
hlwatch's check that the token's router on Solana is this realm". hlwatch's `refundTextRE` matches the refund's
text inside that description, so hlwatch must key the kind on the title ("apply refund") before the regex, or an
apply proposal reads as a refund proposal; an apply proposal is a builder-made proposal to list, pre-armed or not,
with the inner refund's ids, and its own check is the refund decision's state (pending, due) beside the Solana
gate. Decisions (`decisions.json` and the page): a new kind `"apply refund"`, at once (`due == recorded`, applied
in the executing transaction; a refused closure records nothing); the refund kind's queued note becomes "queued 7
days: applied by GovDAO's own apply refund decision after, never by Execute" (the "queued 7 days" prefix kept) and
an applied refund's note "applied by GovDAO's apply refund decision <M>; <n> refunded…" instead of "applied after
the delay; …". Behaviour: `Execute(id)` on a refund decision now panics with the "… never by Execute" text, so the
relayers' loop skips `kind == "refund"` decisions altogether (today's `refundGate` only waits on them); hlwatch's
refund rule keeps alarming on a queued refund until the apply lands, and a due refund with no apply is GovDAO's to
act on (a line such as "awaits GovDAO's apply refund"); `hlsol send`'s realm-address refusal (`realmOf`) reads the
mailbox json's `droppedRoutes` beside `routes` (the field is M1i's; `DroppedRoutes()` is the realm-side reader);
a successor whose `enrolled` is 0 after its last page refuses every `TransferRemote` with AT7's text (hlwatch may
print it beside such a route: sends wait for kind 6). JSON: no new field. Signatures: `hlroute.MustSend(predecessor
string, env Env)` (a /p/ API the Go side never calls); new `hlmailbox.DroppedRoutes() []string`,
`hlroute.DraftApplyRefund(id, now int64, env Env) Draft`, `hlroute.KindApplyRefund`. Events: none new. Texts:
AT2's family above; AT7's refusal; AT6's are R39's texts ("the recipient is a realm of the bridge", "the recipient
must be an address outside the bridge's realms") now covering a dropped route's address. Plan (the plan lane's):
§5.3's refund sentence says the realm's gate is the note and GovDAO's at-once apply, never the relayer's loop, and
"a successor takes no send before its last page is in" gains "nor before r/hlgov has dispatched the kind 6 naming
it"; §5.2's realm-address rule names the dropped routes. The e2e (the Go lane's): the migration drill passes the
kind 6 before hlgnot2's first send, and applies the refund by `NewApplyRefundRequest` (`propose_and_execute`) after
its `Delay` instead of `Execute`.

- Verified on `bridge-m1j` (the final tree): the 17 gno suites green under the pinned v1.2.0 (`scripts/gnotest.sh`,
  43 s of suite time under nice, exit 0); `python3 scripts/check-interrealm.py` with the hl* realms, hlgnot2 and
  the p/ packages clean (its one line is realm/bubbletreasury/treasury.gno:223 `func Do`); gofmt and `gno fmt
  -diff` touch none of the lines added here (their diff on hlroute.gno is the tree's standing alignment of `Env`
  and the imports, left as it was). The e2e was not run here (the Go lane runs the one whole-tree e2e after the
  merge).

### M1k (2026-10-14): the governor — one key decides until it yields to GovDAO

Jae, 2026-10-14: "I want to change this so I can deploy things slowly and test it out. Until I yield to GovDAO I
want to be GovDAO alone." Five commits on `bridge-m1k` (cut from `bridge` at 852ddc35, plan v35), one per package
group — p/decisions; hlmailbox (with hlverifier's test); the three routes; hlfees and hlgov; these notes — every
rule with a test proved red first: the whole tree run once with the gate out (`decisions.DAOGoverns` answering
true, which is the tree before M1k — GovDAO from the first block) and every new test red on the behaviour (the
list under "Verified"), then green with the gate in; the type change (every builder returns the realm's own
`Request`) a compile-red on the old helpers first. Read first, as AGENTS.md asks: `docs/gno-specs/gno-interrealm-
v2.md` §2 (the two contexts: `Govern` calls the builder's closure non-crossing, in its own frame, so the closure's
`cur` is the realm's live one and its writes are the realm's, exactly as under the proxy's `cross`; nothing new
runs under anyone else's authority), §3.2 and §8.2 (storage = authority, the construction-time check and the
conversion guard: a `Request` is an /r/-declared struct with unexported fields, so no other realm can build one,
convert one or change what it decides — the only way to hold one is a builder, the only thing to do with it is
hand it to `Govern` or to GovDAO), §4.1 and §4.3 (the closure a builder makes is declared in the realm and runs
under it whoever invokes it; the security guide's §3(C) gap is a top-level /p/ function, which a `Request` cannot
carry), §5.2–§5.3 (`cur` as the capability: `Govern` proves `cur.IsCurrent()` on its own cur, `mustBeGovernor`
reads the governor from the closure's own `cur.Previous()` under it — `IsUser()` and the address — and `Yield`
the same; the `Request` keeps a proposal, a text and a closure that captures no realm value, as before), §5.4
(the governor's own MsgRun: its ephemeral realm's address is the signer's and `IsUser()` is true of it, frame.gno:83
and uverse.go:1663 of the pinned v1.2.0 — `IsUserCall() || IsUserRun()`), §9 (a refusal is a panic before any
record, so a GovDAO proposal executed before the yield leaves nothing and, on chain, survives for a retry after
it), §11.2–§11.3 and §12.1–§12.2 (`Govern` and `Yield` take `cur`; `Govern` takes a struct, so it is a MsgRun
script's call, as the builders' proposals are; `Yield` a MsgCall's); `docs/gno-specs/interrealm_v2.adr.md` (the
`cur realm` token and `cross(cur)`; `testing.SetRealm` observation-only; a realm captured by a closure must never
persist — none is); `docs/gno-specs/gno-security-guide.md` §2.1, §2.3, §3(C), §5.1 (`Governor()` and `Yielded()`
return values; `Request.Proposal()` a copy of a value GovDAO's own constructor made), §5.5 (an identity, not a
payment: `IsUser()`, which takes the governor's MsgRun, where `IsUserCall()` would not), §5.6 (the governor is
compiled, never a parameter), §5.7 (no realm value kept: the yield is a height), §5.8, §6.1, §8; `gno-ai-contract-
review.md` cases 1, 4, 6 and 10 (the page prints a compiled address and a height) and the checklist; `python3
scripts/check-interrealm.py` clean on hl*, hlgnot2 and p/ (its one line stays realm/bubbletreasury/treasury.gno:223
`func Do`). The readings chosen:

- **Who decides, where it is read.** Each of the six realms compiles `launchGovernor` — its own `const` line in
  the realm's main file (mailbox.gno, hlgnot.gno, hlbubble.gno, hlgnot2.gno, hlfees.gno, hlgov.gno), a PLACEHOLDER
  (`testutils.TestAddress("hl-governor")` = `g1dpkz6em0wejhymn0wf047h6lta047h6lvxn4gx`, as the seats are) until
  the launch notice names Jae's key — and every closure's identity check is `mustBeGovernor(cur)` in place of
  `mustBeDAO`: while `decisions.DAOGoverns(launchGovernor, hlmailbox.Yielded())` is false, `cur.Previous()` must
  be a user realm (`IsUser()`: a MsgCall's, or the governor's own MsgRun script's ephemeral realm, whose address
  is the signer's; never another realm) whose address is the governor, and GovDAO's proxy is refused ("<realm>:
  until the governor yields, g1… alone decides, by Govern from its own key: not gno.land/r/gov/dao (a GovDAO
  proposal is refused until the yield)"); once it is true — the governor yielded, or `""` was compiled — the
  proxy's exact pkgpath as before M1k ("only gno.land/r/gov/dao executes a decision, not …"), the governor refused
  like anyone. `Launch` is the governor's decision like any other (TestLaunch: a request it left 15 days lapses
  on the deadline, nothing live; GovDAO's launch refused; its own Govern makes the realms live, once). The rule
  and the page line live in p/decisions (`DAOGoverns`, `GovernorLine`, `GovernorJSON`), tested there for `""`,
  for a governor before the yield and after; the identity checks live in the realms, since an identity is read
  from a live cur alone.
- **The way in: `Request` and `Govern`.** The pinned r/gov/dao (v1.2.0) keeps a `ProposalRequest`'s executor
  unexported (`Title()`, `Description()`, `Filter()` are its only readers) and `SimpleExecutor.Execute` refuses
  every invoker but `gno.land/r/gov/dao` itself, so a realm cannot run a `dao.ProposalRequest` it is handed;
  the executor's `filter` slot could have carried the closure, but a foreign value in a slot the DAO's impl
  type-switches on (impl/v0's `FilterByTier`) is a smell today and a refusal under a later impl, so no. Every
  builder now returns the realm's own `Request` — a proposal (`Proposal() dao.ProposalRequest`, what GovDAO's
  path takes), the description (`Description()`) and the one closure — and `Govern(cur realm, r Request)` runs
  that closure non-crossing with its own cur after `cur.IsCurrent()`, a nil-closure refusal ("not a builder's
  request"), the yield check and `mustBeGovernor`, so inside the closure `cur.Previous()` is the governor, as it
  is the proxy under GovDAO's `cross`; a request past its 14 days is refused with the deadline error ("<realm>: the
  proposal is past its 14-day deadline (made at …)") and made afresh. The same closure, the same checks, the same
  queue and `Execute(id)` by anyone, the same records, notes and events; only who decides changes — the suites
  run their first decisions under the governor and the rest under GovDAO (below) and assert the same clocks on
  both sides. A `Request` is a value the caller cannot forge (§3.2, §8.2) or alter (unexported fields); a stranger
  holding one is refused by `Govern` ("… not g1…"), a code realm too ("… not gno.land/r/…"), and a zero `Request`
  (`Govern(cross(cur), Request{})`) by the nil check.
- **One yield, on hlmailbox, read by the five.** `hlmailbox.Yield(cur realm)` — the governor alone, by its own
  transaction, refused for a stranger ("only the governor g1… yields, not …"), refused when `""` was compiled
  ("no governor was compiled: gno.land/r/gov/dao governs from the first block") and refused a second time ("the
  governor yielded at height H already") — records `runtime.ChainHeight()` (never 0: a chain's first executing
  block is height 1) and emits `Yielded{governor, height}`; `hlmailbox.Yielded()` is what every realm's
  `mustBeGovernor` and `Govern` read, each time. One call rather than six because a half-yielded bridge must not
  exist: a governor still re-pointing hlgnot's migration or enrolling a router through hlgov while GovDAO already
  governs the mailbox is two governments over one lock; one switch has one state, and a successor deployed after
  the yield (hlgnot2 on mainnet, one day) finds `Govern` closed from its first block. The five compile the address
  all the same, so a member reads it in each realm's served source (§8.3), and each refuses to deploy with another
  (`init`: "<realm>: the compiled governor g1… is not the mailbox's g1…" — hlgnot2's `init(cur realm)` checks
  before it announces itself), so the six are governed by one key until it yields, or by GovDAO from the first
  block, never a mix. After the yield `Govern` refuses the governor itself ("<realm>: the governor yielded at
  height H, gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never Govern"; with `""`: "no
  governor was compiled, gno.land/r/gov/dao governs from the first block: …"); a decision the governor queued
  still applies by `Execute(id)` after its 7 days, or GovDAO cancels it.
- **Refusals are panics.** A GovDAO proposal executed before the yield aborts the transaction (nothing recorded;
  under `ExecuteOrRejectProposal` too, since only an error denies), so on chain it survives for a retry after the
  yield, within its 14 days; the harness keeps the DAO's accepted mark after an abort, so the suites prove the
  refusal and the record unmoved, and make a fresh proposal after the yield.
- **The pages.** Every realm's page shows one line from `decisions.GovernorLine`: "governor: g1… (until the yield)
  — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused" before,
  "governor: gno.land/r/gov/dao since height H (g1… yielded)" after, "governor: gno.land/r/gov/dao from the first
  block (no governor was compiled)" for `""`; every realm's json carries `"governor":"g1…","yielded":H` (0 until
  the yield). The "GovDAO's Launch" wordings of the pages and panics became "the Launch decision"; p/hlroute's
  texts ("applied by GovDAO's own apply refund decision", "a refund is applied by GovDAO's own at-once decision …
  never by Execute") are kept as they are — hlwatch and the Go tests match them — though before the yield that
  decision is the governor's.
- **The tests.** Each suite runs its first decisions under the governor and hands over to GovDAO where the chain
  would: hlmailbox — TestLaunch (the lapsed request, GovDAO refused, a stranger's and a code realm's Govern
  refused, live by Govern, "launched already" on a second), TestGovernor (GovDAO's proposals of a set verifier, a
  rotate signers, a rotate seats and an unpause refused with nothing recorded and the state unmoved; the
  governor refused nonsense by the builders like anyone), TestSetVerifier (the governor's 7-day queue, GovDAO's
  cancel of it refused, a stranger's `Execute` after the delay), TestRotateSigners (at once, queued, the stale
  rotation refused, `Execute`) as the governor's, then TestYield (the governor's rotate seats queued, cancelled,
  an unpause at once; a stranger's and a code realm's Yield refused; the governor's taken, the height, the page
  and the json; a second refused; Govern refused after; GovDAO's rotate seats queued on the same clock and
  cancelled) and the rest — TestFlags to TestNoteEnrolled, the deadline denial and the DAO's page among them — as
  proposals; hlgnot — TestSetup to TestSettings (the launch and the verifier through `hlmailbox.Govern`, the
  voids, restores, settings, sweep, credit, refund and apply refund through `Govern`), TestYield (eleven builders'
  proposals refused — cap floor, maximum, fee, fee floor, router, a range void, credit, sweep, write off, migrate,
  cancel; a restore, a refund and an apply refund need state the test does not make and share the one closure —
  the yield, Govern refused, GovDAO's raise queued and cancelled), then TestSweepVoidAndGuards to TestMigrate as
  proposals; hlbubble — TestSetup to TestBuilders under the governor, TestYield (ten builders' proposals refused),
  then TestRouteAddressSwept and TestApplyRefund as proposals; hlgov — TestKinds and TestGuardians under the
  governor (every kind dispatched by `Govern`, kind 6's enrolment note from the governor's frame), TestYield
  (twelve builders' proposals refused with no number taken, the governor's recipient swap queued and cancelled,
  the yield, GovDAO's swap and cancel-queued, nothing dispatched — the numbers the tests after count on stand),
  then TestCancelSequenceRedispatch to TestEnrollSuccessor as proposals; hlfees — the launch and the first set
  treasurer under the governor, GovDAO's set treasurer and cancel refused, the yield, Govern refused, the second
  swap and its cancel as proposals; hlgnot2 — the whole migration drill under the governor up to the refund's
  apply (hlgnot's two migrates, hlgov's kind 6, the refund and the apply refund by `Govern`), GovDAO's write-off
  refused, the yield in the middle of TestMigration (hlgnot's page says so too), the governor's write-off refused,
  then the write-off, the restore, the two credits and the migrate on as proposals; hlverifier — the launch and
  the two-group rotation as the governor, GovDAO's launch refused. The MsgRun shape of the governor's call cannot
  be modelled in the gno harness (`testing.NewCodeRealm` refuses an /e/ path), so the suites prove the MsgCall
  shape and the e2e proves the script's (the Go lane's).

What the Go side must follow (nothing in `bridge/` was changed here). **Types and signatures:** every builder of
the six realms returns the realm's own `Request` (`hlmailbox.Request`, `hlgnot.Request`, `hlbubble.Request`,
`hlgnot2.Request`, `hlfees.Request`, `hlgov.Request`) instead of `dao.ProposalRequest`, with `Proposal()
dao.ProposalRequest` and `Description() string`; a GovDAO proposal is `dao.MustCreateProposal(cross(cur),
req.Proposal())` — so `propose_and_execute`'s script template and the two hand-written scripts of e2e.sh (the
hlfees pre-armed proposal, the hlwatch section) append `.Proposal()` to a bridge builder's expression, and NOT to
gnops' `proposal.NewValidatorProposalRequest`, which is the DAO's own type. New: `Govern(cur realm, r Request)` on
each of the six realms (a MsgRun script's call, since a `Request` is a struct: `func main(cur realm) {
hlmailbox.Govern(cross(cur), hlmailbox.NewLaunchRequest(cross(cur))) }` from the governor's key — gnodev's test1 in
the e2e, `gnokey maketx run … test1 script.gno` as `run` does); `hlmailbox.Yield(cur realm)` (a MsgCall: `gnokey
maketx call -pkgpath $NS/hlmailbox -func Yield …` from the governor's key, `callpath "$NS/hlmailbox" Yield` in the
e2e's words); `hlmailbox.Governor() address` and `hlmailbox.Yielded() int64`; `decisions.DAOGoverns(governor,
yielded) bool`, `decisions.GovernorLine`, `decisions.GovernorJSON`. **Staging:** the governor is one line in each
realm's main file, `const launchGovernor = address("g1dpkz6em0wejhymn0wf047h6lta047h6lvxn4gx")` in
realm/hlmailbox/mailbox.gno, realm/hlgnot/hlgnot.gno, realm/hlbubble/hlbubble.gno, realm/hlgnot2/hlgnot2.gno,
realm/hlfees/hlfees.gno and realm/hlgov/hlgov.gno; the e2e swaps all six with one regex, as it swaps
`premineTo`: `swap "$BL/realms/<realm>/<file>" '^const launchGovernor = address("[a-z0-9]*")' "const
launchGovernor = address(\"$TEST1\")"` — all six or none, since a realm whose constant differs from the mailbox's
refuses to deploy (addpkg fails at init: "the compiled governor … is not the mailbox's …"). **The e2e's order:**
init GovDAO as today; `Govern` the launch and the first decisions (the treasurer swap, a kind, an unpause — each a
`govern NAME EXPR IMPORT` helper writing `<pkg>.Govern(cross(cur), EXPR)` where `propose_and_execute` writes the
proposal; `Execute(id)` after a delay is anyone's as before); then `Yield`; then the rest through GovDAO as today
with `.Proposal()` — the pre-armed section (a voted, unexecuted hlfees proposal that hlwatch lists) must come after
the yield, since before it GovDAO's execution is refused (a panic, the proposal left pending). **JSON:** every
realm's json gains `"governor"` (string) and `"yielded"` (int) right after `"live"` (hlmailbox: after
`"launchedAt"`; hlfees: after `"treasurer"`); no other field moves. **Pages:** the governor line above on every
realm, between the live line and what followed it (hlfees after the treasurer line). **Events:** `Yielded`
(governor, height) on hlmailbox; nothing else new. **Texts:** the three refusal families above ("until the
governor yields, g1… alone decides, by Govern from its own key: not …", "the governor yielded at height H,
gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never Govern", "only the governor g1… yields,
not …"), "not a builder's request", "the governor yielded at height H already", "no governor was compiled: …";
"GovDAO's Launch" became "the Launch decision" in the not-launched refusals and pages ("not launched: nothing is
sent, credited or paid before the Launch decision", "- **live** since T (the Launch decision executed)"). **hlwatch
and the relayers:** a builder-made proposal with the votes is executable only after the yield — hlwatch should
read `yielded` from the mailbox json and print "awaits the yield" beside a pre-armed one while it is 0 (its
execution panics, leaving it pending, until then) — and the relayers' `ExecuteOrRejectProposal` of a stale
proposal is unchanged (the deadline is an error, before and after the yield; the governor's refusal is a panic).
**Plan (the plan lane's):** §5.3's "the closure checks that the caller is gno.land/r/gov/dao" gains the governor —
the compiled address's own call through `Govern` until its one-way `Yield` on hlmailbox, GovDAO's proxy after, or
from the first block when none is compiled — and the launch vote (§6.6) is, until the yield, the governor's own
`Launch` decision; §8.3's member reads the governor in each realm's served source and `yielded` on the mailbox's
page.

- Verified on `bridge-m1k` (the final tree): the 17 gno suites green under the pinned v1.2.0 (`scripts/gnotest.sh`,
  47 s wall under nice, exit 0; the baseline at 852ddc35 was green in 49 s); `python3 scripts/check-interrealm.py`
  with the hl* realms, hlgnot2 and the p/ packages clean (its one line is realm/bubbletreasury/treasury.gno:223
  `func Do`); `gofmt -l realm/` empty (`gno fmt -diff` reports only the tree's standing import order and
  alignment, none of it on a line added here). The reds, on record from one run of the seven bridge suites with
  the gate out (`decisions.DAOGoverns` answering true — GovDAO from the first block, the tree before M1k — and
  restored after): p/decisions' TestGovernor red (`DAOGoverns(g, 0)` true, the line "until the yield" never
  printed); hlmailbox's TestLaunch red (the governor's `Govern` of the launch refused as "the governor yielded at
  height 0 … never Govern", nothing live, so TestGovernor's first builder refused "not launched"); hlverifier's
  TestTwoGroups red; hlfees' TestSetTreasurer, hlgnot's, hlbubble's and hlgnot2's TestSetup and hlgov's TestKinds
  red at their first decision — the launch through `hlmailbox.Govern`, refused the same way. Before the gate and
  the type were in, the old helpers were a compile-red (`pass` taking a `dao.ProposalRequest` handed a `Request`;
  `req.Proposal().Description()` a pointer method on a value — the test now reads the proposal into a variable
  first). The e2e was not run here (the Go lane drives it: the governor as gnodev's test1, `Govern` for the first
  decisions, `Yield`, then GovDAO as today).

### Left for later milestones

The relayers' `Claim`, `Execute(id)` and `Reemit` loops and the signers' refusal rule (M3); the e2e's migration
drill and the Go half of R35, R37 and AS2 (M1i's list above) and of AT2, AT6 and AT7 (M1j's); the e2e's governor
half (M1k's list above: the six `launchGovernor` swaps, `Govern` for the first decisions, `Yield`, `.Proposal()`
after) and hlwatch's "awaits the yield"; devnet's domain constant and the real launch values, the governor's real
key among them (M4); kind 5's mirror
against the admin program's list (`hlwatch`, M3); the salt in the admin program's and the Go side's kind-1 layout
(M2c, M3c); from round 1: a sweep void (a forged sweep entry — dust, a realm-address or non-gno recipient, an
amount above the lock — keeps the identity open by its amount until one exists), the gas of a range void over a
flood (measure on the devnet build; GovDAO splits a range if it must), restore and credit by range.

# Audit round 1, audit 1 — the plan red team with the code in hand (PLAN.md v28, branch `bridge`, 2026-10-13)

Read: PLAN.md §1–§11 and §13; AUDIT.md; M1-, M2-, M3-NOTES.md; REVIEWS.md rounds 21–22. Code read where a finding
rests on it: `bridge/programs/{gno-light-client-ism,admin-program,pausable-ism,sol-util}`, `bridge/agent/{signer,monitor,identity,executor_sol}.go`,
`realm/{hlmailbox,hlgov,p/hlroute}`. Read-only; nothing built or run.

## Findings (the AN series)

### AN1 — high — four rotations erase the settled set the recovery rests on

Plan: §6.1 "the client keeps a trusted header and the validator-set history (one entry per set change …)"; §6.4 "more
than 2/3 of the power of the last *settled* set (§6.1) … may sign any decision the admin program accepts". The note:
M2 choice 6 "Set history depth is 4 entries … a recovery needs a settled entry among them."

Code: `HISTORY_MAX = 4` (gno-light-client-ism/src/config.rs:31, whose comment says "the settled one before them stays");
`push_history` drops the oldest entry unconditionally (state.rs:95–100), called on every announcing header
(processor.rs:534); `settled_set` searches only that history (admin-program/src/recovery.rs:75–80) and `recovery_init`
/`record` answer `NotSettled` without it (recovery.rs:147, 231).

Scenario: A thief holding 5 of 6 keys (or a header-accepting bug) feeds the client an announcing header naming a set of
its own keys, then three more rotations, each signed by the set before it — four updates, under an hour. At the fourth
the launch set, settled, is evicted. The guardians' hold, set in minute one, keeps the four forged sets unsettled, so
`settled_set` is None: no validator can stage or record a cancel. GovDAO's cancel never lands (the thief races its
numbers). The thief's forged kind 5 lands on the hold's 21st day, then its token upgrade. §1's stand-off ("for as long
as that tool runs and the guardians keep holding") is a capture on day 21.

Fix: Make settlement a recorded fact: a permissionless `settle` instruction on the admin program copies the client's
newest settled entry (both holds read) into the admin state when it is newer; `recovery_init`/`record` sign against the
recorded set; `finish_setup` records the launch set; the executor calls `settle` after every quarantine. Forged
rotations then evict nothing the validators had.

### AN2 — high — a client kept an hour ahead of the chain blinds the signers and the monitors

Plan: §5.6 "a reading the node cannot give yet alarms and never stops, unless T exceeds both the own node's head and a
second source's by more than an hour of blocks"; §5.9 "then its own node's state at a height at or above T (or the
chain's head when the client is past it, which is itself the mismatch flag)".

Code: the signer returns before the identity when its head is below T inside the allowance (agent/signer.go:200–209) and
"keeps signing on its last verdict" (signer.go:274–276); the monitor flags the two Solana objects and returns before the
identity (agent/monitor.go:256–261), so gno `credits` and `payouts` are never set.

Scenario: The thief forges a header 500 blocks above gno.land's head carrying a Dispatch of ten times the lock, delivers
it seconds before the watch's flag, burns the synthetic to four Solana wallets, and submits a forged header every fifty
minutes so T stays 500–1,000 blocks ahead. Every signer: head below T inside the allowance, no verdict, signs on last
week's verdict — the four burns included. The thief's relayer calls `Process`: credited. The monitor: "client past the
chain", no identity verdict, nothing flagged on gno.land. `Claim` pays a tenth of the lock a day until a human flags
`payouts` by judgment; hlwatch's forged-id rule calls the ids genuine (real burns, both providers).

Fix: When T is above the head inside the allowance, evaluate the identity at the own node's head — §5.9's own words —
in the signer and the monitor alike; a signer signs on no verdict older than one round; the monitor's past-the-chain
branch flags Solana and continues to the identity instead of returning.

### AN3 — medium — the signers' tight rule cannot see a forged mint once it is burned

Plan: §5.6 "so the rule is `supply > lock − owed − voided + lost`"; §1 "The signers stop crediting the moment Solana's
supply exceeds the lock (§5.6), and the watch keys stop payouts."

Code: `SignerStops` is that inequality with nothing in flight (agent/identity.go:77); the signer's `Identity` carries no
in-flight burns (signer.go:225).

Scenario: AN2 with the signers reading at the head. The thief mints X at slot s1 and burns it at s2. A signer's snapshot
after s2 shows the supply back at the lock and owed unchanged: the rule holds, it signs. A signer that snapshotted
between s1 and s2 halted, and "a stop clears itself once the identity holds" — after the burn. The credit lands; only
the monitor's identity, owed up by X, flags payouts ten seconds later, and the void is GovDAO's. The plan's first
containment sentence is void; the second carries everything.

Fix: Count uncredited burns as supply in the signers' rule — `supply + burns dispatched and not yet credited > lock −
owed − voided + lost ⇒ stop` — which refuses the thief's burns themselves (X > 0 breaks it) and holds for honest burns in
flight; §1 says the watch keys, not the signers, brake a burned mint until then.

### AN4 — medium — the floor is written by a hand, and the hand is the operator's

Plan: §6.5 "the admin program writes that floor into the decision the first time a hold defers it, no later hold — two
keys re-flagging at the grace's last second to begin a new one — raises it". The note: M2 choice 11 "the first
`Execute` that finds a hold standing whose 21st day is ahead stores holdSince + 21 days … M3's executor tries at
maturity."

Code: the write is inside `execute` (admin-program/src/processor.rs:541–556); the only machine that sends it is the
operator's relayer (agent/executor_sol.go:37–40); hlwatch reads and flags, the published tool replays and submits
updates.

Scenario: moul and g1aeddl pass a replacement of the operator's two seats, which are holding; delivered on the hold's
day 1. The operator stops its executor: no Execute, no floor. Day 19: both keys clear. Twenty-four hours later the hold
ends and in that slot both re-flag: a new `holdSince`, day 20. The first Execute, under hold B, writes day 41. Repeat
every twenty days. "GovDAO overrides them by replacement" needs a member to send one Execute inside hold A, which no
tool of theirs sends and the plan never asks of them.

Fix: Keep a short hold history in the admin state (the last eight holds' start and end, written by the flag instructions
and by replacements); `Execute` computes a replacement's floor from the earliest hold standing at or after its delivery,
no attempt needed; §6.5 says the floor is a function of the hold history.

### AN5 — medium — nothing on Solana bounds what a forged delivery mints

Plan: §5.4 "A per-message maximum of 25,000 GNOT and 25,000 BUBBLE (a rail against fat fingers, GovDAO-settable) is
enforced at send on gno.land"; §1 "if nothing minted in the minute before the flags landed, holders leave … If
something did … the forged synthetic, sold, has left the lock short."

Code: the light-client ISM proves the event, the pausable ISM's `verify` checks flags and quarantine
(pausable-ism/src/processor.rs:233), and Hyperlane's token mints the message's amount; no program reads it.

Scenario: §1's thief, or a header-accepting bug, lands one forged Dispatch in the seconds before the flags. The amount is
whatever the forged event says: one message mints ten times the lock. The thief sells into §11.11's pool and burns the
rest toward gno.land (AN2, AN3). The plan's leak ending turns on "a minute" and the pool's depth; the minute is the
relayer's race and the amount is unbounded, so the loss on Solana is the pool's whole depth and on gno.land the lock
at the meter's rate.

Fix: The pausable ISM reads the token message's amount at `Verify` and refuses one above a compiled per-message maximum,
and keeps a rolling 24-hour mint bucket per token (Hyperlane's own composite ISM has one, §2) sized like §5.4's cap;
state both in §5.8 and §9's leak row.

### AN6 — medium-low — a payouts flag and a void name 256 ids; a flood is bigger

Plan: §5.3 "an id that some guardian's standing `payouts` flag names is voided at once, the rest are queued for 7 days";
§1 "one closure … void the forged claims (at once, for the ids a guardian's `payouts` flag names …), reopen payouts in
the same step". Code: `maxFlagIDs = 256` (realm/hlmailbox/flags.gno:30, 187), `MaxIDs = 256` (realm/p/hlroute/v0/hlroute.gno:23).

Scenario: Two compromised signer machines credit 3,000 forged claims of 50 GNOT to 3,000 wallets overnight (the Solana
burns are real dust; the credits are forged). The watch alarms; an outsider flags payouts naming the first 256. GovDAO's
closure voids those at once and reopens payouts in the same stroke: the other 2,744 pay at a tenth of the lock a day
until the 7-day void lands — or GovDAO keeps payouts shut a week and closes the exit it promised open. Twelve closures
of 256 are twelve votes.

Fix: Let a payouts flag and a void name a range as well as a list — every claim credited between two block heights, or
by the verifier since a height — so one flag and one closure cover a flood; raise both caps to the gas limit meanwhile.

### AN7 — low-medium — the cancelled-range store can be jammed against the validators' range cancel

Plan: §6.4 "one signed cancel may name a list or a whole range of numbers, §13". The note: M2 choice 13 "at most 64
disjoint ranges per channel pair … A full store refuses the cancel (`StoreFull`: pass the accounts)". Code:
`MAX_CANCELLED = 64` and `add_cancelled` (admin-program/src/state.rs:34, 51–70); `apply_cancel` stores every list
entry not closed by a passed pair (processor.rs:324–339).

Scenario: In the stand-off the thief records decisions 1–200, then delivers four forged mailbox cancels (kind 1 executes
at recording, no account) each listing sixteen odd numbers: 64 disjoint single-number ranges fill the store. Its flood
continues at 1,000–20,000. The validators sign a range cancel [1,000..20,000]: it overlaps no stored range, the store
is full, `recovery_record` fails with `StoreFull`, and "pass the accounts" means 19,000 pairs at sixteen a transaction.
Their daily cancel dies unless they know to sign [1..20,000], which merges the junk — written nowhere, for a tool not
built.

Fix: Write the rule into §6.4 and the validators' tool: a recovery range cancel on channel 0 spans from 1, so it absorbs
every stored range; and let `add_cancelled`, when full, merge the new range with the nearest stored one of its channel
rather than refuse.

### AN8 — low — kind 10 to 2^64−1 wedges `handle` for good

Plan: §6.4 "kind 10 only ever raises it, strictly above every number the admin program has recorded". Code:
`s.last_seq + 1` (admin-program/src/processor.rs:244) under `overflow-checks = true` (programs/Cargo.toml:35); kind 10
has no upper bound (processor.rs:754–766).

Scenario: A recovery kind 10 naming 2^64−1 lands — a bug in the validators' unbuilt tool, or a thief's during a lapse
no hold deferred. Every later mailbox decision computes `last_seq + 1`, panics, fails `Handle`; the mailbox leaves it
deliverable forever; r/hlgov's `sync sequence` can name nothing above it. GovDAO's channel is dead; the cure is a
successor by kind 9 with a fresh number compiled into its init — by recovery again.

Fix: Kind 10 is spent above `high_water + 2^32`, and `handle` uses a checked add that ignores rather than panics; §13
states the bound.

### AN9 — low — r/hlgov's seat mirror refuses a legitimate replacement after a recovery moved a seat

Plan: §6.5 "r/hlgov's builders and the admin program refuse a kind 5 that names a seat's current key". The note: M1
"Kind 5 refuses a seat's current key against a mirror of Solana's seats as this realm last set them … a decision that
failed there leaves the mirror ahead of the truth until the next kind 5 (a known gap)" (realm/hlgov/hlgov.gno:441).

Scenario: A lapse recovery's kind 5 moves seat 3 from K to K′. Months later the outsider recovers their key and GovDAO
wants K back in seat 3. The mirror still says seat 3 = K: the builder refuses at creation, though the admin program
would accept. GovDAO passes a throwaway key first — two votes and fourteen days — for a check that saves one spent
decision; the same happens after any kind 5 the admin program spent.

Fix: Drop the mirror's refusal (the admin program's own check spends a true no-op; the builder may warn), or add an
at-once `sync seats` builder taking the list `hlwatch` prints.

### AN10 — low — one slot per Hyperlane program decides who owns it, and the ids are compiled into immutable realms

Plan: §5.8 "Hyperlane's three inits (the mailbox and each token), which make the payer the owner, are sent in the first
slot after each deploy, and the audit's owner check is the residual"; §5.3 the realms compile the routers, the admin
program, the client and the pausable ISM ids. Code: deploy, then init with a one-second retry on "Program is not
deployed" (cmd/hlsol/setup.go:82–96, 315).

Scenario: A bot watching mainnet for loader deploys sends Hyperlane's `Init` for our mailbox in the slot after its
deploy, ahead of `hlsol setup`'s: it is the owner, and only an owner transfers ownership. The audit fails. A closed
program's id cannot be reused, so the mailbox is redeployed under a new id — and the realms, if already uploaded,
compile the old ids while the client compiles the realms' paths: both sides go up again under new names. Days lost,
not SOL (the deployer still holds every authority and closes what it deployed).

Fix: `hlsol setup` runs the Solana side to a passing audit before the realms are uploaded (BUILD.md's order), sends each
Hyperlane init through a private RPC path in the first slot with retries, and §5.8 names that order.

## Checked and held

- The salted cancel: r/hlgov's chain-derived salt is harmless (ignored on the mailbox channel); the record's
  allocate-and-assign defeats pre-funding; a hold anchor never recurs (the Clock is monotone); a repeat is `CancelSpent`.
- Kind 10's high-water mark equals `last_seq` (every stored range is clamped), so no number is reused against a
  cancelled range or a pending account; AN8 is only the bound.
- The sequence race: one copy per number, duplicates ignored, the taken-number report and a raise-only `sync sequence`
  hold; GovDAO's loss to a forger who races numbers is stated.
- `Launch`'s deadline: the closure errors past 14 days, every other builder panics before launch, a lapsed launch is
  re-proposed, and the idle Solana side accepts nothing from r/hlgov before it.
- The pair-with-operator drain is as §1 says: removals of two seats apply at once, clear their flags and empty the
  meters; a fifth seat changes nothing on gno.land.
- The fifth seat on Solana: only the constants and `replace`'s two-per-decision read the count; three holders are two
  decisions landing on one floor.
- The signers' stop past the allowance holds: the second source is read only there, and a lagging node never stops two
  signers; AN2 is the inside case.
- The setup step's binding order is the plan's; `finish_setup` requires the program's own authority none; a second
  run sends nothing; the authority PDA's float covers ≈200 enrolments.
- The quarantine with the real four changes: two changes 20 hours apart extend to the latest, the replay drops the launch
  entry harmlessly, and a hold begun before the first end keeps the old set settled — until AN1's eviction.
- The economics recompute at 5,080 lamports a byte and $109.6: rent 8.6, state 0.05, floats 5.2 against 14.36 funded,
  0.5 of margin; the v27 admin program is 0.003 SOL under v26's figure; the validators' tool key is unfunded, but
  "anyone may submit" a staged recovery.
- The executor and the lift: when a hold ends, the operator's executor lands every matured forged decision in its next
  tick, which §1 states as the side being the thief's; a tool could at least skip decisions its own monitor flagged
  foreign.
- The hold otherwise: three keys rotating never move `holdSince`; a replacement's clear ends it at once; an expired dip
  settles at the next change; a lone watch-key flag defers the 7-day group as a guardian's does.

## Verdict

Not yet honest for a GovDAO member on the one ending §1 promises most carefully, the leak: as built the signers do not
stop (AN2, AN3), the watch keys do not stop payouts while the thief keeps the client inside an hour of the chain
(AN2), nothing bounds the forged mint (AN5), and four forged rotations erase the settled set the recovery rests on
(AN1), so "a stand-off for as long as the tool runs" is a capture on the hold's 21st day with the lock draining
meanwhile. Buildable without inventing a rule for AN2, AN3, AN5–AN10, which the plan's words already imply; AN1 and
AN4 need a rule the plan does not state (a recorded settlement, a hold history) and go to Jae as changes. Above
medium after this round: AN1, AN2. At medium: AN3, AN4, AN5. Fold AN1–AN5 before round 2; AN6–AN10 may follow it.

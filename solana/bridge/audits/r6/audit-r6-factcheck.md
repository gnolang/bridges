# Audit round 6, audit 2 — the plan fact check with the builders' notes (PLAN.md v33, tree 95a7252, 2026-10-13)

Read whole: PLAN.md v33 (1,470 lines), AUDIT.md (the seven audits, every round's "Changes from what Jae was told"
block, the round log), RESUME.md, README.md, BUILD.md, M1-NOTES.md "M1h", M2-NOTES.md "M2h", M3-NOTES.md "M3i",
bridge/audits/r5/ (the fact check C342–C375 first, then the red team, programs, Go, assumptions, realms and
conformance files). Code read where a number or a name had to be checked: agent/record.go, agent/successor_sol.go,
agent/migrate.go, realm/hlmailbox/flags.gno, realm/p/hlroute/v0/hlroute.gno, realm/hlgnot, realm/hlbubble,
cmd/hlrecover/main.go, cmd/hlsol, sol/testdata/vectors.txt. Read-only; nothing built or run; no network (the live
figures of r5's table — the deployer's balance, the validator set, the rent rate — are not re-read; r5 read them
once at slot 456,298,458 and height 766,505 and they stand as the plan's). Not spent on: the heap frame (§6.1), the
one launch day-cap constant for both tokens (§5.4), the seven-aunt proof (§6.2), the mailbox's own body bound (§6.5),
the successor realm's pull loop (§5.3), the public checkpoint store (§5.6), the devnet domain constant (M4), the launch
notice and the member's reads, U1–U10, r/hlgov's kind-6 builder (the realms audit's). The validator count is never a
fact of the design below (six today). IDs from C376.

## Regression: C342–C375 at their lines

- C342 folded (§5.4:596 `expectedLock`, the operator's claim, sticky, "from 14 days after `Launch`"; §11.9:1278).
- C343 folded (§5.9:789 "audits nothing before it, the client's own counter taken as true"; §11.9:1278 "start your
  first `hlwatch` from the deploy, never from the client's checkpoint").
- C344 folded (§6.1:856 "a ring of the capacity it had, filling its free slots first"). C345 folded (§6.3:883
  "169–171k … 26–32k"). C346 folded (§5.3:495 "any route in its chain of predecessors (at most sixteen deep; a
  route migrates once)"). C347 folded (§5.3:571 "stays pending until GovDAO cancels it"). C348 folded (§5.3:494
  "the relayer reads before staging"). C349 folded (§6.5:1030 "four hops deep … a fifth successor would return the
  tools to a config edit"). C350 folded (§5.9:783 "⌈N/1,000⌉ polls"). C351 folded (§6.5:997 "`close cancelled`,
  which the executor sends"). C352 folded (M3-NOTES.md has M3h at line 1306 and M3i at 1385). C353 folded
  (§5.8:729 "`hlwatch` itself reads in the monitors' order").
- C354 folded (§6.5:993 "a growing upgrade's one extension included"). C355 folded (§5.8:740, §10:1215 "after audit
  round 5"; §2:383 "after the fifth audit round"). C356 folded (§6.3:875 "after the fifth audit round"). C357 folded
  (§1:291 names the two; §5.9:783 and :805 say "second poll" and "two minutes" for the other two — four rules, two
  framings, consistent). C358 folded (§5.3:575 "a flood of up to 1,000 credits, a bigger one several closures by
  heights"). C359 folded (§2 and §6.1 agree). C360 folded (§5.4:596). C362 folded (§6.5:997). C363 folded
  (§8.4:1148 "with no successor of its own") — but §6.5:991 now reads three more fields before "immutable" (pending
  decisions, the recovery counter, the layout) and §8.4 still names three: the same shape one round on, listed in
  (b) below without an ID.
- C361 half: §5.9:789 states the checkpoint's blind spot; §6.2:871–873 still says the watch "sees a never-existed
  header proven at a past height as surely as a forged update" with no "from its base" — true of a watch started
  from the deploy, which §11.9 now orders for the outsiders. Listed in (b).
- C364–C367 (the r5 table's stale rows): RESUME.md:93 "after audit round 5" ✓ and :36 "≈15 minutes" (730–1,071 s:
  12–18 min, fair); BUILD.md:76 "three real set changes" ✗ and :182 "the 72-hour run" ✗ (C380); README.md:12
  "plan v26's program changes landing (M2b …)", :41 "the identity with its in-flight tolerance", :56 "the plan's
  light-client delivery is unmeasured" ✗ (C380); README's "68 encodings" ✓ (vectors.txt: 68 entries at 95a7252).
- C368 folded (`expectedLock`, §5.4). C369 folded (four hops §6.5; sixteen §5.3). C370 folded (§5.7:667 "400,000-CU
  budget"). C371 folded (§5.9:783). C372 folded (glossary :27 "the cut" is also the monitors' ordered reading).
  C373 folded (glossary :27 importing / imported / the pair; used in §5.3:495 and §5.9 consistently).
- C374 not folded: the plan names no cadence but SETTLE DUE's hourly (§6.4:891) and the retention retry's (§5.9:789);
  M3i posts `refund:delivered`, `decision:other-program` and `mint:cap-ratio:<token>` daily. Low; left in this list.
- C375 not folded: §5.3:494 "waits for a kind 12" still precedes §5.4:596's naming of kind 12 (the glossary :27
  sends "kind" to §13, so a reader can find it). Low; left in this list.

AUDIT.md's round-5 block against the plan: every bullet is in the text but two — the cap-ratio quiet's third expiry
(C376) and the alarm past the hop bounds (C377).

## (a) Findings

**C376 (low) — the cap-ratio quiet's third expiry is told in AUDIT.md and built, not in the plan.** AUDIT.md,
round 5: "The cap-ratio alarm's quiet expires 14 days after `Launch` and whenever the lock reaches five caps, so a
pool that never fills, or a lie in the config, cannot keep it quiet." M3i (AR5 + G43): "`capQuietEnds`: hlmailbox's
`launchedAt` plus the realms' 14-day deadline … and reaches at five day caps too; the standing persists as
`CapReached`." Plan §5.4:596: "judged only once the lock has first reached the expectation the cap was set against
(… `expectedLock` …), sticky after, and from 14 days after `Launch` whatever the lock has done, so a lock that later
shrinks below five caps alarms and a pool that never fills cannot keep the alarm quiet" — two triggers, the
"shrinks below five caps" a consequence of the ratio rule, not the third trigger. Scenario: the operator's config
says `expectedLock` 10× the real seed; the lock reaches five caps on day 3 and halves on day 5. By the plan's words
the alarm is quiet until day 14; the code alarms on day 5. A member reading §5.4 counts 14 days of blindness and
the "lie in the config" cure he was told is nowhere. Smallest fix: §5.4 "… sticky after, from 14 days after
`Launch` whatever the lock has done, and from the first poll the lock stands at five caps or more — whichever comes
first — so a lock that later shrinks below five caps alarms, and neither a pool that never fills nor a lie in the
config keeps the alarm quiet".

**C377 (low) — running past the tools' hop bounds alarms in the code and in AUDIT.md; the plan says "a config
edit".** AUDIT.md, round 5: "running past the tools' hop bounds alarms". M3i (G38 + F24 + K35): "the monitor
alarms `decision:line-unfollowed` and `route:line:<token>` after two minutes, each naming the config edit". Plan
§6.5:1030: "follow it four hops deep … a fifth successor would return the tools to a config edit, which the launch
notice names as the operator's chore"; §5.3:495: the line "followed from the configured path to its end as the
realm follows it (sixteen deep)" — neither says the tools alarm when the line runs past the bound (the plan has no
"line-unfollowed", no "hop" but §6.5's). Scenario: a fifth admin succession under a leak's cure; the decisions to
the fifth program wait with the decision rules quiet — by the plan's words silently, "a chore" the notice names;
as built, an alarm within two minutes naming the edit. The plan under-states its own watch. Smallest fix: §6.5:1030
"… a fifth successor would return the tools to a config edit, alarmed within two minutes (`decision:line-unfollowed`)
and named in the launch notice as the operator's chore"; §5.3:495 the same for a seventeenth realm (`route:line`).

**C378 (low) — a validator head count as a fact of the design.** RESUME.md:170–172 (the standing rule): "any new
text use[s] rules and numerals (> 2/3, ≥ 2/3, 1/10), never today's head counts as if fixed; the plan's prose follows
the same preference since v19". Plan §5.8:718: "`hlrecover` in M3d, its reproducible build hash pinned in the launch
notice beside `hlwatch`'s, since five validators sign with it" — present tense, no "today"; §6.5:1013: "the week's
margin is there so that five of six validators, at five organisations with their keys in hardware signers, have
time to sign" — the margin's rationale stated in today's count (the rule is more than 2/3 of the settled set's
power, §6.4), and only two of the six are known to hold hardware signers (§11.10: "the two YubiHSMs, 120 of 360; the
four cloud validators' backends are theirs to answer"), so "at five organisations with their keys in hardware
signers" is also not a fact. (§1's "today 5 of 6", §11.15's "five of six validators must sign the cancel — AL4" and
the status line's "five validators' signatures" are history or say "today".) Scenario: Prop #10 passes (five at 60,
one at 1: forging and the override take 4 of 5, §1); §5.8 and §6.5 still say five. Smallest fix: §5.8:718 "since the
validators sign with it (more than 2/3 of the settled power — 5 of 6 today)"; §6.5:1013 "so that the validators
whose signatures the rule needs — 5 of 6 today, at five organisations, two of them known to hold their keys in
hardware signers (§11.10) — have time to sign".

**C379 (low) — RESUME.md says the validators' signing tool is unbuilt; the plan and the code say `hlrecover sign`
carries two backends.** RESUME.md:37–38: "Left: the roster comparator (Proposal 2), the public checkpoint store, the
validators' recovery signing tool (§11.10; builders and vectors exist), M4". Plan §11.10:1307: "`hlrecover` carries
the two backends"; cmd/hlrecover/main.go:9 "hlrecover sign -digest HEX -key FILE | -yubihsm http://127.0.0.1:12345
-auth-id 1 -key-id N", :32–35 (a key file — tmkms softsign, priv_validator_key.json, hlkey, raw hex/base64 — or a
YubiHSM 2 through its connector), :65 `version` among its commands. Scenario: a member or a session resumes from
RESUME.md, reads that the launch gate of §11.10 ("without the assent and the tool the recovery is dead") has no
tool, and either refuses the vote on a false ground or builds a second tool. RESUME.md:32–33 is also a round
behind on the suites ("18 program tests and 24 host tests" — 38 program tests and 12 core unit tests after M2h,
M2-NOTES.md:412–414). Smallest fix: RESUME.md:37–38 "Left: the roster comparator (Proposal 2), the public checkpoint
store, M4 — … (the validators' signing tool is `hlrecover sign`, a key file or a YubiHSM, §11.10; a Ledger cannot
sign; it has signed with no validator's real key yet, M4's drill)"; :33 "38 program tests".

**C380 (low) — README.md and BUILD.md, the code map and the work plan, each a round or more behind the plan.**
README.md:43 (the `cmd/hlsol`, `cmd/hlkey` row) and the whole table: no `cmd/hlrecover` row, though §5.8:718 counts
"Four tools" and the launch notice pins its hash (bridge/cmd/ holds hlbridge, hlkey, hlrecover, hlsol, hlwatch);
README.md:12 "with plan v26's program changes landing (M2b; `M3-NOTES.md` lists what the Go side follows ahead of
them)" (the programs are at v33's shape, M2h); :41 "the identity with its in-flight tolerance" (the exact rule since
v31, §5.9:783 "never by a tolerance of what is in flight"); :56 "the plan's light-client delivery is unmeasured"
(§6.3:880–883 measures it: 29k by the ring, 34–45k by header, 45k, 94–126k); :44 "a relayer and a monitor run"
(the e2e runs two monitors — M3i:1487 "monitor 2's `solana:providers`"). BUILD.md:76 "fixtures for 17 heights …
(the three real set changes among them)" (four: §5.9:755 lists 487,883, 495,707, 720,333, 741,862; §8.1:1122
"four set changes"); BUILD.md:182 "the 72-hour run with every case in §5.9's list" (§5.9:756 "four runs of up to
72 h each"; §10:1211 "the four 72-hour runs"). Scenario: §1 sends a member to "the published tools"; README.md —
"the code map" (:7) — shows three of the four, and says the identity tolerates what is in flight. Smallest fix:
a `cmd/hlrecover` row ("the validators' and any member's tool, §6.4, §6.5: `version`, `status`, `body`, `init`,
`sign` (a key file or a YubiHSM), `submit`, `record`, `close`, `settle`, `execute`"); README.md:12 "at the plan's
shape (M2–M2h)"; :41 "the identity by the signers' exact rule"; :56 "under the light client 29–45k CU for the
proof, 45k for `Verify`, 94–126k for the delivery (§6.3)"; :44 "two monitors"; BUILD.md:76 "four"; :182 "the four
72-hour runs".

**C381 (medium-low) — the launch per-message maximum is 25,000 in four places and "the smaller of 25,000 and the
day cap" in one; the code compiles the latter.** §1:209 "bounded by the pausable ISM's own limits: 25,000 a message
and a day cap of a tenth of the expected lock, at most 100,000"; §5.4:594 "A per-message maximum of 25,000 GNOT and
25,000 BUBBLE … is enforced at send on gno.land"; §5.4:596 "The pausable ISM's own per-message maximum (§13's kind
12) is the same 25,000 at launch" — and, the same paragraph, "the launch per-message maximum is the smaller of
25,000 and that day cap … `hlsol setup` refuses an expected lock whose tenth is below the realms' compiled maximum
unless the realms' is set to match"; §13:1422 "compiled at launch as 25,000 GNOT and 25,000 BUBBLE a message";
§11.11:1310 asks Jae for the lock "from which the pausable ISM's launch day cap is compiled" and says nothing of
the maximum. Code: cmd/hlsol/setup.go:85–98 `launchMaxPerMessage(dayCap)` = min(25,000, the day cap),
`launchMaximumCheck` refuses the mismatch unless `-launch-maximum` names what the realms compile; the realms'
`launchMaximum = 25_000` (hlgnot.gno:60, hlbubble.gno:52); M3i: "`-launch-maximum` is one figure for both tokens, as
`DEFAULT_MAX_PER_MESSAGE` is". Scenario: §11.11 is answered with 150,000 GNOT (AR4's figure): the cap is 15,000, the
ISM's maximum 15,000, and `hlsol setup` refuses until the operator lowers `launchMaximum` in both route realms and
passes `-launch-maximum 15000` — a compiled launch value §5.3:532–534 lists and §8.3:1137 has the member read
"against the proposal", while §1, §5.4's opening, §13 and the notice's reader hold 25,000; and because the figure is
one for both tokens, BUBBLE's maximum follows GNOT's lock. Smallest fix: §1:209, §5.4:594–596, §13:1422 and
§11.11:1310 each say "the per-message maximum at launch is the smaller of 25,000 and the day cap — one figure for
both tokens, compiled into the route realms (`launchMaximum`) and the pausable ISM alike, 25,000 when the expected
lock is 250,000 or more"; §11.11 adds that his figure sets the maximum too when it is smaller.

**C382 (low) — `lost` has a second source since M1h; the glossary, §1 and §5.9 define it by voids alone.** Glossary
:20 "'lost' is the paid part of claims GovDAO declared forged"; §5.9:781 "lost (the part of claims voided as forged
that had already been paid)"; §1:214 "plus what is lost". §5.3:494 (v33): "a `write off` builder, 7 days, books a
send delivered after its refund as lost, so the identity closes"; M1h: "`Lost += amount`, so lock = sentTotal −
paidTotal = supply + owed + voided − lost closes … the json's `writtenOff` and `writtenOffTotal`"; M3i: hlwatch
prints `writtenOff` beside `refunded`. Scenario: a member re-derives the identity from the realm's json after a
write-off: `lost` exceeds the forged voids' paid parts by the written-off amount and, by the plan's definition,
reads as a bookkeeping error; the field that explains it is named nowhere in the plan. Smallest fix: glossary :20
and §5.9:781 "lost is the paid part of claims voided as forged, plus any refunded send Solana delivered after its
refund and GovDAO wrote off (§5.3; the json's `writtenOff`)".

**C383 (low) — §1's slow list lacks two 7-day builders of §5.3: the refund and the write-off.** §1:84–88 lists the
slow changes ("a raise; a swap …; a credit by hand; a migration; a sweep payment; r/hlgov's change of the decision
recipient; a signer-rule change …; a void …; a restore …; a signer rotation …") and :89–90 the instant ones;
§5.3:494 "refund a send by message id (7 days; the amount back from the lock to the sender …)" and "a `write off`
builder, 7 days". Scenario: §1 is the trust statement (RESUME.md:19); a member counting what a pair can do to the
lock at once finds the refund — the one builder that pays coins out of the lock to a sender — in neither list.
Smallest fix: §1:86 "… a sweep payment; a refund of an imported send, and a write-off of one Solana delivered after
its refund (§5.3); …".

**C384 (medium-low) — the acceptance list and the drill name no migration, refund, write-off or re-point, though
round 5 rewrote those rules and nothing but unit tests has run them.** §5.9:749–777 lists the four 72-hour runs'
cases (holds, replacements, recoveries, voids, restores, a credit refused, a re-emit, parks, a growth, a cancelled
upgrade's buffer, the leak ending); §6.4:963 "The drill: on devnet … including a transfer to a successor and a
fork" — the admin program's successor, kind 9, not a route's; §6.6:1047 "Its gates are §5.9's acceptance list".
No realm migration, successor-taking, kind-6 enrolment of a successor, refund, write-off or re-point is on the list.
M1h:906 "the e2e drives no kind 6, refund, write-off or migration"; M3i:1488–1491 "Not exercised by the e2e (unit
tests only): a migration line of two hops, a refund proposal, a double mint and its write-off, a successor with
decisions pending or another layout, a wrapped ring, a checkpoint, a gone decision under a hold, a successor's send,
a stroke without the line, a re-pointed line"; §5.3:495 "No successor realm exists yet". Scenario: the vote passes
on §5.9 fulfilled to the letter; the first migration — §1's cure for a wedged route ("undelivered sends are refunded
by migrate", §1:276) — is the first time a successor's `Pull`, the enrolment note, the relayer's line-taking and a
refund run on any chain, and every rule of AR1, AR2, AR3, R31–R34 and K34 was found by reading, none by running.
Smallest fix: §5.9's runs add "a migration of one route to a successor realm deployed for the drill: announced,
migrated to; a burn addressed to the predecessor delivered after the last page and one to the successor waiting
before page 0; kind 6 enrolling it; a refund refused inside the enrolment's week and landing after it; a write-off
of a send Solana delivered after its refund; a re-point of a migrate whose successor pulled nothing"; §6.4:963 the
same clause; §8.6 "and a migration" beside the void and restore the member saw.

## (b) Internal contradictions (both places quoted above where they carry an ID)

- §5.4:594/596 "25,000 … the same 25,000 at launch" vs §5.4:596 "the smaller of 25,000 and that day cap"; §1:209 and
  §13:1422 side with the first (C381).
- Glossary :20 and §5.9:781 (`lost` by voids) vs §5.3:494 (the write-off books into lost) (C382).
- §1:84–90 (the two lists) vs §5.3:494 (the refund and the write-off, 7 days) (C383).
- §6.5:991 (six fields hlwatch reads before "immutable": predecessor, not inherited, no successor, no pending
  decisions, recovery counter none, the layout) vs §8.4:1148 (three) — C363's shape a round on.
- §6.2:871–873 "the watch … sees a never-existed header proven at a past height as surely as a forged update" vs
  §5.9:789 "audits nothing before it [the checkpoint]" — C361's residue; true of a watch from the deploy, which
  §11.9:1278 orders.
- §2:382 "ours as built on 2026-10-13 (M2g, a milestone of BUILD.md …)" beside sizes that are M2h's (400,280 B,
  "after the fifth audit round (M2h: …)" in the same sentence) — a label.
- §6.5:1030 "a config edit, which the launch notice names as the operator's chore" vs M3i's alarm (C377); §5.4:596's
  two expiries vs the code's three (C376).

## (c) Numbers recomputed from the plan's own inputs and the notes (no network this round)

| Item | Plan | Recomputed | |
|---|---|---|---|
| Program rent | §2 1.39 / 0.84 / 2.03; §5.8 ≈9.0 | (273,768 + 45 + 128) × 5,080 = 1.3916; (164,432 + 173) × 5,080 = 0.8362; (400,280 + 173) × 5,080 = 2.0343; 4.262 (M2h "4.26"); + 1.07288 + 2 × 1.81822 = 8.971 | ✓ |
| M2h's growth | §2 399,632 → 400,280 | 648 × 5,080 = 0.0033 SOL, "as before to the second decimal" | ✓ |
| Need, shortfall, margin | ≈10.6; ≈3.2 (§5.8:744, §11.4:1258); ≈1.1 | 8.971 + 0.15 + 1.5 = 10.62; − 7.357 = 3.26; 8 − 3.26 − 3.7 = 1.04 | ✓ within rounding; 3.26 reads ≈3.3 and 1.04 ≈1.0 (r5 said so; not a finding) |
| Transfer's compute | §6.3 169–171k; spend 26–32k | M2h 169,100–170,600; M2g's spend 26,300–31,700 | ✓ |
| Reset's set | §6.1 252 / 250 | 1 + 9 + 4 + 32 + 8 + 1 = 55; (10,135 − 55) ÷ 40 = 252; with 64: 250 | ✓ |
| Body bounds | §6.5 538 body, 512 refused; M2h 529 of parameters | 538 − 9 = 529; 538 − 512 = 26 of slack | ✓ |
| Staging chunks | §13:1368 23 validators, 40 precommits a transaction | sol/client.go:45 `NextSetChunk = 23`, :51 `PrecommitsPerTx = 40` (:49 sixteen beside the init, unsaid, a detail) | ✓ |
| Chains and hops | §5.3 sixteen; §6.5 four | flags.gno:72 and agent/migrate.go:29 `maxChain = 16`; successor_sol.go:25 `successorHops = 4` | ✓ |
| Range cap, pages | §5.3 1,000; 500 | hlroute.gno:35 `MaxRangeClaims = 1000`; 500 unchanged by M1h (its signature list names no page constant) | ✓ |
| Seed slots | not in the plan; BUILD.md:427 "the seed 50,000 slots" | record.go:235 `recordSeedSlots = 50_000`; 50,000 × 0.218 s = 3.0 h | ✓ |
| Cap-ratio quiet | §5.4 14 days; five caps (C376) | M3i `Clocks.Deadline` (the realms' 14-day deadline, ≈353,000 blocks at the chain's factor), `lock ≥ 5 × dayCap` | ✓ / unstated |
| Launch maximum | 25,000 (×4) vs min(25,000, cap) | setup.go:88 min(25,000, day cap); realms 25,000 | C381 |
| State accounts | §2 21,168 B ≈0.11 | M2-NOTES:196 the suite's deploy account (a set of 4) 21,216; 48 B = 0.0002 SOL | ✓ (a label) |
| Blocks | 7 days = 176,327; 14 = 352,653 | 604,800 ÷ 3.43 = 176,326.5; 352,653.1 | ✓ |
| Vectors | README 68 | sol/testdata/vectors.txt: 68 entries at 95a7252 (M2h: no format change) | ✓ |
| Live figures | 6 × 60; 7.36 SOL; 5,080/B | not re-read (no network); r5 read them once at 766,523 and slot 456,300,181 | stand |

## (d) Terms before definition, parameters without a value

- `launchMaximum` (the realms' compiled maximum) and `-launch-maximum` (hlsol's flag): the figure §8.3's member
  reads and the operator must set when the expected lock is under 250,000 — unnamed (C381).
- `writtenOff` / `writtenOffTotal` (a route's json), `enrolled` / `enrolledAt` (the mailbox's): the fields hlwatch
  reads for the refund rule — unnamed; §5.3:494 says "the height" (C382).
- "the line" (§5.3:495 "a route's whole line of migrations", "the line's last realm"): used beside "the pair", not in
  the glossary — "the line is a route and every successor after it, followed from the configured path (§5.3)".
- The alarms' cadences and the new keys (`refund:proposal`, `refund:delivered`, `router:unknown`, `route:line`,
  `successor:proposal`, `record:checkpoint`, `decision:line-unfollowed`): C374's residue, low.

## (e) What a careful GovDAO member still refuses

1. The vote's per-message maximum is one number in §1, §13 and §5.4's opening and another in §5.4's rule and the
   code; until §11.11's lock is named nobody can say which (C381).
2. No migration, successor-taking, refund, write-off or re-point has run on any chain, and §5.9's gate does not ask
   for one — the rules round 5 rewrote are read, not run (C384).
3. `lost` has a source the identity's definition does not name, and the refund is in neither of §1's lists (C382,
   C383).
4. §5.8 says five validators sign with `hlrecover` and §6.5 that five of six hold hardware signers (C378).
5. RESUME.md says the validators' signing tool is unbuilt; README.md's code map shows three of the four tools and an
   identity that tolerates what is in flight (C379, C380).
6. `hlrecover` has signed with no validator's real key (M4; r4's and r5's item stands).
7. Two of round 5's told changes are not in the plan: the quiet's third expiry and the alarm past the hop bounds
   (C376, C377).

## (f) Verdict

The plan is at rest with its code on every rule round 5 changed: all thirty-four regressions but two lows (C374,
C375) are folded at named lines, every round-5 bullet of AUDIT.md but two is in the text, the rent, compute, chunk,
hop, cap and body numbers recompute from the notes and the constants, and the glossary's new terms are used as
defined. Not at rest: the launch per-message maximum is stated two ways (C381, medium-low), the acceptance list
names no migration drill while round 5 rewrote the migration's rules on paper alone (C384, medium-low), `lost` and
§1's lists lag the write-off and the refund (C382, C383), a head count stands as a design fact (C378), two told
changes are unsaid (C376, C377), and the three companion documents are a round or more behind (C379, C380).
Nothing above medium-low. Fold the nine, then one documents pass over RESUME.md, README.md and BUILD.md; no further
plan round is needed on these grounds.

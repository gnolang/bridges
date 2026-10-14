# GNOT and BUBBLE between gno.land and Solana — a light-client bridge under GovDAO, proofs both ways after Alpenglow

Status: plan v36, 2026-10-14, after twenty-two review rounds (REVIEWS.md), six rounds of the audit loop and its final check (AUDIT.md), Jae's decision of 2026-10-13 to remove the validators' recovery path ("validators should do nothing than validate": every change on Solana is a light-client proof of a decision and nothing else, a client that can no longer verify gno.land ends the deployment, and the validators are asked nothing, §6.4, §11.10) and his decision of 2026-10-14 that he governs alone until he yields to GovDAO ("I want to change this so I can deploy things slowly and test it out. Until I yield to GovDAO I want to be GovDAO alone": one governor address compiled into the realms makes every decision by direct call until a one-way yield recorded on hlmailbox, GovDAO's proposals refused before it and the only way after, the Solana side untouched, §5.3, §6.6, §11.16), after his decision of 2026-10-12
that the guardians have no veto, only a pause and a hold; round 19 found that v22's hold was worth 7 days against anyone able to deliver a decision, and v23 made a replacement of guardians under a hold wait — for the validators' recovery window then, for GovDAO's own cancel since v35 (§6.5, §11.15); round 20 fixed v23's clock: the hold's start belongs to the object, not to flags (§6.1); round 21 caught Prop #13 passing (the set is 6 × 60), the day the grace added to a replacement's landing (§6.1, §6.5) and the one-day margin a replacement under a hold needs (it lands on the hold's 21st day, §6.5); the three Solana programs were built and measured the same day (§5.8, §6.3). v16 removes the temporary signer phase ("Phase 0")
that v1–v15 launched with: the light client is in the trust path from the first block, the admin program holds every
Solana authority from deploy, and there is no interim multisig, no hand-over and no Hyperlane multisig check on the
gno → Solana path; §12's earlier rounds reviewed that older shape, and their findings about it are kept as history.
Nothing is deployed. The code on branch `bridge` passes its local end-to-end run but is not the launch shape: every
change in §5–§6 lands before launch. Round 18 reviewed v20 and found nothing above medium; v21 was its editing pass; round 19 reviewed v22, the no-veto fold, and v23 folded it; round 20 reviewed v23 and v24 folded it; round 21 reviewed v24 and v25; v25 folded its fact check, v26 its red team and the built programs' measurements; round 22 reviewed v26 — its red team found nothing above medium — and v27 folded both reports; a final fact check read v27 for editing residues and v28 folds them. The review loop is at rest; the audit loop of AUDIT.md (seven audits a round over the plan, the code and every assumption) found in its first round that the code's readings had quietly changed four rules, and v29 stated them: a settled set recorded by the admin program (gone with v35, §6.4), the replacement floor computed from a hold history, the pausable ISM bounding what one message and one day can mint, and the signers counting burns in flight. Words: "signers" are the three machines that vouch for
Solana → gno until Proposal 2 and for Solana's validator roster per epoch after it, until SIMD-0511 (§7.3); "validators" are gno.land's chain validators; "guardians"
are four people in numbered "seats" who hold pause keys and nothing else — one flag pauses, two flags hold, and a
hold lasts until GovDAO replaces the holders, on the hold's 21st day at the earliest (§6.5); the two "outsider" seats are the ones outside the operator; "watch keys" are the operator's two monitors' keys,
which can only pause; "the operator" is NewTendermint, LLC, which runs the signers, the relayers, the updater, the
prover and the monitors; a "route" is a realm on gno.land paired with a token program on Solana (one for GNOT, one for
BUBBLE); "T1" is GovDAO's top membership tier. Jargon, once: an ISM is Hyperlane's word for the program that decides
whether an incoming message is genuine; a PDA is an account a Solana program owns; a CPI is one Solana program calling
another; an ATA is a wallet's account for one token; CU are Solana's gas units; an ELF is a compiled Solana program;
"crossing" is a gno call that switches realms; "the lock" is what the gno.land route realms hold against the synthetic on Solana; a
claim is an IOU the bridge owes a recipient ("owed" is credited and unpaid; "voided" is the unpaid remainder of
claims GovDAO withheld, restorable; "lost" is the paid part of claims GovDAO declared forged, plus any refunded send Solana delivered after its refund and GovDAO wrote off (§5.3); "in flight" is sent
and not yet proven); a SIMD is a Solana improvement proposal; a bucket is an allowance that refills at a steady rate up
to a ceiling; a decision is the governor's direct call before the yield and a GovDAO proposal, executed, after it — on gno.land the numbered change it records (§5.3), on Solana the
message it sends to the admin program (which also records as a decision any message from r/hlgov with a parseable
prefix, an unknown kind recorded and spent, §6.5, and the watch calls one r/hlgov never executed foreign, §5.9) —
and nothing else is one: no validator signs a decision (§6.4); a flag is one guardian's or
watch key's "paused" mark; a seat is one of the four numbered guardian positions (3 and 4 the outsiders' — a numbering for the audit and the launch notice, which no on-chain rule uses) and a
watch slot one of the two watch-key positions (§5.8, §13); a hold is two guardians' flags standing together on one object, and its start the moment the object's standing guardian flags reached two — its `holdSince` (§6.1, where "a dip" below two shorter than 24 hours keeps it); "the hold's 14th day" and "21st day" mean 14 × 24 and 21 × 24 hours after that start; "the cut" is also the monitors' ordered reading of §5.9 (the Solana snapshot, then the own node at or after it), a poll that fails past it being no verdict; a route is "importing" from the page that brought its predecessor's counters until the page with nothing after it, "imported" once that page is in, and "the pair" is a predecessor and its successor read together; "the lift" is the holders' own clearing of their flags, which ends a hold 24 hours later, and "the grace" that day (§6.1); "an object" is one of the two Solana accounts flags live on, the pausable ISM's and the admin program's; a "kind" is one of the twelve decision types of §13 (numbered there, three of them reserved) and a "channel" is
the way a decision arrived — there is one, by mailbox from r/hlgov, and §13's channel byte names it;
"the ring" is the client's list of the last 256 accepted header hashes, a cost shortcut for proofs, and "the ring epoch" the client's counter of its clearings, which a staged ring proof carries so the ring's churn never voids it (§6.2); a signer's "round" is one pass of its loop, ten seconds on mainnet (§5.6); "builders" are the realm functions a
decision is made with (§5.3); "the pausable ISM" is our own Solana program in front of the light-client ISM, where the
deliveries' flags live (§5.8; the admin program and hlmailbox keep their own); `sends`, `credits` and `payouts` are
the three functions of a gno route a flag can pause (§5.1); `Type` and `VerifyAccountMetas` are two questions
Hyperlane's relayer asks an ISM by simulation (its kind, and which accounts to pass); the mailbox on chain asks only
`Verify`; ValidatorsHash and
NextValidatorsHash are the header fields that commit to a block's validator set and the next block's; "the admin program" is the Solana program that carries out GovDAO's decisions (§6.5); "the
updater" is whoever feeds gno.land's headers to the light client (§6.1); `Launch` is hlmailbox's one-shot builder: the
governor's call that makes the realms live — GovDAO's only after a yield made before it (§5.3); "the governor" is the one address each realm compiles in at deploy, Jae's key of choice (§11.16), whose direct call of a builder through the realm's `Govern` entry is a decision until the yield, after which GovDAO's proxy is the only governor; "the yield" is the governor's one-way call on hlmailbox, recorded with its height and read by every realm, from which GovDAO's proposals are the decisions and the governor's calls are refused (§5.3); "the testing phase" is the time between `Launch` and the yield — a small lock by rule, no real holder invited, the guardian seats the operator's own if the outsiders are not yet seated (§1); "the yield vote" is GovDAO's proposal 1, its acceptance of the bridge as it stands, on which Jae yields (§6.6); "the launch notice" is the operator's post at the yield (§5.3), and "the deployment's checklist" the same gates published with the mainnet deployment, before `Launch` (§6.6); "the ATA payer" is the account that pays a recipient's first token
account; "the members' channel" is the alert group GovDAO's members name in the launch notice; "the 7-day group" is
every decision kind that waits 7 days (§6.5); "the quarantine" is the 7-day pause of gno → Solana after any
validator change (§6.1); "the watch" is the operator's
monitors and their invariants (§5.9), `hlwatch` the guardians' own tool, `hlsol` the operator's setup and audit tool and `hlrecover` the members' tool, which reads a decision and executes a matured one for any member (§5.8, §6.5); "the executor" is whichever funded key sends a matured decision's `Execute` — the operator's relayers by default, any member by `hlrecover execute`; "synthetic" is the
token minted on Solana against the lock; "a foreign decision" is one the admin program recorded that
r/hlgov never executed; "the fork" is
the point where a forged chain leaves the real one, cut at the Solana slot S of §6.4; "the loader" is Solana's upgradeable
program loader, "a buffer" its staging account for a new program, `SetAuthorityChecked` its instruction for moving
an upgrade authority and `SetBufferAuthority` for handing a buffer to a new authority; T2 and T3 are GovDAO's lower tiers (empty today);
FEC sets and shreds are the pieces Solana cuts a block into for transmission; "the deployer" is the operator's
key that deploys both sides and funds the floats, "the setup step" the transactions that hand every Solana
authority to the admin program before `Launch` (§5.8), "the loader's spill" the account that takes a buffer's
leftover lamports on an upgrade, and `--max-len` the deploy flag that fixes a program's maximum size; Alpenglow is Solana's coming consensus
and LtHash its accounts hash (§2); "the meter" is §5.4's bucket; "the supply identity" is §5.9's equation `lock =
supply + owed + voided − lost`; r/gnops/valopers is gno.land's registry of validator operators; a sysvar is a Solana account the runtime fills
(the Clock among them); amino is tm2's wire encoding and bech32 its address text; ICS23 and RFC 6962 are the two
merkle proof formats gno uses; SP1 and RISC Zero are zkVMs, Groth16 over bn254 the proof system their proofs are
wrapped to for gno's native pairing check, cometblszk gno's existing verifier of that kind; GnoSwap is gno.land's
exchange; "the cold key" is Jae's offline key g1ecsu…; devnet is Solana's public test network; a precompile is a
native instruction (ed25519 verification) priced below program code; a realm is a gno package with state; GovDAO
is gno.land's governance DAO (r/gov/dao); `addpkg` is the transaction that uploads a package, the keeper the chain
code that stores it, gnomod.toml its manifest and `vm/qfile` the query that serves its files; a programdata
account is where Solana's loader keeps an upgradable program's code; a precommit is a validator's final vote on a
block and its sign bytes the exact bytes it signed; LastResultsHash is the header field committing to the previous
block's transaction results (each an ABCIResult: error, data, events); lazy execution is Solana's proposal to
execute a block after it is voted on; wincode is agave's serialization, hashv its multi-input sha256 and Rotor its
block propagation.

## 1. What this asks of GovDAO, and what is trusted at each step

**What it asks.** Nothing today, and nothing at the launch: the bridge launches under one governor, Jae's key, and
GovDAO becomes the owner of a bridge that moves GNOT and BUBBLE to Solana and back at the yield, in two proposals:
the yield vote (its acceptance of the bridge as it stands, on which Jae yields, §6.6) and, after Alpenglow,
Proposal 2 (a third removes the signers when Solana commits its validator roster on chain, SIMD-0511, no date).
Until the yield the trust statement is one line — Jae alone can change any setting, within the delays, and move
the lock after 7 days — and the rest follows from it: every builder of §5.3, `Launch` and r/hlgov's decisions to
Solana are his direct call through each realm's `Govern` entry, with the closures, checks, queues and 7-day delays
GovDAO's proposals get after the yield (a 7-day decision of his waits 7 days, anyone executes it, and two guardian
flags hold it on Solana — his own, since the four seats may all be the operator's until the outsiders are named,
which is at the yield, when the launch notice goes out), and GovDAO's proposals are refused; the week below binds
nobody, since the governor is the operator, a guardian and the signers' owner in one person — the pair that
includes the operator, with one member; nobody holds an admin key after the yield; and a leaked governor key before
it is a leaked lock (a thief with it decides as he does; his cancel answers each decision at once and the thief
re-makes it, a race two holders of one key run until the yield, which the thief cannot prevent and which makes the
thief's queued decisions GovDAO's to cancel inside their 7 days; a leak he does not see inside the first such
decision's 7 days moves the lock), so the lock stays small until the yield and he yields before inviting real
holders (§11.16). From the yield GovDAO owns every setting on both
chains: gno.land's through the realms' builders, Solana's through an immutable admin program that acts only on
decisions proven by the light client — the governor's before the yield, GovDAO's after; it never asks who decided, only that r/hlgov sent it — and on nothing else (§6.4) and holds every Solana authority from the day it is deployed — no multisig, no devices, no
hand-over. From the first block Solana accepts a gno.land transfer only on gno.land's own block signatures (a light
client: more than 2/3 of the current validators' power, whoever they are — today 5 of 6); the only machines still trusted are the three signers that vouch for the way back,
Solana → gno, until Proposal 2 replaces them with proofs of Solana's finality. Two of the four guardians are, from
the yield (§11.9; the testing phase's seats may all be the operator's), people outside the operator, GovDAO and the validator set. One person holds several of these roles
and the statement says so first: Jae is the governor until the yield (§11.16), a GovDAO member (g1ecsu…), runs the operator, holds one of its two
guardian keys and, since 2026-10-11, runs one of gno.land's validators (Mithrandir: 60 of 360, 1/6 of the power, since
Prop #13 passed on 2026-10-12 — the validators bullet); "two of three members" can be Jae and one other, and the two
houses of §3.3 — GovDAO and the guardians — share him, and he signs blocks as a validator too; the
outsiders' seats are the only ones outside the operator. After the yield, every change of a setting is a GovDAO vote: today, 2 of its 3
members, and nobody else (before it, the governor's call, and nobody else) — gno.land's validators sign blocks, which is what proves a decision to Solana, and carry
no decision of their own (§6.4). On gno.land a
change is either slow or instant.
- Slow — 7 days after the proposal is executed, cancellable in between (anyone may execute it, within 14 days;
  `Launch` too, since v27: a passed launch nobody executed in 14 days lapses and is re-proposed, never a standing trap): a raise; a swap of verifier, router, signer, guardian, watch key or treasurer; a
  credit by hand; a migration; a sweep payment; a refund of an imported send and a write-off of one Solana delivered after its refund (§5.3); r/hlgov's change of the decision recipient; a signer-rule change
  of any kind, an addition of a signer too. Also slow: a void of claims that no guardian's `payouts` flag names;
  a restore of a claim voided as forged; a signer rotation that names a rule the remaining set meets (a removal that would leave too few signers for the live rule is refused when made, §5.3).
- Instant: a lowering; removing a guardian or a watch key; removing a signer while enough remain; restoring a
  withheld claim; an unpause; a cancel; r/hlgov's `sync sequence`; and a void of the claims a guardian's standing `payouts` flag names.
- One safeguard: an unpause that clears no flag resets no meter.

What this buys: 2 members alone cannot shut the way home in under 7 days, unless the pair includes the operator (¶2's signers bullet). Only a guardian's own flag, naming the
ids it suspects, makes a void instant — never a monitor's, and never for ids no flag names. On Solana every change
is a decision carried in through the light client, and it waits by its kind: a cancel at once; an unpause 24 hours
after it arrives (it clears only flags set before it arrived, and nothing while 2 guardians hold); everything else,
"the 7-day group", 7 days after it arrives — and it keeps waiting while 2 guardians' flags hold, GovDAO's
replacement of guardians included, which lands on the later of its own 7th day and the hold's 21st day and clears the replaced keys' flags (§6.5). The
guardians delay; they never veto.

**The hard truth: every delay here protects against everyone except 2 people — and, until the yield, 1.** Until the yield the governor needs no validators: a migrate of his moves the lock on its 7th day (§5.3). 2 GovDAO members can replace
gno.land's validators in 1 block (r/sys/validators applies the change at execution). The light client and every
7-day delay in this plan stop everyone else; they do not stop those 2. What the bridge adds is a brake, never a lock:
- Any validator change pauses gno → Solana for 7 days (the quarantine; nothing shortens it). 2 guardians' flags keep that pause up until
  GovDAO replaces them — 21 days after the hold began at the earliest, 28 after the replacement's delivery at most
  (§6.5; 7 to 8 when a hold already 14 days old was standing, the guardians bullet).
- Any decision that changes what Solana trusts waits 7 days, and 2 guardians' flags make it wait until GovDAO
  replaces them: 21 to 28 days from its delivery when the hold answers it, never a guardians' veto. On Solana the brake against anyone able
  to deliver a decision — a GovDAO pair, a forger holding the validators' keys, a bug — is those 7 days, and 21 to
  28 once 2 guardians hold; what ends a hostile decision is GovDAO's own cancel (kind 1, executed the moment it is
  recorded, §6.5), and nothing else: there is no override of GovDAO in this plan, and gno.land's validators carry
  no decision — they sign blocks, and a decision is proven by their signatures or never arrives (§6.4). A cancel is
  proven under the honest chain's headers, which no forger can keep out (a proof carries its own header and
  commit, §6.2), but it is recorded only at the next sequence number, which a forger who keeps delivering can take
  first (§6.5): against such a forger GovDAO's channel may never land, the hold is the whole brake, and the ending
  is the validators bullet's — a rotation of the set on gno.land and a fresh deployment.
- 2 of the 4 guardians are outside the operator and GovDAO: a brake that buys time, never a lock. The operator's
  machines are not in the gno → Solana trust path from the first block (its updater feeds the client, but anyone
  may, §6.1).
- Nothing in this plan binds the 2 members on gno.land itself.

What the week buys on gno.land is **time to leave**. Holders can burn synthetic back at the meter's rate — 1/10 of
what is left in the lock each day, about 1/2 of the lock in 7 days (1 − 0.9^7 ≈ 52%; up to 57% if the meter was
full when the week began; with a lock above 100,000 GNOT and 4 or more recipients leaving — a single holder leaves
at 1/40 a day, and a lock under the reach of §5.4's floor can leave entirely). That holds because the pair's at-once powers stop short of the exit: emptying the signers
waits 7 days; voiding claims is at once only for the ids a guardian's standing `payouts` flag names (a watch key's flag
pauses payouts but never unlocks a void); an unpause that clears no flag resets no meter; restoring a forged claim waits 7
days (§5.3). So a pair that includes the operator — Jae's own guardian seat can flag, and the operator's signers are
the only way home until Proposal 2 — can close the exit at once; a pair without the operator, and without any other
guardian's `payouts` flag (the outsiders' included: a guardian who flags payouts on GovDAO's word is its
accelerator, §11.9), cannot. In numbers: GovDAO has 3 possible pairs, 2 of them include Jae, so the week binds 1 pair from the yield: moul
and g1aeddl (and nobody until it, the governor being Jae); a pair that includes the operator can also take the lock at the meter's rate from day 1 — its signers
credit forged claims, and when the outsiders flag the ids it removes their seats at once, which clears the flags
and resets the meters (§5.3) — so against that pair nothing on gno.land waits a week. The week's price: a forgery nobody flags for 7 days — the operator's own, with its monitors silent and
both outsiders asleep — pays out about 1/2 of the lock before GovDAO's void lands, where an at-once void would have
stopped it at GovDAO's first look. A pair that has replaced the validator set also controls what enters blocks, so
the week assumes blocks keep coming and carry holders' transactions. The real fix belongs to gno.land: a delay on
validator changes and more GovDAO members (§11.1; Jae's, no date).

**The end state.** Solana accepts a gno.land transfer only after checking that more than 2/3 of gno.land's
validator power signed the block it came from; gno.land accepts a Solana transfer only with a cryptographic proof
that Solana finalized the burn — for blocks nobody has griefed; a griefed block's burn is credited by GovDAO's vote
(§7.2). The one key that could rewrite the controlling program on Solana is destroyed before `Launch`; what
stays changeable is changeable only by a decision — the governor's call until the yield, GovDAO's vote after — after the delay; two guardians' flags can only delay it, until
the governor, GovDAO from the yield, replaces them.

**What is still trusted**, so the proposals can repeat it:
- The governor, until the yield: one key, compiled into the five realms at deploy, that makes every decision of
  §5.3 and r/hlgov by direct call — the same closures, checks, queues and delays as a GovDAO proposal, GovDAO's
  proposals refused meanwhile — and nothing else: it holds no Solana authority (the admin program takes r/hlgov's
  decisions and never asks who made them), clears no flag but by the decisions any governor has, and shortens no
  delay and no hold; it can migrate the lock to a realm of its choosing after 7 days, credit any claim after 7 days
  and, with a guardian seat of the operator's, void the claims that seat names at once. The key is Jae's choice at
  deploy (§11.16: the cold key g1ecsu… for any deployment that will hold real funds, a hot key only while the lock
  stays small), it cannot change after the deploy — the yield is the one way out of it, one-way and at once — and it
  is the whole trust of the testing phase: the lock is small by rule until the yield, no real holder is invited
  before it, and the yield vote is where GovDAO accepts what the testing phase showed (§6.6). After the yield this
  bullet is empty: nobody holds an admin key.
- GovDAO, from the yield: 3 T1 members (each with power 3, so 9; a proposal passes at 66.66% of the power, so two of them), no T2 or
  T3; a proposal's acceptance is tallied when it is executed (§2), it never expires, and anyone executes it — with `ExecuteOrRejectProposal`, under which an executor's error denies it for good (§5.3). GovDAO sets the
  validator set in one block. "GovDAO decides" means two named people, with a deadline on what they decided only
  because our builders add one. On gno.land GovDAO is sovereign over the bridge: after 7 days it can move the whole
  lock to a successor realm (migrate) or credit a claim it verified by hand, and it can void any claims, by list — at
  once for the ids a guardian's standing `payouts` flag names, after 7 days otherwise — "funds don't move" in §3 means not
  without a vote and a week, never that they cannot.
- gno.land's validators. There are 6 (4 from genesis, a fifth since height 487,883 on 2026-10-01, a sixth since
  720,333 on 2026-10-11), each with power 60 since height 741,862 (2026-10-12, Prop #13), 360 in all. By signing
  address (r/gnops/valopers, read 2026-10-12) they are: gno-core-validator-1 (run by g1aeddl…, a GovDAO member),
  moul (g1manfred…, a GovDAO member), berty-validator-1 (Berty, that member's company), onbloc-validator-1
  (Onbloc), samourai-crew-validator-1 (Samourai) and Mithrandir, registered to g1ecsu… — Jae's own machine (bare
  metal, the consensus key in a hardware signer), added by Prop #12 at power 1 and raised to 60 by Prop #13, which
  he and moul voted. So the operator's owner also signs blocks, with 1/6 of the power: he can neither forge nor
  halt alone (the arithmetic below), but "the operator's machines are out of the gno → Solana path" is true only
  up to that share, and the yield vote states the share of the day (§8.1), as it names gno-core-validator-1's
  people so a member can see whether they are the operator's staff.
  What they can do. Any group holding more than 2/3 of the power can sign a block that never happened, and the light
  client will believe it — a forged transfer, and a forged decision too, since decisions arrive under the raw
  light-client ISM and skip the pausable ISM (§6.5); nothing on Solana can tell a forged header from a real one,
  so the brakes are the 7-day wait, which lets two guardians hold a forged decision, the hold's 21-day floor under
  a forged replacement of the guardians (§6.5), and GovDAO's cancel of each forged decision inside its 7 days — a
  cancel per forged decision, or one naming a range of numbers — proven under the honest chain's headers; what the
  forger can do against that is take each next sequence number first (§6.5), which is why the sane end is a
  rotation of the validator set on gno.land and a fresh deployment (below) (a forged
  `cancel` runs at once and can only kill GovDAO's own pending decisions, which are re-proposed; a forged `unpause`
  lands in 24 hours unless a hold stands).
  Today that takes 5 of the 6 (300 of 360; any 4 hold 240, exactly 2/3, short of the 241 the rule needs). The two
  members, the member's company and Jae's machine hold 240 of 360, exactly 2/3 — one outside validator short. One
  proposal is open and the vote restates this for the set of its day: under Prop #10 (moul to 1, 301 in all)
  forging takes 4 of the five at 60 and the member-linked share is 181 of 301 (60%), still one short. Against
  GovDAO itself there is no override in this plan, by the validators or anyone: the guardians delay, GovDAO
  decides. Nothing is staked
  and nothing is slashed: their reputation is the only deterrent. Any group holding 1/3 of the power or more can
  also halt the chain by not signing (a commit needs more than 2/3, and with the group out the rest reach at most
  2/3) — today any 2 of the 6; moul's own validator and Berty's together hold 120 of 360, exactly 1/3, so one
  member can halt gno.land (under Prop #10 they hold 61 of 301 and no single member could) — which freezes every send
  and claim on gno.land, and the client with them: it resumes when the chain resumes under its last set (there is
  no trusting period, §6.1), and if the chain restarts under a new chain id or a set its last header never
  announced, the client never follows and the deployment ends (§6.4); no bridge rule touches that, and ¶2's week
  assumes blocks keep coming and carry holders' transactions, which a pair that has replaced the validator set
  also controls.
  If their keys leak. The keys of enough validators to pass the rule (5 of the 6 today, 300 of 360)
  can sign a block that never happened, and the light client believes it. A thief with them and the validators who
  still hold the same keys are indistinguishable to Solana; what follows is a race, not a capture, and it ends
  only with a new deployment:
  - The thief feeds the client forged blocks ahead of the real chain. No quarantine fires (no validator change is
    announced), and no rule on Solana can refuse the headers: they carry the signatures the client was built to
    trust.
  - Within 2 minutes the operator's watch sees the fork and flags (the header rules wait for a second reading, §5.9); 2 guardians hold both Solana objects (3 or 4 when
    they can, §11.9). Nothing mints and no forged decision executes while the hold stands: a forged "replace the
    guardians" decision cannot land before the hold's 21st day (§6.5), and inside those days GovDAO votes and
    delivers its cancel of it and of every other forged decision — one cancel names a list or a whole range of
    numbers, and it executes the moment it is recorded (§6.5). The cancel is proven under the real chain's headers,
    which the thief cannot keep out, but it is recorded only at the next sequence number, which the thief can take
    first: a thief who records a forged decision at every number, for fees alone, keeps GovDAO's cancel out, and
    GovDAO's answer is a `sync sequence` and a vote per attempt (§5.3) — a race run by hand against a machine, not
    a weekly chore, and GovDAO wins it only by landing a cancel per forged decision, or per range, inside each
    one's 7 days. So the Solana side freezes for as long as the guardians keep holding and GovDAO keeps cancelling;
    the deployment is abandoned for a new one the day the thief begins, after the set is rotated on gno.land, and
    if the hold lapses or a cancel comes late, the thief's replacement lands and the side is the thief's. Without a
    hold — one awake guardian, or none — a forged replacement of the guardians lands on its own 7th day and clears
    the flags, and with it land the forged kind 12 (any cap), kind 8 and kind 9 of the same week, so from day 7 the
    side is the thief's as under a lapsed hold: the day cap per token (§5.4) bounds the first week alone — what
    minted before the flags, and a day's cap for each day until day 7. The guardians' standing order follows: two
    flags on day 0, or the side is lost in a week.
  - Meanwhile, if nothing minted in the two minutes before the flags landed (what could is bounded by the pausable ISM's own limits: a per-message maximum of the smaller of 25,000 and the day cap, and a day cap of a tenth of the expected lock, at most 100,000, per token, §5.4), holders leave at the meter's rate — 1/10 of
    the lock a day across 4 or more recipients, 1/40 for one — for as long as the forged chain's lead over the real
    one stays under an hour of blocks, past which the signers stop (§5.6) and the exit is by vote. If something did, Solana's supply exceeds the lock,
    the signers stop crediting (§5.6, the next bullet) and every exit is by GovDAO's vote, 7 days a batch, pro
    rata — the forged synthetic, sold, has left the lock short.
  - The signers stop crediting the moment Solana's supply, the burns in flight counted, exceeds what §5.9's identity allows — the lock less what is owed and voided, plus what is lost; tight, not loose (§5.6) — a
    forged mint burned back toward gno.land stops them by its own amount — and the watch keys stop
    payouts. GovDAO then passes 1 proposal — one closure of the void builder, whose `andUnpause` option takes the
    unpause builder's parameters (§5.3) — that does 3 things in order: void the forged claims (at once, for the
    ids a guardian's `payouts` flag names — Jae's seat or an outsider's, set from the watch's alarm), reopen
    payouts in the same step (so nobody can run the reopening first), and retire the watch keys (a guardian re-sets
    `sends` with their own key at that moment, since removal clears a key's flags). Automatic crediting is over
    for that deployment.
  - Burns after the cut — the Solana slot S at which the client first verified a header that differs from
    gno.land's (§6.4) — are paid by GovDAO vote, in batches, pro rata: if the lock holds 70% of what is owed,
    everyone gets 70%, not the first to run 100%.
  - Nothing more is promised. A set GovDAO has voted out loses this power the moment the client has followed the
    rotation (§6.1: only the current set counts) — if it can: a thief who forged proof headers at past heights
    left the client's trusted header real, and the rotation reaches it; a thief who ran the client ahead of the
    real chain left it on a forged chain the announcing header can never update, and the fresh deployment is the
    only cure (§6.4).
- The 3 signers are the operator's 3 machines, and until Proposal 2 they are the only way home: a burn on Solana is
  paid on gno.land only after 2 of the 3 sign for it (after Proposal 2 they vouch for Solana's validator roster per
  epoch until SIMD-0511, §7.3). They can do 2 things and nothing more. 1. Pay: 2 signers can credit a claim for a
  burn that never happened. It pays out at the meter's rate — 1/40 of the lock a day for one claim (a recipient's quarter), 1/10 a day
  across 4 or more recipients, or the floor if that is more — 10,000 GNOT a day for the bridge, 2,500 for one recipient (§5.4) — until GovDAO voids it. They can never mint on Solana. Each signer reads Solana through 2 RPC providers and
  no 2 signers share a pair, so a forged burn needs 2 signer machines, or 3 of the 4 providers, lying at once.
  2. Stop: if the operator's machines stop signing, nobody leaves Solana until GovDAO swaps the signers — 7 days plus
  the swap. A GovDAO pair cannot empty the signer set in under 7 days and can freeze at once only the claims a guardian's
  standing `payouts` flag names (§5.3); so a pair that includes the operator — Jae is a member,
  the operator and a guardian, and his own seat can flag — can close the exit at once and for good, and a pair
  without him, and without another guardian's `payouts` flag, cannot close it for 7 days (it can price small exits
  out in 7 days by raising the mailbox's protocol fee to its 0.05 SOL maximum, ≈$5.50 a burn, §13's kind 7). "Paused, exits open" therefore means open at that pair's
  pleasure until Proposal 2's verifier replaces the signers — and even then only for blocks nobody has griefed:
  griefing every Solana block costs ≈8–16 SOL a day (at today's 0.22-second slot), griefing the 1 block that carries a burn costs a block producer,
  or anyone who sees the burn coming, under a cent, and a griefed block's burn is paid only by a GovDAO vote (§7.2),
  at every stage of this plan. If §11.13's partner signer is adopted, every credit needs the partner's signature too:
  the operator alone can then forge nothing on this path, but the partner alone can stop the exit for 7 days plus a
  swap, exactly as the operator can, and joins this list (held to the outsiders' rule, §11.13: outside the
  operator, GovDAO's members and their companies, and the validator set).
- The guardians (four people: Jae, one more person of the operator, and two outside the operator, GovDAO and the
  validator set, §11.9), and the watch keys (the operator's monitors at two cloud providers, pausing by themselves on
  an invariant break): any one flag pauses a function of a route on gno.land (sends, credits or payouts), gno →
  Solana deliveries, or the admin program's decisions; on Solana two guardians' flags make a hold, which no unpause
  clears and which keeps every decision waiting — GovDAO's replacement of guardians included, which lands through a
  lone flag 7 days after delivery but under a hold no sooner than the hold's 21st day (§6.5), and then clears the
  replaced keys' flags. That is the whole
  of their power: they can pause, and two of them can hold until GovDAO replaces them; they cannot veto, object to
  or stretch anything (Jae's decision of 2026-10-12, §11.9). What it buys: a lone flag stops minting at once,
  within a minute of the watch's alarm; a hold stops it for 21 days from the hold's start at least — 28 from the decision's delivery at most
  — and for as long as GovDAO takes to vote plus 7 days if that is longer; those 21 days are GovDAO's time to
  cancel a replacement of the holders it never made (§6.5), and for a GovDAO that is itself the problem, is gone,
  or cannot reach the admin program past a forger, the hold is the whole brake and the deployment's ending is
  §6.4's. Who can lift
  what: a guardian or monitor clears its own flag; GovDAO's unpause clears the flags set before it arrived, 24
  hours after delivery, unless a hold stands; GovDAO's replacement of a guardian clears that key's flags when it
  lands — 7 days after delivery, or the hold's 21st day if that is later. The operator holds two of the four keys,
  so it alone can hold the Solana side for 21 to 28 days past any vote, and GovDAO can replace its seats like any
  other; and a hold it set long before, on a quiet bridge, is
  already old, so against a pair that includes the operator the outsiders' flags add nothing to a decision's own
  7 days (8, with the day the operator's own lift takes, §6.1). A stolen guardian key freezes gno → Solana until GovDAO's
  replacement lands (7 days from delivery; two stolen keys are a hold, and 21 to 28 days) — the thief can re-flag until
  then and not after, and can clear its seat's flag as well, so the honest seats hold without it; one awake
  outsider alone can only re-flag against each unpause (24 hours apiece) until replaced; a stolen watch key the
  same as a stolen guardian key; on gno.land GovDAO unpauses and removes either in one proposal, at once; a monitor
  bug is the same, and never a theft. Two guardians who will not yield — rogue, stolen or right — hold the Solana
  side (paused, the exits open as far as the lock reaches; burns return funds to gno.land, undelivered sends are
  refunded by migrate) until GovDAO replaces them, on the hold's 21st day at the earliest; against a GovDAO that is
  itself the problem, the brake is those 21 to 28 days and nothing after them; and against a GovDAO pair that includes Jae, the two outsiders' flags
  are the only brake he is not part of, worth those days only if the operator's seats were not already holding
  (above). A pair that delivers the replacement together with its
  decision pays no extra day beyond that: 7 days is the whole brake when nobody holds, 21 to 28 when two guardians
  do. Each seat's key is held by its person and a named backup (§11.9).
- The operator: runs everything off chain, paid from the bridge's fees through the treasurer GovDAO names (§5.5),
  and pays the machines, the Solana rent and the audits; its absence delays (and, past a float's runway, parks) gno →
  Solana deliveries without stranding them: anyone, a GovDAO member with the published tool included, can update the
  light client, relay, re-emit, claim and execute a matured Solana decision (`hlrecover execute`, so a replacement of the operator's own seats never waits on the operator's relayer) — though nobody will relay for free, so if the operator stops, GovDAO names
  a new treasurer and the fees fund the next relayer; Solana → gno needs its signers until Proposal 2 (the signers
  bullet), and after Proposal 2 its prover — without it every exit is a credit by vote (§7.2) until another prover
  runs. Its monitors poll every ten seconds; a flag lands within a minute of a reading that passes §5.9's
  consistency checks — two minutes for the client-ahead and header rules, which wait for a second reading (the identity waits for its sources, a disagreement for a third).
- The code. 6 programs run on Solana. 3 are Hyperlane's (the mailbox, and the token program deployed twice): byte
  for byte the builds other people audited and run on Solana mainnet today. 3 are ours (the light client, the
  pausable ISM, the admin program), plus the ZK verifier realm of Proposal 2. Ours have been reviewed by §12's model
  rounds and by nobody else yet: §11.2 decides whether a named human reviews them (2–3 weeks) and whether an outside
  firm audits the 3 Solana programs before the yield or within the first 3 months after it (the testing phase's small lock may precede both, §11.16). None is formally proven. 5
  of the 6 can be changed after launch — the token code included, so the power to mint is 1 vote and 7 days away —
  only by a GovDAO decision that waits 7 days, and waits on while 2 guardians' flags hold, until GovDAO replaces
  them.
  The 6th, the admin program, can never be changed. If it has a bug, 1 of 4 things happens: (1) a bug in its heart
  freezes every Solana setting where it stands and makes any flag then set permanent; (2) a bug in how it records
  decisions or counts days freezes them with it — there is no second path into the program (§6.4), so the cure is
  a new deployment; (3) a bug in how it carries
  out 1 kind of decision is cured by a kind 9 hand-over to a successor built without it, since the recording and
  kind 9 are sound by the case; (4) a bug that fails open —
  a missing authority or sender check — is an unauthorized upgrade of a token program and a mint, and the brake
  is two guardians' hold on the foreign decision `hlwatch` shows and GovDAO's cancel of it, then a hand-over by
  kind 9 — unless the bug is there too — which is why §11.2's
  audit is recommended before the yield for this program first. Where none of these works,
  the ending is the one a leak of the validators' keys gets: the Solana side stops, every exit stays open as far as
  the lock reaches, and the bridge is deployed again.
- Solana's validator roster per epoch until SIMD-0511 lands (vouched for by the signers, 2 of 3, with a comparator
  an outside guardian runs, §7.3); Alpenglow's 20% safety bound; the LtHash collision assumption; and at every stage
  the fact that a block anyone has griefed (§7.2) is credited by GovDAO's vote, not by a proof.
- BUBBLE itself: its supply is minted by the operator's key under a day cap and a reserve (the figures are in realm/bubble's own
  render, outside this plan), with Jae's cold key as admin; new BUBBLE dilutes a Solana holder exactly as it dilutes a gno.land holder. Outside this
  bridge, said here so nobody reads "lock" as "fixed supply".
- If GovDAO dies (two of three keys lost) nothing can add members, rotate a signer set or change a verifier; the
  bridge runs on in its last configuration until a guardian pauses it, and then stays paused unless that guardian
  clears its own flag — on gno.land. On Solana nothing moves for it either: no decision comes, and a flag set then
  is permanent (§6.4). New members
  are gno.land's own governance's to add, outside this plan. The lock stays.

## 2. Facts this plan rests on (verified 2026-10-10 and 11, the validator set re-read 2026-10-12; ◆ = corrected by a review)

gno.land (gno v1.2.0 sources — gnoland-1 reports `v1.0.0-rc.0`, tm2's constant, and the public node's `.app/version` answers `dev`: an untagged docker image whose commit is unknowable from outside; tags v1.2.0 to v1.5.0 differ in nothing the realms use, every consensus byte of today's head reproduces under v1.2.0's tm2, which v1.5.0 leaves unchanged — the client's own check, and the only one, since the validators are asked nothing (§11.10); the launch notice says the running commit is unknowable from outside and names what the public node reports, and the realms' suites run on the pinned v1.2.0 toolchain):
- Validators: 4 from genesis, 5 since height 487,883, 6 since 720,333 (2026-10-11, Prop #12: Jae's own, at power 1
  until Prop #13 raised it to 60 at 741,862 on 2026-10-12); ed25519; powers 60 each (360 in all; Prop #10, moul to 1,
  is open, §1 has the arithmetic under it), set
  by GovDAO through r/sys/validators/v0's `NewValidatorProposalRequest` builder on gnoland-1 (master's v2 `NewPropRequest` is the same
  pattern; the proposal's description lists each add or remove) and applied
  at execution, the realm emitting `ValidatorAdded` and `ValidatorRemoved`: a change executed in block A is announced
  in header A+1's NextValidatorsHash and the new set signs from A+2 (tm2 execution.go); no bonding, no slashing. In
  2,781 sampled commits (0.4% of blocks) every validator's precommit was present; the client's bar is the chain's own
  commit rule (power-weighted +2/3), so by construction it never stalls while the chain is live.
- Votes sign `amino.MarshalSized(CanonicalVote)` (vote.go:73, canonical.go:87), 110 bytes at today's heights; the
  header hash is a 16-leaf RFC 6962 merkle (block.go:311). Reproduced against height 701,862; the bytes are in §13.
- ◆ The app's `main` store is a B+32 tree with ICS23 proofs (`ics23:bptree` then `ics23:simple`), path
  `.store/main/key`, verified against header H's AppHash at height H−1. The object graph is NOT soundly merkleized in
  v1.2.0 (a parent persisted before its child in one transaction keeps a zero hash for the child link; reproduced with
  a four-line filetest and fixed by a tested patch — notes/gno-object-hash-gap.md), object hashes are 20 bytes, and
  object bytes live in the unprovable `base` store. So the bridge proves events, not state.
- ◆ Events: `header(H+1).LastResultsHash` is an RFC 6962 merkle over block H's per-transaction
  `ABCIResult{Error, Data, Events}` leaves, 32-byte sha256 throughout (reproduced); a `chain.Emit` event carries
  `pkg_path` = the package of the code that called Emit, set by the VM (emit_event.go:52,67; machine.go:2588), so a
  callee realm emits under its own path and nothing can emit under hlmailbox's; a failed transaction's leaf carries
  `Error`; a panic that crosses a realm boundary aborts the whole transaction and `revive` exists only in tests, so no
  event of a reverted sub-call ever reaches a leaf; ABCI responses are never pruned and `/block_results` is served.
- Native crypto: ed25519, keccak256, sha256, bn254 (G1Add, G1Mul, PairingCheck ≈ 1.8M gas for four pairs by a draft gas table not yet on master: Groth16
  verification), cometbls/cometblszk. No BLS12-381, no blake3. ugnot is unrestricted.
- GovDAO (r/gov/dao proxy, impl v0): `SimpleExecutor.Execute` is a crossing method of the proxy gated on
  `Previous()` being `gno.land/r/gov/dao` or one of its subpackages (our closures check `==`); it calls the
  builder's closure with `cross`, so inside the closure the current
  realm is ours and the previous is the proxy; `ProposalRequest`'s executor has no getter, so a member cannot re-wrap a
  builder's executor under another description; only members create proposals; acceptance is tallied at execution;
  an accepted proposal is executed by anyone, any time; a closure that returns an error marks the proposal denied for
  good when it is executed with `ExecuteOrRejectProposal` (under the plain `ExecuteProposal` the proxy re-panics the
  error and the transaction reverts, the proposal standing), a panic leaves it for a retry (impl/v0's
  govdao.gno:162-170 in v1.2.0, the code on chain byte for byte; master has moved to v3/impl, so a
  proposal cites the tag). The "created in" string is the executor's own. On master (2026-10-11) `SimpleExecutor.Execute` is ungated —
  `SafeExecutor` carries the allowlist — and the proxy panics on an executor's error, so an error and a panic both
  revert and "denied for good" is impl v0's; our closures gate on `==` themselves and depend on neither, and the
  launch notice names the impl gnoland-1 runs at launch.

Solana:
- Tower BFT: no compact finality proof, no published proof of safety or liveness; votes are transactions.
- Alpenglow (SIMD-0326): BLS12-381 min-pk aggregates over SHA256; `Notarize(block)` 60%, `Finalize(slot)` 60%,
  `FinalizeFast(block)` 80%, `Skip` 60%; safety below 20% Byzantine stake. Slow path = two certificates. Testnet since
  the week of 2026-09-22; no mainnet date (migration is X+5000 slots
  after activation): Anza's published target is October 2026 with Agave 4.3, which mainnet runs (4.3.0 on 2026-10-13) with the gate still absent and no epoch announced; nothing here depends on the date. An epoch is 432,000 slots, ≈26 hours since the slot-time feature gates (200 ms nominal from 2026-10-08, 219 ms measured; §13); epoch
  E's stakes are fixed at the start of E−1.
- Block id = double merkle over the slot's FEC-set roots plus a parent-link leaf; `BlockFooterV1` carries the
  block's own post-execution bank hash and is under the block id.
- The bank hash is `sha256(sha256(parent ‖ sig_count ‖ last_blockhash) ‖ lattice_hash[2048 B])`; the merkle
  accounts-delta hash is gone (SIMD-0223) and the lattice hash supports no inclusion proofs (SIMD-0215); failed
  transactions stay in blocks and receipts (SIMD-0064) are stagnant. The epoch stake table is committed nowhere until
  SIMD-0511 (PR #676, open, no date).
- ◆ Rent on mainnet and devnet is 5,080 lamports per byte (plus a 128-byte overhead per account), read from
  `getMinimumBalanceForRentExemption` on 2026-10-10 (0 B → 650,240 lamports; 211,069 B → 1.07288 SOL). The 6,960 in
  the documentation is what a fresh test validator's genesis uses, so the local run's accounts cost 37% more than
  mainnet's. Program rent: mailbox (211,069 B) 1.07 SOL, multisig ISM (166,021 B) 0.84 (measured; not deployed
  since v16), each token (357,789 B) 1.82; ours as built on 2026-10-13 (M2g, a milestone of BUILD.md; the sizes a reproducible build will
  confirm): the light client (270,120 B) 1.37, the pausable ISM (164,432 B) 0.84, the admin program (316,512 B) 1.61 — as built after the sixth audit round, M2j's removal of the recovery path and M2k's starting number (M2k: `Init` carries r/hlgov's counter, +8 bytes of instruction, −144 B of program; M2j: the admin program −84,792 B and the client −3,648 B, its state 1,574 B and never growing; M2i: a mailbox decision never reads the client, a kind 9 spent at another clock factor, the hand-over sweeping the authority float; M2h: an un-inherited successor runs nothing of its own before the hand-over, the hand-over's wire contract pinned byte by byte; M2g: a growth keeps the ring's capacity, a kind 9 to a successor that cannot inherit spent, no recovery stage on a retired program; M2f: a retired predecessor closes what pends there, a decision body bounded by its account, a self-transfer spent, a staged ring proof kept through the ring's churn; M2e: one round per commit, the ring kept through a growth, a proof header recorded once; v29: the recorded settlement, the hold history, the mint limits and chunked staging grew the admin program by a sixth, 337,576 → 397,640 B);
  a deploy pays that rent once (the loader drains the buffer into the payer as it creates the program data), and a
  buffer is left to close only after a failed deploy. A delivery record is 0.00095 SOL; a recipient's first token
  account 0.0015; a dispatch record 0.0016; the state accounts of the six program deployments ≈0.13 together as built (the client's state account, grown for the set history, the ring and the ring epoch, 21,168 B ≈0.11; the admin program's 1,574 B ≈0.008; the rest ≈0.01).
- Hyperlane's Solana programs (read from the sealevel tree of 2026-10-09; the mainnet bytes were deployed in 2024 (the mailbox) and 2026 (the token) and are older — a reproducible build at the deploy commits is the check, §8.2): the mailbox picks the ISM by a CPI to the recipient, verifies before creating the
  processed-message record (a failed verify leaves the message deliverable), then calls the recipient's `handle` — an
  error there reverts the record too; it binds a per-recipient process authority to `Handle` (and, in the master tree, a per-ISM one to `Verify`) and passes `metadata` by value in the
  instruction data (kilobytes of proof need a staged account); a token keeps one remote router per domain (enrolling
  replaces it); the token owner alone may swap a token's ISM or routers; the mailbox's maximum protocol fee is fixed
  at init; nothing in the mailbox or the token program pauses. Hyperlane's tree also ships a composite ISM (an
  owner-gated pause, a rolling 24-hour rate limit, fallback routing; not in its mainnet deployments): one key's
  pause with no per-key flags, no hold and no slot-stamped clearing, which is why the plan has its own pausable ISM — for the flag model, not for
  want of a pause.

## 3. Principles

1. Trust moves; funds don't: one swappable verifier slot per direction on each chain. Funds move only by a decision
   and a week (migrate, credit by vote; the governor's call until the yield, GovDAO's vote after, §5.3) — or, while anything is trusted, by the trusted party up to the meters
   of §5.4 until GovDAO voids it; never by a key alone.
2. Power is the governor's over both chains' settings from the first block and GovDAO's from the yield; every proposal says how many people the vote
   is, and what the operator still holds — and, until the yield, that the governor is one person.
3. Two houses on Solana: GovDAO decides, the guardians can only brake — gno.land's validators only validate: their
   signatures are what the light client checks, never a channel of their own (§6.4) — and the houses share Jae (§1 ¶1), so the
   two outsiders are the brake that counts. Until the yield there is one house — the governor decides, and the guardians, the operator's own if the outsiders are not yet seated, brake only a decision his key made (§1). Four guardians and the operator's watch keys; any one flag pauses gno →
   Solana deliveries (through our own pausable ISM in front of the light-client ISM), the admin program's decisions,
   or on gno.land one function of a route (sends, credits or payouts); two guardians' flags hold it, and a hold keeps
   every decision waiting, GovDAO's replacement of guardians included, which lands on the hold's 21st day at the
   earliest, 28 days after its delivery at most (7 days after delivery when no hold stands) and clears the replaced keys' flags. There is no guardians'
   veto and no override of GovDAO: the guardians delay, GovDAO decides (§11.15). Nobody clears another's flag: a guardian or monitor
   clears its own, and an unpause decision clears, 24 hours after it arrives, the flags that were set before it
   arrived — unless two guardians' flags stand, which spends it. Guardians never move funds. On gno.land GovDAO is
   sovereign: it unpauses and removes a guardian at once, because it can drain the lock anyway (§1); there the guardians
   brake the signers (and after Proposal 2 the verifier), not GovDAO, and the gno.land list follows the Solana one.
4. Caps while anything is trusted: a token bucket, per-recipient queues and buckets paid in turns, a cap that scales
   with what is locked, claims instead of refusals, and a GovDAO void (by message id, with a restore) for forged claims.
5. Decisions are messages with a sequence number, re-dispatchable by anyone until recorded (the 14-day deadline is
   the proposal's, on gno.land; on Solana a decision has no expiry, §6.5): a decision —
   the governor's call until the yield, GovDAO's executed proposal after — makes r/hlgov dispatch; the Solana admin program is a mailbox recipient under the light-client
   ISM, records every decision in order, and executes each after its delay, once no hold stands.
6. Nothing is unfixable without being forgeable: all five programs — the light client, the pausable ISM, the mailbox
   and both tokens — stay upgradable by a 7-day decision (the token code included; two guardians' flags can delay it until GovDAO
   replaces them); a kind 9 can hand every authority to a successor admin program; a migrate builder can move a lock to a successor realm (the reference successor realm hlgnot2 and §5.9's migration drill show how); only the admin program's own upgrade key is
   burned, and after the setup step no key may close a program. The one thing no decision fixes is a client that can no longer verify
   gno.land, since every decision, the client's own upgrade included, arrives through it: that ends the deployment (§6.4). Two guardians who will not yield hold the Solana side, every exit open, until GovDAO replaces them (the hold's
   21st day at the earliest); a leak of the current validator set's keys is a race
   GovDAO must keep up by cancelling the thief's decisions (§1), and the cure for that is a new deployment.
7. Every claim in a proposal is checkable with the published tools and a laptop once they are built (§5.8 says which
   are); the proposal links the commands and says which of the operator's claims (machines, providers, backups,
   monitors) cannot be checked by anyone else.

## 4. Architecture

| | At launch (the governor's `Launch`; GovDAO's from the yield) | After Proposal 2 (Alpenglow live) |
|---|---|---|
| gno.land → Solana accepted by | the gno light-client ISM (more than 2/3 of the current validators' power — today 5 of 6) behind our pausable ISM | same |
| Solana → gno.land accepted by | hlmailbox's ed25519 verifier (2 of 3 signers; with §11.13's partner, 1 of the partner's and 1 of the operator's) | the Alpenglow ZK verifier realm for un-griefed blocks, GovDAO's vote for griefed ones (§7.2); the roster from the signers until SIMD-0511, then Proposal 3 removes them |
| Solana controls held by | the admin program from deploy, acting on proven decisions — the governor's until the yield, GovDAO's after — and on nothing else (§6.4) | same |
| gno.land settings held by | the governor through the builders until the yield, GovDAO after; the launch values compiled in, the governor's one-shot `Launch` makes them live | GovDAO |
| Pause | 1 flag pauses; 2 guardians hold (Solana) / GovDAO unpauses at once (gno.land); no veto — a hold keeps decisions waiting until GovDAO replaces the holders, 21 days from the hold's start at the earliest (§6.5) | same |

Realms: r/hlmailbox (messages in and out, the verifier slot, the `Dispatch` event, re-emit, the guardian list and the
routes' pause flags), r/hlgnot and r/hlbubble (the locks, caps, claims, routes), r/hlfees (the fee realm), r/hlgov
(GovDAO's decisions as messages). Solana programs (six deployments): our own deployment of Hyperlane's mailbox program, two synthetic
tokens (Hyperlane's token program, deployed twice), and our own light-client ISM, pausable ISM and admin program.

## 5. The launch — gno.land's side, the signers for the way back, the Solana deploy (everything here lands before
launch; realms are immutable, so Proposal 2's needs are deployed now)

5.1 hlmailbox keeps one *verifier* per origin chain: the code that decides whether an incoming message is genuine. A
verifier is itself a realm; it registers once and GovDAO chooses the active one by name. Implementer: `type Verifier
interface { Verify(origin uint32, metadata, message []byte) error; Satisfiable(signers []Key) bool }`, non-crossing
(`Satisfiable` says whether a signer set can still meet the rule — asked at creation and again in the transaction
that executes the removal, §5.3); `RegisterVerifier` only from an /r/
realm under its own identity (a sub-realm's is refused, so an entry costs a package deployment), stored with its pkgpath, the registry listed twenty at a time; an implementation is a type declared in the registering realm — the one gate the mailbox cannot apply, which §8.3's member checks in the source; GovDAO activates by pkgpath; the active pkgpath rendered; verifier realms never
`private`; `done.Set(id)` before `Verify`, and a `Verify` error is a panic (the transaction reverts, the mark with it),
so a refused message stays deliverable and a verifier can never cause a double release. The `Dispatch` event carries
id, nonce, origin, destination, sender, recipient and sha256 of the message bytes (encodings in §13), because the
light client proves it from `LastResultsHash`. `Reemit(nonce)`, callable by anyone, emits the same event again in a new block (the
id is unchanged, so Solana's processed-message record still refuses a second delivery): a message dispatched before a
validator-set change, or whose results leaf is bigger than the ISM accepts, is delivered from a fresh header; the
relayers call it without being asked. `Dispatch` stays open; the routes are the only senders the other side accepts.
hlmailbox also keeps the guardian list (gno keys) and the watch keys, and three pause flags per key per route —
`sends`, `credits`, `payouts` (§5.3; a guardian's `payouts` flag may carry a list of claim ids); a function of a route is refused while any key's flag for it is set;
`Paused(route, fn)` is what the routes ask.

5.2 r/hlgnot: the GNOT lock. Accepts the coins sent with a direct user call only (`runtime.AssertOriginCall()` and
`cur.Previous().IsUserCall()`), consumes the envelope exactly once per message, accepts exactly `{ugnot}`, refunds
with `BankerTypeOriginSend`, never stores a realm banker. Same caps, claims, pause and routes as hlbubble. Decimals 6
both sides. A recipient equal to any of the six realms' addresses (the mailbox, both routes, the fee realm, r/hlgov, the bubble token), or to any registered or dropped route's (a successor's, a re-pointed-away one's), is refused at send on
gno.land; a Solana burn to such a recipient is credited to the sweep queue (§5.4).

5.3 Governance: until the yield one key, the governor's, makes every decision, and after it no one holds an admin key; no realm exports a mutator. The only way to change a setting is a
decision made with the realm's own builder functions (the governor's direct call through the realm's `Govern` entry before the yield, a GovDAO proposal after; the rule is stated after the list): set verifier · set cap floor · set per-message maximum ·
set fee and minimum (not below the fee floor) · set fee floor (2 GNOT and 40 BUBBLE at launch, a guard against a
mistaken zero, not against GovDAO) · set treasurer · set router (the old router's messages stay accepted for
7 days more) · rotate signers (hlmailbox's set and its rule together — a threshold, or §11.13's two groups, the builder naming
both; the only signer set there is, since nothing on Solana counts a signer; lowering the threshold is a raise of
power and waits 7 days) ·
rotate guardians and watch keys (two guardians at most per proposal; the gno.land list
follows the Solana one; a removed key's flags clear with it, as on Solana) · unpause (clears every key's flag for the functions it names — sends, credits, payouts —
so reopening payouts need not reopen crediting, and may remove a guardian or watch key in the same stroke) · void
claims by message id (with an `andUnpause` option that takes the unpause builder's parameters — the functions to clear and the keys to remove — in the same closure, so a void
under a flag, the reopening of payouts and the watch keys' retirement never execute in the wrong order — §1's leak ending uses it; an unpause part that finds nothing to clear at execution is skipped and resets no meter, the void and the removals applying all the same), as withheld
(the unpaid remainder stays booked, restorable) or as forged (the
unpaid remainder is dropped and the paid part booked as lost, §5.9, so the identity closes; a withheld claim may be
re-voided as forged; a sweep entry is voided by its message id the same way — as forged only, since an entry is withheld by nature: it leaves owed and the sweep queue, restorable after 7 days like a forged claim, and a credit by id replaces it voided or not — and a void removes a claim from its recipient's queue, a restore re-inserting it at its place, so a voided flood never stands in front of an open claim; a list skips and counts an id no longer voidable — voided by another decision since, paid in full, or gone — and the closure's unpause and removals apply all the same, as a restore's and a refund's lists skip) · restore a voided claim (the full inverse of either void: the remainder back to its queue,
the paid part back out of lost) · credit a claim by message id (the hand-verified fallback, never above the lock; the same trust as
migrate; it marks the id done and refuses an id already done unless that id's claim stands voided, in which case it
restores it and then replaces the restored remainder with the recipient and amount GovDAO names, at the back of
that recipient's queue — owed moves by the difference between the named amount and the old remainder, so the identity closes on the amount GovDAO names and the watch expects owed to move; a sweep entry's id may be credited the same way, the entry dropped and the named claim queued; the cure for a diverted credit; void, restore and credit all
take a list of ids, 256 at most, as a `payouts` flag's list is — void and restore; a credit names one id, its recipient and its amount) · direct the sweep queue (pay the named entries, by message id — 256 at most — to an address, never more than the lock holds: a voided entry only after its restore, an entry gone skipped, a sum the lock no longer covers waiting; the paid entries leave the queue, so no entry is paid or credited twice) · refund a send by message id (7 days; the amount back from the lock to the sender, the send marked `refunded` — a mark the monitors and `hlwatch` read and drop from in flight, and the relayer reads before staging, so a refunded send is never delivered — so sentTotal and the lock fall together, the sum never above the lock; a successor's duty for the sends it imported, which the enrolled router no longer delivers, and never a live route's: a stranded send there waits for a kind 12, since a refund voids nothing on Solana; r/hlgov's kind-6 closure records in hlmailbox the route it enrolled and the height, and a refund is refused until that record is 14 days old (a week for the delivery, a week for Solana's clock; r/hlgov's cancel of a kind 6 clears the record) — the record is r/hlgov's dispatch, never Solana's execution, which a hold defers further, so the gate is Solana's: `hlwatch` reads the token's enrolled router on Solana and the kind 6's state and alarms NOT ENROLLED ON SOLANA on a refund proposal and on a queued refund every poll until it applies, and the refund is applied by the governor's own at-once decision, GovDAO's after the yield (`apply refund`, made only once `hlwatch` shows the router enrolled on Solana) — never by anyone's `Execute`, which refuses a refund decision, and never by the relayers' loop, which skips one; before the enrolment the predecessor's relayer may still deliver, and a refund landing first lets a stranger's `Reemit` mint the send twice — `hlwatch` prints DELIVERED beside any id Solana processed, and a `write off` builder, 7 days, books a send delivered after its refund as lost, so the identity closes (a restore, 7 days, is its inverse)) · cancel a queued change · migrate (the
lock, every claim, the sweep entries and the ids of undelivered sends go to a successor realm in pages of 500 records the successor pulls, the lock and the counters with the first page; from that page until the last the predecessor refuses every function and the successor credits nothing by the verifier (GovDAO's hand credit, restore and credit by vote land meanwhile, so the watch expects owed to move) — the pause of both, which the watch, `hlwatch` and the three signers read over the summed counters of a route's whole line of migrations, followed from the configured path to its end as the realm follows it (sixteen deep) and back to a configured successor's predecessor before page 0, invariant at every height, never flagging a migrated or importing route alone, the relayer delivering to the line's last realm and taking the line's sends as its own (a line it cannot read this tick holds its sends, never marks them) — and each page carries the recipients' counters so the successor's own credits never collide with an imported place; a range void or a second migrate is refused while pages still arrive, and the predecessor's standing flags move with the lock, page 0 waiting under a `payouts` flag; burns made during the migration are addressed to the predecessor until kind 6 enrolls the successor (after page 0, a week at least), so hlmailbox lets the successor take a message addressed to any route in its chain of predecessors (at most sixteen deep, the tools alarming `route:line` when a line cannot be followed to its end; a route migrates once) once its import has ended — the message waits undelivered meanwhile, as does one addressed to the successor itself before its first page — and a successor takes no send before its last page is in and r/hlgov has dispatched the kind 6 naming it (a send before that would park on Solana, unrefundable as a live route's) — and the launch notice says so; a successor migrates in its turn only after its import has ended (refused at creation and again when the decision would apply, pending until then), never to a path that is a route already, and a predecessor whose successor has pulled nothing accepts a second migrate that re-points it (the re-point's reach ends at page 0; kind 6 enrols a successor only once its page 0 is in, so a re-point never drops a route Solana names, and a dropped route neither sends nor credits: hlmailbox remembers it, refuses its `Dispatch` and its `Process`, and lets the line's last realm take a message addressed to it), so no lock is ever stranded on a realm nobody may pull from; the refunded sends travel in the pages with their mark, and a successor starts with empty meters as a launch route does; the successor announces itself first (`AnnounceSuccessor`, from its own `init`, naming the route it succeeds — a stranger's act for the price of a deployment, never withdrawn, proving a realm at the path and its intent and nothing more; §8.3's member reads its source and its served gnomod.toml for `private`, and that its pull — `Adopt`, as the reference successor realm carries it — is callable by anyone, page by page, as `Claim` is), and the builder refuses an unannounced one or another route's, since a mistyped path would strand the lock or hand it to whoever may deploy there; it refunds by id; its `Pull` imports a page and takes its lock in the same call, as this tree's routes do, so the pair's sum never breaks for a block — §8.3's member reads that too. The reference successor realm hlgnot2 (M1i) is the template: hlgnot's code at another path with a permissionless `Adopt`, drilled in §5.9's runs). Who decides: each of the five realms compiles the governor's address beside its launch values (§8.3 reads it in the served source), and hlmailbox keeps the yield — `Yield`, callable by the governor alone, once, recording the height, which hlmailbox renders and every realm reads on every builder call, a successor realm included, so one yield holds for the deployment's life. Before the yield a builder is reached only through the realm's `Govern` entry, the governor's direct call, which runs the builder's own closure in that transaction — the same checks, the same at-once tests, the same queue, the same 7-day delay and the same record, the parameters written as the decision's description, the deadline met by construction, since nothing stands between the call and its execution — and a GovDAO proposal made with a builder is created and voted like any other, listed by `hlwatch` as PRE-ARMED with "awaits the yield", and refused when executed — its closure panics before anything moves, so the proposal survives within its 14 days and may execute the block after the yield, which is why the governor reads that list before he yields; the relayers read nothing of r/gov/dao until the yield and count a proposal's 14 days from it; from the yield's height `Govern` is refused and GovDAO's proposal is the only way, exactly as the rest of this section says. The yield changes nothing else: a decision of the governor's still queued at the yield stays queued, anyone's `Execute(id)` lands it after its delay, and GovDAO's cancel reaches it like any other; r/hlgov dispatches the governor's decisions as it dispatches GovDAO's, under the same numbers, and the admin program checks that r/hlgov sent a decision, never who decided. Everywhere this plan says GovDAO proposes, votes, executes, cancels, voids, unpauses, replaces or decides, read the governor's direct call before the yield: the rule is the same and the hand is one. When GovDAO
executes the proposal the closure checks that the caller is
`gno.land/r/gov/dao` and that the proposal is under 14 days old (≈353,000 blocks), gives the decision a number and
records it with the proposal's description; a closure returns an error only when the deadline has passed (the
proposal is then denied for good when executed with `ExecuteOrRejectProposal` — the relayers and `hlwatch` call it
for any builder-made proposal past its 14 days, the relayers only after the yield, counting from it) and panics on anything transient, so a proposal survives a retry. Members read the
realm's own page of pending and executed decisions (the last 100 of each kind), never the proposal text; a builder
call takes no free text and writes its parameters as the proposal's description (a builder-made description cannot
be re-wrapped, so it is truthful, and the title is GovDAO's own field; `hlwatch` and the realm's page show the
parameters, never the title), and records nothing, so the page cannot be spammed — a decision exists
only when a proposal executes; `hlwatch` lists every builder-made proposal that has the votes and no execution, on every realm of each token's line of migrations, a
pre-armed one included (pre-armed: a builder-made proposal with the votes GovDAO's law needs and no execution). Timing: a raise
of a cap, a floor, a fee, a fee floor or a minimum, a swap (verifier, router, signer, guardian, watch key,
treasurer), a credit, a migration and a sweep payment take effect 7 days by block time (≈176,000 blocks at 3.43 s) after
execution — the per-message maximum raised likewise; anyone applies them by calling `Execute(id)` after the delay
(the relayers do; the caller pays gas) — and GovDAO can cancel in between; lowering any of those (the per-message maximum never below the compiled launch minimum — the transfer minimum of §5.5 — since that would stop
sends, and the cap floor never below the compiled launch floor, since that would revoke §1 ¶2's exit — the builder
refuses both), removing a guardian or a watch key, removing a signer, the rule unchanged, while the verifier says its
rule stays satisfiable by the remaining set (`Satisfiable`, asked at creation and again in the executing transaction, §5.1: a 2-of-3 rule is still met by the 2 that remain after one
removal; one of each group under §11.13's rule, so removing the partner is never at once), restoring a claim
voided as withheld, unpausing and cancelling take effect at once; a signer removal that would leave the rule
unsatisfiable is refused when made (`Satisfiable` asked at creation; one made unsatisfiable between creation and execution by another landing in between queues 7 days); any change of the rule itself (a raised threshold too — a removal and a raise at once would be the instant closing §1 forbids) and any addition of a signer, a restore of a claim voided as forged (it re-opens the identity and would trip every watch), and a
void of claims that no guardian's standing `payouts` flag names takes effect 7 days after execution like a raise.
A guardian's `payouts` flag carries the list of ids it suspects (the watch's alarm lists the forged ones; a flag
with no list pauses payouts and unlocks nothing), and the void's test is made once, per id, in the transaction that
executes the proposal, and applied there: an id that some guardian's standing `payouts` flag names is voided at
once, the rest are queued for 7 days and never accelerated by a flag that appears later (a watch key's flag pauses
payouts, the safety, and never accelerates a void, since a machine's input can be waited for or induced; a void
passed in advance and fired on an honest guardian's flag reaches only the ids that guardian named — so one
compromised signer is cut at once and a forgery the watch has alarmed and a guardian has named is voided at once,
while a GovDAO pair alone can neither empty the way home nor freeze the claims of holders leaving within the week
— §1, §11.14). Every at-once test — `Satisfiable`, the void's flag and its ids, raise-versus-lower, whether an
unpause would clear anything — is decided in the transaction that executes the proposal, against the live state
(the builder checks at creation too, to refuse nonsense early); a change that fails its at-once test there is
queued 7 days, not refused — except an unpause, which is then spent (recorded, no effect, no reset), never queued,
as on Solana — so two removals made in one block cannot both pass as at-once. An `unpause` resets a
meter (§5.4) only when it clears a standing `payouts` flag (a removal that clears one resets it the same way), and the builder refuses an unpause that would clear nothing at the time it is made, so a flood of
unpauses cannot drain the exit by resetting the meters. Every launch value — signers, routes, the active verifier
(the ed25519 one of §5.1), guardians and watch keys, fee, fee floor and minimum, treasurer, cap floor, per-message
maximum, the governor — is compiled into the realms and written by `init()` at `addpkg`, so a member reads them in the deployed
source before `Launch` and again at the yield vote (§8.3); the realms are deployed before `Launch` and refuse sends and credits until the
one-shot `Launch` builder (hlmailbox's; the governor's call in §11.16's order, or GovDAO's proposal after a yield made before it — as a proposal it carries the same 14-day
deadline since v27, a passed launch nobody executed in 14 days lapsing and being re-proposed) executes, at once, and marks
them live — the deployer holds no call on gno.land unless its key is the governor's, which the deployment's checklist says, as it holds none on Solana after the setup step; the realms'
render shows the values live, the governor and the yield's height with them, and the launch notice (the operator's post on gno.land's forum and bubblerumble.net
at the yield) repeats them. None of the five realms, nor any verifier realm, sets `private` in gnomod.toml: that flag
makes a package re-uploadable by its namespace owner (the keeper refuses to overwrite a public package and lets a
private one be replaced by another private one), and a re-upload would run `init()` again over the same package
address, where the lock lives; the keeper also rewrites gnomod.toml at `addpkg` (module, creator, height), so every
hash the vote names is of the served files (`vm/qfile`), never the repository's; every package the realms import is public (the type
checker refuses a private import) and a public package can never be overwritten; a storage deposit lives at the
chain's deposit address for the package, never in the realm's balance, and `migrate` does not count it as lock. Pause: on gno.land three flags per guardian or
watch key per route, in hlmailbox — `sends` (refuses `TransferRemote`), `credits` (refuses `Process`) and `payouts`
(refuses `Claim`) — a guardian setting any of them by judgment, a watch key setting `credits` and `payouts` on an
invariant break (§5.9: nothing credited or paid under a suspect verifier) and `sends` alone to mirror a Solana delivery
pause (nothing locked toward a paused destination); on Solana one flag per key on the pausable ISM, covering both
tokens' gno → Solana deliveries (burns are final and no ISM can stop them), cleared by its own key or by an `unpause` decision
(§6.5). `Launch` is a builder like every other: only a decision made with it executes it, once (a second errors) — the governor's call, in §11.16's order;
before it executes every other builder of the five realms and of r/hlgov but `cancel` is refused — the builder
panics at creation and the closure re-checks — so the values the checklist published are the values
that go live and nothing is queued ahead of it. A signer that detects a violation stops signing and alerts; signers hold no
pause key. r/hlgov: the same builder shape for Solana-side decisions (one builder per kind of §6.5 except the
reserved kinds 4, 10 and 11, plus `set decision recipient`: the program id decisions are addressed to, a 7-day
change, which is how a successor admin program ever receives one), deployed with the admin program once §13's kind
table is frozen, since an immutable r/hlgov that mis-encodes a kind could only be replaced (by kind 9's successor, whose init names the new path); on execution it
dispatches a message to the admin program
carrying kind, parameters and the decision's sequence number (r/hlgov's own counter, taken at execution); its builders validate parameters with the admin
program's own rules, so a proposal that would fail on Solana is refused when it is made — a kind 8 never the admin program's own id, a kind 9 and the recipient swap never the current recipient nor any former one — except what a realm cannot read: a kind 8's buffer and a kind 9's successor are checked by the proposer and by `hlwatch`, which prints "immutable" or "MUTABLE: would be spent" beside the proposal (§8.4); anyone may re-dispatch an
executed decision (same number and parameters) until the admin program has recorded it. A number a foreign
decision has taken (§6.5) loses the proposal that carried it: a `sync sequence` builder (at once — it moves
r/hlgov's counter and nothing on Solana; no kind of §13 moves the admin program's) raises the counter to the number the admin program expects next — it never lowers it, never raises it by more than 2^32, and a fresh r/hlgov for a
new chain is deployed with its predecessor's last number compiled in — and
the proposal is re-made; `hlwatch` and `hlsol audit` print both counters and the gap. The `cancel` builder takes a
list of numbers. Guards the first audit round added (AUDIT.md): a queued rotation of signers or seats records the
set and rule it was decided against and executes only if they still stand; under the two-group rule a pure removal
is at once when the live rule pruned of the removed keys equals the proposed one, the keys are a strict subset and
`Satisfiable` holds; no proposal vacates more than two guardian seats, the void builder's unpause option included, and a removal names the key the seat held when the proposal was made, skipping a seat whose occupant changed since (so a queued removal never evicts a replacement that landed in between); a `since:` range closes at the proposal's creation height and a range the scan finds holding more than 1,000 credits, voidable or not (`MaxRangeClaims`), is refused — split by heights, the decision's note naming the last height voided; the count is made again in the executing transaction, and a queued range the pages have since overfilled stays pending until GovDAO cancels it — since the walk costs ≈0.4M gas a credit by the chain's table (to be measured on a chain);
the fee never exceeds the cap floor's quarter, so a first installment always fits a recipient's meter; a credited
amount above the lock goes to the sweep queue; a guardian's `payouts` flag and the void builder may name a range of
credited claims (between two block heights, or by the verifier since a height) as well as a list of ids, so one flag
and one closure cover a flood of up to 1,000 credits, a bigger one several closures by heights; r/hlgov's kind-5 builder warns, never refuses, on its mirror of the seats; kind 8's
builder refuses the admin program's own id; kind 6's builder, for gno.land's own domain, accepts only a registered
route's address; a message whose 32-byte recipient has non-zero top 12 bytes is swept, never credited to a
truncated address; and builder-made proposals are executed with `ExecuteOrRejectProposal`, under which the
deadline's error denies for good (a plain `ExecuteProposal` reverts the error and leaves the proposal standing). Against a forger who keeps delivering, GovDAO's channel may never land a decision (§1); the brake
is then the hold alone, and the ending a rotation on gno.land and a fresh deployment (§6.4).

5.4 Caps and claims: when the bridge accepts a transfer from Solana it does not pay on the spot; it writes an IOU (a
claim) in the recipient's name, into that recipient's queue. Paying claims is rate-limited two ways: the bridge as a
whole pays from a meter (the glossary's bucket) that refills at cap/86,400 per second up to one day's cap — one day's
cap per day on average, at most two days' cap in any 24 hours, no midnight reset — and any one recipient from a meter
of its own, a quarter of the bridge's in rate and in ceiling; both meters start empty at `Launch` (the realms are deployed weeks before it) and after every
unpause, or removal of a key, that clears a standing `payouts` flag (an unpause that clears nothing resets nothing,
§5.3). `Claim(n)` is permissionless (the caller
pays gas) and pays up to n ≤ 20 claims taking recipients in turns — a cursor over the queues with something waiting,
the oldest claim of each, skipping a recipient whose meter is dry, a turn never asking for more than the lock holds (a dry lock skips, it never reverts) — so a flood aimed at one recipient slows only that
recipient, and a flood from k addresses can take at most min(k/4, k/(k+1)) of the allowance from an honest one (its
own quarter-meters bind below three addresses), paying the fee once for each burn, each burn holding its turn for
amount ÷ (cap/4) days; the relayers call it continuously. A
per-message maximum of 25,000 GNOT and 25,000 BUBBLE (the smaller of 25,000 and the day cap, one figure for both tokens, compiled into the route realms as `launchMaximum` and into the pausable ISM alike — 25,000 at the default expected lock of 250,000; a rail against fat fingers, GovDAO-settable) is enforced at
send on gno.land; a larger Solana burn is still credited and
paid in installments. The pausable ISM's own per-message maximum (§13's kind 12) is the same figure at launch and must never be below gno.land's — a send between the two strands on Solana (`OverMax`, parked until a kind 12 lands) — so a raise goes to Solana first (kind 12, 7 days) and to gno.land after: r/hlgov's kind-12 builder refuses a maximum below the route's current one, the monitors and `hlwatch` alarm `limits:mismatch` while a route's live maximum exceeds the ISM's and `hlwatch` reads a route-maximum proposal against the ISM's limits, and the route's maximum builder says so in its description. The day cap is an absolute number while the lock is any size, so at launch it is the smaller of 100,000 and a tenth of the lock the launch notice expects in its first week — the pool's seed, not a year's hope (§11.11; one launch value `hlsol setup` compiles into the pausable ISM for both tokens, the smaller of the two tokens' figures; kind 12 moves each token's afterwards; the launch per-message maximum is the smaller of 25,000 and that day cap, so no send gno.land accepts can be one Solana never fits, and `hlsol setup` refuses an expected lock whose tenth is below the realms' compiled maximum unless the realms' is set to match — the notice prints both), GovDAO raises it by kind 12 as the lock grows, the monitors and `hlwatch` alarm `mint:cap-ratio` when a token's day cap exceeds a fifth of its lock — judged only once the lock has first reached the expectation the cap was set against (a number in each monitor's and `hlwatch`'s own config, `expectedLock`: the operator's claim, which the notice states), sticky after, from 14 days after `Launch` whatever the lock has done, and from the first poll the lock stands at five caps or more — whichever comes first — so a lock that later shrinks below five caps alarms and neither a pool that never fills nor a lie in the config keeps the alarm quiet — and the notice states each cap as a number beside the lock it was set against: a forged day's mint before the flags is then bounded by a tenth of the lock, the payout meter's own bound. The cap is max(floor, 10% of what is locked) per day, the floor 10,000 GNOT and 10,000 BUBBLE at
launch, GovDAO-settable; so forged claims (four recipients' worth; one claim is bound by its recipient's quarter)
can draw at most one cap in the first 24 hours after `Launch` or an unpause of `payouts` (the meter is empty: the
floor, or a tenth of the lock) and at most two caps in the worst 24 hours otherwise
(a full meter plus a day's refill: 20,000, or a fifth of the lock) until it is voided, and an exit wave drains at a
tenth of what remains a day, never below the floor. GovDAO voids claims by message id (at once for the ids a guardian's standing `payouts` flag names, after 7 days
otherwise, §5.3) and restores
them (at once if withheld, after 7 days if forged); a voided claim's unpaid remainder stays in the lock and pays no fee. A burn below the fee, like a burn to a realm address (§5.2), is
credited to the sweep queue — counted as owed, paying nobody until a GovDAO proposal directs it (a sweep payment or a refund waits while a `payouts` flag stands, like a claim; a forged sweep entry is voided by id like a claim, §5.3). The realm renders
every waiting claim and its position; a large claim delays only its own recipient's queue.

5.5 Fees, minimums, who pays: a flat fee on top of the amount per `TransferRemote` on both gno routes (5 GNOT; 100
BUBBLE), a minimum transfer there (10 GNOT; 200 BUBBLE), and the same flat fee taken from every Solana → gno claim as
its first installment when it is paid, inside the meter (so a voided claim pays no fee and a forged credit moves
nothing before it is paid; a burn below the fee goes to the sweep queue; there is no minimum on that side because a
burn is final), all GovDAO-settable above the fee floor, all paid into r/hlfees, from which only the GovDAO-set
treasurer address may withdraw (`Withdraw(cur)`: `cur.Previous().Address() == treasurer`, to itself). As proposed,
the treasurer at launch is a named key of the operator while it pays the costs, and GovDAO can name the chain's own
address instead at any vote (§11.12). Amount-0 transfers are refused on gno and skipped by the relayer. The Solana
mailbox is initialized with `max_protocol_fee` 0.05 SOL and fee 0. Fees arrive in GNOT and BUBBLE; the operator
converts them to SOL as needed (a named, trusted chore). Five floats, each monitored at 7 days of the trailing rate
(the planned first-week rate, 50 transfers a day, on launch day): the relayers' single SOL key (primary and backup share it), the two
tokens' ATA payers, the relayers' gno gas key (500 GNOT) and the updater's SOL key (0.3 SOL: ≈3.4 months at a header every 10 minutes, four transactions of 5,000 lamports each for gno.land's six validators — three up to three validators — ≈23 days at 500 transfers a day, when a
header follows every waiting dispatch);
the relayers and the updater run on the two monitor machines (primary on one, backup on the other), where these
keys live; parked messages are retried every hour (not only after an
unpause), so a float's lapse delays and never strands — the signers' lapse is another matter (§1). Costs: a gno → Solana delivery sinks 0.00095 SOL of rent for its
record (≈$0.10) and 0.0015 SOL more (≈$0.16) for a recipient's first token account, and the send writes hlmailbox's
record on gno.land (≈0.02–0.04 GNOT of storage deposit, unmeasured); a Solana → gno delivery costs the relayer ≈0.09 GNOT of gas for
`Process` and `Claim` (an upper bound — one `Claim` call pays up to 20 claims — `Claim` is built and its share is measured on devnet, M4) plus ≈0.01–0.03 GNOT of deposit for the claim and done records (unmeasured) — gno refunds a storage deposit to
whoever frees the storage, so `Claim(n)` pays its caller the freed claim records' deposits (the claim records' share, ≈0.1–0.3 GNOT per 20 claims against ≈0.045 of gas, since the done mark stays — an
estimate until measured), which is why strangers will call it, and the successor's caller collects the
migrated records' page by page (`Execute(migrate)` only names the successor); `Reemit` and `Execute(id)` cost their caller ≈0.03 GNOT each (estimates); the 5 GNOT fee covers a
first-time recipient while GNOT ≥ $0.054 (today $0.069) and a repeat recipient while GNOT ≥ $0.021; BUBBLE's fee is
set from its GnoSwap price when the realms are compiled, to the same cost (the compiled launch value is 100; GovDAO
revisits it against the pool after launch). Runways after §11.4's funding: the relayers' 2
SOL pays ≈2,100 records, ≈42 days at 50 transfers a day and ≈4 days at 500; each ATA payer's 1.25 SOL pays ≈830
first-time recipients, ≈33 days at 25 first-time recipients a day and ≈3 at 250; the 500 GNOT gas key ≈8–10 days at 500 a day (gas plus deposit) — so the base
case is topped up monthly and the stress case every three days, alarms included. Before that funding the ATA payers'
0.25 SOL would trip the alarm on launch day, which is why §11.4 is a precondition. Proposal 2 adds its own Solana → gno
fee for proving (§7.2).

5.6 Signers: three dedicated droplets on three providers or accounts (no shared API token on the Mac), keys
generated on-box and never backed up, each running its own gno full node (its commit verification is the proof) and
requiring two independent Solana RPC providers to agree, no pair of providers shared by two signers and no provider in more than two signers (four providers
across the three, each in at most two; a forged burn then needs three providers wrong at once, and one bad
provider halts at most two signers); keys of every kind — signer, guardian,
watch, relayer-gas, treasurer — excluded from every backup, and no provider URL with its token in any alarm, log or status page (every URL is redacted before it is printed; the status pages listen on loopback only); a refusal rule (Solana supply > lock + in-flight ⇒ stop
signing and alert — tight, not loose: by §5.9's identity supply is at most lock − owed − voided + lost, so the rule
is `supply + burns dispatched and not yet credited > lock − owed − voided + lost` (the burns counted so that a
forged mint burned back toward gno.land stops the signers by its own amount), read in §5.9's order — the Solana
snapshot with the client's trusted height T first, then the own node's state at a height at or above T, or at the
node's head when T is above it inside the allowance (a signer never signs on a verdict older than one round, and
halts after an hour without a fresh one; a burn finalized above the verdict's snapshot is signed only after a later verdict whose snapshot is at or past its slot counted it in flight — one round's wait on every burn, the rule that catches a mint-and-burn inside a round); a reading the node cannot give yet alarms and
never stops, unless T exceeds both the own node's head and a second source's by more than an hour of blocks — the
client is then ahead of its chain, the mismatch itself, and the signers stop; a stop clears itself once the identity holds at a height at or above T, as it does after a
forgery is voided as forged); a nonce conflict
stops signing and alerts instead of exiting; a rotated-in signer backfills every undelivered burn; signatures
served over HTTPS and published to the operator's public checkpoint store (an S3-style bucket) too; the store keyed
by deployment. Key types: ed25519 for hlmailbox on gno.land (the signers sign nothing on Solana); each guardian
holds a gno key and a Solana key, funded by the operator
with 20 GNOT and 0.05 SOL for flag transactions (the watch keys the same); the two watch keys (a gno key and a Solana key each) live
on the operator's two monitor machines at two cloud providers, which poll both chains every ten seconds, each
reading Solana through two providers of its own, no pair shared with a signer (six to eight provider accounts in
all); the watch keys share those machines with the relayers' and the updater's keys (§5.5), so a compromised
monitor is a pause lever plus the floats, never a guardian seat or the lock; `hlwatch`
exists from launch and alarms each outsider by the channel they choose (mail, Telegram, SMS).

5.7 Relayers: `ours` requires sender == the enrolled gno router; three deterministic failures park a message and
alert, and parked messages are retried hourly — a delivery the pausable ISM refuses for its day bucket (`OverDayCap`) is retried, oldest first, as soon as the bucket read at finalized has room — and while a token's oldest such park waits, no younger message of that token that would leave it short is delivered, so a large send holds younger ones for at most its amount ÷ cap of a day rather than starving behind them — and a park whose amount exceeds the live maximum or day cap after a lowering kind 12 reserves nothing and waits for a raise, so a lowering never holds a token's flow; a cancel's delivery passes only as many pending pairs as the packet carries (about six with distinct deliverers), the program needing none of them (the bucket drains continuously at the day cap per 24 hours from each charge; there is no calendar day), and one refused for the per-message maximum (`OverMax`) stays parked until a kind 12 raises it (§5.4); backoff per message (exponential, a minute to an hour); signatures cached on disk; a non-numeric
`delivered` answer keeps the message; gas wanted from gnokey's simulation on the running build (45M measured on the pinned build, 60M in the local loop; the default is a fallback) at the chain's current price read from `auth/gasprice` before each call — 0.001 ugnot/gas is the floor, raised when a block uses over 70% of its 3B gas — an "insufficient fees" or out-of-gas refusal re-priced and retried, never parked, and a node's own minimum (the ante's second refusal, "fee required: N … set by the node") paid once under `gno.maxNodeFee` — by default the greater of ten times the block-price fee and 1 GNOT — and parked above it with the node's text; a dedicated gas-only gno key; primary and backup relayers 60 s apart; dynamic account metas
equal to the payer rejected; `GetMultipleAccounts` chunked by 100, only candidate accounts fetched, every Solana transaction of the tools sent under a 400,000-CU budget (the deploy's two token inits under 1,000,000, by the deployer's key), and no single
transaction's error pins the cursor; a body limit (1 MB) and HTTPS on the signer client; signature endpoints public; the
relayers also run `Claim` continuously, apply queued gno.land changes after their delay, call `Reemit` for any
message that needs it, and deliver decisions one copy per sequence number and only when it is the next the admin
program expects, and report a number another decision has already taken (only when the processed-message record
is still absent at finalized on a second pass, the admin state and the record read at one commitment); stage no
route message while the pausable ISM has a flag or the client's quarantine stands (a staging holds ≈0.01 SOL of
rent, and the unpause rides the same float), decisions before routes; compare a resumed stage with its plan field
by field and rebuild it on any difference, and close it after a deterministic failure; before GovDAO proposes a router swap the relayer delivers every message of the old route and the
realm's render shows none undelivered; the client refuses a raw Solana pubkey as a gno recipient and a realm address
as a recipient on either side.

5.8 Solana deploy: six program deployments — our deployment of Hyperlane's mailbox, two synthetic tokens (Hyperlane's
token program, deployed twice, since its PDAs derive from the program id), and our own light-client ISM, pausable
ISM and admin program — deployed one at a time by the deployer key, each token initialised with no interchain gas
paymaster, no destination gas config and no fee config, `--max-len` pinned to the ELF's size for the admin program
(immutable) and set at the ELF's size for the rest, since kind 8 extends the program data before it upgrades
(§6.5), any buffer of a failed deploy closed before the next; then the setup step, before `Launch`
— one logical step in as many transactions as the 1,232-byte limit needs, in a binding order (settings — routers,
ISM pointers, the default ISM, the beneficiary, metadata — then the owners, then the upgrade authorities, the admin
program's own burn last), each printed by `hlsol setup`, which resumes from chain state after a stop and repeats
nothing already done: every upgrade authority of the first five moves to the admin program's authority PDA (through the admin program's `transfer_authority_in`, which refuses the admin program's own programdata, since
`SetAuthorityChecked` needs the new authority's signature) and the admin program's own is burned; every owner (the mailbox, both tokens, the pausable ISM) becomes the admin program;
each token's ISM pointer is set to the pausable ISM and the pausable ISM's target to the light-client ISM; the
mailbox's default ISM is the pausable ISM, never a raw one, so a token whose pointer is unset still goes through it;
the light-client ISM has no owner and no setting; its two controls are its upgrade authority and the `SetAdmin`
that moves its admin role, both the admin PDA's and both moved by kind 9 (the audit prints
the client's admin too, checks that the deployed bytes carry the compiled bindings — the pausable ISM's mailbox and token ids, the client's mailbox path, the admin program's mailbox, tokens and r/hlgov — prints r/hlgov's counter beside the admin program's, a gap failing the audit, and the validator count against the client's measured bound);
the mailbox's fee beneficiary is the treasurer's Solana key; metadata update authority → none once name, symbol and
URI are final (§11.5, a launch gate), metadata pointer authority → none; the pausable ISM's guardian list and watch slots are written by its own `init`
from the same list the admin program's init data carries (no copy instruction exists; every setup-only instruction of the admin program is one-shot on chain, behind a
`setup_done` byte that a permissionless, self-checking `finish_setup` flips only when it reads every upgrade
authority and owner as the admin program or none, the admin program's own upgrade authority as none, the pausable
ISM's guardian list equal to its own, the three programs' clock factors equal and the client's origin domain its
own; each of our three programs' `init` requires the signature of
that program's current upgrade authority, read from its programdata account; Hyperlane's three inits (the mailbox and each token), which make the
payer the owner, are sent in the first slot after each deploy through a private RPC path with retries, the whole Solana side is run to a passing audit before the realms are uploaded — a front-run init costs a redeploy under new ids, and the realms compile those ids — and the audit's owner check is the residual); the deployer key keeps
nothing but what it funds the floats' own keys from (§5.5). There is no
Hyperlane multisig check on the gno → Solana path and no interim holder: the light client checks
gno.land's validators from the first delivery, and the admin program is the only owner from the first block — the
audit before `Launch`, and the members' at the yield, are of the final, immutable deployment. The pausable ISM forwards `Type` to the light-client ISM,
answers `VerifyAccountMetas` with five of its own — its flag account, its state (writable, for the mint buckets), the client's state, the client program and the Instructions sysvar — prepended to the client's (its meter is the two tokens' only: a message to another recipient passes unmetered and mints nothing of ours),
and at `Verify` trusts none of the accounts the relayer passed: it derives its flag account from its own seeds,
requires the client's state account to be the fixed-seed PDA owned by the launch client's program id (a
target that never changes) and refuses anything else, forwards the rest to the client (which derives its own accounts likewise), and
CPIs the client's `Verify` unless any flag is set or a quarantine stands (§6.1) — the mailbox passes whatever
accounts the relayer lists, so the checks are the ISM's; it keeps the guardian list (four numbered seats, 3 and 4 the outsiders', no key
in two seats or in a seat and a watch slot) and the watch keys, a flag per key with the slot it was last set at (fresh on every set; the hold's own clock is the object's, §6.1), lets each
key set or clear its own flag, and lets its owner, the admin program, clear flags set before a given slot only when
no hold stands (§6.1's `holdSince`; an `unpause` decision, §6.5), and replace at most two guardians per decision,
hold or no hold (a replacement clears the replaced keys' flags); it reads the client's quarantine from the client's state account and
fails closed on any layout it does not know. Four tools (`hlwatch`'s gno.land half built in M3a, the rest in M3b; `hlrecover` in M3d, cut to the members' tool in M3k — `version`, `status`, `body`, `execute` — its reproducible build hash pinned in the launch notice beside `hlwatch`'s, since a member who executes a decision by hand runs it): `hlsol setup` performs the deploy
and the setup step above, resumable, with "no human key holds an authority" as its done condition; `hlsol audit` prints every authority and setting — the 6
upgrade authorities (five = the admin program, its own = none), the 4 owners and the mailbox's fee beneficiary, each mint's mint authority (its mint PDA) and freeze authority
(none), the 2 metadata-pointer and 2 metadata update authorities, each token's ISM pointer, mailbox pointer, decimals (6 and
6), interchain gas paymaster (none), destination gas configs (none) and fee config (none), the mailbox's local
domain and `max_protocol_fee` (0.05 SOL; `finish_setup` reads it too), the pausable ISM's target, the client's state PDA (one, fixed seed), the enrolled remote routers, the three guardian and watch lists and every flag, the mailbox's default ISM, the ATA
payers' and deployer's balances, the client's trusted height, its hash against the chain's own block at that height, and its set hash against `/validators`, the hold history, the mint limits, the admin
program's init lists and `setup_done` byte, its last sequence number and its pending
decisions with their maturity times — printing the raw RPC calls it made so a member can repeat each by hand; `hlwatch`, for guardians, polling every
10 s as the operator's monitors do, will show who governs (the compiled governor until the yield's height, GovDAO after), pending decisions and when each matures, r/hlgov's counter against the admin program's last number (a gap means a foreign decision took a number, or GovDAO's was lost), every decision against r/hlgov's executed list, guardian-set changes with the key each replacement names, every flag, every quarantine start, every void as forged of a claim whose Solana dispatch
record matches it (a mistaken or abusive void; `hlwatch` itself reads in the monitors' order — the Solana snapshot first, then a head at or after it, a catching-up or stale node no verdict — and calls an id forged only when both its providers'
scans are complete and find no dispatch record for it, on its second consecutive poll, — the monitors' unindexed-credit rule, which flags on the second such poll — and says
"unindexed, re-read" otherwise), the client's accepted and proof headers against gno.land's, and the
supply identity of §5.9 from its holder's own sources (indexing Solana's dispatched-message accounts by message id,
since Hyperlane keys them by a unique account), and alarm its holder directly — from a node the holder runs and two agreeing Solana providers; on anything less
it alarms only, and its `payouts` flag command refuses — and its flag command sets the holder's flag on the pausable
ISM and on the admin program in one stroke (a hold on the admin program is what defers its decisions, §6.5); its
reproducible build hash is in the
launch notice and the yield vote, it is in §11.2's review scope, and each outsider compiles their own. SOL at the
mainnet rate: rent 1.07 (mailbox) + 1.82 + 1.82 (tokens) + 0.84 (the pausable ISM) + 1.37 (the
light client) + 1.61 (the admin program) ≈ 8.5 — the three measured on the built ELFs' bytes after M2j (the
v16–v25 guesses were 0.3, 1.5 and 1.0) — plus ≈0.13 of state and program accounts as built (a
pending decision holds 0.0014–0.002 more — kind 7's 153 B, kind 5's 246 B with four pairs — paid by the deliverer and returned when the decision is spent; measured as built); relayer
float 1 SOL and 0.25 SOL to each token's ATA payer at launch; ≈10.2 against the deployer's 7.36, so §11.4's 8 SOL
more is a precondition: ≈2.8 for the shortfall (0.4 more of margin since M2j), 1 to the relayers (2 in all), 1 to each ATA payer (1.25 each), 0.2 to
the four guardians' keys, 0.1 to the two watch keys, 0.3 to the updater's key, 0.1 to the admin program's authority
PDA (the rent its account reallocations need — ≈0.0005 per router enrolment at mainnet's rate — `hlsol setup` funds it 0.01 SOL, twenty enrolments, and the other 0.09 stays with the deployer for later ones — printed by the audit, §6.5) and ≈1.1 of margin, the three sizes now measured and the
reproducible build's final sizes to confirm.

5.9 Acceptance before mainnet — Jae's checklist for `Launch`, published with the deployment, and the members' at the yield vote (§6.6): every review round's findings closed (the rounds of §12) and the audit loop's (AUDIT.md); `hlsol audit` read back
after the setup step, every binary's hash pinned in the launch notice and byte-identical to Hyperlane's
mainnet ids where it is theirs; every tool flag required (no defaults pointing at Hyperlane's shared deployments); a
member read the compiled launch values (signers, routes, the active verifier, guardians, watch keys, fee, fee floor,
minimum, treasurer, cap floor, maximum, the governor) in the deployed realms' source and the chain's render of them against the
checklist, and at the yield against the yield vote (§8.3) — after `Launch` the render shows them live, the governor and the yield's height with them, and the launch notice repeats them; the light client has followed gnoland-1 for two
weeks on devnet with no failed update and replayed gnoland-1's four validator-set changes (487,883: a fifth validator at power 1; 495,707: its raise to 60; 720,333: a sixth at power 1; 741,862: its raise to 60); the results-proof verifier has been fuzzed
against real blocks, multi-part blocks (a transaction over 64 KB, §13) included; e2e on Solana devnet plus a gno test chain from the three real droplets, four runs of up to 72 h each on the
factor-8 build (the loop and gno.land's clocks; a hold from its first hour to a replacement landing on its 21st
day, 63 hours, and the deferred decisions executing; the same hold with the replacement cancelled by GovDAO's own
kind 1 on the 14th and the hold ended by the lift a day later; the hand-over and the fork drills), with hourly transfers both ways where deliveries are possible, including a transfer above the per-message maximum (credited, paid in installments), claims to
two recipients paid in turns with the fee as the first installment, a void and a restore, a credit by vote refused
for an id already done, a re-emit, a parked-then-retried delivery, a watch-key pause cleared by an unpause decision
after its 24 hours, a lone-flag clear that leaves a later flag standing, a two-guardian hold an unpause cannot clear and a
guardian replacement that lands on the hold's 21st day — and, in the third run, is cancelled by GovDAO's kind 1
on the 14th, the hold ending on the lift a day later and the decisions it deferred executing then; a cancel of a
decision recorded before the hold began; a foreign decision that takes GovDAO's next number,
the lost proposal and the `sync sequence` re-sync; two replacements in one block naming all four seats and both
watch slots, and the deferred decisions executing once they clear the hold; three guardian keys rotating their
flags under a hold without moving its start; a dip of 25 mainnet hours ending a hold and one of 23 not (3.1 and 2.9 hours at the factor); a range cancel of
a flood of forged decisions; a burst of a dozen sends inside two minutes and a monitor whose own node lags twenty seconds, neither flagging anything; a void of 300 forged claims ahead of an open one, then its `Claim`; a signer
removal refused when made and one made unsatisfiable between creation and execution queued for 7 days, an unflagged void queued and a flagged void applied at once, a restore of a forged
claim queued, an unpause that clears nothing resetting no meter, a validator change
on the devnet chain firing the quarantine, the hand-over drill (a kind 9 to a successor built for it, GovDAO's
recipient switch after it, and a decision landing on the successor) and the fork drill (a forged header fed to the
devnet client: the cut computed by the watch and by `hlwatch` as the slot of the first header of the record that
differs from gno.land's, the flags within two minutes, the burns after the cut listed for the vote, and the
deployment declared ended, §6.4), a growing upgrade (extend, then upgrade) and a cancelled upgrade's buffer return, and a migration of one route to the reference successor realm (hlgnot2, §5.3): announced, migrated under a `payouts` flag, every page pulled by a stranger's `Adopt`, a burn addressed to the predecessor delivered after the last page and one addressed to the successor waiting before it, kind 6 enrolling the successor only after page 0, the successor's send parked until the kind 6 lands, a refund refused inside the enrolment window and taken after it, a stranger's `Reemit` of the refunded send refused by the retired router, a write-off and its restore, a re-point refused after page 0; the governor's first decisions (a 7-day one queued and landed by a stranger's `Execute(id)`, an at-once one, a kind through r/hlgov recorded on the admin program and his cancel of it), a GovDAO proposal refused before the yield, the yield, the governor's `Govern` refused after it, a decision of his still queued at the yield cancelled by GovDAO's first proposal, and the rest by GovDAO — all on a devnet build
whose clocks (24 hours, 7 and 21 days on Solana; 7 and 14 days in the realms) are the mainnet constants divided by a compiled factor of 8 (14 days in 42 hours) that
the audit and the program dump show, the mainnet build's factor being 1 (§8.4 checks it) — the test chain's realms carry the same factor on their 7-day delay (the local run alone stages them at a factor of 1008, so a 7-day decision of the governor's lands inside it; the devnet build keeps 8) (a compiled constant beside §5.3's launch values, which §8.3 reads as 7 days, by block time, in the served source), and the factor divides every compiled Solana delay, the hold's 24-hour grace (§6.1) and an abandoned staging account's 7 days included, and every delay of the realms, the old router's 7 days included; the leak ending in its
order (one closure: the void under the flag, the watch keys' retirement and the unpause together, §5.3); the review or audit §11.2 decides on, published — or, on §12's rounds and the audit loop alone, the checklist's and the yield vote's statement that the
launch rests on them; its results published with the deployment's checklist and again with the launch notice (a post by the operator on gno.land's forum and
bubblerumble.net, at the yield); monitors live — the counters are sentTotal (locked by sends), paidTotal (paid out of claims, fees
included), owed (credited, unpaid; the sweep queue included), voided (the unpaid remainder of claims voided as
withheld) and lost (the part of claims voided as forged that had already been paid, plus any refunded send Solana delivered after its refund and GovDAO wrote off, §5.3), and the identity `lock =
sentTotal − paidTotal = Solana supply + owed + voided − lost` holds up to in-flight messages; a forged credit breaks
it by its amount — the monitors and `hlwatch` judge it broken by the signers' exact rule, the in-flight amounts added to the right side, never by a tolerance of what is in flight, and a credited id that neither provider's dispatch index holds flags `credits` and `payouts` on the second poll that finds it so, both scans complete (the claims walk visits every waiting claim within ⌈N/1,000⌉ polls, so a flood of N waiting claims adds that many ten-second polls to the two) — until GovDAO voids it as forged, which drops its unpaid remainder from owed and books its paid part
as lost, so the identity closes again and the signers resume; the watch flags only when the right side exceeds the
lock (the reverse, a mistaken void, alarms), evaluating it in a fixed order: first the Solana snapshot (the supply, the client's trusted height T and last
entry, the dispatch slots) from its two agreeing providers, then its own node's state at a height at or above T
(or at the node's head when the client is past it — the identity is still evaluated, at the head, and a lead beyond
an hour of blocks is itself the mismatch flag; a catching-up node, a head not after the snapshot or a stale head is
no verdict), every Solana-side reading a flag rests on — the record's transactions, read to the snapshot's slot and no further (a gap counts once the scan has reached it, a scan still behind the snapshot is no verdict; the scanners read transactions of every version Solana serves, v0 and v1 alike, so a stranger's v1 transaction blinds nothing; a scan that cannot read a transaction for two minutes alarms, and a fresh install may start from a checkpoint of the client's record rather than the deploy — the dispatch index starting there too — seeded from the mailbox's dispatched-message accounts where a provider serves them — the claims credited at or below the checkpoint's height trusted as the headers are and never flagged, one credited after it whose burn may predate the slot alarmed until the scan reaches it, a transaction past the provider's retention named so and retried hourly — which audits nothing before it, the client's own counter taken as true, so the checkpoint's base is refused when the own node's block at the trusted height differs from the client's header (the checkpoint's slot is then the cut), compared again every poll, a difference flagging `header:mismatch` as the record's does, and an outsider's first run starts from the deploy or from a record copied off a run it trusts (§11.9's standing orders say so); a poll that fails past the cut alarms `monitor:no-verdict` within two minutes and marks its identity stale, since a silent monitor would read as nothing to flag), the admin and pausable
accounts, pending decisions, processed-message records — taken from both providers with equal bytes, a
disagreement alarming and skipping that rule (a dispatch record the two providers show with a different slot, amount or recipient is disputed — out of the cut and the identity until they agree — and `hlwatch` prints it UNCONFIRMED), the Solana slot being at or
above the dispatch slot of every burn credited in that gno state (the watch indexes the dispatch accounts by id,
so it knows them) — until the order holds it alarms and never flags; stall-with-backlog (no delivery for 30 minutes with a message waiting);
delivery age (two hours); the five floats' runways; a monitor reads at least two independent sources (its own gno
node and rpc.gno.land, the gno core team's public endpoint — never a signer's node, which is the operator's too) and flags when they disagree, re-asserts its Solana flag with a fresh slot
at most once a minute while an invariant is broken (so 0.05 SOL lasts a week of it; every ten-second poll would spend it in 28 hours), and on the gno routes sets `sends` alone whenever gno → Solana deliveries
are paused (clearing it when they resume) and `credits` and `payouts` on an invariant break — never `sends` for
that, so the exits stay open (§5.3); "deliveries paused" is read from the pausable ISM's flags and quarantine. What
flags, and where: the supply identity, and a credited claim whose recipient or amount differs from its Solana
dispatch record (a diverted credit moves no supply, so the identity alone would miss it; verifier-credited claims
only — the realm marks claims credited by GovDAO's vote and the watch skips them) (gno `credits` and
`payouts`, the pausable ISM); a header mismatch — any header the client accepted or verified for a proof (§6.2's
record) that differs from gno.land's at that height, or a height past the chain's; an unfillable gap in that record;
and a foreign decision — a decision r/hlgov never executed (a number r/hlgov's render at the cut's height lacks is re-read at the node's head, a read that fails is no verdict, and the flag waits two minutes like the record's; a 7-day decision gone from the admin program inside its week was cancelled or spent, and the watch alarms it naming the later cancel of r/hlgov's that closed it, or none — a cancel r/hlgov never executed is a foreign decision of its own and flags as one) — (the pausable ISM, the admin program); a disagreement between sources —
conflicting hashes at one height, never a lag or an outage, and only once a third source (the client's accepted
header at that height, or a second public endpoint) shows the monitor's own node to be the odd one out, an alarm
otherwise — (gno `credits` and the pausable ISM, never `payouts`; while its own node is the odd one out the
monitor's identity and mismatch rules alarm instead of flagging, and "gno.land's header" in the mismatch rule is
its own node's); everything else — stalls, ages, runways,
lag — alarms only, so a pause can never feed its own flag. The header-mismatch, gap, foreign-decision and
disagreement rules never touch gno `payouts`: only the identity and the diverted credit set it, and only a
guardian's `payouts` flag, for the ids it names, unlocks GovDAO's at-once void (§5.3). README and code say what this plan says (the code's
1,000,000 BUBBLE day cap, still in `hlbubble.gno`, becomes §5.4's meter); the launch notice states the trust in
people (§1), the governor's first. The bridge launches under the governor after §12's rounds and the audit loop, with a small lock, and yields after §11.2's review or audit if either is chosen (§10, §11.16).

## 6. The light client on Solana and the admin program — in the trust path from the first block

6.1 Header verification: the client keeps a trusted header and the validator-set history (one entry per set change:
from-height, set hash, the set and the Solana slot it was announced at — the record the audit and `hlwatch` read;
the client reads no holds, and a quarantine's end is the Clock's alone). `Update(header,
commit)` accepts a header above the trusted height signed by the set its height belongs to: signatures counted by
slot index with `len(precommits) == len(valset)`, each precommit's sign bytes built from its own BlockID and its own timestamp and tallied
only when equal to the commit's, `Type == 0x02`, chain id in the sign bytes, and `3 · Σpower(signed) > 2 ·
Σpower(set)`, every precommit of one round as tm2's `Commit.ValidateBasic` requires — the chain's own rule, weighted by power, never a 1/3 skip. Set changes are followed in order through
NextValidatorsHash: the announcing header A+1 must be presented, so a gap of any length is caught up by replaying the
announcements, and there is no trusting period — gno.land has no unbonding, so old keys are dangerous forever and a
time limit would buy nothing; what makes a rotated-out set powerless is that only the current set counts for headers (§6.2) once
the client has followed the rotation — and until the client has followed it, the old set is the current set and can run the client
ahead of the real chain, with no quarantine, past the announcing header for good (§1's leak ending), which is why the watch of §5.9 compares every accepted header with
gno.land's own and flags a mismatch within two minutes. The client is never left behind: the operator's updater submits
a header every 10 minutes, whenever a dispatch waits, and the announcing header the moment a validator change
executes (it watches r/sys/validators' `ValidatorAdded` and `ValidatorRemoved` events, so the rotated-out set's
window is seconds), GovDAO members' replay tools may submit too, and anyone may;
a lag of one hour alarms the operator, the guardians and the members' channel. Quarantine: any change of
ValidatorsHash (a key or a power: Prop #12's add at height 720,333 and Prop #13's raise at 741,862 were two, and Prop #10 would be) sets the pausable ISM's quarantine, read by it from the
client's state account, for 7 days by the Clock sysvar's time (≈2.75M slots at today's 0.22-second slot) from the update that announced it;
overlapping changes extend it to the latest; no decision shortens it (a hold the guardians keep stands on the pausable ISM regardless); a hold — two guardians' flags — holds past it, on either object (the pausable ISM or the admin program), and a hold's start belongs to the object, not to any flag: the object records
`holdSince`, the Clock time its standing guardian flags reached two, and keeps it while they stay at two or more —
a dip below two shorter than 24 hours of Clock time keeps it too, so no clearing and re-setting, by two keys or by
three taking turns, ever moves a hold's start or the floor of §6.5 — and a dip of 24 hours ends
the hold, after which the next two flags begin a new one; the one clear that ends a hold at once is a replacement
decision's (§6.5) when it cleared a standing guardian flag and leaves fewer than two (a replacement of a watch slot
or an idle seat during a dip ends nothing), since GovDAO's replacement is what a hold waits for; a hold
"stands" for every rule — the deferral of the 7-day group, the spending of an unpause, the owner's clearing, the
floor — from `holdSince` until it ends, never by the live count, while deliveries follow the flags themselves
(any one refuses them); a flag carries one slot, the moment it was last set, fresh on every set, which only an
unpause reads (§6.5) — a pure function of those inputs and the
Clock, evaluated at every read; the hold history the floor is computed from is written by the flag instructions,
the unpauses and the replacements (§6.5). So a change gno.land's governance made can be seen and, if need be, held,
and the cost is a week's pause of gno → Solana per validator vote, which GovDAO can time. The trusted height only
rises; the deploy writes a trusted header less than an hour old — never one announcing a set change — named with its set in the launch notice. The client's count byte allows 255 validators, but the program's 32 KB heap binds as built: a next set of about 50 can be followed through any history, 60 with a history at rest, 100 for a single change (M2-NOTES.md has the measured limits) — a next set is staged in chunks over several transactions (23 validators a chunk, a commit's precommits in chunks too, so that every staging transaction stays under Solana's 1,232-byte packet — a Go test asserts it for a set of 60; the first build's 25-chunk and one-call precommits would have overflowed past 17 validators) and the state account grows to a named capacity (81,920 B at most, 10 KB a call, with 2,048 B of slack; a growth moves the ring only when the account holds a ring of the capacity it had, filling its free slots first, so no stranger's growth empties or shrinks it); a set the client cannot follow is a lapsed client and the end of the deployment (§6.4), so the heap frame that lifts the bound toward 255 is the next build item, and the updater, the monitors and `hlwatch` alarm `client:set-bound` when the current or an announced set reaches 40 validators — the trigger for that build, which lands before the set does; staging accounts are allocated per update at the
set's size, so a larger set costs rent, never a change. Updates are staged in a PDA keyed by (submitter, header
hash), rent returned on completion (an abandoned one may be closed by anyone after 7 days, the rent to the closer), so nobody can
pre-empt another's update. Every Solana delay in this plan is measured by the Clock sysvar's unix time — the validators' stake-weighted time, bounded by the runtime to between 0.75 and 2.5 of the slot count's time within an epoch and never decreasing, within seconds of the wall clock under honest validators (a stake majority that bends it already owns the chain); the slot
figures are only to give a sense of scale.

6.2 Message proofs from results: `process` takes a staged proof account holding a header of height ≤ the trusted
height signed by the *current* set (the set of the trusted header's height; a header from an earlier set is refused,
and its messages are delivered after a `Reemit`), its commit (verified against that set), a `SimpleProof` over
`LastResultsHash`, and the ABCIResult leaf bytes (at most 8 KB; a bigger leaf means a `Reemit`, whose leaf is always
small; a block of more than 128 results — eight aunts — makes a header proof's staging exceed Solana's packet, so such a block delivers by the ring or after one `Reemit`, and a chain that stays that busy needs a hand), and checks `Error` absent and an event with `pkg_path == hlmailbox` whose attributes carry id, nonce,
destination, recipient and the message hash. One of the last 256 accepted header hashes of the current set may stand
in for the header and commit (a cost shortcut, no safety role; the ring is cleared at a set change, each clearing counted by the ring epoch the body keeps, and a staged ring proof carries the entry's height and results hash under that epoch, so the ring's churn — a stranger's 257 old-header proofs — voids no staged proof, while a set change still does). The client
keeps one counter and one running hash in its state over every header it accepts as an update or verifies for a
proof (hash, height, the slot; a header verified for a proof is recorded once, the first time, and joins the ring), the record itself being the staging transactions' instruction data — never only a
program log, which Solana truncates at ≈10 KB per transaction and a deliverer could bury; the watch rebuilds the
chain from instruction data, flags a mismatch or an unfillable gap, and so sees a never-existed header proven at a
past height as surely as a forged update; the slot of a header's entry is the S of §6.4.

6.3 Packaging: ≈100–120k CU per 6-validator update over 4 transactions (measured under program-test on the built programs after the sixth audit round: 37k to stage four validators, 49k six, 62k forty; 14–27k per signature-check transaction of three signatures; 13–18k to finish); four or more signatures exceed one transaction, so updates and proofs are staged in PDAs
(four transactions per header at gno.land's six validators) or verified in-program through the curve25519 syscalls; precompile
introspection checks the program id, `num_signatures`, and that every offset points inside the precompile
instruction's own data, with the message length. A staged proof PDA belongs to its submitter, who alone closes it
after `process` and takes the rent back (an abandoned one may be closed by anyone after 7 days, the rent to the
closer, as an update's); a proof `process` is two or three transactions: the proof's verification 29k CU by the ring or 34–45k by header (the look-up over a full ring from the second day), the
pausable ISM's `Verify` into the client 45k, the delivery through the mailbox 94–126k, 228k for a results leaf carrying eight dispatches (measured; every figure the
maximum seen per transaction, and the admin program's decisions 18–46k for kinds 2, 3, 5–7 and 12, 159k for an upgrade
plus 2.5k for its one growth extension, 162–165k for a transfer to a successor, its sweep of the authority float included (its spend for a successor that cannot inherit 20–25k), 46k for `finish_setup`). The published tool replays updates and proofs on a laptop, submits updates,
and drives §5.9's devnet drills; `hlwatch` (§5.8) is the guardians' eyes on it.

6.4 No recovery, one rule: every change on Solana is a light-client proof of a GovDAO decision, and there is nothing
else. The admin program has one way in, the mailbox's `Handle` under the light-client ISM (§6.5); no validator's
signature is a decision, no stage holds one, no counter numbers one, no window opens for one, and nothing records a
settled set — gno.land's validators validate, which is to say they sign blocks, and their signatures are what the
client checks on every header and every proof (Jae, 2026-10-13: "validators should do nothing than validate"). The
light client has no reset: its controls are its upgrade authority and `SetAdmin`, both the admin PDA's and both
moved by kind 9 (§5.8), and the chain id and origin it accepts are the deployment's for life. What follows is the
ending, said plainly: if the client can no longer verify gno.land — the chain halted for good, or restarted under a
new chain id or a set its last header never announced; a set change it cannot follow, past the heap's bound of
§6.1 or carrying a key of a kind it refuses (§13); a fork it took the wrong side of; a bug in its own verification
— then gno → Solana stops for good, since every send and every decision needs a proof, the client's own upgrade
and the kind-9 hand-over included: a hand-over is no exit for a lapsed client, because it needs the client too.
Burns back to gno.land keep working, since that path rests on the signers (§5.6), not on the client, so holders
leave at the meter's rate as far as the lock reaches, and what the lock cannot cover is a credit by vote (§5.3); the sends locked after the client's last proven header are cured by a migrate to a successor realm, whose refund of the imported sends follows the fresh deployment's kind 6 enrolling it (§5.3) — the launch notice names the steps; a
flag its own key does not clear then stands for good, since no unpause comes. GovDAO's exit is a fresh deployment:
new programs and new synthetic tokens, the old ones redeemable only by burning back; the realms stay, pointed at
the new deployment by the routes' `set router` and r/hlgov's `set decision recipient` (7 days each; the new admin program is initialised at r/hlgov's current counter — `Init` takes the starting number, `hlsol setup -start-sequence` reads it from the realm and refuses a mismatch, the audit fails on a gap — so r/hlgov's next decision is the first it records, and nothing is dispatched to the old recipient between the deploy and the switch; the old
router's burns stay accepted for the 7 days after the swap lands and are credited by id after, §5.3), or are
replaced by successor realms through `migrate`. It costs the six deployments' rent again (≈8.5 SOL, §5.8), the
pool's seed again (§11.11) and the launch procedure again — the deploy, the setup step, the audit and the switch above by decision, the governor's call before the yield and GovDAO's vote after, since the realms stay and hlmailbox's yield with them (§6.6) — and the launch notice says so. A leak of the validator set's keys is the other ending, §1's: nothing
on Solana tells a forged header from a real one, the brakes are the 7-day delay, the guardians' flags, the hold's
21-day floor and GovDAO's cancels, and the sane end is a rotation on gno.land and the same fresh deployment. The
procedure for a fork, written into the launch notice: the cut is the Solana slot S at which the client verified
the first header that differs from gno.land's (its header record, §6.2 — never the forged header's own gno height,
which the forger chooses), posted in the members' channel with the two headers by the operator, or by any
outsider, whose `hlwatch` computes it too; every delivery verified at or after S is forged for the supply
identity; claims the signers credited before S stand; burns after S are credited by vote, pro rata against what
the lock holds (§1); a fork ends the deployment. The drills: on devnet, before `Launch`, the hand-over and
the fork (§5.9); the route migration's drill is §5.9's too.

6.5 The admin program: immutable (its own upgrade authority burned in the setup step, before `Launch`,
§5.8), a mailbox recipient whose ISM is the light-client ISM, fixed at deploy; it accepts only messages whose origin
is gno.land and whose sender is r/hlgov, and whose `Handle` is signed by our mailbox's process authority for the
admin program (the per-recipient PDA under our mailbox's id — constants, printed by the audit; r/hlgov's first decision recipient is this
program's id, given at its deploy); its guardian list and watch slots are given at its deploy as init data, the pausable ISM's by its own init
from the same list, so §5.9 and §8.3 check the three lists (pausable ISM, admin program, hlmailbox's compiled
defaults) equal before `Launch` and again at the yield, the outsiders then seated; every setup-only instruction is one-shot on chain (§5.8). Lifecycle: a message shorter than 9 bytes fails `Handle` (the mailbox leaves it deliverable; the relayer parks it); one whose parameters exceed 10,135 bytes — what a decision account holds — is spent at recording, and r/hlgov refuses a body above 512 bytes at creation and at dispatch, since a legacy delivery carries at most 538 (14 account keys and 127 bytes of fixed data before the packet's 1,232) and the mainnet mailbox's own heap dies near 2,250 — an undeliverable body would stick the sequence with its number untaken; a decision whose 9-byte prefix (kind, sequence number) parses and whose number is last
+ 1 is *recorded*, whatever its parameters, whatever its kind byte (an unknown kind is recorded and spent) and
whatever flags stand; one with a number ≤ last is accepted and ignored (a GovDAO decision whose number a foreign one took is lost that
way: r/hlgov's `sync sequence` moves its counter to last + 1 and the proposal is re-made, §5.3); one with a number
> last + 1 is refused and stays deliverable. A decision has no expiry on Solana: the sequence, its clock and `cancel` govern it, and GovDAO's
14-day deadline is on the proposal (§5.3); since anyone may re-dispatch, a decision the relayer missed is re-sent
under the same number. Parameters are validated at execution by the admin program's own checks, and a decision that
fails them — or whose parameters never parsed — is spent (GovDAO proposes again), so one bad decision never blocks
the sequence; a CPI that fails aborts the transaction and leaves the decision pending for a retry, and a `set
guardians` executes through any lone flag, and through a hold on the hold's 21st day at the earliest (below); r/hlgov's builders apply the same checks up
front. Timing by kind: `cancel` (a list of pending decisions by number, or a range of numbers, §13) executes at once, in the transaction
that records it — the one answer to a hostile decision, since nothing but GovDAO's own proven message cancels anything (§6.4);
`unpause` (the pausable ISM's flags, or the admin program's own) executes 24 hours after delivery, clears only flags
whose last set slot (§6.1) is before its delivery, and is spent without effect if a hold stands on that object at that moment (§6.1's `holdSince`, dip or no dip);
every other kind — set a token's ISM pointer (to the pausable ISM or none), kind 4 (reserved, refused: the client's state layout is fixed for the deployment's life, §13), set
guardians and watch keys (two guardians at most, plus both watch slots — at most four entries — never a seat's current key; two such decisions may pend together), enroll router, set the mailbox's
protocol fee and beneficiary (≤ its 0.05 SOL maximum), upgrade program P from buffer B with hash H, transfer every
authority to a successor (refused unless the successor is immutable, and spent unless its state names this program as predecessor, not yet inherited and with no successor of its own — the fields `hlwatch` reads before it says "immutable", with the successor's pending decisions, which must be none, and its state's layout, which must be the hand-over's: an un-inherited successor records, stages and executes nothing until `successor_init`, so no decision can pend there before the hand-over; a decision never reads the client's state, so a client at another layout version bricks no corrective kind 8 or kind 9; a kind 9 is spent unless the successor's compiled clock factor equals this build's; the hand-over sweeps the predecessor's authority lamports to the successor's, so its first enrolment waits on no hand; a successor keeps the admin state's layout append-only (a field appended after `inherited` is allowed and `hlwatch` notes it; one inserted before it is LAYOUT DIFFERS) and the hand-over instruction byte-identical, and §5.9's hand-over drill runs with the real successor build, §13), kinds 10 and 11 (reserved since v35 — the
recovery's `set sequence` and `reset client` — recorded and spent at recording, never pending (kind 4 pends with a deliverer's rent and is spent at execution), §13), set the mint limits (kind 12: the pausable ISM's per-message maximum and
daily bucket per token) — matures 7 days after delivery, and anyone executes it then: the operator's relayers by default, any member by `hlrecover execute` (a growing upgrade's one extension included), so no decision ever waits on the operator's hand. There is no
guardians' veto: no guardian can void, object to or stretch a decision; a decision ends only by executing, by GovDAO's
`cancel`, or by failing its own checks (spent). Each pending decision is its own account, rent paid by the
deliverer and returned to it when the decision executes, is cancelled or is spent, never a bounded list (a cancel's delivery returns the rent of the pairs its packet carried; the rest, stored cancelled, by anyone's `close cancelled`, which the executor sends). The seats
are numbered at deploy, 3 and 4 the outsider seats (a numbering for the audit and the launch notice; no rule reads it); no key may hold two seats or a seat and a watch slot.
Replacing a guardian or watch key clears that key's flags. The admin program's own flags (set by guardians and
watch keys; "two guardians' flags" always means two on the same object): a lone flag defers execution of the
7-day group except the `set guardians and watch keys` kind, whichever keys it names (so a stolen key's own flag
never defers its replacement); a hold defers the whole group, that kind included — a replacement of guardians lands
through a hold only on the later of its own 7th day and the hold's 21st day (21 × 24 hours after the start of the
earliest hold that has stood since the decision's delivery — the admin program keeps the last eight holds' start and
end, written by the flag instructions and by replacements (a ninth merges the two oldest, their earliest start kept), and computes the floor from that history at `Execute`, so
no hand need call anything under the hold and two keys re-flagging at the grace's last second to begin a new hold
raise nothing; the floor binds only while a hold stands, a decision with none standing landing on its own 7th day, lifted or not; so a replacement delivered within the hold's first 14 days lands on the hold's 21st day, 7 to 21 days after its
delivery; one delivered up to 7 days before the hold began, 21 to 28 days after; one delivered after the hold's
14th day, on its own 7th day), and then clears the replaced keys' flags, ending the hold at once
if fewer than two remain (§6.1). So a hold lasts until GovDAO
replaces the holders, on the hold's 21st day at the earliest, and never past GovDAO's will; the 21 days are there so that GovDAO — a proposal, two votes, an execution, a dispatch and a delivery under the honest chain's headers, re-made as often as a forger takes its number (§5.3) — has time to cancel a replacement it never made — forged under a leaked set, or let
through by a bug — before it lands (§11.15).
`unpause` and `cancel` are never deferred. For an upgrade it hashes the
named buffer at execution (sha256 syscall) against the decision's ELF hash, first extends the program data by the
buffer's excess over it (the loader's `ExtendProgram`, permissionless and unsigned by any authority, sent top-level by the
executor — the loader admits no extend of any form by CPI, and the checked form never shipped on mainnet, facts the
first audit round verified against agave v4.3.0 — in one extension a block of at least 10 KB — SIMD-0431's minimum, active on mainnet since 2026-07-14; the loader bounds a single extension only by the 10 MiB maximum, so the executor sends the whole excess at once and a growth takes one block before the upgrade, `Execute` answering `NeedsExtend` meanwhile — the
executor paying the added rent — 5,080 lamports a byte at mainnet's rate, read live, ≈0.05 SOL per 10 KB — alarmed with the whole cost before the extension is sent, a number `hlwatch` shows beside the buffer's and the program data's sizes before the decision, `hlrecover execute` refusing a growth above half a SOL without the member's say-so, and parked after three refusals like any call) so a fix may grow, names the buffer's refund key — the `stage upgrade` signer's — as the loader's spill,
and closes the buffer to that refund key when the decision is spent or cancelled; the hash is over the
buffer's ELF bytes after the loader's buffer header; the buffer's authority is handed to the admin PDA through the admin program's `stage upgrade` instruction
once the decision is recorded (the writer signs it; it CPIs `SetBufferAuthority` and records the signer as the
buffer's refund key; a decision whose buffer is not yet staged stays pending for a retry), so the refund key is
the writer's by construction and the decision carries none; a successor receives the sequence number (the highest number recorded, pending or inside a cancelled range), the accepted origin, the seats, the watch slots and the hold history at the transfer, written by kind 9's execution through a `successor_init`
only the predecessor's authority PDA may sign (its own `init` carries none of them, so they are never stale), with
no flags, no hold and no pending decisions (the transfer is itself the cure a hold waited for), and receives decisions only once r/hlgov's `set decision
recipient` (§5.3) names it — GovDAO proposes that switch only once `hlwatch` shows the kind 9 executed — proposed together, a switch landing while the transfer sits deferred under a hold would send GovDAO's own cancels and replacements to a successor that refuses them (`NotInherited`) — and `hlwatch` alarms a pending or queued switch whose kind 9 is unexecuted; from the block kind 9 executes the tools — the monitors, `hlwatch`, the relayers — read the admin program's `successor` from the chain and follow it four hops deep (decisions, the foreign check, the pending list, the one-stroke flag on both states while the old still stands; a fifth successor is alarmed within two minutes (`decision:line-unfollowed`) and returns the tools to a config edit, which the launch notice names as the operator's chore), so no operator's or outsider's config edit stands between a leak's cure and its watch; once kind 9 has executed the predecessor records nothing more (`Retired`), and a message that reached it is re-dispatched to the successor after the switch, never parked for retry; what pends on the predecessor at the transfer never executes and anyone closes it after (the rent to its deliverer, a staged buffer to its refund key), and nothing is staged or recorded there again (`hlrecover` refuses a program whose state names a successor, and has no close path for any program); the recording and the execution of every kind, kind 9 included, are one path, so a bug in either is a new deployment (§1) and kind 9 cures only a bug in how one kind is carried out.
r/hlgov's builder warns of a kind 5 that names a seat's current key and the admin program spends it (§5.3), since a no-op
"replacement" would clear that key's flags. One
`set guardians` decision updates the pausable ISM's list by CPI, so the two Solana lists never differ, and GovDAO's
next gno.land proposal brings hlmailbox's list to the same four.

6.6 The deploy, `Launch` and the yield vote: the Solana programs are deployed and every authority moved to the admin program
(§5.8) before `Launch`, so the audit before it — Jae's, and the members' again at the yield — is of the final, immutable deployment — the realms
are deployed before `Launch` too, with every launch value and the governor compiled in (§5.3), and `Launch` is the governor's own call of
hlmailbox's one-shot builder, so its execution is the launch and nothing else changes
between the deploy and the launch; the deployer holds no call unless its key is the governor's. What v1–v35 called the launch vote is two things now. First the deployment's checklist, Jae's own gates for his `Launch`, published with the mainnet deployment (gno.land's forum and bubblerumble.net) before he calls it: the program ids and reproducible build hashes, the realms' package paths and
the hash of each file at them, the governor's key, `hlsol audit`'s output after the setup step, `hlwatch` green, the two-week devnet log and the drills, the guardian and watch keys by key (the operator's own until the outsiders are seated, §11.9), the admin program's constants and init lists, the signers (and the partner, if §11.13 is taken), the treasurer, the fees and the caps, the lock the testing phase will hold, and the statement that the launch rests on §12's rounds and the audit loop (and on §11.2's review or audit if either came first). Its gates are §5.9's acceptance list; its checks are §8's, made by Jae. Then the yield vote (GovDAO's proposal 1, "the yield"): GovDAO's acceptance of the bridge as it stands at the end of the testing phase — the same list, read by the members against the live deployment, with what the phase showed (the lock's size, every decision the governor made, read on the realms' pages, every alarm and flag), the two outsiders and their backups by name and
key, seated by the governor's own rotation 7 days before (§11.9), every validator's operator
by name, §11.2's review or audit (or the statement that the yield rests on §12's rounds and the audit loop alone), and the launch notice, published with the vote — and the yield itself: Jae yields on its acceptance by his own `Yield` (§5.3), since GovDAO can accept the bridge, never take it; the vote is a proposal in words whose executor touches no realm (every builder refuses GovDAO before the yield), its description naming the deployment it accepts — the program ids, the realms' file hashes, the yield's conditions — so the members read the deployment, not the text. Its gates are §5.9's list again; its checks are §8's, made by the members. After the yield the plan is as it says everywhere else. If the vote fails, nothing changes: the governor stays, the testing phase goes on or he winds it down (holders leave at the meter's rate, undelivered sends are refunded by migrate, §5.3), and no real holder was invited (§1); a `Launch` never called leaves the deployment idle
by rule: the realms never go live and the admin program's first decision never comes (≈8.5 SOL of rent is the cost).

## 7. Proposal 2 — Solana → gno.land on proofs (after Alpenglow mainnet)

7.1 What must be proven for a burn: the certificate chain (fast: `FinalizeFast(block)` at 80%; slow:
`Notarize(block)` plus `Finalize(slot)` at 60% each — two aggregates, two bitmaps) over a block id; the block id's
double-merkle path to the footer; the footer's bank hash; and, since no single account can be proven, a
lattice-delta proof — in one line, the proof re-adds every account the block changed and shows the sum matches the
block's own hash: both bank-hash preimages (N and N−1), every account block N modified with its pre and post state
(pre = identity for a creation), one entry per pubkey, and the check `lt(N) − lt(N−1) = Σ[h(post) − h(pre)]`. The
mailbox's dispatched-message account must have owner == our mailbox program id and decode with sender == the token
program (bound on chain: the mailbox requires the sender's dispatch-authority PDA signature and the token burns in the
same instruction). Inclusion of a transaction is never accepted. Fork rule: the realm tracks one chain by the
parent-link leaf; two certified block ids at one slot is proof of a safety failure ⇒ the verifier realm refuses
every proof past that slot and the watch keys set `credits` and `payouts` until GovDAO's unpause names the chain
it follows. If lazy execution
lands, block N's state is anchored by the certificate of N+1. Implementer facts: §13.

7.2 How: a ZK program (SP1 or RISC Zero) does all of 7.1 and is wrapped to Groth16 over bn254, verified by a realm
with the stdlib's PairingCheck (cometblszk is the pattern; ≈1.8M gas plus VM overhead). The prover is the operator's
node that captures pre- and post-images of every account written per slot, plus shreds and footers. Cost: the
certificate ≈100–200M cycles (1–3 min on one GPU, ≈$0.1–0.2 on a prover network); the lattice-delta part 10–30× that
(10–90 min, ≈$1–6 per block), and anyone — not only a block producer — can inflate it: a one-lamport transfer to
a large program-data account forces its full re-hash, so ≈8–16 SOL a day of such dust (fifty 5–10 MB program-data accounts a
slot — at least 29 such accounts of 5–10 MB stood on mainnet on 2026-10-13, counted at eleven round sizes — in four to eight transactions a slot under the 64 MiB
account-data limit, ≈5,000 lamports each transaction) puts hundreds
of megabytes of blake3 into every block and makes every burn's block "over the budget" — and a block producer, or
anyone who sees a burn coming, does it for under a cent to that one block. The
honest statement, until measured: the trustless path holds for un-griefed blocks; a griefed block (and griefing
only the ~50 blocks a day that carry burns costs cents) falls back to a credit by vote on the signers' and a
member's reading of the chain — the signers' trust plus a 7-day proposal per batch, the proving fee charged all the
same since it funds the budget — and before Proposal 2 the operator measures the griefed cost and either designs around it (per-account data
commitments carried across proofs, which means proving every block, or a zkVM blake3 precompile budget) or states
the degradation as the rule and Proposal 2 says so. Proposal 2 adds a
Solana → gno proving fee (set by GovDAO in that proposal, in GNOT, deducted from the claim) and proves under
a per-block budget with a queue (working figures, to be set at Proposal 2: a 50 GNOT fee, ≈$3.5, since burns at 50 a
day rarely share a block and a block costs $1–6 to prove; $20 per block, and a day's proving budget equal to the day's
proving fees with a $100 floor — ≈$170 at 50 burns a day against $50–300 of cost, so at the dear end a block over the
budget waits for a later day's, up to a week, then is credited by vote — if the measured cost sits at the dear end
Proposal 2 sets the fee to cover it, ≈90 GNOT at $6 a block, rather than make GovDAO a routine crediting oracle;
the proving fee goes to r/hlfees; and since a burn under the two fees — ≈55 GNOT, ≈95 at the dear end — pays out
nothing, the path is for transfers of 500 GNOT and up (the client's warning threshold), the documentation and the client say so, and small
holders are told to pool through a larger one — Hyperlane's token program has no minimum and §5.9 keeps it
byte-identical);
a burn in a block that never fits the budget is not lost: GovDAO raises the budget by vote, or credits the claim by
hand (§5.3's credit builder, 7 days) on the signers' and a member's reading of the chain. Whether the zkVM has a
blake3 precompile is settled before the design is final. The native BLS12-381 path is dropped.

7.3 The roster, honestly: epoch E's stakes are fixed at the start of E−1, so the roster changes about daily (an epoch is ≈26 hours today) and
cannot wait for a 7-day vote. Until SIMD-0511 lands, the roster is submitted per epoch by the signers (2 of 3),
checked by a comparator — a published tool, pinned by hash and in §11.2's review scope like `hlwatch`, that an
outside guardian runs against a provider of their own; the operator's monitors run the same comparator and set the
gno routes' `credits` and `payouts` with the watch keys on any difference in the table, and the outsider's copy
alarms its holder, who flags by hand (a guardian's key is never an automatic pauser; the only GovDAO member with a
guardian key is Jae, who runs the signers, so the independent comparator is the outsiders') — and bounded by a certificate age of two epochs (≈2.2 days at today's 26-hour epoch). So before SIMD-0511 the
signers vouch per epoch instead of per transfer: a real narrowing of what they can do (a false roster is a false
validator set, caught by the comparator within an epoch), not their retirement. When SIMD-0511 lands, the table is
proven from the previous epoch's certified boundary block by the same lattice-delta method and the signers' last role
ends.

7.4 GovDAO proposal 2 ("Solana → gno to the verifier"): the verifier realm's pkgpath, the prover's code hash, the
cryptographic audit of the lattice-delta proof and the code audit, a month of shadow running (every signer-passed
message also proven, zero disagreements, prover lag under an hour), the roster rule above stated as what the signers
still do, the proving fee and the per-block budget, caps and the delay kept (the 20% bound). It follows the launch:
earliest Q2 2027 if Alpenglow activates in late November and nothing slips; realistically Q3 2027.

7.5 GovDAO proposal 3 ("the signers go"): once SIMD-0511 is live on mainnet for a month, the roster comes from the
proof, the signer set is rotated to empty on gno.land and the comparator retires. No date.

## 8. What a GovDAO member checks before approving a proposal

The launch, then the yield vote — the same checks twice: before `Launch` Jae makes them and publishes them with the deployment's checklist (§6.6), and at the yield vote each member makes them again against the bridge as it stands, after the testing phase, and reads what the phase did:
1. The light client's trusted height is within minutes of gnoland-1 now and its accepted header hashes equal the
   chain's; the member's own replay with the published tool matched every update for two weeks, weights by voting
   power, followed gnoland-1's four set changes (§5.9) on devnet, and the 7-day quarantine fired on each; the member's tool can submit
   updates itself and receives the lag alarm; the two outside guardians have run `hlwatch`, compiled by themselves to
   the published hash, through two weeks — on devnet before `Launch`, by the operator's stand-ins, and on mainnet before the yield, by themselves, seated (§11.9) — its supply identity included; every validator's consensus address
   is resolved to a named operator (r/gnops/valopers) and the operator's share of the power is stated as a number (60 of 360 today), Jae's own
   validator included.
2. `solana program dump` of each program hashes to the proposal's reproducible build; our stock programs are
   byte-identical to Hyperlane's mainnet ids.
3. `hlsol audit` shows every upgrade authority = the admin program and the admin program's own = none, every other authority = the admin program or none, and every setting of §5.8's list as the proposal
   states; the admin program's ISM is the light-client ISM, its accepted origin and sender are gno.land and r/hlgov,
   r/hlgov's decision recipient is its program id, its sequence number matches r/hlgov's counter (zero before the governor's first decision, the count of his at the yield); no decision is pending that the realms' pages do not show as the governor's, and before `Launch` none at all; the three guardian lists (pausable ISM, admin program,
   hlmailbox) are, at the yield, the four people of §11.9 with the keys they published, two of them outside the operator, GovDAO
   and the validator set (before `Launch` the operator's own, which the checklist says); the fee beneficiary is the treasurer's key; each token's gas paymaster, destination gas configs and fee config
   are none, its mailbox pointer ours, its decimals 6; the mailbox's local domain is ours and its maximum protocol fee 0.05 SOL; no human key holds an
   authority; the member repeats the audit's printed RPC calls by hand. On gno.land: the source at each of the five
   realms' package paths, served by the chain, hashes to the vote's figures; the compiled launch values in that
   source (§5.3's list, the 7-day delay constant included) match the checklist, the governor's address among them — Jae's key, the cold one if real funds are locked (§11.16) — and hlmailbox renders no yield yet; the enrolled routers printed by the audit equal the route realms'
   addresses and the admin program's accepted sender equals r/hlgov's path; the realms are not live before `Launch` and live at the yield (one `Launch`, the governor's), none of their served gnomod.toml files sets `private` and every package they import is public (the same two checks on any verifier or successor realm GovDAO later activates, and that the active verifier's type is declared in its own realm, §5.1), and each
   mint's mint authority is its mint PDA with no freeze authority; the client's state PDA is one, with the fixed
   seed.
4. The admin program can act only on a decision message from r/hlgov delivered under the light-client ISM; r/hlgov's
   mutators are unexported and reachable only through its builders; the decision's number and parameters appear in
   r/hlgov's own render; every decision carries a sequence number, can be re-dispatched and can be cancelled; the
   three timing groups of §6.5, the flags' deferral, the replacement that lands on a hold's 21st day and GovDAO's cancel of a decision deferred under the hold are as stated and
   were drilled; `hlwatch` showed "immutable" beside a kind 8's buffer and a kind 9's successor; §6.4's rule holds — the admin program's only way in is the mailbox's `Handle` and the client has no reset, which the member reads in both programs' instruction tables in the reviewed source — and the hand-over and the fork were drilled on devnet (§5.9); a growing upgrade (extend, then upgrade), a cancelled upgrade's buffer return and a kind 9 refused for a mutable
   successor were drilled (`hlwatch` says "immutable" only of a successor whose state names the admin program as predecessor, not yet inherited, with no successor of its own); the header ring (256), the quarantine (7 days) and the replacement floor (the hold's 21st day) are stated, and the dump's clock factor is 1.
5. §11.2's review or audit, if either was chosen, is public with critical and high findings closed (on §12's rounds and the audit loop
   alone, the checklist and the proposal say so); the member reproduced one gno → Solana
   transfer on devnet against a header that was no longer the latest, and one after a `Reemit`.
6. The governor could, and GovDAO can from the yield, void and restore claims by message id, and the member saw both on devnet (§5.9) — and, at the yield, a GovDAO proposal refused before it and `Govern` refused after it.
7. The proposal lists what no member can check — the operator's three providers, its backups, its monitors, the outsiders' own nodes and providers — as
   claims, not facts.
The yield vote states: "GovDAO has N members; this vote is K people; the governor was 1, Jae, and yields on this vote by his own call", and lists §1's trusted parties; the checklist states the same of the testing phase — one governor, the operator's guardians, the lock it will hold.

Proposal 2:
1. Alpenglow has run on Solana mainnet for a month with no consensus incident.
2. The shadow month: every signer-passed message also proven, zero disagreements, prover lag under an hour, and the
   roster comparator, run by an outside guardian on a build they compiled to the published hash, never fired.
3. Three past burns' proofs verify on a member's machine with the published tool, including the owner and sender
   bindings against the mailbox and token program ids.
4. The roster rule is stated (the signers vouch per epoch until SIMD-0511) and the member accepts it.
5. The proving fee, the per-block budget and the unprovable-block fallback are stated.

Proposal 3:
1. SIMD-0511 is live on mainnet for a month; the member's tool proved one epoch's table from the boundary block.
2. The signer set on gno.land is rotated to empty in the same proposal; `hlsol audit` and the realm render show it.

## 9. Risks, each with its answer

| Risk | Answer |
|---|---|
| The governor's key, until the yield | One person decides, within the delays: a 7-day decision of his waits 7 days and anyone executes it, two guardians' flags — the operator's own, during the testing phase — hold it on Solana, the caps bound what any decision moves, and the yield is one-way and at once. A leaked key is a leaked lock unless he sees it inside the thief's first decision's 7 days: his cancel answers each decision at once, the thief re-makes it, and the race ends only with the yield, which the thief cannot prevent and which makes the queued decisions GovDAO's to cancel (§1, §5.3); so the lock stays small until the yield, the cold key governs any deployment holding real funds and the key is fixed by the deploy (§11.16), and no real holder is invited before the yield. After the yield the key is nothing. |
| Two GovDAO keys replace the validator set | The 7-day quarantine pauses gno → Solana after any set change; two guardians hold it until GovDAO replaces them (the hold's 21st day at the earliest, §6.5); any decision that would re-point Solana's trust waits 7 days, and under a hold until that replacement lands — a delay, never a guardians' veto, and nothing after it (§6.4); said at the top of §1. The structural fix is in gno.land (§11.1). |
| Alpenglow slips, or SIMD-0511 never lands | Proposal 2 waits, or runs with the signers vouching per epoch; no date by which they must go; the caps are the bound meanwhile. |
| The lattice-delta proof is novel and heavy; anyone can bloat it for ≈8–16 SOL a day, a block producer for under a cent | Audited as cryptography; the proving fee; a per-block budget and queue; a griefed block is credited by vote — the signers' and a member's reading plus 7 days, never a loss — and §7.2 says the trustless claim holds only for un-griefed blocks until the griefed cost is measured and designed around before Proposal 2. |
| The light client refuses genuine headers (an encoding change, a bug) | The ending of §6.4: no decision reaches the admin program past a client that cannot verify gno.land — the upgrade that would fix it included — so gno → Solana stops for good, burns keep working through the signers, and GovDAO deploys again; the two-week devnet follow, the fuzzing against real blocks and §11.2's review are what make it improbable. |
| The light client accepts a forged header (the worse bug) | The watch flags a header that differs from gno.land's within two minutes; two guardians hold; a forged `unpause` clears nothing set after it and nothing under a hold; a forged replacement of the guardians cannot land before the hold's 21st day (§6.5), in which GovDAO cancels it under the real chain's headers; a client that accepts what it should not is left only by a fresh deployment (§6.4); the review, the audit and the fuzzing exist to make this improbable. |
| gno.land halts, restarts under a new chain id or set, or forks with the same chain id | §6.4 — a halt the chain resumes from under its last set the client simply follows (there is no trusting period, §6.1); a restart under a new chain id or a set the last header never announced, or a fork the client took the wrong side of, ends gno → Solana for good: burns keep working, the exit is a fresh deployment, and the realms are pointed at it by `set router` and `set decision recipient` or move by `migrate`; the one-hour lag alarm. |
| The current validator set's keys leak (no slashing) | A race, said in §1: the forger runs the client ahead of the real chain, no quarantine fires, and nothing on Solana tells its headers from real ones; the watch flags the fork within two minutes, 2 guardians hold, and GovDAO cancels each forged decision inside its 7 days (a range in one cancel) while the forger takes what sequence numbers it can first (§6.5); without a hold the side is the thief's on day 7, the forged kind 12, 8 and 9 landing with the kind 5 (§1); exits stay open as far as the lock reaches, and the cure is a rotation on gno.land and a new deployment. A set already rotated out is powerless once the client has followed the rotation (§6.1). |
| An updater races proofs by submitting every header | Proofs carry their own header and commit, verified against the current set; the 256 ring is only a shortcut. |
| A decision is delivered while a flag stands | It is recorded all the same; a lone flag defers its execution until an unpause, a hold until the hold ends; `unpause` and `cancel` run regardless, and `set guardians` through a lone flag at 7 days and through a hold on its 21st day at the earliest, so nothing wedges — a hold is what stops an `unpause`, until GovDAO replaces the holders. |
| 20% Byzantine stake plus a partition certifies two blocks | The verifier realm refuses every proof past that slot and the watch keys set `credits` and `payouts` until GovDAO's unpause names the chain it follows (§7.1); caps and the delay stay after Proposal 2. |
| GovDAO capture (two keys) or death | From the yield (until it, one key is what two are after it — the first row); named in every proposal; the delays bind everyone but those two; two guardians' flags delay them on Solana for 21 to 28 days, and nothing overrides them (§6.4); on gno.land a pair cannot empty the signers, void claims no guardian's flag names, restore a forged claim or reset
the meters by unpause at once (§5.3), so holders have the week unless the pair includes the operator (§1); a dead GovDAO leaves the bridge in its last configuration, pausable, with the lock intact; on Solana nothing moves for it either, and a flag set then is permanent (§6.4). |
| A forged claim from compromised signers | They mint nothing on Solana; on gno.land the pause stops crediting, GovDAO voids at once the ids a guardian names in a `payouts` flag set from the watch's alarm (a watch key's own flag never unlocks a void) and the rest in 7 days, and the meters bound what was paid before detection (one cap from an empty meter, two caps — 20,000, or a fifth of the lock — in the worst 24 hours otherwise); the fee moves nothing until a claim is paid. |
| A guardian or watch key captured | On Solana a freeze of gno → Solana and a deferral of governance until GovDAO's replacement decision lands, 7 days after delivery, for a guardian key or a watch key alike (the thief can re-flag until then and not after); on gno.land one proposal; funds never move. Two guardian keys captured = a hold, until the same replacement lands, on the hold's 21st day at the earliest (§1) — three captured keys cannot stretch it, since the hold's start is the object's, not the flags' (§6.1); a thief can clear its seat's flag as well as set it, so the honest seats hold without it. |
| Guardians colluding or refusing | Two can hold the Solana side (paused, exits open as far as the lock reaches) and every 7-day decision until GovDAO replaces them, on the hold's 21st day at the earliest — and nothing more; GovDAO overrides them by replacement, and nobody overrides GovDAO (§3.3, §6.4, §11.15); three colluding keys cannot wedge the side, since rotating their flags never moves the hold's start (§6.1). |
| An outside guardian asleep, captured or dead | A seat that never flags costs nothing; a captured one is replaced in 7 days; a dead one's backup holds the key (§11.9); `hlwatch` alarms them directly. The two outsiders' flags are 21 to 28 days' delay against a GovDAO pair that includes Jae — nothing beyond a decision's own 7 days (8 with the lift's day) if the operator's seats were already holding — never a veto of their own (§1); one captured or coerced outsider leaves no hold against that pair (§11.9's fifth seat). |
| The operator stops signing | Until Proposal 2 the Solana → gno exit is its signers (and the partner's, if §11.13 is taken): a stop by either closes it for 7 days plus a rotation (GovDAO swaps hlmailbox's signers, new machines); gno → Solana is unaffected, since no signer is on that path; a GovDAO pair that includes the operator can keep the exit closed (§1); Proposal 2 ends the dependence for un-griefed blocks (a griefed block's burn is GovDAO's vote at every stage, §7.2). |
| The operator stops proving (after Proposal 2) | Every Solana → gno exit is a credit by vote, 7 days a batch, until another prover runs (§7.2); no funds are lost; §1's operator bullet says so. |
| A forged decision through a forged proof header | Needs the current set's keys (a capture, §1); the client records every proof header and the watch compares each with gno.land's, and flags any decision absent from r/hlgov's list, within two minutes; two guardians hold it inside its 7 days; GovDAO cancels it under the real chain's headers inside the hold's 21 days, before a forged replacement of the holders could land (§6.5), re-making the cancel for every number the forger takes (§1). |
| The deployer key, before the setup step | It could deploy other code than the audited build; the dump (§8.2) and the audit (§8.3) — Jae's before `Launch`, the members' at the yield — are of the deployment after the setup step, when the deployer holds nothing but the floats' funding; on gno.land it holds no call at all unless it is the governor's key, which the checklist says — the launch is the governor's `Launch`, and a `private` realm (re-uploadable by its deployer) is refused by §5.3 and checked by §8.3. |
| Three Solana RPC providers lie at once | The signers' Solana picture is theirs (§5.6); the refusal rule and the supply monitor catch the result; Proposal 2 ends the dependence for un-griefed blocks. |
| The day bucket filled by real transfers | Four sends at the maximum fill a day's bucket for 20 GNOT of fees, but the cost is capital: 100,000 GNOT locked on gno.land per day of delay (≈$6,900), returned only through the exit at the meter's rate, so a week of it ties up a lock's worth; honest sends above the room park and retry oldest first as the bucket drains, the oldest park reserving its room against younger sends; GovDAO raises the bucket (kind 12), the fee, or both. |
| The return path griefed | k addresses slow an honest recipient to min(1/4, 1/(k+1)) of the cap — its own meter binds below k = 3 — paying one fee per burn and holding each turn amount ÷ (cap/4) days; GovDAO raises the fee or the floor. |
| The operator's floats run dry | Runway alarms at 7 days; parked messages retried hourly; nothing strands while the signers sign. |
| A Solana RPC provider fails | One bad provider halts up to two signers (four providers over six slots means two are shared), and then 2-of-3 is lost — liveness, never a forgery; the operator swaps the provider within a day; the rule for the provider pairs is in §5.6 (the providers themselves are the operator's claims, §8.7). |
| The admin program has a bug | It is immutable: a bug in its core freezes Solana's settings and makes an uncleared flag permanent, and the cure is a new deployment with the exits open, as for a leak (§1); a bug in how it carries out one kind is cured by a kind 9 hand-over to a successor built without it, if the recording and kind 9 are sound; a bug that fails open (a missing authority or sender check) is an unauthorized token upgrade and a mint, braked by two guardians' hold on the foreign decision `hlwatch` shows and GovDAO's cancel of it, then a hand-over — the reason §11.2's audit comes first for this program. |
| BUBBLE's price collapses | A 200-BUBBLE send to a fresh wallet then costs the attacker cents and the operator ≈$0.27 of rent, and the raise of the fee and minimum takes 7 days; meanwhile a guardian sets `sends` on the BUBBLE route by judgment, or the relayer parks BUBBLE deliveries. |
| gno.land's validator set outgrows the client (the heap's bound, §6.1), or takes a key the client refuses (§13) | A set the client cannot follow is a lapsed client and the end of the deployment (§6.4), with burns as the only exit; `client:set-bound` alarms at 40 validators, the heap frame is built before the set gets there, and the launch notice says so; the validators themselves are asked for nothing (§11.10). |
| gno's object-graph hash gap | Not relied on: events through LastResultsHash; the fix is in notes/. |

## 10. Costs and timeline

- The launch: the gno.land realms, the three Solana programs and the signers, relayers, monitors and hlwatch's
  gno.land half were built on 2026-10-12 (BUILD.md: M1, M2, M3a, then the same day M2b — the v26 programs — and M3b — the
  updater, the delivery under the light client, `hlsol setup` and `audit`, the Solana halves of the monitor and
  hlwatch — with the local loop green end to end; the v27 code and the first audit round's fixes landed the next day); then two weeks of the client
  following gnoland-1 on devnet, the four 72-hour runs and the drills; then the mainnet deployment under the governor and his `Launch`, about mid-December 2026 on §12's rounds and the audit loop alone (the deploy, the setup step, the audit and the checklist take days), and the testing phase with a small lock; a human review (§11.2: 2–3 weeks, ≈$20–35k, paid by
  the operator) and the external audit of the client, the admin program and the pausable ISM (3–4 weeks, ≈$40–80k,
  the operator's) before the yield if §11.2 says so, run during the testing phase — a fix to one of the five upgradable programs lands by the governor's kind 8 in place, 7 days, and a fix to the admin program is a fresh deployment, its rent again (§6.4), the price of testing on mainnet before the audit; the audit first and the mainnet deploy after it, about February 2027, avoids it — then the outsiders seated, the notice and the yield vote: about February 2027 with the audit first,
  early-to-mid-January 2027 with the review alone (two weeks of slack), the end of a month's testing phase on §12's rounds and the audit loop alone (a fix after the audit restarts the two-week devnet
  run and the members' replay of the final build; the dates assume none). A fresh deployment — §6.4's ending — costs the
  same again: the rent, the pool's seed and the launch procedure, never the lock. SOL: ≈8.5 of rent (the three programs measured as built after M2j's removal of the recovery path, the
  reproducible build to confirm) plus ≈0.13 of state accounts and 1.5 of floats ≈10.2 against 7.36 funded, so §11.4's 8 more
  is a precondition; three signer droplets with gno nodes and two monitor machines (which also host the relayers and the updater)
  ≈ $100–170/month plus six to eight Solana
  RPC accounts — the signers' four and the monitors' own (≈$150–200/month; one slot can be the operator's own
  Solana node at ≈$500/month instead); the updater's
  Solana fees ≈0.086 SOL/month (≈$9.5, four transactions of 5,000 lamports a header at six validators) at a header every 10 minutes, ≈0.39 at 500 transfers a day. Running cost
  at 50 transfers a day each way (at GNOT $0.069 and SOL $109.6, the prices of 2026-10-10): 1.4 SOL/month
  of unrecoverable rent for delivery records plus 0–2.3 SOL for first-time recipients (≈$150–400) and at most ≈135 GNOT of
  gno gas (`Process` and `Claim`, 45M gas each at 0.001 ugnot — `Claim` is built; its gas is measured on devnet, M4),
  against fee income of 5 GNOT per transfer each way (15,000 GNOT ≈ $1,035 a month against ≈$590–960 of rent,
  machines, RPC (the outsiders' provider accounts included), gas, the updater's fees and the outsiders' stipends:
  positive while roughly 55%–95% of the transfers
  are GNOT, depending on the new-recipient share, since BUBBLE fees have no firm price); at 500 a day (which the
  10,000-a-day cap barely allows at a 20 GNOT average): 14–37 SOL (≈$1,500–4,000) and at most 1,350 GNOT against 150,000
  GNOT ≈ $10,000; the fee covers a first-time recipient at GNOT ≥ $0.054.
- Proposal 2: gated on Alpenglow mainnet; 2–4 months of prover and verifier work after launch; a GPU prover box or
  network; a cryptographic audit (≈$30–50k); a month of shadow running; proving ≈$1–6 per burn-block paid from the
  proving fee; earliest Q2 2027, realistically Q3 2027; the verifier realm's deploy ≈ nothing in SOL. Proposal 3 has
  no date (SIMD-0511).
- What GovDAO gets and bears: the chain's own coin on Solana under a governance that is one person's, within the delays, through a testing phase with a small lock, and its own from
  the yield (§1: the governor decides until the yield and GovDAO after, the guardians can delay, the validators only validate), with the operator's machines
  out of the gno → Solana trust path from launch and out of Solana → gno from Proposal 2 for un-griefed blocks (the
  roster until Proposal 3; a griefed block is GovDAO's vote, §7.2); in return, the votes (unpause, void, replace,
  approve), and the fees go where GovDAO's treasurer vote says — to the operator while it pays the costs, to the
  chain whenever GovDAO says so.

## 11. Decisions that are Jae's

1. gno.land's own governance: a delay on validator-set changes in r/sys/validators and more GovDAO members are what
   would make the light client and the delays mean something against GovDAO itself. Outside this plan; the plan says
   so in §1.
2. Review and audit before the yield. A human review of the pausable ISM (small, the only brake on Solana minting),
   the light client, the admin program, the new realm code (claims, meters, builders), the Go signers and relayers
   (they hold every hot key) and `hlwatch`, the roster comparator (the outsiders' only eyes, written by the party they watch) and `hlrecover`'s execute path (a member's hand on a matured decision), 2–3 weeks and
   ≈$20–35k; and the external audit of the client, the admin program and the pausable ISM (3–4 weeks, ≈$40–80k) —
   before the yield, or within the first quarter after it under the caps; or yield on §12's model review rounds and the audit loop alone — the testing phase's small lock may precede either (§11.16).
   Recommendation: the review and the audit before the yield — those three programs are the whole trust path from the
   first block — and before the mainnet deploy if he would rather not risk the admin program's rent twice (§10); say either way in the launch notice.
3. (Gone with Phase 0: there is no interim multisig and nothing of Jae's holds a Solana authority.)
4. SOL: 8 more SOL to the deployer (FagcHCr…) — a precondition, not padding (6 until the programs were built on
   2026-10-12, 7 when they measured a SOL above the guesses, 8 after the first audit round grew the admin program by
   a sixth): ≈8.5 of rent plus state accounts and floats is ≈10.2 against
   7.36, and the ATA payers' 0.25 SOL each would trip the runway alarm on launch day (§5.5); ≈2.8 covers the
   shortfall (0.4 more of margin since M2j), 1 goes to each token's ATA payer, 1 to the relayers, 0.3 to the guardians' and watch keys, 0.3 to the
   updater's key, 0.1 to the admin program's authority PDA (§6.5), ≈1.1 is margin until the reproducible build fixes
   the sizes.
5. Token metadata on Solana (names, symbols, images): "GNOT" and "BUBBLE" as symbols; the images are branding, his;
   needed before the setup step (§5.8), so before the two-week devnet run.
6. Who governs the BUBBLE route: GovDAO (as GNOT) or BUBBLE's own holder governor. Recommendation: GovDAO while young.
7. Proposal 2 with the signers vouching per epoch before SIMD-0511, or waiting. Recommendation: build it, launch it
   with that rule stated, remove the rule when SIMD-0511 lands.
8. The gno object-hash gap: confirmed with a reproduction and a tested patch (notes/); filing and merging is his.
9. The guardians: four people, two of them outside the operator, GovDAO and the validator set, each with a named
   backup; they can pause (one flag) and hold (two flags), nothing else. Jae's decision of 2026-10-12 removed the
   veto, the standing veto, the affirmation rule and the 28-day stretch that v5–v21 carried ("GovDAO will grow; a
   veto over GovDAO is wrong"). A hold keeps GovDAO's decisions waiting until GovDAO replaces the holders — on the hold's 21st day at the
   earliest, since round 19 showed that a replacement landing at 7 days through a hold let anyone able to deliver a
   decision (a forger under a leaked set, a fail-open bug) replace the guardians before GovDAO could vote and
   deliver a cancel (§6.5, §11.15); so against a GovDAO pair the guardians buy 21 to 28 days, and against a forger
   the 21 days GovDAO's cancel needs — for a GovDAO that is the problem, is gone, or cannot reach the admin
   program past a forger, the hold is the whole brake (§6.4). Recommendation on the people: two outsiders outside
   NewTendermint, GovDAO and the validator set (a partner project, a known community member), each with a named
   backup. Their standing orders, written into the launch notice: look at `hlwatch` weekly, and daily while a flag of yours stands alone; start your first `hlwatch` from the deploy, never from the client's checkpoint, since a checkpointed watch takes the client's own count as true (§5.9) — re-set it each day, since an unpause clears only flags set before its delivery; flag both Solana objects
   (`hlwatch` does it in one stroke; the pausable ISM's half of the stroke never fails with the admin program's line of succession, which a forged successor could make unreadable) — both holders, before a distrusted validator change's quarantine ends, since a
   hold starts at its second flag and the quarantine's end is the Clock's alone (§6.1); a stolen seat's flag is
   worthless (the thief clears it), so the other seats hold; hold until GovDAO's cancels of what you hold against
   have executed or the deployment is abandoned (§6.4); on gno.land flag `payouts` only to stop payments you believe forged, since a
   guardian's `payouts` flag unlocks GovDAO's at-once void of the ids it names (a watch key's never does; name only
   the ids the alarm shows forged, void them as withheld first and as forged once a third source confirms the
   record's absence, and never vote a separate unpause of `payouts` while a flagged void is accepted and unexecuted
   — the void builder's `andUnpause` option does both in one closure) and a guardian who flags on GovDAO's word is
   its accelerator, and `credits` alone to stop a forgery from growing; the unpause to pre-empt is a dispatched,
   undelivered one that `hlwatch` shows, since an unpause clears only flags set before its delivery; hand the backup
   the key for any absence over a few days; under a hold, three or four seats hold when they can (a thief's clear
   of one then changes nothing), and GovDAO proposes no replacement of a holder's seat under a hold unless it means to end the hold (a
   replacement naming a holder's seat ends it at once when fewer than two flags remain, §6.1;
   a replacement of any other seat ends nothing); lift only once GovDAO's cancel of a decision you want dead has
   executed, since your own lift takes a day (§6.1) and a deferred decision past its 7th day executes the moment
   the hold ends. The four people — two outsiders and their two backups — are named at the yield, in
   the yield vote and the launch notice, not before (Jae, 2026-10-13: not yet; until then M4's outsider run and the testing phase's are done by the operator's stand-ins on two of its own machines, and the named outsiders repeat the run before the yield vote, seated by the governor's own rotation — a kind 5 and the matching gno.land rotation, 7 days each — ahead of it, so the vote reads them seated); if he declines the outsiders, the four keys stay the operator's and §1 must
   say the brake is one party's. One sub-question stays open: whether to add a
   fifth seat, a third outsider, so that against a pair including Jae one captured or coerced outsider still leaves
   two to hold (round 22, AM12) — recommendation: yes if a third outsider can be found; the seats are counted by
   number, but a yes renumbers the watch slots and the per-decision counts in §5.8, §6.5, §8.3, §13 and the glossary
   before the programs and realms are built for launch, so it is a pre-launch choice.
10. The validators are asked nothing (Jae, 2026-10-13: "validators should do nothing than validate"): no assent
    letter, no question about the backend that holds a consensus key and no signing tool for one — v29–v34's
    recovery path, which needed more than 2/3 of their power to sign a 32-byte digest, is gone (§6.4) — so the
    launch asks of a validator exactly what gno.land already does, that it sign blocks, and the launch notice names
    each validator's operator from r/gnops/valopers and nothing a validator had to answer.
11. What synthetic GNOT on Solana is for: a pool (where, how deep, who seeds it). Its depth is the loss bound on the
    Solana side while anything is trusted (§9); and the lock the launch notice expects in its first week, from which the pausable ISM's launch day cap is compiled (a tenth of it, at most 100,000; §5.4) — Jae's figure if he has one; he has none (2026-10-13), so the launch takes the default of 250,000 of each token, the smallest figure that keeps the realms' compiled 25,000 maximum (the day cap is then 25,000 of each token a day, the cap-ratio alarm judges it from day 14, and a kind 12 raises it by vote once the lock shows the demand); the notice prints the figure and calls it a default. With the governor (§11.16) the figure the mainnet deploy compiles is either that default — a testing lock under it is bounded by itself, and `mint:cap-ratio` alarms from day 14 until the lock reaches five caps, an alarm the phase expects — or the testing phase's own small lock, the realms' maximum set to match, the caps then raised by his decisions before the yield (kind 12 first, 7 days, the routes' maximum after, §5.4); the checklist prints the choice.
12. The fees: whether the treasurer proposed at launch is the operator (while it pays rent, machines and audits) or
    the chain's own address — the mailbox's Solana fee beneficiary is that treasurer's Solana key — and the operator's
    commitment — the plan assumes twelve months of running from launch
    and a reserve of three months' costs (≈$1,800–2,900) held by the operator; the launch notice says which and how
    much. Also the outsiders' stipend (from the fees, GovDAO-set; the plan's figure is 600 GNOT a month each, ≈$41 at
    today's price, for the small gno node `hlwatch`'s flag requires (§5.8), with each outsider's
    two Solana provider accounts and node in the outsider's own name, the operator reimbursing them from the stipend (≈$80 a month for both outsiders, counted in §10's band) — never accounts the operator could rotate, throttle or let lapse in the month it is the party under suspicion, from launch until
    GovDAO sets it;
    backups share their seat's) and Proposal 2's cryptographic audit (≈$30–50k, the operator's).
13. A third-party signer from launch: one of the three signer machines run by a partner (a gno node and a Solana
    provider of theirs; held to the outsiders' rule — outside the operator, GovDAO's members and their companies,
    and the validator set — else the pair that includes that member stops the exit at once), with hlmailbox's verifier requiring the partner's signature plus one of the operator's.
    What it buys: the operator alone can forge nothing on the Solana → gno path. What it costs: either party alone
    can close that exit for 7 days plus a rotation, exactly as the operator can today, and the partner joins §1's
    list of trusted parties until Proposal 2. Recommendation: yes, if a partner will run it; the replacement signers
    GovDAO might swap in (§9) are then that partner's and the operator's.
14. The at-once powers on gno.land (§5.3): a signer removal that would leave the verifier's rule unsatisfiable
    is refused when made (one made so between creation and execution waits 7 days), and a void is at once only for the ids a guardian's standing `payouts` flag names (a watch key's never unlocks
    it; a void passed in advance reaches only those ids) — the plan's
    choice, so that a GovDAO pair without the operator cannot close the way home inside the week §1 promises; the
    price is that a forgery nobody has flagged is voided only after 7 days, bounded meanwhile by the meters — about
    half the lock (1 − 0.9^7 ≈ 52%, up to 57% if the meter was full when the week began; a lock above 100,000 GNOT and 4 or
    more recipients assumed) if nobody flags for the
    whole week — and by any guardian's flag; and the week
    binds one of GovDAO's three pairs (moul and g1aeddl), since the other two include Jae. Recommendation: keep it,
    and say so in the launch notice. One more trade in the same rule: an at-once cut of one of three signers leaves
    2 of 2, no fault margin — if another box then dies the exit is closed until GovDAO's 7-day addition lands, and
    nothing clocks GovDAO's inaction; the alternative (no at-once cut unless the rule survives one more loss) keeps
    one compromised signer for 7 days, harmless alone since forging needs two. Recommendation: keep the at-once cut
    and name the trade; §11.13's partner as a fourth signer restores the margin.
15. The validators' override (from round 19) is gone — Jae's decision of 2026-10-13: a hold-opened window in which
    the last settled validators could cancel GovDAO's pending decisions and carry their own was a second channel
    into the admin program, and every change on Solana is now a light-client proof of a GovDAO decision (§6.4).
    What stays from that round is the second of its two rules, the hold's 21-day floor under a replacement of
    guardians (§6.5): without it a hold was worth 7 days against anyone able to deliver a decision (AJ1 in
    REVIEWS.md), and its rationale is now GovDAO's own time to vote and deliver a cancel of a replacement it never
    made — 21 days from the hold's start, 28 from delivery at most, against v22's 7 — not the validators'
    signatures (AL4's one-day margin for their signing no longer applies). Nobody overrides GovDAO on Solana;
    against a forger holding the validators' keys GovDAO's brake is that floor and its cancels, and the ending is
    §1's.
16. The governor and the yield (Jae, 2026-10-14: "I want to change this so I can deploy things slowly and test it
    out. Until I yield to GovDAO I want to be GovDAO alone"). Each realm compiles one governor address at deploy, and
    until his one-way `Yield` on hlmailbox every decision — every builder of §5.3, `Launch`, the verifier's
    activation, r/hlgov's decisions to Solana — is his direct call through the realm's `Govern` entry, with the same
    closures, checks, queues and 7-day delays, GovDAO's proposals refused; from the yield GovDAO's proxy is the only
    governor and the plan is as it was (§5.3, §6.6). `Execute(id)` stays anyone's, the apply-refund rule stays the
    governor's at-once act, and the Solana side is untouched: the admin program checks that r/hlgov sent a decision,
    never who decided. What it buys: a mainnet deployment he can run slowly, with a small lock, before any vote;
    what it costs: until the yield the trust statement is one person (§1), and a leaked governor key before the
    yield is a leaked lock unless the yield comes inside the thief's first decision's 7 days (§9). The key: his
    choice at deploy, and fixed by the deploy — the recommendation is the cold key g1ecsu… for any deployment that
    will hold real funds and a hot key only for one whose lock stays small until the yield (devnet's; mainnet's too,
    if the order below is kept). The order: deploy to devnet (M4: §5.9's two weeks, the runs and the drills); deploy
    to mainnet with the governor and publish the checklist (§6.6); `Launch`, and test slowly with a small lock — his
    own funds, no real holder invited — through §11.2's review or audit if either is chosen; name the two outsiders
    and their backups and seat them by his own rotation, 7 days before the yield (§11.9); publish the launch notice;
    the yield vote; then he reads `hlwatch`'s pre-armed list (every builder proposal GovDAO voted while he governed executes the block after the yield) and yields by his `Yield` only to proposals he accepts. Recommendation on the lock: keep it under what he would lose
    to a leak, and yield before the first holder who is not him.

## 13. Appendix — byte-level facts for the implementer (verified; sources in §12)

gno.land, height 701,862:
- Sign bytes (110 B): `6D` uvarint length · `08 02` type · `11` + 8-byte LE height · [`19` + 8-byte LE round, omitted
  when 0] · `22 48 { 0A 20 <blockhash> 12 24 { 08 01 12 20 <partshash> } }` · `2A 0B { 08 uvarint(sec) 10 uvarint(ns) }`
  (ns omitted if 0) · `32 09 "gnoland-1"`. Full example:
  `6D080211A6B50A000000000022480A20B0D79144E283B51F02881A930A56E0D98DB717702D116DFF92271FA5A75A872D1224080112205AE9C4415816870AB1B14D7024D797C7E3DC3098CC3DDB01F11E5F309360112F2A0B08A9C9A8D60610DFC2B9133209676E6F6C616E642D31`.
  The example is a one-part block: `08 01` is the part set's Total, which is the commit's own (a block carrying a
  transaction over 64 KB has 2 or more parts, up to 16 per 1 MB transaction), the length prefix (`6D` here) follows
  it, and each precommit's sign bytes carry its own BlockID and its own timestamp — the verifier builds them per
  precommit, never from one template; every precommit of a commit carries one round (tm2's `Commit.ValidateBasic`), which the staged update carries once and a precommit of another round is refused against (`RoundMismatch`); a next set is staged 23 validators a transaction and a commit's precommits 40 a transaction, each under the 1,232-byte packet.
- Header leaves (an empty field's leaf is `sha256(0x00)`): Version `0A0B"v1.0.0-rc.0"`; ChainID `0A09"gnoland-1"`;
  Height `08`+zigzag; Time `08 uvarint(sec) 10 uvarint(ns)`; NumTxs/TotalTxs `08`+zigzag, empty when 0; AppVersion
  empty; LastBlockID `0A20<h> 1224 {0802 1220 <ph>}`; LastCommitHash, ValidatorsHash, NextValidatorsHash,
  ConsensusHash, AppHash `0A20<32>`; DataHash and LastResultsHash empty when nil; ProposerAddress `0A28<bech32>`.
  Merkle: leaf `sha256(0x00‖x)`, inner `sha256(0x01‖l‖r)`, split at the largest power of two below n.
- Validator leaf: `0A37 { 0A11"/tm.PubKeyEd25519" 1222 { 0A20<pk> } } 10 <zigzag power>`; validators sorted by
  `sha256(pk)[:20]` ascending; ValidatorsHash at 701,862 = `09863806…`. A validator with a non-ed25519 key is a
  refusal (the client stops: a set it cannot follow ends the deployment, §6.4); ed25519 is verified strictly (canonical S, no
  non-canonical point encodings); a commit's precommits beyond the valset's length are a refusal.
- Commit rules: count by slot index; `len(precommits) == len(valset)`; nil precommits are real signatures over a
  BlockID-less canonical vote and must not be tallied; prevotes differ only in the type byte; `3·tallied > 2·total`.
- Results leaf: `[0A <Any error>] 12 <data> 1A <Any event>…`; event `0A09"/tm.Event" 12 len {0A type, 12 {0A key 12
  value}…, 1A pkg_path}`; `LastResultsHash` of block H sits in header H+1 (reproduced: `CB2C900C…`).
- The `Dispatch` event: type `Dispatch`, pkg_path the mailbox realm's real path on gnoland-1 — address-namespaced
  under r/sys/names (`gno.land/r/<deployer address>/hlmailbox`, the address in the launch notice — r/sys/names allows
  an address's own namespace, or a name registered to it in r/sys/users; this plan's r/hlmailbox,
  r/hlgnot, r/hlbubble, r/hlfees and r/hlgov are shorthand), fixed in the ISM's constant, the enrolled routers and
  the admin program's sender constant, and printed in the launch notice — attributes in this order: `id` (32 bytes,
  lowercase hex), `nonce` (decimal), `origin` and `destination` (decimal), `sender` and `recipient` (32 bytes,
  lowercase hex, as the message carries them), `hash` (sha256 of the message bytes, lowercase hex). `Reemit` emits
  the same attributes. Domains: 1198419788 ("GnoL", gnoland-1), 1198419796 ("GnoT", every other chain); our
  mailbox's local domain is Solana's own (1399811149 on mainnet, 1399811151 on devnet), bound in the routes'
  routers and in every checkpoint digest.
- A decision message (r/hlgov → the admin program): body `kind u8 ‖ seq u64 BE ‖ params`; the admin program records
  on the first 9 bytes alone and parses `params` only at execution; there is no other encoding of a decision and no
  other way one is carried — no digest over a body, no signature but the block's (§6.4). The kind table,
  frozen before r/hlgov deploys (§5.3): 1 `cancel` (salt u64 — random, ignored by the program — then form u8: 0 = a list, count u8 ≤ 16 then `channel u8 ‖ number u64` each; 1 = a range, `channel u8 ‖ from u64 ‖ to u64` inclusive — channel 0, the mailbox sequence, the only channel there is; a cancel naming another fails its checks and is spent, §6.5) ·
  2 `unpause` (object u8: 0 the pausable ISM, 1 the admin program) · 3 `set token ISM` (token u8; ISM pubkey 32, which must be the pausable ISM's id or zero — a new pausable ISM is a
  kind-8 upgrade in place, and the mailbox's default ISM, which no kind can set, is the launch pausable ISM for the
  life of the deployment) · 4 reserved, refused at execution: the client's state layout, head and body, that the pausable ISM and the
  immutable admin program read is fixed for the deployment's life (version byte 1) and a client `upgrade` keeps it
  (the pausable ISM derives the client's state account from the launch client's id by the fixed seed `state`, reads
  the quarantine from it, and requires it to be owned by that program with version 1); a
  new layout is a successor admin program and pausable ISM, by kind 9, built to read it and transferred before the
  client's upgrade · 5
  `set guardians and watch keys` (count u8, then pairs `seat u8 ‖ pubkey 32` —
  seats 1–4 guardians, at most two; 5–6 watch slots; count ≤ 4; a seat's current key is refused; executes 7 days
  after delivery through any lone flag, and under a hold on the later of that and the hold's 21st day, §6.5) · 6 `enroll router` (token u8; domain
  u32; router 32 — for gno.land's own domain the builder and the closure accept only the token's own live route's address (`RouteSymbol` names the token; a migrated route is refused; a successor only once its page 0 is in),
  since any other would mint against no lock or strand the token's burns) · 7 `set protocol fee` (lamports u64 ≤ the mailbox's maximum; beneficiary 32 — Hyperlane's
  instruction replaces both together, and the launch audit prints the beneficiary, r/hlfees' Solana counterpart,
  the treasurer's key) · 8 `upgrade` (program 32, never the admin program's own id; buffer 32; ELF sha256 32 over the buffer's ELF bytes after the loader's header; the refund
  key is the signer `stage upgrade` recorded (§6.5) — the loader's spill on upgrade, the buffer's recipient on
  void, spend or cancel; execution extends the program data first) · 9 `transfer to successor` (program 32, never the admin program's own id nor a former recipient's; refused unless the successor's programdata account shows no upgrade
  authority, and spent unless the successor's state names this program as predecessor, not yet inherited, with no successor of its own and compiled at this build's clock factor, so no human key ever inherits the authorities — a realm has no RPC, so the proposer and `hlwatch` check it before the decision, as they check kind 8's program)
  · 10 and 11 reserved (the recovery's `set sequence` and `reset client` until v35; recorded and spent at recording, never pending (kind 4 pends with a deliverer's rent and is spent at execution);
  `handle`'s next-number arithmetic never panics) · 12 `set mint limits` (token u8;
  per-message maximum u64; daily bucket u64, in base units — the pausable ISM's `Verify` parses the token message's
  amount and refuses one above the maximum or beyond the day's bucket — a rolling bucket draining at the day cap per 24 hours from each charge, no calendar day — compiled at launch as the smaller of 25,000 and the day cap a message (25,000 GNOT and 25,000
  BUBBLE at the default expected lock) and, a day, the smaller of 100,000 and a tenth of the lock the launch notice expects (§5.4), the clock factor dividing the day; the pausable ISM binds its charge to the delivery through the Instructions sysvar — the top-level instruction must be the mailbox's `InboxProcess` carrying this message — since the mainnet mailbox build signs no per-ISM process authority; r/hlgov's builder refuses a maximum of 0, a bucket below the maximum and a maximum below the route's current gno.land maximum, §5.4). Kinds 3–9 and 12
  are the 7-day group; 4, 10 and 11 are reserved.
  A client `upgrade` never changes the state layout; one that would takes the successor route of kind 4's note
  first. Unknown kinds are recorded and spent at recording.
- Fixed seeds: the pausable ISM's flag account `flags`; each of our programs' state account `state` (each object's state holds `holdSince` and the Clock time its guardian flags last dipped below two, §6.1); the admin
  program's authority PDA `authority`; a staged update (`update`, submitter, header hash); a staged proof
  (`proof`, submitter, the message hash); a pending decision (`decision`, number); an executed cancel leaves no record — the cancelled decisions' accounts close and their numbers stay in the state's cancelled ranges.
- Solana words used above: the Clock sysvar is the chain's own clock, readable by programs; an address lookup table
  lets a transaction name more accounts than fit in it; a process authority is the PDA the mailbox signs with when
  it calls a recipient's `Handle`, one per recipient (the master tree adds one per ISM for `Verify`); a staged PDA is an account a submitter fills over several transactions before
  the one that uses it; a `SimpleProof` is tm2's merkle inclusion proof.
- `Bytes32ToAddress` requires the top 12 bytes to be zero; a message whose recipient has any other is credited to the route's sweep queue, never to a truncated address (§5.3).
- State proofs (not used by the bridge): `abci_query path=".store/main/key" data=<key> prove=true height=H−1`, keypath
  `/main/x:<hex key>`; ops `ics23:bptree` (leaf `sha256(0x00‖uvarint(len k)‖k‖0x20‖sha256(v))`, inner
  `sha256(0x01‖l‖r)`, EmptyChild `sha256(0x02)`, MinDepth 5, MaxDepth 60) then `ics23:simple` (key `"main"`, value =
  the main commit hash, one inner op with the base leaf); AppHash = `SimpleHashFromMap({main, base})`.
- Chain: genesis 2026-09-12T15:00Z; 3.43 s/block; the fifth validator signed from height 487,883 (at power 1; raised to 60 at 495,707), the sixth from 720,333 (at power 1; raised to 60 at 741,862); the state store's pruning keeps
  705,600 versions, and block results are never pruned (the bridge proves from them, never from state). Conversions: 7 days = 176,327 blocks; 14 days = 352,653 blocks; on Solana, where every delay is
  measured by the Clock sysvar's unix time; at today's 0.22-second slot 24 h ≈ 390,000 slots, 7 days ≈ 2.75M, an epoch ≈ 26 hours — figures of scale only, which no rule reads.
- Solana costs: ed25519 precompile 2,400 CU per signature; sha256 ≈ 85 + len/2 CU; curve25519 edwards msm 2,273 +
  758/point; transaction limit 1,232 B for the legacy and v0 transactions the tools send (a v1 transaction, SIMD-0385, carries 4,096 B on mainnet since 2026-09-15 — a later build may use it for the staging and the proof header).

Solana, Alpenglow (agave master, 2026-10-10):
- Vote payload: `wincode(VotePayloadToSign)`, u8 tag: 1 `Notar{Block{slot u64, block_id [32]}, shred_version u16}`,
  2 `Finalize{slot, shred_version}`, 3 `Skip`, 4 `NotarFallback{Block, sv}`, 5 `SkipFallback{slot, sv}`.
- Signatures: BLS12-381 min-pk, pubkey G1 (48 B compressed), signature G2 (96 B compressed); hash-to-curve DST
  `BLS_SIG_BLS12381G2_XMD:SHA-256_SSWU_RO_POP_`; PoP DST `BLS_POP_BLS12381G2_XMD:SHA-256_SSWU_RO_POP_` over
  `"ALPENGLOW" ‖ vote_account ‖ bls_pubkey`; aggregate pubkey = the sum of signers'; base-3 bitmaps for mixed certs;
  ranks u16 from the epoch's sorted vote accounts with a registered BLS key, duplicates excluded, ≤2,000.
- Certificate = `{cert_type, signature, bitmap}`; thresholds as §2; `BlockFooterV1` (`VersionedBlockFooter` tag 1):
  `bank_hash [32]`, `block_producer_time_nanos u64`, `block_user_agent`, `block_final_cert Option<{slot, block_id,
  final_aggregate{sig 96 B, bitmap}, notar_aggregate Option<…>}>`, two reward certs.
- Block id: `MerkleTree(FEC roots ++ [hashv(parent_slot LE u64 ‖ parent_block_id ‖ fec_set_count LE u32)])`, leaf
  prefix `\x00SOLANA_MERKLE_SHREDS_LEAF`; FEC sets are 32 data shreds.
- Bank hash: `h = sha256(parent_hash ‖ sig_count LE u64 ‖ last_blockhash); h = sha256(h ‖ lt_hash 2048 B)`; with a hard
  fork at the slot, `h = sha256(h ‖ hard_fork_data)`.
- LtHash: 1024 × u16 wrapping add/sub; per account `blake3_xof(2048)(lamports ‖ data ‖ executable ‖ owner ‖ pubkey)`,
  zero-lamport → identity; per block: mix out each modified account's pre-block state, mix in its final state.
- gno gas (native_gas.go, draft): `pairingCheck` 457,574 + 1,745/byte (four pairs ≈ 1.80M); `g1Mul` 44,465; `g1Add`
  14,883; `cometbls.verifyZKP` 2.63M; `sha256` 226 + 8.7/byte; block MaxGas default 3B; storage 100 ugnot/byte.
- Shred-version is inside the signed vote payload; the Genesis certificate at migration, the 2,000-validator cap and
  Rotor's possible change to the FEC structure are watch items.

## 12. Review log

The eighteen review rounds — every finding, its severity and its answer, with ⟂ marks on answers a later round
overturned — are in `REVIEWS.md`, moved out of this file at v22 to keep the plan readable. The current rule is always
the one in §1–§11 and §13; the log cites those section numbers.

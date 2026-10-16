# GNOT and BUBBLE between gno.land and Ethereum — the Solana bridge's design, carried to Ethereum

Status: plan e10, 2026-10-15 — e9 with audit round 5 folded (six audits; folds F17a–F17d, the Solana bridge's S6 and
the integration lane I3; §12): every rule below is the merged code's, and what that code does not do is said where the
rule is; e9 was e8 with audit round 4 folded (ten audits; folds F15a–F15e, the Solana bridge's S5 and the integration
lane I2); e8 was e7 with audit round 3 folded (nine audits; folds F10–F14, the fixes shared with the Solana bridge and
the integration lane I1); e7 was e6 with fold F5's readings (the decision record bounded by the start and the last number,
the launch line `launchNonce`, the successor's bindings, the pinned `HlAdminExec`); e6 had the rest of audit round 2 folded (the plan red team's E2-RT1–RT10, the realms'
E2-RL1–RL7; `AUDIT.md`); e5 had the first code audit round's contract and Ed25519 findings (`audits/r2/`: the
setup's hidden-state attack E2-CT1 and its answer, the trust root bound on chain, the stage alarm, small-order keys);
e4 folded F2's re-check of §6.5 and §13 and E-M3's readings; e3 added
the build's measurements and readings (E-M1 to E-M4, `M*-NOTES.md`); e2 folded
the first review round (`AUDIT.md`: the red team's ER1–ER11 and the fact check's EF1–EF11). e1 was written from the Solana bridge's plan v36 (`bridge/PLAN.md` on this branch, published as
`solana/bridge/PLAN.md`) on Jae's word of 2026-10-14: "make an ethereum <> gno.land bridge in the spirit (as close as
reasonable) to the solana <> gno.land bridge and audit it" — "it goes here of course https://github.com/gnolang/bridges
and bridge.gno.world". Nothing is deployed. Every fact about Ethereum below was read on 2026-10-14 from a primary
source (§2 names them; the research notes are `notes/research-*.md`).

**How to read this plan.** The Solana plan is the design; this plan is that design on Ethereum. Where a rule is the
same, this plan says "as the Solana plan §x" and the Solana plan's words govern, read with the substitutions of §0.
Where Ethereum changes a rule, this plan says what changes and why. Every rule of the Solana plan that this plan does
not name holds unchanged on the gno.land side. The trust statement (§1) and the Ethereum side (§5.8, §6, §13) are
written out in full, because they differ.

## 0. The substitutions

| In the Solana plan | Here |
|---|---|
| Solana, a Solana slot | Ethereum, an Ethereum block (12-second slots; a block's `number`) |
| the Clock sysvar's unix time | `block.timestamp` (a post-merge block's timestamp is its slot's, fixed by the protocol: `genesis + 12·slot`) |
| finalized commitment | the `finalized` block tag: Casper FFG finality, 64–95 slots (12.8–19 minutes) behind the head (88 slots when measured, §2) |
| our deployment of Hyperlane's mailbox program | our deployment of Hyperlane's `Mailbox` (EVM, `@hyperlane-xyz/core` 12.2.0, unchanged), behind OpenZeppelin's `TransparentUpgradeableProxy` |
| two synthetic tokens (Hyperlane's token program, deployed twice) | two `HypERC20` (Hyperlane's synthetic ERC-20, 12.2.0, unchanged), each behind its own proxy; decimals 6, scale 1/1 |
| the mailbox's protocol fee and beneficiary | Hyperlane's `ProtocolFee` hook (12.2.0, unchanged) as the mailbox's required hook; `MerkleTreeHook` (unchanged) as its default hook |
| the light-client ISM (Rust program) | `GnoLightClient` (our Solidity), behind a proxy |
| the pausable ISM (Rust program) | `PausableGate` (our Solidity), behind a proxy — named so as not to be taken for Hyperlane's stock `PausableIsm` |
| the admin program (immutable; its upgrade authority burned) | `HlAdmin` (our Solidity) and `HlAdminExec`, its execution half (created by `HlAdmin` in its constructor, to keep each under EIP-170's 24,576 bytes): plain code, never behind a proxy, so immutable from their deployment |
| an upgrade authority, the loader, programdata | the proxies' admin: one OpenZeppelin `ProxyAdmin` (v4.9.3, unchanged), owned by `HlAdmin` |
| an owner (the mailbox's, a token's) | OpenZeppelin `Ownable` ownership, held by `HlAdmin` |
| a PDA, rent, an account | contract storage, gas; nothing on Ethereum charges rent, so nothing here is "closed for its rent" except to free storage |
| an ATA payer | none: an ERC-20 balance needs no account |
| the ed25519 precompile | `Ed25519Verifier` (our Solidity, its own immutable contract: SHA-512 and the curve in code — Ethereum has no ed25519 precompile, §2 — called by the client through an address fixed in the client's implementation) |
| the Instructions sysvar binding a charge to its delivery | `msg.sender == mailbox` in `PausableGate.verify`: the mailbox calls an ISM only inside `process`, which reverts whole if the recipient's `handle` fails (§2's Mailbox bullet) |
| staged update and proof accounts | the update's calldata, one transaction while it fits; a staged tally in storage when it does not, keyed by the epoch and the expected set and re-checked against the live state when it finishes (§6.3) |
| an abandoned stage, closed by anyone after 7 days for its rent | nothing: there is no rent; a stage dies with its epoch or its set and can never finish stale (§6.3) |
| the client's state layout pinned for the deployment's life (kind 4) | every upgrade keeps each implementation's storage layout append-only — linear layouts with storage gaps, OpenZeppelin 4.9's and Hyperlane's own convention, in the five upgradable contracts (`HlAdmin`'s state, shared by `HlAdminExec`, sits in an ERC-7201 namespace, and `HlAdmin` is never upgraded) — a rule of the members' vote, which nothing on chain checks (E3-RT10): `hlwatch check` reads the old and new layouts it is given (`-layout-old`, `-layout-new`: forge's `storageLayout`; it reads none from the builds) and calls a kind 8 immutable only with both read and the new one append-only — judged through the layouts' types, not their names: a struct's members, an array's length and element, a mapping's key and value, every size (an enum's members aside, which forge's `storageLayout` does not list: a reordered enum compares equal, and none of our contracts declares one, E5-GT4); a struct gains members only in its unused bytes, and an array never shrinks and grows only as the layout's last entry (E4-GT3) — a consumed storage gap accepted, else LAYOUT NOT CHECKED and NOT SHOWN IMMUTABLE (E3-CF3) (a reordered layout could reset the mailbox's delivered marks or the tokens' balances); Hyperlane 12.2.0's `TokenRouter` keeps its fee recipient and fee hook at keccak-named slots no `storageLayout` lists, so the comparison cannot see them, and kind 8's after-checks read both by view (§13; E4-FC8); kind 4 stays reserved |
| `hlsol setup` / `hlsol audit` | `hleth setup` / `hleth audit` |
| `hlrecover` (status, body, execute) | `hleth decision status` / `body` / `execute`, the members' commands, which follow `HlAdmin`'s line to the live one (`successor()`, E4-TL6) |
| a pending decision's rent, returned to its deliverer | storage gas, never repaid: clearing a decision's slots refunds at most a fifth of the clearing transaction's gas to whoever clears it (EIP-3529) |
| Solana's domain (1399811149) | Ethereum's Hyperlane domain: 1 on mainnet, 11155111 on Sepolia, 31337 on the local run |
| a Solana address in base58 | an Ethereum address, `0x` and 40 hex digits, carried in 32 bytes left-padded with twelve zero bytes as Hyperlane does |
| devnet | Sepolia, beside a gno.land test chain |
| SOL | ETH |

The gno.land domain is unchanged (1198419788 "GnoL" for gnoland-1, 1198419796 "GnoT" for any other chain). The
realms are a second deployment, beside the Solana bridge's and sharing nothing with it but the three pure packages
(§4).

## 1. What this asks of GovDAO, and what is trusted at each step

**What it asks.** As the Solana plan §1: nothing at the launch. The bridge launches under one governor — Jae's key,
compiled into the Ethereum deployment's realms — and GovDAO becomes its owner at the governor's one-way `Yield` on
this deployment's own hlmailbox (a separate yield from the Solana bridge's). Then one proposal more, Proposal 2
(§7): Ethereum → gno.land on proofs. There is no Proposal 3 like Solana's: Ethereum commits its own validator roster
(the sync committee, in its beacon state), so the signers vouch for no roster; they stay only as a slow fallback whose
credits wait 7 days, so that an Ethereum fork the verifier has not followed slows the exit and never closes it
(§7.3) — and a later proposal may empty them once the verifier has followed forks without them.

**What is trusted**, so the yield vote can repeat it:
- The governor, until the yield, and GovDAO from it: exactly as the Solana plan §1's first two bullets, with
  "Solana" read as "Ethereum". Every setting on both chains is a decision — the governor's direct call through a
  realm's `Govern` until the yield, a GovDAO proposal after it — made with the realms' builders and, for Ethereum,
  carried by r/hlgov as a message the light client proves; `HlAdmin` acts on such a proven message from r/hlgov
  and on nothing else, and holds every Ethereum authority from the end of the setup step. A leaked governor key
  before the yield is a leaked lock unless he sees it inside the thief's first decision's 7 days (Solana plan §9).
  On gnoland-1 every bridge proposal is made in a `MsgRun` (a builder's request is a struct no `MsgCall` carries),
  and `vm:p:run_submitters` admits three keys, GovDAO's three T1 members, Jae's cold key one of them (read
  2026-10-14): after the yield only those three can propose a cancel, an unpause, a void or a rotation, and a member
  outside the list votes and executes but never proposes (E3-RL5; §11.9).
- gno.land's validators: the light client on Ethereum accepts a header only when validators holding more than
  2/3 of the current set's power signed it, whoever they are (today 5 of 6). Everything the Solana plan §1 says of
  them holds: they can forge only with more than 2/3 of the power, halt with 1/3 or more, carry no decision of
  their own, and a leak of the current set's keys is a race GovDAO must keep up by cancelling, ending in a rotation
  on gno.land and a fresh deployment. Jae runs one of them (Mithrandir, 60 of 360 today).
- The 3 signers, the way back until Proposal 2 and its slow fallback after it (a credit on the signers alone then
  waits 7 days, §7.3): the operator's three machines, 2 of 3 of which must sign a burn on Ethereum before gno.land
  credits it. Each reads Ethereum through two RPC providers that must agree, and signs only a burn
  in a block at or below Ethereum's `finalized` block (§5.6). What they can do is the Solana plan §1's signers
  bullet: pay forged claims at the meters' rate until GovDAO voids them (they never mint on Ethereum), or stop, which
  closes Ethereum → gno.land for 7 days plus a rotation. What finality adds: a burn they sign cannot be undone on
  Ethereum unless validators holding at least 1/3 of all staked ETH are slashed for it — an event Ethereum has
  never had — so a reorganisation is no path to a forged credit. Each reads gno.land through the node its
  configuration names, which must be its own — a requirement for every signer and monitor, since the cut reads its
  state and its head from one node, and a load-balanced public endpoint can answer them from different backends and
  so flag an honest credit (E5-RT6): the Solana deployment's machines read public endpoints (rpc.gno.land, Onbloc),
  whose operators are then trusted for the signers' circuit breaker and whose per-IP limit has banned a machine
  (E3-AS5; §11.10).
- The guardians and the watch keys: as the Solana plan §1, on Ethereum's two objects (`PausableGate` and
  `HlAdmin`). One flag pauses gno.land → Ethereum deliveries or `HlAdmin`'s decisions; two guardians' flags hold;
  a hold keeps every decision of the 7-day group waiting, a replacement of guardians included until the hold's 21st
  day; the guardians delay, they never veto.
- The operator: as the Solana plan §1. On Ethereum it pays gas, not rent, and it alone is likely to pay for the
  light client's header updates (§5.5's costs); anyone may update, relay, re-emit, claim and execute.
- The code. Eleven codes run on Ethereum, four of them behind five proxies, sixteen deployments in all (§5.8):
  Hyperlane's `Mailbox`, two `HypERC20`, `MerkleTreeHook` and `ProtocolFee` (release 12.2.0, byte for byte as compiled
  from the compiler input Hyperlane published with it, §5.9), OpenZeppelin's `ProxyAdmin` and proxies (4.9.3), and
  ours: `GnoLightClient` (with the tm2 libraries inside), `Ed25519Verifier`, `PausableGate`, `HlAdmin` and
  `HlAdminExec`.
  "Audited" is said only where a report says so: the mailbox's `process` is textually the code Hyperlane's v3 audits
  read (2023); the token router that mints — `TokenRouter`/`HypERC20` of 10.0.0 and later (the October 2025
  refactor, the 11.0.0 changes) — is covered by no audit Hyperlane has published, so it is in §11.2's scope beside
  ours. Ours are reviewed by the audit loop (`AUDIT.md`) and by nobody else yet; §11.2 decides on a human review and
  an outside audit. Five are upgradable — the mailbox, both tokens, the client and the gate — only by a decision
  that waits 7 days and waits on under a hold (kind 8, §6.5), and the members vote an upgrade only when `hlwatch`
  shows each storage layout append-only (§0: the vote's rule; nothing on chain checks a layout, E3-RT10); `HlAdmin`
  can never be changed, and a bug in it ends as the Solana plan §1's "the code" bullet
  says (a hand-over by kind 9 when the recording and kind 9 are sound, else a fresh deployment). The ed25519 verifier
  is the heart of the gno.land → Ethereum direction: a bug that accepts a signature it should refuse lets anyone
  forge a header. Its one input for a key is the key's 32 bytes, from which it derives every point itself (a library
  that takes a point beside the key verifies forgeries, §2), and it must agree with Go's `crypto/ed25519` (which tm2
  calls) on every input (§5.9): it is our own implementation of Go's verify path, not the audited SCL, which differs
  from Go in four ways (the point beside the key, S = 0 refused, [L−k]A for a key with a torsion part, a division by
  zero at (0, ±1)) — so it rests on its gates (§6.6) and the audit, not on someone else's audit.
- Ethereum itself: its consensus (Casper FFG finality with LMD-GHOST; at least 2/3 of stake to finalize, a reversal
  of finality costs at least 1/3 of all stake), its execution (the EVM as specified, and as its forks change it:
  Glamsterdam is live on Sepolia and has no mainnet date, §2), and the RPC providers each machine reads it through
  (two per signer and monitor that must agree, independent by operator, which no configuration shows; the operator's
  claims, as the Solana plan §8.7; §5.6, E3-AS4 — the updater and the relayers, the executor among them, read through
  one, which decides their spending and never a credit: a lying one can withhold a header's booking, and make one
  only from a transaction `eth.updater`'s key signed that carries the header, E5-RT3, §9). After Proposal 2, Ethereum's
  sync committee (512 validators, resampled every ~27 hours; a committee is not slashed for signing a wrong header —
  §7.2 says what that costs).
- The price of the exit a GovDAO pair without the operator can impose within 7 days, as the Solana plan §1 says of
  its 0.05 SOL: the mailbox's protocol fee, at most 0.001 ETH a burn (≈$2.4; `ProtocolFee`'s ceiling, fixed at
  deploy), and — as on Solana — the route's own fee, which a raise (7 days) can lift to a quarter of the cap floor
  (2,500 GNOT, 2,500 BUBBLE) in both directions, since a route has one fee; the meters still pay what remains.
- BUBBLE itself, and a dead GovDAO: as the Solana plan §1's last two bullets.

**The hard truth** of the Solana plan §1 holds here unchanged: every delay protects against everyone except 2 GovDAO
members — and, until the yield, 1 — who can replace gno.land's validator set in one block; the light client and the
7-day delays are a brake, never a lock; what the week buys on gno.land is time to leave at the meters' rate.

**The end state.** Ethereum accepts a gno.land transfer only after checking that more than 2/3 of gno.land's
validator power signed the block it came from; gno.land accepts an Ethereum transfer at once with a zero-knowledge
proof that Ethereum's sync committee signed a finalized header whose state holds the burn (§7), or on 2 of the 3
signers alone after 7 days — the slow fallback Proposal 2 keeps for a fork the verifier has not followed, until a
later proposal empties the signer set (§7.3) — so the signers stay trusted, GovDAO voiding inside those 7 days what
they forge (E3-RT10). `HlAdmin` is immutable from its deployment; what stays changeable is changeable only by a
decision, after the delay.

## 2. Facts this plan rests on (read 2026-10-14; sources in `notes/research-*.md`)

gno.land: as the Solana plan §2 and §13 (tm2's header hash, the precommit sign bytes, the commit rule, the
validator leaf, `LastResultsHash` over `ABCIResult` leaves, the `Dispatch` event, GovDAO's executor semantics,
gnoland-1's six validators at 60 each). One addition: tm2 v1.2.0 verifies a precommit's signature with
`golang.org/x/crypto/ed25519.Verify` (tm2/pkg/crypto/ed25519/ed25519.go:130–135), a one-line wrapper of the standard
library's `crypto/ed25519.Verify` — at Go 1.25.9, the version gno v1.2.0's go.mod names,
`crypto/internal/fips140/ed25519.verifyWithDom` and `fips140/edwards25519.Point.SetBytes`: a signature whose last
byte has any of its top three bits set is refused, S must be below the group order L (S = 0 is canonical), the
public key is decoded laxly (a y of 2^255−19 or more is reduced; the sign bit of a zero x is ignored; only a y with
no square root is refused), the challenge hashes the public key's 32 bytes as given (never re-encoded), R is never
decoded but compared byte for byte with the encoding of [S]B − [k]A, and the check is cofactorless. `Ed25519.sol`
implements exactly that (§6.6) — not the Solana client's strict check, which the Solana plan §13 describes.

Which gno gnoland-1 runs (E3-AS3; gnolang's upgrade ledger, read 2026-10-14): v1.2.0 only from genesis to height
36,300, then v1.3.0, v1.4.0 (from 113,000) and v1.5.0 (from 162,200) by halting upgrades, and since 2026-10-13
v1.5.1, a rolling patch of p2p and the node alone. Every source file of what the client verifies — tm2's block,
vote, canonical, validator, validator-set and results types, its merkle and ed25519, amino's encoder, the execution
and baseapp code that writes the results, gnovm's `chain.Emit` — is byte-identical from v1.2.0 to v1.5.1, go.mod
naming Go 1.25.9 throughout; a live header's sixteen leaves, its precommits, its set and a results leaf rebuild under
the client's encodings; and a failed transaction's results leaf carries one of a closed set of 43 error types, the
same at every version (§13). The realms' suites and the local run still use v1.2.0's tools (the realms' suite passes
under v1.5.1 too, 165 tests at fold F17c; fold F10's page gas was measured alike on gnodev v1.2.0 and v1.5.1, and the
render-gas gate's and the migration gate's node factors on a v1.5.1 node, §5.1, §5.3). An upgrade that changed one of
those encodings is §9's.

Ethereum (live reads at block 26,189,683, 2026-10-14 08:56 UTC, on ethereum-rpc.publicnode.com and eth.drpc.org):
- The block gas limit is 60,000,000 (EIP-7935's default; 59,941,408 at block 26,189,683). The base fee was 0.0716 gwei; over the last 1,024 blocks its median was 0.065
  gwei; over 30 days (ultrasound.money, every block) its mean was 0.381 gwei, its maximum 24.86 gwei; a 73,728-block
  sample gives a median of 0.128 gwei, p95 1.65, p99 4.31. Priority fees: median 0.01 gwei, p90 1 gwei.
- ETH $2,417.10, GNOT $0.07223 (Kraken, 2026-10-14 23:19 UTC, E3-AS6; at 08:56 they were $2,506 and $0.0707). Four
  hours later GNOT was $0.05617 and ETH $2,364.76 (2026-10-15 03:15 UTC; GNOT's 24-hour range $0.05442–$0.07558, on
  thin volume): GNOT/ETH ≈20.5% lower (E5-RT4). Every dollar figure here is at the first reading; §5.5's fee coverage
  moves with the rates (E4-AS2).
- Fusaka (mainnet 2025-12-03, EIP-7607) caps every transaction at 16,777,216 gas (EIP-7825) and added only the
  P-256 precompile (EIP-7951). There is no ed25519 precompile on Ethereum L1 (EIP-665 never shipped); the curve and
  SHA-512 run as EVM code.
- The audited Solidity ed25519 (SCL, get-smooth/crypto-lib, MIT; Veridise 2024) measured 808,531–810,468 gas for a
  48–150-byte message (two SHA-512 blocks; gnoland-1's precommit sign bytes are 110 or 111 at round 0, at most 120), 527,208 for one byte, 1,092,451
  for 250 (`eth_estimateGas` on its deployed copy, 2026-10-14) — about 280K per SHA-512 block. So one transaction
  verifies about 20 precommits, and an update at today's set (5 signatures) costs about 4.1M gas in signatures
  alone. SCL's `Verify` takes a precomputed key point it never checks against the 32-byte key: a signature by another
  key verified in the research's reproduction, so the client derives every key value from the stored 32 bytes
  itself (§6.6). Our own verifier (E-M2a) costs 220,816 gas a check on average for a 48–175-byte message (183,215 for
  one SHA-512 block, 258,450 for three), about a third of SCL's on the same inputs and EVM.
- Measured on our contracts (E-M2b's GasReport, Osaka rules, cold accounts and storage; its "exec" column, which is
  what a transaction pays: a top-level call's frame gas holds the base and the calldata already, so the report's
  "tx" column, which adds them again, overstates every figure, and real transactions on an Osaka anvil paid exactly
  exec, less any refund — E3-AS6, E4-FC1): an update at 4 validators (3 signatures) 907,280 gas; at 6 (5)
  1,382,273; at 20 (14) 3,561,178; at 28 (19) 4,770,358; at 40 (27) 6,704,376; at 60 (41) 10,084,827; at 100 (67) 16,378,547 — one transaction up to
  about 85 equal-power validators under the sender's 20% margin (100 fit the bare cap), staged beyond (255, the
  client's maximum, takes four or five calls, §6.3). `proveHeader` at 4 validators 872,925; a delivery with its mint
  (`Mailbox.process` through the gate and the client, to a fresh holder) 279,734, plus ≈77,000 for a second
  `Dispatch` the same results leaf carries and ≈94,000 for each after it (the client reads them all: a leaf of four
  543,668, measured by round 4's fact check, E4-FC8); `transferRemote` 186,022; kind 8 (the gate, no call) 154,571;
  kind 9 434,773 (each deletes its decision's record, so a transaction pays ≈126K and ≈411K after EIP-3529's refund,
  E4-FC1); the whole deployment 37,250,883 by the test's own count (step 7, `HlAdmin` with `HlAdminExec`, 9,571,977;
  `finishSetup` 286,201), an upper estimate, since it counts the script's own memory work (the local run's receipts
  after round 2 paid ≈35.1M for the fifteen creations, intrinsic and calldata included, and ≈1.3M for the sixteen
  calls). The run is fold F15e's, on the merged contracts: F15e moved only `HlAdmin`'s rows (step 7 +64,779 for its
  302 more bytes of code, `finishSetup` +4,706 for check 34's hold record, §5.8), and round 4's fact check reproduced
  every other figure exactly on the audited tree (E4-FC1). The machines' model (`agent/budget.go`: 1,448,600 for an
  update at 6, 311,300 a delivery) and the flag floats (§5.5) are sized on the earlier "tx" figures, so they err
  high, ≈5% on a header and ≈11% on a delivery. Sizes (F15e's build): `HlAdminExec`'s runtime 23,178 bytes, 1,398 under EIP-170; `HlAdmin`'s 19,495, and
  its creation input — `HlAdminExec`'s creation code inside, with its 896 bytes of arguments — 49,011 bytes, 141 under
  EIP-3860's 49,152: the deployment's tightest margin.
- Zero-knowledge light clients of Tendermint on Ethereum exist (sp1-ics07-tendermint, now cosmos/ibc-contracts
  v3.0.2: about 230K gas per verification with Groth16, audited by Zellic and Sherlock in 2025), but decode
  CometBFT protobuf, not tm2's amino; one for gno.land is a new SP1 program (gno-light-core's logic) and its own
  client contract — a later cost cut, not the launch: the launch verifies signatures in the contract, as
  Solana's client does, so its trust is the arithmetic alone.
- Glamsterdam (EIP-7773) activated on Sepolia on 2026-10-06 and has no mainnet date; it raises state-creation gas
  (EIP-8037: a zero-to-non-zero SSTORE measured ≈110.9k gas on Sepolia against ≈22.3k on mainnet), the calldata floor
  (EIP-7976) and the intrinsic gas, and brings ePBS (EIP-7732). So Sepolia's costs are not mainnet's: every gas figure
  here is mainnet's (Osaka), measured on a node pinned to it, and is measured again when Glamsterdam gets a date.
  Under EIP-8037 (Last Call to 2026-11-01) EIP-7825's 2^24 cap bounds a transaction's execution gas only: state gas —
  a creation's code deposit, 1,530 a byte — rides above it up to a tx.gas of 2^32 − 1, and `eth_estimateGas` counts
  both, so a creation leaving 24,000 bytes estimated 37,229,846 gas on Sepolia against 4,896,150 on mainnet, and
  `HlAdmin` with `HlAdminExec` ≈66M there at that slope (≈1,542 gas a byte) on today's sizes, before its
  constructors' fresh slots (E3-AS2, 2026-10-14, whose ≈61M was on the earlier sizes; E4-FC8); the sender's rule for
  it is §5.7's.
- Finality: `finalized` trailed `latest` by 88 slots (17.6 minutes) when measured; 64–95 slots is its normal range.
  A post-merge block's timestamp is its slot's (`genesis_time + 12·slot`), so no proposer bends the clocks.
- Calldata: 16 gas a non-zero byte, 4 a zero byte (EIP-2028), with EIP-7623's floor of 40/10 since Pectra for
  data-heavy transactions — an update is execution-heavy, so it pays the standard rate. More, with every source and
  command: `notes/research-ethereum.md`.
- `SELFDESTRUCT` (EIP-6780, since Cancun) deletes code only in the transaction that created it, so a deployed
  contract's code at an address cannot change: a contract not behind a proxy is immutable.

Hyperlane EVM (`@hyperlane-xyz/core` 12.2.0, 2026-10-02, commit ae1dfdd1; solc 0.8.33, EVM `cancun`, optimizer
10,000 runs; OpenZeppelin 4.9.3; the full compiler input ships in the npm tarball as `dist/buildArtifact.json`):
- `Mailbox.process(metadata, message)`: checks the version (3) and the destination, refuses a delivered id, picks
  the recipient's ISM (`interchainSecurityModule()` by staticcall, else the default ISM), writes the delivery,
  emits `Process` and `ProcessId`, then requires `ism.verify(metadata, message)`, then calls the recipient's
  `handle{value: msg.value}(origin, sender, body)`; a revert anywhere reverts the delivery with it. No reentrancy
  guard; the delivery mark comes first. Not pausable. The owner sets the default ISM, the default hook and the
  required hook, and can transfer or renounce ownership; nothing else.
- `IInterchainSecurityModule.verify` is not `view`: an ISM may write state while verifying.
- `HypERC20`: constructor `(decimals, scaleNumerator, scaleDenominator, mailbox)`; `initialize(totalSupply, name,
  symbol, hook, ism, owner)` mints `totalSupply` to its caller (0 for a synthetic, in the proxy's construction);
  `transferRemote(destination, recipient, amount)` burns from the caller and dispatches
  `recipient 32 ‖ amount 32` to the enrolled router; `handle` (only the mailbox, only the enrolled router of the
  origin) mints, and `bytes32ToAddress` reverts when the top 12 bytes are not zero. The owner enrolls routers, sets
  the ISM and the hook, and may set a fee recipient (since 10.0.0) and a fee hook (since 11.0.0) — `HlAdmin` never
  does either, and `finishSetup` requires both unset. No pause; no mint but `handle`, the initial supply, and a fee
  recipient's share (`_transferFee`) while one is set — never, here.
- `MerkleTreeHook` inserts every id dispatched with it as the hook — and the latest id again for anyone who calls
  its public `postDispatch` — so its index drifts from the mailbox's nonce; it refuses value, so a sender pays
  exactly `quoteDispatch`. `ProtocolFee` (`Ownable`) charges a fee up to an immutable maximum, pays the beneficiary on
  `collectProtocolFees()` (anyone), and its owner alone sets the fee and the beneficiary.
- Domains: Ethereum 1, Sepolia 11155111 (hyperlane-registry). Checkpoints bind the origin's MerkleTreeHook address:
  `domainHash = keccak256(origin ‖ merkleTreeHook ‖ "HYPERLANE")`.
- Hyperlane's own Ethereum mailbox is an older build (solc 0.8.19, 2023) behind a 6-of-10 Safe; we deploy our own,
  and "byte for byte" means against the 12.2.0 release's compiler input, immutables masked (§5.9).

## 3. Principles

As the Solana plan §3, every one. Principle 6 reads: all five upgradable contracts — the light client, the gate,
the mailbox and both tokens — stay upgradable by a 7-day decision; a kind 9 can hand every authority to a successor
admin contract; only `HlAdmin` is fixed, which on Ethereum is the default for plain code.

## 4. Architecture

| | At launch (the governor's `Launch`; GovDAO's from the yield) | After Proposal 2 |
|---|---|---|
| gno.land → Ethereum accepted by | `GnoLightClient` (more than 2/3 of the current validators' power) behind `PausableGate` | same |
| Ethereum → gno.land accepted by | this deployment's hlverifier (2 of 3 signers, ed25519) | the sync-committee ZK verifier realm (§7), at once; the signers alone, after 7 days (the fallback) |
| Ethereum controls held by | `HlAdmin`, acting on proven decisions only | same |
| gno.land settings held by | the governor until the yield, GovDAO after | GovDAO |
| Pause | 1 flag pauses; 2 guardians hold (Ethereum) / GovDAO unpauses at once (gno.land) | same |

gno.land realms (a second deployment of the Solana bridge's six, `eth/realm/` here beside hlgnot2, the reference
successor realm, §5.1–§5.3):
`gno.land/r/<namespace>/eth/hlmailbox`, `…/eth/hlverifier`, `…/eth/hlgnot`, `…/eth/hlbubble`, `…/eth/hlfees`,
`…/eth/hlgov`, importing the Solana deployment's pure packages `gno.land/p/<namespace>/hyperlane/v0`,
`…/decisions/v0` and `…/hlroute/v0` unchanged (they hold no state, and on mainnet they are already deployed and
immutable; their one Solana-specific helper, `p/hyperlane`'s base58 `ParseKey32`, is not used for Ethereum, whose
addresses the Ethereum realms parse themselves). The `eth/` path segment keeps the two deployments apart under one operator
namespace; the dev tree writes the namespace `g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr` and the stage step rewrites it
at deploy (as `bridge/launch/stage.sh` does for Solana) — a step not built for Ethereum yet (E3-CF7: only the local
run's `eth/bridge/local/e2e.sh` swaps the placeholders, for its own chain; an open build item before Sepolia, §10),
while on gnoland-1 every realm's `init` refuses a placeholder left in place (§5.3).

Ethereum contracts (§5.8), sixteen deployments: `ProxyAdmin`; `Mailbox` (proxy and implementation);
`MerkleTreeHook`; `ProtocolFee`; `HypERC20` GNOT and BUBBLE (proxies and implementations); `GnoLightClient` (proxy
and implementation); `Ed25519Verifier`; `PausableGate` (proxy and implementation); `HlAdmin` and `HlAdminExec`
(E3-FC6).

The two locks are this deployment's own: GNOT sent to Ethereum is locked in `…/eth/hlgnot`, never in the Solana
route, so each chain's synthetic is backed by its own lock and a fault on one side touches only its own. Moving
synthetic GNOT from Solana to Ethereum is a burn to gno.land and a send onward.

## 5. The launch — gno.land's side, the signers for the way back, the Ethereum deploy

5.1 hlmailbox: as the Solana plan §5.1, with the launch origin Ethereum's domain (1 on gnoland-1; the compiled test
domain elsewhere, 31337 in the tree, rewritten to 11155111 for a Sepolia deployment). The `Dispatch` event is the
same, emitted under this deployment's hlmailbox path, which the light client on Ethereum compiles (§6.2); a message
the Solana bridge's mailbox dispatched is never this client's (its `pkg_path` differs) and never Ethereum's (its
destination differs). `Dispatch`, open to anyone, refuses a body above 2,048 bytes (`MaxBody`) before anything is
stored or emitted — 32× a token message's 64 bytes, 4× a decision's 512 — and the pages the machines read a message
by write its hex between plain quotes, byte for byte what they wrote before, so `msg/<nonce>` costs at most ≈17M
query gas and a `msgs/` page of twenty ≈178M against gnoland-1's 3B (fold F10, measured on gnodev v1.2.0 and
v1.5.1): a stranger's ≈50 KB body had put its `msg/<nonce>` out of query gas and stopped, for good, every walk that
reads each nonce in order — the relayer's, the signers', the monitors' (E3-RL1). hlmailbox's `json`, which the
machines read for the signers, the seats, the routes and the flags, is written the same way (≈220M on a v1.5.1 node at
its bound — 255 keys, every guardian's `payouts` flag naming 256 ids — and ≈285M in the harness's larger state, where
it had passed 3B). The realms' suite holds every page the machines read — hlmailbox's `json`, `msg/`, `msgs/`,
`decisions.json/<from>` and `delivered/<id>`; a route's `json`, `waiting/<from>`, `claim/<id>`, `send/<id>` and
`decisions.json/<from>`; r/hlgov's `json`, `decision/<n>`, page and `decisions.json/<from>`; r/hlfees' `json` and
`decisions.json/<from>` — at its bound under a third of the 3B at its node figure (`eth/scripts/gnotest.sh`'s
render-gas gate, twenty probes, each required: the probe's figure in the harness times its page's node factor,
measured on a gno v1.5.1 node — but hlgnot2's `json`, which takes the routes' larger `json` factor, hlbubble's, never
measured on a node — since the harness charges an object's load once where a node loads every object of every query,
and counted as little as about a tenth of a node's gas (`delivered/<id>`: a factor of 9.55); a probe with no factor
fails; E3-RL7, E4-RL2, E4-FC3, E5-RT4). The largest on a node: the longest decision's page ≈0.84B (hlmailbox's cancel
of a 255-key rotation, ≈33 KB of text alone on its page), the fullest decision pages ≈0.37–0.41B, a route's
`waiting/<from>` ≈0.22B. A verifier GovDAO activates for an origin (`set verifier`) refuses by a returned error,
never by a panic: `Process` quotes a returned refusal (ASCII, at most 64 bytes of it), but `Verify` runs under the
verifier's own realm, so a panic in it crosses a realm boundary on its way out and aborts the transaction as written —
no realm can catch or quote it — and would put the caller's bytes into the block's results (§9); hlmailbox's
`set verifier` description says so, and the members read it in the verifier's source before the vote (hlverifier
refuses only by fixed-text errors; E5-RL4).

5.2 hlgnot and hlbubble: as the Solana plan §5.2–§5.4. Their recipient on Ethereum is an Ethereum address, written
`0x` and 40 hex digits (any case; mixed case must be the EIP-55 checksum) or as 64 hex digits whose first 24 are
zero; it is carried left-padded. Refused at send: a 32-byte recipient whose top 12 bytes are not zero (Hyperlane's
`bytes32ToAddress` would revert the delivery for good), the zero address (an ERC-20 refuses to mint to it), any
address at or below 0xffff (the precompiles, 0x01–0x11 and 0x100), and every address the Ethereum deployment created —
its sixteen: the five proxies and their five implementations, `HlAdmin`, `HlAdminExec`, the `ProxyAdmin`,
`Ed25519Verifier` and both hooks (E3-FC6) — since `HypERC20` mints to
any other non-zero address and synthetic minted to fixed code can never move (the Solana plan §5.7's "a realm address
as a recipient on either side", carried over). The site and the tools accept only EIP-55 checksummed input: an
address typed in one case is refused and its checksummed form never offered (that form is a typo's checksum too),
and a wallet's own account is read as the wallet gives it; the site refuses as a recipient every realm of both
deployments, their launch hlverifiers and the mailbox's active verifier included, and the sixteen its configuration
names (E3-CF6). The
router is the `HypERC20` proxy's address. A credit whose recipient is one of this deployment's realms (its
hlverifier included, and every realm that is or was an origin's active verifier — Proposal 2's once a decision
activates it, hlmailbox recording each at that decision's `Execute` — which is refused too as a send's recipient and
as a vote's payee, E4-RL5), one of the Solana deployment's six, or the zero 32 bytes goes to the sweep queue, never to
a claim nobody can collect. A realm that announced itself to hlmailbox as a route's successor — from its own `init`,
as hlgnot2 does, so from the block it exists — is a realm of the bridge from its announcement, registered by a
migrate yet or not: refused as a send's recipient, swept at a credit, refused as a vote's payee; and a credit or
sweep vote asks its payee again when its proposal executes and when its 7 days end, one whose payee became a realm
of the bridge in between staying pending for GovDAO's cancel (E3-RL3, E3-CF7). Every one of these addresses is a launch value on its own line, a
placeholder the stage step swaps for the deployed address, as the Solana realms' base58 placeholders are.
Each route keeps an index of its waiting claims beside p/hlroute's state (`waiting.gno`, the same in the three routes
but its package line, which the suite holds): every claim open on the route, once, at the place it took when it became
open — its credit (`Process`), a restore, a credit by vote, a migration's page adopted — a claim open again taking a
new place above every place given before; an entry whose claim left the queue (paid in full, voided, gone with a page)
is listed in its state until a migration's page takes it out or the tidy each `Process` makes (the next two
entries, wrapping) finds it. `waiting/<from>` serves a hundred entries from any place at the cost of a seek and the
page — ≈219M gas on a v1.5.1 node at 2,000 waiting claims, the first page and the deepest alike — where p/hlroute's
`claims/<from>`, shared with the live Solana routes and immutable, skips the claims before <from> by walking them
(≈1.6M gas each on a node: out of a query's 3B past ≈1,600 waiting claims, which a mass exit, or a stranger's
fee-sized burns to fresh addresses, can make; E4-RL1). The index costs a credit ≈6.8M gas (fold F15d's measure,
before fold F17c made an entry three writes where it was four) and ≈0.42 GNOT of storage more — a credit's whole
deposit ≈1.70 GNOT with it (§5.5) — and `Claim`, the payouts' path, nothing.

5.3 Governance: as the Solana plan §5.3, every builder, every timing rule, the governor and the yield, with two of
its figures changed in the Ethereum routes. A migration hands every claim, the sweep entries and the ids of
undelivered sends to the successor in pages of 50 records the successor pulls (`pullPage`, in the file the three
routes keep the same, `waiting.gno`), never p/hlroute's 500: a page of 500 open claims, each leaving one waiting index
and entering the other, was 5.28B gas on a v1.5.1 node, past gnoland-1's block, and a migration stalled at a page
freezes the route for good — the predecessor refusing everything but a re-point or a cancel, the successor crediting
nothing before its last page (E5-RL1). A page adopted whole is ≈0.54B there at a route of 100 open claims and ≈1.04B
at its costliest in a backlog of 3,000 — a page's cost grows with the route, and past ≈20,000 open claims a page of 50
would near half the block by the measured growth (not measured there) — and the realms' migration gate (`gnotest.sh`)
holds the predecessor's `Pull` and the successor's `Adopt` of a page under a third of the block at their node figures.
And a void's, a sweep's, a refund's and a write-off's lists name 256 ids at most, as p/hlroute's do (a credit names
one), but a restore's here 128 (`maxRestoreIDs`): its claims enter the waiting index in the same transaction, and a
256-id restore at once through the governor's `Govern` was 3.69B on a v1.5.1 node where 128 is 1.93B, so a 256-id
void is restored by two decisions (E5-RL2). On gnoland-1, every realm's `init` refuses a launch value still equal to
the dev tree's placeholder — the signers' keys, the seats and watch keys, every Ethereum address, the routers,
`HlAdmin`, the Solana deployment's namespace, the governor — so no stage step that misses one can put public test keys
in charge (E2-RL1); and on every chain the realms refuse at deploy a launch set no launch can run — a signer rule its
keys cannot meet (the empty set among them), a key in two
seats on either chain's list, an Ethereum seat at the zero address, a treasurer in any spelling but gno's canonical
one — and on gnoland-1 an empty governor or an empty seat (E3-RL2), and anywhere two of the sixteen contracts at one
address (E3-CF12).
r/hlgov's `set decision
recipient` may name a former recipient again (a swap made too early can be undone) and names the recipient it
replaces, executing only while that is still the recipient (a stale or pre-armed swap stays pending until GovDAO
cancels it), and never a key that holds an Ethereum seat; kind 9 never names a former recipient. Kind 8 may roll a proxy back to its own launch implementation.
Kinds 5, 7 and 8 refuse the current and former recipients' addresses and the deployment's sixteen. Every typed gno
address is compared in its canonical spelling (gno accepts four spellings of one address — lower and upper case, and
bech32m in either — so a check by text alone could be passed by another spelling). Builders refuse
every address of the Ethereum deployment as a kind 8 implementation, a kind 9 successor or a new recipient (the launch
`HlAdmin` aside, which a swap back restores, E2-RL2; E4-FC8), and kind 6 refuses Ethereum's own domain and gno.land's
other domain. The cancel builder writes the parsed numbers and ranges as its description, never the proposer's text. r/hlgov's
builders are §13's Ethereum kinds; its parameters are checked against `HlAdmin`'s own rules where a realm can read
them (an address's form, a fee against the compiled maximum, a body of at most 512 bytes), and what a realm cannot read
(a proxy's implementation, a contract's code hash, a successor's state) is read by the proposer and by `hlwatch`
before the vote, which prints "immutable" or "MUTABLE: would be spent" beside a kind 9 (as the Solana plan §8.4; here §6.5 and §8).
The builders title their proposals "eth/<realm>: <kind>", the Solana deployment's staying "<realm>: <kind>", so
GovDAO's list tells the two deployments apart (E3-RL6). Every realm with a ledger renders `decisions.json/<from>` —
the decisions numbered from <from> on, at most 20 and none past 16 KB of their text after the first (a page's first
decision is always taken: the longest, ≈33 KB, sits alone on its page; E4-FC3), each as `decisions.json` writes it,
"[]" past the last — and the executor, `hlwatch` (the monitors' view: the monitor proper reads no ledger, E4-CF3) and
the status site read the ledgers page by page until an empty one (a page of fewer than twenty is not the end; the
site reads a realm without pages whole, and names each realm whose ledger does not read apart, E4-TL7), since
`decisions.json` whole passes `vm/qrender`'s 3B near 360 decisions (E3-RL4). r/hlgov's own page, where `hleth audit`
reads the Ethereum seats, lists its newest twenty decisions and counts the rest; the other realms' pages and their
`decisions` still walk the whole ledger, and no machine reads them (E4-RL2). A refund of a successor's imported sends
waits on the token's enrolment on Ethereum, as on Solana, which no realm can see: `hlwatch` reads the token's
enrolled router for gno.land and the kind 6's state on `HlAdmin`, alarms NOT ENROLLED ON ETHEREUM on a refund
proposal, on GovDAO's apply and on every queued refund until it applies, and prints DELIVERED beside an id Ethereum's
mailbox processed already, a refund of which would mint it twice (E4-CF5). Every proposal of these
builders is a `MsgRun`, which gnoland-1 admits from three keys (§1, §11.9; E3-RL5).

5.4 Caps and claims: as the Solana plan §5.4, the gate's mint limits included (kind 12 on `PausableGate`, the same
launch figures: 25,000 a message, a day cap of the smaller of 100,000 and a tenth of the expected lock). `hleth setup
-expected-lock-gnot` and `-expected-lock-bubble` (required on every chain but a local one) compile each token's gate
limits — the gate keeps a pair a token — the day cap the smaller of 100,000 and a tenth of that token's expected
lock, the maximum the smaller of 25,000 and the cap, and refuse a maximum below the route realms' compiled one
(`-launch-maximum` when GovDAO lowered them to match); `hleth audit` holds the limits to the same expected lock
(E3-CF4). r/hlgov's kind-12 floor — a maximum never below the route's on gno.land — reads the launch routes'
maxima for the realm's life (`hlgnot`'s and `hlbubble`'s), so after a migration it compares with the predecessor's
frozen figure; the monitors' and `hlwatch`'s `limits:mismatch`, which follow a route's line to its live realm, are
the guard then (E3-CF7). Before the vote, `hlwatch` reads a route-maximum proposal against the gate's maximum for the
token and alarms RAISE ABOVE THE ETHEREUM MAXIMUM — vote the kind 12 first — since a send between the two is taken on
gno.land and parked on Ethereum (`OverMax`) until a kind 12 raises the gate's (E4-CF5). A claim's installments
(E5-GS1): p/hlroute's `Claim`, shared with the live Solana routes and immutable, pays a claim's first installment only
when the meters hold the fee, whole, but every later one as soon as they hold anything, and a recipient's meter, empty
at its first credit, refills a quarter of the cap a day (2,500 GNOT at the launch floor, ≈0.03 a second) — so a claim
that outran it was paid a sliver at every call, and the relayers called every tick. Each route's `Claim`
(`installments.gno`, the same in the three routes) runs p/hlroute's in turns of one claim and pays a later installment
only when the bridge meter and the recipient's meter both hold at least min(what the claim still owes, the fee) —
one bounded by the lock alone excepted, once — passing a recipient whose meters hold less over, its meter refilling for
a later turn; a `Claim` that only passes slivers over pays nothing and succeeds, and one that pays costs ≈1.5% more gas
than p/hlroute's alone. A recipient its last installment emptied is payable again after min(owed, fee) ÷ (cap/4) of a
day: ≈11.5 minutes for the 20 GNOT fee at the launch cap; the relayers pace their `Claim`s to it (§5.7).

5.5 Fees, minimums, who pays. As the Solana plan §5.5 on gno.land, with Ethereum's costs:
- gno.land → Ethereum costs the sender the route's fee and ≈0.32 GNOT of gno.land storage deposit for the send's
  records (measured on a v1.5.1 node at gnoland-1's 100 ugnot a byte; E5-RT5), and costs the operator one delivery
  (`Mailbox.process`, gas measured by E-M2b) and, for every gno.land block holding a waiting dispatch, a share of one
  header update: the signatures of more than 2/3 of the
  power — five today — measured at 1,382,273 gas an update and 279,734 a delivery (§2, what the transactions pay):
  ≈$0.56 for both at the 30-day median (0.13 gwei base fee plus 0.01 priority, ETH $2,417 on 2026-10-14), ≈$4.0 per
  gwei, ≈$100 at the 30-day maximum (24.9 gwei). A route has one fee for both directions — the shared `p/hlroute` keeps one figure, the send's
  fee and the claim's first installment, as on Solana — so the Ethereum routes' launch fee is set by the costlier
  direction: proposed 20 GNOT (≈$1.44 at $0.0722) and 400 BUBBLE each way, minimums 40 GNOT and 800 BUBBLE — BUBBLE's
  figure is the GNOT fee at the Solana routes' ratio (100 BUBBLE to 5 GNOT, set from BUBBLE's GnoSwap price when those
  realms were compiled), to be re-read against the pool when these are (§11.5 is Jae's); this plan states no BUBBLE
  price, so what 400 BUBBLE is worth is unknown until the launch notice prints the pool's price beside it (E4-FC2). A
  send in a batch of four costs ≈0.89M gas measured — a quarter of the header (≈346K) and its delivery, which reads the
  whole leaf of four (543,668) — and ≈0.98M by the budget's model, which decides (E3-FC2, E3-AS6, E4-FC8), so 20 GNOT
  covers it up to ≈0.61 gwei of base fee and tip (≈0.67 at the measured cost; ≈0.49 at §2's GNOT/ETH four hours
  later); 10 GNOT would cover it only to ≈0.31, below the 30-day mean (0.38). The operator's
  spending on route deliveries is a budget funded by the routes' fees — an accounting rule, not a flow (E3-RT6): the
  fees are GNOT and BUBBLE paid into r/hlfees, which only the treasurer withdraws, and the ETH that buys headers and
  deliveries is the updater's and the relayers' keys' own, the same keys that carry GovDAO's decisions' headers,
  deliveries and executes. What the code guarantees: the updater buys a header for route sends only while the fees
  of the sends it serves — each its own fee, as its `SentTransferRemote` event carries it (one whose event the scan
  never read at its route's fee now, E4-CF6) — cover the whole cost (the header's and every delivery's, each delivery
  priced by its results leaf: the model prices a delivery from a leaf of n `Dispatch` events at 311,300 +
  101,000·(n−1) gas and 34 a byte past their ≈451 each, above the measured ≈280K, ≈77K more for a second event and
  ≈94K for each after it; E3-GS2, E4-FC8), valued at the GNOT/ETH and BUBBLE/ETH rates the
  configuration must carry (the machines refuse to start without them, so the rule never silently lapses) — or, at a
  send's six hours, within the surplus earlier fees left, a deficit drawing nothing (below) — and it books the header
  against those fees, its state saved, before it sends it (E3-GS5), naming the route sends it books in a trailer of
  the header's calls (the nonces, their count and a mark: bytes past the client's arguments, which the client never
  reads, ≈200 gas of calldata a send) and settling the booking by the trailer of the transaction that recorded the
  header: each send credited and debited once whatever headers name it later, and a send once booked adding nothing to
  a later header's due rule (E4-GS2, E4-GS4, E4-CF2). The relayer delivers at no fee check a send only from a header
  that `eth.updater`'s key recorded with a trailer naming it (any of the send's proven events, from the cheapest leaf
  among them), and any other send — under a stranger's header, the backstop's, a decision's or a validator change's
  (which books no route send: a send in its block rides it only by its own fee), or the updater's bought for other
  sends — only while its own fee covers its delivery from the leaf built, else the send is held and waits as an
  unproven one does (E3-GS2, E3-GS7, E4-GS2). Both machines read the bookings from the chain — the client's
  `HeaderRecorded` log; its transaction, whose sender is recovered from its signature, never taken from a provider's
  `from` (E5-RT3), and which, when that sender is `eth.updater`, must be a call of the client carrying that header, its
  receipt the recording (successful, the header's log in the log's block); and its trailer — and keep them
  in their state files: a booking whose log lies above the finalized block is provisional, read again every minute
  until the finalized block passes it and then kept for good, so a reorganisation that moves a header's recording to a
  stranger's transaction moves both machines' reading with it (E5-GS3) — a read that misses it changing nothing while
  its block lies above the finalized one, and after that only two misses of its receipt a minute apart ending it. A
  header's log is looked for from its gno
  block's time on (at the chain's own block spacing, measured, with a twentieth and an hour to spare), so one bought
  hours after its block is found in a provider's recent logs; then over the last three days, at most hourly; and further
  back (to the machine's last pass less three days, never before `eth.deployBlock`) only after a gap in its own passes
  longer than that; a header whose log is not found yet books nothing for now and is asked again a minute later. The
  updater settles its own header from its own recording transaction — the receipt carrying the log, the calldata its
  key signed, the trailer — needing no log, and a recorded header whose recording is not read stands, alarmed
  `bookings:unread` after an hour, never dropped and bought again (E5-GS2); it drops a pending booking for a header
  unrecorded only on the finalized block's word — not recorded there, and the client's epoch there past the booking's,
  or (an update) its trusted height there at or above the header, or nothing of its key in flight since a moment the
  finalized block's time has passed by two minutes while no step has asked for that header for 15 minutes (E4-CF6,
  E5-GS3). So the relayer's and the updater's provider must serve the client's logs of the last three days: each
  machine reads three days of them at its start and every six hours, alarmed `eth:logs-depth` when they do not read,
  and alarms `eth:logs` on a log read that fails, cleared only by a read reaching as far back (E5-GS2). A header's
  price is the measured model (≈221K a signature plus the rest), never a stranger's receipt. What it does not: move
  any fee into the keys (the operator tops them up, alarmed at 7 days of runway); cap a key's daily route spending;
  price BUBBLE from its pool or with a haircut — the rates are the operator's, dated by `eth.pricesAt` and alarmed
  (`prices:stale`) while undated or older than a week (E3-GS11, E3-FC7; §8), a week that is long against GNOT's moves
  (GNOT/ETH ≈20.5% lower four hours after §2's reading): while a stale rate over-values the fees, the operator's ETH
  pays the difference unalarmed (E4-AS2); or keep a decision float route traffic cannot spend (§9, §11.4). What the
  fees cannot cover
  waits, oldest first, and moves at the heartbeat within the budget; a waiting send older than 6 hours buys its own
  header within the budget whatever anyone else's updates did (no stranger can keep the client "fresh" past a waiting
  send: below the trusted height too, where a message a stranger's update left after one re-emit, and a decision at
  once, is proven by a header proof of its block's next header, which no later update pre-empts — E3-RT4, §5.7), a
  stranger's `Reemit` after that header changing nothing, the send being delivered from the header booked for it
  (E4-GS4), and a delivery-age alarm fires at 7 hours, a held send's too. A stream of minimum sends therefore pays for its own
  deliveries or waits behind them; it can delay others by its volume, at its fee per send (320 minimum sends, ≈$460
  of fees, delay others by about three hours at the 30-day mean, where every batch of four is covered and moves at
  the updater's one update in two minutes — an estimate, not a measurement; E3-FC2, E3-RT9), and its cost is booked
  against its own fees, while the ETH still leaves the keys for the operator to refill. Anyone may deliver their own
  send by paying Ethereum's gas (`Mailbox.process` is permissionless) — after carrying the header past it (an update,
  ≈1.38M gas, or `proveHeader`, ≈0.87M) unless someone's update already did; no tool of ours builds it and the site
  does not offer it (E3-FC5, E3-RT9). Route deliveries park while the base fee is above `maxBaseFee` (a config, default 5 gwei) and resume
  when it falls: a spike delays and never strands. Decisions to `HlAdmin`, the headers they need, `HlAdmin.execute`
  and the watch's and guardians' flags never park under `maxBaseFee` (a separate emergency cap, default 50 gwei, which
  the updater's float must cover: 1.45M gas — the model's header at gnoland-1's set; the updater's and the relayers'
  own floors read the set after the client's trusted header, E3-GS11, the monitor's mirror of them gnoland-1's set or
  `eth.updateGas`, E4-FC8 — × 1.2 × 2 × 50 gwei ≈ 0.174 ETH: the updater's floor, alarmed `float:updater` below it,
  E4-FC5 — an alarm, not a stop: route headers are not stopped there, and a decision's header the updater's key cannot
  pay the relayers' backstop buys; a floor route traffic cannot spend is §11.4's, Jae's to ask for, E5-CF2): GovDAO's
  cancels land in a spike.
- Ethereum → gno.land costs the burner Ethereum gas for `transferRemote` (186,022 gas measured, cold — cents at the
  median) and costs the operator gno.land gas for `Process` and `Claim` and the storage deposit a credit's `Process`
  locks from the relayers' gno key at gnoland-1's 100 ugnot a byte: ≈0.9–1.7 GNOT, measured on gno v1.5.1 nodes (0.87
  for one claim to a fresh recipient; 1.28 a credit in batches of 200 before the waiting index, 1.70 with it) — the
  Solana plan's ≈0.01–0.03 GNOT was an unmeasured guess — none of it coming back to the key (a paid claim keeps its
  record, and a release refunds the storage fee collector while ugnot is restricted, its caller only once it is not):
  ≈1.8 GNOT a credit with its gas (E5-GS5, E5-RT5). The route's one fee is taken as the claim's first installment and
  goes to r/hlfees, never to the key. A burn of amount 0 is skipped by the signers and the relayers, as the Solana
  relayer skips one, and a burn below the fee of the realm that would credit it is not processed by the relayers: the
  route would sweep it — owed, paying nobody, no fee taken — while its deposit would be the relayers' (≈300–400 burns of
  one base unit, ≈$0.06 of Ethereum gas each, would have emptied the key's 500 GNOT and stopped every credit until a
  top-up); it stays pending in the relayers' state, re-checked against the route's fee each tick without a read and
  delivered if a decision lowers the fee to it, alarmed `inbound:dust:<symbol>` until a day after the last, and anyone
  may `Process` it at their own cost, the signers signing it as any burn (E5-GS5; the signers and the monitors keep it as
  known dust, §5.6).
- The mailbox's protocol fee (kind 7) starts at 0 under `ProtocolFee`'s immutable maximum of 0.001 ETH (§1), its
  beneficiary the treasurer's Ethereum address.
- Floats: the relayers' ETH key (primary and backup share it), the updater's ETH key, the relayers' gno key
  (≈2,000 GNOT: ≈90 GNOT a day at the planned 50 credits, so ≈22 days, where the 500 GNOT once planned lasts ≈5.6 and
  alarms from launch day; refilled by the treasurer from r/hlfees, each credit's 20 GNOT fee covering ≈11 credits'
  deposit; E5-RT5), the guardians' and watch keys' ETH (0.05 each for flag transactions); the first three, and the
  monitor's own watch key, monitored at 7 days of the trailing rate (the planned rate on launch day, by the model at
  0.2 gwei: the relayers' 50 deliveries a day; the updater's 30 headers a day, ≈0.0087 ETH, so the ≈0.2 ETH above its
  floor lasts ≈23 days, E3-CF8, E4-FC5; the gno key's 50 credits a day at ≈1.8 GNOT, E5-RT5), and the six keys of
  the gate's seats and slots held to the 0.05 ETH a flag key keeps (`float:key<n>`), never a runway (E4-FC8). The
  relayers' key is a second payer
  of decisions' headers: its backstop buys a waiting decision's header — a header proof below the trusted height —
  five minutes after the decision's dispatch when the updater's key cannot pay for it, and thirty minutes after it
  whatever the updater's float (E2-RT8; timed from the dispatch, which no stranger's `Reemit` resets, E3-RT7), so its
  float holds one header at the emergency cap, ≈0.174 ETH at gnoland-1's set, on top of its deliveries (E3-CF8;
  §11.4) — a floor alarmed on its own, `float:relayer` on the relayer machine and on the monitor, since its runway
  alarm reads the deliveries and one such header is weeks of them (E4-RT1). No ATA payers. A flag on Ethereum is one
  transaction per object for a plain key (an account without an EIP-7702 delegation cannot touch the gate and
  `HlAdmin` in one transaction as Solana's two instructions did; a delegated key or a contract wallet can, E4-FC8),
  97–112K gas each by the GasReport's double-counted "tx" column and ≈60–92K in a real transaction (the gate's first
  flag 91,384, later keys' less; `HlAdmin`'s 62–85K; E3-FC6, E3-AS6, E4-FC1), so the watch never re-asserts
  a flag on a timer: it re-sets one only when it finds it cleared, or when `HlAdmin` records an unpause, on both
  objects, once that block is final (on the gate's 112,448 the floats are sized on, which errs safe, 0.05 ETH covers ≈270 flags at the 30-day p95 and still sends one at its 24.9 gwei
maximum — a key holding 0.01 could send none above ≈37 gwei, the sender reserving 2× the base fee on 1.2× the
estimate; a once-a-minute re-flag would
  empty it in hours).
- Header updates are bought only when they buy something (§6.1's cadence): when waiting sends' fees cover it and its
  deliveries, when a send has waited 6 hours (within the budget), when a decision waits, and when gno.land announces a
  validator change; never every 10 minutes as on Solana, where an update costs a cent.

5.6 Signers: as the Solana plan §5.6, with Ethereum's reading rule. A burn is a `Dispatch` log of our mailbox whose
sender topic is one of the two token routers, whose destination is gno.land's domain and whose recipient is that
token's route realm, in a transaction that also carries that token's `SentTransferRemote` of the same amount; nothing
else is counted, indexed or signed — `Mailbox.dispatch` is open to any contract, and a stranger's dispatch counted
as a burn in flight would stop every signer for good (the Solana scanner reads per token program for the same
reason). The monitors and `hlwatch` use the same filter. Each signer reads Ethereum through two RPC providers that
must agree on the `finalized` block — its hash, its time and its parent, at the lower of their two answers, the two
never more than two epochs apart (the cut judges by its time, below; E4-GT1, E5-RT1) — on every such log up to it
(compared byte for byte, each log's block hash checked against the canonical hash at its number), and on each token's
`totalSupply` at that block (`eth_call` pinned by block hash); no pair of providers shared by two signers and no
provider in more than two (`ProviderRule`, which three providers can meet; four across the three signers is the
operator's plan, as on Solana —
E3-FC6). A signer's and a monitor's two providers must each serve full-history state and logs from
`eth.deployBlock`, where a fresh install's scans start: drpc's free plan answers `eth_getLogs` for about 100 blocks a
call (its refusal is now halved like the others' — set its `logRange` to 100; the status site, which reads Ethereum
through a client of its own, not `ethx`, halves its window on drpc's HTTP-400 refusal too, E4-AS3) and its
documented free quota (≈10M calls in 30 days) is below a signer's steady ≈17M a month (≈20 calls a 3-second round),
so a paid plan or a 12-second round; publicnode keeps about 100 blocks of state and refuses logs 10,000 blocks back, so it is never a
signer's or a monitor's provider, nor a guardian's `hlwatch` past the deployment's first days, nor the relayers' or
the updater's, which read three days of the client's logs (§5.5; E5-GS2); and independence is by operator, which no
configuration shows (ethereum-rpc.publicnode.com and ethereum.publicnode.com are one) (E3-AS4; §11.10). A provider's
`batchSize` is the most calls one JSON-RPC batch to it carries — 100 when unset, a pair taking its two providers'
smaller, halved for the process's life when the provider answers a batch as too large (geth refuses one past its
`--rpc.batch-request-limit`, 1,000 by default) — and every read of many views is split by it (E5-GS4). Once a client
may prune the deployment's first logs (EIP-4444's ≈2 months, §6.2), only a provider that keeps the history serves
those scans (E3-FC8). A signer signs a burn only once its block is at or below `finalized`, and
`finalized` has a floor: the highest finalized block a signer or a monitor read at, kept in its state, so a pair
answering below it — a provider stepped back to an older real block — is no verdict, never a reading, alarmed
(`eth:finalized-floor`) at the fifth round in a row on a signer and after two minutes on a monitor (E3-GT9). It signs
Hyperlane's checkpoint digest with this bridge's own convention, as the Solana signers do — origin 1, our
`MerkleTreeHook`'s address as the hook, root 0, index the message's nonce (not the tree's index, which drifts from the
nonce) — with its ed25519 key; hlverifier checks neither root nor index, only the signatures over this message's id. The refusal
rule and the ordered cut are the Solana plan's, Ethereum read at `finalized`: the snapshot is the finalized block F
(each token's supply at F, the client's trusted height T at F, the dispatches at or below F), then the own gno node
(the configured one, §1; E3-AS5) at a height at or above T; the in-flight terms count, beside the burns, the sends
locked on gno.land — the messages a route realm of the token's line sent, never a stranger's `Dispatch` to a token,
which the token refuses and nobody refunds (counted, it stood in flight for good, halting every signer and flagging
both directions for one transaction's fee: E3-RT1, E3-CF1, E3-GT1) — and not yet
delivered on Ethereum (subtracted from the lock's room), since on Ethereum a send waits hours by design and every hour
of it would otherwise be room for an unflagged forged mint. A burn above 2^61 base units, past what gno.land counts,
is neither counted nor signed. A burn below its route's fee, which the relayers leave unprocessed (§5.5), stays in
flight and is signed as any burn, but the signers and the monitors keep it as known dust: its delivered mark is not
read again every round — a mark is re-read when hlmailbox's delivered count moves, every ten minutes, after any round
in doubt, and within the round whenever a token reads BROKEN with a delivery unplaced, a large batch read as
hlmailbox's delivered ids a hundred at a time — it raises no age alarm, a dust burn someone else delivers is noticed, and it is judged again when the route's fee changes (each dust burn
had cost every signer and monitor one render a round for good, and held the monitors' age alarm up: fold F17a's
review, the integration lane I3). The cut's order — F at or above the block of every burn credited in the gno state read
— is checked against the burn index, scanned only to the pair's finalized block, so hlmailbox's delivered count less
the index's credited burns is the deliveries the index cannot place: an honest burn above the index while one of the
machine's providers trails the signers' (a lagging backend, or a liar answering an older finalized block), a forged
credit, a credit by vote. While any is unplaced and the lag is not ruled out, a BROKEN reading is no verdict: it is
kept in the machine's state and judged once the index's finalized block's time has passed the reading's, the honest
lag it carried (the burns credited at or below its height whose block lies above its F) taken off its excess, and a
break that stands then halts the signer and flags on the monitor; meanwhile a signer signs nothing (alarmed at the
fifth round in a row) and a monitor alarms `identity:<token>:pending` after two minutes and at flag level after an
hour, flagging nothing on one provider's word (E4-GT1). That wait is bounded by what both providers show above the
index, each to its own head while the head is fresh (no older than two minutes, at most 600 blocks above the index):
the token's burns the index holds above the reading's F that the cut's state credits, and every burn of the token
either provider shows above the index that the state credits (or whose mark did not read; 200 marks at most a cut),
sum to the most an honest lag can carry, a burn's token and amount read from its message, which its id binds, never
from a log's topics — so a liar can only add burns, which widens the bound, and hides none the honest provider shows;
an excess past it is a break at once, the signer halting and the monitor flagging without waiting the finality lag (a
forged credit paid out in its own block, `Claim` being anyone's, sits in no waiting claim for the dispatch check
below to see: E5-GT1). The reading's height is the own node's head read after the cut, raised to the highest credit
the cut read, so a head answered from a backend a block behind the one that served the state never leaves an honest
credit out of the lag (E5-RT6) — sound on one node only, which is why the own node is a requirement (§1, §11.10). A
monitor rules the lag out at once when every unplaced delivery was made by a height whose time the index's finalized
block has passed (it keeps samples of hlmailbox's count), and judges a waiting claim forged at once when neither
provider, each current to its head, shows a dispatch of its id above the index (§5.9); a forged mint on Ethereum
beside an unplaced delivery waits the finality lag (13–19 minutes) only within that bound, or while what the providers
show above the index is not known. The ahead-stop (the Solana plan §5.6's client ahead of its chain) is kept in the
signer's state, so a
restart never lifts it (E3-GT6). A finality stall alarms `eth:finality-stall`. On a signer it is the finalized block
standing still for an hour, by a clock kept in its state, so a restart never restarts it; the signer signs nothing
newer meanwhile, a pending reading waiting with it: for the signers, liveness, never safety. For the watch it is not
liveness alone (E5-RT1): every Ethereum-side rule of a monitor and `hlwatch` reads at the pair's finalized block — the
supplies, the client's header record, `HlAdmin`'s decisions, the stages, the proxies' slots — and sees nothing newer,
while the light client keeps accepting headers on inclusion and deliveries mint. So a reading whose finalized block is
more than half an hour old by its own time (Ethereum finalizes 13–19 minutes behind its head) is blind — said, never
green, the block's own time enough even for a one-shot `hlwatch status` — and alarmed, at flag level past the hour, by
the block's time or by its standing still an hour on the clock the monitor's state keeps (and `hlwatch`'s, given
`-state`); a forged mint or decision above that block is unseen until finality resumes, a guardian's flag and hold the
brake meanwhile, and whether the monitors should flag the gate then, or read those rules at `latest`, is Jae's
(§11.13). Two providers' finalized answers more than two epochs (64 blocks) apart are a disagreement, no reading —
alarmed `eth:providers` after two minutes on a monitor and at the fifth such round in a row on a signer, since two
honest providers read seconds apart can straddle a jump of an epoch or more — so one provider standing still at an old
real block cannot blind the pair's lower answer (E5-RT1); a provider must answer `finalized` truly, since one that
answers it as `latest` now stops its machine where the lower answer once outvoted it. A message numbered below
`HlAdmin`'s `launchNonce()` — the mailbox's nonce `finishSetup` recorded — is no burn of this deployment (a
deployer's mint and
burn-back while it still owned the mailbox and the tokens; a block number cannot draw that line, since such a burn can
sit in `finishSetup`'s own block); every scan starts at `eth.deployBlock` (`hleth setup` writes it). `hleth audit`
lists every message below the launch line and FAILS only on what no stranger can make — a token's burn of a positive
amount (something was minted before the launch), a delivery to the deployment's own contracts (a mint or a decision
through a stand-in ISM), a `SetupDone` from `HlAdmin`'s creation transaction — and only warns on the rest: after step 9
anyone may send a token's `transferRemote` of 0, or have the mailbox deliver to a recipient of their own through an
ISM of their own, and a stranger may call `finishSetup` first through a contract; failing on those would hand anyone a
veto of the audit, the class E2-RT6 found. The audit re-runs `finishSetup` against `SetupDone`'s block's state (an
archive read) rather than trusting the stored flag. gno.land's realm reads are not height-pinned (realm objects live
in an unversioned store: a render "at height H" answers from the latest state), so the signers and monitors read
hlmailbox again once every mark is read — its count across the read of the routes and their lines of migrations, its
delivered count from before the routes to after the credited and refund marks, and, for a token whose sends can be
refunded or whose line of migrations is open, the token's counters too, summed over its line (E4-CF6) — and a move is
no verdict, quiet, alarmed at the fifth round in a row (a monitor's after two minutes): a credit landing between the reads had left the rule loose by its amount (E3-RT3,
E3-GT2). The machines refuse to start without the contracts each rule reads: a signer without our mailbox, the
client, `HlAdmin`, Ethereum's domain, every route's token and the `MerkleTreeHook`; a monitor and `hlwatch` without
the same less the hook, and with the gate and r/hlgov (E3-GT8). Every server of the machines bounds a request's
reading and its answer's writing and holds at most 1,024 connections, and the loopback status listeners close an idle
one after 60 seconds (E3-GT5). The public signature endpoint, which strangers reach, serves one request a connection
over HTTP/1 only (no keep-alive, no HTTP/2; 5 seconds idle) and holds at most 16 connections of one client (an IPv4
address, an IPv6 /48), 64 of its slots kept for the relayers' addresses (`-relayers`, IPs or CIDR blocks), a
connection past its pool closed as it is accepted: one stranger's 1,024 keep-alive connections had held every slot and
timed out the relayers' fetches (E4-GT2). The deployment is the signer's own TLS on its public port (`-listen
0.0.0.0:<port>`, `-tls-cert`, `-tls-key`) with `-relayers` naming the relayers' egress addresses, and no reverse
proxy in front: behind a proxy every connection arrives from the proxy, so the relayers' reserved slots never apply
(their fetches share the pool of 960), the strangers' idle connections are the proxy's to hold, where nothing reserves
room for the relayers, and a per-address bound keyed by the full address (nginx's `$binary_remote_addr`) does not
group an IPv6 client by its /48 as the signer does (E5-GT3, E5-RT2). Where the public port must be shared with other
sites, the relayers still reach the signer's TLS port directly — the machine's firewall opening it to them and to the
proxy alone — and the proxy forwards everyone else: the signer then runs with `-proxy 127.0.0.1` beside `-relayers`,
counting each request the proxy forwards against the client the last `X-Forwarded-For` entry names, read from the
configured proxy alone (the vhost appending it: `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for`) — at
most 16 in flight, the next answered 429, the relayers exempt — while the relayers' own connections keep their slots
(M3b-NOTES.md, "Deploying the public endpoint").

5.7 Relayers: as the Solana plan §5.7, both directions, with Ethereum's transactions:
- gno.land → Ethereum: for a waiting dispatch in block H, the proof is header H+1's `LastResultsHash`, the
  `SimpleProof` of the transaction's results leaf and the leaf; delivery is `Mailbox.process(metadata, message)`
  once header H+1 is accepted by the client (§6.2: a proof is always by an accepted header). When dispatches from
  more than one gno.land block wait and none of their headers is accepted, the relayer re-emits them in one gno
  transaction of `Reemit` `MsgCall`s, one a nonce — never a `MsgRun`, which gnoland-1's `run_submitters` refuses from
  any key but GovDAO's three T1 members (E3-AS1) — at most 4 a batch, so one header update serves them all — each
  further `Dispatch` in a results leaf costs every delivery from that leaf ≈77–94K gas (the model's 101K, §5.5;
  E4-FC8), so four is the cheapest batch;
  a send sitting in a results leaf with more than four `Dispatch` events (anyone's `Reemit`s can make one) is re-emitted
  into the relayer's own batch rather than delivered from it at the big leaf's price, and a message is proven from the
  smallest results leaf of its block that carries its `Dispatch` (`gnolight.DispatchLeaf`, never a failed
  transaction's; a leaf above 8,192 bytes, `maxLeafBytes`, is re-emitted instead), each delivery priced by that
  leaf's `Dispatch` events and bytes (E3-GS2), and the scan sizes every message's leaf in one pass over its block's
  leaves, each parsed once (a stranger's block of a thousand small `Reemit` transactions had held both machines' ticks
  for minutes; E4-GS3); a dispatch whose header lies below the client's trusted height and was never accepted is
  re-emitted once; a message left below the trusted height again after a re-emit — a stranger's update can lift the client past every re-emit — and a decision
  at once are proven by a header proof (`proveHeader`) of the block's next header when the client's current set signs
  it, under the update's rules (§5.5; E3-RT4), and the relayers' `proveOld` setting proves a dispatch's own header
  without the re-emit first (§6.3: a header proof costs what an update costs). A block whose results leaves the
  relayer cannot rebuild from the node's `/block_results` JSON to the next header's `LastResultsHash` proves nothing —
  an error type outside §13's closed set (`results:schema:<type>`) or a string the JSON cannot carry
  (`results:mismatch`, §9) — and its messages are re-emitted, each backing off from a minute, doubling, to an hour
  while its re-emits keep landing in such blocks (`reemit:poisoned`); a failed transaction beside a dispatch no
  longer spoils its block, its leaf being rebuilt with its error (E3-RT2, E3-GS1). Anyone may `Reemit` a waiting
  message once a block, each re-emit one more event of it, and every event was read every step — its next header from
  the gno node, its client entry from Ethereum in one batch — until a stranger's ≈8 GNOT an hour of re-emits put a
  message past a provider's batch cap and failed every walk, decisions included, for good (E5-GS4). So each step the
  walk of the updater and the relayer reads, of each waiting message, its dispatch, its newest four usable events,
  those whose next header the machine saw recorded (the trusted heights it reads, the bookings, its own headers and
  proofs) and its newest usable event at or below the trusted height (a header proof's target), an entry recorded at
  the finalized block cached for good, every read split by the provider's `batchSize` (§5.6); its other events are
  judged at the finalized block, 64 a step — kept when their header is recorded there, dropped from the index when it
  can be no more (at or below the trusted height there and recorded neither there nor at latest, so judged twice an
  hour apart) — so anyone's re-emits cost a machine two reads apiece, never a read each step for good; a walk failing
  for two minutes alarms `outbox:walk` on both machines. An old re-emit whose header a stranger proves later by
  `proveHeader` is not seen: the message waits for its newest event's header (§9). The updater and the relayer refuse
  to start without `eth.updater`, the updater's own address, by which both tell the updater's bookings (§5.5; E3-GS2,
  E4-GS2).
- Every Ethereum transaction is EIP-1559, gas from `eth_estimateGas` plus 20% and never above EIP-7825's 16,777,216
  (a staged call is sized so its estimate × 1.2 stays under the cap; nothing is ever sent over it) — except a
  contract creation, and a starved execute's resend with its reservoir (§6.5), on a chain whose estimates count
  EIP-8037's state gas (Sepolia since Glamsterdam: probed once, by two creations that leave 0 and 1,024 bytes),
  bounded there by 2^32 − 1 and the head block's gas limit, since EIP-7825 then caps only the execution part (E3-AS2,
  E4-CT3, §2) — the fee cap 2× the
  base fee plus the tip, under `maxBaseFee` for route traffic and the emergency cap for decisions, executions and
  flags (§5.5); nonces are the key's own (one machine sends with a key at a time: the backup sends only while the
  primary's key has sent nothing for its 60 s, so its takeover may replace a pending transaction of the primary's,
  and a sender whose replacement the pool refuses as underpriced outbids the key's other version at that nonce by
  12.5%, within its bounds, E3-GS8); a transaction not mined in 3 minutes is replaced at the same nonce
  with BOTH its fee cap and its tip raised 12.5% over its own (geth's pool refuses a replacement that raises either by
  less than 10%), never recomputed lower from a falling base fee; a nonce that survives three replacements alarms; a
  reverted delivery is read for its reason (`already delivered` is success).
- Decisions to `HlAdmin` one copy per sequence number, only the next it expects, as the Solana plan — the next at
  the latest block, so a copy a reorg dropped is sent again and ignored at or below the last number. The updater
  buys a header only for the copy `HlAdmin` takes next — the number after the last it recorded at the finalized
  block — and once a number, so the copies anyone may re-dispatch buy nothing (E3-GS6: a decision right after
  another waits ≈13–19 minutes for its header, and while Ethereum's finality stalls every decision after the next
  waits for it, §9; E4-FC6), and a stranger's update landing first is no failure of the
  updater's, decisions' headers counting their own failures apart (E3-GS3). The executor reads each realm's ledger
  by page (§5.3) from the lowest number that may still pend, and `HlAdmin`'s numbers from a cursor below which every
  number was read at the finalized block — started past `HlAdmin`'s stored start (its first storage word), never at 1,
  since no number at or below it ever held a decision (E4-TL8) — keeping a decision pending for good (a kind 8
  waiting for code, one under a lasting hold) on a list of its own, so it never hides the numbers after it (E3-GS9). A
  realm whose ledger does not read is logged and skipped that tick, with no alarm of the executor's own (E4-TL4). Its
  gno calls whose gas only a simulation knows — a realm decision's `Execute`, GovDAO's `ExecuteOrRejectProposal` —
  are simulated from its `-gno-gas` (45M) up, the ceiling raised fourfold on an out-of-gas as far as the block's 3B and
  the broadcast sized from the simulation that fit, the key holding the fee of the gas a simulation asks (3 GNOT at the
  block's gas): a 128-id forged restore's `Execute` is ≈1.26B on a v1.5.1 node, past the 180M the client's retry had
  stopped at, which left such a call to a member's own hand (the realms audit's note, round 5; F17d). It finishes the
  GovDAO proposals made with the builders once they have the votes and are past their deadline, and alarms a GovDAO
  list it cannot parse whole, which it had read as empty, finishing nothing, silently (F17b; the integration lane I3).
- Ethereum → gno.land: the Solana plan's `Process`, `Claim`, `Execute(id)` and `Reemit` loops, `Claim` in batches
  sized from its gas: twenty claims at most, the batch aimed at three quarters of the gas the gno client carries
  (180M: 45M, then four times that), shrunk when the last `Claim` passed it, grown by one or more while a full batch
  fits but never back to a size known to be too big, halved on an out-of-gas; a `Claim` of one that runs out is tried
  under four times the gas up to the block's, alarmed `claim:gas:<symbol>` until a `Claim` goes, hourly once nothing
  more can be asked, the alarm cleared when the route goes idle (a `Claim` paying twenty costs ≈140M gas on a v1.5.1
  node at 400 claims, and the first after the launch walks every queue, ≈429M at 400 queues: p/hlroute's
  `ResetMeters`; F15d, I2). And paced to the meters (E5-GS1): a `Claim` counts as paying only when the route's
  `paidTotal`, read again right after it, moved by a unit — the route's fee, never less than a five-hundredth of its
  cap (20 GNOT at the launch values), since GovDAO may lower the fee to 0 — for each claim it paid on and left open, a
  claim it finished counting by its rest; one that paid less counts as nothing, its backoff doubling (a minute to an
  hour), and the next waits at least until a recipient's meter can have refilled a unit (a unit ÷ (cap/4) of a day:
  ≈11.6 minutes at the launch values, an hour at most); a full batch of a unit each is followed at once, a shorter one
  waits the refill while claims stay open; the backup waits the refill after a payout it did not make before its own
  clock starts. With the routes' own rule (§5.4), a 1,000 GNOT transfer to a fresh recipient is paid in ≈50 `Claim`s,
  ≈1.8 GNOT of the gno key's gas, over ≈9.7 hours, where the relayer had called every tick: ≈5,300 calls, ≈185 GNOT.

5.8 The Ethereum deploy and the setup step. `hleth setup` deploys from the deployer key, resumable from chain state,
in this order, each proxy initialized in its own constructor call so no initializer is ever open to a front-runner:
1. `ProxyAdmin`, its ownership handed in the deployer's very next transaction to `HlAdmin`'s precomputed address
   (the deployer's CREATE address at the nonce plan's 16th transaction — from the deployer's nonce n: n the
   `ProxyAdmin`, n+1 the hand-over, n+2–14 the creations, n+15 `HlAdmin` (E3-FC6); `hleth setup` simulates each transaction of steps 1–7
   before sending it, since a reverted one still spends a nonce, and refuses to go on if the plan does not hold; a
   launch `HlAdmin` refuses to be built unless it owns the `ProxyAdmin`), before any proxy exists: the deployer never holds upgrade power, so it can
   never point a proxy at its own code for one call, write storage the checks cannot see — a header entry, a balance,
   an initializer flag — and point it back (E2-CT1; on Solana the client's state was one account the audit read
   whole; on Ethereum mappings and history are invisible to a read of the current values).
2. `Mailbox` implementation (`localDomain` = 1) and its proxy, initialized with the deployer as owner and, since the
   initializer requires contracts and the real ones need the mailbox's address first, the `ProxyAdmin` standing in
   as the default ISM and both hooks until step 9.
3. `MerkleTreeHook(mailbox)`; `ProtocolFee(maxProtocolFee 0.001 ETH, fee 0, beneficiary = the treasurer's Ethereum
   address, owner = the deployer)`.
4. `Ed25519Verifier` (no storage, no owner, no constructor arguments; `hleth setup` creates its own at n+6 — only
   the forge script's local runs reuse one already on chain, E3-FC6), then the
   `GnoLightClient` implementation (its constructor fixes the mailbox's `pkg_path` hash and the verifier's address)
   and its proxy, initialized with the chain id, gnoland-1's domain, the trusted header (height, hash, its validator
   set — less than an hour old and never one announcing a change, which `initialize` itself enforces: it takes the
   header's sixteen leaves, hashes them to the trusted hash, and refuses a header whose ValidatorsHash, the set's
   hash and NextValidatorsHash are not one value or whose time is more than an hour from the block's, either way —
   real time, the same in a factor-8 build; `hleth setup` fetches the header just before this step and holds it to 30
   minutes, or takes it offline from `-trusted-file`, checked as a node's) and the deployer
   as admin. `initialize` checks the header's own consistency, never that it is gno.land's: only `hleth audit` with a
   gno source holds it to gnoland-1's block and validators at that height (below; E3-CF2, E3-RT5).
5. Two `HypERC20` implementations (`decimals` 6, scale 1/1, the mailbox) and proxies, initialized with supply 0,
   the name and symbol, hook 0, ISM 0 and the deployer as owner.
6. `PausableGate` implementation and its proxy, initialized with the mailbox, the client, the two tokens, the four
   guardian seats and two watch slots, the launch mint limits and the deployer as owner.
7. `HlAdmin(mailbox, client, gate, tokens, proxyAdmin, merkleTreeHook, protocolFee, the verifier and its code hash,
   Ethereum's domain, origin = gno.land's domain, hlgov = r/hlgov's 32-byte address, the two route realms' 32-byte
   addresses, the mailbox's pkg_path hash, seats, watch, the launch mint limits, startSequence = r/hlgov's counter
   (at most 2^32: r/hlgov's `sync sequence` raises its counter by at most 2^32 a proposal, so a start above would be
   out of one proposal's reach — 2^64−1 ignores every decision for good — and `HlAdmin` refuses one, `OutOfOrder`, as
   `hleth setup` does before it sends anything; E3-GS4, E4-CF6), predecessor = 0)`, which creates `HlAdminExec`, its
   execution half — recording its code hash and never delegating to other code (`ForeignExec`) — keeps a launch's starting number in its own code, and refuses (`ProxyAdminNotHeld`)
   unless the `ProxyAdmin` is already its own.
8. Settings: each token's ISM → the gate; each token enrolls gno.land's domain → its route realm's 32-byte address.
9. The mailbox's default ISM → the gate (never a raw client, so a token whose pointer is unset still goes through
   it); its default hook → `MerkleTreeHook`; its required hook → `ProtocolFee`.
10. Ownership, in this order: `MerkleTreeHook`, `ProtocolFee`, both tokens, the gate, the mailbox
    (`transferOwnership(HlAdmin)`); the client (`setAdmin(HlAdmin)`) — after which the deployer holds nothing (the
    `ProxyAdmin` went at step 1). Until then the deployer owns the mailbox and the tokens and could mint through a
    stand-in default ISM and burn back, leaving a burn in the record with the supply back at zero; a launch's
    `finishSetup` records the mailbox's nonce as the launch line (`launchNonce()`): a message numbered below it was
    dispatched before the launch and is none of the bridge's burns — the signers, monitors, relayers and `hlwatch` count
    none — and `hleth audit` lists any (§5.6). (A block number cannot draw that line: the deployer's burn can sit in
    `finishSetup`'s own block. F4 had `finishSetup` require that nothing was ever dispatched; F5 retired that: after
    step 9 any stranger's `dispatch` would fail it for good, a one-transaction veto of the launch — E2-RT6.)
11. `HlAdmin.finishSetup()`: permissionless and self-checking — it reads every owner and admin as itself, every
    proxy's admin as the `ProxyAdmin`, the mailbox's ISM and hooks, each token's ISM, hook, fee recipient, fee hook
    and router for gno.land, the gate's guardian list against its own, the clock factors of the client, the gate and
    itself equal, the client's origin domain its own and its compiled hlmailbox `pkg_path` hash `HlAdmin`'s — and,
    since a value held in an immutable is outside any
    masked code hash, every binding that makes a contract ours: each token's `mailbox()` is our mailbox, its scale is
    1/1 and its `decimals()` 6; the gate's mailbox, client and tokens; `MerkleTreeHook`'s
    mailbox; `ProtocolFee`'s maximum the compiled 0.001 ETH (a token built over a stand-in "mailbox" could be minted by
    it directly, past the gate and the client); the client's verifier `HlAdmin`'s, holding code (not an EIP-7702
    designator) of the compiled hash; the mailbox's and both tokens' `localDomain()` Ethereum's; and at a launch each
    token's `totalSupply()` 0 and its ISM the gate itself (a successor's setup takes the gate or zero, kind 3's two
    values, and reads no supply, so it passes on a live bridge: E3-CF10), the
    gate's mint limits the launch ones, the protocol fee 0, its own decision record empty at the start its code
    holds (the last number and the stored start both `startSequence`, nothing pending, no cancelled range, no
    successor) and its hold record one the keys' flags could have left before the setup (at most eight holds, each
    ended before it; no moment of the standing hold after it; a stored hold standing on two guardian flags or in its
    dip) — check 34, in `HlAdmin`'s own code since fold F11 (E3-CT2, E4-CT1), which bounds that record from above
    only: a hold start before `HlAdmin` existed and a flag dated ahead pass it, and an immutable creation time to bound
    them below measured 176 bytes against the 141 of `HlAdmin`'s EIP-3860 margin, so `hleth audit` holds them (below;
    E5-CF1, F17d) — checks 1–31 and 34 (32–33 retired; M2-NOTES.md §2.5, Folds F4–F5).
    It sets `setupDone` and moves nothing; no tool takes `setupDone` as proof of anything.
`hleth audit` prints every one of those, and compares every creation transaction of the sixteen deployments — its
input byte for byte — with the reproducible build's creation code and the expected constructor arguments (a
contract's runtime code says nothing of what its constructor wrote: a deployer could run initcode that writes, say,
a decision record into `HlAdmin`'s storage and then returns `HlAdmin`'s exact code — E2-RT1; `HlAdminExec`'s creation
is inside `HlAdmin`'s, so `HlAdmin`'s input covers it); `HlAdmin`'s creation transaction, which must log nothing — the
build's constructors emit no event, while a planting initcode runs as `HlAdmin` and can log `FlagSet`, `HoldEnded` or
`DecisionRecorded` as it, so any log there fails and none explains the record — and `HlAdmin`'s hold record below
check 34's bound: every hold of the history, and every moment of the stored hold (read whole from its storage word,
since no view shows a hold whose dip ran out unsettled), between `HlAdmin`'s creation and the block read, and every
standing flag a `FlagSet` of its slot at its block (E5-CF1); the `ProxyAdmin`'s `OwnershipTransferred` logs (exactly
two: its deploy, then to `HlAdmin` — a third only in a kind 9's transaction), each proxy's `Upgraded` and
`AdminChanged` logs (its construction's alone — a later `Upgraded` only in its kind 8's transaction), the result of
simulating `initialize` on each of the five proxies (each must refuse) — what no on-chain check can see: a deployer
running its own code instead of the script could create a contract at the planned address, upgrade through it, and
hand the `ProxyAdmin` to an `HlAdmin` made later; the logs show it — the raw RPC calls it made, each contract's runtime code hash against the
reproducible build (Hyperlane's from its release's compiler input, ours from this tree) with every masked immutable's
value printed beside the value it must have (`HlAdmin`'s `hlgov()` beside the 32-byte address of this deployment's
`…/eth/hlgov`, its `origin()` beside gno.land's domain), and each proxy's EIP-1967 implementation and admin slots
(`eth_getStorageAt`: the admin the `ProxyAdmin`, the implementation the recorded one or the one an executed kind 8
put there). Our implementations' constructors disable their initializers; Hyperlane's do not (they are
never used except through their proxies, which are initialized in their constructor calls). The audit PASSes only
with a gno source of the member's own (`-gno-rpc`, several agreeing: every source read before any is judged, a realm
taken as not up only when every source answers the keeper's not-found — one source behind the realms' upload is a
disagreement, a FAIL — and the sources held to agree on the figures the audit reads, a route's maximum and lock, not
on a render that moves with the clock, such as the route's meter; E4-TL2 — a render that moves with gno.land's state,
a route's lock with every send and payout, r/hlgov's renders with every dispatch, read again at one height before the
sources disagree: each answer bracketed by its node's head, the sources brought to the highest within 30 seconds,
answers at one height required equal; E5-CF3), the four path flags (`-mailbox-pkgpath`, `-hlgov`, `-gnot-route`,
`-bubble-route`), since nothing on chain names the bridge's namespace, and the launch values — `-seats`, `-watch`,
`-beneficiary` and each token's expected lock (`-expected-lock-gnot`/`-bubble`), the launch notice's published values,
never the keys or the beneficiary as a kind 5 or 7 left them (E4-TL1, E4-TL6); without any of them its verdict is
UNVERIFIED, never PASS, both exiting 1 (E3-CF2, E3-RT5, E3-GS4, E4-TL1). The machines' config (`-config`,
`bridge.json`) is the operator's file and gives the audit no source and no launch value of its own: its gno sources are
read beside the member's `-gno-rpc`, every reading compared, never in its place — alone they hold gno.land to the
operator's word, and the verdict is UNVERIFIED — and its routes' expected locks are only noted, never taken (E5-CF3).
It holds the deployer's record to sources the deployer does not write: the client's trust root to gno.land's block
and validators at the trusted height (its sixteen leaves, its hash, height, time, chain id and set); `HlAdmin`'s start
to r/hlgov (its compiled start while `HlAdmin` is its first and only decision recipient, its counter, its recipient —
the counter less r/hlgov's decisions in flight, each number above the line's last, at most 64, rendering as r/hlgov's
own `decision/<n>`, noted with its age and warned past two hours, since a decision's header and finality take ≈15–35
minutes, E4-TL6); the seats to `-seats`, `-watch` and r/hlgov's Ethereum seats; the mint limits to the expected lock and the routes' maxima; the
beneficiary to `-beneficiary`; each token's enrolled domains to gno.land's alone (E3-CF12). After governed changes it follows `HlAdmin`'s line and holds each moved value to the
executed decision that moved it — kinds 3, 5, 6, 7, 8, 9 and 12, each read from `HlAdmin`'s own record of it and held
to r/hlgov's body of that number, one r/hlgov never dispatched a FAIL — so a member's audit at the yield tells a
governed change from a tampered one (E3-CF9); a successor's own code, bindings and record it does not audit — those
are what `hlwatch` reads before the kind 9's vote (§6.5). The whole Ethereum side
is run to a passing audit before the realms are uploaded, since the realms compile the routers' and `HlAdmin`'s
addresses and the deployment's addresses they refuse as recipients (§5.2). `hlwatch status` is green only over a
poll that ran every rule: no flag-level or finding alarm standing, every token judged and holding, nothing unread — a
gno.land ledger (named NOT READ, never "none"), GovDAO's list (one not read whole — an item or a status word the
reader does not parse, a page that shows no item and does not say there are none — is a list not read, never "no
proposals": GovDAO's render is not fixed for the bridge's life, E5-GT2) or a proposal's page, r/hlgov's counter, a
stage's block, the sources rule (unrun, too, when no second gno source is configured, `gno.sources`: the ahead rule's,
the stages' and the trusted header's second readings then rest on one node, E5-GT2), a validator set the small-order
rule reads, the encodings probe: each that did not read is said and keeps the page from green (E4-GT5, E4-CF3,
E4-TL4) — no Ethereum-side reading blind for its finalized block's age (§5.6; E5-RT1), no scan behind Ethereum's
finalized block (it polls until caught up, bounded by `-catch-up`, a state file keeping its place), every decision
pending on `HlAdmin` listed (read from a cursor as the executor reads them, §5.7: the last 256 numbers alone had
hidden an older pending kind 8; E4-CF4), the client's trusted header gno.land's block at its height (E4-TL3), and no
pending kind 8 or 9 NOT SHOWN IMMUTABLE (E3-GT3, E3-GT10). `hlwatch` keeps GovDAO's next proposal number and
the set of proposals still open at its last whole read, and each reading reads only the pages of proposals made since
and each open proposal's own page — not a floor at the lowest open id, which a proposal nobody finishes would pin
(gnoland-1's #6 is still open behind #7–#13, all accepted); read whole, a list past the reader's fifty pages — 250
proposals at impl v0's five a page — had kept the page from green for good (F17b's review; the integration lane I3).
It reads only the lines GovDAO writes on a proposal's page, never the proposer's description, which renders first and
could carry a forged "Executor created in" line. Its own gno calls — a
guardian's `SetFlag` naming ids, a `ClearFlag` — are sized from their simulation as the executor's are (§5.7): a
payouts flag naming 256 ids costs ≈0.5B on a v1.5.1 node, past the 180M the client's retry had stopped at (F17d).

5.9 Acceptance before mainnet — as the Solana plan §5.9's list, read for Ethereum (Sepolia for devnet, the drills
on a factor-8 build: 7 days in 21 hours), plus:
- `Ed25519.sol` agrees with Go's `crypto/ed25519` on every vector of RFC 8032, of Wycheproof's `ed25519_test.json`,
  of the "Taming the many EdDSAs" edge cases (small-order keys and R, non-canonical encodings, S ≥ L, mixed-cofactor
  cases), and on at least 100,000 differential cases (valid signatures over random messages and keys, each mutated
  in one bit of R, S, A or the message, and random bytes), with no disagreement — any divergence stated with its
  proof that no gno.land validator's key can reach it; and, separately, since Go's vectors never exercise it, a
  signature by one key checked against another key's bytes fails at the library and at the client;
- the client verifies every fixture height of the Solana build's tests (gnoland-1's own headers, commits, sets and
  results, three of gnoland-1's four set changes among them; the fourth is replayed on Sepolia) with the same answers
  as `gno-light-core`;
- the results leaves of real gnoland-1 blocks holding failed transactions rebuild to the next header's
  `LastResultsHash` (nine blocks, nineteen failed transactions, v1.2.0 to v1.5.x), and §13's closed error set
  encodes as amino does at v1.2.0 through v1.5.1 (101 results, no disagreement; E3-RT2);
- a monitor's (or `hlwatch`'s) burn index that starts past the deploy block — the mailbox's nonce before it above
  the launch line — is the Solana plan §5.9's checkpoint: it alarms the claims it cannot judge and never flags them,
  for the index's life, since Ethereum keeps no per-message account to seed it from (E3-GT4) — as one alarm,
  `credit:unindexed-before-base` (how many, the first three named, repeated daily), never one a claim, each claim kept
  in the state until its cure (E5-CF3); and such a watch's slot watch holds only the kind 8s executed after its first
  block, so a proxy a kind 8 upgraded before it is not held (§6.5; E4-CF6) — a guardian's `hlwatch` starts at the
  deploy block, its default;
- the forged-credit rule (the Solana plan §5.9's unindexed credit): the monitors and `hlwatch` read a route's waiting
  claims only while hlmailbox's delivered count exceeds the burns the index places — every claim is a delivery, so
  with every delivery placed no claim is unindexed, however many wait — and read them at the route's `waiting/<from>`
  (§5.2: a seek and a page, ≈219M gas on a v1.5.1 node at any depth; up to ten new pages a poll from the place last
  read, the claims found there that no burn of the index carries re-read by id, up to a hundred a poll, each judged only
  in a poll that read it, the reading started over past the route's `waitingLast` and once a day; I2 — its place and
  its suspects, with the poll numbers that read them, kept in the monitor's state (and `hlwatch`'s, given `-state`), so
  a restart reads on from where it stopped, E5-CF3), never
  `claims/<from>` on a route that has the index, whose walk of every claim before <from> is out of a query's gas past
  ≈1,600 waiting claims and had stopped every poll there, before the Ethereum rules (E4-RL1) — a route built before
  the index ("unknown path") is read at `claims/0`, `/100` and `/200` only; a page that does not read is said, never green, and the poll goes on. An unindexed claim is
  judged once the index's finalized block's time has passed the credit's — an honest burn credited by then lies at or
  below that block — or at once when neither provider, each current to its head, shows a dispatch of its id above the
  index; until then it waits, named (§5.6; E4-GT1); and its alarm ends only when its burn is indexed or every route
  answered and none holds it waiting — one route's failed read beside another's "no such claim" cures nothing (E5-GT4);
- a diverted credit and a foreign decision are kept in the monitor's and `hlwatch`'s state and judged again every poll
  until cured — a diverted claim gone, credited afresh by vote, or naming the burn's recipient and amount now (a voided
  one kept quiet, and flagged again if a restore brings it back); a foreign decision no longer pending on the live
  `HlAdmin` — their flags set again when found cleared, as the identity's are, so neither a restart nor an unpause
  forgets them (E4-GT6);
- every gas figure the plan uses is measured on a node pinned to mainnet's fork (Osaka), never Sepolia's;
- every Hyperlane contract's runtime code equals the release's compiled code with the immutables masked, every
  masked value is the one §5.8 requires, and Etherscan or Sourcify reports an exact match for all sixteen
  deployments (the eleven that are no proxy, and the five proxies; §1's eleven codes count the proxies' one code and
  the two tokens' one; E4-FC8).

## 6. The light client on Ethereum, the gate and `HlAdmin`

6.1 Header verification, as the Solana plan §6.1: a trusted header and the set it belongs to; `update(header,
commit, set, nextSet)` accepts a header above the trusted height signed by the set its height belongs to — counted
by slot index over a set given in full in the calldata and checked against the stored set hash, each precommit's
sign bytes built from its own timestamp and the commit's BlockID and round, `3·tallied > 2·total` by power, a nil
or other-block precommit never tallied (the submitter leaves its slot out); set changes followed in order through
NextValidatorsHash, the announcing header's next set given in full and checked against it; no trusting period;
heights only rise. The client stores hashes, not sets: the current set's hash, the announced next set's hash, the
trusted header (height, hash, ValidatorsHash, NextValidatorsHash, time) — a set arrives in calldata each time and is
hashed (tm2's simple merkle over the validator leaves) against the stored hash. Quarantine: an update announcing a
change of ValidatorsHash sets `quarantineEnd = max(quarantineEnd, block.timestamp + 7 days)`; the gate reads it;
no decision shortens it. The set history (from-height, set hash, the block and time it was announced, its
quarantine's end) is kept in storage and in events. A key of a type other than ed25519 in a set is refused, as on
Solana (the Solana plan §13's validator leaf: a set the client cannot follow ends the deployment, §6.4; E4-FC8). A
key that decodes to a point of small order (its order divides 8) is accepted, as Go accepts it — and anyone can then
sign for that validator, as on gno.land itself — so the updater, the monitors and `hlwatch` alarm on such a key in a current or announced set; refusing it would split the client from the chain.

Cadence (§5.5): the updater submits the header of the oldest waiting dispatch's block plus one when the budget
covers it, the header the next decision needs at once (within the 2 minutes it keeps between its own updates, the
announcing one excepted; E3-CF11) — the next being the number after the last `HlAdmin` recorded at the finalized
block, so one right after another waits until the first is recorded there, ≈13–19 minutes (§5.7; E3-GS6, E4-FC6) —
the announcing header within a minute of a validator change executing (it watches r/sys/validators' events), and the
header of any send waiting 6 hours; anyone may submit. Route headers wait while the updater's event scan is behind
the head — the route sends whose newest event lies above the scan, whose leaf it has not measured and would price at
the least delivery, while the relayer delivers a booked send from whatever leaf it has (a restart after a gap: a few
steps; alarmed `updater:scan` after ten minutes) — and a decision's does not (E5-GS6). The alarms: a send waiting 7
hours (delivery age, whoever's updates kept the client fresh, on the updater and the relayer alike: E5-GS3), and a
change gno.land announced not accepted within 30 minutes.

6.2 Message proofs. A proof is always under an *accepted* header: the client keeps, for every header it accepted
or proved (§6.3), an entry `{epoch, height, lastResultsHash}` keyed by the header's hash. `verify(metadata,
message)` reads the metadata `headerHash ‖ proof (total, index, aunts) ‖ leaf`, requires the header's entry with
the current epoch and a non-zero results hash, checks the `SimpleProof` of the leaf against it, parses the leaf
(an `ABCIResult` without an error) and requires a `Dispatch` event whose `pkg_path` is this deployment's hlmailbox
and whose seven attributes match the message (id, nonce, origin = gno.land's domain, destination, sender,
recipient, sha256 of the message). The epoch rises at every set change, so a header of a rotated-out set proves
nothing after the client followed the rotation (the Solana plan's ring cleared at a set change; on Ethereum no
256-entry bound is needed, storage being per entry). The record: a counter and a running hash over every header
accepted or proved, `keccak256(previous ‖ hash ‖ height ‖ block.number)`, with an event per header — Ethereum's
logs are committed by the receipts root and are not truncated, so the event is the record the watch rebuilds (the
Solana client needed instruction data for that) — read from providers that keep the history (EIP-4444, a draft, would
let clients prune headers, bodies and receipts older than 14,299 epochs, ≈63.5 days, since 2026-09-28 — a year until
2026-08-28, and ≈146.8 days between (E3-FC8; the assumptions audit, round 4); the watch's own record of what it
rebuilt is then the copy, and `hlwatch`, a fresh install's scans from `eth.deployBlock` and `hleth audit`'s reads of
the setup's logs need a provider that keeps the history once the deployment is that old, §5.6). The record's base — the trust root, recorded at the setup's step 4, before the
record's scan starts — is compared too: every poll the monitors and `hlwatch` hold the client's trusted header to
their own node's block at its height and, on a difference, the second source's, alarming `header:trusted` (never
green) when both differ (E4-TL3).

6.3 Packaging. One transaction carries an update while it fits: the transaction gas cap (16,777,216, §2), the gas of
one signature (≈221K) and the set's size bound it — under the relayers' rule that a call's estimate × 1.2 fits the
cap, one transaction carries an update up to about 85 equal-power validators (by the measured gas, §2; the budget's
model, priced above it, stages from 84), and the client's maximum of 255 takes four or five. `update` and `proveHeader` stage themselves (the interface): a call
whose precommits do not cross 2/3 stores its tally under a stage keyed by (submitter, header hash, BlockID, round,
the client's epoch, the expected set's hash) and returns false; the call that crosses 2/3 finishes — after checking
everything again against the live state (an update's height still above the trusted height, a proof's at or below
it, the chain id, `ValidatorsHash` equal to the set the live state expects, the epoch unchanged), as Solana's
`update_finish` re-checks the live set — so a stage opened under a set that has since rotated can never finish (a
thief holding a leaked set cannot pre-stage forgeries one signature short and finish them after GovDAO's rotation),
no set the transaction cap cannot fit freezes the client, and nobody can pre-empt another's stage. The watch reads
`StageProgress` — whose height and header hash it holds against gno.land's own — and alarms on a stage for any header
gno.land does not have, a stage more than an hour of blocks above both its node's head and the second source's at once
(`stage:unknown`, never green; a stage block its node does not serve is said, E4-GT5); a call that verifies no new
signature is refused, so every stage carries one, and no stranger stores an empty stage to raise that alarm for
nothing. The client takes a set of up to its compiled maximum, 255 validators; the updater's `client:set-bound` alarms at 40 in the set that signs the header
after the client's trusted one (an early word on cost, as on Solana), and its `updater:staged` the first time an
update call misses the cap with its margin — it then stages at half as many signatures a call, and the alarm stands
(E3-FC6, E3-CF11). `proveHeader(header, commit, set)` is the
self-contained path the Solana plan's header proofs are: a cancel under a forger who runs the client ahead is proven
by its own real header, which no forger can keep out. E-M2 measures one signature, an update in one transaction at
4, 6, 20, 28, 40, 60 and 100 validators and staged at 40, 100 and 255, `proveHeader`, `verify` by leaf size, and
`process` with a mint (§2; E3-FC6).

6.4 No recovery, one rule: as the Solana plan §6.4, word for word on Ethereum: `HlAdmin`'s only way in is the
mailbox's `handle` under the raw client; the client has no reset; a client that can no longer verify gno.land ends
the deployment, burns back keep working through the signers, and the exit is a fresh deployment. On Ethereum that
deployment comes with a fresh r/hlgov compiled with it (E3-CF5): r/hlgov's source takes its contracts from
hlmailbox's compiled launch list — the decision recipient, the five proxies kind 8 upgrades and their launch
implementations, the gate kind 3 names, the sixteen every "contract of the deployment" refusal reads — so the r/hlgov
that stays could never upgrade the new contracts. The fresh one names the new deployment's itself; its counter starts
at the old one's last number (`launchSequence`), the new `HlAdmin`'s start (§5.8 step 7), its governor is
hlmailbox's, and the new `HlAdmin`'s `hlgov` binding names it. The routes stay and follow the new tokens by `set
router` (`hleth setup` enrolls them as the new tokens' routers at the deploy, so no kind 6 is needed), and Ethereum →
gno.land needs nothing of the realms. The old r/hlgov is retired by rule: no proposal is made with its builders after
the exit, since a kind 6 there, sent to the abandoned `HlAdmin`, would still write hlmailbox's enrolment note at its
dispatch (a successor route's sends, and 14 days on its refunds, wait on that note, which no execution on the
abandoned Ethereum side stands behind) — a rule nothing on chain enforces and no tool alarms (no `hlwatch` alarm for
the launch r/hlgov's proposals after the exit is built), so it is the members' discipline alone (E4-RT3). What stays
the launch's, as limits: the routes refuse the launch
deployment's sixteen as a send's recipient and as a router, not the new one's (the site refuses those its
configuration names); their realm lists name the launch r/hlgov, so a burn to the fresh one is credited and paid into
it; hlmailbox's `json` keeps the launch contracts, so the machines take the new deployment's from their
configuration; kind 12's floor reads the launch routes (§5.4); and the enrolment note a successor route's sends and
refunds wait on is the launch r/hlgov's alone (hlmailbox's `NoteEnrolled`), so no route migration completes after
the exit — lifting that needs a hlmailbox decision naming the r/hlgov whose notes count, a new power not built
(§9, §11.12).

6.5 `HlAdmin`: as the Solana plan §6.5 — a mailbox recipient whose ISM is the raw client (fixed in its code); it
accepts only messages from gno.land's domain whose sender is r/hlgov; it records a decision whose 9-byte prefix
parses at `last + 1` whatever its kind (an unknown kind recorded and spent), ignores one at or below `last`,
refuses one above `last + 1` (it stays deliverable); `cancel` executes at recording; `unpause` 24 hours after
delivery, spent under a hold on its object; every other kind 7 days after delivery, deferred by any flag on
`HlAdmin` and by a hold, except kind 5, which lands through a lone flag and through a hold on the later of its own
7th day and the 21st day of the earliest hold overlapping its pending life (the last eight holds kept, a ninth
merging the two oldest); anyone executes a matured decision; a decision whose parameters fail `HlAdmin`'s own
checks is spent — and only those: a failing external call (a contract it calls reverting, or starved of gas) reverts
the whole `execute` and leaves the decision pending for a retry, as a failed CPI does on Solana, so nobody can kill
GovDAO's decision by executing it with just too little gas (EIP-150 keeps 1/64 for the caller) — nor starve a kind
8's call: `HlAdmin` forwards it exactly 2,000,000 gas whatever gas the executor sends, and an execution left less
than that reverts (`CallStarved`), pending (E3-CT4; the relayers' executor resends it at the next tick with a
3,000,000 gas limit — on a chain whose estimates meter EIP-8037's state gas, with 2^24 + 8,812,800, a reservoir for
≈90 fresh slots of state its call grows, the key paying its upfront cost, that gas limit at twice the base fee plus
the tip (≈2.6 ETH at the 50-gwei cap), so the relayers' 0.4 ETH carries it only below ≈7 gwei and above that it fails
as a send short of funds, counted, parked hourly and alarmed — a starve at that limit counting toward its parking;
E4-CT3); and `execute` runs no decision above the last number recorded, so a decision record that no delivery made
— planted by a constructor — can never run (E2-RT1). On an Osaka chain a kind 8's call then runs on exactly 2,000,000 gas whoever executes — its
initializer on at least ≈1.92M of it, a few thousand more when the executor warms the path first (+8,299 measured,
E4-CT2); on a chain with EIP-8037's state gas (Glamsterdam: Sepolia since 2026-10-06, mainnet unscheduled) the
2,000,000 bounds execution gas only — the call's state growth is paid first from the executor's reservoir (its
transaction's gas above 2^24, passed to the call whole, which no opcode reads) and only with none from the 2,000,000
(≈110K a fresh slot on Sepolia: some 17, where Osaka's 22.1K gave ≈85) — so how much state the initializer can grow,
and any branch on that, is the executor's (E4-CT3, E4-RT4); and since a simulation applies no reservoir, a kind 8
whose call grows more state than the 2,000,000 holds fails the executor's simulation and is never sent by it: a
transaction sent by hand above 2^24 executes it. Ethereum specifics: a pending decision is a storage record (kind,
parameters, delivery block and time, deliverer), deleted when it ends, and existing only at a number `handle`
accepted — above the start (a launch's held in the code) and at or below the last — so a record anything else wrote (an initcode can write any storage) never runs (at a launch once
`finishSetup` ran, check 34 holding the record to the start in the code; an initcode that also planted `setupDone`
skips `finishSetup`, and there the guarantee is `hleth audit`'s, the creation input byte for byte and `finishSetup`
re-run with `setupDone` false: E3-CT2; check 34 also refuses a hold record the keys' flags never leave before the
setup — more than eight holds (every kind 9 would pend for good, `successorInit` taking eight), a hold not ended
before it (it would lower kind 5's floor under every later hold), a moment dated ahead (a hold standing with no flag
up, or one no replacement can land through), a stored hold with neither two guardian flags nor a dip (the next
guardian flag would revive it) — and vetoes nothing: the guardians' own hold, standing, dipping or ended before the
setup, passes (one ended in the setup's own second waits a block); and check 34 bounds the record from above only —
a hold start before `HlAdmin` existed and a flag dated ahead pass it — so a standing flag or a hold start an initcode
planted reads as the keys' own and is `hleth audit`'s: no log in `HlAdmin`'s creation transaction, every hold moment
between its creation and the block read, every standing flag a `FlagSet` of its slot at its block, §5.8; E4-CT1,
E5-CF1), every number that
can run is within a cancel's reach, and `handle` clears the record at every number it accepts; `handle` refuses value; flags carry the block number they were last set at (the Solana slot's role:
an unpause clears flags set before its delivery block); there is no buffer, no extension and no refund key
(kind 8 is a proxy upgrade, with an optional call so a new version's initializer is never left open, forwarded a
fixed budget of 2,000,000 gas (above), and it reverts — pending — when after the upgrade the proxy's admin is not the
`ProxyAdmin`, the contract's owner not `HlAdmin`, or a binding `finishSetup` holds that no decision moves is not the
deployment's — the mailbox's default ISM, hooks and domain; a token's ISM (the gate or zero), hook, fees, mailbox,
scale, decimals and domain; the gate's mailbox, client, tokens, guardian list and clock factor; the client's clock
factor and origin (`AuthorityLost`; E3-CT3) — so no reviewed upgrade takes a contract out of the deployment's hands:
every after-check reads the proxy, whose answers after the call are the new code's, so code whose call rewrote the
EIP-1967 admin slot, and which answers `admin()`, `implementation()`, `owner()` and the bindings as the deployment's,
passes them; they catch an honest mistake and reviewed source built over another binding, never code written to
answer them falsely, which is the members' review's to refuse (`hlwatch`'s build comparison, §8; E3-RT8, E3-AS7);
`hleth audit` reads both EIP-1967 slots with `eth_getStorageAt` (§5.8), and so do the monitors and `hlwatch status`
from the first executed kind 8 on, every poll with a verdict, through both providers: the slots must hold the newest
kind 8's implementation and the `ProxyAdmin`, else the alarm `proxy:slots:<proxy>` stands and `hlwatch` is not green
(a watch started from a checkpoint holds only the kind 8s executed after its first block, §5.9; a pending kind 8's
slots read after simulating it, before the vote, is not built, §12; E4-FC7, E4-CF6); a kind 8's
implementation and a kind 9's successor are spent when their code is an EIP-7702 delegation designator
(`0xef0100…`: its code hash covers only the pointer, and the account's key can re-delegate it at will) or its code
hash differs from the decision's; a target with no code yet leaves the decision pending (`NoCodeYet`), as a Solana
kind 8 whose buffer is not yet staged pends — code an earlier transaction left at an address never changes, except
an EIP-7702 account's. Whoever can deploy at a pending target decides when its decision runs, so members vote a
kind 8 or 9 only for code already deployed (`hlwatch` reads and checks it before the vote), proposers name addresses
only they can deploy to (never a public CREATE2 factory's), `hlwatch` lists every decision waiting for code — every
decision pending on `HlAdmin`, read from a cursor as the executor reads them, however old (E4-CF4), its
`decision:code-wait:<n>` alarm ended only by code there that both providers give alike or by the decision's end, never
by a read of the code that fails or disagrees (E5-GT4) — and
GovDAO cancels one that goes stale (a forgotten upgrade must not roll a proxy back years later). The hash pins
bytes, not behaviour, so `hlwatch` says "immutable" only of code
equal to a reproducible build of reviewed source that reaches no mutable target by `DELEGATECALL`, printing every
immutable's value beside the value it must have (on Ethereum the immutables hold every binding), and the creation
transaction the proposer names compared with the build — anything less NOT SHOWN IMMUTABLE, a target created
through a factory (its initcode unshown) among it; a kind 9's successor must hold this `HlAdmin`'s bindings
that grant power — mailbox, client, gate, both tokens, `ProxyAdmin`, both hooks, r/hlgov, the origin and local
domains (eleven views, each compared) — or the decision is spent
(`"bindings"`); its `HlAdminExec` and its start are bound only by the code hash the decision pins, which the members
hold to a reproducible build with `hlwatch` — its `exec` the successor's CREATE at nonce 1 and the code there the
`HlAdminExec` build's, its `_execCodeHash` that code's hash, its `_startSequence` 0, no immutable holding different
values in its places — since no on-chain check can bind them: every answer of the successor's is its pinned bytes'
(E3-CT1: `HlAdmin`'s reviewed runtime with its `exec` and `_execCodeHash` pointed at a thief's code passes kind 9,
and anyone's `finishSetup` then hands the thief the `ProxyAdmin`); a successor whose `successorInit` refuses its own
`HlAdminExec` (`ForeignExec`: one its `_execCodeHash` does not name) or one of whose eleven views reverts leaves the
decision pending — the execution reverts — never spent, so it waits for GovDAO's cancel, `hlwatch` never calling
it immutable (E3-CF10, E3-FC9); the verifier, its code hash, the mailbox's path and the routes, which only a successor's own
`finishSetup` reads, are not compared (after a verifier swap, a path change or a moved route, comparing them would
leave every later successor unable to finish its setup; `hlwatch` prints them as "finishSetup's only"); its
inheritance writes its whole record; a kind 8 with a call checks after it that the proxy still points at the
decided implementation (an implementation's code can rewrite the EIP-1967 slot during the call), reverting — pending —
otherwise (`ImplementationMoved`) — after every kind 8, with a call or not — and `hlwatch` decodes the call against the
implementation's ABI for the members; kind 9 transfers the `ProxyAdmin`'s, the mailbox's, both tokens', the gate's,
the hooks' ownership and the client's admin to the successor and writes the live number, seats, watch slots and
hold history into it by `successorInit`, which only the predecessor may call, once; from then on `HlAdmin` records
nothing (`Retired`), and what pends there never runs — nobody need close it, since nothing on Ethereum charges
rent. r/hlgov's decision recipient is `HlAdmin`'s address; its `set decision recipient` (7 days)
follows a kind 9 as on Solana, and `hlwatch` reads a switch proposed or queued against `HlAdmin`'s line — UNEXECUTED
while the kind 9 has not run (executed first, the successor answers `NotInherited` and GovDAO's channel deadlocks
under a hold), RETIRED or NOT INHERITED for a recipient that is no longer, or not yet, the live one — and alarms it
(E4-CF5). Readings of the code (E3-CF12): at most 64 cancelled ranges are stored, a 65th
merged with the nearest — the numbers between them cancelled too, which can end a pending decision no cancel named
and never runs one; a range, clamped at the last number, is stored only while a decision pends, and every stored
range is dropped once none does (E4-CF6); kind 5 is spent when it names the current key of any seat or watch slot,
not only of the seat it fills; the deliverer a pending decision records is `tx.origin`.

6.6 `Ed25519.sol`: `verify(bytes32 publicKey, bytes message, bytes32 r, bytes32 s) returns (bool)`, the semantics
of §2 exactly: refuse when `s`'s top three bits are set or `s ≥ L`; decode A from the 32 bytes laxly (reduce y mod
p; an x of zero with the sign bit set decodes as zero; refuse a y with no square root); k = SHA-512(the signature's
first 32 bytes ‖ the public key's 32 bytes as given ‖ M) as a 512-bit little-endian integer mod L; compute
[S]B + [k](−A) and accept iff its canonical encoding equals R's 32 bytes. The key's 32 bytes are its only key input:
every point is derived from them inside the library, never taken from a caller or a cache (§2's binding hazard).
SHA-512 is written in the contract (FIPS 180-4). The scalar multiplication uses Go's own extended-coordinate
formulas, so small-order and mixed-order points come out exactly as Go's; it is a public-input computation (no
constant-time requirement). E-M2a's gates (M2a-NOTES.md): zero disagreements with Go on RFC 8032 (5), Wycheproof
(151), the speccheck cases (12), 118,392 differential cases and 384 binding cases, SHA-512 on 951; byte-identical
vectors under Go 1.25.9 and 1.27.1. The round-2 audit (E2-ED1) planted five key-decoding bugs those gates missed —
three of them universal forgeries — so the gates now also test the decoding directly against Go's `Point.SetBytes`
(478 keys) and run the auditor's forgery probes (1,104 records), and all 18 planted bugs fail them; the auditor's own
1.5M differential cases and 3.3M decoding checks found no disagreement with Go. Round 3 (E3-ED1–ED4) found three
forging classes each held by one witness, and SHA-512's zeroing of its padded buffer held by none, so the gates
gained edges.vec's three generated families (607 records with the 25 named cases: special (R, S) pairs under honest
keys, −R under a known secret, a small-order R in every lax encoding), an `.asB` probe per probe key (probes.vec:
1,242 records), and SHA-512 and `Ed25519.verify` run on dirty memory: 25 planted bugs fail them, the round-3 seven
none by one witness alone, and the verifier's code is unchanged (fold F11).

6.7 The gate, `PausableGate`: as the Solana plan's pausable ISM (§5.8, §6.1) — `verify` requires its caller to be
the mailbox, no quarantine (`client.quarantineEnd() ≤ block.timestamp`), no flag; charges a token's mint limits
(the per-message maximum and the rolling day bucket) when the recipient is one of the two tokens, the amount read
from the token message (above 2^64 − 1 refused); then requires `client.verify`. Four seats and two watch slots, a
flag per key with the block it was last set at, `holdSince` and the dip rule, `unpause(beforeBlock)` by the owner
only when no hold stands, `setGuardians` (at most two seats plus the watch slots, never a seat's current key,
clearing the replaced keys' flags and ending a hold at once when its own clear leaves fewer than two),
`setMintLimits` by the owner, taking any values, a maximum of 0 included (r/hlgov's kind-12 builder refuses that,
§13; E3-CF12). `moduleType()` answers the client's.

## 7. Proposal 2 — Ethereum → gno.land on proofs

7.1 What must be proven for a burn: that Ethereum's sync committee signed a beacon header that is finalized (the
light-client protocol: the attested header's finality branch to the finalized header, signed by at least 2/3 of
the 512-member committee — the protocol's own bar is `bits·3 ≥ len·2`, and its force-update path, which applies an
update without that supermajority, is refused — the committee itself followed period by period from the previous
committee's signature), that the finalized header commits to the execution block (until Glamsterdam the header
carries the execution payload header with its state and receipts roots; under Gloas it carries only the execution
block hash, and the execution header is a witness hashed to it), and that the state under it holds our
`MerkleTreeHook`'s tree containing the message id (a storage proof of the tree's branch and count, keccak256
Merkle-Patricia nodes, which gno.land's native keccak256 verifies directly), or that the receipts hold our mailbox's
`Dispatch` log of the message. The sync-committee part, BLS12-381 aggregate signatures that gno.land cannot verify
natively, is proven in a zkVM (Succinct's SP1 Helios is that program; it already follows Gloas) and wrapped to
Groth16 over bn254, which a realm verifies with gno's `PairingCheck` (the Solana plan §7.2's pattern). The realm
pins the program's verification key and the bootstrap committee; it has no guardian key (SP1 Helios's own contract
lets a guardian swap program keys — copied, that would be a key over every credit).

7.2 What it trusts: the sync committee is a random 512-validator sample, resampled about every 27 hours; a
committee that signs a header the chain never finalized is not slashed for it, so the path's safety is "2/3 of a
random committee is honest", weaker than Casper finality, which Proposal 2 states; the zkVM and its verifier, and
the Groth16 setup. Caps and the delays stay.

7.3 When, and what it depends on: Ethereum's forks. Every fork that reshapes the light-client containers (Capella,
Deneb, Electra and Gloas each did; Gloas moves the finality and sync-committee branches and drops the execution
payload header) needs a new prover program and verification key, so the verifier realm is built fork-aware — a
GovDAO decision registers the next fork's verification key, effective from that fork's epoch, at least 7 days before
it — and Proposal 2 keeps the signers as a slow fallback rather than emptying them: a credit on the signers alone
then waits 7 days (a decision's delay), a credit with a proof lands at once, so a fork the verifier missed slows the
exit and never closes it. It also waits on gno.land's bn254 gas table on mainnet, on the prover (the operator's, or a
prover network), on an audit of the program and the verifier realm, and on a month of shadow running (every
signer-passed message also proven, zero disagreements).

## 8. What a GovDAO member checks

As the Solana plan §8, with `hleth audit` for `hlsol audit`, the code hashes of §5.9 for the program dumps, the
sixteen deployments' Etherscan or Sourcify matches, every masked immutable's value against §5.8's, each proxy's
EIP-1967 implementation and admin slots, and `hlwatch` reading both objects on Ethereum. The member runs `hleth
audit` with a gno source of their own (`-gno-rpc`: the machines' config is the operator's, its gno sources read beside
the member's and never in their place, its routes' locks only noted, E5-CF3), the four path flags and the launch
notice's values (`-seats`, `-watch`, `-beneficiary`, the expected locks `-expected-lock-gnot`/`-bubble`: the launch's,
never the keys or the beneficiary as a kind 5 or 7 left them), every flag of theirs given — a PASS on nothing less,
each value a decision moved held to that decision, r/hlgov's decisions in flight held to its own record of them (§5.8;
E3-CF2, E3-CF9, E4-TL1, E4-TL6). Before voting a kind 8 or
9 a member reads, as `hlwatch check` prints them, the target's code against a reproducible build, its storage layout
against the running one (append-only by the layouts' types, §0; without both layouts LAYOUT NOT CHECKED, and no
"immutable"; E4-GT3), and that it is not an EIP-7702 account — the decision read from r/hlgov's own record of it once
dispatched (`-decision N`), or before from the proposal's description (`-proposal ID`), which is advisory: on gno
v1.5 GovDAO shows the realm an executor was created in as the executor itself states it, so a stranger's proposal can name r/hlgov's builder and describe any
target — it can never make r/hlgov dispatch, but its voters would read the wrong code — until r/hlgov records each
request its builders make (not built; E3-GT7, §9). For a kind 8 with a call, the member simulates the call as it will
run — an anvil fork of the chain, `HlAdmin.execute`, the call on its 2,000,000 gas — since no tool of ours does
(`hlwatch check` decodes the call against the implementation's ABI and runs nothing; not built, §12; E4-FC7); no
initializer may branch on `gasleft()` or swallow a failed call (a `try`/`catch`, a low-level call whose failure it
ignores) — this rule is the guard, not the budget: the budget makes the outcome the same for every executor only on
an Osaka chain, and there only to within the executor's warm set (a few thousand gas, E4-CT2); on a chain with
EIP-8037's state gas the call's state growth is paid from the executor's reservoir, which no contract can read and no
simulation shows (an `eth_call` applies no split: a member sees only the no-reservoir path), so an initializer that
branches on gas or swallows a failure runs whichever branch the executor's transaction gas picks (E4-CT3) — and an
initializer that needs more gas than the budget — on an EIP-8037 chain, one that grows more state than the budget
holds with no reservoir (≈110K a fresh slot on Sepolia: some 17 slots) — is split into steps, or done by a successor
(E3-CT4, E4-CT3; the budget is a constant of `HlAdminExec`, which only a kind 9 to a new `HlAdmin` changes, E4-RT4).
For a kind 9, the successor's `HlAdminExec` and start are the members' to hold (§6.5; E3-CT1). The operator's claims no member can check (the Solana plan §8.7's list) gain the GNOT/ETH and
BUBBLE/ETH rates the budget values fees at (`eth.prices`, dated by `eth.pricesAt`; `prices:stale` alarms while they
are undated or older than a week, long against GNOT's moves, §2): the launch notice names who sets them, from what
source, BUBBLE's from its GnoSwap pool, and how often they are re-dated (§5.5; E3-FC7, E3-GS11, E4-AS2); the
Ethereum providers' operators and tiers (§5.6); each machine's gno.land endpoint (§1), every signer's and monitor's
its own node (§11.10); and that the signers' public endpoints serve their own TLS, or are reached by the relayers
unproxied where a proxy shares their port (§5.6; E5-RT2).

## 9. Risks

As the Solana plan §9, with these rows changed or added:

| Risk | Answer |
|---|---|
| `Ed25519.sol` accepts a signature tm2 refuses (or refuses one tm2 accepts) | §5.9's vector sets, the differential run and the mismatched-key tests; a refusal of a real signature stalls the client (fail closed: the deployment's ending if it persists), an acceptance is a forgery path, which is why the verifier is the first thing the audit and §11.2's review read. |
| Gas spikes (the 30-day maximum was 24.9 gwei, 190 times the median) | Route deliveries and their updates park above `maxBaseFee` and resume, and a header is bought only when the waiting fees cover it (§5.5); decisions, executions and flags never park; a sustained rise is a GovDAO fee raise. |
| A set grows past what one transaction verifies | `update` and `proveHeader` stage themselves (§6.3) up to the client's compiled maximum; the cost grows with the set; `updater:staged` and `client:set-bound` alarm (E3-FC4). |
| A deploy-time value is wrong (a compromised deployer key or a setup bug: a token over a stand-in mailbox, a scale, `HlAdmin`'s sender, a client rooted in a header of the deployer's making) | `finishSetup` checks every binding a masked code hash cannot see, and `hleth audit` prints each masked value beside the value it must have, holds the trust root, the start, the seats, the limits and the beneficiary to sources the deployer does not write, and is UNVERIFIED, never PASS, without the member's gno source, path flags and launch values (§5.8; E3-CF2, E3-RT5, E3-GS4, E4-TL1); the audit precedes the realms' upload. |
| A compromised deployer plants state no read can see (a header entry, a balance, an initializer flag, a decision record) | It never holds upgrade power (the `ProxyAdmin` is `HlAdmin`'s from its second transaction); `hleth audit` compares every creation transaction with the reproducible build and its arguments, refuses any upgrade or ownership move past construction that no executed decision explains, simulates every `initialize` and takes no stored flag as proof; a launch's decisions start above a start held in `HlAdmin`'s code, check 34 holds the record to that start and the hold record to what the keys' flags could have left (E4-CT1) — from above only, so the audit holds it from below: no log in `HlAdmin`'s creation transaction, which a planting initcode could fill as `HlAdmin`, and every hold moment between its creation and the block read (E5-CF1) — and it runs only its own `HlAdminExec` — a planted `setupDone`, which skips check 34, is refused by the audit alone (E3-CT2); a burn below the launch line (`launchNonce`) is no burn (§5.6, §5.8; E2-CT1, E2-RT1, F5). |
| A kind 8 or 9 names a mutable target (an EIP-7702 account, a proxy of its own, code built to pass the checks) | `HlAdmin` spends a delegation designator; the code hash pins bytes; kind 8's after-checks catch an honest mistake, never code written to answer them falsely, and kind 9 binds the successor's `HlAdminExec` and start only through its code hash: both are the members' review, `hlwatch` calling code immutable only against a reproducible build; the 7 days and a hold are the brake (§6.5, §8; E3-RT8, E3-AS7, E3-CT1). |
| A stranger's `dispatch` poses as a burn, or as a send in flight | Only dispatches by a token router to its route, beside the token's own transfer event, are burns, and only messages a route realm of the token's line sent are sends (§5.6; E3-RT1). |
| A stranger's huge `Dispatch`, or a long queue of waiting claims, stops the walks that read every nonce or every claim | `Dispatch` refuses a body above 2,048 bytes, and every page the machines read stays under a third of the 3B query budget at its node figure, held by the realms' render-gas gate (§5.1; E3-RL1, E4-RL2); the waiting claims are read from each route's own index, whose page costs the same at any depth, never from p/hlroute's walk (§5.2, §5.9; E4-RL1). |
| A migration page outgrows gnoland-1's block, and the migrated route freezes | A successor pulls pages of 50 records, never p/hlroute's 500 (5.28B gas on a v1.5.1 node for 500 open claims, past the 3B block, while from the migrate on the predecessor refuses everything but a re-point or a cancel): a page adopted whole is ≈0.54B at a route of 100 open claims and ≈1.04B at its costliest in a backlog of 3,000, and the realms' migration gate holds a page's `Pull` and `Adopt` under a third of the block at their node figures; a restore names 128 ids at most, so one at once fits the governor's `Govern` before the yield (§5.3; E5-RL1, E5-RL2). |
| A stranger's sends ride a header the budget did not book them under | A decision's or a validator change's header books no route send, and a decision's header proof, the backstop's, or a stranger's update or header proof books nothing — the updater's own header proofs for route sends book them by their trailer, as its updates do (E5-CF2): sends beside a decision, a pause or a front-run header proof are held to their own fee; a stranger's `Reemit` after each six-hour header buys no second header, the send being delivered from the header its trailer names, one booking a send (§5.5; E4-GS2, E4-GS4, E4-CF2). |
| A stranger re-emits a waiting message every block | Each re-emit is one more event of it; the walk reads each step only its dispatch, its newest four events, those whose header the machine saw recorded and its newest at or below the trusted height, in batches of at most the provider's `batchSize`, and judges the rest at the finalized block, dropping the unrecorded — two reads apiece, an hour apart — so the decisions and every send still go (≈8 GNOT an hour of re-emits had failed every walk past geth's batch cap of 1,000 and stopped gno.land → Ethereum for good); `outbox:walk` alarms a walk failing two minutes (§5.7; E5-GS4). An old re-emit whose header the stranger proves later is not seen: the message waits for its newest event's header. |
| A stranger's transaction beside a dispatch spoils its block's results | A failed transaction's leaf is rebuilt with its error from §13's closed set, so failures spoil nothing; a type outside the set alarms (`results:schema:<type>`) and the block's messages are re-emitted (§5.7; E3-RT2, E3-GS1). One door stays open: a string that is not valid UTF-8 in any event attribute or panic message in the block — `chain.Emit` takes any bytes, a panic message is cut at 1,024 bytes mid-rune, and a realm that repeats a caller's string in its panic passes the caller's bytes on (a failed `MsgCall` needs no coins and no `MsgRun`, E4-GS1) — reaches `/block_results`' JSON as U+FFFD while the hashed leaf keeps the bytes, so the block stays unprovable from public RPC JSON; the relayers re-emit with a backoff (a minute, doubling, an hour) and alarm (`results:mismatch`, `reemit:poisoned`), and whoever keeps one such cheap transaction beside each dispatch and re-emit keeps gno.land → Ethereum waiting, decisions included — a decision is never parked or ended by it, and is delivered within a backoff once the stranger stops — the guardians' hold the brake meanwhile. This deployment's realms no longer open that door themselves: every caller's string a panic or an error of theirs repeats is quoted in ASCII and cut at 64 bytes, an event of theirs carries only values they validated, a string handed to p/hlroute (which repeats it whole) is refused unless it is ASCII, and no message of theirs reaches the cut — held by `eth/scripts/check-echoes.go` in the realms' suite (F15d; E4-GS1, E4-RL6), default-deny since fold F17c: every operand of a panic, an error, an event value or a page is a literal, a constant, a sanitizer's call (ASCII by construction, each with a bound: the quoting echo, numbers, hex, addresses — `strconv.Quote`, which keeps printable runes outside ASCII, no longer one) or named by a reason on its line, and a message whose own text is not ASCII is bounded under the cut — thirteen constructs that passed the old checker's taint rules now fail it (E5-RL3); and a verifier GovDAO activates must refuse by a returned error, never a panic, which no realm can catch or quote (§5.1; E5-RL4). Any other realm of gnoland-1 can still open it (r/sys/params repeats a path), and anyone's long panic is still cut mid-rune. Closing it needs a lossless results source — our own gnoland-1 node serving the raw results, or an upstream RPC change — Jae's decision (§11.11). |
| A stranger's updates run the client past waiting sends and decisions | A message left below the trusted height after one re-emit, and a decision at once, is proven by a header proof no later update pre-empts, and the six-hour rule holds there too (§5.5, §5.7; E3-RT4); a stranger who front-runs the updater with the identical header makes the sends under it not the updater's, so they are held above ≈1.9 gwei (a send alone in its leaf; ≈0.97 for one in a leaf of four, both moving with the configured rates, E4-FC8) and re-emitted, each cycle costing the stranger a full update. |
| A gno.land upgrade changes an encoding the client verifies (the header hash, the sign bytes, a validator or results leaf, the event, Go's ed25519 rules) | gnoland-1 upgrades often (three halting upgrades in its first week; v1.5.1 since 2026-10-13), and every input of the client is v1.2.0's byte for byte today (§2). The monitors and `hlwatch` rebuild from their own node what the client verifies — every header they read hashed as the client hashes it, and once a minute the block below the head whole: its set's leaves, every precommit under Go's ed25519 over the client's sign bytes and the tally, its results leaves against the next header's `LastResultsHash` — and alarm `gno:encoding` on a difference, `gno:version` for a day when the node's reported version changes (gnoland-1 reports tm2's constant there, so the rebuild is the guard that does not depend on it); the updater, the relayers' backstop and `proveOld` hash every header they would submit as the client does and, on a difference, submit nothing and alarm `gno:encoding` too (the integration lane I1; E4-CF6, E4-FC4). A results root that differs in a block whose JSON carries U+FFFD is the non-UTF-8 door above, not an upgrade — `results:lossy`, an alarm `hlwatch` can be green beside, the probe taking the nearest of the five blocks below the head that the JSON carries whole, and saying it did not read when none is — and `gno:encoding` stands for a mismatch in a block the JSON carries whole, or a leaf of a type outside §13's closed set, which no stranger's transaction can make (E4-AS1, E4-CF1, E4-GT4). A client that accepts both encodings must be voted, delivered and executed by kind 8 before the upgrade's height (7 days at least); an upgrade with less notice stops gno.land → Ethereum and `HlAdmin`'s decisions with it (§6.4), burns back still exiting through the signers (E3-AS3). |
| A flood of covered sends drains the keys decisions ride | The budget books every header and delivery against the sends' own fees, but the fees are GNOT with the treasurer and the ETH is the updater's and the relayers' keys' (§5.5): such a flood spends them at the rate the updater buys, one update in two minutes; the two keys' floors (`float:updater`, `float:relayer`: a decision's header at the emergency cap, E4-FC5, E4-RT1) and the 7-day runway alarms warn, and the operator refills; no daily cap on a key's route spending and no decision float route traffic cannot spend exist (E3-RT6; §11.4). |
| Strangers' dust burns, or a large transfer's installments, drain the relayers' gno key | A credit locks ≈0.9–1.7 GNOT of storage deposit from the key, never returned to it while ugnot is restricted, so a burn below its route's fee is not processed by the relayers (alarmed `inbound:dust:<symbol>`; anyone may process it at their own cost), and the signers and monitors keep it as known dust (§5.5, §5.6); a `Claim` counts as paying only at a unit a claim, else waiting the meters' refill, and the routes pay a later installment only once min(what the claim owes, the fee) has refilled: a 1,000 GNOT transfer to a fresh recipient is ≈50 `Claim`s and ≈1.8 GNOT of the key's gas, where it was ≈5,300 and ≈185 GNOT (§5.4, §5.7); the key's float is sized on the measured deposit, ≈2,000 GNOT, its runway alarmed (§5.5, §11.4; E5-GS1, E5-GS5, E5-RT5). |
| The exit's fresh deployment and what it leaves the launch's | §6.4: a fresh r/hlgov compiled with the new contracts; the routes' refusals, realm lists, kind 12's floor and hlmailbox's contract list stay the launch's, and no route migration completes after the exit, since only the launch r/hlgov writes the enrolment note — a hlmailbox decision naming the r/hlgov whose notes count would lift it, a new power not built (E3-CF5; §11.12); the launch r/hlgov keeps that power after the exit, retired by the members' rule alone, which nothing enforces and no tool alarms (E4-RT3). |
| A stranger's GovDAO proposal claims r/hlgov's builder | On gno v1.5 GovDAO's page shows the realm an executor was created in as the executor states it, so a proposal can name r/hlgov and describe any target; it can never make r/hlgov dispatch, but voters reading it would read the wrong code. `hlwatch check -proposal` says so, and members read a kind 8 or 9 again from r/hlgov's own record (`-decision`) once dispatched, before it matures; the proposal reading stays advisory until r/hlgov records each request its builders make (§8; E3-GT7). |
| A leaked set pre-stages forgeries before its rotation | Stages are keyed by the epoch and the expected set and re-checked live at the finish; the watch alarms on stages for unknown headers, and at once on a stage more than an hour of blocks above both heads it reads (§6.3; E4-GT5). |
| A hostile decision after the yield (a forger's, or a thief's proposal that passed) | GovDAO's cancel within its 7 days, as on Solana, the guardians' hold the brake meanwhile (§6.5) — but on gnoland-1 only the three keys `vm:p:run_submitters` admits can propose that cancel, since every bridge proposal is a `MsgRun` (§1): a cancel, an unpause, a void or a rotation waits on one of GovDAO's three T1 members, and while none of them can propose, the hold is the only brake — the limit §11.9 asks the yield to lift (E3-RL5, E4-RT2). |
| Ethereum's finality stalls, or a provider's finalized answer stands still | Signers sign nothing newer (for them, liveness only), and a monitor's pending reading waits with them (§5.6); route sends to Ethereum continue (the client depends on inclusion, not on Ethereum's finality), but `HlAdmin`'s decisions past the next one wait for finality, since the updater buys a header only for the number after the last recorded at the finalized block (§5.7; E3-GS6, E4-FC6). For the watch it is not liveness alone: every Ethereum-side rule of the monitors and `hlwatch` reads at the stalled block while deliveries mint, so a forged mint or decision above it is unseen until finality resumes — a reading whose finalized block is more than half an hour old by its own time is blind, said and never green, alarmed `eth:finality-stall` and at flag level past the hour (the signers' alarm after an hour standing still), a guardian's flag and hold the brake meanwhile; and two providers' finalized answers more than two epochs apart are a disagreement, so one provider standing still cannot blind the pair (§5.6; E5-RT1). Whether the monitors then flag the gate, or read at `latest`, is Jae's (§11.13). |
| A deep Ethereum reorg | The signers read only finalized blocks; the relayer re-sends a delivery or update a reorg dropped (each is idempotent: the mailbox's delivered mark, the client's height rule, `HlAdmin`'s sequence). |
| An RPC provider lies | As on Solana: two per signer and monitor must agree — on the finalized block's hash, time and parent too, and within two epochs of each other, so one standing still at an old real block is a disagreement (E5-RT1); the identity and the refusal rule catch a forged burn's credit at the meter; a provider stepping back to an older real finalized block reads below the floor, no verdict (§5.6; E3-GT9); one trailing the signers' finalized block, or lying about it, holds a BROKEN reading unjudged rather than breaking it, but only within what both providers show above the index — an excess past that is a break at once — alarmed after two minutes and at flag level after an hour, never flagged on one provider's word (§5.6; E4-GT1, E5-GT1); both providers omitting an honest burn's log above the index (two faults) makes its lag a break at once, as it makes a waiting claim forged (E5-GT4). The updater and the relayers read through one provider, which decides their spending and never a credit: a header's booking rests on its recording transaction's signature, calldata and receipt, so a lying one can withhold a booking (the send waits) and make one only from a transaction `eth.updater`'s key signed that carries the header, with a receipt it forges too (§5.5; E5-RT3). Independence is by operator, which no configuration shows (E3-AS4). |

## 10. Costs and timeline

E-M1 (realms), E-M2 (contracts) and E-M3 (machines and tools) are built on 2026-10-14 (`BUILD.md`), then the audit
loop; the Sepolia deployment and its two weeks, then mainnet on Jae's word. Ethereum's costs at deploy and per
transfer are E-M2's measurements (§2); the deploy's gas and the floats are §11.4's ask. The local end-to-end run
(`eth/bridge/local/e2e.sh`: anvil pinned to Osaka beside gnodev v1.2.0) passed on each round-5 fold (F17a–F17d, each
on its own tree, in 450–484 seconds). Its header lists what it exercises: five decisions end to end through r/hlgov
and `HlAdmin` — kind 7; kind 8 with a call, the gate upgraded by `upgradeAndCall` to an implementation built in the
run, the new code's initializer run once through the proxy (its mark read back, a second run refused) and the same
execute, left 1,500,000 gas, refused `CallStarved()` at the block before the relayer's, its EIP-1967 slots then held by
the monitor's and `hlwatch`'s slot watch for the rest of the run and seen at work (the implementation slot moved by
hand: `proxy:slots` alarmed and `hlwatch` not green until it is put back); kinds 3, 5 and 6 (a second router of
BUBBLE's, for another domain, none before it), each read back on Ethereum — beside a kind 12 cancelled and the kind 2
unpauses; the gate's refusal under a flag for both tokens (the relayer's own deliveries of a BUBBLE and a GNOT send,
simulated at a block under the flag, revert `Paused()`, and pass at the block before each delivery), both Ethereum
strokes asserted; a stranger's `MsgRun` refused in the allowlist's own words after a control that goes before the
param is set; and `hleth audit` of the governed bridge UNVERIFIED from the operator's config alone, and PASS with the
member's own gno source and launch values beside it (its gno sources two spellings of one node: the agreement code,
not independence) (E4-TL5, E4-RL4, E5-CF4). What it does not: a kind 9 (cmd/hleth's anvil tests hold it), the
executor's starved resend (anvil's estimate finds the least gas that passes, so no execute starves there: the agent's
and forge's tests hold it), a staged update, the backup relayer's takeover, sender replacements, the six-hour draw, a
set of more than one validator. It sets `run_submitters` through GovDAO to GovDAO's one
member there, as gnoland-1 admits its three, so a stranger's `MsgRun` is refused there too and the batch re-emit
runs as `MsgCall`s (E3-GS10, E3-AS1). Open
before Sepolia: the stage step (§4; E3-CF7); a provider whose RPC gas cap takes `HlAdmin`'s ≈66M estimate there
(geth's default is 50M) and a gate that runs the setup against a Sepolia fork, which anvil cannot yet (§2; E3-AS2,
E4-FC8); and the local run on gnoland-1's own gno (v1.5.1, §2; the realms' suite passes there since round 4).

## 11. Decisions that are Jae's

1. As the Solana plan §11.1.
2. Review and audit: as the Solana plan §11.2, the scope being `Ed25519.sol` first, then the client, the gate and
   `HlAdmin`, and Hyperlane 12.2.0's token router (`TokenRouter`/`HypERC20`), which no published audit covers (§1).
3. The governor key for this deployment (§11.16 of the Solana plan: the cold key for real funds).
4. ETH: the deployment (≈37.3M gas by the test's count, an upper estimate, §2: ≈0.005 ETH at the 30-day median,
   ≈0.075 ETH at 2 gwei) and the floats — the relayers' key 0.4 ETH: ≈0.2 for deliveries (≈5,100 at the 30-day
   median, ≈715 at 1 gwei by the measured gas; ≈4,600 and ≈640 by the budget's model) and ≈0.174 for one decision's
   header at the emergency cap, since its key is the decisions' backstop (§5.5; E3-CF8, E3-FC3), a floor alarmed
   (`float:relayer`, E4-RT1); the updater's ≈0.4 ETH: its floor, a decision's header at the emergency cap of 50 gwei
   (≈0.174 ETH at gnoland-1's set, alarmed `float:updater`), and ≈0.2 for its route headers on top (≈1,030 updates at
   the 30-day median, ≈145 at 1 gwei; ≈23 days at its planned 30 a day) — the 0.2 asked before counted once as both,
   and a key funded with it fell under its floor at its first route header (E4-FC5); the four guardians' and two
   watch keys' 0.05 each: ≈1.1 ETH as listed (≈1.17 with the deploy at 2 gwei), so ≈1.2 ETH with margin, Sepolia's
   first (free, by faucet). Not built, and his to ask for: a decision key and float route traffic never spends, and a
   cap on a key's daily route spending (E3-RT6). And GNOT: the relayers' gno key ≈2,000 GNOT — ≈90 GNOT a day at the
   planned 50 credits (the storage deposit a credit locks, ≈1.7 GNOT, and its gas), ≈22 days; the 500 GNOT asked
   before lasts ≈5.6 days and alarms from launch day — refilled by the treasurer from r/hlfees, where a credit's 20
   GNOT fee covers ≈11 credits' deposit (§5.5; E5-RT5).
5. The fees on the Ethereum routes (§5.5's proposal: one fee each way, 20 GNOT and 400 BUBBLE, minimums 40 GNOT and
   800 BUBBLE — 20 GNOT covers a send in a batch of four up to ≈0.61 gwei by the budget's model and 10 GNOT only to
   ≈0.31, the 30-day mean being 0.38; the launch notice prints BUBBLE's GnoSwap price beside its 400, E3-FC7, E4-FC2),
   `maxBaseFee`, and how often the operator re-dates the GNOT/ETH and BUBBLE/ETH rates the budget values fees at:
   `prices:stale` waits a week, and GNOT/ETH moved ≈20.5% in four hours (§2, §5.5; E4-AS2).
6. The guardians on Ethereum: the same four people as the Solana bridge's seats (recommended), each with an
   Ethereum key.
7. Token metadata on Ethereum: names and symbols ("GNOT", "BUBBLE"), decimals 6.
8. The namespace the realms deploy under (the Solana bridge's operator namespace with the `eth/` segment,
   recommended).
9. The yield and `run_submitters` (E3-RL5): every bridge proposal is a `MsgRun`, and gnoland-1 admits a `MsgRun`
   from three keys, GovDAO's three T1 members (§1), so after the yield a member outside that list can vote and
   execute and cannot propose a cancel, an unpause, a void or a rotation. Recommendation: make the yield conditional
   on `run_submitters` admitting GovDAO's members (or on a GovDAO path that takes a `MsgCall`), and say in the yield
   vote's words who can propose: until then the answer to a hostile decision rests on those three keys (§9; E4-RT2).
10. The endpoints the machines read (E3-AS5, E3-AS4, E5-RT6): a gnoland-1 node of its own for every signer and
    monitor — a requirement, not a preference: the cut reads its state and its head from one node, and a load-balanced
    public endpoint can answer them from different backends, which flags an honest credit, pausing the gate for a
    day (§5.6) — so the public endpoints the Solana deployment's machines read serve no signer or monitor here, and a
    machine that reads them has their per-IP limits named in the launch notice, every poller of both bridges counted
    under them; and the Ethereum providers — two per signer and monitor of different operators, full history from
    `eth.deployBlock`, paid plans where the free ones cannot carry a signer's round, and for the updater and the
    relayers one that serves the client's logs of the last three days (§5.5, §5.6) — named in the launch notice with
    their operators, tiers and cost.
11. A lossless results source (§9): our own gnoland-1 node serving the relayers the binary results (the ABCI
    responses of its state store), or an upstream change — `block_results` serving the bytes, or `chain.Emit` and the
    VM refusing a string that is not UTF-8 — closes the last way a stranger keeps gno.land → Ethereum waiting. Round
    4 closed this deployment's own doors (its realms repeat no caller's bytes, E4-GS1, E4-RL6), and round 5 made the
    checker that holds them default-deny and kept a verifier's panic out by rule (E5-RL3, E5-RL4); any other realm's
    failed call, or anyone's long panic cut mid-rune, still opens it, so the source stays a launch item.
12. Route migrations after §6.4's exit: only the launch r/hlgov writes hlmailbox's enrolment note, so after a fresh
    deployment no route migrates; a 7-day hlmailbox decision naming the r/hlgov whose notes count would lift that,
    a new power over the realms, not built — and, hlmailbox being immutable once deployed, built before its deploy or
    never. Meanwhile the launch r/hlgov keeps that power after the exit, retired by the members' rule alone: nothing
    enforces it, and no tool alarms a proposal of its builders then (E4-RT3).
13. A finality stall (E5-RT1; §5.6, §9): while Ethereum's finality stalls, or the watch's providers answer an old
    finalized block, the monitors and `hlwatch` read every Ethereum-side rule at that block — blind, said, never green,
    alarmed `eth:finality-stall`, at flag level past the hour — while the light client keeps accepting headers on
    inclusion and deliveries mint, so a forged mint or decision above that block goes unseen until finality resumes,
    the guardians' flag and hold the only brake (the plan red team's reading: Ethereum mainnet's longest stall so far
    was about an hour, in May 2023, and the Holesky test network went about two weeks without finality in 2025). The
    question: past an hour, should the monitors flag the gate — a pause of gno.land → Ethereum, liveness only — or read
    the gno.land → Ethereum rules (the supplies, the client's record, `HlAdmin`'s decisions) at `latest` meanwhile,
    where a false flag only pauses? Today the stall itself flags nothing: the watch is blind and alarms at flag level.

## 13. Appendix — byte-level facts for the implementer

gno.land: the Solana plan §13's sign bytes, header leaves, validator leaf, commit rules, results leaf and `Dispatch`
event, byte for byte, except its ed25519 sentence: here ed25519 is §2's and §6.6's (Go's lax key decoding, the key's
bytes as given in the challenge), not the Solana client's strict check. `pkg_path` is this deployment's hlmailbox
path, compiled into `GnoLightClient` as its keccak256. `moduleType()` is 0 (UNUSED) for the client and the gate, as
the Solana client's `ISM_TYPE` is: Hyperlane's own relayer builds no metadata for that type (it would send empty
metadata for NULL, which the client refuses), so only a relayer that builds this metadata delivers. gnoland-1 runs
v1.5.1 today, and every one of these is v1.2.0's, byte for byte (§2; E3-AS3). A failed transaction's results leaf
carries its error as an amino Any of one of 43 registered `abci.Error` types — std 25, bank 3, vm 14 and
`abci.StringError`, the one with a field (its string) — the same set at v1.2.0 through v1.5.1
(`bridge/gnolight/errortypes.go`), so the relayer rebuilds it from `/block_results` (E3-RT2); a block is unprovable
from that JSON only for a type outside the set or a string the JSON cannot carry (§5.7, §9), and the proof takes the
smallest leaf carrying the message's `Dispatch`, never a failed one (the client refuses a leaf with an error).

Decision messages (r/hlgov → `HlAdmin`): body `kind u8 ‖ seq u64 BE ‖ params`, every integer big-endian, every
Ethereum address 32 bytes left-padded (a non-zero top 12 bytes makes the decision spent at execution); kind 6's router
is a remote domain's 32-byte address, which `HlAdmin` takes whole and r/hlgov's builder takes only with its top 12
bytes zero, for every domain (E3-CF11). The kind table, frozen
before r/hlgov deploys:
- 1 `cancel` — as Solana's: `salt u64 ‖ form u8` then `count u8 ≤ 16, (channel u8 = 0 ‖ number u64)…` or `channel u8
  = 0 ‖ from u64 ‖ to u64`.
- 2 `unpause` — `object u8`: 0 the gate, 1 `HlAdmin`.
- 3 `set token ISM` — `token u8 ‖ ism 32`: the gate's address or zero (zero falls back to the mailbox's default ISM,
  which is the gate for the deployment's life).
- 4 reserved: recorded, spent at execution.
- 5 `set guardians and watch keys` — `count u8 ≤ 4 ‖ (seat u8 ‖ key 32)…`: seats 1–4 guardians (two at most), 5–6
  watch slots; the current key of any seat or slot spent.
- 6 `enroll router` — `token u8 ‖ domain u32 ‖ router 32`; for gno.land's domain the builder accepts only the
  token's own live route; a router for any other domain must have its top 12 bytes zero (an EVM address — no
  other chain's router is needed here); Ethereum's own domain and gno.land's other domain are refused; r/hlgov's
  builder refuses a zero router or domain — unenrolling is no decision of this
  table (on Ethereum, enrolling a zero router would leave the domain enrolled at zero, a send to it a message to
  address 0; `HlAdmin` would call `unenrollRemoteRouter` for one, which no builder can make).
- 7 `set protocol fee` — `fee u64 (wei, ≤ ProtocolFee's maximum) ‖ beneficiary 32 (non-zero)`.
- 8 `upgrade` — `proxy 32 ‖ implementation 32 ‖ codehash 32 ‖ call` (the call is the rest of the body, possibly
  empty, at most 407 bytes within the 512-byte bound): the proxy one of the five (the mailbox, token 0, token 1, the
  client, the gate), never anything else (spent); at execution the implementation with no code yet pends
  (`NoCodeYet`), an EIP-7702 designator or an `EXTCODEHASH` other than `codehash` is spent; executed as
  `ProxyAdmin.upgrade(proxy, implementation)` when the call is empty and `ProxyAdmin.upgradeAndCall(proxy,
  implementation, call)` otherwise (forwarded exactly 2,000,000 gas: the initializer runs on at least ≈1.92M of it; an
  execution with too little left for that reverts `CallStarved`, pending; E3-CT4; on a chain with EIP-8037's state gas
  that bounds execution gas only, the call's state growth being the executor's reservoir's, §6.5, §8, E4-CT3; a
  constant of `HlAdminExec`, which only a kind 9 changes, E4-RT4), so a new version's initializer runs in the upgrade
  and is never open to a stranger; and the execution reverts (pending) unless afterwards the proxy's admin is still
  the `ProxyAdmin`, the contract's owner (the client: its admin) still `HlAdmin` (`AuthorityLost`), and `ProxyAdmin.getProxyImplementation`
  the decided implementation (`ImplementationMoved`), and every binding of that proxy that `finishSetup` holds and no
  decision moves still the deployment's (checks 9–14, 17–23 and 29 as they apply to it; a token's ISM the gate or
  zero: `AuthorityLost`; E3-CT3) — so a new implementation must answer those views (one that drops, say, `feeHook()`
  reverts the execution: pending, for a cancel), and the checks catch an honest mistake, not code built to pass them
  (§6.5). r/hlgov refuses a zero codehash and
  the empty code's (keccak256 of nothing).
- 9 `transfer to successor` — `successor 32 ‖ codehash 32`: with no code yet it pends (`NoCodeYet`); spent unless the
  successor's code is no EIP-7702 designator, its `EXTCODEHASH` equals `codehash`, its `predecessor()` is this
  contract, it has not inherited, it names no successor of its own, its clock factor is this build's, and its eleven
  power-granting bindings are this contract's (`"bindings"`, §6.5); its `HlAdminExec` and its start are bound by
  its code hash alone, which members hold to the build (`hlwatch`, §8; E3-CT1); a successor whose `successorInit`
  refuses its own `HlAdminExec` (`ForeignExec`) or whose views revert leaves it pending (E3-CF10, E3-FC9); never this
  contract's own address (a malformed address is spent as unparsed parameters).
- 10, 11 reserved: recorded and spent at recording.
- 12 `set mint limits` — `token u8 ‖ maximum u64 ‖ dayCap u64` (base units); r/hlgov's builder refuses a maximum of
  0, a day cap below the maximum and a maximum below the launch route's (§5.4), that last checked again when the
  proposal executes; `HlAdmin` and the gate take any values.
Kinds 3–9 and 12 are the 7-day group.

`GnoLightClient.verify` metadata: `abi.encode(bytes32 headerHash, uint32 total, uint32 index, bytes32[] aunts,
bytes leaf)`. Checkpoint signed by the signers for an Ethereum burn: Hyperlane's digest with this bridge's
convention, `digest(origin 1, our MerkleTreeHook as bytes32, root 0, index = the message's nonce, id)` as
`p/hyperlane`'s `CheckpointDigest`, signed with ed25519 (the gno verifier has no secp256k1, and checks neither root
nor index).

## 12. Review log

`AUDIT.md` (this directory) keeps the audit loop's method, each round's log (its audits, the severities they found,
its folds) and what changed from what Jae was told; each round's findings are in `audits/r<n>/`, and the entries below
map them to their folds and to this plan by group, not one by one (E4-FC2).

Round 3 (2026-10-14, the second code round; nine audits, read-only, of the tree at d31aff0c with plan e7; findings
in `audits/r3/`; this plan's e8): the plan red team (E3-RT1–RT10: two high, four medium, four low), the fact check
(E3-FC1–FC9: three medium, six low), conformance (E3-CF1–CF12: one high, four medium, six low, one info), the realms
(E3-RL1–RL7: one high, three low, three info), the contracts (E3-CT1–CT4: three low, one info), `Ed25519.sol`
(E3-ED1–ED4: four info; no disagreement with Go), the Go's trust path (E3-GT1–GT10: one high, three medium, six
low), its spend path (E3-GS1–GS11: one high, three medium, seven low) and the assumptions (E3-AS1–AS7: three medium,
two medium-low, two low). The six highs are three classes: a stranger's `Dispatch` to a token counted as a send in
flight, which halted every signer and flagged both directions (RT1, CF1, GT1); a stranger's failed transaction
beside a dispatch, which left its block unprovable to the relayer, decisions included (RT2, GS1); and a stranger's
≈50 KB body, whose `msg/<nonce>` page ran out of query gas and stopped every walk that reads each nonce (RL1). The
answers: the send rule — a send is a message a route realm of the token's line sent — before the folds, and the
same in the Solana bridge's monitors; the results fix shared with the Solana relayer (§13's closed set, the
smallest leaf); fold F10, the realms (RL1–RL4, RL6, RL7, CF7's re-asked payee, CF11's comments); F11, the contracts
and the Ed25519 gates (CT2, CT3, CT4, CF10, GS4's bound, ED1–ED4; CT1 decided off chain, §6.5); F12, the trust path
(GT2–GT10, RT3, CF3, AS3, AS4); F13a, the spend path (GS2, GS3, GS5–GS8, GS11, FC7, RT2's relayer half, RT4, RT7,
AS1, AS2, CF8's code half); F13b, the deploy tool, the audit and the executor (CF2, RT5, GS4, CF4, CF9, CF10's
reader, GS9, RL4's reader, GS10); F14, the site (CF6, FC5, CF11's site lines); the integration lane I1 (the
monitors' and `hlwatch`'s EIP-1967 slot reads, RT8 and AS7; the executor's 3,000,000-gas resend, CT4; `hleth
setup`'s 2^32 check, GS4; the updater's `gno:encoding`, AS3; `delivery:age`'s words, FC5; the monitors' re-read after
the lines of migrations, GT2; the local run's dated prices, GS11; E4-FC4); and this plan for the rest (FC1–FC9,
RT6, RT8–RT10, AS5–AS7, CF5, CF7, CF8, CF11, CF12, CT1, RL5) — every code fix with a test that fails before it, and
the local run passing on each fold. Open after round 3: a string that is not valid UTF-8 beside a dispatch (§9,
§11.11); route migrations after the exit (§6.4, §11.12); the proposal reading, advisory until r/hlgov records its
builders' requests (§8); a pending kind 8's EIP-1967 slots read before the vote (§6.5; the monitors read them once it
executes, the integration lane I1); a decision float
route traffic cannot spend and a cap on a key's route spending (§5.5, §11.4); proposals after the yield by three keys
alone (§11.9); the operator's own gno nodes and the providers (§11.10); the Ethereum stage step and the Sepolia gates
(§10); a stranger's identical header holding sends (§9); and, missing from e8's list (E4-FC4), the status site's
whole read of each ledger (E3-RL4's class) and no kind 8 in the local run — both closed in round 4. Round 4 reads the
folded tree.

Round 4 (2026-10-15, the third code round; ten audits, read-only, of the tree at 1a6770b5 with plan e8; findings in
`audits/r4/`; this plan's e9): the plan red team (E4-RT1–RT4: one medium-low, two low, one info), the fact check
(E4-FC1–FC8: eight low), conformance (E4-CF1–CF6: two medium, one medium-low, two low, one info), the realms
(E4-RL1–RL6: one medium, three low, two info), the contracts with `Ed25519.sol`'s delta (E4-CT1–CT3: two low, one
info; the verifier unchanged, its gates now catching all 57 of round 3's planted bugs), the Go's trust path
(E4-GT1–GT6: one medium, one medium-low, four low), its spend path (E4-GS1–GS4: one high, two medium, one low), the
assumptions (E4-AS1–AS3: one medium-low, two low), the tools (E4-TL1–TL8: one medium-low, seven low) and the live
Solana bridge's fixes (E4-SF1–SF6: one high, two medium, two low, one info; the Solana tree's). The two highs: E4-GS1,
round 3's non-UTF-8 class still open, with our own route realms its cheapest door (a failed `TransferRemote`
repeating the caller's bytes needs no coins, no `MsgRun`, no `chain.Emit`); and E4-SF1, on the Solana branch, a
monitor reading an honest send as a break when it is minted between two routes' supply reads. The answers: F15a, the
spend path (GS2, GS4 and CF2 by the bookings' trailer; GS3; RT1's relayer half; FC5; TL8; CT3's resend; guard tests
for SF5's twin here and for GS1's Go half, neither needing a fix); F15b, the trust path and `hlwatch` (GT1–GT6, AS1 =
CF1, CF3, CF4, CF5, TL3, TL4, RL1's Go half, RT1's monitor half, and its own review's: the pair's finalized block
agreed in time and parent, the fast path for a forged claim, a void kept restorable, the monitors'
`eth:finality-stall`); F15c, the tools, the site and the local run (TL1, TL2 and its class, TL5, TL6, TL7 = RL3 =
CF6's item 10, AS3, RL4); F15d, the realms (GS1's realm half = RL6, RL1's waiting index, RL2 = FC3, RL5); F15e, the
contracts (CT1; CT2 and CT3 in the comments and here); S5 on the Solana branch (SF1–SF6, and GT1's and RL1's Solana
siblings); the integration lane I2 (S5's Solana fixes merged into this branch, the monitors and `hlwatch` reading
the routes' `waiting/<from>`, the relayer's `Claim` batch sized from its gas, the signers' `-relayers` and `-proxy`
set out for the deployment); and this plan for the rest (FC1, FC2, FC4, FC6–FC8, RT2–RT4, CT2, CT3, AS2, CF6) — every code fix
with a test that fails before it, and the local run passing on each fold. Open after round 4: the non-UTF-8 class
beyond our realms (§9, §11.11: Jae's lossless results source); a provider lying at the floor with current data, the
Solana bridge's (its cut, S4's residual); route migrations after the exit, and the launch r/hlgov's enrolment note
unalarmed then (§6.4, §11.12; RT3); the proposal reading, advisory until r/hlgov records its builders' requests (§8);
a pending kind 8's slots read before the vote, and its call simulated by a tool (§6.5, §8; FC7); a decision float
route traffic cannot spend and a cap on a key's route spending (§5.5, §11.4); proposals after the yield by three keys
alone (§9, §11.9; RT2); the operator's own gno nodes and the providers (§11.10); the Ethereum stage step and the
Sepolia gates (§10); a stranger's identical header holding sends (§9); a kind 9 end to end (§10); the slot watch of a
checkpointed `hlwatch` (§5.9; CF6); the executor's alarm for a ledger that does not read (§5.7; TL4's executor half);
the prices' week (§5.5, §11.5; AS2); the status site reading every page of every ledger at each status
(F15c); and, under Proposal 2's verifier, which proves anyone's message, the cut counting the routes' deliveries
only (F15b). Not converged by the loop's rule (`AUDIT.md`): E4-GS1 is high, its class open until §11.11's results
source.

Round 5 (2026-10-15, the fourth code round; six audits, read-only, of the tree at 04441d95 with plan e9 and, for the
live Solana bridge's fixes, of `bridge` at e2e87c58; findings in `audits/r5/`; this plan's e10): the Go's trust path
(E5-GT1–GT4: one medium-low, two low, one info), conformance (E5-CF1–CF4: four info) and the plan red team
(E5-RT1–RT6: one medium, one medium-low, four low), the three converged by the loop's rule; the Go's spend path
(E5-GS1–GS6: two high, two medium, two low) and the realms (E5-RL1–RL4: one high, one medium-low, one low, one info),
not converged; and the deploy gate of the live Solana bridge's fixes (E5-SF1–SF6: one medium, three medium-low, one
low, one info; the Solana tree's), converged, and deployable with SF1's fix first — a stranger's burn of amount 0,
credited by the stranger himself, held both Solana monitors at no verdict. The three highs: E5-GS1, the relayers'
`Claim` loop meeting p/hlroute's recipient meters — a claim paid in installments kept `Claim` called every tick,
≈185 GNOT of the relayers' gno key for one 1,000 GNOT transfer to a fresh recipient, the live Solana relayer alike;
E5-GS4, anyone's `Reemit` of a waiting message once a block, growing the events every walk read until a provider's
batch cap failed every walk and stopped gno.land → Ethereum for good, decisions included, for ≈8 GNOT; and E5-RL1, a
route's migration page of 500 records past gnoland-1's block, the migrated route then frozen. The answers: F17a, the
spend path (GS1's relayer half, GS2–GS6, RT3, RT5's float, CF2 kept to the plan's words, and its own review's R2–R8);
F17b, the trust path and `hlwatch` (RT1, GT1, RT6, GT2, GT4's items 1 and 4, CF3's items 4 and 5, GT3 = RT2 in the
operator's notes, and its own review's folds); F17c, the realms (RL1's pages of 50 and the migration gate, GS1's realm
half, RL2, RL3's default-deny checker, RL4's rule); F17d, the tools and the local run (CF1 in `hleth audit` — no
contract changed, an immutable creation time having measured 176 bytes against `HlAdmin`'s 141 of EIP-3860 margin —,
CF3's items 2, 3 and 6, CF4, and the realms audit's notes on the gas of a guardian's flag and of an executed
decision); S6 on the Solana branch (SF1–SF6, GS1's and GS5's Solana siblings, I2's `Claim` batch carried to the live
relayer); the integration lane I3 (S6's fixes merged into this branch; the signers' and the monitors' known dust, the
review of F17a's first finding; `hlwatch`'s GovDAO floor; the executor's alarm for a GovDAO list it cannot parse);
and this plan for the rest (RT1's question, §11.13; RT2; RT3; RT4; RT5's deposits; RT6, §11.10; CF1's bound; CF2;
CF3; CF4; RL2's bound; GT4's two-fault residual) — every code fix with a test that fails before it, and the local run
passing on each fold. Missing from e9's open list (E5-RT4): the live Solana relayer's fixed `Claim(20)` and a
checkpointed watch's alarm a claim, both closed in round 5 (S6, F17b); and listed there, one item I2 had answered —
the machines compare only blocks across gno sources, never a render (E5-CF2) — now struck from it.

Open after round 5: the non-UTF-8 class beyond our realms (§9, §11.11: Jae's lossless results source); what the
watch does in a finality stall past the hour — flag the gate, or read at `latest` (§11.13: Jae's); a stranger's old
re-emit proven later by `proveHeader`, unseen, its message waiting for its newest event's header, and a message's
events above the finalized trusted height kept in the index until it passes them (§5.7, §9); the updater's and the
relayers' one provider, which can withhold a booking, and forging a receipt too, make one of `eth.updater`'s genuine
transaction (§9); a provider's learned batch size, which only shrinks for the process's life (§5.6); a migration
page's cost past ≈20,000 open claims, not measured (§5.3); the installments rule's bridge-meter half, held by
reasoning and a node run, no test of its own (§5.4); `hleth audit`'s 30 seconds to bring a member's gno sources to
one height, past which a lagging source is a FAIL (§5.8); the live Solana routes' migration pages of 500 and their
slivers, immutable (the Solana bridge's); the documentation halves of E4-FC1 and E4-FC4 — `test/Gas.t.sol`'s "tx"
comment, M2-NOTES.md §5's sizes and §6's method, M3b-NOTES.md's ≈0.7 gwei, `agent/floats.go`'s flag figures,
`AUDIT.md`'s round-3 sentence (E5-RT4); and, from round 4's list, still: a provider lying at the floor with current
data, the Solana bridge's; route migrations after the exit, and the launch r/hlgov's enrolment note unalarmed then
(§6.4, §11.12); the proposal reading, advisory until r/hlgov records its builders' requests (§8); a pending kind 8's
slots read before the vote, and its call simulated by a tool (§6.5, §8); a decision float route traffic cannot spend
and a cap on a key's route spending (§5.5, §11.4); proposals after the yield by three keys alone (§9, §11.9); the
operator's own gno nodes, now a requirement, and the providers (§11.10); the Ethereum stage step and the Sepolia gates
(§10); a stranger's identical header holding sends (§9); a kind 9 end to end (§10); the slot watch of a checkpointed
`hlwatch` (§5.9); the executor's alarm for a ledger that does not read (§5.7); the prices' week (§5.5, §11.5); the
status site reading every page of every ledger at each status (F15c); and, under Proposal 2's verifier, the cut
counting the routes' deliveries only (F15b; E5-GT4). Not converged by the loop's rule (`AUDIT.md`): three highs,
E5-GS1, E5-GS4 and E5-RL1, each folded above with a test that fails before it.

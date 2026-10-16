# E-M3b — Ethereum → gno.land: the signers, the inbound relayer, the monitor, `hlwatch`, `hleth`, the local run

Lane E-M3b of `BUILD.md`, branch `eth-m3b` (from lane E-M3's `eth-m3` at its first commit, the `ethx` client, plus
plan e2; `eth-m3` and `bridge-eth` merged in as they moved), built 2026-10-14. This file is the lane's record: what is
built, every reading of the plan where it left room, the findings and questions for the other lanes, the tests of the
last devbox run with their commands, and the end-to-end run. The Solana bridge's Go side (`bridge/agent`,
`bridge/cmd/*`, audited over eight rounds) is the reference; its rules are carried over where `PLAN.md` says "as", and
changed only where Ethereum changes them.

## 1. What is built

### The shared Ethereum side of the cut (`agent/identity.go`, `agent/identity_burns.go`)

- **The identity** (the Solana `identity.go`, Ethereum's terms): `Identity` (lock, owed, voided, lost at a gno
  height; the supply at the agreed finalized block; in flight out and in), `Verdict` (holds / BROKEN / short, the
  break test the exact sum `right + in flight > lock`, the short side's tolerance), `SignerStops` (§5.6's rule),
  `Cut` (the gno block after the snapshot, no burn credited there above the snapshot's block), the third-source rule
  (`JudgeSources`), §5.6's ahead rule (`judgeAhead`). Counts are saturating: a supply past 2^61 base units (a forged
  mint of astronomical size) reads as past any lock instead of wrapping.
- **The Ethereum snapshot** (`takeEthSnapshot`): the finalized block F two providers agree on (number and hash,
  `ethx.Pair.Finalized`), each token's `totalSupply` and the light client's state, every read an `eth_call` pinned to
  F's hash (EIP-1898, requireCanonical) through both providers, compared byte for byte. `ethReadText` names what a
  failure means: a provider without F's state (a pruned public endpoint: no verdict, a full-history provider is
  needed — the coordinator's facts of research-ethereum §5), the providers disagreeing (no verdict until they agree).
- **The burn index** (`burnIndex`): every burn toward gno.land at or below the scan's cursor, by message id, read with
  E-M3's `ethx.ScanBurns` through the pair (a token router's own `Dispatch` to gno.land's domain and its route realm
  beside that token's `SentTransferRemote` of the same recipient and amount; anything else ignored with why, never
  counted — ER5). Bounded to 20,000 blocks a round (a fresh index catches up over rounds, with no verdict until its
  cursor reaches the snapshot). `markCredited` reads hlmailbox's `delivered/<id>` at the cut's height; `inflight`
  sums the burns at or below F not credited and lists the ids credited above F. `deployBlock` finds the deployment's
  first block (the mailbox's code) by a binary search over `eth_getCode` at past blocks when no `-from-block` is given.

### The signer (`agent/signer.go`, `hlethbridge signer`, `hlethbridge providers`)

The Solana signer's rules on Ethereum (§5.6): its own gno node, two providers that must agree (the first two of
`eth.providers`; the same one twice refused unless `allowHTTP` says a local run), ed25519 over this bridge's
checkpoint — `hl.CheckpointDigest(origin = Ethereum's domain, hook = our MerkleTreeHook as 32 bytes, root 0, index =
the message's nonce, id)` with the signer's key file (`bubblerumble/bridge/hl`'s `ValidatorKey`, `SignEd25519`).

- **The ordered cut, each round**: the snapshot (F, the supplies, the client's trusted height T at F); then the own
  node's head waited for until its block time is after the snapshot's moment; T above the head past the allowance →
  `judgeAhead` with the second source (`gno.sources`); the cut's time order; every token's counters at the head (over
  the token's line of migrations, `gnoside.MigrationOf`); then — after the gno read — the pair's finalized block F2
  ≥ F, the burn index scanned to it, the credits marked at the head; a burn credited there whose block is above F
  breaks the order (no verdict); the burns at or below F and uncredited counted as supply.
- **The refusal rule**: `supply + uncredited burns > lock − owed − voided + lost` halts, with the reason on the status
  and the `signer:halt` flag alarm; the stop clears itself when the identity holds again (the ahead stop only at a
  height at or above T). A forged mint burned back toward gno.land keeps the stop by its own amount.
- **The one-round wait**: a burn is signed only once its block is at or below the finalized block of the verdict
  signed on (burns in (F, F2] wait for the next round's verdict, which counts them in flight first).
- **No verdict**: alarms (`signer:no-verdict`, `eth:no-state`, `eth:providers`), signs on the last verdict for one
  more round, halts after an hour without one; a finality stall (F standing still for an hour, the clock kept in
  `state.json` across a restart) alarms (`eth:finality-stall`): nothing newer is signed meanwhile — liveness, never
  safety. Two providers' finalized answers more than two epochs (64 blocks) apart are a disagreement (`eth:providers`),
  never a reading at the lower one (round 5's E5-RT1: a provider standing still at an old real block) — retried quietly
  for four rounds (two honest providers read seconds apart as the finalized block jumps several epochs at once), then
  alarmed. A provider must answer `finalized` truly: one that answers it as `latest` now stops its machine (it used to
  be outvoted by the lower answer).
- **A nonce conflict** (a second message id under a nonce already signed) halts for good until a human looks; the
  first signature keeps being served.
- **Serving**: `GET /v1/eth/<nonce>` on the public listener (HTTPS unless loopback) serves `SignedBurn` —
  `{origin, nonce, id, message, hook, root, block, tx, signature, signer}`; the status (`GET /v1/status`) on a loopback
  listener of its own, never beside the public endpoint, every URL in it redacted.
- **Deploying the public endpoint** (round 4's E4-GT2, fold F15b; round 5's E5-GT3, E5-RT2; `hlethbridge signer -h`):
  the endpoint serves one request a connection over HTTP/1, at most 16 connections of one client (an IPv4 address, an
  IPv6 /48) and 1,024 in all, 64 of them only for the relayers. **The deployment is the signer's own TLS on its public
  port, with no reverse proxy in front**: `-listen 0.0.0.0:<port>` (the port the launch notice names), `-tls-cert`,
  `-tls-key`, and **`-relayers`** naming the relayers' egress addresses (IPs or CIDR blocks, comma separated: the
  primary's and the backup's). There a stranger's connections are bounded per address or /48 at the listener, and the
  relayers keep 64 slots no stranger can take. A reverse proxy in front undoes that, whatever its settings: every
  connection then reaches the signer from the proxy, so the relayers' reserved slots never apply (their fetches share
  the pool of 960), and the strangers' idle connections are the proxy's to hold — the relayers then depend on the
  proxy's own capacity, which nothing here reserves for them, and a per-address bound keyed by the full address (nginx's
  `$binary_remote_addr`) does not group an IPv6 client by its /48 as the signer does. Where the public port must be
  shared with other sites behind a reverse proxy, the relayers still reach the signer directly, never through it: the
  signer serves its own TLS as above on a port the machine's firewall opens to the relayers' addresses and the proxy
  alone, the proxy forwards everyone else to it (`proxy_pass https://127.0.0.1:<port>`), and the signer runs with
  **`-proxy 127.0.0.1`** (the proxy's IP) beside `-relayers`, the vhost appending the client —
  `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;` — since the signer counts each request the proxy
  forwards against the header's LAST entry, read from the configured proxy alone (16 in flight a client, 429 past it;
  the `-relayers` addresses exempt), while the relayers' own connections keep their reserved slots. Without `-proxy` a
  proxied endpoint is one client to the signer, 16 connections for everyone; without the header line every request
  counts as one unnamed client; and a proxy that itself sits behind another (a CDN) makes that one's address the last
  entry, so the signer must face its own proxy only.
- **State**: `eth/<nonce>.json` (the signatures) and `state.json` (the index's first block, its cursor, the burns not
  yet credited and signed); a credited and signed burn leaves the index.
- **`hlethbridge providers c1.json c2.json c3.json`**: §5.6's rule across the signer machines — two providers each,
  never one twice, no pair shared by two signers, no provider in more than two (`agent.ProviderRule`).

### The inbound relayer (`agent/relayer_in.go`, `agent/relayer_in_job.go`)

A job registered with E-M3's `RegisterRelayerJob`, run in the relayer's tick, its state kept in the relayer's state
file under `inbound`: the burns of the finalized blocks after its cursor (5,000 blocks a tick) read through the
relayer's one provider (E-M3's reading 2: it decides nothing a lying provider could cash) and matched by
`ethx.MatchBurns`; amount-0 burns skipped (§5.5); every queued burn's delivery read on gno.land (delivered → dropped);
its target realm (the route, or the line's live successor once its import has ended; before that it waits, named);
the signers' signatures fetched from `GET /v1/eth/<nonce>` (HTTPS or loopback, 1 MB at most, cached on disk), each
checked against the key hlmailbox holds for that signer over this checkpoint; `Process` with exactly the signatures the
rule needs (the lowest-indexed valid ones up to the threshold, or one of each group; `hl.GnoMetadata`) through the
relayer's `TryCall` (its attempts, backoff, parking and hourly retries) and `Act` (the backup 60 s behind); and
`Claim(20)` on every route while a claim waits and the meter holds the fee, backing off a minute to an hour while a
call pays nothing (a migrated route's successor claims once its import has ended) — since the round-4 fold (I2) twenty
claims a call at most, the batch sized from the gas the last Claim used (three quarters of the gno client's ceiling,
halved on an out-of-gas; a Claim of one that runs out simulated under four times the gas, alarmed
`claim:gas:<symbol>`). The realms' `Execute(id)`, the lapsed proposals and the re-emits of gno.land → Ethereum
dispatches are the relayer's own loops (E-M3's `executor.go`, `relayer_out.go`), in the same binary and tick.

### The monitor (`agent/monitor.go`, `agent/monitor_eth.go`, `hlethbridge monitor`)

The Solana monitor's §5.9 on Ethereum, every poll in the cut's order:

1. The snapshot through the pair; a failure persisted two minutes alarms (`eth:providers`, `eth:no-state`); two
   providers' finalized answers more than two epochs apart are such a failure (round 5's E5-RT1). A finalized block
   more than half an hour old by its own time is blind — every Ethereum-side rule reads at it and sees nothing newer,
   while the client accepts headers on inclusion — hlwatch never green over it, `eth:finality-stall` alarmed, at flag
   level past the hour (its time, or standing still: the clock kept in the state).
2. The own node's head after the snapshot: catching up, not after it or older than five minutes is no verdict
   (`gno:own`, `gno:own-lag`); the client past the chain beyond an hour of blocks on both sources is the fork: the
   gate and HlAdmin flagged once it has stood two minutes (`header:ahead` → `header:mismatch`).
3. The sources at H (`JudgeSources`): the own node the odd one out flags credits (never payouts) and the gate.
4. hlmailbox, the routes, their lines of migrations at H (a successor importing alarmed; a line unreadable is no verdict
   for that token).
5. The burn index to the pair's finalized block after the gno read; the sends — hlmailbox's messages toward a route's
   token — and their delivered marks at F (`Mailbox.delivered`, both providers, by 100), their refund marks where one
   can be set (a send refunded and undelivered alarmed daily; one delivered after its refund — minted twice — alarmed
   until written off).
6. The identity of every token by the signers' rule (in flight out and in counted exactly): BROKEN flags credits and
   payouts on gno.land and the gate's flag; short alarms after three polls. The diverted credit (a claim naming
   another recipient or amount than its burn) and the forged credit (a waiting claim the verifier credited whose id no
   burn of the index holds, on its second poll) flag the same. A BROKEN reading while hlmailbox holds a delivery the
   index cannot place waits for the index (E4-GT1) — only within what both providers show above the index, each to
   its fresh head (the token's burns there that the cut's state credits, their marks read inside the cut): an excess
   past that is a break at once (round 5's E5-GT1); the reading's height is the head read after the cut, raised to the
   highest credit the cut read (E5-RT6: a load-balanced endpoint's head a block behind its state) — sound on one node
   only: the cut must read its state and its head from the machine's own node, never a load-balanced endpoint, whose
   backends can answer a mark ahead of the state or a head behind an older mark. The claims a
   checkpointed index cannot judge are one alarm, `credit:unindexed-before-base` (how many, the first named), and the
   waiting index's reading — its place and suspects — is kept in the state across restarts (E5-CF3).
7. The Ethereum side (`ethChecks`): the client's lag (its trusted header older than seven hours, E-M3's
   `ClientLagAfter`); **the header record** rebuilt from the client's `HeaderRecorded` events through the pair —
   each event's record number the next and its running hash `keccak256(previous ‖ hash ‖ height u64 ‖ block u64)`
   (`GnoLightClient._appendRecord`), the rebuilt count and hash held against the client's at F (a gap) — and every
   entry held against the own node's block at its height and, on a difference, the second source's (both differing:
   the fork, the cut the entry's Ethereum block, flagged on both objects after two minutes; the own node alone:
   `header:own-odd`, the sources rule's); **the stages**: every `StageProgress` event's staging call read through both
   providers, its header leaves hashed to the event's header hash and its height read from the third leaf (field 1,
   a zigzag varint), held against the own node's and the second source's block there — a stage for a header neither
   has alarms `stage:unknown:<hash>` (§6.3: alarm only), a stage made through another contract `stage:unread`;
   **HlAdmin's line** (E-M3's `followAdmins`); **the foreign decisions**: every number HlAdmin recorded
   (`DecisionRecorded`, every program of its line) against r/hlgov's `decision/<n>` — a number r/hlgov never
   dispatched (re-read at the head first), a kind, or the body the delivery carried (the `Mailbox.process` calldata of
   the recording transaction, both providers, the message checked to be HlAdmin's and to carry the number), that is
   not r/hlgov's is foreign: both objects flagged after two minutes; a body that could not be read is judged by its
   kind and, while pending, its parameters (`decision:unread` alarmed); HlAdmin's counter past r/hlgov's alarms
   (`decision:counter`); **the pause mirror**: deliveries paused (a flag on the gate, or the quarantine) set the
   watch key's `sends` on every live route, cleared when they resume (E-M3's `deliveriesPaused`); **the caps**:
   `mint:cap-ratio` (a day cap above a fifth of the lock, quiet until the expected lock, five caps or 14 days) and
   `limits:mismatch` (a route's maximum above the gate's).
8. Stalls (30 minutes without a delivery with a message waiting), ages (two hours), runways (E-M3's `Float`: the
   relayers' and the updater's ETH, the watch key's, the relayers' gno gas key).

**The flags on Ethereum (§5.5, ER9)**: a flag is a transaction per object, so it is never re-asserted on a timer.
`flagNeed` (pure) sets it only when it is not set at the final block read (a `setFlag` of this key landed above that
block counts as standing, pending finality), or when an unpause HlAdmin recorded for that object (its parameters'
object byte) at a final block would clear it — its delivery block above the flag's block (an unpause clears the
flags set before its delivery). Each `setFlag` goes under the emergency fee cap (ER6) and is recorded with its block.

### `hlwatch` (`cmd/hlwatch`, `agent/monitor_views.go`)

The guardians' tool over the holder's own node and two agreeing providers; the monitor run read-only (never a flag)
for the cut, the identity, the record, the stages and the decisions, with the holder's pages beside it.

- `status`: who governs; the signers and their rule; the seats; the flags on hlmailbox; the pending decisions on
  gno.land (every realm of each line, hlfees, r/hlgov) with their maturity; the builder-made GovDAO proposals with no
  execution (pre-armed marked, "awaits the yield" before it); the identity per token (the route's counters and the
  monitor's verdict); r/hlgov's counter and recipient; the client's trusted header against the node's block there,
  its epoch, quarantine and clock factor; the header record (rebuilt, checked, awaited; a gap; a mismatch with the
  cut); the gate's and HlAdmin's seats, flags and hold, the gate's mint limits and buckets; HlAdmin's setup, origin,
  counter against r/hlgov's (COUNTERS DIFFER), clock factor, the hold history and every pending decision with its
  maturity and deferral (E-M3's `maturesAt`, `executeWhen`); every standing alarm; and `hlwatch: GREEN`, or
  `NOT GREEN — …` with why (no verdict, a broken identity, a mismatch, a gap, a foreign decision, an unknown stage,
  the providers disagreeing, HlAdmin's counter ahead, any flag standing, a finalized block more than half an hour old,
  no second gno source configured, GovDAO's list not read whole — an item or a status word it does not parse, past 50
  pages: round 5's E5-RT1, E5-GT2).
- **Before a kind 8 or 9** (pending on HlAdmin; `check -target` before the vote): the target's code read through both
  providers at the final block — "NO CODE", "EIP-7702 account … MUTABLE: would be spent" when it begins `0xef0100`,
  its code hash against the decision's and against each reproducible build given (`-build LABEL=artifact.json` or a
  hash file; none matching says so), and for a kind 8 the storage layouts' difference when both are given
  (`-layout-old`, `-layout-new`, forge's `storageLayout` JSON: an entry gone, moved, retyped or added inside the
  running layout is LAYOUT DIFFERS; else append-only with the counts).
- `watch`: the view every period, an alarm (webhook) for every flag set, decision pending, and identity verdict
  changed, besides the read-only monitor's own alarms.
- `flag`/`unflag`: the holder's flag on hlmailbox (its gno key's seat), then on the gate and on HlAdmin (its Ethereum
  key's slot): two Ethereum transactions, each waited for until final (its block at or below the pair's finalized
  block, still the canonical one, the flag read set or cleared there), either re-sent when a reorganisation dropped it
  (three tries).
- `version`: the binary's sha256 and the toolchain's record of its source.

### `hleth` (`cmd/hleth`, `local/hyperlane-artifacts.sh`)

- `setup`: §5.8's eleven steps in E-M2b's order (`contracts/script/Setup.s.sol`): the ProxyAdmin; the Mailbox and its
  proxy (the ProxyAdmin standing in for the ISM and hooks); the MerkleTreeHook and the ProtocolFee; the client and its
  proxy (the trusted header from `-gno-rpc`'s newest block, refusing one announcing a change or older than an hour
  unless `-allow-old-header`, or `-trusted-file`); the two HypERC20 (decimals 6, scale 1/1) and their proxies; the gate
  and its proxy; HlAdmin (which creates HlAdminExec); the tokens' ISM and gno.land routers; the mailbox's default ISM
  and hooks; ownership in step 10's order, the ProxyAdmin last; `finishSetup`. Every proxy initialized in its own
  construction; constructor and initializer arguments packed by name from the artifacts' own ABIs (E-M2b's churn
  absorbed, an unknown parameter refused before anything is sent). Resumable from the chain: each step read first and
  skipped when done; each creation recorded in `deployment.json` before it is broadcast and, on a resume, adopted once
  mined, re-sent at the same nonce and address when dropped, or forgotten when another transaction took the nonce;
  `-start-sequence` held against r/hlgov's counter when the realm is on the chain. Ends `hleth setup: DONE (sent N
  transactions)`, or `(sent nothing: every step was done already)`. Contracts are created by `hleth`'s own sender
  (`chain.go`: ethx's rules, since `ethx.Sender` cannot create — a question for E-M3).
- `audit`: §5.8's list at the finalized block (or `-at`): every owner and admin is HlAdmin; each proxy's EIP-1967
  implementation and admin slots; the mailbox's ISM, hooks and domain; each token's ISM, hook, routers (gno.land's
  alone), fee recipient and fee hook unset, decimals, scale, supply and mailbox; the gate's bindings, seats, limits and
  clock factor; the client's chain id, origin, trusted header (against `-gno-rpc`'s block), set hash, factor and
  `mailboxPkgPathHash`; HlAdmin's every binding, `setupDone`, routes and counter against r/hlgov's (a gap fails); the
  ProtocolFee's maximum, fee and beneficiary; then each of the fifteen runtime codes against its build — Hyperlane's
  and OpenZeppelin's from the 12.2.0 release's own compile, ours from the `-contracts` out directory — with the
  immutables masked and each masked value named and held against the value it must have; the implementations of the
  client and the gate refusing `initialize`. Every raw RPC call is printed before its answer as `rpc> curl -s "$RPC" …`
  (the provider's URL never printed); `hleth audit: PASS` exits 0, `hleth audit: FAIL (n)` exits 1.
- `decision status|body|execute`: the members' commands (the Solana `hlrecover`'s): HlAdmin's counter, pending
  decisions with their maturity and deferral, holds and flags; any kind's body from flags (PLAN §13's layouts, held
  byte for byte against E-M1's r/hlgov vectors and E-M2b's parsers); `execute` from any funded key, refusing an
  immature decision with its maturity.
- `send` (`transferRemote` with the token's quoted fee), `balance`, `keygen` (a seed: a deterministic key for local
  runs only), `version`.
- `deployment.json` (written after every step; read by `audit`, `decision` and the local run, which builds the
  machines' `bridge.json` from it): `chainId`, `localDomain`, `gnoDomain`, `deployer`, `deployBlock` (the first block
  of the deployment: where the burn, record, stage and decision scans start — the machines' `-from-block`), `factor`,
  the fourteen contracts (`proxyAdmin`, `mailboxImpl`, `mailbox`, `merkleTreeHook`, `protocolFee`, `clientImpl`,
  `client`, `token0Impl`, `token0`, `token1Impl`, `token1`, `gateImpl`, `gate`, `hlAdmin`), `hlAdminExec` (created by
  HlAdmin's constructor: M2-NOTES §1.4), `ed25519Verifier` once the client takes one, the launch values, the steps with
  their transactions, and a creation in flight; beside it `<out>.sender.json`, the sender's in-flight record.

### The local run (`local/e2e.sh`)

Everything on one machine from nothing — see §6.

### What the coordinator asked during the build

- **The signer's endpoint for the site** (E-M4 reads `GET <signer url>/v1/eth/<nonce>`, the relayer the same): served,
  the Solana signer's `Signed` shape — `origin, nonce, id, message, hook, root, signature, signer` — with `block` and
  `tx` where the Solana one has `slot`; a 404 for a nonce not signed. `GET /v1/status` is served on the signer's
  loopback status listener, never beside the public endpoint (the Solana signer's split: a flood at the public
  endpoint reaches no loop state).
- **E-M2a's `Ed25519Verifier`** (merged into `bridge-eth` at db5aefe8; E-M2b is moving the client to call it, its
  address an immutable of the client's implementation): `hleth setup` deploys it before the client's implementation
  whenever the client's constructor takes an address, and passes it under that parameter's own name; `hleth audit`
  holds its runtime code against its build and every client immutable whose name says verifier or ed25519 against the
  recorded address; `deployment.json` gains `ed25519Verifier`. Until E-M2b's switch the client takes none and nothing
  changes. (Q8 below: the realms' refused recipients do not list the verifier.)
- **Public RPC facts** (research-ethereum §5): ethx (E-M3) reads each provider's log range from its config and halves
  a refused chunk; the pair compares whole ranges (one provider's empty answer is a disagreement, never an empty
  range); a read at F that a pruned provider cannot serve is `ethx.IsNoState` — no verdict, `eth:no-state` alarmed with
  the cure: **a signer's and a monitor's two providers must serve full-history state** (drpc, mevblocker, Tenderly,
  blastapi and nodies answered at any height when measured; publicnode, 1rpc and blockrazor keep ~100–128 blocks).

## 2. Readings (where the plan left room)

1. **The snapshot's moment** is the wall clock when the snapshot was read (the Solana reading), not F's timestamp:
   the gno block read after it must be later, which is the stricter order.
2. **The burn scan after the gno read** reaches the pair's finalized block then (F2 ≥ F): a credit at the cut's height
   of a burn above F is no verdict; a burn in (F, F2] is signed after the next verdict counts it.
3. **The signer keeps its index on disk** (the burns not yet credited and signed), where the Solana signer kept it in
   memory and rescanned from its signing cursor: no short-side alarm after a restart.
4. **A disagreement is per range**: `ethx.Pair.Logs` compares a whole range and answers `ErrDisagree`, so one
   disagreement is no verdict for the round (the Solana signer disputed one dispatch and judged the rest).
5. **Amount 0 and past 2^61 base units**: neither is indexed, signed or counted in flight (nothing to credit; an amount
   gno.land cannot count) — such a burn makes the identity short, an alarm.
6. **The record's fork rule consults the second source** before it flags: the plan's "compared with the own gno node
   and a second source"; the own node alone differing is the sources rule's.
7. **A stage's height** is read from its call's header leaves (tm2 has no block-by-hash route); a stage made through
   another contract is alarmed as unread, never judged.
8. **A foreign decision's body** is read from the recording transaction's `Mailbox.process` calldata (the event carries
   the kind alone); a decision cancelled or executed at recording is compared by its body, not only its kind.
9. **The flag re-set**: "when HlAdmin records an unpause, on both objects" is read per object — an unpause names the
   object its flags are on (its parameters' first byte); the other object's flag is not touched by it.
10. **The client's lag** is its trusted header's age (gno.land's block time), against E-M3's seven hours.
11. **hlwatch's GREEN** needs no flag standing anywhere: a guardian's own flag is something to act on.
12. **The deploy block** comes from `-from-block` (the e2e passes `deployment.json`'s `deployBlock`) or a binary search
    over `eth_getCode` (a full-history provider); the config has no field for it (Q1).

## 3. Findings

1. **The Solana bridge's `judgeAhead` underflows** (`bridge/agent/signer.go`, `case T-second <= allowance`, uint64):
   with a second source whose head is past T — the usual case, rpc.gno.land at the chain's head — `T − second` wraps
   to a huge number and the verdict is `aheadStop`. A live signer whose own node stalls without saying it is catching
   up (`headAfter` returns the stale head after its wait; nothing checks its age before `judgeAhead`) halts, once T is
   an hour of blocks past the stale head, with "the client runs ahead of its chain (§5.6)" — the forgery-class reason —
   instead of "own node lags" (no verdict). The halt clears itself when the node catches up and the identity holds at
   T or above, and the hour without a verdict would halt it about then anyway: the harm is the reason an operator
   reads. The Solana monitor is shielded (`judgeOwnHead` refuses an old head first); the Solana test table has no case
   with the second source past T. Fixed in this port (`T <= second ||`, tested); `bridge/` is not this lane's to edit.
2. **E-M3 found (M3-NOTES §4.1)**: GnoLightClient with E-M2a's real Ed25519 is past EIP-170 (26,732 bytes); the local
   run starts anvil with `--disable-code-size-limit` until E-M2b splits it.
3. **ScanBurns' recipient is the configured route realm only** (`ethx/burns.go`: `l.Topics[3] != r.Realm`): after a
   migration's kind 6 enrolls a successor as a token's router, every burn is addressed to the successor and ignored —
   the signers would never sign the way home. `BurnRoute` needs the realms of the token's line (E-M3, Q2).
4. **`hlethbridge updater` had no flags for its gap and batch wait** (2 minutes, 90 s): a local run waits minutes per
   header. Added `-min-gap` and `-batch-wait` to E-M3's `cmd/hlethbridge/main.go` (the smallest change, these two flags
   only).

## 4. Questions

- **Q1 (E-M3)**: an `eth.deployBlock` in `Config` — the first block every scan starts from (the signers, the monitors,
  hlwatch, the inbound job); today a flag or a code search.
- **Q2 (E-M3)**: `ethx.BurnRoute` with the realms of the token's line of migrations (finding 3).
- **Q3 (E-M3)**: a `RelayerJob` status hook — the inbound job's queue and claims are in the relayer's `last` notes
  only, not its status JSON.
- **Q4 (E-M3)**: `ethx.Sender` cannot create contracts (`Call.To` is an address); `hleth` carries a creation sender with
  the same rules (`cmd/hleth/chain.go`). Grow ethx, or keep it there?
- **Q5 (E-M3)**: `ethx.ReadAdmin` reads the interface's ABI, which has no custom errors; `hleth` names HlAdmin's
  refusals from its own selector table.
- **Q6 (E-M2b)**: a kind 6 with a zero router unenrolls the domain in `HlAdminExec._enrollRouter`; r/hlgov refuses a
  zero router, so it is unreachable — consistent, but §13 does not say it.
- **Q7 (Jae, the plan)**: amount-0 burns are never signed (reading 5); the plan's "§5.5: amount-0 transfers are skipped"
  is the relayer's — accept for the signers too?
- **Q8 (E-M1)**: once the client calls a separate `Ed25519Verifier`, the deployment has a fifteenth address; hlmailbox's
  `ethContracts` (the routes' refused recipients, ER10) lists fourteen. A send to the verifier would mint where nobody
  can move it — add it to the list (a launch value of its own)?

## 5. Tests (the last devbox run)

`ssh root@<devbox>`, the tree at f981fab9 (`eth-m3` at 0f5bef1d and `bridge-eth` at c9e01fe5 merged) synced by
rsync (BUILD.md) to `/root/src/eth-m3b-test`, Go 1.27.1, Foundry 1.8.5:

```
cd /root/src/eth-m3b-test/eth/bridge
gofmt -l agent cmd ethx gnoside      # nothing
go vet ./...                         # clean
for p in ./agent ./cmd/hlwatch ./cmd/hleth ./cmd/hlethbridge ./ethx ./gnoside; do
  HLETH_CONTRACTS_OUT=/root/src/eth-m3b-contracts/out HLETH_HYPERLANE_DIR=/root/src/eth-m3b-hl-art go test -race -count=1 -v $p
done
```

- `agent`: ok — 58 passed (28 of them this lane's), 1 skipped (E-M3's real-contract test without its
  `ETHX_CONTRACTS_OUT`; see below). This lane's run over fakes of both chains — an Ethereum chain answering JSON-RPC
  through ethx's own client with two providers either of which lies, lags, forgets state or serves a reorganised log,
  and a gno node answering the realms' renders:
  - the signer (11): the token's own burn signed under this bridge's checkpoint (the signature verified, served at
    `/v1/eth/<nonce>`, no status beside it) and a dispatch without the token's transfer ignored with why; two
    providers disagreeing — another amount, no logs and no error, another supply — no verdict, nothing signed; a
    removed log and a log of another fork refused; the refusal rule: a forged mint halts, burned back it stays halted
    by its own amount, the void as forged clears it; the one-round wait; a pruned provider (`eth:no-state`, the
    full-history cure named), the hour without a verdict halting and a verdict lifting it; the finality stall; the
    nonce conflict sticky; the ahead rule (the second source past T: the own node lags — the Solana underflow, finding
    1) and its stop lifted only at T; §5.6's provider rule across signers; the status read while rounds run (every
    field from what the loop published under its lock, `-race` holding it).
  - the inbound relayer (4): exactly the rule's signatures (0 and 2 of three, signer 1's over another hook skipped,
    one valid signature waiting), our hook and the nonce in the metadata, a delivered burn dropped; a burn to a route
    mid-import waiting; `Claim(20)` and its backoff, payouts paused and a dry meter idle; a failed delivery kept.
  - the monitor (9): the identity with a burn in flight, a forged mint flagging credits, payouts and the gate once —
    never again on a timer, pending finality or final; `flagNeed` and the re-set only under a recorded unpause (and a
    cleared flag set again); the fork rule (both objects after two minutes, the cut named; the own node alone odd is an
    alarm); a gap; a stage for a header gno.land never had alarmed and one for a real header not; foreign decisions (a
    number r/hlgov never dispatched, a body not r/hlgov's under its number, r/hlgov's own not); two providers
    disagreeing never flagging; the diverted credit and the forged credit on its second poll; the pause mirrored as
    sends and cleared.
  - the views (4): the storage layouts' difference (append-only, reordered, inserted, retyped, gone); the code check
    (an EIP-7702 account, no code, the decision's hash, the builds); hlwatch's page GREEN on a quiet bridge and not on
    a break, the read-only watch never flagging; every kind described.
- `cmd/hlwatch`: ok — 2 (the `-build` files; `watch`'s alarms on a flag, a pending decision, an identity's verdict, and
  the clear).
- `cmd/hleth`: ok — 12 (with the build and the release's artifacts): every kind's body byte for byte against E-M1's
  r/hlgov vectors and 18 malformed bodies refused by HlAdmin's own rules; maturity per kind against HlAdmin.execute
  (the kind-5 floor among them); the code check on synthetic code (a wrong immutable, a split value, a byte changed
  outside the masks, an immutable with no expectation, a wrong length, empty code) and on the real builds (each of
  HypERC20's 5 immutables and each of HlAdmin's 14, set wrong, fails naming it and both values); arguments packed by
  name; the client's verifier parameter; and on anvil (osaka): setup DONE, a second run sending nothing, a resume with
  other launch values refused, the audit PASS with no provider URL printed, an audit against another r/hlgov FAIL, a
  counter gap FAIL, a wrong trusted hash FAIL, a wrong `-start-sequence` refused with nothing sent, a token's ISM moved
  by impersonating HlAdmin FAIL, a token implementation swapped for one over a stand-in mailbox FAIL (naming
  `MailboxClient.mailbox` and both values), a creation dropped from the pool sent again at the same nonce, never twice.
- `cmd/hlethbridge`: ok — 1 (E-M3's: every machine's command registered, the signer and monitor by this lane's files;
  status endpoints on loopback only).
- E-M3's packages on the merged tree: `ethx` ok — 30 passed, 1 skipped; `gnoside` ok — 5 passed.
- E-M3's real-contract tests (`ethx` TestCalldataOnTheRealContracts, `agent` TestMachinesOverTheRealContracts) need
  `ETHX_CONTRACTS_OUT` naming a build with the real Ed25519: over `/root/src/eth-m3b-contracts/out` (E-M2b at 0f997981,
  its placeholder library) the client's update reverts "Ed25519: not built yet (E-M2a)", as it should; over E-M2b's
  head (b63cdad5, the real library; `forge build --skip test --out /root/src/eth-m3b-deps-b63-out`) both pass with
  `-race`: `ethx` 31 passed, `agent` 59 passed, nothing skipped.

## 6. The end-to-end run

`eth/bridge/local/e2e.sh`, on the devbox, everything from nothing on one machine:

```
# on the Mac: the tree (BUILD.md's rsync) to /root/src/eth-m3b, and E-M2b's committed contracts beside it
git archive --prefix=eth-m3b-deps-b63/ b63cdad5 eth/bridge/contracts | ssh root@<devbox> 'cd /root/src && tar xf -'
# on the devbox: lib/ (forge-std, hyperlane-core, openzeppelin) beside them, then the run
cp -R <a contracts tree's lib>/. /root/src/eth-m3b-deps-b63/eth/bridge/contracts/lib/
cd /root/src/eth-m3b/eth/bridge
CONTRACTS=/root/src/eth-m3b-deps-b63/eth/bridge/contracts sh local/e2e.sh > /root/src/eth-m3b-e2e-final.log 2>&1
```

`CONTRACTS` is lane E-M2b's committed tree (`eth-m2b` at b63cdad5, which carries E-M2a's `Ed25519.sol`, `Sha512.sol`
and `Ed25519Verifier.sol` since its own merge of `bridge-eth`). Once E-M2b is merged into `bridge-eth`, `CONTRACTS`
defaults to this tree's `eth/bridge/contracts`. `ED25519=<dir>` stages `Ed25519.sol`, `Sha512.sol` (and
`Ed25519Verifier.sol` when there) over a contracts tree that predates E-M2a — the earlier runs took E-M2b at 0f997981
that way. What it does, in order:

1. builds `hlethbridge`, `hleth`, `hlwatch` and the Solana tree's `hlkey`; builds the contracts at clock factor 4032 (a
   generated `ClockFactor.sol` behind the `bridge-clock` remapping: HlAdmin's 7 days are 150 s, 24 hours 21 s; the
   realms' factor 1008); compiles Hyperlane 12.2.0's release input (`local/hyperlane-artifacts.sh`);
2. keys (deterministic per run), anvil `--hardfork osaka --slots-in-an-epoch 1 --block-time 1
   --disable-code-size-limit` (finalized two blocks behind the head; the size limit off while GnoLightClient with the
   real verifier is past EIP-170, finding 2), gnodev with GovDAO and the packages at genesis;
3. `hleth setup` (DONE after 30 transactions), a second run sending nothing, `hleth audit` PASS — before any realm;
4. the six realms staged with the run's launch values (the governor test1 in every realm, the fourteen contract
   addresses, the two routers, the signers' keys, the Ethereum seats, test1 in guardian seat 1 and the monitor's own gno
   watch key in seat 5, the meters' cap floor raised so a claim pays in a second, the realms' clock factor), uploaded,
   GovDAO initialized, the governor's `Launch`;
5. three signers, the updater (`-min-gap 3s -batch-wait 5s`), the relayer (both halves), the monitor with both watch
   keys, an alarm sink; every signer signing; the provider rule refusing one provider twice; no status beside a
   signer's public endpoint;
6. 10 GNOT and 500 BUBBLE gno.land → Ethereum, minted on anvil through the gate and the client;
7. 200 BUBBLE Ethereum → gno.land: signed (the one-round wait in the signers' logs), credited by `Process`, paid by
   `Claim` with the 100 BUBBLE fee first;
8. a stranger's `Mailbox.dispatch` toward gno.land naming the BUBBLE route with a token message: no signer serves a
   signature for its nonce, no claim is credited;
9. the identity lines held on the monitor (lock = supply + owed + voided − lost + in flight) and on every signer, the
   record rebuilt and checked, no flag raised;
10. a decision through r/hlgov (kind 7, set protocol fee): delivered to HlAdmin, shown pending by hlwatch and by
    `hleth decision status`, a member's `hleth decision execute` before its maturity refused, executed by the relayer
    after its 150 s — the ProtocolFee's fee 1000 wei;
11. a kind 12 recorded and cancelled by r/hlgov's cancel, executed at recording;
12. hlwatch's flag (gno.land, the gate, HlAdmin, each Ethereum transaction final): deliveries paused, the monitor's
    sends mirror; the governor's unpauses — gno.land's at once, the gate's and HlAdmin's through r/hlgov (kind 2) after
    21 s — every flag cleared, the mirror cleared;
13. a send after the unpause delivers; the identity holds; `hlwatch status` GREEN; no monitor flag in the alarms.

**The result.** The last run, on this branch at f981fab9 with E-M2b's contracts at b63cdad5: **PASS in 379 s**
(2026-10-14 11:08–11:14 UTC), `hlwatch status` GREEN, the alarms file two lines — the monitor's `deliveries:paused`
while the flag stood (11:13:35) and its clear after the unpauses (11:14:20), nothing else. The log is
`/root/src/eth-m3b-e2e-final.log` on the devbox; the machines' logs, `deployment.json`, `bridge.json` and the alarms
are kept in `/root/src/eth-m3b-e2e-final-state/` (the run's own state, `/root/.cache/eth-bridge-e2e`, is wiped by the
next run). The run before it, on c462df70 with E-M2b at 0f997981 and `ED25519` staged: PASS in 374 s
(`/root/src/eth-m3b-e2e.log`). The section times of the last run: the Go build 3 s, the contracts 51 s (our three
at factor 4032 and Hyperlane's release input), anvil and gnodev 7 s, `hleth setup` and its second run 30 s, the
audit 2 s, the realms and `Launch` 4 s, the machines 3 s, gno.land → Ethereum 10 s, Ethereum → gno.land 10 s, the
stranger's dispatch 9 s, the decision 155 s (its 150 s delay), the cancel 25 s, the flag 11 s, the unpauses 51 s
(r/hlgov's kind 2 waits its 21 s on HlAdmin), the send after 4 s, hlwatch 4 s.

## Fold F3 — the Go machines and tools on the merged contracts (2026-10-14)

Lane F3, branch `eth-f3` from `bridge-eth` at b50c4997 (plan e4; every lane merged: the realms, the verifier, the
contracts with fold F2, E-M3, E-M3b, the site). The local run on that tree failed at its first step: `hleth setup`
could not build HlAdmin's new Config ("parameter c: no value for the component ed25519 … the contract's ABI changed",
`/root/eth-int-e2e.log`). This section records what changed, item by item, the test that failed before each change
and passes after, the readings where the brief left room, the findings for other lanes, and the runs. Commits: the
tests first (75d1b259), ethx (c873e55f), the machines (4c35afa8), hleth (49c93888), the local run (8d9ebcd3), the
code audit's E2-ED3 (its test c7f66c4c, then c1033ca5) and these notes. Where this section and §1–§6 above differ,
this section holds. It answers E-M3's Q1 and Q2 (M3-NOTES.md §5) and this file's Q1, Q2, Q4 and Q5 (§4), and
resolves finding 2 (§3).

### F3.1 The changes

1. **`hleth setup` deploys the merged contracts** (`cmd/hleth/setup.go`). Step 4 always deploys `Ed25519Verifier`
   (the deployer's nonce 5, M2-NOTES.md §1 C) and then holds the deployed code against this tree's build: its
   EXTCODEHASH must be keccak256 of the build's runtime code (`Artifacts.Ed25519CodeHash`), or the setup stops there,
   before HlAdmin is deployed over a verifier `finishSetup`'s check 28 would refuse. HlAdmin's Config is packed by
   name in the struct's order with the five new fields: `ed25519` (the verifier), `ed25519CodeHash` (the build's),
   `localDomain` (Ethereum's domain), `maxPerMessage[2]` and `dayCap[2]` (the gate's launch limits, the same values the
   gate's initializer took). `HlAdminExec` is read from `HlAdmin.exec()` and recorded. The record's launch values carry
   the verifier's code hash, so a resumed setup with another build is refused. `finishSetup` passes (its 31 checks).
   The sixteen deployments land at the nonces M2-NOTES.md §1 C gives (the anvil test checks each address against
   CREATE(deployer, n), and HlAdminExec against CREATE(HlAdmin, 1)).
2. **`hleth audit`** (`cmd/hleth/audit.go`) audits the sixteen deployments. The record must name all sixteen. A new
   section holds the verifier three ways: the client's `ED25519()` and HlAdmin's `ed25519()` are the recorded
   verifier; its code is present and no EIP-7702 designator; its EXTCODEHASH is this tree's build's and HlAdmin's
   `ed25519CodeHash()`. HlAdmin's `localDomain()` is read. The code check now covers the verifier unconditionally and
   names every new masked immutable beside its value: HlAdmin's `ed25519`, `ed25519CodeHash`, `localDomain`;
   HlAdminExec's `ed25519`, `ed25519CodeHash`, `localDomain`, `maxPerMessage0/1`, `dayCap0/1` and `launch` (true);
   the client's `ED25519`. An immutable the audit has no value for still fails the audit. The five proxies' EIP-1967
   slots and the non-zero exit on any failure were already there. The anvil test now also sets the verifier's code
   one byte off: the audit fails exactly three ways (the code against the build, the hash against the build, the hash
   against HlAdmin's).
3. **NoCodeYet is a wait, and every HlAdmin error has its name.** `ethx/abi/gen.sh` could no longer build ("ours"
   compiled `src/` without the remappings once the implementations moved in); it now builds one copy of the whole
   project and inspects the interfaces, the implementations and the Guardians library from it. The regenerated ABIs
   differ from E-M3's only in HlAdminExec's `NoCodeYet(address)`, and `Guardians.abi.json` is new: it names
   `CurrentKey`, `TooManyGuardians` and `TooManyWatch`, which the library raises by selector (`replace` reverts with
   `checkReplace`'s answer in assembly), so no contract's ABI lists them. `hleth`'s own selector table is gone: it
   names a refusal by ethx's decoding. The executor (`agent/executor.go`) sorts `NoCodeYet` as "code": nothing is
   sent (the estimate refused it), the attempt is not a failure and never parks, the status says "waits for code: its
   implementation 0x… has no code yet", and it is looked at again five minutes later (`codeRetry`).
4. **hlwatch lists the decisions waiting for code** (`agent/monitor_views.go`). Every pending kind 8 or 9 whose
   implementation or successor has no code at the read block (both providers agreeing) is listed with its maturity:
   "#n kind 8: no code at 0x… yet — matured at T, waiting for code for D". One that matured longer ago than
   `-code-wait` (a week by default, `DefaultCodeWaitAge`) is marked STALE and alarmed `decision:code-wait:<n>`, and
   the page is not green; the alarm clears once the code is there or the decision is gone. The check before a kind 8
   or 9 no longer says "NO CODE: would be spent": since F2 it says the decision pends, and that whoever can create at
   that address decides when it runs. `hleth decision status` and `decision execute` say the same for a member.
5. **Burns follow a token's line of migrations, and amount-0 burns are skipped.** `ethx.BurnRoute` gains `Line`, the
   other realms of the token's line, and a burn may be addressed to any realm of it (`Takes`). The signers, the
   monitors (and so hlwatch) and the inbound relayer read each token's line from gno.land before every scan
   (`readLines`, `gnoside.MigrationOf`, plus the config's `successor` and `predecessor`), so a burn addressed to a
   successor a kind 6 named is signed, counted in flight and relayed like one to the original, and the inbound relayer
   delivers it through the line's live realm. A scan waits while a line cannot be read (the signer and the monitor:
   no verdict; the inbound relayer: its cursor stays): a burn to a successor it did not know would otherwise be passed
   over for good. This mirrors the Solana side, whose scans read a token program's dispatches whatever realm they name
   and judge the identity over the line. Amount-0 burns were already skipped (E-M3b's `burnRecOf` and the inbound
   scan); a test now holds both, and it passed before as well.
6. **`eth.deployBlock`** (`agent/config.go`). Every fresh scan starts there: a signer's burn index, a monitor's (and
   hlwatch's) record, stages and decisions, the inbound relayer's burns. A machine's `-from-block` still overrides it;
   with neither, the mailbox's first block is searched for as before. The local run writes it into `bridge.json` from
   `deployment.json` and passes no `-from-block`.
7. **`ethx.Sender` creates contracts** (`ethx/sender.go`). `Call.Create` sends a transaction with no "to" (estimated
   without one; a mined revert read by an `eth_call` without one). `Call.BeforeBroadcast` hears each version's nonce
   and hash after it is signed and before it is broadcast; its error stops the call. `Sender.Resume` finishes the
   transaction a stopped run left in the state file, and gives it to the pool again at once when the pool dropped it.
   `hleth setup` sends every transaction through it: `cmd/hleth/chain.go`'s own creation sender is gone. A creation's
   record (field, nonce, CREATE address, each version's hash) is written into `deployment.json` before each version
   is broadcast, and the sender's state is `<out>.sender.json`. No `forge create` or `cast` is in the production path.
8. **A waiting send's header is bought by its own fee** (`gnoside/dispatch.go`, `agent/outbox.go`, `agent/due.go`).
   The scan keeps, by nonce, the `fee` of the route's `SentTransferRemote` event in the same transaction as the
   send's Dispatch, with the realm that emitted it. The due rule counts a send's own fee when the event is from the
   send's own realm (the message's sender). A send whose event was never read (an index scanned before this change)
   counts at its route's fee now, and the status says how many did.
9. **The backup replaces the primary's pending transaction** (`ethx/sender.go`, `ethx/client.go`). When the pool
   holds a transaction of the key at the nonce a call will use and this sender did not send it, the call reads it
   (`Client.PendingAt`: `eth_getTransactionBySenderAndNonce` — reth, Erigon, anvil — else `txpool_contentFrom` —
   geth, anvil) and prices its first version at both fees 12.5% over the pending one's own, never below its own. A
   pending transaction this call's bounds cannot outbid keeps its nonce: the call answers `ErrStillPending` and
   nothing is broadcast. A provider that shows neither method leaves the old way: raise 12.5% at a time while the pool
   refuses. The relayer already lets the backup send only once the key has been quiet for its 60 s (`KeyQuiet`). The
   anvil run found one more thing: anvil estimates at "pending" by default, so the backup's estimate ran after the
   primary's transaction it was about to replace, sized the gas for a state that never came, and the backup's call was
   mined and reverted out of gas. Every estimate now names "latest".
10. **The local run** (`local/e2e.sh`). anvil runs with EIP-170 enforced (no `--disable-code-size-limit`; the five
    biggest runtimes are printed: the client implementation 20,669 bytes, the verifier 7,007, HlAdmin 17,712,
    HlAdminExec 21,880, the gate implementation 13,195) and with EIP-7825's transaction gas cap (`--enable-tx-gas-limit`).
    The record must name sixteen distinct contracts. The stage step swaps every `launchEth<Name>` placeholder
    hlmailbox carries, whatever the realm lists, each for the record's field named by lowering the first letter
    (`Mailbox` → `mailbox`, `HlAdminExec` → `hlAdminExec`, `Ed25519Verifier` → `ed25519Verifier`). It fails if the
    record lacks a field or a placeholder is left, and notes when fewer than sixteen are listed (today 14: F1 adds the
    two). Each route's fee and minimum are read from its json once the realms are up: the GNOT send is the minimum,
    the BUBBLE send two and a half minimums, the BUBBLE burned back the minimum, each fee on top. Every signer is
    checked to scan from the config's `deployBlock`.

11. **Small-order validator keys are alarmed** (the code audit's E2-ED3, round 2; `agent/smallorder.go`). A key is
    decoded as Go decodes it (laxly) and is small-order when [8]A is the identity: exactly the fourteen encodings of
    libsodium's seven blocked y values with either sign bit, which the test holds against the torsion subgroup
    derived from [L]P. Anyone can sign for such a key: Go accepts the signatures, and so does the client. The monitor
    (and so hlwatch) reads the client's trusted set and the set it was announced, and gno.land's current set and the
    set its head announces, each once by its hash. It alarms `valset:small-order:<key>` with the key's share of the
    set's power, and clears the alarm once no set read holds the key. hlwatch names the key and is not green. The
    updater alarms the keys of both sets an announcing header carries before it submits the header, and submits it
    anyway. This is an alarm, never a refusal or a flag: refusing would diverge from gno.land and could stall the
    client. The coordinator's other three round-2 items (E2-CT1: the ProxyAdmin's ownership at step 1 and the audit of
    the proxies' logs; E2-CT2: StageProgress's height; E2-CT3: the trusted header's leaves at `initialize`) wait for
    fold F4's contracts.

### F3.2 Readings (where the brief left room)

1. The verifier is always deployed at step 4. M2-NOTES.md §1 C's reuse of a verifier already on the chain (the
   Foundry script's optional `ed25519`) is not offered by `hleth setup`: it deploys this build and holds the code to it.
2. A matured kind 8 or 9 is "waiting for code" from its maturity. The age for the alarm defaults to the decision's own
   delay, a week. Before its maturity it is listed with "matures at".
3. The burn line is every realm `gnoside.MigrationOf` names (from the line's first realm to its last) plus the config's
   `successor` and `predecessor`. A kind 6 that names a router outside the line still has its burns ignored, with why.
4. The precedence for where fresh scans start is `-from-block`, then `eth.deployBlock`, then the code search.
5. The pending-transaction reading runs only when the pool's pending count is past the nonce, and is one more request
   per send.
6. A send's own fee counts only when its event came from the send's own realm.

### F3.3 Findings for the other lanes

1. **F1**: the stage step reads hlmailbox's placeholders by name. F1's `launchEthEd25519Verifier` and
   `launchEthHlAdminExec` (read at eth-f1 84c4dee4) map to the record's `ed25519Verifier` and `hlAdminExec` with no
   change here. The fees and minimums are read from the realms, so F1's 10 GNOT / 200 BUBBLE with minimums 20 / 400
   need no edit of the run (the BUBBLE minimum must stay above its fee, which the run checks).
2. **Everyone**: `ethx/abi/gen.sh` was broken on the merged tree (above), now fixed.
3. **E-M2b (none needed)**: HlAdminExec's `NoCodeYet` reaches the machines through HlAdmin's `execute` (delegatecall
   bubbles it). The Guardians library's selector-raised errors are named only through the library's own ABI.

### F3.4 The runs

On the devbox (`root@<devbox>`), the tree synced to `/root/src/eth-f3` by BUILD.md's rsync, Go 1.27.1, Foundry
1.8.5. The real-contract tests read one build of this tree's contracts, `forge build --force --ast` of
`eth/bridge/contracts` (src, test and script) into `/root/src/eth-f3-build/out`, and the Hyperlane release's compile
from `local/hyperlane-artifacts.sh` into `/root/src/eth-f3-build/hl`:

```
cd /root/src/eth-f3/eth/bridge
gofmt -l agent cmd ethx gnoside tools     # nothing
go vet ./...                              # clean
ETHX_CONTRACTS_OUT=/root/src/eth-f3-build/out HLETH_CONTRACTS_OUT=/root/src/eth-f3-build/out \
  HLETH_HYPERLANE_DIR=/root/src/eth-f3-build/hl go test -race -count=1 -v ./...
```

- **The gates** (the last commit's tree; log `/root/src/eth-f3-gates.log`): 141 tests pass (and 3 subtests), none
  fail, none skip — `agent` 78, `cmd/hleth` 13, `cmd/hlethbridge` 1, `cmd/hlwatch` 2, `ethx` 41, `gnoside` 6. The
  real-contract tests all ran: `TestCalldataOnTheRealContracts`, `TestMachinesOverTheRealContracts`,
  `TestAnvilSetupAndAudit`, `TestAnvilResumesAPendingCreation`, `TestRealBuildsEachWrongImmutableFails`,
  `TestSenderOnAnvil`, `TestSenderCreatesAndTakesOverOnAnvil`. At b50c4997 the same command failed one test,
  `TestAnvilSetupAndAudit`, on the e2e's error.
- **Failing before.** The new tests that compile against b50c4997 were run there (`/root/src/eth-f3-before`: that
  commit's Go tree with only the new test files laid over it; log `/root/src/eth-f3-build/before.log`). Twelve of
  thirteen fail:
  - `TestEveryAdminErrorIsNamedBySelector`: `NoCodeYet`, `TooManyGuardians`, `TooManyWatch` and `CurrentKey` named "".
  - `TestSenderReplacesThePrimarysPendingTransaction`: "replacement transaction underpriced".
  - `TestExecutorWaitsForCode`: "parked after 5 failures", the revert undecoded.
  - `TestWatchListsDecisionsWaitingForCode`: the page does not list it.
  - `TestInboundFollowsTheLine` and `TestSignerSignsABurnToTheLinesSuccessor`: the burn ignored ("not GNOT's route
    realm").
  - `TestFreshScansStartAtTheConfigsDeployBlock`: the index starts at 1, not 42.
  - `TestUpdaterCountsEachSendsOwnFee`: no header bought (0.0002 ETH counted against 0.0084).
  - `TestAnvilSetupAndAudit`: the e2e's setup error.
  - `TestRevertNameByHlAdminsSelectors`: `NoCodeYet` and `CurrentKey` unnamed.
  - `TestMonitorAlarmsASmallOrderKey` and `TestUpdaterAlarmsASmallOrderKeyBeforeAnnouncing`: no alarm.
  - `TestBurnsOfAmountZeroAreSkipped` passes there too: E-M3b already skipped them. It holds the rule now.

  The tests of the new calls (`TestSenderCreates`, `TestSenderResumes`, `TestSenderCreatesAndTakesOverOnAnvil`,
  `TestScanKeepsEachSendsOwnFee`, `TestStatusShowsADecisionWaitingForCode`, `TestSmallOrderKeys`) do not compile
  there.

- **The end-to-end run** (`BL=/root/.cache/eth-f3-e2e sh local/e2e.sh`, the last commit's tree): **PASS in 407 s**
  (2026-10-14 15:45–15:52 UTC), `hlwatch status` GREEN. Log: `/root/src/eth-f3-e2e-final.log`. The machines' logs,
  `deployment.json`, `bridge.json`, the setup's and the audit's logs and the alarms are kept in
  `/root/src/eth-f3-e2e-final-state/`, because the next run wipes `BL`. The alarms file has two lines, the monitor's
  `deliveries:paused` while the flag stood and its clear, and nothing else (no small-order key: gnodev's one validator).
  `hleth setup` sent 31 transactions. Its second run sent nothing. The audit printed 123 RPC calls, gave 163 "ok" lines
  and no FAIL, and passed before any realm went up. anvil enforced EIP-170 (the client implementation 20,669 bytes,
  the verifier 7,007, HlAdmin 17,712, HlAdminExec 21,880, the gate implementation 13,195). hlmailbox carried 14
  placeholders, all swapped. The routes read GNOT fee 5 and minimum 10, BUBBLE fee 100 and minimum 200 (the tree
  before F1's fees), so 10 GNOT and 500 BUBBLE went out and 200 BUBBLE came back, paid with the 100 fee first. Section
  times: the build 4 s; the contracts 62 s (ours at factor 4032 and Hyperlane's release input); anvil and gnodev 7 s;
  the setup and its second run 32 s; the audit 2 s; the realms and `Launch` 5 s; the machines 4 s; gno.land →
  Ethereum 18 s; Ethereum → gno.land 12 s; the stranger's dispatch 8 s; the identity 4 s; the decision 156 s (its
  150 s delay); the cancel 30 s; the flag 13 s; the unpauses 42 s; the send after 3 s; hlwatch 5 s. An earlier run of
  the same code without E2-ED3 passed in 402 s (`/root/src/eth-f3-e2e.log`, state in `/root/src/eth-f3-e2e-state/`).
- **When F1 merges**: the run swaps sixteen placeholders and reads F1's fees and minimums with no change here. Fold
  F4's contracts need the E2-CT1–CT3 halves (F3.1 item 11) and a new run.

### F3.5 Following F4, and round 2's fact check (second pass, 2026-10-14)

`eth-f3` was fast-forwarded to `bridge-eth` at b1fca49b (F4's contracts and F1's realms merged), the Go halves of
M2-NOTES.md "Fold F4" (F4.1) were done, then the coordinator's three items from round 2's fact check (plan e5,
42071ad7), and `bridge-eth` was merged again at 972fd31c (plans e5 and e6, the round-2 audits: documents only).
Commits: 9a650fbf (ethx and the machines), f3097dbf (hleth), e01adb3f (the fact check's items), c64e5a92 (the merge)
and these notes. Where this section and F3.1–F3.4 differ, this section holds.

**The changes F4 asked of Go** (F4.1, items 1–8):

1. **ethx.** `StageProgress` (topic 0xc457ce42…) decodes four values: headerHash, height, tallied, total.
   `initialize(string,uint32,bytes[],(bytes32,int64)[],address)` (0x586832f5) is in `testdata/sigs.txt`.
   `NoNewSignature` (0xe10e1571), `TrustedTime` (0xf3b3b4ed) and `ProxyAdminNotHeld` (0x77bec738) are named. The errors
   test now also reads `GnoLightClient.sol`. The ABIs were regenerated by `gen.sh`. The real-contract tests initialize
   with the trusted header's sixteen leaves (`HeaderLeaves`), with anvil's clock set beside the header's time.
2. **The watch judges a stage by its event's height** (`agent/monitor_eth.go`). It reads no calldata, so a stage made
   through another contract is judged like any other. `stage:unread` is gone. An entry an earlier build left "unread"
   is dropped and read again.
3. **The updater sends no call the client would refuse.** A staging call whose estimate reverts `NoNewSignature`
   (every signature it carries is already counted) is skipped and logged. Nothing is sent for it, and the next calls
   finish the header.
4. **`hleth setup` follows the nonce plan.** The plan is fixed and recorded before the first transaction:
   `firstNonce`, `adminNonce` = first + 15, `adminAt` = CREATE(deployer, adminNonce). The ProxyAdmin goes at the first
   nonce. Its ownership goes to `adminAt` in the deployer's next transaction (step `proxyAdminHandOver`), before any
   proxy exists. The thirteen creations of steps 2–6 take first + 2 … 14, and HlAdmin takes first + 15. Its address is
   checked against `adminAt`, and step 10 no longer touches the ProxyAdmin. The sender's estimate simulates each
   transaction before it is sent, so one that would revert is refused and its nonce is untouched. Each is sent only at
   its planned nonce: `onPlan` checks that latest and pending are both the plan's before anything is signed, and
   `atNonce` checks again before broadcast (the BeforeBroadcast guard). A transaction outside the plan stops the setup
   with `errPlan`, and nothing more is sent.
5. **The trust root.** The client's header is read just before step 4's proxy and passed as its sixteen leaves. The
   same checks apply whichever the source, the node or `-trusted-file`:
   - the header is sane and of the chain;
   - its set is sane and hashes to `ValidatorsHash`;
   - it announces no change;
   - its time is within 30 minutes of Ethereum's newest block (the client allows an hour).

   `-trusted-file` is now `{"header": <the node's /block block.header>, "validators": [{pubkey, power}]}`.
   `-allow-old-header` is gone. The header's time is recorded as `launch.trustedTime`.
6. **`hleth audit` reads the history** (E2-CT1, F4.1 item 8). Each check below reads logs from `deployBlock` to the
   audited block. None of it shows in any state.
   - Every contract sits at its plan's CREATE address. HlAdmin is at `adminAt`, and HlAdminExec is HlAdmin's CREATE
     at its nonce 1.
   - (a) The ProxyAdmin has exactly two `OwnershipTransferred` logs: to the deployer in its creation, then to HlAdmin
     in the recorded hand-over. That hand-over is the deployer's transaction at first + 1.
   - (b) Each proxy has one `Upgraded` (to its recorded implementation) and one `AdminChanged` (to the ProxyAdmin),
     both in its creation transaction.
   - (c) A simulated `initialize` from a stranger is refused on all five proxies, as on the two implementations.
   - (d) The client's first `HeaderRecorded` is the launch's header, and with `-gno-rpc` also the node's block.
   - (e) Exactly one `SetupDone`, and no `Dispatch` or `Process` before it.

**Round 2's fact check** (the coordinator's three items):

7. **Prices fail closed** (E2-FC7). The updater refuses to start unless `eth.prices` gives every route's token a
   positive rate in ETH. So does the relayer, when it names the contracts (`Config.CheckPrices` names the missing
   tokens). `headerDue` no longer has a branch for "the config prices no token: every waiting send buys its header".
   The test config and the e2e's carry `{"GNOT": 1, "BUBBLE": 1}`.
8. **A kind 9's successor is read before the vote** (`Watch.SuccessorCheck`, `ethx.ReadSuccessor`). The successor's
   four answers are read as `HlAdminExec._transfer` reads them, beside its code:
   - `predecessor()` is the HlAdmin that holds the decision;
   - `inherited()` is false;
   - `successor()` is none;
   - `clockFactor()` is that HlAdmin's.

   The check gives one of four verdicts:
   - "immutable" only when all four hold, the code is no EIP-7702 designator, and its hash is both the decision's and
     a reproducible build's;
   - "MUTABLE: would be spent" when HlAdmin would spend the decision;
   - "NO CODE YET: pends";
   - "NOT SHOWN IMMUTABLE" when HlAdmin would execute the decision but no build vouches for the code.

   The page prints this beside every pending kind 9. `hlwatch check -kind 9` reads it against the live HlAdmin.
9. **Flag keys keep 0.05 ETH** (E2-FC3). Every five minutes the monitor reads the ETH of the gate's six keys: the
   four guardians' seats and the two watch slots. A key under 0.05 ETH raises `float:key<n>`, which names the base fee
   that key can still send a gate flag under. That fee is the balance ÷ (1.2 × 112,448 gas × 2): 0.01 ETH reaches
   ≈37 gwei and 0.05 ETH ≈185. The watch key's planned rate now uses the measured flag of 112,448 gas, not 60,000.

**Readings.** (1) HlAdmin always goes at first + 15, because the Go setup always deploys the verifier (F3.2 item 1).
`Setup.s.sol`'s first + 14 for a verifier already on the chain is not offered. (2) The setup holds the trusted header
to 30 minutes, keeping the other half of the client's hour for steps 4–7. (3) A relayer that names no contracts buys
no header, so it starts without prices. (4) "NOT SHOWN IMMUTABLE" is a fourth verdict. With only the brief's three, a
successor that HlAdmin would execute but that matches no given build would have to be called "would be spent". (5)
The kind 9 check reads the successor's four answers, not its bindings. Plan e6's RT3 ("a kind 9's successor must hold
this HlAdmin's bindings … or the decision is spent") is not yet in `HlAdminExec._transfer`. Once the contracts check
the bindings, hlwatch should read them too. (6) The guardians' keys get a floor alarm, not a trailing-rate runway:
they spend only when they flag. The monitor's own watch key keeps its runway as well.

**Findings.** (1) **hlwatch's page panicked on any pending kind 9.** `targetCheck` sliced a kind 9's 64 bytes of
parameters as a kind 8's 96 before it looked at the kind, so `status` and `watch` would have crashed while a
successor was pending. This is fixed, and `TestSuccessorCheckReadsItsState` covers it (M9 below). (2)
`CodeCheck` said "NOT the decision's …: would be spent" of a target with no code, but HlAdmin pends such a decision
(`NoCodeYet`). The text is fixed. (3) **Plan e6 asks more of the Go side than this pass did.** That work is not done
here:
   - RT1: the creation-transaction audit, and no decision above the last recorded number;
   - RT2, RT4, RT5: the fee-funded delivery budget priced by its leaves, batches of four, the 6-hour send-age header
     and the model price;
   - RT6: `deployBlock` in place of F4's launch-veto checks;
   - RT8: the 50-gwei emergency cap and the 0.2 ETH updater float;
   - RT9: undelivered sends in the refusal rule;
   - RT10: kind 8's post-call check in hlwatch.

**The runs** (devbox, Go 1.27.1, Foundry 1.8.5). The contracts are `forge build --force --ast` of the merged tree in
`/root/src/eth-f3-build4/out`; the Hyperlane release is in `/root/src/eth-f3-build/hl`.

- **Gates.** `gofmt` reports nothing and `go vet ./...` is clean. 146 tests pass (and 3 subtests); none fail and none
  skip. By package: `agent` 82, `cmd/hleth` 14, `cmd/hlethbridge` 1, `cmd/hlwatch` 2, `ethx` 41, `gnoside` 6. The
  logs are `/root/src/eth-f3-p2/suite3.log` and `gates3.log` (gofmt and vet) for the merged tree, c64e5a92, and
  `suite2.log` for the same code before the merge. The new tests are:
  - `TestAnvilSetupHoldsThePlanAndTheTrustedHeader`;
  - `TestUpdaterSkipsACallWithNothingNew`;
  - `TestMachinesRefuseToStartWithoutPrices`;
  - `TestSuccessorCheckReadsItsState`;
  - `TestFlagKeysKeepTheirFloat`.

  The extended tests are `TestAnvilSetupAndAudit` (the plan, the history, the proxies' initializers, two tamperings
  caught on the logs alone) and `TestMonitorStageForAnUnknownHeader` (stages made through a relay contract).
- **Failing before (B0).** b1fca49b's Go tree was run against F4's contracts (log
  `/root/src/eth-f3-p2/b0-old-tree.log`): 4 fail, 137 pass, 0 skip.
  - `TestMachinesOverTheRealContracts` and `TestCalldataOnTheRealContracts` fail with "initialize: argument count
    mismatch: got 6 for 5".
  - `TestAnvilSetupAndAudit` fails with "GnoLightClient.initialize: no value for its parameter headerLeaves".
  - `TestEveryAdminErrorIsNamedBySelector` fails because `ProxyAdminNotHeld` is unnamed.
- **Failing before (M1–M11).** Each fix was reverted alone on the finished tree and its test run. All 11 mutants
  fail. The runner is `/root/src/eth-f3-p2/mutants.py` and the log is `/root/src/eth-f3-p2/before.log`.
  - M1, the plan unguarded: the setup sends on, and only HlAdmin's constructor stops it (`ProxyAdminNotHeld`).
  - M2, `-trusted-file` unchecked: the client's estimate reverts `TrustedTime` instead of the setup refusing first.
  - M3, no history: the audit lacks every history line.
  - M4, the proxies' initializers unread.
  - M5, `NoNewSignature` sent: the update fails "call 1 of 3: reverts: NoNewSignature".
  - M6, the event's height ignored: the stage alarms are wrong.
  - M7, the ABIs before F4: four ethx tests fail, the three selectors named "".
  - M8, no price check: both machines start.
  - M9, the old kind 9 slicing: the panic.
  - M10, the code-only kind 9 check: no verdict on the page.
  - M11, no flag-key floats: no alarm at 0.01 ETH.
- **The end-to-end run** used the merged tree (c64e5a92; the code is e01adb3f's):
  `BL=/root/.cache/eth-f3-e2e sh local/e2e.sh`. **PASS in 366 s**, 2026-10-14 17:39–17:45 UTC, with `hlwatch status`
  GREEN. The log is `/root/src/eth-f3-p2/e2e.log`. The machines' logs, `deployment.json`, `bridge.json`, the setup's
  and the audit's logs and the alarms are kept in `/root/src/eth-f3-p2/e2e-state/`, because the next run wipes `BL`.
  - **The setup** sent 31 transactions, and its second run sent nothing. It printed the plan: the ProxyAdmin at nonce
    0, the hand-over at 1, HlAdmin at 15 (0x0B306BF9…).
  - **The audit** printed 145 RPC calls and gave 204 "ok" lines with no FAIL. Among them are the history's lines:
    sixteen plan addresses, two owners, one upgrade and one admin change per proxy, the first record, one SetupDone
    and nothing dispatched before it. All seven initializers were refused.
  - **The realms.** hlmailbox carried 16 placeholders, all swapped. The routes read F1's fees: GNOT fee 10 and minimum
    20, BUBBLE fee 200 and minimum 400. So 20 GNOT and 1,000 BUBBLE went out and 400 BUBBLE came back, paid with the
    200 fee first.
  - **The machines** started on the e2e's prices.
  - **The alarms file** has two lines: the monitor's `deliveries:paused` while the flag stood, and its clear. No
    `float:key` alarm was raised, because the run funds every key with 100 ETH.

## Fold F7 — audit round 2's plan red team, the Go half (2026-10-14)

Lane F7, branch `eth-f7` from `bridge-eth` at 161afc7f (every lane and fold merged), plan e6. It folds the Go half of
`audits/r2/plan-redteam.md`'s E2-RT1–RT10 and the realms audit's E2-RL9. F5 (the contracts) was merged at abfe9ade
with plan e7 and followed (F5.1 of M2-NOTES.md "Fold F5"); F6 (the realms) at 7037e6b8 and followed. Commits:
5a6ff9da (E2-RT1 and E2-RT6 in hleth), 7c8000c6 (E2-RT2, RT4, RT5, RT6, RT8 in the machines), bdbbe330 (E2-RT9,
RT3, RT10 and RT1's hlwatch half), 2af44cbe (merge F5), 2545f996 (merge F6), 67b0d949 (following F5 and F6: the launch
line, F5's immutables and refusals, the audit takes no `setupDone` as proof, E2-RL9), 9c71630b (the cut around a
landing send, which the first e2e found), 47252677 (a moving cut retried quietly, two audit tests), ba27c62c (the
updater's floor held apart from the cap in its test), then these notes.
Where this section and §1–§6 or Fold F3 differ, this section holds.

### F7.1 The changes

1. **E2-RT1 (high), hleth: every creation transaction held byte for byte** (`cmd/hleth/creation.go`, `audit.go`,
   `repro/`). `hleth audit` gains a section "the creation transactions". For each of the deployer's fifteen creations
   (F5.1 item 11's table, n…n+15; HlAdminExec is HlAdmin's CREATE at nonce 1) it reads the transaction and its receipt
   and checks four things. The transaction is a top-level creation (`to` empty) from the deployer at its planned
   nonce. The receipt's `contractAddress` is the recorded address, with status 1. The input equals, byte for byte,
   the build's creation code followed by the ABI encoding of the constructor arguments the setup used. Those are
   rebuilt from the record alone (`creationOf`): this tree's builds for ours, the Hyperlane release's compile
   (`local/hyperlane-artifacts.sh`, the same compile the runtime comparison uses) for Hyperlane's and OpenZeppelin's.
   A difference is named: the code ("N bytes differ, the first at offset k"), a byte past a canonical encoding, or the
   argument and both values (`creationDiff`, `repro.SplitCreation`). The record now keeps the trusted header's
   sixteen leaves (`Launch.TrustedLeaves`) so the client proxy's `initialize` data can be rebuilt; a record whose
   leaves do not hash to its trusted hash is refused. The comparison is what refuses every planted state at a launch
   (F5.1 items 11 and 13); HlAdminExec's code is covered by HlAdmin's input, its runtime comparison and
   `HlAdmin._execCodeHash` (item 9).
   Tests: `TestAnvilAuditHoldsEveryCreationInput` (a ProxyAdmin created with one byte past its creation code has the
   same runtime, owner and logs; the audit fails on its input alone, exactly once), `TestCreationDiffNamesTheArgument`,
   `TestClientInitFromTheRecord`; `TestAnvilSetupAndAudit` now requires the fifteen "byte for byte" lines.
   **hlwatch's half**: `-creation-tx ADDR=0xTX` (repeatable; a bare `0xTX` for `check -target`) gives a kind 8 or 9
   target's creation (`creationCheck`). It is read through both providers. It must be a top-level creation whose
   receipt created the target, in a finalized block. Its input is split against the matched build, and each
   constructor argument is printed beside the value it must have (`argExpectations`: the live deployment's; for a
   successor, the Config with this HlAdmin's bindings, no seats or watch slots, start 0, predecessor this HlAdmin).
   With no transaction given the verdict is NOT SHOWN IMMUTABLE ("no creation transaction given (-creation-tx)"). A
   target created by a factory is refused ("made through a factory, its initcode is not shown"); F5.1 item 11
   allows the creating frame's trace, which hlwatch does not read.
2. **E2-RT2, RT4, RT5 (high/medium): the fee-funded delivery budget** (`agent/budget.go`, `due.go`, `outbox.go`,
   `updater.go`, `relayer_out.go`). Before, a header was bought when the waiting fees covered the last accepted
   update's gas, read from its receipt: a stranger chooses that gas (RT4), and the deliveries were not counted (RT2).
   The heartbeat ran on the client's freshness, which anyone's update resets (RT5), and leaves of any size were
   delivered from. Now:
   - **The cost.** A header is due for waiting route sends only while the fees of the sends it serves cover the
     whole cost. Each send counts at its own fee, from its `SentTransferRemote`. The cost is the header's (the model,
     for the client's set) plus every delivery's (the model, by the leaf it would be proven from), at the live base
     fee plus the tip and the config's `eth.prices`.
   - **The model** (E-M2b's measurements, M2-NOTES.md §6). A header costs 250,000 + 15,600·n + 221,000·k gas for n
     validators and the k signatures that cross 2/3. Its intercept sits above every measurement. A header is staged
     when it passes EIP-7825's cap over 1.2, each call paying the set's share again. A delivery from a leaf of m
     Dispatch events costs 311,300 + 101,000·(m−1) gas (F7.2). `eth.updateGas` is now the operator's override of the
     model, never a receipt.
   - **Batches** are at most `eth.reemitBatch` = 4 (the cheapest, √(1.45M/94K)). A send in a leaf of more than four
     Dispatch events is never delivered from it: the event is marked unusable and the send re-emitted into the
     relayer's own batch. Each send is proven from its cheapest usable leaf, not its newest.
   - **The budget.** A waiting send six hours old (`SendAgeHeader`) buys its own header within the budget, whatever
     anyone else's updates did: the heartbeat is the oldest waiting send's age. The relayer re-emits the batch two
     minutes before (`updateLead`). `delivery:age` alarms at seven hours (`DeliveryAgeAfter`), on the updater and the
     monitor. The monitor's stall counts a send only from its six hours. The monitor's `client:lag` (freshness) and
     the updater's idle heartbeat are gone. The updater's two-minute spacing counts its own updates only, so a
     stranger's update every 119 s cannot hold it off.
   - **Outside the budget.** Decisions, their headers and deliveries, `execute` and the flags go under the emergency
     cap.
   - **The ledger** is the updater's, kept in its state file (`budget`). Each header it buys books the sends it
     serves, once each: each send's fee is credited, and each delivery's model cost is debited at the price when it
     was bought. The header's receipt cost is debited when route sends bought it; a decision's header books its route
     sends, not its cost. A deficit (an update that cost more than the model said, a base fee that rose) carries as a
     negative balance. Sends whose own fees cover their whole cost still go and repay it first. No six-hour send
     draws until the balance covers what its fees lack. A lost state file starts at zero, never with a debt
     forgotten.
   - **The relayer** delivers a route send from the updater's header (the transaction behind its `HeaderRecorded`
     log was sent from `eth.updater`: `headerOurs`) as the budget booked it. From any other header (a stranger's, or
     its own backstop) it delivers only while the send's own fee covers its delivery's model cost at the live price.
     Otherwise it waits for a lower base fee, or for anyone's `Mailbox.process`.
   Tests: `TestUpdaterPricesEveryDelivery`, `TestRelayerReemitsASendFromABigLeaf`,
   `TestRelayerHoldsAStrangersHeaderToTheSendsFee` (and its six-hour send drawing on the budget),
   `TestBatchOfFourAndTheCheapestLeaf`, `TestBudgetSurvivesARestart`, `TestMonitorSendAgeIsSevenHours`.
3. **E2-RT3 (medium), hlwatch: builds masked, immutables printed** (`agent/targets.go`). `-build LABEL=FILE` takes
   forge's artifact (its `deployedBytecode.immutableReferences` and the AST, through `repro`). A target is held to a
   build masked: every byte equal outside the immutables. Each immutable is printed beside the value it must have
   (`expectations`, by name): Hyperlane's (`MailboxClient.mailbox`, `localDomain`, `HypERC20._decimals`, the scale,
   `Indexed.deployedBlock` from the creation's block), the client's (`mailboxPkgPathHash`, `ED25519`), a successor
   HlAdmin's (this HlAdmin's sixteen bindings, `predecessor` this HlAdmin, `exec` its CREATE at nonce 1, and F5's
   `_execCodeHash`, `_startSequence` 0 and `mailboxPkgPathHash`), and a successor's HlAdminExec's (its settings, the
   launch limits any). An immutable with no expected value, or a value that differs, makes the verdict NOT SHOWN
   IMMUTABLE. A bare hash (the old `-build`'s, the immutables zero) matches no deployed code that has immutables, and
   is said to. A kind 8 is also held to the build the proxy runs (a token's HypERC20, …). For a kind 9 the successor's
   sixteen bindings are printed beside this HlAdmin's (`ReadBindings`). A mismatch is "THE SUCCESSOR IS BOUND
   ELSEWHERE … HlAdmin spends it "bindings"", which F5's kind 9 now does on chain. Bindings that do not answer mean
   the execution reverts and pends. The successor's HlAdminExec at CREATE(successor, 1) must be the HlAdminExec build,
   with its immutables the successor's.
   Tests: `TestImplementationHeldToItsBuild`, `TestSuccessorHeldToItsBuildAndBindings`, and
   `TestSuccessorCheckReadsItsState`, `TestCodeCheck`, `TestReadBuilds` (updated).
4. **E2-RT6 (medium): `eth.deployBlock`, and the launch line.** `hleth setup` records `finishSetup`'s block as
   `deployBlock` in `deployment.json` and in the machines' config part it writes (`-config-out`; the e2e builds
   `bridge.json` from it). Every scan of a fresh install starts there: the signers' and the monitors' burn index, the
   monitor's header record (seeded from the client's counter and running hash at the block before), the stages, the
   decisions, hlwatch, the inbound relayer. With no `deployBlock`, the first block at which `setupDone()` is true is
   searched for. F5 then drew the line on chain (F5.1 item 12): `HlAdmin.launchNonce()` is the mailbox's nonce
   `finishSetup` recorded. `ethx.ReadLaunchLine` reads it from the configured HlAdmin, following `predecessor()` back
   to the launch's (a successor's is 0). The burn rule (`ethx.MatchBurns`/`ScanBurns`) counts no message numbered
   below it. So the signers, the monitors, hlwatch and the inbound relayer never count, sign or relay a burn
   dispatched before the launch, even one in finishSetup's own block. The line is read at each scan's last block.
   An index that holds a burn below it (a scan started early by a guessed start) drops it. A config naming no
   HlAdmin counts no burn. `eth.deployBlock` is now the scans' start only. The status pages print the line. In the
   audit: `launchNonce()` must equal the number of Dispatch logs before SetupDone, and every message below the line
   is listed (F7.2 for which fail).
   Tests: `TestFreshScansFindFinishSetupsBlock`, `TestMonitorRecordStartsAtTheDeployBlock`,
   `TestNoBurnBelowTheLaunchLine` (the deployer's mint and burn back at nonce 0, the line 1: the signer signs alice's
   burn and not the deployer's, the identity holds, the inbound relayer queues only alice's, an early index drops it,
   no HlAdmin no scan), `TestMatchBurnsBelowTheLaunchLine`, `TestReadLaunchLine`, `TestJudgeEarlyLogs`.
5. **E2-RT8: the emergency cap and the updater's float.** `DefaultEmergencyGwei` is 50 (was 200). The updater's float
   floor is `UpdaterFloatWei` = 0.2 ETH. Below it, `float:updater` alarms on the updater and on the monitor, naming
   the base fee one header still fits under. **The relayers' key may buy a decision's header when the updater's float
   cannot (yes, as recommended)**: `decisionBackstop` buys it, under the emergency cap and outside the budget. It does
   so five minutes after the decision became servable if the updater's balance is short of the header's gas × 1.2 at
   twice the base fee plus the tip, and after thirty minutes regardless (the updater down).
   Tests: `TestEmergencyCapAndTheUpdatersFloat`, `TestRelayerBackstopBuysADecisionsHeader`.
6. **E2-RT9 (high): sends in flight in the signers' identity** (`agent/sends.go`). The signers and the monitor share
   one index of the sends locked on gno.land (`sendIndex`). It reads hlmailbox's messages by nonce up to its count at
   the cut's height, keeps each route send, and marks it delivered from the mailbox's `delivered(id)` at block F
   (both providers). A send dispatched at or below the gno height with no delivery at F is in flight out, as plan
   e6 §5.6 says. It is subtracted from the lock's room: "supply + uncredited burns + undelivered sends > lock − owed −
   voided + lost" stops the signers. The signer's status shows "undelivered".
   **The cut** (found by the first e2e, F7.3 item 1): a gno node answers a render "at height H" with the latest
   realm objects, so the reads are no snapshot. hlmailbox's count is read before the routes and again after; if it
   moved, the round takes no verdict and the cut is retried. The signer retries quietly, alarming
   `signer:no-verdict` only at the fifth such round in a row. The monitor uses its persisted `cut` alarm.
   Tests: `TestSignerCountsUndeliveredSends`: a forged mint up to the undelivered amount stops the signers, still
   on Ethereum or burned back (then never signed), and the honest delivery holds. `TestSignerCutAroundALandingSend`:
   no halt and no alarm on a landing send, an alarm at the fifth moving round. `TestMonitorCutAroundALandingSend`: no
   verdict on the moving poll, the forged mint flagged on the next.
7. **E2-RT10 (low), hlwatch: a kind 8's call decoded** (`DecodeCall`) against the matched build's ABI. It prints
   "then call initializeV2(address minter = 0x…) through ProxyAdmin.upgradeAndCall — read what it does before the
   vote". Otherwise it prints "UNDECODED: …" with the reason: no build's ABI, a selector no function of the build has,
   arguments that do not decode, or bytes past a canonical encoding. An undecoded call makes the verdict NOT SHOWN
   IMMUTABLE. Tests: `TestDecodeCall`, `TestImplementationHeldToItsBuild`.
8. **E2-RL9: r/hlgov's bodies through HlAdmin on anvil** (`cmd/hleth/rl9_test.go`,
   `TestRHlgovBodiesThroughHlAdmin`). The bodies are **rebuilt byte for byte as the realm builds them**: hleth's
   builders, which `TestBodiesAreRHlgovs` holds to the realm's own vectors (`eth/realm/hlgov/hlgov_test.gno`,
   `bodyHex(kind, seq, params)`; unchanged by F6). Their parameters carry this deployment's addresses, so each runs
   against the real contracts. After `hleth setup` on anvil, the test impersonates the mailbox, delivers each body
   through `HlAdmin.handle(gno.land's domain, r/hlgov, body)`, warps the clock, and runs `execute`. It checks, per
   PLAN.md §13:
   - kind 1 executes at its recording and cancels a pending kind 12; a malformed one is spent "params";
   - kind 2 refuses `NotMature` until its day, then executes;
   - kinds 3, 5, 6, 7, 8 and 12 refuse until seven days, then execute, and the state moved: the ISM, slot 6 on both
     HlAdmin and the gate, the router, the fee and beneficiary, the gate re-pointed at its own implementation, token
     1's limits; the cancelled kind 12 moved nothing and answers `WrongDecision`;
   - kind 4 is spent "reserved" at execution; kinds 10, 11 and 13 are spent "unknown kind" at recording, and a body
     over 512 bytes "too long";
   - kind 8 to HlAdmin (not one of the five proxies) is spent "params"; to an implementation with no code it pends
     (`NoCodeYet`);
   - kind 9 to a successor bound to another client is spent "bindings"; to one with no code it pends; to an honest
     successor (HlAdmin's build, its Config) it executes, `Retired`, and the ProxyAdmin is the successor's;
   - after the hand-over, both pending decisions are spent "retired" and `handle` reverts `RetiredAdmin`.
9. **Following F5** (M2-NOTES.md F5.1).
   - **ABIs and refusals.** The ABIs were regenerated (`ethx/abi/gen.sh`). `ForeignExec` and `ImplementationMoved`
     are named by their selectors, and `launchNonce()` and `mailboxPkgPathHash()` have F5's (`TestF5SelectorsAreNamed`).
     The executor treats `ForeignExec`, `ImplementationMoved` and `AuthorityLost` as alarms, not failures to retry.
     It sends nothing, alarms `execute:<n>` with what the refusal means, and looks again hourly; a cancel ends it
     (`TestExecutorAlarmsOnFoldF5sRefusals`).
   - **The three new immutables.** `hleth audit` expects `HlAdmin.mailboxPkgPathHash` (keccak256 of the path),
     `_execCodeHash` (the EXTCODEHASH of the recorded HlAdminExec, whose code is held to the build) and
     `_startSequence` (the launch's). Without them every honest launch failed the audit (seen: three FAILs on the
     merged contracts).
   - **No `setupDone()` as proof** (item 13). The one SetupDone log must not be in HlAdmin's creation transaction,
     whose initcode runs as HlAdmin and could log one (`TestSetupDoneByItsCreationFails`). A top-level
     `finishSetup()` is said ok; any other caller is a WARN (F7.2). `finishSetup` is run again by `eth_call` on the
     state of SetupDone's block, HlAdmin's first word overridden with `setupDone` false (`ethx.Client.CallOverride`).
     The override's place is proved first, by `setupDone()` reading false through it; checks 1–31 and 34 must pass.
     The stored record is held to its logs (`recordExplained`):
     - `lastSeq()` is the start plus one per `DecisionRecorded`;
     - `pending()` is at most the decisions recorded to pend and not ended;
     - every cancelled range ends where a `RangeCancelled` put it;
     - every standing flag has its `FlagSet` at its block, and a standing hold's start a `FlagSet`;
     - every hold of the history has a `HoldEnded`.
     `TestAnvilAuditRefusesAPlantedRecord` plants three pending, the last number five above the start and the launch
     line at 5 into HlAdmin's first word after an honest setup. The re-run refuses it `SetupIncomplete 34`, and the
     reads name lastSeq, pending and the line, while `setupDone()` still says true.
   - **Checks 32–33 retired, 34 new.** Nothing in Go expected 32–33 (the comments now say so).
   - **hlwatch's kind 9** compares all sixteen bindings (`mailboxPkgPathHash` always, now HlAdmin has it).
10. **Following F6.**
    - **The fees** (GNOT 20/40, BUBBLE 400/800) are read by the e2e from the realms, as before. Its stale "100
      BUBBLE fee" comment is fixed.
    - **The e2e's prices.** `eth.prices` are now of the plan's order: GNOT 0.0001 ETH and BUBBLE 0.000005 ETH (the
      routes' ratio). They were 1 ETH a token, at which a 20 GNOT fee was 20 ETH. A send's own fee is 0.002 ETH,
      about 2.5 times its header and delivery on the e2e's one-validator chain at ≈1 gwei (0.0008 ETH). At the plan's
      own rate, 0.000028 ETH a GNOT, the send would wait for its six hours (§5.5: 20 GNOT covers ≈0.7 gwei).
    - **The budget asserted.** The e2e now requires the sends' headers to be "booked (bought by its sends' fees)" and
      prints the budget's balance.
    - **hlmailbox's new JSON fields** need nothing: nothing here decodes it strictly.

### F7.2 Readings (where the brief left room)

- **The delivery's model**: 311,300 gas from a leaf of one, plus **101,000** for each further Dispatch — the plan's
  ≈94,000 is the client's verification of the leaf (E-M2b: 419,534 for a leaf of 4 against 138,909 of 1), and each
  Dispatch also carries ≈450 bytes of calldata (≈7,200 gas). The model never prices a delivery under its cost.
- **Who owns the budget**: the updater (it buys the headers); the relayer delivers what the updater's headers booked,
  and from anyone else's header only what pays for itself. Header ownership is the sender of the transaction behind
  the `HeaderRecorded` log (`eth.updater`), read once per header.
- **Pre-launch messages in the audit**, against F5.1 item 12's wording ("fails on a Dispatch sent by either token …
  or any Process"). Two of those can be a stranger's, which would hand anyone a veto of the audit, the very class
  E2-RT6 found. After step 9 anyone may call a token's `transferRemote` of 0, a Dispatch by the token. Anyone may have
  the mailbox deliver a message to a recipient of their own through an ISM of their own (Hyperlane's `recipientIsm`):
  a Process on our mailbox. So the audit FAILs, below the line, only on what no stranger can make:
  - a token's burn of a positive amount (something was minted before the launch);
  - a Process delivered to the deployment (a token or HlAdmin: a mint or a decision through a stand-in ISM);
  - a Dispatch before SetupDone not below the line, or whose message does not read.
  Everything else is a WARN with what it is. No machine counts any of it.
- **SetupDone's caller**, against F5.1 item 13's "a top-level call to `finishSetup()`". `finishSetup` is anyone's; a
  stranger calling it through a contract first would make the log not top-level for good. After its creation
  HlAdmin's code is the build's (held by the audit), whose only SetupDone follows finishSetup's checks. So the audit
  FAILs a SetupDone of the creation transaction and WARNs any other non-top-level caller. The re-run of finishSetup
  checks the state either way.
- **The re-run's block**: SetupDone's own block (the launch's state; checks 34 and 24 would fail on a later state
  once decisions and mints exist). It needs a provider that keeps that block's state: an archive node, or an audit
  within ≈128 blocks of the setup. Otherwise it is a FAIL "not read", as every reading the audit cannot make.
- **The launch line's block**: read at each scan's last block (it is set once, at finishSetup, and the scans start at
  its block), from the configured HlAdmin back through `predecessor()`.
- **A moving cut** is retried, never judged. A gno node cannot give a consistent read across calls, so the signer
  and the monitor read hlmailbox's count around the routes and take a verdict only when it stood still.

### F7.3 Findings for the other lanes

1. **gno.land's reads "at a height" are not versioned for realm state** (the coordinator; the Solana bridge's
   monitors and signers too). gno.land mounts the VM's object store as a `dbadapter` store (`gno.land/pkg/gnoland/app.go`:
   `baseKey`); only the main store is versioned. tm2's `handleQueryCustom` loads the requested version
   (`MultiImmutableCacheWrapWithVersion`), but `vm/qrender` reads the realms' objects from the unversioned store, so
   `RenderAt(H)` answers the latest objects. Every "cut at one height" is therefore a sequence of reads at whatever
   is latest, and the order of the reads decides which way a race errs. The first e2e (`e2e2-falsehalt.log`) halted
   two signers for two seconds. The routes were read before hlmailbox; a BUBBLE send landing in between (block 62,
   the cut's head 61) was counted in flight but missing from the lock: "the right side with what is in flight
   exceeds the lock by 2000000000". The terms that existed before E2-RT9 err toward slack (credits are read after
   the lock), never a false break. The Solana signers' and monitors' cut should be read for the same order. A realm
   view that renders every counter of the cut in one call would make it exact.
2. **F5.1 item 12's "fails on … any Process"** and **item 13's "top-level call"**: as read in F7.2, both would let a
   stranger fail the audit; F7 fails only what no stranger can make. For the coordinator to fold into the plan.
3. **Round 3 (BUILD.md "From F5")** drops `ed25519`, `ed25519CodeHash`, `mailboxPkgPathHash` and `route(i)` from kind
   9's equality. hlwatch then must print those four and stop calling a mismatch in them "spent" (`bindingLines`
   marks every one of the sixteen today, as HlAdmin spends them today).
4. **`hleth decision`** builds bodies offline and reads no successor. F5.1 item 6 asks it to print a kind 9's
   bindings too. `hlwatch check -kind 9 -target …` is the reading before the vote (bindings, build, creation,
   HlAdminExec); `hleth decision` was left as it is.

### F7.4 The runs

On the devbox (`root@<devbox>`), the tree synced to `/root/src/eth-f7` by BUILD.md's rsync, Go 1.27.1, Foundry
1.8.5. The real-contract tests read one build of this tree's contracts (`forge build --force --ast`, the default
profile, F5's) into `/root/src/eth-f7-build/out`, and Hyperlane 12.2.0's release compile into
`/root/src/eth-f7-build/hl` (`/root/src/eth-f7-logs/build.sh`):

```
cd /root/src/eth-f7/eth/bridge
gofmt -l agent cmd ethx gnoside tools repro    # nothing
go vet ./...                                   # clean
ETHX_CONTRACTS_OUT=/root/src/eth-f7-build/out HLETH_CONTRACTS_OUT=/root/src/eth-f7-build/out \
  HLETH_HYPERLANE_DIR=/root/src/eth-f7-build/hl go test -race -count=1 -v ./...
```

- **The gates** (47252677's tree; `/root/src/eth-f7-logs/final.gates.log`, `final.log`): gofmt and vet clean; **175
  tests pass (and 15 subtests), none fail, none skip**. By package: `agent` 101, `cmd/hleth` 21, `cmd/hlethbridge` 1,
  `cmd/hlwatch` 2, `ethx` 44, `gnoside` 6. The baseline at 161afc7f on the same box was 146 (`base.log`). Every
  real-contract test ran, the new anvil tests among them (`TestAnvilAuditHoldsEveryCreationInput`,
  `TestAnvilAuditRefusesAPlantedRecord`, `TestRHlgovBodiesThroughHlAdmin`).
- **Failing before.** Each fix was reverted alone on a copy of the finished tree and its test run, with `-race`. The
  runner is `/root/src/eth-f7-logs/mutants.py` and the log `/root/src/eth-f7-logs/mutants.log`.
  **37 of 37 caught** (the first run caught 35; see the two notes below the table):

  | Mutant (the fix reverted) | Test | How it fails |
  |---|---|---|
  | H1 creation inputs not compared | `TestAnvilAuditHoldsEveryCreationInput` | the planted ProxyAdmin: 0 failures |
  | H2 the record's leaves not held to the trusted hash | `TestClientInitFromTheRecord` | another header's leaves accepted |
  | H3 a token's burn below the line passes | `TestJudgeEarlyLogs` | 4 warnings, 4 failures (want 3, 5) |
  | H4 every Process before the launch fails | `TestJudgeEarlyLogs` | a stranger's own delivery fails the audit |
  | H5 `HlAdmin._startSequence` not expected | `TestAnvilSetupAndAudit` | the fresh setup's audit fails 1 |
  | H6 finishSetup not run again | `TestAnvilAuditRefusesAPlantedRecord` | no `SetupIncomplete 34` line |
  | H7 the record not held to its logs | `TestAnvilAuditRefusesAPlantedRecord` | no lastSeq line |
  | H8 the launch line not counted | `TestAnvilAuditRefusesAPlantedRecord` | no launchNonce line |
  | H9 a SetupDone of HlAdmin's creation accepted | `TestSetupDoneByItsCreationFails` | no failure |
  | R1 the scans from the mailbox's first block | `TestFreshScansFindFinishSetupsBlock` | the index starts at 1, not 42 |
  | R2 the record not seeded before deployBlock | `TestMonitorRecordStartsAtTheDeployBlock` | count 0 against the client's 1, `header:gap` |
  | L1 the burn rule draws no line | `TestMatchBurnsBelowTheLaunchLine`, `TestNoBurnBelowTheLaunchLine` | burn #0 counted (and signed) |
  | L2 an early index keeps the burn | `TestNoBurnBelowTheLaunchLine` | "the burn below the line stays" |
  | L3 no predecessor followed | `TestReadLaunchLine` | a successor's line 0 |
  | L4 the inbound relayer draws no line | `TestNoBurnBelowTheLaunchLine` | burn #0 queued |
  | S1 no send in flight for the signers | `TestSignerCountsUndeliveredSends` | the forged mint signs on |
  | S2 the signer's cut unguarded | `TestSignerCutAroundALandingSend` | `signer:halt` (the e2e's false halt) |
  | S3 the monitor's cut unguarded | `TestMonitorCutAroundALandingSend` | a verdict over the moving cut |
  | S4 a moving cut alarms at once | `TestSignerCutAroundALandingSend` | `signer:no-verdict` on the first round |
  | B1 the deliveries out of the cost | `TestUpdaterPricesEveryDelivery` | a header bought whose fees cover the header only |
  | B2 a leaf of any size (both guards off) | `TestRelayerReemitsASendFromABigLeaf` | 5 deliveries from a leaf of five |
  | B3 a stranger's header delivers at a loss | `TestRelayerHoldsAStrangersHeaderToTheSendsFee` | 1 delivery |
  | B4 the newest leaf, not the cheapest | `TestBatchOfFourAndTheCheapestLeaf` | proven under header 123, not 121's |
  | B5 a batch of eight (the old default) | `TestBatchOfFourAndTheCheapestLeaf` | a batch of 6 |
  | B6 the cap 200 gwei (the old default) | `TestEmergencyCapAndTheUpdatersFloat` | 200 gwei |
  | B7 no 0.2 ETH floor | `TestEmergencyCapAndTheUpdatersFloat` | 0.15 ETH over a small set: no alarm |
  | B8 no backstop | `TestRelayerBackstopBuysADecisionsHeader` | 0 updates |
  | B9 the send age alarm at two hours | `TestMonitorSendAgeIsSevenHours` | a 3-hour send alarmed |
  | B10 the ledger not in the state file | `TestBudgetSurvivesARestart` | balance 0 after a restart |
  | B11 a six-hour send cannot draw on the budget | `TestRelayerHoldsAStrangersHeaderToTheSendsFee` | the updater buys nothing |
  | W1 a build by its hash alone | `TestImplementationHeldToItsBuild` | no masked match |
  | W2 no creation transaction needed | `TestImplementationHeldToItsBuild` | no NOT SHOWN without `-creation-tx` |
  | W3 the immutables not held | `TestImplementationHeldToItsBuild`, `…AndBindings` | the stand-in mailbox passes |
  | W4 the bindings not compared | `TestSuccessorHeldToItsBuildAndBindings` | the client elsewhere not spent |
  | W5 the call not decoded | `TestDecodeCall`, `TestImplementationHeldToItsBuild` | no `initializeV2(…)` line |
  | W6 a successor's `_startSequence` not expected | `TestSuccessorHeldToItsBuildAndBindings` | no "= 0 ok" |
  | E1 F5's refusals parked like any revert | `TestExecutorAlarmsOnFoldF5sRefusals` | no alarm |

  - **B2.** Its first form turned off only the delivery-time check (`routeDeliveryCovered`), and survived: the
    outbox's candidate filter (`outbox.go`, the scan's count) stops a leaf of five first. The fix is both guards, the
    second for a leaf the scan has not counted, so B2 reverts both (`mutants-run1.log` keeps the first form).
  - **B7** survived the first run. At gnoland-1's set (≈1.45M gas) the 0.2 ETH floor and the ceiling at the cap
    coincide. The test gained a small set's header (486,600 gas) under 0.2 ETH (ba27c62c), and B7 then fails.
- **The test that found a bug**: the first e2e (e2e2) found the moving cut, F7.3 item 1; S2 is its mutant.
- **The end-to-end runs** (`BL=/root/.cache/eth-f7-e2e sh local/e2e.sh`).
  - **e2e1** (7c8000c6's tree, before F5 and F6): PASS in 377 s (`/root/src/eth-f7-logs/e2e1.log`).
  - **e2e2** (67b0d949's tree): FAIL. Signers 1 and 2 raised `signer:halt` for two seconds on the moving cut
    (F7.3 item 1; `/root/src/eth-f7-logs/e2e2-falsehalt.log`). Fixed by 9c71630b.
  - **e2e3** (9c71630b's tree): PASS in 374 s, `hlwatch status` GREEN. Log `/root/src/eth-f7-logs/e2e3.log`, state
    in `/root/src/eth-f7-logs/e2e3-state/`. Its alarms showed the moving cut alarming `signer:no-verdict` four times
    in two episodes, each cleared within three seconds. 47252677 makes that retry quiet.
  - **e2e4** (47252677's tree, the code of every commit since): **PASS in 375 s** (2026-10-14 19:39–19:46 UTC),
    `hlwatch status` GREEN. Log `/root/src/eth-f7-logs/e2e4.log`. The machines' logs, `deployment.json`, `bridge.json`,
    the setup's and the audit's logs and the alarms are kept in `/root/src/eth-f7-logs/e2e4-state/` (e2e3's in
    `e2e3-state/`), because the next run wipes `BL`.
    - **The alarms** were two lines, the monitor's `deliveries:paused` while the flag stood and its clear.
    - **The setup** sent 31 transactions; its second run sent nothing.
    - **The audit** printed 194 RPC calls and gave 261 "ok" lines, no WARN, no FAIL. It held the fifteen creations
      byte for byte, ran finishSetup again at block 37 (eth.deployBlock) and passed it, found the launch line 0 against
      no dispatch before SetupDone, and checked F5's three immutables.
    - **The routes** read F6's fees: GNOT 20 and minimum 40, BUBBLE 400 and minimum 800. So 40 GNOT and 2,000 BUBBLE
      went out and 800 BUBBLE came back, paid with the 400 fee first.
    - **The headers.** Three were bought by their sends' own fees: GNOT's, BUBBLE's and the send after the unpause's.
      Each fee was 0.002 ETH against 0.0008 ETH at ≈1.00 gwei (the header 486,600 gas by the model, the delivery
      311,300). The decisions' headers were bought outside the budget.
    - **Section times**: the build 3 s; the contracts 46 s (ours at factor 4032 and Hyperlane's release input); the
      keys, anvil and gnodev 7 s; the setup and its second run 32 s; the audit 1 s; the realms 5 s; the machines 4 s;
      gno.land → Ethereum 12 s; Ethereum → gno.land 8 s; the stranger 9 s; the identity 0 s; the decision 154 s (its
      150 s); the cancel 26 s; the flag 11 s; the unpauses 49 s; the send after 3 s; hlwatch 5 s.

# E-M3 — the Go machines' foundation and gno.land → Ethereum

Lane E-M3 of `BUILD.md`, branch `eth-m3` (from `bridge-eth`; `bridge-eth` merged in at `c08117e0`, E-M1's realms, and
again at `ac083be4`, E-M2a's and E-M4's merges and the bridge's signer fix), built 2026-10-14. This file is the record:
what is built, every reading where the plan left room, the coordinator's corrections and the code review folded in,
the findings for the other lanes, the questions, and the last devbox run with its commands.
The Solana bridge's Go side (`bridge/agent`, `bridge/cmd/*`) is the reference; its rules are carried over where
`PLAN.md` says "as", and changed only where Ethereum changes them. "§x" is `eth/bridge/PLAN.md`'s section; "Solana
§x" the Solana plan's.

## 1. What is built

### `ethx/` — the Ethereum client and the calldata (BUILD steps 1–2)

- `client.go` — one provider by name and URL (`Provider{Name, URL, LogRange}`; `String()` and every error redacted;
  `ProviderError` keeps the cause for `errors.As`); reads at a tag, a number or a block hash (`At`: a hash read is
  EIP-1898 with `requireCanonical`); block heads from the provider's own fields (number, hash, parent, time, base fee —
  never recomputed from a header whose fork this build may not know); `eth_call` alone and in JSON-RPC batches; code,
  storage, balance, nonce; logs (a removed log refused; `LogsRange` in the provider's own chunks, halved on a refusal
  as too wide); estimate, tip, raw send, receipt, transaction. `IsNoState`/`ErrNoState`: a read the provider keeps no
  state for (pruned) is no verdict, never a zero or a refusal.
- `redact.go` — the Solana rule (G16) with the websocket schemes: every http(s)/ws(s) URL cut to scheme://host.
- `pair.go` — the "two providers must agree" readings for the signers and monitors: the finalized block (the lower
  of the two finalized numbers, the same hash from both there), a block's canonical hash, `eth_call` and batches pinned
  by hash (a tag refused), code hashes, storage slots, the EIP-1967 slots, and logs over a range — each provider in its
  own chunks, compared byte for byte, each log's block hash the canonical one both give for its number; one provider's
  empty answer where the other has logs is a disagreement.
- `burns.go` — the shared burn scan (red team ER5): a burn toward gno.land counts only as the token's own — a Dispatch
  of our mailbox whose sender topic is one of our two tokens, destination gno.land's domain, recipient that token's
  route realm, its message checking out byte for byte (version, origin, sender, destination, recipient, a token
  message), matched once with that token's SentTransferRemote of the same recipient and amount before it in the same
  transaction, both lists read through a pair. A stranger's dispatch is ignored with the reason.
- `events.go` — the client's `HeaderRecorded` and `StageProgress`, HlAdmin's `DecisionRecorded`, decoded (the
  updater's resumption and last-update facts; the monitor's ER4 check of every stage against gno.land's headers).
- `sender.go` — one key file, one transaction in flight: gas = `eth_estimateGas` + 20%, and that sum must fit EIP-7825's
  16,777,216 (else `ErrGasCap`, the caller splits — never sent with less margin or over the cap; a node whose estimate
  finds no limit it allows, "gas required exceeds allowance", is `ErrGasCap` too, checked on anvil); the estimate is the
  simulation, so a call that would revert is refused with its reason before anything is sent; fee cap = 2 × base fee +
  tip, the tip the provider's suggestion held to [0.01 gwei, MaxTip/1.125³] (the three replacements before the stuck
  alarm fit under MaxTip, 2 gwei; an emergency call's replacements may rise to MaxTip·1.125⁴, so it can take the nonce
  of a route transaction re-priced to MaxTip); no send above `maxBaseFee` (5 gwei: `FeeTooHigh`, a park) unless the call
  carries its own cap (`Call.MaxBaseFee`: ER6's emergency cap); the nonce is the key's mined count (a dead primary's
  pending transaction at it is replaced); unmined after 3 minutes → replaced at its nonce with tip and fee cap both
  12.5% over the pending version's own values (geth's PriceBump is 10% on both; never lower when the base fee fell), the
  fee cap raised further to 2 × base fee + tip when higher, under the ceilings (tip ≤ MaxTip, fee cap ≤ 2 × the call's
  cap + MaxTip), or, when the ceilings leave no rise, the pending version broadcast again as it stands (a pool that
  dropped it takes it back); `OnStuck` after three windows unmined; a call whose base-fee cap is higher than the
  in-flight transaction's takes that transaction's nonce once the base fee has passed its fee cap (displacement: an
  emergency call is never held behind a route transaction a spike stranded; the displaced call's machine makes it
  again); `ErrStillPending` after `Wait` (4 min) with the record kept in a 0600 state file that the next Send or a
  restart resumes (a corrupt file is refused on every call, never forgotten); a nonce taken by another transaction is
  `ErrNonceTaken` on the second look; a mined revert read for its reason at the block before; every signed transaction
  to every provider. `KeyQuiet(d)`: the backup's rule (the shared key's nonce watched, own sends excepted). `Settle`
  (the transaction in flight resolved before a caller plans from the chain), `Tip`, `Balance`, `Address`, `InFlight`.
- `keyfile.go` — 64 hex digits, mode 0600 or narrower, never empty (`hl.CheckKeyFile`), never printed.
- `revert.go` — `Error(string)`, `Panic(uint256)`, custom errors by selector in every embedded ABI.
- `abi.go`, `abi/*.json`, `abi/gen.sh` — ours from the interfaces (forge inspect, solc 0.8.33) and from E-M2b's
  implementations (which win: they name the custom errors — `OverDayCap`, `NotMature`, `RetiredAdmin` …; compiled from
  E-M2b's head `72eba08f`, regenerate at the merge with `gen.sh`), HlAdmin's delegate `HlAdminExec` for its errors;
  Hyperlane 12.2.0's Mailbox, HypERC20, MerkleTreeHook, ProtocolFee and OpenZeppelin 4.9.3's ProxyAdmin and
  TransparentUpgradeableProxy compiled by solc 0.8.33+commit.64118f21 from the npm release's own compiler input.
- `contracts.go` — typed readers through one provider or a pair, at one state, batched: the client (`ReadClient`,
  `ReadEntry/ReadEntries`, `Entry.Proves(epoch)`, `ReadHistory`, `StagePower`), the gate (`ReadGate`,
  `MintLimit.Level` — the draining bucket), HlAdmin (`ReadAdmin`, `ReadDecisions`), the mailbox's delivered marks, a
  token's supply and router; addresses in 32 bytes both ways; `ParseAddress` (EIP-55 checked); an empty answer named
  ("no contract at …").
- `calldata.go` — `UpdateCalls`/`UpdateCallsFor` (the 16 header leaves; the commit's part set and round; the
  precommits for its BlockID, of its round, whose signatures verify here, the fewest crossing 2/3 — largest powers
  first, ties by slot — in slot order; the set in full; the next set exactly when the header announces), staged into
  calls of at most N signatures in slot order; `ProveHeaderCalls`; `HeaderCallOf` (decodes a call back: the header hash
  its leaves give, the round, the slots); `StageKey` (E-M2b's pure `stageKey`: the submitter, the header, the commit's
  part set and round, the client's epoch and the set the header must be signed by — equal to the real client's);
  `VerifyMetadata` = `abi.encode(bytes32, uint32, uint32, bytes32[], bytes)`, the aunts in tm2's `SimpleProof` order;
  `ProcessCall`; `ExecuteCall`.

### `gnoside/` — the gno.land side (step 3)

The JSON the realms render (hlmailbox's with E-M1's `ethContracts`, a route's with `routerAddress`, claims, sends with
their refund and write-off marks, every realm's decisions, r/hlgov's with its EIP-55 `recipient`, `former` and
`proxies`, its decisions), messages checked against their id, nonce and origin, the lines of a route's migrations (AR6,
G41), `Refundable` (G42), GovDAO's proposals; `dispatch.go` — `ScanBlocks` (tm2's `blockchain` listing, twenty metas a
request, finds the blocks with transactions; only those are read for results; hlmailbox's Dispatch events,
r/sys/validators' events, blocks whose leaves do not rebuild, and each message's leaf size read from the leaves' own
bytes) and `EventIndex` (each message's event heights, the dispatch's and every Reemit's, with their leaf sizes; an
unbuildable block marked only when it carries one of our messages); `run.go` — the MsgRun the gno client does not make:
`Runner.Run` through `gnokey maketx run` (simulated, then broadcast with the measured gas at the chain's price; a node's
own minimum paid once under the bound; refusals are `gno.CallError` so `gno.Deterministic` sorts them) and
`ReemitSource` (one `hlmailbox.Reemit(cross, n)` per nonce). The gno client of `bubblerumble/bridge/gno` is used as it
is.

### `agent/` — shared (step 4) and the machines (steps 5–7)

- Shared: `config.go` (the gno side, the providers, the contracts, the keys, the fee caps with ER6's emergency cap, the
  tokens' prices in ETH, an update's gas before one is measured, the staging bound, the leaf bound, the batch size, the
  heartbeat, `-proveOld`; checked: addresses, EIP-55, routes, signers, a price only of a configured route, the
  emergency cap not below `maxBaseFee`, domain and chain id beside the contracts), `alarm.go` and `status.go` (the
  Solana agent's: a webhook POST per alarm, hourly repeats, redacted; a snapshot published per tick, served on
  loopback), `state.go` (atomic 0600 state files), `attempt.go` (backoff a minute to an hour, parked after three
  deterministic failures, retried hourly), `floats.go` (runways at 7 days of the trailing rate; each machine watches
  its own key: `runway:relayer-eth`, `runway:updater-eth`).
- `outbox.go` — the gno → Ethereum backlog both machines read, the scan first and then the walk: `classify` (a route's
  token message from a realm of the token's line to its HypERC20; r/hlgov's decision to HlAdmin or a retired HlAdmin of
  its line; held: a decision to an address the line does not name (G31), a route message while the line is unread (G46);
  everything else not ours), HlAdmin's line of succession (`followAdmins`, 4 hops), `checkContracts` (the config held
  against the contracts hlmailbox compiles: a mismatch alarms `config:contracts` and delivers nothing), `walkOut` (from
  the first nonce not ended; Ethereum's delivered marks in one batch, a mark ending a message only once the finalized
  block shows it too — until then it is settling, never sent again and waiting for nothing; each message's usable events
  — a leaf over the bound, by the scan's sizes, proves nothing — and the newest whose next header the client accepted at
  its current epoch), `scanStart`/`scanEvents` (an ended message's events not kept), `markPast`, `finalPast`,
  `planWaits` (the waiting rule: the waiting by the block of their newest usable event, the ones below the trusted
  height, the batch), `sendRefunded` (both machines end a refunded send), `decisionGate`.
- `due.go` — when a header is bought (§5.5, ER6): `headerDue` over the messages that header serves is true when a
  decision is among them (always), when their fees at the config's prices cover an update (the last update's gas
  from its receipt, else 4.2M, at the live base fee plus tip), when the heartbeat is within 2 minutes, or when the
  config prices nothing; `lastUpdate` finds the trusted header's `HeaderRecorded` log (its block time and gas; the
  client's initialization, record 1, is no update's cost), cached per update.
- `updater.go` — §6.1 on Ethereum: the announcing header at once (no spacing, the emergency cap) with the set it
  announces, planned before and apart from the backlog's reads; the header after the block the waiting dispatches share
  when a header is due for them (a decision's under the emergency cap); split, a due batch given `BatchWait` (90 s,
  counted from when the batch became due) for the relayer's re-emit, then each block's group alone — the blocks holding
  a decision first, then the oldest due (at once when no batch can gather them); the heartbeat (6 h) at the head; never
  two updates within 2 minutes but announcing; `planUpdate` finds the announcing header when a change lies between;
  staged updates (the configured bound, or halved when a call's estimate does not fit the cap) resumed from the chain
  (the key's `StageProgress` logs under the stage key of the client's current epoch and expected set, each call's slots
  read back, a staging call still pending settled first); an update whose calls all went but which the client, read at
  the last call's own block, does not trust is an error and `updater:unfinished`; a failed update backs off (the
  announcing header's backoff kept apart) (the relayer's attempt rule: a refusal the simulation answered waits a minute;
  a mined revert or an unfinished update a minute to an hour, parked after three and tried hourly, `updater:parked`); a
  past decision copy ends once the finalized block's counter covers it; alarms `client:lag` (7 h without an update),
  `client:announce-lag` (30 min), `client:set-bound` (40 validators), `updater:plan`, `updater:refused`,
  `updater:staged`.
- `relayer.go`, `relayer_out.go` — the relayer: primary and backup 60 s apart sharing the key (the backup also waits
  for `KeyQuiet`); at most 8 Ethereum sends a tick; gno → Ethereum: decisions first — one copy per number, only the
  next HlAdmin expects (its counter at latest), under the emergency cap; a number past it ends "recorded" when a copy
  went through our mailbox, "taken" (alarmed once) only when HlAdmin's counter at the finalized block covers it and no
  copy is delivered there, on two passes (a provider without that state: no verdict); a copy to a retired HlAdmin ends
  "retired". Routes: only while the gate takes them (no flag, no quarantine); the token's ISM must be the gate; the day
  cap's parks (OverDayCap retried when the bucket has room, oldest first, its amount reserved; OverMax waits for a
  raise); a refunded send never goes (AQ2, G42, G53); the routers checked against the lines (K34). A delivery builds the
  proof under the accepted header (the block's leaves re-encoded and checked against the header's LastResultsHash, the
  SimpleProof, `VerifyMetadata`) and sends `Mailbox.process`; "Mailbox: already delivered" is success; an unbuildable
  block or a leaf over the bound marks that event unusable. The waiting: split (blocks, or below the trusted height),
  one MsgRun re-emits a batch (oldest first, at most 8, 30 s apart; a block holding a batch already is left to its
  own header) when the batch is due its header; with `-proveOld` the header after a block of messages below the
  trusted height is proven (`proveHeader`) when it is due for them (its signature bound halved when a call does not
  fit the cap). Route sends buy no header and no batch while the gate refuses them (a flag, the quarantine). A
  delivery is settling until the finalized block shows its mark (made again if a reorg drops it). A job registry
  (`RegisterRelayerJob`) lets the inbound lane add its Process and Claim loops beside these files, its state kept under
  its name in the relayer's state file.
- `executor.go` — HlAdmin's matured decisions (`executeWhen`: a cancelled one at any time; a hold defers the 7-day
  group until it ends; a lone flag defers it but kind 5; kind 5 under a hold on the later of its 7th day and day 21 of
  the earliest hold overlapping its pending life; the contract's `NotMature`/`Deferred` are waits) under the emergency
  cap, a low-water mark so only numbers possibly pending are read; gno.land's queued decisions (`Execute(id)` by block
  time; a refund is GovDAO's own apply, AT2) and lapsed proposals (`ExecuteOrRejectProposal`, after the yield only).
  HlAdmin's counter and decisions are read at one block, pinned by its hash; the low-water mark passes only numbers
  with nothing pending at the finalized block.

### `cmd/hlethbridge` (step 8)

`updater` and `relayer` (flags: `-config`, `-state`, `-sender-state`, `-key`, `-listen` loopback only, `-role`,
`-gno-gas`, `-every`); the providers' chain ids checked at start (the first must answer; another that does not is left
out of the broadcasts); a stuck nonce alarmed `eth:stuck:<n>`. `signer.go` and `monitor.go` are stubs holding the
commands' places: the Ethereum → gno.land lane replaces those two files and nothing else (the commands register
themselves in `commands`). `main_test.go`: every command registered, the status address loopback only.

## 2. Readings (where the plan left room)

1. go-ethereum is pinned at v1.17.8 (2026-10-08, `go 1.25`), the current release.
2. The relayer and the updater read Ethereum through one provider (the first) and send through all; only the signers
   and the monitors read through a pair (the Solana relayer's reading: it decides nothing a lying provider could cash).
3. A pair's finalized block is the lower of its two providers' when one is ahead; a disagreement is two hashes at one
   number.
4. The verify metadata's aunts are tm2's order (the leaf's sibling first) — E-M2b's `TmMerkle.rootFromProof` reads them
   so ("the m-th aunt is the sibling at depth depth−1−m"), and the real-contract test proves a real leaf with them.
5. One transaction in flight per key, no pipelining: a tick's deliveries go one receipt after another (at most 8).
6. A replacement the bounds no longer let rise 10% is not sent; the transaction waits; `OnStuck` alarms from the third.
7. **Which header an update targets** (§5.7, §6.1 read together): a message is proven from the newest of its usable
   events (dispatch or Reemit) whose next header the client accepted at its current epoch. The waiting rule: one
   update serves the waiting messages when their newest usable events share one block above the trusted height; when
   they do not (more than one block, or one with no usable event above the trusted height, which no update can serve),
   the relayer re-emits them in one MsgRun — "re-emit them all" read as every waiting message up to the batch bound
   (reading 13), the ones a split leaves in older blocks included, so they share the batch's block — and the updater
   targets the header after it. A block already holding a batch is never re-emitted again (re-emitting it gathers
   nothing, and past the bound the same oldest batch would be re-emitted every 30 s); when the ones a batch could
   gather already share one block, nothing is re-emitted. The updater waits `BatchWait` (90 s, from when the batch
   became due) for that batch, then serves the blocks one by one, the oldest whose own messages are due first (at
   once when no batch can gather them); an older block left behind falls below the trusted height and is re-emitted.
8. **The event index**: the updater and the relayer each scan gno.land's blocks for Dispatch events (the re-emits are
   anybody's, so neither trusts the other's state); a fresh index starts at the oldest undelivered dispatch, 2,000
   blocks a tick at most.
9. **When a header is due** (ER6, §5.5 "the fees of the dispatches it serves"): judged over the messages that header
   serves — the shared block's, a block's group, a batch the relayer would re-emit, a block below the trusted height
   `-proveOld` would prove: always when a decision is among them; for route sends when their fees cover the update, or
   when the heartbeat is within 2 minutes (the relayer's batch lands first); the fee of a waiting send is its route's
   current `fee` (the realm records no per-send fee); with no `eth.prices` the rule is off (a local run: every send
   buys its header). The update's cost is the last update's gas (its receipt, found by its `HeaderRecorded` log; the
   initialization's record is no update) — staged updates count their finishing call only — at the live base fee
   plus the tip a call would pay (read once a step).
10. **The emergency cap** (ER6) covers a decision's delivery, the header a waiting decision needs, the announcing
    header (a rotation the client has not followed is the leak window §1 describes), HlAdmin's executes and a header
    proof for a decision; route deliveries and their headers stay under `maxBaseFee`. Default 200 gwei (the 30-day
    maximum was 24.9).
11. **The decision gate reads HlAdmin's counter at latest** (Solana read finalized): Ethereum's finalized block is ~13
    minutes behind, and a premature copy only reverts in the simulation (`OutOfOrder`), spending nothing; the "taken"
    verdict alone reads finalized, the counter and the delivered marks pinned to one block hash. A past copy is never
    sent (HlAdmin would ignore it and emit `DecisionIgnored`, spending gas) and buys no header (`markPast`).
12. **Pause reading**: route deliveries are not attempted while any flag stands on the gate or the client's quarantine
    stands by the latest block's time (the two facts the gate's verify reads); decisions go regardless (they skip the
    gate). "Deliveries paused" is the same reading the monitor's `sends` mirror will take.
13. **The batch size** is 8 nonces (`eth.reemitBatch`): E-M2b measured that the client reads every Dispatch of the
    mailbox in a leaf, ≈91k gas each, so a batch of N costs each of its N deliveries (N−1)·91k against the N−1 header
    updates (≈0.9–1.5M each at gnoland-1's sets) it saves. At 8 a delivery pays ≈640k for the others, under one
    update; at 16 it would pay ≈1.37M, nearly an update a delivery. A large backlog's cheapest bound is ≈√(U/91k) ≈ 4,
    but the fee rule judges a batch by its own fees (reading 9), and eight sends' fees cover an update at twice the
    base fee four's do (at the fold's proposed 10 GNOT a send, BUILD.md, one send's fee covers a 1.5M-gas update up
    to ≈0.19 gwei, eight's to ≈1.5 gwei).
14. **The leaf bound** the relayer proves from is 8,192 bytes (the Solana ISM's), configurable; E-M2b's client has no
    leaf bound (it bounds the aunts at 100).
15. **A staged update's resumption** reads the key's `StageProgress` logs of the last day under the stage key of the
    client's current epoch and expected set (E-M2b's `stageKey`; a stage another epoch began is orphaned, and skipping
    its slots would leave the update short of 2/3 for good) and the calldata of the transactions that emitted them;
    E-M2b's client skips a slot a stage verified already, so a missed resumption costs calldata, never a double count.
    Every staging call carries the next set (E-M2b reads it only in the call that crosses 2/3): only the last call
    crosses — the chosen precommits are the fewest by power, so no proper subset of them crosses — and carrying it in
    every call keeps each call right whichever crosses; the cost is the set's calldata in the staging calls of an
    announcing header, which are rare.
16. **The config is held against hlmailbox's compiled contracts** (E-M1's `ethContracts`): a mismatch delivers nothing.
17. **Kind 8's body** (E-M1: an optional call after the code hash) is delivered as r/hlgov dispatched it; nothing here
    parses a decision's parameters but a cancel's ranges (none needed on Ethereum: HlAdmin closes its own).
18. **Route sends buy no header and no batch while the gate refuses them** (a flag on it, or the client's quarantine
    by the latest block's time — the gate's own `quarantineEnd() > block.timestamp`): they could not be delivered
    under it (the Solana relayer staged nothing while paused); the decisions, which skip the gate, still buy theirs.
    The gate is read once a step.
19. **A refunded send ends before the waiting rule sees it**, in both machines (the relayer alarms it, the updater
    logs it): it is never delivered, so it buys nothing.
20. **A delivered mark ends a message only at the finalized block** (the Solana relayer read its processed mark at
    finalized): delivered at latest alone, a message is settling — not sent again, waiting for no header — and a
    delivery a reorg drops before finality is made again. Ethereum's finality (~13 minutes) delays only the
    bookkeeping.
21. **Usability is the scan's shared reading**: the scan records the size of the leaf each message would be proven
    from (the first leaf whose own Dispatch bytes carry it, as the proof is built), so the updater skips an event the
    relayer cannot prove from (a leaf over the bound) and buys its re-emit's header; the scan runs before the walk.
    The relayer's own unusable marks remain the fallback for a block the scan has not read.
22. **The announcing header comes before the backlog** (the Solana updater planned it before its dispatch check): a
    failing backlog read never holds a validator change or its `client:announce-lag` alarm.
23. **A failed update backs off** with the relayer's attempt rule: a refusal the simulation answered spent nothing
    (another submitter's update may have landed first) and waits a minute; a mined revert or an update whose calls all
    went without the client trusting the header spent gas, and backs off a minute to an hour, parked after three.
24. **The sender's displacement**: a call whose base-fee cap is higher takes the nonce of the in-flight transaction
    once the base fee has passed that transaction's fee cap; the displaced call is made again by its machine, which
    reads its action back from the chains. A replacement's fees rise 12.5% over the displaced version's own.
25. **HlAdmin's low-water mark moves by the finalized block** (the Solana executor kept no mark and re-read 500
    numbers a tick): the mark passes a number only when nothing is pending there at the finalized block; the
    decisions themselves are read at latest, pinned to one block with the counter.
26. **In the split path a block holding a decision is served first** (ER6), then the oldest due block: a route
    block's header goes under `maxBaseFee` and parks in a spike, and serving it first held the decision's header for
    the whole spike. An older block left behind falls below the trusted height and is re-emitted.
27. **An emergency call's tip bound** is MaxTip raised four times by 12.5% (≈3.2 gwei at MaxTip 2): geth's pool takes
    a replacement only when the tip rises too, so a route transaction re-priced to MaxTip could not otherwise be
    displaced. A call under `maxBaseFee` stays under MaxTip.

## 3. The coordinator's corrections, folded in

- EIP-7825 (the 16,777,216 cap): the estimate plus 20% must fit; a call that does not is `ErrGasCap`, and the updater
  halves its per-call signature bound and stages. Replacement: both tip and fee cap +12.5% over the pending values,
  never lower; `OnStuck` at three replacements. Anvil runs mainnet's fork (`--hardfork osaka --enable-tx-gas-limit`)
  in every test; no gas figure is hard-coded but the 4.2M default an update's measured gas replaces.
- ER5: `ethx.ScanBurns`/`MatchBurns` (above). ER6: the due rule and the emergency cap (above). ER4:
  `DecodeStageProgress` for the monitor (the check is the monitor's). ER9: the watch's re-flagging is the monitor's;
  nothing in this lane flags. Public RPC facts: per-provider `logRange`, halving on refusals, an empty single answer
  never a verdict (the pair compares), no-state reads as no verdict.
- E-M1's realms (merged): the new JSON fields read; the config checked against `ethContracts`.
- E-M2b's M2-NOTES (read 2026-10-14 at 0f997981, again at 72eba08f): the stage key (resumption filtered by it), the
  next set read only by the finishing call (carried by every staging call: reading 15), the initialization's record
  (no update's cost), the ≈91k gas each Dispatch in a leaf costs a delivery (the batch bound, reading 13), the
  quarantine's strict `quarantineEnd() > block.timestamp` (the gate reading, reading 18); at 72eba08f the client's
  constructor takes the `Ed25519Verifier` and HlAdmin's Config the verifier, its code hash, the local domain and the
  launch limits (the tests deploy them; the ABIs regenerated), a signature is 64 bytes by construction (the
  precommit choice carries only signatures that verify, so only 64-byte ones), and the deployment is 16 contracts
  (E-M3b's audit, not this lane's).

### The code review (a reviewer over the machines, nine findings, all fixed in 5e16cb1e and cb66cd1f)

1. High — the sender: an emergency call waited behind a route transaction a spike stranded; a first tip at MaxTip left
   no replacement; an evicted version was never sent again. Fixed: displacement (reading 24), the first tip under
   MaxTip/1.125³, re-broadcast when no rise, `OnStuck` counted by windows.
2. High — the updater never saw the relayer's unusable-event marks, so an oversized leaf's message looped through
   re-emits without its re-emit's header being bought. Fixed: reading 21.
3. High — the executor's low-water mark could pass a recorded decision after one stale read. Fixed: reading 25.
4. Medium — the gas-cap halving never fired: a node answers "gas required exceeds allowance" rather than an estimate
   over the cap. Fixed in the sender; `-proveOld` halves its own bound too.
5. Medium — a corrupt sender state file was honoured once, then forgotten. Fixed.
6. Medium — state that never shrinks: the updater never ended past copies (its cursor stuck), and ended messages'
   events stayed in the index. Fixed: `finalPast`, the scan's forget, unbuildable marks only for our blocks.
7. Medium — the announcing header waited behind the backlog's reads. Fixed: reading 22.
8. Low — a failed update was re-sent every step. Fixed: reading 23.
9. Low — one read at latest ended a message delivered for good. Fixed: reading 20.

### The second review (over the fixes, six findings, all fixed in ed86daab, each with a test that failed before)

1. High — split waiting in a spike: the oldest due block (sends) was served first under `maxBaseFee`, parked, and held
   a newer block's decision header for the whole spike. Fixed: reading 26.
2. Medium — an emergency call could not take the nonce of a route transaction re-priced to MaxTip. Fixed: reading 27.
3. Low-medium — "unfinished" was judged at latest, which a lagging node may answer before the update lands; a park
   earned by other updates held the announcing header. Fixed: the client read at the last call's block; the
   announcing header's backoff kept apart.
4. Low — the updater read the head before the mailbox, so a message dispatched between them lay in a block its scan
   had not read (its leaf size unknown). Fixed: the mailbox first, then the head the scan and the walk share.
5. Low — a park another party delivered kept reserving its amount while it settled. Fixed: settling parks reserve
   nothing.
6. Low — a staged update's resumption could re-send the slots of a staging call still pending. Fixed: `Settle` first.

## 4. Findings for the other lanes

1. **E-M2a/E-M2b — resolved: GnoLightClient's runtime code was 26,885 bytes with E-M2a's real Ed25519 inlined**
   (E-M2b's tree at 0f997981 with E-M2a's library at 249c2c07; 26,732 with c08d92f0), above EIP-170's 24,576. E-M2b's
   head (72eba08f) calls E-M2a's `Ed25519Verifier` (7,007 bytes) instead: the client is 20,667 bytes, HlAdmin 17,708,
   HlAdminExec 21,784 (2,792 to spare: the one to watch), PausableGate 13,193. Nothing the machines send or read
   changed; the real-contract tests here deploy the verifier first and run anvil with EIP-170 on.
2. **Gas measured with the real verifier** (anvil, Osaka; E-M2b's head, the verifier a contract): an update carrying 3
   signatures 0.90–1.06M gas (the 1.06M one carries the next set), a staged update's calls 661k (2) and 426k (1);
   E-M2a measures 217–223k a verification. At gnoland-1's 6 × 60 set an update carries 5 signatures: E-M2b measures
   1.45M (20 validators 3.65M, 40 6.82M, 60 10.24M, one transaction up to about 100) — under ER6's 4.2M estimate at
   gnoland-1's set, which `eth.updateGas` overrides and the first update's receipt replaces.
3. E-M2b's `stageKey` is computed here (`ethx.StageKey`, checked against the real client's pure function and the
   tally a staging call leaves) to resume only the live stage; `IHlAdmin.route(i)` is not used (the routes come from
   the config, checked against the tokens' enrolled routers).
4. E-M2b's measured ≈91k gas per Dispatch a leaf carries set the batch bound (reading 13): the batch re-emit is the
   one place the machines choose how many Dispatches share a leaf.

## 5. Questions

1. ER6's fee rule takes a route's current fee as each waiting send's: a fee lowered after the sends under-counts them
   (their header waits for the heartbeat). Accept, or should hlmailbox record each message's fee?
2. The backup relayer replaces a dead primary's stuck transaction at the shared nonce (its fees +12.5% until the pool
   takes it). Accept, or should the backup only ever use the next nonce after the stuck one?
3. Should the relayer close the pending decisions a retired HlAdmin still holds (HlAdmin.execute spends them,
   refunding storage), or leave them to anyone?
4. PLAN.md §5.7 says the relayer re-emits "them all" in one transaction; with E-M2b's ≈91k gas per Dispatch in a leaf
   that is a loss past ≈11 messages, so the batch is bounded at 8 (reading 13). Amend §5.7's text to name a bound?

## 6. Tests (the last devbox run)

`ssh root@<devbox>`, `cd /root/src/eth-m3/eth/bridge` (synced by rsync as BUILD.md says), Go 1.27.1, Foundry
1.8.5 (forge, anvil); the real-contract tests read `ETHX_CONTRACTS_OUT=/root/src/eth-m3-it/out`, a Foundry build of
E-M2b's `contracts/src` at its head 72eba08f (E-M2a's library and `Ed25519Verifier` merged in, the client calling
the verifier; anvil enforces EIP-170) and two import-only files for OpenZeppelin's proxy and Hyperlane's contracts;
the embedded ABIs are that build's (`forge inspect`):

```
gofmt -l .                       # nothing
go vet ./...                     # clean
for p in ./ethx ./gnoside ./agent ./cmd/hlethbridge; do ETHX_CONTRACTS_OUT=/root/src/eth-m3-it/out go test -race -count=1 -v $p; done
```

- `ethx`: ok — 36 tests: the calldata of four gnoland-1 headers byte for byte against Foundry's `cast`, the verify
  metadata and `process` calldata of three real results leaves against `cast` and a hand layout, selectors and topics
  against `cast sig`/`keccak`; the precommit choice; staging; the readers; the pair's rules and log chunking; the burns
  (ER5); the events; the sender's margin, cap, bounds, first tip, replacements, re-broadcast, stuck alarm, displacement,
  the allowance refusal, resume, a corrupt state file, mined revert, taken nonce, foreign pending, emergency cap and its
  displacement of a transaction re-priced to MaxTip, `KeyQuiet`, redaction; the key files; the sender on anvil (a probe
  contract: a write, two refusals, a call over the gas cap, a replacement mined); and the calldata on the real contracts
  (`TestCalldataOnTheRealContracts`: updates, the announcing header, a staged update whose stage key equals the real
  client's, a header proof, a real leaf's proof).
- `gnoside`: ok — 5 tests: the renders (E-M1's fields among them), a line of migrations, the scan and the index (leaf
  sizes, unbuildable marks only for our blocks), tm2's `blockchain` listing, the MsgRun runner (a fake gnokey).
- `agent`: ok — 40 tests over a synthetic gno.land chain (signed headers, results, renders, MsgRuns) and a model of the
  contracts (an update records the fake chain's header under the real stage key; a process checks the proof under an
  accepted entry, the gate's flags, quarantine and limits, HlAdmin's counter; an execute the maturity; a finalized view
  where the tests need one): the waiting rule (groups, full blocks, the bound) and the due rule, the executor's clock,
  the decision gate, the day cap, the plan toward a target; the relayer's deliveries, batch and its bound, fee rule,
  decisions (one copy, recorded, taken, no verdict before final), pauses, parks, refunds (a settling park reserves
  nothing), backup, fee spike, executes and the low-water mark at finality, gno executes, the admin line, the contract
  check, the end at finality and a delivery a reorg drops; the updater's shared block, fee rule and heartbeat,
  announcing header (before a broken backlog), batch wait (from when the batch is due), each block served alone, staging
  and resumption (an orphaned stage, a pending staging call settled first), lag, paused routes, refunded sends, an
  oversized leaf, past copies and a clean index, the backoff, a decision's block first in a spike, the update read back
  at its own block; the config; the floats; and `TestMachinesOverTheRealContracts` — the updater and the relayer over
  the real Mailbox, HypERC20, hooks, GnoLightClient, PausableGate and HlAdmin on anvil (finality two blocks behind): a
  send minted and ended once final, a stranger's dispatch walked past, two decisions batched into one update and
  delivered once each, the cancelled one closed.
- `cmd/hlethbridge`: ok — 1 test (every command registered, the status address loopback only).

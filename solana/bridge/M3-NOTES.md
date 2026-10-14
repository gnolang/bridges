# M3 notes — the Go side, first half (M3a): what was chosen where the plan left room

M3 of BUILD.md is the Go side (PLAN.md §5.6–§5.9). This half needs only the gno.land realms of M1: the signers,
the relayers' gno.land side, the monitors, and the gno.land half of `hlwatch`. Everything that needs the Solana
programs of M2 — the light client, the pausable ISM, the admin program — is M3b and is left as a named stub (the
list is at the end). Where PLAN.md is specific the code follows it; where it is silent this file says what the
code does and why. It never changes the plan. Run the Go tests with `go test ./...` in `bridge/`; the local chain
end to end with `bridge/local/e2e.sh`.

## What is where

- `cmd/hlbridge` — one binary for the operator's machines: `signer`, `relayer`, `monitor`, and `providers` (the
  §5.6 provider-pair check across several machines' configs). One binary because the three roles share the
  deployment config, the chain readers and the identity rule, and the relayers run on the monitor machines
  (§5.5); a `cmd/hlsigner` would have duplicated the config and the readers for no isolation gain (the signer's
  isolation is its droplet and its key file, not its binary).
- `cmd/hlwatch` — the guardians' tool, its own binary: every outsider compiles their own and its build hash goes in
  the launch notice (§5.8), so it must not drag the operator's roles along.
- `agent/config.go` — the deployment's facts, one file for every machine: the realms, the Solana programs by
  route, the signers (URL and ed25519 key), the two Solana providers, the keys by name. `ProviderPairs` is the
  check across configs.
- `agent/identity.go` — the identity, the verdict, the ordered cut and the third-source rule, pure.
- `agent/chain.go` — what the realms render for the machines and the Solana reads (the dispatch index, the
  supply from two providers, the processed-message record).
- `agent/signer.go`, `agent/relayer.go` (+ `decisions.go`, `rule.go`), `agent/monitor.go` (+ `floats.go`),
  `agent/alarm.go` (the webhook), `agent/views.go` (hlwatch's exported reads).
- `gno/gno.go` — the node client: `status`, `block`, height-pinned `vm/qrender`, `bank/balances`, and gnokey
  calls with the simulation first (`-simulate test`, gnokey's own default, passed explicitly), the result's
  events parsed, and `Deterministic(err)`: whether a failure would repeat (the chain refused it) or was lost on
  the way (a dead node, a timeout, a sequence race).

## Readings and gaps, by section

### §5.6 the signers

- **Two providers agree on each burn**, not only on the supply: a dispatch is signed once provider 2 shows the
  same transaction, finalized, with the same message bytes in the mailbox's record (`solConfirm`). Until then
  the scan cursor stops at it.
- **The refusal rule is evaluated over the same ordered cut as the monitor's** (§5.9): the Solana snapshot
  first (both providers' supplies equal; the slot the lower of the two), then the own node's head, which must be
  after the snapshot, the routes' counters pinned at that height, then the dispatch index refreshed and checked
  — a burn credited at that height whose dispatch slot is above the snapshot's breaks the order. Without the
  order there is **no verdict and no signature that round**; a signer that cannot verify does not sign. The
  plan's "in-flight" tolerance is on the short side only; the rule is the tight one (`supply > lock − owed −
  voided + lost`), which the cut makes exact.
- **A halt clears itself when the identity holds again** (a void as forged); **a nonce conflict never does** —
  it stops signing, alerts, keeps serving what it signed, and waits for a human (a restart with the store
  intact resumes; the conflicting message is still refused).
- **HTTPS** is the `-tls-cert/-tls-key` flags on the server; the client refuses a plain-http signer URL unless
  it is a loopback address or the config says `allowHTTP` (the local run).
- **A rotated-in signer backfills every undelivered burn** by construction: a fresh store has no cursor, so it
  scans the routes' whole dispatch history and signs it all (bounded by that history; a long one takes a while).
- **The public checkpoint store (S3)** is not built: the store directory is plain JSON files, one per nonce,
  which any sync tool publishes. A gap for M3b or ops.
- **The local multisig stand-in**: until M2's light client exists, the local run delivers gno → Solana under
  Hyperlane's multisig ISM, which needs the signers' secp256k1 checkpoints. The signer still makes them when
  the config carries `localMultisig` (never on a deployment); M3b deletes `signGno` and `deliverUnderMultisig`.
- **A persistently unreadable Solana transaction pins the scan cursor** (the dispatch index must not skip a
  burn, so a fetch error is retried rather than passed over); the status endpoint shows the error. The plan's
  "no single transaction's error pins the cursor" is read as: no *dispatch's* delivery error pins it — a
  delivery failure parks that message alone and the loop moves on (true of the relayer).

### §5.7 the relayers

- **60 s apart** is a clock per action: the moment an action first became possible (a message with its
  signatures, a route with a claim the meter can pay, a decision past its delay) is recorded, the primary acts
  at once, the backup only once 60 s have passed with the action still possible. The backup's clock restarts
  when someone else acts (the message is delivered, the route's `paidTotal` moves, the decision is gone).
- **Exactly the signatures the rule needs**: the lowest-indexed valid ones up to the threshold, or the
  lowest-indexed valid one of each group, each checked against the key hlmailbox lists for that signer before
  it is sent (M1-NOTES §5.1: the verifier checks every signature present). The set and rule are read from
  hlmailbox at every pass; the config only says where each key's signer serves.
- **`Claim(20)` continuously** means: while a route's render shows an open claim and the bridge meter holds at
  least the fee (a claim's first installment, whole), call it; a call that paid nothing backs off a minute,
  doubling to an hour (the meters refill on their own clock). The count paid comes from the `Claimed` event.
- **`Execute(id)`** by the chain's own clock: a queued decision whose `due` is at or below the latest block's
  time, on every realm with a ledger (the mailbox, the routes, the fee realm, r/hlgov).
- **`Reemit`** is a pure decision (`needsReemit`) over what the light client says — the current set's first
  height and a block's results-leaf size — through a `clientView`; the M3b stub knows nothing, so nothing is
  re-emitted until the client exists. A Reemit is sent once per reason (once per set change, once for a big
  leaf); the height of the last one is kept in the state file.
- **Parking**: three deterministic failures (gnokey's simulation or the chain refusing: a panic, a paused
  function) park an action and alarm; parked actions retry hourly; before that the backoff doubles from a
  minute; a transient failure (a dead node, a timeout, a sequence race) retries in a minute and counts for
  nothing.
- **Gas**: 45M wanted at the chain's minimum price (0.001 ugnot/gas) by default, `-gno-gas` to change; the fee
  is price × gas, rounded up. The local run passes 60M (gnodev's VM measures higher than §5.5's figures).
- **The gno → Solana delivery under the light client** is the named stub `deliverUnderLightClient` (staged
  header, commit and `SimpleProof`, §6.2–§6.3); decisions to the admin program one copy per sequence number and
  "a number another decision has already taken" are M3b with it (they need the admin program's state).
- **The signer client**: a 1 MB body limit (`io.LimitReader`), HTTPS or loopback, signatures cached in the
  state file by message id and signer key once verified.

### §5.9 the monitors

- **The cut** is the plan's, with one reading: until the light client exists there is no trusted height T, so
  the gno side is read at the own node's head after the snapshot (the plan's "the chain's head when the client
  is past it"); M3b adds T and "a height at or above T", and the mismatch flag when the client is past the
  chain. Every gno render of a poll is pinned at that one height.
- **In flight**: the right side may fall short by the sends Solana has not processed (the mailbox's
  processed-message record, finalized) and the burns dispatched at or before the snapshot's slot that gno.land
  had not credited at the height; nothing in flight can make it exceed the lock, so a break is a break. A short
  side beyond that alarms only once it has stood three polls (a mint lands between a snapshot and the next).
- **A break sets `credits` and `payouts`** through the watch key, once, and sets either again if it is cleared
  while the break stands (M1-NOTES: the realm keys flags by seat, so a re-assert is a replace); the monitor
  **never clears them** — an unpause decision does, or the holder by hand (`hlwatch unflag`), since clearing
  on the first poll that happens to hold would make the pause flutter. `sends` is never set for a break.
- **The diverted credit** is checked once per credited dispatch: the claim at that height must name the
  recipient and amount of the Solana dispatch record (a sweep entry by amount); a claim credited by vote is
  skipped. A dispatch the own node has marked delivered but whose claim is not rendered yet is looked at again
  next poll.
- **The third-source rule** compares block hashes at the poll's height: own node, the first other source
  (rpc.gno.land in a deployment), the second other source as the third. Own ≠ public and the third with the
  public → `credits` on every route, never `payouts`, and while it stands the identity rules alarm instead of
  flagging; the third with own → alarm (the public endpoint is wrong); no third, or three answers → alarm. A
  source that has no block at the height yet, or does not answer, is a lag: nothing.
- **Runways**: each float's spend over the trailing day (decreases only; a top-up counts for nothing), never
  below the planned launch-day rate (§5.5: 50 transfers a day; the ATA payers 25 first-time recipients; the
  updater 0.3 SOL over five months), 7 days the alarm line; `runway` in the config overrides a rate. The
  relayer's gno key is read by its address (`gno.address`), the ATA payers from the token programs, the
  relayers' SOL key from the payer file, the updater's by its pubkey (the updater itself is M3b).
- **Alarms**: the log on every raise; the webhook once per key per hour while a condition stands and once when
  it clears, as `{"source","level","key","text","at"}` with level `alarm`, `flag` or `clear`. Stall is 30
  minutes without a delivery either way while something waits; age is the oldest waiting message past two
  hours (by the time the monitor first saw it); own-node lag is a head older than five minutes; the client's
  one-hour lag is M3b.
- **The Solana flag** (the pausable ISM, re-asserted with a fresh slot every poll while broken), **"deliveries
  paused"** (the ISM's flags and the quarantine, which set `sends` alone on the gno routes and clear it after)
  and the **foreign-decision** and **header-mismatch** rules are M3b stubs (`solFlagStub`, `deliveriesPaused`).

### §5.8 hlwatch, the gno.land half

- Reads from the holder's own node only (`-node`, required; the command refuses without it) and prints: the
  mailbox's state, every seat, every standing flag with its ids, every pending decision of every realm with
  the time to its maturity by block time, every builder-made GovDAO proposal that is open — recognised by the
  executor's creation realm on the proposal's page, never by the title, which is GovDAO's own field — with its
  YES percent and whether it has the votes GovDAO's law needs (`-supermajority`, 66.66 by default: a pre-armed
  one), the identity's gno.land side per route with the supply it implies, and r/hlgov's counter. `watch` loops
  and alarms the holder on every change (a flag set or cleared, a decision recorded or mature, a proposal
  pre-armed, a route's pause).
- The flag command sets or clears the holder's flag on one function of one route through hlmailbox, after
  checking the key holds a seat. **A payouts flag naming ids is refused** until the Solana half can check the
  ids against the dispatch records (§5.8: on anything less than a node and two providers it refuses), and the
  Solana objects join the stroke in M3b.
- The DAO's page format is gno's examples' `r/gov/dao` (impl v0: the list's `### [Prop #N - title](…)` and
  `Status:`, the page's `Executor created in:` and `YES PERCENT:`); mainnet's implementation is read the same
  way and the parser is loose, but it is a text parse and a different implementation needs a look.

### The e2e (bridge/local/e2e.sh)

The relayer's own loop pays the claim (the script's manual `Claim` is gone); the relayer's gas key is a
dedicated one (`relayer`, funded by test1); seat 5 is swapped to test1 so the monitor's watch key could flag;
gnodev makes empty blocks every second (the cut wants a gno block after each Solana snapshot, as mainnet's 3.4 s
blocks give one); the monitor's status endpoint is checked for the identity line (lock = supply, nothing owed,
gap 0, `ok`), no `flag`-level alarm may reach the webhook sink, the three signers must still be signing; then
a GovDAO proposal made with r/hlfees' `set treasurer` builder is voted and left (hlwatch lists it pre-armed),
executed (hlwatch shows the queued decision maturing in 7 days), and the relayer's Execute loop is seen leaving
it alone. The ATA payer's 0.1 SOL trips the runway alarm, as §5.5 says the launch-day float would.

## Left for M3b (needs M2)

(M3b landed; its notes follow this list. Of the list, the public checkpoint store and the roster comparator remain.)

- The gno → Solana delivery under the light client (`deliverUnderLightClient`), the updater (headers on a clock
  and on the valset events) and its key, the decisions to the admin program by sequence number and the
  taken-number report, `Reemit`'s inputs (`clientView`), the header-mismatch and gap rules, the foreign-decision
  rule, the trusted height in the cut.
- The monitor's Solana flag (the pausable ISM), `sends` for paused deliveries, the client's one-hour lag alarm.
- hlwatch's Solana half: the supply and the dispatch index from two agreeing providers, the forged-id rule and
  the "unindexed, re-read" answer, the client's headers against gno.land's, the admin program's counter against
  r/hlgov's, every flag on both objects, the one-stroke flag on the pausable ISM and the admin program, the
  reproducible build hash.
- `hlsol setup` and `hlsol audit` as §5.8 describes them (BUILD.md M3 item 1).
- The public checkpoint store; the roster comparator (Proposal 2).

# M3b — what needs the programs (plan v26): what was chosen where the plan left room

M3b is the rest of M3: everything of the Go side that needs the three programs of M2. It was built against the
programs as they stood at M3a's merge (plan v25's shape) while plan v26 landed; the Go side follows v26, and the
formats the local programs do not yet implement are listed at the end (M2b lands them in `bridge/programs`, which
this half did not touch). Same rules as above: where PLAN.md is specific the code follows it; where it is silent
this file says what the code does and why; the plan is never changed here. Run the Go tests with `go test ./...`
in `bridge/`; the local loop under the light client with `bridge/local/e2e.sh`.

## What is where

- `gnolight/` — the Go twin of `programs/gno-light-core`: amino, the 16-leaf header hash, the precommit sign
  bytes, the validator leaf and set hash, the commit rule (3·tallied > 2·total), `SimpleProof` over
  `LastResultsHash`, the results leaf (`[1 error Any][2 data][3 event Any]*`) with the Dispatch event's seven
  attributes, the recovery and cancel digests, and the node's amino JSON (`BlockFromJSON`, `CommitFromJSON`,
  `ValidatorsFromJSON`, `BlockResultsFromJSON`). Tested on the chain's own blocks in `programs/fixtures`: every
  header hashes to the chain's, every precommit signature verifies over our sign bytes, the three real set
  changes, `LastResultsHash` from re-encoded results.
- `sol/` — the three programs' instructions, PDAs and states from `sol-util`'s layouts (`client.go`,
  `pausable.go`, `admin.go`), the upgradeable loader (`loader.go`), Hyperlane's inbox, outbox, token and
  Token-2022 reads (`hyperlane.go`), borsh (`borsh.go`), and the `process` builder with the ISM's and the
  recipient's accounts (`sol.go`). `testdata/vectors.txt` holds 58 encodings printed by `tools/vectors` (a Rust
  crate with path dependencies on the programs, kept outside them) and `TestVectors` rebuilds each in Go byte
  for byte.
- `gno/light.go` — `/block`, `/commit`, `/validators`, `/block_results` by height, and r/sys/validators' events.
- `agent/updater.go`, `agent/stage.go` — the updater (`hlbridge updater`) and the staged-transaction driver the
  relayer shares (an update or a proof goes up in chunks and is resumed from the on-chain stage account).
- `agent/relayer_sol.go` — `deliverUnderLightClient`, the decision gate, the taken-number report, a cancel's pairs.
- `agent/record.go`, `agent/monitor_sol.go`, `agent/views_sol.go` — the client's header record rebuilt from
  instruction data, the monitor's Solana checks, and the reads `hlwatch` shares with it.
- `cmd/hlsol` (`setup.go`, `audit.go`), `cmd/hlwatch` (rewritten with its Solana half), `cmd/hlbridge updater`.
- Gone: the multisig stand-in (`localMultisig`, `solDelivery`, `deliverUnderMultisig`, `signGno`, the secp256k1
  key in `hl`) and the signers' `address` in the config. A signer key file is ed25519 only; `hlkey new` prints
  the one key.

## Readings and gaps, by section

### §5.5 and §6.1 the updater

- **A command of its own, not a relayer job.** §5.5 gives the updater its own key and float; a job inside the
  relayer would die with the relayer, and a header is what every delivery and the client's one-hour lag alarm
  wait on. It finds a waiting dispatch itself (hlmailbox's last message, at height h, needs header h+1 at or
  below the client's trusted height), so it takes no word from a relayer.
- **When it submits**: every 10 minutes (`UpdateInterval`), at once when a dispatch waits, and the announcing
  header the moment r/sys/validators emits `ValidatorAdded`/`ValidatorRemoved` (the events are scanned from the
  last scanned height, a catch-up bounded at 300 blocks a tick). The announcing header is found by a binary
  search over `/validators` set hashes for the first height X whose set differs from the client's (a linear scan
  when the search's one-change assumption fails): header X−1 goes up with validators(X) as the next set, which
  the client verifies the following commit with.
- **Staged updates are keyed by (submitter, header hash)**: a stage left half done is resumed from the chain (the
  stage account says which chunks are verified), never restarted; a stage the program refuses because someone
  else moved the client first is closed and the plan made again. 400,000 compute units a transaction
  (`-sol-units`); the ed25519 precompile instruction carries each chunk's signatures.
- **A trusted header that is itself announcing cannot be followed**: the client's init takes the set of the
  trusted height only, so a header announcing a change leaves no next set to verify the next commit with. `hlsol
  setup` takes the header before it and says so; the updater alarms if it finds the client there.
- **The lag alarm** is one hour from the last accepted header's time (`ClientLagAfter`), raised by the updater
  and by the monitor; the status endpoint shows each tick's plan (the target, whether announcing, the reason).

### §5.7 the relayers, the Solana side

- **The merkle path**: hlmailbox's `msg/N` gives the dispatch height h; the results leaf is the transaction in
  `/block_results?height=h` whose Dispatch event carries the message id (`findLeaf`), and the proof is that
  leaf's `SimpleProof` under header h+1's `LastResultsHash`. Header h+1 must be at or below the client's trusted
  height, else the message waits for the updater.
- **The proof is staged against the ring when header h+1 is already accepted** (the ring shortcut) and against
  the header with its commit otherwise, which the client checks against the current set. `process` then goes
  through the pausable ISM (its flags and state, the client's state and program, the proof stage) for a route,
  and through the client directly for a decision to the admin program (§6: an unpause must land while the
  bridge is paused).
- **A token's `Handle` accounts come from the simulation**, as Hyperlane's relayer takes them; a dynamic account
  equal to the payer is refused (the payer's key is never writable on a program's say-so). The admin program's
  `Handle` accounts are fixed, in the order its `handle` reads them: state, the decision's PDA, system, the payer
  (the deliverer), and for a cancel the authority PDA and then the pairs. (The program's `HandleAccountMetas`
  answers the authority PDA before the payer, which its own `handle` does not read that way — a relayer taking the
  metas by simulation and appending its payer would fail a cancel with `WrongAccount`, as the e2e did; a nit for
  the programs' owners. Our relayer builds the list itself.)
- **Decisions: one copy per sequence number, only when it is the next expected** (`decisionGate`: deliver, wait,
  past). A copy of a number the admin program has recorded ends "recorded" when one of its copies went through
  our mailbox and "taken" otherwise, with the alarm `decision:taken:<n>` — the report r/hlgov's `sync sequence`
  answers (§6.5).
- **A cancel's pairs**: the pending decisions it names, each with its deliverer, at most 16 (r/hlgov's
  `maxCancel` is 16 too; no address lookup table is built). A decision already closed is skipped; so is a staged
  upgrade (kind 8 with a buffer to return), whose closing needs more accounts than a `Handle` carries — it stays
  cancelled in the state until `close_cancelled`.
- **`Reemit` reads the client's real view** (`clientView.LeafBytes`): a leaf that cannot be rebuilt from the
  block (a failed transaction or an unknown event type beside the dispatch) is a Reemit reason, as is a dispatch
  before the current set's first height.

### §5.9 the monitors, the Solana side

- **The accepted-header record is rebuilt from instruction data** (`StageInit` with `UpdateFinish`, `Init`,
  `Reset`, and a `process` that consumes a proof stage), with a running hash sha256(prev ‖ hash ‖ height LE ‖
  slot LE) that must equal the client's counter and running hash at the snapshot's slot; every recorded header
  is checked against the own node's block hash. **The fork rule**: the first differing header gives the Solana
  slot S of the cut, and the monitor flags the pausable ISM and the admin program through the watch key at once
  (`header:mismatch`); a gap in the record flags after a round of patience (`header:gap`).
- **The trusted height T is in the cut**: the Solana snapshot (both providers agreeing) gives T; the own node
  must be at a height ≥ T, else "client past the chain" — no verdict that round, and a flag if it persists (a
  client ahead of the own node is a fork or a forged header).
- **Every mailbox-channel decision is checked against r/hlgov's `decision/<n>`** body: a number r/hlgov never
  sent, or a body that differs, is a foreign decision, flagged on both objects (`decision:foreign:<n>`).
- **Paused deliveries mirror to gno.land**: while the pausable ISM carries any flag the monitor sets `sends` on
  every route through its gno watch key (no new lock while Solana cannot mint) and clears what it set once the
  flags clear.
- **An invariant break flags the pausable ISM too** (`credits`/`payouts` on gno.land as in M3a, the Solana flag
  beside them); the admin program is flagged only for a header or a decision finding.

### §5.8 hlwatch, the Solana half

- **The supply from two providers** that must agree (else "not read"); **the dispatch index** of burns toward
  gno.land from both (a burn one provider shows is "seen by one provider only" and not counted); **the forged-id
  rule**: an id credited on gno.land that neither provider's index holds is forged, one a single provider holds
  is "unindexed, re-read".
- **Pending decisions on the admin program with their maturity**: the kind's delay at the client's clock factor,
  plus §6.4's deferrals (kind 5 and a lapse-opened recovery decision land on the later of their own 7th day and
  the hold's 21st); both counters (the admin program's last number and r/hlgov's) with `COUNTERS DIFFER`; the
  recovery window and, under v26, a cancel's digest anchor.
- **The one-stroke flag** (`flag -sol-key`): the holder's gno seat and Solana slot must be the same seat on both
  chains; then one call on gno.land and one transaction on Solana set the flag on the pausable ISM and the admin
  program. `-ids` names the suspect ids and prints each one's forged verdict; `unflag` mirrors it. `version`
  prints the build hash the launch notice carries; `status` prints it first.
- **The client against the node**: the trusted header's hash against `/block`, the record consistent with the
  client's counter and running hash, each recorded header against this node; `-state` keeps the index and the
  record between runs.

### `hlsol setup` and `hlsol audit`

- **The order** is §5.8's binding one, every step read from the chain first so a second run repeats nothing: the
  launch values into the programs' `config.rs` (the program ids, the mailbox's pkgpath, r/hlgov's address — the
  seats, the routers and the treasurer are init values, not compiled); the reproducible build (`cargo-build-sbf`,
  the factor as a feature, a stamp over the sources and the factor skips a build already done); the six
  deployments one at a time with `--max-len` at the ELF's size, a failed deploy's buffers closed first and a wait
  for the slot after the deploy (the loader refuses a program in its deployment slot); Hyperlane's inits right
  after each of its programs (the mailbox with the pausable ISM as the default ISM, the 0.05 SOL maximum and the
  treasurer; each token with the pausable ISM, no paymaster, the Token-2022 mint with the mint PDA as its own
  authority, the metadata, the ATA payer's float); our three inits (the client at the latest non-announcing header
  less than an hour old, grown to the ring's size; the seats on the pausable ISM and the admin program); the
  settings (the routers, the ISM pointers, the metadata authorities to none); the owners; the five upgrade
  authorities to the admin PDA and its own burned last; `finish_setup`; the authority PDA's float. Done when the
  audit passes: no human key holds an authority.
- **The audit** prints every RPC call as a `curl` line before its answer (a printing transport) and §5.8's whole
  list, one `ok  `/`FAIL` line each, the admin program's own authority as "its own = none".
- **`keygen`** derives a keypair from a seed, for local runs only (stable ids keep the build cache warm); never
  for a deployment.

### The e2e (bridge/local/e2e.sh)

- From nothing: the validator with mainnet's feature set and Token-2022; the programs' sources staged outside the
  repo (the launch values are written there, never in `bridge/programs`), built at factor 8 and cached by the
  stamp; `hlsol setup` for the six deployments and the setup step, then a second `hlsol setup` that must send no
  transaction; gnodev with the realms swapped to the run's ids (r/hlgov's recipient, pausable ISM, client and six
  Solana seats; the routers; the signers; seat 5 to test1); GovDAO launches; three signers, the updater, a relayer
  and a monitor; 500 BUBBLE and 10 GNOT minted on Solana under the pausable ISM and the light-client ISM from
  real local headers; 200 BUBBLE back as a claim; the monitor's identity lines for both routes with the record
  checked; M3a's pre-armed and queued-decision checks; a kind-7 decision (set protocol fee) delivered and recorded
  pending at its factor-8 maturity; a kind-1 cancel of it executing at recording; `hlsol audit` PASS; `hlwatch
  status` green.
- **The factor-8 reading**: a decision's 7 days are 21 hours at factor 8, which no run waits for, so the e2e shows
  the decision pending at the right maturity and a cancel (kind 1, which executes at recording) closing it. A
  decision executed after its delay is M4's 72-hour run.
- **Where the runs got to** (M3b's session was allowed three): run 1 fell at the keys (`hlsol keygen` wrote the
  key as base64, not solana-keygen's byte array); run 2 at the mailbox init (sent in the slot the program was
  deployed in: the loader's "Program is not deployed" — `hlsol setup` now waits for the next slot and retries an
  init on that error); run 3 at the audit after the setup step (read at finalized seconds after the last
  transactions, it saw nothing: the audit inside `setup` reads at confirmed, a holder's `hlsol audit` at
  finalized). `E2E_ONLY=setup bridge/local/e2e.sh` then ran the setup step alone against the v26 programs:
  the six deployments, the setup step, a second `hlsol setup` that sent nothing, and `hlsol audit` PASS. On the
  merge with the v26 programs the full loop then found two more: the staged transactions' `StageVerify` named the
  precompile at index 0 while the compute-budget instruction was prepended at 0 (sol-util's `NotThePrecompile`:
  the builders now carry the budget themselves and name index 1; `TestStagedTxsNameThePrecompile`), and the
  admin program's `Handle` accounts for a cancel had the authority PDA before the payer (above;
  `TestHandleAccountsFollowTheProgram`). With those the loop ran green from nothing on the v26 programs
  (2026-10-12, ≈4 minutes after the cached build): both mints from real local headers (one header submitted for
  the waiting dispatch, trusted height 77), the claim paid, both identities holding with the record checked (8
  headers rebuilt and checked by the end), the kind-7 decision pending at 21 hours, the cancel executed at
  recording with both counters at 2, `hlsol audit` PASS with 22 RPC calls printed, hlwatch green.

## Plan v26: the Go side and the programs

The Go side was written to v26 (§6.4, §6.5, §13) while the programs were still v25; M2b's programs (merge 1bf9908)
are merged into this branch, so both sides now say the same thing. What v26 changed, and where it lives in Go:

1. **The anchored recovery-cancel digest and its record**: sha256("gno-bridge-cancel" ‖ chain id ‖ admin program
   id ‖ anchor ‖ body), the anchor `0x00 ‖ holdSince i64 BE` (hold-opened) or `0x01 ‖ the last accepted header's
   hash` (lapse-opened), and a PDA ("cancel" ‖ digest: version, anchor kind, submitter, slot, time) created when
   the cancel executes and never closed, so a repeat is refused. In Go: `gnolight.CancelDigest`,
   `CancelAnchorHold`/`CancelAnchorLapse`, `sol.Admin.CancelRecord` and `ParseCancelRecord`; the builders
   `RecoveryRecordIx` (a numbered decision: account 3 its decision PDA) and `RecoveryRecordCancelIx` (a cancel:
   account 3 its digest's record, in the decision's place); the stage PDA stays (`recovery`, submitter, digest)
   with the digest now anchor-dependent — `recovery_init` takes a stage under either anchor that exists at
   staging (the hold's whenever a hold stands on the admin program; the lapse's always) and `recovery_record`
   accepts only the standing window's, the hold's when both stand, which is also the order hlwatch's window line
   follows. The vectors `admin_cancel_anchor_hold/lapse`, `admin_cancel_digest_hold/lapse` and
   `admin_cancel_record_pda` pin the Go side to the program's anchor layouts, prefix and seed; `TestCancelDigestV26`
   pins the formula.
2. **Kind 5 under a hold lands on the later of its own 7th day and holdSince + 21 days** (`HoldReplacementDays`).
3. **A lapse-opened recovery decision is deferred by a hold like a mailbox decision; a hold-opened one is not**
   (`Decision.HeldToTheDay`, hlwatch's maturity).
4. **Kind 8 refuses the admin program's own id** (the program spends such a decision); `finish_setup` requires its
   own upgrade authority to be none — `hlsol setup` burns it last and the audit prints "its own = none".
5. **The signers' refusal rule in §5.9's order** is Go-side: the Solana snapshot with T first, the own node at a
   height ≥ T; a reading the node cannot give yet alarms (`signer:no-verdict`) and never stops a signer that was
   signing; a stop clears itself once the identity holds at a height ≥ T.

The e2e's cancel is a mailbox-channel kind 1, which none of these touch; no machine drives the recovery path (it
is the settled validators' and the guardians' own), so the recovery builders are exercised by the vectors only.

## Gaps

- `sol-util`'s outbox parse assumes an owner is set (the Go `ParseOutbox` reads `None` too); a nit for M2b.
- A cancel naming more than 16 pending decisions closes 16 in the `Handle` and leaves the rest to `close_cancelled`.
- The public checkpoint store and the roster comparator (Proposal 2), as before.

# M3c — the Go side at plan v27/v28: what changed under it, what was chosen where the plan is silent

M3c brings the Go side to the programs of M2c and the realms of M1b (plan v27, read as v28), after both changed
under it. Same rules as above: where PLAN.md is specific the code follows it; where it is silent this section says
what the code does and why; the plan is never edited. Run `go vet ./... && go test ./...` in `bridge/`, the loop
with `bridge/local/e2e.sh`.

## What changed, by piece

1. **`bridge/sol`.** `Decision` carries the trailing `Floor` (`ParseDecision` checks the exact length, counting the
   32 spare zero bytes the program's allocation leaves while the refund key is `None` — `Decision::size` sizes
   every account with the key present — so a v26 account, or any other layout, is refused rather than read
   short). Kind 1's parameters start with the `salt u64` on both channels (`Params.Salt`; `ParseParams` and
   `EncodeParams`; a mailbox body's parameters are the bytes after the 9-byte prefix, salt included). Kind 4 is
   reserved in the table (`KindReserved`; no builder; `ParseParams` answers `ErrReserved`, which `errors.Is`
   `ErrParams`; `ExtraFor` takes no accounts: Execute spends it). `AdminError` names 3035 `CancelSpent`,
   `PausableError` 2017 `LayoutFixed`; `Pausable.SetLayoutVersionIx` is gone (the program refuses the instruction
   outright; discriminator 6 stays `TransferOwnership`). `AdminState.HighWater` is `State::high_water`. A
   lapse-recorded recovery decision is read as v27 reads it: only a replacement (kind 5) takes the floor
   (`HeldToTheDay`), every other 7-day kind waits until the hold ends like a mailbox decision
   (`HeldUntilTheEnd`); v26's Go timed them all by the 21st day. `MaturesAt` uses the stored floor while a hold
   stands, or — none written yet — the standing hold's 21st day, which the first Execute will write
   (`FloorUnder`). `tools/vectors` builds against the v27 programs (`Params::Cancel { salt, ranges }`; no
   `Params::SetLayoutVersion`, so `body_layout` is gone, and `pausable_set_layout` with the builder); 61 vectors
   regenerated and green byte for byte.
2. **The relayer's delivery.** A decision's `Handle` accounts come from the program's own `HandleAccountMetas`
   answer, asked by simulation exactly as a token's are (v27 answers the state, the decision PDA and the system
   program for every kind); the answer is checked against `Admin.HandleMetas` (another answer is "not the v27
   program", a deterministic failure that parks), then the payer is appended as the deliverer and, for a cancel,
   the authority PDA and the pairs — `Admin.HandleAccounts(metas, …)`, in `handle`'s order. A cancel's ranges are
   read past the salt (`cancelRanges`: the parameters after the 9-byte prefix start with it); a cancel naming
   the recovery channel is spent by the program, so nothing is passed to close.
3. **The executor on the admin program** (`agent/executor_sol.go`), which did not exist: M3b left Execute on
   Solana to the 72-hour run. The relayer now attempts `Execute` on every pending decision of both channels
   whose turn has come, by the Clock's time at the compiled factor (`executeWhen`): at maturity; for a
   replacement of guardians under a hold (a mailbox kind 5, or a lapse-recorded recovery one) at once while no
   floor is written, so the hold's 21st day is on record before the holders can lift and re-flag — the first
   Execute a hold defers succeeds without executing (v27), and the executor reads the decision back and reports
   the floor written; a cancelled decision at any time (Execute closes it; a staged kind 8's buffer, refund key
   and the loader are passed for its return). A decision a hold defers until the hold ends, one a lone flag
   defers, and a kind 8 whose buffer is not staged (unless it names the admin program itself, which Execute
   spends) are left alone. The program's `NotMature` and `Deferred` answers are waits, never failures; a
   `CancelSpent` or `LayoutFixed` refusal is reported (`execute:refused:<channel>/<number>`) and never retried,
   kept in the state file as `refused`; `NeedsExtend` is answered with one `ExtendForUpgrade` step (10 KB, the
   executor paying the rent) and Execute again next tick; any other refusal follows §5.7's parking. The primary
   acts at once, the backup 60 s later. Neither error can come from anything the relayer sends today
   (`CancelSpent` is `recovery_record`'s, which the validators' tool sends; `LayoutFixed` the pausable ISM's
   init's and `SetLayoutVersion`'s), so the rule is a guard: a refusal nothing can change is never retried.
4. **The signers' stop (§5.6).** When the client's trusted height T is above the own node's head by more than
   the allowance — `gno.aheadBlocks`, 1,050 by default (3,600 s / 3.43 s) — the signer asks a second source
   (`gno.sources`, the first that answers; rpc.gno.land in a deployment) and stops only when T is more than the
   allowance past that head too (`judgeAhead`): the client is ahead of its chain, the mismatch itself. The stop
   is a halt like the identity's, said on the status (`halted`, and the heights in `cut`), and clears the same
   way: the identity holding at a height at or above T (`resume`); a nonce conflict is still never cleared by
   it. T above the head inside the allowance, past it on the own node alone (the second source's head within it:
   the own node lags), or with no second source configured or answering, is a reading the node cannot give
   yet: `signer:no-verdict`, never a stop. The second source is read only when the own node is past the
   allowance, so a dead source costs nothing in the ordinary round. The e2e's config names the local node twice
   as its sources (as the monitor's); a deployment names rpc.gno.land.
5. **hlwatch and hlsol.** `sol.DescribeParams` says what a decision's parameters mean (a cancel with its salt and
   the numbers it names, kind 4 as "reserved, will be spent", a bad body as spent); hlwatch prints it beside
   every pending decision and lists r/hlgov's last five dispatched decisions (`decision/<n>`) under its r/hlgov
   section, where an executed cancel — which leaves no pending account — shows its salt; a new one is an alarm
   in `watch`. A pending replacement shows its floor: "floor: day 21 of the hold, written (…)" once the program
   wrote it, "not yet written (the first Execute under the hold writes it)" before, and with no hold standing
   "no hold stands now, so it lands on its own 7th day"; `watch` alarms when a floor is written. `hlsol audit`
   checks the client's state layout version (1, a PASS/FAIL line) and prints the admin program's high-water mark
   and its stored cancelled ranges, each pending decision with its text and floor; a decision account that is
   not the v27 layout is a FAIL line, not skipped. `hlsol setup` is unchanged: the init formats did not move
   (the pausable ISM's `target_version` is still 1, which the program now pins).
6. **The e2e** (`bridge/local/e2e.sh`): the kind-7 decision pending at its factor-8 maturity is seen by the
   executor, which waits ("waits: matures at …" on the relayer's status) and executes nothing; the cancel step
   reads r/hlgov's salted body back (`decision/2`), the first cancel executes at recording and closes decision 1,
   hlwatch shows the cancel with its salt; a second cancel of the same list (its own salt, read back and compared)
   executes too — a no-op on the closed number, the range stored — and the admin program records 3; the audit
   must show the layout version line PASS, "high-water mark 3" and "cancelled range: channel 0 numbers 1-1"; the
   signer's status carries its allowance. The programs' staged sources are rebuilt at factor 8 since the v27
   sources moved the stamp.

## Readings where the plan is silent

- **A floor already past.** A hold whose 21st day has passed before any Execute attempt writes nothing (the
  program's rule); the executor then attempts at `MaturesAt`, which is the later of the 7th day and that 21st
  day, and the decision executes. hlwatch says "not yet written" until then, which is true.
- **The executor's reading of a hold** is the same pure function as the programs' (`Guardians.Hold`, from
  `holdSince`, `dipSince` and the Clock), at the relayer's wall clock. Solana's Clock may differ from it by
  seconds, so an attempt a few seconds early answers `NotMature`, which is a wait, not a failure.
- **The second source for the signer's stop** is `gno.sources[0]` (the monitor's public endpoint), then the next
  that answers. Without one the stop cannot be judged and the signer alarms every round it would have stopped;
  the operator's deployment config names one (§5.9 wants rpc.gno.land for the monitors anyway).
- **A simulated `HandleAccountMetas` answer that differs** from the three accounts is refused rather than used:
  the only program that answers otherwise is not v27's, and its `handle` would read the payer in the wrong
  place.
- **`ParseDecision`'s exact length** counts the program's allocation (32 spare bytes while the refund key is
  `None`); hlwatch's and the executor's pending-decision read (`readPendingDecisions`, shared) fails loudly on
  any other layout instead of skipping the account, since a skipped decision is one nobody executes.

## What passes

`go vet ./... && go test ./...` green (the new tests: `TestHandleAccountsFollowTheProgram` on the v27 answer,
`TestParseDecisionFloor`, `TestSaltedCancelAndReserved`, `TestCancelDigestV26` with two salts, `TestExecuteWhen`
over 26 cases, `TestJudgeExecuteErr`, `TestCancelRangesPastTheSalt`, `TestSignerStopsWhenTheClientRunsAhead`).

The e2e, from nothing, on 2026-10-12 (run 1 at 06:48 UTC, 3½ minutes end to end with the staged v27 programs
rebuilt at factor 8 under the warm cargo cache): the six deployments and the setup step, a second setup that sent
nothing; GovDAO's launch; the three signers signing (the status now says the client may run 1,050 blocks ahead
before the stop); 500 BUBBLE and 10 GNOT minted on Solana from real local headers; 200 BUBBLE back as a claim
the relayer paid with the fee first; the locks equal the supplies, the monitor's identity lines holding with
the record checked, no flag; the pre-armed proposal and the queued decision; the kind-7 decision delivered
from the simulated `HandleAccountMetas` answer and recorded pending at 21 hours, shown with its text ("set
protocol fee 1000 lamports, beneficiary …"), the executor waiting on it ("waits: matures at …") and executing
nothing; r/hlgov's cancel of it with the salt read back from `decision/2` (0xb31fa9d1a511b5db), delivered from
the simulated answer with the pair appended, executed at recording, decision 1 closed, hlwatch's r/hlgov section
showing "#2 kind 1: cancel 0/1 (channel/number), salt 0x…"; a second cancel of the same list under its own
salt (0xb5da59d73c335782) recorded as 3 and executed, the range stored; the counters both 3, 5 messages
delivered, nothing taken, no Execute refused; `hlsol audit` PASS with 23 RPC calls printed, the layout-version
line ok, "high-water mark 3" and "cancelled range: channel 0 numbers 1-1"; hlwatch green (trusted height 176
matching the node, 8 headers rebuilt and checked, both identities HOLDS). Run 2 (06:51 UTC, the final code with
the executor's minute of backoff on a `NotMature`/`Deferred` answer, the build stamp unchanged) ran green the
same way in 3½ minutes; the third run allowed was not needed.

## Gaps left by M3c

- The executor sends no address lookup table: a kind 9 transfer's Execute names 18 accounts after the seven
  fixed ones, inside a transaction's limit; a kind 1 pending that never parsed is spent at Execute with none.
- `CloseCancelled` is not sent by any machine: a decision a cancel left cancelled in the state (passed without
  its pair, or the 17th of a list) is closed by its next Execute, which the executor attempts at once, so the
  rent comes back the same way; a dedicated `close_cancelled` is only cheaper.
- The validators' recovery tool (staging, signing, `recovery_record`, a cancel under the standing anchor) is still
  the builders and vectors alone; M2-NOTES' "a tool should execute every pending recovery decision before the
  guardians lift" is the executor now for the decisions it can see, but the lift itself is the guardians' hand.
- The public checkpoint store and the roster comparator (Proposal 2), as before.

# Audit round 1 (2026-10-13): the Go side's fixes, and the readings chosen where the plan is silent

The findings are bridge/audits/r1/ (the Go audit's G1–G12, the red team's AN2, AN3 and AN10, the conformance
audit's K2, K3, K5, K6 and K12) and the assumptions audit's F2 and F3; this section says what each became in
`bridge/` and what was read into the plan where it is silent. One commit per group on branch `bridge-r1-go`, each
with a test where a pure part exists (`agent/audit_r1_test.go`, `hl/keyfile_test.go`, `sol/recovery_test.go`);
`go vet ./... && go test -race ./...` green; the e2e as below. PLAN.md is not edited here.

## What changed, by finding

- **G1 — a stale stage is rebuilt.** `driveStage` reads the stage on chain and compares it with the plan's
  `StageInit` field by field (`stageMatches`: the kind, a proof's message hash, by the ring or by a header and
  which hash, the precommit slots, an announcing update's next set, the proof's total, index, leaf hash and aunts,
  the leaf's length); anything else is closed with `StageClose` and the staging starts from the init. A `process`
  (or a `HandleAccountMetas` simulation) the chain refuses for good closes the stage too (`closeOnRefusal`), so
  the next attempt builds the plan of its day; a transient failure leaves it. `hlsol close-stage -stage PDA` closes
  one by hand (its submitter at any time, anyone after 7 days). Reading: the match is exact — a stage that differs
  in any field is another plan's, whatever the difference costs.
- **G2 — nothing is staged under a pause.** The delivery walk sorts its messages first: decisions go first (the
  admin program's gate, through the client, which a flag never blocks: an unpause must land while paused), then the
  routes' — only after one read of the pausable ISM's flags and the client's quarantine (`deliveriesPausedNow`,
  the same two facts the ISM's Verify reads); while either stands the relayer notes "deliveries: paused" and
  stages nothing. Reading: the check is per pass, not per message; a flag set mid-pass costs at most the stages of
  that pass.
- **G3/AN2 — the monitor's head is judged.** `judgeOwnHead`: a node that is catching up, whose head is not after
  the snapshot, or whose head is older than five minutes gives no verdict (`gno:own`, after two minutes). T above
  the head is judged as the signer judges it: inside the hour of blocks the identity is evaluated at the head
  (§5.9's own words; the monitor used to return); past it the second source decides (`judgeAhead`), a stop
  persists two minutes before `header:mismatch` flags both Solana objects, and the identity is still judged at
  the head; the own node lagging alone, or no second source, is no verdict. The signer the same: inside the
  allowance it evaluates at its head instead of skipping the round. Reading: the two-minute persistence is the
  cut's own `cutFailAfter`; the monitor's "second source" is `gno.sources[0]`.
- **AN3 — uncredited burns count as supply for the signers.** `Identity.SignerStops` is `supply + uncredited >
  lock − owed − voided + lost`, where uncredited is every burn both scans indexed at or before the snapshot's slot
  that `delivered/<id>` does not show at the height (the signer keeps a dispatch index with amounts and credited
  heights now). An honest burn in flight leaves the sum where the supply was; a forged mint burned back is the
  uncredited part. The monitor's verdict is unchanged (in flight only shortens the right side there).
- **G4 — the taken report on two finalized passes.** The admin state and the processed-message records are read
  at finalized; every copy of a decision number is kept across passes (`DecisionCopies`); a recorded number ends
  "recorded" when any copy went through our mailbox — a copy this relayer delivered, or one whose record stands at
  finalized — and "taken" only when no copy's record is there on a second pass either; the alarm fires once.
  Reading: the next decision waits the finality of the one before it (~13 s), the price of the finalized gate.
- **G5 — status snapshots.** The relayer, the monitor and the updater publish a status snapshot at the end of
  each round (`statusBox`: encoded under its own lock, of copies); the handler writes the bytes. The signer builds
  its status under its mutex, of copies, and its `index` writes its cursor under the lock. The signer's `/v1/status`
  is served on `-status-listen` (a loopback address, refused otherwise; 127.0.0.1:9140 by default), apart from the
  public `/v1/sol/` listener.
- **G6 — two providers behind every flag.** A `solReader` is one provider or a `pair`, which answers only what
  both say byte for byte (an account's owner and data; a transaction's slot, failure, bytes, loaded addresses and
  inner instructions; a listing of signatures from the first alone — it is a cursor; every transaction it names
  is read from both). The monitor reads the record's transactions, the admin and pausable accounts, the pending
  decisions, r/hlgov's numbers' decisions and the processed-message records through the pair; a disagreement is
  the alarm `solana:providers` and skips that rule for the poll. hlwatch reads the same sections through the
  pair, and a section the providers give differently is read again from the first alone and printed
  `UNCONFIRMED`; its flag command names no id on such a reading.
- **G7 — the verdict's age, the index's own cursors.** A held cut stamps `lastVerdict`; a round signs on no
  verdict older than one round (two rounds' worth, for the round's jitter; AN2) and an hour without one halts the
  signer ("no verdict for an hour") until one returns. `indexScan` keeps an in-memory cursor per provider, started
  at the signing cursor at load, and the unsigned dispatches in memory in provider 1's order; a dispatch is signed
  once both providers' scans showed the same transaction with the same bytes (the per-dispatch `solConfirm` call
  is gone); the signing cursor is the last signed. A halted signer fetches nothing twice.
- **G8 — the backlog's cost.** The leaf cache is read (`LeafBytes`); `reemits` runs once a minute or when a
  Reemit attempt is due, and skips a message whose read fails instead of ending the pass; `Done` is pruned to the
  nonces at or past `GnoNext`; `readSends` continues past a failed message and reports the first error at the
  end.
- **G9 — inner instructions.** `transactionInstructions` folds each top-level instruction then the inner
  instructions recorded under it, in execution order, so a kind-11 reset (the admin program's Execute calling the
  client by CPI) reaches the record (`TestInstructionsInOrderFoldsInner`). The e2e has no reset step: kind 11 is
  a recovery kind — 7 days ÷ 8 after a 42-hour window — so the loop's reset is M4's 72-hour run.
- **G10 — the audit compares the trusted hash.** One `ok/FAIL` line over the full hashes
  (`/block?height=<trusted>` against the client's); the age of the trusted header is noted. `hlsol setup` refuses
  a trusted header older than an hour unless `-allow-old-header` (never for a deployment).
- **G11 — decisions are re-emitted** like route messages (`classify` in `reemits`): the set-change, leaf-size and
  unbuildable-block reasons are the same.
- **G12 — hygiene.** State files are written to a temporary file, fsynced, renamed, the directory fsynced; a
  zero-length or unparseable state or signature file is named as corruption ("restore it or delete it knowingly")
  and never read as an empty state or a nonce conflict; every key file (a signer's seed, a Solana keypair, a
  recovery key) is refused wider than 0600 or empty (`hl.CheckKeyFile`, `sol.PrivateKeyFromFile`); the same
  provider twice is refused unless `allowHTTP`; every Solana RPC call ends within 30 s (`sol.NewRPC`), a gno RPC
  read within 30 s and a gnokey call within 90 s (`gno.GnokeyTimeout`: the simulation, the broadcast and the wait
  for the commit); the Solana watch key re-asserts its flag at most once a minute across every condition, and
  both watch keys' floats are watched (`watch-sol`, `watch-gno`, at the observed rate, no planned spend: a key
  that flags every minute shows the drain); `forget` clears a parked alarm. Reading of §5.9's "a fresh slot on
  every poll": once a minute is fresh for an unpause that reads the slot.
- **K2 — a strict recipient.** `hl.Bytes32ToGnoAddress` errors on non-zero top twelve bytes; a dispatch to no gno
  address is indexed with an empty recipient and the mark `bad`, and the diverted-credit check expects a sweep of
  its amount (the realm sweeps it, M1c); the relayer still delivers it (the realm decides).
- **K5/K6 — the scan's side list; amount 0.** `solScan` skips a transaction it cannot read, alarms
  (`scan:unreadable:<key>`) and keeps it on a side list retried every round (`retrySkipped`) — the cursor moves
  on; the signer, the relayer and the monitor each keep one. An amount-0 burn is skipped by the relayer and not
  indexed by the watch (nothing to credit, nothing to wait for).
- **K3/K12/F3 — `cmd/hlrecover`, and hlwatch's recoveries.** `hlrecover status|body|init|sign|submit|record|close`:
  the body of a recovery kind with sequence 0 (a cancel's list or range with its salt, kinds 2, 5, 10, 11 from
  flags, any kind from raw parameters; kind 11's header and set read from a node of the new chain), the digest at
  the next counter or a cancel's under the window's anchor (both printed when a hold stands), `recovery_init`
  under the anchor named, a signature over the 32-byte digest from a key file (an hlkey file, a
  priv_validator_key.json, or a raw 32- or 64-byte key in hex or base64 — tmkms's softsign file) or a YubiHSM 2
  through its connector (sign-eddsa under the authentication key, the key id as flags; `yubihsm-go`, untested
  against a device here), the precompile + `recovery_verify` transactions three signatures at a time (each
  checked against the stage's set first: the slot, the power carried), `recovery_record` (a cancel with its
  digest's record, the authority PDA and the pairs it names; another kind at the next counter), `recovery_close`.
  Its help states the procedure (replacements first, foreign decisions, the corrective decision, the reset last)
  and that a Ledger-held or tmkms-served consensus key signs no recovery (they sign votes, proposals and framed
  raw bytes, never the bare digest). `sol.SettledSet` and `sol.RecoveryWindow` are pure twins of the program's.
  hlwatch keeps a `RecoveryLog`: the admin program's transactions scanned once, each `RecoveryVerify`'s precompile
  entries kept by the digest they signed, each `RecoveryRecord`'s stage and account; every recorded recovery is
  printed with its signers, their power in the set the client holds for them and whether it was carried, and
  matched to its pending decision by the recomputed digest.
- **AN10 — the order of the deploy.** The e2e runs `hlsol setup` and its audit to PASS before any realm goes up
  (the realms are uploaded with `addpkg` after it); each Hyperlane init goes in the first slot after its deploy
  with retries (`waitUsable`, `send`'s retry on "not deployed"). A private RPC path is the deployment's choice.
- **F2 — proposals past their deadline.** `ReadProposals` (the DAO page's parse, moved from hlwatch into agent)
  feeds the relayer's `finishProposals`: a builder-made proposal with the votes, open longer than the realms'
  deadline — hlmailbox's `clocks.deadline`, 14 days ÷ the compiled factor — counted from the relayer's first sight
  of it (the creation time is not rendered, so the call never comes early), gets `ExecuteOrRejectProposal`, which
  denies it for good; `hlwatch status` marks such proposals PAST DEADLINE and `hlwatch finish` sends the call;
  the e2e executes every builder-made proposal through `ExecuteOrRejectProposal`. Reading: a proposal without the
  votes is left alone (the call would only waste gas), and one within its deadline is its members'.
- **M1c's formats.** The realms' `json` carries `clocks` (the factor, the divided 7-day delay and 14-day deadline;
  the meters are never divided), which the relayer's deadline, hlwatch and `hlsol audit` read and print;
  `claim/<id>` carries `height` (read, unused yet; `claims/<from>` and `sweeps/<from>` page the lists and are not
  read yet — the watch reads claims by id); hlwatch's `flag -ids` takes a range as the one string
  `heights:<from>-<to>` or `since:<from>` in place of a list; the e2e's realms keep the factor at 1 (nothing in the
  loop reaches a realm delay).

## Other readings where the plan is silent

- The "one round" of AN2 is two rounds of the signer's own clock: the previous round's verdict still serves this
  round, with the round's jitter allowed for; the third round without a verdict signs nothing.
- A monitor whose second provider disagrees (or is down) flags nothing from the Solana side that poll and alarms;
  the gno-side rules still run (the supplies and the client's head must agree before anything).
- The watch's stale-head allowance is the own-node lag alarm's five minutes; a node that just came back serves an
  old head and is no reading of the chain.
- hlrecover's YubiHSM path is the connector protocol tmkms itself speaks; the device-level command is `yubihsm-shell
  --action sign-eddsa --object-id <key> --in <digest file>`, whose output the sign step's file form takes as a
  64-byte signature.
- gnodev loads a package it did not have at genesis lazily, at the transaction that first names it, and that
  load reloads the node and breaks the sender's sequence ("signature verification failed; verify correct
  account, sequence, and chain-id"): everything a transaction of the e2e touches — GovDAO's closure, the valoper
  realms, the nt packages — is staged in the extra root at genesis; the bridge realms go up by `addpkg` after
  the Solana audit (AN10), which gnodev's namespace check does not refuse (r/sys/names is not loaded).
- Two sections of the e2e order themselves by stopping the updater: a send made while no header can follow it
  waits (nothing is staged: the relayer's "header h+1 is above the trusted height"), the flag (or the set change)
  lands, the monitor's `sends` mirror lands, and the updater is started again — so the pause is seen before the
  first stage, and no send races the mirror (a send after it is refused on gno, one before the flag delivers).
- `hlsol setup`'s own audit, run before any realm is on the chain, notes hlmailbox's absence instead of failing:
  its seats and clocks are read once the realms are up (the holder's audit after the launch reads them).
- The e2e's recovery drill stops where gnodev stops it: its validator key is a random in-memory one (`bft.NewMockPV`),
  so no validator signature exists; the drill stages a cancel under the hold anchor, signs with a stand-in key, is
  refused by the tool's own checks at submit (the key is not in the settled set) and record (no window yet), and
  closes the stage. The drill with real signatures is M4's, on devnet.
- A validator-set change on the gno chain in the e2e goes through `r/gnops/valopers` (register test1 as an operator
  with a fresh ed25519 consensus key — `hlkey gnopub` writes it as gno.land's gpub bech32 — propose through its
  facade, execute): the second validator (power 1) never signs, the chain runs on 10 of 11, and the client follows
  the change through the updater's announcing header.

## What passes

`go vet ./... && go test -race ./...` green (the new tests: `TestSignerVerdictAge`, `TestSignerStopsOnUncreditedBurns`,
`TestSignerIndexAndSign`, `TestCorruptionIsNamed`, `TestTwoProvidersRefusesTheSameURL`, `TestJudgeOwnHead`,
`TestMonitorPersisted`, `TestMonitorBadRecipientExpectsTheSweep`, `TestDeliveriesPausedNow`, `TestPastDecisionTwoPasses`,
`TestPruneDoneAndForget`, `TestReemitsDecisionsAndSkipsFailures`, `TestFinishProposals`, `TestStageMatches`,
`TestInstructionsInOrderFoldsInner`, `TestRecoveryLogFold`, `TestSolanaKeyFileMode`, `TestCheckKeyFile`,
`TestGnoPubKeyBech32`, `TestRecoveryStateTwins`).

The e2e's sections, in order: the build; the keys (a valoper's consensus key, a stand-in recovery key); the gno
chain with the packages, GovDAO and the valoper realms at genesis; the Solana validator; `hlsol setup` twice (the
second sends nothing) and `hlsol audit` PASS with the trusted-hash line, before any realm; the seven realms uploaded
with `addpkg`; the launch through `ExecuteOrRejectProposal`; the machines (three signers with their status apart
from the public endpoint, the updater at a 20 s interval, a primary and a backup relayer, a monitor); the same
provider twice refused; 500 BUBBLE and 10 GNOT minted; the 200 BUBBLE claim paid, the backup idle; the identity
lines and the record; the pre-armed proposal and the queued decision; the kind-7 decision pending at 21 hours, the
executor waiting; the first cancel executing at recording with its salt; the primary stopped and the second cancel
delivered by the backup after its 60 s; the watch key's one-stroke credits flag — deliveries paused, nothing staged,
a burn failing against the gno flag; a hold of two guardians with a dip, and hlrecover's status, body, init, sign,
submit (refused: the stand-in key), record (refused: no window), close; a second monitor through a provider that
lies about the supply — no verdict, `solana:providers`; the burn parked after three failures, GovDAO's unpause and
the keys' clears, the send made under the flag delivered, the monitor's `sends` cleared; a validator-set change —
the client's quarantine, the message dispatched before it re-emitted, the relayer seeing the quarantine; `hlsol
audit` PASS; hlwatch green with the identities holding up to what is in flight.

The three runs from nothing allowed on 2026-10-12/13 (each ≈75 s to the Solana audit under the warm cargo cache):
run 1 fell at the audit inside `hlsol setup`, which read hlmailbox's seats from a chain the realms were not yet on
(AN10's order; the audit now notes the realm's absence instead of failing); run 2 fell at the e2e's own `run`
helper, which had put the script before the key name; run 3 ran green through its first fifteen sections — the
setup twice and the audit PASS before the realms (the trusted-hash line ok at height 44), the seven realms
uploaded, the launch through ExecuteOrRejectProposal, the machines (signer 0 signing on the cut "slot 120, then
height 87", its status apart from the public endpoint), the same provider twice refused, 500 BUBBLE and 10 GNOT
minted from real local headers (2 headers submitted, trusted height 91), the claim paid with the fee first and the
backup idle, both identities holding with 5 headers rebuilt and checked, the pre-armed proposal and the queued
decision, the kind-7 decision pending at 21 hours with the executor waiting, the first cancel executing at
recording with its salt, and the primary stopped and the second cancel delivered by the backup after 81 s (its
60 s offset, then the staging) — and fell at the script's own next assertion, which expected the delivered
nonce in the backup's `done` map that G8 now prunes once the walk is past it (the assertion now reads `gnoNext`).
The sections after it — the flag with nothing staged and the burn parked, the hold with the dip and hlrecover,
the disagreeing provider, the unpause, the validator-set change with the quarantine and the re-emit, the final
audit and hlwatch — were not reached by a run and are unverified end to end; the valset change alone was probed
on a throwaway gnodev (register, propose, execute: two validators, powers 10 and 1). One run of the fixed script
is the next step.

Runs 4–6 (the coordinator's re-runs, 2026-10-12 20:00–20:20 UTC): run 4 passed the backup's check and the whole
flag section (deliveries paused on the watch key's one stroke, nothing staged, the burn failing against the gno
credits flag) and fell at the hold section's first hlwatch read, 3 s after the two guardian flags were confirmed —
hlwatch reads at finalized through both providers, some 15 s behind (the script now waits for the finalized
view); run 5 fell at the same-provider refusal's grep, which accepted one of the two wordings `hlbridge providers`
gives depending on the order it walks its map (both accepted now); run 6 passed everything through the unpause's
delivery — the hold with the dip shown by hlwatch, hlrecover's status, body, init (the stage on chain), sign,
submit refused (the stand-in key is not in the settled set), record refused (no window for 42 h), close; the
second monitor through the lying provider with `solana:providers` and no verdict; the burn parked after three
failures (the alarm at the sink); GovDAO's unpause and the keys' clears; the send made under the flag delivered
(alice at 590,000,000: 500 minted − 200 − 10 burned + 300) — and fell on the script's expected balance, which had
forgotten alice's own two burns (fixed). Left unverified by a run: the monitor's `sends` cleared after the
unpause, the validator-set change with the quarantine and the re-emit (probed on a throwaway gnodev), the final
audit and the final hlwatch. One run of the fixed script covers them.

# M3e (2026-10-13): the Go side at the programs' audit-round-1 formats (M2d), and the e2e finished

The programs changed under the Go side in M2d (M2-NOTES, "Audit round 1" and the inventory under "The Go side");
this section says what each format became in `bridge/` and what was read into the plan where it is silent. Branch
`bridge-m3e` (from `bridge` at 2f8c8b8, M1c + M2d + M3d merged), one commit a piece; `go vet ./... && go test -race
./...` green; the e2e as below. PLAN.md is not edited.

## What changed, by piece

1. **`bridge/sol`** (`admin.go`, `client.go`, `pausable.go`, `recovery.go`, `loader.go`; `tools/vectors`
   regenerated, 68 vectors green byte for byte). The admin `AdminState` reads, after the cancelled ranges, the
   recorded settled set (`Settled`: hash, the validators, the from-height), the hold history (`Holds`, at most 8),
   the pending counts (`Pending [2]`), the predecessor and `Inherited`, the account's reserve (zeros past the
   borsh form) behind them; `ReplacementFloor` is `State::replacement_floor`. `Decision` lost its floor;
   `ParseDecision`'s exact length moved back 8 bytes, so a v27 account is refused rather than read short.
   `MaturesAt(factor, hold, state)` and `FloorUnder(hold, state, factor)` compute a replacement's floor from the
   history (nil state: the standing hold's 21st day). `InitIx` takes the predecessor (no counters);
   `SuccessorInitIx` is a successor's init with zero seats. `FinishSetupIx` passes the pausable flags as account
   14. `ExtraFor`: kind 9 adds the successor's state (w) and the system program after the client program, kind
   11 the system program, kind 12 the pausable state (w) and program. `RecoveryInitIx(submitter, digest,
   bodyLen)` (accounts: submitter, stage, the admin state, system), `RecoveryChunkIx`, `RecoveryStageTxs` (the
   init, then 900-byte chunks), `RecoveryVerifyIx(stage, …)` reading the admin state, `recoveryRecord` without
   the pausable flags, `SettleIx` (variant 12). `ExtendForUpgradeIx` is gone; `ExtendProgramIx` in `loader.go` is
   the loader's own tag 6. Kind 12: `KindSetMintLimits`, `Params.Max/DayCap` with `Token`, parse/encode (17
   bytes; a third token is `ErrParams`, spent), `KindName`, `DescribeParams`; `SevenDay` 3–12, `MailboxKind`
   1–9 and 12, `RecoveryKind` 1–12. The client: `GrowIx(payer, bodyCap)`; `StageInitArgs` carries
   `NextSetLen`/`NextSetHash` (`Announce(set)`; `SetHash` zero for none) in place of the set; `StageNextSetIx`
   (variant 10) and `NextSetTxs` (25 a chunk); `UpdateTxs` sends the chunks between the init and the verifies;
   `Stage` reads `NextSet` as a plain vector then `NextSetLen` and `NextSetHash` (`Announcing`,
   `NextSetComplete`); `ClientBody.Size`, `SetEntrySize`, `BodyCapFor` and `BodyCapNeeded(n)` size the region a
   set change needs (`MaxBodyCap` 81,920, `StateSlack` 2048, `HistoryMax` 5). The pausable ISM:
   `PausableState.Limits [2]MintLimit` (max, day cap, used, used at; `Level(now, day)` drains as the program
   does), `SetMintLimitsIx` (variant 7), `VerifyAccounts` answering flags, state (w), client state (w), the
   client program, the Instructions sysvar, then the client's. The recovery stage carries `BodyLen`
   (`IsComplete`). The error tables: client 1034 `HistoryFull`, 1035 `ChunkOutOfOrder`; pausable 2018–2022;
   admin 3036–3039. `SettleDue(record, entry)` is `record_settled`'s test.
2. **The updater.** Before an announcing update it grows the client's body region toward what the set change
   needs (`ensureBodyCap`: `BodyCapNeeded`, one `Grow` a call, at most twelve, read back from the head; a target
   at the bound alarms `updater:body-bound`) and passes the target to the staging, whose `NeedsGrow` answer
   grows toward it; `UpdateTxs` stages the next set in chunks, and a stage already on chain skips the chunks it
   holds (`nextSetChunk`) and is this plan's only if its next set's length and hash are the plan's. Every tick
   it calls `Settle` when due (`settleIfDue`, shared with the executor: the client's newest settled entry by
   `sol.SettledSet` with both holds read, against the record's from-height; nothing sent otherwise; the status
   line `settle` says which).
3. **The executor.** `executeWhen` attempts a replacement at `MaturesAt` with the state — the later of its 7th
   day and day 21 of the earliest hold overlapping its pending life — and never "to write a floor" (the v27
   first-Execute dance is gone; the note names the floor). `NeedsExtend` is answered with one
   `sol.ExtendProgramIx(program, executor, 10,240)` top-level, then Execute again next tick (the loader allows
   one step a block). A success reads the decision back only to say whether it still stands. `Settle` after
   every quarantine's end, as the updater. Kinds 9, 11 and 12 take their accounts from `ExtraFor`. The
   taken-number logic is untouched. The relayer's delivery note names the program's refusal first ("failed
   (pausable ISM: OverMax): …"), and the parked alarm too.
4. **`hlsol`.** `setup` writes `MAILBOX` and `TOKENS` into the pausable ISM's `config.rs` beside the admin
   program's (the admin program's build asserts the two equal); `finish_setup` with the flags account; the admin
   init with no predecessor; `Grow` with no target. `audit` prints the mint limits with each bucket's level, the
   recorded settled set validator by validator, the hold history, the pending counts, a successor's predecessor,
   and has three new verdict lines: the three programs' clock factors agree, the client's and the admin
   program's origin domains agree, and the recorded settled set is the client's newest settled entry by hash
   (when their from-heights are equal; a newer client entry is noted as Settle due, a newer record as a reset's
   or an inheritance). `successor-init` sends a successor's init (zero seats, its predecessor) and, with
   `-burn`, burns its upgrade authority — the path kind 9 needs before it executes.
5. **`hlrecover`.** `init` sends `RecoveryInit` with the body's length and digest, then the body in chunks (one
   transaction each), the stage sized by the recorded settled set; `submit` checks the stage's set hash against
   the record (not the client's history) and refuses an incomplete body; `record` refuses an incomplete body, and
   a reset (kind 11) while another recovery decision pends, unless `-force` (C236: the reset is last); `settle`
   sends the permissionless Settle when the client's newest settled entry is newer than the record and says why
   not otherwise; `status` prints the record, the client's newest settled entry and whether Settle is due, and
   the hold history; `body -kind 12 -token T -max N -day-cap N`. `RecoveryFacts.Settled` is the record,
   `ClientSettled` the client's entry, `SettleDue` their comparison.
6. **`hlwatch`.** The admin line carries the pending counts; new lines: the recorded settled set (with "nothing
   to settle" / "SETTLE DUE"), the hold history, the mint limits with the buckets' levels; a pending replacement
   under a hold shows its floor from the history ("floor …: day 21 of the standing hold" or "of an earlier hold
   of the history that overlapped its pending life"); no "written" state any more. `watch` alarms when a
   pending replacement's floor appears or moves, when the hold history records a hold that ended, when the
   record moves (`settled:<height>`), when Settle is due, and when a token's limits move; the recoveries' signers
   are judged in the recorded settled set first (`RecoveryLog.Views(record, body, pending)`), then the client's
   history.
7. **The e2e** (`bridge/local/e2e.sh`): the sections in order are in "What passes". The new ones: a 30,000
   BUBBLE send above the pausable ISM's 25,000 maximum (the staged route's own maximum raised to 50,000 so the
   ISM is what refuses) is refused at delivery — the relayer's status names `pausable ISM: OverMax` — and parks
   after three refusals while the decision sections run (checked before the flag section pauses deliveries),
   in flight for the identity; a kind-12 decision (BUBBLE: 50,000 a message, 200,000 a day — the change that
   would let the parked send through at its hourly retry, at the decision's maturity only) delivered and
   recorded pending, hlwatch showing its text, the launch limits standing and two pending accounts; `hlrecover
   settle` finding nothing to settle, the updater's and the executor's `settle` lines saying the same; the audit's
   new lines. The cancels are decisions 3 and 4 now (the kind 12 is 2); the backup's walk stands at the parked
   send (`gnoNext` = its nonce).

## Readings where the plan or the task is silent

- **r/hlgov has no kind-12 builder.** PLAN.md v29 §5.5 and §13 give GovDAO `set mint limits`, the admin program
  executes it since M2d, and realm/hlgov (M1c) carries no builder; `realm/` is not this milestone's to touch. The
  e2e stages `bridge/local/hlgov_mintlimits.gno` — `NewSetMintLimitsRequest(cur, token, max, dayCap)`, written
  as the realm's own builders are (`proposeKind`), type-checked and tested beside the realm with the pinned gno
  — into ITS copy of the realm (as it swaps the launch values there). It is the realm lane's to adopt
  (`realm/hlgov`); until then the loop proves the Go side's kind-12 path with it.
- **The e2e's route maximum.** The staged hlbubble's `launchMaximum` is raised from 25,000 to 50,000 BUBBLE so a
  30,000 send leaves gno.land and the pausable ISM's own limit is what refuses it (the two sides' maxima are
  equal at launch by design; the test wants them apart). Nothing in the repo's realm moves.
- **"A mint refused … or assert the refusal and the parking":** the parking is asserted (three deterministic
  refusals, the hourly retry) — its retry after the kind-12 change lands at the decision's factor-8 maturity
  (21 hours), outside the run.
- **"Settle called":** no quarantine ends inside a run (a week ÷ 8 = 21 hours), so `Settle` can only find
  nothing newer than the launch set `finish_setup` recorded. The machines compute that before sending (no
  transaction for a no-op); `hlrecover settle` says why; the audit compares the record with the client's newest
  settled entry by hash. A real settlement is M4's 72-hour run.
- **Grow's target** is `BodyCapNeeded`: the body as it stands plus the larger of the announcement's `next_set`
  and the switch's history entry, with the program's 2,048-byte slack, rounded to 8 and bounded by 81,920. A
  staging's `NeedsGrow` grows toward the same target (0 for a proof: the body's own want).
- **The executor's Settle** runs in `solExecutes` every tick beside the updater's: the first to send records,
  the other reads NothingToSettle locally first (no send) or, racing, on chain (not a failure).
- **A reset is ordered last** by `hlrecover record`'s refusal while any other recovery decision pends (the
  reset voids the lapse-opened ones; a hold-opened one would survive, but the plan's procedure ends with the
  reset either way); `-force` is the operator's override.
- **The successor's init** is a command of its own (`hlsol successor-init`), since `hlsol setup` has no successor
  path: it initialises a deployed successor with zero seats and its predecessor and burns its authority on
  `-burn`; the predecessor's kind 9 (by GovDAO) then writes the live numbers, seats, record and history into it.
  A successor shares the deployment it inherits, so it has no setup step of its own.
- **hlwatch's floor alarm** fires when a pending replacement's computed floor appears or moves (a hold began, or an
  earlier overlapping hold came into view through the history), replacing the v27 "floor written" alarm.
- **The hold history in the loop** stays empty: the run's one hold dips but never reaches the 3-hour grace (24
  hours ÷ 8), so no hold ends; the e2e asserts "no hold has ended" in the audit and in hlwatch.
- **A signer's `signing` flag is a transient** (AN2: a round signs on no verdict older than two rounds — two
  seconds at the e2e's one-second rounds), so on a loaded machine it dips between rounds; the e2e waits for it
  on every signer, as it did for signer 0 (run 1 fell on a one-shot check of signer 2 at load average 29).
- **A program's refusal is named once, at the attempt** (`deliverTodo` wraps the error with `solErrName`), so the
  note, the parked alarm, the status's `parked`/`attempts` entries and the returned error all say "pausable ISM:
  OverMax" before the raw error (run 2 showed the parked entry carrying the code 0x7e4 alone).

## What passes

`go vet ./... && go test -race ./...` green (the new tests: `TestParseDecisionAndTheFloorFromTheHistory`,
`TestParseAdminAndPausableStateR1`, kind 12 and kinds 9/11's accounts in `TestSaltedCancelAndReserved`, the
chunked announcement and `NextSetTxs` in `TestVectors`, the record's signers in `TestRecoveryLogFold`, the
history-based cases of `TestExecuteWhen`, the next-set match and chunk in `TestStageMatches`; 68 vectors green).

The e2e's sections, in order (run 4, 2026-10-13, PASS in 582 s from nothing — under the 15-minute bound): the
build; the keys; the gno chain with the packages, GovDAO and the valoper realms at genesis, r/hlgov's kind-12
builder staged beside its copy; the Solana validator; `hlsol setup` twice (the programs built at factor 8 with
this run's launch values in all three `config.rs` files, the admin init with no predecessor, `finish_setup` with
the pausable flags; the second run sent nothing) and `hlsol audit` PASS before any realm; the seven realms uploaded
with `addpkg`; the launch through `ExecuteOrRejectProposal`; the machines (signer 0 signing on the cut "slot 122,
then height 87"; its status apart from the public endpoint); the same provider twice refused; 500 BUBBLE and 10
GNOT minted from real local headers under the pausable ISM's new `Verify` accounts (the state writable, the
Instructions sysvar); the 200 BUBBLE claim paid with the fee first, the backup idle; the locks equal to the
supplies; the monitor's identity lines holding with the record checked, no flag, the three signers signing; **a
30,000 BUBBLE send refused at delivery by the pausable ISM's 25,000 maximum — the relayer's status "failed
(pausable ISM: OverMax)" 6 s after the send, nothing minted**; the pre-armed proposal and the queued decision; the
kind-7 decision recorded pending at 21 hours, the executor waiting; **the kind-12 decision (BUBBLE: 50,000 a
message, 200,000 a day) delivered from the simulated `HandleAccountMetas` answer and recorded pending as decision
2, hlwatch showing "channel 0 #2 kind 12 (set token 1's mint limits: …)", BUBBLE's launch limits standing and two
pending accounts, the executor waiting on it**; the cancel of 1 (decision 3) executing at recording with its salt;
the primary stopped and the second cancel (decision 4) delivered by the backup after 81 s, the backup's walk
standing at the parked send (`gnoNext` 2); **the 30,000 send parked after three OverMax refusals (the alarm at the
sink), still unminted**; the watch key's one-stroke credits flag — deliveries paused, nothing staged, a burn
failing against the gno flag; a hold of two guardians with a dip, hlrecover's status (the recorded settled set of
1 from height 43, the client's newest settled entry the same, the hold history empty), body, **init with the body
in one chunk**, sign, submit refused (the stand-in key is not in the recorded settled set), record refused (no
window), close, **`hlrecover settle` finding nothing to settle, the updater's and the executor's `settle` lines
saying the same**; the second monitor through the lying provider with `solana:providers` and no verdict; the burn
parked after three failures, GovDAO's unpause and the keys' clears, the send made under the flag delivered (alice
at 590 BUBBLE), **the monitor's `sends` cleared**; **the validator-set change: the updater grew the client's body
region from 2,464 to 2,616 bytes for the set of two before announcing it, the announcement's next set staged by
`StageNextSet`, the client's quarantine (set from height 43, trusted height 544), the message dispatched before
the change re-emitted at height 577, the relayer seeing the quarantine**; **`hlsol audit` PASS with 24 RPC calls
and 49 verdict lines — the clock factors 8/8/8 agreeing, the origin domains agreeing, the recorded settled set
equal to the client's newest settled entry by hash, the hold history empty, both tokens' limits at launch values,
one pending account on the mailbox channel (the kind 12), the high-water mark 4 and the cancelled range 1-1, no
predecessor**; **hlwatch green from nothing** (the trusted hash matching the node's, 34 headers recorded and
consistent, BUBBLE HOLDS with 30,250 in flight out and 10 in, GNOT HOLDS, the recorded settled set with "nothing to
settle", the hold history empty, the limits, the kind 12 still pending).

The four runs allowed: run 1 (13:40 local) fell at the identity section — signer 2's `signing` flag was down for
the moment of a one-shot check (its last verdict two seconds old between one-second rounds; the Mac's load average
was 29): the script now waits for each signer; run 2 (13:58) passed everything through the backup section — the
OverMax refusal, the kind 12 recorded and shown, the cancels renumbered, the backup after 79 s — and fell at the
park check, which looked for the refusal's name in the attempt's raw error (the code 0x7e4 alone): the relayer now
names the refusal on the attempt itself, and the check accepts the code too; run 3 (14:11) passed through the hold
section's `hlrecover settle` and the updater's settle line and fell on the executor's settle line, a timestamped
note checked by prefix (the script bug fixed); run 4 (14:18) passed end to end in 582 s. No product-code failure
in the four runs; the one product change the runs asked for is the refusal's name on the attempt.

## Gaps left by M3e

- `realm/hlgov` carries no kind-12 builder: `bridge/local/hlgov_mintlimits.gno` is the stand-in the e2e stages
  into its realm copy; the realm lane adopts it (the file is written as the realm's builders are, and
  type-checks and tests beside hlgov with the pinned gno).
- The loop exercises no kind 9, 11 or 8 Execute (M4's 72-hour run on devnet: a successor deployed and initialised
  with `hlsol successor-init`, then GovDAO's kind 9 handing over; a reset after a 42-hour window; a kind 8 grown
  by the loader's `ExtendProgram`), no real `Settle` (a quarantine's end is 21 hours at factor 8) and no hold that
  ends (the 3-hour grace): the programs' suite covers each; the Go side's builders match the suite's byte for byte.
- `hlsol successor-init` is untested on a chain (the e2e has one admin program); its two steps are the suite's
  `init_successor` and the loader's burn.
- `BodyCapNeeded` is a bound on what the announcement and the switch add; a history eviction or a chain id
  change is not modelled (the slack covers both; a wrong guess only costs an extra `Grow` at `NeedsGrow`).
- The public checkpoint store and the roster comparator (Proposal 2), as before.

# M3f (2026-10-13): audit round 2's Go findings folded, M2e's format followed, the whole round-2 tree run

The findings are bridge/audits/r2/ (the Go audit's G13–G21, the conformance audit's K13–K16 and K21, the red
team's AO1, AO3, AO4 and AO5, the assumptions audit's F6, F7, F9 and F10); this section says what each became in
`bridge/` and what was read into the plan where it is silent. Branch `bridge-m3f` from `bridge` at 22d28c8,
carrying `bridge-m2e` (the programs after round 2, fast-forwarded) and `bridge` at 1a7ccf3 (plan v30, M1d's
realms, BUILD.md) merged in before the final run, so the branch's PASS is the round-2 tree's. One commit a
group, each with a test where a pure part exists (`agent/audit_r2_test.go`, `sol/loader_test.go`,
`sol/packet_test.go`, `gno/gasprice_test.go`, `cmd/hlwatch/main_test.go`, `cmd/hlsol/main_test.go`);
`go vet ./... && go test -race ./...` green; the e2e as below. PLAN.md is not edited here.

## What changed, by finding

- **G13 — the verdict's slot.** A signer keeps the Solana slot its last verdict's snapshot was taken at
  (`verdictSlot`, the lower of the two providers' slots as the cut takes it) and `signSol` leaves a dispatch
  finalized above it unsigned — the batch stops there, in provider 1's order — until a held cut whose snapshot
  is at or past its slot counted it as a burn in flight (AN3); it is signed one round later, never dropped.
  The status carries `verdictSlot`. Reading: the mint-and-burn of the scenario is caught because the burn's
  slot is above the snapshot that saw the supply before the mint; a burn at or below the snapshot's slot was
  in the supply the verdict judged.
- **K14 — the ahead-stop clears at T.** `applyAhead` marks the halt as the ahead-stop (`aheadHalt`), and a
  held verdict lifts it only at a height at or above the client's trusted height (`conclude`): a verdict at
  the head below T, inside the allowance, is one to sign on later, not the one §5.6 names. A broken identity
  halts at any height; a nonce conflict is never lifted.
- **G14 — the scan stops at the snapshot.** `ClientRecord.Sync` takes the snapshot's slot (`cs.Slot`) and
  folds nothing finalized above it, the cursor stopping before the first such transaction (the listing is
  newest-first; after the reverse, a break on `s.Slot > maxSlot`); hlwatch's `ReadSolana` passes the same. The
  monitor's `header:gap` sighting is dropped while the scan is "behind" too, so a fresh monitor's minutes of
  catching up never seed a flag. The e2e's burst (a dozen sends ten seconds apart, a header every tick) runs
  under it with no `header:gap`, no `header:mismatch`, no flag.
- **G15 — the step-7 rules with the cut's patience.** `Check` leaves an entry above the own node's head in
  `unchecked` (no mismatch: step 2 judges a client past the chain, with the allowance, a second source and two
  minutes); the record's mismatch flags only through `persisted("header:mismatch", cutFailAfter)` — an alarm
  before, "flagged once it has stood 2m0s" — and clears with its sighting; `foreignFindings` re-reads a number
  r/hlgov shows no body for at H at the own node's head before it is foreign, and stops there when the head
  holds it (a decision dispatched after H, judged at a later H; `checked` stays at n−1). hlwatch prints how
  many entries wait above the node's head. The lagging-own-node case is a unit test: the e2e has one gnodev for
  every machine, so a stall of it stalls the updater too and makes no lag.
- **G16 — no provider key leaves a machine.** `Redact` cuts every http(s) URL in a text to its scheme and host
  (the userinfo, the path, the query string and the fragment go: `?api-key=` and `/v2/<key>` alike) — in
  `Alarmer.raise` (the log line and the webhook), in every status snapshot (`statusBox.publish` and the
  signer's handler) and in every log line (`RedactWriter` on the three commands' log output). `providers` is
  off the status; the relayer's, monitor's and updater's `-listen` refuse a non-loopback address; the signer's
  public `-listen` refuses a non-loopback address without `-tls-cert` (G21).
- **G17 — a disputed dispatch.** The second provider's sight of an indexed dispatch is compared with the
  record's slot, amount and recipient (`foldDispatch`, `recordDiffers`; the signer's `record`); a difference
  marks the record disputed and alarms `solana:providers:<id>`. The monitor leaves a disputed record out of the
  cut and the identity (neither in flight nor credited-after), out of the diverted-credit check and the void
  match; hlwatch prints it UNCONFIRMED and its forged-id verdict says so, so `flag -ids` refuses. Every scan
  first reads a disputed record's transaction through both providers at once (`agreedDispatch`: the pair
  compares the slot, the bytes, the accounts) and agreed facts end the dispute. Reading for the signer: a
  dispute is a round with no verdict (§5.6's "two providers must agree"), alarmed, so an hour of it halts the
  signer; a disputed dispatch is not signed.
- **G18 — the extend has bookkeeping.** `extendOnce` runs under its own action key (`solext:<ch>/<n>`): the
  backup after its offset, a refusal backed off and parked after three deterministic ones like any call, the
  plan — the buffer's length past its header against the program data's, the bytes, the rent at 6,960
  lamports a byte — read and alarmed `execute:extend:<decision>` before the first send, the bookkeeping and
  the alarm forgotten with the decision.
- **F7 — one extension.** `sol.ExtendBy` is the whole excess rounded up to the loader's 10,240-byte minimum,
  or exactly the headroom below 10 MiB; the executor and `hlrecover execute` send one `ExtendProgram` for it,
  so a growth takes one block (the round-1 reading of "10 KB a block" was the BPF realloc's rule).
- **G19 / AO4 — the set against the client's bound.** `sol.SetSizeWarn` (40) and `sol.SetSizeBound` (about
  50): the updater (the current set and an announced one, the plan's included), the monitor and hlwatch's
  `watch` alarm `client:set-bound` at 40 or more validators ("the client's measured bound is about 50 through
  any history; the heap frame is due"); hlwatch's client line and `hlsol audit` print the count against the
  bound (the audit's line FAILS from 40, and notes gno.land's own count at the trusted height).
- **G20 — the compiled bindings.** `hlsol audit` reads each deployed ELF and FAILS unless the pausable ISM's
  bytes bind the mailbox and both token ids, the client's the mailbox pkgpath, the admin program's the mailbox,
  the tokens and r/hlgov's 32 bytes (`-hlgov`, the same flag `setup` takes); the admin program's ELF hash line,
  skipped by its branch of the loop before, prints now. Reading: the compiler lays a 32-byte constant out
  contiguously in .rodata or, where it compares it in place, as four `lddw` immediates (the pausable ISM's
  MAILBOX and the admin program's HLGOV_SENDER on the built ELFs; measured) — `ELFBindsKey` accepts either
  form, all four chunks required. The e2e runs one audit with `-so` at a corrupted copy and the wrong realm
  paths and expects the hash line and both binding lines to FAIL, then the real one to PASS.
- **G21 — hygiene.** A message found delivered closes its proof stage when the account still stands
  (`closeDeliveredStage`: a process confirmed after `Send`'s minute); `hlwatch flag -ids` refuses while the
  dispatch index errored on a provider; `hlrecover`'s `-auth-password` is gone (`$YUBIHSM_PASSWORD` only);
  "sequence" leaves gno's transient class for tm2's exact texts ("invalid sequence" and "verify correct
  account, sequence, and chain-id" — the second is the unauthorized error a sequence race or gnodev's lazy
  reload gives, a real race); `sol.Client.Send`'s confirmation wait selects on ctx.
- **K13 — hlwatch alarms its holder on the identity.** `watch`'s `diff` compares each route's verdict: a flag
  on BROKEN, an alarm on a short right side, the clear once it holds.
- **K15 — both counters.** `hlsol audit -hlgov <pkgpath>` reads r/hlgov's `json` beside the admin program's
  last number and prints both and the gap (an ok/FAIL line; a realm not yet on the chain is noted, as
  hlmailbox's absence is).
- **K16 — kind 8 and 9 read on Solana.** hlwatch parses an r/hlgov upgrade or transfer proposal's text for its
  program ids and reads their accounts: a kind 9's successor "immutable" or "SUCCESSOR … MUTABLE (upgrade
  authority X): kind 9 would be spent — burn it first"; a kind 8's program (upgradeable by the admin program,
  immutable, or another's) and buffer (staged, or awaiting its writer's stage upgrade).
- **K21 — a realm's address refused.** `hlsol send -config bridge.json` refuses a `-to` that is the package
  address of the mailbox, the fee realm, r/hlgov or a route (`realmOf`); without `-config` it knows no realm.
- **AO1 — the tool executes.** `agent.ExecuteDecision` reads a decision, judges its clock as the executor does
  (`executeWhen`) and sends one Execute with the kind's accounts (`ExtraFor`, a cancelled upgrade's return)
  from any funded key; a `NeedsExtend` is answered with one `ExtendProgram` for the whole excess and the caller
  runs it again next block. `hlrecover execute -channel C -number N -payer KEY.json`; the help and `status`
  name it as the published tool's duty.
- **AO3 — settle:due.** The monitor alarms `settle:due` (once an hour on the webhook, every poll in the log)
  while the client's newest settled entry, both holds read, is newer than the record and the setup is done;
  cleared when the record catches up.
- **AO5 — OverDayCap retried on the bucket.** A delivery parked on the pausable ISM's `OverDayCap` is retried
  when the day's bucket — its level now at finalized, plus what this pass already delivered — has room for its
  amount (`bucketRoom`), oldest first (the walk's order), and not before, whatever the hour; an `OverMax` park
  keeps its hourly retry.
- **F6 — the packet.** `NextSetChunk` 23; the precommits travel in chunks: as many beside the init as fit,
  measured on the serialized transaction (`TxSize`; a real header and a merkle proof take most of the packet),
  the rest 40 a transaction through the offset `StagePrecommits` takes, and `driveStage` skips a chunk whose
  slots the stage already holds (the program refuses a slot filled twice). `TestStagedTxsFitThePacket`
  serializes every builder's transaction for 6, 17, 18, 60 and 255 validators and asserts each is at most
  1,232 bytes, each slot and next-set entry staged once. A finding of the measurement: a proof's `StageInit`
  alone — the header, its BlockID, the round and a merkle proof — exceeds the packet past seven aunts, a block
  of more than 128 results; `ProofTxs` answers `ErrPacket` then, and the relayer asks a Reemit for it as it
  does for a leaf too big (the fresh block is smaller; a second one is not asked), a reading noted in the gaps.
- **F9 — the price and the gas.** `gno.Client.Call` runs gnokey's simulation first under the caller's gas as
  the ceiling (four times it when the ceiling itself runs out), takes the measured gas with a fifth's margin
  and a million for the broadcast (`GasFor`), and prices the fee at the higher of the client's own price and
  the chain's block gas price read from `auth/gasprice` before the call (`BlockGasPrice`, the three forms the
  node may answer parsed); "insufficient fee" and "out of gas" are transient — re-priced and re-measured next
  call, never parked. The relayer's `-gno-gas` is now the simulation's ceiling, not the gas sent.
- **F10 — the day cap from the expected lock.** `hlsol setup -expected-lock-gnot N -expected-lock-bubble N`
  (base units, required) writes `DEFAULT_DAY_CAP` = min(100,000 × 10⁶, lock ÷ 10) into the pausable ISM's
  config.rs and prints it. The program keeps one constant for both tokens (processor.rs:108 gives both the same
  `MintLimit`), so the smaller of the two tokens' values is compiled — said here, as the addendum asked; a
  per-token constant is the programs lane's if the plan wants the two apart. The e2e passes 1,000,000 of each,
  whose tenth is above the ceiling, so its sections' 100,000 stands.
- **M2e's format (S-08) followed.** `StageInitArgs.Round` is written after the BlockID and read by
  `DecodeStageInit`; `UpdateTxs` and the header form of `ProofTxs` pass the commit's round (`gnolight.Commit.
  Round()`, the first present precommit's, as tm2's `Commit.Round`), the ring form 0; `ParseStage` reads the
  stage's round; `stageMatches` compares it; `ClientError` names `RoundMismatch` at 36; the four
  `client_stage_init_*` vectors regenerated from bridge/tools/vectors (its four literals carry `round`) and
  verified byte for byte (68 vectors). S-10's contract is mirrored: the record keeps the client's ring of header
  hashes (an accepted update pushes its header, a set switch — an update whose ValidatorsHash differs from the
  last accepted one's — clears it, a reset clears it, 256 entries kept), and a header-based proof is folded
  only when its header is not in the ring (`TestClientRecordRingRule`).

## Readings where the plan or the task is silent

- The signer treats a disputed dispatch as a round with no verdict (an hour of it halts), where the monitor
  skips the record: the signer's rule is a stop, the monitor's a flag that must rest on two agreeing readings.
- `solana:providers:<id>` is the dispute's own key (the monitor clears `solana:providers` at every agreeing
  snapshot, which would post a clear and a raise each poll).
- A disputed record is re-read through the pair by the transaction signature it was first seen in; a record
  from before this change has none and stays disputed until a human looks (fresh deployments have it).
- The redaction keeps nothing past the host: a signer URL's path and a provider's path token go alike.
- `GasFor` adds a fifth and a million: gnokey's broadcast meters what the simulation did plus the storage
  deposit events; the local run's calls land under it.
- The audit's set-bound line is a FAIL line from 40 validators, so a member's pre-vote audit says it in red.
- A proof by header of a block of more than 128 results is a Reemit, not a failure (F6's measurement).

## Gaps left by M3f

- The pausable ISM's day cap is one constant for both tokens; F10's per-token cap needs a program change.
- A proof by header past seven aunts asks one Reemit; a second block as large stays stuck until a hand
  re-emits (the leaf-too-big case has the same shape).
- The lagging-own-node case of G15 is a unit test, not an e2e section (one gnodev serves every machine).
- `hlrecover execute` and `settle:due` are exercised by refusal and by a unit test: no decision matures and no
  quarantine ends inside a run.
- The public checkpoint store and the roster comparator (Proposal 2), as before.

## What passes

`go vet ./... && go test -race ./...` green on the merged tree (the new tests: `TestSignerWaitsForTheVerdictSlot`,
`TestSignerAheadStopClearsAtT`, `TestSignerDisputedDispatch`, `TestIndexDisputedDispatch`,
`TestRecordSyncStopsAtTheSnapshotSlot`, `TestClientRecordAboveTheHead`, `TestClientRecordRingRule`,
`TestMonitorMismatchPersists`, `TestForeignFindingsRereadsAtTheHead`, `TestRedact`, `TestSetBoundText`,
`TestExtendOnce`, `TestBucketRoom`, `TestMonitorSettleDueAlarm`, `TestClosesDeliveredStage`,
`TestGasRefusalsAreTransient`, `TestExtendBy`, `TestELFBindsKey`, `TestStagedTxsFitThePacket`, `TestGasPriceParse`,
`TestWatchAlarmsOnTheIdentity`, `TestProposalProgramIDs`, `TestRealmOf`, `TestLaunchDayCap`; the vectors at 68).

The e2e's sections, in order, as the passing run had them (run 5, 2026-10-13, PASS in 775 s from nothing on the
merged tree — M1d's realms, M2e's programs, this branch's Go): the build; the keys (bob's among them); the gno
chain; the Solana validator; `hlsol setup` twice with the expected locks, the day cap compiled at 100,000; the
first audit PASS before any realm, every ELF binding ok and r/hlgov noted as not yet on the chain; the realms
(r/hlgov's own kind-12 builder, nothing staged beside it); the launch; the machines; the same provider twice
refused; 500 BUBBLE and 10 GNOT minted; `hlsol send` refusing the route realm's address, then the 200-BUBBLE
claim paid with the fee first (the relayer's Process, Claim and Execute at the simulation's gas and the chain's
price, F9); the locks and the identity lines; **the burst — twelve 200-BUBBLE sends ten seconds apart to bob,
2,400 minted, 17 headers submitted by then, the monitor's record 24 recorded and 24 checked, no `header:gap`, no
`header:mismatch`, no flag** (G14); the 30,000 send refused by the pausable ISM's maximum; the pre-armed
proposal and the queued decision; the kind 7 and the kind 12 recorded pending (the kind 12 from r/hlgov's own
builder now); the first cancel at recording with its salt; the backup's second cancel after its offset; the
30,000 send parked; the watch key's one stroke with nothing staged and the burn failing against the gno flag;
the hold with the dip, hlrecover's status, body, init, sign, submit refused, record refused, close, settle, and
**`hlrecover execute` refusing the kind 12 with the executor's own reason (deferred by the hold that stands)
and naming decision 1 as gone** (AO1); the disagreeing provider; the burn parked and the unpause; the
validator-set change under M2e's round (the chunked announcing update, the quarantine, the re-emit); **the
audit that must FAIL — a corrupted `pausable_ism.so` beside the others, the wrong mailbox pkgpath and the wrong
r/hlgov: the hash line, the client's and the admin program's binding lines and the counter gap FAIL, the
mailbox binding and the client's hash stay ok** (G20); the real audit PASS with the six binding lines, r/hlgov's
counter at 4 against 4 (K15) and the set of 1 or 2 against the bound (G19); hlwatch green from nothing with the
set size against the bound, no SET BOUND line, no entry left above the node's head.

The runs: run 1 (16:0x local) fell at the first audit on the admin program's binding line, which the loop's
own branch skipped — the admin program's ELF was never hashed or bound-checked before (fixed: ef44cb4); run 2
passed everything through the hold section's settle lines and fell on the script's wording for `hlrecover
execute` (the kind 12 is "deferred by the hold" at that point, not "matures at"; either reason accepted now);
run 3 passed through the final audit — PASS, 0 FAIL lines — and fell on the script's exact set-size wording (the
audit reads at finalized while the client still holds the set of 1 with the set of 2 announced; either side of
the switch accepted now); run 4 died 17 s into the backup section when the Mac's disk filled — the Solana test
validator's own log says `Unable to save tower to storage: No space left on device` at 23:17:21Z, every
machine lost the RPC from then on (the sink's `signer:no-verdict` lines), and the same event emptied the
session's scratchpad (runs 1–3's logs with it; this file's text was committed before); run 5, with 11 GiB freed
and the disk watched, PASS in 775 s. No product-code failure in the five runs but run 1's audit branch. The
passing run's log is kept at ~/.config/bubblerumble-jarvis/bridge-e2e-logs/m3f-run5.log, the validator's
disk-full lines beside it.

# M3g — audit round 3 folded (2026-10-13), branch `bridge-m3g` from `bridge` 02d8635

Round 3's Go findings (audits/r3/audit-r3-go.md, G22–G27), the red team's Go items (audit-r3-redteam.md: AP1,
AP2, AP3's Go half, AP5, AP6, and the fact check's updater floor and audit line), the assumptions audit's F14 and
F15 (audit-r3-assumptions.md rows 9–10, 30) and the Go half of the programs lane's round (M2f: the Stage and Body
formats of S-16, the kind-9 check of S-15) are folded here, one commit a finding or a coherent group, each with a
test in the packages' style (`agent/audit_r3_test.go`, `sol/s16_test.go`, `sol/loader_test.go`,
`gno/gasprice_test.go`, `cmd/hlwatch/main_test.go`, `cmd/hlsol/main_test.go`), `bridge-m2f` (65831ab, the
programs after round 3), `bridge-m1f` (cd01f04, the realms after round 3) and `bridge` (9c73f0f: plan v31 and
M1f's merge) merged in before the e2e, so the branch carries the whole tree. PLAN.md is not edited here (AP3's
§5.3/§6.5 words, AP5's §6.5 rent sentence and AP6's §5.4 cap wording are the plan lane's). The e2e script gains
round 3's checks; the realm lane changed no e2e section, and no Go reads its new renders (`importing`,
`imported`, `successors`, the sweep-by-id and migrate builders), so the merge asked nothing of the Go but one
reading, below.

## What changed, by finding

- **G22 — a complete scan stands at the snapshot.** `ClientRecord.Sync` sets `ScanSlot` to the snapshot's slot
  once every listed transaction at or below it is folded (the break on the first transaction above it included:
  everything at or below is folded then too); a scan that errors midway keeps the last folded slot and stays
  "behind". So a client counter above the rebuilt record is "an unfillable gap" and §5.9's gap flag can fire
  through `seenFor(cutFailAfter)` — before, the scan's slot was the last transaction's, below the snapshot on
  nearly every poll, and the record read "behind: N of N+1" for good. The bounded-scan test's expectation moved
  with it (the second scan stands at 20, the snapshot, not 14, the last transaction).
- **G23 — an erroring head re-read is no finding.** `foreignFindings` returns the error with `checked` unchanged
  when the re-read at the head errors (a closed keep-alive connection, a timeout on that one call), so the poll
  is skipped at that number instead of calling GovDAO's own decision foreign and flagging both Solana objects;
  the monitor's foreign flag goes through `persisted("decision:foreign", cutFailAfter)` as the record's mismatch
  does — `decision:foreign:pending` alarms before, the numbers checked advance past a finding only when it is
  flagged (examined once, as before; the flag on the objects is the record and the monitor never clears it), an
  erroring poll raises and advances nothing and keeps a standing sighting, a clean poll clears the pending alarm
  and the sighting; the status carries the findings.
- **G24 — the signer's status walks nothing the loop writes.** The loop publishes the index's counts (indexed,
  unsigned, disputed) under the mutex after each scan and each signing batch; the status serves the copies. The
  test reads the status beside the loop under `-race`: red on the old walk (5 reports), clean now.
- **G25 — the redaction reaches the outsiders' tools.** hlsol's log output goes through `RedactWriter`; the
  audit's curl lines take `$RPC` and its header names the provider's scheme and host alone; hlwatch's status text
  is redacted on its way out (`SolErr`, the not-read and unconfirmed lines carry solana-go's errors, URL
  included). The e2e checks no curl line carries `http` and the header names the host.
- **G26 — kind 8/9 reads and the seat through both providers.** `agent.TwoProviders` and `ErrProviders` are
  exported for the commands; hlwatch's `authorityOf` and the one-stroke flag's `SeatOnSolana` read through the
  pair and say "UNCONFIRMED (the providers disagree)" on a disagreement (the flag command refuses then); tested
  against two fake JSON-RPC providers, one lying.
- **G27 — hygiene.** `forgetGone` drops the solexec:/solext: bookkeeping and the parked, extend and refused
  alarms of every decision no longer pending; `attempt.parked()` and the four park sites re-raise `parked:` on
  each hourly retry's failure (the Alarmer posts it hourly, the log sees each); `clearPersisted` drops a
  persisted condition's sighting with its alarm for solana:providers, gno:own and cut, the header siblings
  rewritten to it; `stalls` skips a disputed record; `realmOf` compares bech32 case-insensitively.
- **AP1 — the exact identity, and the credited-id rule.** `Identity.Verdict` is BROKEN when the right side with
  what is in flight exceeds the lock — `SignerStops` is the same sum now, the signer's in-flight-out being 0 —
  and the short side keeps its tolerance; the round-1 assertion of the tolerant reading is reversed (a forged
  mint burned back is BROKEN to the monitor too). Reading: over the ordered cut the amounts in flight are exact
  (a send locked and not yet minted at the slot, a burn dispatched at or before the slot and not yet credited at
  the height), so right + in flight = lock to the unit, and any excess is a credit nobody burned for; a lag only
  ever shortens the right side, which stays alarm-only after three polls. The monitor's `unindexedCredits` walks
  each route's waiting claims (`claims/<from>`, ten pages a poll) and, for a verifier credit whose id neither
  provider's index holds, flags credits and payouts with the pausable ISM as a break does once the id has stood
  one more poll — only when both scans succeeded with nothing skipped (G21), a voted claim never, an id that
  turns up in the index clearing its sighting and alarm. hlwatch prints FORGED CREDIT lines from the same
  reading and flags its holder; the e2e's green section refuses them.
- **AP2 — the day cap's reservation.** `capParks` finds each token's oldest OverDayCap park in the walk and
  `capGate` holds every younger message of the token unless the bucket has room for the park's amount beside
  its own (a younger park waits behind the older one too); the park itself and anything older go as before;
  the reservation drops once the park is delivered and counted in the pass. Reading: §5.7's "a large send may
  hold younger ones for amount ÷ cap of a day" is now the mechanism, not a hope.
- **AP3 — Retired, and the recipient switch.** The relayer's `deliverFailed` sorts the admin program's Retired:
  the message is done as "retired", never parked for retry, alarmed once naming the re-dispatch after r/hlgov's
  switch, and the Reemit rule skips it (a fresh Dispatch to the retired program is no help); NotInherited keeps
  the attempt path (deliverable once the kind 9 lands, so its hourly retry is right). hlwatch reads the admin
  program's successor and, when r/hlgov dispatches to another program, whether it inherited, and prints and
  alarms RECIPIENT SWITCH findings: a `set decision recipient` proposed or queued while the kind 9 is unexecuted
  (no successor, or another), RETIRED while r/hlgov still dispatches to a program that handed over, NOT INHERITED
  for a successor before its predecessor's kind 9.
- **AP5 / F15 — the growth's rent at the chain's rate.** `sol.ExtendRent` is the rent-exempt minimum at the new
  length less the programdata's lamports, read live; the compiled 6,960 (the test validator's rate; mainnet's is
  5,080) is gone. The executor's extend alarm, hlrecover execute's preview and hlwatch's kind 8 line (the program
  data's and the buffer's bytes with the growth's rent beside the authorities) print it; hlrecover execute prints
  the growth's size and rent before sending and refuses above `-max-rent` (0.5 SOL) without `-yes`
  (`ExecuteDecision` takes `ExtendTerms`; `extendPlan` is the pure part).
- **AP6 — the cap against the lock.** `capRatioText` alarms `mint:cap-ratio:<token>` (the monitor) and prints
  CAP RATIO (hlwatch, with the lock on the limits line; the watch alarms `cap-ratio:<token>`) while a token's
  day cap exceeds a fifth of its lock on gno.land — the lock from the route's counters at H, the cap from the
  pausable state through both providers. The e2e's run is the launch week's own picture (a 100,000 cap over a
  lock of thousands), so it asserts the line and the alarm stand.
- **The updater's floor.** `plannedPerDay["updater-sol"]` is 144 headers × 4 transactions × 5,000 lamports a
  day (0.00288 SOL, 0.3 SOL ≈ 104 days ≈ 3.4 months, §5.5's figure); the "0.3 SOL over five months" sat 30% low.
- **The audit's cap line.** `hlsol audit -expected-lock-gnot/-bubble` prints each token's day cap beside the
  lock it was compiled from, as a share of it; the setup's own audit passes them; the e2e asserts "10.0% of it".
- **F14 — a node's own minimum fee.** `gno.Client.Call` reads the ante's second refusal ("fee required: N with
  … as minimum gas price set by the node"), pays N once when under the bound — `gno.maxNodeFee` in the config, or
  by default the greater of ten times the fee the call carried at the block price and 1 GNOT — and otherwise
  refuses with the node's text in a form `Deterministic` parks (every "insufficient fee" was transient before: a
  node minimum above the block price retried on backoff for ever). gnodev sets no minimum, so the e2e is
  unchanged by it.
- **A paid sweep entry (M1f).** A credited dispatch whose `claim/<id>` answers null at H is gone from the realm
  — a sweep entry paid by GovDAO's sweep-by-id decision (null once paid, after M1f), or a claim migrated to a
  successor — and `divertedCredits` marks it checked with nothing to compare, never a break (it read null as a
  lag before, though hlmailbox's mark and the route's claim land in one transaction and the mark was read at H,
  and re-read the id every poll for good); a voted payment is GovDAO's, as a voted credit is.
- **M2f's layouts (S-16) and S-15's Go half.** `ParseClientState` reads the body's `ring_epoch` (8 bytes after
  `running_hash`) and `ClientBody.Size` counts it, so `BodyCapNeeded` follows; `ParseStage` reads the 48-byte
  by-ring copy after `leaf_filled` (the epoch, the entry's height, its results hash); the old forms short of them
  are refused; no Go reads a leaf past a stage's bytes (the leaf chunks are read from the transactions). The
  vectors re-verified (68, unchanged: no instruction moved). hlwatch's kind-9 check refuses a successor that is
  the admin program itself (Execute spends a self-transfer) and reads the successor's state before "immutable
  (kind 9 can execute)": no state, a predecessor other than this admin program, or inherited already is named.

## Readings where the plan or the task is silent

- The monitor's foreign flag advances the numbers checked once it is raised and never clears the flag alarm
  (the chain flag is the record; GovDAO's cancel and sync sequence are the cure); a finding re-found each poll
  through the two minutes before it is the price of the patience.
- A credited id is called forged by the monitor only when both scans of this poll succeeded with nothing on a
  side list, and after one more poll; a provider's finalized view a few slots behind the delivery is a lag.
- The day-cap reservation reserves the oldest park's amount alone; a second park of the token waits behind it
  (oldest first) and reserves nothing itself.
- A Retired message advances the relayer's walk past its nonce; the decision reaches the successor as a new
  message after the switch, never as a re-emit of the old one.
- The growth's rent is a number read from provider 1 for the vote (hlwatch) and from the relayer's own provider
  for its alarm; no flag rests on it.
- mint:cap-ratio keys by token (`mint:cap-ratio:BUBBLE`), as the identity's alarms do.
- `hlwatch flag`'s seat check and the kind 8/9 reads say UNCONFIRMED on a disagreement and give no verdict;
  the other sections keep their "shown from provider 1 alone" reading.
- F14's default bound reads the fee the refused call carried from the node's own text, so no second price read
  is needed to size it.

## Gaps left by M3g

- AP4 and AP7 are the realms lane's; AP3's and AP6's plan words, and AP5's §6.5 sentence, are the plan lane's.
- The e2e exercises none of: a forged credit, a Retired delivery, a recipient switch, a day-cap park with a
  younger send, a node minimum fee, a kind 8 growth — each is a unit test (the run has no second admin program,
  no OverDayCap, gnodev has no minimum gas price, no buffer outgrows a program).
- A disputed record from before G17 (no `Sig`) still stays disputed until a human looks (M3f's gap).

## What passes

`go vet ./... && go test -race ./...` green on the merged tree (the new tests: `TestRecordCompleteScanStandsAtTheSnapshot`,
`TestForeignRereadErrorIsNoFinding`, `TestSignerStatusUnderTheLoop`, `TestSolExecutesForgetsGoneDecisions`,
`TestParkedAlarmRepeats`, `TestMonitorClearsSightingsAndSkipsDisputed`, `TestMonitorFlagsAnUnindexedCredit`,
`TestDayCapReservation`, `TestRetiredIsNotParked`, `TestExtendPlan`, `TestCapRatio`, `TestExtendRent`,
`TestClientStateAndStageCarryTheRingEpoch`, `TestNodeMinFee`, `TestAuditLinesRedactTheURL`,
`TestStatusTextRedactsURLs`, `TestProgramChecksNeedBothProviders`, `TestRecipientSwitchNotes`; the identity,
hlwatch-alarm, extend-once, bounded-scan, realm-of and runway tests extended; the vectors at 68).

The e2e (bridge/local/e2e.sh from nothing on the whole merged tree — M1f's realms, M2f's programs at factor 8,
this branch's Go): **PASS in 1071 s**, run 1 (2026-10-13 01:43–02:01 UTC; `== PASS in 1071s (1071s)`, exit 0),
the four FAIL lines being the negative audit's expected ones (the corrupted pausable ISM's hash, the client's
and the admin program's binding lines, the counter gap). What round 3 added to the run and what it saw: the
first audit's curl lines take `$RPC` and its header names the host (G25; 24 calls printed on the final audit);
the final audit takes the expected locks and prints each day cap beside them (`100000000000, 10.0% of it`); the
burst under G22's complete scan — 39 headers recorded and 39 checked at its end, no `header:gap`, no mismatch,
no flag; the identity lines "holds" with gap 0 under the exact rule, in flight 0 out and 0 in at the cut; the
monitor's alarms through the run: `mint:cap-ratio:BUBBLE` and `:GNOT` standing from the first poll (AP6: a
100,000 cap over locks of 33,250 BUBBLE and 10 GNOT — the launch week's own picture), the runways, `parked:` for
the 30,000 send and the burn, `deliveries:paused` under the flag and again under the quarantine, monitor 2's
`solana:providers`; hlwatch green from nothing with the lock on each limits line and both CAP RATIO lines, no
FORGED CREDIT, no RECIPIENT SWITCH; the alarm sink carried no flag-level key at all (the unindexed-credit rule
raised nothing on the run's real claims). The S-16 layouts were exercised by every staging of the run (the
relayer's proofs by header and by ring, the updater's updates, the set change's cleared ring and moved epoch).

The runs: the realm lane's whole-tree run (`m1f-run1.log`, PASS in 2097 s under the Mac's load) held the
ports first; my queued runner launched run 0 on the not-yet-merged tree the moment that run ended, and was
stopped 40 s in, at the setup step, so the one budgeted run would be the whole tree's
(`m3g-run0-aborted-unmerged-tree.log`); run 1 then went from nothing with everything merged. No product-code
failure. The passing run's log is kept at ~/.config/bubblerumble-jarvis/bridge-e2e-logs/m3g-run1.log.

# M3h — the Go after audit round 4 (2026-10-13; branch bridge-m3h, merged as b24c9e9)

Written by the coordinator from the lane's report and its fifteen commit messages (the lane left no section; the
round-5 fact check asked for one, C352). Base a3d647b; `bridge` merged twice (M2g's programs, then M1g's realms);
the one e2e on the merged tree passed in 872 s (m3h-run1.log). Vet and race green.

## What changed, by finding

- G28 (high): every `getTransaction` pinned `MaxSupportedTransactionVersion` to 0; a v1 transaction (SIMD-0385, on
  mainnet since 2026-09-15) was unreadable to every scanner. Now 1 at both sites; a "not supported by the
  requesting client" answer is named BUILD GAP in the skip's alarm, never a transient (`TestReadsAVersionedTransaction`
  folds a real v1 envelope).
- G29: a `Sync` error persisting two minutes alarms `record:scan`; hlwatch alarms `not-read:<section>`; a fresh
  install may start from a checkpoint — `-record-from client` on `hlbridge monitor` and `hlwatch` seeds the record
  from the client's counter, running hash, ring and set hash and the last ≈33 minutes of stages. Reading: everything
  at or below the base is passed over, unaudited — the client's own word; an outsider's first run should start from
  the deploy or from a record copied off a trusted run (the plan says so from v33).
- G30: `afterPoll` marks the identity stale and alarms `monitor:no-verdict` after two minutes of failed polls.
- G31 + AQ6 + S-19: `successor_sol.go` — `followSuccessors` reads the admin program's `successor` from the chain
  (≤4 hops, loops refused) and every tool acts on the live program: the monitors, hlwatch, the relayers, the
  executor, hlrecover (which refuses to stage a recovery on a program whose state names a successor); the
  one-stroke flag covers the pausable ISM and the live admin program, and the retired states where the key holds a
  seat, in a second best-effort transaction; `classify` reads the line — a decision to a program the line does not
  reach is `kindOtherAdmin`, left undone with the alarm `decision:other-program`, never "other".
- G32: a 7-day decision r/hlgov holds at n with no account on the admin program inside its week, outside the
  cancelled ranges and any later r/hlgov cancel, is a foreign finding ("a foreign decision took it").
- G33: hlwatch's `view` reads in §5.9's order — the snapshot first, then `HeadAfter` with `JudgeOwnHead`'s refusal
  of a catching-up, stale or not-after head (`watch:no-verdict`); FORGED CREDIT is printed and alarmed on its
  second consecutive poll (`ForgedSeen` in the state file).
- G34: the claims walk resumes where the last poll stopped and wraps (`ClaimsFrom` per realm in the state), so
  every waiting claim is visited within ⌈N/1,000⌉ polls.
- G35: a park's cause lives in `attempt.Cause` (kept through transient retries); the "required (one of)" refusal
  parks; `CallError` prints the REFUSING head first; `credit:unindexed`, `diverted` and `decision:retired` alarms
  end with their cure; `AlarmEvery` gives `mint:cap-ratio`, a refunded send in hlmailbox and `admin:successor` a
  daily cadence; `gno:own-lag` is raised where it was only cleared.
- F19: `CancelPairsFor(..., fits)` drops pending pairs until `sol.Fits` says the transaction fits the packet (about
  seven with distinct deliverers for a delivery, more for a record); the relayer's process and hlrecover's record
  each measure their own. The dropped decisions are stored cancelled by the program and closed by anyone's
  `CloseCancelled`, which the executor sends.
- AQ1: the relayer's `deliveryTarget` delivers a burn addressed to a migrated route through its successor's
  `Process` once `imported > 0 && !importing` (the realm's `MustHaveImported`); until then the message waits —
  never attempted, never parked, the status says why; claims and executes follow the successor; the route json is
  read once a step.
- AQ3 + K28: `migrate.go` finds a token's pair three ways (`migratedTo`, the config's `successor`/`predecessor`,
  the mailbox's registered routes) and judges the identity over the summed lock, owed, voided and lost; a break
  flags the successor (the live realm), never a migrated or importing route alone; `routeJSON` gains `Importing`
  and `Imported`; hlwatch prints "IMPORTING (n pages)" and a MIGRATION line; the alarm `route:importing:<symbol>`
  stands on both.
- AQ2: the monitors and hlwatch read `send/<id>`'s `refunded` mark per unprocessed send each poll (exact, few
  reads) and drop a refunded send from in flight — never a break; hlwatch prints REFUNDED for one that still sits in
  hlmailbox (an alarm per send, daily); the relayer reads the mark before staging and ends such a send as
  "refunded", out of the Reemit rule.
- AQ4: a park whose amount exceeds the live per-message maximum or day cap after a lowering kind 12 reserves
  nothing, is re-marked OverMax and is not staged until a raise.
- C341: `routes[].expectedLock` in the monitors' and hlwatch's config; `capRatio` is quiet while a token's lock is
  below the expectation the cap was set against, judges from the poll the lock reaches it and for good after
  (`CapReached` persisted; a lock that later shrinks below five caps alarms); the e2e's configs carry it and the
  green section asserts the quiet.
- M1g's shapes: `send/<id>`'s `imported`, the routes' `refunded`/`refundedTotal`, the mailbox's
  `routes[path].predecessor/successor` and `successorRoutes`, r/hlgov's `former`; hlwatch reads a migrate proposal
  against the mailbox ("announced for this route" / NOT ANNOUNCED / "announced for the other route" / "registered
  already") and prints successions, announced successors and former recipients.

## Readings where the plan or the task is silent

A checkpointed record audits nothing before its base (above). `successorHops` is 4 against the realms' chain of 16:
a fifth successor returns the tools to a config edit. The forged-credit latency is two polls plus ⌈N/1,000⌉ over N
waiting claims. The monitors read `send/<id>` per unprocessed send rather than the `refunds/<from>` page. A cancel
beyond the packet closes the rest by `CloseCancelled`, so the rent returns there, not at the cancel. `AlarmEvery`
is daily for the three standing conditions named above, hourly for parks.

## What passes

`go vet ./... && go test -race ./...` on the merged tree; the e2e from nothing on the whole round-4 tree
(cbce742 = b24c9e9's tree): `== PASS in 872s`, with hlwatch's "cap ratio quiet" lines for both tokens, no
`mint:cap-ratio` alarm, `stale` empty, migrations `{}`, and no `record:scan`, BUILD GAP or `decision:other-program`
line anywhere. Not exercised by the e2e (unit tests only): a migration, a refund, a successor admin program, a v1
transaction, a checkpointed record.

# M3i — the Go after audit round 5 (2026-10-13; branch bridge-m3i from bridge 7d37640)

Five parts, by message: the red team's Go items (AR1, AR3–AR6), the Go audit's G36–G45, the programs audit's
S-20/S-21 on the Go side, the assumptions audit's F21–F24, the conformance audit's K34–K37. One commit a
finding where the fix stood alone, one for two findings on the same lines (AR5+G43, G42+F23, G38+F24+K35,
G36+G37+F21+G45's checkpoint item). `bridge` merged twice: M2h's programs (5e5ddd0: the un-inherited
successor's NotInherited, the State layout pin), then M1h's realms (e003ee7: the enrolment note, the write-off,
the refund's 7-day wait, the re-pointing migrate). Vet and race green; the one whole-tree e2e is the last
section.

## What changed, by finding

- AR6 (G41): `migrate.go` follows a line, not a pair — `migratedTo` (or the mailbox's `successor` entry) from the
  configured path to the chain's end, sixteen hops as hlmailbox's `maxChain`, a loop or a longer line refused,
  and back to the line's first realm by the mailbox's `predecessor` entries; the identity sums every realm of
  the line, the relayer delivers a burn addressed to any realm of it to the last once its import ended, claims
  there, walks every realm's decisions, reads a record by id on the live successor first (`Others`). The relayer
  reads the mailbox once a step for the entries (`resetStep`, `mailboxState`); `migrationOf` takes a route
  reader, so the relayer's per-step cache serves it. Reading: a configured realm in the middle of a line still
  yields the whole line.
- AR3: a burn addressed to the live successor itself waits on `!mig.Ended()` as one addressed to a migrated
  realm does — never delivered before page 0 (the realm would sweep it) nor while pages arrive.
- G44: `migrationsOf` tries every route and answers the unread lines by symbol; the monitor's `readMigrations`
  keeps them for the poll, `judgeIdentities` marks such a token "no verdict" (the status says NOT READ), and
  `lineUnread` makes the cap-ratio, diverted-credit and unindexed-credit rules skip it, marking nothing; the
  other token, the stalls and the runways run; hlwatch names the error per token.
- AR5 + G43: `capRatio` takes the quiet's end by the clock — `capQuietEnds`: hlmailbox's `launchedAt` plus the
  realms' 14-day deadline (`Clocks.Deadline`, at the chain's factor) — and reaches at five day caps too; the
  standing persists as `CapReached`. The note keeps its C341 prefix (the e2e greps it) and names both bounds.
- AR4: `hlsol setup` compiles `DEFAULT_MAX_PER_MESSAGE` as min(25,000, the day cap) (`launchMaxPerMessage`) and
  refuses an expected lock whose tenth compiles that below the realms' maximum (`launchMaximumCheck`; the realms'
  figure is their source's 25,000 or `-launch-maximum`); the audit prints the maximum beside the lock and the cap
  and fails one below the realms'. Reading: the e2e raises hlbubble's maximum to 50,000 in the STAGED realm after
  the setup (AN5), which the setup cannot see — it passes `-launch-maximum` nothing and the check holds at 25,000.
- G42 + F23: `readSends` keeps every message's id (read once), fetches the processed records by 100 through the
  pair (`solProcessedMany`), reads a refund mark only where one can be set (`refundable`: a successor or
  migrated route) and only for a send never read or once the token's refund count — the line's sum, persisted
  as `refundCounts` — rose; the relayer reads the mark after the cap gate for the message about to be staged,
  none for a live route. Measured by the test: 250 sends, the second poll makes 0 renders and 3 account reads.
- AR1 (the Go half): hlwatch reads a successor's refund proposal (`refund … of sends imported from the
  predecessor … to their senders: ids`) against the mailbox's enrolment note — NOT ENROLLED, or ENROLLED UNDER
  THE 7-DAY DELAY (the block's time read by height when the note names no time) — alarmed `refund:proposal:<id>`
  while the proposal stands, and prints DELIVERED beside any id Solana processed; the monitors and hlwatch alarm
  a refunded send Solana processed after its refund as the standing `refund:delivered:<id>` (daily; REFUNDED AND
  DELIVERED on the status), its record read again once a minute for the `writtenOff` mark that ends it. The
  enrolment note is read tolerantly (`enrollMark`: a number is a height, or a time past a billion; an object
  with height/at) and M1h's `enrolledAt` beside it (`routeFlags.Enrollment`).
- S-20 + S-21 (the Go half), K36: hlwatch's successor check lists a successor's pending decisions and its
  recovery counter (alarmed `successor:proposal:<id>`) and judges its state against the version-1 layout
  (`sol.AdminStateLayout`: the borsh form ends at 318 plus the vectors' entries, `AdminState.Len`; the reserve
  past it zero) — LAYOUT DIFFERS: the hand-over would be spent or die; `hlrecover init` refuses an un-inherited
  successor (`RecoveryFacts.RefuseInit`, the program's `awaits_inheritance`), replacing the S-19 refusal the
  line's follow made unreachable (G45); `hlrecover version`, and the build line at `status` and before `sign`
  (`agent.BuildHash`, hlwatch's own).
- K34: `classify` takes as ours a send from any route realm of the token's line (`readLines` once a step,
  `lineSender`), never marking a successor's send "other"; `checkRouters` reads each token program's enrolled
  router for gno.land's domain once a tick (`sol.ParseToken`, `RouterFor`) and alarms `router:unknown:<token>`
  while it is no realm of the line or none is enrolled.
- G39 + G45: `retireCopy` ends a decision copy addressed to a retired program of the line at the gate whatever
  its number (no attempt; the alarm and copy record of a Retired answer); the hourly staging gate reads both
  live limits (`overLiveLimits`); `decision:other-program` posts daily.
- G40: `FlagSolana` strokes the pausable ISM and the configured program when the line cannot be read and returns
  the line's error beside the signature (`LineUnreadAfterStroke`; hlwatch and hlsol print both).
- K37 + G45: hlwatch's cap-ratio key is `mint:cap-ratio:<token>`, daily.
- F22: `recordSeedSlots` 50,000 (three hours at 0.218 s a slot).
- G36 + G37 + F21 + G45: `checkpoint.go` — the checkpoint reads the client's state through the pair byte for
  byte, checks the trusted header against the own node's block first (a differing one is flagged
  `record:checkpoint` as the cut at the checkpoint's slot, no base taken; hlwatch prints it), takes the ring in
  the program's eviction order (`ringInOrder`: a full ring's oldest sits at `ring_next`), seeds the open stages
  from the client's stage accounts (`seedStages`, `getProgramAccounts` through the pair, whatever their age) and
  starts each route's empty scan cursors at the newest signature at or below the slot (`seedCursors`); a null
  `getTransaction` is a retention skip retried hourly (`skipFor`, `scanSkip.Retry`), counted in the alarm;
  hlwatch's watch flags `header:mismatch` on a trusted header this node's block does not hold.
- G38 + F24 + K35: `foreignFindings` takes a `foreignCtx` (the recovery window, the holds, the retired programs):
  a gone 7-day decision under a window or a hold is a NOTE (alarmed `decision:foreign:note`, hlwatch alarms,
  never a flag), a number at or below a retired program's last is judged on that program's accounts; the monitor
  alarms `decision:line-unfollowed` and `route:line:<token>` after two minutes, each naming the config edit; the
  signers judge over the line (`signerIdentities`).
- M1h's shapes: `enrolledAt`, the route json's `writtenOff`/`writtenOffTotal` (printed by hlwatch), the line's
  last realm re-read each poll and a change alarmed once (`route:successor-changed:<token>`, in the monitor
  through `LineEnds` and in hlwatch's diff).

## Readings where the plan or the task is silent

The line's "ended" is the last successor's import; the realms before it hold 0 once their successor's page 0
landed. `refund:delivered` stands until the write-off mark, read once a minute for such a send alone. The
checkpoint's mismatch stops nothing: the monitor runs without a base (the scan's alarm says the rest). A note of
the gone-decision rule advances `checked` past the number, as a finding does. `-launch-maximum` is one figure
for both tokens, as `DEFAULT_MAX_PER_MESSAGE` is. The successor-change alarm posts once and clears on the next
poll (an event, not a standing condition). hlrecover's build line prints before `sign` even without a config.

## What passes

`go vet ./... && go test -race ./...` on the merged tree (M1h and M2h in): every package ok. The one whole-tree e2e
from nothing at c89f315 (M1h's realms, M2h's programs, this Go): `== PASS in 730s` (m3i-run1.log, started at load
7.8 with 20 GB free). What the run showed of this milestone: the setup's new line (the per-message maximum
25,000,000,000 beside the day cap, the realms' maximum matched) and the audit's two `ok` lines for it (AR4); the
cap-ratio quiet notes for both tokens with the C341 prefix the e2e greps and no `mint:cap-ratio` alarm (AR5/G43
leave the launch-week quiet in place — the run is minutes old, the lock under five caps); hlwatch's build line
printed at the head of its status; the alarm sink carried no flag-level post and none of this milestone's keys
(`refund:*`, `router:unknown`, `route:line`, `route:successor-changed`, `decision:line-unfollowed`,
`decision:foreign:note`, `record:checkpoint`, `successor:proposal`, `header:mismatch`) — its posts were the known
ones: `deliveries:paused`, the two parks, the runways, `updater:refused`, and monitor 2's `solana:providers` with
G30's `monitor:no-verdict` beside it (the disagreeing proxy, by design). Not exercised by the e2e (unit tests only):
a migration line of two hops, a refund proposal, a double mint and its write-off, a successor with decisions
pending or another layout, a wrapped ring, a checkpoint, a gone decision under a hold, a successor's send, a stroke
without the line, a re-pointed line.

# M3j — the Go after audit round 6 (2026-10-13; branch bridge-m3j from bridge 3af1ad1)

Three parts, by message: the round-6 fixes decided for the Go lane (the assumptions audit's F27 and F29, the red
team's AS2, AS3, AS4, AS7 and AS8), the Go audit's G46–G53 and the conformance audit's K38–K40 (which named no
new item: K38 = G48 + F27's hlwatch half, K39 = G46, K40 = G49/AS3), then the two merged lanes' Go-facing lists
(M2i's tool items, M1i's shapes). One commit a finding where the fix stood alone, one for findings on the same
lines (AS8+G52, F27+AS2+K38, AS3+G49+K40, G46+K39, G48+K38). `bridge` merged twice: M2i's programs (5189ccc: kind
9's successor PDA writable, can_inherit's clock factor, the hand-over's sweep), then M1i's realms (997ec87: the
tombstones, kind 6 on the pulled live route alone, the 14-day note with a cancel clearing it, the write-off's
restore, every registered route's address swept, hlgnot2). Every fix has a test proved red first — a behavioural
red wherever the old code could be run against the new test (a 2-day hold still excusing, an appended byte still
LAYOUT DIFFERS, a failed stroke read as landed, the successor's send walked past as "other", the successor's
proposal dropped, the refund applied under the predecessor's router, the maxima uncompared, the honest claims
flagged on a checkpoint, the prebuilt .so unchecked), a stub or a one-line neutralising of the new rule where the
test needed a reader the old code lacked (G53's drop, G49's verdict, G51's check), each then run green with the
rule in. The e2e's migration section asked for in the brief is dropped from this lane's scope (the realm suite's
`TestMigration` drives the migration end to end; the devnet drill stays an M4 item in the plan); the e2e gained
one positive check (AS4's alarm on its own AN5 raise).

## What changed, by finding

- F27 + AS2 + K38 (the Go's half): the refund's gate is Solana's enrolment, not the realm's note. hlwatch reads each
  token program's enrolled router for gno.land's domain through the pair (`SolanaView.Routers`, each named among
  the line's realms, `agent/enrol.go`) and r/hlgov's latest kind 6 for the realm with its state on the admin
  program (`FindEnrolment` over the last 200 numbers; `Kind6StateOf`: absent, dispatched and not recorded, pending
  with its maturity and deferral, cancelled by a stored range — `SolanaView.Cancelled` — or recorded and gone),
  and `refundCheck` alarms NOT ENROLLED ON SOLANA naming both, on a refund proposal and on a queued refund decision
  every poll until it applies (`refund:queued:<realm>#<id>`, "cancel it while this stands"), whatever the note says
  — the note's 14-day wait (R37: twice the realms' delay, which the realm now enforces) printed beside it, NO NOTE
  where a cancel cleared it. The relayer's `executes` does not call `Execute(id)` on a refund decision while the
  token's enrolled router is not the refunding realm (`refundGate`: a tick's wait with the alarm
  `refund:not-enrolled:<realm>#<id>`, the backup's clock restarted, never a park, never an error; a token whose
  state cannot be read waits too, the dangerous act being the execution). hlrecover lists no gno.land decision, so
  the reason is printed by hlwatch's two lists and the relayer's status. Tests: `TestRefundWaitsForSolanasEnrolment`
  (the predecessor still the router: not executed, alarmed; the router switched: executed next tick; the token
  unread: waits), `TestKind6StateAndFindEnrolment`, `TestWatchReadsARefundProposal` (the predecessor as the router
  with the kind 6 unrecorded or pending, the queued decision).
- AS3 + G49 + K40: a checkpointed dispatch index calls no honest waiting claim forged. The checkpoint seeds the
  index from the mailbox's dispatched-message accounts (`seedDispatches`: one `getProgramAccounts` on the mailbox
  program filtered to the DISPATCH discriminator at offset 1, through the pair with equal sets, every burn of a
  configured token toward gno.land folded with both providers' bits; the accounts are never closed, so the index
  is complete whatever the provider's history). Where a provider refuses the call (the public RPCs' 403 for a
  large program, measured by the assumptions audit) the rule falls back to the cut's height: `IndexFromHeight`, the
  own node's head at the checkpoint, in both state files; `judgeUnindexed` trusts a claim credited at or below it
  (before the checkpoint, not judged — counted in the monitor's status and on hlwatch's index line), alarms one
  credited after it that the unseeded index lacks as `credit:unindexed-before-base:<id>` and never flags it (its
  burn may predate the checkpoint's slot), and judges every later claim as before once the index is seeded whole
  (`IndexSeeded`; the seed retried hourly, `IndexSeedTried`). Which path the e2e exercises: neither — the e2e
  never starts a machine from a checkpoint (`-record-from` is not used), so both paths rest on the unit tests
  (`TestCheckpointedIndexTrustsEarlierClaims`, `TestCheckpointSeedsTheIndexFromTheMailbox`,
  `TestWatchPrintsTheCheckpointedIndex`); a checkpointed run against the local validator would take the seeded
  path, since the test validator serves `getProgramAccounts` for every program.
- AS4: the monitors and hlwatch alarm `limits:mismatch:<token>` (daily) while a route's live per-message maximum —
  the line's last realm's while a migration is open — exceeds the pausable ISM's for the token
  (`limitsMismatch`), hlwatch printing the route's maximum on each limits line and a LIMITS MISMATCH line, and
  hlwatch reads a route-maximum proposal against the ISM's live maximum (`maximumCheck`: RAISE ABOVE THE SOLANA
  MAXIMUM — sends between would strand — alarmed `maximum:proposal:<id>`; at or under it, "no send strands"). The
  e2e makes this very condition on purpose (AN5: hlbubble's maximum raised to 50,000 against the ISM's 25,000 so
  the ISM refuses the 30,000 send), so it now expects the alarm — the LIMITS MISMATCH line for BUBBLE and the
  monitor's `limits:mismatch:BUBBLE` in the sink, none for GNOT — and keeps that one line out of its green greps.
  Tests: `TestLimitsMismatchAlarms`, `TestWatchComparesTheMaxima`.
- F29: `ringInOrder` rotates a full ring at 256 whatever the account holds (`min(RingCapacity, sol.Ring)`): between
  a growth's resize and its relocation the account has room for 398 entries while the program's ring is still 256.
  `TestCheckpointRingFollowsTheProgramsEviction` gains the mid-growth capacity.
- AS7: the gone-decision rule's hold exception reads "a recovery window stood at some moment of the week" —
  `holdOverlaps` takes a hold only once its 14th day (`foreignCtx.Fortnight`, at the factor) is at or before the
  week's end, an ended one by its window from its 14th day to its end; `foreignCtxOf` takes the factor. The note
  says the window stood inside the week. `TestGoneDecisionUnderAHoldIsANote`: a 2-day-old hold inside the week no
  longer excuses, one whose 14th day fell inside it does.
- AS8 + G52: hlwatch's successor check judges what the admin program's `can_inherit` judges before any authority
  moves — the state its own, the version-1 prefix parsing under a tolerant borsh load (LAYOUT DIFFERS for a field
  inserted before `inherited`), this admin program as predecessor, not inherited, no successor of its own — each a
  finding, since the program spends the hand-over on it; bytes past the form that are not zero are a note beside
  the successor line (`sol.AdminStateReserve`, alarmed `successor:note:<id>`), never LAYOUT DIFFERS, since M2h's
  contract lets a later build append a field. The tautological length compare is gone (`AdminStateLayout` →
  `AdminStateReserve`), `AdminStateFreshLen` pinned by the unit test's bytes. `TestAdminStateLayout`,
  `TestSuccessorCheckSeesPendingAndLayout`.
- G46 + K39: a route's line that cannot be read for one tick holds the walk. `readLines` keeps the per-token line
  errors (`lineErrs`, alarmed `route:line-unread:<token>`), `classify` answers a held kind (`kindLineUnread`:
  advance false, a note, no Done entry) for a route message whose sender is not the configured realm while its
  line is unread or not yet read this step, and `checkRouters` skips such a route for the tick.
  `TestSuccessorSendIsOurs` no longer enshrines the failure state; `TestUnreadLineHoldsTheWalk` (the state
  unchanged under the failing render, walked once it returns); `TestClassify` reads the lines first as the walk
  does.
- G48 + K38: the proposals and queued decisions of every realm of a token's line are read — `agent.LineRealms`
  names each line's realms at a height and the ones the config does not name, `ReadProposals` and `ReadPending`
  take them beside the configured realms, hlwatch's view reads the lines first and `programChecks` matches a
  proposal's realm against its token's line (`view.routeOf`), the relayer's finisher reads `realms()`. A
  successor's refund proposal now reaches `refundCheck` in the standard layout (the config on the predecessor).
  `TestLineProposalsAndDecisionsAreRead`, `TestWatchChecksTheLinesProposals`.
- G47: the monitor's checkpoint mismatch is a stroke (`applyCheckpoint` → `flagSolana("header:mismatch")`, the
  pausable ISM and the admin program, re-asserted once a minute while it stands), and on a checkpointed install
  (`-record-from client`, base taken or refused) `solChecks` compares the client's trusted header with the own
  node's block at T every poll (`checkpointedHeader`), through `recordMismatch`'s two-minute patience.
  `TestCheckpointMismatchIsAStroke`.
- G50: `FlagSolana` wraps the line's failure beside a landed stroke in its own type (`lineUnread`) and
  `LineUnreadAfterStroke` reads it by `errors.As`, never by the signature's presence; a failed or unconfirmed
  stroke keeps the "the Solana stroke failed" path with the signature named. `TestStrokeLandsWithoutTheLine` gains
  a failed stroke with a readable line and under an unreadable one.
- G51: `hlsol setup -no-build` checks the prebuilt pausable ISM before the first deploy — the expected locks
  required under `-no-build` too, AR4's `launchMaximumCheck` run, and the .so must bind the launch day cap and
  per-message maximum they compile (`sol.ELFBindsU64`: contiguous, or the `lddw` immediate the e2e's build holds
  each in, one per constant at 0x7730 and 0x7750 of the factor-8 build) — refused with nothing deployed; the
  audit's day cap beside the expected lock is a line. `TestNoBuildChecksTheELF`, `TestELFBindsKey`'s u64 cases.
- G53: a refunded park reserves nothing — the walk reads the refund mark for each token's oldest OverDayCap park
  (`dropRefundedParks`, one render a tick per token) before the reservation is computed and ends a refunded one
  (`endRefunded`, the walk's own refunded branch). `TestRefundedParkReservesNothing`.

## M2i's tool items (S-22, S-23, S-25 on the Go side)

Kind 9's account list passes the successor's authority PDA writable (`ExtraFor` `KindTransfer`: `w(succAuth)`, the
sweep's system transfer); `hlsol successor-init` funds the successor's PDA as `setup` funds the launch one
(`-authority-fund`, 10,000,000 lamports) and prints the balance; hlwatch's `successorCheck` prints the PDA's balance
beside its verdict and the successor's compiled clock factor beside the client's head byte and the live admin
program's (`SolanaView.AdminClockFactor`), alarming a difference as a finding; hlwatch's kind-8 check for the
client says the state layout is the build's to keep (§13 kind 4) and prints the buffer's ELF sha256 beside the fix
build's; a `Settle` or `RecoveryRecord` failing with `InvalidAccountData` is named the layout drift it is
(`agent.LayoutDrift`, `settleIfDue`, `hlrecover record`), never a staging problem.

## M1i's shapes (R35, R36, R38, R39, AS1 on the Go side)

The mailbox's `routes[path].pulled` and `.dropped` and its `droppedRoutes` are read (`routeFlags`, `mailboxJSON`,
`MailboxView.RoutePaths/Predecessors/Pulled/DroppedRoutes`); a dropped route is no hop of any line (`followLine`
and `predecessorsOf` never reach a path the mailbox dropped, since the predecessor's `migratedTo` names the new
successor), but a burn addressed to its address is the line's last realm's to deliver once its import ended
(`deliveryTarget` reads each hop's dropped list, waiting before as for a predecessor's); `checkRouters` alarms
`router:unknown` naming a DROPPED route and the predecessor that re-pointed away from it while the token program
enrols one (AS1); hlwatch reads a kind 6 proposal against the mailbox (`enrolCheck`: NOT A REGISTERED ROUTE, THE
other ROUTE'S, A MIGRATED ROUTE, A DROPPED ROUTE, NOT PULLED — page 0 is not in — each what the closure refuses,
alarmed `enrol:proposal:<id>`; the token's own pulled live route taken) and a restore proposal's send ids
(`restoreSends`: a written-off send's restore undoes the write-off, lost falls by its amount, queued 7 days; a send
not written off refused; `agent.SendOf`); `hlsol send`'s realm-address refusal reads the mailbox's registered
routes too (a successor the config does not name). The refund rule's 14 days from `enrolledAt` and an `enrolled` of
0 after a cancel are F27's reading above. hlgnot2 is not added to the e2e's realm list: the list is fixed and
nothing in it imports hlgnot2, so it does not break (M1i's migration drill is not run by this lane).
`TestM1iShapesAreRead`, `TestWatchReadsAKind6Proposal`, `TestWatchReadsARestoreOfAWriteOff`, `TestRealmOf`.

## Readings where the plan or the task is silent

A refund decision on a route with no token on Solana is applied (nothing to check); one whose token's state cannot
be read waits — the dangerous act is the execution. The kind 6's state is read from r/hlgov's last 200 numbers
only while a refund proposal or queued refund stands (the walk is bounded and runs only then). The before-base
alarm names the claim and the cut; the sighting that would make it a candidate is never recorded while the index
is unseeded, so a later seed judges it from its first poll after. The limits mismatch is judged on the line's last
realm's maximum; a lower maximum on gno.land than the ISM's strands nothing and is silent. `checkpointedHeader`
runs when the record started from a checkpoint whether the base was taken or refused (the refused case is where
the stroke matters most). A dropped route's own `migratedTo` and json are a dead realm's: never read. The held
kind for an unread line also holds a stranger's send for the tick (a hold, not a loss; it is "other" once the line
reads). `enrolCheck` takes a launch route's symbol from the config and a successor's from its announcement.

## The recovery path removed (part 3: Jae's design change of 2026-10-13)

Jae's decision: the validators do nothing but validate, so the validators' recovery path is gone whole — from the
plan (v35), the programs (M2j, merged here at 80ebfec after the Go's first cuts at 9b12797/ce45ea2/d0784c8/716ebf4)
and now the Go. One commit a concern, each with the tests it changes:

- **hlrecover** (d0784c8): cut to `version`, `status`, `body` and `execute`. `init`, `sign`, `submit`, `record`,
  `close` and `settle` are gone with the staging, the signing round, the digest and the window; `status` prints
  the client's trusted head, the admin program's numbers and pending count, both holds, the hold history and the
  line "there is no recovery path: what a hold cannot cure is a new deployment (§1)"; `body` refuses kinds 4, 10
  and 11 as reserved (the e2e reads all three: the hold, the history, the refusals, a cancel body's bytes).
- **the machines** (ce45ea2): the updater's and the executor's Settle sends, the `settle:due` alarms and the
  monitors' settle status lines are gone; `agent/settle.go` and `agent/recovery_views.go` deleted; `AdminFacts`
  (`ReadAdminFacts`) replaces `RecoveryFacts` — the chain id, the trusted hash and the last accepted time remain
  for `status`.
- **the views and rules** (9b12797): hlwatch's SETTLE DUE, the recovery window, the counter, the recorded settled
  set and `Recoveries` are gone from `SolanaView`, the watch's state and the texts; the gone-decision rule is plan
  v35 §5.9's — a 7-day decision gone inside its week that no later cancel of r/hlgov's names is a foreign finding
  outright (AS7's window and hold exceptions went with the validators' cancel; `TestGoneDecisionIsForeign`
  replaces the hold test); a number at or below a retired program's last is still judged on that program.
- **the sol layer** (be880a6, after M2j's merge): one channel. `Admin.Decision(number)` is (`decision`, number
  u64 BE); `ExecuteIx(p, number, deliverer, executor, extra, flagsWritable)` sends `0x03 ‖ number LE` with six
  fixed accounts (the state, the decision, its deliverer, the executor, the authority PDA, the pausable flags —
  the client's state is among no kind's fixed accounts); `StageUpgradeIx(number, …)` is tag 4,
  `CloseCancelledIx(number, …)` tag 7; `ExtraFor` answers (extra, flagsWritable) and kind 9's list carries the
  client's state (writable) before the client program, 21 accounts; `CancelPair{Number, Deliverer}`,
  `CancelledRange{From, To}`, `IsCancelled(number)`; a cancel's `CancelRange.Channel` stays (r/hlgov writes the
  byte) and parses as 0 alone — a cancel naming channel 1 is spent, as the program spends it. `AdminState` loses
  `RecoveryCounter`, `Settled` and the second pending slot (`Pending uint32`); `AdminStateFreshLen` 262,
  `AdminStateAccountLen` 1,574 (`predecessor` at 229, `inherited` at 261, pinned by `TestAdminStateLayout`);
  `HighWater` is gone (no kind 10: `LastSeq` is the mark). `Decision` loses `Channel`, `Trigger` and `Marker`;
  `ParseDecision` is M2j's 95 bytes beside the parameters (`DecisionOverhead`, pinned), the 32 spare bytes of a
  None refund key still counted; `HeldToTheDay` is kind 5, `HeldUntilTheEnd` the rest of the 7-day group;
  `TriggerLapse/Hold`, `RecoveryStage`, `CancelRecord`, every `Recovery*Ix`, `SettleIx`, `CancelRecordState`,
  `CancelPairs` and `sol/recovery.go` (the stage, the settled set, the window, `SettleDue`) are deleted, with
  gnolight's `RecoveryDigest`, `CancelDigest` and the anchors (`DecisionPrefix` stays). Kinds 10 and 11 are
  unknown (`KindName` "unknown", `DescribeParams` "recorded and spent", no `Params` fields, `ExtraFor` nil);
  `AdminError` is the compacted table 0–27 (Retired 25, NeedsExtend 26, NotInherited 27 — `judgeExecuteErr`'s
  "refused" is LayoutFixed alone, CancelSpent being no error). `SuccessorInitData` encodes the hand-over's tag 8
  (never sent by a tool; the vector pins the wire contract). The client: `ResetIx` gone, `SetAdminIx` 7, `GrowIx`
  8, `StageNextSetIx` 9 (the agent's `nextSetChunk` and the packet test read 9), `DecodeTrustedHeader` reads Init
  alone and the record folds no reset (the ring clears at a set switch only). The vectors: `tools/vectors` run on
  the Mac (`cargo run --release`, 2 m 39 s, the two header lines kept) — `admin_execute_5`,
  `admin_stage_upgrade_2`, `admin_close_cancelled_9`, `admin_decision_5_pda`, `admin_decision_300_pda`,
  `admin_successor_init` in; the recovery, settle, cancel-digest, decision-by-channel and reset vectors out;
  `body_cancel_range` and `body_cancel_list` byte-identical. Everything downstream reads one channel: the
  executor's keys are `solexec:<n>` / `solext:<n>` and its texts "decision n (kind k)", `readDecision` and
  `readPendingDecisions` take a number (the last 500), `CancelPairsFor` bounds by `LastSeq`, `PendingSolDecision`
  has no channel, hlwatch's pending line is "  - #n kind k (…)", its successor check reads the new layout and
  says "%d pending" (no recovery counter), hlsol's audit prints "pending decision #n" and ranges without a
  channel and no high-water line, hlrecover's `execute` passes the number alone; `layoutDrift` (a Settle's or
  RecoveryRecord's failure) is gone.
- **the e2e** (716ebf4, b777b69): the hold section keeps the hold, the dip and hlwatch's HOLD line and reads
  hlrecover `status` (the hold, the empty history, the no-path line), `execute`'s two refusals (the deferred kind
  12 as "decision 2 (kind 12) is not executable now", the gone decision 1 as "no pending decision 1") and `body`'s
  cancel bytes with kind 11 refused; the recovery drill (the window and anchors, settle, the digests, init, sign,
  submit, record, close) and the machines' settle lines are gone, the audit's settled-set greps with them;
  hlwatch's pending lines are grepped as "  - #1 kind 7 (" and "  - #2 kind 12 (" (the proposals' "- #1 kind 7:"
  is another line); every other section stays as it was.

Kept, as the brief lists: the quarantine, `holdSince`, the dip rule and the grace, the hold history and kind 5's
21-day floor (`MaturesAt`, `FloorUnder`, `ReplacementFloor`, the `solfloor:` alarm), the hand-over (kind 9's list,
`SuccessorInitIx`, hlwatch's successor check with can_inherit's clock-factor check and the PDA's balance),
`AwaitsInheritance` (NotInherited on Handle and Execute now).

Red first: the regenerated `vectors.txt` against the old layer failed `TestVectors` on `client_grow` (09 → 08),
`client_grow_8192`, `client_set_admin` (08 → 07), `client_stage_next_set_487883` (0a → 09), six admin vectors "not
rebuilt here" and twenty old names with "no vector" — then green with the layer cut; `TestAdminStateLayout` and
`TestParseAdminAndPausableStateR1` are re-pinned at the new offsets (a state of the old layout is "more holds than
the program keeps" to the new parser, which is what the agent's and hlwatch's old fixtures failed with before
their builders were cut to M2j's form); `TestExecuteWhen` loses its eight recovery cases; `TestJudgeExecuteErr`
sends 3026 for NeedsExtend and 3035 as a code past the table; `TestRetiredIsNotParked` sends 3025 and 3027;
`TestDecisionClocks` replaces the cancel-digest test; the record tests fold no reset (`TestClientRecordFold`
counts three entries, the ring test six); `TestInstructionsInOrderFoldsInner` folds the client's init as the inner
instruction (the order rule stands though no program CPIs the client now).

## What passes

`go vet ./... && go test -race ./...` on the merged tree (M1i, M2i and M2j in): every package ok, no race. The
whole-tree e2e from nothing at f900c33 (M1i's realms, M2j's programs, this Go): `== PASS in 731s` (m3j-run3.log,
started at load 2.6 with 21 GB free; the four FAIL lines of the negative-audit section are the expected ones). Two
runs before it stopped on the e2e's own text, not the Go: run 1 at 267 s on the executor's status key (`waits()`
read `solexec:0/1`; the key is `solexec:1` now), run 2 at 481 s on `hlrecover body`'s expected bytes, which
716ebf4 had written by hand in the range form while the encoder writes a single number as a list of one (that
section had never run before this lane's merge), and the same pass fixed the audit's wanted lines the run had not
reached (`cancelled range: numbers 1-1`, `pending decision #2 kind 12`, no high-water line). What the passing run
showed of this milestone: the setup and the audit under M2j's programs (the SBF build of the staged programs from
M2j's sources, hlwatch, the audit and hlrecover reading the admin state and the decision accounts in M2j's layout —
pending accounts 2 then 1, the kind 12's text, the hold), both GovDAO decisions recorded and shown by hlwatch on
the one channel ("  - #1 kind 7 (", "  - #2 kind 12 (") with the executor waiting on each (`solexec:1`, `solexec:2`
"waits: matures at"), the cancel's salted body delivered and decision 1 closed by its number, the hold section's
hlrecover reads (the hold since the flags, the empty history, the no-path line, "decision 2 (kind 12) is not
executable now: deferred by the hold", "no pending decision 1", the cancel body under salt 7 as the list form, kind
11 refused), the limits mismatch alarm on the AN5 raise (AS4) and the green greps without it. Not exercised by the
e2e (unit tests only):
a successor's state in M2j's layout (hlwatch's successor check), a checkpointed index (AS3's e2e exercises neither
the seed nor the fallback: the record starts from the init, not a checkpoint — both paths are
`TestCheckpointSeedsTheIndexFromTheMailbox`, `TestCheckpointedIndexTrustsEarlierClaims`), a migration line of two
hops, a refund proposal, a double mint and its write-off, a wrapped ring, a stroke without the line, a kind 9.

# M3k — the Go after the final check (2026-10-13; branch bridge-m3k from bridge 02ee372c)

The final check's red team (bridge-r7/audit-r7-redteam.md) found two mediums at the seams the brief named and a
residue, and Jae wants the loop wrapped up: exactly these, nothing more. AT3: the fresh deployment both endings
rest on could not take r/hlgov's decisions — a new admin program started at 0 while r/hlgov's counter never goes
down. AT2: the refund's Solana gate lived in the relayer's loop while the realm's `Execute(id)` was anyone's, so
a kind 6 deferred by one flag let a refunded sender mint his send twice on day 21. AT8's residue: a dead variable
in the audit and Go comments that still described the removed recovery path. One commit a finding (3844bee AT3,
acd5d9d AT2, 5b391dc AT8), each with its test proved red first. The programs' half of AT3 is M2k (M2-NOTES.md,
the same commit); the realms' half of AT2 is M1j's, run in parallel — `NewApplyRefundRequest(cur, id)` in
hlgnot, hlbubble and hlgnot2, `Execute(id)` refusing a refund decision — and hlwatch's reading of the apply
proposal is written against the obvious title until M1j's builder names it. AT5 (a diverted credit before a
checkpoint, low) is not in scope: a known residue.

## What changed, by finding

- AT3 (the Go's half): `InitIx(payer, authority, seats, watch, origin, predecessor, lastSeq)` carries the number
  the sequence starts at as its last field (u64 LE after the predecessor), `SuccessorInitIx` 0; the vectors
  regenerated by `tools/vectors` (10 s on the Mac this time, the build cached; `admin_init` and
  `admin_init_successor` eight bytes longer, `admin_init_fresh_37` new, the two header lines kept). `hlsol setup
  -start-sequence N` (default 0): before anything is built or deployed, `startSequenceCheck` renders r/hlgov's
  json on `-gno-rpc` and `startSequenceVerdict` judges the number — r/hlgov on the chain, N must be its `Last()`
  (a fresh deployment under the same r/hlgov; a mismatch refuses, naming the number to pass and that nothing was
  built or deployed); r/hlgov not on the chain (`realmAbsent`: the keeper's not-found, which the audit's two
  renders now read through the same helper), N is taken as given and said so (a chain's first deployment, whose
  realms go up after the Solana side passes its audit; a new chain's r/hlgov compiles its predecessor's last
  number, the same N); the counter unreadable for any other reason, refused. The admin init's line prints the
  number. `hlsol audit`'s counter line is `counterLine`: a FAIL on any gap as K15 had it, each direction named —
  the admin program's number below r/hlgov's is AT3's (a fresh deployment that started low, or r/hlgov's
  decisions dispatched and undelivered, the numbers printed), above it a foreign decision or GovDAO's lost; the
  e2e's expected line stands as the prefix of the agreeing case. Tests: `TestInitIxCarriesTheStartingNumber`
  (the tail, the successor's zeros), `TestVectors` (red first: the regenerated vectors against the old builder
  failed `admin_init` and `admin_init_successor` eight bytes short and `admin_init_fresh_37` unbuilt),
  `TestStartSequenceVerdict` (equal, the four mismatches, absent at 37 and at 0 — the e2e's case — the counter
  unreadable, `realmAbsent`), `TestCounterLine` (the three directions and the e2e's prefix).
- AT2 (the Go's half): the relayer's `executes` leaves a matured refund decision alone — `awaitsApply` logs once
  per decision that it is mature and awaiting GovDAO's apply and keeps that in the status (`r.last`), nothing is
  attempted, so nothing can park, and no alarm stands; `refundGate`, `routeOfRealm`, `refundKey` and the
  `refund:not-enrolled:<realm>#<id>` alarm are gone (M3j's gate had the relayer wait on the token's router and
  apply once it was the successor; now the apply is GovDAO's, since the realm's `Execute` refuses the kind and
  no realm reads Solana). hlwatch reads the apply-refund proposal on any realm of a token's line — told apart by
  its title first, `isApplyRefund` (M1j's builder writes "<realm>: apply refund", `propose`'s "<realm name>: " +
  the kind), then by its description's head (`applyRefundTextRE`: `apply refund decision N at once`); the
  description quotes the refund decision's own text, ids included, so the refund-proposal branch
  (`refundTextRE`) is asked only for a proposal that is no apply, or an apply would have been read as the refund
  it quotes and alarmed twice — against the queued decision it names: `applyRefundCheck` runs `refundCheck`'s
  gate over the decision's ids — the token's
  enrolled router on Solana and the kind 6's state (`RefundGate`), the note's 14 days, DELIVERED beside an id
  Solana processed — with the decision's maturity (not mature: said, the closure executes it only then and a
  passed vote retries); NO QUEUED REFUND where the realm holds none pending under the id (applied, cancelled or
  never made) and NOT A REFUND where the id is another kind's, each what the closure would refuse. Alarmed
  `refund:apply:<proposal>` with "do not propose or vote the apply while this stands"; the queued refund's own
  finding (`refund:queued:`) says "awaiting GovDAO's apply" and the same words in place of "cancel it while this
  stands" (the cancel stays the cure for a cancelled kind 6); the status page's pending list says a refund is
  GovDAO's apply-refund proposal's alone and prints "awaiting GovDAO's apply" beside a queued refund, and the
  `mature:` alarm for a refund says so too. hlrecover lists no gno.land decision (M3j), so the two lists that
  say it are hlwatch's and the relayer's status. Tests: `TestRefundIsNeverExecutedByTheRelayer` (replaces
  `TestRefundWaitsForSolanasEnrolment`: the successor enrolled — where M3j's gate applied the refund — three
  ticks, `Execute(4)` never called, the credit beside it applied each tick, the status line, one log line, no
  clock, no attempt, no alarm; red first with M3j's relayer.go and enrol.go swapped in from 02ee372c: "the
  relayer applied a refund: 3 calls"), `TestWatchReadsAnApplyRefundProposal` (the builder's title and
  description; the predecessor still the router: the apply's finding and the queued refund's, both with the
  words, the apply checked once and never as the refund it quotes; the successor enrolled and the note old:
  clean; not mature: said; a delivered id: alarmed; the title alone telling an apply; no such decision and a
  credit's id: the two refusals; the status page's list; red first with M3j's hlwatch main.go swapped in: the
  proposal's text left unread), round 5's queued-refund assertion following the text. M1j's other shapes on the
  Go side: the decisions json's new kind "apply refund" is at once (applied at its creation, never in
  `dueDecisions`), a queued refund's note reads "queued 7 days: applied by GovDAO's own apply refund decision
  after, never by Execute" and the realm's `Execute` on a refund panics — so the relayer's skip is required, not
  a courtesy; `hlsol send`'s realm-address refusal reads the mailbox json's `droppedRoutes` too
  (`mailboxRealms`: the registered routes, then the dropped in order — AT6's Go mirror: a re-pointed-away
  successor's address is swept by the line's last realm, so it is refused like any realm's; `TestRealmOf` gains
  the dropped case, which the registered list alone fails); a successor with `enrolled` 0 after its last page
  refuses every `TransferRemote` (AT7, the realm's), which the relayer sees as no send at all.
- AT8 residue: `hlsol audit`'s `pausableGuardians` (declared, assigned, `_ =`'d) is gone; the Go comments that
  still described the removed path are trimmed to the rule that stands — sol/admin.go's header, kinds 10 and 11
  ("reserved (§13)"), `AdminState`; sol/admin_test.go's layout test; agent/views_sol.go's header, the
  gone-decision rule and `foreignCtx`; `AdminFacts` (relayer_sol.go); the executor's header; audit_r5_test.go's
  G38 comment; hlrecover's header. Kept, as the task says: Hyperlane's own words, Go's `recover()`, the tool's
  name, hlwatch's "unrecoverable program-data rent" (AP5, English), and hlrecover's printed "there is no recovery
  path" line, which states §6.4's rule and which the e2e reads. `grep -rn -i recover bridge/ --include='*.go'`
  outside those is empty.

## Readings where the plan or the task is silent

The apply proposal is told by its title (the builder's "<realm>: apply refund") before its description, against
§5.3's "hlwatch shows the parameters, never the title": the description quotes the refund's own text, so the
title is the one field that tells the two apart without a parse of the quote, and the description's head is
read beside it (a title alone, with a description of another shape, is still read as the apply of the decision
the head names — 0, NO QUEUED REFUND, when there is none). The relayer applies
no refund at all now, a route with no token on Solana included (M3j applied that one on the note alone): the
realm refuses `Execute` for the kind, so there is nothing for the relayer to do, and GovDAO's apply is the one
path. The relayer's "logged once" is once per process (the status line is in memory; a restart logs it again,
the status says it meanwhile). The audit fails both directions of the gap as K15 had it, the words naming AT3's
direction; a transient gap at a holder's audit — a decision dispatched and not yet delivered — is a FAIL as
before, the line saying to deliver and read again. The setup's check runs before the build and before
`launchValues` writes config.rs, so a refusal spends nothing; `-hlgov` as a g1… address cannot be rendered, the
number is taken as given and said so (a deployment passes the pkgpath). A successor's init with a number is
refused rather than ignored (the realm-side rule of refusing nonsense early, M2k). The apply's maturity is a
note, never a finding: the closure's execution before the delay panics as transient and the proposal survives
a retry.

## What passes

`bridge/programs/test.sh all` at factor 1 and `FACTOR8=1 … sbf && … suite` at factor 8: the core's 28, the
suite's 32 (18 admin, 11 client with one ignored, 1 limits, 2 pausable), green at both. `go vet ./... && go test
-race ./...`: every package ok, no race. The whole-tree e2e: after M1j's merge, below.

**What passes (M3k):** the whole tree's e2e from nothing at 0ffa726b passed in 718 s (`~/.config/bubblerumble-jarvis/bridge-e2e-logs/m3k-run2.log`; the four FAIL lines are the negative-audit section's expected ones, the counter line now among them). A first run (m3k-run1.log) died on a stalled machine — its sections ran at a tenth of their pace and the parked-send check timed out — not on the code; the second run on the same tree passed at the usual pace.

# M3l — the Go side of the governor and the yield (2026-10-14; branch bridge-m3l from bridge 83fcb9dd)

Jae's design change of 2026-10-14 ("Until I yield to GovDAO I want to be GovDAO alone"): one compiled key makes
every realm decision directly through the realm's `Govern` until its one-way `Yield` on hlmailbox hands them to
GovDAO (plan v36 §5.3, §6.6, §11.16; the realms' half is M1k, M1-NOTES.md). This is M1k's "What the Go side must
follow" list, every item, and nothing beyond the task's four: the views read the realms' new json, hlwatch says who
governs and what waits on the yield, the relayers' finisher waits for it, the e2e runs the governor's half (§5.9's
new items). Four commits (47a6327a the views, 701834cc hlwatch, db79a231 the relayers, c39b7d0e the e2e), each
with its test red first; nothing in `realm/` was touched. Read first, as AGENTS.md asks and as the Go side reads the
realms: `docs/gno-specs/gno-interrealm-v2.md` §12 (MsgCall for `Yield`, MsgRun for `Govern` with a `Request`,
which the e2e's scripts follow) and §9 (a refusal is a panic before any record: the proposal survives it), and the
security guide's §5.5 (the governor is an identity, read by the realm from its own cur — the Go side reads only what
the realms render: `"governor"` and `"yielded"`).

## What changed, by piece

- The views (agent): `mailboxJSON` and `routeJSON` carry `"governor"` and `"yielded"` (M1k: right after `"live"`,
  the mailbox's after `"launchedAt"`), `MailboxView`, `RouteView` and `GovView` keep them, `agent.DAOGoverns` is the
  realms' rule (none compiled, or a non-zero yield height) and `MailboxView.GovernorLine` their page line word for
  word (`decisions.GovernorLine`), so one grep reads the realm's page and hlwatch's status alike; `agent.DAOPath`
  names gno.land/r/gov/dao. A realm from before M1k renders neither field and reads as GovDAO from the first block.
  Test: `TestViewsReadTheGovernor` (the mailbox's, a route's and r/hlgov's json before and after the yield, none
  compiled, a realm without the fields; a compile-red on the tree before the fields).
- hlwatch: the governor line under the live line ("governor: g1… (until the yield) — it alone decides, by Govern
  from its own key, and gno.land/r/gov/dao's proposals are refused" / "governor: gno.land/r/gov/dao since height H
  (g1… yielded)" / "… from the first block (no governor was compiled)"); a pre-armed proposal reads "PRE-ARMED:
  has the votes; awaits the yield (until the governor yields, the builder's closure refuses gno.land/r/gov/dao:
  executing it now panics and leaves it pending, within its 14 days)" while `yielded` is 0 with a governor
  compiled, "anyone may execute it" after; the queued-refund verdicts name who applies — the pending list's
  "awaiting the governor's apply (an apply-refund decision by Govern; …)" and the section header, the
  `refund:queued:` finding's "awaiting the governor's apply: … — do not make (by Govern) the apply while this
  stands", the `mature:` alarm's — GovDAO's words as before once it governs (round 5's and round 7's assertions
  stand untouched, since their views compile no governor); the watch alarms `prearmed:` with " — awaits the yield"
  before it and `governor:yielded` once at the yield. The refund gate, the successor and kind-6 checks that read
  proposals are as they were. Tests: `TestWatchPrintsTheGovernor` (the three lines, the two pre-armed wordings, the
  applier in the list, the header and the two alarms, the yield's alarm once), `TestWatchQueuedRefundNamesTheApplier`
  (the finding before and after the yield, against the same NOT ENROLLED gate); red first with 83fcb9dd's main.go
  swapped in — no governor line, the queued refund "awaiting GovDAO's apply" before the yield.
- The relayers: `finishProposals` reads the mailbox json once a tick and, while a governor is compiled and
  `yielded` is 0, neither reads r/gov/dao's pages nor calls it — the status line `proposals` says "awaits the yield:
  until the governor g1… yields on hlmailbox a builder-made proposal is refused by its closure, and nothing of this
  relayer reads or calls gno.land/r/gov/dao", logged once each time the words change (`gate`); from the yield the
  proposals are first seen, so their 14 days (the realms' deadline from the mailbox's clocks) count from there and
  the call never comes early; with none compiled the finisher runs from the first block as before. `executes`,
  `awaitsApply` and the rest are unchanged. Test: `TestFinishProposalsAwaitTheYield` (the DAO's pages removed to
  prove nothing reads them before the yield, past the deadline too; the status line; first sight at the yield, left
  within its days, finished past them; none compiled; the mailbox unreadable); red first with 83fcb9dd's
  relayer.go: "before the yield the finisher read the DAO: r/gov/dao: fake: no render".
- The e2e: the six realms that compile `launchGovernor` are staged and swapped to gnodev's test1 with M1k's one
  regex each (hlgnot2 staged for the swap, not uploaded: the migration drill is a later milestone); the realms'
  clock factor is swapped to 1008 in the staged p/decisions (below); GovDAO's init as today, then
  `hlmailbox.Govern(cross(cur), hlmailbox.NewLaunchRequest(cross(cur)))` from test1 (a MsgRun script, `govern NAME
  REALM EXPR`), the mailbox rendering `"governor":"<test1>","yielded":0`, and his first queued decision — hlfees'
  treasurer swap to the relayer's own gas key, 600 s; after the OverMax send his hlmailbox decisions — a seat
  removal ("4:-", at once: "removals: seats 4 vacated") and a seat replacement ("2:<the deployer's key>", queued
  600 s) — with hlwatch's governor line, seat 4 "(empty)", both queued decisions "matures in", and the relayer's
  `proposals` line "awaits the yield"; r/hlgov's kind 7, kind 12 and the cancel of 1 as his (the same decisions
  1–3 and checks as before, through `Govern`); then GovDAO's cancel of his queued replacement made and voted
  (`.Proposal()`), listed PRE-ARMED with "awaits the yield", its execution refused with "until the governor yields,
  <test1> alone decides, by Govern from its own key: not gno.land/r/gov/dao" (`run_refused`: no OK! and the words),
  the decision still pending, no "ExecuteOrRejectProposal sent" in either relayer's log; `hlmailbox.Yield` from
  test1 (a MsgCall), the height read from the mailbox and the same `"governor","yielded"` on hlfees, hlgnot,
  hlbubble and hlgov, a second `Yield` refused ("yielded at height H already"), a `Govern` of an hlfees swap refused
  ("the governor yielded at height H, gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never
  Govern"), hlwatch's line "gno.land/r/gov/dao since height H", the proposal "anyone may execute it", the relayer
  "since the yield at height H"; that proposal executed as GovDAO's first decision — hlmailbox 3 cancelled, 4 the
  cancel, nothing pre-armed; the hlfees PRE-ARMED section after the yield (GovDAO's swap, decision 2), the backup's
  second cancel, the unpause and the rest as today, every builder in `propose_and_execute` carrying `.Proposal()`
  and gnops' validator proposal through `propose_and_execute_dao` without it; before the final section the
  governor's queued swap landed by the relayer's `Execute(1)` ("applied after the delay"; the treasurer the
  relayer's key, or test1 if GovDAO's decision 2 has landed on a slow run) and the governor line GovDAO's since
  the yield in the green check. Every earlier section's checks stand.

## Readings where the plan or the task is silent

- The realms' clock factor. §5.9's "a 7-day one queued and landed by a stranger's `Execute(id)`" cannot happen at
  the realms' compiled factor of 1 in a twelve-minute run (nor at the programs' 8: 21 hours), so the staged
  p/decisions carries `Factor = int64(1008)` — the plan's own device ("the test chain's realms carry the same factor
  on their 7-day delay", a compiled constant the e2e swaps as it swaps the launch values): the 7-day delay is 600 s,
  the 14-day deadline 1200 s. The numbers that chose it: the launch is at ~75 s and the swap lands at ~675 s, the
  run ends at ~740 s; the deadline also ends the cap-ratio quiet (AR5: `launchedAt` + the deadline, ~1275 s), which
  the final section asserts, so a run half again as slow still passes; a proposal is never open 1200 s. The
  programs stay at 8 (Solana's clocks, the kind 12's 21-hour maturity included); the meters are per real day,
  never divided; the old router's week and the enrolment wait are not exercised here.
- The decisions chosen. The at-once one is a guardian seat's removal (seat 4, a placeholder key; a removal alone
  applies at once, §5.3) — nothing of the run reads gno.land's guardian seats but hlwatch's list and the watch key's
  seat 5. The queued one GovDAO cancels is a seat replacement to the deployer's key (7 days, "applies only while the
  seats stand as they do now"). The one that lands is the treasurer swap to the relayer's gas key: the relayer's
  `executes` is the stranger's `Execute` (its key is neither the governor's nor GovDAO's), so the landing is the
  machine's and not a race against it; GovDAO's own swap to test1 (the pre-armed section) lands 600 s after its
  section, after the run on a normal one, and the final check takes either treasurer by decision 2's state.
- One proposal shows three things. The GovDAO proposal made before the yield is the cancel of the governor's
  queued replacement: pre-armed and "awaits the yield", refused when executed (the realm's panic aborts the
  transaction — r/gov/dao v1.2.0 recovers nothing, so under `ExecuteOrRejectProposal` too the proposal stays
  ACTIVE with its votes, as M1k reads §9), and executed after the yield as GovDAO's first decision, which is what
  §5.9's "a decision of his still queued at the yield cancelled by GovDAO's first proposal" asks. GovDAO's
  proposals are numbered from 0 (hlwatch's "#0"); the e2e reads the id from hlwatch's line and assumes none.
- The finisher reads nothing of the DAO before the yield, `ReadProposals` included ("nothing of the relayers
  touches GovDAO" read as reads too, which cost nothing to withhold); a mailbox render that fails skips the tick
  with the error (before: the 14-day default and the finish all the same) — the yield unknown is a reason to touch
  nothing. The relayer's own apply-refund wording ("awaiting GovDAO's apply") is unchanged, as the task keeps
  `executes` unchanged; hlwatch names the applier.
- hlwatch prints the mailbox's governor and yield: every realm's json is parsed into its view, and the mailbox's is
  printed, since each realm's `init` refuses to deploy with another governor and every realm reads the mailbox's
  yield — a disagreement cannot exist on one deployment, so no rule alarms one.
- gnokey's refusal: a realm's panic reaches gnokey's output as "VM panic: <the realm's words>" (the keeper's
  `doRecoverInternal`), and the simulation refuses before any broadcast, so `run_refused` and `call_refused`
  assert no OK! and the realm's words, printed whole on a miss.

## What passes

`go vet ./... && go test -race ./...`: every package ok, no race (the three new tests among them). The whole tree's
e2e from nothing at db79a231 with the committed e2e passed in 741 s
(`~/.config/bubblerumble-jarvis/bridge-e2e-logs/m3l-run2.log`, `== PASS in 741s`, `e2e exit 0`; the four FAIL lines
are the negative-audit section's expected ones). The sections in order: the realms up and the governor's Launch and
queued swap at 75 s; his hlmailbox decisions at 246 s; r/hlgov's kind 7, kind 12 and his cancel at 249, 277 and 299
s; GovDAO's cancel pre-armed, "awaits the yield" and refused at 327 s; the yield at 331 s (height 355), the second
Yield and the Govern refused, the five realms reading it; GovDAO's first decision at 334 s; the pre-armed hlfees
section at 337 s; the backup's cancel at 345 s; the parked send, the flag, the hold, the provider, the unpause and
the validator-set change as before; the governor's swap applied by the relayer's `Execute(1)` 62 s past its due
time ("applied after the delay"), the treasurer the relayer's key with GovDAO's decision 2 pending; hlwatch green
with "governor: gno.land/r/gov/dao since height 355". A first run (m3l-run1.log) died at 321 s on the e2e's own
check — the `decision` helper printed JSON with spaces after the colons and the grep for `"state":"pending"`
missed — while the refusal matched word for word and the decision stood pending; the helper prints the realm's
compact form now, and the second run is the committed script.

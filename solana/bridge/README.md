# GNOT and BUBBLE between gno.land and Solana

The plan is `PLAN.md`: a light-client bridge under one governor until he yields and under GovDAO from the yield (a gno.land light client on Solana
checks every gno → Solana transfer; three operator signers carry Solana → gno until Alpenglow lets a proof replace
them, and vouch for Solana's validator roster per epoch until SIMD-0511 lands and Proposal 3 removes them), its trust stated in people at every step (§1), and a review log of every finding and answer (§12). This file
is the code map and the local run. The infographic (the two proof paths, the machines, the roles, the trust at launch
and after Alpenglow) is `infographic/`, seven boards as pictures and pages. The review log is
`REVIEWS.md`; the build plan `BUILD.md`. Where the two differ, the plan wins. Since plan v36 (2026-10-14) the bridge launches under one governor — Jae's key, compiled into the realms; every builder of §5.3, `Launch` and r/hlgov's decisions are his direct call through `Govern` until his one-way `Yield` on hlmailbox, GovDAO's proposals refused before it and the only way after — so the mainnet deployment runs a testing phase with a small lock before GovDAO's yield vote (M1k builds it in the realms, M3l in the Go; the rows below describe the tree before them). The gno.land
realms are at the plan's shape (M1 of `BUILD.md`; what they chose where the plan left room is `M1-NOTES.md`); the Go
side — the signers, the updater, the relayers, the monitors, `hlwatch`, `hlsol setup` and `hlsol audit` — is at
the plan's shape too (M3a and M3b; `M3-NOTES.md`), as are the three Solana programs (M2 through M2h; `M2-NOTES.md`), each audit round's fixes folded by one lane per layer (AUDIT.md). The local run
delivers gno → Solana under the light client.

```
gno.land                                              Solana
r/hlbubble (BUBBLE lock)  ── TransferRemote ──▶   our deployment of Hyperlane's mailbox ──▶ pausable ISM ──▶ light-client ISM ──▶ synthetic BUBBLE (mint)
r/hlmailbox                                        (Hyperlane's mailbox and token programs unchanged; the pausable ISM, the
                                                    light-client ISM and the admin program are ours; the admin program owns the
                                                    mailbox, both tokens and the pausable ISM and holds every upgrade
                                                    authority, its own burned)
              ◀── Process: a claim; Claim pays it under the meters ── hlmailbox asks the active verifier (2 of 3 ed25519 at launch) ◀── TransferRemote: burn, dispatch
```

In the plan the signers sign ed25519 for hlmailbox on gno.land only; nothing on Solana counts a signer, and nothing
on this branch signs for Solana (M3a's multisig stand-in is gone).
Hyperlane domains: 1198419788 ("GnoL", gnoland-1) and 1198419796 ("GnoT", every other chain).

## What is where

| | |
|---|---|
| `realm/p/hyperlane/v0` | Hyperlane's message, id, merkle tree, checkpoint digest and token message, byte for byte (tested on Hyperlane's vectors), plus an ed25519 message-id multisig: gno.land has keccak256 natively but no secp256k1. |
| `realm/hlmailbox` | The gno mailbox (`PLAN.md` §5.1): `Dispatch` keeps each message and emits §13's event, `Reemit` emits it again; `Process` lets a recipient realm pull its own message once, marks it delivered and then asks the verifier GovDAO activated by pkgpath. It keeps the one signer set and its rule, the six seats (four guardians, two watch keys) and their `sends`/`credits`/`payouts` flags per route, the one-shot `Launch` and the builders of §5.3. Domains: 1198419788 ("GnoL", gnoland-1) and 1198419796 ("GnoT", every other chain), free in the registry. |
| `realm/hlverifier` | The launch verifier: ed25519 signatures over the Hyperlane checkpoint under hlmailbox's rule (a threshold, or one of each of two groups), with `Satisfiable`. |
| `realm/hlgnot`, `realm/hlbubble` | The GNOT lock (coins, by direct user call with the exact envelope) and the BUBBLE lock (the token's allowance): `TransferRemote` locks, pays the fee to r/hlfees and dispatches; `Process` credits a claim from the enrolled router; `Claim(n)` pays claims in turns under the meters (a tenth of the lock a day, a quarter for one recipient, the fee first); the builders of §5.3 (caps, fees, router, void/restore/credit by id, sweep, refund, migrate). The state machine they share is `realm/p/hlroute/v0`; the decision ledger every realm keeps is `realm/p/decisions/v0`. |
| `realm/hlfees`, `realm/hlgov` | The fee realm (`Withdraw` by the GovDAO-set treasurer, to itself) and GovDAO's decisions to Solana (kinds 1–9 and 12 of §13 as builders, `set decision recipient`, `sync sequence`, re-dispatch by anyone). |
| `bridge/hl` | The Go twin of the format package, plus the signers' ed25519 checkpoint signing. Writes the gno tests' signed messages. |
| `bridge/gnolight` | The Go twin of `programs/gno-light-core`: the header hash, the precommit sign bytes, the validator set hash and the commit rule, the proof over `LastResultsHash`, the results leaf and the Dispatch event; tested on the chain's own blocks. |
| `bridge/sol` | The three programs' instructions, PDAs and states from `sol-util`'s layouts (the light client's stages and ring, the pausable ISM's flags, the admin program's decisions and cancels), the upgradeable loader, Hyperlane's mailbox, token and Token-2022 reads, and the `process` builder with the ISM's and the recipient's accounts (the recipient's by simulation, as Hyperlane's relayer takes them). `testdata/vectors.txt`: 68 encodings printed by `tools/vectors` from the programs, rebuilt byte for byte in the tests. |
| `bridge/agent`, `cmd/hlbridge` | The operator's machines (`PLAN.md` §5.5–§5.9), one binary: the **signer** (its own gno node, two Solana providers that must agree on every burn and on the supply, ed25519 over the checkpoint digest once per message, a nonce conflict halts it, the refusal rule `supply > lock − owed − voided + lost` evaluated over the ordered cut with the client's trusted height stops it and the status endpoint says why), the **updater** (its own key: a header every 10 minutes, at once when a dispatch waits, the announcing header the moment r/sys/validators emits a change; staged updates resumed from the chain; the one-hour lag alarm), the **relayer** (a primary and a backup 60 s apart; gno → Solana under the light client — the merkle path from the block results, the proof staged against the ring or the header with its commit, `process` through the pausable ISM, decisions to the admin program one copy per sequence number with the taken-number report; `Process` with exactly the signatures hlmailbox's rule needs, `Claim(20)` continuously, `Execute(id)` after a decision's delay, `Reemit` from the client's real view, parking after three deterministic failures and hourly retries, a gas-only key with gnokey's simulation first) and the **monitor** (every 10 s the Solana snapshot with the client's trusted height, then gno.land at a height after it, the identity by the signers' exact rule, the diverted-credit check, the third-source rule, the client's header record rebuilt from instruction data and checked against the own node with the fork rule, every decision against r/hlgov's, stalls, ages, the floats' runways; `credits`, `payouts` and `sends` through the gno watch key and the pausable ISM's flag through the Solana one on a break, alarms to a webhook). `hlbridge providers` checks §5.6's provider-pair rule across machines. |
| `cmd/hlwatch` | The guardians' tool (`PLAN.md` §5.8), both halves: from the holder's own node and two Solana providers, the flags and seats on all three objects, every pending decision on gno.land and on the admin program with its maturity, both sequence counters, every builder-made GovDAO proposal with no execution (pre-armed ones marked), the identity with Solana's supply and the dispatch index from two agreeing providers, the forged-id rule, the client's trusted header and record against the node; `watch` alarms the holder on changes; `flag`/`unflag` set the holder's flag on gno.land and on the pausable ISM and the admin program in one stroke; `version` prints the build hash. |
| `cmd/hlrecover` | The members' tool (`PLAN.md` §5.8, §6.5): `version` (the build hash the notice pins), `status` (the admin program's pending decisions and their maturities), `body` (a decision's bytes, decoded), `execute` (a matured decision, by any funded key). The recovery commands it carried until plan v35 — `init`, `sign`, `submit`, `record`, `close`, `settle` — are cut (M3k). |
| `cmd/hlsol`, `cmd/hlkey` | `hlsol setup` (the six deployments and §5.8's setup step in its order, resumable, done when no human key holds an authority), `hlsol audit` (§5.8's list, every RPC call printed), `send`, `balance`, `keygen` (local runs only); signer key files and package addresses. |
| `local/e2e.sh` | Everything on one machine, from nothing, under the light client: `hlsol setup` deploys and sets up the six programs on a local validator (our three built at factor 8); GovDAO (gnodev's test1 as its member) launches the realms; three signers, the updater, a relayer and a monitor run; 500 BUBBLE and 10 GNOT go to Solana through the pausable ISM and the light-client ISM from real local headers, 200 BUBBLE come back as a claim the relayer's own `Claim` loop pays with the fee first; lock = supply + owed + voided − lost holds on both routes and the monitor's identity lines say so; a GovDAO proposal made with a builder is seen pre-armed by `hlwatch`, then executed into a queued decision the relayer leaves alone until it matures; a decision from r/hlgov is delivered to the admin program and recorded pending at its factor-8 maturity, and a cancel of it executes at recording; `hlsol audit` prints a setup that matches §5.8; `hlwatch status` is green. |
| `cmd/hlsite`, `site/` | bridge.gno.world (`SITE.md`, M5): the pages (Home, Bridge, Status, Help; plain ES modules under `connect-src 'self'`, the wallet adapters written here) and the backend that proxies every chain read into a read-only API (`/api/chains`, `/api/<chain>/status`, `/api/<chain>/transfer/<ref>`) and builds one unsigned Solana burn for the wallet to sign (`/api/<chain>/burn-tx`); chain-agnostic by the `Other` interface, Solana the one chain; `-mock` serves a canned deployment; `make -C bridge site-qa` runs the Go tests and the Playwright suite; `site/deploy.sh` for the box, not run. |
| `notes/` | The gno object-hash gap: a reproduction, a tested fix patch, the issue draft. |

## Measured locally (2026-10-10, a test validator with mainnet's feature set)

The test validator's genesis charges rent at 6,960 lamports per byte; mainnet and devnet charge 5,080 (read from
`getMinimumBalanceForRentExemption` the same day), so the rents below are given at both.

| | |
|---|---|
| gno `TransferRemote` | 33.9M gas, plus storage |
| gno `Process` (2 ed25519) | 29.9M gas |
| Solana delivery (2 secp256k1 under Hyperlane's multisig ISM — the plan's light-client delivery is measured in `PLAN.md` §6.3, ≈169–171k CU for a transfer; mint, a new token account) | 245k compute units; 0.000005 SOL fee + 0.0013 SOL rent locally (0.00095 on mainnet) for the delivery record + 0.0021 locally (0.0015 on mainnet) for a recipient's first token account (from the route's ATA payer) |
| Solana `TransferRemote` | 0.0022 SOL locally (0.0016 on mainnet) of rent for the dispatch record, paid by the sender |
| Program rent (from the mainnet binaries' sizes, at mainnet's rate) | mailbox 1.07 SOL, each token 1.82, multisig ISM 0.84 (measured; not deployed in the plan since v16) |

## Running it locally

```
bridge/local/e2e.sh
```

needs Go, `gnodev` and `gnokey`, Python 3, and the Solana CLI 3.0.14 (macOS arm64 tarball from anza-xyz/agave
releases; `SOLBIN` points at its `bin`). `local/dump-programs.py DIR` fetches Hyperlane's mainnet builds the first
time. Two lessons it encodes: the test validator must run mainnet's feature set (`--clone-feature-set`, and
mainnet's Token-2022) — its own defaults break Token-2022's metadata realloc; and gnodev loads `examples` lazily
while genesis skips the lazy loader, so the example packages the realms import — GovDAO's tree included, found by
walking imports — are staged beside them. The launch values a run cannot know (the signers' keys, the BUBBLE
router) are swapped into the staged source, as BUBBLE's premine is.

## Before mainnet

`PLAN.md` §5 (what changes), §5.9 (the acceptance list) and §11 (what is Jae's to decide).

# Building the Ethereum bridge — milestones, lanes, done

The spec is `PLAN.md` (this directory) read over the Solana plan v36 (`bridge/PLAN.md`); this file is the work plan.
Started 2026-10-14. Every build, test and run happens on the devbox (`root@<devbox>`, `/root/src/<lane>`),
never on the Mac: edit on the Mac, `rsync -az --delete --exclude .git --exclude out --exclude cache
--exclude node_modules <worktree>/ root@<devbox>:/root/src/<lane>/`, run there over ssh (nohup + a log for
anything over a minute).

## Layout (private repo, branch `bridge-eth`; public repo `gnolang/bridges` under `ethereum/` by
`bridge/publish/export.sh`)

| Here | Public | What |
|---|---|---|
| `eth/bridge/` | `ethereum/bridge/` | the plan, the build and audit records, the Go module `bubblerumble/eth/bridge` (public `github.com/gnolang/bridges/ethereum/bridge`), which imports the Solana module's `gno`, `gnolight` and `hl` packages through a `replace` |
| `eth/bridge/contracts/` | `ethereum/bridge/contracts/` | the Foundry project: `src/` ours, `lib/` pinned dependencies (Hyperlane 12.2.0, OpenZeppelin 4.9.3, forge-std), `test/` |
| `eth/realm/` | `ethereum/realm/` | this deployment's six realms (module paths `gno.land/r/g1leu8d2…/eth/hl*`); the three pure packages are the Solana tree's `realm/p/*`, imported at their own paths |
| `eth/scripts/` | `ethereum/scripts/` | the realm test runner (stages `realm/p/*` and `eth/realm/*`), the e2e |

## Toolchain (devbox)

- Foundry 1.8.5 (`/root/.foundry/bin`: forge, anvil, cast); solc 0.8.33 (forge fetches it), EVM `cancun`.
- gno v1.2.0 (`/root/.cache/gno-toolchains/v1.2.0/{gno,gnodev,gnokey,gnoland}`, built from the tag at
  `/root/src/gno-v1.2.0`, which is also the GNOROOT for `gno test`).
- Go 1.27.1 (`/usr/local/go`).

## The lanes

**E-M1 — the realms** (`eth/realm/`, `eth/scripts/gnotest.sh`). The Solana bridge's six realms copied and carried to
Ethereum per PLAN.md §5.1–§5.3 and §13: module paths under `…/eth/`, the launch origin Ethereum's domain, Ethereum
recipients and routers (`0x` addresses, EIP-55 when mixed case, left-padded; the zero address and the router
refused), hlgov's kinds in §13's Ethereum encodings with `HlAdmin` as the recipient, every page's words. Every
Solana realm test carried over and passing, new tests for every changed rule (each failing on the unchanged Solana
code where it can), the interrealm checker clean, the specs of `docs/gno-specs/` read first (AGENTS.md).

**E-M2a — `Ed25519.sol`** (`eth/bridge/contracts/src/lib/Ed25519.sol`, `Sha512.sol`). PLAN.md §6.6. Gates: agreement
with Go's `crypto/ed25519` on RFC 8032, Wycheproof, the "Taming the many EdDSAs" cases and ≥ 100,000 differential
cases (§5.9); gas per verification measured and reported.

**E-M2b — the contracts** (`eth/bridge/contracts/`). `GnoLightClient` (with the tm2 libraries: the header leaves'
hash and fields, the precommit sign bytes, the validator leaf and set hash, the commit rule, `SimpleProof`, the
results leaf and the `Dispatch` event), `PausableGate`, `HlAdmin`, against the interfaces in `src/interfaces/`;
Hyperlane 12.2.0 and OpenZeppelin 4.9.3 vendored at their release bytes; the setup of PLAN.md §5.8 as a Foundry
script the tests run (the Go `hleth setup` follows the same order); tests on gnoland-1's own fixtures
(`bridge/programs/fixtures/*.json`, converted by a Go generator over `gnolight`), the whole delivery path through
the real `Mailbox` and `HypERC20`, every kind of §13, every timing and hold rule, invariant tests; gas measured.

**E-M3 — the machines and tools** (`eth/bridge/`: `eth/` the Ethereum client, `agent/`, `cmd/hlethbridge` signer ·
updater · relayer · monitor, `cmd/hleth` setup · audit · send · balance, `cmd/hlwatch`, `local/e2e.sh`). PLAN.md
§5.5–§5.9 and §6.1's cadence; the Solana machines' rules carried over where the plan says "as". The e2e: anvil +
gnodev, everything from nothing, transfers both ways, a decision delivered and executed, a cancel, a flag and an
unpause, `hleth audit` clean, `hlwatch status` green.

**E-M4 — bridge.gno.world** (`bridge/cmd/hlsite`, `bridge/site`): Ethereum as a second chain behind hlsite's
`Other` interface (reads by plain JSON-RPC), the send and burn flows with an EIP-1193 wallet, the Playwright suite
at both widths; Ethereum shown as "not yet deployed" until a deployment's `site.json` lists it.

**Audits** (`AUDIT.md`): the Solana scheme's seven audits a round — plan red team, plan fact check, conformance,
the realms, the contracts, the Go, the assumptions — until a round returns nothing above medium in every audit.

## Merges

Each lane works in its own worktree and branch (`eth-m1`, `eth-m2a`, `eth-m2b`, `eth-m3`, `eth-m4`) from
`bridge-eth`, commits as Jae with no trailers, and is merged into `bridge-eth` when its gates are green on the
devbox. `bridge-eth` merges into `bridge` once the audit loop converges.

## Open after the lanes (folded in the first code audit round)

- E-M1 Q3: each route's `init` refuses a `launchRouter` that differs from hlmailbox's compiled token address (today
  only the tests catch a mismatch).
- E-M1 Q6: an Ethereum burn credited to one of the Solana deployment's realm addresses goes to the sweep queue, as
  one to this deployment's realms does (the stage step swaps both namespaces' addresses).
- E-M1 Q1 (documented, no change): after a fresh Ethereum deployment (§6.4) the realms refuse only the new router;
  the site and the tools refuse the live deployment's addresses from their config.
- E-M1 Q4 (accepted): `p/hlroute`'s five error texts say "Solana" — an immutable shared package; the routes reword
  what they render.
- Fees (plan §5.5, §11.5): p/hlroute keeps ONE fee per route for both directions (the send's fee and the claim's
  first installment), so the Ethereum routes cannot charge more toward Ethereum only. With E-M2a's measured verifier
  (≈221K gas a precommit; an update ≈1.5M gas at 6 validators) the proposal is 10 GNOT and 200 BUBBLE each way,
  minimums 20 GNOT and 400 BUBBLE, the header bought only when the waiting fees cover it. The tree still compiles
  Solana's 5/10 GNOT and 100/200 BUBBLE because the carried-over tests assume them: the fold adapts the tests, then
  sets the figures (Jae's to confirm).
- From F1 (for the realms audit's fold): this deployment's own hlverifier joins each route's realm list (a credit to
  it is swept); kind 9's builder refuses every address of the deployment as a successor; the Solana list covers the
  six launch realms only (a later Solana successor is not covered — documented).
- From F5 (for round 3's contracts fold): kind 9 compares the successor's ed25519 binding, its code hash, the pkg-path
  hash and the routes too — bindings that grant no power and that only finishSetup reads; after a verifier swap, a
  pkg-path change or a route moved by kind 6, every later successor's finishSetup would fail for good. Decided: drop
  those four from kind 9's equality (keep the power-granting twelve), saving HlAdminExec's bytes.
- From F6 (for round 3's realms fold): the decision-recipient swap names the recipient it replaces and executes only
  while that is still the recipient (a stale or pre-armed swap cannot point decisions at a retired HlAdmin, Q-F6b);
  kind 8 may roll a proxy back to its own launch implementation (one of the 16, Q-F6c); kind 5's keys and kind 7's
  beneficiary refuse the 16 deployment addresses; the credit and sweep votes canonicalize an address before comparing
  (an upper-case realm or zero address passes today); after an undo kind 9 still refuses the early successor
  (Q-F6a, documented). The fees are now 20/40 GNOT and 400/800 BUBBLE (F6).
- From F8 (for round 3): `finishSetup`'s check 24 (each token's supply 0) fails any successor's setup once the bridge
  holds a supply — make it launch-only; `ethx.ReadBindings` reads the eleven views as required and the five others as
  optional (its comments are stale).
- Round 3's folds (2026-10-14/15; notes in each lane's record, ~/.config/bubblerumble-jarvis/eth-bridge/fold-r3/):
  F10 the realms (the 2,048-byte body cap and plain-quoted pages, the render-gas gate, the launch-set checks, announced
  successors refused as payees, the paged `decisions.json/<from>`, the titles `eth/<realm>: <kind>`, payees re-checked at
  apply, hlmailbox's json written without `strconv.Quote`); F11 the contracts (check 34 in HlAdmin, check 24 launch-only,
  kind 8's re-checks and its 2,000,000-gas call, a start ≤ 2^32, the Ed25519 vector families); F12 the Go trust path
  (read-twice with the delivered count, the finalized floor, the persisted ahead-stop, hlwatch's green rule, layouts for
  "immutable", the encoding watch, the providers' refusals, server limits, the config's required contracts); F13a the Go
  spend path (booked-only delivery and leaf pricing, the stranger's update, old headers to un-strand, MsgCall re-emits,
  EIP-8037 creations, the results leaf's relayer half with a re-emit backoff, prices dated); F13b `hleth` and the
  executor (UNVERIFIED without a gno source, the trust root held to gno.land, the governed audit, expected locks, the
  executor's cursor, the decision pages, ReadBindings split, the e2e's assertions); F14 the site (EIP-55 only, both
  verifiers refused, sixteen contracts listed, no self-delivery, the Solana site's chart and token addresses merged).
  Already resolved by them: F8's check 24 and ReadBindings items above; E2-RL5 stays as decided (kind 12's floor is the
  launch route's, plan e8).
- From round 3, open (for round 4 and Jae): a string that is not valid UTF-8 in any event or panic message beside a
  dispatch keeps its block unprovable from public RPC JSON (needs a results source of our own, §11); after §6.4's
  exit, route migrations need the old r/hlgov retired and the enrolment note's writer settled (E3-CF5, F10's notes);
  GovDAO's "Executor created in" is the executor's own claim on gno v1.5, so `hlwatch check -proposal` is advisory until
  r/hlgov records its builders' requests (F12's notes); the Sepolia/mainnet stage step is not built (E3-CF7).

# E-M1 notes — the Ethereum deployment's realms

Lane E-M1 of `BUILD.md`: the gno.land realms of the Ethereum deployment, built 2026-10-14 by carrying the live
Solana bridge's seven realms (`realm/hlmailbox`, `hlverifier`, `hlgnot`, `hlbubble`, `hlfees`, `hlgov`, `hlgnot2`,
at `bridge` 200311da) to Ethereum per `PLAN.md` §0, §4, §5.1–§5.3 and §13, with the red team's ER7, ER10 and ER11
folded in (relayed by the coordinator; plan e2 will say the same). This file is the record: what changed in each
realm and the plan line it follows, every reading where the plan left room, the questions, the gno-specs sections
checked, the test results and the commands. "§x" alone is the Solana plan's section (`bridge/PLAN.md`), which the
Ethereum plan carries over with its §0 substitutions; "eth §x" is this directory's `PLAN.md`.

**How to read the change.** Commit `cacebb9c` is the seven realms copied with the mechanical path moves only (module
paths, the realms' imports of each other and the `namespace` constant under `…/eth/`, the routes' `bubblePath`
pinned to the BUBBLE token's live path). Every Ethereum change is in the commits after it, so
`git diff cacebb9c -- eth/realm` shows exactly what this deployment changes; the formatting is its own last realm
commit (`bf313556`, `git diff -w` shows it is import order and line breaks).

## What is where

- `eth/realm/<name>/` — the seven realms, modules `gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/<name>`
  (no `private` in any `gnomod.toml`). They import the Solana tree's pure packages at their own, deployed paths —
  `gno.land/p/g1leu8d2…/{hyperlane,decisions,hlroute}/v0`, unchanged — and the BUBBLE token realm at its live path
  (`gno.land/r/g1leu8d2…/bubble`).
- `eth/realm/hlmailbox/ethaddr.gno` — the deployment's one parser and writer of Ethereum addresses
  (`ParseEthAddress`, `EthAddress`); `ethdeploy.gno` — the Ethereum deployment's contracts, compiled once
  (`EthContractAt`, `EthContractName`), and the routes' one rule for a send's recipient (`RefusedEthRecipient`).
- `eth/realm/*/vectors_test.gno` — signed test messages from Ethereum (origin 31337), made by the generator in the
  appendix.
- `eth/scripts/gnotest.sh` — the staged runner (the Solana `scripts/gnotest.sh` carried over): stages `realm/p/*`, the
  BUBBLE token realm and `eth/realm/*`, runs `gno test -v` in each realm and prints a pass/fail count; one realm by
  name; `ETH_REALM_DIR` stages a scratch copy (the negative controls); `GNOTEST_LINT=1` adds `gno lint`.

## What changed, realm by realm

### hlmailbox (eth §5.1)

- **The launch origin is Ethereum's Hyperlane domain**: `EthereumMainnet = 1` on chain id `gnoland-1`, else
  `testEthereumDomain`, 31337 in the tree (anvil's chain id, the local run's), a `const` on a line of its own so a
  Sepolia stage step's one `sed` on `^const testEthereumDomain = uint32(…)` rewrites it to 11155111. `solanaOf` is
  `ethereumOf`; `SolanaMainnet`/`SolanaLocal` are gone. `LaunchOrigin()` stays the launch verifier's origin, the
  routes' domain and r/hlgov's destination.
- **`ParseEthAddress(s) ([32]byte, error)` and `EthAddress([32]byte) string`** (`ethaddr.gno`): the one place an
  Ethereum address is read and written (the brief: "prefer a function in one realm the others already import").
  Accepted: `0x` and 40 hex digits — all lower case, all upper case, or mixed case that must equal its EIP-55 checksum
  (keccak256 of the lower-case hex, gno v1.2.0's native `crypto/keccak256`); or 64 hex digits, with or without `0x`,
  whose first 24 are zero. Result: the address left-padded to 32 bytes, as Hyperlane carries it. Refused: 32 bytes
  whose top twelve are not zero, a bad checksum, any other length, a non-hex digit, 40 hex digits without `0x`. The
  zero address parses (kind 3 names it); every caller refuses it where the plan does. `EthAddress` writes `0x` and
  the EIP-55 form when the top twelve bytes are zero, else all 64 digits (never a truncation).
- **The Ethereum deployment's contracts** (`ethdeploy.gno`, ER10): every address the setup step creates (eth §5.8)
  — the five proxies (the mailbox, token 0, token 1, the light client, the gate), HlAdmin, the ProxyAdmin, the
  MerkleTreeHook, the ProtocolFee hook and the five launch implementations — fourteen launch values, each a
  placeholder on a line of its own (`launchEth…`; digit-only patterns, and the two tokens the routes' own
  `launchRouter`s), parsed at deploy (a malformed or zero one fails the deploy). `EthContractAt(i)` and
  `EthContractName(k)` read them; the page lists them ("## Ethereum contracts") and the json carries
  `"ethContracts"`. r/hlgov takes HlAdmin and the five proxies from here (one list for the deployment, not two).
- **`RefusedEthRecipient(k, router)`**: the routes' one rule for a send's recipient (eth §5.2 and ER10): the zero
  address, the route's own router, any address at or below 0xffff (the precompiles 0x01–0x11 and 0x100, and the rest
  of the range the protocol reserves), and every contract of the list; it returns the reason, or "".
- **Words**: every Solana word about the remote chain in comments, panics and the page became Ethereum's (the page's
  "bridge to Ethereum … the Dispatch event its light client, GnoLightClient, proves", "domain: …; Ethereum: …",
  "enrolled on Ethereum by kind 6"). Nothing else moved: the Dispatch event, Reemit, Process, the verifier slot, the
  signers, seats, flags, routes, succession, the governor and the yield are the Solana realm's byte for byte.

### hlverifier, hlfees (paths and words)

The verifier is chain-agnostic — the message names its origin and the checkpoint is Hyperlane's on every chain — so
only its package comment and its tests changed (origin 31337, the regenerated vectors). hlfees: the package comment.

### hlgnot, hlbubble, hlgnot2 (eth §5.2)

- **The recipient of a send is an Ethereum address**, read by `hlmailbox.ParseEthAddress` (so the top twelve bytes
  are zero by construction: Hyperlane's `bytes32ToAddress` would revert the delivery for good otherwise) and refused
  by `hlmailbox.RefusedEthRecipient(to, st.Router)`: the zero address (an ERC-20 refuses to mint to it), the route's
  own router (its token contract), an address at or below 0xffff, every contract of the Ethereum deployment (the other
  token, the mailbox, the client, the gate, HlAdmin, the ProxyAdmin, both hooks, the five implementations) — each
  would leave what is minted where nobody can move it — and then, as before, a realm of the bridge (`isRealm`). The
  send's other rules (the direct user call, the exact envelope, the allowance, the fee) are unchanged.
- **The router is the HypERC20 proxy's Ethereum address.** `launchRouter` is a standalone `const` on its own line
  (`^const launchRouter = "…"` for the stage step), a PLACEHOLDER: the vectors' test route, the low twenty bytes of
  sha256("hlgnot test route") / sha256("hlbubble test route") in EIP-55 case (hlgnot2 compiles hlgnot's); it equals
  hlmailbox's token 0 / token 1 (the tests check). `NewSetRouterRequest` parses its argument the same way and writes
  the router into the description in EIP-55 case (the Solana realm wrote the string as typed); `DraftRouter` still
  refuses zero and the router itself.
- **Pages**: a line `router on Ethereum: <EIP-55>`; `RouterAddress()` (new) returns it; the json gains
  `"routerAddress"`. `Router()` and the json's `"router"` keep the 32 bytes in hex, as the Solana realm's do.
- **p/hlroute's words**: the shared state machine (deployed, immutable) writes "Solana" into three decision
  descriptions (the maximum's raise order, the apply refund, the write-off) and into the page's identity line. The
  routes pass those through `ethWords` (`strings.ReplaceAll(s, "Solana", "Ethereum")`): the descriptions are built
  from addresses, ids and amounts only, so nothing a caller types is touched. p/hlroute's **panics** still say Solana
  (raised inside the package): `no kind 6 has enrolled this route on Solana yet`, `a stranded send waits for a kind 12
  on Solana`, `after hlwatch's Solana check`, `only a send delivered on Solana after its refund is written off`, `the
  token's router on Solana (no enrolment note yet)` — Q4.
- **Words** in the realms themselves: Ethereum, the HypERC20, the token contract where Solana's program was.

### hlgov (eth §5.3, §6.5, §13; ER7, ER11)

- `Destination()` is hlmailbox's launch origin (Ethereum's domain); the first decision recipient is HlAdmin's address
  and the five proxies are hlmailbox's compiled contracts (`EthHlAdmin`, `EthMailbox … EthGate`). The seats are this
  realm's own launch values, `launchEthSeat1…6`, each an Ethereum address on its own line (placeholders).
- Every address parameter is read by `mustAddr` = `hlmailbox.ParseEthAddress`: a non-zero top twelve bytes is refused
  at the builder (HlAdmin would spend the decision, eth §13), a bad checksum likewise. Descriptions and pages write
  addresses in EIP-55 case. A code hash (kinds 8, 9) is `mustHash`: 64 hex digits, with or without `0x`, never zero.
- The kinds, byte for byte (`kind u8 ‖ seq u64 BE ‖ params`):
  - 1 cancel, 2 unpause (object 0 the gate, 1 HlAdmin), 12 set mint limits — unchanged encodings; words only.
  - 3 `token u8 ‖ ism 32`: the gate's address or the zero address.
  - 5 `count u8 ‖ (seat u8 ‖ key 32)…`, keys Ethereum addresses, never zero, no address twice (in any spelling), ≤ 4
    entries, ≤ 2 guardian seats; the mirror is `EthSeat` (was `SolSeat`); a key that holds a seat as last set here is
    warned about in the description, as on Solana.
  - 6 `token u8 ‖ domain u32 ‖ router 32`: the router an address (top twelve zero); for gno.land's domain the token's
    own live route, as on Solana; the description appends the route's path then.
  - 7 `fee u64 ‖ beneficiary 32`: the fee in wei, at most `maxProtocolFee = 10^15` (0.001 ETH, ER11; plan e1 said
    0.01), the beneficiary non-zero.
  - 8 `proxy 32 ‖ implementation 32 ‖ codehash 32 ‖ call` (ER7): the proxy one of the five (named in the description),
    anything else refused — HlAdmin included, which is no proxy; the implementation non-zero and not the proxy; the
    code hash non-zero; the call the rest of the body, given in hex (`""` or `"0x"` for none), at most 407 bytes
    (`maxUpgradeCall` = 512 − 9 − 96), refused above with its own message; the description ends "no call (upgrade)" or
    "then call 0x… (N bytes, upgradeAndCall)".
  - 9 `successor 32 ‖ codehash 32`: never HlAdmin itself (the current recipient) nor a former recipient (R29), checked
    when made and when executed, as on Solana; the successor and the code hash non-zero.
  - 4, 10, 11: no builder (`NewSetClientLayoutRequest`, which refused every call, is gone; the page says so).
- `set decision recipient` takes an Ethereum address; `Recipient()`, `Former()`, `EthSeat(n)` and the new `Proxy(i)`
  return EIP-55 strings; the json's `"recipient"` and `"former"` are EIP-55 too and it gains `"proxies"`.
- `MaxDecisionBody` stays 512 (eth §5.3 names it among HlAdmin's rules); its comment and panic no longer give
  Solana's packet as the reason. Every other rule — the sequence and `sync sequence`, the salted cancel, the enrolment
  note and its clearing, the governor and the yield, the deadlines — is the Solana realm's.

## Readings where the plan left room

1. **Spellings.** `0x` must be lower case (`0X` refused); 40 hex digits need `0x`; the 64-digit form takes `0x` or not
   and any case (it is a bytes32, which EIP-55 does not cover); surrounding spaces are trimmed. An address with no
   letters is accepted as written (it is its own checksum).
2. **The zero address** parses and is refused by the caller: the send's recipient, the router (DraftRouter's "all
   zeros"), kind 5's keys, kind 6's router, kind 7's beneficiary, kind 8's implementation, kind 9's successor and the
   decision recipient. Kind 3 takes it (the plan's "or zero").
3. **The refused recipients** are the compiled list plus the route's router now: a router swapped in by a later
   decision is refused by `st.Router` as long as it is the router, and the deployment's launch contracts stay refused
   forever; a contract of a later, fresh Ethereum deployment is not in the list (Q1).
4. **"At or below 0xffff"** is read as the 20-byte address's value, the zero address included (refused first, with
   its own reason).
5. **One list.** The deployment's Ethereum contracts live in hlmailbox, which every route and r/hlgov import; r/hlgov
   reads HlAdmin and the five proxies there rather than compiling them again. The routes keep their own
   `launchRouter` (the brief), equal to hlmailbox's token 0 / token 1 at launch (Q3).
6. **Readers' formats.** The routes keep `Router()` and json `"router"` in hex (the 32 bytes) and add EIP-55
   (`RouterAddress`, `"routerAddress"`); r/hlgov's `Recipient`, `Former`, `EthSeat`, json `"recipient"`/`"former"` are
   EIP-55 strings (the Solana realm's were hex); hlmailbox's json adds `"ethContracts"` — E-M3's tools read these (Q5).
7. **p/hlroute's Solana words** are renamed in descriptions and on the page (`ethWords`), not in panics (Q4).
8. **Kind 4 has no builder** at all (the brief: "kinds 4/10/11 have no builder"), where the Solana realm kept one that
   refused every call.
9. **Kind 6's router for a foreign domain** must be a 20-byte address (the brief: "every address parameter refuses a
   non-zero top 12 bytes"); the Solana builder took any 32-byte key there (Q2).
10. **Kind 8** keeps the Solana builder's "distinct" (the implementation is not the proxy) and "set" (the hash is not
    zero); kind 9 refuses a zero code hash the same way — HlAdmin would spend either (no deployed contract's
    `EXTCODEHASH` is zero), so this only refuses early. Kind 8's call is hex with or without `0x`; an odd digit count
    or a non-hex digit is refused; `"0x"` alone is no call.
11. **Kind 7 at 0 wei** is allowed (the launch fee is 0, eth §5.5).
12. **`isRealm`** is this deployment's six realms plus every route and dropped route the mailbox registers, as on
    Solana; the Solana deployment's realms are not in it (Q6).
13. **The vectors** (appendix): origin 31337, senders and routers Ethereum addresses (the routes' the low twenty bytes
    of sha256("<realm> test route"), a stranger router 0x6666…66, the mailbox tests' sender 0x5e5e…5e), the checkpoint
    eth §5.6's — our MerkleTreeHook as the hook (here a test address, 0x8383C6fE6904FFB33349a199EFe5836a869D2f3E),
    root 0, index the nonce — signed by the same three test signers, so `launchSigners` is unchanged.
14. **Titles** of the pages (`# The bridge's mailbox`, `# GNOT across chains`, …) are unchanged; their first sentence
    names the Ethereum bridge.

## Questions (none changes a rule; each is for Jae or the plan)

- **Q1.** The refused list is the launch deployment's. After a fresh Ethereum deployment (the plan's exit, §6.4),
  only the new token (as the route's router) is refused; the new mailbox, HlAdmin and the rest are not, since the
  realms are immutable. Accept, or have the fresh deployment's notice say so?
- **Q2.** Kind 6 for a foreign domain refuses a router whose top twelve bytes are not zero, so a Solana-style 32-byte
  router can never be enrolled on the Ethereum tokens. Intended (this deployment is gno.land ↔ Ethereum only)?
- **Q3.** Should the routes' `init` refuse a deploy whose `launchRouter` differs from hlmailbox's token 0 / token 1 (as
  every realm refuses a governor that is not the mailbox's)? Today the tests catch a mis-staged tree, if they are run on
  it. (hlgnot2, a later successor, would compare with hlgnot's router instead.)
- **Q4.** p/hlroute's five panics that say Solana (listed above): accept on this deployment, or a p/hlroute v1 with
  chain-neutral words (a new package path, the routes moved to it)?
- **Q5.** r/hlgov's json changed format (`recipient`, `former` EIP-55; `proxies` added), the routes' json gained
  `routerAddress` and hlmailbox's `ethContracts`: E-M3 (hlwatch, the relayers) should read these — tell them, or keep
  hex there?
- **Q6.** Refuse at send and sweep at credit the Solana deployment's realm addresses too? A burn on Ethereum to the
  Solana hlgnot's address would credit a claim paid into a realm whose counters do not hold it; the plan says "as the
  Solana plan §5.2", whose list is one deployment's.
- **Q7.** Accept `0X` and bare 40-hex spellings for convenience? (Refused now: the strict reading of "`0x` and 40 hex".)
- **Q8.** The vector generator lives in this file's appendix (this lane may not touch `bridge/` or the Go module):
  move it into `eth/bridge`'s module as a test beside the Solana `bridge/hl/gen_test.go`?
- **Q9.** Not this lane's, seen while checking: `python3 scripts/check-interrealm.py` on the repo's own tree fails on
  `realm/bubbletreasury/treasury.gno:223 func Do` (a secondary `rlm` read without `IsCurrent()`), and the seven Solana
  bridge realms are not gofmt-clean (18 files differ from `gofmt`: import order, alignment).

## The gno-specs checks (AGENTS.md) — sections checked and how

Read first: `docs/gno-specs/gno-interrealm-v2.md`, `interrealm_v2.adr.md`, `gno-security-guide.md`,
`gno-ai-contract-review.md`. What this lane added or changed was checked against them rule by rule; the rest of the
code is the Solana realms', audited there (bridge/M1-NOTES.md M1e and after), and unchanged.

- **Interrealm v2 §2–§4 (contexts, borrow rules).** `ParseEthAddress`, `EthAddress`, `EthContractAt`,
  `EthContractName` and `RefusedEthRecipient` are /r/hlmailbox non-crossing functions. Called from a route or r/hlgov,
  borrow rule #1 runs their bodies under hlmailbox's storage context; they write no state of hlmailbox (they read only
  its compiled `ethContracts`), so the borrow can change nothing. Their arguments (`st.Router`, the parsed address)
  arrive as copies and what they return is a value: a `[32]byte` (allocated in hlmailbox's frame; every caller copies
  it into its own state — a struct literal, a package var, an array element — and §3.2(3) stamps an unnamed type's
  copy with the copying realm), a `string`, an `error`. TestSettings' router swap and every hlgov kind store such
  values and pass.
- **§5 (crossing functions, `cur`).** No crossing function was added; `TransferRemote`, the builders and `Govern` keep
  their `cur` first and read identity only from their own live `cur` (the closures' `mustBeGovernor`). No secondary
  `realm` parameter was added: `scripts/check-interrealm.py` over `eth/realm` passes (134 functions).
- **§5.3 / guide §5.7 (realm values ephemeral).** No `realm` value is stored anywhere new.
- **§8 (conversion guards) / §3.2 (construction).** No conversion to or construction of another realm's declared type
  was added (`[32]byte` is unnamed; hlmailbox's `Key` is untouched).
- **§9 (panics across boundaries).** A parse error or a refusal reason is returned, and the caller panics in its own
  frame, so a refused send or proposal aborts the transaction as before; nothing half-written.
- **§10 (method values).** None returned.
- **§11.3 public API checklist / guide §5.1, §8 / review checks 3 and 8.** New exported functions —
  `hlmailbox.ParseEthAddress`, `EthAddress`, `EthContractAt`, `EthContractName`, `RefusedEthRecipient`, the routes'
  `RouterAddress`, `hlgov.EthSeat`, `hlgov.Proxy` — return values or strings, never a pointer into state; none takes
  an interface or a function value (§11.3's third and fourth points, guide §5.3–§5.4, review checks 4–5: nothing new to
  canonical-type-check, no callback). The new exported constants (`EthMailbox … EthGateImpl`, `EthContracts`) are
  indices.
- **Guide §3 (A), (B), (C).** No `/p/`-typed state added, nothing embedded, no caller-supplied callback invoked.
- **Guide §5.5 / review check 2 (payment guards).** hlgnot's `TransferRemote` keeps `cur.Previous().IsUserCall()` and
  `runtime.AssertOriginCall()`; the recipient checks run before the envelope is read, refusing nothing that pays.
- **Guide §5.6, §5.8 / review checks 1 and 9.** Caller identity only from `cur`; no `unsafe.PreviousRealm`. hlgnot's
  `chain/runtime/unsafe` import is for `OriginSend` alone, as on Solana.
- **Review check 10 (Render input).** No `Render` path parsing changed; the new page lines print realm state (the
  router, the contracts, the proxies) and builder-written descriptions whose addresses are normalised EIP-55 and whose
  call is hex (no caller text).
- **Review check 11 (GetCoins).** Not used.
- **§12 (MsgCall, MsgRun, MsgAddPackage).** `TransferRemote` stays a MsgCall crossing function; the launch values are
  parsed at `addpkg` by each realm's `init`/var initialisers, which panic on a malformed placeholder (a bad stage step
  fails the deploy, never deploys a wrong address).
- **Interrealm ADR.** `cur`/`cross(cur)` use unchanged; no `unsafe.CurrentRealm`; `testing.SetRealm` in the tests only.

## Tests and results (devbox, gno v1.2.0)

Last run: HEAD `bf313556` (the realms and the runner as committed), `GNOTEST_LINT=1 eth/scripts/gnotest.sh` on the
devbox (gno v1.2.0 at `/root/.cache/gno-toolchains/v1.2.0/gno`, GNOROOT `/root/src/gno-v1.2.0`), 2026-10-14 ≈10:10
UTC, exit 0. Each realm takes 16–28 s on the shared devbox.

| realm | passed | failed | gno lint |
|---|---|---|---|
| r/eth/hlmailbox | 27 | 0 | ok |
| r/eth/hlverifier | 3 | 0 | ok |
| r/eth/hlfees | 2 | 0 | ok |
| r/eth/hlgnot | 11 | 0 | ok |
| r/eth/hlbubble | 8 | 0 | ok |
| r/eth/hlgov | 13 | 0 | ok |
| r/eth/hlgnot2 | 3 | 0 | ok |
| **all** | **67** | **0** | |

The Solana realms carry 63 test functions; all 63 are here, adapted (the vectors, the domains, the addresses, the
words), and 4 are new: hlmailbox's `TestEthereumOrigin`, `TestParseEthAddress` and `TestEthereumContracts`, hlgov's
`TestEthereumKinds`.

The other gates, on the same tree:

- `check-interrealm.py` over `eth/realm` (the shadow invocation below): "134 function(s) take a realm parameter; every
  secondary one is either unused or proved with IsCurrent() first", exit 0.
- `gofmt -l eth/realm/*/*.gno`: nothing; `gno fmt -diff -imports=false eth/realm/...`: no diff.
- `gno lint`: ok in all seven realms.
- The Solana tree's own suite, `scripts/gnotest.sh` from the synced tree (`realm/` untouched by this lane): all 17
  packages ok — p/checkpoint, p/grc20votes, p/governor, p/hyperlane, p/decisions, p/hlroute, r/bubble,
  r/bubbletreasury, r/bubblerumble5, r/wbubble, r/hlmailbox, r/hlverifier, r/hlfees, r/hlgnot, r/hlbubble, r/hlgov,
  r/hlgnot2 — exit 0.

**What the tests hold, rule by rule.**

- The launch origin by chain id: `TestEthereumOrigin` (1 on gnoland-1, 31337 on every other chain id, the page and
  the json say Ethereum, no verifier for Solana's domains), `TestDomainAndHook`.
- Address parsing: `TestParseEthAddress` — EIP-55's four mixed-case examples accepted and written back exactly, each
  also in all lower and all upper case; every single letter's case flipped in each refused as a bad checksum; EIP-55's
  all-caps and all-lower examples; the 64-digit form with and without `0x`, in either case; spaces trimmed; the zero
  address parses; refused: 40 hex digits without `0x`, `0X`, 41 and 43 characters, a non-hex digit in the 40- and the
  64-digit forms, the empty string, a gno address, a Solana key, a non-zero top twelve bytes (three spellings). The
  golden bytes are EIP-55's own strings decoded by `encoding/hex` in the test, never the realm's output.
- The refused recipients (ER10): `TestEthereumContracts` — the fourteen contracts named, listed on the page and in
  the json, and each refused; the zero address; the route's own router; 0x01, 0x11, 0x100, 0x1000 and 0xffff refused;
  0x10000 and two ordinary addresses not. At the routes: hlgnot's `TestTransferRemote` (the zero address in two
  spellings, its router in three, 0x01, 0x100, 0xFFFF, the other token, the mailbox, the client, the gate, HlAdmin,
  the ProxyAdmin, both hooks, two implementations, a bad checksum, bare 40 hex, a non-hex digit, a non-zero top twelve
  bytes, a Solana key — nothing moved), then sends to the same address in its three spellings whose message bytes
  (the recipient left-padded, the amount a big-endian uint256) are checked against hex decoded apart; hlbubble's
  `TestTransferRemote` likewise (a precompile, the other token, HlAdmin, its router, a bad checksum, a non-zero top),
  no BUBBLE moved; hlgnot2's `TestMigration` (its router, the zero address, 0x02, HlAdmin).
- The router: the routes' `TestLaunchValues` (`launchRouter` is the vectors' route, `Router()` its 32 bytes,
  `RouterAddress()`, the page line and the json's `routerAddress`; hlgnot2's equals hlgnot's); hlgnot's `TestSettings`
  (a case slip refused, the zero address, a non-zero top, the router itself in upper case, the 64-digit spelling
  normalised to EIP-55 in the description, then the swap applied and shown).
- p/hlroute's words: the maximum's description says Ethereum (`TestSettings`), as do the apply refund's
  (`TestSweepVoidAndGuards`, hlgnot2's `TestMigration`) and the write-off's (`TestWriteOff`); the pages carry no
  "Solana".
- hlgov's kinds: `TestKinds` (kinds 2, 3, 6, 7, 8, 9 with the Ethereum bodies), `TestGuardians` (kind 5 with
  addresses, the mirror, the warnings), and `TestEthereumKinds`: every kind's body against golden hex written out in
  the test — kind 3 (the gate in its 64-digit spelling), kind 5 (a mixed-case and an all-caps key), kind 6 (domain 10),
  kind 7 at 10^15 wei (`00038d7ea4c68000`) and at 0, kind 8 with no call, with `8129fc1c`, and with a 407-byte call (a
  512-byte body), kind 9 with its code hash; each address parameter of kinds 3, 5, 6, 7, 8 (both), 9 and the decision
  recipient refusing a non-zero top, a bad checksum and bare 40 hex; kind 7 refusing 10^15 + 1, 10^16 and 2^64 − 1;
  kind 8 accepting each of the five proxies by name and refusing HlAdmin (launch and current), two ordinary addresses
  and one a byte off a token, a zero or an equal implementation, a zero, short or non-hex code hash, a 408-byte, an odd or a
  non-hex call; kind 9 refusing HlAdmin itself, a former recipient, the zero address and a zero code hash.
  `TestLaunchValues` checks hlmailbox's compiled contracts against the placeholders spelled out in the test and the
  tokens against the routes' routers.

**Negative controls.** "A test for a changed rule should fail against the unchanged Solana copy where that is
meaningful":

- *The whole Solana copy.* The Ethereum verifier tests run against commit `cacebb9c`'s realms (the Solana logic at
  the Ethereum paths) with this tree's `hlverifier_test.gno` and vectors: `TestRegisteredAtDeploy` fails ("not the
  registered, active launch verifier"), `TestTwoGroups` aborts ("hlmailbox: no verifier for domain 31337"),
  `TestThreshold` passes (the verifier is chain-agnostic: it checks the message's own origin). The other realms'
  Ethereum tests call what the Solana copy has not got (`RouterAddress`, `ParseEthAddress`, `EthSeat`, …), so against
  it they do not compile — no verdict; the mutants are those controls, one rule at a time.
- *Mutants* (appendix B): each puts one changed rule back as the Solana realm has it, or drops the Ethereum check, in a
  scratch copy of `eth/realm`, and runs the named realms' suites. All 21 fail, at the test that holds the rule
  (later tests of the same ordered suite also fail where they need state the failing one would have made, "+ later"):

| mutant | the rule put back | fails |
|---|---|---|
| origin-solana | `ethereumOf` answers Solana's domains | hlmailbox: TestEthereumOrigin, TestDomainAndHook, TestLaunchValues, TestBeforeLaunch, TestGovernor, TestSetVerifier, TestProcess |
| parse-no-checksum | a mixed case is accepted unchecked | hlmailbox: TestParseEthAddress |
| parse-no-top12 | a 64-digit value's top twelve bytes unchecked | hlmailbox: TestParseEthAddress |
| gnot-recipient-base58 | the recipient read by `hl.ParseKey32` (Solana's) | hlgnot: TestTransferRemote + later |
| route-router-recipient-allowed | the route's own router not refused | hlmailbox: TestEthereumContracts; hlgnot, hlbubble: TestTransferRemote + later; hlgnot2: TestMigration |
| route-low-recipient-allowed | addresses at or below 0xffff not refused | the same four |
| route-contract-recipient-allowed | the deployment's contracts not refused | the same four |
| gnot-, bubble-, gnot2-refusals-skipped | a route ignores `RefusedEthRecipient` | hlgnot, hlbubble: TestTransferRemote; hlgnot2: TestMigration |
| gnot-router-base58 | `NewSetRouterRequest` reads base58/hex and writes it as typed | hlgnot: TestSettings + later |
| gnot-solana-words | the descriptions keep p/hlroute's "Solana" | hlgnot: TestSettings + later |
| bubble-recipient-base58 | as gnot-recipient-base58 | hlbubble: TestTransferRemote + later |
| gov-fee-max-lamports | kind 7's maximum 50,000,000 (Solana's lamports) | hlgov: TestKinds, TestEthereumKinds + later |
| gov-fee-max-plan-e1 | kind 7's maximum 10^16 (plan e1, before ER11) | hlgov: TestKinds, TestEthereumKinds + later |
| gov-upgrade-call-dropped | kind 8 without its call (before ER7) | hlgov: TestEthereumKinds |
| gov-upgrade-any-proxy | kind 8 takes any proxy | hlgov: TestKinds, TestEthereumKinds + later |
| gov-successor-no-codehash | kind 9 is `successor 32` alone (Solana's body) | hlgov: TestKinds, TestEthereumKinds |
| gov-address-any-32 | an address parameter takes any 32-byte key (Solana's rule) | hlgov: TestEthereumKinds |
| gov-recipient-hex | `Recipient()` in hex (Solana's reader) | hlgov: nine tests |
| gov-ism-any | kind 3 takes any ISM | hlgov: TestKinds + later |

## Commands

```sh
# on the Mac: sync the worktree to the devbox
rsync -az --delete -e 'ssh -i ~/.ssh/id_ed25519_kourt' --exclude .git --include '/eth/***' --include '/realm/***' \
  --include '/scripts/***' --include '/docs/***' --include '/AGENTS.md' --exclude '*' \
  ~/src/jaekwon/bubblerumble-eth-m1/ root@<devbox>:/root/src/eth-m1/
# on the devbox, in /root/src/eth-m1
eth/scripts/gnotest.sh                         # the seven realms
eth/scripts/gnotest.sh hlgov                   # one realm
GNOTEST_LINT=1 eth/scripts/gnotest.sh          # with gno lint
scripts/gnotest.sh                             # the Solana tree's own suite
# the interrealm checker over eth/realm: the script scans <its dir>/../realm, so run it from a shadow whose realm/
# is eth/realm (scripts/ untouched)
S=$(mktemp -d); mkdir $S/scripts; cp scripts/check-interrealm.py scripts/gnosource.py $S/scripts/
ln -s $PWD/eth/realm $S/realm; python3 $S/scripts/check-interrealm.py; rm -rf $S
# formatting
/usr/local/go/bin/gofmt -l eth/realm/*/*.gno
/root/.cache/gno-toolchains/v1.2.0/gno fmt -diff -imports=false eth/realm/...
# the negative controls: the appendix's mutants.py (each mutant one Ethereum rule put back as the Solana realm has
# it, or dropped), run against a scratch copy of eth/realm
python3 mutants.py /root/src/eth-m1 <out dir>
# the vectors: in a copy of bridge/ (go.mod, go.sum, hl/), the appendix's generator copied in as hl/gen_eth_test.go
HL_GEN_ETH=<tree root> go test -count=1 -run TestGenEthVectors ./hl/
```

## Appendix A — the vectors' generator (`bridge/hl/gen_eth_test.go`)

The vectors (`eth/realm/{hlmailbox,hlverifier,hlgnot,hlbubble}/vectors_test.gno`) are made by this test, run inside a
copy of the Solana bridge's Go module (`bridge/go.mod`, `go.sum`, `hl/`), where it reuses `bridge/hl`'s Hyperlane
encoding, ed25519 signing and gno address derivation and `gen_test.go`'s `testAddress` and `mustB32`. It checks its
keccak256 and EIP-55 against EIP-55's examples before writing. Copy it in as `hl/gen_eth_test.go`, then
`HL_GEN_ETH=<tree root> go test -count=1 -run TestGenEthVectors ./hl/` (the devbox's Go 1.27.1).

```go
package hl

// The Ethereum deployment's realm vectors (eth/bridge/M1-NOTES.md, "The vectors"): copied into bridge/hl/ as
// gen_eth_test.go and run there with HL_GEN_ETH=<repo root> go test -run TestGenEthVectors ./hl/ — it writes
// eth/realm/{hlmailbox,hlverifier,hlgnot,hlbubble}/vectors_test.gno. It reuses the package's own Hyperlane encoding,
// ed25519 signing and gno address derivation, and the Solana generator's (gen_test.go) testAddress and mustB32.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	ethTestDomain = uint32(31337) // hlmailbox's testEthereumDomain: the origin of every message here
	ethNamespace  = "gno.land/r/" + gnoAddr + "/eth/"
)

// ethAddr is a test Ethereum address: the low twenty bytes of sha256(name), carried left-padded to 32 bytes.
func ethAddr(name string) (out [32]byte) {
	h := sha256.Sum256([]byte(name))
	copy(out[12:], h[12:])
	return out
}

// eip55 writes a 32-byte Ethereum address (top twelve bytes zero) as 0x and its EIP-55 checksum case.
func eip55(a [32]byte) string {
	for _, c := range a[:12] {
		if c != 0 {
			panic("eip55: not an address")
		}
	}
	lower := hex.EncodeToString(a[12:])
	h := Keccak([]byte(lower))
	out := []byte(lower)
	for i, c := range out {
		if c < 'a' || c > 'f' {
			continue
		}
		nib := h[i/2] >> 4
		if i%2 == 1 {
			nib = h[i/2] & 0x0f
		}
		if nib >= 8 {
			out[i] = c - 'a' + 'A'
		}
	}
	return "0x" + string(out)
}

// ethHook is the merkle tree hook the test checkpoints name: on Ethereum the signers sign with our MerkleTreeHook's
// address as the hook and root 0 (eth §5.6, §13); here a test address.
var ethHook = ethAddr("eth test merkle tree hook")

type ethVector struct {
	name   string
	msg    Message
	signed []int
}

func (v ethVector) gno() string {
	raw := v.msg.Bytes()
	c := Checkpoint{Hook: ethHook, Index: v.msg.Nonce} // root 0
	var sigs []EdSignature
	for _, i := range v.signed {
		sigs = append(sigs, EdSignature{uint8(i), SignEd25519(testEdKey(i), c, raw)})
	}
	return fmt.Sprintf("\t%sMsg  = %q\n\t%sMeta = %q\n", v.name, hx(raw), v.name, hx(GnoMetadata(c, sigs)))
}

func ethGnoFile(pkg string, vs []ethVector, extra string) string {
	var b strings.Builder
	b.WriteString("package " + pkg + "\n\n")
	b.WriteString("// Generated by the vector generator of eth/bridge/M1-NOTES.md (\"The vectors\"), run as a test of bridge/hl; do\n")
	b.WriteString("// not edit. Messages from Ethereum (the test domain 31337) to a gno test chain, each with the checkpoint the signers\n")
	b.WriteString("// sign for a burn on Ethereum (eth §5.6, §13: our MerkleTreeHook as the hook — here a test address — root 0, index =\n")
	b.WriteString("// the nonce), signed by test validators: ed25519 seeds sha256(\"hyperlane gno test validator <i>\").\n")
	b.WriteString("const (\n")
	b.WriteString(extra)
	var keys []string
	for i := 0; i < 3; i++ {
		keys = append(keys, hx(testEdKey(i).Public().(ed25519.PublicKey)))
	}
	fmt.Fprintf(&b, "\ttestValidators = %q\n\n", strings.Join(keys, ","))
	for _, v := range vs {
		b.WriteString(v.gno())
	}
	b.WriteString(")\n")
	return b.String()
}

func TestGenEthVectors(t *testing.T) {
	root := os.Getenv("HL_GEN_ETH")
	if root == "" {
		t.Skip("HL_GEN_ETH=<repo root> writes the Ethereum deployment's realm vectors (eth/realm/*/vectors_test.gno)")
	}
	// the checks the vectors rest on: keccak and EIP-55 against EIP-55's own examples
	if k := Keccak(nil); hx(k[:]) != "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470" {
		t.Fatalf("keccak256 of nothing: %x", k)
	}
	for _, w := range []string{"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359", "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB", "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb"} {
		var a [32]byte
		b, _ := hex.DecodeString(strings.ToLower(w[2:]))
		copy(a[12:], b)
		if eip55(a) != w {
			t.Fatalf("eip55 %s: %s", w, eip55(a))
		}
	}
	body := []byte("hello, gno")
	rcpt := mustB32(testAddress("hlm-recipient"))
	sender := pad20(0x5e)
	mailbox := []ethVector{
		{"inHello", Message{3, 7, ethTestDomain, sender, DomainGnoTest, rcpt, body}, []int{0, 2}},
		{"inMainnet", Message{3, 8, ethTestDomain, sender, DomainGnoMainnet, rcpt, body}, []int{0, 1}},
		{"inOneSig", Message{3, 9, ethTestDomain, sender, DomainGnoTest, rcpt, body}, []int{1}},
		{"inNoGroupA", Message{3, 10, ethTestDomain, sender, DomainGnoTest, rcpt, body}, []int{1, 2}},
	}
	const one = 1_000_000
	stranger := pad20(0x66) // a router no route enrolls
	// BUBBLE: the fee is 100 BUBBLE, so amounts under it are swept
	route := ethAddr("hlbubble test route")
	self := mustB32(GnoPackageAddress(ethNamespace + "hlbubble"))
	alice, bob := mustB32(testAddress("hlb-alice")), mustB32(testAddress("hlb-bob"))
	dirtyBob := bob
	for i := 0; i < 12; i++ {
		dirtyBob[i] = 0xff
	}
	bubble := []ethVector{
		{"backAlice300", Message{3, 0, ethTestDomain, route, DomainGnoTest, self, TokenMessage(alice, 300*one, nil)}, []int{0, 1}},
		{"strangerRoute", Message{3, 1, ethTestDomain, stranger, DomainGnoTest, self, TokenMessage(alice, 150*one, nil)}, []int{0, 1}},
		{"dirtyBob250", Message{3, 2, ethTestDomain, route, DomainGnoTest, self, TokenMessage(dirtyBob, 250*one, nil)}, []int{1, 2}},
		{"backAlice500", Message{3, 3, ethTestDomain, route, DomainGnoTest, self, TokenMessage(alice, 500*one, []byte("memo"))}, []int{0, 2}},
		{"backCarol50", Message{3, 4, ethTestDomain, route, DomainGnoTest, self, TokenMessage(mustB32(testAddress("hlb-carol")), 50*one, nil)}, []int{0, 1}},
	}
	// GNOT: the fee is 5 GNOT; the route realm's own address is the recipient the messages name
	groute, groute2 := ethAddr("hlgnot test route"), ethAddr("hlgnot test route 2")
	gself := mustB32(GnoPackageAddress(ethNamespace + "hlgnot"))
	fees := mustB32(GnoPackageAddress(ethNamespace + "hlfees"))
	galice, gbob, gcarol, gdave := mustB32(testAddress("hlg-alice")), mustB32(testAddress("hlg-bob")), mustB32(testAddress("hlg-carol")), mustB32(testAddress("hlg-dave"))
	gnot := []ethVector{
		{"inAlice100", Message{3, 0, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(galice, 100*one, nil)}, []int{0, 1}},
		{"inBob3", Message{3, 1, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(gbob, 3*one, nil)}, []int{1, 2}},
		{"inFees50", Message{3, 2, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(fees, 50*one, nil)}, []int{0, 2}},
		{"inAlice30000", Message{3, 3, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(galice, 30_000*one, nil)}, []int{0, 1}},
		{"strangerRoute", Message{3, 4, ethTestDomain, stranger, DomainGnoTest, gself, TokenMessage(galice, 20*one, nil)}, []int{0, 1}},
		{"inCarol1000", Message{3, 5, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(gcarol, 1_000*one, nil)}, []int{0, 1}},
		{"inDave1000", Message{3, 6, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(gdave, 1_000*one, nil)}, []int{1, 2}},
		{"newRouteAlice7", Message{3, 7, ethTestDomain, groute2, DomainGnoTest, gself, TokenMessage(galice, 7*one, nil)}, []int{0, 1}},
		{"oldRouteAlice8", Message{3, 8, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(galice, 8*one, nil)}, []int{0, 2}},
		{"oldRouteAlice9", Message{3, 9, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(galice, 9*one, nil)}, []int{1, 2}},
		{"inBob60", Message{3, 10, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(gbob, 60*one, nil)}, []int{0, 1}},
		// the router addressing another realm of the bridge (AQ1): a successor takes its predecessor's messages once
		// the last page of a migration is in; a route that succeeded none refuses it before the mailbox is asked
		{"inOtherRealm60", Message{3, 11, ethTestDomain, groute, DomainGnoTest, mustB32(GnoPackageAddress(ethNamespace + "hlbubble")), TokenMessage(gbob, 60*one, nil)}, []int{0, 1}},
	}
	write := func(dir string, vs []ethVector, extra string) {
		p := filepath.Join(root, "eth", "realm", dir, "vectors_test.gno")
		if err := os.WriteFile(p, []byte(ethGnoFile(dir, vs, extra)), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", p)
	}
	write("hlmailbox", mailbox, "")
	write("hlverifier", mailbox, "")
	write("hlbubble", bubble, fmt.Sprintf("\ttestRoute = %q // the low 20 bytes of sha256(\"hlbubble test route\"), EIP-55: the compiled launch router\n", eip55(route)))
	write("hlgnot", gnot, fmt.Sprintf("\ttestRoute  = %q // the low 20 bytes of sha256(\"hlgnot test route\"), EIP-55: the compiled launch router\n\ttestRoute2 = %q // the low 20 bytes of sha256(\"hlgnot test route 2\"), EIP-55\n", eip55(groute), eip55(groute2)))
	t.Logf("hook %s, stranger %s", eip55(ethHook), eip55(stranger))
}
```

## Appendix B — the negative controls (`mutants.py`)

Run on the devbox from anywhere: `python3 mutants.py /root/src/eth-m1 <out dir> [mutant …]`; it copies `eth/realm` to
`/tmp/eth-mut-<name>`, applies one edit (refusing to run if it does not match exactly once), runs
`ETH_REALM_DIR=… eth/scripts/gnotest.sh <realm>` for each named realm and prints the failing tests.

```python
#!/usr/bin/env python3
"""Negative controls for eth M1: each mutant puts one changed rule back the way the Solana realms have it (or drops
the Ethereum check), in a scratch copy of eth/realm, and runs the realm's suite there; the record is which tests
fail. Usage: mutants.py <tree root> <out dir> [name ...]"""
import os, re, shutil, subprocess, sys

ROOT, OUT = sys.argv[1], sys.argv[2]
ONLY = set(sys.argv[3:])

MUTANTS = [
    # name, realm to test, file, old, new — each the Solana rule (or no rule) in place of the Ethereum one
    ("origin-solana", "hlmailbox", "hlmailbox/mailbox.gno",
     "		return EthereumMainnet\n	}\n	return testEthereumDomain",
     "		return uint32(1399811149)\n	}\n	return uint32(1399811150)"),
    ("parse-no-checksum", "hlmailbox", "hlmailbox/ethaddr.gno",
     "hasHexLetter(h, 'a', 'f') && hasHexLetter(h, 'A', 'F') && eip55(b) != h",
     "false"),
    ("parse-no-top12", "hlmailbox", "hlmailbox/ethaddr.gno",
     "			if c != 0 {\n				return out, errors.New(\"not an Ethereum address: \" + s + \" is 32 bytes whose top twelve",
     "			if c != c {\n				return out, errors.New(\"not an Ethereum address: \" + s + \" is 32 bytes whose top twelve"),
    ("gnot-recipient-base58", "hlgnot", "hlgnot/hlgnot.gno",
     "	to, err := hlmailbox.ParseEthAddress(recipient)\n	if err != nil {\n		panic(\"hlgnot: the recipient is \" + err.Error())",
     "	to, err := hl.ParseKey32(recipient)\n	if err != nil {\n		panic(\"hlgnot: the recipient is \" + err.Error())"),
    ("route-router-recipient-allowed", "hlmailbox,hlgnot,hlbubble,hlgnot2", "hlmailbox/ethdeploy.gno",
     "	case k == router:", "	case false && k == router:"),
    ("route-low-recipient-allowed", "hlmailbox,hlgnot,hlbubble,hlgnot2", "hlmailbox/ethdeploy.gno",
     "	case lowEthAddress(k):", "	case false && lowEthAddress(k):"),
    ("route-contract-recipient-allowed", "hlmailbox,hlgnot,hlbubble,hlgnot2", "hlmailbox/ethdeploy.gno",
     "	if name := EthContractName(k); name != \"\" {", "	if name := EthContractName(k); false && name != \"\" {"),
    ("gnot-refusals-skipped", "hlgnot", "hlgnot/hlgnot.gno",
     "	if why := hlmailbox.RefusedEthRecipient(to, st.Router); why != \"\" {", "	if why := hlmailbox.RefusedEthRecipient(to, st.Router); false && why != \"\" {"),
    ("gnot-router-base58", "hlgnot", "hlgnot/hlgnot.gno",
     "	k := mustAddr(router)\n	return propose(cur, st.DraftRouter(k, hlmailbox.EthAddress(k)))",
     "	k, err := hl.ParseKey32(router)\n	if err != nil {\n		panic(err.Error())\n	}\n	return propose(cur, st.DraftRouter(k, router))"),
    ("gnot-solana-words", "hlgnot", "hlgnot/hlgnot.gno",
     "	d.Text = ethWords(d.Text) //", "	d.Text = d.Text //"),
    ("bubble-recipient-base58", "hlbubble", "hlbubble/hlbubble.gno",
     "	to, err := hlmailbox.ParseEthAddress(recipient)\n	if err != nil {\n		panic(\"hlbubble: the recipient is \" + err.Error())",
     "	to, err := hl.ParseKey32(recipient)\n	if err != nil {\n		panic(\"hlbubble: the recipient is \" + err.Error())"),
    ("bubble-refusals-skipped", "hlbubble", "hlbubble/hlbubble.gno",
     "	if why := hlmailbox.RefusedEthRecipient(to, st.Router); why != \"\" {", "	if why := hlmailbox.RefusedEthRecipient(to, st.Router); false && why != \"\" {"),
    ("gnot2-refusals-skipped", "hlgnot2", "hlgnot2/hlgnot2.gno",
     "	if why := hlmailbox.RefusedEthRecipient(to, st.Router); why != \"\" {", "	if why := hlmailbox.RefusedEthRecipient(to, st.Router); false && why != \"\" {"),
    ("gov-fee-max-lamports", "hlgov", "hlgov/hlgov.gno",
     "maxProtocolFee = uint64(1_000_000_000_000_000)", "maxProtocolFee = uint64(50_000_000)"),
    ("gov-fee-max-plan-e1", "hlgov", "hlgov/hlgov.gno",
     "maxProtocolFee = uint64(1_000_000_000_000_000)", "maxProtocolFee = uint64(10_000_000_000_000_000)"),
    ("gov-upgrade-call-dropped", "hlgov", "hlgov/hlgov.gno",
     "impl[:]...), h[:]...), data...)", "impl[:]...), h[:]...))"),
    ("gov-upgrade-any-proxy", "hlgov", "hlgov/hlgov.gno",
     "		if proxyName(p) == \"\" {\n			panic(\"hlgov: \" + ea(p) + \" is none of the five proxies",
     "		if false {\n			panic(\"hlgov: \" + ea(p) + \" is none of the five proxies"),
    ("gov-successor-no-codehash", "hlgov", "hlgov/hlgov.gno",
     "	params := append(append([]byte{}, p[:]...), h[:]...)\n	return proposeKind(cur, KindSuccessor,",
     "	params := append([]byte{}, p[:]...)\n	return proposeKind(cur, KindSuccessor,"),
    ("gov-address-any-32", "hlgov", "hlgov/hlgov.gno",
     "	k, err := hlmailbox.ParseEthAddress(s)\n	if err != nil {\n		panic(\"hlgov: \" + err.Error())",
     "	k, err := hlmailbox.ParseEthAddress(s)\n	if err != nil {\n		k, err = hl.ParseKey32(s)\n	}\n	if err != nil {\n		panic(\"hlgov: \" + err.Error())"),
    ("gov-recipient-hex", "hlgov", "hlgov/hlgov.gno",
     "func Recipient() string { return ea(recipient) }", "func Recipient() string { return hx(recipient[:]) }"),
    ("gov-ism-any", "hlgov", "hlgov/hlgov.gno",
     "	if k != gate && k != ([32]byte{}) {", "	if false {"),
]

def run(name, realm, path, old, new):
    d = os.path.join("/tmp", "eth-mut-" + name)
    shutil.rmtree(d, ignore_errors=True)
    shutil.copytree(os.path.join(ROOT, "eth", "realm"), d)
    p = os.path.join(d, path)
    s = open(p, encoding="utf-8").read()
    n = s.count(old)
    if n != 1:
        return "MUTANT DID NOT APPLY (%d matches)" % n
    open(p, "w", encoding="utf-8").write(s.replace(old, new))
    logs = os.path.join(OUT, name)
    os.makedirs(logs, exist_ok=True)
    env = dict(os.environ, ETH_REALM_DIR=d, GNOTEST_LOGS=logs)
    lines = []
    for rl in realm.split(","):
        r = subprocess.run([os.path.join(ROOT, "eth", "scripts", "gnotest.sh"), rl], cwd=ROOT, env=env,
                           capture_output=True, text=True)
        out = r.stdout.splitlines()[0] if r.stdout else r.stderr[:300]
        log = open(os.path.join(logs, rl + ".log"), encoding="utf-8", errors="replace").read()
        fails = re.findall(r"^--- FAIL: (\w+)", log, re.M)
        panics = re.findall(r"^=== RUN   (\w+)\n(?:(?!=== RUN).*\n)*?panic: (.{0,140})", log, re.M)
        lines.append("%s | failed: %s | aborted: %s" % (out, ",".join(fails) or "-", ";".join("%s (%s)" % p for p in panics) or "-"))
    shutil.rmtree(d, ignore_errors=True)
    return ("\n" + " " * 35).join(lines)

for m in MUTANTS:
    if ONLY and m[0] not in ONLY:
        continue
    print("%-34s %s" % (m[0], run(*m)), flush=True)
```

## Fold F1 — the follow-ups the build left in the realms, before the first code audit (2026-10-14)

Lane F1, branch `eth-f1` from `bridge-eth` f5fb68f9 (plan e3), worktree `~/src/jaekwon/bubblerumble-eth-f1`, devbox
`/root/src/eth-f1`; plus one item the coordinator sent from plan e4 (`bridge-eth` b50c4997, §13). It touches
`eth/realm/**` and this file only. Each change has a test that fails on the code before it (run as below: the
working tree's tests over that commit's realm code, `oldcode` in the commands) and passes after, and negative
controls (appendix D) that take the rule back out. "Q3", "Q6" are this file's questions above.

| commit (in the order made) | change (numbered as the brief, the coordinator's item 6) |
|---|---|
| b8e2e882 | 1. a send refuses the Ed25519Verifier and HlAdminExec: the list is the sixteen contracts |
| 2ba4cc3a | 2. each route's `init` refuses a launch router that is not its token (Q3) |
| d9902cef | 3. kind 6's zero router: refused already; the refusal names its reason |
| 4fca0ebe | 6. kinds 8 and 9 refuse the empty code's hash beside zero (the coordinator's item, plan e4 §13) |
| d5a431c7 | 4. a burn crediting one of the Solana deployment's six realms is swept (Q6) |
| ae1b8a6d | 5a. the routes' tests read the compiled fee and minimum; two vectors re-signed |
| 2aac3aa7 | 5b. the launch fees are eth §5.5's proposal: 10/20 GNOT, 200/400 BUBBLE |
| 655c68b7 | five comment lines reflowed to the files' width |
| bf678de4 | these notes, first written |
| f0c7256d | review: hlgnot's six Solana burns re-signed from the route's router as it stands; four comments corrected |
| 29120df6 | review (2): hlgnot2's router rule tested against a swapped predecessor router |
| bc51abf8 | review (2b): hlmailbox refuses at deploy one address written on two lines of its list |

### 1. The Ed25519Verifier and HlAdminExec are refused as recipients (eth §5.2, ER10; M2-NOTES.md §1 G)

- **What.** `hlmailbox/ethdeploy.gno` lists sixteen contracts (eleven codes, five proxies): two placeholders on lines
  of their own, `launchEthEd25519Verifier = "0x2525…25"` (the client's `ED25519()`) and `launchEthHlAdminExec =
  "0x1212…12"` (`HlAdmin.exec()`, its CREATE), digit-only patterns no test uses for anything else; indices
  `EthEd25519Verifier` (14) and `EthHlAdminExec` (15) appended, so every earlier index keeps its place (r/hlgov's five
  proxies and HlAdmin, the e2e's swaps); names "the Ed25519Verifier" and "HlAdminExec"; json keys `ed25519Verifier`
  and `hlAdminExec`, hleth's deployment record's own. `RefusedEthRecipient` walks the list, so both are refused; the
  page and the json carry them.
- **Tests.** hlmailbox `TestEthereumContracts` (sixteen, each named, on the page, in the json, refused); hlgnot's and
  hlbubble's `TestTransferRemote` and hlgnot2's `TestMigration` send to each address and want "…, the
  Ed25519Verifier of this deployment on Ethereum" / "…, HlAdminExec of this deployment on Ethereum".
- **Fails before** (f5fb68f9's code): hlmailbox "14 contracts, not 16"; hlgnot `abort "hlgnot: send exactly
  15000000ugnot …" does not contain "…the Ed25519Verifier…"` (the recipient passed, the send went on to the envelope);
  hlbubble `expected an abort containing "the Ed25519Verifier of this deployment on Ethereum", got none` (the send
  went through); hlgnot2 `abort "invalid non-origin call" does not contain …`. Passes after.

### 2. A route's `init` refuses a launch router that is not its token (Q3)

- **What.** hlgnot's and hlbubble's `init` call `wrongRouter(st.Router)`, which holds the compiled router (as parsed)
  to hlmailbox's compiled token — `EthContractAt(EthToken0)` for GNOT, `EthToken1` for BUBBLE — and panics with
  "hlgnot: the compiled launch router 0x… is not the mailbox's token 0 (GNOT), 0x…: both must be the GNOT HypERC20
  proxy" (hlbubble's says token 1, BUBBLE). A tree whose stage step wrote the two apart now fails its addpkg, where it
  deployed before and only the tests, if run on it, would notice.
- **hlgnot2 (chosen).** Its launch values are "r/hlgnot's at this realm's deploy" (its own comment), so its `init`
  holds its router to r/hlgnot's as it stands then (`hlgnot.Router()`, the 32 bytes in hex): at launch that is the
  mailbox's token 0 — r/hlgnot's own init held it there — and after a router swap the swapped one, which hlmailbox's
  compiled list cannot know of; held to the list, a successor would be undeployable after a swap, or would compile a
  router its predecessor had left. The message: "hlgnot2: the compiled launch router 0x… is not
  gno.land/r/…/eth/hlgnot's router, 0x…: a successor compiles the router of the route it succeeds". The check runs
  before its `AnnounceSuccessor`.
- **Tests.** Each route's `TestLaunchValues`: the rule takes the compiled router and refuses, with the exact message,
  the other token (a stage step that swapped the two), the router one bit off, the zero address (and in hlgnot the
  vectors' second route).
- **Fails before** (b8e2e882). The unit test does not compile there (`undefined: wrongRouter`); the proof that
  counts is at the deploy: each route's tree staged with its `launchRouter` line rewritten to the other token (the
  stage controls, appendix D). With F1 the package stops at `init` — `gno.land/r/…/eth/hlgnot:0: hlgnot: the compiled
  launch router 0x051D52Fd… is not the mailbox's token 0 (GNOT), 0x0BA554Aa…: both must be the GNOT HypERC20 proxy`,
  no test run — and likewise hlbubble and hlgnot2; with b8e2e882's code the same slip loads, and only the assertions
  catch it (`TestLaunchValues: launch values: … router …051d52fd…`, then the burns "not from the router").
- **hlgnot2's rule, tested (from the review).** `TestSuccessorRouterRule` swaps r/hlgnot's router (a decision, 7 days),
  checks that the rule takes the swapped router and refuses the launch one hlmailbox's list still names, and swaps
  back so the migration drill runs as before. Held to the list instead (the control `c2-gnot2-held-to-list`), it
  fails; before, no test told the two rules apart.
- **2b, one address on two lines (from the review).** A stage slip writing one proxy as both tokens — and as both
  routes' routers — passes both routes' checks against one token. hlmailbox's list now goes through
  `distinctContracts` at deploy: "hlmailbox: token 0 (GNOT) and token 1 (BUBBLE) are both 0x0BA554Aa…: every contract
  of the deployment has its own address" (the sixteen are distinct deployments; the e2e's record check holds them to
  the same). `TestEthereumContractsDistinct` takes the compiled list and refuses copies with token 1 written as token
  0, HlAdminExec as HlAdmin, the last line as the first. Fails before (bf678de4): the test does not compile there;
  at the deploy, the slip staged on bf678de4's code loads (28 tests run, only `TestEthereumContracts`' literal
  catching it), and on the final code hlmailbox stops at `init` (the stage control `c2b-stage-token-twice`).

### 3. Kind 6's zero router (eth §13; the reason as plan e4 §13 words it)

- **What.** r/hlgov's kind 6 builder refused a zero router already (carried from the Solana realm: `k == zero ||
  domain == 0` → "the router and the domain must be set"), for every domain and both tokens. F1 keeps the rule and
  splits the refusal so it names its reason: "hlgov: the router is the zero address, which HlAdmin executes as
  unenrolling the domain: unenrolling is no decision of this table (eth §13)"; a zero domain keeps "hlgov: the domain
  must be set". The reason follows plan e4, which corrected e3 and the brief ("Hyperlane reads it as unenrolling"):
  `HlAdminExec._enrollRouter` calls `unenrollRemoteRouter` for a zero router, while the token's own
  `enrollRemoteRouter(domain, 0)` would leave the domain enrolled at zero (Router.sol `_routers.set`), a send there a
  message to address 0. The builder's comment says the same.
- **Test.** `TestEthereumKinds`: the zero router in three spellings (`0x` and 40 zeros, 64 zeros, `0x` and 64 zeros) ×
  domains 10, gno.land's own and Ethereum's 31337 × tokens 0 and 1, each refused with the message, nothing recorded or
  dispatched.
- **Fails before** (2ba4cc3a) on the wording alone (`abort "hlgov: the router and the domain must be set" does not
  contain …`), the rule being there; the control that holds the rule is the mutant `c3-zero-router-allowed` (the check
  dropped), which fails `TestEthereumKinds`.

### 4. A burn crediting one of the Solana deployment's realms is swept (Q6)

- **What.** `hlmailbox/soldeploy.gno` (new): `solanaNamespace = "gno.land/r/g1leu8d2…/"` — the operator's namespace
  without `eth/`, on a line of its own for the stage step — the six realms' addresses under it (`hlmailbox`,
  `hlverifier`, `hlgnot`, `hlbubble`, `hlfees`, `hlgov`; never its reference successor hlgnot2, which the Solana launch
  does not deploy) and `IsSolanaRealm(a address) bool`. Each route (hlgnot, hlbubble, hlgnot2) adds
  `|| hlmailbox.IsSolanaRealm(to)` where its credit rule asks `isRealm(to)` — `Process`'s sweep, and the recipient of
  `NewCreditRequest` and `NewSweepRequest` — so such a burn is swept, to be credited by id to its owner, and never paid
  into that realm's account by vote: "exactly as one to this deployment's own realms does". `isRealm` itself, the
  send's check, is unchanged (a send's recipient is an Ethereum address). The smallest change: one list in the realm
  the routes already import (as M1's reading 5 put the Ethereum contracts), three call sites a route.
- **Tests.** hlmailbox `TestSolanaRealms` (the six by their paths spelled in the test; not this deployment's six, not
  the Solana hlgnot2, not the token realm, not a user; the two namespaces one operator's). hlgnot
  `TestSolanaRealmsSwept`: six signed burns of 150 GNOT, one to each Solana realm (appendix C), each swept and no claim,
  owed and the sweep queue up by 900, the lock unmoved, both votes refusing each — after checking that 150 is at or
  above the fee and within the lock and that no Solana address is in this deployment's list, so it is the Solana rule
  that sweeps — and a send to the Ethereum address that is the Solana GNOT route's twenty bytes goes through (the
  send's check unaffected). hlbubble `TestSolanaRealmsSwept`, last, crafted under the any-verifier: the fee lowered at
  once to the launch fee, a burn to carol credited as a claim (the control), then the six swept and refused likewise,
  and the registered and dropped successor routes of `TestRouteAddressSwept` swept at that fee too (finding F-4).
  hlgnot2 `TestMigration`: six crafted burns to the Solana realms after its R39/AT6 ones, swept, the identity holding.
- **Fails before** (4fca0ebe): hlgnot, hlbubble and hlgnot2 `a burn to the Solana deployment's hlmailbox was not
  swept: swept 0, claim &{… g1q6y9qnuy607md0svpz5rj48tjxlfrmfgpvd2j3 150000000 …}` — a claim to the Solana hlmailbox's
  address; hlmailbox's unit test does not compile there (`undefined: IsSolanaRealm`). Passes after.
- **Corrected after the review** (f0c7256d). The six signed burns first came from the launch router, which
  `TestSettings` swaps away; the test passed only because the harness starts each test function's clock over, which
  reopened the old router's 7-day window (`RouterAccepts`: `OldUntil > now`). They are re-signed from the router the
  route holds by then (appendix C), and the test checks each burn's sender is that router. Against 4fca0ebe's code
  the corrected test fails as before (the burn to the Solana hlmailbox credited as a claim).

### 5. The fees (eth §5.5, §11.5)

- **5a, the tests read the compiled values** (ae1b8a6d, the figures unchanged). Wherever a test assumed Solana's 5/10
  GNOT or 100/200 BUBBLE it reads `Fee()`, `Minimum()`, `FeeFloor()` or `launchFee`: the amounts that must clear the
  minimum (every send refused for another reason, the envelope's "send exactly N"), "below the minimum" one GNOT (one
  BUBBLE) below it, what a claim pays (`100*gnot - Fee()`, an installment of 2,500 less the fee, frank's three claims
  less three fees), what the fee realm takes, the fee raise (`Fee()+gnot`, the minimum kept), the BUBBLE fee lowering
  (`Fee()-20*one`), the 496 small sends of hlgnot2's drill (`hlgnot.Minimum()` each, the lock derived). Every scenario
  whose meaning rests on an amount's side of the fee now checks it first (bob's 3 GNOT below it, the fee realm's 50 at
  or above it so the realm is what sweeps it, carol's 50 BUBBLE below, the 250/500/600 BUBBLE burns above, frank's
  60–80 GNOT and the R39/AT6 burns of 150 and 40 above and within the lock), so a later fee breaks a test by name
  instead of quietly testing another rule. Two vectors could not be read from the realm and were re-signed
  (appendix C): hlgnot's router-swap burns 7/8/9 → 70/80/90 GNOT (claims above the raised fee — 6 under Solana's
  figures, 11 under the plan's — as they were claims before; at 7 and 8 the plan's fee would sweep them and every
  later sweep total would move), and hlbubble's burn-back 300 → 600 BUBBLE (a send of it must clear the minimum, 400
  now, and the lock must return to 0 after it, so the R11 sweep check keeps its meaning). The generator is M1's plus
  those; hlmailbox's and hlverifier's vectors came out byte for byte, every other vector the same bytes. On the final
  tree, appendix A patched with appendix C (sha256 `72c5a8a2…`) regenerates all four vector files byte for byte (after
  `gofmt -w`, as M1's were committed). The suite passed under Solana's figures at this commit.
- **5b, the figures** (2aac3aa7): GNOT fee 10 and minimum 20, BUBBLE fee 200 and minimum 400, hlgnot2 as hlgnot; the
  fee floors (2 GNOT, 40 BUBBLE), the cap floors (10,000), the maximums (25,000) and every other value kept. The
  launch values' comments say they are eth §5.5's proposal and that §11.5 is Jae's.
- **What the figures move besides** (from the review): the per-message maximum can never be set below the launch
  minimum (`DraftMaximum`), so its floor rises to 20 GNOT and 400 BUBBLE; a burn below the fee is swept (`Credit`), so
  burns under 10 GNOT or 200 BUBBLE now wait for a vote crediting them by id. Both as the plan's figures intend.
- **The rules hold.** p/hlroute's `New` checks nothing, its builders and applies check: fee floor ≤ fee (2 ≤ 10;
  40 ≤ 200), fee ≤ a quarter of the cap floor (10 ≤ 2,500; 200 ≤ 2,500, `DraftFee`, `DraftCapFloor`), 0 < minimum ≤
  maximum (20, 400 ≤ 25,000; `DraftMaximum` never below the launch minimum); and the minimum above the fee (twice it,
  eth §5.5). Each route's `TestLaunchValues` now pins the figures and checks those rules on them.
- **Fails before** (ae1b8a6d, Solana's figures): `TestLaunchValues` in each route (`launch values: fee 5000000 min
  10000000 …`) and nothing else — the rest of the suite passes under both sets (the final tree's seven realms with
  Solana's figures put back: 67 of 70, the three pins failing). A third set (GNOT 25/60, BUBBLE 150/500: the
  `c5-*-figures` controls) again fails `TestLaunchValues` alone.

### 6. Kinds 8 and 9 refuse the empty code's hash (the coordinator's item, plan e4 §13)

- **What.** `mustHash` (both kinds' code hash) refuses keccak256 of nothing — computed in the realm,
  `keccak256.Sum256(nil)` — beside zero: "hlgov: the code hash is the empty code's hash (keccak256 of nothing), which
  no contract's code hashes to: HlAdmin could never execute the decision — it pends while the target holds no code
  (NoCodeYet) and is spent once it holds some (eth §13)" (zero's message ends the same). Since F2, `HlAdminExec._codeIs`
  reverts `NoCodeYet` for a target with no code (the decision pends) and compares hashes only for code, which hashes
  to neither, so such a decision could never execute. The comments of `mustHash`, `NewUpgradeRequest` and
  `NewTransferToSuccessorRequest` no longer say HlAdmin "would only spend" it; they say what e4 §6.5 and §13 say (no
  code pends; another hash or an EIP-7702 designator spends; the proposer and hlwatch read the deployed code before the
  vote).
- **Test.** `TestEthereumKinds`: the empty code's hash — the golden literal `c5d2460186f7…5d85a470` written in the
  test, never the realm's — bare, with `0x`, in capitals and with spaces, and zero bare and with `0x`, each refused by
  kind 8 and by kind 9 with the message; nothing recorded or dispatched; a hash one bit off the empty code's is an
  ordinary hash.
- **Fails before** (d9902cef): `expected an abort containing "hlgov: the code hash is the empty code's hash …", got
  none` — the builder took it. Passes after.

### Readings where the brief left room

1. **The two new contracts' placement**: appended (indices 14, 15), not beside HlAdmin and the client, so no index
   anyone reads moves.
2. **hlgnot2's router check** holds it to r/hlgnot's live router, not the mailbox's list (§2 above).
3. **Kind 6's message** names HlAdmin's reading, as plan e4 has it, not Hyperlane's as e3 and the brief had it.
4. **The vote paths** (credit by id, sweep payment) refuse the Solana realms too: the brief's "exactly as one to this
   deployment's own realms does" — a sweep entry is to be credited to its owner, never back to that realm.
5. **The empty code's hash** is computed in the realm (`keccak256.Sum256(nil)`), the golden literal written in the
   test.
6. **2b's distinctness** covers the whole list, not only the two tokens: any two lines equal is a stage slip.
7. **The send's check is pinned unaffected** by a test (a send to the Solana GNOT route's twenty bytes goes through)
   and a mutant (`c4-gnot-send-refuses-solana`), since the brief makes it a requirement; F-5 asks whether to keep it.

### Findings and questions (none changes a rule here; each is for the coordinator or Jae)

- **F-1, the e2e and F1's figures: answered by F3.** When F1 began, `eth/bridge/local/e2e.sh` hard-coded Solana's
  fees (a 10 GNOT send with a 5 fee, 500 BUBBLE approved with a 100 fee, 200 BUBBLE burned back against 100) and the
  fourteen contracts it swaps; under 5b its GNOT send would be refused. Fold F3 (bridge-eth 173c5c39, M3b-NOTES §F3.1
  item 10 and §F3.3 item 1) has since made the run read each route's fee and minimum from its json and swap every
  `launchEth<Name>` placeholder hlmailbox carries by name (`Ed25519Verifier` → the record's `ed25519Verifier`,
  `HlAdminExec` → `hlAdminExec`), so F1 needs no edit there; F3 checks the BUBBLE minimum stays above its fee (400 >
  200). `git merge-tree` of eth-f1 into that bridge-eth is clean (F1 touches no file F3 did).
- **F-2, `solanaNamespace` at the stage step.** The run stages under the dev namespace, where the line needs nothing.
  A mainnet stage step must write there the live Solana deployment's namespace — this deployment's own only under
  §11.8's recommendation (one operator namespace, `eth/` apart) — and should assert it: a value left at the dev
  namespace would make `IsSolanaRealm` match nothing, silently (the tests compare with the placeholders). Rendering the
  namespace and the six addresses on hlmailbox's page and json would let hlwatch check them (not done here).
- **F-3, the verifier realm is in no route's list, on either deployment.** The routes' `realms` (here and in the live
  Solana realms, `realm/hlgnot`, `realm/hlbubble`) name the mailbox, the two routes, hlfees, hlgov and the token realm,
  never the deployment's own hlverifier; hlverifier has no code that moves coins. A burn naming `…/eth/hlverifier` is
  credited as a claim and paid into an account nothing moves (Solana's likewise for its verifier). One line a route
  here (`chain.PackageAddress(namespace + "hlverifier")`, which would also refuse it at send); the Solana side is
  deployed. F1 left it alone (not in the brief); the Solana list F1 added includes the Solana hlverifier, as asked.
- **F-4, hlbubble's `TestRouteAddressSwept` was vacuous at `Process`.** Its burns of 150 and 120 BUBBLE to a registered
  and a dropped successor's address came while the fee stood at 3,000 BUBBLE (set in `TestBuilders`), so the fee alone
  swept them and the R39/AT6 rule at credit went untested on BUBBLE. F1's `TestSolanaRealmsSwept` re-runs both at the
  launch fee, a claim beside them as the control. (hlgnot's realm sweep at credit — the fees realm's 50 GNOT — and
  hlgnot2's R39/AT6 burns were not vacuous; they now check their amounts against the fee too.)
- **F-5 (question), the send's check and the Solana realms.** A send still refuses only this deployment's realms'
  twenty bytes as an Ethereum address (carried from Solana's "a realm address as a recipient on either side"). An
  Ethereum address equal to a Solana realm's twenty bytes is as keyless — refuse it too (one condition)? Kept
  unaffected here, as the brief asks.
- **F-8, the Solana list is the launch's six.** A successor a Solana migration names later — whose page prints it
  as the lock's holder, the very paste Q6 is about — is not among them, and the list is compiled (hlmailbox is
  immutable). Residual: hlwatch and the site could refuse such addresses as recipients of a burn's intent, or a
  governed, append-only list could live in a later mailbox.
- **F-6, a harness rule worth knowing.** `testing.SetRealm` binds to the frame it is called in: hlgnot's new send
  failed `IsUserCall` until the user was set in the test function's own frame — `envelope`'s override, set in the
  helper, did not reach the call, and `TestTransferRemote` passes only because it set alice in its own frame earlier
  (hlgnot2's tests document the same rule). Its sibling: each test function's clock starts over, so a window measured
  from an earlier function's time (an old router's 7 days) reopens — the review caught one F1 test resting on it.
- **F-7, for the audit: kind 9 to another contract of the deployment.** r/hlgov's kind 9 builder refuses HlAdmin and
  former recipients as the successor, not the deployment's other contracts (hlmailbox's list). None of them answers
  `predecessor()` (HlAdmin alone declares it), and `HlAdminExec._transfer` reads it by a plain call whose revert leaves
  the decision pending, never spent, so such a decision would wait until GovDAO cancels it; refusing the list at the
  builder is one condition (not done here: not in the brief).

### The independent review (a read-only agent over `git diff f5fb68f9..655c68b7 -- eth/realm`)

It found no bug in the realm code and nine points; each, and what became of it:

1. hlgnot's Solana test passed for the wrong reason (the old router's window, reopened by the harness's clock) —
   fixed, f0c7256d (§4 above). Its claim that the test *could not pass* missed the clock reset: it passed on the
   devbox, which is the trap.
2. This deployment's hlverifier is in no list — F-3; the two comments that said otherwise corrected (f0c7256d).
3. A wrongly staged `solanaNamespace` would go unnoticed — F-2 (an assertion at the stage step, the page).
4. The Solana list is frozen at the launch's six — F-8.
5. The `init` wiring is tested only by staging, and hlgnot2's rule was not told apart from the list's — the stage
   controls (appendix D) are the wiring's test; `TestSuccessorRouterRule` added (29120df6).
6. One proxy written as both tokens passes both routes' checks — 2b (bc51abf8).
7. The generator in appendix A still made the old vectors — it is appendix A plus appendix C's delta (updated).
8. The figures' side effects (the maximum's floor, small burns swept) — said in §5.
9. Two comments: the Solana fee realm does move what it holds (to its treasurer); the NoCodeYet behaviour is plan
   e4's §13, not this branch's e3 — both corrected (f0c7256d).

It also read `check-interrealm.py` as never covering `eth/realm` (the script scans `realm/`): it does here, through
M1's shadow directory whose `realm/` is `eth/realm` (the 134 functions are `eth/realm`'s).

### The gno-specs checks (AGENTS.md) — sections checked, for what F1 added or changed

- **Interrealm v2 §2–§4 (contexts, borrow rules).** `IsSolanaRealm`, `EthContractAt` (now sixteen), `EthAddress` and
  `hlgnot.Router`/`RouterAddress`, which the new code calls, are non-crossing `/r/` functions: borrow rule #1 runs each
  under its own realm's storage context; none writes; each returns a value (`bool`, a `[32]byte` copy, a `string`).
  `wrongRouter` and `mustHash` are the caller's own non-crossing helpers.
- **§3.2 (storage = authority).** The new state is hlmailbox's, allocated at its init: `solanaRealms` (an array of six
  addresses) and two entries in each compiled array; never returned by reference. **§3.3**: no `/p/` package touched.
- **§5, §5.2 (crossing functions, `cur`).** No crossing function and no secondary `realm` parameter added:
  `check-interrealm.py` over `eth/realm` counts 134 functions taking a realm parameter, as at M1, all clean. hlgnot2's
  `init(cur realm)` makes its new check before its crossing `AnnounceSuccessor`.
- **§5.3 / guide §5.7 (realm values ephemeral).** No realm value is stored.
- **§8 / §3.2 (conversion guards, construction).** No conversion to or construction of another realm's type.
- **§9 (panics across boundaries).** The new refusals panic in the realm's own frame before any write: `init`'s
  aborts the addpkg (§12.3), the builders' abort the proposal, `Process`'s sweep is a boolean into p/hlroute's
  `Credit` in the same call.
- **§10 (method values).** None.
- **§11.3 public API / guide §5.1, §8 / review checks 3, 8.** New exported: `IsSolanaRealm` (a bool), the indices
  `EthEd25519Verifier`, `EthHlAdminExec`; no pointer into state, no interface or function parameter (review checks 4,
  5: nothing to canonical-check, no callback).
- **§12.3 (MsgAddPackage).** The routes' `init` checks read compiled values only, never `PreviousRealm`.
- **Guide §3 (A)(B)(C).** No `/p/`-typed state, nothing embedded, no caller-supplied callback.
- **Guide §5.5 / review check 2 (payment guards).** `TransferRemote`'s `IsUserCall` and `AssertOriginCall` are
  unchanged and still after the recipient checks; the send's recipient rule is unchanged but for the two contracts.
- **Guide §5.6, §5.8 / review checks 1, 9.** No identity from an address parameter; no `unsafe.PreviousRealm`.
- **Review check 10 (Render).** Two more compiled lines on hlmailbox's page and json; no caller text.
- **Review check 11 (GetCoins).** Not used.
- **Interrealm ADR.** `cross(cur)` unchanged; no `unsafe.CurrentRealm`; `testing.SetRealm` in tests only (F-6).

### Tests and results (devbox, gno v1.2.0)

Last run: the realms at bc51abf8, `GNO=/root/.cache/gno-toolchains/v1.2.0/gno GNOTEST_LINT=1 bash
eth/scripts/gnotest.sh` on the devbox, exit 0.

| realm | passed | failed | gno lint |
|---|---|---|---|
| r/eth/hlmailbox | 29 | 0 | ok |
| r/eth/hlverifier | 3 | 0 | ok |
| r/eth/hlfees | 2 | 0 | ok |
| r/eth/hlgnot | 12 | 0 | ok |
| r/eth/hlbubble | 9 | 0 | ok |
| r/eth/hlgov | 13 | 0 | ok |
| r/eth/hlgnot2 | 4 | 0 | ok |
| **all** | **72** | **0** | |

Five functions are new (hlmailbox `TestSolanaRealms` and `TestEthereumContractsDistinct`, hlgnot and hlbubble
`TestSolanaRealmsSwept`, hlgnot2 `TestSuccessorRouterRule`); the rest grew.
The other gates on the same tree: `check-interrealm.py` over `eth/realm` (M1's shadow invocation) — "134 function(s)
take a realm parameter; every secondary one is either unused or proved with IsCurrent() first", exit 0; `gofmt -l`
nothing; `gno fmt -diff -imports=false eth/realm/...` no diff; the Solana tree's own `scripts/gnotest.sh` (realm/
untouched): all 17 packages ok.

**The end-to-end run with F1 merged** (beyond the brief: F3's run reads the realms' figures, so it is the check that
F1's realms work with the machines). bridge-eth 173c5c39 (F3 merged) with eth-f1 merged in — `git merge-tree` clean;
the devbox tree checked file by file against the merge git computes — and `eth/bridge/local/e2e.sh` with its own
state directory and ports (`BL=/root/src/eth-f1-e2e-state ANVIL_PORT=8845 GNO_PORT=26857`): PASS in 369 s on
bf678de4's realms and again in 376 s on bc51abf8's (log `/root/src/eth-f1-e2e-final.log`). In each: `hleth audit:
PASS`; "hlmailbox's contract list: 16 placeholders swapped", the two new ones among them; every realm deployed, the
routes' `init` checks passing against the deployed addresses (and hlmailbox's distinctness, the second time); "the
routes read: GNOT fee 10000000, minimum 20000000; BUBBLE fee 200000000, minimum 400000000", 20 GNOT and 1,000 BUBBLE
minted on Ethereum, 400 BUBBLE burned back and "paid, the 200000000 fee first"; the kind 7 decision, the cancel, the
flag and the unpauses; hlwatch GREEN; two alarm lines, `deliveries:paused` while the flag stood and its clear.

**Negative controls** (appendix D, `f1mutants.py`, on the final tree): every mutant fails at the test that holds its
rule, and every stage control stops its package at `init`:

| control | the rule taken out (or the slip made) | fails |
|---|---|---|
| c1-verifier-unlisted | the Ed25519Verifier's line given a fresh address, so the list no longer holds it | hlmailbox TestEthereumContracts; hlgnot TestTransferRemote + later; hlbubble TestTransferRemote; hlgnot2 TestMigration |
| c1-exec-unlisted | HlAdminExec's line likewise | the same four |
| c2-gnot-, c2-bubble-, c2-gnot2-rule-off | `wrongRouter` takes any router | the route's TestLaunchValues |
| c2-stage-gnot, -bubble, -gnot2 | the route's launch router line rewritten to the other token, as a stage slip | the package stops at `init` with the message; no test runs |
| c2-gnot2-held-to-list | hlgnot2's rule held to hlmailbox's token 0 instead of r/hlgnot's router | hlgnot2 TestSuccessorRouterRule |
| c2b-duplicates-allowed | hlmailbox takes one address on two lines | hlmailbox TestEthereumContractsDistinct |
| c2b-stage-token-twice | token 1's line written as token 0's, as a stage slip | hlmailbox stops at `init` with the message; no test runs |
| c3-zero-router-allowed | kind 6 takes the zero router | hlgov TestEthereumKinds |
| c4-list-empty | `IsSolanaRealm` answers no | hlmailbox TestSolanaRealms; hlgnot, hlbubble TestSolanaRealmsSwept; hlgnot2 TestMigration |
| c4-ROUTE-credit-off (×3) | the route's `Process` sweep ignores the Solana realms | that route's test (hlgnot, hlbubble TestSolanaRealmsSwept; hlgnot2 TestMigration) |
| c4-ROUTE-vote-credit-off (×3) | the route's credit vote takes a Solana realm | that route's test |
| c4-ROUTE-vote-sweep-off (×3) | the route's sweep vote takes a Solana realm | that route's test |
| c4-gnot-send-refuses-solana | the send's check (`isRealm`) refuses the Solana realms too | hlgnot TestSolanaRealmsSwept |
| c5-gnot-figures-25-60, c5-bubble-figures-150-500, c5-gnot2-figures-25-60 | a third set of launch figures | TestLaunchValues alone (its pin); every other test passes |
| c6-empty-codehash-allowed | kinds 8 and 9 take keccak256 of nothing | hlgov TestEthereumKinds |
| c6-zero-codehash-allowed | kinds 8 and 9 take zero | hlgov TestEthereumKinds |

```sh
# the suite, as at M1 (on the devbox, in /root/src/eth-f1)
GNO=/root/.cache/gno-toolchains/v1.2.0/gno GNOTEST_LINT=1 bash eth/scripts/gnotest.sh
# oldcode: the working tree's tests over commit REV's realm code (on the Mac, then the devbox)
git archive REV eth/realm | tar -x -C $D; find $D/eth/realm -name '*_test.gno' -delete
for f in eth/realm/*/*_test.gno; do cp $f $D/eth/realm/$(basename $(dirname $f))/; done   # → devbox $OLD
ETH_REALM_DIR=$OLD eth/scripts/gnotest.sh <realm>
# the negative controls and the stage controls
python3 f1mutants.py /root/src/eth-f1 <out dir> [name …]
# the vectors: M1's generator (appendix A) with appendix C's delta, in a copy of bridge/ (go.mod, go.sum, hl/), then
# gofmt -w on each file written (the committed vectors are gofmt'd; M1's generator reproduces them byte for byte so)
HL_GEN_ETH=<tree root> go test -count=1 -run TestGenEthVectors ./hl/
```

## Appendix C — the vectors' generator, F1's delta to appendix A

```diff
--- a/hl/gen_eth_test.go (appendix A)
+++ b/hl/gen_eth_test.go (F1)
@@ -122,7 +122,7 @@
 	}
 	const one = 1_000_000
 	stranger := pad20(0x66) // a router no route enrolls
-	// BUBBLE: the fee is 100 BUBBLE, so amounts under it are swept
+	// BUBBLE: amounts under the fee are swept
 	route := ethAddr("hlbubble test route")
 	self := mustB32(GnoPackageAddress(ethNamespace + "hlbubble"))
 	alice, bob := mustB32(testAddress("hlb-alice")), mustB32(testAddress("hlb-bob"))
@@ -131,13 +131,13 @@
 		dirtyBob[i] = 0xff
 	}
 	bubble := []ethVector{
-		{"backAlice300", Message{3, 0, ethTestDomain, route, DomainGnoTest, self, TokenMessage(alice, 300*one, nil)}, []int{0, 1}},
+		{"backAlice600", Message{3, 0, ethTestDomain, route, DomainGnoTest, self, TokenMessage(alice, 600*one, nil)}, []int{0, 1}}, // F1: a send of 600 must clear the minimum, 400 at plan e3 §5.5
 		{"strangerRoute", Message{3, 1, ethTestDomain, stranger, DomainGnoTest, self, TokenMessage(alice, 150*one, nil)}, []int{0, 1}},
 		{"dirtyBob250", Message{3, 2, ethTestDomain, route, DomainGnoTest, self, TokenMessage(dirtyBob, 250*one, nil)}, []int{1, 2}},
 		{"backAlice500", Message{3, 3, ethTestDomain, route, DomainGnoTest, self, TokenMessage(alice, 500*one, []byte("memo"))}, []int{0, 2}},
 		{"backCarol50", Message{3, 4, ethTestDomain, route, DomainGnoTest, self, TokenMessage(mustB32(testAddress("hlb-carol")), 50*one, nil)}, []int{0, 1}},
 	}
-	// GNOT: the fee is 5 GNOT; the route realm's own address is the recipient the messages name
+	// GNOT: the route realm's own address is the recipient the messages name
 	groute, groute2 := ethAddr("hlgnot test route"), ethAddr("hlgnot test route 2")
 	gself := mustB32(GnoPackageAddress(ethNamespace + "hlgnot"))
 	fees := mustB32(GnoPackageAddress(ethNamespace + "hlfees"))
@@ -150,14 +150,22 @@
 		{"strangerRoute", Message{3, 4, ethTestDomain, stranger, DomainGnoTest, gself, TokenMessage(galice, 20*one, nil)}, []int{0, 1}},
 		{"inCarol1000", Message{3, 5, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(gcarol, 1_000*one, nil)}, []int{0, 1}},
 		{"inDave1000", Message{3, 6, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(gdave, 1_000*one, nil)}, []int{1, 2}},
-		{"newRouteAlice7", Message{3, 7, ethTestDomain, groute2, DomainGnoTest, gself, TokenMessage(galice, 7*one, nil)}, []int{0, 1}},
-		{"oldRouteAlice8", Message{3, 8, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(galice, 8*one, nil)}, []int{0, 2}},
-		{"oldRouteAlice9", Message{3, 9, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(galice, 9*one, nil)}, []int{1, 2}},
+		{"newRouteAlice70", Message{3, 7, ethTestDomain, groute2, DomainGnoTest, gself, TokenMessage(galice, 70*one, nil)}, []int{0, 1}}, // F1: claims above the raised fee
+		{"oldRouteAlice80", Message{3, 8, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(galice, 80*one, nil)}, []int{0, 2}},
+		{"oldRouteAlice90", Message{3, 9, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(galice, 90*one, nil)}, []int{1, 2}},
 		{"inBob60", Message{3, 10, ethTestDomain, groute, DomainGnoTest, gself, TokenMessage(gbob, 60*one, nil)}, []int{0, 1}},
 		// the router addressing another realm of the bridge (AQ1): a successor takes its predecessor's messages once
 		// the last page of a migration is in; a route that succeeded none refuses it before the mailbox is asked
 		{"inOtherRealm60", Message{3, 11, ethTestDomain, groute, DomainGnoTest, mustB32(GnoPackageAddress(ethNamespace + "hlbubble")), TokenMessage(gbob, 60*one, nil)}, []int{0, 1}},
 	}
+	// F1 (E-M1's Q6): burns of 150 GNOT whose recipient is one of the Solana deployment's six realms — the same
+	// operator namespace without eth/ — which the route sweeps as it sweeps one naming a realm of this deployment;
+	// sent by the router the route holds when they are taken (testRoute2, after TestSettings' swap)
+	solNamespace := "gno.land/r/" + gnoAddr + "/"
+	signers := [][]int{{0, 1}, {1, 2}, {0, 2}}
+	for i, r := range []struct{ name, path string }{{"Mailbox", "hlmailbox"}, {"Verifier", "hlverifier"}, {"Gnot", "hlgnot"}, {"Bubble", "hlbubble"}, {"Fees", "hlfees"}, {"Gov", "hlgov"}} {
+		gnot = append(gnot, ethVector{"inSol" + r.name + "150", Message{3, uint32(12 + i), ethTestDomain, groute2, DomainGnoTest, gself, TokenMessage(mustB32(GnoPackageAddress(solNamespace+r.path)), 150*one, nil)}, signers[i%3]})
+	}
 	write := func(dir string, vs []ethVector, extra string) {
 		p := filepath.Join(root, "eth", "realm", dir, "vectors_test.gno")
 		if err := os.WriteFile(p, []byte(ethGnoFile(dir, vs, extra)), 0o644); err != nil {
```

## Appendix D — F1's negative controls (`f1mutants.py`)

```python
#!/usr/bin/env python3
"""Negative controls for eth F1 (eth/bridge/M1-NOTES.md, "Fold F1"): each mutant takes one F1 rule back out of a
scratch copy of eth/realm — or, for a "stage" control, rewrites one launch router line as a stage step's slip
would — runs the named realms' suites there (eth/scripts/gnotest.sh with ETH_REALM_DIR) and prints what failed.
A mutant must fail; a stage control must stop the package at init. Usage: f1mutants.py <tree root> <out dir> [name ...]"""
import os, re, shutil, subprocess, sys

ROOT, OUT = sys.argv[1], sys.argv[2]
ONLY = set(sys.argv[3:])
T0, T1 = "0x0BA554Aa0D0566e582dE0238a089Ec102c2e05a4", "0x051D52Fd082d902f8E0CF300a60304D484cf2d07"

MUTANTS = [
    # name, realms to test, file, old, new
    # change 1: the two contracts the build added are refused (hlmailbox's list)
    # (each address replaced by a fresh one, not by another line's: the list stays distinct, so 2b does not stop it)
    ("c1-verifier-unlisted", "hlmailbox,hlgnot,hlbubble,hlgnot2", "hlmailbox/ethdeploy.gno",
     'launchEthEd25519Verifier = "0x2525252525252525252525252525252525252525"', 'launchEthEd25519Verifier = "0x2626262626262626262626262626262626262626"'),
    ("c1-exec-unlisted", "hlmailbox,hlgnot,hlbubble,hlgnot2", "hlmailbox/ethdeploy.gno",
     'launchEthHlAdminExec     = "0x1212121212121212121212121212121212121212"', 'launchEthHlAdminExec     = "0x1313131313131313131313131313131313131313"'),
    # change 2: init holds the launch router to the mailbox's token (the rule; its wiring is the stage controls)
    ("c2-gnot-rule-off", "hlgnot", "hlgnot/hlgnot.gno", "\tif k == token {\n", "\tif true || k == token {\n"),
    ("c2-bubble-rule-off", "hlbubble", "hlbubble/hlbubble.gno", "\tif k == token {\n", "\tif true || k == token {\n"),
    ("c2-gnot2-rule-off", "hlgnot2", "hlgnot2/hlgnot2.gno", "\tif hex.EncodeToString(k[:]) == hlgnot.Router() {\n", "\tif true {\n"),
    ("c2-gnot2-held-to-list", "hlgnot2", "hlgnot2/hlgnot2.gno", "\tif hex.EncodeToString(k[:]) == hlgnot.Router() {\n",
     "\tif _, token := hlmailbox.EthContractAt(hlmailbox.EthToken0); k == token {\n"),
    ("c2b-duplicates-allowed", "hlmailbox", "hlmailbox/ethdeploy.gno", "\t\t\tif cs[i] == cs[j] {\n", "\t\t\tif false && cs[i] == cs[j] {\n"),
    # change 3: kind 6 refuses the zero router
    ("c3-zero-router-allowed", "hlgov", "hlgov/hlgov.gno", "\tif k == ([32]byte{}) {\n\t\t// HlAdmin executes", "\tif false && k == ([32]byte{}) {\n\t\t// HlAdmin executes"),
    # change 4: a credit naming one of the Solana deployment's six realms is swept, a vote refuses one, a send is unaffected
    ("c4-list-empty", "hlmailbox,hlgnot,hlbubble,hlgnot2", "hlmailbox/soldeploy.gno",
     "\tfor _, r := range solanaRealms {\n\t\tif r == a {", "\tfor _, r := range solanaRealms {\n\t\tif false && r == a {"),
] + [m for r in ("hlgnot", "hlbubble", "hlgnot2") for m in (
    ("c4-%s-credit-off" % r, r, "%s/%s.gno" % (r, r),
     "err != nil || isRealm(to) || hlmailbox.IsSolanaRealm(to))", "err != nil || isRealm(to))"),
    ("c4-%s-vote-credit-off" % r, r, "%s/%s.gno" % (r, r),
     "\tif !to.IsValid() || isRealm(to) || hlmailbox.IsSolanaRealm(to) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftCredit(" % r,
     "\tif !to.IsValid() || isRealm(to) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftCredit(" % r),
    ("c4-%s-vote-sweep-off" % r, r, "%s/%s.gno" % (r, r),
     "\tif !to.IsValid() || isRealm(to) || hlmailbox.IsSolanaRealm(to) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftSweep(" % r,
     "\tif !to.IsValid() || isRealm(to) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftSweep(" % r),
)] + [
    ("c4-gnot-send-refuses-solana", "hlgnot", "hlgnot/hlgnot.gno",
     "func isRealm(a address) bool {\n\tfor _, r := range realms {", "func isRealm(a address) bool {\n\tif hlmailbox.IsSolanaRealm(a) {\n\t\treturn true\n\t}\n\tfor _, r := range realms {"),
    # change 5: the tests read the compiled figures — a third set of figures fails TestLaunchValues's pin alone
    ("c5-gnot-figures-25-60", "hlgnot", "hlgnot/hlgnot.gno",
     "\tlaunchFee      = 10 * gnot\n\tlaunchMinimum  = 20 * gnot\n", "\tlaunchFee      = 25 * gnot\n\tlaunchMinimum  = 60 * gnot\n"),
    ("c5-bubble-figures-150-500", "hlbubble", "hlbubble/hlbubble.gno",
     "\tlaunchFee      = 200 * one\n\tlaunchMinimum  = 400 * one\n", "\tlaunchFee      = 150 * one\n\tlaunchMinimum  = 500 * one\n"),
    ("c5-gnot2-figures-25-60", "hlgnot2", "hlgnot2/hlgnot2.gno",
     "\tlaunchFee      = 10 * gnot\n\tlaunchMinimum  = 20 * gnot\n", "\tlaunchFee      = 25 * gnot\n\tlaunchMinimum  = 60 * gnot\n"),
    # the coordinator's item (plan e4 §13): kinds 8 and 9 refuse the empty code's hash and zero
    ("c6-empty-codehash-allowed", "hlgov", "hlgov/hlgov.gno", "\tif h == keccak256.Sum256(nil) {", "\tif false && h == keccak256.Sum256(nil) {"),
    ("c6-zero-codehash-allowed", "hlgov", "hlgov/hlgov.gno", "\tif h == ([32]byte{}) {\n\t\tpanic(\"hlgov: the \" + what + \" is all zeros", "\tif false && h == ([32]byte{}) {\n\t\tpanic(\"hlgov: the \" + what + \" is all zeros"),
]

# stage controls: name, realm to load, file, the line as staged, the slip — the package must stop at init
def router_line(v):
    return '\nconst launchRouter = "%s"\n' % v
STAGES = [
    ("c2-stage-gnot", "hlgnot", "hlgnot/hlgnot.gno", router_line(T0), router_line(T1)),
    ("c2-stage-bubble", "hlbubble", "hlbubble/hlbubble.gno", router_line(T1), router_line(T0)),
    ("c2-stage-gnot2", "hlgnot2", "hlgnot2/hlgnot2.gno", router_line(T0), router_line(T1)),
    # one proxy written as both tokens: hlmailbox itself stops (and with it every route that imports it)
    ("c2b-stage-token-twice", "hlmailbox", "hlmailbox/ethdeploy.gno",
     'launchEthToken1          = "%s"' % T1, 'launchEthToken1          = "%s"' % T0),
]

def stage(name):
    d = os.path.join("/tmp", "eth-f1-mut-" + name)
    shutil.rmtree(d, ignore_errors=True)
    shutil.copytree(os.path.join(ROOT, "eth", "realm"), d)
    return d

def edit(d, path, old, new):
    p = os.path.join(d, path)
    s = open(p, encoding="utf-8").read()
    n = s.count(old)
    if n != 1:
        return "DID NOT APPLY (%d matches)" % n
    open(p, "w", encoding="utf-8").write(s.replace(old, new))
    return ""

def suite(name, d, realm):
    logs = os.path.join(OUT, name)
    os.makedirs(logs, exist_ok=True)
    env = dict(os.environ, ETH_REALM_DIR=d, GNOTEST_LOGS=logs)
    r = subprocess.run([os.path.join(ROOT, "eth", "scripts", "gnotest.sh"), realm], cwd=ROOT, env=env, capture_output=True, text=True)
    head = r.stdout.splitlines()[0] if r.stdout else r.stderr[:300]
    log = open(os.path.join(logs, realm + ".log"), encoding="utf-8", errors="replace").read()
    return head, log

def run_mutant(name, realms, path, old, new):
    d = stage(name)
    bad = edit(d, path, old, new)
    if bad:
        return bad
    lines = []
    for rl in realms.split(","):
        head, log = suite(name, d, rl)
        fails = re.findall(r"^--- FAIL: (\w+)", log, re.M)
        panics = re.findall(r"^=== RUN   (\w+)\n(?:(?!=== RUN).*\n)*?panic: (.{0,120})", log, re.M)
        lines.append("%s | failed: %s | aborted: %s" % (head, ",".join(fails) or "-", ";".join("%s (%s)" % p for p in panics) or "-"))
    shutil.rmtree(d, ignore_errors=True)
    return ("\n" + " " * 28).join(lines)

def run_stage(name, realm, path, frm, to):
    d = stage(name)
    bad = edit(d, path, frm, to)
    if bad:
        return bad
    head, log = suite(name, d, realm)
    first = log.splitlines()[0] if log else ""
    ran = len(re.findall(r"^=== RUN", log, re.M))
    shutil.rmtree(d, ignore_errors=True)
    return "%s | tests run: %d | %s" % (head, ran, first[:220])

for m in MUTANTS:
    if not ONLY or m[0] in ONLY:
        print("%-27s %s" % (m[0], run_mutant(*m)), flush=True)
for s in STAGES:
    if not ONLY or s[0] in ONLY:
        print("%-27s %s" % (s[0], run_stage(*s)), flush=True)
```

## Fold F6 — the round-2 realms audit's fixes, and E2-RT5's fees (2026-10-14)

Lane F6, branch `eth-f6` from `bridge-eth` 00d97c09 (the round-2 audits), worktree `~/src/jaekwon/bubblerumble-eth-f6`,
devbox `/root/src/eth-f6`. It folds `audits/r2/realms.md` (E2-RL1–RL4, RL6, RL7; RL5 left as is, below) and the plan
red team's E2-RT5 fee figures (`audits/r2/plan-redteam.md`), touching `eth/realm/**` and this file only. Each change
has a test that fails on the code before it — the working tree's tests over the parent commit's realm code (`oldcode`
in the commands) — and passes after; the negative controls (appendix F) take each rule back out, and stage controls
prove what no unit test can reach: `init` at a chain that counts as gnoland-1.

| commit (in the order made) | change |
|---|---|
| 154c20c8 | 1. E2-RL1: on gnoland-1 every realm's `init` refuses a launch value still the dev tree's; hlmailbox's json names both namespaces |
| 2178cc29 | 2. E2-RL2: the recipient swap may name a former recipient again; kind 9 still refuses them |
| cdfd9900 | 4. E2-RL4: the cancel's description is its parsed numbers |
| 6618c6ef | 5. E2-RL7 in r/hlgov: kinds 8, 9 and the swap refuse the deployment's contracts; kind 6 two domains (and E2-RL6's reading noted) |
| 7726b96c | 5b. E2-RL7 in the routes: a router swap refuses the deployment's contracts but the route's own token |
| 467e1729 | 3. E2-RL3: hlverifier in every route's list; a credit to the zero address swept |
| a49499fc | 7. E2-RT5: GNOT fee 20, minimum 40; BUBBLE fee 400, minimum 800 |
| efd89fe9 | review: on gnoland-1, `solanaNamespace` only the Solana deployment's own; governor and seats only in canonical form; gnodev's test1 a test address |
| (this) | these notes |

### 1. E2-RL1 — the dev tree's launch values refused on gnoland-1

- **What.** hlmailbox's `init` first calls `mustNotBeDevTree` (`hlmailbox/mainnet.gno`): on chain id `gnoland-1`
  (`devPlaceholder(runtime.ChainID(), compiledLaunchValues())`) it panics, naming the line, while any of these is still
  the dev tree's: a key of `launchSigners` (each of the three test signers, whose seeds `sha256("hyperlane gno test
  validator 0|1|2")` are public — the audit's forged 25,000 GNOT burn), `launchGovernor`, a seat holding a test address
  (`launchSeat2`–`6`'s testutils addresses, the governor's, and — from the review — gnodev's test1, the e2e's governor
  and seat 1, whose mnemonic is public; on any seat line), any of the sixteen `launchEth*` contract placeholders (on any
  line: each address is held against all sixteen), and `solanaNamespace` (the dev namespace, where no Solana realm lives
  on gnoland-1). From the review (efd89fe9) it also refuses, on gnoland-1, a governor or seat that is not a gno address
  in its canonical spelling (valid bech32, lower case — `IsValid` takes upper case too, and no caller's address would
  ever equal it: a governor nobody can be, a seat that never pauses), an empty seat or governor still allowed; and any
  `solanaNamespace` but the Solana deployment's own (`gno.land/r/g1zdaz6sypk5tdsjc2fzsqrrn4ttn8qzmnl4waqp/`, held by its
  sha256): a stage step that rewrote the line with this deployment's own namespace root — as the Solana stage step's
  global rewrite would — leaves the dev value behind and passes the dev check, while `IsSolanaRealm` would match
  nothing. r/hlgov's `init` (`hlgov/mainnet.gno`) refuses its six Ethereum seats (`launchEthSeat1`–`6`) and the HlAdmin
  recipient; each route's (`hlgnot`, `hlbubble`, `hlgnot2`) its `launchRouter` (either token's placeholder) — before
  `wrongRouter`, so the message names the placeholder. Those three key on `hlmailbox.LocalDomain() == DomainMainnet`, so
  one realm decides which chain is mainnet. The guard holds **sha256 digests** of the dev values, never the values (a
  stage step's `sed`, which rewrites a dev value wherever it stands, can never rewrite the guard with it), hashed as
  `init` parses them — a key's or an address's 32 bytes, a gno address's string — so a respelling (case, the 64-digit
  form, `0x`) is no way past it, a gno address's string being canonical besides. Off gnoland-1 nothing is read: the
  e2e's `dev` chain and a Sepolia stage's test chain deploy the dev tree as before. hlmailbox's json gains `"namespace"`
  (the namespace it was staged under), `"solanaNamespace"` and `"solanaRealms"` (name → address of the six), after
  `"launchOrigin"`; the page is unchanged (its rule that the page names no Solana word, `TestEthereumOrigin`, holds).
- **The harness and the chain id.** gno v1.2.0's test harness runs `init` at its own chain id, `dev` (gnovm/pkg/test),
  and no filetest directive sets another; `testing.SetContext` sets the chain id only for what a test calls after it.
  So, as the brief allowed, the rule is a function called with a chain id (hlmailbox's `devPlaceholder`) or a domain
  (the others'); `TestMainnetPlaceholders` also sets the chain id to `gnoland-1` with `testing.SetContext` and calls
  `mustNotBeDevTree` itself — init's own call, reading `runtime.ChainID()` — which stops; and the stage controls below
  prove the wiring at deploy.
- **Tests.** hlmailbox `TestMainnetPlaceholders` (`mainnet_test.gno`): the dev values spelled out in the test, never
  read from the realm; the dev tree refused at gnoland-1 naming signer 0, taken at `dev`, `test5`, `gnoland-2`, `""` and
  `staging`; a staged set (fresh keys, governor, seats 2–6, sixteen contracts, the live Solana namespace; seat 1 kept as
  Jae's cold key) taken, and with no governor (`""`, GovDAO from the first block) taken; each test signer at each
  position (one in upper case) refused; the governor and every seat 1–6 holding test1 or a dev address refused; the
  governor and every seat in a non-canonical spelling (a staged and a dev address in upper case, `g1notanaddress`,
  `jae`) refused, an empty seat taken; each of the sixteen placeholders on each of the sixteen lines (256 cases, every
  other one in lower case) refused with its line's constant name; the dev namespace refused as the dev tree's, and this
  deployment's own, the live one without its slash or one character off, the dev one one character off and `""` refused
  as not the Solana deployment's; a contract one bit off a placeholder taken; `mustNotBeDevTree` under
  `SetContext(gnoland-1)` panics with the signer message and under `dev` does not; the json carries the three new keys
  exactly. hlgov `TestMainnetPlaceholders`: the compiled seats checked against the dev values spelled out, refused at
  gnoland-1 (seat 1 named), taken on five other domains; a staged set taken; each dev seat on each line (36 cases, some
  in the 64-digit form) refused; the dev HlAdmin (lower case) refused, one bit off taken. Each route's
  `TestMainnetPlaceholder`: its compiled router refused at gnoland-1 and taken elsewhere; the other token's placeholder
  and three respellings refused; one bit off taken.
- **Fails before.** The review's part: on a49499fc's code the test fails at its first new case, `governor
  g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5: ""` (test1 taken), 29 of 30 passing; the canonical and live-namespace parts
  are held by their mutants. The rest: the unit tests do not compile on 00d97c09 (`undefined: devPlaceholder`); the
  proof that counts is at the deploy. 00d97c09's tree with only hlmailbox's `mainnetChainID` rewritten to `"dev"` — the
  harness's chain then counts as gnoland-1 — loads every realm (tests run: hlmailbox 15, hlverifier 3, hlfees 2, hlgnot
  4, hlbubble 4, hlgov 5, hlgnot2 4; they fail only on the domains): the unstaged dev tree deploys "on mainnet". On F6's
  tree:

| stage control (F6 tree, `mainnetChainID = "dev"`) | edits besides | result |
|---|---|---|
| s1-mailbox-dev | none | hlmailbox stops at `init`: `launchSigners holds a dev tree test signer, ec4ff84b… (signer 0)…`, no test run |
| s1-mailbox-signers | three fresh keys | stops: `launchGovernor is a test address, g1dpkz6em0…` |
| s1-mailbox-governor | + a fresh governor | stops: `launchSeat2 is a test address, g1dpkz6em4…` |
| s1-mailbox-seats | + fresh seats 2–6 | stops: `launchEthMailbox (the mailbox) is a dev tree placeholder, 0x0404…` |
| s1-mailbox-contracts | + sixteen fresh contracts | stops: `solanaNamespace is the dev tree's` |
| s1-mailbox-staged | + the live Solana namespace | loads (15 tests run) |
| s1-mailbox-own-namespace | everything staged, `solanaNamespace` written as this deployment's own root | stops: `solanaNamespace is gno.land/r/g1leu8…/eth/, not the Solana deployment's on gnoland-1` |
| s1-gnot / s1-bubble / s1-gnot2 | hlmailbox staged, the route's router left | the route stops: `<route>: launchRouter is a dev tree placeholder, 0x…` |
| s1-gnot-staged / -bubble- / -gnot2- | + the router staged | loads |
| s1-gov | hlmailbox and the routes staged, hlgov's seats left | hlgov stops: `launchEthSeat1 is a dev tree placeholder` |
| s1-gov-staged | + hlgov's seats staged | loads |
| x1-init-call-dropped (a mutant) | `init`'s call removed | the dev tree loads again (15 tests run): only a stage control sees this — the unit suite passes (m1-init-call-dropped) |

### 2. E2-RL2 — a premature swap is undone by a second one

- **What.** `NewSetDecisionRecipientRequest` (made and in its closure) and `Execute` no longer refuse a former recipient;
  they refuse only the current one (and, since 6618c6ef, the deployment's contracts but HlAdmin). `Execute` records the
  swap with `retire`: the replaced recipient joins `former` and the restored one leaves it, each once — `Former()` is
  every recipient replaced and not since restored, so the current recipient is never listed. Kind 9 keeps refusing
  every former recipient (`mustNotBeFormer`, now kind 9's alone, its message saying so) and HlAdmin itself; between
  them every contract that was ever the recipient stays refused as a successor. The page's line reads "former
  recipients (never a successor by kind 9; a swap may name one again)".
- **Test.** `TestFormerRecipients` (replacing `TestFormerRecipientRefused`): the swap takes each former recipient (its
  description naming the one it replaces) while kind 9 refuses each; the audit's case end to end — a kind 9 to keyF and
  the swap to keyF; the swap lands; a swap back to keyE is taken, the next decision (an unpause of HlAdmin) goes to
  keyE, keyF is refused by kind 9 as former, keyE as itself; the swap to keyF again; `former` checked after every swap
  (json and `Former()`); a pre-armed kind 9 and swap to keyG voted while keyG was nobody, keyG then served and was
  replaced: the kind 9's closure refuses it, nothing dispatched, the swap's closure takes it; a second swap to keyG
  queued beside it is refused while keyG is the recipient (pending) and applies once keyH is restored.
- **Fails before** (154c20c8): `a swap to the former recipient 0x0101…01 refused: hlgov: never a former decision
  recipient (0x0101…01): it is retired, …` — 13 of 14 pass. Passes after.

### 3. E2-RL3 — a credit to hlverifier or to the zero address is swept

- **What.** The `realms` list of hlgnot, hlbubble and hlgnot2 gains `chain.PackageAddress(namespace + "hlverifier")`
  (closing F-3 above: a send to its twenty bytes is refused too), and a new `unpayable(to)` — `to == zeroAddress ||
  isRealm(to) || hlmailbox.IsSolanaRealm(to)` — is what `Process` sweeps on (with `err != nil`, a dirty top) and what
  the credit and sweep votes refuse. `zeroAddress` is `hl.Bytes32ToAddress` of 32 zero bytes, computed at deploy; since
  `Bytes32ToAddress` errs on a non-zero top, `to == zeroAddress` holds exactly when the recipient's 32 bytes are zero.
  A send already refused the zero address (`RefusedEthRecipient`).
- **Tests.** hlgnot `TestUnpayableSwept`: two vectors signed by the test signers (appendix E: `inVerifier150`,
  `inZero150`, 150 GNOT from the router the route holds then), each checked to be a claim's amount (at or above the
  fee, within the lock) and from the route's router, each swept (no claim, delivered), both votes refusing each as the
  recipient and taking the burn's owner, owed and the sweep queue up by 300 and the lock unmoved; a send to hlverifier's
  twenty bytes refused as a realm of the bridge. hlbubble `TestUnpayableSwept` (the audit's proof of concept, inverted;
  crafted under the any-verifier): a send first, so the lock holds the burns at any fee; the same burn to carol a claim
  (the control); hlverifier's and the zero burn swept, the votes refusing both, the send refused, the counters' identity.
  hlgnot2's drill (`TestMigration`): the two burns after the Solana six, swept, refused by the votes, the identity
  holding; the send refused.
- **Fails before** (7726b96c): hlgnot `a burn to this deployment's hlverifier was not swept: swept 0, claim &{… g1lqaj…
  150000000 …}`, hlbubble and hlgnot2 likewise (the claim to `g1lqaj965l79w8vkyl60uz89ljsa0t8xf8snntyv`, this
  deployment's hlverifier) — the zero half is held by the controls m3-*-zero-off and m3-*-votes-old. Passes after.

### 4. E2-RL4 — the cancel's description is its parsed numbers

- **What.** `NewCancelRequest` parses once (`parseCancel` → `cancelSet`: a range, or the list in the order written)
  and writes `"cancel decisions " + c.String()`: `"from-to"` or the numbers joined by `", "`, never the text as typed.
  The body (`c.params()`), the dispatch check (`c.top()`) and the closure's walk over the cancelled decisions
  (`eachCancelled(c, …)`, which re-parsed the raw text before) read the same parsed set. A list typed `"2, 3"` reads as
  before; `"2,3"` now reads `"2, 3"`; the e2e's `"2"` reads `"cancel decisions 2"` as before.
- **Test.** `TestCancelDescription`: the audit's input (`"2"`, eighty newlines, `"-  N"`) gives `cancel decisions 2-N`
  on one line — in the description, the ledger, the page's Sent line and `decision/<n>`'s text — and the body is the
  range 2–N; `" 3 ,\t5,\n\n7 ,"` gives `cancel decisions 3, 5, 7` and the list's body; `" 2 "` gives `cancel decisions 2`.
- **Fails before** (2178cc29): `the range's description "cancel decisions 2\n…\n-  42", want "cancel decisions 2-42"`
  — 14 of 15 pass. Passes after.

### 5. E2-RL7 — no builder names a target that can never serve

- **r/hlgov** (6618c6ef): kind 9 refuses each of hlmailbox's sixteen contracts as the successor (after its HlAdmin and
  former checks, so those keep their messages; none answers `predecessor()` with HlAdmin — F-7 above); kind 8 refuses
  each as the implementation (after the zero/proxy check; a launch implementation too — a rollback deploys the old
  code afresh, as the brief decides); the recipient swap refuses each but HlAdmin (`EthHlAdmin`: the launch HlAdmin,
  which a swap back restores under E2-RL2 — "HlAdmin's own successor path"; a successor is never one of the sixteen);
  kind 6 refuses Ethereum's own domain (`LaunchOrigin`) and gno.land's other domain — 1198419796 on gnoland-1,
  1198419788 on a test chain — through `refusedDomain(domain, local, origin)`, after the zero-router and zero-domain
  checks (their messages unchanged). All are checked when made and again in the closure, as before.
- **The routes** (7726b96c, beyond the brief's list but in the audit's fix text: "the routes' router swap (but its
  own token)"): `NewSetRouterRequest` refuses each of the sixteen but the route's own token (`refusedRouter`: token 0
  for hlgnot and hlgnot2, token 1 for hlbubble), before p/hlroute's `DraftRouter`; a swap back to the own token is
  taken. In its own commit, so the coordinator can drop it.
- **Tests.** hlgov `TestDeploymentTargets`: for each of the sixteen, kind 9 refused (the launch HlAdmin as former),
  kind 8 refused (lower case), the swap refused — HlAdmin taken; an address of no contract taken by kinds 8 and 9;
  kind 6 for domain 31337 and 1198419788 refused for both tokens, domain 10 taken; nothing recorded or dispatched.
  `TestRefusedDomain` (`domains_test.gno`): the rule at gnoland-1 (local 1198419788, Ethereum 1: domains 1 and
  1198419796 refused, 1198419788, 10 and 31337 taken) and at a Sepolia stage (local 1198419796, Ethereum 11155111).
  Each route's `TestRouterSwapTargets`: the sixteen refused (lower case) but its own token, taken or refused as the
  router already; nothing recorded.
- **Fails before.** 6618c6ef's parent cdfd9900 (`domains_test.gno` left out: it calls `refusedDomain`, which the old
  code has not got): `TestDeploymentTargets: expected an abort containing "hlgov: the successor 0x0404…04 is the mailbox
  of this deployment, which can never succeed HlAdmin …", got none`. 7726b96c's parent 6618c6ef: each route's
  `TestRouterSwapTargets: expected an abort containing "<route>: the router 0x0404…04 is the mailbox of this
  deployment, not the <token> token: …", got none` — every other test passing. Each refusal's own control is a mutant.

### 6. E2-RL6 (decided) — a foreign domain's router stays 20 bytes

Kind 6 keeps refusing a router whose top twelve bytes are not zero, for every domain (Q2's reading; `TestEthereumKinds`
holds it). Plan e5 §13's "kind 6's router is a remote domain's 32-byte address, taken whole" is to say so (the
coordinator folds the plan): a token of this deployment enrols only 20-byte routers; a Solana-style 32-byte router is
a successor r/hlgov's, behind a kind 9. Kind 6's comment says it.

### 7. The fees (E2-RT5)

- **What** (a49499fc): GNOT fee 20 and minimum 40 (hlgnot, and hlgnot2 as hlgnot), BUBBLE fee 400 and minimum 800 —
  GNOT's at the Solana routes' ratio, 100 BUBBLE to 5 GNOT; the floors, cap floors and maximums kept. The comments name
  E2-RT5 (eth §5.5's 10/20 paid a send's Ethereum gas only below ≈0.3 gwei) and §11.5 as Jae's.
- **p/hlroute's rules hold** (each route's `TestLaunchValues` checks them on the compiled figures): fee floor ≤ fee
  (2 ≤ 20; 40 ≤ 400), fee ≤ a quarter of the cap floor (≤ 2,500 both: the first installment fits a recipient's
  meter), 0 < minimum ≤ maximum (40, 800 ≤ 25,000), the minimum above the fee (twice it). What the figures move
  besides: the per-message maximum's floor (`DraftMaximum`, never below the launch minimum) rises to 40 GNOT and 800
  BUBBLE, the cap floor's own floor (a quarter at least the fee) to 80 and 1,600 — under the launch floor of 10,000 —
  and a burn under 20 GNOT or 400 BUBBLE is swept, waiting for a vote crediting it by id.
- **Tests.** The pins (each route's `TestLaunchValues`, its page line). hlgnot's and hlgnot2's suites pass unchanged
  otherwise (F1's tests read the compiled figures). hlbubble's broke by name, as F1 designed: its burn-back is
  re-signed at 1,200 BUBBLE (a send of it must clear the minimum, 800, and the lock return to 0; appendix E) and the
  dirty-recipient burn at 500 (at or above the fee, so the recipient sweeps it, never its size); alice is funded 2,000,
  bob sends 1,150 (so the lock's later figures — 1,100 after carol's sweep, 600 after bob's — stand as before), and the
  Solana-realm burns carry 500 (at or above the fee, within the lock).
- **Fails before** (467e1729): each route's `TestLaunchValues` alone (`launch values: fee 10000000 min 20000000 …`,
  `… fee 200000000 min 400000000 …`), every other test passing (14 of 15, 11 of 12, 5 of 6). Passes after.

### E2-RL5 — left as is

Kind 12's floor (R15) reads the launch routes' `Maximum()` for the deployment's life, so after a migration it guards
a number the successor has moved past. Left as the brief decides: the live Solana r/hlgov has the same reading
(`realm/hlgov/hlgov.gno` 765–770), a migration is rare and GovDAO's own act — whose member, per the plan, lowers
Ethereum's limit and the successor's maximum in the order kind 12's description gives — and the fix needs either a
route-to-mailbox report or a refusal of kind 12 after a migration, both new surface for a case that waits for one.

### Not in this fold

- **E2-RL8** (the two deployments' proposals carry the same titles): not in the brief; titles unchanged.
- **E2-RL9** (the tests' blind spots): (2) and (3) are narrower now — `devPlaceholder` and `refusedDomain` are read at
  gnoland-1's values, and the stage controls run `init` with the harness's chain counted as gnoland-1, while the
  vectors still use the placeholders (they test the rules, the guard tests the stage); (1) is the e2e's (every kind
  delivered and executed on Ethereum), (4) the harness's.

### Readings where the brief left room

1. **Seat 1 is not refused**: it is Jae's cold key (`g1ecsu…`), a real key the launch may keep; every seat (1–6) is
   still refused when it holds one of the test addresses (test1 among them since the review).
2. **"Every realm's init"**: each realm refuses the placeholders it compiles itself; what it reads from hlmailbox
   (r/hlgov's proxies, the gate; the routes' domain) hlmailbox's own guard has held, since a realm cannot deploy before
   its imports. r/hlgov's recipient is checked again in r/hlgov (the brief names it), and the routers in the routes
   though `wrongRouter` already ties them to hlmailbox's tokens. The governor is refused in hlmailbox; every other
   realm refuses a governor other than hlmailbox's.
3. **hlfees' `launchTreasurer`** (the deployer's hotkey, `g1leu8d2…`) is not refused: the brief does not name it and
   it is a real key, the operator's (§5.5); say if it should be.
4. **Each value against every dev value of its kind** (a dev contract on another contract's line, a dev seat on seat
   1), and the signers key by key (one test key among two real ones is refused), where the audit spoke of a digest of
   the whole `launchSigners` string.
5. **The guard's messages** name the constant (`launchEthMailbox`, `launchSeat3`, …) and the value; hlmailbox's says
   "refused on " + `mainnetChainID` (so the stage controls read "refused on dev").
6. **The namespaces in the json only**, not on the page: the page's rule (no Solana word, `TestEthereumOrigin`)
   stands; hlwatch reads the json.
7. **`former` holds no current recipient** (`retire`), so the list reads "replaced and not restored"; kind 9 refuses
   former ∪ {recipient}, every contract that was ever the recipient, as before.
8. **The zero address in the votes too**: "as one to the other realms does" — those are refused as a vote's recipient.
9. **The routes' router swap** (E2-RL7's fourth target in the audit's fix text) folded, in its own commit.
10. **Kind 8 refuses the launch implementations** too, per the brief: a rollback to launch code redeploys it.

### Findings and questions (none changes a rule here)

- **F6-1, the e2e at this base stops before the realms.** `eth/bridge/local/e2e.sh` on this tree (`BL=/root/src/
  eth-f6-e2e-state ANVIL_PORT=8846 GNO_PORT=26858`) fails at `hleth setup`, 54 s in, before any realm goes up:
  `GnoLightClient.initialize: no value for its parameter "headerLeaves" (bytes[]): the contract's ABI changed — this
  tool must learn it before deploying` — F4's E2-CT3 (84000264, the client's `initialize` binds its trust root) changed
  the ABI that F3's `hleth setup` encodes; nothing F6 touched. Read against F6's tree, the run would hold: it reads
  each route's fee and minimum from its json (F3), so it sends 40 GNOT and 2,000 BUBBLE and burns back 800, expecting
  400 paid after the fee, and checks `BMIN > FEE` (800 > 400); its seds (`launchSigners     = …`, `launchSeat1/5`,
  `launchEth<Name> *= "0x…"`, `^const launchRouter`, `launchEthSeat<i> = `, `launchCapFloor = 10_000 \* one`) match
  lines F6 left byte for byte — no const block realigned, no new line in `ethdeploy.gno` matching its `launchEth`
  pattern; its one r/hlgov cancel, `"2"`, reads as before; its chain (`dev`) leaves every guard off. To run once hleth
  has learned the new `initialize`.
- **F6-2, the stage step**: a mainnet stage now fails at `addpkg` on any dev value it misses, naming it, and on any
  `solanaNamespace` but `"gno.land/r/g1zdaz6sypk5tdsjc2fzsqrrn4ttn8qzmnl4waqp/"` (F-2 above, now held by the realm);
  hlwatch can hold `"namespace"`, `"solanaNamespace"` and `"solanaRealms"` against the record.
- **F6-3**: the guard refuses the dev values exactly; a malformed stage value (a namespace without its trailing slash, a
  key of the wrong deployment) is the stage step's and hlwatch's to catch, as before.

### The independent review (a read-only agent over `git diff 00d97c09..a49499fc -- eth/realm`)

It recomputed every digest (each matches its placeholder), ran the interrealm checker (134, clean), found nothing
above low and eight points; each, and what became of it:

1. **low, folded** (efd89fe9): `solanaNamespace` was held only against the dev value, though its right value on
   gnoland-1 is known — a stage step rewriting the line with this deployment's own namespace root passed. Now the live
   namespace or nothing (§1).
2. **low, folded** (efd89fe9): gno addresses were hashed as text, so an upper-case dev address (valid to `IsValid`)
   passed, and the e2e's values were not listed. Now a governor or seat must be canonical, and gnodev's test1 is a test
   address. The e2e's Ethereum seats (seeded keys) are hlgov's mirror only — HlAdmin's real seats are `hleth setup`'s,
   checked by `hleth audit` — and are not listed; hlfees' treasurer stays (reading 3).
3. **low, a question (Q-F6a)**: after an undo, kind 9 refuses the early successor for good — B, swapped to too early
   and swapped away from, joins `former`, so a re-timed kind 9 to B (the first cancelled) needs a fresh successor. Its
   fix: `retire` adds the replaced recipient only on a forward swap (a restore drops it). Not folded: the brief keeps
   kind 9 refusing every former recipient, and the fix has its own edge (a retired HlAdmin swapped away from by a
   mistaken restore would leave the list, letting a kind 9 to it be voted and spent). Jae's or the coordinator's call.
4. **low, a question (Q-F6b)**: with R29 gone from the swap, a stale or pre-armed swap can point decisions at a retired
   HlAdmin (a queued undo nobody cancelled after the kind 9 executed), and a pre-armed swap's "(now X)" applies against
   another recipient. Its fix: `recipientParams` keeps the recipient it was decided against and the swap applies only
   while it stands — the pattern of hlmailbox's rotations (oldKeys, R21) — which keeps E2-RL2's undo (made and applied
   against the same B) but not the stale-undo case; hlwatch alarming on a queued swap whose target has a successor
   covers that one. Not folded (beyond the brief; it changes `TestFormerRecipients`' pre-armed case).
5. **info, a question (Q-F6c)**: kind 8 refuses each proxy's own launch implementation, so an incident rollback
   redeploys the audited code — the brief's decision; allowing `impl` = the proxy's own launch implementation is one
   condition.
6. **info, a follow-up**: kind 5's keys and kind 7's beneficiary may still name one of the sixteen (a seat no key holds;
   `collectProtocolFees` reverting until another kind 7) — E2-RL7's class, one condition each; not in the brief.
7. **info, a follow-up**: the credit and sweep votes compare the typed address as a string, so an upper-case realm or
   zero address passes `unpayable` (GNOT would land in that account, BUBBLE under a key nobody spends) — older than F6
   for the realm checks; refusing a non-canonical `to` in the votes is one condition a route.
8. **info, for the coordinator**: `e2e.sh`'s comment "a 100 BUBBLE fee" (248–249), `BUILD.md`'s fees and open items
   (76–84: the hlverifier sweep and kind 9's refusal are done), plan e5 §5.5's 10/20 and 200/400 (274, 614–615).

Its verdict: "the fold matches the brief point by point … Fold 1 and 2 before any gnoland-1 upload" (done). Its one
gap — "the hlgov and route guards' `init` wiring is never run at gnoland-1" — is the stage controls' (§1), which it
did not have.

### The gno-specs checks (AGENTS.md) — sections checked, for what F6 added or changed

Read first: `docs/gno-specs/gno-interrealm-v2.md`, `interrealm_v2.adr.md`, `gno-security-guide.md`,
`gno-ai-contract-review.md`; each rule below against F6's diff (`git diff 00d97c09 -- eth/realm`).

- **Interrealm v2 §2–§4 (contexts, borrow rules).** Every new function is a non-crossing function of its own realm:
  `mustNotBeDevTree`, `compiledLaunchValues`, `sha256In`, `canonical` (hlmailbox), `devPlaceholder` (each realm),
  `isDev`, `retire`, `parseCancel` and `cancelSet`'s methods, `refusedDomain` (r/hlgov), `unpayable`, `zeroAddr`,
  `refusedRouter` (the routes). What they call in hlmailbox (`LocalDomain`, `LaunchOrigin`, `EthContractName`, `EthContractAt`,
  `EthAddress`, `IsSolanaRealm`) are non-crossing reads that borrow rule #1 runs under hlmailbox and that write
  nothing, returning values (a `uint32`, a `string`, a `[32]byte` copy, a `bool`).
- **§3.2 (storage = authority).** New state is each realm's own, allocated in its own frame: hlmailbox's digest arrays
  (strings, at deploy), the routes' `zeroAddress` (a string), r/hlgov's `former` (a slice `retire` rebuilds inside
  r/hlgov's `Execute`, a crossing function of r/hlgov). The cancel's closure captures a `cancelSet` (r/hlgov's own
  type, built in its builder, as the other closures capture r/hlgov's `params`), never returned. **§3.3**: no `/p/`
  package touched.
- **§5, §5.2 (crossing functions, `cur`).** No crossing function and no secondary `realm` parameter added:
  `check-interrealm.py` over `eth/realm` counts 134 functions, as before, all clean.
- **§5.3 / guide §5.7 (realm values ephemeral).** No realm value stored or captured anew.
- **§8 / §3.2 (conversion guards, construction).** `launchValues` and `cancelSet` are built in their declaring realms;
  no conversion to another realm's type.
- **§9 (panics across boundaries).** Every refusal panics in the realm's own frame before any write: `init`'s abort the
  addpkg (§12.3), a builder's abort the proposal (and, in its closure, leave it for a retry or GovDAO's cancel, as
  before), `Process`'s sweep is a boolean into p/hlroute's `Credit` in the same call. The tests catch a crossing
  call's panic with `revive` and an in-realm one (`mustNotBeDevTree` called directly) with `recover`, since only a
  panic that crosses a boundary reaches `revive`.
- **§10 (method values).** None returned; `cancelSet`'s methods are on an unexported type no function returns.
- **§11.3 public API / guide §5.1, §8 / review checks 3, 8.** No new exported function or type; the json gains three
  keys of compiled strings; nothing returns a pointer into state; no interface or function parameter added (review
  checks 4, 5).
- **§12.3 (MsgAddPackage).** The guards read compiled values and the chain id (hlmailbox `runtime.ChainID()`, the
  others hlmailbox's `LocalDomain`), never `PreviousRealm`; each runs before any other write of its `init` (hlgov's
  after its seats are parsed, the routes' before `wrongRouter`, hlgnot2's before `AnnounceSuccessor`).
- **Guide §3 (A)(B)(C).** No `/p/`-typed state added, nothing embedded, no caller-supplied callback.
- **Guide §5.5 / review check 2 (payment guards).** `TransferRemote` unchanged: `IsUserCall` and `AssertOriginCall` (or
  the allowance) still after the recipient checks, which now also refuse hlverifier's twenty bytes; no coin moves on a
  refusal.
- **Guide §5.6, §5.8 / review checks 1, 9.** No identity read from an address parameter; no `unsafe.PreviousRealm`.
- **Review check 10 (Render, descriptions).** E2-RL4 is this class: a description writes parsed values only, as every
  other builder's does; the json's new keys are compiled strings. **Check 11 (GetCoins).** Not used.
- **Interrealm ADR.** `cross(cur)` unchanged; no `unsafe.CurrentRealm`; `testing.SetRealm`, `testing.SetContext` in
  tests only.

### Tests and results (devbox, gno v1.2.0)

Last run: the realms at efd89fe9, `GNO=/root/.cache/gno-toolchains/v1.2.0/gno GNOTEST_LINT=1 bash
eth/scripts/gnotest.sh` on the devbox, exit 0.

| realm | passed | failed | gno lint | new or changed (F6) |
|---|---|---|---|---|
| r/eth/hlmailbox | 30 | 0 | ok | TestMainnetPlaceholders |
| r/eth/hlverifier | 3 | 0 | ok | |
| r/eth/hlfees | 2 | 0 | ok | |
| r/eth/hlgnot | 15 | 0 | ok | TestMainnetPlaceholder, TestUnpayableSwept, TestRouterSwapTargets; TestLaunchValues' pins |
| r/eth/hlbubble | 12 | 0 | ok | the same three; TestLaunchValues' pins; TestTransferRemote, TestProcessAndClaim, TestBuilders, TestSolanaRealmsSwept's amounts |
| r/eth/hlgov | 17 | 0 | ok | TestMainnetPlaceholders, TestRefusedDomain, TestCancelDescription, TestDeploymentTargets; TestFormerRecipients (was TestFormerRecipientRefused) |
| r/eth/hlgnot2 | 6 | 0 | ok | TestMainnetPlaceholder, TestRouterSwapTargets; TestMigration's burns; TestLaunchValues' pins |
| **all** | **85** | **0** | | 72 before F6 |

The other gates on the same tree: `check-interrealm.py` over `eth/realm` (M1's shadow invocation) — "134 function(s)
take a realm parameter; every secondary one is either unused or proved with IsCurrent() first", exit 0; `gofmt -l`
nothing; `gno fmt -diff -imports=false eth/realm/...` no diff. The vectors: appendix A with appendices C and E
(sha256 `28b10223…`) regenerates all four vector files byte for byte (after `gofmt -w`); appendix A with C alone
(F1's, `72c5a8a2…`) regenerated 00d97c09's byte for byte before F6 changed them. `realm/` (the Solana tree) untouched.

**Negative controls** (appendix F, `f6mutants.py`, on efd89fe9): every mutant fails at the test that holds its rule —
one realm's suite each, the others passing — but the two the stage controls hold.

| control | the rule taken out (or the old rule put back) | fails |
|---|---|---|
| m1-keys-off, -governor-off, -seats-off, -contracts-off, -solana-off | hlmailbox's guard skips that kind of value | hlmailbox TestMainnetPlaceholders |
| m1-digest-typo | HlAdminExec's digest one hex digit off | hlmailbox TestMainnetPlaceholders |
| m1-test1-unlisted, m1-canonical-off, m1-solana-live-off | the review's three: test1 out of the list, any spelling taken, any Solana namespace taken | hlmailbox TestMainnetPlaceholders |
| m1-every-chain | the guard reads every chain | hlmailbox stops at `init` on the test chain itself, no test runs |
| m1-init-call-dropped | `init` no longer calls the guard | nothing in the unit suite (30 pass): stage control x1 holds it |
| m1-gov-seats-off, m1-gov-recipient-off | r/hlgov's guard skips its seats, or the recipient | hlgov TestMainnetPlaceholders |
| m1-hlgnot-, -hlbubble-, -hlgnot2-router-off | the route's guard skips its router | that route's TestMainnetPlaceholder |
| m2-swap-refuses-former | the swap refuses a former recipient again (R29's old rule) | hlgov TestFormerRecipients (aborted at the swap back) |
| m2-retire-keeps-restored | a restored recipient stays in `former` | hlgov TestFormerRecipients |
| m2-kind9-takes-former | kind 9 takes a former recipient | hlgov TestFormerRecipients, TestEthereumKinds, TestDeploymentTargets |
| m4-raw-text | the description writes the text as typed | hlgov TestCancelDescription |
| m7-kind9-, -kind8-, -swap-contracts-off | that builder takes the deployment's contracts | hlgov TestDeploymentTargets |
| m7-swap-refuses-hladmin | the swap refuses HlAdmin too | hlgov TestDeploymentTargets (aborted) |
| m7-domain-origin-off, m7-domain-other-off | kind 6 takes Ethereum's own domain, or gno.land's other | hlgov TestRefusedDomain, TestDeploymentTargets |
| m7-kind6-rule-unwired | kind 6 no longer asks `refusedDomain` | hlgov TestDeploymentTargets |
| m7-ROUTE-router-contracts-off (×3) | the router swap takes the deployment's contracts | that route's TestRouterSwapTargets |
| m7-ROUTE-router-own-refused (×3) | the router swap refuses the route's own token too | hlbubble, hlgnot2 TestRouterSwapTargets; hlgnot TestYield (its swap back to the launch router), aborted |
| m3-ROUTE-verifier-unlisted (×3) | hlverifier out of the route's list | hlgnot, hlbubble TestUnpayableSwept; hlgnot2 TestMigration |
| m3-ROUTE-zero-off (×3) | a credit to the zero address not swept | the same |
| m3-ROUTE-votes-old (×3) | the credit vote takes the zero address (the old condition) | the same |
| f-gnot-figures-25-60, f-bubble-figures-500-1000, f-gnot2-figures-25-60 | a third set of launch figures | TestLaunchValues alone (its pin) |

The stage controls (§1's table) ran on the same tree; the pre-F6 deploy check on 00d97c09. The Solana tree's own
suite (`scripts/gnotest.sh`, `realm/` untouched): all 17 packages ok.

### Commands

```sh
# on the Mac: sync the worktree to the devbox
rsync -az --delete -e 'ssh -i ~/.ssh/id_ed25519_kourt' --exclude .git --include '/eth/***' --include '/realm/***' \
  --include '/scripts/***' --include '/docs/***' --include '/AGENTS.md' --exclude '*' \
  ~/src/jaekwon/bubblerumble-eth-f6/ root@<devbox>:/root/src/eth-f6/
# the suite (on the devbox, in /root/src/eth-f6)
GNO=/root/.cache/gno-toolchains/v1.2.0/gno GNOTEST_LINT=1 bash eth/scripts/gnotest.sh
# the interrealm checker over eth/realm (M1's shadow), the formatters
S=$(mktemp -d); mkdir $S/scripts; cp scripts/check-interrealm.py scripts/gnosource.py $S/scripts/
ln -s $PWD/eth/realm $S/realm; python3 $S/scripts/check-interrealm.py; rm -rf $S
/usr/local/go/bin/gofmt -l eth/realm/*/*.gno; /root/.cache/gno-toolchains/v1.2.0/gno fmt -diff -imports=false eth/realm/...
# oldcode: the working tree's tests over commit REV's realm code (F1's), leaving out a test file whose functions the
# old code has not got (E2-RL7's domains_test.gno)
git archive REV eth/realm | tar -x -C $D; find $D/eth/realm -name '*_test.gno' -delete
for f in eth/realm/*/*_test.gno; do cp $f $D/eth/realm/$(basename $(dirname $f))/; done   # → devbox $OLD
ETH_REALM_DIR=$OLD eth/scripts/gnotest.sh <realm>
# the pre-F6 deploy check: 00d97c09's eth/realm with hlmailbox's mainnetChainID rewritten to "dev", every realm loaded
sed -i 's/mainnetChainID = "gnoland-1"/mainnetChainID = "dev"/' $OLD/hlmailbox/mailbox.gno
for r in hlmailbox hlverifier hlfees hlgnot hlbubble hlgov hlgnot2; do ETH_REALM_DIR=$OLD eth/scripts/gnotest.sh $r; done
# the stage controls and the negative controls (appendix F)
python3 f6mutants.py /root/src/eth-f6 <out dir> [name …]
# the vectors: appendix A with C and E, in a copy of bridge/ (go.mod, go.sum, hl/), then gofmt -w each file written
HL_GEN_ETH=<tree root> go test -count=1 -run TestGenEthVectors ./hl/
```

## Appendix E — the vectors' generator, F6's delta to appendices A and C

Two GNOT burns for E2-RL3 (nonces 18 and 19, from the router the route holds after `TestSettings`) and two BUBBLE
burns re-signed for E2-RT5's figures (the burn-back 600 → 1,200, the dirty-recipient burn 250 → 500); every other
vector byte for byte as before. The file after this delta hashes `28b10223…`; appendix A with C alone hashes
`72c5a8a2…`.

```diff
--- a/hl/gen_eth_test.go (appendix A + C)
+++ b/hl/gen_eth_test.go (F6)
@@ -131,9 +131,9 @@
 		dirtyBob[i] = 0xff
 	}
 	bubble := []ethVector{
-		{"backAlice600", Message{3, 0, ethTestDomain, route, DomainGnoTest, self, TokenMessage(alice, 600*one, nil)}, []int{0, 1}}, // F1: a send of 600 must clear the minimum, 400 at plan e3 §5.5
+		{"backAlice1200", Message{3, 0, ethTestDomain, route, DomainGnoTest, self, TokenMessage(alice, 1_200*one, nil)}, []int{0, 1}}, // F6: a send of 1,200 must clear the minimum, 800 at E2-RT5's figures (F1: 600 against 400)
 		{"strangerRoute", Message{3, 1, ethTestDomain, stranger, DomainGnoTest, self, TokenMessage(alice, 150*one, nil)}, []int{0, 1}},
-		{"dirtyBob250", Message{3, 2, ethTestDomain, route, DomainGnoTest, self, TokenMessage(dirtyBob, 250*one, nil)}, []int{1, 2}},
+		{"dirtyBob500", Message{3, 2, ethTestDomain, route, DomainGnoTest, self, TokenMessage(dirtyBob, 500*one, nil)}, []int{1, 2}}, // F6: at or above the fee, 400 at E2-RT5's figures, so the recipient sweeps it, never its size
 		{"backAlice500", Message{3, 3, ethTestDomain, route, DomainGnoTest, self, TokenMessage(alice, 500*one, []byte("memo"))}, []int{0, 2}},
 		{"backCarol50", Message{3, 4, ethTestDomain, route, DomainGnoTest, self, TokenMessage(mustB32(testAddress("hlb-carol")), 50*one, nil)}, []int{0, 1}},
 	}
@@ -166,6 +166,13 @@
 	for i, r := range []struct{ name, path string }{{"Mailbox", "hlmailbox"}, {"Verifier", "hlverifier"}, {"Gnot", "hlgnot"}, {"Bubble", "hlbubble"}, {"Fees", "hlfees"}, {"Gov", "hlgov"}} {
 		gnot = append(gnot, ethVector{"inSol" + r.name + "150", Message{3, uint32(12 + i), ethTestDomain, groute2, DomainGnoTest, gself, TokenMessage(mustB32(GnoPackageAddress(solNamespace+r.path)), 150*one, nil)}, signers[i%3]})
 	}
+	// F6 (the round-2 realms audit's E2-RL3): burns of 150 GNOT whose recipient is this deployment's own hlverifier, or
+	// the zero address (a burn whose recipient field was left empty), which the route sweeps as one naming a realm of
+	// the bridge; sent by the router the route holds then (testRoute2)
+	gnot = append(gnot,
+		ethVector{"inVerifier150", Message{3, 18, ethTestDomain, groute2, DomainGnoTest, gself, TokenMessage(mustB32(GnoPackageAddress(ethNamespace+"hlverifier")), 150*one, nil)}, []int{0, 1}},
+		ethVector{"inZero150", Message{3, 19, ethTestDomain, groute2, DomainGnoTest, gself, TokenMessage([32]byte{}, 150*one, nil)}, []int{1, 2}},
+	)
 	write := func(dir string, vs []ethVector, extra string) {
 		p := filepath.Join(root, "eth", "realm", dir, "vectors_test.gno")
 		if err := os.WriteFile(p, []byte(ethGnoFile(dir, vs, extra)), 0o644); err != nil {
```

## Appendix F — F6's controls (`f6mutants.py`)

Run on the devbox: `python3 f6mutants.py /root/src/eth-f6 <out dir> [name …]` (`--realms <dir>` stages another
`eth/realm`); scratch copies under `<out dir>/scratch`. A stage control prints OK when the realm stopped at `init` with
its message and ran no test, or loaded when it should; a mutant prints the failed and aborted tests.

```python
#!/usr/bin/env python3
"""Negative controls for eth F6 (eth/bridge/M1-NOTES.md, "Fold F6"). A mutant takes one F6 rule back out of a scratch
copy of eth/realm (or puts the old rule back) and runs the named realms' suites there (eth/scripts/gnotest.sh with
ETH_REALM_DIR); it must fail at the test that holds the rule. A stage control rewrites launch lines as a stage step
would, in a tree where the harness's own chain id ("dev") counts as gnoland-1 (hlmailbox's mainnetChainID rewritten),
and loads the named realm: it must stop at init with the named message, or — fully staged — load and run its tests.
Usage: f6mutants.py <tree root> <out dir> [--realms <dir>] [name ...]   (--realms: another eth/realm, e.g. an old one)"""
import hashlib, os, re, shutil, subprocess, sys

args = sys.argv[1:]
ROOT, OUT = args[0], args[1]
REALMS = os.path.join(ROOT, "eth", "realm")
rest = args[2:]
if rest[:1] == ["--realms"]:
    REALMS, rest = rest[1], rest[2:]
ONLY = set(rest)

# ---- bech32 (BIP-173), for staged gno addresses
CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
def _polymod(values):
    gen = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3]
    chk = 1
    for v in values:
        b = chk >> 25
        chk = (chk & 0x1ffffff) << 5 ^ v
        for i in range(5):
            chk ^= gen[i] if ((b >> i) & 1) else 0
    return chk
def _bits(data):
    acc, bits, out = 0, 0, []
    for b in data:
        acc = (acc << 8) | b; bits += 8
        while bits >= 5:
            bits -= 5; out.append((acc >> bits) & 31)
    if bits:
        out.append((acc << (5 - bits)) & 31)
    return out
def gno_addr(seed):
    data = _bits(hashlib.sha256(seed.encode()).digest()[:20])
    hrp = "g"
    pm = _polymod([ord(x) >> 5 for x in hrp] + [0] + [ord(x) & 31 for x in hrp] + data + [0] * 6) ^ 1
    return hrp + "1" + "".join(CHARSET[d] for d in data + [(pm >> 5 * (5 - i)) & 31 for i in range(6)])
def eth_addr(seed):
    return "0x" + hashlib.sha256(seed.encode()).hexdigest()[:40]
def key(seed):
    return hashlib.sha256(seed.encode()).hexdigest()

NAMES = ["Mailbox", "Token0", "Token1", "Client", "Gate", "HlAdmin", "ProxyAdmin", "MerkleTreeHook", "ProtocolFee",
         "MailboxImpl", "Token0Impl", "Token1Impl", "ClientImpl", "GateImpl", "Ed25519Verifier", "HlAdminExec"]
GOV = gno_addr("f6 staged governor")
TOK0, TOK1 = eth_addr("f6 staged Token0"), eth_addr("f6 staged Token1")

# ---- line edits: (file, regex, replacement); each must match exactly once
def sub(path, pattern, repl):
    return (path, pattern, repl)
MAINNET = [sub("hlmailbox/mailbox.gno", r'mainnetChainID = "gnoland-1"', 'mainnetChainID = "dev"')]
SIGNERS = [sub("hlmailbox/mailbox.gno", r'launchSigners     = "[a-f0-9,]*"',
               'launchSigners     = "%s"' % ",".join(key("f6 staged signer %d" % i) for i in range(3)))]
def governor(realms):
    return [sub("%s/%s.gno" % (r, "mailbox" if r == "hlmailbox" else r), r'(?m)^const launchGovernor = address\("[a-z0-9]*"\)',
                'const launchGovernor = address("%s")' % GOV) for r in realms]
SEATS = [sub("hlmailbox/flags.gno", r'launchSeat%d = address\("[a-z0-9]*"\)' % n,
             'launchSeat%d = address("%s")' % (n, gno_addr("f6 staged seat %d" % n))) for n in range(2, 7)]
CONTRACTS = [sub("hlmailbox/ethdeploy.gno", r'launchEth%s *= "0x[0-9a-fA-F]*"' % n,
                 'launchEth%s = "%s"' % (n, eth_addr("f6 staged " + n))) for n in NAMES]
SOLANA = [sub("hlmailbox/soldeploy.gno", r'(?m)^const solanaNamespace = "[^"]*"',
              'const solanaNamespace = "gno.land/r/g1zdaz6sypk5tdsjc2fzsqrrn4ttn8qzmnl4waqp/"')]
MAILBOX_STAGED = MAINNET + SIGNERS + governor(["hlmailbox"]) + SEATS + CONTRACTS + SOLANA
ROUTERS = {r: [sub("%s/%s.gno" % (r, r), r'(?m)^const launchRouter = "0x[0-9a-fA-F]*"', 'const launchRouter = "%s"' % t)]
           for r, t in (("hlgnot", TOK0), ("hlbubble", TOK1), ("hlgnot2", TOK0))}
GOVSEATS = [sub("hlgov/hlgov.gno", r'launchEthSeat%d = "0x[0-9a-fA-F]*"' % i,
                'launchEthSeat%d = "%s"' % (i, eth_addr("f6 staged eth seat %d" % i))) for i in range(1, 7)]
NO_MAILBOX_GUARD = [sub("hlmailbox/mailbox.gno", r'\tmustNotBeDevTree\(\) // [^\n]*\n', "")]

STAGES = [
    # name, realm loaded, edits, what the log must show ("stop: <text>" at init, or "loads")
    ("s1-mailbox-dev", "hlmailbox", MAINNET, "stop: hlmailbox: launchSigners holds a dev tree test signer"),
    ("s1-mailbox-signers", "hlmailbox", MAINNET + SIGNERS, "stop: hlmailbox: launchGovernor is a test address"),
    ("s1-mailbox-governor", "hlmailbox", MAINNET + SIGNERS + governor(["hlmailbox"]), "stop: hlmailbox: launchSeat2 is a test address"),
    ("s1-mailbox-seats", "hlmailbox", MAINNET + SIGNERS + governor(["hlmailbox"]) + SEATS, "stop: hlmailbox: launchEthMailbox (the mailbox) is a dev tree placeholder"),
    ("s1-mailbox-contracts", "hlmailbox", MAINNET + SIGNERS + governor(["hlmailbox"]) + SEATS + CONTRACTS, "stop: hlmailbox: solanaNamespace is the dev tree's"),
    ("s1-mailbox-staged", "hlmailbox", MAILBOX_STAGED, "loads"),
    # the review's scenario: a stage step that wrote this deployment's own namespace root as the Solana one
    ("s1-mailbox-own-namespace", "hlmailbox", MAINNET + SIGNERS + governor(["hlmailbox"]) + SEATS + CONTRACTS +
     [sub("hlmailbox/soldeploy.gno", r'(?m)^const solanaNamespace = "[^"]*"', 'const solanaNamespace = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/"')],
     "stop: hlmailbox: solanaNamespace is gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/, not the Solana deployment's on gnoland-1"),
    ("s1-gnot", "hlgnot", MAILBOX_STAGED + governor(["hlgnot"]), "stop: hlgnot: launchRouter is a dev tree placeholder"),
    ("s1-gnot-staged", "hlgnot", MAILBOX_STAGED + governor(["hlgnot"]) + ROUTERS["hlgnot"], "loads"),
    ("s1-bubble", "hlbubble", MAILBOX_STAGED + governor(["hlbubble"]), "stop: hlbubble: launchRouter is a dev tree placeholder"),
    ("s1-bubble-staged", "hlbubble", MAILBOX_STAGED + governor(["hlbubble"]) + ROUTERS["hlbubble"], "loads"),
    # hlgnot2's tests import r/hlgov (and through it r/hlbubble): every realm it loads is staged but its own router
    ("s1-gnot2", "hlgnot2", MAILBOX_STAGED + governor(["hlgnot", "hlbubble", "hlgov", "hlgnot2"]) + ROUTERS["hlgnot"] + ROUTERS["hlbubble"] + GOVSEATS,
     "stop: hlgnot2: launchRouter is a dev tree placeholder"),
    ("s1-gnot2-staged", "hlgnot2", MAILBOX_STAGED + governor(["hlgnot", "hlbubble", "hlgov", "hlgnot2"]) + ROUTERS["hlgnot"] + ROUTERS["hlbubble"] + GOVSEATS + ROUTERS["hlgnot2"],
     "loads"),
    ("s1-gov", "hlgov", MAILBOX_STAGED + governor(["hlgnot", "hlbubble", "hlgov"]) + ROUTERS["hlgnot"] + ROUTERS["hlbubble"], "stop: hlgov: launchEthSeat1 is a dev tree placeholder"),
    ("s1-gov-staged", "hlgov", MAILBOX_STAGED + governor(["hlgnot", "hlbubble", "hlgov"]) + ROUTERS["hlgnot"] + ROUTERS["hlbubble"] + GOVSEATS, "loads"),
    # a mutant of the wiring, which only a stage control can see (the unit tests call the guard themselves): init's
    # call dropped, the dev tree at "gnoland-1" loads — s1-mailbox-dev's stop is gone
    ("x1-init-call-dropped", "hlmailbox", MAINNET + NO_MAILBOX_GUARD, "loads"),
]

# ---- mutants: name, realms to test, file, old (exact, once), new
MUTANTS = [
    # E2-RL1, the rule's parts (the wiring is the stage controls')
    ("m1-keys-off", "hlmailbox", "hlmailbox/mainnet.gno", "\t\tif sha256In(devKeys[:], k[:]) {", "\t\tif false && sha256In(devKeys[:], k[:]) {"),
    ("m1-governor-off", "hlmailbox", "hlmailbox/mainnet.gno", "\tif sha256In(devAddresses[:], []byte(v.governor)) {", "\tif false && sha256In(devAddresses[:], []byte(v.governor)) {"),
    ("m1-seats-off", "hlmailbox", "hlmailbox/mainnet.gno", "\t\tif sha256In(devAddresses[:], []byte(v.seats[n])) {", "\t\tif false && sha256In(devAddresses[:], []byte(v.seats[n])) {"),
    ("m1-contracts-off", "hlmailbox", "hlmailbox/mainnet.gno", "\t\tif sha256In(devContracts[:], c[:]) {", "\t\tif false && sha256In(devContracts[:], c[:]) {"),
    ("m1-solana-off", "hlmailbox", "hlmailbox/mainnet.gno", "\tif sha256In([]string{devSolanaNamespace}, []byte(v.solana)) {", "\tif false && sha256In([]string{devSolanaNamespace}, []byte(v.solana)) {"),
    # the review's two (F6): test1 among the test addresses, canonical gno addresses, the live Solana namespace
    ("m1-test1-unlisted", "hlmailbox", "hlmailbox/mainnet.gno", '\t"7d79fb46d106c31e857fda2f5582281afd950fc015faccfa004e673e857c3fe8",\n', ""),
    ("m1-canonical-off", "hlmailbox", "hlmailbox/mainnet.gno", "func canonical(a address) bool { return a.IsValid() && string(a) == strings.ToLower(string(a)) }",
     "func canonical(a address) bool { return true }"),
    ("m1-solana-live-off", "hlmailbox", "hlmailbox/mainnet.gno", "\tif !sha256In([]string{mainnetSolanaNamespace}, []byte(v.solana)) {", "\tif false && !sha256In([]string{mainnetSolanaNamespace}, []byte(v.solana)) {"),
    ("m1-digest-typo", "hlmailbox", "hlmailbox/mainnet.gno", '"194d9be1d6b08b527e506daab0c78c144d67442bcaf28cdac093bd7ae8585fbf"', '"194d9be1d6b08b527e506daab0c78c144d67442bcaf28cdac093bd7ae8585fbe"'),
    ("m1-init-call-dropped", "hlmailbox", "hlmailbox/mailbox.gno", "\tmustNotBeDevTree() // on gnoland-1, no launch value may still be the dev tree's (mainnet.gno)\n", ""),
    ("m1-every-chain", "hlmailbox", "hlmailbox/mainnet.gno", "\tif chainID != mainnetChainID {\n\t\treturn \"\"\n\t}", "\tif false {\n\t\treturn \"\"\n\t}"),
    ("m1-gov-seats-off", "hlgov", "hlgov/mainnet.gno", "\t\tif isDev(devEthSeats[:], seats[n]) {", "\t\tif false && isDev(devEthSeats[:], seats[n]) {"),
    ("m1-gov-recipient-off", "hlgov", "hlgov/mainnet.gno", "\tif isDev([]string{devHlAdmin}, hlAdmin) {", "\tif false && isDev([]string{devHlAdmin}, hlAdmin) {"),
] + [("m1-%s-router-off" % r, r, "%s/mainnet.gno" % r, "\t\tif x == d {", "\t\tif false && x == d {") for r in ("hlgnot", "hlbubble", "hlgnot2")] + [
    # E2-RL2
    ("m2-swap-refuses-former", "hlgov", "hlgov/hlgov.gno",
     '\t\t\tpanic("hlgov: the recipient must differ from the current one (" + ea(p) + ")")\n\t\t}\n',
     '\t\t\tpanic("hlgov: the recipient must differ from the current one (" + ea(p) + ")")\n\t\t}\n\t\tif isFormer(p) {\n\t\t\tpanic("hlgov: never a former decision recipient (" + ea(p) + "): it is retired")\n\t\t}\n'),
    ("m2-retire-keeps-restored", "hlgov", "hlgov/hlgov.gno", "\t\tif f != next && f != old {", "\t\tif f != old {"),
    ("m2-kind9-takes-former", "hlgov", "hlgov/hlgov.gno", "\t\tmustNotBeFormer(p)\n", "\t\t_ = p\n"),
    # E2-RL4
    ("m4-raw-text", "hlgov", "hlgov/hlgov.gno", '\ttext := "cancel decisions " + c.String()', '\ttext := "cancel decisions " + numbers'),
    # E2-RL7, r/hlgov
    ("m7-kind9-contracts-off", "hlgov", "hlgov/hlgov.gno", "\t\tif name := hlmailbox.EthContractName(p); name != \"\" {\n\t\t\tpanic(\"hlgov: the successor \"",
     "\t\tif name := hlmailbox.EthContractName(p); false && name != \"\" {\n\t\t\tpanic(\"hlgov: the successor \""),
    ("m7-kind8-contracts-off", "hlgov", "hlgov/hlgov.gno", "\tif name := hlmailbox.EthContractName(impl); name != \"\" {", "\tif name := hlmailbox.EthContractName(impl); false && name != \"\" {"),
    ("m7-swap-contracts-off", "hlgov", "hlgov/hlgov.gno", "\t\tif name := hlmailbox.EthContractName(p); name != \"\" && p != ethContract(hlmailbox.EthHlAdmin) {",
     "\t\tif name := hlmailbox.EthContractName(p); false && name != \"\" && p != ethContract(hlmailbox.EthHlAdmin) {"),
    ("m7-swap-refuses-hladmin", "hlgov", "hlgov/hlgov.gno", "\t\tif name := hlmailbox.EthContractName(p); name != \"\" && p != ethContract(hlmailbox.EthHlAdmin) {",
     "\t\tif name := hlmailbox.EthContractName(p); name != \"\" {"),
    ("m7-domain-origin-off", "hlgov", "hlgov/hlgov.gno", "\tif domain == origin {", "\tif false && domain == origin {"),
    ("m7-domain-other-off", "hlgov", "hlgov/hlgov.gno", "\tif domain != local && (domain == hlmailbox.DomainMainnet || domain == hlmailbox.DomainTest) {",
     "\tif false && domain != local && (domain == hlmailbox.DomainMainnet || domain == hlmailbox.DomainTest) {"),
    ("m7-kind6-rule-unwired", "hlgov", "hlgov/hlgov.gno", "\tif why := refusedDomain(domain, hlmailbox.LocalDomain(), hlmailbox.LaunchOrigin()); why != \"\" {",
     "\tif why := refusedDomain(domain, hlmailbox.LocalDomain(), hlmailbox.LaunchOrigin()); false && why != \"\" {"),
] + [m for r, tok in (("hlgnot", "EthToken0"), ("hlbubble", "EthToken1"), ("hlgnot2", "EthToken0")) for m in (
    ("m7-%s-router-contracts-off" % r, r, "%s/%s.gno" % (r, r), "\tif name := hlmailbox.EthContractName(k); name != \"\" && k != token {",
     "\tif name := hlmailbox.EthContractName(k); false && name != \"\" && k != token {"),
    ("m7-%s-router-own-refused" % r, r, "%s/%s.gno" % (r, r), "\tif name := hlmailbox.EthContractName(k); name != \"\" && k != token {",
     "\tif name := hlmailbox.EthContractName(k); name != \"\" && (k != token || true) {"),
)] + [m for r in ("hlgnot", "hlbubble", "hlgnot2") for m in (
    # E2-RL3
    ("m3-%s-verifier-unlisted" % r, r, "%s/%s.gno" % (r, r), ",\n\t\tchain.PackageAddress(namespace + \"hlverifier\")}", "}"),
    ("m3-%s-zero-off" % r, r, "%s/%s.gno" % (r, r), "\treturn to == zeroAddress || isRealm(to) || hlmailbox.IsSolanaRealm(to)", "\treturn isRealm(to) || hlmailbox.IsSolanaRealm(to)"),
    ("m3-%s-votes-old" % r, r, "%s/%s.gno" % (r, r), "\tif !to.IsValid() || unpayable(to) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftCredit(" % r,
     "\tif !to.IsValid() || isRealm(to) || hlmailbox.IsSolanaRealm(to) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftCredit(" % r),
)] + [
    # the fees: a third set of figures fails each route's pin alone
    ("f-gnot-figures-25-60", "hlgnot", "hlgnot/hlgnot.gno", "\tlaunchFee      = 20 * gnot\n\tlaunchMinimum  = 40 * gnot\n", "\tlaunchFee      = 25 * gnot\n\tlaunchMinimum  = 60 * gnot\n"),
    ("f-bubble-figures-500-1000", "hlbubble", "hlbubble/hlbubble.gno", "\tlaunchFee      = 400 * one\n\tlaunchMinimum  = 800 * one\n", "\tlaunchFee      = 500 * one\n\tlaunchMinimum  = 1_000 * one\n"),
    ("f-gnot2-figures-25-60", "hlgnot2", "hlgnot2/hlgnot2.gno", "\tlaunchFee      = 20 * gnot\n\tlaunchMinimum  = 40 * gnot\n", "\tlaunchFee      = 25 * gnot\n\tlaunchMinimum  = 60 * gnot\n"),
]

SCRATCH = os.path.join(OUT, "scratch")

def stage(name):
    d = os.path.join(SCRATCH, name)
    shutil.rmtree(d, ignore_errors=True)
    shutil.copytree(REALMS, d)
    return d

def edit(d, path, old, new):
    p = os.path.join(d, path)
    s = open(p, encoding="utf-8").read()
    n = s.count(old)
    if n != 1:
        return "DID NOT APPLY (%d matches of %r)" % (n, old[:60])
    open(p, "w", encoding="utf-8").write(s.replace(old, new))
    return ""

def resub(d, path, pattern, repl):
    p = os.path.join(d, path)
    s = open(p, encoding="utf-8").read()
    s2, n = re.subn(pattern, lambda m: repl, s)
    if n != 1:
        return "DID NOT APPLY (%d matches of %r in %s)" % (n, pattern, path)
    open(p, "w", encoding="utf-8").write(s2)
    return ""

def suite(name, d, realm):
    logs = os.path.join(OUT, name)
    os.makedirs(logs, exist_ok=True)
    env = dict(os.environ, ETH_REALM_DIR=d, GNOTEST_LOGS=logs)
    r = subprocess.run([os.path.join(ROOT, "eth", "scripts", "gnotest.sh"), realm], cwd=ROOT, env=env, capture_output=True, text=True)
    head = r.stdout.splitlines()[0] if r.stdout else r.stderr[:300]
    log = open(os.path.join(logs, realm + ".log"), encoding="utf-8", errors="replace").read()
    return head, log

def run_mutant(name, realms, path, old, new):
    d = stage(name)
    bad = edit(d, path, old, new)
    if bad:
        return bad
    lines = []
    for rl in realms.split(","):
        head, log = suite(name, d, rl)
        fails = re.findall(r"^--- FAIL: (\w+)", log, re.M)
        panics = re.findall(r"^=== RUN   (\w+)\n(?:(?!=== RUN).*\n)*?panic: (.{0,100})", log, re.M)
        lines.append("%s | failed: %s | aborted: %s" % (head, ",".join(fails) or "-", ";".join("%s (%s)" % p for p in panics) or "-"))
    shutil.rmtree(d, ignore_errors=True)
    return ("\n" + " " * 34).join(lines)

def run_stage(name, realm, edits, want):
    d = stage(name)
    for path, pattern, repl in edits:
        bad = resub(d, path, pattern, repl)
        if bad:
            return bad
    head, log = suite(name, d, realm)
    ran = len(re.findall(r"^=== RUN", log, re.M))
    m = re.search(r"(gno\.land/r/[^\s:]*:\d+: .{0,160}|panic: .{0,160})", log)
    first = m.group(0) if m else (log.splitlines()[0][:160] if log else "")
    if want == "loads":
        verdict = "OK (loaded)" if ran > 0 else "UNEXPECTED (did not load)"
    else:
        verdict = "OK (stopped at init)" if ran == 0 and want[len("stop: "):] in log else "UNEXPECTED"
    shutil.rmtree(d, ignore_errors=True)
    return "%s | %s | tests run: %d | %s" % (verdict, head, ran, first)

for s in STAGES:
    if not ONLY or s[0] in ONLY:
        print("%-33s %s" % (s[0], run_stage(*s)), flush=True)
for m in MUTANTS:
    if not ONLY or m[0] in ONLY:
        print("%-33s %s" % (m[0], run_mutant(*m)), flush=True)
```

## Fold F9 — the small realm items F6 left, before audit round 3 (2026-10-14)

Lane F9, branch `eth-f9` from `bridge-eth` be9d5e92 (F7's merge), worktree `~/src/jaekwon/bubblerumble-eth-f9`, devbox
`/root/src/eth-f9`. It folds what Fold F6's independent review left for the realms (its points 3–7, and BUILD.md's
"From F6" bullet): Q-F6b (the recipient swap names the recipient it replaces), Q-F6c (kind 8 may roll a proxy back to
its own launch implementation), the two follow-ups (kinds 5 and 7 refuse the deployment's sixteen; the votes read a
typed address in its canonical spelling), with Q-F6a left as documented — and what F9's own two independent reviews
found in that, touching `eth/realm/**` and this file only. Each change has a test that fails on the code before it —
the commit's tests over its parent's realm code (`oldcode` in the commands) — and passes after; the negative controls
(appendix G) take each rule back out, one at a time.

| commit (in order) | change |
|---|---|
| 76d7dd2f | 1. Q-F6b: `set decision recipient` takes `from`; made, executed and applied only while it is the recipient |
| fbdf051b | 2. Q-F6c: kind 8 takes the proxy's own launch implementation (a rollback); the other fifteen refused |
| 09ea362a | 3. kind 5's keys and kind 7's beneficiary refuse the deployment's sixteen |
| 798991a2 | 4. the routes' credit and sweep votes read the recipient in its canonical spelling: `hlmailbox.CanonicalAddress` |
| 300e65ce | 5. init's guard on gnoland-1 reads "canonical" the same way (the first review's finding 2) |
| e2b738ba | 6. kinds 5, 7 and 8 refuse HlAdmin's line; a swap refuses a seat's key; the swap's words say exactly what it holds to (both reviews) |
| eb199508 | 7. the siblings, beyond the brief and last so they drop cleanly: hlmailbox's seats and hlfees' treasurer canonical too |
| (this) | these notes |

### 1. Q-F6b — the recipient swap names the recipient it replaces

- **What.** `NewSetDecisionRecipientRequest(cur, from, contract)`: `from`, read by the deployment's parser (any
  spelling), must be the decision recipient — the first test of `check`, so when the proposal is made and again in its
  closure — and `Execute` applies the queued swap only while `from` still is (`recipientParams` keeps it), as
  hlmailbox's queued rotations keep the set they were decided against (`rotateSignersParams.oldKeys`,
  `rotateSeatsParams.before`). A swap refused at `Execute` stays pending for GovDAO's cancel (`NewCancelQueuedRequest`).
  The description reads `set decision recipient to X, replacing Y (applies only while Y is the recipient)` (it read
  `… (now Y)`, a remark the swap did not hold to); the ledger's note `queued 7 days; applies only while Y is the
  recipient`. `Execute`'s old refusal — the target is the recipient already — is gone, unreachable now: the builder
  refuses a target equal to the recipient after confirming `from` is it, so a recorded swap's target always differs
  from its `from` (the review proved it: one construction site, right after `check`), and one whose `from` stands
  never finds its target there.
- **What it covers, and what not.** A swap pre-armed (voted, unexecuted) or left queued never applies while another
  swap's target stands in place of the recipient it was decided against — the review's case: a swap to keyG pre-armed
  beside the swap that makes keyG the recipient, keyG then replaced (it handed over), would point decisions back at
  keyG. The rule reads the recipient as it is, not the swaps since (the first review's finding 3, low; not a
  regression): a stale swap whose recipient is later restored applies again, so GovDAO cancels a stale swap before a
  swap back restores the recipient it names (76d7dd2f's message, "never points decisions back at a HlAdmin that served
  and handed over since", says more than this; e2b738ba's doc says it exactly). And the stale undo — a swap back to A
  made and applied while B is the recipient, the kind 9 A → B having executed on Ethereum in between — is made and
  applied against the same recipient, so taken: no realm sees Ethereum; hlwatch's to alarm (F9-2).
- **Tests.** `TestSwapReplacesItsRecipient` (new, before `TestFormerRecipients`): a swap from keyC (former), the
  launch HlAdmin (former), keyA (no recipient ever), the zero address or its own target keyD is refused when made,
  naming both, nothing recorded; the recipient in four spellings (as written, 64 digits with and without `0x`, padded)
  is taken and the description names both in EIP-55 case; two swaps decided against keyE queued together (to keyC, to
  keyK) and a third pre-armed six days on (to keyD): the first lands, the second is refused at `Execute` and stays
  pending, the pre-armed one's closure refuses it, nothing recorded; keyE is restored and GovDAO cancels the second.
  In `TestFormerRecipients` the pre-armed part is the review's case — a swap and a kind 9 to keyG pre-armed against
  keyF six days after the swap that makes keyG the recipient, keyG then replaced by keyH: the kind 9 refused as former
  (as in F6), the swap refused as decided against keyF, nothing dispatched or recorded — and E2-RL2's undo runs with
  two swaps from keyH to keyG queued together, the second refused at `Execute` while keyG is the recipient and applied
  once keyH is restored (finding 3's behaviour, asserted). Every test makes the swap through `swapRequest(cur, from,
  to)` (one line holds the builder's signature); `TestEthereumKinds`' address loop holds `from` to the parser's
  refusals too.
- **Fails before** (be9d5e92's realm code; the helper's one line rewritten to the old signature, `from` dropped, the
  commands): 14 of 18 pass. `TestSwapReplacesItsRecipient: expected an abort containing "hlgov: the swap replaces
  0x3030…30, but the decision recipient is 0x5050…50: …", got none`; `TestFormerRecipients: expected an abort
  containing "hlgov: the swap replaces 0x6060…60, but the decision recipient is 0x6262…62: …", got none` — the old
  closure queued the pre-armed swap, decisions bound back for keyG, former; `TestEthereumKinds` (the old builder reads
  no `from`: a dirty one taken); `TestDeploymentTargets` (the description `… (now 0x6262…62)`). Passes after.

### 2. Q-F6c — kind 8 rolls a proxy back to its own launch implementation

- **What.** `NewUpgradeRequest` refuses every contract of the deployment as the implementation but the proxy's own
  launch implementation (`launchImplementation(p)`: the i-th of the five proxies → hlmailbox's `EthMailboxImpl + i`,
  ethdeploy.gno's order), a rollback to the audited launch code, which stays deployed. The description says so:
  `upgrade <name> <proxy> to implementation <impl> (its launch implementation: a rollback) with code hash 0x…`.
  Another proxy's launch implementation and the other contracts stay refused; the message names the exception.
  HlAdmin executes it as any kind 8 (`HlAdminExec._upgrade`, 315–347): the code hash pinned, the proxy left on the
  implementation with HlAdmin its holder. A call stays allowed as for any upgrade; the launch code's initializer has
  run, so an `initialize` there reverts and the decision pends for GovDAO's cancel. The review verified the mapping
  for all five (indices 0–4 and 9–13, `distinctContracts` keeping the sixteen apart) and found no way to aim it
  elsewhere; its finding 6 (info): a rollback reinstates whatever a later upgrade replaced, and storage is not rolled
  back — for hlwatch to say beside a rollback (F9-4).
- **Tests.** `TestRollback` (new): for each of the five, its own launch implementation (ethdeploy.gno's placeholders
  spelled out, never read from the realm; the proxy in lower case, the implementation in 64 digits) taken with the
  description, and every other contract of the deployment refused — the other four launch implementations among them,
  70 cases — nothing recorded; the gate's rollback passed and dispatched, its body byte for byte.
  `TestDeploymentTargets` keeps its kind 8 refusals, naming a proxy the address is not the launch implementation of.
- **Fails before** (76d7dd2f): 18 of 19; `TestRollback: the rollback of the mailbox to its launch implementation
  0x0808…08 refused: hlgov: the implementation 0x0808…08 is the mailbox's implementation of this deployment: an
  upgrade names code deployed for it, never one of the deployment's sixteen contracts`. Passes after.

### 3. Kind 5's keys and kind 7's beneficiary refuse the deployment's sixteen

- **What.** `parseSeatChanges` refuses an entry whose key is one of hlmailbox's sixteen, after the zero check: `seat N's
  key X is <name> of this deployment, a contract of the bridge that flags and holds nothing: the seat would be held by
  nobody`. `NewSetProtocolFeeRequest` refuses such a beneficiary: `… which takes no ETH: collectProtocolFees would
  revert, the fees held in the hook until another kind 7` — `ProtocolFee.collectProtocolFees` pays the balance by
  `sendValue`, and none of the sixteen has a receive function or a payable fallback (src/ has none; nor have Hyperlane's
  Mailbox, MerkleTreeHook, ProtocolFee and HypERC20, nor OpenZeppelin's ProxyAdmin; the proxies delegate to those
  implementations). Both at creation only: the list is compiled, so a re-check in the closure would read the same.
  (HlAdmin's line, which moves, is item 6.)
- **Tests.** `TestDeploymentKeys` (new): each of the sixteen refused on every seat in turn, alone in 64 digits and as
  the second entry after a key of no contract, and as kind 7's beneficiary in lower case; an address of no contract
  taken by both; nothing recorded.
- **Fails before** (fbdf051b): 19 of 20; `TestDeploymentKeys: expected an abort containing "hlgov: seat 1's key
  0x0404…04 is the mailbox of this deployment, …", got none` (kind 7's half: control k-kind7-contracts-off). Passes
  after.

### 4. The credit and sweep votes read the recipient in its canonical spelling

- **What.** gno's `address.IsValid` takes four spellings of one account: as gno writes it, all in upper case, and with
  bech32m's checksum (BIP-350) in either case — tm2's decoder (btcutil's `DecodeNoLimit`) lowers an all-upper string
  and accepts either checksum constant, 1 or 0x2bc830a3, then asks for the prefix `g` and twenty bytes. Every address
  gno writes (`chain.PackageAddress`, a caller's, `Bytes32ToAddress`) is the first, so the realm lists hold only it,
  and the votes compared strings: a realm of the bridge or the zero address in another spelling passed `unpayable`
  (GNOT out of the lock into a realm's account or burned; BUBBLE, whose grc20 ledger keys an account by its string,
  under a key nobody spends). `hlmailbox.CanonicalAddress(a)` (new, `gnoaddr.gno`) is the round trip through
  p/hyperlane — `AddressToBytes32` decodes any spelling IsValid takes (gno's `crypto/bech32`: one case, either
  checksum), `Bytes32ToAddress` writes gno's — or "" for no gno address; a plain function over values, as
  `ParseEthAddress`. Each route's two votes read the typed address through it before `unpayable`, and draft with the
  canonical one: the credit's claim and the sweep's payment go to it.
- **How this item came to be.** The fold's first version lowered the address after IsValid (since rebuilt away);
  F9's independent review found that bech32m's spelling survives lowering (finding 1, medium: "fix before any upload to
  gnoland-1"), with the four spellings of the zero address and hlverifier worked out by an encoder checked against
  BIP-350's vectors; this lane's own encoder (appendix H's, against the same vectors) gives the same. The round trip
  replaced the lowering, the tests run all four spellings, and the history was rebuilt so each commit holds as it
  stands (after the second review too: its findings landed in items 6 and 7, and item 4's message was corrected).
- **Tests.** hlmailbox `TestCanonicalAddress` (new, `gnoaddr_test.gno`): for the zero address, hlmailbox's own and a
  testutils address, all four spellings valid to IsValid and read as the first; mixed case, a checksum one character
  off, a padded one, another prefix, twenty-one bytes, "" and `g1notanaddress` refused by IsValid and read as none;
  the zero address's bech32m spelling pinned as the independent encoder writes it
  (`g1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq4rvsrm`), holding the tests' `spellings` helper (gno's `bech32.EncodeM`) to it.
  hlgnot's and hlbubble's `TestVotesCanonical` (new; hlgnot's before `TestRouterSwapTargets`, since `TestMigrate`
  leaves no draft taken): each realm of the route's list, every route the mailbox registers, the Solana deployment's
  six and the zero address, in each of the four spellings, refused by both votes (a failure names the vote and the
  address), nothing recorded; an owner (dave, carol) in each other spelling named canonical by both drafts, and a sweep
  of a zero burn's entry typed with bech32m's checksum in upper case passed and executed — dave +150 GNOT; carol +401
  BUBBLE under her own key and none under any other spelling, the counters' identity holding. hlgnot2: the same
  refusals and descriptions in `TestMigration`'s drill, after its hlverifier and zero burns and before it migrates on.
- **Fails before** (09ea362a, `gnoaddr_test.gno` left out — it calls `CanonicalAddress`): hlgnot 15 of 16, hlbubble 12
  of 13, hlgnot2 5 of 6, each `the credit vote took G1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQLUUXE as its recipient` — the
  zero address in upper case, the first other spelling (control a-canonical-lower-only shows the bech32m one taken
  too: `the credit vote took g1qqqq…4rvsrm`). Passes after.

### 5. Init's guard reads "canonical" the same way (the first review's finding 2)

- **What.** F6's `canonical(a)` (mainnet.gno: a governor or seat on gnoland-1 must be "a gno address in its canonical
  form") took `a.IsValid() && a == lower(a)`, so a governor or seat staged with bech32m's checksum passed — a governor
  nobody can be (Govern and Yield compare the caller's address, so no realm of the deployment could ever decide), a seat
  that never flags; and a dev address so spelled missed the guard's digests, which hash the string. Now
  `canonical(a) := a != "" && CanonicalAddress(a) == a`. Only a stage step could write such a value (no tool emits
  bech32m for gno), so low.
- **Tests.** `TestMainnetPlaceholders` refuses, as governor and on every seat, jaeCold's bech32m spelling and the dev
  governor's in either case, beside F6's upper-case and malformed ones; `TestCanonicalAddress` holds `canonical` to the
  first spelling alone.
- **Fails before** (798991a2): hlmailbox 29 of 31: `TestCanonicalAddress: g1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq4rvsrm
  canonical: true`; `TestMainnetPlaceholders: governor g1ecsuj0q572jr0dhu29q9njtnmw03hyu77c5qfc: ""` — jaeCold's
  bech32m spelling taken as the governor on gnoland-1. Passes after.

### 6. Kinds 5, 7 and 8 refuse HlAdmin's line; a swap never names a seat's key; the swap's words (both reviews)

- **HlAdmin's line** (the first review's finding 5, info; the second's 2 and 4). Kind 5's keys, kind 7's beneficiary
  and kind 8's implementation refuse every address decisions go to or went to (`hlAdminLine`): the recipient — a
  successor of HlAdmin once a swap names it — and every former recipient, which stays former until a swap names it
  again, so for good in practice (a key a swap named by mistake too; the realm cannot tell a contract from a key, so
  the refusals say only what it knows: "an address decisions go to, or went to, never holds a seat", "the fees never
  go to …", "… is never code behind a proxy"). The launch HlAdmin is one of the sixteen besides. The line moves with
  the swaps, so all three check when made and in the closure (kind 5 gains a `check`; kind 7's `func() {}` becomes
  one; kind 8's `check` gains the line).
- **The converse** (the second review's finding 3, low). A swap refuses a target that holds an Ethereum seat as last
  set here (`ethSeatOf`, the mirror kind 5 writes): when made, in its closure, and at `Execute` — where a kind 5 that
  seated the target after the swap was decided leaves it pending for GovDAO's cancel. So the line and the seats stay
  apart from either side: a kind 5 naming a pending swap's target lands first and the swap is refused at `Execute`
  while that key holds the seat, or the swap lands first and the kind 5 is refused. A wrong paste (a guardian's key as
  the recipient) is refused, where before it took 7 days and a swap back to undo.
- **The swap's words** (the first review's findings 3 and 4). Its description says whose standing it applies under,
  `(applies only while Y is the recipient)`, where "(applied only while it is the recipient)" could be read as the new
  contract; the builder's doc says the rule reads the recipient as it is, not the swaps since (item 1). Left as the
  brief's rule: binding a swap to the count of swaps applied since it was made (the first review's other fix) would
  also refuse a stale swap after an A → B → A, but it is beyond the brief and changes no outcome GovDAO's cancel does
  not; Q-F9a below.
- **Tests.** `TestHlAdminLineKeys` (new): with keyG the recipient and keyC, keyE, keyF, keyH former, each refused as a
  kind 5 key (alone, and as the second entry after a key of no contract), as kind 7's beneficiary and as kind 8's
  implementation, when made, naming which; a kind 5, a kind 7 and a kind 8 naming keyI, pre-armed beside the swap that
  makes keyI the recipient, refused in their closures once it lands, nothing dispatched or recorded; a swap to keyA,
  which holds seat 1, refused when made; a swap to keyJ decided while keyJ was nobody, refused at `Execute` once a
  kind 5 seated keyJ, pending. The older tests' swaps that named a seat's key now name another
  (TestSwapReplacesItsRecipient's second queued swap keyA → keyK, TestEthereumKinds' description eip2 → eip3); the
  description checks read the new words.
- **Fails before** (300e65ce): hlgov 17 of 21: `TestHlAdminLineKeys: expected an abort containing "hlgov: seat 5's key
  0x6161…61 is the decision recipient (HlAdmin's line): an address decisions go to, or went to, never holds a seat",
  got none`, and the three description checks (`… (applied only while it is the recipient)`); kind 8's line and the
  swap's seat checks, which the test reaches after its first failure, are held by their controls (k-kind8-line-off,
  b-swap-seat-off, b-execute-seat-off). Passes after.

### 7. The siblings — hlmailbox's seats and hlfees' treasurer (beyond the brief; droppable)

- **What.** The same class — a gno address typed in a spelling no caller has — in two more places, folded last in a
  commit of their own (as F6's 5b) so the coordinator can drop it without touching the rest: `parseSeatChanges`
  (hlmailbox/gov.gno) reads a seat's address through `CanonicalAddress` — a seat set in another spelling could never
  flag (`SeatOf` compares strings with the caller's address), and a holder's own address so spelled passed "holds X
  already" and "holds seat N already"; `NewSetTreasurerRequest` (hlfees) reads the treasurer so — one named in another
  spelling could never withdraw (`cur.Previous().Address() != treasurer`), and the treasurer's own address so spelled
  passed "is the treasurer already". Both are GovDAO's (or the governor's) acts. Each reads the typed address through
  `CanonicalAddress` alone — "" refused as no address — so one decoder decides both whether it is an address and its
  spelling (the second review's finding 1: were the native IsValid ever to take what the interpreted round trip
  refuses, a seat change would become a removal, or the treasurer "").
- **Tests.** hlmailbox `TestSeatsCanonical`: a holder's own address in each other spelling refused on its seat and on
  another; two spellings of one new key in one proposal refused as one key named for two seats; a new watch key typed
  with bech32m's checksum in upper case named canonical, seated 7 days on, and flagging (and clearing) GNOT's sends.
  hlfees `TestTreasurerCanonical`: the treasurer in each other spelling is the treasurer already; another typed with
  bech32m's checksum in upper case is named, applied and withdraws canonical.
- **Fails before** (e2b738ba): hlmailbox 31 of 32 (`TestSeatsCanonical: expected an abort containing "hlmailbox: seat
  1 holds g1ecsuj0q572jr0dhu29q9njtnmw03hyu7tyyvv6 already", got none` — the holder in upper case), hlfees 2 of 3
  (`TestTreasurerCanonical: … "hlfees: g1dpkz6arjv4shxatjv4e97h6lta047h6lpj7x3m is the treasurer already", got none`).
  Passes after.

### Q-F6a — left as documented

After an undo, kind 9 still refuses the early successor (it joined `former` when swapped away from), so a re-timed
kind 9 needs a fresh successor; unchanged, as the brief decides, and still held by `TestFormerRecipients` (keyF
refused by kind 9 after the swap back) and F6's control m2-kind9-takes-former.

### Readings where the brief left room

1. **The order `(cur, from, contract)`**, "from … to": a slip is always refused — `from` must be the recipient, and
   the target must differ from it.
2. **Pending, not spent, at `Execute`**: a swap whose `from` was replaced, or whose target a kind 5 seated since, panics
   and stays pending, as hlmailbox's rotations do (spending it would need `Execute` to return without a panic, a new
   path); GovDAO cancels it, and hlwatch lists every pending decision.
3. **`Execute`'s "already the recipient" refusal removed**, not kept as code no input reaches.
4. **The rollback keeps any call and any code hash**: HlAdmin pins the hash; the launch notice records each launch
   implementation's.
5. **Kinds 5 and 7 check the sixteen at creation only** (a compiled list); HlAdmin's line, which moves, also in the
   closure, as kind 8's does.
6. **The swap reads the seats from the mirror** (`ethSeats`, what kind 5 last dispatched), which can sit ahead of
   Ethereum's seats or behind them (seatWarnings): enough to refuse a key the realm itself named to a seat, never a
   proof of Ethereum's seats.
7. **Canonicalize, not refuse**, another spelling in the votes (the brief's word): the canonical address is the one
   credited or paid. F6's guard refuses a non-canonical compiled value instead — a deploy-time rule, where a stage
   step's slip should stop the deploy.
8. **One helper in hlmailbox** (`CanonicalAddress`, exported, a pure function like `ParseEthAddress`) for the five
   places, as the review suggested, rather than one per realm.
9. **The siblings folded**, last and in their own commit.

### Findings and questions (none changes a rule here)

- **F9-1, the plan and BUILD.md** (the coordinator folds the plan): eth §5.3's "r/hlgov's `set decision recipient`
  may name a former recipient again" gains "it names the recipient it replaces and applies only while that is the
  recipient"; "Builders refuse every address of the Ethereum deployment as a kind 8 implementation, a kind 9 successor
  or a new recipient (other than a kind 9's successor named after it)" becomes "… as a kind 8 implementation (but the
  proxy's own launch implementation, a rollback), a kind 9 successor, a new recipient (other than a kind 9's successor
  named after it), a kind 5 key or kind 7's beneficiary — the last two HlAdmin's line too"; §13's kind 5, 7 and 8 lines
  likewise; §5.2's "a credit whose recipient is one of this deployment's realms … goes to the sweep queue" gains "and a
  vote reads a typed gno address in its canonical spelling". BUILD.md's "From F6" bullet is done.
- **F9-2, the stale undo** (Q-F6b's uncovered half): hlwatch should alarm on a queued `set decision recipient` whose
  target has handed over on Ethereum (a kind 9 executed) — the Go lanes'. The description names both addresses.
- **F9-3, the Solana tree** (`realm/`, live on gnoland-1, untouched here; the review's finding 8): the same pattern
  stands there — its routes' credit and sweep votes (`realm/hlgnot/hlgnot.gno` 532, 543; `hlbubble` 467, 477;
  `hlgnot2` 524, 532), its seats (`realm/hlmailbox/gov.gno` 390–399), its treasurer (`realm/hlfees/hlfees.gno`
  172–177), the treasury's refusal of the token's own realms (`realm/bubbletreasury/treasury.gno` 247–258), and the
  BUBBLE token (`realm/bubble`, its p/grc20votes ledger keyed by string: any transfer, mint, admin or minter naming
  another spelling strands the tokens or the role). Its r/hlgov's swap names no `from` either. Deployed realms do not
  change; until successors fold it, GovDAO members and the operator type gno addresses as gno writes them — lower case,
  copied from a page or a wallet — and hlwatch could flag a proposal naming any other spelling. For Jae.
- **F9-4, a rollback's description** (the review's finding 6): it says "a rollback", not what it rolls back; hlwatch
  could name the last kind 8 sent for that proxy beside it.
- **F9-5, the votes' realm check is made at creation only** (the review's finding 7, older than F9): a route registered
  or dropped in the vote's 7 days is not re-checked when the credit or sweep applies; re-checking `unpayable(to)` in
  the closure is one condition a route. For round 3.
- **F9-6, `launchTreasurer`'s spelling** (the second review's finding 5, info): hlfees' compiled treasurer is a staged
  gno address no init guard holds to its canonical spelling (F6's guard covers the governor and the seats; the
  treasurer is out of the dev-value guard on purpose, reading 3 of F6). One line in hlfees' init would do it —
  `CanonicalAddress(launchTreasurer) == launchTreasurer`, on every chain; the stage step writes it canonical today.
- **F9-7, `IsSolanaRealm` and `SeatOf`** (its finding 6, info): exported, comparing strings; every caller passes a
  canonical address today (the votes after `CanonicalAddress`, the flags `caller(cur)`, the seats after the same), and a
  successor route calling them must too — their docs could say so.
- **F9-8, what the tests cannot tell** (its finding 7, info): hlgnot's GNOT payment lands in one account whatever the
  spelling (the banker decodes it), so the votes' descriptions carry the proof there, the BUBBLE payment in hlbubble;
  hlgnot2's drill checks descriptions only; an input over 90 characters is moot (an accepted address is 40).
- **Q-F9a, the swap's ABA** (item 1, the first review's finding 3): bind a swap to a counter of swaps applied, read
  when it is made and required unchanged in the closure and at `Execute`, if a stale swap should never apply again
  after its recipient is restored. Jae's or the coordinator's call.

### The independent reviews (read-only agents; nothing built or run)

**The first**, over the fold's first five commits (items 1–3, and items 4 and 7 as they then lowered the address),
read the four gno-specs documents and checked the diff rule by rule (interrealm v2 §2, §3.2–§3.3, §4.1–§4.4,
§5–§5.3, §6–§10, §11.2–§11.3, §12.1–§12.3; the ADR's `cross(cur)`, `unsafe.*` and borrow rules; the guide's §2, §3
(A)(B)(C), §5.1–§5.8, §6.1, §8; the review method's quick checks 1–11 and its checklist), ran the interrealm checker
(134, clean), built and ran nothing, and verified: `Execute`'s removed check unreachable (one construction site of
`recipientParams`, right after `check`); the rollback's mapping and that it can be aimed nowhere else; kinds 5 and 7
refusing every spelling; every refusal before any write; no caller free text in a description; each claim exercised
by a test that is not vacuous. Its findings, and what became of each: 1 (medium) and 2 (low), bech32m's spelling —
items 4 and 5; 3 (low), the swap's ABA — item 6's doc, Q-F9a; 4 (info), the swap's words — item 6; 5 (info), HlAdmin's
line in kinds 5 and 7 — item 6; 6 (info), the rollback's description — F9-4; 7 (info, older), the votes' check at
creation only — F9-5; 8 (info), the Solana tree — F9-3. Its verdict: "the three r/hlgov changes are correct. The
address fix in changes 4 and 5 is not, and it should be fixed before any upload to gnoland-1" (its 4 and 5: the votes
and the siblings as they then lowered) — fixed by items 4, 5 and 7 as they stand.

**The second**, over the rebuilt commits (items 4–7 as they then stood), checked the same sections and answered six
questions with evidence: `CanonicalAddress` returns the canonical spelling for exactly the strings IsValid takes and
"" for every other (gno's `crypto/bech32` is byte for byte btcutil v1.2.0's `bech32.go` but the error texts; the
90-character cap is moot, an accepted address being 40 characters; its own BIP-173/350 model, checked against both
BIPs' vectors, fuzzed 61,104 inputs without a divergence); every caller-typed gno address in eth/realm is now read in
its canonical spelling (the votes, the seats, the treasurer — every other address compared is stored or a pkgpath);
`hlAdminLine`'s closure re-check is sound; the new export widens nothing (§11.2–§11.3, guide §5.1, §5.6); the tests
fail on their parents and none is vacuous. Its findings, none above low: 1 (low), one decoder for both answers in the
siblings — item 7; 2 (low), kind 8 and HlAdmin's line — item 6; 3 (low), the swap and a seat's key — item 6; 4 (info),
the line's messages and its "for good" — item 6; 5, 6, 7 (info) — F9-6, F9-7, F9-8; 8 (info), item 4's commit message
("named and paid" in hlgnot2's drill, which names only) — corrected in the rebuild — and `gnoaddr_test.gno` citing
this section before it landed — it lands with these notes; 9 (info) — F9-3. Its verdict: "the four commits do what
they claim. No finding is above low, and none blocks the upload." The last round (items 6 and 7 as they stand) is held
by its tests and controls below.

### The gno-specs checks (AGENTS.md) — sections checked, for what F9 added or changed

Read first: `docs/gno-specs/gno-interrealm-v2.md`, `interrealm_v2.adr.md`, `gno-security-guide.md`,
`gno-ai-contract-review.md`; each rule below against F9's diff (`git diff be9d5e92 -- eth/realm`), as the independent
review did over the first five commits.

- **Interrealm v2 §2–§4 (contexts, borrow rules).** The new functions are non-crossing functions of their own realm:
  `launchImplementation` and `hlAdminLine` (r/hlgov), `CanonicalAddress` (hlmailbox). What the routes, hlfees and
  r/hlgov call in hlmailbox (`CanonicalAddress`, `EthContractName`, `EthContractAt`) are non-crossing reads that borrow
  rule #1 runs under hlmailbox, reading no state of it (CanonicalAddress calls p/hyperlane's two top-level functions,
  no receiver, no borrow) and returning values (an `address`, a `string`, a `[32]byte` copy).
- **§3.2 (storage = authority).** The one new state is r/hlgov's: `recipientParams` gains `from`, a `[32]byte` value
  the swap's closure builds and records in r/hlgov's own ledger; `Execute` reads it there. The routes', hlmailbox's
  and hlfees' changes store what they stored before (a draft's address, a seat, the treasurer), now canonical.
  **§3.3**: no `/p/` package touched.
- **§5, §5.1, §5.2 (crossing functions, `cur`).** No crossing function and no secondary `realm` parameter added to realm
  code: `check-interrealm.py` over `eth/realm` counts 134 functions, as before, all clean. The tests' `swapRequest(cur
  realm, from, to)` is a crossing function of the test package called non-crossing in the same realm (§5.1's form 2),
  as `pass` is; it crosses with that `cur` only into this realm's own builder.
- **§5.3 / guide §5.7 (realm values ephemeral).** No realm value stored or captured anew: the closures added capture
  `[32]byte` values, slices of them and `check` functions, as the other closures capture parameters.
- **§8 / §3.2 (conversion guards, construction).** `recipientParams` is built in r/hlgov, its declaring realm; no
  conversion to another realm's type.
- **§9 (panics across boundaries).** Every new refusal panics in the realm's own frame before any write: a builder's
  abort the proposal (in its closure, leaving it for a retry within its 14 days or GovDAO's cancel); `Execute`'s
  refusal of a stale swap leaves it pending, nothing written; a vote's refusal records nothing. The tests catch them
  with `revive`.
- **§10 (method values).** None.
- **§11.3 public API / guide §5.1, §8 / review checks 3–5, 8.** One exported function added,
  `hlmailbox.CanonicalAddress`: a pure function over a value, returning a value (no pointer into state, no interface
  or callback parameter); `NewSetDecisionRecipientRequest` gains a `string` parameter.
- **Guide §5.6, §5.8 / review checks 1, 9 (caller identity).** The new address parameter (`from`) designates the
  recipient a swap replaces and is compared with state; it is no identity — who decides is still `mustBeGovernor` on
  the closure's live `cur`. Canonical spellings make the stored seats and treasurer match a caller's
  `cur.Previous().Address()`, which is how identity is read. No `unsafe.PreviousRealm`.
- **Guide §5.5 / review check 2 (payment guards).** `TransferRemote`, `Claim` and `Withdraw`'s guards unchanged.
- **Review check 10 (Render, descriptions).** The new and changed descriptions write parsed values only: Ethereum
  addresses in EIP-55 case (the swap's two, the rollback's), gno addresses canonical (the votes', the treasurer's, a
  seat's), never the text as typed.
- **Interrealm ADR.** `cross(cur)` unchanged; no `unsafe.CurrentRealm`; `testing.SetRealm` in tests only — set in the
  test function's own frame before a crossing call (a helper closure's override binds to its own frame, as hlgnot2's
  envelope note says; TestHlAdminLineKeys makes its pre-armed proposals inline for that).

### Tests and results (devbox, gno v1.2.0)

Last run: the realms at eb199508, `GNO=/root/.cache/gno-toolchains/v1.2.0/gno GNOTEST_LINT=1 bash
eth/scripts/gnotest.sh` on the devbox, exit 0.

| realm | passed | failed | gno lint | new or changed (F9) |
|---|---|---|---|---|
| r/eth/hlmailbox | 32 | 0 | ok | TestCanonicalAddress, TestSeatsCanonical; TestMainnetPlaceholders' bech32m spellings |
| r/eth/hlverifier | 3 | 0 | ok | |
| r/eth/hlfees | 3 | 0 | ok | TestTreasurerCanonical |
| r/eth/hlgnot | 16 | 0 | ok | TestVotesCanonical |
| r/eth/hlbubble | 13 | 0 | ok | TestVotesCanonical |
| r/eth/hlgov | 21 | 0 | ok | TestSwapReplacesItsRecipient, TestRollback, TestDeploymentKeys, TestHlAdminLineKeys; TestFormerRecipients' pre-armed and undo parts; every swap through swapRequest (TestLaunchValues, TestYield, TestRecipient, TestSuccessorIsNotTheRecipient, TestEthereumKinds, TestDeploymentTargets) |
| r/eth/hlgnot2 | 6 | 0 | ok | TestMigration's drill (the canonical votes) |
| **all** | **94** | **0** | | 85 before F9 |

Each commit's tree ran the whole suite with lint, green: 86 (76d7dd2f), 87 (fbdf051b), 88 (09ea362a), 91 (798991a2),
91 (300e65ce), 92 (e2b738ba), 94 (eb199508). The other gates on eb199508: `check-interrealm.py` over `eth/realm` (M1's
shadow invocation) — "134 function(s) take a realm parameter; every secondary one is either unused or proved with
IsCurrent() first", exit 0; `gofmt -l` nothing; `gno fmt -diff -imports=false eth/realm/...` no diff. The e2e
(`eth/bridge/local/e2e.sh`, `BL=/root/src/eth-f9-e2e-state ANVIL_PORT=8849 GNO_PORT=26861`, `bridge/` synced beside
the tree for the Go build) on eb199508: PASS in 376 s — its kind 7 to the treasurer (no contract of the deployment,
nor HlAdmin's line) delivered and executed on anvil, the kind 12 and its cancel, the unpauses, `hlwatch` GREEN; its
two alarm lines are the flag's pause and its clear. (One run on an intermediate tree failed at its first delivery,
"alice has 0 GNOT on Ethereum": the agents' fixed ports, 9301 and 9303, were held by another lane's run at that minute
— `bind: address already in use` in relayer.log and updater.log — and its rerun on the same tree passed; the agents'
ports are fixed in e2e.sh, so two e2e runs on the devbox at once collide.) `realm/` (the Solana tree) untouched; no
vector file touched.

**Negative controls** (appendix G, `f9mutants.py`, on eb199508): each of the 32 fails at the test that holds its rule
— one realm's suite each, but a-canonical-lower-only, run in all five realms that read a gno address.

| control | the rule taken out (or the old rule put back) | fails |
|---|---|---|
| b-from-off | the swap's `from` checked neither when made nor in the closure | hlgov TestSwapReplacesItsRecipient (a swap from keyC taken), TestFormerRecipients, TestHlAdminLineKeys |
| b-made-check-off | the swap's made-time `check` dropped | hlgov TestRecipient (first: "differ from the current one"), TestSuccessorIsNotTheRecipient, TestSwapReplacesItsRecipient, TestFormerRecipients, TestEthereumKinds, TestDeploymentTargets, TestHlAdminLineKeys |
| b-closure-check-off | the swap's closure `check` dropped | hlgov TestSwapReplacesItsRecipient (the pre-armed swap taken), TestFormerRecipients, TestHlAdminLineKeys |
| b-execute-off | `Execute` applies a swap whose `from` was replaced | hlgov TestSwapReplacesItsRecipient (decision 30 applied against keyC), TestFormerRecipients, TestHlAdminLineKeys |
| b-execute-old-rule | `Execute` refuses only a target that is the recipient (F6's rule) | the same three |
| b-swap-seat-off | the swap takes a seat's key when made and in its closure | hlgov TestHlAdminLineKeys (a swap to keyA, seat 1, taken) |
| b-execute-seat-off | `Execute` applies a swap whose target a kind 5 seated since | hlgov TestHlAdminLineKeys (the swap to keyJ, seat 4, applied) |
| c-rollback-off | every launch implementation refused | hlgov TestRollback |
| c-rollback-shifted | each proxy's rollback mapped to the next proxy's launch implementation | hlgov TestDeploymentTargets, TestRollback |
| c-rollback-any-launch | any proxy's launch implementation taken | hlgov TestDeploymentTargets, TestRollback |
| c-rollback-unsaid | the description silent on the rollback | hlgov TestRollback |
| k-kind5-contracts-off, k-kind7-contracts-off | kind 5's keys, or kind 7's beneficiary, take the sixteen | hlgov TestDeploymentKeys |
| k-kind5-line-off, k-kind7-line-off | kind 5's keys, or kind 7's beneficiary, take HlAdmin's line | hlgov TestHlAdminLineKeys |
| k-kind5-line-closure-off | kind 5's closure no longer reads the line | hlgov TestHlAdminLineKeys (the pre-armed kind 5 taken once keyI is the recipient) |
| k-kind8-line-off | kind 8's implementation takes HlAdmin's line | hlgov TestHlAdminLineKeys |
| k-line-former-off | the line without the former recipients | hlgov TestHlAdminLineKeys |
| k-proposekind-closure-off | no builder's `check` in its closure (kind 7's line among them) | hlgov TestMintLimits, TestSuccessorIsNotTheRecipient, TestFormerRecipients, TestHlAdminLineKeys |
| a-canonical-lower-only | `CanonicalAddress` lowers only (the fold's first rule) | hlmailbox TestCanonicalAddress (bech32m read as itself), TestSeatsCanonical, TestMainnetPlaceholders; hlfees TestTreasurerCanonical; hlgnot, hlbubble TestVotesCanonical and hlgnot2 TestMigration (the credit vote took g1qqqq…4rvsrm) |
| g-guard-old | init's guard back to F6's `canonical` | hlmailbox TestCanonicalAddress, TestMainnetPlaceholders |
| s-seats-canonical-off, s-treasurer-canonical-off | the siblings back to IsValid alone, the typed spelling kept | hlmailbox TestSeatsCanonical; hlfees TestTreasurerCanonical |
| v-ROUTE-credit-old (×3) | the credit vote's old rule (IsValid, the string compared) | hlgnot, hlbubble TestVotesCanonical; hlgnot2 TestMigration (the credit vote took the zero address in upper case) |
| v-ROUTE-credit-typed (×3) | the credit vote compares the canonical address but drafts the typed one | the same (the draft names the owner in upper case) |
| v-ROUTE-sweep-old (×3) | the sweep vote's old rule | the same (the sweep vote took the zero address in upper case) |

### Commands

```sh
# on the Mac: sync the worktree to the devbox (add --include '/bridge/***' for the e2e's Go build)
rsync -az --delete -e 'ssh -i ~/.ssh/id_ed25519_kourt' --exclude .git --include '/eth/***' --include '/realm/***' \
  --include '/scripts/***' --include '/docs/***' --include '/AGENTS.md' --exclude '*' \
  ~/src/jaekwon/bubblerumble-eth-f9/ root@<devbox>:/root/src/eth-f9/
# the suite (on the devbox, in /root/src/eth-f9)
GNO=/root/.cache/gno-toolchains/v1.2.0/gno GNOTEST_LINT=1 bash eth/scripts/gnotest.sh
# the interrealm checker over eth/realm (M1's shadow), the formatters
S=$(mktemp -d); mkdir $S/scripts; cp scripts/check-interrealm.py scripts/gnosource.py $S/scripts/
ln -s $PWD/eth/realm $S/realm; python3 $S/scripts/check-interrealm.py; rm -rf $S
/usr/local/go/bin/gofmt -l eth/realm/*/*.gno; /root/.cache/gno-toolchains/v1.2.0/gno fmt -diff -imports=false eth/realm/...
# oldcode: commit C's tests over its parent P's realm code (on the Mac, then the devbox), leaving out a test file whose
# functions P has not got (798991a2's gnoaddr_test.gno)
git archive P eth/realm | tar -x -C $D; find $D/eth/realm -name '*_test.gno' -delete
git archive C eth/realm | tar -x -C $T; for f in $T/eth/realm/*/*_test.gno; do cp $f $D/eth/realm/$(basename $(dirname $f))/; done
# … and for 76d7dd2f over be9d5e92, the swap helper back to the old signature (from dropped):
sed -i 's|\treturn NewSetDecisionRecipientRequest(cross(cur), from, to)|\t_ = from\n\treturn NewSetDecisionRecipientRequest(cross(cur), to)|' $D/eth/realm/hlgov/hlgov_test.gno
ETH_REALM_DIR=$D eth/scripts/gnotest.sh <realm>      # $D synced to the devbox
# the negative controls (appendix G)
python3 f9mutants.py /root/src/eth-f9 <out dir> [name …]
# the e2e (no other e2e on the devbox at the same time: the agents' ports are fixed)
BL=/root/src/eth-f9-e2e-state ANVIL_PORT=8849 GNO_PORT=26861 sh eth/bridge/local/e2e.sh
# the independent bech32m spellings (appendix H)
python3 -I b32.py <gno address> …
```

## Appendix G — F9's controls (`f9mutants.py`)

Run on the devbox: `python3 f9mutants.py /root/src/eth-f9 <out dir> [name …]`; scratch copies under `<out
dir>/scratch`. A mutant prints, for each realm it runs, the failed and aborted tests and the first failure's line.

```python
#!/usr/bin/env python3
"""Negative controls for eth F9 (eth/bridge/M1-NOTES.md, "Fold F9"). A mutant takes one F9 rule back out of a scratch
copy of eth/realm (or puts the old rule back) and runs the named realms' suites there (eth/scripts/gnotest.sh with
ETH_REALM_DIR); it must fail at the test that holds the rule.
Usage: f9mutants.py <tree root> <out dir> [name ...]"""
import os, re, shutil, subprocess, sys

ROOT, OUT = sys.argv[1], sys.argv[2]
ONLY = set(sys.argv[3:])
REALMS = os.path.join(ROOT, "eth", "realm")
GOV = "hlgov/hlgov.gno"

# ---- mutants: name, realms to test (comma separated), file, old (exact, once), new — or tuples of olds and news
MUTANTS = [
    # Q-F6b: the swap names the recipient it replaces
    ("b-from-off", "hlgov", GOV, "\t\tif f != recipient {\n", "\t\tif false && f != recipient {\n"),
    ("b-made-check-off", "hlgov", GOV,
     '\tcheck()\n\ttext := "set decision recipient to "', '\ttext := "set decision recipient to "'),
    ("b-closure-check-off", "hlgov", GOV,
     "\t\tmustBeLive()\n\t\tcheck()\n\t\td := ledger.Record(\"set decision recipient\"", "\t\tmustBeLive()\n\t\td := ledger.Record(\"set decision recipient\""),
    ("b-execute-off", "hlgov", GOV, "\tif p.from != recipient {\n", "\tif false && p.from != recipient {\n"),
    ("b-swap-seat-off", "hlgov", GOV, "\t\tif n := ethSeatOf(p); n != 0 {\n", "\t\tif n := ethSeatOf(p); false && n != 0 {\n"),
    ("b-execute-seat-off", "hlgov", GOV, "\tif n := ethSeatOf(p.to); n != 0 {\n", "\tif n := ethSeatOf(p.to); false && n != 0 {\n"),
    ("b-execute-old-rule", "hlgov", GOV,
     "\tif p.from != recipient {\n\t\tpanic(\"hlgov: decision \" + itoa(id) + \" replaces \"",
     "\tif p.to == recipient {\n\t\tpanic(\"hlgov: decision \" + itoa(id) + \" replaces \""),
    # Q-F6c: kind 8's rollback to the proxy's own launch implementation
    ("c-rollback-off", "hlgov", GOV, "\trollback := impl == launchImplementation(p)\n", "\trollback := false\n"),
    ("c-rollback-shifted", "hlgov", GOV, "\t\t\treturn ethContract(hlmailbox.EthMailboxImpl + i)\n",
     "\t\t\treturn ethContract(hlmailbox.EthMailboxImpl + (i+1)%len(proxies))\n"),
    ("c-rollback-any-launch", "hlgov", GOV, "\trollback := impl == launchImplementation(p)\n",
     "\trollback := strings.HasSuffix(hlmailbox.EthContractName(impl), \"'s implementation\")\n"),
    ("c-rollback-unsaid", "hlgov", GOV, '\t\ttext += " (its launch implementation: a rollback)"\n', ""),
    # kinds 5 and 7 refuse the deployment's sixteen
    ("k-kind5-contracts-off", "hlgov", GOV, "\t\tif name := hlmailbox.EthContractName(k); name != \"\" {\n\t\t\tpanic(\"hlgov: seat \"",
     "\t\tif name := hlmailbox.EthContractName(k); false && name != \"\" {\n\t\t\tpanic(\"hlgov: seat \""),
    ("k-kind7-contracts-off", "hlgov", GOV, "\tif name := hlmailbox.EthContractName(k); name != \"\" {\n\t\tpanic(\"hlgov: the beneficiary \"",
     "\tif name := hlmailbox.EthContractName(k); false && name != \"\" {\n\t\tpanic(\"hlgov: the beneficiary \""),
    # kinds 5 and 7 refuse HlAdmin's line (the review's finding 5), when made and in the closure
    ("k-kind5-line-off", "hlgov", GOV, "\t\t\tif why := hlAdminLine(k); why != \"\" {\n\t\t\t\tpanic(\"hlgov: seat \"",
     "\t\t\tif why := hlAdminLine(k); false && why != \"\" {\n\t\t\t\tpanic(\"hlgov: seat \""),
    ("k-kind5-line-closure-off", "hlgov", GOV, "\t\tmustBeLive()\n\t\tcheck()\n\t\tn := dispatch(cur, KindGuardians", "\t\tmustBeLive()\n\t\tn := dispatch(cur, KindGuardians"),
    ("k-kind7-line-off", "hlgov", GOV, "\t\tif why := hlAdminLine(k); why != \"\" {\n\t\t\tpanic(\"hlgov: the beneficiary \"",
     "\t\tif why := hlAdminLine(k); false && why != \"\" {\n\t\t\tpanic(\"hlgov: the beneficiary \""),
    ("k-kind8-line-off", "hlgov", GOV, "\t\tif why := hlAdminLine(impl); why != \"\" {\n", "\t\tif why := hlAdminLine(impl); false && why != \"\" {\n"),
    ("k-line-former-off", "hlgov", GOV, "\tif isFormer(k) {\n\t\treturn \"a former decision recipient (HlAdmin's line)\"", "\tif false && isFormer(k) {\n\t\treturn \"a former decision recipient (HlAdmin's line)\""),
    ("k-proposekind-closure-off", "hlgov", GOV, "\t\tmustBeLive()\n\t\tcheck()\n\t\tn := dispatch(cur, kind, params, text, t)", "\t\tmustBeLive()\n\t\tn := dispatch(cur, kind, params, text, t)"),
    # a gno address's canonical spelling: hlmailbox.CanonicalAddress and its users (the review's findings 1, 2)
    # (two edits: the import and the body) — F9's first rule, lowering alone, which leaves bech32m's spelling
    ("a-canonical-lower-only", "hlmailbox,hlfees,hlgnot,hlbubble,hlgnot2", "hlmailbox/gnoaddr.gno",
     ('import hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"\n',
      "\tb, err := hl.AddressToBytes32(a)\n\tif err != nil {\n\t\treturn \"\"\n\t}\n\tc, err := hl.Bytes32ToAddress(b)\n\tif err != nil {\n\t\treturn \"\"\n\t}\n\treturn c\n"),
     ('import (\n\t"strings"\n\n\thl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"\n)\n',
      "\tif _, err := hl.AddressToBytes32(a); err != nil {\n\t\treturn \"\"\n\t}\n\treturn address(strings.ToLower(string(a)))\n")),
    ("g-guard-old", "hlmailbox", "hlmailbox/mainnet.gno", "func canonical(a address) bool { return a != \"\" && CanonicalAddress(a) == a }",
     "func canonical(a address) bool { return a.IsValid() && string(a) == strings.ToLower(string(a)) }"),
    ("s-seats-canonical-off", "hlmailbox", "hlmailbox/gov.gno", "\t\t\tif a = CanonicalAddress(a); a == \"\" {\n", "\t\t\tif !a.IsValid() {\n"),
    ("s-treasurer-canonical-off", "hlfees", "hlfees/hlfees.gno", "\tif to = hlmailbox.CanonicalAddress(to); to == \"\" {\n", "\tif !to.IsValid() {\n"),
] + [m for r in ("hlgnot", "hlbubble", "hlgnot2") for m in (
    # the routes' votes read the recipient in its canonical spelling
    ("v-%s-credit-old" % r, r, "%s/%s.gno" % (r, r),
     "\tif to = hlmailbox.CanonicalAddress(to); to == \"\" || unpayable(to) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftCredit(" % r,
     "\tif !to.IsValid() || unpayable(to) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftCredit(" % r),
    ("v-%s-credit-typed" % r, r, "%s/%s.gno" % (r, r),
     "\tif to = hlmailbox.CanonicalAddress(to); to == \"\" || unpayable(to) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftCredit(" % r,
     "\tif c := hlmailbox.CanonicalAddress(to); c == \"\" || unpayable(c) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftCredit(" % r),
    ("v-%s-sweep-old" % r, r, "%s/%s.gno" % (r, r),
     "\tif to = hlmailbox.CanonicalAddress(to); to == \"\" || unpayable(to) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftSweep(" % r,
     "\tif !to.IsValid() || unpayable(to) {\n\t\tpanic(\"%s: the recipient must be an address outside the bridge's realms\")\n\t}\n\treturn propose(cur, st.DraftSweep(" % r),
)]

SCRATCH = os.path.join(OUT, "scratch")

def run_mutant(name, realms, path, old, new):
    d = os.path.join(SCRATCH, name)
    shutil.rmtree(d, ignore_errors=True)
    shutil.copytree(REALMS, d)
    p = os.path.join(d, path)
    s = open(p, encoding="utf-8").read()
    olds, news = (old, new) if isinstance(old, tuple) else ((old,), (new,))
    for o, n in zip(olds, news):
        if s.count(o) != 1:
            return "DID NOT APPLY (%d matches of %r)" % (s.count(o), o[:60])
        s = s.replace(o, n)
    open(p, "w", encoding="utf-8").write(s)
    lines = []
    for rl in realms.split(","):
        logs = os.path.join(OUT, name)
        os.makedirs(logs, exist_ok=True)
        env = dict(os.environ, ETH_REALM_DIR=d, GNOTEST_LOGS=logs)
        r = subprocess.run([os.path.join(ROOT, "eth", "scripts", "gnotest.sh"), rl], cwd=ROOT, env=env, capture_output=True, text=True)
        head = r.stdout.splitlines()[0] if r.stdout else r.stderr[:300]
        log = open(os.path.join(logs, rl + ".log"), encoding="utf-8", errors="replace").read()
        fails = re.findall(r"^--- FAIL: (\w+)", log, re.M)
        panics = re.findall(r"^=== RUN   (\w+)\n(?:(?!=== RUN).*\n)*?panic: (.{0,100})", log, re.M)
        first = re.search(r"^=== RUN   \w+\n((?:(?!=== RUN|--- ).*\n)*?)--- FAIL", log, re.M)
        why = first.group(1).strip().splitlines()[0][:150] if first and first.group(1).strip() else ""
        lines.append("%s | failed: %s | aborted: %s | first: %s" % (head, ",".join(fails) or "-", ";".join("%s (%s)" % p for p in panics) or "-", why))
    shutil.rmtree(d, ignore_errors=True)
    return ("\n" + " " * 28).join(lines)

for m in MUTANTS:
    if not ONLY or m[0] in ONLY:
        print("%-27s %s" % (m[0], run_mutant(*m)), flush=True)
```

## Appendix H — the bech32m spellings, independently (`b32.py`)

BIP-173's and BIP-350's reference encoder (sipa's `segwit_addr.py`, reduced), checked against BIP-350's own vectors
first: it writes a gno address's twenty bytes with either checksum. `python3 -I b32.py` printed `vector … bech32m` for
the four vectors, `zero bech32  g1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqluuxe`, `zero bech32m
g1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq4rvsrm`, and for hlverifier and hlgnot (the dev namespace) `…8snntyv → …890r8pw` and
`…5pylevmj → …5p3rfq7s` — the review's figures; gno's `bech32.EncodeM` (the tests' `spellings`) gives the zero
address's the same, which `TestCanonicalAddress` pins.

```python
# BIP-173 / BIP-350 reference (sipa's segwit_addr.py, reduced): spell a gno address's twenty bytes with either checksum
import sys, hashlib
CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
BECH32, BECH32M = 1, 0x2bc830a3
def polymod(values):
    gen = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3]
    chk = 1
    for v in values:
        b = chk >> 25
        chk = (chk & 0x1ffffff) << 5 ^ v
        for i in range(5):
            chk ^= gen[i] if ((b >> i) & 1) else 0
    return chk
def hrp_expand(h): return [ord(x) >> 5 for x in h] + [0] + [ord(x) & 31 for x in h]
def create_checksum(h, data, const):
    pm = polymod(hrp_expand(h) + data + [0] * 6) ^ const
    return [(pm >> 5 * (5 - i)) & 31 for i in range(6)]
def verify(h, data):
    c = polymod(hrp_expand(h) + data)
    return {BECH32: "bech32", BECH32M: "bech32m"}.get(c)
def encode(h, data, const): return h + "1" + "".join(CHARSET[d] for d in data + create_checksum(h, data, const))
def decode(s):
    s = s.lower(); pos = s.rfind("1"); h = s[:pos]; data = [CHARSET.find(x) for x in s[pos+1:]]
    return h, data[:-6], verify(h, data)
def bits(data, frm, to, pad=True):
    acc = 0; nb = 0; out = []
    for v in data:
        acc = (acc << frm) | v; nb += frm
        while nb >= to:
            nb -= to; out.append((acc >> nb) & ((1 << to) - 1))
    if pad and nb: out.append((acc << (to - nb)) & ((1 << to) - 1))
    return out
# BIP-350's own test vectors first: a valid bech32m string must verify as bech32m
for v in ["A1LQFN3A", "a1lqfn3a", "abcdef1l7aum6echk45nj3s0wdvt2fg8x9yrzpqzd3ryx", "?1v759aa"]:
    print("vector", v, decode(v)[2])
zero = [0] * 20
z5 = bits(zero, 8, 5)
print("zero bech32 ", encode("g", z5, BECH32))
print("zero bech32m", encode("g", z5, BECH32M))
for a in sys.argv[1:]:
    h, d, kind = decode(a)
    print(a, kind, "→ bech32m", encode(h, d, BECH32M))
```

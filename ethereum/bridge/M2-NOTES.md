# E-M2b — the Solidity contracts: notes

Lane E-M2b of BUILD.md: `GnoLightClient`, `PausableGate`, `HlAdmin` (with `HlAdminExec`), the tm2 libraries, the
guardians library, the setup of PLAN.md §5.8 (`script/Setup.s.sol`), the vector generator `tools/evmvectors`, and the
Foundry suite. This file is the record: interface changes first (lane E-M3 reads them), then the shape of what was
built, every reading where the plan left room, every deviation from the Rust and why, the gas, and the last devbox
run. Branch `eth-m2b`, 2026-10-14; `bridge-eth` (E-M2a's real `Ed25519.sol`) merged in the same day.

## 1. Interface changes (src/interfaces/*.sol and what the Go machines call) — E-M3, read this first

**Since the E-M2a merge** (lane E-M2a's verifier in; the adversarial review's findings 1–3 fixed, §3.14):

A. **`GnoLightClient(bytes32 mailboxPkgPathHash, IEd25519Verifier ed25519)`**: the implementation's constructor takes
   the address of lane E-M2a's `Ed25519Verifier` (`src/Ed25519Verifier.sol`: no storage, no owner, no constructor
   arguments, never behind a proxy; 7,007 bytes) and keeps it as the immutable `ED25519`; an address without code is
   refused (`NoVerifier`). Every precommit's signature is `ED25519.verify(pubkey, signBytes, r, s)`, an external
   view call, with the key's 32 bytes as the set carries them. Inline the library would put the client at about
   27,000 bytes, past EIP-170 (M2a-NOTES §1). The relayer carries a precommit only with a 64-byte signature, split
   `r ‖ s`, and leaves any other out (§3.15).
B. **`IGnoLightClient.ED25519() returns (IEd25519Verifier)`** (an `address` in the ABI) added. Replacing the verifier
   is an upgrade of the client (kind 8); it adds no authority.
C. **The deploy's step 4 deploys `Ed25519Verifier` before the client implementation** unless the setup names one
   already on the chain (`BridgeSetup.Config.ed25519`, the JSON key `ed25519`, optional: one verifier serves every
   deployment). A fresh deployer's CREATE nonces are now: 0 ProxyAdmin, 1–2 Mailbox and proxy, 3 MerkleTreeHook,
   4 ProtocolFee, **5 Ed25519Verifier**, 6–7 the client and proxy, 8–9 token 0, 10–11 token 1, 12–13 the gate, 14
   HlAdmin (HlAdminExec is HlAdmin's CREATE at its nonce 1). `BridgeSetup.Config.clientImplCode` is gone (the tests
   no longer substitute a client); `BridgeSetup.Deployment.ed25519` and `BridgeSetup.ed25519CodeHash()` (keccak256 of
   this tree's `Ed25519Verifier` runtime code) are new.
D. **`HlAdmin.Config` gains** `ed25519` (the verifier), `ed25519CodeHash` (its EXTCODEHASH: the setup passes this
   tree's build), `localDomain` (Ethereum's Hyperlane domain), `maxPerMessage[2]` and `dayCap[2]` (the gate's launch
   mint limits), in the struct's order: `mailbox, client, gate, token0, token1, proxyAdmin, merkleTreeHook,
   protocolFee, ed25519, ed25519CodeHash, localDomain, origin, hlgov, route0, route1, mailboxPkgPathHash, seats[4],
   watch[2], maxPerMessage[2], dayCap[2], startSequence, predecessor`. The constructor now refuses a zero `ed25519`,
   `route0` or `route1` (`ZeroAddress`) and a zero `ed25519CodeHash`, `localDomain` or `origin` (`ZeroSetting`, new).
   A successor's limits are not read. **`IHlAdmin` gains `ed25519()`, `ed25519CodeHash()`, `localDomain()`.**
E. **`finishSetup` has 31 checks** (§2.5): 27 the client's `ED25519()` is HlAdmin's `ed25519`; 28 that address holds
   code, not an EIP-7702 designator, whose EXTCODEHASH is HlAdmin's `ed25519CodeHash`; 29 the mailbox's and both
   tokens' `localDomain()` are HlAdmin's; at a launch only (no predecessor): 30 each token's mint limits at the gate
   are HlAdmin's launch ones, 31 the ProtocolFee's `protocolFee()` is 0. `hleth audit` prints `ed25519CodeHash()`
   beside this tree's build of `Ed25519Verifier` and `ed25519()` beside the deployment's record.
F. **Kind 8 checks the authority after the upgrade**: the proxy's admin still the ProxyAdmin (`getProxyAdmin`) and its
   owner (the client: its `admin()`) still HlAdmin, else the execution reverts with `AuthorityLost(address proxy)` (a
   file-level error in `admin/HlAdminStorage.sol`) and the decision pends for a cancel.
G. **The deployment is 11 codes + 5 proxies = 16 contracts**: ProxyAdmin, Mailbox, MerkleTreeHook, ProtocolFee,
   Ed25519Verifier, GnoLightClient, HypERC20 × 2, PausableGate, HlAdmin, HlAdminExec; proxies for the mailbox, the
   client, the two tokens and the gate. PLAN.md §0 and §5.8 still count nine codes and fourteen deployments: E-M1's
   refused-recipient list and E-M3's audit must include `Ed25519Verifier` (`client.ED25519()`) and `HlAdminExec`
   (`HlAdmin.exec()`).

**Before the merge:**

1. **`IGnoLightClient.stageKey(submitter, headerHash, partsTotal, partsHash, round, epoch, setHash)`** added (pure):
   a staged tally lives under `keccak256(abi.encode(submitter, headerHash, partsTotal, partsHash, round, epoch,
   setHash))`, where `epoch` is the client's at the call and `setHash` the set the header must be signed by (for
   `update`: the announced next set's hash while one stands, else the current set's; for `proveHeader`: the current
   set's). `stagePower(bytes32 key)`'s parameter was renamed from `stageKey` (it shadowed the function).
2. **`IGnoLightClient.update`'s `nextSet` is read only by the call that crosses 2/3** (the finishing call): staging
   calls may pass it empty; the finishing call of an announcing header must carry it whole.
3. **`IHlAdmin.route(uint8 i) returns (bytes32)`** added: the route realm (32 bytes, the gno address left-padded) each
   token's router for gno.land must be; `finishSetup` checks it. It comes from HlAdmin's constructor.
4. **Constructors and initializers** (not in the interfaces; what `hleth setup` deploys and calls, in
   `script/Setup.s.sol`'s order):
   - `GnoLightClient(bytes32 mailboxPkgPathHash, IEd25519Verifier ed25519)` (A) — the implementation (its
     constructor disables initializers); the proxy is constructed with `initialize(string chainId, uint32
     originDomain, int64 trustedHeight, bytes32 trustedHash, Validator[] validators, address admin)`.
   - `PausableGate()` — the implementation (initializers disabled); the proxy with `initialize(address owner, address
     mailbox, address client, address[2] tokens, address[4] seats, address[2] watch, uint64[2] maxPerMessage,
     uint64[2] dayCap)`.
   - `HlAdmin(HlAdmin.Config)` (its fields: D). **Its constructor creates `HlAdminExec`** (§2.4): with the verifier
     the deployment is 16 contracts (G); HlAdminExec's address is `HlAdmin.exec()` (CREATE from HlAdmin, nonce 1),
     and the audit compares its code too.
   - `ProtocolFee(maxProtocolFee = 0.001 ether, 0, beneficiary, deployer)` (the red team's ER11): HlAdmin compiles
     0.001 ETH as kind 7's bound and `finishSetup` requires the ProtocolFee's `MAX_PROTOCOL_FEE` to equal it.
5. **The record** (PLAN.md §6.2), which the watch rebuilds from `HeaderRecorded` events: `runningHash =
   keccak256(abi.encodePacked(previous, headerHash, uint64(height), uint64(block.number)))`, `records` counting every
   header accepted (`proven = false`) or proved (`true`). `initialize` records the trusted header (records 1) and
   emits `SetAnnounced(trustedHeight, setHash, 0)`, `SetChanged(0, setHash)` and `HeaderRecorded(trustedHash,
   trustedHeight, 0, 1, runningHash, false)`; it makes no entry for it (its results hash is unknown: `proveHeader`
   brings it, as on Solana).
6. **`GnoLightClient.verify` is `view`** (the interface says "writes nothing"); it reverts with a custom error on every
   refusal and returns true.
7. **HlAdmin's events**: `DecisionRecorded(n, kind, matureAt)` — `matureAt` is 0 for a decision spent at recording,
   the block time for a cancel (it executes at once), `deliveredTime + delay(kind)` otherwise (kind 5's floor is
   applied at execution, not here). `DecisionSpent(n, kind, why)` with `why` one of: `"unknown kind"`, `"too long"`
   (a body over 512 bytes), `"params"` (parameters that do not parse or fail a pure check), `"reserved"` (kind 4),
   `"hold"` (an unpause under a hold on its object), `"current key"` (a kind 5 naming any current key), `"codehash"`
   (kind 8/9: no code, an EIP-7702 designator, another code hash, or the successor is HlAdmin itself), `"successor"`
   (kind 9: predecessor, inherited, successor or clock factor not right), `"retired"` (executing on a retired
   HlAdmin). `DecisionCancelled(n)` for a pending decision deleted by a list cancel or closed inside a stored range;
   `RangeCancelled(from, to)` for a stored range (clamped).
8. **The deliverer** HlAdmin records is `tx.origin` (the relayer's key): informational, nothing is refunded on
   Ethereum.
9. **Errors** are custom errors (selectors the Go side decodes): the client's `ChainId, HeightNotAboveTrusted,
   HeightAboveTrusted, ValidatorsHash, WrongSet, NextSetMissing, NextSetUnexpected, NextValidatorsHash, BadCommit,
   SlotOutOfRange, SlotsUnsorted, SignatureMismatch, UnknownHeader, NoResultsHash, ProofMismatch, DispatchMismatch,
   OriginMismatch, BadMessage, NotAdmin, ZeroAdmin, NoVerifier` and the libraries' `TmHeader.BadHeader, TmValset.BadValidatorSet,
   TmMerkle.BadProof, TmAmino.Encoding, TmResults.{BadLeaf, NoDispatch, BadDispatch}`; the gate's `NotTheMailbox,
   Quarantine, Paused, BadMessage, BadTokenMessage, OverMax, OverDayCap, BadToken, ZeroAddress, NoRenounce`;
   `Guardians.{UnknownKey, DuplicateKey, BadSeat, TooManyGuardians, TooManyWatch, CurrentKey, Hold}`; HlAdmin's
   `NotTheMailbox, Value, NotInherited, RetiredAdmin, WrongOrigin, WrongSender, ShortBody, OutOfOrder, WrongDecision,
   NotMature, Deferred, WrongAuthority, SetupAlreadyDone, ZeroAddress, ZeroSetting` and the file-level
   `SetupIncomplete(uint8 check)` (§2.5's table), `NotDelegated` and `AuthorityLost(address proxy)` (F).
10. **Kind 8's body** is `proxy 32 ‖ implementation 32 ‖ codehash 32 ‖ call` (the red team's ER7a): `call` the rest of
    the body, possibly empty (≤ 407 bytes inside the 512-byte body); empty → `ProxyAdmin.upgrade`, non-empty →
    `ProxyAdmin.upgradeAndCall(proxy, implementation, call)`.
11. **The clock factor is a compile-time constant chosen by the Foundry profile**: the default profile is the mainnet
    build (1); `FOUNDRY_PROFILE=factor8` maps `bridge-clock/` to `src/clock/factor8/ClockFactor.sol` (8). The client,
    the gate, HlAdmin and HlAdminExec all read it; `clockFactor()` answers it; the code hash pins it (no immutable).

## 2. What was built

1. **The tm2 libraries** (`src/lib/`): `TmAmino` (varints with Rust's exact limits, keys, fields, in-place reading
   of a memory buffer), `TmMerkle` (RFC 6962 with sha256, `SimpleProof` walked as merkle.rs does, MAX_AUNTS 100),
   `TmHeader` (the sixteen leaves → hash; the six fields read from their canonical leaves), `TmVote` (CanonicalVote
   sign bytes, a per-commit template so only the time is built per precommit), `TmValset` (the leaf, the set hash,
   valset.rs's checks), `TmResults` (results.rs's `dispatch_events` decision, the Dispatch match), `Guardians`
   (guardians.rs: seats, flags, the hold, the dip, unpause, replacement — one code for the gate and HlAdmin).
2. **`GnoLightClient`** (behind a TransparentUpgradeableProxy): `update` and `proveHeader` with staging, `verify` by
   an accepted header's entry under the current epoch, the record, the quarantine, the set history, `setAdmin`.
   Signature checks go through the one private `_verifySignature(pubkey, signBytes, r, s)`, which calls lane E-M2a's
   `Ed25519Verifier` at the implementation's immutable `ED25519` with the key's 32 bytes as the set carries them
   (never decoded, cached or re-encoded: ER2, correction 4) and the signature as two 32-byte halves (64 bytes by
   construction, §3.15). Nothing overrides it: the test-only subclasses are gone (§7).
3. **`PausableGate`** (behind a proxy, Ownable): `verify` only from the mailbox, the client's quarantine read through
   `quarantineEnd()` only (the Ethereum reading of Solana's layout pin), any flag pauses, the two tokens' mint limits,
   then the client's `verify`; flags, holds, unpause, replacement, mint limits; `renounceOwnership` refused.
4. **`HlAdmin` + `HlAdminExec`**: HlAdmin whole is past EIP-170's 24,576 bytes (31.6 KB at 10,000 runs, 26.3 KB even
   at 1 run), so the kinds' execution and `finishSetup`'s checks live in `HlAdminExec`, which HlAdmin's constructor
   creates with HlAdmin's own settings and calls only by `delegatecall` (it runs as HlAdmin: HlAdmin's storage,
   HlAdmin's authority); called at its own address every function refuses (`NotDelegated`). Both halves keep HlAdmin's
   state at one ERC-7201 namespace (`gno.bridge.HlAdmin`, slot `0xa549…fa00`); HlAdmin is never upgraded — the
   namespace serves the two halves. A revert inside HlAdminExec reverts `execute` with the same reason.
5. **`finishSetup`'s checks**, each refusing with `SetupIncomplete(n)` at the first that fails: 1 the ProxyAdmin's
   owner, 2 the mailbox's, 3 a token's, 4 the gate's, 5 the MerkleTreeHook's, 6 the ProtocolFee's (each HlAdmin), 7 the
   client's admin, 8 each of the five proxies' admin (the ProxyAdmin; a proxy that no longer answers it counts as
   failing), 9–11 the mailbox's default ISM (the gate), default hook (the MerkleTreeHook), required hook (the
   ProtocolFee), 12–15 each token's ISM (the gate), hook (none), fee recipient and fee hook (none), router for
   gno.land (the compiled route), 16 the ProtocolFee's `MAX_PROTOCOL_FEE` (0.001 ETH), 17 the gate's mailbox, client
   and tokens, 18 the gate's guardian list (HlAdmin's), 19 the client's and the gate's clock factors (this build's), 20
   the client's origin domain (HlAdmin's); and what lives in immutables, which a code-hash audit with immutables masked
   cannot see (the red team's ER1): 21 each token's `mailbox()` (ours: a token over another mailbox could be minted by
   it, past the gate and the client), 22 its scale (1/1), 23 its decimals (6), 24 its supply (0), 25 the
   MerkleTreeHook's `mailbox()`, 26 the client's `mailboxPkgPathHash()` (HlAdmin's), 27 the client's `ED25519()`
   (HlAdmin's `ed25519`), 28 the verifier's code (present, no EIP-7702 designator, EXTCODEHASH HlAdmin's
   `ed25519CodeHash`), 29 the mailbox's and both tokens' `localDomain()` (HlAdmin's); and at a launch (no
   predecessor), what the deployer wrote into storage that no code hash pins (the review's finding 1; on Solana the
   limits were compiled constants): 30 each token's mint limits at the gate (HlAdmin's launch ones), 31 the
   ProtocolFee's fee (0). A successor's setup skips 30–31: kinds 7 and 12 may have moved them.
6. **`script/Setup.s.sol`**: `BridgeSetup.deploy` = `contracts` (steps 1–6, every proxy initialized in its own
   construction), `admin` (step 7), `settings` (8–9), `ownership` (10, the ProxyAdmin last), `finishSetup` (11); the
   `Setup` script broadcasts it from `DEPLOYER_KEY` with the settings in the JSON file `BRIDGE_SETUP_CONFIG` names.
7. **`tools/evmvectors`** (package `main` of module `bubblerumble/eth/bridge`, over `gnolight` and `hl`): writes
   `contracts/test/vectors/fixtures.json` (gnoland-1's 17 blocks: leaves, hash, commit with each precommit's slot,
   time, r, s, sign bytes and Go's `crypto/ed25519` verdict, the set and its hash, the results leaves with SimpleProofs
   where the next block is a fixture — 487,881, 495,705, 701,854, 720,331, 740,913 — and every real signature with
   four mutations each and Go's verdict, 430 triples, the §13 example rebuilt) and `synthetic.json` (a "gno-test-1"
   chain from 100 to 111 through sets A 4, B 6, C 20, D 60 validators by deterministic seeds, three announcements, a
   commit of round 2, a block of two parts, seven real Hyperlane messages to the Solidity suite's deployment — whose
   addresses are the deployer's CREATE addresses (nonces 9, 11, 14 since the verifier took nonce 5), computed there
   and checked by the suite — in leaves beside a grc20 event, a forged header 101, a nil and an other-block
   precommit, 60 mutation triples). `go run ./tools/evmvectors
   -fixtures ../../bridge/programs/fixtures -out contracts/test/vectors` from `eth/bridge` regenerates them
   byte-identically. go.mod gained only the indirect requires `hl` needs (btcutil, x/crypto, x/sys).
8. **Upgrade safety** (correction 2): the client and the gate keep a linear, append-only storage layout with a
   `uint256[40] __gap` at the end (storage gaps, not ERC-7201); both implementation constructors call
   `_disableInitializers()` (Hyperlane's implementations do not; ours do); every proxy is initialized in its
   construction; kind 8 reverts an upgrade that leaves a proxy outside HlAdmin's authority (§1.F).
9. **The E-M2a merge** (`bridge-eth` at c9e01fe5 into `eth-m2b`): `src/lib/Ed25519.sol`, `src/lib/Sha512.sol`,
   `src/Ed25519Verifier.sol`, `test/Ed25519*.t.sol` and `test/vectors/ed25519/` came in unchanged; E-M2b's
   `foundry.toml` was kept (M2a-NOTES §1 asks it; it holds every setting E-M2a's tests need, `fs_permissions` read on
   `./` covering `test/vectors/ed25519`). The legacy pipeline stays (`via_ir = false`): via-IR at 10,000 runs inlines
   the verifier to 49,405 bytes. The client calls the verifier contract (§1.A), the setup deploys it (§1.C) and
   HlAdmin checks it (§1.E).

## 3. Readings where the plan left room

1. **Header leaves.** The client takes the sixteen leaves and reads six fields from their canonical forms only: the
   chain id (the leaf compared whole by keccak with `0A len <chain id>`), the height (`08 zigzag`, positive), the time
   (`[08 uvarint(sec)] [10 uvarint(ns)]`, nothing else), ValidatorsHash and NextValidatorsHash (`0A 20 <32>`),
   LastResultsHash (empty, or `0A 20 <32>`). Any other form of these six is `BadHeader`. The other ten are hashed and
   never read: a header the client uses is one more than 2/3 of the power signed, so its leaves are the chain's; the
   Rust `Header::check` bounds on unread fields (lengths, num_txs ≥ 0) bounded Solana accounts.
2. **The trusted header at init** is (height, hash, set), as the Rust init; `trustedTime` is 0 until the first update
   (the Rust `time_secs: 0`). `trustedTime` is the header time's seconds as the leaf carries them (a u64).
3. **Entries.** `entry(hash) = {epoch, height, lastResultsHash}` for every header accepted or proved; `verify` requires
   `height ≠ 0`, `epoch == epoch()` (`UnknownHeader` otherwise — a header of a rotated-out set, or one never
   recorded) and a non-zero results hash. The epoch rises at the update that makes an announced set current, before
   that update's own entry is written (Solana's ring cleared before the push).
4. **Staging** (PLAN.md §6.3; the red team's ER4; correction 3): a call verifies the precommits it carries; one that
   does not cross 2/3 stores `{verified bitmap, power}` under the stage key (§1.1) and returns false; the call that
   crosses deletes the stage and finishes. Precommits carry strictly increasing slots (`SlotsUnsorted`); a slot an
   earlier call of the same stage verified is skipped, never counted twice (Rust's `stage_verify` set the bit
   idempotently). Every call — the finishing one included — re-checks the header against the live state first (chain
   id, height above or at/below the trusted one, ValidatorsHash against the live expected set hash, the set in calldata
   against it). A set change moves the epoch and the expected set hash, orphaning every stage begun before it: no
   tally is ever carried across one (tested by X → Y → X). A header recorded under the current epoch never reaches a
   tally again (an update needs a height above the trusted one; `proveHeader` returns true before the tally). **Who
   clears an abandoned stage: nobody.** It holds no funds and pays no rent, nothing reads it but its own submitter's
   next call with the same key, and no rule needs it gone.
5. **No 8 KB leaf bound** on Ethereum: the Rust `MAX_LEAF` was the stage account's size; calldata and gas bound a leaf
   here. `MAX_AUNTS` (100) is kept. **No `HistoryFull`**: the set history is unbounded storage (the Rust five-entry bound
   was the account's).
6. **`setAdmin` refuses the zero address** (the Rust accepted any key): an Ethereum guard against losing the role;
   HlAdmin only ever names a successor.
7. **The gate** keeps the mailbox, the client and the tokens in storage, set by `initialize` (PLAN.md §5.8 step 6
   "initialized with"), never changed; the quarantine refuses while `quarantineEnd() > block.timestamp` (at its end
   exactly a delivery passes, as Rust's `quarantine_end > now`); the token charge happens before the client's
   `verify` and is undone with any later revert; a message to any other recipient passes unmetered.
8. **HlAdmin's body bound** is on the whole body: over 512 bytes it is recorded and spent at recording (r/hlgov never
   sends one). A list cancel deletes the named pending decisions at once (Ethereum knows which pend; Solana needed the
   accounts passed); a range cancel is clamped at the last number and stored — merged, 64 at most, a full store merging
   with the nearest — only while something pends, and every pending decision inside it is closed as cancelled by
   `execute`; at zero pending every range is pruned. On a retired HlAdmin `execute` spends a pending decision at once
   (`"retired"`): nothing there can run (the Rust closed those by `close_cancelled`). Kind 6 with a zero router
   unenrolls the domain, or does nothing when none is enrolled (Hyperlane's EVM `unenrollRemoteRouter` reverts on an
   absent domain; the sealevel token's `None` removed silently). Kind 7's bound is the compiled 0.001 ETH, a pure check
   (ER7b), and the beneficiary must be non-zero. `handle` refuses value.
9. **ER7b — only pure checks spend.** Every external read whose failure could be starved by a stranger's gas (EIP-150's
   63/64) is a plain call that reverts the whole `execute`: kind 9 reads the successor's `predecessor`, `inherited`,
   `successor` and `clockFactor` by plain calls; kind 2 object 0 reads the gate's `hold()` by a plain call; kind 7's
   maximum is compiled. Tested: a successor that does not answer, or whose `successorInit` reverts, and an upgrade whose
   call reverts, all leave the decision pending.
10. **ER3** — kinds 8 and 9 spend a target whose code is empty or begins `0xef0100` (an EIP-7702 delegation designator:
    its EXTCODEHASH covers only the pointer), and keep the EXTCODEHASH == pinned codehash check at execution.
11. **Guardians' moments** are (block number, block time); block number 0 means "none" (Ethereum's blocks start at 1).
    A flag's block is the one an unpause compares (`< beforeBlock`, the decision's delivery block).
12. **The deliverer** is `tx.origin` (§1.8). **`moduleType()`** is 0 on the client and the gate (the gate answers the
    client's).
13. **Checkpoints** (correction 5): nothing here signs or checks a checkpoint; the signers' index is the message's
    nonce (E-M3's).
14. **The adversarial review** (a read-only agent over `src/` and the setup against the Rust, before the merge): no
    finding above low; no way found to get a header accepted or a message proven without more than 2/3 of the current
    set's power, nor a stage carried where it should not be, nor a sign-bytes difference from the Rust, nor a storage
    or delegatecall hazard between HlAdmin and HlAdminExec, nor a stranger spending a decision by starving its gas.
    Its four lows:
    1. *finishSetup never read the gate's launch mint limits or the starting protocol fee* — values the deployer
       writes into storage, which no code hash pins (on Solana the limits were compiled constants). **Fixed**:
       checks 30 and 31, at a launch only.
    2. *Zero settings passed finishSetup* (a zero route compared equal to an unenrolled router; `origin` 0), *and the
       mailbox's `localDomain` was never read* (an immutable the masked audit cannot see): all fail closed, but
       `setupDone` said otherwise. **Fixed**: the constructor refuses them (`ZeroAddress`, `ZeroSetting`), check 29.
    3. *Kind 8 never checked HlAdmin still held the proxy after an upgrade*: an approved implementation on another
       storage layout (OpenZeppelin 5's namespaced slots, a shifted base) reads its owner as zero and its
       initializer as never run, so anyone could `initialize` it; or an `upgradeAndCall` call overwrites the
       EIP-1967 admin slot. **Fixed**: `AuthorityLost` (§1.F), a revert, so the decision pends for a cancel (only pure
       checks spend, §3.9).
    4. *A matured kind 8 or 9 whose target has no code yet is spent by any caller* ("codehash"), where the Rust
       leaves it pending (`NotStaged`, `WrongAccount`): a stranger can burn a 7-day decision whose code is deployed
       late. **Not changed**: the coordinator's ER3 says kinds 8/9 are spent when the target's code is empty; the
       review's alternative (revert while the code is empty, spend a designator or another hash, and spend a
       decision pinning 0 or keccak256("") so an empty target can never match) is as safe and only changes
       liveness. Open for the coordinator (§9).
15. **A signature is 64 bytes by construction**: a precommit carries `r` and `s` as two `bytes32`, so no other length
    reaches the verifier (M2a-NOTES §1: the caller refuses any other length; tm2's `VerifyBytes` refuses it before
    `Verify`). A precommit whose signature on gno.land is not 64 bytes never counted there and cannot be carried here;
    the relayer leaves it out, as it does a nil one.
16. **The verifier's code is checked against a hash HlAdmin was given** (`ed25519CodeHash`, which the setup takes from
    this tree's build), not one compiled into HlAdmin: the verifier's bytecode carries the compiler's metadata, which
    records every remapping and so differs between the clock profiles (factor 1 and factor 8 builds of the same
    source hash differently), and HlAdmin's own code is trusted only through the audit's comparison with the build
    anyway. Check 28 makes the live verifier the one HlAdmin names; `hleth audit` compares that name and hash with
    the build.

## 4. Deviations from the Rust, and why

- **UTF-8**: strings in a results leaf are compared as bytes and never validated as UTF-8 (the Go twin `gnolight`'s
  reading); the Rust refused a leaf any of whose strings was not UTF-8. Only more leaves are accepted, and only where
  the invalid string is not part of the matched Dispatch (whose values are ASCII by their own rules).
- **No ring**: Ethereum keeps an entry per header (§3.3) and an epoch instead of the ring and its clears; no ring
  proofs, no `Grow`, no stage accounts, no `StageClose`, no ring-epoch copies.
- **Precommits by explicit slot** (strictly increasing) instead of a positional slot array; a nil or other-block
  precommit is left out by the submitter, and one carried anyway fails its signature (the client builds the BlockID's
  sign bytes) — tested with real ed25519 signatures of both.
- **The gate binds a charge to a delivery by `msg.sender == mailbox`** (PLAN.md §0), not the Instructions sysvar.
- **The record** is keccak over (previous, hash, height, block number), with events (PLAN.md §6.2).
- **HlAdmin** splits into HlAdmin + HlAdminExec (§2.4); cancels, the retired path, kind 6's zero router, kinds 7–9 and
  `finishSetup` read as §3.8–§3.10 and §2.5 say.
- **Errors** are Solidity custom errors, not Solana's numbered codes.

## 5. Size (forge build --sizes, solc 0.8.33, cancun, 10,000 runs, via-IR off)

| Contract | Runtime | Margin | Initcode |
|---|---|---|---|
| GnoLightClient (calls `Ed25519Verifier`) | 20,667 | 3,909 | 21,081 |
| Ed25519Verifier (E-M2a's, unchanged) | 7,007 | 17,569 | 7,035 |
| HlAdmin | 17,708 | 6,868 | 44,587 (HlAdminExec's initcode inside; 4,565 under 49,152) |
| HlAdminExec | 21,784 | 2,792 | 23,329 |
| PausableGate | 13,193 | 11,383 | 13,417 |

The client grew 211 bytes from the placeholder build (20,456): the call to the verifier and its address replace the
inline placeholder. Inline, E-M2a's library would have added 6,648 bytes (about 27,100: past EIP-170). HlAdminExec's
margin is the one to watch: the five new checks and kind 8's authority check took 2,258 bytes of it.

## 6. Gas (`forge test --match-contract GasReport --hardfork osaka -vv`, mainnet's execution rules since Fusaka;
bytecode cancun)

Each call is measured as a fresh transaction meets the state: every account it may touch and all their slots cold
(`vm.cool`), the call's own gas read on the callee's side (`vm.lastFrameGas`: the whole frame, before refunds) — so
cold SLOADs (2,100), the implementation's and the verifier's first touch (2,600 each) are in, and the test's own
argument encoding is out. "exec" is that; "tx" adds 21,000 and the calldata (EIP-2028, or EIP-7623's floor of 10 a
token where it binds). Every signature is real ed25519 through E-M2a's `Ed25519Verifier`. Sepolia runs Glamsterdam
(SSTORE from zero ≈110.9k): none of these is Sepolia's. (The pre-merge figures were measured warm from the caller's
side; every figure below is the new method's.)

**An update in one transaction** (equal powers; k the signatures that cross 2/3; "without signatures" is the same
update through a verifier that accepts everything — the client's own share, a call per signature included; "per
signature" is the difference over k, the verification itself, ≈221,000 as M2a-NOTES §5 measured for 110-byte sign
bytes):

| validators n | k | exec | tx | fits 16,777,216 | exec without signatures | per signature |
|---|---|---|---|---|---|---|
| 4 | 3 | 924,401 | 966,125 | yes | 261,503 | 220,966 |
| 6 | 5 | 1,399,394 | 1,445,334 | yes | 295,509 | 220,777 |
| 20 | 14 | 3,578,299 | 3,646,855 | yes | 484,767 | 220,966 |
| 28 | 19 | 4,787,479 | 4,868,663 | yes | 590,481 | 220,894 |
| 40 | 27 | 6,721,497 | 6,822,321 | yes | 755,087 | 220,978 |
| 60 | 41 | 10,101,948 | 10,236,808 | yes | 1,040,130 | 221,019 |
| 100 | 67 | 16,395,668 | 16,595,220 | yes (182k spare) | 1,593,091 | 220,933 |

So every set gnoland-1 has had or is likely to have updates in one transaction: 6 validators 1.45M gas, 20 → 3.65M,
28 → 4.87M, 40 → 6.82M, 60 → 10.24M, up to about 100 at equal powers (a set whose 2/3 needs more than ~67 signatures
stages). **Past one transaction** (PLAN.md §6.3's stage; each call pays the set's hashing again):

| update | call | precommits | exec | tx |
|---|---|---|---|---|
| 40 validators | 1 (stored) | 0–13 | 3,653,778 | 3,735,218 |
| | 2 (finishes) | 14–26 | 3,462,129 | 3,542,161 |
| 100 validators | 1 (stored) | 0–59 | 14,724,021 | 14,913,093 |
| | 2 (finishes) | 60–66 | 2,410,070 | 2,520,222 |
| 255 validators (the bound) | 1 (stored) | 0–59 | 15,652,554 | 15,942,482 |
| | 2 (stored) | 60–119 | 15,619,175 | 15,909,319 |
| | 3 (finishes) | 120–170 | 13,608,517 | 13,885,245 |

| What | exec | tx |
|---|---|---|
| proveHeader at 4 validators (3 signatures) | 872,902 | 913,658 |
| verify, a 451-byte leaf with 1 Dispatch | 138,909 | |
| verify, 1,804 bytes, 4 Dispatches | 419,534 | |
| verify, 7,227 bytes, 16 Dispatches | 1,545,394 | |
| verify, 8,924 bytes: 1 Dispatch beside 8 KB of another realm's events | 284,069 | |
| Mailbox.process → gate → client → HypERC20 mint, a fresh holder | 279,446 | 311,246 |
| the same to a holder again | 245,309 | 277,121 |
| HypERC20.handle alone (a fresh holder) | 64,762 | |
| process + HlAdmin.handle recording a kind 7 | 305,971 | 337,655 |
| process + HlAdmin.handle, a cancel deleting one | 227,466 | 258,946 |
| HypERC20.transferRemote (a burn dispatched) | 186,022 | 207,542 |
| execute kind 2 (the gate) / kind 2 (HlAdmin) | 91,985 / 66,221 | 113,189 / 87,425 |
| execute kind 3 / 5 (one slot) / 6 / 7 | 92,295 / 152,228 / 158,438 / 106,648 | 113,499 / 173,432 / 179,642 / 127,852 |
| execute kind 8 (the gate, with the authority check) / 12 / 4 (spent) | 106,093 / 83,406 / 61,214 | 127,297 / 104,610 / 82,418 |
| execute kind 9 (eight authorities moved, successorInit) | 410,992 | 432,196 |
| the Ed25519Verifier alone (a deployment of its own) | 1,595,769 | |
| the deployment's eleven steps (exec; each step its own transaction) | 36,335,758 (steps 1–6 with the verifier: 26,438,829; 7: 8,877,264; 8–10: 747,295; 11, 31 checks: 272,370) | |

Every Dispatch of the mailbox in a leaf is read whole (Rust's rule: a malformed one refuses the leaf), so each message
proven from a leaf pays ≈94,000 gas for every other Dispatch in it: the relayer's `Reemit` batching (PLAN.md §5.7)
trades one header update against that. The reader's hot paths (hex, decimals, one-byte varints and fields) are in
assembly, each fuzzed against the plain byte-at-a-time reader (ReaderFuzz.t.sol).

## 7. The tests (`contracts/test`)

| Suite | Tests | What |
|---|---|---|
| TmLibs.t.sol | 16 | the 110-byte example; every fixture's header hash and fields, all 86 sign bytes, set hashes and results proofs equal Go's; the reader's refusals; amino; merkle; header and set refusals |
| GnoLightClient.t.sol | 14 | gnoland-1 through its three set changes; the synthetic chain through 4, 6, 20 and 60 validators; the record; a forged header and nil/other-block precommits; Go-refused triples through the client (the vectors' own one-bit-of-s and one-bit-of-r triples over the client's sign bytes, and s + L); height, set, chain, next-set and commit-shape refusals; the round; staging across calls and submitters; proveHeader; the admin; an implementation over no verifier refused; the verifier agreeing with Go on all 490 triples of the vectors (392 refused) |
| GnoLightClientStaging.t.sol | 4 | a stage never outlives X → Y → X; never finishes a recorded header; a precommit of another key; 255 validators (171 signatures staged over five calls, each inside the cap) and 256 refused |
| PausableGate.t.sol | 5 | pausable.rs and limits.rs |
| Delivery.t.sol | 6 | the synthetic chain's messages through the real Mailbox; a flag and a quarantine stop token deliveries, never decisions; the day's bucket; a burn |
| HlAdmin.t.sol | 21 | admin.rs: the sequence, refusals, unknown kinds and long bodies, the sequence's end, kinds 2–7 and 12 on their clocks and holds, kind 5 through a lone flag, its floor and the hold history, cancels, flags, a fresh deployment's start number |
| HlAdminSetup.t.sol | 16 | finishSetup on the launch shape, each of its 31 checks failing alone (27: a client over another verifier; 28: a verifier of other code that the client and HlAdmin agree on; 29: a mailbox at another domain; 30: other limits; 31: a fee), and a verifier already on the chain taken as it is |
| HlAdminHandover.t.sol | 12 | kind 8 for each proxy, with a call, its refusals, a reverting call pending, an upgrade that loses the authority (another layout, another client admin, an admin slot overwritten) pending; kind 9 whole, its refusals, a reverting successor pending, a successor's setup past kinds 7 and 12; a successor before it inherits; the constructor's zero refusals; HlAdminExec only as HlAdmin |
| GuardiansModel.t.sol | 1 fuzz | the library against a transcription of guardians.rs, 512 runs × 48 steps |
| HlAdminInvariant.t.sol | 2 invariants | the last number only rises; pending equals the stored decisions (128 runs × 64 calls) |
| ReaderFuzz.t.sol | 5 | the reader's assembly hot paths against the plain readers |
| Setup.t.sol | 3 | the setup whole (the verifier deployed, the client calling it, HlAdmin's hash its code's); a mint; the script's settings file read whole |
| Gas.t.sol | 7 | §6's measurements |
| E-M2a's Ed25519.t.sol, Ed25519Vectors.t.sol, Ed25519Sha512.t.sol, Ed25519Differential.t.sol | 7, 5, 3, 17 | E-M2a's gates (M2a-NOTES §4), run under this lane's foundry.toml; the 17 differential tests skip without the generated set and pass with it (§8) |

**Every signature is real ed25519 checked by E-M2a's verifier.** The vector suites carry Go's signatures; the
Solidity test chain (`test/helpers/TestChain.sol`) makes real keys from seeds (`vm.publicKeyEd25519`) and signs with
`vm.signEd25519` (RFC 8032, the empty namespace: the plain message), its secrets kept in a key ring written and read
by cheatcodes only (`vm.store`, `vm.load`), so building a commit never takes a pending `vm.prank`. The oracle client
(Go's verdicts by lookup), the keccak stand-in scheme and the client subclasses that overrode the signature check are
gone; `_verifySignature` is private and nothing overrides it. One stand-in remains, in tests only, as a verifier
passed to the real client's constructor: `TrustingVerifier` (accepts everything), for §6's "without signatures" column
(Gas.t.sol) and as check 28's foreign code (HlAdminSetup.t.sol).

## 8. The last devbox run

On `root@<devbox>:/root/src/eth-m2b/eth/bridge/contracts`, Foundry 1.8.5, solc 0.8.33, after the E-M2a merge:

- `forge fmt --check` — clean.
- `forge build --sizes` — every contract under 24,576 bytes (§5).
- `forge test` — **20 suites, 143 tests: 126 passed, 0 failed, 17 skipped** (E-M2a's differential lanes and manifest,
  which skip without the generated set); fuzz 512 runs, invariants 128 × 64.
- `FOUNDRY_PROFILE=factor8 forge test` — the same 126 passed, 17 skipped, on the devnet/Sepolia build.
- E-M2a's gate 4 under this lane's foundry.toml: `go run ./tools/ed25519vectors diff -out
  contracts/test/vectors/ed25519/diff` (from `eth/bridge`; manifest sha256 `c13f8ae9…cf62`, E-M2a's) then
  `forge test --match-contract Ed25519Differential -vv` — **17 passed**: 118,392 records in 16 lanes, Go accepts 21,570,
  0 disagreements.
- `forge test --match-contract GasReport --hardfork osaka -vv` — §6.
- `forge snapshot --no-match-contract Ed25519Differential` — `contracts/.gas-snapshot` (126 tests).
- `go vet ./tools/evmvectors` and `go run ./tools/evmvectors -fixtures ../../bridge/programs/fixtures -out
  contracts/test/vectors` in `eth/bridge` — `fixtures.json` unchanged, `synthetic.json` regenerated for the shifted
  nonces (its messages' recipients), the same bytes on a second run.

## 9. Open

- **The review's finding 4** (§3.14): kinds 8 and 9 spend a decision whose target has no code yet, as ER3 says; the
  Rust left it pending. The coordinator's call; the change is small either way.
- **The counts in PLAN.md** §0 and §5.8 (nine codes, fourteen deployments) are now 11 + 5 = 16 (§1.G): E-M1's
  refused-recipient list and E-M3's audit must include `Ed25519Verifier` and `HlAdminExec`.
- **E-M3's `hleth setup`** deploys `Ed25519Verifier` at step 4 before the client implementation (or reuses one) and
  fills `HlAdmin.Config`'s new fields (§1.D); **`hleth audit`** compares the verifier's runtime code with this tree's
  build (no immutables to mask), `client.ED25519()` with `HlAdmin.ed25519()`, and `HlAdmin.ed25519CodeHash()` with the
  build's hash.
- Every Dispatch in a leaf costs every message proven from that leaf ≈94,000 gas (§6): the relayer's `Reemit` batching
  is the trade.
- The audit's byte-for-byte comparison of Hyperlane's contracts against `lib/hyperlane-core/dist/buildArtifact.json`
  is E-M3's `hleth audit`; this suite compiles Hyperlane's sources unchanged with the release's settings.

## Fold F2 — a kind 8 or 9 whose code is not deployed yet pends (2026-10-14)

Lane F2, branch `eth-f2` from `bridge-eth` at `f5fb68f9` (plan e3): commits `57672b6c` (the tests, failing) and
`5253eb42` (the change), then this section and the regenerated `.gas-snapshot`. It resolves §3.14's finding 4 and §9's
first open item. Where this section and §1.7, §1.9, §3.10, §3.14, §5, §6, §7, §8 or §9 differ, this section holds.

### F2.1 The change

`HlAdminExec._codeIs(target, codehash)` (src/admin/HlAdminExec.sol:374) decides kind 8's implementation and kind 9's
successor:
- **no code** (EXTCODESIZE 0) → the execution reverts with `NoCodeYet(address target)` (a new file-level error in
  src/admin/HlAdminStorage.sol, selector `0x9a3c68b5`), so the decision stays pending and anyone retries it once the
  code is deployed. This is the Solana rule: a kind 8 whose buffer is not yet staged pends (`NotStaged`), and so
  does a kind 9 whose successor program is not executable (`WrongAccount`, processor.rs). A stranger can no longer
  spend GovDAO's 7-day decision by executing it before the code is there;
- code that begins `0xef0100` (an EIP-7702 delegation designator) → spent, `"codehash"`, as before, even when the
  decision pinned the designator's own hash;
- code whose EXTCODEHASH is not the decision's → spent, `"codehash"`, as before.

The decision's own checks still come first: kind 8's proxy must be one of the five (`"params"`) before its
implementation's code is read, and kind 9's successor must parse and must not be HlAdmin itself before its code is.
Because the size is checked before the hash, an empty target never matches whatever the decision pins: an account
that never existed has EXTCODEHASH 0 and one that exists without code has keccak256 of nothing, and either pends.

**Safety is unchanged.** No upgrade or hand-over executes now that could not before: an empty target never executed,
and every non-empty target ends exactly as before. Code that an earlier transaction left at an address never changes
(EIP-6780) except an EIP-7702 account's, whose designator is spent. So once code is there, the outcome is fixed:
executed if the hash matches, spent otherwise. Only someone who can put code at the named address can now end such a
decision early, by deploying other code or setting a designator. That is the address's deployer (the CREATE key, or
anyone who can run the same CREATE2 factory, salt and initcode) or, for an account, its key.

**What the change costs.** A decision that waits for its code ends only when the code appears or when GovDAO cancels
it. Under the old rule the executor spent it at maturity, which worked as an expiry, though a stranger could trigger it
too. Under the new rule, whoever can deploy at the address decides when, or whether, it runs. Behind a public CREATE2
factory with public initcode, that is anyone, at any later time, so a stale kind 8 could roll a proxy back to an older
version. Each waiting decision also keeps a storage record and keeps stored cancel ranges from being pruned (64 at
most). See F2.5, item 7.

**Also changed:** kind 9 with a successor address whose top 12 bytes are not zero is now spent as `"params"` (it does
not parse, §1.7's meaning), where it was `"codehash"`. Kind 8 already said `"params"`. HlAdmin's header comment names
the pending case. Nothing else in `src/` changed.

### F2.2 Interface changes for E-M3 (these replace §1.7 and §1.9 where they differ)

- **`NoCodeYet(address target)`**, `0x9a3c68b5`. `execute(n)` reverts with it for a matured kind 8 whose
  implementation, or kind 9 whose successor, has no code; the decision pends. Treat it like `NotMature`:
  `eth_estimateGas` reverts with it, so send nothing and retry later. `hlwatch` and `hleth decision status` should print
  "waiting for code at <address> since <maturity>" and say who can deploy there (F2.5, item 7).
- **`DecisionSpent` reasons:** `"codehash"` now means, for kinds 8 and 9, an EIP-7702 designator, another code hash,
  or the successor is HlAdmin itself. It no longer covers "no code". `"params"` now also covers kind 9's successor
  with non-zero top 12 bytes.

### F2.3 The proof: fails before, passes after

Three new tests (test/HlAdminHandover.t.sol):
- `test_kind8_anImplementationNotYetDeployedPends`: a kind 8 for the gate names the next CREATE address of a deployer
  that has not deployed yet, pinning GateV2's hash, with `initializeV2(42)` as the call. After 7 days a stranger
  executes: it must revert `NoCodeYet(impl)`, the decision must still pend, and nothing is upgraded. A week later the
  deployer deploys GateV2 at that address (asserted), and the decision then executes with the call run.
- `test_kind9_aSuccessorNotYetDeployedPends`: the same for a real successor HlAdmin. Its code hash depends on its
  address, because its immutables name the HlAdminExec it creates, so the test computes the hash as a proposer would:
  it deploys in a `vm.snapshotState`, reads the hash and reverts the snapshot. The stranger's execute must revert
  `NoCodeYet(at)` with nothing moved. After the deploy, the hand-over runs and the successor inherits.
- `test_kind8_anAccountThatDelegatesLaterIsSpent`: an account without code pends a kind 8 (`NoCodeYet`). After its key
  delegates it (`vm.etch` of `0xef0100 ‖ impl`, whose EXTCODEHASH equals the pinned keccak256 of the designator), the
  decision is spent `"codehash"`.

At `57672b6c` (these tests, and `NoCodeYet` declared but unused, so the contracts behave as at `f5fb68f9`), `forge
test --hardfork osaka --match-contract HlAdminHandoverTest` gave **12 passed, 3 failed**, and the whole suite 126
passed, 3 failed, 17 skipped (from a clean `git archive` of that commit). All three failed with "next call did not
revert as expected". The `-vvvv` trace shows the stranger's `HlAdmin::execute(1)` →
`HlAdminExec::run(8, …)` returning `"codehash"` and `emit DecisionSpent(number: 1, kind: 8, why: "codehash")`, and the
same for kind 9. That is the stranger spending the decision.

At `5253eb42` the same command gave **15 passed**. The two refusal tests were changed to the new rule:
- `test_kind8_refusals`: the cases "an account that never existed" (pinned 0) and "a funded account without code"
  (pinned keccak256(""), its EXTCODEHASH asserted) now pend with `NoCodeYet`. A non-proxy naming an implementation
  without code is still spent `"params"`, because the decision's own checks come first.
- `test_kind9_refusals`: an account without code now pends, and a successor with high bytes is spent `"params"`.

### F2.4 The re-check: PLAN.md e3 §6.5 and §13 against HlAdmin and HlAdminExec

§6.5, clause by clause:
1. *A mailbox recipient whose ISM is the raw client, fixed in its code*: matches (`interchainSecurityModule()` returns
   the immutable `client`).
2. *Only gno.land's domain, only r/hlgov*: matches. `handle` reverts for a caller other than the mailbox
   (`NotTheMailbox`), another origin (`WrongOrigin`) or another sender (`WrongSender`); the message stays deliverable.
3. *Records a decision whose 9-byte prefix parses at last + 1, whatever its kind; an unknown kind is recorded and
   spent*: matches. Under 9 bytes reverts (`ShortBody`). Kinds 0, 10, 11 and 13–255 are recorded and spent at
   recording (`"unknown kind"`), as is a body over 512 bytes (`"too long"`).
4. *Ignores one at or below last; refuses one above last + 1, which stays deliverable*: matches (`DecisionIgnored`;
   `OutOfOrder`).
5. *`cancel` executes at recording*: matches. It never pends; one that does not parse is spent (`"params"`). The
   parse is Solana's kinds.rs byte for byte: count 1–16, channel 0, from ≤ to, nothing after.
6. *`unpause` 24 hours after delivery, spent under a hold on its object*: matches. The delay is a day at the clock
   factor, never deferred. Object 0 reads the gate's `hold()` by a plain call; object 1 reads HlAdmin's own hold, dip
   included. Either standing → `"hold"`. It clears flags whose block is before the delivery block.
7. *Every other kind waits 7 days, deferred by any flag on HlAdmin and by a hold*: matches. `_sevenDay` is 3–9 and 12;
   `Deferred` while HlAdmin's hold stands or any of its six flags is up. The gate's flags never defer a decision.
8. *Kind 5 lands through a lone flag, and through a hold on the later of its own 7th day and the 21st day of the
   earliest hold overlapping its pending life (eight holds kept, a ninth merging the two oldest)*: matches
   (`_replacementFloor`, `noteHoldEnd`, `MAX_HOLDS` 8). The floor binds only while a hold stands.
9. *Anyone executes a matured decision*: matches.
10. *Only HlAdmin's own checks spend; a failing external call reverts and leaves the decision pending (ER7)*: matches,
    kind by kind. Every external call in `run` is a plain call (there is no try/catch), and the delegatecall to
    HlAdminExec bubbles any revert, which also undoes `execute`'s early `_close`. Three spends rest on an external
    read, and none can be forced by starving gas: kind 2 object 0's `"hold"` (a starved `hold()` reverts), kind 9's
    `"successor"` (four plain calls), and kinds 8/9's `"codehash"` (EXTCODESIZE, EXTCODECOPY and EXTCODEHASH are
    opcodes, not calls). Since F2, no code pends.
11. *A pending decision is a storage record (kind, parameters, delivery block and time, deliverer), deleted when it
    ends*: matches (`HlAdminStorage.Pending`, `_close`).
12. *`handle` refuses value*: matches (`Value`).
13. *Flags carry the block they were last set at, and an unpause clears those set before its delivery block*:
    matches. `Guardians.setFlag` rewrites the block on every set; `unpause` clears `flagBlock < deliveredBlock`.
14. *No buffer, no extension, no refund key*: matches.
15. *Kind 8 is a proxy upgrade with an optional call, and reverts (pending) when afterwards the proxy's admin is not
    the ProxyAdmin or the contract's owner is not HlAdmin*: matches. An empty call uses `upgrade`, otherwise
    `upgradeAndCall`; then `getProxyAdmin(proxy) == proxyAdmin` and `owner()` (for the client, `admin()`) == HlAdmin,
    else `AuthorityLost(proxy)`. The ProxyAdmin's `admin()` query to a proxy whose admin slot was overwritten falls
    through to the implementation, which either reverts or does not answer the ProxyAdmin (the client answers
    HlAdmin), so the check catches it either way.
16. *A kind 8's implementation and a kind 9's successor are spent when their code is empty or is a designator; their
    code hash must equal the decision's; a target with no code yet leaves the decision pending*: the code now does
    what the parenthetical says (F2.1). The main clause's "empty or" contradicts it: plan fix 1.
17. *Kind 9 moves the ProxyAdmin's, the mailbox's, both tokens', the gate's and the hooks' ownership and the client's
    admin, then writes the live number, seats, watch slots and hold history by `successorInit` (only the predecessor,
    once); afterwards HlAdmin records nothing*: matches. That is seven `transferOwnership` calls and `setAdmin`, then
    `successorInit(lastSeq, keys, holds)` (`WrongAuthority` for another caller, `NotInherited` a second time). A
    retired HlAdmin's `handle` reverts `RetiredAdmin`, and `execute` spends what pended (`"retired"`). The successor's
    origin, r/hlgov address and contracts are immutables, pinned by the decision's code hash.

§13, line by line:
- *Body `kind u8 ‖ seq u64 BE ‖ params`, big-endian; an address with non-zero top 12 bytes is spent*: matches for
  every Ethereum address HlAdmin reads: kind 3's ISM, kind 5's keys, kind 7's beneficiary and kind 8's proxy and
  implementation give `"params"`, and since F2 so does kind 9's successor. Kind 6's router is a remote router, taken
  as 32 bytes and never read as an Ethereum address: plan fix 5.
- *1 `cancel`*: matches (§6.5, item 5).
- *2 `unpause`*: matches; any object other than 0 or 1 → `"params"`.
- *3 `set token ISM`*: matches. Token 0 or 1 and ISM the gate or zero; anything else → `"params"`.
- *4 reserved*: matches. It pends 7 days (deferred like the group), then is spent `"reserved"`.
- *5 `set guardians and watch keys`*: matches. Count 1–4 at 33 bytes each. Naming any current key of any seat or slot
  → `"current key"`. `Guardians.checkReplace` sends to `"params"` a zero key, a seat outside 1–6 or named twice, more
  than two guardian seats, or a key in two places. Both lists are replaced (the gate's by `setGuardians`).
- *6 `enroll router`*: the contract enrolls any router for any domain; restricting gno.land's domain to the token's
  own live route is r/hlgov's builder's job, as on Solana. **What it does with a zero router:** `_enrollRouter` calls
  `unenrollRemoteRouter(domain)` when `routers(domain)` is non-zero and does nothing otherwise, and in both cases the
  decision is executed, never spent. **It cannot be reached.** HlAdmin takes decisions only from r/hlgov (mailbox,
  origin and sender checked). r/hlgov's only kind 6 builder, `NewEnrollRouterRequest`, panics on a zero router or a
  zero domain ("the router and the domain must be set"). Its only dispatch paths are its builders and `Redispatch` of
  a body it built. Plan wording: fix 6.
- *7 `set protocol fee`*: matches. The body is 40 bytes; the fee is at most the compiled 0.001 ETH, which
  `finishSetup`'s check 16 binds to the ProtocolFee's maximum; the beneficiary is non-zero and parses. Then
  `setProtocolFee`, then `setBeneficiary`.
- *8 `upgrade`*: matches since F2 (plan fix 2). The proxy must be one of the five, else `"params"`, checked before the
  code. The 407-byte call limit follows from the 512-byte body bound at recording.
- *9 `transfer to successor`*: matches since F2 (plan fix 3). High bytes → `"params"`. HlAdmin itself →
  `"codehash"`. No code → pending. A designator or another hash → `"codehash"`. Then predecessor, inherited, successor
  and clock factor are read by plain calls → `"successor"`.
- *10, 11 reserved*: matches (recorded and spent at recording, `"unknown kind"`).
- *12 `set mint limits`*: matches. The body is 17 bytes and the token below 2, else `"params"`; any values are
  accepted (r/hlgov refuses a maximum of 0 and a day cap below the maximum).
- *Kinds 3–9 and 12 are the 7-day group*: matches.

No other code mismatch was found, so nothing else in `src/` changed.

### F2.5 Where the plan is wrong or loose (for the coordinator to fold)

1. **§6.5**, "are spent when their code is empty or is an EIP-7702 delegation designator", contradicts its own
   parenthetical and the code. Read: "are spent when their code is an EIP-7702 delegation designator (…) or its code
   hash is not the decision's, and pend while they have no code (…)".
2. **§13 kind 8**, "the implementation's code non-empty, not an EIP-7702 designator (`0xef0100…`), and its
   `EXTCODEHASH` equal to `codehash` at execution (spent otherwise)". Read: "an implementation with no code yet leaves
   the decision pending (`NoCodeYet`); one whose code is an EIP-7702 designator, or whose `EXTCODEHASH` is not
   `codehash` at execution, is spent". Also add the check that §6.5 cites §13 for but §13 never states: "after the
   upgrade the proxy's admin must still be the ProxyAdmin and its owner (the client: its admin) HlAdmin, else the
   execution reverts (`AuthorityLost`) and the decision pends for a cancel".
3. **§13 kind 9**, "spent unless the successor's code is non-empty, not an EIP-7702 designator, …". Read: "pending
   while the successor has no code; spent unless its code is not an EIP-7702 designator, its `EXTCODEHASH` equals
   `codehash`, …".
4. **§6.5**, "code at an address, once there, never changes except an EIP-7702 account's". Read: "code an earlier
   transaction left at an address never changes (EIP-6780) except an EIP-7702 account's". A contract can still
   self-destruct inside the transaction that created it. The rule is unaffected.
5. **§13 header**, "every address 32 bytes left-padded (a non-zero top 12 bytes makes the decision spent at
   execution)". Read "every Ethereum address". Kind 6's router is a remote router (Hyperlane's `bytes32`), taken
   whole by HlAdmin; r/hlgov refuses one with non-zero top bytes, as it refuses every address.
6. **§13 kind 6**, "a zero router (which Hyperlane reads as unenrolling)". That is Sealevel's reading. On the EVM,
   `enrollRemoteRouter(d, 0)` stores zero and keeps the domain enrolled: `_mustHaveRemoteRouter` returns 0, so a
   `transferRemote` to that domain would burn into a message to recipient 0. Unenrolling is `unenrollRemoteRouter`,
   which is what HlAdmin calls for a zero router (F2.4 kind 6). It cannot be reached either way.
7. **New for §5.3 and §8** (the decided rule's consequence, F2.1): before voting a kind 8 or 9, a member also reads
   whether the target has code yet and, if it does not, who can deploy there. A decision waiting for its code ends
   only by the code appearing or by a cancel. The proposer should name an address only they can deploy to, such as
   their own CREATE nonce or a factory only they call, and should deploy before the vote ends where possible. `hlwatch`
   lists each decision waiting for code, and GovDAO cancels the ones it no longer wants.
8. **r/hlgov's `mustHash`** (E-M1's realm, not this lane's) refuses an all-zero code hash "so HlAdmin would only
   spend the decision". Since F2 such a decision pends while the target has no code and is spent once any code
   appears, so it can never execute. The same holds for keccak256(""), the hash of an account without code, which the
   builder does not refuse. Both could be refused as never executable, and the comment should say "would never
   execute it".
9. **§2, §11.4: the deployment's gas.** It is now 36,355,353 (F2.6), up from 36,335,758: "≈36.3M" becomes ≈36.4M.

### F2.6 The run

On `root@<devbox>:/root/src/eth-f2/eth/bridge/contracts`, synced from the worktree with BUILD.md's rsync, using
Foundry 1.8.5 and solc 0.8.33. A clean `git archive` of the final commit gave the same results for fmt, sizes, both
profiles and the snapshot check:
- `forge fmt --check`: clean.
- `forge test --hardfork osaka`: **20 suites, 146 tests: 129 passed, 0 failed, 17 skipped**. The 17 skips are E-M2a's
  differential lanes and manifest, which skip without the generated set (§8). HlAdminHandover.t.sol has 15 tests,
  up from 12.
- `FOUNDRY_PROFILE=factor8 forge test --hardfork osaka`: the same 129 passed, 0 failed, 17 skipped.
- The baseline at `f5fb68f9` on the same box, both profiles: 126 passed, 0 failed, 17 skipped (143).
- `forge lint` on the four changed files: the same 45 findings before and after; none new.
- `forge build --force`: the same two compiler warnings as before (Ed25519.sol's mutability, one shadowing
  declaration); none new.
- `forge test --match-contract GasReport --hardfork osaka -vv`, both trees. The extra EXTCODESIZE costs: execute kind
  8 (the gate) 106,093 → 106,263 exec, 127,297 → 127,467 tx; execute kind 9 410,992 → 411,160 exec, 432,196 → 432,364
  tx. HlAdminExec's 92 more bytes cost the deploy's step 7 8,877,264 → 8,896,859 and the whole deployment 36,335,758
  → 36,355,353 (+19,595). Every other line is unchanged.
- `forge snapshot --no-match-contract Ed25519Differential` (§8's command; the tracked `.gas-snapshot` passed `forge
  snapshot --check` at `f5fb68f9`): regenerated for 129 tests, and `--check` passes again. The diff covers the
  HlAdminHandover tests, every test that deploys (step 7's +19,595), and the means of four fuzz tests (Ed25519Test and
  ReaderFuzzTest, two each). Those four sample their inputs differently now that the project's code changed; the seed
  is the same and each run reproduces. If other lanes change the file too, regenerate it after the merge rather than
  merging it.
- `forge build --sizes` (the default and factor-8 profiles give the same sizes):

| Contract | Runtime | Margin | Initcode |
|---|---|---|---|
| HlAdmin | 17,708 (unchanged) | 6,868 | 44,679 (+92: HlAdminExec's initcode inside; 4,473 under 49,152) |
| HlAdminExec | 21,876 (+92) | 2,700 | 23,421 |
| GnoLightClient, Ed25519Verifier, PausableGate | unchanged (20,667; 7,007; 13,193) | | |

## Fold F4 — audit round 2's contract findings (2026-10-14)

Lane F4, branch `eth-f4` from `bridge-eth` at `b50c4997` (plan e4). It folds the contract half of the first code audit
round (round 2: `audits/r2/contracts.md` E2-CT1–3, `audits/r2/ed25519.md` E2-ED1–2); lane F3 folds the Go half and
follows F4.1. Commits: `d34968f9` (the five proofs of concept, failing), `5d562a62` (E2-CT1), `de9dcfe8` (E2-CT2),
`4212a955` (E2-CT3's test, failing), `84000264` (E2-CT3), `e0ecf82a` (E2-ED1), `8fff7c1a` (E2-ED2), then this section,
the fixture's comment and the regenerated `.gas-snapshot`. Where this section and §1, §2.5, §3.2, §3.4, §5–§8 or Fold
F2 differ, this section holds.

### F4.1 Interface changes (lane F3: read these first)

1. **The setup's step 1 hands the ProxyAdmin to HlAdmin's CREATE address at once, under a checked nonce plan.**
   The deployer creates the ProxyAdmin at its nonce `n` and, as its next transaction (`n + 1`), calls
   `ProxyAdmin.transferOwnership(CREATE(deployer, n + 15))` — `n + 14` when the setup names a verifier already on the
   chain (`Config.ed25519`) — before any proxy exists. Step 10 no longer moves the ProxyAdmin. The plan for a fresh
   key, every creation and every call one transaction (this replaces §1.C's table):

   | Nonce | The verifier deployed at step 4 | A verifier already on the chain |
   |---|---|---|
   | 0 | ProxyAdmin | ProxyAdmin |
   | 1 | `ProxyAdmin.transferOwnership(CREATE(deployer, 15))` | `ProxyAdmin.transferOwnership(CREATE(deployer, 14))` |
   | 2, 3 | Mailbox implementation, proxy | Mailbox implementation, proxy |
   | 4, 5 | MerkleTreeHook, ProtocolFee | MerkleTreeHook, ProtocolFee |
   | 6 | Ed25519Verifier | client implementation |
   | 7, 8 | client implementation, proxy | client proxy; token 0 implementation |
   | 9, 10 | token 0 implementation, proxy | token 0 proxy; token 1 implementation |
   | 11, 12 | token 1 implementation, proxy | token 1 proxy; gate implementation |
   | 13, 14 | gate implementation, proxy | gate proxy; **HlAdmin** |
   | 15 | **HlAdmin** (its HlAdminExec: HlAdmin's nonce 1) | — |

   The setup refuses to go on when the deployer's nonce leaves the plan: `script/Setup.s.sol` reads it after the
   ProxyAdmin (it must be `n + 1`: the deployer created it), after the hand-over (`n + 2`), before HlAdmin (the plan's
   nonce) and after it, and reverts `BridgeSetup.NoncePlan(string step, uint64 planned, uint64 actual)`. **`hleth
   setup` must do the same**: read the pending nonce before each transaction and stop on any deviation; simulate every
   step-1-to-7 transaction before sending it (a transaction that reverts still takes its nonce, so a reverted step
   also moves HlAdmin off the address the ProxyAdmin went to: the deployment must then start over, with nothing
   minted and nothing lost but gas); resume only while the plan still holds (the ProxyAdmin still the deployer's: send
   the hand-over at exactly `n + 1`). New fields: `BridgeSetup.Deployment.adminAt` (the planned HlAdmin address) and
   `adminNonce`; `BridgeSetup.adminConfig(c, d)` (HlAdmin's settings) and `adminNonce(c, first)`.
2. **A launch HlAdmin's constructor requires the ProxyAdmin to be its own**: with no predecessor it reverts
   `ProxyAdminNotHeld()` (`0x77bec738`) unless `ProxyAdmin.owner() == address(this)`. A successor's constructor reads
   nothing (kind 9 moves the ProxyAdmin after the successor exists). finishSetup's check 1 stays.
3. **finishSetup has 33 checks**: at a launch, after 30–31, **32 the mailbox's `nonce()` is 0** and **33 the
   MerkleTreeHook's `count()` is 0** (`SetupIncomplete(32)`, `SetupIncomplete(33)`): nothing was ever dispatched.
4. **`GnoLightClient.initialize(string chainId, uint32 originDomain, bytes[] headerLeaves, Validator[] validators,
   address admin)`**, selector `0x586832f5` (was `initialize(string,uint32,int64,bytes32,(bytes32,int64)[],address)`,
   `0x76f5b35c`): the trusted header as its sixteen leaves. Refusals, in order: `ChainId` (an id empty or over 64
   bytes), `ZeroAdmin`, `TmHeader.BadHeader` (not sixteen leaves, a read leaf not canonical, height ≤ 0), `ChainId`
   (the ChainID leaf not `0A len <chainId>`), `TmValset.BadValidatorSet`, `ValidatorsHash` (the header's ValidatorsHash
   is not the set's hash), `NextValidatorsHash` (nor is its NextValidatorsHash: a header announcing a change),
   **`TrustedTime()`** (`0xf3b3b4ed`, new: the header's time more than **3,600 seconds** before or after
   `block.timestamp`; real time, the same in the factor-8 build). `trustedTime()` is the root's time from the start (it
   was 0 until the first update). `BridgeSetup.Config.trustedLeaves` (`bytes[]`) replaces `trustedHeight` and
   `trustedHash`; the script's JSON key is `trustedLeaves` (hex strings). **`hleth setup`** builds the leaves of the
   header it trusts (`gnolight`'s, as tools/evmvectors does) and sends step 4 while that header is well inside the
   hour (fetch it just before step 4, refuse a `-trusted-file` header older than, say, 30 minutes, and simulate the
   creation first: a step 4 that reverts on the hour breaks the nonce plan, item 1).
5. **`update` and `proveHeader` revert `NoNewSignature()`** (`0xe10e1571`) when the call verifies no slot its stage
   had not verified: no precommit, or only slots counted already. The updater carries in each staging call at least
   one slot the stage lacks; the stage's bitmap is not readable on chain (`stagePower(key)` is), so it keeps what it
   sent (its own earlier calls' data). An `eth_estimateGas` reverting with it means "nothing new to add": send nothing.
6. **`StageProgress(bytes32 indexed key, address indexed submitter, bytes32 headerHash, int64 height, int64 tallied,
   int64 total)`**: the height is new; topic0 `0xc457ce424e1acf9a965dbb42fe78cadf116f498a5b85b48d799a5a8373c0f567`
   (was `0xd89e21418c698fbb833e56a953b3b3e8f028da99f9d6b436da53a3abbeedb92b`); four non-indexed values (`ethx`'s ABI,
   `DecodeStageProgress`, `testdata/sigs.txt`). The watch holds `(height, headerHash)` against gno.land's header at that
   height without the call's data, so a stage made through a contract is no longer `stage:unread`; and every
   StageProgress now carries at least one signature over its header (item 5), so a `stage:unknown` alarm always
   stands on a real signature.
7. **The `Ed25519Verifier` build changed** (E2-ED2's comment and `_decode`'s visibility move its metadata; the
   runtime is 7,007 bytes as before): EXTCODEHASH `0xd84c28c527865c3a66b0905d5645531d704a1ecf7ffcbac1309439de0b438367`
   at factor 1 (was `0xf2976ed3…1c68`), `0x59e59ff8873e9935ecfcc4b5f7904bbb8744e4ea97115f98a6a766da0d6fc55f` at factor 8.
   A verifier deployed from an earlier tree fails check 28 against this tree's `BridgeSetup.ed25519CodeHash()`.
8. **What `hleth audit` must add — the chain cannot see it** (E2-CT1's other half; the contracts make the honest path
   safe, the audit refuses a dishonest one):
   - (a) the ProxyAdmin's `OwnershipTransferred` logs are exactly two: `0 → deployer` in its creation transaction and
     `deployer → HlAdmin` in the deployer's next one, before the first proxy's creation; HlAdmin's address is
     `CREATE(deployer, n + 15)` (`n + 14` with a verifier already on the chain), `n` the ProxyAdmin's creation nonce.
     A deployer that runs its own code instead of the script can still occupy the planned address with a contract of
     its own, upgrade a proxy through it, and hand the ProxyAdmin on to a HlAdmin it creates at a later nonce:
     HlAdmin's constructor and finishSetup both pass that; (a) and (b) refuse it;
   - (b) each of the five proxies' ERC-1967 `Upgraded(address)` and `AdminChanged(address,address)` logs are exactly
     its construction's: one of each, in its creation transaction;
   - (c) `initialize` simulated (`eth_call`, any sender) on each of the five proxies reverts with "Initializable:
     contract is already initialized" (with the proxies' own initializer signatures: the client's new one, item 4);
   - (d) the client's first record (`HeaderRecorded` record 1, `trustedHash()`/`trustedHeight()`/`trustedTime()` until
     the first update) is gno.land's header at that height: the contract binds the header's time to the
     initialization and refuses an announcing one, the audit binds the hash to the chain;
   - (e) as a cross-check of 32–33, no `Dispatch` and no `Process` log of the mailbox before `SetupDone`.

### F4.2 E2-CT1 (high): the deployer never holds upgrade power, and a launch starts empty

**The change.** Step 1 hands the ProxyAdmin to HlAdmin's address before any proxy exists (F4.1 items 1–2). An address
without code takes the ownership (`Ownable.transferOwnership` refuses only zero), and no key signs for a CREATE address:
the ProxyAdmin answers only to the code created there, which the setup makes HlAdmin at step 7 (a deployer that creates
other code there instead is the audit's to refuse, F4.1 item 8a). So from the first proxy on, no key can upgrade one,
change its admin or reach its storage: the audit's scenarios (a) a planted client entry, (b) a planted token balance and
(c) a cleared initializer flag cannot be mounted. Scenario (d) needs no upgrade — the
deployer owns the mailbox and the tokens until step 10, mints through a stand-in default ISM and burns it back, which
leaves the supply 0 (check 24) but a burn the signers would honour — so finishSetup now refuses any trace of a
dispatch at a launch (checks 32–33), and before step 7 the nonce plan already stops the setup (any transaction of the
deployer's moves HlAdmin off its address).

**Nothing else of (d) needs to be on chain.** HypERC20 burns only in `transferRemote`, which dispatches: with the
supply 0 (24) and no dispatch (32), nothing was ever minted (a zero-amount delivery mints nothing). A delivery to
HlAdmin needs the client's proof (its ISM is the client, fixed; the client holds no planted entry now); the gate and the
client have no `handle`. The deployer's other interim powers end in values finishSetup reads (ISMs, hooks, routers for
gno.land, fees, limits, guardians, owners) or carry no funds. One is read by no check: a token's routers for *other*
domains (Router `domains()`); a stray one only lets a holder burn toward that domain (no mint: the client refuses
another origin), so it is the burner's loss, not the bridge's; `hleth audit` should print `domains()` (one: gno.land's).

**The proof: fails before, passes after.** `test/SetupTrust.t.sol` ports the audit's four proofs of concept
(`Audit2Deployer.t.sol`) as the attacks failing, plus the ordering, and `GnoLightClientStaging.t.sol` the fifth (F4.3).
At `d34968f9` (these tests over `b50c4997`'s contracts) `forge test --hardfork osaka --match-contract
"SetupTrustTest|GnoLightClientStagingTest"` gave 4 passed, 6 failed — each on the attack's success:

| Test | Before (`d34968f9`) | After (`5d562a62` on) |
|---|---|---|
| `test_aPlantedClientEntryCannotBeMounted` (a) | FAIL: at step 7 the ProxyAdmin is the deployer's (`0xDe91…07e5 != HlAdmin`) | the deployer's `upgradeAndCall` reverts "Ownable: caller is not the owner"; the setup finishes; the forged mint and the forged decision both revert `UnknownHeader` |
| `test_aPlantedTokenBalanceCannotBeMounted` (b) | FAIL: the deployer's rewrite of token 0 went through | refused; balance 0, supply 0 |
| `test_aReopenedInitializerCannotBeMounted` (c) | FAIL: the rewrite of the gate went through | refused; after the setup `initialize` reverts on the gate, token 0 and the mailbox |
| `test_aPreSetupMintAndBurnIsRefused` (d) | FAIL: finishSetup passed (`SetupDone` in the trace) after the mint and burn | `SetupIncomplete(32)` |
| `test_theProxyAdminIsHlAdminsBeforeAnyProxyExists` | FAIL: no hand-over before the first proxy | in steps 1–6's logs the ProxyAdmin's `OwnershipTransferred(deployer → HlAdmin)` precedes every `Upgraded`/`AdminChanged`; five `Upgraded`, all of construction |
| `test_aCallWithoutANewSignatureIsRefused` (E2-CT2) | FAIL: the zero-signature stage was stored | F4.3 |

In the tests the attempts come after step 7 (before it, the attempt's own transaction would already stop the setup:
`test_aMintAndBurnBeforeStep7StopsTheSetup`, `NoncePlan("step 7: HlAdmin", 15, 21)`, and no code ever lands at the
ProxyAdmin's owner). New in `HlAdminSetup.t.sol`: checks 32 and 33 failing alone (a dispatch through hooks the
deployer set, after step 7; a MerkleTreeHook count written directly, since our mailbox cannot reach it with a nonce of
0), the constructor's refusal (HlAdmin created by another key; at the planned address after the ProxyAdmin went
elsewhere), the plan's refusals (a ProxyAdmin another key created: `NoncePlan("step 1: the ProxyAdmin, created by the
deployer", 1, 0)`; a deployer transaction between steps 6 and 7: `NoncePlan("step 7: HlAdmin", 15, 16)`) and the whole
setup under `vm.startBroadcast` (HlAdmin at 15). The tests that tampered through the deployer's ProxyAdmin (checks 8,
19–24, 26, 27, 29) now do it by HlAdmin's own hand, as a kind 8 would; check 1 by HlAdmin giving the ProxyAdmin away.

**Forge 1.8.5 counts a pranked top-level call as a transaction**: under `vm.startPrank`, as under a broadcast, every
call (not a static call) and every creation the test makes takes the sender's nonce, but a call made from inside
another call frame does not. So the suite and `Setup.run` share the plan (HlAdmin at 15), and a test that needs the
library's refusal calls the step through an external function only where the step reverts before its first
transaction. `DeliveryVectorsTest` names a verifier already on the chain: its tokens and HlAdmin keep the nonces 9, 11
and 14 tools/evmvectors computed (the hand-over takes the verifier's place), so `synthetic.json` is unchanged.

### F4.3 E2-CT2 (low): no stage without a signature; StageProgress carries the height

`_tally` reverts `NoNewSignature` when the call's precommits add no verified slot (F4.1 item 5), and `StageProgress`
carries the header's height (item 6; emitted from `_announce`, which keeps `_tally` within the stack limit). The audit's
proof of concept (`Audit2Stage.t.sol`) is `test_aCallWithoutANewSignatureIsRefused`: a stranger's call for a header
gno.land never made (the live chain id, a height above the trusted one, the live set's hash, no precommit) stored a
zero stage and emitted `StageProgress` before (`d34968f9`: "next call did not revert as expected"); now it reverts,
directly and through a relay contract, stores nothing and logs nothing, and a call repeating a counted slot reverts
too. `test_aPrecommitOfAnotherKeyFails` changed with it (its "nothing new" call now reverts);
`test_stageProgressCarriesTheHeight` checks the event's fields directly and through a contract.

### F4.4 E2-CT3 (low): initialize binds its trust root

`initialize` takes the trusted header's sixteen leaves and binds them (F4.1 item 4): their hash is the trusted hash,
their height the trusted height, the ChainID leaf the chain id's, ValidatorsHash and NextValidatorsHash both the
set's hash (no change announced), the time within **one hour of `block.timestamp`, either way** — the setup's step 4
is one sitting with the choice of the header — and the time is kept as `trustedTime`. Everything else it stored is
stored as before (the record's first entry, the history's first set, `SetAnnounced`/`SetChanged`, no entry).
`test_initialize_bindsItsTrustRoot`: at `4212a955` (the test written against the old signature) gnoland-1's 487,882,
which announces the fifth validator, and 487,881 a day after its time were both accepted ("next call did not revert as
expected"); now the first reverts `NextValidatorsHash`, the second `TrustedTime`, as do an hour and a second either
side; another chain id `ChainId`, another set `ValidatorsHash`, fifteen leaves `BadHeader`; exactly an hour either side
is taken. The real-chain tests deploy at their header's time (`_deployClient` warps there).

### F4.5 E2-ED1 (low): the decoding is tested against Go directly

- **`decode.vec`** (478 keys; Go decodes 322, refuses 156): `<0|1> <key> <x> <y> <label>`, Go's
  `edwards25519.Point.SetBytes` verdict and both affine coordinates (32-byte big-endian, zero when refused) on y = d,
  p + d, p − 1 − d and 2^255 − 1 − d for d = 0…60 with both sign bits (deduplicated), the fourteen encodings of the
  eight points of order dividing 8, 24 honest keys with their sign-flipped and y-negated forms and 24 mixed-order keys,
  and 96 random strings. The package is internal to Go, so `tools/ed25519vectors/goedwards25519` is a copy of Go
  1.25.9's (`crypto/internal/fips140/edwards25519`: `edwards25519.go`, `field/fe.go`, `field/fe_generic.go` byte for
  byte but their import blocks — the FIPS self-check import dropped, `crypto/subtle`, and `field/byteorder.go` for the
  internal byteorder helper — and `field/fe_noasm.go`, which is `fe_amd64_noasm.go` without its build line; Go's
  license beside it); `coordinates.go` adds the one accessor. The math/big model must agree with it on every key, verdict
  and coordinates, or the generator stops. **`test/Ed25519Decode.t.sol`** holds `_decode` (now `internal`) against it
  and, for every refused key, checks `verify` refuses it before the hash (at most 10,005 gas: a mis-decoding would pay
  a whole verification).
- **`probes.vec`** (1,104 records, Go accepts 22): the auditor's forgery probes (`famProbeDecode`) at the repository's
  scale. Under 138 keys — y = p + d, y = d and y = p − 1 − d for d = 0…18 with both sign bits, and six each of random,
  off-curve, sign-flipped and y-negated honest keys — R = [S]B and, for each of the seven non-identity torsion points
  T, R = [S]B − [j]T with the message ground so that k ≡ j (mod ord T). Go accepts a probe only where the key really is
  that point (the 22: keys that are small-order points). Replayed by `Ed25519DecodeTest` through the library and by
  `Ed25519Test.test_verifierContract_vectors` through `Ed25519Verifier`.
- Both are written by `go run ./tools/ed25519vectors local -out contracts/test/vectors/ed25519` (from `eth/bridge`,
  `probes.go`), each from its own seed, so `edges.vec`, `binding.vec`, `gas.vec` and `sha512.vec` regenerate byte for
  byte (checked), and the new two reproduce on a second run. **The differential set is unchanged**: regenerated with
  this tree it is byte for byte E-M2a's (manifest sha256 `c13f8ae9c27c7d9ed9bbb0fd15d8767638115ca95f2dada53fac426047c9cf62`,
  the 60 chunks concatenated `277e1a6387dc6dd08b657bf7d4c034a519f8638dbc91f865e330dc71dbbb96b1`, every chunk compared
  with `/root/src/eth-m2a`'s set on the devbox).
- **`tools/ed25519vectors/mutants.py`** gains the auditor's five planted decoding bugs, N1–N5 (the filter `decode:`
  runs them alone). The table below is F4.8's run: at `b50c4997`'s gates (its 15 unit and vector tests and the 17
  differential lanes over the unchanged set) N1–N5 survive, as the audit found; at this tree's gates every one of the
  18 mutants is caught.

| Mutant | `b50c4997`'s gates: unit/vector failed (of 15), lanes failed (of 17) | This tree's: unit/vector (of 17), lanes (of 17) | The new gates among the failures |
|---|---|---|---|
| S = L accepted | caught (M2a-NOTES §4) | 2, 1 | — |
| S reduced mod L instead of refused | caught | 4, 3 | — |
| S = 0 refused (SCL) | caught | 3, 2 | — |
| a key with y ≥ p refused (strict decoding) | caught | 4, 2 | decode, probes |
| a key's sign bit on a zero x refused | caught | 5, 2 | decode, probes |
| a key's sign bit ignored | caught | 11, 16 | decode, probes |
| A re-encoded canonically before hashing | caught | 4, 1 | probes |
| [L − k]A instead of −[k]A (SCL) | caught | 4, 2 | probes |
| a non-canonical R accepted | caught | 4, 2 | — |
| 2ZZ left unreduced in the addition | caught | 10, 16 | probes |
| the top window (bits 252–253) skipped | caught | 2, 1 | — |
| SHA-512's padding one byte short | caught | 3, 16 | — |
| k = the digest's low 253 bits | caught | 10, 16 | probes |
| **N1** a no-root key decoded as the identity (a universal forgery) | **survived: 0, 0** | 3, 0 | decode, probes, probes via the verifier |
| **N2** a no-root key: the −u check dropped (x·√−1 taken) | **survived: 0, 0** | 1, 0 | decode |
| **N3** a key with y ≥ p (y − p ≥ 2) decoded as the identity (universal) | **survived: 0, 0** | 3, 0 | decode, probes, probes via the verifier |
| **N4** a no-root key decoded as (0, y) | **survived: 0, 0** | 1, 0 | decode |
| **N5** a no-root key decoded as the point of order 2 (universal) | **survived: 0, 0** | 3, 0 | decode, probes, probes via the verifier |

The control (the pristine library) passed every gate first in both runs, and again last. "decode" is
`test_decode_agreesWithGoSetBytes`, "probes" `test_forgeryProbes_agreeWithGo`, "probes via the verifier"
`test_verifierContract_vectors`. N2 and N4 make a no-root key decode to a point off the curve, which no probe can
predict, so only the direct test catches them (as the audit found: probes catch N1, N3 and N5, the direct test all
five). The decode mutants fail no differential lane: the set (unchanged) never pairs such a key with such a signature.

### F4.6 E2-ED2 (info): "never reverts", said exactly

`IEd25519Verifier.verify`'s comment now says what the audit measured: a well-formed call with no value never reverts
given enough gas (260,000 covers a message of up to 175 bytes, about 221,000 used; each further 128 bytes adds
39,000–41,000); a malformed call is reverted by the ABI decoder and value by the non-payable function. The library's
`verify` says "out of gas aside", the client's `_verifySignature` that its call is well-formed and value-free. E2-ED3
(info, a small-order validator key) asks nothing of the contracts: `hlwatch` and the set-change review flag such a key
(lane F3 and the plan).

### F4.7 Where the plan and the other notes are now wrong (for the coordinator to fold)

1. **PLAN.md §5.8**: step 1 "ProxyAdmin (owner: the deployer)" → "ProxyAdmin, handed in the deployer's next
   transaction to HlAdmin's CREATE address by the nonce plan (F4.1 item 1)"; step 4 "the trusted header (height,
   hash, its validator set …)" → "the trusted header's sixteen leaves and its set; the client refuses a header
   announcing a change or more than an hour from its initialization"; step 7 "which creates HlAdminExec" → add "and
   requires the ProxyAdmin to be its own"; step 10 drops the ProxyAdmin ("after which the deployer holds nothing" stays
   true); step 11 "31 checks" → 33 (32 the mailbox's nonce 0, 33 the MerkleTreeHook's count 0, at a launch);
   `hleth audit`'s list gains F4.1 item 8. **§6.3**: "The watch reads `StageProgress`" → "… whose height and header
   hash it holds against gno.land; a call that verifies no new signature is refused, so every stage carries one".
   **§9**'s row "A deploy-time value is wrong" gains "or the deployer's interim powers left a trace: the ProxyAdmin is
   HlAdmin's from step 1, finishSetup refuses a dispatch, the audit refuses any upgrade or ownership move past
   construction".
2. **This file**: §1.C's nonce table (F4.1 item 1 replaces it); §2.5 and §1.E (33 checks); §3.2 (`trustedTime` is the
   root's time from the start); §3.4 (a call carrying nothing new is refused, not stored); §5 and §6 (F4.8).
3. **M2a-NOTES.md** (not this lane's to edit): §1 "never reverts (out of gas aside)" — add the external call's terms
   (F4.6); §4's mutant table — 18 mutants, five of them the decode bugs, caught by `Ed25519Decode.t.sol` (F4.5); §6's
   `local` command also writes `probes.vec` and `decode.vec`; §7 gains `tools/ed25519vectors/{probes.go,goedwards25519/}`
   and `test/Ed25519Decode.t.sol`.
4. **tools/evmvectors** (not this lane's): its comment "the deployer's CREATE addresses (nonces 9, 11, 14 …)" now
   holds for the deployment that names a verifier already on the chain, as `DeliveryVectorsTest` does; the constants
   need no change.

### F4.8 The run

On `root@<devbox>:/root/src/eth-f4/eth/bridge/contracts`, synced from the worktree with BUILD.md's rsync;
Foundry 1.8.5, solc 0.8.33, Go 1.27.1. The baseline is `b50c4997`'s tree on the same box (`/root/src/eth-f4-base`).

- `forge fmt --check`: clean. `forge build --force`: the same two compiler warnings as before (Ed25519.sol's
  mutability, GuardiansModel's shadowing).
- `forge test --hardfork osaka`, with the differential set generated (`go run ./tools/ed25519vectors diff`, F4.5):
  **22 suites, 162 tests: 162 passed, 0 failed, 0 skipped** (the 16 lanes: 118,392 records, 0 disagreements).
  Without the set: 145 passed, 17 skipped (the baseline: 129 passed, 17 skipped). New and changed suites:
  `SetupTrust.t.sol` 6 (new), `HlAdminSetup.t.sol` 16 → 21, `GnoLightClientStaging.t.sol` 4 → 6,
  `GnoLightClient.t.sol` 14 → 15, `Ed25519Decode.t.sol` 2 (new).
- `FOUNDRY_PROFILE=factor8 forge test --hardfork osaka`: the same **162 passed, 0 failed, 0 skipped**.
- `forge build --sizes` (the default and factor-8 profiles give the same sizes):

| Contract | Runtime | Margin | Initcode |
|---|---|---|---|
| GnoLightClient | 21,150 (+483: initialize binds the root; NoNewSignature; the event's height) | 3,426 | 21,564 |
| HlAdminExec | 22,166 (+290: checks 32–33) | 2,410 | 23,725 |
| HlAdmin | 17,708 (unchanged) | 6,868 | 45,164 (+485: HlAdminExec's initcode inside, +304, and the constructor's read; 3,988 under 49,152) |
| Ed25519Verifier | 7,007 (the same size; another hash, F4.1 item 7) | 17,569 | 7,035 |
| PausableGate | 13,193 (unchanged) | 11,383 | 13,417 |

- `forge test --match-contract GasReport --hardfork osaka -vv`, both trees: the deployment's eleven steps
  36,355,353 → **36,641,678** (+286,325: steps 1–6 +235,223 — the larger client, the hand-over, initialize hashing the
  leaves and keeping the time; step 7 +73,600; steps 8–10 −28,642, the ProxyAdmin gone from them; step 11 +6,144, checks
  32–33). An update in one transaction is 17,121 cheaper at every size (4 validators 924,401 → 907,280 exec): the
  report's update is the first after initialize, which no longer writes `trustedTime` from zero, so its figures are now
  a steady update's; the staged finishing calls likewise. `verify` by leaf moves by −1,200 to +576, process with a mint
  +288 and +240, process with a decision +768 and +48, kind 9 −45, proveHeader +23 (the contracts' new layout and the
  deployment's addresses). PLAN §2 and §11.4's "≈36.4M" becomes ≈36.6M.
- `forge lint` on the seven changed sources: 61 findings before, 65 after. The four new: two `block-timestamp` on the
  trust window's comparison (an hour's window; a proposer moves a timestamp by seconds) and two `reentrancy-events` on
  initialize's events after the header's sha256 (a precompile; nothing re-enters). The rest moved with their lines.
- `forge snapshot --no-match-contract Ed25519Differential` (§8's command): regenerated for 145 tests; `--check` passes.
  If other lanes change the file too, regenerate it after the merge rather than merging it.
- The mutation check (F4.5's table): `python3 -I tools/ed25519vectors/mutants.py <scratch> <pristine> [decode:]`, the
  scratch projects holding the Ed25519 sources, tests and vectors with the differential set as files: `b50c4997`'s gates
  over the five decode mutants, this tree's over all 18 (logs: `/root/src/eth-f4-logs/mut-{before,after}.log`).
- `go vet ./tools/ed25519vectors/...` and `gofmt -l tools/`: clean. `local` writes `edges.vec`, `binding.vec`,
  `gas.vec` and `sha512.vec` byte for byte as committed and `probes.vec` and `decode.vec` the same on every run; `diff`
  writes E-M2a's set byte for byte (F4.5).


## Fold F5 — audit round 2's plan red team, the contract half (2026-10-14)

Lane F5, branch `eth-f5` from `bridge-eth` at `00d97c09`. It folds the contract half of `audits/r2/plan-redteam.md`'s
E2-RT1, E2-RT3, E2-RT6 and E2-RT10; a finding of E2-RT1's class met while folding it (F5.3: a foreign HlAdminExec); and
three findings of an independent review of the fold (F5.7). The Go lanes fold the rest (F5.1 items 11–13). Commits:
`4088d36c` (the red team's scenarios as tests, failing), `4e5725d3` (E2-RT1), `38df65e2` (E2-RT3), `aa1763ab` (E2-RT6),
`306f6288` (E2-RT10), `7b942e72` (a foreign HlAdminExec as tests, failing), `82d70b63` (its guard), `7b519b0e` (kind 9's
bindings under ER7, a test), `fa28b127` (the review's three as tests, failing), `64f0e1b4` (their fixes), then this
section and the regenerated `.gas-snapshot`. Where this section and §1, §2.5, Fold F2 or Fold F4 differ, this section
holds.

### F5.1 Interface changes (the Go lanes: read these first)

1. **Two new views on HlAdmin**: `mailboxPkgPathHash() returns (bytes32)` (`0x40c354cb`: keccak256 of the hlmailbox
   path, until now held only by HlAdminExec; kind 9 compares it, item 6) and `launchNonce() returns (uint32)`
   (`0xe9a037df`: the launch line, item 12).
2. **Three new immutables in HlAdmin, which `hleth audit` must expect** — an immutable with no expectation fails the
   audit (`cmd/hleth/mask.go`), so until these are added every honest launch fails it: `HlAdmin.mailboxPkgPathHash` =
   keccak256 of the hlmailbox path (as `HlAdminExec.mailboxPkgPathHash`); `HlAdmin._execCodeHash` = the EXTCODEHASH of
   the recorded `hlAdminExec`, whose code the audit compares with the build; `HlAdmin._startSequence` = r/hlgov's
   counter at the deploy (`Config.startSequence`; 0 in a successor).
3. **Two new errors**: `HlAdmin.ForeignExec()` (`0x33200c06`) and the file-level `ImplementationMoved(address proxy)`
   (`0x2e23fc45`, in `admin/HlAdminStorage.sol` beside `AuthorityLost`); `ethx`'s error table (`abi/gen.sh`) needs
   both. `ForeignExec` from any HlAdmin means the code it delegates to is not its own: an alarm, never a retry; a kind
   9 reverting with it (the successor's) pends, and GovDAO cancels it. `ImplementationMoved` reads as `AuthorityLost`
   does: an alarm, and a cancel.
4. **`execute(n)` reverts `WrongDecision()`** (`0xc2eb96e4`, as before) unless n lies above the start, at or below
   `lastSeq`, and a record stands there — whatever storage holds at any other number; **`decision(n)`** answers all
   zero unless the same holds (it used to answer number 0 with the stored fields). The start is the launch's
   `startSequence`, or the number a successor inherited (`Inherited(predecessor, lastSeq)`). Nothing changes for a
   number r/hlgov delivered.
5. **`handle`** clears the record at the number it accepts — its `deliveredBlock` and its params' length word —
   before anything else. No interface change.
6. **Kind 9's new spend reason, `DecisionSpent(n, 9, "bindings")`**: after `"codehash"` and `"successor"`, the
   successor must answer sixteen views exactly as HlAdmin answers them, byte for byte: `mailbox()`, `client()`,
   `gate()`, `proxyAdmin()`, `merkleTreeHook()`, `protocolFee()`, `ed25519()`, `ed25519CodeHash()`, `hlgov()`,
   `origin()`, `localDomain()`, `mailboxPkgPathHash()`, `token(0)`, `token(1)`, `route(0)`, `route(1)`. A view that
   reverts reverts the execution, which pends. `hlwatch` and `hleth decision` should print each beside HlAdmin's
   before the vote: a kind 9 that would be spent `"bindings"` is dead on arrival.
7. **Kind 8**: after the upgrade, with a call or without, `ProxyAdmin.getProxyImplementation(proxy)` must be the
   decided implementation (`ImplementationMoved(proxy)` otherwise: the execution reverts, the decision pends); then,
   as before, the proxy's admin and owner (`AuthorityLost`).
8. **finishSetup's checks: 32 and 33 are retired, never reused; 34 is new** (`SetupIncomplete(34)`, at a launch):
   HlAdmin's own record is empty — `lastSeq()` the start, `pending()` 0, `cancelledRanges()` empty. A launch checks
   1–31 and 34, a successor 1–29; `SetupIncomplete(32)` and `(33)` no longer occur. At a launch finishSetup then
   records the launch line (item 12).
9. **`successorInit`** (only the predecessor, once, as before) first requires its own HlAdminExec (`ForeignExec`),
   then writes the successor's whole record: the number and the start (both `lastSeq_`), nothing pending, no cancelled
   range, the holds replaced by the predecessor's, `setupDone` false, `launchNonce` 0, and the keys (their flags and
   the hold cleared, as before). Nothing the successor's own initcode left in its storage survives it.
10. **Gas** (F5.10): kind 9 +30,895; kind 8 +2,801; every other `execute` +719 to +741; `handle` +596 (a pending
    kind) or +4,997 (a cancel); the deployment +413,295 (≈37.1M).
11. **E2-RT1's Go half: what `hleth audit` must compare, which the chain cannot.** For each creation transaction of
    the deployment — a top-level creation (`to` empty) from the deployer at its planned nonce (F4.1 item 1), value 0,
    whose receipt's `contractAddress` is the recorded address — the transaction's input, byte for byte, against the
    build's creation code followed by the ABI encoding of the constructor arguments the plan gives. With the verifier
    deployed at step 4 (the deployer's first nonce n; `script/Setup.s.sol` builds exactly these):

    | Nonce | Contract | Build | Constructor arguments |
    |---|---|---|---|
    | n | ProxyAdmin | OpenZeppelin 4.9.3 `ProxyAdmin` | none |
    | n + 2 | Mailbox implementation | Hyperlane `Mailbox` | `(uint32 localDomain)` |
    | n + 3 | Mailbox proxy | `TransparentUpgradeableProxy` | `(mailboxImpl, proxyAdmin, Mailbox.initialize(deployer, proxyAdmin, proxyAdmin, proxyAdmin))` |
    | n + 4 | MerkleTreeHook | Hyperlane | `(mailbox)` |
    | n + 5 | ProtocolFee | Hyperlane | `(0.001 ether, 0, beneficiary, deployer)` |
    | n + 6 | Ed25519Verifier | ours | none |
    | n + 7 | GnoLightClient implementation | ours | `(mailboxPkgPathHash, verifier)` |
    | n + 8 | client proxy | `TransparentUpgradeableProxy` | `(clientImpl, proxyAdmin, GnoLightClient.initialize(chainId, gnoDomain, trustedLeaves, validators, deployer))` |
    | n + 9, n + 11 | token 0, token 1 implementations | Hyperlane `HypERC20` | `(6, 1, 1, mailbox)` |
    | n + 10, n + 12 | token 0, token 1 proxies | `TransparentUpgradeableProxy` | `(tokenImpl, proxyAdmin, HypERC20.initialize(0, name, symbol, 0, 0, deployer))` |
    | n + 13 | PausableGate implementation | ours | none |
    | n + 14 | gate proxy | `TransparentUpgradeableProxy` | `(gateImpl, proxyAdmin, PausableGate.initialize(deployer, mailbox, client, [token0, token1], seats, watch, maxPerMessage, dayCap))` |
    | n + 15 | HlAdmin | ours | `(HlAdmin.Config)`, one tuple: `BridgeSetup.adminConfig` |

    With a verifier already on the chain every row from n + 6 moves up one (HlAdmin at n + 14), and the verifier's own
    creation is no part of the deployment: it has no storage, no constructor arguments and no immutables, so its
    runtime code against the build is the whole check (check 28 and the audit). HlAdminExec, the sixteenth contract, is
    created inside HlAdmin's constructor (HlAdmin's CREATE nonce 1): HlAdmin's input carries its creation code and its
    settings, and the audit's comparison of its runtime code (and `HlAdmin._execCodeHash`, item 2) covers what was
    created. The creation code is the build's `bytecode.object`, metadata included — ours from this tree at the clock
    profile deployed, Hyperlane's and OpenZeppelin's from the release's compiler input — so it must come from the very
    compile the runtime comparison uses. Before a vote, `hlwatch` does the same for a kind 8 implementation and a kind
    9 successor: the input of the transaction that created the target (a target created inside another call, by a
    factory, is compared from the creating frame's trace, or refused) against the build and the arguments the decision
    implies — a successor HlAdmin's `Config` with the live HlAdmin's sixteen bindings (item 6), `predecessor` the live
    HlAdmin, no seats, watch slots, number or start — and the code at the successor's `exec()` against the build of
    HlAdminExec for it.
12. **E2-RT6's Go half, now on a line the chain draws.** At a launch finishSetup records the mailbox's `nonce()` as
    `launchNonce()`: a message whose nonce is below it was dispatched before the launch — by a stranger after step 9,
    or by the deployer while it owned the mailbox and the tokens (F4.2's scenario d) — and is none of the bridge's
    burns. The signers, the monitors, `hlwatch` and the Proposal 2 realm (which can compile it in) count none below it:
    each holds the message, and so its nonce. Read it from the launch HlAdmin (`deployment.json`'s `hlAdmin`; a
    successor's is 0). A block number cannot draw it: the deployer's burn can sit in finishSetup's own block, before
    it. Today the signers start at `deployBlock`, the ProxyAdmin's creation (`cmd/hleth/deployment.go`,
    `agent/config.go`), and the monitors at the mailbox's first block (`agent/monitor.go:281`), both before step 7:
    they must filter by the line. `hleth audit` lists every `Dispatch` and `Process` of the mailbox below the line, and
    fails on a `Dispatch` sent by either token (a burn: something was minted before the launch) or any `Process` (a
    delivery before the realms exist); a stranger's `Dispatch` is listed, not failed. F4.1 item 8(e) is now the only
    check of it.
13. **What `hleth audit` must not take on trust** (F5.7, the review's finding 2). `setupDone()` is storage, which an
    initcode can plant: the audit requires that finishSetup ran — its `SetupDone` emitted by a top-level call to
    `finishSetup()` after step 10, not by the creation transaction — and re-runs it by `eth_call` with `setupDone`
    overridden to false (it must pass). It reads `lastSeq()` against `startSequence` (and `_startSequence`), `pending()`
    0, and `cancelledRanges()`, `holds()`, `flags()`, `hold()` and `successor()` empty or each explained by its event
    (`HoldEnded`, `FlagSet`, `FlagCleared`: a planted entry has none). The creation-input comparison (item 11) refuses
    every planted state outright; these reads check it.

### F5.2 E2-RT1 (high, the contract half): a decision exists only at a number HlAdmin recorded

**The change.** The layout gains `startSeq` (packed in the first word beside `lastSeq`, with the launch line: no other
field moves), the number the sequence started after — the launch's `startSequence`, written by the constructor, or the
number a successor inherits, written by `successorInit`. HlAdmin also keeps a launch's start as the immutable
`_startSequence` (F5.7). `handle` accepts only `lastSeq + 1`, so the numbers it accepted are exactly (start,
`lastSeq`]; at each it now clears the record (`deliveredBlock` and the params' length word) before anything else, so
a number where a cancel, an unknown kind or a body over 512 bytes lands holds no record, a pending kind writes its own
over nothing, and no planted length makes the write a gas bomb or a panic. `execute`, the `decision` view and a cancel's
one-number path read a record only through `_isDecision`: above both starts, at or below `lastSeq`, `deliveredBlock`
non-zero; at any other number `execute` reverts `WrongDecision` and nothing is spent. At a launch, finishSetup's check
34 requires the record empty (`lastSeq` the start, nothing pending, no cancelled range); `successorInit` writes a
successor's whole record (F5.1 item 9).

**Why more than the red team's `number > lastSeq`.** Alone it leaves two ways to run a planted record. One planted at or
below the start (number 0 at a fresh r/hlgov) is at or below `lastSeq`, mature since 1970, and runs as soon as any real
decision pends (the pending count `_close` needs) — the same kind 9, with no `DecisionRecorded` for the watch to list.
One planted where r/hlgov's next cancel or unknown kind lands is never written over, and is at or below `lastSeq` once
that delivery passes. Hence the floor, the clearing, and check 34 against a planted window or pending count.

**What holds now.** A launch's start never moves (the immutable never; the stored one only in `successorInit`, which a
launch refuses), and only `handle` moves `lastSeq`, one number at a time, clearing or writing that number's record.
So a record an initcode planted can run only inside (`_startSequence`, `lastSeq`] — that is, only if the initcode also
planted `lastSeq` above r/hlgov's counter, which check 34 refuses if finishSetup runs and the audit refuses either way
(F5.1 item 13). A successor's window is empty at its inheritance, by `successorInit`. Every number that can run is
within a cancel's reach: a cancel recorded later clamps its range at its own number, above every number in the window;
a cancel naming a number at or below the start closes nothing (closing a planted record there would underflow the
pending count and leave the cancel undeliverable for good, blocking the sequence).

**What the chain still cannot see at a launch** (the audit's, F5.1 items 11 and 13): a planted `setupDone`
(finishSetup never runs, and the view says true), a planted `successor` (retired at birth), guardian flags, a hold,
the hold history, a planted `lastSeq` above the start, and every immutable (the masked runtime comparison's). Check 34
leaves the guardians' state out on purpose: the guardians can write it before finishSetup (a flag a key keeps up, two
flags and a dip past the grace), so a check of it would hand them a veto over the launch — and a planted `setupDone`
skips any check of finishSetup's anyway.

**The proof: fails before, passes after.** `test/PlantedRecord.t.sol` (new). `PlantingInitcode` is what a compromised
deployer sends at step 7's nonce (or a proposer at a successor's): it creates HlAdminExec with HlAdmin's settings
(HlAdmin's nonce 1), copies the first sixteen words of HlAdmin's namespace as HlAdmin's constructor leaves them (read
from a real HlAdmin deployed at the same address in a snapshot, then undone), writes its record, and returns HlAdmin's
exact runtime code; each test asserts the address, the code hash and HlAdminExec's code hash are the honest ones.
`Grabber` is the deployer's contract a planted kind 9 names: it answers kind 9's questions as a fresh successor and
every binding view as HlAdmin (its code says what it likes, so F5.4's bindings cannot stop it). Before: `4088d36c`'s
tests over `00d97c09`'s contracts (the last two rows: `fa28b127`'s over `7b519b0e`'s); after: the head.

| Test | Before | After |
|---|---|---|
| `test_aDecisionPlantedAboveTheLastNeverRuns` (the red team's: `decisions[2^64−1]`, a kind 9 to the Grabber; a real decision pends) | FAIL: the attacker's `execute(2^64−1)` ran — `DecisionExecuted(18446744073709551615, 9)`, `Retired(Grabber)`, the ProxyAdmin, mailbox, tokens, gate, hooks and the client's admin all the Grabber's | `WrongDecision`; `decision(2^64−1)` empty; nothing moved; the real decision runs on its clock |
| `test_aDecisionPlantedAtOrBelowTheStartNeverRuns` (start 41; records at 41 and 7) | FAIL: `execute(41)` ran the planted kind 9 | `WrongDecision` at 41 and 7; GovDAO's cancel naming both is recorded and closes nothing |
| `test_aDecisionPlantedWhereNothingPendsNeverRuns` (records at 1 and 2; r/hlgov's 1 a cancel, 2 an unknown kind) | FAIL: `execute(1)` ran it | `WrongDecision` at 1 and 2 |
| `test_finishSetup_refusesAPlantedRecord` (a pending count; `lastSeq` 1 with a record at 1; a cancelled range) | FAIL: finishSetup passed | `SetupIncomplete(34)`, each |
| `test_aSuccessorsPlantedRecordDiesAtItsInheritance` (a successor's initcode: every number cancelled, 3 pending, a hold that never ended, its setup done, a record at the number it inherits) | FAIL: "its record is its predecessor's alone: 3 != 0" | its record its predecessor's; the planted record `WrongDecision`; its finishSetup runs; its first decision executes (before, the planted range would have closed it as cancelled, and every one after it) |
| `test_aPlantedSetupDoneRunsNoPlantedDecision` (the review's: start 41, a planted `setupDone`, start 40, a kind 9 at 41, 1 pending) | FAIL: finishSetup refused `SetupAlreadyDone` before check 34, and `execute(41)` ran | `WrongDecision`: 41 is not above `_startSequence` |
| `test_aPlantedParamsLengthBlocksNothing` (the review's: at the next number, a params length of 2^128; a malformed length word) | FAIL: `HlAdmin::handle` ran out of gas (16.1M, under the 16,777,216 transaction cap); the second case, alone: `panic 0x22` | both deliver; the record holds its own params |

### F5.3 A foreign HlAdminExec (high; E2-RT1's class, found while folding it)

**The finding.** Kind 9 pins the successor's runtime code hash, and that runtime names its HlAdminExec by address only
— `exec`, an immutable holding CREATE(successor, 1), the same whatever was created there. A proposer's initcode can
create other code at that nonce and return HlAdmin's exact runtime: the code hash is the honest one (the very hash a
reviewer gets by deploying the reviewed build in a simulation, as F2's test computes it), every kind 9 check and the
sixteen bindings of F5.4 pass, the hand-over runs — and the successor runs that code by delegatecall, as itself, in
`finishSetup` (which anyone may call) and in every `execute`. A scratch proof of concept on `306f6288` (not in the tree:
a `checkSetup()` that transfers the ProxyAdmin) passed: after the hand-over, a stranger's `successor.finishSetup()` gave
the ProxyAdmin to the thief. At a launch the same initcode is the deployer's; the audit's comparison of HlAdminExec's
code with the build refuses it, but nothing on chain did.

**The fix.** HlAdmin's constructor records its HlAdminExec's EXTCODEHASH as the immutable `_execCodeHash`, inside the
runtime a code hash pins; every delegatecall (`execute`, `finishSetup`) and `successorInit` revert `ForeignExec` unless
the code at `exec` has it. An initcode that returns the honest runtime gets nothing foreign run: a launch's finishSetup
refuses before any check, and a successor's inheritance refuses, so the predecessor's kind 9 reverts and pends for a
cancel instead of handing every authority over. An initcode that also changes `_execCodeHash` returns a runtime that
differs from the build's in that immutable: a full-runtime hash computed from the build refuses it, and a masked
comparison must print it beside the hash of the build's HlAdminExec for that HlAdmin (F5.1 items 2 and 11). That
also covers the review's finding 6: the kinds run on HlAdminExec's own copies of the bindings, which kind 9's views
(HlAdmin's copies) do not read; `_execCodeHash` is what pins them. HlAdmin +162 bytes; each delegatecall pays one
EXTCODEHASH, which warms the account the delegatecall then finds warm.

**Other ways, for the coordinator** (none taken: each moves more): HlAdminExec created by CREATE2 under a fixed salt,
so its address commits to its initcode and settings (the address changes, and with it the realms' refused-recipient
list and `hleth`'s record); or an HlAdminExec with no immutables, reading its settings from HlAdmin's views, whose hash
HlAdmin compiles in (`keccak256(type(HlAdminExec).runtimeCode)`) — that pins it with no reviewer's help, and makes the
bindings kind 9 compares the ones the kinds use, at gas and bytes HlAdminExec's margin may not hold.

**The proof.** Before: `7b942e72`'s tests over `306f6288`'s contracts; after: the head.

| Test | Before | After |
|---|---|---|
| `test_aSuccessorsForeignExecNeverRuns` | FAIL: the hand-over ran (`DecisionExecuted(1, 9)`, `Retired(successor)`) | `ForeignExec`; the kind 9 pends; nothing moved; the successor's own finishSetup refuses |
| `test_aLaunchsForeignExecNeverRuns` | FAIL: finishSetup ran the foreign `checkSetup` | `ForeignExec` before any check; `setupDone` false; nothing moved |

### F5.4 E2-RT3 (medium): kind 9 requires the successor bound as HlAdmin is

**The change.** After `"codehash"` and `"successor"`, `_transfer` asks the successor the sixteen binding views of F5.1
item 6 and HlAdmin itself the same (a static call to `address(this)`: HlAdminExec runs as HlAdmin), and compares the
answers' keccak256 — the whole return data, byte for byte; any difference spends the decision `"bindings"`, and a call
that fails reverts the execution (the decision pends: only answers the successor's pinned code gives may spend it,
ER7). HlAdmin gains `mailboxPkgPathHash()`, the one binding it did not expose. All sixteen fit, so nothing of the
brief's list is left to `hlwatch` on chain: HlAdminExec +634 bytes, with the selectors in one `uint32[16]` and both
calls through one assembly helper (measured: a first form with three call sites +1,232; one loop over a `bytes4[16]`
+1,047, each left-aligned selector a 32-byte push).

**Still `hlwatch`'s:** E2-RT3's second scenario is a kind 8 — a client implementation whose `ED25519` immutable is the
proposer's upgradeable verifier — and F5 reads no immutable of a kind 8 implementation; `hlwatch` compares kind 8
targets with the immutables masked and printed beside the values they must have, and their creation input (F5.1 item
11). And a successor's HlAdminExec settings (F5.3).

**The proof.** `test_kind9_aSuccessorBoundElsewhereIsSpent` (HlAdminHandover.t.sol): sixteen successors of HlAdmin's
own code, each with one binding elsewhere (the client an ISM that verifies everything, the red team's); then one bound
as HlAdmin is. Before (`4088d36c`): FAIL "DecisionExecuted != expected DecisionSpent" — the first (another mailbox)
took everything (`Retired(successor)`, `DecisionExecuted(1, 9)`). After: sixteen `"bindings"`, nothing moved, and the
one bound as HlAdmin takes everything. `test_kind9_aSuccessorWhoseBindingsRevertPends` (`7b519b0e`): a successor whose
binding views revert — before (`00d97c09`'s contracts) the hand-over ran; after, the execution reverts "no binding" and
the decision pends. `MockSuccessor` gained a fallback answering every other view as its predecessor, so the tests of its
other refusals still reach them.

### F5.5 E2-RT6 (medium): checks 32 and 33 retired; the launch line

**The change.** finishSetup no longer requires the mailbox's `nonce()` or the MerkleTreeHook's `count()` to be 0 (the
numbers are retired, never reused, F5.1 item 8): after step 9 anyone may `Mailbox.dispatch`, which moves both, so one
stranger's transaction between steps 9 and 11 failed the setup for good. What the checks answered — a burn the
deployer dispatched before the launch while it owned the mailbox and the tokens (F4.2's scenario d) — is answered by a
line instead (the review's finding 1, F5.7): a launch's finishSetup records the mailbox's nonce as `launchNonce()`,
which vetoes nothing, and every consumer of a burn counts none below it (F5.1 item 12). Nothing else of F4.2 leaned on
the checks: the ProxyAdmin is still HlAdmin's from step 1, so no state can be planted through a proxy, and a token's
supply is still 0 at finishSetup (check 24).

**The proof.** `test_finishSetup_aStrangersDispatchVetoesNothing` (HlAdminSetup.t.sol): a stranger's
`dispatch(GNO, route, "")` after step 9 (nonce 1, count 1), then steps 10–11. Before (`4088d36c`): FAIL
`SetupIncomplete(32)`. After: the setup finishes. The two tests of checks 32 and 33 are gone. SetupTrust's scenario (d)
is now `test_aPreSetupMintAndBurnLiesBelowTheLaunchLine`: the deployer's mint and burn back after step 7, the setup
finished, the burn's nonce below `launchNonce()`, and a burn after the launch (a real mint through the gate, then a
burn) at or above it. Before (`fa28b127`'s test over `7b519b0e`): FAIL "HlAdmin draws the launch line" (no such view).

### F5.6 E2-RT10 (low): after kind 8 the proxy runs the implementation the decision pinned

**The change.** After the upgrade (and its call), `_upgrade` requires `ProxyAdmin.getProxyImplementation(proxy)` to be
the decided implementation (`ImplementationMoved(proxy)` otherwise), then, as before, the proxy's admin the ProxyAdmin
and its owner (the client: its admin) HlAdmin (`AuthorityLost(proxy)`). Both revert, so the decision pends for a
cancel. The implementation is checked first so that a call which re-pointed the slot is named for what it did (the
owner's question would otherwise reach the unpinned code). It runs after every kind 8, call or not (an empty call
cannot move the slot, so that path always passes). A call that overwrote the admin slot sends the ProxyAdmin's
questions on to the implementation, which reverts or answers otherwise: the execution reverts either way — unless the
new code answers both `implementation()` and `admin()` as expected, which none of the five can as built (the review's
finding 7: a member reads a kind 8's code for it, §8). HlAdminExec +207 bytes; kind 8 +2,801 gas.

**The proof.** `test_kind8_aCallThatRepointsTheProxyReverts` (HlAdminHandover.t.sol): a gate V2 whose initializer
writes the EIP-1967 implementation slot to `GateUnpinned`. Before (`4088d36c`): FAIL "next call did not revert as
expected" — `Upgraded(GateRepoint)`, `GateRepoint::initializeV2(GateUnpinned)`, then the owner check already answered
by `GateUnpinned::owner()`, and `DecisionExecuted(1, 8)`: the gate left on code no hash pinned. After:
`ImplementationMoved(gate)`, the decision pending, the implementation unchanged, a cancel closing it.

### F5.7 The independent review (an adversarial agent over `00d97c09..82d70b63`, with tests on the devbox)

Seven findings; three were folded (`fa28b127`, `64f0e1b4`), the rest are the Go lanes', the coordinator's or recorded:
1. **High — E2-RT6's off-chain answer did not exist**: the signers start at the ProxyAdmin's creation and the audit
   only notes the nonce, so a pre-launch mint-and-burn would be signed. **Folded**: the launch line (F5.5) — an anchor
   that vetoes nothing — and F5.1 item 12 for the consumers. (Its test that only compared block numbers is replaced.)
2. **High — a planted `setupDone` skips check 34, and the audit takes the flag as proof**: at start 41, a planted start
   of 40 and a kind 9 at 41 ran. **Folded**: the immutable `_startSequence` under every decision (F5.2); F5.1 item 13
   for the audit.
3. **Medium — a planted params length (2^128, or a malformed word) at the next number blocks the sequence for good**,
   at a launch and in a successor. **Folded**: `handle` clears the length word (the reviewer's own fix, tested by it on
   the devbox).
4. **Medium — check 34 misses the hold history, the guardians' state and `successor`** (one planted hold lets a kind 5
   through any hold on its 7th day; nine block every kind 9; a planted length of 2^40 makes every flag change run out of
   gas). **Not folded on chain**: a check of the guardians' state hands them a veto over the launch, and a planted
   `setupDone` skips it anyway; the creation-input comparison refuses it, and the audit reads them (F5.1 item 13). A
   successor's are written by its inheritance.
5. **Low — the setup-only bindings go stale**: kind 9 binds the successor to this HlAdmin's `ed25519`,
   `ed25519CodeHash`, `mailboxPkgPathHash` and `route(i)`, which only finishSetup's checks 15 and 26–28 read. After a
   kind 8 moves the client onto another verifier or path, or a kind 6 moves a route, every later successor's finishSetup
   fails for good. **Open** (F5.9 item 1): the brief binds them; the choice is the coordinator's.
6. **Low — the kinds run on HlAdminExec's own copies of the bindings, which kind 9 does not read**; and the audit lacks
   the new immutables. **Recorded**: `_execCodeHash` pins them (F5.3); F5.1 items 2–3.
7. **Info — kind 8's order**: an implementation that overwrote the admin slot and answers `implementation()` and
   `admin()` as expected would pass both checks; none of the five can. **Recorded** (F5.6).

### F5.8 Where the plan and the other notes are now wrong (for the coordinator to fold)

1. **PLAN.md §5.8**: step 10's "a launch's `finishSetup` therefore requires the mailbox's `nonce()` and
   `MerkleTreeHook`'s `count()` to be 0 — nothing was ever dispatched" → "a launch's `finishSetup` records the mailbox's
   nonce as the launch line: a message below it was dispatched before the launch and is none of the bridge's burns (no
   check of the nonce: after step 9 a stranger's dispatch would veto the launch)". Step 11: "… the protocol fee 0, the
   mailbox's `nonce()` 0 and `MerkleTreeHook`'s `count()` 0 — 33 checks in all" → "… the protocol fee 0, and its own
   record empty (the last number the start, nothing pending, no cancelled range) — checks 1–31 and 34 (32 and 33
   retired)". Step 7 gains "and records its HlAdminExec's code hash: it never delegates to other code". The audit
   paragraph gains F5.1 items 11–13.
2. **§6.5**: "a pending decision is a storage record …" gains "and exists only at a number `handle` accepted, above
   the start (a launch's held in the code) and at or below the last — a record anything else wrote (an initcode can
   write any storage) never runs, and every number that can run is within a cancel's reach"; kind 9's sentence gains "to
   a successor bound as `HlAdmin` is (its mailbox, client, gate, tokens, `ProxyAdmin`, hooks, verifier and its hash,
   `hlgov`, domains, routes and `pkg_path` hash: spent otherwise), whose inheritance writes its whole record and refuses
   an `HlAdminExec` other than the one its runtime pins"; kind 8's parenthesis gains "and when the proxy's
   implementation is not the decided one (`ImplementationMoved`: a call can write the EIP-1967 slot)".
3. **§13**: kind 8 gains "… and the execution reverts (`ImplementationMoved`, pending) unless afterwards
   `ProxyAdmin.getProxyImplementation(proxy)` is the implementation"; kind 9's spend list gains "its sixteen bindings
   not this contract's (`"bindings"`)".
4. **§5.6 and §7**: the signers and Proposal 2 count no burn below `launchNonce()` (F5.1 item 12).
5. **§8 and §9**: before a kind 8 or 9 a member reads the target's creation input against the build (F5.1 item 11),
   for a kind 9 its bindings and the code at its `exec()`, and for a kind 8 that its code answers no proxy-admin
   question (F5.6); §9's row "A compromised deployer plants state no read can see" → "… a launch's decisions start
   above a start held in its code, it runs only its own HlAdminExec, a burn below the launch line is no burn; `hleth
   audit` compares every creation's input with the build and takes no stored flag as proof".
6. **This file**: F4.1 item 3, §2.5 and §1.E (checks 32–33 → retired; 34); F4.1 item 8(e) (F5.1 item 12); §1.7's
   `DecisionSpent` reasons gain `"bindings"`; §1.9's errors gain `ForeignExec` and `ImplementationMoved`; F2.4 item 17
   (successorInit writes the whole record and checks the successor's HlAdminExec); §5–§8 (F5.10); F4.8's "3,988 under
   49,152" for HlAdmin's initcode left out the 896 bytes of its constructor's arguments (F5.9 item 3).
7. **The deployment's gas** (§2, §11.4): ≈36.6M (F4.8) becomes ≈37.1M (F5.10).

### F5.9 Open

1. **Stale setup-only bindings** (F5.7 item 5): after a verifier swap, a pkg-path change or a route moved by kind 6,
   every later successor must carry the stale value and its finishSetup fails check 27, 26 or 15 for good (nothing on
   chain reads a successor's `setupDone`; `hleth audit` and `hlwatch` do). Either compare those four in kind 9 with the
   live values (`client.ED25519()` and its EXTCODEHASH, `client.mailboxPkgPathHash()`, `token.routers(origin)`), at
   bytes in HlAdminExec, or drop them from the bindings (they grant no power).
2. **At a launch the chain still cannot see** a planted `setupDone`, `successor`, guardian state, hold history, a
   planted `lastSeq` above the start, or a wrong immutable: the creation-input comparison and the reads of F5.1 item 13
   are the only guards.
3. **HlAdmin's creation is now the tightest size**: its initcode is 47,175 bytes (HlAdminExec's creation code inside),
   and with `Config`'s 896 bytes of arguments the creation transaction carries 48,071 — 1,081 under EIP-3860's 49,152
   (F4's figure, 3,988, left the arguments out: it was 3,092). Every byte added to HlAdmin's or HlAdminExec's runtime
   counts there. HlAdminExec's runtime has 1,729 to EIP-170, HlAdmin's 5,622.
4. **A successor's HlAdminExec** is pinned on chain only through `_execCodeHash`, whose value a reviewer must check
   (F5.3's other ways would pin it outright).

### F5.10 The run

On `root@<devbox>:/root/src/eth-f5/eth/bridge/contracts`, synced from the worktree with BUILD.md's rsync;
Foundry 1.8.5, solc 0.8.33. The baseline is `00d97c09`'s tree on the same box (`/root/src/eth-f5-base`); the "before"
runs are scratch copies holding an earlier commit's contracts with this fold's tests (`eth-f5-before`, `eth-f5-prefix`).

- `forge fmt --check`: clean. `forge build --force`: the same two compiler warnings as before (Ed25519.sol's
  mutability, GuardiansModel's shadowing).
- `forge test --hardfork osaka`, with the differential set generated (`go run ./tools/ed25519vectors diff`, 118,392
  records in the 16 lanes): **23 suites, 173 tests: 173 passed, 0 failed, 0 skipped**. The baseline on the same box: 22
  suites, 162 passed. New and changed suites: `PlantedRecord.t.sol` 9 (new), `HlAdminHandover.t.sol` 15 → 18,
  `HlAdminSetup.t.sol` 21 → 20 (the stranger's dispatch in, the two tests of checks 32–33 out), `SetupTrust.t.sol` 6
  (scenario d rewritten).
- `FOUNDRY_PROFILE=factor8 forge test --hardfork osaka`: the same **173 passed, 0 failed, 0 skipped**.
- HlAdmin's invariants deeper than the profile's (`FOUNDRY_INVARIANT_RUNS=512 FOUNDRY_INVARIANT_DEPTH=128`, 65,536
  calls): pass.
- **Mutants of this fold** (a script over a scratch copy, not in the tree: each removes or weakens one guard, and the
  named test must fail; every target test passed on the pristine sources before and after): **24 of 24 caught** —
  either floor, the stored floor alone (a successor's), the immutable floor alone, the ceiling, `handle`'s clearing of
  `deliveredBlock`, of the params' length word, the cancel's guard and the view back to the bare record, each of check
  34's three conditions, each of `successorInit`'s five writes (pending, ranges, holds, `setupDone`, the start), its
  Exec check, the delegatecall's Exec check, the launch line unrecorded, the bindings unchecked, one binding (`hlgov`)
  not asked, `token(1)` and `route(1)` not asked, a failed binding call hashed instead of reverted, and kind 8's
  implementation check.
- `forge build --sizes` (the default and factor-8 profiles give the same sizes):

| Contract | Runtime | Margin | Initcode |
|---|---|---|---|
| HlAdmin | 18,954 (+1,246: E2-RT1's window and inheritance +509, the bindings view +77, the Exec hash +162, the review's three +498) | 5,622 | 47,175 (+2,011, HlAdminExec's creation code inside; with `Config`'s 896 bytes of arguments 1,081 under 49,152) |
| HlAdminExec | 22,847 (+681: check 34 +130, the bindings +634, checks 32–33 −290, kind 8's implementation +207) | 1,729 | 24,385 |
| GnoLightClient, Ed25519Verifier, PausableGate | unchanged (21,150; 7,007; 13,193) | | |

- `forge test --match-contract GasReport --hardfork osaka -vv`, both trees (exec; the rest of §6 and F4.8 unchanged):

| What | Before (`00d97c09`) | After | Why |
|---|---|---|---|
| process + HlAdmin.handle, a kind 7 recorded | 306,739 | 307,335 | the record cleared first (two words) |
| process + HlAdmin.handle, a cancel deleting one | 227,514 | 232,511 | the same, on cold slots; the window read |
| execute kinds 2, 3, 4, 5, 6, 7, 12 | 61,214–158,438 | +719 to +741 each | the window, both floors; the Exec's code hash (its account then warm) |
| execute kind 8 (the gate) | 106,263 | 109,064 | `getProxyImplementation` |
| execute kind 9 (eight authorities, successorInit) | 411,115 | 442,010 | sixteen bindings, two calls each; the whole record written; the successor's Exec check |
| deploy step 7 (HlAdmin and HlAdminExec) | 8,970,459 | 9,383,646 | 1,927 more bytes of runtime deposited |
| deploy step 11 (finishSetup) | 278,514 | 278,622 | checks 32–33 out; 34 and the launch line in |
| the deployment's eleven steps | 36,641,678 | **37,054,973** | ≈36.6M → ≈37.1M |

- `forge lint` on the four changed sources: 45 findings before, 46 after; the new one is `require-revert-in-loop` on
  the bindings loop (a call that fails must revert the execution, ER7).
- `forge snapshot --no-match-contract Ed25519Differential` (F4.8's command; the committed file matches it without
  `--hardfork osaka`, and differs with it): regenerated for 156 tests; `--check` passes. The diff covers every HlAdmin
  test, every test that deploys, the new tests, and the means of the fuzz tests (same seed, other samples now that the
  code changed, as in F2.6). If other lanes change the file too, regenerate it after the merge rather than merging it.

## Fold F8 — kind 9 compares the twelve bindings that grant power (2026-10-14)

Lane F8, branch `eth-f8` from `bridge-eth` at `be9d5e92`, before audit round 3. It folds BUILD.md's "From F5" item
(F5.7 item 5, F5.9 item 1), as decided: kind 9 no longer compares the successor's verifier, its code hash, the hlmailbox
path hash or the two routes — bindings that grant nothing and that only a successor's own finishSetup reads. Commits:
`897f9f7c` (the tests, failing), `32ea235c` (the contracts), `36032df2` (hlwatch's kind 9 reading), `31a14ec2`
(hlwatch's kind 8 client verifier: the reader the change leaves unchained, F8.4), then the independent review's
(F8.5): `39d7f231` (its cases as tests, failing over `31a14ec2`), `c2725d4d` (hlwatch), `e5fa85cc` (two doc comments);
then this section and the regenerated `.gas-snapshot`. Where this section and Fold F5 (F5.1 items 6 and 11, F5.4,
F5.9) or M3b-NOTES.md's Fold F7 (F7.1 items 3 and 9) differ, this section holds.

### F8.1 Interface changes (the Go lanes: read these first)

1. **Kind 9's `"bindings"`** asks the successor and HlAdmin eleven views, the whole return data compared: `mailbox()`,
   `client()`, `gate()`, `proxyAdmin()`, `merkleTreeHook()`, `protocolFee()`, `hlgov()`, `origin()`, `localDomain()`,
   `token(1)`, `token(0)`. With the HlAdminExec the successor's runtime pins (its `successorInit` refuses another,
   `ForeignExec`, F5.3) these are the twelve bindings that grant power. `ed25519()`, `ed25519CodeHash()`,
   `mailboxPkgPathHash()`, `route(0)` and `route(1)` are no longer asked: a successor holds whatever its proposer gave
   (its constructor still refuses a zero verifier, code hash or route), and only its own finishSetup reads them (checks
   15 and 26–28, against the live client and tokens). A view of the eleven that reverts still reverts the execution,
   which pends (ER7); one of the five that reverts no longer matters.
2. **No ABI change.** No function, event or error moved: `ethx/abi/gen.sh` regenerated every file byte for byte. HlAdmin
   keeps all sixteen views, `mailboxPkgPathHash()` among them (hlwatch prints it; `hleth audit` expects its immutable).
3. **hlwatch's kind 9 reading** (`agent/targets.go`): the sixteen are printed beside this HlAdmin's, the eleven first. A
   difference in one of the eleven is, as before, "THE SUCCESSOR IS BOUND ELSEWHERE (…): HlAdmin spends it
   "bindings"", the verdict MUTABLE: would be spent. A difference in one of the five reads "… (this HlAdmin's …) differs
   (finishSetup's only; not checked)" — in the views, in the successor's immutables (`HlAdmin.ed25519`,
   `.ed25519CodeHash`, `._route0`, `._route1`, `.mailboxPkgPathHash` and its HlAdminExec's `ed25519`, `ed25519CodeHash`,
   `route0`, `route1`, `mailboxPkgPathHash`) and in its constructor's arguments — never spent and never a reason for
   NOT SHOWN IMMUTABLE; in a kind 8's build those names are held whole like any other. Then one line reads the
   successor's own finishSetup against the live deployment, for those five: "its own finishSetup on the live
   deployment, for the five kind 9 does not compare (said, not a verdict): passes 15 (…), 26 (…), 27 (…), 28 (…)" or
   "… FAILS 27 (ed25519() … is not the client's ED25519() …) …" — each route against its token's router for gno.land,
   the path hash and the verifier against the client's, the verifier's code against the code hash. A read of the
   sixteen that fails says HlAdmin's execute reverts while one of the eleven does not answer.
4. **hlwatch's kind 8 reading** (F8.4): a client implementation's `GnoLightClient.ED25519` immutable and its
   constructor's `ed25519_` are held to the live client's `ED25519()`, no longer to HlAdmin's `ed25519()`.
5. **Gas** (F8.7): kind 9 −7,237 (434,773); the deployment −11,279 (≈37.04M).

### F8.2 The change, and why it is safe

`HlAdminExec._sameBindings` keeps F5's form — the selectors in one memory array, both calls through `_answer` — over
eleven: a `uint32[11]`, the nine plain views, then `token(i & 1)` for the last two (`token(1)`, then `token(0)`). Its
comment says what is compared and why the five are not; `IHlAdmin`'s comments on the five, `HlAdmin.Config`'s and
`finishSetup`'s say so too.

The five grant nothing. HlAdmin stores them as immutables, answers them as views and hands them to HlAdminExec, which
reads them in `checkSetup` alone: check 15 (each token's `routers(origin)` against the route), 26 (the client's
`mailboxPkgPathHash()`), 27 (the client's `ED25519()`) and 28 (that verifier's code and EXTCODEHASH). The client runs
its own `ED25519` and path hash (immutables of its implementation, which only a kind 8 changes), a route's standing is
the token's `routers` entry (which only a kind 6 moves), and nothing in `src/` calls HlAdmin's copies; nothing on chain
reads `setupDone` but `finishSetup` itself. A successor bound to other values there can take nothing a successor bound
to HlAdmin's could not (`test_kind9_aSuccessorsOwnVerifierGrantsNothing`: a successor naming a verifier that accepts
every signature, and the client still refuses strangers' signatures). And after a verifier swap or a path change
(kind 8) or a route moved (kind 6), HlAdmin's own are stale — immutables no kind moves — so the successor bound to them,
the only one kind 9 accepted, failed its finishSetup's check 27, 26 or 15 for good (shown for each), while one bound to
the live values, the only one whose setup could pass them, was spent "bindings". (Check 24 makes the setup of any
successor fail once a token has a supply: F8.6 item 1.)

A reading: `localDomain()`, kept among the twelve as decided, is also read by finishSetup alone (check 29; `handle`
checks the origin, not the local domain). It cannot go stale — Ethereum's domain never moves, and a kind 8 that changed
the mailbox's would refuse every message to Ethereum — so comparing it costs its two calls and nothing more.

HlAdminExec's runtime is 52 bytes smaller (22,847 → 22,795), and so is HlAdmin's initcode, which carries HlAdminExec's
creation code (47,175 → 47,123): with `Config`'s 896 bytes of arguments HlAdmin's creation transaction carries 48,019
bytes, 1,133 under EIP-3860's 49,152 (1,081 before).

### F8.3 The proof: fails before, passes after

Before: the tests over `be9d5e92`'s contracts and Go (on the devbox, `/root/src/eth-f8-before` and
`/root/src/eth-f8-base`), and the review's Go cases over `31a14ec2`'s hlwatch (`/root/src/eth-f8-gobefore`); after:
the head. Logs in `/root/src/eth-f8-logs/` (`before-handover.log`, `before-handover2.log`, `before-trace.log`,
`before-trace2.log`, `before-go.log`, `before-go2.log`, `before-go3.log`).

| Test | Before | After |
|---|---|---|
| `test_kind9_aSuccessorAfterAVerifierSwapExecutes` (HlAdminHandover.t.sol: a kind 8 moves the client onto `Ed25519VerifierV2`, other code at another address; in a snapshot, a successor bound to HlAdmin's stale verifier; then one bound to the live one) | FAIL "DecisionSpent != expected DecisionExecuted": `DecisionSpent(2, 9, "bindings")` for the successor bound to the live verifier. The one bound to the stale verifier took every authority (`Retired`) and its finishSetup reverted `SetupIncomplete(27)`: no successor could both take the hand-over and finish its setup | executes: every authority the successor's, its finishSetup passes, and a kind 12 proven through the new verifier runs on it |
| `test_kind9_aSuccessorAfterAPathChangeExecutes` (the kind 8 to a client over another hlmailbox path and both kind 9s decided together: proofs name the new path afterwards) | FAIL: the stale-bound successor took the hand-over and its setup reverted `SetupIncomplete(26)`; the live-bound one `DecisionSpent(3, 9, "bindings")` | the stale-bound one as before (in a snapshot); the live-bound one executes and its setup passes |
| `test_kind9_aSuccessorAfterARouteMovedExecutes` (a kind 6 enrolls another route realm for gno.land on token 0) | FAIL: the stale-bound successor's setup reverted `SetupIncomplete(15)`; the live-bound one `DecisionSpent(2, 9, "bindings")` | the stale-bound one as before; the live-bound one executes and its setup passes |
| `test_kind9_theSetupOnlyBindingsAreNotCompared` (five successors of HlAdmin's code, one of the five elsewhere in each, each in its own snapshot) | FAIL at the first, another verifier: `DecisionSpent(1, 9, "bindings")` | each takes every authority |
| `test_kind9_aSuccessorsOwnVerifierGrantsNothing` (the review's: a successor naming a verifier that accepts every signature) | FAIL: `DecisionSpent(1, 9, "bindings")` | it takes every authority; a header signed by strangers' keys is refused (`SignatureMismatch`: the client verifies with its own); a decision the chain signed runs on it; its own setup refuses the verifier it named (`SetupIncomplete(27)`) |
| `test_kind9_aSuccessorBoundElsewhereIsSpent` (now eleven: another mailbox, client (an ISM that verifies everything), gate, token 0 or 1, ProxyAdmin, either hook, local domain, origin or r/hlgov) | passes (it was sixteen) | eleven spent "bindings", nothing moved; one bound as HlAdmin is takes everything |
| Go `TestSuccessorAfterAVerifierSwap` (agent/f8_test.go: a successor bound to the live verifier — views, immutables, arguments, its HlAdminExec's — then bound to the stale one, then under another code hash, then all five elsewhere, another r/hlgov beside them, a power binding in its code or creation, a route in its code, and the live path and route after they moved) | over `be9d5e92`: FAIL "MUTABLE: would be spent (THE SUCCESSOR IS BOUND ELSEWHERE (ed25519() 0x…E256, not this HlAdmin's 0x…E255; ed25519CodeHash() …): HlAdmin spends it "bindings" …)", and `HlAdmin.ed25519 = 0x…E256, but it must be 0x…E255 … DIFFERS`; over `31a14ec2`: FAIL, no reading of its own setup | each of the five "differs (finishSetup's only; not checked)", immutable; its setup read passing 15, 26, 27, 28 when bound to the live values and FAILING 27 and 28 when bound to the stale verifier (28 too under another code hash; 15 and 26 with routes and path elsewhere); another r/hlgov beside them spent, named alone; r/hlgov in its code or creation NOT SHOWN ("but it must be"); a route in its code only said to differ |
| Go `TestClientImplementationHeldToTheLiveVerifier` (F8.4, and the review's kind 8 build whose immutable bears a successor's setup-only name) | over `be9d5e92`: FAIL, a client upgrade that keeps the live verifier after a swap read "NOT SHOWN IMMUTABLE (GnoLightClient.ED25519 = 0x…E256, but it must be 0x…E255 (our Ed25519Verifier, HlAdmin's ed25519()); its argument ed25519_ = 0x…E256 has no expected value here)"; over `31a14ec2`: FAIL, `HlAdminExec.ed25519 = 0x…BAd0 (this HlAdmin's ed25519(): 0x…E255) differs (finishSetup's only; not checked)` in a kind 8's build | immutable; an upgrade back to HlAdmin's stale verifier, or to the verifier a successor named, NOT SHOWN with the verifier named beside the live one; the kind 8 build's `HlAdminExec.ed25519` held whole ("but it must be") |

**Mutants** (`/root/src/eth-f8-logs/f8mutants.py`: a fresh copy of the head per mutant, every named test passing on the
pristine copy first; `mutants.log`): **32 of 32 caught** on the head (the first run, on `31a14ec2`, 27 of 27:
`mutants-run1.log`). Contracts (16): each of the eleven views dropped from the loop (`if (i == k) continue;`) —
`test_kind9_aSuccessorBoundElsewhereIsSpent` fails, "DecisionExecuted != expected DecisionSpent"; each of the five put
back (`ed25519`, `ed25519CodeHash`, `mailboxPkgPathHash`, `route(0)`, `route(1)`) —
`test_kind9_theSetupOnlyBindingsAreNotCompared` fails, and with it the verifier-swap test (the first two), the
path-change test (the third) or the route test (the fourth). Go (16): hlwatch spending a difference in each of the five
again (five mutants), the five immutables or the five arguments held strictly, another r/hlgov or another client no
longer spent, the soft reading in a kind 8's too, the setup reading holding the verifier, a route or the path to
HlAdmin's instead of the live deployment's, or taking any code at the verifier — `TestSuccessorAfterAVerifierSwap`,
`TestSuccessorHeldToItsBuildAndBindings` or `TestClientImplementationHeldToTheLiveVerifier` fails; a kind 8 client's
verifier held to HlAdmin's `ed25519()` again, or its `ed25519_` argument left without an expected value again —
`TestClientImplementationHeldToTheLiveVerifier` fails.

### F8.4 Every reader of the five (the safety audit a removed safeguard needs)

Kind 9's equality was a safeguard; what leaned on it, read in the tree (and confirmed by the review, F8.5):
- **On chain**: nothing but each HlAdmin's own `checkSetup` (F8.2).
- **The machines and the realms**: none. The machines take the routes from their config (M3-NOTES.md: `route(i)` is not
  used); the realms never read HlAdmin (r/hlgov's kind 6 builder reads hlmailbox's route registry).
- **`hleth audit`** (`cmd/hleth/audit.go`: `route(i)`, `ed25519()` and `ed25519CodeHash()` at lines 384, 479 and 489):
  the recorded launch HlAdmin against its record — the launch's, which no kind 9 wrote. Unaffected.
- **hlwatch, kind 9** (F8.1 item 3).
- **hlwatch, kind 8 — unsafe, fixed (`31a14ec2`).** `expectations` held a client implementation's
  `GnoLightClient.ED25519` to HlAdmin's `ed25519()`: a trusted value only while kind 9 chained every successor's to the
  launch's audited verifier. Now a successor's is its proposer's. In two steps: a successor naming a verifier M (read
  "differs (finishSetup's only; not checked)", harmless on chain), then a client upgrade over M, which hlwatch would
  have called "GnoLightClient.ED25519 = M ok (our Ed25519Verifier, HlAdmin's ed25519())" — and the client would accept
  whatever M accepts. The verifier is now held to the live client's `ED25519()` (`liveFacts.Verifier`, read since F7
  and never used), as the client's path hash already was to the live client's (`liveFacts.PkgPath`): an upgrade that
  keeps the verifier is vouched — after a swap too, where HlAdmin's is stale and every later client upgrade read
  DIFFERS — and any other verifier is a swap, NOT SHOWN IMMUTABLE, read on its own. **Found with it (F7's)**:
  `argExpectations` held the client's constructor argument under the key `verifier` (E-M2b's first parameter name,
  `verifier_`), and the client's is `ed25519_`: since F7 every client implementation given its creation transaction
  read NOT SHOWN IMMUTABLE ("its argument ed25519_ … has no expected value here"), so hlwatch could vouch for no client
  upgrade. The key is `ed25519` now (`verifier` kept for the old name), the live verifier for both.
- **A successor's `setupDone`** (the review's finding 6): before F8 its checks 15 and 26–28 held the live client and
  tokens to values chained to the launch's; now to its proposer's. So it says only that those five match what runs, and
  — `checkSetup` stopping at its first failure — a successor whose 15 or 27 fails is never read past it. Nothing acts on
  it (the monitor displays it). Recorded as a rule: a reference for the verifier's code is the launch HlAdmin's
  `ed25519CodeHash()` (the first of hlwatch's line of HlAdmins, which `hleth audit` holds) or the build, never a
  successor's view.

### F8.5 The independent review (an adversarial agent over `be9d5e92..31a14ec2`, reading the tree)

No on-chain flaw: the five grant nothing (every use cited), `_sameBindings` compares `token(1)` and `token(0)` and each
of the eleven alone, ER7 holds, the verdict order holds, and the new `ed25519` key is right for all four kind 8 targets.
Eight findings, all Low or Info; folded: 1, 2, 4, 5, 7 and the code comments of 8; the rest are the Go lanes' or the
coordinator's (F8.6):
1. **Low — hlwatch held the five to HlAdmin's stale values, inverting its word in the very case F8 is for**: after a
   swap the successor bound to the live verifier read "differs" and the dead-end one "ok". **Folded** (`c2725d4d`): the
   brief's print beside HlAdmin's stays, and a line reads the successor's own setup against the live deployment (F8.1
   item 3), said and never a verdict.
2. **Low — `finishSetupOnly` was keyed by name alone, so a kind 8 build whose reviewed source holds a contract named
   `HlAdminExec` with an immutable `ed25519` would read soft** (and the verdict IMMUTABLE over a verifier its deployer's
   EIP-7702 account could re-point). **Folded**: the soft reading is a kind 9's alone; a kind 8's build is held whole.
3. **Low — `ethx.ReadBindings` reads the sixteen all or nothing**: a successor missing one of the five views (a pre-F5
   build has no `mailboxPkgPathHash()`) hides the eleven, and hlwatch says the decision pends where HlAdmin would now
   execute it (NOT SHOWN all the same: fail-closed). Not folded: `ethx` is outside this lane's files (F8.6 item 7).
4. **Low — the Go tests pinned only the soft side**. **Folded** (`39d7f231`): a power binding in the successor's code
   or creation NOT SHOWN, a route in its code soft, the live path and route after they moved, the verifier's code hash.
5. **Low — the Solidity tests showed the five uncompared, not powerless**. **Folded**: the always-true verifier test,
   and the stale successor's setup failing 26 and 15 beside 27.
6. **Info — a successor's `setupDone` no longer certifies reviewed values**: recorded (F8.4).
7. **Info — the regenerated `.gas-snapshot` was not committed**: it is, with this section.
8. **Info — text now wrong**: `HlAdmin.Config`'s and `finishSetup`'s doc comments folded (`e5fa85cc`); the rest in F8.6.

### F8.6 Findings for the other lanes, and where the plan and the notes are now wrong

1. **A successor's finishSetup fails check 24 once either token has a supply** (for round 3's contracts fold).
   `_checkToken` requires `totalSupply() == 0` of a successor too; only 30, 31 and 34 are a launch's. In a live bridge
   every successor's setup therefore fails for good at check 24, whatever its bindings: F8 makes checks 15 and 26–28
   passable, not the setup (the tests above mint nothing). Check 12 fails too after a kind 3 to the zero ISM, until a
   kind 3 sets the gate back. Nothing on chain reads a successor's `setupDone`; hlwatch's status prints it. Proposed:
   check 24 joins the launch values (a few bytes of HlAdminExec, which has 1,781 left).
2. PLAN.md §6.5: "(mailbox, client, gate, tokens, `ProxyAdmin`, hooks, verifier, r/hlgov, the origin and local domains,
   the routes, the mailbox's path — sixteen views, each compared)" → "(mailbox, client, gate, tokens, `ProxyAdmin`,
   hooks, r/hlgov, the origin and local domains — eleven views, each compared; with the `HlAdminExec` its code pins,
   the twelve that grant power; not the verifier, its code hash, the routes or the mailbox's path, which only its own
   `finishSetup` reads and which a kind 8 or 6 leaves stale in this `HlAdmin`)"; its sentence on what `hlwatch` prints
   gains: a kind 8's client implementation is held to the live client's verifier (F8.4).
3. PLAN.md §13, kind 9: "and its sixteen bindings are this contract's" → "and its eleven bindings that grant power
   (mailbox, client, gate, tokens, `ProxyAdmin`, hooks, r/hlgov, both domains) are this contract's".
4. PLAN.md §5.8 step 11 ("the client's verifier `HlAdmin`'s") and §9's row on what finishSetup checks: for a successor
   these are its proposer's values (F8.4). §2: "kind 9 432,364" falls by 7,237 (F8.7); "≈37.1M" holds (≈37.04M).
5. This file: F5.1 item 6 and F5.4 (sixteen → eleven; F5.4's "All sixteen fit, so nothing of the brief's list is left
   to `hlwatch` on chain"); F5.1 item 11 (a successor's `Config` with "the live HlAdmin's sixteen bindings": the five
   from the live client and tokens, for its own setup); F5.7 item 5 and F5.9 item 1 (closed: dropped); F5.8 items 2–3
   (the PLAN text they propose lists the five); F5.9 item 3 and F5.10's sizes (1,081 → 1,133 under 49,152) and gas row.
6. M3b-NOTES.md Fold F7: F7.1 item 3 (the successor's immutables held to "this HlAdmin's sixteen bindings", a mismatch
   in any "THE SUCCESSOR IS BOUND ELSEWHERE"; the client's ED25519 held to HlAdmin's) and item 9 ("hlwatch's kind 9
   compares all sixteen bindings"); F7.3 item 3 is done.
7. **`ethx`** (the Go lanes): `ReadBindings` should read the eleven as one required batch and the five as an optional
   one (the review's finding 3); the comments on `Bindings` (`ethx/contracts.go:475`: a successor "must answer the same
   as the HlAdmin it succeeds") and on `ReadBindings` ("mailboxPkgPathHash only when the embedded ABI has the view": it
   is always read, since F5).
8. **r/hlgov** (the realms' fold): the kind 9 builder's doc (`eth/realm/hlgov/hlgov.gno:944–953`) lists HlAdmin's
   refusals without the bindings at all (true since F5).
9. BUILD.md's "From F5 (for round 3's contracts fold)" item: done.

### F8.7 The run

On `root@<devbox>:/root/src/eth-f8`, synced from the worktree by BUILD.md's rsync (the brief's form); Foundry
1.8.5, solc 0.8.33, Go 1.27.1. The baseline is `be9d5e92`'s tree on the same box (`/root/src/eth-f8-base`). Scripts and
logs in `/root/src/eth-f8-logs/` (`baseline.sh`, `gates.sh`, `final.sh`, `f8mutants.py`; the first run's logs kept as
`*-run1.*`).

- `forge fmt --check`: clean. `forge build --force`: the same two compiler warnings as before.
- `forge test --hardfork osaka`, with the differential set generated (`go run ./tools/ed25519vectors diff`, 118,392
  records in the 16 lanes): **23 suites, 178 tests: 178 passed, 0 failed, 0 skipped** (the baseline on the same box:
  173). `FOUNDRY_PROFILE=factor8 forge test --hardfork osaka`: **178 passed, 0 failed, 0 skipped** (173).
  `HlAdminHandover.t.sol` 18 → 23.
- `forge build --sizes` (the default and factor-8 profiles give the same sizes):

| Contract | Runtime | Margin | Initcode |
|---|---|---|---|
| HlAdmin | 18,954 (unchanged) | 5,622 | 47,123 (−52: HlAdminExec's creation code inside; with `Config`'s 896 bytes of arguments 48,019, 1,133 under 49,152) |
| HlAdminExec | 22,795 (−52) | 1,781 | 24,333 (−52) |
| GnoLightClient, Ed25519Verifier, PausableGate | unchanged (21,150; 7,007; 13,193) | | |

- `forge test --match-contract GasReport --hardfork osaka -vv`: execute kind 9 442,010 → **434,773** (−7,237: five pairs
  of view calls fewer); deploy step 7 9,383,646 → 9,372,367; the deployment's eleven steps 37,054,973 → **37,043,694**
  (−11,279: the 52 bytes, and the metadata hashes' bytes); every other line unchanged.
- `forge lint src/admin/HlAdminExec.sol src/interfaces/IHlAdmin.sol`: 12 findings before, the same 12 after.
- `forge snapshot --no-match-contract Ed25519Differential` (without `--hardfork`, as F5 found): regenerated for 161
  tests; `--check` passes. If other lanes change the file too, regenerate it after the merge rather than merging it.
- Go: `ethx/abi/gen.sh` regenerated every ABI byte for byte; `gofmt -l` nothing; `go vet ./...` clean;
  `go test -race -count=1 -v ./...` with `ETHX_CONTRACTS_OUT` and `HLETH_CONTRACTS_OUT` at `forge build --force --ast` of
  this tree (`/root/src/eth-f8-build/out`) and `HLETH_HYPERLANE_DIR` at Hyperlane 12.2.0's release compile: **177 tests
  pass (and 15 subtests), none fail, none skip** (F7's 175): agent 103 (+2, F8.3), cmd/hleth 21, cmd/hlethbridge 1,
  cmd/hlwatch 2, ethx 44, gnoside 6. Every real-contract test ran.
- The mutants of F8.3: 32 of 32 caught, after the gates and the e2e (`final.sh`).
- **The e2e** (`BL=/root/.cache/eth-f8-e2e sh local/e2e.sh`, the head's tree): **PASS in 379 s** (2026-10-14
  20:57:35–21:03:54 UTC), `hlwatch status` GREEN. Log `/root/src/eth-f8-logs/e2e.log`; the setup's, the audit's and
  the machines' logs, `deployment.json`, `bridge.json` and the alarms kept in `/root/src/eth-f8-logs/e2e-state/` (the
  next run wipes `BL`). The setup sent 31 transactions and its second run none; the audit made 193 RPC calls and gave
  261 "ok" lines, no WARN, no FAIL; the alarms were two lines, the monitor's `deliveries:paused` while the flag stood
  and its clear. Section times: the build 3 s; the contracts 46 s; the keys, anvil and gnodev 6 s; the setup and its
  second run 32 s; the audit 1 s; the realms 5 s; the machines 4 s; gno.land → Ethereum 10 s; Ethereum → gno.land 11 s;
  the stranger 8 s; the identity 2 s; the decision 154 s (its 150 s); the cancel 30 s; the flag 13 s; the unpauses 47 s;
  the send after 3 s; hlwatch 4 s. (Another lane's e2e, on its own ports, started at 21:00 and overlapped the last four
  minutes.)

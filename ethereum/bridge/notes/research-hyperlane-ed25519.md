# Hyperlane EVM and ed25519 in Solidity: verified facts for the gno.land ↔ Ethereum bridge

Researched 2026-10-14 for the gno.land ↔ Ethereum bridge: our own deployment of Hyperlane's EVM Mailbox and HypERC20, unchanged, plus our own Solidity light client of gno.land, a pausable ISM and an admin contract. Every fact names the primary source read today. Hyperlane code is quoted from tag `@hyperlane-xyz/core@12.2.0`, commit `ae1dfdd13be888f5015040214a907b19e371464f`, read from a shallow sparse checkout. Paths are relative to `solidity/` and line numbers are from that tree. UNVERIFIED marks anything not confirmed, with the reason. The full list of sources is at the end.

## Part A: Hyperlane EVM

### 1. Release and build settings

- **npm:** `dist-tags.latest` = **12.2.0**, published 2026-10-02T11:52Z (Apache-2.0, with npm SLSA provenance). The two releases before it were 12.1.0 (2026-08-17) and 12.0.0 (2026-08-04).
- **GitHub:** the release `@hyperlane-xyz/core@12.2.0` is an annotated tag (`f62e797b`) pointing at commit `ae1dfdd1`, "chore: release npm packages (#9438)". The SDK and CLI have their own numbering (45.0.x).
- **Compiler:** `foundry.toml` L8-10 and `rootHardhatConfig.cts` L11-19 agree on **solc 0.8.33, evm `cancun`, optimizer on, 10,000 runs**. The one exception is `CrossCollateralRouter.sol`, pinned to 3,599 runs to stay under the EIP-170 size limit. Releases are built with Hardhat (`package.json` L74), so the released bytecode carries the default CBOR metadata (ipfs hash). The CHANGELOG for 12.0.0 records the move from 5,800 to 10,000 runs.
- **The version is part of the bytecode:** `PackageVersioned.sol` L10 sets `PACKAGE_VERSION = "12.2.0"`. `bytecodeversion.sh` rewrites it on every build, and Mailbox and the routers inherit it, so no two releases can match byte for byte.
- **Reference build input:** the tarball includes `dist/buildArtifact.json`, written by `exportBuildArtifact.sh` L31 as `{input, solcLongVersion}`. I read it through unpkg. It holds `solcLongVersion` `0.8.33+commit.64118f21`, settings `{evmVersion: cancun, optimizer: {enabled: true, runs: 10000}}` and 343 sources.
- **OpenZeppelin:** contracts and contracts-upgradeable **4.9.3** (`soldeer.lock` revs `fd81a96f…` and `3d4c0d57…`).

### 2. Mailbox.sol (450 lines)

- **Shape:** `Mailbox is IMailbox, Indexed, Versioned, OwnableUpgradeable, PackageVersioned` (L19-25). The constructor takes only `uint32 _localDomain`, which is immutable. `initialize(address _owner, address _defaultIsm, address _defaultHook, address _requiredHook)` (L88-99) calls the three setters, each of which requires a contract address, then calls `transferOwnership(_owner)`. `VERSION = 3` (`upgrade/Versioned.sol` L9).
- **process(), in order** (L202-246):
  1. version must equal 3 (L209);
  2. destination must equal `localDomain` (L210-213);
  3. `id = keccak256(message)`, which must not already be delivered (L216-217);
  4. `ism = recipientIsm(recipient)` (L220-221);
  5. `deliveries[id] = {msg.sender, uint48(block.number)}` (L225-228);
  6. emit `Process(origin, sender, recipient)` and `ProcessId(id)` (L229-230);
  7. `require(ism.verify(_metadata, _message), "Mailbox: ISM verification failed")` (L235-238);
  8. `IMessageRecipient(recipient).handle{value: msg.value}(origin, sender, body)` (L241-245).

  The mark comes before verify, so a re-entrant replay fails (IMailbox L51-56: `processedAt(id) == block.number` while the ISM runs). No other reentrancy guard.
- **recipientIsm** (L410-431): a low-level `staticcall` of `interchainSecurityModule()`, not try/catch. A successful, non-empty, non-zero answer wins; otherwise `defaultIsm`. The code checks only for zero, not that the ISM is a contract.
- **dispatch** (L277-315): a zero hook means `defaultHook`. The message is `version‖nonce‖localDomain‖msg.sender‖destination‖recipient‖body` (77-byte header, `libs/Message.sol` L33-52). It sets `latestDispatchedId`, increments `nonce`, emits `Dispatch` and `DispatchId`, then:

```solidity
uint256 requiredValue = requiredHook.quoteDispatch(metadata, message);
// if underpaying, defer to required hook's reverting behavior
if (msg.value < requiredValue) {
    requiredValue = msg.value;
}
requiredHook.postDispatch{value: requiredValue}(metadata, message);
hook.postDispatch{value: msg.value - requiredValue}(metadata, message);
```

  (L306-312.) The Mailbox refunds nothing itself: any excess goes to the second hook. `quoteDispatch` returns the sum of both hooks' quotes (L335-354). The 3- and 4-argument overloads use `defaultHook`.
- **Owner powers:** `setDefaultIsm`, `setDefaultHook` and `setRequiredHook` (L369-402), plus OZ `transferOwnership` and `renounceOwnership`. **The Mailbox cannot be paused.** Upgrades belong to the proxy admin.

### 3. IInterchainSecurityModule (`interfaces/IInterchainSecurityModule.sol`)

- **`Types` enum (L5-19):** `UNUSED=0, ROUTING=1, AGGREGATION=2, LEGACY_MULTISIG=3, MERKLE_ROOT_MULTISIG=4, MESSAGE_ID_MULTISIG=5, NULL=6` ("used with relayer carrying no metadata"), `CCIP_READ=7, ARB_L2_TO_L1=8, WEIGHTED_MERKLE_ROOT_MULTISIG=9, WEIGHTED_MESSAGE_ID_MULTISIG=10, OP_L2_TO_L1=11, POLYMER=12`.
- **Signatures:** `moduleType() external view returns (uint8)` (L26). `verify(bytes calldata _metadata, bytes calldata _message) external returns (bool)` (L36-39) is **not `view`**, so an ISM may write state during verify.
- **What a custom ISM should return:** every stock ISM with its own logic returns `NULL`: `PausableIsm` L18, `NoopIsm` L8, `TrustedRelayerIsm` L14, `BlacklistIsm` L31, `RateLimitedIsm` L46, and the hook ISMs. Hyperlane's relayer maps `ModuleType::Null` to `NullMetadataBuilder` (empty metadata) and has no builder for `UNUSED` (`rust/main/agents/relayer/src/msg/metadata/message_builder.rs` L240, same commit). So a light-client ISM whose metadata our own relayer builds should return **NULL (6)**. Hyperlane's relayer would then submit empty metadata, which our `verify` must reject.
- **Stock PausableIsm** (L12-42): `Ownable` (not upgradeable) plus OZ `Pausable`. `verify` is `view whenNotPaused` and returns `true`; `pause()` and `unpause()` are `onlyOwner`. It checks nothing by itself and is meant to sit in a `StaticAggregationIsm` beside a real ISM.

### 4. HypERC20 → TokenRouter → GasRouter → Router → MailboxClient

- **Constructor** (`token/HypERC20.sol` L18-25): `(uint8 __decimals, uint256 _scaleNumerator, uint256 _scaleDenominator, address _mailbox)`, all immutable. TokenRouter requires both scales to be greater than 0 (L80-83). MailboxClient (L71-75) requires the mailbox to be a contract and caches `localDomain`.
- **initialize** (L33-45): `(uint256 _totalSupply, string _name, string _symbol, address _hook, address _interchainSecurityModule, address _owner)`. It **mints `_totalSupply` to `msg.sender`** (L43), so a synthetic token passes 0 and initializes in the proxy constructor's data.
- **transferRemote:** one overload, `(uint32 _destination, bytes32 _recipient, uint256 _amount) payable` (`token/libs/TokenRouter.sol` L167-173). It burns amount + fee from the sender (HypERC20 L67-69), scales with `mulDiv` (L581-589), emits `SentTransferRemote(destination, recipient, amount)`, and dispatches to the enrolled router with the router's own `hook` and metadata `formatWithFeeToken(0, destinationGas[dest], msg.sender, feeToken)` (L298-317, L499-511). All of `msg.value` goes to the Mailbox (L275-277).
- **Fees (since 10.0.0, 2025-10-31), off by default:** an optional `feeRecipient` (an `ITokenFee` such as `fees/LinearFee.sol`, `min(maxFee, amount·maxFee/(2·halfAmount))`), whose fee is **minted to it** (`_transferFee` → `_transferTo`, L566-570), and an optional `feeHook` that takes gas in the token (L376-409).
- **_handle** (L623-636): emits `ReceivedTransferRemote(origin, recipient, amount)`, then calls `_mint(recipient.bytes32ToAddress(), _inboundAmount(amount))`. `bytes32ToAddress` now **reverts if the top 12 bytes are not zero** (`libs/TypeCasts.sol` L11-17). Mailbox.process uses the same cast, so such a message can never be delivered.
- **Inbound rule** (`client/Router.sol` L97-105): `handle` is `onlyMailbox` and requires `_sender == routers(_origin)`.
- **TokenMessage** (`token/libs/TokenMessage.sol` L9-22): `abi.encodePacked(bytes32 recipient, uint256 amount[, bytes metadata])`. transferRemote sends the 64-byte form.
- **Owner powers:** `enrollRemoteRouter(s)`/`unenroll…` (Router L45-88), `setDestinationGas` (GasRouter L40-55), `setHook` and `setInterchainSecurityModule` (MailboxClient L90-106; `address(0)` falls back to the Mailbox default ISM), `setFeeRecipient`, `setFeeHook`.
- **No pause, no direct mint:** `_mint` appears only in initialize and `_transferTo` (L43, L79). The owner can still mint indirectly through a permissive ISM or a proxy upgrade.

### 5. Hooks

- **MerkleTreeHook** (`hooks/MerkleTreeHook.sol`): a MailboxClient with an incremental Merkle tree; constructor `(address _mailbox)`. `_postDispatch` (L63-77):
  - requires `msg.value == 0`;
  - requires `_isLatestDispatched(id)`, so only the message the Mailbox is dispatching can be inserted;
  - inserts the id and emits `InsertedIntoTree(bytes32 messageId, uint32 index)`.

  Its quote is 0. Views: `count()`, `root()`, `tree()`, `latestCheckpoint()` (L37-51).
- **ProtocolFee** (`hooks/ProtocolFee.sol`): `Ownable`, not upgradeable. Constructor `(uint256 _maxProtocolFee, uint256 _protocolFee, address _beneficiary, address _owner)` (L53-63), with `MAX_PROTOCOL_FEE` immutable. `setProtocolFee` (up to the max) and `setBeneficiary` (non-zero) are `onlyOwner` (L90-100). Anyone may call `collectProtocolFees()`, which sends the whole balance to `beneficiary` (L105-107). `_postDispatch` requires `msg.value >= protocolFee`, emits `ProtocolFeePaid`, and refunds the excess to the metadata refund address (default: the message sender) (L112-124).
- **Exact payment:** as the `requiredHook`, ProtocolFee only ever receives min(quote, msg.value). With MerkleTreeHook as the second hook, any overpayment reverts the dispatch (MerkleTreeHook L68), so callers must send exactly `quoteDispatch`.
- **No stock no-op hook** exists outside `contracts/test/`. The closest options:
  - `PausableHook`: owner-pausable; while unpaused, `_postDispatch` does nothing, quotes 0, and keeps any value sent (L46-53);
  - `ProtocolFee` with a maximum of 0.

### 6. Hyperlane's own Ethereum mainnet deployment

Read live on 2026-10-14 at block ~26,189,700 through ethereum-rpc.publicnode.com.

- **Addresses** (hyperlane-registry `chains/ethereum/addresses.yaml`, main `d5990db7`, 2026-10-13):
  - Mailbox proxy `0xc005dc82818d67AF737725bD4bf75435d065D239`
  - proxyAdmin `0x75EE15Ee1B4A75Fa3e2fDF5DF3253c25599cc659`
  - merkleTreeHook `0x48e6c30B97748d1e2e03bf3e9FbE3890ca5f8CCA`
  - validatorAnnounce `0xCe74905e51497b4adD3639366708b821dcBcff96`
- **EIP-1967 slots:** implementation `0x7b4d881c122a5e61adcffb56a2e3ce9927d53455` (8,515 bytes); admin = the proxyAdmin above. ProxyAdmin's `getProxyImplementation` and `getProxyAdmin` return the same two addresses.
- **Owner and configuration:**
  - The Mailbox and the ProxyAdmin are both owned by `0x562dfaac27a84be6c96273f5c9594da1681c0da7`, a Safe v1.3.0 with a 6-of-10 threshold.
  - Mailbox state: `localDomain 1`, `VERSION 3`, `nonce 297,377`.
  - `defaultIsm` is `0xDD0998…` and `defaultHook` is the fallbackRoutingHook `0x571f14…`.
  - `requiredHook` is ProtocolFee `0x8B05BF…`, with `protocolFee 0` and `MAX_PROTOCOL_FEE` 1e9 wei.
- **Which release it is:** its immutable `deployedBlock` is 18,422,581 (2023-10-24 20:44:59Z), before npm 3.0.0-beta0 (2023-10-28), and `PACKAGE_VERSION()` reverts. Etherscan: "Exact Match", `v0.8.19+commit.7dd6d404`, 999,999 runs, default EVM (Blockscout: `paris`, flattened by Hardhat 2.16.1 over OZ 4.8.0). Its `process()` reads the same as 12.2.0's, but its `bytes32ToAddress` truncates silently (the revert came in `c55257cf5f`, 2024-10-23). The exact source commit is UNVERIFIED: it is not a tagged release.
- **Consequence for us:** a 12.2.0 deployment cannot match this bytecode, so byte identity must be stated against the npm release: compile `dist/buildArtifact.json` (§1), compare with `eth_getCode` while masking the immutables (`localDomain`, `deployedBlock`), and submit the same JSON to Etherscan or Sourcify for an exact metadata match.
- **Proxies:** `contracts/upgrade/TransparentUpgradeableProxy.sol` and `ProxyAdmin.sol` only re-import OpenZeppelin 4.9.3 (headers "v4.9.0", "v4.8.3"); constructor `(address _logic, address admin_, bytes _data)`.

### 7. Domain ids and the checkpoint format

- **Domain ids** (registry `d5990db7`):
  - Ethereum: `chainId: 1`, `domainId: 1`.
  - Sepolia: `chainId: 11155111`, `domainId: 11155111`, `isTestnet: true`; Mailbox `0xfFAEF09B3cd11D9b20d1a19bECca54EEC2884766`.
- **CheckpointLib** binds a checkpoint to the origin's MerkleTreeHook address (`libs/CheckpointLib.sol` L80-91, L28-47):

```solidity
function domainHash(uint32 _origin, bytes32 _merkleTreeHook) internal pure returns (bytes32) {
    return keccak256(abi.encodePacked(_origin, _merkleTreeHook, "HYPERLANE"));
}
// digest = ECDSA.toEthSignedMessageHash(keccak256(abi.encodePacked(
//     domainHash, _checkpointRoot, _checkpointIndex /*uint32*/, _messageId)))
```

- **Message-id multisig metadata:** `originMerkleTreeHook(32) ‖ root(32) ‖ index(4) ‖ 65-byte signatures` (`isms/libs/MessageIdMultisigIsmMetadata.sol` L12-15). `AbstractMessageIdMultisigIsm.digest` (L29-37) takes the hook address from this metadata.
- **ValidatorAnnounce** uses a different domain: `keccak256(localDomain ‖ mailbox ‖ "HYPERLANE_ANNOUNCEMENT")` (L111-130).
- **For our signers:** on the Ethereum → gno.land path, signers following Hyperlane's format would sign `digest(1, ourMerkleTreeHook, root, index, id)` with secp256k1 under EIP-191.

## Part B: ed25519 on Ethereum

### 8. Solidity implementations

- **No precompile:** Fusaka (mainnet 2025-12-03, EIP-7607) added only the P-256 precompile (EIP-7951). The same fork's **EIP-7825 caps every transaction at 16,777,216 gas**.
- **The behaviour to match:** tm2 calls `golang.org/x/crypto/ed25519.Verify` (`tm2/pkg/crypto/ed25519/ed25519.go` L130-135, gno `21f9abd7`), i.e. Go's `crypto/ed25519`: S must be canonical (< L), R' = [S]B − [k]A is compared byte for byte with `sig[:32]` (cofactorless), and A is decoded with `SetBytes`, which "accepts all non-canonical encodings of valid points" (go1.25.0 `fips140/ed25519/ed25519.go` L328-337, `edwards25519.go` L142-149).

| Library (licence, commit) | SHA-512 | Checks | Gas | Audit |
|---|---|---|---|---|
| **SCL**, get-smooth/crypto-lib `libSCL_EIP6565.sol` (MIT, `d714e98`) | own (`Sha2Ext.sol`) | 0 < S < L (L196); [S]B + [L−k]A in Weierstrass form, compressed, compared with R (L206-215): cofactorless. **A is never decoded:** the caller passes `extKpub` (X, Y, 32-byte key), unbound | measured below | Veridise 2024-09-06 (commit c559657): 2 High, incl. "EdDSA verification is vulnerable to malleability attacks" (fixed: S < L). CryptoExperts: P-256 only. Formal Land: partial Coq proof |
| **NEAR**, Near-One/rainbow-bridge `Ed25519.sol` (GPL-3.0+, `0c6b0cf`) | own, unrolled | **41-byte messages only** (`check(k, r, s, bytes32, bytes9)`); S < L (L1173); A decompressed; `vr == r` (L1728) | "challenge a NEAR block header: approx 700k gas" (README, 2020); used only in optimistic `challenge()` | none found (UNVERIFIED) |
| **chengwenxi/Ed25519** (Apache-2.0, `34d8c57`, solc ^0.6.8) | own `Sha512.sol` | NEAR's checks, any length (L413, L882) | 904,435 L2 gas on Arbitrum (LimeChain; length not stated); "≈ 1m gas" (farcaster-solidity, optimizer off) | none; Lisk's NEAR port was "out of scope" for Trail of Bits (2024-03) |
| javgh/ed25519-solidity (MIT, 2019) | none | curve operations only | "up to 1 250 000 gas for one scalar multiplication" | none |

**SCL, measured today** with `eth_estimateGas` (21,000 base and calldata included) on a deployed copy of SCL on Base (9,454 bytes). Ethereum L1 with that code state-overridden gave identical numbers:

| Message length | SHA-512 blocks | Gas |
|---|---|---|
| 1 byte | 1 | 527,208 |
| 48 to 150 bytes (gnoland-1's 110-byte precommit sign bytes fall here) | 2 | 808,531 to 810,468 |
| 250 bytes | 3 | 1,092,451 |

That is about 280K gas per SHA-512 block, so the 16.78M transaction cap allows about 20 precommits per transaction. The author's claim, "460K (247K from SHA512)" (X, 2024-06-10), was seen only in a search snippet.

**Binding hazard, reproduced:** a signature made with key A2 but hashed with victim key A's 32 bytes made `Verify_LE` return `true`. A client must derive or check X and Y from the stored 32-byte key.

### 9. Zero-knowledge alternatives

**sp1-ics07-tendermint (IBC Eureka).**

- **Repository:** now `cosmos/ibc-contracts` (Apache-2.0; the old repo is archived). Latest Solidity release v3.0.2 (2026-06-18, `39904319`).
- **Gas:** the v3.0.2 README says "About ~230,000 gas is used for each light client verification (groth16)… proof generation takes around 25 seconds". A one-packet `recvPacket` costs ~524,474 (Groth16) or ~608,862 (PLONK). Main's generated tables add a 553,254-gas end-to-end receive. Neither gives an update-only or membership-only figure.
- **Audits:** Zellic, March 2025 (2 Critical, 1 High; scope includes `programs/sp1-programs/**`), and a Sherlock review, Feb–Mar 2025 (1 High, 7 Medium; scope lists `SP1ICS07Tendermint.sol`). The 2026 Zenith and Zellic reports cover other components.
- **gno.land:** not as it stands. The update program decodes protobuf headers with `ibc_proto` and runs `tendermint-light-client-verifier` 0.40 (`update-client/src/main.rs`); membership uses ICS-23; tm2 signs amino bytes. SP1 is a general zkVM, so gno.land needs a new Rust program (our `gno-light-core`) with its own verification key, not a new circuit. It also needs its own client contract; the SP1 verifier gateway can be reused.

**SP1 Helios** (succinctlabs/sp1-helios v1.3.0, 2026-10-06, `0dd8749f`; sp1-sdk 6.8.1; helios 0.12.0).

- **What it proves and outputs:** Ethereum's sync-committee light client (`verify_update`, `apply_update`, `verify_finality_update`, with BLS12-381 checked inside the zkVM). It commits `ProofOutputs {prevHeader, prevHead, prevSyncCommitteeHash, newHead, newHeader, executionStateRoot, executionBlockNumber, syncCommitteeHash, nextSyncCommitteeHash, StorageSlot[] {key, value, contractAddress}}`. "Execution-header mode" adds `executionBlockHash` and `executionReceiptsRoot` for "about 45k gas" more. A `guardian` in `SP1Helios.sol` can swap the program keys.
- **Verifier gas** (SP1 docs): Groth16 "~260 bytes … ~270k gas" (keys from Aztec Ignition plus Succinct's entropy); PLONK "~868 bytes … ~300k gas", with no new trusted setup.
- **Audit:** Zellic, July 2025 (commit `51b1e4aa`): 1 Critical ("No test suite is in place"), 1 Medium. The newer `execution_header.rs` was outside its scope. The SP1 zkVM's audits are in `succinctlabs/sp1/audits/`.

**gno.land side.** The gnoland-1 genesis tag `chain/mainnet` (and v1.2.0) ships `crypto/bn254` (G1Add, G1Mul, PairingCheck in EIP-196/197 layouts), `crypto/keccak256` and `crypto/sha256`, and no BLS12-381. A realm can check SP1's BN254 proofs; the BLS12-381 work stays inside the proof.

## Still unknown

- **Live Mailbox source commit:** which monorepo commit built Hyperlane's live Ethereum Mailbox implementation. Finding it means compiling candidate commits around 2023-10-24 with solc 0.8.19, 999,999 runs and `paris`, then comparing bytecode.
- **Reproducing 12.2.0:** whether compiling `buildArtifact.json` gives byte-identical code. I installed no toolchain, so this was not done; it belongs on the devbox.
- **Hyperlane audit coverage:** Hyperlane's docs list audits by title only: Trail of Bits "Protocol v3" (Sep 2023), ChainLight "Hook and ISM" v3.1 and "Q2 2025", among others. Which of them cover 12.2.0's TokenRouter fees and the TypeCasts revert is not established. The 12.0.0 CHANGELOG cites a finding "HL-2026Q2-001", but I have not found that report.
- **SCL gas:**
  - the author's measurement method;
  - gas per transaction receipt (I used `eth_estimateGas`, which is an upper bound);
  - whether SCL changed after commit c559657, the version Veridise audited.
- **ed25519 audits:** no audit was found for NEAR's or chengwenxi's ed25519 code.
- **Cost on gno.land:** the gas of an SP1 Groth16 or PLONK verify in a gno realm. No Gno port exists, and the metering of the bn254 natives was not measured.
- **SP1 for tm2:** the SP1 cycle count and proving cost of a tm2 update program.

## Sources

- **Hyperlane monorepo** `ae1dfdd13be888f5015040214a907b19e371464f` (tag `@hyperlane-xyz/core@12.2.0`):
  - `solidity/`: foundry.toml, rootHardhatConfig.cts, hardhat.config.cts, package.json, soldeer.lock, CHANGELOG.md, PackageVersioned.sol, Mailbox.sol, interfaces/IMailbox.sol, interfaces/IInterchainSecurityModule.sol, libs/{Message,TypeCasts,CheckpointLib}.sol, client/{MailboxClient,Router,GasRouter}.sol, token/HypERC20.sol, token/libs/{TokenRouter,TokenMessage}.sol, token/fees/LinearFee.sol, hooks/{MerkleTreeHook,ProtocolFee,PausableHook}.sol, hooks/libs/{AbstractPostDispatchHook,StandardHookMetadata}.sol, isms/{PausableIsm,NoopIsm,TrustedRelayerIsm}.sol, isms/multisig/ValidatorAnnounce.sol, isms/libs/MessageIdMultisigIsmMetadata.sol, upgrade/*;
  - `rust/main/agents/relayer/src/msg/metadata/message_builder.rs`;
  - GitHub commits API for `solidity/contracts/libs/TypeCasts.sol`.
- **npm:** `npm view @hyperlane-xyz/core` (versions, dist-tags, time); `https://unpkg.com/@hyperlane-xyz/core@12.2.0/dist/buildArtifact.json`.
- **hyperlane-registry** `d5990db7af639dda270539f0c07986bd20e21d56`: `chains/{ethereum,sepolia}/{addresses,metadata}.yaml`.
- **Ethereum RPC** `https://ethereum-rpc.publicnode.com`: eth_getStorageAt (EIP-1967 slots), eth_call, eth_getCode, eth_getBlockByNumber, eth_estimateGas with state override.
- **Explorers:** Etherscan `https://etherscan.io/address/0x7b4d881c122a5e61adcffb56a2e3ce9927d53455#code`; Blockscout API `eth.blockscout.com/api/v2/smart-contracts/{0x7b4d…, 0x75EE…, 0xc005…}`.
- **Hyperlane audits page:** `https://docs.hyperlane.xyz/docs/resources/audits`.
- **EIPs:** `https://eips.ethereum.org/EIPS/eip-7607` (Fusaka), `https://eips.ethereum.org/EIPS/eip-7825`.
- **gno:** gnolang/gno master `21f9abd7` `tm2/pkg/crypto/ed25519/ed25519.go`; tags `chain/mainnet` and `v1.2.0` `gnovm/stdlibs/crypto/{bn254,keccak256}`; PR #5725.
- **Go:** go1.25.0 `src/crypto/internal/fips140/ed25519/ed25519.go` and `src/crypto/internal/fips140/edwards25519/edwards25519.go`.
- **SCL:** get-smooth/crypto-lib `d714e9824e2e2e44be3c8fd498e0de651ddc9425`: README.md, src/README.md, src/lib/libSCL_EIP6565.sol, src/lib/libSCL_eddsaUtils.sol, test/libSCL_eip6565.t.sol, doc/Audits/VAR_SmoothCryptoLib_240906_V2.pdf, doc/Conferences/ETHCC2024_RenaudDubois.pdf.
- **NEAR:** Near-One/rainbow-bridge `0c6b0cfcb200e69a8d062c259fd3138d9bbfbde6`: contracts/eth/nearbridge/contracts/{Ed25519,NearBridge}.sol, README.md.
- **chengwenxi/Ed25519** `34d8c5701c8f73c4be68f3f6fdd50cba0e40f1af`.
- **LimeChain/stylus-benchmark** `0e90d774` README.
- **pavlovdog/farcaster-solidity** `cbb770d8` README.
- **javgh/ed25519-solidity** `dfdeedd8` README.
- **LiskHQ/lisk-contracts** `c5c680d1` src/utils/Ed25519.sol; Trail of Bits `reviews/2024-03-lisksmartcontracts-securityreview.pdf`.
- **X post** `x.com/RenaudDUBOIS10/status/1800182890447417479`, seen only as a search-result snippet (HTTP 402 on fetch).
- **cosmos/ibc-contracts:** `39904319` (solidity-v3.0.2) README.md and programs/sp1-programs/{Cargo.toml, update-client/src/main.rs}; main `d4196a39` ibc-solidity/benchmarks/{README.md, snapshots/SP1E2E.json} and docs/audits/{2025-03-25-zellic, 2025-04-03-sherlock, 2026-02-20-zenith, 2026-03-zellic}.pdf.
- **succinctlabs/sp1-helios** v1.3.0 `0dd8749f`: README.md, Cargo.toml, primitives/src/types.rs, contracts/src/SP1Helios.sol, program/src/light_client.rs, "audits/SP1 Helios - Zellic Audit Report.pdf".
- **SP1 docs:** `https://docs.succinct.xyz/docs/sp1/generating-proofs/proof-types`; GitHub listing of `succinctlabs/sp1/audits`.
- **Our bridge repo:** `bridge/PLAN.md` §13, the gnoland-1 sign-bytes layout (internal; verified there).

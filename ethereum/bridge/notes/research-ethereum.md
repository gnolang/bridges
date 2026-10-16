# Ethereum facts for the gno.land <-> Ethereum bridge design

Researched 2026-10-14 (UTC) for the gno.land <-> Ethereum bridge (Ethereum runs a light client of gno.land in Solidity; three signers vouch Ethereum -> gno.land burns until a proof replaces them). Every fact below names the primary source read or queried today. UNVERIFIED marks anything not confirmed, with the reason.

## 1. Live numbers (queried 2026-10-14, 08:56-08:58 UTC)

**Latest block and gas limit.** Block 26,189,683 (timestamp 2026-10-14 08:56:23 UTC): gasLimit 59,941,408 (about 60M), gasUsed 27,296,560, baseFeePerGas 71,599,689 wei = 0.0716 gwei, 237 transactions. eth.drpc.org reported the same head.
Source: `eth_getBlockByNumber("latest")` on ethereum-rpc.publicnode.com.

**Base fee over the last 1,024 blocks** (26,188,661-26,189,684, about 05:31-08:56 UTC): min 0.0536, median 0.0652, max 0.1087 gwei (mean 0.0663). Next block: 0.0743 gwei. Priority fees, as the median over blocks of each block's percentile: p10 0 gwei, p50 0.0101 gwei, p90 1.0 gwei (the highest p90 was 26.1 gwei). Median gasUsedRatio 0.468.
Source: `eth_feeHistory(1024, "latest", [10,50,90])`; publicnode and drpc gave identical answers.

**Base fee over 30 days.** (a) My sample of the chain: 72 windows of 1,024 blocks every 3,000 blocks, so every hour of the day is covered. That is 73,728 blocks, 34% of blocks 25,973,683-26,189,683 (about 2026-09-14 to 10-14). Results in gwei: min 0.0389, p5 0.0547, p25 0.0694, **median 0.128**, mean 0.403, p75 0.330, p95 1.65, p99 4.31, max 14.5. (b) ultrasound.money's 30-day statistics over every block: min 0.0342 gwei (block 25,979,721), **max 24.86 gwei** (block 26,048,837), mean 0.381 gwei. The median of its hourly averages is 0.130 gwei.
Sources: `eth_feeHistory(1024, <block>, [])` on eth.drpc.org; https://ultrasound.money/api/v2/fees/base-fee-per-gas-stats and /api/v2/fees/base-fee-over-time.

**ETH/USD:** $2,506.09 on CoinGecko (updated 08:58:02 UTC). The last Kraken XETHZUSD trade was $2,503.85 (bid 2,503.53, ask 2,503.54) at 08:58 UTC.
Sources: api.coingecko.com/api/v3/simple/price?ids=ethereum; api.kraken.com/0/public/Ticker?pair=XETHZUSD.

**GNOT/USD: Kraken lists it** as pair `GNOTUSD` (wsname GNOT/USD, status online, minimum order 70 GNOT). Last trade $0.07074 (bid 0.07072, ask 0.07074). Over 24 hours: volume-weighted average $0.06936, volume 13.89M GNOT, low 0.06775, high 0.075. CoinGecko (id `gno-land`) shows $0.070753. Kraken's `GNO` is Gnosis, not gno.land.
Sources: Kraken public `AssetPairs`, `Assets` and `Ticker?pair=GNOTUSD`; CoinGecko simple/price?ids=gno-land, all at 08:58 UTC.

## 2. Precompiles, forks, ed25519, CLZ

**2a. Precompiles live on mainnet now.** The `precompiles` list in `eth_config` (EIP-7910): 0x01 ECREC, 0x02 SHA256, 0x03 RIPEMD160, 0x04 ID, 0x05 MODEXP, 0x06 BN254_ADD, 0x07 BN254_MUL, 0x08 BN254_PAIRING, 0x09 BLAKE2F, 0x0a KZG_POINT_EVALUATION. Pectra's BLS12-381 set: 0x0b G1ADD, 0x0c G1MSM, 0x0d G2ADD, 0x0e G2MSM, 0x0f PAIRING_CHECK, 0x10 MAP_FP_TO_G1, 0x11 MAP_FP2_TO_G2. Fusaka's **0x100 P256VERIFY**. There is **no ed25519 entry**. The current fork has activationTime 1767747671 (BPO2, 2026-01-07 01:01:11 UTC) and forkId 0x07c9462e; **`next` is null**.
Source: `eth_config` on publicnode (reth v2.7.0) and drpc, identical answers, 2026-10-14 09:0x UTC.

**2b. Gas, measured live today.** Method: an `eth_call` of init code that reads GAS before and after a STATICCALL to the precompile. The call overhead (119 gas) was calibrated on IDENTITY (21 = 15 + 3 per word). Results:
- **SHA256** = 60 + 12 per 32-byte word: 64 B costs 84; 3,000 B costs 1,188.
- ECRECOVER 3,000. BN254 add 150, mul 6,000, pairing base 45,000.
- BLS12 G1ADD 375; BLS12 pairing with 1 pair 70,300.
- **P256VERIFY 6,900**. It returned 1 for a fresh OpenSSL signature and empty for a tampered hash.
- **MODEXP (EIP-7883 is live).** 32-byte base, exponent and modulus with exponent p-2 (the ed25519 field inverse) costs **4,064**; under EIP-2565 it would be 1,354. Other cases: minimum 500; a 256-byte modulus with e=65537 costs 32,768. A 1,025-byte modulus fails and burns all forwarded gas, so **EIP-7823 is live**. The output was checked against Python's `pow`.
Source: eth_call on publicnode; specs: eips.ethereum.org/EIPS/eip-7883, eip-7823, eip-7951.

**2c. Forks activated.** **Pectra**: 2025-05-07 10:05:11 UTC, epoch 364032 (EIP-7600, Final). **Fusaka**: 2025-12-03 21:49:11 UTC, epoch 411392 (EIP-7607, Final). Blob-parameter-only forks followed: BPO1 on 2025-12-09 14:21:11 UTC (max 15 blobs), BPO2 on 2026-01-07 01:01:11 UTC (target 14, max 21). Fusaka's core EIPs: 7594, 7823, **7825 (transaction gas cap 16,777,216 = 2^24; a block with a larger transaction is invalid)**, 7883, 7917, 7918, 7934, 7939 and 7951. Also in Fusaka: 7935 (default gas limit 60M) and 7910 (`eth_config`). The live beacon schedule agrees: Electra at epoch 364032, Fulu at 411392. **The cap holds on chain.** In blocks 26,189,958-987 (8,387 transactions) the highest gas limit was exactly 16,777,216, and none was higher. In blocks 23,000,000-029, before Fusaka, the highest was 30,000,000. **So a light-client update must fit in 16.7M gas per transaction.**
Sources: eips.ethereum.org/EIPS/eip-7600, eip-7607, eip-7825; ethereum-beacon-api.publicnode.com `/eth/v1/config/fork_schedule` and `/config/spec` (Lighthouse v8.2.3); `eth_getBlockByNumber(…, true)` on drpc.

**2d. Glamsterdam is not on mainnet and has no mainnet date.** It is live on **Sepolia since 2026-10-06 13:53:36 UTC** (epoch 353024). In EIP-7773 (status Review) the Hoodi and mainnet rows are blank. The EF blog (2026-09-17): "Hoodi and mainnet activation dates have not yet been decided." Lighthouse v8.3.0-rc.0 (10-01): "The Gloas fork is not yet scheduled on Hoodi or mainnet." The mainnet beacon spec has GLOAS_FORK_EPOCH = 2^64-1, and `eth_config.next` is null.
Glamsterdam EIPs that change gas or add opcodes. Values are provisional, and 2780, 7976, 8037 and 8038 are in Last Call until 2026-11-01:
- **EIP-7976**: the calldata floor becomes 64 gas per byte, zero bytes included.
- **EIP-2780**: the flat 21,000 splits into 12,000 base + 3,000 for a cold recipient + 6,000 for a value transfer.
- **EIP-8037**: state gas at CPSB = 1,530. **SSTORE from zero to nonzero becomes 97,920** (was 20,000); a new account 183,600; code deposit 1,530 per byte. The 2^24 cap then covers execution gas only, and the total transaction gas is capped at 2^32-1.
- **EIP-8038**: cold account access 3,000; an SSTORE write 10,000.
- EIP-7981 raises access-list costs.
- New opcodes: **SLOTNUM** (7843) and DUPN/SWAPN/EXCHANGE (8024). **No new precompile.**
**The fork after Glamsterdam is Hegotá** (Bogota on the execution side, Heze on the consensus side). It exists only in specs and devnets (consensus-specs `specs/heze`; execution-specs `tests-bogota-devnet@v1.1.0`, 2026-10-13) and has no testnet or mainnet date.
Sources: eips.ethereum.org/EIPS/eip-7773, eip-7976, eip-2780, eip-8037, eip-8038; blog.ethereum.org/en/2026/09/17/glamsterdam-testnet-announcement; geth v1.17.5/v1.17.6, reth v2.6.0 and Lighthouse v8.3.0-rc.0 release notes; GitHub API listings of consensus-specs and execution-specs.

**2e. There is no ed25519 precompile on Ethereum L1, live or scheduled.** It is absent from `eth_config`. The full EIP index has two ed25519 entries:
- **EIP-665**, ED25519VFY, from 2018: **Stagnant**. It proposed address 0x9 (now BLAKE2F) at 2,000 gas.
- **EIP-7980**, ed25519 transaction support: **Withdrawn** in favour of P256.
No Fusaka or Glamsterdam EIP adds one. **RIP-7696** is a generic double scalar multiplication u*P+v*Q on short-Weierstrass curves, 4,000 gas, or 2,500 with precomputed points. It is a **Draft rollup proposal** whose address is still "TBD", and it is not on L1. ed25519 would need its Weierstrass form there.
Sources: `eth_config`; eips.ethereum.org/all (index read 2026-10-14); eips.ethereum.org/EIPS/eip-665; github.com/ethereum/RIPs/blob/master/RIPS/rip-7696.md.

**2f. CLZ (EIP-7939) is live.** Opcode 0x1e returned CLZ(1)=255 and CLZ(0)=256 on both providers. It costs **5 gas** (measured). The same code at block 23,000,000, before Fusaka, fails with `EVM error: NotActivated`.
Source: eth_call of init code on publicnode and drpc.

## 3. Calldata cost

**3a. EIP-7623, live since Pectra.** A zero byte is 1 token and a nonzero byte is 4. The transaction pays the larger of two sums:
- the standard sum: 21000 + 4 × tokens + execution gas (+ creation costs), which is 16 gas per nonzero byte and 4 per zero byte;
- the floor: 21000 + 10 × tokens, which is 40 gas per nonzero byte and 10 per zero byte.
The gas limit must cover the floor. The floor wins only when execution gas ≤ 6 × tokens.
**Verified live.** A call with 3,000 nonzero bytes to an account with no code succeeds at gas 141,000 (= 21000 + 10 × 12,000). At 140,999 it fails with "intrinsic gas too low"; the old rule would allow 69,000. With 3,000 zero bytes the minimum is 51,000. Both providers agree.
Sources: eips.ethereum.org/EIPS/eip-7623 (Final); `eth_call`/`eth_estimateGas` on publicnode and drpc.

**3b. Later changes.** **EIP-7976** (Glamsterdam, Last Call) counts every byte as 4 floor tokens at 16 gas, so the floor becomes 64 gas per byte for any byte. The standard 4/16 path is unchanged. **It is not on mainnet. It is live on Sepolia**, together with EIP-2780's intrinsic split. There the minimum gas for 3,000 bytes to an empty account, zero or nonzero, is **207,000** = 15,000 (12,000 base + 3,000 cold recipient) + 64 × 3,000. On Sepolia an SSTORE from zero to nonzero now costs **110,020** (97,920 state gas + 12,100); on mainnet it costs 22,100. Both were measured today.
Sources: eips.ethereum.org/EIPS/eip-7976, eip-2780, eip-8037, eip-8038; `eth_call` on ethereum-sepolia-rpc.publicnode.com (reth v2.7.0; `eth_config` current fork activated 2026-10-06 13:53:36 UTC).

**3c. Worked example: about 3 KB of calldata and 3M execution gas, under today's mainnet rules.** Take 3,000 bytes, mostly nonzero, which is 12,000 tokens. Calldata costs **48,000 gas**, 16 per byte; a zero byte costs 4. The floor (141,000) does not bind, because 3M is far above 6 × tokens. Total: 21,000 + 48,000 + 3,000,000 = **3,069,000 gas**. Each line below adds the median p50 priority fee, 0.0101 gwei, at ETH $2,506:

| Base fee | Whole transaction | The calldata alone |
|---|---|---|
| Now, 0.0716 gwei | $0.63 | $0.01 |
| 30-day median, 0.128 gwei | $1.06 | $0.017 |
| 30-day p95, 1.65 gwei | $12.77 | $0.20 |
| 30-day p99, 4.31 gwei | $33.23 | $0.52 |
| 30-day maximum, 24.86 gwei | $191 | $3.0 |

EIP-7976's floor would be 21,000 + 192,000, which also would not bind this transaction.
Source: arithmetic on the numbers in sections 1 and 3a.

## 4. Finality and code immutability

**4a. What `finalized` and `safe` mean**, quoted from the JSON-RPC spec:
- `finalized`: "The most recent crypto-economically secure block, cannot be re-orged outside of manual intervention driven by community coordination."
- `safe`: "The most recent block that is safe from re-orgs under honest majority and certain synchronicity assumptions."
- `latest` "may be re-orged out of the canonical chain even under healthy/normal conditions."

Measured today: `finalized` resolves to the first block of the beacon chain's finalized checkpoint epoch, and `safe` to the first block of the current justified checkpoint epoch.
Source: github.com/ethereum/execution-apis `src/schemas/block.yaml` (BlockTag); the beacon API `finality_checkpoints` compared with block times below.

**4b. How far behind the head, measured at 09:27 UTC:**

| Provider | Head block | `safe` behind | `finalized` behind |
|---|---|---|---|
| publicnode | 26,189,837 | 34 blocks, 408 s | **66 blocks, 792 s** |
| drpc | 26,189,838 | 35 blocks, 420 s | 67 blocks, 804 s |

Both providers show the same finalized block, 26,189,771 (09:13:59), and the same safe block, 26,189,803 (09:20:23). On the beacon chain, the head slot 15,428,835 was the fourth slot of epoch 482151. The finalized epoch was 482149, starting at 09:13:59; the justified epoch was 482150, starting at 09:20:23.
Slot and epoch: SECONDS_PER_SLOT = 12 (SLOT_DURATION_MS 12000) and SLOTS_PER_EPOCH = 32, so an epoch is 6.4 minutes.
Derived, not measured over time: in normal running the finalized block trails the head by 2 to 3 epochs, which is 64 to 95 slots, or 12.8 to 19 minutes.
Sources: `eth_getBlockByNumber` with latest/safe/finalized on both providers; ethereum-beacon-api.publicnode.com `/eth/v1/beacon/states/head/finality_checkpoints` and `/eth/v1/config/spec`.

**4c. Mainnet finality stalls since 2023:**
- **2023-05-11**, 20:06:47-20:32:23 UTC: no finality for **4 epochs, about 25 minutes** (epochs 200,551-200,555). Participation fell as low as 40%.
- **2023-05-12**, 17:20-18:24 UTC: no finality for **9 epochs, about 1 hour** (epochs 200,750-200,759). Participation fell as low as 30.7%, and the inactivity penalty applied after 5 epochs.
- Cause of both: attestations aimed at old checkpoints forced clients to replay old states.
- **2025-12-04**, the day after Fusaka, about 02:45-07:12 UTC: a Prysm bug left 248 of 1,344 slots without a block over epochs 411,439-411,480, and participation fell to 75%. **No loss of finality is reported.** The post-mortem says only that a client with more than 1/3 of the network "would have caused a temporary loss in finality."
I found no complete official list of incidents, so that there were no others is **UNVERIFIED**.
Source: Prysm docs, "Mainnet postmortems": prysm.offchainlabs.com/docs/misc/mainnet-postmortems/.

**4d. Can code at an address change? Mostly no; the exception is EIP-7702.** Under EIP-6780 (Cancun, Final), SELFDESTRUCT deletes code and storage **only in the same transaction that created the contract**; otherwise it only moves the balance. The old trick of SELFDESTRUCT in a later transaction followed by a CREATE2 redeploy is gone; the EIP says it is "not supported anymore". The remaining edge: a contract created and destroyed inside one transaction leaves an empty address that CREATE2 can deploy to later. EIP-8246 (Glamsterdam, Last Call) keeps this and stops the ETH burn.
**EIP-7702** (Pectra, Final) lets an EOA set, re-point or clear its own code at any time, as a delegation `0xef0100||address`. EXTCODESIZE then returns 23. So `code.length == 0` no longer proves an address is an EOA.
Sources: eips.ethereum.org/EIPS/eip-6780, eip-8246, eip-7702.

## 5. Public mainnet RPC endpoints without an account (tested 2026-10-14, 09:30-09:45 UTC)

The test was `eth_getLogs` for USDC Transfer events (one topic). Over 1,000 blocks that is about 74,000 logs.

| Endpoint | blockNumber, `finalized` | eth_getLogs limit found | `eth_call` 100 blocks back / archive |
|---|---|---|---|
| ethereum-rpc.publicnode.com (also ethereum.publicnode.com), reth v2.7.0 | yes / yes | **20,000 results** (1,000 USDC blocks refused; 100 gave 7,873). A 10,000-block range near the head is fine; a window 40-50k blocks back is refused as archive | yes; **state only about 100 blocks deep** (104 back: "Archive requests require a personal token") |
| eth.drpc.org | yes / yes | **about 100 blocks** on the free plan; the error wrongly says "over 10000 blocks" | yes / **archive yes** (1M blocks back) |
| rpc.mevblocker.io | yes / yes | **10,000 results** (it suggests a sub-range); 10,000-block range fine | yes / archive yes |
| gateway.tenderly.co/public/mainnet (= mainnet.gateway.tenderly.co) | yes / yes | 1,000 USDC blocks returns "invalid params"; 100 is fine; 50,000 blocks on a quiet contract is fine | yes / archive yes |
| eth-mainnet.public.blastapi.io | yes / yes | **10 blocks** | yes / archive yes |
| eth-pokt.nodies.app | yes / yes | **50 blocks** | yes / archive yes |
| 1rpc.io/eth | yes / yes, but once reported finalized as head-221 | **50 blocks** | yes; state about 128 blocks deep |
| eth.blockrazor.xyz, geth 1.17.5 | yes / yes | **25 blocks** | yes; state 128 blocks deep |
| rpc.flashbots.net | yes / yes | 1,000 USDC blocks fine (74,046 logs), but **a window 40-50k blocks back returned 0 logs with no error**; others returned 1,349 | no: `eth_call` is "not whitelisted" |

**Need a key or not working today:**
- rpc.ankr.com/eth: "must authenticate … API key".
- ethereum.blockpi.network: "Apikey not found".
- eth.api.onfinality.io/public: 429, asks for an API key.
- api.zan.top: `eth_call` and `eth_getLogs` need registration.
- eth.merkle.io and eth.meowrpc.com: rate-limited at once.
- eth.llamarpc.com: Cloudflare error 525.
- cloudflare-eth.com: "Cannot fulfill request".
- eth1.lava.build: "discontinued".
- rpc.eth.gateway.fm: 503.
- omniatech: 521.
- rpc.payload.de, subquery and alchemy /v2/demo: empty replies.

**What this means for the relayers.** `finalized` is normally 64-95 blocks back, so `eth_call` at `finalized` fits in publicnode's window of about 100 blocks with little room. It fails there if finality slips. Never treat one endpoint's empty `eth_getLogs` as "no events".
Source: live JSON-RPC calls to each URL above.

## 6. Testnets

**6a. Sepolia: live, and the recommended testnet for applications.** Chain id 11155111. At 09:49:12 UTC its head was block 11,912,463, with a **gas limit of 200,000,000**. ethereum.org says: "Sepolia is the recommended default testnet for application development." **Sepolia already runs Glamsterdam** (since 2026-10-06), so its gas rules are not mainnet's. Measured differences are in 3b: the 64 gas/byte calldata floor, a 110,020-gas SSTORE from zero to nonzero, and the EIP-2780 intrinsic gas. Gas figures for the bridge must be measured against mainnet rules: a mainnet fork, or a local EVM set to Osaka.
Sources: ethereum.org/developers/docs/networks/ (page updated 2026-09-23); `eth_getBlockByNumber` and `eth_config` on ethereum-sepolia-rpc.publicnode.com.

**6b. Hoodi: live, a testnet for validators and staking.** Chain id 560048. Its head was block 3,814,136 at 09:49:12 UTC, with a gas limit of 60M. Glamsterdam is not yet scheduled on Hoodi.
**Holesky: deprecated** "as of September 2025". The EF blog of 2025-09-01 said validators would shut down 2 weeks after Fusaka finalised on Holesky, which happened on 2025-10-01. Its head today is **block 5,765,077 from 2026-06-03**, so it has stopped. publicnode no longer serves Holesky.
Sources: ethereum.org networks page; blog.ethereum.org/en/2025/09/01/holesky-shutdown-announcement; hoodi.drpc.org, holesky.drpc.org, ethereum-hoodi-rpc.publicnode.com.

**6c. Sepolia ETH without spending anything.**
- **PoW Faucet, sepolia-faucet.pk910.de: no account and no mainnet balance.** It needs a captcha to start, then mining in the browser (CryptoNight, capped at 1,000 H/s). Each session pays 0.05-2.5 SepETH and lasts up to 24 hours. A Gitcoin Passport can raise the reward.
- **Alchemy: 0.1 ETH once every 24 hours**, "No Alchemy account required". It does need "at least 0.001 ETH on Ethereum Mainnet" (holding it, not spending it) and has activity criteria.
- ethereum.org also lists Chain Platform, Chainstack, ethfaucet.com, Google Cloud Web3, Grabteeth, Infura, OpenFaucet.org and QuickNode.
- Google Cloud's terms are **UNVERIFIED**: its page renders by script, and no text could be read.
Sources: sepolia-faucet.pk910.de/api/getFaucetConfig (live config); alchemy.com/faucets/ethereum-sepolia; ethereum.org networks page.

## 7. The sync-committee light client, for a later ZK proof of Ethereum finality

**7a. Size and timing.**
- SYNC_COMMITTEE_SIZE = **512**.
- EPOCHS_PER_SYNC_COMMITTEE_PERIOD = **256**. That is 8,192 slots, or 98,304 s, which is **27.3 hours**.
- MIN_SYNC_COMMITTEE_PARTICIPANTS = 1.
- UPDATE_TIMEOUT = 8,192 slots, one period. After that the store may force-apply its best update, treating the attested header as finalized, "in extended periods of non-finality".
Sources: live mainnet `/eth/v1/config/spec` (Lighthouse v8.2.3); consensus-specs `specs/altair/light-client/sync-protocol.md`.

**7b. The 2/3 rule.** In `process_light_client_update`, a light client moves its **finalized** header only when `get_set_bit_count(sync_committee_bits) * 3 >= len(sync_committee_bits) * 2`. That means at least 342 of 512 signers, and the update must carry a finality branch. The optimistic header needs only more than half of the highest recent participation (`get_safety_threshold`).
Source: consensus-specs `specs/altair/light-client/sync-protocol.md`.

**7c. Sync-committee members cannot be slashed for signing a wrong header.** In every fork from Altair through Electra, Gloas and Heze, `BeaconBlockBody` carries only `proposer_slashings` and `attester_slashings`. No sync-committee slashing container exists. So a light client of Ethereum trusts the honest majority of the 512 members. Unlike a Tendermint commit, misbehaviour costs them no stake.
Source: consensus-specs `specs/{altair,electra,gloas,heze}/beacon-chain.md` (master, read 2026-10-14). Fulu does not redefine the block body.

**7d. What Electra changed.** BeaconState grew, so its Merkle tree went one level deeper. The generalized indices (gindex) of the state fields moved:
- finalized root: 105 → **169**;
- current sync committee: 54 → **86**;
- next sync committee: 55 → **87**.
They are chosen by the slot's epoch (`*_gindex_at_slot`), so branch lengths differ across the fork. EXECUTION_PAYLOAD_GINDEX in BeaconBlockBody stays **25**.
**Gloas (Glamsterdam, not yet on mainnet) changes this again.**
- Under EIP-7688 the gindices become 735 (finalized root), 2,945 (current committee) and 2,946 (next committee).
- Under EIP-7732 (ePBS) `LightClientHeader` drops `execution` and carries only `execution_block_hash`. That hash is proven at gindex 2,856, through `signed_execution_payload_bid.message.parent_block_hash`.
- So a verifier must handle the fork boundary. After Gloas, it must get the state or receipts root from the RLP execution header that hashes to that hash.
Sources: consensus-specs `specs/electra/light-client/sync-protocol.md`, `specs/capella/light-client/sync-protocol.md`, `specs/gloas/light-client/sync-protocol.md`.

**7e. SP1 Helios (Succinct): maintained, and already ready for Gloas.** It is a light client of Ethereum consensus proven in SP1, with an EVM verifier contract.
- **v1.3.0 (2026-10-06)** added Glamsterdam/Gloas support (Helios 0.12.0, SP1 v6.8.1). It added an "execution-header mode" that also commits the finalized execution block hash and the receipts root, at about 45k more gas per update, plus optional storage proofs.
- **v1.2.0 (2026-06-15) fixed a soundness bug, GHSA-83q5-vwj7-gxww.** A prover could commit "an arbitrary next sync committee" that the contract would store (PR #54).
- Proofs come from the Succinct Prover Network.
Sources: github.com/succinctlabs/sp1-helios: README, releases v1.1.2-v1.3.0, PRs #54 and #55 (GitHub API, 2026-10-14).

## Still unknown

1. **Glamsterdam's dates on Hoodi and mainnet.** None are set. Its gas numbers (EIP-2780, 7976, 8037, 8038; Last Call until 2026-11-01) are "provisional", so the bridge's gas budget on mainnet after Glamsterdam cannot be fixed yet.
2. **Whether finality held on 2025-12-04.** Prysm's post-mortem implies it, but does not say so. Whether any other mainnet stall happened since May 2023 is also open: no complete official list exists, and the beaconcha.in API now needs a key.
3. **The exact 30-day median base fee.** My 0.128 gwei comes from a 34% sample of blocks. ultrasound.money gives the minimum, maximum and mean over every block, but not the median.
4. **The limits of public RPCs.** They are undocumented and change without notice; each was tested once. Tenderly's result cap is unknown (it answers "invalid params"). drpc's error text contradicts its real limit of about 100 blocks.
5. **Google Cloud faucet terms.** The page renders by script and could not be read.
6. **How each client's `safe` and `finalized` behave during a finality stall.** Not tested. Whether a sync-committee slashing EIP is being drafted was not searched.
7. **How tight the 16.7M cap is for the light client.** The cost of ed25519 per signature is the other researcher's (see research-hyperlane-evm-ed25519). Here: the ed25519 field inverse by MODEXP costs 4,064 gas.

## Sources (all read or queried 2026-10-14)

- **Live JSON-RPC.** ethereum-rpc.publicnode.com and ethereum.publicnode.com (reth v2.7.0); eth.drpc.org; rpc.mevblocker.io; gateway.tenderly.co/public/mainnet; eth-mainnet.public.blastapi.io; eth-pokt.nodies.app; 1rpc.io/eth; eth.blockrazor.xyz; rpc.flashbots.net; plus the failed endpoints listed in §5. Testnets: ethereum-sepolia-rpc.publicnode.com, ethereum-hoodi-rpc.publicnode.com, hoodi.drpc.org, holesky.drpc.org. Methods: `eth_config`, `eth_feeHistory`, `eth_getBlockByNumber`, `eth_call` with init-code probes, `eth_estimateGas`, `eth_getLogs`.
- **Beacon API.** ethereum-beacon-api.publicnode.com (Lighthouse v8.2.3): `/eth/v1/config/fork_schedule`, `/eth/v1/config/spec`, `/eth/v1/beacon/states/head/finality_checkpoints`.
- **Prices.** api.coingecko.com simple/price (ethereum, gno-land); api.kraken.com public AssetPairs, Assets, and Ticker for XETHZUSD and GNOTUSD. Base fee: ultrasound.money/api/v2/fees/base-fee-per-gas-stats and base-fee-over-time.
- **EIPs.** eips.ethereum.org/EIPS/eip-7600, 7607, 7773, 7623, 7976, 7825, 7823, 7883, 6780, 7702, 8246, 2780, 8037, 8038 and 665; the index at eips.ethereum.org/all. RIPs: github.com/ethereum/RIPs RIPS/rip-7696.md.
- **Release notes, via the GitHub API.** go-ethereum v1.16.7-v1.17.8; reth v2.0.0-v2.7.0; Lighthouse v8.0.0-v8.3.0-rc.0; Nethermind 1.38.0-2.1.0. Repository listings of consensus-specs and execution-specs.
- **EF blog.** blog.ethereum.org/en/2026/09/17/glamsterdam-testnet-announcement and /en/2025/09/01/holesky-shutdown-announcement. ethereum.org/developers/docs/networks/.
- **JSON-RPC spec.** github.com/ethereum/execution-apis src/schemas/block.yaml.
- **Consensus specs** (github.com/ethereum/consensus-specs, master). specs/altair/light-client/sync-protocol.md, specs/capella/light-client/sync-protocol.md, specs/electra/light-client/sync-protocol.md and specs/gloas/light-client/sync-protocol.md. Also the beacon-chain.md for altair, electra, fulu, gloas and heze.
- **Incidents.** prysm.offchainlabs.com/docs/misc/mainnet-postmortems/.
- **Faucets.** sepolia-faucet.pk910.de/api/getFaucetConfig; alchemy.com/faucets/ethereum-sepolia.
- **SP1 Helios.** github.com/succinctlabs/sp1-helios: README, releases, PRs #54 and #55.

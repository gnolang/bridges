// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Script} from "forge-std/Script.sol";
import {VmSafe} from "forge-std/Vm.sol";
import {console2} from "forge-std/console2.sol";

import {Mailbox} from "@hyperlane-xyz/core/contracts/Mailbox.sol";
import {HypERC20} from "@hyperlane-xyz/core/contracts/token/HypERC20.sol";
import {MerkleTreeHook} from "@hyperlane-xyz/core/contracts/hooks/MerkleTreeHook.sol";
import {ProtocolFee} from "@hyperlane-xyz/core/contracts/hooks/ProtocolFee.sol";
import {ProxyAdmin} from "@openzeppelin/contracts/proxy/transparent/ProxyAdmin.sol";
import {TransparentUpgradeableProxy} from "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol";

import {Ed25519Verifier, IEd25519Verifier} from "../src/Ed25519Verifier.sol";
import {GnoLightClient} from "../src/GnoLightClient.sol";
import {HlAdmin} from "../src/HlAdmin.sol";
import {PausableGate} from "../src/PausableGate.sol";
import {IGnoLightClient} from "../src/interfaces/IGnoLightClient.sol";

/// The Ethereum deploy and the setup step (eth/bridge/PLAN.md §5.8), in its order: every proxy initialized in its own
/// constructor call, so no initializer is ever open to a front-runner; the ProxyAdmin handed to HlAdmin's address at
/// step 1, before any proxy exists, so the deployer never holds upgrade power (audit round 2, E2-CT1); the deployer
/// holds nothing at the end. The tests call `deploy` (under a prank of the deployer); `Setup.run` broadcasts it. The
/// Go `hleth setup` (lane E-M3) follows the same order and the same nonce plan.
library BridgeSetup {
    /// The cheatcodes, for the deployer's nonce (`getNonce`) and HlAdmin's CREATE address (`computeCreateAddress`).
    VmSafe private constant VM = VmSafe(address(uint160(uint256(keccak256("hevm cheat code")))));

    /// Steps 2–6 are twelve creations from the deployer, thirteen when it deploys the verifier: the mailbox's
    /// implementation and proxy, the MerkleTreeHook, the ProtocolFee, (the verifier,) the client's implementation and
    /// proxy, each token's implementation and proxy, the gate's implementation and proxy.
    uint64 internal constant CREATIONS = 12;

    /// The deployer's nonce is not the one the plan says `step` uses: the setup stops there, before it wires
    /// anything more to an address HlAdmin would not have.
    error NoncePlan(string step, uint64 planned, uint64 actual);

    struct Config {
        /// The key that deploys and holds every authority but the ProxyAdmin until step 10 (the caller is it: a
        /// prank or a broadcast; under either every call and every creation takes one of its nonces, as a
        /// transaction does).
        address deployer;
        /// Ethereum's Hyperlane domain: 1 mainnet, 11155111 Sepolia, 31337 the local run.
        uint32 localDomain;
        /// gno.land's domain: 1198419788 for gnoland-1, 1198419796 for any other chain.
        uint32 gnoDomain;
        /// The client's start: the chain id, the trusted header as its sixteen leaves and its validator set. The
        /// client refuses a header announcing a change or more than an hour from the initialization (audit round 2,
        /// E2-CT3).
        string chainId;
        bytes[] trustedLeaves;
        IGnoLightClient.Validator[] validators;
        /// keccak256 of this deployment's hlmailbox realm path, compiled into the client.
        bytes32 mailboxPkgPathHash;
        /// An `Ed25519Verifier` already on the chain (lane E-M2a's: one serves every deployment), or zero to deploy
        /// one at step 4. Either way HlAdmin requires its code to be this tree's build (finishSetup's check 28).
        address ed25519;
        /// The ProtocolFee's immutable maximum (0.001 ETH at launch, HlAdmin's compiled bound) and the treasurer's
        /// beneficiary address.
        uint256 maxProtocolFee;
        address beneficiary;
        string name0;
        string symbol0;
        string name1;
        string symbol1;
        address[4] seats;
        address[2] watch;
        uint64[2] maxPerMessage;
        uint64[2] dayCap;
        /// r/hlgov's and the two route realms' 32-byte addresses (the gno address left-padded).
        bytes32 hlgov;
        bytes32 route0;
        bytes32 route1;
        /// r/hlgov's counter: the number HlAdmin's sequence starts after.
        uint64 startSequence;
    }

    struct Deployment {
        ProxyAdmin proxyAdmin;
        Mailbox mailboxImpl;
        Mailbox mailbox;
        MerkleTreeHook merkleTreeHook;
        ProtocolFee protocolFee;
        IEd25519Verifier ed25519;
        address clientImpl;
        GnoLightClient client;
        HypERC20 token0Impl;
        HypERC20 token0;
        HypERC20 token1Impl;
        HypERC20 token1;
        PausableGate gateImpl;
        PausableGate gate;
        HlAdmin admin;
        /// HlAdmin's address by the deployer's nonce plan (`adminNonce`), which owns the ProxyAdmin from step 1.
        address adminAt;
        /// The deployer's nonce step 7 creates HlAdmin at.
        uint64 adminNonce;
    }

    /// The eleven steps in order.
    function deploy(Config memory c) internal returns (Deployment memory d) {
        d = contracts(c);
        d.admin = admin(c, d);
        settings(c, d);
        ownership(d);
        // 11. the self-check
        d.admin.finishSetup();
    }

    /// The deployer's nonce plan (eth/bridge/M2-NOTES.md, Fold F4): `first` creates the ProxyAdmin, `first + 1`
    /// hands it to HlAdmin's address, steps 2–6's creations follow, then HlAdmin: a fresh deployer creates HlAdmin
    /// at nonce 15, or 14 when the verifier is already on the chain.
    function adminNonce(Config memory c, uint64 first) internal pure returns (uint64) {
        return first + 2 + CREATIONS + (c.ed25519 == address(0) ? 1 : 0);
    }

    /// Steps 1–6: every contract but HlAdmin, each proxy initialized in its construction.
    function contracts(Config memory c) internal returns (Deployment memory d) {
        // 1. the ProxyAdmin, handed at once to HlAdmin's address-to-be (its CREATE address by the nonce plan), so the
        //    deployer never holds upgrade power over a proxy: `transferOwnership` takes an address without code, and
        //    HlAdmin's constructor (step 7) refuses to be built unless the ProxyAdmin is already its own
        uint64 first = VM.getNonce(c.deployer);
        d.proxyAdmin = new ProxyAdmin();
        _expectNonce("step 1: the ProxyAdmin, created by the deployer", first + 1, c.deployer);
        d.adminNonce = adminNonce(c, first);
        d.adminAt = VM.computeCreateAddress(c.deployer, d.adminNonce);
        d.proxyAdmin.transferOwnership(d.adminAt);
        _expectNonce("step 1: the hand-over", first + 2, c.deployer);
        address pa = address(d.proxyAdmin);
        // 2. the Mailbox and its proxy: the deployer its owner, the ProxyAdmin standing in for the default ISM and
        //    both hooks (the initializer requires contracts) until step 9
        d.mailboxImpl = new Mailbox(c.localDomain);
        d.mailbox = Mailbox(
            address(
                new TransparentUpgradeableProxy(
                    address(d.mailboxImpl), pa, abi.encodeCall(Mailbox.initialize, (c.deployer, pa, pa, pa))
                )
            )
        );
        // 3. the hooks
        d.merkleTreeHook = new MerkleTreeHook(address(d.mailbox));
        d.protocolFee = new ProtocolFee(c.maxProtocolFee, 0, c.beneficiary, c.deployer);
        // 4. the ed25519 verifier unless the config names one, then the client implementation over it (the verifier's
        //    address an immutable of its code) and its proxy: the chain id, gno.land's domain, the trusted header's
        //    leaves and its set, the deployer as admin
        if (c.ed25519 != address(0)) {
            d.ed25519 = IEd25519Verifier(c.ed25519);
        } else {
            d.ed25519 = new Ed25519Verifier();
        }
        d.clientImpl = address(new GnoLightClient(c.mailboxPkgPathHash, d.ed25519));
        d.client = GnoLightClient(
            address(
                new TransparentUpgradeableProxy(
                    d.clientImpl,
                    pa,
                    abi.encodeCall(
                        GnoLightClient.initialize, (c.chainId, c.gnoDomain, c.trustedLeaves, c.validators, c.deployer)
                    )
                )
            )
        );
        // 5. the two synthetic tokens: decimals 6, scale 1/1; supply 0, hook 0, ISM 0, the deployer as owner
        (d.token0Impl, d.token0) = _token(address(d.mailbox), pa, c.name0, c.symbol0, c.deployer);
        (d.token1Impl, d.token1) = _token(address(d.mailbox), pa, c.name1, c.symbol1, c.deployer);
        // 6. the gate: the mailbox, the client, the tokens, the seats and watch slots, the mint limits
        d.gateImpl = new PausableGate();
        d.gate = PausableGate(
            address(
                new TransparentUpgradeableProxy(
                    address(d.gateImpl),
                    pa,
                    abi.encodeCall(
                        PausableGate.initialize,
                        (
                            c.deployer,
                            address(d.mailbox),
                            address(d.client),
                            [address(d.token0), address(d.token1)],
                            c.seats,
                            c.watch,
                            c.maxPerMessage,
                            c.dayCap
                        )
                    )
                )
            )
        );
    }

    /// Step 7: HlAdmin over the deployment, at the nonce (so the address) the plan handed the ProxyAdmin to.
    function admin(Config memory c, Deployment memory d) internal returns (HlAdmin a) {
        _expectNonce("step 7: HlAdmin", d.adminNonce, c.deployer);
        a = new HlAdmin(adminConfig(c, d));
        _expectNonce("step 7: HlAdmin, created by the deployer", d.adminNonce + 1, c.deployer);
    }

    /// HlAdmin's constructor settings for the deployment (PLAN.md §5.8 step 7).
    function adminConfig(Config memory c, Deployment memory d) internal pure returns (HlAdmin.Config memory) {
        return HlAdmin.Config({
            mailbox: address(d.mailbox),
            client: address(d.client),
            gate: address(d.gate),
            token0: address(d.token0),
            token1: address(d.token1),
            proxyAdmin: address(d.proxyAdmin),
            merkleTreeHook: address(d.merkleTreeHook),
            protocolFee: address(d.protocolFee),
            ed25519: address(d.ed25519),
            ed25519CodeHash: ed25519CodeHash(),
            localDomain: c.localDomain,
            origin: c.gnoDomain,
            hlgov: c.hlgov,
            route0: c.route0,
            route1: c.route1,
            mailboxPkgPathHash: c.mailboxPkgPathHash,
            seats: c.seats,
            watch: c.watch,
            maxPerMessage: c.maxPerMessage,
            dayCap: c.dayCap,
            startSequence: c.startSequence,
            predecessor: address(0)
        });
    }

    /// Refuses to go on when the deployer's nonce is not the one the plan gives `step`.
    function _expectNonce(string memory step, uint64 planned, address deployer) private view {
        uint64 actual = VM.getNonce(deployer);
        if (actual != planned) {
            revert NoncePlan(step, planned, actual);
        }
    }

    /// The EXTCODEHASH of this tree's `Ed25519Verifier` build (its runtime code has no immutables to fill).
    function ed25519CodeHash() internal pure returns (bytes32) {
        return keccak256(type(Ed25519Verifier).runtimeCode);
    }

    /// Steps 8–9: the tokens' ISM and routers, the mailbox's default ISM and hooks.
    function settings(Config memory c, Deployment memory d) internal {
        // 8. each token's ISM → the gate; each token enrolls gno.land's domain → its route realm
        d.token0.setInterchainSecurityModule(address(d.gate));
        d.token0.enrollRemoteRouter(c.gnoDomain, c.route0);
        d.token1.setInterchainSecurityModule(address(d.gate));
        d.token1.enrollRemoteRouter(c.gnoDomain, c.route1);
        // 9. the mailbox's default ISM → the gate; default hook → the MerkleTreeHook; required hook → the ProtocolFee
        d.mailbox.setDefaultIsm(address(d.gate));
        d.mailbox.setDefaultHook(address(d.merkleTreeHook));
        d.mailbox.setRequiredHook(address(d.protocolFee));
    }

    /// Step 10: ownership, in this order (the ProxyAdmin is HlAdmin's since step 1).
    function ownership(Deployment memory d) internal {
        address a = address(d.admin);
        d.merkleTreeHook.transferOwnership(a);
        d.protocolFee.transferOwnership(a);
        d.token0.transferOwnership(a);
        d.token1.transferOwnership(a);
        d.gate.transferOwnership(a);
        d.mailbox.transferOwnership(a);
        d.client.setAdmin(a);
    }

    function _token(address mailbox, address pa, string memory name, string memory symbol, address owner)
        private
        returns (HypERC20 impl, HypERC20 proxy)
    {
        impl = new HypERC20(6, 1, 1, mailbox);
        proxy = HypERC20(
            address(
                new TransparentUpgradeableProxy(
                    address(impl),
                    pa,
                    abi.encodeCall(HypERC20.initialize, (0, name, symbol, address(0), address(0), owner))
                )
            )
        );
    }
}

/// `forge script script/Setup.s.sol --broadcast` with the deployment's settings in a JSON file named by
/// `BRIDGE_SETUP_CONFIG` (keys as `BridgeSetup.Config`'s: `trustedLeaves` the trusted header's sixteen leaves as hex,
/// `pubkeys` and `powers` its set, `mailboxPkgPath` the path whose hash is compiled; `ed25519` optional) and the
/// deployer's key in `DEPLOYER_KEY`. The Go `hleth setup` is the operator's tool; this script is the same order and
/// the same nonce plan for a local run.
contract Setup is Script {
    function run() external returns (BridgeSetup.Deployment memory d) {
        uint256 key = vm.envUint("DEPLOYER_KEY");
        string memory json = vm.readFile(vm.envString("BRIDGE_SETUP_CONFIG"));
        BridgeSetup.Config memory c = _config(json);
        c.deployer = vm.addr(key);
        vm.startBroadcast(key);
        d = BridgeSetup.deploy(c);
        vm.stopBroadcast();
        console2.log("ProxyAdmin", address(d.proxyAdmin));
        console2.log("Mailbox", address(d.mailbox));
        console2.log("MerkleTreeHook", address(d.merkleTreeHook));
        console2.log("ProtocolFee", address(d.protocolFee));
        console2.log("Ed25519Verifier", address(d.ed25519));
        console2.log("GnoLightClient", address(d.client));
        console2.log("token 0", address(d.token0));
        console2.log("token 1", address(d.token1));
        console2.log("PausableGate", address(d.gate));
        console2.log("HlAdmin", address(d.admin));
    }

    function _config(string memory json) internal view returns (BridgeSetup.Config memory c) {
        c.localDomain = uint32(vm.parseJsonUint(json, ".localDomain"));
        c.gnoDomain = uint32(vm.parseJsonUint(json, ".gnoDomain"));
        c.chainId = vm.parseJsonString(json, ".chainId");
        c.trustedLeaves = vm.parseJsonBytesArray(json, ".trustedLeaves");
        bytes32[] memory pks = vm.parseJsonBytes32Array(json, ".pubkeys");
        uint256[] memory powers = vm.parseJsonUintArray(json, ".powers");
        c.validators = new IGnoLightClient.Validator[](pks.length);
        for (uint256 i = 0; i < pks.length; i++) {
            c.validators[i] = IGnoLightClient.Validator(pks[i], int64(uint64(powers[i])));
        }
        c.mailboxPkgPathHash = keccak256(bytes(vm.parseJsonString(json, ".mailboxPkgPath")));
        if (vm.keyExistsJson(json, ".ed25519")) {
            c.ed25519 = vm.parseJsonAddress(json, ".ed25519");
        }
        c.maxProtocolFee = vm.parseJsonUint(json, ".maxProtocolFee");
        c.beneficiary = vm.parseJsonAddress(json, ".beneficiary");
        c.name0 = vm.parseJsonString(json, ".name0");
        c.symbol0 = vm.parseJsonString(json, ".symbol0");
        c.name1 = vm.parseJsonString(json, ".name1");
        c.symbol1 = vm.parseJsonString(json, ".symbol1");
        address[] memory seats = vm.parseJsonAddressArray(json, ".seats");
        address[] memory watch = vm.parseJsonAddressArray(json, ".watch");
        for (uint256 i = 0; i < 4; i++) {
            c.seats[i] = seats[i];
        }
        c.watch = [watch[0], watch[1]];
        uint256[] memory maxes = vm.parseJsonUintArray(json, ".maxPerMessage");
        uint256[] memory caps = vm.parseJsonUintArray(json, ".dayCap");
        c.maxPerMessage = [uint64(maxes[0]), uint64(maxes[1])];
        c.dayCap = [uint64(caps[0]), uint64(caps[1])];
        c.hlgov = vm.parseJsonBytes32(json, ".hlgov");
        c.route0 = vm.parseJsonBytes32(json, ".route0");
        c.route1 = vm.parseJsonBytes32(json, ".route1");
        c.startSequence = uint64(vm.parseJsonUint(json, ".startSequence"));
    }
}

// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {
    ITransparentUpgradeableProxy,
    TransparentUpgradeableProxy
} from "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol";
import {Mailbox} from "@hyperlane-xyz/core/contracts/Mailbox.sol";
import {HypERC20} from "@hyperlane-xyz/core/contracts/token/HypERC20.sol";
import {MerkleTreeHook} from "@hyperlane-xyz/core/contracts/hooks/MerkleTreeHook.sol";

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {BridgeSetup} from "../script/Setup.s.sol";
import {Ed25519Verifier, IEd25519Verifier} from "../src/Ed25519Verifier.sol";
import {GnoLightClient} from "../src/GnoLightClient.sol";
import {HlAdmin} from "../src/HlAdmin.sol";
import {SetupIncomplete} from "../src/admin/HlAdminStorage.sol";
import {IGnoLightClient} from "../src/interfaces/IGnoLightClient.sol";
import {IHlAdmin} from "../src/interfaces/IHlAdmin.sol";
import {BridgeFixture} from "./helpers/BridgeFixture.sol";
import {TestChain} from "./helpers/TestChain.sol";

/// A verifier that accepts every signature: another code than this tree's Ed25519Verifier.
contract TrustingVerifier is IEd25519Verifier {
    function verify(bytes32, bytes calldata, bytes32, bytes32) external pure returns (bool) {
        return true;
    }
}

/// A client implementation that answers what the test sets (the views finishSetup reads).
contract MockClientViews {
    address internal immutable adm;
    uint64 internal immutable factor;
    uint32 internal immutable origin;
    bytes32 internal immutable pkg;

    constructor(address adm_, uint64 factor_, uint32 origin_, bytes32 pkg_) {
        (adm, factor, origin, pkg) = (adm_, factor_, origin_, pkg_);
    }

    function admin() external view returns (address) {
        return adm;
    }

    function setAdmin(address) external {}

    function clockFactor() external view returns (uint64) {
        return factor;
    }

    function originDomain() external view returns (uint32) {
        return origin;
    }

    function mailboxPkgPathHash() external view returns (bytes32) {
        return pkg;
    }
}

/// A token implementation whose supply reads one.
contract HypERC20Supply is HypERC20 {
    constructor(address mailbox) HypERC20(6, 1, 1, mailbox) {}

    function totalSupply() public pure override returns (uint256) {
        return 1;
    }
}

/// finishSetup (PLAN.md §5.8 step 11): it flips on the launch shape, once, and refuses each of its checks failing
/// alone — the owners and the client's admin, the proxies' admin, the mailbox's ISM and hooks, each token's ISM,
/// hook, fees and router, the ProtocolFee's maximum, the gate's bindings and guardian list, the clock factors, the
/// origin, the values held in immutables (a token's mailbox, scale, decimals and supply, the MerkleTreeHook's
/// mailbox, the client's hlmailbox path, its ed25519 verifier and that verifier's code, the local domains) and the
/// launch values in storage (the gate's mint limits, the protocol fee); HlAdmin's own record (34) is
/// PlantedRecord.t.sol's. Checks 32 and 33 (the mailbox's nonce, the MerkleTreeHook's count) are retired: a
/// stranger's dispatch could veto the launch (audit round 2, E2-RT6). The ProxyAdmin is HlAdmin's from step 1 (audit
/// round 2, E2-CT1): a tampering that needs it is done here by HlAdmin's own hand, as a kind 8 would; a launch HlAdmin
/// is never built without it; and the setup refuses to go on off its nonce plan.
contract HlAdminSetupTest is BridgeFixture {
    function _cfg() internal returns (BridgeSetup.Config memory) {
        vm.warp(1_791_000_000);
        chainTime = 1_791_000_000;
        IGnoLightClient.Validator[] memory v = TestChain.validators(1, 4, 60);
        return _config(TestChain.leaves(_header(100, v, v, bytes32(0))), v);
    }

    /// Steps 1–9 as the deployer; the caller tampers, then `_ownership(skip)` and `_expectRefused(check)`.
    function _upToOwnership(BridgeSetup.Config memory c) internal {
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        d.admin = BridgeSetup.admin(c, d);
        BridgeSetup.settings(c, d);
    }

    /// Step 10 but the transfer numbered `skip` (2 the mailbox, 3 token 0, 4 the gate, 5 the MerkleTreeHook, 6 the
    /// ProtocolFee, 7 the client's admin; 0 none). The ProxyAdmin (1) is HlAdmin's from step 1, so for it HlAdmin's
    /// own hand gives it away after step 10 (only a kind 9 could).
    function _ownership(uint8 skip) internal {
        address a = address(d.admin);
        if (skip != 5) d.merkleTreeHook.transferOwnership(a);
        if (skip != 6) d.protocolFee.transferOwnership(a);
        if (skip != 3) d.token0.transferOwnership(a);
        d.token1.transferOwnership(a);
        if (skip != 4) d.gate.transferOwnership(a);
        if (skip != 2) d.mailbox.transferOwnership(a);
        if (skip != 7) d.client.setAdmin(a);
        vm.stopPrank();
        if (skip == 1) {
            address elsewhere = makeAddr("elsewhere");
            vm.prank(a);
            d.proxyAdmin.transferOwnership(elsewhere);
        }
    }

    function _expectRefused(uint8 check) internal {
        vm.expectRevert(abi.encodeWithSelector(SetupIncomplete.selector, check));
        d.admin.finishSetup();
        assertFalse(d.admin.setupDone());
    }

    /// An upgrade between the deployer's steps, by the ProxyAdmin's owner: HlAdmin from step 1, by its own hand here
    /// as a kind 8 would.
    function _upgrade(address proxy, address impl) internal {
        vm.stopPrank();
        vm.prank(address(d.admin));
        d.proxyAdmin.upgrade(ITransparentUpgradeableProxy(proxy), impl);
        vm.startPrank(DEPLOYER);
    }

    // ------------------------------------------------------------------------------------------------ the pass --

    function test_finishSetup_flipsOnTheLaunchShapeOnce() public {
        _upToOwnership(_cfg());
        _ownership(0);
        vm.expectEmit(false, false, false, false, address(d.admin));
        emit IHlAdmin.SetupDone();
        vm.prank(makeAddr("anyone"));
        d.admin.finishSetup();
        assertTrue(d.admin.setupDone());
        vm.expectRevert(HlAdmin.SetupAlreadyDone.selector);
        d.admin.finishSetup();
    }

    // ------------------------------------------------------------------------------------- 1–7: the owners --

    function test_finishSetup_refuses_anOwnerOrTheAdminLeftBehind() public {
        uint8[7] memory checks = [uint8(1), 2, 3, 4, 5, 6, 7];
        for (uint256 i = 0; i < 7; i++) {
            uint256 snap = vm.snapshotState();
            _upToOwnership(_cfg());
            _ownership(checks[i]);
            _expectRefused(checks[i]);
            vm.revertToState(snap);
        }
    }

    // ---------------------------------------------------------------------------------- 8: a proxy's admin --

    function test_finishSetup_refuses_aProxyUnderAnotherAdmin() public {
        _upToOwnership(_cfg());
        address another = makeAddr("another admin");
        vm.stopPrank();
        vm.prank(address(d.admin));
        d.proxyAdmin.changeProxyAdmin(ITransparentUpgradeableProxy(address(d.gate)), another);
        vm.startPrank(DEPLOYER);
        _ownership(0);
        _expectRefused(8);
    }

    // ------------------------------------------------------------------------ 9–15: the mailbox and tokens --

    function test_finishSetup_refuses_theMailboxsIsmOrHooks() public {
        uint256 snap = vm.snapshotState();
        _upToOwnership(_cfg());
        d.mailbox.setDefaultIsm(address(d.client));
        _ownership(0);
        _expectRefused(9);
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        d.mailbox.setDefaultHook(address(d.protocolFee));
        _ownership(0);
        _expectRefused(10);
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        d.mailbox.setRequiredHook(address(d.merkleTreeHook));
        _ownership(0);
        _expectRefused(11);
    }

    function test_finishSetup_refuses_aTokensIsmHookFeesOrRouter() public {
        uint256 snap = vm.snapshotState();
        _upToOwnership(_cfg());
        d.token0.setInterchainSecurityModule(address(d.client));
        _ownership(0);
        _expectRefused(12);
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        d.token1.setHook(address(d.merkleTreeHook));
        _ownership(0);
        _expectRefused(13);
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        d.token0.setFeeRecipient(makeAddr("fee recipient"));
        _ownership(0);
        _expectRefused(14);
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        d.token1.enrollRemoteRouter(GNO, bytes32(uint256(1)));
        _ownership(0);
        _expectRefused(15);
        // a launch's ISM is the gate itself (step 8): zero — kind 3's other value, which a successor's setup and a kind
        // 8 take (audit round 3, E3-CF10) — is refused at a launch
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        d.token1.setInterchainSecurityModule(address(0));
        _ownership(0);
        _expectRefused(12);
    }

    // ------------------------------------------------------------- 16–20: the fee, the gate, factors, origin --

    function test_finishSetup_refuses_anotherProtocolFeeMaximum() public {
        BridgeSetup.Config memory c = _cfg();
        c.maxProtocolFee = 0.01 ether;
        _upToOwnership(c);
        _ownership(0);
        _expectRefused(16);
    }

    function test_finishSetup_refuses_aGateBoundToAnotherClient() public {
        BridgeSetup.Config memory c = _cfg();
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        // a second client proxy, HlAdmin's (made by another key: the deployer's nonces are the plan's): the gate
        // still reads the first
        vm.stopPrank();
        vm.prank(makeAddr("another key"));
        d.client = GnoLightClient(
            address(
                new TransparentUpgradeableProxy(
                    d.clientImpl,
                    address(d.proxyAdmin),
                    abi.encodeCall(
                        GnoLightClient.initialize, (c.chainId, c.gnoDomain, c.trustedLeaves, c.validators, DEPLOYER)
                    )
                )
            )
        );
        vm.startPrank(DEPLOYER);
        d.admin = BridgeSetup.admin(c, d);
        BridgeSetup.settings(c, d);
        _ownership(0);
        _expectRefused(17);
    }

    function test_finishSetup_refuses_aGateWithAnotherGuardianList() public {
        BridgeSetup.Config memory c = _cfg();
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        c.watch[1] = makeAddr("another watch key");
        d.admin = BridgeSetup.admin(c, d);
        BridgeSetup.settings(c, d);
        _ownership(0);
        _expectRefused(18);
    }

    function test_finishSetup_refuses_aClientAtAnotherFactorOriginOrPath() public {
        uint256 snap = vm.snapshotState();
        _upToOwnership(_cfg());
        _upgrade(
            address(d.client),
            address(new MockClientViews(address(d.admin), CLOCK_FACTOR == 1 ? 8 : 1, GNO, _pkgPathHash()))
        );
        _ownership(0);
        _expectRefused(19);
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        _upgrade(
            address(d.client), address(new MockClientViews(address(d.admin), CLOCK_FACTOR, GNO + 1, _pkgPathHash()))
        );
        _ownership(0);
        _expectRefused(20);
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        _upgrade(
            address(d.client),
            address(new MockClientViews(address(d.admin), CLOCK_FACTOR, GNO, keccak256("another path")))
        );
        _ownership(0);
        _expectRefused(26);
    }

    // ---------------------------------------------------------------- 21–25: what immutables hold --

    function test_finishSetup_refuses_aTokenOverAnotherMailboxScaleDecimalsOrSupply() public {
        uint256 snap = vm.snapshotState();
        _upToOwnership(_cfg());
        Mailbox other = new Mailbox(LOCAL);
        _upgrade(address(d.token0), address(new HypERC20(6, 1, 1, address(other))));
        _ownership(0);
        _expectRefused(21);
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        _upgrade(address(d.token1), address(new HypERC20(6, 2, 1, address(d.mailbox))));
        _ownership(0);
        _expectRefused(22);
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        _upgrade(address(d.token0), address(new HypERC20(18, 1, 1, address(d.mailbox))));
        _ownership(0);
        _expectRefused(23);
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        _upgrade(address(d.token0), address(new HypERC20Supply(address(d.mailbox))));
        _ownership(0);
        _expectRefused(24);
    }

    function test_finishSetup_refuses_aMerkleTreeHookOverAnotherMailbox() public {
        BridgeSetup.Config memory c = _cfg();
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        // made by another key (the deployer's nonces are the plan's), handed to the deployer for step 10
        vm.stopPrank();
        vm.startPrank(makeAddr("another key"));
        d.merkleTreeHook = new MerkleTreeHook(address(new Mailbox(LOCAL)));
        d.merkleTreeHook.transferOwnership(DEPLOYER);
        vm.stopPrank();
        vm.startPrank(DEPLOYER);
        d.admin = BridgeSetup.admin(c, d);
        BridgeSetup.settings(c, d);
        _ownership(0);
        _expectRefused(25);
    }

    // ------------------------------------------------- 27–29: the verifier, its code, the local domains --

    /// 27: the client's implementation calls another verifier than HlAdmin's (the same code at another address).
    function test_finishSetup_refuses_aClientOverAnotherVerifier() public {
        _upToOwnership(_cfg());
        IEd25519Verifier other = new Ed25519Verifier();
        _upgrade(address(d.client), address(new GnoLightClient(_pkgPathHash(), other)));
        assertEq(address(d.client.ED25519()), address(other));
        _ownership(0);
        _expectRefused(27);
    }

    /// 28: the verifier the deployment names is not this tree's Ed25519Verifier (one that accepts every signature),
    /// though the client and HlAdmin agree on its address.
    function test_finishSetup_refuses_aVerifierWithAnotherCode() public {
        BridgeSetup.Config memory c = _cfg();
        c.ed25519 = address(new TrustingVerifier());
        _upToOwnership(c);
        assertEq(address(d.client.ED25519()), c.ed25519);
        assertEq(d.admin.ed25519(), c.ed25519);
        _ownership(0);
        _expectRefused(28);
    }

    /// A verifier already on the chain with this tree's code is taken as it is (no second one deployed), and passes.
    function test_finishSetup_takesAVerifierAlreadyOnTheChain() public {
        BridgeSetup.Config memory c = _cfg();
        c.ed25519 = address(new Ed25519Verifier());
        _upToOwnership(c);
        assertEq(address(d.ed25519), c.ed25519);
        _ownership(0);
        d.admin.finishSetup();
        assertTrue(d.admin.setupDone());
    }

    /// 29: the mailbox at another local domain than HlAdmin's (its implementation rebuilt for domain 1).
    function test_finishSetup_refuses_aMailboxAtAnotherDomain() public {
        _upToOwnership(_cfg());
        _upgrade(address(d.mailbox), address(new Mailbox(LOCAL + 1)));
        _ownership(0);
        _expectRefused(29);
    }

    // ------------------------------------------------------------- 30–31: the launch values in storage --

    /// 30: the gate initialized with other mint limits than HlAdmin's launch ones; 31: a protocol fee already set.
    function test_finishSetup_refuses_otherLimitsOrAFee() public {
        uint256 snap = vm.snapshotState();
        BridgeSetup.Config memory c = _cfg();
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        c.dayCap[1] += 1;
        d.admin = BridgeSetup.admin(c, d);
        BridgeSetup.settings(c, d);
        _ownership(0);
        _expectRefused(30);
        vm.revertToState(snap);
        _upToOwnership(_cfg());
        d.protocolFee.setProtocolFee(1);
        _ownership(0);
        _expectRefused(31);
    }

    // ------------------------------------------- 32–33 retired: a dispatch before the launch vetoes nothing --

    /// Once step 9 set the mailbox's hooks, `Mailbox.dispatch` is anyone's (audit round 2, E2-RT6): a stranger's
    /// dispatch before step 11 moves the mailbox's nonce and the MerkleTreeHook's count, and the launch goes on —
    /// finishSetup reads neither (checks 32 and 33, the nonce and the count at 0, are retired). A burn in the record
    /// before the launch is the signers' and the audit's to ignore: they start at finishSetup's block.
    function test_finishSetup_aStrangersDispatchVetoesNothing() public {
        _upToOwnership(_cfg());
        vm.stopPrank();
        vm.prank(makeAddr("a stranger"));
        d.mailbox.dispatch(GNO, ROUTE0, "");
        assertEq(d.mailbox.nonce(), 1);
        assertEq(d.merkleTreeHook.count(), 1);
        vm.startPrank(DEPLOYER);
        _ownership(0);
        d.admin.finishSetup();
        assertTrue(d.admin.setupDone());
    }

    // ------------------------------------------------------------ the ProxyAdmin at step 1, the nonce plan --

    /// A launch HlAdmin is never built unless the ProxyAdmin is already its own: created at another address (another
    /// key's), or at the planned one after the ProxyAdmin went elsewhere, its constructor refuses.
    function test_aLaunchHlAdminRequiresTheProxyAdmin() public {
        BridgeSetup.Config memory c = _cfg();
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        vm.stopPrank();
        assertEq(d.proxyAdmin.owner(), d.adminAt);
        assertEq(d.adminAt, vm.computeCreateAddress(DEPLOYER, 15), "HlAdmin's nonce: 15");
        HlAdmin.Config memory hc = BridgeSetup.adminConfig(c, d);
        address another = makeAddr("another key");
        address elsewhere = makeAddr("elsewhere");
        vm.prank(another);
        vm.expectRevert(HlAdmin.ProxyAdminNotHeld.selector);
        new HlAdmin(hc);
        vm.prank(d.adminAt);
        d.proxyAdmin.transferOwnership(elsewhere);
        vm.resetNonce(d.adminAt); // the prank's call took a nonce; HlAdmin's address has no key, no transaction
        vm.prank(DEPLOYER);
        vm.expectRevert(HlAdmin.ProxyAdminNotHeld.selector);
        new HlAdmin(hc);
    }

    /// A launch starts where r/hlgov can reach (audit round 3, E3-GS4): r/hlgov's counter starts at 0 on a chain's first
    /// deployment and only a `sync sequence` raises it, by at most 2^32 a proposal (its syncReach), so HlAdmin's start
    /// is at most 2^32 — one sync from any counter at or below it. A start above (a deployer's typo, or 2^64−1: every
    /// decision ignored for good, cancels included) is refused (`OutOfOrder`) before HlAdmin exists; 2^32 itself is
    /// taken, its first decision 2^32 + 1.
    function test_aLaunchStartBeyondHlgovsReachIsRefused() public {
        BridgeSetup.Config memory c = _cfg();
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        vm.stopPrank();
        HlAdmin.Config memory hc = BridgeSetup.adminConfig(c, d);
        uint64[2] memory beyond = [(uint64(1) << 32) + 1, type(uint64).max];
        for (uint256 i = 0; i < 2; i++) {
            uint256 snap = vm.snapshotState();
            hc.startSequence = beyond[i];
            vm.prank(DEPLOYER);
            vm.expectRevert(HlAdmin.OutOfOrder.selector);
            new HlAdmin(hc);
            vm.revertToState(snap);
        }
        hc.startSequence = uint64(1) << 32;
        vm.prank(DEPLOYER);
        HlAdmin a = new HlAdmin(hc);
        assertEq(address(a), d.adminAt, "at the planned nonce");
        assertEq(a.lastSeq(), uint64(1) << 32, "the start, r/hlgov's counter after one sync from 0");
    }

    /// Step 1–6 as an external call (so a test can expect the library's refusal), by whoever calls it.
    function stepsOneToSix(BridgeSetup.Config memory c) external {
        d = BridgeSetup.contracts(c);
    }

    /// Step 7 as an external call.
    function stepSeven(BridgeSetup.Config memory c) external {
        d.admin = BridgeSetup.admin(c, d);
    }

    /// The setup stops where the deployer's nonce leaves the plan, before it wires anything to an address HlAdmin
    /// will not have: a ProxyAdmin another key created (refused at step 1, before the hand-over), and a transaction of
    /// the deployer's between step 6 and step 7 (refused at step 7, before HlAdmin is created).
    function test_theSetupStopsOffItsNoncePlan() public {
        BridgeSetup.Config memory c = _cfg();
        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSetup.NoncePlan.selector, "step 1: the ProxyAdmin, created by the deployer", 1, 0
            )
        );
        this.stepsOneToSix(c);
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        assertEq(d.adminNonce, 15);
        d.protocolFee.setBeneficiary(treasurer); // the deployer's nonce 15
        vm.stopPrank();
        vm.expectRevert(abi.encodeWithSelector(BridgeSetup.NoncePlan.selector, "step 7: HlAdmin", 15, 16));
        this.stepSeven(c);
        assertEq(d.proxyAdmin.owner(), d.adminAt, "the ProxyAdmin waits at an address nothing will be created at");
        assertEq(d.adminAt.code.length, 0);
    }

    /// Under a broadcast (the local run's `Setup.run`) the plan is the same: every call and creation is a transaction
    /// that takes a nonce, HlAdmin lands at the fresh key's nonce 15 and owns the ProxyAdmin from step 1, and the
    /// setup finishes.
    function test_theSetupUnderABroadcast() public {
        BridgeSetup.Config memory c = _cfg();
        uint256 key = 0xb0b0;
        c.deployer = vm.addr(key);
        vm.startBroadcast(key);
        d = BridgeSetup.deploy(c);
        vm.stopBroadcast();
        assertEq(d.adminNonce, 15);
        assertEq(address(d.admin), vm.computeCreateAddress(c.deployer, 15));
        assertEq(d.proxyAdmin.owner(), address(d.admin));
        assertTrue(d.admin.setupDone());
    }
}

// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Vm} from "forge-std/Vm.sol";
import {ITransparentUpgradeableProxy} from "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol";

import {BridgeSetup} from "../script/Setup.s.sol";
import {GnoLightClient} from "../src/GnoLightClient.sol";
import {IGnoLightClient} from "../src/interfaces/IGnoLightClient.sol";
import {AdminBase} from "./helpers/AdminBase.sol";
import {TestChain} from "./helpers/TestChain.sol";

/// Code the deployer would point a proxy at for one call: it writes raw storage (audit round 2's E2-CT1).
contract SlotWriter {
    function write(bytes32[] calldata slots, bytes32[] calldata values) external {
        for (uint256 i = 0; i < slots.length; i++) {
            bytes32 s = slots[i];
            bytes32 v = values[i];
            assembly {
                sstore(s, v)
            }
        }
    }
}

/// An ISM that verifies everything: a stand-in default ISM before step 9.
contract YesIsm {
    function moduleType() external pure returns (uint8) {
        return 0;
    }

    function verify(bytes calldata, bytes calldata) external pure returns (bool) {
        return true;
    }
}

/// The setup's trust window (audit round 2, E2-CT1: eth/bridge/audits/r2/contracts.md): the four proofs of concept of
/// the deployer's interim powers, ported. Scenarios (a)–(c) needed the ProxyAdmin, which is HlAdmin's from step 1
/// now (its CREATE address, before any proxy exists), so the deployer's upgrade is refused and nothing can be
/// planted; each test then finishes the setup and shows the attack's payoff fails too. Scenario (d) needs no upgrade
/// (the deployer still owns the mailbox and the tokens until step 10): its trace, a dispatch before the launch, is
/// left out off chain — the signers start at finishSetup's block — since a check of it on chain let any stranger's
/// dispatch veto the launch (audit round 2, E2-RT6).
contract SetupTrustTest is AdminBase {
    address internal attacker = makeAddr("attacker");
    bytes internal constant NOT_OWNER = "Ownable: caller is not the owner";

    function _start() internal returns (BridgeSetup.Config memory c) {
        vm.warp(1_791_000_000);
        vm.roll(1_000);
        chainTime = 1_791_000_000;
        IGnoLightClient.Validator[] memory v = TestChain.validators(1, 4, 60);
        bytes[] memory l = TestChain.leaves(_header(100, v, v, bytes32(0)));
        bytes32 hh = TestChain.hash(l);
        c = _config(l, v);
        _setChainSet(v);
        chainHeight = 100;
        chainHash = hh;
    }

    /// HlAdmin's CREATE address in this suite's deployment: the deployer's nonce 15 (the ProxyAdmin, its hand-over,
    /// then thirteen creations: the setup deploys the verifier at step 4).
    function _hlAdminAt() internal pure returns (address) {
        return vm.computeCreateAddress(DEPLOYER, 15);
    }

    /// Steps 1–7 as the deployer, its prank left running: the deployer still owns the mailbox, the tokens, the gate
    /// and the hooks, and every transaction of its own from here on no longer moves HlAdmin's address.
    function _toStep7(BridgeSetup.Config memory c) internal {
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        d.admin = BridgeSetup.admin(c, d);
    }

    /// Steps 8–11, the deployer's prank stopped before step 11.
    function _finish(BridgeSetup.Config memory c) internal {
        BridgeSetup.settings(c, d);
        BridgeSetup.ownership(d);
        vm.stopPrank();
        d.admin.finishSetup();
        assertTrue(d.admin.setupDone());
        assertEq(address(d.admin), _hlAdminAt(), "HlAdmin where the nonce plan put it");
    }

    /// The deployer's one-call detour through `w` (the proof of concept's `_rewrite`): refused, the ProxyAdmin not
    /// being the deployer's.
    function _refusedRewrite(address proxy, SlotWriter w, bytes32[] memory slots, bytes32[] memory values) internal {
        vm.expectRevert(NOT_OWNER);
        d.proxyAdmin
            .upgradeAndCall(
                ITransparentUpgradeableProxy(proxy), address(w), abi.encodeCall(SlotWriter.write, (slots, values))
            );
    }

    /// The ProxyAdmin is handed to HlAdmin's address at step 1, before the first proxy exists: in the logs of steps
    /// 1–6, its OwnershipTransferred(deployer → HlAdmin) precedes every proxy's AdminChanged and Upgraded, and no
    /// proxy is upgraded past its construction.
    function test_theProxyAdminIsHlAdminsBeforeAnyProxyExists() public {
        BridgeSetup.Config memory c = _start();
        vm.recordLogs();
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        vm.stopPrank();
        Vm.Log[] memory logs = vm.getRecordedLogs();
        bytes32 transferred = keccak256("OwnershipTransferred(address,address)");
        bytes32 adminChanged = keccak256("AdminChanged(address,address)");
        bytes32 upgraded = keccak256("Upgraded(address)");
        uint256 handedAt = type(uint256).max;
        uint256 firstProxy = type(uint256).max;
        uint256 upgrades;
        for (uint256 i = 0; i < logs.length; i++) {
            Vm.Log memory l = logs[i];
            if (
                l.emitter == address(d.proxyAdmin) && l.topics[0] == transferred
                    && address(uint160(uint256(l.topics[2]))) == _hlAdminAt()
            ) {
                handedAt = i;
            }
            if ((l.topics[0] == adminChanged || l.topics[0] == upgraded) && firstProxy == type(uint256).max) {
                firstProxy = i;
            }
            if (l.topics[0] == upgraded) {
                upgrades++;
            }
        }
        assertLt(handedAt, firstProxy, "the ProxyAdmin is HlAdmin's before the first proxy exists");
        assertEq(upgrades, 5, "each of the five proxies upgraded once: its construction");
        assertEq(d.proxyAdmin.owner(), _hlAdminAt());
    }

    /// (a) A planted client entry would prove any message: a mint through the gate, a decision with no gate at all.
    /// The deployer can no longer point the client at code of its own (here tried after step 7, when its transactions
    /// no longer move HlAdmin's address; before, the ProxyAdmin is an address without code); after the setup the
    /// forged mint and the forged decision find no entry.
    function test_aPlantedClientEntryCannotBeMounted() public {
        BridgeSetup.Config memory c = _start();
        SlotWriter w = new SlotWriter();
        _toStep7(c);
        assertEq(d.proxyAdmin.owner(), address(d.admin), "the ProxyAdmin is HlAdmin's");
        bytes memory mint = TestChain.message(
            777,
            GNO,
            ROUTE0,
            LOCAL,
            bytes32(uint256(uint160(address(d.token0)))),
            TestChain.tokenBody(attacker, MAX_PER_MESSAGE)
        );
        // kind 12: token 0's limits to the maximum (any 7-day kind the same: 8 upgrades a token)
        bytes memory decision = TestChain.message(
            778,
            GNO,
            HLGOV,
            LOCAL,
            bytes32(uint256(uint160(address(d.admin)))),
            abi.encodePacked(uint8(12), uint64(1), uint8(0), type(uint64).max, type(uint64).max)
        );
        bytes[] memory ms = new bytes[](2);
        ms[0] = mint;
        ms[1] = decision;
        bytes[] memory l = new bytes[](1);
        l[0] = TestChain.dispatchLeaf(ms, MAILBOX_PATH);
        bytes32 forged = keccak256("a header gno.land never made");
        bytes32 base = keccak256(abi.encode(forged, uint256(9))); // _entries is slot 9
        bytes32[] memory slots = new bytes32[](2);
        bytes32[] memory values = new bytes32[](2);
        slots[0] = base;
        values[0] = bytes32(uint256(1) << 64); // epoch 0, height 1
        slots[1] = bytes32(uint256(base) + 1);
        values[1] = TestChain.root(l); // lastResultsHash
        _refusedRewrite(address(d.client), w, slots, values);

        _finish(c);
        assertEq(d.client.records(), 1);
        assertEq(d.client.entry(forged).height, 0, "no entry was planted");
        bytes memory meta = TestChain.metadata(forged, l, 0);
        vm.prank(relayer, relayer);
        vm.expectRevert(GnoLightClient.UnknownHeader.selector);
        d.mailbox.process(meta, mint);
        assertEq(d.token0.balanceOf(attacker), 0, "nothing minted");
        vm.prank(relayer, relayer);
        vm.expectRevert(GnoLightClient.UnknownHeader.selector);
        d.mailbox.process(meta, decision);
        assertFalse(_pending(1), "no decision recorded");
        assertEq(d.admin.lastSeq(), 0);
    }

    /// (b) A planted token balance would keep totalSupply 0 (check 24) for a later burn into the lock: refused the
    /// same way.
    function test_aPlantedTokenBalanceCannotBeMounted() public {
        BridgeSetup.Config memory c = _start();
        SlotWriter w = new SlotWriter();
        _toStep7(c);
        bytes32[] memory slots = new bytes32[](1);
        bytes32[] memory values = new bytes32[](1);
        slots[0] = keccak256(abi.encode(attacker, uint256(51))); // _balances is slot 51
        values[0] = bytes32(uint256(1e15));
        _refusedRewrite(address(d.token0), w, slots, values);
        _finish(c);
        assertEq(d.token0.totalSupply(), 0);
        assertEq(d.token0.balanceOf(attacker), 0, "no balance planted");
    }

    /// (c) A cleared initializer flag would let anyone re-initialize the gate after the setup and mint through it with
    /// a client of their own: refused the same way, and after the setup every proxy's initializer stays closed.
    function test_aReopenedInitializerCannotBeMounted() public {
        BridgeSetup.Config memory c = _start();
        SlotWriter w = new SlotWriter();
        _toStep7(c);
        bytes32[] memory slots = new bytes32[](1);
        bytes32[] memory values = new bytes32[](1);
        slots[0] = bytes32(0); // _initialized, _initializing
        values[0] = bytes32(0);
        _refusedRewrite(address(d.gate), w, slots, values);
        _finish(c);

        address[4] memory seats = [makeAddr("s1"), makeAddr("s2"), makeAddr("s3"), makeAddr("s4")];
        address[2] memory watch = [makeAddr("w1"), makeAddr("w2")];
        vm.prank(attacker);
        vm.expectRevert("Initializable: contract is already initialized");
        d.gate
            .initialize(
                attacker,
                address(d.mailbox),
                attacker,
                [address(d.token0), address(d.token1)],
                seats,
                watch,
                [type(uint64).max, type(uint64).max],
                [type(uint64).max, type(uint64).max]
            );
        vm.prank(attacker);
        vm.expectRevert("Initializable: contract is already initialized");
        d.token0.initialize(1e18, "x", "x", address(0), address(0), attacker);
        vm.prank(attacker);
        vm.expectRevert("Initializable: contract is already initialized");
        d.mailbox.initialize(attacker, attacker, attacker, attacker);
        assertEq(d.gate.owner(), address(d.admin));
        assertEq(d.token0.totalSupply(), 0);
    }

    /// HlAdmin's launch line, read by a low-level call (whether it answered; the nonce).
    function _launchNonce() internal view returns (bool ok, uint256 nonce) {
        bytes memory r;
        (ok, r) = address(d.admin).staticcall(abi.encodeWithSignature("launchNonce()"));
        if (ok && r.length == 32) {
            nonce = abi.decode(r, (uint256));
        } else {
            ok = false;
        }
    }

    /// (d) With no upgrade at all — as the mailbox's and token 0's owner until step 10 — the deployer mints through a
    /// stand-in default ISM and burns it straight back toward gno.land's route, after step 7 (before it, any
    /// transaction of the deployer's moves HlAdmin off its nonce plan and the setup stops at step 7): the supply is 0
    /// again (check 24 passes), but a burn sits in the mailbox's record. The contracts no longer refuse it: a check of
    /// the mailbox's nonce (32, retired) let any stranger's dispatch veto the launch (audit round 2, E2-RT6). They draw
    /// the line instead: finishSetup records, at a launch, the mailbox's nonce, and every message below it is no burn of
    /// the bridge's — the signers, the monitors and Proposal 2 read the line from HlAdmin, which no block number would
    /// draw exactly (this burn could sit in finishSetup's own block, before it). A burn after the launch lies at or
    /// above it.
    function test_aPreSetupMintAndBurnLiesBelowTheLaunchLine() public {
        BridgeSetup.Config memory c = _start();
        YesIsm yes = new YesIsm();
        _toStep7(c);
        d.mailbox.setDefaultIsm(address(yes));
        d.mailbox.setDefaultHook(address(d.merkleTreeHook));
        d.mailbox.setRequiredHook(address(d.protocolFee));
        d.token0.enrollRemoteRouter(GNO, ROUTE0);
        bytes memory mint = TestChain.message(
            9, GNO, ROUTE0, LOCAL, bytes32(uint256(uint160(address(d.token0)))), TestChain.tokenBody(DEPLOYER, 25_000e6)
        );
        d.mailbox.process("", mint); // token 0's ISM is still 0: the mailbox's default, the stand-in
        assertEq(d.token0.totalSupply(), 25_000e6);
        uint32 early = d.mailbox.nonce();
        d.token0.transferRemote(GNO, bytes32(uint256(0xdead)), 25_000e6);
        assertEq(d.token0.totalSupply(), 0);
        assertEq(d.mailbox.nonce(), early + 1, "one burn dispatched toward gno.land's route, at nonce `early`");
        assertEq(d.merkleTreeHook.count(), 1, "its leaf at index 0");
        BridgeSetup.settings(c, d); // steps 8-9 overwrite the ISMs and hooks
        BridgeSetup.ownership(d);
        vm.stopPrank();
        d.admin.finishSetup();
        assertTrue(d.admin.setupDone(), "finishSetup reads no nonce or count");
        (bool ok, uint256 launchNonce) = _launchNonce();
        assertTrue(ok, "HlAdmin draws the launch line");
        assertGt(launchNonce, early, "the burn lies below the line");
        // a burn after the launch: a real mint through the gate and the client, then a burn back
        _mint(0, attacker, 7e6);
        uint32 late = d.mailbox.nonce();
        vm.prank(attacker);
        d.token0.transferRemote(GNO, bytes32(uint256(0xbeef)), 7e6);
        assertGe(late, launchNonce, "a burn after the launch lies at or above the line");
        assertEq(d.merkleTreeHook.count(), late + 1);
    }

    /// Step 7 as an external call (so the test can expect the library's refusal).
    function stepSeven(BridgeSetup.Config memory c) external {
        d.admin = BridgeSetup.admin(c, d);
    }

    /// (d) before step 7: every transaction of the deployer's (here the six of the mint and the burn) moves HlAdmin
    /// off the address the ProxyAdmin was handed to, so the setup stops at step 7 and the deployment never comes to be.
    function test_aMintAndBurnBeforeStep7StopsTheSetup() public {
        BridgeSetup.Config memory c = _start();
        YesIsm yes = new YesIsm();
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        d.mailbox.setDefaultIsm(address(yes));
        d.mailbox.setDefaultHook(address(d.merkleTreeHook));
        d.mailbox.setRequiredHook(address(d.protocolFee));
        d.token0.enrollRemoteRouter(GNO, ROUTE0);
        d.mailbox
            .process(
                "",
                TestChain.message(
                    9,
                    GNO,
                    ROUTE0,
                    LOCAL,
                    bytes32(uint256(uint160(address(d.token0)))),
                    TestChain.tokenBody(DEPLOYER, 25_000e6)
                )
            );
        d.token0.transferRemote(GNO, bytes32(uint256(0xdead)), 25_000e6);
        vm.stopPrank();
        vm.expectRevert(abi.encodeWithSelector(BridgeSetup.NoncePlan.selector, "step 7: HlAdmin", 15, 21));
        this.stepSeven(c);
        assertEq(_hlAdminAt().code.length, 0, "no HlAdmin: the ProxyAdmin's owner never gets code");
    }
}

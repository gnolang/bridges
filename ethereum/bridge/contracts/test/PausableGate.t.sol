// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Test} from "forge-std/Test.sol";
import {ProxyAdmin} from "@openzeppelin/contracts/proxy/transparent/ProxyAdmin.sol";
import {TransparentUpgradeableProxy} from "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol";

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {PausableGate} from "../src/PausableGate.sol";
import {IPausableGate} from "../src/interfaces/IPausableGate.sol";
import {Guardians} from "../src/lib/Guardians.sol";
import {TestChain} from "./helpers/TestChain.sol";

/// A client that answers what the test sets.
contract MockClient {
    uint64 public quarantineEnd;
    bool public refuse;
    uint256 public verifies;

    function set(uint64 q, bool r) external {
        (quarantineEnd, refuse) = (q, r);
    }

    function verify(bytes calldata, bytes calldata) external returns (bool) {
        require(!refuse, "the client refuses");
        verifies++;
        return true;
    }

    function moduleType() external pure returns (uint8) {
        return 0;
    }
}

/// The gate on its own (the Solana suite's pausable.rs and limits.rs, every case that applies): this test is its
/// owner and its mailbox; the client is a mock.
contract PausableGateTest is Test {
    PausableGate internal gate;
    MockClient internal client;
    address internal token0 = makeAddr("token0");
    address internal token1 = makeAddr("token1");
    address[6] internal k;
    uint64 internal constant MAX = 25_000_000_000;
    uint64 internal constant CAP = 100_000_000_000;
    uint64 internal constant DAY = 1 days / CLOCK_FACTOR;

    function setUp() public {
        vm.warp(1_791_000_000);
        vm.roll(100);
        client = new MockClient();
        k = [
            makeAddr("seat1"),
            makeAddr("seat2"),
            makeAddr("seat3"),
            makeAddr("seat4"),
            makeAddr("watch1"),
            makeAddr("watch2")
        ];
        gate = _gate(address(this), [k[0], k[1], k[2], k[3]], [k[4], k[5]]);
    }

    function _gate(address owner, address[4] memory seats, address[2] memory watch) internal returns (PausableGate) {
        PausableGate impl = new PausableGate();
        ProxyAdmin pa = new ProxyAdmin();
        return PausableGate(
            address(
                new TransparentUpgradeableProxy(
                    address(impl),
                    address(pa),
                    abi.encodeCall(
                        PausableGate.initialize,
                        (owner, address(this), address(client), [token0, token1], seats, watch, [MAX, MAX], [CAP, CAP])
                    )
                )
            )
        );
    }

    function _flag(uint256 i) internal {
        vm.prank(k[i]);
        gate.setFlag();
    }

    function _clear(uint256 i) internal {
        vm.prank(k[i]);
        gate.clearFlag();
    }

    function _next(uint256 blocks, uint256 secs) internal {
        vm.roll(block.number + blocks);
        vm.warp(block.timestamp + secs);
    }

    // ------------------------------------------------------------------------------------------------ init --

    function test_init_runsOnceAndRefusesBadLists() public {
        assertEq(gate.owner(), address(this));
        assertEq(gate.mailbox(), address(this));
        assertEq(gate.client(), address(client));
        assertEq(gate.token(0), token0);
        assertEq(gate.token(1), token1);
        assertEq(gate.clockFactor(), CLOCK_FACTOR);
        assertEq(gate.moduleType(), 0);
        address[6] memory keys = gate.keys();
        for (uint256 i = 0; i < 6; i++) {
            assertEq(keys[i], k[i]);
        }
        IPausableGate.MintLimit memory l = gate.limits(0);
        assertEq(l.maxPerMessage, MAX);
        assertEq(l.dayCap, CAP);
        vm.expectRevert("Initializable: contract is already initialized");
        gate.initialize(
            address(this),
            address(this),
            address(client),
            [token0, token1],
            [k[0], k[1], k[2], k[3]],
            [k[4], k[5]],
            [MAX, MAX],
            [CAP, CAP]
        );
        PausableGate impl = new PausableGate();
        vm.expectRevert("Initializable: contract is already initialized");
        impl.initialize(
            address(this),
            address(this),
            address(client),
            [token0, token1],
            [k[0], k[1], k[2], k[3]],
            [k[4], k[5]],
            [MAX, MAX],
            [CAP, CAP]
        );
        // a key in two places, a zero key
        vm.expectRevert(Guardians.DuplicateKey.selector);
        this.deployGate([k[0], k[1], k[2], k[3]], [k[0], k[5]]);
        vm.expectRevert(Guardians.UnknownKey.selector);
        this.deployGate([k[0], address(0), k[2], k[3]], [k[4], k[5]]);
    }

    function deployGate(address[4] memory seats, address[2] memory watch) external returns (PausableGate) {
        return _gate(address(this), seats, watch);
    }

    // ------------------------------------------------------------------------------ flags, holds, unpause --

    /// pausable.rs `flags_holds_unpause_and_replacement`, on Ethereum's blocks.
    function test_flagsHoldsUnpauseAndReplacement() public {
        address stranger = makeAddr("stranger");
        vm.prank(stranger);
        vm.expectRevert(Guardians.UnknownKey.selector);
        gate.setFlag();
        // seat 1 and watch 5 flag; the owner's unpause before a later block clears both
        _flag(0);
        _next(1, 12);
        _flag(4);
        IPausableGate.Flag[6] memory f = gate.flags();
        assertEq(f[4].blockNumber, block.number, "a flag carries the block it was set at");
        assertEq(f[0].blockNumber, block.number - 1);
        _next(10, 120);
        vm.prank(stranger);
        vm.expectRevert("Ownable: caller is not the owner");
        gate.unpause(uint64(block.number));
        assertEq(gate.unpause(uint64(block.number)), 2);
        f = gate.flags();
        assertEq(f[0].blockNumber + f[4].blockNumber, 0);
        // two guardians: a hold, which refuses the unpause
        _flag(2);
        _next(5, 60);
        _flag(3);
        IPausableGate.Flag memory since = gate.hold();
        assertEq(since.blockNumber, block.number, "the hold began when the second guardian flag landed");
        _next(5, 60);
        vm.expectRevert(Guardians.Hold.selector);
        gate.unpause(uint64(block.number));
        // a key clears its own (a dip) and re-sets within the grace: a fresh flag block, the start unchanged
        _clear(3);
        assertEq(gate.hold().blockNumber, since.blockNumber, "a dip shorter than the grace keeps the hold");
        assertEq(gate.dipSince().blockNumber, block.number);
        _next(100, 1200);
        _flag(3);
        assertEq(gate.flags()[3].blockNumber, block.number);
        assertEq(gate.holdSince().blockNumber, since.blockNumber);
        assertEq(gate.dipSince().blockNumber, 0);
        // three keys taking turns never move the start
        _flag(1);
        _clear(2);
        _flag(2);
        _clear(1);
        assertEq(gate.holdSince().blockNumber, since.blockNumber);
        // a dip of the grace's length ends the hold: the unpause goes through
        _clear(3);
        _next(10, DAY - 60);
        vm.expectRevert(Guardians.Hold.selector);
        gate.unpause(uint64(block.number));
        _next(10, 120);
        assertEq(gate.hold().blockNumber, 0, "read after the grace: ended");
        gate.unpause(uint64(block.number));
        f = gate.flags();
        for (uint256 i = 0; i < 6; i++) {
            assertEq(f[i].blockNumber, 0);
        }
        assertEq(gate.holdSince().blockNumber, 0);
        // the hold again, for the replacement
        _flag(2);
        _flag(3);
        assertTrue(gate.hold().blockNumber != 0);
        address n1 = makeAddr("n1");
        address n2 = makeAddr("n2");
        address n3 = makeAddr("n3");
        address n4 = makeAddr("n4");
        address n5 = makeAddr("n5");
        vm.expectRevert(Guardians.CurrentKey.selector);
        gate.setGuardians(_pairs(3, k[2]));
        IPausableGate.SeatKey[] memory three = new IPausableGate.SeatKey[](3);
        (three[0], three[1], three[2]) =
        (IPausableGate.SeatKey(1, n1), IPausableGate.SeatKey(2, n2), IPausableGate.SeatKey(3, n3));
        vm.expectRevert(Guardians.TooManyGuardians.selector);
        gate.setGuardians(three);
        vm.expectRevert(Guardians.DuplicateKey.selector);
        gate.setGuardians(_pairs(3, k[0]));
        (three[0], three[1], three[2]) =
        (IPausableGate.SeatKey(3, n3), IPausableGate.SeatKey(4, n4), IPausableGate.SeatKey(5, n5));
        vm.prank(stranger);
        vm.expectRevert("Ownable: caller is not the owner");
        gate.setGuardians(three);
        gate.setGuardians(three);
        address[6] memory keys = gate.keys();
        assertEq(keys[2], n3);
        assertEq(keys[3], n4);
        assertEq(keys[4], n5);
        f = gate.flags();
        for (uint256 i = 0; i < 6; i++) {
            assertEq(f[i].blockNumber, 0);
        }
        assertEq(gate.holdSince().blockNumber, 0, "the replacement ends the hold at once");
        // the old key is unknown now
        vm.prank(k[2]);
        vm.expectRevert(Guardians.UnknownKey.selector);
        gate.setFlag();
        // the owner moves, and only the new owner unpauses; nothing makes the gate ownerless
        vm.expectRevert(PausableGate.NoRenounce.selector);
        gate.renounceOwnership();
        address next = makeAddr("next owner");
        gate.transferOwnership(next);
        vm.expectRevert("Ownable: caller is not the owner");
        gate.unpause(uint64(block.number));
        vm.prank(next);
        gate.unpause(uint64(block.number));
    }

    function _pairs(uint8 seat, address key) internal pure returns (IPausableGate.SeatKey[] memory p) {
        p = new IPausableGate.SeatKey[](1);
        p[0] = IPausableGate.SeatKey(seat, key);
    }

    /// An unpause clears only the flags last set before the block it names: a flag set at that block stands.
    function test_unpause_clearsOnlyFlagsSetBeforeItsBlock() public {
        _flag(0);
        uint64 b = uint64(block.number);
        _next(1, 12);
        _flag(5);
        assertEq(gate.unpause(b), 0, "set at b, not before it");
        assertEq(gate.unpause(b + 1), 1);
        assertEq(gate.flags()[5].blockNumber, block.number, "the later flag stands");
        // a flag's block is fresh on every set
        _next(1, 12);
        _flag(5);
        assertEq(gate.unpause(uint64(block.number)), 0);
    }

    // ------------------------------------------------------------------------------------------- verify --

    function _token(address to, uint256 amount) internal pure returns (bytes memory) {
        return TestChain.tokenBody(to, amount);
    }

    function _msg(address recipient, bytes memory body) internal pure returns (bytes memory) {
        return TestChain.message(1, 1198419796, bytes32(uint256(5)), 31337, bytes32(uint256(uint160(recipient))), body);
    }

    function _verify(address token, uint256 amount) internal returns (bool) {
        return gate.verify("", _msg(token, _token(address(7), amount)));
    }

    /// limits.rs `mint_limits_meter_deliveries_and_only_deliveries`: the per-message maximum, the rolling day, a
    /// bucket per token, unmetered recipients, bad token messages, the owner's setter.
    function test_mintLimits() public {
        assertTrue(_verify(token0, MAX));
        assertEq(gate.limits(0).used, MAX);
        vm.expectRevert(PausableGate.OverMax.selector);
        _verify(token0, MAX + 1);
        for (uint256 i = 0; i < 3; i++) {
            _verify(token0, MAX);
        }
        assertEq(gate.limits(0).used, CAP);
        vm.expectRevert(PausableGate.OverDayCap.selector);
        _verify(token0, 1);
        // the other token's bucket is its own
        _verify(token1, MAX);
        assertEq(gate.limits(1).used, MAX);
        // a quarter of a day on, a quarter of the cap drained: the maximum passes once more, one unit more does not
        vm.warp(block.timestamp + DAY / 4);
        _verify(token0, MAX);
        vm.expectRevert(PausableGate.OverDayCap.selector);
        _verify(token0, 1);
        // a whole day on, the bucket is empty
        vm.warp(block.timestamp + DAY);
        _verify(token0, MAX);
        assertEq(gate.limits(0).used, MAX);
        // a recipient that is no token of ours: unmetered, whatever its body
        assertTrue(gate.verify("", _msg(makeAddr("noop"), _token(address(7), type(uint256).max))));
        assertTrue(gate.verify("", _msg(makeAddr("noop"), "not a token message")));
        // a message to a token that is not a token message
        vm.expectRevert(PausableGate.BadTokenMessage.selector);
        gate.verify("", _msg(token0, "short"));
        vm.expectRevert(PausableGate.BadTokenMessage.selector);
        gate.verify("", _msg(token0, _token(address(7), uint256(1) << 64)));
        // the setter: the owner lowers token 0's maximum and day; the level stands
        vm.prank(makeAddr("stranger"));
        vm.expectRevert("Ownable: caller is not the owner");
        gate.setMintLimits(0, 1, 1);
        vm.expectRevert(PausableGate.BadToken.selector);
        gate.setMintLimits(2, 1, 1);
        gate.setMintLimits(0, 10_000_000_000, 30_000_000_000);
        IPausableGate.MintLimit memory l = gate.limits(0);
        assertEq(l.maxPerMessage, 10_000_000_000);
        assertEq(l.dayCap, 30_000_000_000);
        assertEq(l.used, MAX);
        assertEq(gate.limits(1).maxPerMessage, MAX, "the other token's untouched");
        vm.expectRevert(PausableGate.OverMax.selector);
        _verify(token0, 20_000_000_000);
        vm.expectRevert(PausableGate.OverDayCap.selector);
        _verify(token0, 10_000_000_000);
        vm.warp(block.timestamp + DAY);
        _verify(token0, 10_000_000_000);
    }

    /// Only the mailbox; nothing under a quarantine or a flag; the client's refusal undoes the charge.
    function test_verify_onlyTheMailboxAndNothingStanding() public {
        bytes memory m = _msg(token0, _token(address(7), 5));
        vm.prank(makeAddr("stranger"));
        vm.expectRevert(PausableGate.NotTheMailbox.selector);
        gate.verify("", m);
        assertEq(gate.limits(0).used, 0, "a direct call charges nothing");
        // a quarantine standing refuses; at its end exactly, it passes
        client.set(uint64(block.timestamp + 1), false);
        vm.expectRevert(PausableGate.Quarantine.selector);
        gate.verify("", m);
        client.set(uint64(block.timestamp), false);
        assertTrue(gate.verify("", m));
        // a lone watch key's flag refuses
        _flag(5);
        vm.expectRevert(PausableGate.Paused.selector);
        gate.verify("", m);
        _clear(5);
        assertTrue(gate.verify("", m));
        // the client refuses: the charge is undone with the call
        uint64 used = gate.limits(0).used;
        client.set(0, true);
        vm.expectRevert("the client refuses");
        gate.verify("", m);
        assertEq(gate.limits(0).used, used);
        vm.expectRevert(PausableGate.BadMessage.selector);
        gate.verify("", hex"03");
    }
}

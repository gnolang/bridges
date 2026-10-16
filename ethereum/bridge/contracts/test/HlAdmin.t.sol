// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {HlAdmin} from "../src/HlAdmin.sol";
import {HlAdminStorage} from "../src/admin/HlAdminStorage.sol";
import {IHlAdmin} from "../src/interfaces/IHlAdmin.sol";
import {IPausableGate} from "../src/interfaces/IPausableGate.sol";
import {Guardians} from "../src/lib/Guardians.sol";
import {AdminBase} from "./helpers/AdminBase.sol";
import {TestChain} from "./helpers/TestChain.sol";

/// HlAdmin's lifecycle (the Solana suite's admin.rs, every case that applies on Ethereum): decisions arrive through
/// the real mailbox and client from r/hlgov, are recorded by number, run on their clocks, wait under flags and holds,
/// are cancelled, spent or executed.
contract HlAdminTest is AdminBase {
    function setUp() public {
        _world();
    }

    function _admin() internal view returns (address) {
        return address(d.admin);
    }

    function _prove2(bytes memory a, bytes memory b) internal returns (bytes[] memory) {
        bytes[] memory ms = new bytes[](2);
        (ms[0], ms[1]) = (a, b);
        return _prove(ms);
    }

    function _processExpectRevert(bytes memory meta, bytes memory m, bytes memory err) internal {
        vm.prank(relayer, relayer);
        vm.expectRevert(err);
        d.mailbox.process(meta, m);
    }

    // ------------------------------------------------------------------------------------------ recording --

    /// Number 2 before 1 is refused (it stays deliverable); 1 then 2 land; 1 again, a fresh message of the same
    /// number, is accepted and ignored.
    function test_recording_theSequence() public {
        bytes memory m2 = _message(HLGOV, _admin(), _body(7, 2, _fee(1000, treasurer)));
        bytes memory m1 = _message(HLGOV, _admin(), _body(7, 1, _fee(500, treasurer)));
        bytes[] memory meta = _prove2(m2, m1);
        _processExpectRevert(meta[0], m2, abi.encodeWithSelector(HlAdmin.OutOfOrder.selector));
        assertFalse(d.mailbox.delivered(keccak256(m2)), "still deliverable");
        vm.prank(relayer, relayer);
        d.mailbox.process(meta[1], m1);
        vm.prank(relayer, relayer);
        d.mailbox.process(meta[0], m2);
        assertEq(d.admin.lastSeq(), 2);
        assertEq(d.admin.pending(), 2);
        IHlAdmin.Decision memory d1 = d.admin.decision(1);
        assertEq(d1.number, 1);
        assertEq(d1.kind, 7);
        assertEq(d1.deliverer, relayer);
        assertEq(d1.deliveredTime, block.timestamp);
        assertEq(d1.deliveredBlock, block.number);
        assertEq(d1.params, _fee(500, treasurer));
        // number 1 again: ignored, and delivered for good
        _stageBody(_body(7, 1, _fee(500, treasurer)));
        vm.expectEmit(true, false, false, false, _admin());
        emit IHlAdmin.DecisionIgnored(1);
        _land();
        assertTrue(d.mailbox.delivered(keccak256(stagedMsg)));
        assertEq(d.admin.lastSeq(), 2);
        assertEq(d.admin.pending(), 2);
        vm.expectRevert(HlAdmin.WrongDecision.selector);
        d.admin.execute(9);
    }

    /// Refused by `handle`, the message staying deliverable: another sender, a body under nine bytes, value, a caller
    /// that is not the mailbox, another origin.
    function test_recording_refusals() public {
        bytes memory body = _body(7, 1, _fee(1, treasurer));
        bytes memory wrongSender = _message(ROUTE0, _admin(), body);
        bytes memory short = _message(HLGOV, _admin(), hex"070000");
        bytes[] memory meta = _prove2(wrongSender, short);
        _processExpectRevert(meta[0], wrongSender, abi.encodeWithSelector(HlAdmin.WrongSender.selector));
        _processExpectRevert(meta[1], short, abi.encodeWithSelector(HlAdmin.ShortBody.selector));
        bytes memory good = _message(HLGOV, _admin(), body);
        bytes[] memory meta2 = _prove2(good, good);
        vm.deal(relayer, 1 ether);
        vm.prank(relayer, relayer);
        vm.expectRevert(HlAdmin.Value.selector);
        d.mailbox.process{value: 1}(meta2[0], good);
        vm.expectRevert(HlAdmin.NotTheMailbox.selector);
        d.admin.handle(GNO, HLGOV, body);
        vm.prank(address(d.mailbox));
        vm.expectRevert(HlAdmin.WrongOrigin.selector);
        d.admin.handle(GNO + 1, HLGOV, body);
        assertEq(d.admin.lastSeq(), 0);
        vm.prank(relayer, relayer);
        d.mailbox.process(meta2[0], good);
        assertEq(d.admin.lastSeq(), 1);
    }

    /// An unknown kind (0, 10, 11, 42) and a body over 512 bytes are recorded and spent: the sequence moves, nothing
    /// pends. A body of exactly 512 bytes is recorded (and its kind 7 parameters then fail at execution).
    function test_recording_unknownKindsAndLongBodies() public {
        uint8[4] memory unknown = [uint8(0), 10, 11, 42];
        for (uint256 i = 0; i < 4; i++) {
            uint64 seq = _stage(unknown[i], abi.encodePacked(uint64(500)));
            vm.expectEmit(true, false, false, true, _admin());
            emit IHlAdmin.DecisionSpent(seq, unknown[i], "unknown kind");
            _land();
            assertFalse(_pending(seq));
        }
        assertEq(d.admin.lastSeq(), 4);
        uint64 n = _stage(7, new bytes(504));
        vm.expectEmit(true, false, false, true, _admin());
        emit IHlAdmin.DecisionSpent(n, 7, "too long");
        _land();
        assertFalse(_pending(n));
        n = _decideKind(7, new bytes(503));
        assertTrue(_pending(n), "512 bytes: recorded");
        _wait(WEEK);
        _expectEnd(n, 7, "params");
        assertEq(d.admin.pending(), 0);
    }

    /// The sequence at its very end (doctored: nothing can take it there): a delivery is accepted and ignored.
    function test_recording_theSequencesEnd() public {
        bytes32 slot = HlAdminStorage.SLOT;
        uint256 word = uint256(vm.load(_admin(), slot));
        vm.store(_admin(), slot, bytes32((word >> 64 << 64) | type(uint64).max));
        assertEq(d.admin.lastSeq(), type(uint64).max);
        _stageBody(_body(7, type(uint64).max, _fee(1, treasurer)));
        vm.expectEmit(true, false, false, false, _admin());
        emit IHlAdmin.DecisionIgnored(type(uint64).max);
        _land();
        assertEq(d.admin.lastSeq(), type(uint64).max);
    }

    // --------------------------------------------------------------------------------------------- clocks --

    /// Kind 7: the 7-day group waits its week, a lone flag defers it, then the fee and beneficiary are set; a fee
    /// above the compiled maximum, a zero beneficiary, an address with high bytes are spent.
    function test_kind7_aWeekAFlagAndTheFee() public {
        address b = makeAddr("beneficiary");
        uint64 n = _decideKind(7, _fee(500, b));
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n);
        _wait(WEEK - 5);
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n);
        _wait(6);
        _aflag(watch1);
        vm.expectRevert(HlAdmin.Deferred.selector);
        d.admin.execute(n);
        _aclear(watch1);
        _expectEnd(n, 7, "");
        assertEq(d.protocolFee.protocolFee(), 500);
        assertEq(d.protocolFee.beneficiary(), b);
        // spent: above the maximum, no beneficiary, an address with high bytes
        uint64 big = _decideKind(7, _fee(uint64(MAX_PROTOCOL_FEE) + 1, b));
        uint64 zero = _decideKind(7, _fee(1, address(0)));
        uint64 high = _decideKind(7, abi.encodePacked(uint64(1), bytes32(uint256(1) << 160 | uint160(b))));
        uint64 exact = _decideKind(7, _fee(uint64(MAX_PROTOCOL_FEE), b));
        _wait(WEEK);
        _expectEnd(big, 7, "params");
        _expectEnd(zero, 7, "params");
        _expectEnd(high, 7, "params");
        assertEq(d.protocolFee.protocolFee(), 500);
        _expectEnd(exact, 7, "");
        assertEq(d.protocolFee.protocolFee(), MAX_PROTOCOL_FEE);
    }

    /// Kind 2: an unpause of the gate clears, 24 hours after its delivery, the flags set before its delivery block —
    /// never a later one — and is spent while a hold stands on the gate; an unpause of HlAdmin likewise.
    function test_kind2_unpauseIn24Hours() public {
        _gflag(watch1);
        uint64 n = _decideKind(2, _unpause(0));
        _wait(10);
        _gflag(seat1);
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n);
        _wait(DAY);
        _expectEnd(n, 2, "");
        IPausableGate.Flag[6] memory f = d.gate.flags();
        assertEq(f[4].blockNumber, 0, "the watch flag, set before the delivery, is cleared");
        assertTrue(f[0].blockNumber != 0, "the guardian's later flag stands");
        // a hold on the gate: the unpause is spent
        _gflag(seat2);
        n = _decideKind(2, _unpause(0));
        _wait(DAY + 1);
        _expectEnd(n, 2, "hold");
        assertTrue(d.gate.hold().blockNumber != 0, "the hold stands");
        // HlAdmin's own flags
        _aflag(watch2);
        n = _decideKind(2, _unpause(1));
        _wait(DAY);
        _expectEnd(n, 2, "");
        assertEq(d.admin.flags()[5].blockNumber, 0);
        // an object that is neither
        n = _decideKind(2, _unpause(2));
        _wait(DAY);
        _expectEnd(n, 2, "params");
    }

    /// Kind 5 lands through the gate's hold (both lists replaced, the replaced keys' flags cleared); one naming a
    /// current key is spent; under a hold on HlAdmin the 7-day group waits and kind 5 lands on the later of its own
    /// 7th day and the hold's 21st, three keys taking turns never moving the hold's start; the replacement's clear
    /// ends the hold at once.
    function test_kind5_throughAHoldOnItsFloor() public {
        _gflag(seat1);
        _gflag(seat2);
        assertTrue(d.gate.hold().blockNumber != 0);
        address n1 = makeAddr("n1");
        address n2 = makeAddr("n2");
        address n6 = makeAddr("n6");
        uint8[] memory seats = new uint8[](3);
        address[] memory keys = new address[](3);
        (seats[0], seats[1], seats[2]) = (1, 2, 6);
        (keys[0], keys[1], keys[2]) = (n1, n2, n6);
        uint64 n = _decideKind(5, _guardians(seats, keys));
        _wait(WEEK + 1);
        _expectEnd(n, 5, "");
        address[6] memory gk = d.gate.keys();
        address[6] memory ak = d.admin.keys();
        assertEq(gk[0], n1);
        assertEq(gk[1], n2);
        assertEq(gk[5], n6);
        for (uint256 i = 0; i < 6; i++) {
            assertEq(ak[i], gk[i], "the two lists never differ");
        }
        assertEq(d.gate.hold().blockNumber, 0, "the holders replaced: the gate's hold ended at once");
        // a kind 5 naming a current key (of any seat) is spent
        n = _decideKind(5, _guardian(3, seat4));
        _wait(WEEK + 1);
        _expectEnd(n, 5, "current key");

        // a hold on HlAdmin
        _aflag(seat3);
        _wait(100);
        _aflag(seat4);
        uint64 holdSince = d.admin.hold().time;
        assertEq(holdSince, block.timestamp);
        uint64 nf = _decideKind(7, _fee(7, treasurer));
        address n3 = makeAddr("n3");
        uint64 n5 = _decideKind(5, _guardian(3, n3));
        _wait(WEEK + 1);
        vm.expectRevert(HlAdmin.Deferred.selector);
        d.admin.execute(nf);
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n5);
        // three keys taking turns never move the start (seat 1 is n1 now)
        _aflag(n1);
        _aclear(seat3);
        _wait(3600);
        _aflag(seat3);
        _aclear(seat4);
        _wait(3600);
        _aflag(seat4);
        _aclear(n1);
        assertEq(d.admin.hold().time, holdSince);
        uint64 twentyFirst = holdSince + 21 * DAY;
        vm.warp(twentyFirst - 6 * DAY);
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n5);
        vm.warp(twentyFirst - 1);
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n5);
        vm.warp(twentyFirst);
        _expectEnd(n5, 5, "");
        assertEq(d.admin.keys()[2], n3);
        assertEq(d.admin.flags()[2].blockNumber, 0, "the replaced key's flag is cleared");
        assertEq(d.admin.hold().blockNumber, 0, "one flag left: the replacement ended the hold at once");
        // the fee decision is still deferred by the lone flag, and runs once it clears
        vm.expectRevert(HlAdmin.Deferred.selector);
        d.admin.execute(nf);
        _aclear(seat4);
        _expectEnd(nf, 7, "");
    }

    /// Kind 5 lands through a lone flag on HlAdmin on its own 7th day (a stolen key's flag never defers its own
    /// replacement), while the rest of the 7-day group waits on that flag.
    function test_kind5_landsThroughALoneFlag() public {
        address stolen = seat2;
        _aflag(stolen);
        uint64 nf = _decideKind(7, _fee(9, treasurer));
        address fresh = makeAddr("fresh");
        uint64 n5 = _decideKind(5, _guardian(2, fresh));
        _wait(WEEK - 1);
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n5);
        _wait(1);
        vm.expectRevert(HlAdmin.Deferred.selector);
        d.admin.execute(nf);
        _expectEnd(n5, 5, "");
        assertEq(d.admin.keys()[1], fresh);
        assertEq(d.admin.flags()[1].blockNumber, 0, "the stolen key's flag went with it");
        _expectEnd(nf, 7, "");
    }

    /// "A hold stands" is holdSince, dip or no dip: two flags then both cleared within the grace — no flag stands,
    /// yet the 7-day group is deferred and an unpause of either object is spent, until the dip reaches the grace.
    function test_aHoldStandsThroughItsDip() public {
        uint64 nf = _decideKind(7, _fee(8, treasurer));
        _aflag(seat1);
        _aflag(seat4);
        _gflag(seat1);
        _gflag(seat4);
        uint64 nu = _decideKind(2, _unpause(1));
        uint64 nu0 = _decideKind(2, _unpause(0));
        _wait(WEEK + 1);
        _aflag(seat1);
        _aclear(seat1);
        _aclear(seat4);
        _gclear(seat1);
        _gclear(seat4);
        _wait(3600);
        bool anyFlag;
        IPausableGate.Flag[6] memory f = d.admin.flags();
        for (uint256 i = 0; i < 6; i++) {
            anyFlag = anyFlag || f[i].blockNumber != 0;
        }
        assertFalse(anyFlag);
        assertTrue(d.admin.hold().blockNumber != 0, "the dip is inside the grace");
        vm.expectRevert(HlAdmin.Deferred.selector);
        d.admin.execute(nf);
        _expectEnd(nu, 2, "hold");
        _expectEnd(nu0, 2, "hold");
        _wait(DAY);
        assertEq(d.admin.hold().blockNumber, 0);
        _expectEnd(nf, 7, "");
        assertEq(d.protocolFee.protocolFee(), 8);
    }

    /// The replacement's floor comes from the hold history (AN4): a hold whose dip reached the grace is written
    /// with its end at that moment; a new hold begun at the grace's last second raises nothing — the floor is the
    /// earliest hold overlapping [the delivery, now]; the floor binds only while a hold stands.
    function test_kind5_floorFromTheHoldHistory() public {
        _aflag(seat3);
        _aflag(seat4);
        uint64 since = d.admin.hold().time;
        assertEq(d.admin.holds().length, 0);
        _wait(DAY);
        uint8[] memory seats = new uint8[](2);
        address[] memory keys = new address[](2);
        (seats[0], seats[1]) = (3, 4);
        (keys[0], keys[1]) = (makeAddr("k53"), makeAddr("k54"));
        uint64 n5 = _decideKind(5, _guardians(seats, keys));
        // day 19: both holders clear; at day 20 (the grace run) they re-flag: hold A ended at day 20, hold B begins
        vm.warp(since + 19 * DAY);
        _aclear(seat3);
        _aclear(seat4);
        _wait(DAY);
        assertEq(d.admin.hold().blockNumber, 0, "the dip reached the grace");
        _aflag(seat3);
        _aflag(seat4);
        uint64 sinceB = d.admin.hold().time;
        assertEq(sinceB, since + 20 * DAY);
        IHlAdmin.Hold[] memory h = d.admin.holds();
        assertEq(h.length, 1);
        assertEq(h[0].start, since);
        assertEq(h[0].end, since + 20 * DAY, "ended the moment its dip reached the grace");
        // under hold B the floor is hold A's 21st day, not hold B's
        vm.warp(since + 21 * DAY - 1);
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n5);
        vm.warp(since + 21 * DAY);
        assertEq(d.admin.hold().time, sinceB, "hold B stands");
        _expectEnd(n5, 5, "");
        uint64 endB = uint64(block.timestamp);
        h = d.admin.holds();
        assertEq(h.length, 2);
        assertEq(h[1].start, sinceB);
        assertEq(h[1].end, endB, "hold B ended by the replacement's own clear, now");
        assertEq(d.admin.hold().blockNumber, 0);
        // hold C over seats 1 and 2: a replacement of them is NotMature on day 2, the holders lift on day 3, the
        // hold ends on day 4, and the replacement lands on its own 7th day
        _aflag(seat1);
        _aflag(seat2);
        uint64 sinceC = d.admin.hold().time;
        _wait(DAY);
        (seats[0], seats[1]) = (1, 2);
        (keys[0], keys[1]) = (makeAddr("k55"), makeAddr("k56"));
        uint64 n5b = _decideKind(5, _guardians(seats, keys));
        uint64 delivered = d.admin.decision(n5b).deliveredTime;
        _wait(DAY);
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n5b);
        _wait(DAY);
        _aclear(seat1);
        _aclear(seat2);
        _wait(DAY);
        assertEq(d.admin.hold().blockNumber, 0, "lifted on day 3, ended on day 4");
        vm.warp(delivered + WEEK - 1);
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n5b);
        vm.warp(delivered + WEEK);
        _expectEnd(n5b, 5, "");
        h = d.admin.holds();
        assertEq(h.length, 3, "hold C's end learnt at the replacement, written as the moment its dip reached the grace");
        assertEq(h[2].start, sinceC);
        assertEq(h[2].end, sinceC + 4 * DAY);
    }

    /// The hold history keeps eight: a ninth merges the two oldest, the earliest start kept.
    function test_holdHistory_aNinthMergesTheTwoOldest() public {
        uint64 firstStart;
        uint64 secondEnd;
        for (uint256 i = 0; i < 9; i++) {
            _aflag(seat1);
            _aflag(seat2);
            if (i == 0) {
                firstStart = uint64(block.timestamp);
            }
            _aclear(seat1);
            _aclear(seat2);
            _wait(DAY + 1);
            // the next change learns the end; a set and a clear of a watch key is a change
            _aflag(watch1);
            _aclear(watch1);
            if (i == 1) {
                secondEnd = d.admin.holds()[1].end;
            }
            assertEq(d.admin.holds().length, i < 8 ? i + 1 : 8);
        }
        IHlAdmin.Hold[] memory h = d.admin.holds();
        assertEq(h[0].start, firstStart, "the earliest start kept");
        assertEq(h[0].end, secondEnd, "spanning the two oldest");
    }

    // ---------------------------------------------------------------------------------------------- kinds --

    /// Kind 3: a token's ISM to zero (the mailbox's default, the gate) and back to the gate; any other ISM, or no
    /// token of ours, is spent.
    function test_kind3_aTokensIsm() public {
        uint64 n = _decideKind(3, _tokenIsm(0, address(0)));
        _wait(WEEK);
        _expectEnd(n, 3, "");
        assertEq(address(d.token0.interchainSecurityModule()), address(0));
        // a delivery to token 0 still passes the gate: the mailbox's default ISM is the gate
        _gflag(watch1);
        bytes[] memory ms = new bytes[](1);
        ms[0] = _message(ROUTE0, address(d.token0), TestChain.tokenBody(treasurer, 1));
        bytes[] memory meta = _prove(ms);
        vm.prank(relayer, relayer);
        vm.expectRevert(abi.encodeWithSignature("Paused()"));
        d.mailbox.process(meta[0], ms[0]);
        _gclear(watch1);
        n = _decideKind(3, _tokenIsm(0, address(d.gate)));
        uint64 other = _decideKind(3, _tokenIsm(1, address(d.client)));
        uint64 none = _decideKind(3, _tokenIsm(2, address(d.gate)));
        _wait(WEEK);
        _expectEnd(n, 3, "");
        assertEq(address(d.token0.interchainSecurityModule()), address(d.gate));
        _expectEnd(other, 3, "params");
        assertEq(address(d.token1.interchainSecurityModule()), address(d.gate));
        _expectEnd(none, 3, "params");
    }

    /// Kind 4 is reserved: recorded as any decision of the 7-day group, spent at execution.
    function test_kind4_reserved() public {
        uint64 n = _decideKind(4, abi.encodePacked(bytes32(uint256(1)), uint8(2)));
        assertTrue(_pending(n));
        _wait(WEEK - 1);
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n);
        _wait(1);
        _aflag(seat1);
        vm.expectRevert(HlAdmin.Deferred.selector);
        d.admin.execute(n);
        _aclear(seat1);
        _expectEnd(n, 4, "reserved");
    }

    /// Kind 6: a router enrolled, then unenrolled by a zero router, then a zero router again (none enrolled: a
    /// no-op); no token of ours is spent.
    function test_kind6_routers() public {
        uint32 sol = 1399811149;
        bytes32 r = bytes32(uint256(0xab) * 0x0101010101010101010101010101010101010101010101010101010101010101);
        uint64 n = _decideKind(6, _router(1, sol, r));
        _wait(WEEK);
        _expectEnd(n, 6, "");
        assertEq(d.token1.routers(sol), r);
        n = _decideKind(6, _router(1, sol, bytes32(0)));
        uint64 again = _decideKind(6, _router(1, sol, bytes32(0)));
        uint64 none = _decideKind(6, _router(5, sol, r));
        _wait(WEEK);
        _expectEnd(n, 6, "");
        assertEq(d.token1.routers(sol), bytes32(0));
        _expectEnd(again, 6, "");
        _expectEnd(none, 6, "params");
        assertEq(d.token1.routers(GNO), ROUTE1, "gno.land's route untouched");
    }

    /// Kind 12: a token's mint limits on the gate (the bucket's level standing); a token that is none of ours is
    /// spent.
    function test_kind12_mintLimits() public {
        uint64 n = _decideKind(12, _limits(1, 7_000_000_000, 9_000_000_000));
        vm.expectRevert(HlAdmin.NotMature.selector);
        d.admin.execute(n);
        _wait(WEEK);
        _expectEnd(n, 12, "");
        assertEq(d.gate.limits(1).maxPerMessage, 7_000_000_000);
        assertEq(d.gate.limits(1).dayCap, 9_000_000_000);
        assertEq(d.gate.limits(0).maxPerMessage, MAX_PER_MESSAGE, "the other token's untouched");
        n = _decideKind(12, _limits(2, 1, 1));
        _wait(WEEK);
        _expectEnd(n, 12, "params");
    }

    /// Parameters of the wrong length, of every kind that has parameters: spent.
    function test_kinds_parametersThatDoNotParseAreSpent() public {
        uint8[8] memory kinds = [uint8(2), 3, 5, 6, 7, 8, 9, 12];
        uint64[8] memory ns;
        for (uint256 i = 0; i < 8; i++) {
            bytes memory bad = hex"00000000000000000000000000000000000000000000000000000000000000000000000000000000ff";
            ns[i] = _decideKind(kinds[i], kinds[i] == 8 ? new bytes(95) : bad);
        }
        _wait(WEEK);
        for (uint256 i = 0; i < 8; i++) {
            _expectEnd(ns[i], kinds[i], "params");
        }
    }

    // -------------------------------------------------------------------------------------------- cancels --

    /// A list cancel deletes the named pending decisions as it is recorded, stores nothing, and lands through any
    /// hold; a cancel naming another channel does not parse and is spent; a number above the last is no one.
    function test_cancel_aListDeletesNow() public {
        uint64 n = _decideKind(7, _fee(5, treasurer));
        uint64 m = _decideKind(7, _fee(6, treasurer));
        _aflag(seat1);
        _aflag(seat2);
        assertTrue(d.admin.hold().blockNumber != 0);
        uint64 c = _stage(1, _cancelOne(n));
        vm.expectEmit(true, false, false, false, _admin());
        emit IHlAdmin.DecisionCancelled(n);
        _land();
        assertFalse(_pending(n));
        assertFalse(_pending(c), "a cancel never pends");
        assertEq(d.admin.pending(), 1);
        assertEq(d.admin.cancelledRanges().length, 0, "deleted now: nothing stored");
        // channel 1: spent, nothing cancelled
        uint64 spent = _stage(1, abi.encodePacked(salt++, uint8(0), uint8(1), uint8(1), m));
        vm.expectEmit(true, false, false, true, _admin());
        emit IHlAdmin.DecisionSpent(spent, 1, "params");
        _land();
        assertTrue(_pending(m));
        // a number above the last cancels nothing (it will be recorded later)
        _decideKind(1, _cancelOne(nextSeq + 5));
        assertEq(d.admin.cancelledRanges().length, 0);
        assertTrue(_pending(m));
    }

    /// A range is stored (clamped at the last number) while anything pends; execute closes a decision inside it as
    /// cancelled; when nothing pends the ranges are pruned; with nothing pending a range is not stored at all.
    function test_cancel_aRangeIsStoredClampedAndPruned() public {
        uint64 n4 = _decideKind(7, _fee(5, treasurer));
        uint64 n5 = _decideKind(7, _fee(5, treasurer));
        _stage(1, _cancelRange(n4, n5 + 1000));
        vm.expectEmit(false, false, false, true, _admin());
        emit IHlAdmin.RangeCancelled(n4, n5 + 1);
        _land();
        uint64[2][] memory r = d.admin.cancelledRanges();
        assertEq(r.length, 1);
        assertEq(r[0][0], n4);
        assertEq(r[0][1], n5 + 1, "clamped at the last number (the cancel's own)");
        assertTrue(d.admin.isCancelled(n4) && d.admin.isCancelled(n5));
        assertFalse(d.admin.isCancelled(n5 + 2));
        vm.expectEmit(true, false, false, false, _admin());
        emit IHlAdmin.DecisionCancelled(n4);
        d.admin.execute(n4);
        assertEq(d.admin.pending(), 1);
        assertEq(d.admin.cancelledRanges().length, 1, "one still pends: the range stays");
        d.admin.execute(n5);
        assertEq(d.admin.pending(), 0);
        assertEq(d.admin.cancelledRanges().length, 0, "nothing pends: pruned");
        assertEq(d.protocolFee.protocolFee(), 0, "cancelled, never executed");
        // nothing pending: a range is not stored
        _decideKind(1, _cancelRange(1, 3));
        assertEq(d.admin.cancelledRanges().length, 0);
    }

    /// Ranges that overlap or touch merge; a full store (64 ranges) merges a new range with the nearest stored one
    /// rather than refuse (AN7).
    function test_cancel_rangesMergeAndAFullStoreMergesWithTheNearest() public {
        uint64[] memory ns = new uint64[](8);
        for (uint256 i = 0; i < 8; i++) {
            ns[i] = _decideKind(7, _fee(5, treasurer));
        }
        _decideKind(1, _cancelRange(ns[0], ns[1]));
        _decideKind(1, _cancelRange(ns[3], ns[4]));
        assertEq(d.admin.cancelledRanges().length, 2);
        _decideKind(1, _cancelRange(ns[2], ns[2] + 0)); // a range of one: deleted now, nothing stored
        assertFalse(_pending(ns[2]));
        _decideKind(1, _cancelRange(ns[1], ns[3]));
        uint64[2][] memory r = d.admin.cancelledRanges();
        assertEq(r.length, 1, "merged");
        assertEq(r[0][0], ns[0]);
        assertEq(r[0][1], ns[4]);
        // a full store, doctored: 64 disjoint ranges far above
        _storeRanges(64, 1000);
        r = d.admin.cancelledRanges();
        assertEq(r.length, 64);
        assertEq(r[63][0], 1126);
        _decideKind(1, _cancelRange(ns[6], ns[7]));
        r = d.admin.cancelledRanges();
        assertEq(r.length, 64);
        assertTrue(d.admin.isCancelled(ns[6]) && d.admin.isCancelled(ns[7]));
        assertEq(r[63][0], ns[6], "merged with the nearest: [n6, 1000]");
        assertEq(r[63][1], 1000);
        assertTrue(_pending(ns[6]), "dead, closed by execute");
        d.admin.execute(ns[6]);
        assertFalse(_pending(ns[6]));
    }

    /// Writes `n` ranges [from + 2i, from + 2i] into HlAdmin's cancelled store (the ERC-7201 layout: slot + 14).
    function _storeRanges(uint256 n, uint64 from) internal {
        bytes32 base = bytes32(uint256(HlAdminStorage.SLOT) + 14);
        vm.store(_admin(), base, bytes32(n));
        bytes32 data = keccak256(abi.encode(base));
        for (uint256 i = 0; i < n; i++) {
            uint64 x = from + uint64(2 * i);
            vm.store(_admin(), bytes32(uint256(data) + i), bytes32(uint256(x) << 64 | uint256(x)));
        }
    }

    // ------------------------------------------------------------------------------------------------ flags --

    function test_flags_onlyTheSeatsAndSlots() public {
        vm.prank(makeAddr("stranger"));
        vm.expectRevert(Guardians.UnknownKey.selector);
        d.admin.setFlag();
        vm.expectEmit(true, false, false, true, _admin());
        emit IHlAdmin.FlagSet(watch2, 6);
        _aflag(watch2);
        assertEq(d.admin.flags()[5].blockNumber, block.number);
        _aclear(watch2);
        assertEq(d.admin.flags()[5].blockNumber, 0);
    }
}

/// A fresh deployment under the same r/hlgov starts its sequence at r/hlgov's number.
contract HlAdminFreshDeploymentTest is AdminBase {
    function setUp() public {
        startSequence = 41;
        _world();
        nextSeq = 42;
    }

    function test_theSequenceStartsAtRHlgovsNumber() public {
        assertEq(d.admin.lastSeq(), 41);
        _stageBody(_body(7, 41, _fee(1, treasurer)));
        vm.expectEmit(true, false, false, false, address(d.admin));
        emit IHlAdmin.DecisionIgnored(41);
        _land();
        uint64 n = _decideKind(7, _fee(1, treasurer));
        assertEq(n, 42);
        assertTrue(_pending(42));
        assertEq(d.admin.lastSeq(), 42);
    }
}

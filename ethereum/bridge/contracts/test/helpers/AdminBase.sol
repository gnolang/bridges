// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {IHlAdmin} from "../../src/interfaces/IHlAdmin.sol";
import {BridgeFixture} from "./BridgeFixture.sol";

/// HlAdmin's decisions as r/hlgov writes them (§13: `kind u8 ‖ seq u64 BE ‖ params`, every integer big-endian, every
/// address 32 bytes left-padded), delivered through the mailbox on the Solidity test chain.
abstract contract AdminBase is BridgeFixture {
    uint64 internal nextSeq = 1;
    uint64 internal salt = 0x5a170000;

    uint64 internal constant WEEK = uint64(7 days) / CLOCK_FACTOR;
    uint64 internal constant DAY = uint64(1 days) / CLOCK_FACTOR;

    function _body(uint8 kind, uint64 seq, bytes memory params) internal pure returns (bytes memory) {
        return abi.encodePacked(kind, seq, params);
    }

    /// Delivers a decision of `kind` at the next number; returns the number.
    function _decideKind(uint8 kind, bytes memory params) internal returns (uint64 seq) {
        seq = nextSeq++;
        _decide(_body(kind, seq, params));
    }

    bytes internal stagedMeta;
    bytes internal stagedMsg;

    /// Proves a decision body at `seq` (the header update happens here) without delivering it: `_land()` then calls
    /// the mailbox alone, so a test can expect the events of that one call.
    function _stageBody(bytes memory body) internal {
        bytes[] memory ms = new bytes[](1);
        ms[0] = _message(HLGOV, address(d.admin), body);
        bytes[] memory meta = _prove(ms);
        (stagedMeta, stagedMsg) = (meta[0], ms[0]);
    }

    function _stage(uint8 kind, bytes memory params) internal returns (uint64 seq) {
        seq = nextSeq++;
        _stageBody(_body(kind, seq, params));
    }

    function _land() internal {
        vm.prank(relayer, relayer);
        d.mailbox.process(stagedMeta, stagedMsg);
    }

    function _a32(address a) internal pure returns (bytes32) {
        return bytes32(uint256(uint160(a)));
    }

    // ---- the kinds' parameters ----

    function _cancelList(uint64[] memory numbers) internal returns (bytes memory p) {
        p = abi.encodePacked(salt++, uint8(0), uint8(numbers.length));
        for (uint256 i = 0; i < numbers.length; i++) {
            p = abi.encodePacked(p, uint8(0), numbers[i]);
        }
    }

    function _cancelOne(uint64 n) internal returns (bytes memory) {
        uint64[] memory l = new uint64[](1);
        l[0] = n;
        return _cancelList(l);
    }

    function _cancelRange(uint64 from, uint64 to) internal returns (bytes memory) {
        return abi.encodePacked(salt++, uint8(1), uint8(0), from, to);
    }

    function _unpause(uint8 object) internal pure returns (bytes memory) {
        return abi.encodePacked(object);
    }

    function _tokenIsm(uint8 token, address ism) internal pure returns (bytes memory) {
        return abi.encodePacked(token, _a32(ism));
    }

    function _guardians(uint8[] memory seats, address[] memory keys) internal pure returns (bytes memory p) {
        p = abi.encodePacked(uint8(seats.length));
        for (uint256 i = 0; i < seats.length; i++) {
            p = abi.encodePacked(p, seats[i], _a32(keys[i]));
        }
    }

    function _guardian(uint8 seat, address key) internal pure returns (bytes memory) {
        return abi.encodePacked(uint8(1), seat, _a32(key));
    }

    function _router(uint8 token, uint32 domain, bytes32 router) internal pure returns (bytes memory) {
        return abi.encodePacked(token, domain, router);
    }

    function _fee(uint64 fee, address beneficiary) internal pure returns (bytes memory) {
        return abi.encodePacked(fee, _a32(beneficiary));
    }

    function _upgrade(address proxy, address impl, bytes32 codehash, bytes memory call)
        internal
        pure
        returns (bytes memory)
    {
        return abi.encodePacked(_a32(proxy), _a32(impl), codehash, call);
    }

    function _transfer(address succ, bytes32 codehash) internal pure returns (bytes memory) {
        return abi.encodePacked(_a32(succ), codehash);
    }

    function _limits(uint8 token, uint64 max, uint64 cap) internal pure returns (bytes memory) {
        return abi.encodePacked(token, max, cap);
    }

    // ---- flags ----

    function _aflag(address key) internal {
        vm.prank(key);
        d.admin.setFlag();
    }

    function _aclear(address key) internal {
        vm.prank(key);
        d.admin.clearFlag();
    }

    function _gflag(address key) internal {
        vm.prank(key);
        d.gate.setFlag();
    }

    function _gclear(address key) internal {
        vm.prank(key);
        d.gate.clearFlag();
    }

    function _pending(uint64 n) internal view returns (bool) {
        return d.admin.decision(n).deliveredBlock != 0;
    }

    function _wait(uint256 secs) internal {
        vm.warp(block.timestamp + secs);
        vm.roll(block.number + 1);
    }

    /// Expects `execute(n)` to end the decision with `why` (spent) or as executed (`why` empty).
    function _expectEnd(uint64 n, uint8 kind, string memory why) internal {
        vm.expectEmit(true, false, false, true, address(d.admin));
        if (bytes(why).length == 0) {
            emit IHlAdmin.DecisionExecuted(n, kind);
        } else {
            emit IHlAdmin.DecisionSpent(n, kind, why);
        }
        d.admin.execute(n);
        assertFalse(_pending(n), "the decision ended");
    }
}

// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Test} from "forge-std/Test.sol";

import {HlAdmin} from "../src/HlAdmin.sol";
import {AdminBase} from "./helpers/AdminBase.sol";

/// Drives HlAdmin as its mailbox, a stranger and the keys would: decisions near the next number (in order, out of
/// order, below), of every kind with valid or junk parameters, cancels by list and range, executions of any
/// number, flags, time.
contract AdminHandler is Test {
    HlAdmin internal admin;
    address internal mailbox;
    bytes32 internal hlgov;
    uint32 internal origin;
    address[6] internal keys;
    address internal treasurer;
    uint64 public maxSeen;
    bool public decreased;

    constructor(HlAdmin a, address m, bytes32 h, uint32 o, address[6] memory k, address t) {
        (admin, mailbox, hlgov, origin, keys, treasurer) = (a, m, h, o, k, t);
    }

    function _after() internal {
        uint64 last = admin.lastSeq();
        if (last < maxSeen) {
            decreased = true;
        }
        maxSeen = last;
    }

    function decide(uint8 kindSeed, uint8 offset, bytes32 junk) external {
        uint64 last = admin.lastSeq();
        uint64 seq = last + 1;
        uint8 o = offset % 8;
        if (o == 6) {
            seq = last; // at the last: ignored
        } else if (o == 7) {
            seq = last + 2; // a gap: refused
        }
        uint8[9] memory kinds = [uint8(1), 2, 3, 4, 5, 6, 7, 12, 42];
        uint8 kind = kinds[kindSeed % 9];
        bytes memory params;
        if (kind == 1) {
            params = uint8(junk[0]) % 2 == 0
                ? abi.encodePacked(uint64(1), uint8(0), uint8(1), uint8(0), uint64(uint8(junk[1]) % (last + 2)))
                : abi.encodePacked(
                    uint64(1),
                    uint8(1),
                    uint8(0),
                    uint64(uint8(junk[1]) % (last + 1)),
                    uint64(last + uint8(junk[2]) % 4)
                );
        } else if (kind == 2) {
            params = abi.encodePacked(uint8(junk[0]) % 3);
        } else if (kind == 7) {
            params = abi.encodePacked(uint64(uint8(junk[0])), bytes32(uint256(uint160(treasurer))));
        } else if (kind == 12) {
            params = abi.encodePacked(uint8(junk[0]) % 3, uint64(1e12), uint64(4e12));
        } else {
            params = abi.encodePacked(junk);
        }
        vm.prank(mailbox, mailbox);
        try admin.handle(origin, hlgov, abi.encodePacked(kind, seq, params)) {} catch {}
        _after();
    }

    function execute(uint64 n) external {
        uint64 last = admin.lastSeq();
        n = last == 0 ? 0 : n % (last + 1);
        try admin.execute(n) {} catch {}
        _after();
    }

    function flag(uint8 who, bool set) external {
        vm.prank(keys[who % 6]);
        if (set) {
            admin.setFlag();
        } else {
            admin.clearFlag();
        }
        _after();
    }

    function wait(uint32 secs) external {
        vm.warp(block.timestamp + secs % (8 days));
        vm.roll(block.number + 1);
        _after();
    }
}

/// HlAdmin's sequence invariants under the handler: the last number only rises; the pending count equals the
/// decisions stored at numbers up to it; nothing is stored above it.
contract HlAdminInvariantTest is AdminBase {
    AdminHandler internal handler;

    function setUp() public {
        _world();
        handler = new AdminHandler(
            d.admin, address(d.mailbox), HLGOV, GNO, [seat1, seat2, seat3, seat4, watch1, watch2], treasurer
        );
        targetContract(address(handler));
    }

    function invariant_theLastNumberOnlyRises() public view {
        assertFalse(handler.decreased());
        assertEq(d.admin.lastSeq(), handler.maxSeen());
    }

    function invariant_pendingIsTheStoredDecisions() public view {
        uint64 last = d.admin.lastSeq();
        uint32 stored;
        for (uint64 n = 0; n <= last + 2; n++) {
            if (d.admin.decision(n).deliveredBlock != 0) {
                assertLe(n, last, "nothing above the last number");
                assertGt(n, 0);
                stored++;
            }
        }
        assertEq(d.admin.pending(), stored);
        if (stored == 0) {
            assertEq(d.admin.cancelledRanges().length, 0, "nothing pends: no range is kept");
        }
    }
}

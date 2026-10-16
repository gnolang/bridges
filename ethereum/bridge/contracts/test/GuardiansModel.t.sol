// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Test} from "forge-std/Test.sol";

import {IPausableGate} from "../src/interfaces/IPausableGate.sol";
import {Guardians} from "../src/lib/Guardians.sol";

/// The library under test, holding one object's guardians.
contract GuardiansHarness {
    using Guardians for Guardians.State;

    Guardians.State internal g;

    function init(address[6] memory keys) external {
        g.init(keys);
    }

    function setFlag(address key, uint64 b, uint64 t, uint64 grace) external {
        g.setFlag(key, b, t, grace);
    }

    function clearFlag(address key, uint64 b, uint64 t, uint64 grace) external {
        g.clearFlag(key, b, t, grace);
    }

    function unpause(uint64 before, uint64 b, uint64 t, uint64 grace) external returns (uint256) {
        return g.unpause(before, b, t, grace);
    }

    function replace(IPausableGate.SeatKey[] memory pairs, uint64 b, uint64 t, uint64 grace) external {
        g.replace(pairs, b, t, grace);
    }

    function keys() external view returns (address[6] memory) {
        return g.keys;
    }

    function flags() external view returns (IPausableGate.Flag[6] memory) {
        return g.flags();
    }

    function moments() external view returns (uint64, uint64, uint64, uint64) {
        return g.moments();
    }

    function hold(uint64 t, uint64 grace) external view returns (bool, uint64, uint64) {
        return g.hold(t, grace);
    }
}

/// The reference model: sol-util/src/guardians.rs transcribed with its Options as they are (a `some` bit beside
/// each moment), errors returned rather than thrown, written apart from the library so that the two disagree
/// wherever one of them is wrong.
contract GuardiansReference {
    struct Opt {
        bool some;
        uint64 slot;
        uint64 time;
    }

    address[6] public keys;
    Opt[6] internal flagsOf;
    Opt internal holdSince;
    Opt internal dipSince;

    function init(address[6] memory k) external {
        keys = k;
    }

    function flag(uint256 i) external view returns (Opt memory) {
        return flagsOf[i];
    }

    function moments() external view returns (Opt memory, Opt memory) {
        return (holdSince, dipSince);
    }

    function _position(address key) internal view returns (bool, uint256) {
        for (uint256 i = 0; i < 6; i++) {
            if (keys[i] == key) {
                return (true, i);
            }
        }
        return (false, 0);
    }

    function _guardianFlags() internal view returns (uint256 n) {
        for (uint256 i = 0; i < 4; i++) {
            if (flagsOf[i].some) {
                n++;
            }
        }
    }

    /// `hold(now, grace)`: Some(since) while the flags are at two, or a dip shorter than the grace runs.
    function hold(uint64 t, uint64 grace) public view returns (Opt memory) {
        Opt memory none;
        if (!holdSince.some) {
            return none;
        }
        if (_guardianFlags() >= 2) {
            return holdSince;
        }
        if (dipSince.some && int256(uint256(t)) - int256(uint256(dipSince.time)) < int256(uint256(grace))) {
            return holdSince;
        }
        return none;
    }

    function _settle(uint64 t, uint64 grace) internal {
        if (holdSince.some && dipSince.some) {
            if (int256(uint256(t)) - int256(uint256(dipSince.time)) >= int256(uint256(grace))) {
                delete holdSince;
                delete dipSince;
            }
        }
    }

    function _recompute(uint64 b, uint64 t) internal {
        if (_guardianFlags() >= 2) {
            if (!holdSince.some) {
                holdSince = Opt(true, b, t);
            }
            delete dipSince;
        } else if (holdSince.some && !dipSince.some) {
            dipSince = Opt(true, b, t);
        }
    }

    function setFlag(address key, uint64 b, uint64 t, uint64 grace) external returns (bytes4) {
        (bool ok, uint256 i) = _position(key);
        if (!ok) {
            return Guardians.UnknownKey.selector;
        }
        _settle(t, grace);
        flagsOf[i] = Opt(true, b, t);
        _recompute(b, t);
        return 0;
    }

    function clearFlag(address key, uint64 b, uint64 t, uint64 grace) external returns (bytes4) {
        (bool ok, uint256 i) = _position(key);
        if (!ok) {
            return Guardians.UnknownKey.selector;
        }
        _settle(t, grace);
        delete flagsOf[i];
        _recompute(b, t);
        return 0;
    }

    function unpause(uint64 before, uint64 b, uint64 t, uint64 grace) external returns (bytes4, uint256 n) {
        _settle(t, grace);
        if (hold(t, grace).some) {
            return (Guardians.Hold.selector, 0);
        }
        for (uint256 i = 0; i < 6; i++) {
            if (flagsOf[i].some && flagsOf[i].slot < before) {
                delete flagsOf[i];
                n++;
            }
        }
        _recompute(b, t);
        return (0, n);
    }

    function _validate(uint8[] memory seats, address[] memory ks)
        internal
        view
        returns (bytes4, address[6] memory next, bool[6] memory replaced)
    {
        next = keys;
        if (seats.length == 0 || seats.length > 4) {
            return (Guardians.BadSeat.selector, next, replaced);
        }
        uint256 guardians;
        uint256 watch;
        for (uint256 p = 0; p < seats.length; p++) {
            if (seats[p] < 1 || seats[p] > 6) {
                return (Guardians.BadSeat.selector, next, replaced);
            }
            uint256 i = seats[p] - 1;
            if (replaced[i]) {
                return (Guardians.BadSeat.selector, next, replaced);
            }
            if (ks[p] == address(0)) {
                return (Guardians.UnknownKey.selector, next, replaced);
            }
            if (keys[i] == ks[p]) {
                return (Guardians.CurrentKey.selector, next, replaced);
            }
            if (i < 4) {
                guardians++;
            } else {
                watch++;
            }
            next[i] = ks[p];
            replaced[i] = true;
        }
        if (guardians > 2) {
            return (Guardians.TooManyGuardians.selector, next, replaced);
        }
        if (watch > 2) {
            return (Guardians.TooManyWatch.selector, next, replaced);
        }
        for (uint256 i = 0; i < 6; i++) {
            for (uint256 j = 0; j < i; j++) {
                if (next[i] == next[j]) {
                    return (Guardians.DuplicateKey.selector, next, replaced);
                }
            }
        }
        return (bytes4(0), next, replaced);
    }

    function replace(uint8[] memory seats, address[] memory ks, uint64 b, uint64 t, uint64 grace)
        external
        returns (bytes4)
    {
        (bytes4 err, address[6] memory next, bool[6] memory replaced) = _validate(seats, ks);
        if (err != bytes4(0)) {
            return err;
        }
        _settle(t, grace);
        bool clearedGuardian;
        for (uint256 i = 0; i < 6; i++) {
            if (replaced[i]) {
                if (i < 4 && flagsOf[i].some) {
                    clearedGuardian = true;
                }
                delete flagsOf[i];
            }
        }
        keys = next;
        if (clearedGuardian && _guardianFlags() < 2) {
            delete holdSince;
            delete dipSince;
        } else {
            _recompute(b, t);
        }
        return 0;
    }
}

/// The guardians library against the reference model on fuzzed sequences: flags set and cleared by the seats,
/// the slots and strangers, unpauses before random blocks, replacements of random seats with random keys (current
/// ones, zero, new ones), time advancing by up to two days a step — the same refusals and the same state after every
/// step, and the same hold read at every moment.
contract GuardiansModelTest is Test {
    GuardiansHarness internal lib;
    GuardiansReference internal ref;
    address[12] internal pool;
    uint64 internal constant GRACE = 1 days;

    function setUp() public {
        lib = new GuardiansHarness();
        ref = new GuardiansReference();
        for (uint256 i = 0; i < 12; i++) {
            pool[i] = address(uint160(0x1000 + i));
        }
        address[6] memory k = [pool[0], pool[1], pool[2], pool[3], pool[4], pool[5]];
        lib.init(k);
        ref.init(k);
    }

    function _same(uint64 t) internal view {
        _sameFlags();
        _sameMoments();
        _sameHold(t);
        _sameHold(t + GRACE / 2);
        _sameHold(t + GRACE);
    }

    function _sameFlags() internal view {
        address[6] memory lk = lib.keys();
        IPausableGate.Flag[6] memory lf = lib.flags();
        for (uint256 i = 0; i < 6; i++) {
            assertEq(lk[i], ref.keys(i), "keys");
            GuardiansReference.Opt memory rf = ref.flag(i);
            assertEq(lf[i].blockNumber != 0, rf.some, "flag present");
            if (rf.some) {
                assertEq(lf[i].blockNumber, rf.slot);
                assertEq(lf[i].time, rf.time);
            }
        }
    }

    function _sameMoments() internal view {
        (uint64 hb, uint64 ht, uint64 db, uint64 dt) = lib.moments();
        (GuardiansReference.Opt memory h, GuardiansReference.Opt memory dip) = ref.moments();
        assertEq(hb != 0, h.some, "holdSince");
        assertEq(db != 0, dip.some, "dipSince");
        if (h.some) {
            assertEq(hb, h.slot);
            assertEq(ht, h.time);
        }
        if (dip.some) {
            assertEq(db, dip.slot);
            assertEq(dt, dip.time);
        }
    }

    function _sameHold(uint64 t) internal view {
        (bool stands, uint64 sb, uint64 st) = lib.hold(t, GRACE);
        GuardiansReference.Opt memory rh = ref.hold(t, GRACE);
        assertEq(stands, rh.some, "hold");
        if (stands) {
            assertEq(sb, rh.slot);
            assertEq(st, rh.time);
        }
    }

    function _err(bytes memory ret) internal pure returns (bytes4 e) {
        if (ret.length >= 4) {
            e = bytes4(ret);
        }
    }

    function _flagStep(uint256 op, address key, uint64 b, uint64 t) internal returns (bytes4 want, bytes4 got) {
        if (op < 4) {
            want = ref.setFlag(key, b, t, GRACE);
            try lib.setFlag(key, b, t, GRACE) {}
            catch (bytes memory e) {
                got = _err(e);
            }
        } else {
            want = ref.clearFlag(key, b, t, GRACE);
            try lib.clearFlag(key, b, t, GRACE) {}
            catch (bytes memory e) {
                got = _err(e);
            }
        }
    }

    function _unpauseStep(uint256 r, uint64 b, uint64 t) internal returns (bytes4 want, bytes4 got) {
        uint64 back = uint64((r >> 56) % 12);
        uint64 before = b > back ? b - back : 0;
        uint256 n;
        (want, n) = ref.unpause(before, b, t, GRACE);
        try lib.unpause(before, b, t, GRACE) returns (uint256 m) {
            assertEq(m, n, "cleared");
        } catch (bytes memory e) {
            got = _err(e);
        }
    }

    function _replaceStep(uint256 r, uint64 b, uint64 t) internal returns (bytes4 want, bytes4 got) {
        uint256 count = (r >> 64) % 6; // 0–5 pairs: 0 and 5 are refused
        uint8[] memory seats = new uint8[](count);
        address[] memory ks = new address[](count);
        IPausableGate.SeatKey[] memory pairs = new IPausableGate.SeatKey[](count);
        for (uint256 p = 0; p < count; p++) {
            uint256 rr = uint256(keccak256(abi.encode(r, p)));
            seats[p] = uint8(rr % 8); // 0 and 7 are no seat
            uint256 which = (rr >> 8) % 13;
            ks[p] = which == 12 ? address(0) : pool[which];
            pairs[p] = IPausableGate.SeatKey(seats[p], ks[p]);
        }
        want = ref.replace(seats, ks, b, t, GRACE);
        try lib.replace(pairs, b, t, GRACE) {}
        catch (bytes memory e) {
            got = _err(e);
        }
    }

    function testFuzz_theLibraryIsTheModel(uint256 seed) public {
        uint64 b = 1;
        uint64 t = 1_791_000_000;
        for (uint256 step = 0; step < 48; step++) {
            uint256 r = uint256(keccak256(abi.encode(seed, step)));
            b += uint64(1 + r % 7);
            // time: mostly small steps, sometimes past the grace
            t += uint64((r >> 8) % 4 == 0 ? (r >> 16) % (2 days) : (r >> 16) % (3 hours));
            uint256 op = (r >> 40) % 10;
            bytes4 want;
            bytes4 got;
            if (op < 7) {
                (want, got) = _flagStep(op, pool[(r >> 48) % 8], b, t);
            } else if (op < 8) {
                (want, got) = _unpauseStep(r, b, t);
            } else {
                (want, got) = _replaceStep(r, b, t);
            }
            assertEq(got, want, "the same refusal");
            _same(t);
        }
    }
}

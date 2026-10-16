// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {IPausableGate} from "../interfaces/IPausableGate.sol";

/// The guardians' seats, the watch slots and their flags, as the gate and HlAdmin both keep them (eth/bridge/PLAN.md
/// §6.5, §6.7; ported from bridge/programs/sol-util/src/guardians.rs, one code for both objects so their rules
/// never differ): four numbered seats (3 and 4 the outsiders'), two watch slots, no key in two places; a flag per key
/// carrying the block it was last set at (fresh on every set), which only an unpause reads; and the object's hold:
/// `holdSince`, the moment its standing guardian flags reached two, kept while they stay at two or more and through
/// a dip below two shorter than the grace (24 hours at the build's clock factor), ended by a dip of the grace's
/// length, after which the next two flags begin a new one. An unpause clears the flags set before a block and is
/// refused under a hold; a replacement (at most two guardians plus either watch slot per decision, never a seat's
/// current key) clears the replaced keys' flags and lands through any hold, ending it at once when its own clear of a
/// standing guardian flag leaves fewer than two.
///
/// A moment is (block number, block time); block number zero stands for "none" (Ethereum's blocks start at one).
library Guardians {
    error UnknownKey();
    error DuplicateKey();
    error BadSeat();
    error TooManyGuardians();
    error TooManyWatch();
    error CurrentKey();
    error Hold();

    uint256 internal constant SEATS = 4;
    uint256 internal constant WATCH = 2;
    uint256 internal constant KEYS = 6;

    struct State {
        /// Seats 1–4 then watch slots 5–6, by index 0–5.
        address[6] keys;
        /// Each key's flag: the block and time it was last set (block zero: no flag).
        uint64[6] flagBlock;
        uint64[6] flagTime;
        /// The object's hold: when its standing guardian flags reached two (block zero: none stands).
        uint64 holdBlock;
        uint64 holdTime;
        /// While a hold stands, when its guardian flags last dipped below two (block zero: at two or more).
        uint64 dipBlock;
        uint64 dipTime;
    }

    /// The launch lists (or a successor's inherited ones): every key non-zero, no key twice.
    function init(State storage g, address[6] memory keys) internal {
        for (uint256 i = 0; i < KEYS; i++) {
            if (keys[i] == address(0)) {
                revert UnknownKey();
            }
            for (uint256 j = 0; j < i; j++) {
                if (keys[i] == keys[j]) {
                    revert DuplicateKey();
                }
            }
        }
        g.keys = keys;
        for (uint256 i = 0; i < KEYS; i++) {
            g.flagBlock[i] = 0;
            g.flagTime[i] = 0;
        }
        _endHold(g);
    }

    /// The index of a key, or KEYS when it holds no seat or slot.
    function position(State storage g, address key) internal view returns (uint256) {
        for (uint256 i = 0; i < KEYS; i++) {
            if (g.keys[i] == key) {
                return i;
            }
        }
        return KEYS;
    }

    function isGuardian(uint256 i) internal pure returns (bool) {
        return i < SEATS;
    }

    function anyFlag(State storage g) internal view returns (bool) {
        for (uint256 i = 0; i < KEYS; i++) {
            if (g.flagBlock[i] != 0) {
                return true;
            }
        }
        return false;
    }

    function guardianFlags(State storage g) internal view returns (uint256 n) {
        for (uint256 i = 0; i < SEATS; i++) {
            if (g.flagBlock[i] != 0) {
                n++;
            }
        }
    }

    /// The hold standing at `nowTime`, if any, and its start: a pure function of the stored moments and the clock,
    /// so a dip that has run its course reads as the hold's end before anything is written.
    function hold(State storage g, uint64 nowTime, uint64 grace)
        internal
        view
        returns (bool stands, uint64 startBlock, uint64 startTime)
    {
        if (g.holdBlock == 0) {
            return (false, 0, 0);
        }
        if (guardianFlags(g) >= 2) {
            return (true, g.holdBlock, g.holdTime);
        }
        if (g.dipBlock != 0 && (nowTime < g.dipTime || nowTime - g.dipTime < grace)) {
            return (true, g.holdBlock, g.holdTime);
        }
        return (false, 0, 0);
    }

    /// The stored moments behind the hold (for HlAdmin's hold history).
    function moments(State storage g)
        internal
        view
        returns (uint64 holdBlock, uint64 holdTime, uint64 dipBlock, uint64 dipTime)
    {
        return (g.holdBlock, g.holdTime, g.dipBlock, g.dipTime);
    }

    /// Brings the stored moments up to date before a change: a dip that reached the grace ended the hold.
    function settle(State storage g, uint64 nowTime, uint64 grace) internal {
        if (g.holdBlock != 0 && g.dipBlock != 0 && nowTime >= g.dipTime && nowTime - g.dipTime >= grace) {
            _endHold(g);
        }
    }

    /// After a change: two standing guardian flags start a hold (or end a dip); fewer start a dip.
    function recompute(State storage g, uint64 nowBlock, uint64 nowTime) internal {
        if (guardianFlags(g) >= 2) {
            if (g.holdBlock == 0) {
                (g.holdBlock, g.holdTime) = (nowBlock, nowTime);
            }
            (g.dipBlock, g.dipTime) = (0, 0);
        } else if (g.holdBlock != 0 && g.dipBlock == 0) {
            (g.dipBlock, g.dipTime) = (nowBlock, nowTime);
        }
    }

    /// A key sets its own flag at now (fresh on every set). Returns its index.
    function setFlag(State storage g, address key, uint64 nowBlock, uint64 nowTime, uint64 grace)
        internal
        returns (uint256 i)
    {
        i = position(g, key);
        if (i == KEYS) {
            revert UnknownKey();
        }
        settle(g, nowTime, grace);
        (g.flagBlock[i], g.flagTime[i]) = (nowBlock, nowTime);
        recompute(g, nowBlock, nowTime);
    }

    /// A key clears its own flag. Returns its index.
    function clearFlag(State storage g, address key, uint64 nowBlock, uint64 nowTime, uint64 grace)
        internal
        returns (uint256 i)
    {
        i = position(g, key);
        if (i == KEYS) {
            revert UnknownKey();
        }
        settle(g, nowTime, grace);
        (g.flagBlock[i], g.flagTime[i]) = (0, 0);
        recompute(g, nowBlock, nowTime);
    }

    /// An unpause: clears every flag last set at a block before `beforeBlock`, refused while a hold stands. Returns
    /// how many it cleared.
    function unpause(State storage g, uint64 beforeBlock, uint64 nowBlock, uint64 nowTime, uint64 grace)
        internal
        returns (uint256 n)
    {
        settle(g, nowTime, grace);
        (bool stands,,) = hold(g, nowTime, grace);
        if (stands) {
            revert Hold();
        }
        for (uint256 i = 0; i < KEYS; i++) {
            uint64 b = g.flagBlock[i];
            if (b != 0 && b < beforeBlock) {
                (g.flagBlock[i], g.flagTime[i]) = (0, 0);
                n++;
            }
        }
        recompute(g, nowBlock, nowTime);
    }

    /// Why a replacement would be refused, or zero: one to four pairs of (seat 1–6, key); no seat twice; no zero
    /// key; never a seat's current key; at most two guardian seats; no key in two places afterwards (guardians.rs
    /// `replace`, its checks in its order).
    function checkReplace(State storage g, IPausableGate.SeatKey[] memory pairs) internal view returns (bytes4) {
        if (pairs.length == 0 || pairs.length > 4) {
            return BadSeat.selector;
        }
        address[6] memory next = g.keys;
        bool[6] memory replaced;
        uint256 guardians;
        uint256 watch;
        for (uint256 p = 0; p < pairs.length; p++) {
            uint8 seat = pairs[p].seat;
            address key = pairs[p].key;
            if (seat < 1 || seat > KEYS) {
                return BadSeat.selector;
            }
            uint256 i = seat - 1;
            if (replaced[i]) {
                return BadSeat.selector;
            }
            if (key == address(0)) {
                return UnknownKey.selector;
            }
            if (g.keys[i] == key) {
                return CurrentKey.selector;
            }
            if (isGuardian(i)) {
                guardians++;
            } else {
                watch++;
            }
            next[i] = key;
            replaced[i] = true;
        }
        if (guardians > 2) {
            return TooManyGuardians.selector;
        }
        if (watch > WATCH) {
            return TooManyWatch.selector;
        }
        for (uint256 i = 0; i < KEYS; i++) {
            for (uint256 j = 0; j < i; j++) {
                if (next[i] == next[j]) {
                    return DuplicateKey.selector;
                }
            }
        }
        return bytes4(0);
    }

    /// A replacement of seats and slots: refused as `checkReplace` says; the replaced keys' flags cleared; a hold
    /// ended at once when this clear of a standing guardian flag leaves fewer than two.
    function replace(
        State storage g,
        IPausableGate.SeatKey[] memory pairs,
        uint64 nowBlock,
        uint64 nowTime,
        uint64 grace
    ) internal {
        bytes4 err = checkReplace(g, pairs);
        if (err != bytes4(0)) {
            assembly ("memory-safe") {
                mstore(0, err)
                revert(0, 4)
            }
        }
        settle(g, nowTime, grace);
        bool clearedGuardian;
        for (uint256 p = 0; p < pairs.length; p++) {
            uint256 i = pairs[p].seat - 1;
            if (isGuardian(i) && g.flagBlock[i] != 0) {
                clearedGuardian = true;
            }
            (g.flagBlock[i], g.flagTime[i]) = (0, 0);
            g.keys[i] = pairs[p].key;
        }
        // the 24-hour grace is for a holder's own clearing, never for GovDAO's replacement of the holders; a
        // replacement of nobody holding (a watch slot, a seat whose flag is down) during a dip ends nothing
        if (clearedGuardian && guardianFlags(g) < 2) {
            _endHold(g);
        } else {
            recompute(g, nowBlock, nowTime);
        }
    }

    /// No hold stands, and no dip.
    function _endHold(State storage g) private {
        g.holdBlock = 0;
        g.holdTime = 0;
        g.dipBlock = 0;
        g.dipTime = 0;
    }

    // --------------------------------------------------------------------------------------------------- views --

    function flags(State storage g) internal view returns (IPausableGate.Flag[6] memory out) {
        for (uint256 i = 0; i < KEYS; i++) {
            out[i] = IPausableGate.Flag(g.flagBlock[i], g.flagTime[i]);
        }
    }

    /// The standing hold's start at `nowTime`, zero when none stands.
    function holdView(State storage g, uint64 nowTime, uint64 grace) internal view returns (IPausableGate.Flag memory) {
        (bool stands, uint64 b, uint64 t) = hold(g, nowTime, grace);
        if (!stands) {
            return IPausableGate.Flag(0, 0);
        }
        return IPausableGate.Flag(b, t);
    }
}

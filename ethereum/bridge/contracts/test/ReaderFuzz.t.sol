// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Test} from "forge-std/Test.sol";

import {TmAmino} from "../src/lib/TmAmino.sol";
import {TmResults} from "../src/lib/TmResults.sol";

/// The reader's hot paths as written (hex and decimal in assembly, the one-byte varint shortcut).
contract ReaderLib {
    function hex32(bytes memory b) external pure returns (bytes32) {
        return TmResults.hex32(b, 0, b.length);
    }

    function decimal32(bytes memory b) external pure returns (uint32) {
        return TmResults.decimal32(b, 0, b.length);
    }

    function uvarint(bytes memory b) external pure returns (uint64, uint256) {
        return TmAmino.readUvarint(b, 0, b.length);
    }

    function field(bytes memory b) external pure returns (uint256, uint256, uint256, uint256) {
        return TmAmino.readField(b, 0, b.length);
    }

    /// The field as readKey, readBytes and skipValue read it, without readField's shortcut.
    function fieldSlow(bytes memory b) external pure returns (uint256 f, uint256 t, uint256 start, uint256 stop) {
        (f, t, start) = TmAmino.readKey(b, 0, b.length);
        if (t == TmAmino.TYP3_BYTELEN) {
            (start,, stop) = TmAmino.readBytes(b, start, b.length);
        } else {
            stop = TmAmino.skipValue(b, start, b.length, t);
        }
    }
}

/// The same three as results.rs and amino.rs write them, one byte at a time, plainly: the reference.
contract ReaderRef {
    function hex32(bytes memory b) external pure returns (bool, bytes32) {
        if (b.length != 64) {
            return (false, 0);
        }
        uint256 v;
        for (uint256 i = 0; i < 64; i++) {
            uint8 c = uint8(b[i]);
            uint8 n;
            if (c >= 48 && c <= 57) {
                n = c - 48;
            } else if (c >= 97 && c <= 102) {
                n = c - 97 + 10;
            } else {
                return (false, 0);
            }
            v = v * 16 + n;
        }
        return (true, bytes32(v));
    }

    function decimal32(bytes memory b) external pure returns (bool, uint32) {
        if (b.length == 0 || b.length > 10 || (b.length > 1 && b[0] == "0")) {
            return (false, 0);
        }
        uint256 v;
        for (uint256 i = 0; i < b.length; i++) {
            uint8 c = uint8(b[i]);
            if (c < 48 || c > 57) {
                return (false, 0);
            }
            v = v * 10 + (c - 48);
        }
        if (v > type(uint32).max) {
            return (false, 0);
        }
        return (true, uint32(v));
    }

    function uvarint(bytes memory b) external pure returns (bool, uint64, uint256) {
        uint256 v;
        uint256 shift;
        uint256 pos;
        while (true) {
            if (pos >= b.length) {
                return (false, 0, 0);
            }
            uint8 c = uint8(b[pos]);
            pos++;
            if (shift >= 64) {
                return (false, 0, 0);
            }
            v |= uint256(c & 0x7f) << shift;
            if (c & 0x80 == 0) {
                break;
            }
            shift += 7;
        }
        return (true, uint64(v), pos);
    }
}

/// The optimized hot paths of the results reader against the plain reference on fuzzed inputs: valid strings,
/// strings with one byte changed to anything, wrong lengths, leading zeros, values at and past the bounds.
contract ReaderFuzzTest is Test {
    ReaderLib internal lib;
    ReaderRef internal ref;

    function setUp() public {
        lib = new ReaderLib();
        ref = new ReaderRef();
    }

    function _hexOf(uint256 v) internal pure returns (bytes memory out) {
        bytes16 digits = "0123456789abcdef";
        out = new bytes(64);
        for (uint256 i = 0; i < 64; i++) {
            out[63 - i] = digits[v & 15];
            v >>= 4;
        }
    }

    function testFuzz_hex32(uint256 value, uint8 at, uint8 with, uint8 shape) public view {
        bytes memory b = _hexOf(value);
        if (shape % 4 == 1) {
            b[at % 64] = bytes1(with);
        } else if (shape % 4 == 2) {
            b = abi.encodePacked(b, bytes1(with));
        } else if (shape % 4 == 3) {
            bytes memory c = new bytes(63);
            for (uint256 i = 0; i < 63; i++) {
                c[i] = b[i];
            }
            b = c;
        }
        (bool ok, bytes32 want) = ref.hex32(b);
        try lib.hex32(b) returns (bytes32 got) {
            assertTrue(ok, "the library accepted what the reference refuses");
            assertEq(got, want);
            assertEq(uint256(got), shape % 4 == 0 ? value : uint256(got));
        } catch (bytes memory err) {
            assertFalse(ok, "the library refused what the reference accepts");
            assertEq(bytes4(err), TmResults.BadDispatch.selector);
        }
    }

    function testFuzz_decimal32(uint64 value, uint8 at, uint8 with, uint8 shape) public view {
        bytes memory b = bytes(vm.toString(uint256(value) % (shape % 3 == 0 ? uint256(1) << 33 : uint256(1) << 32)));
        if (shape % 5 == 1) {
            b[at % b.length] = bytes1(with);
        } else if (shape % 5 == 2) {
            b = abi.encodePacked("0", b);
        } else if (shape % 5 == 3) {
            b = "";
        }
        (bool ok, uint32 want) = ref.decimal32(b);
        try lib.decimal32(b) returns (uint32 got) {
            assertTrue(ok, "the library accepted what the reference refuses");
            assertEq(got, want);
        } catch (bytes memory err) {
            assertFalse(ok, "the library refused what the reference accepts");
            assertEq(bytes4(err), TmResults.BadDispatch.selector);
        }
    }

    function testFuzz_uvarint(bytes memory b) public view {
        (bool ok, uint64 want, uint256 wantNext) = ref.uvarint(b);
        try lib.uvarint(b) returns (uint64 got, uint256 next) {
            assertTrue(ok);
            assertEq(got, want);
            assertEq(next, wantNext);
        } catch (bytes memory err) {
            assertFalse(ok);
            assertEq(bytes4(err), TmAmino.Encoding.selector);
        }
    }

    function testFuzz_readField(bytes memory b, uint8 k, uint8 l) public view {
        if (b.length > 0 && k % 2 == 0) {
            b[0] = bytes1(k); // a key byte of every kind: one-byte keys, field 0, continuation bytes
        }
        if (b.length > 1 && l % 2 == 0) {
            b[1] = bytes1(l);
        }
        bool okSlow;
        uint256[4] memory slow;
        try lib.fieldSlow(b) returns (uint256 f, uint256 t, uint256 s, uint256 e) {
            (okSlow, slow) = (true, [f, t, s, e]);
        } catch (bytes memory err) {
            assertEq(bytes4(err), TmAmino.Encoding.selector);
        }
        try lib.field(b) returns (uint256 f, uint256 t, uint256 s, uint256 e) {
            assertTrue(okSlow, "the shortcut accepted what the slow path refuses");
            assertEq(f, slow[0]);
            assertEq(t, slow[1]);
            assertEq(s, slow[2]);
            assertEq(e, slow[3]);
        } catch (bytes memory err) {
            assertFalse(okSlow, "the shortcut refused what the slow path accepts");
            assertEq(bytes4(err), TmAmino.Encoding.selector);
        }
    }

    function test_theBounds() public view {
        assertEq(lib.decimal32("4294967295"), type(uint32).max);
        assertEq(lib.decimal32("0"), 0);
        assertEq(lib.hex32(_hexOf(type(uint256).max)), bytes32(type(uint256).max));
        (uint64 v, uint256 next) = lib.uvarint(hex"7f");
        assertEq(v, 127);
        assertEq(next, 1);
        (v, next) = lib.uvarint(hex"8001");
        assertEq(v, 128);
        assertEq(next, 2);
    }
}

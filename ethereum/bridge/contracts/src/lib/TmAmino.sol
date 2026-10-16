// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

/// The slice of amino (tm2's wire encoding, proto3-compatible) the light client needs, ported from
/// bridge/programs/gno-light-core/src/amino.rs (gno v1.2.0's tm2/pkg/amino/binary_encode.go):
///
/// - a field key is `uvarint(field << 3 | typ3)`: typ3 0 varint, 1 eight bytes, 2 length-prefixed, 5 four bytes;
/// - zero-valued non-struct fields are omitted; `int64` is a zigzag varint unless tagged `fixed64`; `uint32`,
///   `uint64` are plain uvarints; strings and byte slices are length-prefixed;
/// - `time.Time` is `{1: uvarint(seconds), 2: uvarint(nanos)}`, each omitted when zero.
///
/// Decoding works over a byte string in memory between two offsets (`pos` .. `end`, both relative to the string's
/// first byte), so a nested value is read in place, never copied. Every rule of the Rust reader is kept: a varint
/// of more than ten bytes is refused, bits past the 64th of a ten-byte varint are dropped (as Rust and Go's
/// `gnolight` do), a key's field number is 1 to 2^32 − 1, a length must fit in what is left.
library TmAmino {
    error Encoding();

    uint8 internal constant TYP3_VARINT = 0;
    uint8 internal constant TYP3_8BYTE = 1;
    uint8 internal constant TYP3_BYTELEN = 2;
    uint8 internal constant TYP3_4BYTE = 5;

    // ------------------------------------------------------------------------------------------------ encoding --

    /// The uvarint of `v` (one to ten bytes).
    function uvarint(uint64 v) internal pure returns (bytes memory out) {
        uint256 n = 1;
        for (uint64 x = v >> 7; x != 0; x >>= 7) {
            n++;
        }
        out = new bytes(n);
        for (uint256 i = 0; i < n; i++) {
            uint8 b = uint8(v & 0x7f);
            v >>= 7;
            if (i + 1 < n) {
                b |= 0x80;
            }
            out[i] = bytes1(b);
        }
    }

    /// Go's `binary.PutVarint` mapping: zigzag.
    function zigzag(int64 v) internal pure returns (uint64) {
        return uint64((v << 1) ^ (v >> 63));
    }

    /// The inverse of `zigzag`.
    function unzigzag(uint64 u) internal pure returns (int64) {
        return int64(u >> 1) ^ -int64(u & 1);
    }

    /// `v` as eight little-endian bytes (`binary:"fixed64"`).
    function le64(uint64 v) internal pure returns (bytes8) {
        uint64 r = 0;
        for (uint256 i = 0; i < 8; i++) {
            r = (r << 8) | (v & 0xff);
            v >>= 8;
        }
        return bytes8(r);
    }

    /// A length-prefixed field (a string, bytes, a nested struct), omitted when empty.
    function bytesField(uint8 field, bytes memory b) internal pure returns (bytes memory) {
        if (b.length == 0) {
            return "";
        }
        return abi.encodePacked(uvarint(uint64(field) << 3 | TYP3_BYTELEN), uvarint(uint64(b.length)), b);
    }

    /// A plain uvarint field (uint32, uint64), omitted when zero.
    function uvarintField(uint8 field, uint64 v) internal pure returns (bytes memory) {
        if (v == 0) {
            return "";
        }
        return abi.encodePacked(uvarint(uint64(field) << 3 | TYP3_VARINT), uvarint(v));
    }

    /// A zigzag varint field (int64, Go's int), omitted when zero.
    function zigzagField(uint8 field, int64 v) internal pure returns (bytes memory) {
        if (v == 0) {
            return "";
        }
        return abi.encodePacked(uvarint(uint64(field) << 3 | TYP3_VARINT), uvarint(zigzag(v)));
    }

    /// An eight-byte little-endian field, omitted when zero.
    function fixed64Field(uint8 field, uint64 v) internal pure returns (bytes memory) {
        if (v == 0) {
            return "";
        }
        return abi.encodePacked(uvarint(uint64(field) << 3 | TYP3_8BYTE), le64(v));
    }

    /// `time.Time`'s own fields: seconds then nanoseconds, each a uvarint of the value as a u64, each omitted when
    /// zero (amino.rs `time_bare`, which casts a negative value to u64).
    function timeBare(int64 secs, int32 nanos) internal pure returns (bytes memory) {
        return abi.encodePacked(uvarintField(1, uint64(secs)), uvarintField(2, uint64(int64(nanos))));
    }

    // ------------------------------------------------------------------------------------------------ decoding --

    /// The byte at `pos` of `b` (the caller checked `pos` against its end, which is within `b`).
    function byteAt(bytes memory b, uint256 pos) internal pure returns (uint256 v) {
        assembly ("memory-safe") {
            v := byte(0, mload(add(add(b, 32), pos)))
        }
    }

    /// A uvarint at `pos`, read no further than `end`; the offset after it.
    function readUvarint(bytes memory b, uint256 pos, uint256 end) internal pure returns (uint64, uint256) {
        // one byte, the common case (every key and length below 128)
        if (pos < end) {
            uint256 first = byteAt(b, pos);
            if (first < 0x80) {
                return (uint64(first), pos + 1);
            }
        }
        uint256 v;
        uint256 shift;
        while (true) {
            if (pos >= end) {
                revert Encoding();
            }
            uint256 c = byteAt(b, pos);
            pos++;
            if (shift >= 64) {
                revert Encoding();
            }
            v |= (c & 0x7f) << shift;
            if (c & 0x80 == 0) {
                break;
            }
            shift += 7;
        }
        return (uint64(v), pos);
    }

    /// A field key at `pos`: the field number, the typ3, and the offset after the key.
    function readKey(bytes memory b, uint256 pos, uint256 end)
        internal
        pure
        returns (uint256 field, uint256 typ3, uint256 next)
    {
        uint64 k;
        (k, next) = readUvarint(b, pos, end);
        field = k >> 3;
        if (field == 0 || field > type(uint32).max) {
            revert Encoding();
        }
        typ3 = k & 7;
    }

    /// A length-prefixed value at `pos`: where its bytes start, how many, and the offset after them.
    function readBytes(bytes memory b, uint256 pos, uint256 end)
        internal
        pure
        returns (uint256 start, uint256 len, uint256 next)
    {
        uint64 n;
        (n, start) = readUvarint(b, pos, end);
        len = n;
        next = start + len;
        if (next > end) {
            revert Encoding();
        }
    }

    /// Skips a value of the given typ3 (a field the reader does not know); the offset after it.
    function skipValue(bytes memory b, uint256 pos, uint256 end, uint256 typ3) internal pure returns (uint256 next) {
        if (typ3 == TYP3_VARINT) {
            (, next) = readUvarint(b, pos, end);
        } else if (typ3 == TYP3_8BYTE) {
            next = pos + 8;
        } else if (typ3 == TYP3_BYTELEN) {
            (,, next) = readBytes(b, pos, end);
        } else if (typ3 == TYP3_4BYTE) {
            next = pos + 4;
        } else {
            revert Encoding();
        }
        if (next > end) {
            revert Encoding();
        }
    }

    /// One field of a bare struct at `pos`: its number, its typ3, and where its value lies, [start, end) — a
    /// length-prefixed field's bytes, any other field's raw bytes; the next field starts at `end` (Rust's `fields`
    /// one step at a time).
    function readField(bytes memory b, uint256 pos, uint256 end)
        internal
        pure
        returns (uint256 field, uint256 typ3, uint256 start, uint256 stop)
    {
        // the common case, read whole: a one-byte key of a length-prefixed field (field 1–15) with a one-byte length
        if (pos + 1 < end) {
            uint256 k;
            uint256 l;
            assembly ("memory-safe") {
                let w := mload(add(add(b, 32), pos))
                k := byte(0, w)
                l := byte(1, w)
            }
            if (k < 0x80 && l < 0x80 && k & 7 == TYP3_BYTELEN && k >= 8) {
                start = pos + 2;
                stop = start + l;
                if (stop > end) {
                    revert Encoding();
                }
                return (k >> 3, TYP3_BYTELEN, start, stop);
            }
        }
        (field, typ3, start) = readKey(b, pos, end);
        if (typ3 == TYP3_BYTELEN) {
            (start,, stop) = readBytes(b, start, end);
        } else {
            stop = skipValue(b, start, end, typ3);
        }
    }

    /// Whether `b[start .. start + len]` equals `want`.
    function equals(bytes memory b, uint256 start, uint256 len, bytes memory want) internal pure returns (bool) {
        if (len != want.length) {
            return false;
        }
        return hashRange(b, start, len) == keccak256(want);
    }

    /// keccak256 of `b[start .. start + len]` (the caller checked the range).
    function hashRange(bytes memory b, uint256 start, uint256 len) internal pure returns (bytes32 h) {
        assembly ("memory-safe") {
            h := keccak256(add(add(b, 32), start), len)
        }
    }
}

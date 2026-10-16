// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Sha512} from "./Sha512.sol";

/// Ed25519 signature verification with exactly the semantics of Go's crypto/ed25519.Verify, which tm2 calls to check a
/// precommit (eth/bridge/PLAN.md §2, §6.6; Go 1.25.9, crypto/internal/fips140/ed25519 verifyWithDom):
///  - the top three bits of the signature's last byte are clear and S < L (S = 0 is canonical);
///  - A is decoded from the key's own 32 bytes the way edwards25519.Point.SetBytes does (lax: y is the low 255 bits
///    reduced mod p, so y >= p is accepted; the sign bit of a zero x is ignored; a y with no x is refused);
///  - k = SHA-512(R ‖ A ‖ M), the key hashed as its 32 bytes were given, read as a 64-byte little-endian integer
///    reduced mod L;
///  - R' = [S]B + [k](-A) computed in the whole curve group (cofactorless), and the signature is valid exactly when
///    the canonical encoding of R' equals R's 32 bytes (R is never decoded, so a non-canonical R never verifies).
/// The key's 32 bytes are the only key input: every value the arithmetic uses is derived from them here, never taken
/// from a caller (a point supplied beside the bytes is how a signature by one key verifies under another's bytes).
/// The arithmetic follows Go's edwards25519: extended coordinates (X:Y:Z:T), the complete formulas of Hisil, Wong,
/// Carter and Dawson for a = -1 as Go writes them (projP1xP1.Add, .Double, fromP1xP1), the same addition chains
/// for inversion and for the square root. Gates and gas: eth/bridge/M2a-NOTES.md.
library Ed25519 {
    /// The field prime 2^255 - 19.
    uint256 private constant P = 0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffed;
    /// The order of the base point, 2^252 + 27742317777372353535851937790883648493.
    uint256 private constant L = 0x1000000000000000000000000000000014def9dea2f79cd65812631a5cf5d3ed;
    /// d = -121665/121666 mod p.
    uint256 private constant D = 0x52036cee2b6ffe738cc740797779e89800700a4d4141d8ab75eb4dca135978a3;
    /// 2^((p-1)/4) mod p, a square root of -1.
    uint256 private constant SQRT_M1 = 0x2b8324804fc1df0b2b4d00993dfbd7a72f431806ad2fe478c4ee1b274a0ea0b0;
    /// 2^256 mod L, to reduce the 512-bit challenge.
    uint256 private constant TWO256_MOD_L = 0x0ffffffffffffffffffffffffffffffec6ef5bf4737dcf70d6ec31748d98951d;

    /// Whether r ‖ s is a valid signature of `message` by `publicKey`, exactly as Go's crypto/ed25519.Verify decides.
    /// `publicKey`, `r` and `s` are the 32-byte encodings as they appear on the wire (little-endian inside). Never
    /// reverts on any input, out of gas aside (an external caller's decoding is the caller's: Ed25519Verifier). tm2
    /// also refuses a signature that is not 64 bytes before it calls Verify; a caller splitting a signature into r
    /// and s must refuse other lengths itself.
    function verify(bytes32 publicKey, bytes memory message, bytes32 r, bytes32 s) internal view returns (bool) {
        // S: Go refuses sig[63] & 224 (the top three bits of S's last byte), then any S that is not below L.
        uint256 sv = _le(s);
        if (uint256(s) & 0xe0 != 0 || sv >= L) return false;
        // A: from the key's own bytes (edwards25519.Point.SetBytes).
        (bool onCurve, uint256 ax, uint256 ay) = _decode(publicKey);
        if (!onCurve) return false;
        // k = SHA-512(R ‖ A ‖ M) as a little-endian 512-bit integer, mod L: digest[0:32] is the low half.
        (bytes32 h0, bytes32 h1) = Sha512.hash(abi.encodePacked(r, publicKey, message));
        uint256 k = addmod(mulmod(_le(h1), TWO256_MOD_L, L), _le(h0), L);
        // R' = [S]B + [k](-A); valid exactly when its canonical encoding is R's 32 bytes.
        (uint256 x, uint256 y, uint256 z) = _doubleScalarMul(sv, k, ax, ay);
        uint256 zinv = _invert(z);
        x = mulmod(x, zinv, P);
        y = mulmod(y, zinv, P);
        return _le(r) == (y | ((x & 1) << 255));
    }

    /// The 32 bytes of `b` read as a little-endian integer (a byte reversal).
    function _le(bytes32 b) private pure returns (uint256 v) {
        assembly ("memory-safe") {
            v := b
            v := or(
                and(shr(8, v), 0x00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff),
                shl(8, and(v, 0x00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff))
            )
            v := or(
                and(shr(16, v), 0x0000ffff0000ffff0000ffff0000ffff0000ffff0000ffff0000ffff0000ffff),
                shl(16, and(v, 0x0000ffff0000ffff0000ffff0000ffff0000ffff0000ffff0000ffff0000ffff))
            )
            v := or(
                and(shr(32, v), 0x00000000ffffffff00000000ffffffff00000000ffffffff00000000ffffffff),
                shl(32, and(v, 0x00000000ffffffff00000000ffffffff00000000ffffffff00000000ffffffff))
            )
            v := or(
                and(shr(64, v), 0x0000000000000000ffffffffffffffff0000000000000000ffffffffffffffff),
                shl(64, and(v, 0x0000000000000000ffffffffffffffff0000000000000000ffffffffffffffff))
            )
            v := or(shr(128, v), shl(128, v))
        }
    }

    /// The point (x, y) the key's 32 bytes encode, as edwards25519.Point.SetBytes decodes it: y is the low 255 bits
    /// reduced mod p; x is the root of x^2 = (y^2 - 1)/(d y^2 + 1) whose low bit is the top bit of the last byte, or
    /// 0 whatever that bit when 0 is the only root; false when there is no root (no point has this y). Internal, not
    /// private, so a test can hold it against Go's SetBytes directly (audit round 2, E2-ED1: test/Ed25519Decode.t.sol);
    /// nothing outside this library calls it.
    function _decode(bytes32 publicKey) internal pure returns (bool, uint256 x, uint256 y) {
        uint256 v = _le(publicKey);
        unchecked {
            y = v & ((1 << 255) - 1);
            if (y >= P) y -= P; // y < 2^255 < 2p
        }
        uint256 y2 = mulmod(y, y, P);
        uint256 u = addmod(y2, P - 1, P); // y^2 - 1
        uint256 w = addmod(mulmod(y2, D, P), 1, P); // d y^2 + 1, never 0: -1/d is not a square
        // field.Element.SqrtRatio: the candidate u w^3 (u w^7)^((p-5)/8); w x^2 is then u (x is a root), -u (x sqrt(-1)
        // is), or neither (u/w is not a square). (p-5)/8 = 2^252 - 3.
        uint256 w3 = mulmod(mulmod(w, w, P), w, P);
        uint256 uw3 = mulmod(u, w3, P);
        uint256 uw7 = mulmod(uw3, mulmod(w3, w, P), P);
        (uint256 t,) = _pow250(uw7); // uw7^(2^250 - 1)
        t = mulmod(t, t, P);
        t = mulmod(t, t, P); // ^(2^252 - 4)
        x = mulmod(uw3, mulmod(t, uw7, P), P);
        uint256 check = mulmod(w, mulmod(x, x, P), P);
        if (check != u) {
            // u != 0 here: u = 0 gives x = 0 and check = 0 = u above.
            if (check != P - u) return (false, 0, 0);
            x = mulmod(x, SQRT_M1, P);
        }
        unchecked {
            if (x & 1 == 1) x = P - x; // the non-negative (even) root, as SqrtRatio returns
            if (v >> 255 == 1 && x != 0) x = P - x; // the sign bit selects the odd root; -0 = 0
        }
        return (true, x, y);
    }

    /// z^(2^250 - 1) and z^11, the common part of Go's addition chains for z^(p-2) and z^((p-5)/8).
    function _pow250(uint256 z) private pure returns (uint256 t, uint256 z11) {
        assembly ("memory-safe") {
            // x^(2^n) for n a multiple of 5.
            function sqn(x, n) -> r {
                let p := 0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffed
                r := x
                for {} n { n := sub(n, 5) } {
                    r := mulmod(r, r, p)
                    r := mulmod(r, r, p)
                    r := mulmod(r, r, p)
                    r := mulmod(r, r, p)
                    r := mulmod(r, r, p)
                }
            }
            let p := 0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffed
            let z2 := mulmod(z, z, p)
            let z9 := mulmod(z2, z2, p)
            z9 := mulmod(mulmod(z9, z9, p), z, p)
            z11 := mulmod(z9, z2, p)
            let t5 := mulmod(mulmod(z11, z11, p), z9, p) // 2^5 - 1
            let t10 := mulmod(sqn(t5, 5), t5, p) // 2^10 - 1
            let t15 := mulmod(sqn(t10, 5), t5, p) // 2^15 - 1
            let t30 := mulmod(sqn(t15, 15), t15, p) // 2^30 - 1
            let t60 := mulmod(sqn(t30, 30), t30, p) // 2^60 - 1
            let t120 := mulmod(sqn(t60, 60), t60, p) // 2^120 - 1
            let t240 := mulmod(sqn(t120, 120), t120, p) // 2^240 - 1
            t := mulmod(sqn(t240, 10), t10, p) // 2^250 - 1
        }
    }

    /// z^(p-2) = z^(2^255 - 21), the inverse of z (0 for 0), by Go's chain: (z^(2^250 - 1))^(2^5) z^11.
    function _invert(uint256 z) private pure returns (uint256 r) {
        (uint256 t, uint256 z11) = _pow250(z);
        assembly ("memory-safe") {
            let p := 0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffed
            t := mulmod(t, t, p)
            t := mulmod(t, t, p)
            t := mulmod(t, t, p)
            t := mulmod(t, t, p)
            t := mulmod(t, t, p)
            r := mulmod(t, z11, p)
        }
    }

    /// [s]B + [k](-A) for s, k < 2^253, A = (ax, ay) a point of the curve (from _decode), in projective coordinates.
    /// Straus's joint 2-bit windows: a table of [i]B + [j](-A) for i, j in 0..3, then from the top, two doublings and
    /// at most one addition per window. Every formula is complete on the whole curve group (a = -1 is a square and d
    /// is not), so small-order and mixed-order A, and any intermediate sum, are computed exactly, as Go computes them.
    function _doubleScalarMul(uint256 s, uint256 k, uint256 ax, uint256 ay)
        private
        pure
        returns (uint256 x, uint256 y, uint256 z)
    {
        assembly ("memory-safe") {
            // An extended point at e: X, Y, Z, T at e, e + 0x20, e + 0x40, e + 0x60 (x = X/Z, y = Y/Z, xy = T/Z).
            // Its cached form (projCached): Y + X, Y - X, Z, 2d T.
            function toCached(e, c) {
                let p := 0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffed
                let ex := mload(e)
                let ey := mload(add(e, 0x20))
                mstore(c, addmod(ey, ex, p))
                mstore(add(c, 0x20), addmod(ey, sub(p, ex), p))
                mstore(add(c, 0x40), mload(add(e, 0x40)))
                mstore(
                    add(c, 0x60),
                    mulmod(mload(add(e, 0x60)), 0x2406d9dc56dffce7198e80f2eef3d13000e0149a8283b156ebd69b9426b2f159, p)
                )
            }
            // o = 2e (projP1xP1.Double, then fromP1xP1).
            function double(e, o) {
                let p := 0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffed
                let ex := mload(e)
                let ey := mload(add(e, 0x20))
                let xx := mulmod(ex, ex, p)
                let yy := mulmod(ey, ey, p)
                let zz2 := mload(add(e, 0x40))
                zz2 := mulmod(zz2, zz2, p)
                zz2 := addmod(zz2, zz2, p)
                let s2 := addmod(ex, ey, p)
                s2 := mulmod(s2, s2, p)
                let y1 := addmod(yy, xx, p) // P1xP1: Y = YY + XX, Z = YY - XX, X = (X+Y)^2 - Y, T = 2ZZ - Z
                let z1 := addmod(yy, sub(p, xx), p)
                let x1 := addmod(s2, sub(p, y1), p)
                let t1 := addmod(zz2, sub(p, z1), p)
                mstore(o, mulmod(x1, t1, p))
                mstore(add(o, 0x20), mulmod(y1, z1, p))
                mstore(add(o, 0x40), mulmod(z1, t1, p))
                mstore(add(o, 0x60), mulmod(x1, y1, p))
            }
            // o = e + c, c cached (projP1xP1.Add, then fromP1xP1).
            function plus(e, c, o) {
                let p := 0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffed
                let ex := mload(e)
                let ey := mload(add(e, 0x20))
                let pp := mulmod(addmod(ey, ex, p), mload(c), p)
                let mm := mulmod(addmod(ey, sub(p, ex), p), mload(add(c, 0x20)), p)
                let tt := mulmod(mload(add(e, 0x60)), mload(add(c, 0x60)), p)
                let zz := mulmod(mload(add(e, 0x40)), mload(add(c, 0x40)), p)
                zz := addmod(zz, zz, p)
                let x1 := addmod(pp, sub(p, mm), p) // P1xP1: X = PP - MM, Y = PP + MM, Z = 2ZZ + TT, T = 2ZZ - TT
                let y1 := addmod(pp, mm, p)
                let z1 := addmod(zz, tt, p)
                let t1 := addmod(zz, sub(p, tt), p)
                mstore(o, mulmod(x1, t1, p))
                mstore(add(o, 0x20), mulmod(y1, z1, p))
                mstore(add(o, 0x40), mulmod(z1, t1, p))
                mstore(add(o, 0x60), mulmod(x1, y1, p))
            }
            // The window loop over the table at tbl (entry 4i + j at tbl + 0x80 (4i + j), cached), with s and k
            // in scratch memory at 0x00 and 0x20. The accumulator is projective (X:Y:Z) between windows, every
            // coordinate reduced (< p); the second doubling also yields T for the addition. Inside a step a sum or
            // difference that only feeds a mulmod is left unreduced: it is below 2p < 2^256, and mulmod reduces
            // any operand; a difference x - y is written x + (p - y) for y <= p, or x + (2p - y) for y < 2p, the
            // latter only inside an addmod (whose sum may pass 2^256).
            function ladder(tbl) -> X, Y, Z {
                let p := 0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffed
                let p2 := not(37) // 2p = 2^256 - 38
                Y := 1
                Z := 1
                for { let sh := 254 } sh {} {
                    sh := sub(sh, 2)
                    {
                        let xx := mulmod(X, X, p)
                        let yy := mulmod(Y, Y, p)
                        let zz := mulmod(Z, Z, p)
                        let s2 := add(X, Y) // < 2p
                        s2 := mulmod(s2, s2, p)
                        let y1 := add(yy, xx) // Y1 = YY + XX < 2p
                        yy := add(yy, sub(p, xx)) // Z1 = YY - XX < 2p
                        xx := addmod(s2, sub(p2, y1), p) // X1 = (X+Y)^2 - Y1
                        zz := addmod(add(zz, zz), sub(p2, yy), p) // T1 = 2ZZ - Z1
                        X := mulmod(xx, zz, p)
                        Y := mulmod(y1, yy, p)
                        Z := mulmod(yy, zz, p)
                    }
                    let T
                    {
                        let xx := mulmod(X, X, p)
                        let yy := mulmod(Y, Y, p)
                        let zz := mulmod(Z, Z, p)
                        let s2 := add(X, Y) // < 2p
                        s2 := mulmod(s2, s2, p)
                        let y1 := add(yy, xx) // Y1 < 2p
                        yy := add(yy, sub(p, xx)) // Z1 < 2p
                        xx := addmod(s2, sub(p2, y1), p) // X1
                        zz := addmod(add(zz, zz), sub(p2, yy), p) // T1
                        X := mulmod(xx, zz, p)
                        Y := mulmod(y1, yy, p)
                        Z := mulmod(yy, zz, p)
                        T := mulmod(xx, y1, p)
                    }
                    let d := or(shl(2, and(shr(sh, mload(0x00)), 3)), and(shr(sh, mload(0x20)), 3))
                    if d {
                        let c := add(tbl, shl(7, d))
                        let pp := mulmod(add(Y, X), mload(c), p) // Y + X < 2p
                        let mm := mulmod(add(Y, sub(p, X)), mload(add(c, 0x20)), p) // Y - X < 2p
                        let tt := mulmod(T, mload(add(c, 0x60)), p)
                        let zz := mulmod(Z, mload(add(c, 0x40)), p)
                        zz := addmod(zz, zz, p) // 2ZZ, reduced
                        X := add(pp, sub(p, mm)) // X1 = PP - MM < 2p
                        Y := add(pp, mm) // Y1 = PP + MM < 2p
                        Z := add(zz, tt) // Z1 = 2ZZ + TT < 2p
                        zz := add(zz, sub(p, tt)) // T1 = 2ZZ - TT < 2p
                        X := mulmod(X, zz, p)
                        Y := mulmod(Y, Z, p)
                        Z := mulmod(Z, zz, p)
                    }
                }
            }

            let p := 0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffed
            // Workspace past the free memory pointer, left unclaimed: the table (16 entries of 0x80; entry 0
            // unused), then B, 2B, 3B, -A, -2A, -3A extended and a spare.
            let tbl := mload(0x40)
            let ext := add(tbl, 0x800)
            {
                // B: y = 4/5, x even; T = xy.
                mstore(ext, 0x216936d3cd6e53fec0a4e231fdd6dc5c692cc7609525a7b2c9562d608f25d51a)
                mstore(add(ext, 0x20), 0x6666666666666666666666666666666666666666666666666666666666666658)
                mstore(add(ext, 0x40), 1)
                mstore(add(ext, 0x60), 0x67875f0fd78b766566ea4e8e64abe37d20f09f80775152f56dde8ab3a5b7dda3)
                // -A = (-x, y), T = -xy.
                let q := add(ext, 0x180)
                let nx := mod(sub(p, ax), p)
                mstore(q, nx)
                mstore(add(q, 0x20), ay)
                mstore(add(q, 0x40), 1)
                mstore(add(q, 0x60), mulmod(nx, ay, p))
            }
            toCached(ext, add(tbl, 0x200)) // 4: B
            double(ext, add(ext, 0x80))
            toCached(add(ext, 0x80), add(tbl, 0x400)) // 8: 2B
            plus(add(ext, 0x80), add(tbl, 0x200), add(ext, 0x100))
            toCached(add(ext, 0x100), add(tbl, 0x600)) // 12: 3B
            toCached(add(ext, 0x180), add(tbl, 0x80)) // 1: -A
            double(add(ext, 0x180), add(ext, 0x200))
            toCached(add(ext, 0x200), add(tbl, 0x100)) // 2: -2A
            plus(add(ext, 0x200), add(tbl, 0x80), add(ext, 0x280))
            toCached(add(ext, 0x280), add(tbl, 0x180)) // 3: -3A
            for { let i := 1 } lt(i, 4) { i := add(i, 1) } {
                for { let j := 1 } lt(j, 4) { j := add(j, 1) } {
                    // 4i + j: [i]B + [j](-A)
                    plus(add(ext, shl(7, sub(i, 1))), add(tbl, shl(7, j)), add(ext, 0x300))
                    toCached(add(ext, 0x300), add(tbl, shl(7, add(shl(2, i), j))))
                }
            }
            mstore(0x00, s)
            mstore(0x20, k)
            x, y, z := ladder(tbl)
        }
    }
}

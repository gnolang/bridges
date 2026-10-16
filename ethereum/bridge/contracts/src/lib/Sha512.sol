// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

/// SHA-512 (FIPS 180-4) in EVM assembly, for Ed25519.sol's challenge k = SHA-512(R ‖ A ‖ M) (eth/bridge/PLAN.md §6.6).
/// Gates (eth/bridge/M2a-NOTES.md): FIPS 180-4's examples and Go's crypto/sha512 on every length 0-300 and around
/// every padding boundary.
library Sha512 {
    /// The SHA-512 digest of `data` as its two 32-byte halves: `h0` is digest[0:32], `h1` digest[32:64].
    function hash(bytes memory data) internal pure returns (bytes32 h0, bytes32 h1) {
        assembly ("memory-safe") {
            // Compresses the 128-byte block at `blk` into the eight state words at `st`, using the 80-word schedule
            // at `w` and the 80 round constants at w + 0xa00 (one 64-bit value per 32-byte word, high bits zero).
            function compress(st, blk, w) {
                let m64 := 0xffffffffffffffff
                // W[0..15]: the block's sixteen big-endian 64-bit words.
                for { let i := 0 } lt(i, 0x80) { i := add(i, 0x20) } {
                    let x := mload(add(blk, i))
                    let o := add(w, shl(2, i))
                    mstore(o, shr(192, x))
                    mstore(add(o, 0x20), and(shr(128, x), m64))
                    mstore(add(o, 0x40), and(shr(64, x), m64))
                    mstore(add(o, 0x60), and(x, m64))
                }
                // W[16..79] = σ1(W[t-2]) + W[t-7] + σ0(W[t-15]) + W[t-16]. A rotation of a 64-bit x is the low 64
                // bits of x ‖ x (x | x << 64) shifted right: σ0 = ROTR1 ^ ROTR8 ^ SHR7, σ1 = ROTR19 ^ ROTR61 ^ SHR6.
                // The bits above 64 that a shift of x ‖ x leaves are not cleared until the sum is: the low 64 bits of a
                // sum depend only on the low 64 bits of its terms, and every term stays below 2^128.
                for { let q := add(w, 0x200) } lt(q, add(w, 0xa00)) { q := add(q, 0x20) } {
                    let x := mload(sub(q, 0x1e0))
                    let xx := or(x, shl(64, x))
                    let s := xor(xor(shr(1, xx), shr(8, xx)), shr(7, x))
                    x := mload(sub(q, 0x40))
                    xx := or(x, shl(64, x))
                    s := add(s, xor(xor(shr(19, xx), shr(61, xx)), shr(6, x)))
                    mstore(q, and(add(s, add(mload(sub(q, 0xe0)), mload(sub(q, 0x200)))), m64))
                }
                let a := mload(st)
                let b := mload(add(st, 0x20))
                let c := mload(add(st, 0x40))
                let d := mload(add(st, 0x60))
                let e := mload(add(st, 0x80))
                let f := mload(add(st, 0xa0))
                let g := mload(add(st, 0xc0))
                let h := mload(add(st, 0xe0))
                // Eighty rounds, eight per pass with the roles renamed instead of the values moved; Σ's high bits are
                // cleared with the sums, as in the schedule. A round with roles (A..H) sets D := D + T1 (the new e)
                // and H := T1 + T2 (the new a), where
                // T1 = H + Σ1(E) + Ch(E,F,G) + K[t] + W[t], T2 = Σ0(A) + Maj(A,B,C),
                // Σ1 = ROTR14 ^ ROTR18 ^ ROTR41, Σ0 = ROTR28 ^ ROTR34 ^ ROTR39, Ch = G ^ (E & (F ^ G)),
                // Maj = (A & B) | (C & (A | B)).
                for { let q := w } lt(q, add(w, 0xa00)) { q := add(q, 0x100) } {
                    {
                        let t := add(add(h, add(mload(q), mload(add(q, 0xa00)))), xor(g, and(e, xor(f, g))))
                        let x := or(e, shl(64, e))
                        t := add(t, xor(xor(shr(14, x), shr(18, x)), shr(41, x)))
                        d := and(add(d, t), m64)
                        x := or(a, shl(64, a))
                        t := add(t, xor(xor(shr(28, x), shr(34, x)), shr(39, x)))
                        h := and(add(t, or(and(a, b), and(c, or(a, b)))), m64)
                    }
                    {
                        let t := add(add(g, add(mload(add(q, 0x20)), mload(add(q, 0xa20)))), xor(f, and(d, xor(e, f))))
                        let x := or(d, shl(64, d))
                        t := add(t, xor(xor(shr(14, x), shr(18, x)), shr(41, x)))
                        c := and(add(c, t), m64)
                        x := or(h, shl(64, h))
                        t := add(t, xor(xor(shr(28, x), shr(34, x)), shr(39, x)))
                        g := and(add(t, or(and(h, a), and(b, or(h, a)))), m64)
                    }
                    {
                        let t := add(add(f, add(mload(add(q, 0x40)), mload(add(q, 0xa40)))), xor(e, and(c, xor(d, e))))
                        let x := or(c, shl(64, c))
                        t := add(t, xor(xor(shr(14, x), shr(18, x)), shr(41, x)))
                        b := and(add(b, t), m64)
                        x := or(g, shl(64, g))
                        t := add(t, xor(xor(shr(28, x), shr(34, x)), shr(39, x)))
                        f := and(add(t, or(and(g, h), and(a, or(g, h)))), m64)
                    }
                    {
                        let t := add(add(e, add(mload(add(q, 0x60)), mload(add(q, 0xa60)))), xor(d, and(b, xor(c, d))))
                        let x := or(b, shl(64, b))
                        t := add(t, xor(xor(shr(14, x), shr(18, x)), shr(41, x)))
                        a := and(add(a, t), m64)
                        x := or(f, shl(64, f))
                        t := add(t, xor(xor(shr(28, x), shr(34, x)), shr(39, x)))
                        e := and(add(t, or(and(f, g), and(h, or(f, g)))), m64)
                    }
                    {
                        let t := add(add(d, add(mload(add(q, 0x80)), mload(add(q, 0xa80)))), xor(c, and(a, xor(b, c))))
                        let x := or(a, shl(64, a))
                        t := add(t, xor(xor(shr(14, x), shr(18, x)), shr(41, x)))
                        h := and(add(h, t), m64)
                        x := or(e, shl(64, e))
                        t := add(t, xor(xor(shr(28, x), shr(34, x)), shr(39, x)))
                        d := and(add(t, or(and(e, f), and(g, or(e, f)))), m64)
                    }
                    {
                        let t := add(add(c, add(mload(add(q, 0xa0)), mload(add(q, 0xaa0)))), xor(b, and(h, xor(a, b))))
                        let x := or(h, shl(64, h))
                        t := add(t, xor(xor(shr(14, x), shr(18, x)), shr(41, x)))
                        g := and(add(g, t), m64)
                        x := or(d, shl(64, d))
                        t := add(t, xor(xor(shr(28, x), shr(34, x)), shr(39, x)))
                        c := and(add(t, or(and(d, e), and(f, or(d, e)))), m64)
                    }
                    {
                        let t := add(add(b, add(mload(add(q, 0xc0)), mload(add(q, 0xac0)))), xor(a, and(g, xor(h, a))))
                        let x := or(g, shl(64, g))
                        t := add(t, xor(xor(shr(14, x), shr(18, x)), shr(41, x)))
                        f := and(add(f, t), m64)
                        x := or(c, shl(64, c))
                        t := add(t, xor(xor(shr(28, x), shr(34, x)), shr(39, x)))
                        b := and(add(t, or(and(c, d), and(e, or(c, d)))), m64)
                    }
                    {
                        let t := add(add(a, add(mload(add(q, 0xe0)), mload(add(q, 0xae0)))), xor(h, and(f, xor(g, h))))
                        let x := or(f, shl(64, f))
                        t := add(t, xor(xor(shr(14, x), shr(18, x)), shr(41, x)))
                        e := and(add(e, t), m64)
                        x := or(b, shl(64, b))
                        t := add(t, xor(xor(shr(28, x), shr(34, x)), shr(39, x)))
                        a := and(add(t, or(and(b, c), and(d, or(b, c)))), m64)
                    }
                }
                mstore(st, and(add(mload(st), a), m64))
                mstore(add(st, 0x20), and(add(mload(add(st, 0x20)), b), m64))
                mstore(add(st, 0x40), and(add(mload(add(st, 0x40)), c), m64))
                mstore(add(st, 0x60), and(add(mload(add(st, 0x60)), d), m64))
                mstore(add(st, 0x80), and(add(mload(add(st, 0x80)), e), m64))
                mstore(add(st, 0xa0), and(add(mload(add(st, 0xa0)), f), m64))
                mstore(add(st, 0xc0), and(add(mload(add(st, 0xc0)), g), m64))
                mstore(add(st, 0xe0), and(add(mload(add(st, 0xe0)), h), m64))
            }

            // Workspace past the free memory pointer, left unclaimed (nothing else runs before this block ends):
            // the state (8 words), the schedule (80 words), the round constants (80 words), the padded message.
            let st := mload(0x40)
            let w := add(st, 0x100)
            let k := add(w, 0xa00)
            let buf := add(k, 0xa00)
            let len := mload(data)
            // Blocks after padding: the message, the 0x80 byte and the 16-byte bit length, rounded up to 128 bytes.
            let size := shl(7, shr(7, add(len, 144)))
            calldatacopy(buf, calldatasize(), size) // zeros
            mstore(add(buf, sub(size, 0x20)), shl(3, len)) // the bit length ends the last block
            mcopy(buf, add(data, 0x20), len)
            mstore8(add(buf, len), 0x80)

            mstore(st, 0x6a09e667f3bcc908)
            mstore(add(st, 0x20), 0xbb67ae8584caa73b)
            mstore(add(st, 0x40), 0x3c6ef372fe94f82b)
            mstore(add(st, 0x60), 0xa54ff53a5f1d36f1)
            mstore(add(st, 0x80), 0x510e527fade682d1)
            mstore(add(st, 0xa0), 0x9b05688c2b3e6c1f)
            mstore(add(st, 0xc0), 0x1f83d9abfb41bd6b)
            mstore(add(st, 0xe0), 0x5be0cd19137e2179)

            // K[0..79]: the first 64 bits of the fractional parts of the cube roots of the first 80 primes, packed
            // four to a word here and spread one to a word for the rounds.
            {
                let p := k
                function spread(dst, v) {
                    mstore(dst, shr(192, v))
                    mstore(add(dst, 0x20), and(shr(128, v), 0xffffffffffffffff))
                    mstore(add(dst, 0x40), and(shr(64, v), 0xffffffffffffffff))
                    mstore(add(dst, 0x60), and(v, 0xffffffffffffffff))
                }
                spread(p, 0x428a2f98d728ae227137449123ef65cdb5c0fbcfec4d3b2fe9b5dba58189dbbc)
                spread(add(p, 0x080), 0x3956c25bf348b53859f111f1b605d019923f82a4af194f9bab1c5ed5da6d8118)
                spread(add(p, 0x100), 0xd807aa98a303024212835b0145706fbe243185be4ee4b28c550c7dc3d5ffb4e2)
                spread(add(p, 0x180), 0x72be5d74f27b896f80deb1fe3b1696b19bdc06a725c71235c19bf174cf692694)
                spread(add(p, 0x200), 0xe49b69c19ef14ad2efbe4786384f25e30fc19dc68b8cd5b5240ca1cc77ac9c65)
                spread(add(p, 0x280), 0x2de92c6f592b02754a7484aa6ea6e4835cb0a9dcbd41fbd476f988da831153b5)
                spread(add(p, 0x300), 0x983e5152ee66dfaba831c66d2db43210b00327c898fb213fbf597fc7beef0ee4)
                spread(add(p, 0x380), 0xc6e00bf33da88fc2d5a79147930aa72506ca6351e003826f142929670a0e6e70)
                spread(add(p, 0x400), 0x27b70a8546d22ffc2e1b21385c26c9264d2c6dfc5ac42aed53380d139d95b3df)
                spread(add(p, 0x480), 0x650a73548baf63de766a0abb3c77b2a881c2c92e47edaee692722c851482353b)
                spread(add(p, 0x500), 0xa2bfe8a14cf10364a81a664bbc423001c24b8b70d0f89791c76c51a30654be30)
                spread(add(p, 0x580), 0xd192e819d6ef5218d69906245565a910f40e35855771202a106aa07032bbd1b8)
                spread(add(p, 0x600), 0x19a4c116b8d2d0c81e376c085141ab532748774cdf8eeb9934b0bcb5e19b48a8)
                spread(add(p, 0x680), 0x391c0cb3c5c95a634ed8aa4ae3418acb5b9cca4f7763e373682e6ff3d6b2b8a3)
                spread(add(p, 0x700), 0x748f82ee5defb2fc78a5636f43172f6084c87814a1f0ab728cc702081a6439ec)
                spread(add(p, 0x780), 0x90befffa23631e28a4506cebde82bde9bef9a3f7b2c67915c67178f2e372532b)
                spread(add(p, 0x800), 0xca273eceea26619cd186b8c721c0c207eada7dd6cde0eb1ef57d4f7fee6ed178)
                spread(add(p, 0x880), 0x06f067aa72176fba0a637dc5a2c898a6113f9804bef90dae1b710b35131c471b)
                spread(add(p, 0x900), 0x28db77f523047d8432caab7b40c724933c9ebe0a15c9bebc431d67c49c100d4c)
                spread(add(p, 0x980), 0x4cc5d4becb3e42b6597f299cfc657e2a5fcb6fab3ad6faec6c44198c4a475817)
            }

            for { let blk := buf } lt(blk, add(buf, size)) { blk := add(blk, 0x80) } {
                compress(st, blk, w)
            }

            h0 := or(
                or(shl(192, mload(st)), shl(128, mload(add(st, 0x20)))),
                or(shl(64, mload(add(st, 0x40))), mload(add(st, 0x60)))
            )
            h1 := or(
                or(shl(192, mload(add(st, 0x80))), shl(128, mload(add(st, 0xa0)))),
                or(shl(64, mload(add(st, 0xc0))), mload(add(st, 0xe0)))
            )
        }
    }
}

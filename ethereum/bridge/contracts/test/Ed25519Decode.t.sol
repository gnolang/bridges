// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {console} from "forge-std/Test.sol";
import {Ed25519} from "../src/lib/Ed25519.sol";
import {Ed25519VectorBase} from "./Ed25519Vectors.t.sol";

/// The gates audit round 2 found missing (E2-ED1: eth/bridge/audits/r2/ed25519.md): five planted bugs in the key
/// decoding passed every gate, three of them universal forgeries. Here the decoding is held against Go's
/// edwards25519.Point.SetBytes directly (decode.vec: its verdict and both affine coordinates, from Go 1.25.9's own
/// code, tools/ed25519vectors/goedwards25519), and through the whole verify against the auditor's forgery probes
/// (probes.vec: under keys a decoder can get wrong, R = [S]B and R = [S]B - [j]T with k = j mod ord(T), which only a
/// key that really is that point verifies; and, audit round 3's E3-ED2, R = [r]B, S = r + k, which only a key that is
/// B verifies — a key Go refuses read as B, in the decoding or in verify, is a universal forgery the small-order probes
/// never aim at). tools/ed25519vectors/mutants.py's decode mutants N1–N5, D7 and VB1 fail here.
contract Ed25519DecodeTest is Ed25519VectorBase {
    /// Every key of decode.vec: _decode's verdict and both coordinates equal Go's; and a key Go refuses is refused
    /// before the hash (an early-refusal tripwire on the gas: a decoder that let it through would pay a whole
    /// verification, about 200,000).
    function test_decode_agreesWithGoSetBytes() public view {
        string[] memory lines = _lines(string.concat(DIR, "decode.vec"));
        uint256 n;
        uint256 decoded;
        uint256 maxRefusalGas;
        for (uint256 i = 0; i < lines.length; i++) {
            if (!_isRecord(lines[i])) continue;
            string[] memory f = vm.split(lines[i], " ");
            require(f.length == 5, string.concat("bad record: ", lines[i]));
            bool want = vm.parseUint(f[0]) == 1;
            bytes32 pk = vm.parseBytes32(f[1]);
            (bool ok, uint256 x, uint256 y) = Ed25519._decode(pk);
            assertEq(ok, want, string.concat("verdict: ", f[4]));
            if (want) {
                assertEq(x, vm.parseUint(f[2]), string.concat("x: ", f[4]));
                assertEq(y, vm.parseUint(f[3]), string.concat("y: ", f[4]));
                decoded++;
            } else {
                uint256 g = gasleft();
                bool v = Ed25519.verify(pk, "", bytes32(0), bytes32(0));
                g -= gasleft();
                assertFalse(v, f[4]);
                assertLt(g, 30_000, string.concat("refused before the hash: ", f[4]));
                if (g > maxRefusalGas) maxRefusalGas = g;
            }
            n++;
        }
        assertEq(n, 478, "every key");
        assertEq(decoded, 322, "Go decodes 322, refuses 156");
        console.log(
            "decode: 478 keys agree with Go; a refused key costs at most", maxRefusalGas, "gas (before the hash)"
        );
    }

    /// The forgery probes (probes.vec) through the library: Go's verdict on every one, 22 of the 1,242 accepted (the
    /// keys that really are small-order points; none of the 138 .asB probes, no key being B); the same through the
    /// verifier contract in Ed25519Test.
    function test_forgeryProbes_agreeWithGo() public view {
        Tally memory t = _replay("probes.vec");
        _report("forgery probes against key decoding", t);
        assertEq(t.records, 1242, "138 keys: 8 probes at small-order points each, then one .asB each");
        assertEq(t.goAccepts, 22);
        assertEq(t.disagreements, 0, "agreement with Go");
    }
}

// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {console} from "forge-std/Test.sol";
import {Sha512} from "../src/lib/Sha512.sol";
import {Ed25519VectorBase} from "./Ed25519Vectors.t.sol";

/// Gate 6 (eth/bridge/M2a-NOTES.md): SHA-512 against FIPS 180-4's examples and Go's crypto/sha512 on every length
/// 0-300, around every padding boundary and on long messages (test/vectors/ed25519/sha512.vec); and the same, and the
/// signature sets through Ed25519.verify, with the memory past the free pointer dirtied first (audit round 3, E3-ED4).
contract Ed25519Sha512Test is Ed25519VectorBase {
    function _digest(bytes memory m) internal pure returns (bytes memory) {
        (bytes32 h0, bytes32 h1) = Sha512.hash(m);
        return abi.encodePacked(h0, h1);
    }

    function test_sha512_vectors() public view {
        string[] memory lines = vm.split(vm.readFile("test/vectors/ed25519/sha512.vec"), "\n");
        uint256 n;
        uint256 fips;
        for (uint256 i = 0; i < lines.length; i++) {
            bytes memory b = bytes(lines[i]);
            if (b.length == 0 || b[0] == "#") continue;
            string[] memory f = vm.split(lines[i], " ");
            bytes memory m = vm.parseBytes(f[0]);
            assertEq(_digest(m), vm.parseBytes(f[1]), f[2]);
            if (vm.indexOf(f[2], "fips.") == 0) fips++;
            n++;
        }
        assertEq(fips, 3);
        assertEq(n, 951);
        console.log(
            "SHA-512: 951 vectors (3 FIPS examples, 903 random of lengths 0-300, 40 at padding boundaries, 5 long)"
        );
    }

    /// FIPS 180-2 appendix C.3: one million bytes of 'a' (7,813 blocks).
    function test_sha512_millionA() public pure {
        bytes memory m = new bytes(1_000_000);
        assembly {
            let p := add(m, 0x20)
            let w := 0x6161616161616161616161616161616161616161616161616161616161616161
            for { let i := 0 } lt(i, 1000000) { i := add(i, 0x20) } { mstore(add(p, i), w) }
        }
        assertEq(
            _digest(m),
            hex"e718483d0ce769644e2e42c7bc15b4638e1f98b13b2044285632a803afa973ebde0ff244877ea60a4cb0432ce577c31beb009c5c2c49aa2e4eadb217ad8cc09b"
        );
    }

    /// The input is left as it was, and memory the caller allocated before the call is untouched.
    function test_sha512_leavesMemoryAlone() public pure {
        bytes memory a = hex"0102030405";
        bytes memory before = bytes.concat(a);
        bytes32 guard = keccak256("guard");
        bytes memory g = abi.encodePacked(guard);
        Sha512.hash(a);
        assertEq(a, before);
        assertEq(bytes32(g), guard);
    }

    /// Every vector again with the 16 KB past the free pointer filled with 0xff first (audit round 3, E3-ED4): the
    /// library pads in a workspace past the pointer, so the bytes after the message and its 0x80 must be zeroed by it,
    /// never taken as found. In every frame the other gates run the workspace lands on memory never written, and the
    /// zeroing could be dropped unseen: the verifier contract hashes once per call, but an inline caller that used
    /// that memory before would get wrong digests (refusing honest signatures; never accepting, the padded input
    /// staying injective). Each digest that differs is logged and counted.
    function test_sha512_dirtyMemory() public view {
        string[] memory lines = vm.split(vm.readFile("test/vectors/ed25519/sha512.vec"), "\n");
        uint256 n;
        uint256 wrong;
        for (uint256 i = 0; i < lines.length; i++) {
            bytes memory b = bytes(lines[i]);
            if (b.length == 0 || b[0] == "#") continue;
            string[] memory f = vm.split(lines[i], " ");
            bytes memory m = vm.parseBytes(f[0]);
            bytes memory want = vm.parseBytes(f[1]);
            _dirty();
            if (keccak256(_digest(m)) != keccak256(want)) {
                wrong++;
                console.log("WRONG DIGEST on dirty memory:", f[2]);
            }
            n++;
        }
        console.log(
            string.concat("SHA-512 on dirty memory: ", vm.toString(n), " vectors, ", vm.toString(wrong), " wrong")
        );
        assertEq(n, 951);
        assertEq(wrong, 0, "every digest on dirty memory");
    }

    /// The signature sets through Ed25519.verify with the memory past the free pointer dirtied before each call
    /// (E3-ED4): Go's verdict on every record of the published sets, the honest gas set and the edges.
    function test_verify_dirtyMemory() public view {
        string[5] memory files = ["rfc8032.vec", "wycheproof.vec", "speccheck.vec", "gas.vec", "edges.vec"];
        Tally memory t;
        for (uint256 i = 0; i < files.length; i++) {
            string[] memory lines = _lines(string.concat(DIR, files[i]));
            for (uint256 j = 0; j < lines.length; j++) {
                if (!_isRecord(lines[j])) continue;
                Vec memory v = _parse(lines[j]);
                t.records++;
                if (v.goValid) t.goAccepts++;
                if (v.sig.length != 64) t.byLength++;
                _dirty();
                if (_verify(v) != v.goValid) {
                    t.disagreements++;
                    console.log("DISAGREES with Go on dirty memory:", v.label, v.goValid ? "Go accepts" : "Go refuses");
                }
            }
        }
        _report("Ed25519.verify on dirty memory (rfc8032, wycheproof, speccheck, gas, edges)", t);
        assertEq(t.records, 5 + 151 + 12 + 8 + 607);
        assertEq(t.disagreements, 0, "agreement with Go on dirty memory");
    }
}

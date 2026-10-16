// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Test, console} from "forge-std/Test.sol";
import {Ed25519} from "../src/lib/Ed25519.sol";
import {IEd25519Verifier} from "../src/Ed25519Verifier.sol";

/// Reads the vector files of test/vectors/ed25519 (written by eth/bridge/tools/ed25519vectors, every record with Go's
/// crypto/ed25519 verdict): '#' lines are comments; a record is "<0|1> <pk> <sig> <msg> <label>".
abstract contract Ed25519VectorBase is Test {
    string internal constant DIR = "test/vectors/ed25519/";

    struct Vec {
        bool goValid;
        bytes32 pk;
        bytes sig;
        bytes msg;
        string label;
    }

    struct Tally {
        uint256 records;
        uint256 goAccepts;
        uint256 byLength; // signatures that are not 64 bytes: refused by tm2 before Verify, never passed to the library
        uint256 disagreements;
    }

    function _lines(string memory path) internal view returns (string[] memory) {
        return vm.split(vm.readFile(path), "\n");
    }

    function _isRecord(string memory line) internal pure returns (bool) {
        bytes memory b = bytes(line);
        return b.length != 0 && b[0] != "#";
    }

    function _parse(string memory line) internal pure returns (Vec memory v) {
        string[] memory f = vm.split(line, " ");
        require(f.length == 5, string.concat("bad record: ", line));
        v.goValid = vm.parseUint(f[0]) == 1;
        v.pk = vm.parseBytes32(f[1]);
        v.sig = vm.parseBytes(f[2]);
        v.msg = vm.parseBytes(f[3]);
        v.label = f[4];
    }

    /// The verdict tm2 reaches through the library: a signature that is not 64 bytes is refused by length
    /// (PubKeyEd25519.VerifyBytes) before Verify; otherwise Ed25519.verify on its two halves.
    function _verify(Vec memory v) internal view returns (bool) {
        if (v.sig.length != 64) return false;
        bytes memory sig = v.sig;
        bytes32 r;
        bytes32 s;
        assembly {
            r := mload(add(sig, 0x20))
            s := mload(add(sig, 0x40))
        }
        return Ed25519.verify(v.pk, v.msg, r, s);
    }

    /// The same verdict through a deployed verifier contract.
    function _verifyVia(IEd25519Verifier verifier, Vec memory v) internal view returns (bool) {
        if (v.sig.length != 64) return false;
        bytes memory sig = v.sig;
        bytes32 r;
        bytes32 s;
        assembly {
            r := mload(add(sig, 0x20))
            s := mload(add(sig, 0x40))
        }
        return verifier.verify(v.pk, v.msg, r, s);
    }

    /// Replays every record of a file through the library (verifier zero) or a deployed verifier; logs each
    /// disagreement with Go.
    function _replay(string memory file) internal view returns (Tally memory t) {
        return _replay(file, IEd25519Verifier(address(0)));
    }

    function _replay(string memory file, IEd25519Verifier verifier) internal view returns (Tally memory t) {
        string[] memory lines = _lines(string.concat(DIR, file));
        for (uint256 i = 0; i < lines.length; i++) {
            if (!_isRecord(lines[i])) continue;
            Vec memory v = _parse(lines[i]);
            t.records++;
            if (v.goValid) t.goAccepts++;
            if (v.sig.length != 64) t.byLength++;
            bool got = address(verifier) == address(0) ? _verify(v) : _verifyVia(verifier, v);
            if (got != v.goValid) {
                t.disagreements++;
                console.log("DISAGREES with Go:", v.label, v.goValid ? "Go accepts" : "Go refuses");
            }
        }
    }

    /// Fills the 16 KB past the free memory pointer with 0xff, as an inline caller that used memory there before would
    /// leave it (Solidity never promises that memory is zero): the next allocation, and any workspace a library takes
    /// past the pointer, starts on dirty bytes.
    function _dirty() internal pure {
        assembly ("memory-safe") {
            let p := mload(0x40)
            for { let i := 0 } lt(i, 0x4000) { i := add(i, 0x20) } { mstore(add(p, i), not(0)) }
        }
    }

    function _report(string memory name, Tally memory t) internal pure {
        console.log(
            string.concat(
                name,
                ": ",
                vm.toString(t.records),
                " records, Go accepts ",
                vm.toString(t.goAccepts),
                ", refuses ",
                vm.toString(t.records - t.goAccepts),
                " (",
                vm.toString(t.byLength),
                " by length), disagreements ",
                vm.toString(t.disagreements)
            )
        );
    }
}

/// Gates 1-3 (eth/bridge/M2a-NOTES.md): RFC 8032 §7.1, Wycheproof's ed25519_test.json and the "Taming the many EdDSAs"
/// cases, every record compared with Go's verdict.
contract Ed25519PublishedVectorsTest is Ed25519VectorBase {
    function test_rfc8032_section7_1() public view {
        Tally memory t = _replay("rfc8032.vec");
        _report("RFC 8032 7.1", t);
        assertEq(t.records, 5, "the five Ed25519 tests");
        assertEq(t.goAccepts, 5, "Go accepts all five");
        assertEq(t.disagreements, 0, "agreement with Go");
    }

    /// Each RFC 8032 signature refused once its message, or any byte of it, changes; and once R or S changes.
    function test_rfc8032_tampered() public view {
        string[] memory lines = _lines(string.concat(DIR, "rfc8032.vec"));
        uint256 n;
        for (uint256 i = 0; i < lines.length; i++) {
            if (!_isRecord(lines[i])) continue;
            Vec memory v = _parse(lines[i]);
            assertTrue(_verify(v), v.label);
            Vec memory w = _parse(lines[i]);
            w.msg = bytes.concat(w.msg, hex"00");
            assertFalse(_verify(w), string.concat(v.label, " +1 byte"));
            if (v.msg.length > 0) {
                w = _parse(lines[i]);
                w.msg[w.msg.length / 2] ^= 0x01;
                assertFalse(_verify(w), string.concat(v.label, " msg bit"));
            }
            w = _parse(lines[i]);
            w.sig[0] ^= 0x01;
            assertFalse(_verify(w), string.concat(v.label, " R bit"));
            w = _parse(lines[i]);
            w.sig[32] ^= 0x01;
            assertFalse(_verify(w), string.concat(v.label, " S bit"));
            n++;
        }
        assertEq(n, 5);
    }

    function test_wycheproof_ed25519() public view {
        Tally memory t = _replay("wycheproof.vec");
        _report("Wycheproof ed25519_test.json", t);
        assertEq(t.records, 151, "every case of the pinned file");
        assertEq(t.goAccepts, 88);
        assertEq(t.byLength, 12);
        assertEq(t.disagreements, 0, "agreement with Go");
    }

    function test_speccheck_cases() public view {
        Tally memory t = _replay("speccheck.vec");
        _report("ed25519-speccheck cases.json", t);
        assertEq(t.records, 12, "the twelve cases");
        assertEq(t.goAccepts, 5, "Go accepts cases 0-3 and 11");
        assertEq(t.disagreements, 0, "agreement with Go");
    }

    /// The named cases behind M2a-NOTES.md's statements, then audit round 3's families (tools/ed25519vectors/
    /// families.go): every special R beside every special S under honest keys (edge.special, E3-ED1: a fast path for
    /// one pair is a universal forgery, and the class hung on Wycheproof tc15), R' = -R under a known secret
    /// (edge.negR, E3-ED3: the comparator's sign rule hung on tc59), and a small-order R' in each lax encoding beside
    /// its canonical one (edge.laxR, E3-ED3: y + p and a zero x's sign bit). Each label ends in the verdict the rules
    /// predict, which the generator checked Go's against; every record's verdict here is Go's, each disagreement
    /// logged and counted.
    function test_edges() public view {
        string[] memory lines = _lines(string.concat(DIR, "edges.vec"));
        string[3] memory fams = ["edge.special.", "edge.negR.", "edge.laxR."];
        uint256[3] memory perFam;
        Tally memory t;
        for (uint256 i = 0; i < lines.length; i++) {
            if (!_isRecord(lines[i])) continue;
            Vec memory v = _parse(lines[i]);
            bool predicted = vm.indexOf(v.label, ".accept") != type(uint256).max;
            assertEq(v.goValid, predicted, string.concat("Go vs prediction: ", v.label));
            t.records++;
            if (v.goValid) t.goAccepts++;
            for (uint256 f = 0; f < 3; f++) {
                if (vm.indexOf(v.label, fams[f]) == 0) perFam[f]++;
            }
            if (_verify(v) != v.goValid) {
                t.disagreements++;
                console.log("DISAGREES with Go:", v.label, v.goValid ? "Go accepts" : "Go refuses");
            }
        }
        _report("edges (25 named, then the special, negR and laxR families)", t);
        assertEq(t.records, 607);
        assertEq(t.goAccepts, 31, "11 named, 20 canonical small-order R'");
        assertEq(perFam[0], 480, "special: 16 R x 5 S x 6 honest keys");
        assertEq(perFam[1], 46, "negR: 32 prime-order keys, 2 mixed for each of the 7 torsion points");
        assertEq(perFam[2], 56, "laxR: 4 prime-order keys x the identity's 4 encodings, 40 under order-4 mixed keys");
        assertEq(t.disagreements, 0, "agreement with Go");
    }
}

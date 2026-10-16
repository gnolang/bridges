// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {console} from "forge-std/Test.sol";
import {Ed25519} from "../src/lib/Ed25519.sol";
import {Ed25519Verifier, IEd25519Verifier} from "../src/Ed25519Verifier.sol";
import {Ed25519VectorBase} from "./Ed25519Vectors.t.sol";

/// The library behind an external call, as a light client would reach it, for the revert and gas checks.
contract Ed25519Caller {
    function verify(bytes32 pk, bytes memory message, bytes32 r, bytes32 s) external view returns (bool) {
        return Ed25519.verify(pk, message, r, s);
    }
}

/// The same external function without the library: the difference in runtime size is what Ed25519.sol (with
/// Sha512.sol) adds to a contract that calls it.
contract Ed25519SizeBaseline {
    function verify(bytes32 pk, bytes memory message, bytes32 r, bytes32 s) external pure returns (bool) {
        return pk == r && s == keccak256(message);
    }
}

/// A caller of the separate verifier contract (the layout proposed for GnoLightClient): what calling it adds.
contract Ed25519ViaVerifier {
    IEd25519Verifier internal immutable VERIFIER;

    constructor(IEd25519Verifier verifier) {
        VERIFIER = verifier;
    }

    function verify(bytes32 pk, bytes memory message, bytes32 r, bytes32 s) external view returns (bool) {
        return VERIFIER.verify(pk, message, r, s);
    }
}

contract Ed25519Test is Ed25519VectorBase {
    uint256 internal constant L = 0x1000000000000000000000000000000014def9dea2f79cd65812631a5cf5d3ed;

    Ed25519Caller internal caller;
    Ed25519Verifier internal verifier;
    Ed25519ViaVerifier internal via;

    function setUp() public {
        caller = new Ed25519Caller();
        verifier = new Ed25519Verifier();
        via = new Ed25519ViaVerifier(verifier);
    }

    function _le(uint256 v) internal pure returns (bytes32 out) {
        for (uint256 i = 0; i < 32; i++) {
            out |= bytes32(uint256(uint8(v >> (8 * i))) << (8 * (31 - i)));
        }
    }

    // ---------------------------------------------------------------------------------------------------------------
    // Gate 5: the key binding. The only key input is the key's 32 bytes; a signature made with another key's secret,
    // even with this key's bytes hashed into its challenge (the forgery SCL accepted when handed the other key's
    // point beside these bytes), is refused. These are checked by their stated outcome, not only against Go, since a
    // Go-derived differential run never pairs a point with foreign bytes.
    // ---------------------------------------------------------------------------------------------------------------

    function test_binding_signatureByAnotherKeyRefused() public view {
        string[] memory lines = _lines(string.concat(DIR, "binding.vec"));
        uint256 forged;
        uint256 honestOther;
        uint256 honestOwn;
        uint256 flips;
        for (uint256 i = 0; i < lines.length; i++) {
            if (!_isRecord(lines[i])) continue;
            Vec memory v = _parse(lines[i]);
            bool got = _verify(v);
            assertEq(got, v.goValid, string.concat("Go: ", v.label));
            if (_endsWith(v.label, ".forgedAgainstA") || _endsWith(v.label, ".forgedAgainstA2")) {
                assertFalse(got, v.label);
                forged++;
            } else if (_endsWith(v.label, ".honestA2AgainstA")) {
                assertFalse(got, v.label);
                honestOther++;
            } else if (_endsWith(v.label, ".honestA2AgainstA2")) {
                assertTrue(got, v.label); // the same machinery accepts the honest pairing
                honestOwn++;
            } else {
                assertTrue(vm.indexOf(v.label, ".keyflip.bit") != type(uint256).max, v.label);
                assertFalse(got, v.label);
                flips++;
            }
        }
        assertEq(forged, 64);
        assertEq(honestOther, 32);
        assertEq(honestOwn, 32);
        assertEq(flips, 256);
        console.log("binding: 64 forgeries (A2's secret, A's bytes hashed) refused under A and A2; 32 honest A2");
        console.log("         signatures refused under A, accepted under A2; 256 one-bit key changes refused");
    }

    function _endsWith(string memory s, string memory suffix) internal pure returns (bool) {
        bytes memory a = bytes(s);
        bytes memory b = bytes(suffix);
        if (b.length > a.length) return false;
        for (uint256 i = 0; i < b.length; i++) {
            if (a[a.length - b.length + i] != b[i]) return false;
        }
        return true;
    }

    // ---------------------------------------------------------------------------------------------------------------
    // Never reverts: any 32-byte key, any message, any r and s give a verdict.
    // ---------------------------------------------------------------------------------------------------------------

    function testFuzz_neverReverts(bytes32 pk, bytes memory message, bytes32 r, bytes32 s) public {
        try caller.verify(pk, message, r, s) returns (bool) {}
        catch {
            fail();
        }
    }

    /// The full path (S canonical, the key on the curve): a real key from gas.vec, S reduced below L.
    function testFuzz_fullPathNeverReverts(uint8 which, bytes memory message, bytes32 r, uint256 sRaw) public {
        Vec memory v = _gasVec(which % 8);
        bytes32 s = _le(sRaw % L);
        try caller.verify(v.pk, message, r, s) returns (bool ok) {
            // r and s are random: a valid signature would be a forgery.
            assertFalse(ok && (r != bytes32(_slice32(v.sig, 0)) || keccak256(message) != keccak256(v.msg)));
        } catch {
            fail();
        }
    }

    function _slice32(bytes memory b, uint256 at) internal pure returns (bytes32 out) {
        assembly {
            out := mload(add(add(b, 0x20), at))
        }
    }

    function _gasVec(uint256 index) internal view returns (Vec memory v) {
        string[] memory lines = _lines(string.concat(DIR, "gas.vec"));
        uint256 n;
        for (uint256 i = 0; i < lines.length; i++) {
            if (!_isRecord(lines[i])) continue;
            if (n++ == index) return _parse(lines[i]);
        }
        revert("gas.vec: no such record");
    }

    // ---------------------------------------------------------------------------------------------------------------
    // Gate 7: gas. One verification by message length (gnoland-1's precommit sign bytes are 110 bytes), measured
    // around the internal call; and the library's runtime bytecode added to a contract that calls it.
    // ---------------------------------------------------------------------------------------------------------------

    function test_gas_perVerification() public view {
        string[] memory lines = _lines(string.concat(DIR, "gas.vec"));
        for (uint256 i = 0; i < lines.length; i++) {
            if (!_isRecord(lines[i])) continue;
            Vec memory v = _parse(lines[i]);
            bytes32 r = _slice32(v.sig, 0);
            bytes32 s = _slice32(v.sig, 32);
            uint256 before = gasleft();
            bool ok = Ed25519.verify(v.pk, v.msg, r, s);
            uint256 used = before - gasleft();
            assertTrue(ok, v.label);
            uint256 ext = gasleft();
            assertTrue(caller.verify(v.pk, v.msg, r, s), v.label);
            ext -= gasleft();
            console.log(
                string.concat(
                    "gas ", v.label, ": ", vm.toString(used), " (internal call); ", vm.toString(ext), " (external call)"
                )
            );
        }
    }

    function test_gas_bytecodeAdded() public view {
        uint256 withLib = type(Ed25519Caller).runtimeCode.length;
        uint256 without = type(Ed25519SizeBaseline).runtimeCode.length;
        uint256 viaContract = address(via).code.length;
        uint256 verifierSize = type(Ed25519Verifier).runtimeCode.length;
        console.log(
            string.concat(
                "runtime bytecode: caller ",
                vm.toString(withLib),
                " bytes, baseline ",
                vm.toString(without),
                " bytes: Ed25519.sol + Sha512.sol add ",
                vm.toString(withLib - without),
                " inline"
            )
        );
        console.log(
            string.concat(
                "Ed25519Verifier runtime ",
                vm.toString(verifierSize),
                " bytes; a caller through it ",
                vm.toString(viaContract),
                " bytes (adds ",
                vm.toString(viaContract - without),
                " over the baseline)"
            )
        );
        assertLt(withLib, 24576);
        assertLt(verifierSize, 24576);
    }

    /// Through the separate verifier contract: the published sets, the edges, the binding cases and the forgery probes
    /// against key decoding give Go's verdicts.
    function test_verifierContract_vectors() public view {
        string[6] memory files =
            ["rfc8032.vec", "wycheproof.vec", "speccheck.vec", "edges.vec", "binding.vec", "probes.vec"];
        for (uint256 i = 0; i < files.length; i++) {
            Tally memory t = _replay(files[i], verifier);
            _report(string.concat("via Ed25519Verifier, ", files[i]), t);
            assertGt(t.records, 0);
            assertEq(t.disagreements, 0, files[i]);
        }
    }

    /// Gas through the verifier contract from a caller holding its address: the first call of a transaction pays
    /// the cold account access (2,600), later ones the warm (100).
    function test_gas_viaVerifierContract() public view {
        string[] memory lines = _lines(string.concat(DIR, "gas.vec"));
        bool first = true;
        for (uint256 i = 0; i < lines.length; i++) {
            if (!_isRecord(lines[i])) continue;
            Vec memory v = _parse(lines[i]);
            bytes32 r = _slice32(v.sig, 0);
            bytes32 s = _slice32(v.sig, 32);
            uint256 g = gasleft();
            assertTrue(via.verify(v.pk, v.msg, r, s), v.label);
            g -= gasleft();
            console.log(
                string.concat(
                    "gas via Ed25519Verifier ", v.label, ": ", vm.toString(g), first ? " (verifier cold)" : " (warm)"
                )
            );
            first = false;
        }
    }
}

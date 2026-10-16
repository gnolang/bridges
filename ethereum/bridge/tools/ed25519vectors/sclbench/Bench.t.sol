// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
// The SCL comparison of eth/bridge/M2a-NOTES.md §2 and §5: SCL's Verify_LE against Ed25519.verify on the same keys
// and messages in the same EVM, and the key-binding hazard reproduced with SCL's own Sign. Not part of the contracts
// build (it needs SCL, which is not vendored). To run it, on the devbox: make a Foundry project with this file in
// test/, foundry.toml from this directory, get-smooth/crypto-lib at d714e9824e2e2e44be3c8fd498e0de651ddc9425 with its
// src/* in src/ and its external/ beside src/, this tree's Ed25519.sol and Sha512.sol in src/m2a/, and lib/forge-std;
// then `forge test -vv` (and `FOUNDRY_PROFILE=sclown forge test -vv` for SCL's own 1,000,000 optimizer runs).
pragma solidity 0.8.33;
import {Test, console} from "forge-std/Test.sol";
import "@solidity/lib/libSCL_EIP6565.sol";
import "@solidity/lib/libSCL_eddsaUtils.sol";
import {Ed25519} from "../src/m2a/Ed25519.sol";

contract Ours {
    function verify(bytes32 pk, bytes memory m, bytes32 r, bytes32 s) external view returns (bool) {
        return Ed25519.verify(pk, m, r, s);
    }
}

contract BenchTest is Test {
    uint256 constant SEED = 0x4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb; // RFC 8032 TEST 2
    uint256 constant SEED2 = 0xc5aa8df43f9f837bedb7442f31dcb7b166d38535076f094b85ce3a2e0b4458f7; // RFC 8032 TEST 3

    function _msg(uint256 n) internal pure returns (bytes memory m) {
        m = new bytes(n);
        for (uint256 i; i < n; i++) m[i] = bytes1(uint8(i * 7 + 3));
    }

    function test_bench() public {
        Ours ours = new Ours();
        (uint256[5] memory ext, uint256[2] memory signer) = SCL_EIP6565_UTILS.SetKey(SEED);
        uint256[3] memory lens = [uint256(0), 110, 300];
        for (uint256 i; i < 3; i++) {
            bytes memory m = _msg(lens[i]);
            (uint256 r, uint256 s) = SCL_EIP6565_UTILS.Sign(ext[4], signer, string(m));
            uint256 g = gasleft();
            bool okScl = SCL_EIP6565.Verify_LE(string(m), r, s, ext);
            uint256 gScl = g - gasleft();
            g = gasleft();
            bool okOurs = ours.verify(bytes32(ext[4]), m, bytes32(r), bytes32(s));
            uint256 gOurs = g - gasleft();
            g = gasleft();
            bool okInt = Ed25519.verify(bytes32(ext[4]), m, bytes32(r), bytes32(s));
            uint256 gInt = g - gasleft();
            assertTrue(okScl && okOurs && okInt);
            console.log(string.concat("len ", vm.toString(lens[i]), ": SCL Verify_LE ", vm.toString(gScl),
                "; ours external ", vm.toString(gOurs), ", internal ", vm.toString(gInt)));
        }
    }

    /// The research's reproduction: A2's secret signs with A's bytes hashed in (SCL's own Sign with A's compressed
    /// key and A2's signer); SCL handed A2's point beside A's bytes accepts it.
    function test_bindingHazard() public {
        (uint256[5] memory extA,) = SCL_EIP6565_UTILS.SetKey(SEED);
        (uint256[5] memory extA2, uint256[2] memory signerA2) = SCL_EIP6565_UTILS.SetKey(SEED2);
        bytes memory m = _msg(110);
        (uint256 r, uint256 s) = SCL_EIP6565_UTILS.Sign(extA[4], signerA2, string(m));
        uint256[5] memory mixed = [extA2[0], extA2[1], extA2[2], extA2[3], extA[4]];
        bool scl = SCL_EIP6565.Verify_LE(string(m), r, s, mixed);
        bool oursA = Ed25519.verify(bytes32(extA[4]), m, bytes32(r), bytes32(s));
        bool oursA2 = Ed25519.verify(bytes32(extA2[4]), m, bytes32(r), bytes32(s));
        console.log("SCL Verify_LE(A2 point, A bytes):", scl);
        console.log("Ed25519.verify under A bytes:", oursA);
        console.log("Ed25519.verify under A2 bytes:", oursA2);
        assertTrue(scl, "the hazard reproduces");
        assertFalse(oursA);
        assertFalse(oursA2);
    }
}

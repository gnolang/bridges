// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

/// tm2's simple merkle tree (tm2/pkg/crypto/merkle; RFC 6962 hashing: a leaf is `sha256(0x00 ‖ x)`, an inner node
/// `sha256(0x01 ‖ left ‖ right)`, a list split at the largest power of two below its length) and its
/// `SimpleProof`, ported from bridge/programs/gno-light-core/src/merkle.rs. sha256 is the 0x02 precompile.
library TmMerkle {
    error BadProof();

    /// tm2's maxAunts: a proof of more siblings than this is refused (gno-light-client-ism's StageInit).
    uint256 internal constant MAX_AUNTS = 100;

    function leafHash(bytes memory leaf) internal pure returns (bytes32) {
        return sha256(abi.encodePacked(bytes1(0x00), leaf));
    }

    function innerHash(bytes32 left, bytes32 right) internal pure returns (bytes32) {
        return sha256(abi.encodePacked(bytes1(0x01), left, right));
    }

    /// The largest power of two strictly below `n` (n ≥ 2).
    function splitPoint(uint256 n) internal pure returns (uint256 k) {
        k = 1;
        while (k * 2 < n) {
            k *= 2;
        }
    }

    /// `SimpleHashFromByteSlices` over items already hashed as leaves: the root of `hashes[lo .. hi]` (hi > lo).
    function rootOf(bytes32[] memory hashes, uint256 lo, uint256 hi) internal pure returns (bytes32) {
        uint256 n = hi - lo;
        if (n == 1) {
            return hashes[lo];
        }
        uint256 k = splitPoint(n);
        return innerHash(rootOf(hashes, lo, lo + k), rootOf(hashes, lo + k, hi));
    }

    /// The root of a non-empty list of leaf hashes.
    function root(bytes32[] memory hashes) internal pure returns (bytes32) {
        return rootOf(hashes, 0, hashes.length);
    }

    /// `computeHashFromAunts`: the root a proof implies. The aunts run from the leaf's sibling upward; the path is
    /// walked top-down and applied bottom-up (merkle.rs `root_from_proof`). Refuses a total of zero, an index outside
    /// it, more than `MAX_AUNTS` aunts, and aunts that are not one per level.
    function rootFromProof(bytes32 leaf, uint256 index, uint256 total, bytes32[] memory aunts)
        internal
        pure
        returns (bytes32 h)
    {
        if (total == 0 || index >= total || aunts.length > MAX_AUNTS) {
            revert BadProof();
        }
        // the directions top-down, as bits: bit d set where the path goes left at depth d
        uint256 dirs;
        uint256 depth;
        uint256 i = index;
        uint256 t = total;
        while (t > 1) {
            uint256 k = splitPoint(t);
            if (i < k) {
                dirs |= 1 << depth;
                t = k;
            } else {
                i -= k;
                t -= k;
            }
            depth++;
        }
        if (depth != aunts.length) {
            revert BadProof();
        }
        h = leaf;
        for (uint256 m = 0; m < depth; m++) {
            // the m-th aunt is the sibling at depth (depth − 1 − m)
            if (dirs & (1 << (depth - 1 - m)) != 0) {
                h = innerHash(h, aunts[m]);
            } else {
                h = innerHash(aunts[m], h);
            }
        }
    }
}

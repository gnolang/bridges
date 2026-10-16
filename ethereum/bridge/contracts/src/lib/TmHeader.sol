// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {TmAmino} from "./TmAmino.sol";
import {TmMerkle} from "./TmMerkle.sol";

/// tm2's block header (gno v1.2.0 tm2/pkg/bft/types/block.go) as its sixteen amino-encoded leaves, the Solana plan
/// §13's "header leaves" (an empty field's leaf is empty, its hash `sha256(0x00)`): Version, ChainID, Height, Time,
/// NumTxs, TotalTxs, AppVersion, LastBlockID, LastCommitHash, DataHash, ValidatorsHash, NextValidatorsHash,
/// ConsensusHash, AppHash, LastResultsHash, ProposerAddress. The header's hash is the simple merkle root of the
/// sixteen (bridge/programs/gno-light-core/src/header.rs).
///
/// The client reads six fields from their canonical leaves and nothing else: the chain id (compared whole, as the
/// leaf `0A len <chain id>`), the height (`08 zigzag`, positive), the time (`[08 uvarint(sec)] [10 uvarint(ns)]`),
/// ValidatorsHash and NextValidatorsHash (`0A 20 <32 bytes>`), LastResultsHash (empty, or `0A 20 <32 bytes>`). A
/// leaf it reads that is not in that exact form is refused (`BadHeader`). The other ten leaves are hashed and
/// nothing more: any header the client uses is one more than 2/3 of the validators' power signed, so its leaves are
/// the chain's own, and a field it never reads cannot change what it decides.
library TmHeader {
    error BadHeader();

    uint256 internal constant LEAVES = 16;
    uint256 internal constant CHAIN_ID = 1;
    uint256 internal constant HEIGHT = 2;
    uint256 internal constant TIME = 3;
    uint256 internal constant VALIDATORS_HASH = 10;
    uint256 internal constant NEXT_VALIDATORS_HASH = 11;
    uint256 internal constant LAST_RESULTS_HASH = 14;

    /// What the client reads of a header.
    struct Fields {
        bytes32 hash;
        bytes32 chainIdLeafHash; // keccak256 of the ChainID leaf, compared with the client's own
        int64 height;
        uint64 time; // the seconds as the leaf carries them (a uvarint of the value as a u64)
        bytes32 validatorsHash;
        bytes32 nextValidatorsHash;
        bytes32 lastResultsHash; // zero when the block before had no transactions (an empty leaf)
    }

    /// The ChainID leaf a chain id makes: `0A uvarint(len) <chain id>` (empty for an empty id).
    function chainIdLeaf(string memory chainId) internal pure returns (bytes memory) {
        return TmAmino.bytesField(1, bytes(chainId));
    }

    /// `Header.Hash()`: the simple merkle root of the sixteen leaves.
    function hash(bytes[] calldata leaves) internal pure returns (bytes32) {
        if (leaves.length != LEAVES) {
            revert BadHeader();
        }
        bytes32[] memory level = new bytes32[](LEAVES);
        for (uint256 i = 0; i < LEAVES; i++) {
            level[i] = sha256(abi.encodePacked(bytes1(0x00), leaves[i]));
        }
        // sixteen is a power of two: the RFC 6962 split is the plain pairwise reduction
        for (uint256 n = LEAVES; n > 1; n /= 2) {
            for (uint256 i = 0; i < n / 2; i++) {
                level[i] = TmMerkle.innerHash(level[2 * i], level[2 * i + 1]);
            }
        }
        return level[0];
    }

    /// The header's hash and the fields the client reads.
    function parse(bytes[] calldata leaves) internal pure returns (Fields memory f) {
        f.hash = hash(leaves);
        f.chainIdLeafHash = keccak256(leaves[CHAIN_ID]);
        f.height = _height(leaves[HEIGHT]);
        f.time = _time(leaves[TIME]);
        f.validatorsHash = _hash32(leaves[VALIDATORS_HASH], false);
        f.nextValidatorsHash = _hash32(leaves[NEXT_VALIDATORS_HASH], false);
        f.lastResultsHash = _hash32(leaves[LAST_RESULTS_HASH], true);
    }

    /// `08 <zigzag varint>`, positive.
    function _height(bytes calldata leaf) private pure returns (int64 height) {
        bytes memory b = leaf;
        if (b.length < 2 || TmAmino.byteAt(b, 0) != 0x08) {
            revert BadHeader();
        }
        (uint64 u, uint256 next) = TmAmino.readUvarint(b, 1, b.length);
        if (next != b.length) {
            revert BadHeader();
        }
        height = TmAmino.unzigzag(u);
        if (height <= 0) {
            revert BadHeader();
        }
    }

    /// `[08 uvarint(sec)] [10 uvarint(ns)]`: the seconds as a u64 (zero when omitted).
    function _time(bytes calldata leaf) private pure returns (uint64 secs) {
        bytes memory b = leaf;
        uint256 pos = 0;
        if (pos < b.length && TmAmino.byteAt(b, pos) == 0x08) {
            (secs, pos) = TmAmino.readUvarint(b, pos + 1, b.length);
        }
        if (pos < b.length && TmAmino.byteAt(b, pos) == 0x10) {
            (, pos) = TmAmino.readUvarint(b, pos + 1, b.length);
        }
        if (pos != b.length) {
            revert BadHeader();
        }
    }

    /// `0A 20 <32 bytes>`; empty only where `optional` (then zero).
    function _hash32(bytes calldata leaf, bool optional) private pure returns (bytes32 h) {
        if (leaf.length == 0 && optional) {
            return bytes32(0);
        }
        if (leaf.length != 34 || leaf[0] != 0x0a || leaf[1] != 0x20) {
            revert BadHeader();
        }
        h = bytes32(leaf[2:34]);
    }
}

// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {TmAmino} from "./TmAmino.sol";

/// A precommit's sign bytes: `amino.MarshalSized(CanonicalVote)` (tm2 vote.go, canonical.go; ported from
/// bridge/programs/gno-light-core/src/vote.rs), the Solana plan §13:
///
/// `uvarint(len)` · `08 02` (Type precommit) · `11` + 8-byte LE height · [`19` + 8-byte LE round, omitted when 0]
/// · `22 len { 0A 20 <blockhash> 12 len { 08 uvarint(total) 12 20 <partshash> } }` · `2A len { [08 uvarint(sec)]
/// [10 uvarint(ns)] }` (omitted when both are zero) · `32 len <chain id>`.
///
/// Built per precommit from the commit's BlockID and round and the precommit's own timestamp: everything but the
/// time is the same for every precommit of a commit, so the client builds the bytes before and after the time once
/// (`Template`) and only the time per precommit.
library TmVote {
    uint8 internal constant PRECOMMIT_TYPE = 2;
    uint8 internal constant PREVOTE_TYPE = 1;

    /// The parts of a commit's sign bytes that every precommit shares.
    struct Template {
        bytes pre; // type, height, round, BlockID
        bytes post; // chain id
    }

    /// `CanonicalBlockID` (the sign bytes' form: the part set's Total a plain uvarint).
    function canonicalBlockId(bytes32 hash, uint32 partsTotal, bytes32 partsHash) internal pure returns (bytes memory) {
        bytes memory parts =
            abi.encodePacked(TmAmino.uvarintField(1, partsTotal), TmAmino.bytesField(2, abi.encodePacked(partsHash)));
        return abi.encodePacked(TmAmino.bytesField(1, abi.encodePacked(hash)), TmAmino.bytesField(2, parts));
    }

    /// The shared parts of the sign bytes of a vote of `typ` at (height, round) for the BlockID, on the chain.
    function template(
        uint8 typ,
        bytes memory chainId,
        int64 height,
        int64 round,
        bytes32 blockHash,
        uint32 partsTotal,
        bytes32 partsHash
    ) internal pure returns (Template memory t) {
        t.pre = abi.encodePacked(
            TmAmino.uvarintField(1, typ),
            TmAmino.fixed64Field(2, uint64(height)),
            TmAmino.fixed64Field(3, uint64(round)),
            TmAmino.bytesField(4, canonicalBlockId(blockHash, partsTotal, partsHash))
        );
        t.post = TmAmino.bytesField(6, chainId);
    }

    /// One vote's sign bytes from the template and its own timestamp.
    function signBytes(Template memory t, int64 secs, int32 nanos) internal pure returns (bytes memory) {
        bytes memory time = TmAmino.bytesField(5, TmAmino.timeBare(secs, nanos));
        uint256 len = t.pre.length + time.length + t.post.length;
        return abi.encodePacked(TmAmino.uvarint(uint64(len)), t.pre, time, t.post);
    }

    /// A precommit's sign bytes in one call (the tests and the tools).
    function precommitSignBytes(
        bytes memory chainId,
        int64 height,
        int64 round,
        bytes32 blockHash,
        uint32 partsTotal,
        bytes32 partsHash,
        int64 secs,
        int32 nanos
    ) internal pure returns (bytes memory) {
        return signBytes(
            template(PRECOMMIT_TYPE, chainId, height, round, blockHash, partsTotal, partsHash), secs, nanos
        );
    }
}

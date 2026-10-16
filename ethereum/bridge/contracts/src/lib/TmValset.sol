// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {IGnoLightClient} from "../interfaces/IGnoLightClient.sol";
import {TmAmino} from "./TmAmino.sol";
import {TmMerkle} from "./TmMerkle.sol";

/// Validators and their set (tm2 validator.go, validator_set.go; ported from
/// bridge/programs/gno-light-core/src/valset.rs): the leaf `0A37 { 0A11"/tm.PubKeyEd25519" 1222 { 0A20<pk> } } 10
/// <zigzag power>` (`Validator.Bytes()`), the set hash (a simple merkle root over the leaves in set order), the
/// order (by address, `sha256(pk)[:20]`, strictly ascending) and the arithmetic of power. Every key is an ed25519
/// key: a set holding another kind of key hashes to something no stored hash is, so it is refused by its hash.
library TmValset {
    error BadValidatorSet();

    /// The client accepts up to 255 validators (the Solana stage's count byte; PLAN.md §9's bound).
    uint256 internal constant MAX_VALIDATORS = 255;
    /// tm2's MaxTotalVotingPower: i64::MAX / 8.
    int64 internal constant MAX_TOTAL_POWER = type(int64).max / 8;

    /// The leaf's bytes before the key: `0A 37 0A 11 "/tm.PubKeyEd25519" 12 22 0A 20`.
    bytes internal constant LEAF_PREFIX = hex"0a370a112f746d2e5075624b65794564323535313912220a20";

    /// `Validator.Bytes()`; the power field is omitted at zero, as amino omits a zero int64.
    function leaf(bytes32 pubkey, int64 power) internal pure returns (bytes memory) {
        return abi.encodePacked(LEAF_PREFIX, pubkey, TmAmino.zigzagField(2, power));
    }

    /// `crypto.PubKey.Address()` for ed25519: the first twenty bytes of sha256 of the key.
    function addressOf(bytes32 pubkey) internal pure returns (bytes20) {
        return bytes20(sha256(abi.encodePacked(pubkey)));
    }

    /// `ValidatorSet.Hash()` of a non-empty set, and its total power (refused when it leaves int64).
    function hash(IGnoLightClient.Validator[] calldata set) internal pure returns (bytes32 root, int64 total) {
        uint256 n = set.length;
        if (n == 0 || n > MAX_VALIDATORS) {
            revert BadValidatorSet();
        }
        bytes32[] memory hashes = new bytes32[](n);
        for (uint256 i = 0; i < n; i++) {
            hashes[i] = TmMerkle.leafHash(leaf(set[i].pubkey, set[i].power));
            total += set[i].power;
        }
        root = TmMerkle.root(hashes);
    }

    /// What a set the client stores must be (valset.rs `check`): 1–255 validators, each with a positive power, the
    /// total within tm2's bound, addresses strictly ascending (so unique).
    function check(IGnoLightClient.Validator[] calldata set) internal pure {
        uint256 n = set.length;
        if (n == 0 || n > MAX_VALIDATORS) {
            revert BadValidatorSet();
        }
        int256 total;
        bytes20 prev;
        for (uint256 i = 0; i < n; i++) {
            if (set[i].power <= 0) {
                revert BadValidatorSet();
            }
            bytes20 a = addressOf(set[i].pubkey);
            if (i > 0 && a <= prev) {
                revert BadValidatorSet();
            }
            prev = a;
            total += set[i].power;
        }
        if (total > MAX_TOTAL_POWER) {
            revert BadValidatorSet();
        }
    }
}

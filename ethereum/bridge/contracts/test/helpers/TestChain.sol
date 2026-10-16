// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Vm} from "forge-std/Vm.sol";

import {IGnoLightClient} from "../../src/interfaces/IGnoLightClient.sol";
import {TmAmino} from "../../src/lib/TmAmino.sol";
import {TmMerkle} from "../../src/lib/TmMerkle.sol";
import {TmValset} from "../../src/lib/TmValset.sol";
import {TmVote} from "../../src/lib/TmVote.sol";

/// A gno.land test chain built in Solidity, for the tests that are not about the vectors (HlAdmin, the gate,
/// staging, gas): headers as their sixteen amino leaves, validator sets of any size (real ed25519 keys from a seed,
/// sorted by address as tm2 keeps them), commits signed with real ed25519 (Foundry's signEd25519, RFC 8032 as Go's
/// crypto/ed25519; the client checks them with lane E-M2a's verifier), results leaves carrying Dispatch events of
/// real Hyperlane messages, SimpleProofs and the client's metadata. Every encoding is the one tools/evmvectors writes
/// with Go's gnolight; TmLibs.t.sol checks the two agree.
library TestChain {
    Vm private constant vm = Vm(address(uint160(uint256(keccak256("hevm cheat code")))));
    /// The key ring: an account whose storage slot `pubkey` holds that key's secret, written and read by cheatcodes
    /// only (vm.store, vm.load), so building a commit makes no call that would take a pending vm.prank.
    address internal constant RING = address(uint160(uint256(keccak256("gno test chain: the key ring"))));

    error UnknownKey(bytes32 pubkey);

    struct Header {
        string chainId;
        int64 height;
        int64 timeSecs;
        int32 timeNanos;
        int64 numTxs;
        int64 totalTxs;
        bytes32 lastBlockHash; // zero: no LastBlockID
        bytes32 validatorsHash;
        bytes32 nextValidatorsHash;
        bytes32 lastResultsHash; // zero: none
        uint32 partsTotal; // the header's own BlockID part set, which its commit carries
        bytes32 partsHash;
    }

    // ---------------------------------------------------------------------------------------------- headers --

    function leaves(Header memory h) internal pure returns (bytes[] memory l) {
        l = new bytes[](16);
        l[0] = TmAmino.bytesField(1, "v1.0.0-rc.0");
        l[1] = TmAmino.bytesField(1, bytes(h.chainId));
        l[2] = TmAmino.zigzagField(1, h.height);
        l[3] = TmAmino.timeBare(h.timeSecs, h.timeNanos);
        l[4] = TmAmino.zigzagField(1, h.numTxs);
        l[5] = TmAmino.zigzagField(1, h.totalTxs);
        l[6] = "";
        if (h.lastBlockHash != bytes32(0)) {
            bytes memory parts = abi.encodePacked(
                TmAmino.zigzagField(1, 1),
                TmAmino.bytesField(2, abi.encodePacked(sha256(abi.encodePacked(h.lastBlockHash))))
            );
            l[7] = abi.encodePacked(
                TmAmino.bytesField(1, abi.encodePacked(h.lastBlockHash)), TmAmino.bytesField(2, parts)
            );
        }
        l[8] = _hashField(sha256(abi.encodePacked("last commit", h.height)));
        if (h.numTxs > 0) {
            l[9] = _hashField(sha256(abi.encodePacked("data", h.height)));
        }
        l[10] = _hashField(h.validatorsHash);
        l[11] = _hashField(h.nextValidatorsHash);
        l[12] = _hashField(sha256("consensus"));
        l[13] = _hashField(sha256(abi.encodePacked("app", h.height)));
        if (h.lastResultsHash != bytes32(0)) {
            l[14] = _hashField(h.lastResultsHash);
        }
        l[15] = TmAmino.bytesField(1, "g1proposer000000000000000000000000000000");
    }

    function _hashField(bytes32 v) private pure returns (bytes memory) {
        return TmAmino.bytesField(1, abi.encodePacked(v));
    }

    /// `Header.Hash()`: the simple merkle root of the sixteen leaves.
    function hash(bytes[] memory l) internal pure returns (bytes32) {
        bytes32[] memory h = new bytes32[](l.length);
        for (uint256 i = 0; i < l.length; i++) {
            h[i] = TmMerkle.leafHash(l[i]);
        }
        return TmMerkle.root(h);
    }

    // ----------------------------------------------------------------------------------------------- the sets --

    function validators(uint256 seed, uint256 n, int64 power) internal returns (IGnoLightClient.Validator[] memory) {
        int64[] memory p = new int64[](n);
        for (uint256 i = 0; i < n; i++) {
            p[i] = power;
        }
        return validatorsWith(seed, p);
    }

    /// The ed25519 key of secret keccak256("val", seed, i) (an RFC 8032 seed), its secret kept in the ring.
    function key(uint256 seed, uint256 i) internal returns (bytes32 pubkey) {
        bytes32 secret = keccak256(abi.encode("val", seed, i));
        pubkey = vm.publicKeyEd25519(secret);
        if (RING.code.length == 0) {
            vm.etch(RING, hex"00"); // a code byte, so the account is never empty (EIP-161) between calls
        }
        vm.store(RING, pubkey, secret);
    }

    /// Keys `key(seed, i)` with the given powers, sorted by address (sha256(key)[:20]) as tm2 keeps a set.
    function validatorsWith(uint256 seed, int64[] memory powers)
        internal
        returns (IGnoLightClient.Validator[] memory v)
    {
        uint256 n = powers.length;
        v = new IGnoLightClient.Validator[](n);
        bytes20[] memory a = new bytes20[](n);
        for (uint256 i = 0; i < n; i++) {
            v[i] = IGnoLightClient.Validator(key(seed, i), powers[i]);
            a[i] = TmValset.addressOf(v[i].pubkey);
        }
        for (uint256 i = 1; i < n; i++) {
            IGnoLightClient.Validator memory x = v[i];
            bytes20 xa = a[i];
            uint256 j = i;
            while (j > 0 && a[j - 1] > xa) {
                v[j] = v[j - 1];
                a[j] = a[j - 1];
                j--;
            }
            v[j] = x;
            a[j] = xa;
        }
    }

    function setHash(IGnoLightClient.Validator[] memory v) internal pure returns (bytes32) {
        bytes32[] memory h = new bytes32[](v.length);
        for (uint256 i = 0; i < v.length; i++) {
            h[i] = TmMerkle.leafHash(TmValset.leaf(v[i].pubkey, v[i].power));
        }
        return TmMerkle.root(h);
    }

    function totalPower(IGnoLightClient.Validator[] memory v) internal pure returns (int64 t) {
        for (uint256 i = 0; i < v.length; i++) {
            t += v[i].power;
        }
    }

    /// The first slots in set order whose power crosses 2/3.
    function quorum(IGnoLightClient.Validator[] memory v) internal pure returns (uint256[] memory slots) {
        int256 total = totalPower(v);
        int256 tallied;
        uint256 n;
        while (tallied * 3 <= total * 2) {
            tallied += v[n].power;
            n++;
        }
        slots = range(0, n);
    }

    function range(uint256 from, uint256 to) internal pure returns (uint256[] memory r) {
        r = new uint256[](to - from);
        for (uint256 i = from; i < to; i++) {
            r[i - from] = i;
        }
    }

    // ---------------------------------------------------------------------------------------------- commits --

    /// The precommit time a slot signs at (its own, as on a real chain: seconds after the header, varied nanos, a
    /// zero nanos for every fifth slot so the field is omitted).
    function precommitTime(Header memory h, uint256 i) internal pure returns (int64 secs, int32 nanos) {
        secs = h.timeSecs + 1 + int64(uint64(i % 3));
        nanos = i % 5 == 4 ? int32(0) : int32(uint32((i * 7919 + uint256(uint64(h.height))) % 1_000_000_000));
    }

    function signBytes(Header memory h, bytes32 headerHash, int64 round, uint256 slot)
        internal
        pure
        returns (bytes memory)
    {
        (int64 secs, int32 nanos) = precommitTime(h, slot);
        return TmVote.precommitSignBytes(
            bytes(h.chainId), h.height, round, headerHash, h.partsTotal, h.partsHash, secs, nanos
        );
    }

    /// The ed25519 signature (r ‖ s) of `message` by the ring's key `pubkey`.
    function sign(bytes32 pubkey, bytes memory message_) internal view returns (bytes32 r, bytes32 s) {
        bytes32 secret = vm.load(RING, pubkey);
        if (secret == bytes32(0)) {
            revert UnknownKey(pubkey);
        }
        bytes memory sig = vm.signEd25519("", message_, secret);
        assembly ("memory-safe") {
            r := mload(add(sig, 32))
            s := mload(add(sig, 64))
        }
    }

    /// The commit of `slots` for the header, each precommit at its own time, signed by its slot's key.
    function commit(
        Header memory h,
        bytes32 headerHash,
        IGnoLightClient.Validator[] memory set,
        uint256[] memory slots,
        int64 round
    ) internal view returns (IGnoLightClient.Commit memory c) {
        c.partsTotal = h.partsTotal;
        c.partsHash = h.partsHash;
        c.round = round;
        c.precommits = new IGnoLightClient.Precommit[](slots.length);
        for (uint256 k = 0; k < slots.length; k++) {
            uint256 i = slots[k];
            (int64 secs, int32 nanos) = precommitTime(h, i);
            (bytes32 r, bytes32 s) = sign(set[i].pubkey, signBytes(h, headerHash, round, i));
            c.precommits[k] = IGnoLightClient.Precommit(uint16(i), secs, nanos, r, s);
        }
    }

    // ---------------------------------------------------------------------------------------------- results --

    /// A Hyperlane message (version 3).
    function message(
        uint32 nonce,
        uint32 origin,
        bytes32 sender,
        uint32 destination,
        bytes32 recipient,
        bytes memory body
    ) internal pure returns (bytes memory) {
        return abi.encodePacked(uint8(3), nonce, origin, sender, destination, recipient, body);
    }

    /// Hyperlane's token message: recipient 32 ‖ amount 32.
    function tokenBody(address to, uint256 amount) internal pure returns (bytes memory) {
        return abi.encodePacked(bytes32(uint256(uint160(to))), amount);
    }

    function any(bytes memory url, bytes memory value) internal pure returns (bytes memory) {
        return abi.encodePacked(TmAmino.bytesField(1, url), TmAmino.bytesField(2, value));
    }

    /// A `chain.Event`'s value: type, attributes, pkg_path.
    function eventValue(bytes memory typ, bytes[] memory keys, bytes[] memory values, bytes memory pkgPath)
        internal
        pure
        returns (bytes memory out)
    {
        out = TmAmino.bytesField(1, typ);
        for (uint256 i = 0; i < keys.length; i++) {
            out = abi.encodePacked(
                out,
                TmAmino.bytesField(
                    2, abi.encodePacked(TmAmino.bytesField(1, keys[i]), TmAmino.bytesField(2, values[i]))
                )
            );
        }
        out = abi.encodePacked(out, TmAmino.bytesField(3, pkgPath));
    }

    /// The seven attributes of a message's Dispatch event, in order, as hlmailbox writes them.
    function dispatchAttributes(bytes memory m) internal pure returns (bytes[] memory keys, bytes[] memory values) {
        keys = new bytes[](7);
        values = new bytes[](7);
        (keys[0], values[0]) = ("id", hexLower(keccak256(m)));
        (keys[1], values[1]) = ("nonce", decimal(_u32(m, 1)));
        (keys[2], values[2]) = ("origin", decimal(_u32(m, 5)));
        (keys[3], values[3]) = ("destination", decimal(_u32(m, 41)));
        (keys[4], values[4]) = ("sender", hexLower(_b32(m, 9)));
        (keys[5], values[5]) = ("recipient", hexLower(_b32(m, 45)));
        (keys[6], values[6]) = ("hash", hexLower(sha256(m)));
    }

    /// The `/tm.Event` Any of a message's Dispatch event under the mailbox's path.
    function dispatchEvent(bytes memory m, string memory pkgPath) internal pure returns (bytes memory) {
        (bytes[] memory keys, bytes[] memory values) = dispatchAttributes(m);
        return any("/tm.Event", eventValue("Dispatch", keys, values, bytes(pkgPath)));
    }

    /// An ABCIResult leaf: an optional error Any, the data, the event Anys.
    function resultLeaf(bytes memory errorAny, bytes memory data, bytes[] memory events)
        internal
        pure
        returns (bytes memory out)
    {
        if (errorAny.length != 0) {
            out = TmAmino.bytesField(1, errorAny);
        }
        out = abi.encodePacked(out, TmAmino.bytesField(2, data));
        for (uint256 i = 0; i < events.length; i++) {
            out = abi.encodePacked(out, TmAmino.bytesField(3, events[i]));
        }
    }

    /// The leaf of one transaction dispatching `messages` (each its own Dispatch event).
    function dispatchLeaf(bytes[] memory messages, string memory pkgPath) internal pure returns (bytes memory) {
        bytes[] memory events = new bytes[](messages.length);
        for (uint256 i = 0; i < messages.length; i++) {
            events[i] = dispatchEvent(messages[i], pkgPath);
        }
        return resultLeaf("", "", events);
    }

    function leafHashes(bytes[] memory l) internal pure returns (bytes32[] memory h) {
        h = new bytes32[](l.length);
        for (uint256 i = 0; i < l.length; i++) {
            h[i] = TmMerkle.leafHash(l[i]);
        }
    }

    function root(bytes[] memory l) internal pure returns (bytes32) {
        return TmMerkle.root(leafHashes(l));
    }

    /// The SimpleProof of item `index` (gnolight.Proof): the aunts from the leaf's sibling upward.
    function proof(bytes[] memory l, uint256 index) internal pure returns (bytes32[] memory aunts) {
        bytes32[] memory h = leafHashes(l);
        bytes32[] memory topDown = new bytes32[](64);
        uint256 d;
        uint256 lo = 0;
        uint256 hi = h.length;
        while (hi - lo > 1) {
            uint256 k = TmMerkle.splitPoint(hi - lo);
            if (index < lo + k) {
                topDown[d++] = TmMerkle.rootOf(h, lo + k, hi);
                hi = lo + k;
            } else {
                topDown[d++] = TmMerkle.rootOf(h, lo, lo + k);
                lo += k;
            }
        }
        aunts = new bytes32[](d);
        for (uint256 i = 0; i < d; i++) {
            aunts[i] = topDown[d - 1 - i];
        }
    }

    /// The client's metadata: abi.encode(bytes32 headerHash, uint32 total, uint32 index, bytes32[] aunts, bytes leaf).
    function metadata(bytes32 headerHash, bytes[] memory l, uint256 index) internal pure returns (bytes memory) {
        return abi.encode(headerHash, uint32(l.length), uint32(index), proof(l, index), l[index]);
    }

    // -------------------------------------------------------------------------------------------- the bytes --

    function hexLower(bytes32 v) internal pure returns (bytes memory out) {
        bytes16 digits = "0123456789abcdef";
        out = new bytes(64);
        for (uint256 i = 0; i < 32; i++) {
            out[2 * i] = digits[uint8(v[i]) >> 4];
            out[2 * i + 1] = digits[uint8(v[i]) & 15];
        }
    }

    function decimal(uint256 v) internal pure returns (bytes memory out) {
        if (v == 0) {
            return "0";
        }
        uint256 n;
        for (uint256 x = v; x != 0; x /= 10) {
            n++;
        }
        out = new bytes(n);
        for (uint256 i = n; i > 0; i--) {
            out[i - 1] = bytes1(uint8(48 + v % 10));
            v /= 10;
        }
    }

    function _u32(bytes memory m, uint256 at) private pure returns (uint32 v) {
        for (uint256 i = 0; i < 4; i++) {
            v = (v << 8) | uint8(m[at + i]);
        }
    }

    function _b32(bytes memory m, uint256 at) private pure returns (bytes32 v) {
        assembly ("memory-safe") {
            v := mload(add(add(m, 32), at))
        }
    }
}

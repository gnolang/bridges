// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Test} from "forge-std/Test.sol";

import {IGnoLightClient} from "../../src/interfaces/IGnoLightClient.sol";

/// Reads tools/evmvectors' JSON (test/vectors): headers with their leaves, commits, sets and results proofs; Go's
/// verdict on every signature.
abstract contract Vectors is Test {
    struct VHeader {
        int64 height;
        bytes32 hash;
        bytes[] leaves;
        uint64 time;
        bytes32 validatorsHash;
        bytes32 nextValidatorsHash;
        bytes lastResultsHash;
        bool announcing;
        IGnoLightClient.Commit commit;
        bytes[] signBytes;
        bool[] valid;
        string set; // synthetic: the signing set's name
        string nextSet; // synthetic: the announced set's name
        IGnoLightClient.Validator[] validators; // fixtures: the signing set inline
        bytes32 setHash;
    }

    struct VProof {
        uint32 index;
        uint32 total;
        bytes32[] aunts;
        bytes leaf;
    }

    function _read(string memory name) internal view returns (string memory) {
        return vm.readFile(string.concat(vm.projectRoot(), "/test/vectors/", name));
    }

    function _count(string memory json, string memory path) internal view returns (uint256 n) {
        while (vm.keyExistsJson(json, string.concat(path, "[", vm.toString(n), "]"))) {
            n++;
        }
    }

    function _at(string memory path, uint256 i) internal pure returns (string memory) {
        return string.concat(path, "[", vm.toString(i), "]");
    }

    function _vheader(string memory json, string memory p) internal pure returns (VHeader memory h) {
        h.height = int64(uint64(vm.parseJsonUint(json, string.concat(p, ".height"))));
        h.hash = vm.parseJsonBytes32(json, string.concat(p, ".hash"));
        h.leaves = vm.parseJsonBytesArray(json, string.concat(p, ".leaves"));
        h.time = uint64(vm.parseJsonUint(json, string.concat(p, ".time")));
        h.validatorsHash = vm.parseJsonBytes32(json, string.concat(p, ".validatorsHash"));
        h.nextValidatorsHash = vm.parseJsonBytes32(json, string.concat(p, ".nextValidatorsHash"));
        h.lastResultsHash = vm.parseJsonBytes(json, string.concat(p, ".lastResultsHash"));
        h.announcing = vm.parseJsonBool(json, string.concat(p, ".announcing"));
        h.commit.partsTotal = uint32(vm.parseJsonUint(json, string.concat(p, ".partsTotal")));
        h.commit.partsHash = vm.parseJsonBytes32(json, string.concat(p, ".partsHash"));
        h.commit.round = int64(uint64(vm.parseJsonUint(json, string.concat(p, ".round"))));
        (h.commit.precommits, h.signBytes, h.valid) = _precommits(json, string.concat(p, ".precommits"));
        h.set = vm.parseJsonString(json, string.concat(p, ".set"));
        h.nextSet = vm.parseJsonString(json, string.concat(p, ".nextSet"));
        h.validators = _validators(json, string.concat(p, ".pubkeys"), string.concat(p, ".powers"));
        bytes memory sh = vm.parseJsonBytes(json, string.concat(p, ".setHash"));
        if (sh.length == 32) {
            h.setHash = bytes32(sh);
        }
    }

    function _precommits(string memory json, string memory p)
        internal
        pure
        returns (IGnoLightClient.Precommit[] memory pcs, bytes[] memory signBytes, bool[] memory valid)
    {
        uint256[] memory index = vm.parseJsonUintArray(json, string.concat(p, ".index"));
        uint256[] memory secs = vm.parseJsonUintArray(json, string.concat(p, ".secs"));
        uint256[] memory nanos = vm.parseJsonUintArray(json, string.concat(p, ".nanos"));
        bytes32[] memory r = vm.parseJsonBytes32Array(json, string.concat(p, ".r"));
        bytes32[] memory s = vm.parseJsonBytes32Array(json, string.concat(p, ".s"));
        signBytes = vm.parseJsonBytesArray(json, string.concat(p, ".signBytes"));
        valid = vm.parseJsonBoolArray(json, string.concat(p, ".valid"));
        pcs = new IGnoLightClient.Precommit[](index.length);
        for (uint256 i = 0; i < index.length; i++) {
            pcs[i] = IGnoLightClient.Precommit(
                uint16(index[i]), int64(uint64(secs[i])), int32(uint32(nanos[i])), r[i], s[i]
            );
        }
    }

    function _validators(string memory json, string memory pubkeys, string memory powers)
        internal
        pure
        returns (IGnoLightClient.Validator[] memory v)
    {
        bytes32[] memory pk = vm.parseJsonBytes32Array(json, pubkeys);
        uint256[] memory pw = vm.parseJsonUintArray(json, powers);
        v = new IGnoLightClient.Validator[](pk.length);
        for (uint256 i = 0; i < pk.length; i++) {
            v[i] = IGnoLightClient.Validator(pk[i], int64(uint64(pw[i])));
        }
    }

    /// A synthetic set by name (".sets[i]").
    function _set(string memory json, string memory name) internal view returns (IGnoLightClient.Validator[] memory) {
        uint256 n = _count(json, ".sets");
        for (uint256 i = 0; i < n; i++) {
            string memory p = _at(".sets", i);
            if (keccak256(bytes(vm.parseJsonString(json, string.concat(p, ".name")))) == keccak256(bytes(name))) {
                return _validators(json, string.concat(p, ".pubkeys"), string.concat(p, ".powers"));
            }
        }
        revert(string.concat("no set ", name));
    }

    function _proofs(string memory json, string memory p) internal view returns (VProof[] memory out) {
        uint256 n = _count(json, string.concat(p, ".resultsProofs"));
        out = new VProof[](n);
        for (uint256 i = 0; i < n; i++) {
            string memory q = _at(string.concat(p, ".resultsProofs"), i);
            out[i].index = uint32(vm.parseJsonUint(json, string.concat(q, ".index")));
            out[i].total = uint32(vm.parseJsonUint(json, string.concat(q, ".total")));
            out[i].aunts = vm.parseJsonBytes32Array(json, string.concat(q, ".aunts"));
            out[i].leaf = vm.parseJsonBytes(json, string.concat(q, ".leaf"));
        }
    }
}

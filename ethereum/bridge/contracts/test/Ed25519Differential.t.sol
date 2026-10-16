// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {console} from "forge-std/Test.sol";
import {Ed25519VectorBase} from "./Ed25519Vectors.t.sol";

/// Gate 4 (eth/bridge/M2a-NOTES.md): the differential set against Go's crypto/ed25519, generated before the run by
///   go run ./tools/ed25519vectors diff -out contracts/test/vectors/ed25519/diff        (from eth/bridge)
/// into chunk files (not committed: about 56 MB). Without them these tests are skipped, visibly; the gate is a run in
/// which all sixteen lanes and the manifest test pass. Each lane replays every sixteenth chunk, each chunk in its own
/// call frame (fresh memory).
contract Ed25519DifferentialTest is Ed25519VectorBase {
    string internal constant DIFF = "test/vectors/ed25519/diff/";
    uint256 internal constant LANES = 16;

    function _manifest() internal returns (bool present, uint256 chunks, uint256 total, uint256 chunkSize) {
        string memory path = string.concat(DIFF, "manifest.txt");
        if (!vm.exists(path)) {
            vm.skip(true, "no differential set: run tools/ed25519vectors diff first");
            return (false, 0, 0, 0);
        }
        string[] memory lines = _lines(path);
        return (true, vm.parseUint(lines[0]), vm.parseUint(lines[1]), vm.parseUint(lines[2]));
    }

    /// Replays one chunk; returns its record count, Go's accepts and the disagreements.
    function replayChunk(uint256 c) external view returns (uint256 n, uint256 accepts, uint256 bad) {
        string memory name = vm.toString(c);
        while (bytes(name).length < 4) name = string.concat("0", name);
        Tally memory t = _replay(string.concat("diff/chunk-", name, ".vec"));
        return (t.records, t.goAccepts, t.disagreements);
    }

    function _lane(uint256 lane) internal {
        (bool present, uint256 chunks, uint256 total, uint256 chunkSize) = _manifest();
        if (!present) return;
        uint256 records;
        uint256 accepts;
        for (uint256 c = lane; c < chunks; c += LANES) {
            (uint256 n, uint256 a, uint256 bad) = this.replayChunk(c);
            uint256 expected = c + 1 < chunks ? chunkSize : total - c * chunkSize;
            assertEq(n, expected, "a chunk holds the records the manifest says");
            assertEq(bad, 0, "agreement with Go");
            records += n;
            accepts += a;
        }
        console.log(
            string.concat(
                "lane ",
                vm.toString(lane),
                ": ",
                vm.toString(records),
                " records, Go accepts ",
                vm.toString(accepts),
                ", disagreements 0"
            )
        );
    }

    function test_differential_manifest() public {
        (bool present, uint256 chunks, uint256 total, uint256 chunkSize) = _manifest();
        if (!present) return;
        assertGe(total, 100_000, "at least 100,000 differential cases");
        assertGt(total, (chunks - 1) * chunkSize);
        assertLe(total, chunks * chunkSize);
        console.log(
            string.concat("differential set: ", vm.toString(total), " records in ", vm.toString(chunks), " chunks")
        );
    }

    function test_differential_lane00() public {
        _lane(0);
    }

    function test_differential_lane01() public {
        _lane(1);
    }

    function test_differential_lane02() public {
        _lane(2);
    }

    function test_differential_lane03() public {
        _lane(3);
    }

    function test_differential_lane04() public {
        _lane(4);
    }

    function test_differential_lane05() public {
        _lane(5);
    }

    function test_differential_lane06() public {
        _lane(6);
    }

    function test_differential_lane07() public {
        _lane(7);
    }

    function test_differential_lane08() public {
        _lane(8);
    }

    function test_differential_lane09() public {
        _lane(9);
    }

    function test_differential_lane10() public {
        _lane(10);
    }

    function test_differential_lane11() public {
        _lane(11);
    }

    function test_differential_lane12() public {
        _lane(12);
    }

    function test_differential_lane13() public {
        _lane(13);
    }

    function test_differential_lane14() public {
        _lane(14);
    }

    function test_differential_lane15() public {
        _lane(15);
    }
}

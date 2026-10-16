// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {IGnoLightClient} from "../src/interfaces/IGnoLightClient.sol";
import {TmAmino} from "../src/lib/TmAmino.sol";
import {TmHeader} from "../src/lib/TmHeader.sol";
import {TmMerkle} from "../src/lib/TmMerkle.sol";
import {TmResults} from "../src/lib/TmResults.sol";
import {TmValset} from "../src/lib/TmValset.sol";
import {TmVote} from "../src/lib/TmVote.sol";
import {TestChain} from "./helpers/TestChain.sol";
import {Vectors} from "./helpers/Vectors.sol";

/// The libraries behind external functions (calldata where the client reads calldata).
contract LibHarness {
    function headerHash(bytes[] calldata l) external pure returns (bytes32) {
        return TmHeader.hash(l);
    }

    function headerParse(bytes[] calldata l) external pure returns (TmHeader.Fields memory) {
        return TmHeader.parse(l);
    }

    function setHash(IGnoLightClient.Validator[] calldata v) external pure returns (bytes32 h, int64 total) {
        return TmValset.hash(v);
    }

    function setCheck(IGnoLightClient.Validator[] calldata v) external pure {
        TmValset.check(v);
    }

    function proves(bytes memory leaf, bytes32 pkg, TmResults.Message memory m) external pure returns (bool) {
        return TmResults.proves(leaf, pkg, m);
    }

    function dispatches(bytes memory leaf, bytes32 pkg) external pure returns (TmResults.Dispatch[] memory) {
        return TmResults.dispatches(leaf, pkg);
    }

    function rootFromProof(bytes32 leaf, uint256 index, uint256 total, bytes32[] memory aunts)
        external
        pure
        returns (bytes32)
    {
        return TmMerkle.rootFromProof(leaf, index, total, aunts);
    }

    function readUvarint(bytes memory b) external pure returns (uint64 v, uint256 next) {
        return TmAmino.readUvarint(b, 0, b.length);
    }

    function readKey(bytes memory b) external pure returns (uint256 field, uint256 typ3, uint256 next) {
        return TmAmino.readKey(b, 0, b.length);
    }
}

/// The tm2 libraries against Go's gnolight (the vectors) and the Solana plan §13.
contract TmLibsTest is Vectors {
    LibHarness internal lib;
    string internal fixtures;
    string internal synthetic;
    bytes32 internal constant PKG = keccak256("gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox");
    string internal constant PATH = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox";

    function setUp() public {
        lib = new LibHarness();
        fixtures = _read("fixtures.json");
        synthetic = _read("synthetic.json");
    }

    // ------------------------------------------------------------------------------------------ sign bytes --

    /// The Solana plan §13's 110-byte example: validator 0's precommit at 701,862, round 0, one part.
    function test_signBytes_theAppendixExample() public view {
        bytes memory want =
            hex"6D080211A6B50A000000000022480A20B0D79144E283B51F02881A930A56E0D98DB717702D116DFF92271FA5A75A872D1224080112205AE9C4415816870AB1B14D7024D797C7E3DC3098CC3DDB01F11E5F309360112F2A0B08A9C9A8D60610DFC2B9133209676E6F6C616E642D31";
        bytes memory got = TmVote.precommitSignBytes(
            "gnoland-1",
            701862,
            0,
            0xB0D79144E283B51F02881A930A56E0D98DB717702D116DFF92271FA5A75A872D,
            1,
            0x5AE9C4415816870AB1B14D7024D797C7E3DC3098CC3DDB01F11E5F309360112F,
            1791632553,
            40788319
        );
        assertEq(got.length, 110);
        assertEq(got, want);
        assertEq(got, vm.parseJsonBytes(fixtures, ".example110"), "Go's gnolight builds the same bytes");
    }

    /// Every precommit of every gnoland-1 fixture: our sign bytes are Go's (which crypto/ed25519 verified).
    function test_fixtures_signBytesAreGos() public view {
        uint256 n = _count(fixtures, ".headers");
        assertEq(n, 17);
        uint256 checked;
        for (uint256 i = 0; i < n; i++) {
            VHeader memory h = _vheader(fixtures, _at(".headers", i));
            for (uint256 k = 0; k < h.commit.precommits.length; k++) {
                IGnoLightClient.Precommit memory p = h.commit.precommits[k];
                bytes memory sb = TmVote.precommitSignBytes(
                    "gnoland-1",
                    h.height,
                    h.commit.round,
                    h.hash,
                    h.commit.partsTotal,
                    h.commit.partsHash,
                    p.timeSecs,
                    p.timeNanos
                );
                assertEq(sb, h.signBytes[k]);
                assertTrue(h.valid[k], "Go verified it");
                checked++;
            }
        }
        assertEq(checked, 86);
    }

    // ---------------------------------------------------------------------------------------------- headers --

    /// Every gnoland-1 fixture header: the sixteen leaves hash to the chain's hash, and the fields the client reads
    /// are the header's.
    function test_fixtures_headerHashesAndFields() public view {
        uint256 n = _count(fixtures, ".headers");
        bytes32 chainLeaf = keccak256(hex"0a09676e6f6c616e642d31");
        for (uint256 i = 0; i < n; i++) {
            VHeader memory h = _vheader(fixtures, _at(".headers", i));
            assertEq(lib.headerHash(h.leaves), h.hash);
            TmHeader.Fields memory f = lib.headerParse(h.leaves);
            assertEq(f.hash, h.hash);
            assertEq(f.chainIdLeafHash, chainLeaf);
            assertEq(f.height, h.height);
            assertEq(f.time, h.time);
            assertEq(f.validatorsHash, h.validatorsHash);
            assertEq(f.nextValidatorsHash, h.nextValidatorsHash);
            assertEq(f.lastResultsHash, h.lastResultsHash.length == 0 ? bytes32(0) : bytes32(h.lastResultsHash));
            assertEq(h.announcing, f.validatorsHash != f.nextValidatorsHash);
            assertEq(keccak256(TmHeader.chainIdLeaf("gnoland-1")), chainLeaf);
        }
    }

    function test_header_refusals() public {
        VHeader memory h = _vheader(fixtures, _at(".headers", 0));
        bytes[] memory l = h.leaves;
        bytes[] memory short = new bytes[](15);
        for (uint256 i = 0; i < 15; i++) {
            short[i] = l[i];
        }
        vm.expectRevert(TmHeader.BadHeader.selector);
        lib.headerHash(short);
        // a ValidatorsHash leaf one byte long
        bytes[] memory bad = _copy(l);
        bad[10] = abi.encodePacked(l[10], hex"00");
        vm.expectRevert(TmHeader.BadHeader.selector);
        lib.headerParse(bad);
        // an empty NextValidatorsHash
        bad = _copy(l);
        bad[11] = "";
        vm.expectRevert(TmHeader.BadHeader.selector);
        lib.headerParse(bad);
        // a height of zero (an empty leaf) and a negative one
        bad = _copy(l);
        bad[2] = "";
        vm.expectRevert(TmHeader.BadHeader.selector);
        lib.headerParse(bad);
        bad[2] = TmAmino.zigzagField(1, -5);
        vm.expectRevert(TmHeader.BadHeader.selector);
        lib.headerParse(bad);
        // a height leaf with a byte after its varint
        bad[2] = abi.encodePacked(TmAmino.zigzagField(1, 7), hex"00");
        vm.expectRevert(TmHeader.BadHeader.selector);
        lib.headerParse(bad);
        // a time leaf with an unknown field
        bad = _copy(l);
        bad[3] = abi.encodePacked(l[3], hex"1801");
        vm.expectRevert(TmHeader.BadHeader.selector);
        lib.headerParse(bad);
        // a LastResultsHash that is neither empty nor 0A 20 <32>
        bad = _copy(l);
        bad[14] = hex"0a0100";
        vm.expectRevert(TmHeader.BadHeader.selector);
        lib.headerParse(bad);
    }

    function _copy(bytes[] memory l) internal pure returns (bytes[] memory c) {
        c = new bytes[](l.length);
        for (uint256 i = 0; i < l.length; i++) {
            c[i] = l[i];
        }
    }

    // -------------------------------------------------------------------------------------------------- sets --

    /// Every fixture's set hashes to its header's ValidatorsHash and is a set the client would store.
    function test_fixtures_setHashes() public view {
        uint256 n = _count(fixtures, ".headers");
        for (uint256 i = 0; i < n; i++) {
            VHeader memory h = _vheader(fixtures, _at(".headers", i));
            (bytes32 sh, int64 total) = lib.setHash(h.validators);
            assertEq(sh, h.setHash);
            assertEq(sh, h.validatorsHash);
            assertGt(total, 0);
            lib.setCheck(h.validators);
        }
    }

    /// The synthetic sets (4, 6, 20, 60 validators) hash as Go hashes them, and TestChain's Solidity sets as the
    /// library does.
    function test_synthetic_setHashes() public view {
        string[4] memory names = ["A", "B", "C", "D"];
        uint256[4] memory sizes = [uint256(4), 6, 20, 60];
        for (uint256 i = 0; i < 4; i++) {
            IGnoLightClient.Validator[] memory v = _set(synthetic, names[i]);
            assertEq(v.length, sizes[i]);
            (bytes32 sh,) = lib.setHash(v);
            assertEq(sh, vm.parseJsonBytes32(synthetic, string.concat(_at(".sets", i), ".hash")));
            assertEq(TestChain.setHash(v), sh);
            lib.setCheck(v);
        }
    }

    function test_validatorLeaf_isTheAppendixForm() public pure {
        bytes memory l = TmValset.leaf(
            bytes32(uint256(0xaa) * 0x0101010101010101010101010101010101010101010101010101010101010101), 60
        );
        assertEq(l.length, 59);
        assertEq(bytes2(l[0]) | (bytes2(l[1]) >> 8), bytes2(hex"0a37"));
        assertEq(uint8(l[57]), 0x10);
        assertEq(uint8(l[58]), 0x78);
    }

    function test_set_refusals() public {
        IGnoLightClient.Validator[] memory v = TestChain.validators(3, 4, 60);
        lib.setCheck(v);
        // unsorted
        IGnoLightClient.Validator[] memory w = TestChain.validators(3, 4, 60);
        (w[0], w[1]) = (w[1], w[0]);
        vm.expectRevert(TmValset.BadValidatorSet.selector);
        lib.setCheck(w);
        // a duplicate
        w = TestChain.validators(3, 4, 60);
        w[1] = w[0];
        vm.expectRevert(TmValset.BadValidatorSet.selector);
        lib.setCheck(w);
        // no power
        w = TestChain.validators(3, 4, 60);
        w[2].power = 0;
        vm.expectRevert(TmValset.BadValidatorSet.selector);
        lib.setCheck(w);
        // empty
        vm.expectRevert(TmValset.BadValidatorSet.selector);
        lib.setCheck(new IGnoLightClient.Validator[](0));
        // 256 validators
        w = TestChain.validators(4, 256, 1);
        vm.expectRevert(TmValset.BadValidatorSet.selector);
        lib.setCheck(w);
        // 255 are allowed
        lib.setCheck(TestChain.validators(4, 255, 1));
        // a total past tm2's bound
        w = TestChain.validators(3, 2, type(int64).max / 16 + 1);
        vm.expectRevert(TmValset.BadValidatorSet.selector);
        lib.setCheck(w);
    }

    // ----------------------------------------------------------------------------------------------- results --

    /// The real gnoland-1 results leaves (a grc20 Approval, a bank transfer, …): each SimpleProof leads to the next
    /// header's LastResultsHash, and the reader walks every leaf to the end and finds no Dispatch of ours.
    function test_fixtures_resultsProofsAndLeaves() public view {
        uint256 n = _count(fixtures, ".headers");
        uint256 checked;
        for (uint256 i = 0; i + 1 < n; i++) {
            VProof[] memory ps = _proofs(fixtures, _at(".headers", i));
            if (ps.length == 0) {
                continue;
            }
            VHeader memory next = _vheader(fixtures, _at(".headers", i + 1));
            for (uint256 k = 0; k < ps.length; k++) {
                bytes32 root = lib.rootFromProof(TmMerkle.leafHash(ps[k].leaf), ps[k].index, ps[k].total, ps[k].aunts);
                assertEq(root, bytes32(next.lastResultsHash));
                try lib.dispatches(ps[k].leaf, PKG) {
                    revert("a real leaf carries no Dispatch of our mailbox");
                } catch (bytes memory err) {
                    assertEq(bytes4(err), TmResults.NoDispatch.selector);
                }
                checked++;
            }
        }
        assertEq(checked, 6);
    }

    /// The synthetic leaves: every message's Dispatch reads back as Go wrote it, and TestChain's leaf of one
    /// dispatch is Go's byte for byte.
    function test_synthetic_dispatchesReadBack() public view {
        uint256 n = _count(synthetic, ".messages");
        assertEq(n, 7);
        for (uint256 i = 0; i < n; i++) {
            string memory p = _at(".messages", i);
            bytes memory m = vm.parseJsonBytes(synthetic, string.concat(p, ".bytes"));
            bytes memory leaf = vm.parseJsonBytes(synthetic, string.concat(p, ".leaf"));
            assertTrue(lib.proves(leaf, PKG, _message(m)));
            TmResults.Dispatch[] memory ds = lib.dispatches(leaf, PKG);
            bool seen;
            for (uint256 k = 0; k < ds.length; k++) {
                if (ds[k].id == keccak256(m)) {
                    seen = true;
                    assertEq(ds[k].hash, sha256(m));
                }
            }
            assertTrue(seen);
            if (keccak256(bytes(vm.parseJsonString(synthetic, string.concat(p, ".name")))) == keccak256("bubble-carol"))
            {
                bytes[] memory one = new bytes[](1);
                one[0] = m;
                assertEq(TestChain.dispatchLeaf(one, PATH), leaf, "TestChain encodes as gnolight does");
            }
        }
    }

    function _message(bytes memory m) internal pure returns (TmResults.Message memory r) {
        r.id = keccak256(m);
        r.hash = sha256(m);
        r.nonce = uint32(bytes4(_slice(m, 1, 4)));
        r.origin = uint32(bytes4(_slice(m, 5, 4)));
        r.sender = bytes32(_slice(m, 9, 32));
        r.destination = uint32(bytes4(_slice(m, 41, 4)));
        r.recipient = bytes32(_slice(m, 45, 32));
    }

    function _slice(bytes memory b, uint256 at, uint256 len) internal pure returns (bytes memory out) {
        out = new bytes(len);
        for (uint256 i = 0; i < len; i++) {
            out[i] = b[at + i];
        }
    }

    function _msg(uint32 nonce) internal pure returns (bytes memory) {
        return TestChain.message(nonce, 1198419796, bytes32(uint256(7)), 31337, bytes32(uint256(8)), "body");
    }

    function _attrsLeaf(bytes[] memory keys, bytes[] memory values, bytes memory typ, string memory path)
        internal
        pure
        returns (bytes memory)
    {
        bytes[] memory events = new bytes[](1);
        events[0] = TestChain.any("/tm.Event", TestChain.eventValue(typ, keys, values, bytes(path)));
        return TestChain.resultLeaf("", "", events);
    }

    /// (The arguments are built before vm.expectRevert: sha256 is a precompile call, which the cheat would take for
    /// the call it expects to revert.)
    function _expectProofRevert(bytes memory leaf, bytes memory m, bytes4 err) internal {
        TmResults.Message memory mm = _message(m);
        vm.expectRevert(err);
        lib.proves(leaf, PKG, mm);
    }

    function _one(bytes memory m) internal pure returns (bytes[] memory one) {
        one = new bytes[](1);
        one[0] = m;
    }

    /// A failed transaction's leaf proves nothing, whatever it carries; another mailbox's Dispatch, or another type
    /// under our path, is no Dispatch of ours; a leaf proves only its own messages.
    function test_results_refusals_failedForeignOther() public {
        bytes memory m = _msg(5);
        bytes memory good = TestChain.dispatchLeaf(_one(m), PATH);
        assertTrue(lib.proves(good, PKG, _message(m)));
        assertFalse(lib.proves(good, PKG, _message(_msg(6))), "another message is not proven by this leaf");
        bytes[] memory events = new bytes[](1);
        events[0] = TestChain.dispatchEvent(m, PATH);
        _expectProofRevert(
            TestChain.resultLeaf(TestChain.any("/std.UnauthorizedError", ""), "", events),
            m,
            TmResults.NoDispatch.selector
        );
        _expectProofRevert(
            TestChain.dispatchLeaf(_one(m), "gno.land/r/someone/hlmailbox"), m, TmResults.NoDispatch.selector
        );
        _expectProofRevert(TestChain.resultLeaf("", "data", new bytes[](0)), m, TmResults.NoDispatch.selector);
        (bytes[] memory keys, bytes[] memory values) = TestChain.dispatchAttributes(m);
        _expectProofRevert(_attrsLeaf(keys, values, "Process", PATH), m, TmResults.NoDispatch.selector);
    }

    /// Uppercase hex, attributes out of order or missing, decimals with a leading zero, past u32 or empty, a hex of
    /// 63 digits, and a malformed Dispatch of ours beside the right one (every Dispatch of ours must read, as in
    /// Rust).
    function test_results_refusals_attributes() public {
        bytes memory m = _msg(5);
        (bytes[] memory keys, bytes[] memory values) = TestChain.dispatchAttributes(m);
        bytes memory upper = values[0];
        for (uint256 i = 0; i < upper.length; i++) {
            if (upper[i] >= "a" && upper[i] <= "f") {
                upper[i] = bytes1(uint8(upper[i]) - 32);
            }
        }
        _expectProofRevert(_attrsLeaf(keys, values, "Dispatch", PATH), m, TmResults.BadDispatch.selector);
        (keys, values) = TestChain.dispatchAttributes(m);
        (keys[1], keys[2]) = (keys[2], keys[1]);
        (values[1], values[2]) = (values[2], values[1]);
        _expectProofRevert(_attrsLeaf(keys, values, "Dispatch", PATH), m, TmResults.BadDispatch.selector);
        (keys, values) = TestChain.dispatchAttributes(m);
        bytes[] memory k6 = new bytes[](6);
        bytes[] memory v6 = new bytes[](6);
        for (uint256 i = 0; i < 6; i++) {
            (k6[i], v6[i]) = (keys[i], values[i]);
        }
        _expectProofRevert(_attrsLeaf(k6, v6, "Dispatch", PATH), m, TmResults.BadDispatch.selector);
        values[1] = "05";
        _expectProofRevert(_attrsLeaf(keys, values, "Dispatch", PATH), m, TmResults.BadDispatch.selector);
        values[1] = "4294967296";
        _expectProofRevert(_attrsLeaf(keys, values, "Dispatch", PATH), m, TmResults.BadDispatch.selector);
        values[1] = "";
        _expectProofRevert(_attrsLeaf(keys, values, "Dispatch", PATH), m, TmResults.BadDispatch.selector);
        (keys, values) = TestChain.dispatchAttributes(m);
        values[6] = _slice(values[6], 0, 63);
        _expectProofRevert(_attrsLeaf(keys, values, "Dispatch", PATH), m, TmResults.BadDispatch.selector);
        (keys, values) = TestChain.dispatchAttributes(m);
        values[3] = "x";
        bytes[] memory two = new bytes[](2);
        two[0] = TestChain.dispatchEvent(m, PATH);
        two[1] = TestChain.any("/tm.Event", TestChain.eventValue("Dispatch", keys, values, bytes(PATH)));
        _expectProofRevert(TestChain.resultLeaf("", "", two), m, TmResults.BadDispatch.selector);
    }

    /// Unknown fields at every level, a truncated length, an Any without its URL or with a third field, a varint
    /// field in an event, an attribute with a third field.
    function test_results_refusals_encoding() public {
        bytes memory m = _msg(5);
        bytes memory good = TestChain.dispatchLeaf(_one(m), PATH);
        _expectProofRevert(abi.encodePacked(good, TmAmino.bytesField(4, "x")), m, TmResults.BadLeaf.selector);
        _expectProofRevert(abi.encodePacked(hex"1001", good), m, TmResults.BadLeaf.selector);
        _expectProofRevert(_slice(good, 0, good.length - 1), m, TmAmino.Encoding.selector);
        bytes[] memory two = new bytes[](2);
        two[0] = TestChain.dispatchEvent(m, PATH);
        two[1] = TmAmino.bytesField(2, "x");
        _expectProofRevert(TestChain.resultLeaf("", "", two), m, TmAmino.Encoding.selector);
        two[1] = abi.encodePacked(TestChain.any("/other.Event", "x"), TmAmino.bytesField(3, "x"));
        _expectProofRevert(TestChain.resultLeaf("", "", two), m, TmAmino.Encoding.selector);
        two[1] = TestChain.any("/tm.Event", abi.encodePacked(TmAmino.bytesField(1, "T"), hex"2001"));
        _expectProofRevert(TestChain.resultLeaf("", "", two), m, TmAmino.Encoding.selector);
        two[1] = TestChain.any("/tm.Event", abi.encodePacked(TmAmino.bytesField(2, TmAmino.bytesField(3, "k"))));
        _expectProofRevert(TestChain.resultLeaf("", "", two), m, TmAmino.Encoding.selector);
    }

    /// What the reader does not refuse: an event of another type URL is never read (garbage passes), and the last
    /// type and pkg_path of an event win, as in Rust.
    function test_results_readings() public view {
        bytes memory m = _msg(5);
        bytes[] memory two = new bytes[](2);
        two[0] = TestChain.dispatchEvent(m, PATH);
        two[1] = TestChain.any("/bank.TransferEvent", hex"ffffffff");
        assertTrue(lib.proves(TestChain.resultLeaf("", "", two), PKG, _message(m)));
        (bytes[] memory keys, bytes[] memory values) = TestChain.dispatchAttributes(m);
        two[0] = TestChain.any("/bank.TransferEvent", "");
        two[1] = TestChain.any(
            "/tm.Event",
            abi.encodePacked(
                TmAmino.bytesField(1, "Other"),
                TestChain.eventValue("Dispatch", keys, values, "x"),
                TmAmino.bytesField(3, bytes(PATH))
            )
        );
        assertTrue(lib.proves(TestChain.resultLeaf("", "", two), PKG, _message(m)));
    }

    // ------------------------------------------------------------------------------------------------- amino --

    function test_amino_varints() public {
        assertEq(TmAmino.uvarint(300), hex"ac02");
        assertEq(TmAmino.uvarint(0), hex"00");
        assertEq(TmAmino.uvarint(type(uint64).max), hex"ffffffffffffffffff01");
        assertEq(TmAmino.zigzag(-1), 1);
        assertEq(TmAmino.zigzag(60), 120);
        assertEq(TmAmino.unzigzag(1), -1);
        assertEq(TmAmino.unzigzag(TmAmino.zigzag(type(int64).min)), type(int64).min);
        assertEq(TmAmino.fixed64Field(2, 0x0ab5a6), hex"11a6b50a0000000000");
        assertEq(TmAmino.timeBare(0, 0).length, 0);
        (uint64 v, uint256 next) = lib.readUvarint(hex"ffffffffffffffffff01");
        assertEq(v, type(uint64).max);
        assertEq(next, 10);
        // a ten-byte varint's bits past 64 are dropped (Rust and gnolight read it so)
        (v,) = lib.readUvarint(hex"ffffffffffffffffff7f");
        assertEq(v, type(uint64).max);
        // eleven bytes, or none left, are refused
        vm.expectRevert(TmAmino.Encoding.selector);
        lib.readUvarint(hex"ffffffffffffffffffff01");
        vm.expectRevert(TmAmino.Encoding.selector);
        lib.readUvarint(hex"80");
        // a key of field 0
        vm.expectRevert(TmAmino.Encoding.selector);
        lib.readKey(hex"02");
        (uint256 field, uint256 typ3,) = lib.readKey(hex"32");
        assertEq(field, 6);
        assertEq(typ3, 2);
    }

    // ------------------------------------------------------------------------------------------------ merkle --

    function test_merkle_proofsRoundTrip() public {
        for (uint256 n = 1; n <= 20; n++) {
            bytes[] memory items = new bytes[](n);
            for (uint256 i = 0; i < n; i++) {
                items[i] = new bytes(i + 1);
                for (uint256 j = 0; j <= i; j++) {
                    items[i][j] = bytes1(uint8(i));
                }
            }
            bytes32 root = TestChain.root(items);
            for (uint256 i = 0; i < n; i++) {
                bytes32[] memory aunts = TestChain.proof(items, i);
                assertEq(lib.rootFromProof(TmMerkle.leafHash(items[i]), i, n, aunts), root);
                if (n > 1) {
                    // a shifted index either walks another depth (refused) or another path (another root)
                    try lib.rootFromProof(TmMerkle.leafHash(items[i]), (i + 1) % n, n, aunts) returns (bytes32 r) {
                        assertTrue(r != root);
                    } catch (bytes memory err) {
                        assertEq(bytes4(err), TmMerkle.BadProof.selector);
                    }
                }
                bytes32[] memory more = new bytes32[](aunts.length + 1);
                for (uint256 k = 0; k < aunts.length; k++) {
                    more[k] = aunts[k];
                }
                bytes32 lh = TmMerkle.leafHash(items[i]);
                vm.expectRevert(TmMerkle.BadProof.selector);
                lib.rootFromProof(lh, i, n, more);
            }
        }
        assertEq(TmMerkle.splitPoint(2), 1);
        assertEq(TmMerkle.splitPoint(3), 2);
        assertEq(TmMerkle.splitPoint(5), 4);
        assertEq(TmMerkle.splitPoint(17), 16);
        vm.expectRevert(TmMerkle.BadProof.selector);
        lib.rootFromProof(bytes32(0), 0, 0, new bytes32[](0));
        vm.expectRevert(TmMerkle.BadProof.selector);
        lib.rootFromProof(bytes32(0), 3, 3, new bytes32[](2));
        vm.expectRevert(TmMerkle.BadProof.selector);
        lib.rootFromProof(bytes32(0), 0, 1, new bytes32[](101));
    }
}

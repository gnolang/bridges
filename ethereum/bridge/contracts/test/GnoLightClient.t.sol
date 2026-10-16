// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {ProxyAdmin} from "@openzeppelin/contracts/proxy/transparent/ProxyAdmin.sol";
import {TransparentUpgradeableProxy} from "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol";

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {Ed25519Verifier, IEd25519Verifier} from "../src/Ed25519Verifier.sol";
import {GnoLightClient} from "../src/GnoLightClient.sol";
import {IGnoLightClient} from "../src/interfaces/IGnoLightClient.sol";
import {TmHeader} from "../src/lib/TmHeader.sol";
import {TmResults} from "../src/lib/TmResults.sol";
import {TmValset} from "../src/lib/TmValset.sol";
import {TestChain} from "./helpers/TestChain.sol";
import {Vectors} from "./helpers/Vectors.sol";

/// The client on Go's vectors: gnoland-1's own blocks through its three validator-set changes, the synthetic chain
/// through sets of 4, 6, 20 and 60 validators, every refusal of the Rust client's suite that applies, staging,
/// proveHeader and proofs by an accepted header's entry. Every signature goes through lane E-M2a's `Ed25519Verifier`
/// (Go's crypto/ed25519.Verify in Solidity), which agrees with Go's verdict on every triple the vectors carry.
contract GnoLightClientVectorsTest is Vectors {
    string internal fixtures;
    string internal synthetic;
    Ed25519Verifier internal verifier;
    GnoLightClient internal client;
    address internal admin = makeAddr("admin");
    IGnoLightClient.Validator[] internal noSet;

    uint32 internal constant GNOLAND1 = 1198419788;
    uint32 internal constant GNOTEST = 1198419796;
    string internal constant PATH = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox";

    function setUp() public {
        fixtures = _read("fixtures.json");
        synthetic = _read("synthetic.json");
        verifier = new Ed25519Verifier();
        vm.warp(1_791_000_000);
        vm.roll(1_000);
    }

    /// A client trusting `h` (at its own time: the client takes a trust root within an hour of it).
    function _deployClient(
        string memory chainId,
        uint32 origin,
        VHeader memory h,
        IGnoLightClient.Validator[] memory set
    ) internal {
        bytes32 pkg = keccak256(bytes(PATH));
        address impl = address(new GnoLightClient(pkg, verifier));
        ProxyAdmin pa = new ProxyAdmin();
        vm.warp(h.time);
        client = GnoLightClient(
            address(
                new TransparentUpgradeableProxy(
                    impl,
                    address(pa),
                    abi.encodeCall(GnoLightClient.initialize, (chainId, origin, h.leaves, set, admin))
                )
            )
        );
        assertEq(client.trustedHash(), h.hash);
        assertEq(client.trustedHeight(), h.height);
        assertEq(client.trustedTime(), h.time);
    }

    function _fx(uint256 i) internal view returns (VHeader memory) {
        return _vheader(fixtures, _at(".headers", i));
    }

    function _syn(uint256 i) internal view returns (VHeader memory) {
        return _vheader(synthetic, _at(".headers", i));
    }

    function _synSet(VHeader memory h) internal view returns (IGnoLightClient.Validator[] memory) {
        return _set(synthetic, h.set);
    }

    function _update(VHeader memory h, IGnoLightClient.Validator[] memory set, IGnoLightClient.Validator[] memory next)
        internal
        returns (bool)
    {
        return client.update(h.leaves, h.commit, set, next);
    }

    // ------------------------------------------------------------------------------------- the real chain --

    /// gnoland-1's own blocks from 487,881 through 741,103: every header accepted in turn, the three set changes
    /// followed through their announcing headers (a fifth validator at 487,883, its raise at 495,707, a sixth at
    /// 720,333), the announcing header required (487,883 straight from 487,881 is refused), the quarantine set at
    /// each announcement, the epoch up at each change, the record counting every header.
    function test_realChain_replaysItsThreeSetChanges() public {
        uint256 n = _count(fixtures, ".headers");
        VHeader memory first = _fx(0);
        assertEq(first.height, 487881);
        _deployClient("gnoland-1", GNOLAND1, first, first.validators);
        assertEq(client.records(), 1);

        // the announcing header must be presented: 487,883 straight from 487,881 is refused
        VHeader memory h3 = _fx(2);
        assertEq(h3.height, 487883);
        vm.expectRevert(GnoLightClient.ValidatorsHash.selector);
        _update(h3, h3.validators, noSet);

        uint256 announcements;
        uint64 epoch;
        for (uint256 i = 1; i < n; i++) {
            VHeader memory h = _fx(i);
            VHeader memory prev = _fx(i - 1);
            IGnoLightClient.Validator[] memory next = h.announcing ? _fx(i + 1).validators : noSet;
            vm.warp(block.timestamp + 60);
            vm.roll(block.number + 5);
            assertTrue(_update(h, h.validators, next), "finished in one call");
            if (prev.validatorsHash != h.validatorsHash) {
                epoch++;
            }
            assertEq(client.trustedHeight(), h.height);
            assertEq(client.trustedHash(), h.hash);
            assertEq(client.trustedTime(), h.time);
            assertEq(client.setHash(), h.validatorsHash, "the set of the trusted height");
            assertEq(client.epoch(), epoch);
            IGnoLightClient.Entry memory e = client.entry(h.hash);
            assertEq(e.height, h.height);
            assertEq(e.epoch, epoch);
            if (h.announcing) {
                announcements++;
                assertEq(client.nextSetHash(), h.nextValidatorsHash);
                assertEq(
                    client.quarantineEnd(),
                    block.timestamp + 7 days / CLOCK_FACTOR,
                    "the quarantine starts at the announcement"
                );
                IGnoLightClient.SetEntry memory se = client.history(client.historyLength() - 1);
                assertEq(se.fromHeight, h.height + 1);
                assertEq(se.setHash, h.nextValidatorsHash);
                assertEq(se.quarantineEnd, block.timestamp + 7 days / CLOCK_FACTOR);
            } else {
                assertEq(client.nextSetHash(), bytes32(0));
            }
        }
        assertEq(announcements, 3);
        assertEq(epoch, 3);
        assertEq(client.records(), n, "the trusted header and every update");
        assertEq(client.historyLength(), 4);
        assertEq(client.trustedHeight(), 741103);
        (bytes32 six,) = this.hashSet(_fx(n - 1).validators);
        assertEq(client.setHash(), six);
    }

    function hashSet(IGnoLightClient.Validator[] calldata v) external pure returns (bytes32, int64) {
        return TmValset.hash(v);
    }

    /// A real results leaf of gnoland-1 (487,881's transaction) proven by its accepted header 487,882 reaches the
    /// reader and is refused there (no Dispatch of our mailbox); after the set changed, the same proof stops at the
    /// entry (an old epoch).
    function test_realChain_resultsProofsGoThroughTheEntry() public {
        VHeader memory h0 = _fx(0);
        VHeader memory h1 = _fx(1);
        _deployClient("gnoland-1", GNOLAND1, h0, h0.validators);
        assertTrue(_update(h1, h1.validators, _fx(2).validators));
        VProof[] memory ps = _proofs(fixtures, _at(".headers", 0));
        assertEq(ps.length, 1);
        bytes memory meta = abi.encode(h1.hash, ps[0].total, ps[0].index, ps[0].aunts, ps[0].leaf);
        bytes memory m = TestChain.message(1, GNOLAND1, bytes32(uint256(1)), 1, bytes32(uint256(2)), "x");
        vm.expectRevert(TmResults.NoDispatch.selector);
        client.verify(meta, m);
        // a message of another origin stops before
        bytes memory other = TestChain.message(1, GNOTEST, bytes32(uint256(1)), 1, bytes32(uint256(2)), "x");
        vm.expectRevert(GnoLightClient.OriginMismatch.selector);
        client.verify(meta, other);
        // 487,883 is the fifth validator's first header: the epoch rises and 487,882's entry proves nothing more
        VHeader memory h2 = _fx(2);
        assertTrue(_update(h2, h2.validators, noSet));
        assertEq(client.epoch(), 1);
        vm.expectRevert(GnoLightClient.UnknownHeader.selector);
        client.verify(meta, m);
    }

    // -------------------------------------------------------------------------------- the synthetic chain --

    function _synWorld() internal {
        VHeader memory h100 = _syn(0);
        _deployClient("gno-test-1", GNOTEST, h100, _synSet(h100));
    }

    function _synNext(VHeader memory h) internal view returns (IGnoLightClient.Validator[] memory) {
        return h.announcing ? _set(synthetic, h.nextSet) : noSet;
    }

    function _synUpdate(uint256 i) internal returns (VHeader memory h) {
        h = _syn(i);
        vm.warp(block.timestamp + 7);
        vm.roll(block.number + 1);
        assertTrue(_update(h, _synSet(h), _synNext(h)), "the update finished");
    }

    function _message(string memory name) internal view returns (bytes memory m, bytes memory meta) {
        uint256 n = _count(synthetic, ".messages");
        for (uint256 i = 0; i < n; i++) {
            string memory p = _at(".messages", i);
            if (keccak256(bytes(vm.parseJsonString(synthetic, string.concat(p, ".name")))) == keccak256(bytes(name))) {
                return (
                    vm.parseJsonBytes(synthetic, string.concat(p, ".bytes")),
                    vm.parseJsonBytes(synthetic, string.concat(p, ".metadata"))
                );
            }
        }
        revert(string.concat("no message ", name));
    }

    /// The test chain through four sets: 4 validators (100–103, a block of two parts at 102), 6 (104–107, a commit
    /// of round 2 at 107), 20 (108) and 60 (109–111); three announcements with their quarantines, the epoch rising
    /// at each set's first header, and every message proven while its header's set is current.
    function test_synthetic_followsFourSetsAndProvesTheirMessages() public {
        _synWorld();
        _synUpdate(1);
        VHeader memory h102 = _synUpdate(2);
        assertEq(h102.commit.partsTotal, 2);
        (bytes memory m1, bytes memory meta1) = _message("gnot-alice");
        assertTrue(client.verify(meta1, m1));
        (bytes memory m3, bytes memory meta3) = _message("decision-kind12");
        assertTrue(client.verify(meta3, m3));
        // the leaf proves each of its four dispatches, and no other message
        (bytes memory m2,) = _message("bubble-bob");
        assertTrue(client.verify(meta1, m2));
        (bytes memory m5,) = _message("gnot-alice-2");
        vm.expectRevert(GnoLightClient.DispatchMismatch.selector);
        client.verify(meta1, m5);

        VHeader memory h103 = _synUpdate(3);
        assertTrue(h103.announcing);
        uint64 q1 = client.quarantineEnd();
        assertEq(q1, block.timestamp + 7 days / CLOCK_FACTOR);
        assertEq(client.epoch(), 0);
        _synUpdate(4); // B's first header
        assertEq(client.epoch(), 1);
        assertEq(client.nextSetHash(), bytes32(0));
        // 102 belongs to A: its entry proves nothing now
        (bytes memory m4, bytes memory meta4) = _message("gnot-carol-old-epoch");
        vm.expectRevert(GnoLightClient.UnknownHeader.selector);
        client.verify(meta4, m4);

        _synUpdate(5);
        _synUpdate(6);
        (, bytes memory meta5) = _message("gnot-alice-2");
        assertTrue(client.verify(meta5, m5));
        VHeader memory h107 = _synUpdate(7); // round 2, announces C
        assertEq(h107.commit.round, 2);
        uint64 q2 = client.quarantineEnd();
        assertEq(q2, block.timestamp + 7 days / CLOCK_FACTOR);
        assertGt(q2, q1);
        _synUpdate(8); // C's first (20 validators), announces D
        assertEq(client.epoch(), 2);
        _synUpdate(9); // D's first (60 validators)
        assertEq(client.epoch(), 3);
        _synUpdate(10);
        (bytes memory m7, bytes memory meta7) = _message("bubble-carol");
        assertTrue(client.verify(meta7, m7));
        _synUpdate(11);
        assertEq(client.trustedHeight(), 111);
        assertEq(client.records(), 12);
        assertEq(client.historyLength(), 4);
        IGnoLightClient.SetEntry memory last = client.history(3);
        assertEq(last.fromHeight, 109);
        assertEq(last.setHash, _syn(9).validatorsHash);
    }

    /// The record: a counter and keccak256(previous ‖ hash ‖ height u64 ‖ block.number u64) over every header
    /// accepted or proved, the init's first; the events carry it.
    function test_synthetic_theRecordIsTheRunningHash() public {
        _synWorld();
        bytes32 running = keccak256(abi.encodePacked(bytes32(0), _syn(0).hash, uint64(100), uint64(block.number)));
        assertEq(client.runningHash(), running);
        for (uint256 i = 1; i <= 3; i++) {
            VHeader memory h = _syn(i);
            vm.roll(block.number + 1);
            running = keccak256(abi.encodePacked(running, h.hash, uint64(int64(h.height)), uint64(block.number)));
            vm.expectEmit(true, true, false, true, address(client));
            emit IGnoLightClient.HeaderRecorded(h.hash, h.height, 0, uint64(i + 1), running, false);
            assertTrue(_update(h, _synSet(h), _synNext(h)));
        }
        assertEq(client.runningHash(), running);
        assertEq(client.records(), 4);
    }

    // ------------------------------------------------------------------------------------------ refusals --

    /// A header whose ValidatorsHash is right, signed in A's slots by other keys (Go refuses each signature under
    /// A's keys): refused; and a nil precommit or one for another block, real signatures over other bytes: refused.
    function test_refusals_signaturesByOtherKeysOrForOtherBlocks() public {
        _synWorld();
        VHeader memory forged = _vheader(synthetic, ".forged");
        IGnoLightClient.Validator[] memory a = _synSet(forged);
        for (uint256 i = 0; i < forged.valid.length; i++) {
            assertFalse(forged.valid[i]);
        }
        vm.expectRevert(GnoLightClient.SignatureMismatch.selector);
        _update(forged, a, noSet);

        VHeader memory h101 = _syn(1);
        (IGnoLightClient.Precommit[] memory other,, bool[] memory valid) = _precommits(synthetic, ".notForTheBlock");
        assertEq(other.length, 2);
        for (uint256 k = 0; k < 2; k++) {
            assertFalse(valid[k]);
            IGnoLightClient.Commit memory c = _copyCommit(h101.commit);
            assertEq(c.precommits[0].index, 0);
            c.precommits[0] = other[k]; // slot 0's nil (k = 0) or other-block (k = 1) precommit
            vm.expectRevert(GnoLightClient.SignatureMismatch.selector);
            client.update(h101.leaves, c, a, noSet);
        }
        // the honest header lands
        assertTrue(_update(h101, a, noSet));
    }

    /// Height, set, chain id and next-set refusals.
    function test_refusals_headerSetChainAndNextSet() public {
        _synWorld();
        VHeader memory h101 = _syn(1);
        IGnoLightClient.Validator[] memory a = _synSet(h101);
        IGnoLightClient.Validator[] memory b = _set(synthetic, "B");
        // a header signed by a set the client never followed to
        VHeader memory h104 = _syn(4);
        vm.expectRevert(GnoLightClient.ValidatorsHash.selector);
        _update(h104, b, noSet);
        // the right header with another set in the calldata
        vm.expectRevert(GnoLightClient.WrongSet.selector);
        _update(h101, b, noSet);
        // another chain id
        bytes[] memory l = h101.leaves;
        bytes memory keep = l[1];
        l[1] = TmHeader.chainIdLeaf("gno-test-2");
        vm.expectRevert(GnoLightClient.ChainId.selector);
        client.update(l, h101.commit, a, noSet);
        l[1] = keep;
        // a next set given for a header that announces none
        vm.expectRevert(GnoLightClient.NextSetUnexpected.selector);
        _update(h101, a, b);
        assertTrue(_update(h101, a, noSet));
        // at or below the trusted height
        vm.expectRevert(GnoLightClient.HeightNotAboveTrusted.selector);
        _update(h101, a, noSet);
        assertTrue(_update(_syn(2), a, noSet));
        // the announcing header without its next set, with another, with one that is not a set the client keeps
        VHeader memory h103 = _syn(3);
        vm.expectRevert(GnoLightClient.NextSetMissing.selector);
        _update(h103, a, noSet);
        vm.expectRevert(GnoLightClient.NextValidatorsHash.selector);
        _update(h103, a, _set(synthetic, "C"));
        IGnoLightClient.Validator[] memory unsorted = _set(synthetic, "B");
        (unsorted[0], unsorted[1]) = (unsorted[1], unsorted[0]);
        vm.expectRevert(TmValset.BadValidatorSet.selector);
        _update(h103, a, unsorted);
        assertTrue(_update(h103, a, b));
    }

    /// The commit's shape: slots strictly increasing and inside the set, nanoseconds in range, a part set of one or
    /// more, a round that is the signatures' own and not negative.
    function test_refusals_commitShape() public {
        _synWorld();
        VHeader memory h = _syn(1);
        IGnoLightClient.Validator[] memory a = _synSet(h);
        IGnoLightClient.Commit memory c = _copyCommit(h.commit);
        (c.precommits[0], c.precommits[1]) = (c.precommits[1], c.precommits[0]);
        vm.expectRevert(GnoLightClient.SlotsUnsorted.selector);
        client.update(h.leaves, c, a, noSet);
        c = _copyCommit(h.commit);
        c.precommits[1] = c.precommits[0];
        vm.expectRevert(GnoLightClient.SlotsUnsorted.selector);
        client.update(h.leaves, c, a, noSet);
        c = _copyCommit(h.commit);
        c.precommits[2].index = 4;
        vm.expectRevert(GnoLightClient.SlotOutOfRange.selector);
        client.update(h.leaves, c, a, noSet);
        c = _copyCommit(h.commit);
        c.precommits[0].timeNanos = 1_000_000_000;
        vm.expectRevert(GnoLightClient.BadCommit.selector);
        client.update(h.leaves, c, a, noSet);
        c = _copyCommit(h.commit);
        c.partsTotal = 0;
        vm.expectRevert(GnoLightClient.BadCommit.selector);
        client.update(h.leaves, c, a, noSet);
        c = _copyCommit(h.commit);
        c.round = -1;
        vm.expectRevert(GnoLightClient.BadCommit.selector);
        client.update(h.leaves, c, a, noSet);
        // another part set: the sign bytes are not the ones signed
        c = _copyCommit(h.commit);
        c.partsHash = bytes32(uint256(c.partsHash) ^ 1);
        vm.expectRevert(GnoLightClient.SignatureMismatch.selector);
        client.update(h.leaves, c, a, noSet);
        // a precommit's own time changed by a nanosecond
        c = _copyCommit(h.commit);
        c.precommits[1].timeNanos += 1;
        vm.expectRevert(GnoLightClient.SignatureMismatch.selector);
        client.update(h.leaves, c, a, noSet);
        assertTrue(_update(h, a, noSet));
    }

    /// A commit of round 2 carried under round 0: every slot's sign bytes are built from the commit's one round, so
    /// none verifies (tm2's Commit.ValidateBasic: one round per commit).
    function test_refusals_theRoundIsTheSignaturesOwn() public {
        _synWorld();
        for (uint256 i = 1; i <= 6; i++) {
            _synUpdate(i);
        }
        VHeader memory h107 = _syn(7);
        assertEq(h107.commit.round, 2);
        IGnoLightClient.Commit memory c = _copyCommit(h107.commit);
        c.round = 0;
        IGnoLightClient.Validator[] memory b = _synSet(h107);
        vm.expectRevert(GnoLightClient.SignatureMismatch.selector);
        client.update(h107.leaves, c, b, _set(synthetic, "C"));
        assertTrue(_update(h107, b, _set(synthetic, "C")));
    }

    function _copyCommit(IGnoLightClient.Commit memory c) internal pure returns (IGnoLightClient.Commit memory d) {
        d.partsTotal = c.partsTotal;
        d.partsHash = c.partsHash;
        d.round = c.round;
        d.precommits = new IGnoLightClient.Precommit[](c.precommits.length);
        for (uint256 i = 0; i < c.precommits.length; i++) {
            IGnoLightClient.Precommit memory p = c.precommits[i];
            d.precommits[i] = IGnoLightClient.Precommit(p.index, p.timeSecs, p.timeNanos, p.r, p.s);
        }
    }

    function _slice(IGnoLightClient.Commit memory c, uint256 from, uint256 to)
        internal
        pure
        returns (IGnoLightClient.Commit memory d)
    {
        d = _copyCommit(c);
        d.precommits = new IGnoLightClient.Precommit[](to - from);
        for (uint256 i = from; i < to; i++) {
            d.precommits[i - from] = c.precommits[i];
        }
    }

    // ------------------------------------------------------------------------------------------- staging --

    /// Header 109 (set D: 60 validators, 38 precommits carried): a first call of 20 precommits stores its tally
    /// and returns false; a second whose precommits overlap the first's five slots finishes it (the overlap is not
    /// counted twice); another submitter's stage is its own; the finished stage is deleted.
    function test_staging_acrossCallsBySubmitter() public {
        _synWorld();
        for (uint256 i = 1; i <= 8; i++) {
            _synUpdate(i);
        }
        VHeader memory h = _syn(9);
        IGnoLightClient.Validator[] memory dset = _synSet(h);
        assertEq(dset.length, 60);
        uint256 n = h.commit.precommits.length;
        assertEq(n, 38);
        address alice = makeAddr("submitter");
        address bob = makeAddr("other submitter");
        bytes32 key = client.stageKey(
            alice, h.hash, h.commit.partsTotal, h.commit.partsHash, 0, client.epoch(), h.validatorsHash
        );
        vm.prank(alice);
        assertFalse(client.update(h.leaves, _slice(h.commit, 0, 20), dset, noSet));
        int64 power;
        for (uint256 i = 0; i < 20; i++) {
            power += dset[h.commit.precommits[i].index].power;
        }
        assertEq(client.stagePower(key), power);
        assertEq(client.trustedHeight(), 108, "nothing accepted yet");
        // bob's precommits 20.. alone do not cross: his own stage
        vm.prank(bob);
        assertFalse(client.update(h.leaves, _slice(h.commit, 20, n), dset, noSet));
        assertEq(client.stagePower(key), power, "alice's stage untouched");
        // alice finishes with 15.. (five slots she verified already)
        vm.prank(alice);
        assertTrue(client.update(h.leaves, _slice(h.commit, 15, n), dset, noSet));
        assertEq(client.trustedHeight(), 109);
        assertEq(client.stagePower(key), 0, "the finished stage is gone");
        assertEq(client.epoch(), 3);
    }

    // ------------------------------------------------------------------------------------------- proveHeader --

    /// Header 106 skipped by the updates (105 → 107) is proved by its own commit while B is current: it records
    /// once (a second proof is a no-op), its LastResultsHash proves block 105's messages, and after the client
    /// followed C nothing it proves holds.
    function test_proveHeader_aSkippedHeaderOfTheCurrentSet() public {
        _synWorld();
        for (uint256 i = 1; i <= 5; i++) {
            _synUpdate(i);
        }
        _synUpdate(7); // 105 → 107, skipping 106
        VHeader memory h106 = _syn(6);
        IGnoLightClient.Validator[] memory b = _synSet(h106);
        (bytes memory m5, bytes memory meta5) = _message("gnot-alice-2");
        vm.expectRevert(GnoLightClient.UnknownHeader.selector);
        client.verify(meta5, m5);
        uint64 records = client.records();
        // a header above the trusted height is not proved
        VHeader memory h108 = _syn(8);
        vm.expectRevert(GnoLightClient.HeightAboveTrusted.selector);
        client.proveHeader(h108.leaves, h108.commit, _synSet(h108));
        assertTrue(client.proveHeader(h106.leaves, h106.commit, b));
        assertEq(client.records(), records + 1);
        assertTrue(client.verify(meta5, m5));
        assertTrue(client.proveHeader(h106.leaves, h106.commit, b), "a recorded header: a no-op");
        assertEq(client.records(), records + 1, "recorded once");
        // a header the client accepted by update is recorded already
        VHeader memory h105 = _syn(5);
        assertTrue(client.proveHeader(h105.leaves, h105.commit, b));
        assertEq(client.records(), records + 1);
        // C's first header: B's headers prove nothing more, and B's header is not proved under C
        _synUpdate(8);
        vm.expectRevert(GnoLightClient.UnknownHeader.selector);
        client.verify(meta5, m5);
        vm.expectRevert(GnoLightClient.ValidatorsHash.selector);
        client.proveHeader(h106.leaves, h106.commit, b);
    }

    // -------------------------------------------------------------------------------------------- the admin --

    function test_admin_setAdminAndNoSecondInit() public {
        _synWorld();
        assertEq(client.admin(), admin);
        vm.expectRevert(GnoLightClient.NotAdmin.selector);
        client.setAdmin(address(this));
        vm.prank(admin);
        vm.expectRevert(GnoLightClient.ZeroAdmin.selector);
        client.setAdmin(address(0));
        address next = makeAddr("next admin");
        vm.prank(admin);
        client.setAdmin(next);
        assertEq(client.admin(), next);
        vm.prank(admin);
        vm.expectRevert(GnoLightClient.NotAdmin.selector);
        client.setAdmin(admin);
        // no second initialize, on the proxy or on the implementation
        IGnoLightClient.Validator[] memory a = _set(synthetic, "A");
        bytes[] memory l = _syn(0).leaves;
        vm.expectRevert("Initializable: contract is already initialized");
        client.initialize("gno-test-1", GNOTEST, l, a, admin);
        GnoLightClient impl = new GnoLightClient(bytes32(0), verifier);
        vm.expectRevert("Initializable: contract is already initialized");
        impl.initialize("gno-test-1", GNOTEST, l, a, admin);
        assertEq(address(client.ED25519()), address(verifier), "the verifier, read through the proxy");
        assertEq(client.moduleType(), 0);
        assertEq(client.clockFactor(), CLOCK_FACTOR);
        assertEq(client.mailboxPkgPathHash(), keccak256(bytes(PATH)));
        assertEq(client.chainId(), "gno-test-1");
        assertEq(client.originDomain(), GNOTEST);
    }

    /// The trust root (audit round 2, E2-CT3): the client binds its trusted header itself — never one announcing a
    /// change (the next header would be signed by the announced set, which the client never stored: dead on
    /// arrival, with no reset), never one more than an hour from the initialization either way (an old root is a past
    /// set's, whose keys may be anyone's by now), always the chain id's and its set's. gnoland-1's 487,882 announces
    /// the fifth validator: refused; 487,881 a day after its time, or an hour and a second either side: refused; at
    /// exactly an hour either side: taken, its hash, height and time kept.
    function test_initialize_bindsItsTrustRoot() public {
        address impl = address(new GnoLightClient(keccak256(bytes(PATH)), verifier));
        ProxyAdmin pa = new ProxyAdmin();
        VHeader memory a = _fx(1);
        assertTrue(a.announcing);
        vm.warp(a.time);
        _expectInitRefused(impl, pa, "gnoland-1", a.leaves, a.validators, GnoLightClient.NextValidatorsHash.selector);
        VHeader memory h = _fx(0);
        vm.warp(h.time + 1 days);
        _expectInitRefused(impl, pa, "gnoland-1", h.leaves, h.validators, GnoLightClient.TrustedTime.selector);
        vm.warp(h.time + 1 hours + 1);
        _expectInitRefused(impl, pa, "gnoland-1", h.leaves, h.validators, GnoLightClient.TrustedTime.selector);
        vm.warp(h.time - 1 hours - 1);
        _expectInitRefused(impl, pa, "gnoland-1", h.leaves, h.validators, GnoLightClient.TrustedTime.selector);
        // another chain id, another set than the header's
        vm.warp(h.time);
        _expectInitRefused(impl, pa, "gnoland-2", h.leaves, h.validators, GnoLightClient.ChainId.selector);
        _expectInitRefused(impl, pa, "gnoland-1", h.leaves, _fx(3).validators, GnoLightClient.ValidatorsHash.selector);
        // a fifteen-leaf header
        bytes[] memory short = new bytes[](15);
        for (uint256 i = 0; i < 15; i++) {
            short[i] = h.leaves[i];
        }
        _expectInitRefused(impl, pa, "gnoland-1", short, h.validators, TmHeader.BadHeader.selector);
        // an hour either side is taken
        for (uint256 k = 0; k < 2; k++) {
            vm.warp(k == 0 ? h.time + 1 hours : h.time - 1 hours);
            GnoLightClient c = GnoLightClient(
                address(
                    new TransparentUpgradeableProxy(
                        impl,
                        address(pa),
                        abi.encodeCall(
                            GnoLightClient.initialize, ("gnoland-1", GNOLAND1, h.leaves, h.validators, admin)
                        )
                    )
                )
            );
            assertEq(c.trustedHash(), h.hash);
            assertEq(c.trustedHeight(), h.height);
            assertEq(c.trustedTime(), h.time);
            assertEq(c.setHash(), h.validatorsHash);
            assertEq(c.records(), 1);
        }
    }

    function _expectInitRefused(
        address impl,
        ProxyAdmin pa,
        string memory chainId,
        bytes[] memory leaves,
        IGnoLightClient.Validator[] memory set,
        bytes4 err
    ) internal {
        bytes memory init = abi.encodeCall(GnoLightClient.initialize, (chainId, GNOLAND1, leaves, set, admin));
        vm.expectRevert(err);
        new TransparentUpgradeableProxy(impl, address(pa), init);
    }

    /// An implementation over no verifier is refused at its construction (a zero address, or one without code).
    function test_admin_aVerifierWithoutCodeIsRefused() public {
        vm.expectRevert(GnoLightClient.NoVerifier.selector);
        new GnoLightClient(bytes32(0), IEd25519Verifier(address(0)));
        vm.expectRevert(GnoLightClient.NoVerifier.selector);
        new GnoLightClient(bytes32(0), IEd25519Verifier(makeAddr("an account without code")));
    }

    // ------------------------------------------------------------------------------------- the signatures --

    /// The verifier the client calls agrees with Go's crypto/ed25519 on every triple of the vectors: each real
    /// signature of gnoland-1 and of the synthetic chain and its four neighbours (a bit of s, a bit of r, the
    /// prevote's bytes, another key), 490 triples, 392 of them refused.
    function test_verifier_agreesWithGoOnEveryTriple() public view {
        uint256 checked;
        uint256 refused;
        string[2] memory files = [fixtures, synthetic];
        for (uint256 f = 0; f < 2; f++) {
            uint256 n = _count(files[f], ".checks");
            for (uint256 i = 0; i < n; i++) {
                string memory p = _at(".checks", i);
                bool valid = vm.parseJsonBool(files[f], string.concat(p, ".valid"));
                bool got = verifier.verify(
                    vm.parseJsonBytes32(files[f], string.concat(p, ".pubkey")),
                    vm.parseJsonBytes(files[f], string.concat(p, ".signBytes")),
                    vm.parseJsonBytes32(files[f], string.concat(p, ".r")),
                    vm.parseJsonBytes32(files[f], string.concat(p, ".s"))
                );
                assertEq(got, valid, vm.parseJsonString(files[f], string.concat(p, ".why")));
                if (!valid) {
                    refused++;
                }
                checked++;
            }
        }
        assertEq(checked, 490);
        assertEq(refused, 392);
    }

    /// Go-refused triples through the client: header 101's slot 0 carrying the signature Go refused with one bit of
    /// s changed, then one bit of r (the vectors' own triples, over the exact sign bytes the client builds); and the
    /// same signature with s + L, which Go refuses as non-canonical (S ≥ L) though [S]B is the same point. Each is
    /// SignatureMismatch; the honest commit then lands.
    function test_refusals_goRefusedTriplesThroughTheClient() public {
        _synWorld();
        VHeader memory h101 = _syn(1);
        IGnoLightClient.Validator[] memory a = _synSet(h101);
        IGnoLightClient.Precommit memory p0 = h101.commit.precommits[0];
        assertEq(p0.index, 0);
        string[2] memory whys = ["gno-test-1 101/0: one bit of s", "gno-test-1 101/0: one bit of r"];
        for (uint256 k = 0; k < 2; k++) {
            (bytes32 pk, bytes memory sb, bytes32 r, bytes32 s, bool valid) = _check(synthetic, whys[k]);
            assertFalse(valid, "Go refused it");
            assertEq(pk, a[0].pubkey, "slot 0's key");
            assertEq(sb, h101.signBytes[0], "the sign bytes the client builds");
            IGnoLightClient.Commit memory c = _copyCommit(h101.commit);
            (c.precommits[0].r, c.precommits[0].s) = (r, s);
            vm.expectRevert(GnoLightClient.SignatureMismatch.selector);
            client.update(h101.leaves, c, a, noSet);
        }
        // s + L: the group order added to the little-endian scalar s
        uint256 L = 2 ** 252 + 27742317777372353535851937790883648493;
        uint256 sPlusL = _le(p0.s) + L; // s < L < 2^253: no overflow
        IGnoLightClient.Commit memory c2 = _copyCommit(h101.commit);
        c2.precommits[0].s = bytes32(_le(bytes32(sPlusL)));
        assertFalse(verifier.verify(a[0].pubkey, h101.signBytes[0], p0.r, c2.precommits[0].s));
        vm.expectRevert(GnoLightClient.SignatureMismatch.selector);
        client.update(h101.leaves, c2, a, noSet);
        assertTrue(_update(h101, a, noSet));
    }

    /// The check named `why` in a vectors file.
    function _check(string memory json, string memory why)
        internal
        view
        returns (bytes32 pk, bytes memory sb, bytes32 r, bytes32 s, bool valid)
    {
        uint256 n = _count(json, ".checks");
        for (uint256 i = 0; i < n; i++) {
            string memory p = _at(".checks", i);
            if (keccak256(bytes(vm.parseJsonString(json, string.concat(p, ".why")))) == keccak256(bytes(why))) {
                return (
                    vm.parseJsonBytes32(json, string.concat(p, ".pubkey")),
                    vm.parseJsonBytes(json, string.concat(p, ".signBytes")),
                    vm.parseJsonBytes32(json, string.concat(p, ".r")),
                    vm.parseJsonBytes32(json, string.concat(p, ".s")),
                    vm.parseJsonBool(json, string.concat(p, ".valid"))
                );
            }
        }
        revert(string.concat("no check ", why));
    }

    /// A 32-byte string read as a little-endian integer (and back: the map is its own inverse).
    function _le(bytes32 b) internal pure returns (uint256 v) {
        for (uint256 i = 0; i < 32; i++) {
            v |= uint256(uint8(b[i])) << (8 * i);
        }
    }
}

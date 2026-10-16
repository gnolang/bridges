// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {GnoLightClient} from "../src/GnoLightClient.sol";
import {IGnoLightClient} from "../src/interfaces/IGnoLightClient.sol";
import {BridgeFixture} from "./helpers/BridgeFixture.sol";
import {TestChain} from "./helpers/TestChain.sol";

/// A contract that stages through the client: the watch cannot read the header from its calldata (audit round 2,
/// E2-CT2's relay).
contract StageRelay {
    function stage(
        IGnoLightClient c,
        bytes[] calldata leaves,
        IGnoLightClient.Commit calldata commit,
        IGnoLightClient.Validator[] calldata set
    ) external returns (bool) {
        IGnoLightClient.Validator[] memory none;
        return c.update(leaves, commit, set, none);
    }
}

/// Staged tallies (PLAN.md §6.3, the red team's ER4): a stage is keyed by the submitter, the header, its BlockID,
/// the round, the client's epoch and the set the header must be signed by, and every call re-checks the header
/// against the live state; so no stage outlives a set change, none finishes a header already recorded, and none is
/// another submitter's. On the Solidity test chain (real ed25519 signatures through lane E-M2a's verifier).
contract GnoLightClientStagingTest is BridgeFixture {
    IGnoLightClient.Validator[] internal none;
    IGnoLightClient.Validator[] internal setX;
    IGnoLightClient.Validator[] internal setY;
    address internal alice = makeAddr("alice");
    address internal bob = makeAddr("bob");

    function setUp() public {
        IGnoLightClient.Validator[] memory x = TestChain.validators(1, 4, 60);
        IGnoLightClient.Validator[] memory y = TestChain.validators(2, 5, 60);
        for (uint256 i = 0; i < x.length; i++) {
            setX.push(x[i]);
        }
        for (uint256 i = 0; i < y.length; i++) {
            setY.push(y[i]);
        }
        _worldWith(x);
    }

    function _x() internal view returns (IGnoLightClient.Validator[] memory v) {
        v = new IGnoLightClient.Validator[](setX.length);
        for (uint256 i = 0; i < v.length; i++) {
            v[i] = setX[i];
        }
    }

    function _y() internal view returns (IGnoLightClient.Validator[] memory v) {
        v = new IGnoLightClient.Validator[](setY.length);
        for (uint256 i = 0; i < v.length; i++) {
            v[i] = setY[i];
        }
    }

    struct H {
        TestChain.Header h;
        bytes[] leaves;
        bytes32 hash;
    }

    function _h(int64 height, IGnoLightClient.Validator[] memory v, IGnoLightClient.Validator[] memory next)
        internal
        view
        returns (H memory r)
    {
        r.h = _header(height, v, next, bytes32(0));
        r.leaves = TestChain.leaves(r.h);
        r.hash = TestChain.hash(r.leaves);
    }

    function _commit(H memory r, IGnoLightClient.Validator[] memory v, uint256 from, uint256 to)
        internal
        view
        returns (IGnoLightClient.Commit memory)
    {
        return TestChain.commit(r.h, r.hash, v, TestChain.range(from, to), 0);
    }

    function _accept(H memory r, IGnoLightClient.Validator[] memory v, IGnoLightClient.Validator[] memory next)
        internal
    {
        vm.prank(relayer);
        assertTrue(d.client.update(r.leaves, _commit(r, v, 0, v.length), v, next));
    }

    /// A stage of header 101 begun under set X (epoch 0) is never finished after the client followed X → Y → X:
    /// the epoch in its key orphans it, so the second half of the signatures alone does not cross 2/3.
    function test_aStageNeverOutlivesASetChange() public {
        H memory h101 = _h(101, _x(), _x());
        _accept(_h(103, _x(), _y()), _x(), _y()); // announces Y; 101 and 102 skipped
        // alice stages 101's proof with two of four (120 of 240): not more than two thirds
        vm.prank(alice);
        assertFalse(d.client.proveHeader(h101.leaves, _commit(h101, _x(), 0, 2), _x()));
        bytes32 key0 = d.client.stageKey(alice, h101.hash, 1, h101.h.partsHash, 0, 0, TestChain.setHash(_x()));
        assertEq(d.client.stagePower(key0), 120);
        // Y's first header, announcing X again
        _accept(_h(104, _y(), _x()), _y(), _x());
        assertEq(d.client.epoch(), 1);
        _refusedUnderY(h101);
        // X's first header again
        _accept(_h(105, _x(), _x()), _x(), none);
        assertEq(d.client.epoch(), 2);
        assertEq(d.client.setHash(), TestChain.setHash(_x()), "X is current again");
        // the other two alone: a fresh stage under epoch 2, 120 of 240 — the old stage counts for nothing
        vm.prank(alice);
        assertFalse(d.client.proveHeader(h101.leaves, _commit(h101, _x(), 2, 4), _x()));
        assertEq(
            d.client
            .stagePower(d.client.stageKey(alice, h101.hash, 1, h101.h.partsHash, 0, 2, TestChain.setHash(_x()))),
            120
        );
        assertEq(d.client.stagePower(key0), 120, "the old stage lies inert");
        // three of four under the current epoch prove it
        vm.prank(alice);
        assertTrue(d.client.proveHeader(h101.leaves, _commit(h101, _x(), 0, 1), _x()));
        assertEq(d.client.entry(h101.hash).epoch, 2);
    }

    function _refusedUnderY(H memory h101) internal {
        IGnoLightClient.Commit memory c = _commit(h101, _x(), 2, 4);
        IGnoLightClient.Validator[] memory x = _x();
        vm.prank(alice);
        vm.expectRevert(GnoLightClient.ValidatorsHash.selector);
        d.client.proveHeader(h101.leaves, c, x);
    }

    /// A stage whose header someone else accepted never finishes: its next call meets the height rule; a header
    /// recorded under the current epoch is a no-op for proveHeader before any tally.
    function test_aStageNeverFinishesARecordedHeader() public {
        IGnoLightClient.Validator[] memory x = _x();
        H memory h101 = _h(101, x, x);
        vm.prank(alice);
        assertFalse(d.client.update(h101.leaves, _commit(h101, x, 0, 2), x, none));
        vm.prank(bob);
        assertTrue(d.client.update(h101.leaves, _commit(h101, x, 0, 3), x, none));
        vm.prank(alice);
        vm.expectRevert(GnoLightClient.HeightNotAboveTrusted.selector);
        d.client.update(h101.leaves, _commit(h101, x, 2, 4), x, none);
        uint64 records = d.client.records();
        vm.prank(alice);
        assertTrue(d.client.proveHeader(h101.leaves, _commit(h101, x, 2, 3), x), "recorded: a no-op");
        assertEq(d.client.records(), records);
    }

    /// A precommit signed with another validator's key in its slot fails, whatever else stands; a call that carries
    /// nothing new is refused and leaves the stage as it was.
    function test_aPrecommitOfAnotherKeyFails() public {
        IGnoLightClient.Validator[] memory x = _x();
        H memory h101 = _h(101, x, x);
        IGnoLightClient.Commit memory c = _commit(h101, x, 0, 4);
        (bytes32 r, bytes32 s) = TestChain.sign(x[2].pubkey, TestChain.signBytes(h101.h, h101.hash, 0, 1));
        (c.precommits[1].r, c.precommits[1].s) = (r, s); // slot 1 carries slot 2's signature over slot 1's bytes
        vm.expectRevert(GnoLightClient.SignatureMismatch.selector);
        d.client.update(h101.leaves, c, x, none);
        vm.prank(alice);
        assertFalse(d.client.update(h101.leaves, _commit(h101, x, 0, 1), x, none));
        vm.prank(alice);
        vm.expectRevert(GnoLightClient.NoNewSignature.selector);
        d.client.update(h101.leaves, _commit(h101, x, 0, 0), x, none);
        bytes32 key = d.client.stageKey(alice, h101.hash, 1, h101.h.partsHash, 0, 0, TestChain.setHash(x));
        assertEq(d.client.stagePower(key), 60);
    }

    /// Every StageProgress carries the header's height beside its hash (audit round 2, E2-CT2), so the watch holds a
    /// stage against gno.land's header at that height without reading the call's data, through a contract as
    /// directly; a call adding a new signature to a stage announces the stage's new tally.
    function test_stageProgressCarriesTheHeight() public {
        IGnoLightClient.Validator[] memory x = _x();
        H memory h101 = _h(101, x, x);
        bytes32 key = d.client.stageKey(alice, h101.hash, 1, h101.h.partsHash, 0, 0, TestChain.setHash(x));
        vm.expectEmit(true, true, false, true, address(d.client));
        emit IGnoLightClient.StageProgress(key, alice, h101.hash, 101, 60, 240);
        vm.prank(alice);
        assertFalse(d.client.update(h101.leaves, _commit(h101, x, 0, 1), x, none));
        vm.expectEmit(true, true, false, true, address(d.client));
        emit IGnoLightClient.StageProgress(key, alice, h101.hash, 101, 120, 240);
        vm.prank(alice);
        assertFalse(d.client.update(h101.leaves, _commit(h101, x, 0, 2), x, none));
        StageRelay relay = new StageRelay();
        bytes32 rkey = d.client.stageKey(address(relay), h101.hash, 1, h101.h.partsHash, 0, 0, TestChain.setHash(x));
        vm.expectEmit(true, true, false, true, address(d.client));
        emit IGnoLightClient.StageProgress(rkey, address(relay), h101.hash, 101, 60, 240);
        relay.stage(d.client, h101.leaves, _commit(h101, x, 3, 4), x);
    }

    /// A call that verifies no new signature stores nothing and announces nothing (audit round 2, E2-CT2's proof of
    /// concept): a stranger staging a header gno.land never made — the live chain id, a height above the trusted one,
    /// the live set's hash — with no precommit at all is refused, directly and through a contract; and so is a call
    /// whose precommits a stage already counted. Every StageProgress then carries a signature over its header.
    function test_aCallWithoutANewSignatureIsRefused() public {
        address stranger = makeAddr("stranger");
        IGnoLightClient.Validator[] memory x = _x();
        H memory r;
        r.h = _header(chainHeight + 5, x, x, keccak256("made up"));
        r.leaves = TestChain.leaves(r.h);
        r.hash = TestChain.hash(r.leaves);
        IGnoLightClient.Commit memory c;
        c.partsTotal = 1;
        c.partsHash = keccak256("parts");
        bytes32 key = d.client.stageKey(stranger, r.hash, 1, c.partsHash, 0, d.client.epoch(), d.client.setHash());
        vm.recordLogs();
        vm.prank(stranger);
        vm.expectRevert(GnoLightClient.NoNewSignature.selector);
        d.client.update(r.leaves, c, x, none);
        assertEq(d.client.stagePower(key), 0, "no stage stored");
        StageRelay relay = new StageRelay();
        vm.prank(stranger);
        vm.expectRevert(GnoLightClient.NoNewSignature.selector);
        relay.stage(d.client, r.leaves, c, x);
        assertEq(vm.getRecordedLogs().length, 0, "no StageProgress announced");
        // a precommit a stage already counted is no new signature either
        vm.prank(alice);
        assertFalse(d.client.update(r.leaves, _commit(r, x, 0, 1), x, none));
        vm.prank(alice);
        vm.expectRevert(GnoLightClient.NoNewSignature.selector);
        d.client.update(r.leaves, _commit(r, x, 0, 1), x, none);
        assertEq(d.client.stagePower(d.client.stageKey(alice, r.hash, 1, r.h.partsHash, 0, 0, d.client.setHash())), 60);
    }

    /// A set of 255 validators, the client's bound, followed through its announcement and its first header — whose
    /// 171 signatures (≈39M gas) are staged over five calls of at most 40, each inside EIP-7825's 16,777,216 — and
    /// 256 refused at an announcement.
    function test_theBoundOf255Validators() public {
        IGnoLightClient.Validator[] memory x = _x();
        IGnoLightClient.Validator[] memory big = TestChain.validators(9, 255, 1);
        H memory a = _h(101, x, big);
        _accept(a, x, big);
        H memory b = _h(102, big, big);
        uint256 k = TestChain.quorum(big).length;
        assertEq(k, 171);
        for (uint256 from = 0; from < k; from += 40) {
            uint256 to = from + 40 < k ? from + 40 : k;
            IGnoLightClient.Commit memory c = _commit(b, big, from, to);
            vm.prank(relayer);
            uint256 g = gasleft();
            bool finished = d.client.update(b.leaves, c, big, none);
            assertLt(g - gasleft(), 16_777_216 - 21_000 - 200_000, "one call inside a transaction's cap");
            assertEq(finished, to == k);
        }
        assertEq(d.client.setHash(), TestChain.setHash(big));
        // 256 are refused at the announcement
        IGnoLightClient.Validator[] memory over = TestChain.validators(10, 256, 1);
        H memory a2 = _h(103, big, over);
        IGnoLightClient.Commit memory c2 = TestChain.commit(a2.h, a2.hash, big, TestChain.quorum(big), 0);
        vm.prank(relayer);
        vm.expectRevert();
        d.client.update(a2.leaves, c2, big, over);
    }
}

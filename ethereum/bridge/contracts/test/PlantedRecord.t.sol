// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {BridgeSetup} from "../script/Setup.s.sol";
import {HlAdmin} from "../src/HlAdmin.sol";
import {HlAdminExec} from "../src/admin/HlAdminExec.sol";
import {HlAdminStorage, SetupIncomplete} from "../src/admin/HlAdminStorage.sol";
import {IGnoLightClient} from "../src/interfaces/IGnoLightClient.sol";
import {IHlAdmin} from "../src/interfaces/IHlAdmin.sol";
import {AdminBase} from "./helpers/AdminBase.sol";
import {TestChain} from "./helpers/TestChain.sol";

/// What a compromised deployer sends at step 7's nonce in place of HlAdmin's initcode (audit round 2, E2-RT1), or a
/// proposer in place of a successor's: code that does what HlAdmin's constructor does — creates HlAdminExec with the
/// same settings (HlAdmin's CREATE nonce 1) and leaves HlAdmin's storage as that constructor leaves it (`words`, the
/// namespace's first sixteen) — then writes a record of its own and returns HlAdmin's exact runtime code. The code
/// hash, every immutable and every finishSetup check see HlAdmin; no `DecisionRecorded` was ever emitted.
contract PlantingInitcode {
    struct Plant {
        /// Decision records written at these numbers: `kind` and `params`, delivered at block 1, time 1 (mature since
        /// 1970).
        uint64[] numbers;
        uint8 kind;
        bytes params;
        /// The last number, when `lastSeqSet` (else the constructor's).
        bool lastSeqSet;
        uint64 lastSeq;
        /// The pending count, when not zero.
        uint32 pending;
        uint64[2][] cancelled;
        IHlAdmin.Hold[] holds;
        bool setupDone;
        /// The start, when `startSeqSet` (else the constructor's).
        bool startSeqSet;
        uint64 startSeq;
        /// Raw words written last: (slot, value).
        bytes32[2][] raw;
    }

    constructor(bytes memory runtime, HlAdminExec.Settings memory settings, bytes32[] memory words, Plant memory p) {
        new HlAdminExec(settings);
        uint256 base = uint256(HlAdminStorage.SLOT);
        for (uint256 i = 0; i < words.length; i++) {
            uint256 s = base + i;
            bytes32 w = words[i];
            assembly ("memory-safe") {
                sstore(s, w)
            }
        }
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        for (uint256 i = 0; i < p.numbers.length; i++) {
            l.decisions[p.numbers[i]] = HlAdminStorage.Pending(address(this), 1, p.kind, 1, p.params);
        }
        if (p.lastSeqSet) {
            l.lastSeq = p.lastSeq;
        }
        if (p.pending != 0) {
            l.pending = p.pending;
        }
        for (uint256 i = 0; i < p.cancelled.length; i++) {
            l.cancelled.push(p.cancelled[i]);
        }
        for (uint256 i = 0; i < p.holds.length; i++) {
            l.holds.push(p.holds[i]);
        }
        if (p.setupDone) {
            l.setupDone = true;
        }
        if (p.startSeqSet) {
            l.startSeq = p.startSeq;
        }
        for (uint256 i = 0; i < p.raw.length; i++) {
            bytes32 s = p.raw[i][0];
            bytes32 w = p.raw[i][1];
            assembly ("memory-safe") {
                sstore(s, w)
            }
        }
        assembly ("memory-safe") {
            return(add(runtime, 32), mload(runtime))
        }
    }
}

interface IOwnableTransfer {
    function transferOwnership(address) external;
}

/// Code an initcode puts where HlAdmin's HlAdminExec should be (HlAdmin's CREATE nonce 1): run by HlAdmin's
/// delegatecall — finishSetup, which anyone calls, or any decision's execute — it hands the ProxyAdmin to a thief.
contract ForeignExec {
    address internal immutable thief;
    address internal immutable proxyAdmin;

    constructor(address t, address p) {
        (thief, proxyAdmin) = (t, p);
    }

    function checkSetup() external {
        IOwnableTransfer(proxyAdmin).transferOwnership(thief);
    }

    function run(uint8, bytes calldata, uint64) external returns (string memory) {
        IOwnableTransfer(proxyAdmin).transferOwnership(thief);
        return "";
    }
}

/// An initcode that creates ForeignExec at its nonce 1 where HlAdmin's constructor creates HlAdminExec, leaves
/// HlAdmin's storage as that constructor leaves it, and returns HlAdmin's exact runtime code — whose `exec` names
/// that address by its CREATE nonce alone, so the code hash a decision pins is HlAdmin's honest one.
contract ForeignExecInitcode {
    constructor(bytes memory runtime, bytes32[] memory words, address thief, address proxyAdmin) {
        new ForeignExec(thief, proxyAdmin);
        uint256 base = uint256(HlAdminStorage.SLOT);
        for (uint256 i = 0; i < words.length; i++) {
            uint256 s = base + i;
            bytes32 w = words[i];
            assembly ("memory-safe") {
                sstore(s, w)
            }
        }
        assembly ("memory-safe") {
            return(add(runtime, 32), mload(runtime))
        }
    }
}

/// The compromised deployer's own contract, to which a planted kind 9 hands every authority: it answers kind 9's
/// questions as a fresh successor would, takes the inheritance, and answers every other view (a binding) as HlAdmin
/// does — its code says what it likes, so no check of a successor's answers can stop this; only the record can.
contract Grabber {
    address internal immutable pred;

    constructor(address p) {
        pred = p;
    }

    function predecessor() external view returns (address) {
        return pred;
    }

    function inherited() external pure returns (bool) {
        return false;
    }

    function successor() external pure returns (address) {
        return address(0);
    }

    function clockFactor() external pure returns (uint64) {
        return CLOCK_FACTOR;
    }

    function successorInit(uint64, address[6] calldata, IHlAdmin.Hold[] calldata) external {}

    fallback(bytes calldata q) external returns (bytes memory) {
        (bool ok, bytes memory r) = pred.staticcall(q);
        require(ok, "no answer");
        return r;
    }
}

/// A decision can exist only at a number HlAdmin recorded (audit round 2, E2-RT1: eth/bridge/audits/r2/plan-redteam.md).
/// Every contract the deployer creates runs initcode it chooses, and the initcode can write any storage: the red
/// team's HlAdmin plants `decisions[2^64−1]` — a kind 9 to the deployer's own contract, mature since 1970, above the
/// last number so no cancel can name it — and returns HlAdmin's runtime code. Only a number in (start, last] runs:
/// each one `handle` accepted and wrote (or cleared), every one within a cancel's reach; finishSetup requires the record
/// empty at a launch, and its hold record one the keys' flags could have left (check 34), and a successor's
/// inheritance writes its whole record. `hleth audit` comparing each creation's input with the build refuses the
/// initcode itself (the Go half).
contract PlantedRecordTest is AdminBase {
    address internal attacker = makeAddr("attacker");

    function _start() internal returns (BridgeSetup.Config memory c) {
        vm.warp(1_791_000_000);
        vm.roll(1_000);
        chainTime = 1_791_000_000;
        IGnoLightClient.Validator[] memory v = TestChain.validators(1, 4, 60);
        bytes[] memory l = TestChain.leaves(_header(100, v, v, bytes32(0)));
        bytes32 hh = TestChain.hash(l);
        c = _config(l, v);
        _setChainSet(v);
        chainHeight = 100;
        chainHash = hh;
    }

    /// HlAdminExec's settings as HlAdmin's constructor hands them over.
    function _settings(HlAdmin.Config memory c) internal pure returns (HlAdminExec.Settings memory) {
        return HlAdminExec.Settings({
            mailbox: c.mailbox,
            client: c.client,
            gate: c.gate,
            token0: c.token0,
            token1: c.token1,
            proxyAdmin: c.proxyAdmin,
            merkleTreeHook: c.merkleTreeHook,
            protocolFee: c.protocolFee,
            ed25519: c.ed25519,
            ed25519CodeHash: c.ed25519CodeHash,
            localDomain: c.localDomain,
            origin: c.origin,
            route0: c.route0,
            route1: c.route1,
            mailboxPkgPathHash: c.mailboxPkgPathHash,
            maxPerMessage: c.maxPerMessage,
            dayCap: c.dayCap,
            launch: c.predecessor == address(0)
        });
    }

    /// HlAdmin as `creator`'s next creation would leave it — its runtime code, HlAdminExec's code hash, the first
    /// sixteen words of its namespace — read in a snapshot that is then undone (the creator's nonce with it).
    function _reference(HlAdmin.Config memory c, address creator)
        internal
        returns (bytes memory code, bytes32 execHash, bytes32[] memory words)
    {
        uint256 snap = vm.snapshotState();
        vm.prank(creator);
        HlAdmin real = new HlAdmin(c);
        code = address(real).code;
        execHash = address(real.exec()).codehash;
        words = new bytes32[](16);
        for (uint256 i = 0; i < 16; i++) {
            words[i] = vm.load(address(real), bytes32(uint256(HlAdminStorage.SLOT) + i));
        }
        vm.revertToState(snap);
    }

    /// Creates, as `creator`'s next creation, the planting initcode for HlAdmin over `c`; checks the result is
    /// HlAdmin's exact code at the address HlAdmin would have.
    function _plantAs(address creator, HlAdmin.Config memory c, PlantingInitcode.Plant memory p)
        internal
        returns (HlAdmin a)
    {
        (bytes memory code, bytes32 execHash, bytes32[] memory words) = _reference(c, creator);
        address at = vm.computeCreateAddress(creator, vm.getNonce(creator));
        vm.prank(creator);
        a = HlAdmin(address(new PlantingInitcode(code, _settings(c), words, p)));
        assertEq(address(a), at, "where HlAdmin would be");
        assertEq(address(a).codehash, keccak256(code), "HlAdmin's exact runtime code");
        assertEq(address(a.exec()).codehash, execHash, "and HlAdminExec's");
    }

    /// The red team's launch: steps 1–6 as the deployer, step 7 by its planting initcode — every planted decision a
    /// kind 9 to the attacker's Grabber — then steps 8–10; finishSetup is the caller's.
    function _plantedLaunch(PlantingInitcode.Plant memory p) internal returns (Grabber g) {
        BridgeSetup.Config memory c = _start();
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        vm.stopPrank();
        vm.prank(attacker);
        g = new Grabber(d.adminAt);
        p.kind = 9;
        p.params = abi.encodePacked(_a32(address(g)), address(g).codehash);
        d.admin = _plantAs(DEPLOYER, BridgeSetup.adminConfig(c, d), p);
        assertEq(address(d.admin), d.adminAt, "at the address the ProxyAdmin was handed to");
        vm.startPrank(DEPLOYER);
        BridgeSetup.settings(c, d);
        BridgeSetup.ownership(d);
        vm.stopPrank();
    }

    function _numbers(uint64 a) internal pure returns (uint64[] memory n) {
        n = new uint64[](1);
        n[0] = a;
    }

    function _numbers(uint64 a, uint64 b) internal pure returns (uint64[] memory n) {
        n = new uint64[](2);
        (n[0], n[1]) = (a, b);
    }

    /// The attacker's execute of a planted number: refused as no decision, nothing moved.
    function _refused(uint64 n, Grabber g) internal {
        vm.prank(attacker);
        vm.expectRevert(HlAdmin.WrongDecision.selector);
        d.admin.execute(n);
        assertEq(d.admin.decision(n).number, 0, "no decision at a number HlAdmin did not record");
        assertEq(d.admin.successor(), address(0));
        assertEq(d.proxyAdmin.owner(), address(d.admin), "nothing moved");
        assertEq(d.mailbox.owner(), address(d.admin));
        assertEq(d.client.admin(), address(d.admin));
        assertTrue(d.proxyAdmin.owner() != address(g));
    }

    /// The red team's scenario: `decisions[2^64−1]` planted, a kind 9 to the deployer's contract. Every check of the
    /// setup passes; once a real decision pends (the pending count the planted one needs), the deployer's execute of
    /// 2^64−1 would hand it every authority. It is refused: HlAdmin never recorded that number.
    function test_aDecisionPlantedAboveTheLastNeverRuns() public {
        PlantingInitcode.Plant memory p;
        p.numbers = _numbers(type(uint64).max);
        Grabber g = _plantedLaunch(p);
        d.admin.finishSetup();
        assertTrue(d.admin.setupDone(), "the code, the immutables, every check: HlAdmin's");
        uint64 real = _decideKind(7, _fee(1, treasurer));
        assertEq(d.admin.pending(), 1);
        _refused(type(uint64).max, g);
        _wait(WEEK);
        _expectEnd(real, 7, "");
        assertEq(d.protocolFee.protocolFee(), 1, "the real decision ran on its clock");
    }

    /// Planted at or below the number the sequence starts after (r/hlgov's counter at the deploy: 41 here): numbers
    /// HlAdmin never recorded either. Refused; and a cancel GovDAO sends naming them is recorded and closes nothing
    /// (a record there is no decision: closing one would underflow the pending count and block the sequence).
    function test_aDecisionPlantedAtOrBelowTheStartNeverRuns() public {
        startSequence = 41;
        nextSeq = 42;
        PlantingInitcode.Plant memory p;
        p.numbers = _numbers(7, 41);
        Grabber g = _plantedLaunch(p);
        d.admin.finishSetup();
        assertEq(d.admin.lastSeq(), 41);
        uint64 real = _decideKind(7, _fee(2, treasurer));
        assertEq(real, 42);
        _refused(41, g);
        _refused(7, g);
        uint64 c = _decideKind(1, _cancelList(_numbers(7, 41)));
        assertEq(d.admin.lastSeq(), c, "the cancel is recorded");
        assertEq(d.admin.pending(), 1, "and closes nothing: the real decision still pends");
        _wait(WEEK);
        _expectEnd(real, 7, "");
    }

    /// Planted at the numbers r/hlgov's first decisions take, where nothing pends: a cancel (number 1) and an unknown
    /// kind (number 2) are recorded without a pending record, so `handle` clears what another hand left at their
    /// numbers. Refused, both.
    function test_aDecisionPlantedWhereNothingPendsNeverRuns() public {
        PlantingInitcode.Plant memory p;
        p.numbers = _numbers(1, 2);
        Grabber g = _plantedLaunch(p);
        d.admin.finishSetup();
        _decideKind(1, _cancelOne(9));
        _decideKind(42, abi.encodePacked(uint64(1)));
        uint64 real = _decideKind(7, _fee(3, treasurer));
        assertEq(d.admin.lastSeq(), 3);
        assertEq(d.admin.pending(), 1);
        _refused(1, g);
        _refused(2, g);
        _wait(WEEK);
        _expectEnd(real, 7, "");
    }

    /// finishSetup requires a launch's record empty (check 34): the last number the start, nothing pending, no
    /// cancelled range. Refused: a planted pending count (the red team's other way to run a planted decision), a
    /// planted last number ahead of the start with a record inside, a planted cancelled range.
    function test_finishSetup_refusesAPlantedRecord() public {
        for (uint256 i = 0; i < 3; i++) {
            uint256 snap = vm.snapshotState();
            PlantingInitcode.Plant memory p;
            if (i == 0) {
                p.numbers = _numbers(type(uint64).max);
                p.pending = 1;
            } else if (i == 1) {
                p.numbers = _numbers(1);
                (p.lastSeqSet, p.lastSeq) = (true, 1);
            } else {
                p.cancelled = new uint64[2][](1);
                p.cancelled[0] = [uint64(1), type(uint64).max];
            }
            _plantedLaunch(p);
            vm.expectRevert(abi.encodeWithSelector(SetupIncomplete.selector, uint8(34)));
            d.admin.finishSetup();
            assertFalse(d.admin.setupDone());
            vm.revertToState(snap);
        }
    }

    /// Check 34 holds a launch's record to the immutable start (audit round 3, E3-CT2): the initcode that plants the last
    /// number can plant the stored start beside it, and a check of one against the other passed a planted pair. Ahead
    /// of r/hlgov (2^64−1: every decision ignored, cancels included), behind it (start 41, the pair at 40: r/hlgov's
    /// next number, 42, out of order for good), or a planted `successor` (every decision refused as retired): each
    /// wedged the governance for good behind a finishSetup that passed. Each is refused now; nothing moves. (And the
    /// stored start alone ahead of the last number — every decision recorded, none ever a decision — refused before
    /// and now.)
    function test_finishSetup_refusesAPlantedPairOrSuccessor() public {
        for (uint256 i = 0; i < 4; i++) {
            uint256 snap = vm.snapshotState();
            PlantingInitcode.Plant memory p;
            if (i == 0) {
                (p.lastSeqSet, p.lastSeq) = (true, type(uint64).max);
                (p.startSeqSet, p.startSeq) = (true, type(uint64).max);
            } else if (i == 1) {
                startSequence = 41;
                (p.lastSeqSet, p.lastSeq) = (true, 40);
                (p.startSeqSet, p.startSeq) = (true, 40);
            } else if (i == 2) {
                p.raw = new bytes32[2][](1);
                p.raw[0] = [bytes32(uint256(HlAdminStorage.SLOT) + 1), bytes32(uint256(uint160(makeAddr("nobody"))))];
            } else {
                (p.startSeqSet, p.startSeq) = (true, type(uint64).max);
            }
            _plantedLaunch(p);
            vm.prank(attacker);
            vm.expectRevert(abi.encodeWithSelector(SetupIncomplete.selector, uint8(34)));
            d.admin.finishSetup();
            assertFalse(d.admin.setupDone());
            assertEq(d.admin.launchNonce(), 0, "no launch line drawn");
            assertEq(d.proxyAdmin.owner(), address(d.admin), "nothing moved");
            vm.revertToState(snap);
        }
    }

    /// A planted `setupDone` skips finishSetup, and check 34 with it (this fold's review): at start 41 the initcode
    /// plants `setupDone`, a start of 40, a kind 9 at 41 (inside the window it planted) and the pending count. Every
    /// view an audit reads is a launch's — `lastSeq` 41, no successor, the owners after step 10 — and finishSetup
    /// refuses (`SetupAlreadyDone`) before any check. A launch's floor is its `startSequence`, held where no initcode can
    /// move it (an immutable, which the audit reads against r/hlgov's counter): 41 is no decision.
    function test_aPlantedSetupDoneRunsNoPlantedDecision() public {
        startSequence = 41;
        nextSeq = 42;
        PlantingInitcode.Plant memory p;
        p.numbers = _numbers(41);
        p.pending = 1;
        p.setupDone = true;
        (p.startSeqSet, p.startSeq) = (true, 40);
        Grabber g = _plantedLaunch(p);
        vm.expectRevert(HlAdmin.SetupAlreadyDone.selector);
        d.admin.finishSetup();
        assertTrue(d.admin.setupDone(), "the view says done");
        assertEq(d.admin.lastSeq(), 41, "the last number r/hlgov's counter");
        _refused(41, g);
    }

    /// The slot of the params' length word of the record at `n` (the decisions mapping is the layout's slot 13; a
    /// record is three words: deliverer, time and kind; deliveredBlock; params).
    function _paramsSlot(uint64 n) internal pure returns (bytes32) {
        return bytes32(uint256(keccak256(abi.encode(uint256(n), uint256(HlAdminStorage.SLOT) + 13))) + 2);
    }

    /// A record planted at the next number with a params length no write can walk — 2^128 bytes (assigning over it
    /// clears the old tail: a gas bomb) or a length word encoded wrong (a panic) — would make r/hlgov's next decision
    /// undeliverable for good, at a launch or in a successor (this fold's review). `handle` clears the length word too:
    /// both deliver within a transaction's gas cap, and the sequence goes on.
    function test_aPlantedParamsLengthBlocksNothing() public {
        bytes32[2] memory words = [bytes32((uint256(1) << 129) | 1), bytes32(uint256(3))];
        for (uint256 i = 0; i < 2; i++) {
            uint256 snap = vm.snapshotState();
            PlantingInitcode.Plant memory p;
            p.numbers = _numbers(1);
            p.raw = new bytes32[2][](1);
            p.raw[0] = [_paramsSlot(1), words[i]];
            _plantedLaunch(p);
            assertEq(vm.load(address(d.admin), _paramsSlot(1)), words[i], "planted where the record's params are");
            d.admin.finishSetup();
            bytes memory m = _message(HLGOV, address(d.admin), _body(7, 1, _fee(4, treasurer)));
            bytes[] memory ms = new bytes[](1);
            ms[0] = m;
            bytes[] memory meta = _prove(ms);
            vm.prank(relayer, relayer);
            d.mailbox.process{gas: 16_777_216}(meta[0], m);
            assertEq(d.admin.lastSeq(), 1, "delivered");
            assertTrue(_pending(1));
            assertEq(d.admin.decision(1).params, _fee(4, treasurer), "its own params");
            vm.revertToState(snap);
        }
    }

    function _successorConfig(address predecessor) internal view returns (HlAdmin.Config memory c) {
        c.mailbox = address(d.mailbox);
        c.client = address(d.client);
        c.gate = address(d.gate);
        c.token0 = address(d.token0);
        c.token1 = address(d.token1);
        c.proxyAdmin = address(d.proxyAdmin);
        c.merkleTreeHook = address(d.merkleTreeHook);
        c.protocolFee = address(d.protocolFee);
        c.ed25519 = address(d.ed25519);
        c.ed25519CodeHash = BridgeSetup.ed25519CodeHash();
        c.localDomain = LOCAL;
        c.origin = GNO;
        c.hlgov = HLGOV;
        c.route0 = ROUTE0;
        c.route1 = ROUTE1;
        c.mailboxPkgPathHash = _pkgPathHash();
        c.predecessor = predecessor;
    }

    /// A successor is created by its proposer, whose initcode the decision's code hash does not pin: one that plants
    /// every number cancelled with a count pending (so the ranges are never pruned: every decision the successor
    /// records would die), a hold that never ended (the replacement's floor), its setup done, and a decision at the
    /// number it inherits. Its inheritance writes its whole record from its predecessor's: nothing planted survives,
    /// its decisions run, its own finishSetup runs.
    function test_aSuccessorsPlantedRecordDiesAtItsInheritance() public {
        _world();
        address proposer = makeAddr("the successor's proposer");
        HlAdmin.Config memory sc = _successorConfig(address(d.admin));
        (bytes memory code,,) = _reference(sc, proposer);
        uint64 n = _decideKind(9, _transfer(vm.computeCreateAddress(proposer, vm.getNonce(proposer)), keccak256(code)));
        PlantingInitcode.Plant memory p;
        p.numbers = _numbers(n);
        p.kind = 12;
        p.params = _limits(0, type(uint64).max, type(uint64).max);
        p.pending = 3;
        p.cancelled = new uint64[2][](1);
        p.cancelled[0] = [uint64(0), type(uint64).max];
        p.holds = new IHlAdmin.Hold[](1);
        p.holds[0] = IHlAdmin.Hold(0, type(uint64).max);
        p.setupDone = true;
        HlAdmin succ = _plantAs(proposer, sc, p);
        _wait(WEEK);
        _expectEnd(n, 9, "");
        assertEq(d.admin.successor(), address(succ), "handed over");
        assertEq(succ.lastSeq(), n);
        assertEq(succ.pending(), 0, "its record is its predecessor's alone");
        assertEq(succ.cancelledRanges().length, 0);
        assertEq(succ.holds().length, d.admin.holds().length);
        assertFalse(succ.setupDone());
        vm.expectRevert(HlAdmin.WrongDecision.selector);
        succ.execute(n);
        succ.finishSetup();
        assertTrue(succ.setupDone());
        uint64 next = nextSeq++;
        _deliver(_message(HLGOV, address(succ), _body(12, next, _limits(0, 5, 6))));
        _wait(WEEK);
        vm.expectEmit(true, false, false, true, address(succ));
        emit IHlAdmin.DecisionExecuted(next, 12);
        succ.execute(next);
        assertEq(d.gate.limits(0).maxPerMessage, 5, "the successor's decisions run");
    }

    /// HlAdmin runs only the HlAdminExec its constructor created (E2-RT1's class, found folding it). A successor's
    /// code hash pins its runtime, which names its HlAdminExec by address only (CREATE(successor, 1)): a proposer's
    /// initcode can put other code there and return HlAdmin's exact runtime — the hash GovDAO pins, the very one a
    /// reviewer gets by deploying the reviewed build in a simulation. Kind 9's checks all pass on it, and without the
    /// guard the hand-over runs and anyone's finishSetup then runs that code with every authority. The successor's
    /// inheritance refuses it (`ForeignExec`): the hand-over reverts and pends for a cancel, nothing moves, and the
    /// foreign code never runs.
    function test_aSuccessorsForeignExecNeverRuns() public {
        _world();
        address proposer = makeAddr("the successor's proposer");
        HlAdmin.Config memory sc = _successorConfig(address(d.admin));
        (bytes memory code,, bytes32[] memory words) = _reference(sc, proposer);
        address at = vm.computeCreateAddress(proposer, vm.getNonce(proposer));
        uint64 n = _decideKind(9, _transfer(at, keccak256(code)));
        vm.prank(proposer);
        address s = address(new ForeignExecInitcode(code, words, attacker, address(d.proxyAdmin)));
        assertEq(s, at);
        assertEq(s.codehash, keccak256(code), "HlAdmin's exact runtime: the hash the decision pins");
        _wait(WEEK);
        vm.expectRevert(abi.encodeWithSignature("ForeignExec()"));
        d.admin.execute(n);
        assertTrue(_pending(n), "the hand-over pends for a cancel");
        assertEq(d.admin.successor(), address(0));
        assertEq(d.proxyAdmin.owner(), address(d.admin), "nothing moved");
        vm.prank(attacker);
        vm.expectRevert(abi.encodeWithSignature("ForeignExec()"));
        HlAdmin(s).finishSetup();
        assertEq(d.proxyAdmin.owner(), address(d.admin));
    }

    /// The same at a launch: the deployer's initcode puts ForeignExec where HlAdmin's HlAdminExec should be, and
    /// without the guard the first finishSetup after step 10 — anyone's — hands the ProxyAdmin to the thief. It refuses
    /// (`ForeignExec`) before any of its checks. (`hleth audit`'s comparison of HlAdminExec's code with the build, and
    /// of each creation's input, refuses it too.)
    function test_aLaunchsForeignExecNeverRuns() public {
        BridgeSetup.Config memory c = _start();
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        vm.stopPrank();
        (bytes memory code,, bytes32[] memory words) = _reference(BridgeSetup.adminConfig(c, d), DEPLOYER);
        vm.prank(DEPLOYER);
        d.admin = HlAdmin(address(new ForeignExecInitcode(code, words, attacker, address(d.proxyAdmin))));
        assertEq(address(d.admin), d.adminAt);
        assertEq(address(d.admin).codehash, keccak256(code), "HlAdmin's exact runtime code");
        vm.startPrank(DEPLOYER);
        BridgeSetup.settings(c, d);
        BridgeSetup.ownership(d);
        vm.stopPrank();
        vm.prank(attacker);
        vm.expectRevert(abi.encodeWithSignature("ForeignExec()"));
        d.admin.finishSetup();
        assertFalse(d.admin.setupDone());
        assertEq(d.proxyAdmin.owner(), address(d.admin), "nothing moved");
    }

    // ------------------------------------------------------------------------------------------ the hold record --

    /// The time `_start` warps to: a launch's eleven steps all run at it.
    uint64 internal constant SETUP_TIME = 1_791_000_000;

    /// The guardians' moments as an initcode plants them: one word, the layout's slot 12 (holdBlock, holdTime,
    /// dipBlock, dipTime, low to high).
    function _moments(uint64 hb, uint64 ht, uint64 db, uint64 dt) internal pure returns (bytes32[2][] memory raw) {
        raw = new bytes32[2][](1);
        raw[0] = [
            bytes32(uint256(HlAdminStorage.SLOT) + 12),
            bytes32(uint256(hb) | (uint256(ht) << 64) | (uint256(db) << 128) | (uint256(dt) << 192))
        ];
    }

    /// `n` holds, each ended long ago: (i + 1, i + 1).
    function _endedHolds(uint256 n) internal pure returns (IHlAdmin.Hold[] memory h) {
        h = new IHlAdmin.Hold[](n);
        for (uint256 i = 0; i < n; i++) {
            h[i] = IHlAdmin.Hold(uint64(i + 1), uint64(i + 1));
        }
    }

    /// The planted launch, then anyone's finishSetup: refused at check 34, and nothing moves.
    function _refusedAt34(PlantingInitcode.Plant memory p) internal {
        _plantedLaunch(p);
        vm.prank(attacker);
        vm.expectRevert(abi.encodeWithSelector(SetupIncomplete.selector, uint8(34)));
        d.admin.finishSetup();
        assertFalse(d.admin.setupDone());
        assertEq(d.admin.launchNonce(), 0, "no launch line drawn");
        assertEq(d.proxyAdmin.owner(), address(d.admin), "nothing moved");
    }

    /// Steps 1–10 of a launch by HlAdmin's own constructor, nothing planted; finishSetup is the caller's.
    function _realLaunch() internal {
        BridgeSetup.Config memory c = _start();
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.contracts(c);
        d.admin = BridgeSetup.admin(c, d);
        BridgeSetup.settings(c, d);
        BridgeSetup.ownership(d);
        vm.stopPrank();
    }

    /// A kind 9 to a successor built by HlAdmin's own constructor, matured: its number and the successor.
    function _handOver() internal returns (uint64 n, HlAdmin succ) {
        HlAdmin.Config memory sc = _successorConfig(address(d.admin));
        vm.prank(makeAddr("the successor's proposer"));
        succ = new HlAdmin(sc);
        n = _decideKind(9, _transfer(address(succ), address(succ).codehash));
        _wait(WEEK);
    }

    /// Nine planted holds, each ended long ago (audit round 4, E4-CT1): a history the keys' flags never leave, since a
    /// ninth hold merges the two oldest. Before, finishSetup passed, and every kind 9 then pended for good:
    /// successorInit takes at most eight holds, and the launch HlAdmin's history never shrinks. Refused now. Eight
    /// ended ones, a history the guardians could have left before the setup, pass, and hand over whole.
    function test_finishSetup_refusesNinePlantedHolds() public {
        uint256 snap = vm.snapshotState();
        PlantingInitcode.Plant memory p;
        p.holds = _endedHolds(9);
        _refusedAt34(p);
        vm.revertToState(snap);
        p.holds = _endedHolds(8);
        _plantedLaunch(p);
        d.admin.finishSetup();
        (uint64 n, HlAdmin succ) = _handOver();
        _expectEnd(n, 9, "");
        assertEq(succ.holds().length, 8, "the successor took the eight");
    }

    /// A planted hold that has not ended, (0, 2^64−1) (E4-CT1). Before, finishSetup passed, and every replacement
    /// (kind 5) under a guardian hold then landed on its 7th day instead of the hold's 21st: the floor takes the
    /// earliest hold overlapping the decision's pending life. Refused now, as is a hold that ends in the setup's own
    /// second (it would overlap a decision delivered in that block). One that ended the second before overlaps no
    /// decision, and passes.
    function test_finishSetup_refusesAPlantedOpenHold() public {
        uint64[3] memory ends = [type(uint64).max, SETUP_TIME, SETUP_TIME - 1];
        for (uint256 i = 0; i < 3; i++) {
            uint256 snap = vm.snapshotState();
            PlantingInitcode.Plant memory p;
            p.holds = new IHlAdmin.Hold[](1);
            p.holds[0] = IHlAdmin.Hold(0, ends[i]);
            if (i < 2) {
                _refusedAt34(p);
            } else {
                _plantedLaunch(p);
                assertEq(block.timestamp, SETUP_TIME);
                d.admin.finishSetup();
                assertTrue(d.admin.setupDone(), "a hold that ended before the setup");
            }
            vm.revertToState(snap);
        }
    }

    /// The guardians' moments, planted (E4-CT1). A dip dated ahead: before, finishSetup passed, and HlAdmin stood held
    /// with no flag up, every 7-day decision deferred until two guardians set and cleared their flags and waited the
    /// grace. Refused now, and two moments no flag leaves either: a hold start dated ahead (while that hold stands, a
    /// replacement's floor, the start plus 21 days, is out of reach), and a hold stored with neither two guardian flags
    /// nor a dip — alone, which no view shows, or beside one guardian flag (the next guardian flag revives it, and its
    /// start dates the guardians' next hold, lowering a replacement's floor under it).
    function test_finishSetup_refusesPlantedHoldMoments() public {
        for (uint256 i = 0; i < 4; i++) {
            uint256 snap = vm.snapshotState();
            PlantingInitcode.Plant memory p;
            if (i == 0) {
                p.raw = _moments(1, 1, 1, type(uint64).max);
            } else if (i == 1) {
                p.raw = _moments(1, type(uint64).max, 1, SETUP_TIME);
            } else if (i == 2) {
                p.raw = _moments(1, 1, 0, 0);
            } else {
                // and seat 1's flag, at block 1, time 1 (the layout's slots 8 and 10: flag blocks, flag times)
                p.raw = new bytes32[2][](3);
                p.raw[0] = _moments(1, 1, 0, 0)[0];
                p.raw[1] = [bytes32(uint256(HlAdminStorage.SLOT) + 8), bytes32(uint256(1))];
                p.raw[2] = [bytes32(uint256(HlAdminStorage.SLOT) + 10), bytes32(uint256(1))];
            }
            _refusedAt34(p);
            vm.revertToState(snap);
        }
    }

    /// No new veto (E4-CT1): the hold record the guardians themselves leave before the setup passes check 34 — their
    /// hold standing at the setup, dipping at it, or ended with its dip run out and nothing since; or nine holds set
    /// and ended before it, the history kept at eight, which a kind 9 then hands over whole.
    function test_finishSetup_takesTheGuardiansOwnHolds() public {
        for (uint256 i = 0; i < 4; i++) {
            uint256 snap = vm.snapshotState();
            _realLaunch();
            if (i < 3) {
                _aflag(seat1);
                _aflag(seat2);
                if (i >= 1) {
                    _aclear(seat1);
                }
                if (i == 2) {
                    _aclear(seat2);
                    _wait(DAY + 1);
                }
                assertEq(d.admin.hold().blockNumber != 0, i < 2, "standing, dipping, or ended");
            } else {
                for (uint256 k = 0; k < 9; k++) {
                    _aflag(seat1);
                    _aflag(seat2);
                    _aclear(seat1);
                    _aclear(seat2);
                    _wait(DAY + 1);
                }
                _aflag(seat1);
                _aclear(seat1);
                assertEq(d.admin.holds().length, 8, "nine holds, the history at eight");
            }
            d.admin.finishSetup();
            assertTrue(d.admin.setupDone());
            if (i == 3) {
                (uint64 n, HlAdmin succ) = _handOver();
                _expectEnd(n, 9, "");
                assertEq(succ.holds().length, 8, "the successor took the eight");
            }
            vm.revertToState(snap);
        }
    }

    /// The one record of the guardians' own that check 34 makes wait (E4-CT1): a hold whose dip ran its course in the
    /// setup's own second, settled by a flag in that block, ended at that second and would overlap a decision delivered
    /// in the block. finishSetup refuses it there; a block later it overlaps none, and finishSetup passes.
    function test_finishSetup_aHoldEndedThisSecondWaitsABlock() public {
        _realLaunch();
        _aflag(seat1);
        _aflag(seat2);
        _aclear(seat1);
        _aclear(seat2);
        _wait(DAY);
        _aflag(watch1);
        IHlAdmin.Hold[] memory h = d.admin.holds();
        assertEq(h.length, 1);
        assertEq(h[0].end, block.timestamp, "the hold ended at this very second");
        vm.expectRevert(abi.encodeWithSelector(SetupIncomplete.selector, uint8(34)));
        d.admin.finishSetup();
        _wait(1);
        d.admin.finishSetup();
        assertTrue(d.admin.setupDone());
    }
}

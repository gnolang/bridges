// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {ITransparentUpgradeableProxy} from "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol";
import {StorageSlot} from "@openzeppelin/contracts/utils/StorageSlot.sol";
import {Mailbox} from "@hyperlane-xyz/core/contracts/Mailbox.sol";
import {HypERC20} from "@hyperlane-xyz/core/contracts/token/HypERC20.sol";

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {BridgeSetup} from "../script/Setup.s.sol";
import {Ed25519Verifier} from "../src/Ed25519Verifier.sol";
import {GnoLightClient} from "../src/GnoLightClient.sol";
import {HlAdmin} from "../src/HlAdmin.sol";
import {PausableGate} from "../src/PausableGate.sol";
import {HlAdminExec} from "../src/admin/HlAdminExec.sol";
import {AuthorityLost, NoCodeYet, NotDelegated, SetupIncomplete} from "../src/admin/HlAdminStorage.sol";
import {IGnoLightClient} from "../src/interfaces/IGnoLightClient.sol";
import {IHlAdmin} from "../src/interfaces/IHlAdmin.sol";
import {Guardians} from "../src/lib/Guardians.sol";
import {AdminBase} from "./helpers/AdminBase.sol";
import {TestChain} from "./helpers/TestChain.sol";

/// A new version of the gate with an initializer of its own, run by kind 8's call.
contract GateV2 is PausableGate {
    uint256 public v2;

    function initializeV2(uint256 x) external reinitializer(2) {
        v2 = x;
    }

    function initializeBroken() external pure {
        revert("broken initializer");
    }
}

/// One slot ahead of everything a contract inheriting it keeps.
abstract contract OneSlotAhead {
    uint256 private ahead;
}

/// A new version of the gate on another storage layout (one slot shifted, as a reordered base or OpenZeppelin 5's
/// namespaced slots would be): it reads its owner, and its initializer's state, from slots that hold neither.
contract GateOtherLayout is OneSlotAhead, PausableGate {}

/// A new version of the gate whose initializer overwrites the proxy's EIP-1967 admin slot.
contract GateAdminClobber is PausableGate {
    function clobber() external {
        StorageSlot.getAddressSlot(0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103).value =
            address(1);
    }
}

/// A "new version" of the client that names another admin.
contract ClientOtherAdmin {
    function admin() external pure returns (address) {
        return address(0xbad);
    }
}

/// A new version of the gate whose initializer points the proxy at other code: it writes the EIP-1967 implementation
/// slot during kind 8's call (audit round 2, E2-RT10).
contract GateRepoint is PausableGate {
    function initializeV2(address to) external {
        StorageSlot.getAddressSlot(0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc).value = to;
    }
}

/// Code no decision pinned (another code hash than the gate's).
contract GateUnpinned is PausableGate {
    function unpinned() external pure returns (bool) {
        return true;
    }
}

/// A fixed verifier: the same verdicts from other code (another address, another code hash), which a kind 8 moves the
/// client onto (a verifier swap).
contract Ed25519VerifierV2 is Ed25519Verifier {
    function version() external pure returns (uint256) {
        return 2;
    }
}

/// A verifier that accepts every signature: what a successor's proposer could name as its `ed25519`.
contract AlwaysTrueVerifier {
    function verify(bytes32, bytes calldata, bytes32, bytes32) external pure returns (bool) {
        return true;
    }
}

/// An ISM that verifies everything: the red team's successor client (E2-RT3).
contract AlwaysTrueIsm {
    function moduleType() external pure returns (uint8) {
        return 0;
    }

    function verify(bytes calldata, bytes calldata) external pure returns (bool) {
        return true;
    }
}

/// A successor that answers kind 9's own four questions and takes the inheritance, but whose binding views revert.
contract SilentBindingsSuccessor {
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

    function successorInit(uint64, address[6] calldata, IHlAdmin.Hold[] calldata) external pure {}

    fallback() external {
        revert("no binding");
    }
}

/// A stand-in successor that answers what it was built to answer (kind 9's checks), or reverts; every other view (a
/// binding) it answers as its predecessor does.
contract MockSuccessor {
    address internal pred;
    bool internal inh;
    address internal succ;
    uint64 internal factor;
    bool internal revertReads;
    bool internal revertInit;

    constructor(address p, bool i, address s, uint64 f, bool rr, bool ri) {
        (pred, inh, succ, factor, revertReads, revertInit) = (p, i, s, f, rr, ri);
    }

    function predecessor() external view returns (address) {
        require(!revertReads, "no answer");
        return pred;
    }

    function inherited() external view returns (bool) {
        return inh;
    }

    function successor() external view returns (address) {
        return succ;
    }

    function clockFactor() external view returns (uint64) {
        return factor;
    }

    function successorInit(uint64, address[6] calldata, IHlAdmin.Hold[] calldata) external view {
        require(!revertInit, "no inheritance");
    }

    fallback(bytes calldata q) external returns (bytes memory) {
        (bool ok, bytes memory r) = pred.staticcall(q);
        require(ok, "no answer");
        return r;
    }
}

/// Kind 8 (an upgrade of each of the five proxies, with or without a call) and kind 9 (the hand-over to a successor
/// HlAdmin), with every refusal: what is spent, and what reverts and so stays pending for a retry.
contract HlAdminHandoverTest is AdminBase {
    function setUp() public {
        _world();
    }

    function _impl(address proxy) internal view returns (address) {
        return d.proxyAdmin.getProxyImplementation(ITransparentUpgradeableProxy(proxy));
    }

    // -------------------------------------------------------------------------------------------- kind 8 --

    /// Each of the five proxies upgraded to a new implementation whose code hash the decision pins; deliveries go
    /// on through the new code over the same state.
    function test_kind8_upgradesEachOfTheFiveProxies() public {
        address[5] memory proxies =
            [address(d.mailbox), address(d.token0), address(d.token1), address(d.client), address(d.gate)];
        address[5] memory impls = [
            address(new Mailbox(LOCAL)),
            address(new HypERC20(6, 1, 1, address(d.mailbox))),
            address(new HypERC20(6, 1, 1, address(d.mailbox))),
            address(new GnoLightClient(_pkgPathHash(), d.ed25519)),
            address(new PausableGate())
        ];
        uint64[5] memory ns;
        for (uint256 i = 0; i < 5; i++) {
            ns[i] = _decideKind(8, _upgrade(proxies[i], impls[i], impls[i].codehash, ""));
        }
        _wait(WEEK);
        for (uint256 i = 0; i < 5; i++) {
            assertTrue(_impl(proxies[i]) != impls[i]);
            _expectEnd(ns[i], 8, "");
            assertEq(_impl(proxies[i]), impls[i]);
        }
        address alice = makeAddr("alice");
        _mint(0, alice, 77);
        assertEq(d.token0.balanceOf(alice), 77);
        assertEq(d.client.trustedHeight(), chainHeight, "the client's state carried over");
    }

    /// With a call: `upgradeAndCall` runs the new version's initializer in the upgrade, never left open.
    function test_kind8_withACallRunsTheNewInitializer() public {
        GateV2 v2 = new GateV2();
        uint64 n = _decideKind(
            8, _upgrade(address(d.gate), address(v2), address(v2).codehash, abi.encodeCall(GateV2.initializeV2, (42)))
        );
        _wait(WEEK);
        _expectEnd(n, 8, "");
        assertEq(GateV2(address(d.gate)).v2(), 42);
        vm.expectRevert("Initializable: contract is already initialized");
        GateV2(address(d.gate)).initializeV2(7);
        assertEq(d.gate.keys()[0], seat1, "the gate's state carried over");
    }

    /// An upgrade that leaves a proxy outside HlAdmin's authority reverts (`AuthorityLost`), and the decision pends
    /// for a cancel (the review's finding 3): a gate on another storage layout (its owner reads zero), a client that
    /// names another admin, and an upgrade whose call overwrote the proxy's admin slot (the ProxyAdmin's question then
    /// reaches the implementation, which has no answer: the execution reverts all the same).
    function test_kind8_anUpgradeThatLosesAuthorityReverts() public {
        GateOtherLayout other = new GateOtherLayout();
        ClientOtherAdmin cl = new ClientOtherAdmin();
        GateAdminClobber clobber = new GateAdminClobber();
        uint64 a = _decideKind(8, _upgrade(address(d.gate), address(other), address(other).codehash, ""));
        uint64 b = _decideKind(8, _upgrade(address(d.client), address(cl), address(cl).codehash, ""));
        uint64 c = _decideKind(
            8,
            _upgrade(
                address(d.gate),
                address(clobber),
                address(clobber).codehash,
                abi.encodeCall(GateAdminClobber.clobber, ())
            )
        );
        _wait(WEEK);
        address gateImpl = _impl(address(d.gate));
        address clientImpl = _impl(address(d.client));
        vm.expectRevert(abi.encodeWithSelector(AuthorityLost.selector, address(d.gate)));
        d.admin.execute(a);
        vm.expectRevert(abi.encodeWithSelector(AuthorityLost.selector, address(d.client)));
        d.admin.execute(b);
        vm.expectRevert();
        d.admin.execute(c);
        for (uint256 i = 0; i < 3; i++) {
            assertTrue(_pending([a, b, c][i]), "the decision pends");
        }
        assertEq(_impl(address(d.gate)), gateImpl, "nothing upgraded");
        assertEq(_impl(address(d.client)), clientImpl);
        assertEq(d.gate.owner(), address(d.admin));
    }

    /// The call cannot move the proxy off the implementation the decision pinned (audit round 2, E2-RT10): a new
    /// version's initializer that writes the EIP-1967 implementation slot — here pointing the gate at code no hash
    /// pinned — reverts the execution (`ImplementationMoved`), and the decision pends for a cancel.
    function test_kind8_aCallThatRepointsTheProxyReverts() public {
        GateRepoint v2 = new GateRepoint();
        GateUnpinned unpinned = new GateUnpinned();
        uint64 n = _decideKind(
            8,
            _upgrade(
                address(d.gate),
                address(v2),
                address(v2).codehash,
                abi.encodeCall(GateRepoint.initializeV2, (address(unpinned)))
            )
        );
        _wait(WEEK);
        address before = _impl(address(d.gate));
        vm.expectRevert(abi.encodeWithSignature("ImplementationMoved(address)", address(d.gate)));
        d.admin.execute(n);
        assertTrue(_pending(n), "the decision pends");
        assertEq(_impl(address(d.gate)), before, "nothing upgraded");
        _decideKind(1, _cancelOne(n));
        assertFalse(_pending(n));
    }

    /// Spent: a proxy that is not one of the five (even naming an implementation without code: the decision's own
    /// checks come first), an address with high bytes, a code hash that is not the implementation's, an EIP-7702
    /// delegation designator. Pending (`NoCodeYet`), never spent: an implementation without code — an account that
    /// never existed (EXTCODEHASH 0) or one that exists without code (EXTCODEHASH keccak256 of nothing) — whatever
    /// hash the decision pins: an empty target never matches.
    function test_kind8_refusals() public {
        PausableGate impl = new PausableGate();
        address delegated = makeAddr("a delegated account");
        vm.etch(delegated, abi.encodePacked(hex"ef0100", address(impl)));
        bytes[5] memory params = [
            _upgrade(address(d.merkleTreeHook), address(impl), address(impl).codehash, ""),
            _upgrade(address(d.merkleTreeHook), makeAddr("nobody"), bytes32(0), ""),
            abi.encodePacked(
                bytes32(uint256(1) << 160 | uint160(address(d.gate))), _a32(address(impl)), address(impl).codehash
            ),
            _upgrade(address(d.gate), address(impl), keccak256("another"), ""),
            _upgrade(address(d.gate), delegated, delegated.codehash, "")
        ];
        string[5] memory why = ["params", "params", "params", "codehash", "codehash"];
        uint64[5] memory ns;
        for (uint256 i = 0; i < 5; i++) {
            ns[i] = _decideKind(8, params[i]);
        }
        address eoa = makeAddr("an account");
        address funded = makeAddr("a funded account");
        vm.deal(funded, 1 ether);
        assertEq(funded.codehash, keccak256(""));
        uint64 none = _decideKind(8, _upgrade(address(d.gate), eoa, eoa.codehash, ""));
        uint64 empty = _decideKind(8, _upgrade(address(d.gate), funded, keccak256(""), ""));
        _wait(WEEK);
        address before = _impl(address(d.gate));
        for (uint256 i = 0; i < 5; i++) {
            _expectEnd(ns[i], 8, why[i]);
        }
        vm.expectRevert(abi.encodeWithSelector(NoCodeYet.selector, eoa));
        d.admin.execute(none);
        vm.expectRevert(abi.encodeWithSelector(NoCodeYet.selector, funded));
        d.admin.execute(empty);
        assertTrue(_pending(none) && _pending(empty), "no code: pending, never spent");
        assertEq(_impl(address(d.gate)), before);
    }

    /// A call that reverts reverts the execution: the decision pends, retried, until a cancel (ER7: an external
    /// call's failure never spends a decision).
    function test_kind8_aRevertingCallLeavesTheDecisionPending() public {
        GateV2 v2 = new GateV2();
        uint64 broken = _decideKind(
            8, _upgrade(address(d.gate), address(v2), address(v2).codehash, abi.encodeCall(GateV2.initializeBroken, ()))
        );
        _wait(WEEK);
        address before = _impl(address(d.gate));
        vm.expectRevert("broken initializer");
        d.admin.execute(broken);
        assertTrue(_pending(broken), "a reverting call leaves the decision pending");
        assertEq(_impl(address(d.gate)), before);
        _decideKind(1, _cancelOne(broken));
        assertFalse(_pending(broken));
    }

    /// No code yet (the review's finding 4): a matured kind 8 whose implementation is not deployed yet pends — a
    /// stranger's early execute reverts (`NoCodeYet`) and spends nothing, as a Solana kind 8 whose buffer is not yet
    /// staged pends — so nobody burns GovDAO's 7 days by executing before the code is there; once the implementation
    /// is deployed at the address the decision names, of the code hash it pins, anyone executes it.
    function test_kind8_anImplementationNotYetDeployedPends() public {
        address deployer = makeAddr("the implementation's deployer");
        address impl = vm.computeCreateAddress(deployer, vm.getNonce(deployer));
        // GateV2 keeps no immutables: its code hash is the same wherever it is deployed
        bytes32 hash = address(new GateV2()).codehash;
        uint64 n = _decideKind(8, _upgrade(address(d.gate), impl, hash, abi.encodeCall(GateV2.initializeV2, (42))));
        _wait(WEEK);
        address before = _impl(address(d.gate));
        vm.prank(makeAddr("a stranger"));
        vm.expectRevert(abi.encodeWithSelector(NoCodeYet.selector, impl));
        d.admin.execute(n);
        assertTrue(_pending(n), "no code yet: the decision pends");
        assertEq(_impl(address(d.gate)), before, "nothing upgraded");
        // a week on, the implementation is deployed where the decision names, and the decision runs
        _wait(WEEK);
        vm.prank(deployer);
        address deployed = address(new GateV2());
        assertEq(deployed, impl, "deployed at the address the decision names");
        _expectEnd(n, 8, "");
        assertEq(_impl(address(d.gate)), impl);
        assertEq(GateV2(address(d.gate)).v2(), 42);
    }

    /// An account's address pends a kind 8 the same way (it has no code); once its key delegates it (EIP-7702), its
    /// code is a delegation designator, which is spent even though the decision pinned the designator's own hash:
    /// the only code an address can gain but by a deployment, and one its key can re-point at will.
    function test_kind8_anAccountThatDelegatesLaterIsSpent() public {
        address eoa = makeAddr("an account");
        PausableGate impl = new PausableGate();
        bytes memory designator = abi.encodePacked(hex"ef0100", address(impl));
        uint64 n = _decideKind(8, _upgrade(address(d.gate), eoa, keccak256(designator), ""));
        _wait(WEEK);
        vm.expectRevert(abi.encodeWithSelector(NoCodeYet.selector, eoa));
        d.admin.execute(n);
        assertTrue(_pending(n), "no code: the decision pends");
        vm.etch(eoa, designator);
        assertEq(eoa.codehash, keccak256(designator));
        address before = _impl(address(d.gate));
        _expectEnd(n, 8, "codehash");
        assertEq(_impl(address(d.gate)), before);
    }

    // -------------------------------------------------------------------------------------------- kind 9 --

    function _successor(address predecessor) internal returns (HlAdmin) {
        return new HlAdmin(_successorConfig(predecessor));
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

    /// Kind 9 moves every authority — the ProxyAdmin, the mailbox, both tokens, the gate, both hooks, the client's
    /// admin — and writes the live number, the seats and watch slots and the hold history into the successor; the
    /// predecessor records nothing more (a decision to it stays deliverable), what pended there is spent, the
    /// successor's finishSetup passes, and decisions land on the successor from the next number.
    function test_kind9_handsEverythingToTheSuccessor() public {
        // an ended hold in the history, and a decision left pending
        _aflag(seat1);
        _aflag(seat2);
        _aclear(seat1);
        _aclear(seat2);
        _wait(DAY + 1);
        _aflag(watch1);
        _aclear(watch1);
        assertEq(d.admin.holds().length, 1);
        uint64 left = _decideKind(7, _fee(3, treasurer));
        HlAdmin succ = _successor(address(d.admin));
        assertEq(succ.predecessor(), address(d.admin));
        assertFalse(succ.inherited());
        uint64 n = _decideKind(9, _transfer(address(succ), address(succ).codehash));
        _wait(WEEK);
        vm.expectEmit(false, false, false, true, address(succ));
        emit IHlAdmin.Inherited(address(d.admin), n);
        _expectEnd(n, 9, "");
        address s = address(succ);
        assertEq(d.admin.successor(), s);
        assertEq(d.proxyAdmin.owner(), s);
        assertEq(d.mailbox.owner(), s);
        assertEq(d.token0.owner(), s);
        assertEq(d.token1.owner(), s);
        assertEq(d.gate.owner(), s);
        assertEq(d.merkleTreeHook.owner(), s);
        assertEq(d.protocolFee.owner(), s);
        assertEq(d.client.admin(), s);
        assertTrue(succ.inherited());
        assertEq(succ.lastSeq(), d.admin.lastSeq());
        address[6] memory ka = d.admin.keys();
        address[6] memory ks = succ.keys();
        for (uint256 i = 0; i < 6; i++) {
            assertEq(ks[i], ka[i]);
        }
        assertEq(succ.holds().length, 1);
        assertEq(succ.holds()[0].start, d.admin.holds()[0].start);
        // the predecessor is retired: a decision to it stays deliverable, what pended there is spent
        _stageBody(_body(7, nextSeq, _fee(1, treasurer)));
        vm.prank(relayer, relayer);
        vm.expectRevert(HlAdmin.RetiredAdmin.selector);
        d.mailbox.process(stagedMeta, stagedMsg);
        _expectEnd(left, 7, "retired");
        // the successor's setup check passes; decisions land on it from the next number
        succ.finishSetup();
        assertTrue(succ.setupDone());
        uint64 next = nextSeq++;
        _deliver(_message(HLGOV, s, _body(12, next, _limits(0, 5, 6))));
        assertEq(succ.lastSeq(), next);
        assertTrue(succ.decision(next).deliveredBlock != 0);
        _wait(WEEK);
        succ.execute(next);
        assertEq(d.gate.limits(0).maxPerMessage, 5, "the successor's authority");
        // and the predecessor can never be handed anything again
        vm.expectRevert(HlAdmin.NotInherited.selector);
        vm.prank(address(d.admin));
        succ.successorInit(1, ka, new IHlAdmin.Hold[](0));
    }

    /// Spent, nothing moved: an address with high bytes (it does not parse), a code hash that is not the
    /// successor's, the contract itself, an account with a delegation designator, a successor naming another
    /// predecessor, one that inherited already, one with a successor of its own, one at another clock factor.
    /// Pending (`NoCodeYet`), never spent: an account without code.
    function test_kind9_refusals() public {
        HlAdmin good = _successor(address(d.admin));
        address delegated = makeAddr("a delegated account");
        vm.etch(delegated, abi.encodePacked(hex"ef0100", address(good)));
        address[7] memory to = [
            address(good),
            address(d.admin),
            delegated,
            address(_successor(makeAddr("another predecessor"))),
            address(new MockSuccessor(address(d.admin), true, address(0), CLOCK_FACTOR, false, false)),
            address(new MockSuccessor(address(d.admin), false, makeAddr("x"), CLOCK_FACTOR, false, false)),
            address(new MockSuccessor(address(d.admin), false, address(0), CLOCK_FACTOR == 1 ? 8 : 1, false, false))
        ];
        string[7] memory why = ["codehash", "codehash", "codehash", "successor", "successor", "successor", "successor"];
        uint64[7] memory ns;
        for (uint256 i = 0; i < 7; i++) {
            ns[i] = _decideKind(9, _transfer(to[i], i == 0 ? keccak256("other code") : to[i].codehash));
        }
        uint64 high = _decideKind(
            9, abi.encodePacked(bytes32(uint256(1) << 160 | uint160(address(good))), address(good).codehash)
        );
        address eoa = makeAddr("an account");
        uint64 none = _decideKind(9, _transfer(eoa, eoa.codehash));
        _wait(WEEK);
        for (uint256 i = 0; i < 7; i++) {
            _expectEnd(ns[i], 9, why[i]);
        }
        _expectEnd(high, 9, "params");
        vm.expectRevert(abi.encodeWithSelector(NoCodeYet.selector, eoa));
        d.admin.execute(none);
        assertTrue(_pending(none), "no code: pending, never spent");
        assertEq(d.admin.successor(), address(0));
        assertEq(d.proxyAdmin.owner(), address(d.admin), "nothing moved");
        assertEq(d.client.admin(), address(d.admin));
    }

    /// A successor bound elsewhere (audit round 2, E2-RT3): HlAdmin's own reviewed code, deployed over any one binding
    /// that grants power and is not this HlAdmin's — another mailbox, client (the red team's: an ISM that verifies
    /// everything), gate, token, ProxyAdmin, hook, local domain, origin or r/hlgov — is spent ("bindings") and nothing
    /// moves. The decision's code hash pins those immutables, but nobody reading the hash can see them; the successor's
    /// views answer them, and each must be HlAdmin's own answer.
    function test_kind9_aSuccessorBoundElsewhereIsSpent() public {
        uint64[11] memory ns;
        for (uint256 i = 0; i < 11; i++) {
            HlAdmin s = new HlAdmin(_boundElsewhere(i));
            ns[i] = _decideKind(9, _transfer(address(s), address(s).codehash));
        }
        _wait(WEEK);
        for (uint256 i = 0; i < 11; i++) {
            _expectEnd(ns[i], 9, "bindings");
        }
        assertEq(d.admin.successor(), address(0));
        assertEq(d.proxyAdmin.owner(), address(d.admin), "nothing moved");
        assertEq(d.mailbox.owner(), address(d.admin));
        assertEq(d.client.admin(), address(d.admin));
        // bound as this HlAdmin is, a successor takes everything
        HlAdmin good = _successor(address(d.admin));
        uint64 n = _decideKind(9, _transfer(address(good), address(good).codehash));
        _wait(WEEK);
        _expectEnd(n, 9, "");
        assertEq(d.proxyAdmin.owner(), address(good));
    }

    /// A successor whose binding views revert (or are starved of gas) reverts the execution: the decision pends, never
    /// spent "bindings" (ER7: only an answer the successor's pinned code gives may spend it), and nothing moves.
    function test_kind9_aSuccessorWhoseBindingsRevertPends() public {
        SilentBindingsSuccessor silent = new SilentBindingsSuccessor(address(d.admin));
        uint64 n = _decideKind(9, _transfer(address(silent), address(silent).codehash));
        _wait(WEEK);
        vm.expectRevert("no binding");
        d.admin.execute(n);
        assertTrue(_pending(n), "the decision pends");
        assertEq(d.admin.successor(), address(0));
        assertEq(d.proxyAdmin.owner(), address(d.admin), "nothing moved");
    }

    /// A successor's configuration with binding `i` (of the eleven views kind 9 compares: the bindings that grant
    /// power) other than this HlAdmin's.
    function _boundElsewhere(uint256 i) internal returns (HlAdmin.Config memory c) {
        c = _successorConfig(address(d.admin));
        address x = makeAddr(string.concat("elsewhere ", vm.toString(i)));
        if (i == 0) {
            c.mailbox = x;
        } else if (i == 1) {
            c.client = address(new AlwaysTrueIsm());
        } else if (i == 2) {
            c.gate = x;
        } else if (i == 3) {
            c.token0 = x;
        } else if (i == 4) {
            c.token1 = x;
        } else if (i == 5) {
            c.proxyAdmin = x;
        } else if (i == 6) {
            c.merkleTreeHook = x;
        } else if (i == 7) {
            c.protocolFee = x;
        } else if (i == 8) {
            c.localDomain = LOCAL + 1;
        } else if (i == 9) {
            c.origin = GNO + 1;
        } else {
            c.hlgov = keccak256(abi.encode("elsewhere", i));
        }
    }

    /// The bindings that grant nothing are not compared (fold F8): HlAdmin's own reviewed code over another verifier,
    /// verifier code hash, hlmailbox path hash, route 0 or route 1 — one at a time, every other binding this
    /// HlAdmin's — takes the hand-over. Only the successor's own finishSetup reads those (checks 15 and 26–28), against
    /// the live client and tokens.
    function test_kind9_theSetupOnlyBindingsAreNotCompared() public {
        for (uint256 i = 0; i < 5; i++) {
            uint256 snap = vm.snapshotState();
            HlAdmin s = new HlAdmin(_setupOnlyElsewhere(i));
            uint64 n = _decideKind(9, _transfer(address(s), address(s).codehash));
            _wait(WEEK);
            _expectEnd(n, 9, "");
            assertEq(d.admin.successor(), address(s));
            assertEq(d.proxyAdmin.owner(), address(s), "every authority moved");
            assertEq(d.client.admin(), address(s));
            assertTrue(s.inherited());
            vm.revertToState(snap);
        }
    }

    /// The setup-only bindings grant nothing (fold F8): a successor naming a verifier that accepts every signature takes
    /// the hand-over, and the client still verifies with its own — a header signed by strangers' keys is refused
    /// (`SignatureMismatch`), a decision the chain's own validators signed runs on the successor — while the successor's
    /// own setup refuses the verifier it named (check 27: not the client's).
    function test_kind9_aSuccessorsOwnVerifierGrantsNothing() public {
        AlwaysTrueVerifier anything = new AlwaysTrueVerifier();
        HlAdmin.Config memory c = _successorConfig(address(d.admin));
        c.ed25519 = address(anything);
        c.ed25519CodeHash = address(anything).codehash;
        HlAdmin succ = new HlAdmin(c);
        uint64 n = _decideKind(9, _transfer(address(succ), address(succ).codehash));
        _wait(WEEK);
        _expectEnd(n, 9, "");
        _assertHandedTo(succ);
        assertEq(address(d.client.ED25519()), address(d.ed25519), "the client runs its own verifier");
        // the next header, signed by strangers' keys in the set's slots
        vm.roll(block.number + 1);
        IGnoLightClient.Validator[] memory v = _chainSet();
        TestChain.Header memory h = _header(chainHeight + 1, v, v, bytes32(0));
        bytes[] memory l = TestChain.leaves(h);
        IGnoLightClient.Validator[] memory strangers = TestChain.validators(99, v.length, 60);
        IGnoLightClient.Commit memory forged = TestChain.commit(h, TestChain.hash(l), strangers, TestChain.quorum(v), 0);
        IGnoLightClient.Validator[] memory none;
        vm.expectRevert(GnoLightClient.SignatureMismatch.selector);
        d.client.update(l, forged, v, none);
        // a decision the chain signed runs on the successor
        uint64 next = nextSeq++;
        _deliver(_message(HLGOV, address(succ), _body(12, next, _limits(0, 5, 6))));
        _wait(WEEK);
        succ.execute(next);
        assertEq(d.gate.limits(0).maxPerMessage, 5, "the successor's decisions run");
        vm.expectRevert(abi.encodeWithSelector(SetupIncomplete.selector, uint8(27)));
        succ.finishSetup();
    }

    /// A successor's configuration with setup-only binding `i` (0 the verifier, 1 its code hash, 2 the hlmailbox path
    /// hash, 3 route 0, 4 route 1) other than this HlAdmin's.
    function _setupOnlyElsewhere(uint256 i) internal returns (HlAdmin.Config memory c) {
        c = _successorConfig(address(d.admin));
        bytes32 y = keccak256(abi.encode("elsewhere", i));
        if (i == 0) {
            c.ed25519 = makeAddr("another verifier");
        } else if (i == 1) {
            c.ed25519CodeHash = y;
        } else if (i == 2) {
            c.mailboxPkgPathHash = y;
        } else if (i == 3) {
            c.route0 = y;
        } else {
            c.route1 = y;
        }
    }

    /// After a verifier swap (fold F8): a kind 8 moves the client onto a fixed verifier — other code, another address
    /// — and HlAdmin's own `ed25519` and `ed25519CodeHash` are stale from then on (immutables: no kind moves them). A
    /// successor bound to them takes the hand-over but can never finish its setup (check 27: the client's verifier is
    /// not its own); the one bound to the live verifier, the only one that can, was spent "bindings" before this fold.
    /// It takes every authority, its setup passes against the live client, and its decisions run.
    function test_kind9_aSuccessorAfterAVerifierSwapExecutes() public {
        Ed25519VerifierV2 v2 = new Ed25519VerifierV2();
        GnoLightClient impl = new GnoLightClient(_pkgPathHash(), v2);
        uint64 swap = _decideKind(8, _upgrade(address(d.client), address(impl), address(impl).codehash, ""));
        _wait(WEEK);
        _expectEnd(swap, 8, "");
        assertEq(address(d.client.ED25519()), address(v2), "the client runs the new verifier");
        assertEq(d.admin.ed25519(), address(d.ed25519), "HlAdmin's own verifier is stale");
        assertTrue(d.admin.ed25519CodeHash() != address(v2).codehash, "and its code hash");
        // bound to HlAdmin's stale verifier: the hand-over runs, and its setup can never finish
        uint256 snap = vm.snapshotState();
        HlAdmin stale = _successor(address(d.admin));
        uint64 s = _decideKind(9, _transfer(address(stale), address(stale).codehash));
        _wait(WEEK);
        _expectEnd(s, 9, "");
        vm.expectRevert(abi.encodeWithSelector(SetupIncomplete.selector, uint8(27)));
        stale.finishSetup();
        vm.revertToState(snap);
        // bound to the live verifier
        HlAdmin.Config memory c = _successorConfig(address(d.admin));
        c.ed25519 = address(v2);
        c.ed25519CodeHash = address(v2).codehash;
        HlAdmin succ = new HlAdmin(c);
        uint64 n = _decideKind(9, _transfer(address(succ), address(succ).codehash));
        _wait(WEEK);
        _expectEnd(n, 9, "");
        _assertHandedTo(succ);
        succ.finishSetup();
        assertTrue(succ.setupDone(), "its setup passes against the live client");
        uint64 next = nextSeq++;
        _deliver(_message(HLGOV, address(succ), _body(12, next, _limits(0, 5, 6))));
        _wait(WEEK);
        succ.execute(next);
        assertEq(d.gate.limits(0).maxPerMessage, 5, "the successor's decisions run, proven through the new verifier");
    }

    /// After a path change (fold F8): a kind 8 moves the client onto another hlmailbox path, and HlAdmin's own
    /// `mailboxPkgPathHash` is stale. A successor bound to it takes the hand-over but can never finish its setup (check
    /// 26); one bound to the live path — decided before the move, as both are (proofs name the new path from then on) —
    /// was spent "bindings"; it takes every authority and its setup passes.
    function test_kind9_aSuccessorAfterAPathChangeExecutes() public {
        bytes32 path2 = keccak256("gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox2");
        GnoLightClient impl = new GnoLightClient(path2, d.ed25519);
        HlAdmin stale = _successor(address(d.admin));
        HlAdmin.Config memory c = _successorConfig(address(d.admin));
        c.mailboxPkgPathHash = path2;
        HlAdmin succ = new HlAdmin(c);
        uint64 move = _decideKind(8, _upgrade(address(d.client), address(impl), address(impl).codehash, ""));
        uint64 s = _decideKind(9, _transfer(address(stale), address(stale).codehash));
        uint64 n = _decideKind(9, _transfer(address(succ), address(succ).codehash));
        _wait(WEEK);
        _expectEnd(move, 8, "");
        assertEq(d.client.mailboxPkgPathHash(), path2, "the client reads the new path");
        assertEq(d.admin.mailboxPkgPathHash(), _pkgPathHash(), "HlAdmin's own path hash is stale");
        uint256 snap = vm.snapshotState();
        _expectEnd(s, 9, "");
        vm.expectRevert(abi.encodeWithSelector(SetupIncomplete.selector, uint8(26)));
        stale.finishSetup();
        vm.revertToState(snap);
        _expectEnd(n, 9, "");
        _assertHandedTo(succ);
        succ.finishSetup();
        assertTrue(succ.setupDone(), "its setup passes against the live client");
    }

    /// After a route moved (fold F8): a kind 6 enrolls another route realm for gno.land on token 0, and HlAdmin's own
    /// `route(0)` is stale. A successor bound to it takes the hand-over but can never finish its setup (check 15); one
    /// bound to the live route was spent "bindings"; it takes every authority and its setup passes.
    function test_kind9_aSuccessorAfterARouteMovedExecutes() public {
        bytes32 route2 = _a32(makeAddr("the GNOT route's next realm"));
        uint64 move = _decideKind(6, _router(0, GNO, route2));
        _wait(WEEK);
        _expectEnd(move, 6, "");
        assertEq(d.token0.routers(GNO), route2, "token 0 takes the new route");
        assertEq(d.admin.route(0), ROUTE0, "HlAdmin's own route is stale");
        uint256 snap = vm.snapshotState();
        HlAdmin stale = _successor(address(d.admin));
        uint64 s = _decideKind(9, _transfer(address(stale), address(stale).codehash));
        _wait(WEEK);
        _expectEnd(s, 9, "");
        vm.expectRevert(abi.encodeWithSelector(SetupIncomplete.selector, uint8(15)));
        stale.finishSetup();
        vm.revertToState(snap);
        HlAdmin.Config memory c = _successorConfig(address(d.admin));
        c.route0 = route2;
        HlAdmin succ = new HlAdmin(c);
        uint64 n = _decideKind(9, _transfer(address(succ), address(succ).codehash));
        _wait(WEEK);
        _expectEnd(n, 9, "");
        _assertHandedTo(succ);
        succ.finishSetup();
        assertTrue(succ.setupDone(), "its setup passes against the live token");
    }

    /// Every authority is the successor's, and it inherited.
    function _assertHandedTo(HlAdmin succ) internal view {
        address s = address(succ);
        assertEq(d.admin.successor(), s);
        assertEq(d.proxyAdmin.owner(), s);
        assertEq(d.mailbox.owner(), s);
        assertEq(d.token0.owner(), s);
        assertEq(d.token1.owner(), s);
        assertEq(d.gate.owner(), s);
        assertEq(d.merkleTreeHook.owner(), s);
        assertEq(d.protocolFee.owner(), s);
        assertEq(d.client.admin(), s);
        assertTrue(succ.inherited());
        assertEq(succ.lastSeq(), d.admin.lastSeq());
    }

    /// A successor whose answers revert, or whose successorInit reverts, reverts the execution: the decision pends
    /// (ER7: a stranger who starves an inner call cannot spend GovDAO's decision), and nothing moved.
    function test_kind9_aRevertingSuccessorLeavesTheDecisionPending() public {
        MockSuccessor mute = new MockSuccessor(address(d.admin), false, address(0), CLOCK_FACTOR, true, false);
        MockSuccessor refuses = new MockSuccessor(address(d.admin), false, address(0), CLOCK_FACTOR, false, true);
        uint64 m = _decideKind(9, _transfer(address(mute), address(mute).codehash));
        uint64 r = _decideKind(9, _transfer(address(refuses), address(refuses).codehash));
        _wait(WEEK);
        vm.expectRevert("no answer");
        d.admin.execute(m);
        assertTrue(_pending(m));
        vm.expectRevert("no inheritance");
        d.admin.execute(r);
        assertTrue(_pending(r));
        assertEq(d.admin.successor(), address(0));
        assertEq(d.proxyAdmin.owner(), address(d.admin), "nothing moved");
        assertEq(d.gate.owner(), address(d.admin));
    }

    /// No code yet for kind 9 too: a matured hand-over to a successor not deployed yet pends (a stranger's early
    /// execute reverts, `NoCodeYet`, and nothing moves); once the successor is deployed at the address the decision
    /// names, of the code hash it pins, anyone executes it.
    function test_kind9_aSuccessorNotYetDeployedPends() public {
        address deployer = makeAddr("the successor's deployer");
        address at = vm.computeCreateAddress(deployer, vm.getNonce(deployer));
        HlAdmin.Config memory c = _successorConfig(address(d.admin));
        // the code hash as the proposer computes it before the vote (a successor's immutables name the HlAdminExec it
        // creates, whose address follows from its own): deployed in a snapshot, read, and the snapshot undone
        uint256 snap = vm.snapshotState();
        vm.prank(deployer);
        bytes32 hash = address(new HlAdmin(c)).codehash;
        vm.revertToState(snap);
        assertEq(at.code.length, 0);
        uint64 n = _decideKind(9, _transfer(at, hash));
        _wait(WEEK);
        vm.prank(makeAddr("a stranger"));
        vm.expectRevert(abi.encodeWithSelector(NoCodeYet.selector, at));
        d.admin.execute(n);
        assertTrue(_pending(n), "no successor yet: the decision pends");
        assertEq(d.admin.successor(), address(0));
        assertEq(d.proxyAdmin.owner(), address(d.admin), "nothing moved");
        // the successor is deployed where the decision names, and the hand-over runs
        vm.prank(deployer);
        HlAdmin succ = new HlAdmin(c);
        assertEq(address(succ), at, "deployed at the address the decision names");
        _expectEnd(n, 9, "");
        assertEq(d.admin.successor(), at);
        assertEq(d.proxyAdmin.owner(), at);
        assertEq(d.client.admin(), at);
        assertTrue(succ.inherited());
    }

    /// A successor runs nothing before it inherited: it records no decision, executes none, and only its
    /// predecessor writes its inheritance, once; its constructor takes no seats, slots or number.
    function test_aSuccessorRunsNothingBeforeItInherited() public {
        HlAdmin succ = _successor(address(d.admin));
        vm.prank(address(d.mailbox));
        vm.expectRevert(HlAdmin.NotInherited.selector);
        succ.handle(GNO, HLGOV, _body(7, 1, _fee(1, treasurer)));
        vm.expectRevert(HlAdmin.NotInherited.selector);
        succ.execute(1);
        address[6] memory k = d.admin.keys();
        vm.expectRevert(HlAdmin.WrongAuthority.selector);
        succ.successorInit(5, k, new IHlAdmin.Hold[](0));
        vm.prank(address(d.admin));
        vm.expectRevert(HlAdmin.WrongDecision.selector);
        succ.successorInit(5, k, new IHlAdmin.Hold[](9));
        vm.prank(address(d.admin));
        succ.successorInit(5, k, new IHlAdmin.Hold[](0));
        assertTrue(succ.inherited());
        assertEq(succ.lastSeq(), 5);
        vm.prank(address(d.admin));
        vm.expectRevert(HlAdmin.NotInherited.selector);
        succ.successorInit(6, k, new IHlAdmin.Hold[](0));
        // the launch HlAdmin is no successor: successorInit is not its
        vm.prank(address(d.admin));
        vm.expectRevert(HlAdmin.NotInherited.selector);
        d.admin.successorInit(5, k, new IHlAdmin.Hold[](0));
        // a successor's constructor takes no seats, watch slots or number
        HlAdmin.Config memory c = _successorConfig(address(d.admin));
        c.seats[0] = seat1;
        vm.expectRevert(Guardians.BadSeat.selector);
        new HlAdmin(c);
        c.seats[0] = address(0);
        c.startSequence = 3;
        vm.expectRevert(HlAdmin.OutOfOrder.selector);
        new HlAdmin(c);
    }

    /// The constructor refuses a zero contract, verifier, r/hlgov or route (ZeroAddress) and a zero verifier code
    /// hash, local domain or origin (ZeroSetting): settings finishSetup would compare against nothing (the review's
    /// finding 2).
    function test_theConstructorRefusesZeroSettings() public {
        for (uint256 i = 0; i < 6; i++) {
            HlAdmin.Config memory c = _successorConfig(address(d.admin));
            bytes4 want = HlAdmin.ZeroAddress.selector;
            if (i == 0) {
                c.ed25519 = address(0);
            } else if (i == 1) {
                c.route0 = bytes32(0);
            } else if (i == 2) {
                c.route1 = bytes32(0);
            } else if (i == 3) {
                (c.ed25519CodeHash, want) = (bytes32(0), HlAdmin.ZeroSetting.selector);
            } else if (i == 4) {
                (c.localDomain, want) = (0, HlAdmin.ZeroSetting.selector);
            } else {
                (c.origin, want) = (0, HlAdmin.ZeroSetting.selector);
            }
            vm.expectRevert(want);
            new HlAdmin(c);
        }
        new HlAdmin(_successorConfig(address(d.admin)));
    }

    /// A successor's finishSetup reads no launch value: kinds 7 and 12 moved the protocol fee and a mint limit
    /// before the hand-over, and the successor's checks still pass (checks 30 and 31 are a launch's).
    function test_kind9_aSuccessorsSetupSkipsTheLaunchValues() public {
        uint64 f = _decideKind(7, _fee(5, treasurer));
        uint64 l = _decideKind(12, _limits(1, 9, 99));
        _wait(WEEK);
        _expectEnd(f, 7, "");
        _expectEnd(l, 12, "");
        HlAdmin succ = _successor(address(d.admin));
        uint64 n = _decideKind(9, _transfer(address(succ), address(succ).codehash));
        _wait(WEEK);
        _expectEnd(n, 9, "");
        succ.finishSetup();
        assertTrue(succ.setupDone());
    }

    /// A successor's finishSetup over a live bridge (audit round 3, E3-CF10; F8.6 item 1): both tokens hold a supply
    /// minted through the bridge, and the successor's checks pass — a token's supply 0 (check 24) is a launch's alone, as
    /// the gate's launch limits and the fee are. Before, every successor's setup failed for good at 24 once a token was
    /// minted, so the one on-chain reading of its verifier, path and routes (15, 26–28) was dead.
    function test_kind9_aSuccessorsSetupPassesOverASupply() public {
        _mint(0, makeAddr("alice"), 77);
        _mint(1, makeAddr("bob"), 5);
        assertEq(d.token0.totalSupply(), 77);
        assertEq(d.token1.totalSupply(), 5);
        HlAdmin succ = _successor(address(d.admin));
        uint64 n = _decideKind(9, _transfer(address(succ), address(succ).codehash));
        _wait(WEEK);
        _expectEnd(n, 9, "");
        succ.finishSetup();
        assertTrue(succ.setupDone(), "a successor's setup reads no supply");
    }

    /// After a kind 3 to zero (audit round 3, E3-CF10): a token's ISM may be the gate or zero — kind 3's two values; zero
    /// falls back to the mailbox's default ISM, the gate (check 9) — and a successor's check 12 takes either, so its
    /// setup passes. Before, it failed at 12 until a kind 3 set the gate back. (A launch's 12 is the gate alone:
    /// HlAdminSetupTest.)
    function test_kind9_aSuccessorsSetupPassesAfterAKind3ToZero() public {
        uint64 z = _decideKind(3, _tokenIsm(0, address(0)));
        _wait(WEEK);
        _expectEnd(z, 3, "");
        assertEq(address(d.token0.interchainSecurityModule()), address(0));
        address alice = makeAddr("alice");
        _mint(0, alice, 9);
        assertEq(d.token0.balanceOf(alice), 9, "through the mailbox's default ISM, the gate");
        HlAdmin succ = _successor(address(d.admin));
        uint64 n = _decideKind(9, _transfer(address(succ), address(succ).codehash));
        _wait(WEEK);
        _expectEnd(n, 9, "");
        succ.finishSetup();
        assertTrue(succ.setupDone(), "a successor's 12 takes the gate or zero");
    }

    /// HlAdminExec runs only as HlAdmin, by its delegatecall.
    function test_theExecutorRunsOnlyAsHlAdmin() public {
        HlAdminExec x = d.admin.exec();
        vm.expectRevert(NotDelegated.selector);
        x.run(7, _fee(1, treasurer), 1);
        vm.expectRevert(NotDelegated.selector);
        x.checkSetup();
    }
}

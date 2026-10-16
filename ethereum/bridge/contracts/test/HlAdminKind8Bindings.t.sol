// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {ProxyAdmin} from "@openzeppelin/contracts/proxy/transparent/ProxyAdmin.sol";
import {ITransparentUpgradeableProxy} from "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol";
import {Mailbox} from "@hyperlane-xyz/core/contracts/Mailbox.sol";
import {IInterchainSecurityModule} from "@hyperlane-xyz/core/contracts/interfaces/IInterchainSecurityModule.sol";
import {IPostDispatchHook} from "@hyperlane-xyz/core/contracts/interfaces/hooks/IPostDispatchHook.sol";
import {HypERC20} from "@hyperlane-xyz/core/contracts/token/HypERC20.sol";

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {GnoLightClient} from "../src/GnoLightClient.sol";
import {PausableGate} from "../src/PausableGate.sol";
import {AuthorityLost} from "../src/admin/HlAdminStorage.sol";
import {IHlAdmin} from "../src/interfaces/IHlAdmin.sol";
import {AdminBase} from "./helpers/AdminBase.sol";
import {TestChain} from "./helpers/TestChain.sol";

interface IRecipientLike {
    function handle(uint32 origin, bytes32 sender, bytes calldata message) external payable;
}

/// The auditor's stand-in "mailbox" (audit round 3, E3-CT3): what HypERC20's constructor reads of a mailbox, and a
/// `handle` call into a token as that mailbox.
contract StandInMailbox {
    uint32 public immutable localDomain;

    constructor(uint32 d) {
        localDomain = d;
    }

    function mint(address token, uint32 origin, bytes32 router, address to, uint256 amount) external {
        IRecipientLike(token).handle(origin, router, TestChain.tokenBody(to, amount));
    }
}

/// Hyperlane's HypERC20 over our mailbox, with initializers that write what finishSetup held at the launch and no
/// decision moves: the ISM, the hook, any slot (the fee recipient's and the fee hook's live at fixed slots).
contract TokenPoke is HypERC20 {
    constructor(address mailbox_) HypERC20(6, 1, 1, mailbox_) {}

    function setIsmRaw(address a) external {
        _interchainSecurityModule = IInterchainSecurityModule(a);
    }

    function setHookRaw(address a) external {
        hook = IPostDispatchHook(a);
    }

    function poke(bytes32 slot, bytes32 value) external {
        assembly ("memory-safe") {
            sstore(slot, value)
        }
    }
}

/// Hyperlane's Mailbox with initializers that re-point its default ISM or hooks.
contract MailboxPoke is Mailbox {
    constructor(uint32 d) Mailbox(d) {}

    function setDefaultIsmRaw(address a) external {
        defaultIsm = IInterchainSecurityModule(a);
    }

    function setDefaultHookRaw(address a) external {
        defaultHook = IPostDispatchHook(a);
    }

    function setRequiredHookRaw(address a) external {
        requiredHook = IPostDispatchHook(a);
    }
}

/// The gate with an initializer that writes any slot of its storage (its client, a guardian's key).
contract GatePoke is PausableGate {
    function poke(bytes32 slot, bytes32 value) external {
        assembly ("memory-safe") {
            sstore(slot, value)
        }
    }
}

/// A gate implementation that answers every view kind 8 reads as the deployment's gate did when it was built (read
/// then: once it runs behind the proxy, the gate's answers are its own), at another clock factor.
contract GateAtAnotherFactor {
    address internal immutable adm;
    address internal immutable mb;
    address internal immutable cl;
    address internal immutable t0;
    address internal immutable t1;
    // the keys, one immutable each (behind the proxy this code's storage is the proxy's)
    address internal immutable k0;
    address internal immutable k1;
    address internal immutable k2;
    address internal immutable k3;
    address internal immutable k4;
    address internal immutable k5;

    constructor(address adm_, PausableGate live) {
        (adm, mb, cl, t0, t1) = (adm_, live.mailbox(), live.client(), live.token(0), live.token(1));
        address[6] memory k = live.keys();
        (k0, k1, k2, k3, k4, k5) = (k[0], k[1], k[2], k[3], k[4], k[5]);
    }

    function owner() external view returns (address) {
        return adm;
    }

    function mailbox() external view returns (address) {
        return mb;
    }

    function client() external view returns (address) {
        return cl;
    }

    function token(uint8 i) external view returns (address) {
        return i == 0 ? t0 : t1;
    }

    function keys() external view returns (address[6] memory) {
        return [k0, k1, k2, k3, k4, k5];
    }

    function clockFactor() external pure returns (uint64) {
        return CLOCK_FACTOR == 1 ? 8 : 1;
    }
}

/// A client implementation that answers the views kind 8 reads: its admin HlAdmin, a clock factor, an origin domain.
contract ClientViews {
    address internal immutable adm;
    uint64 internal immutable factor;
    uint32 internal immutable origin;

    constructor(address adm_, uint64 factor_, uint32 origin_) {
        (adm, factor, origin) = (adm_, factor_, origin_);
    }

    function admin() external view returns (address) {
        return adm;
    }

    function clockFactor() external view returns (uint64) {
        return factor;
    }

    function originDomain() external view returns (uint32) {
        return origin;
    }
}

/// A gate version whose initializer branches on the gas it is given (audit round 3, E3-CT4): what a reviewed V2 could
/// do, knowingly or not — a stranger who executes the kind 8 with just enough gas would pick its branch.
contract GateGasProbe is PausableGate {
    uint256 public seen;
    bool public degraded;

    function probe() external {
        seen = gasleft();
        degraded = gasleft() < 1_500_000;
    }
}

/// A stranger's executor (audit round 4, E4-CT2): one transaction that, when `warm`, first reads what the upgrade then
/// touches — the ProxyAdmin's owner, the proxy's admin and implementation (EIP-2929: their accounts and slots are warm
/// from then on, as an EIP-2930 access list makes them) — then executes.
contract WarmingExecutor {
    function run(IHlAdmin admin, uint64 n, ProxyAdmin pa, address proxy, bool warm) external {
        if (warm) {
            pa.owner();
            pa.getProxyAdmin(ITransparentUpgradeableProxy(proxy));
            pa.getProxyImplementation(ITransparentUpgradeableProxy(proxy));
        }
        admin.execute(n);
    }
}

/// Kind 8 holds, after the upgrade and its call, every binding finishSetup held at the launch that no decision moves,
/// for the proxy upgraded (audit round 3, E3-CT3): the mailbox's default ISM and hooks (9–11) and local domain (29); a
/// token's ISM (12: the gate, or zero — kind 3's two values), hook and fees (13–14), mailbox, scale and decimals
/// (21–23: immutables of the implementation, which no code hash shows) and local domain (29); the gate's mailbox,
/// client and tokens (17), its guardian list (18: kind 5 moves it with HlAdmin's) and clock factor (19); the client's
/// clock factor (19) and origin (20). An upgrade that breaks one reverts (`AuthorityLost`: the proxy left the
/// deployment's hands) and the decision pends for a cancel, as the authority checks do. And the call is forwarded a
/// fixed budget (E3-CT4): whatever gas the executor sends, the call gets the same, or the execution reverts
/// (`CallStarved`) and pends — though an executor can still add to what its code runs on (a warm set, E4-CT2; under
/// EIP-8037 the transaction's state-gas reservoir, E4-CT3: HlAdminExec.CALL_GAS's note).
contract HlAdminKind8BindingsTest is AdminBase {
    address internal attacker = makeAddr("attacker");

    function setUp() public {
        _world();
    }

    function _impl(address proxy) internal view returns (address) {
        return d.proxyAdmin.getProxyImplementation(ITransparentUpgradeableProxy(proxy));
    }

    /// The first slot of `proxy`'s storage (0–399) that holds `value`.
    function _slotOf(address proxy, address value) internal view returns (bytes32) {
        for (uint256 i = 0; i < 400; i++) {
            if (vm.load(proxy, bytes32(i)) == bytes32(uint256(uint160(value)))) {
                return bytes32(i);
            }
        }
        revert("no slot holds it");
    }

    /// Decides the kind 8 (`proxy` to `impl`, with `call`), lets it mature, and expects its execution to revert
    /// `AuthorityLost(proxy)` and the decision to pend with the proxy on its old implementation; undone after.
    function _refused(address proxy, address impl, bytes memory call, string memory what) internal {
        uint256 snap = vm.snapshotState();
        uint64 n = _decideKind(8, _upgrade(proxy, impl, impl.codehash, call));
        _wait(WEEK);
        address before = _impl(proxy);
        vm.prank(makeAddr("an executor"));
        vm.expectRevert(abi.encodeWithSelector(AuthorityLost.selector, proxy));
        d.admin.execute(n);
        assertTrue(_pending(n), string.concat(what, ": the decision pends"));
        assertEq(_impl(proxy), before, string.concat(what, ": nothing upgraded"));
        vm.revertToState(snap);
    }

    /// The auditor's case: token 0 on Hyperlane's own HypERC20 source, built over a stand-in "mailbox". The code matches
    /// the release with the immutables masked; owner, admin and implementation stay where they were. Before this fold
    /// the upgrade executed, and the stand-in then minted 1e30 to the attacker past the gate, a flag standing and every
    /// limit. It reverts now, and the stand-in mints nothing.
    function test_kind8_aTokenOverAnotherMailboxIsRefused() public {
        StandInMailbox fake = new StandInMailbox(LOCAL);
        HypERC20 impl = new HypERC20(6, 1, 1, address(fake));
        _refused(address(d.token0), address(impl), "", "a token over a stand-in mailbox");
        uint64 n = _decideKind(8, _upgrade(address(d.token0), address(impl), address(impl).codehash, ""));
        _wait(WEEK);
        vm.expectRevert(abi.encodeWithSelector(AuthorityLost.selector, address(d.token0)));
        d.admin.execute(n);
        assertEq(address(d.token0.mailbox()), address(d.mailbox), "the token answers our mailbox");
        _gflag(seat1);
        vm.expectRevert("MailboxClient: sender not mailbox");
        fake.mint(address(d.token0), GNO, ROUTE0, attacker, 1e30);
        assertEq(d.token0.balanceOf(attacker), 0, "nothing minted");
    }

    /// A token's every fixed binding, one at a time (12–14 by the upgrade's call, 21–23 and 29 by the implementation's
    /// immutables); each reverts and pends.
    function test_kind8_aTokenThatMovesAFixedBindingIsRefused() public {
        address token = address(d.token1);
        TokenPoke poke = new TokenPoke(address(d.mailbox));
        _refused(token, address(poke), abi.encodeCall(TokenPoke.setIsmRaw, (address(d.client))), "12 the ISM");
        _refused(token, address(poke), abi.encodeCall(TokenPoke.setHookRaw, (address(d.merkleTreeHook))), "13 a hook");
        _refused(
            token,
            address(poke),
            abi.encodeCall(
                TokenPoke.poke,
                (keccak256("FungibleTokenRouter.feeRecipient"), bytes32(uint256(uint160(makeAddr("fee recipient")))))
            ),
            "14 a fee recipient"
        );
        _refused(
            token,
            address(poke),
            abi.encodeCall(
                TokenPoke.poke, (keccak256("TokenRouter.feeHook"), bytes32(uint256(uint160(address(d.protocolFee)))))
            ),
            "14 a fee hook"
        );
        _refused(token, address(new HypERC20(6, 1, 1, address(new Mailbox(LOCAL)))), "", "21 another mailbox");
        _refused(token, address(new HypERC20(6, 2, 1, address(d.mailbox))), "", "22 another scale");
        _refused(token, address(new HypERC20(6, 1, 2, address(d.mailbox))), "", "22 another scale's denominator");
        _refused(token, address(new HypERC20(18, 1, 1, address(d.mailbox))), "", "23 other decimals");
        // our mailbox, read at construction as another domain: the token's local domain immutable says it
        vm.mockCall(address(d.mailbox), abi.encodeWithSignature("localDomain()"), abi.encode(LOCAL + 1));
        HypERC20 otherDomain = new HypERC20(6, 1, 1, address(d.mailbox));
        vm.clearMockedCalls();
        assertEq(otherDomain.localDomain(), LOCAL + 1);
        assertEq(address(otherDomain.mailbox()), address(d.mailbox));
        _refused(token, address(otherDomain), "", "29 another local domain");
    }

    /// The mailbox's: its default ISM and both hooks re-pointed by the call (9–11), its local domain (29).
    function test_kind8_aMailboxThatMovesAFixedBindingIsRefused() public {
        address mb = address(d.mailbox);
        MailboxPoke poke = new MailboxPoke(LOCAL);
        _refused(mb, address(poke), abi.encodeCall(MailboxPoke.setDefaultIsmRaw, (address(d.client))), "9 the ISM");
        _refused(
            mb, address(poke), abi.encodeCall(MailboxPoke.setDefaultHookRaw, (address(d.protocolFee))), "10 the hook"
        );
        _refused(
            mb,
            address(poke),
            abi.encodeCall(MailboxPoke.setRequiredHookRaw, (address(d.merkleTreeHook))),
            "11 the required hook"
        );
        _refused(mb, address(new Mailbox(LOCAL + 1)), "", "29 another local domain");
    }

    /// The gate's: its client re-pointed by the call (17), a guardian's key changed by it (18: only kind 5 moves the
    /// list, HlAdmin's with it), another clock factor (19).
    function test_kind8_aGateThatMovesAFixedBindingIsRefused() public {
        address gate = address(d.gate);
        GatePoke poke = new GatePoke();
        bytes32 seatSlot = _slotOf(gate, seat1);
        address[4] memory bound = [address(d.mailbox), address(d.client), address(d.token0), address(d.token1)];
        for (uint256 i = 0; i < 4; i++) {
            _refused(
                gate,
                address(poke),
                abi.encodeCall(
                    GatePoke.poke, (_slotOf(gate, bound[i]), bytes32(uint256(uint160(makeAddr("another binding")))))
                ),
                "17 another mailbox, client or token"
            );
        }
        _refused(
            gate,
            address(poke),
            abi.encodeCall(GatePoke.poke, (seatSlot, bytes32(uint256(uint160(attacker))))),
            "18 another key"
        );
        _refused(gate, address(new GateAtAnotherFactor(address(d.admin), d.gate)), "", "19 another clock factor");
    }

    /// The client's: another clock factor (19), another origin domain (20).
    function test_kind8_aClientThatMovesAFixedBindingIsRefused() public {
        address cl = address(d.client);
        _refused(
            cl, address(new ClientViews(address(d.admin), CLOCK_FACTOR == 1 ? 8 : 1, GNO)), "", "19 another factor"
        );
        _refused(cl, address(new ClientViews(address(d.admin), CLOCK_FACTOR, GNO + 1)), "", "20 another origin");
    }

    /// What the checks take: each proxy upgraded to a version that keeps every fixed binding executes (kind 8's own
    /// cases: HlAdminHandoverTest), a token whose ISM a kind 3 set to zero among them (kind 3's other value), and a
    /// client over another verifier and path (26–28 move by kind 8: a verifier swap, a path change).
    function test_kind8_keepingTheFixedBindingsExecutes() public {
        uint64 z = _decideKind(3, _tokenIsm(0, address(0)));
        _wait(WEEK);
        _expectEnd(z, 3, "");
        TokenPoke poke = new TokenPoke(address(d.mailbox));
        GnoLightClient swapped = new GnoLightClient(keccak256("another path"), d.ed25519);
        uint64 a = _decideKind(8, _upgrade(address(d.token0), address(poke), address(poke).codehash, ""));
        uint64 b = _decideKind(
            8,
            _upgrade(
                address(d.token1),
                address(poke),
                address(poke).codehash,
                abi.encodeCall(TokenPoke.poke, (bytes32(uint256(999)), bytes32(uint256(1))))
            )
        );
        uint64 c = _decideKind(8, _upgrade(address(d.client), address(swapped), address(swapped).codehash, ""));
        _wait(WEEK);
        _expectEnd(a, 8, "");
        _expectEnd(b, 8, "");
        _expectEnd(c, 8, "");
        assertEq(_impl(address(d.token0)), address(poke));
        assertEq(address(d.token0.interchainSecurityModule()), address(0), "kind 3's zero kept");
        assertEq(_impl(address(d.client)), address(swapped));
    }

    // -------------------------------------------------------------------------------- the call's budget --

    function _probeDecision() internal returns (uint64 n) {
        GateGasProbe v2 = new GateGasProbe();
        n = _decideKind(
            8, _upgrade(address(d.gate), address(v2), address(v2).codehash, abi.encodeCall(GateGasProbe.probe, ()))
        );
        _wait(WEEK);
    }

    /// With whatever gas the executor sends above the floor, the call gets the same budget: the new code's initializer
    /// reads the same `gasleft()` at 3M and at 12M (E3-CT4). Before, the proxy forwarded all but a 64th of what the
    /// executor left, so the two differed.
    function test_kind8_theCallRunsOnAFixedBudget() public {
        uint64 n = _probeDecision();
        uint256 snap = vm.snapshotState();
        d.admin.execute{gas: 3_000_000}(n);
        uint256 seenLow = GateGasProbe(address(d.gate)).seen();
        assertFalse(GateGasProbe(address(d.gate)).degraded());
        vm.revertToState(snap);
        d.admin.execute{gas: 12_000_000}(n);
        uint256 seenHigh = GateGasProbe(address(d.gate)).seen();
        assertEq(seenLow, seenHigh, "the call's gas is the decision's, not the executor's");
        assertGt(seenLow, 1_500_000, "and enough for any initializer a member reviewed");
        // at the edge: the least gas an execution succeeds with still gives the call its whole budget (the floor
        // covers the 64th EIP-150 keeps back and what runs between the check and the call)
        vm.revertToState(snap);
        uint256 lo = 1_000_000;
        uint256 hi = 3_000_000;
        while (hi - lo > 1) {
            uint256 mid = (lo + hi) / 2;
            uint256 s = vm.snapshotState();
            (bool ok,) = address(d.admin).call{gas: mid}(abi.encodeWithSignature("execute(uint64)", n));
            vm.revertToState(s);
            if (ok) {
                hi = mid;
            } else {
                lo = mid;
            }
        }
        d.admin.execute{gas: hi}(n);
        assertEq(GateGasProbe(address(d.gate)).seen(), seenHigh, "the least gas that executes: the same budget");
    }

    /// A stranger who executes with just enough gas for the rest no longer picks the call's branch: below the floor the
    /// execution reverts (`CallStarved`) and the decision pends (ER7); with enough, the call runs as reviewed. Before,
    /// the 1.3M-gas execution ran the initializer's degraded branch and the decision was executed, not pending.
    function test_kind8_aStarvedCallRevertsAndPends() public {
        uint64 n = _probeDecision();
        address before = _impl(address(d.gate));
        vm.prank(attacker);
        vm.expectRevert(abi.encodeWithSignature("CallStarved()"));
        d.admin.execute{gas: 1_300_000}(n);
        assertTrue(_pending(n), "the decision pends");
        assertEq(_impl(address(d.gate)), before, "nothing upgraded");
        d.admin.execute(n);
        assertFalse(_pending(n));
        assertFalse(GateGasProbe(address(d.gate)).degraded(), "the reviewed branch ran");
    }

    /// What the budget leaves to the executor (audit round 4, E4-CT2): its warm set. Through a stranger's contract the
    /// initializer reads the same `gasleft()` at 3M as at 12M (E3-CT4 holds), and never less than with nothing warm;
    /// an executor that reads the ProxyAdmin's owner and the proxy's admin and implementation first leaves it a few
    /// thousand more. So the budget alone does not make an initializer's outcome the same for every executor: one
    /// that branches on `gasleft()` is the members' review's to refuse (PLAN.md §8).
    function test_kind8_theExecutorsWarmSetAddsAFewThousand() public {
        uint64 n = _probeDecision();
        WarmingExecutor x = new WarmingExecutor();
        uint256 snap = vm.snapshotState();
        x.run{gas: 3_000_000}(d.admin, n, d.proxyAdmin, address(d.gate), false);
        uint256 cold = GateGasProbe(address(d.gate)).seen();
        vm.revertToState(snap);
        x.run{gas: 12_000_000}(d.admin, n, d.proxyAdmin, address(d.gate), false);
        assertEq(GateGasProbe(address(d.gate)).seen(), cold, "the gas limit does not move it");
        vm.revertToState(snap);
        x.run{gas: 3_000_000}(d.admin, n, d.proxyAdmin, address(d.gate), true);
        uint256 warm = GateGasProbe(address(d.gate)).seen();
        emit log_named_uint("the initializer's gas, nothing warm", cold);
        emit log_named_uint("the initializer's gas, the path warmed first", warm);
        assertGt(warm, cold, "the warm set adds");
        assertLt(warm - cold, 20_000, "a few thousand");
    }
}

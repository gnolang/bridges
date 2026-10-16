// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {IMailbox} from "@hyperlane-xyz/core/contracts/interfaces/IMailbox.sol";
import {TokenRouter} from "@hyperlane-xyz/core/contracts/token/libs/TokenRouter.sol";

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {BridgeSetup} from "../script/Setup.s.sol";
import {Ed25519Verifier} from "../src/Ed25519Verifier.sol";
import {GnoLightClient} from "../src/GnoLightClient.sol";
import {PausableGate} from "../src/PausableGate.sol";
import {IGnoLightClient} from "../src/interfaces/IGnoLightClient.sol";
import {BridgeFixture} from "./helpers/BridgeFixture.sol";
import {TestChain} from "./helpers/TestChain.sol";
import {Vectors} from "./helpers/Vectors.sol";

/// gno.land → Ethereum through the real Mailbox.process → PausableGate.verify → GnoLightClient.verify →
/// HypERC20.handle, on the synthetic chain Go signed with real ed25519 keys (checked by lane E-M2a's verifier),
/// deployed by script/Setup.s.sol at the addresses tools/evmvectors computed.
contract DeliveryVectorsTest is BridgeFixture, Vectors {
    string internal synthetic;
    address internal alice;
    address internal bob;
    address internal carol;

    function setUp() public {
        synthetic = _read("synthetic.json");
        vm.warp(1_791_000_000);
        vm.roll(1_000);
        VHeader memory h100 = _vheader(synthetic, ".headers[0]");
        // the verifier already on the chain (one serves every deployment): the deployer's nonces then put the tokens
        // and HlAdmin at 9, 11 and 14, the addresses tools/evmvectors computed (the ProxyAdmin's hand-over at nonce 1
        // takes the place of the verifier's creation)
        BridgeSetup.Config memory c = _config(h100.leaves, _set(synthetic, "A"));
        c.ed25519 = address(new Ed25519Verifier());
        _deploy(c);
        alice = vm.parseJsonAddress(synthetic, ".alice");
        bob = vm.parseJsonAddress(synthetic, ".bob");
        carol = vm.parseJsonAddress(synthetic, ".carol");
    }

    function _up(uint256 i) internal {
        VHeader memory h = _vheader(synthetic, _at(".headers", i));
        IGnoLightClient.Validator[] memory next;
        if (h.announcing) {
            next = _set(synthetic, h.nextSet);
        }
        vm.warp(block.timestamp + 7);
        vm.roll(block.number + 1);
        vm.prank(relayer);
        assertTrue(d.client.update(h.leaves, h.commit, _set(synthetic, h.set), next));
    }

    function _msgNamed(string memory name) internal view returns (bytes memory m, bytes memory meta) {
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
        revert(name);
    }

    function _process(string memory name) internal {
        (bytes memory m, bytes memory meta) = _msgNamed(name);
        vm.prank(relayer);
        d.mailbox.process(meta, m);
    }

    function _expectProcessRevert(string memory name, bytes memory err) internal {
        (bytes memory m, bytes memory meta) = _msgNamed(name);
        vm.prank(relayer);
        vm.expectRevert(err);
        d.mailbox.process(meta, m);
    }

    /// The deployment is where the generator put its recipients.
    function test_theDeploymentIsWhereTheVectorsPointed() public view {
        assertEq(address(d.token0), vm.parseJsonAddress(synthetic, ".token0"));
        assertEq(address(d.token1), vm.parseJsonAddress(synthetic, ".token1"));
        assertEq(address(d.admin), vm.parseJsonAddress(synthetic, ".admin"));
        assertEq(DEPLOYER, vm.parseJsonAddress(synthetic, ".deployer"));
        assertEq(ROUTE0, vm.parseJsonBytes32(synthetic, ".route0"));
        assertEq(ROUTE1, vm.parseJsonBytes32(synthetic, ".route1"));
        assertEq(HLGOV, vm.parseJsonBytes32(synthetic, ".hlgov"));
        assertEq(keccak256(bytes(vm.parseJsonString(synthetic, ".mailboxPkgPath"))), _pkgPathHash());
        assertTrue(d.admin.setupDone());
    }

    /// Every message of the synthetic chain, delivered as a relayer would: mints, a decision, refusals (delivered
    /// once, a quarantine, an old epoch, the per-message maximum), and the decision executed a week later.
    function test_deliveries_goVectors() public {
        _up(1);
        _up(2);
        vm.expectEmit(true, true, false, true, address(d.token0));
        emit TokenRouter.ReceivedTransferRemote(GNO, bytes32(uint256(uint160(alice))), 1_000_000);
        _process("gnot-alice");
        assertEq(d.token0.balanceOf(alice), 1_000_000);
        assertEq(d.token0.totalSupply(), 1_000_000);
        _process("bubble-bob");
        assertEq(d.token1.balanceOf(bob), 2_500_000);
        assertEq(d.gate.limits(0).used, 1_000_000);
        assertEq(d.gate.limits(1).used, 2_500_000);
        // a decision of r/hlgov rides the raw client: kind 12, number 1
        _process("decision-kind12");
        assertEq(d.admin.lastSeq(), 1);
        assertEq(d.admin.pending(), 1);
        // a message is delivered once
        _expectProcessRevert("gnot-alice", bytes("Mailbox: already delivered"));
        // a direct call of the gate's verify, by anyone but the mailbox
        (bytes memory m4, bytes memory meta4) = _msgNamed("gnot-carol-old-epoch");
        vm.expectRevert(PausableGate.NotTheMailbox.selector);
        d.gate.verify(meta4, m4);
        // 103 announces set B: token deliveries wait out the quarantine
        _up(3);
        _expectProcessRevert("gnot-carol-old-epoch", abi.encodeWithSelector(PausableGate.Quarantine.selector));
        _up(4); // B's first header: 102 proves nothing more
        vm.warp(block.timestamp + 7 days / CLOCK_FACTOR);
        _expectProcessRevert("gnot-carol-old-epoch", abi.encodeWithSelector(GnoLightClient.UnknownHeader.selector));
        _up(5);
        _up(6);
        _process("gnot-alice-2");
        assertEq(d.token0.balanceOf(alice), 4_000_000);
        _expectProcessRevert("gnot-over-max", abi.encodeWithSelector(PausableGate.OverMax.selector));
        // the week of decision 1 has run: kind 12 sets token 0's limits
        d.admin.execute(1);
        assertEq(d.gate.limits(0).maxPerMessage, 50_000_000_000);
        assertEq(d.gate.limits(0).dayCap, 200_000_000_000);
        assertEq(d.admin.pending(), 0);
        // C, then D: sixty validators
        _up(7);
        _up(8);
        _up(9);
        _up(10);
        vm.warp(block.timestamp + 7 days / CLOCK_FACTOR);
        _process("bubble-carol");
        assertEq(d.token1.balanceOf(carol), 4_000_000);
    }
}

/// Deliveries on the Solidity test chain: what pauses them and what does not, the day's bucket through real
/// deliveries, and a burn back to gno.land.
contract DeliveryTest is BridgeFixture {
    address internal alice = makeAddr("alice");

    function setUp() public {
        _world();
    }

    function _expectMintRevert(uint8 token, uint256 amount, bytes memory err) internal {
        bytes[] memory ms = new bytes[](1);
        ms[0] = _message(
            token == 0 ? ROUTE0 : ROUTE1,
            token == 0 ? address(d.token0) : address(d.token1),
            TestChain.tokenBody(alice, amount)
        );
        bytes[] memory meta = _prove(ms);
        vm.prank(relayer);
        vm.expectRevert(err);
        d.mailbox.process(meta[0], ms[0]);
    }

    /// One flag on the gate stops every token delivery and no decision: HlAdmin's ISM is the raw client.
    function test_aFlagStopsTokenDeliveriesNotDecisions() public {
        _mint(0, alice, 5);
        vm.prank(watch2);
        d.gate.setFlag();
        _expectMintRevert(0, 5, abi.encodeWithSelector(PausableGate.Paused.selector));
        // a decision (an unknown kind: recorded and spent) still lands
        _decide(abi.encodePacked(uint8(77), uint64(1)));
        assertEq(d.admin.lastSeq(), 1);
        vm.prank(watch2);
        d.gate.clearFlag();
        _mint(0, alice, 5);
        assertEq(d.token0.balanceOf(alice), 10);
    }

    /// A set change on gno.land: token deliveries wait the quarantine's week, decisions do not.
    function test_theQuarantineStopsTokenDeliveriesNotDecisions() public {
        IGnoLightClient.Validator[] memory v = _chainSet();
        IGnoLightClient.Validator[] memory w = TestChain.validators(2, 5, 60);
        TestChain.Header memory h = _header(chainHeight + 1, v, w, bytes32(0));
        bytes[] memory l = TestChain.leaves(h);
        bytes32 hh = TestChain.hash(l);
        vm.prank(relayer);
        assertTrue(d.client.update(l, TestChain.commit(h, hh, v, TestChain.quorum(v), 0), v, w));
        (chainHeight, chainHash) = (h.height, hh);
        _setChainSet(w);
        assertEq(d.client.quarantineEnd(), block.timestamp + 7 days / CLOCK_FACTOR);
        _expectMintRevert(0, 5, abi.encodeWithSelector(PausableGate.Quarantine.selector));
        _decide(abi.encodePacked(uint8(77), uint64(1)));
        assertEq(d.admin.lastSeq(), 1);
        vm.warp(block.timestamp + 7 days / CLOCK_FACTOR);
        _mint(0, alice, 5);
        assertEq(d.token0.balanceOf(alice), 5);
    }

    /// The day's bucket through deliveries: four of the maximum fill it, one unit more waits for it to drain.
    function test_theDayCapThroughDeliveries() public {
        for (uint256 i = 0; i < 4; i++) {
            _mint(1, alice, MAX_PER_MESSAGE);
        }
        _expectMintRevert(1, 1, abi.encodeWithSelector(PausableGate.OverDayCap.selector));
        _expectMintRevert(1, MAX_PER_MESSAGE + 1, abi.encodeWithSelector(PausableGate.OverMax.selector));
        vm.warp(block.timestamp + 1 days / CLOCK_FACTOR);
        _mint(1, alice, MAX_PER_MESSAGE);
        assertEq(d.token1.balanceOf(alice), 5 * uint256(MAX_PER_MESSAGE));
    }

    /// A burn back to gno.land: HypERC20.transferRemote burns and dispatches `recipient ‖ amount` to the route.
    function test_aBurnDispatchesTheTokenMessage() public {
        _mint(0, alice, 1_000_000);
        bytes32 to = bytes32(uint256(0xabcdef));
        uint256 quote = d.token0.quoteGasPayment(GNO);
        assertEq(quote, 0, "no fee at launch");
        bytes memory body = abi.encodePacked(to, uint256(400_000));
        bytes memory want = TestChain.message(0, LOCAL, bytes32(uint256(uint160(address(d.token0)))), GNO, ROUTE0, body);
        vm.expectEmit(true, true, true, true, address(d.mailbox));
        emit IMailbox.Dispatch(address(d.token0), GNO, ROUTE0, want);
        vm.prank(alice);
        d.token0.transferRemote(GNO, to, 400_000);
        assertEq(d.token0.balanceOf(alice), 600_000);
        assertEq(d.token0.totalSupply(), 600_000);
        assertEq(d.mailbox.nonce(), 1);
        assertEq(d.merkleTreeHook.count(), 1);
        assertEq(d.mailbox.latestDispatchedId(), keccak256(want));
    }
}

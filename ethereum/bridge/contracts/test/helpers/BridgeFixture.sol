// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Test} from "forge-std/Test.sol";

import {BridgeSetup} from "../../script/Setup.s.sol";
import {IGnoLightClient} from "../../src/interfaces/IGnoLightClient.sol";
import {TestChain} from "./TestChain.sol";

/// The deployment the suite runs against: script/Setup.s.sol's eleven steps from the deployer below (nonce 0
/// first; DeliveryVectorsTest names a verifier already on the chain, so its tokens and HlAdmin land at the nonces 9,
/// 11 and 14 tools/evmvectors computed, the others at 10, 12 and 15), and a gno.land test chain driven in Solidity
/// (TestChain: real ed25519 keys and signatures, checked by lane E-M2a's verifier) for the tests that deliver
/// decisions and transfers of their own making. The constants are synthetic.json's.
abstract contract BridgeFixture is Test {
    address internal constant DEPLOYER = 0xDe9107e5dE9107e5De9107e5de9107E5dE9107e5;
    uint32 internal constant LOCAL = 31337;
    uint32 internal constant GNO = 1198419796;
    string internal constant CHAIN = "gno-test-1";
    string internal constant MAILBOX_PATH = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox";
    bytes32 internal constant ROUTE0 = 0x0000000000000000000000003cb9d0388661c46641f59dc080f10c16aa570681;
    bytes32 internal constant ROUTE1 = 0x000000000000000000000000da39319b821fb3912bbafe1d48d0f383121988a7;
    bytes32 internal constant HLGOV = 0x000000000000000000000000073f0fda0fda3eab0213bfa0ee329cd80ff4ab71;
    uint64 internal constant MAX_PER_MESSAGE = 25_000_000_000; // 25,000 at 6 decimals
    uint64 internal constant DAY_CAP = 100_000_000_000; // 100,000
    uint256 internal constant MAX_PROTOCOL_FEE = 0.001 ether;

    address internal seat1 = makeAddr("seat1");
    address internal seat2 = makeAddr("seat2");
    address internal seat3 = makeAddr("seat3");
    address internal seat4 = makeAddr("seat4");
    address internal watch1 = makeAddr("watch1");
    address internal watch2 = makeAddr("watch2");
    address internal treasurer = makeAddr("treasurer");
    address internal relayer = makeAddr("relayer");

    BridgeSetup.Deployment internal d;
    /// r/hlgov's counter at the deploy (HlAdmin's startSequence).
    uint64 internal startSequence;

    // ---- the Solidity test chain ----
    IGnoLightClient.Validator[] internal chainSet;
    int64 internal chainHeight;
    bytes32 internal chainHash;
    int64 internal chainTime;
    uint32 internal gnoNonce;

    function _pkgPathHash() internal pure returns (bytes32) {
        return keccak256(bytes(MAILBOX_PATH));
    }

    function _config(bytes[] memory trustedLeaves, IGnoLightClient.Validator[] memory validators)
        internal
        view
        returns (BridgeSetup.Config memory c)
    {
        c.deployer = DEPLOYER;
        c.localDomain = LOCAL;
        c.gnoDomain = GNO;
        c.chainId = CHAIN;
        c.trustedLeaves = trustedLeaves;
        c.validators = validators;
        c.mailboxPkgPathHash = _pkgPathHash();
        c.maxProtocolFee = MAX_PROTOCOL_FEE;
        c.beneficiary = treasurer;
        c.name0 = "GNOT";
        c.symbol0 = "GNOT";
        c.name1 = "BUBBLE";
        c.symbol1 = "BUBBLE";
        c.seats = [seat1, seat2, seat3, seat4];
        c.watch = [watch1, watch2];
        c.maxPerMessage = [MAX_PER_MESSAGE, MAX_PER_MESSAGE];
        c.dayCap = [DAY_CAP, DAY_CAP];
        c.hlgov = HLGOV;
        c.route0 = ROUTE0;
        c.route1 = ROUTE1;
        c.startSequence = startSequence;
    }

    function _deploy(BridgeSetup.Config memory c) internal {
        vm.startPrank(DEPLOYER);
        d = BridgeSetup.deploy(c);
        vm.stopPrank();
    }

    // -------------------------------------------------------------------------- the Solidity test chain --

    /// The deployment over a test chain of four validators at power 60, trusted at height 100.
    function _world() internal {
        _worldWith(TestChain.validators(1, 4, 60));
    }

    function _worldWith(IGnoLightClient.Validator[] memory v) internal {
        vm.warp(1_791_000_000);
        vm.roll(1_000);
        chainTime = 1_791_000_000;
        bytes[] memory l = TestChain.leaves(_header(100, v, v, bytes32(0)));
        bytes32 hh = TestChain.hash(l);
        _deploy(_config(l, v));
        _setChainSet(v);
        chainHeight = 100;
        chainHash = hh;
    }

    function _setChainSet(IGnoLightClient.Validator[] memory v) internal {
        delete chainSet;
        for (uint256 i = 0; i < v.length; i++) {
            chainSet.push(v[i]);
        }
    }

    function _chainSet() internal view returns (IGnoLightClient.Validator[] memory v) {
        v = new IGnoLightClient.Validator[](chainSet.length);
        for (uint256 i = 0; i < v.length; i++) {
            v[i] = chainSet[i];
        }
    }

    function _header(
        int64 height,
        IGnoLightClient.Validator[] memory v,
        IGnoLightClient.Validator[] memory next,
        bytes32 lrh
    ) internal view returns (TestChain.Header memory h) {
        h.chainId = CHAIN;
        h.height = height;
        h.timeSecs = chainTime + 7 * (height - 100);
        h.timeNanos = int32(uint32(100_000_000 + uint64(height)));
        h.lastBlockHash = height == 100 ? bytes32(0) : chainHash;
        h.validatorsHash = TestChain.setHash(v);
        h.nextValidatorsHash = TestChain.setHash(next);
        h.lastResultsHash = lrh;
        h.partsTotal = 1;
        h.partsHash = sha256(abi.encodePacked("parts", height));
    }

    /// The next header of the test chain, signed by the current set's quorum, its LastResultsHash `lrh`, accepted
    /// by an update; returns its hash.
    function _advance(bytes32 lrh) internal returns (bytes32) {
        vm.roll(block.number + 1);
        IGnoLightClient.Validator[] memory v = _chainSet();
        TestChain.Header memory h = _header(chainHeight + 1, v, v, lrh);
        bytes[] memory l = TestChain.leaves(h);
        bytes32 hh = TestChain.hash(l);
        IGnoLightClient.Commit memory c = TestChain.commit(h, hh, v, TestChain.quorum(v), 0);
        IGnoLightClient.Validator[] memory none;
        vm.prank(relayer);
        require(d.client.update(l, c, v, none), "the update did not finish");
        chainHeight = h.height;
        chainHash = hh;
        return hh;
    }

    /// A message from gno.land (its domain), as hlmailbox dispatches it, to Ethereum's local domain.
    function _message(bytes32 sender, address recipient, bytes memory body) internal returns (bytes memory) {
        return TestChain.message(gnoNonce++, GNO, sender, LOCAL, bytes32(uint256(uint160(recipient))), body);
    }

    /// Puts `messages` in one transaction's leaf (a block of one transaction), accepts a header committing to it,
    /// and returns each message's metadata.
    function _prove(bytes[] memory messages) internal returns (bytes[] memory meta) {
        bytes[] memory l = new bytes[](1);
        l[0] = TestChain.dispatchLeaf(messages, MAILBOX_PATH);
        bytes32 hh = _advance(TestChain.root(l));
        meta = new bytes[](messages.length);
        for (uint256 i = 0; i < messages.length; i++) {
            meta[i] = TestChain.metadata(hh, l, 0);
        }
    }

    /// Proves one message and delivers it through the mailbox.
    function _deliver(bytes memory m) internal {
        bytes[] memory ms = new bytes[](1);
        ms[0] = m;
        bytes[] memory meta = _prove(ms);
        vm.prank(relayer, relayer);
        d.mailbox.process(meta[0], m);
    }

    /// A decision of r/hlgov to HlAdmin, delivered.
    function _decide(bytes memory body) internal {
        _deliver(_message(HLGOV, address(d.admin), body));
    }

    /// A transfer from a route to its token, delivered.
    function _mint(uint8 token, address to, uint256 amount) internal {
        _deliver(
            _message(
                token == 0 ? ROUTE0 : ROUTE1,
                token == 0 ? address(d.token0) : address(d.token1),
                TestChain.tokenBody(to, amount)
            )
        );
    }
}

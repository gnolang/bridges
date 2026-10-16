// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Test} from "forge-std/Test.sol";

import {BridgeSetup, Setup} from "../script/Setup.s.sol";
import {BridgeFixture} from "./helpers/BridgeFixture.sol";
import {TestChain} from "./helpers/TestChain.sol";

/// The setup of PLAN.md §5.8 run whole, and a first delivery each way.
contract SetupTest is BridgeFixture {
    function setUp() public {
        _world();
    }

    function test_setup_leavesEveryAuthorityWithHlAdmin() public view {
        address a = address(d.admin);
        assertTrue(d.admin.setupDone(), "finishSetup flipped");
        assertEq(d.proxyAdmin.owner(), a);
        assertEq(d.mailbox.owner(), a);
        assertEq(d.token0.owner(), a);
        assertEq(d.token1.owner(), a);
        assertEq(d.gate.owner(), a);
        assertEq(d.merkleTreeHook.owner(), a);
        assertEq(d.protocolFee.owner(), a);
        assertEq(d.client.admin(), a);
        assertEq(address(d.mailbox.defaultIsm()), address(d.gate));
        assertEq(address(d.token0.interchainSecurityModule()), address(d.gate));
        assertEq(d.admin.interchainSecurityModule(), address(d.client));
        assertEq(d.token0.routers(GNO), ROUTE0);
        assertEq(d.token1.routers(GNO), ROUTE1);
        assertEq(d.admin.route(0), ROUTE0);
        assertEq(address(d.client.ED25519()), address(d.ed25519), "the client calls the verifier the setup deployed");
        assertEq(d.admin.ed25519(), address(d.ed25519));
        assertEq(address(d.ed25519).codehash, d.admin.ed25519CodeHash());
        assertEq(d.admin.localDomain(), LOCAL);
        assertEq(d.client.trustedHeight(), 100);
        assertEq(d.client.records(), 1);
        assertEq(d.adminAt, a, "HlAdmin where the nonce plan put it");
        assertEq(d.adminNonce, 15, "the ProxyAdmin, its hand-over, thirteen creations: HlAdmin at 15");
        assertEq(d.mailbox.nonce(), 0);
        assertEq(d.merkleTreeHook.count(), 0);
    }

    function test_mint_throughTheMailboxTheGateAndTheClient() public {
        address alice = makeAddr("alice");
        _mint(0, alice, 1_000_000);
        assertEq(d.token0.balanceOf(alice), 1_000_000);
        _mint(1, alice, 7);
        assertEq(d.token1.balanceOf(alice), 7);
    }
}

/// The script's settings file, read as `Setup.run` reads it.
contract SetupConfigTest is Test, Setup {
    function test_theSettingsFileReadsWhole() public view {
        string memory json = string.concat(
            "{\"localDomain\":31337,\"gnoDomain\":1198419796,\"chainId\":\"gno-test-1\",",
            "\"trustedLeaves\":[\"0x0a0a676e6f2d746573742d31\",\"0x08c801\",\"0x\"],",
            "\"pubkeys\":[\"0x0202020202020202020202020202020202020202020202020202020202020202\"],\"powers\":[60],",
            "\"mailboxPkgPath\":\"gno.land/r/x/eth/hlmailbox\",\"maxProtocolFee\":1000000000000000,",
            "\"ed25519\":\"0x000000000000000000000000000000000000ed25\",",
            "\"beneficiary\":\"0x0000000000000000000000000000000000000abc\",\"name0\":\"GNOT\",\"symbol0\":\"GNOT\",",
            "\"name1\":\"BUBBLE\",\"symbol1\":\"BUBBLE\",",
            "\"seats\":[\"0x0000000000000000000000000000000000000001\",\"0x0000000000000000000000000000000000000002\",",
            "\"0x0000000000000000000000000000000000000003\",\"0x0000000000000000000000000000000000000004\"],",
            "\"watch\":[\"0x0000000000000000000000000000000000000005\",\"0x0000000000000000000000000000000000000006\"],",
            "\"maxPerMessage\":[25000000000,25000000000],\"dayCap\":[100000000000,100000000000],",
            "\"hlgov\":\"0x0000000000000000000000000000000000000000000000000000000000000007\",",
            "\"route0\":\"0x0000000000000000000000000000000000000000000000000000000000000008\",",
            "\"route1\":\"0x0000000000000000000000000000000000000000000000000000000000000009\",\"startSequence\":41}"
        );
        BridgeSetup.Config memory c = _config(json);
        assertEq(c.localDomain, 31337);
        assertEq(c.gnoDomain, 1198419796);
        assertEq(c.chainId, "gno-test-1");
        assertEq(c.trustedLeaves.length, 3);
        assertEq(c.trustedLeaves[0], bytes.concat(hex"0a0a", "gno-test-1"));
        assertEq(c.trustedLeaves[1], hex"08c801");
        assertEq(c.trustedLeaves[2].length, 0);
        assertEq(c.validators.length, 1);
        assertEq(c.validators[0].power, 60);
        assertEq(c.mailboxPkgPathHash, keccak256("gno.land/r/x/eth/hlmailbox"));
        assertEq(c.ed25519, address(0xed25));
        assertEq(c.maxProtocolFee, 0.001 ether);
        assertEq(c.seats[3], address(4));
        assertEq(c.watch[1], address(6));
        assertEq(c.maxPerMessage[1], 25_000_000_000);
        assertEq(c.dayCap[0], 100_000_000_000);
        assertEq(c.route1, bytes32(uint256(9)));
        assertEq(c.startSequence, 41);
    }
}

// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {console2} from "forge-std/console2.sol";
import {Vm} from "forge-std/Vm.sol";
import {Mailbox} from "@hyperlane-xyz/core/contracts/Mailbox.sol";
import {HypERC20} from "@hyperlane-xyz/core/contracts/token/HypERC20.sol";
import {TokenRouter} from "@hyperlane-xyz/core/contracts/token/libs/TokenRouter.sol";
import {ProxyAdmin} from "@openzeppelin/contracts/proxy/transparent/ProxyAdmin.sol";
import {TransparentUpgradeableProxy} from "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol";

import {BridgeSetup} from "../script/Setup.s.sol";
import {Ed25519Verifier, IEd25519Verifier} from "../src/Ed25519Verifier.sol";
import {GnoLightClient} from "../src/GnoLightClient.sol";
import {HlAdmin} from "../src/HlAdmin.sol";
import {PausableGate} from "../src/PausableGate.sol";
import {IGnoLightClient} from "../src/interfaces/IGnoLightClient.sol";
import {AdminBase} from "./helpers/AdminBase.sol";
import {TestChain} from "./helpers/TestChain.sol";

/// A verifier that accepts every signature at the cost of a call: the client's own share of an update, beside E-M2a's
/// verifications.
contract TrustingVerifier is IEd25519Verifier {
    function verify(bytes32, bytes calldata, bytes32, bytes32) external pure returns (bool) {
        return true;
    }
}

/// The measurements of M2-NOTES.md §6. Run as `forge test --match-contract GasReport --hardfork osaka -vv`:
/// Ethereum mainnet's execution rules since Fusaka (2025-12-03); the bytecode stays cancun, as Hyperlane's release.
/// Each call is measured as a fresh transaction meets the state: every account it may touch and all their slots
/// cooled first (vm.cool), the call's own gas read on the callee's side (vm.lastFrameGas: the whole frame, before
/// refunds). "exec" is that; "calldata" the transaction's calldata gas (EIP-2028, or EIP-7623's floor of 10 a token
/// where it binds); "tx" = 21,000 + calldata + exec, what the transaction pays (EIP-7825 caps it at 16,777,216).
/// Signatures are real ed25519 through lane E-M2a's Ed25519Verifier unless a line says otherwise.
contract GasReport is AdminBase {
    uint256 internal constant TX_CAP = 16_777_216; // EIP-7825
    bytes32 internal constant PKG = keccak256("gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox");

    // ------------------------------------------------------------------------------------- the measuring --

    function _calldataGas(bytes memory data, uint256 exec) internal pure returns (uint256) {
        uint256 zeros;
        for (uint256 i = 0; i < data.length; i++) {
            if (data[i] == 0) {
                zeros++;
            }
        }
        uint256 nonzeros = data.length - zeros;
        uint256 standard = 4 * zeros + 16 * nonzeros;
        uint256 floor = 10 * (zeros + 4 * nonzeros);
        // EIP-7623: a transaction pays max(21,000 + standard + exec, 21,000 + floor); the floor's excess over exec
        // is reported as calldata
        return standard + exec >= floor ? standard : floor - exec;
    }

    /// `target.call(data)` as a fresh transaction from `sender` (tx.origin too; none: this contract) meets it: every
    /// account of `cold`, the target and all their slots cold. Returns the call's own gas and the transaction's.
    function _run(address target, bytes memory data, address[] memory cold, address sender)
        internal
        returns (uint256 exec, uint256 tx_)
    {
        for (uint256 i = 0; i < cold.length; i++) {
            vm.cool(cold[i]);
        }
        vm.cool(target);
        if (sender != address(0)) {
            vm.prank(sender, sender);
        }
        (bool ok, bytes memory ret) = target.call(data);
        if (!ok) {
            assembly ("memory-safe") {
                revert(add(ret, 32), mload(ret))
            }
        }
        Vm.Gas memory g = vm.lastFrameGas();
        exec = g.gasTotalUsed;
        tx_ = 21_000 + _calldataGas(data, exec) + exec;
    }

    /// Every contract of the deployment, the verifier and HlAdminExec included.
    function _deployment() internal view returns (address[] memory a) {
        a = new address[](16);
        a[0] = address(d.proxyAdmin);
        a[1] = address(d.mailboxImpl);
        a[2] = address(d.mailbox);
        a[3] = address(d.merkleTreeHook);
        a[4] = address(d.protocolFee);
        a[5] = address(d.ed25519);
        a[6] = d.clientImpl;
        a[7] = address(d.client);
        a[8] = address(d.token0Impl);
        a[9] = address(d.token0);
        a[10] = address(d.token1Impl);
        a[11] = address(d.token1);
        a[12] = address(d.gateImpl);
        a[13] = address(d.gate);
        a[14] = address(d.admin);
        a[15] = address(d.admin.exec());
    }

    function _with(address[] memory a, address x) internal pure returns (address[] memory b) {
        b = new address[](a.length + 1);
        for (uint256 i = 0; i < a.length; i++) {
            b[i] = a[i];
        }
        b[a.length] = x;
    }

    function _line(string memory what, uint256 exec, uint256 tx_) internal pure {
        console2.log(string.concat(what, ": exec ", vm.toString(exec), ", tx ", vm.toString(tx_)));
    }

    // ------------------------------------------------------------------------------------------- updates --

    struct Standalone {
        GnoLightClient c;
        address[] cold;
        IGnoLightClient.Validator[] v;
        TestChain.Header h;
        bytes[] leaves;
        bytes32 hash;
    }

    /// A client trusting height 100 of a set of n validators at power 60, over `ver`, and header 101 to update to.
    function _standalone(uint256 n, IEd25519Verifier ver) internal returns (Standalone memory s) {
        vm.warp(1_791_000_000);
        chainTime = 1_791_000_000;
        s.v = TestChain.validators(n, n, 60);
        bytes32 hh = TestChain.hash(TestChain.leaves(_header(100, s.v, s.v, bytes32(0))));
        chainHash = hh;
        ProxyAdmin pa = new ProxyAdmin();
        address impl = address(new GnoLightClient(PKG, ver));
        s.c = GnoLightClient(
            address(
                new TransparentUpgradeableProxy(
                    impl,
                    address(pa),
                    abi.encodeCall(
                        GnoLightClient.initialize,
                        (CHAIN, GNO, TestChain.leaves(_header(100, s.v, s.v, bytes32(0))), s.v, relayer)
                    )
                )
            )
        );
        s.cold = new address[](2);
        (s.cold[0], s.cold[1]) = (impl, address(ver));
        s.h = _header(101, s.v, s.v, sha256("results"));
        s.leaves = TestChain.leaves(s.h);
        s.hash = TestChain.hash(s.leaves);
    }

    /// One call of an update carrying the precommits of slots [from, to): its exec and tx gas, and whether it finished.
    function _updateCall(Standalone memory s, uint256 from, uint256 to)
        internal
        returns (uint256 exec, uint256 tx_, bool finished)
    {
        IGnoLightClient.Commit memory cm = TestChain.commit(s.h, s.hash, s.v, TestChain.range(from, to), 0);
        bytes memory data =
            abi.encodeCall(GnoLightClient.update, (s.leaves, cm, s.v, new IGnoLightClient.Validator[](0)));
        uint256 trusted = uint256(uint64(s.c.trustedHeight()));
        (exec, tx_) = _run(address(s.c), data, s.cold, relayer);
        finished = uint256(uint64(s.c.trustedHeight())) != trusted;
    }

    /// An update at n validators of equal power carrying the quorum's precommits (k of them) in one call.
    function _update(uint256 n, IEd25519Verifier ver) internal returns (uint256 exec, uint256 tx_, uint256 k) {
        Standalone memory s = _standalone(n, ver);
        k = TestChain.quorum(s.v).length;
        bool finished;
        (exec, tx_, finished) = _updateCall(s, 0, k);
        require(finished, "the update did not finish");
    }

    /// PLAN §6.1's update at 4 to 100 validators, real signatures: what one transaction costs and whether it fits
    /// EIP-7825's cap; beside it the same update through a verifier that accepts everything (the client's own share,
    /// a call to the verifier per signature included) and the difference per signature (the verification itself).
    function test_gas_updates() public {
        IEd25519Verifier real = new Ed25519Verifier();
        IEd25519Verifier trusting = new TrustingVerifier();
        uint256[7] memory ns = [uint256(4), 6, 20, 28, 40, 60, 100];
        console2.log(
            "update in one transaction: n | k | exec | tx | fits 16,777,216 | exec without signatures | per signature"
        );
        for (uint256 i = 0; i < ns.length; i++) {
            (uint256 exec, uint256 tx_, uint256 k) = _update(ns[i], real);
            (uint256 bare,,) = _update(ns[i], trusting);
            console2.log(
                string.concat(
                    vm.toString(ns[i]),
                    " | ",
                    vm.toString(k),
                    " | ",
                    vm.toString(exec),
                    " | ",
                    vm.toString(tx_),
                    tx_ <= TX_CAP ? " | yes | " : " | no | ",
                    vm.toString(bare),
                    " | ",
                    vm.toString((exec - bare) / k)
                )
            );
        }
    }

    /// Past one transaction: 100 validators (67 signatures) and 255 (171), staged in calls of 60 precommits; and 40
    /// validators staged 14 + 13, the plan's example of a stage.
    function test_gas_stagedUpdates() public {
        IEd25519Verifier real = new Ed25519Verifier();
        uint256[3] memory ns = [uint256(40), 100, 255];
        uint256[3] memory chunk = [uint256(14), 60, 60];
        for (uint256 i = 0; i < 3; i++) {
            Standalone memory s = _standalone(ns[i], real);
            uint256 k = TestChain.quorum(s.v).length;
            for (uint256 from = 0; from < k; from += chunk[i]) {
                uint256 to = from + chunk[i] < k ? from + chunk[i] : k;
                (uint256 exec, uint256 tx_, bool finished) = _updateCall(s, from, to);
                require(finished == (to == k), "the stage's end");
                console2.log(
                    string.concat(
                        "staged update at ",
                        vm.toString(ns[i]),
                        " validators, precommits ",
                        vm.toString(from),
                        "-",
                        vm.toString(to - 1),
                        finished ? " (finishes)" : " (stored)",
                        ": exec ",
                        vm.toString(exec),
                        ", tx ",
                        vm.toString(tx_)
                    )
                );
                require(tx_ <= TX_CAP, "a call past the cap");
            }
        }
    }

    // -------------------------------------------------------------------------- proofs and deliveries --

    function test_gas_proveHeaderAndVerify() public {
        _world();
        IGnoLightClient.Validator[] memory v = _chainSet();
        address[] memory all = _deployment();
        // proveHeader at the world's 4 validators: a header at 102 accepted, 101 skipped and proved
        TestChain.Header memory h101 = _header(101, v, v, bytes32(0));
        bytes[] memory l101 = TestChain.leaves(h101);
        bytes32 hh101 = TestChain.hash(l101);
        chainHash = hh101;
        chainHeight = 101;
        _advance(bytes32(0));
        IGnoLightClient.Commit memory c = TestChain.commit(h101, hh101, v, TestChain.quorum(v), 0);
        (uint256 exec, uint256 tx_) =
            _run(address(d.client), abi.encodeCall(GnoLightClient.proveHeader, (l101, c, v)), all, relayer);
        _line("proveHeader at 4 validators (3 signatures)", exec, tx_);
        // verify by leaf size: one dispatch, four, sixteen; one dispatch beside 8 KB of another realm's events
        uint256[3] memory counts = [uint256(1), 4, 16];
        for (uint256 i = 0; i < 3; i++) {
            bytes[] memory ms = new bytes[](counts[i]);
            for (uint256 j = 0; j < counts[i]; j++) {
                ms[j] = _message(ROUTE0, address(d.token0), TestChain.tokenBody(treasurer, 1));
            }
            bytes[] memory meta = _prove(ms);
            bytes memory leaf = TestChain.dispatchLeaf(ms, MAILBOX_PATH);
            (exec,) = _run(
                address(d.client),
                abi.encodeCall(GnoLightClient.verify, (meta[counts[i] - 1], ms[counts[i] - 1])),
                all,
                address(0)
            );
            console2.log(
                string.concat(
                    "verify, a leaf of ",
                    vm.toString(leaf.length),
                    " bytes, dispatches: ",
                    vm.toString(counts[i]),
                    ", exec:"
                ),
                exec
            );
        }
        bytes[] memory events = new bytes[](9);
        bytes memory m = _message(ROUTE0, address(d.token0), TestChain.tokenBody(treasurer, 1));
        for (uint256 j = 0; j < 8; j++) {
            bytes[] memory keys = new bytes[](1);
            bytes[] memory values = new bytes[](1);
            (keys[0], values[0]) = ("blob", new bytes(1000));
            events[j] = TestChain.any("/tm.Event", TestChain.eventValue("Other", keys, values, "gno.land/r/demo/other"));
        }
        events[8] = TestChain.dispatchEvent(m, MAILBOX_PATH);
        bytes[] memory l = new bytes[](1);
        l[0] = TestChain.resultLeaf("", "", events);
        bytes32 hh = _advance(TestChain.root(l));
        (exec,) = _run(
            address(d.client), abi.encodeCall(GnoLightClient.verify, (TestChain.metadata(hh, l, 0), m)), all, address(0)
        );
        console2.log(
            string.concat("verify, a leaf of ", vm.toString(l[0].length), " bytes (8 KB of other events), exec:"), exec
        );
    }

    function test_gas_deliveriesAndHandles() public {
        _world();
        address alice = makeAddr("alice");
        address[] memory all = _with(_deployment(), alice);
        // a mint through Mailbox.process (gate, client, HypERC20.handle) to a fresh holder, then to a holder again
        for (uint256 i = 0; i < 2; i++) {
            bytes[] memory ms = new bytes[](1);
            ms[0] = _message(ROUTE0, address(d.token0), TestChain.tokenBody(alice, 1_000_000));
            bytes[] memory meta = _prove(ms);
            (uint256 exec, uint256 tx_) =
                _run(address(d.mailbox), abi.encodeCall(Mailbox.process, (meta[0], ms[0])), all, relayer);
            _line(i == 0 ? "process + mint (a fresh holder)" : "process + mint (a holder again)", exec, tx_);
        }
        // HypERC20.handle alone, as the mailbox
        address bob = makeAddr("bob");
        (uint256 e2,) = _run(
            address(d.token0),
            abi.encodeCall(d.token0.handle, (GNO, ROUTE0, TestChain.tokenBody(bob, 5))),
            _with(all, bob),
            address(d.mailbox)
        );
        console2.log("HypERC20.handle (a fresh holder), exec:", e2);
        // a decision recorded through process (kind 7), and a cancel executed at recording
        _stage(7, _fee(1, treasurer));
        (uint256 e3, uint256 t3) =
            _run(address(d.mailbox), abi.encodeCall(Mailbox.process, (stagedMeta, stagedMsg)), all, relayer);
        _line("process + HlAdmin.handle (a kind 7 recorded)", e3, t3);
        _stage(1, _cancelOne(1));
        (e3, t3) = _run(address(d.mailbox), abi.encodeCall(Mailbox.process, (stagedMeta, stagedMsg)), all, relayer);
        _line("process + HlAdmin.handle (a cancel deleting one)", e3, t3);
        // a burn back to gno.land
        (e3, t3) = _run(
            address(d.token0), abi.encodeCall(TokenRouter.transferRemote, (GNO, bytes32(uint256(1)), 100)), all, alice
        );
        _line("HypERC20.transferRemote (a burn dispatched)", e3, t3);
    }

    // ------------------------------------------------------------------------------------------ the kinds --

    function test_gas_executePerKind() public {
        _world();
        _gflag(watch1);
        _aflag(watch2);
        uint64[9] memory ns;
        ns[0] = _decideKind(2, _unpause(0));
        ns[1] = _decideKind(2, _unpause(1));
        _aclear(watch2);
        ns[2] = _decideKind(3, _tokenIsm(0, address(d.gate)));
        ns[3] = _decideKind(5, _guardian(5, makeAddr("new watch")));
        ns[4] = _decideKind(6, _router(1, 1399811149, bytes32(uint256(7))));
        ns[5] = _decideKind(7, _fee(1000, treasurer));
        PausableGate impl = new PausableGate();
        ns[6] = _decideKind(8, _upgrade(address(d.gate), address(impl), address(impl).codehash, ""));
        ns[7] = _decideKind(12, _limits(0, 1, 2));
        ns[8] = _decideKind(4, "");
        _wait(WEEK);
        string[9] memory names = [
            "2 unpause (the gate)",
            "2 unpause (HlAdmin)",
            "3 set token ISM",
            "5 set guardians (one slot)",
            "6 enroll router",
            "7 set protocol fee",
            "8 upgrade (the gate)",
            "12 set mint limits",
            "4 reserved (spent)"
        ];
        address[] memory all = _with(_deployment(), address(impl));
        for (uint256 i = 0; i < 9; i++) {
            (uint256 exec, uint256 tx_) =
                _run(address(d.admin), abi.encodeCall(HlAdmin.execute, (ns[i])), all, makeAddr("executor"));
            _line(string.concat("execute kind ", names[i]), exec, tx_);
        }
    }

    function test_gas_kind9HandOver() public {
        _world();
        address[4] memory noSeats;
        address[2] memory noWatch;
        uint64[2] memory none;
        HlAdmin succ = new HlAdmin(
            HlAdmin.Config({
                mailbox: address(d.mailbox),
                client: address(d.client),
                gate: address(d.gate),
                token0: address(d.token0),
                token1: address(d.token1),
                proxyAdmin: address(d.proxyAdmin),
                merkleTreeHook: address(d.merkleTreeHook),
                protocolFee: address(d.protocolFee),
                ed25519: address(d.ed25519),
                ed25519CodeHash: BridgeSetup.ed25519CodeHash(),
                localDomain: LOCAL,
                origin: GNO,
                hlgov: HLGOV,
                route0: ROUTE0,
                route1: ROUTE1,
                mailboxPkgPathHash: _pkgPathHash(),
                seats: noSeats,
                watch: noWatch,
                maxPerMessage: none,
                dayCap: none,
                startSequence: 0,
                predecessor: address(d.admin)
            })
        );
        uint64 n = _decideKind(9, _transfer(address(succ), address(succ).codehash));
        _wait(WEEK);
        (uint256 exec, uint256 tx_) = _run(
            address(d.admin),
            abi.encodeCall(HlAdmin.execute, (n)),
            _with(_deployment(), address(succ)),
            makeAddr("executor")
        );
        _line("execute kind 9 transfer to a successor (eight authorities, successorInit)", exec, tx_);
    }

    // ------------------------------------------------------------------------------------ the deployment --

    function test_gas_theDeployment() public {
        vm.warp(1_791_000_000);
        chainTime = 1_791_000_000;
        IGnoLightClient.Validator[] memory v = TestChain.validators(1, 6, 60);
        BridgeSetup.Config memory c = _config(TestChain.leaves(_header(100, v, v, bytes32(0))), v);
        uint256 g0 = gasleft();
        new Ed25519Verifier();
        console2.log("the Ed25519Verifier alone (a separate deployment's), exec:", g0 - gasleft());
        vm.startPrank(DEPLOYER);
        uint256 g = gasleft();
        d = BridgeSetup.contracts(c);
        uint256 g1 = gasleft();
        d.admin = BridgeSetup.admin(c, d);
        uint256 g2 = gasleft();
        BridgeSetup.settings(c, d);
        BridgeSetup.ownership(d);
        uint256 g3 = gasleft();
        d.admin.finishSetup();
        uint256 g4 = gasleft();
        vm.stopPrank();
        console2.log("deploy, steps 1-6 (every contract but HlAdmin, the verifier included), exec:", g - g1);
        console2.log("deploy, step 7 (HlAdmin and its HlAdminExec), exec:", g1 - g2);
        console2.log("deploy, steps 8-10 (settings, ownership), exec:", g2 - g3);
        console2.log("deploy, step 11 (finishSetup, checks 1-31 and 34), exec:", g3 - g4);
        console2.log("deploy, all eleven steps, exec (add 21,000 and calldata per transaction):", g - g4);
    }
}

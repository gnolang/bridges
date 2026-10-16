// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {IGnoLightClient} from "../interfaces/IGnoLightClient.sol";
import {IHlAdmin} from "../interfaces/IHlAdmin.sol";
import {IPausableGate} from "../interfaces/IPausableGate.sol";
import {Guardians} from "../lib/Guardians.sol";
import {
    AuthorityLost,
    CallStarved,
    HlAdminStorage,
    ImplementationMoved,
    NoCodeYet,
    NotDelegated,
    SetupIncomplete
} from "./HlAdminStorage.sol";

/// The parts of Hyperlane's and OpenZeppelin's contracts HlAdmin reads and moves (their ABIs, by hand).
interface IOwnableLike {
    function owner() external view returns (address);
    function transferOwnership(address newOwner) external;
}

interface IMailboxSettings {
    function localDomain() external view returns (uint32);
    function defaultIsm() external view returns (address);
    function defaultHook() external view returns (address);
    function requiredHook() external view returns (address);
    function nonce() external view returns (uint32);
}

interface ITokenSettings {
    function localDomain() external view returns (uint32);
    function mailbox() external view returns (address);
    function scaleNumerator() external view returns (uint256);
    function scaleDenominator() external view returns (uint256);
    function decimals() external view returns (uint8);
    function totalSupply() external view returns (uint256);
    function interchainSecurityModule() external view returns (address);
    function hook() external view returns (address);
    function feeRecipient() external view returns (address);
    function feeHook() external view returns (address);
    function routers(uint32 domain) external view returns (bytes32);
    function setInterchainSecurityModule(address ism) external;
    function enrollRemoteRouter(uint32 domain, bytes32 router) external;
    function unenrollRemoteRouter(uint32 domain) external;
}

interface IProtocolFeeSettings {
    function MAX_PROTOCOL_FEE() external view returns (uint256);
    function protocolFee() external view returns (uint256);
    function setProtocolFee(uint256 fee) external;
    function setBeneficiary(address beneficiary) external;
}

interface IProxyAdminLike {
    function getProxyAdmin(address proxy) external view returns (address);
    function getProxyImplementation(address proxy) external view returns (address);
    function upgrade(address proxy, address implementation) external;
    function upgradeAndCall(address proxy, address implementation, bytes calldata data) external payable;
}

interface IMerkleTreeHookLike {
    function mailbox() external view returns (address);
}

/// The second half of HlAdmin's code (EIP-170 bounds a contract at 24,576 bytes, and HlAdmin whole is more): the
/// execution of each kind once HlAdmin's `execute` found the decision matured and closed it, and `finishSetup`'s
/// checks. HlAdmin's constructor creates it with HlAdmin's own settings and calls it only by delegatecall, so it
/// runs as HlAdmin — HlAdmin's storage (the ERC-7201 layout both read), HlAdmin's authority over every contract —
/// and nowhere else: called directly, at its own address, every function refuses (`NotDelegated`). Its code is fixed
/// with HlAdmin's: no function of either changes it.
contract HlAdminExec {
    using Guardians for Guardians.State;

    uint8 internal constant KIND_RESERVED = 4;
    uint8 internal constant KIND_UNPAUSE = 2;
    uint8 internal constant KIND_SET_TOKEN_ISM = 3;
    uint8 internal constant KIND_SET_GUARDIANS = 5;
    uint8 internal constant KIND_ENROLL_ROUTER = 6;
    uint8 internal constant KIND_SET_FEE = 7;
    uint8 internal constant KIND_UPGRADE = 8;
    uint8 internal constant KIND_TRANSFER = 9;

    /// The mailbox's protocol fee's immutable maximum at launch (PLAN.md §5.5): kind 7's bound, and what
    /// `checkSetup` reads back from the ProtocolFee.
    uint256 internal constant MAX_PROTOCOL_FEE = 0.001 ether;
    /// The tokens' decimals (PLAN.md §0: 6, scale 1/1).
    uint8 internal constant DECIMALS = 6;
    /// The gas a kind 8's call is forwarded, whatever gas its executor sends (audit round 3, E3-CT4):
    /// `ProxyAdmin.upgradeAndCall` is called with exactly this, and the new code's initializer runs on what is left of
    /// it two calls in — never less than a simulation with nothing warm shows (≈1.92M). That does not make the outcome
    /// the same for every executor (audit round 4): one that warms the path first (an access list, or its own reads of
    /// the ProxyAdmin and the proxy in the same transaction) leaves the initializer a few thousand more (+8,299
    /// measured), and each first access inside the initializer it warmed its cold surcharge more (E4-CT2); and on a
    /// chain with EIP-8037's state gas (Glamsterdam) this bounds execution gas only — the call's state growth is paid
    /// first from the transaction's reservoir (its gas above 2^24, passed to the call whole, read by no opcode) and
    /// only with none from this budget, where a fresh slot costs ≈110K on Sepolia and some 17 fill it (E4-CT3). So an
    /// initializer that branches on `gasleft()` or catches a failed subcall runs the branch its executor picks: the
    /// members' review refuses one, and splits one that grows more state than the budget holds with no reservoir
    /// (PLAN.md §8).
    /// CALL_FLOOR is the gas the execution must still have before that call for all of it to be forwarded (EIP-150
    /// keeps a 64th back), with room for the call's encoding; below it the execution reverts (`CallStarved`) and the
    /// decision pends.
    uint256 internal constant CALL_GAS = 2_000_000;
    uint256 internal constant CALL_FLOOR = CALL_GAS * 64 / 63 + 10_000;

    /// HlAdmin's settings as its constructor hands them over (HlAdmin.Config's, less the guardians and the number;
    /// `launch` is "no predecessor").
    struct Settings {
        address mailbox;
        address client;
        address gate;
        address token0;
        address token1;
        address proxyAdmin;
        address merkleTreeHook;
        address protocolFee;
        address ed25519;
        bytes32 ed25519CodeHash;
        uint32 localDomain;
        uint32 origin;
        bytes32 route0;
        bytes32 route1;
        bytes32 mailboxPkgPathHash;
        uint64[2] maxPerMessage;
        uint64[2] dayCap;
        bool launch;
    }

    /// The HlAdmin that created this code: the only address it runs at.
    address internal immutable admin;
    address internal immutable mailbox;
    address internal immutable client;
    address internal immutable gate;
    address internal immutable token0;
    address internal immutable token1;
    address internal immutable proxyAdmin;
    address internal immutable merkleTreeHook;
    address internal immutable protocolFee;
    address internal immutable ed25519;
    bytes32 internal immutable ed25519CodeHash;
    uint32 internal immutable localDomain;
    uint32 internal immutable origin;
    bytes32 internal immutable route0;
    bytes32 internal immutable route1;
    bytes32 internal immutable mailboxPkgPathHash;
    uint64 internal immutable maxPerMessage0;
    uint64 internal immutable maxPerMessage1;
    uint64 internal immutable dayCap0;
    uint64 internal immutable dayCap1;
    bool internal immutable launch;

    constructor(Settings memory s) {
        admin = msg.sender;
        mailbox = s.mailbox;
        client = s.client;
        gate = s.gate;
        token0 = s.token0;
        token1 = s.token1;
        proxyAdmin = s.proxyAdmin;
        merkleTreeHook = s.merkleTreeHook;
        protocolFee = s.protocolFee;
        ed25519 = s.ed25519;
        ed25519CodeHash = s.ed25519CodeHash;
        localDomain = s.localDomain;
        origin = s.origin;
        route0 = s.route0;
        route1 = s.route1;
        mailboxPkgPathHash = s.mailboxPkgPathHash;
        maxPerMessage0 = s.maxPerMessage[0];
        maxPerMessage1 = s.maxPerMessage[1];
        dayCap0 = s.dayCap[0];
        dayCap1 = s.dayCap[1];
        launch = s.launch;
    }

    modifier delegated() {
        if (address(this) != admin) {
            revert NotDelegated();
        }
        _;
    }

    // ---------------------------------------------------------------------------------------------- the kinds --

    /// Carries out a matured decision HlAdmin's `execute` has closed; the reason it is spent, or "" when it
    /// executed. A call that reverts reverts `execute` whole, and the decision stays pending for a retry.
    function run(uint8 kind, bytes calldata p, uint64 deliveredBlock) external delegated returns (string memory) {
        if (kind == KIND_RESERVED) {
            return "reserved";
        }
        if (kind == KIND_UNPAUSE) {
            return _unpause(p, deliveredBlock);
        }
        if (kind == KIND_SET_TOKEN_ISM) {
            if (p.length != 33) {
                return "params";
            }
            (bool ok, address ism) = _addr(p, 1);
            address t = _tokenOf(uint8(p[0]));
            if (!ok || t == address(0) || (ism != gate && ism != address(0))) {
                return "params";
            }
            ITokenSettings(t).setInterchainSecurityModule(ism);
            return "";
        }
        if (kind == KIND_SET_GUARDIANS) {
            return _setGuardians(p);
        }
        if (kind == KIND_ENROLL_ROUTER) {
            return _enrollRouter(p);
        }
        if (kind == KIND_SET_FEE) {
            if (p.length != 40) {
                return "params";
            }
            uint64 fee = uint64(bytes8(p[0:8]));
            (bool ok, address beneficiary) = _addr(p, 8);
            if (!ok || beneficiary == address(0) || fee > MAX_PROTOCOL_FEE) {
                return "params";
            }
            IProtocolFeeSettings(protocolFee).setProtocolFee(fee);
            IProtocolFeeSettings(protocolFee).setBeneficiary(beneficiary);
            return "";
        }
        if (kind == KIND_UPGRADE) {
            return _upgrade(p);
        }
        if (kind == KIND_TRANSFER) {
            return _transfer(p);
        }
        // kind 12, the last known kind: token u8 ‖ maximum u64 ‖ day cap u64
        if (p.length != 17 || uint8(p[0]) >= 2) {
            return "params";
        }
        IPausableGate(gate).setMintLimits(uint8(p[0]), uint64(bytes8(p[1:9])), uint64(bytes8(p[9:17])));
        return "";
    }

    /// Kind 2: object 0 the gate, 1 HlAdmin; spent under a hold on that object; clears the flags set at a block
    /// before the decision's delivery.
    function _unpause(bytes calldata p, uint64 deliveredBlock) private returns (string memory) {
        if (p.length != 1 || uint8(p[0]) > 1) {
            return "params";
        }
        if (uint8(p[0]) == 0) {
            if (IPausableGate(gate).hold().blockNumber != 0) {
                return "hold";
            }
            IPausableGate(gate).unpause(deliveredBlock);
            return "";
        }
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        uint64 now_ = uint64(block.timestamp);
        (bool stands,,) = l.g.hold(now_, HlAdminStorage.day());
        if (stands) {
            return "hold";
        }
        (uint64 hb, uint64 ht, uint64 db, uint64 dt) = l.g.moments();
        l.g.unpause(deliveredBlock, uint64(block.number), now_, HlAdminStorage.day());
        HlAdminStorage.noteHoldEnd(l, hb, ht, db, dt);
        return "";
    }

    /// Kind 5: `count u8 (1–4) ‖ (seat u8 ‖ key 32)…`; spent when it names any current key or the replacement's
    /// rules refuse it; both lists replaced, the gate's by its owner's call.
    function _setGuardians(bytes calldata p) private returns (string memory) {
        if (p.length == 0) {
            return "params";
        }
        uint256 n = uint8(p[0]);
        if (n == 0 || n > 4 || p.length != 1 + 33 * n) {
            return "params";
        }
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        IPausableGate.SeatKey[] memory pairs = new IPausableGate.SeatKey[](n);
        for (uint256 i = 0; i < n; i++) {
            uint256 at = 1 + 33 * i;
            (bool ok, address key) = _addr(p, at + 1);
            if (!ok) {
                return "params";
            }
            if (l.g.position(key) != Guardians.KEYS) {
                return "current key";
            }
            pairs[i] = IPausableGate.SeatKey(uint8(p[at]), key);
        }
        if (l.g.checkReplace(pairs) != bytes4(0)) {
            return "params";
        }
        (uint64 hb, uint64 ht, uint64 db, uint64 dt) = l.g.moments();
        l.g.replace(pairs, uint64(block.number), uint64(block.timestamp), HlAdminStorage.day());
        HlAdminStorage.noteHoldEnd(l, hb, ht, db, dt);
        IPausableGate(gate).setGuardians(pairs);
        return "";
    }

    /// Kind 6: `token u8 ‖ domain u32 ‖ router 32`; a zero router unenrolls the domain (a no-op when none is).
    function _enrollRouter(bytes calldata p) private returns (string memory) {
        if (p.length != 37) {
            return "params";
        }
        address t = _tokenOf(uint8(p[0]));
        if (t == address(0)) {
            return "params";
        }
        uint32 domain = uint32(bytes4(p[1:5]));
        bytes32 router = bytes32(p[5:37]);
        if (router == bytes32(0)) {
            if (ITokenSettings(t).routers(domain) != bytes32(0)) {
                ITokenSettings(t).unenrollRemoteRouter(domain);
            }
            return "";
        }
        ITokenSettings(t).enrollRemoteRouter(domain, router);
        return "";
    }

    /// Kind 8: `proxy 32 ‖ implementation 32 ‖ codehash 32 ‖ call` (the call the rest of the body, possibly empty):
    /// the proxy one of the five; the implementation's code the decision's (`_codeIs`: no code yet pends, a delegation
    /// designator or another code hash is spent); `ProxyAdmin.upgrade`, or `upgradeAndCall` with the call when there is
    /// one (so a new version's initializer is run in the upgrade, never left open), with CALL_GAS exactly — too little
    /// gas left for it reverts (`CallStarved`), pending (audit round 3, E3-CT4). The upgrade must leave the proxy on
    /// the implementation the decision pinned, or the execution reverts (`ImplementationMoved`) and the decision pends
    /// for r/hlgov to cancel: the call runs the new code as the proxy, and code can write the EIP-1967 implementation
    /// slot by `sstore`, leaving the proxy on code no hash pinned (audit round 2, E2-RT10). And it must leave the
    /// proxy where it was — its admin the ProxyAdmin, its owner (the client: its admin) HlAdmin, and each binding
    /// finishSetup held that no decision moves the deployment's (`_fixed`: a token's mailbox, scale, decimals and local
    /// domain live in the new implementation's immutables, which no code hash shows, and its ISM, hooks and fees in
    /// storage the call can write; audit round 3, E3-CT3) — or the execution reverts (`AuthorityLost`), pending too:
    /// an implementation built on another storage layout (OpenZeppelin 5's namespaced slots, a shifted field) would
    /// read its owner as zero and its initializer as never run, open to anyone's `initialize`; a token over another
    /// mailbox could be minted by it, past the gate and the client. Every answer is the proxy's, so the new code's: a
    /// call that overwrote the admin slot sends the ProxyAdmin's questions on to it, and code written to answer them as
    /// the deployment's would passes — these catch an honest mistake and a reviewed source over another binding; code
    /// built to lie is the members' review's to refuse (hlwatch's build comparison, §8).
    function _upgrade(bytes calldata p) private returns (string memory) {
        if (p.length < 96) {
            return "params";
        }
        (bool ok1, address proxy) = _addr(p, 0);
        (bool ok2, address impl) = _addr(p, 32);
        if (!ok1 || !ok2) {
            return "params";
        }
        if (proxy != mailbox && proxy != token0 && proxy != token1 && proxy != client && proxy != gate) {
            return "params";
        }
        if (!_codeIs(impl, bytes32(p[64:96]))) {
            return "codehash";
        }
        bytes calldata call = p[96:];
        IProxyAdminLike pa = IProxyAdminLike(proxyAdmin);
        if (call.length == 0) {
            pa.upgrade(proxy, impl);
        } else {
            if (gasleft() < CALL_FLOOR) {
                revert CallStarved();
            }
            pa.upgradeAndCall{gas: CALL_GAS}(proxy, impl, call);
        }
        if (pa.getProxyImplementation(proxy) != impl) {
            revert ImplementationMoved(proxy);
        }
        address holder = proxy == client ? IGnoLightClient(client).admin() : IOwnableLike(proxy).owner();
        if (pa.getProxyAdmin(proxy) != proxyAdmin || holder != address(this) || _fixed(proxy, false) != 0) {
            revert AuthorityLost(proxy);
        }
        return "";
    }

    /// Kind 9: `successor 32 ‖ codehash 32`: spent unless the successor is another contract whose code is the
    /// decision's (`_codeIs`: no code yet pends, a delegation designator or another code hash is spent), that names
    /// HlAdmin its predecessor, has not inherited, names no successor of its own and runs at this build's clock
    /// factor ("successor"), and that is bound as HlAdmin is ("bindings", `_sameBindings`). Its answers are read by
    /// plain calls: one that reverts reverts the execution (the decision pends, retried), never spends it. Every
    /// authority moves to it, then its `successorInit` takes the live number, the seats and watch slots and the hold
    /// history; HlAdmin records nothing from then on.
    function _transfer(bytes calldata p) private returns (string memory) {
        if (p.length != 64) {
            return "params";
        }
        (bool ok, address succ) = _addr(p, 0);
        if (!ok) {
            return "params";
        }
        if (succ == address(this) || !_codeIs(succ, bytes32(p[32:64]))) {
            return "codehash";
        }
        IHlAdmin s = IHlAdmin(succ);
        if (
            s.predecessor() != address(this) || s.inherited() || s.successor() != address(0)
                || s.clockFactor() != CLOCK_FACTOR
        ) {
            return "successor";
        }
        if (!_sameBindings(succ)) {
            return "bindings";
        }
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        l.successor = succ;
        IOwnableLike(proxyAdmin).transferOwnership(succ);
        IOwnableLike(mailbox).transferOwnership(succ);
        IOwnableLike(token0).transferOwnership(succ);
        IOwnableLike(token1).transferOwnership(succ);
        IOwnableLike(gate).transferOwnership(succ);
        IOwnableLike(merkleTreeHook).transferOwnership(succ);
        IOwnableLike(protocolFee).transferOwnership(succ);
        IGnoLightClient(client).setAdmin(succ);
        s.successorInit(l.lastSeq, l.g.keys, l.holds);
        emit IHlAdmin.Retired(succ);
        return "";
    }

    /// Kind 9's bindings (audit round 2, E2-RT3; fold F8): the successor must be bound to what grants power as HlAdmin
    /// is — the mailbox, the client, the gate, both tokens, the ProxyAdmin, both hooks, r/hlgov, the origin and local
    /// domains; with the HlAdminExec its runtime pins (successorInit refuses another, `ForeignExec`), the twelve. They
    /// live in its immutables, which the decision's code hash pins and no reader of the hash can see: the reviewed code
    /// over a client that verifies everything, or another r/hlgov, would take every authority and accept any message.
    /// Each view is asked of the successor and of HlAdmin itself; the answers must be the same bytes. The verifier and
    /// its code hash, the hlmailbox path's hash and the two routes are not compared: they grant nothing, and only a
    /// successor's own finishSetup reads them (checks 15 and 26–28), against the live client and tokens — after a
    /// verifier swap or a path change (kind 8), or a route moved (kind 6), HlAdmin's own are stale, and a successor
    /// bound to them could never finish its setup.
    function _sameBindings(address succ) private view returns (bool) {
        uint32[11] memory views = [
            uint32(IHlAdmin.mailbox.selector),
            uint32(IHlAdmin.client.selector),
            uint32(IHlAdmin.gate.selector),
            uint32(IHlAdmin.proxyAdmin.selector),
            uint32(IHlAdmin.merkleTreeHook.selector),
            uint32(IHlAdmin.protocolFee.selector),
            uint32(IHlAdmin.hlgov.selector),
            uint32(IHlAdmin.origin.selector),
            uint32(IHlAdmin.localDomain.selector),
            uint32(IHlAdmin.token.selector),
            uint32(IHlAdmin.token.selector)
        ];
        for (uint256 i = 0; i < 11; i++) {
            // the last two take their index: token(1), token(0)
            bytes4 v = bytes4(views[i]);
            bytes memory q = i < 9 ? abi.encodeWithSelector(v) : abi.encodeWithSelector(v, i & 1);
            if (_answer(succ, q) != _answer(address(this), q)) {
                return false;
            }
        }
        return true;
    }

    /// The keccak256 of `a`'s answer to the view call `q`. A call that fails — a revert, or gas a stranger starved —
    /// reverts the execution with its reason: the decision pends, never spent by it (ER7).
    function _answer(address a, bytes memory q) private view returns (bytes32 h) {
        assembly ("memory-safe") {
            let ok := staticcall(gas(), a, add(q, 32), mload(q), 0, 0)
            let p := mload(0x40)
            returndatacopy(p, 0, returndatasize())
            if iszero(ok) {
                revert(p, returndatasize())
            }
            h := keccak256(p, returndatasize())
        }
    }

    /// Kinds 8 and 9: whether `target`'s code is the one the decision pins — contract code (`_isCode`) whose
    /// EXTCODEHASH is `codehash` at this moment; false spends the decision. No code at all reverts (`NoCodeYet`): the
    /// decision pends until the code is deployed, as a Solana kind 8 whose buffer is not yet staged pends, so nobody
    /// burns GovDAO's 7 days by executing before the code is there. Code an earlier transaction left at an address
    /// never changes (EIP-6780) but an EIP-7702 account's, whose delegation designator is spent here.
    function _codeIs(address target, bytes32 codehash) private view returns (bool) {
        if (target.code.length == 0) {
            revert NoCodeYet(target);
        }
        return _isCode(target) && target.codehash == codehash;
    }

    /// Whether `a` holds contract code: some, and not an EIP-7702 delegation designator (`0xef0100 ‖ address`, whose
    /// EXTCODEHASH covers only the pointer: the account may re-delegate later).
    function _isCode(address a) private view returns (bool) {
        uint256 size;
        bytes32 head;
        assembly ("memory-safe") {
            size := extcodesize(a)
            let ptr := mload(0x40)
            mstore(ptr, 0)
            extcodecopy(a, ptr, 0, 3)
            head := mload(ptr)
        }
        return size != 0 && bytes3(head) != 0xef0100;
    }

    // ------------------------------------------------------------------------------------------------ the setup --

    /// `finishSetup`'s self-check (PLAN.md §5.8 step 11): reverts `SetupIncomplete(n)` at the first setting that is
    /// not the deployment's — 1 the ProxyAdmin's owner, 2 the mailbox's owner, 3 a token's owner, 4 the gate's owner,
    /// 5 the MerkleTreeHook's owner, 6 the ProtocolFee's owner (each must be HlAdmin), 7 the client's admin, 8 a
    /// proxy's admin (each of the five must be the ProxyAdmin); for each proxy, after its 8, the bindings no decision
    /// moves (`_fixed`, which kind 8 holds after every upgrade too): 9–11 the mailbox's default ISM (the gate), default
    /// hook (the MerkleTreeHook) and required hook (the ProtocolFee), 12–14 a token's ISM (the gate; in a successor's
    /// setup the gate or zero), hook (none), fee recipient and fee hook (none), 17 the gate's mailbox, client or tokens, 18
    /// the gate's guardian list (HlAdmin's), 19 a clock factor (the client's, the gate's and HlAdmin's equal), 20 the
    /// client's origin domain (HlAdmin's), and the values that live in immutables, which a code-hash audit with the
    /// immutables masked cannot see: 21 a token's mailbox (ours: a token built over another mailbox could be minted
    /// by it, past the gate and the client), 22 a token's scale (1/1), 23 a token's decimals (6), 29 the mailbox's and
    /// each token's local domain (HlAdmin's `localDomain`); then 15 a token's router for gno.land (the compiled route),
    /// 16 the ProtocolFee's maximum (0.001 ETH), 25 the MerkleTreeHook's mailbox, 26 the client's hlmailbox path hash
    /// (HlAdmin's), 27 the client's ed25519 verifier (HlAdmin's `ed25519`), 28 that verifier's code (present, no
    /// EIP-7702 designator, EXTCODEHASH HlAdmin's `ed25519CodeHash`); and at a launch (no predecessor: kinds 3, 7 and
    /// 12 and the bridge's own mints may have moved these before a successor's setup) the values the deployer wrote
    /// into storage, which no code hash pins: 12 a token's ISM the gate itself (step 8), 24 a token's supply (0), 30
    /// each token's mint limits at the gate (the launch ones HlAdmin was given), 31 the ProtocolFee's fee (0). Check
    /// 34, a launch's record empty at the start in HlAdmin's own code and its hold record one the keys' flags could
    /// have left (audit round 4, E4-CT1), is HlAdmin's (`finishSetup`, after this).
    /// Numbers 32 and 33 are retired, never reused: the mailbox's nonce and the MerkleTreeHook's count (0, E2-CT1) let
    /// any stranger's `Mailbox.dispatch` after step 9 veto the launch (audit round 2, E2-RT6); a burn the deployer
    /// dispatched before the launch is left out off chain — the launch line (`launchNonce`) leaves out every message
    /// below it, the deployer's burn included even in finishSetup's own block, and `hleth audit` lists every dispatch
    /// before it. A successor's setup (audit round 3, E3-CF10) passes on a live bridge: its supply, its ISM after a kind
    /// 3 to zero, the limits and the fee are the launch's alone to read.
    function checkSetup() external view delegated {
        address self = address(this);
        _check(IOwnableLike(proxyAdmin).owner() == self, 1);
        _check(IOwnableLike(mailbox).owner() == self, 2);
        _check(IOwnableLike(token0).owner() == self && IOwnableLike(token1).owner() == self, 3);
        _check(IOwnableLike(gate).owner() == self, 4);
        _check(IOwnableLike(merkleTreeHook).owner() == self, 5);
        _check(IOwnableLike(protocolFee).owner() == self, 6);
        _check(IGnoLightClient(client).admin() == self, 7);
        address[5] memory proxies = [mailbox, token0, token1, client, gate];
        for (uint256 i = 0; i < 5; i++) {
            // a proxy whose admin is another answers the ProxyAdmin's question from its implementation, which
            // reverts: that is check 8 failing too
            try IProxyAdminLike(proxyAdmin).getProxyAdmin(proxies[i]) returns (address a) {
                _check(a == proxyAdmin, 8);
            } catch {
                revert SetupIncomplete(8);
            }
            uint8 broken = _fixed(proxies[i], launch);
            _check(broken == 0, broken);
        }
        _check(ITokenSettings(token0).routers(origin) == route0 && ITokenSettings(token1).routers(origin) == route1, 15);
        _check(IProtocolFeeSettings(protocolFee).MAX_PROTOCOL_FEE() == MAX_PROTOCOL_FEE, 16);
        _check(IMerkleTreeHookLike(merkleTreeHook).mailbox() == mailbox, 25);
        _check(IGnoLightClient(client).mailboxPkgPathHash() == mailboxPkgPathHash, 26);
        _check(address(IGnoLightClient(client).ED25519()) == ed25519, 27);
        _check(_isCode(ed25519) && ed25519.codehash == ed25519CodeHash, 28);
        if (launch) {
            _checkLaunchValues();
        }
    }

    /// The bindings of `proxy` (one of the five) that finishSetup holds and no decision moves, by finishSetup's
    /// numbers: the first that is not the deployment's, 0 when each is. The mailbox: 9–11 its default ISM and hooks,
    /// 29 its local domain. The client: 19 its clock factor, 20 its origin domain. The gate: 17 its mailbox, client and
    /// tokens, 18 its guardian list (kind 5 moves it with HlAdmin's), 19 its clock factor. A token: 12 its ISM (the
    /// gate; zero too unless `strict` — kind 3's other value, which falls back to the mailbox's default ISM, the gate:
    /// a successor's setup and a kind 8 take it, a launch does not), 13–14 no hook, fee recipient or fee hook, 21–23
    /// our mailbox, scale 1/1, 6 decimals, 29 its local domain. Read after every kind 8 too (E3-CT3), so no upgrade
    /// leaves a proxy on a binding a decision could not have given it.
    function _fixed(address proxy, bool strict) private view returns (uint8) {
        if (proxy == mailbox) {
            IMailboxSettings m = IMailboxSettings(proxy);
            if (m.defaultIsm() != gate) {
                return 9;
            }
            if (m.defaultHook() != merkleTreeHook) {
                return 10;
            }
            if (m.requiredHook() != protocolFee) {
                return 11;
            }
            return m.localDomain() == localDomain ? 0 : 29;
        }
        if (proxy == client) {
            if (IGnoLightClient(proxy).clockFactor() != CLOCK_FACTOR) {
                return 19;
            }
            return IGnoLightClient(proxy).originDomain() == origin ? 0 : 20;
        }
        if (proxy == gate) {
            IPausableGate g = IPausableGate(proxy);
            if (g.mailbox() != mailbox || g.client() != client || g.token(0) != token0 || g.token(1) != token1) {
                return 17;
            }
            address[6] memory theirs = g.keys();
            HlAdminStorage.Layout storage l = HlAdminStorage.layout();
            for (uint256 i = 0; i < 6; i++) {
                if (theirs[i] != l.g.keys[i]) {
                    return 18;
                }
            }
            return g.clockFactor() == CLOCK_FACTOR ? 0 : 19;
        }
        ITokenSettings s = ITokenSettings(proxy);
        address ism = s.interchainSecurityModule();
        if (ism != gate && (strict || ism != address(0))) {
            return 12;
        }
        if (s.hook() != address(0)) {
            return 13;
        }
        if (s.feeRecipient() != address(0) || s.feeHook() != address(0)) {
            return 14;
        }
        if (s.mailbox() != mailbox) {
            return 21;
        }
        if (s.scaleNumerator() != 1 || s.scaleDenominator() != 1) {
            return 22;
        }
        if (s.decimals() != DECIMALS) {
            return 23;
        }
        return s.localDomain() == localDomain ? 0 : 29;
    }

    /// Checks 24, 30 and 31, at a launch: no token minted yet, the gate's mint limits the launch ones, the protocol fee
    /// 0 (12 strict is `_fixed`'s; 34 is HlAdmin's).
    function _checkLaunchValues() private view {
        _check(ITokenSettings(token0).totalSupply() == 0 && ITokenSettings(token1).totalSupply() == 0, 24);
        IPausableGate g = IPausableGate(gate);
        IPausableGate.MintLimit memory a = g.limits(0);
        IPausableGate.MintLimit memory b = g.limits(1);
        _check(
            a.maxPerMessage == maxPerMessage0 && a.dayCap == dayCap0 && b.maxPerMessage == maxPerMessage1
                && b.dayCap == dayCap1,
            30
        );
        _check(IProtocolFeeSettings(protocolFee).protocolFee() == 0, 31);
    }

    function _check(bool ok, uint8 check) private pure {
        if (!ok) {
            revert SetupIncomplete(check);
        }
    }

    // ---------------------------------------------------------------------------------------------- the bytes --

    function _tokenOf(uint8 i) private view returns (address) {
        if (i == 0) {
            return token0;
        }
        if (i == 1) {
            return token1;
        }
        return address(0);
    }

    /// An address carried in 32 bytes, left-padded: refused when the top 12 bytes are not zero.
    function _addr(bytes calldata p, uint256 at) private pure returns (bool, address) {
        uint256 w = uint256(bytes32(p[at:at + 32]));
        if (w >> 160 != 0) {
            return (false, address(0));
        }
        return (true, address(uint160(w)));
    }
}

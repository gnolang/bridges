// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {IHlAdmin} from "./interfaces/IHlAdmin.sol";
import {IPausableGate} from "./interfaces/IPausableGate.sol";
import {Guardians} from "./lib/Guardians.sol";
import {HlAdminExec, IMailboxSettings, IOwnableLike} from "./admin/HlAdminExec.sol";
import {HlAdminStorage, SetupIncomplete} from "./admin/HlAdminStorage.sol";

/// The admin contract (eth/bridge/PLAN.md §6.5, §13; the Solana plan's admin program, ported from
/// bridge/programs/admin-program rule by rule): plain code, never behind a proxy, so immutable from its deployment.
/// After the setup step it holds every Ethereum authority — the ProxyAdmin, the mailbox's, both tokens', the gate's
/// and both hooks' ownership, the client's admin role — and acts on nothing but decisions r/hlgov sent from gno.land,
/// proven by the raw light client (its ISM, fixed here: no flag of the gate ever blocks a decision) and delivered by
/// the mailbox.
///
/// The lifecycle: a message whose 9-byte prefix (kind u8 ‖ number u64 BE) parses at `last + 1` is recorded whatever
/// its kind (an unknown kind, or a body over 512 bytes, recorded and spent); one at or below `last` is ignored; one
/// above `last + 1` is refused and stays deliverable. A decision exists only at a number recorded here, in (start,
/// last]: a record no delivery wrote — a constructor's initcode can write any storage — never runs, and every number
/// that can run is within a cancel's reach. A cancel executes as it is recorded (a list deletes the named
/// pending decisions; a range is stored, merged and bounded, and every pending decision inside it dies). An unpause
/// matures 24 hours after delivery and is spent under a hold on its object; every other kind matures 7 days after
/// delivery, deferred by any flag on HlAdmin and by a hold, except kind 5, which lands through a lone flag and
/// through a hold on the later of its own 7th day and the 21st day of the earliest hold overlapping its pending
/// life. Anyone executes a matured decision; one whose parameters fail is spent; a call that reverts, or a kind 8 or 9
/// whose code is not deployed yet, leaves it pending for a retry. Kind 9 hands every authority to a successor HlAdmin
/// bound to the same contracts, keys and domains, and retires this one.
///
/// The kinds' execution and `finishSetup`'s checks run in HlAdminExec, created by this constructor and called only
/// by delegatecall (EIP-170's bound: HlAdmin whole is past 24,576 bytes); both halves keep one ERC-7201 layout.
contract HlAdmin is IHlAdmin {
    using Guardians for Guardians.State;

    error ZeroAddress();
    error ZeroSetting();
    error NotTheMailbox();
    error Value();
    error NotInherited();
    error RetiredAdmin();
    error WrongOrigin();
    error WrongSender();
    error ShortBody();
    error OutOfOrder();
    error WrongDecision();
    error NotMature();
    error Deferred();
    error WrongAuthority();
    error SetupAlreadyDone();
    error ProxyAdminNotHeld();
    error ForeignExec();

    uint8 internal constant KIND_CANCEL = 1;
    uint8 internal constant KIND_UNPAUSE = 2;
    uint8 internal constant KIND_SET_GUARDIANS = 5;
    uint8 internal constant KIND_SET_MINT_LIMITS = 12;

    /// A decision body past this is recorded and spent (r/hlgov refuses one at creation and at dispatch, §13).
    uint256 internal constant MAX_BODY = 512;
    /// The most numbers one list cancel names; the most stored cancelled ranges.
    uint256 internal constant MAX_CANCEL = 16;
    uint256 internal constant MAX_CANCELLED = 64;
    /// The highest start a launch takes (audit round 3, E3-GS4): r/hlgov's counter starts at 0 on a chain's first
    /// deployment and only its `sync sequence` raises it, by at most 2^32 a proposal (its syncReach), so a start above
    /// would take r/hlgov more than one proposal to reach — 2^64−1, every decision ignored for good — and one at or
    /// below is a single sync away from any counter at or below it.
    uint64 internal constant MAX_START = 1 << 32;

    /// The deploy's settings (PLAN.md §5.8 step 7): every contract HlAdmin holds or reads; the ed25519 verifier the
    /// client implementation must call (lane E-M2a's `Ed25519Verifier`) and the EXTCODEHASH its code must have (this
    /// tree's build); Ethereum's Hyperlane domain (the mailbox's and the tokens' `localDomain`) and gno.land's;
    /// r/hlgov's address and the two route realms' (32 bytes, the gno address left-padded); keccak256 of the
    /// hlmailbox realm's path (what `finishSetup` requires the client to carry); the four seats and two watch slots;
    /// the gate's launch mint limits per token; the number the sequence starts after (r/hlgov's counter, at most
    /// MAX_START: `OutOfOrder` above); and the
    /// predecessor (zero at launch; a successor names it and gives no seats, slots or number: successorInit writes
    /// them; its mint limits are not read, kind 12 may have moved the gate's). At a launch the ProxyAdmin must be this
    /// contract's already: the setup hands it to HlAdmin's CREATE address at step 1, before any proxy exists, so the
    /// deployer never holds upgrade power (audit round 2, E2-CT1); a successor receives it by kind 9. A successor's
    /// verifier, its code hash, routes and path hash are its proposer's: kind 9 compares none of them with its
    /// predecessor's (fold F8), and only its own `finishSetup` holds them to the live client and tokens.
    struct Config {
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
        bytes32 hlgov;
        bytes32 route0;
        bytes32 route1;
        bytes32 mailboxPkgPathHash;
        address[4] seats;
        address[2] watch;
        uint64[2] maxPerMessage;
        uint64[2] dayCap;
        uint64 startSequence;
        address predecessor;
    }

    address public immutable override mailbox;
    address public immutable override client;
    address public immutable override gate;
    address internal immutable _token0;
    address internal immutable _token1;
    address public immutable override proxyAdmin;
    address public immutable override merkleTreeHook;
    address public immutable override protocolFee;
    address public immutable override ed25519;
    bytes32 public immutable override ed25519CodeHash;
    uint32 public immutable override localDomain;
    uint32 public immutable override origin;
    bytes32 public immutable override hlgov;
    bytes32 internal immutable _route0;
    bytes32 internal immutable _route1;
    bytes32 public immutable override mailboxPkgPathHash;
    address public immutable override predecessor;
    /// The code the kinds run in (delegatecall only).
    HlAdminExec public immutable exec;
    /// The EXTCODEHASH of the HlAdminExec this constructor created. `exec` names it by its CREATE nonce alone, so a
    /// code hash pinning this contract's runtime (kind 9's) would not pin the code there: an initcode can put other
    /// code at that address and return this runtime. This hash, inside the runtime, does; nothing runs there but it.
    bytes32 internal immutable _execCodeHash;
    /// A launch's start (r/hlgov's counter at the deploy, at most MAX_START; 0 for a successor, whose start
    /// successorInit writes): a floor under every decision held in the code, where the audit reads it against r/hlgov's
    /// counter, and what a launch's check 34 holds the stored record to (E3-CT2). The stored start and last number an
    /// initcode can plant together, and a planted `setupDone` skips check 34 altogether (the audit's to refuse); this
    /// it cannot move without its runtime differing from the build's in this value.
    uint64 internal immutable _startSequence;

    constructor(Config memory c) {
        if (
            c.mailbox == address(0) || c.client == address(0) || c.gate == address(0) || c.token0 == address(0)
                || c.token1 == address(0) || c.proxyAdmin == address(0) || c.merkleTreeHook == address(0)
                || c.protocolFee == address(0) || c.ed25519 == address(0) || c.hlgov == bytes32(0)
                || c.route0 == bytes32(0) || c.route1 == bytes32(0)
        ) {
            revert ZeroAddress();
        }
        if (c.ed25519CodeHash == bytes32(0) || c.localDomain == 0 || c.origin == 0) {
            revert ZeroSetting();
        }
        mailbox = c.mailbox;
        client = c.client;
        gate = c.gate;
        _token0 = c.token0;
        _token1 = c.token1;
        proxyAdmin = c.proxyAdmin;
        merkleTreeHook = c.merkleTreeHook;
        protocolFee = c.protocolFee;
        ed25519 = c.ed25519;
        ed25519CodeHash = c.ed25519CodeHash;
        localDomain = c.localDomain;
        origin = c.origin;
        hlgov = c.hlgov;
        _route0 = c.route0;
        _route1 = c.route1;
        mailboxPkgPathHash = c.mailboxPkgPathHash;
        predecessor = c.predecessor;
        _startSequence = c.startSequence;
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        if (c.predecessor == address(0)) {
            if (IOwnableLike(c.proxyAdmin).owner() != address(this)) {
                revert ProxyAdminNotHeld();
            }
            if (c.startSequence > MAX_START) {
                revert OutOfOrder();
            }
            l.g.init([c.seats[0], c.seats[1], c.seats[2], c.seats[3], c.watch[0], c.watch[1]]);
            l.lastSeq = c.startSequence;
            l.startSeq = c.startSequence;
        } else {
            // a successor's seats, watch slots and number are its predecessor's live ones, by successorInit
            for (uint256 i = 0; i < 4; i++) {
                if (c.seats[i] != address(0)) {
                    revert Guardians.BadSeat();
                }
            }
            if (c.watch[0] != address(0) || c.watch[1] != address(0) || c.predecessor == address(this)) {
                revert Guardians.BadSeat();
            }
            if (c.startSequence != 0) {
                revert OutOfOrder();
            }
        }
        HlAdminExec x = new HlAdminExec(
            HlAdminExec.Settings({
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
            })
        );
        exec = x;
        _execCodeHash = address(x).codehash;
    }

    // --------------------------------------------------------------------------------------------- recording --

    /// @inheritdoc IHlAdmin
    function handle(uint32 origin_, bytes32 sender, bytes calldata body) external payable override {
        if (msg.sender != mailbox) {
            revert NotTheMailbox();
        }
        if (msg.value != 0) {
            revert Value();
        }
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        if (_awaitsInheritance(l)) {
            // a successor takes no number before successorInit wrote its predecessor's: the message stays deliverable
            revert NotInherited();
        }
        if (l.successor != address(0)) {
            // retired by kind 9: nothing is recorded, and the message stays deliverable for the successor
            revert RetiredAdmin();
        }
        if (origin_ != origin) {
            revert WrongOrigin();
        }
        if (sender != hlgov) {
            revert WrongSender();
        }
        if (body.length < 9) {
            revert ShortBody();
        }
        uint8 kind = uint8(body[0]);
        uint64 seq = uint64(bytes8(body[1:9]));
        uint64 last = l.lastSeq;
        if (seq <= last) {
            emit DecisionIgnored(seq);
            return;
        }
        if (seq != last + 1) {
            revert OutOfOrder();
        }
        l.lastSeq = seq;
        // the record at `seq` is this delivery's alone: whatever another hand left there (a constructor's initcode
        // can write any storage) is no decision from now on, whether this one pends or not (audit round 2, E2-RT1);
        // and no write below walks what was there — a planted params length would make the write a gas bomb, or a
        // panic, and this number undeliverable for good — so its length word is cleared first (its data, if any, is
        // unreachable from then on)
        HlAdminStorage.Pending storage r = l.decisions[seq];
        r.deliveredBlock = 0;
        bytes storage stale = r.params;
        assembly ("memory-safe") {
            sstore(stale.slot, 0)
        }
        if (!_knownKind(kind) || body.length > MAX_BODY) {
            emit DecisionRecorded(seq, kind, 0);
            emit DecisionSpent(seq, kind, body.length > MAX_BODY ? "too long" : "unknown kind");
            return;
        }
        if (kind == KIND_CANCEL) {
            emit DecisionRecorded(seq, kind, uint64(block.timestamp));
            (bool ok, uint64[2][] memory ranges) = _parseCancel(body[9:]);
            if (!ok) {
                emit DecisionSpent(seq, kind, "params");
                return;
            }
            _applyCancel(l, ranges);
            emit DecisionExecuted(seq, kind);
            return;
        }
        l.decisions[seq] =
            HlAdminStorage.Pending(tx.origin, uint64(block.timestamp), kind, uint64(block.number), body[9:]);
        l.pending += 1;
        emit DecisionRecorded(seq, kind, uint64(block.timestamp) + _delay(kind));
    }

    /// @inheritdoc IHlAdmin
    function interchainSecurityModule() external view override returns (address) {
        return client;
    }

    /// A cancel's ranges: `salt u64 ‖ form u8`, then form 0 `count u8 (1–16) ‖ (channel u8 = 0 ‖ number u64)…` or
    /// form 1 `channel u8 = 0 ‖ from u64 ‖ to u64` (from ≤ to); nothing after.
    function _parseCancel(bytes calldata p) private pure returns (bool, uint64[2][] memory ranges) {
        if (p.length < 10) {
            return (false, ranges);
        }
        uint8 form = uint8(p[8]);
        if (form == 0) {
            uint256 n = uint8(p[9]);
            if (n == 0 || n > MAX_CANCEL || p.length != 10 + 9 * n) {
                return (false, ranges);
            }
            ranges = new uint64[2][](n);
            for (uint256 i = 0; i < n; i++) {
                uint256 at = 10 + 9 * i;
                if (uint8(p[at]) != 0) {
                    return (false, ranges);
                }
                uint64 x = uint64(bytes8(p[at + 1:at + 9]));
                ranges[i] = [x, x];
            }
            return (true, ranges);
        }
        if (form == 1) {
            if (p.length != 26 || uint8(p[9]) != 0) {
                return (false, ranges);
            }
            uint64 from = uint64(bytes8(p[10:18]));
            uint64 to = uint64(bytes8(p[18:26]));
            if (from > to) {
                return (false, ranges);
            }
            ranges = new uint64[2][](1);
            ranges[0] = [from, to];
            return (true, ranges);
        }
        return (false, ranges);
    }

    /// Cancels: a number or a one-number range that pends is deleted now; a longer range, clamped at the last number
    /// (it cancels what pends now, never a number recorded later), is stored while anything pends, and every pending
    /// decision inside it dies (`execute` closes it). Every number that can run, (startSeq, lastSeq], is within a
    /// cancel's reach; at or below startSeq no number was recorded here, and nothing there is closed.
    function _applyCancel(HlAdminStorage.Layout storage l, uint64[2][] memory ranges) private {
        uint64 last = l.lastSeq;
        for (uint256 i = 0; i < ranges.length; i++) {
            uint64 from = ranges[i][0];
            uint64 to = ranges[i][1] < last ? ranges[i][1] : last;
            if (from > to) {
                continue;
            }
            if (from == to) {
                if (_isDecision(l, from)) {
                    _close(l, from);
                    emit DecisionCancelled(from);
                }
                continue;
            }
            if (l.pending != 0) {
                _addCancelled(l, from, to);
                emit RangeCancelled(from, to);
            }
        }
    }

    // ---------------------------------------------------------------------------------------------- execution --

    /// @inheritdoc IHlAdmin
    function execute(uint64 number) external override {
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        if (_awaitsInheritance(l)) {
            revert NotInherited();
        }
        if (!_isDecision(l, number)) {
            revert WrongDecision();
        }
        HlAdminStorage.Pending memory d = l.decisions[number];
        if (l.successor != address(0)) {
            // retired: the successor holds every authority, nothing pending here can run
            _close(l, number);
            emit DecisionSpent(number, d.kind, "retired");
            return;
        }
        if (_isCancelled(l, number)) {
            _close(l, number);
            emit DecisionCancelled(number);
            return;
        }
        uint64 now_ = uint64(block.timestamp);
        (bool holdStands,, uint64 holdStart) = l.g.hold(now_, HlAdminStorage.day());
        uint64 matureAt = d.deliveredTime + _delay(d.kind);
        if (d.kind == KIND_SET_GUARDIANS) {
            if (holdStands) {
                uint64 floor = _replacementFloor(l, d.deliveredTime, holdStart);
                if (floor > matureAt) {
                    matureAt = floor;
                }
            }
        } else if (_sevenDay(d.kind) && (holdStands || l.g.anyFlag())) {
            revert Deferred();
        }
        if (now_ < matureAt) {
            revert NotMature();
        }
        // effects first: a call below that reverts reverts this, and the decision stays pending for a retry
        _close(l, number);
        bytes memory ret = _delegate(abi.encodeCall(HlAdminExec.run, (d.kind, d.params, d.deliveredBlock)));
        string memory spent = abi.decode(ret, (string));
        if (bytes(spent).length != 0) {
            emit DecisionSpent(number, d.kind, spent);
        } else {
            emit DecisionExecuted(number, d.kind);
        }
    }

    /// Runs HlAdminExec's code as this contract; a revert there reverts here with its own reason.
    function _delegate(bytes memory call) private returns (bytes memory) {
        _ownExec();
        (bool ok, bytes memory ret) = address(exec).delegatecall(call);
        if (!ok) {
            assembly ("memory-safe") {
                revert(add(ret, 32), mload(ret))
            }
        }
        return ret;
    }

    /// The code at `exec` is the HlAdminExec this contract's constructor created (`_execCodeHash`), or nothing runs:
    /// a constructor's initcode can put other code there and return this runtime unchanged (E2-RT1's class).
    function _ownExec() private view {
        if (address(exec).codehash != _execCodeHash) {
            revert ForeignExec();
        }
    }

    /// A replacement's floor while a hold stands: the earliest hold overlapping [its delivery, now] — the standing
    /// one, or an ended one whose end is at or after the delivery — plus 21 days at the clock factor.
    function _replacementFloor(HlAdminStorage.Layout storage l, uint64 deliveredTime, uint64 standing)
        private
        view
        returns (uint64)
    {
        uint64 floor = standing;
        for (uint256 i = 0; i < l.holds.length; i++) {
            Hold memory h = l.holds[i];
            if (h.end >= deliveredTime && h.start < floor) {
                floor = h.start;
            }
        }
        return floor + 21 days / CLOCK_FACTOR;
    }

    /// Whether a pending decision stands at `number`: a record `handle` wrote, which only a number in (startSeq,
    /// lastSeq] can hold — at any other number nothing was ever delivered here, so a record there is one no delivery
    /// wrote (an initcode's), and it never runs (audit round 2, E2-RT1). A launch's start is also its immutable
    /// `_startSequence`, which no planted storage moves (a successor's is 0: its stored start is successorInit's).
    function _isDecision(HlAdminStorage.Layout storage l, uint64 number) private view returns (bool) {
        return _startSequence < number && l.startSeq < number && number <= l.lastSeq
            && l.decisions[number].deliveredBlock != 0;
    }

    /// Ends a pending decision: deleted, counted off; at zero pending every stored range is dead and pruned.
    function _close(HlAdminStorage.Layout storage l, uint64 number) private {
        delete l.decisions[number];
        uint32 n = l.pending - 1;
        l.pending = n;
        if (n == 0) {
            delete l.cancelled;
        }
    }

    // ------------------------------------------------------------------------------------------- the hand-over --

    /// @inheritdoc IHlAdmin
    function successorInit(uint64 lastSeq_, address[6] calldata keys_, Hold[] calldata holds_) external override {
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        if (!_awaitsInheritance(l)) {
            revert NotInherited();
        }
        if (msg.sender != predecessor) {
            revert WrongAuthority();
        }
        if (holds_.length > HlAdminStorage.MAX_HOLDS) {
            revert WrongDecision();
        }
        // a successor whose HlAdminExec is foreign could never run its own code: refusing here reverts the
        // predecessor's kind 9, which pends for a cancel, instead of handing every authority to it
        _ownExec();
        // the whole record is the predecessor's: what this contract's own initcode may have left — a pending count,
        // cancelled ranges (never pruned while the count stands: every decision would die), holds, a setup done, a
        // launch line, a decision at a number at or below the one inherited — is written over (audit round 2, E2-RT1;
        // the launch line is the launch HlAdmin's alone)
        l.lastSeq = lastSeq_;
        l.startSeq = lastSeq_;
        l.pending = 0;
        l.setupDone = false;
        l.launchNonce = 0;
        delete l.cancelled;
        delete l.holds;
        l.g.init(keys_);
        for (uint256 i = 0; i < holds_.length; i++) {
            l.holds.push(holds_[i]);
        }
        l.inherited = true;
        emit Inherited(predecessor, lastSeq_);
    }

    function _awaitsInheritance(HlAdminStorage.Layout storage l) private view returns (bool) {
        return predecessor != address(0) && !l.inherited;
    }

    // -------------------------------------------------------------------------------------------------- flags --

    /// @inheritdoc IHlAdmin
    function setFlag() external override {
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        (uint64 hb, uint64 ht, uint64 db, uint64 dt) = l.g.moments();
        uint256 i = l.g.setFlag(msg.sender, uint64(block.number), uint64(block.timestamp), HlAdminStorage.day());
        HlAdminStorage.noteHoldEnd(l, hb, ht, db, dt);
        emit FlagSet(msg.sender, uint8(i + 1));
    }

    /// @inheritdoc IHlAdmin
    function clearFlag() external override {
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        (uint64 hb, uint64 ht, uint64 db, uint64 dt) = l.g.moments();
        uint256 i = l.g.clearFlag(msg.sender, uint64(block.number), uint64(block.timestamp), HlAdminStorage.day());
        HlAdminStorage.noteHoldEnd(l, hb, ht, db, dt);
        emit FlagCleared(msg.sender, uint8(i + 1));
    }

    // ------------------------------------------------------------------------------------------ cancel ranges --

    function _isCancelled(HlAdminStorage.Layout storage l, uint64 number) private view returns (bool) {
        for (uint256 i = 0; i < l.cancelled.length; i++) {
            if (l.cancelled[i][0] <= number && number <= l.cancelled[i][1]) {
                return true;
            }
        }
        return false;
    }

    /// Adds a cancelled range, merging with every stored one it overlaps or touches; with the store full, merges it
    /// with the nearest stored one rather than refuse (the gap's numbers die too, which only kills pending decisions).
    function _addCancelled(HlAdminStorage.Layout storage l, uint64 from, uint64 to) private {
        uint256 i = 0;
        while (i < l.cancelled.length) {
            uint64 cf = l.cancelled[i][0];
            uint64 ct = l.cancelled[i][1];
            if (cf <= _satInc(to) && from <= _satInc(ct)) {
                if (cf < from) {
                    from = cf;
                }
                if (ct > to) {
                    to = ct;
                }
                _removeCancelled(l, i);
            } else {
                i++;
            }
        }
        if (l.cancelled.length >= MAX_CANCELLED) {
            uint256 best;
            uint64 bestGap = type(uint64).max;
            for (uint256 j = 0; j < l.cancelled.length; j++) {
                uint64 cf = l.cancelled[j][0];
                uint64 ct = l.cancelled[j][1];
                uint64 gap = ct < from ? from - ct : (to < cf ? cf - to : 0);
                if (gap < bestGap) {
                    (best, bestGap) = (j, gap);
                }
            }
            if (l.cancelled[best][0] < from) {
                from = l.cancelled[best][0];
            }
            if (l.cancelled[best][1] > to) {
                to = l.cancelled[best][1];
            }
            _removeCancelled(l, best);
        }
        l.cancelled.push([from, to]);
    }

    /// Removes the i-th stored range, keeping the order of the rest.
    function _removeCancelled(HlAdminStorage.Layout storage l, uint256 i) private {
        uint256 n = l.cancelled.length;
        for (uint256 k = i; k + 1 < n; k++) {
            l.cancelled[k] = l.cancelled[k + 1];
        }
        l.cancelled.pop();
    }

    function _satInc(uint64 x) private pure returns (uint64) {
        return x == type(uint64).max ? x : x + 1;
    }

    // ------------------------------------------------------------------------------------------------ the setup --

    /// @inheritdoc IHlAdmin
    function finishSetup() external override {
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        if (l.setupDone) {
            revert SetupAlreadyDone();
        }
        _delegate(abi.encodeCall(HlAdminExec.checkSetup, ()));
        if (predecessor == address(0)) {
            // check 34: the record a launch's constructor wrote — the last number and the stored start both the
            // immutable start, nothing pending, no cancelled range, no successor — so no number ever held a decision
            // that no delivery wrote (audit round 2, E2-RT1: the initcode can write any storage; only `handle` moves the
            // last number) and nothing planted wedges the sequence: a last/start pair ahead of r/hlgov ignores every
            // decision, one behind it refuses r/hlgov's next number for good, a successor retires this at birth (round
            // 3, E3-CT2). Against the immutable, which no initcode moves (the stored pair it moves together).
            // And the hold record (round 4, E4-CT1): before the setup only the keys' flags write it (no decision has
            // run: the last number is the start), so a record they never leave is an initcode's — more than MAX_HOLDS
            // holds (a ninth merges the two oldest; nine made every kind 9 pend for good, successorInit taking eight),
            // a hold that ends at or after now (it would overlap a decision, every one delivered from now on, and lower
            // kind 5's floor under a later hold), a moment of the standing hold ahead of now (each is a block's time: a
            // dip dated ahead holds HlAdmin with no flag up, a start dated ahead keeps every replacement out while it
            // stands), or a hold stored with neither two guardian flags nor a dip (the next guardian flag revives it,
            // its start with it). No new veto: a hold the guardians set before the setup, standing, dipping or ended,
            // passes; one that ended in this very second waits a block. What else an initcode can write reads as the
            // keys' own — a standing flag, a standing hold's start, the keys — and is the audit's to refuse (`hleth
            // audit`: the creation input byte for byte, every flag and hold held to its log).
            uint64 start = _startSequence;
            uint256 n = l.holds.length;
            Guardians.State storage g = l.g;
            bool planted = n > HlAdminStorage.MAX_HOLDS || g.holdTime > block.timestamp || g.dipTime > block.timestamp
                || (g.holdBlock != 0 && g.dipBlock == 0 && g.guardianFlags() < 2);
            for (uint256 i = 0; !planted && i < n; i++) {
                planted = l.holds[i].end >= block.timestamp;
            }
            if (
                l.lastSeq != start || l.startSeq != start || l.pending != 0 || l.cancelled.length != 0
                    || l.successor != address(0) || planted
            ) {
                revert SetupIncomplete(34);
            }
            // the launch line (audit round 2, E2-RT6): a message below it — anyone's dispatch after step 9, or the
            // deployer's while it owned the mailbox and the tokens — is none of the bridge's burns; it vetoes nothing
            l.launchNonce = IMailboxSettings(mailbox).nonce();
        }
        l.setupDone = true;
        emit SetupDone();
    }

    // ---------------------------------------------------------------------------------------------- the kinds --

    function _knownKind(uint8 kind) private pure returns (bool) {
        return (kind >= 1 && kind <= 9) || kind == KIND_SET_MINT_LIMITS;
    }

    /// Kinds 3–9 and 12: the 7-day group.
    function _sevenDay(uint8 kind) private pure returns (bool) {
        return (kind >= 3 && kind <= 9) || kind == KIND_SET_MINT_LIMITS;
    }

    /// The clock of a kind (seconds after delivery), without the hold rule of kind 5.
    function _delay(uint8 kind) private pure returns (uint64) {
        if (kind == KIND_CANCEL) {
            return 0;
        }
        if (kind == KIND_UNPAUSE) {
            return HlAdminStorage.day();
        }
        return uint64(7 days) / CLOCK_FACTOR;
    }

    // ------------------------------------------------------------------------------------------------- views --

    function lastSeq() external view override returns (uint64) {
        return HlAdminStorage.layout().lastSeq;
    }

    function pending() external view override returns (uint32) {
        return HlAdminStorage.layout().pending;
    }

    function decision(uint64 number) external view override returns (Decision memory r) {
        HlAdminStorage.Layout storage l = HlAdminStorage.layout();
        if (_isDecision(l, number)) {
            HlAdminStorage.Pending storage d = l.decisions[number];
            r = Decision(number, d.kind, d.deliveredBlock, d.deliveredTime, d.deliverer, d.params);
        }
    }

    function isCancelled(uint64 number) external view override returns (bool) {
        return _isCancelled(HlAdminStorage.layout(), number);
    }

    function cancelledRanges() external view override returns (uint64[2][] memory) {
        return HlAdminStorage.layout().cancelled;
    }

    function holds() external view override returns (Hold[] memory) {
        return HlAdminStorage.layout().holds;
    }

    function keys() external view override returns (address[6] memory) {
        return HlAdminStorage.layout().g.keys;
    }

    function flags() external view override returns (IPausableGate.Flag[6] memory) {
        return HlAdminStorage.layout().g.flags();
    }

    function hold() external view override returns (IPausableGate.Flag memory) {
        return HlAdminStorage.layout().g.holdView(uint64(block.timestamp), HlAdminStorage.day());
    }

    function setupDone() external view override returns (bool) {
        return HlAdminStorage.layout().setupDone;
    }

    function launchNonce() external view override returns (uint32) {
        return HlAdminStorage.layout().launchNonce;
    }

    function successor() external view override returns (address) {
        return HlAdminStorage.layout().successor;
    }

    function inherited() external view override returns (bool) {
        return HlAdminStorage.layout().inherited;
    }

    function clockFactor() external pure override returns (uint64) {
        return CLOCK_FACTOR;
    }

    function token(uint8 i) external view override returns (address) {
        return i == 0 ? _token0 : (i == 1 ? _token1 : address(0));
    }

    function route(uint8 i) external view override returns (bytes32) {
        return i == 0 ? _route0 : (i == 1 ? _route1 : bytes32(0));
    }
}

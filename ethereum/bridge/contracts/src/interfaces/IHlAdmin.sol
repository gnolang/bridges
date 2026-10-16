// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {IPausableGate} from "./IPausableGate.sol";

/// The admin contract (eth/bridge/PLAN.md §6.5; the Solana plan's admin program): immutable, it holds every
/// Ethereum authority after the setup step and acts only on decisions r/hlgov sent from gno.land, proven by the
/// light client and delivered by the mailbox. Decisions: PLAN.md §13's kind table.
interface IHlAdmin {
    /// A hold that ended: when its guardian flags reached two, and when it ended (seconds).
    struct Hold {
        uint64 start;
        uint64 end;
    }

    /// A pending decision.
    struct Decision {
        uint64 number;
        uint8 kind;
        uint64 deliveredBlock;
        uint64 deliveredTime;
        address deliverer;
        bytes params;
    }

    /// Hyperlane's recipient interface: only the mailbox; origin gno.land's domain; sender r/hlgov. Records a
    /// decision at last + 1 (any kind; an unknown one recorded and spent), ignores one at or below last, refuses one
    /// above last + 1; a cancel executes here. Refuses value.
    function handle(uint32 origin, bytes32 sender, bytes calldata message) external payable;
    /// The raw light client (fixed in the code): decisions skip the gate, so no flag ever blocks a cancel.
    function interchainSecurityModule() external view returns (address);

    /// Anyone executes a matured decision (or closes a cancelled one). Only a number this contract recorded, in
    /// (its start, lastSeq], holds a decision: any other reverts `WrongDecision`, whatever its storage holds.
    function execute(uint64 number) external;

    /// A seat's or watch slot's own flag on this object.
    function setFlag() external;
    function clearFlag() external;

    /// Permissionless and self-checking: every authority is this contract's and every setting the deployment's (at a
    /// launch also the launch values: no supply, the launch limits, no fee, each token's ISM the gate, the record
    /// empty at the start, and a hold record the keys' flags could have left: audit round 4, E4-CT1). A successor's
    /// holds its own verifier, routes and path hash, which kind 9 did not compare (fold F8), to what runs, and passes
    /// on a live bridge (audit round 3, E3-CF10).
    function finishSetup() external;

    /// Kind 9's hand-over into a successor: only its predecessor, once. It writes the successor's whole record: the
    /// number (its start too), the keys, the holds; nothing pending, cancelled or set up, and no launch line (the
    /// launch's own is read from the launch HlAdmin).
    function successorInit(uint64 lastSeq, address[6] calldata keys, Hold[] calldata holds) external;

    function lastSeq() external view returns (uint64);
    function pending() external view returns (uint32);
    /// The pending decision at `number`; all zero where none stands (as `execute` reads it: a number this contract
    /// recorded, in (its start, lastSeq], whose record is still there).
    function decision(uint64 number) external view returns (Decision memory);
    function isCancelled(uint64 number) external view returns (bool);
    function cancelledRanges() external view returns (uint64[2][] memory);
    function holds() external view returns (Hold[] memory);
    function keys() external view returns (address[6] memory);
    function flags() external view returns (IPausableGate.Flag[6] memory);
    function hold() external view returns (IPausableGate.Flag memory);
    function setupDone() external view returns (bool);
    /// The launch line (audit round 2, E2-RT6): the mailbox's `nonce()` when the launch's finishSetup passed. A
    /// message whose nonce is below it was dispatched before the launch — by a stranger after step 9, or by the
    /// deployer while it owned the mailbox and the tokens — and is none of the bridge's burns: the signers, the
    /// monitors and Proposal 2 count none of them (each holds the message, and so its nonce). Read it from the launch
    /// HlAdmin: 0 there until its finishSetup, and 0 in every successor.
    function launchNonce() external view returns (uint32);
    function successor() external view returns (address);
    function predecessor() external view returns (address);
    function inherited() external view returns (bool);
    function clockFactor() external view returns (uint64);
    function origin() external view returns (uint32);
    function hlgov() external view returns (bytes32);
    function mailbox() external view returns (address);
    function client() external view returns (address);
    function gate() external view returns (address);
    function token(uint8 i) external view returns (address);
    function proxyAdmin() external view returns (address);
    function merkleTreeHook() external view returns (address);
    function protocolFee() external view returns (address);
    /// The ed25519 verifier the client implementation must call (its `ED25519()`) and the EXTCODEHASH its code must
    /// have: both fixed at the deploy, both checked by `finishSetup` (checks 27 and 28). Like `route` and
    /// `mailboxPkgPathHash`, read by `finishSetup` alone: they grant nothing, a kind 8 or 6 can leave them stale, and
    /// kind 9 does not compare a successor's with them (fold F8).
    function ed25519() external view returns (address);
    function ed25519CodeHash() external view returns (bytes32);
    /// Ethereum's Hyperlane domain: the mailbox's and each token's `localDomain` (check 29).
    function localDomain() external view returns (uint32);
    /// The route realm a token's router for gno.land must be (0 GNOT, 1 BUBBLE: 32 bytes, the gno address
    /// left-padded), compiled at the deploy; `finishSetup` checks each token's router against it (check 15).
    function route(uint8 i) external view returns (bytes32);
    /// keccak256 of the hlmailbox realm's path, which `finishSetup` requires the client to carry (check 26).
    function mailboxPkgPathHash() external view returns (bytes32);

    event DecisionRecorded(uint64 indexed number, uint8 kind, uint64 matureAt);
    event DecisionIgnored(uint64 indexed number);
    event DecisionSpent(uint64 indexed number, uint8 kind, string why);
    event DecisionExecuted(uint64 indexed number, uint8 kind);
    event DecisionCancelled(uint64 indexed number);
    event RangeCancelled(uint64 from, uint64 to);
    event FlagSet(address indexed key, uint8 slot);
    event FlagCleared(address indexed key, uint8 slot);
    event HoldEnded(uint64 start, uint64 end);
    event SetupDone();
    event Retired(address successor);
    event Inherited(address predecessor, uint64 lastSeq);
}

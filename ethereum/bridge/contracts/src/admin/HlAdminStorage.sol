// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {IHlAdmin} from "../interfaces/IHlAdmin.sol";
import {Guardians} from "../lib/Guardians.sol";

/// `finishSetup` found a setting that is not the launch one; `check` names it (HlAdminExec.checkSetup's numbers).
error SetupIncomplete(uint8 check);
/// An HlAdminExec function called other than by HlAdmin's delegatecall.
error NotDelegated();
/// A kind 8 upgrade left `proxy` outside HlAdmin's authority — its admin no longer the ProxyAdmin, its owner (the
/// client: its admin) no longer HlAdmin, or a binding finishSetup held that no decision moves no longer the
/// deployment's (HlAdminExec._fixed: a token answering another mailbox could be minted by it; audit round 3, E3-CT3):
/// the execution reverts and the decision stays pending.
error AuthorityLost(address proxy);
/// A kind 8 with a call executed with too little gas for the call's fixed budget (HlAdminExec.CALL_GAS): the execution
/// reverts and the decision stays pending, so no executor starves the new code's initializer below the budget (audit
/// round 3, E3-CT4). What an executor can still add to it — a warm set, and under EIP-8037 the transaction's state-gas
/// reservoir — is CALL_GAS's note (round 4, E4-CT2, E4-CT3).
error CallStarved();
/// A kind 8 upgrade left `proxy` running other code than the implementation the decision pinned — its call wrote the
/// EIP-1967 implementation slot (audit round 2, E2-RT10): the execution reverts and the decision stays pending.
error ImplementationMoved(address proxy);
/// A kind 8's implementation or a kind 9's successor has no code yet: the execution reverts and the decision stays
/// pending until the code is deployed, as a Solana kind 8 whose buffer is not yet staged pends — so nobody burns
/// GovDAO's 7 days by executing before the code is there. Code an earlier transaction left at an address never changes
/// (EIP-6780) but an EIP-7702 account's, and a delegation designator is spent.
error NoCodeYet(address target);

/// HlAdmin's state, kept at one ERC-7201 namespace (`gno.bridge.HlAdmin`) so that HlAdmin and the code it
/// delegatecalls for the kinds' execution (HlAdminExec, created by HlAdmin's constructor) read one layout. HlAdmin is
/// plain code, never upgraded: the namespace is there for the two halves, not for an upgrade.
library HlAdminStorage {
    /// keccak256(abi.encode(uint256(keccak256("gno.bridge.HlAdmin")) - 1)) & ~bytes32(uint256(0xff)).
    bytes32 internal constant SLOT = 0xa549383900acf2d7f17f70f3a8e005874b124005220e85417ec5628fc6dcfa00;

    /// The hold history's bound: a ninth hold merges the two oldest.
    uint256 internal constant MAX_HOLDS = 8;

    /// A pending decision as stored (its number is its key; `deliveredBlock` is never zero for a stored one).
    struct Pending {
        address deliverer;
        uint64 deliveredTime;
        uint8 kind;
        uint64 deliveredBlock;
        bytes params;
    }

    /// @custom:storage-location erc7201:gno.bridge.HlAdmin
    struct Layout {
        uint64 lastSeq;
        /// The number the sequence started after: the launch's `startSequence`, or the number a successor inherited
        /// (successorInit). `handle` accepted every number in (startSeq, lastSeq] and no other, writing or clearing
        /// its record there, so a decision runs only there (audit round 2, E2-RT1: a constructor's initcode can
        /// write any storage, a record at a number never delivered included).
        uint64 startSeq;
        uint32 pending;
        bool setupDone;
        bool inherited;
        /// The launch line (audit round 2, E2-RT6): the mailbox's nonce when a launch's finishSetup passed (a
        /// successor's is 0: the line is the launch's). A message below it — a stranger's dispatch after step 9, or the
        /// deployer's while it owned the mailbox and the tokens — is none of the bridge's burns.
        uint32 launchNonce;
        address successor;
        Guardians.State g;
        mapping(uint64 => Pending) decisions;
        uint64[2][] cancelled;
        IHlAdmin.Hold[] holds;
    }

    function layout() internal pure returns (Layout storage l) {
        bytes32 slot = SLOT;
        assembly ("memory-safe") {
            l.slot := slot
        }
    }

    /// 24 hours at the clock factor: an unpause's delay and the dip's grace.
    function day() internal pure returns (uint64) {
        return uint64(1 days) / CLOCK_FACTOR;
    }

    /// Notes in the hold history a hold that a change of the guardians ended (admin-program `note_hold_end`): by a
    /// dip of the grace's length, at the moment the dip reached it; by a replacement's own clear, now. The
    /// arguments are the stored moments before the change.
    function noteHoldEnd(Layout storage l, uint64 holdBlock, uint64 holdTime, uint64 dipBlock, uint64 dipTime)
        internal
    {
        if (holdBlock == 0) {
            return;
        }
        if (l.g.holdBlock == holdBlock && l.g.holdTime == holdTime) {
            return;
        }
        uint64 now_ = uint64(block.timestamp);
        uint64 end = now_;
        if (dipBlock != 0 && now_ >= dipTime && now_ - dipTime >= day()) {
            end = dipTime + day();
        }
        uint256 n = l.holds.length;
        if (n >= MAX_HOLDS) {
            // the two oldest merge into one spanning both, the earliest start kept
            if (l.holds[1].end > l.holds[0].end) {
                l.holds[0].end = l.holds[1].end;
            }
            for (uint256 i = 1; i + 1 < n; i++) {
                l.holds[i] = l.holds[i + 1];
            }
            l.holds.pop();
        }
        l.holds.push(IHlAdmin.Hold(holdTime, end));
        emit IHlAdmin.HoldEnded(holdTime, end);
    }
}

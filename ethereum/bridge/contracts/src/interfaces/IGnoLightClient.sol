// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {IEd25519Verifier} from "../Ed25519Verifier.sol";

/// The light client of gno.land on Ethereum (eth/bridge/PLAN.md §6.1–§6.3): headers verified against more than 2/3
/// of the current validator set's power, set changes followed through NextValidatorsHash, messages proven from a
/// results leaf under an accepted header's LastResultsHash. This file is the interface the contracts, the Go
/// machines and the tests share; a change to it is a change to all three.
interface IGnoLightClient {
    /// One validator, in the set's own order (tm2 sorts by address, sha256(pubkey)[:20] ascending).
    struct Validator {
        bytes32 pubkey; // ed25519
        int64 power;
    }

    /// One precommit the submitter carries: the validator's slot in the set (strictly increasing within a call),
    /// the precommit's own timestamp, and its ed25519 signature (r ‖ s). Only precommits for the commit's BlockID
    /// are carried; a nil or other-block precommit is left out (never tallied).
    struct Precommit {
        uint16 index;
        int64 timeSecs;
        int32 timeNanos;
        bytes32 r;
        bytes32 s;
    }

    /// A commit's shared part: the BlockID's part set (its hash is the header's) and the round every carried
    /// precommit is of (tm2's Commit.ValidateBasic: one round per commit).
    struct Commit {
        uint32 partsTotal;
        bytes32 partsHash;
        int64 round;
        Precommit[] precommits;
    }

    /// A set history entry (§6.1): the height the set signs from, its hash, when it was announced, its
    /// quarantine's end (0 for the set the client started with).
    struct SetEntry {
        int64 fromHeight;
        bytes32 setHash;
        uint64 announcedBlock;
        uint64 announcedTime;
        uint64 quarantineEnd;
    }

    /// A header accepted or proved: the client's epoch when it was, its height and its LastResultsHash (zero when
    /// the block before it had no transactions).
    struct Entry {
        uint64 epoch;
        int64 height;
        bytes32 lastResultsHash;
    }

    /// Accepts a header above the trusted height, signed by the set its height belongs to. `headerLeaves` are the
    /// sixteen amino-encoded fields (PLAN.md §13 via the Solana plan §13's "header leaves"), hashed to the header's
    /// hash. `set` is the signing set in full (checked against the stored hash). `nextSet` is the next set when the
    /// header announces a change (NextValidatorsHash ≠ ValidatorsHash), else empty; it is read only by the call
    /// that crosses 2/3. A call whose precommits do not reach more than 2/3 of the power stores its tally under the
    /// stage key (submitter, header hash, BlockID, round, epoch, set hash) and returns false; the call that crosses 2/3
    /// finishes the update and returns true. Precommits carry strictly increasing slots; a slot an earlier call of
    /// the stage verified is skipped, never counted twice; a call that verifies no new slot reverts
    /// (`NoNewSignature`), storing and announcing nothing.
    function update(
        bytes[] calldata headerLeaves,
        Commit calldata commit,
        Validator[] calldata set,
        Validator[] calldata nextSet
    ) external returns (bool finished);

    /// Records a header at or below the trusted height, signed by the current set, as proven (the self-contained
    /// path: §6.3). Staged like `update` (a call that verifies no new slot reverts). A header already recorded under
    /// the current epoch is a no-op that returns true.
    function proveHeader(bytes[] calldata headerLeaves, Commit calldata commit, Validator[] calldata set)
        external
        returns (bool finished);

    /// The ISM check (Hyperlane's IInterchainSecurityModule.verify): metadata is
    /// abi.encode(bytes32 headerHash, uint32 total, uint32 index, bytes32[] aunts, bytes leaf). Reverts on any
    /// failure; returns true otherwise. Writes nothing.
    function verify(bytes calldata metadata, bytes calldata message) external returns (bool);

    /// Hyperlane's module type: 0 (UNUSED), so Hyperlane's own relayer builds no metadata for it (PLAN.md §13).
    function moduleType() external view returns (uint8);

    /// Kind 9's hand-over of the admin role (the one control beside the proxy's upgrade).
    function setAdmin(address newAdmin) external;

    function admin() external view returns (address);
    function chainId() external view returns (string memory);
    function originDomain() external view returns (uint32);
    function mailboxPkgPathHash() external view returns (bytes32);
    /// The ed25519 verifier every precommit's signature goes through (lane E-M2a's `Ed25519Verifier`), fixed in the
    /// implementation: `finishSetup` requires it to be HlAdmin's `ed25519()`.
    function ED25519() external view returns (IEd25519Verifier);
    function clockFactor() external view returns (uint64);

    function trustedHeight() external view returns (int64);
    function trustedHash() external view returns (bytes32);
    /// The trusted header's time (seconds), the initialization's header's until the first update.
    function trustedTime() external view returns (uint64);
    function setHash() external view returns (bytes32);
    function nextSetHash() external view returns (bytes32); // zero when no change is announced
    function epoch() external view returns (uint64);
    function quarantineEnd() external view returns (uint64);
    function entry(bytes32 headerHash) external view returns (Entry memory);
    function records() external view returns (uint64);
    function runningHash() external view returns (bytes32);
    function historyLength() external view returns (uint256);
    function history(uint256 i) external view returns (SetEntry memory);

    /// A stage's tally so far (zero when none): the verified slots' power.
    function stagePower(bytes32 key) external view returns (int64);
    /// The stage key of a call: keccak256(abi.encode(submitter, header hash, parts total, parts hash, round,
    /// epoch, set hash)) — the client's epoch at the call and the set hash the header must be signed by, so no stage
    /// outlives a set change.
    function stageKey(
        address submitter,
        bytes32 headerHash,
        uint32 partsTotal,
        bytes32 partsHash,
        int64 round,
        uint64 epoch_,
        bytes32 setHash_
    ) external pure returns (bytes32);

    /// Every header accepted (proven = false) or proved (proven = true), with the record's counter and running hash
    /// after it: the record the watch rebuilds (PLAN.md §6.2).
    event HeaderRecorded(
        bytes32 indexed hash, int64 indexed height, uint64 epoch, uint64 record, bytes32 runningHash, bool proven
    );
    /// A set announced by an accepted header: it signs from `fromHeight`.
    event SetAnnounced(int64 fromHeight, bytes32 setHash, uint64 quarantineEnd);
    /// The announced set became the current one (the first header it signed was accepted); the epoch rose.
    event SetChanged(uint64 epoch, bytes32 setHash);
    /// A stage's tally after a call that did not cross 2/3 and verified at least one new signature over the header
    /// (`headerHash` at `height`): the watch holds it against gno.land's header at that height without reading the
    /// call's data.
    event StageProgress(
        bytes32 indexed key, address indexed submitter, bytes32 headerHash, int64 height, int64 tallied, int64 total
    );
    event AdminChanged(address admin);
}

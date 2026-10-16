// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

/// The gate in front of the light client for the two tokens' deliveries (eth/bridge/PLAN.md §6.7; the Solana plan's
/// pausable ISM): flags, holds, the quarantine, the mint limits. Hyperlane's mailbox calls `verify` inside
/// `process`; the tokens' ISM and the mailbox's default ISM point here.
interface IPausableGate {
    /// One seat or watch slot to replace (kind 5): seats 1–4 guardians, 5–6 watch slots.
    struct SeatKey {
        uint8 seat;
        address key;
    }

    /// A flag: the block and time it was last set (zero block = no flag).
    struct Flag {
        uint64 blockNumber;
        uint64 time;
    }

    /// A token's mint limits: the most one message may mint and a rolling day bucket that drains at `dayCap` per day
    /// (24 hours ÷ the clock factor) from its last charge.
    struct MintLimit {
        uint64 maxPerMessage;
        uint64 dayCap;
        uint64 used;
        uint64 usedAt;
    }

    /// Hyperlane's ISM check: only the mailbox may call it; refused under any flag or the client's quarantine; a
    /// message to one of the two tokens is charged against its mint limits; then the client's own `verify`.
    function verify(bytes calldata metadata, bytes calldata message) external returns (bool);
    function moduleType() external view returns (uint8);

    /// A seat's or watch slot's own flag (the caller's key).
    function setFlag() external;
    function clearFlag() external;

    /// The owner's (HlAdmin's) acts: an unpause clears the flags set at a block before `beforeBlock`, refused while a
    /// hold stands; a replacement of seats and slots; the mint limits.
    function unpause(uint64 beforeBlock) external returns (uint256 cleared);
    function setGuardians(SeatKey[] calldata pairs) external;
    function setMintLimits(uint8 token, uint64 maxPerMessage, uint64 dayCap) external;

    function owner() external view returns (address);
    function transferOwnership(address newOwner) external;

    function mailbox() external view returns (address);
    function client() external view returns (address);
    function token(uint8 i) external view returns (address);
    function clockFactor() external view returns (uint64);
    /// Seats 1–4 then watch slots 5–6, by index 0–5.
    function keys() external view returns (address[6] memory);
    function flags() external view returns (Flag[6] memory);
    /// The standing hold's start (zero when none stands at `block.timestamp`), and the stored moments behind it.
    function hold() external view returns (Flag memory);
    function holdSince() external view returns (Flag memory);
    function dipSince() external view returns (Flag memory);
    function limits(uint8 token) external view returns (MintLimit memory);

    event FlagSet(address indexed key, uint8 slot);
    event FlagCleared(address indexed key, uint8 slot);
    event Unpaused(uint64 beforeBlock, uint256 cleared);
    event GuardiansSet(uint8 seat, address key);
    event MintLimitsSet(uint8 token, uint64 maxPerMessage, uint64 dayCap);
    event Charged(uint8 token, uint64 amount, uint64 level);
}

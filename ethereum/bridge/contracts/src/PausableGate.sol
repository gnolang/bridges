// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {OwnableUpgradeable} from "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {IGnoLightClient} from "./interfaces/IGnoLightClient.sol";
import {IPausableGate} from "./interfaces/IPausableGate.sol";
import {Guardians} from "./lib/Guardians.sol";

/// The gate in front of the light client for gno.land → Ethereum deliveries (eth/bridge/PLAN.md §6.7; the Solana
/// plan's pausable ISM, ported from bridge/programs/pausable-ism rule by rule). The mailbox's default ISM and both
/// tokens' ISM point here. `verify` runs only when the mailbox calls it — Mailbox.process calls an ISM only inside a
/// delivery that reverts whole when the recipient's `handle` fails, which binds a mint limit's charge to its
/// delivery — and only when no quarantine stands on the client and no flag stands here; a message to one of the two
/// tokens is charged against that token's mint limits (the per-message maximum and the rolling day); then the
/// client's own `verify` decides.
///
/// Four guardian seats and two watch slots, a flag per key with the block it was last set at, the hold and the dip
/// (lib/Guardians.sol, the code HlAdmin keeps its own flags with). The owner is HlAdmin: it unpauses (refused under
/// a hold), replaces seats and slots, and sets the mint limits, each only by a decision. Behind a
/// TransparentUpgradeableProxy (kind 8); the storage layout below is the deployment's for its life.
contract PausableGate is IPausableGate, Initializable, OwnableUpgradeable {
    using Guardians for Guardians.State;

    error NotTheMailbox();
    error Quarantine();
    error Paused();
    error BadMessage();
    error BadTokenMessage();
    error OverMax();
    error OverDayCap();
    error BadToken();
    error ZeroAddress();
    error NoRenounce();

    uint256 internal constant MESSAGE_HEADER = 77;

    // ---- storage (the proxy's; append only) ----
    address private _mailbox;
    address private _client;
    address[2] private _tokens;
    Guardians.State private _g;
    MintLimit[2] private _limits;
    uint256[40] private __gap;

    constructor() {
        _disableInitializers();
    }

    /// The deploy's one-shot start (PLAN.md §5.8 step 6, in the proxy's construction): the mailbox, the client, the
    /// two tokens (0 GNOT, 1 BUBBLE), the four seats and two watch slots (the same list HlAdmin's constructor
    /// takes), the launch mint limits per token and the owner (the deployer until the setup step).
    function initialize(
        address owner_,
        address mailbox_,
        address client_,
        address[2] calldata tokens_,
        address[4] calldata seats,
        address[2] calldata watch,
        uint64[2] calldata maxPerMessage,
        uint64[2] calldata dayCap
    ) external initializer {
        if (owner_ == address(0) || mailbox_ == address(0) || client_ == address(0)) {
            revert ZeroAddress();
        }
        if (tokens_[0] == address(0) || tokens_[1] == address(0) || tokens_[0] == tokens_[1]) {
            revert BadToken();
        }
        __Ownable_init();
        _transferOwnership(owner_);
        _mailbox = mailbox_;
        _client = client_;
        _tokens = tokens_;
        _g.init([seats[0], seats[1], seats[2], seats[3], watch[0], watch[1]]);
        for (uint8 i = 0; i < 2; i++) {
            _limits[i] = MintLimit(maxPerMessage[i], dayCap[i], 0, 0);
            emit MintLimitsSet(i, maxPerMessage[i], dayCap[i]);
        }
    }

    /// 24 hours at the build's clock factor: the mint bucket's day and the dip's grace.
    function _day() internal pure returns (uint64) {
        return uint64(1 days) / CLOCK_FACTOR;
    }

    // ---------------------------------------------------------------------------------------------------- ISM --

    /// @inheritdoc IPausableGate
    function verify(bytes calldata metadata, bytes calldata message) external override returns (bool) {
        if (msg.sender != _mailbox) {
            revert NotTheMailbox();
        }
        IGnoLightClient c = IGnoLightClient(_client);
        if (c.quarantineEnd() > block.timestamp) {
            revert Quarantine();
        }
        if (_g.anyFlag()) {
            revert Paused();
        }
        if (message.length < MESSAGE_HEADER) {
            revert BadMessage();
        }
        bytes32 recipient = bytes32(message[45:77]);
        for (uint8 i = 0; i < 2; i++) {
            if (recipient == bytes32(uint256(uint160(_tokens[i])))) {
                _charge(i, _tokenAmount(message[MESSAGE_HEADER:]));
                break;
            }
        }
        return c.verify(metadata, message);
    }

    /// Hyperlane's token message `recipient 32 ‖ amount u256 ‖ metadata`: the amount, which must fit 64 bits.
    function _tokenAmount(bytes calldata body) internal pure returns (uint64) {
        if (body.length < 64 || bytes24(body[32:56]) != bytes24(0)) {
            revert BadTokenMessage();
        }
        return uint64(bytes8(body[56:64]));
    }

    /// A token's charge: refused above the per-message maximum or past the rolling day's cap; the bucket drains at
    /// the cap per day (24 hours at the clock factor) from its last charge (pausable-ism state.rs `MintLimit`).
    function _charge(uint8 i, uint64 amount) internal {
        MintLimit memory l = _limits[i];
        if (amount > l.maxPerMessage) {
            revert OverMax();
        }
        uint64 next = _level(l, uint64(block.timestamp));
        next = amount > type(uint64).max - next ? type(uint64).max : next + amount;
        if (next > l.dayCap) {
            revert OverDayCap();
        }
        _limits[i].used = next;
        _limits[i].usedAt = uint64(block.timestamp);
        emit Charged(i, amount, next);
    }

    /// The bucket's level at `now`: what was charged, less what drained since (the cap a day).
    function _level(MintLimit memory l, uint64 now_) internal pure returns (uint64) {
        uint256 elapsed = now_ > l.usedAt ? now_ - l.usedAt : 0;
        uint256 drained = elapsed * l.dayCap / _day();
        if (drained > type(uint64).max) {
            drained = type(uint64).max;
        }
        return l.used > drained ? l.used - uint64(drained) : 0;
    }

    /// @inheritdoc IPausableGate
    function moduleType() external view override returns (uint8) {
        return IGnoLightClient(_client).moduleType();
    }

    // -------------------------------------------------------------------------------------------------- flags --

    /// @inheritdoc IPausableGate
    function setFlag() external override {
        uint256 i = _g.setFlag(msg.sender, uint64(block.number), uint64(block.timestamp), _day());
        emit FlagSet(msg.sender, uint8(i + 1));
    }

    /// @inheritdoc IPausableGate
    function clearFlag() external override {
        uint256 i = _g.clearFlag(msg.sender, uint64(block.number), uint64(block.timestamp), _day());
        emit FlagCleared(msg.sender, uint8(i + 1));
    }

    // ---------------------------------------------------------------------------------------------- the owner --

    /// @inheritdoc IPausableGate
    function unpause(uint64 beforeBlock) external override onlyOwner returns (uint256 cleared) {
        cleared = _g.unpause(beforeBlock, uint64(block.number), uint64(block.timestamp), _day());
        emit Unpaused(beforeBlock, cleared);
    }

    /// @inheritdoc IPausableGate
    function setGuardians(SeatKey[] calldata pairs) external override onlyOwner {
        _g.replace(pairs, uint64(block.number), uint64(block.timestamp), _day());
        for (uint256 p = 0; p < pairs.length; p++) {
            emit GuardiansSet(pairs[p].seat, pairs[p].key);
        }
    }

    /// @inheritdoc IPausableGate
    function setMintLimits(uint8 token_, uint64 maxPerMessage, uint64 dayCap) external override onlyOwner {
        if (token_ >= 2) {
            revert BadToken();
        }
        // the bucket's level stands
        _limits[token_].maxPerMessage = maxPerMessage;
        _limits[token_].dayCap = dayCap;
        emit MintLimitsSet(token_, maxPerMessage, dayCap);
    }

    /// Nothing here is ever ownerless: kind 9 moves the ownership, nothing removes it.
    function renounceOwnership() public pure override {
        revert NoRenounce();
    }

    function owner() public view override(IPausableGate, OwnableUpgradeable) returns (address) {
        return super.owner();
    }

    function transferOwnership(address newOwner) public override(IPausableGate, OwnableUpgradeable) {
        super.transferOwnership(newOwner);
    }

    // ------------------------------------------------------------------------------------------------- views --

    function mailbox() external view override returns (address) {
        return _mailbox;
    }

    function client() external view override returns (address) {
        return _client;
    }

    function token(uint8 i) external view override returns (address) {
        return _tokens[i];
    }

    function clockFactor() external pure override returns (uint64) {
        return CLOCK_FACTOR;
    }

    function keys() external view override returns (address[6] memory) {
        return _g.keys;
    }

    function flags() external view override returns (Flag[6] memory) {
        return _g.flags();
    }

    function hold() external view override returns (Flag memory) {
        return _g.holdView(uint64(block.timestamp), _day());
    }

    function holdSince() external view override returns (Flag memory) {
        return Flag(_g.holdBlock, _g.holdTime);
    }

    function dipSince() external view override returns (Flag memory) {
        return Flag(_g.dipBlock, _g.dipTime);
    }

    function limits(uint8 i) external view override returns (MintLimit memory l) {
        l = _limits[i];
    }
}

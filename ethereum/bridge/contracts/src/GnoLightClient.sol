// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";

import {CLOCK_FACTOR} from "bridge-clock/ClockFactor.sol";
import {IEd25519Verifier} from "./Ed25519Verifier.sol";
import {IGnoLightClient} from "./interfaces/IGnoLightClient.sol";
import {TmHeader} from "./lib/TmHeader.sol";
import {TmMerkle} from "./lib/TmMerkle.sol";
import {TmResults} from "./lib/TmResults.sol";
import {TmValset} from "./lib/TmValset.sol";
import {TmVote} from "./lib/TmVote.sol";

/// The light client of gno.land on Ethereum (eth/bridge/PLAN.md §6.1–§6.4; the Solana plan's light-client ISM, ported
/// from bridge/programs/gno-light-client-ism and gno-light-core rule by rule). It keeps hashes, never sets: the
/// current set's hash, the announced next set's hash, the trusted header; a set arrives in the calldata of every
/// call and is hashed against the stored hash. A header above the trusted height is accepted when validators holding
/// more than 2/3 of its set's power signed it (counted by slot, each precommit's sign bytes built from its own time
/// and the commit's BlockID and round); set changes are followed in order through NextValidatorsHash, the
/// announcing header's next set given whole; there is no trusting period and no reset. A message is proven by a
/// results leaf under the LastResultsHash of a header the client accepted or proved, while the set that header
/// belongs to is still the current one (its entry's epoch).
///
/// Behind a TransparentUpgradeableProxy (upgraded only by HlAdmin's kind 8); its one other control is `setAdmin`,
/// which HlAdmin's kind 9 moves. The storage layout below is the deployment's for its life: an upgrade appends.
contract GnoLightClient is IGnoLightClient, Initializable {
    error NotAdmin();
    error ZeroAdmin();
    error ChainId();
    error HeightNotAboveTrusted();
    error HeightAboveTrusted();
    error ValidatorsHash();
    error WrongSet();
    error NextSetMissing();
    error NextSetUnexpected();
    error NextValidatorsHash();
    error BadCommit();
    error SlotOutOfRange();
    error SlotsUnsorted();
    error SignatureMismatch();
    error UnknownHeader();
    error NoResultsHash();
    error ProofMismatch();
    error DispatchMismatch();
    error OriginMismatch();
    error BadMessage();
    error NoVerifier();
    error NoNewSignature();
    error TrustedTime();

    /// The most precommit slots: the set bound (a set of more is refused by TmValset).
    uint256 internal constant MAX_SLOTS = 255;
    /// Hyperlane's message version and header length.
    uint8 internal constant MESSAGE_VERSION = 3;
    uint256 internal constant MESSAGE_HEADER = 77;
    /// How far the trusted header's time may lie from the initialization's, either way (PLAN.md §5.8 step 4: "less
    /// than an hour old"; audit round 2, E2-CT3): the setup is done in one sitting. Real time, not the clock factor's:
    /// it bounds the trust root's age, not a governance delay.
    uint256 internal constant TRUST_WINDOW = 1 hours;

    /// keccak256 of this deployment's hlmailbox realm path (PLAN.md §13), compiled into the implementation.
    bytes32 public immutable override mailboxPkgPathHash;
    /// Lane E-M2a's `Ed25519Verifier` (eth/bridge/M2a-NOTES.md §1): Go's crypto/ed25519.Verify behind its own
    /// address, since inline it would put this contract past EIP-170. Fixed in the implementation's code, so
    /// replacing the verifier is an upgrade of the client (kind 8); `finishSetup` checks it is HlAdmin's and that its
    /// code is the one HlAdmin was built to expect.
    IEd25519Verifier public immutable override ED25519;

    // ---- storage (the proxy's; append only) ----
    address private _admin;
    uint32 private _originDomain;
    int64 private _trustedHeight;
    uint64 private _epoch;
    uint64 private _quarantineEnd;
    uint64 private _records;
    uint64 private _trustedTime;
    bytes32 private _trustedHash;
    bytes32 private _setHash;
    bytes32 private _nextSetHash;
    bytes32 private _runningHash;
    bytes32 private _chainIdLeafHash;
    string private _chainId;
    mapping(bytes32 => Entry) private _entries;
    SetEntry[] private _history;
    mapping(bytes32 => Stage) private _stages;
    uint256[40] private __gap;

    /// A staged tally (PLAN.md §6.3): the slots verified so far and their power.
    struct Stage {
        uint256 verified;
        int64 power;
    }

    constructor(bytes32 mailboxPkgPathHash_, IEd25519Verifier ed25519_) {
        if (address(ed25519_).code.length == 0) {
            revert NoVerifier();
        }
        mailboxPkgPathHash = mailboxPkgPathHash_;
        ED25519 = ed25519_;
        _disableInitializers();
    }

    /// The deploy's one-shot start (PLAN.md §5.8 step 4, called in the proxy's construction): the chain id, the
    /// origin domain, the trusted header as its sixteen leaves and its validator set, and the admin (the deployer
    /// until the setup step's `setAdmin(HlAdmin)`). The client binds its trust root itself (audit round 2, E2-CT3):
    /// the leaves hash to the trusted hash and give its height and time; the header is the chain id's; its
    /// ValidatorsHash is the set's hash and so is its NextValidatorsHash (never a header announcing a change: the next
    /// header would be signed by a set the client never stored, and there is no reset); and its time is within an
    /// hour of `block.timestamp` (never an old root: a past set's, whose keys may be anyone's by now). The trusted
    /// header is recorded (the record's first entry) and its time kept, but no entry is made for it: it proves a
    /// message only once `proveHeader` brought its results hash, as on Solana.
    function initialize(
        string calldata chainId_,
        uint32 originDomain_,
        bytes[] calldata headerLeaves,
        Validator[] calldata validators,
        address admin_
    ) external initializer {
        uint256 n = bytes(chainId_).length;
        if (n == 0 || n > 64) {
            revert ChainId();
        }
        if (admin_ == address(0)) {
            revert ZeroAdmin();
        }
        TmHeader.Fields memory h = TmHeader.parse(headerLeaves);
        bytes32 chainLeaf = keccak256(TmHeader.chainIdLeaf(chainId_));
        if (h.chainIdLeafHash != chainLeaf) {
            revert ChainId();
        }
        TmValset.check(validators);
        (bytes32 sh,) = TmValset.hash(validators);
        if (h.validatorsHash != sh) {
            revert ValidatorsHash();
        }
        if (h.nextValidatorsHash != sh) {
            revert NextValidatorsHash();
        }
        if (uint256(h.time) + TRUST_WINDOW < block.timestamp || uint256(h.time) > block.timestamp + TRUST_WINDOW) {
            revert TrustedTime();
        }
        _admin = admin_;
        _chainId = chainId_;
        _chainIdLeafHash = chainLeaf;
        _originDomain = originDomain_;
        _trustedHeight = h.height;
        _trustedHash = h.hash;
        _trustedTime = h.time;
        _setHash = sh;
        _history.push(SetEntry(h.height, sh, uint64(block.number), uint64(block.timestamp), 0));
        emit SetAnnounced(h.height, sh, 0);
        emit SetChanged(0, sh);
        _appendRecord(h.hash, h.height, 0, false);
    }

    // ------------------------------------------------------------------------------------------------ updates --

    /// @inheritdoc IGnoLightClient
    function update(
        bytes[] calldata headerLeaves,
        Commit calldata commit,
        Validator[] calldata set,
        Validator[] calldata nextSet
    ) external override returns (bool finished) {
        TmHeader.Fields memory h = TmHeader.parse(headerLeaves);
        if (h.chainIdLeafHash != _chainIdLeafHash) {
            revert ChainId();
        }
        if (h.height <= _trustedHeight) {
            revert HeightNotAboveTrusted();
        }
        bytes32 next = _nextSetHash;
        bool usingNext = next != bytes32(0);
        bytes32 expect = usingNext ? next : _setHash;
        if (h.validatorsHash != expect) {
            revert ValidatorsHash();
        }
        if (!_tally(h, commit, set, expect)) {
            return false;
        }
        // the announcing header's next set, whole and checked against its NextValidatorsHash; read only by the
        // call that crosses 2/3
        bool announcing = h.nextValidatorsHash != h.validatorsHash;
        if (announcing) {
            if (nextSet.length == 0) {
                revert NextSetMissing();
            }
            TmValset.check(nextSet);
            (bytes32 nh,) = TmValset.hash(nextSet);
            if (nh != h.nextValidatorsHash) {
                revert NextValidatorsHash();
            }
        } else if (nextSet.length != 0) {
            revert NextSetUnexpected();
        }
        uint64 epoch_ = _epoch;
        if (usingNext) {
            // the first header of the announced set: it becomes the current one, and every header of the old set
            // stops proving (its entries' epoch is behind)
            epoch_ += 1;
            _epoch = epoch_;
            _setHash = next;
            _nextSetHash = bytes32(0);
            emit SetChanged(epoch_, next);
        }
        _trustedHeight = h.height;
        _trustedHash = h.hash;
        _trustedTime = h.time;
        if (announcing) {
            uint64 end = uint64(block.timestamp) + 7 days / CLOCK_FACTOR;
            if (_quarantineEnd > end) {
                end = _quarantineEnd;
            }
            _quarantineEnd = end;
            _nextSetHash = h.nextValidatorsHash;
            _history.push(
                SetEntry(h.height + 1, h.nextValidatorsHash, uint64(block.number), uint64(block.timestamp), end)
            );
            emit SetAnnounced(h.height + 1, h.nextValidatorsHash, end);
        }
        _entries[h.hash] = Entry(epoch_, h.height, h.lastResultsHash);
        _appendRecord(h.hash, h.height, epoch_, false);
        return true;
    }

    /// @inheritdoc IGnoLightClient
    function proveHeader(bytes[] calldata headerLeaves, Commit calldata commit, Validator[] calldata set)
        external
        override
        returns (bool finished)
    {
        TmHeader.Fields memory h = TmHeader.parse(headerLeaves);
        if (h.chainIdLeafHash != _chainIdLeafHash) {
            revert ChainId();
        }
        if (h.height > _trustedHeight) {
            revert HeightAboveTrusted();
        }
        bytes32 current = _setHash;
        if (h.validatorsHash != current) {
            revert ValidatorsHash();
        }
        uint64 epoch_ = _epoch;
        Entry storage e = _entries[h.hash];
        if (e.height != 0 && e.epoch == epoch_) {
            // recorded under the current set already (accepted or proved): nothing new to record
            return true;
        }
        if (!_tally(h, commit, set, current)) {
            return false;
        }
        _entries[h.hash] = Entry(epoch_, h.height, h.lastResultsHash);
        _appendRecord(h.hash, h.height, epoch_, true);
        return true;
    }

    /// The commit rule over this call's precommits and the stage's (PLAN.md §6.1, §6.3): the set must hash to
    /// `setHash_`; every precommit carried is for the header's BlockID (its part set from the commit) at the commit's
    /// one round, in a strictly increasing slot inside the set, its signature over its own sign bytes; a slot an
    /// earlier call of the stage verified is not verified or counted again, and a call that verifies no new slot is
    /// refused (`NoNewSignature`: audit round 2, E2-CT2), so no stage is stored and no `StageProgress` announced
    /// without a signature over its header behind it. True when the power crosses 2/3 (the stage is deleted);
    /// otherwise the tally is stored under the stage key and false is returned.
    function _tally(TmHeader.Fields memory h, Commit calldata commit, Validator[] calldata set, bytes32 setHash_)
        internal
        returns (bool)
    {
        (bytes32 sh, int64 total) = TmValset.hash(set);
        if (sh != setHash_) {
            revert WrongSet();
        }
        if (commit.partsTotal == 0 || commit.round < 0) {
            revert BadCommit();
        }
        bytes32 key = stageKey(msg.sender, h.hash, commit.partsTotal, commit.partsHash, commit.round, _epoch, setHash_);
        Stage storage st = _stages[key];
        uint256 had = st.verified;
        (uint256 verified, int64 tallied) =
            _verifyPrecommits(_template(h, commit), commit.precommits, set, had, st.power);
        if (verified == had) {
            revert NoNewSignature();
        }
        if (tallied >= 0 && total > 0 && int256(tallied) * 3 > int256(total) * 2) {
            if (had != 0) {
                delete _stages[key];
            }
            return true;
        }
        st.verified = verified;
        st.power = tallied;
        _announce(key, h, tallied, total);
        return false;
    }

    /// The stage's new tally, with the header's hash and height (the watch's, PLAN.md §6.3).
    function _announce(bytes32 key, TmHeader.Fields memory h, int64 tallied, int64 total) private {
        emit StageProgress(key, msg.sender, h.hash, h.height, tallied, total);
    }

    /// The sign bytes every precommit of the commit shares: the type, the height, the round, the BlockID, the chain.
    function _template(TmHeader.Fields memory h, Commit calldata commit) private view returns (TmVote.Template memory) {
        return TmVote.template(
            TmVote.PRECOMMIT_TYPE, bytes(_chainId), h.height, commit.round, h.hash, commit.partsTotal, commit.partsHash
        );
    }

    /// Verifies the precommits a call carries, in strictly increasing slots, skipping a slot verified before; the
    /// verified slots and their power after them.
    function _verifyPrecommits(
        TmVote.Template memory t,
        Precommit[] calldata precommits,
        Validator[] calldata set,
        uint256 verified,
        int64 tallied
    ) private view returns (uint256, int64) {
        uint256 nextSlot = 0;
        for (uint256 k = 0; k < precommits.length; k++) {
            Precommit calldata p = precommits[k];
            uint256 i = p.index;
            if (i < nextSlot) {
                revert SlotsUnsorted();
            }
            nextSlot = i + 1;
            if (i >= set.length) {
                revert SlotOutOfRange();
            }
            if (p.timeNanos < 0 || p.timeNanos > 999_999_999) {
                revert BadCommit();
            }
            if (verified & (1 << i) != 0) {
                continue;
            }
            if (!_verifySignature(set[i].pubkey, TmVote.signBytes(t, p.timeSecs, p.timeNanos), p.r, p.s)) {
                revert SignatureMismatch();
            }
            verified |= 1 << i;
            tallied += set[i].power;
        }
        return (verified, tallied);
    }

    /// The one signature check (PLAN.md §6.6): Go's crypto/ed25519.Verify through lane E-M2a's verifier, with the
    /// key's 32 bytes as the set carries them (never decoded, re-encoded or cached) and the signature as its two
    /// 32-byte halves, so it is 64 bytes by construction (tm2 refuses any other length before Verify). The call is
    /// well-formed and carries no value, so the verifier never reverts on its inputs (sign bytes of at most 130
    /// bytes: about 221,000 gas); a call that runs out of gas reverts the update whole.
    function _verifySignature(bytes32 pubkey, bytes memory signBytes, bytes32 r, bytes32 s)
        private
        view
        returns (bool)
    {
        return ED25519.verify(pubkey, signBytes, r, s);
    }

    /// The record (PLAN.md §6.2): a counter and a running hash over every header accepted or proved,
    /// `keccak256(previous ‖ hash ‖ height u64 BE ‖ block.number u64 BE)`, with an event per header.
    function _appendRecord(bytes32 hash, int64 height, uint64 epoch_, bool proven) private {
        uint64 n = _records + 1;
        bytes32 running = keccak256(abi.encodePacked(_runningHash, hash, uint64(height), uint64(block.number)));
        _records = n;
        _runningHash = running;
        emit HeaderRecorded(hash, height, epoch_, n, running, proven);
    }

    // ------------------------------------------------------------------------------------------------- proofs --

    /// @inheritdoc IGnoLightClient
    function verify(bytes calldata metadata, bytes calldata message) external view override returns (bool) {
        if (message.length < MESSAGE_HEADER || uint8(message[0]) != MESSAGE_VERSION) {
            revert BadMessage();
        }
        uint32 origin = uint32(bytes4(message[5:9]));
        if (origin != _originDomain) {
            revert OriginMismatch();
        }
        (bytes32 headerHash, uint32 total, uint32 index, bytes32[] memory aunts, bytes memory leaf) =
            abi.decode(metadata, (bytes32, uint32, uint32, bytes32[], bytes));
        Entry memory e = _entries[headerHash];
        if (e.height == 0 || e.epoch != _epoch) {
            revert UnknownHeader();
        }
        if (e.lastResultsHash == bytes32(0)) {
            revert NoResultsHash();
        }
        if (total == 0 || index >= total || aunts.length > TmMerkle.MAX_AUNTS) {
            revert ProofMismatch();
        }
        if (TmMerkle.rootFromProof(TmMerkle.leafHash(leaf), index, total, aunts) != e.lastResultsHash) {
            revert ProofMismatch();
        }
        TmResults.Message memory m = TmResults.Message({
            id: keccak256(message),
            hash: sha256(message),
            nonce: uint32(bytes4(message[1:5])),
            origin: origin,
            destination: uint32(bytes4(message[41:45])),
            sender: bytes32(message[9:41]),
            recipient: bytes32(message[45:77])
        });
        if (!TmResults.proves(leaf, mailboxPkgPathHash, m)) {
            revert DispatchMismatch();
        }
        return true;
    }

    /// @inheritdoc IGnoLightClient
    function moduleType() external pure override returns (uint8) {
        return 0;
    }

    // ------------------------------------------------------------------------------------------------- admin --

    /// @inheritdoc IGnoLightClient
    function setAdmin(address newAdmin) external override {
        if (msg.sender != _admin) {
            revert NotAdmin();
        }
        if (newAdmin == address(0)) {
            revert ZeroAdmin();
        }
        _admin = newAdmin;
        emit AdminChanged(newAdmin);
    }

    // ------------------------------------------------------------------------------------------------- views --

    /// The stage key of a call (PLAN.md §6.3): keccak256(abi.encode(submitter, header hash, parts total, parts
    /// hash, round, epoch, set hash)) — the epoch the client is at and the set hash the header must be signed by
    /// (the current set's for `proveHeader`, the announced one's or else the current one's for `update`). A set
    /// change moves both, orphaning every stage begun before it, so no tally is ever carried across one; every call,
    /// the one that finishes included, checks the header against the live state before it counts anything (the
    /// chain id, the height against the trusted one, the ValidatorsHash against the live expected set hash); a stage
    /// is deleted by the call that finishes it; and a header recorded under the current epoch never reaches a stage
    /// again (an update needs a height above the trusted one, and `proveHeader` returns before the tally for a
    /// recorded header). Nobody clears an abandoned stage: it holds no funds and pays no rent, and nothing reads it
    /// but its own submitter's next call with the same key.
    function stageKey(
        address submitter,
        bytes32 headerHash,
        uint32 partsTotal,
        bytes32 partsHash,
        int64 round,
        uint64 epoch_,
        bytes32 setHash_
    ) public pure override returns (bytes32) {
        return keccak256(abi.encode(submitter, headerHash, partsTotal, partsHash, round, epoch_, setHash_));
    }

    function admin() external view override returns (address) {
        return _admin;
    }

    function chainId() external view override returns (string memory) {
        return _chainId;
    }

    function originDomain() external view override returns (uint32) {
        return _originDomain;
    }

    function clockFactor() external pure override returns (uint64) {
        return CLOCK_FACTOR;
    }

    function trustedHeight() external view override returns (int64) {
        return _trustedHeight;
    }

    function trustedHash() external view override returns (bytes32) {
        return _trustedHash;
    }

    function trustedTime() external view override returns (uint64) {
        return _trustedTime;
    }

    function setHash() external view override returns (bytes32) {
        return _setHash;
    }

    function nextSetHash() external view override returns (bytes32) {
        return _nextSetHash;
    }

    function epoch() external view override returns (uint64) {
        return _epoch;
    }

    function quarantineEnd() external view override returns (uint64) {
        return _quarantineEnd;
    }

    function entry(bytes32 headerHash) external view override returns (Entry memory) {
        return _entries[headerHash];
    }

    function records() external view override returns (uint64) {
        return _records;
    }

    function runningHash() external view override returns (bytes32) {
        return _runningHash;
    }

    function historyLength() external view override returns (uint256) {
        return _history.length;
    }

    function history(uint256 i) external view override returns (SetEntry memory) {
        return _history[i];
    }

    function stagePower(bytes32 key) external view override returns (int64) {
        return _stages[key].power;
    }
}

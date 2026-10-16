// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {Ed25519} from "./lib/Ed25519.sol";

/// Ed25519.sol behind an address, for a caller without room for the bytecode the library adds inline (about 6.6 KB;
/// eth/bridge/M2a-NOTES.md, "Interface"). The verdict is Ed25519.verify's: exactly Go's crypto/ed25519.Verify.
interface IEd25519Verifier {
    /// Whether r ‖ s is a valid signature of `message` by `publicKey` (32-byte wire encodings), as Go's
    /// crypto/ed25519.Verify decides. A well-formed call with no value never reverts given enough gas: 260,000 covers
    /// a message of up to 175 bytes (two SHA-512 blocks, about 221,000 used; a precommit's sign bytes are at most
    /// 130), and each further 128 bytes adds 39,000–41,000. A malformed call (cut short, an offset or a length past
    /// the calldata) is reverted by the ABI decoder, a call with value by the non-payable function, and one with too
    /// little gas runs out (audit round 2, E2-ED2).
    function verify(bytes32 publicKey, bytes calldata message, bytes32 r, bytes32 s) external view returns (bool);
}

/// No storage, no owner, no constructor arguments, never behind a proxy: deployed once, its code is the whole of its
/// trust. A caller fixes this contract's address in its own code (an immutable of its implementation), so replacing
/// the verifier is an upgrade of the caller.
contract Ed25519Verifier is IEd25519Verifier {
    /// @inheritdoc IEd25519Verifier
    function verify(bytes32 publicKey, bytes calldata message, bytes32 r, bytes32 s) external view returns (bool) {
        return Ed25519.verify(publicKey, message, r, s);
    }
}

// SPDX-License-Identifier: UNLICENSED
pragma solidity 0.8.33;

/// The sender test contract of ethx (eth/bridge/ethx/anvil_test.go): a write, a require that fails, a custom error,
/// a call over the transaction gas cap.
contract SenderProbe {
    error Bad(uint256 x);

    uint256 public n;

    function ok() external {
        n += 1;
    }

    function fail() external pure {
        revert("nope");
    }

    function custom() external pure {
        revert Bad(7);
    }

    /// A write per round: asked for more rounds than one transaction's gas carries (EIP-7825), its estimate fails.
    function burn(uint256 rounds) external {
        for (uint256 i = 0; i < rounds; i++) {
            n += 1;
        }
    }
}

// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

// The compiled clock factor of the mainnet build (PLAN.md §5.9): every delay of the client, the gate and HlAdmin is
// its mainnet constant divided by it. The `factor8` profile maps `bridge-clock/` to the testnet build's file.
uint64 constant CLOCK_FACTOR = 1;

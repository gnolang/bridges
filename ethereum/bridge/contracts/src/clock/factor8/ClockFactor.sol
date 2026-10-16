// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

// The compiled clock factor of the devnet/Sepolia build (PLAN.md §5.9): 7 days in 21 hours, 24 hours in 3. Built by
// `FOUNDRY_PROFILE=factor8`; the mainnet build's file is `../factor1/ClockFactor.sol`.
uint64 constant CLOCK_FACTOR = 8;

// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Predeploys } from "../libraries/Predeploys.sol";
import { L2CrossDomainMessenger } from "./L2CrossDomainMessenger.sol";
import { Ownable } from "@openzeppelin/contracts/access/Ownable.sol";

/**
 * @title CrossDomainOwnable2
 * @notice This contract extends the OpenZeppelin `Ownable` contract
 *         for L2 contracts to be owned by contracts on L1. Note that
 *         this contract is meant to be used with systems that use the
 *         CrossDomainMessenger system. It will not work if the OptimismPortal
 *         is used directly.
 */
abstract contract CrossDomainOwnable2 is Ownable {
    L2CrossDomainMessenger internal messenger =
        L2CrossDomainMessenger(Predeploys.L2_CROSS_DOMAIN_MESSENGER);

    /**
     * @notice Overrides the implementation of the `onlyOwner` modifier
     *         to check that the unaliased `xDomainMessageSender`
     *         is the owner of the contract. This value is set
     *         to the caller of the L1CrossDomainMessenger.
     */
    function _checkOwner() internal view override {
        require(
            owner() == messenger.xDomainMessageSender(),
            "CrossDomainOwnable2: caller is not the owner"
        );
    }
}

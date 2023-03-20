// SPDX-License-Identifier: MIT
pragma solidity ^0.8.7;

import "../interfaces/IUniswapV2Router02.sol";

interface zContract {
    function onCrossChainCall(
        address zrc20,
        uint256 amount,
        bytes calldata message
    ) external;
}
interface IZRC20 {
    function totalSupply() external view returns (uint256);

    function balanceOf(address account) external view returns (uint256);

    function transfer(address recipient, uint256 amount) external returns (bool);

    function allowance(address owner, address spender) external view returns (uint256);

    function approve(address spender, uint256 amount) external returns (bool);

    function transferFrom(
        address sender,
        address recipient,
        uint256 amount
    ) external returns (bool);

    function deposit(address to, uint256 amount) external returns (bool);

    function burn(address account, uint256 amount) external returns (bool);

    function withdraw(bytes memory to, uint256 amount) external returns (bool);

    function withdrawGasFee() external view returns (address, uint256);

    function PROTOCOL_FEE() external view returns (uint256);

    event Transfer(address indexed from, address indexed to, uint256 value);
    event Approval(address indexed owner, address indexed spender, uint256 value);
    event Deposit(bytes from, address indexed to, uint256 value);
    event Withdrawal(address indexed from, bytes to, uint256 value, uint256 gasFee, uint256 protocolFlatFee);
}




contract ZEVMSwapApp is zContract {
    error InvalidSender();
    error LowAmount();

    uint256 constant private _DEADLINE = 1 << 64;
    address immutable public router02;
    address immutable public systemContract;
    
    constructor(address router02_, address systemContract_) {
        router02 = router02_;
        systemContract = systemContract_;
    }
    
    // Call this function to perform a cross-chain swap
    function onCrossChainCall(address zrc20, uint256 amount, bytes calldata message) external override {
        if (msg.sender != systemContract) {
            revert InvalidSender();
        }
        address targetZRC20;
        address recipient;
        uint256 minAmountOut; 
        (targetZRC20, recipient, minAmountOut) = abi.decode(message, (address,address,uint256));
        address[] memory path;
        path = new address[](2);
        path[0] = zrc20;
        path[1] = targetZRC20;
        // Approve the usage of this token by router02
        IZRC20(zrc20).approve(address(router02), amount);
        // Swap for your target token
        uint256[] memory amounts = IUniswapV2Router02(router02).swapExactTokensForTokens(amount, 0, path, address(this), _DEADLINE);
        // Withdraw amount to target recipient
        (, uint256 gasFee) = IZRC20(targetZRC20).withdrawGasFee();
        if (gasFee > amounts[1]) {
            revert LowAmount();
        }
        IZRC20(targetZRC20).approve(address(targetZRC20), gasFee);
        IZRC20(targetZRC20).withdraw(bytes("bcrt1qlj8pkmftahy9pxj290lu32k2w8um2vkdnu35w6"), amounts[1] - gasFee);
    }
}
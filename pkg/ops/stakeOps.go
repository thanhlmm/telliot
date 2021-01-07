// Copyright (c) The Tellor Authors.
// Licensed under the MIT License.

package ops

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/pkg/errors"
	"github.com/tellor-io/telliot/pkg/contracts"
	"github.com/tellor-io/telliot/pkg/rpc"
	"github.com/tellor-io/telliot/pkg/util"
)

/**
 * This is the operational deposit component. Its purpose is to deposit Tellor Tokens so you can mine
 */

func printStakeStatus(bigStatus *big.Int, started *big.Int) {
	// 0-not Staked, 1=Staked, 2=LockedForWithdraw 3= OnDispute
	status := bigStatus.Uint64()
	stakeTime := time.Unix(started.Int64(), 0)
	switch status {
	case 0:
		fmt.Printf("Not currently staked\n")
	case 1:
		fmt.Printf("Staked in good standing since %s\n", stakeTime.UTC())
	case 2:
		startedRound := started.Int64()
		startedRound = ((startedRound + 86399) / 86400) * 86400
		target := time.Unix(startedRound, 0)
		timePassed := time.Since(target)
		delta := timePassed - (time.Hour * 24 * 7)
		if delta > 0 {
			fmt.Printf("Stake has been eligbile to withdraw for %s\n", delta)
		} else {
			fmt.Printf("Stake will be eligible to withdraw in %s\n", -delta)
		}
	case 3:
		fmt.Printf("Stake is currently under dispute")
	}
}

func Deposit(
	ctx context.Context,
	logger log.Logger,
	client rpc.ETHClient,
	contract *contracts.Tellor,
	account *rpc.Account,
) error {

	balance, err := contract.Getter.BalanceOf(nil, account.Address)
	if err != nil {
		return errors.Wrap(err, "get TRB balance")
	}

	status, startTime, err := contract.Getter.GetStakerInfo(nil, account.Address)
	if err != nil {
		return errors.Wrap(err, "get stake status")
	}

	if status.Uint64() != 0 && status.Uint64() != 2 {
		printStakeStatus(status, startTime)
		return nil
	}

	dat := crypto.Keccak256([]byte("stakeAmount"))
	var dat32 [32]byte
	copy(dat32[:], dat)
	stakeAmt, err := contract.Getter.GetUintVar(nil, dat32)
	if err != nil {
		return errors.Wrap(err, "fetching stake amount")
	}

	if balance.Cmp(stakeAmt) < 0 {
		return errors.Errorf("insufficient mining stake TRB balance actual: %v, required:%v",
			util.FormatERC20Balance(balance),
			util.FormatERC20Balance(stakeAmt))
	}

	auth, err := PrepareEthTransaction(ctx, client, account)
	if err != nil {
		return errors.Wrap(err, "prepare ethereum transaction")
	}

	tx, err := contract.Caller.DepositStake(auth)
	if err != nil {
		return errors.Wrap(err, "contract failed")
	}
	level.Info(logger).Log("msg", "stake depositied", "txHash", tx.Hash().Hex())
	return nil
}

func ShowStatus(
	ctx context.Context,
	logger log.Logger,
	client rpc.ETHClient,
	contract *contracts.Tellor,
	account *rpc.Account,
) error {
	status, startTime, err := contract.Getter.GetStakerInfo(nil, account.Address)
	if err != nil {
		return errors.Wrap(err, "get stake status")
	}

	printStakeStatus(status, startTime)
	return nil
}

func RequestStakingWithdraw(
	ctx context.Context,
	logger log.Logger,
	client rpc.ETHClient,
	contract *contracts.Tellor,
	account *rpc.Account,
) error {

	status, startTime, err := contract.Getter.GetStakerInfo(nil, account.Address)
	if err != nil {
		return errors.Wrap(err, "get stake status")
	}
	if status.Uint64() != 1 {
		printStakeStatus(status, startTime)
		return nil
	}

	auth, err := PrepareEthTransaction(ctx, client, account)
	if err != nil {
		return errors.Wrap(err, "prepare ethereum transaction")
	}

	tx, err := contract.Caller.RequestStakingWithdraw(auth)
	if err != nil {
		return errors.Wrap(err, "contract")
	}

	level.Info(logger).Log("msg", "withdrawal request sent", "txHash", tx.Hash().Hex())
	return nil
}

func WithdrawStake(
	ctx context.Context,
	logger log.Logger,
	client rpc.ETHClient,
	contract *contracts.Tellor,
	account *rpc.Account,
) error {
	status, startTime, err := contract.Getter.GetStakerInfo(nil, account.Address)
	if err != nil {
		return errors.Wrap(err, "get stake status")
	}
	if status.Uint64() != 2 {

		fmt.Printf("Can't withdraw")
		printStakeStatus(status, startTime)
		return nil
	}

	auth, err := PrepareEthTransaction(ctx, client, account)
	if err != nil {
		return errors.Wrap(err, "prepare ethereum transaction")
	}

	tx, err := contract.Caller.WithdrawStake(auth)
	if err != nil {
		return errors.Wrap(err, "contract")
	}
	level.Info(logger).Log("msg", "withdrew stake", "txHash", tx.Hash().Hex())
	return nil
}

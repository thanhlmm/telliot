// Copyright (c) The Tellor Authors.
// Licensed under the MIT License.

package ops

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/go-kit/kit/log"
	"github.com/pkg/errors"
	"github.com/tellor-io/telliot/pkg/apiOracle"
	"github.com/tellor-io/telliot/pkg/config"
	"github.com/tellor-io/telliot/pkg/contracts"
	"github.com/tellor-io/telliot/pkg/contracts/master"
	"github.com/tellor-io/telliot/pkg/rpc"
	"github.com/tellor-io/telliot/pkg/tracker"
	"github.com/tellor-io/telliot/pkg/util"
)

/**
 * This file handles all operations related to disputes
 */

func Dispute(
	ctx context.Context,
	client rpc.ETHClient,
	contract *contracts.Tellor,
	account *rpc.Account,
	requestId *big.Int,
	timestamp *big.Int,
	minerIndex *big.Int,
) error {

	if !minerIndex.IsUint64() || minerIndex.Uint64() > 4 {
		return errors.Errorf("miner index should be between 0 and 4 (got %s)", minerIndex.Text(10))
	}

	balance, err := contract.Getter.BalanceOf(nil, account.Address)
	if err != nil {
		return errors.Wrap(err, "fetch balance")
	}
	var asBytes32 [32]byte
	copy(asBytes32[:], "0x8b75eb45d88e80f0e4ec77d23936268694c0e7ac2e0c9085c5c6bdfcfbc49239") // keccak256(disputeFee).
	disputeCost, err := contract.Getter.GetUintVar(nil, asBytes32)
	if err != nil {
		return errors.Wrap(err, "get dispute cost")
	}

	if balance.Cmp(disputeCost) < 0 {
		return errors.Errorf("insufficient balance TRB actual: %v, TRB required:%v)",
			util.FormatERC20Balance(balance),
			util.FormatERC20Balance(disputeCost))
	}

	auth, err := PrepareEthTransaction(ctx, client, account)
	if err != nil {
		return errors.Wrapf(err, "prepare ethereum transaction")
	}

	tx, err := contract.Caller.BeginDispute(auth, requestId, timestamp, minerIndex)
	if err != nil {
		return errors.Wrap(err, "send dispute txn")
	}
	fmt.Printf("dispute started with txn: %s\n", tx.Hash().Hex())
	return nil
}

func Vote(
	ctx context.Context,
	client rpc.ETHClient,
	contract *contracts.Tellor,
	account *rpc.Account,
	disputeId *big.Int,
	supportsDispute bool,
) error {

	voted, err := contract.Getter.DidVote(nil, disputeId, contract.Address)
	if err != nil {
		return errors.Wrapf(err, "check if you've already voted")
	}
	if voted {
		fmt.Printf("You have already voted on this dispute\n")
		return nil
	}

	auth, err := PrepareEthTransaction(ctx, client, account)
	if err != nil {
		return errors.Wrapf(err, "prepare ethereum transaction")
	}
	tx, err := contract.Caller.Vote(auth, disputeId, supportsDispute)
	if err != nil {
		return errors.Wrapf(err, "submit vote transaction")
	}

	fmt.Printf("Vote submitted with transaction %s\n", tx.Hash().Hex())
	return nil
}

func getNonceSubmissions(
	ctx context.Context,
	client rpc.ETHClient,
	contract *contracts.Tellor,
	valueBlock *big.Int,
	dispute *master.TellorDisputeNewDispute,
) ([]*apiOracle.PriceStamp, error) {
	tokenAbi, err := abi.JSON(strings.NewReader(master.TellorLibraryABI))
	if err != nil {
		return nil, errors.Wrap(err, "parse abi")
	}

	// Just use nil for most of the variables, only using this object to call UnpackLog which only uses the abi
	bar := bind.NewBoundContract(contract.Address, tokenAbi, nil, nil, nil)

	allVals, err := contract.Getter.GetSubmissionsByTimestamp(nil, dispute.RequestId, dispute.Timestamp)
	if err != nil {
		return nil, errors.Wrap(err, "get other submitted values for dispute")
	}

	allAddrs, err := contract.Getter.GetMinersByRequestIdAndTimestamp(nil, dispute.RequestId, dispute.Timestamp)
	if err != nil {
		return nil, errors.Wrap(err, "get miner addresses for dispute")
	}

	const blockStep = 100
	high := int64(valueBlock.Uint64())
	low := high - blockStep
	nonceSubmitID := tokenAbi.Events["NonceSubmitted"].ID
	timedValues := make([]*apiOracle.PriceStamp, 5)
	found := 0
	for found < 5 {
		query := ethereum.FilterQuery{
			FromBlock: big.NewInt(low),
			ToBlock:   big.NewInt(high),
			Addresses: []common.Address{contract.Address},
			Topics:    [][]common.Hash{{nonceSubmitID}},
		}

		logs, err := client.FilterLogs(ctx, query)
		if err != nil {
			return nil, errors.Wrap(err, "get nonce logs")
		}

		for _, l := range logs {
			nonceSubmit := master.TellorLibraryNonceSubmitted{}
			err := bar.UnpackLog(&nonceSubmit, "NonceSubmitted", l)
			if err != nil {
				return nil, errors.Wrap(err, "unpack into object")
			}
			header, err := client.HeaderByNumber(ctx, big.NewInt(int64(l.BlockNumber)))
			if err != nil {
				return nil, errors.Wrap(err, "get nonce block header")
			}
			for i := 0; i < 5; i++ {
				if nonceSubmit.Miner == allAddrs[i] {
					valTime := time.Unix(int64(header.Time), 0)

					bigF := new(big.Float)
					bigF.SetInt(allVals[i])
					f, _ := bigF.Float64()

					timedValues[i] = &apiOracle.PriceStamp{
						Created:   valTime,
						PriceInfo: apiOracle.PriceInfo{Price: f},
					}
					found++
					break
				}
			}
		}
		high -= blockStep
		low = high - blockStep
	}
	return timedValues, nil
}

func List(
	ctx context.Context,
	logger log.Logger,
	client rpc.ETHClient,
	contract *contracts.Tellor,
	account *rpc.Account,
) error {
	cfg := config.GetConfig()
	tokenAbi, err := abi.JSON(strings.NewReader(master.TellorDisputeABI))
	if err != nil {
		return errors.Wrap(err, "parse abi")
	}

	// Just use nil for most of the variables, only using this object to call UnpackLog which only uses the abi.
	bar := bind.NewBoundContract(contract.Address, tokenAbi, nil, nil, nil)

	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "get latest eth block header")
	}

	startBlock := big.NewInt(10e3 * 14)
	startBlock.Sub(header.Number, startBlock)
	newDisputeID := tokenAbi.Events["NewDispute"].ID
	query := ethereum.FilterQuery{
		FromBlock: startBlock,
		ToBlock:   nil,
		Addresses: []common.Address{contract.Address},
		Topics:    [][]common.Hash{{newDisputeID}},
	}

	logs, err := client.FilterLogs(ctx, query)
	if err != nil {
		return errors.Wrap(err, "filter eth logs")
	}

	fmt.Printf("There are currently %d open disputes\n", len(logs))
	fmt.Printf("-------------------------------------\n")
	for _, rawDispute := range logs {
		dispute := master.TellorDisputeNewDispute{}
		err := bar.UnpackLog(&dispute, "NewDispute", rawDispute)
		if err != nil {
			return errors.Wrap(err, "unpack dispute event from logs")
		}
		_, executed, votePassed, _, reportedAddr, reportingMiner, _, uintVars, currTally, err := contract.Getter.GetAllDisputeVars(nil, dispute.DisputeId)
		if err != nil {
			return errors.Wrap(err, "get dispute details")
		}

		votingEnds := time.Unix(uintVars[3].Int64(), 0)
		createdTime := votingEnds.Add(-7 * 24 * time.Hour)

		var descString string
		if executed {
			descString = "complete, "
			if votePassed {
				descString += "successful"
			} else {
				descString += "rejected"
			}
		} else {
			descString = "in progress"
		}

		fmt.Printf("Dispute %s (%s):\n", dispute.DisputeId.String(), descString)
		fmt.Printf("    Accused Party: %s\n", reportedAddr.Hex())
		fmt.Printf("    Disputed by: %s\n", reportingMiner.Hex())
		fmt.Printf("    Created on:  %s\n", createdTime.Format("3:04 PM January 02, 2006 MST"))
		fmt.Printf("    Fee: %s TRB\n", util.FormatERC20Balance(uintVars[8]))
		fmt.Printf("    \n")
		fmt.Printf("    Value disputed for requestID %d:\n", dispute.RequestId.Uint64())

		allSubmitted, err := getNonceSubmissions(ctx, client, contract, uintVars[5], &dispute)
		if err != nil {
			return errors.Wrapf(err, "get the values submitted by other miners for the disputed block")
		}
		disputedValTime := allSubmitted[uintVars[6].Uint64()].Created

		for i := len(allSubmitted) - 1; i >= 0; i-- {
			sub := allSubmitted[i]
			valStr := fmt.Sprintf("%f\n", sub.Price)
			var pointerStr string
			if i == int(uintVars[6].Uint64()) {
				pointerStr = " <--disputed"
			}

			fmt.Printf("      %s @ %s%s\n", valStr, sub.Created.Format("3:04:05 PM"), pointerStr)
		}
		fmt.Printf("    \n")

		tmp := new(big.Float)
		tmp.SetInt(currTally)
		currTallyFloat, _ := tmp.Float64()
		tmp.SetInt(uintVars[7])
		currQuorum, _ := tmp.Float64()
		currTallyFloat += currQuorum
		currTallyRatio := currTallyFloat / 2 * currQuorum
		fmt.Printf("    Currently %.0f%% of %s TRB support this dispute (%s votes)\n", currTallyRatio*100, util.FormatERC20Balance(uintVars[7]), uintVars[4])

		result := tracker.CheckValueAtTime(cfg, dispute.RequestId.Uint64(), uintVars[2], disputedValTime)
		if result == nil || len(result.Datapoints) < 0 {
			fmt.Printf("      No data available for recommendation\n")
			continue
		}
		fmt.Printf("      Recommendation:\n")
		fmt.Printf("      Vote %t\n", !result.WithinRange)
		fmt.Printf("      Submitted value %s, expected range %.0f to %0.f\n", uintVars[2].String(), result.Low, result.High)
		numToShow := 3
		if numToShow > len(result.Datapoints) {
			numToShow = len(result.Datapoints)
		}
		fmt.Printf("      Based on %d locally saved datapoints within %.0f minutes (showing closest %d)\n",
			len(result.Datapoints), cfg.DisputeTimeDelta.Duration.Minutes(), numToShow)
		minTotalDelta := time.Duration(math.MaxInt64)
		index := 0
		for i := 0; i < len(result.Datapoints)-numToShow; i++ {
			totalDelta := time.Duration(0)
			for j := 0; j < numToShow; j++ {
				delta := result.Times[i+j].Sub(disputedValTime)
				if delta < 0 {
					delta = -delta
				}
				totalDelta += delta
			}
			if totalDelta < minTotalDelta {
				minTotalDelta = totalDelta
				index = i
			}
		}
		for i := 0; i < numToShow; i++ {
			dp := result.Datapoints[index+i]
			t := result.Times[index+i]
			fmt.Printf("        %f, ", dp)
			delta := disputedValTime.Sub(t)
			if delta > 0 {
				fmt.Printf("%.0fs before\n", delta.Seconds())
			} else {
				fmt.Printf("%.0fs after\n", (-delta).Seconds())
			}
		}
		fmt.Printf("\n")
	}

	return nil
}

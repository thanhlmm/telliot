// Copyright (c) The Tellor Authors.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	cli "github.com/jawher/mow.cli"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	tellorCommon "github.com/tellor-io/telliot/pkg/common"
	"github.com/tellor-io/telliot/pkg/config"
	"github.com/tellor-io/telliot/pkg/contracts"
	"github.com/tellor-io/telliot/pkg/db"
	"github.com/tellor-io/telliot/pkg/ops"
	"github.com/tellor-io/telliot/pkg/rest"
	"github.com/tellor-io/telliot/pkg/rpc"
	"github.com/tellor-io/telliot/pkg/util"
)

var ctx context.Context
var cont contracts.Tellor
var acc rpc.Account
var clt rpc.ETHClient
var logLevel string
var database db.DB
var proxy db.DataServerProxy

func ExitOnError(err error, operation string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %s\n", operation, err.Error())
		cli.Exit(-1)
	}
}

func setup() error {
	cfg := config.GetConfig()

	err := util.SetupLoggingConfig(cfg.Logger)
	if err != nil {
		return errors.Wrapf(err, "parsing log config")
	}

	logLevel = cfg.LogLevel

	if !cfg.EnablePoolWorker {
		// Create an rpc client
		client, err := rpc.NewClient(os.Getenv(config.NodeURLEnvName))
		if err != nil {
			return errors.Wrap(err, "create rpc client instance")
		}

		contract, err := contracts.NewTellor(cfg, client)
		if err != nil {
			return errors.Wrap(err, "creating contract")
		}

		account, err := rpc.NewAccount(cfg)
		if err != nil {
			return errors.Wrap(err, "creating account")
		}
		ctx = context.Background()
		// Issue #55, halt if client is still syncing with Ethereum network
		s, err := client.IsSyncing(ctx)
		if err != nil {
			return errors.Wrap(err, "determining if Ethereum client is syncing")
		}
		if s {
			return errors.New("ethereum node is still syncing with the network")
		}

		clt = client
		acc = account
		cont = contract
	}
	return nil
}

// migrateAndOpenDB migrates the tx costs and deletes the db.
// The DB is always deleted because the price avarages calculations
// is not calculated properly between restarts.
// TODO don't do this and just improve the price calculations.
func migrateAndOpenDB() (db.DB, error) {
	cfg := config.GetConfig()
	// Create a db instance
	DB, err := db.Open(cfg.DBFile)
	if err != nil {
		return nil, errors.Wrapf(err, "opening DB instance")
	}

	var txsGas [][]byte
	for i := 0; i <= 5; i++ {
		txID := tellorCommon.PriceTXs + strconv.Itoa(i)
		txGas, err := DB.Get(txID)
		if err == nil && len(txGas) > 0 {
			txsGas = append(txsGas, txGas)
		}
	}
	if err := DB.Close(); err != nil {
		return nil, errors.Wrapf(err, "closing DB instance for migration")
	}
	os.RemoveAll(cfg.DBFile)

	DB, err = db.Open(cfg.DBFile)
	if err != nil {
		return nil, errors.Wrapf(err, "opening DB instance")
	}

	for i, txGas := range txsGas {
		txID := tellorCommon.PriceTXs + strconv.Itoa(i)
		_ = DB.Put(txID, txGas)
	}

	return DB, nil
}

func AddDBToCtx(remote bool) error {
	DB, err := migrateAndOpenDB()
	if err != nil {
		return errors.Wrap(err, "opening DB instance")
	}
	var dataProxy db.DataServerProxy
	if remote {
		proxy, err := db.OpenRemoteDB(DB)
		if err != nil {
			return errors.Wrapf(err, "open remote DB instance")

		}
		dataProxy = proxy
	} else {
		proxy, err := db.OpenLocalProxy(DB)
		if err != nil {
			return errors.Wrapf(err, "opening local DB instance:")

		}
		dataProxy = proxy
	}
	database = DB
	proxy = dataProxy
	return nil
	"fmt"
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	"github.com/alecthomas/kong"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/pkg/errors"
	tellorCommon "github.com/tellor-io/telliot/pkg/common"
	"github.com/tellor-io/telliot/pkg/config"
	"github.com/tellor-io/telliot/pkg/contracts/getter"
	"github.com/tellor-io/telliot/pkg/contracts/tellor"
	"github.com/tellor-io/telliot/pkg/db"
	"github.com/tellor-io/telliot/pkg/ops"
	"github.com/tellor-io/telliot/pkg/rpc"
	"github.com/tellor-io/telliot/pkg/util"
)

var ctx context.Context

// var cont tellorCommon.Contract
// var acc tellorCommon.Account
// var clt rpc.ETHClient

// func ExitOnError(err error, operation string) {
// 	if err != nil {
// 		fmt.Fprintf(os.Stderr, "%s failed: %s\n", operation, err.Error())
// 		cli.Exit(-1)
// 	}
// }

func setup() (rpc.ETHClient, tellorCommon.Contract, tellorCommon.Account, error) {

	cfg := config.GetConfig()

	if !cfg.EnablePoolWorker {

		// Create an rpc client
		client, err := rpc.NewClient(cfg.NodeURL)
		if err != nil {
			return nil, tellorCommon.Contract{}, tellorCommon.Account{}, errors.Wrap(err, "create rpc client instance")
		}

		// Create an instance of the tellor master contract for on-chain interactions
		contractAddress := common.HexToAddress(cfg.ContractAddress)
		contractTellorInstance, err := tellor.NewTellor(contractAddress, client)
		if err != nil {
			return nil, tellorCommon.Contract{}, tellorCommon.Account{}, errors.Wrap(err, "create tellor master instance")
		}

		contractGetterInstance, err := getter.NewTellorGetters(contractAddress, client)

		if err != nil {
			return nil, tellorCommon.Contract{}, tellorCommon.Account{}, errors.Wrap(err, "create tellor transactor instance")
		}
		// Leaving those in because are still used in some places(miner submission mostly).
		ctx := context.WithValue(context.Background(), tellorCommon.ClientContextKey, client)
		ctx = context.WithValue(ctx, tellorCommon.ContractAddress, contractAddress)
		ctx = context.WithValue(ctx, tellorCommon.ContractsTellorContextKey, contractTellorInstance)
		ctx = context.WithValue(ctx, tellorCommon.ContractsGetterContextKey, contractGetterInstance)

		privateKey, err := crypto.HexToECDSA(cfg.PrivateKey)
		if err != nil {
			return nil, tellorCommon.Contract{}, tellorCommon.Account{}, errors.Wrap(err, "getting private key to ECDSA")
		}

		publicKey := privateKey.Public()
		publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
		if !ok {
			return nil, tellorCommon.Contract{}, tellorCommon.Account{}, errors.New("casting public key to ECDSA")
		}

		publicAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

		// Issue #55, halt if client is still syncing with Ethereum network
		s, err := client.IsSyncing(ctx)
		if err != nil {
			return nil, tellorCommon.Contract{}, tellorCommon.Account{}, errors.Wrap(err, "determining if Ethereum client is syncing")
		}
		if s {
			return nil, tellorCommon.Contract{}, tellorCommon.Account{}, errors.New("ethereum node is still syncing with the network")
		}

		account := tellorCommon.Account{
			Address:    publicAddress,
			PrivateKey: privateKey,
		}
		contract := tellorCommon.Contract{
			Getter:  contractGetterInstance,
			Caller:  contractTellorInstance,
			Address: contractAddress,
		}
		return client, contract, account, nil
	}
	// Not sure why we need this case.
	return nil, tellorCommon.Contract{}, tellorCommon.Account{}, nil
}

func AddDBToCtx(remote bool) error {
	cfg := config.GetConfig()
	// Create a db instance
	os.RemoveAll(cfg.DBFile)
	DB, err := db.Open(cfg.DBFile)
	if err != nil {
		return errors.Wrapf(err, "opening DB instance")
	}

	var dataProxy db.DataServerProxy
	if remote {
		proxy, err := db.OpenRemoteDB(DB)
		if err != nil {
			return errors.Wrapf(err, "open remote DB instance")

		}
		dataProxy = proxy
	} else {
		proxy, err := db.OpenLocalProxy(DB)
		if err != nil {
			return errors.Wrapf(err, "opening local DB instance:")

		}
		dataProxy = proxy
	}
	ctx = context.WithValue(ctx, tellorCommon.DataProxyKey, dataProxy)
	ctx = context.WithValue(ctx, tellorCommon.DBContextKey, DB)
	return nil
}

// var GitTag string
// var GitHash string

// const versionMessage = `
//     The official Tellor cli tool %s (%s)
//     -----------------------------------------
// 	Website: https://tellor.io
// 	Github:  https://github.com/tellor-io/telliot
// `

// func App() *cli.Cli {

// 	app := cli.App("telliot", "The tellor.io official cli tool")

// 	// App wide config options
// 	configPath := app.StringOpt("config", "configs/config.json", "Path to the primary JSON config file")
// 	logLevel := app.StringOpt("logLevel", "error", "The level of log messages")
// 	logPath := app.StringOpt("logConfig", "", "Path to a JSON logging config file")

// 	logSetup := util.SetupLogger()
// 	// This will get run before any of the commands
// 	app.Before = func() {
// 		ExitOnError(util.ParseLoggingConfig(*logPath), "parsing log file")
// 		ExitOnError(config.ParseConfig(*configPath), "parsing config file")
// 		ExitOnError(setup(), "setting up")
// 	}

// 	versionMessage := fmt.Sprintf(versionMessage, GitTag, GitHash)
// 	app.Version("version", versionMessage)

// 	app.Command("stake", "staking operations", stakeCmd(logSetup, logLevel))
// 	app.Command("transfer", "send TRB to address", moveCmd(ops.Transfer, logSetup, logLevel))
// 	app.Command("approve", "approve TRB to address", moveCmd(ops.Approve, logSetup, logLevel))
// 	app.Command("balance", "check balance of address", balanceCmd)
// 	app.Command("dispute", "dispute operations", disputeCmd(logSetup, logLevel))
// 	app.Command("mine", "mine for TRB", mineCmd(logSetup, logLevel))
// 	app.Command("dataserver", "start an independent dataserver", dataserverCmd(logSetup, logLevel))
// 	return app
// }

// func stakeCmd(logSetup func(string) log.Logger, logLevel *string) func(*cli.Cmd) {
// 	return func(cmd *cli.Cmd) {
// 		cmd.Command("deposit", "deposit TRB stake", simpleCmd(ops.Deposit, logSetup, logLevel))
// 		cmd.Command("withdraw", "withdraw TRB stake", simpleCmd(ops.WithdrawStake, logSetup, logLevel))
// 		cmd.Command("request", "withdraw TRB stake", simpleCmd(ops.RequestStakingWithdraw, logSetup, logLevel))
// 		cmd.Command("status", "show current staking status", simpleCmd(ops.ShowStatus, logSetup, logLevel))
// 	}
// }

// func simpleCmd(
// 	f func(context.Context,
// 		log.Logger,
// 		rpc.ETHClient,
// 		tellorCommon.Contract,
// 		tellorCommon.Account) error,
// 	logSetup func(string) log.Logger,
// 	logLevel *string) func(*cli.Cmd) {
// 	return func(cmd *cli.Cmd) {
// 		cmd.Action = func() {
// 			ExitOnError(f(ctx, logSetup(*logLevel), clt, cont, acc), "")
// 		}
// 	}
// }

// func moveCmd(
// 	f func(context.Context,
// 		log.Logger,
// 		rpc.ETHClient,
// 		tellorCommon.Contract,
// 		tellorCommon.Account,
// 		common.Address,
// 		*big.Int) error,
// 	logSetup func(string) log.Logger,
// 	logLevel *string) func(*cli.Cmd) {
// 	return func(cmd *cli.Cmd) {
// 		amt := TRBAmount{}
// 		addr := ETHAddress{}
// 		cmd.VarArg("AMOUNT", &amt, "amount to transfer")
// 		cmd.VarArg("ADDRESS", &addr, "ethereum public address")
// 		cmd.Action = func() {
// 			ExitOnError(f(ctx, logSetup(*logLevel), clt, cont, acc, addr.addr, amt.Int), "move")
// 		}
// 	}
// }

// func balanceCmd(cmd *cli.Cmd) {
// 	addr := ETHAddress{}
// 	cmd.VarArg("ADDRESS", &addr, "ethereum public address")
// 	cmd.Spec = "[ADDRESS]"
// 	cmd.Action = func() {
// 		// Using values from context, until we have a function that setups the client and returns as values, not as part of the context
// 		commonAddress := ctx.Value(tellorCommon.PublicAddress).(common.Address)
// 		var zero [20]byte
// 		if bytes.Equal(addr.addr.Bytes(), zero[:]) {
// 			addr.addr = commonAddress
// 		}
// 		ExitOnError(ops.Balance(ctx, clt, cont.Getter, addr.addr), "checking balance")
// 	}

// }

// func disputeCmd(loggerSetup func(string) log.Logger, logLevel *string) func(*cli.Cmd) {
// 	return func(cmd *cli.Cmd) {
// 		cmd.Command("vote", "vote on an active dispute", voteCmd)
// 		cmd.Command("new", "start a new dispute", newDisputeCmd)
// 		cmd.Command("show", "show existing disputes", simpleCmd(ops.List, loggerSetup, logLevel))
// 	}
// }

// func voteCmd(cmd *cli.Cmd) {
// 	disputeID := EthereumInt{}
// 	cmd.VarArg("DISPUTE_ID", &disputeID, "dispute id")
// 	supports := cmd.BoolArg("SUPPORT", false, "do you support the dispute? (true|false)")
// 	cmd.Action = func() {
// 		ExitOnError(ops.Vote(ctx, clt, cont, acc, disputeID.Int, *supports), "vote")
// 	}
// }

func (m mineCmd) Run(logger log.Logger) error {
	// Create os kill sig listener.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	exitChannels := make([]*chan os.Signal, 0)

	cfg := config.GetConfig()
	var ds *ops.DataServerOps
	if !cfg.EnablePoolWorker {
		var err error
		err = AddDBToCtx(m.remote)
		if err != nil {
			return errors.Wrapf(err, "initializing database")
		}
		if !m.remote {
			ch := make(chan os.Signal)
			exitChannels = append(exitChannels, &ch)

			ds, err = ops.CreateDataServerOps(ctx, logger, ch)
			if err != nil {
				return errors.Wrapf(err, "creating data server")
			}
			// Start and wait for it to be ready.
			if err := ds.Start(ctx); err != nil {
				return errors.Wrapf(err, "starting data server")
			}
			<-ds.Ready()
		}
	}
	// Start miner
	DB := ctx.Value(tellorCommon.DataProxyKey).(db.DataServerProxy)
	v, err := DB.Get(db.DisputeStatusKey)
	if err != nil {
		level.Warn(logger).Log("msg", "getting dispute status. Check if staked")
	}
	status, _ := hexutil.DecodeBig(string(v))
	if status.Cmp(big.NewInt(1)) != 0 {
		return errors.New("miner is not able to mine with current status")
	}
	ch := make(chan os.Signal)
	exitChannels = append(exitChannels, &ch)
	miner, err := ops.CreateMiningManager(logger, ch, cfg, DB)
	if err != nil {
		return errors.Wrapf(err, "creating miner")
	}
	go func() {
		miner.Start(ctx)
	}()

	// Wait for kill sig.
	<-c
	// Then notify exit channels.
	for _, ch := range exitChannels {
		*ch <- os.Interrupt
	}
	cnt := 0
	start := time.Now()
	for {
		cnt++
		dsStopped := false
		minerStopped := false

		if ds != nil {
			dsStopped = !ds.Running
		} else {
			dsStopped = true
		}

		if miner != nil {
			minerStopped = !miner.Running
		} else {
			minerStopped = true
		}

		if !dsStopped && !minerStopped && cnt > 60 {
			level.Warn(logger).Log("msg", "taking longer than expected to stop operations", "waited", time.Since(start))
		} else if dsStopped && minerStopped {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	level.Info(logger).Log("msg", "main shutdown complete")
	return nil
}

func (d dataserverCmd) Run(logger log.Logger) error {
	// Create os kill sig listener.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	var ds *ops.DataServerOps
	var err error
	err = AddDBToCtx(true)
	if err != nil {
		return errors.Wrapf(err, "initializing database")
	}
	ch := make(chan os.Signal)
	ds, err = ops.CreateDataServerOps(ctx, logger, ch)
	if err != nil {
		return errors.Wrapf(err, "creating data server")
	}
	// Start and wait for it to be ready
	if err := ds.Start(ctx); err != nil {
		return errors.Wrapf(err, "starting data server")
	}
	<-ds.Ready()

	// Wait for kill sig.
	<-c
	// Notify exit channels.
	ch <- os.Interrupt

	cnt := 0
	start := time.Now()
	for {
		cnt++
		dsStopped := false

		if ds != nil {
			dsStopped = !ds.Running
		} else {
			dsStopped = true
		}

		if !dsStopped && cnt > 60 {
			level.Warn(logger).Log("msg", "taking longer than expected to stop operations", "waited", time.Since(start))
		} else if dsStopped {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	level.Info(logger).Log("msg", "main shutdown complete")
	return nil
}

// func main() {
// 	// Programming is easy. Just create an App() and run it!!!!!
// 	app := App()
// 	err := app.Run(os.Args)
// 	if err != nil {
// 		fmt.Fprintf(os.Stderr, "app.Run failed: %v\n", err)
// 	}
// }

var cli struct {
	Config    configPath    `help: path to config file`
	LogConfig logConfigPath `optional type:"path"`
	LogLevel  logLevel      `type:"path"`
	Transfer  transferCmd   `cmd help:"Transfer tokens"`
	Approve   approveCmd    `cmd help:"Approve tokens"`
	Balance   balanceCmd    `cmd help:"Check the balance of an address"`
	Stake     stakeCmd      `cmd help:"perform one of the stake operations"`
	// Dispute   struct {
	// 	//		New newDisputeCmd `cmd`
	// 	New struct {
	// 		Requestid  int `arg required`
	// 		Timestamp  int `arg required`
	// 		MinerIndex int `arg required`
	// 	} `cmd`
	// 	Vote struct {
	// 		disputeId int  `arg required`
	// 		support   bool `arg required`
	// 	} `cmd`
	// 	Show struct {
	// 	} `cmd`
	// } `cmd`
	Dataserver dataserverCmd `cmd`
	Mine       mineCmd       `cmd`
}

type configPath string
type logConfigPath string
type logLevel string

type dataserverCmd struct {
}

type mineCmd struct {
	remote bool
}

func (l logLevel) BeforeApply(ctx *kong.Context) error {
	logger := util.GetLogger(string(l))
	ctx.Bind(logger)
	return nil
}

func (c configPath) AfterApply(ctx *kong.Context) error {
	err := config.ParseConfig(string(c))
	if err != nil {
		return errors.Wrapf(err, "parsing config")
	}
	fmt.Println("llalas")
	client, contract, account, err := setup()
	if err != nil {
		return errors.Wrapf(err, "setting up variables")
	}

	ctx.Bind(client)
	ctx.Bind(contract)
	ctx.Bind(account)
	return nil
}

func (c logConfigPath) BeforeApply(ctx *kong.Context) error {
	err := util.ParseLoggingConfig(string(c))
	if err != nil {
		return errors.Wrapf(err, "parsing log config")
	}
	return nil
}

type newDisputeCmd struct {
	requestID  string `arg required `
	minerIndex string `arg required `
	timestamp  string `arg required `
}

func (n newDisputeCmd) Run(logger log.Logger, client rpc.ETHClient, contract tellorCommon.Contract, account tellorCommon.Account) error {
	requestID := EthereumInt{}
	err := requestID.Set(n.requestID)
	if err != nil {
		return errors.Wrapf(err, "parsing argument")
	}
	timestamp := EthereumInt{}
	err = timestamp.Set(n.timestamp)
	if err != nil {
		return errors.Wrapf(err, "parsing argument")
	}
	minerIndex := EthereumInt{}
	err = minerIndex.Set(n.minerIndex)
	if err != nil {
		return errors.Wrapf(err, "parsing argument")
	}
	return ops.Dispute(ctx, client, contract, account, requestID.Int, timestamp.Int, minerIndex.Int)
}

type voteCmd struct {
	disputeId string `arg required`
	support   bool   `arg required`
}

func (v voteCmd) Run(client rpc.ETHClient, contract tellorCommon.Contract, account tellorCommon.Account) error {
	disputeID := EthereumInt{}
	err := disputeID.Set(v.disputeId)
	if err != nil {
		return errors.Wrapf(err, "parsing argument")
	}
	return ops.Vote(ctx, client, contract, account, disputeID.Int, v.support)
}

type stakeCmd struct {
	Operation string `arg required`
}

func (s *stakeCmd) Run(logger log.Logger, client rpc.ETHClient, contract tellorCommon.Contract, account tellorCommon.Account) error {
	switch s.Operation {
	case "deposit":
		return ops.Deposit(ctx, logger, client, contract, account)
	case "withdraw":
		return ops.WithdrawStake(ctx, logger, client, contract, account)
	case "request":
		return ops.RequestStakingWithdraw(ctx, logger, client, contract, account)
	case "status":
		return ops.ShowStatus(ctx, logger, client, contract, account)
	default:
		return errors.New("unknown stake command")
	}
}

type balanceCmd struct {
	Address string `arg optional`
}
type transferCmd tokenCmd
type approveCmd tokenCmd
type tokenCmd struct {
	Address string `arg required`
	Amount  string `arg required`
}

func (b *balanceCmd) Run(client rpc.ETHClient, contract tellorCommon.Contract) error {
	fmt.Println("las")
	addr := ETHAddress{}
	var err error
	if b.Address == "" {
		err = addr.Set(contract.Address.String())
		if err != nil {
			return errors.Wrapf(err, "parsing argument")
		}
	} else {
		err = addr.Set(b.Address)
		if err != nil {
			return errors.Wrapf(err, "parsing argument")
		}
	}
	return ops.Balance(ctx, client, contract.Getter, addr.addr)
}

func (c *transferCmd) Run(logger log.Logger, client rpc.ETHClient, contract tellorCommon.Contract, account tellorCommon.Account) error {
	address := ETHAddress{}
	err := address.Set(c.Address)
	if err != nil {
		return errors.Wrapf(err, "parsing argument")
	}
	amount := EthereumInt{}
	err = amount.Set(c.Amount)
	if err != nil {
		return errors.Wrapf(err, "parsing argument")
	}
	return ops.Transfer(ctx, logger, client, contract, account, address.addr, amount.Int)
}

func (c *approveCmd) Run(logger log.Logger, client rpc.ETHClient, contract tellorCommon.Contract, account tellorCommon.Account) error {
	address := ETHAddress{}
	err := address.Set(c.Address)
	if err != nil {
		return errors.Wrapf(err, "parsing argument")
	}
	amount := EthereumInt{}
	err = amount.Set(c.Amount)
	if err != nil {
		return errors.Wrapf(err, "parsing argument")
	}
	return ops.Approve(ctx, logger, client, contract, account, address.addr, amount.Int)
}

func main() {
	ctx := kong.Parse(&cli)
	err := ctx.Run(ctx)
	ctx.FatalIfErrorf(err)
}

// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package runtime

import (
	"errors"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"

	ourVm "github.com/Gealber/evm-simulator/vm"
)

// Config is a basic type specifying certain configuration flags for running
// the EVM.
type Config struct {
	ChainConfig *params.ChainConfig
	Difficulty  *big.Int
	Origin      common.Address
	Coinbase    common.Address
	BlockNumber *big.Int
	Time        uint64
	GasLimit    uint64
	GasPrice    *big.Int
	Value       *big.Int
	Debug       bool
	EVMConfig   vm.Config
	BaseFee     *big.Int
	BlobBaseFee *big.Int
	BlobHashes  []common.Hash
	BlobFeeCap  *big.Int
	Random      *common.Hash
	RPCEndpoint string
	ErrorRatio  float64

	GetHashFn func(n uint64) common.Hash
}

type RecordToInitiateState struct {
	AddressCodeSet    map[common.Address]struct{}
	AddressBalanceSet map[common.Address]struct{}
	AddressStorageSet map[string]common.Hash
	AccessList        types.AccessList
}

// sets defaults on the config
func SetDefaults(cfg *Config) {
	if cfg.ChainConfig == nil {
		var (
			shanghaiTime = uint64(0)
			cancunTime   = uint64(0)
		)
		cfg.ChainConfig = &params.ChainConfig{
			ChainID:                       big.NewInt(1),
			HomesteadBlock:                new(big.Int),
			DAOForkBlock:                  new(big.Int),
			DAOForkSupport:                false,
			EIP150Block:                   new(big.Int),
			EIP155Block:                   new(big.Int),
			EIP158Block:                   new(big.Int),
			ByzantiumBlock:                new(big.Int),
			ConstantinopleBlock:           new(big.Int),
			PetersburgBlock:               new(big.Int),
			IstanbulBlock:                 new(big.Int),
			MuirGlacierBlock:              new(big.Int),
			BerlinBlock:                   new(big.Int),
			LondonBlock:                   new(big.Int),
			ArrowGlacierBlock:             nil,
			GrayGlacierBlock:              nil,
			TerminalTotalDifficulty:       big.NewInt(0),
			TerminalTotalDifficultyPassed: true,
			MergeNetsplitBlock:            nil,
			ShanghaiTime:                  &shanghaiTime,
			CancunTime:                    &cancunTime}
	}
	if cfg.Difficulty == nil {
		cfg.Difficulty = new(big.Int)
	}
	if cfg.GasLimit == 0 {
		cfg.GasLimit = math.MaxUint64
	}
	if cfg.GasPrice == nil {
		cfg.GasPrice = new(big.Int)
	}
	if cfg.Value == nil {
		cfg.Value = new(big.Int)
	}
	if cfg.BlockNumber == nil {
		cfg.BlockNumber = new(big.Int)
	}
	if cfg.GetHashFn == nil {
		cfg.GetHashFn = func(n uint64) common.Hash {
			return common.BytesToHash(crypto.Keccak256([]byte(new(big.Int).SetUint64(n).String())))
		}
	}
	if cfg.BaseFee == nil {
		cfg.BaseFee = big.NewInt(params.InitialBaseFee)
	}
	if cfg.BlobBaseFee == nil {
		cfg.BlobBaseFee = big.NewInt(params.BlobTxMinBlobGasprice)
	}
	// Merge indicators
	if t := cfg.ChainConfig.ShanghaiTime; cfg.ChainConfig.TerminalTotalDifficultyPassed || (t != nil && *t == 0) {
		cfg.Random = &(common.Hash{})
	}

	// // set EVM tracer in case is not present
	// if cfg.EVMConfig.Tracer == nil {
	// 	logCfg := &logger.Config{
	// 		Debug: true,
	// 		// DisableStorage: true,
	// 		// DisableStack: true,
	// 		EnableReturnData: true,
	// 		EnableMemory:     true,
	// 	}
	// 	cfg.EVMConfig.Tracer = logger.NewJSONLogger(logCfg, os.Stdout)
	// }
}

type ExecutionResult struct {
	Ret          []byte
	GasUsed      uint64
	Refund       uint64
	IntrinsicGas uint64
	Record       *RecordToInitiateState
}

// Execute executes the code using the input as call data during the execution.
// It returns the EVM's return value, the new state and an error if it failed.
//
// Execute sets up an in-memory, temporary, environment for the execution of
// the given code. It makes sure that it's restored to its original state afterwards.
// In order to get an appropiate gas estimation, this should be run twice
// one for generating the access lists, take a look to Simulate from simulator package
func Execute(
	address common.Address,
	originBalance *big.Int,
	code, input []byte,
	cfg *Config,
	state *state.StateDB,
	recordToInit *ourVm.RecordToInitiateState,
) (*ExecutionResult, error) {
	if cfg == nil {
		cfg = new(Config)
	}
	SetDefaults(cfg)

	if state == nil {
		return nil, errors.New("state db missing please provide one in the config file")
	}
	var (
		vmenv  = NewEnv(cfg, state, recordToInit)
		sender = vm.AccountRef(cfg.Origin)
		rules  = cfg.ChainConfig.Rules(vmenv.Context.BlockNumber, vmenv.Context.Random != nil, vmenv.Context.Time)
	)

	if cfg.EVMConfig.Tracer != nil && cfg.EVMConfig.Tracer.OnTxStart != nil {
		cfg.EVMConfig.Tracer.OnTxStart(vmenv.GetVMContext(), types.NewTx(&types.LegacyTx{To: &address, Data: input, Value: cfg.Value, Gas: cfg.GasLimit}), cfg.Origin)
	}
	// fetch origin account
	originAcc, err := state.GetTrie().GetAccount(cfg.Origin)
	if err != nil {
		return nil, err
	}

	if originAcc == nil {
		// register origin account in case is not
		state.CreateAccount(cfg.Origin)
	}

	if originBalance.Cmp(big.NewInt(0)) > 0 {
		// get balance of origin
		balance := uint256.MustFromBig(originBalance)
		state.SetBalance(cfg.Origin, balance, tracing.BalanceChangeUnspecified)
		state.SetBalance(sender.Address(), balance, tracing.BalanceChangeUnspecified)
		vmenv.Interpreter().MarkAddressBalance(cfg.Origin)
	}

	// Execute the preparatory steps for state transition which includes:
	// - prepare accessList(post-berlin)
	// - reset transient storage(eip 1153)
	var accessList types.AccessList
	if recordToInit != nil {
		accessList = recordToInit.AccessList
	}

	state.Prepare(rules, cfg.Origin, cfg.Coinbase, &address, vm.ActivePrecompiles(rules), accessList)
	if !state.Exist(address) {
		state.CreateAccount(address)
		// set the receiver's (the executing contract) code for execution.
		state.SetCode(address, code)
		vmenv.Interpreter().MarkAddressCode(address)
	}

	// Call the code with the given configuration.
	ret, leftOverGas, err := vmenv.Call(
		sender,
		address,
		input,
		cfg.GasLimit,
		uint256.MustFromBig(cfg.Value),
	)
	if err != nil {
		return nil, err
	}

	inRecord := vmenv.Interpreter().GetRecordToInitState()
	intrinsicGas, err := core.IntrinsicGas(input, inRecord.AccessList, false, cfg.ChainConfig.IsHomestead(new(big.Int)), cfg.ChainConfig.IsIstanbul(new(big.Int)), cfg.ChainConfig.IsShanghai(new(big.Int), 0))
	if err != nil {
		return nil, err
	}

	refund := vmenv.StateDB.GetRefund()
	gasUsed := cfg.GasLimit - leftOverGas + intrinsicGas - refund

	record := &RecordToInitiateState{
		AddressCodeSet:    inRecord.AddressCodeSet,
		AddressBalanceSet: inRecord.AddressBalanceSet,
		AddressStorageSet: inRecord.AddressStorageSet,
		AccessList:        inRecord.AccessList,
	}

	return &ExecutionResult{
		Ret:          ret,
		GasUsed:      gasUsed,
		Refund:       refund,
		IntrinsicGas: intrinsicGas,
		Record:       record,
	}, nil
}

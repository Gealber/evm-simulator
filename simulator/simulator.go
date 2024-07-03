package simulator

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/Gealber/evm-simulator/rpc"
	"github.com/Gealber/evm-simulator/vm/runtime"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"

	ourVm "github.com/Gealber/evm-simulator/vm"
)

type Simulation struct {
	From        common.Address
	To          common.Address
	BlockNumber *big.Int
	GasLimit    uint64
	GasPrice    *big.Int
	Value       *big.Int
	Input       []byte
	Code        []byte
}

type Simulator struct {
	RPCClt *rpc.Client
}

type SimulationResult struct {
	ReturnedData []byte
	GasUsed      uint64
	GasLimit     uint64
	Record       *runtime.RecordToInitiateState
}

func NewSimulator(rpcClt *rpc.Client) (*Simulator, error) {
	return &Simulator{RPCClt: rpcClt}, nil
}

// Simulate perform the simulation of a transaction
// does not return a propper gas computation, for that use EstimateGas
func (s *Simulator) Simulate(simulation Simulation, stateDB *state.StateDB, recordInitializer *runtime.RecordToInitiateState) (*SimulationResult, error) {
	cfg := s.ConfigFromSimulation(simulation)

	var (
		blk     = ""
		err     error
		code    = simulation.Code
		balance = big.NewInt(0)
	)

	if simulation.BlockNumber.Cmp(big.NewInt(0)) > 0 {
		blk = "0x" + simulation.BlockNumber.Text(16)
	} else {
		// fetch latest block number
	}

	if len(code) == 0 && stateDB.GetCodeSize(simulation.To) == 0 {
		// fetch code of address
		code, err = s.RPCClt.GetCode(simulation.To.Hex(), blk)
		if err != nil {
			return nil, err
		}
	} else if len(code) == 0 {
		code = stateDB.GetCode(simulation.To)
	}

	if simulation.Value.Cmp(big.NewInt(0)) > 0 && stateDB.GetBalance(simulation.From).Cmp(common.U2560) <= 0 {
		balance, err = s.RPCClt.GetBalance(simulation.From.Hex(), blk)
		if err != nil {
			return nil, err
		}

		if balance.Cmp(simulation.Value) <= 0 {
			return nil, errors.New("insuficient balance to proceed with simulation")
		}
	}

	var recordToInit *ourVm.RecordToInitiateState
	if recordInitializer != nil {
		recordToInit = &ourVm.RecordToInitiateState{
			AddressCodeSet:    recordInitializer.AddressCodeSet,
			AddressBalanceSet: recordInitializer.AddressBalanceSet,
			AddressStorageSet: recordInitializer.AddressStorageSet,
			// AccessList:        recordInitializer.AccessList,
		}
	}

	// first execution to generate proper access lists
	result, err := runtime.Execute(simulation.To, balance, code, simulation.Input, cfg, stateDB, recordToInit)
	if err != nil {
		return nil, err
	}

	stateDB, err = InitIdealState(stateDB, result.Record)
	if err != nil {
		return nil, err
	}

	recordToInit = &ourVm.RecordToInitiateState{
		AddressCodeSet:    result.Record.AddressCodeSet,
		AddressBalanceSet: result.Record.AddressBalanceSet,
		AddressStorageSet: result.Record.AddressStorageSet,
		AccessList:        result.Record.AccessList,
	}

	result, err = runtime.Execute(simulation.To, balance, code, simulation.Input, cfg, stateDB, recordToInit)
	if err != nil {
		return nil, err
	}

	return &SimulationResult{
		ReturnedData: result.Ret,
		GasUsed:      result.GasUsed,
		Record:       result.Record,
	}, nil
}

func (s *Simulator) unoptimalSimulation(simulation Simulation, stateDB *state.StateDB, recordInitializer *runtime.RecordToInitiateState) (*SimulationResult, error) {
	cfg := s.ConfigFromSimulation(simulation)

	var (
		blk  = ""
		err  error
		code = simulation.Code
	)

	if simulation.BlockNumber.Cmp(big.NewInt(0)) > 0 {
		blk = "0x" + simulation.BlockNumber.Text(16)
	} else {
		// fetch latest block number
	}

	if len(code) == 0 && stateDB.GetCodeSize(simulation.To) == 0 {
		// fetch code of address
		code, err = s.RPCClt.GetCode(simulation.To.Hex(), blk)
		if err != nil {
			return nil, err
		}
	} else if len(code) == 0 {
		code = stateDB.GetCode(simulation.To)
	}

	balance := stateDB.GetBalance(simulation.From).ToBig()
	if simulation.Value.Cmp(big.NewInt(0)) > 0 && balance.Cmp(big.NewInt(0)) <= 0 {
		balance, err = s.RPCClt.GetBalance(simulation.From.Hex(), blk)
		if err != nil {
			return nil, err
		}
	}

	if balance.Cmp(simulation.Value) < 0 {
		return nil, errors.New("insuficient balance to proceed with simulation")
	}

	var recordToInit *ourVm.RecordToInitiateState
	if recordInitializer != nil {
		recordToInit = &ourVm.RecordToInitiateState{
			AddressCodeSet:    recordInitializer.AddressCodeSet,
			AddressBalanceSet: recordInitializer.AddressBalanceSet,
			AddressStorageSet: recordInitializer.AddressStorageSet,
			AccessList:        recordInitializer.AccessList,
		}
	}

	// first execution to generate proper access lists
	result, err := runtime.Execute(simulation.To, balance, code, simulation.Input, cfg, stateDB, recordToInit)
	if err != nil {
		return nil, err
	}

	return &SimulationResult{
		ReturnedData: result.Ret,
		GasUsed:      result.GasUsed,
		Record:       result.Record,
	}, nil
}

// SimulateBundle simulate a bundle of transactions using always the same state
func (s *Simulator) SimulateBundle(simulations []Simulation, stateDB *state.StateDB, recordInitializer *runtime.RecordToInitiateState) ([]*SimulationResult, error) {
	recordAccessLists := make([]types.AccessList, len(simulations))
	result := make([]*SimulationResult, len(simulations))
	for i := range simulations {
		simResult, err := s.unoptimalSimulation(simulations[i], stateDB, recordInitializer)
		if err != nil {
			return nil, err
		}

		recordAccessLists[i] = simResult.Record.AccessList
		recordInitializer = simResult.Record
		recordInitializer.AccessList = nil
	}

	// optimizing simulation gas computation
	stateDB, err := InitIdealState(stateDB, recordInitializer)
	if err != nil {
		return nil, err
	}

	for i := range simulations {
		recordInitializer.AccessList = recordAccessLists[i]
		simResult, err := s.unoptimalSimulation(simulations[i], stateDB, recordInitializer)
		if err != nil {
			return nil, err
		}

		recordInitializer = simResult.Record
		result[i] = simResult
		// commit state
		root, err := stateDB.Commit(0, false)
		if err != nil {
			return nil, fmt.Errorf("commit error: %s", err)
		}

		stateDB, err = state.New(root, stateDB.Database(), nil)
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

func runtimeCfgFromSimulation(simulation Simulation) *runtime.Config {
	cfg := &runtime.Config{
		Debug:       true,
		Origin:      simulation.From,
		BlockNumber: simulation.BlockNumber,
		GasLimit:    simulation.GasLimit,
		GasPrice:    simulation.GasPrice,
		Value:       simulation.Value,
	}
	runtime.SetDefaults(cfg)

	return cfg
}

func InitIdealState(originState *state.StateDB, record *runtime.RecordToInitiateState) (*state.StateDB, error) {
	db := state.NewDatabase(rawdb.NewMemoryDatabase())
	tmp, err := state.New(types.EmptyRootHash, db, nil)
	if err != nil {
		return nil, err
	}

	// create the accounts and set their code
	for acc := range record.AddressCodeSet {
		tmp.CreateAccount(acc)
		code := originState.GetCode(acc)
		tmp.SetCode(acc, code)
	}

	// set balances of accounts that need it
	for acc := range record.AddressBalanceSet {
		balance := originState.GetBalance(acc)
		tmp.SetBalance(acc, balance, tracing.BalanceChangeUnspecified)
	}

	// set storages of accounts that need it
	for key, value := range record.AddressStorageSet {
		split := strings.Split(key, ":")
		acc := common.HexToAddress(split[0])
		slot := common.HexToHash(split[1])

		tmp.SetState(acc, slot, value)
	}

	root, err := tmp.Commit(0, false)
	if err != nil {
		return nil, fmt.Errorf("commit error: %s", err)
	}

	return state.New(root, db, nil)
}

func (s *Simulator) ConfigFromSimulation(simulation Simulation) *runtime.Config {
	return &runtime.Config{
		Debug:       true,
		Origin:      simulation.From,
		BlockNumber: simulation.BlockNumber,
		GasLimit:    simulation.GasLimit,
		GasPrice:    simulation.GasPrice,
		Value:       simulation.Value,
		RPCEndpoint: s.RPCClt.Endpoint,
	}
}

func combineRecordInitializers(records []*runtime.RecordToInitiateState) *runtime.RecordToInitiateState {
	record := &runtime.RecordToInitiateState{
		AddressCodeSet:    make(map[common.Address]struct{}),
		AddressBalanceSet: make(map[common.Address]struct{}),
		AddressStorageSet: make(map[string]common.Hash),
	}

	for _, r := range records {
		if r != nil {
			// combine address code set
			for k, v := range r.AddressCodeSet {
				record.AddressCodeSet[k] = v
			}

			// combine address balance set
			for k, v := range r.AddressBalanceSet {
				record.AddressBalanceSet[k] = v
			}

			// combine address storage set
			for k, v := range r.AddressStorageSet {
				if _, ok := record.AddressStorageSet[k]; !ok {
					// adding only first occurrence
					record.AddressStorageSet[k] = v
				}
			}
		}
	}

	return record
}

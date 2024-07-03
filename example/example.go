package main

import (
	"log"
	"math/big"

	"github.com/Gealber/evm-simulator/rpc"
	"github.com/Gealber/evm-simulator/simulator"
	"github.com/Gealber/evm-simulator/vm"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
)

func main() {
	exampleSimulateBundle()
}

func simpleSim() {
	code := []byte{
		byte(vm.PUSH0), byte(vm.CALLDATALOAD),
		byte(vm.PUSH0), byte(vm.SSTORE),
		byte(vm.PUSH0), byte(vm.SLOAD),
		byte(vm.PUSH0), byte(vm.MSTORE),
		byte(vm.PUSH1), byte(0x20), byte(vm.PUSH0), byte(vm.RETURN),
	}

	rpcEndpoint := "https://eth.llamarpc.com"
	blkNumber := big.NewInt(1)

	rpcClt := rpc.NewClient(rpcEndpoint)
	sim, err := simulator.NewSimulator(rpcClt)
	if err != nil {
		log.Fatal(err)
	}

	gasPrice := big.NewInt(0)
	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000011")

	simulation := simulator.Simulation{
		From:        common.HexToAddress("0x0000000000000000000000000000000000000000"),
		To:          contractAddr,
		Code:        code,
		BlockNumber: blkNumber,
		GasLimit:    300000,
		GasPrice:    gasPrice,
		Value:       big.NewInt(0),
		Input:       hexutil.MustDecode(`0x0000000000000000000000000000000000000000000000000000000000000020`),
	}

	stateDB, err := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		log.Fatal(err)
	}

	result, err := sim.Simulate(simulation, stateDB, nil)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("-----------------------------------------------------------")
	// just log the returned value for now
	log.Println(hexutil.Encode(result.ReturnedData))
	log.Println(result.GasUsed)
}

func exampleSimulateBundle() {
	rpcEndpoint := "https://eth.llamarpc.com"
	blkNumber := big.NewInt(20219603)

	rpcClt := rpc.NewClient(rpcEndpoint)
	sim, err := simulator.NewSimulator(rpcClt)
	if err != nil {
		log.Fatal(err)
	}

	gasPrice := big.NewInt(0)

	simulations := []simulator.Simulation{
		{
			From:        common.HexToAddress(""),
			To:          common.HexToAddress(""),
			BlockNumber: blkNumber,
			GasLimit:    300000,
			GasPrice:    gasPrice,
			Value:       big.NewInt(196834),
			Input:       hexutil.MustDecode(``),
		},
		{
			From:        common.HexToAddress(""),
			To:          common.HexToAddress(""),
			BlockNumber: blkNumber,
			GasLimit:    300000,
			GasPrice:    gasPrice,
			Value:       big.NewInt(0),
			Input:       hexutil.MustDecode(``),
		},
		{
			From:        common.HexToAddress(""),
			To:          common.HexToAddress(""),
			BlockNumber: blkNumber,
			GasLimit:    300000,
			GasPrice:    gasPrice,
			Value:       big.NewInt(197057),
			Input:       hexutil.MustDecode(``),
		},
	}

	stateDB, err := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		log.Fatal(err)
	}

	result, err := sim.SimulateBundle(simulations, stateDB, nil)
	if err != nil {
		log.Fatal(err)
	}

	for _, r := range result {
		log.Println("-----------------------------------------------------------")
		// just log the returned value for now
		log.Println(hexutil.Encode(r.ReturnedData))
		log.Println(r.GasUsed)

		for _, l := range r.Record.AccessList {
			log.Println("ADDRESS: ", l.Address.Hex())
			for _, st := range l.StorageKeys {
				log.Println(st.Hex())
			}
		}
	}
}

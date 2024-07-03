package simulator

import (
	"log"
	"math/big"
	"testing"

	"github.com/Gealber/evm-simulator/rpc"
	"github.com/Gealber/evm-simulator/vm"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestSimulate(t *testing.T) {
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
	sim, err := NewSimulator(rpcClt)
	if err != nil {
		log.Fatal(err)
	}

	gasPrice := big.NewInt(0)
	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000011")

	simulation := Simulation{
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
		t.Fatal(err)
	}

	log.Println("-----------------------------------------------------------")
	// just log the returned value for now
	log.Println(hexutil.Encode(result.ReturnedData))
	log.Println(result.GasUsed)

	for _, l := range result.Record.AccessList {
		log.Println("ADDRESS: ", l.Address.Hex())
		for _, st := range l.StorageKeys {
			log.Println(st.Hex())
		}
	}

	codeLen := stateDB.GetCodeSize(contractAddr)
	if codeLen == 0 {
		t.Fatal("code of contract is zero")
	}

	// check state value
	val := new(big.Int).SetBytes(result.ReturnedData)
	if val.Cmp(big.NewInt(32)) != 0 {
		t.Fatalf("value: %s i: %d", val, 32)
	}
}

func TestSimulateBundle(t *testing.T) {
	code := []byte{
		byte(vm.PUSH0), byte(vm.CALLDATALOAD),
		byte(vm.PUSH0), byte(vm.SLOAD),
		byte(vm.ADD),
		byte(vm.PUSH0), byte(vm.SSTORE),
		byte(vm.PUSH0), byte(vm.SLOAD),
		byte(vm.PUSH0), byte(vm.MSTORE),
		byte(vm.PUSH1), byte(0x20), byte(vm.PUSH0), byte(vm.RETURN),
	}

	rpcEndpoint := "https://eth.llamarpc.com"
	blkNumber := big.NewInt(1)

	rpcClt := rpc.NewClient(rpcEndpoint)
	sim, err := NewSimulator(rpcClt)
	if err != nil {
		log.Fatal(err)
	}

	gasPrice := big.NewInt(0)
	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000011")

	simulations := []Simulation{
		{
			From:        common.HexToAddress("0x0000000000000000000000000000000000000000"),
			To:          contractAddr,
			Code:        code,
			BlockNumber: blkNumber,
			GasLimit:    300000,
			GasPrice:    gasPrice,
			Value:       big.NewInt(0),
			Input:       hexutil.MustDecode(`0x0000000000000000000000000000000000000000000000000000000000000001`),
		},
		{
			From:        common.HexToAddress("0x0000000000000000000000000000000000000000"),
			To:          contractAddr,
			Code:        code,
			BlockNumber: blkNumber,
			GasLimit:    300000,
			GasPrice:    gasPrice,
			Value:       big.NewInt(0),
			Input:       hexutil.MustDecode(`0x0000000000000000000000000000000000000000000000000000000000000002`),
		},
		{
			From:        common.HexToAddress("0x0000000000000000000000000000000000000000"),
			To:          contractAddr,
			Code:        code,
			BlockNumber: blkNumber,
			GasLimit:    300000,
			GasPrice:    gasPrice,
			Value:       big.NewInt(0),
			Input:       hexutil.MustDecode(`0x0000000000000000000000000000000000000000000000000000000000000003`),
		},
	}

	stateDB, err := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		log.Fatal(err)
	}
	result, err := sim.SimulateBundle(simulations, stateDB, nil)
	if err != nil {
		t.Fatal(err)
	}

	for i, r := range result {
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

		// check state value
		val := new(big.Int).SetBytes(r.ReturnedData)
		switch i {
		case 0:
			if val.Cmp(big.NewInt(int64(1))) != 0 {
				t.Fatalf("value: %s i: %d", val, i)
			}
		case 1:
			if val.Cmp(big.NewInt(int64(3))) != 0 {
				t.Fatalf("value: %s i: %d", val, i)
			}
		case 2:
			if val.Cmp(big.NewInt(int64(6))) != 0 {
				t.Fatalf("value: %s i: %d", val, i)
			}
		}
	}
}

// Copyright 2014 The go-ethereum Authors
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

package vm

import (
	"errors"
	"fmt"

	"github.com/Gealber/evm-simulator/rpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/holiman/uint256"
)

// ScopeContext contains the things that are per-call, such as stack and memory,
// but not transients like pc and gas
type ScopeContext struct {
	Memory   *Memory
	Stack    *Stack
	Contract *Contract
}

// MemoryData returns the underlying memory slice. Callers must not modify the contents
// of the returned data.
func (ctx *ScopeContext) MemoryData() []byte {
	if ctx.Memory == nil {
		return nil
	}
	return ctx.Memory.Data()
}

// StackData returns the stack data. Callers must not modify the contents
// of the returned data.
func (ctx *ScopeContext) StackData() []uint256.Int {
	if ctx.Stack == nil {
		return nil
	}
	return ctx.Stack.Data()
}

// Caller returns the current caller.
func (ctx *ScopeContext) Caller() common.Address {
	return ctx.Contract.Caller()
}

// Address returns the address where this scope of execution is taking place.
func (ctx *ScopeContext) Address() common.Address {
	return ctx.Contract.Address()
}

// CallValue returns the value supplied with this call.
func (ctx *ScopeContext) CallValue() *uint256.Int {
	return ctx.Contract.Value()
}

// CallInput returns the input/calldata with this call. Callers must not modify
// the contents of the returned data.
func (ctx *ScopeContext) CallInput() []byte {
	return ctx.Contract.Input
}

// EVMInterpreter represents an EVM interpreter
type EVMInterpreter struct {
	rpcClt *rpc.Client
	evm    *EVM
	table  *JumpTable

	hasher    crypto.KeccakState // Keccak256 hasher instance shared across opcodes
	hasherBuf common.Hash        // Keccak256 hasher result array shared across opcodes

	readOnly   bool   // Whether to throw on stateful modifications
	returnData []byte // Last CALL's return data for subsequent reuse

	// map to track when a address code was set, to avoid fetching again from fork
	// TODO: this is ugly think in to refactor
	addressCodeSet    map[common.Address]struct{}
	addressBalanceSet map[common.Address]struct{}
	// key should be address:key
	addressStorageSet        map[string]common.Hash
	addressSlotAccessListSet map[string]struct{}
	// access list
	accessList types.AccessList
}

type RecordToInitiateState struct {
	// map to track when a address code was set, to avoid fetching again from fork
	AddressCodeSet    map[common.Address]struct{}
	AddressBalanceSet map[common.Address]struct{}
	// key should be address:key
	AddressStorageSet map[string]common.Hash
	// access list
	AccessList types.AccessList
}

// NewEVMInterpreter returns a new instance of the Interpreter.
func NewEVMInterpreter(evm *EVM, record *RecordToInitiateState, rpcEndpoint string) *EVMInterpreter {
	rpcClt := rpc.NewClient(rpcEndpoint)
	// If jump table was not initialised we set the default one.
	var table *JumpTable
	switch {
	case evm.chainRules.IsVerkle:
		// TODO replace with proper instruction set when fork is specified
		table = &verkleInstructionSet
	case evm.chainRules.IsCancun:
		table = &cancunInstructionSet
	case evm.chainRules.IsShanghai:
		table = &shanghaiInstructionSet
	case evm.chainRules.IsMerge:
		table = &mergeInstructionSet
	case evm.chainRules.IsLondon:
		table = &londonInstructionSet
	case evm.chainRules.IsBerlin:
		table = &berlinInstructionSet
	case evm.chainRules.IsIstanbul:
		table = &istanbulInstructionSet
	case evm.chainRules.IsConstantinople:
		table = &constantinopleInstructionSet
	case evm.chainRules.IsByzantium:
		table = &byzantiumInstructionSet
	case evm.chainRules.IsEIP158:
		table = &spuriousDragonInstructionSet
	case evm.chainRules.IsEIP150:
		table = &tangerineWhistleInstructionSet
	case evm.chainRules.IsHomestead:
		table = &homesteadInstructionSet
	default:
		table = &frontierInstructionSet
	}
	var extraEips []int
	if len(evm.Config.ExtraEips) > 0 {
		// Deep-copy jumptable to prevent modification of opcodes in other tables
		table = copyJumpTable(table)
	}
	for _, eip := range evm.Config.ExtraEips {
		if err := EnableEIP(eip, table); err != nil {
			// Disable it, so caller can check if it's activated or not
			log.Error("EIP activation failed", "eip", eip, "error", err)
		} else {
			extraEips = append(extraEips, eip)
		}
	}
	evm.Config.ExtraEips = extraEips
	interpreter := &EVMInterpreter{
		rpcClt: rpcClt,
		evm:    evm,
		table:  table,
	}

	if record != nil {
		interpreter.addressCodeSet = record.AddressCodeSet
		interpreter.addressBalanceSet = record.AddressBalanceSet
		interpreter.addressStorageSet = record.AddressStorageSet
	} else {
		interpreter.addressCodeSet = make(map[common.Address]struct{})
		interpreter.addressBalanceSet = make(map[common.Address]struct{})
		interpreter.addressStorageSet = make(map[string]common.Hash)
	}

	interpreter.addressSlotAccessListSet = make(map[string]struct{})

	return interpreter
}

func (in *EVMInterpreter) MarkAddressCode(addr common.Address) {
	in.addressCodeSet[addr] = struct{}{}
}

func (in *EVMInterpreter) MarkAddressBalance(addr common.Address) {
	in.addressBalanceSet[addr] = struct{}{}
}

func (in *EVMInterpreter) AccessList() types.AccessList {
	return in.accessList
}

func (in *EVMInterpreter) GetRecordToInitState() *RecordToInitiateState {
	return &RecordToInitiateState{
		AddressCodeSet:    in.addressCodeSet,
		AddressBalanceSet: in.addressBalanceSet,
		AddressStorageSet: in.addressStorageSet,
		AccessList:        in.accessList,
	}
}

// Run loops and evaluates the contract's code with the given input data and returns
// the return byte-slice and an error if one occurred.
//
// It's important to note that any errors returned by the interpreter should be
// considered a revert-and-consume-all-gas operation except for
// ErrExecutionReverted which means revert-and-keep-gas-left.
func (in *EVMInterpreter) Run(contract *Contract, input []byte, readOnly bool) (ret []byte, err error) {
	// Increment the call depth which is restricted to 1024
	in.evm.depth++
	defer func() { in.evm.depth-- }()

	// Make sure the readOnly is only set if we aren't in readOnly yet.
	// This also makes sure that the readOnly flag isn't removed for child calls.
	if readOnly && !in.readOnly {
		in.readOnly = true
		defer func() { in.readOnly = false }()
	}

	// Reset the previous call's return data. It's unimportant to preserve the old buffer
	// as every returning call will return new data anyway.
	in.returnData = nil

	// Don't bother with the execution if there's no code.
	if len(contract.Code) == 0 {
		return nil, nil
	}

	var (
		op          OpCode        // current opcode
		mem         = NewMemory() // bound memory
		stack       = newstack()  // local stack
		callContext = &ScopeContext{
			Memory:   mem,
			Stack:    stack,
			Contract: contract,
		}
		// For optimisation reason we're using uint64 as the program counter.
		// It's theoretically possible to go above 2^64. The YP defines the PC
		// to be uint256. Practically much less so feasible.
		pc   = uint64(0) // program counter
		cost uint64
		// copies used by tracer
		pcCopy  uint64 // needed for the deferred EVMLogger
		gasCopy uint64 // for EVMLogger to log gas remaining before execution
		logged  bool   // deferred EVMLogger should ignore already logged steps
		res     []byte // result of the opcode execution function
		debug   = in.evm.Config.Tracer != nil
	)
	// Don't move this deferred function, it's placed before the OnOpcode-deferred method,
	// so that it gets executed _after_: the OnOpcode needs the stacks before
	// they are returned to the pools
	defer func() {
		returnStack(stack)
	}()
	contract.Input = input

	if debug {
		defer func() { // this deferred method handles exit-with-error
			if err == nil {
				return
			}
			if !logged && in.evm.Config.Tracer.OnOpcode != nil {
				in.evm.Config.Tracer.OnOpcode(pcCopy, byte(op), gasCopy, cost, callContext, in.returnData, in.evm.depth, vm.VMErrorFromErr(err))
			}
			if logged && in.evm.Config.Tracer.OnFault != nil {
				in.evm.Config.Tracer.OnFault(pcCopy, byte(op), gasCopy, cost, callContext, in.evm.depth, vm.VMErrorFromErr(err))
			}
		}()
	}
	// The Interpreter main run loop (contextual). This loop runs until either an
	// explicit STOP, RETURN or SELFDESTRUCT is executed, an error occurred during
	// the execution of one of the operations or until the done flag is set by the
	// parent context.
	for {
		if debug {
			// Capture pre-execution values for tracing.
			logged, pcCopy, gasCopy = false, pc, contract.Gas
		}

		if in.evm.chainRules.IsEIP4762 && !contract.IsDeployment {
			// if the PC ends up in a new "chunk" of verkleized code, charge the
			// associated costs.
			contractAddr := contract.Address()
			contract.Gas -= in.evm.TxContext.AccessEvents.CodeChunksRangeGas(contractAddr, pc, 1, uint64(len(contract.Code)), false)
		}

		// Get the operation from the jump table and validate the stack to ensure there are
		// enough stack items available to perform the operation.
		op = contract.GetOp(pc)

		switch {
		case readStorage(op):
			// register address code if needed
			err = in.registerAddressStorage(op, callContext, "0x"+in.evm.Context.BlockNumber.Text(16))
			if err != nil {
				return nil, err
			}
		case isCall(op):
			err = in.registerAddressCodeForCalls(op, callContext, "0x"+in.evm.Context.BlockNumber.Text(16))
			if err != nil {
				return nil, err
			}
		case isExtCode(op):
			err = in.registerAddressCodeForExt(op, callContext, "0x"+in.evm.Context.BlockNumber.Text(16))
			if err != nil {
				return nil, err
			}
		}

		if interactWithStorage(op) {
			in.appendToAccessList(op, callContext)
		}

		operation := in.table[op]
		cost = operation.constantGas // For tracing
		// Validate stack
		if sLen := stack.len(); sLen < operation.minStack {
			return nil, &ErrStackUnderflow{stackLen: sLen, required: operation.minStack}
		} else if sLen > operation.maxStack {
			return nil, &ErrStackOverflow{stackLen: sLen, limit: operation.maxStack}
		}
		if !contract.UseGas(cost, in.evm.Config.Tracer, tracing.GasChangeIgnored) {
			return nil, vm.ErrOutOfGas
		}

		if operation.dynamicGas != nil {
			// All ops with a dynamic memory usage also has a dynamic gas cost.
			var memorySize uint64
			// calculate the new memory size and expand the memory to fit
			// the operation
			// Memory check needs to be done prior to evaluating the dynamic gas portion,
			// to detect calculation overflows
			if operation.memorySize != nil {
				memSize, overflow := operation.memorySize(stack)
				if overflow {
					return nil, vm.ErrGasUintOverflow
				}
				// memory is expanded in words of 32 bytes. Gas
				// is also calculated in words.
				if memorySize, overflow = math.SafeMul(toWordSize(memSize), 32); overflow {
					return nil, vm.ErrGasUintOverflow
				}
			}
			// Consume the gas and return an error if not enough gas is available.
			// cost is explicitly set so that the capture state defer method can get the proper cost
			var dynamicCost uint64
			dynamicCost, err = operation.dynamicGas(in.evm, contract, stack, mem, memorySize)
			cost += dynamicCost // for tracing
			if err != nil {
				return nil, fmt.Errorf("%w: %v", vm.ErrOutOfGas, err)
			}
			if !contract.UseGas(dynamicCost, in.evm.Config.Tracer, tracing.GasChangeIgnored) {
				return nil, vm.ErrOutOfGas
			}

			// Do tracing before memory expansion
			if debug {
				if in.evm.Config.Tracer.OnGasChange != nil {
					in.evm.Config.Tracer.OnGasChange(gasCopy, gasCopy-cost, tracing.GasChangeCallOpCode)
				}
				if in.evm.Config.Tracer.OnOpcode != nil {
					in.evm.Config.Tracer.OnOpcode(pc, byte(op), gasCopy, cost, callContext, in.returnData, in.evm.depth, vm.VMErrorFromErr(err))
					logged = true
				}
			}
			if memorySize > 0 {
				mem.Resize(memorySize)
			}
		} else if debug {
			if in.evm.Config.Tracer.OnGasChange != nil {
				in.evm.Config.Tracer.OnGasChange(gasCopy, gasCopy-cost, tracing.GasChangeCallOpCode)
			}
			if in.evm.Config.Tracer.OnOpcode != nil {
				in.evm.Config.Tracer.OnOpcode(pc, byte(op), gasCopy, cost, callContext, in.returnData, in.evm.depth, vm.VMErrorFromErr(err))
				logged = true
			}
		}

		// execute the operation
		res, err = operation.execute(&pc, in, callContext)
		if err != nil {
			break
		}
		pc++
	}

	if err == errStopToken {
		err = nil // clear stop token error
	}

	return res, err
}

func readStorage(op OpCode) bool {
	return op == SLOAD
}

func interactWithStorage(op OpCode) bool {
	return op == SLOAD || op == SSTORE
}

func isCall(op OpCode) bool {
	return op == CALL || op == CALLCODE || op == DELEGATECALL || op == STATICCALL
}

func isExtCode(op OpCode) bool {
	return op == EXTCODECOPY || op == EXTCODEHASH || op == EXTCODESIZE
}

// registerAddressCodeForCalls in case the opcode will be
// CALL, CALLCODE, DELEGATECALL, or STATICCALL
// we will try to fetch the address code
// that will be requested this opcodes on the current blocknumber
// and register that code in the evm state.
// TODO: use cache to avoid double requesting Http
func (in *EVMInterpreter) registerAddressCodeForCalls(op OpCode, scope *ScopeContext, blk string) error {
	if len(scope.StackData()) < 3 {
		return errors.New("insufficient elements in stack")
	}

	// copy data in stack
	stackTmp := make([]uint256.Int, len(scope.StackData()))
	copy(stackTmp, scope.StackData())
	// extracting element in position len(stackTmp) - 2, which is the address to which our contract
	// will interact, the element 0 is not needed
	addr := common.Address(stackTmp[len(stackTmp)-2].Bytes20())

	// if the address code was set once, there's no need to refetch it
	if _, ok := in.addressCodeSet[addr]; ok {
		return nil
	}

	// fetch code and storage of address, and register in evm state
	// retrieving the latest
	code, err := in.rpcClt.GetCode(addr.Hex(), blk)
	if err != nil {
		return err
	}

	// check if address exists in state
	if !in.evm.StateDB.Exist(addr) {
		// create address
		in.evm.StateDB.CreateAccount(addr)
	}

	in.evm.StateDB.SetCode(addr, code)
	in.addressCodeSet[addr] = struct{}{}

	// set balance in case we will need it
	if op == CALL || op == CALLCODE {
		value := stackTmp[len(stackTmp)-3]
		// currentBalance of account
		currrentStateBalance := in.evm.StateDB.GetBalance(addr)
		_, balanceSetOnce := in.addressBalanceSet[addr]
		if value.Cmp(currrentStateBalance) > 0 && !balanceSetOnce {
			// current balance in account
			balanceBig, err := in.rpcClt.GetBalance(addr.Hex(), blk)
			if err != nil {
				return err
			}
			// wanted balance fetched from rpc
			balance := uint256.MustFromBig(balanceBig)

			if balance.Cmp(&value) >= 0 {
				diff := new(uint256.Int).Sub(balance, currrentStateBalance)
				// add the remaining balance, between wanted and current
				in.evm.StateDB.AddBalance(addr, diff, tracing.BalanceChangeUnspecified)
				in.addressBalanceSet[addr] = struct{}{}
			}
		}
	}

	return nil
}

// registerAddressStorage in case the opcode will be
//
// we will try to fetch the address storage
// that will be requested this opcodes on the current blocknumber
// and register that storage in the evm state.
// TODO: use cache to avoid double requesting Http
func (in *EVMInterpreter) registerAddressStorage(op OpCode, scope *ScopeContext, blk string) error {
	if len(scope.StackData()) < 1 {
		return errors.New("insufficient elements in stack")
	}

	// copy data in stack
	loc := scope.Stack.peek()
	hash := common.Hash(loc.Bytes32())

	// if the address storage was set once, there's no need to refetch it
	key := scope.Address().Hex() + ":" + hash.Hex()
	if _, ok := in.addressStorageSet[key]; ok {
		return nil
	}

	// retrieve storage of value in contract in position hash
	storage, err := in.rpcClt.GetStorageAt(scope.Address().Hex(), hash.Hex(), blk)
	if err != nil {
		return err
	}

	in.evm.StateDB.SetState(scope.Address(), hash, storage)
	in.addressStorageSet[key] = storage

	return nil
}

// registerAddressCodeForExt in case the opcode will be
//
//	op == EXTCODECOPY || op == EXTCODEHASH || op == EXTCODESIZE
//
// we will try to fetch the address code
// that will be requested this opcodes on the current blocknumber
// and register that code in the evm state.
// TODO: use cache to avoid double requesting Http
func (in *EVMInterpreter) registerAddressCodeForExt(op OpCode, scope *ScopeContext, blk string) error {
	if len(scope.StackData()) < 1 {
		return errors.New("insufficient elements in stack")
	}

	// copy data in stack
	stackTmp := make([]uint256.Int, len(scope.StackData()))
	copy(stackTmp, scope.StackData())
	// extracting element in position len(stackTmp) - 1, which is the address to which our contract
	// will interact, the element 0 is not needed
	addr := common.Address(stackTmp[len(stackTmp)-1].Bytes20())

	// if the address code was set once, there's no need to refetch it
	if _, ok := in.addressCodeSet[addr]; ok {
		return nil
	}

	// fetch code and storage of address, and register in evm state
	// retrieving the latest
	code, err := in.rpcClt.GetCode(addr.Hex(), blk)
	if err != nil {
		return err
	}

	// check if address exists in state
	if !in.evm.StateDB.Exist(addr) {
		// create address
		in.evm.StateDB.CreateAccount(addr)
	}

	in.evm.StateDB.SetCode(addr, code)
	in.addressCodeSet[addr] = struct{}{}

	return nil
}

// appendToAccessList will fetch the slots in storage involved in SLOAD or SSTORE op
// and append it to the access list without duplicating addresses
func (in *EVMInterpreter) appendToAccessList(op OpCode, scope *ScopeContext) {
	// copy data in stack
	loc := scope.Stack.peek()
	slot := common.Hash(loc.Bytes32())
	key := scope.Address().Hex() + ":" + slot.Hex()

	if _, ok := in.addressSlotAccessListSet[key]; ok {
		return
	}

	addressInitialized := false
	for i, l := range in.accessList {
		if l.Address.Cmp(scope.Address()) == 0 {
			addressInitialized = true
			in.accessList[i].StorageKeys = append(in.accessList[i].StorageKeys, slot)
			break
		}
	}

	if !addressInitialized {
		in.accessList = append(in.accessList, types.AccessTuple{
			Address: scope.Address(),
			StorageKeys: []common.Hash{
				slot,
			},
		})
	}

	in.addressSlotAccessListSet[key] = struct{}{}
}

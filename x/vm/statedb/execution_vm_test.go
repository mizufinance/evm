package statedb_test

import (
	"context"
	storetypes "cosmossdk.io/store/types"
	"math/big"
	"reflect"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

type effectKey struct{}
type effects struct {
	parent *effects
	value  int
	closed bool
}

func (s *effects) PrepareAdopt() error { s.parent.value += s.value; s.closed = true; return nil }
func (s *effects) Adopt()              {}
func (s *effects) Abort()              { s.closed = true }

// Run actual CALL/REVERT bytecode, including a caught child revert followed by
// a successful sibling. External effects must follow the EVM frame outcome.
func TestEVMExecutionScopesCaughtAndAncestorReverts(t *testing.T) {
	for _, revertParent := range []bool{false, true} {
		t.Run(map[bool]string{false: "caught_child", true: "ancestor_revert"}[revertParent], func(t *testing.T) {
			root := &effects{}
			var frames []*effects
			ctx := sdk.Context{}.WithContext(context.Background()).WithEventManager(sdk.NewEventManager()).WithValue(effectKey{}, root)
			ctx = types.WithExecutionScopeHook(ctx, func(parent sdk.Context) (sdk.Context, types.ExecutionScope, error) {
				scope := &effects{parent: parent.Value(effectKey{}).(*effects), value: 1}
				frames = append(frames, scope)
				return parent.WithValue(effectKey{}, scope), scope, nil
			})
			db := statedb.New(ctx, NewMockKeeper(), statedb.NewEmptyTxConfig())
			parent := common.HexToAddress("0x100")
			failed := common.HexToAddress("0x101")
			success := common.HexToAddress("0x102")
			// CALL(gas=65535,to=address,value=0,in=empty,out=empty); POP result.
			call := func(address common.Address) []byte {
				code := []byte{0x60, 0, 0x60, 0, 0x60, 0, 0x60, 0, 0x60, 0, 0x73}
				code = append(code, address.Bytes()...)
				return append(code, 0x61, 0xff, 0xff, 0xf1, 0x50)
			}
			code := append(call(failed), call(success)...)
			if revertParent {
				code = append(code, 0x60, 0, 0x60, 0, 0xfd)
			} else {
				code = append(code, 0x00)
			}
			for address, code := range map[common.Address][]byte{parent: code, failed: {0x60, 0, 0x60, 0, 0xfd}, success: {0x00}} {
				db.CreateAccount(address)
				db.SetCode(address, code)
			}
			evm := vm.NewEVM(vm.BlockContext{CanTransfer: core.CanTransfer, Transfer: core.Transfer, GetHash: func(uint64) common.Hash { return common.Hash{} }, BlockNumber: big.NewInt(1), GasLimit: 1000000, Time: 1, Difficulty: big.NewInt(0), BaseFee: big.NewInt(0)}, db, params.AllEthashProtocolChanges, vm.Config{Tracer: db.TraceExecutionFrames(nil)})
			_, _, err := evm.Call(common.HexToAddress("0x200"), parent, nil, 500000, uint256.NewInt(0))
			if revertParent && err != vm.ErrExecutionReverted {
				t.Fatalf("expected parent revert: %v", err)
			}
			if !revertParent && err != nil {
				t.Fatal(err)
			}
			expected := 2
			if revertParent {
				expected = 0
			}
			if root.value != expected {
				t.Fatalf("external effects=%d, want %d", root.value, expected)
			}
			if len(frames) != 3 {
				t.Fatalf("expected parent and two children, got %d", len(frames))
			}
			for _, frame := range frames {
				if !frame.closed {
					t.Fatal("open execution frame")
				}
			}
		})
	}
}

type panickingPrecompile struct{ cause any }

func (p panickingPrecompile) RequiredGas([]byte) uint64                       { return 0 }
func (p panickingPrecompile) Address() common.Address                         { return common.HexToAddress("0x100") }
func (p panickingPrecompile) Run(*vm.EVM, *vm.Contract, bool) ([]byte, error) { panic(p.cause) }

func TestEVMPanicDiscardsEffectsAndPreservesOutOfGas(t *testing.T) {
	for _, cause := range []any{"precompile failure", storetypes.ErrorOutOfGas{Descriptor: "free gas limit"}} {
		root := &effects{}
		ctx := sdk.Context{}.WithContext(context.Background()).WithEventManager(sdk.NewEventManager()).WithValue(effectKey{}, root)
		ctx = types.WithExecutionScopeHook(ctx, func(parent sdk.Context) (sdk.Context, types.ExecutionScope, error) {
			scope := &effects{parent: parent.Value(effectKey{}).(*effects), value: 1}
			return parent.WithValue(effectKey{}, scope), scope, nil
		})
		db := statedb.New(ctx, NewMockKeeper(), statedb.NewEmptyTxConfig())
		address := common.HexToAddress("0x100")
		evm := vm.NewEVM(vm.BlockContext{CanTransfer: core.CanTransfer, Transfer: core.Transfer, GetHash: func(uint64) common.Hash { return common.Hash{} }, BlockNumber: big.NewInt(1), GasLimit: 1000000, Time: 1, Difficulty: big.NewInt(0), BaseFee: big.NewInt(0)}, db, params.AllEthashProtocolChanges, vm.Config{Tracer: db.TraceExecutionFrames(nil)})
		evm.WithPrecompiles(vm.PrecompiledContracts{address: panickingPrecompile{cause}})
		got := func() (caught any) {
			defer func() { caught = recover() }()
			func() {
				defer db.ExecutionPanicRecovery()()
				// Model the enclosing host cache: successful child closes are provisional.
				db.BeginExecutionFrame()
				_, _, _ = evm.Call(common.HexToAddress("0x200"), address, nil, 500000, uint256.NewInt(0))
			}()
			return nil
		}()
		if !reflect.DeepEqual(got, cause) || root.value != 0 {
			t.Fatalf("panic=%v, committed effects=%d", got, root.value)
		}
	}
}

package statedb

import (
	"errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"math/big"
)

type executionFrame struct {
	context sdk.Context
	scope   types.ExecutionScope
}

func (s *StateDB) BeginExecutionFrame() {
	ctx := s.ctx
	if n := len(s.executionFrames); n > 0 {
		ctx = s.executionFrames[n-1].context
	}
	child, scope, err := types.BeginExecutionScope(ctx)
	if err != nil {
		panic(types.ExecutionScopePanic{Cause: err})
	}
	s.executionFrames = append(s.executionFrames, executionFrame{context: child, scope: scope})
}

func (s *StateDB) EndExecutionFrame(reverted bool) {
	n := len(s.executionFrames)
	if n == 0 {
		panic(types.ExecutionScopePanic{Cause: errors.New("EVM frame scope underflow")})
	}
	frame := s.executionFrames[n-1]
	if frame.scope != nil {
		if reverted {
			frame.scope.Abort()
		} else {
			types.AdoptExecutionScope(frame.scope, func() {})
		}
	}
	s.executionFrames = s.executionFrames[:n-1]
}

// TraceExecutionFrames composes mandatory scope hooks with optional tracing.
func (s *StateDB) TraceExecutionFrames(tracer *tracing.Hooks) *tracing.Hooks {
	hooks := new(tracing.Hooks)
	if tracer != nil {
		*hooks = *tracer
	}
	enter, exit := hooks.OnEnter, hooks.OnExit
	hooks.OnEnter = func(depth int, typ byte, from, to common.Address, input []byte, gas uint64, value *big.Int) {
		s.BeginExecutionFrame()
		if enter != nil {
			enter(depth, typ, from, to, input, gas, value)
		}
	}
	hooks.OnExit = func(depth int, output []byte, gas uint64, err error, reverted bool) {
		if exit != nil {
			exit(depth, output, gas, err, reverted)
		}
		s.EndExecutionFrame(reverted || err != nil)
	}
	return hooks
}

// ExecutionPanicRecovery captures the frames owned by this ApplyMessage call.
// Ordinary panics retain their type after cleanup; nested calls leave their
// caller's frames intact. Uncertain adoption failures must not be retried.
func (s *StateDB) ExecutionPanicRecovery() func() {
	depth := len(s.executionFrames)
	return func() {
		cause := recover()
		if cause == nil {
			return
		}
		if _, fatal := cause.(interface{ FatalExecution() }); fatal {
			panic(cause)
		}
		func() {
			defer func() {
				if failure := recover(); failure != nil {
					panic(types.ExecutionScopePanic{Cause: failure})
				}
			}()
			for len(s.executionFrames) > depth {
				s.EndExecutionFrame(true)
			}
		}()
		panic(cause)
	}
}

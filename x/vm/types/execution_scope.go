package types

import sdk "github.com/cosmos/cosmos-sdk/types"

// ExecutionScope couples external effects to an EVM frame or host cache.
// PrepareAdopt must complete the fallible close before the SDK cache is written.
type ExecutionScope interface {
	PrepareAdopt() error
	Adopt()
	Abort()
}

type ExecutionScopeHook func(sdk.Context) (sdk.Context, ExecutionScope, error)
type executionScopeKey struct{}

func WithExecutionScopeHook(ctx sdk.Context, hook ExecutionScopeHook) sdk.Context {
	return ctx.WithValue(executionScopeKey{}, hook)
}
func HasExecutionScopeHook(ctx sdk.Context) bool {
	_, ok := ctx.Value(executionScopeKey{}).(ExecutionScopeHook)
	return ok
}
func BeginExecutionScope(ctx sdk.Context) (sdk.Context, ExecutionScope, error) {
	hook, ok := ctx.Value(executionScopeKey{}).(ExecutionScopeHook)
	if !ok {
		return ctx, nil, nil
	}
	return hook(ctx)
}

// ExecutionScopePanic must escape transaction recovery. A failed external close
// or panic during cache adoption leaves no deterministic transaction outcome.
type ExecutionScopePanic struct{ Cause any }

func (ExecutionScopePanic) FatalExecution() {}

func AdoptExecutionScope(scope ExecutionScope, write func()) {
	defer func() {
		if cause := recover(); cause != nil {
			panic(ExecutionScopePanic{Cause: cause})
		}
	}()
	if scope != nil {
		if err := scope.PrepareAdopt(); err != nil {
			panic(ExecutionScopePanic{Cause: err})
		}
	}
	write()
	if scope != nil {
		scope.Adopt()
	}
}

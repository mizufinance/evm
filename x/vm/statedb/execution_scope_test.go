package statedb

import (
	"context"
	storetypes "cosmossdk.io/store/types"
	"reflect"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/types"
)

type frameKey struct{}
type testFrame struct {
	id     int
	closed bool
	events *[]int
}

func (f *testFrame) PrepareAdopt() error {
	f.closed = true
	*f.events = append(*f.events, f.id)
	return nil
}
func (f *testFrame) Adopt() {}
func (f *testFrame) Abort() {
	if !f.closed {
		f.closed = true
		*f.events = append(*f.events, -f.id)
	}
}

func TestExecutionFramesRestoreParentContextAndCloseImmediately(t *testing.T) {
	var events []int
	next := 0
	ctx := sdk.Context{}.WithContext(context.Background())
	ctx = types.WithExecutionScopeHook(ctx, func(parent sdk.Context) (sdk.Context, types.ExecutionScope, error) {
		next++
		return parent.WithValue(frameKey{}, next), &testFrame{id: next, events: &events}, nil
	})
	db := &StateDB{ctx: ctx, cacheCtx: ctx, writeCache: func() {}}
	db.BeginExecutionFrame()
	for i := 0; i < 100; i++ {
		db.BeginExecutionFrame()
		child, err := db.GetCacheContext()
		if err != nil {
			t.Fatal(err)
		}
		if child.Value(frameKey{}) != i+2 {
			t.Fatal("precompile did not receive active frame context")
		}
		db.EndExecutionFrame(i%2 == 0)
		parent, err := db.GetCacheContext()
		if err != nil {
			t.Fatal(err)
		}
		if parent.Value(frameKey{}) != 1 {
			t.Fatal("parent context not restored")
		}
		if len(db.executionFrames) != 1 {
			t.Fatal("completed siblings remain active")
		}
	}
	db.EndExecutionFrame(true)
	if !reflect.DeepEqual(events[:4], []int{-2, 3, -4, 5}) || events[len(events)-1] != -1 {
		t.Fatalf("wrong frame dispositions: %v", events)
	}
	if len(db.executionFrames) != 0 {
		t.Fatal("root frame remains active")
	}
}

func TestExecutionPanicRecoveryPreservesCauseAndClosesFrames(t *testing.T) {
	for _, cause := range []any{"precompile failure", storetypes.ErrorOutOfGas{Descriptor: "free execution"}} {
		var events []int
		next := 0
		ctx := sdk.Context{}.WithContext(context.Background())
		ctx = types.WithExecutionScopeHook(ctx, func(parent sdk.Context) (sdk.Context, types.ExecutionScope, error) {
			next++
			return parent, &testFrame{id: next, events: &events}, nil
		})
		db := &StateDB{ctx: ctx}
		got := func() (caught any) {
			defer func() { caught = recover() }()
			func() {
				defer db.ExecutionPanicRecovery()()
				db.BeginExecutionFrame()
				db.BeginExecutionFrame()
				panic(cause)
			}()
			return nil
		}()
		if !reflect.DeepEqual(got, cause) || !reflect.DeepEqual(events, []int{-2, -1}) || len(db.executionFrames) != 0 {
			t.Fatalf("panic=%v events=%v remaining=%d", got, events, len(db.executionFrames))
		}
	}
}

type failingAbort struct{ calls int }

func (*failingAbort) PrepareAdopt() error { return nil }
func (*failingAbort) Adopt()              {}
func (s *failingAbort) Abort()            { s.calls++; panic("uncertain rollback") }

func TestExecutionPanicRecoveryDoesNotRetryUncertainCleanup(t *testing.T) {
	scope := &failingAbort{}
	db := &StateDB{}
	caught := func(cause any) (result any) {
		defer func() { result = recover() }()
		func() {
			defer db.ExecutionPanicRecovery()()
			if len(db.executionFrames) == 0 {
				db.executionFrames = append(db.executionFrames, executionFrame{scope: scope})
			}
			panic(cause)
		}()
		return nil
	}
	fatal := caught("ordinary failure")
	if _, ok := fatal.(types.ExecutionScopePanic); !ok {
		t.Fatalf("not fatal: %v", fatal)
	}
	if got := caught(fatal); !reflect.DeepEqual(got, fatal) || scope.calls != 1 {
		t.Fatalf("uncertain cleanup retried: %v, %d calls", got, scope.calls)
	}
}

func TestNestedPanicLeavesCallerFrameOpen(t *testing.T) {
	var events []int
	parent, child := &testFrame{id: 1, events: &events}, &testFrame{id: 2, events: &events}
	db := &StateDB{executionFrames: []executionFrame{{scope: parent}}}
	func() {
		defer func() {
			if recover() != "nested failure" {
				t.Fatal("panic changed")
			}
		}()
		func() {
			defer db.ExecutionPanicRecovery()()
			db.executionFrames = append(db.executionFrames, executionFrame{scope: child})
			panic("nested failure")
		}()
	}()
	if parent.closed || !child.closed || len(db.executionFrames) != 1 {
		t.Fatal("nested recovery closed caller frame or leaked child")
	}
	db.EndExecutionFrame(true)
	if !reflect.DeepEqual(events, []int{-2, -1}) {
		t.Fatal(events)
	}
}

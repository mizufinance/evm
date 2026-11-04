package snapshotmulti

import (
	"fmt"
	"io"
	"sort"

	"github.com/cosmos/evm/x/vm/store/snapshotkv"
	"github.com/cosmos/evm/x/vm/store/types"
	vmtypes "github.com/cosmos/evm/x/vm/types"

	storetypes "cosmossdk.io/store/types"
)

type Store struct {
	cms        storetypes.CacheMultiStore
	stores     map[storetypes.StoreKey]*lazyStore
	storeKeys  []*storetypes.KVStoreKey        // ordered kv keys
	tStoreKeys []*storetypes.TransientStoreKey // ordered transient keys
	head       int
}

// lazyStore defers creating the snapshot store and its cache layers until the
// key is accessed. Most txs touch a few of the ~35 stores, so eager branching wastes work.
type lazyStore struct {
	kv *snapshotkv.Store
	// levels[i] is the multi store snapshot index that kv layer i belongs to.
	levels []int
}

var _ types.SnapshotMultiStore = (*Store)(nil)

// NewStore creates a new Store objectwith CacheMultiStore and KVStoreKeys
func NewStore(cms storetypes.CacheMultiStore, keys map[string]*storetypes.KVStoreKey, tkeys map[string]*storetypes.TransientStoreKey) *Store {
	s := &Store{
		cms:       cms,
		stores:    make(map[storetypes.StoreKey]*lazyStore, len(keys)),
		storeKeys: vmtypes.SortedKVStoreKeys(keys),
		head:      types.InitialHead,
	}

	for _, key := range s.storeKeys {
		s.stores[key] = &lazyStore{}
	}

	// transient keys are routed through GetKVStore just like kv keys; a
	// cache-wrapped transient store implements CacheKVStore.
	s.tStoreKeys = sortedTransientStoreKeys(tkeys)
	for _, tk := range s.tStoreKeys {
		s.stores[tk] = &lazyStore{}
	}

	return s
}

// sortedTransientStoreKeys returns the transient store keys ordered by name for
// deterministic iteration.
func sortedTransientStoreKeys(keys map[string]*storetypes.TransientStoreKey) []*storetypes.TransientStoreKey {
	res := make([]*storetypes.TransientStoreKey, 0, len(keys))
	for _, k := range keys {
		res = append(res, k)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Name() < res[j].Name()
	})
	return res
}

// NewStore creates a new Store object with KVStores
func NewStoreWithKVStores(stores map[*storetypes.KVStoreKey]storetypes.CacheWrap) *Store {
	s := &Store{
		stores: make(map[storetypes.StoreKey]*lazyStore),
		head:   types.InitialHead,
	}

	for key, store := range stores {
		s.stores[key] = &lazyStore{kv: snapshotkv.NewStore(store.(storetypes.CacheKVStore))}
		s.storeKeys = append(s.storeKeys, key)
	}

	sort.Slice(s.storeKeys, func(i, j int) bool {
		return s.storeKeys[i].Name() < s.storeKeys[j].Name()
	})

	return s
}

// current returns the active store for key, creating the snapshot store and
// the cache layer for the current snapshot on first access.
func (s *Store) current(key storetypes.StoreKey) storetypes.CacheKVStore {
	ls := s.stores[key]
	if key == nil || ls == nil {
		panic(fmt.Sprintf("kv store with key %v has not been registered in stores", key))
	}
	if ls.kv == nil {
		ls.kv = snapshotkv.NewStore(s.cms.GetKVStore(key).(storetypes.CacheKVStore))
	}
	// Layers skipped for snapshots where the key wasn't touched hold no writes,
	// so a single layer for the latest snapshot is equivalent.
	if top := s.head - 1; top >= 0 && (len(ls.levels) == 0 || ls.levels[len(ls.levels)-1] != top) {
		ls.kv.Snapshot()
		ls.levels = append(ls.levels, top)
	}
	return ls.kv.CurrentStore()
}

// Snapshot pushes a new cached context to the stack,
// and returns the index of it.
func (s *Store) Snapshot() int {
	s.head++

	// latest snapshot is just before head
	return s.head - 1
}

// RevertToSnapshot pops all the cached stores
// whose index is greator than or equal to target.
// The target should be snapshot index returned by `Snapshot`.
// This function panics if the index is out of bounds.
func (s *Store) RevertToSnapshot(target int) {
	if target < 0 || target >= s.head {
		panic(fmt.Errorf("snapshot index %d out of bound [%d..%d)", target, 0, s.head))
	}
	for _, ls := range s.stores {
		n := len(ls.levels)
		for n > 0 && ls.levels[n-1] >= target {
			n--
		}
		if n < len(ls.levels) {
			ls.kv.RevertToSnapshot(n)
			ls.levels = ls.levels[:n]
		}
	}
	s.head = target
}

// GetStoreType returns the type of the store.
func (s *Store) GetStoreType() storetypes.StoreType {
	return storetypes.StoreTypeMulti
}

// Implements CacheWrapper.
func (s *Store) CacheWrap() storetypes.CacheWrap {
	return s.CacheMultiStore().(storetypes.CacheWrap)
}

// CacheWrapWithTrace implements the CacheWrapper interface.
//
// NOTE: CacheWrapWithTrace is a method that enables a Store to satisfy the CacheWrapper interface.
// Although it accepts an io.Writer and tracingContext as inputs, these are not used in the implementation.
// Instead, it simply adds an additional cache layer on top of the existing KVStores.
// As a result, while the return value differs, the behavior is effectively the same as the Snapshot() method.
func (s *Store) CacheWrapWithTrace(_ io.Writer, _ storetypes.TraceContext) storetypes.CacheWrap {
	return s.CacheWrap()
}

// CacheMultiStore snapshots store and return current store.
func (s *Store) CacheMultiStore() storetypes.CacheMultiStore {
	s.Snapshot()
	return s
}

// CacheMultiStoreWithVersion load stores at a snapshot version.
//
// NOTE: CacheMultiStoreWithVersion is no-op function.
func (s *Store) CacheMultiStoreWithVersion(_ int64) (storetypes.CacheMultiStore, error) {
	return s, nil
}

// GetStore returns an underlying Store by key.
func (s *Store) GetStore(key storetypes.StoreKey) storetypes.Store {
	return s.current(key)
}

// GetKVStore returns an underlying KVStore by key.
func (s *Store) GetKVStore(key storetypes.StoreKey) storetypes.KVStore {
	return s.current(key)
}

// TracingEnabled returns if tracing is enabled for the MultiStore.
func (s *Store) TracingEnabled() bool {
	return false
}

// SetTracer sets the tracer for the MultiStore that the underlying
// stores will utilize to trace operations. A MultiStore is returned.
//
// NOTE: SetTracer no-op function.
func (s *Store) SetTracer(_ io.Writer) storetypes.MultiStore {
	return s
}

// SetTracingContext updates the tracing context for the MultiStore by merging
// the given context with the existing context by key. Any existing keys will
// be overwritten. It is implied that the caller should update the context when
// necessary between tracing operations. It returns a modified MultiStore.
//
// NOTE: SetTracingContext no-op function
func (s *Store) SetTracingContext(_ storetypes.TraceContext) storetypes.MultiStore {
	return s
}

// LatestVersion returns the branch version of the store
func (s *Store) LatestVersion() int64 {
	return int64(s.head)
}

// Write calls Write on each underlying store.
func (s *Store) Write() {
	for _, key := range s.storeKeys {
		ls := s.stores[key]
		if ls.kv == nil {
			continue
		}
		ls.kv.Commit()
		ls.levels = nil
	}
	for _, key := range s.tStoreKeys {
		ls := s.stores[key]
		if ls.kv == nil {
			continue
		}
		ls.kv.Commit()
		ls.levels = nil
	}
	s.head = types.InitialHead
}

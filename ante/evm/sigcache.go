package evm

import (
	"github.com/ethereum/go-ethereum/common"
	lru "github.com/hashicorp/golang-lru/v2"
)

// DefaultSignatureCacheSize is the default number of recovered senders to retain.
// Beyond the 32B hash + 20B address payload, golang-lru keeps a map entry and a
// linked-list node per item, so the real footprint is ~100B+/entry: roughly
// 50-80MB resident at this size. Size it down if memory-constrained.
const DefaultSignatureCacheSize = 500_000

// SenderCache is the cache contract the signature verification path depends on.
// It is intentionally minimal so an app can supply its own implementation (for
// example a single cache shared with other ante handlers) without importing the
// concrete SignatureCache or any external signature-cache package.
type SenderCache interface {
	// Get returns the cached sender for a tx hash, if present.
	Get(hash common.Hash) (common.Address, bool)
	// Add stores the recovered sender for a tx hash.
	Add(hash common.Hash, sender common.Address)
}

// SignatureCache is the default SenderCache implementation.
var _ SenderCache = (*SignatureCache)(nil)

// SignatureCache memoizes the sender recovered from an Ethereum transaction
// signature, keyed by the transaction hash. The same Eth tx has its sender
// recovered (ecrecover) once on CheckTx (mempool admission) and again during
// FinalizeBlock; go-ethereum's per-object cache does not survive the re-decode
// between those phases, so the recovery runs twice. This cache turns the second
// recovery into a map lookup.
//
// The tx hash commits to the signed payload and the signature, so a given hash
// determines exactly one recovered sender. The sender->From binding is still
// checked on every hit (see SignatureVerificationWithCache), so the cache can
// never cause a bad signature to be accepted; it only skips work already proven
// safe.
type SignatureCache struct {
	cache *lru.Cache[common.Hash, common.Address]
}

// NewSignatureCache returns a SignatureCache holding up to size entries.
func NewSignatureCache(size int) (*SignatureCache, error) {
	c, err := lru.New[common.Hash, common.Address](size)
	if err != nil {
		return nil, err
	}
	return &SignatureCache{cache: c}, nil
}

// Get returns the cached sender for a tx hash, if present.
func (sc *SignatureCache) Get(hash common.Hash) (common.Address, bool) {
	return sc.cache.Get(hash)
}

// Add stores the recovered sender for a tx hash.
func (sc *SignatureCache) Add(hash common.Hash, sender common.Address) {
	sc.cache.Add(hash, sender)
}

// Len returns the number of cached senders. Useful for metrics and tests.
func (sc *SignatureCache) Len() int {
	return sc.cache.Len()
}

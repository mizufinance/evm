package evm_test

import (
	"math/big"
	"testing"

	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/evm/ante/evm"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	evmsdktypes "github.com/cosmos/evm/x/vm/types"
)

// setupEVMConfigForCache initializes the global EVM chain config the same way the
// mono decorator tests do, so signed Ethereum txs can be built and verified.
func setupEVMConfigForCache(t *testing.T) {
	t.Helper()
	configurator := evmsdktypes.NewEVMConfigurator()
	configurator.ResetTestConfig()
	require.NoError(t, evmsdktypes.SetChainConfig(evmsdktypes.DefaultChainConfig(evmsdktypes.DefaultEVMChainID)))
	require.NoError(t, configurator.
		WithExtendedEips(evmsdktypes.DefaultCosmosEVMActivators).
		WithEVMCoinInfo(evmsdktypes.EvmCoinInfo{
			Denom:         evmsdktypes.DefaultEVMExtendedDenom,
			ExtendedDenom: evmsdktypes.DefaultEVMExtendedDenom,
			DisplayDenom:  evmsdktypes.DefaultEVMDisplayDenom,
			Decimals:      18,
		}).
		Configure())
}

func newSignedEthMsg(t *testing.T) (*evmsdktypes.MsgEthereumTx, ethtypes.Signer) {
	t.Helper()
	privKey, err := ethsecp256k1.GenerateKey()
	require.NoError(t, err)
	msg := signMsgEthereumTx(t, privKey, &evmsdktypes.EvmTxArgs{
		Nonce:    0,
		GasLimit: 100000,
		GasPrice: big.NewInt(1),
		Input:    []byte("test"),
	})
	signer := ethtypes.LatestSignerForChainID(evmsdktypes.GetEthChainConfig().ChainID)
	return msg, signer
}

// TestSignatureCacheHit verifies the same tx twice (CheckTx then FinalizeBlock):
// the second pass must succeed without adding a new cache entry, proving it was
// served from cache instead of re-running ecrecover.
func TestSignatureCacheHit(t *testing.T) {
	setupEVMConfigForCache(t)
	msg, signer := newSignedEthMsg(t)

	cache, err := evm.NewSignatureCache(evm.DefaultSignatureCacheSize)
	require.NoError(t, err)

	require.NoError(t, evm.SignatureVerificationWithCache(msg, msg.AsTransaction(), signer, cache))
	require.Equal(t, 1, cache.Len())

	require.NoError(t, evm.SignatureVerificationWithCache(msg, msg.AsTransaction(), signer, cache))
	require.Equal(t, 1, cache.Len(), "second verify should be a cache hit, not a new entry")
}

// TestSignatureCacheRejectsBadSender ensures the cache never masks a sender
// mismatch: tampering with msg.From must be rejected and never cached.
func TestSignatureCacheRejectsBadSender(t *testing.T) {
	setupEVMConfigForCache(t)
	msg, signer := newSignedEthMsg(t)

	// flip a byte of the claimed sender so it no longer matches the recovered one.
	msg.From[0] ^= 0xFF

	cache, err := evm.NewSignatureCache(evm.DefaultSignatureCacheSize)
	require.NoError(t, err)

	require.Error(t, evm.SignatureVerificationWithCache(msg, msg.AsTransaction(), signer, cache))
	require.Equal(t, 0, cache.Len(), "mismatched sender must never be cached")
}

// TestSignatureCacheNilMatchesUncached asserts a nil cache behaves exactly like
// the original SignatureVerification.
func TestSignatureCacheNilMatchesUncached(t *testing.T) {
	setupEVMConfigForCache(t)
	msg, signer := newSignedEthMsg(t)

	require.NoError(t, evm.SignatureVerificationWithCache(msg, msg.AsTransaction(), signer, nil))
	require.NoError(t, evm.SignatureVerification(msg, msg.AsTransaction(), signer))
}

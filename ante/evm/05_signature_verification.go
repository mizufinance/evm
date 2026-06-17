package evm

import (
	"bytes"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"

	anteinterfaces "github.com/cosmos/evm/ante/interfaces"
	evmtypes "github.com/cosmos/evm/x/vm/types"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	errortypes "github.com/cosmos/cosmos-sdk/types/errors"
)

// EthSigVerificationDecorator validates an ethereum signatures
type EthSigVerificationDecorator struct {
	evmKeeper anteinterfaces.EVMKeeper
	sigCache  SenderCache
}

// NewEthSigVerificationDecorator creates a new EthSigVerificationDecorator
func NewEthSigVerificationDecorator(ek anteinterfaces.EVMKeeper) EthSigVerificationDecorator {
	return EthSigVerificationDecorator{
		evmKeeper: ek,
	}
}

// WithSignatureCache returns a copy of the decorator that skips re-recovering the
// sender of a transaction already verified (e.g. during CheckTx). Leaving it
// unset preserves the existing behavior of recovering the sender every time.
func (esvd EthSigVerificationDecorator) WithSignatureCache(cache SenderCache) EthSigVerificationDecorator {
	esvd.sigCache = cache
	return esvd
}

// AnteHandle validates checks that the registered chain id is the same as the one on the message, and
// that the signer address matches the one defined on the message.
// It's not skipped for RecheckTx, because it set `From` address which is critical from other ante handler to work.
// Failure in RecheckTx will prevent tx to be included into block, especially when CheckTx succeed, in which case user
// won't see the error message.
func (esvd EthSigVerificationDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (newCtx sdk.Context, err error) {
	ethCfg := evmtypes.GetEthChainConfig()
	blockNum := big.NewInt(ctx.BlockHeight())
	signer := ethtypes.MakeSigner(ethCfg, blockNum, uint64(ctx.BlockTime().Unix())) //#nosec G115 -- int overflow is not a concern here

	msgs := tx.GetMsgs()
	if msgs == nil {
		return ctx, errorsmod.Wrap(errortypes.ErrUnknownRequest, "invalid transaction. Transaction without messages")
	}

	for _, msg := range msgs {
		msgEthTx, ok := msg.(*evmtypes.MsgEthereumTx)
		if !ok {
			return ctx, errorsmod.Wrapf(errortypes.ErrUnknownRequest, "invalid message type %T, expected %T", msg, (*evmtypes.MsgEthereumTx)(nil))
		}

		err := SignatureVerificationWithCache(msgEthTx, msgEthTx.AsTransaction(), signer, esvd.sigCache)
		if err != nil {
			return ctx, err
		}
	}

	return next(ctx, tx, simulate)
}

// SignatureVerification checks that the registered chain id is the same as the one on the message, and
// that the signer address matches the one defined on the message.
// The function set the field from of the given message equal to the sender
// computed from the signature of the Ethereum transaction.
func SignatureVerification(
	msg *evmtypes.MsgEthereumTx,
	ethTx *ethtypes.Transaction,
	signer ethtypes.Signer,
) error {
	if err := msg.VerifySender(signer); err != nil {
		return errorsmod.Wrapf(errortypes.ErrorInvalidSigner, "signature verification failed: %s", err.Error())
	}

	return nil
}

// SignatureVerificationWithCache behaves like SignatureVerification but, when
// cache is non-nil, skips the ecrecover for a transaction whose sender was
// already recovered (e.g. during CheckTx). The sender->From binding is still
// verified on every call, so the cache never relaxes signature checking. A nil
// cache makes this identical to SignatureVerification.
func SignatureVerificationWithCache(
	msg *evmtypes.MsgEthereumTx,
	ethTx *ethtypes.Transaction,
	signer ethtypes.Signer,
	cache SenderCache,
) error {
	if cache == nil {
		return SignatureVerification(msg, ethTx, signer)
	}

	// The tx hash commits to the signed payload and signature, so it uniquely
	// determines the recovered sender. On a hit we still bind it to msg.From.
	hash := ethTx.Hash()
	if sender, ok := cache.Get(hash); ok {
		if !bytes.Equal(msg.From, sender.Bytes()) {
			return errorsmod.Wrapf(errortypes.ErrorInvalidSigner,
				"signature verification failed: got %s, expected %s",
				sender, common.BytesToAddress(msg.From))
		}
		return nil
	}

	from, err := ethtypes.Sender(signer, ethTx)
	if err != nil {
		return errorsmod.Wrapf(errortypes.ErrorInvalidSigner, "signature verification failed: %s", err.Error())
	}
	if !bytes.Equal(msg.From, from.Bytes()) {
		return errorsmod.Wrapf(errortypes.ErrorInvalidSigner,
			"signature verification failed: got %s, expected %s",
			from, common.BytesToAddress(msg.From))
	}

	cache.Add(hash, from)
	return nil
}

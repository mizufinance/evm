package server

import (
	"fmt"

	rpcclient "github.com/cometbft/cometbft/rpc/client"

	"github.com/cosmos/cosmos-sdk/client"
)

// CometClientWrapper is an optional app hook to wrap the in-process CometBFT
// client that JSON-RPC, gRPC and REST all share. For example, an archive node
// can serve heights pruned from the block store out of its own storage.
//
// The wrapper must still be a full rpcclient.Client (embed the one passed in),
// since the JSON-RPC backend and EVM indexer assert on it.
type CometClientWrapper interface {
	WrapCometClient(client.CometRPC) client.CometRPC
}

// wrapCometClient applies the app's wrapper, if it has one.
func wrapCometClient(app any, c client.CometRPC) (client.CometRPC, error) {
	w, ok := app.(CometClientWrapper)
	if !ok {
		return c, nil
	}
	wrapped := w.WrapCometClient(c)
	if _, ok := wrapped.(rpcclient.Client); !ok {
		return nil, fmt.Errorf("WrapCometClient returned %T, which is not a full rpcclient.Client", wrapped)
	}
	return wrapped, nil
}

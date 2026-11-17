package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	rpcclient "github.com/cometbft/cometbft/rpc/client"
	"github.com/cometbft/cometbft/rpc/client/mock"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"

	"github.com/cosmos/cosmos-sdk/client"
)

type plainApp struct{}

type wrappingApp struct {
	wrap func(client.CometRPC) client.CometRPC
}

func (a wrappingApp) WrapCometClient(c client.CometRPC) client.CometRPC { return a.wrap(c) }

// archiveClient overrides one call and embeds the rest, the intended usage.
type archiveClient struct{ rpcclient.Client }

func (archiveClient) Block(context.Context, *int64) (*coretypes.ResultBlock, error) {
	return &coretypes.ResultBlock{}, nil
}

// partialClient only satisfies CometRPC, which would break the JSON-RPC backend.
type partialClient struct{ client.CometRPC }

func TestWrapCometClient(t *testing.T) {
	base := mock.Client{}
	cases := []struct {
		name    string
		app     any
		want    any
		wantErr bool
	}{
		{"no hook keeps the client", plainApp{}, base, false},
		{"hook wraps the client", wrappingApp{func(c client.CometRPC) client.CometRPC { return archiveClient{c.(rpcclient.Client)} }}, archiveClient{base}, false},
		{"partial client rejected", wrappingApp{func(c client.CometRPC) client.CometRPC { return partialClient{c} }}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := wrapCometClient(tc.app, base)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

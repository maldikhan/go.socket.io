package socketio_v5_client

import (
	"context"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	mocks "github.com/maldikhan/go.socket.io/socket.io/v5/client/mocks"
	"github.com/stretchr/testify/require"
)

func TestConnectRejectsConcurrentCallsWithoutReplacingContext(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	engine := mocks.NewMockEngineIOClient(ctrl)
	client := &Client{engineio: engine}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	finish := make(chan struct{})
	engine.EXPECT().Connect(ctx).DoAndReturn(func(context.Context) error {
		close(started)
		<-finish
		return nil
	})
	engine.EXPECT().Close().Return(nil)
	first := make(chan error, 1)
	go func() { first <- client.Connect(ctx) }()
	<-started
	const contenders = 32
	var callers sync.WaitGroup
	results := make(chan error, contenders)
	for i := 0; i < contenders; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			results <- client.Connect(context.Background())
		}()
	}
	callers.Wait()
	close(results)
	for err := range results {
		require.ErrorIs(t, err, ErrConnectAlreadyStarted)
	}
	close(finish)
	require.NoError(t, <-first)
	client.mutex.RLock()
	require.Same(t, ctx, client.ctx)
	client.mutex.RUnlock()
	require.ErrorIs(t, client.Connect(context.Background()), ErrConnectAlreadyStarted)
	require.NoError(t, client.Close())
	require.ErrorIs(t, client.Connect(context.Background()), ErrClientClosed)
}

func TestConnectAfterCloseDoesNotCallCustomEngine(t *testing.T) {
	t.Parallel()
	engine := mocks.NewMockEngineIOClient(gomock.NewController(t))
	client := &Client{engineio: engine}
	engine.EXPECT().Close().Return(nil)
	require.NoError(t, client.Close())
	require.ErrorIs(t, client.Connect(context.Background()), ErrClientClosed)
	require.Nil(t, client.ctx)
}

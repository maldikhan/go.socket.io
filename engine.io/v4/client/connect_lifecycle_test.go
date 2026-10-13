package engineio_v4_client

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	"github.com/stretchr/testify/require"
)

func TestConnectRejectsConcurrentAndRepeatedSessions(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runStarted := make(chan struct{})
	finishRun := make(chan struct{})
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(runCtx context.Context, _ *url.URL, _ string, _ chan<- engineio_v4.Frame, _ chan<- error) error {
			close(runStarted)
			<-finishRun
			return nil
		})
	transport.EXPECT().RequestHandshake().Return(nil)
	transport.EXPECT().Stop().DoAndReturn(func() error {
		client.transportClosed <- nil
		return nil
	})
	first := make(chan error, 1)
	go func() { first <- client.Connect(ctx) }()
	<-runStarted
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
	close(finishRun)
	require.NoError(t, <-first)
	require.ErrorIs(t, client.Connect(context.Background()), ErrConnectAlreadyStarted)
	client.transportMu.RLock()
	require.Same(t, ctx, client.ctx, "rejected Connect must preserve the live session context")
	client.transportMu.RUnlock()
	require.NoError(t, client.Close())
	require.ErrorIs(t, client.Connect(context.Background()), ErrClientClosed)
}

func TestConnectAfterCloseCannotClearClosingFlag(t *testing.T) {
	t.Parallel()
	client, _, _, _ := newReconnectClient(t)
	t.Cleanup(client.pingInterval.Stop)
	atomic.StoreUint32(&client.closing, 1)
	require.ErrorIs(t, client.Connect(context.Background()), ErrClientClosed)
	require.Equal(t, uint32(1), atomic.LoadUint32(&client.closing))
	require.False(t, client.connectStarted)
}

func TestTimedConnectRejectsConcurrentCalls(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)
	client.connectTimeout = 30 * time.Millisecond
	runStarted := make(chan struct{})
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *url.URL, _ string, _ chan<- engineio_v4.Frame, _ chan<- error) error {
			close(runStarted)
			<-ctx.Done()
			return ctx.Err()
		})
	first := make(chan error, 1)
	go func() { first <- client.Connect(context.Background()) }()
	<-runStarted
	require.ErrorIs(t, client.Connect(context.Background()), ErrConnectAlreadyStarted)
	require.ErrorIs(t, <-first, context.DeadlineExceeded)
	client.pingInterval.Stop()
}

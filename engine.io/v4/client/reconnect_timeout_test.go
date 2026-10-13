package engineio_v4_client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	ws_transport "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket"
	ws_coder "github.com/maldikhan/go.socket.io/websocket/coder"
	"github.com/stretchr/testify/require"
)

func TestReconnectTimeoutBoundsDialAndHandshakeRequest(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"dial", "handshake request"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			client, transport, _, _ := newReconnectClient(t)
			t.Cleanup(client.pingInterval.Stop)
			var attemptCtx context.Context
			transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, _ *url.URL, _ string, _ chan<- engineio_v4.Frame, _ chan<- error) error {
					attemptCtx = ctx
					if phase == "dial" {
						<-ctx.Done()
						return ctx.Err()
					}
					return nil
				})
			if phase == "handshake request" {
				transport.EXPECT().RequestHandshake().DoAndReturn(func() error {
					<-attemptCtx.Done()
					return attemptCtx.Err()
				})
				transport.EXPECT().Stop().DoAndReturn(func() error {
					client.transportClosed <- nil
					return nil
				})
			}
			started := time.Now()
			require.ErrorIs(t, client.reconnectOnce(30*time.Millisecond), context.DeadlineExceeded)
			require.Less(t, time.Since(started), time.Second)
			require.NoError(t, client.ctx.Err(), "failed attempt must not cancel the parent session")
		})
	}
}

func TestReconnectWaitsForUpgradeAndSurvivesItsTimeout(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)
	var attemptCtx context.Context
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *url.URL, _ string, _ chan<- engineio_v4.Frame, _ chan<- error) error {
			attemptCtx = ctx
			return nil
		})
	upgrade := make(chan struct{})
	requestComplete := make(chan struct{})
	transport.EXPECT().RequestHandshake().DoAndReturn(func() error {
		client.transportMu.Lock()
		client.waitUpgrade = upgrade
		client.transportMu.Unlock()
		completeHandshake(client)
		close(requestComplete)
		return nil
	})
	transport.EXPECT().Stop().DoAndReturn(func() error {
		client.transportClosed <- nil
		return nil
	})
	finished := make(chan error, 1)
	go func() { finished <- client.reconnectOnce(100 * time.Millisecond) }()
	<-requestComplete
	select {
	case err := <-finished:
		t.Fatalf("retry finished before probe pong: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	client.transportMu.Lock()
	client.hadUpgrade.Do(func() { close(upgrade) })
	client.transportMu.Unlock()
	require.NoError(t, <-finished)
	// The attempt's child becomes the session context: removing the timer must
	// leave a healthy transport alive well beyond the connection phase bound.
	select {
	case <-attemptCtx.Done():
		t.Fatal("successful retry context cancelled after handshake")
	case <-time.After(150 * time.Millisecond):
	}
	require.NoError(t, client.Close())
	require.ErrorIs(t, attemptCtx.Err(), context.Canceled)
}

func TestReconnectRejectsMissingProbeAndUpgradeWriteFailure(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"missing probe pong", "upgrade write fails"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			client, transport, _, _ := newReconnectClient(t)
			t.Cleanup(client.pingInterval.Stop)
			writeErr := errors.New("upgrade write failed")
			transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			transport.EXPECT().RequestHandshake().DoAndReturn(func() error {
				client.transportMu.Lock()
				client.waitUpgrade = make(chan struct{})
				client.transportMu.Unlock()
				completeHandshake(client)
				if phase == "upgrade write fails" {
					client.transportMu.Lock()
					client.handshakeErr = writeErr
					client.hadUpgrade.Do(func() { close(client.waitUpgrade) })
					client.transportMu.Unlock()
				}
				return nil
			})
			transport.EXPECT().Stop().DoAndReturn(func() error {
				client.transportClosed <- nil
				return nil
			})
			err := client.reconnectOnce(30 * time.Millisecond)
			if phase == "missing probe pong" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.ErrorIs(t, err, writeErr)
			}
			require.NoError(t, client.ctx.Err())
		})
	}
}

func TestSupervisorCancellationWaitsForTransportExit(t *testing.T) {
	t.Parallel()
	client, _, _, _ := newReconnectClient(t)
	t.Cleanup(client.pingInterval.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	closed := make(chan error, 1)
	done := make(chan struct{})
	go client.superviseCycle(closed, done, ctx)
	cancel()
	select {
	case <-done:
		t.Fatal("supervisor released its cycle before the transport stopped")
	case <-time.After(10 * time.Millisecond):
	}
	closed <- ctx.Err()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not finish after transport exited")
	}
}

func TestReconnectCoderBlackholeExhaustsBoundedFreshAttempts(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-r.Context().Done() // Accept TCP but never answer the HTTP upgrade.
	}))
	defer server.Close()
	backend, err := ws_coder.New()
	require.NoError(t, err)
	transport, err := ws_transport.NewTransport(ws_transport.WithWebSocket(backend))
	require.NoError(t, err)
	client, err := NewClient(
		WithRawURL(server.URL),
		WithTransport(transport),
		WithSupportedTransports([]Transport{transport}),
		WithConnectTimeout(30*time.Millisecond),
		WithReconnectWait(time.Millisecond),
		WithReconnectAttempts(2),
	)
	require.NoError(t, err)
	client.ctx = context.Background()
	var failed, reconnected atomic.Bool
	client.On("reconnect_failed", func([]byte) { failed.Store(true) })
	client.On("reconnect", func([]byte) { reconnected.Store(true) })
	started := time.Now()
	client.reconnectLoop(errors.New("connection dropped"))
	require.True(t, failed.Load())
	require.False(t, reconnected.Load())
	require.Equal(t, int32(2), requests.Load(), "each retry needs a fresh, uncancelled dial context")
	require.Less(t, time.Since(started), time.Second, "a blackholed upgrade must not leave retries hung")
}

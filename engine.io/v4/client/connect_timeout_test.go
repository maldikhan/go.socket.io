package engineio_v4_client

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	mocks "github.com/maldikhan/go.socket.io/engine.io/v4/client/mocks"
)

func TestWithConnectTimeout(t *testing.T) {
	t.Parallel()

	t.Run("valid timeout", func(t *testing.T) {
		client, err := NewClient(
			WithRawURL("http://localhost"),
			WithConnectTimeout(3*time.Second),
		)
		require.NoError(t, err)
		assert.Equal(t, 3*time.Second, client.connectTimeout)
	})

	t.Run("non-positive timeout", func(t *testing.T) {
		for _, timeout := range []time.Duration{0, -time.Second} {
			client, err := NewClient(
				WithRawURL("http://localhost"),
				WithConnectTimeout(timeout),
			)
			assert.Nil(t, client)
			assert.ErrorContains(t, err, "connect timeout must be positive")
		}
	})
}

// newTimeoutTestClient builds a client wired with mocks the way Connect()
// expects, with a connect timeout configured.
func newTimeoutTestClient(t *testing.T, ctrl *gomock.Controller, timeout time.Duration) (*Client, *mocks.MockTransport, *mocks.MockParser) {
	t.Helper()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

	mockParser := mocks.NewMockParser(ctrl)
	mockTransport := mocks.NewMockTransport(ctrl)

	testURL, err := url.Parse("http://localhost")
	require.NoError(t, err)

	client := &Client{
		url:                 testURL,
		log:                 mockLogger,
		parser:              mockParser,
		supportedTransports: map[engineio_v4.EngineIOTransport]Transport{engineio_v4.TransportPolling: mockTransport},
		transport:           mockTransport,
		connectTimeout:      timeout,
	}
	return client, mockTransport, mockParser
}

func TestClient_Connect_Timeout(t *testing.T) {
	t.Parallel()

	t.Run("handshake completes in time", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockTransport, mockParser := newTimeoutTestClient(t, ctrl, 5*time.Second)

		handshakeResp := &engineio_v4.HandshakeResponse{
			Sid:          "test-sid",
			PingInterval: 25000,
			PingTimeout:  5000,
		}
		respData, err := json.Marshal(handshakeResp)
		require.NoError(t, err)

		mockTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockTransport.EXPECT().RequestHandshake().DoAndReturn(func() error {
			go func() {
				client.messages <- append([]byte{'0'}, respData...)
			}()
			return nil
		})
		mockParser.EXPECT().Parse(gomock.Any()).Return(&engineio_v4.Message{
			Type: engineio_v4.PacketOpen,
			Data: respData,
		}, nil)
		mockTransport.EXPECT().SetHandshake(gomock.Any())
		mockTransport.EXPECT().Stop().Do(func() {
			client.transportClosed <- nil
		})

		err = client.Connect(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "test-sid", client.sid)

		// Connect() with a timeout blocks until the handshake is processed, so
		// the gate must already be closed.
		select {
		case <-client.waitHandshake:
		default:
			t.Fatal("Connect returned before the handshake gate was closed")
		}

		// The session context survives the connect phase: the timeout did not
		// cancel it.
		require.NotNil(t, client.ctx)
		assert.NoError(t, client.ctx.Err(), "session context must stay alive after a successful connect")

		require.NoError(t, client.Close())
		// Close() releases the connect-phase context to avoid a context leak.
		assert.Error(t, client.ctx.Err(), "Close must release the connection context")
	})

	t.Run("handshake never arrives", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockTransport, _ := newTimeoutTestClient(t, ctrl, 30*time.Millisecond)

		mockTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockTransport.EXPECT().RequestHandshake().Return(nil)
		mockTransport.EXPECT().Stop().Do(func() {
			client.transportClosed <- nil
		})

		start := time.Now()
		err := client.Connect(context.Background())
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Less(t, time.Since(start), 5*time.Second, "Connect must fail fast on timeout")
	})

	t.Run("timeout fires during handshake request", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockTransport, _ := newTimeoutTestClient(t, ctrl, 30*time.Millisecond)

		var runCtx context.Context
		mockTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, _ *url.URL, _ string, _ chan<- []byte, _ chan<- error) error {
				runCtx = ctx
				return nil
			})
		// Simulate a hanging handshake request (e.g. unreachable host): it only
		// returns once the connection context is cancelled by the timeout.
		mockTransport.EXPECT().RequestHandshake().DoAndReturn(func() error {
			<-runCtx.Done()
			return runCtx.Err()
		})
		mockTransport.EXPECT().Stop().Do(func() {
			client.transportClosed <- nil
		})

		err := client.Connect(context.Background())
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("transport run error is passed through", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockTransport, _ := newTimeoutTestClient(t, ctrl, 5*time.Second)

		runErr := errors.New("run error")
		mockTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(runErr)

		err := client.Connect(context.Background())
		assert.ErrorIs(t, err, runErr)
		assert.NotErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("caller cancellation wins over timeout error", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockTransport, _ := newTimeoutTestClient(t, ctrl, 5*time.Second)

		mockTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockTransport.EXPECT().RequestHandshake().Return(nil)
		mockTransport.EXPECT().Stop().Do(func() {
			client.transportClosed <- nil
		})

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		err := client.Connect(ctx)
		assert.ErrorIs(t, err, context.Canceled)
		assert.NotErrorIs(t, err, context.DeadlineExceeded)
	})
}

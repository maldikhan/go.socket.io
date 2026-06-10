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

		// The failed-connect teardown must leave the client observably closed:
		// connect() published the handshake gate before RequestHandshake
		// failed, so the gate has to be released and the transport nilled —
		// otherwise Send() would park forever on a handshake that will never
		// complete.
		sendErr := make(chan error, 1)
		go func() { sendErr <- client.Send([]byte("x")) }()
		select {
		case err := <-sendErr:
			assert.EqualError(t, err, "client is closed")
		case <-time.After(2 * time.Second):
			t.Fatal("Send must fail fast after a failed Connect, not block")
		}
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

// setupUpgradeScenario wires a client whose handshake advertises a websocket
// upgrade, so Connect() goes through the transport upgrade path. The returned
// websocket mock still needs SendMessage expectations for the probe ping (and
// the upgrade packet when the pong is delivered).
func setupUpgradeScenario(t *testing.T, ctrl *gomock.Controller, timeout time.Duration) (*Client, *mocks.MockTransport) {
	t.Helper()

	client, mockPolling, mockParser := newTimeoutTestClient(t, ctrl, timeout)
	mockWs := mocks.NewMockTransport(ctrl)
	client.supportedTransports[engineio_v4.TransportWebsocket] = mockWs

	handshakeResp := &engineio_v4.HandshakeResponse{
		Sid:          "test-sid",
		PingInterval: 25000,
		PingTimeout:  5000,
		Upgrades:     []string{"websocket"},
	}
	respData, err := json.Marshal(handshakeResp)
	require.NoError(t, err)

	mockPolling.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockPolling.EXPECT().RequestHandshake().DoAndReturn(func() error {
		go func() {
			client.messages <- append([]byte{'0'}, respData...)
		}()
		return nil
	})
	mockPolling.EXPECT().SetHandshake(gomock.Any())
	mockPolling.EXPECT().Transport().Return(engineio_v4.TransportPolling).AnyTimes()
	// Stopped when the upgrade swaps the transports.
	mockPolling.EXPECT().Stop().DoAndReturn(func() error {
		client.transportClosed <- nil
		return nil
	})

	mockWs.EXPECT().SetHandshake(gomock.Any())
	mockWs.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	// Stopped by the final Close().
	mockWs.EXPECT().Stop().DoAndReturn(func() error {
		client.transportClosed <- nil
		return nil
	})

	mockParser.EXPECT().Parse(gomock.Any()).DoAndReturn(func(data []byte) (*engineio_v4.Message, error) {
		if len(data) > 0 && data[0] == '0' {
			return &engineio_v4.Message{Type: engineio_v4.PacketOpen, Data: data[1:]}, nil
		}
		return &engineio_v4.Message{Type: engineio_v4.PacketPong, Data: []byte("probe")}, nil
	}).AnyTimes()
	mockParser.EXPECT().Serialize(gomock.Any()).DoAndReturn(func(m *engineio_v4.Message) ([]byte, error) {
		switch m.Type {
		case engineio_v4.PacketPing:
			return []byte("2probe"), nil
		case engineio_v4.PacketUpgrade:
			return []byte("5"), nil
		default:
			return []byte("4"), nil
		}
	}).AnyTimes()

	return client, mockWs
}

func TestClient_Connect_Timeout_Upgrade(t *testing.T) {
	t.Parallel()

	t.Run("upgrade completes in time", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockWs := setupUpgradeScenario(t, ctrl, 5*time.Second)

		// The server answers the probe ping, finishing the upgrade.
		mockWs.EXPECT().SendMessage([]byte("2probe")).DoAndReturn(func([]byte) error {
			go func() {
				client.messages <- []byte("3probe")
			}()
			return nil
		})
		mockWs.EXPECT().SendMessage([]byte("5")).Return(nil)

		err := client.Connect(context.Background())
		require.NoError(t, err)

		// Connect() must not return before the upgrade gate is closed,
		// otherwise Send() could still block after a "successful" connect.
		select {
		case <-client.waitUpgrade:
		default:
			t.Fatal("Connect returned before the upgrade completed")
		}

		require.NoError(t, client.Close())
	})

	t.Run("upgrade probe never answered", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockWs := setupUpgradeScenario(t, ctrl, 50*time.Millisecond)

		// The probe ping is sent but the server never replies with the pong,
		// so the upgrade (and thus the connection phase) never completes.
		mockWs.EXPECT().SendMessage([]byte("2probe")).Return(nil)

		start := time.Now()
		err := client.Connect(context.Background())
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Less(t, time.Since(start), 5*time.Second, "Connect must fail fast on a stuck upgrade")
	})
}

func TestClient_Connect_Timeout_UnblocksSend(t *testing.T) {
	t.Parallel()

	t.Run("send after a timed-out connect fails fast", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockTransport, _ := newTimeoutTestClient(t, ctrl, 30*time.Millisecond)

		mockTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockTransport.EXPECT().RequestHandshake().Return(nil)
		mockTransport.EXPECT().Stop().DoAndReturn(func() error {
			client.transportClosed <- nil
			return nil
		})

		err := client.Connect(context.Background())
		require.ErrorIs(t, err, context.DeadlineExceeded)

		// The teardown closed the connection gates, so Send() must observe the
		// nil transport instead of blocking on the never-completed handshake.
		sendDone := make(chan error, 1)
		go func() {
			sendDone <- client.Send([]byte("hello"))
		}()

		select {
		case err := <-sendDone:
			assert.ErrorContains(t, err, "client is closed")
		case <-time.After(time.Second):
			t.Fatal("Send blocked after a failed connect")
		}
	})

	t.Run("timeout unblocks a Send pending on the handshake gate", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockTransport, _ := newTimeoutTestClient(t, ctrl, 50*time.Millisecond)

		mockTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockTransport.EXPECT().RequestHandshake().Return(nil)
		mockTransport.EXPECT().Stop().DoAndReturn(func() error {
			client.transportClosed <- nil
			return nil
		})

		connectDone := make(chan error, 1)
		go func() {
			connectDone <- client.Connect(context.Background())
		}()

		// Wait until the connect cycle published the handshake gate, then park
		// a Send() on it.
		require.Eventually(t, func() bool {
			client.transportMu.RLock()
			defer client.transportMu.RUnlock()
			return client.waitHandshake != nil
		}, time.Second, time.Millisecond)

		sendDone := make(chan error, 1)
		go func() {
			sendDone <- client.Send([]byte("hello"))
		}()

		select {
		case err := <-connectDone:
			assert.ErrorIs(t, err, context.DeadlineExceeded)
		case <-time.After(time.Second):
			t.Fatal("Connect did not time out")
		}

		select {
		case err := <-sendDone:
			assert.ErrorContains(t, err, "client is closed")
		case <-time.After(time.Second):
			t.Fatal("Send was not unblocked by the connect timeout teardown")
		}
	})
}

// TestClient_Connect_Timeout_CloseAborts verifies that a concurrent Close()
// aborting a timed Connect() is reported as ErrConnectAborted — not
// misclassified as context.DeadlineExceeded (the timer never fired) and not as
// a caller cancellation (the session context is still live).
func TestClient_Connect_Timeout_CloseAborts(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client, mockTransport, _ := newTimeoutTestClient(t, ctrl, 30*time.Second)

	mockTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	// The handshake request succeeds but no OPEN packet ever arrives, so
	// Connect parks waiting on the handshake gate until Close() aborts it.
	mockTransport.EXPECT().RequestHandshake().Return(nil)
	mockTransport.EXPECT().Stop().Do(func() {
		client.transportClosed <- nil
	})

	go func() {
		// Give Connect a moment to reach the gate wait, then close the client.
		time.Sleep(50 * time.Millisecond)
		_ = client.Close()
	}()

	start := time.Now()
	err := client.Connect(context.Background())
	assert.ErrorIs(t, err, ErrConnectAborted)
	assert.NotErrorIs(t, err, context.DeadlineExceeded,
		"a concurrent Close must not be misreported as a timeout")
	assert.Less(t, time.Since(start), 10*time.Second, "Close must abort Connect promptly")
}

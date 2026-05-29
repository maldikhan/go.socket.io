package engineio_v4_client_transport

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	mock_engineio_v4_client_transport "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket/mocks"
	"github.com/maldikhan/go.socket.io/utils"
)

func TestTransport_Transport(t *testing.T) {
	t.Parallel()

	transport := &Transport{}
	assert.Equal(t, engineio_v4.TransportWebsocket, transport.Transport())
}

func TestTransport_Stop(t *testing.T) {
	t.Parallel()

	t.Run("normal stop", func(t *testing.T) {
		transport := &Transport{
			stopPooling: make(chan struct{}, 1),
		}
		err := transport.Stop()
		assert.NoError(t, err)
		assert.Len(t, transport.stopPooling, 1)
	})

	t.Run("non-blocking when read loop already exited", func(t *testing.T) {
		// stopPooling is unbuffered and nobody is reading — Stop must not deadlock
		transport := &Transport{
			stopPooling: make(chan struct{}),
		}

		done := make(chan struct{})
		go func() {
			err := transport.Stop()
			assert.NoError(t, err)
			close(done)
		}()

		select {
		case <-done:
			// Stop returned without blocking — correct
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Stop() deadlocked on full/unread stopPooling channel")
		}
	})
}

func TestTransport_Run(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
	mockLogger := &utils.DefaultLogger{Level: utils.NONE}

	// Limit test time
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	u, _ := url.Parse("http://example.com")
	sid := "test-sid"
	messagesChan := make(chan []byte, 1)
	onClose := make(chan error, 1)

	transport := &Transport{
		log:         mockLogger,
		ws:          mockWS,
		stopPooling: make(chan struct{}, 1),
	}

	mockWS.EXPECT().Dial(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
		*message = []byte("test message")
		return nil
	}).AnyTimes()
	mockWS.EXPECT().Close().Return(nil).AnyTimes()

	err := transport.Run(ctx, u, sid, messagesChan, onClose)
	require.NoError(t, err)

	// Check transport settings
	assert.Equal(t, ctx, transport.ctx)
	assert.Equal(t, sid, transport.sid)
	assert.Equal(t, u, transport.url)
	assert.NotNil(t, transport.messages)
	assert.NotNil(t, transport.onClose)

	// Wait init
	time.Sleep(100 * time.Millisecond)

	// Check message received
	select {
	case msg := <-messagesChan:
		assert.Equal(t, []byte("test message"), msg)
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for message")
	}

	// Stop transport
	err = transport.Stop()
	assert.NoError(t, err)
}

func TestTransport_RequestHandshake(t *testing.T) {
	t.Parallel()

	transport := &Transport{}
	err := transport.RequestHandshake()
	assert.NoError(t, err)
}

func TestTransport_SetHandshake(t *testing.T) {
	t.Parallel()

	transport := &Transport{}
	handshake := &engineio_v4.HandshakeResponse{Sid: "new-sid"}
	transport.SetHandshake(handshake)
	assert.Equal(t, "new-sid", transport.sid)
}

func TestTransport_buildWsUrl(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tests := []struct {
		name                       string
		url                        *url.URL
		sid                        string
		expected                   string
		expectedErrorLog           string
		expectedErrorLogParameters []interface{}
	}{
		{
			name:     "HTTP URL",
			url:      &url.URL{Scheme: "http", Host: "example.com", Path: "/socket.io/"},
			sid:      "test-sid",
			expected: "ws://example.com/socket.io/?EIO=4&sid=test-sid&transport=websocket",
		},
		{
			name:     "HTTPS URL",
			url:      &url.URL{Scheme: "https", Host: "example.com", Path: "/socket.io/"},
			sid:      "test-sid",
			expected: "wss://example.com/socket.io/?EIO=4&sid=test-sid&transport=websocket",
		},
		{
			name:     "HTTPS URL with query",
			url:      &url.URL{Scheme: "https", Host: "example.com", Path: "/socket.io/", RawQuery: "query=foo"},
			sid:      "test-sid",
			expected: "wss://example.com/socket.io/?EIO=4&query=foo&sid=test-sid&transport=websocket",
		},
		{
			name:                       "HTTPS URL with malformed query",
			url:                        &url.URL{Scheme: "http", Host: "example.com", Path: "/socket.io/", RawQuery: "key1=value1&key2=%ZZ"},
			sid:                        "test-sid",
			expected:                   "ws://example.com/socket.io/?EIO=4&key1=value1&sid=test-sid&transport=websocket",
			expectedErrorLog:           "malformed query on url: %s",
			expectedErrorLogParameters: []interface{}{gomock.Any()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

			if tt.expectedErrorLog != "" {
				mockLogger.EXPECT().Errorf(tt.expectedErrorLog, tt.expectedErrorLogParameters...).Times(1)
			}

			transport := &Transport{
				url: tt.url,
				sid: tt.sid,
				log: mockLogger,
			}
			result := transport.buildWsUrl()
			assert.Equal(t, tt.expected, result.String())
		})
	}
}

func TestTransport_connectWebSocket_error(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
	mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	transport := &Transport{
		log: mockLogger,
		ws:  mockWS,
		url: &url.URL{Scheme: "http", Host: "example.com", Path: "/socket.io/"},
		ctx: ctx,
	}

	mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockWS.EXPECT().Dial(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("dial error"))

	err := transport.connectWebSocket()
	assert.Error(t, err)
}

func TestTransport_wsCloseError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
	mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	messages := make(chan []byte, 1)
	onClose := make(chan error, 1)
	transport := &Transport{
		log:         mockLogger,
		ws:          mockWS,
		url:         &url.URL{Scheme: "http", Host: "example.com", Path: "/socket.io/"},
		sid:         "test-sid",
		ctx:         ctx,
		messages:    messages,
		onClose:     onClose,
		stopPooling: make(chan struct{}, 1),
	}

	mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).DoAndReturn(func(format string, v ...interface{}) {
		errorStr := fmt.Sprintf(format, v...)
		assert.Contains(t, errorStr, "context canceled")
	})
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).DoAndReturn(func(format string, v ...interface{}) {
		errorStr := fmt.Sprintf(format, v...)
		assert.Contains(t, errorStr, "close error expected")
	})
	mockWS.EXPECT().Dial(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
		*message = []byte("test message")
		return nil
	}).AnyTimes()
	mockWS.EXPECT().Close().Return(errors.New("close error expected")).AnyTimes()

	err := transport.connectWebSocket()
	require.NoError(t, err)

	// Wait init
	time.Sleep(100 * time.Millisecond)

	// Check message received
	select {
	case msg := <-messages:
		assert.Equal(t, []byte("test message"), msg)
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for message")
	}

	// Stop transport
	cancel()
	//assert.NoError(t, err)

	select {
	case <-onClose:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for onClose")
	}
	time.Sleep(100 * time.Millisecond)
}

func TestTransport_connectWebSocket(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
	mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	messages := make(chan []byte, 1)
	onClose := make(chan error, 1)
	transport := &Transport{
		log:         mockLogger,
		ws:          mockWS,
		url:         &url.URL{Scheme: "http", Host: "example.com", Path: "/socket.io/"},
		sid:         "test-sid",
		ctx:         ctx,
		messages:    messages,
		onClose:     onClose,
		stopPooling: make(chan struct{}, 1),
	}

	mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockWS.EXPECT().Dial(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
		*message = []byte("test message")
		return nil
	}).AnyTimes()
	mockWS.EXPECT().Close().Return(nil).AnyTimes()

	err := transport.connectWebSocket()
	require.NoError(t, err)

	// Wait init
	time.Sleep(100 * time.Millisecond)

	// Check message received
	select {
	case msg := <-messages:
		assert.Equal(t, []byte("test message"), msg)
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for message")
	}

	// Stop transport
	err = transport.Stop()
	assert.NoError(t, err)

	select {
	case <-onClose:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for onClose")
	}
}

func TestTransport_wsReadLoop(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
	mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	messages := make(chan []byte, 1)
	onClose := make(chan error, 1)
	transport := &Transport{
		log:         mockLogger,
		ws:          mockWS,
		ctx:         ctx,
		messages:    messages,
		onClose:     onClose,
		stopPooling: make(chan struct{}, 1),
	}

	mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

	t.Run("message receive and stop pooling", func(t *testing.T) {

		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			*message = []byte("test message")
			return nil
		}).Times(1)
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			<-transport.ctx.Done()
			return nil
		}).Times(1)

		go func() {
			err := transport.wsReadLoop()
			assert.NoError(t, err)
		}()

		// Check message is received
		select {
		case msg := <-messages:
			assert.Equal(t, []byte("test message"), msg)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for message")
		}

		// Stop pooling
		transport.stopPooling <- struct{}{}
		select {
		case err := <-onClose:
			assert.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}
	})

	// Check WS Error
	t.Run("ws error", func(t *testing.T) {
		ErrExpected := errors.New("test error")
		mockWS.EXPECT().Receive(gomock.Any()).Return(ErrExpected)
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		go func() {
			err := transport.wsReadLoop()
			assert.Equal(t, ErrExpected, err)
		}()

		select {
		case err := <-onClose:
			assert.Error(t, err)
			assert.Equal(t, ErrExpected, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}
	})

	// Check context is canceled
	t.Run("context canceled", func(t *testing.T) {
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			<-transport.ctx.Done()
			return nil
		}).Times(1)

		go func() {
			err := transport.wsReadLoop()
			assert.Equal(t, context.Canceled, err)
		}()

		// Wait init
		time.Sleep(50 * time.Millisecond)

		// Check context is canceled
		cancel()
		select {
		case err := <-onClose:
			assert.Error(t, err)
			assert.Equal(t, context.Canceled, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}
	})
}

// TestTransport_wsReadLoop_FullChannel verifies that wsReadLoop does not block
// forever when the messages channel is full. When ctx is cancelled or a stop
// signal arrives, the loop must exit via the inner select, not spin forever.
//
// The barrier used here is the "receiveWs: %s" log message, which is emitted
// inside case C of the outer select, immediately before the inner select.
// Because it is the only 2-argument Debugf call in wsReadLoop, it can be
// intercepted without ordering ambiguity. Cancelling/stopping only after this
// barrier guarantees the test actually exercises the inner select's exit path.
func TestTransport_wsReadLoop_FullChannel(t *testing.T) {
	t.Parallel()

	t.Run("context cancelled while messages channel is full", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// receiveWsLogged fires when "receiveWs: %s" is logged — the exact point
		// between the outer select's case C and the inner select. Only then is
		// the goroutine guaranteed to be blocked on c.messages <- message inside
		// the inner select.
		receiveWsLogged := make(chan struct{})

		// Unbuffered messages channel ensures the inner select blocks.
		messages := make(chan []byte)
		onClose := make(chan error, 1)
		transport := &Transport{
			log:         mockLogger,
			ws:          mockWS,
			ctx:         ctx,
			messages:    messages,
			onClose:     onClose,
			stopPooling: make(chan struct{}, 1),
		}

		// "receiveWs: %s" is the only 2-arg Debugf in wsReadLoop; intercept it
		// specifically to avoid any ordering ambiguity with AnyTimes().
		mockLogger.EXPECT().Debugf("receiveWs: %s", gomock.Any()).DoAndReturn(
			func(format string, v ...any) { close(receiveWsLogged) },
		).Times(1)
		mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()

		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(msg *[]byte) error {
			*msg = []byte("msg")
			return nil
		}).Times(1)
		// A background goroutine may still be in Receive after the loop exits.
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(msg *[]byte) error {
			<-ctx.Done()
			return ctx.Err()
		}).AnyTimes()
		mockWS.EXPECT().Close().Return(nil).AnyTimes()

		go func() {
			_ = transport.wsReadLoop()
		}()

		// Wait until we are inside case C, right before the inner select.
		<-receiveWsLogged
		cancel()

		select {
		case err := <-onClose:
			assert.ErrorIs(t, err, context.Canceled)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("wsReadLoop blocked indefinitely when messages channel was full and context was cancelled")
		}
	})

	t.Run("stop signal while messages channel is full", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx := context.Background()

		receiveWsLogged := make(chan struct{})

		messages := make(chan []byte)
		onClose := make(chan error, 1)
		stopPooling := make(chan struct{}, 1)
		transport := &Transport{
			log:         mockLogger,
			ws:          mockWS,
			ctx:         ctx,
			messages:    messages,
			onClose:     onClose,
			stopPooling: stopPooling,
		}

		mockLogger.EXPECT().Debugf("receiveWs: %s", gomock.Any()).DoAndReturn(
			func(format string, v ...any) { close(receiveWsLogged) },
		).Times(1)
		mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()

		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(msg *[]byte) error {
			*msg = []byte("msg")
			return nil
		}).Times(1)
		mockWS.EXPECT().Close().Return(nil).AnyTimes()

		go func() {
			_ = transport.wsReadLoop()
		}()

		// Wait until we are inside case C, right before the inner select.
		<-receiveWsLogged
		stopPooling <- struct{}{}

		select {
		case err := <-onClose:
			assert.NoError(t, err)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("wsReadLoop blocked indefinitely when messages channel was full and stop was signalled")
		}
	})
}

func TestTransport_SendMessage(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
	mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

	transport := &Transport{
		log: mockLogger,
		ws:  mockWS,
	}

	mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockWS.EXPECT().Send(gomock.Any()).Return(nil)

	err := transport.SendMessage([]byte("test message"))
	assert.NoError(t, err)
}

func TestTransport_SendMessage_Error(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
	mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

	transport := &Transport{
		log: mockLogger,
		ws:  mockWS,
	}

	mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockWS.EXPECT().Send(gomock.Any()).Return(errors.New("send error"))

	err := transport.SendMessage([]byte("test message"))
	assert.Error(t, err)
	assert.Equal(t, "send error", err.Error())
}

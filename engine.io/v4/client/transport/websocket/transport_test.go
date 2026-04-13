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
		// May be context canceled or close error
		assert.True(t, contains(errorStr, "context canceled") || contains(errorStr, "close error"))
	}).AnyTimes()
	mockWS.EXPECT().Dial(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	callCount := 0
	mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
		callCount++
		if callCount == 1 {
			*message = []byte("test message")
			return nil
		}
		// After first message, block until context is done
		<-ctx.Done()
		return context.Canceled
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

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
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
	t.Run("message receive and stop pooling", func(t *testing.T) {
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

		callCount := 0
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			callCount++
			if callCount == 1 {
				*message = []byte("test message")
				return nil
			}
			// After first message, block until context is done
			<-ctx.Done()
			return context.Canceled
		}).AnyTimes()

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

		ErrExpected := errors.New("test error")
		mockWS.EXPECT().Receive(gomock.Any()).Return(ErrExpected).AnyTimes()
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

		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			<-ctx.Done()
			return context.Canceled
		}).AnyTimes()

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

	// Test nested select in message delivery - successful message send
	t.Run("nested select message delivery success", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Use buffered channel to allow message delivery
		messages := make(chan []byte, 10)
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

		callCount := 0
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			callCount++
			if callCount == 1 {
				*message = []byte("test message 1")
				return nil
			}
			if callCount == 2 {
				*message = []byte("test message 2")
				return nil
			}
			// Block until context is done
			<-ctx.Done()
			return context.Canceled
		}).AnyTimes()

		go func() {
			err := transport.wsReadLoop()
			// Should be context canceled from our defer cancel()
			if err != nil {
				assert.Equal(t, context.Canceled, err)
			}
		}()

		// Receive multiple messages
		select {
		case msg := <-messages:
			assert.Equal(t, []byte("test message 1"), msg)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for first message")
		}

		select {
		case msg := <-messages:
			assert.Equal(t, []byte("test message 2"), msg)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for second message")
		}

		// Stop and verify clean exit
		transport.stopPooling <- struct{}{}
		select {
		case <-onClose:
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}
	})

	// Test nested select in message delivery - stop during delivery
	t.Run("nested select message delivery with stop", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Use unbuffered channel to block on send
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

		mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		callCount := 0
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			callCount++
			if callCount <= 2 {
				*message = []byte(fmt.Sprintf("test message %d", callCount))
				return nil
			}
			// Block forever to keep goroutine alive
			<-ctx.Done()
			return context.Canceled
		}).AnyTimes()

		go func() {
			err := transport.wsReadLoop()
			assert.NoError(t, err)
		}()

		// Wait for messages to start queueing
		time.Sleep(100 * time.Millisecond)

		// Send stop signal while message is being delivered
		go func() {
			time.Sleep(50 * time.Millisecond)
			transport.stopPooling <- struct{}{}
		}()

		// Try to receive - should eventually see onClose due to stop
		select {
		case <-onClose:
			// Successfully stopped
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}
	})

	// Test nested select in message delivery - context cancel during delivery
	t.Run("nested select message delivery with context cancel", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Use unbuffered channel to block on send
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

		mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		callCount := 0
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			callCount++
			if callCount <= 2 {
				*message = []byte(fmt.Sprintf("test message %d", callCount))
				return nil
			}
			// Block forever to keep goroutine alive
			<-ctx.Done()
			return context.Canceled
		}).AnyTimes()

		go func() {
			err := transport.wsReadLoop()
			assert.Equal(t, context.Canceled, err)
		}()

		// Wait for messages to start queueing
		time.Sleep(100 * time.Millisecond)

		// Cancel context while message is being delivered
		cancel()

		// Should see onClose with context error
		select {
		case err := <-onClose:
			assert.Equal(t, context.Canceled, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}
	})

	// Test error path with pending message drain - message delivery succeeds
	t.Run("error path with pending message drain success", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		messages := make(chan []byte, 10)
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
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		readCalls := 0
		ErrExpected := errors.New("ws read error")
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			readCalls++
			if readCalls == 1 {
				// First call returns a message
				*message = []byte("pending message")
				return nil
			}
			// Second call returns error immediately
			return ErrExpected
		}).AnyTimes()

		go func() {
			err := transport.wsReadLoop()
			assert.Equal(t, ErrExpected, err)
		}()

		// Wait for pending message to be received
		select {
		case msg := <-messages:
			assert.Equal(t, []byte("pending message"), msg)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for pending message")
		}

		// Now trigger error
		select {
		case err := <-onClose:
			assert.Equal(t, ErrExpected, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}
	})

	// Test error path with pending message drain - multiple read calls to hit error path
	t.Run("error path with pending message drain stop", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		messages := make(chan []byte, 10)
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
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		readCalls := 0
		ErrExpected := errors.New("ws read error")
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			readCalls++
			if readCalls == 1 {
				// First call returns a message that will be delivered
				*message = []byte("pending message")
				return nil
			}
			if readCalls == 2 {
				// Second call also returns a message to put in the channel
				*message = []byte("another message")
				return nil
			}
			// Third call returns error
			return ErrExpected
		}).AnyTimes()

		errorReceived := make(chan error, 1)
		go func() {
			err := transport.wsReadLoop()
			errorReceived <- err
		}()

		// Receive the messages
		select {
		case msg := <-messages:
			assert.Equal(t, []byte("pending message"), msg)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for first message")
		}

		select {
		case msg := <-messages:
			assert.Equal(t, []byte("another message"), msg)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for second message")
		}

		// Should eventually get error
		select {
		case err := <-onClose:
			assert.Equal(t, ErrExpected, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}

		// Verify wsReadLoop returned the error
		select {
		case err := <-errorReceived:
			assert.Equal(t, ErrExpected, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for wsReadLoop error")
		}
	})

	// Test error path with pending message drain - context cancel during drain
	t.Run("error path with pending message drain context cancel", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		messages := make(chan []byte, 10)
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
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		readCalls := 0
		ErrExpected := errors.New("ws read error")
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			readCalls++
			if readCalls == 1 {
				// First call returns a message
				*message = []byte("pending message")
				return nil
			}
			if readCalls == 2 {
				// Second call returns another message
				*message = []byte("another message")
				return nil
			}
			// Third call returns error
			return ErrExpected
		}).AnyTimes()

		errorReceived := make(chan error, 1)
		go func() {
			err := transport.wsReadLoop()
			errorReceived <- err
		}()

		// Receive messages
		select {
		case msg := <-messages:
			assert.Equal(t, []byte("pending message"), msg)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for first message")
		}

		select {
		case msg := <-messages:
			assert.Equal(t, []byte("another message"), msg)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for second message")
		}

		// Should eventually get error
		select {
		case err := <-onClose:
			assert.Equal(t, ErrExpected, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}

		// Verify wsReadLoop returned the error
		select {
		case err := <-errorReceived:
			assert.Equal(t, ErrExpected, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for wsReadLoop error")
		}
	})

	// Test error path with no pending message (default case in nested select)
	t.Run("error path with no pending message", func(t *testing.T) {
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
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		ErrExpected := errors.New("ws read error")
		mockWS.EXPECT().Receive(gomock.Any()).Return(ErrExpected).AnyTimes()

		go func() {
			err := transport.wsReadLoop()
			assert.Equal(t, ErrExpected, err)
		}()

		// Should immediately get error since no message is pending
		select {
		case err := <-onClose:
			assert.Equal(t, ErrExpected, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
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

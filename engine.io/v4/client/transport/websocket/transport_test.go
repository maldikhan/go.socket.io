package engineio_v4_client_transport

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
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

	// Test message receive and multiple messages with buffered channel
	t.Run("multiple messages with buffered channel", func(t *testing.T) {
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

	// Test message delivery with context canceled (NEW code path)
	// This tests the case where context is cancelled while trying to send message
	t.Run("message delivery with context cancel", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Use unbuffered channel so send blocks
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

		delivered := false
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			if !delivered {
				delivered = true
				*message = []byte("blocked message")
				return nil
			}
			// Subsequent calls block until context is done
			<-ctx.Done()
			return context.Canceled
		}).AnyTimes()

		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		go func() {
			err := transport.wsReadLoop()
			assert.Equal(t, context.Canceled, err)
		}()

		// Wait for wsReadLoop to block on c.messages <- message
		// (messages channel is unbuffered with no reader)
		time.Sleep(50 * time.Millisecond)

		// Cancel context - should exit inner message delivery select via ctx.Done()
		cancel()

		// Should see onClose with context error
		select {
		case err := <-onClose:
			assert.Equal(t, context.Canceled, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}
	})

	// Test message delivery with stop signal (stopPooling while backpressured)
	t.Run("message delivery with stop signal", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Use unbuffered channel so send blocks
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

		mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		delivered := false
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			if !delivered {
				delivered = true
				*message = []byte("blocked message")
				return nil
			}
			<-ctx.Done()
			return context.Canceled
		}).AnyTimes()

		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		done := make(chan error, 1)
		go func() {
			done <- transport.wsReadLoop()
		}()

		// Wait for wsReadLoop to block on c.messages <- message
		time.Sleep(50 * time.Millisecond)

		// Send stop signal - should exit inner message delivery select
		stopPooling <- struct{}{}

		// Should exit with nil error
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for wsReadLoop to exit")
		}

		// Should see nil on onClose
		select {
		case err := <-onClose:
			assert.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}
	})

	// Test message delivery with stop signal when onClose is already full
	t.Run("message delivery stop with full onClose", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		messages := make(chan []byte)
		// Pre-fill onClose so the default branch is taken
		onClose := make(chan error, 1)
		onClose <- errors.New("pre-filled")
		stopPooling := make(chan struct{}, 1)
		transport := &Transport{
			log:         mockLogger,
			ws:          mockWS,
			ctx:         ctx,
			messages:    messages,
			onClose:     onClose,
			stopPooling: stopPooling,
		}

		mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		delivered := false
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			if !delivered {
				delivered = true
				*message = []byte("blocked message")
				return nil
			}
			<-ctx.Done()
			return context.Canceled
		}).AnyTimes()

		done := make(chan error, 1)
		go func() {
			done <- transport.wsReadLoop()
		}()

		time.Sleep(50 * time.Millisecond)
		stopPooling <- struct{}{}

		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for wsReadLoop to exit")
		}
	})

	// Test message delivery with context cancel when onClose is already full
	t.Run("message delivery cancel with full onClose", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())

		messages := make(chan []byte)
		// Pre-fill onClose so the default branch is taken
		onClose := make(chan error, 1)
		onClose <- errors.New("pre-filled")
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

		delivered := false
		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			if !delivered {
				delivered = true
				*message = []byte("blocked message")
				return nil
			}
			<-ctx.Done()
			return context.Canceled
		}).AnyTimes()

		done := make(chan error, 1)
		go func() {
			done <- transport.wsReadLoop()
		}()

		time.Sleep(50 * time.Millisecond)
		cancel()

		select {
		case err := <-done:
			assert.Equal(t, context.Canceled, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for wsReadLoop to exit")
		}
	})

	// Test error path - WebSocket read error
	t.Run("ws read error handling", func(t *testing.T) {
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
				*message = []byte("message before error")
				return nil
			}
			// Second call returns error immediately
			return ErrExpected
		}).AnyTimes()

		go func() {
			err := transport.wsReadLoop()
			assert.Equal(t, ErrExpected, err)
		}()

		// Wait for first message to be received
		select {
		case msg := <-messages:
			assert.Equal(t, []byte("message before error"), msg)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for message")
		}

		// Now error should be reported via onClose
		select {
		case err := <-onClose:
			assert.Equal(t, ErrExpected, err)
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for onClose")
		}
	})

	// Test non-blocking onClose on error (NEW code path - default send)
	t.Run("non-blocking onClose on error", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Use non-buffered onClose so we can see if send blocks
		messages := make(chan []byte, 1)
		onClose := make(chan error) // unbuffered - non-blocking send will not block
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

		// wsReadLoop should not block even though onClose is unbuffered
		// because the send is non-blocking (select with default)
		done := make(chan error, 1)
		go func() {
			err := transport.wsReadLoop()
			done <- err
		}()

		// Check that wsReadLoop returned quickly (non-blocking send)
		select {
		case err := <-done:
			assert.Equal(t, ErrExpected, err)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("wsReadLoop blocked on onClose send - should be non-blocking")
		}
	})

	// Test non-blocking onClose on context cancel (NEW code path - default send)
	t.Run("non-blocking onClose on context cancel", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())

		// Use unbuffered onClose
		messages := make(chan []byte, 1)
		onClose := make(chan error) // unbuffered - non-blocking send will skip
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

		done := make(chan error, 1)
		go func() {
			err := transport.wsReadLoop()
			done <- err
		}()

		// Give Receive goroutine time to block on ctx.Done()
		time.Sleep(50 * time.Millisecond)

		// Cancel context
		cancel()

		// wsReadLoop should return quickly (non-blocking send to onClose)
		select {
		case err := <-done:
			assert.Equal(t, context.Canceled, err)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("wsReadLoop blocked on onClose send - should be non-blocking")
		}
	})

	// Test non-blocking onClose on stopPooling (NEW code path - default send)
	t.Run("non-blocking onClose on stop", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWS := mock_engineio_v4_client_transport.NewMockWebSocket(ctrl)
		mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Use unbuffered onClose
		messages := make(chan []byte, 1)
		onClose := make(chan error) // unbuffered - non-blocking send will skip
		transport := &Transport{
			log:         mockLogger,
			ws:          mockWS,
			ctx:         ctx,
			messages:    messages,
			onClose:     onClose,
			stopPooling: make(chan struct{}),
		}

		mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		mockWS.EXPECT().Receive(gomock.Any()).DoAndReturn(func(message *[]byte) error {
			// Block until context is done
			<-ctx.Done()
			return context.Canceled
		}).AnyTimes()

		done := make(chan error, 1)
		go func() {
			err := transport.wsReadLoop()
			done <- err
		}()

		// Wait for Receive goroutine to block
		time.Sleep(50 * time.Millisecond)

		// Send stop signal
		transport.stopPooling <- struct{}{}

		// wsReadLoop should return quickly (non-blocking send to onClose)
		select {
		case err := <-done:
			assert.NoError(t, err) // stopPooling returns nil
		case <-time.After(100 * time.Millisecond):
			t.Fatal("wsReadLoop blocked on onClose send - should be non-blocking")
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

func TestConcurrentSetHandshakeAndBuildUrl(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mock_engineio_v4_client_transport.NewMockLogger(ctrl)
	mockLogger.EXPECT().Errorf(gomock.Any()).AnyTimes()

	transport := &Transport{
		log: mockLogger,
		url: &url.URL{Scheme: "http", Host: "example.com", Path: "/socket.io/"},
	}

	var wg sync.WaitGroup

	// Run SetHandshake and buildWsUrl concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(iter int) {
			defer wg.Done()
			handshake := &engineio_v4.HandshakeResponse{
				Sid: "sid-" + string(rune(iter)),
			}
			transport.SetHandshake(handshake)
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			url := transport.buildWsUrl()
			assert.NotNil(t, url)
		}()
	}

	wg.Wait()

	// If there was a race condition, the race detector would catch it
}

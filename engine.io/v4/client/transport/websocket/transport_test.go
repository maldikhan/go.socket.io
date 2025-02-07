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

	transport := &Transport{
		stopPooling: make(chan struct{}, 1),
	}
	err := transport.Stop()
	assert.NoError(t, err)
	assert.Len(t, transport.stopPooling, 1)
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

	tests := []struct {
		name     string
		url      *url.URL
		sid      string
		expected string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &Transport{
				url: tt.url,
				sid: tt.sid,
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

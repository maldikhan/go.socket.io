package socketio_v5_client

import (
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	mocks "github.com/maldikhan/go.socket.io/socket.io/v5/client/mocks"
)

//go:generate mockgen -destination=mocks_test.go -package=socketio_v5_client github.com/maldikhan/go.socket.io/socket.io/v5/client/emit Parser,Logger

func TestClientOn(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockParser := mocks.NewMockParser(ctrl)

	client := &Client{
		defaultNs: &namespace{
			client:   &Client{parser: mockParser},
			handlers: make(map[string][]func([]interface{})),
		},
	}

	handler := func(args ...interface{}) {}
	wrappedHandler := func([]interface{}) {}

	mockParser.EXPECT().WrapCallback(gomock.Any()).Return(wrappedHandler)

	client.On("test_event", handler)

	assert.Len(t, client.defaultNs.handlers["test_event"], 1)
	assert.NotNil(t, client.defaultNs.handlers["test_event"][0])
}

func TestNamespaceOn(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockParser := mocks.NewMockParser(ctrl)

	ns := &namespace{
		client:   &Client{parser: mockParser},
		handlers: make(map[string][]func([]interface{})),
	}

	handler := func(args ...interface{}) {}
	wrappedHandler := func([]interface{}) {}

	mockParser.EXPECT().WrapCallback(gomock.Any()).Return(wrappedHandler)

	ns.On("test_event", handler)

	assert.Len(t, ns.handlers["test_event"], 1)
	assert.NotNil(t, ns.handlers["test_event"][0])
}

func TestClientOnMessage(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockParser := mocks.NewMockParser(ctrl)
	mockLogger := mocks.NewMockLogger(ctrl)

	client := &Client{
		parser:     mockParser,
		logger:     mockLogger,
		namespaces: make(map[string]*namespace),
	}

	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()

	t.Run("Parse Error", func(t *testing.T) {
		mockParser.EXPECT().Parse(gomock.Any()).Return(nil, assert.AnError)
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any())

		client.onMessage([]byte("invalid data"))
	})

	t.Run("PacketEvent", func(t *testing.T) {
		ns := &namespace{handlers: make(map[string][]func([]interface{}))}
		client.namespaces[""] = ns

		msg := &socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			Event: &socketio_v5.Event{Name: "test_event"},
		}

		mockParser.EXPECT().Parse(gomock.Any()).Return(msg, nil)

		client.onMessage([]byte("valid data"))
	})

	t.Run("PacketAck", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:  socketio_v5.PacketAck,
			Event: &socketio_v5.Event{},
			AckId: new(int),
		}

		mockParser.EXPECT().Parse(gomock.Any()).Return(msg, nil)

		client.onMessage([]byte("valid data"))
	})

	t.Run("PacketConnectError", func(t *testing.T) {
		errorMsg := "connection error"
		msg := &socketio_v5.Message{
			Type:         socketio_v5.PacketConnectError,
			ErrorMessage: &errorMsg,
		}

		mockParser.EXPECT().Parse(gomock.Any()).Return(msg, nil)
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any())

		client.onMessage([]byte("valid data"))
	})
}

func TestClientHandleEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)

	client := &Client{
		logger: mockLogger,
	}

	ns := &namespace{
		handlers: make(map[string][]func([]interface{})),
	}

	t.Run("No Handlers", func(t *testing.T) {
		mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any())

		client.handleEvent(ns, &socketio_v5.Event{Name: "test_event"})
	})

	t.Run("With Handlers", func(t *testing.T) {
		handlerCalled := make(chan bool)
		ns.handlers["test_event"] = []func([]interface{}){
			func(args []interface{}) {
				handlerCalled <- true
			},
		}

		client.handleEvent(ns, &socketio_v5.Event{Name: "test_event"})

		assert.True(t, <-handlerCalled)
	})
}

func TestClientHandleAck(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)

	client := &Client{
		logger:       mockLogger,
		ackCallbacks: make(map[int]func([]interface{})),
		mutex:        sync.RWMutex{},
	}

	t.Run("No Callback", func(t *testing.T) {
		mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any())

		client.handleAck(&socketio_v5.Event{}, 1)
	})

	t.Run("With Callback", func(t *testing.T) {
		callbackCalled := make(chan bool)
		client.ackCallbacks[1] = func(args []interface{}) {
			callbackCalled <- true
		}

		client.handleAck(&socketio_v5.Event{}, 1)

		assert.True(t, <-callbackCalled)
		assert.Len(t, client.ackCallbacks, 0)
	})
}

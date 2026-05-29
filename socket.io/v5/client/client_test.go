package socketio_v5_client

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	mocks "github.com/maldikhan/go.socket.io/socket.io/v5/client/mocks"
)

func TestSetHandshakeData(t *testing.T) {
	client := &Client{
		handshakeData: make(map[string]interface{}),
	}

	testData := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
	}

	client.SetHandshakeData(testData)

	if !reflect.DeepEqual(client.handshakeData, testData) {
		t.Errorf("SetHandshakeData() did not set the handshake data correctly. Got %v, want %v", client.handshakeData, testData)
	}
}

func TestConnectSocketIO(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockEngineIO := mocks.NewMockEngineIOClient(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockLogger := mocks.NewMockLogger(ctrl)

	client := &Client{
		engineio:      mockEngineIO,
		parser:        mockParser,
		logger:        mockLogger,
		handshakeData: map[string]interface{}{"foo": "bar"},
	}

	expectedPacket := &socketio_v5.Message{
		Type:    socketio_v5.PacketConnect,
		Payload: client.handshakeData,
	}

	mockParser.EXPECT().Serialize(gomock.Eq(expectedPacket)).Return([]byte("encoded_packet"), nil)
	mockEngineIO.EXPECT().Send(gomock.Any()).Return(nil)

	client.connectSocketIO(nil)

	mockParser.EXPECT().Serialize(gomock.Eq(expectedPacket)).Return(nil, errors.New("send error"))
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).Do(func(format string, v ...interface{}) {
		str := fmt.Sprintf(format, v...)
		assert.Contains(t, str, "send error")
	})

	client.connectSocketIO(nil)
}

func TestConnect(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockEngineIO := mocks.NewMockEngineIOClient(ctrl)

	client := &Client{
		engineio: mockEngineIO,
	}

	t.Run("Connect", func(t *testing.T) {
		ctx := context.Background()
		mockEngineIO.EXPECT().Connect(ctx).Return(nil)

		err := client.Connect(ctx)

		if err != nil {
			t.Errorf("Connect() returned an error: %v", err)
		}

		if client.ctx != ctx {
			t.Errorf("Connect() did not set the context correctly")
		}
	})

	t.Run("Connect with callbacks", func(t *testing.T) {
		ctx := context.Background()
		mockEngineIO.EXPECT().Connect(ctx).Return(nil)

		parser := mocks.NewMockParser(ctrl)
		parser.EXPECT().WrapCallback(gomock.Any()).Return(func(in []interface{}) {}).Times(2)

		client.parser = parser
		client.defaultNs = &namespace{
			client:   client,
			handlers: make(map[string][]func([]interface{})),
		}

		err := client.Connect(ctx, func(arg interface{}) {}, func(arg interface{}) {})

		if err != nil {
			t.Errorf("Connect() returned an error: %v", err)
		}

		if client.ctx != ctx {
			t.Errorf("Connect() did not set the context correctly")
		}

		assert.Len(t, client.defaultNs.handlers["connect"], 2)
	})
}

func TestNamespace(t *testing.T) {
	client := &Client{
		namespaces: make(map[string]*namespace),
	}

	testCases := []struct {
		name      string
		callTwice bool
	}{
		{"testNamespace", false},
		{"/", false},
		{"testNamespace2", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ns := client.namespace(tc.name)

			if ns == nil {
				t.Errorf("namespace() returned nil for name %s", tc.name)
				return
			}

			if ns.name != tc.name {
				t.Errorf("namespace() returned namespace with incorrect name. Got %s, want %s", ns.name, tc.name)
			}

			if ns.client != client {
				t.Errorf("namespace() returned namespace with incorrect client reference")
			}

			if len(ns.handlers) != 0 {
				t.Errorf("namespace() returned namespace with non-empty handlers")
			}

			if _, exists := client.namespaces[tc.name]; !exists {
				t.Errorf("namespace() did not add the namespace to the client's namespaces map")
			}

			if tc.callTwice {
				ns2 := client.namespace(tc.name)
				if ns2 != ns {
					t.Errorf("namespace() returned a different instance when called twice with the same name")
				}
			}
		})
	}
}

func TestClose(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockEngineIO := mocks.NewMockEngineIOClient(ctrl)

	client := &Client{
		engineio: mockEngineIO,
	}

	mockEngineIO.EXPECT().Close().Return(nil)

	err := client.Close()

	if err != nil {
		t.Errorf("Close() returned an error: %v", err)
	}
}

func TestConcurrentConnectAndEmit(t *testing.T) {
	// This test ensures no data race when Connect() writes c.ctx
	// while Emit() reads it concurrently
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockEngineIO := mocks.NewMockEngineIOClient(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockLogger := mocks.NewMockLogger(ctrl)

	ctx := context.Background()

	client := &Client{
		engineio:      mockEngineIO,
		parser:        mockParser,
		logger:        mockLogger,
		ctx:           ctx, // Initialize ctx
		namespaces:    make(map[string]*namespace),
		ackCallbacks:  make(map[int]func([]interface{})),
		handshakeData: make(map[string]interface{}),
	}

	// Create default namespace with a pre-closed waitConnected channel
	// so Emit exercises the ctx snapshot path without blocking
	alreadyConnected := make(chan struct{})
	close(alreadyConnected)
	client.defaultNs = &namespace{
		client:        client,
		name:          "/",
		handlers:      make(map[string][]func([]interface{})),
		waitConnected: alreadyConnected,
	}

	mockEngineIO.EXPECT().Connect(ctx).Return(nil).AnyTimes()
	mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte("packet"), nil).AnyTimes()
	mockEngineIO.EXPECT().Send(gomock.Any()).Return(nil).AnyTimes()

	// Simulate concurrent Connect and Emit operations
	done := make(chan bool, 2)
	go func() {
		// Simulate Connect writing to c.ctx
		for i := 0; i < 10; i++ {
			_ = client.Connect(ctx)
		}
		done <- true
	}()

	go func() {
		// Simulate Emit reading from c.ctx
		for i := 0; i < 10; i++ {
			_ = client.Emit("test_event")
		}
		done <- true
	}()

	<-done
	<-done
}

func TestSetHandshakeDataMakesaCopy(t *testing.T) {
	// This test ensures SetHandshakeData makes a copy to prevent mutation
	client := &Client{
		handshakeData: make(map[string]interface{}),
		mutex:         sync.RWMutex{},
	}

	originalData := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
	}

	client.SetHandshakeData(originalData)

	// Mutate the original data
	originalData["key1"] = "mutated"

	// Verify that client.handshakeData is not affected
	client.mutex.RLock()
	if client.handshakeData["key1"] != "value1" {
		t.Errorf("SetHandshakeData() did not make a copy. Got %v, want value1", client.handshakeData["key1"])
	}
	client.mutex.RUnlock()
}

func TestConcurrentSetHandshakeDataAndConnect(t *testing.T) {
	// This test ensures no data race when SetHandshakeData writes c.handshakeData
	// while connectSocketIO reads it concurrently
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockParser := mocks.NewMockParser(ctrl)
	mockLogger := mocks.NewMockLogger(ctrl)
	mockEngineIO := mocks.NewMockEngineIOClient(ctrl)

	client := &Client{
		parser:        mockParser,
		logger:        mockLogger,
		engineio:      mockEngineIO,
		handshakeData: make(map[string]interface{}),
		mutex:         sync.RWMutex{},
	}

	mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte("packet"), nil).AnyTimes()
	mockEngineIO.EXPECT().Send(gomock.Any()).Return(nil).AnyTimes()

	done := make(chan bool, 2)
	go func() {
		// Simulate concurrent SetHandshakeData calls
		for i := 0; i < 10; i++ {
			data := map[string]interface{}{
				"key":   "value",
				"index": i,
			}
			client.SetHandshakeData(data)
		}
		done <- true
	}()

	go func() {
		// Simulate concurrent connectSocketIO calls
		for i := 0; i < 10; i++ {
			client.connectSocketIO(nil)
		}
		done <- true
	}()

	<-done
	<-done
}

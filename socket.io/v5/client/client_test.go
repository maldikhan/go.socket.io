package socketio_v5_client

import (
	"context"
	"reflect"
	"testing"

	"github.com/golang/mock/gomock"

	socketio_v5 "maldikhan/go.socket.io/socket.io/v5"
	mocks "maldikhan/go.socket.io/socket.io/v5/client/mocks"
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

	client := &Client{
		engineio:      mockEngineIO,
		parser:        mockParser,
		handshakeData: map[string]interface{}{"foo": "bar"},
	}

	expectedPacket := &socketio_v5.Message{
		Type:    socketio_v5.PacketConnect,
		Payload: client.handshakeData,
	}

	mockParser.EXPECT().Serialize(gomock.Eq(expectedPacket)).Return([]byte("encoded_packet"), nil)
	mockEngineIO.EXPECT().Send(gomock.Any()).Return(nil)

	client.connectSocketIO(nil)
}

func TestConnect(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockEngineIO := mocks.NewMockEngineIOClient(ctrl)

	client := &Client{
		engineio: mockEngineIO,
	}

	ctx := context.Background()
	mockEngineIO.EXPECT().Connect(ctx).Return(nil)

	err := client.Connect(ctx)

	if err != nil {
		t.Errorf("Connect() returned an error: %v", err)
	}

	if client.ctx != ctx {
		t.Errorf("Connect() did not set the context correctly")
	}
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

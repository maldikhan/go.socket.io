package engineio_v4_client_transport

import (
	"errors"
	"net/url"
	"reflect"
	"testing"

	"github.com/golang/mock/gomock"

	mocks "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket/mocks"
	"github.com/maldikhan/go.socket.io/utils"
	ws_native "github.com/maldikhan/go.socket.io/websocket/native"
)

func TestNewTransport(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockWebSocket := mocks.NewMockWebSocket(ctrl)

	testOrigin, _ := url.Parse("http://example.com")

	tests := []struct {
		name    string
		options []EngineTransportOption
		want    *Transport
		wantErr bool
	}{
		{
			name: "Default options",
			want: &Transport{
				log:         &utils.DefaultLogger{},
				ws:          &ws_native.WebSocketConnection{},
				stopPooling: make(chan struct{}, 1),
			},
		},
		{
			name: "With custom logger",
			options: []EngineTransportOption{
				WithLogger(mockLogger),
			},
			want: &Transport{
				log:         mockLogger,
				ws:          &ws_native.WebSocketConnection{},
				stopPooling: make(chan struct{}, 1),
			},
		},
		{
			name: "With nil logger",
			options: []EngineTransportOption{
				WithLogger(nil),
			},
			wantErr: true,
		},
		{
			name: "With custom WebSocket",
			options: []EngineTransportOption{
				WithWebSocket(mockWebSocket),
			},
			want: &Transport{
				log:         &utils.DefaultLogger{},
				ws:          mockWebSocket,
				stopPooling: make(chan struct{}, 1),
			},
		},
		{
			name: "With nil WebSocket",
			options: []EngineTransportOption{
				WithWebSocket(nil),
			},
			wantErr: true,
		},
		{
			name: "With custom origin",
			options: []EngineTransportOption{
				WithOrigin(testOrigin),
			},
			want: &Transport{
				log:         &utils.DefaultLogger{},
				ws:          &ws_native.WebSocketConnection{},
				origin:      testOrigin,
				stopPooling: make(chan struct{}, 1),
			},
		},
		{
			name: "With option error",
			options: []EngineTransportOption{
				func(c *Transport) error {
					return errors.New("oops")
				},
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewTransport(tt.options...)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewTransport() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got == nil || tt.want == nil {
				if got == tt.want {
					return
				}
				t.Errorf("NewTransport() got = %v, want %v", got, tt.want)
				return
			}

			// Проверяем тип и значение log
			if tt.name == "With custom logger" {
				if got.log != mockLogger {
					t.Errorf("NewTransport() log is not the expected mock logger")
				}
			} else {
				if _, ok := got.log.(*utils.DefaultLogger); !ok {
					t.Errorf("NewTransport() log is not DefaultLogger when it should be")
				}
			}

			// Проверяем тип и значение ws
			if tt.name == "With custom WebSocket" {
				if got.ws != mockWebSocket {
					t.Errorf("NewTransport() ws is not the expected mock WebSocket")
				}
			} else {
				if _, ok := got.ws.(*ws_native.WebSocketConnection); !ok {
					t.Errorf("NewTransport() ws is not WebSocketConnection when it should be")
				}
			}

			// Проверяем origin
			if tt.name == "With custom origin" {
				if !reflect.DeepEqual(got.origin, testOrigin) {
					t.Errorf("NewTransport() origin = %v, want %v", got.origin, testOrigin)
				}
			}

			// Проверяем stopPooling
			if cap(got.stopPooling) != cap(tt.want.stopPooling) {
				t.Errorf("NewTransport() stopPooling capacity = %d, want %d", cap(got.stopPooling), cap(tt.want.stopPooling))
			}
		})
	}
}

func TestWithLogger(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	option := WithLogger(mockLogger)

	transport := &Transport{}
	err := option(transport)

	if err != nil {
		t.Errorf("WithLogger() returned an error: %v", err)
	}

	if transport.log != mockLogger {
		t.Errorf("WithLogger() did not set the logger correctly")
	}
}

func TestWithWebSocket(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWebSocket := mocks.NewMockWebSocket(ctrl)
	option := WithWebSocket(mockWebSocket)

	transport := &Transport{}
	err := option(transport)

	if err != nil {
		t.Errorf("WithWebSocket() returned an error: %v", err)
	}

	if transport.ws != mockWebSocket {
		t.Errorf("WithWebSocket() did not set the WebSocket correctly")
	}
}

func TestWithOrigin(t *testing.T) {
	testOrigin, _ := url.Parse("http://example.com")
	option := WithOrigin(testOrigin)

	transport := &Transport{}
	err := option(transport)

	if err != nil {
		t.Errorf("WithOrigin() returned an error: %v", err)
	}

	if !reflect.DeepEqual(transport.origin, testOrigin) {
		t.Errorf("WithOrigin() did not set the origin correctly")
	}
}

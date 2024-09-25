package engineio_v4_client_transport

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/golang/mock/gomock"

	mocks "maldikhan/go.socket.io/engine.io/v4/client/transport/polling/mocks"
	"maldikhan/go.socket.io/utils"
)

func TestNewTransport(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockHTTPClient := mocks.NewMockHttpClient(ctrl)

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
				httpClient:  &http.Client{},
				pinger:      time.NewTicker(10 * time.Second),
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
				httpClient:  &http.Client{},
				pinger:      time.NewTicker(10 * time.Second),
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
			name: "With custom HTTP client",
			options: []EngineTransportOption{
				WithHTTPClient(mockHTTPClient),
			},
			want: &Transport{
				log:         &utils.DefaultLogger{},
				httpClient:  mockHTTPClient,
				pinger:      time.NewTicker(10 * time.Second),
				stopPooling: make(chan struct{}, 1),
			},
		},
		{
			name: "With nil HTTP client",
			options: []EngineTransportOption{
				WithHTTPClient(nil),
			},
			wantErr: true,
		},
		{
			name: "With custom pinger",
			options: []EngineTransportOption{
				WithDefaultPinger(time.NewTicker(5 * time.Second)),
			},
			want: &Transport{
				log:         &utils.DefaultLogger{},
				httpClient:  &http.Client{},
				pinger:      time.NewTicker(5 * time.Second),
				stopPooling: make(chan struct{}, 1),
			},
		},
		{
			name: "With nil pinger",
			options: []EngineTransportOption{
				WithDefaultPinger(nil),
			},
			wantErr: true,
		},
		{
			name: "With option error",
			options: []EngineTransportOption{
				func(c *Transport) error {
					return errors.New("oops")
				},
			},
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

			// Проверяем тип и значение httpClient
			if tt.name == "With custom HTTP client" {
				if got.httpClient != mockHTTPClient {
					t.Errorf("NewTransport() httpClient is not the expected mock client")
				}
			} else {
				if _, ok := got.httpClient.(*http.Client); !ok {
					t.Errorf("NewTransport() httpClient is not http.Client when it should be")
				}
			}

			// Проверяем pinger
			if got.pinger == nil {
				t.Errorf("NewTransport() pinger is nil")
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

func TestWithHTTPClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockHttpClient(ctrl)
	option := WithHTTPClient(mockClient)

	transport := &Transport{}
	err := option(transport)

	if err != nil {
		t.Errorf("WithHTTPClient() returned an error: %v", err)
	}

	if transport.httpClient != mockClient {
		t.Errorf("WithHTTPClient() did not set the HTTP client correctly")
	}
}

func TestWithDefaultPinger(t *testing.T) {
	customPinger := time.NewTicker(5 * time.Second)
	option := WithDefaultPinger(customPinger)

	transport := &Transport{
		pinger: time.NewTicker(10 * time.Second),
	}
	err := option(transport)

	if err != nil {
		t.Errorf("WithDefaultPinger() returned an error: %v", err)
	}

	if transport.pinger != customPinger {
		t.Errorf("WithDefaultPinger() did not set the pinger correctly")
	}
}

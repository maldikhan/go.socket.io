package engineio_v4_client

import (
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	mocks "github.com/maldikhan/go.socket.io/engine.io/v4/client/mocks"
	engineio_v4_client_transport_polling "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/polling"
	engineio_v4_client_transport_ws "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket"
	engineio_v4_parser "github.com/maldikhan/go.socket.io/engine.io/v4/parser"
	"github.com/maldikhan/go.socket.io/utils"
)

func TestNewClientEdges(t *testing.T) {
	t.Run("Empty url", func(t *testing.T) {
		_, err := NewClient()
		assert.Error(t, err)
	})

	t.Run("Empty logger", func(t *testing.T) {
		_, err := NewClient(WithRawURL("http://example.com"), WithLogger(nil))
		assert.Error(t, err)
	})

	t.Run("Empty parser", func(t *testing.T) {
		_, err := NewClient(WithRawURL("http://example.com"), WithParser(nil))
		assert.Error(t, err)
	})

	t.Run("Transport fallback", func(t *testing.T) {
		client, err := NewClient(
			WithRawURL("http://example.com"),
			WithSupportedTransports([]Transport{&engineio_v4_client_transport_polling.Transport{}}),
		)
		assert.NoError(t, err)
		assert.Equal(t, client.transport, &engineio_v4_client_transport_polling.Transport{})
	})

	t.Run("Transport fallback", func(t *testing.T) {
		client, err := NewClient(
			WithRawURL("http://example.com"),
			WithSupportedTransports([]Transport{&engineio_v4_client_transport_ws.Transport{}}),
		)
		assert.NoError(t, err)
		assert.Equal(t, client.transport, &engineio_v4_client_transport_ws.Transport{})
	})
}

func TestNewClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockTransport := mocks.NewMockTransport(ctrl)

	testURL, _ := url.Parse("http://example.com/socket.io/")

	tests := []struct {
		name    string
		options []EngineClientOption
		want    *Client
		wantErr bool
	}{
		{
			name: "Default options",
			options: []EngineClientOption{
				WithURL(testURL),
			},
			want: &Client{
				url:               testURL,
				log:               &utils.DefaultLogger{},
				parser:            &engineio_v4_parser.EngineIOV4Parser{},
				reconnectAttempts: 5,
				reconnectWait:     5 * time.Second,
				pingInterval:      time.NewTicker(10 * time.Second),
				stopPooling:       make(chan struct{}, 1),
				transportClosed:   make(chan error, 1),
				supportedTransports: map[engineio_v4.EngineIOTransport]Transport{
					engineio_v4.TransportWebsocket: &engineio_v4_client_transport_ws.Transport{},
					engineio_v4.TransportPolling:   &engineio_v4_client_transport_polling.Transport{},
				},
			},
		},
		{
			name: "With custom options",
			options: []EngineClientOption{
				WithURL(testURL),
				WithLogger(mockLogger),
				WithParser(mockParser),
				WithTransport(mockTransport),
				WithReconnectAttempts(3),
				WithReconnectWait(3 * time.Second),
			},
			want: &Client{
				url:               testURL,
				log:               mockLogger,
				parser:            mockParser,
				transport:         mockTransport,
				reconnectAttempts: 3,
				reconnectWait:     3 * time.Second,
				pingInterval:      time.NewTicker(10 * time.Second),
				stopPooling:       make(chan struct{}, 1),
				transportClosed:   make(chan error, 1),
				supportedTransports: map[engineio_v4.EngineIOTransport]Transport{
					engineio_v4.TransportWebsocket: &engineio_v4_client_transport_ws.Transport{},
					engineio_v4.TransportPolling:   &engineio_v4_client_transport_polling.Transport{},
				},
			},
		},
		{
			name:    "Without URL",
			options: []EngineClientOption{},
			wantErr: true,
		},
		{
			name: "With empty transports",
			options: []EngineClientOption{
				WithSupportedTransports([]Transport{nil}),
			},
			wantErr: true,
		},
		{
			name: "With invalid URL scheme",
			options: []EngineClientOption{
				WithRawURL("ftp://example.com"),
			},
			wantErr: true,
		},
		{
			name: "With option error",
			options: []EngineClientOption{
				WithURL(testURL),
				func(*Client) error { return errors.New("oops") },
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewClient(tt.options...)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if got == nil || tt.want == nil {
				require.Equal(t, got, tt.want)
				return
			}

			// Common fields check
			assert.Equal(t, got.url, tt.want.url)
			assert.IsType(t, got.log, tt.want.log)
			assert.IsType(t, got.parser, tt.want.parser)
			assert.Equal(t, got.reconnectAttempts, tt.want.reconnectAttempts)
			assert.Equal(t, got.reconnectWait, tt.want.reconnectWait)
			assert.Equal(t, cap(got.stopPooling), cap(tt.want.stopPooling))
			assert.IsType(t, cap(got.transportClosed), cap(tt.want.transportClosed))
			assert.Len(t, got.supportedTransports, len(tt.want.supportedTransports))

			for k, v := range tt.want.supportedTransports {
				assert.Contains(t, got.supportedTransports, k)
				assert.IsType(t, v, got.supportedTransports[k])
			}
			assert.Len(t, got.supportedTransports, len(tt.want.supportedTransports))

			// Default transport
			if tt.want.transport == nil {
				assert.Equal(t, got.supportedTransports[engineio_v4.TransportPolling], got.transport)
			} else {
				assert.Equal(t, tt.want.transport, got.transport)
			}
		})
	}
}

func TestWithURL(t *testing.T) {
	testCases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"Valid HTTP URL", "http://example.com", false},
		{"Valid HTTPS URL", "https://example.com", false},
		{"Valid WS URL", "ws://example.com", false},
		{"Valid WSS URL", "wss://example.com", false},
		{"Invalid scheme", "ftp://example.com", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parsedURL, _ := url.Parse(tc.url)
			option := WithURL(parsedURL)
			client := &Client{}
			err := option(client)

			if (err != nil) != tc.wantErr {
				t.Errorf("WithURL() error = %v, wantErr %v", err, tc.wantErr)
				return
			}

			if !tc.wantErr {
				if client.url == nil {
					t.Errorf("WithURL() did not set the URL")
				}
				if client.url.Query().Get("EIO") != "4" {
					t.Errorf("WithURL() did not set EIO query parameter")
				}
			}
		})
	}
}

func TestWithRawURL(t *testing.T) {
	testCases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"Valid HTTP URL", "http://example.com", false},
		{"Valid HTTPS URL", "https://example.com", false},
		{"Valid WS URL", "ws://example.com", false},
		{"Valid WSS URL", "wss://example.com", false},
		{"Invalid URL", "://invalid", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			option := WithRawURL(tc.url)
			client := &Client{}
			err := option(client)

			if (err != nil) != tc.wantErr {
				t.Errorf("WithRawURL() error = %v, wantErr %v", err, tc.wantErr)
				return
			}

			if !tc.wantErr {
				if client.url == nil {
					t.Errorf("WithRawURL() did not set the URL")
				}
			}
		})
	}
}

func TestWithLogger(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	option := WithLogger(mockLogger)
	client := &Client{}
	err := option(client)

	if err != nil {
		t.Errorf("WithLogger() returned an error: %v", err)
	}

	if client.log != mockLogger {
		t.Errorf("WithLogger() did not set the logger correctly")
	}
}

func TestWithTransport(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTransport := mocks.NewMockTransport(ctrl)
	option := WithTransport(mockTransport)
	client := &Client{}
	err := option(client)

	if err != nil {
		t.Errorf("WithTransport() returned an error: %v", err)
	}

	if client.transport != mockTransport {
		t.Errorf("WithTransport() did not set the transport correctly")
	}
}

func TestWithSupportedTransports(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTransport1 := mocks.NewMockTransport(ctrl)
	mockTransport2 := mocks.NewMockTransport(ctrl)

	mockTransport1.EXPECT().Transport().Return(engineio_v4.TransportWebsocket)
	mockTransport2.EXPECT().Transport().Return(engineio_v4.TransportPolling)

	transports := []Transport{mockTransport1, mockTransport2}
	option := WithSupportedTransports(transports)
	client := &Client{supportedTransports: make(map[engineio_v4.EngineIOTransport]Transport)}
	err := option(client)

	if err != nil {
		t.Errorf("WithSupportedTransports() returned an error: %v", err)
	}

	if len(client.supportedTransports) != 2 {
		t.Errorf("WithSupportedTransports() did not set the correct number of transports")
	}

	if client.supportedTransports[engineio_v4.TransportWebsocket] != mockTransport1 {
		t.Errorf("WithSupportedTransports() did not set the websocket transport correctly")
	}

	if client.supportedTransports[engineio_v4.TransportPolling] != mockTransport2 {
		t.Errorf("WithSupportedTransports() did not set the polling transport correctly")
	}
}

func TestWithParser(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockParser := mocks.NewMockParser(ctrl)
	option := WithParser(mockParser)
	client := &Client{}
	err := option(client)

	if err != nil {
		t.Errorf("WithParser() returned an error: %v", err)
	}

	if client.parser != mockParser {
		t.Errorf("WithParser() did not set the parser correctly")
	}
}

func TestWithReconnectAttempts(t *testing.T) {
	testCases := []struct {
		name     string
		attempts int
	}{
		{"Zero attempts", 0},
		{"Positive attempts", 5},
		{"Negative attempts", -1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			option := WithReconnectAttempts(tc.attempts)
			client := &Client{}

			err := option(client)

			if err != nil {
				t.Errorf("WithReconnectAttempts() returned an error: %v", err)
			}

			if client.reconnectAttempts != tc.attempts {
				t.Errorf("WithReconnectAttempts() did not set the correct number of attempts. Got %d, want %d", client.reconnectAttempts, tc.attempts)
			}
		})
	}
}

func TestWithReconnectWait(t *testing.T) {
	testCases := []struct {
		name string
		wait time.Duration
	}{
		{"Zero wait", 0},
		{"Positive wait", 5 * time.Second},
		{"Negative wait", -1 * time.Second},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			option := WithReconnectWait(tc.wait)
			client := &Client{}

			err := option(client)

			if err != nil {
				t.Errorf("WithReconnectWait() returned an error: %v", err)
			}

			if client.reconnectWait != tc.wait {
				t.Errorf("WithReconnectWait() did not set the correct wait time. Got %v, want %v", client.reconnectWait, tc.wait)
			}
		})
	}
}

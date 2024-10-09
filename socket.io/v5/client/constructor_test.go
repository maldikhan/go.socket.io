package socketio_v5_client

import (
	"errors"
	"net/url"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mocks "github.com/maldikhan/go.socket.io/socket.io/v5/client/mocks"
	socketio_v5_parser "github.com/maldikhan/go.socket.io/socket.io/v5/parser/default"
	"github.com/maldikhan/go.socket.io/utils"
)

func TestNewClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockTimer := mocks.NewMockTimer(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockEngineIOClient := mocks.NewMockEngineIOClient(ctrl)
	mockNamespace := &namespace{
		name: "/new",
	}

	testURL, _ := url.Parse("http://example.com/socket.io/")

	tests := []struct {
		name    string
		options []ClientOption
		want    *Client
		wantErr bool
	}{
		{
			name: "Default options with URL",
			options: []ClientOption{
				WithURL(testURL),
			},
			want: &Client{
				handshakeData: make(map[string]interface{}),
				namespaces: map[string]*namespace{
					"/": {name: "/"},
				},
				defaultNs: &namespace{
					name: "/",
				},
				ackCallbacks: make(map[int]func([]interface{})),
				logger:       &utils.DefaultLogger{},
				timer:        &utils.DefaultTimer{},
				parser:       &socketio_v5_parser.SocketIOV5DefaultParser{},
			},
		},
		{
			name: "Custom options",
			options: []ClientOption{
				WithEngineIOClient(mockEngineIOClient),
				WithDefaultNamespace(mockNamespace.name),
				WithLogger(mockLogger),
				WithTimer(mockTimer),
				WithParser(mockParser),
			},
			want: &Client{
				handshakeData: make(map[string]interface{}),
				ackCallbacks:  make(map[int]func([]interface{})),
				engineio:      mockEngineIOClient,
				namespaces: map[string]*namespace{
					mockNamespace.name: mockNamespace,
				},
				defaultNs: mockNamespace,
				logger:    mockLogger,
				timer:     mockTimer,
				parser:    mockParser,
			},
		},
		{
			name:    "No URL or EngineIOClient",
			options: []ClientOption{},
			wantErr: true,
		},
		{
			name: "Both URL and EngineIOClient",
			options: []ClientOption{
				WithURL(testURL),
				WithEngineIOClient(mockEngineIOClient),
			},
			wantErr: true,
		},
		{
			name: "bad url",
			options: []ClientOption{
				WithURL(&url.URL{
					Scheme: "xyz",
				}),
			},
			wantErr: true,
		},
		{
			name: "options error",
			options: []ClientOption{
				func(ic *InitClient) error {
					return errors.New("oops")
				},
			},
			wantErr: true,
		},
		{
			name: "Nil logger",
			options: []ClientOption{
				WithURL(testURL),
				WithLogger(nil),
			},
			wantErr: true,
		},
		{
			name: "Nil parser",
			options: []ClientOption{
				WithURL(testURL),
				WithParser(nil),
			},
			want: &Client{
				handshakeData: make(map[string]interface{}),
				namespaces: map[string]*namespace{
					"/": {name: "/"},
				},
				defaultNs: &namespace{
					name: "/",
				},
				ackCallbacks: make(map[int]func([]interface{})),
				logger:       &utils.DefaultLogger{},
				timer:        &utils.DefaultTimer{},
				parser:       &socketio_v5_parser.SocketIOV5DefaultParser{},
			},
		},
		{
			name: "Nil timer",
			options: []ClientOption{
				WithURL(testURL),
				WithTimer(nil),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.wantErr {
				mockEngineIOClient.EXPECT().On("connect", gomock.Any()).AnyTimes()
				mockEngineIOClient.EXPECT().On("message", gomock.Any()).AnyTimes()
			}

			got, err := NewClient(tt.options...)
			require.Equal(t, tt.wantErr, err != nil)

			if got == nil || tt.want == nil {
				require.Equal(t, tt.want, got)
				return
			}

			// Check main properties
			assert.Equal(t, tt.want.handshakeData, got.handshakeData)
			// Check NS by name only
			for k, v := range got.namespaces {
				if assert.Contains(t, tt.want.namespaces, k) {
					assert.Equal(t, v.name, tt.want.namespaces[k].name)
				}

			}
			assert.Len(t, got.namespaces, len(tt.want.namespaces))
			assert.Equal(t, got.ackCallbacks, tt.want.ackCallbacks)
			assert.IsType(t, tt.want.logger, got.logger)
			assert.IsType(t, tt.want.timer, got.timer)
			assert.IsType(t, tt.want.parser, got.parser)

			if got.engineio != tt.want.engineio {
				// A bit cheat to not mock default engine io client
				assert.Nil(t, tt.want.engineio)
			}
			// For NS compare names only
			assert.Equal(t, got.defaultNs.name, tt.want.defaultNs.name)
		})
	}
}

func TestWithURL(t *testing.T) {
	testCases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"Valid URL", "http://example.com", false},
		{"Valid URL with path", "http://example.com/custom", false},
		{"Valid URL without path", "http://example.com", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parsedURL, _ := url.Parse(tc.url)
			option := WithURL(parsedURL)
			client := &InitClient{Client: &Client{}}
			err := option(client)

			if (err != nil) != tc.wantErr {
				t.Errorf("WithURL() error = %v, wantErr %v", err, tc.wantErr)
				return
			}

			if !tc.wantErr {
				if client.url == nil {
					t.Errorf("WithURL() did not set the URL")
				}
				if client.url.Path == "" {
					t.Errorf("WithURL() did not set the default path")
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
		{"Valid URL", "http://example.com", false},
		{"Valid URL with path", "http://example.com/custom", false},
		{"Invalid URL", "://invalid", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			option := WithRawURL(tc.url)
			client := &InitClient{Client: &Client{}}
			err := option(client)

			if (err != nil) != tc.wantErr {
				t.Errorf("WithRawURL() error = %v, wantErr %v", err, tc.wantErr)
				return
			}

			if !tc.wantErr {
				if client.url == nil {
					t.Errorf("WithRawURL() did not set the URL")
				}
				if client.url.Path == "" {
					t.Errorf("WithRawURL() did not set the default path")
				}
			}
		})
	}
}

func TestWithEngineIOClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockEngineIOClient := mocks.NewMockEngineIOClient(ctrl)
	option := WithEngineIOClient(mockEngineIOClient)
	client := &InitClient{Client: &Client{}}
	err := option(client)

	if err != nil {
		t.Errorf("WithEngineIOClient() returned an error: %v", err)
	}

	if client.engineio != mockEngineIOClient {
		t.Errorf("WithEngineIOClient() did not set the EngineIOClient correctly")
	}
}

func TestWithDefaultNamespace(t *testing.T) {
	mockNamespace := &namespace{}
	option := WithDefaultNamespace(mockNamespace.name)
	client := &InitClient{Client: &Client{}}
	err := option(client)

	if err != nil {
		t.Errorf("WithDefaultNamespace() returned an error: %v", err)
	}

	if *client.defaultNsName != mockNamespace.name {
		t.Errorf("WithDefaultNamespace() did not set the default namespace correctly")
	}
}

func TestWithLogger(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	option := WithLogger(mockLogger)
	client := &InitClient{Client: &Client{}}
	err := option(client)

	if err != nil {
		t.Errorf("WithLogger() returned an error: %v", err)
	}

	if client.logger != mockLogger {
		t.Errorf("WithLogger() did not set the logger correctly")
	}
}

func TestWithTimer(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTimer := mocks.NewMockTimer(ctrl)
	option := WithTimer(mockTimer)
	client := &InitClient{Client: &Client{}}
	err := option(client)

	if err != nil {
		t.Errorf("WithTimer() returned an error: %v", err)
	}

	if client.timer != mockTimer {
		t.Errorf("WithTimer() did not set the timer correctly")
	}
}

func TestWithParser(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockParser := mocks.NewMockParser(ctrl)
	option := WithParser(mockParser)
	client := &InitClient{Client: &Client{}}
	err := option(client)

	if err != nil {
		t.Errorf("WithParser() returned an error: %v", err)
	}

	if client.parser != mockParser {
		t.Errorf("WithParser() did not set the parser correctly")
	}
}

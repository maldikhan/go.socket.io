package socketio_v5_client

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	engineio_v4_client "github.com/maldikhan/go.socket.io/engine.io/v4/client"
	socketio_v5_parser_default "github.com/maldikhan/go.socket.io/socket.io/v5/parser/default"
	"github.com/maldikhan/go.socket.io/utils"
)

type ClientOption func(*InitClient) error

// defaultParserFactory builds the default socket.io parser. It is a
// package-level variable so tests can exercise the construction-failure path
// (NewParser only fails when an option fails, which the options used by
// NewClient never do).
var defaultParserFactory = socketio_v5_parser_default.NewParser

type InitClient struct {
	url            *url.URL
	defaultNsName  *string
	connectTimeout time.Duration
	*Client
}

func NewClient(options ...ClientOption) (*Client, error) {
	client := &InitClient{
		Client: &Client{
			ctx:           context.Background(), // safe default so Emit before Connect won't panic
			handshakeData: make(map[string]interface{}),
			namespaces:    make(map[string]*namespace),
			ackCallbacks:  make(map[int]func([]interface{})),

			logger:        &utils.DefaultLogger{},
			timer:         &utils.DefaultTimer{},
			redactPayload: true, // production-safe default; WithDebugPayload(true) opts out
		},
	}

	for _, opt := range options {
		if err := opt(client); err != nil {
			return nil, err
		}
	}

	if client.logger == nil {
		return nil, errors.New("logger is nil")
	}

	if client.parser == nil {
		parser, err := defaultParserFactory(
			socketio_v5_parser_default.WithLogger(client.logger),
		)
		if err != nil {
			return nil, err
		}
		client.parser = parser
	}

	if client.timer == nil {
		return nil, errors.New("timer is nil")
	}

	if (client.engineio == nil && client.url == nil) || (client.engineio != nil && client.url != nil) {
		return nil, fmt.Errorf("either WithURL or WithEngineIOClient must be provided")
	}

	if client.engineio == nil {
		engineOptions := []engineio_v4_client.EngineClientOption{
			engineio_v4_client.WithURL(client.url),
			engineio_v4_client.WithLogger(client.logger),
			engineio_v4_client.WithDebugPayload(!client.redactPayload),
		}
		if client.connectTimeout > 0 {
			engineOptions = append(engineOptions, engineio_v4_client.WithConnectTimeout(client.connectTimeout))
		}
		engineioClient, err := engineio_v4_client.NewClient(engineOptions...)
		if err != nil {
			return nil, err
		}
		client.engineio = engineioClient
	} else if client.connectTimeout > 0 {
		return nil, errors.New("WithConnectTimeout can't be combined with WithEngineIOClient: configure the timeout on the engine.io client instead")
	}

	defaultNsName := "/"
	if client.defaultNsName != nil {
		defaultNsName = *client.defaultNsName
	}
	client.defaultNs = client.namespace(defaultNsName)

	client.engineio.On("connect", client.connectSocketIO)
	client.engineio.On("message", client.onMessage)

	return client.Client, nil
}

func WithURL(url *url.URL) ClientOption {
	return func(c *InitClient) error {
		if url.Path == "" {
			url.Path = "/socket.io/"
		}
		c.url = url
		return nil
	}
}

func WithRawURL(rawUrl string) ClientOption {
	return func(c *InitClient) error {
		parsedUrl, err := url.Parse(rawUrl)
		if err != nil {
			return err
		}
		if parsedUrl.Path == "" {
			parsedUrl.Path = "/socket.io/"
		}
		c.url = parsedUrl
		return nil
	}
}

func WithEngineIOClient(engineIOClient EngineIOClient) ClientOption {
	return func(c *InitClient) error {
		c.engineio = engineIOClient
		return nil
	}
}

func WithDefaultNamespace(ns string) ClientOption {
	return func(c *InitClient) error {
		c.defaultNsName = &ns
		return nil
	}
}

func WithLogger(logger Logger) ClientOption {
	return func(c *InitClient) error {
		c.logger = logger
		return nil
	}
}

func WithTimer(timer Timer) ClientOption {
	return func(c *InitClient) error {
		c.timer = timer
		return nil
	}
}

func WithParser(parser Parser) ClientOption {
	return func(c *InitClient) error {
		c.parser = parser
		return nil
	}
}

// WithConnectTimeout limits the duration of the connection phase (transport
// dial and engine.io handshake) without limiting the session lifetime: with
// this option set, Connect() fails fast with context.DeadlineExceeded when the
// server is unreachable, while the context passed to Connect() still controls
// how long an established session lives.
//
// The option configures the engine.io client built internally by NewClient,
// so it can't be combined with WithEngineIOClient — pass
// engineio_v4_client.WithConnectTimeout to your own engine.io client instead.
func WithConnectTimeout(timeout time.Duration) ClientOption {
	return func(c *InitClient) error {
		if timeout <= 0 {
			return fmt.Errorf("connect timeout must be positive, got %s", timeout)
		}
		c.connectTimeout = timeout
		return nil
	}
}

// WithDebugPayload enables logging of raw payloads at debug level across the
// client and the default transports it builds. Disabled by default so
// production logs do not leak message contents (tokens, PII).
func WithDebugPayload(enabled bool) ClientOption {
	return func(c *InitClient) error {
		c.redactPayload = !enabled
		return nil
	}
}

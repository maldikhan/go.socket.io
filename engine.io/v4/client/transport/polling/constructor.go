package engineio_v4_client_transport

import (
	"errors"
	"net/http"
	"time"

	"github.com/maldikhan/go.socket.io/utils"
)

type EngineTransportOption func(*Transport) error

// defaultHTTPTimeout bounds the full lifecycle (dial + TLS + headers + body)
// of a polling request. Without it, the default http.Client has no timeout and
// a poll can hang indefinitely on an unstable network, stalling the transport
// goroutine. Override with WithHTTPClient for custom needs.
const defaultHTTPTimeout = 30 * time.Second

func NewTransport(options ...EngineTransportOption) (*Transport, error) {
	// Create default client
	client := &Transport{
		log:            &utils.DefaultLogger{},
		httpClient:     &http.Client{Timeout: defaultHTTPTimeout},
		pinger:         time.NewTicker(10 * time.Second),
		stopPooling:    make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
		maxPayloadSize: 4 * 1024 * 1024, // 4MB default, matches socket.io JS maxHttpBufferSize
		redactPayload:  true,            // production-safe default; WithDebugPayload(true) opts out
	}

	// Apply options
	for _, opt := range options {
		if err := opt(client); err != nil {
			return nil, err
		}
	}

	if client.pinger == nil {
		return nil, errors.New("pinger is nil")
	}

	if client.log == nil {
		return nil, errors.New("logger is nil")
	}

	if client.httpClient == nil {
		return nil, errors.New("HTTP client is nil")
	}

	return client, nil
}

func WithLogger(logger Logger) EngineTransportOption {
	return func(c *Transport) error {
		c.log = logger
		return nil
	}
}

func WithHTTPClient(httpClient HttpClient) EngineTransportOption {
	return func(c *Transport) error {
		c.httpClient = httpClient
		return nil
	}
}

func WithDefaultPinger(pinger *time.Ticker) EngineTransportOption {
	return func(c *Transport) error {
		c.pinger.Stop()
		c.pinger = pinger
		return nil
	}
}

func WithMaxPayloadSize(size int64) EngineTransportOption {
	return func(c *Transport) error {
		if size <= 0 {
			return errors.New("maxPayloadSize must be positive")
		}
		c.maxPayloadSize = size
		return nil
	}
}

// WithDebugPayload enables logging of raw payloads at debug level.
// Disabled by default so production logs don't leak message contents.
func WithDebugPayload(enabled bool) EngineTransportOption {
	return func(c *Transport) error {
		c.redactPayload = !enabled
		return nil
	}
}

package engineio_v4_client

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	engineio_v4 "maldikhan/go.socket.io/engine.io/v4"
	engineio_v4_client_transport_polling "maldikhan/go.socket.io/engine.io/v4/client/transport/polling"
	engineio_v4_client_transport_ws "maldikhan/go.socket.io/engine.io/v4/client/transport/websocket"
	engineio_v4_parser "maldikhan/go.socket.io/engine.io/v4/parser"
	"maldikhan/go.socket.io/utils"
	ws_native "maldikhan/go.socket.io/websocket/native"
)

type ClientInit struct {
	httpClient engineio_v4_client_transport_polling.HttpClient
	ws         engineio_v4_client_transport_ws.WebSocket
	*Client
}

type EngineClientOption func(*ClientInit) error

func NewClient(options ...EngineClientOption) (*Client, error) {
	// Создаем клиент с настройками по умолчанию
	client := &ClientInit{
		httpClient: &http.Client{},
		ws:         &ws_native.WebSocketConnection{},
		Client: &Client{
			log:               &utils.DefaultLogger{},
			parser:            &engineio_v4_parser.EngineIOV4Parser{},
			reconnectAttempts: 5,
			reconnectWait:     5 * time.Second,
			pingInterval:      time.NewTicker(10 * time.Second),
			stopPooling:       make(chan struct{}, 1),
			transportClosed:   make(chan error, 1),
		},
	}

	for _, opt := range options {
		if err := opt(client); err != nil {
			return nil, err
		}
	}

	if client.url == nil {
		return nil, errors.New("URL is nil")
	}

	if client.log == nil {
		return nil, errors.New("logger is nil")
	}

	if client.parser == nil {
		return nil, errors.New("logger is nil")
	}

	if len(client.supportedTransports) == 0 {
		wsTransport, err := engineio_v4_client_transport_ws.NewTransport(
			engineio_v4_client_transport_ws.WithLogger(client.log),
			engineio_v4_client_transport_ws.WithWebSocket(client.ws),
		)
		if err != nil {
			return nil, err
		}
		pollingTransport, err := engineio_v4_client_transport_polling.NewTransport(
			engineio_v4_client_transport_polling.WithDefaultPinger(client.pingInterval),
			engineio_v4_client_transport_polling.WithLogger(client.log),
			engineio_v4_client_transport_polling.WithHTTPClient(client.httpClient),
		)
		if err != nil {
			return nil, err
		}
		if client.supportedTransports == nil {
			client.supportedTransports = make(map[engineio_v4.EngineIOTransport]Transport)
		}
		for _, transport := range []Transport{wsTransport, pollingTransport} {
			client.supportedTransports[transport.Transport()] = transport
		}
	}

	if client.transport == nil {
		var ok bool
		if client.transport, ok = client.supportedTransports[engineio_v4.TransportPolling]; !ok {
			client.transport = client.supportedTransports[engineio_v4.TransportWebsocket]
		}
	}

	return client.Client, nil
}

func WithURL(url *url.URL) EngineClientOption {
	return func(c *ClientInit) error {

		switch url.Scheme {
		case "http", "https":
		case "ws":
			url.Scheme = "http"
		case "wss":
			url.Scheme = "https"
		default:
			return fmt.Errorf("invalid URL scheme: %s", url.Scheme)
		}
		query := url.Query()
		query.Set("EIO", "4")
		url.RawQuery = query.Encode()

		c.url = url
		return nil
	}
}

func WithRawURL(urlRaw string) EngineClientOption {
	url, err := url.Parse(urlRaw)
	if err != nil {
		return func(_ *ClientInit) error {
			return err
		}
	}
	return WithURL(url)
}

func WithLogger(logger Logger) EngineClientOption {
	return func(c *ClientInit) error {
		c.log = logger
		return nil
	}
}

func WithTransport(transport Transport) EngineClientOption {
	return func(c *ClientInit) error {
		c.transport = transport
		return nil
	}
}

func WithSupportedTransports(transports []Transport) EngineClientOption {
	return func(c *ClientInit) error {
		if c.supportedTransports == nil {
			c.supportedTransports = make(map[engineio_v4.EngineIOTransport]Transport)
		}
		for _, transport := range transports {
			if transport == nil {
				return fmt.Errorf("nil transport is not allowed")
			}
			c.supportedTransports[transport.Transport()] = transport
		}
		return nil
	}
}

func WithHTTPClient(httpClient engineio_v4_client_transport_polling.HttpClient) EngineClientOption {
	return func(c *ClientInit) error {
		c.httpClient = httpClient
		return nil
	}
}

func WithWebSocket(ws engineio_v4_client_transport_ws.WebSocket) EngineClientOption {
	return func(c *ClientInit) error {
		c.ws = ws
		return nil
	}
}

func WithParser(parser Parser) EngineClientOption {
	return func(c *ClientInit) error {
		c.parser = parser
		return nil
	}
}

func WithReconnectAttempts(attempts int) EngineClientOption {
	return func(c *ClientInit) error {
		c.reconnectAttempts = attempts
		return nil
	}
}

func WithReconnectWait(wait time.Duration) EngineClientOption {
	return func(c *ClientInit) error {
		c.reconnectWait = wait
		return nil
	}
}

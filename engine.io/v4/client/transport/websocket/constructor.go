package engineio_v4_client_transport

import (
	"errors"
	"net/url"

	"maldikhan/go.socket.io/utils"
	ws_native "maldikhan/go.socket.io/websocket/native"
)

type EngineTransportOption func(*Transport) error

func NewTransport(options ...EngineTransportOption) (*Transport, error) {
	// Создаем клиент с настройками по умолчанию
	client := &Transport{
		log:         &utils.DefaultLogger{},
		ws:          &ws_native.WebSocketConnection{},
		stopPooling: make(chan struct{}, 1),
	}

	// Применяем пользовательские настройки
	for _, opt := range options {
		if err := opt(client); err != nil {
			return nil, err
		}
	}

	if client.log == nil {
		return nil, errors.New("logger is nil")
	}

	if client.ws == nil {
		return nil, errors.New("websocket connection is nil")
	}

	return client, nil
}

func WithLogger(logger Logger) EngineTransportOption {
	return func(c *Transport) error {
		c.log = logger
		return nil
	}
}

func WithWebSocket(ws WebSocket) EngineTransportOption {
	return func(c *Transport) error {
		c.ws = ws
		return nil
	}
}

func WithOrigin(origin *url.URL) EngineTransportOption {
	return func(c *Transport) error {
		c.origin = origin
		return nil
	}
}

package engineio_v4_client_transport

import (
	"errors"
	"net/http"
	"time"

	"maldikhan/go.socket.io/utils"
)

type EngineTransportOption func(*Transport) error

func NewTransport(options ...EngineTransportOption) (*Transport, error) {
	// Создаем клиент с настройками по умолчанию
	client := &Transport{
		log:         &utils.DefaultLogger{},
		httpClient:  &http.Client{},
		pinger:      time.NewTicker(10 * time.Second),
		stopPooling: make(chan struct{}, 1),
	}

	// Применяем пользовательские настройки
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

package socketio_v5_client

import (
	"errors"
	"fmt"
	"net/url"

	engineio_v4_client "maldikhan/go.socket.io/engine.io/v4/client"
	socketio_v5_parser "maldikhan/go.socket.io/socket.io/v5/parser"
	"maldikhan/go.socket.io/utils"
)

type ClientOption func(*InitClient) error

type InitClient struct {
	url           *url.URL
	defaultNsName *string
	*Client
}

func NewClient(options ...ClientOption) (*Client, error) {
	client := &InitClient{
		Client: &Client{
			handshakeData: make(map[string]interface{}),
			namespaces:    make(map[string]*namespace),
			ackCallbacks:  make(map[int]func([]interface{})),
			logger:        &utils.DefaultLogger{},
			timer:         &utils.DefaultTimer{},
			parser:        &socketio_v5_parser.SocketIOV5Parser{},
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
		return nil, errors.New("parser is nil")
	}

	if client.timer == nil {
		return nil, errors.New("timer is nil")
	}

	if (client.engineio == nil && client.url == nil) || (client.engineio != nil && client.url != nil) {
		return nil, fmt.Errorf("either WithURL or WithEngineIOClient must be provided")
	}

	if client.engineio == nil {
		engineioClient, err := engineio_v4_client.NewClient(
			engineio_v4_client.WithURL(client.url),
			engineio_v4_client.WithLogger(client.logger),
		)
		if err != nil {
			return nil, err
		}
		client.engineio = engineioClient
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

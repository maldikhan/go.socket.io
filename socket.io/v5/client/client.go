package socketio_v5_client

import (
	"context"
	"sync"

	socketio_v5 "maldikhan/go.socket.io/socket.io/v5"
)

type Client struct {
	ctx           context.Context
	handshakeData map[string]interface{}
	engineio      EngineIOClient
	parser        Parser
	namespaces    map[string]*namespace
	defaultNs     *namespace
	logger        Logger
	timer         Timer
	mutex         sync.RWMutex
	ackCallbacks  map[int]func([]interface{})
	ackCounter    int
}

type namespace struct {
	client   *Client
	name     string
	handlers map[string][]func([]interface{})
}

func (c *Client) SetHandshakeData(data map[string]interface{}) {
	c.handshakeData = data
}

func (c *Client) connectSocketIO(_ []byte) {
	c.sendPacket(&socketio_v5.Message{
		Type:    socketio_v5.PacketConnect,
		Payload: c.handshakeData,
	})
}

func (c *Client) Connect(ctx context.Context) error {
	c.ctx = ctx

	return c.engineio.Connect(ctx)
}

func (c *Client) namespace(name string) *namespace {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if ns, ok := c.namespaces[name]; ok {
		return ns
	}

	ns := &namespace{
		client:   c,
		name:     name,
		handlers: make(map[string][]func([]interface{})),
	}
	c.namespaces[name] = ns
	return ns
}

func (c *Client) Close() error {
	return c.engineio.Close()
}

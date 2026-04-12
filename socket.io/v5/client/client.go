package socketio_v5_client

import (
	"context"
	"sync"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

type Client struct {
	engineio EngineIOClient
	parser   Parser
	logger   Logger
	timer    Timer

	ctx   context.Context
	mutex sync.RWMutex

	handshakeData map[string]interface{}

	namespaces map[string]*namespace
	defaultNs  *namespace

	ackCallbacks map[int]func([]interface{})
	ackCounter   int
}

type namespace struct {
	client *Client

	name string

	mu          sync.RWMutex
	handlers    map[string][]func([]interface{})
	anyHandlers []func(string, []interface{})

	waitConnected chan struct{}
	hadConnected  sync.Once
}

func (c *Client) SetHandshakeData(data map[string]interface{}) {
	// Make a copy to prevent external mutation after set
	dataCopy := make(map[string]interface{})
	for k, v := range data {
		dataCopy[k] = v
	}
	c.mutex.Lock()
	c.handshakeData = dataCopy
	c.mutex.Unlock()
}

func (c *Client) connectSocketIO(_ []byte) {
	c.mutex.RLock()
	handshakeData := c.handshakeData
	c.mutex.RUnlock()

	err := c.sendPacket(&socketio_v5.Message{
		Type:    socketio_v5.PacketConnect,
		Payload: handshakeData,
	})

	if err != nil {
		c.logger.Errorf("Can't connect: %v", err)
	}
}

func (c *Client) Connect(ctx context.Context, callbacks ...func(arg interface{})) error {
	c.mutex.Lock()
	c.ctx = ctx
	c.mutex.Unlock()
	for _, callback := range callbacks {
		c.On("connect", callback)
	}
	return c.engineio.Connect(ctx)
}

func (c *Client) namespace(name string) *namespace {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if ns, ok := c.namespaces[name]; ok {
		return ns
	}

	ns := &namespace{
		client:        c,
		name:          name,
		handlers:      make(map[string][]func([]interface{})),
		waitConnected: make(chan struct{}),
		hadConnected:  sync.Once{},
	}
	c.namespaces[name] = ns
	return ns
}

func (c *Client) Close() error {
	return c.engineio.Close()
}

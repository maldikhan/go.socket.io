package socketio_v5_client

import (
	"context"
	"fmt"
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

	// redactPayload, when true, replaces raw payloads in debug logs with a
	// size marker. Zero value is verbose; NewClient sets the safe default.
	redactPayload bool
}

// payload returns a size marker when redaction is enabled, or the raw data.
func (c *Client) payload(data []byte) string {
	if c.redactPayload {
		return fmt.Sprintf("[redacted %d bytes]", len(data))
	}
	return string(data)
}

type namespace struct {
	client *Client

	name string

	mu          sync.RWMutex
	handlers    map[string][]func([]interface{})
	anyHandlers []func(string, []interface{})

	// waitConnected gates Emit until the server acknowledged the namespace
	// CONNECT: open (blocking) while a CONNECT is outstanding, closed once the
	// ack arrived. It is re-armed by resetConnectionGate when a reconnect cycle
	// starts, so post-reconnect emits wait for the re-sent CONNECT to be
	// acknowledged. Guarded by mu.
	waitConnected chan struct{}
}

// connectionGate returns the current waitConnected channel (which may be nil
// for namespaces built without the constructor in tests).
func (n *namespace) connectionGate() chan struct{} {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.waitConnected
}

// resetConnectionGate re-arms waitConnected before a reconnect attempt so Emit
// blocks until the server acknowledges the re-sent CONNECT. Only a closed gate
// is replaced: replacing an open one would orphan emitters already waiting on
// it (their CONNECT ack is still the next one to arrive).
func (n *namespace) resetConnectionGate() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.waitConnected == nil {
		n.waitConnected = make(chan struct{})
		return
	}
	select {
	case <-n.waitConnected:
		n.waitConnected = make(chan struct{})
	default:
	}
}

// openConnectionGate marks the namespace connected, releasing every emitter
// blocked on the gate. Closing is idempotent so duplicate CONNECT acks are safe.
func (n *namespace) openConnectionGate() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.waitConnected == nil {
		return
	}
	select {
	case <-n.waitConnected:
	default:
		close(n.waitConnected)
	}
}

// SetHandshakeData stores a shallow copy of the provided map as the handshake
// payload. Top-level keys are detached from the caller's map, but nested
// maps/slices are still shared. Callers must not mutate nested values after
// calling SetHandshakeData, or supply only flat/JSON-primitive values.
func (c *Client) SetHandshakeData(data map[string]interface{}) {
	// Make a shallow copy to prevent external mutation of top-level keys
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
		NS:      c.defaultNs.name,
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
	}
	c.namespaces[name] = ns
	return ns
}

func (c *Client) safeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.logger.Errorf("panic in event handler: %v", r)
			}
		}()
		fn()
	}()
}

func (c *Client) Close() error {
	return c.engineio.Close()
}

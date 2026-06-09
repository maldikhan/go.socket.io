package engineio_v4_client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
)

type Client struct {
	url                 *url.URL
	ctx                 context.Context
	log                 Logger
	transport           Transport
	supportedTransports map[engineio_v4.EngineIOTransport]Transport
	sid                 string
	pingInterval        *time.Ticker
	pingTimeout         time.Duration
	parser              Parser
	messageHandler      func([]byte)
	closeHandler        func()
	reconnectAttempts   int
	reconnectWait       time.Duration
	connectTimeout      time.Duration
	waitUpgrade         chan struct{}
	hadUpgrade          sync.Once
	waitHandshake       chan struct{}
	hadHandshake        sync.Once
	stopPooling         chan struct{}
	transportClosed     chan error
	afterConnect        func()
	messages            chan []byte
	messagesDone        chan struct{} // closed when messageLoop exits

	// transportMu serializes access to the transport field and
	// the waitUpgrade / waitHandshake channels so that Send() never
	// races with transportUpgrade() or Close().
	transportMu sync.RWMutex

	// connectCancel cancels the connection context created by Connect() when
	// a connect timeout is configured. It is stored so Close() can release the
	// context (and its resources) early instead of waiting for the parent
	// context to be cancelled. Guarded by transportMu.
	connectCancel context.CancelFunc

	// handlerMu protects access to the handler fields
	// (messageHandler, closeHandler, afterConnect).
	// On() writes them from the user goroutine, while handlePacket()
	// and handleHandshake() read them from the transport goroutine.
	handlerMu sync.RWMutex

	// redactPayload, when true, replaces raw packet payloads in debug logs
	// with a size marker so production logs never leak message contents
	// (tokens, PII). The zero value is "don't redact" (verbose); NewClient
	// sets the production-safe default and WithDebugPayload(true) disables it.
	redactPayload bool
}

// payload returns a size marker for debug logging when payload redaction is
// enabled, or the raw data otherwise. The packet type/prefix stays visible
// either way.
func (c *Client) payload(data []byte) string {
	if c.redactPayload {
		return fmt.Sprintf("[redacted %d bytes]", len(data))
	}
	return string(data)
}

// Connect establishes the engine.io connection. ctx controls the lifetime of
// the whole session: cancelling it stops the transports and the message loop.
//
// When a connect timeout is configured via WithConnectTimeout, the timeout
// applies only to the connection phase (transport dial, handshake request and
// the OPEN packet, including an eventual transport upgrade): Connect blocks
// until the handshake completes and returns context.DeadlineExceeded if it
// does not finish in time, while a successfully established session keeps
// running for as long as ctx allows. Without the option, Connect returns as
// soon as the handshake request is sent (previous behavior).
func (c *Client) Connect(ctx context.Context) error {
	if c.connectTimeout <= 0 {
		return c.connect(ctx)
	}

	// connCtx drives the dial/handshake phase. On success it simply remains
	// the session context: it is derived from ctx, so the caller's
	// cancellation still propagates, and it is only cancelled early (by the
	// timer below) when the handshake does not complete in time.
	connCtx, connCancel := context.WithCancel(ctx)
	c.transportMu.Lock()
	c.connectCancel = connCancel
	c.transportMu.Unlock()

	timer := time.AfterFunc(c.connectTimeout, connCancel)

	timeoutErr := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("engine.io: connect timeout after %s: %w", c.connectTimeout, context.DeadlineExceeded)
	}

	err := c.connect(connCtx)
	if err != nil {
		// Check the contexts before releasing connCtx: connCancel() below sets
		// connCtx.Err() unconditionally, which would mask the real cause.
		timedOut := connCtx.Err() != nil && ctx.Err() == nil
		timer.Stop()
		connCancel()
		if timedOut {
			// The failure was caused by the timeout firing, not by the caller.
			return timeoutErr()
		}
		return err
	}

	// The handshake request is sent; wait for the OPEN packet (handleHandshake
	// closes the waitHandshake gate after an eventual transport upgrade).
	c.transportMu.RLock()
	wh := c.waitHandshake
	c.transportMu.RUnlock()

	select {
	case <-wh:
		timer.Stop()
		if connCtx.Err() == nil {
			return nil
		}
		// The timeout fired while the handshake was completing: the transports
		// are already shutting down, so report the failure to the caller.
	case <-connCtx.Done():
		timer.Stop()
	}

	// Timed out (or the caller cancelled ctx): tear down whatever was started.
	_ = c.Close()
	return timeoutErr()
}

// connect performs the connection sequence: it runs the transport, starts the
// message loop and sends the handshake request. It does not wait for the
// handshake response.
func (c *Client) connect(ctx context.Context) error {
	c.ctx = ctx

	c.messages = make(chan []byte, 100)

	// Run transport before starting the message loop so that a Run()
	// failure doesn't leak a goroutine.
	c.transportClosed = make(chan error, 1)
	err := c.transport.Run(ctx, c.url, c.sid, c.messages, c.transportClosed)
	if err != nil {
		close(c.transportClosed)
		return err
	}

	// Start the message loop only after Run() succeeds.
	c.messagesDone = make(chan struct{})
	go c.messageLoop(ctx, c.messages)

	c.transportMu.Lock()
	c.hadHandshake = sync.Once{}
	c.waitHandshake = make(chan struct{}, 1)
	c.transportMu.Unlock()

	err = c.transport.RequestHandshake()
	if err != nil {
		// Clean up: stop transport and wait for the message loop to exit
		// so we don't leak a goroutine.
		_ = c.transport.Stop()
		if c.transportClosed != nil {
			<-c.transportClosed
		}
		close(c.messages)
		<-c.messagesDone
		return err
	}

	return nil
}

func (c *Client) messageLoop(ctx context.Context, messages <-chan []byte) {
	if c.messagesDone != nil {
		defer close(c.messagesDone)
	}
	if messages == nil {
		c.log.Errorf("messages channel is nil, can't read transport messages")
		return
	}
	for {
		select {
		case message, ok := <-messages:
			if !ok {
				return
			}
			err := c.handlePacket(message)
			if err != nil {
				c.log.Errorf("handle packet error: %s", err)
			}
		case <-ctx.Done():
			c.log.Warnf("context done, engine.io client stopped processing messages")
			return
		}
	}
}

func (c *Client) transportUpgrade(transport Transport) error {
	// Lock while mutating state that Send() reads.
	c.transportMu.Lock()
	c.hadUpgrade = sync.Once{}
	c.waitUpgrade = make(chan struct{}, 1)

	// failUpgrade closes the upgrade gate so that Send() callers waiting
	// on waitUpgrade are unblocked even when the upgrade fails.
	failUpgrade := func(err error) error {
		c.hadUpgrade.Do(func() {
			close(c.waitUpgrade)
		})
		c.transportMu.Unlock()
		return err
	}

	err := c.transport.Stop()
	if err != nil {
		c.log.Errorf("stop transport: %s", err)
		return failUpgrade(err)
	}
	<-c.transportClosed
	c.transport = transport

	c.transportClosed = make(chan error, 1)
	err = c.transport.Run(c.ctx, c.url, c.sid, c.messages, c.transportClosed)
	if err != nil {
		close(c.transportClosed)
		c.log.Errorf("run transport: %s", err)
		return failUpgrade(err)
	}
	c.transportMu.Unlock()

	err = c.sendPacket(&engineio_v4.Message{
		Type: engineio_v4.PacketPing,
		Data: []byte("probe"),
	})
	if err != nil {
		// Probe failed — unblock Send() callers waiting on the upgrade gate.
		c.transportMu.Lock()
		c.hadUpgrade.Do(func() {
			close(c.waitUpgrade)
		})
		c.transportMu.Unlock()
	}
	return err
}

func (c *Client) handleHandshake(data []byte) error {
	c.log.Debugf("apply handshake: %s", c.payload(data))

	handshakeResp := &engineio_v4.HandshakeResponse{}
	err := json.Unmarshal(data, handshakeResp)
	if err != nil {
		return err
	}

	if handshakeResp.Sid == "" {
		return fmt.Errorf("handshake error: no sid")
	}

	// Upgrade transports
	for _, transport := range c.supportedTransports {
		transport.SetHandshake(handshakeResp)
	}

	c.sid = handshakeResp.Sid
	if handshakeResp.PingInterval != 0 {
		if c.pingInterval != nil {
			// Reset reuses the existing ticker (shared with the polling transport),
			// so we don't break the transport's pinger reference.
			c.pingInterval.Reset(time.Duration(handshakeResp.PingInterval) * time.Millisecond)
		} else {
			c.pingInterval = time.NewTicker(time.Duration(handshakeResp.PingInterval) * time.Millisecond)
		}
	}

	if handshakeResp.PingTimeout != 0 {
		c.pingTimeout = time.Duration(handshakeResp.PingTimeout) * time.Millisecond
	}

	// Perform protocol upgrade BEFORE unblocking Send() callers so that
	// waitUpgrade is published before waitHandshake is closed. Otherwise
	// a Send() waiting on waitHandshake would wake with waitUpgrade == nil
	// and write on the old transport during the upgrade window.
	if len(handshakeResp.Upgrades) > 0 {
		for _, newTransportName := range handshakeResp.Upgrades {
			if c.transport.Transport() == engineio_v4.EngineIOTransport(newTransportName) {
				break
			}
			if newTransport, found := c.supportedTransports[engineio_v4.EngineIOTransport(newTransportName)]; found {
				err = c.transportUpgrade(newTransport)
				if err != nil {
					// Close the handshake gate so that any Send() caller
					// waiting on waitHandshake doesn't block forever.
					c.hadHandshake.Do(func() {
						close(c.waitHandshake)
					})
					return err
				}

				break
			} else {
				c.log.Warnf("unsupported upgrade: %s", newTransportName)
			}
		}
	}

	// Close the handshake gate AFTER the upgrade gate (waitUpgrade) is
	// already published so Send() sees both gates atomically.
	c.hadHandshake.Do(func() {
		close(c.waitHandshake)
	})

	// Call onConnect hook in a goroutine so that messageLoop can continue
	// processing engine.io packets (e.g. the WebSocket upgrade probe response
	// "3probe") while the hook is running. If afterConnect calls Send(),
	// Send() waits on waitUpgrade, which is only closed after messageLoop
	// processes "3probe" — calling afterConnect inline would deadlock.
	// The handler is copied under handlerMu so a concurrent On() registration
	// is race-free, and invoked via the local copy (never c.afterConnect).
	c.handlerMu.RLock()
	handler := c.afterConnect
	c.handlerMu.RUnlock()
	if handler != nil {
		go handler()
	}

	return nil
}

func (c *Client) handlePacket(packetData []byte) error {
	c.log.Debugf("handle packet: %s", c.payload(packetData))
	packet, err := c.parser.Parse(packetData)
	if err != nil {
		c.log.Errorf("Can't parse packet: %s %v", string(packetData), err)
		return err
	}

	c.log.Debugf("handle: %d %s", packet.Type, c.payload(packet.Data))

	switch packet.Type {
	case engineio_v4.PacketOpen:
		err := c.handleHandshake(
			packet.Data,
		)
		if err != nil {
			c.log.Errorf("handle handshake error: %s", err)
			return err
		}
	case engineio_v4.PacketClose:
		c.handlerMu.RLock()
		handler := c.closeHandler
		c.handlerMu.RUnlock()
		if handler != nil {
			handler()
		}
	case engineio_v4.PacketPing:
		err := c.sendPacket(&engineio_v4.Message{
			Type: engineio_v4.PacketPong,
		})
		if err != nil {
			c.log.Errorf("send ping error: %s", err)
		}
		return err
	case engineio_v4.PacketPong:
		if string(packet.Data) == "probe" {
			err := c.sendPacket(&engineio_v4.Message{
				Type: engineio_v4.PacketUpgrade,
			})
			c.hadUpgrade.Do(func() {
				close(c.waitUpgrade)
			})
			if err != nil {
				c.log.Errorf("send upgrade error: %s", err)
				return err
			} else {
				c.log.Debugf("Protocol upgraded")
			}
		}
	case engineio_v4.PacketMessage:
		c.handlerMu.RLock()
		handler := c.messageHandler
		c.handlerMu.RUnlock()
		if handler != nil {
			handler(packet.Data)
		}
	}
	return nil
}

func (c *Client) sendPacket(packet *engineio_v4.Message) error {
	if packet == nil {
		return errors.New("no packet provided")
	}

	msg, err := c.parser.Serialize(packet)
	if err != nil {
		return err
	}

	return c.transport.SendMessage(msg)
}

func (c *Client) Send(message []byte) error {
	// Snapshot wait channels under the lock so that we never miss a
	// channel created by a concurrent transportUpgrade or Connect.
	c.transportMu.RLock()
	wh := c.waitHandshake
	wu := c.waitUpgrade
	c.transportMu.RUnlock()

	if wh != nil {
		<-wh
	}
	if wu != nil {
		<-wu
	}

	// Re-acquire the lock to read the current transport safely.
	c.transportMu.RLock()
	t := c.transport
	c.transportMu.RUnlock()

	if t == nil {
		return errors.New("client is closed")
	}

	// Use the snapshotted transport directly instead of sendPacket()
	// which re-reads c.transport and would race with Close()/upgrade.
	msg, err := c.parser.Serialize(&engineio_v4.Message{
		Type: engineio_v4.PacketMessage,
		Data: message,
	})
	if err != nil {
		return err
	}
	return t.SendMessage(msg)
}

func (c *Client) On(event string, handler func([]byte)) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()

	switch event {
	case "connect":
		c.afterConnect = func() { handler(nil) }
	case "message":
		c.messageHandler = handler
	case "close":
		c.closeHandler = func() { handler(nil) }
	}
}

func (c *Client) Close() error {
	// Write-lock to prevent new Send() calls from acquiring the transport
	// while we are tearing it down. Setting transport to nil ensures that
	// any Send() arriving after Close releases the lock will see nil and
	// fail fast instead of writing on a stopped transport.
	c.transportMu.Lock()
	t := c.transport
	c.transport = nil
	connectCancel := c.connectCancel
	c.connectCancel = nil
	c.transportMu.Unlock()

	// Release the connection context created by Connect() (when a connect
	// timeout is configured) so it does not stay parked on the parent context.
	if connectCancel != nil {
		defer connectCancel()
	}

	// Stop the ping ticker to prevent goroutine leak
	if c.pingInterval != nil {
		c.pingInterval.Stop()
	}

	if t == nil {
		return nil
	}

	err := t.Stop()
	if err != nil {
		return err
	}
	if c.transportClosed != nil {
		<-c.transportClosed
	}
	if c.messages != nil {
		close(c.messages)
	}
	// Wait for messageLoop goroutine to finish so that no mock/logger
	// calls happen after the caller returns (prevents test panics and
	// ensures clean shutdown).
	if c.messagesDone != nil {
		<-c.messagesDone
	}
	return nil
}

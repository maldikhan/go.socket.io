package engineio_v4_client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
)

// errClientClosed is returned by connection attempts that race with Close().
var errClientClosed = errors.New("client is closed")

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
	reconnect           bool
	reconnectHandler    func()
	reconnectFailedHand func()
	reconnectingHandler func()
	waitUpgrade         chan struct{}
	hadUpgrade          sync.Once
	waitHandshake       chan struct{}
	hadHandshake        sync.Once
	stopPooling         chan struct{}
	transportClosed     chan error
	afterConnect        func()
	messages            chan []byte
	messagesDone        chan struct{} // closed when messageLoop exits

	// closing is set to 1 by Close() before the transport is stopped so the
	// reconnect supervisor can distinguish an intentional shutdown from an
	// unexpected drop. It is read/written with sync/atomic for goroutine safety
	// (atomic.Bool is unavailable on Go 1.18).
	closing uint32

	// superviseOnce guards startSupervisor so exactly one supervisor goroutine
	// runs per connection cycle. It is reset before every (re)connect attempt.
	superviseOnce sync.Once
	// supervisorStarted is 1 while a supervisor goroutine is responsible for
	// draining transportClosed for the current cycle. Close() reads it to decide
	// whether to wait on the supervisor or drain transportClosed itself.
	supervisorStarted uint32
	// supervisorDone is closed when the supervisor goroutine for the current
	// connection cycle exits. Close() waits on it instead of reading
	// transportClosed directly, because the supervisor owns that channel.
	supervisorDone chan struct{}

	// transportMu serializes access to the transport field and
	// the waitUpgrade / waitHandshake channels so that Send() never
	// races with transportUpgrade() or Close().
	transportMu sync.RWMutex

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

func (c *Client) Connect(ctx context.Context) error {
	c.ctx = ctx
	// Mark the client as live so that an unexpected transport drop triggers a
	// reconnect rather than being mistaken for an intentional Close.
	atomic.StoreUint32(&c.closing, 0)
	return c.startConnection(ctx)
}

// startConnection runs a single connection cycle: it (re)initialises every
// per-connection channel and sync.Once guard, runs the transport, starts the
// message loop and requests the handshake. It is called by Connect for the
// initial connection and by the reconnect supervisor for each retry, so it must
// fully reset the connection state before touching the transport — otherwise a
// stale, already-closed channel or a fired sync.Once would deadlock or
// double-close on a reconnect.
func (c *Client) startConnection(ctx context.Context) error {
	// Snapshot the transport under the lock. Close() sets it to nil; if a
	// reconnect attempt races with Close() we must bail out instead of
	// dereferencing a nil transport.
	c.transportMu.RLock()
	transport := c.transport
	c.transportMu.RUnlock()
	if transport == nil {
		return errClientClosed
	}

	c.messages = make(chan []byte, 100)

	// Reset the supervisor guard so the next successful handshake starts a fresh
	// supervisor for this connection cycle, and create the done channel it will
	// close on exit.
	c.superviseOnce = sync.Once{}
	c.supervisorDone = make(chan struct{})

	// Reset the upgrade gate: a previous cycle may have left waitUpgrade closed
	// and hadUpgrade fired. They are re-armed lazily by transportUpgrade(), but
	// clearing them here keeps Send() from observing a stale, closed gate from
	// the prior connection.
	c.transportMu.Lock()
	c.hadUpgrade = sync.Once{}
	c.waitUpgrade = nil
	c.transportMu.Unlock()

	// Run transport before starting the message loop so that a Run()
	// failure doesn't leak a goroutine.
	c.transportClosed = make(chan error, 1)
	err := transport.Run(ctx, c.url, c.sid, c.messages, c.transportClosed)
	if err != nil {
		close(c.transportClosed)
		close(c.supervisorDone)
		return err
	}

	// Start the message loop only after Run() succeeds. messagesDone and
	// messages are captured locally and passed to messageLoop so the goroutine
	// owns this cycle's channels and never races with a later reconnect cycle
	// that reassigns the c.messages / c.messagesDone fields.
	messagesDone := make(chan struct{})
	messages := c.messages
	c.messagesDone = messagesDone
	go c.messageLoop(ctx, messages, messagesDone)

	c.transportMu.Lock()
	c.hadHandshake = sync.Once{}
	c.waitHandshake = make(chan struct{}, 1)
	c.transportMu.Unlock()

	err = transport.RequestHandshake()
	if err != nil {
		// Clean up: stop transport and wait for the message loop to exit
		// so we don't leak a goroutine.
		_ = transport.Stop()
		if c.transportClosed != nil {
			<-c.transportClosed
		}
		close(c.messages)
		<-c.messagesDone
		close(c.supervisorDone)
		return err
	}

	return nil
}

func (c *Client) messageLoop(ctx context.Context, messages <-chan []byte, messagesDone chan struct{}) {
	if messagesDone != nil {
		defer close(messagesDone)
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

	// Start the reconnect supervisor now that the connection is fully
	// established (handshake done, any upgrade complete). Starting it here —
	// rather than in Connect — guarantees the supervisor watches the final
	// transportClosed channel for this cycle and never competes with the
	// synchronous <-transportClosed read performed by transportUpgrade().
	c.startSupervisor()

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
	case "reconnecting":
		c.reconnectingHandler = func() { handler(nil) }
	case "reconnect":
		c.reconnectHandler = func() { handler(nil) }
	case "reconnect_failed":
		c.reconnectFailedHand = func() { handler(nil) }
	}
}

func (c *Client) Close() error {
	// Mark the client as intentionally closing BEFORE stopping the transport so
	// the reconnect supervisor treats the resulting drop as a graceful shutdown
	// and does not attempt to reconnect.
	atomic.StoreUint32(&c.closing, 1)

	// Write-lock to prevent new Send() calls from acquiring the transport
	// while we are tearing it down. Setting transport to nil ensures that
	// any Send() arriving after Close releases the lock will see nil and
	// fail fast instead of writing on a stopped transport.
	c.transportMu.Lock()
	t := c.transport
	c.transport = nil
	transportClosed := c.transportClosed
	c.transportMu.Unlock()

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

	// The reconnect supervisor is the sole owner of transportClosed once the
	// connection is established. If it is running, let it drain transportClosed
	// (Stop() just triggered the close notification) and wait for it to exit. If
	// no supervisor is running — the handshake never completed, or we are being
	// called from the supervisor itself on reconnect exhaustion — drain
	// transportClosed here so the messageLoop teardown can proceed.
	if atomic.LoadUint32(&c.supervisorStarted) == 1 {
		if c.supervisorDone != nil {
			<-c.supervisorDone
		}
	} else if transportClosed != nil {
		<-transportClosed
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

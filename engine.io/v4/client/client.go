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
	binaryHandler       func([]byte)
	closeHandler        func()
	reconnectAttempts   int
	reconnectWait       time.Duration
	waitUpgrade         chan struct{}
	hadUpgrade          sync.Once
	waitHandshake       chan struct{}
	hadHandshake        sync.Once
	stopPooling         chan struct{}
	transportClosed     chan error
	afterConnect        func()
	messages            chan engineio_v4.Frame
	messagesDone        chan struct{} // closed when messageLoop exits

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

	c.messages = make(chan engineio_v4.Frame, 100)

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

func (c *Client) messageLoop(ctx context.Context, messages <-chan engineio_v4.Frame) {
	if c.messagesDone != nil {
		defer close(c.messagesDone)
	}
	if messages == nil {
		c.log.Errorf("messages channel is nil, can't read transport messages")
		return
	}
	for {
		select {
		case frame, ok := <-messages:
			if !ok {
				return
			}
			err := c.handleFrame(frame)
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

// recordSeparator is the engine.io v4 payload delimiter (U+001E) that joins
// multiple packets inside a single HTTP long-polling body.
const recordSeparator = '\x1e'

// handleFrame dispatches one transport frame. A binary frame is delivered as a
// single attachment to the binary handler. A text frame may carry several
// engine.io packets joined by the record separator (HTTP long-polling), so it
// is split and each record handled in order. WebSocket frames carry exactly one
// record, so splitting is a no-op there.
func (c *Client) handleFrame(frame engineio_v4.Frame) error {
	if frame.IsBinary {
		c.log.Debugf("handle binary frame: %s", c.payload(frame.Data))
		c.handlerMu.RLock()
		handler := c.binaryHandler
		c.handlerMu.RUnlock()
		if handler != nil {
			handler(frame.Data)
		}
		return nil
	}

	for _, record := range splitRecords(frame.Data) {
		if len(record) == 0 {
			continue
		}
		if err := c.handlePacket(record); err != nil {
			return err
		}
	}
	return nil
}

// splitRecords splits a text payload on the engine.io record separator. A
// payload without separators yields a single record, so single-packet frames
// (and every WebSocket frame) flow through unchanged.
func splitRecords(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == recordSeparator {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	return append(out, data[start:])
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
		binaryHandler := c.binaryHandler
		c.handlerMu.RUnlock()
		// A base64 "b"-prefixed record decodes to a binary attachment; route it
		// to the binary handler so socket.io can reassemble binary events.
		if packet.Binary {
			if binaryHandler != nil {
				binaryHandler(packet.Data)
			}
		} else if handler != nil {
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

// SendBinary delivers a raw binary attachment after waiting on the same
// handshake/upgrade gates as Send(), so binary frames are never written on a
// transport that is mid-upgrade or closed. The transport encodes the bytes for
// the active transport (raw binary frame for websocket, base64 record for
// long-polling).
func (c *Client) SendBinary(message []byte) error {
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

	c.transportMu.RLock()
	t := c.transport
	c.transportMu.RUnlock()

	if t == nil {
		return errors.New("client is closed")
	}

	// Pass the raw attachment bytes to the transport; each transport applies its
	// own wire encoding (raw binary frame over websocket, base64 "b"-record over
	// HTTP long-polling).
	return t.SendBinary(message)
}

func (c *Client) On(event string, handler func([]byte)) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()

	switch event {
	case "connect":
		c.afterConnect = func() { handler(nil) }
	case "message":
		c.messageHandler = handler
	case "binary":
		c.binaryHandler = handler
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

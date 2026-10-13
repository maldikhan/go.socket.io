package engineio_v4_client_transport

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
)

type Transport struct {
	log Logger
	ws  WebSocket

	sid    string
	url    *url.URL
	origin *url.URL
	ctx    context.Context

	messages    chan<- engineio_v4.Frame
	onClose     chan<- error
	stopPooling chan struct{}
	readDone    chan struct{}
	mu          sync.RWMutex

	// redactPayload, when true, replaces raw payloads in debug logs with a
	// size marker. Zero value is verbose; NewTransport sets the safe default.
	redactPayload bool
}

// payload returns a size marker when redaction is enabled, or the raw data.
func (c *Transport) payload(data []byte) string {
	if c.redactPayload {
		return fmt.Sprintf("[redacted %d bytes]", len(data))
	}
	return string(data)
}

func (c *Transport) Transport() engineio_v4.EngineIOTransport {
	return engineio_v4.TransportWebsocket
}

func (c *Transport) Stop() (err error) {
	c.mu.RLock()
	stop := c.stopPooling
	c.mu.RUnlock()
	// Use non-blocking send: if wsReadLoop already exited (e.g. via
	// ctx.Done() or a WebSocket error), nobody is reading from
	// stopPooling and a blocking send would deadlock.
	select {
	case stop <- struct{}{}:
	default:
	}
	return nil
}

func (c *Transport) Run(
	ctx context.Context,
	url *url.URL,
	sid string,
	messagesChan chan<- engineio_v4.Frame,
	onClose chan<- error,
) error {
	c.mu.Lock()
	c.ctx = ctx
	c.sid = sid
	c.url = url
	c.messages = messagesChan
	c.onClose = onClose
	c.stopPooling = make(chan struct{}, 1)
	c.mu.Unlock()
	return c.connectWebSocket()
}

func (c *Transport) RequestHandshake() error {
	return nil
}

func (c *Transport) SetHandshake(handshake *engineio_v4.HandshakeResponse) {
	c.mu.Lock()
	c.sid = handshake.Sid
	c.mu.Unlock()
}

func (c *Transport) buildWsUrl() *url.URL {
	c.mu.RLock()
	sid := c.sid
	c.mu.RUnlock()

	wsURL := &url.URL{
		Scheme: "ws",
		Host:   c.url.Host,
		Path:   c.url.Path,
	}
	if c.url.Scheme == "https" {
		wsURL.Scheme = "wss"
	}

	q, err := url.ParseQuery(c.url.RawQuery)
	if err != nil {
		c.log.Errorf("malformed query on url: %s", err)
	}

	query := wsURL.Query()

	for k, v := range q {
		query[k] = v
	}

	query.Set("transport", "websocket")
	query.Set("EIO", "4")
	query.Set("sid", sid)

	wsURL.RawQuery = query.Encode()
	return wsURL
}

func (c *Transport) connectWebSocket() error {
	c.log.Debugf("open ws")
	// Each read loop owns immutable cycle state. Reconnect reuses this
	// transport, so old loops must never read a newer run's channels/context.
	c.mu.RLock()
	cycle := &Transport{
		log: c.log, ws: c.ws, sid: c.sid, url: c.url, origin: c.origin,
		ctx: c.ctx, messages: c.messages, stopPooling: c.stopPooling,
		onClose: make(chan error, 1), redactPayload: c.redactPayload,
	}
	onClose := c.onClose
	c.mu.RUnlock()

	origin := cycle.origin
	if origin == nil {
		origin = cycle.url
	}

	err := cycle.ws.Dial(cycle.ctx, cycle.buildWsUrl(), origin)
	if err != nil {
		return err
	}

	go func() {
		err := cycle.wsReadLoop()
		if err != nil {
			c.log.Errorf("wsReadLoop: %s", err)
		}
		closeErr := cycle.ws.Close()
		if closeErr != nil {
			c.log.Errorf("wsClose: %s", closeErr)
		}
		// Close unblocks the in-flight Receive. Wait until it has returned
		// before allowing a new Dial to replace the shared WebSocket.
		if cycle.readDone != nil {
			<-cycle.readDone
		}
		select {
		case onClose <- err:
		default:
		}
		// Reconnection is handled at the engine.io Client level: wsReadLoop
		// reports the drop (with its error) via onClose, and the client's
		// reconnect supervisor performs a fresh handshake / transport
		// re-selection. The transport intentionally does not reconnect itself.
	}()

	return nil
}

func (c *Transport) wsReadLoop() error {
	c.log.Debugf("run ws read loop")

	// Buffered (size 1) so that a per-iteration reader goroutine still blocked
	// in c.ws.Receive() when wsReadLoop returns can always complete its send
	// and exit, instead of leaking. After wsReadLoop returns, connectWebSocket
	// calls c.ws.Close(), which unblocks Receive() with an error; the goroutine
	// then delivers that error into the buffer (no reader required) and exits.
	// Only one reader goroutine is ever in flight at a time, so a capacity of 1
	// is sufficient.
	messageCh := make(chan engineio_v4.Frame, 1)
	errorCh := make(chan error, 1)
	for {
		// Run ws read in goroutine
		readDone := make(chan struct{})
		c.readDone = readDone
		go func() {
			defer close(readDone)
			var message []byte
			var isBinary bool
			var err error
			if binaryWS, ok := c.ws.(BinaryWebSocket); ok {
				isBinary, err = binaryWS.ReceiveFrame(&message)
			} else {
				err = c.ws.Receive(&message)
			}
			if err != nil {
				errorCh <- err
				return
			}
			messageCh <- engineio_v4.Frame{Data: message, IsBinary: isBinary}
		}()

		select {
		case <-c.stopPooling:
			c.log.Debugf("Context cancelled, exiting ws read loop")
			select {
			case c.onClose <- nil:
			default:
			}
			return nil

		case <-c.ctx.Done():
			// Context cancelled
			c.log.Debugf("Context cancelled, exiting ws read loop")
			select {
			case c.onClose <- c.ctx.Err():
			default:
			}
			return c.ctx.Err()

		case frame := <-messageCh:
			// New message received
			c.log.Debugf("receiveWs: %s", c.payload(frame.Data))
			select {
			case c.messages <- frame:
			case <-c.stopPooling:
				c.log.Debugf("Stop signal received, exiting ws read loop")
				select {
				case c.onClose <- nil:
				default:
				}
				return nil
			case <-c.ctx.Done():
				c.log.Debugf("Context cancelled, exiting ws read loop")
				select {
				case c.onClose <- c.ctx.Err():
				default:
				}
				return c.ctx.Err()
			}

		case err := <-errorCh:
			// WebSocket error
			c.log.Errorf("receiveWsError: %v", err)
			select {
			case c.onClose <- err:
			default:
			}
			return err
		}
	}
}

func (c *Transport) SendMessage(msg []byte) error {
	c.log.Debugf("sendWs: %s", c.payload(msg))
	return c.ws.Send(msg)
}

// SendBinary writes a raw binary attachment as a WebSocket binary frame. The
// engine.io binary marker / base64 encoding is not applied over websocket:
// binary travels in its own frame type.
func (c *Transport) SendBinary(msg []byte) error {
	c.log.Debugf("sendWsBinary: %s", c.payload(msg))
	binaryWS, ok := c.ws.(BinaryWebSocket)
	if !ok {
		return errors.New("websocket implementation does not support binary attachments")
	}
	return binaryWS.SendBinary(msg)
}

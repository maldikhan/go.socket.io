package engineio_v4_client_transport

import (
	"context"
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

	messages    chan<- []byte
	onClose     chan<- error
	stopPooling chan struct{}
	mu          sync.RWMutex
}

func (c *Transport) Transport() engineio_v4.EngineIOTransport {
	return engineio_v4.TransportWebsocket
}

func (c *Transport) Stop() (err error) {
	// Use non-blocking send: if wsReadLoop already exited (e.g. via
	// ctx.Done() or a WebSocket error), nobody is reading from
	// stopPooling and a blocking send would deadlock.
	select {
	case c.stopPooling <- struct{}{}:
	default:
	}
	return nil
}

func (c *Transport) Run(
	ctx context.Context,
	url *url.URL,
	sid string,
	messagesChan chan<- []byte,
	onClose chan<- error,
) error {
	c.ctx = ctx
	c.mu.Lock()
	c.sid = sid
	c.mu.Unlock()
	c.url = url
	c.messages = messagesChan
	c.onClose = onClose
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

	origin := c.origin
	if origin == nil {
		origin = c.url
	}

	err := c.ws.Dial(c.ctx, c.buildWsUrl(), origin)
	if err != nil {
		return err
	}

	go func() {
		err := c.wsReadLoop()
		if err != nil {
			c.log.Errorf("wsReadLoop: %s", err)
		}
		err = c.ws.Close()
		if err != nil {
			c.log.Errorf("wsClose: %s", err)
		}
		// TODO: Reconnect
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
	messageCh := make(chan []byte, 1)
	errorCh := make(chan error, 1)
	for {
		// Run ws read in goroutine
		go func() {
			var message []byte
			err := c.ws.Receive(&message)
			if err != nil {
				errorCh <- err
				return
			}
			messageCh <- message
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

		case message := <-messageCh:
			// New message received
			c.log.Debugf("receiveWs: %s", message)
			select {
			case c.messages <- message:
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
	c.log.Debugf("sendWs: %s", string(msg))
	return c.ws.Send(msg)
}

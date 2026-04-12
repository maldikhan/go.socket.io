package engineio_v4_client_transport

import (
	"context"
	"net/url"

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
	c.sid = sid
	c.url = url
	c.messages = messagesChan
	c.onClose = onClose
	return c.connectWebSocket()
}

func (c *Transport) RequestHandshake() error {
	return nil
}

func (c *Transport) SetHandshake(handshake *engineio_v4.HandshakeResponse) {
	c.sid = handshake.Sid
}

func (c *Transport) buildWsUrl() *url.URL {
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
	query.Set("sid", c.sid)

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

	// Make buffered channels for messages and errors.
	// Buffering(1) allows the read goroutine to send its result before
	// wsReadLoop may have exited, preventing goroutine leaks.
	messageCh := make(chan []byte, 1)
	errorCh := make(chan error, 1)

	// Spawn exactly ONE read goroutine before the loop.
	// This goroutine runs in an infinite loop, continuously reading from
	// the WebSocket and writing to the buffered channels.
	go func() {
		for {
			var message []byte
			err := c.ws.Receive(&message)
			if err != nil {
				errorCh <- err
				return
			}
			messageCh <- message
		}
	}()

	// Main select loop handles incoming messages and control signals.
	// The read goroutine above will be unblocked when ws.Close() is called
	// (in connectWebSocket after wsReadLoop returns).
	for {
		select {
		case <-c.stopPooling:
			c.log.Debugf("Stop signal received, exiting ws read loop")
			c.onClose <- nil
			return nil

		case <-c.ctx.Done():
			// Context cancelled
			c.log.Debugf("Context cancelled, exiting ws read loop")
			c.onClose <- c.ctx.Err()
			return c.ctx.Err()

		case message := <-messageCh:
			// New message received
			c.log.Debugf("receiveWs: %s", message)
			// Use nested select so we can still respond to stop/cancel
			// while waiting to deliver the message downstream.
			select {
			case c.messages <- message:
			case <-c.stopPooling:
				c.log.Debugf("Stop signal received, exiting ws read loop")
				c.onClose <- nil
				return nil
			case <-c.ctx.Done():
				c.log.Debugf("Context cancelled, exiting ws read loop")
				c.onClose <- c.ctx.Err()
				return c.ctx.Err()
			}

		case err := <-errorCh:
			// WebSocket error - drain any pending message before returning error
			select {
			case msg := <-messageCh:
				c.messages <- msg
			default:
			}
			c.log.Errorf("receiveWsError: %v", err)
			c.onClose <- err
			return err
		}
	}
}

func (c *Transport) SendMessage(msg []byte) error {
	c.log.Debugf("sendWs: %s", string(msg))
	return c.ws.Send(msg)
}

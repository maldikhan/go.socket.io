package engineio_v4_client_transport

import (
	"context"
	"net/url"

	engineio_v4 "maldikhan/go.socket.io/engine.io/v4"
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
	c.stopPooling <- struct{}{}
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

	query := wsURL.Query()

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
		c.ws.Close()
		// TODO: Reconnect
	}()

	return nil
}

func (c *Transport) wsReadLoop() error {
	c.log.Debugf("run ws read loop")

	// Создаем канал для получения WebSocket сообщений
	messageCh := make(chan []byte)
	errorCh := make(chan error)
	for {
		// Запускаем горутину для чтения WebSocket сообщений
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
			c.onClose <- nil
			return nil

		case <-c.ctx.Done():
			// Контекст отменен, выходим из цикла
			c.log.Debugf("Context cancelled, exiting ws read loop")
			c.onClose <- c.ctx.Err()
			return c.ctx.Err()

		case message := <-messageCh:
			// Получено сообщение WebSocket
			c.log.Debugf("receiveWs: %s", message)
			c.messages <- message

		case err := <-errorCh:
			// Произошла ошибка при чтении WebSocket
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

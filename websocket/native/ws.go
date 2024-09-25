package ws_native

import (
	"context"
	"errors"
	"net/url"

	"golang.org/x/net/websocket"
)

type WebSocketConnection struct {
	conn *websocket.Conn
}

var ErrNotConnected = errors.New("socket connection is not initialized")

func (ws *WebSocketConnection) Dial(ctx context.Context, url *url.URL, origin *url.URL) error {
	var err error
	config, err := websocket.NewConfig(url.String(), origin.String())
	if err != nil {
		return err
	}
	ws.conn, err = config.DialContext(ctx)
	return err
}

func (ws *WebSocketConnection) Send(v []byte) error {
	if ws.conn == nil {
		return ErrNotConnected
	}
	return websocket.Message.Send(ws.conn, string(v))
}

func (ws *WebSocketConnection) Receive(v *[]byte) error {
	if ws.conn == nil {
		return ErrNotConnected
	}
	return websocket.Message.Receive(ws.conn, v)
}

func (ws *WebSocketConnection) Close() error {
	if ws.conn == nil {
		return nil
	}
	return ws.conn.Close()
}

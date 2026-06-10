package ws_native

import (
	"context"
	"errors"
	"net/url"
	"sync"

	"golang.org/x/net/websocket"
)

type WebSocketConnection struct {
	mu   sync.Mutex
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
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return websocket.Message.Send(ws.conn, string(v))
}

// SendBinary writes v as a WebSocket binary frame (opcode 2). Engine.io v4
// carries socket.io binary attachments as binary frames over websocket.
func (ws *WebSocketConnection) SendBinary(v []byte) error {
	if ws.conn == nil {
		return ErrNotConnected
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return websocket.Message.Send(ws.conn, v)
}

func (ws *WebSocketConnection) Receive(v *[]byte) error {
	if ws.conn == nil {
		return ErrNotConnected
	}
	return websocket.Message.Receive(ws.conn, v)
}

// ReceiveFrame reads the next frame and reports whether it was a binary frame
// (opcode 2). It uses a codec that preserves the payload type so the caller can
// distinguish binary attachments from text packets, which the default
// websocket.Message codec collapses. Text and binary frames are both returned
// as raw bytes in *v.
func (ws *WebSocketConnection) ReceiveFrame(v *[]byte) (isBinary bool, err error) {
	if ws.conn == nil {
		return false, ErrNotConnected
	}
	pt := byte(websocket.TextFrame)
	codec := websocket.Codec{
		Unmarshal: func(data []byte, payloadType byte, dst interface{}) error {
			pt = payloadType
			*(dst.(*[]byte)) = data
			return nil
		},
	}
	if err := codec.Receive(ws.conn, v); err != nil {
		return false, err
	}
	return pt == byte(websocket.BinaryFrame), nil
}

func (ws *WebSocketConnection) Close() error {
	if ws.conn == nil {
		return nil
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.conn.Close()
}

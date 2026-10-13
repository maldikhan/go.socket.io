package ws_native

import (
	"context"
	"errors"
	"net/url"
	"sync"

	"golang.org/x/net/websocket"
)

type WebSocketConnection struct {
	mu     sync.RWMutex
	sendMu sync.Mutex
	conn   *websocket.Conn
}

var ErrNotConnected = errors.New("socket connection is not initialized")

func (ws *WebSocketConnection) Dial(ctx context.Context, url *url.URL, origin *url.URL) error {
	var err error
	config, err := websocket.NewConfig(url.String(), origin.String())
	if err != nil {
		return err
	}
	conn, err := config.DialContext(ctx)
	if err != nil {
		return err
	}
	ws.mu.Lock()
	previous := ws.conn
	ws.conn = conn
	ws.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return nil
}

func (ws *WebSocketConnection) Send(v []byte) error {
	ws.mu.RLock()
	conn := ws.conn
	ws.mu.RUnlock()
	if conn == nil {
		return ErrNotConnected
	}
	ws.sendMu.Lock()
	defer ws.sendMu.Unlock()
	return websocket.Message.Send(conn, string(v))
}

// SendBinary writes v as a WebSocket binary frame (opcode 2). Engine.io v4
// carries socket.io binary attachments as binary frames over websocket.
func (ws *WebSocketConnection) SendBinary(v []byte) error {
	ws.mu.RLock()
	conn := ws.conn
	ws.mu.RUnlock()
	if conn == nil {
		return ErrNotConnected
	}
	ws.sendMu.Lock()
	defer ws.sendMu.Unlock()
	return websocket.Message.Send(conn, v)
}

func (ws *WebSocketConnection) Receive(v *[]byte) error {
	ws.mu.RLock()
	conn := ws.conn
	ws.mu.RUnlock()
	if conn == nil {
		return ErrNotConnected
	}
	return websocket.Message.Receive(conn, v)
}

// ReceiveFrame reads the next frame and reports whether it was a binary frame
// (opcode 2). It uses a codec that preserves the payload type so the caller can
// distinguish binary attachments from text packets, which the default
// websocket.Message codec collapses. Text and binary frames are both returned
// as raw bytes in *v.
func (ws *WebSocketConnection) ReceiveFrame(v *[]byte) (isBinary bool, err error) {
	ws.mu.RLock()
	conn := ws.conn
	ws.mu.RUnlock()
	if conn == nil {
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
	if err := codec.Receive(conn, v); err != nil {
		return false, err
	}
	return pt == byte(websocket.BinaryFrame), nil
}

func (ws *WebSocketConnection) Close() error {
	ws.mu.Lock()
	conn := ws.conn
	ws.conn = nil
	ws.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

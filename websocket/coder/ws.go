// Package ws_coder provides an optional WebSocket backend with compression,
// message size limits and RFC 6455 close handshakes.
package ws_coder

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/coder/websocket"

	transport "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket"
)

var (
	ErrNotConnected     = errors.New("socket connection is not initialized")
	ErrAlreadyConnected = errors.New("socket connection is already connecting or connected")
)

// WebSocketConnection implements the engine.io WebSocket interface. A single
// Receive or ReceiveFrame may run concurrently with Send, SendBinary and Close.
// Dial must follow Close before the backend can be reused for a new session.
type WebSocketConnection struct {
	mu         sync.RWMutex
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	generation uint64

	httpClient  *http.Client
	compression websocket.CompressionMode
	readLimit   int64
}

var _ transport.WebSocket = (*WebSocketConnection)(nil)

type Option func(*WebSocketConnection) error

// New constructs a backend. Compression is disabled by default and incoming
// messages are limited to 4 MiB, matching the polling transport's default.
func New(options ...Option) (*WebSocketConnection, error) {
	ws := &WebSocketConnection{readLimit: 4 * 1024 * 1024}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("nil WebSocket option")
		}
		if err := option(ws); err != nil {
			return nil, err
		}
	}
	return ws, nil
}

// WithCompression enables negotiation of permessage-deflate with the peer.
func WithCompression(mode websocket.CompressionMode) Option {
	return func(ws *WebSocketConnection) error {
		switch mode {
		case websocket.CompressionDisabled, websocket.CompressionContextTakeover, websocket.CompressionNoContextTakeover:
			ws.compression = mode
			return nil
		default:
			return fmt.Errorf("invalid WebSocket compression mode: %d", mode)
		}
	}
}

// WithReadLimit limits the size of each inbound text or binary message. A limit
// of -1 disables the limit; other non-positive values are rejected.
func WithReadLimit(limit int64) Option {
	return func(ws *WebSocketConnection) error {
		if limit != -1 && limit <= 0 {
			return errors.New("WebSocket read limit must be positive or -1")
		}
		ws.readLimit = limit
		return nil
	}
}

// WithHTTPClient uses client for the HTTP upgrade request, allowing custom TLS,
// proxy, cookie and timeout settings. Its transport must support WebSocket dials.
func WithHTTPClient(client *http.Client) Option {
	return func(ws *WebSocketConnection) error {
		if client == nil {
			return errors.New("WebSocket HTTP client is nil")
		}
		ws.httpClient = client
		return nil
	}
}

// Dial connects and uses ctx for the whole session. Close can abort an in-flight
// dial; a connection that completes after Close is discarded instead of leaked.
func (ws *WebSocketConnection) Dial(ctx context.Context, endpoint *url.URL, origin *url.URL) error {
	if endpoint == nil {
		return errors.New("WebSocket URL is nil")
	}
	ws.mu.Lock()
	if ws.cancel != nil {
		ws.mu.Unlock()
		return ErrAlreadyConnected
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	ws.generation++
	generation := ws.generation
	ws.ctx, ws.cancel = sessionCtx, cancel
	ws.mu.Unlock()

	headers := make(http.Header)
	if origin != nil {
		headers.Set("Origin", origin.String())
	}
	conn, _, err := websocket.Dial(sessionCtx, endpoint.String(), &websocket.DialOptions{
		HTTPClient:      ws.httpClient,
		HTTPHeader:      headers,
		CompressionMode: ws.compression,
	})
	if err != nil {
		cancel()
		ws.mu.Lock()
		if ws.generation == generation {
			ws.ctx, ws.cancel = nil, nil
		}
		ws.mu.Unlock()
		return err
	}
	readLimit := ws.readLimit
	if readLimit == 0 { // Keep the zero value useful too.
		readLimit = 4 * 1024 * 1024
	}
	conn.SetReadLimit(readLimit)
	context.AfterFunc(sessionCtx, func() { _ = conn.CloseNow() })

	ws.mu.Lock()
	if ws.generation != generation || sessionCtx.Err() != nil {
		err := sessionCtx.Err()
		if err == nil {
			err = context.Canceled
		}
		if ws.generation == generation {
			ws.ctx, ws.cancel = nil, nil
		}
		ws.mu.Unlock()
		cancel()
		_ = conn.CloseNow()
		return err
	}
	ws.conn = conn
	ws.mu.Unlock()
	return nil
}

func (ws *WebSocketConnection) snapshot() (*websocket.Conn, context.Context) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.conn, ws.ctx
}

// Send writes an engine.io text frame.
func (ws *WebSocketConnection) Send(v []byte) error {
	return ws.write(websocket.MessageText, v)
}

// SendBinary writes a binary attachment without converting it to text.
func (ws *WebSocketConnection) SendBinary(v []byte) error {
	return ws.write(websocket.MessageBinary, v)
}

func (ws *WebSocketConnection) write(kind websocket.MessageType, v []byte) error {
	conn, ctx := ws.snapshot()
	if conn == nil {
		return ErrNotConnected
	}
	return conn.Write(ctx, kind, v)
}

// Receive reads the next message, preserving its bytes regardless of frame type.
func (ws *WebSocketConnection) Receive(v *[]byte) error {
	_, err := ws.ReceiveFrame(v)
	return err
}

// ReceiveFrame reads a text or binary message and preserves the frame type for
// engine.io binary attachments. It also processes WebSocket ping/pong frames.
func (ws *WebSocketConnection) ReceiveFrame(v *[]byte) (bool, error) {
	if v == nil {
		return false, errors.New("WebSocket receive destination is nil")
	}
	conn, ctx := ws.snapshot()
	if conn == nil {
		return false, ErrNotConnected
	}
	kind, payload, err := conn.Read(ctx)
	if err != nil {
		return false, err
	}
	*v = payload
	return kind == websocket.MessageBinary, nil
}

// Close sends a normal close handshake and releases session resources. Calls
// after the first Close are no-ops. The handshake is bounded by the upstream
// backend's write/read timeouts (up to 5 seconds each for an unresponsive peer).
func (ws *WebSocketConnection) Close() error {
	ws.mu.Lock()
	conn, cancel := ws.conn, ws.cancel
	ws.conn, ws.ctx, ws.cancel = nil, nil, nil
	ws.generation++
	ws.mu.Unlock()
	if cancel != nil {
		defer cancel()
	}
	if conn == nil {
		return nil
	}
	return conn.Close(websocket.StatusNormalClosure, "")
}

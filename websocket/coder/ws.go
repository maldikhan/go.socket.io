// Package ws_coder provides a WebSocket connection backend based on
// github.com/coder/websocket (the actively maintained continuation of
// nhooyr.io/websocket).
//
// Compared to the default zero-dependency backend (websocket/native, built on
// the frozen golang.org/x/net/websocket), this backend supports the
// permessage-deflate compression extension, performs a proper RFC 6455 close
// handshake and enforces a configurable read limit.
//
// It is opt-in: build a connection with New() and plug it into the engine.io
// WebSocket transport via its WithWebSocket option.
package ws_coder

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"

	"github.com/coder/websocket"
)

// defaultReadLimit caps a single inbound message; it matches the polling
// transport default (4MB, socket.io JS maxHttpBufferSize).
const defaultReadLimit = 4 * 1024 * 1024

var ErrNotConnected = errors.New("socket connection is not initialized")

// WebSocketConnection implements the engine.io transport WebSocket interface
// on top of github.com/coder/websocket.
type WebSocketConnection struct {
	// mu guards conn and ctx, which are written by Dial() and read by
	// Send()/Receive()/Close() from other goroutines.
	mu   sync.Mutex
	conn *websocket.Conn
	// ctx is the context passed to Dial(); the engine.io transport scopes it
	// to the connection lifetime, so reads and writes are bound to it.
	ctx context.Context

	httpClient  *http.Client
	compression websocket.CompressionMode
	readLimit   int64
}

type Option func(*WebSocketConnection) error

// New builds a WebSocketConnection ready to be passed to the engine.io
// WebSocket transport via its WithWebSocket option.
func New(options ...Option) (*WebSocketConnection, error) {
	ws := &WebSocketConnection{
		compression: websocket.CompressionDisabled,
		readLimit:   defaultReadLimit,
	}
	for _, option := range options {
		if err := option(ws); err != nil {
			return nil, err
		}
	}
	return ws, nil
}

// WithHTTPClient sets the HTTP client used for the WebSocket handshake
// (e.g. to customize TLS configuration or proxies).
func WithHTTPClient(client *http.Client) Option {
	return func(ws *WebSocketConnection) error {
		if client == nil {
			return errors.New("http client is nil")
		}
		ws.httpClient = client
		return nil
	}
}

// WithCompression enables negotiation of the permessage-deflate extension.
// Compression is disabled by default; see the documentation of
// websocket.CompressionMode for the trade-offs of each mode.
func WithCompression(mode websocket.CompressionMode) Option {
	return func(ws *WebSocketConnection) error {
		ws.compression = mode
		return nil
	}
}

// WithReadLimit overrides the maximum size of a single inbound message
// (default 4MB). Pass -1 to disable the limit entirely.
func WithReadLimit(limit int64) Option {
	return func(ws *WebSocketConnection) error {
		if limit <= 0 && limit != -1 {
			return fmt.Errorf("read limit must be positive or -1 (unlimited), got %d", limit)
		}
		ws.readLimit = limit
		return nil
	}
}

func (ws *WebSocketConnection) Dial(ctx context.Context, u *url.URL, origin *url.URL) error {
	opts := &websocket.DialOptions{
		HTTPClient:      ws.httpClient,
		CompressionMode: ws.compression,
	}
	if origin != nil {
		opts.HTTPHeader = http.Header{"Origin": []string{origin.String()}}
	}

	conn, _, err := websocket.Dial(ctx, u.String(), opts)
	if err != nil {
		return err
	}
	conn.SetReadLimit(ws.readLimit)

	ws.mu.Lock()
	ws.conn = conn
	ws.ctx = ctx
	ws.mu.Unlock()
	return nil
}

func (ws *WebSocketConnection) Send(v []byte) error {
	conn, ctx := ws.snapshot()
	if conn == nil {
		return ErrNotConnected
	}
	// Engine.IO v4 packets are text frames; conn.Write is safe for
	// concurrent use, no extra locking is needed here.
	return conn.Write(ctx, websocket.MessageText, v)
}

func (ws *WebSocketConnection) Receive(v *[]byte) error {
	conn, ctx := ws.snapshot()
	if conn == nil {
		return ErrNotConnected
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	*v = data
	return nil
}

// Close performs the RFC 6455 close handshake (close frame exchange) and
// releases the connection resources. It unblocks a concurrent Receive() and
// is a no-op when the connection was never established or is already closed.
func (ws *WebSocketConnection) Close() error {
	conn, _ := ws.snapshot()
	if conn == nil {
		return nil
	}
	err := conn.Close(websocket.StatusNormalClosure, "")
	if errors.Is(err, net.ErrClosed) {
		// Already closed (e.g. by a concurrent Close or a failed read):
		// closing is idempotent, mirror the native backend behavior.
		return nil
	}
	return err
}

// snapshot returns the current connection and its context without holding the
// lock during (potentially blocking) network IO.
func (ws *WebSocketConnection) snapshot() (*websocket.Conn, context.Context) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.conn, ws.ctx
}

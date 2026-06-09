package ws_coder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startEchoServer runs a WebSocket echo server and returns its ws:// and
// http:// URLs. The server echoes every message back and records the Origin
// header of the last handshake.
func startEchoServer(t *testing.T) (wsURL *url.URL, httpURL *url.URL, lastOrigin *string) {
	t.Helper()

	origin := new(string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*origin = r.Header.Get("Origin")
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			CompressionMode: websocket.CompressionContextTakeover,
		})
		if err != nil {
			return
		}
		defer conn.CloseNow() //nolint:errcheck
		for {
			msgType, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), msgType, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	httpURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	wsCopy := *httpURL
	wsCopy.Scheme = "ws"
	return &wsCopy, httpURL, origin
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("defaults", func(t *testing.T) {
		ws, err := New()
		require.NoError(t, err)
		assert.Equal(t, int64(defaultReadLimit), ws.readLimit)
		assert.Equal(t, websocket.CompressionDisabled, ws.compression)
		assert.Nil(t, ws.httpClient)
	})

	t.Run("with options", func(t *testing.T) {
		httpClient := &http.Client{}
		ws, err := New(
			WithHTTPClient(httpClient),
			WithCompression(websocket.CompressionContextTakeover),
			WithReadLimit(1024),
		)
		require.NoError(t, err)
		assert.Same(t, httpClient, ws.httpClient)
		assert.Equal(t, websocket.CompressionContextTakeover, ws.compression)
		assert.Equal(t, int64(1024), ws.readLimit)
	})

	t.Run("unlimited read limit", func(t *testing.T) {
		ws, err := New(WithReadLimit(-1))
		require.NoError(t, err)
		assert.Equal(t, int64(-1), ws.readLimit)
	})

	t.Run("nil http client", func(t *testing.T) {
		ws, err := New(WithHTTPClient(nil))
		assert.Nil(t, ws)
		assert.ErrorContains(t, err, "http client is nil")
	})

	t.Run("invalid read limit", func(t *testing.T) {
		for _, limit := range []int64{0, -2} {
			ws, err := New(WithReadLimit(limit))
			assert.Nil(t, ws)
			assert.ErrorContains(t, err, "read limit must be positive")
		}
	})
}

func TestDialSendReceive(t *testing.T) {
	t.Parallel()

	wsURL, httpURL, lastOrigin := startEchoServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, err := New()
	require.NoError(t, err)
	require.NoError(t, ws.Dial(ctx, wsURL, httpURL))
	defer ws.Close() //nolint:errcheck

	assert.Equal(t, httpURL.String(), *lastOrigin, "origin header should be sent")

	require.NoError(t, ws.Send([]byte("hello")))

	var got []byte
	require.NoError(t, ws.Receive(&got))
	assert.Equal(t, []byte("hello"), got)
}

func TestDialWithCompression(t *testing.T) {
	t.Parallel()

	wsURL, httpURL, _ := startEchoServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, err := New(WithCompression(websocket.CompressionContextTakeover))
	require.NoError(t, err)
	require.NoError(t, ws.Dial(ctx, wsURL, httpURL))
	defer ws.Close() //nolint:errcheck

	// Bigger than the 128-byte compression threshold so the message actually
	// goes through the permessage-deflate path.
	payload := []byte(strings.Repeat("compressible payload ", 32))
	require.NoError(t, ws.Send(payload))

	var got []byte
	require.NoError(t, ws.Receive(&got))
	assert.Equal(t, payload, got)
}

func TestDialNilOrigin(t *testing.T) {
	t.Parallel()

	wsURL, _, lastOrigin := startEchoServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, err := New()
	require.NoError(t, err)
	require.NoError(t, ws.Dial(ctx, wsURL, nil))
	defer ws.Close() //nolint:errcheck

	assert.Empty(t, *lastOrigin, "no origin header should be sent")
}

func TestDialError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	t.Run("unreachable server", func(t *testing.T) {
		// Reserve a port and close the listener so the dial is refused.
		server := httptest.NewServer(http.NotFoundHandler())
		addr := server.Listener.Addr().String()
		server.Close()

		ws, err := New()
		require.NoError(t, err)
		assert.Error(t, ws.Dial(ctx, &url.URL{Scheme: "ws", Host: addr}, nil))
	})

	t.Run("handshake rejected", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(server.Close)
		serverURL, err := url.Parse(server.URL)
		require.NoError(t, err)
		serverURL.Scheme = "ws"

		ws, err := New()
		require.NoError(t, err)
		assert.Error(t, ws.Dial(ctx, serverURL, nil))
	})
}

func TestNotConnected(t *testing.T) {
	t.Parallel()

	ws, err := New()
	require.NoError(t, err)

	assert.ErrorIs(t, ws.Send([]byte("data")), ErrNotConnected)

	var buf []byte
	assert.ErrorIs(t, ws.Receive(&buf), ErrNotConnected)

	assert.NoError(t, ws.Close(), "closing a never-connected socket is a no-op")
}

func TestReadLimit(t *testing.T) {
	t.Parallel()

	wsURL, httpURL, _ := startEchoServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, err := New(WithReadLimit(8))
	require.NoError(t, err)
	require.NoError(t, ws.Dial(ctx, wsURL, httpURL))
	defer ws.Close() //nolint:errcheck

	require.NoError(t, ws.Send([]byte("payload larger than the limit")))

	var got []byte
	assert.Error(t, ws.Receive(&got), "oversized message must be rejected")
}

func TestClose(t *testing.T) {
	t.Parallel()

	wsURL, httpURL, _ := startEchoServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("graceful close and idempotency", func(t *testing.T) {
		ws, err := New()
		require.NoError(t, err)
		require.NoError(t, ws.Dial(ctx, wsURL, httpURL))

		assert.NoError(t, ws.Close())
		assert.NoError(t, ws.Close(), "second close must be a no-op")

		var buf []byte
		assert.Error(t, ws.Receive(&buf), "receive after close must fail")
	})

	t.Run("close unblocks a pending receive", func(t *testing.T) {
		ws, err := New()
		require.NoError(t, err)
		require.NoError(t, ws.Dial(ctx, wsURL, httpURL))

		received := make(chan error, 1)
		go func() {
			var buf []byte
			received <- ws.Receive(&buf)
		}()

		// Give the reader a moment to block on the connection.
		time.Sleep(50 * time.Millisecond)
		require.NoError(t, ws.Close())

		select {
		case err := <-received:
			assert.Error(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Receive was not unblocked by Close")
		}
	})
}

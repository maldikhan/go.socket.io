package ws_coder

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func endpoint(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func newBackend(t *testing.T, options ...Option) *WebSocketConnection {
	t.Helper()
	ws, err := New(options...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

// All reads have a deadline so a broken client cannot leave a server goroutine
// running past the test. Each connection exits on the normal close handshake.
func echoServer(t *testing.T, options *websocket.AcceptOptions, requests chan<- *http.Request) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			requests <- r
		}
		conn, err := websocket.Accept(w, r, options)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(8 * 1024 * 1024)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for {
			kind, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if err := conn.Write(ctx, kind, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestNew(t *testing.T) {
	t.Parallel()
	ws := newBackend(t)
	assert.Equal(t, int64(4*1024*1024), ws.readLimit)
	assert.Equal(t, websocket.CompressionDisabled, ws.compression)
	for _, option := range []Option{
		WithReadLimit(0), WithReadLimit(-2), WithCompression(websocket.CompressionMode(99)), WithHTTPClient(nil),
	} {
		ws, err := New(option)
		assert.Nil(t, ws)
		assert.Error(t, err)
	}
	client := &http.Client{}
	ws = newBackend(t, WithReadLimit(-1), WithCompression(websocket.CompressionNoContextTakeover), WithHTTPClient(client))
	assert.Equal(t, int64(-1), ws.readLimit)
	assert.Equal(t, websocket.CompressionNoContextTakeover, ws.compression)
	assert.Same(t, client, ws.httpClient)
}

func TestFramesAndOrigin(t *testing.T) {
	t.Parallel()
	requests := make(chan *http.Request, 1)
	server := echoServer(t, &websocket.AcceptOptions{OriginPatterns: []string{"app.example.com"}}, requests)
	ws := newBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, ws.Dial(ctx, endpoint(t, server.URL), endpoint(t, "https://app.example.com")))
	assert.Equal(t, "https://app.example.com", (<-requests).Header.Get("Origin"))
	for _, message := range []struct {
		binary bool
		data   []byte
	}{
		{false, []byte("4hello")}, {true, []byte{0, 255, 128, 10}}, {true, []byte{}},
	} {
		if message.binary {
			require.NoError(t, ws.SendBinary(message.data))
		} else {
			require.NoError(t, ws.Send(message.data))
		}
		var data []byte
		binary, err := ws.ReceiveFrame(&data)
		require.NoError(t, err)
		assert.Equal(t, message.binary, binary)
		assert.True(t, bytes.Equal(message.data, data))
	}
	require.NoError(t, ws.Send([]byte("legacy Receive")))
	var data []byte
	require.NoError(t, ws.Receive(&data))
	assert.Equal(t, "legacy Receive", string(data))
	require.NoError(t, ws.Close())
	require.NoError(t, ws.Close())
}

func TestCompression(t *testing.T) {
	t.Parallel()
	requests := make(chan *http.Request, 1)
	server := echoServer(t, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover}, requests)
	ws := newBackend(t, WithCompression(websocket.CompressionContextTakeover))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, ws.Dial(ctx, endpoint(t, server.URL), nil))
	assert.Contains(t, (<-requests).Header.Get("Sec-WebSocket-Extensions"), "permessage-deflate")
	data := []byte(strings.Repeat("compressible message ", 100))
	require.NoError(t, ws.Send(data))
	var received []byte
	require.NoError(t, ws.Receive(&received))
	assert.Equal(t, data, received)
}

func TestCustomHTTPClient(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		<-conn.CloseRead(ctx).Done()
	}))
	defer server.Close()
	ws := newBackend(t, WithHTTPClient(server.Client()))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, ws.Dial(ctx, endpoint(t, server.URL), nil))
}

func TestReadLimit(t *testing.T) {
	t.Parallel()
	server := echoServer(t, nil, nil)
	for _, binary := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "binary"}[binary], func(t *testing.T) {
			ws := newBackend(t, WithReadLimit(32))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, ws.Dial(ctx, endpoint(t, server.URL), nil))
			data := bytes.Repeat([]byte{'x'}, 64)
			if binary {
				require.NoError(t, ws.SendBinary(data))
			} else {
				require.NoError(t, ws.Send(data))
			}
			var received []byte
			_, err := ws.ReceiveFrame(&received)
			require.Error(t, err)
			assert.ErrorIs(t, err, websocket.ErrMessageTooBig)
		})
	}
	t.Run("disabled", func(t *testing.T) {
		ws := newBackend(t, WithReadLimit(-1))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, ws.Dial(ctx, endpoint(t, server.URL), nil))
		data := bytes.Repeat([]byte{'x'}, 4*1024*1024+1)
		require.NoError(t, ws.SendBinary(data))
		var received []byte
		require.NoError(t, ws.Receive(&received))
		assert.Equal(t, data, received)
	})
}

func TestNotConnected(t *testing.T) {
	t.Parallel()
	ws := newBackend(t)
	assert.ErrorIs(t, ws.Send(nil), ErrNotConnected)
	assert.ErrorIs(t, ws.SendBinary(nil), ErrNotConnected)
	var data []byte
	assert.ErrorIs(t, ws.Receive(&data), ErrNotConnected)
	_, err := ws.ReceiveFrame(&data)
	assert.ErrorIs(t, err, ErrNotConnected)
	assert.Error(t, ws.Receive(nil))
	assert.Error(t, ws.Dial(context.Background(), nil, nil))
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	assert.Error(t, ws.Dial(context.Background(), endpoint(t, server.URL), nil))
	assert.Nil(t, ws.cancel, "failed dial must release its session context")
}

func TestCloseAbortsDial(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ws := newBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ws.Dial(ctx, endpoint(t, server.URL), nil) }()
	<-started
	require.NoError(t, ws.Close())
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal("Close did not abort the HTTP upgrade request")
	}
	assert.ErrorIs(t, ws.Send(nil), ErrNotConnected)
}

func TestContextCancellationAndReuse(t *testing.T) {
	t.Parallel()
	server := echoServer(t, nil, nil)
	ws := newBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, ws.Dial(ctx, endpoint(t, server.URL), nil))
	assert.ErrorIs(t, ws.Dial(ctx, endpoint(t, server.URL), nil), ErrAlreadyConnected)
	done := make(chan error, 1)
	go func() { var data []byte; done <- ws.Receive(&data) }()
	cancel()
	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("session cancellation did not unblock Receive")
	}
	_ = ws.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, ws.Dial(ctx, endpoint(t, server.URL), nil))
	require.NoError(t, ws.Send([]byte("second session")))
	var data []byte
	require.NoError(t, ws.Receive(&data))
	assert.Equal(t, "second session", string(data))
}

func TestConcurrentSendClose(t *testing.T) {
	t.Parallel()
	server := echoServer(t, nil, nil)
	ws := newBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, ws.Dial(ctx, endpoint(t, server.URL), nil))
	var senders sync.WaitGroup
	for i := 0; i < 32; i++ {
		senders.Add(1)
		go func() { defer senders.Done(); _ = ws.Send([]byte("concurrent")) }()
	}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			var data []byte
			if ws.Receive(&data) != nil {
				return
			}
		}
	}()
	require.NoError(t, ws.Close())
	senders.Wait()
	select {
	case <-readDone:
	case <-ctx.Done():
		t.Fatal("Close did not unblock Receive")
	}
}

func TestCloseHandshakeAndIdleCancellation(t *testing.T) {
	t.Parallel()
	for _, cancelSession := range []bool{false, true} {
		name := "normal close handshake"
		if cancelSession {
			name = "idle session cancellation"
		}
		t.Run(name, func(t *testing.T) {
			peerClosed := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					peerClosed <- err
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _, err = conn.Read(ctx)
				peerClosed <- err
			}))
			defer server.Close()
			ws := newBackend(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, ws.Dial(ctx, endpoint(t, server.URL), nil))
			// No client-side read or write is pending: the session context must
			// still close an idle connection, rather than only aborting Dial.
			if cancelSession {
				cancel()
			} else {
				require.NoError(t, ws.Close())
			}
			select {
			case err := <-peerClosed:
				require.Error(t, err)
				if !cancelSession {
					assert.Equal(t, websocket.StatusNormalClosure, websocket.CloseStatus(err))
				}
			case <-time.After(2 * time.Second):
				t.Fatal("server did not observe the connection closing")
			}
		})
	}
}

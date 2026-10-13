package ws_native

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/websocket"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// binEchoServer starts a websocket server that echoes each frame back,
// preserving its payload type (binary stays binary, text stays text). It
// returns the ws:// URL and a cleanup function.
func binEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	handler := websocket.Handler(func(ws *websocket.Conn) {
		for {
			// Receive into []byte preserves the frame; echo it back as a binary
			// frame so the client observes opcode 2.
			var frame []byte
			if err := websocket.Message.Receive(ws, &frame); err != nil {
				return
			}
			ws.PayloadType = websocket.BinaryFrame
			if err := websocket.Message.Send(ws, frame); err != nil {
				return
			}
		}
	})
	server := httptest.NewServer(handler)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	return wsURL, server.Close
}

func TestWebSocketConnection_SendBinary_NotConnected(t *testing.T) {
	ws := &WebSocketConnection{}
	err := ws.SendBinary([]byte{1, 2, 3})
	assert.ErrorIs(t, err, ErrNotConnected)
}

func TestWebSocketConnection_ReceiveFrame_NotConnected(t *testing.T) {
	ws := &WebSocketConnection{}
	var data []byte
	_, err := ws.ReceiveFrame(&data)
	assert.ErrorIs(t, err, ErrNotConnected)
}

func TestWebSocketConnection_SendBinary_ReceiveFrame(t *testing.T) {
	wsURL, closeServer := binEchoServer(t)
	defer closeServer()

	u, err := url.Parse(wsURL)
	require.NoError(t, err)
	origin, _ := url.Parse("http://localhost/")

	conn := &WebSocketConnection{}
	require.NoError(t, conn.Dial(context.Background(), u, origin))
	defer conn.Close() //nolint:errcheck

	payload := []byte{0x00, 0x01, 0xFF, 0x42}
	require.NoError(t, conn.SendBinary(payload))

	var received []byte
	isBinary, err := conn.ReceiveFrame(&received)
	require.NoError(t, err)
	assert.True(t, isBinary, "echoed frame should be binary")
	assert.Equal(t, payload, received)
}

func TestWebSocketConnection_ReceiveFrame_Error(t *testing.T) {
	wsURL, closeServer := binEchoServer(t)

	u, err := url.Parse(wsURL)
	require.NoError(t, err)
	origin, _ := url.Parse("http://localhost/")

	conn := &WebSocketConnection{}
	require.NoError(t, conn.Dial(context.Background(), u, origin))
	defer closeServer()

	// Close the client connection, then attempt to receive. The read fails
	// inside codec.Receive on the closed connection, exercising the error path
	// (not the nil guard) without blocking on a frame that never arrives.
	require.NoError(t, conn.Close())

	var received []byte
	_, err = conn.ReceiveFrame(&received)
	assert.Error(t, err)
}

func TestWebSocketConnection_ReceiveFrame_Text(t *testing.T) {
	// A text echo server lets us verify ReceiveFrame reports isBinary == false.
	handler := websocket.Handler(func(ws *websocket.Conn) {
		var msg string
		if err := websocket.Message.Receive(ws, &msg); err != nil {
			return
		}
		ws.PayloadType = websocket.TextFrame
		_ = websocket.Message.Send(ws, msg)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	u, err := url.Parse("ws" + strings.TrimPrefix(server.URL, "http"))
	require.NoError(t, err)
	origin, _ := url.Parse("http://localhost/")

	conn := &WebSocketConnection{}
	require.NoError(t, conn.Dial(context.Background(), u, origin))
	defer conn.Close() //nolint:errcheck

	require.NoError(t, conn.Send([]byte("hello")))

	var received []byte
	isBinary, err := conn.ReceiveFrame(&received)
	require.NoError(t, err)
	assert.False(t, isBinary, "echoed text frame should not be binary")
	assert.Equal(t, []byte("hello"), received)
}

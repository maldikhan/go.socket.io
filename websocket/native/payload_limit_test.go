package ws_native

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"
)

func TestWebSocketConnectionRejectsOversizedFrame(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		defer func() { _ = conn.Close() }()
		_ = websocket.Message.Send(conn, strings.Repeat("x", 4*1024*1024+1))
	}))
	defer server.Close()
	u, err := url.Parse(strings.Replace(server.URL, "http://", "ws://", 1))
	require.NoError(t, err)
	origin, err := url.Parse(server.URL)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ws := &WebSocketConnection{}
	require.NoError(t, ws.Dial(ctx, u, origin))
	defer func() { _ = ws.Close() }()
	var data []byte
	require.ErrorIs(t, ws.Receive(&data), websocket.ErrFrameTooLarge)
	require.Empty(t, data)
}

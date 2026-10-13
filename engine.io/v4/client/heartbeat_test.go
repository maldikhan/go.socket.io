package engineio_v4_client

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	wsTransport "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket"
	"github.com/maldikhan/go.socket.io/utils"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"
)

func TestSilentWebSocketTriggersPingTimeout(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		if err := websocket.Message.Send(ws, `0{"sid":"silent","pingInterval":25,"pingTimeout":25}`); err != nil {
			return
		}
		var packet string
		_ = websocket.Message.Receive(ws, &packet)
	}))
	defer server.Close()
	transport, err := wsTransport.NewTransport(wsTransport.WithLogger(&utils.DefaultLogger{Level: utils.NONE}))
	require.NoError(t, err)
	failed := make(chan struct{}, 1)
	client, err := NewClient(
		WithRawURL(server.URL), WithTransport(transport), WithSupportedTransports([]Transport{transport}),
		WithLogger(&utils.DefaultLogger{Level: utils.NONE}), WithReconnectAttempts(0),
		WithOnReconnectFailed(func() { failed <- struct{}{} }),
	)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))
	defer func() { _ = client.Close() }()
	select {
	case <-failed:
		client.transportMu.RLock()
		cause := client.heartbeatErr
		client.transportMu.RUnlock()
		require.ErrorIs(t, cause, ErrPingTimeout)
	case <-ctx.Done():
		t.Fatal("silent WebSocket stayed connected past its heartbeat deadline")
	}
}

func TestHandshakeRejectsInvalidHeartbeatValues(t *testing.T) {
	for _, values := range [][2]int{{-1, 1}, {1, -1}, {math.MaxInt, 1}, {1, math.MaxInt}} {
		client, _, _, _ := newReconnectClient(t)
		data, err := json.Marshal(map[string]interface{}{
			"sid": "bad", "pingInterval": values[0], "pingTimeout": values[1],
		})
		require.NoError(t, err)
		require.NotPanics(t, func() { require.Error(t, client.handleHandshake(data)) })
	}
}

func TestPingRefreshesHeartbeatDeadline(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	client.heartbeatReset = make(chan struct{}, 1)
	client.heartbeatTimeout = time.Second
	client.refreshHeartbeat()
	firstDeadline := client.heartbeatDeadline
	client.refreshHeartbeat()
	require.True(t, client.heartbeatDeadline.After(firstDeadline))
	require.Len(t, client.heartbeatReset, 1)
}

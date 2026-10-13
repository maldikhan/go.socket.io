//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	engineio "github.com/maldikhan/go.socket.io/engine.io/v4/client"
	ws "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket"
	socketio "github.com/maldikhan/go.socket.io/socket.io/v5/client"
	ws_coder "github.com/maldikhan/go.socket.io/websocket/coder"
)

func newCoderBinaryClient(t *testing.T, compression websocket.CompressionMode) *socketio.Client {
	t.Helper()
	backend, err := ws_coder.New(ws_coder.WithCompression(compression))
	if err != nil {
		t.Fatal(err)
	}
	transport, err := ws.NewTransport(ws.WithWebSocket(backend))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := engineio.NewClient(
		engineio.WithRawURL(strings.TrimRight(serverURL(), "/")+"/socket.io/"),
		engineio.WithTransport(transport),
		engineio.WithSupportedTransports([]engineio.Transport{transport}),
		engineio.WithConnectTimeout(5*time.Second),
		engineio.WithReconnectWait(100*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	client, err := socketio.NewClient(socketio.WithEngineIOClient(engine))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestE2E_CoderBinaryAndReconnect(t *testing.T) {
	for _, mode := range []struct {
		name        string
		compression websocket.CompressionMode
	}{
		{"uncompressed", websocket.CompressionDisabled},
		{"compressed", websocket.CompressionContextTakeover},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Run("binary event and ack", func(t *testing.T) { runBinaryScenario(t, newCoderBinaryClient(t, mode.compression)) })
			t.Run("multiple and nested attachments", func(t *testing.T) { runRichBinaryScenario(t, newCoderBinaryClient(t, mode.compression)) })
			t.Run("reconnect", func(t *testing.T) { runReconnectScenario(t, newCoderBinaryClient(t, mode.compression)) })
		})
	}
}

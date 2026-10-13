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

// Exercise the optional backend against Socket.IO itself, including a timed
// engine.io handshake, namespace connection, pushed events and acknowledgments.
func TestE2E_CoderWebsocket(t *testing.T) {
	for _, compression := range []websocket.CompressionMode{
		websocket.CompressionDisabled, websocket.CompressionContextTakeover,
	} {
		name := "uncompressed"
		if compression != websocket.CompressionDisabled {
			name = "compression enabled"
		}
		t.Run(name, func(t *testing.T) {
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
			)
			if err != nil {
				t.Fatal(err)
			}
			client, err := socketio.NewClient(socketio.WithEngineIOClient(engine))
			if err != nil {
				t.Fatal(err)
			}
			runScenario(t, client)
		})
	}
}

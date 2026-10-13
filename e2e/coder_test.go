//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	engineio "github.com/maldikhan/go.socket.io/engine.io/v4/client"
	ws "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket"
	socketio "github.com/maldikhan/go.socket.io/socket.io/v5/client"
	"github.com/maldikhan/go.socket.io/socket.io/v5/client/emit"
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

// A dial context in coder/websocket normally expires after the handshake, while
// this adapter also owns a session context. Verify that stopping the connect
// timeout leaves that context live and the established Socket.IO session usable.
func TestE2E_CoderConnectTimeoutSessionSurvives(t *testing.T) {
	const connectTimeout = 500 * time.Millisecond
	backend, err := ws_coder.New()
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
		engineio.WithConnectTimeout(connectTimeout),
	)
	if err != nil {
		t.Fatal(err)
	}
	client, err := socketio.NewClient(socketio.WithEngineIOClient(engine))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connected := make(chan struct{}, 1)
	client.On("connect", func() { connected <- struct{}{} })
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("namespace did not connect")
	}
	timer := time.NewTimer(2 * connectTimeout)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal("session context unexpectedly expired")
	}
	ack := make(chan string, 1)
	if err := client.Emit("echo", "after connect timeout", emit.WithAck(func(s string) { ack <- s })); err != nil {
		t.Fatalf("emit after connect timeout: %v", err)
	}
	select {
	case message := <-ack:
		if message != "after connect timeout" {
			t.Fatalf("unexpected acknowledgment: %q", message)
		}
	case <-ctx.Done():
		t.Fatal("established session did not survive the connect timeout")
	}
}

func TestE2E_CoderReconnectWithConnectTimeout(t *testing.T) {
	backend, err := ws_coder.New()
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
		engineio.WithConnectTimeout(500*time.Millisecond),
		engineio.WithReconnectWait(10*time.Millisecond),
		engineio.WithReconnectAttempts(3),
	)
	if err != nil {
		t.Fatal(err)
	}
	client, err := socketio.NewClient(socketio.WithEngineIOClient(engine))
	if err != nil {
		t.Fatal(err)
	}
	runReconnectScenario(t, client)
}

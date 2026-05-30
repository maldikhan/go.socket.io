//go:build e2e

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	engineio "github.com/maldikhan/go.socket.io/engine.io/v4/client"
	polling "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/polling"
	ws "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket"
	socketio "github.com/maldikhan/go.socket.io/socket.io/v5/client"
	"github.com/maldikhan/go.socket.io/socket.io/v5/client/emit"
)

// serverURL returns the address of the Socket.IO server under test (a single
// server that advertises a websocket upgrade, so every transport mode is
// exercised against the same realistic endpoint).
func serverURL() string {
	if u := os.Getenv("E2E_SERVER_URL"); u != "" {
		return u
	}
	return "http://localhost:3000"
}

// newClient builds a socket.io client constrained to the given transport mode,
// all against the same upgrade-advertising server:
//   - "default":   polling with automatic upgrade to websocket
//   - "polling":   HTTP long-polling only (no upgrade performed, even though the
//     server offers one)
//   - "websocket": websocket only (connects directly, no polling phase)
func newClient(t *testing.T, mode string) *socketio.Client {
	t.Helper()
	url := serverURL()

	if mode == "default" {
		client, err := socketio.NewClient(socketio.WithRawURL(url))
		if err != nil {
			t.Fatalf("new default client: %v", err)
		}
		return client
	}

	var primary engineio.Transport
	switch mode {
	case "polling":
		pt, err := polling.NewTransport()
		if err != nil {
			t.Fatalf("new polling transport: %v", err)
		}
		primary = pt
	case "websocket":
		wt, err := ws.NewTransport()
		if err != nil {
			t.Fatalf("new websocket transport: %v", err)
		}
		primary = wt
	default:
		t.Fatalf("unknown transport mode %q", mode)
	}

	// The engine.io client (unlike the socket.io client) does not inject the
	// default "/socket.io/" path, so add it explicitly when building a client
	// directly from a transport.
	engineURL := strings.TrimRight(url, "/") + "/socket.io/"
	engine, err := engineio.NewClient(
		engineio.WithRawURL(engineURL),
		engineio.WithSupportedTransports([]engineio.Transport{primary}),
		engineio.WithTransport(primary),
	)
	if err != nil {
		t.Fatalf("new engine client (%s): %v", mode, err)
	}

	client, err := socketio.NewClient(socketio.WithEngineIOClient(engine))
	if err != nil {
		t.Fatalf("new socket.io client (%s): %v", mode, err)
	}
	return client
}

// runScenario exercises the core protocol surface: connect, a server-pushed
// event, an acknowledgement round-trip, and a clean disconnect.
func runScenario(t *testing.T, client *socketio.Client) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	welcome := make(chan string, 1)
	client.On("welcome", func(msg string) {
		select {
		case welcome <- msg:
		default:
		}
	})

	// Detect connection via the "connect" event (zero-arg handler, matching the
	// documented usage) registered before Connect so it is never missed.
	connected := make(chan struct{}, 1)
	client.On("connect", func() {
		select {
		case connected <- struct{}{}:
		default:
		}
	})

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("timed out waiting for connect event")
	}

	// Server-pushed event: emit "hello", expect a "welcome" back.
	if err := client.Emit("hello", "world"); err != nil {
		t.Fatalf("emit hello: %v", err)
	}
	select {
	case msg := <-welcome:
		if msg != "hello world" {
			t.Fatalf("welcome payload = %q, want %q", msg, "hello world")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for welcome event")
	}

	// Acknowledgement round-trip: emit "echo" with an ack callback.
	ack := make(chan string, 1)
	if err := client.Emit("echo", "ping", emit.WithAck(func(s string) {
		select {
		case ack <- s:
		default:
		}
	})); err != nil {
		t.Fatalf("emit echo: %v", err)
	}
	select {
	case s := <-ack:
		if s != "ping" {
			t.Fatalf("ack payload = %q, want %q", s, "ping")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for ack")
	}
}

func TestE2E_DefaultTransport(t *testing.T) {
	runScenario(t, newClient(t, "default"))
}

func TestE2E_PollingOnly(t *testing.T) {
	runScenario(t, newClient(t, "polling"))
}

func TestE2E_WebsocketOnly(t *testing.T) {
	runScenario(t, newClient(t, "websocket"))
}

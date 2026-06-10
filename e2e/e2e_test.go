//go:build e2e

package e2e

import (
	"context"
	"errors"
	"net"
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
//
// Extra socket.io options (e.g. a non-default namespace) may be supplied.
func newClient(t *testing.T, mode string, opts ...socketio.ClientOption) *socketio.Client {
	t.Helper()
	url := serverURL()

	if mode == "default" {
		client, err := socketio.NewClient(append([]socketio.ClientOption{socketio.WithRawURL(url)}, opts...)...)
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

	client, err := socketio.NewClient(append([]socketio.ClientOption{socketio.WithEngineIOClient(engine)}, opts...)...)
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

// TestE2E_Namespace runs the same scenario on a non-default namespace ("/admin")
// to cover the namespace connect/emit/ack path end-to-end.
func TestE2E_Namespace(t *testing.T) {
	runScenario(t, newClient(t, "default", socketio.WithDefaultNamespace("/admin")))
}

// TestE2E_ConnectTimeout_CompletesAgainstRealServer verifies that a client
// configured with WithConnectTimeout connects to the real Socket.IO server
// well within the bound: the timeout must only bound the connection phase
// (dial, handshake, upgrade) and not break a healthy connect or the session
// that follows it.
func TestE2E_ConnectTimeout_CompletesAgainstRealServer(t *testing.T) {
	client, err := socketio.NewClient(
		socketio.WithRawURL(serverURL()),
		socketio.WithConnectTimeout(10*time.Second),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	runScenario(t, client)
}

// TestE2E_ConnectTimeout_UnresponsiveServer verifies the failure semantics of
// WithConnectTimeout against a real TCP black hole: a listener that accepts
// connections but never answers the HTTP handshake. Connect() must return
// context.DeadlineExceeded promptly (bounded by the configured timeout, not by
// the session context), proving the timeout actually bounds the connection
// phase.
func TestE2E_ConnectTimeout_UnresponsiveServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	// Accept and hold connections without ever responding, like a hung server.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()

	client, err := socketio.NewClient(
		socketio.WithRawURL("http://"+ln.Addr().String()),
		socketio.WithConnectTimeout(2*time.Second),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	// The session context is much larger than the connect timeout: the error
	// must come from the timeout, not from this context.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	err = client.Connect(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("connect to unresponsive server: err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("connect took %s, want it bounded by the 2s connect timeout", elapsed)
	}
}

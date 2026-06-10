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

// runBinaryScenario exercises the Socket.IO v5 binary attachment paths against
// the real server: a binary ack round-trip ("binEcho") and a server-pushed
// binary event ("binWelcome"), asserting the []byte survives both directions.
func runBinaryScenario(t *testing.T, client *socketio.Client) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	binWelcome := make(chan []byte, 1)
	client.On("binWelcome", func(buf []byte) {
		select {
		case binWelcome <- buf:
		default:
		}
	})

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

	// Binary ack round-trip: send a Buffer, expect the same bytes back.
	payload := []byte{0x00, 0x01, 0x02, 0xFF, 0x7F, 0x80}
	ack := make(chan []byte, 1)
	if err := client.Emit("binEcho", payload, emit.WithAck(func(buf []byte) {
		select {
		case ack <- buf:
		default:
		}
	})); err != nil {
		t.Fatalf("emit binEcho: %v", err)
	}
	select {
	case got := <-ack:
		if string(got) != string(payload) {
			t.Fatalf("binEcho ack = %v, want %v", got, payload)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for binEcho ack")
	}

	// Server-pushed binary event: emit "binPush", expect a "binWelcome" Buffer.
	if err := client.Emit("binPush", "world"); err != nil {
		t.Fatalf("emit binPush: %v", err)
	}
	select {
	case got := <-binWelcome:
		if string(got) != "world" {
			t.Fatalf("binWelcome payload = %q, want %q", got, "world")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for binWelcome event")
	}
}

func TestE2E_BinaryDefaultTransport(t *testing.T) {
	runBinaryScenario(t, newClient(t, "default"))
}

func TestE2E_BinaryPollingOnly(t *testing.T) {
	runBinaryScenario(t, newClient(t, "polling"))
}

func TestE2E_BinaryWebsocketOnly(t *testing.T) {
	runBinaryScenario(t, newClient(t, "websocket"))
}

// TestE2E_Namespace runs the same scenario on a non-default namespace ("/admin")
// to cover the namespace connect/emit/ack path end-to-end.
func TestE2E_Namespace(t *testing.T) {
	runScenario(t, newClient(t, "default", socketio.WithDefaultNamespace("/admin")))
}

// runRichBinaryScenario exercises the harder corners of the binary protocol
// against the real server: multiple attachments in one packet (both
// directions) and a nested object mixing a Buffer, a null and a string —
// asserting placeholder substitution at nested positions and that nil byte
// slices stay JSON null rather than becoming empty attachments.
func runRichBinaryScenario(t *testing.T, client *socketio.Client) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

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

	// Two attachments in one packet, echoed back as two attachments.
	first := []byte{0x01, 0x02, 0x03}
	second := []byte{0xFF, 0x00, 0x7F, 0x80}
	multiAck := make(chan [2][]byte, 1)
	if err := client.Emit("binMulti", first, second, emit.WithAck(func(a, b []byte) {
		select {
		case multiAck <- [2][]byte{a, b}:
		default:
		}
	})); err != nil {
		t.Fatalf("emit binMulti: %v", err)
	}
	select {
	case got := <-multiAck:
		if string(got[0]) != string(first) || string(got[1]) != string(second) {
			t.Fatalf("binMulti ack = %v/%v, want %v/%v", got[0], got[1], first, second)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for binMulti ack")
	}

	// A nested object: Buffer at a key, an explicit null (sent as a nil byte
	// slice, which must serialize as JSON null) and a plain string.
	type nested struct {
		File []byte  `json:"file"`
		Note *string `json:"note"`
		Name string  `json:"name"`
	}
	payload := map[string]interface{}{
		"file": []byte("attachment-bytes"),
		"note": []byte(nil), // nil []byte must arrive as null, not an empty Buffer
		"name": "report.bin",
	}
	nestedAck := make(chan nested, 1)
	if err := client.Emit("binNested", payload, emit.WithAck(func(obj nested) {
		select {
		case nestedAck <- obj:
		default:
		}
	})); err != nil {
		t.Fatalf("emit binNested: %v", err)
	}
	select {
	case got := <-nestedAck:
		if string(got.File) != "attachment-bytes" {
			t.Fatalf("binNested file = %q, want %q", got.File, "attachment-bytes")
		}
		if got.Note != nil {
			t.Fatalf("binNested note = %q, want JSON null (nil)", *got.Note)
		}
		if got.Name != "report.bin" {
			t.Fatalf("binNested name = %q, want %q", got.Name, "report.bin")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for binNested ack")
	}
}

func TestE2E_BinaryRich_DefaultTransport(t *testing.T) {
	runRichBinaryScenario(t, newClient(t, "default"))
}

func TestE2E_BinaryRich_PollingOnly(t *testing.T) {
	runRichBinaryScenario(t, newClient(t, "polling"))
}

func TestE2E_BinaryRich_WebsocketOnly(t *testing.T) {
	runRichBinaryScenario(t, newClient(t, "websocket"))
}

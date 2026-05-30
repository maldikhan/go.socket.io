package engineio_v4_client

import (
	"context"
	"net/url"
	"runtime"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	mocks "github.com/maldikhan/go.socket.io/engine.io/v4/client/mocks"
	"github.com/stretchr/testify/assert"
)

// TestReconnectRestoresInitialTransportAndClearsSid is the Bug 1 regression
// test. After a polling->websocket upgrade c.transport is the websocket
// transport and c.sid is the live session id. A reconnect must start a FRESH
// Engine.IO session: it must dial on the INITIAL (polling) transport with an
// EMPTY sid so the server performs a brand-new handshake rather than rejecting
// a resume of a dead session.
func TestReconnectRestoresInitialTransportAndClearsSid(t *testing.T) {
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

	initial := mocks.NewMockTransport(ctrl)  // polling transport (real handshake)
	upgraded := mocks.NewMockTransport(ctrl) // websocket transport (no-op handshake)

	client := &Client{
		url:               &url.URL{Scheme: "http", Host: "localhost:8080"},
		transport:         upgraded, // post-upgrade state
		initialTransport:  initial,  // captured on the first connect
		sid:               "dead-session-id",
		parser:            mocks.NewMockParser(ctrl),
		log:               logger,
		pingInterval:      time.NewTicker(time.Second),
		reconnect:         true,
		reconnectAttempts: 3,
		reconnectWait:     time.Millisecond,
		supportedTransports: map[engineio_v4.EngineIOTransport]Transport{
			engineio_v4.TransportPolling: initial,
		},
		transportClosed: make(chan error, 1),
	}
	client.ctx = context.Background()

	// The reconnect attempt MUST run on the initial transport with an empty sid.
	var ranSid string
	initial.EXPECT().
		Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *url.URL, sid string, _ chan<- []byte, _ chan<- error) error {
			ranSid = sid
			return nil
		})
	initial.EXPECT().RequestHandshake().Return(nil)

	err := client.startConnection(client.ctx)
	assert.NoError(t, err)

	// The upgraded (websocket) transport must NOT have been dialed.
	assert.Same(t, initial, client.transport, "reconnect must restore the initial transport")
	assert.Equal(t, "", ranSid, "reconnect must dial with an empty sid for a fresh session")
	assert.Equal(t, "", client.sid, "stale sid must be cleared before reconnecting")

	// Clean up the message loop the successful cycle started.
	client.transportClosed <- nil
	initial.EXPECT().Stop().Return(nil)
	_ = client.Close()
}

// TestConnectCapturesInitialTransport verifies Connect records the pre-upgrade
// transport on the first connect (covering the initial-transport capture branch).
func TestConnectCapturesInitialTransport(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)
	client.initialTransport = nil

	transport.EXPECT().
		Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).
		AnyTimes()
	transport.EXPECT().RequestHandshake().Return(nil).AnyTimes()

	err := client.Connect(context.Background())
	assert.NoError(t, err)
	assert.Same(t, transport, client.initialTransport, "Connect must capture the initial transport")

	// Clean up the cycle's message loop.
	client.transportClosed <- nil
	transport.EXPECT().Stop().Return(nil)
	_ = client.Close()
}

// TestNoMessageLoopLeakAcrossReconnect is the Bug 2 regression test. On a
// reconnect, startConnection must tear down the previous cycle's messageLoop
// (closing its messagesDone) before installing fresh channels, so the old
// goroutine never leaks. We assert both that the first cycle's messagesDone is
// closed after the reconnect and that the live goroutine count returns to its
// pre-reconnect baseline.
func TestNoMessageLoopLeakAcrossReconnect(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)

	transport.EXPECT().
		Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).
		AnyTimes()
	transport.EXPECT().RequestHandshake().Return(nil).AnyTimes()

	// First cycle: starts a messageLoop reading the first messages channel.
	err := client.startConnection(context.Background())
	assert.NoError(t, err)
	firstDone := client.messagesDone
	assert.NotNil(t, firstDone)

	// Let the first loop reach its select, then snapshot the goroutine count.
	waitGoroutines := func() int {
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
		return runtime.NumGoroutine()
	}
	baseline := waitGoroutines()

	// Second cycle (reconnect): must stop the first loop before swapping channels.
	err = client.startConnection(context.Background())
	assert.NoError(t, err)

	// The first cycle's loop must have exited (its done channel is closed).
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("previous messageLoop did not exit on reconnect (goroutine leak)")
	}

	// The goroutine count must not have grown by an extra leaked loop.
	after := waitGoroutines()
	assert.LessOrEqual(t, after, baseline, "reconnect must not leak a messageLoop goroutine")

	// Clean up the second cycle's loop.
	client.transportClosed <- nil
	transport.EXPECT().Stop().Return(nil)
	_ = client.Close()
}

// TestStopMessageLoopNoop verifies stopMessageLoop is safe when no loop is
// running (nil stop channel) and idempotent when called twice.
func TestStopMessageLoopNoop(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	client.messagesStop = nil
	client.messagesDone = nil
	client.stopMessageLoop() // nil stop: must return without blocking
	client.stopMessageLoop() // still nil: idempotent
}

// TestMessageLoopStopSignal verifies messageLoop exits when its dedicated stop
// channel is closed even though the messages channel is never closed and the
// context is never cancelled.
func TestMessageLoopStopSignal(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	messages := make(chan []byte) // never written/closed
	done := make(chan struct{})
	stop := make(chan struct{})

	go client.messageLoop(context.Background(), messages, done, stop)
	close(stop)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("messageLoop did not exit on stop signal")
	}
}

// TestReconnectLoopNilContext verifies reconnectLoop does not panic when no run
// context was assigned: the wait select must fall back to the backoff timer.
func TestReconnectLoopNilContext(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)
	client.ctx = nil
	client.reconnectAttempts = 1
	client.reconnectWait = time.Millisecond

	failedCh := make(chan struct{})
	client.reconnectFailedHand = func() { close(failedCh) }

	// The single attempt fails, exhausting the loop and closing the client.
	transport.EXPECT().
		Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(assertErr("dial failed"))
	transport.EXPECT().Stop().Return(nil)

	client.reconnectLoop(assertErr("connection dropped"))

	select {
	case <-failedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnectLoop did not finish with a nil context")
	}
}

// assertErr is a tiny error helper so this file does not need the errors import
// solely for test fixtures.
type assertErr string

func (e assertErr) Error() string { return string(e) }

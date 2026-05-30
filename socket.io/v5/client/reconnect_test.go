package socketio_v5_client

import (
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	mocks "github.com/maldikhan/go.socket.io/socket.io/v5/client/mocks"
)

// passthroughWrap returns a WrapCallback stub that adapts a func(...interface{})
// user handler into the func([]interface{}) form the namespace stores, matching
// what the default parser does at runtime.
func passthroughWrap(cb interface{}) func([]interface{}) {
	return func(args []interface{}) {
		if f, ok := cb.(func(...interface{})); ok {
			f(args...)
		}
	}
}

// captureEngineHandlers builds a socket.io client backed by a mock engine.io
// client and records every handler the constructor registers via engineio.On,
// keyed by event name. This lets the tests invoke the engine-level callbacks
// (connect, message, reconnect, reconnecting, reconnect_failed) directly and
// assert the socket.io-level reaction.
func captureEngineHandlers(t *testing.T) (*Client, map[string]func([]byte), *mocks.MockEngineIOClient, *mocks.MockParser) {
	t.Helper()
	ctrl := gomock.NewController(t)

	mockEngine := mocks.NewMockEngineIOClient(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

	handlers := make(map[string]func([]byte))
	mockEngine.EXPECT().On(gomock.Any(), gomock.Any()).
		DoAndReturn(func(event string, handler func([]byte)) {
			handlers[event] = handler
		}).AnyTimes()

	client, err := NewClient(
		WithEngineIOClient(mockEngine),
		WithLogger(mockLogger),
		WithParser(mockParser),
	)
	require.NoError(t, err)
	return client, handlers, mockEngine, mockParser
}

// TestConstructorRegistersReconnectHandlers verifies the constructor wires all
// engine.io lifecycle events the reconnect feature relies on.
func TestConstructorRegistersReconnectHandlers(t *testing.T) {
	_, handlers, _, _ := captureEngineHandlers(t)
	for _, event := range []string{"connect", "message", "reconnect", "reconnecting", "reconnect_failed"} {
		assert.NotNil(t, handlers[event], "constructor must register engine handler for %q", event)
	}
}

// TestReconnectResendsConnectAndEmits verifies that the engine "reconnect" event
// (1) re-sends the socket.io CONNECT packet so the namespace re-joins, and
// (2) emits the socket.io-level "reconnect" event to user handlers.
func TestReconnectResendsConnectAndEmits(t *testing.T) {
	client, handlers, mockEngine, mockParser := captureEngineHandlers(t)

	// connectSocketIO serializes a CONNECT packet and sends it over the engine.
	var mu sync.Mutex
	var sentConnect bool
	mockParser.EXPECT().Serialize(gomock.Any()).DoAndReturn(func(msg *socketio_v5.Message) ([]byte, error) {
		mu.Lock()
		if msg.Type == socketio_v5.PacketConnect {
			sentConnect = true
		}
		mu.Unlock()
		return []byte("0"), nil
	}).AnyTimes()
	mockEngine.EXPECT().Send(gomock.Any()).Return(nil).AnyTimes()
	mockParser.EXPECT().WrapCallback(gomock.Any()).DoAndReturn(passthroughWrap).AnyTimes()

	reconnected := make(chan struct{})
	client.On("reconnect", func(args ...interface{}) { close(reconnected) })

	// Fire the engine-level reconnect event.
	handlers["reconnect"](nil)

	select {
	case <-reconnected:
	case <-time.After(time.Second):
		t.Fatal("socket.io reconnect handler not invoked")
	}

	mu.Lock()
	assert.True(t, sentConnect, "reconnect must re-send the CONNECT packet")
	mu.Unlock()
}

// TestReconnectingAndFailedEmit verifies that the "reconnecting" and
// "reconnect_failed" engine events reach socket.io-level handlers.
func TestReconnectingAndFailedEmit(t *testing.T) {
	client, handlers, _, mockParser := captureEngineHandlers(t)
	mockParser.EXPECT().WrapCallback(gomock.Any()).DoAndReturn(passthroughWrap).AnyTimes()

	reconnecting := make(chan struct{})
	failed := make(chan struct{})
	client.On("reconnecting", func(args ...interface{}) { close(reconnecting) })
	client.On("reconnect_failed", func(args ...interface{}) { close(failed) })

	handlers["reconnecting"](nil)
	handlers["reconnect_failed"](nil)

	select {
	case <-reconnecting:
	case <-time.After(time.Second):
		t.Fatal("reconnecting handler not invoked")
	}
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("reconnect_failed handler not invoked")
	}
}

// TestEmitReservedNoHandlers exercises emitReserved when there are no handlers
// registered for the event and when the default namespace is nil (both no-ops).
func TestEmitReservedNoHandlers(t *testing.T) {
	client, _, _, _ := captureEngineHandlers(t)
	// No handlers registered for "reconnect" yet: must be a no-op, not a panic.
	client.emitReserved("reconnect")

	// Nil default namespace: must short-circuit safely.
	client.defaultNs = nil
	client.emitReserved("reconnect")
}

// TestEmitReservedAnyHandler verifies emitReserved also dispatches to OnAny
// handlers with a nil payload.
func TestEmitReservedAnyHandler(t *testing.T) {
	client, _, _, _ := captureEngineHandlers(t)

	got := make(chan string, 1)
	client.OnAny(func(event string, args []interface{}) {
		got <- event
	})

	client.emitReserved("reconnecting")

	select {
	case ev := <-got:
		assert.Equal(t, "reconnecting", ev)
	case <-time.After(time.Second):
		t.Fatal("OnAny handler not invoked by emitReserved")
	}
}

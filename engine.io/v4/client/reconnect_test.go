package engineio_v4_client

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	mocks "github.com/maldikhan/go.socket.io/engine.io/v4/client/mocks"
	"github.com/stretchr/testify/assert"
)

// newReconnectClient builds a client wired for reconnect tests: short waits,
// a single supported transport, and a logger that swallows all levels.
func newReconnectClient(t *testing.T) (*Client, *mocks.MockTransport, *mocks.MockParser, *mocks.MockLogger) {
	ctrl := gomock.NewController(t)
	transport := mocks.NewMockTransport(ctrl)
	parser := mocks.NewMockParser(ctrl)
	logger := mocks.NewMockLogger(ctrl)
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

	client := &Client{
		url:               &url.URL{Scheme: "http", Host: "localhost:8080"},
		transport:         transport,
		parser:            parser,
		log:               logger,
		pingInterval:      time.NewTicker(time.Second),
		reconnect:         true,
		reconnectAttempts: 3,
		reconnectWait:     time.Millisecond,
		supportedTransports: map[engineio_v4.EngineIOTransport]Transport{
			engineio_v4.TransportPolling: transport,
		},
		stopPooling:     make(chan struct{}, 1),
		transportClosed: make(chan error, 1),
	}
	client.ctx = context.Background()
	return client, transport, parser, logger
}

// TestSuperviseGracefulNilError verifies that a nil close value (graceful stop)
// makes the supervisor exit without firing any reconnect logic.
func TestSuperviseGracefulNilError(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	closed := make(chan error, 1)
	done := make(chan struct{})
	closed <- nil

	var reconnecting int32
	client.reconnectingHandler = func() { atomic.AddInt32(&reconnecting, 1) }

	client.supervise(closed, done)
	<-done
	assert.Equal(t, int32(0), atomic.LoadInt32(&reconnecting))
}

// TestSuperviseContextDone verifies that cancelling the parent context unblocks
// the supervisor even when the transport never reports a close.
func TestSuperviseContextDone(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel before supervising so the select deterministically takes the
	// context-done arm (closed is never written).
	cancel()
	client.ctx = ctx
	closed := make(chan error) // never written
	done := make(chan struct{})

	client.supervise(closed, done)
	<-done
}

// TestSuperviseIntentionalClose verifies that a drop observed while closing==1
// is treated as a graceful shutdown (no reconnect).
func TestSuperviseIntentionalClose(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	atomic.StoreUint32(&client.closing, 1)

	var reconnecting int32
	client.reconnectingHandler = func() { atomic.AddInt32(&reconnecting, 1) }

	closed := make(chan error, 1)
	done := make(chan struct{})
	closed <- errors.New("drop")

	client.supervise(closed, done)
	<-done
	assert.Equal(t, int32(0), atomic.LoadInt32(&reconnecting))
}

// TestSuperviseReconnectDisabled verifies that with reconnect disabled the close
// handler is invoked instead of starting the reconnect loop.
func TestSuperviseReconnectDisabled(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	client.reconnect = false

	var closed32 int32
	// Register via On so the "close" handler closure is exercised too.
	client.On("close", func([]byte) { atomic.AddInt32(&closed32, 1) })
	var reconnecting int32
	client.reconnectingHandler = func() { atomic.AddInt32(&reconnecting, 1) }

	closed := make(chan error, 1)
	done := make(chan struct{})
	closed <- errors.New("drop")

	client.supervise(closed, done)
	<-done
	assert.Equal(t, int32(1), atomic.LoadInt32(&closed32))
	assert.Equal(t, int32(0), atomic.LoadInt32(&reconnecting))
}

// TestSuperviseReconnectDisabledNilHandler exercises the nil close-handler
// branch of the reconnect-disabled path.
func TestSuperviseReconnectDisabledNilHandler(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	client.reconnect = false

	closed := make(chan error, 1)
	done := make(chan struct{})
	closed <- errors.New("drop")

	client.supervise(closed, done) // must not panic with nil closeHandler
	<-done
}

// TestSuperviseNilDone confirms the supervisor tolerates a nil done channel
// (the direct-handleHandshake unit-test path).
func TestSuperviseNilDone(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	closed := make(chan error, 1)
	closed <- nil
	client.supervise(closed, nil) // must not panic
}

// TestSuperviseTriggersReconnect drives the full supervisor path: a non-nil drop
// with reconnect enabled must reach reconnectLoop and re-establish the
// connection (covering the supervise -> reconnectLoop edge).
func TestSuperviseTriggersReconnect(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)

	reconnected := make(chan struct{})
	client.reconnectHandler = func() { close(reconnected) }

	// reconnectLoop's first attempt re-runs the transport and handshakes.
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	transport.EXPECT().RequestHandshake().Return(nil)

	closed := make(chan error, 1)
	done := make(chan struct{})
	closed <- errors.New("connection dropped")

	go client.supervise(closed, done)

	select {
	case <-reconnected:
	case <-time.After(2 * time.Second):
		t.Fatal("supervise did not drive a reconnect")
	}
	<-done

	// Clean up the message loop spun up by the successful reconnect.
	client.transportClosed <- nil
	transport.EXPECT().Stop().Return(nil)
	_ = client.Close()
}

// TestSuperviseNilContext confirms the supervisor doesn't panic when no run
// context was assigned: it must fall back to watching only the close channel.
func TestSuperviseNilContext(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	client.ctx = nil
	closed := make(chan error, 1)
	done := make(chan struct{})
	closed <- nil // graceful stop

	client.supervise(closed, done) // must not panic on nil ctx
	<-done
}

// TestStartSupervisorNilTransportClosed verifies startSupervisor is a no-op when
// there is no transportClosed channel to watch (handshake exercised in isolation).
func TestStartSupervisorNilTransportClosed(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	client.transportClosed = nil
	client.superviseOnce = sync.Once{}

	client.startSupervisor()
	assert.Equal(t, uint32(0), atomic.LoadUint32(&client.supervisorStarted),
		"no supervisor should start without a transportClosed channel")
}

// TestReconnectLoopSuccess drives a full reconnect: a dropped connection, one
// failed attempt, then a successful re-handshake that re-establishes the
// supervisor and fires the "reconnect" hook.
func TestReconnectLoopSuccess(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)

	var reconnecting, reconnected int32
	client.reconnectingHandler = func() { atomic.AddInt32(&reconnecting, 1) }
	reconnectedCh := make(chan struct{})
	client.reconnectHandler = func() {
		atomic.AddInt32(&reconnected, 1)
		close(reconnectedCh)
	}

	// First attempt: Run fails. Second attempt: Run succeeds + handshake.
	gomock.InOrder(
		transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(errors.New("dial failed")),
		transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil),
	)
	transport.EXPECT().RequestHandshake().Return(nil)

	client.reconnectLoop(errors.New("connection dropped"))

	select {
	case <-reconnectedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect hook not fired")
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&reconnecting))
	assert.Equal(t, int32(1), atomic.LoadInt32(&reconnected))

	// Clean up the message loop started by the successful reconnect.
	client.transportClosed <- nil
	transport.EXPECT().Stop().Return(nil)
	_ = client.Close()
}

// TestReconnectLoopExhausted verifies that when every attempt fails the
// "reconnect_failed" hook fires and the client is closed.
func TestReconnectLoopExhausted(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)
	client.reconnectAttempts = 2

	var failed int32
	failedCh := make(chan struct{})
	client.reconnectFailedHand = func() {
		atomic.AddInt32(&failed, 1)
		close(failedCh)
	}

	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("dial failed")).Times(2)
	// reconnectLoop -> Close(): transport is non-nil, Stop() is called.
	transport.EXPECT().Stop().Return(nil)

	client.reconnectLoop(errors.New("connection dropped"))

	select {
	case <-failedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect_failed hook not fired")
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&failed))
}

// TestReconnectLoopStopRequestedBeforeWait verifies the loop aborts immediately
// when the client is already closing.
func TestReconnectLoopStopRequestedBeforeWait(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)
	atomic.StoreUint32(&client.closing, 1)

	var failed int32
	client.reconnectFailedHand = func() { atomic.AddInt32(&failed, 1) }

	// No Run/Stop expected: the loop must bail before the first attempt.
	client.reconnectLoop(errors.New("connection dropped"))

	assert.Equal(t, int32(0), atomic.LoadInt32(&failed))
	_ = transport // unused expectations: none
}

// TestReconnectLoopContextCancelledDuringWait verifies the loop exits when the
// context is cancelled while waiting for the backoff timer.
func TestReconnectLoopContextCancelledDuringWait(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	client.reconnectWait = time.Hour // ensure we are parked in the wait select
	ctx, cancel := context.WithCancel(context.Background())
	client.ctx = ctx

	done := make(chan struct{})
	go func() {
		client.reconnectLoop(errors.New("connection dropped"))
		close(done)
	}()

	// Give the loop a moment to reach the wait, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnectLoop did not exit on context cancel")
	}
}

// TestReconnectLoopStopRequestedAfterWait verifies the post-wait stop check by
// flipping closing to 1 from a concurrent goroutine during a short wait.
func TestReconnectLoopStopRequestedAfterWait(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	client.reconnectWait = 30 * time.Millisecond

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		atomic.StoreUint32(&client.closing, 1)
	}()

	var failed int32
	client.reconnectFailedHand = func() { atomic.AddInt32(&failed, 1) }

	client.reconnectLoop(errors.New("connection dropped"))
	wg.Wait()
	assert.Equal(t, int32(0), atomic.LoadInt32(&failed))
}

// TestReconnectLoopBackoffCap drives several failing attempts with a base wait
// so that the doubling hits the 8x cap branch.
func TestReconnectLoopBackoffCap(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)
	client.reconnectAttempts = 5
	client.reconnectWait = time.Millisecond

	var failed int32
	failedCh := make(chan struct{})
	client.reconnectFailedHand = func() {
		atomic.AddInt32(&failed, 1)
		close(failedCh)
	}

	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("dial failed")).Times(5)
	transport.EXPECT().Stop().Return(nil)

	client.reconnectLoop(errors.New("connection dropped"))

	select {
	case <-failedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect_failed hook not fired")
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&failed))
}

// TestReconnectLoopZeroWaitDefault verifies the default base wait is used when
// reconnectWait is zero (so backoff is well-defined).
func TestReconnectLoopZeroWaitDefault(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)
	client.reconnectAttempts = 1
	client.reconnectWait = 0
	// Cancel quickly so the (default 1s) wait does not slow the test.
	ctx, cancel := context.WithCancel(context.Background())
	client.ctx = ctx

	done := make(chan struct{})
	go func() {
		client.reconnectLoop(errors.New("connection dropped"))
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnectLoop did not exit")
	}
	_ = transport
}

// TestStartConnectionTransportNil verifies a reconnect attempt that races with
// Close (transport set to nil) returns errClientClosed instead of panicking.
func TestStartConnectionTransportNil(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	client.transport = nil
	err := client.startConnection(context.Background())
	assert.ErrorIs(t, err, errClientClosed)
}

// TestStartConnectionRunError covers the transport.Run failure cleanup path:
// transportClosed and supervisorDone are both closed and the error is returned.
func TestStartConnectionRunError(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)
	runErr := errors.New("dial failed")
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(runErr)

	err := client.startConnection(context.Background())
	assert.Equal(t, runErr, err)

	// supervisorDone must have been closed by the failure path.
	select {
	case <-client.supervisorDone:
	default:
		t.Fatal("supervisorDone was not closed on Run failure")
	}
}

// TestStartConnectionHandshakeError covers the RequestHandshake failure cleanup
// path: the transport is stopped, transportClosed/messages are drained and the
// message loop is awaited before the error is returned.
func TestStartConnectionHandshakeError(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)
	hsErr := errors.New("handshake failed")

	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *url.URL, _ string, _ chan<- []byte, onClose chan<- error) error {
			// Simulate the transport reporting it has stopped once Stop() is
			// called: deliver a value so the cleanup's <-transportClosed unblocks.
			go func() { onClose <- nil }()
			return nil
		})
	transport.EXPECT().RequestHandshake().Return(hsErr)
	transport.EXPECT().Stop().Return(nil)

	err := client.startConnection(context.Background())
	assert.Equal(t, hsErr, err)

	select {
	case <-client.supervisorDone:
	default:
		t.Fatal("supervisorDone was not closed on handshake failure")
	}
}

// TestStopRequested covers the three stopRequested outcomes.
func TestStopRequested(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)

	// Neither closing nor cancelled.
	assert.False(t, client.stopRequested())

	// Closing.
	atomic.StoreUint32(&client.closing, 1)
	assert.True(t, client.stopRequested())

	// Cancelled context (closing reset).
	atomic.StoreUint32(&client.closing, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client.ctx = ctx
	assert.True(t, client.stopRequested())
}

// TestFireHooksNil ensures the fire helpers are no-ops when no handler is set.
func TestFireHooksNil(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	client.fireReconnecting()
	client.fireReconnect()
	client.fireReconnectFailed()
}

// TestOnReconnectEvents verifies On registers the reconnect lifecycle handlers
// and that their closures are invoked when the corresponding event fires.
func TestOnReconnectEvents(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)

	var a, b, d int32
	client.On("reconnecting", func([]byte) { atomic.AddInt32(&a, 1) })
	client.On("reconnect", func([]byte) { atomic.AddInt32(&b, 1) })
	client.On("reconnect_failed", func([]byte) { atomic.AddInt32(&d, 1) })

	client.fireReconnecting()
	client.fireReconnect()
	client.fireReconnectFailed()

	assert.Equal(t, int32(1), atomic.LoadInt32(&a))
	assert.Equal(t, int32(1), atomic.LoadInt32(&b))
	assert.Equal(t, int32(1), atomic.LoadInt32(&d))
}

// TestOnConnectMessageHandlers covers the "connect" and "message" branches of On
// and the execution of their wrapping closures.
func TestOnConnectMessageHandlers(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)

	var connected, messaged int32
	client.On("connect", func([]byte) { atomic.AddInt32(&connected, 1) })
	client.On("message", func([]byte) { atomic.AddInt32(&messaged, 1) })

	// Invoke the registered handlers via the stored fields.
	client.handlerMu.RLock()
	afterConnect := client.afterConnect
	messageHandler := client.messageHandler
	client.handlerMu.RUnlock()

	if assert.NotNil(t, afterConnect) {
		afterConnect()
	}
	if assert.NotNil(t, messageHandler) {
		messageHandler(nil)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&connected))
	assert.Equal(t, int32(1), atomic.LoadInt32(&messaged))
}

// TestStartSupervisorOnce verifies startSupervisor starts exactly one goroutine
// per cycle and that the supervisor exits on a graceful (nil) close.
func TestStartSupervisorOnce(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	client.supervisorDone = make(chan struct{})
	client.transportClosed = make(chan error, 1)

	client.startSupervisor()
	client.startSupervisor() // second call is a no-op (superviseOnce)

	assert.Equal(t, uint32(1), atomic.LoadUint32(&client.supervisorStarted))

	// Graceful close: supervisor exits and closes supervisorDone.
	client.transportClosed <- nil
	select {
	case <-client.supervisorDone:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not exit on graceful close")
	}
}

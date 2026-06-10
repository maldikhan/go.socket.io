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
	mocks "github.com/maldikhan/go.socket.io/engine.io/v4/client/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCloseDuringInFlightAttemptHandshake reproduces the review finding: Close()
// racing with a reconnect attempt that is blocked in RequestHandshake. The stale
// supervisorStarted flag from the old supervisor must not make Close() wait on
// the attempt's fresh (unowned) supervisorDone channel forever, and Close() must
// not compete with the attempt's cleanup for the single transportClosed value.
func TestCloseDuringInFlightAttemptHandshake(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)

	// Simulate the reconnect context: the old supervisor goroutine (which would
	// be running reconnectLoop -> startConnection) left its flag set.
	atomic.StoreUint32(&client.supervisorStarted, 1)

	handshakeStarted := make(chan struct{})
	handshakeAbort := make(chan struct{})
	var abortOnce sync.Once

	var onCloseCh atomic.Value // chan<- error
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *url.URL, _ string, _ chan<- []byte, onClose chan<- error) error {
			onCloseCh.Store(onClose)
			return nil
		})
	transport.EXPECT().RequestHandshake().DoAndReturn(func() error {
		close(handshakeStarted)
		<-handshakeAbort
		return errors.New("handshake aborted")
	})
	transport.EXPECT().Stop().DoAndReturn(func() error {
		abortOnce.Do(func() {
			// The transport loop exits and reports once.
			if ch, ok := onCloseCh.Load().(chan<- error); ok {
				ch <- nil
			}
			close(handshakeAbort)
		})
		return nil
	}).AnyTimes()

	attemptDone := make(chan error, 1)
	go func() {
		attemptDone <- client.startConnection(context.Background())
	}()

	<-handshakeStarted

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- client.Close()
	}()

	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Close deadlocked during an in-flight reconnect attempt")
	}

	select {
	case err := <-attemptDone:
		assert.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("startConnection did not finish after Close")
	}
}

// TestStartConnectionAfterClose verifies the entry guard: an attempt that runs
// after Close() must bail out with errClientClosed and must not resurrect the
// transport that Close() already nil'ed.
func TestStartConnectionAfterClose(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)
	client.initialTransport = transport

	atomic.StoreUint32(&client.closing, 1)
	client.transportMu.Lock()
	client.transport = nil
	client.transportMu.Unlock()

	err := client.startConnection(context.Background())
	assert.ErrorIs(t, err, errClientClosed)

	client.transportMu.RLock()
	defer client.transportMu.RUnlock()
	assert.Nil(t, client.transport, "startConnection must not resurrect the transport after Close")
}

// TestStartConnectionCloseRaceAfterRun covers the window between transport.Run
// and the message loop start: a Close() that hit that window stopped a transport
// that was not running yet, so the attempt itself must tear the fresh transport
// down and report errClientClosed.
func TestStartConnectionCloseRaceAfterRun(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)

	var onCloseCh chan<- error
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *url.URL, _ string, _ chan<- []byte, onClose chan<- error) error {
			onCloseCh = onClose
			// Close() lands exactly between Run() and the post-Run check.
			atomic.StoreUint32(&client.closing, 1)
			return nil
		})
	transport.EXPECT().Stop().DoAndReturn(func() error {
		onCloseCh <- nil
		return nil
	})

	err := client.startConnection(context.Background())
	assert.ErrorIs(t, err, errClientClosed)

	// The cycle's channels are fully torn down: supervisorDone closed,
	// transportClosed drained and closed.
	select {
	case <-client.supervisorDone:
	default:
		t.Fatal("supervisorDone was not closed by the teardown")
	}
}

// TestStartConnectionCloseRaceAfterHandshake covers the window between a
// successful RequestHandshake and startConnection's return: the final closing
// check must tear the connection down instead of leaving it running on a closed
// client.
func TestStartConnectionCloseRaceAfterHandshake(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)

	var onCloseCh chan<- error
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *url.URL, _ string, _ chan<- []byte, onClose chan<- error) error {
			onCloseCh = onClose
			return nil
		})
	transport.EXPECT().RequestHandshake().DoAndReturn(func() error {
		// Close() lands while the handshake request is completing.
		atomic.StoreUint32(&client.closing, 1)
		return nil
	})
	transport.EXPECT().Stop().DoAndReturn(func() error {
		onCloseCh <- nil
		return nil
	})

	err := client.startConnection(context.Background())
	assert.ErrorIs(t, err, errClientClosed)

	select {
	case <-client.supervisorDone:
	default:
		t.Fatal("supervisorDone was not closed by the teardown")
	}
	// The message loop was stopped via its stop signal.
	client.transportMu.RLock()
	assert.Nil(t, client.messagesStop)
	client.transportMu.RUnlock()
}

// TestCloseAfterHandshakeErrorAttempt verifies that a Close() issued after a
// failed attempt (e.g. on reconnect exhaustion) does not block draining the
// already-consumed transportClosed channel and does not double-close the
// messages channel that the failed attempt already closed.
func TestCloseAfterHandshakeErrorAttempt(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)
	hsErr := errors.New("handshake failed")

	var onCloseCh chan<- error
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *url.URL, _ string, _ chan<- []byte, onClose chan<- error) error {
			onCloseCh = onClose
			return nil
		})
	transport.EXPECT().RequestHandshake().Return(hsErr)
	var deliverOnce sync.Once
	transport.EXPECT().Stop().DoAndReturn(func() error {
		// The transport reports its loop exit exactly once; a second Stop()
		// (from Close) must not send again — the attempt's cleanup has already
		// drained and closed the channel.
		deliverOnce.Do(func() {
			if onCloseCh != nil {
				onCloseCh <- nil
			}
		})
		return nil
	}).AnyTimes()

	err := client.startConnection(context.Background())
	require.ErrorIs(t, err, hsErr)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- client.Close()
	}()
	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked after a failed connection attempt")
	}
}

// TestStartSupervisorSkippedWhenClosing ensures a handshake that completes
// after Close() does not start a supervisor that would block forever on the
// transportClosed channel Close() already drained.
func TestStartSupervisorSkippedWhenClosing(t *testing.T) {
	t.Parallel()
	client, _, _, _ := newReconnectClient(t)
	client.supervisorDone = make(chan struct{})

	atomic.StoreUint32(&client.closing, 1)
	client.startSupervisor()

	assert.Equal(t, uint32(0), atomic.LoadUint32(&client.supervisorStarted),
		"supervisor must not be started on a closing client")
}

// TestStopMessageLoopAlreadyClosedStop covers the defensive branch where the
// stop channel observed by stopMessageLoop was already closed: the call must
// not double-close it and must still wait for the loop's done channel.
func TestStopMessageLoopAlreadyClosedStop(t *testing.T) {
	t.Parallel()
	client, _, _, _ := newReconnectClient(t)

	stop := make(chan struct{})
	close(stop)
	done := make(chan struct{})
	close(done)
	client.transportMu.Lock()
	client.messagesStop = stop
	client.messagesDone = done
	client.transportMu.Unlock()

	assert.NotPanics(t, func() { client.stopMessageLoop() })
}

// TestAwaitConnectionEstablished covers the failure arms of the
// post-startConnection handshake wait introduced for reconnect attempts.
func TestAwaitConnectionEstablished(t *testing.T) {
	t.Parallel()

	t.Run("transport dies before the handshake", func(t *testing.T) {
		t.Parallel()
		client, transport, _, _ := newReconnectClient(t)
		client.transportMu.Lock()
		client.waitHandshake = make(chan struct{}, 1)
		client.transportClosed = make(chan error, 1)
		// The dead transport's close notification is already queued; the wait
		// must not steal it early (an upgrade drain could own it) — the death
		// is detected via the bound, after which this loop owns the drain.
		client.transportClosed <- errors.New("dropped")
		closed := client.transportClosed
		client.transportMu.Unlock()

		transport.EXPECT().Stop().Return(nil)

		assert.False(t, client.awaitConnectionEstablished(10*time.Millisecond))
		// The drained channel must be closed so a later Close() doesn't block.
		select {
		case _, ok := <-closed:
			assert.False(t, ok, "transportClosed must be closed after the drain")
		default:
			t.Fatal("transportClosed left open")
		}
	})

	t.Run("handshake never completes within the bound", func(t *testing.T) {
		t.Parallel()
		client, transport, _, _ := newReconnectClient(t)
		client.transportMu.Lock()
		client.waitHandshake = make(chan struct{}, 1)
		client.transportClosed = make(chan error, 1)
		client.transportMu.Unlock()

		transport.EXPECT().Stop().DoAndReturn(func() error {
			client.transportClosed <- nil
			return nil
		})

		assert.False(t, client.awaitConnectionEstablished(10*time.Millisecond))
	})

	t.Run("handshake completes exactly at the bound", func(t *testing.T) {
		t.Parallel()
		client, transport, _, _ := newReconnectClient(t)
		client.transportMu.Lock()
		client.waitHandshake = make(chan struct{}, 1)
		client.transportClosed = make(chan error, 1)
		client.transportMu.Unlock()

		// Stop() races a handshake that completes right after the bound: the
		// teardown select must take the waitHandshake arm and not block on the
		// transportClosed channel the new supervisor would own.
		transport.EXPECT().Stop().DoAndReturn(func() error {
			completeHandshake(client)
			return nil
		})

		assert.False(t, client.awaitConnectionEstablished(10*time.Millisecond))
	})

	t.Run("context cancellation", func(t *testing.T) {
		t.Parallel()
		client, _, _, _ := newReconnectClient(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		client.ctx = ctx
		client.transportMu.Lock()
		client.waitHandshake = make(chan struct{}, 1)
		client.transportClosed = make(chan error, 1)
		client.transportMu.Unlock()

		assert.False(t, client.awaitConnectionEstablished(time.Second))
	})

	t.Run("close wakes the wait", func(t *testing.T) {
		t.Parallel()
		client, _, _, _ := newReconnectClient(t)
		client.closeCh = make(chan struct{})
		close(client.closeCh)
		client.transportMu.Lock()
		client.waitHandshake = make(chan struct{}, 1)
		client.transportClosed = make(chan error, 1)
		client.transportMu.Unlock()

		assert.False(t, client.awaitConnectionEstablished(time.Second))
	})

	t.Run("nil transport at timeout", func(t *testing.T) {
		t.Parallel()
		client, _, _, _ := newReconnectClient(t)
		client.transportMu.Lock()
		client.transport = nil
		client.waitHandshake = make(chan struct{}, 1)
		client.transportClosed = make(chan error, 1)
		client.transportMu.Unlock()

		assert.False(t, client.awaitConnectionEstablished(10*time.Millisecond))
	})
}

// TestReconnectLoopHandshakeIncomplete verifies that an attempt whose
// handshake never completes is treated as failed and retried instead of
// firing "reconnect" for a session that never opened.
func TestReconnectLoopHandshakeIncomplete(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)
	client.reconnectAttempts = 2

	var reconnected int32
	client.reconnectHandler = func() { atomic.AddInt32(&reconnected, 1) }
	failedCh := make(chan struct{})
	client.reconnectFailedHand = func() { close(failedCh) }

	// Every attempt starts fine but the OPEN packet never arrives, so the
	// handshake gate stays open and the bound elapses. The Stop mock delivers
	// exactly one close notification per Run (like a real transport): the
	// extra Stop() from the exhaustion Close() must not send again into a
	// channel the await teardown already drained and closed.
	var lifecycleMu sync.Mutex
	pendingRuns := 0
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, *url.URL, string, chan<- []byte, chan<- error) error {
			lifecycleMu.Lock()
			pendingRuns++
			lifecycleMu.Unlock()
			return nil
		}).Times(2)
	transport.EXPECT().RequestHandshake().Return(nil).Times(2)
	transport.EXPECT().Stop().DoAndReturn(func() error {
		lifecycleMu.Lock()
		defer lifecycleMu.Unlock()
		if pendingRuns == 0 {
			return nil
		}
		pendingRuns--
		client.transportMu.RLock()
		closed := client.transportClosed
		client.transportMu.RUnlock()
		closed <- nil
		return nil
	}).AnyTimes()

	client.reconnectLoop(errors.New("connection dropped"))

	select {
	case <-failedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect_failed not fired for incomplete handshakes")
	}
	assert.Equal(t, int32(0), atomic.LoadInt32(&reconnected),
		"reconnect must not fire when the handshake never completed")
}

// TestReconnectLoopStopDuringAwait verifies that a cancellation arriving while
// the loop waits for the handshake makes the loop exit without further retries
// or a reconnect_failed notification.
func TestReconnectLoopStopDuringAwait(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)
	client.reconnectAttempts = 3
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.ctx = ctx

	var failed int32
	client.reconnectFailedHand = func() { atomic.AddInt32(&failed, 1) }

	transport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).Times(1)
	transport.EXPECT().RequestHandshake().DoAndReturn(func() error {
		// The run context is cancelled while the loop is waiting for the
		// handshake to complete.
		cancel()
		return nil
	}).Times(1)

	client.reconnectLoop(errors.New("connection dropped"))

	assert.Equal(t, int32(0), atomic.LoadInt32(&failed),
		"a cancelled session must not be reported as reconnect_failed")
}

// TestTransportUpgradeBailsOnAbandonedCycle reproduces the review finding: a
// reconnect attempt that timed out owns the transportClosed drain, so a
// late-arriving OPEN packet must not let transportUpgrade stop/replace the
// transport or compete for the close notification.
func TestTransportUpgradeBailsOnAbandonedCycle(t *testing.T) {
	t.Parallel()
	client, _, _, _ := newReconnectClient(t)
	atomic.StoreUint32(&client.cycleAbandoned, 1)

	newTransport := mocks.NewMockTransport(gomock.NewController(t))

	err := client.transportUpgrade(newTransport)
	assert.ErrorIs(t, err, errClientClosed)

	// The upgrade gate must be released so Send() callers don't block.
	client.transportMu.RLock()
	wu := client.waitUpgrade
	client.transportMu.RUnlock()
	select {
	case <-wu:
	default:
		t.Fatal("waitUpgrade left open after an abandoned upgrade")
	}
}

// TestStartSupervisorSkippedWhenCycleAbandoned mirrors the closing guard: a
// handshake that completes after the reconnect loop abandoned the cycle must
// not start a supervisor that would compete for transportClosed.
func TestStartSupervisorSkippedWhenCycleAbandoned(t *testing.T) {
	t.Parallel()
	client, _, _, _ := newReconnectClient(t)
	client.supervisorDone = make(chan struct{})
	atomic.StoreUint32(&client.cycleAbandoned, 1)

	client.startSupervisor()
	assert.Equal(t, uint32(0), atomic.LoadUint32(&client.supervisorStarted),
		"supervisor must not start on an abandoned cycle")
}

// TestAwaitConnectionEstablishedPhotoFinish covers the under-lock re-check of
// the handshake gate: the test holds transportMu while the bound elapses, so
// the wait commits to the timeout arm, then closes the gate before releasing
// the lock — the re-check must observe the completed handshake and report
// success instead of tearing the established session down.
func TestAwaitConnectionEstablishedPhotoFinish(t *testing.T) {
	t.Parallel()
	client, _, _, _ := newReconnectClient(t)

	wh := make(chan struct{})
	client.transportMu.Lock()
	client.waitHandshake = wh
	client.transportClosed = make(chan error, 1)
	client.transportMu.Unlock()

	result := make(chan bool, 1)
	go func() {
		result <- client.awaitConnectionEstablished(200 * time.Millisecond)
	}()

	// Let the wait pass its entry snapshot and enter the select, then block its
	// post-timeout lock acquisition while the bound elapses.
	time.Sleep(50 * time.Millisecond)
	client.transportMu.Lock()
	// The bound elapses while we hold the lock, so the wait commits to the
	// timeout arm and parks on transportMu; complete the handshake before
	// releasing the lock so the under-lock re-check observes it.
	time.Sleep(300 * time.Millisecond)
	close(wh)
	client.transportMu.Unlock()

	select {
	case ok := <-result:
		assert.True(t, ok, "a handshake completed before the re-check must be reported as success")
	case <-time.After(2 * time.Second):
		t.Fatal("awaitConnectionEstablished did not return")
	}
}

// TestAbandonedAttemptReleasesHandshakeGate reproduces the review finding: a
// reconnect attempt that is abandoned because its handshake never completed
// must close the cycle's waitHandshake gate. Every cycle arms a FRESH channel,
// so a Send() parked on an abandoned cycle's gate would otherwise block
// forever — even after a later successful reconnect or a final
// "reconnect_failed" — instead of failing fast against the dead transport.
func TestAbandonedAttemptReleasesHandshakeGate(t *testing.T) {
	t.Parallel()
	client, transport, parser, _ := newReconnectClient(t)
	client.transportMu.Lock()
	client.waitHandshake = make(chan struct{}, 1)
	client.transportClosed = make(chan error, 1)
	client.transportMu.Unlock()

	transport.EXPECT().Stop().DoAndReturn(func() error {
		client.transportClosed <- nil
		return nil
	})
	// The woken Send() proceeds to the stopped transport and must surface its
	// error instead of blocking on the gate.
	sendErr := errors.New("transport stopped")
	parser.EXPECT().Serialize(gomock.Any()).Return([]byte("4x"), nil).AnyTimes()
	transport.EXPECT().SendMessage(gomock.Any()).Return(sendErr).AnyTimes()

	// Park a Send() on the handshake gate before the attempt is abandoned.
	sendDone := make(chan error, 1)
	go func() { sendDone <- client.Send([]byte("x")) }()

	assert.False(t, client.awaitConnectionEstablished(10*time.Millisecond))

	select {
	case err := <-sendDone:
		assert.ErrorIs(t, err, sendErr, "the parked Send must fail against the stopped transport")
	case <-time.After(2 * time.Second):
		t.Fatal("Send stayed parked on the abandoned cycle's handshake gate")
	}

	// The gate itself must be observably closed for future Send() snapshots.
	client.transportMu.RLock()
	wh := client.waitHandshake
	client.transportMu.RUnlock()
	select {
	case <-wh:
	default:
		t.Fatal("waitHandshake left open after the attempt was abandoned")
	}
}

// TestCloseReleasesGates verifies that Close() releases both the handshake and
// the upgrade gate, so a Send() parked on either wakes, observes the nil
// transport and fails fast with "client is closed".
func TestCloseReleasesGates(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)
	client.transportMu.Lock()
	client.waitHandshake = make(chan struct{}, 1)
	client.waitUpgrade = make(chan struct{}, 1)
	client.transportClosed = make(chan error, 1)
	client.transportMu.Unlock()

	transport.EXPECT().Stop().DoAndReturn(func() error {
		client.transportClosed <- nil
		return nil
	})

	// Park one Send() before Close(); it waits on both gates.
	sendDone := make(chan error, 1)
	go func() { sendDone <- client.Send([]byte("x")) }()
	// Give the Send a moment to snapshot the gates and park.
	time.Sleep(20 * time.Millisecond)

	require.NoError(t, client.Close())

	select {
	case err := <-sendDone:
		assert.ErrorContains(t, err, "client is closed")
	case <-time.After(2 * time.Second):
		t.Fatal("Send stayed parked on the gates after Close")
	}
}

// TestAwaitConnectionEstablishedUpgradeFailure reproduces the review finding:
// handleHandshake's upgrade-error path closes waitHandshake (to wake parked
// senders) after recording the failure in handshakeErr. A reconnect attempt
// waiting on that gate must treat the closure as a FAILED attempt — tearing
// the half-open cycle down and letting the loop retry — not as a successful
// reconnect on a connection whose transports were stopped mid-upgrade.
func TestAwaitConnectionEstablishedUpgradeFailure(t *testing.T) {
	t.Parallel()

	t.Run("probe failure leaves the new transport running", func(t *testing.T) {
		t.Parallel()
		client, transport, _, _ := newReconnectClient(t)
		client.transportMu.Lock()
		client.waitHandshake = make(chan struct{}, 1)
		client.transportClosed = make(chan error, 1)
		client.handshakeErr = errors.New("probe send failed")
		client.transportMu.Unlock()
		// The gate is released by the upgrade-error path after the error is
		// recorded.
		client.hadHandshake.Do(func() { close(client.waitHandshake) })

		// The teardown must stop the still-running transport of the half-open
		// attempt. Stop may race the photo-finish select arm, so the close
		// notification may or may not be drained here — both are valid.
		transport.EXPECT().Stop().DoAndReturn(func() error {
			select {
			case client.transportClosed <- nil:
			default:
			}
			return nil
		})

		assert.False(t, client.awaitConnectionEstablished(time.Second),
			"a failed upgrade must not be reported as a successful reconnect")
	})

	t.Run("upgrade run failure already closed transportClosed", func(t *testing.T) {
		t.Parallel()
		client, transport, _, _ := newReconnectClient(t)
		client.transportMu.Lock()
		client.waitHandshake = make(chan struct{}, 1)
		// transportUpgrade closes the fresh channel itself when the new
		// transport's Run fails; the teardown must not close it a second time.
		closed := make(chan error)
		close(closed)
		client.transportClosed = closed
		client.handshakeErr = errors.New("run websocket failed")
		client.transportMu.Unlock()
		client.hadHandshake.Do(func() { close(client.waitHandshake) })

		transport.EXPECT().Stop().Return(nil)

		assert.False(t, client.awaitConnectionEstablished(time.Second))
	})
}

// A gate closed while Close() is in progress must not report success even when
// the close raced ahead of the handshake (first-select arm of the wait).
func TestAwaitConnectionEstablishedClosedGateWhileClosing(t *testing.T) {
	t.Parallel()
	client, _, _, _ := newReconnectClient(t)
	client.transportMu.Lock()
	client.waitHandshake = make(chan struct{}, 1)
	client.transportMu.Unlock()
	client.hadHandshake.Do(func() { close(client.waitHandshake) })
	atomic.StoreUint32(&client.closing, 1)

	assert.False(t, client.awaitConnectionEstablished(time.Second),
		"a gate released by Close's teardown is not a completed handshake")
}

// TestSuperviseContextErrorOnTransportClosed reproduces the review finding: a
// cancelled run context makes the transports report ctx.Err() on the
// transportClosed channel, and the supervisor's select may receive that error
// before noticing the context cancellation. That is a normal shutdown — it
// must not fire "reconnecting" (or the close handler when reconnect is
// disabled).
func TestSuperviseContextErrorOnTransportClosed(t *testing.T) {
	t.Parallel()
	client, _, _, _ := newReconnectClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	client.ctx = ctx
	cancel()

	fired := make(chan struct{}, 1)
	client.reconnectingHandler = func() { fired <- struct{}{} }
	client.closeHandler = func() { fired <- struct{}{} }

	// The transport reports the cancellation as a close error; the buffered
	// value is available immediately, so the select can take the closed arm.
	closed := make(chan error, 1)
	closed <- ctx.Err()
	done := make(chan struct{})
	client.supervise(closed, done)

	select {
	case <-fired:
		t.Fatal("supervise started reconnect logic for a context-based shutdown")
	default:
	}
}

// TestSuperviseCloseHandlerCallsClose reproduces the review finding: with
// reconnect disabled, the close handler runs on the supervisor goroutine, and
// a handler that reacts to the drop by calling Close() must not deadlock —
// neither waiting on supervisorDone (closed only after the handler returns)
// nor draining the transportClosed channel whose single notification the
// supervisor already consumed.
func TestSuperviseCloseHandlerCallsClose(t *testing.T) {
	t.Parallel()
	client, transport, _, _ := newReconnectClient(t)
	client.reconnect = false

	closed := make(chan error, 1)
	closed <- errors.New("dropped")
	client.transportMu.Lock()
	client.transportClosed = closed
	client.transportMu.Unlock()
	atomic.StoreUint32(&client.supervisorStarted, 1)

	transport.EXPECT().Stop().Return(nil).AnyTimes()

	closeErr := make(chan error, 1)
	client.closeHandler = func() {
		closeErr <- client.Close()
	}

	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		client.supervise(closed, done)
		close(finished)
	}()

	select {
	case err := <-closeErr:
		assert.NoError(t, err, "Close from the close handler must complete")
	case <-time.After(2 * time.Second):
		t.Fatal("Close called from the close handler deadlocked")
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("supervise did not return")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisorDone was not closed")
	}
}

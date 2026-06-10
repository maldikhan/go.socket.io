package engineio_v4_client

import (
	"errors"
	"sync/atomic"
	"time"
)

// errHandshakeIncomplete marks a reconnect attempt whose transport came up but
// whose Engine.IO handshake never completed (malformed OPEN packet, failed
// upgrade, or a transport drop before the OPEN arrived).
var errHandshakeIncomplete = errors.New("engine.io handshake did not complete")

// startSupervisor launches the reconnect supervisor for the current connection
// cycle. It is invoked once per successful handshake (guarded by superviseOnce,
// which is reset on every (re)connect attempt) so that exactly one supervisor
// goroutine watches the live transport at any time.
//
// The supervisor is the sole consumer of c.transportClosed once the connection
// is established: it blocks until the transport reports it is gone. A nil value
// means a graceful stop (Close, or an in-progress upgrade that already drained
// the channel) and the supervisor simply exits; a non-nil error means the
// connection dropped unexpectedly and, when reconnect is enabled, triggers the
// exponential-backoff reconnect loop.
//
// When the handshake is exercised in isolation (no Connect/startConnection set
// up the cycle), there is no transportClosed channel to watch, so startSupervisor
// is a no-op — there is nothing to supervise.
func (c *Client) startSupervisor() {
	c.superviseOnce.Do(func() {
		// Snapshot the cycle's channels and flip supervisorStarted in the same
		// critical section that startConnection/Close use, so Close() always
		// observes a consistent supervisorStarted/supervisorDone pair.
		c.transportMu.RLock()
		closed := c.transportClosed
		// Capture the done channel for THIS cycle. A successful reconnect
		// replaces c.supervisorDone with a fresh channel, so the supervisor must
		// close the one it was started with, not the current field.
		done := c.supervisorDone
		closing := atomic.LoadUint32(&c.closing) == 1 ||
			atomic.LoadUint32(&c.cycleAbandoned) == 1
		if closed != nil && !closing {
			atomic.StoreUint32(&c.supervisorStarted, 1)
		}
		c.transportMu.RUnlock()
		if closed == nil {
			return
		}
		if closing {
			// Close() already ran (or the reconnect loop abandoned this cycle):
			// the teardown owner drains transportClosed itself, so a supervisor
			// started now would compete for — or block forever on — that
			// channel.
			return
		}
		go c.supervise(closed, done)
	})
}

// supervise waits for the given connection's transportClosed channel to report a
// drop and reacts to it. closed and done are snapshotted by startSupervisor so
// that the supervisor watches the channels that belong to this connection cycle
// even if the fields are later replaced by a reconnect attempt.
func (c *Client) supervise(closed chan error, done chan struct{}) {
	if done != nil {
		defer close(done)
	}

	// ctxDone is the cancellation channel for the run context. It is nil when a
	// context was never assigned (defensive: the normal Connect path always sets
	// one), in which case the select simply blocks on the transport close.
	var ctxDone <-chan struct{}
	if c.ctx != nil {
		ctxDone = c.ctx.Done()
	}

	var err error
	select {
	case err = <-closed:
	case <-ctxDone:
		return
	}

	// A nil error means a graceful stop (Close or upgrade already drained the
	// channel for us). Nothing to do.
	if err == nil {
		return
	}

	// Close() sets closing before stopping the transport, and a cancelled run
	// context makes the transports report ctx.Err() on transportClosed — this
	// select may receive that error before noticing ctxDone. Both are
	// intentional shutdowns: starting reconnect logic (or invoking the close
	// handler below) would emit spurious lifecycle events, so bail out first.
	if c.stopRequested() {
		return
	}

	if !c.reconnect {
		// Reconnection disabled: the drop is terminal for this client. Release
		// the cycle state BEFORE surfacing it to the close handler, because a
		// handler that reacts by calling Close() must not deadlock: with
		// supervisorStarted still set, Close() would wait on supervisorDone —
		// which this goroutine only closes after the handler returns — and
		// with the flag cleared it would instead block draining the
		// transportClosed channel whose single notification was already
		// consumed above. The channel is closed here (the dead transport sends
		// exactly one value) so that drain returns immediately.
		atomic.StoreUint32(&c.supervisorStarted, 0)
		close(closed)

		// Surface the drop to the close handler so the behaviour matches the
		// pre-reconnect client exactly.
		c.handlerMu.RLock()
		handler := c.closeHandler
		c.handlerMu.RUnlock()
		if handler != nil {
			handler()
		}
		return
	}

	c.reconnectLoop(err)
}

// reconnectLoop performs the exponential-backoff reconnect attempts. It fires
// the "reconnecting" notification before the first attempt, retries up to
// reconnectAttempts times doubling the wait each time (capped at 8x the base
// wait), fires "reconnect" on success and "reconnect_failed" (followed by
// Close) when the attempts are exhausted. The loop stops promptly when the
// parent context is cancelled or Close() is called.
func (c *Client) reconnectLoop(cause error) {
	c.log.Warnf("engine.io connection dropped: %s, reconnecting", cause)
	c.fireReconnecting()

	base := c.reconnectWait
	if base <= 0 {
		base = time.Second
	}
	backoff := base
	maxBackoff := base * 8

	// ctxDone is nil when no run context was assigned (defensive: the normal
	// Connect path always sets one). A receive on a nil channel blocks forever,
	// so the wait select simply falls through to the backoff timer in that case.
	var ctxDone <-chan struct{}
	if c.ctx != nil {
		ctxDone = c.ctx.Done()
	}

	// closeCh is closed by Close() to wake this backoff wait immediately instead
	// of blocking for the remaining (possibly multi-second) backoff duration. It
	// is nil only in tests that build a Client without NewClient; a receive on a
	// nil channel blocks forever, so the wait simply falls back to the timer/ctx.
	closeCh := c.closeCh

	for attempt := 1; attempt <= c.reconnectAttempts; attempt++ {
		// Respect cancellation/close before waiting and retrying.
		if c.stopRequested() {
			return
		}

		select {
		case <-time.After(backoff):
		case <-ctxDone:
			return
		case <-closeCh:
			// Close() was called during the backoff wait; stop promptly.
			return
		}

		if c.stopRequested() {
			return
		}

		c.log.Debugf("reconnect attempt %d/%d", attempt, c.reconnectAttempts)
		err := c.startConnection(c.ctx)
		if err == nil {
			// startConnection only guarantees the handshake request was sent
			// (for polling it returns once the first poll response is queued);
			// the OPEN packet is processed asynchronously by the message loop.
			// Only declare the reconnect successful once the handshake actually
			// completed — otherwise a malformed response or a failed upgrade
			// would fire "reconnect" for a session that never opened and
			// silently end the retry loop.
			if c.awaitConnectionEstablished(maxBackoff) {
				c.log.Infof("engine.io reconnected on attempt %d", attempt)
				c.fireReconnect()
				return
			}
			if c.stopRequested() {
				return
			}
			err = errHandshakeIncomplete
		}
		c.log.Errorf("reconnect attempt %d failed: %s", attempt, err)

		// Exponential backoff with an upper bound so a long-lived outage does
		// not produce ever-growing waits.
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	c.log.Errorf("engine.io reconnect failed after %d attempts", c.reconnectAttempts)
	c.fireReconnectFailed()

	// We are running on the supervisor goroutine, so clear supervisorStarted
	// before calling Close(): otherwise Close() would wait on supervisorDone,
	// which this very goroutine only closes after Close() returns — a deadlock.
	atomic.StoreUint32(&c.supervisorStarted, 0)
	_ = c.Close()
}

// awaitConnectionEstablished waits for the attempt started by startConnection
// to finish its Engine.IO handshake: handleHandshake closes the waitHandshake
// gate only after the OPEN packet is processed and an eventual transport
// upgrade completed. It returns false — after tearing the half-open attempt
// down — when the transport dies first, the bound elapses, the run context is
// cancelled or Close() is called.
func (c *Client) awaitConnectionEstablished(bound time.Duration) bool {
	c.transportMu.RLock()
	wh := c.waitHandshake
	c.transportMu.RUnlock()

	// nil channels block forever in a select, which is exactly the desired
	// fallback for clients built without NewClient/Connect (tests).
	var ctxDone <-chan struct{}
	if c.ctx != nil {
		ctxDone = c.ctx.Done()
	}
	closeCh := c.closeCh

	timer := time.NewTimer(bound)
	defer timer.Stop()

	// Deliberately NOT selecting on transportClosed here: during a
	// polling->websocket upgrade, transportUpgrade (running on the message
	// loop goroutine) must consume the old transport's close notification from
	// that very channel — stealing it here would leave the upgrade blocked. A
	// transport that dies before the handshake is instead caught by the timer.
	select {
	case <-wh:
		// Close() also releases the handshake gate (so parked Send()s fail
		// fast); a gate closed by that teardown — not by a real handshake —
		// must not be reported as a successful reconnect.
		if c.stopRequested() {
			return false
		}
		if c.handshakeFailure() == nil {
			return true
		}
		// The gate was released by handleHandshake's upgrade-error path: the
		// handshake never established a session and the half-open attempt may
		// have already stopped its transports mid-upgrade. Fall through to the
		// abandon teardown below so the loop retries with fresh state instead
		// of firing "reconnect" for a dead connection.
	case <-ctxDone:
		// Context cancellation winds the transports and the message loop down
		// on its own, but nothing ever closes this cycle's handshake gate;
		// release it so Send() callers parked there fail fast.
		c.releaseGates()
		return false
	case <-closeCh:
		// Close() owns the teardown (attempt finished, supervisor not started,
		// so it drains transportClosed itself) and releases the gates.
		return false
	case <-timer.C:
	}

	// The bound elapsed. Re-check the gate under the lock and abandon the
	// cycle before touching the transport: transportUpgrade serialises its
	// drain of transportClosed under transportMu and bails out once the cycle
	// is abandoned, so after this section there is exactly one consumer (us or
	// an already-started supervisor) for the close notification.
	c.transportMu.Lock()
	completed := false
	select {
	case <-wh:
		// Same guards as above: a gate released by Close()'s teardown or by a
		// failed upgrade is not a completed handshake.
		completed = c.handshakeErr == nil
	default:
	}
	if completed {
		c.transportMu.Unlock()
		return !c.stopRequested()
	}
	atomic.StoreUint32(&c.cycleAbandoned, 1)
	transport := c.transport
	closed := c.transportClosed
	c.transportMu.Unlock()

	if transport != nil {
		_ = transport.Stop()
		select {
		case _, ok := <-closed:
			// Single close notification consumed; close the channel so a later
			// Close() doesn't block draining it. When the failed upgrade
			// already closed the channel (its new transport never ran), ok is
			// false and there is nothing left to close.
			if ok {
				close(closed)
			}
		case <-wh:
			// Photo-finish: the handshake completed while we were stopping the
			// transport and its supervisor consumed the close notification (it
			// observes the Stop() as a graceful close and exits). The transport
			// is stopped either way, so still report failure and let the loop
			// retry with fresh state.
		}
	}
	c.stopMessageLoop()
	// Release this cycle's handshake gate: nothing will ever close it (the
	// handshake never completed and the next attempt arms a fresh channel), so
	// a Send() parked on it — including emitters released by a final
	// "reconnect_failed" — would otherwise block forever instead of failing
	// fast against the stopped transport.
	c.releaseGates()
	return false
}

// handshakeFailure returns the handshake/upgrade error recorded for the
// current connection cycle, if any. A non-nil value means the handshake gate
// was released by a failure path, not by an established session.
func (c *Client) handshakeFailure() error {
	c.transportMu.RLock()
	defer c.transportMu.RUnlock()
	return c.handshakeErr
}

// stopRequested reports whether the reconnect loop should abort because the
// client is closing or the parent context has been cancelled.
func (c *Client) stopRequested() bool {
	if atomic.LoadUint32(&c.closing) == 1 {
		return true
	}
	// A nil run context (never assigned) is treated as "not cancelled" so the
	// loop does not dereference a nil context. supervise applies the same guard.
	if c.ctx == nil {
		return false
	}
	select {
	case <-c.ctx.Done():
		return true
	default:
		return false
	}
}

func (c *Client) fireReconnecting() {
	c.handlerMu.RLock()
	handler := c.reconnectingHandler
	c.handlerMu.RUnlock()
	if handler != nil {
		handler()
	}
}

func (c *Client) fireReconnect() {
	c.handlerMu.RLock()
	handler := c.reconnectHandler
	c.handlerMu.RUnlock()
	if handler != nil {
		handler()
	}
}

func (c *Client) fireReconnectFailed() {
	c.handlerMu.RLock()
	handler := c.reconnectFailedHand
	c.handlerMu.RUnlock()
	if handler != nil {
		handler()
	}
}

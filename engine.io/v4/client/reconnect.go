package engineio_v4_client

import (
	"sync/atomic"
	"time"
)

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
		c.transportMu.RLock()
		closed := c.transportClosed
		c.transportMu.RUnlock()
		if closed == nil {
			return
		}
		// Capture the done channel for THIS cycle. A successful reconnect
		// replaces c.supervisorDone with a fresh channel, so the supervisor must
		// close the one it was started with, not the current field.
		done := c.supervisorDone
		atomic.StoreUint32(&c.supervisorStarted, 1)
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

	// Close() sets closing before stopping the transport. If the drop is the
	// result of an intentional Close we must not attempt to reconnect.
	if atomic.LoadUint32(&c.closing) == 1 {
		return
	}

	if !c.reconnect {
		// Reconnection disabled: surface the drop to the close handler so the
		// behaviour matches the pre-reconnect client exactly.
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

	for attempt := 1; attempt <= c.reconnectAttempts; attempt++ {
		// Respect cancellation/close before waiting and retrying.
		if c.stopRequested() {
			return
		}

		select {
		case <-time.After(backoff):
		case <-c.ctx.Done():
			return
		}

		if c.stopRequested() {
			return
		}

		c.log.Debugf("reconnect attempt %d/%d", attempt, c.reconnectAttempts)
		err := c.startConnection(c.ctx)
		if err == nil {
			c.log.Infof("engine.io reconnected on attempt %d", attempt)
			c.fireReconnect()
			return
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

// stopRequested reports whether the reconnect loop should abort because the
// client is closing or the parent context has been cancelled.
func (c *Client) stopRequested() bool {
	if atomic.LoadUint32(&c.closing) == 1 {
		return true
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

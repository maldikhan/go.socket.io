package engineio_v4_client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
)

// errClientClosed is returned by connection attempts that race with Close().
var errClientClosed = errors.New("client is closed")

var ErrConnectAborted = errors.New("engine.io: connect aborted: client closed")

// ErrPingTimeout reports a peer that stopped sending Engine.IO heartbeats.
var ErrPingTimeout = errors.New("engine.io ping timeout")

type Client struct {
	url                 *url.URL
	ctx                 context.Context
	log                 Logger
	transport           Transport
	supportedTransports map[engineio_v4.EngineIOTransport]Transport
	// initialTransport is the transport the session started on, before any
	// polling->websocket upgrade. It is captured once on the first connect and
	// restored at the start of every reconnect attempt so a reconnect begins a
	// fresh Engine.IO session on the original transport instead of reusing an
	// upgraded websocket transport that carries a now-dead sid.
	initialTransport    Transport
	sid                 string
	pingInterval        *time.Ticker
	pingTimeout         time.Duration
	parser              Parser
	messageHandler      func([]byte)
	closeHandler        func()
	reconnectAttempts   int
	reconnectWait       time.Duration
	connectTimeout      time.Duration
	connectCancel       context.CancelFunc
	binaryHandler       func([]byte)
	reconnect           bool
	reconnectHandler    func()
	reconnectFailedHand func()
	reconnectingHandler func()
	waitUpgrade         chan struct{}
	hadUpgrade          sync.Once
	waitHandshake       chan struct{}
	hadHandshake        sync.Once
	stopPooling         chan struct{}
	transportClosed     chan error
	afterConnect        func()
	messages            chan engineio_v4.Frame
	messagesDone        chan struct{} // closed when messageLoop exits
	messagesStop        chan struct{} // closed to ask messageLoop to exit

	// closing is set to 1 by Close() before the transport is stopped so the
	// reconnect supervisor can distinguish an intentional shutdown from an
	// unexpected drop. It is read/written with sync/atomic for goroutine safety
	// (atomic.Bool is unavailable on Go 1.18).
	closing uint32
	// A transport packet callback may call Close synchronously. In that case
	// Close stops the transport without waiting for its own message loop.
	callbacksInFlight uint32

	// superviseOnce guards startSupervisor so exactly one supervisor goroutine
	// runs per connection cycle. It is reset before every (re)connect attempt.
	superviseOnce sync.Once
	// supervisorStarted is 1 while a supervisor goroutine is responsible for
	// draining transportClosed for the current cycle. Close() reads it to decide
	// whether to wait on the supervisor or drain transportClosed itself.
	supervisorStarted uint32
	// supervisorDone is closed when the supervisor goroutine for the current
	// connection cycle exits. Close() waits on it instead of reading
	// transportClosed directly, because the supervisor owns that channel.
	supervisorDone chan struct{}
	// attemptInFlight is 1 while startConnection is between its first state
	// reset and its final return. During that window the attempt owns the
	// cycle's transportClosed teardown (every exit path drains and closes it,
	// then closes supervisorDone), so Close() must wait on supervisorDone
	// instead of competing for the single transportClosed value.
	attemptInFlight uint32
	// cycleAbandoned is set to 1 by awaitConnectionEstablished when a reconnect
	// attempt's handshake does not complete within its bound: a late-arriving
	// OPEN packet must then no longer upgrade the transport or start a
	// supervisor, because the reconnect loop has already torn the cycle down
	// and will retry. Reset to 0 by startConnection for every new cycle.
	cycleAbandoned uint32

	// closeCh is closed exactly once by Close() to wake any goroutine parked in
	// the reconnect backoff wait, so Close() returns promptly instead of blocking
	// for the remaining backoff (which can be many seconds with WithReconnectWait).
	// closeOnce guards the close so concurrent Close() calls never double-close it.
	closeCh   chan struct{}
	closeOnce sync.Once

	// transportMu serializes access to the transport field and
	// the waitUpgrade / waitHandshake channels so that Send() never
	// races with transportUpgrade() or Close().
	transportMu sync.RWMutex

	// handshakeErr records a handshake/upgrade failure that released the
	// handshake gate without establishing the session, so a reconnect attempt
	// waiting on that gate reports the failure instead of success. Set by
	// handleHandshake's upgrade-error path before the gate is closed, cleared
	// by startConnection when the gate is re-armed. Guarded by transportMu.
	handshakeErr      error
	heartbeatTimeout  time.Duration
	heartbeatDeadline time.Time
	heartbeatReset    chan struct{}
	heartbeatErr      error
	// cycleCtx is the current attempt/session context, including a retry's
	// setup deadline cancellation. Upgrade must inherit the same context.
	cycleCtx    context.Context
	cycleCancel context.CancelFunc

	// handlerMu protects access to the handler fields
	// (messageHandler, closeHandler, afterConnect).
	// On() writes them from the user goroutine, while handlePacket()
	// and handleHandshake() read them from the transport goroutine.
	handlerMu sync.RWMutex

	// redactPayload, when true, replaces raw packet payloads in debug logs
	// with a size marker so production logs never leak message contents
	// (tokens, PII). The zero value is "don't redact" (verbose); NewClient
	// sets the production-safe default and WithDebugPayload(true) disables it.
	redactPayload bool
}

// payload returns a size marker for debug logging when payload redaction is
// enabled, or the raw data otherwise. The packet type/prefix stays visible
// either way.
func (c *Client) payload(data []byte) string {
	if c.redactPayload {
		return fmt.Sprintf("[redacted %d bytes]", len(data))
	}
	return string(data)
}

func (c *Client) Connect(ctx context.Context) error {
	if c.connectTimeout <= 0 {
		return c.connect(ctx)
	}

	// connCtx drives the dial/handshake phase. On success it simply remains
	// the session context: it is derived from ctx, so the caller's
	// cancellation still propagates, and it is only cancelled early (by the
	// timer below) when the handshake does not complete in time.
	connCtx, connCancel := context.WithCancel(ctx)
	c.transportMu.Lock()
	c.connectCancel = connCancel
	c.transportMu.Unlock()

	// timerFired distinguishes the timeout timer cancelling connCtx from a
	// concurrent Close() invoking the same cancel func: both leave
	// connCtx.Err() non-nil with the caller's ctx still live, but only the
	// former is a timeout.
	// timerDone is closed when the timeout callback has finished running, so
	// the success path can synchronize with a callback that Stop() failed to
	// prevent (Stop() does not wait for an in-flight AfterFunc to complete).
	var timerFired atomic.Bool
	timerDone := make(chan struct{})
	timer := time.AfterFunc(c.connectTimeout, func() {
		defer close(timerDone)
		timerFired.Store(true)
		connCancel()
	})

	// connectErr names the actual cause of an aborted connect: the caller's
	// context, the timeout timer, or (when it is neither) a concurrent Close().
	connectErr := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if timerFired.Load() {
			return fmt.Errorf("engine.io: connect timeout after %s: %w", c.connectTimeout, context.DeadlineExceeded)
		}
		return ErrConnectAborted
	}

	err := c.connect(connCtx)
	if err != nil {
		// Check connCtx before releasing it: connCancel() below sets
		// connCtx.Err() unconditionally, which would mask the real cause.
		aborted := connCtx.Err() != nil
		timer.Stop()
		connCancel()
		// connect() already stopped the transport and joined the message loop
		// on its error paths, but it leaves the handshake gate it published
		// open and the transport field set. Mark the client closed so a
		// concurrent or subsequent Send() wakes from the gate, observes the
		// nil transport and fails fast instead of blocking forever. The
		// transport needs no further Stop() here — connect() owns that.
		c.markClosed()
		if aborted {
			// The connect context was cancelled — by the caller, the timeout
			// timer or a concurrent Close(); name the actual cause.
			return connectErr()
		}
		return err
	}

	// The handshake request is sent; wait for the OPEN packet (handleHandshake
	// closes the waitHandshake gate after an eventual transport upgrade).
	c.transportMu.RLock()
	wh := c.waitHandshake
	c.transportMu.RUnlock()

	select {
	case <-wh:
		// Handshake complete. If a transport upgrade was initiated, also wait
		// for the probe/pong exchange: handleHandshake publishes waitUpgrade
		// before closing waitHandshake, and the gate is only closed once the
		// upgrade finishes (or fails). Without this, Connect() could return
		// success while Send() still blocks on the unfinished upgrade.
		c.transportMu.RLock()
		wu := c.waitUpgrade
		c.transportMu.RUnlock()
		if wu != nil {
			select {
			case <-wu:
			case <-connCtx.Done():
			}
		}
		if !timer.Stop() {
			// The timeout callback already started in its own goroutine; wait
			// for it to finish before judging success. Otherwise Connect could
			// observe connCtx still live, return nil, and the in-flight
			// callback would then cancel the context now serving the
			// established session, tearing it down at the timeout boundary.
			<-timerDone
		}
		// The gates are also released by Close() (so parked Send()s fail fast)
		// and by a failed transport upgrade, so waking here does not by itself
		// mean the handshake succeeded: a concurrent Close() or an upgrade
		// error must not let Connect report success. Only return success when
		// the client is still live and no handshake failure was recorded.
		c.transportMu.RLock()
		closed := c.transport == nil
		hsErr := c.handshakeErr
		c.transportMu.RUnlock()
		if connCtx.Err() == nil && !closed && hsErr == nil {
			return nil
		}
		if hsErr != nil && connCtx.Err() == nil {
			// The handshake/upgrade itself failed: tear down and surface that
			// error rather than a generic cancellation cause.
			_ = c.Close()
			return fmt.Errorf("engine.io: connect failed: %w", hsErr)
		}
		// The timeout fired (or the client was closed) while the handshake was
		// completing: the transports are already shutting down, so report the
		// failure to the caller.
	case <-connCtx.Done():
		timer.Stop()
	}

	// The connection phase was aborted (timeout, caller cancellation or a
	// concurrent Close): tear down whatever was started and name the cause.
	_ = c.Close()
	return connectErr()
}

func (c *Client) connect(ctx context.Context) error {
	c.transportMu.Lock()
	c.ctx = ctx
	c.transportMu.Unlock()
	// Mark the client as live so that an unexpected transport drop triggers a
	// reconnect rather than being mistaken for an intentional Close.
	atomic.StoreUint32(&c.closing, 0)
	// Capture the initial (pre-upgrade) transport once so that reconnects can
	// always start a fresh session on it. Connect is the only entry point that
	// runs before any upgrade, so c.transport is still the initial transport here.
	c.transportMu.Lock()
	if c.initialTransport == nil {
		c.initialTransport = c.transport
	}
	c.transportMu.Unlock()
	return c.startConnection(ctx)
}

// startConnection runs a single connection cycle: it (re)initialises every
// per-connection channel and sync.Once guard, runs the transport, starts the
// message loop and requests the handshake. It is called by Connect for the
// initial connection and by the reconnect supervisor for each retry, so it must
// fully reset the connection state before touching the transport — otherwise a
// stale, already-closed channel or a fired sync.Once would deadlock or
// double-close on a reconnect.
func (c *Client) startConnection(ctx context.Context) error {
	// Tear down any message loop left running by a previous cycle before we
	// replace its channels below. On a fresh connect there is nothing running,
	// so this is a no-op; on a reconnect it stops the goroutine that was still
	// reading the now-dead transport's messages channel, preventing a leak.
	c.stopMessageLoop()

	// Restore the initial transport and clear the stale sid so the cycle starts
	// a brand-new Engine.IO session with a real handshake. On the first connect
	// the transport is already the initial one and the sid is already empty, so
	// this is a no-op; after a polling->websocket upgrade it reverts the upgraded
	// websocket transport (whose RequestHandshake is a no-op) back to the polling
	// transport that performs a real handshake. Close() sets the transport to nil;
	// if a reconnect attempt races with Close() we must bail out instead of
	// dereferencing a nil transport — and we must not restore the transport after
	// Close() already nil'ed it, or the attempt would resurrect a closed client.
	c.transportMu.Lock()
	if atomic.LoadUint32(&c.closing) == 1 {
		c.transportMu.Unlock()
		return errClientClosed
	}
	// From here on this attempt owns the cycle teardown; Close() observing the
	// flag will wait on supervisorDone (closed by every exit path below)
	// instead of draining transportClosed itself.
	atomic.StoreUint32(&c.attemptInFlight, 1)
	if c.initialTransport != nil {
		c.transport = c.initialTransport
	}
	c.sid = ""
	transport := c.transport
	if transport == nil {
		atomic.StoreUint32(&c.attemptInFlight, 0)
		c.transportMu.Unlock()
		return errClientClosed
	}

	// Reset the per-cycle supervisor state under the lock so Close() observes a
	// consistent pair: a fresh supervisorDone always comes with
	// supervisorStarted == 0 (no goroutine owns the new channel until the next
	// successful handshake runs startSupervisor). Without this, Close() racing
	// with an in-flight reconnect attempt could wait on a supervisorDone channel
	// that no goroutine will ever close — a deadlock.
	transportClosed := make(chan error, 1)
	messages := make(chan engineio_v4.Frame, 100)
	c.messages = messages
	c.superviseOnce = sync.Once{}
	c.supervisorDone = make(chan struct{})
	atomic.StoreUint32(&c.supervisorStarted, 0)
	atomic.StoreUint32(&c.cycleAbandoned, 0)

	// Reset the upgrade gate: a previous cycle may have left waitUpgrade closed
	// and hadUpgrade fired. They are re-armed lazily by transportUpgrade(), but
	// clearing them here keeps Send() from observing a stale, closed gate from
	// the prior connection.
	c.hadUpgrade = sync.Once{}
	c.waitUpgrade = nil
	// Arm the handshake before Run can publish an OPEN packet. WebSocket
	// transports may deliver it immediately, before RequestHandshake returns.
	c.hadHandshake = sync.Once{}
	c.waitHandshake = make(chan struct{})
	c.handshakeErr = nil
	c.heartbeatTimeout = 0
	c.heartbeatDeadline = time.Time{}
	c.heartbeatReset = make(chan struct{}, 1)
	c.heartbeatErr = nil
	c.cycleCtx = ctx

	// Run transport before starting the message loop so that a Run()
	// failure doesn't leak a goroutine.
	c.transportClosed = transportClosed
	c.transportMu.Unlock()

	err := transport.Run(ctx, c.url, c.sid, messages, transportClosed)
	if err != nil {
		close(transportClosed)
		close(c.supervisorDone)
		atomic.StoreUint32(&c.attemptInFlight, 0)
		return err
	}

	// Close() may have raced with this attempt between the closing check above
	// and transport.Run: such a Close() stopped a transport that was not running
	// yet, so its stop had no effect. Re-check and tear the fresh transport down
	// ourselves instead of leaving a live connection behind on a closed client.
	if atomic.LoadUint32(&c.closing) == 1 {
		_ = transport.Stop()
		<-transportClosed
		close(transportClosed)
		close(c.supervisorDone)
		atomic.StoreUint32(&c.attemptInFlight, 0)
		return errClientClosed
	}

	// Start the message loop only after Run() succeeds. messagesDone and
	// messages are captured locally and passed to messageLoop so the goroutine
	// owns this cycle's channels and never races with a later reconnect cycle
	// that reassigns the c.messages / c.messagesDone fields.
	messagesDone := make(chan struct{})
	messagesStop := make(chan struct{})
	c.transportMu.Lock()
	c.messagesDone = messagesDone
	c.messagesStop = messagesStop
	c.transportMu.Unlock()
	go c.messageLoop(ctx, messages, messagesDone, messagesStop)

	err = transport.RequestHandshake()
	if err != nil {
		// Clean up: stop transport and wait for the message loop to exit so we
		// don't leak a goroutine. The cleanup operates on the channels captured
		// for this cycle so it is unaffected by a later reconnect cycle.
		c.stopAttemptTransport()
		c.stopMessageLoop()
		<-messagesDone
		// Detach the closed channels from the client so a later Close() (e.g.
		// after reconnect exhaustion) doesn't close messages a second time or
		// wait on the already-finished loop.
		c.transportMu.Lock()
		if c.messages == messages {
			c.messages = nil
		}
		if c.messagesDone == messagesDone {
			c.messagesDone = nil
		}
		c.messagesStop = nil
		c.transportMu.Unlock()
		// The handshake gate armed above will never be closed by a handshake;
		// release it so Send() callers parked on it fail fast against the
		// stopped transport instead of waiting on a channel no future cycle
		// closes (every cycle arms a fresh one).
		c.releaseGates()
		close(c.supervisorDone)
		atomic.StoreUint32(&c.attemptInFlight, 0)
		return err
	}

	// Final closing check, atomic with clearing attemptInFlight: a Close() that
	// raced with the handshake request either locked before us (it saw
	// attemptInFlight == 1 and is waiting on supervisorDone, which the teardown
	// below closes) or locks after us and sees attemptInFlight == 0 with the
	// transportClosed channel already drained and closed.
	c.transportMu.Lock()
	closingNow := atomic.LoadUint32(&c.closing) == 1
	if !closingNow {
		atomic.StoreUint32(&c.attemptInFlight, 0)
	}
	c.transportMu.Unlock()
	if closingNow {
		c.stopAttemptTransport()
		// Stop the message loop via its stop signal; the messages channel is
		// left for Close() to close exactly once.
		c.stopMessageLoop()
		// The concurrent Close() released the gates of the PREVIOUS cycle; the
		// fresh gate armed above (after Close's release) must be released by
		// this teardown, or Send() callers parked on it would never wake.
		c.releaseGates()
		close(c.supervisorDone)
		atomic.StoreUint32(&c.attemptInFlight, 0)
		return errClientClosed
	}
	// OPEN/probe can finish while RequestHandshake is still returning. The
	// attempt owned teardown until now, so only now may the supervisor take it.
	if c.connectionEstablished() {
		c.startSupervisor()
	}

	return nil
}

// stopAttemptTransport joins the cycle's current transport. OPEN may already
// have upgraded it while RequestHandshake was returning; the original Run's
// transport/channel would then refer to an already drained polling cycle.
func (c *Client) stopAttemptTransport() {
	c.transportMu.RLock()
	transport := c.transport
	closed := c.transportClosed
	c.transportMu.RUnlock()
	if transport != nil {
		_ = transport.Stop()
	}
	if closed != nil {
		if _, ok := <-closed; ok {
			close(closed)
		}
	}
}

func (c *Client) connectionEstablished() bool {
	c.transportMu.RLock()
	defer c.transportMu.RUnlock()
	if c.handshakeErr != nil || c.waitHandshake == nil {
		return false
	}
	select {
	case <-c.waitHandshake:
	default:
		return false
	}
	if c.waitUpgrade != nil {
		select {
		case <-c.waitUpgrade:
		default:
			return false
		}
	}
	return true
}

func (c *Client) messageLoop(ctx context.Context, messages <-chan engineio_v4.Frame, messagesDone chan struct{}, stop <-chan struct{}) {
	if messagesDone != nil {
		defer close(messagesDone)
	}
	if messages == nil {
		c.log.Errorf("messages channel is nil, can't read transport messages")
		return
	}
	c.transportMu.RLock()
	heartbeatReset := c.heartbeatReset
	c.transportMu.RUnlock()
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var heartbeatTick <-chan time.Time
	armHeartbeat := func() {
		c.transportMu.RLock()
		deadline := c.heartbeatDeadline
		c.transportMu.RUnlock()
		if deadline.IsZero() {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(time.Until(deadline))
		heartbeatTick = timer.C
	}
	for {
		select {
		case message, ok := <-messages:
			if !ok {
				return
			}
			err := c.handleFrame(message)
			if err != nil {
				c.log.Errorf("handle packet error: %s", err)
			}
		case <-heartbeatReset:
			armHeartbeat()
		case <-heartbeatTick:
			c.transportMu.Lock()
			// A queued PING may have refreshed the deadline before the reset
			// notification was selected. Prefer that new deadline.
			if time.Now().Before(c.heartbeatDeadline) {
				c.transportMu.Unlock()
				armHeartbeat()
				continue
			}
			c.heartbeatErr = ErrPingTimeout
			transport := c.transport
			c.transportMu.Unlock()
			if transport != nil {
				_ = transport.Stop()
			}
			return
		case <-stop:
			// A new connection cycle asked this loop to exit so it can take over
			// with fresh channels (reconnect teardown).
			return
		case <-ctx.Done():
			c.log.Warnf("context done, engine.io client stopped processing messages")
			return
		}
	}
}

// stopMessageLoop signals the current cycle's messageLoop to exit and waits for
// it to finish. It is idempotent and safe to call when no loop is running: a nil
// stop channel means there is nothing to stop, and a stop channel that was
// already closed is left untouched (a non-blocking receive detects the closed
// state). The dropped transport has already stopped, so it will not write to the
// old messages channel after this point; we therefore tear the loop down via the
// dedicated stop signal rather than closing the messages channel here, leaving
// that channel for Close() to close exactly once.
func (c *Client) stopMessageLoop() {
	c.transportMu.Lock()
	stop := c.messagesStop
	done := c.messagesDone
	c.messagesStop = nil
	c.messagesDone = nil
	c.transportMu.Unlock()

	if stop == nil {
		return
	}
	// Close the stop channel unless a previous call already did. A non-blocking
	// receive that succeeds means the channel is already closed.
	select {
	case <-stop:
	default:
		close(stop)
	}
	if done != nil {
		<-done
	}
}

func (c *Client) transportUpgrade(transport Transport) error {
	// Lock while mutating state that Send() reads.
	c.transportMu.Lock()
	c.hadUpgrade = sync.Once{}
	c.waitUpgrade = make(chan struct{}, 1)

	// failUpgrade closes the upgrade gate so that Send() callers waiting
	// on waitUpgrade are unblocked even when the upgrade fails.
	failUpgrade := func(err error) error {
		c.hadUpgrade.Do(func() {
			close(c.waitUpgrade)
		})
		c.transportMu.Unlock()
		return err
	}

	// A reconnect attempt that timed out has already torn this cycle down (and
	// owns the transportClosed drain); a late upgrade must not stop/replace the
	// transport or compete for the close notification.
	if atomic.LoadUint32(&c.cycleAbandoned) == 1 || atomic.LoadUint32(&c.closing) == 1 || c.transport == nil {
		return failUpgrade(errClientClosed)
	}

	err := c.transport.Stop()
	if err != nil {
		c.log.Errorf("stop transport: %s", err)
		return failUpgrade(err)
	}
	<-c.transportClosed
	c.transport = transport

	c.transportClosed = make(chan error, 1)
	cycleCtx := c.cycleCtx
	if cycleCtx == nil {
		cycleCtx = c.ctx
	}
	err = c.transport.Run(cycleCtx, c.url, c.sid, c.messages, c.transportClosed)
	if err != nil {
		close(c.transportClosed)
		c.log.Errorf("run transport: %s", err)
		return failUpgrade(err)
	}
	c.transportMu.Unlock()

	err = c.sendPacket(&engineio_v4.Message{
		Type: engineio_v4.PacketPing,
		Data: []byte("probe"),
	})
	if err != nil {
		// Probe failed — unblock Send() callers waiting on the upgrade gate.
		c.transportMu.Lock()
		c.hadUpgrade.Do(func() {
			close(c.waitUpgrade)
		})
		c.transportMu.Unlock()
	}
	return err
}

func (c *Client) handleHandshake(data []byte) error {
	c.log.Debugf("apply handshake: %s", c.payload(data))

	handshakeResp := &engineio_v4.HandshakeResponse{}
	err := json.Unmarshal(data, handshakeResp)
	if err != nil {
		return c.failHandshake(err)
	}

	if handshakeResp.Sid == "" {
		return c.failHandshake(fmt.Errorf("handshake error: no sid"))
	}
	// Validate before creating/resetting tickers; malformed negative or
	// overflowing server values must never panic the packet goroutine.
	maxMS := int64(math.MaxInt64 / int64(time.Millisecond))
	intervalMS := int64(handshakeResp.PingInterval)
	timeoutMS := int64(handshakeResp.PingTimeout)
	if intervalMS < 0 || timeoutMS < 0 || intervalMS > maxMS || timeoutMS > maxMS-intervalMS {
		return c.failHandshake(errors.New("handshake error: invalid heartbeat interval or timeout"))
	}
	if c.stopRequested() {
		return errClientClosed
	}

	// Upgrade transports
	for _, transport := range c.supportedTransports {
		transport.SetHandshake(handshakeResp)
	}

	c.transportMu.Lock()
	c.sid = handshakeResp.Sid
	if handshakeResp.PingInterval != 0 {
		if c.pingInterval != nil {
			// Reset reuses the existing ticker (shared with the polling transport),
			// so we don't break the transport's pinger reference.
			c.pingInterval.Reset(time.Duration(handshakeResp.PingInterval) * time.Millisecond)
		} else {
			c.pingInterval = time.NewTicker(time.Duration(handshakeResp.PingInterval) * time.Millisecond)
		}
	}

	if handshakeResp.PingTimeout != 0 {
		c.pingTimeout = time.Duration(handshakeResp.PingTimeout) * time.Millisecond
	}
	c.heartbeatTimeout = time.Duration(intervalMS+timeoutMS) * time.Millisecond
	c.transportMu.Unlock()
	c.refreshHeartbeat()

	// Perform protocol upgrade BEFORE unblocking Send() callers so that
	// waitUpgrade is published before waitHandshake is closed. Otherwise
	// a Send() waiting on waitHandshake would wake with waitUpgrade == nil
	// and write on the old transport during the upgrade window.
	if len(handshakeResp.Upgrades) > 0 {
		for _, newTransportName := range handshakeResp.Upgrades {
			c.transportMu.RLock()
			currentTransport := c.transport
			c.transportMu.RUnlock()
			if currentTransport == nil {
				return errClientClosed
			}
			if currentTransport.Transport() == engineio_v4.EngineIOTransport(newTransportName) {
				break
			}
			if newTransport, found := c.supportedTransports[engineio_v4.EngineIOTransport(newTransportName)]; found {
				err = c.transportUpgrade(newTransport)
				if err != nil {
					// Record the failure BEFORE releasing the gate: a reconnect
					// attempt waiting on waitHandshake must observe it and
					// report the failed upgrade instead of a successful
					// reconnect (gate closure alone does not mean the session
					// was established).
					c.transportMu.Lock()
					c.handshakeErr = err
					c.transportMu.Unlock()
					// Close the handshake gate so that any Send() caller
					// waiting on waitHandshake doesn't block forever.
					c.hadHandshake.Do(func() {
						close(c.waitHandshake)
					})
					return err
				}

				break
			} else {
				c.log.Warnf("unsupported upgrade: %s", newTransportName)
			}
		}
	}

	// Close the handshake gate AFTER the upgrade gate (waitUpgrade) is
	// already published so Send() sees both gates atomically.
	c.hadHandshake.Do(func() {
		close(c.waitHandshake)
	})

	// Start the reconnect supervisor now that the connection is fully
	// established (handshake done, any upgrade complete). Starting it here —
	// rather than in Connect — guarantees the supervisor watches the final
	// transportClosed channel for this cycle and never competes with the
	// synchronous <-transportClosed read performed by transportUpgrade().
	if c.connectionEstablished() {
		c.startSupervisor()
	}

	// Call onConnect hook in a goroutine so that messageLoop can continue
	// processing engine.io packets (e.g. the WebSocket upgrade probe response
	// "3probe") while the hook is running. If afterConnect calls Send(),
	// Send() waits on waitUpgrade, which is only closed after messageLoop
	// processes "3probe" — calling afterConnect inline would deadlock.
	// The handler is copied under handlerMu so a concurrent On() registration
	// is race-free, and invoked via the local copy (never c.afterConnect).
	c.handlerMu.RLock()
	handler := c.afterConnect
	c.handlerMu.RUnlock()
	if handler != nil {
		go handler()
	}

	return nil
}

func (c *Client) handlePacket(packetData []byte) error {
	c.log.Debugf("handle packet: %s", c.payload(packetData))
	packet, err := c.parser.Parse(packetData)
	if err != nil {
		c.log.Errorf("Can't parse packet: %s %v", c.payload(packetData), err)
		return err
	}

	c.log.Debugf("handle: %d %s", packet.Type, c.payload(packet.Data))

	switch packet.Type {
	case engineio_v4.PacketOpen:
		err := c.handleHandshake(
			packet.Data,
		)
		if err != nil {
			c.log.Errorf("handle handshake error: %s", err)
			return err
		}
	case engineio_v4.PacketClose:
		c.handlerMu.RLock()
		handler := c.closeHandler
		c.handlerMu.RUnlock()
		if handler != nil {
			atomic.AddUint32(&c.callbacksInFlight, 1)
			func() {
				defer atomic.AddUint32(&c.callbacksInFlight, ^uint32(0))
				handler()
			}()
		}
	case engineio_v4.PacketPing:
		c.refreshHeartbeat()
		err := c.sendPacket(&engineio_v4.Message{
			Type: engineio_v4.PacketPong,
		})
		if err != nil {
			c.log.Errorf("send ping error: %s", err)
		}
		return err
	case engineio_v4.PacketPong:
		if string(packet.Data) == "probe" {
			err := c.sendPacket(&engineio_v4.Message{
				Type: engineio_v4.PacketUpgrade,
			})
			if err != nil {
				// Record the failure BEFORE releasing the upgrade gate (like
				// the transportUpgrade error path does for the handshake
				// gate): a timed Connect() waiting on waitUpgrade must report
				// the failed upgrade write instead of success.
				c.transportMu.Lock()
				c.handshakeErr = err
				c.transportMu.Unlock()
			}
			c.hadUpgrade.Do(func() {
				close(c.waitUpgrade)
			})
			if err != nil {
				c.log.Errorf("send upgrade error: %s", err)
				return err
			}
			c.log.Debugf("Protocol upgraded")
			c.startSupervisor()
		}
	case engineio_v4.PacketMessage:
		c.handlerMu.RLock()
		handler := c.messageHandler
		binaryHandler := c.binaryHandler
		c.handlerMu.RUnlock()
		if packet.Binary {
			handler = binaryHandler
		}
		if handler != nil {
			atomic.AddUint32(&c.callbacksInFlight, 1)
			func() {
				defer atomic.AddUint32(&c.callbacksInFlight, ^uint32(0))
				handler(packet.Data)
			}()
		}
	}
	return nil
}

func (c *Client) refreshHeartbeat() {
	c.transportMu.Lock()
	defer c.transportMu.Unlock()
	if c.heartbeatTimeout <= 0 {
		return
	}
	c.heartbeatDeadline = time.Now().Add(c.heartbeatTimeout)
	select {
	case c.heartbeatReset <- struct{}{}:
	default:
	}
}

func (c *Client) sendPacket(packet *engineio_v4.Message) error {
	if packet == nil {
		return errors.New("no packet provided")
	}

	msg, err := c.parser.Serialize(packet)
	if err != nil {
		return err
	}

	c.transportMu.RLock()
	transport := c.transport
	c.transportMu.RUnlock()
	if transport == nil {
		return errClientClosed
	}
	return transport.SendMessage(msg)
}

func (c *Client) Send(message []byte) error {
	// Snapshot wait channels under the lock so that we never miss a
	// channel created by a concurrent transportUpgrade or Connect.
	c.transportMu.RLock()
	wh := c.waitHandshake
	c.transportMu.RUnlock()

	if wh != nil {
		if err := c.waitForGate(wh); err != nil {
			return err
		}
	}
	// The handshake may have published an upgrade gate while Send waited.
	c.transportMu.RLock()
	wu := c.waitUpgrade
	c.transportMu.RUnlock()
	if wu != nil {
		if err := c.waitForGate(wu); err != nil {
			return err
		}
	}

	// Re-acquire the lock to read the current transport safely.
	c.transportMu.RLock()
	t := c.transport
	c.transportMu.RUnlock()

	if t == nil {
		return errors.New("client is closed")
	}

	// Use the snapshotted transport directly instead of sendPacket()
	// which re-reads c.transport and would race with Close()/upgrade.
	msg, err := c.parser.Serialize(&engineio_v4.Message{
		Type: engineio_v4.PacketMessage,
		Data: message,
	})
	if err != nil {
		return err
	}
	return t.SendMessage(msg)
}

func (c *Client) waitForGate(gate <-chan struct{}) error {
	c.transportMu.RLock()
	closed := c.transport == nil
	c.transportMu.RUnlock()
	if closed || atomic.LoadUint32(&c.closing) == 1 {
		return errClientClosed
	}
	if atomic.LoadUint32(&c.closing) == 1 {
		return errClientClosed
	}
	c.transportMu.RLock()
	ctx := c.ctx
	c.transportMu.RUnlock()
	var ctxDone <-chan struct{}
	if ctx != nil {
		ctxDone = ctx.Done()
	}
	select {
	case <-c.closeCh:
		return errClientClosed
	case <-ctxDone:
		if atomic.LoadUint32(&c.closing) == 1 {
			return errClientClosed
		}
		return ctx.Err()
	case <-gate:
		return nil
	}
}

func (c *Client) On(event string, handler func([]byte)) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()

	switch event {
	case "connect":
		c.afterConnect = func() { handler(nil) }
	case "message":
		c.messageHandler = handler
	case "binary":
		c.binaryHandler = handler
	case "close":
		c.closeHandler = func() { handler(nil) }
	case "reconnecting":
		c.reconnectingHandler = func() { handler(nil) }
	case "reconnect":
		c.reconnectHandler = func() { handler(nil) }
	case "reconnect_failed":
		c.reconnectFailedHand = func() { handler(nil) }
	}
}

// releaseGates closes the current cycle's handshake/upgrade gates (each via
// its sync.Once guard) so Send() callers parked on them wake up, observe the
// dead/nil transport and fail fast. Every (re)connect cycle arms a FRESH
// waitHandshake channel, so a gate left open by an abandoned cycle would park
// its waiters forever — no future cycle ever closes the old channel object.
// Called when a cycle is abandoned (handshake never completed), when its
// handshake request fails after the gate was armed, and by Close().
func (c *Client) releaseGates() {
	c.transportMu.Lock()
	defer c.transportMu.Unlock()
	c.hadHandshake.Do(func() {
		if c.waitHandshake != nil {
			close(c.waitHandshake)
		}
	})
	c.hadUpgrade.Do(func() {
		if c.waitUpgrade != nil {
			close(c.waitUpgrade)
		}
	})
}

func (c *Client) Close() error {
	// Mark the client as intentionally closing BEFORE stopping the transport so
	// the reconnect supervisor treats the resulting drop as a graceful shutdown
	// and does not attempt to reconnect.
	atomic.StoreUint32(&c.closing, 1)

	// Wake any goroutine parked in the reconnect backoff wait so Close() does not
	// block for the remaining backoff. Guarded by closeOnce so concurrent Close()
	// calls (or a Close() racing with reconnect exhaustion) never double-close it.
	// closeCh is nil only for Clients built directly in tests without NewClient;
	// guarding here keeps Close() safe in that case too.
	if c.closeCh != nil {
		c.closeOnce.Do(func() { close(c.closeCh) })
	}

	// Write-lock to prevent new Send() calls from acquiring the transport
	// while we are tearing it down. Setting transport to nil ensures that
	// any Send() arriving after Close releases the lock will see nil and
	// fail fast instead of writing on a stopped transport. All per-cycle
	// channels are snapshotted in the same critical section that mutates them
	// in startConnection, so the supervisorStarted/supervisorDone pair is
	// always consistent: a fresh (unowned) supervisorDone is only ever seen
	// together with supervisorStarted == 0.
	c.transportMu.Lock()
	t := c.transport
	c.transport = nil
	connectCancel := c.connectCancel
	c.connectCancel = nil
	transportClosed := c.transportClosed
	// Wait on supervisorDone when either a supervisor goroutine owns the
	// cycle's transportClosed channel, or a startConnection attempt is in
	// flight (its exit paths drain transportClosed and close supervisorDone).
	// Competing with them for the single transportClosed value would leave one
	// of the parties blocked forever.
	waitForCycle := atomic.LoadUint32(&c.supervisorStarted) == 1 ||
		atomic.LoadUint32(&c.attemptInFlight) == 1
	// NewClient allocates a placeholder close channel before any producer is
	// running. Closing an unused client must not wait for a value that can never
	// be produced. ctx/messages also cover directly constructed test clients.
	hadConnection := waitForCycle || c.cycleCtx != nil || c.ctx != nil || c.messages != nil
	supervisorDone := c.supervisorDone
	cycleCancel := c.cycleCancel
	c.cycleCancel = nil
	c.transportMu.Unlock()
	if cycleCancel != nil {
		cycleCancel()
	}

	if connectCancel != nil {
		connectCancel()
	}

	// Release the handshake/upgrade gates AFTER the transport is nil'ed above:
	// a Send() parked on a gate wakes, re-reads the transport, sees nil and
	// fails fast with "client is closed" instead of blocking forever on a gate
	// that no future cycle will close.
	c.releaseGates()

	// Stop the ping ticker to prevent goroutine leak
	c.transportMu.RLock()
	pingInterval := c.pingInterval
	c.transportMu.RUnlock()
	if pingInterval != nil {
		pingInterval.Stop()
	}

	if t == nil {
		return nil
	}

	err := t.Stop()
	if err != nil {
		return err
	}

	// The reconnect supervisor is the sole owner of transportClosed once the
	// connection is established. If it is running, let it drain transportClosed
	// (Stop() just triggered the close notification) and wait for it to exit. If
	// no supervisor is running — the handshake never completed, or we are being
	// called from the supervisor itself on reconnect exhaustion — drain
	// transportClosed here so the messageLoop teardown can proceed.
	// Packet callbacks can synchronously call Close while an attempt/supervisor
	// is joining this message loop. Release the callback so that owner can
	// finish; its teardown still owns the transport-close notification.
	inCallback := atomic.LoadUint32(&c.callbacksInFlight) != 0
	if waitForCycle && !inCallback {
		if supervisorDone != nil {
			<-supervisorDone
		}
	} else if !waitForCycle && hadConnection && transportClosed != nil {
		<-transportClosed
	}

	// Read the message channels only after the cycle owner has finished: a
	// failing startConnection attempt closes the messages channel and detaches
	// it under the lock, so snapshotting it before the wait above could make
	// Close() close the same channel a second time.
	c.transportMu.Lock()
	c.messages = nil
	messagesDone := c.messagesDone
	c.messagesDone = nil
	messagesStop := c.messagesStop
	c.messagesStop = nil
	c.transportMu.Unlock()

	// Transport goroutines are the producers of messages. Never close their
	// channel from the consumer: cancellation can race an already selected
	// send. A separate broadcast stops the loop without risking that panic.
	if messagesStop != nil {
		select {
		case <-messagesStop:
		default:
			close(messagesStop)
		}
	}
	// Wait for messageLoop goroutine to finish so that no mock/logger
	// calls happen after the caller returns (prevents test panics and
	// ensures clean shutdown).
	if messagesDone != nil && !inCallback {
		<-messagesDone
	}
	return nil
}

func (c *Client) SendBinary(message []byte) error {
	c.transportMu.RLock()
	wh := c.waitHandshake
	c.transportMu.RUnlock()

	if wh != nil {
		if err := c.waitForGate(wh); err != nil {
			return err
		}
	}
	// The handshake may have published an upgrade gate while Send waited.
	c.transportMu.RLock()
	wu := c.waitUpgrade
	c.transportMu.RUnlock()
	if wu != nil {
		if err := c.waitForGate(wu); err != nil {
			return err
		}
	}

	c.transportMu.RLock()
	t := c.transport
	c.transportMu.RUnlock()

	if t == nil {
		return errors.New("client is closed")
	}

	// Pass the raw attachment bytes to the transport; each transport applies its
	// own wire encoding (raw binary frame over websocket, base64 "b"-record over
	// HTTP long-polling).
	binaryTransport, ok := t.(BinaryTransport)
	if !ok {
		return errors.New("transport does not support binary attachments")
	}
	return binaryTransport.SendBinary(message)
}

func (c *Client) handleFrame(frame engineio_v4.Frame) error {
	if frame.IsBinary {
		c.log.Debugf("handle binary frame: %s", c.payload(frame.Data))
		c.handlerMu.RLock()
		handler := c.binaryHandler
		c.handlerMu.RUnlock()
		if handler != nil {
			atomic.AddUint32(&c.callbacksInFlight, 1)
			func() {
				defer atomic.AddUint32(&c.callbacksInFlight, ^uint32(0))
				handler(frame.Data)
			}()
		}
		return nil
	}

	// Defensively skip an empty text frame: it carries no packet to parse.
	if len(frame.Data) == 0 {
		return nil
	}
	return c.handlePacket(frame.Data)
}

func (c *Client) failHandshake(err error) error {
	c.transportMu.Lock()
	c.handshakeErr = err
	c.transportMu.Unlock()
	c.hadHandshake.Do(func() {
		if c.waitHandshake != nil {
			close(c.waitHandshake)
		}
	})
	return err
}

func (c *Client) markClosed() (Transport, context.CancelFunc) {
	// Write-lock to prevent new Send() calls from acquiring the transport
	// while we are tearing it down. Setting transport to nil ensures that
	// any Send() arriving after the lock is released will see nil and fail
	// fast instead of writing on a stopped transport.
	c.transportMu.Lock()
	defer c.transportMu.Unlock()
	t := c.transport
	c.transport = nil
	connectCancel := c.connectCancel
	c.connectCancel = nil
	c.hadHandshake.Do(func() {
		if c.waitHandshake != nil {
			close(c.waitHandshake)
		}
	})
	c.hadUpgrade.Do(func() {
		if c.waitUpgrade != nil {
			close(c.waitUpgrade)
		}
	})
	return t, connectCancel
}

// SupportsBinary reports whether the current transport can send binary
// attachments. Wrappers can use it before writing a multi-frame packet header.
func (c *Client) SupportsBinary() bool {
	c.transportMu.RLock()
	t := c.transport
	c.transportMu.RUnlock()
	if _, ok := t.(BinaryTransport); !ok {
		return false
	}
	if capability, ok := t.(interface{ SupportsBinary() bool }); ok {
		return capability.SupportsBinary()
	}
	return true
}

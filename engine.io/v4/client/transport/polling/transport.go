package engineio_v4_client_transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	// Note: we use atomic.LoadUint32/StoreUint32 instead of atomic.Bool
	// to maintain compatibility with Go 1.18 (atomic.Bool requires Go 1.19).

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
)

// errTransportStopped is an internal sentinel returned by poll() when it
// observes stopCh while blocked sending to c.messages. It stays inside the
// package: pollingLoop handles it directly, and RequestHandshake maps it to
// context.Canceled before it can reach public API callers.
var errTransportStopped = errors.New("transport stopped")

type Transport struct {
	log        Logger
	httpClient HttpClient
	pinger     *time.Ticker

	url *url.URL
	sid string
	ctx context.Context

	messages    chan<- []byte
	onClose     chan<- error
	stopPooling chan struct{}

	// reqCtx scopes the in-flight poll/handshake HTTP request. It is derived
	// from the run context in Run() and cancelled by Stop(), so Stop() can
	// interrupt a long-poll GET that the server is holding open (e.g. during a
	// polling->websocket upgrade) instead of waiting up to one ping interval.
	reqCtx     context.Context
	pollCancel context.CancelFunc

	// handshakeDone gates the continuous polling loop until a session id is
	// known, so it never issues a GET while RequestHandshake()'s own poll is in
	// flight — engine.io rejects two concurrent polls on the same session. It is
	// closed by SetHandshake(), or immediately by Run() when the session id is
	// already known (e.g. this transport is the target of an upgrade), so the
	// gate is order-independent and can't deadlock if SetHandshake() happens to
	// run before Run(). Guarded by mu.
	handshakeDone chan struct{}

	// stopCh is closed by Stop() to broadcast the stop signal to any goroutine
	// currently blocked inside poll(). Using a closed channel (instead of a
	// buffered send) means both poll() and pollingLoop can observe the stop
	// independently without competing to consume a single value.
	stopCh   chan struct{}
	stopOnce sync.Once

	stopped        uint32 // atomic; 0 = running, 1 = stopped
	maxPayloadSize int64

	// pollErrorBackoff is the pause after a failed poll before retrying, so a
	// persistently failing server does not turn the loop into a hot spin.
	pollErrorBackoff time.Duration

	// mu guards the sid field, which is written by SetHandshake()/Run() and
	// read by buildHttpUrl() from the polling goroutine.
	mu sync.RWMutex

	// redactPayload, when true, replaces raw payloads in debug logs with a
	// size marker. Zero value is verbose; NewTransport sets the safe default.
	redactPayload bool
}

// payload returns a size marker when redaction is enabled, or the raw data.
func (c *Transport) payload(data []byte) string {
	if c.redactPayload {
		return fmt.Sprintf("[redacted %d bytes]", len(data))
	}
	return string(data)
}

func (c *Transport) SetHandshake(handshake *engineio_v4.HandshakeResponse) {
	c.mu.Lock()
	c.sid = handshake.Sid
	c.mu.Unlock()
	pingInterval := 10 * time.Second
	if handshake.PingInterval != 0 {
		pingInterval = time.Duration(handshake.PingInterval) * time.Millisecond
	}
	c.pinger.Reset(pingInterval)
	// Release the polling loop now that a session id is available.
	c.mu.Lock()
	c.markHandshakeDoneLocked()
	c.mu.Unlock()
}

// markHandshakeDoneLocked closes the handshake gate exactly once. The caller
// must hold c.mu. It is a no-op if the gate has not been allocated yet (Run()
// not called) or is already closed.
func (c *Transport) markHandshakeDoneLocked() {
	if c.handshakeDone == nil {
		return
	}
	select {
	case <-c.handshakeDone:
		// already closed
	default:
		close(c.handshakeDone)
	}
}

func (c *Transport) RequestHandshake() error {
	err := c.poll()
	if errors.Is(err, errTransportStopped) {
		// Transport was stopped while waiting to deliver the handshake response.
		// Map the internal sentinel to a public cancellation error so callers
		// never observe an unexported implementation detail.
		if ctxErr := c.ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return context.Canceled
	}
	return err
}

func (c *Transport) Transport() engineio_v4.EngineIOTransport {
	return engineio_v4.TransportPolling
}

func (c *Transport) Run(
	ctx context.Context,
	url *url.URL,
	sid string,
	messagesChan chan<- []byte,
	onClose chan<- error,
) error {
	c.ctx = ctx
	// Derive a request-scoped context that Stop() can cancel independently of
	// the parent context, so an in-flight long-poll can be interrupted promptly.
	c.reqCtx, c.pollCancel = context.WithCancel(ctx)
	c.mu.Lock()
	c.sid = sid
	// Fresh handshake gate for this run. If a session id is already known (e.g.
	// this transport is the target of an upgrade, where SetHandshake() has
	// already run with handshakeDone still nil), open it immediately so
	// pollingLoop never waits for a SetHandshake() that won't come again.
	c.handshakeDone = make(chan struct{})
	if sid != "" {
		c.markHandshakeDoneLocked()
	}
	c.mu.Unlock()
	c.url = url
	c.messages = messagesChan
	c.onClose = onClose
	// Reset the stop state so the transport can be safely reused after a Stop().
	atomic.StoreUint32(&c.stopped, 0)
	// Reinitialize stopPooling channel to ensure fresh channel for new run.
	c.stopPooling = make(chan struct{}, 1)
	// Reinitialize the stop broadcast channel and its sync.Once as well —
	// otherwise a reused transport would observe the previously-closed stopCh
	// (and the spent stopOnce) and poll() would bail out immediately.
	c.stopCh = make(chan struct{})
	c.stopOnce = sync.Once{}

	go func() {
		c.pinger.Stop()
		err := c.pollingLoop()
		if err != nil {
			c.log.Errorf("pollingLoop error: %s", err)
		}
		// Reconnection is handled at the engine.io Client level: finishPolling
		// reports the drop via onClose, and the client's reconnect supervisor
		// performs a fresh handshake. The transport does not reconnect itself.
	}()

	return nil
}

func (c *Transport) Stop() error {
	// CAS ensures only one concurrent Stop() call proceeds;
	// subsequent calls see stopped==1 and return immediately.
	if !atomic.CompareAndSwapUint32(&c.stopped, 0, 1) {
		return nil
	}
	// Close stopCh to broadcast the stop signal to any poll() call that is
	// currently blocked sending to c.messages, and cancel reqCtx to interrupt a
	// poll that is blocked waiting for the server's long-poll response. sync.Once
	// guarantees we only do this once even if Stop() is called concurrently.
	c.stopOnce.Do(func() {
		if c.pollCancel != nil {
			c.pollCancel()
		}
		close(c.stopCh)
	})
	// Non-blocking send: if pollingLoop already exited (e.g. via
	// ctx.Done()), nobody is reading from stopPooling and a blocking
	// send would deadlock.
	select {
	case c.stopPooling <- struct{}{}:
	default:
	}
	return nil
}

// defaultPollErrorBackoff is the default pause after a failed poll before
// retrying (see Transport.pollErrorBackoff).
const defaultPollErrorBackoff = time.Second

// pollingLoop continuously long-polls the server. Each poll() issues a GET that
// the server holds open until it has data (or sends a keepalive ping), then the
// loop immediately issues the next one. This keeps server->client latency low,
// independent of the ping interval; a successful poll returns quickly when data
// is queued and otherwise blocks until the server responds.
func (c *Transport) pollingLoop() error {
	// Wait for the handshake to assign a session id before polling. Polling
	// earlier would race RequestHandshake()'s poll and trigger a concurrent-poll
	// rejection from the server. A nil gate (transport driven without Run, e.g.
	// in unit tests) means "already handshaked".
	c.mu.RLock()
	gate := c.handshakeDone
	c.mu.RUnlock()
	if gate != nil {
		select {
		case <-gate:
		case <-c.stopPooling:
			return c.finishPolling(true, nil)
		case <-c.ctx.Done():
			return c.finishPolling(false, c.ctx.Err())
		}
	}

	for {
		// Honor a stop/cancel requested between polls before issuing a new GET.
		select {
		case <-c.stopPooling:
			return c.finishPolling(true, nil)
		case <-c.ctx.Done():
			return c.finishPolling(false, c.ctx.Err())
		default:
		}

		err := c.poll()
		if errors.Is(err, errTransportStopped) {
			return c.finishPolling(true, nil)
		}
		if err != nil {
			// A request cancelled by Stop() (reqCtx) or by the parent context is
			// a normal shutdown, not a transport error.
			if atomic.LoadUint32(&c.stopped) == 1 {
				return c.finishPolling(true, nil)
			}
			if c.ctx.Err() != nil {
				return c.finishPolling(false, c.ctx.Err())
			}
			// Genuine transient error (network blip, server hiccup): log and back
			// off briefly, but stay responsive to stop/cancel during the pause.
			c.log.Errorf("poll error: %s", err)
			select {
			case <-time.After(c.pollErrorBackoff):
			case <-c.stopPooling:
				return c.finishPolling(true, nil)
			case <-c.ctx.Done():
				return c.finishPolling(false, c.ctx.Err())
			}
		}
	}
}

// finishPolling performs the shared loop teardown: it marks the transport
// stopped and notifies the engine client (onClose always carries the run
// context's error — nil for an upgrade/Stop-driven exit, the cancellation cause
// for a context shutdown). stopRequested selects the matching debug log.
func (c *Transport) finishPolling(stopRequested bool, ret error) error {
	if stopRequested {
		c.log.Debugf("stop polling")
	} else {
		c.log.Debugf("context done, stop http polling")
	}
	atomic.StoreUint32(&c.stopped, 1)
	if c.onClose != nil {
		c.onClose <- c.ctx.Err()
	}
	return ret
}

func (c *Transport) buildHttpUrl() *url.URL {
	c.mu.RLock()
	sid := c.sid
	c.mu.RUnlock()

	reqURL := &url.URL{
		Scheme: c.url.Scheme,
		Host:   c.url.Host,
		Path:   c.url.Path,
	}

	query := reqURL.Query()

	query.Set("transport", "polling")
	query.Set("EIO", "4")
	query.Set("sid", sid)

	reqURL.RawQuery = query.Encode()
	return reqURL
}

func (c *Transport) poll() error {
	c.log.Debugf("run polling")

	if c.messages == nil {
		c.log.Errorf("messages channel is nil, can't read transport messages")
		return errors.New("messages channel is nil")
	}

	// Use the request-scoped context so Stop() can cancel a long-poll that the
	// server is holding open. Fall back to the run context when the transport is
	// driven directly (e.g. in unit tests) without Run() setting reqCtx.
	reqCtx := c.reqCtx
	if reqCtx == nil {
		reqCtx = c.ctx
	}
	req, err := http.NewRequestWithContext(reqCtx, "GET", c.buildHttpUrl().String(), nil)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	// A non-2xx response (e.g. 400 "Session ID unknown" after the session was
	// dropped) is surfaced as an error so the loop backs off instead of
	// forwarding the error body as an engine.io packet and hot-spinning.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("unexpected polling response status %d", resp.StatusCode)
	}

	// Limit payload size to guard against OOM from a malicious/buggy server.
	// maxPayloadSize <= 0 means unlimited (e.g. a Transport built directly
	// without the constructor default of 4MB).
	var reader io.Reader = resp.Body
	if c.maxPayloadSize > 0 {
		// Read one byte more than the limit to detect oversized payloads.
		// Guard against int64 overflow when maxPayloadSize is near math.MaxInt64.
		readLimit := c.maxPayloadSize + 1
		if readLimit <= 0 {
			readLimit = c.maxPayloadSize // overflow: clamp to maxPayloadSize
		}
		reader = io.LimitReader(resp.Body, readLimit)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return err
	}

	// Check if payload exceeds the maximum size.
	if c.maxPayloadSize > 0 && int64(len(body)) > c.maxPayloadSize {
		return fmt.Errorf("inbound payload size %d exceeds maximum allowed %d", len(body), c.maxPayloadSize)
	}

	c.log.Debugf("receiveHttp: %s", c.payload(body))

	select {
	case c.messages <- body:
	case <-c.ctx.Done():
		return c.ctx.Err()
	case <-c.stopCh:
		// Stop() was called while we were blocked sending. stopCh is a closed
		// channel (broadcast), so pollingLoop's own stopPooling case remains
		// intact — it will still exit cleanly via the value Stop() enqueued.
		return errTransportStopped
	}
	return nil
}

func (c *Transport) SendMessage(msg []byte) error {

	c.log.Debugf("sendHttp: %s", c.payload(msg))
	req, err := http.NewRequestWithContext(c.ctx, "POST", c.buildHttpUrl().String(), bytes.NewReader(msg))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	_, _ = io.Copy(io.Discard, resp.Body)
	c.log.Debugf("receiveHttp: %s", resp.Status)
	return nil
}

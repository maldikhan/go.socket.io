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

	// stopCh is closed by Stop() to broadcast the stop signal to any goroutine
	// currently blocked inside poll(). Using a closed channel (instead of a
	// buffered send) means both poll() and pollingLoop can observe the stop
	// independently without competing to consume a single value.
	stopCh   chan struct{}
	stopOnce sync.Once

	stopped uint32 // atomic; 0 = running, 1 = stopped
}

func (c *Transport) SetHandshake(handshake *engineio_v4.HandshakeResponse) {
	c.sid = handshake.Sid
	pingInterval := 10 * time.Second
	if handshake.PingInterval != 0 {
		pingInterval = time.Duration(handshake.PingInterval) * time.Millisecond
	}
	c.pinger.Reset(pingInterval)
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
	c.sid = sid
	c.url = url
	c.messages = messagesChan
	c.onClose = onClose

	go func() {
		c.pinger.Stop()
		err := c.pollingLoop()
		if err != nil {
			c.log.Errorf("pollingLoop error: %s", err)
		}
		// TODO: reconnect
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
	// currently blocked sending to c.messages. sync.Once guarantees we only
	// close once even if Stop() is called from multiple goroutines.
	c.stopOnce.Do(func() {
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

func (c *Transport) pollingLoop() error {
	for {
		select {
		case _, ok := <-c.pinger.C:
			if !ok {
				c.log.Warnf("pinger closed: stop pooling")
				return errors.New("pinger closed")
			}
			err := c.poll()
			if errors.Is(err, errTransportStopped) {
				c.log.Debugf("stop polling")
				atomic.StoreUint32(&c.stopped, 1)
				if c.onClose != nil {
					c.onClose <- c.ctx.Err()
				}
				return nil
			}
			if err != nil {
				c.log.Errorf("poll error: %s", err)
			}
		case <-c.stopPooling:
			c.log.Debugf("stop polling")
			atomic.StoreUint32(&c.stopped, 1)
			if c.onClose != nil {
				c.onClose <- c.ctx.Err()
			}
			return nil
		case <-c.ctx.Done():
			c.log.Debugf("context done, stop http polling")
			atomic.StoreUint32(&c.stopped, 1)
			if c.onClose != nil {
				c.onClose <- c.ctx.Err()
			}
			return c.ctx.Err()
		}
	}
}

func (c *Transport) buildHttpUrl() *url.URL {

	reqURL := &url.URL{
		Scheme: c.url.Scheme,
		Host:   c.url.Host,
		Path:   c.url.Path,
	}

	query := reqURL.Query()

	query.Set("transport", "polling")
	query.Set("EIO", "4")
	query.Set("sid", c.sid)

	reqURL.RawQuery = query.Encode()
	return reqURL
}

func (c *Transport) poll() error {
	c.log.Debugf("run polling")

	if c.messages == nil {
		c.log.Errorf("messages channel is nil, can't read transport messages")
		return errors.New("messages channel is nil")
	}

	req, err := http.NewRequestWithContext(c.ctx, "GET", c.buildHttpUrl().String(), nil)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	c.log.Debugf("receiveHttp: %s", string(body))

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

	c.log.Debugf("sendHttp: %s", msg)
	req, err := http.NewRequestWithContext(c.ctx, "POST", c.buildHttpUrl().String(), bytes.NewReader(msg))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	c.log.Debugf("receiveHttp: %s", resp.Status)
	return err
}

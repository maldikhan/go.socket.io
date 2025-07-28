package engineio_v4_client_transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
)

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

	stopped bool
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
	return c.poll()
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
	if c.stopped {
		return nil
	}
	c.stopPooling <- struct{}{}
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
			if err != nil {
				c.log.Errorf("poll error: %s", err)
			}
		case <-c.stopPooling:
			c.log.Debugf("stop polling")
			c.stopped = true
			if c.onClose != nil {
				c.onClose <- c.ctx.Err()
			}
			return nil
		case <-c.ctx.Done():
			c.log.Debugf("context done, stop http polling")
			c.stopped = true
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

	c.messages <- body
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

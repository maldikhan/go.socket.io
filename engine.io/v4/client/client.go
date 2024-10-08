package engineio_v4_client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
)

type Client struct {
	url                 *url.URL
	ctx                 context.Context
	log                 Logger
	transport           Transport
	supportedTransports map[engineio_v4.EngineIOTransport]Transport
	sid                 string
	pingInterval        *time.Ticker
	pingTimeout         time.Duration
	parser              Parser
	messageHandler      func([]byte)
	closeHandler        func()
	reconnectAttempts   int
	reconnectWait       time.Duration
	waitUpgrade         chan struct{}
	hadUpgrade          sync.Once
	waitHandshake       chan struct{}
	hadHandshake        sync.Once
	stopPooling         chan struct{}
	transportClosed     chan error
	afterConnect        func()
	messages            chan []byte
}

func (c *Client) Connect(ctx context.Context) error {
	c.ctx = ctx

	// Run main loop
	c.messages = make(chan []byte, 100)
	go c.messageLoop(c.messages)

	// Run transport
	c.transportClosed = make(chan error, 1)
	err := c.transport.Run(ctx, c.url, c.sid, c.messages, c.transportClosed)
	if err != nil {
		close(c.transportClosed)
		return err
	}

	c.hadHandshake = sync.Once{}
	c.waitHandshake = make(chan struct{}, 1)
	err = c.transport.RequestHandshake()
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) messageLoop(messages <-chan []byte) {
	if messages == nil {
		c.log.Errorf("messages channel is nil, can't read transport messages")
		return
	}
	for {
		select {
		case message, ok := <-messages:
			if !ok {
				return
			}
			err := c.handlePacket(message)
			if err != nil {
				c.log.Errorf("handle packet error: %s", err)
			}
		case <-c.ctx.Done():
			c.log.Warnf("context done, engine.io client stopped processing messages")
			return
		}
	}
}

func (c *Client) transportUpgrade(transport Transport) error {
	c.hadUpgrade = sync.Once{}
	c.waitUpgrade = make(chan struct{}, 1)
	err := c.transport.Stop()
	if err != nil {
		c.log.Errorf("stop transport: %s", err)
		return err
	}
	<-c.transportClosed
	c.transport = transport

	c.transportClosed = make(chan error, 1)
	err = c.transport.Run(c.ctx, c.url, c.sid, c.messages, c.transportClosed)
	if err != nil {
		close(c.transportClosed)
		c.log.Errorf("run transport: %s", err)
		return err
	}

	return c.sendPacket(&engineio_v4.Message{
		Type: engineio_v4.PacketPing,
		Data: []byte("probe"),
	})
}

func (c *Client) handleHandshake(data []byte) error {
	c.log.Debugf("apply handshake: %s", string(data))

	handshakeResp := &engineio_v4.HandshakeResponse{}
	err := json.Unmarshal(data, handshakeResp)
	if err != nil {
		return err
	}

	if handshakeResp.Sid == "" {
		return fmt.Errorf("handshake error: no sid")
	}

	// Upgrade transports
	for _, transport := range c.supportedTransports {
		transport.SetHandshake(handshakeResp)
	}

	c.sid = handshakeResp.Sid
	if handshakeResp.PingInterval != 0 {
		c.pingInterval = time.NewTicker(time.Duration(handshakeResp.PingInterval) * time.Millisecond)
	}

	if handshakeResp.PingTimeout != 0 {
		c.pingTimeout = time.Duration(handshakeResp.PingTimeout) * time.Millisecond
	}

	// close handshake
	c.hadHandshake.Do(func() {
		close(c.waitHandshake)
	})

	// Call onConnect hook
	if c.afterConnect != nil {
		c.afterConnect()
	}

	// Perform protocol upgrade
	if len(handshakeResp.Upgrades) > 0 {
		for _, newTransportName := range handshakeResp.Upgrades {
			if c.transport.Transport() == engineio_v4.EngineIOTransport(newTransportName) {
				break
			}
			if newTransport, found := c.supportedTransports[engineio_v4.EngineIOTransport(newTransportName)]; found {
				return c.transportUpgrade(newTransport)
			} else {
				c.log.Warnf("unsupported upgrade: %s", handshakeResp.Upgrades[0])
			}
		}
	}

	return nil
}

func (c *Client) handlePacket(packetData []byte) error {
	c.log.Debugf("handle packet: %s", string(packetData))
	packet, err := c.parser.Parse(packetData)
	if err != nil {
		c.log.Errorf("Can't parse packet: %s %v", string(packetData), err)
		return err
	}

	c.log.Debugf("handle: %d %s", packet.Type, packet.Data)

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
		if c.closeHandler != nil {
			c.closeHandler()
		}
	case engineio_v4.PacketPing:
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
			c.hadUpgrade.Do(func() {
				close(c.waitUpgrade)
			})
			if err != nil {
				c.log.Errorf("send upgrade error: %s", err)
				return err
			} else {
				c.log.Debugf("Protocol upgraded")
			}
		}
	case engineio_v4.PacketMessage:
		if c.messageHandler != nil {
			c.messageHandler(packet.Data)
		}
	}
	return nil
}

func (c *Client) sendPacket(packet *engineio_v4.Message) error {
	if packet == nil {
		return errors.New("no packet provided")
	}

	msg, err := c.parser.Serialize(packet)
	if err != nil {
		return err
	}

	return c.transport.SendMessage(msg)
}

func (c *Client) Send(message []byte) error {
	if c.waitHandshake != nil {
		<-c.waitHandshake
	}
	if c.waitUpgrade != nil {
		<-c.waitUpgrade
	}

	return c.sendPacket(&engineio_v4.Message{
		Type: engineio_v4.PacketMessage,
		Data: message,
	})
}

func (c *Client) On(event string, handler func([]byte)) {
	switch event {
	case "connect":
		c.afterConnect = func() { handler(nil) }
	case "message":
		c.messageHandler = handler
	case "close":
		c.closeHandler = func() { handler(nil) }
	}
}

func (c *Client) Close() error {
	err := c.transport.Stop()
	if err != nil {
		return err
	}
	if c.transportClosed != nil {
		<-c.transportClosed
	}
	if c.messages != nil {
		close(c.messages)
	}
	return nil
}

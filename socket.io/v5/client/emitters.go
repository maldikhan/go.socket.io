package socketio_v5_client

import (
	"errors"
	"time"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	"github.com/maldikhan/go.socket.io/socket.io/v5/client/emit"
)

func (c *Client) Emit(event interface{}, args ...interface{}) error {

	return c.defaultNs.Emit(event, args...)
}

func (n *namespace) Emit(event interface{}, args ...interface{}) error {

	if n.waitConnected != nil {
		// Snapshot ctx under lock to prevent data race
		n.client.mutex.RLock()
		ctx := n.client.ctx
		n.client.mutex.RUnlock()

		select {
		case <-n.waitConnected:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	emitOptions := &emit.EmitOptions{}

	emitEvent := &socketio_v5.Event{}
	isEventDefined := true

	switch eventData := event.(type) {
	case string:
		emitEvent.Name = eventData
		isEventDefined = false
	case socketio_v5.Event:
		emitEvent = &eventData
	case *socketio_v5.Event:
		emitEvent = eventData
	default:
		return errors.New("invalid event type")
	}

	optLen := 0
	for i := 0; i < len(args); i++ {
		switch v := args[i].(type) {
		case emit.EmitOption:
			v(emitOptions)
		default:
			if isEventDefined {
				return errors.New("you can pass ether Event() ether eventName and params, not both")
			}
			args[optLen] = args[i]
			optLen++
		}
	}

	if !isEventDefined {
		emitEvent.Payloads = args[:optLen]
	}

	if emitOptions.AckCallback() == nil && emitOptions.Timeout() == nil {
		return n.client.sendPacket(&socketio_v5.Message{
			NS:    n.name,
			Type:  socketio_v5.PacketEvent,
			Event: emitEvent,
		})
	}

	return n.client.sendPacketWithAckTimeout(
		&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			Event: emitEvent,
		},
		emitOptions.AckCallback(),
		emitOptions.Timeout(),
		emitOptions.TimeoutCallback(),
	)
}

func (c *Client) sendPacketWithAckTimeout(
	packet *socketio_v5.Message,
	callback interface{},
	timeout *time.Duration,
	timeoutCallback func(),
) error {

	var wrappedCallback func([]interface{})
	if callback != nil {
		wrappedCallback = c.parser.WrapCallback(callback)
		if wrappedCallback == nil {
			return errors.New("callback must be a function")
		}
	}

	var done chan []interface{}
	if timeout != nil {
		done = make(chan []interface{}, 1)
	}

	c.mutex.Lock()
	c.ackCounter++
	counter := c.ackCounter
	packet.AckId = &counter
	if timeout != nil {
		c.ackCallbacks[counter] = func(param []interface{}) {
			done <- param
		}
	} else {
		if wrappedCallback != nil {
			c.ackCallbacks[counter] = wrappedCallback
		}
	}
	c.mutex.Unlock()

	if timeout != nil {
		// Snapshot ctx under lock to prevent data race
		c.mutex.RLock()
		ctx := c.ctx
		c.mutex.RUnlock()

		go func(done chan []interface{}) {
			select {
			case <-c.timer.After(*timeout):
				c.logger.Warnf("ack timeout: %v", timeout)
				c.mutex.Lock()
				delete(c.ackCallbacks, counter)
				c.mutex.Unlock()
				if timeoutCallback != nil {
					timeoutCallback()
				}
			case <-ctx.Done():
				c.logger.Warnf("context is done: %v", ctx.Err())
			case param := <-done:
				if wrappedCallback != nil {
					wrappedCallback(param)
				}
			}
		}(done)
	}

	return c.sendPacket(packet)
}

func (c *Client) sendPacket(packet *socketio_v5.Message) error {
	// Events carrying []byte payloads are sent as a binary packet: a text header
	// with {"_placeholder"} markers followed by the raw attachment frames.
	if packet.Event != nil && c.parser.HasBinary(packet.Event) {
		header, attachments, err := c.parser.SerializeBinary(packet)
		if err != nil {
			return err
		}
		// Serialization happens outside the lock; only the transport writes are
		// serialized so the header and all of its attachment frames are emitted
		// contiguously, with no foreign frame interleaved by a concurrent emit.
		c.sendMu.Lock()
		defer c.sendMu.Unlock()
		if err := c.engineio.Send(header); err != nil {
			return err
		}
		for _, attachment := range attachments {
			if err := c.engineio.SendBinary(attachment); err != nil {
				return err
			}
		}
		return nil
	}

	packetData, err := c.parser.Serialize(packet)
	if err != nil {
		return err
	}
	// Hold sendMu so a single-frame packet cannot slip between a binary
	// header and its attachment frames emitted by a concurrent binary send.
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.engineio.Send(packetData)
}

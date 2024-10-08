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
			case <-c.ctx.Done():
				c.logger.Warnf("context is done: %v", c.ctx.Err())
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
	packetData, err := c.parser.Serialize(packet)
	if err != nil {
		return err
	}
	return c.engineio.Send(packetData)
}

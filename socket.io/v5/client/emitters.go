package socketio_v5_client

import (
	"time"

	socketio_v5 "maldikhan/go.socket.io/socket.io/v5"
)

func (c *Client) Emit(event string, args ...interface{}) error {
	return c.defaultNs.Emit(event, args...)
}

func (c *Client) EmitWathAckTimeout(timeout time.Duration, callback interface{}, onTimeout func(), event string, args ...interface{}) error {
	return c.EmitWathAckRawTimeout(timeout, c.defaultNs.parseEventCallback(callback), onTimeout, event, args...)
}

func (c *Client) EmitWathAckRawTimeout(timeout time.Duration, callback func([]interface{}), onTimeout func(), event string, args ...interface{}) error {
	return c.defaultNs.EmitWathAckRawTimeout(timeout, callback, onTimeout, event, args...)
}

func (c *Client) EmitWathAck(callback interface{}, event string, args ...interface{}) error {
	return c.EmitWathAckRaw(c.defaultNs.parseEventCallback(callback), event, args...)
}

func (c *Client) EmitWathAckRaw(callback func([]interface{}), event string, args ...interface{}) error {
	return c.defaultNs.EmitWathAckRaw(callback, event, args...)
}

func (n *namespace) Emit(event string, args ...interface{}) error {
	return n.client.sendPacket(&socketio_v5.Message{
		NS:   n.name,
		Type: socketio_v5.PacketEvent,
		Event: &socketio_v5.Event{
			Name:     event,
			Payloads: args,
		},
	})
}

func (n *namespace) EmitWathAckTimeout(timeout time.Duration, callback interface{}, onTimeout func(), event string, args ...interface{}) error {
	return n.EmitWathAckRawTimeout(timeout, n.parseEventCallback(callback), onTimeout, event, args)
}
func (n *namespace) EmitWathAckRawTimeout(timeout time.Duration, callback func([]interface{}), onTimeout func(), event string, args ...interface{}) error {
	return n.client.sendPacketWithAckTimeout(
		&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			Event: &socketio_v5.Event{
				Name:     event,
				Payloads: args,
			},
		},
		callback,
		&timeout,
		onTimeout,
	)
}

func (n *namespace) EmitWathAck(callback interface{}, event string, args ...interface{}) error {
	return n.EmitWathAckRaw(n.parseEventCallback(callback), event, args)
}
func (n *namespace) EmitWathAckRaw(callback func([]interface{}), event string, args ...interface{}) error {
	return n.client.sendPacketWithAck(&socketio_v5.Message{
		Type: socketio_v5.PacketEvent,
		Event: &socketio_v5.Event{
			Name:     event,
			Payloads: args,
		},
	}, callback)
}

func (c *Client) sendPacketWithAck(packet *socketio_v5.Message, callback func([]interface{})) error {
	return c.sendPacketWithAckTimeout(packet, callback, nil, nil)
}

func (c *Client) sendPacketWithAckTimeout(
	packet *socketio_v5.Message,
	callback func([]interface{}),
	timeout *time.Duration,
	timeoutCallback func(),
) error {

	var done chan []interface{}
	if timeout != nil {
		done = make(chan []interface{})
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
		if callback != nil {
			c.ackCallbacks[counter] = callback
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
				if callback != nil {
					callback(param)
				}
			}
		}(done)
	}

	packetData, err := c.parser.Serialize(packet)

	if err != nil {
		return err
	}

	return c.engineio.Send(packetData)
}

func (c *Client) sendPacket(packet *socketio_v5.Message) error {
	packetData, err := c.parser.Serialize(packet)
	if err != nil {
		return err
	}
	return c.engineio.Send(packetData)
}

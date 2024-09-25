package socketio_v5_client

import socketio_v5 "maldikhan/go.socket.io/socket.io/v5"

func (c *Client) OnRaw(event string, handler func([]interface{})) {
	c.defaultNs.On(event, handler)
}
func (c *Client) On(event string, handler interface{}) {
	c.defaultNs.On(event, c.defaultNs.parseEventCallback(handler))
}

func (n *namespace) On(event string, handler func([]interface{})) {
	n.handlers[event] = append(n.handlers[event], handler)
}

func (c *Client) onMessage(data []byte) {
	c.logger.Debugf("socketio receive %s", string(data))

	msg, err := c.parser.Parse(data)
	if err != nil {
		c.logger.Errorf("Can't parse message: %v", err)
		return
	}

	ns := c.namespaces[msg.NS]

	switch msg.Type {
	case socketio_v5.PacketEvent:
		c.handleEvent(ns, msg.Event)
	case socketio_v5.PacketAck:
		c.handleAck(msg.Event, *msg.AckId)
	case socketio_v5.PacketConnectError:
		c.logger.Errorf("Connect error: %v", *msg.ErrorMessage)
	}
}

func (c *Client) handleEvent(ns *namespace, event *socketio_v5.Event) {
	handlers, ok := ns.handlers[event.Name]
	if !ok {
		c.logger.Infof("No handlers for event: %s", event.Name)
		return
	}

	for _, handler := range handlers {
		go handler(event.Payloads)
	}
}

func (c *Client) handleAck(event *socketio_v5.Event, ackId int) {
	c.mutex.Lock()
	callback, ok := c.ackCallbacks[ackId]
	delete(c.ackCallbacks, ackId)
	c.mutex.Unlock()

	if !ok {
		c.logger.Infof("No ack callback for id: %d", ackId)
		return
	}

	go callback(event.Payloads)
}

package socketio_v5_client

import socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"

func (c *Client) On(event string, handler interface{}) {
	c.defaultNs.On(event, handler)
}

func (c *Client) OnAny(handler interface{}) {
	c.defaultNs.OnAny(handler)
}

func (n *namespace) On(event string, handler interface{}) {
	n.handlers[event] = append(
		n.handlers[event],
		n.client.parser.WrapCallback(handler),
	)
}

func (n *namespace) OnAny(handler interface{}) {
	n.anyHandlers = append(
		n.anyHandlers,
		n.client.parser.WrapCallback(handler),
	)
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
	case socketio_v5.PacketDisconnect:
		c.handleDisconnect(ns, msg.Payload)
	case socketio_v5.PacketConnect:
		c.handleConnect(ns, msg.Payload)
	case socketio_v5.PacketEvent:
		c.handleEvent(ns, msg.Event)
	case socketio_v5.PacketAck:
		c.handleAck(msg.Event, *msg.AckId)
	case socketio_v5.PacketConnectError:
		c.handleConnectError(ns, msg.Payload)
		c.logger.Errorf("Connect error: %v", *msg.ErrorMessage)
	}
}

func (c *Client) handleConnectError(ns *namespace, payload interface{}) {
	c.logger.Infof("Connect error, namespace: %s", ns.name)

	handlers, ok := ns.handlers["error"]
	if !ok {
		c.logger.Infof("No handlers for event: %s", "error")
		return
	}

	for _, handler := range handlers {
		go handler([]interface{}{payload})
	}
}

func (c *Client) handleDisconnect(ns *namespace, payload interface{}) {
	c.logger.Infof("Disconnected from namespace: %s", ns.name)

	handlers, ok := ns.handlers["disconnect"]
	if !ok {
		c.logger.Infof("No handlers for event: %s", "disconnect")
		return
	}

	for _, handler := range handlers {
		go handler([]interface{}{payload})
	}
}

func (c *Client) handleConnect(ns *namespace, payload interface{}) {
	c.logger.Infof("Connected to namespace: %s", ns.name)
	ns.hadConnected.Do(func() {
		if ns.waitConnected != nil {
			close(ns.waitConnected)
		}
	})

	handlers, ok := ns.handlers["connect"]
	if !ok {
		c.logger.Debugf("No handlers for event: %s", "connect")
		return
	}

	for _, handler := range handlers {
		go handler([]interface{}{payload})
	}
}

func (c *Client) handleEvent(ns *namespace, event *socketio_v5.Event) {

	handlers, ok := ns.handlers[event.Name]
	if !ok && len(ns.anyHandlers) == 0 {
		c.logger.Infof("No handlers for event: %s", event.Name)
		return
	}

	if len(ns.anyHandlers) > 0 {
		params := append([]interface{}{event.Name}, event.Payloads...)
		for _, handler := range ns.anyHandlers {
			go handler(params)
		}
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

package socketio_v5_client

import socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"

func (c *Client) On(event string, handler interface{}) {
	c.defaultNs.On(event, handler)
}

func (c *Client) OnAny(handler func(string, []interface{})) {
	c.defaultNs.OnAny(handler)
}

func (n *namespace) On(event string, handler interface{}) {
	wrapped := n.client.parser.WrapCallback(handler)

	n.mu.Lock()
	n.handlers[event] = append(n.handlers[event], wrapped)
	n.mu.Unlock()
}

func (n *namespace) OnAny(handler func(string, []interface{})) {
	n.mu.Lock()
	n.anyHandlers = append(n.anyHandlers, handler)
	n.mu.Unlock()
}

func (c *Client) onMessage(data []byte) {
	c.logger.Debugf("socketio receive %s", string(data))

	msg, err := c.parser.Parse(data)
	if err != nil {
		c.logger.Errorf("Can't parse message: %v", err)
		return
	}

	c.mutex.RLock()
	ns := c.namespaces[msg.NS]
	c.mutex.RUnlock()

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

	ns.mu.RLock()
	handlers, ok := ns.handlers["error"]
	ns.mu.RUnlock()

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

	ns.mu.RLock()
	handlers, ok := ns.handlers["disconnect"]
	ns.mu.RUnlock()

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

	ns.mu.RLock()
	handlers, ok := ns.handlers["connect"]
	ns.mu.RUnlock()

	if !ok {
		c.logger.Debugf("No handlers for event: %s", "connect")
		return
	}

	for _, handler := range handlers {
		go handler([]interface{}{payload})
	}
}

func (c *Client) handleEvent(ns *namespace, event *socketio_v5.Event) {
	ns.mu.RLock()
	handlers, ok := ns.handlers[event.Name]
	anyHandlers := ns.anyHandlers
	ns.mu.RUnlock()

	if !ok && len(anyHandlers) == 0 {
		c.logger.Infof("No handlers for event: %s", event.Name)
		return
	}

	for _, handler := range anyHandlers {
		go handler(event.Name, event.Payloads)
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

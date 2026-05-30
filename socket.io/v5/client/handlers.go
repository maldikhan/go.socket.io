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
	c.logger.Debugf("socketio receive %s", c.payload(data))

	msg, err := c.parser.Parse(data)
	if err != nil {
		c.logger.Errorf("Can't parse message: %v", err)
		return
	}

	// A binary event/ack header is not complete until its attachment frames
	// arrive. Stage it and wait for onBinaryAttachment to reassemble and
	// dispatch it. A zero-attachment binary packet is reassembled immediately.
	if msg.BinaryAttachments != nil {
		if c.stageBinary(msg) {
			return
		}
	}

	c.dispatch(msg)
}

// stageBinary stores a binary header awaiting attachments. It returns true when
// the message is incomplete (more attachments expected) and the caller must not
// dispatch yet; it returns false once the message is fully reconstructed (e.g.
// zero attachments) so the caller dispatches it normally.
func (c *Client) stageBinary(msg *socketio_v5.Message) bool {
	needed := *msg.BinaryAttachments
	c.binaryMu.Lock()
	if needed == 0 {
		c.clearPendingLocked()
		c.binaryMu.Unlock()
		if err := c.parser.ReconstructBinary(msg, nil); err != nil {
			c.logger.Errorf("Can't reconstruct binary message: %v", err)
			return true
		}
		return false
	}
	c.pendingBinary = msg
	c.pendingNeeded = needed
	c.pendingAttachments = make([][]byte, 0, needed)
	c.binaryMu.Unlock()
	return true
}

// onBinaryAttachment collects a binary attachment frame and, once the staged
// header has received all of its expected attachments, reconstructs and
// dispatches the complete message.
func (c *Client) onBinaryAttachment(data []byte) {
	c.binaryMu.Lock()
	if c.pendingBinary == nil {
		c.binaryMu.Unlock()
		c.logger.Errorf("received binary attachment without a pending binary packet, dropping")
		return
	}
	// Copy the frame: the transport may reuse the underlying buffer.
	buf := make([]byte, len(data))
	copy(buf, data)
	c.pendingAttachments = append(c.pendingAttachments, buf)
	if len(c.pendingAttachments) < c.pendingNeeded {
		c.binaryMu.Unlock()
		return
	}
	msg := c.pendingBinary
	attachments := c.pendingAttachments
	c.clearPendingLocked()
	c.binaryMu.Unlock()

	if err := c.parser.ReconstructBinary(msg, attachments); err != nil {
		c.logger.Errorf("Can't reconstruct binary message: %v", err)
		return
	}
	c.dispatch(msg)
}

// clearPendingLocked resets the staged binary state. The caller must hold
// binaryMu.
func (c *Client) clearPendingLocked() {
	c.pendingBinary = nil
	c.pendingNeeded = 0
	c.pendingAttachments = nil
}

// dispatch routes a fully decoded message (binary or text) to the appropriate
// namespace handlers. Binary events/acks are normalized to their text packet
// type so the existing dispatch paths handle them uniformly.
func (c *Client) dispatch(msg *socketio_v5.Message) {
	switch msg.Type {
	case socketio_v5.PacketBinaryEvent:
		msg.Type = socketio_v5.PacketEvent
	case socketio_v5.PacketBinaryAck:
		msg.Type = socketio_v5.PacketAck
	}

	// Handle ACK packets first — they don't carry a meaningful namespace,
	// so they must not be dropped by the unknown-namespace guard below.
	if msg.Type == socketio_v5.PacketAck {
		if msg.AckId == nil {
			c.logger.Errorf("received ACK packet without ack ID, dropping")
			return
		}
		if msg.Event == nil {
			c.logger.Errorf("received ACK packet without event data, dropping")
			// Clean up the callback to prevent memory leak
			c.mutex.Lock()
			delete(c.ackCallbacks, *msg.AckId)
			c.mutex.Unlock()
			return
		}
		c.handleAck(msg.Event, *msg.AckId)
		return
	}

	// Safely read namespace under RLock to prevent race condition
	c.mutex.RLock()
	ns := c.namespaces[msg.NS]
	c.mutex.RUnlock()

	// Handle nil namespace case
	if ns == nil {
		// For PacketConnect, auto-create the namespace
		if msg.Type == socketio_v5.PacketConnect {
			ns = c.namespace(msg.NS)
		} else {
			// For other packet types, log warning and return
			c.logger.Warnf("Received %v for unknown namespace: %s", msg.Type, msg.NS)
			return
		}
	}

	switch msg.Type {
	case socketio_v5.PacketDisconnect:
		c.handleDisconnect(ns, msg.Payload)
	case socketio_v5.PacketConnect:
		c.handleConnect(ns, msg.Payload)
	case socketio_v5.PacketEvent:
		c.handleEvent(ns, msg.Event)
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
		h := handler
		c.safeGo(func() { h([]interface{}{payload}) })
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
		h := handler
		c.safeGo(func() { h([]interface{}{payload}) })
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
		h := handler
		c.safeGo(func() { h([]interface{}{payload}) })
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
		h := handler
		c.safeGo(func() { h(event.Name, event.Payloads) })
	}

	for _, handler := range handlers {
		h := handler
		c.safeGo(func() { h(event.Payloads) })
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

	c.safeGo(func() { callback(event.Payloads) })
}

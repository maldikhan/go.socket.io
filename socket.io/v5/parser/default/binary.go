package socketio_v5_parser_default

import (
	"encoding/json"
	"errors"
	"fmt"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// ErrParseBinary is returned when binary attachments cannot be reconciled with
// the placeholders found in a PacketBinaryEvent/PacketBinaryAck payload.
var ErrParseBinary = errors.New("parse binary error")

// ReconstructBinary replaces every {"_placeholder":true,"num":N} marker in the
// message's event payloads with attachments[N], exposing the binary data as
// []byte. It must be called once all expected attachments (msg.BinaryAttachments)
// have been collected. On success BinaryAttachments is cleared so the message is
// indistinguishable from a fully decoded event.
func (p *SocketIOV5DefaultParser) ReconstructBinary(msg *socketio_v5.Message, attachments [][]byte) error {
	if msg == nil {
		return fmt.Errorf("%w: %v", ErrParseBinary, errors.New("empty package"))
	}
	expected := 0
	if msg.BinaryAttachments != nil {
		expected = *msg.BinaryAttachments
	}
	if len(attachments) != expected {
		return fmt.Errorf("%w: expected %d attachments, got %d", ErrParseBinary, expected, len(attachments))
	}
	if msg.Event != nil {
		for i, payload := range msg.Event.Payloads {
			raw, ok := payload.(json.RawMessage)
			if !ok {
				// Already-decoded payloads (e.g. a custom payload parser) are left
				// untouched; only raw JSON can contain placeholders.
				continue
			}
			var decoded interface{}
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return fmt.Errorf("%w: %v", ErrParseBinary, err)
			}
			replaced, err := replacePlaceholders(decoded, attachments)
			if err != nil {
				return err
			}
			msg.Event.Payloads[i] = replaced
		}
	}
	msg.BinaryAttachments = nil
	return nil
}

// replacePlaceholders walks a decoded JSON value and substitutes any placeholder
// object with the matching binary attachment ([]byte). Nested arrays and objects
// are traversed so attachments inside structures are reconstructed too.
func replacePlaceholders(value interface{}, attachments [][]byte) (interface{}, error) {
	switch v := value.(type) {
	case map[string]interface{}:
		if buf, ok, err := asPlaceholder(v, attachments); err != nil {
			return nil, err
		} else if ok {
			return buf, nil
		}
		for key, item := range v {
			replaced, err := replacePlaceholders(item, attachments)
			if err != nil {
				return nil, err
			}
			v[key] = replaced
		}
		return v, nil
	case []interface{}:
		for i, item := range v {
			replaced, err := replacePlaceholders(item, attachments)
			if err != nil {
				return nil, err
			}
			v[i] = replaced
		}
		return v, nil
	default:
		return value, nil
	}
}

// asPlaceholder reports whether m is a binary placeholder and, if so, returns
// the attachment it points at. It returns ok == false for ordinary objects.
func asPlaceholder(m map[string]interface{}, attachments [][]byte) ([]byte, bool, error) {
	flag, hasFlag := m["_placeholder"]
	if !hasFlag {
		return nil, false, nil
	}
	isPlaceholder, _ := flag.(bool)
	if !isPlaceholder {
		return nil, false, nil
	}
	numValue, hasNum := m["num"]
	if !hasNum {
		return nil, false, fmt.Errorf("%w: %v", ErrParseBinary, errors.New("placeholder without num"))
	}
	num, ok := numValue.(float64)
	if !ok {
		return nil, false, fmt.Errorf("%w: %v", ErrParseBinary, errors.New("placeholder num is not a number"))
	}
	idx := int(num)
	if idx < 0 || idx >= len(attachments) {
		return nil, false, fmt.Errorf("%w: placeholder index %d out of range", ErrParseBinary, idx)
	}
	return attachments[idx], true, nil
}

// HasBinary reports whether the event carries any []byte payload that requires
// a PacketBinaryEvent/PacketBinaryAck encoding. Callers use it to decide between
// Serialize (text) and SerializeBinary.
func (p *SocketIOV5DefaultParser) HasBinary(event *socketio_v5.Event) bool {
	if event == nil {
		return false
	}
	for _, payload := range event.Payloads {
		if valueHasBinary(payload) {
			return true
		}
	}
	return false
}

// valueHasBinary reports whether a payload value contains any []byte, searching
// nested slices and maps so binary buffers placed inside structures are found.
func valueHasBinary(value interface{}) bool {
	switch v := value.(type) {
	case []byte:
		return true
	case map[string]interface{}:
		for _, item := range v {
			if valueHasBinary(item) {
				return true
			}
		}
	case []interface{}:
		for _, item := range v {
			if valueHasBinary(item) {
				return true
			}
		}
	}
	return false
}

// extractBinary walks a payload value and replaces every []byte with a
// {"_placeholder":true,"num":N} marker, appending the buffer to *attachments in
// the order encountered. The returned value is safe to JSON-marshal as the
// binary event header. Nested slices and maps are traversed.
func extractBinary(value interface{}, attachments *[][]byte) interface{} {
	switch v := value.(type) {
	case []byte:
		num := len(*attachments)
		*attachments = append(*attachments, v)
		return map[string]interface{}{"_placeholder": true, "num": num}
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, item := range v {
			out[key] = extractBinary(item, attachments)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = extractBinary(item, attachments)
		}
		return out
	default:
		return value
	}
}

// SerializeBinary encodes a message whose event payloads contain binary data as
// a PacketBinaryEvent/PacketBinaryAck. It returns the header packet (with the
// attachment count and {"_placeholder"} markers) and the ordered list of binary
// attachments to send as separate frames after the header.
func (p *SocketIOV5DefaultParser) SerializeBinary(msg *socketio_v5.Message) ([]byte, [][]byte, error) {
	if msg == nil || msg.Event == nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrParseBinary, errors.New("empty event"))
	}

	// Map the requested text type onto its binary counterpart so callers may
	// pass either PacketEvent/PacketAck or the binary variants.
	var binaryType socketio_v5.SocketIOPacket
	switch msg.Type {
	case socketio_v5.PacketEvent, socketio_v5.PacketBinaryEvent:
		binaryType = socketio_v5.PacketBinaryEvent
	case socketio_v5.PacketAck, socketio_v5.PacketBinaryAck:
		binaryType = socketio_v5.PacketBinaryAck
	default:
		return nil, nil, fmt.Errorf("%w: %v", ErrParseBinary, errors.New("not an event or ack"))
	}

	var attachments [][]byte
	placeholders := make([]interface{}, len(msg.Event.Payloads))
	for i, payload := range msg.Event.Payloads {
		placeholders[i] = extractBinary(payload, &attachments)
	}

	count := len(attachments)
	headerMsg := &socketio_v5.Message{
		Type:              binaryType,
		BinaryAttachments: &count,
		NS:                msg.NS,
		AckId:             msg.AckId,
		Event: &socketio_v5.Event{
			Name:     msg.Event.Name,
			Payloads: placeholders,
		},
	}

	header, err := p.serializeHeader(headerMsg)
	if err != nil {
		return nil, nil, err
	}
	return header, attachments, nil
}

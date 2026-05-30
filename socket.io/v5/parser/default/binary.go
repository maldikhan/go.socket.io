package socketio_v5_parser_default

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// maxBinaryScanDepth bounds the recursion of valueHasBinary so a pathological or
// cyclic structure (e.g. a struct that points back at itself through a pointer or
// interface) can never make HasBinary hang or overflow the stack. Real payloads
// are far shallower than this, so the bound never trips for legitimate data.
const maxBinaryScanDepth = 100

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
// nested slices, maps AND struct fields (including through pointers and
// interfaces) so binary buffers placed anywhere reachable inside a Go value are
// found. This is what lets Emit("upload", struct{ File []byte }{...}) be encoded
// as a real binary packet instead of base64-stringifying the bytes.
func valueHasBinary(value interface{}) bool {
	// Fast paths for the common, already-decoded shapes avoid the reflect cost.
	switch v := value.(type) {
	case nil:
		return false
	case []byte:
		return true
	case map[string]interface{}:
		for _, item := range v {
			if valueHasBinary(item) {
				return true
			}
		}
		return false
	case []interface{}:
		for _, item := range v {
			if valueHasBinary(item) {
				return true
			}
		}
		return false
	}
	return reflectHasBinary(reflect.ValueOf(value), maxBinaryScanDepth)
}

// reflectHasBinary walks an arbitrary reflect.Value looking for a []byte leaf.
// depth bounds the recursion to guard against cyclic structures reached via
// pointers/interfaces. A []byte is the binary leaf (Slice of Uint8); any other
// slice/array is recursed into element by element.
func reflectHasBinary(rv reflect.Value, depth int) bool {
	if depth <= 0 || !rv.IsValid() {
		return false
	}

	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return false
		}
		return reflectHasBinary(rv.Elem(), depth-1)
	case reflect.Slice:
		// []byte is the binary leaf; anything else is a slice to descend into.
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return true
		}
		for i := 0; i < rv.Len(); i++ {
			if reflectHasBinary(rv.Index(i), depth-1) {
				return true
			}
		}
		return false
	case reflect.Array:
		// A [N]byte array is not a Socket.IO binary attachment (only []byte is),
		// so arrays are always descended into element by element.
		for i := 0; i < rv.Len(); i++ {
			if reflectHasBinary(rv.Index(i), depth-1) {
				return true
			}
		}
		return false
	case reflect.Map:
		for _, key := range rv.MapKeys() {
			if reflectHasBinary(rv.MapIndex(key), depth-1) {
				return true
			}
		}
		return false
	case reflect.Struct:
		t := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			// Only exported fields are readable by reflect and marshalable by
			// encoding/json; unexported fields can never become attachments.
			if t.Field(i).PkgPath != "" {
				continue
			}
			if reflectHasBinary(rv.Field(i), depth-1) {
				return true
			}
		}
		return false
	}
	return false
}

// extractBinary walks a payload value and replaces every []byte with a
// {"_placeholder":true,"num":N} marker, appending the buffer to *attachments in
// the order encountered. The returned value is safe to JSON-marshal as the
// binary event header. Nested slices and maps are traversed; arbitrary structs,
// pointers and typed containers are handled via reflection so binary inside a
// Go struct (e.g. struct{ File []byte }) becomes a real attachment rather than
// a base64 string.
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
	case nil:
		return nil
	default:
		return reflectExtractBinary(reflect.ValueOf(value), attachments, maxBinaryScanDepth)
	}
}

// reflectExtractBinary mirrors extractBinary for arbitrary Go values reached via
// reflection. It returns a JSON-marshalable tree (maps/slices/scalars) with every
// []byte replaced by a placeholder and appended to *attachments. Values that
// cannot contain binary (or once the depth bound is hit) are returned via their
// interface{} so encoding/json serializes them normally. Only exported struct
// fields are emitted, matching encoding/json and valueHasBinary.
func reflectExtractBinary(rv reflect.Value, attachments *[][]byte, depth int) interface{} {
	if !rv.IsValid() {
		return nil
	}
	if depth <= 0 {
		return rv.Interface()
	}

	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return rv.Interface()
		}
		return reflectExtractBinary(rv.Elem(), attachments, depth-1)
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			// []byte leaf -> attachment + placeholder.
			buf := rv.Bytes()
			num := len(*attachments)
			*attachments = append(*attachments, buf)
			return map[string]interface{}{"_placeholder": true, "num": num}
		}
		out := make([]interface{}, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = reflectExtractBinary(rv.Index(i), attachments, depth-1)
		}
		return out
	case reflect.Array:
		out := make([]interface{}, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = reflectExtractBinary(rv.Index(i), attachments, depth-1)
		}
		return out
	case reflect.Map:
		out := make(map[string]interface{}, rv.Len())
		for _, key := range rv.MapKeys() {
			out[mapKeyString(key)] = reflectExtractBinary(rv.MapIndex(key), attachments, depth-1)
		}
		return out
	case reflect.Struct:
		t := rv.Type()
		out := make(map[string]interface{}, rv.NumField())
		for i := 0; i < rv.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue // unexported
			}
			name, omit := jsonFieldName(field)
			if omit {
				continue
			}
			out[name] = reflectExtractBinary(rv.Field(i), attachments, depth-1)
		}
		return out
	default:
		return rv.Interface()
	}
}

// mapKeyString renders a map key as a JSON object key. encoding/json requires
// map keys to be strings (or types implementing TextMarshaler / integer kinds);
// fmt.Sprint reproduces the string and integer renderings json uses, which is
// all that is needed to carry binary placeholders out of a typed map.
func mapKeyString(key reflect.Value) string {
	return fmt.Sprint(key.Interface())
}

// jsonFieldName returns the JSON object key for a struct field, honoring a
// `json:"name,options"` tag. A `json:"-"` tag omits the field (the obscure
// `json:"-,"` literal-dash form is not supported; it is vanishingly rare and not
// needed for binary payloads). An empty tag name falls back to the field name.
func jsonFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", true
	}
	name := tag
	if comma := indexComma(tag); comma >= 0 {
		name = tag[:comma]
	}
	if name == "" {
		return field.Name, false
	}
	return name, false
}

// indexComma returns the index of the first comma in s, or -1. (Avoids pulling
// in strings just for this; keeps the helper trivially Go 1.18 compatible.)
func indexComma(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			return i
		}
	}
	return -1
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

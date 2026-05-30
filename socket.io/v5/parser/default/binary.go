package socketio_v5_parser_default

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// binaryWalkDepth bounds the reflection traversal used by HasBinary and the
// binary extractor. It guards against cyclic data structures (e.g. a struct or
// map that points back at itself) so neither walk can hang.
const binaryWalkDepth = 64

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

// valueHasBinary reports whether a payload value contains any []byte. It walks
// nested structures with reflection so a buffer reached through a struct field,
// a pointer, a typed map, or a named/typed slice or array is still detected.
// []byte is treated as a leaf (it never recurses into the bytes themselves), and
// a []string (or any non-byte slice) does not misfire. The walk is depth-bounded
// to stay safe against cyclic structures.
func valueHasBinary(value interface{}) bool {
	if value == nil {
		return false
	}
	return reflectHasBinary(reflect.ValueOf(value), binaryWalkDepth)
}

// reflectHasBinary is the reflection core of valueHasBinary.
func reflectHasBinary(rv reflect.Value, depth int) bool {
	if depth <= 0 || !rv.IsValid() {
		return false
	}
	switch rv.Kind() {
	case reflect.Interface, reflect.Ptr:
		if rv.IsNil() {
			return false
		}
		return reflectHasBinary(rv.Elem(), depth-1)
	case reflect.Slice, reflect.Array:
		// []byte is the binary leaf; never recurse into its elements.
		if isByteSlice(rv) {
			return true
		}
		for i := 0; i < rv.Len(); i++ {
			if reflectHasBinary(rv.Index(i), depth-1) {
				return true
			}
		}
	case reflect.Map:
		iter := rv.MapRange()
		for iter.Next() {
			if reflectHasBinary(iter.Value(), depth-1) {
				return true
			}
		}
	case reflect.Struct:
		t := rv.Type()
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				// Unexported field: skip, mirroring encoding/json.
				continue
			}
			if jsonFieldName(field) == "" {
				// Field tagged json:"-": not serialized, so not a carrier.
				continue
			}
			if reflectHasBinary(rv.Field(i), depth-1) {
				return true
			}
		}
	}
	return false
}

// isByteSlice reports whether rv is a slice (or array) whose element kind is
// uint8, i.e. a []byte / [N]byte that must be treated as a binary leaf.
func isByteSlice(rv reflect.Value) bool {
	return (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) &&
		rv.Type().Elem().Kind() == reflect.Uint8
}

// jsonFieldName returns the JSON object key encoding/json would use for the
// struct field, honoring a `json:"name"` tag. It returns "" when the field is
// tagged json:"-" (and thus skipped). An empty tag name falls back to the Go
// field name, matching encoding/json.
func jsonFieldName(field reflect.StructField) string {
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return field.Name
	}
	name := tag
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		name = tag[:comma]
	}
	if name == "-" {
		// `json:"-"` skips the field, but `json:"-,"` names it "-".
		if tag == "-" {
			return ""
		}
		return "-"
	}
	if name == "" {
		return field.Name
	}
	return name
}

// jsonFieldOmitEmpty reports whether the field carries the json omitempty option.
func jsonFieldOmitEmpty(field reflect.StructField) bool {
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return false
	}
	for _, opt := range strings.Split(tag, ",")[1:] {
		if opt == "omitempty" {
			return true
		}
	}
	return false
}

// isEmptyValue mirrors encoding/json's notion of an empty value for omitempty.
func isEmptyValue(rv reflect.Value) bool {
	switch rv.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len() == 0
	case reflect.Bool:
		return !rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return rv.IsNil()
	}
	return false
}

// extractBinary walks a payload value and replaces every []byte with a
// {"_placeholder":true,"num":N} marker, appending the buffer to *attachments in
// the order encountered. The returned value is safe to JSON-marshal as the
// binary event header and produces JSON identical to what encoding/json would
// emit for the non-byte parts (json:"name", json:"-" and omitempty are honored).
// Structs, pointers, typed maps and named/typed slices or arrays are traversed
// with reflection so a []byte at any reachable position becomes an attachment.
func extractBinary(value interface{}, attachments *[][]byte) interface{} {
	if value == nil {
		return nil
	}
	return reflectExtractBinary(reflect.ValueOf(value), attachments, binaryWalkDepth)
}

// placeholder returns a marker object for the next attachment and records buf.
func placeholder(buf []byte, attachments *[][]byte) map[string]interface{} {
	num := len(*attachments)
	*attachments = append(*attachments, buf)
	return map[string]interface{}{"_placeholder": true, "num": num}
}

// reflectExtractBinary is the reflection core of extractBinary. When a subtree
// contains no binary it is returned as-is (interface value) so encoding/json
// serializes it exactly as it normally would; only branches that carry binary
// are rebuilt into map/slice forms with placeholders substituted.
func reflectExtractBinary(rv reflect.Value, attachments *[][]byte, depth int) interface{} {
	if !rv.IsValid() {
		return nil
	}
	// If this subtree has no binary at all, hand the original value back so
	// json.Marshal renders it verbatim (preserving Marshaler impls, tags, etc.).
	if depth <= 0 || !reflectHasBinary(rv, depth) {
		if rv.CanInterface() {
			return rv.Interface()
		}
		return nil
	}

	switch rv.Kind() {
	case reflect.Interface, reflect.Ptr:
		if rv.IsNil() {
			return nil
		}
		return reflectExtractBinary(rv.Elem(), attachments, depth-1)
	case reflect.Slice, reflect.Array:
		if isByteSlice(rv) {
			return placeholder(toByteSlice(rv), attachments)
		}
		out := make([]interface{}, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = reflectExtractBinary(rv.Index(i), attachments, depth-1)
		}
		return out
	case reflect.Map:
		out := make(map[string]interface{}, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[mapKeyString(iter.Key())] = reflectExtractBinary(iter.Value(), attachments, depth-1)
		}
		return out
	case reflect.Struct:
		out := make(map[string]interface{})
		t := rv.Type()
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name := jsonFieldName(field)
			if name == "" {
				continue
			}
			fv := rv.Field(i)
			if jsonFieldOmitEmpty(field) && isEmptyValue(fv) {
				continue
			}
			out[name] = reflectExtractBinary(fv, attachments, depth-1)
		}
		return out
	default:
		if rv.CanInterface() {
			return rv.Interface()
		}
		return nil
	}
}

// toByteSlice returns a []byte for a byte slice or byte array value.
func toByteSlice(rv reflect.Value) []byte {
	if rv.Kind() == reflect.Slice {
		return rv.Bytes()
	}
	// Byte array: copy into a slice (arrays are not addressable via Bytes here).
	buf := make([]byte, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		buf[i] = byte(rv.Index(i).Uint())
	}
	return buf
}

// mapKeyString renders a map key the way encoding/json would for object keys
// (string keys verbatim, integer keys formatted as their decimal string).
func mapKeyString(k reflect.Value) string {
	switch k.Kind() {
	case reflect.String:
		return k.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", k.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return fmt.Sprintf("%d", k.Uint())
	default:
		return fmt.Sprintf("%v", k.Interface())
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

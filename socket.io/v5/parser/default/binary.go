package socketio_v5_parser_default

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// maxBinaryWalkDepth bounds the reflective traversal performed by HasBinary and
// the binary extractor so a cyclic structure (e.g. a struct that points back to
// itself) can never cause an unbounded recursion / hang.
const maxBinaryWalkDepth = 256

// byteSliceType is the reflect.Type of []byte, the binary "leaf".
var byteSliceType = reflect.TypeOf([]byte(nil))

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
// nested slices, maps, structs and pointers so binary buffers placed inside
// arbitrary Go structures are found. The traversal mirrors extractBinary so the
// send decision and the actual extraction always agree.
func valueHasBinary(value interface{}) bool {
	if value == nil {
		return false
	}
	return reflectHasBinary(reflect.ValueOf(value), 0)
}

// reflectHasBinary walks an arbitrary reflect.Value looking for a []byte leaf.
// It treats []byte itself as the binary leaf (never recursing into it as a slice
// of bytes) and bounds recursion via depth to stay cycle-safe. Only exported
// struct fields and fields not tagged json:"-" are considered, matching what
// encoding/json (and therefore the extractor) can actually see.
func reflectHasBinary(v reflect.Value, depth int) bool {
	if depth > maxBinaryWalkDepth {
		return false
	}
	if !v.IsValid() {
		return false
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return false
		}
		return reflectHasBinary(v.Elem(), depth+1)
	case reflect.Ptr:
		if v.IsNil() {
			return false
		}
		return reflectHasBinary(v.Elem(), depth+1)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			// []byte (or a named type with []byte underlying) is the leaf.
			return true
		}
		for i := 0; i < v.Len(); i++ {
			if reflectHasBinary(v.Index(i), depth+1) {
				return true
			}
		}
	case reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			// A byte array is JSON-encoded as a number list, not as binary; it
			// is not a binary attachment leaf, so do not recurse element-wise.
			return false
		}
		for i := 0; i < v.Len(); i++ {
			if reflectHasBinary(v.Index(i), depth+1) {
				return true
			}
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			if reflectHasBinary(v.MapIndex(key), depth+1) {
				return true
			}
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" { // unexported
				continue
			}
			if jsonFieldName(field) == "-" {
				continue
			}
			if reflectHasBinary(v.Field(i), depth+1) {
				return true
			}
		}
	}
	return false
}

// jsonFieldName returns the JSON object key encoding/json would use for the
// struct field, honoring the `json:"name"` tag. It returns "-" when the field is
// explicitly skipped (json:"-").
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
		// json:"-" skips, but json:"-," names the field "-".
		if tag == "-" {
			return "-"
		}
		return field.Name
	}
	if name == "" {
		return field.Name
	}
	return name
}

// extractBinary walks a payload value and replaces every []byte with a
// {"_placeholder":true,"num":N} marker, appending the buffer to *attachments in
// the order encountered. The returned value is safe to JSON-marshal as the
// binary event header. Slices, maps, structs and pointers are traversed via
// reflection so that the produced header JSON matches what encoding/json would
// emit for non-binary fields (json tags / omitempty honored) while every []byte
// becomes an ordered attachment + placeholder.
//
// extractBinary mirrors reflectHasBinary exactly: a value flagged as containing
// binary by HasBinary is guaranteed to have its []byte leaves extracted here, so
// the attachment count in the header always matches the frames that follow.
func extractBinary(value interface{}, attachments *[][]byte) interface{} {
	if value == nil {
		return nil
	}
	// Fast paths for the already-generic JSON types preserve the prior behavior
	// (and the prior map iteration semantics) exactly.
	switch v := value.(type) {
	case []byte:
		return appendAttachment(v, attachments)
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
	}
	return reflectExtractBinary(reflect.ValueOf(value), attachments, 0)
}

// appendAttachment records buf as the next ordered attachment and returns the
// placeholder marker that takes its place in the header JSON.
func appendAttachment(buf []byte, attachments *[][]byte) interface{} {
	num := len(*attachments)
	*attachments = append(*attachments, buf)
	return map[string]interface{}{"_placeholder": true, "num": num}
}

// reflectExtractBinary canonicalizes an arbitrary Go value into the generic
// JSON shape (maps/slices/scalars) encoding/json would produce, replacing every
// []byte leaf with a placeholder marker. It honors json struct tags, json:"-"
// and the omitempty option, and is bounded by depth to stay cycle-safe. Values
// without binary content fall through unchanged so non-binary fields keep their
// concrete type and marshal normally.
func reflectExtractBinary(v reflect.Value, attachments *[][]byte, depth int) interface{} {
	if depth > maxBinaryWalkDepth || !v.IsValid() {
		if v.IsValid() && v.CanInterface() {
			return v.Interface()
		}
		return nil
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return reflectExtractBinary(v.Elem(), attachments, depth+1)
	case reflect.Ptr:
		if v.IsNil() {
			return nil
		}
		return reflectExtractBinary(v.Elem(), attachments, depth+1)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			// []byte leaf -> attachment.
			return appendAttachment(toByteSlice(v), attachments)
		}
		out := make([]interface{}, v.Len())
		for i := 0; i < v.Len(); i++ {
			out[i] = reflectExtractBinary(v.Index(i), attachments, depth+1)
		}
		return out
	case reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			// Byte arrays are not binary attachments; let encoding/json handle
			// them (it encodes them as a number list).
			return v.Interface()
		}
		out := make([]interface{}, v.Len())
		for i := 0; i < v.Len(); i++ {
			out[i] = reflectExtractBinary(v.Index(i), attachments, depth+1)
		}
		return out
	case reflect.Map:
		out := make(map[string]interface{}, v.Len())
		for _, key := range v.MapKeys() {
			out[mapKeyString(key)] = reflectExtractBinary(v.MapIndex(key), attachments, depth+1)
		}
		return out
	case reflect.Struct:
		out := make(map[string]interface{})
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" { // unexported
				continue
			}
			name := jsonFieldName(field)
			if name == "-" {
				continue
			}
			fv := v.Field(i)
			if jsonOmitEmpty(field) && isEmptyValue(fv) {
				continue
			}
			out[name] = reflectExtractBinary(fv, attachments, depth+1)
		}
		return out
	default:
		if v.CanInterface() {
			return v.Interface()
		}
		return nil
	}
}

// toByteSlice returns the []byte underlying v, which has a []byte-kinded type
// (possibly a named type). Bytes() works for addressable/byte-element slices.
func toByteSlice(v reflect.Value) []byte {
	if v.Type() == byteSliceType {
		return v.Bytes()
	}
	// Named slice type with []byte underlying: convert to []byte.
	return v.Convert(byteSliceType).Bytes()
}

// mapKeyString renders a map key the way encoding/json would for a JSON object
// key: string keys verbatim, integer keys as their base-10 text.
func mapKeyString(key reflect.Value) string {
	switch key.Kind() {
	case reflect.String:
		return key.String()
	default:
		// Fall back to fmt for integer / encoding.TextMarshaler-ish keys; this
		// keeps numeric map keys (the common non-string case) JSON-correct.
		return fmt.Sprintf("%v", key.Interface())
	}
}

// jsonOmitEmpty reports whether the field carries the json omitempty option.
func jsonOmitEmpty(field reflect.StructField) bool {
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return false
	}
	parts := strings.Split(tag, ",")
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			return true
		}
	}
	return false
}

// isEmptyValue mirrors encoding/json's notion of an empty value for omitempty.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	}
	return false
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

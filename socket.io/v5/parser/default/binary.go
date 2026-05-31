package socketio_v5_parser_default

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
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

var (
	// byteSliceType is the unnamed []byte type, used to tell a plain []byte from a
	// named alias (e.g. type Blob []byte) so the latter's type is preserved.
	byteSliceType = reflect.TypeOf([]byte(nil))
	// jsonMarshalerType / textMarshalerType are the marshaler interfaces. A value
	// whose type implements either owns its own encoding (e.g. time.Time ->
	// RFC3339, json.RawMessage -> verbatim), so its bytes are emitted by that
	// marshaler and are never lifted into Socket.IO binary attachments.
	jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*interface{ MarshalText() ([]byte, error) })(nil)).Elem()
)

// sentinelMagic prefixes every binary sentinel slice. Each sentinel is also given
// a per-serialization random nonce and the attachment index, so its base64 form is
// a unique needle for splicePlaceholders that an application payload cannot
// predict: a user string can therefore never be mistaken for a sentinel (and so
// never rewritten into a placeholder) except with cryptographically negligible
// probability. The leading control bytes keep it visually distinct in logs.
var sentinelMagic = []byte{0x1b, 'S', 'I', 'O', 'B', 'I', 'N', 0x1b}

// sentinelNonceLen is the number of random bytes mixed into every sentinel of a
// single serialization, making the sentinels' base64 needles unguessable.
const sentinelNonceLen = 16

// randRead is the source of sentinel-nonce randomness, indirected through a
// package var only so tests can force the (otherwise unreachable) rand failure.
var randRead = rand.Read

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

	// A type that controls its own JSON/text encoding owns its representation;
	// its bytes go out via that marshaler (e.g. time.Time, json.RawMessage), never
	// as a binary attachment. For Interface kind rv.Type() is the interface itself
	// (no marshaler) and the concrete type is re-checked after the deref below.
	if implementsMarshaler(rv.Type()) {
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
		// (json.RawMessage and other marshaler-owned byte slices were already
		// excluded by the implementsMarshaler check above.)
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

// implementsMarshaler reports whether t controls its own JSON or text encoding via
// json.Marshaler / encoding.TextMarshaler, on EITHER a value or a pointer receiver.
// The pointer-receiver case matters because encoding/json invokes a pointer
// marshaler for the addressable values it builds (slice/array elements, addressable
// struct fields). If we descended into such a value and swapped its []byte for a
// sentinel, the marshaler would encode the sentinel bytes itself (e.g. as hex) and
// never emit the base64 needle splicePlaceholders looks for — corrupting the output
// and leaving an orphan attachment. Treating these types as owning their encoding
// keeps the binary path byte-for-byte consistent with the text path for them.
func implementsMarshaler(t reflect.Type) bool {
	if t.Implements(jsonMarshalerType) || t.Implements(textMarshalerType) {
		return true
	}
	pt := reflect.PtrTo(t)
	return pt.Implements(jsonMarshalerType) || pt.Implements(textMarshalerType)
}

// extractBinary returns a JSON-marshalable representation of one event payload in
// which every []byte has been lifted into *attachments and replaced by a
// {"_placeholder":true,"num":N} marker. Payloads with no binary are returned
// unchanged so encoding/json renders them verbatim.
//
// For payloads that DO contain binary, the encoding is delegated to
// encoding/json: a same-typed copy is built in which each []byte is swapped for a
// unique sentinel, the copy is marshaled (so json tags, omitempty, ,string,
// embedded fields and json.Marshaler implementations are honored exactly as on
// the text Serialize path), and the sentinels' base64 renderings are spliced back
// into placeholder objects. The header is therefore byte-for-byte identical to
// the non-binary path apart from the lifted buffers.
func extractBinary(value interface{}, attachments *[][]byte) (interface{}, error) {
	if !valueHasBinary(value) {
		return value, nil
	}
	nonce := make([]byte, sentinelNonceLen)
	if _, err := randRead(nonce); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseBinary, err)
	}
	sentinels := make(map[string]int)
	transformed := transformBinary(reflect.ValueOf(value), attachments, sentinels, nonce, maxBinaryScanDepth)
	data, err := json.Marshal(transformed.Interface())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseBinary, err)
	}
	return json.RawMessage(splicePlaceholders(data, sentinels)), nil
}

// transformBinary returns a value of the SAME type as rv in which every []byte on
// a binary-bearing path is replaced by a unique sentinel slice; each sentinel's
// base64 rendering is recorded in sentinels (keyed to its attachment index) and
// the original bytes are appended to *attachments. Subtrees with no binary (or
// whose type owns its encoding, or once the depth bound is hit) are returned
// unchanged and shared with the original — they are never mutated, because only
// freshly built containers are ever written to.
func transformBinary(rv reflect.Value, attachments *[][]byte, sentinels map[string]int, nonce []byte, depth int) reflect.Value {
	if depth <= 0 || !rv.IsValid() || !reflectHasBinary(rv, depth) {
		return rv
	}

	switch rv.Kind() {
	case reflect.Ptr:
		np := reflect.New(rv.Type().Elem())
		np.Elem().Set(transformBinary(rv.Elem(), attachments, sentinels, nonce, depth-1))
		return np
	case reflect.Interface:
		out := reflect.New(rv.Type()).Elem()
		out.Set(transformBinary(rv.Elem(), attachments, sentinels, nonce, depth-1))
		return out
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return sentinelSlice(rv, attachments, sentinels, nonce)
		}
		ns := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			ns.Index(i).Set(transformBinary(rv.Index(i), attachments, sentinels, nonce, depth-1))
		}
		return ns
	case reflect.Array:
		na := reflect.New(rv.Type()).Elem()
		for i := 0; i < rv.Len(); i++ {
			na.Index(i).Set(transformBinary(rv.Index(i), attachments, sentinels, nonce, depth-1))
		}
		return na
	case reflect.Map:
		nm := reflect.MakeMap(rv.Type())
		for _, key := range rv.MapKeys() {
			nm.SetMapIndex(key, transformBinary(rv.MapIndex(key), attachments, sentinels, nonce, depth-1))
		}
		return nm
	}

	// Remaining binary-bearing kind: struct. A same-typed copy is built so json
	// applies the real field tags during marshaling; only exported fields are
	// copied (matching json and valueHasBinary), `json:"-"` fields are dropped,
	// and an omitempty field that json would omit is left zero so it stays omitted
	// rather than being forced in by a (non-empty) sentinel.
	ns := reflect.New(rv.Type()).Elem()
	t := rv.Type()
	for i := 0; i < rv.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue // unexported
		}
		skip, omitempty := jsonFieldOptions(field)
		if skip {
			continue
		}
		fv := rv.Field(i)
		if omitempty && isEmptyValue(fv) {
			continue
		}
		ns.Field(i).Set(transformBinary(fv, attachments, sentinels, nonce, depth-1))
	}
	return ns
}

// sentinelSlice records rv's bytes as the next attachment and returns a same-typed
// slice holding a unique sentinel. encoding/json renders the sentinel as a base64
// string, which splicePlaceholders then rewrites into the placeholder object.
func sentinelSlice(rv reflect.Value, attachments *[][]byte, sentinels map[string]int, nonce []byte) reflect.Value {
	orig := rv.Bytes()
	buf := make([]byte, len(orig))
	copy(buf, orig)
	num := len(*attachments)
	*attachments = append(*attachments, buf)

	// num is a slice length, hence always non-negative; the conversion to the
	// fixed-width index is safe (no overflow/sign change).
	sentinel := makeSentinel(nonce, uint64(num))
	sentinels[base64.StdEncoding.EncodeToString(sentinel)] = num

	sv := reflect.ValueOf(sentinel)
	if rv.Type() != byteSliceType {
		// Preserve a named []byte type (e.g. type Blob []byte) so the copy stays
		// assignable to its field/element.
		sv = sv.Convert(rv.Type())
	}
	return sv
}

// makeSentinel builds the unique, marshaling-stable byte token for attachment num:
// the fixed magic, this serialization's random nonce, and the index. The nonce
// makes the token (and thus its base64 needle) unpredictable to an application
// payload; the index keeps tokens distinct within one serialization (see
// sentinelMagic).
func makeSentinel(nonce []byte, num uint64) []byte {
	s := make([]byte, 0, len(sentinelMagic)+len(nonce)+8)
	s = append(s, sentinelMagic...)
	s = append(s, nonce...)
	var idx [8]byte
	binary.BigEndian.PutUint64(idx[:], num)
	return append(s, idx[:]...)
}

// splicePlaceholders rewrites each sentinel's base64 JSON string ("<b64>") into a
// {"_placeholder":true,"num":N} object. Each sentinel is unique, so it appears
// exactly once.
func splicePlaceholders(data []byte, sentinels map[string]int) []byte {
	for b64, num := range sentinels {
		needle := []byte(`"` + b64 + `"`)
		repl := []byte(fmt.Sprintf(`{"_placeholder":true,"num":%d}`, num))
		data = bytes.Replace(data, needle, repl, 1)
	}
	return data
}

// isEmptyValue mirrors encoding/json's emptiness test for omitempty so the binary
// path omits exactly the fields the text path would.
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

// jsonFieldOptions reports whether a struct field is dropped by `json:"-"` and
// whether it carries the omitempty option. The field name is not needed:
// transformBinary copies into the same struct type, so encoding/json applies the
// real tag (name, ,string, etc.) when it marshals.
func jsonFieldOptions(field reflect.StructField) (skip, omitempty bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return true, false
	}
	if comma := indexComma(tag); comma >= 0 {
		omitempty = hasOmitempty(tag[comma+1:])
	}
	return false, omitempty
}

// hasOmitempty reports whether the comma-separated json tag options contain
// "omitempty".
func hasOmitempty(opts string) bool {
	for opts != "" {
		var part string
		if i := indexComma(opts); i >= 0 {
			part, opts = opts[:i], opts[i+1:]
		} else {
			part, opts = opts, ""
		}
		if part == "omitempty" {
			return true
		}
	}
	return false
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
		pv, err := extractBinary(payload, &attachments)
		if err != nil {
			return nil, nil, err
		}
		placeholders[i] = pv
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

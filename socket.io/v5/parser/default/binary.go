package socketio_v5_parser_default

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"

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
	// num arrives as a JSON number (float64) straight off the wire. Reject a
	// fractional, negative, or out-of-range value rather than letting int(num)
	// silently truncate it (e.g. 1.5 -> 1, substituting the wrong attachment) or
	// overflow on a huge value and index out of bounds. Comparing as float64
	// before converting keeps int(num) exact and in range.
	if num != math.Trunc(num) || num < 0 || num >= float64(len(attachments)) {
		return nil, false, fmt.Errorf("%w: placeholder num %v is not a valid attachment index", ErrParseBinary, num)
	}
	return attachments[int(num)], true, nil
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
	return valueHasBinaryDepth(value, maxBinaryScanDepth)
}

// valueHasBinaryDepth is valueHasBinary with an explicit recursion bound. The
// depth is threaded through the map/slice fast paths (not just the reflect path)
// so a self-referential map[string]interface{} / []interface{} — which Emit hands
// to HasBinary before encoding/json gets a chance to reject the cycle — can never
// overflow the stack; it simply stops at the bound and reports no binary, exactly
// as reflectHasBinary does for cyclic structures reached via pointers/interfaces.
func valueHasBinaryDepth(value interface{}, depth int) bool {
	if depth <= 0 {
		return false
	}
	// Fast paths for the common, already-decoded shapes avoid the reflect cost.
	switch v := value.(type) {
	case nil:
		return false
	case []byte:
		// A nil byte slice is rendered by encoding/json as JSON null — it is a
		// nullable value, not an empty buffer, so it must stay on the text path
		// (matching the JS client, where null is not binary). An empty non-nil
		// slice IS binary: it becomes a zero-length attachment.
		return v != nil
	case map[string]interface{}:
		for _, item := range v {
			if valueHasBinaryDepth(item, depth-1) {
				return true
			}
		}
		return false
	case []interface{}:
		for _, item := range v {
			if valueHasBinaryDepth(item, depth-1) {
				return true
			}
		}
		return false
	}
	return reflectHasBinary(reflect.ValueOf(value), depth)
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
	if ownsEncoding(rv) {
		return false
	}

	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return false
		}
		return reflectHasBinary(rv.Elem(), depth-1)
	case reflect.Slice:
		// []byte is the binary leaf; anything else is a slice to descend into.
		// (json.RawMessage and other marshaler-owned byte slices were already
		// excluded by the implementsMarshaler check above.) A nil byte slice is
		// NOT binary: encoding/json renders it as null, so it stays a nullable
		// JSON value instead of becoming an empty attachment. transformBinary
		// relies on this — it never lifts a subtree this function rejects.
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return !rv.IsNil()
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

// ownsEncoding reports whether rv controls its own JSON or text encoding via
// json.Marshaler / encoding.TextMarshaler, in which case its bytes are emitted by
// that marshaler (e.g. time.Time, json.RawMessage) and must never be lifted into a
// binary attachment.
//
// A value-receiver marshaler always owns its encoding. A type that implements the
// marshaler only on its POINTER receiver is honored by encoding/json ONLY when the
// value is addressable — slice/array elements, addressable struct fields, pointer
// derefs. For a non-addressable value held in an interface{} (a top-level Emit
// payload, a map value) json ignores the pointer marshaler and encodes the exported
// fields instead, so we must descend to find binary there rather than treat it as
// marshaler-owned. rv.CanAddr() mirrors that rule exactly, because
// reflectHasBinary/transformBinary recurse over the original values whose
// addressability matches what encoding/json sees.
func ownsEncoding(rv reflect.Value) bool {
	t := rv.Type()
	if t.Implements(jsonMarshalerType) || t.Implements(textMarshalerType) {
		return true
	}
	if rv.CanAddr() {
		pt := reflect.PointerTo(t)
		return pt.Implements(jsonMarshalerType) || pt.Implements(textMarshalerType)
	}
	return false
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
	sentinels := make(map[string][]byte)
	transformed := transformBinary(reflect.ValueOf(value), sentinels, nonce, maxBinaryScanDepth)
	data, err := json.Marshal(transformed.Interface())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseBinary, err)
	}
	return json.RawMessage(splicePlaceholders(data, sentinels, attachments)), nil
}

// transformBinary returns a value of the SAME type as rv in which every []byte on
// a binary-bearing path is replaced by a unique sentinel slice; each sentinel's
// base64 rendering is recorded in sentinels mapped to the original bytes. The
// attachment list and placeholder numbering are NOT assigned here — they are
// finalized by splicePlaceholders against the marshaled output, so a sentinel
// whose field encoding/json ends up dropping (e.g. a json tag/name conflict)
// never becomes an orphan attachment. Subtrees with no binary (or whose type owns
// its encoding, or once the depth bound is hit) are returned unchanged and shared
// with the original — they are never mutated, because only freshly built
// containers are ever written to.
func transformBinary(rv reflect.Value, sentinels map[string][]byte, nonce []byte, depth int) reflect.Value {
	if depth <= 0 || !rv.IsValid() || !reflectHasBinary(rv, depth) {
		return rv
	}

	switch rv.Kind() {
	case reflect.Pointer:
		np := reflect.New(rv.Type().Elem())
		np.Elem().Set(transformBinary(rv.Elem(), sentinels, nonce, depth-1))
		return np
	case reflect.Interface:
		out := reflect.New(rv.Type()).Elem()
		out.Set(transformBinary(rv.Elem(), sentinels, nonce, depth-1))
		return out
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return sentinelSlice(rv, sentinels, nonce)
		}
		ns := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			ns.Index(i).Set(transformBinary(rv.Index(i), sentinels, nonce, depth-1))
		}
		return ns
	case reflect.Array:
		na := reflect.New(rv.Type()).Elem()
		for i := 0; i < rv.Len(); i++ {
			na.Index(i).Set(transformBinary(rv.Index(i), sentinels, nonce, depth-1))
		}
		return na
	case reflect.Map:
		nm := reflect.MakeMap(rv.Type())
		for _, key := range rv.MapKeys() {
			nm.SetMapIndex(key, transformBinary(rv.MapIndex(key), sentinels, nonce, depth-1))
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
		ns.Field(i).Set(transformBinary(fv, sentinels, nonce, depth-1))
	}
	return ns
}

// sentinelSlice records rv's bytes under a unique sentinel and returns a
// same-typed slice holding that sentinel. encoding/json renders the sentinel as a
// base64 string, which splicePlaceholders then rewrites into the placeholder
// object (and only then assigns the attachment its index). The per-serialization
// index is the current sentinel count, which (with the random nonce) keeps every
// sentinel — and thus its base64 needle — unique.
func sentinelSlice(rv reflect.Value, sentinels map[string][]byte, nonce []byte) reflect.Value {
	orig := rv.Bytes()
	buf := make([]byte, len(orig))
	copy(buf, orig)

	// len(sentinels) is non-negative, so the conversion to the fixed-width index
	// is safe (no overflow/sign change).
	sentinel := makeSentinel(nonce, uint64(len(sentinels)))
	sentinels[base64.StdEncoding.EncodeToString(sentinel)] = buf

	sv := reflect.ValueOf(sentinel)
	if rv.Type() == byteSliceType {
		return sv
	}
	// rv is a named byte-slice type. A type whose underlying type is []byte
	// (e.g. type Blob []byte) is convertible from []byte, but a slice of a *named*
	// uint8 element (e.g. type Octet uint8; []Octet) is NOT — sv.Convert would
	// panic. Build the same-typed slice element by element instead, which works
	// for both; encoding/json still renders it as the sentinel's base64 needle.
	named := reflect.MakeSlice(rv.Type(), len(sentinel), len(sentinel))
	for i, b := range sentinel {
		named.Index(i).SetUint(uint64(b))
	}
	return named
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

// splicePlaceholders finalizes the binary attachments for one marshaled payload.
// It rewrites each sentinel's base64 JSON string ("<b64>") into a
// {"_placeholder":true,"num":N} object, assigning the surviving sentinels
// contiguous attachment indices (continuing from the message-wide *attachments)
// in order of their appearance in data, and appends their bytes to *attachments in
// the same order. A sentinel whose field encoding/json dropped (e.g. a json
// tag/name conflict) never appears in data, so it is discarded here rather than
// left as an orphan attachment with no placeholder — keeping the attachment count,
// the placeholder numbering, and the bytes consistent. Each sentinel is unique, so
// it appears at most once.
func splicePlaceholders(data []byte, sentinels map[string][]byte, attachments *[][]byte) []byte {
	// Collect the sentinels that actually survived into the marshaled output,
	// ordered by where they appear so numbering is deterministic.
	type presentSentinel struct {
		pos int
		b64 string
	}
	present := make([]presentSentinel, 0, len(sentinels))
	for b64 := range sentinels {
		if pos := bytes.Index(data, []byte(`"`+b64+`"`)); pos >= 0 {
			present = append(present, presentSentinel{pos: pos, b64: b64})
		}
	}
	sort.Slice(present, func(i, j int) bool { return present[i].pos < present[j].pos })

	for _, s := range present {
		num := len(*attachments)
		*attachments = append(*attachments, sentinels[s.b64])
		needle := []byte(`"` + s.b64 + `"`)
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
	case reflect.Interface, reflect.Pointer:
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
	if count == 0 {
		// HasBinary saw a []byte, but no placeholder survived into the
		// marshaled payloads (e.g. the only buffer sat in a `json:"-"` field
		// or was dropped by omitempty). The wire form carries no binary, so
		// emit the plain EVENT/ACK packet instead of a 0-attachment binary
		// one — peers should not be asked to reconstruct an empty set.
		if binaryType == socketio_v5.PacketBinaryEvent {
			headerMsg.Type = socketio_v5.PacketEvent
		} else {
			headerMsg.Type = socketio_v5.PacketAck
		}
		headerMsg.BinaryAttachments = nil
	}

	header, err := p.serializeHeader(headerMsg)
	if err != nil {
		return nil, nil, err
	}
	return header, attachments, nil
}

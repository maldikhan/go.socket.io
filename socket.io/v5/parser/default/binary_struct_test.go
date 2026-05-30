package socketio_v5_parser_default

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// uploadStruct is a typical "file upload" payload: a struct carrying a []byte.
type uploadStruct struct {
	File []byte `json:"file"`
	Name string `json:"name"`
}

// taggedStruct exercises json tag handling: renamed field, omitempty, skipped.
type taggedStruct struct {
	Data    []byte `json:"data"`
	Renamed string `json:"label,omitempty"`
	Skip    string `json:"-"`
	hidden  string //nolint:unused // unexported; encoding/json ignores it
}

// nestedStruct carries binary through a nested struct, a pointer, a typed map
// and a named slice to exercise the full reflective traversal.
type innerStruct struct {
	Blob []byte `json:"blob"`
}

type namedByteSlice []byte

type nestedStruct struct {
	Inner   innerStruct       `json:"inner"`
	Ptr     *innerStruct      `json:"ptr"`
	ByMap   map[string][]byte `json:"bymap"`
	Bytes   namedByteSlice    `json:"bytes"`
	Strings []string          `json:"strings"`
}

// cyclicNode is a self-referential type used to prove cycle safety.
type cyclicNode struct {
	Next *cyclicNode
	Data []byte
}

func TestHasBinary_Struct(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	tests := []struct {
		name    string
		payload interface{}
		want    bool
	}{
		{name: "struct with byte field", payload: uploadStruct{File: []byte("x")}, want: true},
		{name: "pointer to struct with byte field", payload: &uploadStruct{File: []byte("x")}, want: true},
		{name: "struct without byte field", payload: struct{ Name string }{Name: "n"}, want: false},
		{name: "string slice is not binary", payload: []string{"a", "b"}, want: false},
		{name: "named byte slice is binary", payload: namedByteSlice("hi"), want: true},
		{name: "typed byte map is binary", payload: map[string][]byte{"k": []byte("v")}, want: true},
		{name: "struct with nested struct/map/slice binary", payload: nestedStruct{
			Inner: innerStruct{Blob: []byte("b")},
		}, want: true},
		{name: "nil pointer struct is not binary", payload: (*uploadStruct)(nil), want: false},
		{name: "byte array field is not a binary leaf", payload: struct{ A [3]byte }{A: [3]byte{1, 2, 3}}, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{tt.payload}}
			assert.Equal(t, tt.want, p.HasBinary(ev))
		})
	}
}

// TestHasBinary_Cycle proves that a cyclic structure does not hang HasBinary.
func TestHasBinary_Cycle(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	a := &cyclicNode{Data: []byte("a")}
	b := &cyclicNode{}
	a.Next = b
	b.Next = a // cycle

	done := make(chan bool, 1)
	go func() {
		ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{a}}
		done <- p.HasBinary(ev)
	}()
	select {
	case got := <-done:
		assert.True(t, got, "cyclic structure carrying []byte must report binary")
	case <-time.After(5 * time.Second):
		t.Fatal("HasBinary hung on a cyclic structure")
	}
}

// TestSerializeBinary_Struct verifies the SEND extraction for structs: the
// attachment holds the original bytes and the header JSON carries a placeholder
// where the byte field was, with non-byte fields preserved per their json tags.
func TestSerializeBinary_Struct(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	t.Run("single struct payload", func(t *testing.T) {
		orig := []byte{0xDE, 0xAD, 0xBE, 0xEF}
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "upload",
				Payloads: []interface{}{uploadStruct{File: orig, Name: "avatar"}},
			},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		assert.Equal(t, orig, attachments[0])

		// Header is "51-[\"upload\",{...}]"; decode the JSON array after "51-".
		arr := decodeHeaderArray(t, header, 1)
		obj, ok := arr[1].(map[string]interface{})
		require.True(t, ok)
		// Byte field becomes a placeholder.
		assert.Equal(t, map[string]interface{}{"_placeholder": true, "num": float64(0)}, obj["file"])
		// Non-byte field preserved with its json-tag name.
		assert.Equal(t, "avatar", obj["name"])
	})

	t.Run("pointer to struct", func(t *testing.T) {
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			NS:    "/",
			Event: &socketio_v5.Event{Name: "u", Payloads: []interface{}{&uploadStruct{File: []byte("z")}}},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		assert.Equal(t, []byte("z"), attachments[0])
		arr := decodeHeaderArray(t, header, 1)
		obj := arr[1].(map[string]interface{})
		assert.Equal(t, map[string]interface{}{"_placeholder": true, "num": float64(0)}, obj["file"])
	})

	t.Run("json tags omitempty and skip", func(t *testing.T) {
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "t",
				Payloads: []interface{}{taggedStruct{Data: []byte("d"), Renamed: "", Skip: "secret"}},
			},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		arr := decodeHeaderArray(t, header, 1)
		obj := arr[1].(map[string]interface{})
		// Placeholder under the json-tag name "data".
		assert.Equal(t, map[string]interface{}{"_placeholder": true, "num": float64(0)}, obj["data"])
		// omitempty empty string is omitted.
		_, hasLabel := obj["label"]
		assert.False(t, hasLabel, "omitempty empty field must be omitted")
		// json:"-" is skipped.
		_, hasSkip := obj["Skip"]
		assert.False(t, hasSkip)
		// Compare exactly with what encoding/json would produce for the non-byte
		// fields by marshaling a struct whose byte field is a placeholder marker.
		assert.Len(t, obj, 1, "only the data placeholder should remain")
	})

	t.Run("nested struct/pointer/map/named-slice", func(t *testing.T) {
		payload := nestedStruct{
			Inner:   innerStruct{Blob: []byte("i")},
			Ptr:     &innerStruct{Blob: []byte("p")},
			ByMap:   map[string][]byte{"k": []byte("m")},
			Bytes:   namedByteSlice("n"),
			Strings: []string{"s1", "s2"},
		}
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			NS:    "/",
			Event: &socketio_v5.Event{Name: "n", Payloads: []interface{}{payload}},
		})
		require.NoError(t, err)
		// Four []byte leaves: inner.blob, ptr.blob, bymap[k], bytes.
		require.Len(t, attachments, 4)

		arr := decodeHeaderArray(t, header, 4)
		obj := arr[1].(map[string]interface{})
		assert.Contains(t, obj["inner"].(map[string]interface{}), "blob")
		assert.Contains(t, obj["ptr"].(map[string]interface{}), "blob")
		assert.Contains(t, obj["bymap"].(map[string]interface{}), "k")
		// Named byte slice serialized as a placeholder, not a number array.
		ph, ok := obj["bytes"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, true, ph["_placeholder"])
		// Non-byte slice preserved as a JSON array of strings.
		assert.Equal(t, []interface{}{"s1", "s2"}, obj["strings"])
	})
}

// TestStructBinaryRoundTrip proves the full SEND->parse->reconstruct->RECEIVE
// path for a struct with a []byte field.
func TestStructBinaryRoundTrip(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	orig := []byte{0x00, 0x10, 0x20, 0xFF}
	header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type: socketio_v5.PacketEvent,
		NS:   "/",
		Event: &socketio_v5.Event{
			Name:     "upload",
			Payloads: []interface{}{uploadStruct{File: orig, Name: "doc"}},
		},
	})
	require.NoError(t, err)

	parsed, err := p.Parse(header)
	require.NoError(t, err)
	require.NoError(t, p.ReconstructBinary(parsed, attachments))

	// Deliver the reconstructed payload to a typed struct handler.
	called := false
	var got uploadStruct
	cb := p.WrapCallback(func(v uploadStruct) {
		called = true
		got = v
	})
	require.NotNil(t, cb)
	cb(parsed.Event.Payloads)

	require.True(t, called, "struct handler must fire for a binary event")
	assert.Equal(t, orig, got.File, "byte field must survive the round trip intact")
	assert.Equal(t, "doc", got.Name)
}

// TestWrapCallback_StructFromMap covers the RECEIVE conversion in isolation: a
// reconstructed map carrying []byte binds to a struct{ File []byte } handler.
func TestWrapCallback_StructFromMap(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	bin := []byte{0x01, 0x02, 0x03}

	t.Run("struct with byte field accepted", func(t *testing.T) {
		called := false
		var got uploadStruct
		cb := p.WrapCallback(func(v uploadStruct) {
			called = true
			got = v
		})
		require.NotNil(t, cb)
		cb([]interface{}{map[string]interface{}{"file": bin, "name": "n"}})
		require.True(t, called)
		assert.Equal(t, bin, got.File)
		assert.Equal(t, "n", got.Name)
	})

	t.Run("top-level []byte to []byte param still works", func(t *testing.T) {
		called := false
		var got []byte
		cb := p.WrapCallback(func(b []byte) {
			called = true
			got = b
		})
		require.NotNil(t, cb)
		cb([]interface{}{bin})
		require.True(t, called)
		assert.Equal(t, bin, got)
	})

	t.Run("top-level []byte to string param is rejected", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(s string) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{[]byte("data")})
		assert.False(t, called, "binary leaf must never be converted to a base64 string param")
	})

	t.Run("map still binds to map param", func(t *testing.T) {
		called := false
		var got map[string]interface{}
		cb := p.WrapCallback(func(m map[string]interface{}) {
			called = true
			got = m
		})
		require.NotNil(t, cb)
		cb([]interface{}{map[string]interface{}{"file": bin}})
		require.True(t, called)
		raw, ok := got["file"].([]byte)
		require.True(t, ok)
		assert.Equal(t, bin, raw)
	})

	t.Run("map not convertible to int is rejected", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(n int) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{map[string]interface{}{"file": bin}})
		assert.False(t, called)
	})

	t.Run("slice payload converts to struct slice", func(t *testing.T) {
		called := false
		var got []uploadStruct
		cb := p.WrapCallback(func(v []uploadStruct) {
			called = true
			got = v
		})
		require.NotNil(t, cb)
		cb([]interface{}{[]interface{}{
			map[string]interface{}{"file": bin, "name": "a"},
		}})
		require.True(t, called)
		require.Len(t, got, 1)
		assert.Equal(t, bin, got[0].File)
	})
}

// decodeHeaderArray strips the "<count>-" prefix from a binary header and
// returns the decoded top-level JSON array, asserting the attachment count.
func decodeHeaderArray(t *testing.T, header []byte, wantCount int) []interface{} {
	t.Helper()
	// header looks like: <type><count>-<json...>  e.g. 51-["ev",{...}]
	idx := -1
	for i, ch := range header {
		if ch == '-' {
			idx = i
			break
		}
	}
	require.GreaterOrEqual(t, idx, 1, "header must contain a count prefix")
	count := string(header[1:idx])
	assert.Equal(t, strconv.Itoa(wantCount), count, "attachment count prefix")
	var arr []interface{}
	require.NoError(t, json.Unmarshal(header[idx+1:], &arr))
	require.GreaterOrEqual(t, len(arr), 1)
	return arr
}

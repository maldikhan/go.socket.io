package socketio_v5_parser_default

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// --- Bug A: HasBinary must detect []byte inside structs / pointers / nested
// containers on the SEND path. ---

type uploadStruct struct {
	File []byte
	Name string
}

type nestedStruct struct {
	Inner uploadStruct
}

type sliceStruct struct {
	Files [][]byte
}

type mapStruct struct {
	Files map[string][]byte
}

type noBinaryStruct struct {
	Name string
	Age  int
}

type unexportedBinaryStruct struct {
	secret []byte // unexported: not marshalable, must be ignored
	Name   string
}

type cyclic struct {
	Self *cyclic
	Name string
}

func TestHasBinary_StructPayloads(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	data := []byte{0x01, 0x02, 0x03, 0xff}

	tests := []struct {
		name    string
		payload interface{}
		want    bool
	}{
		{"struct with []byte field", uploadStruct{File: data, Name: "x"}, true},
		{"pointer to struct with []byte", &uploadStruct{File: data}, true},
		{"nested struct with []byte", nestedStruct{Inner: uploadStruct{File: data}}, true},
		{"struct with [][]byte slice", sliceStruct{Files: [][]byte{data}}, true},
		{"struct with map[string][]byte", mapStruct{Files: map[string][]byte{"a": data}}, true},
		{"typed slice of structs with []byte", []uploadStruct{{File: data}}, true},
		{"typed map to struct with []byte", map[string]uploadStruct{"k": {File: data}}, true},

		{"struct without []byte", noBinaryStruct{Name: "x", Age: 1}, false},
		{"pointer to struct without []byte", &noBinaryStruct{Name: "x"}, false},
		{"unexported []byte field is ignored", unexportedBinaryStruct{secret: data, Name: "x"}, false},
		{"plain []string is not binary", []string{"a", "b"}, false},
		{"nil pointer struct", (*uploadStruct)(nil), false},
		{"plain string", "hello", false},
		{"nil payload", nil, false},

		// Fast-path / existing-shape cases stay correct.
		{"plain []byte", data, true},
		{"map[string]interface{} with []byte", map[string]interface{}{"f": data}, true},
		{"[]interface{} with []byte", []interface{}{"x", data}, true},
		{"map[string]interface{} without []byte", map[string]interface{}{"f": "s"}, false},
		{"[]interface{} without []byte", []interface{}{"x", 1}, false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			event := &socketio_v5.Event{Name: "upload", Payloads: []interface{}{tt.payload}}
			assert.Equal(t, tt.want, parser.HasBinary(event))
		})
	}
}

func TestHasBinary_NilEvent(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))
	assert.False(t, parser.HasBinary(nil))
}

// A cyclic structure must not hang HasBinary (depth guard).
func TestHasBinary_CyclicDoesNotHang(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	a := &cyclic{Name: "a"}
	a.Self = a // self-reference

	event := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{a}}
	// No []byte anywhere, so the result is false, and crucially it returns.
	assert.False(t, parser.HasBinary(event))

	// Same but with binary reachable before the cycle would matter.
	b := &cyclic{Name: "b"}
	b.Self = b
	withBin := struct {
		Loop *cyclic
		File []byte
	}{Loop: b, File: []byte{0x09}}
	event2 := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{withBin}}
	assert.True(t, parser.HasBinary(event2))
}

// Full Emit->SerializeBinary round-trip: a struct{File []byte} must produce a
// real BINARY packet with the bytes as an attachment, not a base64 string.
func TestSerializeBinary_StructProducesAttachment(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	data := []byte{0xde, 0xad, 0xbe, 0xef}
	event := &socketio_v5.Event{
		Name:     "upload",
		Payloads: []interface{}{uploadStruct{File: data, Name: "pic"}},
	}

	require.True(t, parser.HasBinary(event), "HasBinary must select the binary path")

	header, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: event,
	})
	require.NoError(t, err)

	// Exactly one attachment, equal to the original bytes (NOT base64).
	require.Len(t, attachments, 1)
	assert.True(t, bytes.Equal(data, attachments[0]), "attachment must be the raw bytes")

	// Header is a PacketBinaryEvent ("5"), declares 1 attachment ("1-") and uses
	// a placeholder for the bytes rather than a base64 string.
	headerStr := string(header)
	assert.Equal(t, byte('5'), header[0], "must be a binary event packet")
	assert.Contains(t, headerStr, "1-")
	assert.Contains(t, headerStr, "_placeholder")
	assert.Contains(t, headerStr, `"num":0`)
	// The struct's other field must survive in the header JSON.
	assert.Contains(t, headerStr, `"Name":"pic"`)
	// Base64 of {0xde,0xad,0xbe,0xef} is "3q2+7w==" — it must NOT appear.
	assert.NotContains(t, headerStr, "3q2+7w==")
}

// Contrast: a struct without []byte stays on the plain text Serialize path and
// (if it ever went through SerializeBinary) yields zero attachments.
func TestSerialize_NonBinaryStructStaysText(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	event := &socketio_v5.Event{
		Name:     "hello",
		Payloads: []interface{}{noBinaryStruct{Name: "x", Age: 7}},
	}
	require.False(t, parser.HasBinary(event))

	out, err := parser.Serialize(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: event,
	})
	require.NoError(t, err)
	assert.Equal(t, byte('2'), out[0], "must be a plain event packet")
	assert.Contains(t, string(out), `"Name":"x"`)
}

// --- Bug B: typed STRUCT handlers must receive reconstructed binary on the
// RECEIVE path. ---

func TestWrapCallback_StructHandlerReceivesBinary(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	original := []byte{0x00, 0x10, 0x20, 0x7f, 0xfe}

	var got uploadStruct
	called := false
	cb := func(v uploadStruct) {
		got = v
		called = true
	}

	wrapped := parser.WrapCallback(cb)
	require.NotNil(t, wrapped)

	// Reconstruction produces a map[string]interface{} that is NOT directly
	// assignable to uploadStruct; the marshal/unmarshal fallback must convert it.
	reconstructed := map[string]interface{}{
		"File": original,
		"Name": "pic",
	}
	wrapped([]interface{}{reconstructed})

	require.True(t, called, "struct handler must fire")
	assert.Equal(t, "pic", got.Name)
	// CRUCIAL: the []byte round-trips through base64 back to the original bytes.
	assert.True(t, bytes.Equal(original, got.File), "File must equal original bytes")
}

func TestWrapCallback_NestedStructHandlerReceivesBinary(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	original := []byte("nested-bytes")

	var got nestedStruct
	cb := func(v nestedStruct) { got = v }
	wrapped := parser.WrapCallback(cb)
	require.NotNil(t, wrapped)

	reconstructed := map[string]interface{}{
		"Inner": map[string]interface{}{
			"File": original,
			"Name": "n",
		},
	}
	wrapped([]interface{}{reconstructed})

	assert.Equal(t, "n", got.Inner.Name)
	assert.True(t, bytes.Equal(original, got.Inner.File))
}

// The existing direct-assignment fast paths must keep working.
func TestWrapCallback_MapAndByteHandlersStillWork(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	original := []byte{1, 2, 3}

	t.Run("map[string]interface{} handler", func(t *testing.T) {
		var got map[string]interface{}
		cb := func(v map[string]interface{}) { got = v }
		wrapped := parser.WrapCallback(cb)
		require.NotNil(t, wrapped)
		in := map[string]interface{}{"file": original}
		wrapped([]interface{}{in})
		require.NotNil(t, got)
		assert.True(t, bytes.Equal(original, got["file"].([]byte)))
	})

	t.Run("[]byte handler", func(t *testing.T) {
		var got []byte
		cb := func(v []byte) { got = v }
		wrapped := parser.WrapCallback(cb)
		require.NotNil(t, wrapped)
		wrapped([]interface{}{original})
		assert.True(t, bytes.Equal(original, got))
	})
}

// A non-RawMessage value that can neither be assigned nor marshaled into the
// target type must be reported and skipped (no panic, callback not called).
func TestWrapCallback_UnconvertibleValueSkipped(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	called := false
	cb := func(v int) { called = true }
	wrapped := parser.WrapCallback(cb)
	require.NotNil(t, wrapped)

	// A string value cannot json.Unmarshal into an int.
	wrapped([]interface{}{"not-an-int"})
	assert.False(t, called)
}

// json.RawMessage path is unchanged: ordinary JSON still unmarshals into a
// struct handler.
func TestWrapCallback_RawMessageStructStillWorks(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	var got noBinaryStruct
	cb := func(v noBinaryStruct) { got = v }
	wrapped := parser.WrapCallback(cb)
	require.NotNil(t, wrapped)

	wrapped([]interface{}{json.RawMessage(`{"Name":"z","Age":5}`)})
	assert.Equal(t, "z", got.Name)
	assert.Equal(t, 5, got.Age)
}

// --- Additional reflective coverage for the SEND path (extractBinary). ---

type taggedStruct struct {
	File    []byte `json:"file"`
	Skipped string `json:"-"`
	Renamed string `json:"renamed,omitempty"`
	Empty   string `json:",omitempty"` // empty tag name -> field name "Empty"
}

type arrayStruct struct {
	Two [2][]byte
}

type ptrFieldStruct struct {
	File *[]byte
}

// SerializeBinary must honor json tags, skip json:"-" fields, descend through
// pointer fields and fixed-size arrays, and stringify non-string map keys.
func TestSerializeBinary_ReflectiveShapes(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	t.Run("json tags and dash handling", func(t *testing.T) {
		data := []byte{0xaa}
		header, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{taggedStruct{File: data, Skipped: "x", Renamed: "r", Empty: "e"}},
			},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		assert.Equal(t, data, attachments[0])
		hs := string(header)
		assert.Contains(t, hs, `"file":`)        // tag-renamed []byte key
		assert.Contains(t, hs, `"renamed":"r"`)  // tag with options
		assert.Contains(t, hs, `"Empty":"e"`)    // empty tag name -> field name
		assert.NotContains(t, hs, `"Skipped"`)   // json:"-" omitted
		assert.NotContains(t, hs, `"x"`)         // skipped value gone
		assert.Contains(t, hs, `"_placeholder"`) // bytes became a placeholder
	})

	t.Run("array of []byte field", func(t *testing.T) {
		_, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{arrayStruct{Two: [2][]byte{{1}, {2}}}},
			},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 2)
		assert.Equal(t, []byte{1}, attachments[0])
		assert.Equal(t, []byte{2}, attachments[1])
	})

	t.Run("pointer-to-[]byte field", func(t *testing.T) {
		b := []byte("ptr")
		_, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{ptrFieldStruct{File: &b}},
			},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		assert.Equal(t, []byte("ptr"), attachments[0])
	})

	t.Run("nil pointer-to-[]byte field yields no attachment", func(t *testing.T) {
		_, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{ptrFieldStruct{File: nil}},
			},
		})
		require.NoError(t, err)
		assert.Len(t, attachments, 0)
	})

	t.Run("non-string map key with binary value", func(t *testing.T) {
		header, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{map[int][]byte{7: {0x1}}},
			},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		assert.Equal(t, []byte{0x1}, attachments[0])
		assert.Contains(t, string(header), `"7"`) // int key stringified
	})

	t.Run("nil payload extracts nothing", func(t *testing.T) {
		_, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{nil},
			},
		})
		require.NoError(t, err)
		assert.Len(t, attachments, 0)
	})

	t.Run("scalar payload passes through", func(t *testing.T) {
		header, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{42},
			},
		})
		require.NoError(t, err)
		assert.Len(t, attachments, 0)
		assert.Contains(t, string(header), "42")
	})
}

// reflectExtractBinary / reflectHasBinary must terminate on cyclic structures
// (depth bound) without hanging, including when serializing.
func TestSerializeBinary_CyclicTerminates(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	a := &cyclic{Name: "a"}
	a.Self = a

	withBin := struct {
		Loop *cyclic
		File []byte
	}{Loop: a, File: []byte{0x42}}

	// HasBinary must terminate and report true via the File field.
	require.True(t, parser.HasBinary(&socketio_v5.Event{
		Name: "ev", Payloads: []interface{}{withBin},
	}))

	// SerializeBinary walks the same structure; it must also terminate. The
	// cyclic pointer chain hits the depth bound and is returned as-is, which is
	// not JSON-marshalable, so an error is returned rather than a hang.
	_, _, err := parser.SerializeBinary(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{withBin}},
	})
	assert.Error(t, err)
}

// reflectHasBinary array branch and []byte-array (not binary) cases.
func TestHasBinary_ArrayAndByteArray(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	// [3][]byte array containing binary -> true.
	assert.True(t, parser.HasBinary(&socketio_v5.Event{
		Name:     "ev",
		Payloads: []interface{}{[3][]byte{{1}, nil, nil}},
	}))

	// A [4]byte array is NOT a Socket.IO attachment (only []byte is) -> false.
	assert.False(t, parser.HasBinary(&socketio_v5.Event{
		Name:     "ev",
		Payloads: []interface{}{[4]byte{1, 2, 3, 4}},
	}))

	// Typed slice of non-binary -> false.
	assert.False(t, parser.HasBinary(&socketio_v5.Event{
		Name:     "ev",
		Payloads: []interface{}{[]int{1, 2, 3}},
	}))
}

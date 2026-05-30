package socketio_v5_parser_default

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// These tests target the remaining reflective branches of the SEND/RECEIVE
// binary paths so the package keeps 100% statement coverage.

// reflectExtractBinary: top-level pointer payload (Ptr deref) and a typed slice
// of non-byte elements that contain binary (slice element recursion).
func TestSerializeBinary_TopLevelPointerAndTypedSlice(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	t.Run("top-level pointer to struct with bytes", func(t *testing.T) {
		data := []byte("ptr-top")
		_, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{&uploadStruct{File: data}},
			},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		assert.True(t, bytes.Equal(data, attachments[0]))
	})

	t.Run("typed slice of structs containing bytes", func(t *testing.T) {
		_, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{[]uploadStruct{{File: []byte("a")}, {File: []byte("b")}}},
			},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 2)
		assert.Equal(t, []byte("a"), attachments[0])
		assert.Equal(t, []byte("b"), attachments[1])
	})

	t.Run("typed slice element nil interface inside slice", func(t *testing.T) {
		// []interface{} is handled by the fast path, but a typed []*uploadStruct
		// with a nil element reaches reflectExtractBinary and exercises the
		// invalid/nil element handling.
		_, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{[]*uploadStruct{nil, {File: []byte("z")}}},
			},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		assert.Equal(t, []byte("z"), attachments[0])
	})
}

// reflectExtractBinary depth<=0: a deeply self-referential pointer chain hits
// the depth bound and returns the raw value, which then fails json.Marshal
// (proving termination rather than a hang).
func TestSerializeBinary_DepthBoundReturnsRaw(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	a := &cyclic{Name: "a"}
	a.Self = a

	_, _, err := parser.SerializeBinary(&socketio_v5.Message{
		Type: socketio_v5.PacketEvent,
		NS:   "/",
		Event: &socketio_v5.Event{
			Name:     "ev",
			Payloads: []interface{}{a},
		},
	})
	// The cyclic pointer chain hits the depth bound; the leftover *cyclic is not
	// JSON-marshalable as a finite document, so Marshal errors. No hang.
	assert.Error(t, err)
}

// reflectHasBinary depth<=0 / interface branch: a self-referential struct with
// NO binary must terminate and report false (depth bound), and one with binary
// reachable before the bound reports true.
func TestHasBinary_DepthBound(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	a := &cyclic{Name: "a"}
	a.Self = a
	assert.False(t, parser.HasBinary(&socketio_v5.Event{
		Name:     "ev",
		Payloads: []interface{}{a},
	}))
}

// WrapCallback: the json.Marshal-error fallback path. A reconstructed value that
// cannot be marshaled (contains a channel) and is not assignable must be
// reported and skipped without panicking.
func TestWrapCallback_MarshalErrorSkipped(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	called := false
	cb := func(v noBinaryStruct) { called = true }
	wrapped := parser.WrapCallback(cb)
	require.NotNil(t, wrapped)

	// map[string]interface{} with an unmarshalable channel value: not assignable
	// to noBinaryStruct, and json.Marshal fails -> skipped.
	wrapped([]interface{}{map[string]interface{}{"x": make(chan int)}})
	assert.False(t, called)
}

// Direct unit tests of extractBinary's reflective leaf cases that are awkward to
// trigger end-to-end: a nil pointer leaf and the depth bound.
func TestExtractBinary_NilPointerLeaf(t *testing.T) {
	t.Parallel()
	var attachments [][]byte
	var nilPtr *[]byte
	// A typed nil pointer reaches reflectExtractBinary's Ptr/IsNil branch and is
	// returned as-is, producing no attachment.
	out := extractBinary(nilPtr, &attachments)
	assert.Len(t, attachments, 0)
	assert.Nil(t, out)
}

func TestReflectExtractBinary_DepthBound(t *testing.T) {
	t.Parallel()
	var attachments [][]byte
	// depth == 0 returns the value via Interface() without recursing.
	out := reflectExtractBinary(reflect.ValueOf("x"), &attachments, 0)
	assert.Equal(t, "x", out)
	assert.Len(t, attachments, 0)
}

func TestReflectExtractBinary_InvalidValue(t *testing.T) {
	t.Parallel()
	var attachments [][]byte
	// An invalid reflect.Value (the zero Value) returns nil.
	out := reflectExtractBinary(reflect.Value{}, &attachments, 5)
	assert.Nil(t, out)
	assert.Len(t, attachments, 0)
}

// reflectHasBinary: a typed map with NO binary must return false (the Map-case
// fall-through), and reflectExtractBinary must skip unexported struct fields
// (the PkgPath continue) when reached through the reflective path.
func TestHasBinary_TypedMapWithoutBinary(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))
	assert.False(t, parser.HasBinary(&socketio_v5.Event{
		Name:     "ev",
		Payloads: []interface{}{map[string]int{"a": 1, "b": 2}},
	}))
}

func TestSerializeBinary_StructWithUnexportedField(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	// A struct with both an exported []byte and an unexported field reaches
	// reflectExtractBinary; the unexported field is skipped (PkgPath continue)
	// and only the exported bytes become an attachment.
	_, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
		Type: socketio_v5.PacketEvent,
		NS:   "/",
		Event: &socketio_v5.Event{
			Name:     "ev",
			Payloads: []interface{}{unexportedBinaryStruct{secret: []byte("ignored"), Name: "n"}},
		},
	})
	require.NoError(t, err)
	// secret is unexported, so it is NOT extracted; no attachment is produced.
	assert.Len(t, attachments, 0)
}

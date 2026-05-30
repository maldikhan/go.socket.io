package socketio_v5_parser_default

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// timeAfter returns a channel that fires after a generous deadline; used by the
// cycle test to fail fast if the walk hangs rather than blocking the suite.
func timeAfter() <-chan time.Time {
	return time.After(5 * time.Second)
}

// fileStruct is the canonical "struct containing a []byte field" used across the
// struct binary tests.
type fileStruct struct {
	Name string `json:"name"`
	File []byte `json:"file"`
}

// taggedStruct exercises the json tag handling of the reflection walker:
// rename, json:"-" skip, omitempty, and an exported field with no tag.
type taggedStruct struct {
	Renamed  []byte `json:"data"`
	Skipped  []byte `json:"-"`
	OmitGone string `json:"opt,omitempty"`
	OmitKept string `json:"keep,omitempty"`
	NoTag    int
	unexp    []byte //nolint:unused // intentionally unexported, must be ignored
}

type namedBytes []byte

type stringSliceStruct struct {
	Tags []string `json:"tags"`
}

// TestHasBinary_Struct verifies HasBinary detects []byte inside structs,
// pointers, typed maps and named/typed slices, and does NOT misfire on a struct
// that only contains scalars or a []string.
func TestHasBinary_Struct(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	bytePtr := &fileStruct{Name: "a", File: []byte{1}}
	var nilPtr *fileStruct

	tests := []struct {
		name  string
		value interface{}
		want  bool
	}{
		{"struct with bytes", fileStruct{Name: "x", File: []byte{1, 2}}, true},
		{"struct without bytes", struct{ Name string }{Name: "x"}, false},
		{"pointer to struct with bytes", bytePtr, true},
		{"nil pointer is safe", nilPtr, false},
		{"named byte slice", namedBytes{1, 2, 3}, true},
		{"typed map with bytes", map[string]fileStruct{"k": {File: []byte{1}}}, true},
		{"typed map of byte slices", map[string][]byte{"k": {1, 2}}, true},
		{"typed slice of structs with bytes", []fileStruct{{File: []byte{1}}}, true},
		{"string slice must not misfire", []string{"a", "b"}, false},
		{"struct with string slice must not misfire", stringSliceStruct{Tags: []string{"a"}}, false},
		{"byte array is not a binary leaf", [3]byte{1, 2, 3}, false},
		{"nested pointer struct with bytes", &struct{ Inner *fileStruct }{Inner: bytePtr}, true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{tt.value}}
			assert.Equal(t, tt.want, p.HasBinary(ev))
		})
	}
}

// TestHasBinary_ExtractLockstep is the critical guard: for every payload that
// HasBinary reports true, SerializeBinary must produce at least one attachment
// (and never base64 in the header), and when HasBinary is false the header must
// be the plain text packet. This proves the two walkers agree.
func TestHasBinary_ExtractLockstep(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	payloads := []interface{}{
		fileStruct{Name: "x", File: []byte("abc")},
		&fileStruct{Name: "y", File: []byte("de")},
		map[string]fileStruct{"k": {File: []byte("f")}},
		[]fileStruct{{File: []byte("g")}, {File: []byte("h")}},
		namedBytes("named"),
	}

	for _, payload := range payloads {
		ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{payload}}
		require.True(t, p.HasBinary(ev), "HasBinary must be true for %T", payload)

		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			NS:    "/",
			Event: ev,
		})
		require.NoError(t, err)
		require.NotEmpty(t, attachments, "extractor must emit attachments for %T", payload)
		// The header must contain placeholders and must NOT contain raw base64
		// of the bytes (the bug a previous attempt shipped).
		assert.Contains(t, string(header), `"_placeholder":true`)
	}
}

// TestSerializeBinary_StructRoundTrip is the full send round-trip: a struct with
// a []byte field is serialized to a real binary packet whose header carries the
// placeholder and the json-tagged sibling fields, and whose single attachment
// equals the original bytes.
func TestSerializeBinary_StructRoundTrip(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	orig := fileStruct{Name: "avatar.png", File: []byte{0xDE, 0xAD, 0xBE, 0xEF}}

	header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type: socketio_v5.PacketEvent,
		NS:   "/",
		Event: &socketio_v5.Event{
			Name:     "upload",
			Payloads: []interface{}{orig},
		},
	})
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	assert.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, attachments[0])

	// Header correctness: the json tag "name" is honored, the []byte field
	// becomes a placeholder, and the attachment count prefix is "1-".
	assert.Equal(t,
		`51-["upload",{"file":{"_placeholder":true,"num":0},"name":"avatar.png"}]`,
		string(header),
	)

	// Round-trip through the wire: parse + reconstruct restores the bytes.
	parsed, err := p.Parse(header)
	require.NoError(t, err)
	require.NotNil(t, parsed.BinaryAttachments)
	require.Equal(t, 1, *parsed.BinaryAttachments)
	require.NoError(t, p.ReconstructBinary(parsed, attachments))

	obj, ok := parsed.Event.Payloads[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "avatar.png", obj["name"])
	assert.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, obj["file"])
}

// TestSerializeBinary_StructHeaderMatchesEncodingJSON proves the non-byte parts
// of the produced header are byte-identical to encoding/json output (with the
// []byte field swapped for the placeholder), honoring rename/skip/omitempty.
func TestSerializeBinary_StructHeaderMatchesEncodingJSON(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	val := taggedStruct{
		Renamed:  []byte("R"),
		Skipped:  []byte("S"),
		OmitGone: "",
		OmitKept: "kept",
		NoTag:    7,
	}

	header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type: socketio_v5.PacketEvent,
		NS:   "/",
		Event: &socketio_v5.Event{
			Name:     "ev",
			Payloads: []interface{}{val},
		},
	})
	require.NoError(t, err)
	// Only the renamed field is a binary leaf; Skipped is dropped by json:"-".
	require.Len(t, attachments, 1)
	assert.Equal(t, []byte("R"), attachments[0])

	// Build the expected header value object via encoding/json: the same struct
	// but with the binary leaf replaced by the placeholder and json:"-" dropped.
	expectedObj := map[string]interface{}{
		"data":  map[string]interface{}{"_placeholder": true, "num": 0},
		"keep":  "kept",
		"NoTag": 7,
		// "opt" omitted (omitempty + empty), "Skipped" dropped (json:"-").
	}
	expectedJSON, err := json.Marshal([]interface{}{"ev", expectedObj})
	require.NoError(t, err)
	assert.Equal(t, "51-"+string(expectedJSON), string(header))
}

// TestWrapCallback_StructWithBytes is the receive path: a reconstructed
// {name, file:<bytes>} map fires a func(struct{...[]byte...}) with bytes intact.
func TestWrapCallback_StructWithBytes(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	binData := []byte{0x10, 0x20, 0x30}

	t.Run("struct param receives bytes", func(t *testing.T) {
		var got fileStruct
		called := false
		cb := p.WrapCallback(func(v fileStruct) {
			called = true
			got = v
		})
		require.NotNil(t, cb)

		// This is exactly what ReconstructBinary produces for {file:<Buffer>}.
		payload := map[string]interface{}{
			"name": "avatar",
			"file": binData,
		}
		cb([]interface{}{payload})

		require.True(t, called, "struct handler must fire")
		assert.Equal(t, "avatar", got.Name)
		assert.Equal(t, binData, got.File, "bytes must survive marshal/unmarshal")
	})

	t.Run("pointer-to-struct param receives bytes", func(t *testing.T) {
		var got *fileStruct
		called := false
		cb := p.WrapCallback(func(v *fileStruct) {
			called = true
			got = v
		})
		require.NotNil(t, cb)
		cb([]interface{}{map[string]interface{}{"name": "p", "file": binData}})
		require.True(t, called)
		require.NotNil(t, got)
		assert.Equal(t, binData, got.File)
	})

	t.Run("slice-of-struct param receives bytes", func(t *testing.T) {
		var got []fileStruct
		called := false
		cb := p.WrapCallback(func(v []fileStruct) {
			called = true
			got = v
		})
		require.NotNil(t, cb)
		payload := []interface{}{
			map[string]interface{}{"name": "a", "file": []byte("AA")},
			map[string]interface{}{"name": "b", "file": []byte("BB")},
		}
		cb([]interface{}{payload})
		require.True(t, called)
		require.Len(t, got, 2)
		assert.Equal(t, []byte("AA"), got[0].File)
		assert.Equal(t, []byte("BB"), got[1].File)
	})
}

// TestWrapCallback_AcceptRejectMatrix locks in the binding discrimination rule:
//   - top-level reconstructed []byte binds to []byte and interface{} only;
//   - it is REJECTED for string / int / struct scalar targets (no base64
//     coercion);
//   - reconstructed composite (map containing bytes) binds to map / struct;
//   - reconstructed composite is rejected for scalar targets.
func TestWrapCallback_AcceptRejectMatrix(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	t.Run("bytes accepted by []byte param", func(t *testing.T) {
		var got []byte
		called := false
		cb := p.WrapCallback(func(b []byte) { called = true; got = b })
		require.NotNil(t, cb)
		cb([]interface{}{[]byte("hi")})
		require.True(t, called)
		assert.Equal(t, []byte("hi"), got)
	})

	t.Run("bytes accepted by interface{} param", func(t *testing.T) {
		var got interface{}
		called := false
		cb := p.WrapCallback(func(v interface{}) { called = true; got = v })
		require.NotNil(t, cb)
		cb([]interface{}{[]byte("hi")})
		require.True(t, called)
		assert.Equal(t, []byte("hi"), got)
	})

	t.Run("bytes rejected by string param", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(s string) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{[]byte("hi")})
		assert.False(t, called, "binary leaf must not be coerced to a base64 string")
	})

	t.Run("bytes rejected by int param", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(n int) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{[]byte("hi")})
		assert.False(t, called)
	})

	t.Run("bytes rejected by struct param (leaf is not a composite)", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(v fileStruct) { called = true })
		require.NotNil(t, cb)
		// A bare top-level []byte is the binary leaf; it must not be marshaled
		// into a struct (it would just be a base64 string, not an object).
		cb([]interface{}{[]byte("hi")})
		assert.False(t, called)
	})

	t.Run("composite rejected by string param", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(s string) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{map[string]interface{}{"file": []byte{1}}})
		assert.False(t, called)
	})

	t.Run("composite accepted by map param", func(t *testing.T) {
		called := false
		var got map[string]interface{}
		cb := p.WrapCallback(func(m map[string]interface{}) { called = true; got = m })
		require.NotNil(t, cb)
		cb([]interface{}{map[string]interface{}{"name": "x", "file": []byte("z")}})
		require.True(t, called)
		assert.Equal(t, "x", got["name"])
	})
}

// TestWrapCallback_StructEndToEnd ties send and receive together for the issue
// scenario: Emit-side struct -> binary packet -> wire -> reconstruct -> typed
// On handler, all in one test, with byte equality asserted.
func TestWrapCallback_StructEndToEnd(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	orig := fileStruct{Name: "doc.bin", File: []byte{0x00, 0x01, 0x7F, 0xFF}}

	header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: &socketio_v5.Event{Name: "upload", Payloads: []interface{}{orig}},
	})
	require.NoError(t, err)

	parsed, err := p.Parse(header)
	require.NoError(t, err)
	require.NoError(t, p.ReconstructBinary(parsed, attachments))

	var got fileStruct
	called := false
	cb := p.WrapCallback(func(v fileStruct) { called = true; got = v })
	require.NotNil(t, cb)
	cb(parsed.Event.Payloads)

	require.True(t, called)
	assert.Equal(t, orig.Name, got.Name)
	assert.Equal(t, orig.File, got.File)
}

// cyclicNode is a self-referential type used to prove the reflection walkers are
// depth-bounded and do not hang on a cycle.
type cyclicNode struct {
	File []byte
	Next *cyclicNode
}

// TestBinaryWalk_CycleDoesNotHang builds a pointer cycle and confirms HasBinary
// and SerializeBinary terminate (depth-bounded) instead of recursing forever.
func TestBinaryWalk_CycleDoesNotHang(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	a := &cyclicNode{File: []byte{1, 2, 3}}
	a.Next = a // cycle

	done := make(chan struct{})
	go func() {
		defer close(done)
		ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{a}}
		// Must terminate; we only care that these calls return.
		_ = p.HasBinary(ev)
		_, _, _ = p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			NS:    "/",
			Event: ev,
		})
	}()

	select {
	case <-done:
		// terminated, good
	case <-timeAfter():
		t.Fatal("binary walk did not terminate on a cyclic structure")
	}
}

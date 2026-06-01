package socketio_v5_parser_default

import (
	"bytes"
	"errors"
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

// valueHasBinary fast-path depth bound (Codex P2, binary.go:181): a
// self-referential map[string]interface{} / []interface{} is handed to HasBinary
// before encoding/json can reject the cycle, so the map/slice fast paths must be
// depth-bounded too — otherwise scanning for binary recurses forever and
// overflows the stack. Both shapes must terminate and report no binary.
func TestHasBinary_FastPathCycleTerminates(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	cyclicMap := map[string]interface{}{}
	cyclicMap["self"] = cyclicMap
	assert.False(t, parser.HasBinary(&socketio_v5.Event{
		Name:     "ev",
		Payloads: []interface{}{cyclicMap},
	}))

	cyclicSlice := make([]interface{}, 1)
	cyclicSlice[0] = cyclicSlice
	assert.False(t, parser.HasBinary(&socketio_v5.Event{
		Name:     "ev",
		Payloads: []interface{}{cyclicSlice},
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

// Direct unit tests of extractBinary's leaf cases that are awkward to trigger
// end-to-end: a binary-free typed nil pointer, and transformBinary's depth /
// invalid-value guards.
func TestExtractBinary_NilPointerLeaf(t *testing.T) {
	t.Parallel()
	var attachments [][]byte
	var nilPtr *[]byte
	// A typed nil pointer contains no binary, so extractBinary returns it as-is
	// (json renders it as null) with no attachment.
	out, err := extractBinary(nilPtr, &attachments)
	require.NoError(t, err)
	assert.Len(t, attachments, 0)
	assert.Nil(t, out)
}

func TestTransformBinary_DepthBound(t *testing.T) {
	t.Parallel()
	var attachments [][]byte
	sentinels := map[string]int{}
	// depth == 0 returns the value unchanged without recursing.
	out := transformBinary(reflect.ValueOf("x"), &attachments, sentinels, nil, 0)
	assert.Equal(t, "x", out.Interface())
	assert.Len(t, attachments, 0)
}

func TestTransformBinary_InvalidValue(t *testing.T) {
	t.Parallel()
	var attachments [][]byte
	sentinels := map[string]int{}
	// An invalid reflect.Value (the zero Value) is returned as-is, no panic.
	out := transformBinary(reflect.Value{}, &attachments, sentinels, nil, 5)
	assert.False(t, out.IsValid())
	assert.Len(t, attachments, 0)
}

// blobType is a named []byte alias, exercising sentinelSlice's type-preserving
// named-byte-slice branch.
type blobType []byte

// transformKinds drives the remaining transformBinary / isEmptyValue / hasOmitempty
// branches in one binary-bearing struct: a named []byte (Convert), an array of
// []byte (Array case), a map of []byte (Map case), omitempty fields of every
// emptiness kind (all skipped), a struct-typed omitempty field (isEmptyValue
// default -> not skipped), and a multi-option tag (hasOmitempty continuation).
type transformKinds struct {
	File  []byte            `json:"file"`
	Named blobType          `json:"named"`
	Arr   [2][]byte         `json:"arr"`
	M     map[string][]byte `json:"m"`

	S     string         `json:"s,omitempty"`
	B     bool           `json:"b,omitempty"`
	I     int            `json:"i,omitempty"`
	U     uint           `json:"u,omitempty"`
	F     float64        `json:"f,omitempty"`
	Sl    []int          `json:"sl,omitempty"`
	Mp    map[string]int `json:"mp,omitempty"`
	P     *int           `json:"p,omitempty"`
	If    interface{}    `json:"if,omitempty"`
	ArrO  [0]int         `json:"arro,omitempty"`
	Multi int            `json:"multi,string,omitempty"`

	Keep struct{ X int } `json:"keep,omitempty"` // struct kind: never "empty"
}

func TestSerializeBinary_TransformKindsCoverage(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	payload := transformKinds{
		File:  []byte("f"),
		Named: blobType("n"),
		Arr:   [2][]byte{[]byte("a0"), []byte("a1")},
		M:     map[string][]byte{"k": []byte("mv")},
		// all omitempty scalar/container fields left empty -> omitted
	}
	header, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
		Type: socketio_v5.PacketEvent, NS: "/", Event: &socketio_v5.Event{
			Name: "ev", Payloads: []interface{}{payload},
		},
	})
	require.NoError(t, err)
	// File + Named + Arr(2) + M(1) = 5 attachments.
	require.Len(t, attachments, 5)

	headerStr := string(header)
	// Empty omitempty fields are dropped; the struct-typed field is kept.
	assert.NotContains(t, headerStr, `"s"`)
	assert.NotContains(t, headerStr, `"multi"`)
	assert.Contains(t, headerStr, `"keep"`)
	assert.Contains(t, headerStr, `"_placeholder"`)
}

// extractBinary surfaces a json.Marshal error for a binary-bearing payload whose
// non-binary part is unencodable (a func field), covering the error branch.
func TestExtractBinary_MarshalError(t *testing.T) {
	t.Parallel()
	type withFunc struct {
		File []byte
		Fn   func() `json:"fn"`
	}
	var attachments [][]byte
	_, err := extractBinary(withFunc{File: []byte("x"), Fn: func() {}}, &attachments)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrParseBinary)
}

// reflectHasBinary: a typed map with NO binary must return false (the Map-case
// fall-through), and the reflective path must skip unexported struct fields
// (the PkgPath continue).
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

// A struct with an EXPORTED []byte reaches reflectExtractBinary's struct walk
// (the value contains binary), and its unexported sibling field exercises the
// PkgPath continue while the bytes still become an attachment.
func TestSerializeBinary_ExportedBinaryWithUnexportedField(t *testing.T) {
	t.Parallel()
	parser := NewParser(WithLogger(logger))

	_, attachments, err := parser.SerializeBinary(&socketio_v5.Message{
		Type: socketio_v5.PacketEvent,
		NS:   "/",
		Event: &socketio_v5.Event{
			Name:     "ev",
			Payloads: []interface{}{exportedBinaryWithUnexported{File: []byte("x"), secret: "s"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	assert.Equal(t, []byte("x"), attachments[0])
}

// extractBinary surfaces a failure of the sentinel-nonce random source as an
// ErrParseBinary rather than emitting predictable sentinels. randRead is swapped
// in a NON-parallel test so the override is fully restored before any t.Parallel()
// test resumes.
func TestExtractBinary_RandFailure(t *testing.T) {
	orig := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	defer func() { randRead = orig }()

	var attachments [][]byte
	_, err := extractBinary(uploadStruct{File: []byte{0x01}}, &attachments)
	assert.ErrorIs(t, err, ErrParseBinary)
}

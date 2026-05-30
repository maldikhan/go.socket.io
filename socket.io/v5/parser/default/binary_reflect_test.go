package socketio_v5_parser_default

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// TestReflectExtract_NilLeafAndPointers covers the nil []byte leaf (rendered as
// JSON null, no attachment), nil pointer/interface, and a non-nil pointer leaf.
func TestReflectExtract_NilLeafAndPointers(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	type withNilBytes struct {
		A []byte `json:"a"`
		B []byte `json:"b"`
	}

	// A is nil, B has data: only one attachment, A serialized as null.
	header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{withNilBytes{A: nil, B: []byte("x")}}},
	})
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	assert.Equal(t, []byte("x"), attachments[0])
	assert.Contains(t, string(header), `"a":null`)
	assert.Contains(t, string(header), `"b":{"_placeholder":true,"num":0}`)

	// Nil pointer field and nil interface field produce null, no attachments, and
	// HasBinary stays false when there is no actual byte data.
	type withNilPtr struct {
		P *fileStruct `json:"p"`
		I interface{} `json:"i"`
	}
	ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{withNilPtr{}}}
	assert.False(t, p.HasBinary(ev))
}

// TestReflectExtract_MapKeysSortedAndTyped covers map handling: string keys are
// emitted in sorted order and integer-keyed maps render keys as base-10 strings.
func TestReflectExtract_MapKeysSortedAndTyped(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	t.Run("string keyed typed map sorted", func(t *testing.T) {
		val := map[string][]byte{"b": []byte("B"), "a": []byte("A")}
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			NS:    "/",
			Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{val}},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 2)
		// Sorted: "a" -> num 0, "b" -> num 1.
		assert.Equal(t, []byte("A"), attachments[0])
		assert.Equal(t, []byte("B"), attachments[1])
		assert.Equal(t,
			`52-["ev",{"a":{"_placeholder":true,"num":0},"b":{"_placeholder":true,"num":1}}]`,
			string(header),
		)
	})

	t.Run("int keyed map renders numeric string keys", func(t *testing.T) {
		val := map[int][]byte{2: []byte("two"), 1: []byte("one")}
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			NS:    "/",
			Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{val}},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 2)
		assert.Equal(t, []byte("one"), attachments[0])
		assert.Equal(t, []byte("two"), attachments[1])
		assert.Contains(t, string(header), `"1":{"_placeholder":true,"num":0}`)
		assert.Contains(t, string(header), `"2":{"_placeholder":true,"num":1}`)
	})

	t.Run("uint keyed map renders numeric string keys", func(t *testing.T) {
		val := map[uint][]byte{5: []byte("five")}
		_, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			NS:    "/",
			Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{val}},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		assert.Equal(t, []byte("five"), attachments[0])
	})
}

// TestReflectExtract_ArrayAndScalar covers the array branch and the scalar
// default branch of the extractor.
func TestReflectExtract_ArrayAndScalar(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	type holder struct {
		Arr   [2][]byte `json:"arr"`
		Count int       `json:"count"`
	}
	val := holder{Arr: [2][]byte{[]byte("p"), []byte("q")}, Count: 9}
	header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{val}},
	})
	require.NoError(t, err)
	require.Len(t, attachments, 2)
	assert.Equal(t, []byte("p"), attachments[0])
	assert.Equal(t, []byte("q"), attachments[1])
	assert.Contains(t, string(header), `"count":9`)
}

// TestReflectExtract_Untagged covers the struct key-fallback path: a field with
// no json tag uses the Go field name as the key.
func TestReflectExtract_Untagged(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	type untagged struct {
		Blob []byte
	}
	header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{untagged{Blob: []byte("z")}}},
	})
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	assert.Contains(t, string(header), `"Blob":{"_placeholder":true,"num":0}`)
}

// unexpFirst places an unexported []byte field BEFORE the only exported byte
// field so both reflection walkers must traverse (and skip) the PkgPath branch
// before finding real binary data.
type unexpFirst struct {
	unexp []byte //nolint:unused // intentionally unexported, must be skipped
	Data  []byte `json:"data"`
}

// TestReflectExtract_UnexportedFieldSkipped covers the PkgPath skip branch in
// both reflectHasBinary and reflectExtractBinary: an unexported []byte field is
// ignored, matching encoding/json, even when it precedes the real binary field.
func TestReflectExtract_UnexportedFieldSkipped(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	val := unexpFirst{unexp: []byte("U"), Data: []byte("R")}
	ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{val}}
	// HasBinary must traverse past the unexported field to find Data.
	require.True(t, p.HasBinary(ev))

	header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: ev,
	})
	require.NoError(t, err)
	// Only the exported Data field yields an attachment; unexp is skipped.
	require.Len(t, attachments, 1)
	assert.Equal(t, []byte("R"), attachments[0])
	assert.NotContains(t, string(header), "unexp")
	assert.NotContains(t, string(header), `"U"`)
}

// TestParseJSONFieldTag_Direct exercises parseJSONFieldTag for the no-tag,
// empty-name, named, skip, and escape-hatch cases directly.
func TestParseJSONFieldTag_Direct(t *testing.T) {
	t.Parallel()

	type s struct {
		NoTag    int
		EmptyTag int `json:",omitempty"`
		Named    int `json:"named"`
		Dropped  int `json:"-"`
		DashName int `json:"-,"`
	}
	rt := reflect.TypeOf(s{})

	name, omit, skip := parseJSONFieldTag(rt.Field(0))
	assert.Equal(t, "", name)
	assert.False(t, omit)
	assert.False(t, skip)

	name, omit, skip = parseJSONFieldTag(rt.Field(1))
	assert.Equal(t, "", name)
	assert.True(t, omit)
	assert.False(t, skip)

	name, omit, skip = parseJSONFieldTag(rt.Field(2))
	assert.Equal(t, "named", name)
	assert.False(t, omit)
	assert.False(t, skip)

	_, _, skip = parseJSONFieldTag(rt.Field(3))
	assert.True(t, skip, "json:\"-\" must drop the field")

	// json:"-," is the escape hatch for a field literally named "-".
	name, _, skip = parseJSONFieldTag(rt.Field(4))
	assert.Equal(t, "-", name)
	assert.False(t, skip)
}

// TestIsEmptyValue_Direct covers every kind branch of isEmptyValue.
func TestIsEmptyValue_Direct(t *testing.T) {
	t.Parallel()

	assert.True(t, isEmptyValue(reflect.ValueOf("")))
	assert.False(t, isEmptyValue(reflect.ValueOf("x")))
	assert.True(t, isEmptyValue(reflect.ValueOf([]int{})))
	assert.True(t, isEmptyValue(reflect.ValueOf(map[string]int{})))
	assert.True(t, isEmptyValue(reflect.ValueOf([0]int{})))
	assert.True(t, isEmptyValue(reflect.ValueOf(false)))
	assert.False(t, isEmptyValue(reflect.ValueOf(true)))
	assert.True(t, isEmptyValue(reflect.ValueOf(int(0))))
	assert.False(t, isEmptyValue(reflect.ValueOf(int(1))))
	assert.True(t, isEmptyValue(reflect.ValueOf(uint(0))))
	assert.False(t, isEmptyValue(reflect.ValueOf(uint(2))))
	assert.True(t, isEmptyValue(reflect.ValueOf(float64(0))))
	assert.False(t, isEmptyValue(reflect.ValueOf(float64(1.5))))
	var nilPtr *int
	assert.True(t, isEmptyValue(reflect.ValueOf(nilPtr)))
	x := 3
	assert.False(t, isEmptyValue(reflect.ValueOf(&x)))
	// A kind with no specific branch (struct) returns false.
	assert.False(t, isEmptyValue(reflect.ValueOf(struct{}{})))
}

// TestMapKeyString_Direct covers the unsupported-key default returning "".
func TestMapKeyString_Direct(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hello", mapKeyString(reflect.ValueOf("hello")))
	assert.Equal(t, "-3", mapKeyString(reflect.ValueOf(int(-3))))
	assert.Equal(t, "7", mapKeyString(reflect.ValueOf(uint(7))))
	assert.Equal(t, "", mapKeyString(reflect.ValueOf(1.5)))
}

// TestReflectHelpers_InvalidValue covers the !IsValid early returns in both
// reflection walkers via a nil interface value, and a nil payload element.
func TestReflectHelpers_InvalidValue(t *testing.T) {
	t.Parallel()
	var invalid reflect.Value // zero Value, IsValid()==false
	assert.False(t, reflectHasBinary(invalid, 0))
	assert.Nil(t, reflectExtractBinary(invalid, &[][]byte{}, 0))

	// A nil payload reaches valueHasBinary/extractBinary's nil case.
	p := newBinaryParser()
	ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{nil}}
	assert.False(t, p.HasBinary(ev))
}

// TestReflectHelpers_DepthBound directly drives both walkers past the depth
// limit to cover the depth>maxBinaryWalkDepth guards deterministically.
func TestReflectHelpers_DepthBound(t *testing.T) {
	t.Parallel()
	v := reflect.ValueOf(fileStruct{File: []byte{1}})
	assert.False(t, reflectHasBinary(v, maxBinaryWalkDepth+1))
	assert.Nil(t, reflectExtractBinary(v, &[][]byte{}, maxBinaryWalkDepth+1))
}

// TestReflectHelpers_UnexportedSkipDirect calls both walkers directly on a
// struct whose ONLY []byte data lives in an unexported field, so the PkgPath
// skip branch is the deciding factor: HasBinary must return false and the
// extractor must emit no attachment, matching encoding/json which ignores
// unexported fields.
func TestReflectHelpers_UnexportedSkipDirect(t *testing.T) {
	t.Parallel()
	type onlyUnexported struct {
		secret []byte //nolint:unused // unexported []byte must be ignored
		Name   string `json:"name"`
	}
	v := reflect.ValueOf(onlyUnexported{secret: []byte("S"), Name: "n"})

	// reflectHasBinary must skip the unexported []byte and find no binary.
	assert.False(t, reflectHasBinary(v, 0))

	// reflectExtractBinary must skip it too: no attachment, name preserved.
	var attachments [][]byte
	out := reflectExtractBinary(v, &attachments, 0)
	require.Len(t, attachments, 0)
	m, ok := out.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "n", m["name"])
	_, hasSecret := m["secret"]
	assert.False(t, hasSecret, "unexported field must not appear in the output")
}

// TestReflectExtract_NilByteFieldInStruct covers the nil []byte leaf branch
// reached through a struct field (the inner `if v.IsNil()` of the byte-leaf
// case), producing JSON null and no attachment.
func TestReflectExtract_NilByteFieldInStruct(t *testing.T) {
	t.Parallel()
	type onlyNilBytes struct {
		Data []byte `json:"data"`
	}
	v := reflect.ValueOf(onlyNilBytes{Data: nil})

	// A []byte field is a binary leaf type regardless of nil-ness, so HasBinary
	// reports true; the extractor then renders the nil value as JSON null.
	assert.True(t, reflectHasBinary(v, 0))

	var attachments [][]byte
	out := reflectExtractBinary(v, &attachments, 0)
	require.Len(t, attachments, 0, "nil []byte must not allocate an attachment")
	m, ok := out.(map[string]interface{})
	require.True(t, ok)
	assert.Nil(t, m["data"], "nil []byte renders as null")
}

// TestReflectHasBinary_JSONDashSkip covers the `if skip { continue }` branch of
// reflectHasBinary (and the matching branch in reflectExtractBinary): a struct
// whose only []byte lives in a json:"-" field must report false and emit no
// attachment, exactly like encoding/json which drops the field.
func TestReflectHasBinary_JSONDashSkip(t *testing.T) {
	t.Parallel()
	type dashOnly struct {
		Hidden []byte `json:"-"`
		Name   string `json:"name"`
	}
	v := reflect.ValueOf(dashOnly{Hidden: []byte("X"), Name: "n"})
	assert.False(t, reflectHasBinary(v, 0), "json:\"-\" []byte must be skipped")

	var attachments [][]byte
	out := reflectExtractBinary(v, &attachments, 0)
	require.Len(t, attachments, 0)
	m, ok := out.(map[string]interface{})
	require.True(t, ok)
	_, hidden := m["Hidden"]
	assert.False(t, hidden)
	assert.Equal(t, "n", m["name"])
}

// TestReflectExtract_NilPtrAndInterface covers the `if v.IsNil()` branch of the
// reflectExtractBinary Ptr/Interface case: a nil pointer and a nil interface
// field both serialize to JSON null with no attachment, while a sibling pointer
// holding real binary is followed through.
func TestReflectExtract_NilPtrAndInterface(t *testing.T) {
	t.Parallel()
	type holder struct {
		NilPtr   *fileStruct `json:"nilptr"`
		NilIface interface{} `json:"niliface"`
		Live     *fileStruct `json:"live"`
	}
	v := reflect.ValueOf(holder{
		NilPtr:   nil,
		NilIface: nil,
		Live:     &fileStruct{Name: "k", File: []byte("F")},
	})

	var attachments [][]byte
	out := reflectExtractBinary(v, &attachments, 0)
	require.Len(t, attachments, 1)
	assert.Equal(t, []byte("F"), attachments[0])

	m, ok := out.(map[string]interface{})
	require.True(t, ok)
	assert.Nil(t, m["nilptr"], "nil pointer renders as null")
	assert.Nil(t, m["niliface"], "nil interface renders as null")
	live, ok := m["live"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "k", live["name"])
}

// TestExtractBinary_NilTopLevel covers the nil case of the dynamic extractBinary
// switch (a nil payload entry serializes to JSON null with no attachment).
func TestExtractBinary_NilTopLevel(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()
	header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type: socketio_v5.PacketEvent,
		NS:   "/",
		Event: &socketio_v5.Event{
			Name:     "ev",
			Payloads: []interface{}{nil, []byte("b")},
		},
	})
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	assert.Equal(t, []byte("b"), attachments[0])
	assert.Contains(t, string(header), `"ev",null,{"_placeholder":true,"num":0}]`)
}

// TestCanMarshalBind_Direct covers canMarshalBind for every category.
func TestCanMarshalBind_Direct(t *testing.T) {
	t.Parallel()
	assert.True(t, canMarshalBind(reflect.TypeOf(fileStruct{})))
	assert.True(t, canMarshalBind(reflect.TypeOf(&fileStruct{})))
	assert.True(t, canMarshalBind(reflect.TypeOf(map[string]int{})))
	assert.True(t, canMarshalBind(reflect.TypeOf([]int{})))
	assert.True(t, canMarshalBind(reflect.TypeOf([2]int{})))
	assert.False(t, canMarshalBind(reflect.TypeOf("")))
	assert.False(t, canMarshalBind(reflect.TypeOf(0)))
}

// TestWrapCallback_MarshalBindError covers the json.Unmarshal error path of the
// marshal-bind branch: a reconstructed map whose shape is incompatible with the
// target struct field type must be rejected, not delivered.
func TestWrapCallback_MarshalBindError(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	type strictStruct struct {
		File []byte `json:"file"`
	}
	called := false
	cb := p.WrapCallback(func(v strictStruct) { called = true })
	require.NotNil(t, cb)
	// "file" holds a nested object that cannot unmarshal into []byte.
	cb([]interface{}{map[string]interface{}{"file": map[string]interface{}{"nested": []byte{1}}}})
	assert.False(t, called, "incompatible composite must be rejected on unmarshal error")
}

// TestWrapCallback_MarshalError covers the json.Marshal error path of the
// marshal-bind branch: a reconstructed slice carrying an unmarshalable value
// (a channel) targeting a slice parameter must be rejected.
func TestWrapCallback_MarshalError(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	called := false
	cb := p.WrapCallback(func(v []interface{}) { called = true })
	require.NotNil(t, cb)
	// []interface{} is assignable to []interface{} directly, so to force the
	// marshal-bind path we target a typed slice that is not directly assignable.
	cb2 := p.WrapCallback(func(v []int) { called = true })
	require.NotNil(t, cb2)
	// A slice containing a channel cannot be JSON-marshaled.
	cb2([]interface{}{[]interface{}{make(chan int)}})
	assert.False(t, called, "unmarshalable composite must be rejected on marshal error")
}

// TestWrapCallback_InvalidArg covers the !IsValid branch: a nil interface
// element bound to a non-interface parameter is rejected.
func TestWrapCallback_InvalidArg(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()
	called := false
	cb := p.WrapCallback(func(s string) { called = true })
	require.NotNil(t, cb)
	cb([]interface{}{nil})
	assert.False(t, called, "nil (invalid) value must be rejected for a string param")
}

// TestWrapCallback_NamedByteSliceParam confirms a named []byte type still
// round-trips through a struct field of the named type.
func TestWrapCallback_NamedByteSliceParam(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	type holder struct {
		Data namedBytes `json:"data"`
	}
	var got holder
	called := false
	cb := p.WrapCallback(func(v holder) { called = true; got = v })
	require.NotNil(t, cb)
	cb([]interface{}{map[string]interface{}{"data": []byte("nb")}})
	require.True(t, called)
	assert.Equal(t, namedBytes("nb"), got.Data)
}

// TestWrapCallback_RawMessageStillWorks guards the unchanged json.RawMessage
// path: a normal JSON arg still unmarshals into a struct.
func TestWrapCallback_RawMessageStillWorks(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	var got fileStruct
	called := false
	cb := p.WrapCallback(func(v fileStruct) { called = true; got = v })
	require.NotNil(t, cb)
	// base64 of bytes in JSON unmarshals into []byte natively.
	raw := json.RawMessage(`{"name":"n","file":"YWJj"}`) // "abc"
	cb([]interface{}{raw})
	require.True(t, called)
	assert.Equal(t, "n", got.Name)
	assert.Equal(t, []byte("abc"), got.File)
}

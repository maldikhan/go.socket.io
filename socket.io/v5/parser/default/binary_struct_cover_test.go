package socketio_v5_parser_default

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// TestValueHasBinary_Nil covers the explicit nil guard.
func TestValueHasBinary_Nil(t *testing.T) {
	t.Parallel()
	assert.False(t, valueHasBinary(nil))
}

// TestReflectHasBinary_SkipTaggedField ensures a json:"-" byte field is skipped
// while a sibling exported byte field is still detected (struct-case coverage).
func TestReflectHasBinary_SkipTaggedField(t *testing.T) {
	t.Parallel()
	type mixed struct {
		Hidden []byte `json:"-"`
		Real   []byte `json:"real"`
	}
	assert.True(t, reflectHasBinary(reflect.ValueOf(mixed{Hidden: []byte{1}, Real: []byte{2}}), binaryWalkDepth))

	type onlyHidden struct {
		Hidden []byte `json:"-"`
	}
	assert.False(t, reflectHasBinary(reflect.ValueOf(onlyHidden{Hidden: []byte{1}}), binaryWalkDepth))
}

// TestJSONFieldName covers all tag-name branches.
func TestJSONFieldName(t *testing.T) {
	t.Parallel()
	type s struct {
		Untagged string
		Named    string `json:"renamed"`
		Skip     string `json:"-"`
		DashName string `json:"-,"`
		EmptyTag string `json:","`
		OptsOnly string `json:",omitempty"`
	}
	rt := reflect.TypeOf(s{})
	cases := map[string]string{
		"Untagged": "Untagged",
		"Named":    "renamed",
		"Skip":     "",
		"DashName": "-",
		"EmptyTag": "EmptyTag",
		"OptsOnly": "OptsOnly",
	}
	for field, want := range cases {
		f, ok := rt.FieldByName(field)
		require.True(t, ok)
		assert.Equal(t, want, jsonFieldName(f), "field %s", field)
	}
}

// TestJSONFieldOmitEmpty covers the omitempty detection branches.
func TestJSONFieldOmitEmpty(t *testing.T) {
	t.Parallel()
	type s struct {
		None     string
		Tagged   string `json:"tagged"`
		Omit     string `json:"omit,omitempty"`
		OmitOnly string `json:",omitempty"`
		Other    string `json:"other,string"`
	}
	rt := reflect.TypeOf(s{})
	cases := map[string]bool{
		"None":     false,
		"Tagged":   false,
		"Omit":     true,
		"OmitOnly": true,
		"Other":    false,
	}
	for field, want := range cases {
		f, ok := rt.FieldByName(field)
		require.True(t, ok)
		assert.Equal(t, want, jsonFieldOmitEmpty(f), "field %s", field)
	}
}

// TestIsEmptyValue covers every kind arm of isEmptyValue.
func TestIsEmptyValue(t *testing.T) {
	t.Parallel()
	var nilPtr *int
	x := 5
	cases := []struct {
		v    interface{}
		want bool
	}{
		{"", true},
		{"x", false},
		{[]int{}, true},
		{[]int{1}, false},
		{map[string]int{}, true},
		{[0]int{}, true},
		{false, true},
		{true, false},
		{0, true},
		{1, false},
		{uint(0), true},
		{uint(2), false},
		{0.0, true},
		{1.5, false},
		{nilPtr, true},
		{&x, false},
		{struct{}{}, false}, // default arm
	}
	for _, c := range cases {
		assert.Equal(t, c.want, isEmptyValue(reflect.ValueOf(c.v)), "%v", c.v)
	}
	// invalid value falls through to default false.
	assert.False(t, isEmptyValue(reflect.Value{}))
}

// TestExtractBinary_Nil covers the nil guard of extractBinary.
func TestExtractBinary_Nil(t *testing.T) {
	t.Parallel()
	var att [][]byte
	assert.Nil(t, extractBinary(nil, &att))
	assert.Empty(t, att)
}

// TestReflectExtractBinary_Edges covers the invalid, nil-pointer and default
// branches of reflectExtractBinary.
func TestReflectExtractBinary_Edges(t *testing.T) {
	t.Parallel()
	var att [][]byte

	// invalid value
	assert.Nil(t, reflectExtractBinary(reflect.Value{}, &att, binaryWalkDepth))

	// nil pointer inside a binary-bearing struct: the field carries no binary,
	// so the no-binary fast path returns it as a typed nil interface value.
	type withPtr struct {
		P    *int   `json:"p"`
		File []byte `json:"file"`
	}
	out := reflectExtractBinary(reflect.ValueOf(withPtr{File: []byte{1}}), &att, binaryWalkDepth)
	m, ok := out.(map[string]interface{})
	require.True(t, ok)
	assert.Nil(t, m["p"])

	// A non-nil pointer that itself points at binary exercises the Ptr branch
	// of the rebuild path (depth from interface unwrap).
	att = nil
	blob := []byte("ptrblob")
	type holder struct {
		PB *[]byte `json:"pb"`
	}
	out = reflectExtractBinary(reflect.ValueOf(holder{PB: &blob}), &att, binaryWalkDepth)
	m, ok = out.(map[string]interface{})
	require.True(t, ok)
	ph, ok := m["pb"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, ph["_placeholder"])
	require.Len(t, att, 1)
	assert.Equal(t, blob, att[0])

	// default branch: a binary-bearing map whose value is a non-composite,
	// non-byte scalar reached after the map carries binary elsewhere.
	att = nil
	val := map[string]interface{}{"n": 42, "f": []byte("x")}
	out = reflectExtractBinary(reflect.ValueOf(val), &att, binaryWalkDepth)
	m, ok = out.(map[string]interface{})
	require.True(t, ok)
	assert.EqualValues(t, 42, m["n"])
}

// TestToByteSlice_Array covers the byte-array copy path.
func TestToByteSlice_Array(t *testing.T) {
	t.Parallel()
	arr := [4]byte{1, 2, 3, 4}
	assert.Equal(t, []byte{1, 2, 3, 4}, toByteSlice(reflect.ValueOf(arr)))
	assert.Equal(t, []byte{9}, toByteSlice(reflect.ValueOf([]byte{9})))
}

// TestMapKeyString covers string, int, uint and default key kinds.
func TestMapKeyString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "key", mapKeyString(reflect.ValueOf("key")))
	assert.Equal(t, "-7", mapKeyString(reflect.ValueOf(int64(-7))))
	assert.Equal(t, "12", mapKeyString(reflect.ValueOf(uint16(12))))
	assert.Equal(t, "true", mapKeyString(reflect.ValueOf(true)))
}

// TestSerializeBinary_IntKeyMap proves integer map keys serialize and round the
// extractor's mapKeyString path with a real binary value present.
func TestSerializeBinary_IntKeyMap(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()
	_, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{map[int][]byte{3: []byte("v")}}},
	})
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	assert.Equal(t, []byte("v"), attachments[0])
}

// TestIsConvertibleTarget covers the pointer-deref loop and each kind arm.
func TestIsConvertibleTarget(t *testing.T) {
	t.Parallel()
	type s struct{}
	assert.True(t, isConvertibleTarget(reflect.TypeOf(s{})))
	assert.True(t, isConvertibleTarget(reflect.TypeOf(&s{})))         // pointer deref
	assert.True(t, isConvertibleTarget(reflect.TypeOf((**s)(nil))))   // double pointer
	assert.True(t, isConvertibleTarget(reflect.TypeOf(map[string]int{})))
	assert.True(t, isConvertibleTarget(reflect.TypeOf([]int{})))
	assert.True(t, isConvertibleTarget(reflect.TypeOf([2]int{})))
	assert.False(t, isConvertibleTarget(reflect.TypeOf("")))
	assert.False(t, isConvertibleTarget(reflect.TypeOf(0)))
}

// TestWrapCallback_MarshalError exercises the json.Marshal failure path of the
// convert branch by handing a composite value that cannot be marshaled.
func TestWrapCallback_MarshalError(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()
	called := false
	cb := p.WrapCallback(func(m map[string]interface{}) { called = true })
	require.NotNil(t, cb)
	// A slice carrying an unmarshalable element (a channel) reaches the convert
	// branch (target is a map, value is []interface{}, not assignable) and fails
	// json.Marshal, so the handler must be skipped.
	cb([]interface{}{[]interface{}{make(chan int)}})
	assert.False(t, called, "marshal failure must skip the handler")
}

package socketio_v5_parser_default

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// extractCover exercises the reflective branches of extractBinary /
// reflectExtractBinary and the helper functions to full coverage.
func TestExtractBinary_ReflectBranches(t *testing.T) {
	t.Parallel()

	serialize := func(payload interface{}) ([]byte, [][]byte, error) {
		p := newBinaryParser()
		return p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			NS:    "/",
			Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{payload}},
		})
	}

	t.Run("nil interface payload", func(t *testing.T) {
		header, attachments, err := serialize(nil)
		require.NoError(t, err)
		require.Len(t, attachments, 0)
		arr := decodeHeaderArray(t, header, 0)
		assert.Nil(t, arr[1])
	})

	t.Run("pointer to non-binary struct preserved", func(t *testing.T) {
		type plain struct {
			Name string `json:"name"`
		}
		// No binary anywhere: extractBinary still produces a header (count 0)
		// and preserves the field.
		header, attachments, err := serialize(&plain{Name: "n"})
		require.NoError(t, err)
		require.Len(t, attachments, 0)
		arr := decodeHeaderArray(t, header, 0)
		obj := arr[1].(map[string]interface{})
		assert.Equal(t, "n", obj["name"])
	})

	t.Run("nil pointer field becomes null", func(t *testing.T) {
		type holder struct {
			Blob []byte       `json:"blob"`
			Ptr  *innerStruct `json:"ptr"`
		}
		header, attachments, err := serialize(holder{Blob: []byte("b")})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		arr := decodeHeaderArray(t, header, 1)
		obj := arr[1].(map[string]interface{})
		assert.Nil(t, obj["ptr"])
	})

	t.Run("nil interface field becomes null", func(t *testing.T) {
		type holder struct {
			Blob []byte      `json:"blob"`
			Any  interface{} `json:"any"`
		}
		header, _, err := serialize(holder{Blob: []byte("b")})
		require.NoError(t, err)
		arr := decodeHeaderArray(t, header, 1)
		obj := arr[1].(map[string]interface{})
		assert.Nil(t, obj["any"])
	})

	t.Run("non-byte slice with binary element", func(t *testing.T) {
		// A []interface{}-typed field carrying binary exercises the slice loop
		// in the reflective extractor.
		type holder struct {
			Items []interface{} `json:"items"`
		}
		header, attachments, err := serialize(holder{Items: []interface{}{"x", []byte("y")}})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		assert.Equal(t, []byte("y"), attachments[0])
		arr := decodeHeaderArray(t, header, 1)
		obj := arr[1].(map[string]interface{})
		items := obj["items"].([]interface{})
		assert.Equal(t, "x", items[0])
		assert.Equal(t, true, items[1].(map[string]interface{})["_placeholder"])
	})

	t.Run("typed non-byte array with binary element", func(t *testing.T) {
		type holder struct {
			Arr [2]interface{} `json:"arr"`
		}
		header, attachments, err := serialize(holder{Arr: [2]interface{}{"a", []byte("b")}})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		arr := decodeHeaderArray(t, header, 1)
		obj := arr[1].(map[string]interface{})
		got := obj["arr"].([]interface{})
		assert.Equal(t, "a", got[0])
		assert.Equal(t, true, got[1].(map[string]interface{})["_placeholder"])
	})

	t.Run("byte array field is a number list not a placeholder", func(t *testing.T) {
		type holder struct {
			Blob []byte  `json:"blob"`
			Arr  [3]byte `json:"arr"`
		}
		header, attachments, err := serialize(holder{Blob: []byte("b"), Arr: [3]byte{1, 2, 3}})
		require.NoError(t, err)
		require.Len(t, attachments, 1) // only Blob is an attachment
		arr := decodeHeaderArray(t, header, 1)
		obj := arr[1].(map[string]interface{})
		assert.Equal(t, []interface{}{float64(1), float64(2), float64(3)}, obj["arr"])
	})

	t.Run("numeric map key with binary value", func(t *testing.T) {
		header, attachments, err := serialize(map[int][]byte{7: []byte("v")})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		arr := decodeHeaderArray(t, header, 1)
		obj := arr[1].(map[string]interface{})
		ph, ok := obj["7"].(map[string]interface{})
		require.True(t, ok, "numeric map key must serialize as its base-10 text")
		assert.Equal(t, true, ph["_placeholder"])
	})

	t.Run("omitempty across kinds", func(t *testing.T) {
		type holder struct {
			Blob    []byte         `json:"blob"`
			Zero    int            `json:"zero,omitempty"`
			NonZero int            `json:"nonzero,omitempty"`
			Flag    bool           `json:"flag,omitempty"`
			Uns     uint           `json:"uns,omitempty"`
			F       float64        `json:"f,omitempty"`
			S       []string       `json:"s,omitempty"`
			M       map[string]int `json:"m,omitempty"`
			Ptr     *int           `json:"ptr,omitempty"`
		}
		header, _, err := serialize(holder{Blob: []byte("b"), NonZero: 5})
		require.NoError(t, err)
		arr := decodeHeaderArray(t, header, 1)
		obj := arr[1].(map[string]interface{})
		// Only blob placeholder and nonzero survive.
		assert.Equal(t, float64(5), obj["nonzero"])
		for _, k := range []string{"zero", "flag", "uns", "f", "s", "m", "ptr"} {
			_, ok := obj[k]
			assert.False(t, ok, "omitempty empty field %q must be omitted", k)
		}
	})

	t.Run("named byte slice top level", func(t *testing.T) {
		header, attachments, err := serialize(namedByteSlice("nb"))
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		assert.Equal(t, []byte("nb"), attachments[0])
		arr := decodeHeaderArray(t, header, 1)
		assert.Equal(t, true, arr[1].(map[string]interface{})["_placeholder"])
	})

	t.Run("scalar struct field passes through", func(t *testing.T) {
		type holder struct {
			Blob []byte `json:"blob"`
			N    int    `json:"n"`
		}
		header, _, err := serialize(holder{Blob: []byte("b"), N: 9})
		require.NoError(t, err)
		arr := decodeHeaderArray(t, header, 1)
		assert.Equal(t, float64(9), arr[1].(map[string]interface{})["n"])
	})
}

// TestExtractBinary_CycleDepthGuard proves the depth guard terminates a cyclic
// structure during extraction (SerializeBinary) rather than hanging.
func TestExtractBinary_CycleDepthGuard(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	a := &cyclicNode{Data: []byte("a")}
	a.Next = a // self cycle

	done := make(chan struct{}, 1)
	go func() {
		// The depth guard guarantees the reflective walk terminates; the
		// resulting structure still contains a cycle, so json.Marshal in the
		// header serializer reports an error rather than hanging. Either way the
		// call must RETURN (not hang); that is what we assert.
		_, _, _ = p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			NS:    "/",
			Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{a}},
		})
		done <- struct{}{}
	}()
	select {
	case <-done:
		// returned without hanging: success
	case <-time.After(5 * time.Second):
		t.Fatal("SerializeBinary hung on a cyclic structure")
	}
}

// TestJSONFieldName covers the json-tag name resolution edge cases.
func TestSerialize_JSONTagNameVariants(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	type holder struct {
		Blob    []byte `json:"blob"`
		Renamed string `json:",omitempty"` // empty name before comma -> field name
		Dash    string `json:"-,"`         // literal "-" key
		Plain   string // no tag -> field name
	}
	header, _, err := p.SerializeBinary(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{holder{Blob: []byte("b"), Renamed: "r", Dash: "d", Plain: "p"}}},
	})
	require.NoError(t, err)
	arr := decodeHeaderArray(t, header, 1)
	obj := arr[1].(map[string]interface{})
	assert.Equal(t, "r", obj["Renamed"], "empty json name keeps the field name")
	assert.Equal(t, "d", obj["-"], `json:"-," names the key "-"`)
	assert.Equal(t, "p", obj["Plain"], "untagged field keeps its name")
}

// TestConvertViaJSON_MarshalError exercises the marshal-error path of the
// RECEIVE conversion: a composite value that cannot be marshaled is rejected.
func TestConvertViaJSON_Errors(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	t.Run("unmarshalable into target rejects", func(t *testing.T) {
		called := false
		// A map cannot unmarshal into an int parameter.
		cb := p.WrapCallback(func(n int) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{map[string]interface{}{"a": 1}})
		assert.False(t, called)
	})

	t.Run("unmarshalable composite (chan in slice) rejects", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(v uploadStruct) { called = true })
		require.NotNil(t, cb)
		// A slice containing a channel cannot be JSON-marshaled.
		cb([]interface{}{[]interface{}{make(chan int)}})
		assert.False(t, called, "unmarshalable composite must be rejected")
	})

	t.Run("array payload converts", func(t *testing.T) {
		called := false
		var got [2]int
		cb := p.WrapCallback(func(v [2]int) {
			called = true
			got = v
		})
		require.NotNil(t, cb)
		cb([]interface{}{[2]int{1, 2}})
		require.True(t, called)
		assert.Equal(t, [2]int{1, 2}, got)
	})

	t.Run("pointer composite converts", func(t *testing.T) {
		called := false
		var got *uploadStruct
		cb := p.WrapCallback(func(v *uploadStruct) {
			called = true
			got = v
		})
		require.NotNil(t, cb)
		cb([]interface{}{&uploadStruct{File: []byte("x"), Name: "n"}})
		require.True(t, called)
		require.NotNil(t, got)
		assert.Equal(t, []byte("x"), got.File)
	})

	t.Run("scalar non-raw value rejected", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(s string) { called = true })
		require.NotNil(t, cb)
		// A bare int is neither raw JSON nor a convertible composite nor
		// assignable to string -> rejected.
		cb([]interface{}{42})
		assert.False(t, called)
	})

	t.Run("nil entry rejected", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(s string) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{nil})
		assert.False(t, called)
	})
}

// jsonRoundTripParity asserts that for a struct WITHOUT binary, the header JSON
// for non-byte fields matches exactly what encoding/json would produce.
func TestSerialize_HeaderParityWithEncodingJSON(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	type doc struct {
		File  []byte `json:"file"`
		Title string `json:"title"`
		Pages int    `json:"pages"`
		Tags  []string
	}
	d := doc{File: []byte("PDF"), Title: "T", Pages: 3, Tags: []string{"a", "b"}}

	header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{d}},
	})
	require.NoError(t, err)
	require.Len(t, attachments, 1)

	arr := decodeHeaderArray(t, header, 1)
	obj := arr[1].(map[string]interface{})

	// Build expected from encoding/json with the byte field swapped for a
	// placeholder marker.
	want := map[string]interface{}{
		"file":  map[string]interface{}{"_placeholder": true, "num": float64(0)},
		"title": "T",
		"pages": float64(3),
		"Tags":  []interface{}{"a", "b"},
	}
	assert.Equal(t, want, obj)

	// Sanity: marshaling the same struct (without binary handling) yields the
	// same non-byte field shape (title/pages/Tags).
	plain, _ := json.Marshal(d)
	var pm map[string]interface{}
	require.NoError(t, json.Unmarshal(plain, &pm))
	assert.Equal(t, pm["title"], obj["title"])
	assert.Equal(t, pm["pages"], obj["pages"])
	assert.Equal(t, pm["Tags"], obj["Tags"])
}

// TestConvertViaJSON_Direct exercises convertViaJSON's guard and marshal-error
// branches directly (white-box); the WrapCallback flow reaches them only for
// edge inputs.
func TestConvertViaJSON_Direct(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()
	strType := reflect.TypeOf("")
	structType := reflect.TypeOf(uploadStruct{})

	// Non-composite scalar -> isConvertibleComposite false -> early return.
	if _, ok := p.convertViaJSON(42, strType); ok {
		t.Fatal("scalar must not convert")
	}
	// Composite that cannot be JSON-marshaled -> marshal-error branch.
	if _, ok := p.convertViaJSON([]interface{}{make(chan int)}, structType); ok {
		t.Fatal("unmarshalable composite must not convert")
	}
	// Composite that unmarshals into the target.
	v, ok := p.convertViaJSON(map[string]interface{}{"file": []byte("z"), "name": "n"}, structType)
	require.True(t, ok, "valid composite must convert")
	us := v.Interface().(uploadStruct)
	assert.Equal(t, []byte("z"), us.File)
	assert.Equal(t, "n", us.Name)
}

// TestReflectExtract_Direct exercises the depth guard, unexported-field skip and
// the isEmptyValue interface/ptr and default branches directly.
func TestReflectExtract_Direct(t *testing.T) {
	t.Parallel()

	var atts [][]byte

	// Depth guard: a valid value beyond max depth is returned unchanged.
	got := reflectExtractBinary(reflect.ValueOf(5), &atts, maxBinaryWalkDepth+1)
	assert.Equal(t, 5, got)

	// Unexported struct field is skipped.
	type withUnexported struct {
		Blob   []byte `json:"blob"`
		hidden int    //nolint:unused
	}
	out := reflectExtractBinary(reflect.ValueOf(withUnexported{Blob: []byte("b"), hidden: 7}), &atts, 0)
	m := out.(map[string]interface{})
	_, hasHidden := m["hidden"]
	assert.False(t, hasHidden, "unexported field must be skipped")

	// isEmptyValue: nil ptr and nil interface are empty (ptr/iface branch); a
	// struct value falls through to the default false.
	var nilPtr *int
	assert.True(t, isEmptyValue(reflect.ValueOf(nilPtr)))
	ifaceField := reflect.ValueOf(struct{ X interface{} }{}).Field(0)
	assert.True(t, isEmptyValue(ifaceField))
	assert.False(t, isEmptyValue(reflect.ValueOf(struct{ A int }{A: 1})))
}

// TestHasBinary_ReflectCoverage targets the remaining reflective branches of
// valueHasBinary / reflectHasBinary for full coverage.
func TestHasBinary_ReflectCoverage(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	t.Run("nil top-level payload", func(t *testing.T) {
		// valueHasBinary nil short-circuit.
		ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{nil}}
		assert.False(t, p.HasBinary(ev))
	})

	t.Run("interface element wrapping struct with binary", func(t *testing.T) {
		// A []interface{} element is reflect.Interface kind, exercising the
		// Interface case which recurses into the concrete struct.
		ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{
			[]interface{}{nil, uploadStruct{File: []byte("x")}},
		}}
		assert.True(t, p.HasBinary(ev))
	})

	t.Run("array of structs carrying binary", func(t *testing.T) {
		// A non-byte array whose elements carry binary exercises the Array loop.
		ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{
			[2]uploadStruct{{Name: "a"}, {File: []byte("b")}},
		}}
		assert.True(t, p.HasBinary(ev))
	})

	t.Run("array of structs without binary", func(t *testing.T) {
		ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{
			[2]struct{ Name string }{{Name: "a"}, {Name: "b"}},
		}}
		assert.False(t, p.HasBinary(ev))
	})

	t.Run("invalid reflect value via nil interface field", func(t *testing.T) {
		// A struct with a nil interface field reaches reflectHasBinary on an
		// interface kind whose IsNil is true (the invalid/nil guards).
		type holder struct {
			Any interface{}
		}
		ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{holder{}}}
		assert.False(t, p.HasBinary(ev))
	})
}

// TestReflectExtract_DepthAndDefault targets the residual return-nil branches of
// reflectExtractBinary: the depth/invalid guard returning nil for an invalid
// value, and the default branch on an invalid value.
func TestReflectExtract_DepthAndDefault(t *testing.T) {
	t.Parallel()

	var atts [][]byte

	// Invalid value at/over the depth limit -> guard returns nil.
	assert.Nil(t, reflectExtractBinary(reflect.Value{}, &atts, maxBinaryWalkDepth+1))

	// Invalid value within depth -> falls through (IsValid false) and returns nil.
	assert.Nil(t, reflectExtractBinary(reflect.Value{}, &atts, 0))

	// A nil interface field within a struct serializes to nil (Interface IsNil).
	type holder struct {
		Blob []byte      `json:"blob"`
		Any  interface{} `json:"any"`
	}
	out := reflectExtractBinary(reflect.ValueOf(holder{Blob: []byte("b")}), &atts, 0)
	m := out.(map[string]interface{})
	assert.Nil(t, m["any"])
}

// TestReflect_RemainingBranches drives the few defensive/edge branches in the
// reflective walkers directly so the package reaches full statement coverage.
func TestReflect_RemainingBranches(t *testing.T) {
	t.Parallel()

	// reflectHasBinary: invalid reflect.Value -> false (the !IsValid guard).
	assert.False(t, reflectHasBinary(reflect.Value{}, 0))

	// reflectHasBinary: struct with an unexported field and a json:"-" field,
	// neither contributing binary -> exercises the PkgPath and jsonFieldSkip
	// continue branches; the exported []byte still makes it true.
	type mixed struct {
		Blob   []byte `json:"blob"`
		Hidden string `json:"hidden"`
		hidden int    //nolint:unused
		Skip   []byte `json:"-"`
	}
	assert.True(t, reflectHasBinary(reflect.ValueOf(mixed{Blob: []byte("b"), Skip: []byte("ignored")}), 0))

	// reflectHasBinary: same struct shape but without exported binary; the
	// json:"-" []byte must NOT be counted as binary (skip branch), so false.
	type mixedNoBin struct {
		Name   string `json:"name"`
		hidden int    //nolint:unused
		Skip   []byte `json:"-"`
	}
	assert.False(t, reflectHasBinary(reflect.ValueOf(mixedNoBin{Name: "n", Skip: []byte("ignored")}), 0))

	// reflectExtractBinary: an unexported field's value is not addressable for
	// Interface(); fetched via Field on an unexported field it has CanInterface
	// == false. Reaching the default branch with such a value returns nil.
	type holder struct {
		x chan int //nolint:unused
	}
	hv := reflect.ValueOf(holder{}).Field(0) // unexported -> CanInterface() false
	require.False(t, hv.CanInterface())
	var atts [][]byte
	assert.Nil(t, reflectExtractBinary(hv, &atts, 0))
}

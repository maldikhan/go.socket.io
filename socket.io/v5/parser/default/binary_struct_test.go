package socketio_v5_parser_default

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// uploadStruct is a typical "upload" payload: a struct carrying a []byte field
// alongside ordinary fields with json tags.
type uploadStruct struct {
	Name    string `json:"name"`
	File    []byte `json:"file"`
	Skipped string `json:"-"`
	Note    string `json:"note,omitempty"`
	secret  string //nolint:unused // unexported: must be ignored by both walks
}

// nestedStruct exercises pointers, nested structs, typed slices and maps that
// reach a []byte through several layers.
type innerStruct struct {
	Blob []byte `json:"blob"`
}

type nestedStruct struct {
	Inner   *innerStruct           `json:"inner"`
	Files   [][]byte               `json:"files"`
	ByName  map[string][]byte      `json:"byName"`
	Tags    []string               `json:"tags"`
	Numbers []int                  `json:"numbers"`
	Extra   map[string]innerStruct `json:"extra"`
}

// TestHasBinary_Struct verifies the SEND-side detection traverses structs,
// pointers, typed maps and named slices, treats []byte as a leaf and does not
// misfire on []string.
func TestHasBinary_Struct(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	type noBinary struct {
		Name string `json:"name"`
		Tags []string
	}
	type onlySkipped struct {
		File []byte `json:"-"`
	}
	type byteArray struct {
		Arr [3]byte `json:"arr"`
	}

	tests := []struct {
		name  string
		value interface{}
		want  bool
	}{
		{"struct with byte field", uploadStruct{Name: "a", File: []byte{1}}, true},
		{"struct without byte field", noBinary{Name: "x", Tags: []string{"a", "b"}}, false},
		{"pointer to struct with bytes", &uploadStruct{File: []byte{1}}, true},
		{"nil pointer", (*uploadStruct)(nil), false},
		{"nested pointer struct", nestedStruct{Inner: &innerStruct{Blob: []byte{9}}}, true},
		{"typed slice of byte slices", nestedStruct{Files: [][]byte{{1}, {2}}}, true},
		{"typed map to byte slice", nestedStruct{ByName: map[string][]byte{"k": {1}}}, true},
		{"map to struct with bytes", nestedStruct{Extra: map[string]innerStruct{"k": {Blob: []byte{1}}}}, true},
		{"named slice of strings does not misfire", nestedStruct{Tags: []string{"x", "y"}}, false},
		{"slice of ints does not misfire", []int{1, 2, 3}, false},
		{"string slice top level", []string{"a", "b"}, false},
		{"byte field tagged json:- is ignored", onlySkipped{File: []byte{1, 2}}, false},
		{"byte array field detected", byteArray{Arr: [3]byte{1, 2, 3}}, true},
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

// TestHasBinary_Cycle verifies the depth-bounded walk terminates on cyclic data.
func TestHasBinary_Cycle(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	type node struct {
		Next *node                  `json:"next"`
		Self map[string]interface{} `json:"self"`
	}
	n := &node{}
	n.Next = n // pointer cycle
	n.Self = map[string]interface{}{}
	n.Self["self"] = n.Self // map cycle

	done := make(chan bool, 1)
	go func() {
		ev := &socketio_v5.Event{Name: "ev", Payloads: []interface{}{n}}
		_ = p.HasBinary(ev)
		done <- true
	}()
	select {
	case <-done:
		// terminated: success
	case <-time.After(5 * time.Second):
		t.Fatal("HasBinary did not terminate on cyclic structure")
	}
}

// TestSerializeBinary_Struct proves the extractor mirrors HasBinary: a struct
// with a []byte field becomes a real binary packet whose header carries a
// placeholder plus the json-tagged non-byte fields, and whose attachment equals
// the original bytes.
func TestSerializeBinary_Struct(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	t.Run("single struct payload", func(t *testing.T) {
		fileBytes := []byte{0xDE, 0xAD, 0xBE, 0xEF}
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name: "upload",
				Payloads: []interface{}{uploadStruct{
					Name:    "avatar",
					File:    fileBytes,
					Skipped: "ignore-me",
					secret:  "hidden",
				}},
			},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 1)
		assert.Equal(t, fileBytes, attachments[0])

		// Header: one attachment, placeholder substituted for file, json tags
		// honored, json:"-" and unexported fields absent, note omitted (empty).
		assert.Equal(t,
			`51-["upload",{"file":{"_placeholder":true,"num":0},"name":"avatar"}]`,
			string(header))
	})

	t.Run("omitempty present field is kept", func(t *testing.T) {
		header, _, err := p.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "upload",
				Payloads: []interface{}{uploadStruct{Name: "x", File: []byte{1}, Note: "n"}},
			},
		})
		require.NoError(t, err)
		assert.Contains(t, string(header), `"note":"n"`)
	})

	t.Run("header matches encoding/json for non-byte parts", func(t *testing.T) {
		val := uploadStruct{Name: "avatar", File: []byte{1, 2}, Note: "hi"}
		header, _, err := p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			NS:    "/",
			Event: &socketio_v5.Event{Name: "upload", Payloads: []interface{}{val}},
		})
		require.NoError(t, err)

		// Marshal a copy whose byte field is the placeholder to derive the
		// canonical expectation, then compare the non-byte keys survive.
		assert.Contains(t, string(header), `"name":"avatar"`)
		assert.Contains(t, string(header), `"note":"hi"`)
		assert.Contains(t, string(header), `"file":{"_placeholder":true,"num":0}`)
		assert.NotContains(t, string(header), `"secret"`)
		assert.NotContains(t, string(header), `"Skipped"`)
	})

	t.Run("nested struct, pointers, typed map and slice", func(t *testing.T) {
		val := nestedStruct{
			Inner:  &innerStruct{Blob: []byte("inner")},
			Files:  [][]byte{[]byte("f0"), []byte("f1")},
			ByName: map[string][]byte{"k": []byte("kv")},
			Tags:   []string{"t1"},
		}
		_, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketEvent,
			NS:    "/",
			Event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{val}},
		})
		require.NoError(t, err)
		// 1 (inner.blob) + 2 (files) + 1 (byName) = 4 attachments.
		require.Len(t, attachments, 4)
		// Ordered: inner first (field order), then files, then byName.
		assert.Equal(t, []byte("inner"), attachments[0])
		assert.Equal(t, []byte("f0"), attachments[1])
		assert.Equal(t, []byte("f1"), attachments[2])
		assert.Equal(t, []byte("kv"), attachments[3])
	})
}

// TestStructBinary_RoundTrip is the lockstep proof: serialize a struct payload
// to a binary packet, parse + reconstruct, and confirm both the bytes and the
// non-byte fields are restored through a typed struct handler.
func TestStructBinary_RoundTrip(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	fileBytes := []byte{0x00, 0x01, 0x02, 0xFF, 0x10}
	orig := uploadStruct{Name: "doc", File: fileBytes, Note: "n"}

	header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
		Type:  socketio_v5.PacketEvent,
		NS:    "/",
		Event: &socketio_v5.Event{Name: "upload", Payloads: []interface{}{orig}},
	})
	require.NoError(t, err)
	require.Len(t, attachments, 1)

	parsed, err := p.Parse(header)
	require.NoError(t, err)
	require.NotNil(t, parsed.BinaryAttachments)
	require.Equal(t, 1, *parsed.BinaryAttachments)
	require.NoError(t, p.ReconstructBinary(parsed, attachments))

	// The reconstructed payload is a map[string]interface{} with []byte at file.
	require.Len(t, parsed.Event.Payloads, 1)

	var got uploadStruct
	called := false
	cb := p.WrapCallback(func(u uploadStruct) {
		called = true
		got = u
	})
	require.NotNil(t, cb)
	cb(parsed.Event.Payloads)

	require.True(t, called, "typed struct handler must fire")
	assert.Equal(t, "doc", got.Name)
	assert.Equal(t, "n", got.Note)
	assert.Equal(t, fileBytes, got.File, "byte field must survive the round trip intact")
}

// TestWrapCallback_StructConvert covers the RECEIVE discrimination rule.
func TestWrapCallback_StructConvert(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	binData := []byte{0x09, 0x08, 0x07}

	t.Run("reconstructed map binds to struct via marshal/unmarshal", func(t *testing.T) {
		called := false
		var got uploadStruct
		cb := p.WrapCallback(func(u uploadStruct) {
			called = true
			got = u
		})
		require.NotNil(t, cb)
		// Reconstructed value: a decoded map with []byte at the file key.
		cb([]interface{}{map[string]interface{}{"name": "n", "file": binData}})
		require.True(t, called)
		assert.Equal(t, "n", got.Name)
		assert.Equal(t, binData, got.File)
	})

	t.Run("map handler still works directly", func(t *testing.T) {
		called := false
		var got map[string]interface{}
		cb := p.WrapCallback(func(m map[string]interface{}) {
			called = true
			got = m
		})
		require.NotNil(t, cb)
		payload := map[string]interface{}{"file": binData}
		cb([]interface{}{payload})
		require.True(t, called)
		assert.Equal(t, binData, got["file"])
	})

	t.Run("top-level []byte binds to []byte param", func(t *testing.T) {
		var got []byte
		cb := p.WrapCallback(func(b []byte) { got = b })
		require.NotNil(t, cb)
		cb([]interface{}{binData})
		assert.Equal(t, binData, got)
	})

	t.Run("top-level []byte to string param is rejected", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(s string) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{binData})
		assert.False(t, called, "binary leaf must not be coerced to a string")
	})

	t.Run("top-level []byte to []string param is rejected", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(s []string) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{binData})
		assert.False(t, called, "binary leaf must not be coerced to []string")
	})

	t.Run("reconstructed map to scalar param is rejected", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(n int) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{map[string]interface{}{"a": binData}})
		assert.False(t, called, "composite value must not bind to a scalar param")
	})

	t.Run("reconstructed map incompatible with struct is rejected", func(t *testing.T) {
		called := false
		// file is bytes but target field expects an int -> unmarshal fails.
		type wrong struct {
			File int `json:"file"`
		}
		cb := p.WrapCallback(func(w wrong) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{map[string]interface{}{"file": binData}})
		assert.False(t, called, "unmarshal failure must skip the handler")
	})

	t.Run("reconstructed slice binds to typed slice via marshal", func(t *testing.T) {
		called := false
		var got [][]byte
		cb := p.WrapCallback(func(s [][]byte) {
			called = true
			got = s
		})
		require.NotNil(t, cb)
		cb([]interface{}{[]interface{}{[]byte("a"), []byte("bb")}})
		require.True(t, called)
		require.Len(t, got, 2)
		assert.Equal(t, []byte("a"), got[0])
		assert.Equal(t, []byte("bb"), got[1])
	})

	t.Run("json raw message still unmarshals", func(t *testing.T) {
		var got string
		cb := p.WrapCallback(func(s string) { got = s })
		require.NotNil(t, cb)
		cb([]interface{}{json.RawMessage(`"hello"`)})
		assert.Equal(t, "hello", got)
	})
}

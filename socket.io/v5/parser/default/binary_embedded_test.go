package socketio_v5_parser_default

import (
	"encoding/json"
	"reflect"
	"testing"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	"github.com/stretchr/testify/require"
)

type binaryEmbeddedInner struct {
	File   []byte `json:"file"`
	Label  string `json:"label"`
	hidden string
}
type binaryEmbeddedOuter struct {
	binaryEmbeddedInner
	Extra []byte `json:"extra,omitempty"`
}

func TestBinaryUnexportedEmbeddedStruct(t *testing.T) {
	t.Parallel()
	p := NewParser()
	original := binaryEmbeddedOuter{binaryEmbeddedInner: binaryEmbeddedInner{File: []byte{1, 2}, Label: "kept", hidden: "private"}}
	// encoding/json promotes the exported fields of an unexported anonymous struct.
	plain, err := json.Marshal(original)
	require.NoError(t, err)
	require.JSONEq(t, `{"file":"AQI=","label":"kept"}`, string(plain))
	for _, payload := range []interface{}{original, &original, []binaryEmbeddedOuter{original}, map[string]binaryEmbeddedOuter{"item": original}} {
		event := &socketio_v5.Event{Name: "upload", Payloads: []interface{}{payload}}
		require.True(t, p.HasBinary(event))
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{Type: socketio_v5.PacketEvent, NS: "/", Event: event})
		require.NoError(t, err)
		require.Equal(t, [][]byte{{1, 2}}, attachments)
		msg, err := p.Parse(header)
		require.NoError(t, err)
		require.NoError(t, p.ReconstructBinary(msg, attachments))
		require.Contains(t, string(header), `"file":{"_placeholder":true,"num":0}`)
		require.Contains(t, string(header), `"label":"kept"`)
		require.NotContains(t, string(header), "private")
	}
	require.Equal(t, []byte{1, 2}, original.File)
	// Exported descendants of an embedded unexported struct are settable.
	copied := reflect.New(reflect.TypeOf(original)).Elem()
	copied.Set(reflect.ValueOf(original))
	require.True(t, copied.Field(0).Field(0).CanSet())
}

type binaryEmbeddedPointerOuter struct{ *binaryEmbeddedInner }

func TestBinaryUnexportedEmbeddedPointerFailsExplicitly(t *testing.T) {
	t.Parallel()
	p := NewParser()
	original := binaryEmbeddedPointerOuter{&binaryEmbeddedInner{File: []byte{1, 2}}}
	for _, payload := range []interface{}{original, &original, []binaryEmbeddedPointerOuter{original}, map[string]interface{}{"nested": original}} {
		event := &socketio_v5.Event{Name: "upload", Payloads: []interface{}{payload}}
		require.True(t, p.HasBinary(event))
		_, _, err := p.SerializeBinary(&socketio_v5.Message{Type: socketio_v5.PacketEvent, NS: "/", Event: event})
		require.ErrorIs(t, err, ErrParseBinary)
		require.ErrorContains(t, err, "unexported embedded pointers")
	}
	require.Equal(t, []byte{1, 2}, original.File)
}

package socketio_v5_parser_default

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// newBinaryParser builds a default parser with a silent logger for the binary
// tests.
func newBinaryParser() *SocketIOV5DefaultParser {
	return NewParser(WithLogger(logger))
}

// TestParser_WrapCallback_Binary verifies that a reconstructed []byte payload
// is passed straight through to a []byte callback parameter (rather than being
// JSON-unmarshaled), which is how binary attachments reach handlers/acks.
func TestParser_WrapCallback_Binary(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	t.Run("byte slice passthrough", func(t *testing.T) {
		var got []byte
		cb := p.WrapCallback(func(b []byte) { got = b })
		require.NotNil(t, cb)
		cb([]interface{}{[]byte{0x1, 0x2, 0x3}})
		assert.Equal(t, []byte{0x1, 0x2, 0x3}, got)
	})

	t.Run("mixed binary and json args", func(t *testing.T) {
		var gotStr string
		var gotBin []byte
		cb := p.WrapCallback(func(s string, b []byte) {
			gotStr = s
			gotBin = b
		})
		require.NotNil(t, cb)
		cb([]interface{}{json.RawMessage(`"name"`), []byte("data")})
		assert.Equal(t, "name", gotStr)
		assert.Equal(t, []byte("data"), gotBin)
	})

	t.Run("byte payload to non-byte param is rejected", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(s string) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{[]byte("data")})
		assert.False(t, called, "binary payload must not bind to a string parameter")
	})

	t.Run("malformed json raw message is rejected", func(t *testing.T) {
		called := false
		cb := p.WrapCallback(func(n int) { called = true })
		require.NotNil(t, cb)
		// A json.RawMessage that cannot unmarshal into the parameter type must
		// take the json.Unmarshal error path and not invoke the handler.
		cb([]interface{}{json.RawMessage(`"not-an-int"`)})
		assert.False(t, called, "malformed json must not invoke the handler")
	})
}

// TestParser_WrapCallback_NestedBinary verifies that a typed handler receives
// an already-decoded value (a map[string]interface{} / []interface{} produced
// by binary attachment reconstruction) that contains []byte at a nested
// position. Such values arrive as concrete Go types (not json.RawMessage), so
// WrapCallback must assign them to the handler parameter directly rather than
// attempting json.Unmarshal. Previously these only worked via OnAny.
func TestParser_WrapCallback_NestedBinary(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	binData := []byte{0x01, 0x02, 0x03, 0x04}

	t.Run("map with nested binary fires typed handler", func(t *testing.T) {
		called := false
		var got map[string]interface{}
		cb := p.WrapCallback(func(m map[string]interface{}) {
			called = true
			got = m
		})
		require.NotNil(t, cb)

		// Reconstructed payload: a decoded map containing []byte at a key.
		payload := map[string]interface{}{
			"name": "avatar",
			"file": binData,
		}
		cb([]interface{}{payload})

		require.True(t, called, "typed handler must fire for nested-binary map")
		raw, ok := got["file"].([]byte)
		require.True(t, ok, "expected []byte at key file")
		assert.Equal(t, binData, raw)
	})

	t.Run("map containing nested binary slice fires typed handler", func(t *testing.T) {
		called := false
		var got map[string]interface{}
		cb := p.WrapCallback(func(m map[string]interface{}) {
			called = true
			got = m
		})
		require.NotNil(t, cb)

		// Reconstructed payload: a decoded map whose value is a slice that
		// contains []byte at an index.
		payload := map[string]interface{}{
			"items": []interface{}{"first", binData},
		}
		cb([]interface{}{payload})

		require.True(t, called, "typed handler must fire for nested-binary slice")
		items, ok := got["items"].([]interface{})
		require.True(t, ok, "expected []interface{} at key items")
		require.Len(t, items, 2)
		raw, ok := items[1].([]byte)
		require.True(t, ok, "expected []byte at index 1")
		assert.Equal(t, binData, raw)
	})

	t.Run("decoded value not assignable to param is rejected", func(t *testing.T) {
		called := false
		// A decoded map cannot be assigned to an int parameter.
		cb := p.WrapCallback(func(n int) { called = true })
		require.NotNil(t, cb)
		cb([]interface{}{map[string]interface{}{"a": []byte{1}}})
		assert.False(t, called, "non-assignable decoded value must be rejected")
	})
}

func TestParser_HasBinary(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	tests := []struct {
		name  string
		event *socketio_v5.Event
		want  bool
	}{
		{name: "nil event", event: nil, want: false},
		{
			name:  "text only",
			event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{"hello", 42}},
			want:  false,
		},
		{
			name:  "direct binary",
			event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{[]byte{1, 2, 3}}},
			want:  true,
		},
		{
			name: "binary nested in slice",
			event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{
				[]interface{}{"a", []byte{9}},
			}},
			want: true,
		},
		{
			name: "binary nested in map",
			event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{
				map[string]interface{}{"file": []byte{7}},
			}},
			want: true,
		},
		{
			name: "no binary in nested structures",
			event: &socketio_v5.Event{Name: "ev", Payloads: []interface{}{
				map[string]interface{}{"x": []interface{}{"y", 1}},
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, p.HasBinary(tt.event))
		})
	}
}

func TestParser_SerializeBinary(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	t.Run("single attachment event", func(t *testing.T) {
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "binEvent",
				Payloads: []interface{}{[]byte{0x1, 0x2, 0x3}},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, []byte(`51-["binEvent",{"_placeholder":true,"num":0}]`), header)
		require.Len(t, attachments, 1)
		assert.Equal(t, []byte{0x1, 0x2, 0x3}, attachments[0])
	})

	t.Run("multiple attachments preserve order", func(t *testing.T) {
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "multi",
				Payloads: []interface{}{[]byte("first"), "text", []byte("second")},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, []byte(`52-["multi",{"_placeholder":true,"num":0},"text",{"_placeholder":true,"num":1}]`), header)
		require.Len(t, attachments, 2)
		assert.Equal(t, []byte("first"), attachments[0])
		assert.Equal(t, []byte("second"), attachments[1])
	})

	t.Run("binary nested in map and slice", func(t *testing.T) {
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name: "nested",
				Payloads: []interface{}{
					map[string]interface{}{"file": []byte("data")},
					[]interface{}{[]byte("more")},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, attachments, 2)
		assert.Equal(t, []byte("data"), attachments[0])
		assert.Equal(t, []byte("more"), attachments[1])
		// The header must carry the attachment count and the placeholders.
		assert.Contains(t, string(header), `52-`)
		assert.Contains(t, string(header), `"_placeholder":true`)
	})

	t.Run("ack with namespace and ack id", func(t *testing.T) {
		header, attachments, err := p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketAck,
			NS:    "/admin",
			AckId: intPtr(7),
			Event: &socketio_v5.Event{
				Payloads: []interface{}{[]byte{0xFF}},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, []byte(`61-/admin,7[{"_placeholder":true,"num":0}]`), header)
		require.Len(t, attachments, 1)
		assert.Equal(t, []byte{0xFF}, attachments[0])
	})

	t.Run("nil message", func(t *testing.T) {
		_, _, err := p.SerializeBinary(nil)
		assert.ErrorIs(t, err, ErrParseBinary)
	})

	t.Run("nil event", func(t *testing.T) {
		_, _, err := p.SerializeBinary(&socketio_v5.Message{Type: socketio_v5.PacketEvent})
		assert.ErrorIs(t, err, ErrParseBinary)
	})

	t.Run("non event/ack type", func(t *testing.T) {
		_, _, err := p.SerializeBinary(&socketio_v5.Message{
			Type:  socketio_v5.PacketConnect,
			Event: &socketio_v5.Event{Name: "x", Payloads: []interface{}{[]byte{1}}},
		})
		assert.ErrorIs(t, err, ErrParseBinary)
	})

	t.Run("unserializable payload", func(t *testing.T) {
		_, _, err := p.SerializeBinary(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			Event: &socketio_v5.Event{
				Name:     "x",
				Payloads: []interface{}{[]byte{1}, make(chan int)},
			},
		})
		assert.Error(t, err)
	})
}

func TestParser_ReconstructBinary(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	t.Run("single placeholder", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			NS:                "/",
			BinaryAttachments: intPtr(1),
			Event: &socketio_v5.Event{
				Name:     "binEvent",
				Payloads: []interface{}{json.RawMessage(`{"_placeholder":true,"num":0}`)},
			},
		}
		err := p.ReconstructBinary(msg, [][]byte{{0x1, 0x2, 0x3}})
		require.NoError(t, err)
		assert.Nil(t, msg.BinaryAttachments)
		require.Len(t, msg.Event.Payloads, 1)
		assert.Equal(t, []byte{0x1, 0x2, 0x3}, msg.Event.Payloads[0])
	})

	t.Run("placeholder nested in object", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			NS:                "/",
			BinaryAttachments: intPtr(1),
			Event: &socketio_v5.Event{
				Name:     "binEvent",
				Payloads: []interface{}{json.RawMessage(`{"file":{"_placeholder":true,"num":0},"name":"x"}`)},
			},
		}
		err := p.ReconstructBinary(msg, [][]byte{[]byte("payload")})
		require.NoError(t, err)
		obj, ok := msg.Event.Payloads[0].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, []byte("payload"), obj["file"])
		assert.Equal(t, "x", obj["name"])
	})

	t.Run("placeholder nested in array", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			NS:                "/",
			BinaryAttachments: intPtr(2),
			Event: &socketio_v5.Event{
				Name:     "binEvent",
				Payloads: []interface{}{json.RawMessage(`["a",{"_placeholder":true,"num":1},{"_placeholder":true,"num":0}]`)},
			},
		}
		err := p.ReconstructBinary(msg, [][]byte{[]byte("zero"), []byte("one")})
		require.NoError(t, err)
		arr, ok := msg.Event.Payloads[0].([]interface{})
		require.True(t, ok)
		assert.Equal(t, "a", arr[0])
		assert.Equal(t, []byte("one"), arr[1])
		assert.Equal(t, []byte("zero"), arr[2])
	})

	t.Run("zero attachments", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			NS:                "/",
			BinaryAttachments: intPtr(0),
			Event: &socketio_v5.Event{
				Name:     "binEvent",
				Payloads: []interface{}{json.RawMessage(`"text"`)},
			},
		}
		err := p.ReconstructBinary(msg, nil)
		require.NoError(t, err)
		assert.Nil(t, msg.BinaryAttachments)
	})

	t.Run("nil message", func(t *testing.T) {
		assert.ErrorIs(t, p.ReconstructBinary(nil, nil), ErrParseBinary)
	})

	t.Run("count mismatch", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			BinaryAttachments: intPtr(2),
			Event:             &socketio_v5.Event{Name: "x"},
		}
		assert.ErrorIs(t, p.ReconstructBinary(msg, [][]byte{{1}}), ErrParseBinary)
	})

	t.Run("placeholder index out of range", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			BinaryAttachments: intPtr(1),
			Event: &socketio_v5.Event{
				Name:     "x",
				Payloads: []interface{}{json.RawMessage(`{"_placeholder":true,"num":5}`)},
			},
		}
		assert.ErrorIs(t, p.ReconstructBinary(msg, [][]byte{{1}}), ErrParseBinary)
	})

	t.Run("placeholder without num", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			BinaryAttachments: intPtr(1),
			Event: &socketio_v5.Event{
				Name:     "x",
				Payloads: []interface{}{json.RawMessage(`{"_placeholder":true}`)},
			},
		}
		assert.ErrorIs(t, p.ReconstructBinary(msg, [][]byte{{1}}), ErrParseBinary)
	})

	t.Run("placeholder num not a number", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			BinaryAttachments: intPtr(1),
			Event: &socketio_v5.Event{
				Name:     "x",
				Payloads: []interface{}{json.RawMessage(`{"_placeholder":true,"num":"a"}`)},
			},
		}
		assert.ErrorIs(t, p.ReconstructBinary(msg, [][]byte{{1}}), ErrParseBinary)
	})

	t.Run("invalid placeholder nested in object propagates error", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			BinaryAttachments: intPtr(1),
			Event: &socketio_v5.Event{
				Name:     "x",
				Payloads: []interface{}{json.RawMessage(`{"file":{"_placeholder":true,"num":9}}`)},
			},
		}
		assert.ErrorIs(t, p.ReconstructBinary(msg, [][]byte{{1}}), ErrParseBinary)
	})

	t.Run("invalid placeholder nested in array propagates error", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			BinaryAttachments: intPtr(1),
			Event: &socketio_v5.Event{
				Name:     "x",
				Payloads: []interface{}{json.RawMessage(`["a",{"_placeholder":true,"num":9}]`)},
			},
		}
		assert.ErrorIs(t, p.ReconstructBinary(msg, [][]byte{{1}}), ErrParseBinary)
	})

	t.Run("invalid json payload", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			BinaryAttachments: intPtr(1),
			Event: &socketio_v5.Event{
				Name:     "x",
				Payloads: []interface{}{json.RawMessage(`{not json}`)},
			},
		}
		assert.ErrorIs(t, p.ReconstructBinary(msg, [][]byte{{1}}), ErrParseBinary)
	})

	t.Run("placeholder flag false is a plain object", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			BinaryAttachments: intPtr(0),
			Event: &socketio_v5.Event{
				Name:     "x",
				Payloads: []interface{}{json.RawMessage(`{"_placeholder":false,"num":0}`)},
			},
		}
		require.NoError(t, p.ReconstructBinary(msg, nil))
		obj, ok := msg.Event.Payloads[0].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, false, obj["_placeholder"])
	})

	t.Run("non-raw payload is left untouched", func(t *testing.T) {
		msg := &socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			BinaryAttachments: intPtr(0),
			Event: &socketio_v5.Event{
				Name:     "x",
				Payloads: []interface{}{"already decoded"},
			},
		}
		require.NoError(t, p.ReconstructBinary(msg, nil))
		assert.Equal(t, "already decoded", msg.Event.Payloads[0])
	})
}

func TestParser_validateMessage_Binary(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	t.Run("binary event without name fails", func(t *testing.T) {
		_, err := p.Serialize(&socketio_v5.Message{
			Type:  socketio_v5.PacketBinaryEvent,
			Event: &socketio_v5.Event{Name: ""},
		})
		assert.Error(t, err)
	})

	t.Run("binary event with name serializes", func(t *testing.T) {
		count := 0
		_, err := p.Serialize(&socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryEvent,
			NS:                "/",
			BinaryAttachments: &count,
			Event:             &socketio_v5.Event{Name: "ev"},
		})
		assert.NoError(t, err)
	})

	t.Run("binary ack without event fails", func(t *testing.T) {
		_, err := p.Serialize(&socketio_v5.Message{
			Type: socketio_v5.PacketBinaryAck,
		})
		assert.Error(t, err)
	})

	t.Run("binary ack with event serializes", func(t *testing.T) {
		count := 0
		_, err := p.Serialize(&socketio_v5.Message{
			Type:              socketio_v5.PacketBinaryAck,
			NS:                "/",
			AckId:             intPtr(1),
			BinaryAttachments: &count,
			Event:             &socketio_v5.Event{Payloads: []interface{}{}},
		})
		assert.NoError(t, err)
	})
}

func TestParser_ReconstructBinary_ScalarPayload(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	// A scalar (non-object, non-array) payload exercises the default branch of
	// replacePlaceholders and is returned unchanged.
	msg := &socketio_v5.Message{
		Type:              socketio_v5.PacketBinaryEvent,
		NS:                "/",
		BinaryAttachments: intPtr(1),
		Event: &socketio_v5.Event{
			Name:     "ev",
			Payloads: []interface{}{json.RawMessage(`"scalar"`), json.RawMessage(`{"_placeholder":true,"num":0}`)},
		},
	}
	require.NoError(t, p.ReconstructBinary(msg, [][]byte{[]byte("buf")}))
	assert.Equal(t, "scalar", msg.Event.Payloads[0])
	assert.Equal(t, []byte("buf"), msg.Event.Payloads[1])
}

// TestParser_BinaryRoundTrip verifies serialize -> parse -> reconstruct restores
// the original binary payloads for both events and acks, including multiple and
// nested attachments.
func TestParser_BinaryRoundTrip(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	tests := []struct {
		name string
		msg  *socketio_v5.Message
	}{
		{
			name: "single binary event",
			msg: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "/",
				Event: &socketio_v5.Event{
					Name:     "ev",
					Payloads: []interface{}{[]byte{0x0, 0x1, 0x2, 0xFF}},
				},
			},
		},
		{
			name: "multi binary with text",
			msg: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "/admin",
				Event: &socketio_v5.Event{
					Name:     "ev",
					Payloads: []interface{}{[]byte("a"), []byte("bb"), []byte("ccc")},
				},
			},
		},
		{
			name: "binary ack with id",
			msg: &socketio_v5.Message{
				Type:  socketio_v5.PacketAck,
				NS:    "/",
				AckId: intPtr(42),
				Event: &socketio_v5.Event{
					Payloads: []interface{}{[]byte("ack-bytes")},
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			header, attachments, err := p.SerializeBinary(tt.msg)
			require.NoError(t, err)

			parsed, err := p.Parse(header)
			require.NoError(t, err)
			require.NotNil(t, parsed.BinaryAttachments)
			require.Equal(t, len(attachments), *parsed.BinaryAttachments)

			require.NoError(t, p.ReconstructBinary(parsed, attachments))

			// Compare reconstructed []byte payloads with the originals.
			require.Len(t, parsed.Event.Payloads, len(tt.msg.Event.Payloads))
			for i, want := range tt.msg.Event.Payloads {
				assert.Equal(t, want, parsed.Event.Payloads[i])
			}
			// Namespace and ack id survive the round trip.
			assert.Equal(t, tt.msg.NS, parsed.NS)
			if tt.msg.AckId != nil {
				require.NotNil(t, parsed.AckId)
				assert.Equal(t, *tt.msg.AckId, *parsed.AckId)
			}
		})
	}
}

// TestParser_ParseBinaryAttachmentCount exercises the attachment-count parsing
// edge cases on the binary packet header.
func TestParser_ParseBinaryAttachmentCount(t *testing.T) {
	t.Parallel()
	p := newBinaryParser()

	tests := []struct {
		name    string
		input   []byte
		wantErr error
		count   *int
	}{
		{
			name:  "binary ack zero attachments",
			input: []byte(`60-["data"]`),
			count: intPtr(0),
		},
		{
			name:    "missing dash",
			input:   []byte(`51["ev"]`),
			wantErr: ErrParsePackage,
		},
		{
			name:    "no count digits",
			input:   []byte(`5-["ev"]`),
			wantErr: ErrParsePackage,
		},
		{
			name:    "absurdly long count",
			input:   []byte(`51234567890123456789-["ev"]`),
			wantErr: ErrParsePackage,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg, err := p.Parse(tt.input)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, msg.BinaryAttachments)
			assert.Equal(t, *tt.count, *msg.BinaryAttachments)
		})
	}
}

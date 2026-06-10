package engineio_v4_parser

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
)

func TestEngineIOV4Parser_Parse(t *testing.T) {
	parser := &EngineIOV4Parser{}
	tests := []struct {
		name    string
		input   []byte
		want    *engineio_v4.Message
		wantErr error
	}{
		{
			name:  "Valid message",
			input: []byte("0hello"),
			want: &engineio_v4.Message{
				Type: engineio_v4.EngineIOPacket(0),
				Data: []byte("hello"),
			},
			wantErr: nil,
		},
		{
			name:    "Empty message",
			input:   []byte{},
			want:    nil,
			wantErr: errors.New("empty message"),
		},
		{
			name:  "Message with only type",
			input: []byte("4"),
			want: &engineio_v4.Message{
				Type: engineio_v4.EngineIOPacket(4),
				Data: []byte{},
			},
			wantErr: nil,
		},
		{
			name:    "Unknown packet type (above range)",
			input:   []byte("9data"),
			want:    nil,
			wantErr: errors.New("unknown engine.io packet type: 0x39"),
		},
		{
			name:    "Unknown packet type (below range)",
			input:   []byte(" data"),
			want:    nil,
			wantErr: errors.New("unknown engine.io packet type: 0x20"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.Parse(tt.input)

			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErr.Error(), err.Error())
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEngineIOV4Parser_Serialize(t *testing.T) {
	parser := &EngineIOV4Parser{}
	tests := []struct {
		name    string
		input   *engineio_v4.Message
		want    []byte
		wantErr error
	}{
		{
			name: "Valid message",
			input: &engineio_v4.Message{
				Type: engineio_v4.EngineIOPacket(0),
				Data: []byte("hello"),
			},
			want:    []byte("0hello"),
			wantErr: nil,
		},
		{
			name: "Message with only type",
			input: &engineio_v4.Message{
				Type: engineio_v4.EngineIOPacket(4),
				Data: []byte{},
			},
			want:    []byte("4"),
			wantErr: nil,
		},
		{
			name: "Message with non-ASCII type",
			input: &engineio_v4.Message{
				Type: engineio_v4.EngineIOPacket(10),
				Data: []byte("test"),
			},
			want:    []byte(":test"),
			wantErr: nil,
		},
		{
			name: "Veeery long message",
			input: &engineio_v4.Message{
				Type: engineio_v4.EngineIOPacket(10),
				Data: func() []byte {
					return make([]byte, 65*1024*1024)
				}(),
			},
			want:    nil,
			wantErr: errors.New("message data too large"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.Serialize(tt.input)

			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErr.Error(), err.Error())
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewEngineIOV4Parser(t *testing.T) {
	t.Run("Default limit", func(t *testing.T) {
		p, err := NewEngineIOV4Parser()
		assert.NoError(t, err)
		assert.Equal(t, int64(defaultMaxSerializeSize), p.maxSerializeSize)
	})

	t.Run("Custom limit", func(t *testing.T) {
		p, err := NewEngineIOV4Parser(WithMaxSerializeSize(1024))
		assert.NoError(t, err)
		assert.Equal(t, int64(1024), p.maxSerializeSize)
	})

	t.Run("Invalid limit propagates error", func(t *testing.T) {
		p, err := NewEngineIOV4Parser(WithMaxSerializeSize(0))
		assert.Error(t, err)
		assert.Nil(t, p)
	})
}

func TestWithMaxSerializeSize(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		p := &EngineIOV4Parser{}
		assert.NoError(t, WithMaxSerializeSize(2048)(p))
		assert.Equal(t, int64(2048), p.maxSerializeSize)
	})

	t.Run("Zero", func(t *testing.T) {
		assert.Error(t, WithMaxSerializeSize(0)(&EngineIOV4Parser{}))
	})

	t.Run("Negative", func(t *testing.T) {
		assert.Error(t, WithMaxSerializeSize(-1)(&EngineIOV4Parser{}))
	})
}

func TestEngineIOV4Parser_Serialize_CustomLimit(t *testing.T) {
	p, err := NewEngineIOV4Parser(WithMaxSerializeSize(4))
	assert.NoError(t, err)

	// Within the custom limit.
	got, err := p.Serialize(&engineio_v4.Message{
		Type: engineio_v4.EngineIOPacket(4),
		Data: []byte("ok"),
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("4ok"), got)

	// Exceeds the custom limit.
	_, err = p.Serialize(&engineio_v4.Message{
		Type: engineio_v4.EngineIOPacket(4),
		Data: []byte("toolong"),
	})
	assert.Error(t, err)
	assert.Equal(t, "message data too large", err.Error())
}

func TestEngineIOV4Parser_ParseBinary(t *testing.T) {
	parser := &EngineIOV4Parser{}

	t.Run("base64 binary attachment", func(t *testing.T) {
		// "bAQIDBA==" decodes to {1,2,3,4}.
		got, err := parser.Parse([]byte("bAQIDBA=="))
		assert.NoError(t, err)
		assert.Equal(t, &engineio_v4.Message{
			Type:   engineio_v4.PacketMessage,
			Data:   []byte{1, 2, 3, 4},
			Binary: true,
		}, got)
	})

	t.Run("empty base64 attachment", func(t *testing.T) {
		got, err := parser.Parse([]byte("b"))
		assert.NoError(t, err)
		assert.Equal(t, &engineio_v4.Message{
			Type:   engineio_v4.PacketMessage,
			Data:   []byte{},
			Binary: true,
		}, got)
	})

	t.Run("invalid base64", func(t *testing.T) {
		_, err := parser.Parse([]byte("b!!!notbase64"))
		assert.Error(t, err)
	})
}

func TestEngineIOV4Parser_SerializeBinary(t *testing.T) {
	parser := &EngineIOV4Parser{}

	got, err := parser.Serialize(&engineio_v4.Message{
		Type:   engineio_v4.PacketMessage,
		Data:   []byte{1, 2, 3, 4},
		Binary: true,
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("bAQIDBA=="), got)

	// Oversized binary still hits the size guard.
	_, err = parser.Serialize(&engineio_v4.Message{
		Type:   engineio_v4.PacketMessage,
		Data:   make([]byte, 65*1024*1024),
		Binary: true,
	})
	assert.Error(t, err)
}

// TestEngineIOV4Parser_BinaryRoundTrip ensures Serialize/Parse are inverse for
// binary attachments over the base64 long-polling representation.
func TestEngineIOV4Parser_BinaryRoundTrip(t *testing.T) {
	parser := &EngineIOV4Parser{}
	original := []byte{0x00, 0x10, 0xFF, 0x42, 0x7F}

	wire, err := parser.Serialize(&engineio_v4.Message{
		Type:   engineio_v4.PacketMessage,
		Data:   original,
		Binary: true,
	})
	assert.NoError(t, err)

	got, err := parser.Parse(wire)
	assert.NoError(t, err)
	assert.True(t, got.Binary)
	assert.Equal(t, original, got.Data)
}

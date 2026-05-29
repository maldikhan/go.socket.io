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

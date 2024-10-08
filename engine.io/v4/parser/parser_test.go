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

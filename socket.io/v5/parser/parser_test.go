package socketio_v5_parser

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	socketio_v5 "maldikhan/go.socket.io/socket.io/v5"
)

func TestSocketIOV5Parser_Serialize(t *testing.T) {
	parser := &SocketIOV5Parser{}

	var nilValue []interface{}

	tests := []struct {
		name    string
		input   *socketio_v5.Message
		want    []byte
		wantErr error
	}{
		{
			name:    "Nil message",
			input:   nil,
			want:    nil,
			wantErr: errors.New("empty package"),
		},
		{
			name: "Connect message",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketConnect,
				NS:   "/",
			},
			want:    []byte("0"),
			wantErr: nil,
		},
		{
			name: "Connect message with empty payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketConnect,
				NS:      "/",
				Payload: "",
			},
			want:    []byte("0"),
			wantErr: nil,
		},
		{
			name: "Connect message with {} payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketConnect,
				NS:      "/",
				Payload: map[string]any{},
			},
			want:    []byte("0"),
			wantErr: nil,
		},
		{
			name: "Connect message with nil payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketConnect,
				NS:      "/",
				Payload: nilValue,
			},
			want:    []byte("0"),
			wantErr: nil,
		},
		{
			name: "Connect message",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketConnect,
				NS:   "/",
			},
			want:    []byte("0"),
			wantErr: nil,
		},
		{
			name: "Event with {} payload",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				Event: &socketio_v5.Event{
					Name:     "test",
					Payloads: []interface{}{map[string]any{}},
				},
				NS: "/",
			},
			want:    []byte(`2["test",{}]`),
			wantErr: nil,
		},
		{
			name: "Event with empty name and no payloads",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "/",
				Event: &socketio_v5.Event{
					Name:     "",
					Payloads: []interface{}{},
				},
			},
			want:    nil,
			wantErr: errors.New("wrong event name"),
		},
		{
			name: "Event with empty name and nil payload",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "/",
				Event: &socketio_v5.Event{
					Name:     "",
					Payloads: []interface{}{nil},
				},
			},
			want:    nil,
			wantErr: errors.New("wrong event name"),
		},
		{
			name: "Event with name and empty payloads",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "/",
				Event: &socketio_v5.Event{
					Name:     "test",
					Payloads: []interface{}{},
				},
			},
			want:    []byte(`2["test"]`),
			wantErr: nil,
		},
		{
			name: "Event with name and nil payload",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "/",
				Event: &socketio_v5.Event{
					Name:     "test",
					Payloads: []interface{}{nil},
				},
			},
			want:    []byte(`2["test",null]`),
			wantErr: nil,
		},
		{
			name: "Event message with namespace and ack",
			input: &socketio_v5.Message{
				Type:  socketio_v5.PacketEvent,
				NS:    "/chat",
				AckId: intPtr(123),
				Event: &socketio_v5.Event{
					Name:     "message",
					Payloads: []interface{}{"Hello, world!"},
				},
			},
			want:    []byte(`2/chat,123["message","Hello, world!"]`),
			wantErr: nil,
		},
		{
			name: "Binary event message",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketBinaryEvent,
			},
			want:    nil,
			wantErr: errors.New("biniary events are not supported yet"),
		},
		{
			name: "Message with payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketConnect,
				Payload: map[string]interface{}{"token": "abc123"},
			},
			want:    []byte(`0{"token":"abc123"}`),
			wantErr: nil,
		},
		{
			name: "Message with empty namespace",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "",
				Event: &socketio_v5.Event{
					Name:     "test",
					Payloads: []interface{}{"data"},
				},
			},
			want:    []byte(`2["test","data"]`),
			wantErr: nil,
		},
		{
			name: "Message with root namespace",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "/",
				Event: &socketio_v5.Event{
					Name:     "test",
					Payloads: []interface{}{"data"},
				},
			},
			want:    []byte(`2["test","data"]`),
			wantErr: nil,
		},
		{
			name: "Message with large ack ID",
			input: &socketio_v5.Message{
				Type:  socketio_v5.PacketEvent,
				NS:    "/",
				AckId: intPtr(1000000),
				Event: &socketio_v5.Event{
					Name:     "test",
					Payloads: []interface{}{"data"},
				},
			},
			want:    []byte(`21000000["test","data"]`),
			wantErr: nil,
		},
		{
			name: "Message with null payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketEvent,
				NS:      "/",
				Payload: nil,
			},
			want:    nil,
			wantErr: errors.New("wrong event name"),
		},
		{
			name: "Channel in event payload",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				Event: &socketio_v5.Event{
					Name:     "test",
					Payloads: []interface{}{make(chan int)},
				},
			},
			wantErr: errors.New("json: unsupported type: chan int"),
		},
		{
			name: "Channel in connect payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketConnect,
				Payload: make(chan int),
			},
			wantErr: errors.New("json: unsupported type: chan int"),
		},
		{
			name: "Empty event name and empty payloads in ack",
			input: &socketio_v5.Message{
				Type:  socketio_v5.PacketAck,
				AckId: intPtr(44),
				Event: &socketio_v5.Event{
					Name:     "",
					Payloads: []interface{}{},
				},
			},
			want: []byte("344[]"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
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

func TestSocketIOV5Parser_Parse(t *testing.T) {
	parser := &SocketIOV5Parser{}

	tests := []struct {
		name    string
		input   []byte
		want    *socketio_v5.Message
		wantErr error
	}{
		{
			name:    "Empty message",
			input:   []byte{},
			want:    nil,
			wantErr: errors.New("empty message"),
		},
		{
			name:  "Connect message",
			input: []byte("0"),
			want: &socketio_v5.Message{
				Type: socketio_v5.PacketConnect,
				NS:   "/",
			},
			wantErr: nil,
		},
		{
			name:  "Event message with namespace and ack",
			input: []byte(`2/chat,123["message","Hello, world!"]`),
			want: &socketio_v5.Message{
				Type:  socketio_v5.PacketEvent,
				NS:    "/chat",
				AckId: intPtr(123),
				Event: &socketio_v5.Event{
					Name:     "message",
					Payloads: []interface{}{json.RawMessage(`"Hello, world!"`)},
				},
			},
			wantErr: nil,
		},
		{
			name:    "Event message with wrong namespace",
			input:   []byte(`2/chat["message","Hello, world!"]`),
			want:    nil,
			wantErr: errors.New("can't find event entity start: invalid character '/' looking for beginning of value"),
		},
		{
			name:  "Connect with ack only",
			input: []byte(`0123`),
			want: &socketio_v5.Message{
				Type:  socketio_v5.PacketConnect,
				NS:    "/",
				AckId: intPtr(123),
			},
			wantErr: nil,
		},
		{
			name:    "Event with ack only",
			input:   []byte(`2123`),
			want:    nil,
			wantErr: errors.New("wrong package payload"),
		},
		{
			name:  "Connect with namespace only",
			input: []byte(`0/test,`),
			want: &socketio_v5.Message{
				Type: socketio_v5.PacketConnect,
				NS:   "/test",
			},
			wantErr: nil,
		},
		{
			name:    "Event with namespace only",
			input:   []byte(`2/test,`),
			want:    nil,
			wantErr: errors.New("wrong package payload"),
		},
		{
			name:  "Binary event message",
			input: []byte("5"),
			want: &socketio_v5.Message{
				Type: socketio_v5.PacketBinaryEvent,
				NS:   "/",
			},
			wantErr: errors.New("binary events are not supported yet"),
		},
		{
			name:  "Connect error message",
			input: []byte("4Error message"),
			want: &socketio_v5.Message{
				Type:         socketio_v5.PacketConnectError,
				NS:           "/",
				ErrorMessage: strPtr("Error message"),
			},
			wantErr: nil,
		},
		{
			name:  "Message with large ack ID",
			input: []byte(`21000000["test","data"]`),
			want: &socketio_v5.Message{
				Type:  socketio_v5.PacketEvent,
				NS:    "/",
				AckId: intPtr(1000000),
				Event: &socketio_v5.Event{
					Name:     "test",
					Payloads: []interface{}{json.RawMessage(`"data"`)},
				},
			},
			wantErr: nil,
		},
		{
			name:    "Message with invalid ack ID",
			input:   []byte(`2abc["test","data"]`),
			want:    nil,
			wantErr: errors.New("can't find event entity start: invalid character 'a' looking for beginning of value"),
		},
		{
			name:    "Message with too large ack ID",
			input:   []byte(`29999999999999999999["test","data"]`),
			want:    nil,
			wantErr: errors.New("wrong package ack data"),
		},
		{
			name:  "Message with empty payload",
			input: []byte(`2`),
			want: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "/",
			},
			wantErr: nil,
		},
		{
			name:    "Message with invalid JSON payload",
			input:   []byte(`2["test",{invalid json}]`),
			want:    nil,
			wantErr: errors.New("error decoding payload: invalid character 'i' looking for beginning of object key string"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
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

func TestSocketIOV5Parser_parseEvent(t *testing.T) {
	parser := &SocketIOV5Parser{}

	tests := []struct {
		name       string
		input      []byte
		emptyEvent bool
		want       *socketio_v5.Event
		wantErr    error
	}{
		{
			name:       "Valid event",
			input:      []byte(`["message","Hello, world!"]`),
			emptyEvent: false,
			want: &socketio_v5.Event{
				Name:     "message",
				Payloads: []interface{}{json.RawMessage(`"Hello, world!"`)},
			},
			wantErr: nil,
		},
		{
			name:       "Empty event",
			input:      []byte(`["Hello, world!"]`),
			emptyEvent: true,
			want: &socketio_v5.Event{
				Payloads: []interface{}{json.RawMessage(`"Hello, world!"`)},
			},
			wantErr: nil,
		},
		{
			name:       "Event with multiple payloads",
			input:      []byte(`["userJoined", "John", 25, {"status": "online"}]`),
			emptyEvent: false,
			want: &socketio_v5.Event{
				Name:     "userJoined",
				Payloads: []interface{}{json.RawMessage(`"John"`), json.RawMessage(`25`), json.RawMessage(`{"status": "online"}`)},
			},
			wantErr: nil,
		},
		{
			name:       "Invalid JSON",
			input:      []byte(`["message","Hello, world!"`),
			emptyEvent: false,
			want:       nil,
			wantErr:    errors.New("can't find event entity end: EOF"),
		},
		{
			name:       "Not an array",
			input:      []byte(`{"message": "Hello, world!"}`),
			emptyEvent: false,
			want:       nil,
			wantErr:    errors.New("can't find event entity start: <nil>"),
		},
		{
			name:       "Empty array",
			input:      []byte(`[]`),
			emptyEvent: false,
			want:       nil,
			wantErr:    errors.New("error decoding event name: invalid character ']' looking for beginning of value"),
		},
		{
			name:       "Array with only event name",
			input:      []byte(`["test"]`),
			emptyEvent: false,
			want: &socketio_v5.Event{
				Name: "test",
			},
			wantErr: nil,
		},
		{
			name:       "Array with complex payload",
			input:      []byte(`["test", {"key": "value"}, [1, 2, 3], null, true, 42]`),
			emptyEvent: false,
			want: &socketio_v5.Event{
				Name: "test",
				Payloads: []interface{}{
					json.RawMessage(`{"key": "value"}`),
					json.RawMessage(`[1, 2, 3]`),
					json.RawMessage(`null`),
					json.RawMessage(`true`),
					json.RawMessage(`42`),
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parser.parseEvent(tt.input, tt.emptyEvent)
			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErr.Error(), err.Error())
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)

			if got != nil && tt.want != nil {
				assert.Equal(t, tt.want.Name, got.Name)
				assert.Len(t, got.Payloads, len(tt.want.Payloads))

				for i, payload := range tt.want.Payloads {
					assert.JSONEq(t, toJSON(t, payload), toJSON(t, got.Payloads[i]))
				}
			}
		})
	}
}

func toJSON(t *testing.T, v interface{}) string {
	jsonBytes, err := json.Marshal(v)
	assert.NoError(t, err)
	return string(jsonBytes)
}

func intPtr(i int) *int {
	return &i
}

func strPtr(s string) *string {
	return &s
}

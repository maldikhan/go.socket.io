package socketio_v5_parser_default

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	mock_socketio_v5_parser_default "github.com/maldikhan/go.socket.io/socket.io/v5/parser/default/mocks"
	"github.com/maldikhan/go.socket.io/utils"
)

var logger = &utils.DefaultLogger{Level: utils.NONE}

func TestSocketIOV5DefaultParser_Parse(t *testing.T) {
	t.Parallel()

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
			wantErr: ErrParsePackage,
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
			wantErr: ErrParseEvent,
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
			wantErr: ErrParsePackage,
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
			wantErr: ErrParsePackage,
		},
		{
			name:  "Binary event message",
			input: []byte("5"),
			want: &socketio_v5.Message{
				Type: socketio_v5.PacketBinaryEvent,
				NS:   "/",
			},
			wantErr: ErrParseEventUnsupported,
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
			wantErr: ErrParseEvent,
		},
		{
			name:    "Message with too large ack ID",
			input:   []byte(`29999999999999999999["test","data"]`),
			want:    nil,
			wantErr: ErrParsePackage,
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
			wantErr: ErrParseEvent,
		},
	}

	parser := NewParser(
		WithLogger(logger),
	)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parser.Parse(tt.input)
			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
			if tt.want != nil || got != nil {
				if got.Event != nil {
					for i, payload := range got.Event.Payloads {
						// Jsoniter parser returns []byte instead of RawMessage
						if bytesPayload, ok := payload.([]byte); ok {
							got.Event.Payloads[i] = json.RawMessage(bytesPayload)
						}
					}
				}

				assert.Equal(t, tt.want, got)
			}

		})
	}
}

func TestSocketIOV5DefaultParser_parseEvent(t *testing.T) {
	t.Parallel()

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
			name: "Valid event with garbage 2",
			input: []byte(`["eventName", 
    
			"param1",  
			
			123,
			
			{"key": "value"}
			
			]`),
			emptyEvent: false,
			want: &socketio_v5.Event{
				Name: "eventName",
				Payloads: []interface{}{
					json.RawMessage(`"param1"`),
					json.RawMessage(`123`),
					json.RawMessage(`{"key":"value"}`),
				},
			},
			wantErr: nil,
		},
		{
			name: "Valid event with garbage",
			input: []byte(`[  	
				"message"    ,     
				 "Hello"
				 ,   123 ,    ["a", "b", "c"]  
				   ,    {"Ivan": "Pupkin"}
				   
				 
				 ]  `),
			emptyEvent: false,
			want: &socketio_v5.Event{
				Name: "message",
				Payloads: []interface{}{
					json.RawMessage(`"Hello"`),
					json.RawMessage(`123`),
					json.RawMessage(`["a", "b", "c"]`),
					json.RawMessage(`{"Ivan": "Pupkin"}`),
				},
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
			name:       "Empty event payload",
			input:      []byte(`["Hello, world!"]`),
			emptyEvent: false,
			want: &socketio_v5.Event{
				Name: `Hello, world!`,
			},
			wantErr: nil,
		},
		{
			name:       "Wrong event name",
			input:      []byte(`[{}, Hello, world!"]`),
			emptyEvent: false,
			want:       nil,
			wantErr:    ErrParseEvent,
		},
		{
			name:       "Wrong event element",
			input:      []byte(`[{}, \/ ,Hello, world!"]`),
			emptyEvent: false,
			want:       nil,
			wantErr:    ErrParseEvent,
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
			wantErr:    ErrParseEvent,
		},
		{
			name:       "Not an array",
			input:      []byte(`{"message": "Hello, world!"}`),
			emptyEvent: false,
			want:       nil,
			wantErr:    ErrParseEvent,
		},
		{
			name:       "Empty array",
			input:      []byte(`[]`),
			emptyEvent: false,
			want:       nil,
			wantErr:    ErrParseEvent,
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
		{
			name:       "Invalid JSON payload",
			input:      []byte(`["event", {"key": "value"}, [1, 2, 3], {"invalid": json}]`),
			emptyEvent: false,
			want:       nil,
			wantErr:    ErrParseEvent,
		},
	}

	parser := NewParser(
		WithLogger(logger),
	)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parser.ParseEvent(tt.input, tt.emptyEvent)
			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}

			if got != nil && tt.want != nil {
				assert.Equal(t, tt.want.Name, got.Name)
				assert.Len(t, got.Payloads, len(tt.want.Payloads))

				if got.Payloads != nil {
					for i, payload := range got.Payloads {
						// Jsoniter parser returns []byte instead of RawMessage
						if bytesPayload, ok := payload.([]byte); ok {
							got.Payloads[i] = json.RawMessage(bytesPayload)
						}
					}
				}

				for i, payload := range tt.want.Payloads {
					assert.JSONEq(t, toJSON(t, payload), toJSON(t, got.Payloads[i]))
				}
			}

			if (got == nil || tt.want == nil) && (got != tt.want) {
				assert.Fail(t, "got: %v, want: %v", got, tt.want)
			}
		})
	}

	t.Run("3rd party parser", func(t *testing.T) {

		ctrl := gomock.NewController(t)
		mockPayloadParser := mock_socketio_v5_parser_default.NewMockPayloadParser(ctrl)
		defer ctrl.Finish()

		parser := NewParser(
			WithLogger(logger),
			WithPayloadParser(mockPayloadParser),
		)

		mockPayloadParser.EXPECT().ParseEvent([]byte("123"), false).Return(&socketio_v5.Event{}, nil)

		_, err := parser.ParseEvent([]byte("123"), false)
		assert.Nil(t, err)
	})
}

func TestSocketIOV5DefaultParser_Serialize(t *testing.T) {
	t.Parallel()

	var nilValue []interface{}

	tests := []struct {
		name    string
		input   *socketio_v5.Message
		want    []byte
		wantErr bool
	}{
		{
			name:    "Nil message",
			input:   nil,
			want:    nil,
			wantErr: true,
		},
		{
			name: "Connect message",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketConnect,
				NS:   "/",
			},
			want: []byte("0"),
		},
		{
			name: "Connect message with empty payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketConnect,
				NS:      "/",
				Payload: "",
			},
			want: []byte("0"),
		},
		{
			name: "Connect message with {} payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketConnect,
				NS:      "/",
				Payload: map[string]any{},
			},
			want: []byte("0"),
		},
		{
			name: "Connect message with nil payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketConnect,
				NS:      "/",
				Payload: nilValue,
			},
			want: []byte("0"),
		},
		{
			name: "Connect message",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketConnect,
				NS:   "/",
			},
			want: []byte("0"),
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
			want: []byte(`2["test",{}]`),
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
			wantErr: true,
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
			wantErr: true,
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
			want: []byte(`2["test"]`),
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
			want: []byte(`2["test",null]`),
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
			want: []byte(`2/chat,123["message","Hello, world!"]`),
		},
		{
			name: "Binary event message",
			input: &socketio_v5.Message{
				Type: socketio_v5.PacketBinaryEvent,
			},
			wantErr: true,
		},
		{
			name: "Message with payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketConnect,
				Payload: map[string]interface{}{"token": "abc123"},
			},
			want:    []byte(`0{"token":"abc123"}`),
			wantErr: false,
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
			want: []byte(`2["test","data"]`),
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
			want: []byte(`2["test","data"]`),
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
			want: []byte(`21000000["test","data"]`),
		},
		{
			name: "Message with null payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketEvent,
				NS:      "/",
				Payload: nil,
			},
			wantErr: true,
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
			wantErr: true,
		},
		{
			name: "Channel in connect payload",
			input: &socketio_v5.Message{
				Type:    socketio_v5.PacketConnect,
				Payload: make(chan int),
			},
			wantErr: true,
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
	parser := NewParser(
		WithLogger(logger),
	)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parser.Serialize(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestThreadSafety(t *testing.T) {
	t.Parallel()
	type E struct {
		Arg int `json:"arg"`
	}

	parser := NewParser(
		WithLogger(logger),
	)

	callback := parser.WrapCallback(func(arg *E, z []int) {
		assert.Equal(t, []int{11, 12, 13}, z)
		assert.Equal(t, 33, arg.Arg)
		go func() {
			arg.Arg += 1000
			z[0] += 1000
			z[1] += 1000
			z[2] += 1000
		}()
	})

	parsedData, _ := parser.ParseEvent([]byte(`["event", {"arg": 33},[11, 12, 13]]`), false)

	for i := 0; i < 4; i++ {
		go func() {
			for i := 0; i < 100; i++ {
				callback(parsedData.Payloads)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
}

func TestWrapCallback(t *testing.T) {
	event, err := json.Marshal(typedEvent)
	assert.NoError(t, err)

	parser := NewParser(
		WithLogger(logger),
	)

	t.Run(fmt.Sprintf("%T-%s", parser, "json"), func(t *testing.T) {
		t.Parallel()

		eventParsed, err := parser.ParseEvent(event, false)
		assert.NoError(t, err)
		assert.Equal(t, typedEvent[0].(string), eventParsed.Name)

		t.Run("Typed Event", func(t *testing.T) {
			parsed := false
			callback := parser.WrapCallback(
				func(arg1 string, arg2 int, arg3 EventArgTyped, arg4 []string) {
					assert.Equal(t, typedEvent[1].(string), arg1)
					assert.Equal(t, typedEvent[2].(int), arg2)
					assert.Equal(t, typedEvent[3].(EventArgTyped), arg3)
					assert.Equal(t, typedEvent[4].([]string), arg4)
					parsed = true
				},
			)
			callback(eventParsed.Payloads)
			assert.True(t, parsed)
		})

		t.Run("Typed Event, Low params", func(t *testing.T) {
			parsed := false
			callback := parser.WrapCallback(
				func(arg1 string, arg2 int, arg3 EventArgTyped) {
					assert.Equal(t, typedEvent[1].(string), arg1)
					assert.Equal(t, typedEvent[2].(int), arg2)
					assert.Equal(t, typedEvent[3].(EventArgTyped), arg3)
					parsed = true
				},
			)
			callback(eventParsed.Payloads)
			assert.True(t, parsed)
		})

		t.Run("Typed Event, Many params", func(t *testing.T) {
			parsed := false
			callback := parser.WrapCallback(
				func(_ string, _ int, _ EventArgTyped, _ []string, _ string) {
					parsed = true
				},
			)
			callback(eventParsed.Payloads)
			assert.False(t, parsed)
		})

		t.Run("Typed Event, Wrong input", func(t *testing.T) {
			parsed := false
			callback := parser.WrapCallback(
				func(_ string) {
					parsed = true
				},
			)
			in := make([]interface{}, 0, 1)
			in = append(in, 1)
			callback(in)
			assert.False(t, parsed)
		})

		t.Run("Typed Event, Wrong params", func(t *testing.T) {
			parsed := false
			callback := parser.WrapCallback(
				func(_ int, _ int, _ int, _ int) {
					parsed = true
				},
			)
			callback(eventParsed.Payloads)
			assert.False(t, parsed)
		})

		t.Run("Raw Event", func(t *testing.T) {
			parsed := false
			callback := parser.WrapCallback(func(args []interface{}) {
				assert.Len(t, args, 4)
				parsed = true
			})
			callback(eventParsed.Payloads)
			assert.True(t, parsed)
		})

		t.Run("Wrong Callback", func(t *testing.T) {
			callback := parser.WrapCallback(1)
			assert.Nil(t, callback)
		})

		t.Run("3rd Party callback", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockPayloadParser := mock_socketio_v5_parser_default.NewMockPayloadParser(ctrl)
			defer ctrl.Finish()
			mockPayloadParser.EXPECT().WrapCallback(1).Return(nil)
			parser.payloadParser = mockPayloadParser
			callback := parser.WrapCallback(1)
			assert.Nil(t, callback)
		})
	})
}

func TestMsgSerializeRestore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		msg      *socketio_v5.Message
		callback interface{}
	}{
		{
			name: "Valid event with string params",
			msg: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "/",
				Event: &socketio_v5.Event{
					Name:     "test",
					Payloads: []interface{}{"data"},
				},
			},
			callback: func(arg1 string) {
				assert.Equal(t, "data", arg1)
			},
		},
		{
			name: "Valid event with mixed params",
			msg: &socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "/",
				Event: &socketio_v5.Event{
					Name: "test",
					Payloads: []interface{}{
						123.4,
						"data",
						42,
						struct{ A bool }{
							A: true,
						}},
				},
			},
			callback: func(arg1 float64, arg2 string, arg3 int, arg4 struct{ A bool }) {
				assert.Equal(t, 123.4, arg1)
				assert.Equal(t, "data", arg2)
				assert.Equal(t, 42, arg3)
				assert.Equal(t, struct{ A bool }{A: true}, arg4)
			},
		},
	}

	parser := NewParser(
		WithLogger(logger),
	)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			serialized, err := parser.Serialize(tt.msg)
			require.NoError(t, err)
			got, err := parser.Parse(serialized)
			require.NoError(t, err)
			if got.Event != nil {
				parser.WrapCallback(tt.callback)(got.Event.Payloads)
				// We checks typed params on callback
				got.Event.Payloads = tt.msg.Event.Payloads
				require.Equal(t, tt.msg, got)
			} else {
				assert.Nil(t, tt.callback)
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

type AStruct struct {
	B string  `json:"b"`
	C int     `json:"c"`
	D DStruct `json:"d"`
}
type BStruct struct {
	A int `json:"d"`
}
type DStruct struct {
	A string `json:"a"`
	B int    `json:"b"`
}
type EventArgTyped struct {
	A AStruct                `json:"a"`
	B BStruct                `json:"b"`
	C map[string]interface{} `json:"c"`
	D int                    `json:"d"`
	E string                 `json:"e"`
	F []string               `json:"f"`
	G *string                `json:"g"`
}

var typedEvent = []interface{}{
	"eventName",
	"test",
	1,
	EventArgTyped{
		A: AStruct{
			B: "c",
			C: 123,
			D: DStruct{
				A: "b",
				B: 123,
			},
		},
		B: BStruct{
			A: 123,
		},
		C: map[string]interface{}{
			"d": float64(123),
			"e": map[string]interface{}{
				"a": map[string]interface{}{
					"b": "c",
					"c": "d",
				},
				"c": "d",
			},
			"f": []interface{}{"a", "b", "c"},
			"g": "h",
		},
		D: 123,
		E: "e",
	},
	[]string{"a", "b", "c"},
}

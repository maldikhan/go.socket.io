package socketio_v5_parser_default_jsoniter

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	"github.com/maldikhan/go.socket.io/utils"
	"github.com/stretchr/testify/assert"
)

var logger = &utils.DefaultLogger{Level: utils.NONE}

func TestSocketIOV5DefaultParser_parseEvent(t *testing.T) {

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
	}

	parser := NewPayloadParser()
	{
		t.Run(fmt.Sprintf("%T", parser), func(t *testing.T) {
			t.Parallel()
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
		})
	}
}

func TestThreadSafety(t *testing.T) {

	type E struct {
		Arg int `json:"arg"`
	}

	parser := NewPayloadParser()
	{
		t.Run(fmt.Sprintf("%T", parser), func(t *testing.T) {
			t.Parallel()
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
		})
	}
}

func TestWrapCallback(t *testing.T) {
	event, err := json.Marshal(typedEvent)
	assert.NoError(t, err)

	parser := NewPayloadParser()
	{
		t.Run(fmt.Sprintf("%T", parser), func(t *testing.T) {
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

			t.Run("Wromg Callback", func(t *testing.T) {
				callback := parser.WrapCallback(1)
				assert.Nil(t, callback)
			})
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

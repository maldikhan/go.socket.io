package socketio_v5_parser

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	socketio_v5_client "github.com/maldikhan/go.socket.io/socket.io/v5/client"
	socketio_v5_parser_default "github.com/maldikhan/go.socket.io/socket.io/v5/parser/default"
	"github.com/maldikhan/go.socket.io/utils"
)

var logger = &utils.DefaultLogger{Level: utils.NONE}

var allParsers = map[string]socketio_v5_client.Parser{
	"json": socketio_v5_parser_default.NewParser(
		socketio_v5_parser_default.WithLogger(logger),
	),
}

func TestWrapCallback(t *testing.T) {

	for name, parser := range allParsers {
		parser := parser

		t.Run(fmt.Sprintf("%T-%s", parser, name), func(t *testing.T) {
			t.Parallel()

			event, err := parser.Serialize(&socketio_v5.Message{
				Type: socketio_v5.PacketEvent,
				NS:   "/",
				Event: &socketio_v5.Event{
					Name:     typedEvent[0].(string),
					Payloads: append([]interface{}{}, typedEvent[1:]...),
				},
			})
			assert.NoError(t, err)

			eventParsed, err := parser.Parse(event)
			assert.NoError(t, err)
			require.NotNil(t, eventParsed.Event)
			assert.Equal(t, typedEvent[0].(string), eventParsed.Event.Name)

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
				callback(eventParsed.Event.Payloads)
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
				callback(eventParsed.Event.Payloads)
				assert.True(t, parsed)
			})

			t.Run("Typed Event, Many params", func(t *testing.T) {
				parsed := false
				callback := parser.WrapCallback(
					func(_ string, _ int, _ EventArgTyped, _ []string, _ string) {
						parsed = true
					},
				)
				callback(eventParsed.Event.Payloads)
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
				callback(eventParsed.Event.Payloads)
				assert.False(t, parsed)
			})

			t.Run("Raw Event", func(t *testing.T) {
				parsed := false
				callback := parser.WrapCallback(func(args []interface{}) {
					assert.Len(t, args, 4)
					parsed = true
				})
				callback(eventParsed.Event.Payloads)
				assert.True(t, parsed)
			})

			t.Run("Wromg Callback", func(t *testing.T) {
				callback := parser.WrapCallback(1)
				assert.Nil(t, callback)
			})
		})
	}
}

func TestMsgSerializeRestore(t *testing.T) {
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

	for _, parser := range allParsers {
		parser := parser

		t.Run(fmt.Sprintf("%T", parser), func(t *testing.T) {
			t.Parallel()
			for _, tt := range tests {
				tt := tt

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
		})
	}
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

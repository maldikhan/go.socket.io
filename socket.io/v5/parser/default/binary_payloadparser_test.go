package socketio_v5_parser_default

import (
	"encoding/json"
	"testing"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	"github.com/stretchr/testify/require"
)

// Like the existing jsoniter plugin, this legacy parser uses []byte as raw JSON.
type legacyPayloadParser struct{}

func (legacyPayloadParser) ParseEvent(data []byte, ack bool) (*socketio_v5.Event, error) {
	ev, err := NewParser().ParseEvent(data, ack)
	if err == nil {
		for i, payload := range ev.Payloads {
			ev.Payloads[i] = []byte(payload.(json.RawMessage))
		}
	}
	return ev, err
}
func (legacyPayloadParser) WrapCallback(_ interface{}) func([]interface{}) {
	return func([]interface{}) {}
}

type binaryPayloadParser struct{ legacyPayloadParser }

func (binaryPayloadParser) ReconstructBinary(msg *socketio_v5.Message, attachments [][]byte) error {
	for i, payload := range msg.Event.Payloads {
		msg.Event.Payloads[i] = json.RawMessage(payload.([]byte))
	}
	return NewParser().ReconstructBinary(msg, attachments)
}

func TestBinaryLegacyPayloadParserPreservesText(t *testing.T) {
	t.Parallel()
	p := NewParser(WithPayloadParser(legacyPayloadParser{}))
	event := &socketio_v5.Event{Name: "upload", Payloads: []interface{}{[]byte{1, 2}}}
	require.False(t, p.HasBinary(event))
	msg := &socketio_v5.Message{Type: socketio_v5.PacketEvent, NS: "/", Event: event}
	text, err := p.Serialize(msg)
	require.NoError(t, err)
	require.Equal(t, `2["upload","AQI="]`, string(text))
	_, _, err = p.SerializeBinary(msg)
	require.ErrorContains(t, err, "custom payload parser does not support binary")
	binaryMsg, err := p.Parse([]byte(`51-["upload",{"_placeholder":true,"num":0}]`))
	require.NoError(t, err)
	require.ErrorContains(t, p.ReconstructBinary(binaryMsg, [][]byte{{1}}), "custom payload parser does not support binary")
}

func TestBinaryPayloadParserOwnsReconstruction(t *testing.T) {
	t.Parallel()
	p := NewParser(WithPayloadParser(binaryPayloadParser{}))
	require.True(t, p.HasBinary(&socketio_v5.Event{Payloads: []interface{}{[]byte{1}}}))
	msg, err := p.Parse([]byte(`51-["upload",{"_placeholder":true,"num":0}]`))
	require.NoError(t, err)
	require.NoError(t, p.ReconstructBinary(msg, [][]byte{{1, 2}}))
	require.Equal(t, []byte{1, 2}, msg.Event.Payloads[0])
	require.Nil(t, msg.BinaryAttachments)
}

func TestBinaryReconstructionPreservesLargeIntegers(t *testing.T) {
	t.Parallel()
	p := NewParser()
	msg, err := p.Parse([]byte(`51-["upload",{"file":{"_placeholder":true,"num":0},"id":9007199254740993},9223372036854775807]`))
	require.NoError(t, err)
	require.NoError(t, p.ReconstructBinary(msg, [][]byte{{1, 2}}))
	type payload struct {
		File []byte
		ID   int64
	}
	var got payload
	var number int64
	p.WrapCallback(func(v payload, id int64) { got = v; number = id })(msg.Event.Payloads)
	require.Equal(t, int64(9007199254740993), got.ID)
	require.Equal(t, int64(9223372036854775807), number)
	require.Equal(t, []byte{1, 2}, got.File)
}

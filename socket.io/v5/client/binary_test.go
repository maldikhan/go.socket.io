package socketio_v5_client

import (
	"context"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	mocks "github.com/maldikhan/go.socket.io/socket.io/v5/client/mocks"
	parser "github.com/maldikhan/go.socket.io/socket.io/v5/parser/default"
	"github.com/maldikhan/go.socket.io/utils"
)

// newBinaryClient builds a client backed by the real default parser (so the
// binary serialize/reconstruct logic runs for real) and a mock engine.io client
// used to capture or inject frames.
func newBinaryClient(t *testing.T) (*Client, *mocks.MockEngineIOClient) {
	ctrl := gomock.NewController(t)
	mockEngine := mocks.NewMockEngineIOClient(ctrl)

	client := &Client{
		parser:        parser.NewParser(parser.WithLogger(&utils.DefaultLogger{Level: utils.NONE})),
		engineio:      mockEngine,
		logger:        &utils.DefaultLogger{Level: utils.NONE},
		timer:         mocks.NewMockTimer(ctrl),
		ctx:           context.Background(),
		namespaces:    make(map[string]*namespace),
		ackCallbacks:  make(map[int]func([]interface{})),
		handshakeData: make(map[string]interface{}),
	}
	client.defaultNs = client.namespace("/")
	// Mark default namespace as connected so Emit does not block.
	client.defaultNs.hadConnected.Do(func() { close(client.defaultNs.waitConnected) })
	return client, mockEngine
}

func TestClient_Emit_Binary(t *testing.T) {
	client, mockEngine := newBinaryClient(t)

	var sent []byte
	var binaries [][]byte
	var mu sync.Mutex

	mockEngine.EXPECT().Send(gomock.Any()).DoAndReturn(func(data []byte) error {
		mu.Lock()
		sent = append([]byte{}, data...)
		mu.Unlock()
		return nil
	})
	mockEngine.EXPECT().SendBinary(gomock.Any()).DoAndReturn(func(data []byte) error {
		mu.Lock()
		binaries = append(binaries, append([]byte{}, data...))
		mu.Unlock()
		return nil
	}).Times(2)

	require.NoError(t, client.Emit("binEvent", []byte("first"), "text", []byte("second")))

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []byte(`52-["binEvent",{"_placeholder":true,"num":0},"text",{"_placeholder":true,"num":1}]`), sent)
	require.Len(t, binaries, 2)
	assert.Equal(t, []byte("first"), binaries[0])
	assert.Equal(t, []byte("second"), binaries[1])
}

func TestClient_Emit_TextStillSingleSend(t *testing.T) {
	client, mockEngine := newBinaryClient(t)

	// A text-only event must use the regular Send path (no SendBinary calls).
	mockEngine.EXPECT().Send([]byte(`2["textEvent","hello"]`)).Return(nil)

	require.NoError(t, client.Emit("textEvent", "hello"))
}

func TestClient_ReceiveBinaryEvent(t *testing.T) {
	client, _ := newBinaryClient(t)

	got := make(chan []byte, 1)
	client.On("binEvent", func(data []byte) {
		got <- data
	})

	// Header announces one attachment; the attachment frame follows.
	client.onMessage([]byte(`51-["binEvent",{"_placeholder":true,"num":0}]`))
	client.onBinaryAttachment([]byte{0xAA, 0xBB, 0xCC})

	assert.Equal(t, []byte{0xAA, 0xBB, 0xCC}, <-got)
}

func TestClient_ReceiveBinaryEvent_MultiAttachment(t *testing.T) {
	client, _ := newBinaryClient(t)

	got := make(chan []interface{}, 1)
	client.OnAny(func(_ string, payloads []interface{}) {
		got <- payloads
	})

	client.onMessage([]byte(`52-["multi",{"_placeholder":true,"num":0},"text",{"_placeholder":true,"num":1}]`))
	client.onBinaryAttachment([]byte("aaa"))
	client.onBinaryAttachment([]byte("bbb"))

	payloads := <-got
	require.Len(t, payloads, 3)
	assert.Equal(t, []byte("aaa"), payloads[0])
	assert.Equal(t, "text", payloads[1])
	assert.Equal(t, []byte("bbb"), payloads[2])
}

func TestClient_ReceiveBinaryAck(t *testing.T) {
	client, _ := newBinaryClient(t)

	got := make(chan []interface{}, 1)
	client.mutex.Lock()
	client.ackCallbacks[5] = func(args []interface{}) { got <- args }
	client.mutex.Unlock()

	client.onMessage([]byte(`61-5[{"_placeholder":true,"num":0}]`))
	client.onBinaryAttachment([]byte("ackdata"))

	payloads := <-got
	require.Len(t, payloads, 1)
	assert.Equal(t, []byte("ackdata"), payloads[0])
}

func TestClient_ReceiveBinaryZeroAttachments(t *testing.T) {
	client, _ := newBinaryClient(t)

	got := make(chan []interface{}, 1)
	client.On("noAttach", func(payloads []interface{}) {
		got <- payloads
	})

	// Zero attachments: the message is complete immediately.
	client.onMessage([]byte(`50-["noAttach","plain"]`))

	payloads := <-got
	require.Len(t, payloads, 1)
}

func TestClient_ReceiveBinary_ReconstructError(t *testing.T) {
	// A binary header that declares a placeholder index out of range fails
	// reconstruction; the message is dropped without panic.
	client, _ := newBinaryClient(t)
	client.onMessage([]byte(`51-["ev",{"_placeholder":true,"num":9}]`))
	client.onBinaryAttachment([]byte{1})
}

func TestClient_ReceiveBinary_ZeroAttachmentReconstructError(t *testing.T) {
	// Zero-attachment header whose placeholder points past the (empty)
	// attachment list: reconstruction fails immediately and is dropped.
	client, _ := newBinaryClient(t)
	client.onMessage([]byte(`50-["ev",{"_placeholder":true,"num":0}]`))
}

func TestClient_BinaryAttachmentWithoutPending(t *testing.T) {
	client, _ := newBinaryClient(t)
	// No pending binary header: the stray attachment is dropped without panic.
	client.onBinaryAttachment([]byte{1, 2, 3})
}

func TestClient_RoundTrip_EmitToReceive(t *testing.T) {
	// Emit a binary event, capture the wire frames, then feed them back into a
	// receiving client and assert the []byte survives the full path.
	sender, senderEngine := newBinaryClient(t)

	var header []byte
	var attachments [][]byte
	var mu sync.Mutex
	senderEngine.EXPECT().Send(gomock.Any()).DoAndReturn(func(d []byte) error {
		mu.Lock()
		header = append([]byte{}, d...)
		mu.Unlock()
		return nil
	})
	senderEngine.EXPECT().SendBinary(gomock.Any()).DoAndReturn(func(d []byte) error {
		mu.Lock()
		attachments = append(attachments, append([]byte{}, d...))
		mu.Unlock()
		return nil
	}).AnyTimes()

	require.NoError(t, sender.Emit("roundtrip", []byte{0xDE, 0xAD, 0xBE, 0xEF}))

	receiver, _ := newBinaryClient(t)
	got := make(chan []byte, 1)
	receiver.On("roundtrip", func(data []byte) { got <- data })

	mu.Lock()
	h := header
	a := attachments
	mu.Unlock()

	receiver.onMessage(h)
	for _, attachment := range a {
		receiver.onBinaryAttachment(attachment)
	}

	assert.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, <-got)
}

// TestClient_sendPacket_BinarySendError covers the error branches when the
// engine.io client fails to deliver a binary header or attachment.
func TestClient_sendPacket_BinarySendError(t *testing.T) {
	t.Run("header send fails", func(t *testing.T) {
		client, mockEngine := newBinaryClient(t)
		mockEngine.EXPECT().Send(gomock.Any()).Return(errBinaryTestClient)
		err := client.sendPacket(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{[]byte{1}},
			},
		})
		assert.Error(t, err)
	})

	t.Run("attachment send fails", func(t *testing.T) {
		client, mockEngine := newBinaryClient(t)
		mockEngine.EXPECT().Send(gomock.Any()).Return(nil)
		mockEngine.EXPECT().SendBinary(gomock.Any()).Return(errBinaryTestClient)
		err := client.sendPacket(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{[]byte{1}},
			},
		})
		assert.Error(t, err)
	})

	t.Run("serialize binary fails", func(t *testing.T) {
		client, _ := newBinaryClient(t)
		// An unserializable payload alongside binary triggers SerializeBinary error.
		err := client.sendPacket(&socketio_v5.Message{
			Type: socketio_v5.PacketEvent,
			NS:   "/",
			Event: &socketio_v5.Event{
				Name:     "ev",
				Payloads: []interface{}{[]byte{1}, make(chan int)},
			},
		})
		assert.Error(t, err)
	})
}

var errBinaryTestClient = &binaryClientError{}

type binaryClientError struct{}

func (e *binaryClientError) Error() string { return "binary client error" }

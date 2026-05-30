package engineio_v4_client

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	mocks "github.com/maldikhan/go.socket.io/engine.io/v4/client/mocks"
)

// newBinaryTestClient builds a Client wired with fresh mocks for the binary
// tests. Debugf is allowed any number of times since the binary paths log.
func newBinaryTestClient(t *testing.T) (*Client, *mocks.MockTransport, *mocks.MockParser, *mocks.MockLogger) {
	ctrl := gomock.NewController(t)
	mockTransport := mocks.NewMockTransport(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

	client := &Client{
		log:       mockLogger,
		transport: mockTransport,
		parser:    mockParser,
	}
	return client, mockTransport, mockParser, mockLogger
}

func TestClient_splitRecords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  [][]byte
	}{
		{
			name:  "no separator yields single record",
			input: []byte("4hello"),
			want:  [][]byte{[]byte("4hello")},
		},
		{
			name:  "two records",
			input: []byte("4hello\x1e4world"),
			want:  [][]byte{[]byte("4hello"), []byte("4world")},
		},
		{
			name:  "trailing separator yields empty tail",
			input: []byte("4a\x1e"),
			want:  [][]byte{[]byte("4a"), {}},
		},
		{
			name:  "empty input yields one empty record",
			input: []byte{},
			want:  [][]byte{{}},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, splitRecords(tt.input))
		})
	}
}

func TestClient_handleFrame_Binary(t *testing.T) {
	client, _, _, _ := newBinaryTestClient(t)

	got := make(chan []byte, 1)
	client.On("binary", func(data []byte) {
		got <- data
	})

	err := client.handleFrame(engineio_v4.Frame{Data: []byte{1, 2, 3}, IsBinary: true})
	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3}, <-got)
}

func TestClient_handleFrame_BinaryNoHandler(t *testing.T) {
	client, _, _, _ := newBinaryTestClient(t)
	// No binary handler registered: must not panic and returns nil.
	err := client.handleFrame(engineio_v4.Frame{Data: []byte{1}, IsBinary: true})
	assert.NoError(t, err)
}

func TestClient_handleFrame_EmptyRecordSkipped(t *testing.T) {
	client, _, mockParser, _ := newBinaryTestClient(t)

	// A trailing separator produces an empty record that must be skipped (only
	// the non-empty "4a" record is parsed).
	mockParser.EXPECT().Parse([]byte("4a")).Return(&engineio_v4.Message{
		Type: engineio_v4.PacketMessage, Data: []byte("a"),
	}, nil)

	err := client.handleFrame(engineio_v4.Frame{Data: []byte("4a\x1e")})
	require.NoError(t, err)
}

func TestClient_handleFrame_MultiRecord(t *testing.T) {
	client, _, mockParser, _ := newBinaryTestClient(t)

	got := make(chan []byte, 2)
	client.On("message", func(data []byte) { got <- data })

	mockParser.EXPECT().Parse([]byte("4a")).Return(&engineio_v4.Message{
		Type: engineio_v4.PacketMessage, Data: []byte("a"),
	}, nil)
	mockParser.EXPECT().Parse([]byte("4b")).Return(&engineio_v4.Message{
		Type: engineio_v4.PacketMessage, Data: []byte("b"),
	}, nil)

	err := client.handleFrame(engineio_v4.Frame{Data: []byte("4a\x1e4b")})
	require.NoError(t, err)
	assert.Equal(t, []byte("a"), <-got)
	assert.Equal(t, []byte("b"), <-got)
}

func TestClient_handleFrame_RecordParseError(t *testing.T) {
	client, _, mockParser, _ := newBinaryTestClient(t)

	mockParser.EXPECT().Parse(gomock.Any()).Return(nil, assertErr())

	err := client.handleFrame(engineio_v4.Frame{Data: []byte("bad")})
	assert.Error(t, err)
}

func TestClient_handlePacket_BinaryMessage(t *testing.T) {
	client, _, mockParser, _ := newBinaryTestClient(t)

	got := make(chan []byte, 1)
	client.On("binary", func(data []byte) { got <- data })

	mockParser.EXPECT().Parse(gomock.Any()).Return(&engineio_v4.Message{
		Type:   engineio_v4.PacketMessage,
		Data:   []byte{9, 8, 7},
		Binary: true,
	}, nil)

	err := client.handleFrame(engineio_v4.Frame{Data: []byte("bCQgH")})
	require.NoError(t, err)
	assert.Equal(t, []byte{9, 8, 7}, <-got)
}

func TestClient_handlePacket_BinaryMessageNoHandler(t *testing.T) {
	client, _, mockParser, _ := newBinaryTestClient(t)

	mockParser.EXPECT().Parse(gomock.Any()).Return(&engineio_v4.Message{
		Type:   engineio_v4.PacketMessage,
		Data:   []byte{1},
		Binary: true,
	}, nil)

	// No binary handler registered: must not panic.
	err := client.handleFrame(engineio_v4.Frame{Data: []byte("bAQ==")})
	require.NoError(t, err)
}

func TestClient_On_BinaryHandlerRegistration(t *testing.T) {
	client, _, _, _ := newBinaryTestClient(t)
	// Registering the "binary" handler must set binaryHandler so binary frames
	// are routed to it.
	called := make(chan struct{}, 1)
	client.On("binary", func([]byte) { called <- struct{}{} })

	client.handlerMu.RLock()
	h := client.binaryHandler
	client.handlerMu.RUnlock()
	require.NotNil(t, h)
	h(nil)
	<-called
}

// TestClient_On_AllEvents registers every supported event and invokes the
// stored wrappers so each On case (including the connect/close zero-arg
// adapters) is exercised.
func TestClient_On_AllEvents(t *testing.T) {
	client, _, _, _ := newBinaryTestClient(t)

	hits := make(chan string, 4)
	client.On("connect", func([]byte) { hits <- "connect" })
	client.On("message", func([]byte) { hits <- "message" })
	client.On("binary", func([]byte) { hits <- "binary" })
	client.On("close", func([]byte) { hits <- "close" })

	client.handlerMu.RLock()
	connect := client.afterConnect
	message := client.messageHandler
	binary := client.binaryHandler
	closeH := client.closeHandler
	client.handlerMu.RUnlock()

	require.NotNil(t, connect)
	require.NotNil(t, message)
	require.NotNil(t, binary)
	require.NotNil(t, closeH)

	connect()
	message(nil)
	binary(nil)
	closeH()

	got := map[string]bool{}
	for i := 0; i < 4; i++ {
		got[<-hits] = true
	}
	assert.Equal(t, map[string]bool{"connect": true, "message": true, "binary": true, "close": true}, got)
}

func TestClient_SendBinary(t *testing.T) {
	t.Run("delivers to transport", func(t *testing.T) {
		client, mockTransport, _, _ := newBinaryTestClient(t)
		mockTransport.EXPECT().SendBinary([]byte{1, 2, 3}).Return(nil)
		assert.NoError(t, client.SendBinary([]byte{1, 2, 3}))
	})

	t.Run("waits on handshake and upgrade gates", func(t *testing.T) {
		client, mockTransport, _, _ := newBinaryTestClient(t)
		wh := make(chan struct{})
		wu := make(chan struct{})
		client.waitHandshake = wh
		client.waitUpgrade = wu
		close(wh)
		close(wu)
		mockTransport.EXPECT().SendBinary(gomock.Any()).Return(nil)
		assert.NoError(t, client.SendBinary([]byte{4}))
	})

	t.Run("closed client", func(t *testing.T) {
		client, _, _, _ := newBinaryTestClient(t)
		client.transport = nil
		err := client.SendBinary([]byte{1})
		assert.Error(t, err)
	})
}

// assertErr is a small helper returning a deterministic error for the binary
// tests.
func assertErr() error {
	return errBinaryTest
}

var errBinaryTest = &binaryTestError{}

type binaryTestError struct{}

func (e *binaryTestError) Error() string { return "binary test error" }

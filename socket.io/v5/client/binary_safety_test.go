package socketio_v5_client

import (
	"testing"

	"github.com/golang/mock/gomock"
	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	"github.com/stretchr/testify/require"
)

// These adapters deliberately expose only the pre-binary dependency interfaces.
// Keeping them valid verifies third-party parser/client source compatibility.
type textOnlyParser struct{ Parser }
type textOnlyEngine struct{ EngineIOClient }

func TestBinaryCustomParserUsesOwnSerialization(t *testing.T) {
	c, engine := newBinaryClient(t)
	c.parser = textOnlyParser{c.parser}
	engine.EXPECT().Send([]byte(`2["upload","AQI="]`)).Return(nil)
	require.NoError(t, c.Emit("upload", []byte{1, 2}))
}

func TestBinaryUnsupportedTransportDoesNotSendHeader(t *testing.T) {
	c, _ := newBinaryClient(t)
	c.engineio = textOnlyEngine{c.engineio}
	require.ErrorContains(t, c.Emit("upload", []byte{1}), "does not support binary")
}

func TestBinaryCustomParserCannotStageAttachments(t *testing.T) {
	c, _ := newBinaryClient(t)
	c.parser = textOnlyParser{c.parser}
	c.onMessage([]byte(`51-["upload",{"_placeholder":true,"num":0}]`))
	require.Nil(t, c.pendingBinary)
	c.onBinaryAttachment([]byte{1})
}

func TestBinaryAttachmentAggregateLimit(t *testing.T) {
	c, _ := newBinaryClient(t)
	c.maxBinarySize = 4
	c.onMessage([]byte(`52-["upload",{"_placeholder":true,"num":0},{"_placeholder":true,"num":1}]`))
	c.onBinaryAttachment([]byte{1, 2, 3})
	require.Equal(t, 3, c.pendingBytes)
	c.onBinaryAttachment([]byte{4, 5})
	require.Nil(t, c.pendingBinary)
	require.Nil(t, c.pendingAttachments)
	require.Zero(t, c.pendingBytes)
	c.onBinaryAttachment([]byte{6})
}

func TestBinaryAttachmentAtAggregateLimit(t *testing.T) {
	c, _ := newBinaryClient(t)
	c.maxBinarySize = 4
	got := make(chan []byte, 1)
	c.On("upload", func(b []byte) { got <- b })
	c.onMessage([]byte(`51-["upload",{"_placeholder":true,"num":0}]`))
	c.onBinaryAttachment([]byte{1, 2, 3, 4})
	require.Equal(t, []byte{1, 2, 3, 4}, <-got)
	require.Zero(t, c.pendingBytes)
}

func TestBinaryTextInterruptionDropsBothHeaders(t *testing.T) {
	for _, text := range []string{`2["text"]`, `51-["second",{"_placeholder":true,"num":0}]`, `50-["second"]`, `bad`} {
		t.Run(text, func(t *testing.T) {
			c, _ := newBinaryClient(t)
			c.onMessage([]byte(`52-["first",{"_placeholder":true,"num":0},{"_placeholder":true,"num":1}]`))
			c.onBinaryAttachment([]byte("first"))
			c.onMessage([]byte(text))
			require.Nil(t, c.pendingBinary)
			require.Zero(t, c.pendingBytes)
			c.onBinaryAttachment([]byte("old-second"))
			require.Nil(t, c.pendingBinary)
		})
	}
}

func TestBinaryNegativeCustomAttachmentCount(t *testing.T) {
	c, _ := newBinaryClient(t)
	count := -1
	require.True(t, c.stageBinary(&socketio_v5.Message{BinaryAttachments: &count}))
	require.Nil(t, c.pendingBinary)
}

func TestBinaryCloseReleasesPendingAttachments(t *testing.T) {
	c, engine := newBinaryClient(t)
	c.onMessage([]byte(`52-["upload",{"_placeholder":true,"num":0},{"_placeholder":true,"num":1}]`))
	c.onBinaryAttachment([]byte{1})
	engine.EXPECT().Close().Return(nil)
	require.NoError(t, c.Close())
	require.Nil(t, c.pendingBinary)
	require.Nil(t, c.pendingAttachments)
}

func TestBinaryReconnectReleasesPendingAttachments(t *testing.T) {
	c, engine := newBinaryClient(t)
	c.onMessage([]byte(`52-["upload",{"_placeholder":true,"num":0},{"_placeholder":true,"num":1}]`))
	c.onBinaryAttachment([]byte{1})
	engine.EXPECT().Send(gomock.Any()).Return(nil)
	c.connectSocketIO(nil)
	require.Nil(t, c.pendingBinary)
}

func TestWithMaxBinarySize(t *testing.T) {
	init := &InitClient{Client: &Client{}}
	require.Error(t, WithMaxBinarySize(0)(init))
	require.Error(t, WithMaxBinarySize(-1)(init))
	require.NoError(t, WithMaxBinarySize(4)(init))
	require.Equal(t, 4, init.maxBinarySize)
}

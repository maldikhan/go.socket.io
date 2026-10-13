package engineio_v4_client_transport

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	mocks "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket/mocks"
	"github.com/maldikhan/go.socket.io/utils"
	"github.com/stretchr/testify/require"
)

type textOnlyWebSocket struct{ WebSocket }

func TestCustomTextWebSocketUsesReceive(t *testing.T) {
	ctrl := gomock.NewController(t)
	ws := mocks.NewMockWebSocket(ctrl)
	frames := make(chan engineio_v4.Frame, 1)
	closed := make(chan error, 1)
	transport := &Transport{ws: textOnlyWebSocket{ws}, ctx: context.Background(), log: &utils.DefaultLogger{Level: utils.NONE}, messages: frames, onClose: closed, stopPooling: make(chan struct{}, 1)}
	gomock.InOrder(
		ws.EXPECT().Receive(gomock.Any()).DoAndReturn(func(data *[]byte) error { *data = []byte("4hello"); return nil }),
		ws.EXPECT().Receive(gomock.Any()).Return(io.EOF),
	)
	done := make(chan error, 1)
	go func() { done <- transport.wsReadLoop() }()
	select {
	case frame := <-frames:
		require.False(t, frame.IsBinary)
		require.Equal(t, []byte("4hello"), frame.Data)
	case <-time.After(time.Second):
		t.Fatal("legacy websocket did not deliver text frame")
	}
	require.ErrorIs(t, <-done, io.EOF)
	require.ErrorContains(t, transport.SendBinary([]byte{1}), "does not support binary")
}

func TestSupportsBinaryRejectsTextOnlyWebSocket(t *testing.T) {
	ctrl := gomock.NewController(t)
	ws := mocks.NewMockWebSocket(ctrl)
	transport := &Transport{ws: ws}
	require.True(t, transport.SupportsBinary())
	transport.ws = textOnlyWebSocket{ws}
	require.False(t, transport.SupportsBinary())
}

type capabilityWebSocket struct {
	*mocks.MockWebSocket
	enabled bool
}

func (ws capabilityWebSocket) SupportsBinary() bool { return ws.enabled }

func TestSupportsBinaryPropagatesWebSocketCapability(t *testing.T) {
	ctrl := gomock.NewController(t)
	ws := mocks.NewMockWebSocket(ctrl)
	transport := &Transport{ws: capabilityWebSocket{MockWebSocket: ws, enabled: false}}
	require.False(t, transport.SupportsBinary())
	transport.ws = capabilityWebSocket{MockWebSocket: ws, enabled: true}
	require.True(t, transport.SupportsBinary())
}

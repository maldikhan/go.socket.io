package engineio_v4_client_transport

import (
	"context"
	"errors"
	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	"net/url"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	mocks "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket/mocks"
	"github.com/maldikhan/go.socket.io/utils"
	"github.com/stretchr/testify/require"
)

func TestRunNotifiesCloseAfterWebSocketCleanup(t *testing.T) {
	ctrl := gomock.NewController(t)
	ws := mocks.NewMockWebSocket(ctrl)
	transport, err := NewTransport(WithWebSocket(ws), WithLogger(&utils.DefaultLogger{Level: utils.NONE}))
	require.NoError(t, err)
	closeStarted := make(chan struct{})
	allowClose := make(chan struct{})
	ws.EXPECT().Dial(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	ws.EXPECT().ReceiveFrame(gomock.Any()).Return(false, errors.New("dropped"))
	ws.EXPECT().Close().DoAndReturn(func() error {
		close(closeStarted)
		<-allowClose
		return nil
	})
	onClose := make(chan error, 1)
	u, _ := url.Parse("http://localhost/socket.io/")
	require.NoError(t, transport.Run(context.Background(), u, "", make(chan engineio_v4.Frame, 1), onClose))
	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("WebSocket cleanup did not start")
	}
	select {
	case <-onClose:
		t.Error("drop was published before WebSocket cleanup, allowing a reconnect to race it")
	default:
	}
	close(allowClose)
	select {
	case err := <-onClose:
		require.EqualError(t, err, "dropped")
	case <-time.After(time.Second):
		t.Fatal("drop notification missing after cleanup")
	}
}

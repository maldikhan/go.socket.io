package socketio_v5_client

import (
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
)

func TestCloseReleasesPendingNamespaceEmit(t *testing.T) {
	client, _, engine, parser := captureEngineHandlers(t)
	engine.EXPECT().Close().Return(nil)
	engine.EXPECT().Send(gomock.Any()).Return(errors.New("client is closed"))
	parser.EXPECT().Serialize(gomock.Any()).Return([]byte("packet"), nil)
	done := make(chan error, 1)
	go func() { done <- client.Emit("pending") }()
	require.NoError(t, client.Close())
	select {
	case err := <-done:
		require.EqualError(t, err, "client is closed")
	case <-time.After(time.Second):
		t.Fatal("Emit stayed blocked on namespace CONNECT after Close")
	}
}

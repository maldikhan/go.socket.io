package engineio_v4_client

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	"github.com/stretchr/testify/require"
)

func TestReconnectLifecycleHandlerCanClose(t *testing.T) {
	for _, event := range []string{"reconnecting", "reconnect_failed"} {
		t.Run(event, func(t *testing.T) {
			client, transport, _, _ := newReconnectClient(t)
			client.reconnectAttempts = 0
			client.closeCh = make(chan struct{})
			client.supervisorDone = make(chan struct{})
			atomic.StoreUint32(&client.supervisorStarted, 1)
			transport.EXPECT().Stop().Return(nil).AnyTimes()
			called := make(chan error, 1)
			client.On(event, func([]byte) { called <- client.Close() })
			client.transportClosed <- errors.New("dropped")
			go client.supervise(client.transportClosed, client.supervisorDone)
			select {
			case err := <-called:
				require.NoError(t, err)
			case <-time.After(time.Second):
				t.Fatal("Close deadlocked in " + event + " handler")
			}
			select {
			case <-client.supervisorDone:
			case <-time.After(time.Second):
				t.Fatal("supervisor did not exit after callback Close")
			}
		})
	}
}

func TestTransportCallbackCanClose(t *testing.T) {
	for _, event := range []string{"message", "close"} {
		t.Run(event, func(t *testing.T) {
			client, transport, parser, _ := newReconnectClient(t)
			messages := make(chan engineio_v4.Frame, 1)
			client.messages = messages
			client.messagesDone = make(chan struct{})
			closed := client.transportClosed
			transport.EXPECT().Stop().DoAndReturn(func() error { closed <- nil; return nil })
			packetType := engineio_v4.PacketMessage
			if event == "close" {
				packetType = engineio_v4.PacketClose
			}
			parser.EXPECT().Parse(gomock.Any()).Return(&engineio_v4.Message{Type: packetType}, nil)
			returned := make(chan error, 1)
			client.On(event, func([]byte) { returned <- client.Close() })
			loopDone := client.messagesDone
			go client.messageLoop(client.ctx, messages, loopDone, nil)
			messages <- engineio_v4.Frame{Data: []byte("packet")}
			select {
			case err := <-returned:
				require.NoError(t, err)
			case <-time.After(time.Second):
				t.Fatal("Close deadlocked in transport " + event + " handler")
			}
			select {
			case <-loopDone:
			case <-time.After(time.Second):
				t.Fatal("message loop did not finish")
			}
		})
	}
}

func TestSendWaitingForHandshakeReturnsOnCancellation(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	client.ctx = ctx
	client.waitHandshake = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- client.Send([]byte("pending")) }()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Send stayed blocked on handshake after context cancellation")
	}
}

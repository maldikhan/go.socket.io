package engineio_v4_client

import (
	"context"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	wsTransport "github.com/maldikhan/go.socket.io/engine.io/v4/client/transport/websocket"
	parser "github.com/maldikhan/go.socket.io/engine.io/v4/parser"
	"github.com/maldikhan/go.socket.io/utils"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"
)

func TestCloseBeforeFirstConnect(t *testing.T) {
	client, err := NewClient(WithRawURL("http://localhost/socket.io/"), WithLogger(&utils.DefaultLogger{Level: utils.NONE}))
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { result <- client.Close() }()
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close waited for a transport that was never started")
	}
	require.NoError(t, client.Close())
}

func TestCloseWhileOpenArrivesBeforeHandshakeRequestReturns(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)
	client.parser = &parser.EngineIOV4Parser{}
	requestStarted := make(chan struct{})
	allowRequest := make(chan struct{})
	stopCalled := make(chan struct{})
	connected := make(chan struct{}, 1)
	client.On("connect", func([]byte) { connected <- struct{}{} })
	var stopped sync.Once
	var closed chan<- error
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), "", gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ interface{}, _ string, messages chan<- engineio_v4.Frame, onClose chan<- error) error {
			closed = onClose
			messages <- engineio_v4.Frame{Data: []byte(`0{"sid":"early-open","pingInterval":1000,"pingTimeout":1000}`)}
			return nil
		})
	transport.EXPECT().SetHandshake(gomock.Any())
	transport.EXPECT().RequestHandshake().DoAndReturn(func() error {
		close(requestStarted)
		<-allowRequest
		return nil
	})
	transport.EXPECT().Stop().DoAndReturn(func() error {
		stopped.Do(func() { closed <- nil; close(stopCalled) })
		return nil
	}).AnyTimes()
	connectResult := make(chan error, 1)
	go func() { connectResult <- client.Connect(context.Background()) }()
	<-requestStarted
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("OPEN was not processed during RequestHandshake")
	}
	closeResult := make(chan error, 1)
	go func() { closeResult <- client.Close() }()
	<-stopCalled
	close(allowRequest)
	select {
	case err := <-connectResult:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("connection attempt competed with supervisor for transport close")
	}
	select {
	case err := <-closeResult:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close did not finish the in-flight handshake teardown")
	}
}

func TestCloseJoinsTransportAfterConnectionContextCancellation(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)
	client.parser = &parser.EngineIOV4Parser{}
	client.connectTimeout = time.Second
	allowStopped := make(chan struct{})
	stopCalled := make(chan struct{})
	var stopOnce sync.Once
	var closed chan<- error
	transport.EXPECT().Run(gomock.Any(), gomock.Any(), "", gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ interface{}, _ string, messages chan<- engineio_v4.Frame, onClose chan<- error) error {
			closed = onClose
			messages <- engineio_v4.Frame{Data: []byte(`0{"sid":"cancel","pingInterval":1000,"pingTimeout":1000}`)}
			return nil
		})
	transport.EXPECT().SetHandshake(gomock.Any())
	transport.EXPECT().RequestHandshake().Return(nil)
	transport.EXPECT().Stop().DoAndReturn(func() error {
		stopOnce.Do(func() {
			close(stopCalled)
			go func() { <-allowStopped; closed <- context.Canceled }()
		})
		return nil
	}).AnyTimes()
	require.NoError(t, client.Connect(context.Background()))
	require.Eventually(t, func() bool { return atomic.LoadUint32(&client.supervisorStarted) == 1 }, time.Second, time.Millisecond)
	result := make(chan error, 1)
	go func() { result <- client.Close() }()
	<-stopCalled
	select {
	case <-result:
		t.Error("Close returned before the transport's cleanup notification")
	case <-time.After(20 * time.Millisecond):
	}
	close(allowStopped)
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close did not return after transport cleanup")
	}
}

func TestRealWebSocketRepeatedCancelAndCloseWithIncomingPackets(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		if websocket.Message.Send(ws, `0{"sid":"flood","pingInterval":1000,"pingTimeout":1000}`) != nil {
			return
		}
		for {
			if websocket.Message.Send(ws, "4incoming") != nil {
				return
			}
		}
	}))
	defer server.Close()
	for i := 0; i < 20; i++ {
		transport, err := wsTransport.NewTransport(wsTransport.WithLogger(&utils.DefaultLogger{Level: utils.NONE}))
		require.NoError(t, err)
		client, err := NewClient(WithRawURL(server.URL), WithTransport(transport), WithSupportedTransports([]Transport{transport}),
			WithConnectTimeout(time.Second), WithLogger(&utils.DefaultLogger{Level: utils.NONE}))
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		require.NoError(t, client.Connect(ctx))
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); cancel() }()
		go func() { defer wg.Done(); _ = client.Close() }()
		wg.Wait()
	}
}

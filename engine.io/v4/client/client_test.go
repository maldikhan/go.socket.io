package engineio_v4_client

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
	mocks "github.com/maldikhan/go.socket.io/engine.io/v4/client/mocks"
)

func TestClient_Connect(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockTransport := mocks.NewMockTransport(ctrl)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	testURL, _ := url.Parse("http://localhost")
	client := &Client{
		url:                 testURL,
		log:                 mockLogger,
		parser:              mockParser,
		supportedTransports: map[engineio_v4.EngineIOTransport]Transport{engineio_v4.TransportPolling: mockTransport},
		transport:           mockTransport,
		messages:            make(chan []byte, 100),
		transportClosed:     make(chan error, 1),
		ctx:                 ctx,
	}

	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

	t.Run("Successful connect", func(t *testing.T) {

		connected := make(chan struct{}, 1)
		client.afterConnect = func() { close(connected) }

		handshakeResp := &engineio_v4.HandshakeResponse{
			Sid:          "test-sid",
			PingInterval: 25000,
			PingTimeout:  5000,
			Upgrades:     []string{},
		}
		respData, _ := json.Marshal(handshakeResp)

		client.transportClosed = make(chan error, 1)
		mockTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockTransport.EXPECT().RequestHandshake().DoAndReturn(func() error {
			go func() {
				client.messages <- append([]byte{'0'}, respData...)
			}()
			return nil
		})
		mockParser.EXPECT().Parse(gomock.Any()).Return(&engineio_v4.Message{
			Type: engineio_v4.PacketOpen,
			Data: respData,
		}, nil)
		mockTransport.EXPECT().SetHandshake(gomock.Any())
		mockTransport.EXPECT().Stop().Do(func() {
			close(client.transportClosed)
		})

		err := client.Connect(ctx)
		require.NoError(t, err)

		select {
		case <-connected:
		case <-time.After(100 * time.Millisecond):
			require.Fail(t, "afterConnect not called")
		}
		client.afterConnect = nil

		assert.Equal(t, "test-sid", client.sid)

		closed := make(chan struct{}, 1)
		go func() {
			err = client.Close()
			require.NoError(t, err)
			close(closed)
		}()

		select {
		case <-closed:
		case <-time.After(100 * time.Millisecond):
			require.Fail(t, "close stuck")
		}

	})

	t.Run("Transport run error", func(t *testing.T) {
		mockTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("run error"))
		mockTransport.EXPECT().Stop()
		err := client.Connect(ctx)

		assert.Error(t, err)

		closed := make(chan struct{}, 1)
		go func() {
			err = client.Close()
			require.NoError(t, err)
			close(closed)
		}()

		select {
		case <-closed:
		case <-time.After(100 * time.Millisecond):
			require.Fail(t, "close stuck")
		}
	})

	t.Run("Handshake request error", func(t *testing.T) {
		ctx := context.Background()
		mockTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockTransport.EXPECT().RequestHandshake().Return(errors.New("handshake error"))
		mockTransport.EXPECT().Stop().Do(func() {
			close(client.transportClosed)
		})

		err := client.Connect(ctx)
		assert.Error(t, err)

		closed := make(chan struct{}, 1)
		go func() {
			err = client.Close()
			require.NoError(t, err)
			close(closed)
		}()

		select {
		case <-closed:
		case <-time.After(100 * time.Millisecond):
			require.Fail(t, "close stuck")
		}
	})
}

func TestClient_messageLoop(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockParser := mocks.NewMockParser(ctrl)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := &Client{
		log:      mockLogger,
		parser:   mockParser,
		messages: make(chan []byte, 100),
		ctx:      ctx,
	}

	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

	t.Run("Handle packet", func(t *testing.T) {
		mockParser.EXPECT().Parse([]byte("test")).Return(&engineio_v4.Message{Type: engineio_v4.PacketMessage}, nil)

		go client.messageLoop(client.messages)
		client.messages <- []byte("test")
		time.Sleep(10 * time.Millisecond)
	})

	t.Run("Handle packet error", func(t *testing.T) {
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).Times(2)
		mockParser.EXPECT().Parse([]byte("error")).Return(nil, errors.New("parse error"))

		go client.messageLoop(client.messages)
		client.messages <- []byte("error")
		time.Sleep(10 * time.Millisecond)
	})

	t.Run("Context done", func(t *testing.T) {
		mockLogger.EXPECT().Warnf("context done, engine.io client stopped processing messages").AnyTimes()
		messages := make(chan []byte, 1)
		go client.messageLoop(messages)
		cancel()
		time.Sleep(10 * time.Millisecond)
	})

	t.Run("NIL messages channel", func(t *testing.T) {
		mockLogger.EXPECT().Errorf("messages channel is nil, can't read transport messages")
		client.messageLoop(nil)
	})
}

func TestClient_transportUpgrade(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockOldTransport := mocks.NewMockTransport(ctrl)
	mockNewTransport := mocks.NewMockTransport(ctrl)

	testURL, _ := url.Parse("http://localhost")
	client := &Client{
		url:             testURL,
		log:             mockLogger,
		parser:          mockParser,
		transport:       mockOldTransport,
		transportClosed: make(chan error, 1),
		messages:        make(chan []byte, 100),
	}

	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

	t.Run("success", func(t *testing.T) {
		mockOldTransport.EXPECT().Stop().Return(nil)
		client.transportClosed <- nil
		mockNewTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte("probe"), nil)
		mockNewTransport.EXPECT().SendMessage([]byte("probe")).Return(nil)

		err := client.transportUpgrade(mockNewTransport)
		assert.NoError(t, err)
		assert.Equal(t, mockNewTransport, client.transport)
	})

	t.Run("stop transport error", func(t *testing.T) {
		client.transport = mockOldTransport
		mockOldTransport.EXPECT().Stop().Return(errors.New("oops"))
		err := client.transportUpgrade(mockNewTransport)
		assert.ErrorContains(t, err, "oops")
		assert.Equal(t, mockOldTransport, client.transport)
	})

	t.Run("start new transport error", func(t *testing.T) {
		mockOldTransport.EXPECT().Stop().Return(nil)
		client.transportClosed <- nil
		mockNewTransport.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("oops"))
		err := client.transportUpgrade(mockNewTransport)
		assert.ErrorContains(t, err, "oops")
		assert.Equal(t, mockNewTransport, client.transport)
	})

}

func TestClient_handleHandshake(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockTransportPolling := mocks.NewMockTransport(ctrl)
	mockTransportWs := mocks.NewMockTransport(ctrl)
	mockTransportPolling.EXPECT().Transport().Return(engineio_v4.TransportPolling).AnyTimes()
	mockTransportWs.EXPECT().Transport().Return(engineio_v4.TransportWebsocket).AnyTimes()

	client := &Client{
		log:       mockLogger,
		transport: mockTransportPolling,
		parser:    mockParser,
		supportedTransports: map[engineio_v4.EngineIOTransport]Transport{
			engineio_v4.TransportPolling:   mockTransportPolling,
			engineio_v4.TransportWebsocket: mockTransportWs,
		},
	}

	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

	t.Run("Successful handshake", func(t *testing.T) {
		handshakeResp := &engineio_v4.HandshakeResponse{
			Sid:          "test-sid",
			PingInterval: 25000,
			PingTimeout:  5000,
			Upgrades:     []string{"polling"},
		}
		data, _ := json.Marshal(handshakeResp)

		mockTransportPolling.EXPECT().SetHandshake(handshakeResp)
		mockTransportWs.EXPECT().SetHandshake(handshakeResp)

		client.waitHandshake = make(chan struct{})
		err := client.handleHandshake(data)
		assert.NoError(t, err)

		select {
		case <-client.waitHandshake:
		case <-time.After(50 * time.Millisecond):
			assert.Fail(t, "handshake not completed")
		}
		assert.Equal(t, "test-sid", client.sid)
	})

	t.Run("Successful handshake with upgrade", func(t *testing.T) {
		handshakeResp := &engineio_v4.HandshakeResponse{
			Sid:          "test-sid",
			PingInterval: 25000,
			PingTimeout:  5000,
			Upgrades:     []string{"websocket"},
		}
		data, _ := json.Marshal(handshakeResp)

		mockTransportPolling.EXPECT().SetHandshake(handshakeResp)
		mockTransportPolling.EXPECT().Stop().Do(func() {
			client.transportClosed = make(chan error)
			close(client.transportClosed)
		})
		mockTransportWs.EXPECT().SetHandshake(handshakeResp)
		mockTransportWs.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte("probe"), nil)
		mockTransportWs.EXPECT().SendMessage([]byte("probe")).Return(nil)

		client.hadHandshake = sync.Once{}
		client.waitHandshake = make(chan struct{})
		err := client.handleHandshake(data)
		assert.NoError(t, err)

		select {
		case <-client.waitHandshake:
		case <-time.After(50 * time.Millisecond):
			assert.Fail(t, "handshake not completed")
		}
		assert.Equal(t, "test-sid", client.sid)
	})

	t.Run("Successful handshake with wrong upgrade", func(t *testing.T) {
		handshakeResp := &engineio_v4.HandshakeResponse{
			Sid:          "test-sid",
			PingInterval: 25000,
			PingTimeout:  5000,
			Upgrades:     []string{"xyz"},
		}
		data, _ := json.Marshal(handshakeResp)

		mockTransportPolling.EXPECT().SetHandshake(handshakeResp)
		mockTransportWs.EXPECT().SetHandshake(handshakeResp)
		mockLogger.EXPECT().Warnf("unsupported upgrade: %s", "xyz")

		client.hadHandshake = sync.Once{}
		client.waitHandshake = make(chan struct{})
		err := client.handleHandshake(data)
		assert.NoError(t, err)

		select {
		case <-client.waitHandshake:
		case <-time.After(50 * time.Millisecond):
			assert.Fail(t, "handshake not completed")
		}
		assert.Equal(t, "test-sid", client.sid)
	})

	t.Run("Invalid handshake data", func(t *testing.T) {
		err := client.handleHandshake([]byte("invalid"))
		assert.Error(t, err)
	})

	t.Run("Empty sid", func(t *testing.T) {
		handshakeResp := &engineio_v4.HandshakeResponse{
			Sid:          "",
			PingInterval: 25000,
			PingTimeout:  5000,
		}
		data, _ := json.Marshal(handshakeResp)

		err := client.handleHandshake(data)
		assert.Error(t, err)
	})
}

func TestClient_handlePacket(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockTransport := mocks.NewMockTransport(ctrl)

	client := &Client{
		log:       mockLogger,
		parser:    mockParser,
		transport: mockTransport,
	}

	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

	t.Run("Open packet error (wrong handshake)", func(t *testing.T) {
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any())
		mockParser.EXPECT().Parse([]byte("open")).Return(&engineio_v4.Message{Type: engineio_v4.PacketOpen, Data: []byte("{}")}, nil)
		err := client.handlePacket([]byte("open"))
		assert.Error(t, err)
	})

	t.Run("Open packet with Sid only", func(t *testing.T) {
		client.waitHandshake = make(chan struct{})
		mockParser.EXPECT().Parse([]byte("open")).Return(&engineio_v4.Message{Type: engineio_v4.PacketOpen, Data: []byte(`{"sid":"123"}`)}, nil)
		err := client.handlePacket([]byte("open"))
		assert.NoError(t, err)
		assert.Equal(t, "123", client.sid)
	})

	t.Run("Few open packets", func(t *testing.T) {
		client.waitHandshake = make(chan struct{})
		mockParser.EXPECT().Parse([]byte("open")).Return(&engineio_v4.Message{Type: engineio_v4.PacketOpen, Data: []byte(`{"sid":"123"}`)}, nil)
		mockParser.EXPECT().Parse([]byte("open")).Return(&engineio_v4.Message{Type: engineio_v4.PacketOpen, Data: []byte(`{"sid":"1234"}`)}, nil)
		err := client.handlePacket([]byte("open"))
		assert.NoError(t, err)
		assert.Equal(t, "123", client.sid)
		err = client.handlePacket([]byte("open"))
		assert.NoError(t, err)
		assert.Equal(t, "1234", client.sid)
	})

	t.Run("Close packet", func(t *testing.T) {
		mockParser.EXPECT().Parse([]byte("close")).Return(&engineio_v4.Message{Type: engineio_v4.PacketClose}, nil)
		closeCalled := false
		client.closeHandler = func() { closeCalled = true }
		err := client.handlePacket([]byte("close"))
		assert.NoError(t, err)
		assert.True(t, closeCalled)
	})

	t.Run("Ping packet", func(t *testing.T) {
		mockParser.EXPECT().Parse([]byte("ping")).Return(&engineio_v4.Message{Type: engineio_v4.PacketPing}, nil)
		mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte("pong"), nil)
		mockTransport.EXPECT().SendMessage([]byte("pong")).Return(nil)
		err := client.handlePacket([]byte("ping"))
		assert.NoError(t, err)
	})

	t.Run("Ping packet error", func(t *testing.T) {
		mockParser.EXPECT().Parse([]byte("ping")).Return(&engineio_v4.Message{Type: engineio_v4.PacketPing}, nil)
		mockParser.EXPECT().Serialize(gomock.Any()).Return(nil, errors.New("serialize error"))
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any())
		err := client.handlePacket([]byte("ping"))
		assert.Error(t, err)
	})

	t.Run("Pong packet", func(t *testing.T) {
		mockParser.EXPECT().Parse([]byte("pong")).Return(&engineio_v4.Message{Type: engineio_v4.PacketPong}, nil)
		err := client.handlePacket([]byte("pong"))
		assert.NoError(t, err)
	})

	t.Run("Pong packet error", func(t *testing.T) {
		mockParser.EXPECT().Parse([]byte("pong")).Return(nil, errors.New("parse error"))
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any())
		err := client.handlePacket([]byte("pong"))
		assert.Error(t, err)
	})

	t.Run("Pong probe packet", func(t *testing.T) {
		mockParser.EXPECT().Parse([]byte("pong probe")).Return(&engineio_v4.Message{Type: engineio_v4.PacketPong, Data: []byte("probe")}, nil)
		mockParser.EXPECT().Serialize(&engineio_v4.Message{Type: engineio_v4.PacketUpgrade}).Return([]byte("5"), nil)
		mockTransport.EXPECT().SendMessage([]byte("5")).Return(nil)
		client.waitUpgrade = make(chan struct{})
		err := client.handlePacket([]byte("pong probe"))
		assert.NoError(t, err)
	})

	t.Run("Pong probe packet error", func(t *testing.T) {
		mockParser.EXPECT().Parse([]byte("pong probe")).Return(&engineio_v4.Message{Type: engineio_v4.PacketPong, Data: []byte("probe")}, nil)
		mockParser.EXPECT().Serialize(&engineio_v4.Message{Type: engineio_v4.PacketUpgrade}).Return([]byte("5"), nil)
		mockTransport.EXPECT().SendMessage([]byte("5")).Return(errors.New("send error"))
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any())
		client.waitUpgrade = make(chan struct{})
		err := client.handlePacket([]byte("pong probe"))
		assert.Error(t, err)
	})

	t.Run("Message packet", func(t *testing.T) {
		mockParser.EXPECT().Parse([]byte("message")).Return(&engineio_v4.Message{Type: engineio_v4.PacketMessage, Data: []byte("test")}, nil)
		messageCalled := false
		client.messageHandler = func(data []byte) { messageCalled = true }
		err := client.handlePacket([]byte("message"))
		assert.NoError(t, err)
		assert.True(t, messageCalled)
	})
}

func TestClient_sendPacket(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockTransport := mocks.NewMockTransport(ctrl)

	client := &Client{
		log:       mockLogger,
		parser:    mockParser,
		transport: mockTransport,
	}

	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

	t.Run("Successful send", func(t *testing.T) {
		packet := &engineio_v4.Message{Type: engineio_v4.PacketMessage, Data: []byte("test")}
		mockParser.EXPECT().Serialize(packet).Return([]byte("serialized"), nil)
		mockTransport.EXPECT().SendMessage([]byte("serialized")).Return(nil)

		err := client.sendPacket(packet)
		assert.NoError(t, err)
	})

	t.Run("Serialize error", func(t *testing.T) {
		packet := &engineio_v4.Message{Type: engineio_v4.PacketMessage, Data: []byte("test")}
		mockParser.EXPECT().Serialize(packet).Return(nil, errors.New("serialize error"))

		err := client.sendPacket(packet)
		assert.Error(t, err)
	})

	t.Run("Send error", func(t *testing.T) {
		packet := &engineio_v4.Message{Type: engineio_v4.PacketMessage, Data: []byte("test")}
		mockParser.EXPECT().Serialize(packet).Return([]byte("serialized"), nil)
		mockTransport.EXPECT().SendMessage([]byte("serialized")).Return(errors.New("send error"))

		err := client.sendPacket(packet)
		assert.Error(t, err)
	})

	t.Run("Send nil", func(t *testing.T) {
		err := client.sendPacket(nil)
		assert.Error(t, err)
	})
}

func TestClient_Send(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockTransport := mocks.NewMockTransport(ctrl)

	client := &Client{
		log:       mockLogger,
		parser:    mockParser,
		transport: mockTransport,
	}

	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

	t.Run("Successful send", func(t *testing.T) {
		message := []byte("test")
		mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte("serialized"), nil)
		mockTransport.EXPECT().SendMessage([]byte("serialized")).Return(nil)

		err := client.Send(message)
		assert.NoError(t, err)
	})

	t.Run("Successful with wait", func(t *testing.T) {
		message := []byte("test")
		mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte("serialized"), nil).Times(2)
		mockTransport.EXPECT().SendMessage([]byte("serialized")).Return(nil).Times(2)

		client.waitUpgrade = make(chan struct{}, 1)
		messageSent := make(chan struct{})

		time.After(time.Millisecond * 100)
		go func() {
			err := client.Send(message)
			assert.NoError(t, err)
			close(messageSent)
		}()

		go func() {
			<-time.After(time.Millisecond * 100)
			close(client.waitUpgrade)
		}()

		select {
		case <-messageSent:
			assert.Fail(t, "message was sent before update was completed")
		case <-time.After(time.Millisecond * 50):
		}

		select {
		case <-messageSent:
		case <-time.After(time.Millisecond * 100):
			assert.Fail(t, "message have not been sent after update was completed")
		}

		client.waitHandshake = make(chan struct{}, 1)
		messageSent = make(chan struct{})

		time.After(time.Millisecond * 100)
		go func() {
			err := client.Send(message)
			assert.NoError(t, err)
			close(messageSent)
		}()

		go func() {
			<-time.After(time.Millisecond * 100)
			close(client.waitHandshake)
		}()

		select {
		case <-messageSent:
			assert.Fail(t, "message was sent before handshake was completed")
		case <-time.After(time.Millisecond * 50):
		}

		select {
		case <-messageSent:
		case <-time.After(time.Millisecond * 100):
			assert.Fail(t, "message have not been sent after handshake was completed")
		}
	})
}

func TestClient_On(t *testing.T) {
	client := &Client{}

	t.Run("Connect event", func(t *testing.T) {
		called := false
		client.On("connect", func([]byte) { called = true })
		client.afterConnect()
		assert.True(t, called)
	})

	t.Run("Message event", func(t *testing.T) {
		var receivedMessage []byte
		client.On("message", func(msg []byte) { receivedMessage = msg })
		testMessage := []byte("test message")
		client.messageHandler(testMessage)
		assert.Equal(t, testMessage, receivedMessage)
	})

	t.Run("Close event", func(t *testing.T) {
		called := false
		client.On("close", func([]byte) { called = true })
		client.closeHandler()
		assert.True(t, called)
	})
}

func TestClient_Close(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTransport := mocks.NewMockTransport(ctrl)

	client := &Client{
		transport:       mockTransport,
		transportClosed: make(chan error, 1),
	}

	t.Run("Successful close", func(t *testing.T) {
		mockTransport.EXPECT().Stop().Return(nil)
		client.transportClosed <- nil

		err := client.Close()
		assert.NoError(t, err)
	})

	t.Run("Failed close", func(t *testing.T) {

		mockTransport.EXPECT().Stop().Return(errors.New("oops"))
		client.transportClosed <- nil

		err := client.Close()
		assert.ErrorContains(t, err, "oops")
	})
}

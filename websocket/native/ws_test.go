package ws_native

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"
)

func TestWebSocketConnection_Dial(t *testing.T) {

	ctx := context.Background()

	origin, err := url.Parse("http://localhost")
	require.NoError(t, err, "Failed to parse server URL")

	t.Run("Successful connection", func(t *testing.T) {
		t.Parallel()

		server := createTestServer(t, nil)
		defer server.Close()

		url, err := url.Parse(server.URL)
		require.NoError(t, err, "Failed to parse server URL")
		url.Scheme = "ws"

		ws := &WebSocketConnection{}
		err = ws.Dial(ctx, url, origin)

		require.NoError(t, err, "Dial should not return an error")
		assert.NotNil(t, ws.conn, "Connection should be established")
	})

	t.Run("Connection error", func(t *testing.T) {
		t.Parallel()
		ws := &WebSocketConnection{}
		err = ws.Dial(ctx, &url.URL{
			Path:   "invalid-url",
			Scheme: "ws",
		}, origin)
		assert.Error(t, err, "Dial should return an error for invalid URL")
		assert.Nil(t, ws.conn, "Connection should not be established")
	})

	t.Run("Config error", func(t *testing.T) {
		t.Parallel()
		ws := &WebSocketConnection{}
		err = ws.Dial(ctx, &url.URL{
			Path:   "invalid-url",
			Scheme: ";;;",
		}, origin)
		assert.Error(t, err, "Dial should return an error for invalid URL")
		assert.Nil(t, ws.conn, "Connection should not be established")
	})
}

func TestWebSocketConnection_Send(t *testing.T) {

	ctx := context.Background()

	origin, err := url.Parse("http://localhost")
	require.NoError(t, err, "Failed to parse server URL")

	t.Run("Send message and verify server receipt", func(t *testing.T) {
		t.Parallel()

		receivedChan := make(chan string, 1)
		server := createTestServer(t, receivedChan)
		defer server.Close()

		url, err := url.Parse(server.URL)
		require.NoError(t, err, "Failed to parse server URL")
		url.Scheme = "ws"

		ws := &WebSocketConnection{}
		err = ws.Dial(ctx, url, origin)
		require.NoError(t, err, "Dial should not return an error")

		message := "Hello, WebSocket!"
		err = ws.Send([]byte(message))
		assert.NoError(t, err, "Send should not return an error")

		select {
		case received := <-receivedChan:
			assert.Equal(t, message, received, "Server should receive the correct message")
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for server to receive message")
		}
	})

	t.Run("Send without connection", func(t *testing.T) {
		t.Parallel()
		ws := &WebSocketConnection{}
		err := ws.Send([]byte("Hello"))
		assert.Equal(t, ErrNotConnected, err, "Send should return ErrNotConnected")
	})
}

func TestWebSocketConnection_Receive(t *testing.T) {

	ctx := context.Background()

	origin, err := url.Parse("http://localhost")
	require.NoError(t, err, "Failed to parse server URL")

	t.Run("Receive message", func(t *testing.T) {
		t.Parallel()

		receivedChan := make(chan string, 1)
		server := createTestServer(t, receivedChan)
		defer server.Close()

		url, err := url.Parse(server.URL)
		require.NoError(t, err, "Failed to parse server URL")
		url.Scheme = "ws"

		ws := &WebSocketConnection{}
		err = ws.Dial(ctx, url, origin)
		require.NoError(t, err, "Dial should not return an error")

		err = ws.Send([]byte("Echo"))
		require.NoError(t, err, "Send should not return an error")

		var received []byte
		err = ws.Receive(&received)
		require.NoError(t, err, "Receive should not return an error")
		assert.Equal(t, "Echo", string(received), "Received message should match sent message")
	})

	t.Run("Receive without connection", func(t *testing.T) {
		t.Parallel()
		ws := &WebSocketConnection{}
		var received []byte
		err := ws.Receive(&received)
		assert.Equal(t, ErrNotConnected, err, "Receive should return ErrNotConnected")
	})
}

func TestWebSocketConnection_Close(t *testing.T) {

	ctx := context.Background()

	origin, err := url.Parse("http://localhost")
	require.NoError(t, err, "Failed to parse server URL")

	t.Run("Close connection", func(t *testing.T) {
		t.Parallel()

		receivedChan := make(chan string, 1)
		server := createTestServer(t, receivedChan)
		defer server.Close()

		url, err := url.Parse(server.URL)
		require.NoError(t, err, "Failed to parse server URL")
		url.Scheme = "ws"

		ws := &WebSocketConnection{}
		err = ws.Dial(ctx, url, origin)
		require.NoError(t, err, "Dial should not return an error")

		// Небольшая задержка, чтобы убедиться, что соединение установлено
		time.Sleep(50 * time.Millisecond)

		err = ws.Close()
		assert.NoError(t, err, "Close should not return an error")

		// Проверяем, что канал receivedChan пуст (сервер не получил сообщений)
		select {
		case msg := <-receivedChan:
			t.Errorf("Unexpected message received: %s", msg)
		default:
			// Это ожидаемое поведение - канал должен быть пуст
		}
	})

	t.Run("Close without connection", func(t *testing.T) {
		t.Parallel()
		ws := &WebSocketConnection{}
		err := ws.Close()
		assert.NoError(t, err, "Close should return nil for uninitialized connection")
	})
}

func createTestServer(t *testing.T, receivedChan chan<- string) *httptest.Server {
	return httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		for {
			var msg string
			err := websocket.Message.Receive(ws, &msg)
			if err != nil {
				// Игнорируем ошибку EOF, так как она ожидаема при закрытии соединения
				if err.Error() != "EOF" {
					t.Logf("Server received an error: %v", err)
				}
				return
			}
			if receivedChan != nil {
				receivedChan <- msg
			}
			// Echo the message back
			if err := websocket.Message.Send(ws, msg); err != nil {
				t.Errorf("Server failed to send message: %v", err)
				return
			}
		}
	}))
}

package engineio_v4_client_transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	engineio_v4 "maldikhan/go.socket.io/engine.io/v4"
	mocks "maldikhan/go.socket.io/engine.io/v4/client/transport/polling/mocks"
)

func TestSetHandshake(t *testing.T) {
	t.Parallel()
	client := &Transport{
		pinger: time.NewTicker(time.Minute),
	}

	handshake := &engineio_v4.HandshakeResponse{
		Sid:          "test-sid",
		PingInterval: 5000,
	}

	client.SetHandshake(handshake)

	assert.Equal(t, "test-sid", client.sid)
	// Note: We can't directly test the ticker's interval, but we can check if it's been reset
	assert.NotNil(t, client.pinger)
}

func TestRequestHandshake(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockHttpClient := mocks.NewMockHttpClient(ctrl)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	messages := make(chan []byte, 1)
	client := &Transport{
		log:        mockLogger,
		httpClient: mockHttpClient,
		url:        &url.URL{Scheme: "http", Host: "example.com", Path: "/socket.io/"},
		ctx:        ctx,
		messages:   messages,
	}

	mockLogger.EXPECT().Debugf("run polling")
	mockLogger.EXPECT().Debugf("receiveHttp: %s", "test response")

	mockResp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("test response")),
	}

	mockHttpClient.EXPECT().
		Do(gomock.Any()).
		DoAndReturn(func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, "GET", req.Method)
			assert.Equal(t, "http://example.com/socket.io/?EIO=4&sid=&transport=polling", req.URL.String())
			return mockResp, nil
		})

	err := client.RequestHandshake()
	assert.NoError(t, err)

	select {
	case <-ctx.Done():
		assert.Fail(t, "context timeout")
	case <-messages:
	case <-time.After(500 * time.Millisecond):
		assert.Fail(t, "no handshake message received")
	}

}

func TestTransport(t *testing.T) {
	t.Parallel()
	client := &Transport{}
	assert.Equal(t, engineio_v4.TransportPolling, client.Transport())
}

func TestRun(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockHttpClient := mocks.NewMockHttpClient(ctrl)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	url, _ := url.Parse("http://example.com")
	messagesChan := make(chan []byte, 1)
	onCloseChan := make(chan error, 1)

	client := &Transport{
		log:         mockLogger,
		httpClient:  mockHttpClient,
		pinger:      time.NewTicker(time.Millisecond),
		url:         url,
		stopPooling: make(chan struct{}, 1),
	}

	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

	// Mock the poll method to avoid actual HTTP requests
	mockHttpClient.EXPECT().
		Do(gomock.Any()).
		Return(&http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("test response")),
		}, nil).
		AnyTimes()

	t.Run("Run success", func(t *testing.T) {
		err := client.Run(ctx, url, "test-sid", messagesChan, onCloseChan)
		assert.NoError(t, err)

		// Allow some time for the goroutine to start
		time.Sleep(10 * time.Millisecond)

		// Stop the client
		client.Stop()

		// Check if onClose channel receives nil
		select {
		case err := <-onCloseChan:
			assert.NoError(t, err)
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Timeout waiting for onClose signal")
		}
	})

	t.Run("Run error", func(t *testing.T) {
		ctxTmp, cancelContext := context.WithTimeout(ctx, time.Microsecond)
		defer cancelContext()
		// expire context
		time.Sleep(1 * time.Millisecond)

		err := client.Run(ctxTmp, url, "test-sid", messagesChan, onCloseChan)
		assert.NoError(t, err)

		// Allow some time for the goroutine to start
		time.Sleep(10 * time.Millisecond)

		// Stop the client
		client.Stop()

		// Check if onClose channel receives nil
		select {
		case err := <-onCloseChan:
			assert.ErrorContains(t, err, "context deadline exceeded")
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout waiting for onClose signal")
		}
	})
}

func TestPollingLoop(t *testing.T) {
	t.Run("Stop polling", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().Debugf("stop polling")

		client := &Transport{
			log:         mockLogger,
			pinger:      time.NewTicker(time.Minute),
			stopPooling: make(chan struct{}),
			ctx:         ctx,
			onClose:     make(chan error, 1),
		}

		go func() {
			time.Sleep(10 * time.Millisecond)
			close(client.stopPooling)
		}()

		err := client.pollingLoop()
		assert.NoError(t, err)
	})

	t.Run("Context cancellation", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().Debugf("context done, stop http polling")

		ctx, cancel := context.WithCancel(context.Background())
		ctx, timeout := context.WithTimeout(ctx, 5*time.Second)
		defer timeout()

		client := &Transport{
			log:         mockLogger,
			pinger:      time.NewTicker(time.Minute),
			stopPooling: make(chan struct{}),
			ctx:         ctx,
			onClose:     make(chan error, 1),
		}

		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		err := client.pollingLoop()
		assert.Error(t, err)
		assert.True(t, errors.Is(err, context.Canceled))
	})

	t.Run("Ping interval", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		errExpected := errors.New("expected error")
		pingCalled := make(chan struct{})

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHttpClient := mocks.NewMockHttpClient(ctrl)
		mockLogger.EXPECT().Debugf("run polling").MinTimes(1)
		mockLogger.EXPECT().Debugf("stop polling").MinTimes(1)
		mockLogger.EXPECT().Errorf("poll error: %s", errExpected).MinTimes(1)
		mockHttpClient.EXPECT().Do(gomock.Any()).DoAndReturn(func(_ *http.Request) (*http.Response, error) {
			close(pingCalled)
			return nil, errExpected
		})

		onClose := make(chan error, 1)
		client := &Transport{
			url:         &url.URL{Scheme: "http", Host: "localhost"},
			httpClient:  mockHttpClient,
			log:         mockLogger,
			pinger:      time.NewTicker(10 * time.Millisecond),
			stopPooling: make(chan struct{}, 1),
			ctx:         ctx,
			onClose:     onClose,
			messages:    make(chan []byte, 1),
		}

		go func() {
			select {
			case <-pingCalled:
			case <-time.After(100 * time.Millisecond):
				assert.Fail(t, "expected ping to be called")
			}

			client.Stop()
		}()

		exitChan := make(chan struct{})
		go func() {
			err := client.pollingLoop()
			assert.NoError(t, err)
			exitChan <- struct{}{}
		}()

		select {
		case <-exitChan:
		case <-time.After(100 * time.Millisecond):
			assert.Fail(t, "expected pollingLoop to exit")
		}

		select {
		case err := <-onClose:
			assert.NoError(t, err)
		case <-time.After(100 * time.Millisecond):
			assert.Fail(t, "expected pollingLoop to exit and emit onClose")
		}

	})

	t.Run("Ping interval closed chan", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		tickerChan := make(chan time.Time)
		close(tickerChan)
		ticker := &time.Ticker{
			C: tickerChan,
		}

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().Warnf("pinger closed: stop pooling")

		client := &Transport{
			log:         mockLogger,
			pinger:      ticker,
			stopPooling: make(chan struct{}),
			ctx:         ctx,
		}

		err := client.pollingLoop()
		assert.ErrorContains(t, err, "pinger closed")
	})
}

func TestPoll(t *testing.T) {

	t.Run("Successful poll", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHttpClient(ctrl)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Create a buffered channel to avoid blocking
		messagesChan := make(chan []byte, 1)

		client := &Transport{
			log:        mockLogger,
			httpClient: mockHTTPClient,
			url:        &url.URL{Scheme: "http", Host: "example.com", Path: "/socket.io/"},
			sid:        "test-sid",
			ctx:        ctx,
			messages:   messagesChan,
		}

		mockLogger.EXPECT().Debugf("run polling")
		mockLogger.EXPECT().Debugf("receiveHttp: %s", "test response")

		mockResp := &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("test response")),
		}

		mockHTTPClient.EXPECT().
			Do(gomock.Any()).
			DoAndReturn(func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, "GET", req.Method)
				assert.Equal(t, "http://example.com/socket.io/?EIO=4&sid=test-sid&transport=polling", req.URL.String())
				return mockResp, nil
			})

		// Use a channel to signal when the message is sent
		done := make(chan struct{})
		go func() {
			err := client.poll()
			assert.NoError(t, err)
			close(done)
		}()

		// Wait for the poll to complete or timeout
		select {
		case <-done:
			// Check if a message was sent to the channel
			assert.Equal(t, 1, len(messagesChan), "Expected one message to be sent")
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for poll to complete")
		}
	})

	t.Run("Error creating request", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)

		client := &Transport{
			log:      mockLogger,
			url:      &url.URL{Scheme: "http", Host: "example.com"},
			ctx:      nil, // Intentionally set to nil to cause an error
			messages: make(chan<- []byte),
		}

		mockLogger.EXPECT().Debugf("run polling")

		err := client.poll()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error creating request")
	})

	t.Run("HTTP client error", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHttpClient(ctrl)

		client := &Transport{
			log:        mockLogger,
			httpClient: mockHTTPClient,
			url:        &url.URL{Scheme: "http", Host: "example.com"},
			sid:        "test-sid",
			ctx:        context.Background(),
			messages:   make(chan<- []byte),
		}

		mockLogger.EXPECT().Debugf("run polling")

		expectedError := errors.New("http client error")

		mockHTTPClient.EXPECT().
			Do(gomock.Any()).
			Return(nil, expectedError)

		err := client.poll()
		assert.Error(t, err)
		assert.Equal(t, expectedError, err)
	})

	t.Run("Error reading response body", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockHTTPClient := mocks.NewMockHttpClient(ctrl)

		client := &Transport{
			log:        mockLogger,
			httpClient: mockHTTPClient,
			url:        &url.URL{Scheme: "http", Host: "example.com"},
			sid:        "test-sid",
			ctx:        context.Background(),
			messages:   make(chan<- []byte),
		}

		mockLogger.EXPECT().Debugf("run polling")

		mockResp := &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(&errorReader{err: errors.New("read error")}),
		}

		mockHTTPClient.EXPECT().
			Do(gomock.Any()).
			Return(mockResp, nil)

		err := client.poll()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "read error")
	})

	t.Run("Poll with no messages channel", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().Debugf("run polling")
		mockLogger.EXPECT().Errorf("messages channel is nil, can't read transport messages")

		client := &Transport{
			log: mockLogger,
		}
		err := client.poll()
		assert.Error(t, err)
	})
}

func TestSendMessage(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockHTTPClient := mocks.NewMockHttpClient(ctrl)

	client := &Transport{
		log:        mockLogger,
		httpClient: mockHTTPClient,
		url:        &url.URL{Scheme: "http", Host: "example.com", Path: "/socket.io/"},
		sid:        "test-sid",
		ctx:        ctx,
	}

	mockLogger.EXPECT().Debugf("sendHttp: %s", []byte("test message")).Times(2)
	mockLogger.EXPECT().Debugf("receiveHttp: %s", "200 OK")

	mockResp := &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader("")),
	}

	mockHTTPClient.EXPECT().
		Do(gomock.Any()).
		DoAndReturn(func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, "POST", req.Method)
			assert.Equal(t, "http://example.com/socket.io/?EIO=4&sid=test-sid&transport=polling", req.URL.String())
			body, _ := io.ReadAll(req.Body)
			assert.Equal(t, []byte("test message"), body)
			return mockResp, nil
		})

	t.Run("Success", func(t *testing.T) {
		err := client.SendMessage([]byte("test message"))
		assert.NoError(t, err)
	})

	t.Run("Request make error", func(t *testing.T) {
		client.url.Scheme = ";;;"
		err := client.SendMessage([]byte("test message"))
		assert.Error(t, err)
	})
}

// errorReader is a helper type that always returns an error when Read is called
type errorReader struct {
	err error
}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, e.err
}

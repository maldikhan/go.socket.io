package socketio_v5_client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	"github.com/maldikhan/go.socket.io/socket.io/v5/client/emit"
	mocks "github.com/maldikhan/go.socket.io/socket.io/v5/client/mocks"
)

func TestClientEmit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockEngineIO := mocks.NewMockEngineIOClient(ctrl)
	mockParser := mocks.NewMockParser(ctrl)
	mockTimer := mocks.NewMockTimer(ctrl)
	mockLogger := mocks.NewMockLogger(ctrl)

	client := &Client{
		defaultNs: &namespace{
			client: &Client{
				engineio:     mockEngineIO,
				parser:       mockParser,
				ackCallbacks: make(map[int]func([]interface{})),
				ackCounter:   0,
				timer:        mockTimer,
				ctx:          context.Background(),
				logger:       mockLogger,
			},
		},
	}

	mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte{}, nil)
	mockEngineIO.EXPECT().Send(gomock.Any()).Return(nil)

	err := client.Emit("test_event", "arg1", "arg2")
	assert.NoError(t, err)
}

func TestNamespaceEmit(t *testing.T) {
	tests := []struct {
		name          string
		event         interface{}
		args          []interface{}
		expectedError error
		WithAck       bool
	}{
		{"Valid string event", "test_event", []interface{}{"arg1", "arg2"}, nil, false},
		{"Valid Event struct", socketio_v5.Event{Name: "test_event"}, []interface{}{}, nil, false},
		{"Valid Event struct ref", &socketio_v5.Event{Name: "test_event"}, []interface{}{}, nil, false},
		{"Invalid event + params", &socketio_v5.Event{Name: "test_event"}, []interface{}{"arg1", "arg2"}, errors.New("you can pass ether Event() ether eventName and params, not both"), false},
		{"Invalid event ref + params", &socketio_v5.Event{Name: "test_event"}, []interface{}{"arg1", "arg2"}, errors.New("you can pass ether Event() ether eventName and params, not both"), false},
		{"Invalid event type", 123, []interface{}{}, errors.New("invalid event type"), false},
		{"Event with EmitOption", "test_event", []interface{}{emit.WithAck(func() {})}, nil, true},
		{"Event with bad EmitOption", "test_event", []interface{}{emit.WithAck(123)}, errors.New("callback must be a function"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockEngineIO := mocks.NewMockEngineIOClient(ctrl)
			mockParser := mocks.NewMockParser(ctrl)
			mockTimer := mocks.NewMockTimer(ctrl)
			mockLogger := mocks.NewMockLogger(ctrl)

			if tt.WithAck {
				mockParser.EXPECT().WrapCallback(gomock.Any()).DoAndReturn(func(cb interface{}) interface{} {
					switch cb.(type) {
					case int:
						return nil
					default:
						return func(in []interface{}) {}
					}
				})
			}

			client := &Client{
				engineio:     mockEngineIO,
				parser:       mockParser,
				ackCallbacks: make(map[int]func([]interface{})),
				ackCounter:   0,
				timer:        mockTimer,
				ctx:          context.Background(),
				logger:       mockLogger,
			}

			ns := &namespace{client: client}

			if tt.expectedError == nil {
				mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte{}, nil)
				mockEngineIO.EXPECT().Send(gomock.Any()).Return(nil)
			}

			err := ns.Emit(tt.event, tt.args...)
			if tt.expectedError != nil {
				assert.EqualError(t, err, tt.expectedError.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSendPacketWithAckTimeout(t *testing.T) {
	tests := []struct {
		name            string
		callback        interface{}
		timeout         *time.Duration
		timeoutCallback func()
		expectedError   error
	}{
		{"No callback, no timeout", nil, nil, nil, nil},
		{"With callback, no timeout", func() {}, nil, nil, nil},
		{"With callback and timeout", func() {}, &[]time.Duration{time.Second}[0], func() {}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockEngineIO := mocks.NewMockEngineIOClient(ctrl)
			mockParser := mocks.NewMockParser(ctrl)
			mockTimer := mocks.NewMockTimer(ctrl)
			mockLogger := mocks.NewMockLogger(ctrl)

			client := &Client{
				engineio:     mockEngineIO,
				parser:       mockParser,
				ackCallbacks: make(map[int]func([]interface{})),
				ackCounter:   0,
				timer:        mockTimer,
				ctx:          context.Background(),
				logger:       mockLogger,
			}

			mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte{}, nil)
			mockEngineIO.EXPECT().Send(gomock.Any()).Return(nil)

			if tt.callback != nil {
				mockParser.EXPECT().WrapCallback(gomock.Any()).Return(func([]interface{}) {})
			}

			onTimeout := make(chan struct{}, 1)
			if tt.timeout != nil {
				mockTimer.EXPECT().After(*tt.timeout).DoAndReturn(func(time.Duration) <-chan time.Time {
					return time.After(time.Millisecond * 20)
				})
				mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any())
				originalTimeoutCallback := tt.timeoutCallback
				tt.timeoutCallback = func() {
					onTimeout <- struct{}{}
					originalTimeoutCallback()
				}
			}

			err := client.sendPacketWithAckTimeout(&socketio_v5.Message{}, tt.callback, tt.timeout, tt.timeoutCallback)
			assert.Equal(t, tt.expectedError, err)
			if tt.timeout != nil {
				select {
				case <-time.After(time.Millisecond * 100):
					assert.Fail(t, "timeout callback is not fired")
				case <-onTimeout:
				}
			}
		})
	}

	t.Run("On ack", func(t *testing.T) {

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockEngineIO := mocks.NewMockEngineIOClient(ctrl)
		mockParser := mocks.NewMockParser(ctrl)
		mockTimer := mocks.NewMockTimer(ctrl)
		mockLogger := mocks.NewMockLogger(ctrl)

		mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte{}, nil)
		mockEngineIO.EXPECT().Send(gomock.Any()).Return(nil)
		mockTimer.EXPECT().After(gomock.Any()).DoAndReturn(func(time.Duration) <-chan time.Time {
			return time.After(time.Millisecond * 20)
		})

		onTimeout := make(chan struct{}, 1)
		onResponse := make(chan struct{}, 1)

		timeoutCallback := func() {
			onTimeout <- struct{}{}
		}

		mockParser.EXPECT().WrapCallback(gomock.Any()).Return(func([]interface{}) {
			onResponse <- struct{}{}
		})

		client := &Client{
			engineio:     mockEngineIO,
			parser:       mockParser,
			ackCallbacks: make(map[int]func([]interface{})),
			ackCounter:   0,
			timer:        mockTimer,
			ctx:          context.Background(),
			logger:       mockLogger,
		}

		timeout := 50 * time.Millisecond

		err := client.sendPacketWithAckTimeout(&socketio_v5.Message{}, func() {}, &timeout, timeoutCallback)
		go func() {
			<-time.After(time.Millisecond * 10)
			client.ackCallbacks[1]([]interface{}{})
		}()

		assert.Nil(t, err)
		select {
		case <-time.After(time.Millisecond * 100):
			assert.Fail(t, "timeout callback is fired instead of response callback")
		case <-onTimeout:
			assert.Fail(t, "ack callback is not fired")
		case <-onResponse:
		}

	})

	t.Run("On ctx expired", func(t *testing.T) {

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockEngineIO := mocks.NewMockEngineIOClient(ctrl)
		mockParser := mocks.NewMockParser(ctrl)
		mockTimer := mocks.NewMockTimer(ctrl)
		mockLogger := mocks.NewMockLogger(ctrl)

		mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte{}, nil)
		mockEngineIO.EXPECT().Send(gomock.Any()).Return(nil)
		mockTimer.EXPECT().After(gomock.Any()).DoAndReturn(func(time.Duration) <-chan time.Time {
			return time.After(time.Millisecond * 20)
		})

		onTimeout := make(chan struct{}, 1)
		onResponse := make(chan struct{}, 1)

		timeoutCallback := func() {
			onTimeout <- struct{}{}
		}

		onContextExpire := make(chan struct{}, 1)

		mockParser.EXPECT().WrapCallback(gomock.Any()).Return(func([]interface{}) {
			onResponse <- struct{}{}
		})

		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).Do(
			func(format string, args ...interface{}) {
				onContextExpire <- struct{}{}
			},
		)

		ctx, cancel := context.WithCancel(context.Background())

		client := &Client{
			engineio:     mockEngineIO,
			parser:       mockParser,
			ackCallbacks: make(map[int]func([]interface{})),
			ackCounter:   0,
			timer:        mockTimer,
			ctx:          ctx,
			logger:       mockLogger,
		}

		timeout := 50 * time.Millisecond

		err := client.sendPacketWithAckTimeout(&socketio_v5.Message{}, func() {}, &timeout, timeoutCallback)
		go func() {
			<-time.After(time.Millisecond * 30)
			client.ackCallbacks[1]([]interface{}{})
		}()
		<-time.After(time.Millisecond * 5)
		cancel()

		assert.Nil(t, err)
		select {
		case <-time.After(time.Millisecond * 100):
			assert.Fail(t, "timeout callback is fired instead of ctx expired callback")
		case <-onTimeout:
			assert.Fail(t, "expired context ignored")
		case <-onResponse:
			assert.Fail(t, "response fired on expired context")
		case <-onContextExpire:
		}

	})
}

func TestSendPacket(t *testing.T) {
	tests := []struct {
		name          string
		serializeErr  error
		sendErr       error
		expectedError error
	}{
		{"Success", nil, nil, nil},
		{"Serialize error", errors.New("serialize error"), nil, errors.New("serialize error")},
		{"Send error", nil, errors.New("send error"), errors.New("send error")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockEngineIO := mocks.NewMockEngineIOClient(ctrl)
			mockParser := mocks.NewMockParser(ctrl)

			client := &Client{
				engineio: mockEngineIO,
				parser:   mockParser,
			}

			mockParser.EXPECT().Serialize(gomock.Any()).Return([]byte{}, tt.serializeErr)
			if tt.serializeErr == nil {
				mockEngineIO.EXPECT().Send(gomock.Any()).Return(tt.sendErr)
			}

			err := client.sendPacket(&socketio_v5.Message{})
			if tt.expectedError != nil {
				assert.EqualError(t, err, tt.expectedError.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

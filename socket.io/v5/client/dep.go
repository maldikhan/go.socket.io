//go:generate mockgen -source=$GOFILE -destination=mocks/deps.go -package=${packageName}

package socketio_v5_client

import (
	"context"
	"time"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

type Parser interface {
	WrapCallback(callback interface{}) func(in []interface{})
	Parse([]byte) (*socketio_v5.Message, error)
	Serialize(*socketio_v5.Message) ([]byte, error)
	// HasBinary reports whether an event carries []byte payloads that must be
	// sent as a binary packet (PacketBinaryEvent/PacketBinaryAck).
	HasBinary(event *socketio_v5.Event) bool
	// SerializeBinary encodes an event with binary payloads into a header packet
	// plus the ordered binary attachments that follow it on the wire.
	SerializeBinary(*socketio_v5.Message) (header []byte, attachments [][]byte, err error)
	// ReconstructBinary substitutes collected binary attachments back into the
	// placeholders of a parsed binary packet, exposing them as []byte.
	ReconstructBinary(msg *socketio_v5.Message, attachments [][]byte) error
}

// EngineIOClient представляет интерфейс для клиента Engine.IO
type EngineIOClient interface {
	Connect(ctx context.Context) error
	Send(message []byte) error
	// SendBinary delivers a raw binary attachment frame (the buffers that follow
	// a binary event/ack header).
	SendBinary(message []byte) error
	On(event string, handler func([]byte))
	Close() error
}

// Logger представляет интерфейс для логирования
type Logger interface {
	Debugf(format string, v ...any)
	Infof(format string, v ...any)
	Warnf(format string, v ...any)
	Errorf(format string, v ...any)
}

// Timer представляет интерфейс для работы с таймерами
type Timer interface {
	After(d time.Duration) <-chan time.Time
}

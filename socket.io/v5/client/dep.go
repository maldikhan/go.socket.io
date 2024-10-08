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
}

// EngineIOClient представляет интерфейс для клиента Engine.IO
type EngineIOClient interface {
	Connect(ctx context.Context) error
	Send(message []byte) error
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

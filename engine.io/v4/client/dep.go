//go:generate mockgen -source=$GOFILE -destination=mocks/deps.go -package=${packageName}

package engineio_v4_client

import (
	"context"
	"net/url"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
)

type Parser interface {
	Parse([]byte) (
		*engineio_v4.Message,
		error,
	)
	Serialize(*engineio_v4.Message) (
		[]byte,
		error,
	)
}

type Transport interface {
	Transport() engineio_v4.EngineIOTransport
	Run(
		ctx context.Context,
		url *url.URL,
		sid string,
		messagesChan chan<- []byte,
		onClose chan<- error,
	) error
	SetHandshake(handshake *engineio_v4.HandshakeResponse)
	RequestHandshake() error
	Stop() error
	SendMessage(message []byte) error
}

// Logger представляет интерфейс для логирования
type Logger interface {
	Debugf(format string, v ...any)
	Infof(format string, v ...any)
	Warnf(format string, v ...any)
	Errorf(format string, v ...any)
}

//go:generate mockgen -source=$GOFILE -destination=mocks/deps.go -package=${packageName}

package engineio_v4_client_transport

import (
	"context"
	"net/url"
)

type Logger interface {
	Debugf(format string, v ...any)
	Infof(format string, v ...any)
	Warnf(format string, v ...any)
	Errorf(format string, v ...any)
}

type WebSocket interface {
	Dial(ctx context.Context, url *url.URL, origin *url.URL) (err error)
	Send(v []byte) (err error)
	Receive(v *[]byte) (err error)
	Close() error
}

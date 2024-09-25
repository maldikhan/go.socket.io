//go:generate mockgen -source=$GOFILE -destination=mocks/deps.go -package=${packageName}

package engineio_v4_client_transport

import (
	"net/http"
)

type Logger interface {
	Debugf(format string, v ...any)
	Infof(format string, v ...any)
	Warnf(format string, v ...any)
	Errorf(format string, v ...any)
}

type HttpClient interface {
	Do(req *http.Request) (resp *http.Response, err error)
}

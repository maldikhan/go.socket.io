//go:generate mockgen -source=$GOFILE -destination=mocks/deps.go -package=${packageName}

package socketio_v5_parser_default

import socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"

type PayloadParser interface {
	ParseEvent(data []byte, emptyEvent bool) (*socketio_v5.Event, error)
	WrapCallback(callback interface{}) func(in []interface{})
}

type Logger interface {
	Debugf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

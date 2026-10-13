//go:generate mockgen -source=$GOFILE -destination=mocks/deps.go -package=${packageName}

package socketio_v5_parser_default

import socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"

type PayloadParser interface {
	ParseEvent(data []byte, emptyEvent bool) (*socketio_v5.Event, error)
	WrapCallback(callback interface{}) func(in []interface{})
}

// BinaryPayloadParser is an optional capability of a custom PayloadParser.
// Reconstruction and WrapCallback must agree on the representation of binary
// values, which differs from a text-only parser's raw JSON byte slices.
type BinaryPayloadParser interface {
	ReconstructBinary(msg *socketio_v5.Message, attachments [][]byte) error
}

type Logger interface {
	Debugf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

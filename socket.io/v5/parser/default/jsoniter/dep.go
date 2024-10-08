//go:generate mockgen -source=$GOFILE -destination=mocks/deps.go -package=${packageName}

package socketio_v5_parser_default_jsoniter

type Logger interface {
	Debugf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

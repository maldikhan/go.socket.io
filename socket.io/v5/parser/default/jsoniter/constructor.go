package socketio_v5_parser_default_jsoniter

import "github.com/maldikhan/go.socket.io/utils"

type ParserOption func(*SocketIOV5PayloadParser)

func NewPayloadParser(opts ...ParserOption) *SocketIOV5PayloadParser {

	payloadParser := &SocketIOV5PayloadParser{
		logger: &utils.DefaultLogger{},
	}

	for _, opt := range opts {
		opt(payloadParser)
	}

	return payloadParser
}

func WithLogger(logger Logger) ParserOption {
	return func(p *SocketIOV5PayloadParser) {
		p.logger = logger
	}
}

package socketio_v5_parser_default

import (
	"github.com/maldikhan/go.socket.io/utils"
)

type ParserOption func(*SocketIOV5DefaultParser) error

func NewParser(options ...ParserOption) (*SocketIOV5DefaultParser, error) {
	parser := &SocketIOV5DefaultParser{}

	for _, option := range options {
		if err := option(parser); err != nil {
			return nil, err
		}
	}

	if parser.logger == nil {
		parser.logger = &utils.DefaultLogger{}
	}

	return parser, nil
}

func WithLogger(logger Logger) ParserOption {
	return func(p *SocketIOV5DefaultParser) error {
		p.logger = logger
		return nil
	}
}

func WithPayloadParser(payloadParser PayloadParser) ParserOption {
	return func(p *SocketIOV5DefaultParser) error {
		p.payloadParser = payloadParser
		return nil
	}
}

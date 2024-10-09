package engineio_v4_parser

import (
	"errors"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
)

type EngineIOV4Parser struct{}

func (p *EngineIOV4Parser) Parse(data []byte) (*engineio_v4.Message, error) {
	if len(data) == 0 {
		return nil, errors.New("empty message")
	}
	return &engineio_v4.Message{
		Type: engineio_v4.EngineIOPacket(data[0] - 0x30),
		Data: data[1:],
	}, nil
}

func (p *EngineIOV4Parser) Serialize(msg *engineio_v4.Message) ([]byte, error) {
	if len(msg.Data) > 64*1024*1024 { // 64 MB limit
		return nil, errors.New("message data too large")
	}
	packet := make([]byte, 1, len(msg.Data)+1)
	packet[0] = byte(msg.Type) + 0x30
	return append(packet, msg.Data...), nil
}

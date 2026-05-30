package engineio_v4_parser

import (
	"encoding/base64"
	"errors"
	"fmt"

	engineio_v4 "github.com/maldikhan/go.socket.io/engine.io/v4"
)

// binaryPrefix is the engine.io v4 marker that introduces a base64-encoded
// binary attachment inside a (text) payload record, e.g. "bAQIDBA==".
const binaryPrefix = 'b'

// defaultMaxSerializeSize is the outbound payload limit applied when none is
// configured. It mirrors the inbound default and guards against accidentally
// serializing pathologically large frames.
const defaultMaxSerializeSize = 64 * 1024 * 1024 // 64 MB

type EngineIOV4Parser struct {
	// maxSerializeSize caps the size of msg.Data accepted by Serialize().
	// A value <= 0 means "use defaultMaxSerializeSize", which keeps the
	// zero value (&EngineIOV4Parser{}) behaving exactly as before.
	maxSerializeSize int64
}

// ParserOption configures an EngineIOV4Parser built with NewEngineIOV4Parser.
type ParserOption func(*EngineIOV4Parser) error

// NewEngineIOV4Parser builds a parser with the given options. With no options
// it is equivalent to &EngineIOV4Parser{} and uses the default limits.
func NewEngineIOV4Parser(options ...ParserOption) (*EngineIOV4Parser, error) {
	p := &EngineIOV4Parser{maxSerializeSize: defaultMaxSerializeSize}
	for _, opt := range options {
		if err := opt(p); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// WithMaxSerializeSize sets the maximum outbound payload size (in bytes)
// accepted by Serialize(). The size must be positive.
func WithMaxSerializeSize(size int64) ParserOption {
	return func(p *EngineIOV4Parser) error {
		if size <= 0 {
			return errors.New("maxSerializeSize must be positive")
		}
		p.maxSerializeSize = size
		return nil
	}
}

func (p *EngineIOV4Parser) Parse(data []byte) (*engineio_v4.Message, error) {
	if len(data) == 0 {
		return nil, errors.New("empty message")
	}
	// A leading 'b' marks a base64-encoded binary attachment (engine.io v4
	// represents binary inside a text record this way over HTTP long-polling).
	// Decode it into a binary PacketMessage so callers receive the raw bytes.
	if data[0] == binaryPrefix {
		raw, err := base64.StdEncoding.DecodeString(string(data[1:]))
		if err != nil {
			return nil, fmt.Errorf("decode binary attachment: %w", err)
		}
		return &engineio_v4.Message{
			Type:   engineio_v4.PacketMessage,
			Data:   raw,
			Binary: true,
		}, nil
	}
	// data[0] is an ASCII digit '0'..'6' (0x30..0x36). Subtracting 0x30 maps it
	// to the packet type. Any other byte yields a value greater than PacketNoop
	// (bytes below '0' wrap around in the unsigned subtraction), so a single
	// upper-bound check rejects every malformed type without a separate
	// lower-bound test.
	packetType := engineio_v4.EngineIOPacket(data[0] - 0x30)
	if packetType > engineio_v4.PacketNoop {
		return nil, fmt.Errorf("unknown engine.io packet type: 0x%02x", data[0])
	}
	return &engineio_v4.Message{
		Type: packetType,
		Data: data[1:],
	}, nil
}

func (p *EngineIOV4Parser) Serialize(msg *engineio_v4.Message) ([]byte, error) {
	limit := p.maxSerializeSize
	if limit <= 0 {
		limit = defaultMaxSerializeSize
	}
	if int64(len(msg.Data)) > limit {
		return nil, errors.New("message data too large")
	}
	// A binary attachment is serialized as 'b' followed by the base64 encoding
	// of the raw bytes — the engine.io v4 text-payload representation used over
	// HTTP long-polling.
	if msg.Binary {
		encoded := base64.StdEncoding.EncodeToString(msg.Data)
		packet := make([]byte, 1, len(encoded)+1)
		packet[0] = binaryPrefix
		return append(packet, encoded...), nil
	}
	packet := make([]byte, 1, len(msg.Data)+1)
	packet[0] = byte(msg.Type) + 0x30
	return append(packet, msg.Data...), nil
}

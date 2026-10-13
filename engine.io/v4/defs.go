package engineio_v4

type EngineIOPacket byte

const (
	PacketOpen    EngineIOPacket = 0x00
	PacketClose   EngineIOPacket = 0x01
	PacketPing    EngineIOPacket = 0x02
	PacketPong    EngineIOPacket = 0x03
	PacketMessage EngineIOPacket = 0x04
	PacketUpgrade EngineIOPacket = 0x05
	PacketNoop    EngineIOPacket = 0x06
)

type EngineIOTransport string

const (
	TransportPolling   EngineIOTransport = "polling"
	TransportWebsocket EngineIOTransport = "websocket"
)

type Message struct {
	Force *bool
	Type  EngineIOPacket
	Data  []byte
	// Binary marks a PacketMessage whose Data is a raw binary attachment
	// (engine.io v4 binary payload). Text packets leave it false, preserving
	// the previous behavior for every non-binary frame.
	Binary bool
}

// Frame is a single engine.io frame as delivered from a transport to the
// engine.io client. IsBinary distinguishes a binary attachment (websocket
// binary frame, or a base64 "b"-prefixed record over HTTP long-polling) from a
// regular UTF-8 text frame. The zero value (IsBinary == false) is a text frame,
// which keeps existing transports backward compatible.
type Frame struct {
	Data     []byte
	IsBinary bool
}

type HandshakeResponse struct {
	Sid          string   `json:"sid"`
	Upgrades     []string `json:"upgrades,omitempty"`
	PingInterval int      `json:"pingInterval,omitempty"`
	PingTimeout  int      `json:"pingTimeout,omitempty"`
}

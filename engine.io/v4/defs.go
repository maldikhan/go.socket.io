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
}

type HandshakeResponse struct {
	Sid          string   `json:"sid"`
	Upgrades     []string `json:"upgrades,omitempty"`
	PingInterval int      `json:"pingInterval,omitempty"`
	PingTimeout  int      `json:"pingTimeout,omitempty"`
}

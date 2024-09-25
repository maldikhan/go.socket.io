package socketio_v5

type SocketIOPacket byte

const (
	PacketConnect      SocketIOPacket = 0x00
	PacketDisconnect   SocketIOPacket = 0x01
	PacketEvent        SocketIOPacket = 0x02
	PacketAck          SocketIOPacket = 0x03
	PacketConnectError SocketIOPacket = 0x04
	PacketBinaryEvent  SocketIOPacket = 0x05
	PacketBinaryAck    SocketIOPacket = 0x06
)

type Message struct {
	Type              SocketIOPacket
	BinaryAttachments *int
	NS                string
	AckId             *int
	Event             *Event
	Payload           interface{}
	ErrorMessage      *string
}

type Event struct {
	Name     string
	Payloads []interface{}
}

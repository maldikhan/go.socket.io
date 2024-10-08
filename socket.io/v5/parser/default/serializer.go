package socketio_v5_parser_default

import (
	"encoding/json"
	"errors"
	"strconv"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

func (p *SocketIOV5DefaultParser) Serialize(msg *socketio_v5.Message) ([]byte, error) {
	if msg == nil {
		return nil, errors.New("empty package")
	}
	pkgLen := 1
	var ackId []byte = nil
	var jsonData []byte = nil
	var err error

	switch msg.Type {
	case socketio_v5.PacketBinaryAck, socketio_v5.PacketBinaryEvent:
		return nil, errors.New("biniary events are not supported yet")
	case socketio_v5.PacketEvent:
		if msg.Event == nil || msg.Event.Name == "" {
			return nil, errors.New("wrong event name")
		}
	}

	if msg.NS != "/" {
		pkgLen += (len(msg.NS) + 1)
	}
	if msg.AckId != nil {
		ackId = []byte(strconv.Itoa(*msg.AckId))
		pkgLen += len(ackId)
	}

	if msg.Event != nil {
		data := []interface{}{}
		if msg.Event.Name != "" {
			data = append(data, msg.Event.Name)
		}
		data = append(data, msg.Event.Payloads...)
		jsonData, err = json.Marshal(data)
		if err != nil {
			return nil, err
		}
		pkgLen += len(msg.Event.Name) + 2 + len(jsonData)
	}

	if msg.Payload != nil {
		jsonData, err = json.Marshal(msg.Payload)
		if err != nil {
			return nil, err
		}
		if string(jsonData) == "null" || string(jsonData) == `""` || string(jsonData) == `{}` {
			jsonData = []byte{}
		}
		pkgLen += len(jsonData)
	}

	packet := make([]byte, 1, 1+pkgLen)
	packet[0] = byte(msg.Type) + 0x30

	if msg.NS != "/" && msg.NS != "" {
		packet = append(packet, []byte(msg.NS)...)
		packet = append(packet, ',')
	}

	packet = append(packet, ackId...)

	return append(packet, jsonData...), nil
}

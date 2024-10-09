package socketio_v5_parser_default

import (
	"errors"
	"fmt"
	"strconv"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

type SocketIOV5DefaultParser struct {
	logger        Logger
	payloadParser PayloadParser
}

var ErrParseEventUnsupported = errors.New("unsuported event")
var ErrParsePackage = errors.New("parse package error")

func (p *SocketIOV5DefaultParser) checkData(data []byte) (socketio_v5.SocketIOPacket, error) {
	if len(data) == 0 {
		return socketio_v5.PacketUnknown, fmt.Errorf("%w: %v", ErrParsePackage, "empty message")
	}
	eventType := socketio_v5.SocketIOPacket(data[0] - '0')
	switch eventType {
	case socketio_v5.PacketBinaryEvent, socketio_v5.PacketBinaryAck:
		return eventType, fmt.Errorf("%w: %v", ErrParseEventUnsupported, errors.New("binary events are not supported yet"))
	}
	return eventType, nil
}

func (p *SocketIOV5DefaultParser) extractNamespace(msg *socketio_v5.Message, packetData []byte) []byte {
	if packetData[0] != '/' {
		return packetData
	}
	for i := 0; i < len(packetData); i++ {
		switch packetData[i] {
		case ',':
			msg.NS = string(packetData[:i])
			return packetData[i+1:]
		case '[', '{':
			return packetData
		}
	}
	return packetData
}

func (p *SocketIOV5DefaultParser) extractAckId(msg *socketio_v5.Message, packetData []byte) ([]byte, error) {
	ackEnd := 0
	for i, ch := range packetData {
		if ch < '0' || ch > '9' {
			break
		}
		ackEnd = i + 1
		if ackEnd > 18 { // max int64
			return nil, fmt.Errorf("%w: %v", ErrParsePackage, errors.New("wrong package payload"))
		}
	}
	if ackEnd > 0 {
		ackId, _ := strconv.Atoi(string(packetData[:ackEnd]))
		msg.AckId = &ackId
	}
	return packetData[ackEnd:], nil
}

func (p *SocketIOV5DefaultParser) Parse(data []byte) (*socketio_v5.Message, error) {

	messageType, err := p.checkData(data)
	msg := &socketio_v5.Message{
		NS:   "/",
		Type: messageType,
	}
	if err != nil {
		return msg, err
	}

	packetData := data[1:]
	if len(packetData) == 0 {
		return msg, nil
	}

	packetData = p.extractNamespace(msg, packetData)

	if len(packetData) == 0 {
		switch msg.Type {
		case socketio_v5.PacketEvent, socketio_v5.PacketAck, socketio_v5.PacketConnectError:
			return nil, fmt.Errorf("%w: %v", ErrParsePackage, errors.New("wrong package payload"))
		}
		return msg, nil
	}

	packetData, err = p.extractAckId(msg, packetData)
	if err != nil {
		return nil, err
	}
	if len(packetData) == 0 {
		switch msg.Type {
		case socketio_v5.PacketEvent, socketio_v5.PacketAck, socketio_v5.PacketConnectError:
			return nil, fmt.Errorf("%w: %v", ErrParsePackage, errors.New("wrong package payload"))
		}
		return msg, nil
	}

	switch msg.Type {
	case socketio_v5.PacketEvent, socketio_v5.PacketAck:
		msg.Event, err = p.ParseEvent(packetData, msg.Type == socketio_v5.PacketAck)
	case socketio_v5.PacketConnectError:
		errorMessage := string(packetData)
		msg.ErrorMessage = &errorMessage
	}

	if err != nil {
		return nil, err
	}

	return msg, nil
}

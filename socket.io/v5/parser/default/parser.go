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

func (p *SocketIOV5DefaultParser) Parse(data []byte) (*socketio_v5.Message, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: %v", ErrParsePackage, "empty message")
	}
	msg := &socketio_v5.Message{
		NS:   "/",
		Type: socketio_v5.SocketIOPacket(data[0] - '0'),
	}

	switch msg.Type {
	case socketio_v5.PacketBinaryEvent, socketio_v5.PacketBinaryAck:
		return msg, fmt.Errorf("%w: %v", ErrParseEventUnsupported, errors.New("binary events are not supported yet"))
	}

	packetData := data[1:]
	if len(packetData) == 0 {
		return msg, nil
	}

	// Custom namespace is always started with /
	if packetData[0] == '/' {
	parser:
		for i := 0; i < len(packetData); i++ {
			switch packetData[i] {
			case ',':
				msg.NS = string(packetData[:i])
				packetData = packetData[i+1:]
				break parser
			case '[', '{':
				break parser
			}
		}
	}
	if len(packetData) == 0 {
		switch msg.Type {
		case socketio_v5.PacketEvent, socketio_v5.PacketAck, socketio_v5.PacketConnectError:
			return nil, fmt.Errorf("%w: %v", ErrParsePackage, errors.New("wrong package payload"))
		}
		return msg, nil
	}

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
		packetData = packetData[ackEnd:]
	}

	if len(packetData) == 0 {
		switch msg.Type {
		case socketio_v5.PacketEvent, socketio_v5.PacketAck, socketio_v5.PacketConnectError:
			return nil, fmt.Errorf("%w: %v", ErrParsePackage, errors.New("wrong package payload"))
		}
		return msg, nil
	}

	var err error
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

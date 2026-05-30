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
	return eventType, nil
}

// extractBinaryAttachments reads the binary attachment count that prefixes a
// PacketBinaryEvent/PacketBinaryAck header, e.g. the "1-" in
// `51-["ev",{"_placeholder":true,"num":0}]`. It records the count on msg and
// returns the remaining packet bytes (after the '-'). For non-binary packets it
// is a no-op and returns packetData unchanged.
func (p *SocketIOV5DefaultParser) extractBinaryAttachments(msg *socketio_v5.Message, packetData []byte) ([]byte, error) {
	if msg.Type != socketio_v5.PacketBinaryEvent && msg.Type != socketio_v5.PacketBinaryAck {
		return packetData, nil
	}
	end := 0
	for i, ch := range packetData {
		if ch < '0' || ch > '9' {
			break
		}
		end = i + 1
		if end > 18 { // guard against absurdly long counts
			return nil, fmt.Errorf("%w: %v", ErrParsePackage, errors.New("wrong attachment count"))
		}
	}
	if end == 0 || end >= len(packetData) || packetData[end] != '-' {
		return nil, fmt.Errorf("%w: %v", ErrParsePackage, errors.New("missing attachment count"))
	}
	count, _ := strconv.Atoi(string(packetData[:end]))
	msg.BinaryAttachments = &count
	return packetData[end+1:], nil
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
		switch msg.Type {
		case socketio_v5.PacketBinaryEvent, socketio_v5.PacketBinaryAck:
			return nil, fmt.Errorf("%w: %v", ErrParsePackage, errors.New("wrong package payload"))
		}
		return msg, nil
	}

	// Binary packets carry the attachment count (e.g. "1-") immediately after
	// the type and before the namespace, per the socket.io v5 wire format.
	packetData, err = p.extractBinaryAttachments(msg, packetData)
	if err != nil {
		return nil, err
	}

	packetData = p.extractNamespace(msg, packetData)

	if len(packetData) == 0 {
		switch msg.Type {
		case socketio_v5.PacketEvent, socketio_v5.PacketAck, socketio_v5.PacketConnectError,
			socketio_v5.PacketBinaryEvent, socketio_v5.PacketBinaryAck:
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
		case socketio_v5.PacketEvent, socketio_v5.PacketAck, socketio_v5.PacketConnectError,
			socketio_v5.PacketBinaryEvent, socketio_v5.PacketBinaryAck:
			return nil, fmt.Errorf("%w: %v", ErrParsePackage, errors.New("wrong package payload"))
		}
		return msg, nil
	}

	switch msg.Type {
	case socketio_v5.PacketEvent, socketio_v5.PacketAck,
		socketio_v5.PacketBinaryEvent, socketio_v5.PacketBinaryAck:
		isAck := msg.Type == socketio_v5.PacketAck || msg.Type == socketio_v5.PacketBinaryAck
		msg.Event, err = p.ParseEvent(packetData, isAck)
	case socketio_v5.PacketConnectError:
		errorMessage := string(packetData)
		msg.ErrorMessage = &errorMessage
	}

	if err != nil {
		return nil, err
	}

	return msg, nil
}

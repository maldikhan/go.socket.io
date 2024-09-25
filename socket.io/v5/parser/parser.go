package socketio_v5_parser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	socketio_v5 "maldikhan/go.socket.io/socket.io/v5"
)

type SocketIOV5Parser struct{}

/*
<packet type>[<# of binary attachments>-][<namespace>,][<acknowledgment id>][JSON-stringified payload without binary]

+ binary attachments extracted
*/

func (p *SocketIOV5Parser) Serialize(msg *socketio_v5.Message) ([]byte, error) {
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

func (p *SocketIOV5Parser) Parse(data []byte) (*socketio_v5.Message, error) {
	if len(data) == 0 {
		return nil, errors.New("empty message")
	}
	msg := &socketio_v5.Message{
		NS:   "/",
		Type: socketio_v5.SocketIOPacket(data[0] - 0x30),
	}

	switch msg.Type {
	case socketio_v5.PacketBinaryEvent, socketio_v5.PacketBinaryAck:
		return msg, errors.New("binary events are not supported yet")
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
			return nil, errors.New("wrong package payload")
		}
		return msg, nil
	}

	ackLen := 0
	ackId := 0
parseAck:
	for {
		if ackLen > len(packetData)-1 {
			break
		}

		switch packetData[ackLen] {
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			ackId = ackId*10 + int(packetData[ackLen]-0x30)
			ackLen++
			if ackLen > 18 { //max int64
				return nil, errors.New("wrong package ack data")
			}
			continue parseAck
		}
		break
	}
	if ackLen > 0 {
		msg.AckId = &ackId
		packetData = packetData[ackLen:]
	}
	if len(packetData) == 0 {
		switch msg.Type {
		case socketio_v5.PacketEvent, socketio_v5.PacketAck, socketio_v5.PacketConnectError:
			return nil, errors.New("wrong package payload")
		}
		return msg, nil
	}

	var err error
	switch msg.Type {
	case socketio_v5.PacketEvent, socketio_v5.PacketAck:
		msg.Event, err = p.parseEvent(packetData, msg.Type == socketio_v5.PacketAck)
	case socketio_v5.PacketConnectError:
		errorMessage := string(packetData)
		msg.ErrorMessage = &errorMessage
	}

	if err != nil {
		return nil, err
	}

	return msg, nil
}

func (p *SocketIOV5Parser) parseEvent(data []byte, emptyEvent bool) (*socketio_v5.Event, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))

	if t, err := decoder.Token(); err != nil || t != json.Delim('[') {
		return nil, fmt.Errorf("can't find event entity start: %v", err)
	}

	event := &socketio_v5.Event{}

	if !emptyEvent {
		if err := decoder.Decode(&event.Name); err != nil {
			return nil, fmt.Errorf("error decoding event name: %v", err)
		}
	}

	for decoder.More() {
		var payloadRecord json.RawMessage
		if err := decoder.Decode(&payloadRecord); err != nil {
			return nil, fmt.Errorf("error decoding payload: %v", err)
		}
		event.Payloads = append(event.Payloads, payloadRecord)
	}

	if t, err := decoder.Token(); err != nil || t != json.Delim(']') {
		return nil, fmt.Errorf("can't find event entity end: %v", err)
	}

	return event, nil
}

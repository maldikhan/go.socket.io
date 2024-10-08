package socketio_v5_parser_default

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

var ErrParseEvent = errors.New("parse event error")

func (p *SocketIOV5DefaultParser) ParseEvent(data []byte, isAck bool) (*socketio_v5.Event, error) {

	if p.payloadParser != nil {
		return p.payloadParser.ParseEvent(data, isAck)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))

	if t, err := decoder.Token(); err != nil || t != json.Delim('[') {
		return nil, fmt.Errorf("%w: %v", ErrParseEvent, err)
	}

	event := &socketio_v5.Event{}

	if !isAck {
		if err := decoder.Decode(&event.Name); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrParseEvent, err)
		}
	}

	for decoder.More() {
		var payloadRecord json.RawMessage
		if err := decoder.Decode(&payloadRecord); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrParseEvent, err)
		}
		event.Payloads = append(event.Payloads, payloadRecord)
	}

	if t, err := decoder.Token(); err != nil || t != json.Delim(']') {
		return nil, fmt.Errorf("%w: %v", ErrParseEvent, err)
	}

	return event, nil
}

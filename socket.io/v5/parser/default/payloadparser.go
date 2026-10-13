package socketio_v5_parser_default

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

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
		name, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrParseEvent, err)
		}
		var ok bool
		event.Name, ok = name.(string)
		if !ok {
			return nil, fmt.Errorf("%w: event name must be a string", ErrParseEvent)
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
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing data after event", ErrParseEvent)
	}

	return event, nil
}

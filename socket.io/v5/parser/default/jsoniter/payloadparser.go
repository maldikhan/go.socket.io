package socketio_v5_parser_default_jsoniter

import (
	"errors"
	"fmt"

	jsoniter "github.com/json-iterator/go"
	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

type SocketIOV5PayloadParser struct {
	logger Logger
}

var ErrParseEvent = errors.New("parse event error")

func (p *SocketIOV5PayloadParser) ParseEvent(data []byte, isAck bool) (*socketio_v5.Event, error) {
	iter := jsoniter.ConfigFastest.BorrowIterator(data)
	defer jsoniter.ConfigFastest.ReturnIterator(iter)

	event := &socketio_v5.Event{}

	if !isAck {
		if !iter.ReadArray() {
			return nil, fmt.Errorf("%w: %v", ErrParseEvent, errors.New("can't find event entity start"))
		}

		event.Name = iter.ReadString()
		if iter.Error != nil {
			return nil, fmt.Errorf("%w: %v", ErrParseEvent, iter.Error)
		}
	}

	for iter.ReadArray() {
		// In jsoniter WhatIsNext is not clearly idempotent,
		// it moves pointer to the next entity start, skipping all spaces, \t \n \r
		iter.WhatIsNext()
		payload := iter.SkipAndReturnBytes()
		if iter.Error != nil {
			return nil, fmt.Errorf("%w: %v", ErrParseEvent, iter.Error)
		}
		event.Payloads = append(event.Payloads, payload) // payload)
	}

	if iter.Error != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseEvent, iter.Error)
	}

	return event, nil
}

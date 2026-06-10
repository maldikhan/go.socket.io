package socketio_v5_parser_default

import (
	"encoding/json"
	"reflect"
)

func (p *SocketIOV5DefaultParser) WrapCallback(callback interface{}) func(in []interface{}) {

	if p.payloadParser != nil {
		return p.payloadParser.WrapCallback(callback)
	}

	switch v := callback.(type) {
	case func([]interface{}):
		return v
	}

	callbackValue := reflect.ValueOf(callback)
	callbackType := callbackValue.Type()

	if callbackType.Kind() != reflect.Func {
		p.logger.Errorf("callback must be a function")
		return nil
	}

	return func(in []interface{}) {
		// A variadic callback (e.g. func(args ...interface{})) only requires
		// its fixed parameters: when exactly those are supplied, the variadic
		// slot is omitted and reflect's Call invokes the function with an
		// empty variadic list. Without this, a handler registered for a
		// reserved lifecycle event ("reconnect", "reconnecting",
		// "reconnect_failed") — which is dispatched with no payload — would be
		// silently rejected by the arity check below.
		numIn := callbackType.NumIn()
		if callbackType.IsVariadic() && len(in) == numIn-1 {
			numIn--
		} else if len(in) < numIn {
			p.logger.Errorf("Error: expected %d arguments, got %d\n", callbackType.NumIn(), len(in))
			return
		}

		args := make([]reflect.Value, numIn)
		for i := 0; i < numIn; i++ {
			argType := callbackType.In(i)
			argValue := reflect.New(argType).Interface()
			data, ok := in[i].(json.RawMessage)
			if !ok {
				p.logger.Errorf("Wrong data in %d json entity", i)
				return
			}
			if err := json.Unmarshal(data, argValue); err != nil {
				p.logger.Errorf("Error unmarshaling argument %d (%v): %v\n", i, in[i], err)
				return
			}
			args[i] = reflect.ValueOf(argValue).Elem()
		}

		callbackValue.Call(args)
	}
}

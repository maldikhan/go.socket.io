package socketio_v5_parser_default_jsoniter

import (
	"reflect"

	jsoniter "github.com/json-iterator/go"
)

func (p *SocketIOV5PayloadParser) WrapCallback(callback interface{}) func(in []interface{}) {
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

	numIn := callbackType.NumIn()
	argTypes := make([]reflect.Type, numIn)
	for i := 0; i < numIn; i++ {
		argTypes[i] = callbackType.In(i)
	}

	return func(in []interface{}) {
		if len(in) < numIn {
			p.logger.Errorf("Error: expected %d arguments, got %d\n", numIn, len(in))
			return
		}

		args := make([]reflect.Value, numIn)
		for i := 0; i < numIn; i++ {
			data, ok := in[i].([]byte)
			if !ok {
				p.logger.Errorf("Wrong data in %d json entity", i)
				return
			}

			argValue := reflect.New(argTypes[i]).Interface()
			if err := jsoniter.ConfigFastest.Unmarshal(data, argValue); err != nil {
				p.logger.Errorf("Error unmarshaling argument %d: %v\n", i, err)
				return
			}
			args[i] = reflect.ValueOf(argValue).Elem()
		}

		callbackValue.Call(args)
	}
}

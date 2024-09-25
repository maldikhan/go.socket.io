package socketio_v5_client

import (
	"encoding/json"
	"reflect"
)

func (n *namespace) parseEventCallback(callback interface{}) func(in []interface{}) {
	callbackValue := reflect.ValueOf(callback)
	callbackType := callbackValue.Type()

	if callbackType.Kind() != reflect.Func {
		panic("callback must be a function")
	}

	return func(in []interface{}) {
		if len(in) < callbackType.NumIn() {
			n.client.logger.Errorf("Error: expected %d arguments, got %d\n", callbackType.NumIn(), len(in))
			return
		}

		args := make([]reflect.Value, callbackType.NumIn())
		for i := 0; i < callbackType.NumIn(); i++ {
			argType := callbackType.In(i)
			argValue := reflect.New(argType).Interface()
			data, ok := in[i].(json.RawMessage)
			if !ok {
				n.client.logger.Errorf("Wrong data in %d json entity", i)
				return
			}
			if err := json.Unmarshal(data, argValue); err != nil {
				n.client.logger.Errorf("Error unmarshaling argument %d (%v): %v\n", i, in[i], err)
				return
			}
			args[i] = reflect.ValueOf(argValue).Elem()
		}

		callbackValue.Call(args)
	}
}

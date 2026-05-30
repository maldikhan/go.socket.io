package socketio_v5_parser_default

import (
	"encoding/json"
	"reflect"
)

// canMarshalBind reports whether a reconstructed composite value (a decoded
// map/slice that carried binary at a nested position) may be bound to a
// parameter of type t via a json.Marshal+json.Unmarshal round-trip. Only
// composite targets qualify: a struct, map, slice or array (optionally behind a
// pointer). Scalar targets (string, int, ...) are excluded so that, e.g., a
// reconstructed value is never coerced into a misleading string/number.
func canMarshalBind(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array:
		return true
	default:
		return false
	}
}

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
		if len(in) < callbackType.NumIn() {
			p.logger.Errorf("Error: expected %d arguments, got %d\n", callbackType.NumIn(), len(in))
			return
		}

		args := make([]reflect.Value, callbackType.NumIn())
		for i := 0; i < callbackType.NumIn(); i++ {
			argType := callbackType.In(i)

			// Values that are not json.RawMessage are already-decoded Go values
			// produced by binary attachment reconstruction (e.g. a []byte for a
			// top-level binary, or a map[string]interface{} / []interface{} that
			// contains []byte at a nested position). They cannot be parsed as raw
			// JSON, so they are bound to the callback parameter as follows.
			data, isRaw := in[i].(json.RawMessage)
			if !isRaw {
				argValueOf := reflect.ValueOf(in[i])
				if !argValueOf.IsValid() {
					p.logger.Errorf("Wrong data in %d json entity", i)
					return
				}
				// 1) Directly assignable: pass through unchanged. This covers a
				//    top-level []byte binding to a []byte/interface{} parameter
				//    and a reconstructed map/slice binding to an identically
				//    typed parameter.
				if argValueOf.Type().AssignableTo(argType) {
					args[i] = argValueOf
					continue
				}
				// 2) A reconstructed []byte is the binary LEAF. It must only ever
				//    bind to []byte/interface{} (handled above). It must NOT be
				//    re-encoded to base64 and pushed into, say, a string
				//    parameter, so reject anything else explicitly.
				if isByteSlice(argValueOf.Type()) {
					p.logger.Errorf("binary []byte payload not assignable to argument %d (%s)", i, argType.String())
					return
				}
				// 3) A reconstructed composite value (map/slice/etc. that
				//    contained binary at a nested position) targeting a struct,
				//    map or slice parameter: round-trip through encoding/json so
				//    that struct fields (including []byte<->base64) are populated.
				if canMarshalBind(argType) {
					marshaled, err := json.Marshal(in[i])
					if err != nil {
						p.logger.Errorf("Error marshaling argument %d (%v): %v\n", i, in[i], err)
						return
					}
					argValue := reflect.New(argType).Interface()
					if err := json.Unmarshal(marshaled, argValue); err != nil {
						p.logger.Errorf("Error unmarshaling argument %d (%v): %v\n", i, in[i], err)
						return
					}
					args[i] = reflect.ValueOf(argValue).Elem()
					continue
				}
				p.logger.Errorf("Wrong data in %d json entity", i)
				return
			}

			argValue := reflect.New(argType).Interface()
			if err := json.Unmarshal(data, argValue); err != nil {
				p.logger.Errorf("Error unmarshaling argument %d (%v): %v\n", i, in[i], err)
				return
			}
			args[i] = reflect.ValueOf(argValue).Elem()
		}

		callbackValue.Call(args)
	}
}

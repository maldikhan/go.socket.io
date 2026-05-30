package socketio_v5_parser_default

import (
	"encoding/json"
	"reflect"
)

// isConvertibleTarget reports whether a reconstructed composite value
// (map/slice produced by binary reassembly) may be bound to the parameter type
// by re-marshaling to JSON and unmarshaling into it. Only struct, map and slice
// targets qualify; scalar parameters (e.g. string, int) are rejected so a
// composite value is never silently coerced. Pointers are followed.
func isConvertibleTarget(t reflect.Type) bool {
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
			// contains []byte at a nested position). They cannot be JSON
			// unmarshaled from raw bytes, so they are bound directly.
			data, isRaw := in[i].(json.RawMessage)
			if !isRaw {
				argValueOf := reflect.ValueOf(in[i])
				// Direct assignment when the concrete type already matches the
				// parameter (e.g. []byte -> []byte, map[string]interface{} ->
				// map[string]interface{}, or any value -> interface{}).
				if argValueOf.IsValid() && argValueOf.Type().AssignableTo(argType) {
					args[i] = argValueOf
					continue
				}
				// A raw binary leaf ([]byte) is only meaningful for a []byte or
				// interface{} parameter (handled above). It must NOT be coerced
				// into other scalar/struct types (e.g. base64 string), so reject.
				if _, isBytes := in[i].([]byte); isBytes {
					p.logger.Errorf("Wrong data in %d json entity", i)
					return
				}
				// Composite reconstructed value (struct / map / slice target whose
				// concrete type differs, e.g. map[string]interface{} carrying
				// []byte -> struct{File []byte}). Re-encode to JSON and decode into
				// a fresh parameter; encoding/json round-trips []byte <-> base64 so
				// byte fields are restored intact.
				if isConvertibleTarget(argType) {
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

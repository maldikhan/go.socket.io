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
			// unmarshaled directly, so assign them to the callback parameter via
			// reflection when the concrete type is assignable.
			data, isRaw := in[i].(json.RawMessage)
			if !isRaw {
				argValueOf := reflect.ValueOf(in[i])
				if argValueOf.IsValid() && argValueOf.Type().AssignableTo(argType) {
					args[i] = argValueOf
					continue
				}

				// A raw binary leaf ([]byte) that is not assignable to the target
				// parameter must be rejected, exactly as before: it must never be
				// re-encoded into a base64 string and handed to, say, func(string).
				// Only composite reconstructed values (maps/slices/structs) may be
				// converted to the target via a JSON marshal/unmarshal round trip,
				// which lets handlers receive Go structs (incl. []byte fields,
				// which json round-trips through base64) for binary events.
				if converted, ok := p.convertViaJSON(in[i], argType); ok {
					args[i] = converted
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

// convertViaJSON attempts to bind an already-decoded composite value (produced
// by binary attachment reconstruction) to a callback parameter of type argType
// by marshaling it to JSON and unmarshaling into a fresh argType instance. This
// is how a handler such as func(struct{ File []byte }) receives a reconstructed
// {"file": <bytes>} value: encoding/json round-trips []byte through base64, so
// the struct field ends up holding the original bytes.
//
// To preserve the historical "reject mismatched type" semantics, this is only
// applied to composite values (maps, slices, arrays, structs and pointers to
// them). A raw binary leaf ([]byte) destined for a non-[]byte parameter is NOT
// converted here (it returns ok == false), so it continues to be rejected by
// the caller instead of being silently turned into a base64 string.
func (p *SocketIOV5DefaultParser) convertViaJSON(value interface{}, argType reflect.Type) (reflect.Value, bool) {
	if !isConvertibleComposite(value) {
		return reflect.Value{}, false
	}
	raw, err := json.Marshal(value)
	if err != nil {
		p.logger.Errorf("Error marshaling reconstructed value: %v", err)
		return reflect.Value{}, false
	}
	argValue := reflect.New(argType).Interface()
	if err := json.Unmarshal(raw, argValue); err != nil {
		p.logger.Errorf("Error converting reconstructed value to %v: %v", argType, err)
		return reflect.Value{}, false
	}
	return reflect.ValueOf(argValue).Elem(), true
}

// isConvertibleComposite reports whether value is a composite (map, slice,
// array, struct, or pointer to one) for which a JSON marshal/unmarshal
// conversion to a typed parameter is appropriate. A []byte (the binary leaf) is
// explicitly excluded so it cannot be re-encoded as a base64 string for a
// non-[]byte parameter.
func isConvertibleComposite(value interface{}) bool {
	if value == nil {
		return false
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice:
		// []byte is the binary leaf, not a convertible composite.
		return rv.Type().Elem().Kind() != reflect.Uint8
	case reflect.Array, reflect.Map, reflect.Struct, reflect.Ptr:
		return true
	default:
		return false
	}
}

package socketio_v5_parser_default

import (
	"encoding/json"
	"reflect"
)

var rawMessageType = reflect.TypeOf(json.RawMessage{})

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
			// unmarshaled directly, so first try a zero-cost reflect assignment
			// when the concrete type already matches the callback parameter
			// (the fast path for []byte and map[string]interface{} handlers).
			data, isRaw := in[i].(json.RawMessage)
			if !isRaw {
				if in[i] == nil {
					// A reconstructed JSON null arrives as a nil interface{}, for which
					// reflect.ValueOf is invalid. Mirror json.Unmarshal("null", ...),
					// which leaves the target at its zero value for any type (nil for
					// pointers/interfaces/maps/slices), so a null argument alongside a
					// binary attachment behaves exactly as it does on the non-binary
					// path instead of being rejected as "wrong data".
					args[i] = reflect.Zero(argType)
					continue
				}
				argValueOf := reflect.ValueOf(in[i])
				// json.RawMessage parameters must always hold valid JSON, but a
				// reconstructed binary attachment is a plain []byte of arbitrary
				// bytes — assignable to RawMessage, yet not JSON. Skip the direct
				// assignment for RawMessage targets so the value takes the
				// marshal round-trip below, which encodes the bytes as a base64
				// JSON string (the same representation typed []byte fields see).
				if argValueOf.IsValid() && argValueOf.Type().AssignableTo(argType) && argType != rawMessageType {
					args[i] = argValueOf
					continue
				}
				// A top-level []byte that is not directly assignable must NOT be
				// silently coerced into a string/other type (it would marshal to a
				// base64 string), so it is rejected just like before — UNLESS the
				// parameter is itself a uint8-slice kind (a defined byte-slice type
				// such as `type Blob []Octet` with `type Octet uint8`), which the
				// round-trip decodes back into the typed bytes exactly as it would
				// for the equivalent base64 JSON payload. Other reconstructed values
				// (e.g. a map[string]interface{} containing []byte) fall back to a
				// json.Marshal/Unmarshal round-trip so typed handlers such as
				// func(v struct{ File []byte }) still receive the value;
				// encoding/json round-trips []byte through base64 back into a
				// []byte field, reproducing the original bytes.
				_, isBytes := in[i].([]byte)
				bytesTarget := argType.Kind() == reflect.Slice && argType.Elem().Kind() == reflect.Uint8
				if (!isBytes || bytesTarget) && argValueOf.IsValid() {
					converted := reflect.New(argType).Interface()
					marshaled, err := json.Marshal(in[i])
					if err != nil {
						p.logger.Errorf("Error marshaling argument %d (%v): %v\n", i, in[i], err)
						return
					}
					if err := json.Unmarshal(marshaled, converted); err != nil {
						p.logger.Errorf("Error unmarshaling argument %d (%v): %v\n", i, in[i], err)
						return
					}
					args[i] = reflect.ValueOf(converted).Elem()
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

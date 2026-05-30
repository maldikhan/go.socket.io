package socketio_v5_parser_default

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// maxBinaryWalkDepth bounds the recursion used by the reflection based binary
// walkers so that pathological or cyclic (via pointers/interfaces) structures
// cannot cause an unbounded recursion / hang. Socket.IO payloads are not deep.
const maxBinaryWalkDepth = 64

// isByteSlice reports whether t is the binary leaf type: a slice whose element
// kind is uint8 (i.e. []byte or any named type with underlying []byte). It is
// deliberately NOT true for [N]byte arrays (encoding/json treats those as JSON
// arrays of numbers, not base64), and never true for []string etc.
func isByteSlice(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8
}

// reflectHasBinary reports whether v (or anything it transitively contains)
// holds a []byte leaf, using reflection so that structs, pointers, typed maps
// and named slices/arrays are inspected. It is the reflection counterpart of
// valueHasBinary and MUST stay in lockstep with reflectExtractBinary: every
// position the extractor would emit an attachment must be reported here, and
// vice versa, otherwise the header attachment count and the produced
// attachments disagree.
func reflectHasBinary(v reflect.Value, depth int) bool {
	if depth > maxBinaryWalkDepth {
		return false
	}
	if !v.IsValid() {
		return false
	}

	// Binary leaf: []byte (and named types whose underlying type is []byte).
	if isByteSlice(v.Type()) {
		return true
	}

	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return false
		}
		return reflectHasBinary(v.Elem(), depth+1)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if sf.PkgPath != "" {
				// Unexported field: skipped, matching encoding/json.
				continue
			}
			_, _, skip := parseJSONFieldTag(sf)
			if skip {
				continue
			}
			if reflectHasBinary(v.Field(i), depth+1) {
				return true
			}
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			if reflectHasBinary(v.MapIndex(key), depth+1) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		// (isByteSlice already returned above for the []byte leaf case.)
		for i := 0; i < v.Len(); i++ {
			if reflectHasBinary(v.Index(i), depth+1) {
				return true
			}
		}
	}
	return false
}

// reflectExtractBinary converts v into an interface{} tree that mirrors what
// encoding/json would produce for v, except that every []byte leaf is replaced
// by a {"_placeholder":true,"num":N} marker and the bytes are appended, in walk
// order, to *attachments. json struct tags (name, "-", omitempty) are honored
// so the resulting header JSON matches encoding/json for the non-binary parts.
//
// It MUST visit []byte leaves in exactly the same positions, and in the same
// order, as reflectHasBinary detects them, so the attachment count in the
// header equals len(attachments).
func reflectExtractBinary(v reflect.Value, attachments *[][]byte, depth int) interface{} {
	if depth > maxBinaryWalkDepth {
		return nil
	}
	if !v.IsValid() {
		return nil
	}

	// Binary leaf.
	if isByteSlice(v.Type()) {
		if v.IsNil() {
			// encoding/json renders a nil []byte as null; preserve that and do
			// not allocate an attachment for it.
			return nil
		}
		num := len(*attachments)
		buf := make([]byte, v.Len())
		reflect.Copy(reflect.ValueOf(buf), v)
		*attachments = append(*attachments, buf)
		return placeholderMarker(num)
	}

	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return reflectExtractBinary(v.Elem(), attachments, depth+1)
	case reflect.Struct:
		t := v.Type()
		out := make(map[string]interface{})
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if sf.PkgPath != "" {
				continue
			}
			name, omitempty, skip := parseJSONFieldTag(sf)
			if skip {
				continue
			}
			field := v.Field(i)
			if omitempty && isEmptyValue(field) {
				continue
			}
			key := name
			if key == "" {
				key = sf.Name
			}
			out[key] = reflectExtractBinary(field, attachments, depth+1)
		}
		return out
	case reflect.Map:
		out := make(map[string]interface{}, v.Len())
		// Deterministic order so the produced JSON / attachment order is stable
		// (encoding/json also sorts string map keys).
		keys := v.MapKeys()
		strKeys := make([]string, 0, len(keys))
		keyByStr := make(map[string]reflect.Value, len(keys))
		for _, k := range keys {
			ks := mapKeyString(k)
			strKeys = append(strKeys, ks)
			keyByStr[ks] = k
		}
		sort.Strings(strKeys)
		for _, ks := range strKeys {
			out[ks] = reflectExtractBinary(v.MapIndex(keyByStr[ks]), attachments, depth+1)
		}
		return out
	case reflect.Slice, reflect.Array:
		n := v.Len()
		out := make([]interface{}, n)
		for i := 0; i < n; i++ {
			out[i] = reflectExtractBinary(v.Index(i), attachments, depth+1)
		}
		return out
	default:
		// Scalars (and anything else) are returned as their Go value; json.Marshal
		// in serializeHeader produces the correct JSON for them.
		return v.Interface()
	}
}

// placeholderMarker builds the socket.io binary placeholder object for index n.
func placeholderMarker(n int) map[string]interface{} {
	return map[string]interface{}{"_placeholder": true, "num": n}
}

// parseJSONFieldTag extracts the json tag information for a struct field,
// matching encoding/json semantics. It returns the resolved JSON name (empty
// means "use the Go field name"), whether omitempty is set, and whether the
// field is dropped entirely (json:"-" without an alternate name).
func parseJSONFieldTag(sf reflect.StructField) (name string, omitempty bool, skip bool) {
	tag, ok := sf.Tag.Lookup("json")
	if !ok {
		return "", false, false
	}
	// `json:"-"` drops the field; `json:"-,"` is the escape hatch for a field
	// literally named "-".
	if tag == "-" {
		return "", false, true
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty, false
}

// isEmptyValue mirrors encoding/json's notion of an empty value for the
// omitempty option.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	}
	return false
}

// mapKeyString renders a map key the way encoding/json would key the resulting
// JSON object. JSON object keys must be strings; encoding/json also accepts
// integer-keyed maps, rendering the key as its base-10 string.
func mapKeyString(k reflect.Value) string {
	switch k.Kind() {
	case reflect.String:
		return k.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(k.Uint(), 10)
	default:
		return ""
	}
}

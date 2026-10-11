package module

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Wire is v as a host encodes it: the values msgpack and JSON both carry — nil, bool, integers,
// floats, string, []byte, []any and map[string]any. A struct becomes a map by its json tags
// (omitempty and "-" honoured), a time its RFC 3339 text. Anything else is an error: a handler's
// result type is a bug, never a client's.
func Wire(v any) (any, error) {
	return wire(reflect.ValueOf(v))
}

func wire(v reflect.Value) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	if t, ok := v.Interface().(time.Time); ok {
		return t.Format(time.RFC3339), nil
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil, nil
		}
		return wire(v.Elem())
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint(), nil
	case reflect.Float32, reflect.Float64:
		return v.Float(), nil
	case reflect.String:
		return v.String(), nil
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v.Bytes(), nil
		}
		if v.IsNil() {
			return []any{}, nil
		}
		fallthrough
	case reflect.Array:
		out := make([]any, v.Len())
		for i := range v.Len() {
			e, err := wire(v.Index(i))
			if err != nil {
				return nil, err
			}
			out[i] = e
		}
		return out, nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("module.Wire: a map's keys must be strings, not %s", v.Type().Key())
		}
		out := make(map[string]any, v.Len())
		for it := v.MapRange(); it.Next(); {
			e, err := wire(it.Value())
			if err != nil {
				return nil, err
			}
			out[it.Key().String()] = e
		}
		return out, nil
	case reflect.Struct:
		out := map[string]any{}
		t := v.Type()
		for i := range t.NumField() {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			name, opts, _ := strings.Cut(sf.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = sf.Name
			}
			fv := v.Field(i)
			if strings.Contains(opts, "omitempty") && empty(fv) {
				continue
			}
			e, err := wire(fv)
			if err != nil {
				return nil, err
			}
			out[name] = e
		}
		return out, nil
	}
	return nil, fmt.Errorf("module.Wire: cannot carry %s", v.Type())
}

// empty is json's omitempty test: false, 0, a nil pointer or interface, and an empty string, slice
// or map.
func empty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Struct:
		return false
	}
	return v.IsZero()
}

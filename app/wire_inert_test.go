package app

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// module.Wire flattens an embedded struct tagged json:",inline" (todo.Assigned), and is otherwise
// inert: for every answer type a daemon module registers (Verb.Returns), a value with every field
// set encodes to the same wire with this build's Wire as with canary's (frozen below), but for the
// answers that embed an inline struct, todos.add's and todos.assign's. A type a later change adds
// is checked the same way, by its own registration.
func TestWireIsInertButForAnInlineStruct(t *testing.T) {
	var differ, checked []string
	for _, m := range modules(nil) {
		for _, v := range m.Verbs() {
			if v.Returns == nil {
				continue
			}
			checked = append(checked, v.Verb)
			val := filled(v.Returns, 0)
			now, err := module.Wire(val.Interface())
			if err != nil {
				t.Errorf("%s: %s: %v", v.Verb, v.Returns, err)
				continue
			}
			was, err := canaryWire(val)
			if err != nil {
				t.Errorf("%s: %s, canary's Wire: %v", v.Verb, v.Returns, err)
				continue
			}
			if !reflect.DeepEqual(now, was) {
				differ = append(differ, v.Verb)
			}
		}
	}
	slices.Sort(differ)
	if want := []string{"todos.add", "todos.assign"}; !slices.Equal(differ, want) {
		t.Errorf("verbs whose wire changed: %q; want %q only (the inline Assigned)", differ, want)
	}
	if len(checked) < 40 {
		t.Errorf("checked %d verbs: %s; want every module verb's answer", len(checked), strings.Join(checked, " "))
	}
	t.Logf("%d verbs' answers checked", len(checked))
}

// filled is a value of t with every field set: a string "s", numbers 1, true, a slice of one
// element, a map of one key, a pointer to a filled value, a time; an interface stays nil. depth
// bounds a type that holds itself.
func filled(t reflect.Type, depth int) reflect.Value {
	v := reflect.New(t).Elem()
	if depth > 6 {
		return v
	}
	if t == reflect.TypeFor[time.Time]() {
		v.Set(reflect.ValueOf(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)))
		return v
	}
	switch t.Kind() {
	case reflect.String:
		v.SetString("s")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Pointer:
		v.Set(filled(t.Elem(), depth+1).Addr())
	case reflect.Slice:
		s := reflect.MakeSlice(t, 1, 1)
		s.Index(0).Set(filled(t.Elem(), depth+1))
		v.Set(s)
	case reflect.Array:
		for i := range v.Len() {
			v.Index(i).Set(filled(t.Elem(), depth+1))
		}
	case reflect.Map:
		mp := reflect.MakeMap(t)
		key := reflect.New(t.Key()).Elem()
		if t.Key().Kind() == reflect.String {
			key.SetString("k")
		}
		mp.SetMapIndex(key, filled(t.Elem(), depth+1))
		v.Set(mp)
	case reflect.Struct:
		for i := range t.NumField() {
			if t.Field(i).IsExported() {
				v.Field(i).Set(filled(t.Field(i).Type, depth+1))
			}
		}
	}
	return v
}

// canaryWire is module.Wire as canary 80ff431 has it, before an inline struct was flattened: the
// baseline the cell above compares against. Frozen: never edit it to follow Wire.
func canaryWire(v reflect.Value) (any, error) {
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
		return canaryWire(v.Elem())
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
			e, err := canaryWire(v.Index(i))
			if err != nil {
				return nil, err
			}
			out[i] = e
		}
		return out, nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("canaryWire: a map's keys must be strings, not %s", v.Type().Key())
		}
		out := make(map[string]any, v.Len())
		for it := v.MapRange(); it.Next(); {
			e, err := canaryWire(it.Value())
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
			if strings.Contains(opts, "omitempty") && canaryEmpty(fv) {
				continue
			}
			e, err := canaryWire(fv)
			if err != nil {
				return nil, err
			}
			out[name] = e
		}
		return out, nil
	}
	return nil, fmt.Errorf("canaryWire: cannot carry %s", v.Type())
}

// empty is json's omitempty test: false, 0, a nil pointer or interface, and an empty string, slice
// or map.
func canaryEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Struct:
		return false
	}
	return v.IsZero()
}

package module

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/yongjohnlee80/golib/errs"
)

// ErrInvalidParams is a call whose params do not fit its contract. The host answers it as invalid
// params, with the ParamError's text: it names a param and a type, never a value.
var ErrInvalidParams = errs.Sentinel(errs.ErrInvalidArgument, "module: invalid params")

// ParamError is one param that does not fit.
type ParamError struct {
	Verb, Param, Want string
}

func (e *ParamError) Error() string {
	return fmt.Sprintf("%s: %s must be %s", e.Verb, e.Param, e.Want)
}

func (e *ParamError) Unwrap() error { return ErrInvalidParams }

// Decoder is a param type that decodes itself from its wire value: one param that may be a string
// or a map, say. ParamType is what its contract shows. The method set is the pointer's.
type Decoder interface {
	DecodeParam(v any) error
	ParamType() string
}

var decoderType = reflect.TypeFor[Decoder]()

// field is one of P's params: where it sits in P, and how its wire value becomes it.
type field struct {
	index  int
	param  Param
	decode func(v any, dst reflect.Value) error
}

// Handle makes a typed Verb. P is a struct whose exported fields, in order, are the positional
// params: a `param:"name"` tag names one (`param:"name,optional"` for a trailing optional one), a
// `doc:"…"` tag documents it, and its Go type gives its Param.Type. c.Params must be empty: Handle
// derives them, so the contract cannot drift from the decoder. A P that cannot be expressed panics,
// at registration.
func Handle[P, R any](c Contract, fn func(context.Context, P) (R, error)) Verb {
	if len(c.Params) > 0 {
		panic("module.Handle: " + c.Verb + ": Params are derived from the handler's type, not given")
	}
	pt := reflect.TypeFor[P]()
	if pt.Kind() != reflect.Struct {
		panic("module.Handle: " + c.Verb + ": params must be a struct, not " + pt.String())
	}
	fields := fieldsOf(c.Verb, pt)
	for _, f := range fields {
		c.Params = append(c.Params, f.param)
	}
	verb := c.Verb
	return Verb{Contract: c, Call: func(ctx context.Context, params []any) (any, error) {
		if len(params) > len(fields) {
			return nil, &ParamError{Verb: verb, Param: "params", Want: fmt.Sprintf("at most %d", len(fields))}
		}
		var p P
		pv := reflect.ValueOf(&p).Elem()
		for i, f := range fields {
			if i >= len(params) || params[i] == nil {
				if !f.param.Optional {
					return nil, &ParamError{Verb: verb, Param: f.param.Name, Want: "given"}
				}
				continue
			}
			if err := f.decode(params[i], pv.Field(f.index)); err != nil {
				return nil, &ParamError{Verb: verb, Param: f.param.Name, Want: f.param.Type}
			}
		}
		return fn(ctx, p)
	}}
}

func fieldsOf(verb string, pt reflect.Type) []field {
	var out []field
	optional := false
	for i := range pt.NumField() {
		sf := pt.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(sf.Tag.Get("param"), ",")
		if name == "" {
			name = strings.ToLower(sf.Name)
		}
		p := Param{Name: name, Optional: opts == "optional", Doc: sf.Tag.Get("doc")}
		if optional && !p.Optional {
			panic("module.Handle: " + verb + ": " + name + " is required after an optional param")
		}
		optional = p.Optional
		var dec func(any, reflect.Value) error
		p.Type, dec = decoderFor(verb, name, sf.Type)
		out = append(out, field{index: i, param: p, decode: dec})
	}
	return out
}

// decoderFor is a field type's Param.Type and the function that sets it from a wire value.
func decoderFor(verb, name string, t reflect.Type) (string, func(any, reflect.Value) error) {
	if reflect.PointerTo(t).Implements(decoderType) {
		typ := reflect.New(t).Interface().(Decoder).ParamType()
		return typ, func(v any, dst reflect.Value) error { return dst.Addr().Interface().(Decoder).DecodeParam(v) }
	}
	switch {
	case t.Kind() == reflect.String:
		return "string", func(v any, dst reflect.Value) error {
			s, ok := v.(string)
			if !ok {
				return errWrongType
			}
			dst.SetString(s)
			return nil
		}
	case t.Kind() == reflect.Bool:
		return "bool", func(v any, dst reflect.Value) error {
			b, ok := v.(bool)
			if !ok {
				return errWrongType
			}
			dst.SetBool(b)
			return nil
		}
	case t.Kind() == reflect.Int || t.Kind() == reflect.Int64:
		return "int", func(v any, dst reflect.Value) error {
			n, ok := wireInt(v)
			if !ok {
				return errWrongType
			}
			dst.SetInt(n)
			return nil
		}
	case t == reflect.TypeFor[[]byte]():
		return "bytes", func(v any, dst reflect.Value) error {
			switch b := v.(type) {
			case []byte:
				dst.SetBytes(b)
			case string:
				dst.SetBytes([]byte(b))
			default:
				return errWrongType
			}
			return nil
		}
	case t == reflect.TypeFor[[]string]():
		return "[]string", func(v any, dst reflect.Value) error {
			l, ok := v.([]any)
			if !ok {
				return errWrongType
			}
			out := make([]string, len(l))
			for i, e := range l {
				if out[i], ok = e.(string); !ok {
					return errWrongType
				}
			}
			dst.Set(reflect.ValueOf(out))
			return nil
		}
	case t == reflect.TypeFor[map[string]any]():
		return "map", func(v any, dst reflect.Value) error {
			m, ok := v.(map[string]any)
			if !ok {
				return errWrongType
			}
			dst.Set(reflect.ValueOf(m))
			return nil
		}
	case t.Kind() == reflect.Interface && t.NumMethod() == 0:
		return "any", func(v any, dst reflect.Value) error {
			dst.Set(reflect.ValueOf(v))
			return nil
		}
	case t.Kind() == reflect.Struct:
		// a map param decoded by the struct's json tags: an options map
		return "map", func(v any, dst reflect.Value) error {
			m, ok := v.(map[string]any)
			if !ok {
				return errWrongType
			}
			b, err := json.Marshal(m)
			if err != nil {
				return errWrongType
			}
			dec := json.NewDecoder(strings.NewReader(string(b)))
			dec.DisallowUnknownFields()
			return dec.Decode(dst.Addr().Interface())
		}
	}
	panic(fmt.Sprintf("module.Handle: %s: param %s has a type the wire cannot carry: %s", verb, name, t))
}

var errWrongType = errs.Sentinel(errs.ErrInvalidArgument, "module: wrong type")

// wireInt is an integer as the wire carries it: msgpack's signed or unsigned, or JSON's float when
// it is whole (--call reads JSON).
func wireInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case uint64:
		if n > 1<<63-1 {
			return 0, false
		}
		return int64(n), true
	case float64:
		if n != float64(int64(n)) {
			return 0, false
		}
		return int64(n), true
	}
	return 0, false
}

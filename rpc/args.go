package rpc

import (
	"fmt"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// Positional parameters arrive in msgpack's vocabulary: int64, string, []byte, []any,
// map[string]any. These read them, answering InvalidParams for a wrong shape.

func invalid(msg string) error {
	return &golibrpc.Error{Code: golibrpc.CodeInvalidParams, Message: msg}
}

func argsBetween(p []any, lo, hi int) error {
	if len(p) < lo || len(p) > hi {
		if lo == hi {
			return invalid(fmt.Sprintf("want %d parameters, got %d", lo, len(p)))
		}
		return invalid(fmt.Sprintf("want %d to %d parameters, got %d", lo, hi, len(p)))
	}
	return nil
}

func exactArgs(p []any, n int) error { return argsBetween(p, n, n) }

func argStr(p []any, i int, name string) (string, error) {
	if i >= len(p) {
		return "", invalid(name + " is required")
	}
	s, ok := p[i].(string)
	if !ok {
		return "", invalid(name + " must be a string")
	}
	return s, nil
}

func argInt(p []any, i int, name string) (int64, error) {
	if i >= len(p) {
		return 0, invalid(name + " is required")
	}
	switch n := p[i].(type) {
	case int64:
		return n, nil
	case uint64:
		if n <= 1<<63-1 {
			return int64(n), nil
		}
	}
	return 0, invalid(name + " must be an integer")
}

// argBytes reads a document's content: binary, or a string (the bytes as written).
func argBytes(p []any, i int, name string) ([]byte, error) {
	if i >= len(p) {
		return nil, invalid(name + " is required")
	}
	switch b := p[i].(type) {
	case []byte:
		return b, nil
	case string:
		return []byte(b), nil
	}
	return nil, invalid(name + " must be binary or a string")
}

func strList(v any, name string) ([]string, error) {
	l, ok := v.([]any)
	if !ok {
		return nil, invalid(name + " must be a list of strings")
	}
	out := make([]string, len(l))
	for i, e := range l {
		s, ok := e.(string)
		if !ok {
			return nil, invalid(name + " must be a list of strings")
		}
		out[i] = s
	}
	return out, nil
}

// strs is a []string in the encoder's vocabulary.
func strs(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

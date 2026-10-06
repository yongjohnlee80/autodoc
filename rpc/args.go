package rpc

import (
	"context"
	"fmt"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/index"
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

// protocolKey carries a request's session protocol to its handler.
type protocolKey struct{}

func withProtocol(ctx context.Context, p int64) context.Context {
	return context.WithValue(ctx, protocolKey{}, p)
}

// protocolOf is the protocol the request's session declared; 0 outside a session.
func protocolOf(ctx context.Context) int64 {
	p, _ := ctx.Value(protocolKey{}).(int64)
	return p
}

// reclaimSince is the protocol from which embedding.cancel_switch and embedding.remove delete
// vectors (ADR 1791284787 §2.7): the partial target's, and the removed provider's model's. A session
// below it knew them to keep the vectors, which cannot be kept for it (the start sweep would delete
// them), so it is refused the two verbs.
const reclaimSince = 16

// needReclaim refuses verb to a session below reclaimSince.
func needReclaim(ctx context.Context, verb string) error {
	if protocolOf(ctx) < reclaimSince {
		return invalid(fmt.Sprintf("%s deletes vectors since protocol %d", verb, reclaimSince))
	}
	return nil
}

// relationsSince is the protocol frontmatter relation links arrived in. A session below it sees the
// body links alone: a new kind of link changes what a graph answer means.
const relationsSince = 14

// graphKinds is the kinds of link a graph verb answers with: the body kinds below relationsSince,
// otherwise every kind, or those the optional {kinds} at p[i] names. Asking for kinds below
// relationsSince is refused, not ignored, so an old client never takes an answer it did not ask for.
func graphKinds(ctx context.Context, p []any, i int) ([]string, error) {
	below := protocolOf(ctx) < relationsSince
	if i < len(p) {
		opts, ok := p[i].(map[string]any)
		if !ok {
			return nil, invalid("options must be a map")
		}
		if raw, ok := opts["kinds"]; ok {
			if below {
				return nil, invalid(fmt.Sprintf("kinds needs protocol %d", relationsSince))
			}
			list, ok := raw.([]any)
			if !ok {
				return nil, invalid("kinds must be a list of strings")
			}
			kinds := make([]string, 0, len(list))
			for _, k := range list {
				s, ok := k.(string)
				if !ok {
					return nil, invalid("kinds must be a list of strings")
				}
				kinds = append(kinds, s)
			}
			return kinds, nil
		}
	}
	if below {
		return index.BodyKinds, nil
	}
	return nil, nil
}

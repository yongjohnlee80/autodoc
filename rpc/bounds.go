package rpc

import (
	"context"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/fsbounds"
)

// BoundsProtocol is the protocol a gated script's bounds arrive in (ADR 1792004705 §4.2a.1): one
// parameter past every verb's own. A session below it sends none.
const BoundsProtocol int64 = 34

// errBounds is a bounds parameter that is not one.
var errBounds = invalid("the parameter past the verb's own is a gated script's bounds: {within, protect?}")

// takeBounds takes a gated script's bounds off params, a call of a verb that declares n params: the
// TUI's gate pads a gated call to n and adds its bounds as one more. A call with no more than n is
// the user's, unbounded; anything else past n is left for the arity check to refuse.
func takeBounds(params []any, n int, proto int64) ([]any, *fsbounds.Bounds, error) {
	if proto < BoundsProtocol || len(params) != n+1 {
		return params, nil, nil
	}
	m, ok := params[n].(map[string]any)
	if !ok {
		return params, nil, nil
	}
	within, ok := m["within"].(string)
	if !ok || within == "" {
		return nil, nil, errBounds
	}
	b := &fsbounds.Bounds{Within: within}
	if p, ok := m["protect"]; ok {
		list, ok := p.([]any)
		if !ok {
			return nil, nil, errBounds
		}
		for _, v := range list {
			s, ok := v.(string)
			if !ok {
				return nil, nil, errBounds
			}
			b.Protect = append(b.Protect, s)
		}
	}
	return params[:n], b, nil
}

// bounded is h with a gated script's bounds taken off its params and put on its ctx, where every
// workspace's files (fsbounds) and file.* read them.
func bounded(h golibrpc.Handler, n int) golibrpc.Handler {
	return func(ctx context.Context, req *golibrpc.Request) (any, error) {
		params, b, err := takeBounds(req.Params, n, sessionProtocol(req.Session))
		if err != nil {
			return nil, err
		}
		if b != nil {
			r := *req
			r.Params = params
			return h(fsbounds.With(ctx, *b), &r)
		}
		return h(ctx, req)
	}
}

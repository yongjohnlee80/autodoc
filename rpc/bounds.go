package rpc

import (
	"context"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/fsbounds"
	"github.com/yongjohnlee80/autodoc/core/schema"
)

// BoundsProtocol is the protocol a gated script's bounds arrive in
// (docs/decisions/contract.md#a-gated-scripts-files-stay-in-the-workspace): one parameter past
// every verb's own. A session below it sends none.
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
// workspace's files (fsbounds) and file.* read them. A Root verb (one that adds a root or moves
// where files are written) is refused to a bounded call: it would move the bounds themselves.
func bounded(h golibrpc.Handler, n int, verb string, root bool) golibrpc.Handler {
	return func(ctx context.Context, req *golibrpc.Request) (any, error) {
		params, b, err := takeBounds(req.Params, n, sessionProtocol(req.Session))
		if err != nil {
			return nil, err
		}
		if b != nil && root {
			return nil, &golibrpc.Error{Code: golibrpc.CodeAccessDenied,
				Message: verb + " adds a root or moves where files are written: " + fsbounds.ErrRefused.Error()}
		}
		if b != nil {
			r := *req
			r.Params = params
			return h(fsbounds.With(ctx, *b), &r)
		}
		return h(ctx, req)
	}
}

// boundedSchema refuses a gated script's schema path (workspace.set_schema, workspace.configure's
// schema) that a bounded read of the workspace would not reach: the daemon reads that file itself,
// for its fields and its errors, so it is held to what the script could read. "" (no schema) and
// the user's call are not checked.
func (s *Server) boundedSchema(ctx context.Context, ws, path string) error {
	b, ok := fsbounds.From(ctx)
	if !ok || path == "" {
		return nil
	}
	w, found := s.workspaces.Get(ws)
	if !found {
		return errNoSuchWorkspace
	}
	// the schema holder's own rule ("~/" the home folder), so the file checked is the file it loads
	abs := schema.ResolvePath(w.Root(), path)
	root, rel, err := s.guard.Locate(abs)
	if err != nil {
		return err
	}
	if root.Name != ws {
		return fsbounds.Outside(path)
	}
	return s.guard.CheckRead(root, rel, b)
}

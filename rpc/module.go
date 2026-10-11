package rpc

import (
	"context"
	"errors"
	"fmt"

	"github.com/yongjohnlee80/golib/logger"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// verb is what the server keeps per name: the contract, the handler the gate calls, and the
// expander of the events a success logs when its EventSpec cannot say them.
type verb struct {
	module.Contract
	h      golibrpc.Handler
	events func(ctx context.Context, params []any, result any) []module.Event
}

// module is v as core/module sees it, for its events.
func (v verb) module() module.Verb { return module.Verb{Contract: v.Contract, Events: v.events} }

// WithModules serves each module's verbs beside rpc's own. A verb name or an error code registered
// twice, a code outside the module's range, and a contract that does not check panic at start.
func WithModules(ms ...module.Module) Option {
	return func(o *options) { o.modules = append(o.modules, ms...) }
}

// registerModules registers every module's verbs and keeps their errors for the catalog.
func (s *Server) registerModules(ms []module.Module) {
	owners := map[int64]int{} // code → the module that answers it
	for i, m := range ms {
		if err := module.CheckCodes(m); err != nil {
			panic("rpc: " + err.Error())
		}
		errs := m.Errors()
		for _, e := range errs {
			if module.Shared(e.Code) {
				continue // the daemon's shared codes mean the same whichever module answers them
			}
			if j, ok := owners[e.Code]; ok && j != i {
				panic(fmt.Sprintf("rpc: error code %d is registered by two modules: %s and %s", e.Code, ms[j].Name(), m.Name()))
			}
			owners[e.Code] = i
		}
		s.moduleErrs = append(s.moduleErrs, errs...)
		for _, v := range m.Verbs() {
			s.add(verb{Contract: v.Contract, h: s.serve(v, errs), events: v.Events})
		}
	}
}

// own registers rpc's own typed verbs: served as a module's are, their errors mapped by publicErrs.
func (s *Server) own(vs ...module.Verb) {
	for _, v := range vs {
		s.add(verb{Contract: v.Contract, h: s.serve(v, nil), events: v.Events})
	}
}

type workspaceKey struct{}

// workspaceOf is the workspace a ScopeWorkspace verb of rpc's named, as serve resolved it.
func workspaceOf(ctx context.Context) Workspace {
	w, _ := ctx.Value(workspaceKey{}).(Workspace)
	return w
}

// verb wraps a handler: it checks the parameter count (lo to hi), resolves the workspace named
// first when ws is set, and maps the errors.
func (s *Server) verb(lo, hi int, h func(context.Context, Workspace, []any) (any, error), ws bool) golibrpc.Handler {
	return func(ctx context.Context, req *golibrpc.Request) (any, error) {
		if err := argsBetween(req.Params, lo, hi); err != nil {
			return nil, err
		}
		var w Workspace
		if ws {
			var err error
			if w, err = s.workspace(req.Params); err != nil {
				return nil, wireErr(err)
			}
		}
		out, err := h(withProtocol(ctx, sessionProtocol(req.Session)), w, req.Params)
		return out, wireErr(err)
	}
}

// serve is a module verb's handler, on the wrappers rpc's own verbs use: verb checks the arity the
// contract gives, resolves a ScopeWorkspace verb's workspace and maps what the module leaves
// unmapped; local refuses a ScopeLocalPeer verb over TCP. The handler's ctx carries the session's
// protocol and client token too. The result is the handler's own: chain carries it with
// module.Wire once its events are logged.
func (s *Server) serve(v module.Verb, errs []module.PublicError) golibrpc.Handler {
	call := func(ctx context.Context, w Workspace, p []any) (any, error) {
		ctx = module.WithProtocol(ctx, protocolOf(ctx))
		if w != nil {
			ctx = module.WithWorkspace(context.WithValue(ctx, workspaceKey{}, w), module.Workspace{Name: w.Name(), Root: w.Root()})
		}
		out, err := v.Call(ctx, p)
		return out, moduleErr(err, errs)
	}
	lo, hi := v.Required(), len(v.Params)
	h := s.verb(lo, hi, call, v.Scope == module.ScopeWorkspace)
	if v.Scope == module.ScopeLocalPeer {
		h = s.local(v.Verb, lo, hi, call)
	}
	return func(ctx context.Context, req *golibrpc.Request) (any, error) {
		ctx = context.WithValue(ctx, sessionKey{}, req.Session)
		return h(module.WithClient(ctx, clientOf(req.Session)), req)
	}
}

type sessionKey struct{}

// sessionOf is the session a verb of rpc's was called in: sys.hello opens it.
func sessionOf(ctx context.Context) *golibrpc.Session {
	sess, _ := ctx.Value(sessionKey{}).(*golibrpc.Session)
	return sess
}

// moduleErr is a module verb's error as the client sees it: params that do not fit its contract,
// then the module's own errors (first match wins); anything else is left to wireErr (a store's or
// a filesystem's error).
func moduleErr(err error, errs []module.PublicError) error {
	if err == nil {
		return nil
	}
	var pe *module.ParamError
	if errors.As(err, &pe) {
		return invalid(pe.Error())
	}
	for _, e := range errs {
		if errors.Is(err, e.Err) {
			return &golibrpc.Error{Code: e.Code, Message: e.Message}
		}
	}
	return err
}

// wired carries a verb's result as the wire does: a struct by its json tags, a hand-built map as it
// is. A result it cannot carry is the handler's bug: logged, and an internal error to the client.
func (s *Server) wired(h golibrpc.Handler) golibrpc.Handler {
	return func(ctx context.Context, req *golibrpc.Request) (any, error) {
		out, err := h(ctx, req)
		if err != nil {
			return nil, err
		}
		w, err := module.Wire(out)
		if err != nil {
			logger.Warning(s.log, err, req.Method+"'s result")
			return nil, err
		}
		return w, nil
	}
}

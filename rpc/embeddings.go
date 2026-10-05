package rpc

import (
	"context"
	"errors"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE EMBEDDING PROVIDERS — the store's providers semantic search can use, the one in use, what
// each offers, and each one's usage and recent calls. A key goes in (embedding.add, update) and
// never comes out: a provider answers only whether it has one.

// Embeddings are the daemon's embedding providers. A server given none answers the embedding
// verbs that it cannot.
type Embeddings interface {
	Providers(ctx context.Context) (providers []store.ProviderInfo, active, lastErr string, err error)
	AddProvider(ctx context.Context, sp store.ProviderSpec) (store.ProviderInfo, error)
	UpdateProvider(ctx context.Context, name string, sp store.ProviderSpec) error
	RemoveProvider(ctx context.Context, name string) error
	Use(ctx context.Context, name string) error
	Models(ctx context.Context, name string, sp store.ProviderSpec) ([]string, error)
	Usage(ctx context.Context, name string, days int) ([]store.Usage, error)
	Log(ctx context.Context, name string, limit int) ([]store.LogEntry, error)
	CancelSwitch(ctx context.Context) (model string, err error)
}

// WithEmbeddings serves the daemon's embedding providers.
func WithEmbeddings(e Embeddings) Option { return func(o *options) { o.embeddings = e } }

// ErrNoSwitch is embedding.cancel_switch with no model filling to replace the active one.
var ErrNoSwitch = errors.New("rpc: no model switch is under way")

// errNoEmbeddings answers the embedding verbs of a server given no Embeddings.
var errNoEmbeddings = errors.New("rpc: this server keeps no embedding providers")

func providerMap(p store.ProviderInfo) map[string]any {
	return map[string]any{"name": p.Name, "kind": p.Kind, "base_url": p.BaseURL, "model": p.Model, "context": int64(p.Context), "has_key": p.HasKey}
}

// specOf reads a provider as a client writes it: name, kind, base_url and model; context, the
// context window in tokens, which absent is the default; and key, which absent keeps the key the
// store holds, and "" removes it.
func specOf(v any) (store.ProviderSpec, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return store.ProviderSpec{}, invalid("a provider must be a map")
	}
	var sp store.ProviderSpec
	for k, dst := range map[string]*string{"name": &sp.Name, "kind": &sp.Kind, "base_url": &sp.BaseURL, "model": &sp.Model} {
		if x, ok := m[k]; ok {
			s, ok := x.(string)
			if !ok {
				return sp, invalid(k + " must be a string")
			}
			*dst = s
		}
	}
	if _, ok := m["context"]; ok {
		n, err := argInt([]any{m["context"]}, 0, "context")
		if err != nil {
			return sp, err
		}
		if n < 0 || n > store.MaxContext { // the store checks the range; this keeps int() exact
			return sp, store.ErrContextRange
		}
		sp.Context = int(n)
	}
	if x, ok := m["key"]; ok {
		s, ok := x.(string)
		if !ok {
			return sp, invalid("key must be a string")
		}
		sp.Key = &s
	}
	return sp, nil
}

func (s *Server) registerEmbeddings() {
	need := func() error {
		if s.embeddings == nil {
			return errNoEmbeddings
		}
		return nil
	}
	s.handle("embedding.providers", s.verb(0, 0, func(ctx context.Context, _ *Workspace, _ []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		ps, active, lastErr, err := s.embeddings.Providers(ctx)
		if err != nil {
			return nil, err
		}
		list := []any{}
		for _, p := range ps {
			list = append(list, providerMap(p))
		}
		return map[string]any{"providers": list, "active": active, "error": lastErr}, nil
	}, false))
	s.handle("embedding.add", s.verb(1, 1, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		sp, err := specOf(p[0])
		if err != nil {
			return nil, err
		}
		info, err := s.embeddings.AddProvider(ctx, sp)
		if err != nil {
			return nil, err
		}
		return providerMap(info), nil
	}, false))
	s.handle("embedding.update", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		sp, err := specOf(p[1])
		if err != nil {
			return nil, err
		}
		return nil, s.embeddings.UpdateProvider(ctx, name, sp)
	}, false))
	s.handle("embedding.remove", s.verb(1, 1, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		return nil, s.embeddings.RemoveProvider(ctx, name)
	}, false))
	s.handle("embedding.use", s.verb(1, 1, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		return nil, s.embeddings.Use(ctx, name)
	}, false))
	// the model switch under way ends: the provider in use goes back to the model still active
	s.handle("embedding.cancel_switch", s.verb(0, 0, func(ctx context.Context, _ *Workspace, _ []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		model, err := s.embeddings.CancelSwitch(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"model": model}, nil
	}, false))
	// a stored provider's models by its name, or an unsaved one's by its kind, base_url and key
	s.handle("embedding.models", s.verb(1, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		// a stored one by its name; an unsaved one by its spec; an edit of a stored one by its name
		// and the edit's spec, which is what is asked, with the stored key unless it carries one
		var name string
		var sp store.ProviderSpec
		switch v := p[0].(type) {
		case string:
			name = v
			if len(p) == 2 {
				var err error
				if sp, err = specOf(p[1]); err != nil {
					return nil, err
				}
			}
		default:
			if len(p) == 2 {
				return nil, invalid("embedding.models: a spec alone, or a name and a spec")
			}
			var err error
			if sp, err = specOf(v); err != nil {
				return nil, err
			}
		}
		models, err := s.embeddings.Models(ctx, name, sp)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, m := range models {
			out = append(out, m)
		}
		return out, nil
	}, false))
	s.handle("embedding.model_context", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		info, ok := s.embeddings.(interface {
			ModelContext(context.Context, string, store.ProviderSpec) (int, error)
		})
		if !ok {
			return nil, errNoEmbeddings
		}
		stored, err := argStr(p, 0, "stored provider")
		if err != nil {
			return nil, err
		}
		sp, err := specOf(p[1])
		if err != nil {
			return nil, err
		}
		maximum, err := info.ModelContext(ctx, stored, sp)
		if err != nil {
			return nil, err
		}
		return map[string]any{"maximum": int64(maximum)}, nil
	}, false))
	s.handle("embedding.usage", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		days, err := argInt(p, 1, "days")
		if err != nil {
			return nil, err
		}
		us, err := s.embeddings.Usage(ctx, name, int(days))
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, u := range us {
			out = append(out, map[string]any{"day": u.Day, "requests": u.Requests, "texts": u.Texts, "tokens": u.Tokens,
				"failures": u.Failures, "limited": u.Limited})
		}
		return out, nil
	}, false))
	s.handle("embedding.log", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		limit, err := argInt(p, 1, "limit")
		if err != nil {
			return nil, err
		}
		es, err := s.embeddings.Log(ctx, name, int(limit))
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, e := range es {
			out = append(out, map[string]any{"at": e.At, "texts": e.Texts, "tokens": e.Tokens, "millis": e.Millis, "outcome": e.Outcome})
		}
		return out, nil
	}, false))
}

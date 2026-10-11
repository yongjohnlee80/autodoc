package rpc

import (
	"context"
	"errors"

	"github.com/yongjohnlee80/autodoc/core/module"
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

// modelContexter is Embeddings that reads a model's context window (the daemon's), read at New.
type modelContexter interface {
	ModelContext(ctx context.Context, stored string, sp store.ProviderSpec) (int, error)
}

type providerJSON struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	Context int    `json:"context"`
	HasKey  bool   `json:"has_key"`
}

func providerOf(p store.ProviderInfo) *providerJSON {
	return &providerJSON{Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, Model: p.Model, Context: p.Context, HasKey: p.HasKey}
}

type providersJSON struct {
	Providers []*providerJSON `json:"providers"`
	Active    string          `json:"active"`
	Error     string          `json:"error"`
}

type (
	providerName struct {
		Name string `param:"name"`
	}
	providerAdd struct {
		Provider optionMap `param:"provider" doc:"{name, kind, base_url, model, context?, key?}: an absent key keeps the stored one, \"\" removes it"`
	}
	providerUpdate struct {
		Name     string    `param:"name"`
		Provider optionMap `param:"provider" doc:"{name, kind, base_url, model, context?, key?}: an absent key keeps the stored one, \"\" removes it"`
	}
	providerModels struct {
		Provider any       `param:"provider" doc:"a stored provider's name, or an unsaved one's spec"`
		Spec     optionMap `param:"spec,optional" doc:"with a name: the edit asked about"`
	}
	providerContext struct {
		Stored   string    `param:"stored" doc:"the stored provider; \"\" for an unsaved one"`
		Provider optionMap `param:"provider" doc:"{name, kind, base_url, model, context?, key?}"`
	}
	meterParams struct {
		Name string `param:"name"`
		Days int    `param:"days"`
	}
	callsParams struct {
		Name  string `param:"name"`
		Limit int    `param:"limit"`
	}
)

// usageJSON and callJSON are a provider's or a ranker's usage by day and recent calls.
type usageJSON struct {
	Day      string `json:"day"`
	Requests int64  `json:"requests"`
	Texts    int64  `json:"texts"`
	Tokens   int64  `json:"tokens"`
	Failures int64  `json:"failures"`
	Limited  int64  `json:"limited"`
}

type callJSON struct {
	At      int64  `json:"at"`
	Texts   int64  `json:"texts"`
	Tokens  int64  `json:"tokens"`
	Millis  int64  `json:"millis"`
	Outcome string `json:"outcome"`
}

// meters are the usage, log and models verbs an embedding provider and a ranker share: need says
// whether the server keeps them, and specOf reads a spec of S.
type meters[S any] struct {
	need   func() error
	specOf func(any) (S, error)
	usage  func(ctx context.Context, name string, days int) ([]store.Usage, error)
	log    func(ctx context.Context, name string, limit int) ([]store.LogEntry, error)
	models func(ctx context.Context, name string, sp S) ([]string, error)
}

func (m meters[S]) usageVerb(c module.Contract) module.Verb {
	return module.Handle(c, func(ctx context.Context, p meterParams) ([]usageJSON, error) {
		if err := m.need(); err != nil {
			return nil, err
		}
		us, err := m.usage(ctx, p.Name, p.Days)
		if err != nil {
			return nil, err
		}
		out := make([]usageJSON, len(us))
		for i, u := range us {
			out[i] = usageJSON{Day: u.Day, Requests: u.Requests, Texts: u.Texts, Tokens: u.Tokens, Failures: u.Failures, Limited: u.Limited}
		}
		return out, nil
	})
}

func (m meters[S]) logVerb(c module.Contract) module.Verb {
	return module.Handle(c, func(ctx context.Context, p callsParams) ([]callJSON, error) {
		if err := m.need(); err != nil {
			return nil, err
		}
		es, err := m.log(ctx, p.Name, p.Limit)
		if err != nil {
			return nil, err
		}
		out := make([]callJSON, len(es))
		for i, e := range es {
			out[i] = callJSON{At: e.At, Texts: e.Texts, Tokens: e.Tokens, Millis: e.Millis, Outcome: e.Outcome}
		}
		return out, nil
	})
}

// modelsOf lists the models of a stored one by its name, an unsaved one by its spec, or an edit of a
// stored one by its name and the edit's spec, which is what is asked, with the stored key unless it
// carries one.
func (m meters[S]) modelsOf(ctx context.Context, verb string, first any, spec optionMap) ([]string, error) {
	if err := m.need(); err != nil {
		return nil, err
	}
	var name string
	var sp S
	var err error
	switch v := first.(type) {
	case string:
		name = v
		if spec.raw != nil {
			if sp, err = m.specOf(spec.raw); err != nil {
				return nil, err
			}
		}
	default:
		if spec.raw != nil {
			return nil, invalid(verb + ": a spec alone, or a name and a spec")
		}
		if sp, err = m.specOf(v); err != nil {
			return nil, err
		}
	}
	return m.models(ctx, name, sp)
}

func (s *Server) embeddingVerbs() []module.Verb {
	need := func() error {
		if s.embeddings == nil {
			return errNoEmbeddings
		}
		return nil
	}
	m := meters[store.ProviderSpec]{need: need, specOf: specOf,
		usage: func(ctx context.Context, n string, d int) ([]store.Usage, error) {
			return s.embeddings.Usage(ctx, n, d)
		},
		log: func(ctx context.Context, n string, l int) ([]store.LogEntry, error) {
			return s.embeddings.Log(ctx, n, l)
		},
		models: func(ctx context.Context, n string, sp store.ProviderSpec) ([]string, error) {
			return s.embeddings.Models(ctx, n, sp)
		}}
	user := module.AudienceUser
	ev := func(kind, detail string) *module.EventSpec { return &module.EventSpec{Kind: kind, Detail: detail} }
	return []module.Verb{
		module.Handle(module.Contract{Verb: "embedding.providers", Since: 3, Audience: user,
			Result: "{providers: [{name, kind, base_url, model, context, has_key}], active, error}", Errors: []int64{CodeUnsupported},
			Doc: "The stored embedding providers, the one in use, and why the one chosen is not in use."},
			func(ctx context.Context, _ struct{}) (*providersJSON, error) {
				if err := need(); err != nil {
					return nil, err
				}
				ps, active, lastErr, err := s.embeddings.Providers(ctx)
				if err != nil {
					return nil, err
				}
				out := &providersJSON{Providers: []*providerJSON{}, Active: active, Error: lastErr}
				for _, p := range ps {
					out.Providers = append(out.Providers, providerOf(p))
				}
				return out, nil
			}),
		module.Handle(module.Contract{Verb: "embedding.add", Since: 3, Audience: user,
			Result: "{name, kind, base_url, model, context, has_key}", Errors: []int64{CodeUnsupported, CodeConflict, CodeProviderRefused},
			Event: ev("embedding.providers", "")},
			func(ctx context.Context, p providerAdd) (*providerJSON, error) {
				if err := need(); err != nil {
					return nil, err
				}
				sp, err := specOf(p.Provider.raw)
				if err != nil {
					return nil, err
				}
				info, err := s.embeddings.AddProvider(ctx, sp)
				if err != nil {
					return nil, err
				}
				return providerOf(info), nil
			}),
		module.Handle(module.Contract{Verb: "embedding.update", Since: 3, Audience: user,
			Errors: []int64{CodeUnsupported, CodeNotFound, CodeConflict, CodeProviderRefused}, Event: ev("embedding.providers", "name")},
			func(ctx context.Context, p providerUpdate) (any, error) {
				if err := need(); err != nil {
					return nil, err
				}
				sp, err := specOf(p.Provider.raw)
				if err != nil {
					return nil, err
				}
				return nil, s.embeddings.UpdateProvider(ctx, p.Name, sp)
			}),
		module.Handle(module.Contract{Verb: "embedding.remove", Since: 3, Audience: user,
			Errors: []int64{CodeUnsupported, CodeNotFound}, Event: ev("embedding.providers", "name"),
			Doc: "Deletes the provider and its model's vectors (refused below protocol 16)."},
			func(ctx context.Context, p providerName) (any, error) {
				if err := need(); err != nil {
					return nil, err
				}
				if err := needReclaim(ctx, "embedding.remove"); err != nil {
					return nil, err
				}
				return nil, s.embeddings.RemoveProvider(ctx, p.Name)
			}),
		module.Handle(module.Contract{Verb: "embedding.use", Since: 3, Audience: user,
			Errors: []int64{CodeUnsupported, CodeNotFound, CodeProviderRefused}, Event: ev("embedding.switched", "name"),
			Doc: "Switches the daemon's provider; the new model fills while the old one answers."},
			func(ctx context.Context, p providerName) (any, error) {
				if err := need(); err != nil {
					return nil, err
				}
				return nil, s.embeddings.Use(ctx, p.Name)
			}),
		// the model switch under way ends: the provider in use goes back to the model still active
		module.Handle(module.Contract{Verb: "embedding.cancel_switch", Since: 4, Audience: user,
			Result: "{model}", Errors: []int64{CodeUnsupported}, Event: ev("embedding.cancelled", ""),
			Doc: "Ends the model switch under way and deletes the partial target's vectors (refused below protocol 16)."},
			func(ctx context.Context, _ struct{}) (map[string]string, error) {
				if err := need(); err != nil {
					return nil, err
				}
				if err := needReclaim(ctx, "embedding.cancel_switch"); err != nil {
					return nil, err
				}
				model, err := s.embeddings.CancelSwitch(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]string{"model": model}, nil
			}),
		module.Handle(module.Contract{Verb: "embedding.models", Since: 3, Audience: user,
			Result: "[string]", Errors: []int64{CodeUnsupported, CodeNotFound, CodeProviderRefused}, Doc: "The models a provider offers."},
			func(ctx context.Context, p providerModels) ([]string, error) {
				return m.modelsOf(ctx, "embedding.models", p.Provider, p.Spec)
			}),
		module.Handle(module.Contract{Verb: "embedding.model_context", Since: 5, Audience: user,
			Result: "{maximum}", Errors: []int64{CodeUnsupported, CodeNotFound, CodeProviderRefused},
			Doc: "The context window the provider's model reports, in tokens."},
			func(ctx context.Context, p providerContext) (map[string]int, error) {
				if err := need(); err != nil {
					return nil, err
				}
				if s.modelContext == nil {
					return nil, errNoEmbeddings
				}
				sp, err := specOf(p.Provider.raw)
				if err != nil {
					return nil, err
				}
				maximum, err := s.modelContext.ModelContext(ctx, p.Stored, sp)
				if err != nil {
					return nil, err
				}
				return map[string]int{"maximum": maximum}, nil
			}),
		m.usageVerb(module.Contract{Verb: "embedding.usage", Since: 3, Audience: user,
			Result: "[{day, requests, texts, tokens, failures, limited}]", Errors: []int64{CodeUnsupported, CodeNotFound},
			Doc: "A provider's usage by day."}),
		m.logVerb(module.Contract{Verb: "embedding.log", Since: 3, Audience: user,
			Result: "[{at, texts, tokens, millis, outcome}]", Errors: []int64{CodeUnsupported, CodeNotFound},
			Doc: "A provider's recent calls."}),
	}
}

func modelContextOf(e Embeddings) modelContexter {
	m, _ := e.(modelContexter)
	return m
}

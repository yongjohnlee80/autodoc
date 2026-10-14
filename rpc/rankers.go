package rpc

import (
	"context"
	"errors"
	"fmt"

	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE RANKER MODELS — the store's rankers search's second stage can use (ADR 0215), the one in use
// and the window it ranks, what each serves, and each one's usage and recent calls. A key goes in
// (ranker.add, update) and never comes out. A build that supplies its own ranker refuses changes to
// the one in use, and to the stored selection kept for a build that does not.

// RankerList is ranker.list's answer: the stored rankers, the one in use, its window, why the one
// named is not in use, and the build's own ranker's model when it supplies one.
type RankerList struct {
	Rankers  []store.RankerInfo
	Active   string
	Window   int
	Err      string
	Supplied string
}

// Rankers are the daemon's rankers. A server given none answers the ranker verbs that it cannot.
type Rankers interface {
	ListRankers(ctx context.Context) (RankerList, error)
	AddRanker(ctx context.Context, sp store.RankerSpec) (store.RankerInfo, error)
	UpdateRanker(ctx context.Context, name string, sp store.RankerSpec) error
	RemoveRanker(ctx context.Context, name string) error
	Use(ctx context.Context, name string) error
	SetWindow(ctx context.Context, n int) error
	Models(ctx context.Context, name string, sp store.RankerSpec) ([]string, error)
	Usage(ctx context.Context, name string, days int) ([]store.Usage, error)
	Log(ctx context.Context, name string, limit int) ([]store.LogEntry, error)
	// Supplied is the build's own ranker's model, and whether the build supplies one.
	Supplied() (string, bool)
}

// WithRankers serves the daemon's rankers.
func WithRankers(r Rankers) Option { return func(o *options) { o.rankers = r } }

// errNoRankers answers the ranker verbs of a server given no Rankers.
var errNoRankers = errors.New("rpc: this server keeps no rankers")

// ErrSuppliedRanker is a change refused while the build supplies its ranker: the one in use, the
// window, or the stored selection kept dormant for a build that does not.
var ErrSuppliedRanker = errors.New("this build supplies its ranker")

// ErrWindow is a window outside the ranker's bounds.
var ErrWindow = fmt.Errorf("a ranker's window is %d to %d candidates", rank.MinWindow, rank.MaxWindow)

type rankerJSON struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	HasKey  bool   `json:"has_key"`
}

func rankerOf(r store.RankerInfo) *rankerJSON {
	return &rankerJSON{Name: r.Name, Kind: r.Kind, BaseURL: r.BaseURL, Model: r.Model, HasKey: r.HasKey}
}

type rankersJSON struct {
	Rankers  []*rankerJSON `json:"rankers"`
	Active   string        `json:"active"`
	Window   int           `json:"window"`
	Error    string        `json:"error"`
	Supplied string        `json:"supplied"`
}

type (
	rankerAdd struct {
		Ranker optionMap `param:"ranker" doc:"{name, kind, base_url, model, key?}: an absent key keeps the stored one, \"\" removes it"`
	}
	rankerUpdate struct {
		Name   string    `param:"name"`
		Ranker optionMap `param:"ranker" doc:"{name, kind, base_url, model, key?}: an absent key keeps the stored one, \"\" removes it"`
	}
	rankerUse struct {
		Name string `param:"name" doc:"\"\" for none"`
	}
	rankerWindow struct {
		Window int `param:"window" doc:"how many candidates it ranks, 10 to 100"`
	}
	rankerModels struct {
		Ranker any       `param:"ranker" doc:"a stored ranker's name, or an unsaved one's spec"`
		Spec   optionMap `param:"spec,optional" doc:"with a name: the edit asked about"`
	}
)

// rankerSpecOf reads a ranker as a client writes it: name, kind, base_url and model; and key,
// which absent keeps the key the store holds, and "" removes it.
func rankerSpecOf(v any) (store.RankerSpec, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return store.RankerSpec{}, invalid("a ranker must be a map")
	}
	var sp store.RankerSpec
	for k, dst := range map[string]*string{"name": &sp.Name, "kind": &sp.Kind, "base_url": &sp.BaseURL, "model": &sp.Model} {
		if x, ok := m[k]; ok {
			s, ok := x.(string)
			if !ok {
				return sp, invalid(k + " must be a string")
			}
			*dst = s
		}
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

func (s *Server) rankerVerbs() []module.Verb {
	need := func() error {
		if s.rankers == nil {
			return errNoRankers
		}
		return nil
	}
	m := meters[store.RankerSpec]{need: need, specOf: rankerSpecOf,
		usage: func(ctx context.Context, n string, d int) ([]store.Usage, error) { return s.rankers.Usage(ctx, n, d) },
		log:   func(ctx context.Context, n string, l int) ([]store.LogEntry, error) { return s.rankers.Log(ctx, n, l) },
		models: func(ctx context.Context, n string, sp store.RankerSpec) ([]string, error) {
			return s.rankers.Models(ctx, n, sp)
		}}
	user := module.AudienceUser
	ev := func(kind, detail string) *module.EventSpec { return &module.EventSpec{Kind: kind, Detail: detail} }
	return []module.Verb{
		module.Handle(module.Contract{Verb: "ranker.list", Effect: module.EffectRead, Since: 11, Audience: user,
			Result: "{rankers: [{name, kind, base_url, model, has_key}], active, window, error, supplied}", Errors: []int64{CodeUnsupported},
			Doc: "The stored rankers (kind tei or rerank-api), the one in use, how many of the top candidates it ranks, why the one chosen is not in use, and the model of the build's own ranker (\"\" for none), which is then the one in use."},
			func(ctx context.Context, _ struct{}) (*rankersJSON, error) {
				if err := need(); err != nil {
					return nil, err
				}
				l, err := s.rankers.ListRankers(ctx)
				if err != nil {
					return nil, err
				}
				out := &rankersJSON{Rankers: []*rankerJSON{}, Active: l.Active, Window: l.Window, Error: l.Err, Supplied: l.Supplied}
				for _, r := range l.Rankers {
					out.Rankers = append(out.Rankers, rankerOf(r))
				}
				return out, nil
			}),
		module.Handle(module.Contract{Verb: "ranker.add", Effect: module.EffectSettings, Since: 11, Audience: user,
			Result: "{name, kind, base_url, model, has_key}", Errors: []int64{CodeUnsupported, CodeConflict, CodeProviderRefused},
			Event: ev("ranker.providers", "")},
			func(ctx context.Context, p rankerAdd) (*rankerJSON, error) {
				if err := need(); err != nil {
					return nil, err
				}
				sp, err := rankerSpecOf(p.Ranker.raw)
				if err != nil {
					return nil, err
				}
				info, err := s.rankers.AddRanker(ctx, sp)
				if err != nil {
					return nil, err
				}
				return rankerOf(info), nil
			}),
		module.Handle(module.Contract{Verb: "ranker.update", Effect: module.EffectSettings, Since: 11, Audience: user,
			Errors: []int64{CodeUnsupported, CodeNotFound, CodeConflict, CodeProviderRefused}, Event: ev("ranker.providers", "name")},
			func(ctx context.Context, p rankerUpdate) (any, error) {
				if err := need(); err != nil {
					return nil, err
				}
				sp, err := rankerSpecOf(p.Ranker.raw)
				if err != nil {
					return nil, err
				}
				return nil, s.rankers.UpdateRanker(ctx, p.Name, sp)
			}),
		module.Handle(module.Contract{Verb: "ranker.remove", Effect: module.EffectSettings, Since: 11, Audience: user, Errors: []int64{CodeUnsupported, CodeNotFound},
			Event: ev("ranker.providers", "name")},
			func(ctx context.Context, p providerName) (any, error) {
				if err := need(); err != nil {
					return nil, err
				}
				return nil, s.rankers.RemoveRanker(ctx, p.Name)
			}),
		module.Handle(module.Contract{Verb: "ranker.use", Effect: module.EffectSettings, Since: 11, Audience: user, Errors: []int64{CodeUnsupported, CodeNotFound, CodeProviderRefused},
			Event: ev("ranker.switched", "name"), Doc: "Refused while the build supplies its ranker."},
			func(ctx context.Context, p rankerUse) (any, error) {
				if err := need(); err != nil {
					return nil, err
				}
				return nil, s.rankers.Use(ctx, p.Name)
			}),
		module.Handle(module.Contract{Verb: "ranker.window", Effect: module.EffectSettings, Since: 11, Audience: user, Errors: []int64{CodeUnsupported},
			Event: ev("ranker.switched", ""), Doc: "Refused while the build supplies its ranker."},
			func(ctx context.Context, p rankerWindow) (any, error) {
				if err := need(); err != nil {
					return nil, err
				}
				return nil, s.rankers.SetWindow(ctx, p.Window)
			}),
		// a ranker's models: a TEI server's one model, none for a rerank-API endpoint (its model is typed)
		module.Handle(module.Contract{Verb: "ranker.models", Effect: module.EffectRead, Since: 11, Audience: user,
			Result: "[string]", Errors: []int64{CodeUnsupported, CodeNotFound, CodeProviderRefused},
			Doc: "A TEI server's one model; none for a rerank-API endpoint, whose model is typed."},
			func(ctx context.Context, p rankerModels) ([]string, error) {
				return m.modelsOf(ctx, "ranker.models", p.Ranker, p.Spec)
			}),
		m.usageVerb(module.Contract{Verb: "ranker.usage", Effect: module.EffectRead, Since: 11, Audience: user,
			Result: "[{day, requests, texts, tokens, failures, limited}]", Errors: []int64{CodeUnsupported, CodeNotFound},
			Doc: "A ranker's usage by day."}),
		m.logVerb(module.Contract{Verb: "ranker.log", Effect: module.EffectRead, Since: 11, Audience: user,
			Result: "[{at, texts, tokens, millis, outcome}]", Errors: []int64{CodeUnsupported, CodeNotFound},
			Doc: "A ranker's recent calls."}),
	}
}

// rankerCapability is sys.capabilities' ranker: whether the build supplies its own, and its model.
func (s *Server) rankerCapability() map[string]any {
	if s.rankers == nil {
		return map[string]any{"supplied": false}
	}
	model, ok := s.rankers.Supplied()
	if !ok {
		return map[string]any{"supplied": false}
	}
	return map[string]any{"supplied": true, "model": model}
}

package rpc

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE PREFERENCES — what clients keep between runs, by name: the store's. A preference that a verb
// of its own sets (embedding.use, ranker.use, ranker.window) is not written around that verb.

type preferenceSet struct {
	Name  string `param:"name"`
	Value string `param:"value"`
}

func (s *Server) preferenceVerbs() []module.Verb {
	return []module.Verb{
		module.Handle(module.Contract{Verb: "preference.list", Since: 3, Audience: module.AudienceUser,
			Result: "{name: value}", Errors: []int64{CodeUnsupported}, Doc: "What clients keep between runs."},
			func(ctx context.Context, _ struct{}) (map[string]string, error) {
				if s.preferences == nil {
					return nil, errNoPreferences
				}
				return s.preferences.Preferences(ctx)
			}),
		module.Handle(module.Contract{Verb: "preference.set", Since: 3, Audience: module.AudienceUser,
			Errors: []int64{CodeUnsupported}, Event: &module.EventSpec{Kind: "preference.changed", Detail: "name"},
			Doc: "A preference that has a verb of its own (embedding.use, ranker.use, ranker.window) is refused."},
			func(ctx context.Context, p preferenceSet) (any, error) {
				if s.preferences == nil {
					return nil, errNoPreferences
				}
				if store.Owned(p.Name) {
					return nil, store.ErrOwnedPreference
				}
				return nil, s.preferences.SetPreference(ctx, p.Name, p.Value)
			}),
	}
}

package rpc

import (
	"context"
	"errors"
	"fmt"

	"github.com/yongjohnlee80/golib/search/rank"

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

func rankerMap(r store.RankerInfo) map[string]any {
	return map[string]any{"name": r.Name, "kind": r.Kind, "base_url": r.BaseURL, "model": r.Model, "has_key": r.HasKey}
}

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

func (s *Server) registerRankers() {
	need := func() error {
		if s.rankers == nil {
			return errNoRankers
		}
		return nil
	}
	s.handle("ranker.list", s.verb(0, 0, func(ctx context.Context, _ *Workspace, _ []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		l, err := s.rankers.ListRankers(ctx)
		if err != nil {
			return nil, err
		}
		list := []any{}
		for _, r := range l.Rankers {
			list = append(list, rankerMap(r))
		}
		return map[string]any{"rankers": list, "active": l.Active, "window": int64(l.Window), "error": l.Err,
			"supplied": l.Supplied}, nil
	}, false))
	s.handle("ranker.add", s.verb(1, 1, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		sp, err := rankerSpecOf(p[0])
		if err != nil {
			return nil, err
		}
		info, err := s.rankers.AddRanker(ctx, sp)
		if err != nil {
			return nil, err
		}
		return rankerMap(info), nil
	}, false))
	s.handle("ranker.update", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		sp, err := rankerSpecOf(p[1])
		if err != nil {
			return nil, err
		}
		return nil, s.rankers.UpdateRanker(ctx, name, sp)
	}, false))
	s.handle("ranker.remove", s.verb(1, 1, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		return nil, s.rankers.RemoveRanker(ctx, name)
	}, false))
	s.handle("ranker.use", s.verb(1, 1, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		return nil, s.rankers.Use(ctx, name)
	}, false))
	s.handle("ranker.window", s.verb(1, 1, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		n, err := argInt(p, 0, "window")
		if err != nil {
			return nil, err
		}
		return nil, s.rankers.SetWindow(ctx, int(n))
	}, false))
	// a ranker's models: a TEI server's one model, none for a rerank-API endpoint (its model is typed)
	s.handle("ranker.models", s.verb(1, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if err := need(); err != nil {
			return nil, err
		}
		// a stored one by its name; an unsaved one by its spec; an edit of a stored one by its name
		// and the edit's spec, which is what is asked, with the stored key unless it carries one
		var name string
		var sp store.RankerSpec
		switch v := p[0].(type) {
		case string:
			name = v
			if len(p) == 2 {
				var err error
				if sp, err = rankerSpecOf(p[1]); err != nil {
					return nil, err
				}
			}
		default:
			if len(p) == 2 {
				return nil, invalid("ranker.models: a spec alone, or a name and a spec")
			}
			var err error
			if sp, err = rankerSpecOf(v); err != nil {
				return nil, err
			}
		}
		models, err := s.rankers.Models(ctx, name, sp)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, m := range models {
			out = append(out, m)
		}
		return out, nil
	}, false))
	s.handle("ranker.usage", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
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
		us, err := s.rankers.Usage(ctx, name, int(days))
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
	s.handle("ranker.log", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
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
		es, err := s.rankers.Log(ctx, name, int(limit))
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

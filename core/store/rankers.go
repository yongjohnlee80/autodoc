package store

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
)

// RANKER MODELS — the re-ranking models search's second stage can use (ADR 0215), one of them in
// use for every workspace (the preference ranker.in_use), each with its API key sealed by the
// keyslot, its usage by day and its recent calls, as an embedding provider's.

// The ranker kinds: Text Embeddings Inference, which serves one model, and a Cohere-style /rerank
// endpoint, which names its model.
const (
	KindTEI       = "tei"
	KindRerankAPI = "rerank-api"
)

// The preferences of the ranker in use: its name ("" or absent: none), and the window it ranks.
const (
	PrefRanker       = "ranker.in_use"
	PrefRankerWindow = "ranker.window"
)

// The refusals of the ranker table.
var (
	ErrNoRanker      = errs.Sentinel(errs.ErrInvalidArgument, "store: no such ranker")
	ErrRankerTaken   = errs.Sentinel(errs.ErrPrecondition, "store: another ranker has this name")
	ErrRankerInvalid = errs.Sentinel(errs.ErrInvalidArgument, "store: a ranker needs a name, a kind (tei or rerank-api), a base URL, and a model for rerank-api")
)

// RankerSpec is a ranker as a client writes it. Key nil keeps the key the store holds; a pointer
// to "" removes it. A tei ranker's model is the server's: Model is kept "" for it.
type RankerSpec struct {
	Name, Kind, BaseURL, Model string
	Key                        *string
}

// RankerInfo is a ranker as a client reads it: never its key, only whether it has one.
type RankerInfo struct {
	ID                         int64
	Name, Kind, BaseURL, Model string
	HasKey                     bool
}

func rankerInfoOf(r *Ranker) RankerInfo {
	return RankerInfo{ID: r.ID, Name: r.Name, Kind: r.Kind, BaseURL: r.BaseURL, Model: r.Model, HasKey: len(r.APIKey) > 0}
}

// Check refuses a spec the store would not keep, so the ranker in use is not probed with one.
func (sp RankerSpec) Check() error {
	if strings.TrimSpace(sp.Name) == "" || (sp.Kind != KindTEI && sp.Kind != KindRerankAPI) ||
		(sp.Kind == KindRerankAPI && strings.TrimSpace(sp.Model) == "") {
		return ErrRankerInvalid
	}
	if u, err := url.Parse(sp.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: base URL %q: want an http or https URL", ErrRankerInvalid, sp.BaseURL)
	}
	return nil
}

// model is the spec's model as the row keeps it: "" for tei.
func (sp RankerSpec) model() string {
	if sp.Kind == KindTEI {
		return ""
	}
	return strings.TrimSpace(sp.Model)
}

// rankerColumns sets a ranker's own columns from its spec.
func rankerColumns(sp RankerSpec) meteredSetter[*Ranker, RankerField] {
	return func(d dao.DAO[*Ranker, RankerField, int64]) dao.DAO[*Ranker, RankerField, int64] {
		return d.Set(RankerName, sp.Name).Set(RankerKind, sp.Kind).Set(RankerBaseURL, sp.BaseURL).Set(RankerModel, sp.model())
	}
}

// Rankers are every ranker, by name.
func (s *Store) Rankers(ctx context.Context) ([]RankerInfo, error) {
	rows, err := s.rankers().list(ctx)
	out := make([]RankerInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, rankerInfoOf(r))
	}
	return out, err
}

// AddRanker records a ranker, its key sealed.
func (s *Store) AddRanker(ctx context.Context, sp RankerSpec) (RankerInfo, error) {
	if err := sp.Check(); err != nil {
		return RankerInfo{}, err
	}
	var info RankerInfo
	err := s.Write(ctx, func(tx *Tx) error {
		m := s.rankers()
		id, err := m.add(tx, rankerColumns(sp), sp.Key)
		if err != nil {
			return err
		}
		r, err := m.get(tx, id)
		info = rankerInfoOf(r)
		return err
	})
	return info, err
}

// UpdateRanker rewrites the ranker named name as sp: sp.Name renames it, and the preference naming
// it follows.
func (s *Store) UpdateRanker(ctx context.Context, name string, sp RankerSpec) error {
	if err := sp.Check(); err != nil {
		return err
	}
	return s.Write(ctx, func(tx *Tx) error {
		_, err := s.rankers().update(tx, name, sp.Name, rankerColumns(sp), sp.Key)
		return err
	})
}

// RemoveRanker deletes a ranker, and with it its usage and its log; a preference naming it names
// none.
func (s *Store) RemoveRanker(ctx context.Context, name string) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := s.rankers().remove(tx, name)
		return err
	})
}

// RankerWithKey is the ranker named name and its key, opened: for the daemon, which ranks with it,
// and never for a client.
func (s *Store) RankerWithKey(ctx context.Context, name string) (RankerInfo, string, error) {
	r, key, err := s.rankers().withKey(ctx, name)
	if err != nil {
		return RankerInfo{}, "", err
	}
	return rankerInfoOf(r), key, nil
}

// RecordRankerCalls adds calls to a ranker's usage by day and to its log, which keeps the last 200.
func (s *Store) RecordRankerCalls(ctx context.Context, rankerID int64, calls []CallRecord) error {
	return s.rankers().record(ctx, rankerID, calls)
}

// RankerUsage is the ranker's use by day, the latest first, at most days of them.
func (s *Store) RankerUsage(ctx context.Context, name string, days int) ([]Usage, error) {
	return s.rankers().usage(ctx, name, days)
}

// RankerLog is the ranker's recent calls, the latest first, at most limit of them.
func (s *Store) RankerLog(ctx context.Context, name string, limit int) ([]LogEntry, error) {
	return s.rankers().log(ctx, name, limit)
}

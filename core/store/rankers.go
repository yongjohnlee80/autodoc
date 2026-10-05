package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/dao"
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
	ErrNoRanker      = errors.New("store: no such ranker")
	ErrRankerTaken   = errors.New("store: another ranker has this name")
	ErrRankerInvalid = errors.New("store: a ranker needs a name, a kind (tei or rerank-api), a base URL, and a model for rerank-api")
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

// rankerKeyAAD binds a sealed key to its purpose and its row.
func rankerKeyAAD(id int64) string { return "autodoc:ranker.api_key:" + strconv.FormatInt(id, 10) }

// Rankers are every ranker, by name.
func (s *Store) Rankers(ctx context.Context) ([]RankerInfo, error) {
	var out []RankerInfo
	err := s.Read(ctx, func(tx *Tx) error {
		rows, err := tx.t.rankers.On(tx.tx).OrderBy(dao.Asc(ByKey)).Select()
		for _, r := range rows {
			out = append(out, rankerInfoOf(r))
		}
		return err
	})
	return out, err
}

// AddRanker records a ranker, its key sealed.
func (s *Store) AddRanker(ctx context.Context, sp RankerSpec) (RankerInfo, error) {
	if err := sp.Check(); err != nil {
		return RankerInfo{}, err
	}
	var info RankerInfo
	err := s.Write(ctx, func(tx *Tx) error {
		now := time.Now().Unix()
		id, err := tx.t.rankers.On(tx.tx).Set(RankerName, sp.Name).Set(RankerKind, sp.Kind).
			Set(RankerBaseURL, sp.BaseURL).Set(RankerModel, sp.model()).
			Set(RankerCreatedAt, now).Set(RankerUpdatedAt, now).Insert()
		if err != nil {
			return rankerTaken(err)
		}
		if err := s.setRankerKey(tx, id, sp.Key); err != nil {
			return err
		}
		r, err := tx.t.rankers.On(tx.tx).With(RankerID, id).Get()
		if err != nil {
			return err
		}
		info = rankerInfoOf(r)
		return nil
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
		id, err := s.rankerID(tx, name)
		if err != nil {
			return err
		}
		if err := tx.t.rankers.On(tx.tx).With(RankerID, id).Set(RankerName, sp.Name).Set(RankerKind, sp.Kind).
			Set(RankerBaseURL, sp.BaseURL).Set(RankerModel, sp.model()).
			Set(RankerUpdatedAt, time.Now().Unix()).Update(); err != nil {
			return rankerTaken(err)
		}
		if err := s.setRankerKey(tx, id, sp.Key); err != nil {
			return err
		}
		return followRanker(tx, name, sp.Name)
	})
}

// RemoveRanker deletes a ranker, and with it its usage and its log; a preference naming it names
// none.
func (s *Store) RemoveRanker(ctx context.Context, name string) error {
	return s.Write(ctx, func(tx *Tx) error {
		id, err := s.rankerID(tx, name)
		if err != nil {
			return err
		}
		if err := tx.t.rankers.On(tx.tx).With(RankerID, id).Delete(); err != nil {
			return err
		}
		return followRanker(tx, name, "")
	})
}

// followRanker makes the preference naming ranker from name to instead ("" for removed: none in
// use), in the transaction that renames or removes it.
func followRanker(tx *Tx, from, to string) error {
	if from == to {
		return nil
	}
	return tx.t.preferences.On(tx.tx).With(PrefName, PrefRanker).With(PrefValue, from).
		Set(PrefValue, to).Set(PrefUpdatedAt, time.Now().Unix()).Update()
}

// setRankerKey seals key into the ranker's row; nil leaves it, "" removes it.
func (s *Store) setRankerKey(tx *Tx, id int64, key *string) error {
	if key == nil {
		return nil
	}
	var sealed any
	if *key != "" {
		b, err := s.keys.seal([]byte(*key), rankerKeyAAD(id))
		if err != nil {
			return err
		}
		sealed = b
	}
	return tx.t.rankers.On(tx.tx).With(RankerID, id).Set(RankerAPIKey, sealed).Update()
}

// RankerWithKey is the ranker named name and its key, opened: for the daemon, which ranks with it,
// and never for a client.
func (s *Store) RankerWithKey(ctx context.Context, name string) (RankerInfo, string, error) {
	var r *Ranker
	err := s.Read(ctx, func(tx *Tx) error {
		var err error
		r, err = tx.t.rankers.On(tx.tx).With(RankerName, name).Get()
		if errors.Is(err, dao.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrNoRanker, name)
		}
		return err
	})
	if err != nil {
		return RankerInfo{}, "", err
	}
	if len(r.APIKey) == 0 {
		return rankerInfoOf(r), "", nil
	}
	key, err := s.keys.open(r.APIKey, rankerKeyAAD(r.ID))
	if err != nil {
		return RankerInfo{}, "", err
	}
	return rankerInfoOf(r), string(key), nil
}

// RecordRankerCalls adds calls to a ranker's usage by day and to its log, which keeps the last 200.
func (s *Store) RecordRankerCalls(ctx context.Context, rankerID int64, calls []CallRecord) error {
	return s.recordCalls(ctx, func(t *tables) meterTables { return t.rankerMeter() }, rankerID, calls)
}

// RankerUsage is the ranker's use by day, the latest first, at most days of them.
func (s *Store) RankerUsage(ctx context.Context, name string, days int) ([]Usage, error) {
	return s.usageOf(ctx, func(t *tables) meterTables { return t.rankerMeter() }, s.rankerID, name, days)
}

// RankerLog is the ranker's recent calls, the latest first, at most limit of them.
func (s *Store) RankerLog(ctx context.Context, name string, limit int) ([]LogEntry, error) {
	return s.logOf(ctx, func(t *tables) meterTables { return t.rankerMeter() }, s.rankerID, name, limit)
}

func (s *Store) rankerID(tx *Tx, name string) (int64, error) {
	r, err := tx.t.rankers.On(tx.tx).With(RankerName, name).Get(RankerID)
	if errors.Is(err, dao.ErrNoRows) {
		return 0, fmt.Errorf("%w: %s", ErrNoRanker, name)
	}
	if err != nil {
		return 0, err
	}
	return r.ID, nil
}

func rankerTaken(err error) error {
	if errors.Is(err, dao.ErrDuplicate) {
		return fmt.Errorf("%w: %v", ErrRankerTaken, err)
	}
	return err
}

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// RANKING — the ranker search's second stage uses (ADR 0215): one for the daemon, every workspace's
// indexer reading it from one rank.Holder. It is either the stored ranker the preference
// ranker.in_use names, set up with its key opened, probed, metered into the store's usage and log,
// and switched while the daemon runs; or the build's own (app.Options.Rank.Ranker), fixed for the
// daemon's life. None, or a stored one that does not set up, and search answers in recall order.

// rankerStore is what Ranking keeps in the store.
type rankerStore interface {
	Preferences(ctx context.Context) (map[string]string, error)
	SetPreference(ctx context.Context, name, value string) error
	Rankers(ctx context.Context) ([]store.RankerInfo, error)
	AddRanker(ctx context.Context, sp store.RankerSpec) (store.RankerInfo, error)
	UpdateRanker(ctx context.Context, name string, sp store.RankerSpec) error
	RemoveRanker(ctx context.Context, name string) error
	RankerWithKey(ctx context.Context, name string) (store.RankerInfo, string, error)
	RecordRankerCalls(ctx context.Context, rankerID int64, calls []store.CallRecord) error
	RankerUsage(ctx context.Context, name string, days int) ([]store.Usage, error)
	RankerLog(ctx context.Context, name string, limit int) ([]store.LogEntry, error)
}

// Supplied is a build's own ranker, and the window it ranks (0: the default).
type Supplied struct {
	Ranker rank.Ranker
	Window int
}

// Ranking is the daemon's ranker, and the store's rankers behind it.
type Ranking struct {
	db       rankerStore
	log      logger.Logger
	client   *http.Client
	holder   *rank.Holder
	supplied Supplied

	// switching serialises the changes to the ranker in use: each sets up, then writes the store,
	// then sets the Holder
	switching sync.Mutex

	mu      sync.Mutex
	active  string      // the stored ranker in use, by name; "" for none
	live    rank.Ranker // it, set up; nil for none
	window  int
	lastErr string // why the ranker the store names is not in use, "" when it is
	failed  string // the ranker the store names that did not set up at Start, "" for none

	meter *callMeter
}

// NewRanking is the daemon's ranking over db, setting holder, which every workspace's indexer
// reads. A build's own ranker, when supplied has one, is the only one in use. Nothing is set up
// until Start.
func NewRanking(db *store.Store, holder *rank.Holder, supplied Supplied, log logger.Logger) *Ranking {
	if log == nil {
		log = logger.New()
	}
	r := &Ranking{db: db, log: log, client: &http.Client{}, holder: holder, supplied: supplied, window: rank.DefaultWindow}
	r.meter = newCallMeter(db.RecordRankerCalls, log, "ranker usage")
	return r
}

// Start writes the meters' calls until ctx ends, and sets up the ranker in use: the build's own,
// fixed for the daemon's life, else the stored one the preference names. A build's ranker whose
// probe fails is kept, so each search's stage fails (state error, or refused under Required),
// never a stored ranker in its place.
func (r *Ranking) Start(ctx context.Context) {
	go r.meter.flush(ctx)
	if s := r.supplied.Ranker; s != nil {
		r.window = rank.Window(r.supplied.Window)
		if err := probe(ctx, s); err != nil {
			logger.Warning(r.log, err, "the build's ranker did not answer its probe: searches are not ranked until it does")
			r.mu.Lock()
			r.lastErr = err.Error()
			r.mu.Unlock()
		}
		r.holder.Set(s, r.window)
		return
	}
	prefs, err := r.db.Preferences(ctx)
	if err != nil {
		logger.Warning(r.log, err, "ranking off: the preferences could not be read")
		return
	}
	r.window = windowOf(prefs[store.PrefRankerWindow])
	if name := prefs[store.PrefRanker]; name != "" {
		live, err := r.setUp(ctx, name)
		if err != nil {
			logger.Warning(r.log, err, "ranking off: "+name+" could not be set up")
			r.mu.Lock()
			r.lastErr, r.failed = err.Error(), name
			r.mu.Unlock()
			return
		}
		r.swap(name, live)
	}
}

// Wait returns once the last calls heard are written, after Start's ctx ended.
func (r *Ranking) Wait() { r.meter.wait() }

// windowOf is a stored window, the default when none or a bad one is stored.
func windowOf(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < rank.MinWindow || n > rank.MaxWindow {
		return rank.DefaultWindow
	}
	return n
}

// rankTimeout bounds a ranker's setup and probe.
const rankTimeout = 30 * time.Second

// probe has r score two texts: an answer of two finite scores, or the reason it gave none.
func probe(ctx context.Context, r rank.Ranker) error {
	ctx, cancel := context.WithTimeout(ctx, rankTimeout)
	defer cancel()
	scores, err := r.Rank(ctx, "probe", []string{"a ranker's probe", "another text"})
	if err != nil {
		return err
	}
	if len(scores) != 2 {
		return fmt.Errorf("%w: %d scores for the probe's 2 texts", rank.ErrBadAnswer, len(scores))
	}
	return nil
}

// build sets up a stored ranker with its key, metered, and probes it: TEI's setup asks /info for a
// re-ranker, and both kinds must score the probe's two texts.
func (r *Ranking) build(ctx context.Context, info store.RankerInfo, key string) (rank.Ranker, error) {
	ctx, cancel := context.WithTimeout(ctx, rankTimeout)
	defer cancel()
	var live rank.Ranker
	switch info.Kind {
	case store.KindTEI:
		t, err := rank.NewTEI(ctx, info.BaseURL, key, r.client)
		if err != nil {
			return nil, err
		}
		t.SetMeter(func(c rank.Call) { r.meter.hear(info.ID, c.Texts, c.Tokens, c.Duration, c.Err) })
		live = t
	default:
		a := rank.NewRerankAPI(info.BaseURL, key, info.Model, r.client)
		a.SetMeter(func(c rank.Call) { r.meter.hear(info.ID, c.Texts, c.Tokens, c.Duration, c.Err) })
		live = a
	}
	if err := probe(ctx, live); err != nil {
		return nil, err
	}
	return live, nil
}

// setUp sets up the stored ranker named name.
func (r *Ranking) setUp(ctx context.Context, name string) (rank.Ranker, error) {
	info, key, err := r.db.RankerWithKey(ctx, name)
	if err != nil {
		return nil, err
	}
	return r.build(ctx, info, key)
}

// swap makes the stored ranker live (nil: none) the one in use, named name, in the Holder.
func (r *Ranking) swap(name string, live rank.Ranker) {
	r.mu.Lock()
	r.active, r.live, r.lastErr, r.failed = name, live, "", ""
	window := r.window
	r.mu.Unlock()
	r.holder.Set(live, window)
}

// refuseSupplied refuses a change to the ranker in use, or to the stored selection, while the build
// supplies its ranker.
func (r *Ranking) refuseSupplied() error {
	if r.supplied.Ranker != nil {
		return rpc.ErrSuppliedRanker
	}
	return nil
}

// Use makes the stored ranker named name the one in use, and remembers it; "" uses none. The ranker
// is set up and probed, then the preference written, and only then set in the Holder: one that does
// not set up, or a preference that is not written, changes nothing.
func (r *Ranking) Use(ctx context.Context, name string) error {
	if err := r.refuseSupplied(); err != nil {
		return err
	}
	r.switching.Lock()
	defer r.switching.Unlock()
	if name == "" {
		if err := r.db.SetPreference(ctx, store.PrefRanker, ""); err != nil {
			return err
		}
		r.swap("", nil)
		return nil
	}
	live, err := r.setUp(ctx, name)
	if err != nil {
		return err
	}
	if err := r.db.SetPreference(ctx, store.PrefRanker, name); err != nil {
		return err
	}
	r.swap(name, live)
	return nil
}

// SetWindow sets how many of the top candidates are ranked (rank.MinWindow to rank.MaxWindow), kept
// and then set in the Holder.
func (r *Ranking) SetWindow(ctx context.Context, n int) error {
	if err := r.refuseSupplied(); err != nil {
		return err
	}
	if n < rank.MinWindow || n > rank.MaxWindow {
		return rpc.ErrWindow
	}
	r.switching.Lock()
	defer r.switching.Unlock()
	if err := r.db.SetPreference(ctx, store.PrefRankerWindow, strconv.Itoa(n)); err != nil {
		return err
	}
	r.mu.Lock()
	r.window = n
	live := r.live
	r.mu.Unlock()
	if live != nil {
		r.holder.Set(live, n)
	}
	return nil
}

// AddRanker records a ranker; it is used once chosen.
func (r *Ranking) AddRanker(ctx context.Context, sp store.RankerSpec) (store.RankerInfo, error) {
	return r.db.AddRanker(ctx, sp)
}

// dormant reports whether name is the stored selection, kept for a build that does not supply its
// ranker: under a build's ranker it may be neither rewritten nor removed.
func (r *Ranking) dormant(ctx context.Context, name string) (bool, error) {
	if r.supplied.Ranker == nil {
		return false, nil
	}
	prefs, err := r.db.Preferences(ctx)
	if err != nil {
		return false, err
	}
	return prefs[store.PrefRanker] == name, nil
}

// UpdateRanker rewrites a ranker. The one in use is set up and probed with what it will say BEFORE
// it is rewritten, so an edit that does not set up is refused with nothing changed; then the store
// is written (a rename carrying the preference) and the Holder takes it.
func (r *Ranking) UpdateRanker(ctx context.Context, name string, sp store.RankerSpec) error {
	r.switching.Lock()
	defer r.switching.Unlock()
	if d, err := r.dormant(ctx, name); err != nil || d {
		if d {
			err = rpc.ErrSuppliedRanker
		}
		return err
	}
	r.mu.Lock()
	inUse := r.active == name && r.live != nil
	r.mu.Unlock()
	if !inUse {
		if err := r.db.UpdateRanker(ctx, name, sp); err != nil {
			return err
		}
		r.forget(name)
		return nil
	}
	if err := sp.Check(); err != nil {
		return err
	}
	old, key, err := r.db.RankerWithKey(ctx, name)
	if err != nil {
		return err
	}
	if sp.Key != nil {
		key = *sp.Key
	}
	live, err := r.build(ctx, store.RankerInfo{ID: old.ID, Name: sp.Name, Kind: sp.Kind, BaseURL: sp.BaseURL, Model: sp.Model}, key)
	if err != nil {
		return err
	}
	if err := r.db.UpdateRanker(ctx, name, sp); err != nil {
		return err
	}
	r.swap(sp.Name, live)
	return nil
}

// RemoveRanker deletes a ranker, the preference naming none with it when it names this one; only
// once it is gone does the one in use go out of use.
func (r *Ranking) RemoveRanker(ctx context.Context, name string) error {
	r.switching.Lock()
	defer r.switching.Unlock()
	if d, err := r.dormant(ctx, name); err != nil || d {
		if d {
			err = rpc.ErrSuppliedRanker
		}
		return err
	}
	if err := r.db.RemoveRanker(ctx, name); err != nil {
		return err
	}
	r.mu.Lock()
	inUse := r.active == name
	r.mu.Unlock()
	if inUse {
		r.swap("", nil)
	}
	r.forget(name)
	return nil
}

// forget drops why name did not set up at Start, once it is edited or removed: the reason is about
// a ranker that is no longer as it was.
func (r *Ranking) forget(name string) {
	r.mu.Lock()
	if r.failed == name {
		r.lastErr, r.failed = "", ""
	}
	r.mu.Unlock()
}

// ListRankers is the ranking as a client lists it: the stored rankers, the one in use and its
// window, why the one named is not in use (or why the build's own did not answer its probe), and
// the build's own ranker when it supplies one, in place of a stored one in use.
func (r *Ranking) ListRankers(ctx context.Context) (rpc.RankerList, error) {
	rs, err := r.db.Rankers(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	st := rpc.RankerList{Rankers: rs, Active: r.active, Window: r.window, Err: r.lastErr}
	if s := r.supplied.Ranker; s != nil {
		st.Supplied, st.Active = s.Model().Name, ""
	}
	return st, err
}

// Supplied is the build's ranker's model, and whether the build supplies one.
func (r *Ranking) Supplied() (string, bool) {
	if s := r.supplied.Ranker; s != nil {
		return s.Model().Name, true
	}
	return "", false
}

// Models are what a ranker serves: a stored one's, by name; one not yet stored, by its kind, base
// URL and key; or a stored one as an edit would make it, by its name and the edit's kind and base
// URL, the stored key used unless the edit has one. A TEI server names its one model; a rerank-API
// endpoint names none, so its model is typed.
func (r *Ranking) Models(ctx context.Context, name string, sp store.RankerSpec) ([]string, error) {
	kind, base, key := sp.Kind, sp.BaseURL, ""
	if sp.Key != nil {
		key = *sp.Key
	}
	if name != "" {
		info, k, err := r.db.RankerWithKey(ctx, name)
		if err != nil {
			return nil, err
		}
		if sp.Kind == "" {
			kind, base = info.Kind, info.BaseURL
		}
		if sp.Key == nil {
			key = k
		}
	}
	if kind != store.KindTEI {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, rankTimeout)
	defer cancel()
	t, err := rank.NewTEI(ctx, base, key, r.client)
	if err != nil {
		return nil, err
	}
	return []string{t.Model().Name}, nil
}

// Usage and Log are a ranker's, from the store.
func (r *Ranking) Usage(ctx context.Context, name string, days int) ([]store.Usage, error) {
	return r.db.RankerUsage(ctx, name, days)
}

func (r *Ranking) Log(ctx context.Context, name string, limit int) ([]store.LogEntry, error) {
	return r.db.RankerLog(ctx, name, limit)
}

// rankOutcome is a failed ranker call as its log says it.
func rankOutcome(err error) string {
	switch {
	case errors.Is(err, rank.ErrRateLimited):
		return "rate limited: the ranker's usage limit"
	case errors.Is(err, rank.ErrUnauthorized):
		return "the ranker refused the key"
	case errors.Is(err, rank.ErrUnreachable):
		return "the ranker did not answer"
	case errors.Is(err, rank.ErrBadAnswer):
		return "the ranker's answer was not one score for each text"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timed out"
	}
	return "failed"
}

var _ rpc.Rankers = (*Ranking)(nil)

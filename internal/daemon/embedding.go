package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/embed"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// EMBEDDING — the provider semantic search uses: one of the store's providers, the one the
// preference embedding.provider names, set up with its key opened, metered into the store's usage
// and log, and switched while the daemon runs. None named, or none that sets up, and search is by
// words alone.

// setupTimeout bounds a provider's setup: its model's digest, and one probe text.
const setupTimeout = 30 * time.Second

// flushEvery is how often the calls the meters heard are written to the store: a write per call
// would take the store's one writer from the indexers as often as they embed.
const flushEvery = 2 * time.Second

// providerStore is what the Embedding keeps in the store: the preference naming the provider in
// use, the providers, and their usage and log.
type providerStore interface {
	Preferences(ctx context.Context) (map[string]string, error)
	SetPreference(ctx context.Context, name, value string) error
	Providers(ctx context.Context) ([]store.ProviderInfo, error)
	AddProvider(ctx context.Context, sp store.ProviderSpec) (store.ProviderInfo, error)
	UpdateProvider(ctx context.Context, name string, sp store.ProviderSpec) error
	RemoveProvider(ctx context.Context, name string) error
	ProviderWithKey(ctx context.Context, name string) (store.ProviderInfo, string, error)
	RecordCalls(ctx context.Context, providerID int64, calls []store.CallRecord) error
	ProviderUsage(ctx context.Context, name string, days int) ([]store.Usage, error)
	ProviderLog(ctx context.Context, name string, limit int) ([]store.LogEntry, error)
}

// Embedding is the daemon's embedding provider, and the store's providers behind it.
type Embedding struct {
	db     providerStore
	ws     *Workspaces
	log    logger.Logger
	client *http.Client

	// switching serialises the changes to the provider in use (Use, UpdateProvider,
	// RemoveProvider): each sets up, then writes the store, then swaps, and two interleaved could
	// leave the store naming one provider and the workspaces embedding with another
	switching sync.Mutex

	mu      sync.Mutex
	active  string // the provider in use, by name; "" for none
	lastErr string // why the provider named is not in use, "" when it is

	// the calls the meters heard and the store has yet to keep, by provider; a write that fails
	// leaves them here for the next
	pmu     sync.Mutex
	pending map[int64][]store.CallRecord
	unkept  int // calls past maxPending, not kept: reported at the next write

	done chan struct{}
}

// maxPending bounds a provider's calls held for the store: a store that cannot be written for
// long holds this many (a few hundred KiB), and the calls past them are counted and reported,
// not kept.
const maxPending = 10000

// NewEmbedding is the embedding of the daemon over db, serving ws. Nothing is set up until Start.
func NewEmbedding(db *store.Store, ws *Workspaces, log logger.Logger) *Embedding {
	if log == nil {
		log = logger.New()
	}
	return &Embedding{db: db, ws: ws, log: log, client: &http.Client{}, pending: map[int64][]store.CallRecord{}, done: make(chan struct{})}
}

// Start writes the meters' calls to the store until ctx ends, and sets up the provider the
// preference names. One that does not set up is logged, and search is by words.
func (e *Embedding) Start(ctx context.Context) {
	go e.flush(ctx)
	prefs, err := e.db.Preferences(ctx)
	if err != nil {
		logger.Warning(e.log, err, "embedding off: the preferences could not be read")
		return
	}
	if name := prefs[store.PrefProvider]; name != "" {
		if err := e.apply(ctx, name); err != nil {
			logger.Warning(e.log, err, "embedding off: "+name+" could not be set up")
		}
	}
}

// Wait returns once the last calls heard are written, after Start's ctx ended.
func (e *Embedding) Wait() { <-e.done }

// Use makes the provider named name the one semantic search uses, and remembers it; "" turns
// semantic search off. In that order: the provider is set up, the preference written, and only
// then do the workspaces take it, so a provider that does not set up, or a preference that is not
// written, leaves the one in use in use, and the store naming it.
func (e *Embedding) Use(ctx context.Context, name string) error {
	e.switching.Lock()
	defer e.switching.Unlock()
	if name == "" {
		if err := e.db.SetPreference(ctx, store.PrefProvider, ""); err != nil {
			return err
		}
		e.swap("", nil, nil)
		return nil
	}
	p, pf, err := e.setUp(ctx, name)
	if err != nil {
		return err
	}
	if err := e.db.SetPreference(ctx, store.PrefProvider, name); err != nil {
		return err
	}
	e.swap(name, p, pf)
	return nil
}

// apply is Start's: the provider the preference already names, set up and given to the
// workspaces.
func (e *Embedding) apply(ctx context.Context, name string) error {
	p, pf, err := e.setUp(ctx, name)
	if err != nil {
		return err
	}
	e.swap(name, p, pf)
	return nil
}

// setUp sets up the stored provider named name; one that does not is the reason kept for
// Providers, the one in use staying.
func (e *Embedding) setUp(ctx context.Context, name string) (embed.Provider, func(embed.Model) (embed.Provider, error), error) {
	info, key, err := e.db.ProviderWithKey(ctx, name)
	if err == nil {
		var p embed.Provider
		var pf func(embed.Model) (embed.Provider, error)
		if p, pf, err = e.build(ctx, info, key); err == nil {
			return p, pf, nil
		}
	}
	e.set(e.current(), err.Error())
	return nil, nil, err
}

// swap gives every workspace provider p (nil: none) as the one named name.
func (e *Embedding) swap(name string, p embed.Provider, pf func(embed.Model) (embed.Provider, error)) {
	e.ws.SetEmbedding(p, pf)
	e.set(name, "")
}

func (e *Embedding) set(active, lastErr string) {
	e.mu.Lock()
	e.active, e.lastErr = active, lastErr
	e.mu.Unlock()
}

func (e *Embedding) current() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active
}

// build sets up provider info with its key, metered, and the maker of its other models' providers
// (the model still active while a new one fills).
func (e *Embedding) build(ctx context.Context, info store.ProviderInfo, key string) (embed.Provider, func(embed.Model) (embed.Provider, error), error) {
	name := info.Name
	newProvider := func(ctx context.Context, model string) (embed.Provider, error) {
		ctx, cancel := context.WithTimeout(ctx, setupTimeout)
		defer cancel()
		var p embed.Provider
		var err error
		if info.Kind == store.KindOllama || info.Kind == store.KindOllamaCloud {
			p, err = embed.NewOllama(ctx, info.BaseURL, key, model, e.client)
		} else {
			p, err = embed.NewOpenAI(ctx, info.BaseURL, key, model, e.client)
		}
		if err != nil {
			return nil, err
		}
		if m, ok := p.(embed.Metered); ok {
			m.SetMeter(func(c embed.Call) { e.hear(info.ID, c) })
		}
		return p, nil
	}
	p, err := newProvider(ctx, info.Model)
	if err != nil {
		return nil, nil, err
	}
	return p, func(m embed.Model) (embed.Provider, error) {
		if m.Provider != p.Model().Provider {
			return nil, fmt.Errorf("model %s is not %s's", m.Fingerprint(), name)
		}
		return newProvider(context.Background(), m.Name)
	}, nil
}

// hear holds a call for the store. It takes a lock for an append, never waiting on the store, so
// the embedding worker that made the call is not held up and no call is dropped while the store
// keeps up.
func (e *Embedding) hear(provider int64, c embed.Call) {
	rec := store.CallRecord{At: time.Now(), Texts: c.Texts, Tokens: c.Tokens, Millis: c.Duration.Milliseconds(), Outcome: "ok"}
	if c.Err != nil {
		rec.Failed, rec.Outcome = true, outcomeOf(c.Err)
		rec.Limited = errors.Is(c.Err, embed.ErrRateLimited)
	}
	e.pmu.Lock()
	defer e.pmu.Unlock()
	if len(e.pending[provider]) >= maxPending {
		e.unkept++
		return
	}
	e.pending[provider] = append(e.pending[provider], rec)
}

// outcomeOf is a failed call as its log says it.
func outcomeOf(err error) string {
	switch {
	case errors.Is(err, embed.ErrRateLimited):
		return "rate limited: the provider's usage limit"
	case errors.Is(err, embed.ErrUnauthorized):
		return "the provider refused the key"
	case errors.Is(err, embed.ErrRejected):
		return "the provider rejected the input"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timed out"
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return "failed: " + msg
}

// flush writes the calls heard every flushEvery, and once more when ctx ends.
func (e *Embedding) flush(ctx context.Context) {
	defer close(e.done)
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			e.writePending(ctx)
		case <-ctx.Done():
			e.writePending(context.WithoutCancel(ctx))
			return
		}
	}
}

// writePending writes the calls held, a provider's in one transaction. A provider's write that
// fails puts its calls back, before any heard since, to be written with the next.
func (e *Embedding) writePending(ctx context.Context) {
	e.pmu.Lock()
	batch, unkept := e.pending, e.unkept
	e.pending, e.unkept = map[int64][]store.CallRecord{}, 0
	e.pmu.Unlock()
	if unkept > 0 {
		logger.Warning(e.log, nil, fmt.Sprintf("embedding usage: %d calls not kept, the store not written for too long", unkept))
	}
	for id, recs := range batch {
		err := e.db.RecordCalls(ctx, id, recs)
		if err == nil {
			continue
		}
		logger.Warning(e.log, err, "embedding usage not recorded yet: kept for the next write")
		e.pmu.Lock()
		back := append(recs, e.pending[id]...)
		if over := len(back) - maxPending; over > 0 {
			back, e.unkept = back[over:], e.unkept+over // the oldest go
		}
		e.pending[id] = back
		e.pmu.Unlock()
	}
}

// Providers are the store's providers, the one in use, and why the one named is not, if it is not.
func (e *Embedding) Providers(ctx context.Context) ([]store.ProviderInfo, string, string, error) {
	ps, err := e.db.Providers(ctx)
	e.mu.Lock()
	defer e.mu.Unlock()
	return ps, e.active, e.lastErr, err
}

// AddProvider records a provider; it is used once chosen.
func (e *Embedding) AddProvider(ctx context.Context, sp store.ProviderSpec) (store.ProviderInfo, error) {
	return e.db.AddProvider(ctx, sp)
}

// UpdateProvider rewrites a provider. The one in use is set up with what it will say BEFORE it is
// rewritten, so an edit that does not set up is refused with nothing changed; then the store is
// written (a rename renaming the preference with it) and the workspaces take it, a model switch
// among them.
func (e *Embedding) UpdateProvider(ctx context.Context, name string, sp store.ProviderSpec) error {
	e.switching.Lock()
	defer e.switching.Unlock()
	if e.current() != name {
		return e.db.UpdateProvider(ctx, name, sp)
	}
	old, key, err := e.db.ProviderWithKey(ctx, name)
	if err != nil {
		return err
	}
	if sp.Key != nil {
		key = *sp.Key
	}
	p, pf, err := e.build(ctx, store.ProviderInfo{ID: old.ID, Name: sp.Name, Kind: sp.Kind, BaseURL: sp.BaseURL, Model: sp.Model}, key)
	if err != nil {
		return err
	}
	if err := e.db.UpdateProvider(ctx, name, sp); err != nil {
		return err
	}
	e.swap(sp.Name, p, pf)
	return nil
}

// RemoveProvider deletes a provider, the preference naming none with it when it names this one;
// only once it is gone does the one in use go out of use, so a remove that fails changes nothing.
func (e *Embedding) RemoveProvider(ctx context.Context, name string) error {
	e.switching.Lock()
	defer e.switching.Unlock()
	if err := e.db.RemoveProvider(ctx, name); err != nil {
		return err
	}
	if e.current() == name {
		e.swap("", nil, nil)
	}
	return nil
}

// Models are what a provider offers: a stored one's, by name (its key opened here), or those of
// one not yet stored, by its kind, base URL and key.
func (e *Embedding) Models(ctx context.Context, name string, sp store.ProviderSpec) ([]string, error) {
	kind, base, key := sp.Kind, sp.BaseURL, ""
	if sp.Key != nil {
		key = *sp.Key
	}
	if name != "" {
		info, k, err := e.db.ProviderWithKey(ctx, name)
		if err != nil {
			return nil, err
		}
		kind, base, key = info.Kind, info.BaseURL, k
	}
	ctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	return embed.Models(ctx, kind, base, key, e.client)
}

// Usage and Log are a provider's, from the store.
func (e *Embedding) Usage(ctx context.Context, name string, days int) ([]store.Usage, error) {
	return e.db.ProviderUsage(ctx, name, days)
}

func (e *Embedding) Log(ctx context.Context, name string, limit int) ([]store.LogEntry, error) {
	return e.db.ProviderLog(ctx, name, limit)
}

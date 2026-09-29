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

// Embedding is the daemon's embedding provider, and the store's providers behind it.
type Embedding struct {
	db     *store.Store
	ws     *Workspaces
	log    logger.Logger
	client *http.Client

	mu      sync.Mutex
	active  string // the provider in use, by name; "" for none
	lastErr string // why the provider named is not in use, "" when it is

	calls chan heard
	done  chan struct{}
}

// heard is one call a meter heard, for the provider it was made to.
type heard struct {
	provider int64
	rec      store.CallRecord
}

// NewEmbedding is the embedding of the daemon over db, serving ws. Nothing is set up until Start.
func NewEmbedding(db *store.Store, ws *Workspaces, log logger.Logger) *Embedding {
	if log == nil {
		log = logger.New()
	}
	return &Embedding{db: db, ws: ws, log: log, client: &http.Client{}, calls: make(chan heard, 1024), done: make(chan struct{})}
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
// semantic search off. A provider that does not set up is refused, and the one in use stays.
func (e *Embedding) Use(ctx context.Context, name string) error {
	if err := e.apply(ctx, name); err != nil {
		return err
	}
	return e.db.SetPreference(ctx, store.PrefProvider, name)
}

// apply sets up the provider named name (none for "") and gives it to every workspace.
func (e *Embedding) apply(ctx context.Context, name string) error {
	if name == "" {
		e.ws.SetEmbedding(nil, nil)
		e.set("", "")
		return nil
	}
	p, pf, err := e.build(ctx, name)
	if err != nil {
		e.set(e.current(), err.Error())
		return err
	}
	e.ws.SetEmbedding(p, pf)
	e.set(name, "")
	return nil
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

// build sets up the provider named name, metered, and the maker of its other models' providers
// (the model still active while a new one fills).
func (e *Embedding) build(ctx context.Context, name string) (embed.Provider, func(embed.Model) (embed.Provider, error), error) {
	info, key, err := e.db.ProviderWithKey(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	newProvider := func(ctx context.Context, model string) (embed.Provider, error) {
		ctx, cancel := context.WithTimeout(ctx, setupTimeout)
		defer cancel()
		var p embed.Provider
		var err error
		if info.Kind == store.KindOllama {
			p, err = embed.NewOllama(ctx, info.BaseURL, model, e.client)
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

// hear queues a call for the store; a queue full to its brim drops it rather than hold up the
// embedding worker that made it.
func (e *Embedding) hear(provider int64, c embed.Call) {
	rec := store.CallRecord{At: time.Now(), Texts: c.Texts, Tokens: c.Tokens, Millis: c.Duration.Milliseconds(), Outcome: "ok"}
	if c.Err != nil {
		rec.Failed, rec.Outcome = true, outcomeOf(c.Err)
		rec.Limited = errors.Is(c.Err, embed.ErrRateLimited)
	}
	select {
	case e.calls <- heard{provider, rec}:
	default:
	}
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

// flush writes the calls heard, by provider, every flushEvery, and once more when ctx ends.
func (e *Embedding) flush(ctx context.Context) {
	defer close(e.done)
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	pending := map[int64][]store.CallRecord{}
	write := func(ctx context.Context) {
		for id, recs := range pending {
			if err := e.db.RecordCalls(ctx, id, recs); err != nil && !errors.Is(err, context.Canceled) {
				logger.Warning(e.log, err, "embedding usage not recorded")
			}
		}
		clear(pending)
	}
	for {
		select {
		case h := <-e.calls:
			pending[h.provider] = append(pending[h.provider], h.rec)
		case <-tick.C:
			write(ctx)
		case <-ctx.Done():
			for {
				select {
				case h := <-e.calls:
					pending[h.provider] = append(pending[h.provider], h.rec)
					continue
				default:
				}
				break
			}
			write(context.WithoutCancel(ctx))
			return
		}
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

// UpdateProvider rewrites a provider; the one in use is set up again with what it now says, a
// model switch among them.
func (e *Embedding) UpdateProvider(ctx context.Context, name string, sp store.ProviderSpec) error {
	if err := e.db.UpdateProvider(ctx, name, sp); err != nil {
		return err
	}
	if e.current() != name {
		return nil
	}
	return e.Use(ctx, sp.Name) // a rename renames the preference too
}

// RemoveProvider deletes a provider; the one in use goes out of use first.
func (e *Embedding) RemoveProvider(ctx context.Context, name string) error {
	if e.current() == name {
		if err := e.Use(ctx, ""); err != nil {
			return err
		}
	}
	return e.db.RemoveProvider(ctx, name)
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

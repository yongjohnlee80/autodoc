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

// EMBEDDING PROVIDERS — the providers semantic search can use, one of them in use (the preference
// embedding.provider), each with its API key sealed by the keyslot, its usage by day and its
// recent calls.

// The provider kinds: a local Ollama (no key), Ollama Cloud (a key, always), and any
// OpenAI-compatible endpoint (a key where it takes one).
const (
	KindOllama      = "ollama"
	KindOllamaCloud = "ollama-cloud"
	KindOpenAI      = "openai"
)

// knownKind reports one of the three.
func knownKind(k string) bool { return k == KindOllama || k == KindOllamaCloud || k == KindOpenAI }

// PrefProvider is the preference naming the provider in use; "" or absent: none, words alone.
const PrefProvider = "embedding.provider"

// logKept is how many of a provider's calls its log keeps.
const logKept = 200

// The refusals of the provider table.
var (
	ErrNoProvider       = errors.New("store: no such embedding provider")
	ErrProviderTaken    = errors.New("store: another embedding provider has this name")
	ErrProviderInvalid  = errors.New("store: an embedding provider needs a name, a kind (ollama, ollama-cloud or openai), a base URL and a model")
	ErrProviderNeedsKey = errors.New("store: an ollama-cloud provider needs its API key")
)

// A provider's context window, in tokens: what one text sent to it may hold. An Ollama server
// loads the model at this size (num_ctx), so it also decides the model's memory.
const (
	DefaultContext = 8192
	MinContext     = 256
	MaxContext     = 262144
)

// ErrContextRange is a context window outside MinContext and MaxContext.
var ErrContextRange = fmt.Errorf("store: a provider's context window is %d to %d tokens", MinContext, MaxContext)

// ContextMaximumError is a window above the chosen Ollama model's advertised maximum.
type ContextMaximumError struct{ Maximum int }

func (e *ContextMaximumError) Error() string {
	return fmt.Sprintf("context window exceeds this model's maximum of %d tokens", e.Maximum)
}

// ProviderSpec is a provider as a client writes it. Key nil keeps the key the store holds; a
// pointer to "" removes it. Context 0 is DefaultContext.
type ProviderSpec struct {
	Name, Kind, BaseURL, Model string
	Context                    int
	Key                        *string
}

// ProviderInfo is a provider as a client reads it: never its key, only whether it has one.
type ProviderInfo struct {
	ID                         int64
	Name, Kind, BaseURL, Model string
	Context                    int
	HasKey                     bool
}

func infoOf(p *Provider) ProviderInfo {
	return ProviderInfo{ID: p.ID, Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, Model: p.Model, Context: int(p.Context),
		HasKey: len(p.APIKey) > 0}
}

// ContextWindow is the spec's context window in tokens, DefaultContext for none.
func (sp ProviderSpec) ContextWindow() int {
	if sp.Context == 0 {
		return DefaultContext
	}
	return sp.Context
}

func (sp ProviderSpec) check() error {
	if strings.TrimSpace(sp.Name) == "" || !knownKind(sp.Kind) ||
		strings.TrimSpace(sp.Model) == "" {
		return ErrProviderInvalid
	}
	if c := sp.ContextWindow(); c < MinContext || c > MaxContext {
		return ErrContextRange
	}
	if u, err := url.Parse(sp.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: base URL %q: want an http or https URL", ErrProviderInvalid, sp.BaseURL)
	}
	return nil
}

// keyAAD binds a sealed key to its purpose and its row.
func keyAAD(id int64) string {
	return "autodoc:embedding_provider.api_key:" + strconv.FormatInt(id, 10)
}

// Providers are every provider, by name.
func (s *Store) Providers(ctx context.Context) ([]ProviderInfo, error) {
	var out []ProviderInfo
	err := s.Read(ctx, func(tx *Tx) error {
		rows, err := tx.t.providers.On(tx.tx).OrderBy(dao.Asc(ByKey)).Select()
		for _, r := range rows {
			out = append(out, infoOf(r))
		}
		return err
	})
	return out, err
}

// AddProvider records a provider, its key sealed.
func (s *Store) AddProvider(ctx context.Context, sp ProviderSpec) (ProviderInfo, error) {
	if err := sp.check(); err != nil {
		return ProviderInfo{}, err
	}
	var info ProviderInfo
	err := s.Write(ctx, func(tx *Tx) error {
		now := time.Now().Unix()
		id, err := tx.t.providers.On(tx.tx).Set(ProviderName, sp.Name).Set(ProviderKind, sp.Kind).
			Set(ProviderBaseURL, sp.BaseURL).Set(ProviderModel, sp.Model).Set(ProviderContext, int64(sp.ContextWindow())).
			Set(ProviderCreatedAt, now).Set(ProviderUpdatedAt, now).Insert()
		if err != nil {
			return providerTaken(err)
		}
		if err := s.setKey(tx, id, sp.Key); err != nil {
			return err
		}
		p, err := s.keyed(tx, id)
		if err != nil {
			return err
		}
		info = infoOf(p)
		return nil
	})
	return info, err
}

// keyed is the provider's row once its key is set: an ollama-cloud provider left without one is
// refused, and the write it is in rolls back.
func (s *Store) keyed(tx *Tx, id int64) (*Provider, error) {
	p, err := tx.t.providers.On(tx.tx).With(ProviderID, id).Get()
	if err != nil {
		return nil, err
	}
	if p.Kind == KindOllamaCloud && len(p.APIKey) == 0 {
		return nil, ErrProviderNeedsKey
	}
	return p, nil
}

// UpdateProvider rewrites the provider named name as sp: sp.Name renames it.
func (s *Store) UpdateProvider(ctx context.Context, name string, sp ProviderSpec) error {
	if err := sp.check(); err != nil {
		return err
	}
	return s.Write(ctx, func(tx *Tx) error {
		p, err := tx.t.providers.On(tx.tx).With(ProviderName, name).Get(ProviderID)
		if errors.Is(err, dao.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrNoProvider, name)
		}
		if err != nil {
			return err
		}
		if err := tx.t.providers.On(tx.tx).With(ProviderID, p.ID).Set(ProviderName, sp.Name).Set(ProviderKind, sp.Kind).
			Set(ProviderBaseURL, sp.BaseURL).Set(ProviderModel, sp.Model).Set(ProviderContext, int64(sp.ContextWindow())).
			Set(ProviderUpdatedAt, time.Now().Unix()).Update(); err != nil {
			return providerTaken(err)
		}
		if err := s.setKey(tx, p.ID, sp.Key); err != nil {
			return err
		}
		if _, err = s.keyed(tx, p.ID); err != nil {
			return err
		}
		return followProvider(tx, name, sp.Name)
	})
}

// followProvider makes the preference naming provider from name to instead, in the transaction
// that renames or removes it ("" for removed: none in use), so the preference never names a
// provider the store does not have.
func followProvider(tx *Tx, from, to string) error {
	if from == to {
		return nil
	}
	return tx.t.preferences.On(tx.tx).With(PrefName, PrefProvider).With(PrefValue, from).
		Set(PrefValue, to).Set(PrefUpdatedAt, time.Now().Unix()).Update()
}

// setKey seals key into the provider's row; nil leaves it, "" removes it.
func (s *Store) setKey(tx *Tx, id int64, key *string) error {
	if key == nil {
		return nil
	}
	var sealed any
	if *key != "" {
		b, err := s.keys.seal([]byte(*key), keyAAD(id))
		if err != nil {
			return err
		}
		sealed = b
	}
	return tx.t.providers.On(tx.tx).With(ProviderID, id).Set(ProviderAPIKey, sealed).Update()
}

// RemoveProvider deletes a provider, and with it its usage and its log; a preference naming it
// names none.
func (s *Store) RemoveProvider(ctx context.Context, name string) error {
	return s.Write(ctx, func(tx *Tx) error {
		id, err := s.providerID(tx, name)
		if err != nil {
			return err
		}
		if err := tx.t.providers.On(tx.tx).With(ProviderID, id).Delete(); err != nil {
			return err
		}
		return followProvider(tx, name, "")
	})
}

// ProviderWithKey is the provider named name and its key, opened: for the daemon, which embeds
// with it, and never for a client.
func (s *Store) ProviderWithKey(ctx context.Context, name string) (ProviderInfo, string, error) {
	var p *Provider
	err := s.Read(ctx, func(tx *Tx) error {
		var err error
		p, err = tx.t.providers.On(tx.tx).With(ProviderName, name).Get()
		if errors.Is(err, dao.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrNoProvider, name)
		}
		return err
	})
	if err != nil {
		return ProviderInfo{}, "", err
	}
	if len(p.APIKey) == 0 {
		return infoOf(p), "", nil
	}
	key, err := s.keys.open(p.APIKey, keyAAD(p.ID))
	if err != nil {
		return ProviderInfo{}, "", err
	}
	return infoOf(p), string(key), nil
}

// CallRecord is one call to a provider, as its meter reported it.
type CallRecord struct {
	At            time.Time
	Texts, Tokens int
	Millis        int64
	Outcome       string // "ok", or what went wrong
	Failed        bool
	Limited       bool // the failure was the provider's usage limit
}

// RecordCalls adds calls to a provider's usage by day and to its log, which keeps the last 200.
func (s *Store) RecordCalls(ctx context.Context, providerID int64, calls []CallRecord) error {
	return s.recordCalls(ctx, func(t *tables) meterTables { return t.embeddingMeter() }, providerID, calls)
}

// meterTables are where a model's calls are kept: its usage by day and its log (log names the
// log's table). An embedding provider's and a ranker's are the same shape in their own tables.
type meterTables struct {
	usage *dao.Schema[*Usage, UsageField, noSort, int64]
	calls *dao.Schema[*LogEntry, LogField, noSort, int64]
	log   string
}

func (t *tables) embeddingMeter() meterTables { return meterTables{t.usage, t.calls, "embedding_log"} }
func (t *tables) rankerMeter() meterTables {
	return meterTables{t.rankUsage, t.rankCalls, "ranker_log"}
}

func (s *Store) recordCalls(ctx context.Context, meterOf func(*tables) meterTables, id int64, calls []CallRecord) error {
	if len(calls) == 0 {
		return nil
	}
	return s.Write(ctx, func(tx *Tx) error {
		m := meterOf(tx.t)
		for _, c := range calls {
			if err := addUsage(tx, m, id, c); err != nil {
				return err
			}
			if _, err := m.calls.On(tx.tx).Set(LogProvider, id).Set(LogAt, c.At.Unix()).
				Set(LogTexts, int64(c.Texts)).Set(LogTokens, int64(c.Tokens)).Set(LogMillis, c.Millis).
				Set(LogOutcome, c.Outcome).Insert(); err != nil {
				return err
			}
		}
		// the oldest beyond the last logKept go
		old, err := m.calls.On(tx.tx).With(LogProvider, id).OrderBy(dao.Desc(ByKey)).
			Offset(logKept).Limit(1).Select(LogID)
		if err != nil || len(old) == 0 {
			return err
		}
		return m.calls.On(tx.tx).With(LogProvider, id).
			WithPredicate(dao.Cmp(dao.T(m.log, LogID), dao.OpLte, dao.Int(old[0].ID))).Delete()
	})
}

func addUsage(tx *Tx, m meterTables, id int64, c CallRecord) error {
	day := c.At.UTC().Format(time.DateOnly)
	var failed, limited int64
	if c.Failed {
		failed = 1
	}
	if c.Limited {
		limited = 1
	}
	n, err := dao.UpdateAffected(m.usage.On(tx.tx).With(UsageProvider, id).With(UsageDay, day).
		Set(UsageRequests, dao.Incr(1)).Set(UsageTexts, dao.Incr(int64(c.Texts))).Set(UsageTokens, dao.Incr(int64(c.Tokens))).
		Set(UsageFailures, dao.Incr(failed)).Set(UsageLimited, dao.Incr(limited)))
	if err != nil || n > 0 {
		return err
	}
	_, err = m.usage.On(tx.tx).Set(UsageProvider, id).Set(UsageDay, day).Set(UsageRequests, int64(1)).
		Set(UsageTexts, int64(c.Texts)).Set(UsageTokens, int64(c.Tokens)).Set(UsageFailures, failed).
		Set(UsageLimited, limited).Insert()
	return err
}

// ProviderUsage is the provider's use by day, the latest first, at most days of them.
func (s *Store) ProviderUsage(ctx context.Context, name string, days int) ([]Usage, error) {
	return s.usageOf(ctx, func(t *tables) meterTables { return t.embeddingMeter() }, s.providerID, name, days)
}

// ProviderLog is the provider's recent calls, the latest first, at most limit of them.
func (s *Store) ProviderLog(ctx context.Context, name string, limit int) ([]LogEntry, error) {
	return s.logOf(ctx, func(t *tables) meterTables { return t.embeddingMeter() }, s.providerID, name, limit)
}

func (s *Store) usageOf(ctx context.Context, meterOf func(*tables) meterTables, idOf func(*Tx, string) (int64, error), name string, days int) ([]Usage, error) {
	var out []Usage
	err := s.Read(ctx, func(tx *Tx) error {
		id, err := idOf(tx, name)
		if err != nil {
			return err
		}
		rows, err := meterOf(tx.t).usage.On(tx.tx).With(UsageProvider, id).OrderBy(dao.Desc(ByKey)).Limit(uint64(max(days, 1))).Select()
		for _, r := range rows {
			out = append(out, *r)
		}
		return err
	})
	return out, err
}

func (s *Store) logOf(ctx context.Context, meterOf func(*tables) meterTables, idOf func(*Tx, string) (int64, error), name string, limit int) ([]LogEntry, error) {
	var out []LogEntry
	err := s.Read(ctx, func(tx *Tx) error {
		id, err := idOf(tx, name)
		if err != nil {
			return err
		}
		rows, err := meterOf(tx.t).calls.On(tx.tx).With(LogProvider, id).OrderBy(dao.Desc(ByKey)).Limit(uint64(max(limit, 1))).Select()
		for _, r := range rows {
			out = append(out, *r)
		}
		return err
	})
	return out, err
}

func (s *Store) providerID(tx *Tx, name string) (int64, error) {
	p, err := tx.t.providers.On(tx.tx).With(ProviderName, name).Get(ProviderID)
	if errors.Is(err, dao.ErrNoRows) {
		return 0, fmt.Errorf("%w: %s", ErrNoProvider, name)
	}
	if err != nil {
		return 0, err
	}
	return p.ID, nil
}

func providerTaken(err error) error {
	if errors.Is(err, dao.ErrDuplicate) {
		return fmt.Errorf("%w: %v", ErrProviderTaken, err)
	}
	return err
}

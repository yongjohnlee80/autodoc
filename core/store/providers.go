package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/dao"
)

// EMBEDDING PROVIDERS — the providers semantic search can use, one of them in use (the preference
// embedding.provider), each with its API key sealed by the keyslot, its usage by day and its
// recent calls.

// The provider kinds.
const (
	KindOllama = "ollama"
	KindOpenAI = "openai"
)

// PrefProvider is the preference naming the provider in use; "" or absent: none, words alone.
const PrefProvider = "embedding.provider"

// logKept is how many of a provider's calls its log keeps.
const logKept = 200

// The refusals of the provider table.
var (
	ErrNoProvider      = errors.New("store: no such embedding provider")
	ErrProviderTaken   = errors.New("store: another embedding provider has this name")
	ErrProviderInvalid = errors.New("store: an embedding provider needs a name, a kind (ollama or openai), a base URL and a model")
)

// ProviderSpec is a provider as a client writes it. Key nil keeps the key the store holds; a
// pointer to "" removes it.
type ProviderSpec struct {
	Name, Kind, BaseURL, Model string
	Key                        *string
}

// ProviderInfo is a provider as a client reads it: never its key, only whether it has one.
type ProviderInfo struct {
	ID                         int64
	Name, Kind, BaseURL, Model string
	HasKey                     bool
}

func infoOf(p *Provider) ProviderInfo {
	return ProviderInfo{ID: p.ID, Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, Model: p.Model, HasKey: len(p.APIKey) > 0}
}

func (sp ProviderSpec) check() error {
	if strings.TrimSpace(sp.Name) == "" || (sp.Kind != KindOllama && sp.Kind != KindOpenAI) ||
		strings.TrimSpace(sp.BaseURL) == "" || strings.TrimSpace(sp.Model) == "" {
		return ErrProviderInvalid
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
			Set(ProviderBaseURL, sp.BaseURL).Set(ProviderModel, sp.Model).
			Set(ProviderCreatedAt, now).Set(ProviderUpdatedAt, now).Insert()
		if err != nil {
			return providerTaken(err)
		}
		if err := s.setKey(tx, id, sp.Key); err != nil {
			return err
		}
		p, err := tx.t.providers.On(tx.tx).With(ProviderID, id).Get()
		info = infoOf(p)
		return err
	})
	return info, err
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
			Set(ProviderBaseURL, sp.BaseURL).Set(ProviderModel, sp.Model).
			Set(ProviderUpdatedAt, time.Now().Unix()).Update(); err != nil {
			return providerTaken(err)
		}
		return s.setKey(tx, p.ID, sp.Key)
	})
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

// RemoveProvider deletes a provider, and with it its usage and its log.
func (s *Store) RemoveProvider(ctx context.Context, name string) error {
	return s.Write(ctx, func(tx *Tx) error {
		id, err := s.providerID(tx, name)
		if err != nil {
			return err
		}
		return tx.t.providers.On(tx.tx).With(ProviderID, id).Delete()
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
	if len(calls) == 0 {
		return nil
	}
	return s.Write(ctx, func(tx *Tx) error {
		for _, c := range calls {
			if err := s.addUsage(tx, providerID, c); err != nil {
				return err
			}
			if _, err := tx.t.calls.On(tx.tx).Set(LogProvider, providerID).Set(LogAt, c.At.Unix()).
				Set(LogTexts, int64(c.Texts)).Set(LogTokens, int64(c.Tokens)).Set(LogMillis, c.Millis).
				Set(LogOutcome, c.Outcome).Insert(); err != nil {
				return err
			}
		}
		// the oldest beyond the last logKept go
		old, err := tx.t.calls.On(tx.tx).With(LogProvider, providerID).OrderBy(dao.Desc(ByKey)).
			Offset(logKept).Limit(1).Select(LogID)
		if err != nil || len(old) == 0 {
			return err
		}
		return tx.t.calls.On(tx.tx).With(LogProvider, providerID).
			WithPredicate(dao.Cmp(dao.T("embedding_log", LogID), dao.OpLte, dao.Int(old[0].ID))).Delete()
	})
}

func (s *Store) addUsage(tx *Tx, providerID int64, c CallRecord) error {
	day := c.At.UTC().Format(time.DateOnly)
	var failed, limited int64
	if c.Failed {
		failed = 1
	}
	if c.Limited {
		limited = 1
	}
	n, err := dao.UpdateAffected(tx.t.usage.On(tx.tx).With(UsageProvider, providerID).With(UsageDay, day).
		Set(UsageRequests, dao.Incr(1)).Set(UsageTexts, dao.Incr(int64(c.Texts))).Set(UsageTokens, dao.Incr(int64(c.Tokens))).
		Set(UsageFailures, dao.Incr(failed)).Set(UsageLimited, dao.Incr(limited)))
	if err != nil || n > 0 {
		return err
	}
	_, err = tx.t.usage.On(tx.tx).Set(UsageProvider, providerID).Set(UsageDay, day).Set(UsageRequests, int64(1)).
		Set(UsageTexts, int64(c.Texts)).Set(UsageTokens, int64(c.Tokens)).Set(UsageFailures, failed).
		Set(UsageLimited, limited).Insert()
	return err
}

// ProviderUsage is the provider's use by day, the latest first, at most days of them.
func (s *Store) ProviderUsage(ctx context.Context, name string, days int) ([]Usage, error) {
	var out []Usage
	err := s.Read(ctx, func(tx *Tx) error {
		id, err := s.providerID(tx, name)
		if err != nil {
			return err
		}
		rows, err := tx.t.usage.On(tx.tx).With(UsageProvider, id).OrderBy(dao.Desc(ByKey)).Limit(uint64(max(days, 1))).Select()
		for _, r := range rows {
			out = append(out, *r)
		}
		return err
	})
	return out, err
}

// ProviderLog is the provider's recent calls, the latest first, at most limit of them.
func (s *Store) ProviderLog(ctx context.Context, name string, limit int) ([]LogEntry, error) {
	var out []LogEntry
	err := s.Read(ctx, func(tx *Tx) error {
		id, err := s.providerID(tx, name)
		if err != nil {
			return err
		}
		rows, err := tx.t.calls.On(tx.tx).With(LogProvider, id).OrderBy(dao.Desc(ByKey)).Limit(uint64(max(limit, 1))).Select()
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

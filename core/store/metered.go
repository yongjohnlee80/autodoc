package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/yongjohnlee80/golib/dao"
)

// METERED SERVICES — the models the daemon calls and meters: the embedding providers and the
// rankers. Each kind is one table of rows with unique names. A row's API key is sealed for that
// row alone. A preference names the one in use, and follows a rename and a removal. Every call is
// metered, by day and in a log. metered is that repository, written once. providers.go and
// rankers.go keep what is their own: the spec, its check, and the columns it sets.

// meteredSetter sets a spec's own columns on a row being added or rewritten.
type meteredSetter[R any, F ~string] func(dao.DAO[R, F, int64]) dao.DAO[R, F, int64]

// metered is one kind of metered service over s.
type metered[R any, F ~string] struct {
	s      *Store
	schema func(*tables) *dao.Schema[R, F, noSort, int64]
	meter  func(*tables) meterTables
	// pref is the preference naming the one in use; purpose binds a sealed key to its kind (the
	// row's id follows it). Both are what the store already holds: a sealed key is opened with the
	// purpose it was sealed for.
	pref, purpose string
	// none is a name no row has; taken is a name another row has
	none, taken error
	id          func(R) int64
	key         func(R) []byte
}

// col is the column every metered table names alike.
func (metered[R, F]) col(name string) F { return F(name) }

func (m metered[R, F]) on(tx *Tx) dao.DAO[R, F, int64] { return m.schema(tx.t).On(tx.tx) }

// list is every row, by name.
func (m metered[R, F]) list(ctx context.Context) ([]R, error) {
	var out []R
	err := m.s.Read(ctx, func(tx *Tx) error {
		var err error
		out, err = m.on(tx).OrderBy(dao.Asc(ByKey)).Select()
		return err
	})
	return out, err
}

// add records a row with set's columns and its key sealed, and returns its id.
func (m metered[R, F]) add(tx *Tx, set meteredSetter[R, F], key *string) (int64, error) {
	now := tx.now.Now().Unix()
	id, err := set(m.on(tx)).Set(m.col("created_at"), now).Set(m.col("updated_at"), now).Insert()
	if err != nil {
		return 0, m.isTaken(err)
	}
	return id, m.setKey(tx, id, key)
}

// update rewrites the row named name with set's columns (its name among them: to) and its key;
// the preference naming it follows the rename. It returns the row's id.
func (m metered[R, F]) update(tx *Tx, name, to string, set meteredSetter[R, F], key *string) (int64, error) {
	id, err := m.idOf(tx, name)
	if err != nil {
		return 0, err
	}
	if err := set(m.on(tx).With(m.col("id"), id)).Set(m.col("updated_at"), tx.now.Now().Unix()).Update(); err != nil {
		return 0, m.isTaken(err)
	}
	if err := m.setKey(tx, id, key); err != nil {
		return 0, err
	}
	return id, m.follow(tx, name, to)
}

// remove deletes the row named name, and with it its usage and its log; the preference naming it
// names none. It returns the row as it was.
func (m metered[R, F]) remove(tx *Tx, name string) (R, error) {
	r, err := m.byName(tx, name)
	if err != nil {
		return r, err
	}
	if err := m.on(tx).With(m.col("id"), m.id(r)).Delete(); err != nil {
		return r, err
	}
	return r, m.follow(tx, name, "")
}

// get is the row with id.
func (m metered[R, F]) get(tx *Tx, id int64) (R, error) { return m.on(tx).With(m.col("id"), id).Get() }

// byName is the row named name.
func (m metered[R, F]) byName(tx *Tx, name string) (R, error) {
	r, err := m.on(tx).With(m.col("name"), name).Get()
	if errors.Is(err, dao.ErrNoRows) {
		return r, fmt.Errorf("%w: %s", m.none, name)
	}
	return r, err
}

// idOf is the id of the row named name.
func (m metered[R, F]) idOf(tx *Tx, name string) (int64, error) {
	r, err := m.on(tx).With(m.col("name"), name).Get(m.col("id"))
	if errors.Is(err, dao.ErrNoRows) {
		return 0, fmt.Errorf("%w: %s", m.none, name)
	}
	if err != nil {
		return 0, err
	}
	return m.id(r), nil
}

// withKey is the row named name and its key, opened: for the daemon, which calls the service, and
// never for a client.
func (m metered[R, F]) withKey(ctx context.Context, name string) (R, string, error) {
	var r R
	err := m.s.Read(ctx, func(tx *Tx) error {
		var err error
		r, err = m.byName(tx, name)
		return err
	})
	if err != nil || len(m.key(r)) == 0 {
		return r, "", err
	}
	key, err := m.s.keys.open(m.key(r), m.aad(m.id(r)))
	if err != nil {
		var zero R
		return zero, "", err
	}
	return r, string(key), nil
}

// aad binds a sealed key to its kind and its row.
func (m metered[R, F]) aad(id int64) string { return m.purpose + strconv.FormatInt(id, 10) }

// setKey seals key into the row; nil leaves it, "" removes it.
func (m metered[R, F]) setKey(tx *Tx, id int64, key *string) error {
	if key == nil {
		return nil
	}
	var sealed any
	if *key != "" {
		b, err := m.s.keys.seal([]byte(*key), m.aad(id))
		if err != nil {
			return err
		}
		sealed = b
	}
	return m.on(tx).With(m.col("id"), id).Set(m.col("api_key"), sealed).Update()
}

// follow makes the preference naming the one in use from name to instead ("" for removed: none in
// use), in the transaction that renames or removes it, so the preference never names a row the
// store does not have.
func (m metered[R, F]) follow(tx *Tx, from, to string) error {
	if from == to {
		return nil
	}
	return tx.t.preferences.On(tx.tx).With(PrefName, m.pref).With(PrefValue, from).
		Set(PrefValue, to).Set(PrefUpdatedAt, tx.now.Now().Unix()).Update()
}

// isTaken is err as the name another row has, when it is that.
func (m metered[R, F]) isTaken(err error) error {
	if errors.Is(err, dao.ErrDuplicate) {
		return fmt.Errorf("%w: %w", m.taken, err)
	}
	return err
}

// record adds calls to the row's usage by day and to its log, which keeps the last logKept.
func (m metered[R, F]) record(ctx context.Context, id int64, calls []CallRecord) error {
	return m.s.recordCalls(ctx, m.meter, id, calls)
}

// usage is the named row's use by day, the latest first, at most days of them.
func (m metered[R, F]) usage(ctx context.Context, name string, days int) ([]Usage, error) {
	return m.s.usageOf(ctx, m.meter, m.idOf, name, days)
}

// log is the named row's recent calls, the latest first, at most limit of them.
func (m metered[R, F]) log(ctx context.Context, name string, limit int) ([]LogEntry, error) {
	return m.s.logOf(ctx, m.meter, m.idOf, name, limit)
}

// providers is the embedding providers' repository.
func (s *Store) providers() metered[*Provider, ProviderField] {
	return metered[*Provider, ProviderField]{s: s,
		schema:  func(t *tables) *dao.Schema[*Provider, ProviderField, noSort, int64] { return t.providers },
		meter:   (*tables).embeddingMeter,
		pref:    PrefProvider,
		purpose: "autodoc:embedding_provider.api_key:",
		none:    ErrNoProvider, taken: ErrProviderTaken,
		id: func(p *Provider) int64 { return p.ID }, key: func(p *Provider) []byte { return p.APIKey }}
}

// rankers is the rankers' repository.
func (s *Store) rankers() metered[*Ranker, RankerField] {
	return metered[*Ranker, RankerField]{s: s,
		schema:  func(t *tables) *dao.Schema[*Ranker, RankerField, noSort, int64] { return t.rankers },
		meter:   (*tables).rankerMeter,
		pref:    PrefRanker,
		purpose: "autodoc:ranker.api_key:",
		none:    ErrNoRanker, taken: ErrRankerTaken,
		id: func(r *Ranker) int64 { return r.ID }, key: func(r *Ranker) []byte { return r.APIKey }}
}

package store

import (
	"context"
	"errors"
	"time"

	"github.com/yongjohnlee80/golib/dao"
)

// PREFERENCES — what a client keeps between runs, by name: the store's, not a
// workspace's, so they are the same whichever workspace is open.

// ErrNoPreferenceName is a preference with no name.
var ErrNoPreferenceName = errors.New("store: a preference needs a name")

// Preferences are every preference the store holds, by name.
func (s *Store) Preferences(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	err := s.Read(ctx, func(tx *Tx) error {
		rows, err := tx.t.preferences.On(tx.tx).OrderBy(dao.Asc(ByKey)).Select()
		for _, r := range rows {
			out[r.Name] = r.Value
		}
		return err
	})
	return out, err
}

// SetPreference keeps value under name, replacing what it held.
func (s *Store) SetPreference(ctx context.Context, name, value string) error {
	if name == "" {
		return ErrNoPreferenceName
	}
	return s.Write(ctx, func(tx *Tx) error {
		return tx.t.preferences.On(tx.tx).Set(PrefName, name).Set(PrefValue, value).
			Set(PrefUpdatedAt, time.Now().Unix()).Upsert()
	})
}

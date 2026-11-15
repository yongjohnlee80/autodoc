//go:build !server

package store

import "context"

// pgStore is a store on PostgreSQL's own state; a build without the server tag has none, so a
// Store's pg is always nil here.
type pgStore struct{ loc Location }

func (pg *pgStore) close() error { return nil }

// openPostgres is ErrNoPostgresInThisBuild: the PostgreSQL store is the server build's.
func openPostgres(context.Context, Location, ...Option) (*Store, error) {
	return nil, ErrNoPostgresInThisBuild
}

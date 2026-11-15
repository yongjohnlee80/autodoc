package store

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/golib/errs"
)

// A STORE'S LOCATION, on either engine (docs/decisions/store.md#the-store-on-postgresql). The
// PostgreSQL store itself is the server build's (postgres.go, -tags server): a build without the
// tag has no PostgreSQL driver at all, and OpenAt answers ErrNoPostgresInThisBuild for a PostgreSQL
// Location (postgres_off.go).

// The engines a Location names.
const (
	EngineOfSQLite   = "sqlite"
	EngineOfPostgres = "postgres"
)

// ErrNotSQLite is an operation only a SQLite store's file has, asked of a store on PostgreSQL.
var ErrNotSQLite = errs.Sentinel(errs.ErrUnsupported, "store: only a SQLite store has this")

// Location is where a store is: a SQLite file (Engine "sqlite", or "": Path), or a PostgreSQL
// database (Engine "postgres": DSN, Schema and KeyFile).
type Location struct {
	Engine string
	// Path is a SQLite store's file.
	Path string
	// DSN is a PostgreSQL store's database; Schema the schema its tables live in, made when missing.
	DSN, Schema string
	// KeyFile is a PostgreSQL store's key, which seals its secrets (a SQLite store's is Path+".key").
	KeyFile string
}

// LeaseTable is the table a PostgreSQL store's writer lease is a row of (000036), and LeaseScope
// its row.
const (
	LeaseTable = "writer_lease"
	LeaseScope = "store"
)

// ErrNoPostgresInThisBuild is OpenAt of a PostgreSQL Location in a build without the server tag,
// which carries no PostgreSQL driver.
var ErrNoPostgresInThisBuild = errs.Sentinel(errs.ErrUnsupported, "store: this build has no PostgreSQL store: use the server build (-tags server)")

// OpenAt opens the store at loc, as Open opens a SQLite file: it takes the store's lease (a live
// one is ErrBusy), opens it and brings its schema up to date. A PostgreSQL Location needs the
// server build; any other answers ErrNoPostgresInThisBuild.
func OpenAt(ctx context.Context, loc Location, opts ...Option) (*Store, error) {
	switch loc.Engine {
	case "", EngineOfSQLite:
		return Open(ctx, loc.Path, opts...)
	case EngineOfPostgres:
		return openPostgres(ctx, loc, opts...)
	}
	return nil, fmt.Errorf("store: no engine %q (sqlite or postgres)", loc.Engine)
}

// Location is where the store is.
func (s *Store) Location() Location {
	if s.pg != nil {
		return s.pg.loc
	}
	return Location{Engine: EngineOfSQLite, Path: s.path}
}

// sqliteOnly is ErrNotSQLite for a store on PostgreSQL, nil for a SQLite one.
func (s *Store) sqliteOnly() error {
	if s.pg != nil {
		return ErrNotSQLite
	}
	return nil
}

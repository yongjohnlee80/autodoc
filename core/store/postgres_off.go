//go:build !server

package store

import (
	"context"
	"time"

	"github.com/yongjohnlee80/golib/dao"
)

// pgStore is a store on PostgreSQL's own state; a build without the server tag has none, so a
// Store's pg is always nil here.
type pgStore struct {
	loc Location
	id  string
}

// leaseTTL is a PostgreSQL store's lease length: none without the server build, where OpenAt of a
// PostgreSQL Location is ErrNoPostgresInThisBuild at once and OpenAtWaiting never waits.
var leaseTTL time.Duration

func (pg *pgStore) close() error { return nil }

// fence is ErrNoPostgresInThisBuild: there is no lease without the server build.
func (pg *pgStore) fence(context.Context, dao.Querier) error { return ErrNoPostgresInThisBuild }

// openPostgres is ErrNoPostgresInThisBuild: the PostgreSQL store is the server build's.
func openPostgres(context.Context, Location, ...Option) (*Store, error) {
	return nil, ErrNoPostgresInThisBuild
}

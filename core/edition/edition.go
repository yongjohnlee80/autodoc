// Package edition is what this build of AutoDoc offers beyond its core: the one place a Community
// build and a Pro build differ (ADR 0214 §8).
//
// Databases is the database features: a workspace's source database, where its .view files run,
// and its destination, Postgres with pgvector instead of the local store. When it is off, the
// daemon refuses those settings and sys.capabilities says so, and the TUI hides them. Flipping it
// changes no schema and no protocol.
package edition

import (
	"fmt"

	"github.com/yongjohnlee80/golib/errs"
)

// Databases offers a workspace's source and destination databases.
//
// TODO: on until the Community and Pro split is decided; then the Community build sets it false.
const Databases = true

// ErrDatabases is a database setting refused by a build without Databases; it is
// errs.ErrUnsupported.
var ErrDatabases = fmt.Errorf("%w: this edition has no database settings", errs.ErrUnsupported)

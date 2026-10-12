package pgstore

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/errs"
)

// A pgstore in a schema of its own, beside a business database's tables: its tables, its
// rag_english configuration and its migration ledger are created and found through the
// connection's search_path, never by a qualified name. A pool's queries run on whichever physical
// connection is free, so a search_path set once, on one connection, leaves the others in public:
// WithSchema sets it on every physical connection the pool opens, before that connection serves.

// ErrSchemaName is a schema name that is not a plain identifier.
var ErrSchemaName = errs.Sentinel(errs.ErrInvalidArgument, "pgstore: not a plain schema name")

// plainIdent is a plain PostgreSQL identifier as an unquoted name folds: lowercase ASCII letters,
// digits and underscores, not starting with a digit, at most 63 bytes (NAMEDATALEN - 1).
var plainIdent = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// checkSchema refuses a schema name that is not a plain identifier, or one in the pg_ namespace
// PostgreSQL reserves for its own schemas.
func checkSchema(schema string) error {
	if !plainIdent.MatchString(schema) || strings.HasPrefix(schema, "pg_") {
		return fmt.Errorf("%w: %q (lowercase letters, digits and _, not starting with a digit or pg_, at most 63)", ErrSchemaName, schema)
	}
	return nil
}

// WithSchema is the pool option that runs SET search_path TO schema, public on every physical
// connection the pool opens, replacements included, before the connection serves any query: so
// Migrate, the ledger and every Store query run in schema, whichever connection serves them.
// public stays on the path for the vector extension, which is usually installed there (000001).
//
// A name that is not a plain identifier fails every connect with ErrSchemaName, and so every
// query; check it first with EnsureSchema. The schema must exist before Migrate (EnsureSchema):
// PostgreSQL creates an object in the first schema of the path that exists, so in a missing one's
// place it would create the tables in public.
func WithSchema(schema string) postgres.Option {
	return postgres.WithConnectHook(func(ctx context.Context, c dao.ConnectedConn) error {
		if err := checkSchema(schema); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, "SET search_path TO "+quoteIdent(schema)+", public"); err != nil {
			return fmt.Errorf("pgstore: set search_path to %s: %w", schema, err)
		}
		return nil
	})
}

// EnsureSchema checks schema is a plain identifier (ErrSchemaName otherwise, before any I/O) and
// creates it on conn if it does not exist. The connection's role needs CREATE on the database the
// first time, and nothing after.
func EnsureSchema(ctx context.Context, conn dao.DataConn, schema string) error {
	if err := checkSchema(schema); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoteIdent(schema)); err != nil {
		return fmt.Errorf("pgstore: create schema %s: %w", schema, err)
	}
	return nil
}

// quoteIdent quotes a name checkSchema admitted: it holds no quote to escape.
func quoteIdent(name string) string { return `"` + name + `"` }

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

// CheckSchema refuses (ErrSchemaName) a schema name that is not a plain identifier, or one in the
// pg_ namespace PostgreSQL reserves for its own schemas: the check EnsureSchema and WithSchema make,
// for a caller validating its configuration before it opens anything.
func CheckSchema(schema string) error {
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
		if err := CheckSchema(schema); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, "SET search_path TO "+quoteIdent(schema)+", public"); err != nil {
			return fmt.Errorf("pgstore: set search_path to %s: %w", schema, err)
		}
		return nil
	})
}

// EnsureSchema checks schema is a plain identifier (ErrSchemaName otherwise, before any I/O) and
// creates it on conn if it does not exist. A schema that exists is left as it is, with no CREATE
// statement: PostgreSQL checks the database's CREATE privilege before it looks for the schema,
// even under IF NOT EXISTS, so a role a DBA gave a schema of its own, and no CREATE on the
// database, could not run one. Such a role needs nothing more here; one creating its schema needs
// CREATE on the database.
func EnsureSchema(ctx context.Context, conn dao.DataConn, schema string) error {
	if err := CheckSchema(schema); err != nil {
		return err
	}
	rows, err := conn.QueryContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", schema)
	if err != nil {
		return fmt.Errorf("pgstore: look for schema %s: %w", schema, err)
	}
	var exists bool
	if rows.Next() {
		err = rows.Scan(&exists)
	}
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("pgstore: look for schema %s: %w", schema, err)
	}
	if exists {
		return nil
	}
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoteIdent(schema)); err != nil {
		return fmt.Errorf("pgstore: create schema %s: %w", schema, err)
	}
	return nil
}

// quoteIdent quotes a name CheckSchema admitted: it holds no quote to escape.
func quoteIdent(name string) string { return `"` + name + `"` }

// IndexDatabase is what a database holds for an index to live in it, as its role sees it.
type IndexDatabase struct {
	Vector           string // the vector extension's installed version; "" when not installed
	VectorAvailable  bool   // the server has the extension to install
	CanInstallVector bool   // the role may install it: a superuser, or a trusted extension and CREATE on the database
	SchemaExists     bool   // the index's schema is there
	CanCreate        bool   // the role may create tables in the schema or, when it is missing, create it
}

// indexDatabaseQuery reads an IndexDatabase, read-only; $1 is the schema.
const indexDatabaseQuery = `SELECT
	coalesce((SELECT extversion FROM pg_extension WHERE extname = 'vector'), ''),
	EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector'),
	(SELECT rolsuper FROM pg_roles WHERE rolname = current_user)
		OR (coalesce((SELECT bool_or(trusted) FROM pg_available_extension_versions WHERE name = 'vector'), false)
			AND has_database_privilege(current_database(), 'CREATE')),
	EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1),
	CASE WHEN EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)
		THEN has_schema_privilege($1, 'CREATE')
		ELSE has_database_privilege(current_database(), 'CREATE') END`

// ReadIndexDatabase reads, without writing anything, whether conn's database can hold an index in
// schema: pgvector installed or installable by the role, and the schema's tables, or the schema,
// creatable by it. A server's --check asks it of an index's database of its own.
func ReadIndexDatabase(ctx context.Context, conn dao.DataConn, schema string) (IndexDatabase, error) {
	var d IndexDatabase
	if err := CheckSchema(schema); err != nil {
		return d, err
	}
	rows, err := conn.QueryContext(ctx, indexDatabaseQuery, schema)
	if err != nil {
		return d, fmt.Errorf("pgstore: read the index's database: %w", err)
	}
	if err := scanRows(rows, &d.Vector, &d.VectorAvailable, &d.CanInstallVector, &d.SchemaExists, &d.CanCreate); err != nil {
		return d, fmt.Errorf("pgstore: read the index's database: %w", err)
	}
	return d, nil
}

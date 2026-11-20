package pgstore

import (
	"context"
	"net/url"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
)

// layout is how a cell's index database is set up: the scratch database's owner in its public
// schema, or a login that owns only a schema of its own.
type layout struct {
	name string
	open func(t *testing.T, seqscanOff bool) dao.DataConn
}

// layouts are both: a cell that runs over them holds for the database a server's index has to
// itself as for the one it shares with the views.
var layouts = []layout{
	{"one database", func(t *testing.T, seqscanOff bool) dao.DataConn {
		if seqscanOff {
			return indexedScratch(t)
		}
		return scratch(t)
	}},
	{"a schema-only login", schemaLogin},
}

// schemaLogin is a scratch database set up as an index's database of its own is (the operator's
// guide): the database and pgvector are the admin's, CREATE on public is nobody's, and the login
// the store connects as has CONNECT and owns one schema, nothing else. The store is migrated
// through that login, in that schema. seqscanOff turns sequential scans off on its connections,
// as indexedScratch does, for a cell that reads a plan.
func schemaLogin(t *testing.T, seqscanOff bool) dao.DataConn {
	t.Helper()
	ctx := context.Background()
	admin, err := postgres.Open(ctx, dsnBase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	login, password := "autodoc_login_"+randHex(t), randHex(t)
	// registered before the database's own drop, so it runs after it: the login owns objects there
	t.Cleanup(func() { admin.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+login) })
	name := scratchName(t, admin, "autodoc_pgstore_")
	owner, err := postgres.Open(ctx, dsnOf(t, admin, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	schema := "idx"
	for _, q := range []string{
		"CREATE EXTENSION IF NOT EXISTS vector",
		"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
		"REVOKE CONNECT, TEMPORARY ON DATABASE " + name + " FROM PUBLIC",
		"CREATE ROLE " + login + " LOGIN PASSWORD '" + password + "'",
		"GRANT CONNECT ON DATABASE " + name + " TO " + login,
		"CREATE SCHEMA " + schema + " AUTHORIZATION " + login,
	} {
		exec(t, owner, q)
	}
	u, err := url.Parse(dsnOf(t, admin, name))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(login, password)
	if seqscanOff {
		q := u.Query()
		q.Set("enable_seqscan", "off")
		u.RawQuery = q.Encode()
	}
	conn, err := postgres.Open(ctx, u.String(), WithSchema(schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := EnsureSchema(ctx, conn, schema); err != nil {
		t.Fatalf("the login's own schema: %v", err)
	}
	if err := Migrate(ctx, conn); err != nil {
		t.Fatalf("migrate through the schema-only login: %v", err)
	}
	if got := scalar(t, conn, "SELECT has_database_privilege(current_database(), 'CREATE')::text"); got != "false" {
		t.Fatalf("the login may create in the database (%s): the layout is not the one meant", got)
	}
	return conn
}

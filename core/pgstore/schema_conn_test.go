package pgstore

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"
)

// Under a pool of several physical connections opened WithSchema, on a database whose pgvector is
// already installed in public, EnsureSchema then Migrate put every rag_* table, the rag_english
// configuration and the ledger in the schema and none in public; transactions held at once on
// every connection of the pool all run in the schema; and the Store writes and reads there. The
// mutants, named here: a hook that sets nothing (the tables land in public), search_path SET once
// on the pool instead of on every connection (the connections opened after it run in public), and
// a path without public (the vector type is not found).
func TestWithSchemaPutsEveryTableInItOnEveryConnection(t *testing.T) {
	ctx := context.Background()
	admin, err := postgres.Open(ctx, dsnBase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := scratchName(t, admin, "autodoc_pgstore_")
	schema := "search_" + randHex(t)
	const n = 4
	conn, err := postgres.OpenNamed(ctx, name, dsnOf(t, admin, name), WithSchema(schema), postgres.MaxOpenConns(n))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	// the business database already has pgvector, in public: the schema's pool finds it there
	plain, err := postgres.OpenNamed(ctx, name, dsnOf(t, admin, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { plain.Close() })
	if _, err := plain.ExecContext(ctx, "CREATE EXTENSION vector SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(ctx, conn, schema); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(ctx, conn, schema); err != nil {
		t.Fatalf("a second EnsureSchema: %v", err)
	}
	if err := Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}

	// n transactions open at once hold n physical connections: each must be in the schema
	type session struct {
		pid    int
		schema string
	}
	var (
		mu       sync.Mutex
		sessions []session
		wg       sync.WaitGroup
		opened   sync.WaitGroup
		release  = make(chan struct{})
	)
	for range n {
		wg.Add(1)
		opened.Add(1)
		go func() {
			defer wg.Done()
			tx, err := conn.Begin(ctx)
			if err != nil {
				opened.Done()
				t.Error(err)
				return
			}
			defer func() { _ = tx.Rollback() }()
			rows, err := tx.QueryContext(ctx, "SELECT pg_backend_pid(), current_schema()")
			var s session
			if err == nil {
				if rows.Next() {
					err = rows.Scan(&s.pid, &s.schema)
				}
				_ = rows.Close()
			}
			opened.Done()
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			sessions = append(sessions, s)
			mu.Unlock()
			<-release
		}()
	}
	opened.Wait()
	close(release)
	wg.Wait()
	pids := map[int]bool{}
	for _, s := range sessions {
		pids[s.pid] = true
		if s.schema != schema {
			t.Errorf("connection %d runs in %q, want %q", s.pid, s.schema, schema)
		}
	}
	if len(pids) != n {
		t.Errorf("%d transactions held %d physical connections, want %d", n, len(pids), n)
	}

	// the catalog, read from a connection of the default search_path, outside the schema's pool
	for q, want := range map[string]string{
		"SELECT string_agg(table_name, ',' ORDER BY table_name) FROM information_schema.tables WHERE table_schema = '" + schema + "'":                                   "autodoc_schema,rag_chunk,rag_cursor,rag_document,rag_embedding,rag_ingest_error,rag_link,rag_meta,rag_tenant_policy",
		"SELECT count(*)::text FROM information_schema.tables WHERE table_schema = 'public' AND (table_name LIKE 'rag!_%' ESCAPE '!' OR table_name = 'autodoc_schema')": "0",
		"SELECT n.nspname FROM pg_ts_config c JOIN pg_namespace n ON n.oid = c.cfgnamespace WHERE c.cfgname = 'rag_english'":                                            schema,
	} {
		if got := scalar(t, plain, q); got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}

	// the Store works through the pool, in the schema
	s, err := New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, Document{Path: "a.md", Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "a", Body: "schema words"}}}); err != nil {
		t.Fatal(err)
	}
	if got := lexicalPaths(t, s, "schema"); strings.Join(got, ",") != "a.md" {
		t.Errorf("the Store in the schema: %v", got)
	}
}

// A schema name that is not a plain identifier is refused before any I/O by EnsureSchema, and
// fails every connect of a pool opened WithSchema. The mutant, named here: EnsureSchema without
// its check — a name with a quote or a capital then reaches PostgreSQL.
func TestASchemaNameIsAPlainIdentifier(t *testing.T) {
	ctx := context.Background()
	for _, bad := range []string{"", "Search", "1abc", "a-b", `x"; DROP SCHEMA public; --`, "pg_mine", "a b",
		strings.Repeat("a", 64)} {
		if err := EnsureSchema(ctx, nil, bad); !errors.Is(err, ErrSchemaName) {
			t.Errorf("EnsureSchema(%q): %v, want ErrSchemaName", bad, err)
		}
	}
	for _, good := range []string{"autodoc_search", "_x", "s1", strings.Repeat("a", 63)} {
		if err := CheckSchema(good); err != nil {
			t.Errorf("CheckSchema(%q): %v, want accepted", good, err)
		}
	}
	admin, err := postgres.Open(ctx, dsnBase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := scratchName(t, admin, "autodoc_pgstore_")
	conn, err := postgres.OpenNamed(ctx, name, dsnOf(t, admin, name), WithSchema("Bad-Name"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.ExecContext(ctx, "SELECT 1"); err == nil || !strings.Contains(err.Error(), "not a plain schema name") {
		t.Errorf("a query on a pool WithSchema(%q): %v, want the connect refused", "Bad-Name", err)
	}
}

// A role a DBA gave a schema of its own, with no CREATE on the database, prepares its store there:
// EnsureSchema finds the schema and creates nothing, and Migrate runs in it, pgvector being already
// installed in public. The mutant, named here: EnsureSchema running CREATE SCHEMA IF NOT EXISTS
// when the schema exists (PostgreSQL refuses it: permission denied for the database).
func TestASchemaOfItsOwnNeedsNoCreateOnTheDatabase(t *testing.T) {
	ctx := context.Background()
	admin, err := postgres.Open(ctx, dsnBase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	role := "autodoc_pgstore_role_" + randHex(t)
	if _, err := admin.ExecContext(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD 'scratch'"); err != nil {
		t.Fatal(err)
	}
	// dropped after the database that holds its schema, which the next cleanup drops first
	t.Cleanup(func() { admin.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+role) })
	name := scratchName(t, admin, "autodoc_pgstore_")
	owner, err := postgres.OpenNamed(ctx, name, dsnOf(t, admin, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	schema := "search_" + randHex(t)
	for _, q := range []string{"CREATE EXTENSION vector SCHEMA public", "CREATE SCHEMA " + schema + " AUTHORIZATION " + role} {
		if _, err := owner.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	u, err := url.Parse(dsnOf(t, admin, name))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, "scratch")
	conn, err := postgres.OpenNamed(ctx, name, u.String(), WithSchema(schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := EnsureSchema(ctx, conn, schema); err != nil {
		t.Fatalf("EnsureSchema as the schema's owner, with no CREATE on the database: %v", err)
	}
	if err := Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := scalar(t, owner, "SELECT count(*)::text FROM information_schema.tables WHERE table_schema = '"+schema+"'"); got != "9" {
		t.Errorf("%s tables in the schema, want 9", got)
	}
	// a schema it may not create is still refused, as PostgreSQL says
	if err := EnsureSchema(ctx, conn, "other_"+randHex(t)); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("EnsureSchema of a schema the role may not create: %v", err)
	}
}

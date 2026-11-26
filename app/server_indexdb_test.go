//go:build server

package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"

	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

// readOnlySource makes the fixture's source database readable only, by a role of the test's own
// that may SELECT the track table and nothing more, and points AUTODOC_TEST_SERVER_DSN at it. It
// returns that role's DSN; the role is dropped when the test ends.
func readOnlySource(t *testing.T, f *serverFixture) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	role := "autodoc_ro_" + hex.EncodeToString(b)
	password := "ro-" + hex.EncodeToString(b) + "-password"
	f.exec(`CREATE ROLE ` + role + ` LOGIN PASSWORD '` + password + `'`)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = f.plain.ExecContext(ctx, `DROP OWNED BY `+role)
		_, _ = f.plain.ExecContext(ctx, `DROP ROLE IF EXISTS `+role)
	})
	f.exec(`REVOKE CREATE ON SCHEMA public FROM PUBLIC`)
	f.exec(`GRANT USAGE ON SCHEMA public TO ` + role)
	f.exec(`GRANT SELECT ON track TO ` + role)
	u, err := url.Parse(os.Getenv("AUTODOC_TEST_SERVER_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, password)
	t.Setenv("AUTODOC_TEST_SERVER_DSN", u.String())
	return u.String()
}

// indexDatabase is a second scratch database for the index, named to the fixture's server.toml as
// [postgres] index_dsn_env = "AUTODOC_TEST_INDEX_DSN", and a pool on it.
func indexDatabase(t *testing.T, f *serverFixture) dao.DataConn {
	t.Helper()
	dsn := pgtest.ScratchURL(t)
	t.Setenv("AUTODOC_TEST_INDEX_DSN", dsn)
	conn, err := postgres.Open(f.ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f.pg = "index_dsn_env = \"AUTODOC_TEST_INDEX_DSN\"\n"
	f.configure("fake-model", f.url)
	return conn
}

func scalarOn(t *testing.T, conn dao.DataConn, q string) string {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var s string
	if !rows.Next() {
		t.Fatalf("%s: no row", q)
	}
	if err := rows.Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// With [postgres] index_dsn_env, the index lives in a database of its own and the source is only
// read: the server runs with a source role that may SELECT the view's table and nothing else, its
// schema, documents, vectors and writer lease are in the index's database, and the source database
// has no schema of the server's. A row added to the source is indexed at the next pass. The
// mutant, named here: the index opened on the source's DSN (the read-only role can't create the
// schema: the server stops at its migrations).
func TestTheIndexLivesInADatabaseOfItsOwnAndTheSourceIsOnlyRead(t *testing.T) {
	f := newServerFixture(t, serverView)
	readOnlySource(t, f)
	idx := indexDatabase(t, f)
	ctx, cancel := context.WithCancel(f.ctx)
	done, stderr := f.serve(ctx)
	f.waitFor("the server serving, or stopped", func() bool {
		select {
		case code := <-done:
			t.Fatalf("--server stopped, exit %d: %s", code, stderr)
		default:
		}
		return strings.Contains(stderr.String(), "serving enterprise search")
	})
	count := func() string {
		return scalarOn(t, idx, `SELECT CASE WHEN to_regclass('`+f.schema+`.rag_document') IS NULL THEN '0'
			ELSE (xpath('/row/c/text()', query_to_xml('SELECT count(*) AS c FROM `+f.schema+`.rag_document', false, true, '')))[1]::text END`)
	}
	f.waitFor("both rows indexed in the index's database", func() bool { return count() == "2" })
	f.waitFor("both rows embedded in the index's database", func() bool {
		return scalarOn(t, idx, `SELECT (NOT EXISTS (SELECT 1 FROM `+f.schema+`.rag_document WHERE NOT semantic_ready))::text`) == "true"
	})
	if n := scalarOn(t, idx, `SELECT count(*)::text FROM `+f.schema+`.rag_writer_lease WHERE holder <> ''`); n != "1" {
		t.Errorf("%s writer leases held in the index's database, want the tenant's", n)
	}
	if n := f.scalar(`SELECT count(*)::text FROM pg_namespace WHERE nspname = '` + f.schema + `'`); n != "0" {
		t.Errorf("the source database has the server's schema")
	}
	f.exec(`INSERT INTO track VALUES (3, 'A', 'osprey song', now() - interval '1 minute')`)
	f.waitFor("the new row indexed", func() bool { return count() == "3" })
	cancel()
	if code := exitOf(t, done, stderr); code != 0 {
		t.Fatalf("--server exited %d: %s", code, stderr)
	}
}

// --server --check with an index database of its own checks it too, read-only, and says what it
// found: pgvector installed (or installable by the role) and the schema to be created. An index
// role that can neither install pgvector nor create the schema is refused, exit 2, naming
// postgres.index_dsn_env, with no DSN or password in what it says. The mutant, named here: the
// index's database not checked (--check says ok, and the server would stop at its migrations).
func TestTheServerChecksTheIndexDatabase(t *testing.T) {
	f := newServerFixture(t, serverView)
	ro := readOnlySource(t, f)
	idx := indexDatabase(t, f)
	var stdout, stderr bytes.Buffer
	if code := run(f.ctx, []string{"--server", "--check", f.path}, testOptions, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "index: a database of its own: vector") ||
		!strings.Contains(stdout.String(), "schema "+f.schema+" created at the first start") ||
		!strings.Contains(stdout.String(), "order confirmed over 2 rows") {
		t.Fatalf("--check: exit %d, said %q %q", code, stdout.String(), stderr.String())
	}
	if n := scalarOn(t, idx, `SELECT count(*)::text FROM pg_namespace WHERE nspname = '`+f.schema+`'`); n != "0" {
		t.Error("--check created the index's schema")
	}
	// the index's database, through the read-only role: no pgvector it may install, no schema it
	// may create
	u, err := url.Parse(os.Getenv("AUTODOC_TEST_INDEX_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := url.Parse(ro)
	u.User = r.User
	if _, err := idx.ExecContext(f.ctx, `REVOKE CREATE ON SCHEMA public FROM PUBLIC`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTODOC_TEST_INDEX_DSN", u.String())
	stdout.Reset()
	stderr.Reset()
	code := run(f.ctx, []string{"--server", "--check", f.path}, testOptions, &stdout, &stderr)
	said := stdout.String() + stderr.String()
	if code != 2 || !strings.Contains(stderr.String(), "postgres.index_dsn_env") {
		t.Fatalf("--check of an index role that can't make the index: exit %d, said %q", code, said)
	}
	pw, _ := r.User.Password()
	if strings.Contains(said, pw) || strings.Contains(said, u.String()) {
		t.Errorf("--check shows the index's DSN or its password: %q", said)
	}
}

// hardenedIndexRole makes the index's database as a DBA who trusts no default does: pgvector
// installed, a login role of the test's own that owns the index's schema there and nothing else,
// and TEMPORARY revoked from PUBLIC. It points AUTODOC_TEST_INDEX_DSN at that role, and returns
// the role, its password and the database's name; the role is dropped when the test ends.
func hardenedIndexRole(t *testing.T, f *serverFixture, idx dao.DataConn) (role, password, database string) {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	role = "autodoc_ix_" + hex.EncodeToString(b)
	password = "ix-" + hex.EncodeToString(b) + "-password"
	database = scalarOn(t, idx, `SELECT current_database()::text`)
	f.exec(`CREATE ROLE ` + role + ` LOGIN PASSWORD '` + password + `'`)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = idx.ExecContext(ctx, `DROP OWNED BY `+role)
		_, _ = f.plain.ExecContext(ctx, `DROP ROLE IF EXISTS `+role)
	})
	for _, q := range []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		`REVOKE CONNECT, TEMPORARY ON DATABASE ` + database + ` FROM PUBLIC`,
		`GRANT CONNECT ON DATABASE ` + database + ` TO ` + role,
		`CREATE SCHEMA ` + f.schema + ` AUTHORIZATION ` + role,
	} {
		if _, err := idx.ExecContext(f.ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	u, err := url.Parse(os.Getenv("AUTODOC_TEST_INDEX_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, password)
	t.Setenv("AUTODOC_TEST_INDEX_DSN", u.String())
	return role, password, database
}

// An index role that can't create temporary tables is refused by --check, exit 2, naming
// postgres.index_dsn_env and the grant: a database whose owner revoked TEMPORARY from PUBLIC, with
// everything else the index needs in place (pgvector, a schema the role owns). An index build
// makes a temporary table, so without the grant the server starts and searches, and the
// approximate search's index fails to build once a tenant is large enough to want it, long after
// the grants were made. With the grant, --check passes. The mutants, named here: the temporary
// tables not checked (--check says ok); the database's CREATE read in place of TEMPORARY (the
// grant made, --check still refuses).
func TestTheServerChecksTheIndexRoleCanMakeTemporaryTables(t *testing.T) {
	f := newServerFixture(t, serverView)
	readOnlySource(t, f)
	idx := indexDatabase(t, f)
	role, password, database := hardenedIndexRole(t, f, idx)
	check := func() (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := run(f.ctx, []string{"--server", "--check", f.path}, testOptions, &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	code, stdout, stderr := check()
	if code != 2 || !strings.Contains(stderr, "postgres.index_dsn_env") || !strings.Contains(stderr, "temporary tables") ||
		!strings.Contains(stderr, "GRANT TEMPORARY ON DATABASE") {
		t.Fatalf("--check of an index role that can't make temporary tables: exit %d, said %q %q", code, stdout, stderr)
	}
	if said := stdout + stderr; strings.Contains(said, password) {
		t.Errorf("--check shows the index role's password: %q", said)
	}
	if _, err := idx.ExecContext(f.ctx, `GRANT TEMPORARY ON DATABASE `+database+` TO `+role); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = check()
	if code != 0 || !strings.Contains(stdout, "index: a database of its own: vector") || !strings.Contains(stdout, "schema "+f.schema+"\n") ||
		!strings.Contains(stdout, "order confirmed over 2 rows") {
		t.Fatalf("--check with the grant made: exit %d, said %q %q", code, stdout, stderr)
	}
}

// --check says which database didn't answer. With the index in a database of its own there are
// two DSNs, and the driver's words (a user, an address) leave the reader to work out which
// variable to mend: the index's was named, the source's wasn't. Exit 1, the source's database and
// its field named, and neither the DSN nor a password in what it says. The mutant, named here:
// the source's failure passed on as the driver gave it.
func TestTheServerCheckNamesTheSourceDatabaseThatDoesNotAnswer(t *testing.T) {
	f := newServerFixture(t, serverView)
	ro := readOnlySource(t, f)
	indexDatabase(t, f)
	u, err := url.Parse(ro)
	if err != nil {
		t.Fatal(err)
	}
	right, _ := u.User.Password()
	const wrong = "not-the-password-0123456789"
	u.User = url.UserPassword(u.User.Username(), wrong)
	t.Setenv("AUTODOC_TEST_SERVER_DSN", u.String())
	var stdout, stderr bytes.Buffer
	code := run(f.ctx, []string{"--server", "--check", f.path}, testOptions, &stdout, &stderr)
	said := stdout.String() + stderr.String()
	if code != 1 || !strings.Contains(stderr.String(), "the source's database (postgres.dsn_env)") {
		t.Fatalf("--check with a source that refuses the password: exit %d, said %q", code, said)
	}
	if strings.Contains(said, wrong) || strings.Contains(said, right) || strings.Contains(said, u.String()) {
		t.Errorf("--check shows the source's DSN or a password: %q", said)
	}
}

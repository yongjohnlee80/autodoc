package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

// serverView is a source view of the test's business table.
const serverView = `---
version: 1
name: track
source: postgres
args: [after_ts, after_id, upto_ts]
process: |
  SELECT jsonb_build_object('id', id, 'label_id', label_id, 'title', title, 'updated_at', updated_at)
  FROM track
  WHERE (updated_at, id) > ($1, $2) AND updated_at <= $3
  ORDER BY updated_at, id
  LIMIT 100
export: track/{{.id}}.md
---
# {{.title}}
`

// serverFixture is a business database with a track table, a server.toml naming it through
// AUTODOC_TEST_SERVER_DSN, and a pool for looking at what the server did.
type serverFixture struct {
	t      *testing.T
	ctx    context.Context
	path   string // server.toml
	schema string
	plain  dao.DataConn
}

func newServerFixture(t *testing.T, view string) *serverFixture {
	t.Helper()
	dsn := pgtest.ScratchURL(t)
	t.Setenv("AUTODOC_TEST_SERVER_DSN", dsn)
	f := &serverFixture{t: t, ctx: context.Background(), schema: "enterprise_search"}
	plain, err := postgres.Open(f.ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { plain.Close() })
	f.plain = plain
	f.exec(`CREATE TABLE track (id bigint PRIMARY KEY, label_id text, title text, updated_at timestamptz NOT NULL)`)
	f.exec(`INSERT INTO track VALUES (1, 'A', 'kestrel song', now() - interval '1 hour'), (2, 'B', 'heron song', now() - interval '1 hour')`)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "track.view"), []byte(view), 0o600); err != nil {
		t.Fatal(err)
	}
	f.path = filepath.Join(dir, "server.toml")
	cfg := `
[postgres]
dsn_env = "AUTODOC_TEST_SERVER_DSN"
schema  = "` + f.schema + `"

[embedding]
provider = "ollama"
model    = "nomic-embed-text"

[[source]]
tenant   = "lm-catalog"
view     = "track.view"
interval = "1s"
lag      = "0s"
access   = ["label_id"]
`
	if err := os.WriteFile(f.path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *serverFixture) exec(q string) {
	f.t.Helper()
	if _, err := f.plain.ExecContext(f.ctx, q); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

func (f *serverFixture) scalar(q string) string {
	f.t.Helper()
	rows, err := f.plain.QueryContext(f.ctx, q)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var s string
	if !rows.Next() {
		f.t.Fatalf("%s: no row", q)
	}
	if err := rows.Scan(&s); err != nil {
		f.t.Fatal(err)
	}
	return s
}

// documents is how many documents the server wrote, 0 before its schema exists.
func (f *serverFixture) documents() string {
	return f.scalar(`SELECT CASE WHEN to_regclass('` + f.schema + `.rag_document') IS NULL THEN '0'
		ELSE (xpath('/row/c/text()', query_to_xml('SELECT count(*) AS c FROM ` + f.schema + `.rag_document', false, true, '')))[1]::text END`)
}

// locks is how many advisory locks are held in the database.
func (f *serverFixture) locks() string {
	return f.scalar(`SELECT count(*)::text FROM pg_locks WHERE locktype = 'advisory' AND granted`)
}

func (f *serverFixture) waitFor(what string, cond func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			f.t.Fatalf("%s: not within 15 s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// serve runs --server in this process until ctx ends, and returns its exit code on the channel.
func (f *serverFixture) serve(ctx context.Context, args ...string) (<-chan int, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, append([]string{"--server"}, append(args, f.path)...), testOptions, &stdout, &stderr)
	}()
	return done, &stderr
}

func exitOf(t *testing.T, done <-chan int, stderr *bytes.Buffer) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(15 * time.Second):
		t.Fatalf("--server did not stop; it said %s", stderr)
		return -1
	}
}

// TestServerStepsStartInOrder: Postgres opens, the schema is created before pgstore's migrations
// run in it, the tenants' policies are recorded, and the ingesters start last.
func TestServerStepsStartInOrder(t *testing.T) {
	var names []string
	for _, s := range serverSteps {
		names = append(names, s.name)
	}
	if want := []string{"postgres", "schema", "policy", "ingesters"}; !slices.Equal(names, want) {
		t.Fatalf("serverSteps %v, want %v", names, want)
	}
}

// --server refuses what it cannot run, before it opens anything: no configuration (2), --check
// without --server (2), a configuration or a view it refuses (2, naming the field), and a file it
// cannot read (1). A database it cannot reach is 1, and the error never shows the DSN's password.
func TestTheServersRefusals(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("track.view", serverView)
	write("since.view", strings.Replace(serverView, "args: [after_ts, after_id, upto_ts]", "args: [since]", 1))
	cfg := func(view string) string {
		return "[postgres]\ndsn_env = \"AUTODOC_TEST_UNREACHABLE\"\n[embedding]\nprovider = \"ollama\"\nmodel = \"m\"\n" +
			"[[source]]\ntenant = \"t\"\nview = \"" + view + "\"\naccess = [\"label_id\"]\n"
	}
	t.Setenv("AUTODOC_TEST_UNREACHABLE", "postgres://who:hunter2-secret@127.0.0.1:1/db?connect_timeout=2")
	for _, c := range []struct {
		args []string
		code int
		says string
	}{
		{[]string{"--server"}, 2, "--server needs its configuration"},
		{[]string{"--check", "--version"}, 2, "--check is for --server"},
		{[]string{"--server", filepath.Join(dir, "missing.toml")}, 1, "no such file"},
		{[]string{"--server", write("empty.toml", "[postgres]\ndsn_env = \"AUTODOC_TEST_UNREACHABLE\"\n")}, 2, "source: at least one"},
		{[]string{"--server", write("since.toml", cfg("since.view"))}, 2, "source[0].view: " + filepath.Join(dir, "since.view") + ": args"},
		{[]string{"--server", "--check", write("since2.toml", cfg("since.view"))}, 2, "source[0].view"},
		{[]string{"--server", write("down.toml", cfg("track.view"))}, 1, "schema:"},
		{[]string{"--server", "--check", write("down2.toml", cfg("track.view"))}, 1, "autodoc:"},
	} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), c.args, testOptions, &stdout, &stderr)
		if code != c.code || !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%v: exit %d, said %q; want %d saying %q", c.args, code, stderr.String(), c.code, c.says)
		}
		if strings.Contains(stderr.String(), "hunter2-secret") {
			t.Errorf("%v: the password is in %q", c.args, stderr.String())
		}
	}
}

// redact takes the DSN, and its password, out of an error, whatever the DSN's form.
func TestRedactTakesTheDSNOut(t *testing.T) {
	for dsn, msg := range map[string]string{
		"postgres://u:p4ss@h/db":                 "cannot parse `postgres://u:p4ss@h/db`: bad",
		"host=h user=u password='p4ss' dbname=d": "failed: password=p4ss is wrong",
	} {
		err := redact(errorString(msg), dsn)
		if strings.Contains(err.Error(), "p4ss") {
			t.Errorf("%s: %v", dsn, err)
		}
	}
	plain := errorString("nothing secret")
	if redact(plain, "postgres://u:p@h/d") != error(plain) || redact(nil, "x") != nil {
		t.Error("an error with nothing to redact is not kept as it was")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

// The server's whole life, in process: it creates its schema and every pgstore table there and none
// in public, records the tenant's policy, indexes the source, and holds the tenant's lock; when its
// context ends it stops in reverse, releasing the lock, and exits 0. Run again and its lock's
// connection killed, it stops and exits 1. The mutant, named here: the migrations run before the
// schema is created (the tables land in public).
func TestTheServerIndexesItsSourcesAndStopsInReverse(t *testing.T) {
	f := newServerFixture(t, serverView)
	ctx, cancel := context.WithCancel(f.ctx)
	done, stderr := f.serve(ctx)
	f.waitFor("the tenant's lock taken", func() bool { return f.locks() == "1" })
	tables := f.scalar(`SELECT coalesce(string_agg(table_name, ',' ORDER BY table_name), '') FROM information_schema.tables WHERE table_schema = '` + f.schema + `'`)
	if tables != "autodoc_schema,rag_chunk,rag_cursor,rag_document,rag_embedding,rag_ingest_error,rag_link,rag_meta,rag_tenant_policy" {
		t.Errorf("the schema's tables: %s", tables)
	}
	if public := f.scalar(`SELECT count(*)::text FROM information_schema.tables WHERE table_schema = 'public'
		AND (table_name LIKE 'rag!_%' ESCAPE '!' OR table_name = 'autodoc_schema')`); public != "0" {
		t.Errorf("%s of pgstore's tables are in public", public)
	}
	f.waitFor("both rows indexed", func() bool { return f.documents() == "2" })
	store, err := postgres.Open(f.ctx, os.Getenv("AUTODOC_TEST_SERVER_DSN"), pgstore.WithSchema(f.schema))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	st, err := pgstore.New(store, "lm-catalog")
	if err != nil {
		t.Fatal(err)
	}
	if p, err := st.Policy(f.ctx); err != nil || !slices.Equal(p.AccessFields, []string{"label_id"}) || p.View != "track" {
		t.Errorf("the tenant's policy: %+v, %v", p, err)
	}
	if got := f.locks(); got != "1" {
		t.Errorf("%s advisory locks held while serving, want the tenant's", got)
	}
	// a row added while it serves is indexed at its next pass
	f.exec(`INSERT INTO track VALUES (3, 'A', 'osprey song', now() - interval '1 minute')`)
	f.waitFor("the new row indexed", func() bool { return f.documents() == "3" })
	cancel()
	if code := exitOf(t, done, stderr); code != 0 {
		t.Fatalf("--server exited %d: %s", code, stderr)
	}
	if got := f.locks(); got != "0" {
		t.Errorf("%s advisory locks held after the server stopped", got)
	}
	if !strings.Contains(stderr.String(), "serving enterprise search: 1 sources in schema "+f.schema) {
		t.Errorf("the server said %s", stderr)
	}

	// again, and its lock's connection is killed: it stops, non-zero
	done, stderr = f.serve(f.ctx)
	f.waitFor("the lock taken", func() bool { return f.locks() == "1" })
	f.exec(`SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype = 'advisory' AND granted`)
	// a row to write: the next pass's commit confirms the lock first, sooner than the ping
	f.exec(`INSERT INTO track VALUES (4, 'A', 'tern song', now() - interval '1 minute')`)
	if code := exitOf(t, done, stderr); code != 1 || !strings.Contains(stderr.String(), "writer lock was lost") {
		t.Fatalf("--server after its lock was lost: exit %d: %s", code, stderr)
	}
}

// --server --check confirms each view's order against the source and exits 0 saying so; a view
// whose order is not its cursor's is refused, exit 2, naming its source.
func TestTheServerChecksItsViews(t *testing.T) {
	f := newServerFixture(t, serverView)
	f.exec(`INSERT INTO track VALUES (3, 'A', 'a', '2026-01-01T00:00:00Z'), (4, 'A', 'b', '2026-01-01T00:00:00Z')`)
	var stdout, stderr bytes.Buffer
	if code := run(f.ctx, []string{"--server", "--check", f.path}, testOptions, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "order confirmed over 4 rows") || !strings.HasSuffix(stdout.String(), ": ok\n") {
		t.Fatalf("--check: exit %d, said %q %q", code, stdout.String(), stderr.String())
	}
	if f.documents() != "0" {
		t.Error("--check wrote documents")
	}
	view := filepath.Join(filepath.Dir(f.path), "track.view")
	if err := os.WriteFile(view, []byte(strings.Replace(serverView, "ORDER BY updated_at, id", "ORDER BY id", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(f.ctx, []string{"--server", "--check", f.path}, testOptions, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "source[0].view") {
		t.Fatalf("--check of a view ordered by id: exit %d, said %q", code, stderr.String())
	}
}

// SIGTERM stops a --server process cleanly: exit 0, and its tenant's lock released. The mutant,
// named here: the mode without SIGTERM among its signals (the process dies of it, holding nothing
// in order).
func TestSIGTERMStopsTheServer(t *testing.T) {
	f := newServerFixture(t, serverView)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "--server", f.path)
	cmd.Env = append(os.Environ(), "AUTODOC_TEST_MAIN=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	f.waitFor("both rows indexed", func() bool { return f.documents() == "2" })
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("--server after SIGTERM: %v: %s", err, out.String())
		}
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("--server did not stop on SIGTERM: %s", out.String())
	}
	if got := f.locks(); got != "0" {
		t.Errorf("%s advisory locks held after SIGTERM", got)
	}
}

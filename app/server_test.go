package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search/embed"

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
// AUTODOC_TEST_SERVER_DSN, an OpenAI-compatible embedding endpoint of the test's own, and a pool for
// looking at what the server did.
type serverFixture struct {
	t      *testing.T
	ctx    context.Context
	path   string // server.toml
	view   string // the source's view file
	schema string
	plain  dao.DataConn
	emb    *fakeEmbeddings
	url    string // the endpoint's base URL, as server.toml names it
}

// embedKey is the embedding endpoint's API key, which the fixture's server.toml names through
// AUTODOC_TEST_EMBED_KEY.
const embedKey = "sk-test-embedding-secret-key"

// fakeEmbeddings is an OpenAI-compatible /v1/embeddings with no network beyond loopback: it
// answers a request bearing embedKey with a four-dimensional vector per text, made from the text's
// hash, and counts the texts; any other key is a 401 quoting it, as some providers do.
type fakeEmbeddings struct {
	mu    sync.Mutex
	texts map[string]int
}

func (f *fakeEmbeddings) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/embeddings" {
		http.NotFound(w, r)
		return
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+embedKey {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error": "invalid key %s"}`, strings.TrimPrefix(got, "Bearer "))
		return
	}
	var req struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	type datum struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	}
	var out struct {
		Data []datum `json:"data"`
	}
	f.mu.Lock()
	for i, text := range req.Input {
		f.texts[text]++
		h := sha256.Sum256([]byte(req.Model + "\x00" + text))
		out.Data = append(out.Data, datum{Index: i, Embedding: []float32{float32(h[0]) + 1, float32(h[1]) + 1, float32(h[2]) + 1, float32(h[3]) + 1}})
	}
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(out)
}

// asked is how many times the endpoint embedded text.
func (f *fakeEmbeddings) asked(text string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.texts[text]
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
	f.view = filepath.Join(dir, "track.view")
	if err := os.WriteFile(f.view, []byte(view), 0o600); err != nil {
		t.Fatal(err)
	}
	f.emb = &fakeEmbeddings{texts: map[string]int{}}
	srv := httptest.NewServer(f.emb)
	t.Cleanup(srv.Close)
	f.url = srv.URL + "/v1"
	t.Setenv("AUTODOC_TEST_EMBED_KEY", embedKey)
	f.path = filepath.Join(dir, "server.toml")
	f.configure("fake-model", f.url)
	return f
}

// configure writes server.toml with the embedding model and base URL given.
func (f *serverFixture) configure(model, baseURL string) {
	f.t.Helper()
	cfg := `
[postgres]
dsn_env = "AUTODOC_TEST_SERVER_DSN"
schema  = "` + f.schema + `"

[embedding]
provider = "openai"
base_url = "` + baseURL + `"
model    = "` + model + `"
key_env  = "AUTODOC_TEST_EMBED_KEY"

[[source]]
tenant   = "lm-catalog"
view     = "track.view"
interval = "1s"
lag      = "0s"
access   = ["label_id"]
`
	if err := os.WriteFile(f.path, []byte(cfg), 0o600); err != nil {
		f.t.Fatal(err)
	}
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

// embedded reports whether the tenant is embedded under model: it is the active one, every
// distinct chunk text has a vector under it, there are documents, and every one is ready.
func (f *serverFixture) embedded(model string) bool {
	s := f.schema
	return f.scalar(`SELECT coalesce((
		(SELECT active_model FROM `+s+`.rag_meta WHERE tenant = 'lm-catalog') = '`+model+`'
		AND (SELECT count(*) FROM `+s+`.rag_embedding WHERE model = '`+model+`') = (SELECT count(DISTINCT text_hash) FROM `+s+`.rag_chunk)
		AND EXISTS (SELECT 1 FROM `+s+`.rag_document)
		AND NOT EXISTS (SELECT 1 FROM `+s+`.rag_document WHERE NOT semantic_ready)), false)::text`) == "true"
}

// vectors is how many vectors the server stored under model.
func (f *serverFixture) vectors(model string) string {
	return f.scalar(`SELECT count(*)::text FROM ` + f.schema + `.rag_embedding WHERE model = '` + model + `'`)
}

// ownLocks selects the advisory locks held in the fixture's database: pg_locks shows the whole
// server's, other databases' included.
const ownLocks = `FROM pg_locks WHERE locktype = 'advisory' AND granted
	AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`

// locks is how many advisory locks are held in the fixture's database.
func (f *serverFixture) locks() string {
	return f.scalar(`SELECT count(*)::text ` + ownLocks)
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

// syncBuffer is a buffer the server writes from its goroutines while the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// serve runs --server in this process until ctx ends, and returns its exit code on the channel.
func (f *serverFixture) serve(ctx context.Context, args ...string) (<-chan int, *syncBuffer) {
	var stdout bytes.Buffer
	stderr := &syncBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, append([]string{"--server"}, append(args, f.path)...), testOptions, &stdout, stderr)
	}()
	return done, stderr
}

// started waits for the server to say it serves: every step has started, the migrations committed.
// (The migrations hold an advisory lock of their own while they run, so a lock held is no sign.)
func (f *serverFixture) started(stderr *syncBuffer) {
	f.t.Helper()
	f.waitFor("the server serving", func() bool { return strings.Contains(stderr.String(), "serving enterprise search") })
}

func exitOf(t *testing.T, done <-chan int, stderr *syncBuffer) int {
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
// run in it, the tenants' policies are recorded, the ingesters start, and the embedding worker
// starts last, so it stops first, while the ingesters still hold their tenants' locks.
func TestServerStepsStartInOrder(t *testing.T) {
	var names []string
	for _, s := range serverSteps {
		names = append(names, s.name)
	}
	if want := []string{"postgres", "schema", "policy", "ingesters", "embedder"}; !slices.Equal(names, want) {
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
	f.started(stderr)
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
	f.started(stderr)
	f.waitFor("the lock taken", func() bool { return f.locks() == "1" })
	f.exec(`SELECT pg_terminate_backend(pid) ` + ownLocks)
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
	if !strings.Contains(stdout.String(), "embedding: openai at "+f.url+" answered: openai|fake-model||4, 4 dimensions") {
		t.Errorf("--check did not report the embedding provider: %q", stdout.String())
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

// The server embeds its tenant centrally: every distinct text gets a vector under the configured
// model, which is the tenant's active one, and every document is ready. A row added while it
// serves is embedded at once, its commit waking the worker long before the worker's poll. With
// another model configured, the next start switches: it fills the new model, flips to it and
// reclaims the old one's vectors. The key is never in the log. The mutant, named here: the
// ingesters not waking the worker (the new row waits for the 30 s poll).
func TestTheServerEmbedsItsTenantsCentrally(t *testing.T) {
	f := newServerFixture(t, serverView)
	first, second := "openai|fake-model||4", "openai|fake-model-2||4"
	ctx, cancel := context.WithCancel(f.ctx)
	done, stderr := f.serve(ctx)
	f.started(stderr)
	f.waitFor("both rows embedded", func() bool { return f.documents() == "2" && f.embedded(first) })
	f.exec(`INSERT INTO track VALUES (3, 'A', 'osprey song', now() - interval '1 minute')`)
	f.waitFor("the new row embedded, woken by its commit", func() bool { return f.documents() == "3" && f.embedded(first) })
	cancel()
	if code := exitOf(t, done, stderr); code != 0 {
		t.Fatalf("--server exited %d: %s", code, stderr)
	}
	log := stderr.String()

	f.configure("fake-model-2", f.url)
	ctx, cancel = context.WithCancel(f.ctx)
	defer cancel()
	done, stderr = f.serve(ctx)
	f.started(stderr)
	f.waitFor("the switch to the second model", func() bool { return f.embedded(second) && f.vectors(first) == "0" })
	cancel()
	if code := exitOf(t, done, stderr); code != 0 {
		t.Fatalf("--server exited %d: %s", code, stderr)
	}
	log += stderr.String()
	for _, says := range []string{"embedding with " + first, "switching from " + first + " to " + second, second + " is active"} {
		if !strings.Contains(log, says) {
			t.Errorf("the log does not say %q: %s", says, log)
		}
	}
	if strings.Contains(log, embedKey) {
		t.Errorf("the key is in the log: %s", log)
	}
}

// --server --check asks the embedding provider for a probe: a key it refuses is refused, exit 2,
// naming embedding.key_env, and the key it quotes back is never shown; a provider that does not
// answer is a warning, and the check passes. The mutants, named here: a refused key reported as a
// warning, and the provider's error shown with the key in it.
func TestTheServerChecksItsEmbeddingProvider(t *testing.T) {
	f := newServerFixture(t, serverView)
	t.Setenv("AUTODOC_TEST_EMBED_KEY", "sk-the-wrong-secret")
	var stdout, stderr bytes.Buffer
	code := run(f.ctx, []string{"--server", "--check", f.path}, testOptions, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "embedding.key_env: the provider refused the key") {
		t.Errorf("--check with a refused key: exit %d, said %q %q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "sk-the-wrong-secret") || !strings.Contains(stderr.String(), "[REDACTED]") {
		t.Errorf("--check showed the key, or did not say it hid it: %q", stderr.String())
	}
	t.Setenv("AUTODOC_TEST_EMBED_KEY", embedKey)
	f.configure("fake-model", "http://127.0.0.1:1/v1")
	stdout.Reset()
	stderr.Reset()
	code = run(f.ctx, []string{"--server", "--check", f.path}, testOptions, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "warning: embedding: openai at http://127.0.0.1:1/v1 did not answer the probe") ||
		!strings.HasSuffix(stdout.String(), ": ok\n") {
		t.Errorf("--check with a provider that does not answer: exit %d, said %q %q", code, stdout.String(), stderr.String())
	}
}

// An OpenAI-compatible base URL is its root, whether or not server.toml names the API's /v1; and
// a key taken out of an error leaves the error what it was, so a rejection still splits a batch.
func TestTheEmbeddingProvidersURLAndKey(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.openai.com/v1":      "https://api.openai.com",
		"https://api.openai.com/v1/":     "https://api.openai.com",
		"https://embeddings.example/":    "https://embeddings.example",
		"https://embeddings.example/v1x": "https://embeddings.example/v1x",
	} {
		if got := openAIRoot(in); got != want {
			t.Errorf("openAIRoot(%q) = %q, want %q", in, got, want)
		}
	}
	err := hideKey(fmt.Errorf("%w: the key sk-abc is not allowed this input", embed.ErrRejected), "sk-abc")
	if !errors.Is(err, embed.ErrRejected) || strings.Contains(err.Error(), "sk-abc") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("hideKey: %v (a rejection: %v)", err, errors.Is(err, embed.ErrRejected))
	}
	plain := errors.New("nothing secret")
	if hideKey(plain, "sk-abc") != plain || hideKey(nil, "sk-abc") != nil || hideKey(plain, "") != plain {
		t.Error("an error with no key in it is not kept as it was")
	}
}

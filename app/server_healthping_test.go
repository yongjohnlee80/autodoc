//go:build server

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Health pings the database on a connection the searches can't take: with every connection of the
// store's pool held (its one connection waiting on a table lock), GET /v1/health still answers 200
// with the database ok, at once rather than at its ping's deadline. The mutant, named here: the
// ping on the store's pool (it waits out its 2 s deadline, and health answers 503 unavailable).
func TestHealthAnswersWhileEveryStoreConnectionIsHeld(t *testing.T) {
	f := newServerFixture(t, serverView)
	u, err := url.Parse(os.Getenv("AUTODOC_TEST_SERVER_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("pool_max_conns", "1") // the store's pool is one connection, which the test then holds
	u.RawQuery = q.Encode()
	t.Setenv("AUTODOC_TEST_SERVER_DSN", u.String())
	t.Setenv("AUTODOC_TEST_HTTP_TOKEN", httpToken)
	f.extra = httpBlock
	f.configure("fake-model", f.url)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done, stderr := f.serve(ctx)
	f.waitServing(done, stderr, "the server serving", func() bool { return strings.Contains(stderr.String(), "serving enterprise search") })
	f.waitServing(done, stderr, "both rows written", func() bool { return f.documents() == "2" })
	m := listening.FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("the server does not say where it listens: %s", stderr)
	}

	tx, err := f.plain.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(f.ctx, `LOCK TABLE `+f.schema+`.rag_document IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO track VALUES (3, 'A', 'wren song', now())`)
	f.waitServing(done, stderr, "a store connection waiting on the lock", func() bool {
		return f.scalar(`SELECT count(*)::text FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'
			AND query ILIKE '%rag_document%'`) != "0"
	})
	start := time.Now()
	res, err := http.Get("http://" + m[1] + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	took := time.Since(start)
	var h struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	if res.StatusCode != http.StatusOK || h.Checks["database"] != "ok" {
		t.Errorf("health with the store's connection held: %d %s, want 200 with the database ok", res.StatusCode, b)
	}
	if took >= time.Second {
		t.Errorf("health took %v with the store's connection held: its ping waited for the store's pool", took)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	f.waitServing(done, stderr, "the row that waited written", func() bool { return f.documents() == "3" })
	cancel()
	if code := exitOf(t, done, stderr); code != 0 {
		t.Errorf("--server exited %d: %s", code, stderr)
	}
}

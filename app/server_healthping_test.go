//go:build server

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Health pings the database on a connection the searches can't take: with every connection of the
// store's pool held (its two waiting on a table lock, the ingester's write on one and a search on
// the other), GET /v1/health still answers 200 with the database ok, at once rather than at its
// ping's deadline. The mutant, named here: the ping on the store's pool (it waits out its 2 s
// deadline, and health answers 503 unavailable).
func TestHealthAnswersWhileEveryStoreConnectionIsHeld(t *testing.T) {
	quickBuilder(t)
	f := newServerFixture(t, serverView)
	f.storePool(minIndexConns) // the fewest the server takes, both of which the test then holds
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

	release, searched := f.holdStore(done, stderr, m[1])
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
		t.Errorf("health with every store connection held: %d %s, want 200 with the database ok", res.StatusCode, b)
	}
	if took >= time.Second {
		t.Errorf("health took %v with every store connection held: its ping waited for the store's pool", took)
	}
	release()
	f.waitServing(done, stderr, "the row that waited written", func() bool { return f.documents() == "3" })
	if code := <-searched; code != http.StatusOK {
		t.Errorf("the search that waited on the lock answered %d, want 200", code)
	}
	cancel()
	if code := exitOf(t, done, stderr); code != 0 {
		t.Errorf("--server exited %d: %s", code, stderr)
	}
}

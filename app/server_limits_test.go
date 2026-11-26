//go:build server

package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// A client's limits in server.toml are the served API's. A client given one search in flight, its
// search held in the embedding provider: its second is refused 429 too_many_in_flight with
// Retry-After 1, while the first is still answered once the provider lets it go. The refusal has
// no log line of its own; the status counts it under server.http.refused; and the server, stopping
// before the minute is out, says it in the client's summary. The mutants, named here: the client's
// limits not carried from the configuration to the API (the second search is let in); the
// collector not given the API's counts (the status has no http).
func TestAClientsLimitsAreTheServedAPIs(t *testing.T) {
	f := newServerFixture(t, serverView)
	t.Setenv("AUTODOC_TEST_HTTP_TOKEN", httpToken)
	t.Setenv("AUTODOC_TEST_OPS_TOKEN", opsToken)
	f.extra = `
[http]
listen = "127.0.0.1:0"

[[http.client]]
name          = "web-app"
token_env     = "AUTODOC_TEST_HTTP_TOKEN"
max_in_flight = 1

[http.client.grants.music]
label_id = ["A"]

[[http.operator]]
name      = "ops"
token_env = "AUTODOC_TEST_OPS_TOKEN"
tenants   = ["music"]
`
	f.configure("fake-model", f.url)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done, stderr := f.serve(ctx)
	f.started(stderr)
	f.waitFor("both rows embedded", func() bool { return f.documents() == "2" && f.embedded("openai|fake-model||4") })
	m := listening.FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("the server does not say where it listens: %s", stderr)
	}
	call := func(method, path, token, body string) (int, http.Header, string) {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+m[1]+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header, string(b)
	}

	held, release := make(chan struct{}, 1), make(chan struct{})
	var released sync.Once
	let := func() { released.Do(func() { close(release) }) }
	t.Cleanup(let) // before the endpoint's own cleanup, which waits for its held handler
	f.emb.mu.Lock()
	f.emb.gate = func(text string) {
		if text == "hold this query" {
			held <- struct{}{}
			<-release
		}
	}
	f.emb.mu.Unlock()
	first := make(chan int, 1)
	go func() {
		status, _, _ := call(http.MethodPost, "/v1/search", httpToken, `{"tenant": "music", "query": "hold this query", "scope": {"label_id": ["A"]}}`)
		first <- status
	}()
	<-held

	status, header, body := call(http.MethodPost, "/v1/search", httpToken, `{"tenant": "music", "query": "kestrel", "scope": {"label_id": ["A"]}}`)
	if status != http.StatusTooManyRequests || !strings.Contains(body, `"too_many_in_flight"`) || header.Get("Retry-After") != "1" {
		t.Fatalf("a second search with the client's one in flight: %d %s, Retry-After %q", status, body, header.Get("Retry-After"))
	}
	if status, _, body := call(http.MethodGet, "/v1/status", opsToken, ""); status != http.StatusOK ||
		!strings.Contains(body, `"http":{"refused":{"in_flight":1,"rate":0,"unauthorized":0}}`) {
		t.Errorf("the status: %d %s", status, body)
	}
	let()
	if status := <-first; status != http.StatusOK {
		t.Errorf("the search in flight: %d", status)
	}
	if status, _, body := call(http.MethodPost, "/v1/search", httpToken, `{"tenant": "music", "query": "kestrel", "scope": {"label_id": ["A"]}}`); status != http.StatusOK {
		t.Errorf("a search once the first was answered: %d %s", status, body)
	}
	if said := stderr.String(); strings.Contains(said, "429 too_many") || strings.Contains(said, "refused in") {
		t.Errorf("the refusal is in the log before its summary is due: %s", said)
	}
	cancel()
	if code := exitOf(t, done, stderr); code != 0 {
		t.Fatalf("--server exited %d: %s", code, stderr)
	}
	want := "search: client web-app: 1 refused in 1m0s: 1 over its searches in flight (too_many_in_flight), 0 over its rate (too_many_requests)"
	if said := stderr.String(); !strings.Contains(said, want) {
		t.Errorf("the server stopped without the client's summary %q: %s", want, said)
	}
	if strings.Contains(stderr.String(), httpToken) {
		t.Errorf("the token is in the log: %s", stderr)
	}
}

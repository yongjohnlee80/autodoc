package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao/postgres"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/enterprise/config"
	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

// httpToken is the fixture's client's bearer token, read through AUTODOC_TEST_HTTP_TOKEN.
const httpToken = "a-test-bearer-token-longer-than-32-bytes-0123456789"

// httpBlock is an [http] on a free loopback port, with one client granted label A of the catalog.
const httpBlock = `
[http]
listen = "127.0.0.1:0"

[[http.client]]
name      = "lm-web"
token_env = "AUTODOC_TEST_HTTP_TOKEN"

[http.client.grants.lm-catalog]
label_id = ["A"]
`

var listening = regexp.MustCompile(`search API on (\S+) `)

// searchJSON is a response of POST /v1/search, as the tests read it.
type searchJSON struct {
	Tenant   string `json:"tenant"`
	ModeUsed string `json:"mode_used"`
	Semantic string `json:"semantic"`
	Hits     []struct {
		Path      string   `json:"path"`
		Snippet   string   `json:"snippet"`
		Score     float64  `json:"score"`
		Relevance float64  `json:"relevance"`
		Via       []string `json:"via"`
		SourceID  string   `json:"source_id"`
	} `json:"hits"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// --server with [http] serves search over HTTP, after the embedder: by words and by meaning,
// within the client's grants, each hit with its row's id, and the JSON is the library's answer
// to the same scope and request on the same tenant. Stopping drains a search in flight: held in
// the provider when the server is told to stop, it is answered 200, and only then does the server
// exit. The token never reaches the log. The mutants, named here: the HTTP step left out of the
// stack (nothing listens); the requests' context the server's (the held search cancelled).
func TestTheServerServesSearchOverHTTP(t *testing.T) {
	f := newServerFixture(t, serverView)
	t.Setenv("AUTODOC_TEST_HTTP_TOKEN", httpToken)
	f.extra = httpBlock
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
	post := func(body string) (int, searchJSON) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, "http://"+m[1]+"/v1/search", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+httpToken)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		var out searchJSON
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("%s: %v", b, err)
		}
		return res.StatusCode, out
	}

	status, got := post(`{"tenant": "lm-catalog", "query": "kestrel", "scope": {"label_id": ["A"]}}`)
	if status != http.StatusOK || len(got.Hits) == 0 || got.Hits[0].Path != "track/1.md" || got.Hits[0].SourceID != "1" || got.Semantic != "ready" {
		t.Fatalf("kestrel within A: %d %+v", status, got)
	}
	for _, h := range got.Hits {
		if h.Path != "track/1.md" {
			t.Errorf("within A, the hit %s", h.Path)
		}
	}
	if status, out := post(`{"tenant": "lm-catalog", "query": "heron", "mode": "lexical", "scope": {"label_id": ["A"]}}`); status != http.StatusOK || len(out.Hits) != 0 {
		t.Errorf("heron (a B track) within A: %d %+v", status, out)
	}
	if status, out := post(`{"tenant": "lm-catalog", "query": "heron", "scope": {"label_id": ["B"]}}`); status != http.StatusForbidden || out.Error.Code != "scope_forbidden" {
		t.Errorf("label B, not granted: %d %+v", status, out)
	}

	// the library, on its own pool, with the server's provider, answers the same
	cfg, err := config.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := providerOf(cfg.Embedding)(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(f.ctx, os.Getenv("AUTODOC_TEST_SERVER_DSN"), pgstore.WithSchema(f.schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	sr, err := search.Open(f.ctx, pool, "lm-catalog", search.QueryEmbedder(p))
	if err != nil {
		t.Fatal(err)
	}
	lib, err := sr.Search(f.ctx, search.Scope{"label_id": {"A"}}, search.Request{Query: "kestrel"})
	if err != nil {
		t.Fatal(err)
	}
	if string(lib.ModeUsed) != got.ModeUsed || string(lib.Semantic) != got.Semantic || len(lib.Hits) != len(got.Hits) {
		t.Fatalf("the library: %+v; the API: %+v", lib, got)
	}
	for i, h := range lib.Hits {
		g := got.Hits[i]
		if h.Path != g.Path || h.SourceID != g.SourceID || h.Snippet != g.Snippet || h.Score != g.Score || h.Relevance != g.Relevance || strings.Join(h.Via, ",") != strings.Join(g.Via, ",") {
			t.Errorf("hit %d: the library's %+v, the API's %+v", i, h, g)
		}
	}

	// a search held in the provider when the server stops is answered, then the server exits
	held, release := make(chan struct{}, 1), make(chan struct{})
	f.emb.mu.Lock()
	f.emb.gate = func(text string) {
		if text == "hold this query" {
			held <- struct{}{}
			<-release
		}
	}
	f.emb.mu.Unlock()
	answered := make(chan int, 1)
	go func() {
		status, _ := post(`{"tenant": "lm-catalog", "query": "hold this query", "scope": {"label_id": ["A"]}}`)
		answered <- status
	}()
	<-held
	cancel()
	select {
	case code := <-done:
		t.Fatalf("--server exited %d with a search in flight", code)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if status := <-answered; status != http.StatusOK {
		t.Errorf("the search in flight when the server stopped: %d", status)
	}
	if code := exitOf(t, done, stderr); code != 0 {
		t.Fatalf("--server exited %d: %s", code, stderr)
	}
	if strings.Contains(stderr.String(), httpToken) {
		t.Errorf("the token is in the log: %s", stderr)
	}
}

//go:build server

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeTEI is a TEI server serving a reranker: /info names it, and /rerank scores each text by the
// word in it (owl highest), or answers refuse when set. It records every query and text it was
// sent, which the log must never hold.
type fakeTEI struct {
	refuse atomic.Int32 // an HTTP status to answer /rerank with; 0 answers

	mu   sync.Mutex
	sent []string
}

func (f *fakeTEI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/info":
		_, _ = io.WriteString(w, `{"model_id":"fake-rerank","model_type":{"reranker":{"id2label":{}}},"max_client_batch_size":32}`)
	case "/rerank":
		var req struct {
			Query string   `json:"query"`
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.sent = append(append(f.sent, req.Query), req.Texts...)
		f.mu.Unlock()
		if code := f.refuse.Load(); code != 0 {
			http.Error(w, "refused", int(code))
			return
		}
		type scored struct {
			Index int     `json:"index"`
			Score float64 `json:"score"`
		}
		out := make([]scored, len(req.Texts))
		for i, t := range req.Texts {
			out[i] = scored{Index: i, Score: 0.1}
			switch {
			case strings.Contains(t, "owl"):
				out[i].Score = 0.9
			case strings.Contains(t, "sparrow"):
				out[i].Score = 0.5
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	default:
		http.NotFound(w, r)
	}
}

// --server with [ranker] reranks its searches through it: a search within the client's grants
// answers in the ranker's order, its rank ready and each hit with its score; the status names the
// ranker up and counts the search, and the metrics count its call and texts. A ranker that refuses
// leaves the search whole, in recall order, its rank in error with provider_refused, and the
// ranker down in the status; once it answers again the prober brings it back up. The log never
// holds a query or a text sent to the ranker, on any of these paths.
func TestTheServerReranksThroughItsRanker(t *testing.T) {
	f := newServerFixture(t, serverView)
	f.exec(`INSERT INTO track VALUES (3, 'A', 'owl song zqtextmarker', now() - interval '1 hour'), (4, 'A', 'sparrow song', now() - interval '1 hour')`)
	tei := &fakeTEI{}
	ranker := httptest.NewServer(tei)
	t.Cleanup(ranker.Close)
	t.Setenv("AUTODOC_TEST_HTTP_TOKEN", httpToken)
	t.Setenv("AUTODOC_TEST_OPS_TOKEN", opsToken)
	f.extra = httpBlock + `
[[http.operator]]
name      = "ops"
token_env = "AUTODOC_TEST_OPS_TOKEN"
tenants   = ["music"]

[metrics]
enabled = true

[ranker]
kind     = "tei"
base_url = "` + ranker.URL + `"
timeout  = "5s"
`
	f.configure("fake-model", f.url)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done, stderr := f.serve(ctx)
	f.started(stderr)
	f.waitFor("the rows embedded", func() bool { return f.documents() == "4" && f.embedded("openai|fake-model||4") })
	m := listening.FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("the server does not say where it listens: %s", stderr)
	}
	call := func(method, path, token, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, "http://"+m[1]+path, strings.NewReader(body))
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
		return res.StatusCode, string(b)
	}
	type answer struct {
		Rank struct{ State, Model, Error string } `json:"rank"`
		Hits []struct {
			Path      string   `json:"path"`
			RankScore *float64 `json:"rank_score"`
		} `json:"hits"`
	}
	search := func(body string) answer {
		t.Helper()
		status, b := call(http.MethodPost, "/v1/search", httpToken, body)
		var a answer
		if status != http.StatusOK || json.Unmarshal([]byte(b), &a) != nil {
			t.Fatalf("search %s: %d %s", body, status, b)
		}
		return a
	}
	status := func() string {
		t.Helper()
		_, b := call(http.MethodGet, "/v1/status", opsToken, "")
		return b
	}
	f.waitFor("the ranker up", func() bool { return strings.Contains(status(), `"kind":"tei","model":"fake-rerank","state":"up"`) })

	got := search(`{"tenant": "music", "query": "song qqquerymarker", "mode": "semantic", "scope": {"label_id": ["A"]}}`)
	if got.Rank.State != "ready" || got.Rank.Model != "fake-rerank" || len(got.Hits) != 3 {
		t.Fatalf("a ranked search: %+v", got)
	}
	if got.Hits[0].Path != "track/3.md" || got.Hits[1].Path != "track/4.md" || got.Hits[0].RankScore == nil || *got.Hits[0].RankScore != 0.9 {
		t.Errorf("hits %+v, want the owl, then the sparrow, each with its score", got.Hits)
	}
	st := status()
	if !strings.Contains(st, `"ranker":{"kind":"tei","model":"fake-rerank","state":"up","window":40,"ranked":1,`) {
		t.Errorf("status %s, want the ranker up with one search ranked", st)
	}
	_, metrics := call(http.MethodGet, "/v1/metrics", opsToken, "")
	for _, want := range []string{"autodoc_rank_up 1", `autodoc_rank_calls_total{result="ok"} 1`, "autodoc_rank_texts_total 3", "autodoc_rank_seconds_count 1"} {
		if !strings.Contains(metrics, want) {
			t.Errorf("the metrics lack %q:\n%s", want, metrics)
		}
	}

	tei.refuse.Store(http.StatusUnauthorized)
	got = search(`{"tenant": "music", "query": "song qqquerymarker", "mode": "semantic", "scope": {"label_id": ["A"]}}`)
	if got.Rank.State != "error" || got.Rank.Error != "provider_refused" || len(got.Hits) != 3 || got.Hits[0].RankScore != nil {
		t.Fatalf("a search the ranker refused: %+v, want it whole, in recall order, provider_refused", got)
	}
	if st := status(); !strings.Contains(st, `"state":"down","error":"provider_refused"`) || !strings.Contains(st, `"unranked":{"provider_refused":1}`) {
		t.Errorf("status %s, want the ranker down, refused, and the search counted unranked", st)
	}
	tei.refuse.Store(0)
	f.waitFor("the ranker up again", func() bool { return strings.Contains(status(), `"model":"fake-rerank","state":"up"`) })
	if got := search(`{"tenant": "music", "query": "song", "scope": {"label_id": ["A"]}, "rerank": false}`); got.Rank.State != "off" {
		t.Errorf("rerank false: %+v", got.Rank)
	}
	if st := status(); !strings.Contains(st, `"skipped":1`) {
		t.Errorf("status %s, want the search counted skipped", st)
	}

	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	log := stderr.String()
	tei.mu.Lock()
	sent := strings.Join(tei.sent, "\n")
	tei.mu.Unlock()
	if !strings.Contains(sent, "qqquerymarker") || !strings.Contains(sent, "zqtextmarker") {
		t.Fatalf("the ranker was not sent the markers: %q", sent)
	}
	for _, marker := range []string{"qqquerymarker", "zqtextmarker"} {
		if strings.Contains(log, marker) {
			t.Errorf("the log holds %q, a query's or a text's:\n%s", marker, log)
		}
	}
	if !strings.Contains(log, "ranker: tei is down (provider_refused)") {
		t.Errorf("the log does not say the ranker went down:\n%s", log)
	}
}

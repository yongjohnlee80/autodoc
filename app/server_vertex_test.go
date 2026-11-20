//go:build server

package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// GOOGLE, FAKED: the token endpoint a service account's key file names, Vertex AI's :embedContent
// and the Ranking API's rankingConfigs.rank, as Google's references give them. No call leaves the
// machine, and no credential of anyone's is used: the key is the test's own.

const (
	vertexEmbedPath = "/v1/projects/p-1/locations/us/publishers/google/models/gemini-embedding-2:embedContent"
	vertexRankPath  = "/v1/projects/p-1/locations/global/rankingConfigs/default_ranking_config:rank"
)

// fakeGoogle answers tokens ("ya29.fake-<n>", n counting) and both APIs, recording the texts and
// queries they were sent. embedRefuse and rankRefuse, when set, are the status and body a call is
// answered with instead.
type fakeGoogle struct {
	tokens      atomic.Int64
	tokenRefuse atomic.Bool // the token endpoint answers 400 with a marker in its body

	mu          sync.Mutex
	issued      map[string]bool
	embedded    []string // the texts as sent, prompt and all
	ranked      []string // the queries and the records' contents
	embedRefuse *refusal
	rankRefuse  *refusal
}

type refusal struct {
	status int
	body   string
}

func newFakeGoogle(t *testing.T) (*fakeGoogle, *httptest.Server) {
	t.Helper()
	g := &fakeGoogle{issued: map[string]bool{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return g, srv
}

func (g *fakeGoogle) refuse(embed, rank *refusal) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.embedRefuse, g.rankRefuse = embed, rank
}

func (g *fakeGoogle) sent() (embedded, ranked []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.embedded...), append([]string(nil), g.ranked...)
}

func (g *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/token" {
		if g.tokenRefuse.Load() {
			http.Error(w, `{"error":"invalid_grant","error_description":"zqtokenbodymarker"}`, http.StatusBadRequest)
			return
		}
		tok := "ya29.fake-" + strconv.FormatInt(g.tokens.Add(1), 10)
		g.mu.Lock()
		g.issued[tok] = true
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":3600}`, tok)
		return
	}
	g.mu.Lock()
	ok := g.issued[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	embedRefuse, rankRefuse := g.embedRefuse, g.rankRefuse
	g.mu.Unlock()
	if !ok {
		http.Error(w, `{"error":{"code":401,"status":"UNAUTHENTICATED"}}`, http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case vertexEmbedPath:
		if embedRefuse != nil {
			http.Error(w, embedRefuse.body, embedRefuse.status)
			return
		}
		var req struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			Config struct {
				Dims int `json:"outputDimensionality"`
			} `json:"embedContentConfig"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Content.Parts) != 1 {
			http.Error(w, `{"error":{"code":400,"status":"INVALID_ARGUMENT"}}`, http.StatusBadRequest)
			return
		}
		text := req.Content.Parts[0].Text
		g.mu.Lock()
		g.embedded = append(g.embedded, text)
		g.mu.Unlock()
		dims := req.Config.Dims
		if dims == 0 {
			dims = 3072
		}
		// a vector by the text's words, the prompt left out: a query and a document sharing a
		// word are near
		vec := make([]float32, dims)
		_, body, _ := strings.Cut(text, ": ")
		if i := strings.LastIndex(text, "| text: "); i >= 0 {
			body = text[i+len("| text: "):]
		} else if i := strings.LastIndex(text, "| query: "); i >= 0 {
			body = text[i+len("| query: "):]
		}
		for _, word := range strings.Fields(strings.ToLower(body)) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(word))
			vec[int(h.Sum32())%dims]++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": map[string]any{"values": vec},
			"usageMetadata": map[string]any{"promptTokenCount": len(strings.Fields(text))}})
	case vertexRankPath:
		if rankRefuse != nil {
			http.Error(w, rankRefuse.body, rankRefuse.status)
			return
		}
		var req struct {
			Model   string `json:"model"`
			Query   string `json:"query"`
			Records []struct {
				ID      string `json:"id"`
				Content string `json:"content"`
			} `json:"records"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model != "semantic-ranker-default-005" {
			http.Error(w, `{"error":{"code":400,"status":"INVALID_ARGUMENT"}}`, http.StatusBadRequest)
			return
		}
		g.mu.Lock()
		g.ranked = append(g.ranked, req.Query)
		type scored struct {
			ID    string  `json:"id"`
			Score float64 `json:"score"`
		}
		out := make([]scored, len(req.Records))
		for i, rec := range req.Records {
			g.ranked = append(g.ranked, rec.Content)
			out[i] = scored{ID: rec.ID, Score: 0.1}
			if strings.Contains(rec.Content, "owl") {
				out[i].Score = 0.9
			}
		}
		g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"records": out})
	default:
		http.NotFound(w, r)
	}
}

// serviceAccount writes a service account key file, the test's own key, its token endpoint the
// fake's, names it in GOOGLE_APPLICATION_CREDENTIALS, and starts the process's credentials afresh
// (gcpTokens keeps the source it found).
func serviceAccount(t *testing.T, tokenURL string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "p-1", "private_key_id": "k1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "autodoc@p-1.iam.gserviceaccount.com", "client_id": "1", "token_uri": tokenURL,
	})
	p := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", p)
	freshTokens(t)
}

// freshTokens starts the process's credentials afresh for the test, and again after it.
func freshTokens(t *testing.T) {
	t.Helper()
	gcpTokens = &googleTokens{}
	t.Cleanup(func() { gcpTokens = &googleTokens{} })
}

// configureVertex writes server.toml with vertex's embedding at dims and ranker, both at google.
func (f *serverFixture) configureVertex(google string, dims int) {
	f.t.Helper()
	cfg := `
[postgres]
dsn_env = "AUTODOC_TEST_SERVER_DSN"
schema  = "` + f.schema + `"

[embedding]
provider   = "vertex"
base_url   = "` + google + `"
project    = "p-1"
location   = "us"
model      = "gemini-embedding-2"
dimensions = ` + strconv.Itoa(dims) + `

[ranker]
kind     = "vertex"
base_url = "` + google + `"
project  = "p-1"
model    = "semantic-ranker-default-005"
timeout  = "5s"

[[source]]
tenant   = "music"
view     = "track.view"
interval = "1s"
lag      = "0s"
access   = ["label_id"]
` + f.extra
	if err := os.WriteFile(f.path, []byte(cfg), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// --server with vertex embeds through Vertex AI and reranks through the Ranking API, with tokens
// from Application Default Credentials (a service account's key file): the corpus embedded at
// 1536 dimensions, each document in Google's document format and the query in its query format;
// a search reranked through vertex, the owl first; the ranker refusing, the search whole in recall
// order with provider_refused. dimensions 768 at the next start is a model switch, filled with no
// gap. No token reaches the log.
func TestTheServerEmbedsAndRanksThroughVertex(t *testing.T) {
	f := newServerFixture(t, serverView)
	f.exec(`INSERT INTO track VALUES (3, 'A', 'owl song', now() - interval '1 hour'), (4, 'A', 'sparrow song', now() - interval '1 hour')`)
	g, google := newFakeGoogle(t)
	serviceAccount(t, google.URL+"/token")
	t.Setenv("AUTODOC_TEST_HTTP_TOKEN", httpToken)
	f.extra = httpBlock
	f.configureVertex(google.URL, 1536)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done, stderr := f.serve(ctx)
	f.started(stderr)
	f.waitFor("the rows embedded through vertex", func() bool { return f.documents() == "4" && f.embedded("vertex|gemini-embedding-2|doc-v1|1536") })
	m := listening.FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("the server does not say where it listens: %s", stderr)
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
		req, _ := http.NewRequest(http.MethodPost, "http://"+m[1]+"/v1/search", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+httpToken)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		var a answer
		if res.StatusCode != http.StatusOK || json.Unmarshal(b, &a) != nil {
			t.Fatalf("search %s: %d %s", body, res.StatusCode, b)
		}
		return a
	}
	var got answer
	f.waitFor("a search reranked through vertex", func() bool {
		got = search(`{"tenant": "music", "query": "song", "mode": "semantic", "scope": {"label_id": ["A"]}}`)
		return got.Rank.State == "ready"
	})
	if got.Rank.Model != "semantic-ranker-default-005" || len(got.Hits) != 3 || got.Hits[0].Path != "track/3.md" || got.Hits[0].RankScore == nil {
		t.Fatalf("the vertex-ranked search: %+v", got)
	}
	embedded, ranked := g.sent()
	docs, queries := 0, 0
	for _, x := range embedded {
		switch {
		case strings.HasPrefix(x, "title: none | text: "):
			docs++
		case x == "task: search result | query: song":
			queries++
		}
	}
	if docs == 0 || queries == 0 || docs+queries+1 < len(embedded) { // +1: the client's probe, a document
		t.Errorf("sent %d documents and %d queries in Google's formats of %d texts: %q", docs, queries, len(embedded), embedded)
	}
	if !strings.Contains(strings.Join(ranked, "\n"), "owl song") {
		t.Errorf("the ranker was sent %q, want the window's texts", ranked)
	}

	g.refuse(nil, &refusal{http.StatusForbidden, `{"error":{"code":403,"status":"PERMISSION_DENIED"}}`})
	got = search(`{"tenant": "music", "query": "song", "mode": "semantic", "scope": {"label_id": ["A"]}}`)
	if got.Rank.State != "error" || got.Rank.Error != "provider_refused" || len(got.Hits) != 3 || got.Hits[0].RankScore != nil {
		t.Fatalf("a search vertex refused: %+v, want it whole, in recall order, provider_refused", got)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	log := stderr.String()

	// another size at the next start: a switch, the old vectors searched until the new are in
	g.refuse(nil, nil)
	f.configureVertex(google.URL, 768)
	ctx2, cancel2 := context.WithCancel(f.ctx)
	defer cancel2()
	done2, stderr2 := f.serve(ctx2)
	f.started(stderr2)
	f.waitFor("the switch to 768 dimensions", func() bool {
		return f.embedded("vertex|gemini-embedding-2|doc-v1|768") // the active model, its every text embedded
	})
	cancel2()
	if code := <-done2; code != 0 {
		t.Fatalf("exit %d: %s", code, stderr2)
	}
	log += stderr2.String()
	if n := g.tokens.Load(); n == 0 {
		t.Fatal("no token was asked for")
	}
	for i := int64(1); i <= g.tokens.Load(); i++ {
		if strings.Contains(log, "ya29.fake-"+strconv.FormatInt(i, 10)) {
			t.Errorf("the log holds token %d:\n%s", i, log)
		}
	}
}

// --check with vertex reports each failure as the field to fix: no credentials at all names the
// provider; a 403 saying the API is not enabled names the project and the API; a 403 of any other
// kind names the project and the role; a good probe says the model and its size. A token
// endpoint's refusal is said by its status, never its body.
func TestCheckReportsVertexsCredentials(t *testing.T) {
	f := newServerFixture(t, serverView)
	g, google := newFakeGoogle(t)
	f.configureVertex(google.URL, 1536)
	check := func() (int, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := run(context.Background(), []string{"--server", "--check", f.path}, testOptions, &out, &errOut)
		return code, out.String() + errOut.String()
	}

	t.Run("no credentials", func(t *testing.T) {
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
		t.Setenv("HOME", t.TempDir()) // no gcloud application-default login either
		t.Setenv("CLOUDSDK_CONFIG", t.TempDir())
		freshTokens(t)
		code, out := check()
		if code != 2 || !strings.Contains(out, "embedding.provider") || !strings.Contains(out, "no Google credentials") {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	serviceAccount(t, google.URL+"/token")
	t.Run("the API not enabled", func(t *testing.T) {
		g.refuse(&refusal{http.StatusForbidden, `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo",` +
			`"reason":"SERVICE_DISABLED","domain":"googleapis.com","metadata":{"service":"aiplatform.googleapis.com","consumer":"projects/123"}}]}}`}, nil)
		code, out := check()
		if code != 2 || !strings.Contains(out, "embedding.project") || !strings.Contains(out, "aiplatform.googleapis.com") {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("refused", func(t *testing.T) {
		g.refuse(&refusal{http.StatusForbidden, `{"error":{"code":403,"status":"PERMISSION_DENIED"}}`}, nil)
		code, out := check()
		if code != 2 || !strings.Contains(out, "embedding.project") || !strings.Contains(out, "Vertex AI User") {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("the ranker's API not enabled", func(t *testing.T) {
		g.refuse(nil, &refusal{http.StatusForbidden, `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo",` +
			`"reason":"SERVICE_DISABLED","domain":"googleapis.com","metadata":{"service":"discoveryengine.googleapis.com","consumer":"projects/123"}}]}}`})
		code, out := check()
		if code != 2 || !strings.Contains(out, "ranker.project") || !strings.Contains(out, "discoveryengine.googleapis.com") {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("a good probe", func(t *testing.T) {
		g.refuse(nil, nil)
		code, out := check()
		if code != 0 || !strings.Contains(out, "vertex|gemini-embedding-2|doc-v1|1536") || !strings.Contains(out, "ranker: vertex at "+google.URL+" answered: semantic-ranker-default-005") {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("the token endpoint refusing", func(t *testing.T) {
		g.tokenRefuse.Store(true)
		defer g.tokenRefuse.Store(false)
		freshTokens(t)
		code, out := check()
		if code == 0 || strings.Contains(out, "zqtokenbodymarker") || !strings.Contains(out, "400") {
			t.Fatalf("exit %d: %s: want a failure saying the status, not the body", code, out)
		}
	})
}

// The credentials' token is cached: two calls fetch one; refresh (after a 401) fetches a new one.
func TestGoogleTokensRefreshBypassesTheCache(t *testing.T) {
	g, google := newFakeGoogle(t)
	serviceAccount(t, google.URL+"/token")
	ctx := context.Background()
	a, err := gcpTokens.token(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := gcpTokens.token(ctx, false)
	if err != nil || a != b || g.tokens.Load() != 1 {
		t.Fatalf("two calls: %q %q (%v), %d fetches: want one cached token", a, b, err, g.tokens.Load())
	}
	c, err := gcpTokens.token(ctx, true)
	if err != nil || c == a || g.tokens.Load() != 2 {
		t.Fatalf("refresh: %q (%v), %d fetches: want a new token", c, err, g.tokens.Load())
	}
	ended, cancel := context.WithCancel(ctx)
	cancel()
	if tok, err := gcpTokens.token(ended, true); tok != "" || !errors.Is(err, context.Canceled) {
		t.Errorf("a call whose context has ended: %q, %v; want the context's end and no token", tok, err)
	}
}

// A down vertex ranker is probed at most once a minute, every probe a billed count; the other
// kinds from a second, doubling to a minute.
func TestAVertexRankerIsProbedOnceAMinute(t *testing.T) {
	if first, most := proberRetry("vertex"); first != time.Minute || most != time.Minute {
		t.Errorf("vertex: %v, %v", first, most)
	}
	if first, most := proberRetry("tei"); first != time.Second || most != time.Minute {
		t.Errorf("tei: %v, %v", first, most)
	}
}

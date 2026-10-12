package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	gsearch "github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

const token = "a-long-random-bearer-token-0123456789abcdef"

// fake is a Searcher that answers res or err, records what it was asked, and, with hold set, waits
// for hold or its context first.
type fake struct {
	res  search.Result
	err  error
	hold chan struct{}

	mu    sync.Mutex
	asked []asked
}

type asked struct {
	scope search.Scope
	req   search.Request
}

func (f *fake) Search(ctx context.Context, scope search.Scope, r search.Request) (search.Result, error) {
	f.mu.Lock()
	f.asked = append(f.asked, asked{scope, r})
	f.mu.Unlock()
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return search.Result{}, ctx.Err()
		}
	}
	return f.res, f.err
}

func (f *fake) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asked)
}

// syncBuffer is a log the handler writes from its goroutines.
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

// api is the handler over the catalog (granted A and B of label_id), "any" (granted any label) and
// "secret" (served, granted to no one), as an HTTP server, with its log.
func api(t *testing.T, catalog Searcher, opts ...Option) (*httptest.Server, *Handler, *syncBuffer) {
	t.Helper()
	log := &syncBuffer{}
	clients := []Client{
		{Name: "someone-else", Token: sha256.Sum256([]byte("another-token-that-is-also-long-enough-xx")), Grants: map[string]map[string][]string{"secret": {"label_id": {"*"}}}},
		{Name: "lm-web", Token: sha256.Sum256([]byte(token)), Grants: map[string]map[string][]string{
			"lm-catalog": {"label_id": {"A", "B"}},
			"any":        {"label_id": {"*"}},
		}},
	}
	tenants := map[string]Searcher{"lm-catalog": catalog, "any": catalog, "secret": catalog}
	h := New(clients, tenants, append([]Option{WithLogger(logger.New(logger.WithWriter(log)))}, opts...)...)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, h, log
}

// reply is a response as the tests read it.
type reply struct {
	status int
	header http.Header
	body   []byte
}

func (r reply) code() string {
	var f failure
	_ = json.Unmarshal(r.body, &f)
	return f.Error.Code
}

func post(t *testing.T, srv *httptest.Server, auth, ctype, body string) reply {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+Path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	return do(t, req)
}

func do(t *testing.T, req *http.Request) reply {
	t.Helper()
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return reply{status: res.StatusCode, header: res.Header, body: b}
}

// client bounds every request, so a handler that never answers fails a test rather than hanging it.
var client = &http.Client{Timeout: 10 * time.Second}

const bearer = "Bearer " + token
const valid = `{"tenant": "lm-catalog", "query": "love song", "scope": {"label_id": ["A"]}}`

// A request without a valid bearer token is 401, with WWW-Authenticate, before its body is read: a
// missing header, another scheme, a wrong token, and a valid token's prefix; with the token, the
// same bad body is the body's refusal. The search is never asked. The mutant, named here: the body
// read before the token (a caller without a token learning that its body is wrong).
func TestATokenIsRequiredAndCheckedFirst(t *testing.T) {
	f := &fake{}
	srv, _, _ := api(t, f)
	for name, auth := range map[string]string{
		"none":         "",
		"basic":        "Basic " + token,
		"wrong":        "Bearer not-the-token",
		"a prefix":     "Bearer " + token[:20],
		"empty bearer": "Bearer ",
	} {
		r := post(t, srv, auth, "application/json", `{"bad`)
		if r.status != http.StatusUnauthorized || r.code() != "unauthorized" || !strings.HasPrefix(r.header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: %d %s %q", name, r.status, r.code(), r.header.Get("WWW-Authenticate"))
		}
	}
	if r := post(t, srv, "bearer "+token, "application/json", `{"bad`); r.status != http.StatusBadRequest {
		t.Errorf("the token, its scheme in lower case, with a bad body: %d %s", r.status, r.code())
	}
	if f.calls() != 0 {
		t.Errorf("the search was asked %d times", f.calls())
	}
}

// Only POST /v1/search is answered: another method is 405 with Allow, another path 404.
func TestOnlyPostToThePathIsAnswered(t *testing.T) {
	srv, _, _ := api(t, &fake{})
	req, _ := http.NewRequest(http.MethodGet, srv.URL+Path, nil)
	req.Header.Set("Authorization", bearer)
	if r := do(t, req); r.status != http.StatusMethodNotAllowed || r.header.Get("Allow") != http.MethodPost {
		t.Errorf("GET: %d, Allow %q", r.status, r.header.Get("Allow"))
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/v2/search", strings.NewReader(valid))
	req.Header.Set("Authorization", bearer)
	if r := do(t, req); r.status != http.StatusNotFound || r.code() != "not_found" {
		t.Errorf("another path: %d %s", r.status, r.code())
	}
}

// The body is one JSON search request: another media type is 415, more than MaxBody bytes 413, an
// unknown field, a second value or a query over MaxQuery bytes 400.
func TestTheBodyIsOneBoundedSearchRequest(t *testing.T) {
	f := &fake{}
	srv, _, _ := api(t, f)
	big := `{"tenant": "lm-catalog", "query": "` + strings.Repeat("a", MaxBody) + `"}`
	for name, c := range map[string]struct {
		ctype, body string
		status      int
	}{
		"text":           {"text/plain", valid, http.StatusUnsupportedMediaType},
		"too large":      {"application/json", big, http.StatusRequestEntityTooLarge},
		"an unknown key": {"application/json", `{"tenant": "lm-catalog", "scope": {"label_id": ["A"]}, "access": {}}`, http.StatusBadRequest},
		"two values":     {"application/json", valid + valid, http.StatusBadRequest},
		"a long query":   {"application/json", `{"tenant": "lm-catalog", "query": "` + strings.Repeat("a", MaxQuery+1) + `"}`, http.StatusBadRequest},
	} {
		if r := post(t, srv, bearer, c.ctype, c.body); r.status != c.status {
			t.Errorf("%s: %d %s, want %d", name, r.status, r.code(), c.status)
		}
	}
	if r := post(t, srv, bearer, "application/json; charset=utf-8", valid); r.status != http.StatusOK {
		t.Errorf("JSON with a charset: %d %s", r.status, r.code())
	}
	if f.calls() != 1 {
		t.Errorf("the search was asked %d times, want once (the valid request)", f.calls())
	}
}

// A tenant the token is not granted is 403, the same whether or not it exists; a scope value the
// token is not granted is 403 too, and the search is not asked. A "*" grant admits any value the
// request names.
func TestTheTenantAndTheScopeAreTheTokens(t *testing.T) {
	f := &fake{}
	srv, _, _ := api(t, f)
	secret := post(t, srv, bearer, "application/json", `{"tenant": "secret", "query": "x", "scope": {"label_id": ["A"]}}`)
	nobody := post(t, srv, bearer, "application/json", `{"tenant": "nobody", "query": "x", "scope": {"label_id": ["A"]}}`)
	if secret.status != http.StatusForbidden || secret.code() != "forbidden" {
		t.Errorf("a tenant not granted: %d %s", secret.status, secret.code())
	}
	if nobody.status != secret.status || !bytes.Equal(nobody.body, bytes.ReplaceAll(secret.body, []byte("secret"), []byte("nobody"))) {
		t.Errorf("a tenant that does not exist answers otherwise: %d %s / %s", nobody.status, nobody.body, secret.body)
	}
	if r := post(t, srv, bearer, "application/json", `{"tenant": "lm-catalog", "query": "x", "scope": {"label_id": ["A", "C"]}}`); r.status != http.StatusForbidden || r.code() != "scope_forbidden" {
		t.Errorf("a value not granted: %d %s", r.status, r.code())
	}
	if f.calls() != 0 {
		t.Fatalf("the search was asked %d times for requests refused", f.calls())
	}
	if r := post(t, srv, bearer, "application/json", `{"tenant": "any", "query": "x", "scope": {"label_id": ["Z"]}}`); r.status != http.StatusOK {
		t.Errorf("a value under a * grant: %d %s", r.status, r.code())
	}
}

// The search's refusals and failures are answered by their kind: the caller's to mend 400, a
// switch in semantic mode 503 with Retry-After, the provider 502, anything else 500 with its cause
// logged and not answered.
func TestTheSearchsErrorsAreAnsweredByKind(t *testing.T) {
	cause := errors.New("pq: the database's own words")
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("x: %w", search.ErrScope), http.StatusBadRequest, "scope_required"},
		{search.ErrProtectedFacet, http.StatusBadRequest, "protected_facet"},
		{search.ErrUnknownFacet, http.StatusBadRequest, "unknown_facet"},
		{search.ErrLimit, http.StatusBadRequest, "bad_limit"},
		{gsearch.ErrUnknownMode, http.StatusBadRequest, "bad_mode"},
		{gsearch.ErrSwitching, http.StatusServiceUnavailable, "switching"},
		{gsearch.ErrEmbedFailed, http.StatusBadGateway, "embed_failed"},
		{cause, http.StatusInternalServerError, "internal"},
	} {
		srv, _, log := api(t, &fake{err: c.err})
		r := post(t, srv, bearer, "application/json", valid)
		if r.status != c.status || r.code() != c.code {
			t.Errorf("%v: %d %s, want %d %s", c.err, r.status, r.code(), c.status, c.code)
		}
		if c.status == http.StatusServiceUnavailable && r.header.Get("Retry-After") == "" {
			t.Errorf("%v: no Retry-After", c.err)
		}
		if c.err == cause && (bytes.Contains(r.body, []byte("database")) || !strings.Contains(log.String(), "the database's own words")) {
			t.Errorf("the cause answered or not logged: %s / %s", r.body, log.String())
		}
	}
}

// The answer is the search's result, field for field, each hit with its source id; the search is
// asked with the request's scope, query, mode, filter and limit.
func TestTheAnswerIsTheSearchsResult(t *testing.T) {
	rank := 0.5
	res := search.Result{ModeUsed: gsearch.ModeHybrid, Semantic: gsearch.StateReady, Hits: []search.Hit{
		{Hit: gsearch.Hit{Path: "track/42.md", Breadcrumb: "Love Song", Snippet: "a \x02love\x03 song", Generation: 3, ByteStart: 1, ByteEnd: 9,
			Score: 0.031, Relevance: 0.87, Via: []string{"lexical", "semantic"}, RankScore: &rank}, SourceID: "42"},
	}}
	f := &fake{res: res}
	srv, _, _ := api(t, f)
	r := post(t, srv, bearer, "application/json", `{"tenant": "lm-catalog", "query": "love song", "mode": "lexical", "limit": 5,
		"scope": {"label_id": ["A", "B"]}, "filter": {"tags": ["single"], "paths": ["track/"]}}`)
	if r.status != http.StatusOK {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var got response
	if err := json.Unmarshal(r.body, &got); err != nil {
		t.Fatal(err)
	}
	want := response{Tenant: "lm-catalog", ModeUsed: "hybrid", Semantic: "ready", Hits: []hit{{Path: "track/42.md", Breadcrumb: "Love Song",
		Snippet: "a \x02love\x03 song", Generation: 3, ByteStart: 1, ByteEnd: 9, Score: 0.031, Relevance: 0.87, Via: []string{"lexical", "semantic"}, SourceID: "42"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the answer:\n%+v\nwant\n%+v", got, want)
	}
	wantAsked := asked{scope: search.Scope{"label_id": {"A", "B"}}, req: search.Request{Query: "love song", Mode: gsearch.ModeLexical, Limit: 5,
		Filter: gsearch.Filter{Tags: []string{"single"}, Paths: []string{"track/"}}}}
	if f.calls() != 1 || !reflect.DeepEqual(f.asked[0], wantAsked) {
		t.Errorf("the search was asked %+v, want %+v", f.asked, wantAsked)
	}
}

// The log names the client, the tenant, the status and the query's length, never the token or the
// query's text, for an answered request and a refused one alike.
func TestNeitherTheTokenNorTheQueryIsLogged(t *testing.T) {
	srv, _, log := api(t, &fake{})
	post(t, srv, bearer, "application/json", `{"tenant": "lm-catalog", "query": "a private matter", "scope": {"label_id": ["A"]}}`)
	post(t, srv, "Bearer "+token+"x", "application/json", valid)
	post(t, srv, bearer, "application/json", `{"tenant": "secret", "query": "a private matter", "scope": {"label_id": ["A"]}}`)
	got := log.String()
	for _, secret := range []string{token, "a private matter"} {
		if strings.Contains(got, secret) {
			t.Errorf("the log holds %q:\n%s", secret, got)
		}
	}
	for _, said := range []string{"client lm-web, tenant lm-catalog: 200", "client -, tenant -: 401", "403 forbidden", "a query of 16 bytes"} {
		if !strings.Contains(got, said) {
			t.Errorf("the log does not say %q:\n%s", said, got)
		}
	}
}

// A request's budget: a body still arriving after Body is cut off, the search never asked; a search
// held past Search is answered 503 within Write.
func TestARequestKeepsToItsBudget(t *testing.T) {
	f := &fake{hold: make(chan struct{})}
	srv, _, _ := api(t, f, WithBudget(Budget{Body: 200 * time.Millisecond, Search: 300 * time.Millisecond, Write: 2 * time.Second}))
	t.Cleanup(func() { close(f.hold) })

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: x\r\nAuthorization: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n{\"tenant\"",
		Path, bearer, len(valid))
	time.Sleep(500 * time.Millisecond) // the rest of the body never comes in time
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	head, _ := io.ReadAll(io.LimitReader(conn, 64))
	if !strings.Contains(string(head), "408") {
		t.Errorf("a body past its deadline: %q", head)
	}
	if f.calls() != 0 {
		t.Errorf("the search was asked for a body that did not arrive")
	}

	start := time.Now()
	r := post(t, srv, bearer, "application/json", valid)
	if r.status != http.StatusServiceUnavailable || r.code() != "unavailable" || time.Since(start) > 2*time.Second {
		t.Errorf("a search held past its deadline: %d %s in %v", r.status, r.code(), time.Since(start))
	}
}

// A server stopping drains its requests: Shutdown waits for a search in flight, which is answered
// 200, and Wait returns only after its handler has. The mutant, named here: requests whose context
// is the server's, which Shutdown cancels (a 503 instead of the answer).
func TestStoppingDrainsTheRequestsInFlight(t *testing.T) {
	f := &fake{hold: make(chan struct{})}
	h := New([]Client{{Name: "lm-web", Token: sha256.Sum256([]byte(token)), Grants: map[string]map[string][]string{"lm-catalog": {"label_id": {"A"}}}}},
		map[string]Searcher{"lm-catalog": f})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, BaseContext: func(net.Listener) context.Context { return context.Background() }}
	go func() { _ = srv.Serve(ln) }()

	answered := make(chan reply, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String()+Path, strings.NewReader(valid))
		req.Header.Set("Authorization", bearer)
		req.Header.Set("Content-Type", "application/json")
		answered <- do(t, req)
	}()
	for f.calls() == 0 {
		time.Sleep(time.Millisecond)
	}
	stopped := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		h.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("the server stopped with a search in flight")
	case <-time.After(200 * time.Millisecond):
	}
	close(f.hold)
	if r := <-answered; r.status != http.StatusOK {
		t.Errorf("the search in flight was answered %d %s", r.status, r.code())
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not stop once the search was answered")
	}
}

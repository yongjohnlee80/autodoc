package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
	"github.com/yongjohnlee80/autodoc/enterprise/ops"
)

// summaries is the refusals' timer as a cell holds it: the summary owed is run when the cell says,
// never by a clock.
type summaries struct {
	mu      sync.Mutex
	owed    func()
	armed   int // how many times a summary was owed
	stopped int // how many times one owed was stopped
}

// hold makes h's refusals owe their summaries to the cell.
func (s *summaries) hold(h *Handler) {
	h.refused.after = func(_ time.Duration, f func()) func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.owed = f
		s.armed++
		return func() bool {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.stopped++
			pending := s.owed != nil
			s.owed = nil
			return pending
		}
	}
}

// say runs the summary owed, as its timer would; false when none is owed.
func (s *summaries) say() bool {
	s.mu.Lock()
	f := s.owed
	s.owed = nil
	s.mu.Unlock()
	if f == nil {
		return false
	}
	f()
	return true
}

func (s *summaries) count() (armed, stopped int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.armed, s.stopped
}

// from is one request to the handler itself, as from the address given.
func from(h *Handler, addr, method, path, tok, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = addr + ":40000"
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func codeOf(rec *httptest.ResponseRecorder) string {
	var f failure
	_ = json.Unmarshal(rec.Body.Bytes(), &f)
	return f.Error.Code
}

// lines are the log's lines holding sub.
func lines(log *syncBuffer, sub string) []string {
	var out []string
	for _, l := range strings.Split(log.String(), "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return out
}

// Unauthorized requests have no line each in the log: they are counted, and said once, a minute
// after the first, with how many and from how many addresses. Four from two addresses, one of them
// on the status path: no line while they come, one summary owed, and when it is due, one line. An
// answered search still has its own line. The next unauthorized request owes the next summary. The
// counts since the start are the handler's Refused. The mutants, named here: a line each as before
// (the log holds a 401); the summary never owed (nothing is said).
func TestUnauthorizedRequestsAreSaidOnceAMinuteNotOneByOne(t *testing.T) {
	srv, h, log := limited(t, &fake{}, &fake{}, Client{})
	_ = srv
	var sum summaries
	sum.hold(h)
	for i, r := range []*httptest.ResponseRecorder{
		from(h, "203.0.113.7", http.MethodPost, Path, "", quickBody),
		from(h, "203.0.113.7", http.MethodPost, Path, "not-a-token-anyone-holds", quickBody),
		from(h, "203.0.113.8", http.MethodPost, Path, "another-token-no-one-holds", quickBody),
		from(h, "203.0.113.8", http.MethodGet, StatusPath, "another-token-no-one-holds", ""),
	} {
		if i < 3 && (r.Code != http.StatusUnauthorized || codeOf(r) != "unauthorized" || !strings.HasPrefix(r.Header().Get("WWW-Authenticate"), "Bearer")) {
			t.Errorf("unauthorized request %d: %d %s", i+1, r.Code, codeOf(r))
		}
	}
	if r := from(h, "203.0.113.9", http.MethodPost, Path, otherToken, quickBody); r.Code != http.StatusOK {
		t.Fatalf("a search with a token: %d %s", r.Code, codeOf(r))
	}
	if got := lines(log, "401 unauthorized"); len(got) != 0 {
		t.Errorf("unauthorized requests have lines of their own: %q", got)
	}
	if got := lines(log, "search: client other, tenant quick: 200"); len(got) != 1 {
		t.Errorf("the answered search's line: %q in %s", got, log)
	}
	if armed, _ := sum.count(); armed != 1 {
		t.Errorf("%d summaries owed for the four, want one", armed)
	}
	if got := lines(log, "unauthorized requests"); len(got) != 0 {
		t.Errorf("the summary was said before it was due: %q", got)
	}
	if !sum.say() {
		t.Fatal("no summary is owed")
	}
	if got := lines(log, "unauthorized requests"); len(got) != 1 || !strings.Contains(got[0], "http: 3 unauthorized requests in 1m0s, from 2 addresses") {
		// the status path's 404 (this handler has no ops) is not one of them
		t.Errorf("the summary: %q", got)
	}
	if got := h.Refused(); got != (ops.Refused{Unauthorized: 3}) {
		t.Errorf("the refusals since the start: %+v", got)
	}
	from(h, "203.0.113.7", http.MethodPost, Path, "", quickBody)
	if armed, _ := sum.count(); armed != 2 {
		t.Errorf("%d summaries owed after the next unauthorized request, want a second", armed)
	}
	sum.say()
	if got := lines(log, "unauthorized requests"); len(got) != 2 || !strings.Contains(got[1], "http: 1 unauthorized requests in 1m0s, from 1 address") {
		t.Errorf("the second summary: %q", got)
	}
}

// An address that has sent too many requests without a valid token is refused before its token is
// compared. Twenty are answered 401; the twenty-first is 429 too_many_requests with Retry-After 1,
// and so is a request bearing a valid token, which shows no token was compared. Another address is
// answered meanwhile. A fifth of a second later one try is back: the valid token is let in, as
// often as it comes, since only a request without a token takes a try; one more without takes it,
// and the next is refused again. After four seconds with none, every try is back. The summary says
// how many were refused unread. The mutants, named here: the tries not kept (the twenty-first is
// 401); the token compared first (the valid token is let in with no try left).
func TestAnAddressOutOfTriesIsRefusedBeforeItsTokenIsCompared(t *testing.T) {
	clk := &testClock{now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	_, h, log := limited(t, &fake{}, &fake{}, Client{}, WithClock(clk.Now))
	var sum summaries
	sum.hold(h)
	const knocker, neighbour = "203.0.113.7", "203.0.113.8"
	for i := range triesBurst {
		if r := from(h, knocker, http.MethodPost, Path, "no-token-of-anyones", quickBody); r.Code != http.StatusUnauthorized {
			t.Fatalf("unauthorized request %d: %d %s", i+1, r.Code, codeOf(r))
		}
	}
	r := from(h, knocker, http.MethodPost, Path, "no-token-of-anyones", quickBody)
	if r.Code != http.StatusTooManyRequests || codeOf(r) != "too_many_requests" || r.Header().Get("Retry-After") != "1" {
		t.Fatalf("the twenty-first: %d %s, Retry-After %q", r.Code, codeOf(r), r.Header().Get("Retry-After"))
	}
	if r := from(h, knocker, http.MethodPost, Path, otherToken, quickBody); r.Code != http.StatusTooManyRequests {
		t.Errorf("a valid token from the address out of tries: %d %s (want 429: no token is compared)", r.Code, codeOf(r))
	}
	if r := from(h, knocker, http.MethodGet, StatusPath, "no-token-of-anyones", ""); r.Code != http.StatusNotFound {
		// this handler has no ops: the path is not one that takes a token
		t.Errorf("the status path with no ops: %d", r.Code)
	}
	if r := from(h, neighbour, http.MethodPost, Path, otherToken, quickBody); r.Code != http.StatusOK {
		t.Errorf("another address meanwhile: %d %s", r.Code, codeOf(r))
	}
	if r := from(h, neighbour, http.MethodPost, Path, "no-token-of-anyones", quickBody); r.Code != http.StatusUnauthorized {
		t.Errorf("another address without a token: %d %s (want its own 401)", r.Code, codeOf(r))
	}

	clk.advance(250 * time.Millisecond) // one try back, and a quarter of the next
	for range 3 {
		if r := from(h, knocker, http.MethodPost, Path, otherToken, quickBody); r.Code != http.StatusOK {
			t.Fatalf("a valid token with a try back: %d %s", r.Code, codeOf(r))
		}
	}
	if r := from(h, knocker, http.MethodPost, Path, "no-token-of-anyones", quickBody); r.Code != http.StatusUnauthorized {
		t.Errorf("without a token, a try back: %d %s", r.Code, codeOf(r))
	}
	if r := from(h, knocker, http.MethodPost, Path, otherToken, quickBody); r.Code != http.StatusTooManyRequests {
		t.Errorf("after the try was taken: %d %s", r.Code, codeOf(r))
	}
	clk.advance(4 * time.Second)
	for i := range triesBurst {
		if r := from(h, knocker, http.MethodPost, Path, "no-token-of-anyones", quickBody); r.Code != http.StatusUnauthorized {
			t.Fatalf("four seconds later, unauthorized request %d: %d %s (want every try back)", i+1, r.Code, codeOf(r))
		}
	}

	sum.say()
	want := fmt.Sprintf("http: %d unauthorized requests in 1m0s, from 2 addresses; 3 of them refused unread, their address over its tries", 2*triesBurst+1+1+3)
	if got := lines(log, "unauthorized requests"); len(got) != 1 || !strings.Contains(got[0], want) {
		t.Errorf("the summary: %q, want %q", got, want)
	}
	if got := h.Refused().Unauthorized; got != uint64(2*triesBurst+1+1+3) {
		t.Errorf("%d unauthorized since the start", got)
	}
}

// Behind a proxy every request comes from the proxy's address, so no tries are kept: thirty
// requests without a token are thirty 401s, and the summary names no address.
func TestBehindAProxyNoAddressIsCounted(t *testing.T) {
	_, h, log := limited(t, &fake{}, &fake{}, Client{}, WithoutAddressTries())
	var sum summaries
	sum.hold(h)
	for i := range 30 {
		if r := from(h, "10.0.0.2", http.MethodPost, Path, "no-token-of-anyones", quickBody); r.Code != http.StatusUnauthorized {
			t.Fatalf("request %d through the proxy: %d %s", i+1, r.Code, codeOf(r))
		}
	}
	sum.say()
	if got := lines(log, "unauthorized requests"); len(got) != 1 || !strings.HasSuffix(got[0], "http: 30 unauthorized requests in 1m0s") {
		t.Errorf("the summary: %q", got)
	}
}

// The tries remember ten thousand addresses at most, and forget the one longest unseen first: the
// first of ten thousand and one, out of tries, has every try again, and the last is still out.
func TestTheTriesForgetTheAddressLongestUnseen(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	tr := newTries()
	spend := func(addr string) {
		for range triesBurst {
			tr.take(addr, now)
		}
	}
	spend("first")
	if _, spent := tr.spent("first", now); !spent {
		t.Fatal("the first address has a try left after twenty were taken")
	}
	for i := range triesAddrs - 1 {
		tr.take(fmt.Sprintf("a%d", i), now)
	}
	spend("last")
	if n := len(tr.byAddr); n != triesAddrs || tr.seen.Len() != triesAddrs {
		t.Errorf("%d addresses remembered (%d in order), want %d", n, tr.seen.Len(), triesAddrs)
	}
	if _, spent := tr.spent("first", now); spent {
		t.Error("the address longest unseen was not the one forgotten")
	}
	if _, spent := tr.spent("last", now); !spent {
		t.Error("the last address, out of tries, was forgotten")
	}
}

// A client's refusals have no line each: they are said once a minute, by client, with how many of
// each kind, and counted by reason since the start. The flooding client, with one place and a
// burst of two: one search held, then three refused for its place and two for its rate. The other
// client is refused nothing, and has no line. The mutants, named here: a line each as before; the
// two kinds counted as one.
func TestAClientsRefusalsAreSaidOnceAMinuteByClient(t *testing.T) {
	clk := &testClock{now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	held, quick := &fake{hold: make(chan struct{})}, &fake{}
	srv, h, log := limited(t, held, quick, Client{MaxInFlight: 1, Rate: 1, Burst: 4}, WithClock(clk.Now))
	var sum summaries
	sum.hold(h)
	answers := inFlight(t, srv, held, 1) // the first of the burst's four
	for i := range 3 {
		if r := searchAs(t, srv, floodToken, quickBody); r.code() != "too_many_in_flight" {
			t.Fatalf("refusal %d: %d %s", i+1, r.status, r.code())
		}
	}
	for i := range 2 {
		if r := searchAs(t, srv, floodToken, quickBody); r.code() != "too_many_requests" {
			t.Fatalf("refusal %d, the burst spent: %d %s", i+4, r.status, r.code())
		}
	}
	if r := searchAs(t, srv, otherToken, quickBody); r.status != http.StatusOK {
		t.Fatalf("the other client: %d %s", r.status, r.code())
	}
	if got := lines(log, "429 too_many"); len(got) != 0 {
		t.Errorf("refused requests have lines of their own: %q", got)
	}
	if got := h.Refused(); got != (ops.Refused{InFlight: 3, Rate: 2}) {
		t.Errorf("the refusals since the start: %+v, want 3 in flight and 2 for the rate", got)
	}
	sum.say()
	want := "search: client flood: 5 refused in 1m0s: 3 over its searches in flight (too_many_in_flight), 2 over its rate (too_many_requests)"
	if got := lines(log, "refused in"); len(got) != 1 || !strings.HasSuffix(got[0], want) {
		t.Errorf("the summary: %q, want %q", got, want)
	}
	close(held.hold)
	<-answers
}

// A server stopping says the refusals counted and not yet said, once: the summary owed is stopped,
// what it would have said is said by the stop, and nothing is said twice if the timer fires all
// the same. The mutant, named here: the stop saying nothing (a flood's last minute is lost).
func TestTheRefusalsNotYetSaidAreSaidWhenTheServerStops(t *testing.T) {
	_, h, log := limited(t, &fake{}, &fake{}, Client{})
	var sum summaries
	var late func()
	sum.hold(h)
	from(h, "203.0.113.7", http.MethodPost, Path, "", quickBody)
	sum.mu.Lock()
	late = sum.owed // the timer's own call, as if it fired while the server stopped
	sum.mu.Unlock()
	h.Wait()
	if got := lines(log, "unauthorized requests"); len(got) != 1 || !strings.Contains(got[0], "http: 1 unauthorized requests in 1m0s, from 1 address") {
		t.Fatalf("the stop's summary: %q", got)
	}
	if _, stopped := sum.count(); stopped != 1 {
		t.Errorf("the summary owed was stopped %d times, want once", stopped)
	}
	late()
	h.Wait()
	if got := lines(log, "unauthorized requests"); len(got) != 1 {
		t.Errorf("the summary was said %d times", len(got))
	}
	// a refusal after the stop is counted, and owes nothing
	from(h, "203.0.113.7", http.MethodPost, Path, "", quickBody)
	if armed, _ := sum.count(); armed != 1 {
		t.Errorf("a refusal after the stop owed a summary (%d owed in all)", armed)
	}
	if got := h.Refused().Unauthorized; got != 2 {
		t.Errorf("%d unauthorized since the start, want 2", got)
	}
}

// The refusals reach the status and the metrics, by reason and never by client: the status's
// server.http.refused, and autodoc_http_refused_total with its three reasons. An operator's own
// request without a token is one of the unauthorized. The mutant, named here: the collector not
// given the handler's counts (no http in the status, no series).
func TestTheRefusalsReachTheStatusAndTheMetrics(t *testing.T) {
	clk := &testClock{now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	held := &fake{hold: make(chan struct{})}
	var h *Handler
	c := ops.New(ops.Options{
		Provider: func() embedder.Health { return embedder.Health{State: embedder.ProviderUp, Model: "m1"} },
		Now:      func(context.Context) (time.Time, error) { return time.Now(), nil },
		Refused:  func() ops.Refused { return h.Refused() },
	})
	srv, h2, _ := limited(t, held, &fake{}, Client{MaxInFlight: 1, Rate: 1, Burst: 3}, WithClock(clk.Now),
		WithOps(c), WithMetrics(true), WithOperators([]Operator{{Name: "ops", Token: sha256.Sum256([]byte(opsToken)), Tenants: []string{"*"}}}))
	h = h2
	answers := inFlight(t, srv, held, 1)
	for range 2 {
		searchAs(t, srv, floodToken, quickBody) // at its place
	}
	searchAs(t, srv, floodToken, quickBody) // the burst of three spent
	if r := get(t, srv, StatusPath, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("status with no token: %d", r.status)
	}
	r := get(t, srv, StatusPath, opsToken)
	if r.status != http.StatusOK || !bytes.Contains(r.body, []byte(`"http":{"refused":{"in_flight":2,"rate":1,"unauthorized":1}}`)) {
		t.Errorf("the status: %d %s", r.status, r.body)
	}
	m := get(t, srv, MetricsPath, opsToken)
	for _, want := range []string{
		`autodoc_http_refused_total{reason="in_flight"} 2`,
		`autodoc_http_refused_total{reason="rate"} 1`,
		`autodoc_http_refused_total{reason="unauthorized"} 1`,
	} {
		if !bytes.Contains(m.body, []byte(want)) {
			t.Errorf("the metrics lack %s:\n%s", want, m.body)
		}
	}
	if bytes.Contains(m.body, []byte("flood")) || bytes.Contains(r.body, []byte("flood")) {
		t.Error("a client's name is in the status or the metrics")
	}
	close(held.hold)
	<-answers
}

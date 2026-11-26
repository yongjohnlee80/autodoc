package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

// The limits' cells have two clients, each with its own token, both granted every label of two
// tenants: "held", whose searches wait for the cell, and "quick", whose searches answer at once.
const (
	floodToken = "the-flooding-clients-long-random-token-0123456789"
	otherToken = "the-other-clients-long-random-token-9876543210ab"
)

const (
	heldBody  = `{"tenant": "held", "query": "love song", "scope": {"label_id": ["A"]}}`
	quickBody = `{"tenant": "quick", "query": "love song", "scope": {"label_id": ["A"]}}`
)

// limited is the handler for the flooding client, with the limits given, and another client with
// none, over the two tenants, as an HTTP server.
func limited(t *testing.T, held, quick Searcher, flood Client, opts ...Option) (*httptest.Server, *Handler, *syncBuffer) {
	t.Helper()
	grants := map[string]map[string][]string{"held": {"label_id": {"*"}}, "quick": {"label_id": {"*"}}}
	flood.Name, flood.Token, flood.Grants = "flood", sha256.Sum256([]byte(floodToken)), grants
	other := Client{Name: "other", Token: sha256.Sum256([]byte(otherToken)), Grants: grants}
	log := &syncBuffer{}
	h := New([]Client{flood, other}, map[string]Searcher{"held": held, "quick": quick},
		append([]Option{WithLogger(logger.New(logger.WithWriter(log)))}, opts...)...)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, h, log
}

func searchAs(t *testing.T, srv *httptest.Server, tok, body string) reply {
	t.Helper()
	return post(t, srv, "Bearer "+tok, "application/json", body)
}

// inFlight starts n of the flooding client's searches of the held tenant and returns once the
// tenant's searcher has been asked all of them; each one's answer comes on the channel returned.
func inFlight(t *testing.T, srv *httptest.Server, held *fake, n int) <-chan reply {
	t.Helper()
	before := held.calls()
	out := make(chan reply, n)
	for range n {
		go func() {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+Path, strings.NewReader(heldBody))
			req.Header.Set("Authorization", "Bearer "+floodToken)
			req.Header.Set("Content-Type", "application/json")
			res, err := client.Do(req)
			if err != nil {
				out <- reply{status: -1}
				return
			}
			res.Body.Close()
			out <- reply{status: res.StatusCode, header: res.Header}
		}()
	}
	eventually(t, fmt.Sprintf("%d searches in flight", n), func() bool { return held.calls() == before+n })
	return out
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within 10 s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A client at its cap is refused at once, and another client is answered meanwhile. The flooding
// client may have two searches in flight; with two held, its third is 429 too_many_in_flight with
// Retry-After 1, saying the cap, and the searcher is not asked. The other client's search is
// answered while they are held. Once the two are answered, the client searches again. The mutants,
// named here: no cap (the third search is taken); the cap one too high (the same).
func TestAClientAtItsCapIsRefusedAtOnceAndAnotherIsAnswered(t *testing.T) {
	held, quick := &fake{hold: make(chan struct{})}, &fake{}
	srv, _, _ := limited(t, held, quick, Client{MaxInFlight: 2})
	answers := inFlight(t, srv, held, 2)

	for range 3 { // each refused: a refusal holds no place
		r := searchAs(t, srv, floodToken, heldBody)
		if r.status != http.StatusTooManyRequests || r.code() != "too_many_in_flight" || r.header.Get("Retry-After") != "1" {
			t.Fatalf("a third search in flight: %d %s, Retry-After %q", r.status, r.code(), r.header.Get("Retry-After"))
		}
		if !strings.Contains(string(r.body), "this client has 2 searches in flight (at most 2)") {
			t.Errorf("the refusal doesn't say the cap: %s", r.body)
		}
	}
	if n := held.calls(); n != 2 {
		t.Errorf("the searcher was asked %d times, want the 2 in flight", n)
	}
	// the cap is the client's own: its search of another tenant is refused too, and the other
	// client's is answered
	if r := searchAs(t, srv, floodToken, quickBody); r.code() != "too_many_in_flight" {
		t.Errorf("the client's search of another tenant, at its cap: %d %s", r.status, r.code())
	}
	if r := searchAs(t, srv, otherToken, quickBody); r.status != http.StatusOK {
		t.Errorf("another client's search meanwhile: %d %s", r.status, r.code())
	}

	close(held.hold)
	for range 2 {
		if r := <-answers; r.status != http.StatusOK {
			t.Errorf("a search in flight was answered %d", r.status)
		}
	}
	if r := searchAs(t, srv, floodToken, heldBody); r.status != http.StatusOK {
		t.Errorf("the client's search once its two were answered: %d %s", r.status, r.code())
	}
}

// readsCounted is a request body that counts the reads made of it.
type readsCounted struct {
	r     *strings.Reader
	reads atomic.Int32
}

func (b *readsCounted) Read(p []byte) (int, error) {
	b.reads.Add(1)
	return b.r.Read(p)
}

// A refused request takes no place among those in flight, and the handler never reads its body.
// With the client's one search held, a request over the cap is refused with not one read of its
// body, where a request that is let in is read; a client over its rate is refused the same way.
// After many refusals and the held search's answer, the client has exactly its one place again:
// one search enters, a second is refused. The mutant, named here: the body read before the limits.
//
// The handler's reads are counted on the handler itself. Over HTTP/1.1, net/http reads what a
// client announced of a body before it writes any answer the handler left unread, so that the
// connection can be used again: that is the server's, bounded by the request's body deadline, and
// no reading of the handler's.
func TestARefusedRequestTakesNoPlaceAndItsBodyIsNotRead(t *testing.T) {
	clk := &testClock{now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	held, quick := &fake{hold: make(chan struct{})}, &fake{}
	srv, h, _ := limited(t, held, quick, Client{MaxInFlight: 1, Rate: 100, Burst: 100}, WithClock(clk.Now))
	answers := inFlight(t, srv, held, 1)

	// serve is one request to the handler itself, its body's reads counted
	serve := func(tok, body string) (int, string, int32) {
		t.Helper()
		b := &readsCounted{r: strings.NewReader(body)}
		req := httptest.NewRequest(http.MethodPost, Path, b)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var f failure
		_ = json.Unmarshal(rec.Body.Bytes(), &f)
		return rec.Code, f.Error.Code, b.reads.Load()
	}
	if status, code, reads := serve(floodToken, heldBody); status != http.StatusTooManyRequests || code != "too_many_in_flight" || reads != 0 {
		t.Errorf("at the cap: %d %s, its body read %d times (want a refusal and no read)", status, code, reads)
	}
	if status, _, reads := serve(otherToken, quickBody); status != http.StatusOK || reads == 0 {
		t.Errorf("a request let in: %d, its body read %d times (want an answer, its body read)", status, reads)
	}
	for range 20 {
		if r := searchAs(t, srv, floodToken, heldBody); r.code() != "too_many_in_flight" {
			t.Fatalf("at the cap: %d %s", r.status, r.code())
		}
	}
	close(held.hold)
	if r := <-answers; r.status != http.StatusOK {
		t.Fatalf("the held search was answered %d", r.status)
	}
	// exactly one place again: neither lost to the refusals, nor added by them
	held.mu.Lock()
	held.hold = make(chan struct{})
	held.mu.Unlock()
	again := inFlight(t, srv, held, 1)
	if r := searchAs(t, srv, floodToken, heldBody); r.code() != "too_many_in_flight" {
		t.Errorf("a second search with one in flight, after the refusals: %d %s", r.status, r.code())
	}
	close(held.hold)
	if r := <-again; r.status != http.StatusOK {
		t.Errorf("the search after the refusals was answered %d", r.status)
	}

	// over its rate (the burst of 100 is spent by now, in one instant of the cell's clock): refused
	// with no read either
	for range 100 {
		serve(floodToken, quickBody)
	}
	if status, code, reads := serve(floodToken, quickBody); status != http.StatusTooManyRequests || code != "too_many_requests" || reads != 0 {
		t.Errorf("over its rate: %d %s, its body read %d times (want a refusal and no read)", status, code, reads)
	}
}

// testClock is a clock a cell moves.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// A client over its rate is refused, and gets through again after the wait. At 2 searches a second
// with a burst of 2: two are answered, the third is 429 too_many_requests with Retry-After 1 (half
// a second, in whole seconds), the searcher not asked; 400 ms later it is still refused; at 600 ms
// it is answered. A slower rate says how long: at one search every five seconds, Retry-After is 5.
// The other client, with no rate, is never refused. The mutants, named here: the rate not checked
// (the third search is answered); the bucket never refilled (the search after the wait is refused).
func TestAClientOverItsRateIsRefusedAndGetsThroughAfterTheWait(t *testing.T) {
	clk := &testClock{now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	held, quick := &fake{}, &fake{}
	srv, _, _ := limited(t, held, quick, Client{Rate: 2, Burst: 2}, WithClock(clk.Now))
	for i := range 2 {
		if r := searchAs(t, srv, floodToken, quickBody); r.status != http.StatusOK {
			t.Fatalf("search %d within the burst: %d %s", i+1, r.status, r.code())
		}
	}
	r := searchAs(t, srv, floodToken, quickBody)
	if r.status != http.StatusTooManyRequests || r.code() != "too_many_requests" || r.header.Get("Retry-After") != "1" {
		t.Fatalf("the third search in the same instant: %d %s, Retry-After %q", r.status, r.code(), r.header.Get("Retry-After"))
	}
	if !strings.Contains(string(r.body), "over its rate of 2 searches a second") {
		t.Errorf("the refusal doesn't say the rate: %s", r.body)
	}
	if n := quick.calls(); n != 2 {
		t.Errorf("the searcher was asked %d times, want the burst's 2", n)
	}
	clk.advance(400 * time.Millisecond)
	if r := searchAs(t, srv, floodToken, quickBody); r.code() != "too_many_requests" {
		t.Errorf("400 ms later, before a search is earned: %d %s", r.status, r.code())
	}
	clk.advance(200 * time.Millisecond)
	if r := searchAs(t, srv, floodToken, quickBody); r.status != http.StatusOK {
		t.Errorf("600 ms later, a search earned: %d %s", r.status, r.code())
	}
	if r := searchAs(t, srv, floodToken, quickBody); r.code() != "too_many_requests" {
		t.Errorf("the next, in the same instant: %d %s", r.status, r.code())
	}
	for range 10 {
		if r := searchAs(t, srv, otherToken, quickBody); r.status != http.StatusOK {
			t.Fatalf("the client with no rate: %d %s", r.status, r.code())
		}
	}

	// one search every five seconds
	slow, _, _ := limited(t, held, quick, Client{Rate: 0.2, Burst: 1}, WithClock(clk.Now))
	if r := searchAs(t, slow, floodToken, quickBody); r.status != http.StatusOK {
		t.Fatalf("the first search: %d %s", r.status, r.code())
	}
	if r := searchAs(t, slow, floodToken, quickBody); r.code() != "too_many_requests" || r.header.Get("Retry-After") != "5" {
		t.Errorf("the second, at once: %d %s, Retry-After %q (want 5)", r.status, r.code(), r.header.Get("Retry-After"))
	}
	clk.advance(4900 * time.Millisecond)
	if r := searchAs(t, slow, floodToken, quickBody); r.code() != "too_many_requests" || r.header.Get("Retry-After") != "1" {
		t.Errorf("4.9 s later: %d %s, Retry-After %q (want 1)", r.status, r.code(), r.header.Get("Retry-After"))
	}
	clk.advance(200 * time.Millisecond)
	if r := searchAs(t, slow, floodToken, quickBody); r.status != http.StatusOK {
		t.Errorf("5.1 s later: %d %s", r.status, r.code())
	}
	// a clock that goes back earns nothing, and takes nothing earned away
	clk.advance(-time.Hour)
	if r := searchAs(t, slow, floodToken, quickBody); r.code() != "too_many_requests" {
		t.Errorf("after the clock went back: %d %s", r.status, r.code())
	}
}

// The rate is checked before the searches in flight, so a request the rate refuses takes no place
// among them. A client with one place and a burst of one, its one search held: the next request is
// over both, and is told of its rate; once the rate allows a search, it is told of its place. The
// mutant, named here: the searches in flight checked first.
func TestTheRateIsCheckedBeforeTheSearchesInFlight(t *testing.T) {
	clk := &testClock{now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	held, quick := &fake{hold: make(chan struct{})}, &fake{}
	srv, _, _ := limited(t, held, quick, Client{MaxInFlight: 1, Rate: 1, Burst: 1}, WithClock(clk.Now))
	answers := inFlight(t, srv, held, 1)
	if r := searchAs(t, srv, floodToken, quickBody); r.code() != "too_many_requests" {
		t.Errorf("over its rate and at its cap: %d %s, want the rate's refusal", r.status, r.code())
	}
	clk.advance(time.Second)
	if r := searchAs(t, srv, floodToken, quickBody); r.code() != "too_many_in_flight" {
		t.Errorf("within its rate, at its cap: %d %s", r.status, r.code())
	}
	close(held.hold)
	if r := <-answers; r.status != http.StatusOK {
		t.Errorf("the held search was answered %d", r.status)
	}
}

// ways is a Searcher whose next search ends as the cell says.
type ways struct {
	mu   sync.Mutex
	next func(ctx context.Context) (search.Result, error)
}

func (w *ways) Search(ctx context.Context, _ search.Scope, _ search.Request) (search.Result, error) {
	w.mu.Lock()
	next := w.next
	w.mu.Unlock()
	if next == nil {
		return search.Result{}, nil
	}
	return next(ctx)
}

func (w *ways) set(next func(ctx context.Context) (search.Result, error)) {
	w.mu.Lock()
	w.next = next
	w.mu.Unlock()
}

// A search in flight ends on every way out of the handler, exactly once. A client with one place:
// after a search that failed, a request refused for its body, its tenant or its scope, a search
// that panicked, and a search whose caller went away, the place is free again, and after each the
// client has one place and no more. The mutant, named here: the place never given back (the
// client's second search is refused for good).
func TestASearchInFlightEndsOnEveryWayOut(t *testing.T) {
	w := &ways{}
	srv, _, _ := limited(t, w, &fake{}, Client{MaxInFlight: 1})
	// one place, and no more: a search enters and is held; a second is refused; then the first ends
	onePlace := func(after string) {
		t.Helper()
		entered, release := make(chan struct{}), make(chan struct{})
		w.set(func(ctx context.Context) (search.Result, error) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return search.Result{}, nil
		})
		done := make(chan reply, 1)
		go func() {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+Path, strings.NewReader(heldBody))
			req.Header.Set("Authorization", "Bearer "+floodToken)
			req.Header.Set("Content-Type", "application/json")
			res, err := client.Do(req)
			if err != nil {
				done <- reply{status: -1}
				return
			}
			res.Body.Close()
			done <- reply{status: res.StatusCode}
		}()
		select {
		case <-entered:
		case r := <-done:
			t.Fatalf("after %s, the client's search did not enter: %d", after, r.status)
		case <-time.After(10 * time.Second):
			t.Fatalf("after %s, the client's search did not enter within 10 s", after)
		}
		w.set(nil)
		if r := searchAs(t, srv, floodToken, heldBody); r.code() != "too_many_in_flight" {
			t.Errorf("after %s, a second search with one in flight: %d %s", after, r.status, r.code())
		}
		close(release)
		if r := <-done; r.status != http.StatusOK {
			t.Errorf("after %s, the search was answered %d", after, r.status)
		}
	}
	onePlace("nothing")

	w.set(func(context.Context) (search.Result, error) {
		return search.Result{}, errors.New("the index is on fire")
	})
	if r := searchAs(t, srv, floodToken, heldBody); r.status != http.StatusInternalServerError {
		t.Fatalf("a failed search: %d %s", r.status, r.code())
	}
	onePlace("a failed search")

	w.set(nil)
	for what, r := range map[string]reply{
		"a body that is not a request": post(t, srv, "Bearer "+floodToken, "application/json", `{"bad`),
		"another media type":           post(t, srv, "Bearer "+floodToken, "text/plain", heldBody),
		"a tenant not granted":         searchAs(t, srv, floodToken, `{"tenant": "nowhere", "query": "x", "scope": {"label_id": ["A"]}}`),
		"a scope not granted":          searchAs(t, srv, floodToken, `{"tenant": "held", "query": "x", "scope": {"other": ["A"]}}`),
	} {
		if r.status < 400 || r.status == http.StatusTooManyRequests {
			t.Fatalf("%s: %d %s", what, r.status, r.code())
		}
	}
	onePlace("four requests refused after they entered")

	w.set(func(context.Context) (search.Result, error) { panic("a search that panics") })
	req, _ := http.NewRequest(http.MethodPost, srv.URL+Path, strings.NewReader(heldBody))
	req.Header.Set("Authorization", "Bearer "+floodToken)
	req.Header.Set("Content-Type", "application/json")
	if res, err := client.Do(req); err == nil {
		res.Body.Close()
		t.Fatalf("a search that panicked was answered %d", res.StatusCode)
	}
	onePlace("a search that panicked")

	// a caller that goes away: its search is told (its context ends), and its place is given back
	// once the handler returns
	entered, left := make(chan struct{}), make(chan struct{})
	w.set(func(ctx context.Context) (search.Result, error) {
		close(entered)
		<-ctx.Done()
		close(left)
		return search.Result{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+Path, strings.NewReader(heldBody))
	req.Header.Set("Authorization", "Bearer "+floodToken)
	req.Header.Set("Content-Type", "application/json")
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		if res, err := client.Do(req); err == nil {
			res.Body.Close()
		}
	}()
	<-entered
	cancel()
	<-gone
	select {
	case <-left:
	case <-time.After(10 * time.Second):
		t.Fatal("the search of a caller gone was not told within 10 s")
	}
	w.set(nil)
	eventually(t, "the place of a caller gone given back", func() bool {
		return searchAs(t, srv, floodToken, heldBody).status == http.StatusOK
	})
	onePlace("a caller gone")
}

// A client with no cap and no rate is never refused for either, and the handler made without
// limits answers as it always did: forty searches at once, all answered.
func TestAClientWithNoLimitsIsNeverRefusedForThem(t *testing.T) {
	held, quick := &fake{hold: make(chan struct{})}, &fake{}
	srv, _, _ := limited(t, held, quick, Client{}, WithSearches(64))
	answers := inFlight(t, srv, held, 40)
	close(held.hold)
	for range 40 {
		if r := <-answers; r.status != http.StatusOK {
			t.Fatalf("a search of a client with no cap was answered %d", r.status)
		}
	}
}

// The searches of a single tenant share the searches in flight across every request, as the
// tenants of a search across several do. With two at most (WithSearches) and two searches held,
// from two clients: a third search is not run. It waits its turn within its own budget and, the
// budget spent, is answered 503 unavailable, its searcher never asked. Once the two are answered,
// a search runs again. Neither client has a cap of its own here: the bound is the server's,
// whoever asks. The mutant, named here: a single tenant's search run without a slot (the third is
// run while the two are held).
func TestTheSearchesOfASingleTenantShareTheSearchesInFlight(t *testing.T) {
	// the held searches end when the cell says, not when their own budget does: they must hold
	// their slots for longer than the third search waits
	var entered atomic.Int32
	release := make(chan struct{})
	held := &ways{}
	held.set(func(context.Context) (search.Result, error) {
		entered.Add(1)
		<-release
		return search.Result{}, nil
	})
	quick := &fake{}
	srv, _, _ := limited(t, held, quick, Client{}, WithSearches(2), WithBudget(Budget{Search: 300 * time.Millisecond}))
	// the held searches are let go however the cell ends: the server's own cleanup, which runs
	// after this one, waits for them
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	answers := make(chan reply, 2)
	for _, tok := range []string{floodToken, otherToken} {
		go func() { answers <- searchAs(t, srv, tok, heldBody) }()
	}
	eventually(t, "the two searches in flight", func() bool { return entered.Load() == 2 })

	start := time.Now()
	r := searchAs(t, srv, otherToken, quickBody)
	if r.status != http.StatusServiceUnavailable || r.code() != "unavailable" {
		t.Fatalf("a third search with the two slots held: %d %s (want 503 unavailable, its budget spent waiting)", r.status, r.code())
	}
	if took := time.Since(start); took < 250*time.Millisecond {
		t.Errorf("the third search was answered in %v: it did not wait its turn for its budget", took)
	}
	if n := quick.calls(); n != 0 {
		t.Errorf("the third search was run %d times while the two slots were held", n)
	}
	letGo()
	for range 2 {
		if r := <-answers; r.status != http.StatusOK {
			t.Errorf("a held search was answered %d %s", r.status, r.code())
		}
	}
	if r := searchAs(t, srv, otherToken, quickBody); r.status != http.StatusOK {
		t.Errorf("a search once the slots were free: %d %s", r.status, r.code())
	}
}

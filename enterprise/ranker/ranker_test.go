package ranker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	gsearch "github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

// client is a ranker client: it fails with failing() when set, blocks while hold is closed-over
// and open, and counts its calls.
type client struct {
	failing func() error
	hold    chan struct{}
	calls   atomic.Int64
}

func (c *client) Model() rank.Model { return rank.Model{Provider: "tei", Name: "bge-reranker"} }

func (c *client) Rank(ctx context.Context, _ string, texts []string) ([]float64, error) {
	c.calls.Add(1)
	if c.hold != nil {
		select {
		case <-c.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.failing != nil {
		if err := c.failing(); err != nil {
			return nil, err
		}
	}
	return make([]float64, len(texts)), nil
}

// lines is a logger's messages, each formatted.
type lines struct {
	mu  sync.Mutex
	all []string
}

func (l *lines) logger() logger.Logger {
	return logger.Adapt(func(_ logger.Severity, v any) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.all = append(l.all, fmt.Sprint(v))
	})
}

func (l *lines) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.all, "\n")
}

func waitState(t *testing.T, r *Ranker, state string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for r.Health().State != state {
		if time.Now().After(deadline) {
			t.Fatalf("the ranker is %s, want %s", r.Health().State, state)
		}
		time.Sleep(time.Millisecond)
	}
}

func run(t *testing.T, r *Ranker) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// A ranker whose probe fails is down: a search's call fails at once with the probe's code, the
// client not called, and the prober tries again until it answers; up, calls reach the client. A
// call that finds it unreachable marks it down at once, and the prober brings it back. Each
// change of state is logged once, by code, with no query or text.
func TestADownRankerIsAskedNothingAndProbedBackUp(t *testing.T) {
	var down atomic.Bool
	down.Store(true)
	c := &client{failing: func() error {
		if down.Load() {
			return fmt.Errorf("%w: POST /rerank", rank.ErrUnreachable)
		}
		return nil
	}}
	var log lines
	r := New("tei", "", 40, 4, func(context.Context) (rank.Ranker, error) { return c, nil },
		WithRetry(time.Millisecond, 5*time.Millisecond), WithLogger(log.logger()))
	run(t, r)
	waitState(t, r, Down)
	if h := r.Health(); h.Error != search.RankUnreachable {
		t.Fatalf("down with %q, want %s", h.Error, search.RankUnreachable)
	}
	_, err := r.Rank(context.Background(), "QUERY-MARKER", []string{"TEXT-MARKER"})
	var cd search.Coded
	if !errors.As(err, &cd) || cd.RankCode() != search.RankUnreachable {
		t.Fatalf("a call while down: %v, want a coded %s, answered without the client", err, search.RankUnreachable)
	}
	if h := r.Health(); h.Texts != 0 || h.Calls[search.RankUnreachable] != 1 {
		t.Fatalf("counts %+v: the call while down sent no text", h)
	}
	down.Store(false)
	waitState(t, r, Up)
	if _, err := r.Rank(context.Background(), "QUERY-MARKER", []string{"TEXT-MARKER"}); err != nil {
		t.Fatalf("up: %v", err)
	}
	down.Store(true)
	if _, err := r.Rank(context.Background(), "QUERY-MARKER", []string{"TEXT-MARKER"}); !errors.Is(err, rank.ErrUnreachable) {
		t.Fatalf("a call that finds it unreachable: %v", err)
	}
	if s := r.Health().State; s != Down {
		t.Fatalf("after an unreachable call the ranker is %s, want down at once", s)
	}
	down.Store(false)
	waitState(t, r, Up)
	got := log.text()
	if strings.Contains(got, "QUERY-MARKER") || strings.Contains(got, "TEXT-MARKER") {
		t.Errorf("the log holds a query or a text:\n%s", got)
	}
	if n := strings.Count(got, "is down"); n != 2 {
		t.Errorf("%d down lines, want one per change of state (2):\n%s", n, got)
	}
	if n := strings.Count(got, "is up"); n != 2 {
		t.Errorf("%d up lines, want 2:\n%s", n, got)
	}
}

// A refused probe is down with provider_refused; a build that fails (TEI serving an embedding
// model) is down too, and the client is made again on the next probe.
func TestARefusedOrUnbuiltRankerIsDown(t *testing.T) {
	c := &client{failing: func() error { return fmt.Errorf("%w: 401", rank.ErrUnauthorized) }}
	r := New("rerank-api", "rerank-v3.5", 40, 4, func(context.Context) (rank.Ranker, error) { return c, nil }, WithRetry(time.Millisecond, time.Millisecond))
	run(t, r)
	waitState(t, r, Down)
	if h := r.Health(); h.Error != search.RankRefused || h.Model != "bge-reranker" {
		t.Fatalf("health %+v, want down refused, the client's model named", h)
	}
	var builds atomic.Int64
	r2 := New("tei", "", 40, 4, func(context.Context) (rank.Ranker, error) {
		builds.Add(1)
		return nil, fmt.Errorf("%w: serves an embedding model", rank.ErrNotARanker)
	}, WithRetry(time.Millisecond, time.Millisecond))
	run(t, r2)
	waitState(t, r2, Down)
	if h := r2.Health(); h.Error != search.RankRefused || h.Model != "" {
		t.Fatalf("health %+v, want down refused", h)
	}
	for builds.Load() < 3 {
		time.Sleep(time.Millisecond)
	}
}

// At most slots calls are in flight: a call that cannot start before its context ends is busy,
// and is counted so, the client not called for it.
func TestTheRankerBoundsItsCallsInFlight(t *testing.T) {
	hold := make(chan struct{})
	c := &client{hold: hold}
	r := New("tei", "", 40, 1, func(context.Context) (rank.Ranker, error) { return c, nil })
	r.set(c, Up, "")
	first := make(chan error, 1)
	go func() {
		_, err := r.Rank(context.Background(), "q", []string{"a"})
		first <- err
	}()
	for c.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := r.Rank(ctx, "q", []string{"b"})
	var cd search.Coded
	if !errors.As(err, &cd) || cd.RankCode() != search.RankBusy {
		t.Fatalf("a call with the slot taken: %v, want busy", err)
	}
	close(hold)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if n := c.calls.Load(); n != 1 {
		t.Errorf("%d client calls, want the one that had the slot", n)
	}
	h := r.Health()
	if h.Calls[ResultOK] != 1 || h.Calls[search.RankBusy] != 1 || h.Texts != 1 || h.Count != 1 {
		t.Errorf("counts %+v, want one ok call of one text and one busy", h)
	}
}

// The searches' rankings are counted: ranked, unranked by code, and skipped; a search where
// nothing was ranked is none of them.
func TestTheRankerCountsTheSearches(t *testing.T) {
	r := New("tei", "", 40, 4, nil)
	r.Searched(search.Ranking{State: gsearch.RankReady}, false)
	r.Searched(search.Ranking{State: gsearch.RankReady}, false)
	r.Searched(search.Ranking{State: gsearch.RankError, Error: search.RankTimeout}, false)
	r.Searched(search.Ranking{State: gsearch.RankOff}, true)
	r.Searched(search.Ranking{State: gsearch.RankOff}, false)
	h := r.Health()
	if h.Ranked != 2 || h.Skipped != 1 || len(h.Unranked) != 1 || h.Unranked[search.RankTimeout] != 1 || h.Window != 40 || h.State != Starting {
		t.Errorf("health %+v", h)
	}
}

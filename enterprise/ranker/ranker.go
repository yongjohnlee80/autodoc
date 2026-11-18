// Package ranker is `autodoc --server`'s ranker: the one rank.Ranker every tenant's search and
// every search across tenants share ([ranker]; docs/decisions/server.md#a-reranker-after-recall).
//
// It holds the client its Build makes, probed with a two-text query: until a probe answers, and
// after a call finds it unreachable or refused, it is down, a search's call of it fails at once
// (search.RankUnreachable or search.RankRefused, so the search answers in recall order), and Run
// probes again, backing off to once a minute. It bounds the calls in flight across every search
// (slots): a call that cannot start before its context ends is search.RankBusy. It counts what an
// operator reads in /v1/status and /v1/metrics, and logs a change of state once, by code: never a
// query, a text, a key or an answer's body.
package ranker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	gsearch "github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/clock"
	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

// Build makes the ranker's client: rank.NewTEI or rank.NewRerankAPI, from [ranker]. A TEI client
// asks the server what it serves as it is made, so Build may fail as a probe does.
type Build func(ctx context.Context) (rank.Ranker, error)

// The ranker's states, as Health reports them.
const (
	Starting = "starting" // not probed yet
	Up       = "up"       // its last probe or call answered
	Down     = "down"     // unreachable or refused: searches answer in recall order, and Run probes
)

// The probe: two short texts against a query, as the daemon probes its ranker.
var (
	probeQuery = "probe"
	probeTexts = []string{"a probe", "another probe"}
)

// The prober's backoff, and the most one probe may take.
const (
	DefaultRetry    = time.Second
	DefaultMaxRetry = time.Minute
	probeTimeout    = 10 * time.Second
)

// DurationBuckets are the ranker calls' histogram upper bounds, in seconds.
var DurationBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 10}

// The results a ranker call is counted under: ok, or a search.Rank* code.
const ResultOK = "ok"

// Option configures a Ranker.
type Option func(*Ranker)

// WithLogger is where the ranker logs a change of its state. The default logs nothing.
func WithLogger(l logger.Logger) Option { return func(r *Ranker) { r.log = l } }

// WithRetry sets the prober's first wait and its longest.
func WithRetry(first, most time.Duration) Option {
	return func(r *Ranker) {
		if first > 0 {
			r.retry = first
		}
		if most > 0 {
			r.maxRetry = most
		}
	}
}

// Ranker is the server's ranker: a rank.Ranker. Its zero value is not usable: New makes one.
type Ranker struct {
	kind, model     string
	window          int
	build           Build
	slots           chan struct{}
	log             logger.Logger
	retry, maxRetry time.Duration
	wake            chan struct{}

	mu     sync.Mutex
	client rank.Ranker
	state  string
	code   string
	c      counts
}

// counts are what the ranker has done since the server started.
type counts struct {
	ranked, skipped int64
	unranked        map[string]int64
	calls           map[string]int64 // by result: ResultOK or a code
	texts           int64
	buckets         []int64 // DurationBuckets', each the calls no longer than its bound
	count           int64
	sum             time.Duration
}

// New is a ranker of kind and model (as [ranker] names them, for the status), its window the
// configured one, at most slots calls in flight, its client made by build. Run probes it.
func New(kind, model string, window, slots int, build Build, opts ...Option) *Ranker {
	r := &Ranker{kind: kind, model: model, window: window, build: build, slots: make(chan struct{}, max(slots, 1)),
		retry: DefaultRetry, maxRetry: DefaultMaxRetry, wake: make(chan struct{}, 1), state: Starting,
		c: counts{unranked: map[string]int64{}, calls: map[string]int64{}, buckets: make([]int64, len(DurationBuckets))}}
	for _, o := range opts {
		if o != nil {
			o(r)
		}
	}
	if r.log == nil {
		r.log = logger.Adapt(func(logger.Severity, any) {})
	}
	return r
}

// Run probes the ranker until ctx ends: at once, then whenever it is down, backing off from the
// first wait to the longest while it stays down, and waiting while it is up.
func (r *Ranker) Run(ctx context.Context) {
	wait := r.retry
	for ctx.Err() == nil {
		if r.probe(ctx) {
			wait = r.retry
			select {
			case <-r.wake: // a call found it down
			case <-ctx.Done():
				return
			}
			continue
		}
		// NOTE: a ranker that is down sends no event to wait on: the prober tries again on a
		// timer, backing off to the longest wait
		if !clock.Sleep(ctx, wait) {
			return
		}
		wait = min(2*wait, r.maxRetry)
	}
}

// probe makes the client when there is none, and ranks the probe's texts with it: true when it
// answered, the state up; false, the state down with the failure's code.
func (r *Ranker) probe(ctx context.Context) bool {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	r.mu.Lock()
	client := r.client
	r.mu.Unlock()
	if client == nil {
		c, err := r.build(pctx)
		if err != nil {
			r.set(nil, Down, search.RankCode(pctx, err))
			return false
		}
		client = c
	}
	scores, err := client.Rank(pctx, probeQuery, probeTexts)
	switch {
	case err != nil:
		r.set(client, Down, search.RankCode(pctx, err))
		return false
	case len(scores) != len(probeTexts):
		r.set(client, Down, search.RankBadAnswer)
		return false
	}
	r.set(client, Up, "")
	return true
}

// set records the client and the state, logging a change of state (or of a down state's code).
func (r *Ranker) set(client rank.Ranker, state, code string) {
	r.mu.Lock()
	if client != nil {
		r.client = client
	}
	changed := state != r.state || code != r.code
	r.state, r.code = state, code
	r.mu.Unlock()
	if !changed {
		return
	}
	if state == Up {
		logger.Info(r.log, fmt.Sprintf("ranker: %s %s is up: searches are reranked", r.kind, r.name()))
		return
	}
	logger.Warning(r.log, errors.New(code), fmt.Sprintf("ranker: %s is down (%s): searches answer in recall order until it answers again", r.kind, code))
}

// name is the model's name: the client's once it is made, else the configured one.
func (r *Ranker) name() string {
	r.mu.Lock()
	c := r.client
	r.mu.Unlock()
	if c != nil {
		if n := c.Model().Name; n != "" {
			return n
		}
	}
	return r.model
}

// Model is the client's model once it is made, else the configured kind and model.
func (r *Ranker) Model() rank.Model {
	r.mu.Lock()
	c := r.client
	r.mu.Unlock()
	if c != nil {
		return c.Model()
	}
	return rank.Model{Provider: r.kind, Name: r.model}
}

// coded is a call the ranker answered itself, with the code of why.
type coded struct{ code string }

func (e coded) Error() string    { return "ranker: " + e.code }
func (e coded) RankCode() string { return e.code }

// Rank is one search's call: at once a failure while the ranker is not up, else at most slots in
// flight, waiting for one under ctx (busy when ctx ends first). A call that finds the ranker
// unreachable or refused marks it down, and Run probes it again.
func (r *Ranker) Rank(ctx context.Context, query string, texts []string) ([]float64, error) {
	r.mu.Lock()
	client, state, code := r.client, r.state, r.code
	r.mu.Unlock()
	if client == nil || state != Up {
		if code == "" {
			code = search.RankUnreachable // starting: not probed yet
		}
		r.called(code, 0, 0)
		return nil, coded{code}
	}
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		r.called(search.RankBusy, 0, 0)
		return nil, coded{search.RankBusy}
	}
	defer func() { <-r.slots }()
	start := time.Now()
	scores, err := client.Rank(ctx, query, texts)
	took := time.Since(start)
	if err != nil {
		c := search.RankCode(ctx, err)
		r.called(c, len(texts), took)
		if c == search.RankUnreachable || c == search.RankRefused {
			r.set(nil, Down, c)
			select {
			case r.wake <- struct{}{}:
			default:
			}
		}
		return nil, err
	}
	r.called(ResultOK, len(texts), took)
	return scores, nil
}

// called counts one call: its result, the texts it sent, and its time (a call that sent nothing
// has none).
func (r *Ranker) called(result string, texts int, took time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.c.calls[result]++
	if texts == 0 {
		return
	}
	r.c.texts += int64(texts)
	r.c.count++
	r.c.sum += took
	for i, le := range DurationBuckets {
		if took.Seconds() <= le {
			r.c.buckets[i]++
		}
	}
}

// Searched counts one search's ranking: ranked, skipped (the request said rerank false), or
// answered unranked by its code. A search where nothing was ranked (no words, a listing by
// facets) is none of them.
func (r *Ranker) Searched(rk search.Ranking, skipped bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case skipped:
		r.c.skipped++
	case rk.State == gsearch.RankReady:
		r.c.ranked++
	case rk.State == gsearch.RankError:
		r.c.unranked[rk.Error]++
	}
}

// Health is the ranker as an operator sees it: its kind, model, state and a down state's code,
// the configured window, and its counts since the server started.
type Health struct {
	Kind, Model, State, Error string
	Window                    int
	Ranked, Skipped           int64
	Unranked                  map[string]int64 // by code
	Calls                     map[string]int64 // by result: ResultOK or a code
	Texts                     int64            // texts sent, distinct within each call
	Buckets                   []int64          // DurationBuckets', cumulative
	Count                     int64
	Sum                       time.Duration
}

// Health is the ranker now. It may be called from any goroutine.
func (r *Ranker) Health() Health {
	model := r.name()
	r.mu.Lock()
	defer r.mu.Unlock()
	h := Health{Kind: r.kind, Model: model, State: r.state, Error: r.code, Window: r.window,
		Ranked: r.c.ranked, Skipped: r.c.skipped, Unranked: map[string]int64{}, Calls: map[string]int64{},
		Texts: r.c.texts, Buckets: append([]int64(nil), r.c.buckets...), Count: r.c.count, Sum: r.c.sum}
	for k, v := range r.c.unranked {
		h.Unranked[k] = v
	}
	for k, v := range r.c.calls {
		h.Calls[k] = v
	}
	return h
}

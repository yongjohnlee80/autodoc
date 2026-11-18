package search

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/errs"
	gsearch "github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// The bounds on a search across tenants.
const (
	MaxTenants         = 16 // tenants one request may name
	MaxWeight          = 10 // the largest weight a tenant may be given
	MaxTenantsInFlight = 4  // tenants one request searches at once
)

// ErrTenants is a search across tenants that names none, more than MaxTenants, one twice, or a
// weight outside (0, MaxWeight].
var ErrTenants = errs.Sentinel(errs.ErrInvalidArgument, "search: the request's tenants are not searchable together")

// TenantSearch is one tenant of a search across tenants: its Searcher, its own scope (required, as
// for Search) and narrowing, and its weight.
type TenantSearch struct {
	Tenant   string // its name: every hit of it carries it
	Searcher *Searcher
	Scope    Scope
	Filter   gsearch.Filter
	Ranges   map[string]Range
	// Weight multiplies its hits' relevance in the merge: above 0, at most MaxWeight; 0 is 1.
	Weight float64
}

// GlobalRequest is a search across tenants: the query, mode, limit and grouping every tenant
// shares, and the tenants.
type GlobalRequest struct {
	Query   string
	Mode    gsearch.Mode
	Limit   int    // as Request's; each tenant is asked for Limit hits, and the merge is cut to it
	Group   string // as Request's, per tenant: two tenants' documents are never one group
	Tenants []TenantSearch
	// NoRerank and Window are as Request's, for the one ranker call after the merge
	// (WithGlobalRanker): Window 0 is the larger of the Global's window and 4 a tenant, at most
	// rank.MaxWindow.
	NoRerank bool
	Window   int
}

// GlobalHit is a hit of one of the tenants, named.
type GlobalHit struct {
	Hit
	Tenant string
}

// TenantResult is how one tenant of a search across tenants answered: its mode and semantic
// state, as a single search's Result says them.
type TenantResult struct {
	ModeUsed      gsearch.Mode
	Semantic      gsearch.State
	SemanticError string
}

// GlobalResult is a search across tenants' answer: the tenants' hits merged by relevance, and how
// each tenant answered, by name.
type GlobalResult struct {
	Hits    []GlobalHit
	Tenants map[string]TenantResult
	// Rank is what the Global's ranker did with the merged hits, once for the request
	Rank Ranking
}

// TenantError is one tenant's failure in a search across tenants, which fails the whole request:
// it names the tenant, and wraps the error the tenant's Search gave, so errors.Is finds its
// refusal.
type TenantError struct {
	Tenant string
	Err    error
}

func (e *TenantError) Error() string { return "tenant " + e.Tenant + ": " + e.Err.Error() }
func (e *TenantError) Unwrap() error { return e.Err }

// Global searches across tenants: every request of every tenant shares one pool of searches in
// flight, so concurrent requests cannot take a database pool whole. It is safe for concurrent use.
type Global struct {
	slots chan struct{}
	rank  *stage // WithGlobalRanker's; nil is none
}

// GlobalOption configures a Global.
type GlobalOption func(*Global)

// WithGlobalRanker reranks a search across tenants once, after the merge, as WithRanker reranks a
// tenant's search: one call over the merged top window, each hit's text from its own tenant's
// View. The tenants' own rankers are not used.
func WithGlobalRanker(r rank.Ranker, window int, timeout time.Duration) GlobalOption {
	return func(g *Global) { g.rank = newStage(r, window, timeout) }
}

// NewGlobal is a Global running at most inFlight tenants' searches at once across every request
// (below 1: 1).
func NewGlobal(inFlight int, opts ...GlobalOption) *Global {
	g := &Global{slots: make(chan struct{}, max(inFlight, 1))}
	for _, o := range opts {
		if o != nil {
			o(g)
		}
	}
	return g
}

// tenantWindow is how many candidates a ranked search across tenants admits by default: four a
// tenant, so each tenant's best few reach the ranker at equal weights.
const tenantWindow = 4

// Search answers r across its tenants.
//
//   - Every tenant's request is checked first, its scope against its policy as Search checks
//     them, before any query is embedded: one refusal refuses the whole request, naming its
//     tenant (TenantError), and no tenant is searched.
//   - The tenants are then searched concurrently, at most MaxTenantsInFlight of this request at
//     once and the Global's slots across requests, a tenant waiting for a slot under ctx. Each is
//     Search: its own snapshot, its own access, checked again in it. The query is embedded once
//     per model and text for the whole request (WithQueryCache).
//   - A tenant that fails fails the request, naming it; the other tenants' searches are cancelled.
//   - The hits are merged by relevance × weight, highest first, then by the tenant's place in r,
//     the path, and the chunk's place in its document, and cut to the limit. Relevance is on a
//     fixed scale (1 is first in every retriever its tenant ran), so tenants' hits compare; Score
//     does not, and is not used.
func (g *Global) Search(ctx context.Context, r GlobalRequest) (GlobalResult, error) {
	if err := checkTenants(r.Tenants); err != nil {
		return GlobalResult{}, err
	}
	if err := checkWindow(r.Window); err != nil {
		return GlobalResult{}, err
	}
	ranking := g.rank != nil && !r.NoRerank
	window := r.Window
	if ranking && window == 0 {
		window = min(max(g.rank.window, tenantWindow*len(r.Tenants)), rank.MaxWindow)
	}
	reqs := make([]Request, len(r.Tenants))
	for i, t := range r.Tenants {
		// a tenant's own ranker never runs here: the merge is ranked once, after it
		reqs[i] = Request{Query: r.Query, Mode: r.Mode, Limit: r.Limit, Group: r.Group, Filter: t.Filter, Ranges: t.Ranges, NoRerank: true}
		if _, err := t.Searcher.precheck(ctx, t.Scope, reqs[i]); err != nil {
			return GlobalResult{}, &TenantError{Tenant: t.Tenant, Err: err}
		}
		if ranking {
			// each tenant is asked for the window at least, so the window can fill from one
			// tenant when the weights say so
			lim := reqs[i].Limit
			if lim == 0 {
				lim = t.Searcher.def
			}
			reqs[i].Limit, reqs[i].keepTexts = min(max(lim, window), t.Searcher.max), true
		}
	}

	ctx, cancel := context.WithCancelCause(WithQueryCache(ctx))
	defer cancel(nil)
	results := make([]Result, len(r.Tenants))
	var (
		wg      sync.WaitGroup
		mine    = make(chan struct{}, MaxTenantsInFlight)
		errOnce sync.Once
		first   error
	)
	fail := func(err error) {
		errOnce.Do(func() {
			first = err
			cancel(err)
		})
	}
	for i, t := range r.Tenants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !take(ctx, mine) {
				return
			}
			defer func() { <-mine }()
			if !take(ctx, g.slots) {
				return
			}
			defer func() { <-g.slots }()
			res, err := t.Searcher.Search(ctx, t.Scope, reqs[i])
			if err != nil {
				fail(&TenantError{Tenant: t.Tenant, Err: err})
				return
			}
			results[i] = res
		}()
	}
	wg.Wait()
	if first != nil {
		return GlobalResult{}, first
	}
	if err := ctx.Err(); err != nil {
		return GlobalResult{}, context.Cause(ctx)
	}

	out := GlobalResult{Tenants: make(map[string]TenantResult, len(r.Tenants))}
	type ranked struct {
		hit   GlobalHit
		score float64
		at    int
	}
	var all []ranked
	for i, t := range r.Tenants {
		res := results[i]
		out.Tenants[t.Tenant] = TenantResult{ModeUsed: res.ModeUsed, Semantic: res.Semantic, SemanticError: res.SemanticError}
		w := t.Weight
		if w == 0 {
			w = 1
		}
		for _, h := range res.Hits {
			all = append(all, ranked{hit: GlobalHit{Hit: h, Tenant: t.Tenant}, score: h.Relevance * w, at: i})
		}
	}
	slices.SortStableFunc(all, func(a, b ranked) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		if c := cmp.Compare(a.at, b.at); c != 0 {
			return c
		}
		if c := cmp.Compare(a.hit.Path, b.hit.Path); c != 0 {
			return c
		}
		return cmp.Compare(a.hit.ByteStart, b.hit.ByteStart)
	})
	limit := r.Limit
	if limit == 0 {
		limit = r.Tenants[0].Searcher.def
	}
	out.Rank = Ranking{State: gsearch.RankOff}
	if ranking {
		merged := make([]GlobalHit, len(all))
		kept := make([]map[pgstore.TextKey]string, len(all))
		for i, x := range all {
			merged[i], kept[i] = x.hit, results[x.at].texts
		}
		hits, rk, err := g.ranked(ctx, r.Query, results[0].ModeUsed, merged, kept, window, limit)
		if err != nil {
			return GlobalResult{}, err
		}
		out.Hits, out.Rank = hits, rk
		return out, nil
	}
	for _, x := range all[:min(len(all), limit)] {
		out.Hits = append(out.Hits, x.hit)
	}
	return out, nil
}

// ranked is a search across tenants' merged hits through the Global's stage: golib's stage over a
// searcher that answers the merge, its hits named by their place in it, so the texts and the
// tenants are found again by place, each hit's text from its own tenant's View.
func (g *Global) ranked(ctx context.Context, query string, mode gsearch.Mode, merged []GlobalHit, kept []map[pgstore.TextKey]string,
	window, limit int) ([]GlobalHit, Ranking, error) {
	out := &outcome{}
	inner := mergedSearcher{hits: merged, mode: mode}
	texts := func(_ context.Context, hits []gsearch.Hit) ([]string, error) {
		ts := make([]string, len(hits))
		for i, h := range hits {
			at, ok := placeOf(h, len(merged))
			if !ok {
				out.fail(RankInternal)
				return nil, fmt.Errorf("search: a merged hit with no place %q", h.Path)
			}
			m := merged[at]
			t, err := textsOf(kept[at], []gsearch.Hit{m.Hit.Hit}, out)
			if err != nil {
				return nil, fmt.Errorf("tenant %s: %w", m.Tenant, err)
			}
			ts[i] = t[0]
		}
		return ts, nil
	}
	rs := rank.NewSearcher(inner, texts, source{r: timed{st: g.rank, out: out}, window: window},
		rank.WithDefaultLimit(limit), rank.WithMaxLimit(max(limit, MaxLimit)))
	res, err := rs.Search(ctx, gsearch.Query{Text: query, Limit: limit})
	if err != nil {
		return nil, Ranking{}, err
	}
	hits := make([]GlobalHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		at, ok := placeOf(h, len(merged))
		if !ok {
			return nil, Ranking{}, fmt.Errorf("search: a ranked hit with no place %q", h.Path)
		}
		x := merged[at]
		x.RankScore, x.Via = h.RankScore, h.Via
		hits = append(hits, x)
	}
	return hits, out.ranking(res.Rank), nil
}

// mergedSearcher answers the merge of a search across tenants, whatever it is asked: its hits in
// the merge's order, each named by its place (placeOf), so a stage over it orders places.
type mergedSearcher struct {
	hits []GlobalHit
	mode gsearch.Mode
}

func (m mergedSearcher) Search(context.Context, gsearch.Query) (gsearch.Result, error) {
	res := gsearch.Result{Hits: make([]gsearch.Hit, len(m.hits)), ModeUsed: m.mode}
	for i, h := range m.hits {
		x := h.Hit.Hit
		x.Path = strconv.Itoa(i)
		res.Hits[i] = x
	}
	return res, nil
}

// placeOf is the place in the merge a mergedSearcher's hit names.
func placeOf(h gsearch.Hit, n int) (int, bool) {
	at, err := strconv.Atoi(h.Path)
	return at, err == nil && at >= 0 && at < n
}

// take waits for a slot of ch under ctx: false when ctx ended first.
func take(ctx context.Context, ch chan struct{}) bool {
	select {
	case ch <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// checkTenants refuses (ErrTenants) a search across tenants that names none, more than
// MaxTenants, one twice or with no Searcher, or a weight outside (0, MaxWeight] (0 is 1).
func checkTenants(ts []TenantSearch) error {
	if len(ts) == 0 || len(ts) > MaxTenants {
		return fmt.Errorf("%w: %d tenants (1 to %d)", ErrTenants, len(ts), MaxTenants)
	}
	seen := map[string]bool{}
	for _, t := range ts {
		switch {
		case t.Tenant == "" || t.Searcher == nil:
			return fmt.Errorf("%w: a tenant with no name or no searcher", ErrTenants)
		case seen[t.Tenant]:
			return fmt.Errorf("%w: %s is named twice", ErrTenants, t.Tenant)
		case t.Weight < 0 || t.Weight > MaxWeight || math.IsNaN(t.Weight):
			return fmt.Errorf("%w: %s's weight %v (above 0, at most %d)", ErrTenants, t.Tenant, t.Weight, MaxWeight)
		}
		seen[t.Tenant] = true
	}
	return nil
}

// queryCacheKey keys a request's query cache on its context.
type queryCacheKey struct{}

// queryCache is one request's embedded queries, by model and text.
type queryCache struct {
	mu sync.Mutex
	m  map[[2]string]*cachedQuery
}

// cachedQuery is one query's embedding: done closes when vec and err are set.
type cachedQuery struct {
	done chan struct{}
	vec  []float32
	err  error
}

// WithQueryCache is ctx with a cache of the queries embedded under it, by model and text: a search
// across tenants embeds its query once per model, however many of its tenants share one. A query
// embedder that knows its model (QueryEmbedder, the server's worker) consults it (EmbedQuery).
func WithQueryCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, queryCacheKey{}, &queryCache{m: map[[2]string]*cachedQuery{}})
}

// EmbedQuery is embed's answer for text under model, embedded once per ctx's query cache
// (WithQueryCache): a second call for the same model and text waits for the first and shares its
// vector, or its error. Without a cache it calls embed. The vector is shared: callers do not
// modify it.
func EmbedQuery(ctx context.Context, model, text string, embed func() ([]float32, error)) ([]float32, error) {
	c, _ := ctx.Value(queryCacheKey{}).(*queryCache)
	if c == nil {
		return embed()
	}
	k := [2]string{model, text}
	c.mu.Lock()
	q, ok := c.m[k]
	if !ok {
		q = &cachedQuery{done: make(chan struct{})}
		c.m[k] = q
	}
	c.mu.Unlock()
	if !ok {
		// done closes however embed ends: a panic leaves the waiters an error, then goes on up
		defer func() {
			if r := recover(); r != nil {
				q.vec, q.err = nil, fmt.Errorf("%w: the query's embedding panicked", gsearch.ErrEmbedFailed)
				close(q.done)
				panic(r)
			}
		}()
		q.vec, q.err = embed()
		close(q.done)
		return q.vec, q.err
	}
	select {
	case <-q.done:
		return q.vec, q.err
	case <-ctx.Done():
		return nil, errors.Join(gsearch.ErrEmbedFailed, ctx.Err())
	}
}

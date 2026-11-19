package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	gsearch "github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

// THE SECOND STAGE (rank.go): a search with a ranker answers in its order over the window and in
// recall order after it, from the texts its own View read, within its scope; a ranker that fails
// answers in recall order with the code of why; a search across tenants is ranked once, after
// the merge.

// fakeRanker scores each text by score (0 for nil), after wait, or fails with err; it records
// every call's query and texts.
type fakeRanker struct {
	score func(text string) float64
	wait  time.Duration
	err   error

	mu      sync.Mutex
	queries []string
	calls   [][]string
}

func (f *fakeRanker) Model() rank.Model { return rank.Model{Provider: "fake", Name: "fake-ranker"} }

func (f *fakeRanker) Rank(ctx context.Context, q string, texts []string) ([]float64, error) {
	f.mu.Lock()
	f.queries, f.calls = append(f.queries, q), append(f.calls, slices.Clone(texts))
	f.mu.Unlock()
	if f.wait > 0 {
		select {
		case <-time.After(f.wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	out := make([]float64, len(texts))
	for i, t := range texts {
		if f.score != nil {
			out[i] = f.score(t)
		}
	}
	return out, nil
}

// sent are the texts of every call, in order.
func (f *fakeRanker) sent() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// numbered scores a text by the number after its last "n": "crimson n07" scores 7.
func numbered(text string) float64 {
	i := strings.LastIndex(text, " n")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(text[i+2:]))
	if err != nil {
		return 0
	}
	return float64(n)
}

// openRanked opens the fixture's tenant with r as its ranker, at window, under timeout.
func (f *fixture) openRanked(r rank.Ranker, window int, timeout time.Duration) *Searcher {
	f.t.Helper()
	s, err := Open(f.ctx, f.conn, "t", qe, WithRanker(r, window, timeout))
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// crimsons writes n label A tracks matching "crimson", track/cNN.md with body "crimson nNN".
func (f *fixture) crimsons(n int) {
	f.t.Helper()
	for i := range n {
		f.put(fmt.Sprintf("track/c%02d.md", i), fmt.Sprintf("c%d", i), map[string][]string(scopeA), fmt.Sprintf("crimson n%02d", i))
	}
}

func paths(hs []Hit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Path
	}
	return out
}

func (f *fixture) search(s *Searcher, r Request) Result {
	f.t.Helper()
	res, err := s.Search(f.ctx, scopeA, r)
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

// A search with a ranker answers its window in the ranker's order, each hit with its own score
// and "rank" in its via, and the hits past the window after them in recall order, unscored; its
// Ranking is ready, naming the model. The mutant, named here: the stage not applied.
func TestARankedSearchAnswersInTheRankersOrderThenRecall(t *testing.T) {
	f := newFixture(t)
	f.crimsons(12)
	r := &fakeRanker{score: numbered}
	s := f.openRanked(r, 10, time.Second)
	recall := f.search(s, Request{Query: "crimson", Mode: gsearch.ModeLexical, Limit: 12, NoRerank: true})
	if recall.Rank.State != gsearch.RankOff || len(r.sent()) != 0 {
		t.Fatalf("rerank false: %+v, %d calls; want off and no call", recall.Rank, len(r.sent()))
	}
	got := f.search(s, Request{Query: "crimson", Mode: gsearch.ModeLexical, Limit: 12})
	if got.Rank != (Ranking{State: gsearch.RankReady, Model: "fake-ranker"}) {
		t.Fatalf("ranking %+v, want ready, fake-ranker", got.Rank)
	}
	if len(got.Hits) != 12 || len(recall.Hits) != 12 {
		t.Fatalf("%d hits ranked, %d in recall; want 12 each", len(got.Hits), len(recall.Hits))
	}
	window := slices.Clone(recall.Hits[:10])
	slices.SortStableFunc(window, func(a, b Hit) int { return int(numbered(b.Snippet) - numbered(a.Snippet)) })
	if w, g := paths(window), paths(got.Hits[:10]); !slices.Equal(w, g) {
		t.Errorf("the window in %v, want the ranker's order %v", g, w)
	}
	if w, g := paths(recall.Hits[10:]), paths(got.Hits[10:]); !slices.Equal(w, g) {
		t.Errorf("past the window %v, want recall's %v", g, w)
	}
	for i, h := range got.Hits {
		switch {
		case i < 10 && (h.RankScore == nil || *h.RankScore != numbered(h.Breadcrumb+"\n"+h.Snippet) || !slices.Contains(h.Via, "rank")):
			t.Errorf("hit %d %s: score %v via %v, want its own score and rank in via", i, h.Path, h.RankScore, h.Via)
		case i >= 10 && (h.RankScore != nil || slices.Contains(h.Via, "rank")):
			t.Errorf("hit %d %s past the window: score %v via %v, want unscored", i, h.Path, h.RankScore, h.Via)
		case h.SourceID == "":
			t.Errorf("hit %d %s has no source id", i, h.Path)
		}
	}
}

// The ranker is sent each window hit's text as the search's View read it, its breadcrumb and its
// body, once per distinct text: two hits with one text cost one, and both get its score.
func TestTheRankerIsSentTheViewsTextsOncePerText(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"track/twin1.md", "track/twin2.md"} {
		c := gsearch.Chunk{Ord: 0, Breadcrumb: "Twins", Body: "crimson twin", ByteEnd: 12}
		if _, err := f.st.Put(f.ctx, pgstore.Document{Path: p, Title: "twin", Facets: map[string][]string(scopeA), Chunks: []gsearch.Chunk{c}, SourceID: p}); err != nil {
			t.Fatal(err)
		}
	}
	f.put("track/other.md", "o", map[string][]string(scopeA), "crimson other")
	r := &fakeRanker{score: func(t string) float64 { return float64(len(t)) }}
	got := f.search(f.openRanked(r, 10, time.Second), Request{Query: "crimson", Mode: gsearch.ModeLexical})
	calls := r.sent()
	if len(calls) != 1 {
		t.Fatalf("%d calls, want one", len(calls))
	}
	sent := slices.Sorted(slices.Values(calls[0]))
	if want := []string{"Twins\ncrimson twin", "track/1.md\ncrimson river ballad", "track/other.md\ncrimson other"}; !slices.Equal(sent, want) {
		t.Fatalf("sent %q, want each distinct text once, breadcrumb and body: %q", sent, want)
	}
	var twins []float64
	for _, h := range got.Hits {
		if strings.HasPrefix(h.Path, "track/twin") {
			twins = append(twins, *h.RankScore)
		}
	}
	if len(twins) != 2 || twins[0] != twins[1] {
		t.Errorf("the twins' scores %v, want one score for both", twins)
	}
}

// A ranked search sends only texts its scope admits: a label B document matching the query is
// neither a hit nor a text sent.
func TestARankedSearchSendsOnlyItsScopesTexts(t *testing.T) {
	f := newFixture(t)
	f.crimsons(3)
	f.put("track/secret.md", "s", map[string][]string{"label_id": {"B"}}, "crimson secret")
	r := &fakeRanker{score: numbered}
	got := f.search(f.openRanked(r, 10, time.Second), Request{Query: "crimson", Mode: gsearch.ModeLexical})
	for _, c := range r.sent() {
		for _, text := range c {
			if strings.Contains(text, "secret") {
				t.Fatalf("label A's search sent label B's text %q", text)
			}
		}
	}
	if slices.Contains(paths(got.Hits), "track/secret.md") || got.Rank.State != gsearch.RankReady {
		t.Fatalf("hits %v, ranking %+v", paths(got.Hits), got.Rank)
	}
}

// coded is a ranker error with its own code, as the server's ranker answers a busy or down call.
type coded string

func (c coded) Error() string    { return "ranker: " + string(c) }
func (c coded) RankCode() string { return string(c) }

// A ranker that fails answers the search in recall order, whole and cut to its limit, its Ranking
// in error with the code of why: unreachable, refused, rate limited, a bad answer (an error, or
// the wrong number of scores), past the budget, busy. The mutant, named here: a failure returned
// as the search's error.
func TestARankerThatFailsAnswersInRecallOrderWithItsCode(t *testing.T) {
	f := newFixture(t)
	f.crimsons(6)
	recall := f.search(f.open(qe), Request{Query: "crimson", Mode: gsearch.ModeLexical, Limit: 4})
	for _, c := range []struct {
		name string
		r    rank.Ranker
		code string
	}{
		{"unreachable", &fakeRanker{err: fmt.Errorf("%w: POST /rerank", rank.ErrUnreachable)}, RankUnreachable},
		{"refused", &fakeRanker{err: fmt.Errorf("%w: 401", rank.ErrUnauthorized)}, RankRefused},
		{"no model", &fakeRanker{err: rank.ErrNoModel}, RankRefused},
		{"rate limited", &fakeRanker{err: rank.ErrRateLimited}, RankLimited},
		{"bad answer", &fakeRanker{err: rank.ErrBadAnswer}, RankBadAnswer},
		{"wrong length", shortRanker{}, RankBadAnswer},
		{"timeout", &fakeRanker{wait: time.Minute, score: numbered}, RankTimeout},
		{"busy", &fakeRanker{err: coded(RankBusy)}, RankBusy},
	} {
		model := c.r.Model().Name
		t.Run(c.name, func(t *testing.T) {
			s := f.openRanked(c.r, 10, 200*time.Millisecond)
			got, err := s.Search(f.ctx, scopeA, Request{Query: "crimson", Mode: gsearch.ModeLexical, Limit: 4})
			if err != nil {
				t.Fatalf("the search failed: %v; want it answered in recall order", err)
			}
			if got.Rank != (Ranking{State: gsearch.RankError, Model: model, Error: c.code}) {
				t.Errorf("ranking %+v, want error %s", got.Rank, c.code)
			}
			if w, g := paths(recall.Hits), paths(got.Hits); !slices.Equal(w, g) {
				t.Errorf("hits %v, want recall's %v", g, w)
			}
		})
	}
}

// shortRanker answers one score too few.
type shortRanker struct{}

func (shortRanker) Model() rank.Model { return rank.Model{Provider: "fake", Name: "short"} }
func (shortRanker) Rank(_ context.Context, _ string, texts []string) ([]float64, error) {
	return make([]float64, max(len(texts)-1, 0)), nil
}

// rerank false, a listing by facets alone and a query with no words each answer in recall order,
// their Ranking off, and call no ranker.
func TestNothingIsRankedWhereNothingIsAskedOrWorded(t *testing.T) {
	f := newFixture(t)
	f.crimsons(3)
	r := &fakeRanker{score: numbered}
	s := f.openRanked(r, 10, time.Second)
	for name, req := range map[string]Request{
		"rerank false": {Query: "crimson", NoRerank: true},
		"facets alone": {Filter: gsearch.Filter{Tags: []string{"song"}}},
		"no words":     {Query: "   "},
	} {
		got, err := s.Search(f.ctx, scopeA, req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Rank.State != gsearch.RankOff {
			t.Errorf("%s: ranking %+v, want off", name, got.Rank)
		}
	}
	if n := len(r.sent()); n != 0 {
		t.Errorf("%d ranker calls, want none", n)
	}
}

// A request's window outside 10 to 100 is refused (ErrWindow), before anything is searched.
func TestAWindowOutOfRangeIsRefused(t *testing.T) {
	f := newFixture(t)
	s := f.openRanked(&fakeRanker{}, 10, time.Second)
	for _, w := range []int{-1, 9, 101} {
		if _, err := s.Search(f.ctx, scopeA, Request{Query: "crimson", Window: w}); !errors.Is(err, ErrWindow) {
			t.Errorf("window %d: %v, want ErrWindow", w, err)
		}
	}
}

// rankTenants makes n tenants in one scratch database, tenant i named tNN with docs documents
// "crimson <word> <tenant> nNN", each searcher with its own ranker (never called across tenants).
func rankTenants(t *testing.T, conn dao.DataConn, n, docs int, word func(tenant int) string, own rank.Ranker) []TenantSearch {
	t.Helper()
	m := embed.Model{Provider: "test", Name: "rank", Dims: 64}
	p := &countingProvider{model: m}
	var ts []TenantSearch
	for i := range n {
		name := fmt.Sprintf("t%02d", i)
		var ds []tenantDoc
		for j := range docs {
			ds = append(ds, tenantDoc{fmt.Sprintf("doc/%02d.md", j), map[string][]string{"org": {"1"}}, fmt.Sprintf("crimson %s %s n%02d", word(i), name, j)})
		}
		s := openTenant(t, conn, name, []string{"org"}, m, p, ds...)
		s.rank = newStage(own, 10, time.Second)
		ts = append(ts, TenantSearch{Tenant: name, Searcher: s, Scope: Scope{"org": {"1"}}})
	}
	return ts
}

// A search across tenants is ranked once, after the merge: three tenants with the same paths, one
// weak, at equal weights. Each hit's text is its own tenant's (the paths are the same in every
// tenant); the weak tenant's best hit, which ties for first in recall, ranks below the strong
// tenants' hits; one ranker call for the request, the tenants' own rankers unused. With weight 0.1
// the weak tenant's candidates take fewer of the window's places. The mutants, named here: the
// texts keyed by path alone (two tenants' same path cross); a call per tenant.
func TestASearchAcrossTenantsIsRankedOnceAfterTheMerge(t *testing.T) {
	conn := pgtest.Scratch(t, pgstore.Migrate)
	own := &fakeRanker{score: numbered}
	ts := rankTenants(t, conn, 3, 6, func(i int) string { return map[int]string{0: "strong", 1: "strong", 2: "weak"}[i] }, own)
	score := func(text string) float64 {
		if strings.Contains(text, "weak") {
			return 0.1
		}
		return 1 + numbered(text)/100
	}
	r := &fakeRanker{score: score}
	g := NewGlobal(4, WithGlobalRanker(r, 10, time.Second)) // across three tenants: a window of 12, 4 each
	got, err := g.Search(context.Background(), GlobalRequest{Query: "crimson", Mode: gsearch.ModeLexical, Limit: 14, Tenants: ts})
	if err != nil {
		t.Fatal(err)
	}
	if got.Rank != (Ranking{State: gsearch.RankReady, Model: "fake-ranker"}) {
		t.Fatalf("ranking %+v", got.Rank)
	}
	if n, m := len(r.sent()), len(own.sent()); n != 1 || m != 0 {
		t.Fatalf("%d calls of the Global's ranker and %d of the tenants', want one and none", n, m)
	}
	if len(got.Hits) != 14 {
		t.Fatalf("%d hits, want 14: %v", len(got.Hits), globalHits(got))
	}
	weakAt, lastStrong := -1, -1
	for i, h := range got.Hits[:12] { // the window
		want := score(fmt.Sprintf("crimson %s %s n%s", map[string]string{"t00": "strong", "t01": "strong", "t02": "weak"}[h.Tenant], h.Tenant,
			strings.TrimSuffix(strings.TrimPrefix(h.Path, "doc/"), ".md")))
		if h.RankScore == nil || *h.RankScore != want {
			t.Errorf("hit %d %s:%s scored %v, want its own tenant's text's %v", i, h.Tenant, h.Path, h.RankScore, want)
		}
		switch {
		case h.Tenant == "t02" && weakAt < 0:
			weakAt = i
			if h.Relevance != 1 {
				t.Errorf("the weak tenant's best has relevance %v, want 1: tied for first in recall", h.Relevance)
			}
		case h.Tenant != "t02":
			lastStrong = i
		}
	}
	if weakAt < 0 || weakAt < lastStrong {
		t.Errorf("the weak tenant's best at %d, the last ranked strong hit at %d: want it below them all: %v", weakAt, lastStrong, globalHits(got))
	}
	weakSent := func(texts []string) (n int) {
		for _, x := range texts {
			if strings.Contains(x, "weak") {
				n++
			}
		}
		return n
	}
	equal := weakSent(r.sent()[0])
	ts[2].Weight = 0.1
	if _, err := g.Search(context.Background(), GlobalRequest{Query: "crimson", Mode: gsearch.ModeLexical, Limit: 10, Tenants: ts}); err != nil {
		t.Fatal(err)
	}
	if light := weakSent(r.sent()[1]); light >= equal {
		t.Errorf("the weak tenant at weight 0.1 sent %d texts of the window, at equal weights %d: want fewer", light, equal)
	}
}

// A search across twelve tenants admits 4 a tenant at the default window, 48 in all at equal
// weights; a request's window of 100 admits 100.
func TestTheWindowAcrossTenants(t *testing.T) {
	conn := pgtest.Scratch(t, pgstore.Migrate)
	ts := rankTenants(t, conn, 12, 9, func(int) string { return "same" }, &fakeRanker{})
	r := &fakeRanker{score: numbered}
	g := NewGlobal(4, WithGlobalRanker(r, rank.DefaultWindow, time.Second))
	for _, c := range []struct{ window, want int }{{0, 48}, {100, 100}} {
		if _, err := g.Search(context.Background(), GlobalRequest{Query: "crimson", Mode: gsearch.ModeLexical, Window: c.window, Tenants: ts}); err != nil {
			t.Fatal(err)
		}
		calls := r.sent()
		sent := calls[len(calls)-1]
		if len(sent) != c.want {
			t.Errorf("window %d: %d texts, want %d", c.window, len(sent), c.want)
		}
		if c.window == 0 {
			per := map[string]int{}
			for _, x := range sent {
				per[strings.Fields(x)[3]]++
			}
			for name, n := range per {
				if n != 4 {
					t.Errorf("tenant %s admitted %d, want 4 a tenant: %v", name, n, per)
				}
			}
		}
	}
	if _, err := g.Search(context.Background(), GlobalRequest{Query: "crimson", Window: 101, Tenants: ts}); !errors.Is(err, ErrWindow) {
		t.Errorf("window 101: %v, want ErrWindow", err)
	}
}

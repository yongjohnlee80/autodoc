package search

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	gsearch "github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/search/searchtest"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

const model = "test|embed||64"

// fixture is a tenant "t" in a scratch database: its policy is label_id, its model is active, and
// it holds two label A tracks and one label B track, each ready by meaning.
type fixture struct {
	t    *testing.T
	ctx  context.Context
	conn dao.DataConn
	st   *pgstore.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, ctx: context.Background(), conn: pgtest.Scratch(t, pgstore.Migrate)}
	st, err := pgstore.New(f.conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	f.st = st
	if err := st.SetModels(f.ctx, model, ""); err != nil {
		t.Fatal(err)
	}
	f.policy("label_id")
	f.put("track/1.md", "1", map[string][]string{"label_id": {"A"}}, "crimson river ballad")
	f.put("track/2.md", "2", map[string][]string{"label_id": {"A"}}, "golden morning anthem")
	f.put("track/3.md", "3", map[string][]string{"label_id": {"B"}}, "midnight ocean lullaby")
	return f
}

func (f *fixture) policy(fields ...string) {
	f.t.Helper()
	if err := f.st.SetPolicy(f.ctx, pgstore.Policy{AccessFields: fields, View: "track"}); err != nil {
		f.t.Fatal(err)
	}
}

// put writes a track tagged "song", its one chunk's vector searchtest.Embed of its body, ready.
func (f *fixture) put(path, sid string, facets map[string][]string, body string) {
	f.t.Helper()
	c := gsearch.Chunk{Ord: 0, Breadcrumb: path, Body: body, ByteEnd: len(body)}
	d := pgstore.Document{Path: path, Title: body, Tags: []string{"song"}, Facets: facets, Chunks: []gsearch.Chunk{c}, SourceID: sid}
	if _, err := f.st.Put(f.ctx, d); err != nil {
		f.t.Fatal(err)
	}
	if err := f.st.Embed(f.ctx, model, []pgstore.Embedding{{TextHash: c.TextHash(), Vec: searchtest.Embed(body)}}); err != nil {
		f.t.Fatal(err)
	}
	if ready, err := f.st.MarkReady(f.ctx, path, model); err != nil || !ready {
		f.t.Fatalf("mark %s ready: %v, %v", path, ready, err)
	}
}

// qe embeds a query as the fixture's chunks are embedded.
func qe(_ context.Context, q string) (string, []float32, error) {
	return model, searchtest.Embed(q), nil
}

func (f *fixture) open(qe gsearch.QueryEmbedder) *Searcher {
	f.t.Helper()
	s, err := Open(f.ctx, f.conn, "t", qe)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// hits is r's hits as path=source_id, in order.
func hits(r Result) []string {
	var out []string
	for _, h := range r.Hits {
		out = append(out, h.Path+"="+h.SourceID)
	}
	return out
}

var scopeA = Scope{"label_id": {"A"}}

// Several matching chunks must not consume the document limit. Scope remains applied before
// retrieval, including semantic hits; a different export path with the same source id is distinct.
func TestDocumentGroupingBeforeTheLimit(t *testing.T) {
	f := newFixture(t)
	chunks := []gsearch.Chunk{
		{Ord: 0, Body: "crimson river.", ByteEnd: 14},
		{Ord: 1, Body: "crimson grapes!", ByteStart: 14, ByteEnd: 28},
		{Ord: 2, Body: "crimson fox.", ByteStart: 28, ByteEnd: 40},
	}
	if _, err := f.st.Put(f.ctx, pgstore.Document{Path: "track/1.md", SourceID: "1", Tags: []string{"song"},
		Facets: map[string][]string(scopeA), Chunks: chunks}); err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		if err := f.st.Embed(f.ctx, model, []pgstore.Embedding{{TextHash: c.TextHash(), Vec: searchtest.Embed(c.Body)}}); err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := f.st.MarkReady(f.ctx, "track/1.md", model); err != nil || !ready {
		t.Fatalf("mark ready: %v, %v", ready, err)
	}
	f.put("track/2.md", "1", map[string][]string(scopeA), "crimson anthem.")
	s := f.open(qe)
	for _, mode := range []gsearch.Mode{gsearch.ModeLexical, gsearch.ModeSemantic, gsearch.ModeAuto} {
		ungrouped, err := s.Search(f.ctx, scopeA, Request{Query: "crimson", Mode: mode})
		if err != nil || len(ungrouped.Hits) < 4 {
			t.Fatalf("%s chunks: %v, %v", mode, hits(ungrouped), err)
		}
		for _, group := range []string{"", "chunk"} {
			got, err := s.Search(f.ctx, scopeA, Request{Query: "crimson", Mode: mode, Limit: 2, Group: group})
			if err != nil || len(got.Hits) != 2 || got.Hits[0].Path != ungrouped.Hits[0].Path || got.Hits[1].Path != ungrouped.Hits[1].Path {
				t.Fatalf("%s %q: %v, %v", mode, group, hits(got), err)
			}
		}
		got, err := s.Search(f.ctx, scopeA, Request{Query: "crimson", Mode: mode, Limit: 2, Group: "document"})
		if err != nil || len(got.Hits) != 2 || got.Hits[0].Path == got.Hits[1].Path {
			t.Fatalf("%s documents: %v, %v", mode, hits(got), err)
		}
		for _, h := range got.Hits {
			best := ungrouped.Hits[slices.IndexFunc(ungrouped.Hits, func(x Hit) bool { return x.Path == h.Path })]
			if h.ByteStart != best.ByteStart || h.Score != best.Score || h.SourceID != "1" || h.Path == "track/3.md" {
				t.Errorf("%s: not the best scoped chunk: %+v, want %+v", mode, h, best)
			}
			if mode != gsearch.ModeSemantic && !strings.Contains(h.Snippet, "\x02crimson\x03") {
				t.Errorf("punctuation inside highlight: %q", h.Snippet)
			}
		}
	}
	if _, err := s.Search(f.ctx, scopeA, Request{Group: "unknown"}); !errors.Is(err, ErrGroup) {
		t.Errorf("unknown group: %v", err)
	}
	got, err := s.Search(f.ctx, scopeA, Request{Filter: gsearch.Filter{Tags: []string{"song"}}, Group: "document", Limit: 2})
	if err != nil || len(got.Hits) != 2 || got.Hits[0].Path == got.Hits[1].Path {
		t.Errorf("grouped listing: %v, %v", hits(got), err)
	}
}

// A search answers only within its scope, whichever way it finds: by words, by meaning (a query
// nearest to a B track answers A tracks only), and by a filter alone; a scope of A and B answers
// both. Each hit carries its row's id. The mutant, named here: the engine's store reading the
// tenant unscoped (the B track answers within A).
func TestASearchAnswersOnlyWithinItsScope(t *testing.T) {
	f := newFixture(t)
	s := f.open(qe)
	got, err := s.Search(f.ctx, scopeA, Request{Query: "ballad", Mode: gsearch.ModeLexical})
	if err != nil || !slices.Equal(hits(got), []string{"track/1.md=1"}) {
		t.Fatalf("ballad within A: %v, %v", hits(got), err)
	}
	if got, err = s.Search(f.ctx, scopeA, Request{Query: "midnight ocean lullaby", Mode: gsearch.ModeSemantic}); err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) == 0 || slices.ContainsFunc(got.Hits, func(h Hit) bool { return h.Path == "track/3.md" }) {
		t.Errorf("the B track's own words by meaning, within A: %v", hits(got))
	}
	if got, err = s.Search(f.ctx, scopeA, Request{Filter: gsearch.Filter{Tags: []string{"song"}}}); err != nil {
		t.Fatal(err)
	}
	paths := hits(got)
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"track/1.md=1", "track/2.md=2"}) || got.ModeUsed != gsearch.ModeFacet {
		t.Errorf("a filter alone, within A: %v (%s)", paths, got.ModeUsed)
	}
	if got, err = s.Search(f.ctx, Scope{"label_id": {"A", "B"}}, Request{Query: "lullaby", Mode: gsearch.ModeLexical}); err != nil || !slices.Equal(hits(got), []string{"track/3.md=3"}) {
		t.Errorf("lullaby within A and B: %v, %v", hits(got), err)
	}
	if got, err = s.Search(f.ctx, scopeA, Request{Query: "lullaby", Mode: gsearch.ModeLexical}); err != nil || len(got.Hits) != 0 {
		t.Errorf("lullaby within A: %v, %v", hits(got), err)
	}
}

// A query word or a filter facet naming an access field is refused, in any case: access is the
// scope's alone. A filter facet on another field is refused too (the tenant keeps none), while a
// field:value word of another field is only a word. The mutants, named here: the query's words not
// checked; field names compared with case.
func TestARequestNamingAnAccessFieldIsRefused(t *testing.T) {
	f := newFixture(t)
	s := f.open(qe)
	for name, r := range map[string]Request{
		"the query":          {Query: "label_id:B"},
		"among words":        {Query: "ballad label_id:B"},
		"in another case":    {Query: "LABEL_ID:B"},
		"the filter's facet": {Query: "ballad", Filter: gsearch.Filter{Facets: map[string][]string{"label_id": {"B"}}}},
		"a filter alone":     {Filter: gsearch.Filter{Facets: map[string][]string{"Label_Id": {"A"}}}},
	} {
		if _, err := s.Search(f.ctx, scopeA, r); !errors.Is(err, ErrProtectedFacet) {
			t.Errorf("%s: %v, want ErrProtectedFacet", name, err)
		}
	}
	if _, err := s.Search(f.ctx, scopeA, Request{Query: "ballad genre:rock"}); err != nil {
		t.Errorf("a word of another field: %v", err)
	}
	if _, err := s.Search(f.ctx, scopeA, Request{Query: "ballad", Filter: gsearch.Filter{Facets: map[string][]string{"genre": {"rock"}}}}); !errors.Is(err, ErrUnknownFacet) {
		t.Errorf("a facet of another field: %v, want ErrUnknownFacet", err)
	}
}

// A scope that is not the tenant's access is refused: none, a field without values, an empty or a
// repeated value, "*", a field the policy does not have, or the policy's field missing. A limit
// out of range is refused; a tenant with no policy cannot be opened.
func TestAScopeThatIsNotTheTenantsAccessIsRefused(t *testing.T) {
	f := newFixture(t)
	s := f.open(qe)
	for name, sc := range map[string]Scope{
		"none":          nil,
		"empty":         {},
		"no values":     {"label_id": {}},
		"an empty one":  {"label_id": {""}},
		"a repeat":      {"label_id": {"A", "A"}},
		"any":           {"label_id": {"*"}},
		"another field": {"label_id": {"A"}, "territory": {"US"}},
		"only another":  {"territory": {"US"}},
	} {
		if _, err := s.Search(f.ctx, sc, Request{Query: "ballad"}); !errors.Is(err, ErrScope) {
			t.Errorf("%s: %v, want ErrScope", name, err)
		}
	}
	for _, n := range []int{-1, MaxLimit + 1} {
		if _, err := s.Search(f.ctx, scopeA, Request{Query: "ballad", Limit: n}); !errors.Is(err, ErrLimit) {
			t.Errorf("limit %d: %v", n, err)
		}
	}
	if _, err := Open(f.ctx, f.conn, "nobody", qe); !errors.Is(err, ErrNoPolicy) {
		t.Errorf("a tenant with no policy: %v", err)
	}
}

// A Searcher held while the server's policy gains a field refuses the old scope at its next
// search: the policy is read every time, not when the Searcher was opened.
func TestAPolicyChangedUnderALiveSearcherRefusesTheOldScope(t *testing.T) {
	f := newFixture(t)
	s := f.open(qe)
	if _, err := s.Search(f.ctx, scopeA, Request{Query: "ballad"}); err != nil {
		t.Fatal(err)
	}
	f.policy("label_id", "team")
	if _, err := s.Search(f.ctx, scopeA, Request{Query: "ballad"}); !errors.Is(err, ErrScope) {
		t.Errorf("the old scope after the change: %v, want ErrScope", err)
	}
}

// The barrier at the Searcher: the policy gains a field, and a document that needs it is written,
// after the early check passed and before the search's view (the query embedder runs between the
// two). The view holds the scope to the policy in its own snapshot, so the search is refused; it
// never answers the new document under the old policy. The mutant, named here: the engine's store
// scoping by the early check alone (ViewWithin), which answers the new document.
func TestAPolicyChangedAfterTheEarlyCheckIsHeldInTheView(t *testing.T) {
	f := newFixture(t)
	var once sync.Once
	hook := func(ctx context.Context, q string) (string, []float32, error) {
		once.Do(func() {
			f.policy("label_id", "team")
			f.put("track/9.md", "9", map[string][]string{"label_id": {"A"}, "team": {"t1"}}, "crimson secret")
		})
		return qe(ctx, q)
	}
	s := f.open(hook)
	got, err := s.Search(f.ctx, scopeA, Request{Query: "crimson"})
	if !errors.Is(err, ErrScope) {
		t.Fatalf("the search after the change: %v, %v; want ErrScope", hits(got), err)
	}
}

// The engine's store fails closed: a view asked for without a search's call is ErrScope, and its
// function never runs.
func TestTheEnginesStoreFailsClosed(t *testing.T) {
	f := newFixture(t)
	ran := false
	err := scoped{f.st}.View(f.ctx, func(*pgstore.View) error { ran = true; return nil })
	if !errors.Is(err, ErrScope) || ran {
		t.Errorf("a view without a call: %v, ran %v", err, ran)
	}
}

// fixed is a provider answering one fixed vector, of length 5.
type fixed struct{}

func (fixed) Name() string       { return "fixed" }
func (fixed) Model() embed.Model { return embed.Model{Provider: "fixed", Name: "m", Dims: 2} }
func (fixed) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{3, 4}
	}
	return out, nil
}

// QueryEmbedder answers the provider's model fingerprint and the normalized vector, as the server
// stores the texts'.
func TestQueryEmbedderNormalizes(t *testing.T) {
	m, v, err := QueryEmbedder(fixed{})(context.Background(), "q")
	if err != nil || m != (fixed{}).Model().Fingerprint() || !slices.Equal(v, []float32{0.6, 0.8}) {
		t.Errorf("%q %v %v", m, v, err)
	}
}

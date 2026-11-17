package search

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	gsearch "github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/search/searchtest"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

// countingProvider is an embedding provider of model fp's dimensions that counts its calls and
// embeds as the fixtures' chunks are embedded; block, when set, holds each call until it closes or
// the call's ctx ends.
type countingProvider struct {
	model embed.Model
	calls atomic.Int64
	block chan struct{}
	fail  error
}

func (p *countingProvider) Name() string       { return p.model.Provider }
func (p *countingProvider) Model() embed.Model { return p.model }
func (p *countingProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	p.calls.Add(1)
	if p.block != nil {
		select {
		case <-p.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.fail != nil {
		return nil, p.fail
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = searchtest.Embed(t)
	}
	return out, nil
}

var (
	modelA = embed.Model{Provider: "test", Name: "embed", Dims: 64} // the fixtures' model
	modelB = embed.Model{Provider: "test", Name: "other", Dims: 64} // another model of the same size
)

// tenantDoc is a document of a tenant: its path, its access facets and its body.
type tenantDoc struct {
	path   string
	facets map[string][]string
	body   string
}

// openTenant makes tenant in conn with access as its policy and model m active, writes docs
// ready under m, and opens its Searcher with p's query embedder.
func openTenant(t *testing.T, conn dao.DataConn, tenant string, access []string, m embed.Model, p *countingProvider, docs ...tenantDoc) *Searcher {
	t.Helper()
	ctx := context.Background()
	st, err := pgstore.New(conn, tenant)
	if err != nil {
		t.Fatal(err)
	}
	fp := m.Fingerprint()
	if err := st.SetModels(ctx, fp, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPolicy(ctx, pgstore.Policy{AccessFields: access, View: tenant}); err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		c := gsearch.Chunk{Ord: 0, Breadcrumb: d.path, Body: d.body, ByteEnd: len(d.body)}
		if _, err := st.Put(ctx, pgstore.Document{Path: d.path, Title: d.body, Facets: d.facets, Chunks: []gsearch.Chunk{c}, SourceID: d.path}); err != nil {
			t.Fatal(err)
		}
		if err := st.Embed(ctx, fp, []pgstore.Embedding{{TextHash: c.TextHash(), Vec: searchtest.Embed(d.body)}}); err != nil {
			t.Fatal(err)
		}
		if ready, err := st.MarkReady(ctx, d.path, fp); err != nil || !ready {
			t.Fatalf("mark %s ready: %v, %v", d.path, ready, err)
		}
	}
	s, err := Open(ctx, conn, tenant, QueryEmbedder(p))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// globalFixture is three tenants with different access fields, every document about crimson:
// releases (label_id A and B), payees (org_id 7 and 8), tracks (label_id A, territory US and CA).
// releases and payees embed with pa, tracks with pb (another model).
type globalFixture struct {
	releases, payees, tracks *Searcher
	pa, pb                   *countingProvider
}

func newGlobalFixture(t *testing.T) *globalFixture {
	t.Helper()
	conn := pgtest.Scratch(t, pgstore.Migrate)
	f := &globalFixture{pa: &countingProvider{model: modelA}, pb: &countingProvider{model: modelB}}
	f.releases = openTenant(t, conn, "releases", []string{"label_id"}, modelA, f.pa,
		tenantDoc{"release/1.md", map[string][]string{"label_id": {"A"}}, "crimson river release"},
		tenantDoc{"release/2.md", map[string][]string{"label_id": {"B"}}, "crimson tide release"})
	f.payees = openTenant(t, conn, "payees", []string{"org_id"}, modelA, f.pa,
		tenantDoc{"payee/7.md", map[string][]string{"org_id": {"7"}}, "crimson sky payee"},
		tenantDoc{"payee/8.md", map[string][]string{"org_id": {"8"}}, "crimson sea payee"})
	f.tracks = openTenant(t, conn, "tracks", []string{"label_id", "territory"}, modelB, f.pb,
		tenantDoc{"track/1.md", map[string][]string{"label_id": {"A"}, "territory": {"US"}}, "crimson dawn track"},
		tenantDoc{"track/2.md", map[string][]string{"label_id": {"A"}, "territory": {"CA"}}, "crimson dusk track"})
	return f
}

// tenants are the fixture's three tenants, each scoped to one record of its own.
func (f *globalFixture) tenants() []TenantSearch {
	return []TenantSearch{
		{Tenant: "releases", Searcher: f.releases, Scope: Scope{"label_id": {"A"}}},
		{Tenant: "payees", Searcher: f.payees, Scope: Scope{"org_id": {"7"}}},
		{Tenant: "tracks", Searcher: f.tracks, Scope: Scope{"label_id": {"A"}, "territory": {"US"}}},
	}
}

func globalHits(r GlobalResult) []string {
	var out []string
	for _, h := range r.Hits {
		out = append(out, h.Tenant+":"+h.Path)
	}
	return out
}

// A search across three tenants with different access fields answers each within its own scope,
// every hit naming its tenant; the query is embedded once per model, however many tenants share
// one; and each tenant's mode and semantic state are reported. The mutants, named here: one
// tenant's scope applied to another (a payee answers under a release's scope, or the request is
// refused); the embedding cache keyed by text alone (the tracks' model gets the releases' vector,
// and its provider no call).
func TestASearchAcrossTenantsKeepsEachScope(t *testing.T) {
	f := newGlobalFixture(t)
	g := NewGlobal(8)
	res, err := g.Search(context.Background(), GlobalRequest{Query: "crimson", Tenants: f.tenants()})
	if err != nil {
		t.Fatal(err)
	}
	got := globalHits(res)
	slices.Sort(got)
	if want := []string{"payees:payee/7.md", "releases:release/1.md", "tracks:track/1.md"}; !slices.Equal(got, want) {
		t.Fatalf("hits %v; want each tenant's scoped record alone", got)
	}
	if a, b := f.pa.calls.Load(), f.pb.calls.Load(); a != 1 || b != 1 {
		t.Errorf("provider calls: %d of the releases' and payees' model, %d of the tracks'; want one each", a, b)
	}
	for _, name := range []string{"releases", "payees", "tracks"} {
		if tr := res.Tenants[name]; tr.ModeUsed != gsearch.ModeHybrid || tr.Semantic != gsearch.StateReady {
			t.Errorf("%s answered %+v; want hybrid, ready", name, tr)
		}
	}
}

// Every tenant is checked before anything is searched or embedded: a refused scope, an
// undeclared facet, or a limit out of range refuses the whole request, naming the tenant, and no
// provider is called. So do the request's own bounds: no tenant, more than MaxTenants, one twice, a
// weight outside (0, MaxWeight]. The mutant, named here: the checks run as each tenant is searched
// (a tenant searched, its query embedded, before another's refusal).
func TestARefusedTenantRefusesTheRequestBeforeAnyEmbedding(t *testing.T) {
	f := newGlobalFixture(t)
	g := NewGlobal(8)
	ts := f.tenants()
	ts[2].Scope = Scope{"label_id": {"A"}} // tracks need a territory too
	_, err := g.Search(context.Background(), GlobalRequest{Query: "crimson", Tenants: ts})
	var te *TenantError
	if !errors.As(err, &te) || te.Tenant != "tracks" || !errors.Is(err, ErrScope) {
		t.Fatalf("a refused scope: %v; want ErrScope naming tracks", err)
	}
	ts = f.tenants()
	ts[1].Filter = gsearch.Filter{Facets: map[string][]string{"status": {"x"}}}
	if _, err := g.Search(context.Background(), GlobalRequest{Query: "crimson", Tenants: ts}); !errors.As(err, &te) || te.Tenant != "payees" || !errors.Is(err, ErrUnknownFacet) {
		t.Fatalf("an undeclared facet: %v; want ErrUnknownFacet naming payees", err)
	}
	if n := f.pa.calls.Load() + f.pb.calls.Load(); n != 0 {
		t.Errorf("refused requests embedded %d queries; want none", n)
	}
	many := make([]TenantSearch, MaxTenants+1)
	for i := range many {
		many[i] = TenantSearch{Tenant: strings.Repeat("t", i+1), Searcher: f.releases, Scope: Scope{"label_id": {"A"}}}
	}
	for name, ts := range map[string][]TenantSearch{
		"none":        nil,
		"too many":    many,
		"named twice": {f.tenants()[0], f.tenants()[0]},
		"weight 11":   {{Tenant: "releases", Searcher: f.releases, Scope: Scope{"label_id": {"A"}}, Weight: 11}},
		"weight -1":   {{Tenant: "releases", Searcher: f.releases, Scope: Scope{"label_id": {"A"}}, Weight: -1}},
	} {
		if _, err := g.Search(context.Background(), GlobalRequest{Query: "crimson", Tenants: ts}); !errors.Is(err, ErrTenants) {
			t.Errorf("%s: %v; want ErrTenants", name, err)
		}
	}
}

// The merge is by relevance × weight, then the tenant's place in the request, then the path: two
// tenants' best hits, both first in their tenant (relevance 1), come in the request's order; a
// weight of 2 on the second puts it first; and a tenant answering by words alone, its best hit
// relevance 1 but a lower score than a hybrid tenant's, is first when weighted 1.5. The mutants,
// named here: the weight ignored; the merge by Score.
func TestTheMergeIsRelevanceTimesWeight(t *testing.T) {
	f := newGlobalFixture(t)
	g := NewGlobal(8)
	ts := f.tenants()[:2]
	res, err := g.Search(context.Background(), GlobalRequest{Query: "crimson", Mode: gsearch.ModeLexical, Tenants: ts})
	if err != nil {
		t.Fatal(err)
	}
	if got := globalHits(res); !slices.Equal(got, []string{"releases:release/1.md", "payees:payee/7.md"}) {
		t.Errorf("equal relevance: %v; want the request's order", got)
	}
	ts[1].Weight = 2
	if res, err = g.Search(context.Background(), GlobalRequest{Query: "crimson", Mode: gsearch.ModeLexical, Tenants: ts}); err != nil {
		t.Fatal(err)
	}
	if got := globalHits(res); !slices.Equal(got, []string{"payees:payee/7.md", "releases:release/1.md"}) {
		t.Errorf("payees weighted 2: %v; want payees first", got)
	}
	ts[0].Weight, ts[1].Weight = 0, 0
	if res, err = g.Search(context.Background(), GlobalRequest{Query: "crimson", Mode: gsearch.ModeLexical, Limit: 1, Tenants: ts}); err != nil || len(res.Hits) != 1 {
		t.Errorf("a limit of 1 across two tenants: %v, %v; want one hit", globalHits(res), err)
	}

	// relevance, not score: the releases' best hit is first in both arms (score about 2/61), the
	// tracks' first by words alone, their provider down (about 1/61); both are relevance 1, so a
	// weight of 1.5 puts the tracks' first, where their scores would not
	f.pb.fail = errors.New("the provider is down")
	both := []TenantSearch{f.tenants()[0], f.tenants()[2]}
	both[1].Weight = 1.5
	if res, err = g.Search(context.Background(), GlobalRequest{Query: "crimson", Tenants: both}); err != nil {
		t.Fatal(err)
	}
	if got := globalHits(res); !slices.Equal(got, []string{"tracks:track/1.md", "releases:release/1.md"}) || res.Tenants["tracks"].ModeUsed != gsearch.ModeLexical {
		t.Errorf("hybrid against words alone, words weighted 1.5: %v (%+v); want the tracks first", got, res.Tenants)
	}
}

// A tenant whose semantic arm is unavailable answers by words, and its entry says so, while the
// others answer hybrid. A tenant that fails (semantic mode, its query not embedded) fails the
// request, naming it, and cancels the others' searches.
func TestATenantsFailureIsItsOwnOrTheRequests(t *testing.T) {
	f := newGlobalFixture(t)
	f.pb.fail = errors.New("the provider is down")
	g := NewGlobal(8)
	res, err := g.Search(context.Background(), GlobalRequest{Query: "crimson", Tenants: f.tenants()})
	if err != nil {
		t.Fatal(err)
	}
	if tr := res.Tenants["tracks"]; tr.ModeUsed != gsearch.ModeLexical || tr.Semantic != gsearch.StateError || tr.SemanticError == "" {
		t.Errorf("tracks, their provider down: %+v; want by words, semantic error", tr)
	}
	if tr := res.Tenants["releases"]; tr.ModeUsed != gsearch.ModeHybrid {
		t.Errorf("releases: %+v; want hybrid", tr)
	}
	f.pa.block = make(chan struct{}) // the others' queries wait: the failure must cancel them
	start := time.Now()
	_, err = g.Search(context.Background(), GlobalRequest{Query: "crimson", Mode: gsearch.ModeSemantic, Tenants: f.tenants()})
	var te *TenantError
	if !errors.As(err, &te) || te.Tenant != "tracks" || !errors.Is(err, gsearch.ErrEmbedFailed) {
		t.Fatalf("a tenant failing in semantic mode: %v; want ErrEmbedFailed naming tracks", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the request took %v: the others' searches were not cancelled", took)
	}
}

// The Global's slots bound the tenants' searches in flight across every request: with one slot,
// two requests of two tenants each never run two at once, and a request whose deadline ends while
// it waits for a slot fails with it.
func TestTheGlobalSlotsBoundSearchesAcrossRequests(t *testing.T) {
	f := newGlobalFixture(t)
	f.pa.block = make(chan struct{})
	g := NewGlobal(1)
	ts := f.tenants()[:2]
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = g.Search(context.Background(), GlobalRequest{Query: "crimson", Mode: gsearch.ModeSemantic, Tenants: ts})
		}()
	}
	time.Sleep(200 * time.Millisecond)
	if n := f.pa.calls.Load(); n != 1 {
		t.Errorf("with one slot held, %d queries were embedded; want one", n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := g.Search(ctx, GlobalRequest{Query: "crimson", Mode: gsearch.ModeSemantic, Tenants: ts}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a request whose deadline ends waiting for a slot: %v; want its deadline", err)
	}
	close(f.pa.block)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("request %d: %v", i, err)
		}
	}
}

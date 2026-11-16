package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	gsearch "github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/searchtest"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// facetsDecl is the faceted fixture's declaration: two keywords, a date and a number.
var facetsDecl = map[string]string{"status": pgstore.FacetKeyword, "territory": pgstore.FacetKeyword, "released": pgstore.FacetDate, "bpm": pgstore.FacetNumber}

// faceted is newFixture's tenant declaring facetsDecl, its tracks rewritten with their facets:
//
//	track/1 (A) crimson river ballad   status released,  territory CA and US, 2024-03-01, bpm 120
//	track/2 (A) golden morning anthem  status scheduled, territory US,        2025-02-01, bpm 90 and 126
//	track/3 (B) midnight ocean lullaby status released,  territory US,        2024-03-01, bpm 120
//	track/4 (A) crimson dawn ballad    status Released,  territory GB,        2023-12-31T23:00Z, bpm 118
func faceted(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	if err := f.st.SetPolicy(f.ctx, pgstore.Policy{AccessFields: []string{"label_id"}, View: "track", Facets: facetsDecl}); err != nil {
		t.Fatal(err)
	}
	f.putTyped("track/1.md", "1", "A", "crimson river ballad", map[string][]string{"status": {"released"}, "territory": {"CA", "US"}},
		map[string][]string{"released": {"2024-03-01"}, "bpm": {"120"}})
	f.putTyped("track/2.md", "2", "A", "golden morning anthem", map[string][]string{"status": {"scheduled"}, "territory": {"US"}},
		map[string][]string{"released": {"2025-02-01"}, "bpm": {"90", "126"}})
	f.putTyped("track/3.md", "3", "B", "midnight ocean lullaby", map[string][]string{"status": {"released"}, "territory": {"US"}},
		map[string][]string{"released": {"2024-03-01"}, "bpm": {"120"}})
	f.putTyped("track/4.md", "4", "A", "crimson dawn ballad", map[string][]string{"status": {"Released"}, "territory": {"GB"}},
		map[string][]string{"released": {"2023-12-31T23:00:00Z"}, "bpm": {"118"}})
	return f
}

// putTyped writes a ready track of label with its keyword facets and its typed values, as ingest
// keeps them: keywords beside the access pair, the others parsed by their declared type.
func (f *fixture) putTyped(path, sid, label, body string, keywords, typed map[string][]string) {
	f.t.Helper()
	facets := map[string][]string{"label_id": {label}}
	for k, v := range keywords {
		facets[k] = v
	}
	values := map[string][]float64{}
	for k, texts := range typed {
		for _, s := range texts {
			v, err := pgstore.TypedValue(facetsDecl[k], s)
			if err != nil {
				f.t.Fatal(err)
			}
			values[k] = append(values[k], v)
		}
	}
	c := gsearch.Chunk{Ord: 0, Breadcrumb: path, Body: body, ByteEnd: len(body)}
	d := pgstore.Document{Path: path, Title: body, Tags: []string{"song"}, Facets: facets, Values: values, Chunks: []gsearch.Chunk{c}, SourceID: sid}
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

// sortedHits is r's hits as path=source_id, sorted.
func sortedHits(r Result) []string {
	out := hits(r)
	slices.Sort(out)
	return out
}

// Keyword facets filter exactly, case included: values ORed within a field, fields ANDed, a list
// value matching any of its values, within the scope, by a filter alone (listed), with words
// (lexical) and by meaning (semantic). A query word name:value for a declared keyword facet is a
// filter too. The mutants, named here: a keyword ORed across fields; the engine given no Fields
// (the query word stays a word, and the filter is refused by golib).
func TestKeywordFacetsFilterExactly(t *testing.T) {
	f := faceted(t)
	s := f.open(qe)
	facets := func(kv ...string) gsearch.Filter {
		m := map[string][]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = append(m[kv[i]], kv[i+1])
		}
		return gsearch.Filter{Facets: m}
	}
	for _, c := range []struct {
		name string
		r    Request
		want []string
	}{
		{"status released, listed", Request{Filter: facets("status", "released")}, []string{"track/1.md=1"}},
		{"status Released, listed: case exact", Request{Filter: facets("status", "Released")}, []string{"track/4.md=4"}},
		{"released or scheduled", Request{Filter: facets("status", "released", "status", "scheduled")}, []string{"track/1.md=1", "track/2.md=2"}},
		{"released or scheduled, and CA", Request{Filter: facets("status", "released", "status", "scheduled", "territory", "CA")}, []string{"track/1.md=1"}},
		{"territory US: a list value", Request{Filter: facets("territory", "US")}, []string{"track/1.md=1", "track/2.md=2"}},
		{"crimson, status released, by words", Request{Query: "crimson", Mode: gsearch.ModeLexical, Filter: facets("status", "released")}, []string{"track/1.md=1"}},
		{"crimson status:Released, a query word", Request{Query: "crimson status:Released", Mode: gsearch.ModeLexical}, []string{"track/4.md=4"}},
		{"golden morning anthem by meaning, status released", Request{Query: "golden morning anthem", Mode: gsearch.ModeSemantic, Filter: facets("status", "released")}, []string{"track/1.md=1"}},
	} {
		got, err := s.Search(f.ctx, scopeA, c.r)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if paths := sortedHits(got); !slices.Equal(paths, c.want) {
			t.Errorf("%s: %v; want %v", c.name, paths, c.want)
		}
	}
}

// Ranges filter number and date facets through Search, every bound read as the facet's values
// are (a number from a decimal, a date from YYYY-MM-DD or RFC 3339), ANDed with each other, with
// keyword facets and with the scope, by a filter alone, with words and by meaning. The B track,
// within every range given, never answers within A.
func TestRangesFilterNumbersAndDates(t *testing.T) {
	f := faceted(t)
	s := f.open(qe)
	for _, c := range []struct {
		name string
		r    Request
		want []string
	}{
		{"bpm 118 to 120", Request{Ranges: map[string]Range{"bpm": {Gte: "118", Lte: "120"}}}, []string{"track/1.md=1", "track/4.md=4"}},
		{"bpm above 120", Request{Ranges: map[string]Range{"bpm": {Gt: "120"}}}, []string{"track/2.md=2"}},
		{"released in 2024", Request{Ranges: map[string]Range{"released": {Gte: "2024-01-01", Lt: "2025-01-01"}}}, []string{"track/1.md=1"}},
		{"released up to an instant", Request{Ranges: map[string]Range{"released": {Lte: "2023-12-31T23:00:00Z"}}}, []string{"track/4.md=4"}},
		{"bpm 119 up, before 2025", Request{Ranges: map[string]Range{"bpm": {Gte: "119"}, "released": {Lt: "2025-01-01"}}}, []string{"track/1.md=1"}},
		{"crimson, before 2024, by words", Request{Query: "crimson", Mode: gsearch.ModeLexical, Ranges: map[string]Range{"released": {Lt: "2024-01-01"}}}, []string{"track/4.md=4"}},
		{"by meaning, bpm under 100", Request{Query: "crimson river ballad", Mode: gsearch.ModeSemantic, Ranges: map[string]Range{"bpm": {Lt: "100"}}}, []string{"track/2.md=2"}},
		{"status released and bpm 120", Request{Filter: gsearch.Filter{Facets: map[string][]string{"status": {"released"}}}, Ranges: map[string]Range{"bpm": {Gte: "120", Lte: "120"}}}, []string{"track/1.md=1"}},
	} {
		got, err := s.Search(f.ctx, scopeA, c.r)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if paths := sortedHits(got); !slices.Equal(paths, c.want) {
			t.Errorf("%s: %v; want %v", c.name, paths, c.want)
		}
	}
	got, err := s.Search(f.ctx, Scope{"label_id": {"B"}}, Request{Ranges: map[string]Range{"bpm": {Gte: "120", Lte: "120"}}})
	if err != nil || !slices.Equal(sortedHits(got), []string{"track/3.md=3"}) {
		t.Errorf("bpm 120 within B: %v, %v", hits(got), err)
	}
}

// Every refusal of a facet or a range is named, before any query is embedded: an access field as
// a facet, a range or a query word (ErrProtectedFacet); an undeclared field, naming the declared
// ones (ErrUnknownFacet); equality on a number or a date, a range on a keyword, a bound that does
// not parse, NaN or an infinity (ErrFacetType); a facet with no value, too many values or ranges,
// a range with no bound or crossed bounds (ErrFilter). The mutant, named here: the type check
// skipped (a range on a keyword passes, and matches nothing).
func TestEveryFacetRefusalIsNamed(t *testing.T) {
	f := faceted(t)
	embedded := 0
	s := f.open(func(ctx context.Context, q string) (string, []float32, error) { embedded++; return qe(ctx, q) })
	many := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprint(i)
		}
		return out
	}
	ranges := map[string]Range{}
	for i := range MaxRanges + 1 {
		ranges[fmt.Sprintf("f%d", i)] = Range{Gte: "1"}
	}
	for _, c := range []struct {
		name  string
		r     Request
		want  error
		names string
	}{
		{"an access field as a facet", Request{Query: "x", Filter: gsearch.Filter{Facets: map[string][]string{"label_id": {"B"}}}}, ErrProtectedFacet, "label_id"},
		{"an access field as a range", Request{Query: "x", Ranges: map[string]Range{"Label_Id": {Gte: "1"}}}, ErrProtectedFacet, "Label_Id"},
		{"an access field as a query word", Request{Query: "x label_id:B"}, ErrProtectedFacet, "label_id:B"},
		{"an undeclared facet", Request{Query: "x", Filter: gsearch.Filter{Facets: map[string][]string{"genre": {"rock"}}}}, ErrUnknownFacet, "bpm (number), released (date), status (keyword), territory (keyword)"},
		{"an undeclared range", Request{Query: "x", Ranges: map[string]Range{"rating": {Gte: "1"}}}, ErrUnknownFacet, "rating"},
		{"equality on a number", Request{Query: "x", Filter: gsearch.Filter{Facets: map[string][]string{"bpm": {"120"}}}}, ErrFacetType, "bpm is a number facet"},
		{"equality on a date", Request{Query: "x", Filter: gsearch.Filter{Facets: map[string][]string{"released": {"2024-03-01"}}}}, ErrFacetType, "released"},
		{"a range on a keyword", Request{Query: "x", Ranges: map[string]Range{"status": {Gte: "a"}}}, ErrFacetType, "status is a keyword facet"},
		{"a number bound that does not parse", Request{Query: "x", Ranges: map[string]Range{"bpm": {Gte: "fast"}}}, ErrFacetType, "bpm"},
		{"a date bound that does not parse", Request{Query: "x", Ranges: map[string]Range{"released": {Lt: "next year"}}}, ErrFacetType, "released"},
		{"NaN", Request{Query: "x", Ranges: map[string]Range{"bpm": {Gte: "NaN"}}}, ErrFacetType, "bpm"},
		{"an infinity", Request{Query: "x", Ranges: map[string]Range{"bpm": {Lt: "+Inf"}}}, ErrFacetType, "bpm"},
		{"a facet with no value", Request{Query: "x", Filter: gsearch.Filter{Facets: map[string][]string{"status": {}}}}, ErrFilter, "status"},
		{"too many values", Request{Query: "x", Filter: gsearch.Filter{Facets: map[string][]string{"status": many(MaxFacetValues + 1)}}}, ErrFilter, "at most 64"},
		{"too many ranges", Request{Query: "x", Ranges: ranges}, ErrFilter, "at most 16"},
		{"a range with no bound", Request{Query: "x", Ranges: map[string]Range{"bpm": {}}}, ErrFilter, "bpm"},
		{"crossed bounds", Request{Query: "x", Ranges: map[string]Range{"bpm": {Gt: "130", Lt: "120"}}}, ErrFilter, "bpm"},
	} {
		_, err := s.Search(f.ctx, scopeA, c.r)
		if !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.names) {
			t.Errorf("%s: %v; want %v naming %q", c.name, err, c.want, c.names)
		}
	}
	if embedded != 0 {
		t.Errorf("refused requests embedded %d queries; want none", embedded)
	}
	if _, err := s.Search(f.ctx, scopeA, Request{Query: "x", Filter: gsearch.Filter{Facets: map[string][]string{"status": many(MaxFacetValues)}},
		Ranges: map[string]Range{"bpm": {Gte: "1"}}}); err != nil {
		t.Errorf("64 values and a range: %v", err)
	}
}

// A tenant declaring no facet refuses any facet as it always has, saying it declares none.
func TestATenantWithoutFacetsRefusesThem(t *testing.T) {
	f := newFixture(t)
	s := f.open(qe)
	_, err := s.Search(f.ctx, scopeA, Request{Query: "x", Ranges: map[string]Range{"bpm": {Gte: "1"}}})
	if !errors.Is(err, ErrUnknownFacet) || !strings.Contains(err.Error(), "declares no facet") {
		t.Errorf("a range: %v", err)
	}
}

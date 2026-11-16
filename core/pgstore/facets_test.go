package pgstore

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/searchtest"
)

// valued writes a ready document of label A (or the label given) with one chunk, body, and values
// as its number and date facets' values.
func valued(t *testing.T, s *Store, path, label, body string, values map[string][]float64) {
	t.Helper()
	ctx := context.Background()
	c := search.Chunk{Ord: 0, Breadcrumb: path, Body: body, ByteEnd: len(body)}
	if _, err := s.Put(ctx, Document{Path: path, Facets: map[string][]string{"label_id": {label}}, Chunks: []search.Chunk{c}, Values: values}); err != nil {
		t.Fatal(err)
	}
	if err := s.Embed(ctx, model, []Embedding{{TextHash: c.TextHash(), Vec: searchtest.Embed(body)}}); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.MarkReady(ctx, path, model); err != nil || !ready {
		t.Fatalf("mark %s ready: %v, %v", path, ready, err)
	}
}

// valueRows are the tenant's rag_document_value rows as path:field=value, sorted.
func valueRows(t *testing.T, conn dao.DataConn, tenant string) []string {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), `SELECT d.path || ':' || v.field || '=' || v.value::text
		FROM rag_document_value v JOIN rag_document d ON d.tenant = v.tenant AND d.id = v.doc_id WHERE v.tenant = $1`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// A document's typed values are written with it, one row per distinct value, and replaced whole
// by its next write, on the single path and in a batch: a value the new write drops leaves no row.
// They go with the document when it is deleted, alone or in a batch, through the foreign key.
// Another tenant's rows are never touched. The mutants, named here: the update path not deleting
// the old values; the batch path writing none.
func TestTypedValuesAreReplacedWithTheirDocument(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", nil)
	other := openCorpus(t, conn, "u", nil)
	valued(t, other, "x.md", "A", "other tenant", map[string][]float64{"bpm": {120}})
	valued(t, s, "x.md", "A", "first", map[string][]float64{"bpm": {120, 126, 126}, "day": {1.7e9}})
	if got, want := valueRows(t, conn, "t"), []string{"x.md:bpm=120", "x.md:bpm=126", "x.md:day=1700000000"}; !slices.Equal(got, want) {
		t.Fatalf("after the first write: %v; want %v", got, want)
	}
	valued(t, s, "x.md", "A", "second", map[string][]float64{"bpm": {90}})
	if got, want := valueRows(t, conn, "t"), []string{"x.md:bpm=90"}; !slices.Equal(got, want) {
		t.Fatalf("after a write dropping values: %v; want %v", got, want)
	}
	b := s.Begin(ctx)
	if _, err := b.Put(ctx, Document{Path: "x.md", Chunks: []search.Chunk{{Body: "third"}}, Values: map[string][]float64{"bpm": {100, 101}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put(ctx, Document{Path: "y.md", Chunks: []search.Chunk{{Body: "y"}}, Values: map[string][]float64{"day": {5}}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	if got, want := valueRows(t, conn, "t"), []string{"x.md:bpm=100", "x.md:bpm=101", "y.md:day=5"}; !slices.Equal(got, want) {
		t.Fatalf("after a batch: %v; want %v", got, want)
	}
	b = s.Begin(ctx)
	if _, err := b.Put(ctx, Document{Path: "x.md", Chunks: []search.Chunk{{Body: "fourth"}}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(ctx, "y.md"); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := valueRows(t, conn, "t"); len(got) != 0 {
		t.Fatalf("after a batch writing x.md with no value and deleting y.md: %v; want none", got)
	}
	valued(t, s, "x.md", "A", "fifth", map[string][]float64{"bpm": {1}})
	if err := s.Delete(ctx, "x.md"); err != nil {
		t.Fatal(err)
	}
	if got := valueRows(t, conn, "t"); len(got) != 0 {
		t.Fatalf("after a delete: %v; want none", got)
	}
	if got, want := valueRows(t, conn, "u"), []string{"x.md:bpm=120"}; !slices.Equal(got, want) {
		t.Fatalf("the other tenant's values: %v; want %v", got, want)
	}
	for _, bad := range []map[string][]float64{{"bpm": {math.NaN()}}, {"bpm": {math.Inf(1)}}, {"not a name": {1}}} {
		if _, err := s.Put(ctx, Document{Path: "z.md", Values: bad}); !errors.Is(err, errs.ErrInvalidArgument) {
			t.Errorf("a document with values %v: %v; want ErrInvalidArgument", bad, err)
		}
	}
}

// day is a date as Unix seconds, as TypedValue reads it.
func day(t *testing.T, s string) float64 {
	t.Helper()
	v, err := TypedValue(FacetDate, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func f64(v float64) *float64 { return &v }

// The ranged corpus, label A unless said: t1 bpm 120 on 2024-03-01; t2 bpm 90 and 126 (a list) on
// 2025-02-01; t3 bpm 118 at 2023-12-31T23:00Z; t4 no value; b1 of label B, bpm 120 on 2024-03-01.
func rangedCorpus(t *testing.T) (*Store, dao.DataConn) {
	t.Helper()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", nil)
	valued(t, s, "t1.md", "A", "common words one", map[string][]float64{"bpm": {120}, "day": {day(t, "2024-03-01")}})
	valued(t, s, "t2.md", "A", "common words two", map[string][]float64{"bpm": {90, 126}, "day": {day(t, "2025-02-01")}})
	valued(t, s, "t3.md", "A", "common words three", map[string][]float64{"bpm": {118}, "day": {day(t, "2023-12-31T23:00:00Z")}})
	valued(t, s, "t4.md", "A", "common words four", nil)
	valued(t, s, "b1.md", "B", "common words bee", map[string][]float64{"bpm": {120}, "day": {day(t, "2024-03-01")}})
	return s, conn
}

// A range is ANDed in SQL on every retrieval path: Lexical, Semantic (exact) and List answer the
// same documents, within the View's access. Each operator holds at its bound (gt and lt exclusive,
// gte and lte inclusive), a list value matches when any of its values is within every bound,
// ranges on two fields are ANDed, a range with a keyword filter is ANDed with it, and a document
// with no value never matches. The mutants, named here: a bound's inclusive and exclusive
// swapped; ranges ORed across fields; ranges left out of where.
func TestARangeIsANDedOnEveryRetrievalPath(t *testing.T) {
	ctx := context.Background()
	s, _ := rangedCorpus(t)
	q := searchtest.Embed("common words")
	cases := []struct {
		name   string
		ranges []Range
		filter search.Filter
		want   []string
	}{
		{"bpm ≥ 120", []Range{{Field: "bpm", Gte: f64(120)}}, search.Filter{}, []string{"t1.md", "t2.md"}},
		{"bpm > 120", []Range{{Field: "bpm", Gt: f64(120)}}, search.Filter{}, []string{"t2.md"}},
		{"bpm ≤ 118", []Range{{Field: "bpm", Lte: f64(118)}}, search.Filter{}, []string{"t2.md", "t3.md"}},
		{"bpm < 118", []Range{{Field: "bpm", Lt: f64(118)}}, search.Filter{}, []string{"t2.md"}},
		{"118 ≤ bpm ≤ 120", []Range{{Field: "bpm", Gte: f64(118), Lte: f64(120)}}, search.Filter{}, []string{"t1.md", "t3.md"}},
		{"100 < bpm < 125, no value of the list within", []Range{{Field: "bpm", Gt: f64(100), Lt: f64(125)}}, search.Filter{}, []string{"t1.md", "t3.md"}},
		{"2024", []Range{{Field: "day", Gte: f64(day(t, "2024-01-01")), Lt: f64(day(t, "2025-01-01"))}}, search.Filter{}, []string{"t1.md"}},
		{"up to 2023-12-31T23:00Z", []Range{{Field: "day", Lte: f64(day(t, "2023-12-31T23:00:00Z"))}}, search.Filter{}, []string{"t3.md"}},
		{"bpm ≥ 119 and before 2025", []Range{{Field: "day", Lt: f64(day(t, "2025-01-01"))}, {Field: "bpm", Gte: f64(119)}}, search.Filter{}, []string{"t1.md"}},
		{"a field no document has", []Range{{Field: "rating", Gte: f64(0)}}, search.Filter{}, nil},
		{"with a path filter", []Range{{Field: "bpm", Gte: f64(90)}}, search.Filter{Paths: []string{"t2.md"}}, []string{"t2.md"}},
	}
	for _, c := range cases {
		var lex, sem, list []string
		err := s.ViewWithin(ctx, map[string][]string{"label_id": {"A"}}, func(v *View) error {
			if err := v.Within(c.ranges); err != nil {
				return err
			}
			l, err := v.Lexical(ctx, []search.Term{{Text: "common"}}, c.filter, 100)
			if err != nil {
				return err
			}
			m, err := v.Semantic(ctx, model, q, c.filter, 100)
			if err != nil {
				return err
			}
			ls, err := v.List(ctx, c.filter, 100)
			lex, sem, list = sortedPaths(l), sortedPaths(m), sortedPaths(ls)
			return err
		})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for path, got := range map[string][]string{"lexical": lex, "semantic": sem, "list": list} {
			if !slices.Equal(got, c.want) {
				t.Errorf("%s, %s: %v; want %v", c.name, path, got, c.want)
			}
		}
	}
}

func sortedPaths(cs []search.Candidate[int64]) []string {
	var out []string
	for _, c := range cs {
		if !slices.Contains(out, c.Path) {
			out = append(out, c.Path)
		}
	}
	slices.Sort(out)
	return out
}

// A range no search can answer is refused before anything is read (ErrRange), naming its field,
// and leaves the View as it was: no bound, a bound that is not finite, a low bound above its high
// one, a field that is not a facet name.
func TestARangeNoSearchCanAnswerIsRefused(t *testing.T) {
	ctx := context.Background()
	s, _ := rangedCorpus(t)
	for name, r := range map[string]Range{
		"no bound":          {Field: "bpm"},
		"NaN":               {Field: "bpm", Gte: f64(math.NaN())},
		"infinite":          {Field: "bpm", Lt: f64(math.Inf(1))},
		"low above high":    {Field: "bpm", Gt: f64(10), Lte: f64(9)},
		"inclusive crossed": {Field: "bpm", Gte: f64(10), Lt: f64(9)},
		"not a name":        {Field: "b p m", Gte: f64(1)},
	} {
		err := s.ViewWithin(ctx, map[string][]string{"label_id": {"A"}}, func(v *View) error {
			if err := v.Within([]Range{{Field: "day", Gte: f64(0)}, r}); err != nil {
				return err
			}
			return errors.New("accepted")
		})
		if !errors.Is(err, ErrRange) || !strings.Contains(err.Error(), r.Field) {
			t.Errorf("%s: %v; want ErrRange naming %q", name, err, r.Field)
		}
	}
	// an equal low and high bound, inclusive, is one value
	err := s.ViewWithin(ctx, map[string][]string{"label_id": {"A"}}, func(v *View) error {
		if err := v.Within([]Range{{Field: "bpm", Gte: f64(118), Lte: f64(118)}}); err != nil {
			return err
		}
		ls, err := v.List(ctx, search.Filter{}, 10)
		if got := sortedPaths(ls); err == nil && !slices.Equal(got, []string{"t3.md"}) {
			return fmt.Errorf("bpm = 118: %v", got)
		}
		return err
	})
	if err != nil {
		t.Error(err)
	}
}

// TypedValue reads a number as a finite decimal and a date as YYYY-MM-DD (midnight UTC) or RFC
// 3339, as Unix seconds; a keyword, NaN, an infinity and anything else are ErrFacetValue.
func TestTypedValuesParse(t *testing.T) {
	for _, c := range []struct {
		typ, text string
		want      float64
	}{
		{FacetNumber, "120", 120}, {FacetNumber, " -1.5e2 ", -150}, {FacetNumber, "0", 0},
		{FacetDate, "1970-01-02", 86400}, {FacetDate, "2024-01-01T00:00:00Z", 1704067200},
		{FacetDate, "2024-01-01T01:00:00+01:00", 1704067200}, {FacetDate, "1970-01-01T00:00:00.5Z", 0.5},
	} {
		if got, err := TypedValue(c.typ, c.text); err != nil || got != c.want {
			t.Errorf("%s %q: %v, %v; want %v", c.typ, c.text, got, err, c.want)
		}
	}
	for _, c := range [][2]string{{FacetNumber, "NaN"}, {FacetNumber, "Inf"}, {FacetNumber, "-inf"}, {FacetNumber, "1e400"}, {FacetNumber, "12 bpm"},
		{FacetNumber, ""}, {FacetDate, "2024-13-01"}, {FacetDate, "01/02/2024"}, {FacetDate, "2024-01-01 00:00"}, {FacetKeyword, "x"}} {
		if _, err := TypedValue(c[0], c[1]); !errors.Is(err, ErrFacetValue) {
			t.Errorf("%s %q: %v; want ErrFacetValue", c[0], c[1], err)
		}
	}
}

// CheckFacets refuses, naming the facet: an access field in any case, tags, path or title, a name
// that is not a plain identifier, and an unknown type. A declaration of keyword, number and date
// facets beside the access fields passes.
func TestAFacetDeclarationIsChecked(t *testing.T) {
	access := []string{"label_id"}
	if err := CheckFacets(access, map[string]string{"status": FacetKeyword, "bpm": FacetNumber, "released": FacetDate}); err != nil {
		t.Fatal(err)
	}
	for name, typ := range map[string]string{"Label_ID": FacetKeyword, "tags": FacetKeyword, "Path": FacetKeyword, "title": FacetNumber,
		"a-b": FacetKeyword, "a=b": FacetKeyword, "1st": FacetNumber, "": FacetKeyword, "genre": "text"} {
		err := CheckFacets(access, map[string]string{name: typ})
		if !errors.Is(err, ErrFacetDecl) || !strings.Contains(err.Error(), fmt.Sprintf("%q", name)) && !strings.Contains(err.Error(), name+"'s") {
			t.Errorf("%q = %q: %v; want ErrFacetDecl naming it", name, typ, err)
		}
	}
}

// A policy keeps its declared facets, read back by Policy and inside ViewPolicy's snapshot. A
// replacement whose facets differ says so and forgets the tenant's last refresh, in the same
// transaction, so the ingester refreshes at once; one with the same facets (or none, as before)
// leaves it. A declaration CheckFacets refuses is not recorded.
func TestAChangedDeclarationForgetsTheLastRefresh(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", nil)
	refreshed := func(ts string) {
		t.Helper()
		b := s.Begin(ctx)
		at, err := time.Parse(time.DateOnly, ts)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.SetRefreshed(ctx, Refresh{Started: at, Finished: at}); err != nil {
			t.Fatal(err)
		}
		if err := b.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	replace := func(facets map[string]string) bool {
		t.Helper()
		changed, err := s.ReplacePolicy(ctx, Policy{AccessFields: []string{"label_id"}, View: "v", Facets: facets})
		if err != nil {
			t.Fatal(err)
		}
		return changed
	}
	if replace(nil) {
		t.Fatal("a first policy with no facets: changed")
	}
	refreshed("2026-01-01")
	if replace(map[string]string{}) {
		t.Fatal("no facets again: changed")
	}
	if r, _ := s.Refreshed(ctx); r.Finished.IsZero() {
		t.Fatal("no facets again forgot the refresh")
	}
	decl := map[string]string{"status": FacetKeyword, "bpm": FacetNumber}
	if !replace(decl) {
		t.Fatal("facets declared: not changed")
	}
	if r, _ := s.Refreshed(ctx); !r.Finished.IsZero() || !r.Started.IsZero() {
		t.Fatalf("facets declared: the refresh is still %+v", r)
	}
	p, err := s.Policy(ctx)
	if err != nil || !maps.Equal(p.Facets, decl) {
		t.Fatalf("the policy read back: %+v, %v", p, err)
	}
	err = s.ViewPolicy(ctx, map[string][]string{"label_id": {"A"}}, func(p Policy, _ map[string][]string) error {
		if !maps.Equal(p.Facets, decl) {
			return fmt.Errorf("in the snapshot: %v", p.Facets)
		}
		return nil
	}, func(*View) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	refreshed("2026-01-02")
	if replace(map[string]string{"bpm": FacetNumber, "status": FacetKeyword}) {
		t.Fatal("the same facets: changed")
	}
	if !replace(map[string]string{"bpm": FacetDate, "status": FacetKeyword}) {
		t.Fatal("a type changed: not changed")
	}
	if _, err := s.ReplacePolicy(ctx, Policy{AccessFields: []string{"label_id"}, Facets: map[string]string{"label_id": FacetKeyword}}); !errors.Is(err, ErrFacetDecl) {
		t.Fatalf("an access field declared a facet: %v", err)
	}
	if p, _ := s.Policy(ctx); p.Facets["bpm"] != FacetDate {
		t.Fatalf("the refused declaration was recorded: %v", p.Facets)
	}
}

// rangedANNCorpus is annCorpus's 20,000 documents of tenant t, each with one value of "n": its
// number modulo 100, so n ≥ 50 holds for half of them and n = 7 for 200.
func rangedANNCorpus(t *testing.T, cfg ANNConfig) (dao.DataConn, *ANN, string) {
	t.Helper()
	conn, a := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 20000, annDims, 0.4)
	for _, q := range []string{
		`INSERT INTO rag_document_value (tenant, doc_id, field, value)
		  SELECT tenant, id, 'n', (substring(path from 5)::int % 100) FROM rag_document WHERE tenant = 't'`,
		`ANALYZE rag_document_value`,
	} {
		if _, err := conn.ExecContext(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	buildFor(t, a, model)
	return conn, a, model
}

// nOf is each document's n, by id.
func nOf(t *testing.T, conn dao.DataConn) map[int64]float64 {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), `SELECT doc_id, value FROM rag_document_value WHERE tenant = 't' AND field = 'n'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int64]float64{}
	for rows.Next() {
		var id int64
		var v float64
		if err := rows.Scan(&id, &v); err != nil {
			t.Fatal(err)
		}
		out[id] = v
	}
	return out
}

// rangedSemantic is the top n for vec through st, the View narrowed to r.
func rangedSemantic(t *testing.T, st *Store, model string, vec []float32, r Range, n int) []int64 {
	t.Helper()
	var out []search.Candidate[int64]
	err := st.View(context.Background(), func(v *View) error {
		if err := v.Within([]Range{r}); err != nil {
			return err
		}
		var err error
		out, err = v.Semantic(context.Background(), model, vec, search.Filter{}, n)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return docsOf(out)
}

// On the approximate path a range is ANDed in the candidate stage. A wide range (half of the
// tenant) is answered through the index: one candidate stage, every hit within the range, no
// underfill. A narrow one (200 documents, at most ann_exact_scope_docs) is answered exactly up
// front, the index untried, counted as exact_scoped, and its answer is the exact one. The
// mutants, named here: the ranges left out of the candidate stage's where (a wide range's hits
// include documents outside it); the small-scope count reading the filter alone (a narrow range
// tries the index).
func TestANN_ARangeIsANDedOnTheIndexPath(t *testing.T) {
	cfg := DefaultANNConfig()
	cfg.Threshold = 1000
	conn, built, model := rangedANNCorpus(t, cfg)
	exact, _ := New(conn, "t")
	at, _ := New(conn, "t", WithANN(built))
	ns := nOf(t, conn)
	q := queryVecs(t, conn, "t", model, 1)[0]

	wide := Range{Field: "n", Gte: f64(50)}
	scoped, underfill := built.scopedOf(model).Load(), built.underfillOf(model).Load()
	h := &annRan{}
	withViewHook(t, h)
	got := rangedSemantic(t, at, model, q, wide, 50)
	viewHook = nil
	if h.cand != 1 || len(got) != 50 {
		t.Fatalf("a range over half the tenant: %d candidate stages, %d hits; want the index and 50", h.cand, len(got))
	}
	for _, id := range got {
		if ns[id] < 50 {
			t.Fatalf("a range over half the tenant answered document %d, n = %v", id, ns[id])
		}
	}
	if d, u := built.scopedOf(model).Load()-scoped, built.underfillOf(model).Load()-underfill; d != 0 || u != 0 {
		t.Fatalf("a range over half the tenant: exact_scoped +%d, underfill +%d; want neither", d, u)
	}

	narrow := Range{Field: "n", Gte: f64(7), Lte: f64(7)}
	h = &annRan{}
	withViewHook(t, h)
	got = rangedSemantic(t, at, model, q, narrow, 10)
	viewHook = nil
	if want := rangedSemantic(t, exact, model, q, narrow, 10); h.cand != 0 || !slices.Equal(got, want) {
		t.Fatalf("a range over 200 documents: %d candidate stages, %v; want none and the exact answer %v", h.cand, got, want)
	}
	if d := built.scopedOf(model).Load() - scoped; d != 1 {
		t.Fatalf("a range over 200 documents: exact_scoped +%d; want 1", d)
	}
}

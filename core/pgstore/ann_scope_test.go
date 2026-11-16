package pgstore

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"
)

// A search whose filter admits at most ExactScopeDocs ready documents is answered exactly, the
// index untried: no candidate stage runs, the answer is the exact one, and the small-scope count
// rises while the underfill count stays. At one document past the bound, and with no filter, the
// index answers. The corpus's "rare" tag is on 200 of 20,000 documents, "tenth" on 2,000.
func TestANN_ASmallScopeIsAnsweredExactlyUpFront(t *testing.T) {
	cfg := DefaultANNConfig()
	cfg.Threshold, cfg.ExactScopeDocs = 1000, 200
	conn, built := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 20000, annDims, 0.4)
	buildFor(t, built, model)
	exact, _ := New(conn, "t")
	at, _ := New(conn, "t", WithANN(built))
	under := cfg
	under.ExactScopeDocs = 199
	below := NewANN(conn, under, "scope-below")
	past, _ := New(conn, "t", WithANN(below))
	q := queryVecs(t, conn, "t", model, 1)[0]
	rare := search.Filter{Tags: []string{"rare"}}

	ran := func(st *Store, f search.Filter) (int, []int64) {
		h := &annRan{}
		withViewHook(t, h)
		got := docsOf(semantic(t, st, model, q, f, 10))
		viewHook = nil
		return h.cand, got
	}
	want := docsOf(semantic(t, exact, model, q, rare, 10))
	scoped, underfill := built.scopedOf(model).Load(), built.underfillOf(model).Load()
	if cand, got := ran(at, rare); cand != 0 || !slices.Equal(got, want) {
		t.Fatalf("200 documents at a bound of 200: %d candidate stages, %v; want none and the exact answer %v", cand, got, want)
	}
	if d, u := built.scopedOf(model).Load()-scoped, built.underfillOf(model).Load()-underfill; d != 1 || u != 0 {
		t.Fatalf("the small-scope count rose by %d and the underfill count by %d; want 1 and 0", d, u)
	}
	sts, err := built.Status(context.Background())
	if err != nil || len(sts) != 1 || sts[0].ExactScoped != 1 {
		t.Fatalf("the status: %+v %v; want exact_scoped 1", sts, err)
	}
	if cand, _ := ran(past, rare); cand != 1 {
		t.Fatalf("200 documents at a bound of 199: %d candidate stages; want the index tried", cand)
	}
	if cand, _ := ran(at, search.Filter{Tags: []string{"tenth"}}); cand != 1 {
		t.Fatalf("2,000 documents at a bound of 200: %d candidate stages; want the index tried", cand)
	}
	if cand, _ := ran(at, search.Filter{}); cand != 1 || built.scopedOf(model).Load()-scoped != 1 {
		t.Fatalf("no filter: %d candidate stages, the small-scope count at %d; want the index, and no count", cand, built.scopedOf(model).Load()-scoped)
	}
}

// sqlSeen keeps every statement a View runs.
type sqlSeen struct {
	dao.NopHook
	sql []string
}

func (h *sqlSeen) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	h.sql = append(h.sql, q.SQL)
	return nil
}

// A scoped read's facet and tag predicates are the array operators the GIN indexes on facets and
// tags answer (overlap and containment), never the membership form (value = ANY(column)), which no
// index answers: with it every scoped search scanned all of the tenant's documents.
func TestScopePredicatesAreTheIndexesOwn(t *testing.T) {
	access, err := accessPredicates(map[string][]string{"label_id": {"A", "B"}, "territory": {"US"}})
	if err != nil {
		t.Fatal(err)
	}
	preds := append(filters(search.Filter{Tags: []string{"x", "y"}, Facets: map[string][]string{"kind": {"k1", "k2"}, "none": nil}}), access...)
	var sqls []string
	var args []any
	next := 0
	for _, p := range preds {
		s, a := p.ToSQL(postgres.PostgresDialect{}, &next)
		sqls, args = append(sqls, s), append(args, a...)
	}
	want := []string{"rag_document.tags @> $1", "rag_document.facets && $2", "rag_document.facets && $3", "rag_document.facets && $4"}
	if !slices.Equal(sqls, want) {
		t.Fatalf("the predicates: %q; want %q", sqls, want)
	}
	wantArgs := [][]string{{"x", "y"}, {"kind=k1", "kind=k2"}, {"label_id=A", "label_id=B"}, {"territory=US"}}
	if len(args) != len(wantArgs) {
		t.Fatalf("%d arguments; want %d", len(args), len(wantArgs))
	}
	for i, a := range args {
		if got, _ := a.([]string); !slices.Equal(got, wantArgs[i]) {
			t.Fatalf("argument %d: %v; want %v", i, a, wantArgs[i])
		}
	}
	if strings.Contains(strings.Join(sqls, " "), "ANY(") {
		t.Fatal("a scope predicate in the membership form")
	}
}

// planOps keeps the settings statements a View sends around its own.
type planOps struct {
	dao.NopHook
	sets []dao.QueryInfo
}

func (h *planOps) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	if q.Op == dao.OpLocalSettings {
		h.sets = append(h.sets, *q)
	}
	return nil
}

// A scoped View plans each statement for its own arguments: every retrieval is sent with
// plan_cache_mode = force_custom_plan, for that statement alone. A View with no scope sends no
// setting, and costs no round trip more than it did.
func TestAScopedViewPlansForItsArguments(t *testing.T) {
	ctx := context.Background()
	s := openCorpus(t, scratch(t), "t", accessDocs)
	run := func(view func(func(*View) error) error) []dao.QueryInfo {
		t.Helper()
		h := &planOps{}
		withViewHook(t, h)
		err := view(func(v *View) error {
			if _, err := v.Lexical(ctx, []search.Term{{Text: "common"}}, search.Filter{}, 10); err != nil {
				return err
			}
			_, err := v.List(ctx, search.Filter{}, 10)
			return err
		})
		viewHook = nil
		if err != nil {
			t.Fatal(err)
		}
		return h.sets
	}
	scoped := run(func(fn func(*View) error) error { return s.ViewWithin(ctx, map[string][]string{"label_id": {"A"}}, fn) })
	if len(scoped) < 2 { // a retrieval may read more than once (the tenant's models, then its rows)
		t.Fatalf("a scoped View's two retrievals sent %d settings statements; want one for each statement", len(scoped))
	}
	for _, q := range scoped {
		if !strings.Contains(q.SQL, "set_config") || !slices.Contains(q.Args, any("plan_cache_mode")) || !slices.Contains(q.Args, any("force_custom_plan")) {
			t.Errorf("a scoped retrieval's settings: %s %v; want plan_cache_mode = force_custom_plan", q.SQL, q.Args)
		}
	}
	if open := run(func(fn func(*View) error) error { return s.View(ctx, fn) }); len(open) != 0 {
		t.Errorf("a View with no scope sent %d settings statements; want none", len(open))
	}
}

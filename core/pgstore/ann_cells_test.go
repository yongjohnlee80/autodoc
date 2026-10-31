package pgstore

import (
	"context"
	"database/sql/driver"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"
)

// AP-7's acceptance cells (approximate semantic search through per-model HNSW indexes), against
// the PostgreSQL with pgvector that AUTODOC_TEST_PGURL names. Each cell has a scratch database of
// its own. The corpora are made in SQL, one chunk per document, clustered vectors, so a 100,000
// embedding tenant takes seconds; every search is checked against the exact answer the same store
// gives without its ANN.

const annDims = 16

func annModel(dims int) string { return fmt.Sprintf("test|ann-model||%d", dims) }

// annCorpus writes n ready documents for tenant under model, one chunk and one embedding each,
// clustered round 40 centers (seeded, so a run is repeatable); every 100th document is tagged
// rare, and every 10th tenth. The tenant's active model is model.
func annCorpus(t testing.TB, conn dao.DataConn, tenant, model string, n, dims int, seed float64) {
	t.Helper()
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	vec := `('[' || array_to_string(ARRAY(SELECT c.v[j] + (random() - 0.5) * 0.3 FROM generate_series(1, ` + fmt.Sprint(dims) + `) j
	          WHERE k.doc > 0), ',') || ']')::vector`
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`SELECT setseed($1)`, []any{seed}},
		{`CREATE TEMPORARY TABLE IF NOT EXISTS ann_centers (i int PRIMARY KEY, v real[])`, nil},
		{`TRUNCATE ann_centers`, nil},
		{`INSERT INTO ann_centers SELECT i, ARRAY(SELECT (random() - 0.5)::real FROM generate_series(1, $1) WHERE i >= 0) FROM generate_series(0, 39) i`, []any{dims}},
		{`INSERT INTO rag_document (tenant, path, tags, semantic_ready)
		  SELECT $1, 'doc/' || lpad(g::text, 8, '0'), CASE WHEN g % 100 = 0 THEN ARRAY['rare', 'tenth'] WHEN g % 10 = 0 THEN ARRAY['tenth'] ELSE '{}'::text[] END, true
		    FROM generate_series(1, $2) g`, []any{tenant, n}},
		{`ANALYZE rag_document`, nil},
		{`INSERT INTO rag_chunk (tenant, doc, ord, body, text_hash)
		  SELECT tenant, id, 0, 'body ' || path, sha256(convert_to(tenant || path, 'UTF8')) FROM rag_document WHERE tenant = $1`, []any{tenant}},
		{`ANALYZE rag_chunk`, nil}, {`ANALYZE ann_centers`, nil},
		// one embedding per chunk, straight from the chunk row: no join a fresh table's missing
		// statistics could make a nested loop of
		{`INSERT INTO rag_embedding (tenant, model, text_hash, vec)
		  SELECT k.tenant, $2, k.text_hash, ` + vec + `
		    FROM rag_chunk k JOIN ann_centers c ON c.i = k.doc % 40
		   WHERE k.tenant = $1`, []any{tenant, model}},
		{`INSERT INTO rag_meta (tenant, active_model) VALUES ($1, $2)
		  ON CONFLICT (tenant) DO UPDATE SET active_model = EXCLUDED.active_model`, []any{tenant, model}},
		{`ANALYZE rag_document`, nil}, {`ANALYZE rag_chunk`, nil}, {`ANALYZE rag_embedding`, nil},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("corpus %q: %v", strings.Fields(q.sql)[0], err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// queryVecs are n query vectors near existing embeddings of tenant: one read back and moved a
// little, so a query lands in a cluster as a real one would.
func queryVecs(t testing.TB, conn dao.DataConn, tenant, model string, n int) [][]float32 {
	t.Helper()
	st, err := New(conn, tenant)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.rw.emb.DAO().With(eTenant, tenant).With(eModel, model).Limit(uint64(n)).Select(eVec)
	if err != nil || len(rows) < n {
		t.Fatalf("query vectors: %d %v", len(rows), err)
	}
	rng := rand.New(rand.NewPCG(uint64(n), uint64(len(tenant)))) // seeded: a run is repeatable
	out := make([][]float32, n)
	for i, r := range rows {
		v := make([]float32, len(r.Vec))
		for j, x := range r.Vec {
			v[j] = x + (rng.Float32()-0.5)*0.1
		}
		out[i] = v
	}
	return out
}

// annSetup is a scratch database with the ANN at threshold, and a function that builds model's
// index (Want, then a Tick).
func annSetup(t *testing.T, cfg ANNConfig) (dao.DataConn, *ANN) {
	t.Helper()
	conn := scratch(t)
	return conn, NewANN(conn, cfg, "cellrun")
}

// rebuildFor drops model's index and builds it again on the same corpus, at a's config: a fresh
// build, checked to be a new index (another relation) at the declared ef_construction, whose
// relation id it answers.
func rebuildFor(t *testing.T, conn dao.DataConn, a *ANN, model string) int64 {
	t.Helper()
	ctx := context.Background()
	before := annIndexOID(t, conn, model)
	p, err := postgres.PinSessionConn(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	err = postgres.DropIndexConcurrently(ctx, p, "", annIndexName(model), postgres.BuildOptions{})
	p.Discard()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Tick(ctx); err != nil { // the ready model's index is gone: wanted, claimed and built
		t.Fatalf("the rebuild: %v", err)
	}
	st, err := postgres.ReadIndexState(ctx, conn, "", annIndexName(model))
	if err != nil {
		t.Fatal(err)
	}
	if after := annIndexOID(t, conn, model); after == 0 || after == before || !st.Valid ||
		st.Def.Options != fmt.Sprintf("ef_construction=%d", a.cfg.EfConstruction) || indexOf(t, a, model).State != annReady {
		t.Fatalf("the rebuild: index %d (was %d), %+v, %s; want a new valid index, ef_construction=%d, ready",
			after, before, st, indexOf(t, a, model).State, a.cfg.EfConstruction)
	}
	return annIndexOID(t, conn, model)
}

// annIndexOID is model's index's relation id, 0 when there is none.
func annIndexOID(t *testing.T, conn dao.DataConn, model string) int64 {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), "SELECT oid::bigint FROM pg_class WHERE relname = $1 AND relkind = 'i'", annIndexName(model))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var oid int64
	if rows.Next() {
		if err := rows.Scan(&oid); err != nil {
			t.Fatal(err)
		}
	}
	return oid
}

func buildFor(t *testing.T, a *ANN, model string) {
	t.Helper()
	ctx := context.Background()
	if err := a.Want(ctx, model); err != nil {
		t.Fatal(err)
	}
	if err := a.Tick(ctx); err != nil {
		t.Fatalf("the build: %v", err)
	}
	st, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if s.Model == model && s.Mode != ModeANN {
			t.Fatalf("after the build %s is %s (%s)", model, s.Mode, s.Reason)
		}
	}
}

// semantic is tenant's top n for vec through st.
func semantic(t testing.TB, st *Store, model string, vec []float32, f search.Filter, n int) []search.Candidate[int64] {
	t.Helper()
	var out []search.Candidate[int64]
	if err := st.View(context.Background(), func(v *View) error {
		var err error
		out, err = v.Semantic(context.Background(), model, vec, f, n)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func docsOf(cs []search.Candidate[int64]) []int64 {
	out := make([]int64, len(cs))
	for i, c := range cs {
		out[i] = c.Doc
	}
	return out
}

// recall is |a ∩ exact| / |exact|.
func recall(a, exact []int64) float64 {
	if len(exact) == 0 {
		return 1
	}
	in := map[int64]bool{}
	for _, d := range a {
		in[d] = true
	}
	hit := 0
	for _, d := range exact {
		if in[d] {
			hit++
		}
	}
	return float64(hit) / float64(len(exact))
}

// annRan counts the candidate statements a View sends (viewHook): the ORDER BY over the cast.
type annRan struct {
	dao.NopHook
	cand, rerank int
	sql          []string
	args         [][]any
}

func (h *annRan) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	switch {
	case strings.Contains(q.SQL, "::vector("+fmt.Sprint(annDims)+")") || strings.Contains(q.SQL, "::halfvec("):
		h.cand++
		h.sql, h.args = append(h.sql, q.SQL), append(h.args, slices.Clone(q.Args))
	case strings.Contains(q.SQL, "unnest("):
		h.rerank++
	}
	return nil
}

func withViewHook(t *testing.T, h dao.Hook) {
	t.Helper()
	viewHook = h
	t.Cleanup(func() { viewHook = nil })
}

// Cell 1: recall@n ≥ 0.95 at n = 10 and 50 against the exact answer, for a single tenant of
// 100,000 clustered embeddings, for skewed tenants sharing the model's index (90% and 5% of it,
// both past the threshold), and under a 1% filter (which mostly falls back: the answer is then
// exact, and the cell says how many did). Every search is checked to have run the candidate
// stage, not fallen back unseen. pgvector draws each node's level at random, so one build's
// recall is not the next's: the skewed tenant's is pinned over three builds of the index at the
// declared ef_construction, the first one the table's, the others fresh builds on the same corpus
// (the index dropped and built again), each checked to be a new index.
func TestANN_Recall(t *testing.T) {
	if testing.Short() {
		t.Skip("the recall corpora are large")
	}
	cfg := DefaultANNConfig()
	cfg.Threshold = 1000
	conn, a := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "big", model, 100000, annDims, 0.1)
	annCorpus(t, conn, "skew90", model, 90000, annDims, 0.2)
	annCorpus(t, conn, "skew5", model, 5000, annDims, 0.3)
	buildFor(t, a, model)
	skew90 := func(t *testing.T, exact, approx *Store, label string) {
		for _, n := range []int{10, 50} {
			t.Run(fmt.Sprintf("%s/n=%d", label, n), func(t *testing.T) {
				h := &annRan{}
				withViewHook(t, h)
				sum, qs := 0.0, queryVecs(t, conn, "skew90", model, 50)
				for _, q := range qs {
					sum += recall(docsOf(semantic(t, approx, model, q, search.Filter{}, n)), docsOf(semantic(t, exact, model, q, search.Filter{}, n)))
				}
				if h.cand != len(qs) {
					t.Fatalf("%d candidate stages ran for %d searches", h.cand, len(qs))
				}
				r := sum / float64(len(qs))
				t.Logf("%s: recall@%d %.4f", label, n, r)
				if r < 0.95 {
					t.Fatalf("%s: recall@%d is %.4f, below 0.95", label, n, r)
				}
			})
		}
	}
	rebuilds := func() { // after the table: two more builds, the skewed tenant's recall in each
		exact, err := New(conn, "skew90")
		if err != nil {
			t.Fatal(err)
		}
		approx, err := New(conn, "skew90", WithANN(a))
		if err != nil {
			t.Fatal(err)
		}
		builds := []int64{annIndexOID(t, conn, model)}
		for b := 1; b < 3; b++ {
			builds = append(builds, rebuildFor(t, conn, a, model))
			skew90(t, exact, approx, fmt.Sprintf("skew90/build%d", b))
		}
		if slices.Sort(builds); len(slices.Compact(builds)) != 3 {
			t.Fatalf("the skewed tenant's recall was checked on indexes %v, want three builds", builds)
		}
	}
	for _, tc := range []struct {
		tenant string
		f      search.Filter
	}{
		{"big", search.Filter{}},
		{"skew90", search.Filter{}},
		{"skew5", search.Filter{}},
		{"big", search.Filter{Tags: []string{"rare"}}},
	} {
		exact, err := New(conn, tc.tenant)
		if err != nil {
			t.Fatal(err)
		}
		approx, err := New(conn, tc.tenant, WithANN(a))
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range []int{10, 50} {
			t.Run(fmt.Sprintf("%s/tags=%v/n=%d", tc.tenant, tc.f.Tags, n), func(t *testing.T) {
				h := &annRan{}
				withViewHook(t, h)
				before := a.underfillOf(model).Load()
				sum, qs := 0.0, queryVecs(t, conn, tc.tenant, model, 50)
				for _, q := range qs {
					want := docsOf(semantic(t, exact, model, q, tc.f, n))
					got := docsOf(semantic(t, approx, model, q, tc.f, n))
					if len(got) != len(want) {
						t.Fatalf("%d hits; the exact answer has %d", len(got), len(want))
					}
					sum += recall(got, want)
				}
				under := a.underfillOf(model).Load() - before
				if h.cand != len(qs) {
					t.Fatalf("%d candidate stages ran for %d searches", h.cand, len(qs))
				}
				r := sum / float64(len(qs))
				t.Logf("recall@%d %.4f over %d queries (%d answered exactly after an underfill)", n, r, len(qs), under)
				if r < 0.95 {
					t.Fatalf("recall@%d is %.4f, below 0.95", n, r)
				}
			})
		}
	}
	rebuilds()
}

// Cell 2: with the scan budgets lowered until the scan exhausts them under a 1% filter, the search
// still answers n hits, the exact answer, and the underfill count rises.
func TestANN_FillAndFallback(t *testing.T) {
	cfg := DefaultANNConfig()
	cfg.Threshold, cfg.MaxScanTuples = 1000, 200
	conn, built := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 20000, annDims, 0.4)
	buildFor(t, built, model)
	// The planner answers a 1% tag filter filter-first, with an exact sort, where no budget can
	// bite. With sorting off for this database's new sessions only the HNSW scan gives the order,
	// so it drives the candidate stage (as it does when a filter's selectivity is misjudged), and
	// its budget is what runs out. The exact answer comes from the unsteered pool.
	steered := freshPool(t, conn, "enable_sort = off", "enable_incremental_sort = off")
	a := NewANN(steered, cfg, "fill")
	exact, _ := New(conn, "t")
	approx, _ := New(steered, "t", WithANN(a))
	f := search.Filter{Tags: []string{"rare"}}
	before := a.underfillOf(model).Load()
	for _, q := range queryVecs(t, conn, "t", model, 5) {
		want := docsOf(semantic(t, exact, model, q, f, 10))
		got := docsOf(semantic(t, approx, model, q, f, 10))
		if len(got) != 10 || !slices.Equal(got, want) {
			t.Fatalf("under exhausted budgets: %v; want the exact answer %v", got, want)
		}
	}
	if d := a.underfillOf(model).Load() - before; d != 5 {
		t.Fatalf("underfill rose by %d; want 5", d)
	}

	// below the threshold the search is exact: no candidate stage at all
	annCorpus(t, conn, "small", model, 500, annDims, 0.41)
	small, _ := New(conn, "small", WithANN(built))
	h := &annRan{}
	withViewHook(t, h)
	semantic(t, small, model, queryVecs(t, conn, "small", model, 1)[0], search.Filter{}, 10)
	if h.cand != 0 {
		t.Fatal("a tenant below the threshold ran the candidate stage")
	}
	if mode, reason, err := small.ANNMode(context.Background(), model); err != nil || mode != ModeExact || !strings.Contains(reason, "fewer than") {
		t.Fatalf("below the threshold: %s %q %v", mode, reason, err)
	}
}

// explainLocal is the plan of sql with args under the candidate stage's own settings, set locally
// in one transaction, as dao.WithLocalSettings sends them.
func explainLocal(t *testing.T, conn dao.DataConn, a *ANN, k int, sql string, args []any, analyze bool) string {
	t.Helper()
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT set_config('plan_cache_mode', 'force_custom_plan', true),
		set_config('hnsw.iterative_scan', 'relaxed_order', true), set_config('hnsw.ef_search', $1, true),
		set_config('hnsw.max_scan_tuples', $2, true), set_config('hnsw.scan_mem_multiplier', $3, true)`,
		fmt.Sprint(min(max(k, 100), 1000)), fmt.Sprint(a.cfg.MaxScanTuples), fmt.Sprint(a.cfg.ScanMemMultiplier)); err != nil {
		t.Fatal(err)
	}
	return planOf(t, tx, sql, args, analyze)
}

func planOf(t *testing.T, q dao.Querier, sql string, args []any, analyze bool) string {
	t.Helper()
	opt := "COSTS OFF"
	if analyze {
		opt = "ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF"
	}
	rows, err := q.QueryContext(context.Background(), "EXPLAIN ("+opt+") "+sql, args...)
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// Cell 3: the candidate stage's plan is the HNSW index scan driving a nested loop; the exact path
// shows no HNSW scan.
func TestANN_Plan(t *testing.T) {
	cfg := DefaultANNConfig()
	cfg.Threshold = 1000
	conn, a := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 30000, annDims, 0.5)
	buildFor(t, a, model)
	approx, _ := New(conn, "t", WithANN(a))
	h := &annRan{}
	withViewHook(t, h)
	q := queryVecs(t, conn, "t", model, 1)[0]
	semantic(t, approx, model, q, search.Filter{}, 10)
	if len(h.sql) != 1 {
		t.Fatalf("%d candidate statements", len(h.sql))
	}
	plan := explainLocal(t, conn, a, candidateCount(10), h.sql[0], h.args[0], false)
	name := annIndexName(model)
	if !strings.Contains(plan, "Index Scan using "+name) || !strings.Contains(plan, "Nested Loop") {
		t.Fatalf("the candidate stage's plan doesn't drive a nested loop from the HNSW scan:\n%s", plan)
	}
	// the innermost nested loop's first child, its outer side, is the HNSW scan
	lines := strings.Split(plan, "\n")
	last := -1
	for i, l := range lines {
		if strings.Contains(l, "Nested Loop") {
			last = i
		}
	}
	for _, l := range lines[last+1:] {
		if strings.Contains(l, "->") {
			if !strings.Contains(l, name) {
				t.Errorf("the innermost nested loop's outer side is not the HNSW scan:\n%s", plan)
			}
			break
		}
	}
	exact, _ := New(conn, "t")
	var sql string
	var args []any
	viewHook = &capture{f: func(s string, a []any) { sql, args = s, a }}
	semantic(t, exact, model, q, search.Filter{}, 10)
	if p := planOf(t, conn, sql, args, false); strings.Contains(p, name) {
		t.Fatalf("the exact path uses the HNSW index:\n%s", p)
	}
}

// capture is a viewHook that keeps the last statement.
type capture struct {
	dao.NopHook
	f func(string, []any)
}

func (c *capture) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	c.f(q.SQL, slices.Clone(q.Args))
	return nil
}

// Cell 4: with the session's baseline at force_generic_plan, the candidate
// statement prepared on one connection and executed 10 times uses the partial index every time
// under its local force_custom_plan; without that override, no execution does.
func TestANN_PreparedExecution(t *testing.T) {
	cfg := DefaultANNConfig()
	cfg.Threshold = 1000
	conn, a := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 20000, annDims, 0.6)
	buildFor(t, a, model)
	approx, _ := New(conn, "t", WithANN(a))
	h := &annRan{}
	withViewHook(t, h)
	semantic(t, approx, model, queryVecs(t, conn, "t", model, 1)[0], search.Filter{}, 10)
	sql, args := h.sql[0], h.args[0]
	p, err := postgres.PinSessionConn(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Discard()
	ctx := context.Background()
	stmt := sql
	for _, override := range []bool{true, false} {
		tx, err := p.BeginSessionTx(ctx, dao.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT set_config('plan_cache_mode', 'force_generic_plan', true)"); err != nil {
			t.Fatal(err)
		}
		// a PREPARE outlives its transaction: each pass prepares its own
		name := fmt.Sprintf("ann_cand_%v", override)
		if _, err := tx.ExecContext(ctx, "PREPARE "+name+" AS "+stmt); err != nil {
			t.Fatalf("PREPARE: %v", err)
		}
		exec := "EXECUTE " + name + "(" + literals(t, args) + ")"
		for i := 0; i < 10; i++ {
			if override {
				if _, err := tx.ExecContext(ctx, "SELECT set_config('plan_cache_mode', 'force_custom_plan', true), set_config('hnsw.iterative_scan', 'relaxed_order', true)"); err != nil {
					t.Fatal(err)
				}
			}
			plan := planOf(t, tx, exec, nil, true)
			uses := strings.Contains(plan, annIndexName(model))
			if override && !uses {
				t.Fatalf("execution %d under the local override uses no HNSW index:\n%s", i+1, plan)
			}
			if !override && uses {
				t.Fatalf("execution %d without the override uses the partial index, so the cell can't tell:\n%s", i+1, plan)
			}
			if override {
				if _, err := tx.ExecContext(ctx, "SELECT set_config('plan_cache_mode', 'force_generic_plan', true)"); err != nil {
					t.Fatal(err)
				}
			}
		}
		_ = tx.Rollback()
	}
}

// literals are args as SQL literals, for EXECUTE's parameter list (EXPLAIN EXECUTE takes no bound
// parameters of its own).
func literals(t *testing.T, args []any) string {
	t.Helper()
	ps := make([]string, len(args))
	for i, a := range args {
		if v, ok := a.(driver.Valuer); ok {
			x, err := v.Value()
			if err != nil {
				t.Fatal(err)
			}
			a = x
		}
		switch x := a.(type) {
		case string:
			ps[i] = "'" + strings.ReplaceAll(x, "'", "''") + "'"
		case bool, int, int32, int64:
			ps[i] = fmt.Sprint(x)
		default:
			t.Fatalf("a candidate statement argument of type %T", a)
		}
	}
	return strings.Join(ps, ", ")
}

// freshPool is a new pool on conn's database, opened after settings were made the database's
// defaults (ALTER DATABASE … SET), so its sessions start with them.
func freshPool(t *testing.T, conn dao.DataConn, settings ...string) dao.DataConn {
	t.Helper()
	ctx := context.Background()
	var db string
	rows, err := conn.QueryContext(ctx, "SELECT current_database()")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		_ = rows.Scan(&db)
	}
	rows.Close()
	for _, s := range settings {
		if _, err := conn.ExecContext(ctx, "ALTER DATABASE "+db+" SET "+s); err != nil {
			t.Fatal(err)
		}
	}
	p, err := postgres.OpenNamed(ctx, db+"_fresh", dsnOf(t, conn, db))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// Cell 6: a 3,000-dimension model's search goes through its halfvec index, and its hits are
// in the exact full-precision order over the candidates; a 5,000-dimension model stays exact, and
// says why.
func TestANN_Dimensions(t *testing.T) {
	cfg := DefaultANNConfig()
	cfg.Threshold = 100
	conn, a := annSetup(t, cfg)
	model := annModel(3000)
	annCorpus(t, conn, "t", model, 600, 3000, 0.7)
	buildFor(t, a, model)
	approx, _ := New(conn, "t", WithANN(a))
	exact, _ := New(conn, "t")
	h := &annRan{}
	withViewHook(t, h)
	q := queryVecs(t, conn, "t", model, 1)[0]
	got := semantic(t, approx, model, q, search.Filter{}, 10)
	if h.cand != 1 || !strings.Contains(h.sql[0], "::halfvec(3000)") {
		t.Fatalf("the 3,000-dimension search didn't run its halfvec candidate stage: %d %q", h.cand, h.sql)
	}
	// the rerank is exact: the hits, in order, are the exact order of the hits themselves
	want := semantic(t, exact, model, q, search.Filter{}, 600)
	rank := map[int64]int{}
	for i, c := range want {
		rank[c.Doc] = i
	}
	for i := 1; i < len(got); i++ {
		if rank[got[i-1].Doc] > rank[got[i].Doc] {
			t.Fatalf("hits %d and %d are out of the exact order", i-1, i)
		}
	}

	big := annModel(5000)
	if err := a.Want(context.Background(), big); err != nil {
		t.Fatal(err)
	}
	mode, reason, err := approx.ANNMode(context.Background(), big)
	if err != nil || mode != ModeExact || !strings.Contains(reason, "4,000") {
		t.Fatalf("a 5,000-dimension model: %s %q %v", mode, reason, err)
	}
}

// Cell 7: hits at an equal distance come back in path, then ordinal order.
func TestANN_Ties(t *testing.T) {
	cfg := DefaultANNConfig()
	cfg.Threshold = 100
	conn, a := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 2000, annDims, 0.8)
	ctx := context.Background()
	// 30 documents share one vector: a tie group
	if _, err := conn.ExecContext(ctx, `UPDATE rag_embedding e SET vec = (SELECT vec FROM rag_embedding ORDER BY text_hash LIMIT 1)
		FROM rag_chunk c WHERE c.tenant = e.tenant AND c.text_hash = e.text_hash AND c.doc % 50 = 7`); err != nil {
		t.Fatal(err)
	}
	buildFor(t, a, model)
	approx, _ := New(conn, "t", WithANN(a))
	st, _ := New(conn, "t")
	c, err := st.rw.chunk.DAO().With(cTenant, "t").With(cDoc, 7).Get(cHash)
	if err != nil {
		t.Fatal(err)
	}
	e, err := st.rw.emb.DAO().With(eTenant, "t").With(eHash, c.TextHash).Get(eVec)
	if err != nil {
		t.Fatal(err)
	}
	got := semantic(t, approx, model, e.Vec, search.Filter{}, 20)
	for i := 1; i < len(got); i++ {
		if got[i-1].Path > got[i].Path {
			t.Fatalf("tied hits %d and %d are out of path order: %s, %s", i-1, i, got[i-1].Path, got[i].Path)
		}
	}
}

// Cell 8: the two stages read one snapshot: a candidate's document deleted and committed between
// them is still in the answer.
func TestANN_OneSnapshot(t *testing.T) {
	cfg := DefaultANNConfig()
	cfg.Threshold = 100
	conn, a := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 3000, annDims, 0.9)
	buildFor(t, a, model)
	approx, _ := New(conn, "t", WithANN(a))
	q := queryVecs(t, conn, "t", model, 1)[0]
	first := semantic(t, approx, model, q, search.Filter{}, 10)
	var deleted bool
	viewHook = &afterCandidates{f: func() {
		if !deleted {
			deleted = true
			if _, err := conn.ExecContext(context.Background(), "DELETE FROM rag_document WHERE tenant = 't' AND id = $1", first[0].Doc); err != nil {
				t.Error(err)
			}
		}
	}}
	t.Cleanup(func() { viewHook = nil })
	got := semantic(t, approx, model, q, search.Filter{}, 10)
	if !deleted || len(got) == 0 || got[0].Doc != first[0].Doc {
		t.Fatalf("the rerank didn't read the candidate stage's snapshot: deleted %v, %v, want %d first", deleted, docsOf(got), first[0].Doc)
	}
	if after := semantic(t, approx, model, q, search.Filter{}, 10); slices.Contains(docsOf(after), first[0].Doc) {
		t.Fatal("the deleted document is still found by a later search")
	}
}

// afterCandidates runs f after the candidate stage's statement, before the rerank.
type afterCandidates struct {
	dao.NopHook
	f func()
}

func (h *afterCandidates) AfterExec(_ context.Context, q *dao.QueryInfo, out dao.Outcome) error {
	if strings.Contains(q.SQL, "::vector(") && q.Op == dao.OpSelect {
		h.f()
	}
	return out.Err
}

// Cell 4, through the code's own path: on a database whose sessions start at
// plan_cache_mode = force_generic_plan, ten searches on one pooled connection (pgx caches the
// candidate statement, so it is prepared and re-executed) each scan the model's HNSW index, as
// pg_stat_user_indexes counts them: the candidate stage's local force_custom_plan is what lets a
// prepared plan prove the partial index's predicate.
func TestANN_AGenericBaselineStillUsesTheIndex(t *testing.T) {
	cfg := DefaultANNConfig()
	cfg.Threshold = 1000
	conn, a := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 20000, annDims, 0.66)
	buildFor(t, a, model)
	ctx := context.Background()
	var db string
	rows, err := conn.QueryContext(ctx, "SELECT current_database()")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		_ = rows.Scan(&db)
	}
	rows.Close()
	if _, err := conn.ExecContext(ctx, "ALTER DATABASE "+db+" SET plan_cache_mode = 'force_generic_plan'"); err != nil {
		t.Fatal(err)
	}
	one, err := postgres.OpenNamed(ctx, db+"_one", dsnOf(t, conn, db), postgres.MaxOpenConns(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = one.Close() })
	var mode string
	if r, err := one.QueryContext(ctx, "SHOW plan_cache_mode"); err == nil {
		if r.Next() {
			_ = r.Scan(&mode)
		}
		r.Close()
	}
	if mode != "force_generic_plan" {
		t.Fatalf("the session's baseline is %q; the cell needs force_generic_plan", mode)
	}
	scans := func() int64 {
		var n int64
		r, err := conn.QueryContext(ctx, "SELECT idx_scan FROM pg_stat_user_indexes WHERE indexrelname = $1", annIndexName(model))
		if err == nil {
			if r.Next() {
				_ = r.Scan(&n)
			}
			r.Close()
		}
		return n
	}
	before := scans()
	approx, _ := New(one, "t", WithANN(NewANN(one, cfg, "generic")))
	qs := queryVecs(t, conn, "t", model, 10)
	for _, q := range qs {
		semantic(t, approx, model, q, search.Filter{}, 10)
	}
	until(t, "the index's scan count to reach ten more", func() bool { return scans()-before >= int64(len(qs)) })
}

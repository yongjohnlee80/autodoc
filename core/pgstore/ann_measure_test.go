package pgstore

import (
	"context"
	"errors"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/search"
)

// TestANN_Measure is AP-7's measurements, for the PR: at each size in
// AUTODOC_ANN_MEASURE (embeddings, comma-free, e.g. 100000 or 1000000), the index's build time
// and size, the insert throughput with the index in place, and query p50/p99 exact against ann,
// with the fallback rate, per query shape. It runs only when that variable is set: it measures,
// it doesn't assert beyond the answers being whole.
func TestANN_Measure(t *testing.T) {
	size, _ := strconv.Atoi(os.Getenv("AUTODOC_ANN_MEASURE"))
	if size <= 0 {
		t.Skip("AUTODOC_ANN_MEASURE is not set: the measurements are a separate run")
	}
	cfg := DefaultANNConfig()
	conn, a := annSetup(t, cfg)
	model := annModel(annDims)
	start := time.Now()
	annCorpus(t, conn, "t", model, size, annDims, 0.5)
	t.Logf("size %d: corpus in %s", size, time.Since(start).Round(time.Millisecond))
	ctx := context.Background()
	if err := a.Want(ctx, model); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("size %d: index build %s", size, time.Since(start).Round(time.Millisecond))
	rows, err := conn.QueryContext(ctx, "SELECT pg_relation_size($1::regclass), pg_relation_size('rag_embedding')", annIndexName(model))
	if err != nil {
		t.Fatal(err)
	}
	var idx, table int64
	if rows.Next() {
		_ = rows.Scan(&idx, &table)
	}
	rows.Close()
	t.Logf("size %d: index %d MB, rag_embedding %d MB", size, idx>>20, table>>20)

	// inserts with the index in place: 10,000 more embeddings, by the corpus's own statements
	start = time.Now()
	annCorpus(t, conn, "insert", model, 10000, annDims, 0.6)
	el := time.Since(start)
	t.Logf("size %d: 10,000 documents with their chunks and embeddings in %s (%.0f/s) with the index in place", size, el.Round(time.Millisecond), 10000/el.Seconds())

	exact, _ := New(conn, "t")
	approx, _ := New(conn, "t", WithANN(a))
	qs := queryVecs(t, conn, "t", model, 100)
	for _, shape := range []struct {
		name string
		f    search.Filter
	}{{"unfiltered", search.Filter{}}, {"tag 1%", search.Filter{Tags: []string{"rare"}}}} {
		for _, n := range []int{10, 50} {
			var te, ta []time.Duration
			before := a.underfillOf(model).Load()
			sum := 0.0
			for _, q := range qs {
				s := time.Now()
				want := docsOf(semantic(t, exact, model, q, shape.f, n))
				te = append(te, time.Since(s))
				s = time.Now()
				got := docsOf(semantic(t, approx, model, q, shape.f, n))
				ta = append(ta, time.Since(s))
				sum += recall(got, want)
			}
			under := a.underfillOf(model).Load() - before
			t.Logf("size %d, %s, n=%d: exact p50 %s p99 %s; ann p50 %s p99 %s; recall %.4f; fallback %d/%d",
				size, shape.name, n, pct(te, 50), pct(te, 99), pct(ta, 50), pct(ta, 99), sum/float64(len(qs)), under, len(qs))
		}
	}
}

func pct(ds []time.Duration, p int) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[min(len(s)-1, len(s)*p/100)].Round(10 * time.Microsecond)
}

// TestANN_RecallSweep measures recall@10 and @50 against exact for skewed tenants sharing one
// model's index, at each hnsw.ef_search factor in AUTODOC_ANN_SWEEP (e.g. "1 2 3 4"): the
// evidence the default factor stands on. It runs only when that variable is set.
func TestANN_RecallSweep(t *testing.T) {
	factors := strings.Fields(os.Getenv("AUTODOC_ANN_SWEEP"))
	if len(factors) == 0 {
		t.Skip("AUTODOC_ANN_SWEEP is not set: the sweep is a separate run")
	}
	cfg := DefaultANNConfig()
	cfg.Threshold = 1000
	conn, build := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "big", model, 100000, annDims, 0.1)
	annCorpus(t, conn, "skew90", model, 90000, annDims, 0.2)
	annCorpus(t, conn, "skew5", model, 5000, annDims, 0.3)
	buildFor(t, build, model)
	for _, fs := range factors {
		f, _ := strconv.Atoi(fs)
		c := cfg
		c.EfFactor = f
		a := NewANN(conn, c, "sweep")
		for _, tenant := range []string{"big", "skew90", "skew5"} {
			exact, _ := New(conn, tenant)
			approx, _ := New(conn, tenant, WithANN(a))
			qs := queryVecs(t, conn, tenant, model, 100)
			for _, n := range []int{10, 50} {
				sum, worst := 0.0, 1.0
				var took time.Duration
				for _, q := range qs {
					want := docsOf(semantic(t, exact, model, q, search.Filter{}, n))
					s := time.Now()
					got := docsOf(semantic(t, approx, model, q, search.Filter{}, n))
					took += time.Since(s)
					r := recall(got, want)
					sum += r
					worst = min(worst, r)
					if r < 0.5 {
						// a tie group (equal distances) would show the same distances on both sides
						t.Logf("factor %d, %s, n=%d, a query at recall %.2f: exact distances %v; ann distances %v",
							f, tenant, n, r, distancesOf(t, exact, want, q), distancesOf(t, exact, got, q))
					}
				}
				t.Logf("factor %d, %s, n=%d: recall %.4f (worst query %.2f), ann mean %s", f, tenant, n, sum/float64(len(qs)), worst, (took / time.Duration(len(qs))).Round(10*time.Microsecond))
			}
		}
	}
}

// distancesOf is each document's cosine distance from q, read from its first chunk's embedding.
func distancesOf(t *testing.T, st *Store, docs []int64, q []float32) []string {
	t.Helper()
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		c, err := st.rw.chunk.DAO().With(cTenant, st.tenant).With(cDoc, d).With(cOrd, 0).Get(cHash)
		if err != nil {
			t.Fatal(err)
		}
		e, err := st.rw.emb.DAO().With(eTenant, st.tenant).With(eHash, c.TextHash).Get(eVec)
		if err != nil {
			t.Fatal(err)
		}
		var dot, na, nb float64
		for i := range q {
			dot += float64(q[i]) * float64(e.Vec[i])
			na += float64(q[i]) * float64(q[i])
			nb += float64(e.Vec[i]) * float64(e.Vec[i])
		}
		out = append(out, strconv.FormatFloat(1-dot/math.Sqrt(na*nb), 'f', 6, 64))
	}
	return out
}

// TestANN_BudgetSweep measures, at AUTODOC_ANN_MEASURE embeddings, each hnsw.max_scan_tuples in
// AUTODOC_ANN_BUDGETS (e.g. "40000 20000 10000 5000"): exact against ann p50 and p99, recall and
// the fallback rate over 50 queries, unfiltered and under a 10% and a 1% tag filter, at n=10. It is
// the evidence the budget's default stands on, and runs only when both variables are set.
func TestANN_BudgetSweep(t *testing.T) {
	size, _ := strconv.Atoi(os.Getenv("AUTODOC_ANN_MEASURE"))
	budgets := strings.Fields(os.Getenv("AUTODOC_ANN_BUDGETS"))
	if size <= 0 || len(budgets) == 0 {
		t.Skip("AUTODOC_ANN_MEASURE and AUTODOC_ANN_BUDGETS are not set: the sweep is a separate run")
	}
	cfg := DefaultANNConfig()
	conn, build := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, size, annDims, 0.5)
	buildFor(t, build, model)
	exact, _ := New(conn, "t")
	qs := queryVecs(t, conn, "t", model, 50)
	shapes := []struct {
		name string
		f    search.Filter
	}{{"unfiltered", search.Filter{}}, {"tag 10%", search.Filter{Tags: []string{"tenth"}}}, {"tag 1%", search.Filter{Tags: []string{"rare"}}}}
	// the exact answers and their times, once per shape
	type ex struct {
		want [][]int64
		took []time.Duration
	}
	exacts := make([]ex, len(shapes))
	for i, sh := range shapes {
		for _, q := range qs {
			s := time.Now()
			exacts[i].want = append(exacts[i].want, docsOf(semantic(t, exact, model, q, sh.f, 10)))
			exacts[i].took = append(exacts[i].took, time.Since(s))
		}
	}
	for _, bs := range budgets {
		b, _ := strconv.Atoi(bs)
		c := cfg
		c.MaxScanTuples = b
		a := NewANN(conn, c, "budget")
		approx, _ := New(conn, "t", WithANN(a))
		for i, sh := range shapes {
			var took []time.Duration
			sum := 0.0
			before := a.underfillOf(model).Load()
			for j, q := range qs {
				s := time.Now()
				got := docsOf(semantic(t, approx, model, q, sh.f, 10))
				took = append(took, time.Since(s))
				sum += recall(got, exacts[i].want[j])
			}
			ep, ap := pct(exacts[i].took, 50), pct(took, 50)
			t.Logf("budget %d, %s, n=10: exact p50 %s p99 %s; ann p50 %s p99 %s (%.2f× exact); recall %.4f; fallback %d/%d",
				b, sh.name, ep, pct(exacts[i].took, 99), ap, pct(took, 99), float64(ap)/float64(ep), sum/float64(len(qs)),
				a.underfillOf(model).Load()-before, len(qs))
		}
	}
}

// TestANN_BulkLoadMeasure is the measurement behind a model's first index waiting for a tenant's
// bulk load (docs/decisions/server.md#a-models-index-waits-for-its-bulk-load). At the size in
// AUTODOC_ANN_BULK (documents, one chunk and embedding each), a tenant imports its model as the
// server does: its documents first, not ready, then their embeddings in batches of 20,000, each
// batch marking its documents ready, the builder ticking after each. Once with the index built
// first (DeferBacklog 0), once deferred (the defaults): the load's time, the time until the index
// is ready, recall@10 against exact afterwards, whether a build fell back to serial, and the
// backlog count's plan at a full and a drained backlog. It runs only when that variable is set.
func TestANN_BulkLoadMeasure(t *testing.T) {
	size, _ := strconv.Atoi(os.Getenv("AUTODOC_ANN_BULK"))
	if size <= 0 {
		t.Skip("AUTODOC_ANN_BULK is not set: the measurements are a separate run")
	}
	for _, mode := range []struct {
		name    string
		backlog int
	}{{"index first", 0}, {"deferred", DefaultANNConfig().DeferBacklog}} {
		cfg := DefaultANNConfig()
		cfg.DeferBacklog = mode.backlog
		conn, a := annSetup(t, cfg)
		ctx := context.Background()
		model := annModel(annDims)
		bulkExec(t, conn, `INSERT INTO rag_meta (tenant, active_model) VALUES ('t', $1)`, model)
		if err := a.Want(ctx, model); err != nil {
			t.Fatal(err)
		}
		// the documents and their chunks first, none ready: the import's first pass
		start := time.Now()
		bulkExec(t, conn, `INSERT INTO rag_document (tenant, path, tags, semantic_ready)
			SELECT 't', 'doc/' || lpad(g::text, 8, '0'), '{}', false FROM generate_series(1, $1) g`, size)
		bulkExec(t, conn, `INSERT INTO rag_chunk (tenant, doc, ord, body, text_hash)
			SELECT tenant, id, 0, 'body ' || path, sha256(convert_to(tenant || path, 'UTF8')) FROM rag_document WHERE tenant = 't'`)
		bulkExec(t, conn, `CREATE TABLE IF NOT EXISTS bulk_centers (i int PRIMARY KEY, v real[])`)
		bulkExec(t, conn, `INSERT INTO bulk_centers SELECT i, ARRAY(SELECT (random() - 0.5)::real FROM generate_series(1, $1) WHERE i >= 0) FROM generate_series(0, 39) i ON CONFLICT DO NOTHING`, annDims)
		bulkExec(t, conn, `ANALYZE rag_document`)
		bulkExec(t, conn, `ANALYZE rag_chunk`)
		t.Logf("%s, %d: documents and chunks in %s", mode.name, size, time.Since(start).Round(time.Millisecond))
		if mode.backlog > 0 {
			t.Logf("%s, %d: the backlog count at a full backlog:\n%s", mode.name, size, bulkExplain(t, conn, mode.backlog))
		}

		// the embeddings in batches, each marking its documents ready, the builder ticking after
		start = time.Now()
		var ready time.Duration
		var serially bool
		tick := func() {
			err := a.Tick(ctx)
			if errors.Is(err, ErrBuiltSerially) {
				serially = true
			} else if err != nil {
				t.Fatalf("%s: the tick: %v", mode.name, err)
			}
			if ready == 0 && indexOf(t, a, model).State == annReady {
				ready = time.Since(start)
			}
		}
		tick()
		const batch = 20000
		for lo := 1; lo <= size; lo += batch {
			bulkExec(t, conn, `INSERT INTO rag_embedding (tenant, model, text_hash, vec)
				SELECT k.tenant, $1, k.text_hash, ('[' || array_to_string(ARRAY(SELECT c.v[j] + (random() - 0.5) * 0.3
				         FROM generate_series(1, `+strconv.Itoa(annDims)+`) j WHERE k.doc > 0), ',') || ']')::vector
				  FROM rag_chunk k JOIN rag_document d ON d.id = k.doc JOIN bulk_centers c ON c.i = k.doc % 40
				 WHERE d.tenant = 't' AND d.path BETWEEN $2 AND $3`, model, bulkPath(lo), bulkPath(lo+batch-1))
			bulkExec(t, conn, `UPDATE rag_document SET semantic_ready = true WHERE tenant = 't' AND path BETWEEN $1 AND $2`,
				bulkPath(lo), bulkPath(lo+batch-1))
			tick()
		}
		loaded := time.Since(start)
		for i := 0; ready == 0 && i < 3; i++ {
			tick()
		}
		t.Logf("%s, %d: embeddings loaded in %s (%.0f/s); index ready %s after the load began; built serially %v",
			mode.name, size, loaded.Round(time.Millisecond), float64(size)/loaded.Seconds(), ready.Round(time.Millisecond), serially)
		if mode.backlog > 0 {
			t.Logf("%s, %d: the backlog count at a drained backlog:\n%s", mode.name, size, bulkExplain(t, conn, mode.backlog))
		}
		bulkExec(t, conn, `ANALYZE rag_embedding`)
		exact, _ := New(conn, "t")
		approx, _ := New(conn, "t", WithANN(a))
		sum := 0.0
		qs := queryVecs(t, conn, "t", model, 100)
		for _, q := range qs {
			sum += recall(docsOf(semantic(t, approx, model, q, search.Filter{}, 10)), docsOf(semantic(t, exact, model, q, search.Filter{}, 10)))
		}
		t.Logf("%s, %d: recall@10 %.4f over %d queries", mode.name, size, sum/float64(len(qs)), len(qs))
	}
}

func bulkPath(g int) string {
	return "doc/" + strings.Repeat("0", max(0, 8-len(strconv.Itoa(g)))) + strconv.Itoa(g)
}

func bulkExec(t *testing.T, conn dao.DataConn, q string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", strings.Fields(q)[0], err)
	}
}

// bulkExplain is the plan of unreadyBacklog's count, analyzed.
func bulkExplain(t *testing.T, conn dao.DataConn, limit int) string {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), `EXPLAIN (ANALYZE, BUFFERS) SELECT count(*) FROM (SELECT 1 FROM rag_document
		WHERE tenant = $1 AND NOT semantic_ready LIMIT $2) backlog`, "t", limit)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		b.WriteString("    " + line + "\n")
	}
	return b.String()
}

package pgstore

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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
				}
				t.Logf("factor %d, %s, n=%d: recall %.4f (worst query %.2f), ann mean %s", f, tenant, n, sum/float64(len(qs)), worst, (took / time.Duration(len(qs))).Round(10*time.Microsecond))
			}
		}
	}
}

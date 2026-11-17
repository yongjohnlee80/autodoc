package pgstore

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search"
)

// TestMeasureASweep measures SweepEmbeddings at LabelManager's shape, AUTODOC_MEASURE_SWEEP=1 (a
// separate run): 56,000 documents of three chunks, a 1,024-d vector per chunk (168,000), tagged
// with one of 200 labels. It times a sweep that finds nothing (the common case), one of 1% orphans
// and one of 10% (a template change's refresh), and what a 10% sweep costs searches running at the
// same time: a label's exact search by meaning, p50 and p95, during the sweep against without.
func TestMeasureASweep(t *testing.T) {
	if os.Getenv("AUTODOC_MEASURE_SWEEP") != "1" {
		t.Skip("AUTODOC_MEASURE_SWEEP is not set: the measurement is a separate run")
	}
	const docs, chunks, dims = 56000, 3, 1024
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", nil)
	m := "test|sweep||" + strconv.Itoa(dims)
	if err := s.SetModels(ctx, m, ""); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	bulkExec(t, conn, `INSERT INTO rag_document (tenant, path, tags, semantic_ready)
		SELECT 't', 'doc/' || lpad(g::text, 8, '0'), ARRAY['l' || (g % 200)], true FROM generate_series(1, $1) g`, docs)
	bulkExec(t, conn, `INSERT INTO rag_chunk (tenant, doc, ord, body, text_hash)
		SELECT tenant, id, o, 'body ' || path || ' ' || o, sha256(convert_to(path || ' ' || o, 'UTF8'))
		  FROM rag_document, generate_series(0, $1 - 1) o WHERE tenant = 't'`, chunks)
	vec := `('[' || array_to_string(ARRAY(SELECT (random() - 0.5)::real FROM generate_series(1, ` + strconv.Itoa(dims) + `) WHERE g IS NOT NULL), ',') || ']')::vector`
	bulkExec(t, conn, `INSERT INTO rag_embedding (tenant, model, text_hash, vec)
		SELECT 't', $1, text_hash, `+vec+` FROM (SELECT DISTINCT text_hash, 1 AS g FROM rag_chunk WHERE tenant = 't') k`, m)
	bulkExec(t, conn, `VACUUM ANALYZE rag_document`)
	bulkExec(t, conn, `VACUUM ANALYZE rag_chunk`)
	bulkExec(t, conn, `VACUUM ANALYZE rag_embedding`)
	t.Logf("the corpus (%d documents, %d chunks and vectors) in %s", docs, docs*chunks, time.Since(start).Round(time.Millisecond))

	orphans := func(n, seed int) {
		bulkExec(t, conn, `INSERT INTO rag_embedding (tenant, model, text_hash, vec)
			SELECT 't', $1, sha256(convert_to('orphan ' || $3::text || ' ' || g, 'UTF8')), `+vec+` FROM generate_series(1, $2) g`, m, n, strconv.Itoa(seed))
	}
	timed := func(what string, want int) time.Duration {
		start := time.Now()
		n, err := s.SweepEmbeddings(ctx)
		took := time.Since(start)
		if err != nil || n != want {
			t.Fatalf("%s: swept %d, %v; want %d", what, n, err, want)
		}
		t.Logf("%s: %d swept in %s", what, n, took.Round(time.Millisecond))
		return took
	}
	for i := range 3 {
		timed("no orphans, run "+strconv.Itoa(i+1), 0)
	}
	orphans(docs*chunks/100, 1)
	timed("1% orphans", docs*chunks/100)

	qs := queryVecs(t, conn, "t", m, 50)
	label := search.Filter{Tags: []string{"l7"}}
	search1 := func(q []float32) time.Duration {
		start := time.Now()
		if len(semantic(t, s, m, q, label, 10)) == 0 {
			t.Fatal("a label's search found nothing")
		}
		return time.Since(start)
	}
	var base []time.Duration
	for range 4 {
		for _, q := range qs {
			base = append(base, search1(q))
		}
	}
	t.Logf("a label's exact search, alone: p50 %s, p95 %s over %d", pct(base, 50), pct(base, 95), len(base))
	var during []time.Duration
	for run := range 3 {
		orphans(docs*chunks/10, 10+run)
		bulkExec(t, conn, `ANALYZE rag_embedding`)
		done := make(chan time.Duration)
		go func() {
			start := time.Now()
			_, err := s.SweepEmbeddings(ctx)
			if err != nil {
				t.Error(err)
			}
			done <- time.Since(start)
		}()
	searching:
		for i := 0; ; i++ {
			select {
			case took := <-done:
				t.Logf("10%% orphans, run %d: swept in %s, %d searches during it", run+1, took.Round(time.Millisecond), i)
				break searching
			default:
			}
			during = append(during, search1(qs[i%len(qs)]))
		}
	}
	if len(during) > 0 {
		t.Logf("a label's exact search during a 10%% sweep: p50 %s, p95 %s over %d", pct(during, 50), pct(during, 95), len(during))
	}
}

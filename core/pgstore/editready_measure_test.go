package pgstore

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/searchtest"
)

// TestMeasureAnEditsReadiness measures what an edit's readiness costs a rewrite of every document,
// AUTODOC_MEASURE_EDIT=1 (a separate run): AUTODOC_MEASURE_EDIT_DOCS documents (default 20,000) of
// three chunks each, embedded and ready, written again whole in batches of 250 as a refresh writes
// a page, twice: once with the same texts (each edit stays ready), once with one text changed
// (each goes unready). Run on a tree without the readiness statement, it is the baseline.
func TestMeasureAnEditsReadiness(t *testing.T) {
	if os.Getenv("AUTODOC_MEASURE_EDIT") != "1" {
		t.Skip("AUTODOC_MEASURE_EDIT is not set: the measurement is a separate run")
	}
	n := 20000
	if v, err := strconv.Atoi(os.Getenv("AUTODOC_MEASURE_EDIT_DOCS")); err == nil && v > 0 {
		n = v
	}
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", nil)
	doc := func(d int, coda string) Document {
		var chunks []search.Chunk
		for i, body := range []string{fmt.Sprintf("track %d, its words", d), fmt.Sprintf("track %d, its credits", d), coda} {
			chunks = append(chunks, search.Chunk{Ord: i, Breadcrumb: "t", Body: body, Embed: body, ByteEnd: len(body)})
		}
		return Document{Path: fmt.Sprintf("track/%d.md", d), Facets: map[string][]string{"label_id": {"L" + strconv.Itoa(d%200)}}, Chunks: chunks}
	}
	load := func(what string, coda func(int) string) {
		start := time.Now()
		for lo := 0; lo < n; lo += 250 {
			b := s.Begin(ctx)
			for d := lo; d < min(lo+250, n); d++ {
				if _, err := b.Put(ctx, doc(d, coda(d))); err != nil {
					_ = b.Rollback()
					t.Fatal(err)
				}
			}
			if err := b.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		took := time.Since(start)
		unready, _ := s.rw.doc.DAO(dao.WithQueryContext(ctx)).With(dTenant, "t").With(dReady, false).Count()
		t.Logf("%s: %d documents in %s, %.0f documents/s; %d unready after", what, n, took.Round(time.Millisecond), float64(n)/took.Seconds(), unready)
	}
	same := func(d int) string { return fmt.Sprintf("track %d, its coda", d) }
	load("the first write", same)
	var vecs []Embedding
	for d := range n {
		for _, c := range doc(d, same(d)).Chunks {
			vecs = append(vecs, Embedding{TextHash: c.TextHash(), Vec: searchtest.Embed(c.Body)})
		}
		if len(vecs) >= 3000 || d == n-1 {
			if err := s.Embed(ctx, model, vecs); err != nil {
				t.Fatal(err)
			}
			vecs = vecs[:0]
		}
	}
	bulkExec(t, conn, `UPDATE rag_document SET semantic_ready = true WHERE tenant = 't'`)
	bulkExec(t, conn, `VACUUM ANALYZE rag_document`)
	bulkExec(t, conn, `VACUUM ANALYZE rag_chunk`)
	bulkExec(t, conn, `VACUUM ANALYZE rag_embedding`)
	load("an edit, same texts", same)
	load("an edit, one text new", func(d int) string { return fmt.Sprintf("track %d, a new coda", d) })
}

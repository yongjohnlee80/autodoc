package pgstore

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search"
)

// TestMeasureABulkLoadsGINCost measures what fastupdate = off on the document's GIN indexes costs a
// bulk load, AUTODOC_MEASURE_GIN=1 (a separate run): AUTODOC_MEASURE_GIN_DOCS documents (default
// 20,000) of LabelManager's facets (a label, a status, one to three territories, two tags) and a
// chunk each, written in batches of 250 as the ingester writes a page, with the option on and then
// off, each on a fresh database. With it on, the load leaves a pending list, whose merge (what a
// later VACUUM or MergePending does) is timed too.
func TestMeasureABulkLoadsGINCost(t *testing.T) {
	if os.Getenv("AUTODOC_MEASURE_GIN") != "1" {
		t.Skip("AUTODOC_MEASURE_GIN is not set: the measurement is a separate run")
	}
	n := 20000
	if v, err := strconv.Atoi(os.Getenv("AUTODOC_MEASURE_GIN_DOCS")); err == nil && v > 0 {
		n = v
	}
	ctx := context.Background()
	territories := []string{"US", "CA", "GB", "DE", "JP", "BR"}
	statuses := []string{"released", "scheduled", "draft"}
	for _, mode := range []string{"on", "off"} {
		conn := scratch(t)
		s := openCorpus(t, conn, "t", nil)
		for _, ix := range []string{"rag_document_facets", "rag_document_tags"} {
			exec(t, conn, "ALTER INDEX "+ix+" SET (fastupdate = "+mode+")")
		}
		start := time.Now()
		for lo := 0; lo < n; lo += 250 {
			b := s.Begin(ctx)
			for d := lo; d < min(lo+250, n); d++ {
				body := fmt.Sprintf("track %d of a catalogue, its words and its credits", d)
				doc := Document{Path: fmt.Sprintf("track/%d.md", d), Tags: []string{"track", "t" + strconv.Itoa(d%50)},
					Facets: map[string][]string{"label_id": {"L" + strconv.Itoa(d%200)}, "status": {statuses[d%3]},
						"territory": territories[d%4 : d%4+1+d%3]},
					Chunks: []search.Chunk{{Body: body, ByteEnd: len(body)}}}
				if _, err := b.Put(ctx, doc); err != nil {
					_ = b.Rollback()
					t.Fatal(err)
				}
			}
			if err := b.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		took := time.Since(start)
		pending := pendingPages(t, conn, "rag_document_facets") + pendingPages(t, conn, "rag_document_tags")
		mstart := time.Now()
		exec(t, conn, "SELECT gin_clean_pending_list('rag_document_facets'::regclass), gin_clean_pending_list('rag_document_tags'::regclass)")
		t.Logf("fastupdate %s: %d documents in %s, %.0f documents/s; the document indexes' pending pages after: %d, merged in %s",
			mode, n, took.Round(time.Millisecond), float64(n)/took.Seconds(), pending, time.Since(mstart).Round(time.Millisecond))
	}
}
